import { DbConnection, tables } from "./generated/index";

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

function nowISO(): string {
  return new Date().toISOString();
}

function asRowState(row: any): any | null {
  if (!row) return null;
  const raw = row.stateJson || row.state_json || row.stateJSON || "";
  if (!raw) return null;
  try {
    return JSON.parse(String(raw));
  } catch (_) {
    return null;
  }
}

class TicketSpacetimeClient {
  private cfg: TicketClientConfig;
  private handlers: TicketClientHandlers;
  private conn: DbConnection | null = null;
  private subscription: { unsubscribe: () => void } | null = null;
  private reconnectTimer = 0;
  private manuallyDisconnected = false;

  constructor(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    this.cfg = cfg;
    this.handlers = handlers || {};
  }

  connect(): void {
    this.disconnect(false);
    this.manuallyDisconnected = false;
    this.handlers.onStatus?.("connecting");
    const builder = DbConnection.builder()
      .withUri(this.websocketURL())
      .withDatabaseName(this.cfg.database)
      .withToken(this.cfg.token)
      .onConnect((connection) => {
        this.conn = connection;
        this.handlers.onStatus?.("live");
        this.attachStateListeners(connection);
        this.subscribeState(connection);
        this.heartbeat(true);
      })
      .onDisconnect(() => {
        if (this.manuallyDisconnected) return;
        this.handlers.onStatus?.("reconnecting");
        this.scheduleReconnect();
      })
      .onConnectError((_ctx, error) => {
        this.handlers.onStatus?.("offline", error && String(error));
        this.scheduleReconnect();
      });
    this.conn = builder.build();
  }

  disconnect(markDisconnected = true): void {
    if (this.reconnectTimer) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = 0;
    }
    if (markDisconnected && this.conn) {
      this.heartbeat(false);
    }
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
    const reducer = this.reducer("memberHeartbeatPresence");
    Promise.resolve(reducer({
      ticketId: this.cfg.ticketId,
      sessionId: this.cfg.sessionId,
      displayName: this.cfg.email,
      page: "ticket",
      connected,
      now: nowISO(),
    })).catch((error) => this.handlers.onStatus?.("heartbeat_failed", error && String(error)));
  }

  disconnectPresence(): void {
    Promise.resolve(this.reducer("memberDisconnectPresence")({
      ticketId: this.cfg.ticketId,
      sessionId: this.cfg.sessionId,
      now: nowISO(),
    })).catch(() => {});
  }

  claimControl(): Promise<void> {
    return this.callReducer("memberClaimControl", {
      ticketId: this.cfg.ticketId,
      sessionId: this.cfg.sessionId,
      now: nowISO(),
    });
  }

  extendControl(): Promise<void> {
    return this.callReducer("memberExtendControl", {
      ticketId: this.cfg.ticketId,
      sessionId: this.cfg.sessionId,
      now: nowISO(),
    });
  }

  releaseControl(reason = "user_released"): Promise<void> {
    return this.callReducer("memberReleaseControl", {
      ticketId: this.cfg.ticketId,
      sessionId: this.cfg.sessionId,
      reason,
      now: nowISO(),
    });
  }

  revokeControl(reason = "admin_revoked"): Promise<void> {
    return this.callReducer("memberRevokeControl", {
      ticketId: this.cfg.ticketId,
      reason,
      now: nowISO(),
    });
  }

  upsertMember(email: string, role: string): Promise<void> {
    return this.callReducer("memberUpsertMember", {
      ticketId: this.cfg.ticketId,
      email,
      role,
      now: nowISO(),
    });
  }

  removeMember(email: string): Promise<void> {
    return this.callReducer("memberRemoveMember", {
      ticketId: this.cfg.ticketId,
      email,
      now: nowISO(),
    });
  }

  private websocketURL(): URL {
    const base = new URL(this.cfg.host);
    base.protocol = base.protocol === "https:" ? "wss:" : "ws:";
    return base;
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = 0;
      this.connect();
    }, 1000);
  }

  private attachStateListeners(connection: DbConnection): void {
    const table = this.liveStateTable(connection.db);
    const publish = () => this.publishCurrentState();
    if (table.onInsert) table.onInsert(publish);
    if (table.onUpdate) table.onUpdate(publish);
    if (table.onDelete) table.onDelete(publish);
  }

  private subscribeState(connection: DbConnection): void {
    const tableQuery = pickAccessor(tables, [
      "ticketremoteLiveState",
      "ticketRemoteLiveState",
      "ticketremote_live_state",
    ]);
    this.subscription = connection.subscriptionBuilder()
      .onApplied(() => this.publishCurrentState())
      .subscribe(tableQuery);
  }

  private publishCurrentState(): void {
    const table = this.liveStateTable(this.requireConnection().db);
    const rows = Array.from(table.iter ? table.iter() : []) as any[];
    const wanted = rows.find((row) => String(row.ticketId || row.ticket_id || "") === this.cfg.ticketId) || rows[0];
    const state = asRowState(wanted);
    if (state) {
      this.handlers.onState?.(state);
    }
  }

  private liveStateTable(source: any): any {
    return pickAccessor(source, [
      "ticketremoteLiveState",
      "ticketRemoteLiveState",
      "ticketremote_live_state",
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
    if (!this.conn) {
      throw new Error("Spacetime connection unavailable");
    }
    return this.conn;
  }
}

(window as any).TicketSpacetime = {
  create(cfg: TicketClientConfig, handlers: TicketClientHandlers) {
    return new TicketSpacetimeClient(cfg, handlers);
  },
};
