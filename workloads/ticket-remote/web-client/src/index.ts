import { DbConnection } from "./generated/index";
import { installCspSafeSpacetimeCodecs } from "./csp-safe-codecs";
import { ticketActionV3ActionsByAuthority } from "../ticket-action-v3-core.mjs";
import {
  ownerViviConnectionEventAllowed,
  prepareOwnerViviAccessBeforeSubscribe,
} from "../owner-vivi-access-core.js";
import { relayLastFrameAgeMillis } from "./relay-current-report";
import { phoneControlNow } from "../phone-control-core.mjs";

// SpacetimeDB 2.6.1 uses this method for reducer calls; iOS 16 lacks it.
if (typeof (Promise as any).withResolvers !== "function") {
  (Promise as any).withResolvers = () => {
    let resolve!: (value?: unknown) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
  };
}

installCspSafeSpacetimeCodecs();

type TicketClientConfig = {
  host: string;
  database: string;
  token: string;
  ticketId: string;
  sessionId: string;
  email: string;
  actorId?: string;
  accountScopeId: string;
  backendId?: string;
  ownerViviAuth?: boolean;
  automaticReconnect?: boolean;
};

type TicketClientHandlers = {
  onState?: (state: any) => void;
  onStatus?: (status: string, detail?: string) => void;
  onSnapshotApplied?: () => void;
};

export const TICKET_HDR_DISPLAY_BOOSTS = [2, 3, 4, 5, 6] as const;
export type TicketHDRDisplayBoost = typeof TICKET_HDR_DISPLAY_BOOSTS[number];



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
      const emailSort = String(left.email || "").localeCompare(String(right.email || ""));
      if (emailSort) return emailSort;
      return rowId(left).localeCompare(rowId(right));
    });
}

class TicketSpacetimeClient {
  private cfg: TicketClientConfig;
  private handlers: TicketClientHandlers;
  private conn: DbConnection | null = null;
  private subscription: { unsubscribe: () => void } | null = null;
  private reconnectTimer = 0;
  private reconnectAttempted = false;
  private connected = false;
  private connectionGeneration = 0;
  private manuallyDisconnected = false;
  private lastHeartbeatAt = 0;
  private lastStreamFocusActive: boolean | null = null;
  private viewerPresenceExpiryTimer = 0;
  private livePromise: Promise<void> | null = null;
  private resolveLivePromise: (() => void) | null = null;
  private rejectLivePromise: ((error: Error) => void) | null = null;
  private controlClock: { serverUpperAtReceipt: number; receivedMonotonic: number; receivedWall: number } | null = null;
  private controlClockRefresh: Promise<void> | null = null;


  constructor(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    this.cfg = cfg;
    this.handlers = handlers || {};
  }

