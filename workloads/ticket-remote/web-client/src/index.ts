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

function tableRows(table: any): any[] {
  return Array.from(table && table.iter ? table.iter() : []) as any[];
}

function rowTicketId(row: any): string {
  return String(row && (row.ticketId || row.ticket_id) || "");
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
    const reducer = this.reducer("memberHeartbeatPresence");
    Promise.resolve(reducer({
      ticketId: this.cfg.ticketId,
      displayName: this.cfg.email,
      page: "ticket",
      connected,
    })).catch((error) => this.handlers.onStatus?.("heartbeat_failed", error && String(error)));
  }

  disconnectPresence(): void {
    if (!this.isReady()) return;
    Promise.resolve(this.reducer("memberDisconnectPresence")({
      ticketId: this.cfg.ticketId,
    })).catch(() => {});
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
    this.subscription = connection.subscriptionBuilder()
      .onApplied(() => this.publishFocusedState())
      .subscribe([
        `SELECT * FROM ticketremote_ticket_summary WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_viewer_public WHERE ticketId = ${ticket}`,
        `SELECT * FROM ticketremote_phone_status WHERE ticketId = ${ticket}`,
      ]);
  }

  private publishFocusedState(): void {
    if (!this.isReady()) return;
    const db = this.requireConnection().db;
    const summary = tableRows(this.ticketSummaryTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const viewers = tableRows(this.viewerPublicTable(db))
      .filter((row) => rowTicketId(row) === this.cfg.ticketId && row.connected !== false)
      .sort((a, b) => String(a.publicId || a.public_id || "").localeCompare(String(b.publicId || b.public_id || "")));
    const phone = tableRows(this.phoneStatusTable(db))
      .find((row) => rowTicketId(row) === this.cfg.ticketId) || null;
    const updatedAt = String(
      summary && (summary.updatedAt || summary.updated_at) ||
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
      viewerCount: Number((summary ? (summary.viewerCount ?? summary.viewer_count) : undefined) ?? viewers.length),
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
      serverTime: updatedAt,
      stateBackend: "spacetime",
    });
  }

  private focusedStateTables(source: any): any[] {
    return [
      this.ticketSummaryTable(source),
      this.viewerPublicTable(source),
      this.phoneStatusTable(source),
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
