import test from 'node:test';
import assert from 'node:assert/strict';
import { Presentation } from './presentation.mjs';
import { ClientHDRRenderer } from './client-hdr-renderer.mjs';

const drain = async () => { for (let i = 0; i < 24; i++) await new Promise(setImmediate); };
const deferred = () => { let resolve; const promise = new Promise(done => resolve = done); return { promise, resolve }; };
function fixture(t) {
  const frames = new Set(), canvases = new Set(), renderers = [];
  const styles = new Map(), samples = [];
  const theme = { content: '#020304', getAttribute() { return this.content; }, setAttribute(_name, value) { this.content = value; } };
  let sourceRGB = [48, 59, 61], readbackFailure = false;
  let nextFrame = 0;
  class Frame {
    constructor(source) {
      this.rawId = source?.rawId || ++nextFrame;
      this.decorated = source instanceof Canvas || Boolean(source?.decorated);
      this.rgb = source?.rgb || sourceRGB;
      this.displayWidth = 10; this.displayHeight = 20;
      frames.add(this);
    }
    clone() { assert.ok(frames.has(this)); return new Frame(this); }
    close() { assert.ok(frames.delete(this), 'frame closed exactly once'); }
  }
  class Canvas {
    constructor() { this.width = 10; this.height = 20; this.dataset = {}; this.hidden = true; canvases.add(this); }
    getContext() {
      this.context ||= { canvas: this, drawImage: (source, ...args) => { this.lastSource = source; this.drawArgs = args; this.rgb = source.rgb; }, clearRect() {},
        getImageData: () => {
          samples.push({ canvas: this, source: this.lastSource, args: this.drawArgs });
          if (readbackFailure) throw Error('fixture readback unavailable');
          return { data: new Uint8ClampedArray([...(this.rgb || sourceRGB), 255]) };
        },
        save() {}, restore() {}, translate() {}, beginPath() {}, roundRect() {}, clip() {},
        fillRect() {}, fillText() {}, arc() {}, fill() {}, scale() {}, stroke() {},
        createLinearGradient: () => ({ addColorStop() {} }) };
      return this.context;
    }
    setAttribute() {}
    cloneNode() { return new Canvas(); }
    replaceWith() { canvases.delete(this); }
    before() {}
    remove() { canvases.delete(this); }
    toDataURL() { return 'data:image/png;base64,fixture'; }
  }
  const values = { VideoFrame: Frame, HTMLCanvasElement: Canvas, navigator: { gpu: {} },
    CSS: { supports: () => true }, matchMedia: () => ({ matches: true }),
    document: { body: { dataset: {}, classList: { add() {}, remove() {} } }, createElement: () => new Canvas(),
      documentElement: { style: { setProperty: (key, value) => styles.set(key, value),
        getPropertyValue: key => styles.get(key) || '', removeProperty: key => styles.delete(key) } },
      querySelector: selector => selector.includes('theme-color') ? theme : null },
    window: { scrollTo() {} }, Path2D: class {}, requestAnimationFrame: callback => setImmediate(callback),
    cancelAnimationFrame: clearImmediate };
  for (const [key, value] of Object.entries(values)) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, key);
    Object.defineProperty(globalThis, key, { configurable: true, value });
    t.after(() => descriptor ? Object.defineProperty(globalThis, key, descriptor) : delete globalThis[key]);
  }
  t.mock.method(ClientHDRRenderer.prototype, 'initialize', async function() { renderers.push(this); });
  t.mock.method(ClientHDRRenderer.prototype, 'setBoost', function(boost) { this.boost = boost; });
  t.mock.method(ClientHDRRenderer.prototype, 'render', async function() {});
  t.mock.method(ClientHDRRenderer.prototype, 'present', async function() {});
  t.mock.method(ClientHDRRenderer.prototype, 'waitForCompositorSettlement', async function() {});
  const callbacks = [], failures = [];
  const elements = { canvas: new Canvas(), hdrCanvas: new Canvas(),
    resultArea: { dataset: {}, getBoundingClientRect: () => ({ width: 10 }) },
    resultImage: { hidden: true, decode: async () => {}, removeAttribute() {} } };
  let now = 0;
  const presentation = new Presentation(elements, { age: metadata => metadata ? now - metadata.at : Infinity,
    onRendered: (metadata, shown) => callbacks.push({ metadata, shown }), onFailure: reason => failures.push(reason) });
  const receive = sequence => {
    const frame = new Frame();
    presentation.receive(frame, { epoch: 1, sequence, configGeneration: 1, at: now, visualAgeMillis: 0 });
    frame.close();
  };
  t.after(() => { presentation.dispose(); assert.equal(frames.size, 0, 'all retained frames released'); });
  return { presentation, elements, receive, renderers, callbacks, failures, canvases, frames, styles, theme, samples,
    setSourceRGB: rgb => { sourceRGB = rgb; }, failReadback: fail => { readbackFailure = fail; }, advance: value => now += value };
}

