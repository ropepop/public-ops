// @ts-nocheck
import {
  CaseConversionPolicy,
  SenderError,
  schema,
  table,
  t,
} from 'spacetimedb/server';
import { ScheduleAt } from 'spacetimedb';

const REPORT_COOLDOWN_MS = 3 * 60 * 1000;
const REPORT_DEDUPE_MS = 90 * 1000;
const STATION_SIGHTING_COOLDOWN_MS = 3 * 60 * 1000;
const STATION_SIGHTING_DEDUPE_MS = 90 * 1000;
const UNDO_CHECKOUT_WINDOW_MS = 10 * 1000;
const CHECKIN_GRACE_MS = 10 * 60 * 1000;
const CHECKIN_FALLBACK_WINDOW_MS = 6 * 60 * 60 * 1000;
const STATION_MATCH_PAST_WINDOW_MS = 5 * 60 * 1000;
const STATION_MATCH_FUTURE_WINDOW_MS = 90 * 60 * 1000;
const TRAIN_ACTIVITY_ACTIVE_MS = 15 * 60 * 1000;
const STATION_ACTIVITY_ACTIVE_MS = 30 * 60 * 1000;
const ACTIVITY_RETENTION_MS = 7 * 24 * 60 * 60 * 1000;
const TRAINBOT_DB_PREFIX = 'trainbot_';
const CLEANUP_RETENTION_POLICY_ID = 'cleanup-retention-policy';
const RIGA_TIME_ZONE = 'Europe/Riga';
const DEFAULT_SCHEDULE_CUTOFF_HOUR = 3;

const rigaScheduleFormatter = new Intl.DateTimeFormat('en-CA', {
  timeZone: RIGA_TIME_ZONE,
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  hourCycle: 'h23',
});

function named(suffix: string): string {
  return `${TRAINBOT_DB_PREFIX}${suffix}`;
}

const stationDoc = t.object('TrainbotStationDoc', {
  id: t.string(),
  name: t.string(),
  normalizedKey: t.string(),
  latitude: t.option(t.number()),
  longitude: t.option(t.number()),
});

const stopDoc = t.object('TrainbotStopDoc', {
  stationId: t.string(),
  stationName: t.string(),
  seq: t.u32(),
  arrivalAt: t.option(t.string()),
  departureAt: t.option(t.string()),
  latitude: t.option(t.number()),
  longitude: t.option(t.number()),
});

const settingsDoc = t.object('TrainbotSettingsDoc', {
  alertsEnabled: t.bool(),
  alertStyle: t.string(),
  language: t.string(),
  updatedAt: t.string(),
});

const favoriteDoc = t.object('TrainbotFavoriteDoc', {
  fromStationId: t.string(),
  fromStationName: t.string(),
  toStationId: t.string(),
  toStationName: t.string(),
  createdAt: t.string(),
});

const rideDoc = t.object('TrainbotRideDoc', {
  trainInstanceId: t.string(),
  boardingStationId: t.string(),
  checkedInAt: t.string(),
  autoCheckoutAt: t.string(),
});

const undoRideDoc = t.object('TrainbotUndoRideDoc', {
  trainInstanceId: t.string(),
  boardingStationId: t.string(),
  checkedInAt: t.string(),
  autoCheckoutAt: t.string(),
  expiresAt: t.string(),
});

const muteDoc = t.object('TrainbotMuteDoc', {
  trainInstanceId: t.string(),
  mutedUntil: t.string(),
  createdAt: t.string(),
});

const subscriptionDoc = t.object('TrainbotSubscriptionDoc', {
  trainInstanceId: t.string(),
  expiresAt: t.string(),
  isActive: t.bool(),
  createdAt: t.string(),
  updatedAt: t.string(),
});

const recentActionStateDoc = t.object('TrainbotRecentActionStateDoc', {
  updatedAt: t.string(),
});

const activitySummaryDoc = t.object('TrainbotActivitySummaryDoc', {
  lastReportName: t.string(),
  lastReportAt: t.string(),
  lastActivityName: t.string(),
  lastActivityAt: t.string(),
  lastActivityActor: t.string(),
  lastReporter: t.string(),
});

const activityTimelineDoc = t.object('TrainbotActivityTimelineDoc', {
  id: t.string(),
  kind: t.string(),
  stableId: t.string(),
  nickname: t.string(),
  name: t.string(),
  detail: t.string(),
  createdAt: t.string(),
  signal: t.string(),
  trainInstanceId: t.string(),
  stationId: t.string(),
  stationName: t.string(),
  destinationStationId: t.string(),
  destinationStationName: t.string(),
  matchedTrainInstanceId: t.string(),
});

const activityCommentDoc = t.object('TrainbotActivityCommentDoc', {
  id: t.string(),
  stableId: t.string(),
  nickname: t.string(),
  body: t.string(),
  createdAt: t.string(),
});

const activityVoteDoc = t.object('TrainbotActivityVoteDoc', {
  stableId: t.string(),
  nickname: t.string(),
  value: t.string(),
  createdAt: t.string(),
  updatedAt: t.string(),
});

const timelineBucketDoc = t.object('TrainbotTimelineBucketDoc', {
  at: t.string(),
  signal: t.string(),
  count: t.u32(),
});

const feedImportDoc = t.object('TrainbotFeedImportDoc', {
  importId: t.string(),
  serviceDate: t.string(),
  sourceVersion: t.string(),
  status: t.string(),
  eventCount: t.u32(),
  committedAt: t.string(),
  abortedAt: t.string(),
  createdAt: t.string(),
  updatedAt: t.string(),
});

const feedEventDoc = t.object('TrainbotFeedEventDoc', {
  id: t.string(),
  importId: t.string(),
  serviceDate: t.string(),
  kind: t.string(),
  entityId: t.string(),
  sourceVersion: t.string(),
  createdAt: t.string(),
  payloadJson: t.string(),
});

const trainbot_service_day = table(
  { name: named('service_day'), public: true },
  {
    serviceDate: t.string().primaryKey(),
    sourceVersion: t.string(),
    importedAt: t.string(),
    stations: t.array(stationDoc),
  }
);

const trainbot_trip = table(
  { name: named('trip'), public: true },
  {
    id: t.string().primaryKey(),
    serviceDate: t.string().index(),
    fromStationId: t.string(),
    fromStationName: t.string(),
    toStationId: t.string(),
    toStationName: t.string(),
    departureAt: t.string(),
    arrivalAt: t.string(),
    sourceVersion: t.string(),
    stops: t.array(stopDoc),
  }
);

const trainbot_rider = table(
  { name: named('rider') },
  {
    stableId: t.string().primaryKey(),
    telegramUserId: t.string().index(),
    nickname: t.string(),
    createdAt: t.string(),
    updatedAt: t.string(),
    lastSeenAt: t.string(),
    settings: settingsDoc,
    favorites: t.array(favoriteDoc),
    currentRide: t.option(rideDoc),
    undoRide: t.option(undoRideDoc),
    mutes: t.array(muteDoc),
    subscriptions: t.array(subscriptionDoc),
    recentActionState: t.option(recentActionStateDoc),
  }
);

const trainbot_activity = table(
  { name: named('activity') },
  {
    id: t.string().primaryKey(),
    scopeType: t.string().index(),
    subjectId: t.string().index(),
    subjectName: t.string(),
    serviceDate: t.string().index(),
    active: t.bool(),
    lastActivityAt: t.string().index(),
    summary: activitySummaryDoc,
    timeline: t.array(activityTimelineDoc),
    comments: t.array(activityCommentDoc),
    votes: t.array(activityVoteDoc),
  }
);

// Keep the empty compatibility table until live migration support can drop it safely.
const trainbot_ops_state = table(
  { name: named('ops_state') },
  {
    id: t.string().primaryKey(),
    kind: t.string().index(),
    scopeKey: t.string().index(),
    serviceDate: t.string().index(),
    updatedAt: t.string(),
    sourceVersion: t.string(),
    payloadJson: t.string(),
  }
);

const trainbot_runtime_state = table(
  { name: named('runtime_state'), public: true },
  {
    id: t.string().primaryKey(),
    requestedServiceDate: t.string(),
    effectiveServiceDate: t.string(),
    loadedServiceDate: t.string(),
    fallbackActive: t.bool(),
    cutoffHour: t.u32(),
    available: t.bool(),
    sameDayFresh: t.bool(),
    updatedAt: t.string(),
  }
);

const trainbot_runtime_config = table(
  { name: named('runtime_config') },
  {
    id: t.string().primaryKey(),
    scheduleCutoffHour: t.u32(),
    updatedAt: t.string(),
  }
);

const trainbot_maintenance_state = table(
  { name: named('maintenance_state') },
  {
    id: t.string().primaryKey(),
    updatedAt: t.string(),
    checkinsDeleted: t.u32(),
    subscriptionsDeleted: t.u32(),
    reportsDeleted: t.u32(),
    stationSightingsDeleted: t.u32(),
    trainStopsDeleted: t.u32(),
    trainsDeleted: t.u32(),
    feedEventsDeleted: t.u32(),
    feedImportsDeleted: t.u32(),
    importChunksDeleted: t.u32(),
  }
);

const trainbot_test_login_ticket = table(
  { name: named('test_login_ticket') },
  {
    nonceHash: t.string().primaryKey(),
    stableId: t.string(),
    expiresAt: t.string(),
    consumedAt: t.string(),
  }
);

const trainbot_active_bundle = table(
  { name: named('active_bundle') },
  {
    id: t.string().primaryKey(),
    version: t.string(),
    serviceDate: t.string(),
    generatedAt: t.string(),
    sourceVersion: t.string(),
    updatedAt: t.string(),
  }
);

const trainbot_station = table(
  { name: named('station'), public: true },
  {
    id: t.string().primaryKey(),
    serviceDate: t.string().index(),
    name: t.string(),
    normalizedKey: t.string(),
    latitude: t.option(t.number()),
    longitude: t.option(t.number()),
  }
);

const trainbot_trip_stop = table(
  { name: named('trip_stop'), public: true },
  {
    id: t.string().primaryKey(),
    trainId: t.string().index(),
    serviceDate: t.string().index(),
    stationId: t.string().index(),
    stationName: t.string(),
    seq: t.u32(),
    arrivalAt: t.option(t.string()),
    departureAt: t.option(t.string()),
    latitude: t.option(t.number()),
    longitude: t.option(t.number()),
  }
);

const trainbot_trip_live = table(
  { name: named('trip_live'), public: true },
  {
    trainId: t.string().primaryKey(),
    serviceDate: t.string().index(),
    state: t.string(),
    confidence: t.string(),
    uniqueReporters: t.u32(),
    riders: t.u32(),
    lastReportAt: t.string(),
    updatedAt: t.string(),
  }
);

const trainbot_trip_timeline_bucket = table(
  { name: named('trip_timeline_bucket'), public: true },
  {
    id: t.string().primaryKey(),
    trainId: t.string().index(),
    serviceDate: t.string().index(),
    at: t.string(),
    signal: t.string(),
    count: t.u32(),
  }
);