  connect(automatic = false): void {
    if (!automatic) this.reconnectAttempted = false;
    this.disconnect(false);
    const generation = this.connectionGeneration + 1;
    this.connectionGeneration = generation;
    this.manuallyDisconnected = false;
    this.createLivePromise();
    this.connected = false;
    this.lastStreamFocusActive = null;
    this.handlers.onStatus?.("connecting");
    try {
      const builder = DbConnection.builder()
        .withUri(this.websocketURL())
        .withDatabaseName(this.cfg.database)
        .withToken(this.cfg.token)
        .withCompression("none")
        .onConnect((connection) => {
          if (generation !== this.connectionGeneration) {
            try { connection.disconnect(); } catch (_) {}
            return;
          }
          this.conn = connection;
          this.connected = true;
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
        .onDisconnect((_ctx, error) => {
          if (generation !== this.connectionGeneration) return;
          this.invalidateControlClock();
          this.connected = false;
          this.conn = null;
          this.rejectLive(new Error("Spacetime connection disconnected"));
          if (this.manuallyDisconnected) return;
          this.handlers.onStatus?.("reconnecting", error && String(error));
          this.scheduleReconnect();
        })
        .onConnectError((_ctx, error) => {
          if (generation !== this.connectionGeneration) return;
          this.invalidateControlClock();
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
    this.invalidateControlClock();
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
    const generation = this.connectionGeneration;
    const reducer = this.reducer("memberSetStreamFocus");
    Promise.resolve(reducer({
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      active,
      reason: reason || (active ? "browser_stream_heartbeat" : "browser_no_stream_heartbeat"),
    })).catch((error) => {
      if (generation !== this.connectionGeneration || !this.isReady()) return;
      this.lastStreamFocusActive = null;
      this.handlers.onStatus?.("heartbeat_failed", error && String(error));
    });
  }

  requestControlCode(digits: string, expectedFastRevision = "", beforeSubmit?: () => void,
    commandId = `control_code_${crypto.randomUUID()}`): Promise<void> {
    return this.requestPhoneCommand(commandId, "control_code", expectedFastRevision, {
      sessionId: this.cfg.sessionId,
      digits,
    }, beforeSubmit);
  }

  private requestPhoneCommand(commandId: string, operation: string, contextRevision: string,
    payload: Record<string, string>, beforeSubmit?: () => void): Promise<void> {
    const clock = phoneControlNow(this.controlClock, performance.now(), Date.now());
    return this.callReducer("memberCommand", {
      version: 2, ticketId: this.cfg.ticketId, backendId: this.backendId(),
      commandId, operation, contextRevision, issuedAt: new Date(Number.isFinite(clock) ? clock : Date.now()).toISOString(),
      payloadJson: JSON.stringify(payload),
    }, beforeSubmit);
  }

  setLimitPreference(obeyLimits: boolean): Promise<void> {
    return this.callReducer("memberSetLimitPreference", {
      ticketId: this.cfg.ticketId,
      obeyLimits: Boolean(obeyLimits),
    });
  }

  checkIn(requestId: string, expectedRevision: string, direction: string, carriage: number): Promise<void> {
    return this.callReducer("memberCheckIn", { ticketId: this.cfg.ticketId, requestId, expectedRevision, direction, carriage });
  }

  checkOut(expectedRevision: string): Promise<void> {
    return this.callReducer("memberCheckOut", { ticketId: this.cfg.ticketId, expectedRevision });
  }

  claimCheckinNotice(pageId: string): Promise<void> {
    return this.callReducer("memberClaimCheckinNotice", { ticketId: this.cfg.ticketId, pageId });
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

  requestViviReauthLogoutLogin(
    requestId: string,
    credentialRevision: string,
    redetectAfterLogin = false,
  ): Promise<void> {
    return this.requestPhoneCommand(requestId,
      redetectAfterLogin ? "vivi_logout_login_redetect" : "vivi_logout_login", "", {
      credentialRevision,
    });
  }

  requestViviReauthFullReset(requestId: string, credentialRevision: string): Promise<void> {
    return this.requestPhoneCommand(requestId, "vivi_full_reset", "", {
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

  private invalidateControlClock(): void {
    this.controlClock = null;
    // The SDK does not settle pending reducer promises when its socket closes.
    // A new connection must not inherit the old single-flight refresh.
    this.controlClockRefresh = null;
  }

  refreshLimitState(): Promise<void> {
    if (this.controlClockRefresh) return this.controlClockRefresh;
    const connection = this.conn;
    const generation = this.connectionGeneration;
    const started = performance.now();
    const task = this.callReducer("memberRefreshLimitState", { ticketId: this.cfg.ticketId }).then(() => {
      if (connection !== this.conn || generation !== this.connectionGeneration || !connection) return;
      const row = tableRows(tableAccessor(connection.db, "member_limit_state"))
        .find((candidate) => rowTicketId(candidate) === this.cfg.ticketId &&
          candidate.ownerPublicId === accountPublicId(this.cfg.actorId || this.cfg.email));
      const received = performance.now();
      const server = Date.parse(String(row && row.serverAt || ""));
      if (!Number.isFinite(server) || received - started > 2000) return;
      // The complete round trip bounds response age without assuming the device's wall clock.
      this.controlClock = { serverUpperAtReceipt: server + received - started,
        receivedMonotonic: received, receivedWall: Date.now() };
      this.publishFocusedState();
    }).finally(() => { if (this.controlClockRefresh === task) this.controlClockRefresh = null; });
    this.controlClockRefresh = task;
    return task;
  }

  requestTicketActionV3(args: {
    actionId: string;
    target: string;
    source: string;
    reason: string;
    attemptId?: string;
    expectedInteractionRevision?: string;
    scheduleId?: string;
  }, beforeSubmit?: () => void): Promise<void> {
    return this.requestPhoneCommand(args.actionId, args.target, args.expectedInteractionRevision || "", {
      source: args.source,
      reason: args.reason,
    }, beforeSubmit);
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

  confirmControlCodeBrowserCapture(requestId: string, candidateFrameEpoch: unknown, candidateFrameSequence: unknown, markerRevision: string, acceptedReason: string): Promise<void> {
    return this.callReducer("memberConfirmControlCodeBrowserCapture", {
      ticketId: this.cfg.ticketId,
      backendId: this.backendId(),
      sessionId: this.cfg.sessionId,
      requestId,
      candidateFrameEpoch: String(candidateFrameEpoch || "0"),
      candidateFrameSequence: String(candidateFrameSequence || "0"),
      markerRevision,
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
    if (this.cfg.automaticReconnect === false || this.manuallyDisconnected ||
      this.reconnectTimer || this.reconnectAttempted) return;
    this.reconnectAttempted = true;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = 0;
      this.connect(true);
    }, 1000);
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
    const ownerPublicId = sqlString(accountPublicId(this.cfg.actorId || this.cfg.email));
    const accountScopeId = sqlString(validAccountScopeId(this.cfg.accountScopeId));
    let applied = false;
    const queries = [
      `SELECT * FROM ticketremote_member_stream_state WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_service_phone_current_report WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_phone_control_state WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_privileged_relay_report WHERE id = ${backendRow}`,
      `SELECT * FROM ticketremote_privileged_viewers WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
      `SELECT * FROM ticketremote_member_checkin`,
      `SELECT * FROM ticketremote_member_checkin_groups`,
      `SELECT * FROM ticketremote_control_code_request WHERE ticketId = ${ticket} AND ownerPublicId = ${ownerPublicId}`,
      `SELECT * FROM ticketremote_member_ticket_switch WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
      `SELECT * FROM ticketremote_ticket_action_v3 WHERE ticketId = ${ticket} AND backendId = ${backendId}`,
      `SELECT * FROM ticketremote_member_hdr_state WHERE ticketId = ${ticket} AND accountScopeId = ${accountScopeId}`,
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
      .onError((ctx) => {
        if (!connectionIsCurrent()) return;
        this.handlers.onStatus?.("subscription_failed", ctx.event && String(ctx.event));
      })
      .onApplied(() => {
        if (!connectionIsCurrent()) return;
        this.reconnectAttempted = false;
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
        void this.refreshHDRBoostState().catch((error) => {
          if (!connectionIsCurrent()) return;
          this.handlers.onStatus?.("hdr_boost_refresh_failed", error && String(error));
        });
      })
      .subscribe(queries);
  }

  private publishFocusedState(): void {
    if (!this.isReady()) return;
    if (!Number.isFinite(phoneControlNow(this.controlClock, performance.now(), Date.now())) ||
      (this.controlClock && performance.now() - this.controlClock.receivedMonotonic >= 25000)) {
      void this.refreshLimitState().catch(() => {});
    }
    const db = this.requireConnection().db;
    const ticketId = this.cfg.ticketId, backendId = this.backendId();
    const backendRow = `${ticketId}:${backendId}`;
    const ownerPublicId = accountPublicId(this.cfg.actorId || this.cfg.email);
    const accountScopeId = validAccountScopeId(this.cfg.accountScopeId);
    const rows = (name: string) => tableRows(tableAccessor(db, name));
    const backend = (name: string) => rows(name).find(row => row.id === backendRow) || null;
    const account = (name: string) => rows(name).find(row => row.ticketId === ticketId && row.accountScopeId === accountScopeId) || null;
    const desired = backend("member_stream_state");
    const phoneReport = backend("service_phone_current_report");
    const phoneControlState = backend("phone_control_state");
    const relayReport = backend("privileged_relay_report");
    const memberLimits = rows("member_limit_state").find(row => row.ticketId === ticketId && row.ownerPublicId === ownerPublicId) || null;
    const memberHDR = account("member_hdr_state");
    const hdrBoost = account("member_hdr_boost_state");
    const ticketActions = ticketActionV3ActionsByAuthority(rows("ticket_action_v3")
      .filter(row => row.ticketId === ticketId && row.backendId === backendId));
    const viewerFocusRows = activeViewerFocusRows(rows("privileged_viewers"), ticketId, backendId);
    this.scheduleViewerPresenceExpiry(viewerFocusRows);
    const viewerPresence = viewerFocusRows.map(row => ({
      email: String(row.email || "").trim() || "Unknown account", connected: true,
      lastSeenAt: row.lastSeenAt, expiresAt: row.expiresAt,
    }));
    const controlCodeRequests = rows("control_code_request")
      .filter(row => row.ticketId === ticketId && row.ownerPublicId === ownerPublicId)
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
      .map(row => ({ ...row, requestId: row.id, resultMarkerRevision: row.resultMarkerRevision || "" }));
    const viviReauthAttempts = this.cfg.ownerViviAuth ? rows("vivi_reauth_attempt")
      .filter(row => row.ticketId === ticketId && row.backendId === backendId)
      .sort((a, b) => b.updatedAt.localeCompare(a.updatedAt) || b.requestId.localeCompare(a.requestId)) : [];
    const updatedAt = relayReport?.updatedAt || phoneReport?.updatedAt || new Date().toISOString();
    this.handlers.onState?.({
      ticket: { id: ticketId, displayName: "ViVi timed ticket", updatedAt },
      viewerCount: viewerPresence.length,
      viewerPresence,
      checkin: rows("member_checkin")[0] || null,
      checkinGroups: rows("member_checkin_groups"),
      phone: {
        id: backendId, attachName: backendId,
        desiredState: desired?.desiredActive ? "streaming" : "idle",
        lastSeenAt: phoneReport?.updatedAt || "",
      },
      streamDesired: desired,
      phoneCurrentReport: phoneReport,
      memberLimits,
      memberHDR,
      memberHDRBoost: {
        ...hdrBoost, selectedDisplayBoost: ticketHDRDisplayBoost(hdrBoost?.selectedDisplayBoost),
        accountProjectionAvailable: Boolean(hdrBoost),
      },
      viviCredentialState: this.cfg.ownerViviAuth ? backend("vivi_credential_state") : null,
      ownerViviCredentials: this.cfg.ownerViviAuth ? backend("owner_vivi_credentials") : null,
      viviReauthAttempts,
      viviReauthAttempt: viviReauthAttempts[0] || null,
      controlClock: this.controlClock,
      phoneControlState: phoneControlState ? {
        ...phoneControlState,
        sessionGeneration: String(phoneControlState.sessionGeneration),
        observationSequence: String(phoneControlState.observationSequence),
      } : null,
      ticketSwitch: backend("member_ticket_switch"),
      ticketActions,
      ticketAction: ticketActions[0] || null,
      relayCurrentReport: relayReport ? {
        ...relayReport, lastFrameAgoMillis: relayLastFrameAgeMillis(relayReport),
      } : null,
      controlCodeRequests,
      serverTime: updatedAt,
      stateBackend: "spacetime",
    });
  }

  private focusedStateTables(source: any): any[] {
    const names = ["member_stream_state", "service_phone_current_report", "phone_control_state", "privileged_relay_report", "privileged_viewers", "member_checkin", "member_checkin_groups", "control_code_request", "ticket_action_v3", "member_ticket_switch", "member_hdr_state", "member_hdr_boost_state", "member_limit_state"];
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

  private async callReducer(name: string, args: Record<string, unknown>, beforeSubmit?: () => void): Promise<void> {
    await this.whenLive(2000);
    const reducer = this.reducer(name);
    beforeSubmit?.();
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