test('edge background follows the displayed SDR, HDR and frozen picture without interfering with rendering', async t => {
  const f = fixture(t), p = f.presentation;
  const color = () => f.styles.get('--ticket-picture-background');
  const assertColor = expected => {
    assert.deepEqual(color()?.match(/[\d.]+/g).map(Number), expected);
    assert.equal(f.theme.content, color(), 'browser theme follows the displayed border color');
  };
  f.receive(1); await drain();
  assertColor([48, 59, 61]);
  assert.equal(f.samples.length, 1);
  assert.equal(f.samples[0].source, f.elements.canvas, 'sampling uses the already drawn canvas');
  assert.equal(f.samples[0].canvas.width, 1);
  assert.equal(f.samples[0].canvas.height, 1);
  const reads = f.samples.length;
  p.draw(p.latest.frame, p.latest.metadata, { visualOnly: true });
  assert.equal(f.samples.length, reads, 'local animation does not cause readbacks');
  p.setPreference(true, 4); await drain();
  assertColor([96, 116, 120]);
  p.setPreference(true, 6); await drain();
  assertColor([117, 140, 144]);
  const request = { requestId: 'background', status: 'succeeded', captureRequired: true,
    resultFrameEpoch: '1', resultMinFrameSequence: '1', resultMarkerRevision: '1:1' };
  assert.equal(await p.presentResult(request, () => true), true);
  f.setSourceRGB([20, 30, 40]); f.receive(2); await drain();
  assertColor([117, 140, 144]);
  assert.equal(p.frozen.metadata.sequence, 1, 'new live pictures cannot recolor a frozen result');
  p.controller.fail('fixture_frozen_fallback'); await drain();
  assertColor([48, 59, 61]);
  p.closeResult(); await drain();
  assertColor([20, 30, 40]);
  p.setPreference(false, 4); p.setPreference(true, 4); await drain();
  const heldColor = color(), wait = deferred();
  const slowRender = t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  p.setVisible(false); p.setVisible(true); await drain();
  assert.ok(p.holdover);
  f.setSourceRGB([80, 82, 84]); f.receive(3);
  p.draw(p.latest.frame, p.latest.metadata);
  assert.equal(color(), heldColor, 'a retained HDR picture keeps its matching border during replacement');
  p.controller.fail('fixture_holdover_fallback'); wait.resolve(); slowRender.mock.restore(); await drain();
  assertColor([80, 82, 84]);
  f.failReadback(true); f.setSourceRGB([90, 90, 90]);
  assert.doesNotThrow(() => f.receive(4)); await drain();
  assertColor([80, 82, 84]);
  assert.equal(p.rendered.sequence, 4, 'failed edge readback cannot block the next ticket picture');
  assert.deepEqual(f.failures, ['fixture_frozen_fallback', 'fixture_holdover_fallback']);
  const sample = f.samples[0].canvas;
  p.clearForColdRestart();
  assert.equal(color(), undefined, 'cold clear removes stale sampled color');
  assert.equal(f.theme.content, '#020304');
  assert.equal(f.canvases.has(sample), false, 'sampling canvas is released with its owner');
});

test('return retains HDR until boosted replacement settles; repeated return creates one attempt', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  assert.ok(p.controller.snapshot().proofFresh);
  const original = p.elements.hdrCanvas, oldRenderer = p.controller.renderer;
  p.setVisible(false);
  const settlement = deferred();
  t.mock.method(ClientHDRRenderer.prototype, 'waitForCompositorSettlement', () => settlement.promise);
  p.setVisible(true); p.setVisible(true); p.recoverHDR();
  await drain();
  assert.equal(f.renderers.length, 2);
  assert.equal(p.holdover.canvas, original);
  assert.equal(original.dataset.clientHdrSurface, 'visible');
  assert.equal(oldRenderer.disposed, false);
  assert.equal(p.visiblePicture(), null);
  settlement.resolve(); await drain();
  assert.equal(p.holdover, null);
  assert.equal(oldRenderer.disposed, true);
  assert.equal(p.visiblePicture().sequence, 1);
});

