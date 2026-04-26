import { DbConnection, tables } from "./generated/index";

type SessionLike = {
  token?: string;
  expiresAt?: string;
} | null | undefined;

type LiveClientConfig = {
  host: string;
  database: string;
  feed?: string;
  pageMode?: PageMode;
};

type ConnectionState = "idle" | "connecting" | "live" | "reconnecting" | "offline";
type PageMode = "map" | "incidents";

type SubscriptionHandleLike = {
  unsubscribe: () => void;
} | null;

type VoteSelectionMap = Record<string, string>;

const DEFAULT_FEED = "transport";
const SATIKSMEBOT_DB_PREFIX = "satiksmebot_";

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function asNumber(value: unknown): number {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function rowsFrom(iterable: Iterable<unknown> | null | undefined): any[] {
  return iterable ? Array.from(iterable as Iterable<any>) : [];
}

function maybeAccessor<T = any>(source: any, candidates: string[]): T | null {
  for (const candidate of candidates) {
    if (candidate && source && candidate in source) {
      return source[candidate] as T;
    }
  }
  return null;
}

function parseTimeMs(value: unknown): number {
  const raw = asString(value).trim();
  if (!raw) {
    return 0;
  }
  const parsed = new Date(raw);
  const millis = parsed.getTime();
  return Number.isFinite(millis) ? millis : 0;
}

function sortByTimeAscending(left: any, right: any, field: string): number {
  return parseTimeMs(left?.[field]) - parseTimeMs(right?.[field]);
}

function sortByTimeDescending(left: any, right: any, field: string): number {
  return sortByTimeAscending(right, left, field);
}

function normalizeSnapshotState(row: any) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    feed: asString(row.feed).trim(),
    version: asString(row.version).trim(),
    path: asString(row.path).trim(),
    hash: asString(row.hash).trim(),
    publishedAt: asString(row.publishedAt).trim(),
    lastSuccessAt: asString(row.lastSuccessAt).trim(),
    lastAttemptAt: asString(row.lastAttemptAt).trim(),
    status: asString(row.status).trim(),
    consecutiveFailures: Number(row.consecutiveFailures) || 0,
    vehicleCount: Number(row.vehicleCount) || 0,
  };
}

function normalizeVehicleContext(row: any) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    scopeKey: asString(row.scopeKey).trim(),
    stopId: asString(row.stopId).trim(),
    stopName: asString(row.stopName).trim(),
    mode: asString(row.mode).trim(),
    routeLabel: asString(row.routeLabel).trim(),
    direction: asString(row.direction).trim(),
    destination: asString(row.destination).trim(),
    departureSeconds: asNumber(row.departureSeconds),
    liveRowId: asString(row.liveRowId).trim(),
  };
}

function normalizeIncidentVotes(row: any, voteSelections: VoteSelectionMap = {}) {
  const incidentId = asString(row?.id).trim();
  const userValue = incidentId ? asString(voteSelections[incidentId]).trim() : "";
  return {
    ongoing: asNumber(row?.ongoingVotes),
    cleared: asNumber(row?.clearedVotes),
    userValue,
  };
}

function normalizeIncidentSummary(row: any, voteSelections: VoteSelectionMap = {}) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    id: asString(row.id).trim(),
    scope: asString(row.scope).trim(),
    subjectId: asString(row.subjectId).trim(),
    subjectName: asString(row.subjectName).trim(),
    stopId: asString(row.stopId).trim(),
    lastReportName: asString(row.lastReportName).trim(),
    lastReportAt: asString(row.lastReportAt).trim(),
    lastReporter: asString(row.lastReporter).trim(),
    commentCount: asNumber(row.commentCount),
    votes: normalizeIncidentVotes(row, voteSelections),
    active: row?.active === true,
    resolved: row?.resolved === true,
    vehicle: normalizeVehicleContext(row.vehicle),
  };
}

function normalizeIncidentEvent(row: any) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    id: asString(row.id).trim(),
    incidentId: asString(row.incidentId).trim(),
    kind: asString(row.kind).trim(),
    name: asString(row.name).trim(),
    nickname: asString(row.nickname).trim(),
    createdAt: asString(row.createdAt).trim(),
  };
}

