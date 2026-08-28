import assert from 'node:assert/strict';
import test from 'node:test';

import { ExperimentalHDRPreferenceController } from './experimental-hdr-preference.mjs';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function harness(options = {}) {
  const applies = [];
  const writes = [];
  const statuses = [];
  const failures = [];
  const controller = new ExperimentalHDRPreferenceController({
    applyEnabled(enabled, meta) {
      applies.push({ enabled, reason: meta.reason });
    },
    persistEnabled(enabled) {
      const write = deferred();
      writes.push({ enabled, ...write });
      return write.promise;
    },
    onStatus(state) {
      statuses.push({ ...state });
    },
    onFailure(failure) {
      failures.push({ code: failure.code, state: { ...failure.state } });
    },
    ...options
  });
  return { applies, controller, failures, statuses, writes };
}

async function startWrites() {
  await Promise.resolve();
  await Promise.resolve();
}

test('defaults off before a projection and treats a missing preference as off', () => {
  const state = harness();

  assert.equal(state.controller.enabled, false);
  assert.deepEqual(state.applies, [{ enabled: false, reason: 'default' }]);
  assert.deepEqual(state.controller.getState(), {
    phase: 'default',
    enabled: false,
    desiredEnabled: false,
    projectionKnown: false,
    projectedEnabled: null,
    localOverride: false,
    inFlight: false,
    inFlightValue: null,
    failed: false
  });

  assert.equal(state.controller.observe(undefined), true);
  assert.equal(state.controller.enabled, false);
  assert.deepEqual(state.applies, [{ enabled: false, reason: 'default' }]);
  assert.equal(state.controller.getState().phase, 'synced');
  assert.equal(state.controller.getState().projectedEnabled, false);
});

test('an account projection applies when no local choice is protected', () => {
  const state = harness();

  assert.equal(state.controller.observe(true), true);
  assert.equal(state.controller.enabled, true);
  assert.deepEqual(state.applies.at(-1), { enabled: true, reason: 'projection' });

  assert.equal(state.controller.observe(false), true);
  assert.equal(state.controller.enabled, false);
  assert.deepEqual(state.applies.at(-1), { enabled: false, reason: 'projection' });
});

test('a user choice applies immediately and saves asynchronously', async () => {
  const state = harness();
  state.controller.observe(false);

  state.controller.choose(true);
  assert.equal(state.controller.enabled, true);
  assert.deepEqual(state.applies.at(-1), { enabled: true, reason: 'user' });
  assert.equal(state.controller.getState().phase, 'saving');
  assert.equal(state.writes.length, 0, 'persistence starts outside the input event stack');

  await startWrites();
  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true]);
  state.writes[0].resolve();
  await state.controller.whenIdle();

  assert.equal(state.controller.enabled, true);
  assert.equal(state.controller.getState().phase, 'saved');
  assert.equal(state.controller.getState().localOverride, true);

  assert.equal(state.controller.observe(true), true);
  assert.equal(state.controller.getState().phase, 'synced');
  assert.equal(state.controller.getState().localOverride, false);
});

test('rapid toggles keep one write active and coalesce to the latest different value', async () => {
  const state = harness();
  state.controller.observe(false);

  state.controller.choose(true);
  await startWrites();
  state.controller.choose(false);
  state.controller.choose(true);
  state.controller.choose(false);

  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true]);
  assert.equal(state.controller.enabled, false);
  state.writes[0].resolve();
  await startWrites();
  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true, false]);

  state.writes[1].resolve();
  await state.controller.whenIdle();
  assert.equal(state.controller.enabled, false);
  assert.equal(state.controller.getState().phase, 'synced');
  assert.equal(state.controller.getState().localOverride, false);
});

test('returning to the in-flight value needs no duplicate write after success', async () => {
  const state = harness();
  state.controller.observe(false);

  state.controller.choose(true);
  await startWrites();
  state.controller.choose(false);
  state.controller.choose(true);
  state.controller.observe(true);
  state.writes[0].resolve();
  await state.controller.whenIdle();

  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true]);
  assert.equal(state.controller.enabled, true);
  assert.equal(state.controller.getState().localOverride, false);
  assert.equal(state.controller.getState().phase, 'synced');
});

test('stale projections cannot undo a pending local choice', async () => {
  const state = harness();
  state.controller.observe(false);
  state.controller.choose(true);
  await startWrites();

  assert.equal(state.controller.observe(false), false);
  assert.equal(state.controller.enabled, true);
  assert.deepEqual(state.applies.at(-1), { enabled: true, reason: 'user' });

  state.writes[0].resolve();
  await state.controller.whenIdle();
  assert.equal(state.controller.observe(false), false);
  assert.equal(state.controller.enabled, true);

  assert.equal(state.controller.observe(true), true);
  assert.equal(state.controller.getState().localOverride, false);
});

