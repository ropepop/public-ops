import { DbConnection } from "./generated/index";
import { installCspSafeSpacetimeCodecs } from "./csp-safe-codecs";
import { ticketActionV3ActionsByAuthority } from "../ticket-action-v3-core.mjs";
import {
  ownerViviConnectionEventAllowed,
  prepareOwnerViviAccessBeforeSubscribe,
} from "../owner-vivi-access-core.js";

installCspSafeSpacetimeCodecs();

type TicketClientConfig = {
  host: string;
  database: string;
  token: string;
  ticketId: string;
  sessionId: string;
  email: string;
  accountScopeId: string;
  backendId?: string;
  ownerViviAuth?: boolean;
};

type TicketClientHandlers = {
  onState?: (state: any) => void;
  onStatus?: (status: string, detail?: string) => void;
  onSnapshotApplied?: () => void;
};

export type TicketHDREngine = "client_webgpu_v2";
export const TICKET_HDR_DISPLAY_BOOSTS = [2, 3, 4, 5, 6] as const;
export type TicketHDRDisplayBoost = typeof TICKET_HDR_DISPLAY_BOOSTS[number];

function ticketHDREngine(value: unknown): TicketHDREngine {
  return "client_webgpu_v2";
}

function ticketHDRDisplayBoost(value: unknown): TicketHDRDisplayBoost {
  const boost = Number(value);
  return TICKET_HDR_DISPLAY_BOOSTS.includes(boost as TicketHDRDisplayBoost)
    ? boost as TicketHDRDisplayBoost
    : 4;
}

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

