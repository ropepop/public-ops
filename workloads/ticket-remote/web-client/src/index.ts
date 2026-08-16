import { DbConnection } from "./generated/index";
import { installCspSafeSpacetimeCodecs } from "./csp-safe-codecs";

installCspSafeSpacetimeCodecs();

type TicketClientConfig = {
  host: string;
  database: string;
  token: string;
  ticketId: string;
  sessionId: string;
  email: string;
};

type TicketClientHandlers = {
  onState?: (state: any) => void;
  onStatus?: (status: string, detail?: string) => void;
};

const STREAM_FOCUS_REFRESH_MS = 30000;

function pickAccessor<T = any>(source: any, candidates: string[]): T {
  for (const candidate of candidates) {
    if (candidate && source && candidate in source) {
      return source[candidate] as T;
    }
  }
  throw new Error(`missing accessor: ${candidates.join(", ")}`);
}

function tableAccessor(source: any, name: string): any {
  const title = name.split("_").map((part) => part[0].toUpperCase() + part.slice(1)).join("");
  return pickAccessor(source, [`ticketremote${title}`, `ticketRemote${title}`, `ticketremote_${name}`]);
}

function sqlString(value: string): string {
  return `'${String(value || "").replace(/'/g, "''")}'`;
}

function accountPublicId(email: string): string {
  const normalized = String(email || "").trim().toLowerCase();
  let hash = 2166136261 >>> 0;
  for (let i = 0; i < normalized.length; i += 1) {
    hash ^= normalized.charCodeAt(i) & 0xff;
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  return hash.toString(36).padStart(4, "0").slice(0, 4);
}

function tableRows(table: any): any[] {
  return Array.from(table && table.iter ? table.iter() : []) as any[];
}

function rowTicketId(row: any): string {
  return String(row && (row.ticketId || row.ticket_id) || "");
}

function rowBackendId(row: any): string {
  return String(row && (row.backendId || row.backend_id) || "");
}

function rowId(row: any): string {
  return String(row && row.id || "");
}

function rowTime(row: any, field: string, snakeField: string): number {
  const value = String(row && (row[field] || row[snakeField]) || "").trim();
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function activeViewerFocusRows(rows: any[], ticketId: string, backendId: string): any[] {
  const now = Date.now();
  return rows
    .filter((row) => rowTicketId(row) === ticketId && rowBackendId(row) === backendId)
    .filter((row) => (row.active ?? true) !== false)
    .filter((row) => String(row.publicId || row.public_id || "").trim())
    .filter((row) => {
      const expiresAt = rowTime(row, "expiresAt", "expires_at");
      return !expiresAt || expiresAt > now;
    })
    .sort((left, right) => {
      const publicSort = String(left.publicId || left.public_id || "").localeCompare(String(right.publicId || right.public_id || ""));
      if (publicSort) return publicSort;
      return rowId(left).localeCompare(rowId(right));
    });
}

function ageMillisFromTimestamp(value: any): number {
  const text = String(value || "").trim();
  if (!text) return 0;
  const at = Date.parse(text);
  if (!Number.isFinite(at)) return 0;
  return Math.max(0, Date.now() - at);
}

class TicketSpacetimeClient {
  private cfg: TicketClientConfig;
  private handlers: TicketClientHandlers;
  private conn: DbConnection | null = null;
  private subscription: { unsubscribe: () => void } | null = null;
  private reconnectTimer = 0;
  private reconnectDelayMs = 1000;
  private connected = false;
  private connectionGeneration = 0;
  private manuallyDisconnected = false;
  private lastHeartbeatAt = 0;
  private lastStreamFocusActive: boolean | null = null;
  private viewerPresenceExpiryTimer = 0;
  private livePromise: Promise<void> | null = null;
  private resolveLivePromise: (() => void) | null = null;
  private rejectLivePromise: ((error: Error) => void) | null = null;

  constructor(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    this.cfg = cfg;
    this.handlers = handlers || {};
  }

  connect(): void {
    this.disconnect(false);
    const generation = this.connectionGeneration + 1;
    this.connectionGeneration = generation;
    this.manuallyDisconnected = false;
    this.createLivePromise();
    this.connected = false;
    this.handlers.onStatus?.("connecting");
    try {
      const builder = DbConnection.builder()
        .withUri(this.websocketURL())
        .withDatabaseName(this.cfg.database)
        .withToken(this.cfg.token)
        .onConnect((connection) => {
          if (generation !== this.connectionGeneration) {
            try { connection.disconnect(); } catch (_) {}
            return;
          }
          this.conn = connection;
          this.connected = true;
          this.reconnectDelayMs = 1000;
          this.handlers.onStatus?.("live");
          this.resolveLive();
          this.subscribeState(connection);
        })
        .onDisconnect(() => {
          if (generation !== this.connectionGeneration) return;
          this.connected = false;
          this.conn = null;
          this.rejectLive(new Error("Spacetime connection disconnected"));
          if (this.manuallyDisconnected) return;
          this.handlers.onStatus?.("reconnecting");
          this.scheduleReconnect();
        })
        .onConnectError((_ctx, error) => {
          if (generation !== this.connectionGeneration) return;
          this.connected = false;
          this.conn = null;
          this.rejectLive(new Error(error && String(error) || "Spacetime connection failed"));
          this.handlers.onStatus?.("offline", error && String(error));
          this.scheduleReconnect();
        });
      this.conn = builder.build();
    } catch (error) {
      if (generation !== this.connectionGeneration) return;
      this.connected = false;
      this.conn = null;
      const connectionError = error instanceof Error ? error : new Error(String(error || "Spacetime connection failed"));
      this.handlers.onStatus?.("offline", connectionError.message);
      this.rejectLive(connectionError);
      if (!this.manuallyDisconnected) this.scheduleReconnect();
    }
  }

  disconnect(markDisconnected = true): void {
    this.connectionGeneration += 1;
    this.rejectLive(new Error("Spacetime connection stopped"));
    if (this.reconnectTimer) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = 0;
    }
    if (markDisconnected && this.conn) {
      this.heartbeat(false);
    }
    this.connected = false;
    if (this.subscription) {
      try { this.subscription.unsubscribe(); } catch (_) {}
      this.subscription = null;
    }
    this.clearViewerPresenceExpiryTimer();
    if (this.conn) {
      try { this.conn.disconnect(); } catch (_) {}
      this.conn = null;
    }
  }

  close(): void {
    this.manuallyDisconnected = true;
    this.rejectLive(new Error("Spacetime connection closed"));
    this.disconnect(true);
  }

  heartbeat(connected = true, reason = ""): void {
    if (!this.isReady()) return;
    const active = Boolean(connected);
    const now = Date.now();
    if (this.lastStreamFocusActive === active) {
      if (!active || now - this.lastHeartbeatAt < STREAM_FOCUS_REFRESH_MS) return;
    }
    this.lastStreamFocusActive = active;
    this.lastHeartbeatAt = now;
    const reducer = this.reducer("memberSetStreamFocus");
    Promise.resolve(reducer({
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      active,
      reason: reason || (active ? "browser_stream_heartbeat" : "browser_no_stream_heartbeat"),
    })).catch((error) => {
      this.lastStreamFocusActive = null;
      this.handlers.onStatus?.("heartbeat_failed", error && String(error));
    });
  }

  disconnectPresence(): void {
    if (!this.isReady()) return;
    this.setStreamFocus(false, "browser_disconnect").catch(() => {});
  }

  setStreamFocus(active: boolean, reason: string): Promise<void> {
    const nextActive = Boolean(active);
    if (this.lastStreamFocusActive === nextActive) {
      return Promise.resolve();
    }
    this.lastStreamFocusActive = nextActive;
    return this.callReducer("memberSetStreamFocus", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      active: nextActive,
      reason,
    }).catch((error) => {
      this.lastStreamFocusActive = null;
      throw error;
    });
  }

  requestKeyframe(reason: string): Promise<void> {
    return this.streamAction("memberRequestKeyframe", reason);
  }

  recoverStream(reason: string): Promise<void> {
    return this.streamAction("memberRecoverStream", reason);
  }

  requestControlCode(digits: string, expectedFastRevision = ""): Promise<void> {
    return this.callReducer("memberRequestControlCode", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      digits,
      expectedFastRevision,
    });
  }

  confirmControlCodeBrowserCapture(requestId: string, candidateFrameEpoch: unknown, candidateFrameSequence: unknown, acceptedReason: string): Promise<void> {
    return this.callReducer("memberConfirmControlCodeBrowserCapture", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      requestId,
      candidateFrameEpoch: String(candidateFrameEpoch || "0"),
      candidateFrameSequence: String(candidateFrameSequence || "0"),
      acceptedReason,
    });
  }

  closeControlCode(requestId: string, reason: string): Promise<void> {
    return this.callReducer("memberCloseControlCode", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      requestId,
      reason,
    });
  }

  private websocketURL(): URL {
    const base = new URL(this.cfg.host);
    base.protocol = base.protocol === "https:" ? "wss:" : "ws:";
    return base;
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return;
    const delayMs = this.reconnectDelayMs;
    this.reconnectDelayMs = Math.min(this.reconnectDelayMs * 2, 60000);
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = 0;
      this.connect();
    }, delayMs);
  }

  private attachStateListeners(connection: DbConnection): void {
    const publish = () => this.publishFocusedState();
    for (const table of this.focusedStateTables(connection.db)) {
      if (table.onInsert) table.onInsert(publish);
      if (table.onUpdate) table.onUpdate(publish);
      if (table.onDelete) table.onDelete(publish);
    }
  }

  private subscribeState(connection: DbConnection): void {
    const ticket = sqlString(this.cfg.ticketId);
    const backendRow = sqlString(`${this.cfg.ticketId}:${this.backendId()}`);
    const backendId = sqlString(this.backendId());
    const ownerPublicId = sqlString(accountPublicId(this.cfg.email));
    let applied = false;
    this.subscription = connection.subscriptionBuilder()
      .onApplied(() => {
        if (!applied) {
          applied = true;
          this.attachStateListeners(connection);
        }
        this.publishFocusedState();
      })
      .subscribe([
        `SELECT * FROM ticketremote_stream_desired_state WHERE id = ${backendRow}`,
        `SELECT * FROM ticketremote_phone_current_report WHERE id = ${backendRow}`,
        `SELECT * FROM ticketremote_control_code_fast_state WHERE id = ${backendRow}`,
        `SELECT * FROM ticketremote_relay_current_report WHERE id = ${backendRow}`,
        `SELECT * FROM ticketremote_stream_viewer_focus WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
        `SELECT * FROM ticketremote_control_code_request WHERE ticketId = ${ticket} AND ownerPublicId = ${ownerPublicId}`,
      ]);
  }

  private publishFocusedState(): void {
    if (!this.isReady()) return;
    const db = this.requireConnection().db;
    const backendRow = `${this.cfg.ticketId}:${this.backendId()}`;
    const desired = tableRows(tableAccessor(db, "stream_desired_state"))
      .find((row) => rowId(row) === backendRow) || null;
    const phoneReport = tableRows(tableAccessor(db, "phone_current_report"))
      .find((row) => rowId(row) === backendRow) || null;
    const controlCodeFastState = tableRows(tableAccessor(db, "control_code_fast_state"))
      .find((row) => rowId(row) === backendRow) || null;
    const relayReport = tableRows(tableAccessor(db, "relay_current_report"))
      .find((row) => rowId(row) === backendRow) || null;
    const viewerFocusRows = activeViewerFocusRows(
      tableRows(tableAccessor(db, "stream_viewer_focus")),
      this.cfg.ticketId,
      this.backendId()
    );
    this.scheduleViewerPresenceExpiry(viewerFocusRows);
    const viewerPresence = viewerFocusRows.map((row) => {
      const publicId = String(row.publicId || row.public_id || "").trim();
      return {
        publicId,
        label: publicId,
        connected: true,
        lastSeenAt: String(row.lastSeenAt || row.last_seen_at || ""),
        expiresAt: String(row.expiresAt || row.expires_at || ""),
      };
    });
    const ownerPublicId = accountPublicId(this.cfg.email);
    const controlCodeRequests = tableRows(tableAccessor(db, "control_code_request"))
      .filter((row) => rowTicketId(row) === this.cfg.ticketId && String(row.ownerPublicId || row.owner_public_id || "") === ownerPublicId)
      .sort((a, b) => String(b.updatedAt || b.updated_at || "").localeCompare(String(a.updatedAt || a.updated_at || "")));
    const updatedAt = String(
      relayReport && (relayReport.updatedAt || relayReport.updated_at) ||
      phoneReport && (phoneReport.updatedAt || phoneReport.updated_at) ||
      new Date().toISOString()
    );
    const phoneBackendId = String(
      phoneReport && (phoneReport.backendId || phoneReport.backend_id) ||
      relayReport && (relayReport.backendId || relayReport.backend_id) ||
      "pixel"
    );
    const phoneDesiredState = String(desired && (desired.desiredActive ?? desired.desired_active) ? "streaming" : "idle");
    const phoneLastSeenAt = String(phoneReport && (phoneReport.updatedAt || phoneReport.updated_at) || "");
    const reportedViewerCount = Number(desired && (desired.viewerCount ?? desired.viewer_count) || relayReport && (relayReport.videoClients ?? relayReport.video_clients) || 0);
    const viewerCount = Math.max(Number.isFinite(reportedViewerCount) ? reportedViewerCount : 0, viewerPresence.length);
    this.handlers.onState?.({
      ticket: {
        id: this.cfg.ticketId,
        displayName: "ViVi timed ticket",
        updatedAt,
      },
      viewerCount,
      viewerPresence,
      activeControl: null,
      phone: phoneBackendId ? {
        id: phoneBackendId,
        attachName: phoneBackendId,
        desiredState: phoneDesiredState,
        lastSeenAt: phoneLastSeenAt,
      } : null,
      streamDesired: desired ? {
        backendId: String(desired.backendId || desired.backend_id || ""),
        desiredActive: desired.desiredActive ?? desired.desired_active === true,
        viewerCount: Number(desired.viewerCount ?? desired.viewer_count ?? 0),
        reason: String(desired.reason || ""),
        revision: String(desired.revision || ""),
        updatedAt: String(desired.updatedAt || desired.updated_at || ""),
      } : null,
      phoneCurrentReport: phoneReport ? {
        backendId: String(phoneReport.backendId || phoneReport.backend_id || ""),
        streamState: String(phoneReport.streamState || phoneReport.stream_state || ""),
        desiredActive: phoneReport.desiredActive ?? phoneReport.desired_active === true,
        lastCommandId: String(phoneReport.lastCommandId || phoneReport.last_command_id || ""),
        lastCommandRevision: String(phoneReport.lastCommandRevision || phoneReport.last_command_revision || ""),
        statusJson: String(phoneReport.statusJson || phoneReport.status_json || "{}"),
        updatedAt: String(phoneReport.updatedAt || phoneReport.updated_at || ""),
      } : null,
      controlCodeFastState: controlCodeFastState ? {
        backendId: String(controlCodeFastState.backendId || controlCodeFastState.backend_id || ""),
        status: String(controlCodeFastState.status || ""),
        revision: String(controlCodeFastState.revision || ""),
        reason: String(controlCodeFastState.reason || ""),
        preparedAt: String(controlCodeFastState.preparedAt || controlCodeFastState.prepared_at || ""),
        expiresAt: String(controlCodeFastState.expiresAt || controlCodeFastState.expires_at || ""),
        streamEpoch: String(controlCodeFastState.streamEpoch || controlCodeFastState.stream_epoch || "0"),
        frameSequence: String(controlCodeFastState.frameSequence || controlCodeFastState.frame_sequence || "0"),
        rawTicketConfirmed: controlCodeFastState.rawTicketConfirmed ?? controlCodeFastState.raw_ticket_confirmed === true,
        cleanupClear: controlCodeFastState.cleanupClear ?? controlCodeFastState.cleanup_clear === true,
        streamLive: controlCodeFastState.streamLive ?? controlCodeFastState.stream_live === true,
        updatedAt: String(controlCodeFastState.updatedAt || controlCodeFastState.updated_at || ""),
      } : null,
      relayCurrentReport: relayReport ? {
        backendId: String(relayReport.backendId || relayReport.backend_id || ""),
        videoClients: Number(relayReport.videoClients ?? relayReport.video_clients ?? 0),
        streamVerdict: String(relayReport.streamVerdict || relayReport.stream_verdict || ""),
        lastFrameAt: String(relayReport.lastFrameAt || relayReport.last_frame_at || ""),
        lastFrameAgoMillis: Number(relayReport.lastFrameAgoMillis ?? relayReport.last_frame_ago_millis ?? ageMillisFromTimestamp(relayReport.lastFrameAt || relayReport.last_frame_at)),
        framesForwarded: String(relayReport.framesForwarded || relayReport.frames_forwarded || "0"),
        statusJson: String(relayReport.statusJson || relayReport.status_json || "{}"),
        updatedAt: String(relayReport.updatedAt || relayReport.updated_at || ""),
      } : null,
      controlCodeRequests: controlCodeRequests.map((request) => ({
        requestId: String(request.id || request.requestId || request.request_id || ""),
        ownerPublicId: String(request.ownerPublicId || request.owner_public_id || ""),
        status: String(request.status || ""),
        reason: String(request.reason || ""),
        message: String(request.message || ""),
        requestedAt: String(request.requestedAt || request.requested_at || ""),
        updatedAt: String(request.updatedAt || request.updated_at || ""),
        expiresAt: String(request.expiresAt || request.expires_at || ""),
        resultExpiresAt: String(request.resultExpiresAt || request.result_expires_at || ""),
        resultProof: String(request.resultProof || request.result_proof || ""),
        resultProofAt: String(request.resultProofAt || request.result_proof_at || ""),
        captureRequired: request.captureRequired ?? request.capture_required === true,
        captureAcknowledged: request.captureAcknowledged ?? request.capture_acknowledged === true,
        cleanupPending: request.cleanupPending ?? request.cleanup_pending === true,
        streamEpoch: String(request.streamEpoch || request.stream_epoch || "0"),
        frameSequence: String(request.frameSequence || request.frame_sequence || "0"),
        minFrameSequence: String(request.minFrameSequence || request.min_frame_sequence || "0"),
        resultFrameEpoch: String(request.resultFrameEpoch || request.result_frame_epoch || "0"),
        resultMinFrameSequence: String(request.resultMinFrameSequence || request.result_min_frame_sequence || "0"),
        captureFrameEpoch: String(request.captureFrameEpoch || request.capture_frame_epoch || "0"),
        captureFrameSequence: String(request.captureFrameSequence || request.capture_frame_sequence || "0"),
      })),
      serverTime: updatedAt,
      stateBackend: "spacetime",
    });
  }

  private focusedStateTables(source: any): any[] {
    return ["stream_desired_state", "phone_current_report", "control_code_fast_state", "relay_current_report", "stream_viewer_focus", "control_code_request"]
      .map((name) => tableAccessor(source, name));
  }

  private backendId(): string {
    return "pixel";
  }

  private reducer(name: string): any {
    const connection = this.requireConnection();
    const suffix = `${name.charAt(0).toUpperCase()}${name.slice(1)}`;
    return pickAccessor(connection.reducers, [`ticketremote${suffix}`, `ticketRemote${suffix}`, name]);
  }

  private async callReducer(name: string, args: Record<string, unknown>): Promise<void> {
    await this.whenLive(2000);
    const reducer = this.reducer(name);
    await reducer(args);
  }

  private streamAction(name: string, reason: string): Promise<void> {
    return this.callReducer(name, { ticketId: this.cfg.ticketId, backendId: this.backendId(), reason });
  }

  private requireConnection(): DbConnection {
    if (!this.isReady() || !this.conn) {
      throw new Error("Spacetime connection is not ready");
    }
    return this.conn;
  }

  private isReady(): boolean {
    return Boolean(this.conn && this.connected);
  }

  private scheduleViewerPresenceExpiry(rows: any[]): void {
    this.clearViewerPresenceExpiryTimer();
    let nearest = 0;
    for (const row of rows) {
      const expiresAt = rowTime(row, "expiresAt", "expires_at");
      if (expiresAt > Date.now() && (!nearest || expiresAt < nearest)) {
        nearest = expiresAt;
      }
    }
    if (!nearest) return;
    const delayMs = Math.max(250, nearest - Date.now() + 250);
    this.viewerPresenceExpiryTimer = window.setTimeout(() => {
      this.viewerPresenceExpiryTimer = 0;
      this.publishFocusedState();
    }, delayMs);
  }

  private clearViewerPresenceExpiryTimer(): void {
    if (!this.viewerPresenceExpiryTimer) return;
    window.clearTimeout(this.viewerPresenceExpiryTimer);
    this.viewerPresenceExpiryTimer = 0;
  }

  private createLivePromise(): void {
    this.livePromise = new Promise((resolve, reject) => {
      this.resolveLivePromise = resolve;
      this.rejectLivePromise = reject;
    });
    // The generation promise is also the admission gate. Keep a passive
    // rejection handler attached so a cold page that has not submitted yet
    // cannot produce an unhandled browser rejection.
    void this.livePromise.catch(() => undefined);
  }

  private resolveLive(): void {
    const resolve = this.resolveLivePromise;
    this.resolveLivePromise = null;
    this.rejectLivePromise = null;
    resolve?.();
  }

  private rejectLive(error: Error): void {
    const reject = this.rejectLivePromise;
    this.resolveLivePromise = null;
    this.rejectLivePromise = null;
    reject?.(error);
  }

  private whenLive(timeoutMs: number): Promise<void> {
    if (this.isReady()) return Promise.resolve();
    const livePromise = this.livePromise;
    if (!livePromise) {
      return Promise.reject(new Error("Spacetime connection is not starting"));
    }
    return new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(() => {
        reject(new Error("Spacetime connection is not ready"));
      }, Math.max(1, timeoutMs));
      livePromise.then(
        () => {
          window.clearTimeout(timer);
          resolve();
        },
        (error) => {
          window.clearTimeout(timer);
          reject(error);
        },
      );
    });
  }
}

(window as any).TicketSpacetime = {
  create(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    return new TicketSpacetimeClient(cfg, handlers);
  },
};
