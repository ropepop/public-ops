import test from 'node:test';
import assert from 'node:assert/strict';
import { ActivityOutbox, PageActivity, firstRetainedActivitySlot } from './page-activity.mjs';

const start = Date.parse('2026-09-20T12:00:00Z');
const baseSlot = start / 5000;

function fixture() {
  const state = { now: 0, wall: 0, visible: true, online: true, calls: [], diagnostics: [], reloads: 0,
    pending: new Set(), timers: new Map(), nextTimer: 0 };
  const outbox = {
    async put(slot) { state.pending.add(slot); },
    async prune(first) { for (const slot of state.pending) if (slot < first) state.pending.delete(slot); },
    async pending(first) { return [...state.pending].filter(slot => slot >= first).sort((a, b) => a - b).slice(0, 720); },
    async remove(slots) { for (const slot of slots) state.pending.delete(slot); }
  };
  const config = { accountScopeId: 'scope-a', ticketId: 'ticket-a', pageVersion: 'v1', serverTime: new Date(start).toISOString() };
  const reply = (body, extra = {}) => ({ ok: true, status: 200, json: async () => ({
    accountScopeId: config.accountScopeId, serverTime: new Date(start + state.now).toISOString(),
    serverVersion: 'v1', acknowledgedSlots: body.slots, discardedSlots: [], ...extra
  }) });
  state.respond = async body => reply(body);
  const activity = new PageActivity({ config, outbox,
    now: () => state.now, wallNow: () => start + state.wall,
    visible: () => state.visible, online: () => state.online,
    diagnostic: reason => state.diagnostics.push(reason), reload: () => state.reloads++,
    setTimer: fn => { const id = ++state.nextTimer; state.timers.set(id, fn); return id; },
    clearTimer: id => state.timers.delete(id),
    fetch: async (url, options) => {
      const body = JSON.parse(options.body);
      state.calls.push({ url, body, options });
      return state.respond(body);
    }
  });
  const advance = milliseconds => { state.now += milliseconds; state.wall += milliseconds; };
  return { state, activity, outbox, reply, advance };
}

test('foreground collection starts immediately and sends only observed five-second slots', async () => {
  const { state, activity, advance } = fixture();
  await activity.tick();
  assert.deepEqual(state.calls[0].body.slots, [baseSlot]);
  assert.equal(state.calls[0].options.credentials, 'same-origin');
  advance(4999); await activity.tick();
  assert.equal(state.calls.length, 1);
  advance(1); await activity.tick();
  state.visible = false; advance(15000); await activity.tick();
  assert.equal(state.calls.length, 2);
  state.visible = true; await activity.wake();
  assert.deepEqual(state.calls[2].body.slots, [baseSlot + 4]);
  advance(60000); await activity.tick();
  assert.deepEqual(state.calls[3].body.slots, [baseSlot + 16], 'suspension never invents catch-up samples');
});

test('offline slots persist before sending and drain in bounded batches after reconnection', async () => {
  const { state, activity, advance } = fixture();
  state.online = false;
  for (let i = 0; i < 725; i++) { await activity.tick(); advance(5000); }
  assert.equal(state.pending.size, 725);
  assert.equal(state.calls.length, 0);
  state.online = true; await activity.wake();
  assert.deepEqual(state.calls.map(call => call.body.slots.length), [720, 6]);
  assert.equal(state.pending.size, 0);
});

test('a stalled send cannot block sampling and expires even if fetch ignores cancellation', async () => {
  const { state, activity, advance, reply } = fixture();
  let resolve;
  state.respond = () => new Promise(done => { resolve = done; });
  const first = activity.tick();
  for (let i = 0; i < 8; i++) await Promise.resolve();
  advance(5000); await activity.observe();
  activity.deliver();
  assert.equal(state.calls.length, 1);
  assert.equal(state.pending.size, 2);
  [...state.timers.values()][0]();
  await first;
  assert.equal(state.pending.size, 2);
  assert.ok(state.calls[0].options.signal.aborted);
  state.respond = async body => reply(body);
  advance(5000); await activity.tick();
  assert.deepEqual(state.calls[1].body.slots, [baseSlot, baseSlot + 1, baseSlot + 2]);
  resolve({ ok: false, status: 403 });
  await Promise.resolve(); await Promise.resolve();
  assert.equal(activity.authBlocked, false, 'late obsolete responses cannot change the current auth state');
  assert.equal(state.pending.size, 0);
});

test('lost and partial acknowledgements retain samples and only exact sent slots are deleted', async () => {
  const { state, activity, advance, reply } = fixture();
  state.respond = async () => { throw Error('network'); };
  await activity.tick();
  advance(5000);
  state.respond = async body => reply(body, { acknowledgedSlots: [baseSlot, baseSlot + 500], discardedSlots: [] });
  await activity.tick();
  assert.deepEqual([...state.pending], [baseSlot + 1]);
  state.respond = async body => reply(body, { acknowledgedSlots: [], discardedSlots: body.slots });
  await activity.tick();
  assert.equal(state.pending.size, 0);
});