test('HDR failure stays in SDR outside the bounded follow-up until return or explicit retry', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const original = p.elements.hdrCanvas;
  p.controller.fail('render_failed'); f.advance(3001); f.receive(2); await drain();
  assert.equal(original.dataset.clientHdrSurface, 'standby');
  assert.equal(p.visiblePicture().sequence, 2);
  assert.equal(p.rendered.sequence, 2);
  assert.equal(p.enabled, true);
  assert.equal(p.recovering, false);
  assert.equal(p.failure, 'render_failed');
  assert.equal(document.body.dataset.experimentalMedia, 'fallback-sdr');
  p.recoverHDR(); await drain();
  assert.equal(f.renderers.length, 1, 'ordinary recovery does not retry a failed HDR device');
  p.setVisible(false); p.setVisible(true); await drain();
  assert.equal(f.renderers.length, 2, 'foreground return retries a failed HDR device');
  assert.equal(p.failure, '');
  p.controller.fail('render_failed');
  p.setPreference(false, 4); p.setPreference(true, 4); await drain();
  assert.equal(p.visiblePicture().sequence, 2);
  assert.equal(p.failure, '');
  assert.equal(p.holdover, null);
});

test('one-second follow-up reasserts the same boosted surface once without identity activation', async t => {
  const f = fixture(t), p = f.presentation;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const renders = [], copies = [];
  t.mock.method(ClientHDRRenderer.prototype, 'render', async function(frame, options) { renders.push(options); });
  t.mock.method(ClientHDRRenderer.prototype, 'present', async function(options) { copies.push(options); });
  p.setPreference(true, 5); f.receive(1); await drain();
  const canvas = p.elements.hdrCanvas, renderer = p.controller.renderer;
  renders.length = copies.length = 0;
  t.mock.timers.tick(999); await drain();
  assert.equal(copies.length, 0);
  t.mock.timers.tick(1); await drain();
  assert.deepEqual(copies, [{ reconfigure: true }]);
  assert.deepEqual(renders, [{ activationFrame: false, requestPatch: false }]);
  assert.equal(p.elements.hdrCanvas, canvas);
  assert.equal(p.controller.renderer, renderer);
  assert.equal(p.controller.boost, 5);
  assert.equal(p.holdover, null);
  assert.equal(p.recovering, false);
  t.mock.timers.tick(20000); f.receive(2); await drain();
  assert.equal(copies.filter(copy => copy.reconfigure).length, 1);
});

test('follow-up waits for a slow first presentation and never overlaps GPU work', async t => {
  const f = fixture(t), p = f.presentation;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const wait = deferred(), copies = [];
  const render = t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  t.mock.method(ClientHDRRenderer.prototype, 'present', async options => copies.push(options));
  p.setPreference(true, 4); f.receive(1); await drain();
  t.mock.timers.tick(1000); await drain();
  assert.equal(f.renderers.length, 1);
  assert.equal(p.hdrFollowupDue, true);
  assert.equal(copies.length, 0);
  render.mock.restore(); wait.resolve(); await drain();
  assert.deepEqual(copies.map(copy => copy.reconfigure), [false, false, true]);
  assert.equal(p.recovering, false);
  assert.equal(p.hdrFollowupDue, false);
});

test('cold initialization failure gets one timed retry and no continuing retry loop', async t => {
  const f = fixture(t), p = f.presentation;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let attempts = 0;
  t.mock.method(ClientHDRRenderer.prototype, 'initialize', async () => { attempts++; throw Error('sleeping_gpu'); });
  p.setPreference(true, 4); f.receive(1); await drain();
  assert.equal(attempts, 1);
  t.mock.timers.tick(999); await drain(); assert.equal(attempts, 1);
  t.mock.timers.tick(1); await drain(); assert.equal(attempts, 2);
  t.mock.timers.tick(20000); f.receive(2); await drain(); assert.equal(attempts, 2);
  assert.equal(p.hdrBlocked, true);
  assert.equal(p.visiblePicture().sequence, 2);
});