function normalizeIncidentComment(row: any) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    id: asString(row.id).trim(),
    incidentId: asString(row.incidentId).trim(),
    nickname: asString(row.nickname).trim(),
    body: asString(row.body).trim(),
    createdAt: asString(row.createdAt).trim(),
  };
}

function normalizeStopSighting(row: any) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    id: asString(row.id).trim(),
    incidentId: asString(row.incidentId).trim(),
    stopId: asString(row.stopId).trim(),
    stopName: asString(row.stopName).trim(),
    createdAt: asString(row.createdAt).trim(),
  };
}

function normalizeVehicleSighting(row: any) {
  if (!row || typeof row !== "object") {
    return null;
  }
  return {
    id: asString(row.id).trim(),
    incidentId: asString(row.incidentId).trim(),
    stopId: asString(row.stopId).trim(),
    stopName: asString(row.stopName).trim(),
    mode: asString(row.mode).trim(),
    routeLabel: asString(row.routeLabel).trim(),
    direction: asString(row.direction).trim(),
    destination: asString(row.destination).trim(),
    departureSeconds: asNumber(row.departureSeconds),
    liveRowId: asString(row.liveRowId).trim(),
    createdAt: asString(row.createdAt).trim(),
  };
}

class SatiksmeLiveClient {
  private readonly config: LiveClientConfig;
  private readonly feed: string;
  private connection: DbConnection | null = null;
  private subscription: SubscriptionHandleLike = null;
  private listeners = new Set<() => void>();
  private state: ConnectionState = "idle";
  private reconnectTimer: number | null = null;
  private reconnectAttempt = 0;
  private token = "";
  private connectPromise: Promise<boolean> | null = null;
  private manuallyDisconnected = false;
  private pageMode: PageMode;
  private incidentDetailTarget = "";

  constructor(config: LiveClientConfig) {
    this.config = {
      host: String(config.host || "").replace(/\/+$/, ""),
      database: String(config.database || "").trim(),
      feed: String(config.feed || DEFAULT_FEED).trim() || DEFAULT_FEED,
      pageMode: config.pageMode === "incidents" ? "incidents" : "map",
    };
    this.feed = this.config.feed || DEFAULT_FEED;
    this.pageMode = this.config.pageMode || "map";
  }

  onInvalidate(callback: () => void): () => void {
    this.listeners.add(callback);
    return () => {
      this.listeners.delete(callback);
    };
  }

  getConnectionState(): ConnectionState {
    return this.state;
  }

  setPageMode(mode: PageMode): void {
    const nextMode = mode === "incidents" ? "incidents" : "map";
    if (this.pageMode === nextMode) {
      return;
    }
    this.pageMode = nextMode;
    this.refreshSubscriptionSoon();
  }

  setIncidentDetailTarget(incidentId: string | null | undefined): void {
    const nextTarget = asString(incidentId).trim();
    if (this.incidentDetailTarget === nextTarget) {
      return;
    }
    this.incidentDetailTarget = nextTarget;
    this.refreshSubscriptionSoon();
  }

  isLive(): boolean {
    return this.state === "live";
  }

  async connect(session?: SessionLike): Promise<boolean> {
    const nextToken = this.normalizeToken(session);
    const tokenChanged = nextToken !== this.token;
    this.token = nextToken;
    if (!this.config.host || !this.config.database) {
      this.state = "offline";
      this.emitInvalidate();
      return false;
    }
    if (tokenChanged && (this.connection || this.connectPromise)) {
      this.unsubscribeAll();
      if (this.connection) {
        this.connection.disconnect();
        this.connection = null;
      }
      this.connectPromise = null;
      this.state = "idle";
    }
    if (this.state === "live" && !tokenChanged) {
      return true;
    }
    return this.openConnection();
  }

  disconnect(): void {
    this.manuallyDisconnected = true;
    this.clearReconnectTimer();
    this.unsubscribeAll();
    if (this.connection) {
      this.connection.disconnect();
      this.connection = null;
    }
    this.state = "idle";
    this.emitInvalidate();
  }

  currentSnapshotState(): any | null {
    if (this.pageMode !== "map") {
      return null;
    }
    const db = this.connection ? this.connection.db : tables;
    const table = this.liveSnapshotStateTable(db);
    if (!table) {
      return null;
    }
    const direct = table.feed && typeof table.feed.find === "function"
      ? table.feed.find(this.feed)
      : null;
    if (direct) {
      return normalizeSnapshotState(direct);
    }
    return normalizeSnapshotState(rowsFrom(table.iter ? table.iter() : []).find((row) => asString(row.feed).trim() === this.feed) || null);
  }

