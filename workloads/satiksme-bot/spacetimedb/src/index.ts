// @ts-nocheck
import {
  CaseConversionPolicy,
  SenderError,
  schema,
  table,
  t,
} from 'spacetimedb/server';

const SATIKSMEBOT_DB_PREFIX = 'satiksmebot_';
const SATIKSMEBOT_SCHEMA_MODULE = 'satiksme-bot';
const SATIKSMEBOT_SCHEMA_VERSION = '2026-05-11-public-direct-privacy-v2';
const VISIBLE_SIGHTING_WINDOW_MS = 30 * 60 * 1000;
const INCIDENT_LOOKBACK_MS = 24 * 60 * 60 * 1000;
const INCIDENT_RESOLVED_VOTE_COUNT = 2;
const REPORT_DEDUPE_WINDOW_MS = 90 * 1000;
const SAME_VOTE_WINDOW_MS = 30 * 60 * 1000;
const MAP_REPORT_WINDOW_MS = 30 * 60 * 1000;
const MAP_REPORT_LIMIT = 5;
const VOTE_ACTION_WINDOW_MS = 60 * 60 * 1000;
const VOTE_ACTION_LIMIT = 20;
const COMMENT_ACTION_WINDOW_MS = 60 * 60 * 1000;
const COMMENT_ACTION_LIMIT = 10;
const INCIDENT_COMMENT_ACTION_LIMIT = 50;
const PUBLIC_INCIDENT_ACTOR_LABEL = 'anonīmi';
const DEFAULT_AREA_RADIUS_METERS = 100;
const MAX_AREA_RADIUS_METERS = 500;

function named(suffix: string): string {
  return `${SATIKSMEBOT_DB_PREFIX}${suffix}`;
}

function publicIncidentNickname(): string {
  return PUBLIC_INCIDENT_ACTOR_LABEL;
}

const vehicleContextDoc = t.object('SatiksmeVehicleContextDoc', {
  // Retained for non-destructive upgrades from the v1 public schema. Public
  // projections always write an empty value.
  scopeKey: t.string(),
  stopId: t.string(),
  stopName: t.string(),
  mode: t.string(),
  routeLabel: t.string(),
  direction: t.string(),
  destination: t.string(),
  departureSeconds: t.u32(),
  // Retained for v1 schema compatibility; never populated publicly.
  liveRowId: t.string(),
});

const areaContextDoc = t.object('SatiksmeAreaContextDoc', {
  // Retained for v1 schema compatibility; never populated publicly.
  scopeKey: t.string(),
  latitude: t.number(),
  longitude: t.number(),
  radiusMeters: t.u32(),
  description: t.string(),
});

const satiksmebot_active_bundle = table(
  { name: named('active_bundle'), public: true },
  {
    id: t.string().primaryKey(),
    version: t.string(),
    generatedAt: t.string(),
    stopCount: t.u32(),
    routeCount: t.u32(),
    updatedAt: t.string(),
  }
);

const satiksmebot_stop_catalog = table(
  { name: named('stop_catalog'), public: true },
  {
    id: t.string().primaryKey(),
    // Retained for v1 schema compatibility; always empty in public rows.
    liveId: t.string(),
    name: t.string(),
    latitude: t.number(),
    longitude: t.number(),
    modes: t.array(t.string()),
    routeLabels: t.array(t.string()),
    // Retained for v1 schema compatibility; always empty in public rows.
    nearbyStopIds: t.array(t.string()),
  }
);

const satiksmebot_route_catalog = table(
  { name: named('route_catalog'), public: true },
  {
    id: t.string().primaryKey(),
    label: t.string().index(),
    mode: t.string().index(),
    name: t.string(),
    stopIds: t.array(t.string()),
  }
);

const satiksmebot_import_chunk = table(
  { name: named('import_chunk') },
  {
    id: t.string().primaryKey(),
    importId: t.string().index(),
    chunkKind: t.string().index(),
    version: t.string(),
    generatedAt: t.string(),
    createdAt: t.string().index(),
    payloadJson: t.string(),
  }
);

const satiksmebot_reporter_identity = table(
  { name: named('reporter_identity') },
  {
    stableId: t.string().primaryKey(),
    userId: t.string().index(),
    nickname: t.string(),
    language: t.string(),
    createdAt: t.string(),
    updatedAt: t.string(),
    lastSeenAt: t.string(),
  }
);