function validAccountScopeId(value: string): string {
  const normalized = String(value || "").trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(normalized)) {
    throw new Error("account scope is unavailable");
  }
  return normalized;
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
  private latestActivationDecisions: any[] = [];
  private activationDecisionWaiters = new Map<string, {
    resolve: (decision: any) => void;
    reject: (error: Error) => void;
    timer: number;
  }>();

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
          void prepareOwnerViviAccessBeforeSubscribe({
            ownerViviAuth: this.cfg.ownerViviAuth === true,
            prepare: () => this.callReducerOnConnection(connection, "ownerPrepareViviCredentials", {
              ticketId: this.cfg.ticketId,
              backendId: this.backendId(),
            }),
            subscribe: () => {
              if (generation !== this.connectionGeneration || this.conn !== connection) return;
              this.subscribeState(connection, generation);
            },
            ready: () => {
              if (generation !== this.connectionGeneration || this.conn !== connection) return;
              this.handlers.onStatus?.("live");
              this.resolveLive();
            },
          }).catch((error) => {
            if (generation !== this.connectionGeneration || this.conn !== connection) return;
            this.handlers.onStatus?.("owner_vivi_access_failed", error && String(error));
          });
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

  refresh(): void {
    if (this.manuallyDisconnected) return;
    this.connect();
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
    for (const waiter of this.activationDecisionWaiters.values()) {
      window.clearTimeout(waiter.timer);
      waiter.reject(new Error("Spacetime connection stopped"));
    }
    this.activationDecisionWaiters.clear();
    this.latestActivationDecisions = [];
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

  recordActivityTick(): Promise<void> {
    return this.callReducer("memberRecordActivityTick", {
      ticketId: this.cfg.ticketId,
    });
  }

  setLimitPreference(obeyLimits: boolean): Promise<void> {
    return this.callReducer("memberSetLimitPreference", {
      ticketId: this.cfg.ticketId,
      obeyLimits: Boolean(obeyLimits),
    });
  }

  saveViviCredentials(email: string, password: string, expectedRevision: string, revision: string): Promise<void> {
    return this.callReducer("ownerSaveViviCredentials", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      email,
      password,
      expectedRevision,
      revision,
    });
  }

  clearViviCredentials(expectedRevision: string, revision: string): Promise<void> {
    return this.callReducer("ownerClearViviCredentials", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      expectedRevision,
      revision,
    });
  }

  requestViviReauth(requestId: string, credentialRevision: string): Promise<void> {
    return this.callReducer("ownerRequestViviReauth", {
      version: 1,
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      requestId,
      credentialRevision,
    });
  }

  requestViviReauthLogoutLogin(
    requestId: string,
    credentialRevision: string,
    redetectAfterLogin = false,
  ): Promise<void> {
    return this.callReducer("ownerRequestViviReauthLogoutLogin", {
      version: redetectAfterLogin ? 4 : 3,
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      requestId,
      credentialRevision,
    });
  }

  requestViviReauthFullReset(requestId: string, credentialRevision: string): Promise<void> {
    return this.callReducer("ownerRequestViviReauthFullReset", {
      version: 2,
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      requestId,
      credentialRevision,
    });
  }

  setHDRPreference(enabled: boolean): Promise<void> {
    return this.callReducer("memberSetHdrPreference", {
      ticketId: this.cfg.ticketId,
      enabled: Boolean(enabled),
    });
  }

  refreshHDRState(): Promise<void> {
    return this.callReducer("memberRefreshHdrState", {
      ticketId: this.cfg.ticketId,
    });
  }

  setHDREngine(engine: TicketHDREngine): Promise<void> {
    return this.callReducer("ownerSetHdrEngine", {
      ticketId: this.cfg.ticketId,
      engine: ticketHDREngine(engine),
    });
  }

  refreshHDREngineState(): Promise<void> {
    return this.callReducer("memberRefreshHdrEngineState", {
      ticketId: this.cfg.ticketId,
    });
  }

  setHDRDisplayBoost(selectedDisplayBoost: TicketHDRDisplayBoost): Promise<void> {
    return this.callReducer("ownerSetHdrDisplayBoost", {
      ticketId: this.cfg.ticketId,
      selectedDisplayBoost: ticketHDRDisplayBoost(selectedDisplayBoost),
    });
  }

  refreshHDRBoostState(): Promise<void> {
    return this.callReducer("memberRefreshHdrBoostState", {
      ticketId: this.cfg.ticketId,
    });
  }

  refreshLimitState(): Promise<void> {
    return this.callReducer("memberRefreshLimitState", {
      ticketId: this.cfg.ticketId,
    });
  }

  requestTicketActionV3(args: {
    actionId: string;
    target: string;
    source: string;
    reason: string;
    attemptId?: string;
    expectedInteractionRevision?: string;
    scheduleId?: string;
  }): Promise<void> {
    return this.callReducer("memberRequestTicketActionV3", {
      version: 3,
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      actionId: args.actionId,
      target: args.target,
      source: args.source,
      reason: args.reason,
      attemptId: args.attemptId || "",
      expectedInteractionRevision: args.expectedInteractionRevision || "",
      scheduleId: args.scheduleId || "",
    });
  }

  scheduleTicketActionV3(args: {
    scheduleId: string;
    scheduledAtMicros: bigint;
    phoneLocalTime: string;
    phoneTimeZone: string;
    target?: string;
  }): Promise<void> {
    return this.callReducer("adminScheduleTicketActionV3", {
      version: 3,
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      scheduleId: args.scheduleId,
      scheduledAtMicros: args.scheduledAtMicros,
      phoneLocalTime: args.phoneLocalTime,
      phoneTimeZone: args.phoneTimeZone,
      target: args.target || "redetect_latest",
    });
  }

  waitForActivationDecision(attemptId: string, timeoutMs = 4000): Promise<any> {
    const cleanAttemptId = String(attemptId || "").trim();
    if (!cleanAttemptId) return Promise.reject(new Error("activation attempt ID is required"));
    const existing = this.latestActivationDecisions.find((row) =>
      String(row.attemptId || row.attempt_id || "") === cleanAttemptId
    );
    if (existing) return Promise.resolve(existing);
    return new Promise((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.activationDecisionWaiters.delete(cleanAttemptId);
        reject(new Error("activation decision timed out"));
      }, Math.max(500, timeoutMs));
      this.activationDecisionWaiters.set(cleanAttemptId, { resolve, reject, timer });
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

  private attachStateListeners(connection: DbConnection, generation: number): void {
    const publish = () => {
      if (!ownerViviConnectionEventAllowed({
        eventGeneration: generation,
        currentGeneration: this.connectionGeneration,
        eventConnection: connection,
        currentConnection: this.conn,
      })) return;
      this.publishFocusedState();
    };
    for (const table of this.focusedStateTables(connection.db)) {
      if (table.onInsert) table.onInsert(publish);
      if (table.onUpdate) table.onUpdate(publish);
      if (table.onDelete) table.onDelete(publish);
    }
  }

  private subscribeState(connection: DbConnection, generation: number): void {
    const ticket = sqlString(this.cfg.ticketId);
    const backendRow = sqlString(`${this.cfg.ticketId}:${this.backendId()}`);
    const backendId = sqlString(this.backendId());
    const ownerPublicId = sqlString(accountPublicId(this.cfg.email));
    const accountScopeId = sqlString(validAccountScopeId(this.cfg.accountScopeId));
    let applied = false;
    const queries = [
      `SELECT * FROM ticketremote_stream_desired_state WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_phone_current_report WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_control_code_fast_state WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_relay_current_report WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_stream_viewer_focus WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
      `SELECT * FROM ticketremote_control_code_request WHERE ticketId = ${ticket} AND ownerPublicId = ${ownerPublicId}`,
      `SELECT * FROM ticketremote_ticket_interaction WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_activation_eligibility WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_activation_decision WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
      `SELECT * FROM ticketremote_ticket_action_v3 WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
      `SELECT * FROM ticketremote_ticket_slider_region_v3 WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_member_hdr_state WHERE ticketId = ${ticket} AND accountScopeId = ${accountScopeId}`,
      `SELECT * FROM ticketremote_member_hdr_engine_state WHERE ticketId = ${ticket} AND accountScopeId = ${accountScopeId}`,
      `SELECT * FROM ticketremote_member_hdr_boost_state WHERE ticketId = ${ticket} AND accountScopeId = ${accountScopeId}`,
      `SELECT * FROM ticketremote_member_limit_state WHERE ticketId = ${ticket} AND ownerPublicId = ${ownerPublicId}`,
    ];
    if (this.cfg.ownerViviAuth) {
      queries.push(
        `SELECT * FROM ticketremote_vivi_credential_state WHERE id = ${backendRow}`,
        `SELECT * FROM ticketremote_vivi_reauth_attempt WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
        `SELECT * FROM ticketremote_owner_vivi_credentials WHERE id = ${backendRow}`,
      );
    }
    const connectionIsCurrent = () => ownerViviConnectionEventAllowed({
      eventGeneration: generation,
      currentGeneration: this.connectionGeneration,
      eventConnection: connection,
      currentConnection: this.conn,
    });
    this.subscription = connection.subscriptionBuilder()
      .onApplied(() => {
        if (!connectionIsCurrent()) return;
        if (!applied) {
          applied = true;
          this.attachStateListeners(connection, generation);
        }
        this.handlers.onSnapshotApplied?.();
        this.publishFocusedState();
        void this.refreshLimitState().catch((error) => {
          if (!connectionIsCurrent()) return;
          this.handlers.onStatus?.("limit_refresh_failed", error && String(error));
        });
        void this.refreshHDRState().catch((error) => {
          if (!connectionIsCurrent()) return;
          this.handlers.onStatus?.("hdr_refresh_failed", error && String(error));
        });
        void this.refreshHDREngineState().catch((error) => {
          if (!connectionIsCurrent()) return;
          this.handlers.onStatus?.("hdr_engine_refresh_failed", error && String(error));
        });
        void this.refreshHDRBoostState().catch((error) => {
          if (!connectionIsCurrent()) return;
          this.handlers.onStatus?.("hdr_boost_refresh_failed", error && String(error));
        });
      })
      .subscribe(queries);
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
    const ticketInteraction = tableRows(tableAccessor(db, "ticket_interaction"))
      .find((row) => rowId(row) === backendRow) || null;
    const activationEligibility = tableRows(tableAccessor(db, "activation_eligibility"))
      .find((row) => rowId(row) === backendRow) || null;
    const ticketSliderRegion = tableRows(tableAccessor(db, "ticket_slider_region_v3"))
      .find((row) => rowId(row) === backendRow) || null;
    const memberLimitState = tableRows(tableAccessor(db, "member_limit_state"))
      .find((row) => rowTicketId(row) === this.cfg.ticketId &&
        String(row.ownerPublicId || row.owner_public_id || "") === accountPublicId(this.cfg.email)) || null;
    const memberHDRState = tableRows(tableAccessor(db, "member_hdr_state"))
      .find((row) => rowTicketId(row) === this.cfg.ticketId &&
        String(row.accountScopeId || row.account_scope_id || "") === validAccountScopeId(this.cfg.accountScopeId)) || null;
    const memberHDREngineState = tableRows(tableAccessor(db, "member_hdr_engine_state"))
      .find((row) => rowTicketId(row) === this.cfg.ticketId &&
        String(row.accountScopeId || row.account_scope_id || "") === validAccountScopeId(this.cfg.accountScopeId)) || null;
    const memberHDREngine = ticketHDREngine(memberHDREngineState && memberHDREngineState.engine);
    const memberHDRBoostState = tableRows(tableAccessor(db, "member_hdr_boost_state"))
      .find((row) => rowTicketId(row) === this.cfg.ticketId &&
        String(row.accountScopeId || row.account_scope_id || "") === validAccountScopeId(this.cfg.accountScopeId)) || null;
    const memberHDRDisplayBoost = ticketHDRDisplayBoost(memberHDRBoostState &&
      (memberHDRBoostState.selectedDisplayBoost ?? memberHDRBoostState.selected_display_boost));
    const viviCredentialState = this.cfg.ownerViviAuth
      ? tableRows(tableAccessor(db, "vivi_credential_state")).find((row) => rowId(row) === backendRow) || null
      : null;
    const ownerViviCredentials = this.cfg.ownerViviAuth
      ? tableRows(tableAccessor(db, "owner_vivi_credentials")).find((row) => rowId(row) === backendRow) || null
      : null;
    const viviReauthAttempts = this.cfg.ownerViviAuth
      ? tableRows(tableAccessor(db, "vivi_reauth_attempt"))
        .filter((row) => rowTicketId(row) === this.cfg.ticketId && rowBackendId(row) === this.backendId())
        .sort((left, right) => {
          const updated = String(right.updatedAt || right.updated_at || "")
            .localeCompare(String(left.updatedAt || left.updated_at || ""));
          if (updated) return updated;
          return String(right.requestId || right.request_id || "")
            .localeCompare(String(left.requestId || left.request_id || ""));
        })
        .map((row) => ({
          requestId: String(row.requestId || row.request_id || ""),
          credentialRevision: String(row.credentialRevision || row.credential_revision || ""),
          ownerPublicId: String(row.ownerPublicId || row.owner_public_id || ""),
          status: String(row.status || ""),
          phase: String(row.phase || ""),
          reason: String(row.reason || ""),
          proofSource: String(row.proofSource || row.proof_source || ""),
          createdAt: String(row.createdAt || row.created_at || ""),
          updatedAt: String(row.updatedAt || row.updated_at || ""),
          completedAt: String(row.completedAt || row.completed_at || ""),
        }))
      : [];
    const activationDecisions = tableRows(tableAccessor(db, "activation_decision"))
      .filter((row) => rowTicketId(row) === this.cfg.ticketId && rowBackendId(row) === this.backendId())
      .map((row) => ({
        id: String(row.id || ""),
        attemptId: String(row.attemptId || row.attempt_id || ""),
        flow: String(row.flow || ""),
        accepted: row.accepted === true,
        reason: String(row.reason || ""),
        retryAt: String(row.retryAt || row.retry_at || ""),
        serverAt: String(row.serverAt || row.server_at || ""),
        interactionRevision: String(row.interactionRevision || row.interaction_revision || ""),
        updatedAt: String(row.updatedAt || row.updated_at || ""),
      }));
    const ticketActions = ticketActionV3ActionsByAuthority(tableRows(tableAccessor(db, "ticket_action_v3"))
      .filter((row) => rowTicketId(row) === this.cfg.ticketId && rowBackendId(row) === this.backendId())
      .map((row) => ({
        id: String(row.id || ""),
        actionId: String(row.actionId || row.action_id || ""),
        ticketId: String(row.ticketId || row.ticket_id || ""),
        backendId: String(row.backendId || row.backend_id || ""),
        target: String(row.target || ""),
        parentActionId: String(row.parentActionId || row.parent_action_id || ""),
        rootActionId: String(row.rootActionId || row.root_action_id || row.actionId || row.action_id || ""),
        retryOrdinal: Number(row.retryOrdinal ?? row.retry_ordinal ?? 0),
        status: String(row.status || ""),
        phase: String(row.phase || ""),
        currentView: String(row.currentView || row.current_view || "unknown"),
        switchAvailable: row.switchAvailable ?? row.switch_available === true,
        switchExpiresAt: String(row.switchExpiresAt || row.switch_expires_at || ""),
        streamEpoch: String(row.streamEpoch || row.stream_epoch || "0"),
        frameSequence: String(row.frameSequence || row.frame_sequence || "0"),
        reason: String(row.reason || ""),
        createdAt: String(row.createdAt || row.created_at || ""),
        updatedAt: String(row.updatedAt || row.updated_at || ""),
        completedAt: String(row.completedAt || row.completed_at || ""),
        expiresAt: String(row.expiresAt || row.expires_at || ""),
      })));
    this.latestActivationDecisions = activationDecisions;
    for (const decision of activationDecisions) {
      const waiter = this.activationDecisionWaiters.get(decision.attemptId);
      if (!waiter) continue;
      window.clearTimeout(waiter.timer);
      this.activationDecisionWaiters.delete(decision.attemptId);
      waiter.resolve(decision);
    }
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
      ticketInteraction: ticketInteraction ? {
        status: String(ticketInteraction.status || ""),
        interactionRevision: String(ticketInteraction.interactionRevision || ticketInteraction.interaction_revision || ""),
        activationRevision: String(ticketInteraction.activationRevision || ticketInteraction.activation_revision || ""),
        activationAt: String(ticketInteraction.activationAt || ticketInteraction.activation_at || ""),
        scheduledResetAt: String(ticketInteraction.scheduledResetAt || ticketInteraction.scheduled_reset_at || ""),
        resetRequestId: String(ticketInteraction.resetRequestId || ticketInteraction.reset_request_id || ""),
        streamEpoch: String(ticketInteraction.streamEpoch || ticketInteraction.stream_epoch || "0"),
        frameSequence: String(ticketInteraction.frameSequence || ticketInteraction.frame_sequence || "0"),
        phoneDisplayWidth: Number(ticketInteraction.phoneDisplayWidth ?? ticketInteraction.phone_display_width ?? 0),
        phoneDisplayHeight: Number(ticketInteraction.phoneDisplayHeight ?? ticketInteraction.phone_display_height ?? 0),
        sliderLeft: Number(ticketInteraction.sliderLeft ?? ticketInteraction.slider_left ?? 0),
        sliderTop: Number(ticketInteraction.sliderTop ?? ticketInteraction.slider_top ?? 0),
        sliderRight: Number(ticketInteraction.sliderRight ?? ticketInteraction.slider_right ?? 0),
        sliderBottom: Number(ticketInteraction.sliderBottom ?? ticketInteraction.slider_bottom ?? 0),
        ownerPublicId: String(ticketInteraction.ownerPublicId || ticketInteraction.owner_public_id || ""),
        controlId: String(ticketInteraction.controlId || ticketInteraction.control_id || ""),
        leasePhase: String(ticketInteraction.leasePhase || ticketInteraction.lease_phase || "none"),
        leaseExpiresAt: String(ticketInteraction.leaseExpiresAt || ticketInteraction.lease_expires_at || ""),
        latestInputSequence: String(ticketInteraction.latestInputSequence || ticketInteraction.latest_input_sequence || "0"),
        latestInputPhase: String(ticketInteraction.latestInputPhase || ticketInteraction.latest_input_phase || ""),
        latestProgress: Number(ticketInteraction.latestProgress ?? ticketInteraction.latest_progress ?? 0),
        lastAppliedSequence: String(ticketInteraction.lastAppliedSequence || ticketInteraction.last_applied_sequence || "0"),
        lastAppliedProgress: Number(ticketInteraction.lastAppliedProgress ?? ticketInteraction.last_applied_progress ?? 0),
        reason: String(ticketInteraction.reason || ""),
        updatedAt: String(ticketInteraction.updatedAt || ticketInteraction.updated_at || ""),
        expiresAt: String(ticketInteraction.expiresAt || ticketInteraction.expires_at || ""),
      } : null,
      activationEligibility: activationEligibility ? {
        allowed: activationEligibility.allowed === true,
        reason: String(activationEligibility.reason || ""),
        retryAt: String(activationEligibility.retryAt || activationEligibility.retry_at || ""),
        cooldownUntil: String(activationEligibility.cooldownUntil || activationEligibility.cooldown_until || ""),
        admissionsInWindow: Number(activationEligibility.admissionsInWindow ?? activationEligibility.admissions_in_window ?? 0),
        serverAt: String(activationEligibility.serverAt || activationEligibility.server_at || ""),
        updatedAt: String(activationEligibility.updatedAt || activationEligibility.updated_at || ""),
      } : null,
      memberLimits: memberLimitState ? {
        obeyLimits: memberLimitState.obeyLimits ?? memberLimitState.obey_limits === true,
        canBypass: memberLimitState.canBypass ?? memberLimitState.can_bypass === true,
        effectiveLimited: memberLimitState.effectiveLimited ?? memberLimitState.effective_limited === true,
        registrationAllowed: memberLimitState.registrationAllowed ?? memberLimitState.registration_allowed === true,
        registrationReason: String(memberLimitState.registrationReason || memberLimitState.registration_reason || ""),
        registrationCount: Number(memberLimitState.registrationCount ?? memberLimitState.registration_count ?? 0),
        registrationLimit: Number(memberLimitState.registrationLimit ?? memberLimitState.registration_limit ?? 10),
        registrationIntervalSeconds: Number(memberLimitState.registrationIntervalSeconds ?? memberLimitState.registration_interval_seconds ?? 30),
        registrationRetryAt: String(memberLimitState.registrationRetryAt || memberLimitState.registration_retry_at || ""),
        registrationNextReleaseAt: String(memberLimitState.registrationNextReleaseAt || memberLimitState.registration_next_release_at || ""),
        controlCodeCount: Number(memberLimitState.controlCodeCount ?? memberLimitState.control_code_count ?? 0),
        controlCodeLimit: Number(memberLimitState.controlCodeLimit ?? memberLimitState.control_code_limit ?? 2),
        controlCodeWindowSeconds: Number(memberLimitState.controlCodeWindowSeconds ?? memberLimitState.control_code_window_seconds ?? 60),
        controlCodeRetryAt: String(memberLimitState.controlCodeRetryAt || memberLimitState.control_code_retry_at || ""),
        controlCodeAllowed: memberLimitState.controlCodeAllowed ?? memberLimitState.control_code_allowed === true,
        controlCodeReason: String(memberLimitState.controlCodeReason || memberLimitState.control_code_reason || ""),
        updatedAt: String(memberLimitState.updatedAt || memberLimitState.updated_at || ""),
        serverAt: String(memberLimitState.serverAt || memberLimitState.server_at || ""),
      } : null,
      memberHDR: memberHDRState ? {
        enabled: memberHDRState.enabled === true,
        updatedAt: String(memberHDRState.updatedAt || memberHDRState.updated_at || ""),
        serverAt: String(memberHDRState.serverAt || memberHDRState.server_at || ""),
      } : null,
      memberHDREngine: {
        engine: memberHDREngine,
        ownerProjectionAvailable: Boolean(memberHDREngineState),
        updatedAt: String(memberHDREngineState && (memberHDREngineState.updatedAt || memberHDREngineState.updated_at) || ""),
        serverAt: String(memberHDREngineState && (memberHDREngineState.serverAt || memberHDREngineState.server_at) || ""),
      },
      memberHDRBoost: {
        selectedDisplayBoost: memberHDRDisplayBoost,
        accountProjectionAvailable: Boolean(memberHDRBoostState),
        updatedAt: String(memberHDRBoostState && (memberHDRBoostState.updatedAt || memberHDRBoostState.updated_at) || ""),
        serverAt: String(memberHDRBoostState && (memberHDRBoostState.serverAt || memberHDRBoostState.server_at) || ""),
      },
      viviCredentialState: viviCredentialState ? {
        configured: viviCredentialState.configured === true,
        revision: String(viviCredentialState.revision || ""),
        updatedAt: String(viviCredentialState.updatedAt || viviCredentialState.updated_at || ""),
      } : null,
      ownerViviCredentials: ownerViviCredentials ? {
        email: String(ownerViviCredentials.email || ""),
        password: String(ownerViviCredentials.password || ""),
        revision: String(ownerViviCredentials.revision || ""),
        updatedAt: String(ownerViviCredentials.updatedAt || ownerViviCredentials.updated_at || ""),
      } : null,
      viviReauthAttempts,
      // Keep the singular projection for older admin bundles while new code
      // follows its exact request and computes phone-lane busy from every row.
      viviReauthAttempt: viviReauthAttempts[0] || null,
      activationDecisions,
      ticketActions,
      ticketAction: ticketActions[0] || null,
      ticketSliderRegion: ticketSliderRegion ? {
        id: String(ticketSliderRegion.id || ""),
        ticketId: String(ticketSliderRegion.ticketId || ticketSliderRegion.ticket_id || ""),
        backendId: String(ticketSliderRegion.backendId || ticketSliderRegion.backend_id || ""),
        proofActionId: String(ticketSliderRegion.proofActionId || ticketSliderRegion.proof_action_id || ""),
        streamEpoch: String(ticketSliderRegion.streamEpoch || ticketSliderRegion.stream_epoch || "0"),
        frameSequence: String(ticketSliderRegion.frameSequence || ticketSliderRegion.frame_sequence || "0"),
        leftBasisPoints: Number(ticketSliderRegion.leftBasisPoints ?? ticketSliderRegion.left_basis_points ?? 0),
        topBasisPoints: Number(ticketSliderRegion.topBasisPoints ?? ticketSliderRegion.top_basis_points ?? 0),
        rightBasisPoints: Number(ticketSliderRegion.rightBasisPoints ?? ticketSliderRegion.right_basis_points ?? 0),
        bottomBasisPoints: Number(ticketSliderRegion.bottomBasisPoints ?? ticketSliderRegion.bottom_basis_points ?? 0),
        updatedAt: String(ticketSliderRegion.updatedAt || ticketSliderRegion.updated_at || ""),
        expiresAt: String(ticketSliderRegion.expiresAt || ticketSliderRegion.expires_at || ""),
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
    const names = ["stream_desired_state", "phone_current_report", "control_code_fast_state", "relay_current_report", "stream_viewer_focus", "control_code_request", "ticket_interaction", "activation_eligibility", "activation_decision", "ticket_action_v3", "ticket_slider_region_v3", "member_hdr_state", "member_hdr_engine_state", "member_hdr_boost_state", "member_limit_state"];
    if (this.cfg.ownerViviAuth) {
      names.push("vivi_credential_state", "vivi_reauth_attempt", "owner_vivi_credentials");
    }
    return names
      .map((name) => tableAccessor(source, name));
  }

  private backendId(): string {
    return String(this.cfg.backendId || "pixel");
  }

  private reducer(name: string): any {
    return this.reducerOnConnection(this.requireConnection(), name);
  }

  private reducerOnConnection(connection: DbConnection, name: string): any {
    const suffix = `${name.charAt(0).toUpperCase()}${name.slice(1)}`;
    return pickAccessor(connection.reducers, [`ticketremote${suffix}`, `ticketRemote${suffix}`, name]);
  }

  private async callReducerOnConnection(connection: DbConnection, name: string, args: Record<string, unknown>): Promise<void> {
    const reducer = this.reducerOnConnection(connection, name);
    await reducer(args);
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
