import assert from 'node:assert/strict';
import test from 'node:test';

import {
  ExperimentalHDRImageSwitcher,
  advanceExperimentalHDRReplacementFailure
} from './experimental-hdr-image-switcher.mjs';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

function fakeImage(name) {
  return {
    name,
    hidden: true,
    src: '',
    style: {},
    attributes: new Map(),
    setAttribute(key, value) {
      this.attributes.set(key, value);
    },
    removeAttribute(key) {
      this.attributes.delete(key);
      if (key === 'src') this.src = '';
    }
  };
}

function harness(options = {}) {
  const images = [fakeImage('front'), fakeImage('back')];
  const live = new Set();
  const revoked = [];
  let nextURL = 0;
  let peakLive = 0;
  const switcher = new ExperimentalHDRImageSwitcher(images, {
    createObjectURL() {
      const url = `blob:hdr-${++nextURL}`;
      live.add(url);
      peakLive = Math.max(peakLive, live.size);
      return url;
    },
    revokeObjectURL(url) {
      assert.equal(live.delete(url), true, `${url} must be revoked exactly once`);
      revoked.push(url);
    },
    waitForReady: options.waitForReady || (() => Promise.resolve()),
    afterSwap: options.afterSwap || (() => {})
  });
  return { images, live, revoked, switcher, peakLive: () => peakLive };
}

test('rapid consecutive HDR results allow only the latest candidate to commit', async () => {
  const waits = [];
  const swaps = [];
  const state = harness({
    waitForReady(_image, url) {
      const ready = deferred();
      waits.push({ url, ready });
      return ready.promise;
    },
    afterSwap(event) {
      swaps.push(event.url);
    }
  });

  const first = state.switcher.present({ frame: 1 });
  waits[0].ready.resolve();
  assert.deepEqual(await first, { status: 'committed' });

  const second = state.switcher.present({ frame: 2 });
  const third = state.switcher.present({ frame: 3 });
  assert.deepEqual([...state.live], ['blob:hdr-1', 'blob:hdr-3']);
  assert.deepEqual(state.revoked, ['blob:hdr-2']);

  waits[1].ready.resolve();
  assert.deepEqual(await second, { status: 'stale' });
  assert.deepEqual(swaps, ['blob:hdr-1']);

  waits[2].ready.resolve();
  assert.deepEqual(await third, { status: 'committed' });
  assert.deepEqual(swaps, ['blob:hdr-1', 'blob:hdr-3']);
  assert.deepEqual([...state.live], ['blob:hdr-3']);
});

test('a delayed replacement keeps the proven HDR surface visible until an atomic swap', async () => {
  const waits = [];
  let state;
  const swapSnapshots = [];
  state = harness({
    waitForReady(_image, url) {
      const ready = deferred();
      waits.push({ url, ready });
      return ready.promise;
    },
    afterSwap(event) {
      swapSnapshots.push({
        nextVisible: !event.image.hidden,
        previousHidden: !event.previousImage || event.previousImage.hidden,
        previousStillLive: !event.previousURL || state.live.has(event.previousURL)
      });
    }
  });

  const first = state.switcher.present({ frame: 1 });
  waits[0].ready.resolve();
  await first;
  const active = state.images.find((image) => !image.hidden);

  const replacement = state.switcher.present({ frame: 2 });
  assert.equal(active.hidden, false);
  const staged = state.images.find((image) => image !== active);
  assert.equal(staged.hidden, false);
  assert.equal(staged.attributes.get('aria-hidden'), 'true');
  assert.equal(staged.style.zIndex, '2');
  assert.equal(active.style.zIndex, '3');
  assert.equal(state.live.has('blob:hdr-1'), true);

  waits[1].ready.resolve();
  assert.deepEqual(await replacement, { status: 'committed' });
  assert.deepEqual(swapSnapshots[1], {
    nextVisible: true,
    previousHidden: true,
    previousStillLive: true
  });
  assert.equal(active.hidden, true);
  assert.equal(state.images.filter((image) => !image.hidden).length, 1);
  assert.deepEqual(state.revoked, ['blob:hdr-1']);
});