const trainbot_public_sighting = table(
  { name: named('public_sighting'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    serviceDate: t.string().index(),
    stationId: t.string().index(),
    stationName: t.string(),
    destinationStationId: t.string(),
    destinationStationName: t.string(),
    matchedTrainInstanceId: t.string().index(),
    createdAt: t.string(),
    isRecent: t.bool(),
  }
);

const trainbot_public_incident = table(
  { name: named('incident_summary'), public: true },
  {
    id: t.string().primaryKey(),
    scopeType: t.string().index(),
    subjectId: t.string().index(),
    subjectName: t.string(),
    serviceDate: t.string().index(),
    active: t.bool(),
    lastReportName: t.string(),
    lastReportAt: t.string(),
    lastActivityName: t.string(),
    lastActivityAt: t.string(),
    lastActivityActor: t.string(),
    lastReporter: t.string(),
    commentCount: t.u32(),
    ongoingVotes: t.u32(),
    clearedVotes: t.u32(),
  }
);

const trainbot_public_incident_event = table(
  { name: named('incident_event'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    serviceDate: t.string().index(),
    kind: t.string(),
    name: t.string(),
    detail: t.string(),
    nickname: t.string(),
    createdAt: t.string(),
    signal: t.string(),
  }
);

const trainbot_public_incident_comment = table(
  { name: named('incident_comment'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    serviceDate: t.string().index(),
    nickname: t.string(),
    body: t.string(),
    createdAt: t.string(),
  }
);

const trainbot_import_chunk = table(
  { name: named('import_chunk') },
  {
    id: t.string().primaryKey(),
    importId: t.string().index(),
    chunkKind: t.string().index(),
    serviceDate: t.string().index(),
    sourceVersion: t.string(),
    createdAt: t.string(),
    payloadJson: t.string(),
  }
);

const trainbot_feed_import = table(
  { name: named('feed_import') },
  {
    importId: t.string().primaryKey(),
    serviceDate: t.string().index(),
    sourceVersion: t.string(),
    createdAt: t.string(),
    updatedAt: t.string(),
  }
);

const trainbot_feed_event = table(
  { name: named('feed_event') },
  {
    id: t.string().primaryKey(),
    importId: t.string().index(),
    serviceDate: t.string().index(),
    kind: t.string().index(),
    entityId: t.string().index(),
    sourceVersion: t.string(),
    createdAt: t.string().index(),
    payloadJson: t.string(),
  }
);

const trainbot_job: any = table(
  { name: named('job'), scheduled: () => runTrainbotJob },
  {
    scheduled_id: t.u64().primaryKey().autoInc(),
    scheduled_at: t.scheduleAt(),
    jobId: t.string().index(),
    kind: t.string().index(),
    subjectId: t.string().index(),
    serviceDate: t.string().index(),
    createdAt: t.string(),
    payloadJson: t.string(),
  }
);

const trainbot_service_station = table(
  { name: named('service_station'), public: true },
  {
    id: t.string().primaryKey(),
    stationId: t.string().index(),
    serviceDate: t.string().index(),
    name: t.string(),
    normalizedKey: t.string(),
    latitude: t.option(t.number()),
    longitude: t.option(t.number()),
  }
);

const trainbot_trip_public = table(
  { name: named('trip_public'), public: true },
  {
    id: t.string().primaryKey(),
    serviceDate: t.string().index(),
    fromStationId: t.string(),
    fromStationName: t.string(),
    toStationId: t.string(),
    toStationName: t.string(),
    departureAt: t.string(),
    arrivalAt: t.string(),
    sourceVersion: t.string(),
    state: t.string(),
    confidence: t.string(),
    uniqueReporters: t.u32(),
    riders: t.u32(),
    lastReportAt: t.string(),
    updatedAt: t.string(),
    recentTimeline: t.array(timelineBucketDoc),
  }
);

const trainbot_rider_identity = table(
  { name: named('rider_identity') },
  {
    stableId: t.string().primaryKey(),
    senderIdentity: t.string().index(),
    telegramUserId: t.string().index(),
    nickname: t.string(),
    createdAt: t.string(),
    updatedAt: t.string(),
    lastSeenAt: t.string(),
  }
);

const trainbot_rider_settings = table(
  { name: named('rider_settings') },
  {
    stableId: t.string().primaryKey(),
    alertsEnabled: t.bool(),
    alertStyle: t.string(),
    language: t.string(),
    updatedAt: t.string(),
  }
);

const trainbot_favorite_route = table(
  { name: named('favorite_route') },
  {
    id: t.string().primaryKey(),
    stableId: t.string().index(),
    fromStationId: t.string(),
    fromStationName: t.string(),
    toStationId: t.string(),
    toStationName: t.string(),
    createdAt: t.string(),
  }
);

const trainbot_active_checkin = table(
  { name: named('active_checkin') },
  {
    stableId: t.string().primaryKey(),
    trainInstanceId: t.string().index(),
    boardingStationId: t.string(),
    checkedInAt: t.string(),
    autoCheckoutAt: t.string(),
  }
);

const trainbot_undo_checkout = table(
  { name: named('undo_checkout') },
  {
    stableId: t.string().primaryKey(),
    trainInstanceId: t.string().index(),
    boardingStationId: t.string(),
    checkedInAt: t.string(),
    autoCheckoutAt: t.string(),
    expiresAt: t.string(),
  }
);

const trainbot_train_mute = table(
  { name: named('train_mute') },
  {
    id: t.string().primaryKey(),
    stableId: t.string().index(),
    trainInstanceId: t.string().index(),
    mutedUntil: t.string(),
    createdAt: t.string(),
  }
);

const trainbot_train_subscription = table(
  { name: named('train_subscription') },
  {
    id: t.string().primaryKey(),
    stableId: t.string().index(),
    trainInstanceId: t.string().index(),
    expiresAt: t.string(),
    isActive: t.bool(),
    createdAt: t.string(),
    updatedAt: t.string(),
  }
);

const trainbot_recent_action_state = table(
  { name: named('recent_action_state') },
  {
    stableId: t.string().primaryKey(),
    updatedAt: t.string(),
  }
);

const trainbot_incident_vote = table(
  { name: named('incident_vote') },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    stableId: t.string().index(),
    nickname: t.string(),
    value: t.string(),
    createdAt: t.string(),
    updatedAt: t.string(),
  }
);

const spacetimedb: any = schema(
  {
    trainbot_service_day,
    trainbot_trip,
    trainbot_rider,
    trainbot_activity,
    trainbot_ops_state,
    trainbot_runtime_state,
    trainbot_runtime_config,
    trainbot_maintenance_state,
    trainbot_test_login_ticket,
    trainbot_active_bundle,
    trainbot_station,
    trainbot_trip_stop,
    trainbot_trip_live,
    trainbot_trip_timeline_bucket,
    trainbot_public_sighting,
    trainbot_public_incident,
    trainbot_public_incident_event,
    trainbot_public_incident_comment,
    trainbot_import_chunk,
    trainbot_feed_import,
    trainbot_feed_event,
    trainbot_job,
    trainbot_service_station,
    trainbot_trip_public,
    trainbot_rider_identity,
    trainbot_rider_settings,
    trainbot_favorite_route,
    trainbot_active_checkin,
    trainbot_undo_checkout,
    trainbot_train_mute,
    trainbot_train_subscription,
    trainbot_recent_action_state,
    trainbot_incident_vote,
  },
  { CASE_CONVERSION_POLICY: CaseConversionPolicy.None }
);

export default spacetimedb;

type ParsedObject = Record<string, unknown>;

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function rowsFrom(iterable: any): any[] {
  return Array.from(iterable as Iterable<any>) as any[];
}

function firstRow(iterable: any): any | null {
  const rows = rowsFrom(iterable);
  return rows.length ? rows[0] : null;
}

function parseJSON(raw: string, errorMessage: string): any {
  try {
    return JSON.parse(raw);
  } catch {
    throw new SenderError(errorMessage);
  }
}

function serialize(payload: unknown): string {
  return JSON.stringify(payload);
}

function ctxTimestampDate(ctx: any): Date | null {
  const timestamp = ctx?.timestamp;
  if (timestamp && typeof timestamp.toDate === 'function') {
    const date = timestamp.toDate();
    if (date instanceof Date && !Number.isNaN(date.getTime())) {
      return date;
    }
  }
  if (timestamp instanceof Date && !Number.isNaN(timestamp.getTime())) {
    return timestamp;
  }
  if (timestamp != null && typeof timestamp !== 'bigint') {
    const date = new Date(timestamp);
    if (!Number.isNaN(date.getTime())) {
      return date;
    }
  }
  return null;
}

function nowDate(ctx: any): Date {
  return ctxTimestampDate(ctx) || new Date(Date.now());
}

function nowISO(ctx: any): string {
  return nowDate(ctx).toISOString();
}

function parseISO(value: string | undefined | null): Date | null {
  if (!value) {
    return null;
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function isoPlus(base: string, deltaMs: number): string {
  const parsed = parseISO(base);
  if (!parsed) {
    return new Date(Date.now() + deltaMs).toISOString();
  }
  return new Date(parsed.getTime() + deltaMs).toISOString();
}

function nsFromMs(valueMs: number): number {
  return Math.max(0, Math.round(valueMs * 1000000));
}

function trimOptional(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

function compareTimeAscending(left: string | undefined, right: string | undefined): number {
  return (parseISO(left || '')?.getTime() || 0) - (parseISO(right || '')?.getTime() || 0);
}

function compareTimeDescending(left: string | undefined, right: string | undefined): number {
  return compareTimeAscending(right, left);
}

function normalizeLanguage(value: string): string {
  const normalized = value.trim().toUpperCase();
  return normalized === 'LV' ? 'LV' : 'EN';
}

function normalizeAlertStyle(value: string): string {
  const normalized = value.trim().toUpperCase();
  if (normalized === 'DISCREET' || normalized === 'DETAILED') {
    return normalized;
  }
  throw new SenderError('invalid alert style');
}

function normalizeStationQueryValue(value: string): string {
  let normalized = value.trim().toLowerCase();
  if (!normalized) {
    return '';
  }
  const folds: Array<[string, string]> = [
    ['ā', 'a'],
    ['č', 'c'],
    ['ē', 'e'],
    ['ģ', 'g'],
    ['ī', 'i'],
    ['ķ', 'k'],
    ['ļ', 'l'],
    ['ņ', 'n'],
    ['š', 's'],
    ['ū', 'u'],
    ['ž', 'z'],
  ];
  for (const [from, to] of folds) {
    normalized = normalized.replaceAll(from, to);
  }
  normalized = normalized.replaceAll('-', ' ');
  return normalized.split(/\s+/).filter(Boolean).join(' ');
}

function normalizeStationKey(value: string): string {
  return normalizeStationQueryValue(value);
}

function normalizeStationId(value: string): string {
  return normalizeStationQueryValue(value).replaceAll(' ', '-') || value.trim();
}

function rigaDateParts(date: Date): { year: string; month: string; day: string; hour: number } {
  const parts = rigaScheduleFormatter.formatToParts(date);
  const byType = new Map(parts.map((part) => [part.type, part.value]));
  const hour = Number(byType.get('hour') || '0');
  return {
    year: byType.get('year') || '1970',
    month: byType.get('month') || '01',
    day: byType.get('day') || '01',
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

function utcDayStart(date: Date): Date {
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate(), 0, 0, 0, 0));
}

function utcDayEnd(date: Date): Date {
  return new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate(), 23, 59, 59, 999));
}

function genericNickname(stableId: string): string {
  const adjectives = [
    'Amber', 'Cedar', 'Silver', 'North', 'Swift', 'Mellow', 'Harbor', 'Forest',
    'Granite', 'Quiet', 'Bright', 'Saffron', 'Willow', 'Copper', 'River', 'Cloud',
  ];
  const nouns = [
    'Scout', 'Rider', 'Signal', 'Beacon', 'Traveler', 'Watcher', 'Harbor', 'Comet',
    'Falcon', 'Lantern', 'Pioneer', 'Courier', 'Voyager', 'Pilot', 'Atlas', 'Drifter',
  ];
  let hash = 2166136261 >>> 0;
  const input = `train:${stableId}`;
  for (let index = 0; index < input.length; index += 1) {
    hash ^= input.charCodeAt(index);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  const adjective = adjectives[hash % adjectives.length];
  const noun = nouns[(hash >>> 8) % nouns.length];
  const suffix = String((hash % 900) + 100).padStart(3, '0');
  return `${adjective} ${noun} ${suffix}`;
}

function requireUserSession(tx: any) {
  const auth = tx.senderAuth;
  if (!auth || !auth.hasJWT || !auth.jwt) {
    throw new SenderError('telegram auth required');
  }
  const jwt = auth.jwt;
  const stableId = asString(jwt.subject).trim();
  if (!stableId) {
    throw new SenderError('telegram auth required');
  }
  const payload = (jwt.fullPayload || {}) as ParsedObject;
  const roles = Array.isArray(payload.roles)
    ? payload.roles.filter((item): item is string => typeof item === 'string')
    : [];
  return {
    stableId,
    roles,
    language: normalizeLanguage(asString(payload.language)),
  };
}

function requireServiceRole(tx: any): void {
  const session = requireUserSession(tx);
  if (!session.roles.includes('train_service')) {
    throw new SenderError('service role required');
  }
}

function optionalViewerStableId(tx: any): string {
  const auth = tx.senderAuth;
  if (!auth || !auth.hasJWT || !auth.jwt) {
    return '';
  }
  return asString(auth.jwt.subject).trim();
}

function telegramUserIdForStableId(stableId: string): string {
  return stableId.startsWith('telegram:') ? stableId.slice('telegram:'.length) : stableId;
}

function sanitizeStationDoc(item: any): any {
  const id = asString(item?.id).trim() || normalizeStationId(asString(item?.name));
  return {
    id,
    name: asString(item?.name).trim(),
    normalizedKey: trimOptional(asString(item?.normalizedKey)) || normalizeStationKey(asString(item?.name)),
    latitude: typeof item?.latitude === 'number' ? item.latitude : undefined,
    longitude: typeof item?.longitude === 'number' ? item.longitude : undefined,
  };
}

function sanitizeStopDoc(item: any): any {
  const seq = Number(item?.seq);
  return {
    stationId: asString(item?.stationId).trim() || normalizeStationId(asString(item?.stationName)),
    stationName: asString(item?.stationName).trim(),
    seq: Number.isFinite(seq) && seq >= 0 ? Math.floor(seq) : 0,
    arrivalAt: trimOptional(asString(item?.arrivalAt)),
    departureAt: trimOptional(asString(item?.departureAt)),
    latitude: typeof item?.latitude === 'number' ? item.latitude : undefined,
    longitude: typeof item?.longitude === 'number' ? item.longitude : undefined,
  };
}

function sanitizeSettingsDoc(item: any, languageFallback: string, updatedAtFallback: string): any {
  return {
    alertsEnabled: item?.alertsEnabled !== false,
    alertStyle: normalizeAlertStyle(asString(item?.alertStyle || 'DETAILED')),
    language: normalizeLanguage(asString(item?.language) || languageFallback),
    updatedAt: trimOptional(asString(item?.updatedAt)) || updatedAtFallback,
  };
}

function sanitizeFavoriteDoc(item: any): any | null {
  const fromStationId = asString(item?.fromStationId).trim();
  const toStationId = asString(item?.toStationId).trim();
  if (!fromStationId || !toStationId) {
    return null;
  }
  return {
    fromStationId,
    fromStationName: asString(item?.fromStationName).trim(),
    toStationId,
    toStationName: asString(item?.toStationName).trim(),
    createdAt: trimOptional(asString(item?.createdAt)) || new Date(0).toISOString(),
  };
}

function sanitizeRideDoc(item: any): any | undefined {
  const trainInstanceId = asString(item?.trainInstanceId).trim();
  if (!trainInstanceId) {
    return undefined;
  }
  return {
    trainInstanceId,
    boardingStationId: asString(item?.boardingStationId).trim(),
    checkedInAt: asString(item?.checkedInAt).trim(),
    autoCheckoutAt: asString(item?.autoCheckoutAt).trim(),
  };
}

function sanitizeUndoRideDoc(item: any): any | undefined {
  const ride = sanitizeRideDoc(item);
  if (!ride) {
    return undefined;
  }
  const expiresAt = asString(item?.expiresAt).trim();
  if (!expiresAt) {
    return undefined;
  }
  return { ...ride, expiresAt };
}

function sanitizeMuteDoc(item: any): any | null {
  const trainInstanceId = asString(item?.trainInstanceId).trim();
  const mutedUntil = asString(item?.mutedUntil).trim();
  if (!trainInstanceId || !mutedUntil) {
    return null;
  }
  return {
    trainInstanceId,
    mutedUntil,
    createdAt: trimOptional(asString(item?.createdAt)) || mutedUntil,
  };
}

function sanitizeSubscriptionDoc(item: any): any | null {
  const trainInstanceId = asString(item?.trainInstanceId).trim();
  const expiresAt = asString(item?.expiresAt).trim();
  if (!trainInstanceId || !expiresAt) {
    return null;
  }
  return {
    trainInstanceId,
    expiresAt,
    isActive: item?.isActive !== false,
    createdAt: trimOptional(asString(item?.createdAt)) || expiresAt,
    updatedAt: trimOptional(asString(item?.updatedAt)) || expiresAt,
  };
}

function sanitizeTimelineEvent(item: any): any | null {
  const id = asString(item?.id).trim();
  const createdAt = asString(item?.createdAt).trim();
  if (!id || !createdAt) {
    return null;
  }
  return {
    id,
    kind: asString(item?.kind).trim(),
    stableId: asString(item?.stableId).trim(),
    nickname: asString(item?.nickname).trim(),
    name: asString(item?.name).trim(),
    detail: asString(item?.detail).trim(),
    createdAt,
    signal: asString(item?.signal).trim().toUpperCase(),
    trainInstanceId: asString(item?.trainInstanceId).trim(),
    stationId: asString(item?.stationId).trim(),
    stationName: asString(item?.stationName).trim(),
    destinationStationId: asString(item?.destinationStationId).trim(),
    destinationStationName: asString(item?.destinationStationName).trim(),
    matchedTrainInstanceId: asString(item?.matchedTrainInstanceId).trim(),
  };
}

function sanitizeCommentDoc(item: any): any | null {
  const id = asString(item?.id).trim();
  const createdAt = asString(item?.createdAt).trim();
  const body = asString(item?.body).trim();
  if (!id || !createdAt || !body) {
    return null;
  }
  return {
    id,
    stableId: asString(item?.stableId).trim(),
    nickname: asString(item?.nickname).trim(),
    body,
    createdAt,
  };
}

function sanitizeVoteDoc(item: any): any | null {
  const stableId = asString(item?.stableId).trim();
  if (!stableId) {
    return null;
  }
  const value = asString(item?.value).trim().toUpperCase();
  if (value !== 'ONGOING' && value !== 'CLEARED') {
    return null;
  }
  const createdAt = trimOptional(asString(item?.createdAt)) || trimOptional(asString(item?.updatedAt)) || new Date(0).toISOString();
  const updatedAt = trimOptional(asString(item?.updatedAt)) || createdAt;
  return {
    stableId,
    nickname: asString(item?.nickname).trim(),
    value,
    createdAt,
    updatedAt,
  };
}

function sanitizeRiderRow(tx: any, item: any): any {
  const currentAt = nowISO(tx);
  const stableId = asString(item?.stableId).trim();
  if (!stableId) {
    throw new SenderError('stableId is required');
  }
  const favorites = Array.isArray(item?.favorites)
    ? item.favorites.map(sanitizeFavoriteDoc).filter(Boolean)
    : [];
  favorites.sort((left: any, right: any) => compareTimeDescending(left.createdAt, right.createdAt));

  const mutes = Array.isArray(item?.mutes)
    ? item.mutes.map(sanitizeMuteDoc).filter(Boolean)
    : [];
  mutes.sort((left: any, right: any) => compareTimeDescending(left.mutedUntil, right.mutedUntil));

  const subscriptions = Array.isArray(item?.subscriptions)
    ? item.subscriptions.map(sanitizeSubscriptionDoc).filter(Boolean)
    : [];
  subscriptions.sort((left: any, right: any) => compareTimeDescending(left.updatedAt, right.updatedAt));

  const createdAt = trimOptional(asString(item?.createdAt)) || currentAt;
  const updatedAt = trimOptional(asString(item?.updatedAt)) || currentAt;
  return {
    stableId,
    telegramUserId: trimOptional(asString(item?.telegramUserId)) || telegramUserIdForStableId(stableId),
    nickname: trimOptional(asString(item?.nickname)) || genericNickname(stableId),
    createdAt,
    updatedAt,
    lastSeenAt: trimOptional(asString(item?.lastSeenAt)) || updatedAt,
    settings: sanitizeSettingsDoc(item?.settings, 'EN', updatedAt),
    favorites,
    currentRide: sanitizeRideDoc(item?.currentRide),
    undoRide: sanitizeUndoRideDoc(item?.undoRide),
    mutes,
    subscriptions,
    recentActionState: item?.recentActionState && typeof item.recentActionState === 'object'
      ? { updatedAt: trimOptional(asString(item.recentActionState.updatedAt)) || updatedAt }
      : undefined,
  };
}

function latestReportEvent(activity: any): any | null {
  for (const item of activity.timeline || []) {
    if (item.kind === 'report' || item.kind === 'station_sighting') {
      return item;
    }
  }
  return null;
}

function latestOngoingVote(activity: any): any | null {
  for (const item of activity.votes || []) {
    if (asString(item.value).trim().toUpperCase() === 'ONGOING') {
      return item;
    }
  }
  return null;
}

function incidentCommentActivityLabel(): string {
  return 'Comment';
}

function incidentVoteEventLabel(value: string): string {
  switch (value.trim().toUpperCase()) {
    case 'ONGOING':
      return 'Still there';
    case 'CLEARED':
      return 'Cleared';
    default:
      return 'Vote';
  }
}

function refreshActivityRow(tx: any, row: any): any {
  const timeline = Array.isArray(row.timeline)
    ? row.timeline.map(sanitizeTimelineEvent).filter(Boolean)
    : [];
  timeline.sort((left: any, right: any) => compareTimeDescending(left.createdAt, right.createdAt));

  const comments = Array.isArray(row.comments)
    ? row.comments.map(sanitizeCommentDoc).filter(Boolean)
    : [];
  comments.sort((left: any, right: any) => compareTimeDescending(left.createdAt, right.createdAt));

  const votesByStableId = new Map<string, any>();
  for (const vote of Array.isArray(row.votes) ? row.votes.map(sanitizeVoteDoc).filter(Boolean) : []) {
    const existing = votesByStableId.get(vote.stableId);
    if (!existing || compareTimeDescending(vote.updatedAt, existing.updatedAt) < 0) {
      votesByStableId.set(vote.stableId, vote);
    }
  }
  const votes = Array.from(votesByStableId.values());
  votes.sort((left: any, right: any) => compareTimeDescending(left.updatedAt, right.updatedAt));

  const report = latestReportEvent({ timeline });
  let lastActivityName = report ? report.name : '';
  let lastActivityAt = report ? report.createdAt : '';
  let lastActivityActor = report ? report.nickname : '';

  const comment = comments[0];
  if (comment && compareTimeDescending(comment.createdAt, lastActivityAt) < 0) {
    lastActivityName = incidentCommentActivityLabel();
    lastActivityAt = comment.createdAt;
    lastActivityActor = comment.nickname;
  }

  const ongoingVote = latestOngoingVote({ votes });
  if (ongoingVote && compareTimeDescending(ongoingVote.updatedAt, lastActivityAt) < 0) {
    lastActivityName = incidentVoteEventLabel(ongoingVote.value);
    lastActivityAt = ongoingVote.updatedAt;
    lastActivityActor = ongoingVote.nickname;
  }

  const now = nowDate(tx).getTime();
  const reportAtMs = parseISO(report?.createdAt || '')?.getTime() || 0;
  const activeThreshold = row.scopeType === 'station' ? STATION_ACTIVITY_ACTIVE_MS : TRAIN_ACTIVITY_ACTIVE_MS;
  const active = Boolean(report && now-reportAtMs <= activeThreshold);

  return {
    id: asString(row.id).trim(),
    scopeType: asString(row.scopeType).trim(),
    subjectId: asString(row.subjectId).trim(),
    subjectName: asString(row.subjectName).trim(),
    serviceDate: asString(row.serviceDate).trim(),
    active,
    lastActivityAt,
    summary: {
      lastReportName: report ? report.name : '',
      lastReportAt: report ? report.createdAt : '',
      lastActivityName,
      lastActivityAt,
      lastActivityActor,
      lastReporter: report ? report.nickname : '',
    },
    timeline: timeline.slice(0, 250),
    comments: comments.slice(0, 100),
    votes,
  };
}

function sanitizeActivityRow(tx: any, item: any): any {
  const row = {
    id: asString(item?.id).trim(),
    scopeType: asString(item?.scopeType).trim(),
    subjectId: asString(item?.subjectId).trim(),
    subjectName: asString(item?.subjectName).trim(),
    serviceDate: asString(item?.serviceDate).trim(),
    active: item?.active === true,
    lastActivityAt: asString(item?.lastActivityAt).trim(),
    summary: item?.summary || {},
    timeline: Array.isArray(item?.timeline) ? item.timeline : [],
    comments: Array.isArray(item?.comments) ? item.comments : [],
    votes: Array.isArray(item?.votes) ? item.votes : [],
  };
  if (!row.id || !row.scopeType || !row.subjectId || !row.serviceDate) {
    throw new SenderError('activity row is missing required fields');
  }
  return refreshActivityRow(tx, row);
}

function clearRowsByStableId(tableView: any, stableId: string): void {
  for (const row of rowsFrom(tableView.stableId.filter(stableId))) {
    tableView.delete(row);
  }
}

function syncRiderProjection(tx: any, rider: any): void {
  const stableId = asString(rider?.stableId).trim();
  if (!stableId) {
    return;
  }
  const existingIdentity = tx.db.trainbot_rider_identity.stableId.find(stableId);
  const senderIdentity = trimOptional(asString(rider?.senderIdentity))
    || trimOptional(asString(existingIdentity?.senderIdentity))
    || '';

  tx.db.trainbot_rider_identity.stableId.delete(stableId);
  tx.db.trainbot_rider_settings.stableId.delete(stableId);
  tx.db.trainbot_active_checkin.stableId.delete(stableId);
  tx.db.trainbot_undo_checkout.stableId.delete(stableId);
  tx.db.trainbot_recent_action_state.stableId.delete(stableId);
  clearRowsByStableId(tx.db.trainbot_favorite_route, stableId);
  clearRowsByStableId(tx.db.trainbot_train_mute, stableId);
  clearRowsByStableId(tx.db.trainbot_train_subscription, stableId);

  tx.db.trainbot_rider_identity.insert({
    stableId,
    senderIdentity,
    telegramUserId: asString(rider.telegramUserId).trim(),
    nickname: asString(rider.nickname).trim(),
    createdAt: asString(rider.createdAt).trim(),
    updatedAt: asString(rider.updatedAt).trim(),
    lastSeenAt: asString(rider.lastSeenAt).trim(),
  });

  tx.db.trainbot_rider_settings.insert({
    stableId,
    alertsEnabled: rider.settings?.alertsEnabled !== false,
    alertStyle: asString(rider.settings?.alertStyle).trim(),
    language: asString(rider.settings?.language).trim(),
    updatedAt: asString(rider.settings?.updatedAt).trim() || asString(rider.updatedAt).trim(),
  });

  for (const favorite of rider.favorites || []) {
    const fromStationId = asString(favorite.fromStationId).trim();
    const toStationId = asString(favorite.toStationId).trim();
    if (!fromStationId || !toStationId) {
      continue;
    }
    tx.db.trainbot_favorite_route.insert({
      id: `${stableId}|${fromStationId}|${toStationId}`,
      stableId,
      fromStationId,
      fromStationName: asString(favorite.fromStationName).trim(),
      toStationId,
      toStationName: asString(favorite.toStationName).trim(),
      createdAt: asString(favorite.createdAt).trim(),
    });
  }

  if (rider.currentRide) {
    tx.db.trainbot_active_checkin.insert({
      stableId,
      trainInstanceId: asString(rider.currentRide.trainInstanceId).trim(),
      boardingStationId: asString(rider.currentRide.boardingStationId).trim(),
      checkedInAt: asString(rider.currentRide.checkedInAt).trim(),
      autoCheckoutAt: asString(rider.currentRide.autoCheckoutAt).trim(),
    });
  }

  if (rider.undoRide) {
    tx.db.trainbot_undo_checkout.insert({
      stableId,
      trainInstanceId: asString(rider.undoRide.trainInstanceId).trim(),
      boardingStationId: asString(rider.undoRide.boardingStationId).trim(),
      checkedInAt: asString(rider.undoRide.checkedInAt).trim(),
      autoCheckoutAt: asString(rider.undoRide.autoCheckoutAt).trim(),
      expiresAt: asString(rider.undoRide.expiresAt).trim(),
    });
  }

  for (const mute of rider.mutes || []) {
    const trainInstanceId = asString(mute.trainInstanceId).trim();
    if (!trainInstanceId) {
      continue;
    }
    tx.db.trainbot_train_mute.insert({
      id: `${stableId}|${trainInstanceId}`,
      stableId,
      trainInstanceId,
      mutedUntil: asString(mute.mutedUntil).trim(),
      createdAt: asString(mute.createdAt).trim(),
    });
  }

  for (const subscription of rider.subscriptions || []) {
    const trainInstanceId = asString(subscription.trainInstanceId).trim();
    if (!trainInstanceId) {
      continue;
    }
    tx.db.trainbot_train_subscription.insert({
      id: `${stableId}|${trainInstanceId}`,
      stableId,
      trainInstanceId,
      expiresAt: asString(subscription.expiresAt).trim(),
      isActive: subscription.isActive !== false,
      createdAt: asString(subscription.createdAt).trim(),
      updatedAt: asString(subscription.updatedAt).trim(),
    });
  }

  if (rider.recentActionState) {
    tx.db.trainbot_recent_action_state.insert({
      stableId,
      updatedAt: asString(rider.recentActionState.updatedAt).trim(),
    });
  }
}

function syncIncidentVoteProjection(tx: any, activity: any): void {
  const incidentId = asString(activity?.id).trim();
  if (!incidentId) {
    return;
  }
  for (const row of rowsFrom(tx.db.trainbot_incident_vote.incidentId.filter(incidentId))) {
    tx.db.trainbot_incident_vote.id.delete(row.id);
  }
  for (const vote of activity?.votes || []) {
    const stableId = asString(vote.stableId).trim();
    if (!stableId) {
      continue;
    }
    tx.db.trainbot_incident_vote.insert({
      id: `${incidentId}|${stableId}`,
      incidentId,
      stableId,
      nickname: asString(vote.nickname).trim(),
      value: asString(vote.value).trim(),
      createdAt: asString(vote.createdAt).trim(),
      updatedAt: asString(vote.updatedAt).trim(),
    });
  }
}

function putRiderRow(tx: any, item: any): any {
  const rider = sanitizeRiderRow(tx, item);
  const senderIdentity = trimOptional(asString(item?.senderIdentity))
    || trimOptional(asString(tx.db.trainbot_rider_identity.stableId.find(rider.stableId)?.senderIdentity))
    || '';
  tx.db.trainbot_rider.stableId.delete(rider.stableId);
  const inserted = tx.db.trainbot_rider.insert(rider);
  syncRiderProjection(tx, { ...inserted, senderIdentity });
  return inserted;
}

function putActivityRow(tx: any, item: any): any {
  const activity = sanitizeActivityRow(tx, item);
  tx.db.trainbot_activity.id.delete(activity.id);
  const inserted = tx.db.trainbot_activity.insert(activity);
  syncIncidentVoteProjection(tx, inserted);
  return inserted;
}

function defaultSettings(language: string, updatedAt: string): any {
  return sanitizeSettingsDoc({}, language, updatedAt);
}

function resetTestRider(tx: any, stableId: string): any {
  const cleanStableId = asString(stableId).trim();
  if (!cleanStableId) {
    throw new SenderError('stableId is required');
  }
  const currentAt = nowISO(tx);
  const existing = tx.db.trainbot_rider.stableId.find(cleanStableId);
  const rider = putRiderRow(tx, {
    stableId: cleanStableId,
    telegramUserId: telegramUserIdForStableId(cleanStableId),
    nickname: trimOptional(asString(existing?.nickname)) || genericNickname(cleanStableId),
    createdAt: trimOptional(asString(existing?.createdAt)) || currentAt,
    updatedAt: currentAt,
    lastSeenAt: currentAt,
    settings: defaultSettings('EN', currentAt),
    favorites: [],
    currentRide: undefined,
    undoRide: undefined,
    mutes: [],
    subscriptions: [],
    recentActionState: { updatedAt: currentAt },
  });
  scheduleRiderExpiryJobs(tx, rider);

  for (const activity of rowsFrom(tx.db.trainbot_activity.iter())) {
    const timeline = Array.isArray(activity?.timeline) ? activity.timeline : [];
    const comments = Array.isArray(activity?.comments) ? activity.comments : [];
    const votes = Array.isArray(activity?.votes) ? activity.votes : [];

    const nextTimeline = timeline.filter((item: any) => asString(item?.stableId).trim() !== cleanStableId);
    const nextComments = comments.filter((item: any) => asString(item?.stableId).trim() !== cleanStableId);
    const nextVotes = votes.filter((item: any) => asString(item?.stableId).trim() !== cleanStableId);

    if (nextTimeline.length === timeline.length && nextComments.length === comments.length && nextVotes.length === votes.length) {
      continue;
    }
    if (!nextTimeline.length && !nextComments.length && !nextVotes.length) {
      deleteActivity(tx, asString(activity.id).trim());
      continue;
    }
    const nextActivity = putActivityRow(tx, {
      ...activity,
      timeline: nextTimeline,
      comments: nextComments,
      votes: nextVotes,
    });
    refreshActivityProjection(tx, nextActivity.id);
    scheduleActivityRefreshJobs(tx, nextActivity);
  }

  cleanupOrphanIncidentVotes(tx);
  writeRuntimeState(tx);
  ensureRuntimeRefreshJob(tx);
  return rider;
}

function collectRiderTrainIds(rider: any): string[] {
  const ids = new Set<string>();
  if (rider?.currentRide && asString(rider.currentRide.trainInstanceId).trim()) {
    ids.add(asString(rider.currentRide.trainInstanceId).trim());
  }
  for (const subscription of rider?.subscriptions || []) {
    const trainId = asString(subscription?.trainInstanceId).trim();
    if (trainId) {
      ids.add(trainId);
    }
  }
  return Array.from(ids.values());
}

function scheduleRiderExpiryJobs(tx: any, rider: any): void {
  const stableId = asString(rider?.stableId).trim();
  if (!stableId) {
    return;
  }
  const prefix = `rider:${stableId}|`;
  deleteJobsWithPrefix(tx, prefix);

  const currentRide = rider.currentRide;
  if (currentRide && asString(currentRide.trainInstanceId).trim()) {
    const trainId = asString(currentRide.trainInstanceId).trim();
    const train = trainById(tx, trainId);
    upsertJob(
      tx,
      `${prefix}checkin`,
      asString(currentRide.autoCheckoutAt).trim() || nowISO(tx),
      'expire_checkin',
      stableId,
      asString(train?.serviceDate).trim(),
      { stableId, trainId }
    );
  }

  if (rider.undoRide && asString(rider.undoRide.expiresAt).trim()) {
    upsertJob(
      tx,
      `${prefix}undo`,
      asString(rider.undoRide.expiresAt).trim(),
      'expire_undo',
      stableId,
      '',
      { stableId }
    );
  }

  for (const subscription of Array.isArray(rider.subscriptions) ? rider.subscriptions : []) {
    if (!subscription || subscription.isActive === false) {
      continue;
    }
    const trainInstanceId = asString(subscription.trainInstanceId).trim();
    const expiresAt = asString(subscription.expiresAt).trim();
    if (!trainInstanceId || !expiresAt) {
      continue;
    }
    const train = trainById(tx, trainInstanceId);
    upsertJob(
      tx,
      `${prefix}subscription|${trainInstanceId}`,
      expiresAt,
      'expire_subscription',
      stableId,
      asString(train?.serviceDate).trim(),
      { stableId, trainId: trainInstanceId }
    );
  }

  for (const mute of Array.isArray(rider.mutes) ? rider.mutes : []) {
    const trainId = asString(mute?.trainInstanceId).trim();
    const mutedUntil = asString(mute?.mutedUntil).trim();
    if (!trainId || !mutedUntil) {
      continue;
    }
    const train = trainById(tx, trainId);
    upsertJob(
      tx,
      `${prefix}mute|${trainId}`,
      mutedUntil,
      'expire_mute',
      stableId,
      asString(train?.serviceDate).trim(),
      { stableId, trainId }
    );
  }
}

function senderIdentityHex(tx: any): string {
  return tx?.sender && typeof tx.sender.toHexString === 'function'
    ? String(tx.sender.toHexString())
    : '';
}

function loadViewer(tx: any) {
  const session = requireUserSession(tx);
  const currentAt = nowISO(tx);
  const existing = tx.db.trainbot_rider.stableId.find(session.stableId);
  const identity = tx.db.trainbot_rider_identity.stableId.find(session.stableId);
  const rider = existing
    ? {
      ...existing,
      senderIdentity: trimOptional(asString(identity?.senderIdentity)) || senderIdentityHex(tx),
      nickname: trimOptional(asString(existing.nickname)) || genericNickname(session.stableId),
      settings: sanitizeSettingsDoc(existing.settings, session.language, trimOptional(asString(existing.updatedAt)) || currentAt),
    }
    : null;
  return { session, rider };
}

function ensureRider(tx: any) {
  const { session, rider } = loadViewer(tx);
  if (rider) {
    const identity = senderIdentityHex(tx);
    if (identity && identity !== asString(rider.senderIdentity).trim()) {
      const next = putRiderRow(tx, { ...rider, senderIdentity: identity, updatedAt: nowISO(tx), lastSeenAt: nowISO(tx) });
      scheduleRiderExpiryJobs(tx, next);
      return { session, rider: next };
    }
    return { session, rider };
  }
  const currentAt = nowISO(tx);
  const next = putRiderRow(tx, {
    stableId: session.stableId,
    senderIdentity: senderIdentityHex(tx),
    telegramUserId: telegramUserIdForStableId(session.stableId),
    nickname: genericNickname(session.stableId),
    createdAt: currentAt,
    updatedAt: currentAt,
    lastSeenAt: currentAt,
    settings: defaultSettings(session.language, currentAt),
    favorites: [],
    currentRide: undefined,
    undoRide: undefined,
    mutes: [],
    subscriptions: [],
    recentActionState: { updatedAt: currentAt },
  });
  scheduleRiderExpiryJobs(tx, next);
  return { session, rider: next };
}

function scheduleCutoffHour(tx: any): number {
  const config = tx.db.trainbot_runtime_config.id.find('runtime');
  const raw = Number(config?.scheduleCutoffHour);
  if (Number.isFinite(raw) && raw >= 0 && raw <= 23) {
    return Math.floor(raw);
  }
  return DEFAULT_SCHEDULE_CUTOFF_HOUR;
}

function scheduleContextPayload(tx: any) {
  const now = nowDate(tx);
  const requestedServiceDate = formatServiceDateFor(now);
  const fallbackServiceDate = formatServiceDateFor(new Date(now.getTime() - 24 * 60 * 60 * 1000));
  const cutoffHour = scheduleCutoffHour(tx);
  const todayCount = rowsFrom(tx.db.trainbot_trip.serviceDate.filter(requestedServiceDate)).length;
  const fallbackCount = rowsFrom(tx.db.trainbot_trip.serviceDate.filter(fallbackServiceDate)).length;
  const beforeCutoff = isBeforeScheduleCutoff(now, cutoffHour);
  const available = todayCount > 0 || (beforeCutoff && fallbackCount > 0);
  const fallbackActive = todayCount === 0 && beforeCutoff && fallbackCount > 0;
  const effectiveServiceDate = todayCount > 0
    ? requestedServiceDate
    : fallbackActive
      ? fallbackServiceDate
      : '';
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

function withSchedulePayload(tx: any, payload: Record<string, unknown>) {
  return {
    ...payload,
    schedule: scheduleContextPayload(tx),
  };
}

function activeServiceDate(tx: any): string {
  const schedule = scheduleContextPayload(tx);
  if (schedule.available && schedule.effectiveServiceDate) {
    return schedule.effectiveServiceDate;
  }
  return formatServiceDateFor(nowDate(tx));
}

function listTripsForServiceDate(tx: any, serviceDate: string): any[] {
  const trips = rowsFrom(tx.db.trainbot_trip.serviceDate.filter(serviceDate));
  trips.sort((left: any, right: any) => compareTimeAscending(left.departureAt, right.departureAt));
  return trips;
}

function listTripPublicRowsForServiceDate(tx: any, serviceDate: string): any[] {
  const trips = rowsFrom(tx.db.trainbot_trip_public.serviceDate.filter(serviceDate));
  trips.sort((left: any, right: any) => compareTimeAscending(left.departureAt, right.departureAt));
  return trips;
}

function serviceDayRow(tx: any, serviceDate: string): any | null {
  return tx.db.trainbot_service_day.serviceDate.find(serviceDate) || null;
}

function listStationsForServiceDate(tx: any, serviceDate: string): any[] {
  const projected = rowsFrom(tx.db.trainbot_service_station.serviceDate.filter(serviceDate));
  const stations = projected.length
    ? projected.map((item: any) => ({
      id: item.stationId,
      name: item.name,
      normalizedKey: item.normalizedKey,
      latitude: item.latitude,
      longitude: item.longitude,
    }))
    : Array.isArray(serviceDayRow(tx, serviceDate)?.stations)
      ? serviceDayRow(tx, serviceDate).stations.slice()
      : [];
  stations.sort((left: any, right: any) => asString(left.name).localeCompare(asString(right.name)));
  return stations;
}

function stationById(tx: any, stationId: string): any | null {
  const cleanId = stationId.trim();
  if (!cleanId) {
    return null;
  }
  const preferredDates = [activeServiceDate(tx), formatServiceDateFor(new Date(nowDate(tx).getTime() - 24 * 60 * 60 * 1000))];
  const seenDates = new Set<string>();
  for (const date of preferredDates) {
    if (!date || seenDates.has(date)) {
      continue;
    }
    seenDates.add(date);
    for (const station of listStationsForServiceDate(tx, date)) {
      if (station.id === cleanId) {
        return station;
      }
    }
  }
  for (const day of rowsFrom(tx.db.trainbot_service_day.iter())) {
    if (seenDates.has(day.serviceDate)) {
      continue;
    }
    for (const station of Array.isArray(day.stations) ? day.stations : []) {
      if (station.id === cleanId) {
        return station;
      }
    }
  }
  return null;
}

function stationNameFor(tx: any, stationId: string): string {
  return stationById(tx, stationId)?.name || '';
}

function trainById(tx: any, trainId: string): any | null {
  return tx.db.trainbot_trip.id.find(trainId) || null;
}

function trainStopsSorted(tx: any, trainId: string): any[] {
  const train = trainById(tx, trainId);
  const stops = Array.isArray(train?.stops) ? train.stops.slice() : [];
  stops.sort((left: any, right: any) => Number(left.seq) - Number(right.seq));
  return stops;
}

function findTerminalStop(tx: any, trainId: string): any | null {
  const stops = trainStopsSorted(tx, trainId);
  return stops.length ? stops[stops.length - 1] : null;
}

function stopPassAt(stop: any): string {
  return asString(stop.departureAt || stop.arrivalAt || '');
}

function stopArrivalOrDeparture(stop: any): string {
  return asString(stop.arrivalAt || stop.departureAt || '');
}

function selectedStopForBoarding(tx: any, trainId: string, stationId: string): any | null {
  for (const stop of trainStopsSorted(tx, trainId)) {
    if (stop.stationId === stationId) {
      return stop;
    }
  }
  return null;
}

function validateCheckIn(tx: any, trainId: string, boardingStationId: string | undefined) {
  const now = nowDate(tx);
  const train = requireCheckInTrain(tx, trainId);
  const arrivalAt = parseISO(train.arrivalAt);
  if (arrivalAt && now.getTime() >= arrivalAt.getTime() + CHECKIN_GRACE_MS) {
    throw new SenderError('check-in unavailable');
  }
  if (boardingStationId) {
    const stop = selectedStopForBoarding(tx, trainId, boardingStationId);
    if (stop) {
      const passAt = parseISO(stop.departureAt || stop.arrivalAt || '');
      if (passAt && now.getTime() >= passAt.getTime() + CHECKIN_GRACE_MS) {
        throw new SenderError('check-in unavailable');
      }
    } else {
      throw new SenderError('not found');
    }
  }
}

function requireCheckInTrain(tx: any, trainId: string): any {
  const train = trainById(tx, trainId);
  if (!train) {
    throw new SenderError('not found');
  }
  return train;
}

function nextAutoCheckoutAt(tx: any, trainId: string): string {
  const train = trainById(tx, trainId);
  if (!train) {
    return isoPlus(nowISO(tx), CHECKIN_FALLBACK_WINDOW_MS);
  }
  return isoPlus(train.arrivalAt, CHECKIN_GRACE_MS);
}

function nextMapAutoCheckoutAt(tx: any, trainId: string): string {
  const train = trainById(tx, trainId);
  if (!train) {
    return isoPlus(nowISO(tx), CHECKIN_FALLBACK_WINDOW_MS);
  }
  const fallbackAt = parseISO(isoPlus(nowISO(tx), CHECKIN_FALLBACK_WINDOW_MS));
  const trainAutoCheckoutAt = parseISO(isoPlus(train.arrivalAt, CHECKIN_GRACE_MS));
  if (!fallbackAt || !trainAutoCheckoutAt) {
    return isoPlus(nowISO(tx), CHECKIN_FALLBACK_WINDOW_MS);
  }
  return trainAutoCheckoutAt.getTime() >= fallbackAt.getTime()
    ? trainAutoCheckoutAt.toISOString()
    : fallbackAt.toISOString();
}

function putCurrentRide(tx: any, rider: any, trainId: string, boardingStationId: string, autoCheckoutAt: string) {
  return putRiderRow(tx, {
    ...rider,
    currentRide: {
      trainInstanceId: trainId,
      boardingStationId,
      checkedInAt: nowISO(tx),
      autoCheckoutAt,
    },
    undoRide: undefined,
    updatedAt: nowISO(tx),
    lastSeenAt: nowISO(tx),
    recentActionState: { updatedAt: nowISO(tx) },
  });
}

function buildCurrentRide(tx: any, stableId: string) {
  const rider = tx.db.trainbot_rider.stableId.find(stableId);
  if (!rider || !rider.currentRide) {
    return null;
  }
  const autoCheckoutAt = parseISO(rider.currentRide.autoCheckoutAt);
  if (!autoCheckoutAt || autoCheckoutAt.getTime() < nowDate(tx).getTime()) {
    return null;
  }
  const boardingStationId = asString(rider.currentRide.boardingStationId).trim();
  return {
    checkIn: {
      trainInstanceId: rider.currentRide.trainInstanceId,
      boardingStationId,
      checkedInAt: rider.currentRide.checkedInAt,
      autoCheckoutAt: rider.currentRide.autoCheckoutAt,
    },
    train: buildTrainStatusView(tx, stableId, rider.currentRide.trainInstanceId),
    boardingStationId,
    boardingStationName: stationNameFor(tx, boardingStationId),
  };
}

function buildBootstrapPayload(tx: any) {
  const { session, rider } = ensureRider(tx);
  return {
    userId: session.stableId,
    stableUserId: session.stableId,
    nickname: rider ? rider.nickname : genericNickname(session.stableId),
    settings: rider ? rider.settings : defaultSettings(session.language, nowISO(tx)),
    currentRide: buildCurrentRide(tx, session.stableId),
  };
}

function favoriteListPayload(tx: any) {
  const { rider } = loadViewer(tx);
  const favorites = rider && Array.isArray(rider.favorites) ? rider.favorites.slice() : [];
  favorites.sort((left: any, right: any) => compareTimeDescending(left.createdAt, right.createdAt));
  return {
    favorites: favorites.map((row: any) => ({
      fromStationId: row.fromStationId,
      fromStationName: row.fromStationName,
      toStationId: row.toStationId,
      toStationName: row.toStationName,
    })),
  };
}

function trainActivityId(trainId: string, serviceDate: string): string {
  return `train:${trainId}:${serviceDate}`;
}

function stationActivityId(stationId: string, serviceDate: string): string {
  return `station:${stationId}:${serviceDate}`;
}

function activityVoteSummary(activity: any, viewerStableId: string) {
  let ongoing = 0;
  let cleared = 0;
  let userValue = '';
  for (const vote of activity.votes || []) {
    const value = asString(vote.value).trim().toUpperCase();
    if (value === 'ONGOING') {
      ongoing += 1;
    } else if (value === 'CLEARED') {
      cleared += 1;
    }
    if (viewerStableId && vote.stableId === viewerStableId) {
      userValue = value;
    }
  }
  return { ongoing, cleared, userValue };
}

function trainSignalIncidentLabel(signal: string): string {
  switch (signal.trim().toUpperCase()) {
    case 'INSPECTION_STARTED':
      return 'Inspection started';
    case 'INSPECTION_IN_MY_CAR':
      return 'Inspection in carriage';
    case 'INSPECTION_ENDED':
      return 'Inspection ended';
    default:
      return signal.trim();
  }
}

function buildTrainState(tx: any, trainId: string) {
  const train = trainById(tx, trainId);
  const serviceDate = asString(train?.serviceDate).trim();
  const activity = serviceDate ? tx.db.trainbot_activity.id.find(trainActivityId(trainId, serviceDate)) : null;
  const reports = (activity?.timeline || []).filter((item: any) => item.kind === 'report');
  if (!reports.length) {
    return {
      state: 'NO_REPORTS',
      confidence: 'LOW',
      uniqueReporters: 0,
    };
  }
  const now = nowDate(tx);
  let latestAt = reports[0].createdAt;
  let hasIssue = false;
  let hasResolved = false;
  const recentWindowMs = now.getTime() - 10 * 60 * 1000;
  const confidenceWindowMs = now.getTime() - 15 * 60 * 1000;
  const unique = new Set<string>();
  for (const report of reports) {
    const createdMs = parseISO(report.createdAt)?.getTime() || 0;
    if (createdMs > (parseISO(latestAt)?.getTime() || 0)) {
      latestAt = report.createdAt;
    }
    if (createdMs >= recentWindowMs) {
      if (report.signal === 'INSPECTION_STARTED' || report.signal === 'INSPECTION_IN_MY_CAR') {
        hasIssue = true;
      }
      if (report.signal === 'INSPECTION_ENDED') {
        hasResolved = true;
      }
    }
    if (createdMs >= confidenceWindowMs && report.stableId) {
      unique.add(report.stableId);
    }
  }
  const latestMs = parseISO(latestAt)?.getTime() || 0;
  return {
    state: hasIssue && hasResolved ? 'MIXED_REPORTS' : 'LAST_SIGHTING',
    lastReportAt: latestAt,
    confidence: now.getTime() - latestMs > 15 * 60 * 1000
      ? 'LOW'
      : unique.size >= 3
        ? 'HIGH'
        : unique.size === 2
          ? 'MEDIUM'
          : 'LOW',
    uniqueReporters: unique.size,
  };
}

function trainReportEvents(tx: any, trainId: string): any[] {
  const train = trainById(tx, trainId);
  const serviceDate = asString(train?.serviceDate).trim();
  const activity = serviceDate ? tx.db.trainbot_activity.id.find(trainActivityId(trainId, serviceDate)) : null;
  const items = (activity?.timeline || []).filter((item: any) => item.kind === 'report');
  items.sort((left: any, right: any) => compareTimeDescending(left.createdAt, right.createdAt));
  return items;
}

function recentTimeline(tx: any, trainId: string, limit: number): any[] {
  const grouped = new Map<string, { at: string; signal: string; count: number }>();
  for (const report of trainReportEvents(tx, trainId).slice(0, 200)) {
    const date = parseISO(report.createdAt);
    if (!date) {
      continue;
    }
    date.setUTCSeconds(0, 0);
    const bucket = date.toISOString();
    const key = `${bucket}|${report.signal}`;
    const existing = grouped.get(key);
    if (existing) {
      existing.count += 1;
      continue;
    }
    grouped.set(key, { at: bucket, signal: report.signal, count: 1 });
  }
  const items = Array.from(grouped.values());
  items.sort((left, right) => compareTimeDescending(left.at, right.at));
  return items.slice(0, limit > 0 ? limit : items.length);
}

function activeRidersForTrain(tx: any, trainId: string): number {
  return lookupActiveCheckinUserIds(tx, trainId, nowDate(tx)).length;
}

function buildTrainCard(tx: any, stableId: string, train: any) {
  return {
    train: {
      id: train.id,
      serviceDate: train.serviceDate,
      fromStation: train.fromStationName,
      toStation: train.toStationName,
      departureAt: train.departureAt,
      arrivalAt: train.arrivalAt,
      sourceVersion: train.sourceVersion,
    },
    status: buildTrainState(tx, train.id),
    riders: activeRidersForTrain(tx, train.id),
  };
}

function stationActivities(tx: any): any[] {
  return rowsFrom(tx.db.trainbot_activity.scopeType.filter('station'));
}

function stationSightingsSince(tx: any, sinceMs: number, limit: number): any[] {
  const items: any[] = [];
  for (const activity of stationActivities(tx)) {
    for (const event of activity.timeline || []) {
      if (event.kind !== 'station_sighting') {
        continue;
      }
      const createdMs = parseISO(event.createdAt)?.getTime() || 0;
      if (createdMs >= sinceMs) {
        items.push({
          id: event.id,
          stationId: event.stationId,
          stationName: event.stationName,
          destinationStationId: event.destinationStationId,
          destinationStationName: event.destinationStationName,
          matchedTrainInstanceId: event.matchedTrainInstanceId,
          createdAt: event.createdAt,
        });
      }
    }
  }
  items.sort((left, right) => compareTimeDescending(left.createdAt, right.createdAt));
  return items.slice(0, limit > 0 ? limit : items.length);
}

function recentStationSightingsByStation(tx: any, stationId: string, minutes: number, limit: number): any[] {
  const sinceMs = nowDate(tx).getTime() - minutes * 60 * 1000;
  const items = stationSightingsSince(tx, sinceMs, 500).filter((item) => item.stationId === stationId);
  return items.slice(0, limit > 0 ? limit : items.length);
}

function stationSightingsByStationSince(tx: any, stationId: string, sinceMs: number, limit: number): any[] {
  const items = stationSightingsSince(tx, sinceMs, 500).filter((item) => item.stationId === stationId);
  return items.slice(0, limit > 0 ? limit : items.length);
}

function recentStationSightingsByTrain(tx: any, trainId: string, minutes: number, limit: number): any[] {
  const sinceMs = nowDate(tx).getTime() - minutes * 60 * 1000;
  const items = stationSightingsSince(tx, sinceMs, 500).filter((item) => asString(item.matchedTrainInstanceId) === trainId);
  return items.slice(0, limit > 0 ? limit : items.length);
}

function stationSightingContextForPassAt(items: any[], passAt: string): any[] {
  const passMs = parseISO(passAt)?.getTime() || 0;
  return items.filter((item) => {
    const createdMs = parseISO(item.createdAt)?.getTime() || 0;
    return Math.abs(createdMs - passMs) <= 30 * 60 * 1000;
  });
}

function buildTrainStatusView(tx: any, stableId: string, trainId: string) {
  const train = trainById(tx, trainId);
  if (!train) {
    return null;
  }
  return {
    trainCard: buildTrainCard(tx, stableId, train),
    timeline: recentTimeline(tx, trainId, 5),
    stationSightings: recentStationSightingsByTrain(tx, trainId, 30, 5),
  };
}

function buildPublicTrainView(tx: any, trainId: string) {
  const train = trainById(tx, trainId);
  if (!train) {
    throw new SenderError('not found');
  }
  return {
    train: {
      id: train.id,
      serviceDate: train.serviceDate,
      fromStation: train.fromStationName,
      toStation: train.toStationName,
      departureAt: train.departureAt,
      arrivalAt: train.arrivalAt,
      sourceVersion: train.sourceVersion,
    },
    status: buildTrainState(tx, trainId),
    riders: activeRidersForTrain(tx, trainId),
    timeline: recentTimeline(tx, trainId, 5),
    stationSightings: recentStationSightingsByTrain(tx, trainId, 30, 5),
  };
}

function trainsByWindow(tx: any, windowId: string): any[] {
  const now = nowDate(tx);
  let start = new Date(now.getTime());
  let end = new Date(now.getTime());
  switch (windowId.trim()) {
    case 'now':
      start = new Date(now.getTime() - 15 * 60 * 1000);
      end = new Date(now.getTime() + 15 * 60 * 1000);
      break;
    case 'next_hour':
      start = now;
      end = new Date(now.getTime() + 60 * 60 * 1000);
      break;
    case 'today':
    default:
      start = new Date(now.getTime() - 30 * 60 * 1000);
      end = utcDayEnd(now);
      break;
  }
  const startMs = start.getTime();
  const endMs = end.getTime();
  return listTripsForServiceDate(tx, activeServiceDate(tx)).filter((train) => {
    const departureAt = parseISO(train.departureAt);
    if (!departureAt) {
      return false;
    }
    const departureMs = departureAt.getTime();
    return departureMs >= startMs && departureMs <= endMs;
  });
}

function stationWindowTrains(tx: any, stationId: string, startMs: number, endMs: number): any[] {
  const items: any[] = [];
  for (const train of listTripsForServiceDate(tx, activeServiceDate(tx))) {
    const stops = trainStopsSorted(tx, train.id);
    for (const stop of stops) {
      if (stop.stationId !== stationId) {
        continue;
      }
      const passAt = stopPassAt(stop);
      const passMs = parseISO(passAt)?.getTime() || 0;
      if (passMs < startMs || passMs > endMs) {
        continue;
      }
      items.push({
        train,
        stationId,
        stationName: stop.stationName,
        passAt,
      });
      break;
    }
  }
  items.sort((left, right) => compareTimeAscending(left.passAt, right.passAt));
  return items;
}

function buildStationTrainCards(tx: any, stableId: string, items: any[], nowMs: number, sightings: any[]): any[] {
  return items.map((item) => {
    const context = stationSightingContextForPassAt(sightings, item.passAt);
    return {
      trainCard: buildTrainCard(tx, stableId, item.train),
      stationId: item.stationId,
      stationName: item.stationName,
      passAt: item.passAt,
      sightingCount: context.length,
      sightingContext: context,
    };
  });
}

function routeWindowTrains(tx: any, fromStationId: string, toStationId: string, startMs: number, endMs: number): any[] {
  const items: any[] = [];
  for (const train of listTripsForServiceDate(tx, activeServiceDate(tx))) {
    const stops = trainStopsSorted(tx, train.id);
    let fromStop: any = null;
    let toStop: any = null;
    for (const stop of stops) {
      if (!fromStop && stop.stationId === fromStationId) {
        fromStop = stop;
        continue;
      }
      if (fromStop && stop.stationId === toStationId && Number(stop.seq) > Number(fromStop.seq)) {
        toStop = stop;
        break;
      }
    }
    if (!fromStop || !toStop) {
      continue;
    }
    const fromPassAt = stopPassAt(fromStop);
    const fromMs = parseISO(fromPassAt)?.getTime() || 0;
    if (fromMs < startMs || fromMs > endMs) {
      continue;
    }
    items.push({
      train,
      fromStationId,
      fromStationName: fromStop.stationName,
      toStationId,
      toStationName: toStop.stationName,
      fromPassAt,
      toPassAt: stopArrivalOrDeparture(toStop),
    });
  }
  items.sort((left, right) => compareTimeAscending(left.fromPassAt, right.fromPassAt));
  return items;
}

function routeDestinations(tx: any, fromStationId: string): any[] {
  const destinations = new Map<string, any>();
  for (const train of listTripsForServiceDate(tx, activeServiceDate(tx))) {
    const stops = trainStopsSorted(tx, train.id);
    let seenOrigin = false;
    for (const stop of stops) {
      if (!seenOrigin) {
        if (stop.stationId === fromStationId) {
          seenOrigin = true;
        }
        continue;
      }
      if (destinations.has(stop.stationId)) {
        continue;
      }
      const station = stationById(tx, stop.stationId);
      if (station) {
        destinations.set(stop.stationId, station);
      }
    }
  }
  const items = Array.from(destinations.values());
  items.sort((left, right) => asString(left.name).localeCompare(asString(right.name)));
  return items;
}

function terminalDestinations(tx: any, fromStationId: string): any[] {
  const destinations = new Map<string, any>();
  for (const train of listTripsForServiceDate(tx, activeServiceDate(tx))) {
    const stops = trainStopsSorted(tx, train.id);
    const originIndex = stops.findIndex((stop) => stop.stationId === fromStationId);
    if (originIndex < 0 || originIndex >= stops.length - 1) {
      continue;
    }
    const terminal = stops[stops.length - 1];
    if (!terminal || Number(terminal.seq) <= Number(stops[originIndex].seq)) {
      continue;
    }
    const station = stationById(tx, terminal.stationId);
    if (station) {
      destinations.set(terminal.stationId, station);
    }
  }
  const items = Array.from(destinations.values());
  items.sort((left, right) => asString(left.name).localeCompare(asString(right.name)));
  return items;
}

function maybeResolveMatchedTrain(tx: any, stationId: string, destinationStationId: string | undefined): string {
  if (!destinationStationId) {
    return '';
  }
  const now = nowDate(tx);
  const candidates = routeWindowTrains(
    tx,
    stationId,
    destinationStationId,
    now.getTime() - STATION_MATCH_PAST_WINDOW_MS,
    now.getTime() + STATION_MATCH_FUTURE_WINDOW_MS
  ).map((item) => item.train.id);
  const unique = Array.from(new Set(candidates));
  return unique.length === 1 ? unique[0] : '';
}

function trainStopPayload(tx: any, stableId: string, trainId: string) {
  const train = trainById(tx, trainId);
  if (!train) {
    throw new SenderError('not found');
  }
  return {
    trainCard: buildTrainCard(tx, stableId, train),
    train: {
      id: train.id,
      serviceDate: train.serviceDate,
      fromStation: train.fromStationName,
      toStation: train.toStationName,
      departureAt: train.departureAt,
      arrivalAt: train.arrivalAt,
      sourceVersion: train.sourceVersion,
    },
    stops: trainStopsSorted(tx, trainId),
    stationSightings: recentStationSightingsByTrain(tx, trainId, 30, 10),
  };
}

function publicDashboardPayload(tx: any, limit: number) {
  const now = nowDate(tx);
  const startMs = now.getTime() - 30 * 60 * 1000;
  const endMs = utcDayEnd(now).getTime();
  const serviceDate = activeServiceDate(tx);
  const projected = listTripPublicRowsForServiceDate(tx, serviceDate);
  const source = projected.length
    ? projected
    : listTripsForServiceDate(tx, serviceDate).map((train) => {
      const status = buildTrainState(tx, train.id);
      const buckets = recentTimeline(tx, train.id, 0);
      return {
        id: train.id,
        serviceDate: train.serviceDate,
        fromStationId: train.fromStationId,
        fromStationName: train.fromStationName,
        toStationId: train.toStationId,
        toStationName: train.toStationName,
        departureAt: train.departureAt,
        arrivalAt: train.arrivalAt,
        sourceVersion: train.sourceVersion,
        state: asString(status.state).trim(),
        confidence: asString(status.confidence).trim(),
        uniqueReporters: Number(status.uniqueReporters) || 0,
        riders: activeRidersForTrain(tx, train.id),
        lastReportAt: trimOptional(asString(status.lastReportAt)) || '',
        updatedAt: nowISO(tx),
        recentTimeline: buckets.map((bucket) => ({
          at: asString(bucket.at).trim(),
          signal: asString(bucket.signal).trim(),
          count: Number(bucket.count) || 0,
        })),
      };
    });
  const trains = source.filter((train) => {
    const departureMs = parseISO(asString(train.departureAt))?.getTime() || 0;
    return departureMs >= startMs && departureMs <= endMs;
  });
  const trimmed = limit > 0 ? trains.slice(0, limit) : trains;
  return trimmed.map((train) => ({
    train: {
      id: train.id,
      serviceDate: train.serviceDate,
      fromStation: train.fromStationName,
      toStation: train.toStationName,
      departureAt: train.departureAt,
      arrivalAt: train.arrivalAt,
      sourceVersion: train.sourceVersion,
    },
    status: {
      state: asString(train.state).trim() || 'NO_REPORTS',
      confidence: asString(train.confidence).trim() || 'LOW',
      uniqueReporters: Number(train.uniqueReporters) || 0,
      lastReportAt: asString(train.lastReportAt).trim(),
    },
    riders: Number(train.riders) || 0,
    timeline: (train.recentTimeline || []).slice(0, 5).map((bucket: any) => ({
      at: asString(bucket.at).trim(),
      signal: asString(bucket.signal).trim(),
      count: Number(bucket.count) || 0,
    })),
    stationSightings: [],
  }));
}

function publicServiceDayPayload(tx: any) {
  return listTripsForServiceDate(tx, activeServiceDate(tx)).map((train) => ({
    train: {
      id: train.id,
      serviceDate: train.serviceDate,
      fromStation: train.fromStationName,
      toStation: train.toStationName,
      departureAt: train.departureAt,
      arrivalAt: train.arrivalAt,
      sourceVersion: train.sourceVersion,
    },
    status: buildTrainState(tx, train.id),
    riders: activeRidersForTrain(tx, train.id),
    timeline: recentTimeline(tx, train.id, 5),
    stationSightings: [],
  }));
}

function publicStationDeparturesPayload(tx: any, stationId: string, limit: number) {
  const station = stationById(tx, stationId);
  if (!station) {
    throw new SenderError('not found');
  }
  const now = nowDate(tx);
  const recent = stationWindowTrains(tx, stationId, utcDayStart(now).getTime(), now.getTime() - 1);
  const upcoming = stationWindowTrains(tx, stationId, now.getTime(), utcDayEnd(now).getTime());
  const lastDeparture = recent.length
    ? buildStationTrainCards(tx, '', recent.slice(-1), now.getTime(), [])[0]
    : null;
  return {
    station,
    lastDeparture,
    upcoming: buildStationTrainCards(tx, '', limit > 0 ? upcoming.slice(0, limit) : upcoming, now.getTime(), []),
    recentSightings: recentStationSightingsByStation(tx, stationId, 30, 10),
  };
}

function stationDeparturesPayload(tx: any, stableId: string, stationId: string) {
  const station = stationById(tx, stationId);
  if (!station) {
    throw new SenderError('not found');
  }
  const now = nowDate(tx);
  const startMs = now.getTime() - 2 * 60 * 60 * 1000;
  const endMs = now.getTime() + 2 * 60 * 60 * 1000;
  const trains = stationWindowTrains(tx, stationId, startMs, endMs);
  const contextSightings = stationSightingsByStationSince(tx, stationId, startMs - 30 * 60 * 1000, 250);
  return {
    station,
    trains: buildStationTrainCards(tx, stableId, trains, now.getTime(), contextSightings),
    recentSightings: recentStationSightingsByStation(tx, stationId, 30, 10),
  };
}

function networkMapPayload(tx: any) {
  const stations = listStationsForServiceDate(tx, activeServiceDate(tx)).filter((item) => item.latitude != null && item.longitude != null);
  const now = nowDate(tx);
  const sameDaySightings = stationSightingsSince(tx, utcDayStart(now).getTime(), 500);
  const visibleTrainIds = new Set(publicDashboardPayload(tx, 0).map((item) => asString(item.train.id)));
  const recentSightings = sameDaySightings.filter((item) => {
    const createdMs = parseISO(item.createdAt)?.getTime() || 0;
    return createdMs >= now.getTime() - 30 * 60 * 1000
      && asString(item.matchedTrainInstanceId) !== ''
      && visibleTrainIds.has(asString(item.matchedTrainInstanceId));
  });
  return {
    stations,
    recentSightings,
    sameDaySightings,
  };
}

function incidentSummaryPayload(tx: any, activity: any, viewerStableId: string) {
  return {
    id: activity.id,
    scope: activity.scopeType,
    subjectId: activity.subjectId,
    subjectName: activity.subjectName,
    lastReportName: activity.summary.lastReportName,
    lastReportAt: activity.summary.lastReportAt,
    lastActivityName: activity.summary.lastActivityName,
    lastActivityAt: activity.summary.lastActivityAt,
    lastActivityActor: activity.summary.lastActivityActor,
    lastReporter: activity.summary.lastReporter,
    commentCount: Array.isArray(activity.comments) ? activity.comments.length : 0,
    votes: activityVoteSummary(activity, viewerStableId),
    active: activity.active === true,
  };
}

function listIncidentSummariesPayload(tx: any, limit: number) {
  const viewerStableId = optionalViewerStableId(tx);
  const serviceDate = activeServiceDate(tx);
  const items = rowsFrom(tx.db.trainbot_activity.iter())
    .filter((item) => item.serviceDate === serviceDate)
    .sort((left, right) => compareTimeDescending(left.lastActivityAt, right.lastActivityAt))
    .map((item) => incidentSummaryPayload(tx, item, viewerStableId));
  return limit > 0 ? items.slice(0, limit) : items;
}

function incidentDetailPayload(tx: any, incidentId: string) {
  const activity = tx.db.trainbot_activity.id.find(incidentId);
  if (!activity) {
    throw new SenderError('not found');
  }
  const viewerStableId = optionalViewerStableId(tx);
  const timelineEvents = (activity.timeline || []).map((item: any) => ({
    id: item.id,
    kind: item.kind === 'station_sighting' ? 'report' : item.kind,
    name: item.name,
    detail: item.detail,
    nickname: item.nickname,
    createdAt: item.createdAt,
  }));
  const commentEvents = (activity.comments || []).map((item: any) => ({
    id: item.id,
    kind: 'comment',
    name: incidentCommentActivityLabel(),
    detail: item.body,
    nickname: item.nickname,
    createdAt: item.createdAt,
  }));
  const voteEvents = (activity.votes || [])
    .filter((item: any) => asString(item.value).trim().toUpperCase() === 'ONGOING')
    .map((item: any) => ({
      id: `${activity.id}|vote|${item.stableId}`,
      kind: 'vote',
      name: incidentVoteEventLabel(item.value),
      nickname: item.nickname,
      createdAt: item.updatedAt,
    }));
  const events = [...timelineEvents, ...commentEvents, ...voteEvents];
  events.sort((left, right) => compareTimeDescending(left.createdAt, right.createdAt));
  return {
    summary: incidentSummaryPayload(tx, activity, viewerStableId),
    events,
    comments: (activity.comments || []).slice(),
  };
}

function appendTimelineEvent(activity: any, event: any): any {
  return refreshActivityRow(
    { timestamp: { toDate: () => new Date(), toISOString: () => new Date().toISOString() } },
    { ...activity, timeline: [event, ...(activity.timeline || [])] }
  );
}

function trainActivityFor(tx: any, trainId: string): any {
  const train = trainById(tx, trainId);
  if (!train) {
    return null;
  }
  const id = trainActivityId(trainId, train.serviceDate);
  return tx.db.trainbot_activity.id.find(id) || null;
}

function latestReportFor(tx: any, stableId: string, trainId: string): any | null {
  const activity = trainActivityFor(tx, trainId);
  if (!activity) {
    return null;
  }
  for (const event of activity.timeline || []) {
    if (event.kind === 'report' && event.stableId === stableId) {
      return event;
    }
  }
  return null;
}

function latestStationSightingFor(tx: any, stableId: string, stationId: string, destinationStationId: string | undefined): any | null {
  const activity = tx.db.trainbot_activity.id.find(stationActivityId(stationId, activeServiceDate(tx))) || null;
  if (!activity) {
    return null;
  }
  for (const event of activity.timeline || []) {
    const currentDestination = asString(event.destinationStationId).trim();
    if (event.kind !== 'station_sighting' || event.stableId !== stableId || currentDestination !== (destinationStationId || '')) {
      continue;
    }
    return event;
  }
  return null;
}

function ensureTrainActivity(tx: any, trainId: string): any {
  const train = trainById(tx, trainId);
  if (!train) {
    throw new SenderError('not found');
  }
  const id = trainActivityId(trainId, train.serviceDate);
  const existing = tx.db.trainbot_activity.id.find(id);
  if (existing) {
    return existing;
  }
  return putActivityRow(tx, {
    id,
    scopeType: 'train',
    subjectId: trainId,
    subjectName: `${train.fromStationName} -> ${train.toStationName}`,
    serviceDate: train.serviceDate,
    timeline: [],
    comments: [],
    votes: [],
  });
}

function ensureStationActivity(tx: any, stationId: string, stationName: string, serviceDate: string): any {
  const id = stationActivityId(stationId, serviceDate);
  const existing = tx.db.trainbot_activity.id.find(id);
  if (existing) {
    return existing;
  }
  return putActivityRow(tx, {
    id,
    scopeType: 'station',
    subjectId: stationId,
    subjectName: stationName,
    serviceDate,
    timeline: [],
    comments: [],
    votes: [],
  });
}

function parseBatchItems(itemsJson: string): Record<string, unknown>[] {
  const parsed = parseJSON(itemsJson, 'invalid batch JSON');
  if (Array.isArray(parsed)) {
    return parsed.filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object');
  }
  if (parsed && typeof parsed === 'object' && Array.isArray((parsed as ParsedObject).items)) {
    return ((parsed as ParsedObject).items as unknown[]).filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === 'object');
  }
  throw new SenderError('invalid batch JSON');
}

function rowsForImport(tx: any, importId: string): any[] {
  return rowsFrom(tx.db.trainbot_import_chunk.importId.filter(importId));
}

function clearImport(tx: any, importId: string): number {
  let deleted = 0;
  for (const row of rowsForImport(tx, importId)) {
    deleted += 1;
    tx.db.trainbot_import_chunk.id.delete(row.id);
  }
  return deleted;
}

function clearFeedEventsForImport(tx: any, importId: string): number {
  let deleted = 0;
  for (const row of rowsFrom(tx.db.trainbot_feed_event.importId.filter(asString(importId).trim()))) {
    deleted += 1;
    tx.db.trainbot_feed_event.id.delete(row.id);
  }
  return deleted;
}

function clearFeedImport(tx: any, importId: string): number {
  const cleanImportId = asString(importId).trim();
  const existing = tx.db.trainbot_feed_import.importId.find(cleanImportId);
  if (!existing) {
    return 0;
  }
  tx.db.trainbot_feed_import.importId.delete(cleanImportId);
  return 1;
}

function clearImportArtifacts(tx: any, importId: string): any {
  return {
    importChunksDeleted: clearImport(tx, importId),
    feedEventsDeleted: clearFeedEventsForImport(tx, importId),
    feedImportsDeleted: clearFeedImport(tx, importId),
  };
}

function clearImportArtifactsByServiceDate(tx: any, serviceDate: string): any {
  let importChunksDeleted = 0;
  let feedEventsDeleted = 0;
  let feedImportsDeleted = 0;

  for (const row of rowsFrom(tx.db.trainbot_import_chunk.serviceDate.filter(serviceDate))) {
    importChunksDeleted += 1;
    tx.db.trainbot_import_chunk.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.trainbot_feed_event.serviceDate.filter(serviceDate))) {
    feedEventsDeleted += 1;
    tx.db.trainbot_feed_event.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.trainbot_feed_import.serviceDate.filter(serviceDate))) {
    feedImportsDeleted += 1;
    tx.db.trainbot_feed_import.importId.delete(asString(row.importId).trim());
  }

  return {
    importChunksDeleted,
    feedEventsDeleted,
    feedImportsDeleted,
  };
}

function upsertFeedImport(tx: any, importId: string, serviceDate: string, sourceVersion: string): any {
  const row = {
    importId: asString(importId).trim(),
    serviceDate: asString(serviceDate).trim(),
    sourceVersion: asString(sourceVersion).trim(),
    status: 'open',
    eventCount: 0,
    committedAt: '',
    abortedAt: '',
    createdAt: nowISO(tx),
    updatedAt: nowISO(tx),
  };
  return replaceFeedImport(tx, row);
}

function upsertFeedEvent(tx: any, event: {
  id: string;
  importId: string;
  serviceDate: string;
  kind: string;
  entityId: string;
  sourceVersion: string;
  payloadJson: string;
}): any {
  tx.db.trainbot_feed_event.id.delete(event.id);
  return tx.db.trainbot_feed_event.insert({
    id: asString(event.id).trim(),
    importId: asString(event.importId).trim(),
    serviceDate: asString(event.serviceDate).trim(),
    kind: asString(event.kind).trim(),
    entityId: asString(event.entityId).trim(),
    sourceVersion: asString(event.sourceVersion).trim(),
    createdAt: nowISO(tx),
    payloadJson: asString(event.payloadJson),
  });
}

function replaceFeedImport(tx: any, row: any): any {
  tx.db.trainbot_feed_import.importId.delete(asString(row.importId).trim());
  return tx.db.trainbot_feed_import.insert(row);
}

function replaceOpsState(tx: any, row: any): any {
  tx.db.trainbot_ops_state.id.delete(asString(row.id).trim());
  return tx.db.trainbot_ops_state.insert(row);
}

function markFeedImportCommitted(tx: any, importId: string, serviceDate: string, sourceVersion: string, eventCount: number): any {
  return replaceFeedImport(tx, {
    importId: asString(importId).trim(),
    serviceDate: asString(serviceDate).trim(),
    sourceVersion: asString(sourceVersion).trim(),
    status: 'committed',
    eventCount: Math.max(0, Math.floor(Number(eventCount) || 0)),
    committedAt: nowISO(tx),
    abortedAt: '',
    createdAt: nowISO(tx),
    updatedAt: nowISO(tx),
  });
}

function markFeedImportAborted(tx: any, importId: string, serviceDate: string, sourceVersion: string): any {
  return replaceFeedImport(tx, {
    importId: asString(importId).trim(),
    serviceDate: asString(serviceDate).trim(),
    sourceVersion: asString(sourceVersion).trim(),
    status: 'aborted',
    eventCount: 0,
    committedAt: '',
    abortedAt: nowISO(tx),
    createdAt: nowISO(tx),
    updatedAt: nowISO(tx),
  });
}

function recordCleanupRetentionPolicy(tx: any, nowIso: string, retentionCutoffIso: string, oldestKeptServiceDate: string): void {
  replaceOpsState(tx, {
    id: CLEANUP_RETENTION_POLICY_ID,
    kind: 'retention_policy',
    scopeKey: 'cleanup_expired_state',
    serviceDate: asString(oldestKeptServiceDate).trim(),
    updatedAt: asString(nowIso).trim(),
    sourceVersion: '',
    payloadJson: serialize({
      nowIso: asString(nowIso).trim(),
      retentionCutoffIso: asString(retentionCutoffIso).trim(),
      oldestKeptServiceDate: asString(oldestKeptServiceDate).trim(),
    }),
  });
}

function cleanupRetentionPolicy(tx: any): any | null {
  const row = tx.db.trainbot_ops_state.id.find(CLEANUP_RETENTION_POLICY_ID);
  if (!row) {
    return null;
  }
  try {
    const payload = JSON.parse(asString(row.payloadJson)) as ParsedObject;
    const retentionCutoffAt = parseISO(asString(payload?.retentionCutoffIso).trim());
    const oldestKeptServiceDate = asString(payload?.oldestKeptServiceDate).trim() || asString(row.serviceDate).trim();
    if (!retentionCutoffAt || !oldestKeptServiceDate) {
      return null;
    }
    return {
      retentionCutoffAt,
      oldestKeptServiceDate,
    };
  } catch {
    return null;
  }
}

function feedEventKey(importId: string, chunkKind: string, rowId: string, index: number, entityId: string): string {
  return `${asString(importId).trim()}|${asString(chunkKind).trim()}|${asString(rowId).trim()}|${Math.max(0, index)}|${asString(entityId).trim()}`;
}

function feedEventRowsForImport(tx: any, importId: string): any[] {
  const rows = rowsFrom(tx.db.trainbot_feed_event.importId.filter(asString(importId).trim()));
  rows.sort((left, right) => {
    const created = compareTimeAscending(asString(left.createdAt), asString(right.createdAt));
    if (created !== 0) {
      return created;
    }
    return asString(left.id).localeCompare(asString(right.id));
  });
  return rows;
}

function buildServiceDateProjectionFromChunkRows(chunkRows: any[], serviceDate: string, sourceVersion: string): {
  stations: any[];
  trains: any[];
  stopsByTrainId: Map<string, any[]>;
  eventCount: number;
} {
  const stationsById = new Map<string, any>();
  const trainsById = new Map<string, any>();
  const stopsByTrainId = new Map<string, any[]>();
  let eventCount = 0;
  for (const row of chunkRows) {
    const items = parseBatchItems(row.payloadJson);
    if (row.chunkKind === 'stations') {
      for (const item of items) {
        const station = sanitizeStationDoc(item);
        if (station.id) {
          stationsById.set(station.id, station);
        }
        eventCount += 1;
      }
      continue;
    }
    if (row.chunkKind === 'trips') {
      for (const item of items) {
        const train = sanitizeTrainDoc(item, serviceDate, sourceVersion);
        if (train.id) {
          trainsById.set(train.id, train);
        }
        eventCount += 1;
      }
      continue;
    }
    if (row.chunkKind === 'stops') {
      for (const item of items) {
        const trainId = asString(item.trainInstanceId || item.trainId).trim();
        const stop = sanitizeStopDoc(item);
        if (!trainId || !stop.stationName) {
          eventCount += 1;
          continue;
        }
        const next = stopsByTrainId.get(trainId) || [];
        next.push({
          ...stop,
          trainInstanceId: trainId,
        });
        stopsByTrainId.set(trainId, next);
        eventCount += 1;
      }
    }
  }
  return {
    stations: Array.from(stationsById.values()).sort((left, right) => asString(left.name).localeCompare(asString(right.name))),
    trains: Array.from(trainsById.values()).sort((left, right) => compareTimeAscending(left.departureAt, right.departureAt) || asString(left.id).localeCompare(asString(right.id))),
    stopsByTrainId,
    eventCount,
  };
}

function buildServiceDateProjectionFromFeedEvents(tx: any, importId: string, serviceDate: string): {
  stations: any[];
  trains: any[];
  stopsByTrainId: Map<string, any[]>;
  eventCount: number;
} {
  const stationsById = new Map<string, any>();
  const trainsById = new Map<string, any>();
  const stopsByTrainId = new Map<string, any[]>();
  let eventCount = 0;
  for (const event of feedEventRowsForImport(tx, importId)) {
    const payload = parseJSON(event.payloadJson, 'invalid feed event JSON');
    if (event.kind === 'station') {
      const station = sanitizeStationDoc(payload);
      if (station.id) {
        stationsById.set(station.id, station);
      }
      eventCount += 1;
      continue;
    }
    if (event.kind === 'train') {
      const train = sanitizeTrainDoc(payload, serviceDate, asString(event.sourceVersion).trim());
      if (train.id) {
        trainsById.set(train.id, train);
      }
      eventCount += 1;
      continue;
    }
    if (event.kind === 'stop') {
      const stop = sanitizeStopDoc(payload);
      const trainId = asString(payload?.trainInstanceId || payload?.trainId).trim();
      if (!trainId || !stop.stationName) {
        eventCount += 1;
        continue;
      }
      const next = stopsByTrainId.get(trainId) || [];
      next.push({
        ...stop,
        trainInstanceId: trainId,
      });
      stopsByTrainId.set(trainId, next);
      eventCount += 1;
      continue;
    }
  }
  return {
    stations: Array.from(stationsById.values()).sort((left, right) => asString(left.name).localeCompare(asString(right.name))),
    trains: Array.from(trainsById.values()).sort((left, right) => compareTimeAscending(left.departureAt, right.departureAt) || asString(left.id).localeCompare(asString(right.id))),
    stopsByTrainId,
    eventCount,
  };
}

function sanitizeTrainDoc(item: any, serviceDateFallback: string, sourceVersionFallback: string): any {
  const id = asString(item?.id || item?.trainId).trim();
  const serviceDate = trimOptional(asString(item?.serviceDate)) || asString(serviceDateFallback).trim();
  const departureAt = asString(item?.departureAt).trim();
  const arrivalAt = asString(item?.arrivalAt).trim();
  if (!id || !serviceDate || !departureAt || !arrivalAt) {
    throw new SenderError('invalid train feed event');
  }
  return {
    id,
    serviceDate,
    fromStationId: trimOptional(asString(item?.fromStationId)) || normalizeStationId(asString(item?.fromStation || item?.fromStationName)),
    fromStationName: asString(item?.fromStation || item?.fromStationName).trim(),
    toStationId: trimOptional(asString(item?.toStationId)) || normalizeStationId(asString(item?.toStation || item?.toStationName)),
    toStationName: asString(item?.toStation || item?.toStationName).trim(),
    departureAt,
    arrivalAt,
    sourceVersion: trimOptional(asString(item?.sourceVersion)) || asString(sourceVersionFallback).trim(),
  };
}

function scheduleAtFor(iso: string): any {
  const date = parseISO(iso);
  if (!date) {
    return ScheduleAt.time(BigInt(Date.now()) * 1000n);
  }
  return ScheduleAt.time(BigInt(date.getTime()) * 1000n);
}

function scheduledIdValue(value: unknown): bigint | null {
  try {
    if (typeof value === 'bigint') {
      return value;
    }
    if (typeof value === 'number' && Number.isFinite(value)) {
      return BigInt(Math.trunc(value));
    }
    const raw = asString(value).trim();
    if (!raw) {
      return null;
    }
    return BigInt(raw);
  } catch {
    return null;
  }
}

function scheduledIdToken(value: unknown): string {
  return scheduledIdValue(value)?.toString() || '';
}

function matchesScheduledJobRow(stored: any, arg: any): boolean {
  if (!stored || !arg) {
    return false;
  }
  return scheduledIdToken(stored.scheduled_id) === scheduledIdToken(arg.scheduled_id)
    && asString(stored.jobId).trim() === asString(arg.jobId).trim()
    && asString(stored.kind).trim() === asString(arg.kind).trim()
    && asString(stored.subjectId).trim() === asString(arg.subjectId).trim()
    && asString(stored.serviceDate).trim() === asString(arg.serviceDate).trim()
    && asString(stored.createdAt) === asString(arg.createdAt)
    && asString(stored.payloadJson) === asString(arg.payloadJson);
}

function requireScheduledJobRow(tx: any, arg: any): any {
  const scheduledId = scheduledIdValue(arg?.scheduled_id);
  if (scheduledId == null) {
    throw new SenderError('scheduled job mismatch');
  }
  const stored = tx.db.trainbot_job.scheduled_id.find(scheduledId);
  if (!matchesScheduledJobRow(stored, arg)) {
    throw new SenderError('scheduled job mismatch');
  }
  return stored;
}

function upsertJob(tx: any, id: string, whenIso: string, kind: string, subjectId: string, serviceDate: string, payload: unknown): void {
  for (const row of rowsFrom(tx.db.trainbot_job.jobId.filter(id))) {
    tx.db.trainbot_job.scheduled_id.delete(row.scheduled_id);
  }
  tx.db.trainbot_job.insert({
    scheduled_id: 0n,
    scheduled_at: scheduleAtFor(whenIso),
    jobId: id,
    kind,
    subjectId,
    serviceDate,
    createdAt: nowISO(tx),
    payloadJson: serialize(payload),
  });
}

function deleteJobsWithPrefix(tx: any, prefix: string): void {
  for (const row of rowsFrom(tx.db.trainbot_job.iter())) {
    if (asString(row.jobId).startsWith(prefix)) {
      tx.db.trainbot_job.scheduled_id.delete(row.scheduled_id);
    }
  }
}

function runtimeStatePayload(tx: any) {
  const now = nowDate(tx);
  const requestedServiceDate = formatServiceDateFor(now);
  const fallbackServiceDate = formatServiceDateFor(new Date(now.getTime() - 24 * 60 * 60 * 1000));
  const cutoffHour = scheduleCutoffHour(tx);
  const todayCount = rowsFrom(tx.db.trainbot_trip.serviceDate.filter(requestedServiceDate)).length;
  const fallbackCount = rowsFrom(tx.db.trainbot_trip.serviceDate.filter(fallbackServiceDate)).length;
  const beforeCutoff = isBeforeScheduleCutoff(now, cutoffHour);
  const available = todayCount > 0 || (beforeCutoff && fallbackCount > 0);
  const fallbackActive = todayCount === 0 && beforeCutoff && fallbackCount > 0;
  const effectiveServiceDate = todayCount > 0
    ? requestedServiceDate
    : fallbackActive
      ? fallbackServiceDate
      : '';
  return {
    id: 'runtime',
    requestedServiceDate,
    effectiveServiceDate,
    loadedServiceDate: effectiveServiceDate,
    fallbackActive,
    cutoffHour,
    available,
    sameDayFresh: todayCount > 0,
    updatedAt: now.toISOString(),
  };
}

function writeRuntimeState(tx: any): any {
  const next = runtimeStatePayload(tx);
  tx.db.trainbot_runtime_state.id.delete(next.id);
  return tx.db.trainbot_runtime_state.insert(next);
}

function writeCleanupSummary(tx: any, summary: any): any {
  const next = {
    id: 'cleanup',
    updatedAt: nowISO(tx),
    checkinsDeleted: Math.max(0, Number(summary?.checkinsDeleted) || 0),
    subscriptionsDeleted: Math.max(0, Number(summary?.subscriptionsDeleted) || 0),
    reportsDeleted: Math.max(0, Number(summary?.reportsDeleted) || 0),
    stationSightingsDeleted: Math.max(0, Number(summary?.stationSightingsDeleted) || 0),
    trainStopsDeleted: Math.max(0, Number(summary?.trainStopsDeleted) || 0),
    trainsDeleted: Math.max(0, Number(summary?.trainsDeleted) || 0),
    feedEventsDeleted: Math.max(0, Number(summary?.feedEventsDeleted) || 0),
    feedImportsDeleted: Math.max(0, Number(summary?.feedImportsDeleted) || 0),
    importChunksDeleted: Math.max(0, Number(summary?.importChunksDeleted) || 0),
  };
  tx.db.trainbot_maintenance_state.id.delete(next.id);
  return tx.db.trainbot_maintenance_state.insert(next);
}

function activeBundleMetadata(tx: any): any {
  return tx.db.trainbot_active_bundle.id.find('active') || null;
}

function requireActiveBundle(tx: any, bundleVersion: string, bundleServiceDate: string): void {
  const cleanVersion = bundleVersion.trim();
  const cleanServiceDate = bundleServiceDate.trim();
  const runtime = activeBundleMetadata(tx);
  const activeVersion = trimOptional(asString(runtime?.bundleVersion || runtime?.version)) || '';
  const activeServiceDate = trimOptional(asString(runtime?.bundleServiceDate || runtime?.serviceDate)) || '';
  if (!activeVersion || !activeServiceDate) {
    throw new SenderError('active bundle unavailable');
  }
  if (!cleanVersion || !cleanServiceDate) {
    throw new SenderError('bundle identity required');
  }
  if (cleanVersion !== activeVersion || cleanServiceDate !== activeServiceDate) {
    throw new SenderError('stale bundle identity');
  }
}

function nextRuntimeRefreshAtISO(base: Date): string {
  const next = new Date(base.getTime());
  next.setUTCMinutes(0, 0, 0);
  next.setUTCHours(next.getUTCHours() + 1);
  return next.toISOString();
}

function ensureRuntimeRefreshJob(tx: any): void {
  upsertJob(tx, 'runtime-refresh', nextRuntimeRefreshAtISO(nowDate(tx)), 'runtime_refresh', 'runtime', '', {});
}

function lookupActiveCheckinUserIds(tx: any, trainId: string, nowAt: Date): string[] {
  const userIds = new Set<string>();
  for (const ride of rowsFrom(tx.db.trainbot_active_checkin.trainInstanceId.filter(trainId))) {
    const autoCheckoutAt = parseISO(asString(ride.autoCheckoutAt));
    if (autoCheckoutAt && autoCheckoutAt.getTime() >= nowAt.getTime()) {
      const userId = asString(tx.db.trainbot_rider_identity.stableId.find(asString(ride.stableId).trim())?.telegramUserId).trim()
        || telegramUserIdForStableId(asString(ride.stableId).trim());
      if (userId) {
        userIds.add(userId);
      }
    }
  }
  return Array.from(userIds.values()).sort();
}

function lookupActiveSubscriptionUserIds(tx: any, trainId: string, nowAt: Date): string[] {
  const userIds = new Set<string>();
  for (const item of rowsFrom(tx.db.trainbot_train_subscription.trainInstanceId.filter(trainId))) {
    if (item.isActive === false) {
      continue;
    }
    const expiresAt = parseISO(asString(item.expiresAt));
    if (expiresAt && expiresAt.getTime() >= nowAt.getTime()) {
      const userId = asString(tx.db.trainbot_rider_identity.stableId.find(asString(item.stableId).trim())?.telegramUserId).trim()
        || telegramUserIdForStableId(asString(item.stableId).trim());
      if (userId) {
        userIds.add(userId);
      }
    }
  }
  return Array.from(userIds.values()).sort();
}

function serviceGetSchedulePayload(tx: any, serviceDate: string) {
  const cleanDate = serviceDate.trim() || activeServiceDate(tx);
  const day = serviceDayRow(tx, cleanDate);
  return {
    serviceDay: day || null,
    trips: listTripsForServiceDate(tx, cleanDate),
  };
}

function listActivitiesFiltered(tx: any, sinceIso: string, scopeType: string, subjectId: string, serviceDate: string): any[] {
  const sinceMs = parseISO(sinceIso)?.getTime() || 0;
  const cleanScopeType = scopeType.trim();
  const cleanSubjectId = subjectId.trim();
  const cleanServiceDate = serviceDate.trim();
  return rowsFrom(tx.db.trainbot_activity.iter()).filter((item) => {
    if (sinceMs > 0 && (parseISO(item.lastActivityAt)?.getTime() || 0) < sinceMs) {
      return false;
    }
    if (cleanScopeType && item.scopeType !== cleanScopeType) {
      return false;
    }
    if (cleanSubjectId && item.subjectId !== cleanSubjectId) {
      return false;
    }
    if (cleanServiceDate && item.serviceDate !== cleanServiceDate) {
      return false;
    }
    return true;
  }).sort((left, right) => compareTimeDescending(left.lastActivityAt, right.lastActivityAt));
}

function clearRowsByServiceDate(tableView: any, serviceDate: string): void {
  for (const row of rowsFrom(tableView.serviceDate.filter(serviceDate))) {
    tableView.delete(row);
  }
}

function clearRowsByIncident(tableView: any, incidentId: string): void {
  for (const row of rowsFrom(tableView.incidentId.filter(incidentId))) {
    tableView.delete(row);
  }
}

function clearJobsByServiceDate(tx: any, serviceDate: string): void {
  for (const row of rowsFrom(tx.db.trainbot_job.serviceDate.filter(serviceDate))) {
    tx.db.trainbot_job.scheduled_id.delete(row.scheduled_id);
  }
}

function clearScheduleProjectionRows(tx: any, serviceDate: string): void {
  clearRowsByServiceDate(tx.db.trainbot_service_station, serviceDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_stop, serviceDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_live, serviceDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_timeline_bucket, serviceDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_public, serviceDate);
}

function deleteActivity(tx: any, incidentId: string): boolean {
  const existing = tx.db.trainbot_activity.id.find(incidentId);
  deleteJobsWithPrefix(tx, `activity:${incidentId}|`);
  clearRowsByIncident(tx.db.trainbot_public_incident_event, incidentId);
  clearRowsByIncident(tx.db.trainbot_public_incident_comment, incidentId);
  clearRowsByIncident(tx.db.trainbot_public_sighting, incidentId);
  clearRowsByIncident(tx.db.trainbot_incident_vote, incidentId);
  tx.db.trainbot_public_incident.id.delete(incidentId);
  tx.db.trainbot_activity.id.delete(incidentId);
  if (existing && asString(existing.scopeType).trim() === 'train') {
    refreshTripProjection(tx, asString(existing.subjectId).trim());
  }
  return Boolean(existing);
}

function deleteServiceDayData(tx: any, serviceDate: string): any {
  const cleanDate = asString(serviceDate).trim();
  let tripsDeleted = 0;
  let stopsDeleted = 0;

  for (const trip of rowsFrom(tx.db.trainbot_trip.serviceDate.filter(cleanDate))) {
    tripsDeleted += 1;
    stopsDeleted += Array.isArray(trip.stops) ? trip.stops.length : 0;
    tx.db.trainbot_trip.id.delete(trip.id);
  }

  const existed = tx.db.trainbot_service_day.serviceDate.find(cleanDate);
  tx.db.trainbot_service_day.serviceDate.delete(cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_station, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_service_station, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_stop, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_live, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_timeline_bucket, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_trip_public, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_public_sighting, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_public_incident_event, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_public_incident_comment, cleanDate);
  clearRowsByServiceDate(tx.db.trainbot_public_incident, cleanDate);
  for (const activity of rowsFrom(tx.db.trainbot_activity.serviceDate.filter(cleanDate))) {
    deleteActivity(tx, asString(activity.id).trim());
  }
  clearJobsByServiceDate(tx, cleanDate);
  const rawCleanup = clearImportArtifactsByServiceDate(tx, cleanDate);

  return {
    serviceDate: cleanDate,
    tripsDeleted,
    stopsDeleted,
    serviceDayDeleted: existed ? 1 : 0,
    feedEventsDeleted: rawCleanup.feedEventsDeleted,
    feedImportsDeleted: rawCleanup.feedImportsDeleted,
    importChunksDeleted: rawCleanup.importChunksDeleted,
  };
}

function addServiceDates(rows: any[], out: Set<string>): void {
  for (const row of rows) {
    const serviceDate = asString(row?.serviceDate).trim();
    if (serviceDate) {
      out.add(serviceDate);
    }
  }
}

function staleServiceDates(tx: any, oldestKeptServiceDate: string): string[] {
  const dates = new Set<string>();
  addServiceDates(rowsFrom(tx.db.trainbot_service_day.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_trip.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_station.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_service_station.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_trip_stop.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_trip_live.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_trip_timeline_bucket.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_trip_public.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_public_sighting.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_public_incident_event.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_public_incident_comment.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_public_incident.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_activity.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_job.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_import_chunk.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_feed_import.iter()), dates);
  addServiceDates(rowsFrom(tx.db.trainbot_feed_event.iter()), dates);

  return Array.from(dates.values())
    .filter((serviceDate) => serviceDate && serviceDate < oldestKeptServiceDate)
    .sort();
}

function pruneActivityForRetention(tx: any, activity: any, retentionCutoffMs: number): any {
  const timeline = Array.isArray(activity?.timeline) ? activity.timeline : [];
  const keptTimeline: any[] = [];
  let reportsDeleted = 0;
  let stationSightingsDeleted = 0;

  for (const item of timeline) {
    const createdMs = parseISO(asString(item?.createdAt).trim())?.getTime() || 0;
    if (createdMs > 0 && createdMs < retentionCutoffMs) {
      if (item.kind === 'station_sighting') {
        stationSightingsDeleted += 1;
      } else if (item.kind === 'report') {
        reportsDeleted += 1;
      }
      continue;
    }
    keptTimeline.push(item);
  }

  if (reportsDeleted === 0 && stationSightingsDeleted === 0) {
    return {
      reportsDeleted,
      stationSightingsDeleted,
    };
  }

  if (keptTimeline.length === 0) {
    deleteActivity(tx, asString(activity.id).trim());
    return {
      reportsDeleted,
      stationSightingsDeleted,
    };
  }

  const next = putActivityRow(tx, {
    ...activity,
    timeline: keptTimeline,
  });
  refreshActivityProjection(tx, next.id);
  scheduleActivityRefreshJobs(tx, next);
  return {
    reportsDeleted,
    stationSightingsDeleted,
  };
}

function refreshScheduleProjection(tx: any, serviceDate: string): void {
  clearRowsByServiceDate(tx.db.trainbot_trip_stop, serviceDate);
  clearRowsByServiceDate(tx.db.trainbot_service_station, serviceDate);

  const day = serviceDayRow(tx, serviceDate);
  for (const station of Array.isArray(day?.stations) ? day.stations : []) {
    tx.db.trainbot_service_station.insert({
      id: `${serviceDate}|${asString(station.id).trim()}`,
      stationId: asString(station.id).trim(),
      serviceDate,
      name: asString(station.name).trim(),
      normalizedKey: trimOptional(asString(station.normalizedKey)) || normalizeStationKey(asString(station.name)),
      latitude: typeof station.latitude === 'number' ? station.latitude : undefined,
      longitude: typeof station.longitude === 'number' ? station.longitude : undefined,
    });
  }

  for (const trip of listTripsForServiceDate(tx, serviceDate)) {
    for (const stop of trainStopsSorted(tx, trip.id)) {
      tx.db.trainbot_trip_stop.insert({
        id: `${trip.id}|${Number(stop.seq)}`,
        trainId: trip.id,
        serviceDate,
        stationId: asString(stop.stationId).trim(),
        stationName: asString(stop.stationName).trim(),
        seq: Number(stop.seq) || 0,
        arrivalAt: trimOptional(asString(stop.arrivalAt)),
        departureAt: trimOptional(asString(stop.departureAt)),
        latitude: typeof stop.latitude === 'number' ? stop.latitude : undefined,
        longitude: typeof stop.longitude === 'number' ? stop.longitude : undefined,
      });
    }
  }
}

function refreshTripProjection(tx: any, trainId: string): void {
  tx.db.trainbot_trip_live.trainId.delete(trainId);
  tx.db.trainbot_trip_public.id.delete(trainId);
  for (const row of rowsFrom(tx.db.trainbot_trip_timeline_bucket.trainId.filter(trainId))) {
    tx.db.trainbot_trip_timeline_bucket.id.delete(row.id);
  }
  const trip = trainById(tx, trainId);
  if (!trip) {
    return;
  }
  const status = buildTrainState(tx, trainId);
  tx.db.trainbot_trip_live.insert({
    trainId,
    serviceDate: trip.serviceDate,
    state: asString(status.state).trim(),
    confidence: asString(status.confidence).trim(),
    uniqueReporters: Number(status.uniqueReporters) || 0,
    riders: activeRidersForTrain(tx, trainId),
    lastReportAt: trimOptional(asString(status.lastReportAt)) || '',
    updatedAt: nowISO(tx),
  });

  const buckets = recentTimeline(tx, trainId, 0);
  for (const bucket of buckets) {
    tx.db.trainbot_trip_timeline_bucket.insert({
      id: `${trainId}|${bucket.at}|${bucket.signal}`,
      trainId,
      serviceDate: trip.serviceDate,
      at: bucket.at,
      signal: bucket.signal,
      count: Number(bucket.count) || 0,
    });
  }
  tx.db.trainbot_trip_public.insert({
    id: trip.id,
    serviceDate: trip.serviceDate,
    fromStationId: trip.fromStationId,
    fromStationName: trip.fromStationName,
    toStationId: trip.toStationId,
    toStationName: trip.toStationName,
    departureAt: trip.departureAt,
    arrivalAt: trip.arrivalAt,
    sourceVersion: trip.sourceVersion,
    state: asString(status.state).trim(),
    confidence: asString(status.confidence).trim(),
    uniqueReporters: Number(status.uniqueReporters) || 0,
    riders: activeRidersForTrain(tx, trainId),
    lastReportAt: trimOptional(asString(status.lastReportAt)) || '',
    updatedAt: nowISO(tx),
    recentTimeline: buckets.map((bucket) => ({
      at: asString(bucket.at).trim(),
      signal: asString(bucket.signal).trim(),
      count: Number(bucket.count) || 0,
    })),
  });
}

function refreshActivityProjection(tx: any, incidentId: string): void {
  clearRowsByIncident(tx.db.trainbot_public_incident_event, incidentId);
  clearRowsByIncident(tx.db.trainbot_public_incident_comment, incidentId);
  clearRowsByIncident(tx.db.trainbot_public_sighting, incidentId);
  tx.db.trainbot_public_incident.id.delete(incidentId);

  const activity = tx.db.trainbot_activity.id.find(incidentId);
  if (!activity) {
    return;
  }
  syncIncidentVoteProjection(tx, activity);

  const summary = incidentSummaryPayload(tx, activity, '');
  tx.db.trainbot_public_incident.insert({
    id: incidentId,
    scopeType: activity.scopeType,
    subjectId: activity.subjectId,
    subjectName: activity.subjectName,
    serviceDate: activity.serviceDate,
    active: activity.active === true,
    lastReportName: summary.lastReportName,
    lastReportAt: summary.lastReportAt,
    lastActivityName: summary.lastActivityName,
    lastActivityAt: summary.lastActivityAt,
    lastActivityActor: summary.lastActivityActor,
    lastReporter: summary.lastReporter,
    commentCount: Number(summary.commentCount) || 0,
    ongoingVotes: Number(summary.votes?.ongoing) || 0,
    clearedVotes: Number(summary.votes?.cleared) || 0,
  });

  for (const event of activity.timeline || []) {
    tx.db.trainbot_public_incident_event.insert({
      id: event.id,
      incidentId,
      serviceDate: activity.serviceDate,
      kind: event.kind === 'station_sighting' ? 'report' : event.kind,
      name: event.name,
      detail: event.detail,
      nickname: event.nickname,
      createdAt: event.createdAt,
      signal: event.signal,
    });
    if (event.kind === 'station_sighting') {
      const createdMs = parseISO(event.createdAt)?.getTime() || 0;
      tx.db.trainbot_public_sighting.insert({
        id: event.id,
        incidentId,
        serviceDate: activity.serviceDate,
        stationId: event.stationId,
        stationName: event.stationName,
        destinationStationId: event.destinationStationId,
        destinationStationName: event.destinationStationName,
        matchedTrainInstanceId: event.matchedTrainInstanceId,
        createdAt: event.createdAt,
        isRecent: createdMs >= nowDate(tx).getTime() - 30 * 60 * 1000,
      });
    }
  }

  for (const comment of activity.comments || []) {
    tx.db.trainbot_public_incident_comment.insert({
      id: comment.id,
      incidentId,
      serviceDate: activity.serviceDate,
      nickname: comment.nickname,
      body: comment.body,
      createdAt: comment.createdAt,
    });
  }

  for (const vote of activity.votes || []) {
    if (asString(vote.value).trim().toUpperCase() !== 'ONGOING') {
      continue;
    }
    tx.db.trainbot_public_incident_event.insert({
      id: `${incidentId}|vote|${vote.stableId}`,
      incidentId,
      serviceDate: activity.serviceDate,
      kind: 'vote',
      name: incidentVoteEventLabel(vote.value),
      detail: '',
      nickname: vote.nickname,
      createdAt: vote.updatedAt,
      signal: '',
    });
  }

  if (activity.scopeType === 'train') {
    refreshTripProjection(tx, activity.subjectId);
  }
}

function refreshAllPublicProjections(tx: any, serviceDate?: string): void {
  const targetServiceDate = trimOptional(serviceDate || '');
  writeRuntimeState(tx);
  ensureRuntimeRefreshJob(tx);
  if (targetServiceDate) {
    refreshScheduleProjection(tx, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_public_incident_event, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_public_incident_comment, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_public_sighting, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_public_incident, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_trip_live, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_trip_timeline_bucket, targetServiceDate);
    clearRowsByServiceDate(tx.db.trainbot_trip_public, targetServiceDate);
  }
  for (const activity of rowsFrom(tx.db.trainbot_activity.iter())) {
    if (targetServiceDate && asString(activity.serviceDate) !== targetServiceDate) {
      continue;
    }
    refreshActivityProjection(tx, activity.id);
  }
  const trips = targetServiceDate ? listTripsForServiceDate(tx, targetServiceDate) : rowsFrom(tx.db.trainbot_trip.iter());
  for (const trip of trips) {
    refreshTripProjection(tx, trip.id);
  }
}

function scheduleActivityRefreshJobs(tx: any, activity: any): void {
  const incidentId = asString(activity?.id).trim();
  if (!incidentId) {
    return;
  }
  deleteJobsWithPrefix(tx, `activity:${incidentId}|`);
  const lastReportAt = asString(activity?.summary?.lastReportAt).trim();
  if (!lastReportAt) {
    return;
  }
  const base = parseISO(lastReportAt);
  if (!base) {
    return;
  }
  if (activity.scopeType === 'train') {
    upsertJob(
      tx,
      `activity:${incidentId}|10m`,
      new Date(base.getTime() + 10 * 60 * 1000).toISOString(),
      'refresh_activity_projection',
      incidentId,
      asString(activity.serviceDate).trim(),
      { incidentId }
    );
    upsertJob(
      tx,
      `activity:${incidentId}|15m`,
      new Date(base.getTime() + 15 * 60 * 1000).toISOString(),
      'refresh_activity_projection',
      incidentId,
      asString(activity.serviceDate).trim(),
      { incidentId }
    );
  } else if (activity.scopeType === 'station') {
    upsertJob(
      tx,
      `activity:${incidentId}|30m`,
      new Date(base.getTime() + 30 * 60 * 1000).toISOString(),
      'refresh_activity_projection',
      incidentId,
      asString(activity.serviceDate).trim(),
      { incidentId }
    );
  }
}

const myProfileView = t.object('TrainbotMyProfileView', {
  stableId: t.string(),
  nickname: t.string(),
  alertsEnabled: t.bool(),
  alertStyle: t.string(),
  language: t.string(),
  updatedAt: t.string(),
});

const myRideView = t.object('TrainbotMyRideView', {
  trainInstanceId: t.string(),
  boardingStationId: t.string(),
  checkedInAt: t.string(),
  autoCheckoutAt: t.string(),
  undoTrainInstanceId: t.string(),
  undoBoardingStationId: t.string(),
  undoCheckedInAt: t.string(),
  undoAutoCheckoutAt: t.string(),
  undoExpiresAt: t.string(),
});

const myTrainPrefView = t.object('TrainbotMyTrainPrefView', {
  trainInstanceId: t.string(),
  mutedUntil: t.string(),
  subscriptionExpiresAt: t.string(),
});

const myIncidentVoteView = t.object('TrainbotMyIncidentVoteView', {
  incidentId: t.string(),
  value: t.string(),
});

const publicNetworkMapLiveRow = t.object('TrainbotPublicNetworkMapLiveRow', {
  kind: t.string(),
  id: t.string(),
  serviceDate: t.string(),
  stationId: t.string(),
  stationName: t.string(),
  normalizedKey: t.string(),
  latitude: t.option(t.number()),
  longitude: t.option(t.number()),
  incidentId: t.string(),
  destinationStationId: t.string(),
  destinationStationName: t.string(),
  matchedTrainInstanceId: t.string(),
  createdAt: t.string(),
  isRecent: t.bool(),
});

function projectedServiceDateForReadonlyDb(db: any): string {
  const runtime = db.trainbot_runtime_state?.id?.find('runtime') || firstRow(db.trainbot_runtime_state?.iter?.());
  const preferred = asString(runtime?.effectiveServiceDate).trim()
    || asString(runtime?.loadedServiceDate).trim()
    || asString(runtime?.requestedServiceDate).trim();
  if (preferred) {
    return preferred;
  }
  const days = rowsFrom(db.trainbot_service_day?.iter?.());
  days.sort((left: any, right: any) => asString(right.serviceDate).localeCompare(asString(left.serviceDate)));
  return asString(days[0]?.serviceDate).trim();
}

function riderForSenderIdentity(db: any, sender: any): any | null {
  const senderIdentity = sender && typeof sender.toHexString === 'function' ? sender.toHexString() : '';
  if (!senderIdentity) {
    return null;
  }
  const identity = rowsFrom(db.trainbot_rider_identity.senderIdentity.filter(senderIdentity))[0];
  if (identity) {
    const stableId = asString(identity.stableId).trim();
    return {
      stableId,
      nickname: asString(identity.nickname).trim(),
      settings: db.trainbot_rider_settings.stableId.find(stableId) || null,
      currentRide: db.trainbot_active_checkin.stableId.find(stableId) || null,
      undoRide: db.trainbot_undo_checkout.stableId.find(stableId) || null,
      favorites: rowsFrom(db.trainbot_favorite_route.stableId.filter(stableId)),
      mutes: rowsFrom(db.trainbot_train_mute.stableId.filter(stableId)),
      subscriptions: rowsFrom(db.trainbot_train_subscription.stableId.filter(stableId)),
      recentActionState: db.trainbot_recent_action_state.stableId.find(stableId) || null,
      updatedAt: asString(identity.updatedAt).trim(),
      lastSeenAt: asString(identity.lastSeenAt).trim(),
    };
  }
  return null;
}

export const myProfile = spacetimedb.view(
  { name: named('my_profile'), public: true },
  t.option(myProfileView),
  (ctx) => {
    const rider = riderForSenderIdentity(ctx.db, ctx.sender);
    if (!rider) {
      return null;
    }
    return {
      stableId: rider.stableId,
      nickname: rider.nickname,
      alertsEnabled: rider.settings.alertsEnabled !== false,
      alertStyle: rider.settings.alertStyle,
      language: rider.settings.language,
      updatedAt: rider.updatedAt,
    };
  }
);

export const myFavorites = spacetimedb.view(
  { name: named('my_favorites'), public: true },
  t.array(favoriteDoc),
  (ctx) => {
    const rider = riderForSenderIdentity(ctx.db, ctx.sender);
    return rider ? (rider.favorites || []).slice() : [];
  }
);

export const myCurrentRide = spacetimedb.view(
  { name: named('my_current_ride'), public: true },
  t.option(myRideView),
  (ctx) => {
    const rider = riderForSenderIdentity(ctx.db, ctx.sender);
    if (!rider) {
      return null;
    }
    return {
      trainInstanceId: rider.currentRide?.trainInstanceId || '',
      boardingStationId: rider.currentRide?.boardingStationId || '',
      checkedInAt: rider.currentRide?.checkedInAt || '',
      autoCheckoutAt: rider.currentRide?.autoCheckoutAt || '',
      undoTrainInstanceId: rider.undoRide?.trainInstanceId || '',
      undoBoardingStationId: rider.undoRide?.boardingStationId || '',
      undoCheckedInAt: rider.undoRide?.checkedInAt || '',
      undoAutoCheckoutAt: rider.undoRide?.autoCheckoutAt || '',
      undoExpiresAt: rider.undoRide?.expiresAt || '',
    };
  }
);

export const myTrainPrefs = spacetimedb.view(
  { name: named('my_train_prefs'), public: true },
  t.array(myTrainPrefView),
  (ctx) => {
    const rider = riderForSenderIdentity(ctx.db, ctx.sender);
    if (!rider) {
      return [];
    }
    const byTrain = new Map<string, { trainInstanceId: string; mutedUntil: string; subscriptionExpiresAt: string }>();
    for (const mute of rider.mutes || []) {
      const trainId = asString(mute.trainInstanceId).trim();
      if (!trainId) {
        continue;
      }
      byTrain.set(trainId, {
        trainInstanceId: trainId,
        mutedUntil: asString(mute.mutedUntil).trim(),
        subscriptionExpiresAt: byTrain.get(trainId)?.subscriptionExpiresAt || '',
      });
    }
    for (const sub of rider.subscriptions || []) {
      if (sub.isActive === false) {
        continue;
      }
      const trainId = asString(sub.trainInstanceId).trim();
      if (!trainId) {
        continue;
      }
      byTrain.set(trainId, {
        trainInstanceId: trainId,
        mutedUntil: byTrain.get(trainId)?.mutedUntil || '',
        subscriptionExpiresAt: asString(sub.expiresAt).trim(),
      });
    }
    return Array.from(byTrain.values());
  }
);

export const myIncidentVotes = spacetimedb.view(
  { name: named('my_incident_votes'), public: true },
  t.array(myIncidentVoteView),
  (ctx) => {
    const rider = riderForSenderIdentity(ctx.db, ctx.sender);
    if (!rider) {
      return [];
    }
    const stableId = asString(rider.stableId).trim();
    return rowsFrom(ctx.db.trainbot_incident_vote.stableId.filter(stableId)).map((vote: any) => ({
      incidentId: asString(vote.incidentId).trim(),
      value: asString(vote.value).trim().toUpperCase(),
    }));
  }
);

export const publicDashboardLive = spacetimedb.anonymousView(
  { name: named('public_dashboard_live'), public: true },
  t.array(trainbot_trip_public.rowType),
  (ctx) => {
    const serviceDate = projectedServiceDateForReadonlyDb(ctx.db);
    const projected = serviceDate
      ? rowsFrom(ctx.db.trainbot_trip_public.serviceDate.filter(serviceDate))
      : rowsFrom(ctx.db.trainbot_trip_public.iter());
    if (projected.length || !serviceDate) {
      return projected;
    }
    return listTripsForServiceDate(ctx, serviceDate).map((trip) => {
      const status = buildTrainState(ctx, trip.id);
      const buckets = recentTimeline(ctx, trip.id, 0);
      return {
        id: trip.id,
        serviceDate: trip.serviceDate,
        fromStationId: trip.fromStationId,
        fromStationName: trip.fromStationName,
        toStationId: trip.toStationId,
        toStationName: trip.toStationName,
        departureAt: trip.departureAt,
        arrivalAt: trip.arrivalAt,
        sourceVersion: trip.sourceVersion,
        state: asString(status.state).trim(),
        confidence: asString(status.confidence).trim(),
        uniqueReporters: Number(status.uniqueReporters) || 0,
        riders: activeRidersForTrain(ctx, trip.id),
        lastReportAt: trimOptional(asString(status.lastReportAt)) || '',
        updatedAt: nowISO(ctx),
        recentTimeline: buckets.map((bucket) => ({
          at: asString(bucket.at).trim(),
          signal: asString(bucket.signal).trim(),
          count: Number(bucket.count) || 0,
        })),
      };
    });
  }
);

export const publicIncidentListLive = spacetimedb.anonymousView(
  { name: named('public_incident_list_live'), public: true },
  t.array(trainbot_public_incident.rowType),
  (ctx) => {
    const serviceDate = projectedServiceDateForReadonlyDb(ctx.db);
    const projected = serviceDate
      ? rowsFrom(ctx.db.trainbot_public_incident.serviceDate.filter(serviceDate))
      : rowsFrom(ctx.db.trainbot_public_incident.iter());
    if (projected.length || !serviceDate) {
      return projected;
    }
    return rowsFrom(ctx.db.trainbot_activity.iter())
      .filter((item) => asString(item.serviceDate).trim() === serviceDate)
      .sort((left, right) => compareTimeDescending(left.lastActivityAt, right.lastActivityAt))
      .map((activity) => {
        const summary = incidentSummaryPayload(ctx, activity, '');
        return {
          id: asString(summary.id).trim(),
          serviceDate,
          scope: asString(summary.scope).trim(),
          subjectId: asString(summary.subjectId).trim(),
          subjectName: asString(summary.subjectName).trim(),
          lastReportName: asString(summary.lastReportName).trim(),
          lastReportAt: asString(summary.lastReportAt).trim(),
          lastActivityName: asString(summary.lastActivityName).trim(),
          lastActivityAt: asString(summary.lastActivityAt).trim(),
          lastActivityActor: asString(summary.lastActivityActor).trim(),
          lastReporter: asString(summary.lastReporter).trim(),
          commentCount: Number(summary.commentCount) || 0,
          ongoingVotes: Number(summary.votes?.ongoing) || 0,
          clearedVotes: Number(summary.votes?.cleared) || 0,
          active: summary.active === true,
        };
      });
  }
);

export const publicNetworkMapLive = spacetimedb.anonymousView(
  { name: named('public_network_map_live'), public: true },
  t.array(publicNetworkMapLiveRow),
  (ctx) => {
    const serviceDate = projectedServiceDateForReadonlyDb(ctx.db);
    const rows: any[] = [];
    const stations = serviceDate
      ? listStationsForServiceDate(ctx, serviceDate).map((station) => ({
        id: `${serviceDate}|${asString(station.id).trim()}`,
        serviceDate,
        stationId: asString(station.id).trim(),
        name: asString(station.name).trim(),
        normalizedKey: asString(station.normalizedKey).trim(),
        latitude: station.latitude,
        longitude: station.longitude,
      }))
      : rowsFrom(ctx.db.trainbot_service_station.iter());
    for (const station of stations) {
      rows.push({
        kind: 'station',
        id: asString(station.id).trim(),
        serviceDate: asString(station.serviceDate).trim(),
        stationId: asString(station.stationId).trim(),
        stationName: asString(station.name).trim(),
        normalizedKey: asString(station.normalizedKey).trim(),
        latitude: typeof station.latitude === 'number' ? station.latitude : undefined,
        longitude: typeof station.longitude === 'number' ? station.longitude : undefined,
        incidentId: '',
        destinationStationId: '',
        destinationStationName: '',
        matchedTrainInstanceId: '',
        createdAt: '',
        isRecent: false,
      });
    }
    const projectedSightings = serviceDate
      ? rowsFrom(ctx.db.trainbot_public_sighting.serviceDate.filter(serviceDate))
      : rowsFrom(ctx.db.trainbot_public_sighting.iter());
    const currentTime = nowDate(ctx).getTime();
    const sightings = projectedSightings.length || !serviceDate
      ? projectedSightings
      : stationSightingsSince(ctx, utcDayStart(nowDate(ctx)).getTime(), 500).map((item) => ({
        id: asString(item.id).trim(),
        incidentId: '',
        serviceDate,
        stationId: asString(item.stationId).trim(),
        stationName: asString(item.stationName).trim(),
        destinationStationId: asString(item.destinationStationId).trim(),
        destinationStationName: asString(item.destinationStationName).trim(),
        matchedTrainInstanceId: asString(item.matchedTrainInstanceId).trim(),
        createdAt: asString(item.createdAt).trim(),
        isRecent: (parseISO(asString(item.createdAt))?.getTime() || 0) >= currentTime - 30 * 60 * 1000,
      }));
    for (const sighting of sightings) {
      rows.push({
        kind: 'sighting',
        id: asString(sighting.id).trim(),
        serviceDate: asString(sighting.serviceDate).trim(),
        stationId: asString(sighting.stationId).trim(),
        stationName: asString(sighting.stationName).trim(),
        normalizedKey: '',
        latitude: undefined,
        longitude: undefined,
        incidentId: asString(sighting.incidentId).trim(),
        destinationStationId: asString(sighting.destinationStationId).trim(),
        destinationStationName: asString(sighting.destinationStationName).trim(),
        matchedTrainInstanceId: asString(sighting.matchedTrainInstanceId).trim(),
        createdAt: asString(sighting.createdAt).trim(),
        isRecent: sighting.isRecent === true,
      });
    }
    return rows;
  }
);

export const runTrainbotJob = spacetimedb.reducer(
  { name: named('run_trainbot_job') },
  { arg: trainbot_job.rowType },
  (ctx, { arg }) => {
    const scheduledJob = requireScheduledJobRow(ctx, arg);
    const payload = parseJSON(asString(scheduledJob.payloadJson), 'invalid job payload') as ParsedObject;
    const stableId = asString(payload.stableId).trim();
    const rider = stableId ? ctx.db.trainbot_rider.stableId.find(stableId) : null;
    switch (asString(scheduledJob.kind).trim()) {
      case 'runtime_refresh':
        writeRuntimeState(ctx);
        ensureRuntimeRefreshJob(ctx);
        break;
      case 'expire_checkin':
        if (rider && rider.currentRide) {
          const next = putRiderRow(ctx, { ...rider, currentRide: undefined, updatedAt: nowISO(ctx), lastSeenAt: nowISO(ctx) });
          scheduleRiderExpiryJobs(ctx, next);
          for (const trainId of collectRiderTrainIds({ ...rider, currentRide: undefined })) {
            refreshTripProjection(ctx, trainId);
          }
        }
        break;
      case 'expire_undo':
        if (rider && rider.undoRide) {
          const next = putRiderRow(ctx, { ...rider, undoRide: undefined, updatedAt: nowISO(ctx), lastSeenAt: nowISO(ctx) });
          scheduleRiderExpiryJobs(ctx, next);
        }
        break;
      case 'expire_subscription':
        if (rider) {
          const trainId = asString(payload.trainId).trim();
          const next = putRiderRow(ctx, {
            ...rider,
            subscriptions: (rider.subscriptions || []).filter((item: any) => asString(item.trainInstanceId).trim() !== trainId),
            updatedAt: nowISO(ctx),
            lastSeenAt: nowISO(ctx),
          });
          scheduleRiderExpiryJobs(ctx, next);
        }
        break;
      case 'expire_mute':
        if (rider) {
          const trainId = asString(payload.trainId).trim();
          const next = putRiderRow(ctx, {
            ...rider,
            mutes: (rider.mutes || []).filter((item: any) => asString(item.trainInstanceId).trim() !== trainId),
            updatedAt: nowISO(ctx),
            lastSeenAt: nowISO(ctx),
          });
          scheduleRiderExpiryJobs(ctx, next);
        }
        break;
      case 'refresh_activity_projection':
        refreshActivityProjection(ctx, asString(payload.incidentId || scheduledJob.subjectId).trim());
        break;
      default:
        break;
    }
    ctx.db.trainbot_job.scheduled_id.delete(scheduledJob.scheduled_id);
  }
);

export const bindSession = spacetimedb.reducer(
  { name: named('bind_session') },
  (ctx) => {
    const { rider } = ensureRider(ctx);
    scheduleRiderExpiryJobs(ctx, rider);
  }
);

export const bootstrapMe = spacetimedb.procedure(
  { name: named('bootstrap_me') },
  t.string(),
  (ctx) => ctx.withTx((tx) => serialize(buildBootstrapPayload(tx)))
);

export const getCurrentRide = spacetimedb.procedure(
  { name: named('get_current_ride') },
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    const { session } = loadViewer(tx);
    return serialize({ currentRide: buildCurrentRide(tx, session.stableId) });
  })
);