test('a write failure keeps the in-session choice, ignores projections, and does not retry', async () => {
  const state = harness();
  const failure = new Error('database unavailable');
  state.controller.observe(false);
  state.controller.choose(true);
  await startWrites();

  state.writes[0].reject(failure);
  await state.controller.whenIdle();

  assert.equal(state.controller.enabled, true);
  assert.equal(state.controller.getState().phase, 'failed');
  assert.equal(state.controller.getState().failed, true);
  assert.equal(state.controller.getState().localOverride, true);
  assert.equal(state.failures.length, 1);
  assert.equal(state.failures[0].code, 'hdr_preference_write_failed');
  assert.equal(JSON.stringify(state.failures).includes(failure.message), false);

  assert.equal(state.controller.observe(false), false);
  assert.equal(state.controller.observe(true), false);
  await startWrites();
  assert.equal(state.writes.length, 1, 'projections do not trigger a retry loop');
  assert.equal(state.controller.enabled, true);
});

test('only another explicit choice retries a failed value and matching projection settles it', async () => {
  const state = harness();
  state.controller.observe(false);
  state.controller.choose(true);
  await startWrites();
  state.writes[0].reject(new Error('first write failed'));
  await state.controller.whenIdle();

  state.controller.observe(true);
  await startWrites();
  assert.equal(state.writes.length, 1);

  state.controller.choose(true);
  assert.deepEqual(state.applies.at(-1), { enabled: true, reason: 'user' });
  await startWrites();
  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true, true]);
  state.writes[1].resolve();
  await state.controller.whenIdle();

  assert.equal(state.controller.getState().phase, 'synced');
  assert.equal(state.controller.getState().localOverride, false);
  assert.equal(state.failures.length, 1);
});

test('a newer queued choice proceeds once after an obsolete write failure', async () => {
  const state = harness();
  state.controller.observe(false);
  state.controller.choose(true);
  await startWrites();
  state.controller.choose(false);

  state.writes[0].reject(new Error('obsolete true failed'));
  await startWrites();
  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true, false]);
  assert.equal(state.failures.length, 1);

  state.writes[1].resolve();
  await state.controller.whenIdle();
  assert.equal(state.controller.enabled, false);
  assert.equal(state.controller.getState().phase, 'synced');
});

test('a newer explicit choice retries the same value once if the active write fails', async () => {
  const state = harness();
  state.controller.observe(false);
  state.controller.choose(true);
  await startWrites();
  state.controller.choose(true);

  state.writes[0].reject(new Error('first true failed'));
  await startWrites();
  assert.deepEqual(state.writes.map(({ enabled }) => enabled), [true, true]);

  state.writes[1].reject(new Error('second true failed'));
  await state.controller.whenIdle();
  await startWrites();
  assert.equal(state.writes.length, 2, 'the failed retry stops without a loop');
  assert.equal(state.controller.enabled, true);
  assert.equal(state.failures.length, 2);
});

test('after a matching projection settles the local write, later device projections apply', async () => {
  const state = harness();
  state.controller.observe(false);
  state.controller.choose(true);
  await startWrites();
  state.writes[0].resolve();
  await state.controller.whenIdle();
  state.controller.observe(true);

  assert.equal(state.controller.getState().localOverride, false);
  assert.equal(state.controller.observe(false), true);
  assert.equal(state.controller.enabled, false);
  assert.deepEqual(state.applies.at(-1), { enabled: false, reason: 'projection' });
});

test('status snapshots contain bounded state and never include failure details', async () => {
  const state = harness();
  state.controller.choose(true);
  await startWrites();
  state.writes[0].reject(new Error('secret backend detail'));
  await state.controller.whenIdle();

  assert.deepEqual(state.statuses.map(({ phase }) => phase), ['default', 'saving', 'failed']);
  assert.equal(JSON.stringify(state.statuses).includes('secret backend detail'), false);
});

test('advisory callback failures cannot break preference failure containment', async () => {
  const applies = [];
  const writes = [];
  const controller = new ExperimentalHDRPreferenceController({
    applyEnabled(enabled) {
      applies.push(enabled);
    },
    persistEnabled(enabled) {
      writes.push(enabled);
      return Promise.reject(new Error('database unavailable'));
    },
    onStatus() {
      throw new Error('status renderer unavailable');
    },
    onFailure() {
      throw new Error('logger unavailable');
    }
  });

  controller.choose(true);
  await controller.whenIdle();

  assert.deepEqual(applies, [false, true]);
  assert.deepEqual(writes, [true]);
  assert.equal(controller.enabled, true);
  assert.equal(controller.getState().phase, 'failed');
});