test('failure after the one-second deadline spends the follow-up once and restores HDR', async t => {
  const f = fixture(t), p = f.presentation;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const wait = deferred();
  const render = t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  p.setPreference(true, 4); f.receive(1); await drain();
  t.mock.timers.tick(1000); await drain();
  assert.equal(p.hdrFollowupDue, true);
  render.mock.restore();
  p.controller.fail('late_wake_failure'); wait.resolve(); await drain();
  assert.equal(f.renderers.length, 2);
  assert.equal(p.hdrFollowupDue, false);
  assert.equal(p.hdrFollowupTimer, null);
  assert.equal(p.controller.snapshot().displayConfirmed, true);
  p.controller.fail('second_failure');
  t.mock.timers.tick(10000); f.receive(2); await drain();
  assert.equal(f.renderers.length, 2);
});

test('follow-up waits for a picture on fresh opening and cancellation stops late work', async t => {
  const f = fixture(t), p = f.presentation;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const copies = [];
  t.mock.method(ClientHDRRenderer.prototype, 'present', async options => copies.push(options));
  p.setPreference(true, 4); await drain();
  t.mock.timers.tick(1000); await drain();
  assert.equal(p.hdrFollowupDue, true);
  assert.equal(copies.length, 0);
  f.receive(1); await drain();
  assert.deepEqual(copies.map(copy => copy.reconfigure), [false, false, true]);
  for (const cancel of [() => p.cancelHDRRecovery(), () => p.setVisible(false), () => p.setPreference(false, 4)]) {
    p.setVisible(true); p.setPreference(true, 4);
    p.setVisible(true, { foregroundReturn: true }); await drain();
    const before = copies.length;
    cancel(); t.mock.timers.tick(1000); await drain();
    assert.equal(copies.length, before);
    assert.equal(p.hdrFollowupDue, false);
  }
});

test('one-second reassertion keeps a frozen result exact without fresh stream authority', async t => {
  const f = fixture(t), p = f.presentation;
  t.mock.timers.enable({ apis: ['setTimeout'] });
  const copies = [];
  t.mock.method(ClientHDRRenderer.prototype, 'present', async options => copies.push(options));
  p.setPreference(true, 4); f.receive(1); await drain();
  p.frozen = { metadata: { ...p.latest.metadata }, frame: p.latest.frame.clone(), displayed: true };
  f.advance(10000); f.receive(2);
  t.mock.timers.tick(1000); await drain();
  assert.equal(copies.at(-1).reconfigure, true);
  assert.equal(p.controller.snapshot().sequence, 1);
  assert.equal(p.controller.snapshot().proofFresh, false);
  assert.equal(p.elements.resultArea.dataset.presentation, 'exact-hdr');
});

test('focus-only return replaces HDR once requested, while HDR off stays off', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 5); f.receive(1); await drain();
  const original = p.elements.hdrCanvas;
  p.setVisible(true, { foregroundReturn: true }); await drain();
  assert.equal(f.renderers.length, 2);
  assert.notEqual(p.elements.hdrCanvas, original);
  assert.equal(p.controller.boost, 5);
  p.setVisible(true); await drain();
  assert.equal(f.renderers.length, 2);
  p.setPreference(false, 5);
  p.setVisible(true, { foregroundReturn: true }); await drain();
  assert.equal(f.renderers.length, 2);
  assert.equal(p.controller, null);
});

test('another departure fences unfinished recovery and waits quietly for a fresh picture', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const original = p.elements.hdrCanvas, settlement = deferred();
  const mock = t.mock.method(ClientHDRRenderer.prototype, 'waitForCompositorSettlement', () => settlement.promise);
  p.setVisible(false); p.setVisible(true); await drain();
  const abandoned = p.controller;
  p.setVisible(false); f.advance(3001);
  mock.mock.restore();
  p.setVisible(true); await drain();
  assert.equal(f.renderers.length, 3);
  assert.equal(p.holdover.canvas, original);
  assert.equal(p.displayedHDR, null);
  settlement.resolve(); await drain();
  assert.equal(p.holdover.canvas, original, 'late completion cannot release the held picture');
  assert.equal(abandoned.active, false);
  f.receive(2); await drain();
  assert.equal(p.holdover, null);
  assert.equal(p.visiblePicture().sequence, 2);
});