  currentPublicIncidents(limit = 0, voteSelections: VoteSelectionMap = {}) {
    const db = this.connection ? this.connection.db : tables;
    const table = this.publicIncidentTable(db);
    if (!table) {
      return [];
    }
    let items = rowsFrom(table.iter ? table.iter() : [])
      .map((row) => normalizeIncidentSummary(row, voteSelections))
      .filter(Boolean)
      .sort((left, right) => sortByTimeDescending(left, right, "lastReportAt"));
    if (limit > 0) {
      items = items.slice(0, limit);
    }
    return items;
  }

  currentIncidentDetail(incidentId: string, voteSelections: VoteSelectionMap = {}) {
    const cleanIncidentId = asString(incidentId).trim();
    if (!cleanIncidentId) {
      return null;
    }
    const db = this.connection ? this.connection.db : tables;
    const incidentTable = this.publicIncidentTable(db);
    const eventTable = this.publicIncidentEventTable(db);
    const commentTable = this.publicIncidentCommentTable(db);
    if (!incidentTable || !eventTable || !commentTable) {
      return null;
    }
    const summaryRow = incidentTable.id && typeof incidentTable.id.find === "function"
      ? incidentTable.id.find(cleanIncidentId)
      : rowsFrom(incidentTable.iter ? incidentTable.iter() : []).find((row) => asString(row.id).trim() === cleanIncidentId);
    const summary = normalizeIncidentSummary(summaryRow, voteSelections);
    if (!summary) {
      return null;
    }
    const events = rowsFrom(eventTable.iter ? eventTable.iter() : [])
      .filter((row) => asString(row.incidentId).trim() === cleanIncidentId)
      .map(normalizeIncidentEvent)
      .filter(Boolean)
      .sort((left, right) => sortByTimeAscending(left, right, "createdAt"));
    const comments = rowsFrom(commentTable.iter ? commentTable.iter() : [])
      .filter((row) => asString(row.incidentId).trim() === cleanIncidentId)
      .map(normalizeIncidentComment)
      .filter(Boolean)
      .sort((left, right) => sortByTimeAscending(left, right, "createdAt"));
    return { summary, events, comments };
  }

  currentSharedMapState(limit = 0, voteSelections: VoteSelectionMap = {}) {
    if (this.pageMode !== "map") {
      return null;
    }
    const db = this.connection ? this.connection.db : tables;
    const stopTable = this.publicStopSightingTable(db);
    const vehicleTable = this.publicVehicleSightingTable(db);
    if (!stopTable || !vehicleTable) {
      return null;
    }
    let stopSightings = rowsFrom(stopTable.iter ? stopTable.iter() : [])
      .map(normalizeStopSighting)
      .filter(Boolean)
      .sort((left, right) => sortByTimeDescending(left, right, "createdAt"));
    let vehicleSightings = rowsFrom(vehicleTable.iter ? vehicleTable.iter() : [])
      .map(normalizeVehicleSighting)
      .filter(Boolean)
      .sort((left, right) => sortByTimeDescending(left, right, "createdAt"));
    if (limit > 0) {
      stopSightings = stopSightings.slice(0, limit);
      vehicleSightings = vehicleSightings.slice(0, limit);
    }
    return {
      sightings: {
        stopSightings,
        vehicleSightings,
      },
      incidents: this.currentPublicIncidents(0, voteSelections),
    };
  }

  private normalizeToken(session: SessionLike): string {
    if (!session || typeof session.token !== "string") {
      return "";
    }
    const expiresAt = typeof session.expiresAt === "string" ? new Date(session.expiresAt) : null;
    if (expiresAt && !Number.isNaN(expiresAt.getTime()) && expiresAt.getTime() <= Date.now()) {
      return "";
    }
    return session.token.trim();
  }

