import { DbConnection } from "./generated/index";

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

function pickAccessor<T = any>(source: any, candidates: string[]): T {
  for (const candidate of candidates) {
    if (candidate && source && candidate in source) {
      return source[candidate] as T;
    }
  }
  throw new Error(`missing accessor: ${candidates.join(", ")}`);
}

function maybeAccessor<T = any>(source: any, candidates: string[]): T | null {
  for (const candidate of candidates) {
    if (candidate && source && candidate in source) {
      return source[candidate] as T;
    }
  }
  return null;
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

function rowExpiresAfter(row: any, nowMs: number): boolean {
  const expiresAt = Date.parse(String(row && (row.expiresAt || row.expires_at) || ""));
  return Number.isFinite(expiresAt) && expiresAt > nowMs;
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

  constructor(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    this.cfg = cfg;
    this.handlers = handlers || {};
  }

  connect(): void {
    this.disconnect(false);
    const generation = this.connectionGeneration + 1;
    this.connectionGeneration = generation;
    this.manuallyDisconnected = false;
    this.connected = false;
    this.handlers.onStatus?.("connecting");
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
        this.attachStateListeners(connection);
        this.subscribeState(connection);
        this.heartbeat(true);
      })
      .onDisconnect(() => {
        if (generation !== this.connectionGeneration) return;
        this.connected = false;
        this.conn = null;
        if (this.manuallyDisconnected) return;
        this.handlers.onStatus?.("reconnecting");
        this.scheduleReconnect();
      })
      .onConnectError((_ctx, error) => {
        if (generation !== this.connectionGeneration) return;
        this.connected = false;
        this.conn = null;
        this.handlers.onStatus?.("offline", error && String(error));
        this.scheduleReconnect();
      });
    this.conn = builder.build();
  }

  disconnect(markDisconnected = true): void {
    this.connectionGeneration += 1;
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
    if (this.conn) {
      try { this.conn.disconnect(); } catch (_) {}
      this.conn = null;
    }
  }

  close(): void {
    this.manuallyDisconnected = true;
    this.disconnect(true);
  }

  heartbeat(connected = true): void {
    if (!this.isReady()) return;
    const now = Date.now();
    if (connected && this.lastHeartbeatAt && now - this.lastHeartbeatAt < 30000) return;
    if (connected) this.lastHeartbeatAt = now;
    const reducer = this.reducer("memberSetStreamFocus");
    Promise.resolve(reducer({
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      active: connected,
      reason: connected ? "browser_heartbeat" : "browser_disconnect",
    })).catch((error) => this.handlers.onStatus?.("heartbeat_failed", error && String(error)));
  }

  disconnectPresence(): void {
    if (!this.isReady()) return;
    Promise.resolve(this.reducer("memberSetStreamFocus")({
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      active: false,
      reason: "browser_disconnect",
    })).catch(() => {});
  }

  setStreamFocus(active: boolean, reason: string): Promise<void> {
    return this.callReducer("memberSetStreamFocus", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      active,
      reason,
    });
  }

  requestKeyframe(reason: string): Promise<void> {
    return this.callReducer("memberRequestKeyframe", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      reason,
    });
  }

  recoverStream(reason: string): Promise<void> {
    return this.callReducer("memberRecoverStream", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      reason,
    });
  }

  prepareControlCode(reason: string): Promise<void> {
    return this.callReducer("memberPrepareControlCode", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      reason,
    });
  }

  requestControlCode(digits: string): Promise<void> {
    return this.callReducer("memberRequestControlCode", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      digits,
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

  appendSafeLog(level: string, event: string, detailJson: string, correlationId = ""): Promise<void> {
    return this.callReducer("memberAppendSafeOperationalLog", {
      ticketId: this.cfg.ticketId,
      level,
      event,
      correlationId,
      detailJson,
    });
  }

  upsertMember(email: string, role: string): Promise<void> {
    return this.callReducer("memberUpsertMember", {
      ticketId: this.cfg.ticketId,
      email,
      role,
    });
  }

  removeMember(email: string): Promise<void> {
    return this.callReducer("memberRemoveMember", {
      ticketId: this.cfg.ticketId,
      email,
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
    const ownerPublicId = sqlString(accountPublicId(this.cfg.email));
    this.subscription = connection.subscriptionBuilder()
      .onApplied(() => this.publishFocusedState())
      .subscribe([
        `SELECT * FROM ticketremote_ticket_summary WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_viewer_public WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_phone_status WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_stream_desired_state WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_phone_current_report WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_relay_current_report WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_control_code_request WHERE ticketId = ${ticket} AND ownerPublicId = ${ownerPublicId}`,
      ]);
  }

  private publishFocusedState(): void {
    if (!this.isReady()) return;
    const db = this.requireConnection().db;
    const nowMs = Date.now();
    const summary = tableRows(this.ticketSummaryTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const viewers = tableRows(this.viewerPublicTable(db))
      .filter((row) => rowTicketId(row) === this.cfg.ticketId && row.connected !== false && rowExpiresAfter(row, nowMs))
      .sort((a, b) => String(a.publicId || a.public_id || "").localeCompare(String(b.publicId || b.public_id || "")));
    const phone = tableRows(this.phoneStatusTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const desired = tableRows(this.streamDesiredStateTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const phoneReport = tableRows(this.phoneCurrentReportTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const relayReport = tableRows(this.relayCurrentReportTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const ownerPublicId = accountPublicId(this.cfg.email);
    const controlCodeRequests = tableRows(this.controlCodeRequestTable(db))
      .filter((row) => rowTicketId(row) === this.cfg.ticketId && String(row.ownerPublicId || row.owner_public_id || "") === ownerPublicId)
      .sort((a, b) => String(b.updatedAt || b.updated_at || "").localeCompare(String(a.updatedAt || a.updated_at || "")));
    const updatedAt = String(
      summary && (summary.updatedAt || summary.updated_at) ||
      relayReport && (relayReport.updatedAt || relayReport.updated_at) ||
      phoneReport && (phoneReport.updatedAt || phoneReport.updated_at) ||
      phone && (phone.updatedAt || phone.updated_at || phone.lastSeenAt || phone.last_seen_at) ||
      new Date().toISOString()
    );
    const phoneBackendId = String(phone && (phone.backendId || phone.backend_id || phone.id) || summary && (summary.phoneBackendId || summary.phone_backend_id) || "");
    const phoneAttachName = String(phone && (phone.attachName || phone.attach_name) || summary && (summary.phoneAttachName || summary.phone_attach_name) || phoneBackendId);
    const phoneDesiredState = String(phone && (phone.desiredState || phone.desired_state) || summary && (summary.phoneDesiredState || summary.phone_desired_state) || "");
    const phoneLastSeenAt = String(phone && (phone.lastSeenAt || phone.last_seen_at) || summary && (summary.phoneLastSeenAt || summary.phone_last_seen_at) || "");
    this.handlers.onState?.({
      ticket: {
        id: this.cfg.ticketId,
        displayName: String(summary && (summary.displayName || summary.display_name) || "ViVi timed ticket"),
        updatedAt,
      },
      viewerCount: viewers.length,
      viewerPresence: viewers.map((viewer) => ({
        publicId: String(viewer.publicId || viewer.public_id || ""),
        label: String(viewer.label || viewer.publicId || viewer.public_id || ""),
      })),
      activeControl: null,
      phone: phoneBackendId ? {
        id: phoneBackendId,
        attachName: phoneAttachName,
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
      relayCurrentReport: relayReport ? {
        backendId: String(relayReport.backendId || relayReport.backend_id || ""),
        videoClients: Number(relayReport.videoClients ?? relayReport.video_clients ?? 0),
        streamVerdict: String(relayReport.streamVerdict || relayReport.stream_verdict || ""),
        lastFrameAgoMillis: Number(relayReport.lastFrameAgoMillis ?? relayReport.last_frame_ago_millis ?? 0),
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
    return [
      this.ticketSummaryTable(source),
      this.viewerPublicTable(source),
      this.phoneStatusTable(source),
      this.streamDesiredStateTable(source),
      this.phoneCurrentReportTable(source),
      this.relayCurrentReportTable(source),
      this.controlCodeRequestTable(source),
    ];
  }

  private ticketSummaryTable(source: any): any {
    return pickAccessor(source, [
      "ticketremoteTicketSummary",
      "ticketRemoteTicketSummary",
      "ticketremote_ticket_summary",
    ]);
  }

  private viewerPublicTable(source: any): any {
    return pickAccessor(source, [
      "ticketremoteViewerPublic",
      "ticketRemoteViewerPublic",
      "ticketremote_viewer_public",
    ]);
  }

  private phoneStatusTable(source: any): any {
    return pickAccessor(source, [
      "ticketremotePhoneStatus",
      "ticketRemotePhoneStatus",
      "ticketremote_phone_status",
    ]);
  }

  private streamDesiredStateTable(source: any): any {
    return pickAccessor(source, [
      "ticketremoteStreamDesiredState",
      "ticketRemoteStreamDesiredState",
      "ticketremote_stream_desired_state",
    ]);
  }

  private phoneCurrentReportTable(source: any): any {
    return pickAccessor(source, [
      "ticketremotePhoneCurrentReport",
      "ticketRemotePhoneCurrentReport",
      "ticketremote_phone_current_report",
    ]);
  }

  private relayCurrentReportTable(source: any): any {
    return pickAccessor(source, [
      "ticketremoteRelayCurrentReport",
      "ticketRemoteRelayCurrentReport",
      "ticketremote_relay_current_report",
    ]);
  }

  private controlCodeRequestTable(source: any): any {
    return pickAccessor(source, [
      "ticketremoteControlCodeRequest",
      "ticketRemoteControlCodeRequest",
      "ticketremote_control_code_request",
    ]);
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
    const reducer = this.reducer(name);
    await reducer(args);
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
}

(window as any).TicketSpacetime = {
  create(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    return new TicketSpacetimeClient(cfg, handlers);
  },
};
