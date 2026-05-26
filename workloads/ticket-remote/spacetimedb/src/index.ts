// @ts-nocheck
import { ScheduleAt } from 'spacetimedb';
import {
  CaseConversionPolicy,
  SenderError,
  schema,
  table,
  t,
} from 'spacetimedb/server';

const PREFIX = 'ticketremote_';
const CONTROL_MS = 90 * 1000;
const PRESENCE_TTL_MS = 45 * 1000;
const HISTORY_TTL_MS = 72 * 60 * 60 * 1000;
const CLEANUP_BATCH_SIZE = 200;
const CLEANUP_INTERVAL_MICROS = 5n * 60n * 1000n * 1000n;
const PHONE_KEEPALIVE_MS = 60 * 1000;

function named(suffix: string): string {
  return `${PREFIX}${suffix}`;
}

const ticketremote_ticket = table(
  { name: named('ticket') },
  {
    id: t.string().primaryKey(),
    displayName: t.string(),
    createdAt: t.string(),
    updatedAt: t.string(),
  }
);

const ticketremote_ticket_member = table(
  {
    name: named('ticket_member'),
    indexes: [
      { accessor: 'ticketEmail', name: named('ticket_member_ticket_email_idx'), algorithm: 'btree', columns: ['ticketId', 'email'] },
    ],
  },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    email: t.string().index(),
    role: t.string().index(),
    active: t.bool(),
    createdAt: t.string(),
    updatedAt: t.string(),
  }
);

const ticketremote_viewer_presence = table(
  {
    name: named('viewer_presence'),
    indexes: [
      { accessor: 'ticketExpiresAt', name: named('viewer_presence_ticket_expires_idx'), algorithm: 'btree', columns: ['ticketId', 'expiresAt'] },
    ],
  },
  {
    sessionId: t.string().primaryKey(),
    ticketId: t.string().index(),
    email: t.string().index(),
    displayName: t.string(),
    page: t.string(),
    connected: t.bool(),
    createdAt: t.string(),
    lastSeenAt: t.string().index(),
    expiresAt: t.string().index(),
  }
);

const ticketremote_viewer_public = table(
  {
    name: named('viewer_public'),
    public: true,
    indexes: [
      { accessor: 'ticketExpiresAt', name: named('viewer_public_ticket_expires_idx'), algorithm: 'btree', columns: ['ticketId', 'expiresAt'] },
    ],
  },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    publicId: t.string(),
    label: t.string(),
    connected: t.bool(),
    lastSeenAt: t.string().index(),
    expiresAt: t.string().index(),
  }
);

const ticketremote_control_session = table(
  {
    name: named('control_session'),
    indexes: [
      { accessor: 'ticketExpiresAt', name: named('control_session_ticket_expires_idx'), algorithm: 'btree', columns: ['ticketId', 'expiresAt'] },
    ],
  },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    sessionId: t.string().index(),
    email: t.string().index(),
    state: t.string().index(),
    claimedAt: t.string(),
    expiresAt: t.string().index(),
    extended: t.bool(),
    endedAt: t.string(),
    endReason: t.string(),
  }
);

const ticketremote_phone_backend = table(
  { name: named('phone_backend') },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    backendId: t.string().index(),
    attachName: t.string(),
    baseUrl: t.string(),
    desiredState: t.string(),
    streamState: t.string(),
    healthJson: t.string(),
    lastError: t.string(),
    lastSeenAt: t.string().index(),
  }
);

const ticketremote_phone_status = table(
  { name: named('phone_status'), public: true },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    backendId: t.string(),
    attachName: t.string(),
    desiredState: t.string(),
    streamState: t.string(),
    lastSeenAt: t.string(),
    updatedAt: t.string().index(),
  }
);

const ticketremote_phone_status_history = table(
  {
    name: named('phone_status_history'),
    indexes: [
      { accessor: 'ticketExpiresAt', name: named('phone_status_history_ticket_expires_idx'), algorithm: 'btree', columns: ['ticketId', 'expiresAt'] },
    ],
  },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    backendId: t.string().index(),
    attachName: t.string(),
    desiredState: t.string(),
    streamState: t.string(),
    lastError: t.string(),
    createdAt: t.string().index(),
    expiresAt: t.string().index(),
  }
);

const ticketremote_audit_event = table(
  {
    name: named('audit_event'),
    indexes: [
      { accessor: 'ticketExpiresAt', name: named('audit_event_ticket_expires_idx'), algorithm: 'btree', columns: ['ticketId', 'expiresAt'] },
    ],
  },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    actorEmail: t.string().index(),
    event: t.string().index(),
    payloadJson: t.string(),
    createdAt: t.string().index(),
    expiresAt: t.string().index(),
  }
);

const ticketremote_audit_counter = table(
  { name: named('audit_counter') },
  {
    ticketId: t.string().primaryKey(),
    nextOrdinal: t.string(),
    updatedAt: t.string(),
  }
);

const ticketremote_auth_config = table(
  { name: named('auth_config') },
  {
    ticketId: t.string().primaryKey(),
    issuer: t.string(),
    audience: t.string(),
    updatedAt: t.string(),
  }
);

const ticketremote_ticket_summary = table(
  { name: named('ticket_summary'), public: true },
  {
    id: t.string().primaryKey(),
    ticketId: t.string().index(),
    displayName: t.string(),
    viewerCount: t.u32(),
    phoneBackendId: t.string(),
    phoneAttachName: t.string(),
    phoneDesiredState: t.string(),
    phoneStreamState: t.string(),
    phoneLastSeenAt: t.string(),
    updatedAt: t.string().index(),
  }
);

