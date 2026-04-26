import { DbConnection, tables } from "./generated/index";

type SessionLike = {
  token?: string;
  expiresAt?: string;
} | null | undefined;

type BundleIdentity = {
  version?: string;
  serviceDate?: string;
} | null | undefined;

type LiveClientConfig = {
  host: string;
  database: string;
};

type ConnectionState = "idle" | "connecting" | "live" | "reconnecting" | "offline";

type DetailTargets = {
  trainId: string;
  stationId: string;
  incidentId: string;
};

type SubscriptionHandleLike = {
  unsubscribe: () => void;
} | null;

const CHECKIN_GRACE_MS = 10 * 60 * 1000;
const CHECKIN_FALLBACK_WINDOW_MS = 6 * 60 * 60 * 1000;
const STATION_MATCH_PAST_WINDOW_MS = 5 * 60 * 1000;
const STATION_MATCH_FUTURE_WINDOW_MS = 90 * 60 * 1000;
const TRAIN_ACTIVITY_ACTIVE_MS = 15 * 60 * 1000;
const STATION_ACTIVITY_ACTIVE_MS = 30 * 60 * 1000;
const NETWORK_RECENT_MS = 30 * 60 * 1000;
const CURRENT_RIDE_SETTLE_RETRIES = 6;
const CURRENT_RIDE_SETTLE_DELAY_MS = 250;
const RIGA_TIME_ZONE = "Europe/Riga";
const DEFAULT_SCHEDULE_CUTOFF_HOUR = 3;

const rigaScheduleFormatter = new Intl.DateTimeFormat("en-CA", {
  timeZone: RIGA_TIME_ZONE,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  hourCycle: "h23",
});

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

function asString(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function trimOptional(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed === "" ? undefined : trimmed;
}

function rowsFrom(iterable: Iterable<unknown> | null | undefined): any[] {
  return iterable ? Array.from(iterable as Iterable<any>) : [];
}

function firstRow(iterable: Iterable<unknown> | null | undefined): any | null {
  const items = rowsFrom(iterable);
  return items.length ? items[0] : null;
}

function parseISO(value: string | undefined | null): Date | null {
  if (!value) {
    return null;
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function sleepMs(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, Math.max(0, ms));
  });
}

function compareTimeAscending(left: string | undefined, right: string | undefined): number {
  return (parseISO(left || "")?.getTime() || 0) - (parseISO(right || "")?.getTime() || 0);
}

function compareTimeDescending(left: string | undefined, right: string | undefined): number {
  return compareTimeAscending(right, left);
}

function normalizeLanguage(value: string): string {
  const normalized = String(value || "").trim().toUpperCase();
  return normalized === "LV" ? "LV" : "EN";
}

function normalizeAlertStyle(value: string): string {
  const normalized = String(value || "").trim().toUpperCase();
  return normalized === "DISCREET" ? "DISCREET" : "DETAILED";
}

function normalizeStationQueryValue(value: string): string {
  let normalized = value.trim().toLowerCase();
  if (!normalized) {
    return "";
  }
  const folds: Array<[string, string]> = [
    ["ā", "a"],
    ["č", "c"],
    ["ē", "e"],
    ["ģ", "g"],
    ["ī", "i"],
    ["ķ", "k"],
    ["ļ", "l"],
    ["ņ", "n"],
    ["š", "s"],
    ["ū", "u"],
    ["ž", "z"],
  ];
  for (const [from, to] of folds) {
    normalized = normalized.replaceAll(from, to);
  }
  normalized = normalized.replaceAll("-", " ");
  return normalized.split(/\s+/).filter(Boolean).join(" ");
}

function rigaDateParts(date: Date): { year: string; month: string; day: string; hour: number } {
  const parts = rigaScheduleFormatter.formatToParts(date);
  const byType = new Map(parts.map((part) => [part.type, part.value]));
  const hour = Number(byType.get("hour") || "0");
  return {
    year: byType.get("year") || "1970",
    month: byType.get("month") || "01",
    day: byType.get("day") || "01",
    hour: Number.isFinite(hour) ? hour : 0,
  };
}

function formatServiceDateFor(date: Date): string {
  const parts = rigaDateParts(date);
  return `${parts.year}-${parts.month}-${parts.day}`;
}

function isBeforeScheduleCutoff(date: Date, cutoffHour: number): boolean {
  const parts = rigaDateParts(date);
  return parts.hour < cutoffHour;
}

function fallbackScheduleCutoffHour(): number {
  const appConfig = typeof globalThis === "object" && globalThis
    ? (globalThis as { TRAIN_APP_CONFIG?: { schedule?: { cutoffHour?: unknown } } }).TRAIN_APP_CONFIG
    : undefined;
  const raw = Number(appConfig?.schedule?.cutoffHour);
  if (Number.isFinite(raw) && raw >= 0 && raw <= 23) {
    return Math.floor(raw);
  }
  return DEFAULT_SCHEDULE_CUTOFF_HOUR;
}

function utcDayStart(date: Date): Date {
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate(), 0, 0, 0, 0));
}

function utcDayEnd(date: Date): Date {
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate(), 23, 59, 59, 999));
}

function isoPlus(base: string, deltaMs: number): string {
  const parsed = parseISO(base);
  if (!parsed) {
    return new Date(Date.now() + deltaMs).toISOString();
  }
  return new Date(parsed.getTime() + deltaMs).toISOString();
}