test('default readiness waits for decode and two animation frames before revealing the first image', async () => {
  const imageA = fakeImage('front');
  const imageB = fakeImage('back');
  const decodeReady = deferred();
  imageA.decode = () => decodeReady.promise;
  imageB.decode = () => decodeReady.promise;
  const animationFrames = [];
  const switcher = new ExperimentalHDRImageSwitcher([imageA, imageB], {
    createObjectURL: () => 'blob:first',
    revokeObjectURL: () => {},
    requestAnimationFrame(callback) {
      animationFrames.push(callback);
    }
  });

  const pending = switcher.present({ frame: 1 });
  assert.equal(imageA.hidden, true);
  decodeReady.resolve();
  await Promise.resolve();
  assert.equal(animationFrames.length, 1);
  animationFrames.shift()();
  await Promise.resolve();
  assert.equal(animationFrames.length, 1);
  assert.equal(imageA.hidden, true);
  animationFrames.shift()();
  assert.deepEqual(await pending, { status: 'committed' });
  assert.equal(imageA.hidden, false);
});

test('replacement failure preserves the prior good HDR image and cleans only the candidate', async () => {
  const failure = new Error('replacement decode failed');
  let call = 0;
  const state = harness({
    waitForReady() {
      if (++call === 1) return Promise.resolve();
      return Promise.reject(failure);
    }
  });

  await state.switcher.present({ frame: 1 });
  const active = state.images.find((image) => !image.hidden);
  const result = await state.switcher.present({ frame: 2 });

  assert.equal(result.status, 'failed');
  assert.equal(result.error, failure);
  assert.equal(active.hidden, false);
  assert.equal(active.src, 'blob:hdr-1');
  assert.deepEqual([...state.live], ['blob:hdr-1']);
  assert.deepEqual(state.revoked, ['blob:hdr-2']);
});

test('replacement failures retain a good frame twice, fall back on the third, and reset after success', () => {
  let failures = 0;
  for (let attempt = 1; attempt <= 2; attempt += 1) {
    const decision = advanceExperimentalHDRReplacementFailure(failures, 'failed', true, 3);
    failures = decision.failures;
    assert.equal(decision.fallback, false);
  }
  let decision = advanceExperimentalHDRReplacementFailure(failures, 'committed', true, 3);
  assert.deepEqual(decision, { failures: 0, fallback: false });
  failures = decision.failures;
  decision = advanceExperimentalHDRReplacementFailure(failures, 'failed', true, 3);
  assert.deepEqual(decision, { failures: 1, fallback: false });
  decision = advanceExperimentalHDRReplacementFailure(2, 'failed', true, 3);
  assert.deepEqual(decision, { failures: 3, fallback: true });
  assert.equal(advanceExperimentalHDRReplacementFailure(0, 'failed', false, 3).fallback, true);
});

test('an initial decode failure throws because no proven HDR image can be retained', async () => {
  const failure = new Error('initial decode failed');
  const state = harness({ waitForReady: () => Promise.reject(failure) });

  await assert.rejects(state.switcher.present({ frame: 1 }), failure);
  assert.equal(state.switcher.hasActive(), false);
  assert.equal(state.live.size, 0);
});

test('clear invalidates an in-flight generation and immediately restores fallback state', async () => {
  const ready = deferred();
  let current = true;
  const state = harness({ waitForReady: () => ready.promise });
  const pending = state.switcher.present({ frame: 1 }, { isCurrent: () => current });

  current = false;
  state.switcher.clear();
  assert.equal(state.switcher.hasActive(), false);
  assert.equal(state.live.size, 0);
  assert.equal(state.images.every((image) => image.hidden && image.src === ''), true);

  ready.resolve();
  assert.deepEqual(await pending, { status: 'stale' });
  assert.equal(state.images.every((image) => image.hidden), true);
});

test('socket-close and stale-input clear semantics discard a proven frame', async () => {
  for (const condition of ['socket close', 'stale input']) {
    const state = harness();
    state.switcher.setDimensions(1080.9, 2400.4);
    await state.switcher.present({ condition });
    assert.equal(state.switcher.hasActive(), true, condition);
    assert.deepEqual(state.images.map(({ width, height }) => ({ width, height })), [
      { width: 1080, height: 2400 },
      { width: 1080, height: 2400 }
    ]);

    state.switcher.clear();
    assert.equal(state.switcher.hasActive(), false, condition);
    assert.equal(state.images.every((image) => image.hidden && image.src === ''), true, condition);
    assert.equal(state.live.size, 0, condition);
  }
});

test('object URL ownership stays bounded to two and every URL is eventually revoked', async () => {
  const state = harness();

  for (let frame = 1; frame <= 20; frame += 1) {
    assert.deepEqual(await state.switcher.present({ frame }), { status: 'committed' });
    assert.equal(state.live.size, 1);
  }
  assert.ok(state.peakLive() <= 2, `peak live URL count was ${state.peakLive()}`);

  state.switcher.clear();
  assert.equal(state.live.size, 0);
  assert.equal(state.revoked.length, 20);
  assert.equal(new Set(state.revoked).size, 20);
});
