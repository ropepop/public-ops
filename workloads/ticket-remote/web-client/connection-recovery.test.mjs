import test from 'node:test';
import assert from 'node:assert/strict';
import { ConnectionRecovery } from './connection-recovery.mjs';

function fixture() {
  const starts = [], stops = [];
  const loop = new ConnectionRecovery({ now: 0, start: id => starts.push(id), stop: () => stops.push(true) });
  const step = (at, healthy = false, extra = {}) => loop.step(at, { visible: true, healthy, ...extra });
  return { loop, step, starts, stops };
}

test('unlimited rapid failures show an error only at 30 seconds, then recover automatically', () => {
  const { loop, step, starts } = fixture();
  for (let at = 0; at < 30000; at += 1000) {
    step(at);
    loop.fail(loop.generation, at);
    assert.equal(loop.showError, false);
  }
  step(29999); assert.equal(loop.showError, false);
  step(30000); assert.equal(loop.showError, true);
  loop.fail(loop.generation, 30000);
  step(31000); assert.equal(starts.length, 32);
  step(31250, true);
  assert.equal(loop.showError, false);
  assert.equal(loop.downtime, 0);
  assert.equal(loop.phase, 'live');
});

test('hanging attempts expire, duplicate and old failures cannot cancel their replacement', () => {
  const { loop, step, starts, stops } = fixture();
  step(0); step(9999); assert.equal(starts.length, 1);
  step(10000); assert.equal(stops.length, 1);
  assert.equal(loop.fail(1, 10001), false);
  step(10999); assert.equal(starts.length, 1);
  step(11000); assert.equal(starts.length, 2);
  assert.equal(loop.fail(1, 11001), false);
  assert.equal(loop.accepts(2), true);
  loop.wake(11002); step(11002); assert.equal(starts.length, 2);
});

test('background time is excluded and returning starts immediately even after the error', () => {
  const { loop, step } = fixture();
  step(0); step(10000); loop.suspend(12000);
  assert.equal(loop.downtime, 12000);
  step(72000); assert.equal(loop.downtime, 12000);
  step(82000); step(89999); assert.equal(loop.showError, false);
  step(90000); assert.equal(loop.showError, true);
  loop.suspend(91000); step(200000);
  assert.equal(loop.phase, 'connecting');
  assert.equal(loop.showError, true);
});

test('a lost live connection starts replacement immediately and a new outage gets a fresh grace period', () => {
  const { loop, step, starts } = fixture();
  step(0); step(1000, true); step(2000, false);
  assert.equal(starts.length, 2);
  assert.equal(loop.downtime, 0);
  step(3000, true); step(4000, false); step(5000, false);
  assert.equal(loop.downtime, 1000);
});

test('owner pause suppresses the error clock without preventing database recovery', () => {
  const { loop, step, starts } = fixture();
  for (let at = 0; at <= 60000; at += 1000) step(at, false, { paused: true });
  assert.ok(starts.length > 3);
  assert.equal(loop.showError, false);
  assert.equal(loop.downtime, 0);
});

test('visible healthy viewing has no inactivity cutoff; synchronous startup failures remain bounded', () => {
  const { loop, step, starts } = fixture();
  step(0); step(1000, true); step(3600000, true);
  assert.equal(starts.length, 1);
  assert.equal(loop.phase, 'live');
  let attempts = 0;
  const broken = new ConnectionRecovery({ now: 0, start: () => { attempts++; throw Error('unavailable'); }, stop() {} });
  broken.step(0, { visible: true, healthy: false });
  broken.step(999, { visible: true, healthy: false });
  assert.equal(attempts, 1);
  broken.step(1000, { visible: true, healthy: false });
  assert.equal(attempts, 2);
});