export const getUserSettings = spacetimedb.procedure(
  { name: named('get_user_settings') },
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    const { session, rider } = loadViewer(tx);
    return serialize(rider ? rider.settings : defaultSettings(session.language, nowISO(tx)));
  })
);

export const listFavoriteRoutes = spacetimedb.procedure(
  { name: named('list_favorite_routes') },
  t.string(),
  (ctx) => ctx.withTx((tx) => serialize(favoriteListPayload(tx)))
);

export const setAlertsEnabled = spacetimedb.reducer(
  { name: named('set_alerts_enabled') },
  { enabled: t.bool() },
  (ctx, { enabled }) => {
    const { rider } = ensureRider(ctx);
    const next = putRiderRow(ctx, {
      ...rider,
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
      settings: {
        ...rider.settings,
        alertsEnabled: enabled,
        updatedAt: nowISO(ctx),
      },
    });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const setAlertStyle = spacetimedb.reducer(
  { name: named('set_alert_style') },
  { style: t.string() },
  (ctx, { style }) => {
    const { rider } = ensureRider(ctx);
    const next = putRiderRow(ctx, {
      ...rider,
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
      settings: {
        ...rider.settings,
        alertStyle: normalizeAlertStyle(style),
        updatedAt: nowISO(ctx),
      },
    });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const setLanguage = spacetimedb.reducer(
  { name: named('set_language') },
  { language: t.string() },
  (ctx, { language }) => {
    const { rider } = ensureRider(ctx);
    const nextLanguage = normalizeLanguage(language);
    const next = putRiderRow(ctx, {
      ...rider,
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
      settings: {
        ...rider.settings,
        language: nextLanguage,
        updatedAt: nowISO(ctx),
      },
    });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const saveFavoriteRoute = spacetimedb.reducer(
  { name: named('save_favorite_route') },
  {
    fromStationId: t.string(),
    toStationId: t.string(),
    fromStationName: t.string(),
    toStationName: t.string(),
  },
  (ctx, args) => {
    const { rider } = ensureRider(ctx);
    const fromStationId = asString(args.fromStationId).trim();
    const toStationId = asString(args.toStationId).trim();
    if (!fromStationId || !toStationId) {
      throw new SenderError('fromStationId and toStationId are required');
    }
    const nextFavorites = (rider.favorites || []).filter((item: any) => !(item.fromStationId === fromStationId && item.toStationId === toStationId));
    nextFavorites.unshift({
      fromStationId,
      fromStationName: trimOptional(asString(args.fromStationName)) || stationNameFor(ctx, fromStationId),
      toStationId,
      toStationName: trimOptional(asString(args.toStationName)) || stationNameFor(ctx, toStationId),
      createdAt: nowISO(ctx),
    });
    const next = putRiderRow(ctx, { ...rider, favorites: nextFavorites, updatedAt: nowISO(ctx), lastSeenAt: nowISO(ctx) });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const deleteFavoriteRoute = spacetimedb.reducer(
  { name: named('delete_favorite_route') },
  {
    fromStationId: t.string(),
    toStationId: t.string(),
  },
  (ctx, args) => {
    const { rider } = ensureRider(ctx);
    const fromStationId = asString(args.fromStationId).trim();
    const toStationId = asString(args.toStationId).trim();
    const next = putRiderRow(ctx, {
      ...rider,
      favorites: (rider.favorites || []).filter((item: any) => !(item.fromStationId === fromStationId && item.toStationId === toStationId)),
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
    });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const checkIn = spacetimedb.reducer(
  { name: named('check_in') },
  {
    trainId: t.string(),
    boardingStationId: t.string(),
    bundleVersion: t.string(),
    bundleServiceDate: t.string(),
  },
  (ctx, args) => {
    const { rider } = ensureRider(ctx);
    const trainId = asString(args.trainId).trim();
    const boardingStationId = asString(args.boardingStationId).trim();
    if (!trainId) {
      throw new SenderError('trainId is required');
    }
    requireActiveBundle(ctx, asString(args.bundleVersion), asString(args.bundleServiceDate));
    validateCheckIn(ctx, trainId, trimOptional(boardingStationId));
    const previousTrainIds = collectRiderTrainIds(rider);
    const next = putCurrentRide(ctx, rider, trainId, boardingStationId, nextAutoCheckoutAt(ctx, trainId));
    scheduleRiderExpiryJobs(ctx, next);
    for (const id of new Set([...previousTrainIds, ...collectRiderTrainIds(next)])) {
      refreshTripProjection(ctx, id);
    }
  }
);

export const checkInMap = spacetimedb.reducer(
  { name: named('check_in_map') },
  {
    trainId: t.string(),
    boardingStationId: t.string(),
    bundleVersion: t.string(),
    bundleServiceDate: t.string(),
  },
  (ctx, args) => {
    const { rider } = ensureRider(ctx);
    const trainId = asString(args.trainId).trim();
    const boardingStationId = asString(args.boardingStationId).trim();
    if (!trainId) {
      throw new SenderError('trainId is required');
    }
    requireActiveBundle(ctx, asString(args.bundleVersion), asString(args.bundleServiceDate));
    validateCheckIn(ctx, trainId, trimOptional(boardingStationId));
    const previousTrainIds = collectRiderTrainIds(rider);
    const next = putCurrentRide(ctx, rider, trainId, boardingStationId, nextMapAutoCheckoutAt(ctx, trainId));
    scheduleRiderExpiryJobs(ctx, next);
    for (const id of new Set([...previousTrainIds, ...collectRiderTrainIds(next)])) {
      refreshTripProjection(ctx, id);
    }
  }
);

export const checkout = spacetimedb.reducer(
  { name: named('checkout') },
  (ctx) => {
    const { rider } = ensureRider(ctx);
    const ride = rider.currentRide;
    const next = ride
      ? {
        ...rider,
        currentRide: undefined,
        undoRide: {
          ...ride,
          expiresAt: isoPlus(nowISO(ctx), UNDO_CHECKOUT_WINDOW_MS),
        },
        updatedAt: nowISO(ctx),
        lastSeenAt: nowISO(ctx),
      }
      : rider;
    const previousTrainIds = collectRiderTrainIds(rider);
    const inserted = putRiderRow(ctx, next);
    scheduleRiderExpiryJobs(ctx, inserted);
    for (const id of new Set([...previousTrainIds, ...collectRiderTrainIds(inserted)])) {
      refreshTripProjection(ctx, id);
    }
  }
);

export const undoCheckoutAction = spacetimedb.reducer(
  { name: named('undo_checkout') },
  (ctx) => {
    const { rider } = ensureRider(ctx);
    const undoRide = rider.undoRide;
    if (!undoRide) {
      return;
    }
    const expiresAt = parseISO(undoRide.expiresAt);
    if (!expiresAt || expiresAt.getTime() < nowDate(ctx).getTime()) {
      const next = putRiderRow(ctx, { ...rider, undoRide: undefined, updatedAt: nowISO(ctx), lastSeenAt: nowISO(ctx) });
      scheduleRiderExpiryJobs(ctx, next);
      return;
    }
    const next = putRiderRow(ctx, {
      ...rider,
      currentRide: {
        trainInstanceId: undoRide.trainInstanceId,
        boardingStationId: undoRide.boardingStationId,
        checkedInAt: undoRide.checkedInAt,
        autoCheckoutAt: undoRide.autoCheckoutAt,
      },
      undoRide: undefined,
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
    });
    scheduleRiderExpiryJobs(ctx, next);
    for (const id of collectRiderTrainIds(next)) {
      refreshTripProjection(ctx, id);
    }
  }
);

export const setTrainMute = spacetimedb.reducer(
  { name: named('set_train_mute') },
  {
    trainId: t.string(),
    durationMinutes: t.u32(),
  },
  (ctx, args) => {
    const { rider } = ensureRider(ctx);
    const trainId = asString(args.trainId).trim();
    if (!trainId) {
      throw new SenderError('trainId is required');
    }
    const durationMinutes = Number(args.durationMinutes) > 0 ? Number(args.durationMinutes) : 30;
    const nextMutes = (rider.mutes || []).filter((item: any) => item.trainInstanceId !== trainId);
    nextMutes.unshift({
      trainInstanceId: trainId,
      mutedUntil: isoPlus(nowISO(ctx), durationMinutes * 60 * 1000),
      createdAt: nowISO(ctx),
    });
    const next = putRiderRow(ctx, {
      ...rider,
      mutes: nextMutes,
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
    });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const setTrainSubscription = spacetimedb.reducer(
  { name: named('set_train_subscription') },
  {
    trainId: t.string(),
    enabled: t.bool(),
    expiresAt: t.string(),
  },
  (ctx, args) => {
    const { rider } = ensureRider(ctx);
    const trainId = asString(args.trainId).trim();
    if (!trainId) {
      throw new SenderError('trainId is required');
    }
    const nextSubscriptions = (rider.subscriptions || []).filter((item: any) => item.trainInstanceId !== trainId);
    if (args.enabled) {
      nextSubscriptions.unshift({
        trainInstanceId: trainId,
        expiresAt: trimOptional(asString(args.expiresAt)) || isoPlus(nowISO(ctx), 6 * 60 * 60 * 1000),
        isActive: true,
        createdAt: nowISO(ctx),
        updatedAt: nowISO(ctx),
      });
    }
    const next = putRiderRow(ctx, {
      ...rider,
      subscriptions: nextSubscriptions,
      updatedAt: nowISO(ctx),
      lastSeenAt: nowISO(ctx),
    });
    scheduleRiderExpiryJobs(ctx, next);
  }
);

export const submitReport = spacetimedb.reducer(
  { name: named('submit_report') },
  {
    trainId: t.string(),
    signal: t.string(),
    bundleVersion: t.string(),
    bundleServiceDate: t.string(),
  },
  (ctx, args) => {
    const { session, rider } = ensureRider(ctx);
    const trainId = asString(args.trainId).trim();
    const signal = asString(args.signal).trim().toUpperCase();
    if (!trainId) {
      throw new SenderError('trainId is required');
    }
    requireActiveBundle(ctx, asString(args.bundleVersion), asString(args.bundleServiceDate));
    if (signal !== 'INSPECTION_STARTED' && signal !== 'INSPECTION_IN_MY_CAR' && signal !== 'INSPECTION_ENDED') {
      throw new SenderError('unsupported report signal');
    }
    const ride = buildCurrentRide(ctx, session.stableId);
    if (!ride || !ride.checkIn || ride.checkIn.trainInstanceId !== trainId) {
      throw new SenderError('active ride required for this departure');
    }
    const latest = latestReportFor(ctx, session.stableId, trainId);
    if (latest) {
      const deltaMs = nowDate(ctx).getTime() - (parseISO(latest.createdAt)?.getTime() || 0);
      if (latest.signal === signal && deltaMs < REPORT_DEDUPE_MS) {
        throw new SenderError('duplicate report ignored');
      }
      if (deltaMs < REPORT_COOLDOWN_MS) {
        throw new SenderError(`report cooldown active for ${Math.ceil((REPORT_COOLDOWN_MS - deltaMs) / 1000)}s`);
      }
    }
    const event = {
      id: `${session.stableId}|${ctx.newUuidV7().toString()}`,
      kind: 'report',
      stableId: session.stableId,
      nickname: rider.nickname,
      name: trainSignalIncidentLabel(signal),
      detail: '',
      createdAt: nowISO(ctx),
      signal,
      trainInstanceId: trainId,
      stationId: '',
      stationName: '',
      destinationStationId: '',
      destinationStationName: '',
      matchedTrainInstanceId: '',
    };
    const activity = ensureTrainActivity(ctx, trainId);
    const nextActivity = putActivityRow(ctx, { ...activity, timeline: [event, ...(activity.timeline || [])] });
    refreshActivityProjection(ctx, nextActivity.id);
    scheduleActivityRefreshJobs(ctx, nextActivity);
    const nextRider = putRiderRow(ctx, { ...rider, recentActionState: { updatedAt: nowISO(ctx) }, updatedAt: nowISO(ctx), lastSeenAt: nowISO(ctx) });
    scheduleRiderExpiryJobs(ctx, nextRider);
  }
);

export const submitStationSighting = spacetimedb.reducer(
  { name: named('submit_station_sighting') },
  {
    stationId: t.string(),
    destinationStationId: t.string(),
    trainId: t.string(),
    bundleVersion: t.string(),
    bundleServiceDate: t.string(),
  },
  (ctx, args) => {
    const { session, rider } = ensureRider(ctx);
    const stationId = asString(args.stationId).trim();
    const destinationStationId = asString(args.destinationStationId).trim();
    const selectedTrainId = asString(args.trainId).trim();
    if (!stationId) {
      throw new SenderError('stationId is required');
    }
    requireActiveBundle(ctx, asString(args.bundleVersion), asString(args.bundleServiceDate));
    const latest = latestStationSightingFor(ctx, session.stableId, stationId, trimOptional(destinationStationId));
    if (latest) {
      const deltaMs = nowDate(ctx).getTime() - (parseISO(latest.createdAt)?.getTime() || 0);
      if (deltaMs < STATION_SIGHTING_DEDUPE_MS) {
        throw new SenderError('duplicate station sighting ignored');
      }
      if (deltaMs < STATION_SIGHTING_COOLDOWN_MS) {
        throw new SenderError(`station sighting cooldown active for ${Math.ceil((STATION_SIGHTING_COOLDOWN_MS - deltaMs) / 1000)}s`);
      }
    }
    const matchedTrainInstanceId = selectedTrainId || maybeResolveMatchedTrain(ctx, stationId, trimOptional(destinationStationId));
    const stationName = stationNameFor(ctx, stationId);
    const destinationStationName = stationNameFor(ctx, destinationStationId);
    const event = {
      id: `${session.stableId}|${ctx.newUuidV7().toString()}`,
      kind: 'station_sighting',
      stableId: session.stableId,
      nickname: rider.nickname,
      name: destinationStationName ? `Platform sighting to ${destinationStationName}` : 'Platform sighting',
      detail: '',
      createdAt: nowISO(ctx),
      signal: '',
      trainInstanceId: '',
      stationId,
      stationName,
      destinationStationId,
      destinationStationName,
      matchedTrainInstanceId,
    };
    const serviceDate = activeServiceDate(ctx) || formatServiceDateFor(nowDate(ctx));
    const activity = ensureStationActivity(ctx, stationId, stationName, serviceDate);
    const nextActivity = putActivityRow(ctx, { ...activity, timeline: [event, ...(activity.timeline || [])] });
    refreshActivityProjection(ctx, nextActivity.id);
    scheduleActivityRefreshJobs(ctx, nextActivity);
    const nextRider = putRiderRow(ctx, { ...rider, recentActionState: { updatedAt: nowISO(ctx) }, updatedAt: nowISO(ctx), lastSeenAt: nowISO(ctx) });
    scheduleRiderExpiryJobs(ctx, nextRider);
  }
);

export const voteIncident = spacetimedb.reducer(
  { name: named('vote_incident') },
  {
    incidentId: t.string(),
    value: t.string(),
  },
  (ctx, args) => {
    const { session, rider } = ensureRider(ctx);
    const incidentId = asString(args.incidentId).trim();
    const value = asString(args.value).trim().toUpperCase();
    if (!incidentId) {
      throw new SenderError('incidentId is required');
    }
    if (value !== 'ONGOING' && value !== 'CLEARED') {
      throw new SenderError('invalid vote value');
    }
    const activity = ctx.db.trainbot_activity.id.find(incidentId);
    if (!activity) {
      throw new SenderError('not found');
    }
    const existing = (activity.votes || []).find((item: any) => item.stableId === session.stableId);
    const nextVotes = (activity.votes || []).filter((item: any) => item.stableId !== session.stableId);
    nextVotes.unshift({
      stableId: session.stableId,
      nickname: rider.nickname,
      value,
      createdAt: existing ? existing.createdAt : nowISO(ctx),
      updatedAt: nowISO(ctx),
    });
    const nextActivity = putActivityRow(ctx, { ...activity, votes: nextVotes });
    refreshActivityProjection(ctx, nextActivity.id);
    scheduleActivityRefreshJobs(ctx, nextActivity);
  }
);

export const commentIncident = spacetimedb.reducer(
  { name: named('comment_incident') },
  {
    incidentId: t.string(),
    body: t.string(),
  },
  (ctx, args) => {
    const { session, rider } = ensureRider(ctx);
    const incidentId = asString(args.incidentId).trim();
    const body = asString(args.body).trim();
    if (!incidentId) {
      throw new SenderError('incidentId is required');
    }
    if (!body) {
      throw new SenderError('comment body is required');
    }
    const activity = ctx.db.trainbot_activity.id.find(incidentId);
    if (!activity) {
      throw new SenderError('not found');
    }
    const comment = {
      id: `${session.stableId}|${ctx.newUuidV7().toString()}`,
      stableId: session.stableId,
      nickname: rider.nickname,
      body,
      createdAt: nowISO(ctx),
    };
    const nextActivity = putActivityRow(ctx, { ...activity, comments: [comment, ...(activity.comments || [])] });
    refreshActivityProjection(ctx, nextActivity.id);
    scheduleActivityRefreshJobs(ctx, nextActivity);
  }
);

export const getPublicDashboard = spacetimedb.procedure(
  { name: named('get_public_dashboard') },
  { limit: t.u32() },
  t.string(),
  (ctx, { limit }) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, {
    generatedAt: nowISO(tx),
    trains: publicDashboardPayload(tx, Number(limit)),
  })))
);

export const getPublicServiceDayTrains = spacetimedb.procedure(
  { name: named('get_public_service_day_trains') },
  t.string(),
  (ctx) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, {
    generatedAt: nowISO(tx),
    trains: publicServiceDayPayload(tx),
  })))
);

export const getPublicTrain = spacetimedb.procedure(
  { name: named('get_public_train') },
  { trainId: t.string() },
  t.string(),
  (ctx, { trainId }) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, buildPublicTrainView(tx, asString(trainId).trim()))))
);

export const getPublicTrainStops = spacetimedb.procedure(
  { name: named('get_public_train_stops') },
  { trainId: t.string() },
  t.string(),
  (ctx, { trainId }) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, trainStopPayload(tx, '', asString(trainId).trim()))))
);

