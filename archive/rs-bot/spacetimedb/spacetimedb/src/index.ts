// @ts-nocheck
import {
  CaseConversionPolicy,
  SenderError,
  schema,
  table,
  t,
} from 'spacetimedb/server';

const PREFIX = 'rigassatiksmeqrbot_';
const STATE_ID = 'access';

function named(suffix: string): string {
  return `${PREFIX}${suffix}`;
}

const rigassatiksmeqrbot_access_state = table(
  { name: named('access_state') },
  {
    id: t.string().primaryKey(),
    stateJson: t.string(),
    updatedAt: t.string().index(),
  }
);

const rigassatiksmeqrbot_access_audit = table(
  { name: named('access_audit') },
  {
    id: t.string().primaryKey(),
    event: t.string().index(),
    payloadJson: t.string(),
    createdAt: t.string().index(),
  }
);

const spacetimedb: any = schema(
  {
    rigassatiksmeqrbot_access_state,
    rigassatiksmeqrbot_access_audit,
  },
  { CASE_CONVERSION_POLICY: CaseConversionPolicy.None }
);

export default spacetimedb;

function requireService(tx: any): void {
  if (hasServiceRole(tx)) return;
  throw new SenderError('service role required');
}

function hasServiceRole(tx: any): boolean {
  const auth = tx.senderAuth;
  const roles = auth?.jwt?.fullPayload?.roles;
  return Boolean(auth?.hasJWT && Array.isArray(roles) && roles.includes('rigassatiksmeqrbot_service'));
}

function now(ctx: any): string {
  const stamp = ctx.timestamp;
  const text = stamp && typeof stamp.toISOString === 'function' ? stamp.toISOString() : String(stamp || '').trim();
  return text || new Date().toISOString();
}

function parseState(value: string): any | null {
  const clean = String(value || '').trim();
  if (!clean) return null;
  return JSON.parse(clean);
}

function serializeStateJson(value: string): string {
  const text = String(value || '').trim();
  if (!text) return JSON.stringify({ version: 1, admins: {}, users: {}, groups: {}, chats: {}, usage: {} });
  const state = JSON.parse(text);
  if (!state.version) state.version = 1;
  return JSON.stringify(state);
}

function rowsFrom(iterable: any): any[] {
  return Array.from(iterable as Iterable<any>) as any[];
}

function audit(tx: any, event: string, payload: any, createdAt: string): void {
  const ordinal = String(rowsFrom(tx.db.rigassatiksmeqrbot_access_audit.iter()).length + 1).padStart(12, '0');
  tx.db.rigassatiksmeqrbot_access_audit.insert({
    id: `${createdAt}:${ordinal}`,
    event,
    payloadJson: JSON.stringify(payload || {}),
    createdAt,
  });
}

export const importAccessState = spacetimedb.reducer(
  { name: named('import_access_state') },
  { stateJson: t.string() },
  (ctx, args) => {
    const tx = ctx;
    requireService(tx);
    const updatedAt = now(ctx);
    const existing = tx.db.rigassatiksmeqrbot_access_state.id.find(STATE_ID);
    if (existing) tx.db.rigassatiksmeqrbot_access_state.id.delete(STATE_ID);
    tx.db.rigassatiksmeqrbot_access_state.insert({
      id: STATE_ID,
      stateJson: serializeStateJson(args.stateJson),
      updatedAt,
    });
    audit(tx, 'access_state_imported', { updatedAt }, updatedAt);
  }
);

export const exportAccessState = spacetimedb.procedure(
  { name: named('export_access_state') },
  {},
  t.string(),
  (ctx) => ctx.withTx((tx) => {
    requireService(tx);
    const row = tx.db.rigassatiksmeqrbot_access_state.id.find(STATE_ID);
    const state = row ? parseState(row.stateJson) : null;
    return JSON.stringify({ state });
  })
);

export const bootstrapAdmin = spacetimedb.reducer(
  { name: named('bootstrap_admin') },
  { userId: t.string() },
  (ctx, args) => {
    const tx = ctx;
    requireService(tx);
    const updatedAt = now(ctx);
    const row = tx.db.rigassatiksmeqrbot_access_state.id.find(STATE_ID);
    const state = row ? parseState(row.stateJson) : { version: 1, admins: {}, users: {}, groups: {}, chats: {}, usage: {} };
    const userId = String(args.userId || '').trim();
    if (!userId) throw new SenderError('userId required');
    state.admins = state.admins || {};
    state.admins[userId] = true;
    state.updatedAt = updatedAt;
    if (row) tx.db.rigassatiksmeqrbot_access_state.id.delete(STATE_ID);
    tx.db.rigassatiksmeqrbot_access_state.insert({ id: STATE_ID, stateJson: JSON.stringify(state), updatedAt });
    audit(tx, 'admin_bootstrapped', { userId }, updatedAt);
  }
);