test('device loss and disabling release surfaces without restoring late work', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  p.controller.fail('device_lost');
  assert.equal(p.elements.hdrCanvas.dataset.clientHdrSurface, 'standby');
  const wait = deferred();
  t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  p.setPreference(false, 4); p.setPreference(true, 4); await drain();
  p.setPreference(false, 4);
  wait.resolve(); await drain();
  assert.equal(p.controller, null);
  assert.equal(p.holdover, null);
  assert.equal(p.elements.hdrCanvas.hidden, true);
  assert.equal(p.recovering, false);
});

test('displayed exact result is reprocessed after return without gaining fresh authority', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  // This is the accepted-result boundary; retain its original frame just as presentResult does.
  p.frozen = { metadata: { ...p.latest.metadata }, frame: p.latest.frame.clone(), displayed: true, presenting: false };
  f.advance(6000); f.receive(2);
  p.setVisible(false); p.setVisible(true); await drain();
  assert.equal(p.controller.snapshot().sequence, 1);
  assert.equal(p.controller.snapshot().proofFresh, false);
  assert.equal(p.controller.snapshot().displayConfirmed, true);
  assert.equal(p.elements.resultArea.dataset.presentation, 'exact-hdr');
  assert.equal(p.elements.resultImage.hidden, true);
  p.closeResult(); await drain();
  assert.equal(p.controller.snapshot().sequence, 2);
  assert.equal(p.controller.snapshot().proofFresh, true);
});

test('result dismissal rejects delayed retained-result output', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  p.frozen = { metadata: { ...p.latest.metadata }, frame: p.latest.frame.clone(), displayed: true, presenting: false };
  const wait = deferred();
  t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  p.setVisible(false); p.setVisible(true); await drain();
  f.receive(2); p.closeResult(); wait.resolve(); await drain();
  assert.equal(p.controller.snapshot().sequence, 2);
  assert.equal(p.elements.resultArea.hidden, true);
});

test('presentResult retains and releases exactly its accepted frame', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const request = { requestId: 'test', status: 'succeeded', captureRequired: true,
    resultFrameEpoch: '1', resultMinFrameSequence: '1', resultMarkerRevision: '1:1' };
  assert.equal(await p.presentResult(request, () => true), true);
  assert.equal(p.frozen.displayed, true);
  assert.ok(f.frames.has(p.frozen.frame));
  const captured = p.frozen.frame;
  p.closeResult(); await drain();
  assert.equal(f.frames.has(captured), false);
});

test('failed background replacement releases both HDR owners and shows SDR', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const original = p.elements.hdrCanvas, renderer = p.controller.renderer;
  const wait = deferred();
  t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  p.setVisible(false); p.setVisible(true); await drain();
  assert.equal(p.holdover.canvas, original);
  assert.equal(f.canvases.size, 4, 'SDR, sampling, retained HDR and replacement HDR canvases');
  const replacement = p.controller.renderer;
  p.controller.fail('test_failure'); wait.resolve(); await drain();
  assert.equal(p.holdover, null);
  assert.equal(p.controller, null);
  assert.equal(renderer.disposed, true);
  assert.equal(replacement.disposed, true);
  assert.equal(p.visiblePicture().sequence, 1);
  assert.equal(p.recovering, false);
  assert.equal(f.canvases.size, 3, 'only SDR, sampling and the reusable HDR element remain');
});

test('unsupported HDR uses ordinary pictures without a retry spinner', async t => {
  const f = fixture(t), p = f.presentation;
  navigator.gpu = null;
  p.setPreference(true, 4); f.receive(1); await drain();
  assert.equal(p.visiblePicture().sequence, 1);
  assert.equal(p.recovering, false);
  assert.equal(p.failure, 'hdr_unsupported');
  assert.deepEqual(f.failures, ['hdr_unsupported']);
  p.recoverHDR(); p.size(20, 40); f.receive(2); await drain();
  assert.equal(f.renderers.length, 0);
  assert.equal(p.visiblePicture().sequence, 2);
});