test('account changes preserve data and require a matching empty authentication probe', async () => {
  const { state, activity, advance, reply } = fixture();
  state.respond = async () => ({ ok: false, status: 403 });
  await activity.tick();
  advance(5000); await activity.tick();
  assert.equal(state.calls.length, 1);
  assert.equal(state.pending.size, 2);
  state.respond = async body => reply(body, { accountScopeId: 'scope-b' });
  await activity.wake();
  assert.deepEqual(state.calls[1].body.slots, []);
  assert.equal(state.pending.size, 2);
  state.respond = async body => reply(body);
  await activity.wake();
  assert.deepEqual(state.calls[2].body.slots, []);
  assert.deepEqual(state.calls[3].body.slots, [baseSlot, baseSlot + 1]);
  assert.equal(state.pending.size, 0);
});

test('restored authentication recovers on the existing timer with bounded empty probes', async () => {
  for (const status of [401, 403]) {
    const { state, activity, advance, reply } = fixture();
    state.respond = async () => ({ ok: false, status });
    await activity.tick();
    for (let i = 0; i < 5; i++) { advance(5000); await activity.tick(); }
    assert.equal(state.calls.length, 1, 'blocked requests wait thirty seconds');
    advance(5000); await activity.tick();
    assert.deepEqual(state.calls[1].body.slots, [], 'blocked timer sends only an identity probe');
    state.respond = async body => reply(body);
    for (let i = 0; i < 5; i++) { advance(5000); await activity.tick(); }
    assert.equal(state.calls.length, 2, 'failed probes stay bounded');
    advance(5000); await activity.tick();
    assert.deepEqual(state.calls[2].body.slots, []);
    assert.equal(state.calls[3].body.slots.length, 13, 'matching restored identity drains saved slots');
    assert.equal(state.pending.size, 0);
  }
});

test('offline collection prunes expired data without needing a delivery attempt', async () => {
  const { state, activity, advance } = fixture();
  const outbox = new ActivityOutbox({ accountScopeId: 'scope-a', ticketId: 'ticket-a', indexedDB: null });
  activity.outbox = outbox;
  state.online = false;
  await activity.tick();
  assert.deepEqual([...outbox.memory], [baseSlot]);
  advance(31 * 24 * 60 * 60 * 1000);
  await activity.tick();
  assert.deepEqual([...outbox.memory], [baseSlot + 31 * 24 * 60 * 60 / 5]);
  assert.equal(state.calls.length, 0);
});

test('version refresh waits for persistence and waits for a visible online page', async () => {
  const { state, activity, outbox, advance } = fixture();
  let persist;
  outbox.put = slot => new Promise(resolve => { persist = () => { state.pending.add(slot); resolve(); }; });
  const version = activity.version('v2');
  await Promise.resolve();
  assert.equal(state.reloads, 0);
  persist(); await version;
  assert.equal(state.reloads, 1);
  assert.deepEqual([...state.pending], [baseSlot]);
  await activity.version('v3');
  assert.equal(state.reloads, 1, 'one reload owner');

  const hidden = fixture();
  hidden.state.visible = false;
  await hidden.activity.version('v2');
  assert.equal(hidden.state.reloads, 0);
  hidden.advance(5000); hidden.state.visible = true;
  await hidden.activity.wake();
  assert.equal(hidden.state.reloads, 1);
});

test('ambiguous clock changes require a fresh server anchor without fabricating elapsed time', async () => {
  const { state, activity, advance } = fixture();
  await activity.tick();
  state.wall += 3600000;
  advance(5000); await activity.tick();
  assert.deepEqual(state.calls[1].body.slots, [], 'clock probe precedes resumed sampling');
  assert.deepEqual(state.calls[2].body.slots, [baseSlot + 1]);
});

test('retention uses 30 Riga calendar days across both daylight-saving transitions', () => {
  assert.equal(firstRetainedActivitySlot(Date.parse('2026-04-26T12:00:00Z')), Date.parse('2026-03-27T22:00:00Z') / 5000);
  assert.equal(firstRetainedActivitySlot(Date.parse('2026-04-27T12:00:00Z')), Date.parse('2026-03-28T22:00:00Z') / 5000);
  assert.equal(firstRetainedActivitySlot(Date.parse('2026-11-23T12:00:00Z')), Date.parse('2026-10-24T21:00:00Z') / 5000);
  assert.equal(firstRetainedActivitySlot(Date.parse('2026-11-24T12:00:00Z')), Date.parse('2026-10-25T22:00:00Z') / 5000);
});

test('unavailable persistent storage keeps an in-memory outbox with one bounded diagnostic', async () => {
  const diagnostics = [];
  const outbox = new ActivityOutbox({ accountScopeId: 'a', ticketId: 't', indexedDB: null,
    diagnostic: reason => diagnostics.push(reason) });
  await outbox.put(2); await outbox.put(3); await outbox.put(3);
  assert.deepEqual(await outbox.pending(3), [3]);
  await outbox.remove([3]);
  assert.deepEqual(await outbox.pending(3), []);
  assert.deepEqual(diagnostics, ['activity_storage_unavailable']);
});