export const getPublicNetworkMap = spacetimedb.procedure(
  { name: named('get_public_network_map') },
  t.string(),
  (ctx) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, networkMapPayload(tx))))
);

export const searchPublicStations = spacetimedb.procedure(
  { name: named('search_public_stations') },
  { query: t.string() },
  t.string(),
  (ctx, { query }) => ctx.withTx((tx) => {
    const normalizedQuery = normalizeStationQueryValue(asString(query));
    const items = listStationsForServiceDate(tx, activeServiceDate(tx)).filter((item) => {
      if (!normalizedQuery) {
        return true;
      }
      const key = normalizeStationQueryValue(asString(item.normalizedKey || item.name));
      const name = normalizeStationQueryValue(asString(item.name));
      return key.startsWith(normalizedQuery) || name.startsWith(normalizedQuery);
    });
    return serialize(withSchedulePayload(tx, { stations: items }));
  })
);

export const getPublicStationDepartures = spacetimedb.procedure(
  { name: named('get_public_station_departures') },
  { stationId: t.string() },
  t.string(),
  (ctx, { stationId }) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, publicStationDeparturesPayload(tx, asString(stationId).trim(), 8))))
);

export const listPublicIncidents = spacetimedb.procedure(
  { name: named('list_public_incidents') },
  { limit: t.u32() },
  t.string(),
  (ctx, { limit }) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, {
    generatedAt: nowISO(tx),
    incidents: listIncidentSummariesPayload(tx, Number(limit)),
  })))
);