test('failure shows the exact SDR code and never substitutes a newer live picture', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const request = { requestId: 'test', status: 'succeeded', captureRequired: true,
    resultFrameEpoch: '1', resultMinFrameSequence: '1', resultMarkerRevision: '1:1' };
  assert.equal(await p.presentResult(request, () => true), true);
  f.receive(2);
  p.controller.fail('device_lost'); await drain();
  assert.equal(p.elements.resultArea.dataset.presentation, 'sdr');
  assert.equal(p.elements.resultImage.hidden, false);
  assert.equal(p.frozen.metadata.sequence, 1);
  assert.equal(p.recovering, false);
});

test('failure during final code paint reveals its prepared SDR image before acknowledgement', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  let failed = false;
  t.mock.method(globalThis, 'requestAnimationFrame', callback => {
    if (!failed && p.frozen?.presenting && p.elements.resultArea.dataset.presentation === 'exact-hdr') {
      failed = true;
      p.controller.fail('device_lost');
    }
    return setImmediate(callback);
  });
  const request = { requestId: 'test', status: 'succeeded', captureRequired: true,
    resultFrameEpoch: '1', resultMinFrameSequence: '1', resultMarkerRevision: '1:1' };
  assert.equal(await p.presentResult(request, () => true), true);
  assert.equal(failed, true);
  p.size(20, 40);
  assert.equal(p.elements.resultArea.dataset.presentation, 'sdr');
  assert.equal(p.elements.resultImage.hidden, false);
  assert.equal(p.frozen.metadata.sequence, 1);
});

test('changing boost reprocesses the same retained result without restarting', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  p.frozen = { metadata: { ...p.latest.metadata }, frame: p.latest.frame.clone(), displayed: true, presenting: false };
  const renderer = p.controller.renderer;
  p.setPreference(true, 6); await drain();
  assert.equal(p.controller.renderer, renderer);
  assert.equal(p.controller.snapshot().displayConfirmed, true);
  assert.equal(renderer.boost, 6);
});

test('a source becoming invalid during GPU work cannot replace the held picture', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const wait = deferred();
  t.mock.method(ClientHDRRenderer.prototype, 'render', () => wait.promise);
  p.setVisible(false); p.setVisible(true); await drain();
  f.advance(3001); wait.resolve(); await drain();
  assert.equal(p.controller.snapshot().displayConfirmed, false);
  assert.ok(p.holdover);
  assert.equal(p.visiblePicture(), null);
  f.receive(2); await drain();
  assert.equal(p.visiblePicture().sequence, 2);
});

test('cold restart restores unchanged saved HDR after clearing every old surface', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 6); f.receive(1); await drain();
  p.clearForColdRestart();
  assert.equal(p.controller, null);
  assert.equal(p.enabled, true);
  p.recoverHDR(); f.receive(2); await drain();
  assert.equal(p.visiblePicture().sequence, 2);
  assert.equal(p.controller.renderer.boost, 6);
});

test('expiry without fresh video erases the old result rather than keeping its pixels live', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  p.frozen = { metadata: { ...p.latest.metadata }, frame: p.latest.frame.clone(), displayed: true, presenting: false };
  const oldRenderer = p.controller.renderer;
  f.advance(3001); p.closeResult(); await drain();
  assert.equal(p.displayedHDR, null);
  assert.equal(p.rendered, null);
  assert.equal(p.holdover, null);
  assert.equal(oldRenderer.disposed, true);
  assert.equal(p.recovering, true);
  f.receive(2); await drain();
  assert.equal(p.visiblePicture().sequence, 2);
});

const slider = (offset = 0, state = 'ready', reducedMotion = true) => ({
  region: { leftBasisPoints: 1000, topBasisPoints: 5000, rightBasisPoints: 9000, bottomBasisPoints: 6000 },
  offset, state, reducedMotion
});