const ticketremote_cleanup_schedule = table(
  {
    name: named('cleanup_schedule'),
    scheduled: (): any => scheduledCleanupExpired,
  },
  {
    scheduled_id: t.u64().primaryKey().autoInc(),
    scheduled_at: t.scheduleAt(),
    ticketId: t.string().index(),
    batchSize: t.u32(),
    createdAt: t.string(),
    updatedAt: t.string(),
  }
);

const spacetimedb: any = schema(
  {
    ticketremote_ticket,
    ticketremote_ticket_member,
    ticketremote_viewer_presence,
    ticketremote_viewer_public,
    ticketremote_control_session,
    ticketremote_phone_backend,
    ticketremote_phone_status,
    ticketremote_phone_status_history,
    ticketremote_audit_event,
    ticketremote_audit_counter,
    ticketremote_auth_config,
    ticketremote_ticket_summary,
    ticketremote_cleanup_schedule,
  },
  { CASE_CONVERSION_POLICY: CaseConversionPolicy.None }
);

export default spacetimedb;

function rowsFrom(iterable: any): any[] {
  return Array.from(iterable as Iterable<any>) as any[];
}

function serialize(payload: unknown): string {
  return JSON.stringify(payload);
}

function cleanEmail(value: string): string {
  return String(value || '').trim().toLowerCase();
}