export const getPublicIncidentDetail = spacetimedb.procedure(
  { name: named('get_public_incident_detail') },
  { incidentId: t.string() },
  t.string(),
  (ctx, { incidentId }) => ctx.withTx((tx) => serialize(withSchedulePayload(tx, incidentDetailPayload(tx, asString(incidentId).trim()))))
);

export const serviceGetSchedule = spacetimedb.procedure(
  { name: named('service_get_schedule') },
  { serviceDate: t.string() },
  t.string(),
  (ctx, { serviceDate }) => ctx.withTx((tx) => serialize(serviceGetSchedulePayload(tx, asString(serviceDate).trim())))
);

export const serviceListActivities = spacetimedb.procedure(
  { name: named('service_list_activities') },
  {
    sinceIso: t.string(),
    scopeType: t.string(),
    subjectId: t.string(),
    serviceDate: t.string(),
  },
  t.string(),
  (ctx, { sinceIso, scopeType, subjectId, serviceDate }) => ctx.withTx((tx) => serialize({
    activities: listActivitiesFiltered(
      tx,
      asString(sinceIso).trim(),
      asString(scopeType).trim(),
      asString(subjectId).trim(),
      asString(serviceDate).trim(),
    ),
  }))
);

export const listWindowTrains = spacetimedb.procedure(
  { name: named('list_window_trains') },
  { windowId: t.string() },
  t.string(),
  (ctx, { windowId }) => ctx.withTx((tx) => {
    const { session } = loadViewer(tx);
    const trains = trainsByWindow(tx, asString(windowId).trim()).map((train) => buildTrainCard(tx, session.stableId, train));
    return serialize(withSchedulePayload(tx, { trains }));
  })
);

