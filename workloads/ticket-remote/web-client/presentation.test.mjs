import test from 'node:test';
import assert from 'node:assert/strict';
import { Presentation } from './presentation.mjs';
import { ClientHDRRenderer } from './client-hdr-renderer.mjs';

const drain = async () => { for (let i = 0; i < 24; i++) await new Promise(setImmediate); };
const deferred = () => { let resolve; const promise = new Promise(done => resolve = done); return { promise, resolve }; };
function fixture(t) {
  const frames = new Set(), canvases = new Set(), renderers = [];
  class Frame {
    constructor() { frames.add(this); }
    clone() { assert.ok(frames.has(this)); return new Frame(); }
    close() { assert.ok(frames.delete(this), 'frame closed exactly once'); }
  }
  class Canvas {
    constructor() { this.width = 10; this.height = 20; this.dataset = {}; this.hidden = true; canvases.add(this); }
    getContext() { return { drawImage() {}, clearRect() {} }; }
    setAttribute() {}
    cloneNode() { return new Canvas(); }
    replaceWith() { canvases.delete(this); }
    before() {}
    remove() { canvases.delete(this); }
    toDataURL() { return 'data:image/png;base64,fixture'; }
  }
  const values = { VideoFrame: Frame, HTMLCanvasElement: Canvas, navigator: { gpu: {} },
    CSS: { supports: () => true }, matchMedia: () => ({ matches: true }),
    document: { body: { dataset: {}, classList: { add() {}, remove() {} } }, createElement: () => new Canvas() },
    window: { scrollTo() {} }, requestAnimationFrame: callback => setImmediate(callback) };
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
  return { presentation, elements, receive, renderers, callbacks, failures, canvases, frames, advance: value => now += value };
}

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

test('HDR failure silently reveals fresh SDR and stays there until the user retries', async t => {
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
  p.setVisible(false); p.setVisible(true); await drain();
  assert.equal(f.renderers.length, 1, 'background return does not retry a failed HDR device');
  p.setPreference(false, 4); p.setPreference(true, 4); await drain();
  assert.equal(p.visiblePicture().sequence, 2);
  assert.equal(p.failure, '');
  assert.equal(p.holdover, null);
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
  assert.equal(f.canvases.size, 3);
  const replacement = p.controller.renderer;
  p.controller.fail('test_failure'); wait.resolve(); await drain();
  assert.equal(p.holdover, null);
  assert.equal(p.controller, null);
  assert.equal(renderer.disposed, true);
  assert.equal(replacement.disposed, true);
  assert.equal(p.visiblePicture().sequence, 1);
  assert.equal(p.recovering, false);
  assert.equal(f.canvases.size, 2);
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
