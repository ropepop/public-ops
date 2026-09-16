import test from 'node:test';
import assert from 'node:assert/strict';
import { ConnectionRecovery, FrameSilence } from './connection-recovery.mjs';
import { MediaSession } from './media-session.mjs';

test('new receipts alone reset the visible silence deadline, independently of connection health', () => {
  const clock = new FrameSilence(0);
  const { loop, step } = fixture();
  clock.step(0, true);
  step(0);
  for (let now = 1000; now <= 29000; now += 1000) {
    clock.step(now);
    step(now);
    loop.wake(now);
  }
  assert.equal(clock.receive({ epoch: 7, sequence: 1 }, 29000, 29000), true);
  clock.step(30000);
  assert.equal(clock.elapsed, 1000);
  assert.equal(clock.showError, false);
  step(30000, true); // A healthy connection cannot move the separate receipt deadline.
  clock.step(58999); assert.equal(clock.showError, false);
  clock.step(59000); assert.equal(clock.showError, true);
  clock.receive({ epoch: 7, sequence: 2 }, 59000, 59000);
  assert.equal(clock.showError, false);
  assert.equal(clock.elapsed, 0);
});

test('silence pauses while hidden or intentionally paused and buffered frames retain their arrival time', () => {
  const clock = new FrameSilence(0);
  clock.step(0, true);
  clock.step(10000, false);
  clock.step(70000, true);
  assert.equal(clock.elapsed, 10000);
  clock.step(89999); assert.equal(clock.showError, false);
  clock.step(90000); assert.equal(clock.showError, true);
  clock.step(91000, false); assert.equal(clock.showError, false);
  assert.equal(clock.receive({ epoch: 7, sequence: 1 }, 95000, 95000), false);
  clock.step(100000, true); assert.equal(clock.showError, true);
  clock.receive({ epoch: 7, sequence: 1 }, 100000, 100000);
  assert.equal(clock.elapsed, 0);
  const buffered = new FrameSilence(0);
  buffered.step(0, true);
  buffered.receive({ epoch: 7, sequence: 1 }, 1000, 10000);
  assert.equal(buffered.elapsed, 9000);
  buffered.step(30999); assert.equal(buffered.showError, false);
  buffered.step(31000); assert.equal(buffered.showError, true);
});

test('validated frame receipt precedes decoding; transport resets, invalid frames and replay do not renew silence', () => {
  const clock = new FrameSilence(0);
  clock.step(0, true);
  let now = 1000, decoded = 0;
  const media = new MediaSession({}, { onReceived: (picture, at) => clock.receive(picture, at, now) });
  media.feedback = () => {};
  media.decodeNewest = () => { decoded++; assert.equal(clock.receivedAt, now); };
  media.config = { feedbackConfigGeneration: 1 };
  media.epoch = 7;
  function raw(epoch, sequence) {
    const bytes = new ArrayBuffer(94), view = new DataView(bytes);
    view.setUint32(0, 0x54534633); view.setUint8(4, 1);
    [epoch, sequence, 1, 1, 1000, 1001, 1002, 1003, 1004, 1, 1]
      .forEach((value, index) => view.setBigUint64(5 + index * 8, BigInt(value)));
    return bytes;
  }
  media.receive(raw(7, 10), now);
  assert.equal(decoded, 1);
  now = 5000;
  media.receive(raw(8, 11), now);
  media.receive(new ArrayBuffer(93), now);
  media.receive(raw(7, 9), now);
  assert.equal(decoded, 1);
  media.close();
  media.config = { feedbackConfigGeneration: 2 };
  media.epoch = 7;
  media.decodeNewest = () => { decoded++; };
  media.receive(raw(7, 10), now);
  assert.equal(clock.receivedAt, 1000);
  assert.equal(clock.receive({ epoch: 7, sequence: 11 }, 6000, now), false);
  clock.step(31000); assert.equal(clock.showError, true);
  now = 31000;
  // A phone reboot can start a new epoch with a lower monotonic origin.
  media.epoch = 2;
  media.received = 0;
  media.receive(raw(2, 1), now);
  assert.equal(clock.showError, false);
});

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
