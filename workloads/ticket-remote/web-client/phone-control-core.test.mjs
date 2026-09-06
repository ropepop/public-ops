import test from 'node:test';
import assert from 'node:assert/strict';
import { phoneControlNow, phoneControlReady, phoneRegistrationRegion, phoneRegistrationSnapshot, phoneRegistrationMatches } from './phone-control-core.mjs';

const now = Date.parse('2026-09-06T12:00:00Z');
const observation = {
  sessionId: 'pc-session', sessionGeneration: '1', contextRevision: 'pc-session:1',
  observationSequence: '1', ready: true, busy: false, view: 'unactivated_detail',
  observedAt: new Date(now).toISOString(), expiresAt: new Date(now + 3000).toISOString(),
  leftBasisPoints: 1000, topBasisPoints: 7000, rightBasisPoints: 9000, bottomBasisPoints: 8000,
};

test('database clock ages monotonically and rejects suspend, wall-clock changes, and expired calibration', () => {
  const clock = { serverUpperAtReceipt: now, receivedMonotonic: 1000, receivedWall: now + 100000 };
  assert.equal(phoneControlNow(clock, 2000, now + 101000), now + 1000);
  assert.ok(Number.isNaN(phoneControlNow(null, 2000, now)));
  assert.ok(Number.isNaN(phoneControlNow(clock, 31000, now + 130000)));
  assert.ok(Number.isNaN(phoneControlNow(clock, 2000, now + 110000)));
  assert.ok(Number.isNaN(phoneControlNow(clock, 999, now + 100000)));
});

test('readiness needs no encoder, video, or HDR state and expires at the source deadline', () => {
  assert.equal(phoneControlReady(observation, now), true);
  assert.equal(phoneControlReady(observation, now + 2999), true);
  assert.equal(phoneControlReady(observation, now + 3000), false);
  assert.equal(phoneControlReady(observation, now - 1), false);
  assert.equal(phoneControlReady({ ...observation, expiresAt: new Date(now + 3001).toISOString() }, now), false);
  for (const change of [{ busy: true }, { ready: false }, { view: 'unknown' }, { sessionId: 'pc-replacement' }]) {
    assert.equal(phoneControlReady({ ...observation, ...change }, now), false);
  }
});

test('registration requires an unused ticket and bounded geometry', () => {
  assert.ok(phoneRegistrationRegion(observation, now));
  for (const change of [{ view: 'activated_detail' }, { leftBasisPoints: 9000 }, { bottomBasisPoints: 10001 }]) {
    assert.equal(phoneRegistrationRegion({ ...observation, ...change }, now), null);
  }
});

test('a renewed observation preserves a swipe only for the identical session, context, and geometry', () => {
  const snapshot = phoneRegistrationSnapshot(observation, 1, now);
  const renewed = { ...observation, observationSequence: '2',
    observedAt: new Date(now + 1000).toISOString(), expiresAt: new Date(now + 4000).toISOString() };
  assert.equal(phoneRegistrationMatches(snapshot, renewed, 1, now + 1000), true);
  assert.equal(phoneRegistrationMatches(snapshot, renewed, 2, now + 1000), false);
  for (const change of [{ contextRevision: 'pc-session:2' }, { sessionGeneration: '2' }, { topBasisPoints: 7001 }, { busy: true }]) {
    assert.equal(phoneRegistrationMatches(snapshot, { ...renewed, ...change }, 1, now + 1000), false);
  }
  assert.equal(phoneRegistrationMatches(snapshot, observation, 1, now + 3000), false);
});