  private openConnection(): Promise<boolean> {
    if (this.connectPromise) {
      return this.connectPromise;
    }
    this.manuallyDisconnected = false;
    this.clearReconnectTimer();
    this.unsubscribeAll();
    if (this.connection) {
      this.connection.disconnect();
      this.connection = null;
    }
    this.state = this.reconnectAttempt > 0 ? "reconnecting" : "connecting";
    this.emitInvalidate();
    this.connectPromise = new Promise<boolean>((resolve) => {
      let settled = false;
      const finish = (value: boolean) => {
        if (settled) {
          return;
        }
        settled = true;
        this.connectPromise = null;
        resolve(value);
      };
      const builder = DbConnection.builder()
        .withUri(this.websocketURL())
        .withDatabaseName(this.config.database)
        .onConnect((connection) => {
          void this.handleConnected(connection, finish);
        })
        .onDisconnect(() => {
          if (this.manuallyDisconnected) {
            return;
          }
          this.state = "reconnecting";
          this.emitInvalidate();
          this.scheduleReconnect();
          finish(false);
        })
        .onConnectError(() => {
          this.state = "offline";
          this.emitInvalidate();
          this.scheduleReconnect();
          finish(false);
        });
      if (this.token) {
        builder.withToken(this.token);
      }
      this.connection = builder.build();
      window.setTimeout(() => {
        finish(this.state === "live");
      }, 5000);
    });
    return this.connectPromise;
  }

  private async handleConnected(connection: DbConnection, finish: (value: boolean) => void): Promise<void> {
    try {
      this.connection = connection;
      this.attachTableListeners(connection);
      await this.refreshSubscription();
      this.state = "live";
      this.reconnectAttempt = 0;
      this.emitInvalidate();
      finish(true);
    } catch (_) {
      this.state = "offline";
      this.emitInvalidate();
      this.scheduleReconnect();
      finish(false);
    }
  }

  private websocketURL(): URL {
    const base = new URL(this.config.host);
    base.protocol = base.protocol === "https:" ? "wss:" : "ws:";
    return base;
  }

  private liveSnapshotStateTable(source: any): any | null {
    return maybeAccessor(source, [
      "satiksmebot_public_live_snapshot_state",
      "satiksmebotPublicLiveSnapshotState",
      "publicLiveSnapshotState",
      "public_live_snapshot_state",
      `${SATIKSMEBOT_DB_PREFIX}public_live_snapshot_state`,
    ]);
  }

  private publicIncidentTable(source: any): any | null {
    return maybeAccessor(source, [
      "satiksmebot_public_incident",
      "satiksmebotPublicIncident",
      "publicIncident",
      "public_incident",
      `${SATIKSMEBOT_DB_PREFIX}public_incident`,
    ]);
  }

  private publicIncidentEventTable(source: any): any | null {
    return maybeAccessor(source, [
      "satiksmebot_public_incident_event",
      "satiksmebotPublicIncidentEvent",
      "publicIncidentEvent",
      "public_incident_event",
      `${SATIKSMEBOT_DB_PREFIX}public_incident_event`,
    ]);
  }

  private publicIncidentCommentTable(source: any): any | null {
    return maybeAccessor(source, [
      "satiksmebot_public_incident_comment",
      "satiksmebotPublicIncidentComment",
      "publicIncidentComment",
      "public_incident_comment",
      `${SATIKSMEBOT_DB_PREFIX}public_incident_comment`,
    ]);
  }

  private publicStopSightingTable(source: any): any | null {
    return maybeAccessor(source, [
      "satiksmebot_public_stop_sighting",
      "satiksmebotPublicStopSighting",
      "publicStopSighting",
      "public_stop_sighting",
      `${SATIKSMEBOT_DB_PREFIX}public_stop_sighting`,
    ]);
  }

  private publicVehicleSightingTable(source: any): any | null {
    return maybeAccessor(source, [
      "satiksmebot_public_vehicle_sighting",
      "satiksmebotPublicVehicleSighting",
      "publicVehicleSighting",
      "public_vehicle_sighting",
      `${SATIKSMEBOT_DB_PREFIX}public_vehicle_sighting`,
    ]);
  }

  private attachTableListeners(connection: DbConnection): void {
    const invalidate = () => {
      this.emitInvalidate();
    };
    for (const table of this.listenerTables(connection.db)) {
      table.onInsert?.(invalidate);
      table.onUpdate?.(invalidate);
      table.onDelete?.(invalidate);
    }
  }

  private async refreshSubscription(): Promise<void> {
    if (!this.connection) {
      this.subscription = await this.replaceSubscription(this.subscription, []);
      return;
    }
    const queries = this.subscriptionQueries(tables);
    this.subscription = await this.replaceSubscription(this.subscription, queries);
  }