const satiksmebot_stop_sighting = table(
  { name: named('stop_sighting') },
  {
    id: t.string().primaryKey(),
    stopId: t.string().index(),
    stableId: t.string().index(),
    userId: t.string().index(),
    hidden: t.bool(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_vehicle_sighting = table(
  { name: named('vehicle_sighting') },
  {
    id: t.string().primaryKey(),
    stopId: t.string().index(),
    stableId: t.string().index(),
    userId: t.string().index(),
    mode: t.string().index(),
    routeLabel: t.string().index(),
    direction: t.string(),
    destination: t.string(),
    departureSeconds: t.u32(),
    liveRowId: t.string(),
    scopeKey: t.string().index(),
    hidden: t.bool(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_area_report = table(
  { name: named('area_report') },
  {
    id: t.string().primaryKey(),
    stableId: t.string().index(),
    userId: t.string().index(),
    latitude: t.number(),
    longitude: t.number(),
    radiusMeters: t.u32(),
    description: t.string(),
    scopeKey: t.string().index(),
    hidden: t.bool(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_incident_vote = table(
  { name: named('incident_vote') },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    stableId: t.string().index(),
    userId: t.string().index(),
    nickname: t.string(),
    value: t.string(),
    createdAt: t.string(),
    updatedAt: t.string().index(),
  }
);

const satiksmebot_incident_vote_event = table(
  { name: named('incident_vote_event') },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    stableId: t.string().index(),
    userId: t.string().index(),
    nickname: t.string(),
    value: t.string(),
    source: t.string().index(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_incident_comment = table(
  { name: named('incident_comment') },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    stableId: t.string().index(),
    userId: t.string().index(),
    nickname: t.string(),
    body: t.string(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_report_dump = table(
  { name: named('report_dump') },
  {
    id: t.string().primaryKey(),
    payload: t.string(),
    attempts: t.u32(),
    createdAt: t.string().index(),
    nextAttemptAt: t.string().index(),
    lastAttemptAt: t.string(),
    lastError: t.string(),
  }
);

const satiksmebot_report_dedupe = table(
  { name: named('report_dedupe') },
  {
    id: t.string().primaryKey(),
    reportKind: t.string().index(),
    stableId: t.string().index(),
    scopeKey: t.string().index(),
    lastReportAt: t.string().index(),
  }
);

const satiksmebot_chat_analyzer_checkpoint = table(
  { name: named('chat_analyzer_checkpoint') },
  {
    chatId: t.string().primaryKey(),
    lastMessageId: t.string(),
    updatedAt: t.string().index(),
  }
);

const satiksmebot_chat_analyzer_message = table(
  { name: named('chat_analyzer_message') },
  {
    id: t.string().primaryKey(),
    chatId: t.string().index(),
    messageId: t.string().index(),
    senderId: t.string().index(),
    senderStableId: t.string().index(),
    senderNickname: t.string(),
    text: t.string(),
    messageDate: t.string().index(),
    receivedAt: t.string().index(),
    replyToMessageId: t.string(),
    status: t.string().index(),
    attempts: t.u32(),
    analysisJson: t.string(),
    appliedActionId: t.string(),
    appliedTargetKey: t.string().index(),
    lastError: t.string(),
    processedAt: t.string().index(),
  }
);

const satiksmebot_chat_analyzer_batch = table(
  { name: named('chat_analyzer_batch') },
  {
    id: t.string().primaryKey(),
    status: t.string().index(),
    dryRun: t.bool(),
    startedAt: t.string().index(),
    finishedAt: t.string(),
    messageCount: t.u32(),
    reportCount: t.u32(),
    voteCount: t.u32(),
    ignoredCount: t.u32(),
    wouldApply: t.u32(),
    appliedCount: t.u32(),
    errorCount: t.u32(),
    model: t.string(),
    selectedModel: t.string(),
    resultJson: t.string(),
    lastError: t.string(),
  }
);

const satiksmebot_chat_analyzer_batch_message = table(
  { name: named('chat_analyzer_batch_message') },
  {
    id: t.string().primaryKey(),
    batchId: t.string().index(),
    chatMessageId: t.string().index(),
    messageId: t.string().index(),
    status: t.string().index(),
    processedAt: t.string().index(),
  }
);

const satiksmebot_public_stop_sighting = table(
  { name: named('public_stop_sighting'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    stopId: t.string().index(),
    stopName: t.string(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_public_vehicle_sighting = table(
  { name: named('public_vehicle_sighting'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    stopId: t.string().index(),
    stopName: t.string(),
    mode: t.string().index(),
    routeLabel: t.string().index(),
    direction: t.string(),
    destination: t.string(),
    departureSeconds: t.u32(),
    // Retained for v1 schema compatibility; never populated publicly.
    liveRowId: t.string(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_public_area_report = table(
  { name: named('public_area_report'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    latitude: t.number(),
    longitude: t.number(),
    radiusMeters: t.u32(),
    description: t.string(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_public_incident = table(
  { name: named('public_incident'), public: true },
  {
    id: t.string().primaryKey(),
    scope: t.string().index(),
    subjectId: t.string().index(),
    subjectName: t.string(),
    stopId: t.string().index(),
    lastReportName: t.string(),
    lastReportAt: t.string().index(),
    lastReporter: t.string(),
    commentCount: t.u32(),
    ongoingVotes: t.u32(),
    clearedVotes: t.u32(),
    active: t.bool(),
    resolved: t.bool(),
    vehicle: t.option(vehicleContextDoc),
    area: t.option(areaContextDoc).default({
      scopeKey: '',
      latitude: 0,
      longitude: 0,
      radiusMeters: 0,
      description: '',
    }),
  }
);

const satiksmebot_public_incident_event = table(
  { name: named('public_incident_event'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    kind: t.string(),
    name: t.string(),
    nickname: t.string(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_public_incident_comment = table(
  { name: named('public_incident_comment'), public: true },
  {
    id: t.string().primaryKey(),
    incidentId: t.string().index(),
    nickname: t.string(),
    body: t.string(),
    createdAt: t.string().index(),
  }
);

const satiksmebot_public_live_snapshot_state = table(
  { name: named('public_live_snapshot_state'), public: true },
  {
    feed: t.string().primaryKey(),
    version: t.string(),
    path: t.string(),
    // Retained v1 columns are deliberately inert on the public table.
    hash: t.string(),
    publishedAt: t.string(),
    lastSuccessAt: t.string(),
    lastAttemptAt: t.string(),
    status: t.string(),
    consecutiveFailures: t.u32(),
    vehicleCount: t.u32(),
    updatedAt: t.string().index(),
  }
);

const satiksmebot_live_viewer_heartbeat = table(
  // The v1 table must remain public for a non-destructive schema update. It is
  // an empty compatibility shell; current viewer state lives only in the
  // private satiksmebot_live_viewer_state table.
  { name: named('live_viewer_heartbeat'), public: true },
  {
    sessionId: t.string().primaryKey(),
    page: t.string().index(),
    lastSeenAt: t.string().index(),
  }
);

const satiksmebot_live_viewer_state = table(
  { name: named('live_viewer_state') },
  {
    sessionId: t.string().primaryKey(),
    page: t.string().index(),
    visible: t.bool(),
    updatedAt: t.string().index(),
  }
);

const spacetimedb: any = schema(
  {
    satiksmebot_active_bundle,
    satiksmebot_stop_catalog,
    satiksmebot_route_catalog,
    satiksmebot_import_chunk,
    satiksmebot_reporter_identity,
    satiksmebot_stop_sighting,
    satiksmebot_vehicle_sighting,
    satiksmebot_area_report,
    satiksmebot_incident_vote,
    satiksmebot_incident_vote_event,
    satiksmebot_incident_comment,
    satiksmebot_report_dump,
    satiksmebot_report_dedupe,
    satiksmebot_chat_analyzer_checkpoint,
    satiksmebot_chat_analyzer_message,
    satiksmebot_chat_analyzer_batch,
    satiksmebot_chat_analyzer_batch_message,
    satiksmebot_public_stop_sighting,
    satiksmebot_public_vehicle_sighting,
    satiksmebot_public_area_report,
    satiksmebot_public_incident,
    satiksmebot_public_incident_event,
    satiksmebot_public_incident_comment,
    satiksmebot_public_live_snapshot_state,
    satiksmebot_live_viewer_heartbeat,
    satiksmebot_live_viewer_state,
  },
  { CASE_CONVERSION_POLICY: CaseConversionPolicy.None }
);

export default spacetimedb;

function asString(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function asInt(value: unknown): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) {
    return 0;
  }
  return Math.max(0, Math.floor(parsed));
}

function rowsFrom(iterable: any): any[] {
  return Array.from(iterable as Iterable<any>) as any[];
}

function serialize(payload: unknown): string {
  return JSON.stringify(payload);
}

function parseJSON(raw: string, errorMessage: string): any {
  try {
    return JSON.parse(raw);
  } catch {
    throw new SenderError(errorMessage);
  }
}

function nowDate(ctx: any): Date {
  return ctx.timestamp.toDate();
}

function nowISO(ctx: any): string {
  return ctx.timestamp.toISOString();
}

function parseISO(value: string | undefined | null): Date | null {
  if (!value) {
    return null;
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function publicTimestamp(value: unknown): string {
  const raw = asString(value).trim();
  const parsed = parseISO(raw);
  if (!parsed) {
    return raw;
  }
  parsed.setUTCMilliseconds(0);
  return parsed.toISOString().replace('.000Z', 'Z');
}

function compareTimeAscending(left: string | undefined, right: string | undefined): number {
  return (parseISO(left || '')?.getTime() || 0) - (parseISO(right || '')?.getTime() || 0);
}

function compareTimeDescending(left: string | undefined, right: string | undefined): number {
  return compareTimeAscending(right, left);
}

function trimOptional(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed === '' ? undefined : trimmed;
}

function parseBatchItems(itemsJson: string): any[] {
  const parsed = parseJSON(itemsJson, 'invalid batch JSON');
  if (Array.isArray(parsed)) {
    return parsed.filter((item) => Boolean(item) && typeof item === 'object');
  }
  if (parsed && typeof parsed === 'object' && Array.isArray(parsed.items)) {
    return parsed.items.filter((item) => Boolean(item) && typeof item === 'object');
  }
  throw new SenderError('invalid batch JSON');
}

function rowsForImport(tx: any, importId: string): any[] {
  return rowsFrom(tx.db.satiksmebot_import_chunk.importId.filter(importId));
}

function clearImport(tx: any, importId: string): void {
  for (const row of rowsForImport(tx, importId)) {
    tx.db.satiksmebot_import_chunk.id.delete(row.id);
  }
}

function randomId(ctx: any, prefix: string): string {
  return `${prefix}-${ctx.newUuidV7().toString()}`;
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
  const input = `satiksme:${stableId}`;
  for (let index = 0; index < input.length; index += 1) {
    hash ^= input.charCodeAt(index);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  const adjective = adjectives[hash % adjectives.length];
  const noun = nouns[(hash >>> 8) % nouns.length];
  const suffix = String((hash % 900) + 100).padStart(3, '0');
  return `${adjective} ${noun} ${suffix}`;
}

function normalizeDirection(value: string): string {
  return value.trim().replaceAll('>', '-');
}

function sanitizeIncidentKey(value: string): string {
  let next = value.trim().toLowerCase();
  if (!next) {
    return 'unknown';
  }
  const replacer = [
    [' ', '-'],
    ['/', '-'],
    ['\\', '-'],
    [':', '-'],
    ['|', '-'],
    ['>', '-'],
    ['<', '-'],
  ];
  for (const [from, to] of replacer) {
    next = next.replaceAll(from, to);
  }
  next = next.replace(/-+/g, '-').replace(/^-+|-+$/g, '');
  return next || 'unknown';
}

function stopIncidentID(stopId: string): string {
  return `stop:${sanitizeIncidentKey(stopId)}`;
}

function vehicleIncidentID(scopeKey: string): string {
  const clean = scopeKey.trim().toLowerCase();
  if (/^pub-[0-9a-f]{8}$/.test(clean)) {
    return `vehicle:${clean}`;
  }
  return `vehicle:pub-${fnv1a32Hex(scopeKey)}`;
}

function fnv1a32Hex(value: string): string {
  let hash = 2166136261 >>> 0;
  const input = value.trim() || 'unknown';
  for (let index = 0; index < input.length; index += 1) {
    hash ^= input.charCodeAt(index);
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  return hash.toString(16).padStart(8, '0');
}

function publicAreaIncidentID(scopeKey: string): string {
  const clean = scopeKey.trim().toLowerCase();
  if (/^pub-[0-9a-f]{8}$/.test(clean)) {
    return `area:${clean}`;
  }
  return `area:pub-${fnv1a32Hex(scopeKey)}`;
}

function publicAreaReportID(reportId: string): string {
  return `area-report:pub-${fnv1a32Hex(reportId)}`;
}

function publicStopSightingID(reportId: string): string {
  return `stop-report:pub-${fnv1a32Hex(reportId)}`;
}

function publicVehicleSightingID(reportId: string): string {
  return `vehicle-report:pub-${fnv1a32Hex(reportId)}`;
}

function publicIncidentEventID(incidentId: string, eventId: string, createdAt: string): string {
  const seed = [
    asString(incidentId).trim(),
    asString(eventId).trim(),
    asString(createdAt).trim(),
  ].join('|');
  return `incident-event:pub-${fnv1a32Hex(seed)}`;
}

function publicIncidentCommentID(incidentId: string, commentId: string, createdAt: string): string {
  const seed = [
    asString(incidentId).trim(),
    asString(commentId).trim(),
    asString(createdAt).trim(),
  ].join('|');
  return `incident-comment:pub-${fnv1a32Hex(seed)}`;
}

function areaIncidentID(scopeKey: string): string {
  return publicAreaIncidentID(scopeKey);
}

function vehicleScopeKey(payload: any): string {
  const mode = asString(payload?.mode).trim().toLowerCase();
  const routeLabel = asString(payload?.routeLabel).trim();
  const direction = asString(payload?.direction).trim();
  const destination = asString(payload?.destination).trim().toLowerCase();
  const liveRowId = asString(payload?.liveRowId).trim();
  if (liveRowId) {
    // This is private storage identity and must stay byte-for-byte compatible
    // with the v1 module and the Go service. Only the incident ID derived from
    // it is exposed, and that ID is opaque.
    return `live:${mode}:${routeLabel}:${direction}:${liveRowId}`;
  }
  return `fallback:${mode}:${routeLabel}:${direction}:${destination}`;
}

function opaqueVehicleScopeKey(logicalScopeKey: string): string {
  return vehicleIncidentID(logicalScopeKey).slice('vehicle:'.length);
}

function vehicleLogicalScopeKey(row: any): string {
  const storedScopeKey = asString(row?.scopeKey).trim();
  if (storedScopeKey && !/^pub-[0-9a-f]{8}$/i.test(storedScopeKey)) {
    return storedScopeKey;
  }
  return vehicleScopeKey(row);
}

function ensureVehiclePublicIDAvailable(tx: any, logicalScopeKey: string): void {
  const cleanLogicalScopeKey = asString(logicalScopeKey).trim();
  const opaqueScopeKey = opaqueVehicleScopeKey(cleanLogicalScopeKey);
  for (const row of rowsFrom(tx.db.satiksmebot_vehicle_sighting.iter())) {
    const existingLogicalScopeKey = vehicleLogicalScopeKey(row);
    if (opaqueVehicleScopeKey(existingLogicalScopeKey) === opaqueScopeKey && existingLogicalScopeKey !== cleanLogicalScopeKey) {
      throw new SenderError('opaque vehicle incident collision');
    }
  }
}

function normalizeAreaRadius(value: unknown): number {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return DEFAULT_AREA_RADIUS_METERS;
  }
  return Math.min(Math.floor(parsed), MAX_AREA_RADIUS_METERS);
}

function roundCoordinate(value: number): number {
  return Math.round(value * 100000) / 100000;
}

function publicAreaCoordinate(value: number): number {
  return Math.round(value * 1000) / 1000;
}

function publicAreaRadius(value: unknown): number {
  const radius = normalizeAreaRadius(value);
  return radius < 250 ? 250 : radius;
}

function publicAreaContext(row: any) {
  if (!row || typeof row !== 'object') {
    return undefined;
  }
  const latitude = Number(row.latitude);
  const longitude = Number(row.longitude);
  const radiusMeters = publicAreaRadius(row.radiusMeters);
  if (!validCoordinate(latitude, -90, 90) || !validCoordinate(longitude, -180, 180) || radiusMeters <= 0) {
    return undefined;
  }
  return {
    scopeKey: '',
    latitude: publicAreaCoordinate(latitude),
    longitude: publicAreaCoordinate(longitude),
    radiusMeters,
    description: asString(row.description).trim(),
  };
}

function publicVehicleContext(row: any, stopName: string) {
  return {
    scopeKey: '',
    stopId: asString(row.stopId).trim(),
    stopName,
    mode: asString(row.mode).trim(),
    routeLabel: asString(row.routeLabel).trim(),
    direction: normalizeDirection(asString(row.direction)),
    destination: asString(row.destination).trim(),
    departureSeconds: Number(row.departureSeconds) || 0,
    liveRowId: '',
  };
}

function publicAreaReportPayload(row: any) {
  const area = publicAreaContext(row);
  return {
    id: publicAreaReportID(asString(row.id).trim()),
    incidentId: asString(row.incidentId).trim(),
    latitude: Number(area?.latitude) || 0,
    longitude: Number(area?.longitude) || 0,
    radiusMeters: Number(area?.radiusMeters) || 0,
    description: asString(area?.description).trim(),
    createdAt: publicTimestamp(row.createdAt),
  };
}

function validCoordinate(value: number, min: number, max: number): boolean {
  return Number.isFinite(value) && value >= min && value <= max;
}

function trimIncidentSlug(value: string): string {
  const clean = value.trim().slice(0, 48).replace(/^-+|-+$/g, '');
  return clean || 'area';
}

function normalizeAreaReportInput(payload: any) {
  const latitude = roundCoordinate(Number(payload?.latitude));
  const longitude = roundCoordinate(Number(payload?.longitude));
  const description = asString(payload?.description).trim().replace(/\s+/g, ' ');
  if (!validCoordinate(latitude, -90, 90) || !validCoordinate(longitude, -180, 180)) {
    throw new SenderError('invalid coordinates');
  }
  if (!description) {
    throw new SenderError('description is required');
  }
  if (Array.from(description).length > 160) {
    throw new SenderError('description is too long');
  }
  return {
    latitude,
    longitude,
    radiusMeters: normalizeAreaRadius(payload?.radiusMeters),
    description,
  };
}

function areaScopeKey(payload: any): string {
  const normalized = normalizeAreaReportInput(payload);
  const latCell = Math.round(normalized.latitude * 1000);
  const lonCell = Math.round(normalized.longitude * 1000);
  const slug = trimIncidentSlug(sanitizeIncidentKey(normalized.description));
  return `${latCell}:${lonCell}:${normalized.radiusMeters}:${slug}`;
}

function stopIncidentLabel(): string {
  return incidentVoteLabel('ONGOING');
}

function vehicleIncidentSubjectName(item: any, stopName: string): string {
  const label = `${asString(item.mode).trim()} ${asString(item.routeLabel).trim()}`.trim();
  if (label) {
    return label;
  }
  if (stopName) {
    return stopName;
  }
  if (asString(item.destination).trim()) {
    return asString(item.destination).trim();
  }
  return asString(item.scopeKey).trim();
}

function vehicleIncidentLabel(item: any): string {
  const mode = asString(item.mode).trim();
  const route = asString(item.routeLabel).trim();
  const destination = asString(item.destination).trim();
  const label = `${mode} ${route}`.trim();
  if (!label && !destination) {
    return 'Transporta kontrole';
  }
  if (!label) {
    return `Kontrole uz ${destination}`;
  }
  if (!destination) {
    return `Kontrole ${label}`;
  }
  return `Kontrole ${label} uz ${destination}`;
}

function areaIncidentSubjectName(item: any): string {
  return asString(item?.description).trim() || 'Atzīmēta vieta';
}

function sessionFromTx(tx: any) {
  const auth = tx.senderAuth;
  if (!auth || !auth.hasJWT || !auth.jwt) {
    throw new SenderError('telegram auth required');
  }
  const jwt = auth.jwt;
  const payload = (jwt.fullPayload || {}) as Record<string, unknown>;
  const roles = Array.isArray(payload.roles)
    ? payload.roles.filter((item): item is string => typeof item === 'string')
    : [];
  const stableId = asString(jwt.subject).trim();
  if (!stableId) {
    throw new SenderError('telegram auth required');
  }
  let userId = asString(payload.telegram_user_id).trim();
  if (!userId && stableId.startsWith('telegram:')) {
    userId = stableId.slice('telegram:'.length).trim();
  }
  return {
    stableId,
    userId,
    language: asString(payload.language).trim().toLowerCase() || 'lv',
    roles,
    smoke: payload.smoke === true,
  };
}

function requireReporterSession(tx: any) {
  const session = sessionFromTx(tx);
  if (!session.roles.includes('satiksme_viewer') || !session.stableId.startsWith('telegram:')) {
    throw new SenderError('telegram auth required');
  }
  return session;
}

function optionalViewerStableId(tx: any): string {
  try {
    return requireReporterSession(tx).stableId;
  } catch (_) {
    return '';
  }
}

function requireServiceRole(tx: any): void {
  const session = sessionFromTx(tx);
  if (!session.roles.includes('satiksme_service')) {
    throw new SenderError('service role required');
  }
}

function ensureReporter(tx: any) {
  const session = requireReporterSession(tx);
  const currentAt = nowISO(tx);
  const existing = tx.db.satiksmebot_reporter_identity.stableId.find(session.stableId);
  const next = {
    stableId: session.stableId,
    userId: session.userId,
    nickname: trimOptional(asString(existing?.nickname)) || genericNickname(session.stableId),
    language: session.language || 'lv',
    createdAt: trimOptional(asString(existing?.createdAt)) || currentAt,
    updatedAt: currentAt,
    lastSeenAt: currentAt,
  };
  tx.db.satiksmebot_reporter_identity.stableId.delete(session.stableId);
  return {
    ...tx.db.satiksmebot_reporter_identity.insert(next),
    smoke: session.smoke === true,
  };
}

function stableIdFromServiceItem(item: any): string {
  const stableId = trimOptional(asString(item?.stableId));
  const userId = asString(item?.userId).trim();
  if (stableId && !validTelegramStableId(stableId)) {
    throw new SenderError('valid telegram stableId is required');
  }
  if (userId && !validTelegramUserId(userId)) {
    throw new SenderError('valid telegram userId is required');
  }
  if (stableId && userId && stableId !== `telegram:${userId}`) {
    throw new SenderError('telegram stableId and userId mismatch');
  }
  if (stableId) {
    return stableId;
  }
  if (userId) {
    return `telegram:${userId}`;
  }
  throw new SenderError('telegram user id is required');
}

function userIdForStableId(stableId: string, item: any): string {
  const userId = asString(item?.userId).trim();
  if (userId) {
    if (!validTelegramUserId(userId)) {
      throw new SenderError('valid telegram userId is required');
    }
    return userId;
  }
  if (stableId.startsWith('telegram:')) {
    const parsed = stableId.slice('telegram:'.length).trim();
    if (!validTelegramUserId(parsed)) {
      throw new SenderError('valid telegram userId is required');
    }
    return parsed;
  }
  return '';
}

function validTelegramUserId(raw: string): boolean {
  const clean = asString(raw).trim();
  if (!/^[1-9][0-9]*$/.test(clean)) {
    return false;
  }
  return Number.isSafeInteger(Number(clean));
}

function validTelegramStableId(raw: string): boolean {
  const clean = asString(raw).trim();
  return clean.startsWith('telegram:') && validTelegramUserId(clean.slice('telegram:'.length));
}

function nicknameForStableId(tx: any, stableId: string): string {
  const cleanStableId = stableId.trim();
  return trimOptional(asString(tx.db.satiksmebot_reporter_identity.stableId.find(cleanStableId)?.nickname))
    || genericNickname(cleanStableId);
}

function activeBundle(tx: any): any | null {
  return tx.db.satiksmebot_active_bundle.id.find('active') || null;
}

function requireActiveBundle(tx: any, bundleVersion: string, bundleGeneratedAt: string): void {
  const active = activeBundle(tx);
  if (!active) {
    throw new SenderError('active bundle unavailable');
  }
  if (!bundleVersion.trim() || !bundleGeneratedAt.trim()) {
    throw new SenderError('bundle identity required');
  }
  if (bundleVersion.trim() !== asString(active.version).trim() || bundleGeneratedAt.trim() !== asString(active.generatedAt).trim()) {
    throw new SenderError('stale bundle identity');
  }
}

function sanitizeStopCatalogRow(item: any): any | null {
  const id = asString(item?.id).trim();
  if (!id) {
    return null;
  }
  return {
    id,
    liveId: '',
    name: asString(item?.name).trim(),
    latitude: Number(item?.latitude) || 0,
    longitude: Number(item?.longitude) || 0,
    modes: Array.isArray(item?.modes) ? item.modes.map((value: any) => asString(value).trim()).filter(Boolean) : [],
    routeLabels: Array.isArray(item?.routeLabels) ? item.routeLabels.map((value: any) => asString(value).trim()).filter(Boolean) : [],
    nearbyStopIds: [],
  };
}

function sanitizeRouteCatalogRow(item: any): any | null {
  const mode = asString(item?.mode).trim();
  const label = asString(item?.label).trim();
  if (!mode || !label) {
    return null;
  }
  return {
    id: `${mode}:${label}`,
    label,
    mode,
    name: asString(item?.name).trim(),
    stopIds: Array.isArray(item?.stopIds) ? item.stopIds.map((value: any) => asString(value).trim()).filter(Boolean) : [],
  };
}

function applyBundleSnapshot(tx: any, snapshot: any): { stops: number, routes: number } {
  const version = asString(snapshot?.version).trim();
  const generatedAt = asString(snapshot?.generatedAt).trim();
  if (!version || !generatedAt) {
    throw new SenderError('bundle version and generatedAt are required');
  }

  const stopsById = new Map<string, any>();
  const routesById = new Map<string, any>();
  for (const item of Array.isArray(snapshot?.stops) ? snapshot.stops : []) {
    const next = sanitizeStopCatalogRow(item);
    if (next) {
      stopsById.set(next.id, next);
    }
  }
  for (const item of Array.isArray(snapshot?.routes) ? snapshot.routes : []) {
    const next = sanitizeRouteCatalogRow(item);
    if (next) {
      routesById.set(next.id, next);
    }
  }

  for (const row of rowsFrom(tx.db.satiksmebot_stop_catalog.iter())) {
    tx.db.satiksmebot_stop_catalog.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.satiksmebot_route_catalog.iter())) {
    tx.db.satiksmebot_route_catalog.id.delete(row.id);
  }
  for (const row of stopsById.values()) {
    tx.db.satiksmebot_stop_catalog.insert(row);
  }
  for (const row of routesById.values()) {
    tx.db.satiksmebot_route_catalog.insert(row);
  }
  tx.db.satiksmebot_active_bundle.id.delete('active');
  tx.db.satiksmebot_active_bundle.insert({
    id: 'active',
    version,
    generatedAt,
    stopCount: stopsById.size,
    routeCount: routesById.size,
    updatedAt: nowISO(tx),
  });
  refreshPublicProjections(tx);
  return { stops: stopsById.size, routes: routesById.size };
}

function stopCatalogRow(tx: any, stopId: string): any | null {
  return tx.db.satiksmebot_stop_catalog.id.find(stopId.trim()) || null;
}

function requireStopCatalogRow(tx: any, stopId: string): any {
  const row = stopCatalogRow(tx, stopId);
  if (!row) {
    throw new SenderError('stop not found');
  }
  return row;
}

function userIdNumber(userId: string): number {
  const parsed = Number(userId);
  return Number.isFinite(parsed) ? Math.trunc(parsed) : 0;
}

function stopSightingRowToJSON(row: any) {
  return {
    id: asString(row.id).trim(),
    stopId: asString(row.stopId).trim(),
    userId: userIdNumber(asString(row.userId).trim()),
    hidden: row.hidden === true,
    createdAt: asString(row.createdAt).trim(),
  };
}

function vehicleSightingRowToJSON(row: any) {
  return {
    id: asString(row.id).trim(),
    stopId: asString(row.stopId).trim(),
    userId: userIdNumber(asString(row.userId).trim()),
    mode: asString(row.mode).trim(),
    routeLabel: asString(row.routeLabel).trim(),
    direction: asString(row.direction).trim(),
    destination: asString(row.destination).trim(),
    departureSeconds: Number(row.departureSeconds) || 0,
    liveRowId: asString(row.liveRowId).trim(),
    scopeKey: asString(row.scopeKey).trim(),
    hidden: row.hidden === true,
    createdAt: asString(row.createdAt).trim(),
  };
}

function areaReportRowToJSON(row: any) {
  return {
    id: asString(row.id).trim(),
    userId: userIdNumber(asString(row.userId).trim()),
    latitude: Number(row.latitude) || 0,
    longitude: Number(row.longitude) || 0,
    radiusMeters: Number(row.radiusMeters) || 0,
    description: asString(row.description).trim(),
    scopeKey: asString(row.scopeKey).trim(),
    hidden: row.hidden === true,
    createdAt: asString(row.createdAt).trim(),
  };
}

function incidentAreaContext(row: any) {
  return publicAreaContext(row);
}

function incidentVoteRowToJSON(row: any) {
  return {
    incidentId: asString(row.incidentId).trim(),
    userId: userIdNumber(asString(row.userId).trim()),
    nickname: asString(row.nickname).trim(),
    value: asString(row.value).trim(),
    createdAt: asString(row.createdAt).trim(),
    updatedAt: asString(row.updatedAt).trim(),
  };
}

function incidentVoteEventRowToJSON(row: any) {
  return {
    id: asString(row.id).trim(),
    incidentId: asString(row.incidentId).trim(),
    userId: userIdNumber(asString(row.userId).trim()),
    nickname: asString(row.nickname).trim(),
    value: asString(row.value).trim(),
    source: asString(row.source).trim(),
    createdAt: asString(row.createdAt).trim(),
  };
}

function incidentCommentRowToJSON(row: any) {
  return {
    id: asString(row.id).trim(),
    incidentId: asString(row.incidentId).trim(),
    userId: userIdNumber(asString(row.userId).trim()),
    nickname: asString(row.nickname).trim(),
    body: asString(row.body).trim(),
    createdAt: asString(row.createdAt).trim(),
  };
}

function sanitizeStopSighting(tx: any, item: any): any {
  const id = asString(item?.id).trim() || randomId(tx, 'stop');
  const stopId = asString(item?.stopId).trim();
  if (!stopId) {
    throw new SenderError('stopId is required');
  }
  const stableId = stableIdFromServiceItem(item);
  const userId = userIdForStableId(stableId, item);
  return {
    id,
    stopId,
    stableId,
    userId,
    hidden: item?.hidden === true,
    createdAt: trimOptional(asString(item?.createdAt)) || new Date().toISOString(),
  };
}

function sanitizeVehicleSighting(tx: any, item: any): any {
  const stableId = stableIdFromServiceItem(item);
  const userId = userIdForStableId(stableId, item);
  const payload = {
    id: asString(item?.id).trim() || randomId(tx, 'vehicle'),
    stopId: asString(item?.stopId).trim(),
    stableId,
    userId,
    mode: asString(item?.mode).trim(),
    routeLabel: asString(item?.routeLabel).trim(),
    direction: asString(item?.direction).trim(),
    destination: asString(item?.destination).trim(),
    departureSeconds: asInt(item?.departureSeconds),
    liveRowId: asString(item?.liveRowId).trim(),
    scopeKey: asString(item?.scopeKey).trim(),
    hidden: item?.hidden === true,
    createdAt: trimOptional(asString(item?.createdAt)) || new Date().toISOString(),
  };
  payload.scopeKey = payload.scopeKey || opaqueVehicleScopeKey(vehicleScopeKey(payload));
  return payload;
}

function sanitizeAreaReport(tx: any, item: any): any {
  const stableId = stableIdFromServiceItem(item);
  const userId = userIdForStableId(stableId, item);
  const normalized = normalizeAreaReportInput(item);
  const payload = {
    id: asString(item?.id).trim() || randomId(tx, 'area'),
    stableId,
    userId,
    latitude: normalized.latitude,
    longitude: normalized.longitude,
    radiusMeters: normalized.radiusMeters,
    description: normalized.description,
    scopeKey: asString(item?.scopeKey).trim(),
    hidden: item?.hidden === true,
    createdAt: trimOptional(asString(item?.createdAt)) || new Date().toISOString(),
  };
  payload.scopeKey = payload.scopeKey || areaScopeKey(payload);
  return payload;
}

function sanitizeIncidentVote(tx: any, item: any): any {
  const stableId = stableIdFromServiceItem(item);
  const userId = userIdForStableId(stableId, item);
  const value = asString(item?.value).trim().toUpperCase();
  if (value !== 'ONGOING' && value !== 'CLEARED') {
    throw new SenderError('invalid vote value');
  }
  const incidentId = asString(item?.incidentId).trim();
  if (!incidentId) {
    throw new SenderError('incidentId is required');
  }
  const createdAt = trimOptional(asString(item?.createdAt)) || trimOptional(asString(item?.updatedAt)) || new Date().toISOString();
  return {
    id: `${incidentId}|${stableId}`,
    incidentId,
    stableId,
    userId,
    nickname: nicknameForStableId(tx, stableId),
    value,
    createdAt,
    updatedAt: trimOptional(asString(item?.updatedAt)) || createdAt,
  };
}

function sanitizeIncidentVoteEvent(tx: any, item: any): any {
  const stableId = stableIdFromServiceItem(item);
  const userId = userIdForStableId(stableId, item);
  const value = asString(item?.value).trim().toUpperCase();
  if (value !== 'ONGOING' && value !== 'CLEARED') {
    throw new SenderError('invalid vote value');
  }
  const source = asString(item?.source).trim();
  if (source !== 'map_report' && source !== 'vote' && source !== 'telegram_chat') {
    throw new SenderError('invalid vote event source');
  }
  const incidentId = asString(item?.incidentId).trim();
  if (!incidentId) {
    throw new SenderError('incidentId is required');
  }
  const createdAt = trimOptional(asString(item?.createdAt)) || new Date().toISOString();
  return {
    id: asString(item?.id).trim() || randomId(tx, 'vote-event'),
    incidentId,
    stableId,
    userId,
    nickname: nicknameForStableId(tx, stableId),
    value,
    source,
    createdAt,
  };
}

function sanitizeIncidentComment(tx: any, item: any): any {
  const stableId = stableIdFromServiceItem(item);
  const userId = userIdForStableId(stableId, item);
  const incidentId = asString(item?.incidentId).trim();
  const body = asString(item?.body).trim();
  if (!incidentId) {
    throw new SenderError('incidentId is required');
  }
  if (!body) {
    throw new SenderError('comment is required');
  }
  return {
    id: asString(item?.id).trim() || randomId(tx, 'comment'),
    incidentId,
    stableId,
    userId,
    nickname: nicknameForStableId(tx, stableId),
    body,
    createdAt: trimOptional(asString(item?.createdAt)) || new Date().toISOString(),
  };
}

function sanitizeReportDumpItem(tx: any, item: any): any {
  const payload = asString(item?.payload);
  if (!payload.trim()) {
    throw new SenderError('report dump payload is required');
  }
  const createdAt = trimOptional(asString(item?.createdAt));
  if (!createdAt || !parseISO(createdAt)) {
    throw new SenderError('report dump createdAt is required');
  }
  const nextAttemptAt = trimOptional(asString(item?.nextAttemptAt));
  if (!nextAttemptAt || !parseISO(nextAttemptAt)) {
    throw new SenderError('report dump nextAttemptAt is required');
  }
  const lastAttemptAt = trimOptional(asString(item?.lastAttemptAt)) || '';
  if (lastAttemptAt && !parseISO(lastAttemptAt)) {
    throw new SenderError('report dump lastAttemptAt is invalid');
  }
  return {
    id: asString(item?.id).trim() || randomId(tx, 'dump'),
    payload,
    attempts: asInt(item?.attempts),
    createdAt,
    nextAttemptAt,
    lastAttemptAt,
    lastError: asString(item?.lastError),
  };
}

function numericString(value: unknown): string {
  const raw = String(value ?? '').trim();
  if (!raw) {
    return '0';
  }
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) {
    return '0';
  }
  return String(Math.trunc(parsed));
}

function numericValue(value: unknown): number {
  const parsed = Number(String(value ?? '').trim());
  return Number.isFinite(parsed) ? Math.trunc(parsed) : 0;
}

function sanitizeChatAnalyzerStatus(value: string): string {
  const status = value.trim() || 'pending';
  if (['pending', 'applied', 'ignored', 'uncertain', 'failed', 'dry_run'].includes(status)) {
    return status;
  }
  throw new SenderError('invalid chat analyzer status');
}

function sanitizeChatAnalyzerBatchStatus(value: string): string {
  const status = value.trim() || 'running';
  if (['running', 'completed', 'failed'].includes(status)) {
    return status;
  }
  throw new SenderError('invalid chat analyzer batch status');
}

function sanitizeChatAnalyzerMessage(tx: any, item: any): any {
  const id = asString(item?.id).trim();
  const chatId = asString(item?.chatId).trim();
  const messageId = numericString(item?.messageId);
  const senderId = numericString(item?.senderId);
  const text = asString(item?.text);
  const messageDate = trimOptional(asString(item?.messageDate));
  const receivedAt = trimOptional(asString(item?.receivedAt));
  if (!id || !chatId || !messageId || messageId === '0') {
    throw new SenderError('chat analyzer message identity is required');
  }
  if (!text.trim()) {
    throw new SenderError('chat analyzer message text is required');
  }
  if (!messageDate || !parseISO(messageDate)) {
    throw new SenderError('chat analyzer messageDate is required');
  }
  if (!receivedAt || !parseISO(receivedAt)) {
    throw new SenderError('chat analyzer receivedAt is required');
  }
  const processedAt = trimOptional(asString(item?.processedAt)) || '';
  if (processedAt && !parseISO(processedAt)) {
    throw new SenderError('chat analyzer processedAt is invalid');
  }
  return {
    id,
    chatId,
    messageId,
    senderId,
    senderStableId: asString(item?.senderStableId).trim() || `telegram:${senderId}`,
    senderNickname: asString(item?.senderNickname).trim() || genericNickname(`telegram:${senderId}`),
    text,
    messageDate,
    receivedAt,
    replyToMessageId: numericString(item?.replyToMessageId),
    status: sanitizeChatAnalyzerStatus(asString(item?.status)),
    attempts: asInt(item?.attempts),
    analysisJson: asString(item?.analysisJson),
    appliedActionId: asString(item?.appliedActionId).trim(),
    appliedTargetKey: asString(item?.appliedTargetKey).trim(),
    lastError: asString(item?.lastError),
    processedAt,
  };
}

function sanitizeChatAnalyzerBatch(item: any): any {
  const id = asString(item?.id).trim();
  const startedAt = trimOptional(asString(item?.startedAt));
  const finishedAt = trimOptional(asString(item?.finishedAt));
  if (!id) {
    throw new SenderError('chat analyzer batch id is required');
  }
  if (!startedAt || !parseISO(startedAt)) {
    throw new SenderError('chat analyzer batch startedAt is required');
  }
  if (finishedAt && !parseISO(finishedAt)) {
    throw new SenderError('chat analyzer batch finishedAt is invalid');
  }
  return {
    id,
    status: sanitizeChatAnalyzerBatchStatus(asString(item?.status)),
    dryRun: item?.dryRun === true,
    startedAt,
    finishedAt,
    messageCount: asInt(item?.messageCount),
    reportCount: asInt(item?.reportCount),
    voteCount: asInt(item?.voteCount),
    ignoredCount: asInt(item?.ignoredCount),
    wouldApply: asInt(item?.wouldApply),
    appliedCount: asInt(item?.appliedCount),
    errorCount: asInt(item?.errorCount),
    model: asString(item?.model).trim(),
    selectedModel: asString(item?.selectedModel).trim(),
    resultJson: asString(item?.resultJson),
    lastError: asString(item?.error || item?.lastError),
  };
}

function chatAnalyzerMessageRowToJSON(row: any) {
  return {
    id: asString(row.id).trim(),
    chatId: asString(row.chatId).trim(),
    messageId: numericValue(row.messageId),
    senderId: numericValue(row.senderId),
    senderStableId: asString(row.senderStableId).trim(),
    senderNickname: asString(row.senderNickname).trim(),
    text: asString(row.text),
    messageDate: asString(row.messageDate).trim(),
    receivedAt: asString(row.receivedAt).trim(),
    replyToMessageId: numericValue(row.replyToMessageId),
    status: asString(row.status).trim(),
    attempts: Number(row.attempts) || 0,
    analysisJson: asString(row.analysisJson),
    appliedActionId: asString(row.appliedActionId).trim(),
    appliedTargetKey: asString(row.appliedTargetKey).trim(),
    lastError: asString(row.lastError),
    processedAt: asString(row.processedAt).trim(),
  };
}

function liveSnapshotStateRowToJSON(row: any) {
  return {
    feed: asString(row.feed).trim(),
    version: asString(row.version).trim(),
    path: asString(row.path).trim(),
    updatedAt: asString(row.updatedAt).trim(),
  };
}

function latestVoteMap(tx: any, incidentId: string, sinceMs: number): Map<string, any> {
  const latest = new Map<string, any>();
  for (const row of rowsFrom(tx.db.satiksmebot_incident_vote.incidentId.filter(incidentId))) {
    const updatedMs = parseISO(asString(row.updatedAt))?.getTime() || 0;
    if (updatedMs < sinceMs) {
      continue;
    }
    const key = asString(row.stableId).trim();
    const existing = latest.get(key);
    if (!existing || compareTimeDescending(asString(row.updatedAt), asString(existing.updatedAt)) < 0) {
      latest.set(key, row);
    }
  }
  return latest;
}

function incidentVoteLabel(value: string): string {
  return asString(value).trim().toUpperCase() === 'CLEARED' ? 'Nav kontrole' : 'Kontrole';
}

function currentIncidentVote(tx: any, incidentId: string, stableId: string): any | null {
  return tx.db.satiksmebot_incident_vote.id.find(`${incidentId.trim()}|${stableId.trim()}`) || null;
}

function countMapReportsForStableIdSince(tx: any, stableId: string, sinceMs: number): number {
  let count = 0;
  for (const row of rowsFrom(tx.db.satiksmebot_stop_sighting.stableId.filter(stableId.trim()))) {
    const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
    if (row.hidden !== true && createdMs >= sinceMs) {
      count += 1;
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_vehicle_sighting.stableId.filter(stableId.trim()))) {
    const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
    if (row.hidden !== true && createdMs >= sinceMs) {
      count += 1;
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_area_report.stableId.filter(stableId.trim()))) {
    const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
    if (row.hidden !== true && createdMs >= sinceMs) {
      count += 1;
    }
  }
  return count;
}

function countIncidentVoteEventsForStableIdSince(tx: any, stableId: string, source: string, sinceMs: number): number {
  let count = 0;
  for (const row of rowsFrom(tx.db.satiksmebot_incident_vote_event.stableId.filter(stableId.trim()))) {
    const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
    if (asString(row.source).trim() === source && createdMs >= sinceMs) {
      count += 1;
    }
  }
  return count;
}

function countIncidentCommentsForStableIdSince(tx: any, stableId: string, sinceMs: number): number {
  let count = 0;
  for (const row of rowsFrom(tx.db.satiksmebot_incident_comment.stableId.filter(stableId.trim()))) {
    const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
    if (createdMs >= sinceMs) {
      count += 1;
    }
  }
  return count;
}

function countIncidentCommentsForIncidentSince(tx: any, incidentId: string, sinceMs: number): number {
  let count = 0;
  for (const row of rowsFrom(tx.db.satiksmebot_incident_comment.incidentId.filter(incidentId.trim()))) {
    const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
    if (createdMs >= sinceMs) {
      count += 1;
    }
  }
  return count;
}

function enforceIncidentCommentLimit(tx: any, incidentId: string, stableId: string) {
  const sinceMs = nowDate(tx).getTime() - COMMENT_ACTION_WINDOW_MS;
  if (countIncidentCommentsForStableIdSince(tx, stableId, sinceMs) >= COMMENT_ACTION_LIMIT) {
    throw new SenderError('Pārāk daudz komentāru. Jānogaida.');
  }
  if (countIncidentCommentsForIncidentSince(tx, incidentId, sinceMs) >= INCIDENT_COMMENT_ACTION_LIMIT) {
    throw new SenderError('Pārāk daudz komentāru šajā ziņojumā. Jānogaida.');
  }
}

function countPublicVoteActionsForStableIdSince(tx: any, stableId: string, sinceMs: number): number {
  return countIncidentVoteEventsForStableIdSince(tx, stableId, 'vote', sinceMs)
    + countIncidentVoteEventsForStableIdSince(tx, stableId, 'telegram_chat', sinceMs);
}

function sameVoteCooldownSeconds(tx: any, incidentId: string, stableId: string, value: string): number {
  const current = currentIncidentVote(tx, incidentId, stableId);
  if (!current || asString(current.value).trim().toUpperCase() !== asString(value).trim().toUpperCase()) {
    return 0;
  }
  const updatedMs = parseISO(asString(current.updatedAt))?.getTime() || 0;
  const remainingMs = SAME_VOTE_WINDOW_MS - (nowDate(tx).getTime() - updatedMs);
  return remainingMs > 0 ? Math.ceil(remainingMs / 1000) : 0;
}

function reportDedupeID(reportKind: string, stableId: string, scopeKey: string): string {
  return `${asString(reportKind).trim()}|${asString(stableId).trim()}|${asString(scopeKey).trim()}`;
}

function reportDedupeClaimActive(tx: any, reportKind: string, stableId: string, scopeKey: string, reportAt: string, windowMs: number): boolean {
  const cleanKind = asString(reportKind).trim();
  const cleanStableId = asString(stableId).trim();
  const cleanScopeKey = asString(scopeKey).trim();
  const reportMs = parseISO(reportAt)?.getTime() || nowDate(tx).getTime();
  const dedupeMs = Number(windowMs) || 0;
  if (!cleanKind || !cleanStableId || !cleanScopeKey || dedupeMs <= 0) {
    return false;
  }
  const existing = tx.db.satiksmebot_report_dedupe.id.find(reportDedupeID(cleanKind, cleanStableId, cleanScopeKey));
  if (!existing) {
    return false;
  }
  const lastMs = parseISO(asString(existing.lastReportAt))?.getTime() || 0;
  return lastMs > reportMs - dedupeMs;
}

function claimReportDedupe(tx: any, reportKind: string, stableId: string, scopeKey: string, reportAt: string, windowMs: number): boolean {
  const cleanKind = asString(reportKind).trim();
  const cleanStableId = asString(stableId).trim();
  const cleanScopeKey = asString(scopeKey).trim();
  const id = reportDedupeID(cleanKind, cleanStableId, cleanScopeKey);
  const reportMs = parseISO(reportAt)?.getTime() || nowDate(tx).getTime();
  const dedupeMs = Number(windowMs) || 0;
  if (!cleanKind || !cleanStableId || !cleanScopeKey || dedupeMs <= 0) {
    return true;
  }
  const existing = tx.db.satiksmebot_report_dedupe.id.find(id);
  if (existing) {
    const lastMs = parseISO(asString(existing.lastReportAt))?.getTime() || 0;
    if (lastMs > reportMs - dedupeMs) {
      return false;
    }
    tx.db.satiksmebot_report_dedupe.id.delete(id);
  }
  tx.db.satiksmebot_report_dedupe.insert({
    id,
    reportKind: cleanKind,
    stableId: cleanStableId,
    scopeKey: cleanScopeKey,
    lastReportAt: reportAt,
  });
  return true;
}

function recordIncidentVoteAction(tx: any, voteItem: any, eventItem: any): any {
  const nextVote = sanitizeIncidentVote(tx, voteItem);
  const nextEvent = sanitizeIncidentVoteEvent(tx, eventItem);
  tx.db.satiksmebot_incident_vote.id.delete(nextVote.id);
  tx.db.satiksmebot_incident_vote.insert(nextVote);
  tx.db.satiksmebot_incident_vote_event.id.delete(nextEvent.id);
  tx.db.satiksmebot_incident_vote_event.insert(nextEvent);
  return nextVote;
}

function recordReporterIncidentVote(tx: any, reporter: any, incidentId: string, value: string, source: string, eventId: string, createdAt: string): any {
  const stableId = asString(reporter.stableId).trim();
  const existing = currentIncidentVote(tx, incidentId, stableId);
  return recordIncidentVoteAction(tx, {
    incidentId,
    stableId,
    userId: asString(reporter.userId).trim(),
    nickname: asString(reporter.nickname).trim(),
    value,
    createdAt: trimOptional(asString(existing?.createdAt)) || createdAt,
    updatedAt: createdAt,
  }, {
    id: eventId,
    incidentId,
    stableId,
    userId: asString(reporter.userId).trim(),
    nickname: asString(reporter.nickname).trim(),
    value,
    source,
    createdAt,
  });
}

function hasInvalidReporterIdentity(row: any): boolean {
  const stableId = asString(row?.stableId).trim();
  const userId = asString(row?.userId).trim();
  return !validTelegramStableId(stableId) || !validTelegramUserId(userId) || stableId !== `telegram:${userId}`;
}

function hasInvalidReporterStableId(row: any): boolean {
  const stableId = asString(row?.stableId).trim();
  return !validTelegramStableId(stableId);
}

function cleanupInvalidReporterState(tx: any): void {
  for (const row of rowsFrom(tx.db.satiksmebot_stop_sighting.iter())) {
    if (hasInvalidReporterIdentity(row)) {
      tx.db.satiksmebot_stop_sighting.id.delete(asString(row.id).trim());
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_vehicle_sighting.iter())) {
    if (hasInvalidReporterIdentity(row)) {
      tx.db.satiksmebot_vehicle_sighting.id.delete(asString(row.id).trim());
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_area_report.iter())) {
    if (hasInvalidReporterIdentity(row)) {
      tx.db.satiksmebot_area_report.id.delete(asString(row.id).trim());
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_incident_vote.iter())) {
    if (hasInvalidReporterIdentity(row)) {
      tx.db.satiksmebot_incident_vote.id.delete(asString(row.id).trim());
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_incident_vote_event.iter())) {
    if (hasInvalidReporterIdentity(row)) {
      tx.db.satiksmebot_incident_vote_event.id.delete(asString(row.id).trim());
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_incident_comment.iter())) {
    if (hasInvalidReporterIdentity(row)) {
      tx.db.satiksmebot_incident_comment.id.delete(asString(row.id).trim());
    }
  }
  for (const row of rowsFrom(tx.db.satiksmebot_report_dedupe.iter())) {
    if (hasInvalidReporterStableId(row)) {
      tx.db.satiksmebot_report_dedupe.id.delete(asString(row.id).trim());
    }
  }
}

function refreshPublicProjections(tx: any): void {
  cleanupInvalidReporterState(tx);
  for (const row of rowsFrom(tx.db.satiksmebot_public_stop_sighting.iter())) {
    tx.db.satiksmebot_public_stop_sighting.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_vehicle_sighting.iter())) {
    tx.db.satiksmebot_public_vehicle_sighting.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_area_report.iter())) {
    tx.db.satiksmebot_public_area_report.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_incident.iter())) {
    tx.db.satiksmebot_public_incident.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_incident_event.iter())) {
    tx.db.satiksmebot_public_incident_event.id.delete(row.id);
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_incident_comment.iter())) {
    tx.db.satiksmebot_public_incident_comment.id.delete(row.id);
  }

  const nowMs = nowDate(tx).getTime();
  const visibleSinceMs = nowMs - VISIBLE_SIGHTING_WINDOW_MS;
  const incidentSinceMs = nowMs - INCIDENT_LOOKBACK_MS;
  const incidents = new Map<string, any>();

  for (const row of rowsFrom(tx.db.satiksmebot_stop_sighting.iter())) {
    const sourceCreatedAt = asString(row.createdAt).trim();
    const createdAt = publicTimestamp(sourceCreatedAt);
    const createdMs = parseISO(sourceCreatedAt)?.getTime() || 0;
    if (row.hidden === true || createdMs < incidentSinceMs) {
      continue;
    }
    const stopId = asString(row.stopId).trim();
    const stop = stopCatalogRow(tx, stopId);
    const stopName = asString(stop?.name).trim();
    const incidentId = stopIncidentID(stopId);
    const event = {
      id: asString(row.id).trim(),
      kind: 'report',
      name: incidentVoteLabel('ONGOING'),
      nickname: publicIncidentNickname(),
      createdAt,
    };
    const current = incidents.get(incidentId) || {
      id: incidentId,
      scope: 'stop',
      subjectId: stopId,
      subjectName: stopName || stopId,
      stopId,
      lastReportName: '',
      lastReportAt: '',
      lastReporter: '',
      vehicle: null,
      area: null,
      events: [],
    };
    current.events.push(event);
    if (!current.lastReportAt || compareTimeDescending(createdAt, current.lastReportAt) < 0) {
      current.lastReportName = event.name;
      current.lastReportAt = createdAt;
      current.lastReporter = publicIncidentNickname();
      current.subjectName = stopName || stopId;
    }
    incidents.set(incidentId, current);
    if (createdMs >= visibleSinceMs) {
      tx.db.satiksmebot_public_stop_sighting.insert({
        id: publicStopSightingID(asString(row.id).trim()),
        incidentId,
        stopId,
        stopName: stopName || stopId,
        createdAt,
      });
    }
  }

  for (const row of rowsFrom(tx.db.satiksmebot_vehicle_sighting.iter())) {
    const sourceCreatedAt = asString(row.createdAt).trim();
    const createdAt = publicTimestamp(sourceCreatedAt);
    const createdMs = parseISO(sourceCreatedAt)?.getTime() || 0;
    if (row.hidden === true || createdMs < incidentSinceMs) {
      continue;
    }
    const stopId = asString(row.stopId).trim();
    const stop = stopCatalogRow(tx, stopId);
    const stopName = asString(stop?.name).trim();
    const storedScopeKey = asString(row.scopeKey).trim() || vehicleScopeKey(row);
    const incidentId = vehicleIncidentID(storedScopeKey);
    const event = {
      id: asString(row.id).trim(),
      kind: 'report',
      name: incidentVoteLabel('ONGOING'),
      nickname: publicIncidentNickname(),
      createdAt,
    };
    const vehicle = publicVehicleContext(row, stopName);
    const current = incidents.get(incidentId) || {
      id: incidentId,
      scope: 'vehicle',
      subjectId: '',
      subjectName: vehicleIncidentSubjectName(row, stopName),
      stopId,
      lastReportName: '',
      lastReportAt: '',
      lastReporter: '',
      vehicle,
      area: null,
      events: [],
    };
    current.events.push(event);
    if (!current.lastReportAt || compareTimeDescending(createdAt, current.lastReportAt) < 0) {
      current.lastReportName = event.name;
      current.lastReportAt = createdAt;
      current.lastReporter = publicIncidentNickname();
      current.subjectName = vehicleIncidentSubjectName(row, stopName);
      current.stopId = stopId;
      current.vehicle = vehicle;
    }
    incidents.set(incidentId, current);
    if (createdMs >= visibleSinceMs) {
      tx.db.satiksmebot_public_vehicle_sighting.insert({
        id: publicVehicleSightingID(asString(row.id).trim()),
        incidentId,
        stopId,
        stopName,
        mode: asString(row.mode).trim(),
        routeLabel: asString(row.routeLabel).trim(),
        direction: normalizeDirection(asString(row.direction)),
        destination: asString(row.destination).trim(),
        departureSeconds: Number(row.departureSeconds) || 0,
        liveRowId: '',
        createdAt,
      });
    }
  }

  for (const row of rowsFrom(tx.db.satiksmebot_area_report.iter())) {
    const sourceCreatedAt = asString(row.createdAt).trim();
    const createdAt = publicTimestamp(sourceCreatedAt);
    const createdMs = parseISO(sourceCreatedAt)?.getTime() || 0;
    if (row.hidden === true || createdMs < incidentSinceMs) {
      continue;
    }
    const scopeKey = asString(row.scopeKey).trim() || areaScopeKey(row);
    const incidentId = areaIncidentID(scopeKey);
    const event = {
      id: asString(row.id).trim(),
      kind: 'report',
      name: incidentVoteLabel('ONGOING'),
      nickname: publicIncidentNickname(),
      createdAt,
    };
    const area = publicAreaContext(row);
    const current = incidents.get(incidentId) || {
      id: incidentId,
      scope: 'area',
      subjectId: '',
      subjectName: areaIncidentSubjectName(row),
      stopId: '',
      lastReportName: '',
      lastReportAt: '',
      lastReporter: '',
      vehicle: null,
      area,
      events: [],
    };
    current.events.push(event);
    if (!current.lastReportAt || compareTimeDescending(createdAt, current.lastReportAt) < 0) {
      current.lastReportName = event.name;
      current.lastReportAt = createdAt;
      current.lastReporter = publicIncidentNickname();
      current.subjectName = areaIncidentSubjectName(row);
      current.stopId = '';
      current.vehicle = null;
      current.area = area;
    }
    incidents.set(incidentId, current);
    if (createdMs >= visibleSinceMs) {
      tx.db.satiksmebot_public_area_report.insert(publicAreaReportPayload({
        ...row,
        incidentId,
        createdAt,
      }));
    }
  }

  for (const incident of Array.from(incidents.values())) {
    const latestVotes = latestVoteMap(tx, incident.id, incidentSinceMs);
    const votes = Array.from(latestVotes.values()).sort((left, right) => compareTimeDescending(asString(left.updatedAt), asString(right.updatedAt)));
    const comments = rowsFrom(tx.db.satiksmebot_incident_comment.incidentId.filter(incident.id))
      .filter((row) => (parseISO(asString(row.createdAt))?.getTime() || 0) >= incidentSinceMs)
      .sort((left, right) => compareTimeAscending(asString(left.createdAt), asString(right.createdAt)));
    const voteEvents = rowsFrom(tx.db.satiksmebot_incident_vote_event.incidentId.filter(incident.id))
      .filter((row) => (parseISO(asString(row.createdAt))?.getTime() || 0) >= incidentSinceMs)
      .sort((left, right) => compareTimeAscending(asString(left.createdAt), asString(right.createdAt)));
    const seenEventIds = new Set<string>();
    const unifiedEvents: any[] = [];
    for (const event of voteEvents) {
      const id = asString(event.id).trim();
      seenEventIds.add(id);
      unifiedEvents.push({
        id,
        kind: asString(event.source).trim(),
        name: incidentVoteLabel(asString(event.value)),
        nickname: publicIncidentNickname(),
        createdAt: publicTimestamp(event.createdAt),
      });
    }
    for (const event of incident.events) {
      const id = asString(event.id).trim();
      if (seenEventIds.has(id)) {
        continue;
      }
      unifiedEvents.push({
        ...event,
        name: incidentVoteLabel('ONGOING'),
      });
    }
    let ongoingVotes = 0;
    let clearedVotes = 0;
    for (const vote of votes) {
      if (asString(vote.value).trim().toUpperCase() === 'ONGOING') {
        ongoingVotes += 1;
      } else if (asString(vote.value).trim().toUpperCase() === 'CLEARED') {
        clearedVotes += 1;
      }
    }
    const resolved = clearedVotes >= INCIDENT_RESOLVED_VOTE_COUNT;
    const active = !resolved;
    unifiedEvents.sort((left: any, right: any) => compareTimeAscending(asString(left.createdAt), asString(right.createdAt)));
    if (voteEvents.length > 0) {
      const latestEvent = voteEvents.slice().sort((left: any, right: any) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))[0];
      incident.lastReportName = incidentVoteLabel(asString(latestEvent.value));
      incident.lastReportAt = publicTimestamp(latestEvent.createdAt);
      incident.lastReporter = publicIncidentNickname();
    }
    tx.db.satiksmebot_public_incident.insert({
      id: incident.id,
      scope: incident.scope,
      subjectId: incident.subjectId,
      subjectName: incident.subjectName,
      stopId: incident.stopId,
      lastReportName: incident.lastReportName,
      lastReportAt: publicTimestamp(incident.lastReportAt),
      lastReporter: publicIncidentNickname(),
      commentCount: comments.length,
      ongoingVotes,
      clearedVotes,
      active,
      resolved,
      vehicle: incident.vehicle || undefined,
      area: publicAreaContext(incident.area) || undefined,
    });
    for (const event of unifiedEvents) {
      tx.db.satiksmebot_public_incident_event.insert({
        id: publicIncidentEventID(incident.id, event.id, event.createdAt),
        incidentId: incident.id,
        kind: event.kind,
        name: event.name,
        nickname: publicIncidentNickname(),
        createdAt: publicTimestamp(event.createdAt),
      });
    }
    for (const comment of comments) {
      tx.db.satiksmebot_public_incident_comment.insert({
        id: publicIncidentCommentID(incident.id, asString(comment.id).trim(), publicTimestamp(comment.createdAt)),
        incidentId: incident.id,
        nickname: publicIncidentNickname(),
        body: asString(comment.body).trim(),
        createdAt: publicTimestamp(comment.createdAt),
      });
    }
  }
}

function incidentSummaryPayload(tx: any, row: any, viewerStableId: string) {
  const latestVotes = latestVoteMap(tx, asString(row.id).trim(), nowDate(tx).getTime() - INCIDENT_LOOKBACK_MS);
  let userValue = '';
  if (viewerStableId) {
    const current = latestVotes.get(viewerStableId);
    userValue = asString(current?.value).trim();
  }
  return {
    id: asString(row.id).trim(),
    scope: asString(row.scope).trim(),
    subjectId: asString(row.subjectId).trim(),
    subjectName: asString(row.subjectName).trim(),
    stopId: asString(row.stopId).trim(),
    lastReportName: asString(row.lastReportName).trim(),
    lastReportAt: publicTimestamp(row.lastReportAt),
    lastReporter: publicIncidentNickname(),
    commentCount: Number(row.commentCount) || 0,
    votes: {
      ongoing: Number(row.ongoingVotes) || 0,
      cleared: Number(row.clearedVotes) || 0,
      userValue,
    },
    active: row.active === true,
    resolved: row.resolved === true,
    vehicle: row.vehicle || undefined,
    area: incidentAreaContext(row.area),
  };
}

function visibleSightingsPayload(tx: any, stopId: string, limit: number) {
  const cleanStopId = stopId.trim();
  let stopSightings = rowsFrom(tx.db.satiksmebot_public_stop_sighting.iter())
    .filter((row) => !cleanStopId || asString(row.stopId).trim() === cleanStopId)
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map((row) => ({
      id: asString(row.id).trim(),
      stopId: asString(row.stopId).trim(),
      stopName: asString(row.stopName).trim(),
      createdAt: publicTimestamp(row.createdAt),
    }));
  let vehicleSightings = rowsFrom(tx.db.satiksmebot_public_vehicle_sighting.iter())
    .filter((row) => !cleanStopId || asString(row.stopId).trim() === cleanStopId)
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map((row) => ({
      id: asString(row.id).trim(),
      stopId: asString(row.stopId).trim(),
      stopName: asString(row.stopName).trim(),
      mode: asString(row.mode).trim(),
      routeLabel: asString(row.routeLabel).trim(),
      direction: asString(row.direction).trim(),
      destination: asString(row.destination).trim(),
      departureSeconds: Number(row.departureSeconds) || 0,
      createdAt: publicTimestamp(row.createdAt),
    }));
  let areaReports = cleanStopId ? [] : rowsFrom(tx.db.satiksmebot_public_area_report.iter())
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map((row) => publicAreaReportPayload(row));
  if (limit > 0) {
    stopSightings = stopSightings.slice(0, limit);
    vehicleSightings = vehicleSightings.slice(0, limit);
    areaReports = areaReports.slice(0, limit);
  }
  return {
    stopSightings,
    vehicleSightings,
    areaReports,
  };
}

function userSightingsPayload(tx: any, stableId: string, stopId: string, limit: number) {
  const sinceMs = nowDate(tx).getTime() - VISIBLE_SIGHTING_WINDOW_MS;
  const cleanStopId = stopId.trim();
  let stopSightings = rowsFrom(tx.db.satiksmebot_stop_sighting.stableId.filter(stableId))
    .filter((row) => {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < sinceMs) {
        return false;
      }
      if (cleanStopId && asString(row.stopId).trim() !== cleanStopId) {
        return false;
      }
      return true;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map((row) => ({
      id: asString(row.id).trim(),
      stopId: asString(row.stopId).trim(),
      stopName: asString(stopCatalogRow(tx, asString(row.stopId).trim())?.name).trim(),
      createdAt: asString(row.createdAt).trim(),
    }));
  let vehicleSightings = rowsFrom(tx.db.satiksmebot_vehicle_sighting.stableId.filter(stableId))
    .filter((row) => {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < sinceMs) {
        return false;
      }
      if (cleanStopId && asString(row.stopId).trim() !== cleanStopId) {
        return false;
      }
      return true;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map((row) => ({
      id: asString(row.id).trim(),
      stopId: asString(row.stopId).trim(),
      stopName: asString(stopCatalogRow(tx, asString(row.stopId).trim())?.name).trim(),
      mode: asString(row.mode).trim(),
      routeLabel: asString(row.routeLabel).trim(),
      direction: asString(row.direction).trim(),
      destination: asString(row.destination).trim(),
      departureSeconds: Number(row.departureSeconds) || 0,
      liveRowId: asString(row.liveRowId).trim(),
      createdAt: asString(row.createdAt).trim(),
    }));
  let areaReports = cleanStopId ? [] : rowsFrom(tx.db.satiksmebot_area_report.stableId.filter(stableId))
    .filter((row) => {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      return createdMs >= sinceMs;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map((row) => ({
      id: asString(row.id).trim(),
      incidentId: areaIncidentID(asString(row.scopeKey).trim()),
      latitude: Number(row.latitude) || 0,
      longitude: Number(row.longitude) || 0,
      radiusMeters: Number(row.radiusMeters) || 0,
      description: asString(row.description).trim(),
      createdAt: asString(row.createdAt).trim(),
    }));
  if (limit > 0) {
    stopSightings = stopSightings.slice(0, limit);
    vehicleSightings = vehicleSightings.slice(0, limit);
    areaReports = areaReports.slice(0, limit);
  }
  return { stopSightings, vehicleSightings, areaReports };
}

function latestStopSightingFor(tx: any, stableId: string, stopId: string): any | null {
  const items = rowsFrom(tx.db.satiksmebot_stop_sighting.stableId.filter(stableId))
    .filter((row) => asString(row.stopId).trim() === stopId.trim())
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)));
  return items[0] || null;
}

function latestVehicleSightingFor(tx: any, stableId: string, scopeKey: string): any | null {
  const items = rowsFrom(tx.db.satiksmebot_vehicle_sighting.stableId.filter(stableId))
    .filter((row) => asString(row.scopeKey).trim() === scopeKey.trim())
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)));
  return items[0] || null;
}

function latestAreaReportFor(tx: any, stableId: string, scopeKey: string): any | null {
  const items = rowsFrom(tx.db.satiksmebot_area_report.stableId.filter(stableId))
    .filter((row) => asString(row.scopeKey).trim() === scopeKey.trim())
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)));
  return items[0] || null;
}

function submitStopReportImpl(tx: any, stopId: string, bundleVersion: string, bundleGeneratedAt: string) {
  const reporter = ensureReporter(tx);
  requireActiveBundle(tx, bundleVersion, bundleGeneratedAt);
  requireStopCatalogRow(tx, stopId);
  const incidentId = stopIncidentID(stopId);
  const stableId = asString(reporter.stableId).trim();
  const createdAt = nowISO(tx);
  if (reporter.smoke !== true) {
    if (reportDedupeClaimActive(tx, 'stop', stableId, stopId, createdAt, REPORT_DEDUPE_WINDOW_MS)) {
      return serialize({ accepted: false, deduped: true, incidentId });
    }
    const sameVoteCooldown = sameVoteCooldownSeconds(tx, incidentId, stableId, 'ONGOING');
    if (sameVoteCooldown > 0) {
      return serialize({ accepted: false, rateLimited: true, reason: 'same_vote', cooldownSeconds: sameVoteCooldown, incidentId });
    }
    if (countMapReportsForStableIdSince(tx, stableId, nowDate(tx).getTime() - MAP_REPORT_WINDOW_MS) >= MAP_REPORT_LIMIT) {
      return serialize({ accepted: false, rateLimited: true, reason: 'map_report_limit', cooldownSeconds: Math.ceil(MAP_REPORT_WINDOW_MS / 1000), incidentId });
    }
    if (!claimReportDedupe(tx, 'stop', stableId, stopId, createdAt, REPORT_DEDUPE_WINDOW_MS)) {
      return serialize({ accepted: false, deduped: true, incidentId });
    }
  }
  const sighting = {
    id: randomId(tx, 'stop'),
    stopId: stopId.trim(),
    stableId,
    userId: asString(reporter.userId).trim(),
    hidden: reporter.smoke === true,
    createdAt,
  };
  tx.db.satiksmebot_stop_sighting.insert(sighting);
  if (sighting.hidden !== true) {
    recordReporterIncidentVote(tx, reporter, incidentId, 'ONGOING', 'map_report', sighting.id, createdAt);
  }
  refreshPublicProjections(tx);
  return serialize({ accepted: true, deduped: false, incidentId });
}

function submitVehicleReportImpl(tx: any, payload: any, bundleVersion: string, bundleGeneratedAt: string) {
  const reporter = ensureReporter(tx);
  requireActiveBundle(tx, bundleVersion, bundleGeneratedAt);
  const stopId = asString(payload?.stopId).trim();
  if (stopId) {
    requireStopCatalogRow(tx, stopId);
  }
  const logicalScopeKey = vehicleScopeKey(payload);
  ensureVehiclePublicIDAvailable(tx, logicalScopeKey);
  const scopeKey = opaqueVehicleScopeKey(logicalScopeKey);
  const incidentId = vehicleIncidentID(scopeKey);
  const stableId = asString(reporter.stableId).trim();
  const createdAt = nowISO(tx);
  if (reporter.smoke !== true) {
    if (reportDedupeClaimActive(tx, 'vehicle', stableId, scopeKey, createdAt, REPORT_DEDUPE_WINDOW_MS)) {
      return serialize({ accepted: false, deduped: true, incidentId });
    }
    const sameVoteCooldown = sameVoteCooldownSeconds(tx, incidentId, stableId, 'ONGOING');
    if (sameVoteCooldown > 0) {
      return serialize({ accepted: false, rateLimited: true, reason: 'same_vote', cooldownSeconds: sameVoteCooldown, incidentId });
    }
    if (countMapReportsForStableIdSince(tx, stableId, nowDate(tx).getTime() - MAP_REPORT_WINDOW_MS) >= MAP_REPORT_LIMIT) {
      return serialize({ accepted: false, rateLimited: true, reason: 'map_report_limit', cooldownSeconds: Math.ceil(MAP_REPORT_WINDOW_MS / 1000), incidentId });
    }
    if (!claimReportDedupe(tx, 'vehicle', stableId, scopeKey, createdAt, REPORT_DEDUPE_WINDOW_MS)) {
      return serialize({ accepted: false, deduped: true, incidentId });
    }
  }
  const sighting = {
    id: randomId(tx, 'vehicle'),
    stopId,
    stableId,
    userId: asString(reporter.userId).trim(),
    mode: asString(payload?.mode).trim(),
    routeLabel: asString(payload?.routeLabel).trim(),
    direction: asString(payload?.direction).trim(),
    destination: asString(payload?.destination).trim(),
    departureSeconds: asInt(payload?.departureSeconds),
    liveRowId: asString(payload?.liveRowId).trim(),
    scopeKey,
    hidden: reporter.smoke === true,
    createdAt,
  };
  tx.db.satiksmebot_vehicle_sighting.insert(sighting);
  if (sighting.hidden !== true) {
    recordReporterIncidentVote(tx, reporter, incidentId, 'ONGOING', 'map_report', sighting.id, createdAt);
  }
  refreshPublicProjections(tx);
  return serialize({ accepted: true, deduped: false, incidentId });
}

function submitAreaReportImpl(tx: any, payload: any, bundleVersion: string, bundleGeneratedAt: string) {
  const reporter = ensureReporter(tx);
  requireActiveBundle(tx, bundleVersion, bundleGeneratedAt);
  const normalized = normalizeAreaReportInput(payload);
  const scopeKey = areaScopeKey(normalized);
  const incidentId = areaIncidentID(scopeKey);
  const stableId = asString(reporter.stableId).trim();
  const createdAt = nowISO(tx);
  if (reporter.smoke !== true) {
    if (reportDedupeClaimActive(tx, 'area', stableId, scopeKey, createdAt, REPORT_DEDUPE_WINDOW_MS)) {
      return serialize({ accepted: false, deduped: true, incidentId });
    }
    const sameVoteCooldown = sameVoteCooldownSeconds(tx, incidentId, stableId, 'ONGOING');
    if (sameVoteCooldown > 0) {
      return serialize({ accepted: false, rateLimited: true, reason: 'same_vote', cooldownSeconds: sameVoteCooldown, incidentId });
    }
    if (countMapReportsForStableIdSince(tx, stableId, nowDate(tx).getTime() - MAP_REPORT_WINDOW_MS) >= MAP_REPORT_LIMIT) {
      return serialize({ accepted: false, rateLimited: true, reason: 'map_report_limit', cooldownSeconds: Math.ceil(MAP_REPORT_WINDOW_MS / 1000), incidentId });
    }
    if (!claimReportDedupe(tx, 'area', stableId, scopeKey, createdAt, REPORT_DEDUPE_WINDOW_MS)) {
      return serialize({ accepted: false, deduped: true, incidentId });
    }
  }
  const report = {
    id: randomId(tx, 'area'),
    stableId,
    userId: asString(reporter.userId).trim(),
    latitude: normalized.latitude,
    longitude: normalized.longitude,
    radiusMeters: normalized.radiusMeters,
    description: normalized.description,
    scopeKey,
    hidden: reporter.smoke === true,
    createdAt,
  };
  tx.db.satiksmebot_area_report.insert(report);
  if (report.hidden !== true) {
    recordReporterIncidentVote(tx, reporter, incidentId, 'ONGOING', 'map_report', report.id, createdAt);
  }
  refreshPublicProjections(tx);
  return serialize({ accepted: true, deduped: false, incidentId });
}

function serviceDedupeWindowMs(dedupeSeconds: number): number {
  const seconds = Number(dedupeSeconds) || 0;
  return seconds > 0 ? seconds * 1000 : 0;
}

function recordServiceStopSightingWithVotePayload(tx: any, sightingJson: string, voteJson: string, eventJson: string, dedupeSeconds: number) {
  const sighting = sanitizeStopSighting(tx, parseJSON(sightingJson, 'invalid stop sighting'));
  const vote = sanitizeIncidentVote(tx, parseJSON(voteJson, 'invalid vote'));
  const event = sanitizeIncidentVoteEvent(tx, parseJSON(eventJson, 'invalid vote event'));
  validateServiceReportVotePair(sighting, vote, event, stopIncidentID(asString(sighting.stopId).trim()));
  if (tx.db.satiksmebot_stop_sighting.id.find(sighting.id)) {
    return { deduped: true, reason: 'idempotent_replay' };
  }
  if (sighting.hidden !== true) {
    const stableId = asString(sighting.stableId).trim();
    const createdAt = asString(sighting.createdAt).trim();
    const dedupeMs = serviceDedupeWindowMs(dedupeSeconds);
    if (reportDedupeClaimActive(tx, 'stop', stableId, asString(sighting.stopId).trim(), createdAt, dedupeMs)) {
      return { deduped: true, reason: 'duplicate_report' };
    }
    if (sameVoteCooldownSeconds(tx, asString(vote.incidentId).trim(), stableId, asString(vote.value).trim()) > 0) {
      return { deduped: true, reason: 'same_vote' };
    }
    if (countMapReportsForStableIdSince(tx, stableId, nowDate(tx).getTime() - MAP_REPORT_WINDOW_MS) >= MAP_REPORT_LIMIT) {
      return { deduped: true, reason: 'map_report_limit' };
    }
    if (!claimReportDedupe(tx, 'stop', stableId, asString(sighting.stopId).trim(), createdAt, dedupeMs)) {
      return { deduped: true, reason: 'duplicate_report' };
    }
  }
  tx.db.satiksmebot_stop_sighting.id.delete(sighting.id);
  tx.db.satiksmebot_stop_sighting.insert(sighting);
  if (sighting.hidden !== true) {
    recordIncidentVoteAction(tx, vote, event);
  }
  refreshPublicProjections(tx);
  return { deduped: false };
}

function recordServiceVehicleSightingWithVotePayload(tx: any, sightingJson: string, voteJson: string, eventJson: string, dedupeSeconds: number) {
  const sighting = sanitizeVehicleSighting(tx, parseJSON(sightingJson, 'invalid vehicle sighting'));
  const vote = sanitizeIncidentVote(tx, parseJSON(voteJson, 'invalid vote'));
  const event = sanitizeIncidentVoteEvent(tx, parseJSON(eventJson, 'invalid vote event'));
  validateServiceReportVotePair(sighting, vote, event, vehicleIncidentID(asString(sighting.scopeKey).trim()));
  if (tx.db.satiksmebot_vehicle_sighting.id.find(sighting.id)) {
    return { deduped: true, reason: 'idempotent_replay' };
  }
  if (sighting.hidden !== true) {
    const stableId = asString(sighting.stableId).trim();
    const scopeKey = asString(sighting.scopeKey).trim();
    const createdAt = asString(sighting.createdAt).trim();
    const dedupeMs = serviceDedupeWindowMs(dedupeSeconds);
    if (reportDedupeClaimActive(tx, 'vehicle', stableId, scopeKey, createdAt, dedupeMs)) {
      return { deduped: true, reason: 'duplicate_report' };
    }
    if (sameVoteCooldownSeconds(tx, asString(vote.incidentId).trim(), stableId, asString(vote.value).trim()) > 0) {
      return { deduped: true, reason: 'same_vote' };
    }
    if (countMapReportsForStableIdSince(tx, stableId, nowDate(tx).getTime() - MAP_REPORT_WINDOW_MS) >= MAP_REPORT_LIMIT) {
      return { deduped: true, reason: 'map_report_limit' };
    }
    if (!claimReportDedupe(tx, 'vehicle', stableId, scopeKey, createdAt, dedupeMs)) {
      return { deduped: true, reason: 'duplicate_report' };
    }
  }
  tx.db.satiksmebot_vehicle_sighting.id.delete(sighting.id);
  tx.db.satiksmebot_vehicle_sighting.insert(sighting);
  if (sighting.hidden !== true) {
    recordIncidentVoteAction(tx, vote, event);
  }
  refreshPublicProjections(tx);
  return { deduped: false };
}

function recordServiceAreaReportWithVotePayload(tx: any, reportJson: string, voteJson: string, eventJson: string, dedupeSeconds: number) {
  const report = sanitizeAreaReport(tx, parseJSON(reportJson, 'invalid area report'));
  const vote = sanitizeIncidentVote(tx, parseJSON(voteJson, 'invalid vote'));
  const event = sanitizeIncidentVoteEvent(tx, parseJSON(eventJson, 'invalid vote event'));
  validateServiceReportVotePair(report, vote, event, areaIncidentID(asString(report.scopeKey).trim()));
  if (tx.db.satiksmebot_area_report.id.find(report.id)) {
    return { deduped: true, reason: 'idempotent_replay' };
  }
  if (report.hidden !== true) {
    const stableId = asString(report.stableId).trim();
    const scopeKey = asString(report.scopeKey).trim();
    const createdAt = asString(report.createdAt).trim();
    const dedupeMs = serviceDedupeWindowMs(dedupeSeconds);
    if (reportDedupeClaimActive(tx, 'area', stableId, scopeKey, createdAt, dedupeMs)) {
      return { deduped: true, reason: 'duplicate_report' };
    }
    if (sameVoteCooldownSeconds(tx, asString(vote.incidentId).trim(), stableId, asString(vote.value).trim()) > 0) {
      return { deduped: true, reason: 'same_vote' };
    }
    if (countMapReportsForStableIdSince(tx, stableId, nowDate(tx).getTime() - MAP_REPORT_WINDOW_MS) >= MAP_REPORT_LIMIT) {
      return { deduped: true, reason: 'map_report_limit' };
    }
    if (!claimReportDedupe(tx, 'area', stableId, scopeKey, createdAt, dedupeMs)) {
      return { deduped: true, reason: 'duplicate_report' };
    }
  }
  tx.db.satiksmebot_area_report.id.delete(report.id);
  tx.db.satiksmebot_area_report.insert(report);
  if (report.hidden !== true) {
    recordIncidentVoteAction(tx, vote, event);
  }
  refreshPublicProjections(tx);
  return { deduped: false };
}

function validateServiceReportVotePair(sighting: any, vote: any, event: any, incidentId: string): void {
  const stableId = asString(sighting.stableId).trim();
  const userId = asString(sighting.userId).trim();
  if (asString(vote.stableId).trim() !== stableId || asString(event.stableId).trim() !== stableId) {
    throw new SenderError('report and vote stableId mismatch');
  }
  if (asString(vote.userId).trim() !== userId || asString(event.userId).trim() !== userId) {
    throw new SenderError('report and vote userId mismatch');
  }
  if (asString(vote.incidentId).trim() !== incidentId || asString(event.incidentId).trim() !== incidentId) {
    throw new SenderError('report and vote incident mismatch');
  }
  if (asString(vote.value).trim() !== asString(event.value).trim()) {
    throw new SenderError('report and vote value mismatch');
  }
}

function clearLegacyPublicViewerHeartbeats(tx: any): void {
  for (const row of rowsFrom(tx.db.satiksmebot_live_viewer_heartbeat.iter())) {
    tx.db.satiksmebot_live_viewer_heartbeat.sessionId.delete(asString(row.sessionId).trim());
  }
}

function scrubLegacyPublicCompatibilityFields(tx: any): void {
  clearLegacyPublicViewerHeartbeats(tx);
  for (const row of rowsFrom(tx.db.satiksmebot_stop_catalog.iter())) {
    if (asString(row.liveId) === '' && rowsFrom(row.nearbyStopIds || []).length === 0) {
      continue;
    }
    tx.db.satiksmebot_stop_catalog.id.delete(asString(row.id).trim());
    tx.db.satiksmebot_stop_catalog.insert({
      ...row,
      liveId: '',
      nearbyStopIds: [],
    });
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_vehicle_sighting.iter())) {
    if (asString(row.liveRowId) === '') {
      continue;
    }
    tx.db.satiksmebot_public_vehicle_sighting.id.delete(asString(row.id).trim());
    tx.db.satiksmebot_public_vehicle_sighting.insert({
      ...row,
      liveRowId: '',
    });
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_incident.iter())) {
    const vehicle = row.vehicle
      ? { ...row.vehicle, scopeKey: '', liveRowId: '' }
      : undefined;
    const area = row.area
      ? { ...row.area, scopeKey: '' }
      : undefined;
    const dirtyVehicle = asString(row.vehicle?.scopeKey) !== '' || asString(row.vehicle?.liveRowId) !== '';
    const dirtyArea = asString(row.area?.scopeKey) !== '';
    if (!dirtyVehicle && !dirtyArea) {
      continue;
    }
    tx.db.satiksmebot_public_incident.id.delete(asString(row.id).trim());
    tx.db.satiksmebot_public_incident.insert({
      ...row,
      vehicle,
      area,
    });
  }
  for (const row of rowsFrom(tx.db.satiksmebot_public_live_snapshot_state.iter())) {
    const dirty = asString(row.hash) !== ''
      || asString(row.publishedAt) !== ''
      || asString(row.lastSuccessAt) !== ''
      || asString(row.lastAttemptAt) !== ''
      || asString(row.status) !== ''
      || Number(row.consecutiveFailures) !== 0
      || Number(row.vehicleCount) !== 0;
    if (!dirty) {
      continue;
    }
    tx.db.satiksmebot_public_live_snapshot_state.feed.delete(asString(row.feed).trim());
    tx.db.satiksmebot_public_live_snapshot_state.insert({
      ...row,
      hash: '',
      publishedAt: '',
      lastSuccessAt: '',
      lastAttemptAt: '',
      status: '',
      consecutiveFailures: 0,
      vehicleCount: 0,
    });
  }
}

function upsertLiveViewerPayload(tx: any, sessionId: string, page: string, visible: boolean) {
  const cleanSessionId = asString(sessionId).trim();
  if (!cleanSessionId) {
    throw new SenderError('sessionId is required');
  }
  const cleanPage = asString(page).trim() || 'map';
  const updatedAt = nowISO(tx);
  clearLegacyPublicViewerHeartbeats(tx);
  tx.db.satiksmebot_live_viewer_state.sessionId.delete(cleanSessionId);
  tx.db.satiksmebot_live_viewer_state.insert({
    sessionId: cleanSessionId,
    page: cleanPage,
    visible: visible === true,
    updatedAt,
  });
  return {
    sessionId: cleanSessionId,
    page: cleanPage,
    lastSeenAt: updatedAt,
    visible: visible === true,
    updatedAt,
  };
}

function heartbeatLiveViewerPayload(tx: any, sessionId: string, page: string) {
  return upsertLiveViewerPayload(tx, sessionId, page, true);
}

function setLiveViewerStatePayload(tx: any, sessionId: string, page: string, visible: boolean) {
  return upsertLiveViewerPayload(tx, sessionId, page, visible);
}

function liveViewerVisible(tx: any, sessionId: string): boolean {
  const current = tx.db.satiksmebot_live_viewer_state.sessionId.find(asString(sessionId).trim());
  if (!current) {
    return true;
  }
  return current.visible === true;
}

function listPublicIncidentsPayload(tx: any, limit: number) {
  const viewerStableId = optionalViewerStableId(tx);
  const items = rowsFrom(tx.db.satiksmebot_public_incident.iter())
    .sort((left, right) => compareTimeDescending(asString(left.lastReportAt), asString(right.lastReportAt)))
    .map((row) => incidentSummaryPayload(tx, row, viewerStableId));
  const max = Number(limit) || 0;
  return {
    generatedAt: nowISO(tx),
    incidents: max > 0 ? items.slice(0, max) : items,
  };
}

function publicIncidentDetailPayload(tx: any, incidentId: string) {
  const row = incidentMustExist(tx, incidentId);
  const viewerStableId = optionalViewerStableId(tx);
  const summary = incidentSummaryPayload(tx, row, viewerStableId);
  const events = rowsFrom(tx.db.satiksmebot_public_incident_event.incidentId.filter(summary.id))
    .sort((left, right) => compareTimeAscending(asString(left.createdAt), asString(right.createdAt)))
    .map((item) => ({
      id: asString(item.id).trim(),
      kind: asString(item.kind).trim(),
      name: asString(item.name).trim(),
      nickname: publicIncidentNickname(),
      createdAt: publicTimestamp(item.createdAt),
    }));
  const comments = rowsFrom(tx.db.satiksmebot_public_incident_comment.incidentId.filter(summary.id))
    .sort((left, right) => compareTimeAscending(asString(left.createdAt), asString(right.createdAt)))
    .map((item) => ({
      id: asString(item.id).trim(),
      incidentId: summary.id,
      nickname: publicIncidentNickname(),
      body: asString(item.body).trim(),
      createdAt: publicTimestamp(item.createdAt),
    }));
  return { summary, events, comments };
}

function upsertLiveSnapshotStatePayload(tx: any, stateJson: string) {
  const item = parseJSON(stateJson, 'invalid live snapshot state');
  const feed = asString(item?.feed).trim() || 'transport';
  const next = {
    feed,
    version: asString(item?.version).trim(),
    path: asString(item?.path).trim(),
    hash: '',
    publishedAt: '',
    lastSuccessAt: '',
    lastAttemptAt: '',
    status: '',
    consecutiveFailures: 0,
    vehicleCount: 0,
    updatedAt: nowISO(tx),
  };
  tx.db.satiksmebot_public_live_snapshot_state.feed.delete(feed);
  tx.db.satiksmebot_public_live_snapshot_state.insert(next);
  return liveSnapshotStateRowToJSON(next);
}

function countLiveViewersPayload(tx: any, activeSinceIso: string) {
  const cutoffMs = parseISO(activeSinceIso)?.getTime() || 0;
  clearLegacyPublicViewerHeartbeats(tx);
  const count = rowsFrom(tx.db.satiksmebot_live_viewer_state.iter())
    .filter((row) => {
      return row.visible === true
        && (parseISO(asString(row.updatedAt))?.getTime() || 0) >= cutoffMs;
    })
    .length;
  return { count };
}

function listStopSightingsSincePayload(tx: any, sinceIso: string, stopId: string, limit: number) {
  const sinceMs = parseISO(sinceIso)?.getTime() || 0;
  const cleanStopId = stopId.trim();
  let items = rowsFrom(tx.db.satiksmebot_stop_sighting.iter())
    .filter((row) => {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < sinceMs) {
        return false;
      }
      if (cleanStopId && asString(row.stopId).trim() !== cleanStopId) {
        return false;
      }
      return true;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map(stopSightingRowToJSON);
  const max = Number(limit) || 0;
  if (max > 0) {
    items = items.slice(0, max);
  }
  return { sightings: items };
}

function listVehicleSightingsSincePayload(tx: any, sinceIso: string, stopId: string, limit: number) {
  const sinceMs = parseISO(sinceIso)?.getTime() || 0;
  const cleanStopId = stopId.trim();
  let items = rowsFrom(tx.db.satiksmebot_vehicle_sighting.iter())
    .filter((row) => {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < sinceMs) {
        return false;
      }
      if (cleanStopId && asString(row.stopId).trim() !== cleanStopId) {
        return false;
      }
      return true;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map(vehicleSightingRowToJSON);
  const max = Number(limit) || 0;
  if (max > 0) {
    items = items.slice(0, max);
  }
  return { sightings: items };
}

function listAreaReportsSincePayload(tx: any, sinceIso: string, limit: number) {
  const sinceMs = parseISO(sinceIso)?.getTime() || 0;
  let items = rowsFrom(tx.db.satiksmebot_area_report.iter())
    .filter((row) => {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      return createdMs >= sinceMs;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map(areaReportRowToJSON);
  const max = Number(limit) || 0;
  if (max > 0) {
    items = items.slice(0, max);
  }
  return { reports: items };
}

function listIncidentVotesPayload(tx: any, incidentId: string) {
  const items = Array.from(latestVoteMap(tx, incidentId, 0).values())
    .sort((left, right) => compareTimeDescending(asString(left.updatedAt), asString(right.updatedAt)))
    .map(incidentVoteRowToJSON);
  return { votes: items };
}

function listIncidentVoteEventsPayload(tx: any, incidentId: string, sinceIso: string, limit: number) {
  const sinceMs = parseISO(sinceIso)?.getTime() || 0;
  let items = rowsFrom(tx.db.satiksmebot_incident_vote_event.iter())
    .filter((row) => {
      if (incidentId.trim() && asString(row.incidentId).trim() !== incidentId.trim()) {
        return false;
      }
      if (sinceMs > 0 && (parseISO(asString(row.createdAt))?.getTime() || 0) < sinceMs) {
        return false;
      }
      return true;
    })
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map(incidentVoteEventRowToJSON);
  const max = Number(limit) || 0;
  if (max > 0) {
    items = items.slice(0, max);
  }
  return { events: items };
}

function listIncidentCommentsPayload(tx: any, incidentId: string, limit: number) {
  let items = rowsFrom(tx.db.satiksmebot_incident_comment.incidentId.filter(incidentId.trim()))
    .sort((left, right) => compareTimeDescending(asString(left.createdAt), asString(right.createdAt)))
    .map(incidentCommentRowToJSON);
  const max = Number(limit) || 0;
  if (max > 0) {
    items = items.slice(0, max);
  }
  return { comments: items };
}

function nextReportDumpPayload(tx: any, nowIso: string) {
  const nowMs = parseISO(nowIso)?.getTime() || 0;
  const items = rowsFrom(tx.db.satiksmebot_report_dump.iter())
    .filter((row) => reportDumpRowHasPayload(row) && (parseISO(asString(row.nextAttemptAt))?.getTime() || 0) <= nowMs)
    .sort((left, right) => {
      const nextAttempt = compareTimeAscending(asString(left.nextAttemptAt), asString(right.nextAttemptAt));
      if (nextAttempt !== 0) {
        return nextAttempt;
      }
      return compareTimeAscending(asString(left.createdAt), asString(right.createdAt));
    });
  const row = items[0] || null;
  return {
    item: row ? reportDumpItemPayload(row) : null,
  };
}

function reportDumpRowHasPayload(row: any): boolean {
  return asString(row?.payload).trim() !== '';
}

function reportDumpItemPayload(row: any) {
  return {
    id: asString(row.id).trim(),
    payload: asString(row.payload),
    attempts: Number(row.attempts) || 0,
    createdAt: asString(row.createdAt).trim(),
    nextAttemptAt: asString(row.nextAttemptAt).trim(),
    lastAttemptAt: asString(row.lastAttemptAt).trim(),
    lastError: asString(row.lastError),
  };
}

function peekReportDumpPayload(tx: any) {
  const items = rowsFrom(tx.db.satiksmebot_report_dump.iter())
    .filter(reportDumpRowHasPayload)
    .sort((left, right) => {
      const nextAttempt = compareTimeAscending(asString(left.nextAttemptAt), asString(right.nextAttemptAt));
      if (nextAttempt !== 0) {
        return nextAttempt;
      }
      return compareTimeAscending(asString(left.createdAt), asString(right.createdAt));
    });
  return {
    item: items[0] ? reportDumpItemPayload(items[0]) : null,
  };
}

function pendingReportDumpCountPayload(tx: any) {
  return { pending: rowsFrom(tx.db.satiksmebot_report_dump.iter()).filter(reportDumpRowHasPayload).length };
}

function incidentMustExist(tx: any, incidentId: string): any {
  const row = tx.db.satiksmebot_public_incident.id.find(incidentId.trim()) || null;
  if (!row) {
    throw new SenderError('incident not found');
  }
  return row;
}

export const schemaInfo = spacetimedb.procedure(
  { name: named('schema_info') },
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    // The backend calls schema_info before accepting the module. Clear any
    // retained rows from the formerly active v1 public heartbeat table before
    // the updated service is considered ready.
    scrubLegacyPublicCompatibilityFields(tx);
    return serialize({
      module: SATIKSMEBOT_SCHEMA_MODULE,
      schemaVersion: SATIKSMEBOT_SCHEMA_VERSION,
    });
  })
);

export const bootstrapMe = spacetimedb.procedure(
  { name: named('bootstrap_me') },
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    const reporter = ensureReporter(tx);
    return serialize({
      userId: userIdNumber(asString(reporter.userId).trim()),
      stableUserId: asString(reporter.stableId).trim(),
      nickname: asString(reporter.nickname).trim(),
      language: asString(reporter.language).trim(),
    });
  })
);

export const listRecentReports = spacetimedb.procedure(
  { name: named('list_recent_reports') },
  { stopId: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { stopId, limit }) => ctx.withTx((tx) => {
    const session = requireReporterSession(tx);
    return serialize(userSightingsPayload(tx, session.stableId, stopId, Number(limit) || 0));
  })
);

export const listPublicSightings = spacetimedb.procedure(
  { name: named('list_public_sightings') },
  { stopId: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { stopId, limit }) => ctx.withTx((tx) => serialize(visibleSightingsPayload(tx, stopId, Number(limit) || 0)))
);

export const heartbeatLiveViewer = spacetimedb.reducer(
  { name: named('heartbeat_live_viewer') },
  { sessionId: t.string(), page: t.string() },
  (ctx, { sessionId, page }) => {
    const tx = ctx;
    requireServiceRole(tx);
    heartbeatLiveViewerPayload(tx, sessionId, page);
  }
);

export const setLiveViewerState = spacetimedb.reducer(
  { name: named('set_live_viewer_state') },
  { sessionId: t.string(), page: t.string(), visible: t.bool() },
  (ctx, { sessionId, page, visible }) => {
    const tx = ctx;
    requireServiceRole(tx);
    setLiveViewerStatePayload(tx, sessionId, page, visible === true);
  }
);

export const listPublicIncidents = spacetimedb.procedure(
  { name: named('list_public_incidents') },
  { limit: t.u32() },
  t.string(),
  (ctx, { limit }) => ctx.withTx((tx) => serialize(listPublicIncidentsPayload(tx, Number(limit) || 0)))
);

export const getPublicIncidentDetail = spacetimedb.procedure(
  { name: named('get_public_incident_detail') },
  { incidentId: t.string() },
  t.string(),
  (ctx, { incidentId }) => ctx.withTx((tx) => serialize(publicIncidentDetailPayload(tx, incidentId)))
);

export const submitStopReport = spacetimedb.procedure(
  { name: named('submit_stop_report') },
  { stopId: t.string(), bundleVersion: t.string(), bundleGeneratedAt: t.string() },
  t.string(),
  (ctx, { stopId, bundleVersion, bundleGeneratedAt }) => ctx.withTx((tx) => submitStopReportImpl(tx, stopId, bundleVersion, bundleGeneratedAt))
);

export const submitVehicleReport = spacetimedb.procedure(
  { name: named('submit_vehicle_report') },
  {
    stopId: t.string(),
    mode: t.string(),
    routeLabel: t.string(),
    direction: t.string(),
    destination: t.string(),
    departureSeconds: t.u32(),
    liveRowId: t.string(),
    bundleVersion: t.string(),
    bundleGeneratedAt: t.string(),
  },
  t.string(),
  (ctx, args) => ctx.withTx((tx) => submitVehicleReportImpl(tx, args, args.bundleVersion, args.bundleGeneratedAt))
);

export const submitAreaReport = spacetimedb.procedure(
  { name: named('submit_area_report') },
  {
    latitude: t.number(),
    longitude: t.number(),
    radiusMeters: t.u32(),
    description: t.string(),
    bundleVersion: t.string(),
    bundleGeneratedAt: t.string(),
  },
  t.string(),
  (ctx, args) => ctx.withTx((tx) => submitAreaReportImpl(tx, args, args.bundleVersion, args.bundleGeneratedAt))
);

export const voteIncident = spacetimedb.procedure(
  { name: named('vote_incident') },
  { incidentId: t.string(), value: t.string() },
  t.string(),
  (ctx, { incidentId, value }) => ctx.withTx((tx) => {
    const reporter = ensureReporter(tx);
    incidentMustExist(tx, incidentId);
    const stableId = asString(reporter.stableId).trim();
    const cleanValue = asString(value).trim().toUpperCase();
    const sameVoteCooldown = sameVoteCooldownSeconds(tx, incidentId, stableId, cleanValue);
    if (sameVoteCooldown > 0) {
      throw new SenderError('Šāds balsojums jau ir iesniegts. Jānogaida.');
    }
    if (countPublicVoteActionsForStableIdSince(tx, stableId, nowDate(tx).getTime() - VOTE_ACTION_WINDOW_MS) >= VOTE_ACTION_LIMIT) {
      throw new SenderError('Pārāk daudz balsojumu. Jānogaida.');
    }
    recordReporterIncidentVote(tx, reporter, incidentId, cleanValue, 'vote', randomId(tx, 'vote'), nowISO(tx));
    refreshPublicProjections(tx);
    const summary = incidentSummaryPayload(tx, incidentMustExist(tx, incidentId), asString(reporter.stableId).trim());
    return serialize(summary.votes);
  })
);

export const commentIncident = spacetimedb.procedure(
  { name: named('comment_incident') },
  { incidentId: t.string(), body: t.string() },
  t.string(),
  (ctx, { incidentId, body }) => ctx.withTx((tx) => {
    const reporter = ensureReporter(tx);
    incidentMustExist(tx, incidentId);
    const stableId = asString(reporter.stableId).trim();
    const trimmedBody = asString(body).trim();
    if (!trimmedBody) {
      throw new SenderError('comment is required');
    }
    if (Array.from(trimmedBody).length > 280) {
      throw new SenderError('comment is too long');
    }
    enforceIncidentCommentLimit(tx, incidentId, stableId);
    const next = sanitizeIncidentComment(tx, {
      incidentId,
      stableId,
      userId: asString(reporter.userId).trim(),
      nickname: asString(reporter.nickname).trim(),
      body: trimmedBody,
      createdAt: nowISO(tx),
    });
    tx.db.satiksmebot_incident_comment.id.delete(next.id);
    tx.db.satiksmebot_incident_comment.insert(next);
    refreshPublicProjections(tx);
    return serialize({
      id: next.id,
      incidentId: next.incidentId,
      nickname: publicIncidentNickname(),
      body: next.body,
      createdAt: next.createdAt,
    });
  })
);

export const beginBundleImport = spacetimedb.reducer(
  { name: named('begin_bundle_import') },
  { importId: t.string(), version: t.string(), generatedAt: t.string() },
  (ctx, { importId, version, generatedAt }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cleanImportId = asString(importId).trim();
    const cleanVersion = asString(version).trim();
    const cleanGeneratedAt = asString(generatedAt).trim();
    if (!cleanImportId || !cleanVersion || !cleanGeneratedAt) {
      throw new SenderError('importId, version, and generatedAt are required');
    }
    clearImport(tx, cleanImportId);
    tx.db.satiksmebot_import_chunk.insert({
      id: `${cleanImportId}|header`,
      importId: cleanImportId,
      chunkKind: 'header',
      version: cleanVersion,
      generatedAt: cleanGeneratedAt,
      createdAt: nowISO(tx),
      payloadJson: '',
    });
  }
);

export const appendBundleChunk = spacetimedb.reducer(
  { name: named('append_bundle_chunk') },
  { importId: t.string(), chunkKind: t.string(), payloadJson: t.string() },
  (ctx, { importId, chunkKind, payloadJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cleanImportId = asString(importId).trim();
    const cleanChunkKind = asString(chunkKind).trim();
    if (cleanChunkKind !== 'stops' && cleanChunkKind !== 'routes') {
      throw new SenderError('unsupported chunk kind');
    }
    const header = tx.db.satiksmebot_import_chunk.id.find(`${cleanImportId}|header`);
    if (!header) {
      throw new SenderError('bundle import not found');
    }
    parseBatchItems(payloadJson);
    tx.db.satiksmebot_import_chunk.insert({
      id: `${cleanImportId}|${cleanChunkKind}|${ctx.newUuidV7().toString()}`,
      importId: cleanImportId,
      chunkKind: cleanChunkKind,
      version: asString(header.version).trim(),
      generatedAt: asString(header.generatedAt).trim(),
      createdAt: nowISO(tx),
      payloadJson,
    });
  }
);

export const commitBundleImport = spacetimedb.reducer(
  { name: named('commit_bundle_import') },
  { importId: t.string() },
  (ctx, { importId }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cleanImportId = asString(importId).trim();
    const header = tx.db.satiksmebot_import_chunk.id.find(`${cleanImportId}|header`);
    if (!header) {
      throw new SenderError('bundle import not found');
    }
    const snapshot = {
      version: asString(header.version).trim(),
      generatedAt: asString(header.generatedAt).trim(),
      stops: [] as any[],
      routes: [] as any[],
    };
    for (const row of rowsForImport(tx, cleanImportId)) {
      if (row.id === `${cleanImportId}|header`) {
        continue;
      }
      const items = parseBatchItems(asString(row.payloadJson));
      if (asString(row.chunkKind).trim() === 'stops') {
        snapshot.stops.push(...items);
      } else if (asString(row.chunkKind).trim() === 'routes') {
        snapshot.routes.push(...items);
      }
    }
    const result = applyBundleSnapshot(tx, snapshot);
    clearImport(tx, cleanImportId);
    void result;
  }
);

export const abortBundleImport = spacetimedb.reducer(
  { name: named('abort_bundle_import') },
  { importId: t.string() },
  (ctx, { importId }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cleanImportId = asString(importId).trim();
    if (!cleanImportId) {
      throw new SenderError('importId is required');
    }
    clearImport(tx, cleanImportId);
  }
);

export const serviceSyncBundle = spacetimedb.reducer(
  { name: named('service_sync_bundle') },
  { snapshotJson: t.string() },
  (ctx, { snapshotJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    void applyBundleSnapshot(tx, parseJSON(snapshotJson, 'invalid bundle snapshot'));
  }
);

export const serviceImportStateSnapshot = spacetimedb.reducer(
  { name: named('service_import_state_snapshot') },
  { snapshotJson: t.string() },
  (ctx, { snapshotJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const snapshot = parseJSON(snapshotJson, 'invalid state snapshot');
    for (const item of Array.isArray(snapshot?.stopSightings) ? snapshot.stopSightings : []) {
      const next = sanitizeStopSighting(tx, item);
      tx.db.satiksmebot_stop_sighting.id.delete(next.id);
      tx.db.satiksmebot_stop_sighting.insert(next);
    }
    for (const item of Array.isArray(snapshot?.vehicleSightings) ? snapshot.vehicleSightings : []) {
      const next = sanitizeVehicleSighting(tx, item);
      tx.db.satiksmebot_vehicle_sighting.id.delete(next.id);
      tx.db.satiksmebot_vehicle_sighting.insert(next);
    }
    for (const item of Array.isArray(snapshot?.areaReports) ? snapshot.areaReports : []) {
      const next = sanitizeAreaReport(tx, item);
      tx.db.satiksmebot_area_report.id.delete(next.id);
      tx.db.satiksmebot_area_report.insert(next);
    }
    for (const item of Array.isArray(snapshot?.incidentVotes) ? snapshot.incidentVotes : []) {
      const next = sanitizeIncidentVote(tx, item);
      tx.db.satiksmebot_incident_vote.id.delete(next.id);
      tx.db.satiksmebot_incident_vote.insert(next);
    }
    for (const item of Array.isArray(snapshot?.incidentVoteEvents) ? snapshot.incidentVoteEvents : []) {
      const next = sanitizeIncidentVoteEvent(tx, item);
      tx.db.satiksmebot_incident_vote_event.id.delete(next.id);
      tx.db.satiksmebot_incident_vote_event.insert(next);
    }
    for (const item of Array.isArray(snapshot?.incidentComments) ? snapshot.incidentComments : []) {
      const next = sanitizeIncidentComment(tx, item);
      tx.db.satiksmebot_incident_comment.id.delete(next.id);
      tx.db.satiksmebot_incident_comment.insert(next);
    }
    for (const item of Array.isArray(snapshot?.reportDumpItems) ? snapshot.reportDumpItems : []) {
      const next = sanitizeReportDumpItem(tx, item);
      tx.db.satiksmebot_report_dump.id.delete(next.id);
      tx.db.satiksmebot_report_dump.insert(next);
    }
    refreshPublicProjections(tx);
  }
);

export const serviceUpsertLiveSnapshotState = spacetimedb.reducer(
  { name: named('service_upsert_live_snapshot_state') },
  { stateJson: t.string() },
  (ctx, { stateJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    upsertLiveSnapshotStatePayload(tx, stateJson);
  }
);

export const serviceCountLiveViewers = spacetimedb.procedure(
  { name: named('service_count_live_viewers') },
  { activeSinceIso: t.string() },
  t.string(),
  (ctx, { activeSinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(countLiveViewersPayload(tx, activeSinceIso));
  })
);

export const serviceCleanupLiveViewers = spacetimedb.reducer(
  { name: named('service_cleanup_live_viewers') },
  { cutoffIso: t.string() },
  (ctx, { cutoffIso }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cutoffMs = parseISO(cutoffIso)?.getTime() || 0;
    clearLegacyPublicViewerHeartbeats(tx);
    for (const row of rowsFrom(tx.db.satiksmebot_live_viewer_state.iter())) {
      const updatedMs = parseISO(asString(row.updatedAt))?.getTime() || 0;
      if (updatedMs < cutoffMs) {
        tx.db.satiksmebot_live_viewer_state.sessionId.delete(asString(row.sessionId).trim());
      }
    }
  }
);

export const servicePutStopSighting = spacetimedb.reducer(
  { name: named('service_put_stop_sighting') },
  { sightingJson: t.string() },
  (ctx, { sightingJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeStopSighting(tx, parseJSON(sightingJson, 'invalid stop sighting'));
    tx.db.satiksmebot_stop_sighting.id.delete(next.id);
    tx.db.satiksmebot_stop_sighting.insert(next);
    refreshPublicProjections(tx);
  }
);

export const serviceRecordStopSightingWithVote = spacetimedb.procedure(
  { name: named('service_record_stop_sighting_with_vote') },
  { sightingJson: t.string(), voteJson: t.string(), eventJson: t.string(), dedupeSeconds: t.u32() },
  t.string(),
  (ctx, { sightingJson, voteJson, eventJson, dedupeSeconds }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(recordServiceStopSightingWithVotePayload(tx, sightingJson, voteJson, eventJson, Number(dedupeSeconds) || 0));
  })
);

export const serviceGetLastStopSighting = spacetimedb.procedure(
  { name: named('service_get_last_stop_sighting') },
  { userId: t.string(), stopId: t.string() },
  t.string(),
  (ctx, { userId, stopId }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const stableId = `telegram:${userId.trim()}`;
    const row = latestStopSightingFor(tx, stableId, stopId);
    return serialize({ sighting: row ? stopSightingRowToJSON(row) : null });
  })
);

export const serviceListStopSightingsSince = spacetimedb.procedure(
  { name: named('service_list_stop_sightings_since') },
  { sinceIso: t.string(), stopId: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { sinceIso, stopId, limit }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(listStopSightingsSincePayload(tx, sinceIso, stopId, Number(limit) || 0));
  })
);

export const servicePutVehicleSighting = spacetimedb.reducer(
  { name: named('service_put_vehicle_sighting') },
  { sightingJson: t.string() },
  (ctx, { sightingJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeVehicleSighting(tx, parseJSON(sightingJson, 'invalid vehicle sighting'));
    tx.db.satiksmebot_vehicle_sighting.id.delete(next.id);
    tx.db.satiksmebot_vehicle_sighting.insert(next);
    refreshPublicProjections(tx);
  }
);

export const serviceRecordVehicleSightingWithVote = spacetimedb.procedure(
  { name: named('service_record_vehicle_sighting_with_vote') },
  { sightingJson: t.string(), voteJson: t.string(), eventJson: t.string(), dedupeSeconds: t.u32() },
  t.string(),
  (ctx, { sightingJson, voteJson, eventJson, dedupeSeconds }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(recordServiceVehicleSightingWithVotePayload(tx, sightingJson, voteJson, eventJson, Number(dedupeSeconds) || 0));
  })
);

export const serviceGetLastVehicleSighting = spacetimedb.procedure(
  { name: named('service_get_last_vehicle_sighting') },
  { userId: t.string(), scopeKey: t.string() },
  t.string(),
  (ctx, { userId, scopeKey }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const stableId = `telegram:${userId.trim()}`;
    const row = latestVehicleSightingFor(tx, stableId, scopeKey);
    return serialize({ sighting: row ? vehicleSightingRowToJSON(row) : null });
  })
);

export const serviceListVehicleSightingsSince = spacetimedb.procedure(
  { name: named('service_list_vehicle_sightings_since') },
  { sinceIso: t.string(), stopId: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { sinceIso, stopId, limit }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(listVehicleSightingsSincePayload(tx, sinceIso, stopId, Number(limit) || 0));
  })
);

export const servicePutAreaReport = spacetimedb.reducer(
  { name: named('service_put_area_report') },
  { reportJson: t.string() },
  (ctx, { reportJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeAreaReport(tx, parseJSON(reportJson, 'invalid area report'));
    tx.db.satiksmebot_area_report.id.delete(next.id);
    tx.db.satiksmebot_area_report.insert(next);
    refreshPublicProjections(tx);
  }
);

export const serviceRecordAreaReportWithVote = spacetimedb.procedure(
  { name: named('service_record_area_report_with_vote') },
  { reportJson: t.string(), voteJson: t.string(), eventJson: t.string(), dedupeSeconds: t.u32() },
  t.string(),
  (ctx, { reportJson, voteJson, eventJson, dedupeSeconds }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(recordServiceAreaReportWithVotePayload(tx, reportJson, voteJson, eventJson, Number(dedupeSeconds) || 0));
  })
);

export const serviceGetLastAreaReport = spacetimedb.procedure(
  { name: named('service_get_last_area_report') },
  { userId: t.string(), scopeKey: t.string() },
  t.string(),
  (ctx, { userId, scopeKey }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const stableId = `telegram:${userId.trim()}`;
    const row = latestAreaReportFor(tx, stableId, scopeKey);
    return serialize({ report: row ? areaReportRowToJSON(row) : null });
  })
);

export const serviceListAreaReportsSince = spacetimedb.procedure(
  { name: named('service_list_area_reports_since') },
  { sinceIso: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { sinceIso, limit }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(listAreaReportsSincePayload(tx, sinceIso, Number(limit) || 0));
  })
);

export const serviceUpsertIncidentVote = spacetimedb.reducer(
  { name: named('service_upsert_incident_vote') },
  { voteJson: t.string() },
  (ctx, { voteJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeIncidentVote(tx, parseJSON(voteJson, 'invalid vote'));
    tx.db.satiksmebot_incident_vote.id.delete(next.id);
    tx.db.satiksmebot_incident_vote.insert(next);
    refreshPublicProjections(tx);
  }
);

export const serviceRecordIncidentVote = spacetimedb.reducer(
  { name: named('service_record_incident_vote') },
  { voteJson: t.string(), eventJson: t.string() },
  (ctx, { voteJson, eventJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    recordIncidentVoteAction(
      tx,
      parseJSON(voteJson, 'invalid vote'),
      parseJSON(eventJson, 'invalid vote event')
    );
    refreshPublicProjections(tx);
  }
);

export const serviceListIncidentVotes = spacetimedb.procedure(
  { name: named('service_list_incident_votes') },
  { incidentId: t.string() },
  t.string(),
  (ctx, { incidentId }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(listIncidentVotesPayload(tx, incidentId));
  })
);

export const serviceListIncidentVoteEvents = spacetimedb.procedure(
  { name: named('service_list_incident_vote_events') },
  { incidentId: t.string(), sinceIso: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { incidentId, sinceIso, limit }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(listIncidentVoteEventsPayload(tx, incidentId, sinceIso, Number(limit) || 0));
  })
);

export const serviceCountMapReportsByUserSince = spacetimedb.procedure(
  { name: named('service_count_map_reports_by_user_since') },
  { userId: t.string(), sinceIso: t.string() },
  t.string(),
  (ctx, { userId, sinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const stableId = `telegram:${userId.trim()}`;
    return serialize({ count: countMapReportsForStableIdSince(tx, stableId, parseISO(sinceIso)?.getTime() || 0) });
  })
);

export const serviceCountIncidentVoteEventsByUserSince = spacetimedb.procedure(
  { name: named('service_count_incident_vote_events_by_user_since') },
  { userId: t.string(), source: t.string(), sinceIso: t.string() },
  t.string(),
  (ctx, { userId, source, sinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const stableId = `telegram:${userId.trim()}`;
    return serialize({ count: countIncidentVoteEventsForStableIdSince(tx, stableId, asString(source).trim(), parseISO(sinceIso)?.getTime() || 0) });
  })
);

export const serviceCountIncidentCommentsByUserSince = spacetimedb.procedure(
  { name: named('service_count_incident_comments_by_user_since') },
  { userId: t.string(), sinceIso: t.string() },
  t.string(),
  (ctx, { userId, sinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const stableId = `telegram:${userId.trim()}`;
    return serialize({ count: countIncidentCommentsForStableIdSince(tx, stableId, parseISO(sinceIso)?.getTime() || 0) });
  })
);

export const serviceCountIncidentCommentsByIncidentSince = spacetimedb.procedure(
  { name: named('service_count_incident_comments_by_incident_since') },
  { incidentId: t.string(), sinceIso: t.string() },
  t.string(),
  (ctx, { incidentId, sinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize({ count: countIncidentCommentsForIncidentSince(tx, incidentId, parseISO(sinceIso)?.getTime() || 0) });
  })
);

export const servicePutIncidentComment = spacetimedb.reducer(
  { name: named('service_put_incident_comment') },
  { commentJson: t.string() },
  (ctx, { commentJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeIncidentComment(tx, parseJSON(commentJson, 'invalid comment'));
    tx.db.satiksmebot_incident_comment.id.delete(next.id);
    tx.db.satiksmebot_incident_comment.insert(next);
    refreshPublicProjections(tx);
  }
);

export const serviceListIncidentComments = spacetimedb.procedure(
  { name: named('service_list_incident_comments') },
  { incidentId: t.string(), limit: t.u32() },
  t.string(),
  (ctx, { incidentId, limit }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(listIncidentCommentsPayload(tx, incidentId, Number(limit) || 0));
  })
);

export const serviceEnqueueReportDump = spacetimedb.reducer(
  { name: named('service_enqueue_report_dump') },
  { itemJson: t.string() },
  (ctx, { itemJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeReportDumpItem(tx, parseJSON(itemJson, 'invalid report dump item'));
    tx.db.satiksmebot_report_dump.id.delete(next.id);
    tx.db.satiksmebot_report_dump.insert(next);
  }
);

export const serviceNextReportDump = spacetimedb.procedure(
  { name: named('service_next_report_dump') },
  { nowIso: t.string() },
  t.string(),
  (ctx, { nowIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(nextReportDumpPayload(tx, nowIso));
  })
);

export const servicePeekReportDump = spacetimedb.procedure(
  { name: named('service_peek_report_dump') },
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(peekReportDumpPayload(tx));
  })
);

export const serviceDeleteReportDump = spacetimedb.reducer(
  { name: named('service_delete_report_dump') },
  { id: t.string() },
  (ctx, { id }) => {
    const tx = ctx;
    requireServiceRole(tx);
    tx.db.satiksmebot_report_dump.id.delete(id.trim());
  }
);

export const serviceUpdateReportDumpFailure = spacetimedb.reducer(
  { name: named('service_update_report_dump_failure') },
  { id: t.string(), attempts: t.u32(), nextAttemptAt: t.string(), lastAttemptAt: t.string(), lastError: t.string() },
  (ctx, { id, attempts, nextAttemptAt, lastAttemptAt, lastError }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const existing = tx.db.satiksmebot_report_dump.id.find(id.trim());
    if (!existing) {
      return;
    }
    tx.db.satiksmebot_report_dump.id.delete(id.trim());
    tx.db.satiksmebot_report_dump.insert({
      ...existing,
      attempts: Number(attempts) || 0,
      nextAttemptAt: nextAttemptAt.trim(),
      lastAttemptAt: lastAttemptAt.trim(),
      lastError: asString(lastError),
    });
  }
);

export const servicePendingReportDumpCount = spacetimedb.procedure(
  { name: named('service_pending_report_dump_count') },
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    return serialize(pendingReportDumpCountPayload(tx));
  })
);

export const serviceGetChatAnalyzerCheckpoint = spacetimedb.procedure(
  { name: named('service_get_chat_analyzer_checkpoint') },
  { chatId: t.string() },
  t.string(),
  (ctx, { chatId }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const cleanChatId = asString(chatId).trim();
    const row = tx.db.satiksmebot_chat_analyzer_checkpoint.chatId.find(cleanChatId);
    return serialize({
      found: Boolean(row),
      lastMessageId: row ? numericValue(row.lastMessageId) : 0,
    });
  })
);

export const serviceSetChatAnalyzerCheckpoint = spacetimedb.reducer(
  { name: named('service_set_chat_analyzer_checkpoint') },
  { chatId: t.string(), lastMessageId: t.string(), updatedAt: t.string() },
  (ctx, { chatId, lastMessageId, updatedAt }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cleanChatId = asString(chatId).trim();
    if (!cleanChatId) {
      throw new SenderError('chatId is required');
    }
    const nextID = numericString(lastMessageId);
    const existing = tx.db.satiksmebot_chat_analyzer_checkpoint.chatId.find(cleanChatId);
    const existingID = numericValue(existing?.lastMessageId);
    const chosenID = Math.max(existingID, numericValue(nextID));
    tx.db.satiksmebot_chat_analyzer_checkpoint.chatId.delete(cleanChatId);
    tx.db.satiksmebot_chat_analyzer_checkpoint.insert({
      chatId: cleanChatId,
      lastMessageId: String(chosenID),
      updatedAt: trimOptional(asString(updatedAt)) || nowISO(tx),
    });
  }
);

export const serviceEnqueueChatAnalyzerMessage = spacetimedb.procedure(
  { name: named('service_enqueue_chat_analyzer_message') },
  { itemJson: t.string() },
  t.string(),
  (ctx, { itemJson }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const next = sanitizeChatAnalyzerMessage(tx, parseJSON(itemJson, 'invalid chat analyzer message'));
    if (tx.db.satiksmebot_chat_analyzer_message.id.find(next.id)) {
      return serialize({ inserted: false });
    }
    tx.db.satiksmebot_chat_analyzer_message.insert(next);
    return serialize({ inserted: true });
  })
);

export const serviceListPendingChatAnalyzerMessages = spacetimedb.procedure(
  { name: named('service_list_pending_chat_analyzer_messages') },
  { limit: t.u32() },
  t.string(),
  (ctx, { limit }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const max = Number(limit) > 0 ? Number(limit) : 25;
    const messages = rowsFrom(tx.db.satiksmebot_chat_analyzer_message.status.filter('pending'))
      .sort((left, right) => {
        const received = compareTimeAscending(asString(left.receivedAt), asString(right.receivedAt));
        if (received !== 0) {
          return received;
        }
        return numericValue(left.messageId) - numericValue(right.messageId);
      })
      .slice(0, max)
      .map(chatAnalyzerMessageRowToJSON);
    return serialize({ messages });
  })
);

export const serviceMarkChatAnalyzerMessageProcessed = spacetimedb.reducer(
  { name: named('service_mark_chat_analyzer_message_processed') },
  { id: t.string(), status: t.string(), analysisJson: t.string(), appliedActionId: t.string(), appliedTargetKey: t.string(), batchId: t.string(), lastError: t.string(), processedAt: t.string() },
  (ctx, { id, status, analysisJson, appliedActionId, appliedTargetKey, batchId, lastError, processedAt }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const cleanID = asString(id).trim();
    const existing = tx.db.satiksmebot_chat_analyzer_message.id.find(cleanID);
    if (!existing) {
      return;
    }
    const cleanBatchId = asString(batchId).trim();
    const cleanStatus = sanitizeChatAnalyzerStatus(asString(status));
    const cleanProcessedAt = trimOptional(asString(processedAt)) || nowISO(tx);
    tx.db.satiksmebot_chat_analyzer_message.id.delete(cleanID);
    tx.db.satiksmebot_chat_analyzer_message.insert({
      ...existing,
      status: cleanStatus,
      attempts: (Number(existing.attempts) || 0) + 1,
      analysisJson: asString(analysisJson),
      appliedActionId: asString(appliedActionId).trim(),
      appliedTargetKey: asString(appliedTargetKey).trim(),
      lastError: asString(lastError),
      processedAt: cleanProcessedAt,
    });
    if (cleanBatchId) {
      const linkID = `${cleanBatchId}:${cleanID}`;
      tx.db.satiksmebot_chat_analyzer_batch_message.id.delete(linkID);
      tx.db.satiksmebot_chat_analyzer_batch_message.insert({
        id: linkID,
        batchId: cleanBatchId,
        chatMessageId: cleanID,
        messageId: asString(existing.messageId),
        status: cleanStatus,
        processedAt: cleanProcessedAt,
      });
    }
  }
);

export const serviceSaveChatAnalyzerBatch = spacetimedb.reducer(
  { name: named('service_save_chat_analyzer_batch') },
  { batchJson: t.string() },
  (ctx, { batchJson }) => {
    const tx = ctx;
    requireServiceRole(tx);
    const next = sanitizeChatAnalyzerBatch(parseJSON(batchJson, 'invalid chat analyzer batch'));
    tx.db.satiksmebot_chat_analyzer_batch.id.delete(next.id);
    tx.db.satiksmebot_chat_analyzer_batch.insert(next);
  }
);

export const serviceCountChatAnalyzerMessagesBySenderSince = spacetimedb.procedure(
  { name: named('service_count_chat_analyzer_messages_by_sender_since') },
  { chatId: t.string(), senderId: t.string(), sinceIso: t.string() },
  t.string(),
  (ctx, { chatId, senderId, sinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const cleanChatId = asString(chatId).trim();
    const cleanSenderId = numericString(senderId);
    const sinceMs = parseISO(sinceIso)?.getTime() || 0;
    const count = rowsFrom(tx.db.satiksmebot_chat_analyzer_message.senderId.filter(cleanSenderId))
      .filter((row) => asString(row.chatId).trim() === cleanChatId)
      .filter((row) => (parseISO(asString(row.receivedAt))?.getTime() || 0) >= sinceMs)
      .length;
    return serialize({ count });
  })
);

export const serviceCountChatAnalyzerAppliedByTargetSince = spacetimedb.procedure(
  { name: named('service_count_chat_analyzer_applied_by_target_since') },
  { targetKey: t.string(), sinceIso: t.string() },
  t.string(),
  (ctx, { targetKey, sinceIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const cleanTargetKey = asString(targetKey).trim();
    const sinceMs = parseISO(sinceIso)?.getTime() || 0;
    const count = rowsFrom(tx.db.satiksmebot_chat_analyzer_message.appliedTargetKey.filter(cleanTargetKey))
      .filter((row) => asString(row.status).trim() === 'applied')
      .filter((row) => (parseISO(asString(row.processedAt))?.getTime() || 0) >= sinceMs)
      .length;
    return serialize({ count });
  })
);

export const serviceCleanupExpiredState = spacetimedb.procedure(
  { name: named('service_cleanup_expired_state') },
  { nowIso: t.string(), cutoffIso: t.string() },
  t.string(),
  (ctx, { cutoffIso }) => ctx.withTx((tx) => {
    requireServiceRole(tx);
    const cutoffMs = parseISO(cutoffIso)?.getTime() || 0;
    let stopDeleted = 0;
    let vehicleDeleted = 0;
    let areaDeleted = 0;
    for (const row of rowsFrom(tx.db.satiksmebot_stop_sighting.iter())) {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < cutoffMs) {
        tx.db.satiksmebot_stop_sighting.id.delete(row.id);
        stopDeleted += 1;
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_vehicle_sighting.iter())) {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < cutoffMs) {
        tx.db.satiksmebot_vehicle_sighting.id.delete(row.id);
        vehicleDeleted += 1;
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_area_report.iter())) {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < cutoffMs) {
        tx.db.satiksmebot_area_report.id.delete(row.id);
        areaDeleted += 1;
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_incident_vote.iter())) {
      const updatedMs = parseISO(asString(row.updatedAt))?.getTime() || 0;
      if (updatedMs < cutoffMs) {
        tx.db.satiksmebot_incident_vote.id.delete(row.id);
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_incident_vote_event.iter())) {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < cutoffMs) {
        tx.db.satiksmebot_incident_vote_event.id.delete(row.id);
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_incident_comment.iter())) {
      const createdMs = parseISO(asString(row.createdAt))?.getTime() || 0;
      if (createdMs < cutoffMs) {
        tx.db.satiksmebot_incident_comment.id.delete(row.id);
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_report_dedupe.iter())) {
      const lastReportMs = parseISO(asString(row.lastReportAt))?.getTime() || 0;
      if (lastReportMs < cutoffMs) {
        tx.db.satiksmebot_report_dedupe.id.delete(row.id);
      }
    }
    for (const row of rowsFrom(tx.db.satiksmebot_chat_analyzer_message.iter())) {
      const receivedMs = parseISO(asString(row.receivedAt))?.getTime() || 0;
      if (receivedMs < cutoffMs) {
        tx.db.satiksmebot_chat_analyzer_message.id.delete(row.id);
      }
    }
    refreshPublicProjections(tx);
    return serialize({
      stopSightingsDeleted: stopDeleted,
      vehicleSightingsDeleted: vehicleDeleted,
      areaReportsDeleted: areaDeleted,
    });
  })
);