test('local HDR animation retains source authority and skips repeated compositor settling', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const rendered = p.rendered, raw = p.latest.frame, canvas = p.elements.hdrCanvas;
  const callbackCount = f.callbacks.length;
  const renderer = p.controller.renderer;
  let settlements = 0, renders = 0;
  t.mock.method(renderer, 'waitForCompositorSettlement', async () => { settlements++; });
  t.mock.method(renderer, 'render', async frame => { assert.equal(frame.decorated, true); renders++; });
  p.setSlider(slider(0, 'ready', false)); await drain();
  assert.ok(renders > 2, 'the wave advances between source frames');
  assert.equal(settlements, 0, 'already activated local animation has no compositor delay');
  assert.equal(f.callbacks.length, callbackCount, 'local frames emit no transport or startup feedback');
  assert.equal(p.rendered, rendered, 'source timestamps and presentation ordinal are unchanged');
  assert.equal(p.latest.frame, raw);
  assert.equal(raw.decorated, false, 'retained source remains raw');
  assert.equal(p.elements.hdrCanvas, canvas, 'animation keeps the existing HDR canvas');
  assert.equal(p.controller.snapshot().sequence, 1);
  assert.equal(p.controller.snapshot().proofFresh, true);
  assert.equal(p.controller.ensureExactProof(1, 1), false, 'decorated pixels cannot prove an exact result');
  f.advance(3001); await drain();
  const stopped = renders;
  await drain(); assert.equal(renders, stopped, 'animation stops when the actual source expires');
  assert.equal(p.sliderAnimation, null);
});

test('SDR slider redraws reuse a canvas without VideoFrame copies or fresh feedback', async t => {
  const f = fixture(t), p = f.presentation;
  f.receive(1); await drain();
  const source = p.latest.frame, callbackCount = f.callbacks.length, rendered = p.rendered;
  p.setSlider(slider());
  const composition = p.composition;
  p.setSlider(slider(0.7, 'dragging'));
  assert.equal(p.composition, composition);
  assert.equal(p.elements.canvas.lastSource, composition);
  assert.equal(f.frames.size, 1, 'SDR owns only the retained raw source');
  f.advance(3001); p.setSlider(null); await drain();
  assert.equal(p.elements.canvas.lastSource, source, 'expired slider removal restores the retained raw pixels');
  assert.equal(p.rendered, rendered);
  assert.equal(f.callbacks.length, callbackCount);
});

test('local offsets coalesce without cancelling active work or displacing a queued source', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  p.setSlider(slider(0, 'dragging')); await drain();
  const callbackCount = f.callbacks.length;
  const wait = deferred(), seen = [];
  t.mock.method(p.controller.renderer, 'render', async frame => { seen.push(frame.rawId); await wait.promise; });
  p.setSlider(slider(0.1, 'dragging')); await drain();
  const inFlight = p.controller.inFlight;
  p.setSlider(slider(0.2, 'dragging'));
  p.setSlider(slider(0.6, 'dragging'));
  assert.equal(p.controller.inFlight, inFlight);
  assert.equal(p.controller.pending.visualOnly, true);
  f.receive(2);
  assert.equal(p.controller.pending.sequence, 2);
  assert.equal(p.controller.pending.visualOnly, false);
  p.setSlider(slider(0.8, 'dragging'));
  assert.equal(p.controller.pending.sequence, 2, 'local input does not drop a new source frame');
  assert.equal(p.controller.pending.visualOnly, false);
  wait.resolve(); await drain();
  assert.equal(seen.length, 3, 'local work, queued source, then the latest input finish in order');
  assert.equal(f.callbacks.length, callbackCount + 2, 'only the new source produces commit and proof callbacks');
  assert.equal(p.controller.snapshot().sequence, 2);
  assert.equal(p.controller.snapshot().displayConfirmed, true);
});

test('slider removal fences delayed decoration and restores stale HDR without granting freshness', async t => {
  const f = fixture(t), p = f.presentation;
  let time = 0;
  t.mock.method(performance, 'now', () => time);
  p.setPreference(true, 4); f.receive(1); await drain();
  p.setSlider(slider(0, 'dragging')); await drain();
  const rendered = p.rendered, callbackCount = f.callbacks.length;
  const wait = deferred(), presented = [];
  let staged;
  t.mock.method(p.controller.renderer, 'render', async frame => { staged = frame.decorated; await wait.promise; });
  t.mock.method(p.controller.renderer, 'present', async () => { presented.push(staged); });
  p.setSlider(slider(0.4, 'dragging')); await drain();
  f.advance(4000); time = 4000;
  p.setSlider(null);
  wait.resolve(); await drain();
  assert.deepEqual(presented, [false], 'cancelled decorated work never replaces the raw restoration');
  assert.equal(p.rendered, rendered);
  assert.equal(p.controller.snapshot().decorated, false);
  assert.equal(p.controller.snapshot().proofFresh, false);
  assert.equal(f.callbacks.length, callbackCount);
  assert.equal(p.elements.hdrCanvas.dataset.clientHdrSurface, 'visible');
});

