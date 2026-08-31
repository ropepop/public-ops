import assert from 'node:assert/strict';
import test from 'node:test';

import { ClientHDRBoostPreferenceController } from './client-hdr-boost-preference.mjs';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function harness() {
  const applies = [];
  const writes = [];
  const statuses = [];
  const failures = [];
  const controller = new ClientHDRBoostPreferenceController({
    applyBoost(boost, meta) { applies.push({ boost, reason: meta.reason }); },
    persistBoost(boost) {
      const write = deferred();
      writes.push({ boost, ...write });
      return write.promise;
    },
    onStatus(state) { statuses.push({ ...state }); },
    onFailure(failure) { failures.push({ code: failure.code, state: { ...failure.state } }); }
  });
  return { applies, controller, failures, statuses, writes };
}

async function startWrites() {
  await Promise.resolve();
  await Promise.resolve();
}

test('defaults retired, invalid, and missing projections to 4x', () => {
  const state = harness();
  assert.equal(state.controller.boost, 4);
  assert.deepEqual(state.applies, [{ boost: 4, reason: 'default' }]);
  for (const retired of [1, 8, 10, 12, 14, 16, null, 'invalid']) {
    assert.equal(state.controller.observe(retired), true);
    assert.equal(state.controller.boost, 4);
  }
  assert.equal(state.controller.getState().phase, 'synced');
});

test('stale unrelated projections cannot undo a pending or saved boost choice', async () => {
  const state = harness();
  state.controller.observe(6);
  state.controller.choose(3);
  await startWrites();
  for (let index = 0; index < 20; index += 1) {
    assert.equal(state.controller.observe(6), false);
    assert.equal(state.controller.boost, 3);
  }
  assert.deepEqual(state.writes.map(({ boost }) => boost), [3]);
  state.writes[0].resolve();
  await state.controller.whenIdle();
  assert.equal(state.controller.getState().phase, 'saved');
  assert.equal(state.controller.observe(6), false);
  assert.equal(state.controller.boost, 3);
  assert.equal(state.controller.observe(3), true);
  assert.equal(state.controller.getState().phase, 'synced');
});

test('rapid level choices keep one write active and coalesce to the latest level', async () => {
  const state = harness();
  state.controller.observe(6);
  state.controller.choose(2);
  await startWrites();
  state.controller.choose(3);
  state.controller.choose(4);
  state.controller.choose(5);
  assert.deepEqual(state.writes.map(({ boost }) => boost), [2]);
  assert.equal(state.controller.boost, 5);
  state.writes[0].resolve();
  await startWrites();
  assert.deepEqual(state.writes.map(({ boost }) => boost), [2, 5]);
  state.writes[1].resolve();
  await state.controller.whenIdle();
  assert.equal(state.controller.boost, 5);
  assert.equal(state.controller.getState().phase, 'saved');
  state.controller.observe(5);
  assert.equal(state.controller.getState().localOverride, false);
});

test('retired user choices are persisted only as the 4x fallback', async () => {
  const state = harness();
  state.controller.observe(6);
  state.controller.choose(16);
  await startWrites();
  assert.equal(state.controller.boost, 4);
  assert.deepEqual(state.writes.map(({ boost }) => boost), [4]);
  state.writes[0].resolve();
  await state.controller.whenIdle();
  assert.equal(state.controller.getState().phase, 'saved');
});

test('a failed save keeps the live session level and retries only after another choice', async () => {
  const state = harness();
  state.controller.observe(6);
  state.controller.choose(4);
  await startWrites();
  state.writes[0].reject(new Error('private backend detail'));
  await state.controller.whenIdle();
  assert.equal(state.controller.boost, 4);
  assert.equal(state.controller.getState().phase, 'failed');
  assert.equal(state.failures.length, 1);
  assert.equal(JSON.stringify(state.failures).includes('private backend detail'), false);
  assert.equal(state.controller.observe(6), false);
  await startWrites();
  assert.equal(state.writes.length, 1);
  state.controller.choose(4);
  await startWrites();
  assert.deepEqual(state.writes.map(({ boost }) => boost), [4, 4]);
});

test('reset ignores a late write completion and restores a safe fresh-session default', async () => {
  const state = harness();
  state.controller.observe(2);
  state.controller.choose(3);
  await startWrites();
  state.controller.reset();
  assert.equal(state.controller.boost, 4);
  assert.equal(state.controller.getState().localOverride, false);
  state.writes[0].resolve();
  await startWrites();
  assert.equal(state.controller.boost, 4);
  assert.equal(state.controller.getState().phase, 'default');
});