function accountPublicId(email: string): string {
  const alphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ';
  const normalized = cleanEmail(email);
  let hash = 2166136261 >>> 0;
  for (let i = 0; i < normalized.length; i += 1) {
    hash ^= normalized.charCodeAt(i) & 0xff;
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  let value = hash % (36 * 36 * 36 * 36);
  let out = '';
  for (let i = 0; i < 4; i += 1) {
    out = alphabet[value % 36] + out;
    value = Math.floor(value / 36);
  }
  return out;
}

function publicPresenceRowId(ticketId: string, sessionId: string): string {
  const alphabet = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ';
  const source = `${ticketId}:${sessionId}`;
  let hash = 2166136261 >>> 0;
  for (let i = 0; i < source.length; i += 1) {
    hash ^= source.charCodeAt(i) & 0xff;
    hash = Math.imul(hash, 16777619) >>> 0;
  }
  let value = hash;
  let out = '';
  for (let i = 0; i < 8; i += 1) {
    out = alphabet[value % 36] + out;
    value = Math.floor(value / 36);
  }
  return `${ticketId}:${out}`;
}

function cleanRole(value: string): string {
  const role = String(value || '').trim().toLowerCase();
  if (role === 'owner' || role === 'admin') return role;
  return 'member';
}

function parseTime(value: string): number {
  const ms = Date.parse(String(value || ''));
  return Number.isFinite(ms) ? ms : 0;
}

function isoFromMs(ms: number): string {
  return new Date(ms).toISOString();
}

function nowOr(value: string): string {
  const clean = String(value || '').trim();
  return clean || new Date().toISOString();
}

function serverNow(ctx: any): string {
  const stamp = ctx.timestamp;
  const text = stamp && typeof stamp.toISOString === 'function' ? stamp.toISOString() : String(stamp || '').trim();
  if (!text) throw new SenderError('server time required');
  return text;
}

function connectionSessionId(ctx: any): string {
  const connectionId = ctx.connectionId;
  const text = connectionId && typeof connectionId.toHexString === 'function'
    ? connectionId.toHexString()
    : String(connectionId || '').trim();
  if (!text) throw new SenderError('connection required');
  return text;
}

function memberId(ticketId: string, email: string): string {
  return `${ticketId}:${cleanEmail(email)}`;
}

function phoneRowId(ticketId: string, backendId: string): string {
  return `${ticketId}:${String(backendId || '').trim() || 'pixel'}`;
}

function historyExpiresAt(now: string): string {
  const base = parseTime(now) || Date.now();
  return isoFromMs(base + HISTORY_TTL_MS);
}

function presenceExpiresAt(now: string): string {
  const base = parseTime(now) || Date.now();
  return isoFromMs(base + PRESENCE_TTL_MS);
}

function requireService(tx: any): void {
  if (hasServiceRole(tx)) return;
  throw new SenderError('service role required');
}

function hasServiceRole(tx: any): boolean {
  const auth = tx.senderAuth;
  const roles = auth?.jwt?.fullPayload?.roles;
  return Boolean(auth?.hasJWT && Array.isArray(roles) && roles.includes('ticketremote_service'));
}

function hasInternalRole(tx: any): boolean {
  return Boolean(tx.senderAuth?.isInternal || tx.senderAuth?.isSystem);
}

function authConfig(tx: any, ticketId: string): any | null {
  return tx.db.ticketremote_auth_config.ticketId.find(String(ticketId || '').trim() || 'vivi-default') || null;
}

function jwtAudienceIncludes(jwt: any, expected: string): boolean {
  const clean = String(expected || '').trim();
  if (!clean) return false;
  const audiences = jwt?.audience || jwt?.fullPayload?.aud;
  if (Array.isArray(audiences)) return audiences.some((item) => String(item || '').trim() === clean);
  return String(audiences || '').trim() === clean;
}

function clientEmailFromAuth(tx: any, ticketId: string): string {
  const auth = tx.senderAuth;
  const jwt = auth?.jwt;
  if (!auth?.hasJWT || !jwt) {
    throw new SenderError('auth required');
  }
  const config = authConfig(tx, ticketId);
  if (!config || !String(config.issuer || '').trim() || !String(config.audience || '').trim()) {
    throw new SenderError('auth config required');
  }
  if (String(jwt.issuer || '').trim().replace(/\/$/, '') !== String(config.issuer || '').trim().replace(/\/$/, '')) {
    throw new SenderError('invalid auth issuer');
  }
  if (!jwtAudienceIncludes(jwt, config.audience)) {
    throw new SenderError('invalid auth audience');
  }
  const payload = jwt.fullPayload || {};
  const email = cleanEmail(payload.email);
  if (!email || payload.email_verified !== true) {
    throw new SenderError('verified email required');
  }
  if (!isMember(tx, ticketId, email)) {
    throw new SenderError('ticket membership required');
  }
  return email;
}

function ensureTicket(tx: any, ticketId: string, displayName: string, now: string): any {
  const cleanTicketId = String(ticketId || '').trim() || 'vivi-default';
  const existing = tx.db.ticketremote_ticket.id.find(cleanTicketId);
  if (existing) {
    if (displayName && existing.displayName !== displayName) {
      tx.db.ticketremote_ticket.id.delete(cleanTicketId);
      return tx.db.ticketremote_ticket.insert({ ...existing, displayName, updatedAt: now });
    }
    return existing;
  }
  return tx.db.ticketremote_ticket.insert({
    id: cleanTicketId,
    displayName: displayName || 'ViVi timed ticket',
    createdAt: now,
    updatedAt: now,
  });
}

function isMember(tx: any, ticketId: string, email: string): boolean {
  const member = tx.db.ticketremote_ticket_member.id.find(memberId(ticketId, email));
  return Boolean(member && member.active === true);
}

function isAdmin(tx: any, ticketId: string, email: string): boolean {
  const member = tx.db.ticketremote_ticket_member.id.find(memberId(ticketId, email));
  return Boolean(member && member.active === true && (member.role === 'owner' || member.role === 'admin'));
}

function nextAuditOrdinal(tx: any, ticketId: string, now: string): number {
  const existing = tx.db.ticketremote_audit_counter.ticketId.find(ticketId);
  const ordinal = Math.max(1, Number.parseInt(String(existing?.nextOrdinal || '1'), 10) || 1);
  if (existing) tx.db.ticketremote_audit_counter.ticketId.delete(ticketId);
  tx.db.ticketremote_audit_counter.insert({
    ticketId,
    nextOrdinal: String(ordinal + 1),
    updatedAt: now,
  });
  return ordinal;
}

function audit(tx: any, ticketId: string, actorEmail: string, event: string, payloadJson: string, now: string): void {
  const ordinal = nextAuditOrdinal(tx, ticketId, now);
  const stamp = String(now || '').replace(/[^0-9A-Za-z]/g, '') || 'time';
  const cleanEvent = String(event || 'event').replace(/[^0-9A-Za-z_-]/g, '_');
  tx.db.ticketremote_audit_event.insert({
    id: `${ticketId}:${stamp}:${ordinal}:${cleanEvent}`,
    ticketId,
    actorEmail: cleanEmail(actorEmail),
    event: String(event || '').trim(),
    payloadJson: payloadJson || '{}',
    createdAt: now,
    expiresAt: historyExpiresAt(now),
  });
}

function ensureCleanupSchedule(tx: any, ticketId: string, now: string): void {
  const existing = rowsFrom(tx.db.ticketremote_cleanup_schedule.ticketId.filter(ticketId))[0];
  if (existing) return;
  tx.db.ticketremote_cleanup_schedule.insert({
    scheduled_id: 0n,
    scheduled_at: ScheduleAt.interval(CLEANUP_INTERVAL_MICROS),
    ticketId,
    batchSize: CLEANUP_BATCH_SIZE,
    createdAt: now,
    updatedAt: now,
  });
}

function clearPhoneBackends(tx: any, ticketId: string): void {
  for (const row of rowsFrom(tx.db.ticketremote_phone_backend.ticketId.filter(ticketId))) {
    tx.db.ticketremote_phone_backend.id.delete(row.id);
  }
}

function compactPhoneStreamState(desiredState: string, healthJson: string): string {
  const desired = String(desiredState || '').trim() || 'idle';
  const raw = String(healthJson || '').trim();
  if (!raw) return desired;
  try {
    const parsed = JSON.parse(raw);
    const data = parsed?.data || parsed || {};
    const verdict = String(data.streamVerdict || data.streamState || data.captureState || '').trim();
    if (verdict) return verdict;
    if (data.streamActive === true || data.connected === true) return 'streaming';
    if (data.streamActive === false) return 'idle';
  } catch (_) {
  }
  return desired;
}

function upsertPhoneStatus(tx: any, ticketId: string, backendId: string, attachName: string, desiredState: string, streamState: string, now: string, forceKeepalive: boolean): boolean {
  const existing = tx.db.ticketremote_phone_status.id.find(ticketId);
  const compactChanged = !existing ||
    existing.backendId !== backendId ||
    existing.attachName !== attachName ||
    existing.desiredState !== desiredState ||
    existing.streamState !== streamState ||
    existing.lastSeenAt !== now;
  if (!compactChanged && !forceKeepalive) return false;
  if (existing) tx.db.ticketremote_phone_status.id.delete(ticketId);
  tx.db.ticketremote_phone_status.insert({
    id: ticketId,
    ticketId,
    backendId,
    attachName,
    desiredState,
    streamState,
    lastSeenAt: now,
    updatedAt: now,
  });
  return compactChanged;
}

function appendPhoneHistory(tx: any, ticketId: string, backendId: string, attachName: string, desiredState: string, streamState: string, lastError: string, now: string): void {
  const stamp = String(now || '').replace(/[^0-9A-Za-z]/g, '') || 'time';
  const ordinal = nextAuditOrdinal(tx, `${ticketId}:phone`, now);
  tx.db.ticketremote_phone_status_history.insert({
    id: `${ticketId}:${stamp}:${ordinal}:phone`,
    ticketId,
    backendId,
    attachName,
    desiredState,
    streamState,
    lastError,
    createdAt: now,
    expiresAt: historyExpiresAt(now),
  });
}

function activePublicViewerRows(tx: any, ticketId: string, now: string): any[] {
  const nowMs = parseTime(now);
  return rowsFrom(tx.db.ticketremote_viewer_public.ticketId.filter(ticketId))
    .filter((row) => row.connected === true && parseTime(row.expiresAt) > nowMs)
    .sort((a, b) => String(a.publicId || '').localeCompare(String(b.publicId || '')));
}

function syncPublicTicketState(tx: any, ticketId: string, now: string): void {
  const ticket = ensureTicket(tx, ticketId, '', now);
  const viewers = activePublicViewerRows(tx, ticket.id, now);
  const phone = tx.db.ticketremote_phone_status.id.find(ticket.id);
  const existing = tx.db.ticketremote_ticket_summary.id.find(ticket.id);
  if (existing) tx.db.ticketremote_ticket_summary.id.delete(ticket.id);
  tx.db.ticketremote_ticket_summary.insert({
    id: ticket.id,
    ticketId: ticket.id,
    displayName: ticket.displayName,
    viewerCount: viewers.length,
    phoneBackendId: String(phone?.backendId || ''),
    phoneAttachName: String(phone?.attachName || ''),
    phoneDesiredState: String(phone?.desiredState || ''),
    phoneStreamState: String(phone?.streamState || ''),
    phoneLastSeenAt: String(phone?.lastSeenAt || ''),
    updatedAt: now,
  });
}

function upsertPresence(tx: any, ticketId: string, sessionId: string, email: string, displayName: string, page: string, connected: boolean, now: string): void {
  const clean = cleanEmail(email);
  const existing = tx.db.ticketremote_viewer_presence.sessionId.find(sessionId);
  if (existing) tx.db.ticketremote_viewer_presence.sessionId.delete(sessionId);
  const expiresAt = presenceExpiresAt(now);
  if (connected) {
    tx.db.ticketremote_viewer_presence.insert({
      sessionId,
      ticketId,
      email: clean,
      displayName: String(displayName || '').trim() || clean,
      page: String(page || '').trim() || 'ticket',
      connected: true,
      createdAt: existing?.createdAt || now,
      lastSeenAt: now,
      expiresAt,
    });
    const publicId = accountPublicId(clean);
    const publicRowId = publicPresenceRowId(ticketId, sessionId);
    const existingPublic = tx.db.ticketremote_viewer_public.id.find(publicRowId);
    if (existingPublic) tx.db.ticketremote_viewer_public.id.delete(publicRowId);
    tx.db.ticketremote_viewer_public.insert({
      id: publicRowId,
      ticketId,
      publicId,
      label: publicId,
      connected: true,
      lastSeenAt: now,
      expiresAt,
    });
  } else {
    tx.db.ticketremote_viewer_public.id.delete(publicPresenceRowId(ticketId, sessionId));
  }
  syncPublicTicketState(tx, ticketId, now);
}

function disconnectPresence(tx: any, ticketId: string, sessionId: string, now: string): void {
  tx.db.ticketremote_viewer_presence.sessionId.delete(sessionId);
  tx.db.ticketremote_viewer_public.id.delete(publicPresenceRowId(ticketId, sessionId));
  syncPublicTicketState(tx, ticketId, now);
}

function disconnectPresenceForEmail(tx: any, ticketId: string, email: string, now: string): void {
  const clean = cleanEmail(email);
  for (const row of rowsFrom(tx.db.ticketremote_viewer_presence.ticketId.filter(ticketId))) {
    if (cleanEmail(row.email) === clean) {
      tx.db.ticketremote_viewer_presence.sessionId.delete(row.sessionId);
      tx.db.ticketremote_viewer_public.id.delete(publicPresenceRowId(ticketId, row.sessionId));
    }
  }
  syncPublicTicketState(tx, ticketId, now);
}

function expireActiveControls(tx: any, ticketId: string, now: string): void {
  const nowMs = parseTime(now);
  for (const row of rowsFrom(tx.db.ticketremote_control_session.ticketId.filter(ticketId))) {
    if (row.state === 'active' && parseTime(row.expiresAt) <= nowMs) {
      tx.db.ticketremote_control_session.id.delete(row.id);
      tx.db.ticketremote_control_session.insert({
        ...row,
        state: 'expired',
        endedAt: now,
        endReason: 'timeout',
        expiresAt: historyExpiresAt(now),
      });
      audit(tx, ticketId, row.email, 'control_expired', serialize({ sessionId: row.sessionId }), now);
    }
  }
}

function cleanupExpired(tx: any, ticketId: string, now: string, batchSize = CLEANUP_BATCH_SIZE): number {
  const ticket = ensureTicket(tx, ticketId, '', now);
  const nowMs = parseTime(now);
  let deleted = 0;
  const limit = Math.max(1, Number(batchSize) || CLEANUP_BATCH_SIZE);
  const canDelete = () => deleted < limit;

  expireActiveControls(tx, ticket.id, now);

  for (const row of rowsFrom(tx.db.ticketremote_viewer_presence.ticketId.filter(ticket.id))) {
    if (!canDelete()) break;
    if (parseTime(row.expiresAt) <= nowMs) {
      tx.db.ticketremote_viewer_presence.sessionId.delete(row.sessionId);
      tx.db.ticketremote_viewer_public.id.delete(publicPresenceRowId(ticket.id, row.sessionId));
      deleted += 1;
    }
  }
  for (const row of rowsFrom(tx.db.ticketremote_viewer_public.ticketId.filter(ticket.id))) {
    if (!canDelete()) break;
    if (parseTime(row.expiresAt) <= nowMs) {
      tx.db.ticketremote_viewer_public.id.delete(row.id);
      deleted += 1;
    }
  }
  for (const row of rowsFrom(tx.db.ticketremote_control_session.ticketId.filter(ticket.id))) {
    if (!canDelete()) break;
    if (row.state !== 'active' && parseTime(row.expiresAt) <= nowMs) {
      tx.db.ticketremote_control_session.id.delete(row.id);
      deleted += 1;
    }
  }
  for (const row of rowsFrom(tx.db.ticketremote_audit_event.ticketId.filter(ticket.id))) {
    if (!canDelete()) break;
    if (parseTime(row.expiresAt) <= nowMs) {
      tx.db.ticketremote_audit_event.id.delete(row.id);
      deleted += 1;
    }
  }
  for (const row of rowsFrom(tx.db.ticketremote_phone_status_history.ticketId.filter(ticket.id))) {
    if (!canDelete()) break;
    if (parseTime(row.expiresAt) <= nowMs) {
      tx.db.ticketremote_phone_status_history.id.delete(row.id);
      deleted += 1;
    }
  }
  if (deleted > 0) syncPublicTicketState(tx, ticket.id, now);
  return deleted;
}

function remainingMs(expiresAt: string, now: string): number {
  const remaining = parseTime(expiresAt) - parseTime(now);
  return remaining > 0 ? remaining : 0;
}

function snapshot(tx: any, ticketId: string, now: string): string {
  const ticket = ensureTicket(tx, ticketId, '', now);
  expireActiveControls(tx, ticket.id, now);
  const nowMs = parseTime(now);
  const members = rowsFrom(tx.db.ticketremote_ticket_member.ticketId.filter(ticket.id))
    .sort((a, b) => cleanEmail(a.email).localeCompare(cleanEmail(b.email)))
    .map((row) => {
      const email = cleanEmail(row.email);
      return {
        email,
        publicId: accountPublicId(email),
        role: cleanRole(row.role),
        active: row.active === true,
        updatedAt: String(row.updatedAt || ''),
      };
    });
  const viewers = rowsFrom(tx.db.ticketremote_viewer_presence.ticketId.filter(ticket.id))
    .filter((row) => row.connected === true && parseTime(row.expiresAt) > nowMs)
    .sort((a, b) => cleanEmail(a.email).localeCompare(cleanEmail(b.email)))
    .map((row) => ({
      sessionId: String(row.sessionId || ''),
      email: cleanEmail(row.email),
      displayName: String(row.displayName || ''),
      page: String(row.page || ''),
      connected: row.connected === true,
      lastSeenAt: String(row.lastSeenAt || ''),
    }));
  const activeControlRow = rowsFrom(tx.db.ticketremote_control_session.ticketId.filter(ticket.id))
    .filter((row) => row.state === 'active' && parseTime(row.expiresAt) > nowMs)
    .sort((a, b) => parseTime(a.expiresAt) - parseTime(b.expiresAt))[0];
  const phoneRow = rowsFrom(tx.db.ticketremote_phone_backend.ticketId.filter(ticket.id))[0];
  const state: any = {
    ticket: {
      id: ticket.id,
      displayName: ticket.displayName || 'ViVi timed ticket',
      updatedAt: ticket.updatedAt || now,
    },
    members,
    viewers,
    serverTime: now,
    stateBackend: 'spacetime',
  };
  if (activeControlRow) {
    state.activeControl = {
      id: String(activeControlRow.id || ''),
      sessionId: String(activeControlRow.sessionId || ''),
      email: cleanEmail(activeControlRow.email),
      claimedAt: String(activeControlRow.claimedAt || ''),
      expiresAt: String(activeControlRow.expiresAt || ''),
      extended: activeControlRow.extended === true,
      remainingMs: remainingMs(activeControlRow.expiresAt, now),
    };
  }
  if (phoneRow) {
    state.phone = {
      id: String(phoneRow.backendId || phoneRow.id || ''),
      attachName: String(phoneRow.attachName || ''),
      baseUrl: String(phoneRow.baseUrl || ''),
      desiredState: String(phoneRow.desiredState || ''),
      healthJson: String(phoneRow.healthJson || ''),
      lastError: String(phoneRow.lastError || ''),
      lastSeenAt: String(phoneRow.lastSeenAt || ''),
    };
  }
  return serialize({ ok: true, state });
}

function applyPhoneUpdate(tx: any, args: any): any {
  const now = nowOr(args.now);
  const ticket = ensureTicket(tx, args.ticketId, '', now);
  const backendId = String(args.backendId || '').trim() || 'pixel';
  const id = phoneRowId(ticket.id, backendId);
  const attachName = String(args.attachName || '').trim() || backendId;
  const desiredState = String(args.desiredState || '').trim() || 'idle';
  const streamState = compactPhoneStreamState(desiredState, args.healthJson);
  const existing = tx.db.ticketremote_phone_backend.id.find(id);
  const keepaliveDue = existing ? parseTime(now) - parseTime(existing.lastSeenAt) >= PHONE_KEEPALIVE_MS : true;
  const unchanged = existing &&
    existing.attachName === attachName &&
    existing.baseUrl === String(args.baseUrl || '').trim() &&
    existing.desiredState === desiredState &&
    existing.streamState === streamState &&
    existing.healthJson === String(args.healthJson || '') &&
    existing.lastError === String(args.lastError || '');
  if (!unchanged || keepaliveDue) {
    if (existing) tx.db.ticketremote_phone_backend.id.delete(id);
    tx.db.ticketremote_phone_backend.insert({
      id,
      ticketId: ticket.id,
      backendId,
      attachName,
      baseUrl: String(args.baseUrl || '').trim(),
      desiredState,
      streamState,
      healthJson: String(args.healthJson || ''),
      lastError: String(args.lastError || ''),
      lastSeenAt: now,
    });
    const compactChanged = upsertPhoneStatus(tx, ticket.id, backendId, attachName, desiredState, streamState, now, keepaliveDue);
    if (compactChanged) appendPhoneHistory(tx, ticket.id, backendId, attachName, desiredState, streamState, String(args.lastError || ''), now);
    syncPublicTicketState(tx, ticket.id, now);
  }
  return ticket;
}

function configuredTicketId(tx: any): string {
  const rows = rowsFrom(tx.db.ticketremote_auth_config.iter());
  return String(rows[0]?.ticketId || 'vivi-default').trim() || 'vivi-default';
}

export const clientConnected = spacetimedb.clientConnected((ctx) => {
  if (hasServiceRole(ctx)) return;
  if (!ctx.senderAuth?.hasJWT) return;
  const now = serverNow(ctx);
  const ticketId = configuredTicketId(ctx);
  let email = '';
  try {
    email = clientEmailFromAuth(ctx, ticketId);
  } catch (_) {
    return;
  }
  upsertPresence(ctx, ticketId, connectionSessionId(ctx), email, email, 'ticket', true, now);
});

export const clientDisconnected = spacetimedb.clientDisconnected((ctx) => {
  if (hasServiceRole(ctx)) return;
  if (!ctx.senderAuth?.hasJWT) return;
  try {
    const now = serverNow(ctx);
    const ticketId = configuredTicketId(ctx);
    clientEmailFromAuth(ctx, ticketId);
    disconnectPresence(ctx, ticketId, connectionSessionId(ctx), now);
  } catch (_) {
  }
});

export const memberHeartbeatPresence = spacetimedb.reducer(
  { name: named('member_heartbeat_presence') },
  { ticketId: t.string(), displayName: t.string(), page: t.string(), connected: t.bool() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const sessionId = connectionSessionId(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    const email = clientEmailFromAuth(ctx, ticket.id);
    upsertPresence(ctx, ticket.id, sessionId, email, args.displayName, args.page, args.connected === true, now);
  }
);

export const memberDisconnectPresence = spacetimedb.reducer(
  { name: named('member_disconnect_presence') },
  { ticketId: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const sessionId = connectionSessionId(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    clientEmailFromAuth(ctx, ticket.id);
    disconnectPresence(ctx, ticket.id, sessionId, now);
  }
);

export const memberClaimControl = spacetimedb.reducer(
  { name: named('member_claim_control') },
  { ticketId: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    clientEmailFromAuth(ctx, ticket.id);
    throw new SenderError('control_mode_removed');
  }
);

export const memberExtendControl = spacetimedb.reducer(
  { name: named('member_extend_control') },
  { ticketId: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    clientEmailFromAuth(ctx, ticket.id);
    throw new SenderError('extension_disabled');
  }
);

export const memberReleaseControl = spacetimedb.reducer(
  { name: named('member_release_control') },
  { ticketId: t.string(), reason: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const sessionId = connectionSessionId(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    const email = clientEmailFromAuth(ctx, ticket.id);
    expireActiveControls(ctx, ticket.id, now);
    const active = rowsFrom(ctx.db.ticketremote_control_session.ticketId.filter(ticket.id)).find((row) => row.state === 'active');
    if (!active) return;
    if (active.email !== email || active.sessionId !== sessionId) throw new SenderError('not_controller');
    ctx.db.ticketremote_control_session.id.delete(active.id);
    ctx.db.ticketremote_control_session.insert({ ...active, state: 'released', endedAt: now, endReason: String(args.reason || 'released'), expiresAt: historyExpiresAt(now) });
    audit(ctx, ticket.id, email, 'control_released', serialize({ reason: args.reason, source: 'spacetime_client' }), now);
  }
);

export const memberRevokeControl = spacetimedb.reducer(
  { name: named('member_revoke_control') },
  { ticketId: t.string(), reason: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    const email = clientEmailFromAuth(ctx, ticket.id);
    if (!isAdmin(ctx, ticket.id, email)) throw new SenderError('forbidden');
    expireActiveControls(ctx, ticket.id, now);
    for (const active of rowsFrom(ctx.db.ticketremote_control_session.ticketId.filter(ticket.id)).filter((row) => row.state === 'active')) {
      ctx.db.ticketremote_control_session.id.delete(active.id);
      ctx.db.ticketremote_control_session.insert({ ...active, state: 'revoked', endedAt: now, endReason: String(args.reason || 'admin_revoked'), expiresAt: historyExpiresAt(now) });
    }
    audit(ctx, ticket.id, email, 'control_revoked', serialize({ reason: args.reason, source: 'spacetime_client' }), now);
  }
);

export const memberUpsertMember = spacetimedb.reducer(
  { name: named('member_upsert_member') },
  { ticketId: t.string(), email: t.string(), role: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    const actor = clientEmailFromAuth(ctx, ticket.id);
    if (!isAdmin(ctx, ticket.id, actor)) throw new SenderError('forbidden');
    const email = cleanEmail(args.email);
    if (!email) throw new SenderError('email required');
    const id = memberId(ticket.id, email);
    const existing = ctx.db.ticketremote_ticket_member.id.find(id);
    if (existing) ctx.db.ticketremote_ticket_member.id.delete(id);
    ctx.db.ticketremote_ticket_member.insert({ id, ticketId: ticket.id, email, role: cleanRole(args.role), active: true, createdAt: existing?.createdAt || now, updatedAt: now });
    audit(ctx, ticket.id, actor, 'member_upserted', serialize({ email, role: cleanRole(args.role), source: 'spacetime_client' }), now);
  }
);

export const memberRemoveMember = spacetimedb.reducer(
  { name: named('member_remove_member') },
  { ticketId: t.string(), email: t.string() },
  (ctx, args) => {
    const now = serverNow(ctx);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    const actor = clientEmailFromAuth(ctx, ticket.id);
    if (!isAdmin(ctx, ticket.id, actor)) throw new SenderError('forbidden');
    const id = memberId(ticket.id, args.email);
    const existing = ctx.db.ticketremote_ticket_member.id.find(id);
    if (existing) {
      ctx.db.ticketremote_ticket_member.id.delete(id);
      ctx.db.ticketremote_ticket_member.insert({ ...existing, active: false, updatedAt: now });
      disconnectPresenceForEmail(ctx, ticket.id, args.email, now);
    }
    audit(ctx, ticket.id, actor, 'member_removed', serialize({ email: cleanEmail(args.email), source: 'spacetime_client' }), now);
  }
);

export const serviceBootstrap = spacetimedb.reducer(
  { name: named('service_bootstrap') },
  { ticketId: t.string(), displayName: t.string(), adminEmail: t.string(), phoneBackendId: t.string(), phoneBaseUrl: t.string(), phoneAttachName: t.string(), authIssuer: t.string(), authAudience: t.string() },
  (ctx, args) => {
    const tx = ctx;
    requireService(tx);
    const now = serverNow(tx);
    const ticket = ensureTicket(tx, args.ticketId, args.displayName, now);
    const email = cleanEmail(args.adminEmail);
    if (email) {
      const id = memberId(ticket.id, email);
      const existing = tx.db.ticketremote_ticket_member.id.find(id);
      if (!existing) {
        tx.db.ticketremote_ticket_member.insert({ id, ticketId: ticket.id, email, role: 'owner', active: true, createdAt: now, updatedAt: now });
      }
    }
    if (String(args.phoneBackendId || '').trim()) {
      const backendId = String(args.phoneBackendId).trim();
      const attachName = String(args.phoneAttachName || '').trim() || backendId;
      clearPhoneBackends(tx, ticket.id);
      tx.db.ticketremote_phone_backend.insert({
        id: phoneRowId(ticket.id, backendId),
        ticketId: ticket.id,
        backendId,
        attachName,
        baseUrl: String(args.phoneBaseUrl || '').trim(),
        desiredState: 'idle',
        streamState: 'idle',
        healthJson: '',
        lastError: '',
        lastSeenAt: now,
      });
      upsertPhoneStatus(tx, ticket.id, backendId, attachName, 'idle', 'idle', now, true);
    }
    const issuer = String(args.authIssuer || '').trim();
    const audience = String(args.authAudience || '').trim();
    if (issuer && audience) {
      const existingAuth = tx.db.ticketremote_auth_config.ticketId.find(ticket.id);
      if (existingAuth) tx.db.ticketremote_auth_config.ticketId.delete(ticket.id);
      tx.db.ticketremote_auth_config.insert({ ticketId: ticket.id, issuer, audience, updatedAt: now });
    }
    ensureCleanupSchedule(tx, ticket.id, now);
    cleanupExpired(tx, ticket.id, now, CLEANUP_BATCH_SIZE);
    syncPublicTicketState(tx, ticket.id, now);
  }
);

export const scheduledCleanupExpired = spacetimedb.reducer(
  { name: named('scheduled_cleanup_expired') },
  { arg: ticketremote_cleanup_schedule.rowType },
  (ctx, { arg }) => {
    if (!hasInternalRole(ctx) && !hasServiceRole(ctx)) throw new SenderError('internal role required');
    const now = serverNow(ctx);
    cleanupExpired(ctx, arg.ticketId, now, Number(arg.batchSize) || CLEANUP_BATCH_SIZE);
  }
);

export const getState = spacetimedb.procedure(
  { name: named('get_state') },
  { ticketId: t.string(), now: t.string() },
  t.string(),
  (ctx, args) => ctx.withTx((tx: any) => {
    requireService(tx);
    return snapshot(tx, args.ticketId, nowOr(args.now));
  })
);

export const upsertMember = spacetimedb.reducer(
  { name: named('upsert_member') },
  { ticketId: t.string(), actorEmail: t.string(), email: t.string(), role: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    if (!isAdmin(ctx, ticket.id, args.actorEmail)) throw new SenderError('forbidden');
    const email = cleanEmail(args.email);
    const id = memberId(ticket.id, email);
    const existing = ctx.db.ticketremote_ticket_member.id.find(id);
    if (existing) ctx.db.ticketremote_ticket_member.id.delete(id);
    ctx.db.ticketremote_ticket_member.insert({ id, ticketId: ticket.id, email, role: cleanRole(args.role), active: true, createdAt: existing?.createdAt || now, updatedAt: now });
    audit(ctx, ticket.id, args.actorEmail, 'member_upserted', serialize({ email, role: cleanRole(args.role) }), now);
    syncPublicTicketState(ctx, ticket.id, now);
  }
);

export const removeMember = spacetimedb.reducer(
  { name: named('remove_member') },
  { ticketId: t.string(), actorEmail: t.string(), email: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    if (!isAdmin(ctx, ticket.id, args.actorEmail)) throw new SenderError('forbidden');
    const id = memberId(ticket.id, args.email);
    const existing = ctx.db.ticketremote_ticket_member.id.find(id);
    if (existing) {
      ctx.db.ticketremote_ticket_member.id.delete(id);
      ctx.db.ticketremote_ticket_member.insert({ ...existing, active: false, updatedAt: now });
      disconnectPresenceForEmail(ctx, ticket.id, args.email, now);
    }
    audit(ctx, ticket.id, args.actorEmail, 'member_removed', serialize({ email: cleanEmail(args.email) }), now);
    syncPublicTicketState(ctx, ticket.id, now);
  }
);

export const heartbeatPresence = spacetimedb.reducer(
  { name: named('heartbeat_presence') },
  { ticketId: t.string(), sessionId: t.string(), email: t.string(), displayName: t.string(), page: t.string(), connected: t.bool(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    if (!isMember(ctx, ticket.id, args.email)) throw new SenderError('not_member');
    upsertPresence(ctx, ticket.id, args.sessionId, args.email, args.displayName, args.page, args.connected === true, now);
  }
);

export const disconnectPresenceProcedure = spacetimedb.reducer(
  { name: named('disconnect_presence') },
  { ticketId: t.string(), sessionId: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    disconnectPresence(ctx, args.ticketId, args.sessionId, now);
  }
);

export const claimControl = spacetimedb.reducer(
  { name: named('claim_control') },
  { ticketId: t.string(), sessionId: t.string(), email: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    if (!isMember(ctx, ticket.id, args.email)) throw new SenderError('not_member');
    throw new SenderError('control_mode_removed');
  }
);

export const extendControl = spacetimedb.reducer(
  { name: named('extend_control') },
  { ticketId: t.string(), sessionId: t.string(), email: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    ensureTicket(ctx, args.ticketId, '', now);
    throw new SenderError('extension_disabled');
  }
);

export const releaseControl = spacetimedb.reducer(
  { name: named('release_control') },
  { ticketId: t.string(), sessionId: t.string(), email: t.string(), reason: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    const active = rowsFrom(ctx.db.ticketremote_control_session.ticketId.filter(ticket.id)).find((row) => row.state === 'active');
    if (!active) return;
    const actorEmail = cleanEmail(args.email);
    if (actorEmail && active.email !== actorEmail) throw new SenderError('not_controller');
    ctx.db.ticketremote_control_session.id.delete(active.id);
    ctx.db.ticketremote_control_session.insert({ ...active, state: 'released', endedAt: now, endReason: String(args.reason || 'released'), expiresAt: historyExpiresAt(now) });
    audit(ctx, ticket.id, args.email || active.email, 'control_released', serialize({ reason: args.reason }), now);
  }
);

export const revokeControl = spacetimedb.reducer(
  { name: named('revoke_control') },
  { ticketId: t.string(), actorEmail: t.string(), reason: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    const ticket = ensureTicket(ctx, args.ticketId, '', now);
    if (!isAdmin(ctx, ticket.id, args.actorEmail)) throw new SenderError('forbidden');
    for (const active of rowsFrom(ctx.db.ticketremote_control_session.ticketId.filter(ticket.id)).filter((row) => row.state === 'active')) {
      ctx.db.ticketremote_control_session.id.delete(active.id);
      ctx.db.ticketremote_control_session.insert({ ...active, state: 'revoked', endedAt: now, endReason: String(args.reason || 'admin_revoked'), expiresAt: historyExpiresAt(now) });
    }
    audit(ctx, ticket.id, args.actorEmail, 'control_revoked', serialize({ reason: args.reason }), now);
  }
);

export const updatePhone = spacetimedb.reducer(
  { name: named('update_phone') },
  { ticketId: t.string(), backendId: t.string(), attachName: t.string(), baseUrl: t.string(), desiredState: t.string(), healthJson: t.string(), lastError: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    applyPhoneUpdate(ctx, args);
  }
);

export const updatePhoneStatus = spacetimedb.reducer(
  { name: named('update_phone_status') },
  { ticketId: t.string(), backendId: t.string(), attachName: t.string(), baseUrl: t.string(), desiredState: t.string(), healthJson: t.string(), lastError: t.string(), now: t.string() },
  (ctx, args) => {
    requireService(ctx);
    applyPhoneUpdate(ctx, args);
  }
);

export const cleanupExpiredNow = spacetimedb.reducer(
  { name: named('cleanup_expired') },
  { ticketId: t.string(), now: t.string(), batchSize: t.u32() },
  (ctx, args) => {
    requireService(ctx);
    const now = nowOr(args.now);
    cleanupExpired(ctx, args.ticketId, now, Number(args.batchSize) || CLEANUP_BATCH_SIZE);
  }
);

export const auditEvent = spacetimedb.reducer(
  { name: named('audit') },
  { ticketId: t.string(), actorEmail: t.string(), event: t.string(), payloadJson: t.string(), now: t.string() },
  (ctx, args) => {
    const tx = ctx;
    requireService(tx);
    const now = nowOr(args.now);
    audit(tx, args.ticketId, args.actorEmail, args.event, args.payloadJson || '{}', now);
    cleanupExpired(tx, args.ticketId, now, CLEANUP_BATCH_SIZE);
  }
);