test('same-sequence decorated proof cannot finish a frozen raw result before raw GPU completion', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  p.setSlider(slider()); await drain();
  assert.equal(p.controller.snapshot().decorated, true);
  const wait = deferred();
  t.mock.method(p.controller.renderer, 'render', async frame => {
    assert.equal(frame.decorated, false, 'the result bypasses slider composition');
    await wait.promise;
  });
  const request = { requestId: 'raw', status: 'succeeded', captureRequired: true,
    resultFrameEpoch: '1', resultMinFrameSequence: '1', resultMarkerRevision: '1:1' };
  let completed = false;
  const result = p.presentResult(request, () => true).then(value => { completed = true; return value; });
  await drain();
  assert.equal(completed, false);
  assert.equal(p.controller.snapshot().proofFresh, false);
  assert.equal(p.frozen.frame.decorated, false);
  const callbacks = f.callbacks.length;
  p.setSlider(slider(0.8, 'dragging')); await drain();
  assert.equal(f.callbacks.length, callbacks, 'frozen results ignore local slider input');
  wait.resolve();
  assert.equal(await result, true);
  assert.equal(p.elements.resultArea.dataset.presentation, 'exact-hdr');
  assert.equal(p.sliderAnimation, null);
});

test('canvas snapshot failure falls back once without dropping the SDR slider or inventing feedback', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const callbacks = f.callbacks.length;
  t.mock.property(globalThis, 'VideoFrame', class { constructor() { throw Error('conversion failed'); } });
  p.setSlider(slider()); await drain();
  assert.equal(p.controller, null);
  assert.equal(p.failure, 'slider_frame_conversion_failed');
  assert.deepEqual(f.failures, ['slider_frame_conversion_failed']);
  assert.equal(p.elements.canvas.lastSource, p.composition);
  assert.equal(p.rendered.sequence, 1);
  assert.equal(f.callbacks.length, callbacks);
  p.setSlider(slider(0.5, 'dragging')); await drain();
  assert.equal(f.failures.length, 1);
});

test('proof expiry refresh and gesture state do not cancel a renderer in flight', async t => {
  const f = fixture(t), p = f.presentation;
  p.setPreference(true, 4); f.receive(1); await drain();
  const initial = slider();
  initial.region.contextRevision = 4;
  initial.region.observedAt = 100;
  initial.region.expiresAt = 200;
  p.setSlider(initial); await drain();
  const generation = p.controller.presentationGeneration;
  p.setSlider({ ...initial, state: 'dragging', offset: 0.4,
    region: { ...initial.region, observedAt: 150, expiresAt: 250 } });
  assert.equal(p.controller.presentationGeneration, generation);
  assert.equal(p.slider.region.observedAt, undefined);
  p.setSlider({ ...initial, region: { ...initial.region, contextRevision: 5 } });
  assert.equal(p.controller.presentationGeneration, generation + 1, 'actual context change fences old work');
  await drain();
});

test('input and cancelled drag during initial activation do not duplicate source proof', async t => {
  const f = fixture(t), p = f.presentation;
  const wait = deferred();
  let activations = 0, localCopies = 0;
  t.mock.method(ClientHDRRenderer.prototype, 'render', async (_frame, options) => {
    if (options.activationFrame) activations++;
    else localCopies++;
    await wait.promise;
  });
  p.setSlider(slider()); p.setPreference(true, 4); f.receive(1); await drain();
  const callbacks = f.callbacks.length, inFlight = p.controller.inFlight;
  p.setSlider(slider(0.7, 'dragging'));
  p.setSlider({ ...slider(), resetFrom: 0.7, resetAt: performance.now() });
  assert.equal(p.controller.inFlight, inFlight);
  assert.equal(p.controller.pending, null, 'local input does not queue another source presentation');
  wait.resolve(); await drain();
  assert.equal(activations, 1);
  assert.equal(localCopies, 2, 'boosted activation is followed by the latest local input');
  assert.equal(f.callbacks.length, callbacks + 2, 'the accepted source is committed and confirmed once');
  assert.equal(p.controller.snapshot().decorated, true);
  assert.equal(p.slider.resetFrom, 0.7);
  assert.equal(p.controller.snapshot().sequence, 1);
});