export const searchStations = spacetimedb.procedure(
  { name: named('search_stations') },
  { query: t.string() },
  t.string(),
  (ctx, { query }) => ctx.withTx((tx) => {
    loadViewer(tx);
    const normalizedQuery = normalizeStationQueryValue(asString(query));
    const items = listStationsForServiceDate(tx, activeServiceDate(tx)).filter((item) => {
      if (!normalizedQuery) {
        return true;
      }
      const key = normalizeStationQueryValue(asString(item.normalizedKey || item.name));
      const name = normalizeStationQueryValue(asString(item.name));
      return key.startsWith(normalizedQuery) || name.startsWith(normalizedQuery);
    });
    return serialize(withSchedulePayload(tx, { stations: items }));
  })
);

export const getStationDepartures = spacetimedb.procedure(
  { name: named('get_station_departures') },
  { stationId: t.string() },
  t.string(),
  (ctx, { stationId }) => ctx.withTx((tx) => {
    const { session } = loadViewer(tx);
    return serialize(withSchedulePayload(tx, stationDeparturesPayload(tx, session.stableId, asString(stationId).trim())));
  })
);

export const getStationSightingDestinations = spacetimedb.procedure(
  { name: named('get_station_sighting_destinations') },
  { stationId: t.string() },
  t.string(),
  (ctx, { stationId }) => ctx.withTx((tx) => {
    loadViewer(tx);
    return serialize(withSchedulePayload(tx, { stations: terminalDestinations(tx, asString(stationId).trim()) }));
  })
);