function genericNickname(stableId: string): string {
  const adjectives = [
    "Amber", "Cedar", "Silver", "North", "Swift", "Mellow", "Harbor", "Forest",
    "Granite", "Quiet", "Bright", "Saffron", "Willow", "Copper", "River", "Cloud",
  ];
  const nouns = [
    "Scout", "Rider", "Signal", "Beacon", "Traveler", "Watcher", "Harbor", "Comet",
    "Falcon", "Lantern", "Pioneer", "Courier", "Voyager", "Pilot", "Atlas", "Drifter",
  ];
  let hash = 2166136261 >>> 0;
  const input = `train:${stableId}`;
  for (let index = 0; index < input.length; index += 1) {
    hash ^= input.charCodeAt(index);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  const adjective = adjectives[hash % adjectives.length];
  const noun = nouns[(hash >>> 8) % nouns.length];
  const suffix = String((hash % 900) + 100).padStart(3, "0");
  return `${adjective} ${noun} ${suffix}`;
}

function incidentCommentActivityLabel(): string {
  return "Comment";
}

function incidentVoteEventLabel(value: string): string {
  switch (String(value || "").trim().toUpperCase()) {
    case "ONGOING":
      return "Still there";
    case "CLEARED":
      return "Cleared";
    default:
      return "Vote";
  }
}

class TrainAppLiveClient {
  private readonly config: LiveClientConfig;
  private connection: DbConnection | null = null;
  private publicSubscription: SubscriptionHandleLike = null;
  private userSubscription: SubscriptionHandleLike = null;
  private detailSubscription: SubscriptionHandleLike = null;
  private listeners = new Set<() => void>();
  private state: ConnectionState = "idle";
  private reconnectTimer: number | null = null;
  private reconnectAttempt = 0;
  private token = "";
  private connectPromise: Promise<boolean> | null = null;
  private manuallyDisconnected = false;
  private detailTargets: DetailTargets = { trainId: "", stationId: "", incidentId: "" };

  constructor(config: LiveClientConfig) {
    this.config = {
      host: String(config.host || "").replace(/\/+$/, ""),
      database: String(config.database || "").trim(),
    };
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

  isLive(): boolean {
    return this.state === "live";
  }

  async connect(session?: SessionLike): Promise<boolean> {
    const nextToken = this.normalizeToken(session);
    if (!this.config.host || !this.config.database) {
      this.state = "offline";
      return false;
    }
    if (this.connection && this.token === nextToken && (this.state === "live" || this.state === "connecting" || this.state === "reconnecting")) {
      return this.isLive();
    }
    this.token = nextToken;
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
    this.state = "offline";
    this.emitInvalidate();
  }

  async readPublicDashboard(limit = 0): Promise<any> {
    this.requireLiveConnection();
    try {
      return await this.callProcedure("getPublicDashboard", { limit: Math.max(0, Math.trunc(Number(limit) || 0)) }, true);
    } catch (_) {
      return this.withSchedule({
        generatedAt: this.nowISO(),
        trains: this.publicDashboardPayload(limit),
      });
    }
  }

  async readPublicServiceDayTrains(): Promise<any> {
    this.requireLiveConnection();
    try {
      return await this.callProcedure("getPublicServiceDayTrains", {}, true);
    } catch (_) {
      return this.withSchedule({
        generatedAt: this.nowISO(),
        trains: this.publicServiceDayPayload(),
      });
    }
  }

  async readPublicTrain(trainId: string): Promise<any> {
    await this.setDetailTargets({ trainId });
    try {
      return this.withSchedule(this.buildPublicTrainView(trainId));
    } catch (_) {
      return this.callProcedure("getPublicTrain", { trainId }, true);
    }
  }

  async readPublicTrainStops(trainId: string): Promise<any> {
    await this.setDetailTargets({ trainId });
    try {
      return this.withSchedule(this.trainStopPayload(trainId));
    } catch (_) {
      return this.callProcedure("getPublicTrainStops", { trainId }, true);
    }
  }

  async readPublicNetworkMap(): Promise<any> {
    this.requireLiveConnection();
    try {
      return await this.callProcedure("getPublicNetworkMap", {}, true);
    } catch (_) {
      return this.withSchedule(this.networkMapPayload());
    }
  }

  async searchPublicStations(query: string): Promise<any> {
    this.requireLiveConnection();
    try {
      return await this.callProcedure("searchPublicStations", { query }, true);
    } catch (_) {
      const normalizedQuery = normalizeStationQueryValue(asString(query));
      const stations = this.listStationsForServiceDate(this.activeServiceDate()).filter((item) => {
        if (!normalizedQuery) {
          return true;
        }
        const key = normalizeStationQueryValue(asString(item.normalizedKey || item.name));
        const name = normalizeStationQueryValue(asString(item.name));
        return key.startsWith(normalizedQuery) || name.startsWith(normalizedQuery);
      });
      return this.withSchedule({ stations });
    }
  }

  async readPublicStationDepartures(stationId: string): Promise<any> {
    this.requireLiveConnection();
    return this.callProcedure("getPublicStationDepartures", { stationId }, true);
  }

  async listPublicIncidents(limit = 0): Promise<any> {
    this.requireLiveConnection();
    try {
      return await this.callProcedure("listPublicIncidents", { limit: Math.max(0, Math.trunc(Number(limit) || 0)) }, true);
    } catch (_) {
      return this.withSchedule({
        generatedAt: this.nowISO(),
        incidents: this.listIncidentSummariesPayload(limit),
      });
    }
  }

  async readPublicIncidentDetail(incidentId: string): Promise<any> {
    await this.setDetailTargets({ incidentId });
    try {
      return this.withSchedule(this.incidentDetailPayload(incidentId));
    } catch (_) {
      return this.callProcedure("getPublicIncidentDetail", { incidentId }, true);
    }
  }

  async bootstrapMe(): Promise<any> {
    this.requireAuthenticatedConnection();
    const profile = this.profileRow();
    const stableId = asString(profile?.stableId).trim();
    return {
      userId: stableId,
      stableUserId: stableId,
      nickname: asString(profile?.nickname).trim() || genericNickname(stableId),
      settings: this.settingsPayload(),
      currentRide: await this.resolvedCurrentRidePayload(),
    };
  }

  async currentRide(): Promise<any> {
    this.requireAuthenticatedConnection();
    return {
      currentRide: await this.resolvedCurrentRidePayload(),
    };
  }

  async settings(): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.settingsPayload();
  }

  async favorites(): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.favoriteListPayload();
  }

  async listWindowTrains(windowId: string): Promise<any> {
    this.requireAuthenticatedConnection();
    const trains = this.trainsByWindow(windowId).map((train) => this.buildTrainCard(train));
    return this.withSchedule({ trains });
  }

  async searchStations(query: string): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.searchPublicStations(query);
  }

  async stationDepartures(stationId: string): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.callProcedure("getStationDepartures", { stationId }, false);
  }

  async stationSightingDestinations(stationId: string): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.callProcedure("getStationSightingDestinations", { stationId }, false);
  }

  async searchRouteDestinations(originStationId: string, query: string): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.callProcedure("searchRouteDestinations", { originStationId, query }, false);
  }

  async listRouteTrains(originStationId: string, destinationStationId: string): Promise<any> {
    this.requireAuthenticatedConnection();
    return this.callProcedure("listRouteTrains", { originStationId, destinationStationId }, false);
  }

  async trainStatus(trainId: string): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.setDetailTargets({ trainId });
    return this.withSchedule(this.buildTrainStatusView(trainId));
  }

  async trainStops(trainId: string): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.setDetailTargets({ trainId });
    return this.withSchedule(this.trainStopPayload(trainId));
  }

  async patchSettings(body: Record<string, unknown>): Promise<any> {
    this.requireAuthenticatedConnection();
    const next = body || {};
    await this.runReducer(async () => {
      if (Object.prototype.hasOwnProperty.call(next, "alertsEnabled")) {
        await this.reducer("setAlertsEnabled")({ enabled: Boolean(next.alertsEnabled) });
      }
      if (typeof next.alertStyle === "string" && next.alertStyle) {
        await this.reducer("setAlertStyle")({ style: String(next.alertStyle) });
      }
      if (typeof next.language === "string" && next.language) {
        await this.reducer("setLanguage")({ language: String(next.language) });
      }
    });
    return this.settingsPayload();
  }

  async saveFavoriteRoute(body: Record<string, unknown>): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("saveFavoriteRoute")({
      fromStationId: asString(body.fromStationId),
      toStationId: asString(body.toStationId),
      fromStationName: asString(body.fromStationName),
      toStationName: asString(body.toStationName),
    }));
    return { ok: true };
  }

  async deleteFavoriteRoute(body: Record<string, unknown>): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("deleteFavoriteRoute")({
      fromStationId: asString(body.fromStationId),
      toStationId: asString(body.toStationId),
    }));
    return { ok: true };
  }

  async checkIn(trainId: string, boardingStationId: string, fromMap: boolean, bundleIdentity?: BundleIdentity): Promise<any> {
    this.requireAuthenticatedConnection();
    const bundleVersion = asString(bundleIdentity?.version);
    const bundleServiceDate = asString(bundleIdentity?.serviceDate);
    await this.runReducer(() => {
      if (fromMap) {
        return this.reducer("checkInMap")({
          trainId,
          boardingStationId,
          bundleVersion,
          bundleServiceDate,
        });
      }
      return this.reducer("checkIn")({
        trainId,
        boardingStationId,
        bundleVersion,
        bundleServiceDate,
      });
    });
    await this.setDetailTargets({ trainId });
    return {
      currentRide: await this.resolvedCurrentRidePayload(trainId),
    };
  }

  async checkout(): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("checkout")());
    return { ok: true };
  }

  async undoCheckout(): Promise<any> {
    this.requireAuthenticatedConnection();
    const before = this.currentRideRow();
    const hadUndo = Boolean(asString(before?.undoTrainInstanceId).trim());
    await this.runReducer(() => this.reducer("undoCheckout")());
    const currentRide = await this.resolvedCurrentRidePayload();
    return {
      restored: Boolean(hadUndo && currentRide && currentRide.checkIn && currentRide.checkIn.trainInstanceId),
    };
  }

  async submitReport(trainId: string, signal: string, bundleIdentity?: BundleIdentity): Promise<any> {
    this.requireAuthenticatedConnection();
    const bundleVersion = asString(bundleIdentity?.version);
    const bundleServiceDate = asString(bundleIdentity?.serviceDate);
    try {
      await this.runReducer(() => this.reducer("submitReport")({
        trainId,
        signal,
        bundleVersion,
        bundleServiceDate,
      }));
      return {
        accepted: true,
        deduped: false,
        cooldownRemaining: 0,
      };
    } catch (error) {
      return this.reportErrorPayload(error);
    }
  }

  async submitStationSighting(stationId: string, body: Record<string, unknown>, bundleIdentity?: BundleIdentity): Promise<any> {
    this.requireAuthenticatedConnection();
    const destinationStationId = asString(body.destinationStationId);
    const trainId = asString(body.trainId);
    const bundleVersion = asString(bundleIdentity?.version);
    const bundleServiceDate = asString(bundleIdentity?.serviceDate);
    try {
      await this.runReducer(() => this.reducer("submitStationSighting")({
        stationId,
        destinationStationId,
        trainId,
        bundleVersion,
        bundleServiceDate,
      }));
      return {
        accepted: true,
        deduped: false,
        cooldownRemaining: 0,
        event: this.latestRecentSighting(stationId, destinationStationId, trainId),
      };
    } catch (error) {
      return this.stationSightingErrorPayload(error);
    }
  }

  async voteIncident(incidentId: string, value: string): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("voteIncident")({
      incidentId,
      value,
    }));
    return this.incidentVoteSummary(incidentId);
  }

  async commentIncident(incidentId: string, body: string): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("commentIncident")({
      incidentId,
      body,
    }));
    const comments = this.incidentComments(incidentId);
    return comments[0] || null;
  }

  async setTrainMute(trainId: string, durationMinutes: number): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("setTrainMute")({
      trainId,
      durationMinutes: durationMinutes > 0 ? Math.floor(durationMinutes) : 30,
    }));
    return { ok: true };
  }

  async setTrainSubscription(trainId: string, enabled: boolean, expiresAt: string): Promise<any> {
    this.requireAuthenticatedConnection();
    await this.runReducer(() => this.reducer("setTrainSubscription")({
      trainId,
      enabled,
      expiresAt,
    }));
    return { ok: true };
  }

  private normalizeToken(session?: SessionLike): string {
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
      await this.refreshPublicSubscription();
      await this.refreshUserSubscription();
      await this.refreshDetailSubscription();
      if (this.token) {
        await this.runReducer(() => this.reducer("bindSession")(), 750);
        await this.refreshUserSubscription();
      }
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

  private attachTableListeners(connection: DbConnection): void {
    const invalidate = () => {
      this.emitInvalidate();
    };
    const tablesToWatch = [
      this.runtimeStateTable(connection.db),
      this.serviceStationTable(connection.db),
      this.tripPublicTable(connection.db),
      this.tripStopTable(connection.db),
      this.tripTimelineTable(connection.db),
      this.publicSightingTable(connection.db),
      this.publicIncidentTable(connection.db),
      this.publicIncidentEventTable(connection.db),
      this.publicIncidentCommentTable(connection.db),
      this.publicDashboardLiveTable(connection.db),
      this.publicIncidentListLiveTable(connection.db),
      this.publicNetworkMapLiveTable(connection.db),
      connection.db.myProfile,
      connection.db.myFavorites,
      connection.db.myCurrentRide,
      connection.db.myTrainPrefs,
      connection.db.myIncidentVotes,
    ].filter(Boolean) as Array<{ onInsert: (cb: () => void) => void; onUpdate: (cb: () => void) => void; onDelete: (cb: () => void) => void }>;
    for (const tableView of tablesToWatch) {
      tableView.onInsert(invalidate);
      tableView.onUpdate(invalidate);
      tableView.onDelete(invalidate);
    }
  }

  private async refreshPublicSubscription(): Promise<void> {
    const dates = this.candidateServiceDates();
    const runtimeTable = this.runtimeStateTable(tables);
    const stationTable = this.serviceStationTable(tables);
    const dashboardLive = this.publicDashboardLiveTable(tables);
    const incidentListLive = this.publicIncidentListLiveTable(tables);
    const networkMapLive = this.publicNetworkMapLiveTable(tables);
    const queries = [
      runtimeTable.where((row: any) => row.id.eq("runtime")),
      ...dates.map((serviceDate) => stationTable.where((row: any) => row.serviceDate.eq(serviceDate))),
    ];
    if (dashboardLive) {
      queries.push(dashboardLive);
    }
    if (incidentListLive) {
      queries.push(incidentListLive);
    }
    if (networkMapLive) {
      queries.push(networkMapLive);
    } else {
      queries.push(...dates.map((serviceDate) => this.publicSightingTable(tables).where((row: any) => row.serviceDate.eq(serviceDate))));
    }
    this.publicSubscription = await this.replaceSubscription(this.publicSubscription, queries);
  }

  private async refreshUserSubscription(): Promise<void> {
    if (!this.connection || !this.token) {
      this.userSubscription = await this.replaceSubscription(this.userSubscription, []);
      return;
    }
    this.userSubscription = await this.replaceSubscription(this.userSubscription, [
      tables.myProfile,
      tables.myFavorites,
      tables.myCurrentRide,
      tables.myTrainPrefs,
      tables.myIncidentVotes,
    ]);
  }

  private async refreshDetailSubscription(): Promise<void> {
    if (!this.connection) {
      this.detailSubscription = await this.replaceSubscription(this.detailSubscription, []);
      return;
    }
    const queries: any[] = [];
    if (this.detailTargets.trainId) {
      queries.push(this.tripStopTable(tables).where((row: any) => row.trainId.eq(this.detailTargets.trainId)));
      queries.push(this.tripTimelineTable(tables).where((row: any) => row.trainId.eq(this.detailTargets.trainId)));
      queries.push(this.publicSightingTable(tables).where((row: any) => row.matchedTrainInstanceId.eq(this.detailTargets.trainId)));
    }
    if (this.detailTargets.incidentId) {
      queries.push(this.publicIncidentEventTable(tables).where((row: any) => row.incidentId.eq(this.detailTargets.incidentId)));
      queries.push(this.publicIncidentCommentTable(tables).where((row: any) => row.incidentId.eq(this.detailTargets.incidentId)));
    }
    this.detailSubscription = await this.replaceSubscription(this.detailSubscription, queries);
  }

  private async replaceSubscription(current: SubscriptionHandleLike, queries: any[]): Promise<SubscriptionHandleLike> {
    if (!this.connection || !queries.length) {
      if (current) {
        try {
          current.unsubscribe();
        } catch (_) {
          // Ignore stale handles during reconnects.
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
        // Ignore stale handles during reconnects.
      }
    }
    return nextHandle;
  }

  private async setDetailTargets(partial: Partial<DetailTargets>): Promise<boolean> {
    const nextTargets: DetailTargets = {
      trainId: typeof partial.trainId === "string" ? partial.trainId.trim() : this.detailTargets.trainId,
      stationId: typeof partial.stationId === "string" ? partial.stationId.trim() : this.detailTargets.stationId,
      incidentId: typeof partial.incidentId === "string" ? partial.incidentId.trim() : this.detailTargets.incidentId,
    };
    const changed = nextTargets.trainId !== this.detailTargets.trainId
      || nextTargets.stationId !== this.detailTargets.stationId
      || nextTargets.incidentId !== this.detailTargets.incidentId;
    if (!changed) {
      return false;
    }
    this.detailTargets = nextTargets;
    if (this.connection) {
      await this.refreshDetailSubscription();
    }
    return true;
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
    for (const handle of [this.publicSubscription, this.userSubscription, this.detailSubscription]) {
      if (!handle) {
        continue;
      }
      try {
        handle.unsubscribe();
      } catch (_) {
        // Ignore stale handles.
      }
    }
    this.publicSubscription = null;
    this.userSubscription = null;
    this.detailSubscription = null;
  }

  private emitInvalidate(): void {
    for (const listener of Array.from(this.listeners)) {
      try {
        listener();
      } catch (_) {
        // Keep the live client resilient to consumer errors.
      }
    }
  }

  private async runReducer<T>(runner: () => Promise<T>, timeoutMs = 500): Promise<T> {
    const followup = this.waitForInvalidate(timeoutMs);
    const result = await runner();
    await followup;
    return result;
  }

  private waitForInvalidate(timeoutMs: number): Promise<void> {
    return new Promise((resolve) => {
      let released = false;
      let timeoutId = 0;
      const cleanup = () => {
        if (released) {
          return;
        }
        released = true;
        release();
        if (timeoutId) {
          window.clearTimeout(timeoutId);
        }
        resolve();
      };
      const release = this.onInvalidate(cleanup);
      timeoutId = window.setTimeout(cleanup, timeoutMs);
    });
  }

  private requireConnection(): DbConnection {
    if (!this.connection) {
      throw new Error("spacetime connection unavailable");
    }
    return this.connection;
  }

  private requireLiveConnection(): DbConnection {
    const connection = this.requireConnection();
    if (this.state !== "live" && this.state !== "connecting" && this.state !== "reconnecting") {
      throw new Error("spacetime connection unavailable");
    }
    return connection;
  }

  private requireAuthenticatedConnection(): DbConnection {
    const connection = this.requireLiveConnection();
    if (!this.token) {
      throw new Error("auth required");
    }
    return connection;
  }

  private runtimeStateTable(source: any): any {
    return pickAccessor(source, ["runtimeState", "runtime_state", "trainbot_runtime_state"]);
  }

  private serviceStationTable(source: any): any {
    return pickAccessor(source, ["serviceStation", "service_station", "station", "trainbot_service_station", "trainbot_station"]);
  }

  private tripPublicTable(source: any): any {
    return pickAccessor(source, ["tripPublic", "trip_public", "trip", "trainbot_trip_public", "trainbot_trip"]);
  }

  private tripStopTable(source: any): any {
    return pickAccessor(source, ["tripStop", "trip_stop", "trainbot_trip_stop"]);
  }

  private tripTimelineTable(source: any): any {
    return pickAccessor(source, ["tripTimelineBucket", "trip_timeline_bucket", "trainbot_trip_timeline_bucket"]);
  }

  private publicSightingTable(source: any): any {
    return pickAccessor(source, ["publicSighting", "public_sighting", "trainbot_public_sighting"]);
  }

  private publicIncidentTable(source: any): any {
    return pickAccessor(source, ["publicIncident", "public_incident", "trainbot_public_incident"]);
  }

  private publicIncidentEventTable(source: any): any {
    return pickAccessor(source, ["publicIncidentEvent", "public_incident_event", "trainbot_public_incident_event"]);
  }

  private publicIncidentCommentTable(source: any): any {
    return pickAccessor(source, ["publicIncidentComment", "public_incident_comment", "trainbot_public_incident_comment"]);
  }

  private publicDashboardLiveTable(source: any): any | null {
    return maybeAccessor(source, ["publicDashboardLive", "trainbot_public_dashboard_live", "trainBotPublicDashboardLive"]);
  }

  private publicIncidentListLiveTable(source: any): any | null {
    return maybeAccessor(source, ["publicIncidentListLive", "trainbot_public_incident_list_live", "trainBotPublicIncidentListLive"]);
  }

  private publicNetworkMapLiveTable(source: any): any | null {
    return maybeAccessor(source, ["publicNetworkMapLive", "trainbot_public_network_map_live", "trainBotPublicNetworkMapLive"]);
  }

  private reducer(name: string): any {
    const connection = this.requireConnection();
    const suffix = `${name.charAt(0).toUpperCase()}${name.slice(1)}`;
    return pickAccessor(connection.reducers, [`trainbot${suffix}`, `trainBot${suffix}`, name]);
  }

  private procedure(name: string): any {
    const connection = this.requireConnection();
    const suffix = `${name.charAt(0).toUpperCase()}${name.slice(1)}`;
    return pickAccessor(connection.procedures, [`trainbot${suffix}`, `trainBot${suffix}`, name]);
  }

  private async callProcedure(name: string, params: Record<string, unknown>, allowAnonymous = false): Promise<any> {
    const token = this.token;
    if (!allowAnonymous && !token) {
      throw new Error("auth required");
    }
    return this.procedure(name)(params || {});
  }

  private db(): any {
    return this.requireConnection().db;
  }

  private nowDate(): Date {
    return new Date();
  }

  private nowISO(): string {
    return this.nowDate().toISOString();
  }

  private candidateServiceDates(): string[] {
    const now = this.nowDate();
    const offsets = [-1, 0, 1];
    return offsets.map((offset) => {
      const date = new Date(now.getTime() + offset * 24 * 60 * 60 * 1000);
      return formatServiceDateFor(date);
    });
  }

  private runtimeState(): any | null {
    const db = this.db();
    const table = this.runtimeStateTable(db);
    return table.id.find("runtime") || firstRow(table.iter());
  }

  private schedulePayload(): any {
    const runtime = this.runtimeState();
    if (runtime) {
      return {
        requestedServiceDate: asString(runtime.requestedServiceDate),
        effectiveServiceDate: asString(runtime.effectiveServiceDate),
        loadedServiceDate: asString(runtime.loadedServiceDate),
        fallbackActive: runtime.fallbackActive === true,
        cutoffHour: Number(runtime.cutoffHour) || 0,
        available: runtime.available === true,
        sameDayFresh: runtime.sameDayFresh === true,
      };
    }
    const now = this.nowDate();
    const requestedServiceDate = formatServiceDateFor(now);
    const fallbackServiceDate = formatServiceDateFor(new Date(now.getTime() - 24 * 60 * 60 * 1000));
    const cutoffHour = fallbackScheduleCutoffHour();
    const todayCount = this.listTripsForServiceDate(requestedServiceDate).length;
    const fallbackCount = this.listTripsForServiceDate(fallbackServiceDate).length;
    const beforeCutoff = isBeforeScheduleCutoff(now, cutoffHour);
    const available = todayCount > 0 || (beforeCutoff && fallbackCount > 0);
    const fallbackActive = todayCount === 0 && beforeCutoff && fallbackCount > 0;
    const effectiveServiceDate = todayCount > 0
      ? requestedServiceDate
      : fallbackActive
        ? fallbackServiceDate
        : "";
    return {
      requestedServiceDate,
      effectiveServiceDate,
      loadedServiceDate: effectiveServiceDate,
      fallbackActive,
      cutoffHour,
      available,
      sameDayFresh: todayCount > 0,
    };
  }

  private withSchedule(payload: Record<string, unknown>): any {
    return {
      ...payload,
      schedule: this.schedulePayload(),
    };
  }

  private activeServiceDate(): string {
    const schedule = this.schedulePayload();
    if (schedule.available && schedule.effectiveServiceDate) {
      return asString(schedule.effectiveServiceDate);
    }
    return formatServiceDateFor(this.nowDate());
  }

  private listStationsForServiceDate(serviceDate: string): any[] {
    const db = this.db();
    const stationTable = this.serviceStationTable(db);
    const stations = rowsFrom(stationTable.serviceDate.filter(serviceDate)).map((station) => ({
      id: asString(station.stationId || station.id),
      name: asString(station.name),
      normalizedKey: asString(station.normalizedKey),
      latitude: typeof station.latitude === "number" ? station.latitude : null,
      longitude: typeof station.longitude === "number" ? station.longitude : null,
      serviceDate: asString(station.serviceDate),
    }));
    stations.sort((left, right) => asString(left.name).localeCompare(asString(right.name)));
    return stations;
  }

  private stationById(stationId: string): any | null {
    const cleanId = stationId.trim();
    if (!cleanId) {
      return null;
    }
    const db = this.db();
    const preferredDates = [this.activeServiceDate(), formatServiceDateFor(new Date(this.nowDate().getTime() - 24 * 60 * 60 * 1000))];
    const seenDates = new Set<string>();
    const stationTable = this.serviceStationTable(db);
    for (const date of preferredDates) {
      if (!date || seenDates.has(date)) {
        continue;
      }
      seenDates.add(date);
      const match = rowsFrom(stationTable.serviceDate.filter(date)).find((station) => asString(station.stationId || station.id).trim() === cleanId);
      if (match) {
        return {
          id: asString(match.stationId || match.id),
          name: asString(match.name),
          normalizedKey: asString(match.normalizedKey),
          latitude: match.latitude,
          longitude: match.longitude,
          serviceDate: asString(match.serviceDate),
        };
      }
    }
    const fallback = rowsFrom(stationTable.iter()).find((station) => asString(station.stationId || station.id).trim() === cleanId);
    return fallback ? {
      id: asString(fallback.stationId || fallback.id),
      name: asString(fallback.name),
      normalizedKey: asString(fallback.normalizedKey),
      latitude: fallback.latitude,
      longitude: fallback.longitude,
      serviceDate: asString(fallback.serviceDate),
    } : null;
  }

  private stationNameFor(stationId: string): string {
    return asString(this.stationById(stationId)?.name);
  }

  private listTripsForServiceDate(serviceDate: string): any[] {
    const db = this.db();
    const liveRows = this.publicDashboardLiveTable(db);
    const sourceRows = liveRows ? rowsFrom(liveRows.iter()) : rowsFrom(this.tripPublicTable(db).serviceDate.filter(serviceDate));
    const trips = sourceRows.filter((trip) => !serviceDate || asString(trip.serviceDate) === serviceDate);
    trips.sort((left, right) => compareTimeAscending(asString(left.departureAt), asString(right.departureAt)));
    return trips;
  }

  private liveTripById(trainId: string): any | null {
    const liveRows = this.publicDashboardLiveTable(this.db());
    if (!liveRows) {
      return null;
    }
    return rowsFrom(liveRows.iter()).find((item) => asString(item.id) === trainId) || null;
  }

  private trainById(trainId: string): any | null {
    const db = this.db();
    const tripPublic = this.tripPublicTable(db);
    return tripPublic.id.find(trainId) || this.liveTripById(trainId);
  }

  private tripStopsSorted(trainId: string): any[] {
    const db = this.db();
    const stops = rowsFrom(this.tripStopTable(db).trainId.filter(trainId));
    stops.sort((left, right) => Number(left.seq) - Number(right.seq));
    return stops;
  }

  private trainState(trainId: string): any {
    const db = this.db();
    const trip = this.tripPublicTable(db).id.find(trainId);
    const live = maybeAccessor(db, ["trainbot_trip_live"])?.trainId?.find?.(trainId) || null;
    const source = trip || live;
    if (!source) {
      return {
        state: "NO_REPORTS",
        confidence: "LOW",
        uniqueReporters: 0,
      };
    }
    return {
      state: asString(source.state),
      confidence: asString(source.confidence),
      uniqueReporters: Number(source.uniqueReporters) || 0,
      lastReportAt: trimOptional(asString(source.lastReportAt)) || "",
    };
  }

  private recentTimeline(trainId: string, limit: number): any[] {
    const db = this.db();
    const trip = this.trainById(trainId);
    const projected = Array.isArray(trip?.recentTimeline)
      ? trip.recentTimeline.map((item: any) => ({
        at: asString(item.at),
        signal: asString(item.signal),
        count: Number(item.count) || 0,
      }))
      : [];
    const buckets = projected.length ? projected : rowsFrom(this.tripTimelineTable(db).trainId.filter(trainId));
    buckets.sort((left, right) => compareTimeDescending(asString(left.at), asString(right.at)));
    const mapped = buckets.map((item) => ({
      at: asString(item.at),
      signal: asString(item.signal),
      count: Number(item.count) || 0,
    }));
    return limit > 0 ? mapped.slice(0, limit) : mapped;
  }

  private activeRidersForTrain(trainId: string): number {
    const db = this.db();
    const trip = this.tripPublicTable(db).id.find(trainId);
    const live = maybeAccessor(db, ["trainbot_trip_live"])?.trainId?.find?.(trainId) || null;
    return trip ? Number(trip.riders) || 0 : live ? Number(live.riders) || 0 : 0;
  }

  private buildTrainCard(train: any): any {
    return {
      train: {
        id: asString(train.id),
        serviceDate: asString(train.serviceDate),
        fromStation: asString(train.fromStationName),
        toStation: asString(train.toStationName),
        departureAt: asString(train.departureAt),
        arrivalAt: asString(train.arrivalAt),
        sourceVersion: asString(train.sourceVersion),
      },
      status: this.trainState(asString(train.id)),
      riders: this.activeRidersForTrain(asString(train.id)),
    };
  }

  private stationSightingsSince(sinceMs: number, limit: number): any[] {
    const db = this.db();
    const mapLive = this.publicNetworkMapLiveTable(db);
    const sourceRows = mapLive
      ? rowsFrom(mapLive.iter()).filter((item) => asString(item.kind).trim() === "sighting")
      : rowsFrom(this.publicSightingTable(db).iter());
    const items = sourceRows.filter((item) => {
      const createdMs = parseISO(asString(item.createdAt))?.getTime() || 0;
      return createdMs >= sinceMs;
    }).map((item) => ({
      id: asString(item.id),
      incidentId: asString(item.incidentId),
      stationId: asString(item.stationId),
      stationName: asString(item.stationName),
      destinationStationId: asString(item.destinationStationId),
      destinationStationName: asString(item.destinationStationName),
      matchedTrainInstanceId: asString(item.matchedTrainInstanceId),
      createdAt: asString(item.createdAt),
      isRecent: item.isRecent === true,
    }));
    items.sort((left, right) => compareTimeDescending(left.createdAt, right.createdAt));
    return limit > 0 ? items.slice(0, limit) : items;
  }

  private recentStationSightingsByStation(stationId: string, minutes: number, limit: number): any[] {
    const sinceMs = this.nowDate().getTime() - minutes * 60 * 1000;
    const items = this.stationSightingsSince(sinceMs, 500).filter((item) => item.stationId === stationId);
    return limit > 0 ? items.slice(0, limit) : items;
  }

  private stationSightingsByStationSince(stationId: string, sinceMs: number, limit: number): any[] {
    const items = this.stationSightingsSince(sinceMs, 500).filter((item) => item.stationId === stationId);
    return limit > 0 ? items.slice(0, limit) : items;
  }

  private recentStationSightingsByTrain(trainId: string, minutes: number, limit: number): any[] {
    const sinceMs = this.nowDate().getTime() - minutes * 60 * 1000;
    const items = this.stationSightingsSince(sinceMs, 500).filter((item) => item.matchedTrainInstanceId === trainId);
    return limit > 0 ? items.slice(0, limit) : items;
  }

  private stationSightingContextForPassAt(items: any[], passAt: string): any[] {
    const passMs = parseISO(passAt)?.getTime() || 0;
    return items.filter((item) => {
      const createdMs = parseISO(item.createdAt)?.getTime() || 0;
      return Math.abs(createdMs - passMs) <= 30 * 60 * 1000;
    });
  }

  private buildTrainStatusView(trainId: string): any {
    const train = this.trainById(trainId);
    if (!train) {
      throw new Error("not found");
    }
    return {
      trainCard: this.buildTrainCard(train),
      timeline: this.recentTimeline(trainId, 5),
      stationSightings: this.recentStationSightingsByTrain(trainId, 30, 5),
    };
  }

  private buildPublicTrainView(trainId: string): any {
    const train = this.trainById(trainId);
    if (!train) {
      throw new Error("not found");
    }
    return {
      train: {
        id: asString(train.id),
        serviceDate: asString(train.serviceDate),
        fromStation: asString(train.fromStationName),
        toStation: asString(train.toStationName),
        departureAt: asString(train.departureAt),
        arrivalAt: asString(train.arrivalAt),
        sourceVersion: asString(train.sourceVersion),
      },
      status: this.trainState(trainId),
      riders: this.activeRidersForTrain(trainId),
      timeline: this.recentTimeline(trainId, 5),
      stationSightings: this.recentStationSightingsByTrain(trainId, 30, 5),
    };
  }

  private trainsByWindow(windowId: string): any[] {
    const now = this.nowDate();
    let start = new Date(now.getTime());
    let end = new Date(now.getTime());
    switch (windowId.trim()) {
      case "now":
        start = new Date(now.getTime() - 15 * 60 * 1000);
        end = new Date(now.getTime() + 15 * 60 * 1000);
        break;
      case "next_hour":
        start = now;
        end = new Date(now.getTime() + 60 * 60 * 1000);
        break;
      case "today":
      default:
        start = new Date(now.getTime() - 30 * 60 * 1000);
        end = utcDayEnd(now);
        break;
    }
    const startMs = start.getTime();
    const endMs = end.getTime();
    return this.listTripsForServiceDate(this.activeServiceDate()).filter((train) => {
      const departureMs = parseISO(asString(train.departureAt))?.getTime() || 0;
      return departureMs >= startMs && departureMs <= endMs;
    });
  }

  private stopPassAt(stop: any): string {
    return asString(stop.departureAt || stop.arrivalAt || "");
  }

  private stopArrivalOrDeparture(stop: any): string {
    return asString(stop.arrivalAt || stop.departureAt || "");
  }

  private stationWindowTrains(stationId: string, startMs: number, endMs: number): any[] {
    const items: any[] = [];
    for (const train of this.listTripsForServiceDate(this.activeServiceDate())) {
      const stops = this.tripStopsSorted(asString(train.id));
      for (const stop of stops) {
        if (asString(stop.stationId) !== stationId) {
          continue;
        }
        const passAt = this.stopPassAt(stop);
        const passMs = parseISO(passAt)?.getTime() || 0;
        if (passMs < startMs || passMs > endMs) {
          continue;
        }
        items.push({
          train,
          stationId,
          stationName: asString(stop.stationName),
          passAt,
        });
        break;
      }
    }
    items.sort((left, right) => compareTimeAscending(left.passAt, right.passAt));
    return items;
  }

  private buildStationTrainCards(items: any[], sightings: any[]): any[] {
    return items.map((item) => ({
      trainCard: this.buildTrainCard(item.train),
      stationId: item.stationId,
      stationName: item.stationName,
      passAt: item.passAt,
      sightingCount: this.stationSightingContextForPassAt(sightings, item.passAt).length,
      sightingContext: this.stationSightingContextForPassAt(sightings, item.passAt),
    }));
  }

  private routeWindowTrains(fromStationId: string, toStationId: string, startMs: number, endMs: number): any[] {
    const items: any[] = [];
    for (const train of this.listTripsForServiceDate(this.activeServiceDate())) {
      const stops = this.tripStopsSorted(asString(train.id));
      let fromStop: any = null;
      let toStop: any = null;
      for (const stop of stops) {
        if (!fromStop && asString(stop.stationId) === fromStationId) {
          fromStop = stop;
          continue;
        }
        if (fromStop && asString(stop.stationId) === toStationId && Number(stop.seq) > Number(fromStop.seq)) {
          toStop = stop;
          break;
        }
      }
      if (!fromStop || !toStop) {
        continue;
      }
      const fromPassAt = this.stopPassAt(fromStop);
      const fromMs = parseISO(fromPassAt)?.getTime() || 0;
      if (fromMs < startMs || fromMs > endMs) {
        continue;
      }
      items.push({
        train,
        fromStationId,
        fromStationName: asString(fromStop.stationName),
        toStationId,
        toStationName: asString(toStop.stationName),
        fromPassAt,
        toPassAt: this.stopArrivalOrDeparture(toStop),
      });
    }
    items.sort((left, right) => compareTimeAscending(left.fromPassAt, right.fromPassAt));
    return items;
  }

  private routeDestinations(fromStationId: string): any[] {
    const destinations = new Map<string, any>();
    for (const train of this.listTripsForServiceDate(this.activeServiceDate())) {
      const stops = this.tripStopsSorted(asString(train.id));
      let seenOrigin = false;
      for (const stop of stops) {
        if (!seenOrigin) {
          if (asString(stop.stationId) === fromStationId) {
            seenOrigin = true;
          }
          continue;
        }
        const stopStationId = asString(stop.stationId);
        if (!stopStationId || destinations.has(stopStationId)) {
          continue;
        }
        const station = this.stationById(stopStationId);
        if (station) {
          destinations.set(stopStationId, station);
        }
      }
    }
    const items = Array.from(destinations.values());
    items.sort((left, right) => asString(left.name).localeCompare(asString(right.name)));
    return items;
  }

  private terminalDestinations(fromStationId: string): any[] {
    const destinations = new Map<string, any>();
    for (const train of this.listTripsForServiceDate(this.activeServiceDate())) {
      const stops = this.tripStopsSorted(asString(train.id));
      const originIndex = stops.findIndex((stop) => asString(stop.stationId) === fromStationId);
      if (originIndex < 0 || originIndex >= stops.length - 1) {
        continue;
      }
      const terminal = stops[stops.length - 1];
      if (!terminal || Number(terminal.seq) <= Number(stops[originIndex].seq)) {
        continue;
      }
      const station = this.stationById(asString(terminal.stationId));
      if (station) {
        destinations.set(asString(terminal.stationId), station);
      }
    }
    const items = Array.from(destinations.values());
    items.sort((left, right) => asString(left.name).localeCompare(asString(right.name)));
    return items;
  }

  private maybeResolveMatchedTrain(stationId: string, destinationStationId: string | undefined): string {
    if (!destinationStationId) {
      return "";
    }
    const now = this.nowDate();
    const candidates = this.routeWindowTrains(
      stationId,
      destinationStationId,
      now.getTime() - STATION_MATCH_PAST_WINDOW_MS,
      now.getTime() + STATION_MATCH_FUTURE_WINDOW_MS,
    ).map((item) => asString(item.train.id));
    const unique = Array.from(new Set(candidates));
    return unique.length === 1 ? unique[0] : "";
  }

  private trainStopPayload(trainId: string): any {
    const train = this.trainById(trainId);
    if (!train) {
      throw new Error("not found");
    }
    return {
      trainCard: this.buildTrainCard(train),
      train: {
        id: asString(train.id),
        serviceDate: asString(train.serviceDate),
        fromStation: asString(train.fromStationName),
        toStation: asString(train.toStationName),
        departureAt: asString(train.departureAt),
        arrivalAt: asString(train.arrivalAt),
        sourceVersion: asString(train.sourceVersion),
      },
      stops: this.tripStopsSorted(trainId),
      stationSightings: this.recentStationSightingsByTrain(trainId, 30, 10),
    };
  }

  private publicDashboardPayload(limit: number): any[] {
    const trains = this.trainsByWindow("today");
    const trimmed = limit > 0 ? trains.slice(0, limit) : trains;
    return trimmed.map((train) => ({
      train: {
        id: asString(train.id),
        serviceDate: asString(train.serviceDate),
        fromStation: asString(train.fromStationName),
        toStation: asString(train.toStationName),
        departureAt: asString(train.departureAt),
        arrivalAt: asString(train.arrivalAt),
        sourceVersion: asString(train.sourceVersion),
      },
      status: this.trainState(asString(train.id)),
      riders: this.activeRidersForTrain(asString(train.id)),
      timeline: this.recentTimeline(asString(train.id), 5),
      stationSightings: [],
    }));
  }

  private publicServiceDayPayload(): any[] {
    return this.listTripsForServiceDate(this.activeServiceDate()).map((train) => ({
      train: {
        id: asString(train.id),
        serviceDate: asString(train.serviceDate),
        fromStation: asString(train.fromStationName),
        toStation: asString(train.toStationName),
        departureAt: asString(train.departureAt),
        arrivalAt: asString(train.arrivalAt),
        sourceVersion: asString(train.sourceVersion),
      },
      status: this.trainState(asString(train.id)),
      riders: this.activeRidersForTrain(asString(train.id)),
      timeline: this.recentTimeline(asString(train.id), 5),
      stationSightings: [],
    }));
  }

  private publicStationDeparturesPayload(stationId: string, limit: number): any {
    const station = this.stationById(stationId);
    if (!station) {
      throw new Error("not found");
    }
    const now = this.nowDate();
    const recent = this.stationWindowTrains(stationId, utcDayStart(now).getTime(), now.getTime() - 1);
    const upcoming = this.stationWindowTrains(stationId, now.getTime(), utcDayEnd(now).getTime());
    const lastDeparture = recent.length
      ? this.buildStationTrainCards(recent.slice(-1), [])[0]
      : null;
    return {
      station,
      lastDeparture,
      upcoming: this.buildStationTrainCards(limit > 0 ? upcoming.slice(0, limit) : upcoming, []),
      recentSightings: this.recentStationSightingsByStation(stationId, 30, 10),
    };
  }

  private stationDeparturesPayload(stationId: string): any {
    const station = this.stationById(stationId);
    if (!station) {
      throw new Error("not found");
    }
    const now = this.nowDate();
    const startMs = now.getTime() - 2 * 60 * 60 * 1000;
    const endMs = now.getTime() + 2 * 60 * 60 * 1000;
    const trains = this.stationWindowTrains(stationId, startMs, endMs);
    const contextSightings = this.stationSightingsByStationSince(stationId, startMs - 30 * 60 * 1000, 250);
    return {
      station,
      trains: this.buildStationTrainCards(trains, contextSightings),
      recentSightings: this.recentStationSightingsByStation(stationId, 30, 10),
    };
  }

  private networkMapPayload(): any {
    const db = this.db();
    const mapLive = this.publicNetworkMapLiveTable(db);
    const stationRows = mapLive
      ? rowsFrom(mapLive.iter()).filter((item) => asString(item.kind).trim() === "station")
      : this.listStationsForServiceDate(this.activeServiceDate());
    const stations = stationRows.filter((item) => item.latitude != null && item.longitude != null).map((item) => ({
      id: asString(item.stationId || item.id),
      name: asString(item.stationName || item.name),
      normalizedKey: asString(item.normalizedKey),
      latitude: item.latitude,
      longitude: item.longitude,
    }));
    const now = this.nowDate();
    const sameDaySightings = this.stationSightingsSince(utcDayStart(now).getTime(), 500);
    const visibleTrainIds = new Set(this.publicDashboardPayload(0).map((item) => asString(item.train?.id)));
    const recentSightings = sameDaySightings.filter((item) => {
      const createdMs = parseISO(item.createdAt)?.getTime() || 0;
      return createdMs >= now.getTime() - NETWORK_RECENT_MS
        && item.matchedTrainInstanceId !== ""
        && visibleTrainIds.has(item.matchedTrainInstanceId);
    });
    return {
      stations,
      recentSightings,
      sameDaySightings,
    };
  }

  private incidentVoteSummary(incidentId: string): any {
    const db = this.db();
    const summary = this.publicIncidentTable(db).id.find(incidentId);
    const userValue = this.myIncidentVoteValue(incidentId);
    return {
      ongoing: summary ? Number(summary.ongoingVotes) || 0 : 0,
      cleared: summary ? Number(summary.clearedVotes) || 0 : 0,
      userValue,
    };
  }

  private incidentSummaryPayload(row: any): any {
    return {
      id: asString(row.id),
      scope: asString(row.scopeType),
      subjectId: asString(row.subjectId),
      subjectName: asString(row.subjectName),
      lastReportName: asString(row.lastReportName),
      lastReportAt: asString(row.lastReportAt),
      lastActivityName: asString(row.lastActivityName),
      lastActivityAt: asString(row.lastActivityAt),
      lastActivityActor: asString(row.lastActivityActor),
      lastReporter: asString(row.lastReporter),
      commentCount: Number(row.commentCount) || 0,
      votes: this.incidentVoteSummary(asString(row.id)),
      active: row.active === true,
    };
  }

  private listIncidentSummariesPayload(limit: number): any[] {
    const db = this.db();
    const liveTable = this.publicIncidentListLiveTable(db);
    const serviceDate = this.activeServiceDate();
    const sourceRows = liveTable ? rowsFrom(liveTable.iter()) : rowsFrom(this.publicIncidentTable(db).serviceDate.filter(serviceDate));
    const items = sourceRows
      .filter((item) => !serviceDate || asString(item.serviceDate) === serviceDate)
      .sort((left, right) => compareTimeDescending(asString(left.lastActivityAt), asString(right.lastActivityAt)))
      .map((item) => this.incidentSummaryPayload(item));
    return limit > 0 ? items.slice(0, limit) : items;
  }

  private incidentEvents(incidentId: string): any[] {
    const db = this.db();
    const events = rowsFrom(this.publicIncidentEventTable(db).incidentId.filter(incidentId)).map((item) => ({
      id: asString(item.id),
      kind: asString(item.kind),
      name: asString(item.name),
      detail: asString(item.detail),
      nickname: asString(item.nickname),
      createdAt: asString(item.createdAt),
    }));
    events.sort((left, right) => compareTimeDescending(left.createdAt, right.createdAt));
    return events;
  }

  private incidentComments(incidentId: string): any[] {
    const db = this.db();
    const comments = rowsFrom(this.publicIncidentCommentTable(db).incidentId.filter(incidentId)).map((item) => ({
      id: asString(item.id),
      nickname: asString(item.nickname),
      body: asString(item.body),
      createdAt: asString(item.createdAt),
    }));
    comments.sort((left, right) => compareTimeDescending(left.createdAt, right.createdAt));
    return comments;
  }

  private incidentDetailPayload(incidentId: string): any {
    const db = this.db();
    const summaryRow = this.publicIncidentTable(db).id.find(incidentId);
    if (!summaryRow) {
      throw new Error("not found");
    }
    const summary = this.incidentSummaryPayload(summaryRow);
    const comments = this.incidentComments(incidentId);
    const commentEvents = comments.map((item) => ({
      id: item.id,
      kind: "comment",
      name: incidentCommentActivityLabel(),
      detail: item.body,
      nickname: item.nickname,
      createdAt: item.createdAt,
    }));
    const events = [...this.incidentEvents(incidentId), ...commentEvents];
    events.sort((left, right) => compareTimeDescending(left.createdAt, right.createdAt));
    return {
      summary,
      events,
      comments,
    };
  }

  private settingsPayload(): any {
    const profile = this.profileRow();
    const updatedAt = trimOptional(asString(profile?.updatedAt)) || this.nowISO();
    return {
      alertsEnabled: profile ? profile.alertsEnabled !== false : true,
      alertStyle: normalizeAlertStyle(asString(profile?.alertStyle || "DETAILED")),
      language: normalizeLanguage(asString(profile?.language || "EN")),
      updatedAt,
    };
  }

  private favoriteListPayload(): any {
    const favorites = rowsFrom(this.db().myFavorites.iter()).slice();
    favorites.sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)));
    return {
      favorites: favorites.map((row) => ({
        fromStationId: asString(row.fromStationId),
        fromStationName: asString(row.fromStationName),
        toStationId: asString(row.toStationId),
        toStationName: asString(row.toStationName),
      })),
    };
  }

  private profileRow(): any | null {
    return firstRow(this.db().myProfile.iter());
  }

  private currentRideRow(): any | null {
    return firstRow(this.db().myCurrentRide.iter());
  }

  private myIncidentVoteValue(incidentId: string): string {
    const match = rowsFrom(this.db().myIncidentVotes.iter()).find((item) => asString(item.incidentId) === incidentId);
    return match ? asString(match.value).trim().toUpperCase() : "";
  }

  private async currentRidePayload(): Promise<any> {
    const ride = this.currentRideRow();
    if (!ride || !asString(ride.trainInstanceId).trim()) {
      return null;
    }
    const trainId = asString(ride.trainInstanceId).trim();
    await this.setDetailTargets({ trainId });
    const autoCheckoutAt = trimOptional(asString(ride.autoCheckoutAt)) || isoPlus(this.nowISO(), CHECKIN_FALLBACK_WINDOW_MS);
    const autoCheckoutDate = parseISO(autoCheckoutAt);
    if (!autoCheckoutDate || autoCheckoutDate.getTime() < this.nowDate().getTime()) {
      return null;
    }
    const boardingStationId = asString(ride.boardingStationId).trim();
    let trainView: any = null;
    try {
      trainView = this.buildTrainStatusView(trainId);
    } catch (_) {
      // Old remembered rides can outlive the local train snapshot during service-day rollover.
      // Keep the ride readable so higher-level fallbacks can recover instead of surfacing "not found".
      trainView = null;
    }
    return {
      checkIn: {
        trainInstanceId: trainId,
        boardingStationId,
        checkedInAt: asString(ride.checkedInAt),
        autoCheckoutAt,
      },
      train: trainView,
      boardingStationId,
      boardingStationName: this.stationNameFor(boardingStationId),
    };
  }

  private async currentRideProcedurePayload(): Promise<any> {
    try {
      const payload = await this.callProcedure("getCurrentRide", {}, false);
      const ride = payload && payload.currentRide ? payload.currentRide : null;
      if (!ride || !ride.checkIn || !asString(ride.checkIn.trainInstanceId).trim()) {
        return null;
      }
      const trainId = asString(ride.checkIn.trainInstanceId).trim();
      await this.setDetailTargets({ trainId });
      if (!ride.train) {
        try {
          ride.train = this.buildTrainStatusView(trainId);
        } catch (_) {
          // Keep the fallback tolerant when projected train detail is still catching up.
        }
      }
      const boardingStationId = asString(ride.boardingStationId || ride.checkIn.boardingStationId).trim();
      if (!ride.boardingStationName && boardingStationId) {
        ride.boardingStationName = this.stationNameFor(boardingStationId);
      }
      if (!ride.boardingStationId && boardingStationId) {
        ride.boardingStationId = boardingStationId;
      }
      return ride;
    } catch (_) {
      return null;
    }
  }

  private async resolvedCurrentRidePayload(expectedTrainId = ""): Promise<any> {
    const normalizedExpectedTrainId = asString(expectedTrainId).trim();
    const maxAttempts = normalizedExpectedTrainId ? CURRENT_RIDE_SETTLE_RETRIES : 1;
    let fallbackRide: any = null;
    for (let attempt = 0; attempt < maxAttempts; attempt += 1) {
      let localRide: any = null;
      try {
        localRide = await this.currentRidePayload();
      } catch (_) {
        localRide = null;
      }
      const localTrainId = asString(localRide?.checkIn?.trainInstanceId).trim();
      if (localRide && (!normalizedExpectedTrainId || localTrainId === normalizedExpectedTrainId)) {
        return localRide;
      }
      if (!normalizedExpectedTrainId && localRide) {
        fallbackRide = localRide;
      }

      const procedureRide = await this.currentRideProcedurePayload();
      const procedureTrainId = asString(procedureRide?.checkIn?.trainInstanceId).trim();
      if (procedureRide && (!normalizedExpectedTrainId || procedureTrainId === normalizedExpectedTrainId)) {
        return procedureRide;
      }
      if (!normalizedExpectedTrainId && procedureRide) {
        fallbackRide = procedureRide;
      }

      if (attempt + 1 < maxAttempts) {
        await sleepMs(CURRENT_RIDE_SETTLE_DELAY_MS);
      }
    }
    return normalizedExpectedTrainId ? null : fallbackRide;
  }

  private latestRecentSighting(stationId: string, destinationStationId: string, trainId: string): any | null {
    const candidates = this.recentStationSightingsByStation(stationId, 10, 25).filter((item) => {
      if (destinationStationId && item.destinationStationId !== destinationStationId) {
        return false;
      }
      if (trainId && item.matchedTrainInstanceId && item.matchedTrainInstanceId !== trainId) {
        return false;
      }
      return true;
    });
    if (candidates.length) {
      return candidates[0];
    }
    if (!destinationStationId) {
      return null;
    }
    const matchedTrainInstanceId = trainId || this.maybeResolveMatchedTrain(stationId, destinationStationId);
    return {
      matchedTrainInstanceId,
    };
  }

  private reportErrorPayload(error: unknown): any {
    const message = error instanceof Error ? error.message : String(error || "");
    const duplicate = /duplicate report ignored/i.test(message);
    const cooldownMatch = message.match(/report cooldown active for\s+(\d+)s/i);
    return {
      accepted: false,
      deduped: duplicate,
      cooldownRemaining: cooldownMatch ? Number(cooldownMatch[1]) * 1000000000 : 0,
    };
  }

  private stationSightingErrorPayload(error: unknown): any {
    const message = error instanceof Error ? error.message : String(error || "");
    const duplicate = /duplicate station sighting ignored/i.test(message);
    const cooldownMatch = message.match(/station sighting cooldown active for\s+(\d+)s/i);
    return {
      accepted: false,
      deduped: duplicate,
      cooldownRemaining: cooldownMatch ? Number(cooldownMatch[1]) * 1000000000 : 0,
      event: null,
    };
  }
}

declare global {
  interface Window {
    TrainAppLiveClient?: {
      create: (config: LiveClientConfig) => TrainAppLiveClient;
    };
  }
}

window.TrainAppLiveClient = {
  create(config: LiveClientConfig) {
    return new TrainAppLiveClient(config);
  },
};