  private refreshSubscriptionSoon(): void {
    if (!this.connection) {
      this.emitInvalidate();
      return;
    }
    void this.refreshSubscription().then(() => {
      this.emitInvalidate();
    }).catch(() => {
      this.state = "offline";
      this.emitInvalidate();
      this.scheduleReconnect();
    });
  }

  private async replaceSubscription(current: SubscriptionHandleLike, queries: any[]): Promise<SubscriptionHandleLike> {
    if (!this.connection || !queries.length) {
      if (current) {
        try {
          current.unsubscribe();
        } catch (_) {
          // Ignore stale handles.
        }
      }
      return null;
    }
    const nextHandle = await new Promise<SubscriptionHandleLike>((resolve, reject) => {
      let settled = false;
      let handle: SubscriptionHandleLike = null;
      handle = this.connection!.subscriptionBuilder()
        .onApplied(() => {
          if (!settled) {
            settled = true;
            resolve(handle);
          }
          this.emitInvalidate();
        })
        .onError(() => {
          if (!settled) {
            settled = true;
            reject(new Error("subscription failed"));
            return;
          }
          this.state = "offline";
          this.emitInvalidate();
          this.scheduleReconnect();
        })
        .subscribe(queries) as SubscriptionHandleLike;
    });
    if (current) {
      try {
        current.unsubscribe();
      } catch (_) {
        // Ignore stale handles.
      }
    }
    return nextHandle;
  }

  private scheduleReconnect(): void {
    if (this.manuallyDisconnected || this.reconnectTimer !== null) {
      return;
    }
    this.reconnectAttempt += 1;
    const delayMs = Math.min(30000, 1000 * Math.pow(2, Math.max(0, this.reconnectAttempt - 1)));
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      void this.openConnection();
    }, delayMs);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private unsubscribeAll(): void {
    if (!this.subscription) {
      return;
    }
    try {
      this.subscription.unsubscribe();
    } catch (_) {
      // Ignore stale handles.
    }
    this.subscription = null;
  }

  private emitInvalidate(): void {
    for (const listener of Array.from(this.listeners)) {
      try {
        listener();
      } catch (_) {
        // Ignore listener errors so reconnects continue.
      }
    }
  }

  private listenerTables(source: any): any[] {
    return [
      this.liveSnapshotStateTable(source),
      this.publicIncidentTable(source),
      this.publicIncidentEventTable(source),
      this.publicIncidentCommentTable(source),
      this.publicStopSightingTable(source),
      this.publicVehicleSightingTable(source),
    ].filter(Boolean);
  }

  private subscriptionQueries(source: any): any[] {
    const queries: any[] = [];
    const incidentTable = this.publicIncidentTable(source);
    if (incidentTable) {
      queries.push(incidentTable);
    }
    if (this.pageMode === "map") {
      const snapshotTable = this.liveSnapshotStateTable(source);
      if (snapshotTable) {
        queries.push(this.filteredFeedQuery(snapshotTable));
      }
      const stopTable = this.publicStopSightingTable(source);
      if (stopTable) {
        queries.push(stopTable);
      }
      const vehicleTable = this.publicVehicleSightingTable(source);
      if (vehicleTable) {
        queries.push(vehicleTable);
      }
    }
    if (this.incidentDetailTarget) {
      const eventTable = this.publicIncidentEventTable(source);
      if (eventTable) {
        queries.push(this.filteredIncidentQuery(eventTable));
      }
      const commentTable = this.publicIncidentCommentTable(source);
      if (commentTable) {
        queries.push(this.filteredIncidentQuery(commentTable));
      }
    }
    return queries.filter(Boolean);
  }

  private filteredFeedQuery(table: any): any {
    if (table && typeof table.where === "function") {
      return table.where((row: any) => row.feed.eq(this.feed));
    }
    return table;
  }

  private filteredIncidentQuery(table: any): any {
    if (table && typeof table.where === "function") {
      return table.where((row: any) => row.incidentId.eq(this.incidentDetailTarget));
    }
    return table;
  }
}

window.SatiksmeLiveClient = {
  create(config: LiveClientConfig) {
    return new SatiksmeLiveClient(config);
  },
};