export const searchRouteDestinations = spacetimedb.procedure(
  { name: named('search_route_destinations') },
  {
    originStationId: t.string(),
    query: t.string(),
  },
  t.string(),
  (ctx, { originStationId, query }) => ctx.withTx((tx) => {
    loadViewer(tx);
    const normalizedQuery = normalizeStationQueryValue(asString(query));
    const items = routeDestinations(tx, asString(originStationId).trim()).filter((item) => {
      if (!normalizedQuery) {
        return true;
      }
      const key = normalizeStationQueryValue(asString(item.normalizedKey || item.name));
      const name = normalizeStationQueryValue(asString(item.name));
      return key.startsWith(normalizedQuery) || name.startsWith(normalizedQuery);
    });
    return serialize(withSchedulePayload(tx, { stations: items }));
  })
);

export const listRouteTrains = spacetimedb.procedure(
  { name: named('list_route_trains') },
  {
    originStationId: t.string(),
    destinationStationId: t.string(),
  },
  t.string(),
  (ctx, { originStationId, destinationStationId }) => ctx.withTx((tx) => {
    const { session } = loadViewer(tx);
    const now = nowDate(tx);
    const items = routeWindowTrains(
      tx,
      asString(originStationId).trim(),
      asString(destinationStationId).trim(),
      now.getTime() - 30 * 60 * 1000,
      now.getTime() + 18 * 60 * 60 * 1000
    ).map((item) => ({
      trainCard: buildTrainCard(tx, session.stableId, item.train),
      fromStationId: item.fromStationId,
      fromStationName: item.fromStationName,
      toStationId: item.toStationId,
      toStationName: item.toStationName,
      fromPassAt: item.fromPassAt,
      toPassAt: item.toPassAt,
    }));
    return serialize(withSchedulePayload(tx, { trains: items }));
  })
);

export const getTrainStatus = spacetimedb.procedure(
  { name: named('get_train_status') },
  { trainId: t.string() },
  t.string(),
  (ctx, { trainId }) => ctx.withTx((tx) => {
    const { session } = loadViewer(tx);
    const payload = buildTrainStatusView(tx, session.stableId, asString(trainId).trim());
    if (!payload) {
      throw new SenderError('not found');
    }
    return serialize(withSchedulePayload(tx, payload));
  })
);

export const getTrainStops = spacetimedb.procedure(
  { name: named('get_train_stops') },
  { trainId: t.string() },
  t.string(),
  (ctx, { trainId }) => ctx.withTx((tx) => {
    const { session } = loadViewer(tx);
    return serialize(withSchedulePayload(tx, trainStopPayload(tx, session.stableId, asString(trainId).trim())));
  })
);

export const beginServiceDayImport = spacetimedb.procedure(
  { name: named('begin_service_day_import') },
  {
    importId: t.string(),
    serviceDate: t.string(),
    sourceVersion: t.string(),
  },
  t.string(),
  (ctx, args) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const importId = asString(args.importId).trim();
    const serviceDate = asString(args.serviceDate).trim();
    if (!importId || !serviceDate) {
      throw new SenderError('importId and serviceDate are required');
    }
    clearImportArtifacts(tx, importId);
    upsertFeedImport(tx, importId, serviceDate, asString(args.sourceVersion).trim());
    tx.db.trainbot_import_chunk.insert({
      id: `${importId}|header`,
      importId,
      chunkKind: 'header',
      serviceDate,
      sourceVersion: asString(args.sourceVersion).trim(),
      createdAt: nowISO(tx),
      payloadJson: '',
    });
    return serialize({ ok: true, importId, serviceDate });
  })
);

export const appendServiceDayChunk = spacetimedb.procedure(
  { name: named('append_service_day_chunk') },
  {
    importId: t.string(),
    chunkKind: t.string(),
    payloadJson: t.string(),
  },
  t.string(),
  (ctx, args) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const importId = asString(args.importId).trim();
    const chunkKind = asString(args.chunkKind).trim();
    if (chunkKind !== 'stations' && chunkKind !== 'trips' && chunkKind !== 'stops') {
      throw new SenderError('unsupported chunk kind');
    }
    const header = tx.db.trainbot_import_chunk.id.find(`${importId}|header`);
    if (!header) {
      throw new SenderError('schedule import not found');
    }
    parseBatchItems(args.payloadJson);
    tx.db.trainbot_import_chunk.insert({
      id: `${importId}|${chunkKind}|${ctx.newUuidV7().toString()}`,
      importId,
      chunkKind,
      serviceDate: header.serviceDate,
      sourceVersion: header.sourceVersion,
      createdAt: nowISO(tx),
      payloadJson: args.payloadJson,
    });
    return serialize({ ok: true, importId, chunkKind });
  })
);

export const commitServiceDayImport = spacetimedb.procedure(
  { name: named('commit_service_day_import') },
  { importId: t.string() },
  t.string(),
  (ctx, args) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const importId = asString(args.importId).trim();
    const header = tx.db.trainbot_import_chunk.id.find(`${importId}|header`);
    if (!header) {
      throw new SenderError('schedule import not found');
    }
    const chunkRows = rowsForImport(tx, importId)
      .filter((row) => row.id !== `${importId}|header`)
      .sort((left, right) => {
        const created = compareTimeAscending(asString(left.createdAt), asString(right.createdAt));
        if (created !== 0) {
          return created;
        }
        return asString(left.id).localeCompare(asString(right.id));
      });

    const projection = buildServiceDateProjectionFromChunkRows(chunkRows, header.serviceDate, header.sourceVersion);
    const stations = projection.stations;
    const trains = projection.trains;
    const stopsByTrainId = projection.stopsByTrainId;

    tx.db.trainbot_service_day.serviceDate.delete(header.serviceDate);
    tx.db.trainbot_service_day.insert({
      serviceDate: header.serviceDate,
      sourceVersion: header.sourceVersion,
      importedAt: nowISO(tx),
      stations,
    });

    for (const existingID of rowsFrom(tx.db.trainbot_trip.serviceDate.filter(header.serviceDate)).map((row) => asString(row.id).trim())) {
      tx.db.trainbot_trip.id.delete(existingID);
    }

    for (const trip of trains) {
      const stops = (stopsByTrainId.get(trip.id) || []).slice().sort((left, right) => Number(left.seq) - Number(right.seq));
      const firstStop = stops[0];
      const lastStop = stops[stops.length - 1];
      tx.db.trainbot_trip.insert({
        id: trip.id,
        serviceDate: trip.serviceDate,
        fromStationId: asString(firstStop?.stationId).trim() || normalizeStationId(trip.fromStationName),
        fromStationName: asString(firstStop?.stationName).trim() || trip.fromStationName,
        toStationId: asString(lastStop?.stationId).trim() || normalizeStationId(trip.toStationName),
        toStationName: asString(lastStop?.stationName).trim() || trip.toStationName,
        departureAt: trip.departureAt,
        arrivalAt: trip.arrivalAt,
        sourceVersion: trip.sourceVersion,
        stops,
      });
    }

    refreshAllPublicProjections(tx, header.serviceDate);
    clearImportArtifacts(tx, importId);
    return serialize({
      ok: true,
      importId,
      serviceDate: header.serviceDate,
      stations: stations.length,
      trains: trains.length,
      stops: Array.from(stopsByTrainId.values()).reduce((sum, items) => sum + items.length, 0),
    });
  })
);

export const abortServiceDayImport = spacetimedb.procedure(
  { name: named('abort_service_day_import') },
  { importId: t.string() },
  t.string(),
  (ctx, args) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const importId = asString(args.importId).trim();
    clearImportArtifacts(tx, importId);
    return serialize({ ok: true, importId });
  })
);

export const upsertRiderBatch = spacetimedb.reducer(
  { name: named('upsert_rider_batch') },
  { payloadJson: t.string() },
  (ctx, { payloadJson }) => {
    requireServiceRole(ctx);
    const items = parseBatchItems(payloadJson);
    for (const item of items) {
      const next = putRiderRow(ctx, item);
      scheduleRiderExpiryJobs(ctx, next);
      for (const trainId of collectRiderTrainIds(next)) {
        refreshTripProjection(ctx, trainId);
      }
    }
  }
);

export const upsertActivityBatch = spacetimedb.reducer(
  { name: named('upsert_activity_batch') },
  { payloadJson: t.string() },
  (ctx, { payloadJson }) => {
    requireServiceRole(ctx);
    const items = parseBatchItems(payloadJson);
    for (const item of items) {
      const next = putActivityRow(ctx, item);
      refreshActivityProjection(ctx, next.id);
      scheduleActivityRefreshJobs(ctx, next);
    }
  }
);

function cleanupExpiredRiderState(tx: any, nowAt: Date): any {
  let checkinsDeleted = 0;
  let subscriptionsDeleted = 0;
  const nowMs = nowAt.getTime();
  const currentAt = nowISO(tx);

  for (const rider of rowsFrom(tx.db.trainbot_rider.iter())) {
    let changed = false;
    const currentRide = rider.currentRide;
    const undoRide = rider.undoRide;
    const currentRideExpired = Boolean(currentRide)
      && (parseISO(asString(currentRide.autoCheckoutAt).trim())?.getTime() || 0) <= nowMs;
    const undoRideExpired = Boolean(undoRide)
      && (parseISO(asString(undoRide.expiresAt).trim())?.getTime() || 0) <= nowMs;

    let nextCurrentRide = currentRide;
    if (currentRideExpired) {
      nextCurrentRide = undefined;
      checkinsDeleted += 1;
      changed = true;
    }

    let nextUndoRide = undoRide;
    if (undoRideExpired) {
      nextUndoRide = undefined;
      changed = true;
    }

    const nextSubscriptions = [];
    for (const subscription of Array.isArray(rider.subscriptions) ? rider.subscriptions : []) {
      const expiresMs = parseISO(asString(subscription?.expiresAt).trim())?.getTime() || 0;
      const active = subscription?.isActive !== false;
      if (!active || (expiresMs > 0 && expiresMs <= nowMs)) {
        subscriptionsDeleted += 1;
        changed = true;
        continue;
      }
      nextSubscriptions.push(subscription);
    }

    const nextMutes = [];
    for (const mute of Array.isArray(rider.mutes) ? rider.mutes : []) {
      const mutedUntilMs = parseISO(asString(mute?.mutedUntil).trim())?.getTime() || 0;
      if (mutedUntilMs > 0 && mutedUntilMs <= nowMs) {
        changed = true;
        continue;
      }
      nextMutes.push(mute);
    }

    if (!changed) {
      continue;
    }

    const refreshTrainIds = new Set<string>([
      ...collectRiderTrainIds(rider),
      ...collectRiderTrainIds({
        ...rider,
        currentRide: nextCurrentRide,
        subscriptions: nextSubscriptions,
      }),
    ]);
    const next = putRiderRow(tx, {
      ...rider,
      currentRide: nextCurrentRide,
      undoRide: nextUndoRide,
      subscriptions: nextSubscriptions,
      mutes: nextMutes,
      updatedAt: currentAt,
      lastSeenAt: currentAt,
    });
    scheduleRiderExpiryJobs(tx, next);
    for (const trainId of refreshTrainIds) {
      refreshTripProjection(tx, trainId);
    }
  }

  return {
    checkinsDeleted,
    subscriptionsDeleted,
  };
}

function cleanupImportArtifacts(tx: any, retentionCutoffMs: number): any {
  let importChunksDeleted = 0;
  let feedEventsDeleted = 0;
  let feedImportsDeleted = 0;

  const importChunkIDsToDelete: string[] = [];
  for (const row of rowsFrom(tx.db.trainbot_import_chunk.iter())) {
    const hasParent = Boolean(tx.db.trainbot_feed_import.importId.find(asString(row.importId).trim()));
    const createdMs = parseISO(asString(row.createdAt).trim())?.getTime() || 0;
    if (!hasParent || (createdMs > 0 && createdMs < retentionCutoffMs)) {
      importChunkIDsToDelete.push(asString(row.id).trim());
    }
  }
  for (const id of importChunkIDsToDelete) {
    tx.db.trainbot_import_chunk.id.delete(id);
    importChunksDeleted += 1;
  }

  const importIDsToClear: string[] = [];
  for (const row of rowsFrom(tx.db.trainbot_feed_import.iter())) {
    const importId = asString(row.importId).trim();
    if (rowsForImport(tx, importId).length > 0) {
      continue;
    }
    importIDsToClear.push(importId);
  }
  for (const importId of importIDsToClear) {
    const deleted = clearImportArtifacts(tx, importId);
    importChunksDeleted += deleted.importChunksDeleted;
    feedEventsDeleted += deleted.feedEventsDeleted;
    feedImportsDeleted += deleted.feedImportsDeleted;
  }

  const feedEventIDsToDelete: string[] = [];
  for (const row of rowsFrom(tx.db.trainbot_feed_event.iter())) {
    if (tx.db.trainbot_feed_import.importId.find(asString(row.importId).trim())) {
      continue;
    }
    feedEventIDsToDelete.push(asString(row.id).trim());
  }
  for (const id of feedEventIDsToDelete) {
    tx.db.trainbot_feed_event.id.delete(id);
    feedEventsDeleted += 1;
  }

  return {
    importChunksDeleted,
    feedEventsDeleted,
    feedImportsDeleted,
  };
}

function applyStoredCleanupRetention(tx: any): any {
  const policy = cleanupRetentionPolicy(tx);
  if (!policy) {
    return {
      trainStopsDeleted: 0,
      trainsDeleted: 0,
      feedEventsDeleted: 0,
      feedImportsDeleted: 0,
      importChunksDeleted: 0,
    };
  }

  let trainStopsDeleted = 0;
  let trainsDeleted = 0;
  let feedEventsDeleted = 0;
  let feedImportsDeleted = 0;
  let importChunksDeleted = 0;

  for (const serviceDate of staleServiceDates(tx, policy.oldestKeptServiceDate)) {
    const deleted = deleteServiceDayData(tx, serviceDate);
    trainStopsDeleted += Number(deleted.stopsDeleted) || 0;
    trainsDeleted += Number(deleted.tripsDeleted) || 0;
    feedEventsDeleted += Number(deleted.feedEventsDeleted) || 0;
    feedImportsDeleted += Number(deleted.feedImportsDeleted) || 0;
    importChunksDeleted += Number(deleted.importChunksDeleted) || 0;
  }

  const rawCleanup = cleanupImportArtifacts(tx, policy.retentionCutoffAt.getTime());
  feedEventsDeleted += rawCleanup.feedEventsDeleted;
  feedImportsDeleted += rawCleanup.feedImportsDeleted;
  importChunksDeleted += rawCleanup.importChunksDeleted;

  return {
    trainStopsDeleted,
    trainsDeleted,
    feedEventsDeleted,
    feedImportsDeleted,
    importChunksDeleted,
  };
}

function cleanupOrphanIncidentVotes(tx: any): void {
  for (const vote of rowsFrom(tx.db.trainbot_incident_vote.iter())) {
    if (!tx.db.trainbot_activity.id.find(asString(vote.incidentId).trim())) {
      tx.db.trainbot_incident_vote.id.delete(vote.id);
    }
  }
}

function cleanupExpiredTestLoginTickets(tx: any, nowAt: Date): void {
  const cutoffMs = nowAt.getTime();
  for (const ticket of rowsFrom(tx.db.trainbot_test_login_ticket.iter())) {
    const expiresAt = parseISO(asString(ticket.expiresAt).trim());
    if (expiresAt && expiresAt.getTime() <= cutoffMs) {
      tx.db.trainbot_test_login_ticket.nonceHash.delete(asString(ticket.nonceHash).trim());
    }
  }
}

export const cleanupExpiredState = spacetimedb.reducer(
  { name: named('cleanup_expired_state') },
  {
    nowIso: t.string(),
    retentionCutoffIso: t.string(),
    oldestKeptServiceDate: t.string(),
  },
  (ctx, { nowIso, retentionCutoffIso, oldestKeptServiceDate }) => {
    requireServiceRole(ctx);
    const nowAt = parseISO(asString(nowIso).trim());
    const retentionCutoffAt = parseISO(asString(retentionCutoffIso).trim());
    const oldestKept = asString(oldestKeptServiceDate).trim();
    if (!nowAt || !retentionCutoffAt || !oldestKept) {
      throw new SenderError('invalid cleanup arguments');
    }
    recordCleanupRetentionPolicy(ctx, asString(nowIso).trim(), asString(retentionCutoffIso).trim(), oldestKept);

    const riderCleanup = cleanupExpiredRiderState(ctx, nowAt);
    let reportsDeleted = 0;
    let stationSightingsDeleted = 0;

    for (const activity of rowsFrom(ctx.db.trainbot_activity.iter())) {
      const pruned = pruneActivityForRetention(ctx, activity, retentionCutoffAt.getTime());
      reportsDeleted += Number(pruned.reportsDeleted) || 0;
      stationSightingsDeleted += Number(pruned.stationSightingsDeleted) || 0;
    }

    const rawCleanup = applyStoredCleanupRetention(ctx);
    cleanupExpiredTestLoginTickets(ctx, nowAt);
    const summary = {
      checkinsDeleted: riderCleanup.checkinsDeleted,
      subscriptionsDeleted: riderCleanup.subscriptionsDeleted,
      reportsDeleted,
      stationSightingsDeleted,
      trainStopsDeleted: rawCleanup.trainStopsDeleted,
      trainsDeleted: rawCleanup.trainsDeleted,
      feedEventsDeleted: rawCleanup.feedEventsDeleted,
      feedImportsDeleted: rawCleanup.feedImportsDeleted,
      importChunksDeleted: rawCleanup.importChunksDeleted,
    };
    cleanupOrphanIncidentVotes(ctx);
    writeCleanupSummary(ctx, summary);
    writeRuntimeState(ctx);
    ensureRuntimeRefreshJob(ctx);
  }
);

export const servicePutRider = spacetimedb.reducer(
  { name: named('service_put_rider') },
  { riderJson: t.string() },
  (ctx, { riderJson }) => {
    requireServiceRole(ctx);
    const rider = putRiderRow(ctx, parseJSON(riderJson, 'invalid rider JSON'));
    scheduleRiderExpiryJobs(ctx, rider);
    for (const trainId of collectRiderTrainIds(rider)) {
      refreshTripProjection(ctx, trainId);
    }
  }
);

export const servicePutActivity = spacetimedb.reducer(
  { name: named('service_put_activity') },
  { activityJson: t.string() },
  (ctx, { activityJson }) => {
    requireServiceRole(ctx);
    const activity = putActivityRow(ctx, parseJSON(activityJson, 'invalid activity JSON'));
    refreshActivityProjection(ctx, activity.id);
    scheduleActivityRefreshJobs(ctx, activity);
  }
);

export const serviceResetTestRider = spacetimedb.reducer(
  { name: named('service_reset_test_rider') },
  { stableId: t.string() },
  (ctx, { stableId }) => {
    requireServiceRole(ctx);
    resetTestRider(ctx, asString(stableId).trim());
  }
);

export const serviceConsumeTestLoginTicket = spacetimedb.reducer(
  { name: named('service_consume_test_login_ticket') },
  {
    nonceHash: t.string(),
    stableId: t.string(),
    expiresAt: t.string(),
  },
  (ctx, { nonceHash, stableId, expiresAt }) => {
    requireServiceRole(ctx);
    const cleanNonceHash = asString(nonceHash).trim();
    const cleanStableId = asString(stableId).trim();
    const cleanExpiresAt = asString(expiresAt).trim();
    const parsedExpiresAt = parseISO(cleanExpiresAt);
    if (!cleanNonceHash || !cleanStableId || !parsedExpiresAt) {
      throw new SenderError('invalid test login ticket consume arguments');
    }
    cleanupExpiredTestLoginTickets(ctx, nowDate(ctx));
    if (ctx.db.trainbot_test_login_ticket.nonceHash.find(cleanNonceHash)) {
      throw new SenderError('test login ticket already used');
    }
    ctx.db.trainbot_test_login_ticket.insert({
      nonceHash: cleanNonceHash,
      stableId: cleanStableId,
      expiresAt: parsedExpiresAt.toISOString(),
      consumedAt: nowISO(ctx),
    });
  }
);

export const serviceSetRuntimeConfig = spacetimedb.reducer(
  { name: named('service_set_runtime_config') },
  { scheduleCutoffHour: t.u32() },
  (ctx, { scheduleCutoffHour }) => {
    requireServiceRole(ctx);
    const rawCutoffHour = Number(scheduleCutoffHour);
    const nextCutoffHour = Number.isFinite(rawCutoffHour) && rawCutoffHour >= 0 && rawCutoffHour <= 23
      ? Math.floor(rawCutoffHour)
      : DEFAULT_SCHEDULE_CUTOFF_HOUR;
    ctx.db.trainbot_runtime_config.id.delete('runtime');
    ctx.db.trainbot_runtime_config.insert({
      id: 'runtime',
      scheduleCutoffHour: nextCutoffHour,
      updatedAt: nowISO(ctx),
    });
    writeRuntimeState(ctx);
    ensureRuntimeRefreshJob(ctx);
  }
);

export const serviceSetActiveBundle = spacetimedb.reducer(
  { name: named('service_set_active_bundle') },
  {
    version: t.string(),
    serviceDate: t.string(),
    generatedAt: t.string(),
    sourceVersion: t.string(),
  },
  (ctx, { version, serviceDate, generatedAt, sourceVersion }) => {
    requireServiceRole(ctx);
    const next = {
      id: 'active',
      version: asString(version).trim(),
      serviceDate: asString(serviceDate).trim(),
      generatedAt: asString(generatedAt).trim(),
      sourceVersion: asString(sourceVersion).trim(),
      updatedAt: new Date().toISOString(),
    };
    ctx.db.trainbot_active_bundle.id.delete(next.id);
    ctx.db.trainbot_active_bundle.insert(next);
  }
);

export const serviceReplaceScheduleBatch = spacetimedb.reducer(
  { name: named('service_replace_schedule_batch') },
  {
    serviceDate: t.string(),
    sourceVersion: t.string(),
    stationsJson: t.string(),
    tripsJson: t.string(),
    reset: t.bool(),
    finalize: t.bool(),
  },
  (ctx, { serviceDate, sourceVersion, stationsJson, tripsJson, reset, finalize }) => {
    requireServiceRole(ctx);
    const cleanDate = asString(serviceDate).trim();
    const cleanSourceVersion = asString(sourceVersion).trim();
    if (!cleanDate) {
      throw new SenderError('serviceDate is required');
    }

    const stationItems = parseBatchItems(stationsJson);
    const stations = stationItems
      .map((item) => sanitizeStationDoc(item))
      .filter((item) => item.id);

    if (reset) {
      ctx.db.trainbot_service_day.serviceDate.delete(cleanDate);
      for (const existingID of rowsFrom(ctx.db.trainbot_trip.serviceDate.filter(cleanDate)).map((row) => asString(row.id).trim())) {
        ctx.db.trainbot_trip.id.delete(existingID);
      }
      clearScheduleProjectionRows(ctx, cleanDate);
    }

    const existingServiceDay = ctx.db.trainbot_service_day.serviceDate.find(cleanDate);
    if (reset || stations.length > 0 || !existingServiceDay) {
      const nextStations = stations.length
        ? stations
        : Array.isArray(existingServiceDay?.stations)
          ? existingServiceDay.stations
          : [];
      ctx.db.trainbot_service_day.serviceDate.delete(cleanDate);
      ctx.db.trainbot_service_day.insert({
        serviceDate: cleanDate,
        sourceVersion: cleanSourceVersion || asString(existingServiceDay?.sourceVersion).trim(),
        importedAt: nowISO(ctx),
        stations: nextStations,
      });
    }

    const trips = parseBatchItems(tripsJson);
    for (const item of trips) {
      const train = sanitizeTrainDoc(item, cleanDate, cleanSourceVersion);
      const stops = Array.isArray(item.stops)
        ? item.stops
          .map((stop) => sanitizeStopDoc(stop))
          .filter((stop) => stop.stationName)
          .sort((left, right) => Number(left.seq) - Number(right.seq))
        : [];
      const firstStop = stops[0];
      const lastStop = stops[stops.length - 1];
      ctx.db.trainbot_trip.id.delete(train.id);
      ctx.db.trainbot_trip.insert({
        id: train.id,
        serviceDate: train.serviceDate,
        fromStationId: asString(firstStop?.stationId).trim() || normalizeStationId(train.fromStationName),
        fromStationName: asString(firstStop?.stationName).trim() || train.fromStationName,
        toStationId: asString(lastStop?.stationId).trim() || normalizeStationId(train.toStationName),
        toStationName: asString(lastStop?.stationName).trim() || train.toStationName,
        departureAt: train.departureAt,
        arrivalAt: train.arrivalAt,
        sourceVersion: train.sourceVersion,
        stops,
      });
    }

    if (finalize) {
      refreshAllPublicProjections(ctx, cleanDate);
    }
  }
);

export const serviceDeleteServiceDay = spacetimedb.reducer(
  { name: named('service_delete_service_day') },
  { serviceDate: t.string() },
  (ctx, { serviceDate }) => {
    requireServiceRole(ctx);
    deleteServiceDayData(ctx, asString(serviceDate).trim());
    cleanupOrphanIncidentVotes(ctx);
    writeRuntimeState(ctx);
    ensureRuntimeRefreshJob(ctx);
  }
);
