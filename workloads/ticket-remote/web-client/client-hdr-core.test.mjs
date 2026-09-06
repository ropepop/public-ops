import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CLIENT_HDR_DISPLAY_BOOSTS,
  CLIENT_HDR_ENGINE,
  CLIENT_HDR_PAINT_WAIT_TIMEOUT_MILLIS,
  CLIENT_HDR_PIPELINE,
  CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS,
  CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS,
  ClientHDRController,
  clientHDRCapability,
  clientHDREngineProjectionDecision,
  clientHDRFreshness,
  normalizeClientHDRDisplayBoost,
  offerClientHDRCanvasFrame,
  resolveCapabilityHDREngine
} from './client-hdr-core.mjs';
import { CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS } from './client-hdr-renderer.mjs';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolveValue, rejectValue) => {
    resolve = resolveValue;
    reject = rejectValue;
  });
  return { promise, resolve, reject };
}

function tick() {
  return new Promise((resolve) => setImmediate(resolve));
}

function fakeFrame(label, closed) {
  return {
    clone() { return fakeFrame(`${label}-clone`, closed); },
    close() { closed.push(label); }
  };
}

function harness(options = {}) {
  let clock = 100;
  let wallClock = 1000;
  const renderers = [];
  const surfaces = [];
  const metrics = [];
  const statuses = [];
  const timers = [];
  const paintChecks = [];
  const recoveryRequests = [];
  const postPresentPaintGates = Array.isArray(options.postPresentPaintGates)
    ? Array.from(options.postPresentPaintGates)
    : null;
  const presentCompletionGates = Array.isArray(options.presentCompletionGates)
    ? Array.from(options.presentCompletionGates)
    : null;
  const postPresentOpportunityCounts = Array.isArray(options.postPresentOpportunityCounts)
    ? Array.from(options.postPresentOpportunityCounts)
    : null;
  const postPresentPaintErrors = Array.isArray(options.postPresentPaintErrors)
    ? Array.from(options.postPresentPaintErrors)
    : null;
  const rendererFactory = (rendererOptions) => {
    const renderer = {
      rendererOptions,
      initializeDeferred: options.deferredInitialize ? deferred() : null,
      renders: [],
      boosts: [],
      presents: 0,
      presentations: [],
      presentCompletionWaits: 0,
      postPresentPaints: 0,
      postPresentOpportunityTargets: [],
      compositorSettlementCancellations: [],
      discardedPreparedFrames: 0,
      disposed: false,
      currentBoost: 4,
      initialize(init = {}) {
        if (this.initializeDeferred) return this.initializeDeferred.promise;
        this.currentBoost = Number(init.boost) || 4;
        return Promise.resolve({
          canvasEncoding: 'srgb-linear',
          continuousSurface: true,
          edrRequestPatchIntended: this.currentBoost > 1,
          intendedRequestPatchPeak: this.currentBoost > 1 ? 1.25 : 0,
          intendedRequestPatchEdge: this.currentBoost > 1 ? 0.002 : 0
        });
      },
      render(frame, metadata, renderOptions = {}) {
        if (options.syncRenderFailure) {
          this.rendererOptions.onFailure('render_submit_failed:synthetic');
          return Promise.reject(new Error('render_submit_failed:synthetic'));
        }
        const operation = deferred();
        const render = { frame, metadata, options: renderOptions, operation };
        this.renders.push(render);
        this.preparedRender = render;
        const pairedActivationTarget = renderOptions.activationFrame !== true &&
          this.awaitingActivationTarget === true;
        if (options.autoRender || (pairedActivationTarget && options.autoTwoStageTarget !== false)) {
          operation.resolve(Object.assign({}, metadata, {
          queueDelayMillis: 2,
          submitMillis: 3,
          completionMillis: 8,
          displayReadyMillis: 16,
          decodedFrameToSubmitMillis: 5,
          decodedFrameToDisplayReadyMillis: 18,
          canvasEncoding: 'srgb-linear',
          displayBoost: renderOptions.activationFrame === true ? 1 : this.currentBoost,
          intendedOutputPeak: renderOptions.activationFrame === true
            ? renderOptions.requestPatch === true && this.currentBoost > 1 ? 1.25 : 1
            : this.currentBoost,
          selectedDisplayBoost: this.currentBoost,
          activationFrame: renderOptions.activationFrame === true,
          activationIdentity: renderOptions.activationFrame === true,
          edrRequestPatchIntended: renderOptions.requestPatch === true && this.currentBoost > 1,
          intendedRequestPatchPeak: renderOptions.requestPatch === true && this.currentBoost > 1 ? 1.25 : 0,
          intendedRequestPatchEdge: renderOptions.requestPatch === true && this.currentBoost > 1 ? 0.002 : 0,
          gpuCompleted: true,
          compositorOpportunitiesCompleted: false
          }));
        }
        return operation.promise;
      },
      setBoost(boost) {
        if (options.boostWriteError && boost !== 4) throw new Error('synthetic boost write error');
        this.currentBoost = boost;
        this.boosts.push(boost);
      },
      present() {
        if (options.presentError) throw new Error('synthetic present error');
        if (typeof options.onPresent === 'function') options.onPresent();
        this.presents += 1;
        this.presentations.push(this.preparedRender || null);
        if (this.preparedRender && this.preparedRender.options.activationFrame === true) {
          this.awaitingActivationTarget = true;
        } else if (this.awaitingActivationTarget) {
          this.awaitingActivationTarget = false;
        }
        this.preparedRender = null;
      },
      async waitForPresentCompletion() {
        this.presentCompletionWaits += 1;
        const completionGate = presentCompletionGates
          ? presentCompletionGates.shift()
          : options.presentCompletionGate;
        if (completionGate) await completionGate;
        if (options.presentCompletionError) throw new Error(String(options.presentCompletionError));
        return { presentCompletionMillis: 4, gpuCompleted: true, compositorOpportunitiesCompleted: false };
      },
      async waitForPresentedCompositorOpportunities(requiredFrames = 2) {
        this.postPresentPaints += 1;
        this.postPresentOpportunityTargets.push(requiredFrames);
        if (typeof options.onPostPresentPaintStart === 'function') {
          options.onPostPresentPaintStart(requiredFrames, this.postPresentPaints);
        }
        const paintGate = postPresentPaintGates ? postPresentPaintGates.shift() : options.postPresentPaintGate;
        if (paintGate) await paintGate;
        const paintError = postPresentPaintErrors
          ? postPresentPaintErrors.shift()
          : options.postPresentPaintError;
        if (paintError) throw new Error(String(paintError));
        if (options.postPresentPaintNull) return null;
        const source = String(options.postPresentPaintResult || 'animation_frame');
        return {
          postPresentSource: source,
          postPresentOpportunityCount: postPresentOpportunityCounts
            ? Number(postPresentOpportunityCounts.shift())
            : Object.prototype.hasOwnProperty.call(options, 'postPresentOpportunityCount')
            ? Number(options.postPresentOpportunityCount)
            : source === 'animation_frame' ? requiredFrames : 0,
          gpuCompleted: options.postPresentGPUCompleted !== false,
          compositorOpportunitiesCompleted: Object.prototype.hasOwnProperty.call(
            options,
            'postPresentCompositorOpportunitiesCompleted'
          )
            ? options.postPresentCompositorOpportunitiesCompleted === true
            : source === 'animation_frame'
        };
      },
      cancelCompositorSettlementWaits(reason = 'renderer_disposed') {
        this.compositorSettlementCancellations.push(reason);
        if (typeof options.onCancelCompositorSettlementWaits === 'function') {
          options.onCancelCompositorSettlementWaits(reason, this);
        }
      },
      discardPreparedFrame() { this.discardedPreparedFrames += 1; this.preparedRender = null; },
      dispose() { this.disposed = true; }
    };
    if (options.presentCompletion === false) delete renderer.waitForPresentCompletion;
    if (options.postPresentPaint === false) delete renderer.waitForPresentedCompositorOpportunities;
    renderers.push(renderer);
    return renderer;
  };
  const controller = new ClientHDRController({
    rendererFactory,
    now: () => clock,
    wallNow: () => wallClock,
    waitForPaint: options.waitForPaint || (() => Promise.resolve()),
    setTimer: (callback, millis) => {
      const timer = { callback, millis, cancelled: false };
      timers.push(timer);
      return timer;
    },
    clearTimer: (timer) => { if (timer) timer.cancelled = true; },
    rendererInitTimeoutMillis: options.rendererInitTimeoutMillis,
    paintWaitTimeoutMillis: options.paintWaitTimeoutMillis,
    settlementTimeoutMillis: options.settlementTimeoutMillis,
    schedulePaintCheck: (callback) => {
      const check = { callback, cancelled: false };
      paintChecks.push(check);
      return check;
    },
    cancelPaintCheck: (check) => { if (check) check.cancelled = true; },
    onSurface: (visible, presented, reason) => {
      surfaces.push({ visible, presented, reason });
      if (typeof options.onSurface === 'function') options.onSurface(visible, presented, reason);
    },
    canRevealSurface: () => typeof options.canRevealSurface === 'function'
      ? options.canRevealSurface()
      : options.canRevealSurface !== false,
    onStatus: (status, reason) => statuses.push({ status, reason }),
    onMetric: (event, detail) => metrics.push({ event, detail }),
    onRecoveryRequest: (reason, detail) => {
      recoveryRequests.push({ reason, detail });
      if (typeof options.onRecoveryRequest === 'function') options.onRecoveryRequest(reason, detail);
    },
    canReleaseHoldover: (presentation, snapshot) => typeof options.canReleaseHoldover === 'function'
      ? options.canReleaseHoldover(presentation, snapshot)
      : options.canReleaseHoldover !== false
  });
  const canvas = { width: 720, height: 1482, getContext() { return {}; } };
  return {
    controller,
    canvas,
    renderers,
    surfaces,
    statuses,
    metrics,
    timers,
    paintChecks,
    recoveryRequests,
    setClock(value) { clock = value; },
    setWallClock(value) { wallClock = value; }
  };
}

test('HDR uses the supported browser engine and bounded display boosts', () => {
  assert.equal(CLIENT_HDR_ENGINE, 'client_webgpu_v2');
  assert.equal(CLIENT_HDR_PIPELINE, 'webgpu-mainthread-edr-v2');
  assert.equal(CLIENT_HDR_PAINT_WAIT_TIMEOUT_MILLIS, 2000);
  assert.equal(CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS, 8000);
  assert.equal(CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS, 2000);
  assert.deepEqual(CLIENT_HDR_DISPLAY_BOOSTS, [2, 3, 4, 5, 6]);
  for (const boost of CLIENT_HDR_DISPLAY_BOOSTS) assert.equal(normalizeClientHDRDisplayBoost(boost), boost);
  for (const retired of [1, 8, 10, 12, 14, 16, null, 'invalid']) {
    assert.equal(normalizeClientHDRDisplayBoost(retired), 4);
  }
  assert.equal(resolveCapabilityHDREngine([CLIENT_HDR_ENGINE]), CLIENT_HDR_ENGINE);
  assert.equal(resolveCapabilityHDREngine([]), '');
  assert.deepEqual(clientHDREngineProjectionDecision({
    ownerProjectionAvailable: true,
    engine: CLIENT_HDR_ENGINE
  }, false), {
    ownerProjectionAvailable: true,
    roleLost: false,
    engine: CLIENT_HDR_ENGINE
  });
  assert.deepEqual(clientHDREngineProjectionDecision(null, true), {
    ownerProjectionAvailable: false,
    roleLost: true,
    engine: CLIENT_HDR_ENGINE
  });
});

test('capability requires main-thread WebGPU, a decoded frame, HDR display, and no-limit CSS', () => {
  const environment = {
    VideoFrame() {},
    HTMLCanvasElement: function HTMLCanvasElement() {},
    navigator: { gpu: {} },
    matchMedia: () => ({ matches: true }),
    CSS: { supports: (property, value) => property === 'dynamic-range-limit' && value === 'no-limit' }
  };
  environment.HTMLCanvasElement.prototype.getContext = () => ({});
  assert.equal(clientHDRCapability(environment).supported, true);
  delete environment.Worker;
  assert.equal(clientHDRCapability(environment).supported, true, 'workers are not part of v2 capability');
  environment.CSS.supports = () => false;
  assert.equal(clientHDRCapability(environment).supported, false);
});

test('freshness requires matching epoch and bounded sequence and age lag', () => {
  const current = { epoch: 7, sequence: 10, visualAgeMillis: 40, renderedAt: 100 };
  assert.equal(clientHDRFreshness({ epoch: 7, sequence: 9, visualAgeMillis: 80, offeredAt: 100 }, current, 110).fresh, true);
  assert.equal(clientHDRFreshness({ epoch: 7, sequence: 8, visualAgeMillis: 80, offeredAt: 100 }, current, 110).reason, 'sequence_lag');
  assert.equal(clientHDRFreshness({ epoch: 6, sequence: 10, visualAgeMillis: 80, offeredAt: 100 }, current, 110).reason, 'epoch_mismatch');
  assert.equal(clientHDRFreshness(
    { epoch: 7, sequence: 10, configGeneration: 12, visualAgeMillis: 40, offeredAt: 100 },
    { epoch: 7, sequence: 10, configGeneration: 13, visualAgeMillis: 40, renderedAt: 100 },
    110
  ).reason, 'config_generation_mismatch');
  assert.equal(clientHDRFreshness({ epoch: 7, sequence: 10, visualAgeMillis: 400, offeredAt: 100 }, current, 110).reason, 'visual_age');
  const boundaryCurrent = { epoch: 7, sequence: 10, visualAgeMillis: 0, renderedAt: 100 };
  assert.equal(clientHDRFreshness({ epoch: 7, sequence: 10, visualAgeMillis: 250, offeredAt: 100 }, boundaryCurrent, 100).fresh, true);
  assert.equal(clientHDRFreshness({ epoch: 7, sequence: 10, visualAgeMillis: 251, offeredAt: 100 }, boundaryCurrent, 100).reason, 'visual_age');
});

test('freshness counts visible presentations when source sequences are coalesced', () => {
  const presented = { epoch: 7, sequence: 20, presentationOrdinal: 10, visualAgeMillis: 40, offeredAt: 100 };
  const current = { epoch: 7, sequence: 22, presentationOrdinal: 11, visualAgeMillis: 40, renderedAt: 100 };
  const fresh = clientHDRFreshness(presented, current, 110);
  assert.equal(fresh.fresh, true);
  assert.equal(fresh.sequenceLag, 1);
  assert.equal(fresh.sourceSequenceLag, 2);
});

test('absolute frame age does not make matching retained SDR and HDR watermarks disagree', () => {
  const current = { epoch: 9, sequence: 44, presentationOrdinal: 12, visualAgeMillis: 5000, renderedAt: 100 };
  const presented = { epoch: 9, sequence: 44, presentationOrdinal: 12, visualAgeMillis: 5000, offeredAt: 100 };
  const fresh = clientHDRFreshness(presented, current, 3100);
  assert.equal(fresh.fresh, true);
  assert.equal(fresh.ageDeltaMillis, 0);
});

test('latest-frame mailbox closes superseded and disabled pending frames before renderer readiness', async () => {
  const state = harness({ deferredInitialize: true });
  const closed = [];
  assert.equal(state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 }), true);
  state.controller.offerFrame(fakeFrame('first', closed), { epoch: 1, sequence: 1 });
  state.controller.offerFrame(fakeFrame('second', closed), { epoch: 1, sequence: 2 });
  assert.deepEqual(closed, ['first-clone']);
  assert.equal(state.controller.snapshot().coalesced, 1);
  state.controller.dispose('disabled');
  assert.deepEqual(closed, ['first-clone', 'second-clone']);
  state.renderers[0].initializeDeferred.resolve({ canvasEncoding: 'srgb-linear' });
  await tick();
  assert.equal(state.renderers[0].disposed, true);
});

test('stalled renderer initialization times out and late completion cannot resurrect it', async () => {
  const state = harness({
    deferredInitialize: true,
    rendererInitTimeoutMillis: CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS
  });
  assert.equal(state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 }), true);
  const watchdog = state.timers.find((timer) => !timer.cancelled);
  assert.ok(watchdog);
  assert.equal(watchdog.millis, CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS);
  watchdog.callback();
  await tick();
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().rendererInitTimeoutPending, false);
  assert.equal(state.controller.snapshot().failures, 1);
  assert.equal(state.statuses.at(-1).status, 'failed');
  assert.equal(state.statuses.at(-1).reason, 'renderer_init_timeout');
  assert.equal(state.metrics.filter(({ event }) => event === 'renderer_init_timeout').length, 1);
  assert.equal(state.renderers[0].disposed, true);

  state.renderers[0].initializeDeferred.resolve({ canvasEncoding: 'srgb-linear' });
  await tick();
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().ready, false);
  assert.equal(state.renderers.length, 1, 'late initialization must not create a replacement renderer');
  assert.equal(state.statuses.filter(({ status }) => status === 'ready').length, 0);
});

test('wake-late renderer initialization cannot beat its suspended watchdog callback', async () => {
  const state = harness({
    deferredInitialize: true,
    rendererInitTimeoutMillis: CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS
  });
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 });
  state.setWallClock(1000 + CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS);
  state.renderers[0].initializeDeferred.resolve({ canvasEncoding: 'srgb-linear' });
  await tick();

  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().ready, false);
  assert.equal(state.statuses.filter(({ status }) => status === 'ready').length, 0);
  assert.ok(state.metrics.some(({ event, detail }) =>
    event === 'renderer_init_timeout' && detail.rendererInitCheckSource === 'completion'));
});

test('controller keeps one main-thread render in flight and presents only a fresh watermark', async () => {
  const state = harness();
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 });
  await tick();
  assert.equal(state.controller.snapshot().rendererGeneration, 1);
  assert.equal(state.controller.snapshot().gpuCompleted, false);
  assert.equal(state.controller.snapshot().compositorOpportunitiesCompleted, false);
  assert.equal(state.controller.snapshot().continuousSurface, true);
  assert.equal(state.controller.snapshot().edrRequestPatchIntended, true);
  assert.equal(state.controller.snapshot().intendedRequestPatchPeak, 1.25);
  assert.equal(state.controller.snapshot().intendedRequestPatchEdge, 0.002);
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 10, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('first', closed), { epoch: 4, sequence: 20, presentationOrdinal: 10, visualAgeMillis: 30, offeredAt: 100 });
  state.controller.offerFrame(fakeFrame('second', closed), { epoch: 4, sequence: 21, presentationOrdinal: 11, visualAgeMillis: 30, offeredAt: 100 });
  assert.equal(state.renderers[0].renders.length, 1);
  assert.equal(state.controller.snapshot().pending, true);
  assert.equal(state.controller.snapshot().gpuCompleted, false, 'staging submission cannot claim GPU completion');
  assert.equal(state.controller.snapshot().compositorOpportunitiesCompleted, false,
    'staging submission cannot claim compositor opportunities');
  state.renderers[0].renders[0].operation.resolve(Object.assign({}, state.renderers[0].renders[0].metadata, {
    queueDelayMillis: 2, submitMillis: 3, completionMillis: 8, displayReadyMillis: 16,
    decodedFrameToSubmitMillis: 5, decodedFrameToDisplayReadyMillis: 18,
    canvasEncoding: 'srgb-linear', intendedOutputPeak: 1.25, selectedDisplayBoost: 6,
    activationFrame: true, activationIdentity: true, edrRequestPatchIntended: true,
    gpuCompleted: true, compositorOpportunitiesCompleted: false,
    sourceColorSpace: 'primaries=bt709;transfer=iec61966-2-1;matrix=rgb;range=limited'
  }));
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().rendererActive, true);
  assert.equal(state.controller.snapshot().canvasEncoding, 'srgb-linear');
  assert.equal(
    state.controller.snapshot().sourceColorSpace,
    'primaries=bt709;transfer=iec61966-2-1;matrix=rgb;range=limited'
  );
  assert.equal(state.controller.snapshot().selectedDisplayBoost, 6);
  assert.equal(state.controller.snapshot().gpuCompleted, true);
  assert.equal(state.controller.snapshot().compositorOpportunitiesCompleted, true);
  assert.equal(state.controller.snapshot().postPresentSource, 'animation_frame');
  assert.equal(state.controller.snapshot().postPresentOpportunityCount, 1);
  assert.equal(state.controller.snapshot().activationPostPresentOpportunityCount, 2);
  assert.equal(state.renderers[0].renders.length, 3);
  assert.equal(state.renderers[0].renders[0].options.activationFrame, true);
  assert.equal(state.renderers[0].renders[0].options.requestPatch, true,
    'the SDR-identity activation frame must carry the bounded EDR request patch');
  assert.equal(state.renderers[0].renders[1].options.activationFrame, false);
  assert.equal(state.renderers[0].renders[1].options.requestPatch, false,
    'the full target must never carry the activation request patch');
  assert.equal(state.renderers[0].renders[2].metadata.sequence, 21);
  assert.equal(state.controller.snapshot().edrRequestPatchIntended, false,
    'the snapshot must describe the currently staging patch-free frame');
  assert.deepEqual(closed, ['first-clone']);
  assert.equal(state.controller.ensureExactProof(4, 19), false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  state.renderers[0].renders[2].operation.resolve(state.renderers[0].renders[2].metadata);
  await tick();
  assert.deepEqual(closed, ['first-clone', 'second-clone']);
});

test('SDR-identity activation stays continuously topmost before the full target is copied', async () => {
  const activationPainted = deferred();
  const targetPainted = deferred();
  const timeline = [];
  let state;
  state = harness({
    autoRender: true,
    postPresentPaint: true,
    postPresentPaintGates: [activationPainted.promise, targetPainted.promise],
    onSurface: (visible, _presented, reason) => timeline.push(`surface:${visible}:${reason}`),
    onPostPresentPaintStart: (requiredFrames) => {
      timeline.push(`post-present-paint-start:${requiredFrames}`);
      assert.equal(state.controller.snapshot().surfaceVisible, true,
        'the same activation surface must be onscreen for every compositor opportunity');
    }
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 6, sequence: 41, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('continuous-target', closed), {
    epoch: 6, sequence: 41, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.deepEqual(timeline, [
    'surface:false:starting',
    'surface:true:activation_copied',
    'post-present-paint-start:2'
  ]);
  assert.equal(state.renderers.length, 1, 'activation must keep the fresh renderer and canvas owner');
  assert.equal(state.renderers[0].presents, 1);
  assert.equal(state.renderers[0].renders.length, 1,
    'a stalled activation must not prepare or expose the full-strength target');
  assert.equal(state.renderers[0].presentations[0].options.activationFrame, true);
  assert.equal(state.renderers[0].presentations[0].options.requestPatch, true);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().presentationState, 'settling');
  assert.equal(state.controller.snapshot().compositorOpportunitiesCompleted, false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(state.surfaces.slice(1), [{
    visible: true,
    presented: state.surfaces[1].presented,
    reason: 'activation_copied'
  }], 'the activation surface cannot hide, demote, or be replaced while settling');

  activationPainted.resolve();
  await tick();
  assert.equal(state.metrics.filter(({ event }) => event === 'edr_activation_presented').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.equal(state.renderers[0].renders.length, 2);
  assert.equal(state.renderers[0].renders[1].options.activationFrame, false);
  assert.equal(state.renderers[0].renders[1].options.requestPatch, false);
  assert.equal(state.renderers[0].presents, 2);
  assert.deepEqual(state.renderers[0].postPresentOpportunityTargets, [2, 1]);
  assert.deepEqual(state.surfaces.slice(1), [{
    visible: true,
    presented: state.surfaces[1].presented,
    reason: 'activation_copied'
  }], 'the full target copy must not hide, demote, or replace the activation surface');

  targetPainted.resolve();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().presentationState, 'visible');
  assert.equal(state.controller.snapshot().compositorOpportunitiesCompleted, true);
  assert.equal(state.controller.snapshot().postPresentSource, 'animation_frame');
  assert.equal(state.controller.snapshot().postPresentOpportunityCount, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'first_presented').length, 1);
  assert.deepEqual(timeline, [
    'surface:false:starting',
    'surface:true:activation_copied',
    'post-present-paint-start:2',
    'post-present-paint-start:1'
  ]);
  assert.deepEqual(closed, ['continuous-target-clone']);
});

test('a stalled full-target render never replaces the settled SDR-identity activation', async () => {
  const state = harness({ autoTwoStageTarget: false });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 6, sequence: 42, presentationOrdinal: 42, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('target-gpu-stall', closed), {
    epoch: 6, sequence: 42, presentationOrdinal: 42, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(Object.assign({}, state.renderers[0].renders[0].metadata, {
    selectedDisplayBoost: 6,
    activationFrame: true,
    activationIdentity: true,
    edrRequestPatchIntended: true,
    intendedRequestPatchPeak: 1.25
  }));
  await tick();

  assert.equal(state.renderers[0].renders.length, 2);
  assert.equal(state.renderers[0].renders[1].options.activationFrame, false);
  assert.equal(state.renderers[0].presents, 1,
    'the stalled full target must not reach the visible swapchain');
  assert.equal(state.renderers[0].presentations[0].options.activationFrame, true);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.metrics.filter(({ event }) => event === 'edr_activation_presented').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);

  const targetGPUWatchdog = state.timers.findLast((timer) =>
    !timer.cancelled && timer.millis === CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  assert.ok(targetGPUWatchdog);
  targetGPUWatchdog.callback();
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.renderers[0].presentations.length, 1);

  state.renderers[0].renders[1].operation.resolve(state.renderers[0].renders[1].metadata);
  await tick();
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['target-gpu-stall-clone']);
});

test('a stalled target post-copy opportunity fails closed and late completion is inert', async () => {
  const targetPainted = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaintGates: [Promise.resolve(), targetPainted.promise],
    settlementTimeoutMillis: 2000
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 6, sequence: 43, presentationOrdinal: 43, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('target-paint-stall', closed), {
    epoch: 6, sequence: 43, presentationOrdinal: 43, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();

  assert.equal(state.renderers[0].presents, 2);
  assert.deepEqual(state.renderers[0].postPresentOpportunityTargets, [2, 1]);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.metrics.filter(({ event }) => event === 'edr_activation_presented').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  const settlementWatchdog = state.timers.findLast((timer) =>
    !timer.cancelled && timer.millis === 2000);
  assert.ok(settlementWatchdog);
  settlementWatchdog.callback();
  await tick();

  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  targetPainted.resolve();
  await tick();
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['target-paint-stall-clone']);
});

test('an independent settlement watchdog fails a copied target closed when compositor callbacks stall', async () => {
  const compositorGate = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaintGate: compositorGate.promise,
    settlementTimeoutMillis: 2000
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 9, sequence: 1, presentationOrdinal: 1, visualAgeMillis: 10, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('stalled-settlement', closed), {
    epoch: 9, sequence: 1, presentationOrdinal: 1, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();

  const settling = state.controller.snapshot();
  assert.equal(settling.presentationState, 'settling');
  assert.equal(settling.surfaceVisible, true);
  assert.equal(settling.settlementPending, true);
  assert.equal(state.metrics.filter(({ event }) => event === 'settlement_started').length, 1);
  const watchdog = state.timers.findLast((timer) => !timer.cancelled && timer.millis === 2000);
  assert.ok(watchdog, 'the copied target must own an independent settlement deadline');
  watchdog.callback();
  await tick();

  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().settlementPending, false);
  assert.equal(state.statuses.at(-1).reason, 'settlement_deadline_exceeded');
  assert.equal(state.metrics.filter(({ event }) => event === 'settlement_deadline_exceeded').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);

  compositorGate.resolve();
  await tick();
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false,
    'a late compositor callback must not resurrect the disposed target');
  assert.deepEqual(closed, ['stalled-settlement-clone']);
});

test('entering holdover while a copied frame settles cancels its teardown deadline', async () => {
  const stalledCompositor = deferred();
  let globalStreamFresh = true;
  const state = harness({
    autoRender: true,
    postPresentPaintGates: [Promise.resolve(), Promise.resolve(), stalledCompositor.promise],
    settlementTimeoutMillis: 2000,
    canReleaseHoldover: () => globalStreamFresh
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 10, sequence: 70, presentationOrdinal: 70, visualAgeMillis: 10, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('settling-holdover-initial', closed), {
    epoch: 10, sequence: 70, presentationOrdinal: 70, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().proofFresh, true);

  const renderer = state.renderers[0];
  const rendererGeneration = state.controller.snapshot().rendererGeneration;
  state.controller.noteSDRFrame({
    epoch: 10, sequence: 71, presentationOrdinal: 71, visualAgeMillis: 5, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('settling-holdover-copied', closed), {
    epoch: 10, sequence: 71, presentationOrdinal: 71, visualAgeMillis: 5, offeredAt: 100
  });
  await tick();

  const settling = state.controller.snapshot();
  assert.equal(settling.presentationState, 'settling');
  assert.equal(settling.settlementPending, true);
  assert.deepEqual({
    epoch: settling.epoch,
    sequence: settling.sequence,
    presentationOrdinal: settling.presentationOrdinal
  }, { epoch: 10, sequence: 71, presentationOrdinal: 71 },
  'metadata must already identify the pixels synchronously copied to the canvas');
  const staleWatchdog = state.timers.findLast((timer) =>
    !timer.cancelled && timer.millis === 2000);
  assert.ok(staleWatchdog);

  globalStreamFresh = false;
  assert.equal(state.controller.holdLastPresentation('video_socket_reconnecting'), true);
  const held = state.controller.snapshot();
  assert.equal(staleWatchdog.cancelled, true);
  assert.equal(held.settlementPending, false);
  assert.equal(held.presentationState, 'holdover');
  assert.equal(held.visualHoldover, true);
  assert.equal(held.proofFresh, false);
  assert.equal(held.surfaceVisible, true);
  assert.deepEqual({
    epoch: held.epoch,
    sequence: held.sequence,
    presentationOrdinal: held.presentationOrdinal
  }, { epoch: 10, sequence: 71, presentationOrdinal: 71 });

  state.setWallClock(4000);
  staleWatchdog.callback();
  await tick();
  const afterOldDeadline = state.controller.snapshot();
  assert.equal(afterOldDeadline.active, true);
  assert.equal(afterOldDeadline.surfaceVisible, true);
  assert.equal(afterOldDeadline.visualHoldover, true);
  assert.equal(afterOldDeadline.proofFresh, false);
  assert.equal(afterOldDeadline.rendererGeneration, rendererGeneration);
  assert.equal(renderer.disposed, false);
  assert.equal(state.metrics.some(({ event }) => event === 'settlement_deadline_exceeded'), false);

  stalledCompositor.resolve();
  await tick();
  const afterLateCompositor = state.controller.snapshot();
  assert.equal(afterLateCompositor.surfaceVisible, true);
  assert.equal(afterLateCompositor.presentationState, 'holdover');
  assert.equal(afterLateCompositor.visualHoldover, true);
  assert.equal(afterLateCompositor.proofFresh, false);
  assert.deepEqual({
    epoch: afterLateCompositor.epoch,
    sequence: afterLateCompositor.sequence,
    presentationOrdinal: afterLateCompositor.presentationOrdinal
  }, { epoch: 10, sequence: 71, presentationOrdinal: 71 });
  assert.equal(renderer.disposed, false);
  assert.deepEqual(closed, ['settling-holdover-initial-clone', 'settling-holdover-copied-clone']);
});

test('renderer compositor cancellation and a queued timeout cannot tear down holdover', async () => {
  for (const ending of ['cancelled', 'timeout_race']) {
    const stalledCompositor = deferred();
    let state;
    state = harness({
      autoRender: true,
      postPresentPaintGates: [Promise.resolve(), Promise.resolve(), stalledCompositor.promise],
      settlementTimeoutMillis: 2000,
      canReleaseHoldover: () => false,
      onCancelCompositorSettlementWaits: (reason) => {
        if (ending === 'cancelled') stalledCompositor.reject(new Error(reason));
      }
    });
    const closed = [];
    state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
    await tick();
    state.controller.noteSDRFrame({
      epoch: 11, sequence: 80, presentationOrdinal: 80, visualAgeMillis: 10, renderedAt: 100
    });
    state.controller.offerFrame(fakeFrame(`${ending}-initial`, closed), {
      epoch: 11, sequence: 80, presentationOrdinal: 80, visualAgeMillis: 10, offeredAt: 100
    });
    await tick();

    const renderer = state.renderers[0];
    const generation = state.controller.snapshot().rendererGeneration;
    const transitions = state.controller.snapshot().surfaceTransitions;
    state.controller.noteSDRFrame({
      epoch: 11, sequence: 81, presentationOrdinal: 81, visualAgeMillis: 5, renderedAt: 100
    });
    state.controller.offerFrame(fakeFrame(`${ending}-copied`, closed), {
      epoch: 11, sequence: 81, presentationOrdinal: 81, visualAgeMillis: 5, offeredAt: 100
    });
    await tick();
    assert.equal(state.controller.snapshot().presentationState, 'settling');
    assert.equal(state.controller.snapshot().settlementPending, true);

    assert.equal(state.controller.holdLastPresentation('control_socket_reconnecting'), true);
    assert.deepEqual(renderer.compositorSettlementCancellations,
      ['hdr_holdover_settlement_superseded']);
    if (ending === 'timeout_race') {
      // Model the renderer deadline already having settled its promise just
      // before cancellation reached it. Its queued timeout rejection still
      // belongs to the now-superseded compositor wait.
      stalledCompositor.reject(new Error('hdr_presented_display_refresh_timeout'));
    }
    await tick();

    const held = state.controller.snapshot();
    assert.equal(held.active, true, `${ending} must keep the controller active`);
    assert.equal(held.surfaceVisible, true, `${ending} must keep the HDR canvas visible`);
    assert.equal(held.presentationState, 'holdover');
    assert.equal(held.visualHoldover, true);
    assert.equal(held.proofFresh, false);
    assert.equal(held.paintPending, false);
    assert.equal(held.settlementPending, false);
    assert.equal(held.rendererGeneration, generation);
    assert.equal(held.surfaceTransitions, transitions);
    assert.deepEqual({
      epoch: held.epoch,
      sequence: held.sequence,
      presentationOrdinal: held.presentationOrdinal
    }, { epoch: 11, sequence: 81, presentationOrdinal: 81 },
    `${ending} must retain the metadata for the pixels already copied`);
    assert.equal(renderer.disposed, false);
    assert.equal(state.metrics.some(({ event }) => event === 'fallback'), false);
    assert.equal(state.metrics.some(({ event, detail }) => event === 'presented' &&
      detail && detail.sequence === 81), false);
    assert.equal(state.metrics.filter(({ event }) => event === 'holdover_settlement_superseded').length, 1);
    assert.deepEqual(closed, [`${ending}-initial-clone`, `${ending}-copied-clone`]);
  }
});

test('a fresh SDR frame enforces the settlement wall deadline even when the timer never fires', async () => {
  const compositorGate = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaintGate: compositorGate.promise,
    settlementTimeoutMillis: 2000
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 10, sequence: 4, presentationOrdinal: 4, visualAgeMillis: 10, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('wall-deadline', closed), {
    epoch: 10, sequence: 4, presentationOrdinal: 4, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().presentationState, 'settling');

  state.setWallClock(3001);
  assert.equal(state.controller.noteSDRFrame({
    epoch: 10, sequence: 5, presentationOrdinal: 5, visualAgeMillis: 10, renderedAt: 100
  }), false);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.ok(state.metrics.some(({ event, detail }) =>
    event === 'settlement_deadline_exceeded' && detail.settlementCheckSource === 'sdr_frame'));

  compositorGate.resolve();
  await tick();
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['wall-deadline-clone']);
});

test('wake-late compositor callbacks cannot cancel an expired wall deadline', async () => {
  const compositorGate = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaintGate: compositorGate.promise,
    settlementTimeoutMillis: 2000
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 12, sequence: 1, presentationOrdinal: 1, visualAgeMillis: 10, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('wake-late-compositor', closed), {
    epoch: 12, sequence: 1, presentationOrdinal: 1, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().presentationState, 'settling');

  // Model iOS freezing both timer and animation-frame queues. On wake the two
  // renderer callbacks resolve before the queued watchdog callback runs.
  state.setWallClock(3001);
  compositorGate.resolve();
  await tick();

  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.ok(state.metrics.some(({ event, detail }) =>
    event === 'settlement_deadline_exceeded' &&
    detail.settlementCheckSource === 'activation_compositor_completion'));
  assert.deepEqual(closed, ['wake-late-compositor-clone']);
});

test('the settlement deadline remains enforceable after a newer SDR frame hides the copied target', async () => {
  const compositorGate = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaintGate: compositorGate.promise,
    settlementTimeoutMillis: 2000
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 11, sequence: 8, presentationOrdinal: 8, visualAgeMillis: 10, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('stale-while-settling', closed), {
    epoch: 11, sequence: 8, presentationOrdinal: 8, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().presentationState, 'settling');

  state.setWallClock(1500);
  state.controller.noteSDRFrame({
    epoch: 11, sequence: 11, presentationOrdinal: 11, visualAgeMillis: 10, renderedAt: 100
  });
  assert.equal(state.controller.snapshot().presentationState, 'fallback_latched');
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().settlementPending, true,
    'SDR fallback must not orphan the owned compositor settlement');

  state.setWallClock(3001);
  assert.equal(state.controller.checkSettlementDeadline('foreground_return'), true);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().settlementPending, false);
  assert.ok(state.metrics.some(({ event, detail }) =>
    event === 'settlement_deadline_exceeded' && detail.settlementCheckSource === 'foreground_return'));

  compositorGate.resolve();
  await tick();
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['stale-while-settling-clone']);
});

test('failed activation compositor opportunities restore SDR without exposing the full target', async () => {
  const state = harness({
    autoRender: true,
    postPresentPaint: true,
    postPresentPaintError: 'hdr_presented_display_refresh_timeout'
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 6, sequence: 42, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('post-present-timeout', closed), {
    epoch: 6, sequence: 42, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.renderers[0].presents, 1);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.surfaces.some(({ visible, reason }) => visible && reason === 'activation_copied'), true);
  assert.equal(state.renderers[0].renders.length, 1);
  assert.equal(state.renderers[0].presentations.some((render) =>
    render && render.options.activationFrame === false), false);
  assert.equal(state.surfaces.at(-1).visible, false);
  assert.equal(state.statuses.at(-1).status, 'failed');
  assert.equal(state.statuses.at(-1).reason, 'hdr_presented_display_refresh_timeout');
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['post-present-timeout-clone']);
});

test('missing GPU-copy or exact two-opportunity confirmation fails closed without a synthetic success path', async () => {
  for (const scenario of [
    { label: 'copy-confirmation', options: { presentCompletion: false }, reason: 'renderer_present_completion_unavailable' },
    {
      label: 'compositor-confirmation',
      options: { postPresentPaint: false },
      reason: 'renderer_compositor_opportunities_unavailable'
    },
    { label: 'one-opportunity-only', options: { postPresentOpportunityCount: 1 }, reason: 'hdr_presented_display_refresh_failed' },
    { label: 'three-opportunities', options: { postPresentOpportunityCount: 3 }, reason: 'hdr_presented_display_refresh_failed' },
    { label: 'null-paint-result', options: { postPresentPaintNull: true }, reason: 'hdr_presented_display_refresh_failed' }
  ]) {
    const state = harness(Object.assign({ autoRender: true }, scenario.options));
    const closed = [];
    state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
    await tick();
    state.controller.noteSDRFrame({ epoch: 6, sequence: 50, visualAgeMillis: 20, renderedAt: 100 });
    state.controller.offerFrame(fakeFrame(scenario.label, closed), {
      epoch: 6, sequence: 50, visualAgeMillis: 20, offeredAt: 100
    });
    await tick();
    assert.equal(state.controller.snapshot().surfaceVisible, false, `${scenario.label} left HDR visible`);
    assert.equal(state.controller.snapshot().active, false, `${scenario.label} did not fail closed`);
    assert.equal(state.statuses.at(-1).reason, scenario.reason);
    assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
    assert.deepEqual(closed, [`${scenario.label}-clone`]);
  }
});

test('revoked reveal authority during activation settling returns to SDR without a target copy', async () => {
  const targetPainted = deferred();
  let revealAllowed = true;
  const state = harness({
    autoRender: true,
    postPresentPaint: true,
    postPresentPaintGate: targetPainted.promise,
    canRevealSurface: () => revealAllowed
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 6, sequence: 43, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('reveal-revoked', closed), {
    epoch: 6, sequence: 43, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.renderers[0].presents, 1);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.surfaces.at(-1).reason, 'activation_copied');
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  revealAllowed = false;
  targetPainted.resolve();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().presentationState, 'standby');
  assert.equal(state.surfaces.some(({ visible }) => visible), true);
  assert.equal(state.surfaces.at(-1).visible, false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.equal(state.renderers[0].renders.length, 1);
  assert.deepEqual(closed, ['reveal-revoked-clone']);
});

test('activation-time freshness revocation disposes the surface and fences queued work', async () => {
  const targetPainted = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaintGate: targetPainted.promise
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 6, sequence: 60, presentationOrdinal: 60, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('settling-stale', closed), {
    epoch: 6, sequence: 60, presentationOrdinal: 60, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().presentationState, 'settling');
  assert.equal(state.controller.snapshot().requestPatchPresented, true,
    'the one-time request patch must retire as soon as its copied target is topmost');
  assert.equal(state.renderers[0].renders[0].options.requestPatch, true);

  state.controller.noteSDRFrame({
    epoch: 6, sequence: 63, presentationOrdinal: 63, visualAgeMillis: 20, renderedAt: 100
  });
  assert.equal(state.controller.snapshot().surfaceVisible, false,
    'settling surfaces cannot remain visible after their freshness authority is revoked');
  assert.equal(state.surfaces.at(-1).visible, false);
  state.controller.offerFrame(fakeFrame('after-settle-revocation', closed), {
    epoch: 6, sequence: 63, presentationOrdinal: 63, visualAgeMillis: 20, offeredAt: 100
  });

  targetPainted.resolve();
  await tick();
  await tick();
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.renderers[0].renders.length, 1,
    'revoked activation must never start the full-strength target render');
  assert.equal(state.controller.snapshot().requestPatchPresented, true);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['after-settle-revocation-clone', 'settling-stale-clone']);
});

test('disposing during target copy completion fences its later paint and promotion', async () => {
  const targetCopied = deferred();
  const state = harness({
    autoRender: true,
    presentCompletion: true,
    presentCompletionGate: targetCopied.promise,
    postPresentPaint: true
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 6, sequence: 44, presentationOrdinal: 44, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('disposed-target', closed), {
    epoch: 6, sequence: 44, presentationOrdinal: 44, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.renderers[0].presents, 1);
  assert.equal(state.renderers[0].postPresentPaints, 0);
  state.controller.dispose('document_hidden');
  targetCopied.resolve();
  await tick();
  assert.equal(state.renderers[0].postPresentPaints, 0,
    'a disposed target must not enter the post-present paint gate');
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['disposed-target-clone']);
});

test('a boost change during activation blocks the old copy and leaves retry to the coordinator', async () => {
  const firstPainted = deferred();
  const state = harness({
    autoRender: true,
    postPresentPaint: true,
    postPresentPaintGate: firstPainted.promise
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482, boost: 6 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 6, sequence: 45, presentationOrdinal: 45, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('boost-old', closed), {
    epoch: 6, sequence: 45, presentationOrdinal: 45, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.renderers[0].presents, 1);
  assert.equal(state.controller.setDisplayBoost(5), true);
  state.controller.noteSDRFrame({ epoch: 6, sequence: 46, presentationOrdinal: 46, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('boost-new', closed), {
    epoch: 6, sequence: 46, presentationOrdinal: 46, visualAgeMillis: 20, offeredAt: 100
  });
  firstPainted.resolve();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, false,
    'the copied old boost must never become visible');
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.renderers[0].renders.length, 1,
    'the superseded activation must never expose either full-strength target');
  assert.equal(state.renderers[0].currentBoost, 5);
  assert.deepEqual(closed, ['boost-new-clone', 'boost-old-clone']);
});

test('ordinary-stream stale state and device loss reveal SDR immediately', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('source', closed), { epoch: 4, sequence: 20, visualAgeMillis: 30, offeredAt: 100 });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  state.controller.markSDRStale('stream_timeout');
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.surfaces.at(-1).reason, 'stream_timeout');
  state.renderers[0].rendererOptions.onFailure('device_lost');
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.statuses.at(-1).status, 'failed');
});

test('one synchronous GPU submit failure produces one fallback and one cleanup', async () => {
  const state = harness({ syncRenderFailure: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  assert.equal(state.controller.offerFrame(fakeFrame('submit-failure', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, offeredAt: 100
  }), false);
  const snapshot = state.controller.snapshot();
  assert.equal(snapshot.active, false);
  assert.equal(snapshot.failures, 1);
  assert.equal(snapshot.dropped, 1);
  assert.equal(state.statuses.filter(({ status }) => status === 'failed').length, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'fallback').length, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'session_summary').length, 1);
  assert.equal(state.timers.some((timer) => !timer.cancelled), false,
    'a renderer that fails during its async call cannot arm a watchdog after disposal');
  assert.deepEqual(closed, ['submit-failure-clone']);
});

test('sequence staleness hides an established HDR surface synchronously', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  const transitions = state.controller.snapshot().surfaceTransitions;

  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, renderedAt: 100 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.paintChecks.length, 0, 'sequence staleness cannot receive an animation-frame grace period');
  assert.equal(state.surfaces.at(-1).reason, 'sequence_lag');

  state.controller.offerFrame(fakeFrame('caught-up', closed), {
    epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions + 2);
  assert.equal(state.metrics.some(({ event }) => event.startsWith('source_advance_mismatch_')), false);
});

test('an already-drawn control-code frame hands off to HDR without hiding the visible surface', async () => {
  let controlPaint = null;
  let authoritativeRenderSerial = 20;
  const state = harness({
    autoRender: true,
    waitForPaint: () => controlPaint ? controlPaint.promise : Promise.resolve()
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  const transitions = state.controller.snapshot().surfaceTransitions;
  const surfaceEvents = state.surfaces.length;

  controlPaint = deferred();
  authoritativeRenderSerial = 21;
  state.controller.offerFrame(fakeFrame('priority-21', closed), {
    epoch: 4, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: (_frame, candidate) => authoritativeRenderSerial === 21 ? candidate : false
  });
  await tick();
  assert.equal(state.controller.snapshot().paintPending, true);
  assert.equal(state.controller.snapshot().surfaceVisible, true);

  authoritativeRenderSerial = 22;
  state.controller.offerFrame(fakeFrame('priority-22', closed), {
    epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: (_frame, candidate) => authoritativeRenderSerial === 22 ? candidate : false
  });
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions);

  controlPaint.resolve();
  await tick();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().presentationState, 'visible');
  assert.equal(state.controller.snapshot().sequence, 22);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions);
  assert.equal(state.surfaces.length, surfaceEvents, 'control-code continuity must not emit an HDR-to-SDR transition');
  assert.equal(state.controller.ensureExactProof(4, 22), true);
  assert.deepEqual(closed, ['initial-clone', 'priority-21-clone', 'priority-22-clone']);
});

test('a prepared catch-up frame cannot delay synchronous sequence fallback', async () => {
  let catchUpPaint = null;
  const state = harness({
    autoRender: true,
    waitForPaint: () => catchUpPaint ? catchUpPaint.promise : Promise.resolve()
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  const transitions = state.controller.snapshot().surfaceTransitions;

  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, renderedAt: 100 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  catchUpPaint = deferred();
  state.controller.offerFrame(fakeFrame('gpu-complete', closed), {
    epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().paintPending, true);
  assert.equal(state.controller.presented.sequence, 20, 'the older frame cannot regain display authority');
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.paintChecks.length, 0);

  catchUpPaint.resolve();
  await tick();
  assert.equal(state.controller.presented.sequence, 22);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions + 2);
  assert.equal(state.controller.snapshot().paintPending, false);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.deepEqual(closed, ['initial-clone', 'gpu-complete-clone']);
});

test('a prepared catch-up frame cannot delay synchronous visual-age fallback', async () => {
  let catchUpPaint = null;
  const state = harness({
    autoRender: true,
    waitForPaint: () => catchUpPaint ? catchUpPaint.promise : Promise.resolve()
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  const transitions = state.controller.snapshot().surfaceTransitions;

  state.setClock(500);
  state.controller.noteSDRFrame({ epoch: 4, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 0, renderedAt: 500 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.surfaces.at(-1).reason, 'visual_age');
  assert.equal(state.paintChecks.length, 0);

  catchUpPaint = deferred();
  state.controller.offerFrame(fakeFrame('age-catch-up', closed), {
    epoch: 4, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 0, offeredAt: 500
  });
  await tick();
  assert.equal(state.controller.snapshot().paintPending, true);
  assert.equal(state.controller.snapshot().surfaceVisible, false);

  catchUpPaint.resolve();
  await tick();
  assert.equal(state.controller.presented.sequence, 21);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions + 2);
  assert.equal(state.controller.snapshot().paintPending, false);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.deepEqual(closed, ['initial-clone', 'age-catch-up-clone']);
});

test('a sequence gap reveals SDR in the same source-advance call', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, renderedAt: 100 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.paintChecks.length, 0);
  assert.equal(state.surfaces.at(-1).reason, 'sequence_lag');
});

test('a relative-age mismatch reveals SDR in the same source-advance call', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  state.setClock(500);
  state.controller.noteSDRFrame({ epoch: 4, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 0, renderedAt: 500 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.paintChecks.length, 0);
  assert.equal(state.surfaces.at(-1).reason, 'visual_age');
});

test('disposing after synchronous freshness fallback has no deferred check to cancel', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
  });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, renderedAt: 100 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.paintChecks.length, 0);
  state.controller.dispose('disabled');
  assert.equal(state.controller.snapshot().rendererActive, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
});

test('epoch, hard-stale, and exact-proof failures still reveal SDR synchronously', async () => {
  for (const failure of ['epoch', 'hard', 'proof']) {
    const state = harness({ autoRender: true });
    const closed = [];
    state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
    await tick();
    state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
    state.controller.offerFrame(fakeFrame(failure, closed), {
      epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, offeredAt: 100
    });
    await tick();
    if (failure === 'epoch') {
      state.controller.noteSDRFrame({ epoch: 5, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 30, renderedAt: 100 });
    } else if (failure === 'hard') {
      state.controller.markSDRStale('stream_unavailable');
    } else {
      state.controller.ensureExactProof(4, 19);
    }
    assert.equal(state.controller.snapshot().surfaceVisible, false, `${failure} must hide synchronously`);
    assert.equal(state.paintChecks.length, 0);
  }
});

test('a presented HDR surface stays live through scroll while transient recovery proof remains passive', async () => {
  let globalStreamFresh = false;
  const state = harness({
    autoRender: true,
    canReleaseHoldover: () => globalStreamFresh
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();

  const renderer = state.renderers[0];
  const generation = state.controller.snapshot().rendererGeneration;
  const transitions = state.controller.snapshot().surfaceTransitions;
  const surfaceEventsBeforeScroll = state.surfaces.length;
  let heldPresentation = {
    epoch: state.controller.snapshot().epoch,
    sequence: state.controller.snapshot().sequence,
    presentationOrdinal: state.controller.snapshot().presentationOrdinal
  };
  let presentsBeforeHoldoverCandidate = renderer.presents;
  assert.equal(state.controller.snapshot().firstPresented, true);
  assert.equal(state.metrics.filter(({ event }) => event === 'first_presented').length, 1);

  assert.equal(state.controller.setStreamRegionVisible(false), true);
  const offscreen = state.controller.snapshot();
  assert.deepEqual({
    active: offscreen.active,
    ready: offscreen.ready,
    visible: offscreen.surfaceVisible,
    state: offscreen.presentationState,
    firstPresented: offscreen.firstPresented,
    streamRegionVisible: offscreen.streamRegionVisible,
    proofFresh: offscreen.proofFresh
  }, {
    active: true,
    ready: true,
    visible: true,
    state: 'visible',
    firstPresented: true,
    streamRegionVisible: false,
    proofFresh: true
  }, 'scrolling away must retain the fresh HDR picture without hiding it');
  assert.equal(state.controller.ensureExactProof(4, 20), true,
    'scrolling to the below-stream controls must preserve exact action proof');
  assert.equal(state.controller.snapshot().surfaceVisible, true,
    'off-screen bookkeeping must not latch SDR fallback');
  assert.equal(state.controller.snapshot().rendererGeneration, generation);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions);
  assert.equal(state.surfaces.length, surfaceEventsBeforeScroll,
    'scrolling away must not emit another surface transition');
  assert.equal(renderer.presents, presentsBeforeHoldoverCandidate,
    'scrolling away must not recopy or replace the presented pixels');
  assert.equal(renderer.disposed, false);
  assert.deepEqual({
    epoch: state.controller.snapshot().epoch,
    sequence: state.controller.snapshot().sequence,
    presentationOrdinal: state.controller.snapshot().presentationOrdinal
  }, heldPresentation, 'scrolling away must retain the exact presented watermark');

  state.controller.noteSDRFrame({
    epoch: 4, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 15, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('offscreen-fresh', closed), {
    epoch: 4, sequence: 21, presentationOrdinal: 21, visualAgeMillis: 15, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().streamRegionVisible, false);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().rendererGeneration, generation);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions);
  assert.equal(renderer.presents, presentsBeforeHoldoverCandidate + 1,
    'the live HDR renderer must continue presenting newer frames while naturally off-screen');
  assert.equal(renderer.disposed, false);
  assert.equal(state.controller.ensureExactProof(4, 21), true,
    'the latest off-screen presentation must remain exact proof for below-stream controls');
  heldPresentation = {
    epoch: state.controller.snapshot().epoch,
    sequence: state.controller.snapshot().sequence,
    presentationOrdinal: state.controller.snapshot().presentationOrdinal
  };
  presentsBeforeHoldoverCandidate = renderer.presents;
  const presentedMetricsBeforeHoldoverCandidate = state.metrics.filter(
    ({ event }) => event === 'presented'
  ).length;

  assert.equal(state.controller.setStreamRegionVisible(true), true);
  assert.equal(state.controller.snapshot().surfaceVisible, true,
    'scrolling away and back must not expose SDR');
  assert.equal(state.controller.snapshot().proofFresh, true,
    'returning in view must retain proof for the exact fresh presentation');
  assert.equal(state.controller.ensureExactProof(4, 21), true);
  assert.equal(state.controller.snapshot().rendererGeneration, generation,
    'scrolling must not recreate the renderer');
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions);
  assert.equal(renderer.disposed, false);

  assert.equal(state.controller.holdLastPresentation('video_socket_reconnecting'), true);
  assert.deepEqual({
    visible: state.controller.snapshot().surfaceVisible,
    state: state.controller.snapshot().presentationState,
    holdover: state.controller.snapshot().visualHoldover,
    proofFresh: state.controller.snapshot().proofFresh
  }, { visible: true, state: 'holdover', holdover: true, proofFresh: false });
  assert.equal(state.controller.ensureExactProof(4, 21), false,
    'a held bright picture must not count as fresh exact-frame proof');
  assert.equal(state.controller.snapshot().surfaceVisible, true,
    'refusing proof during a transient wait must not flash through SDR');
  assert.equal(state.controller.holdLastPresentation('video_socket_reconnecting'), true);
  assert.equal(state.metrics.filter(({ event }) => event === 'presentation_holdover').length, 1,
    'repeated stale watchdog observations must coalesce into one visual holdover');
  assert.equal(renderer.disposed, false);

  state.controller.noteSDRFrame({
    epoch: 5, sequence: 1, presentationOrdinal: 22, visualAgeMillis: 10, renderedAt: 100
  });
  assert.equal(state.controller.snapshot().surfaceVisible, true,
    'the new epoch watermark must stay hidden beneath the held HDR canvas');
  state.controller.offerFrame(fakeFrame('fresh-keyframe', closed), {
    epoch: 5, sequence: 1, presentationOrdinal: 22, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();

  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().visualHoldover, true,
    'a relative-fresh HDR copy must remain unproven while the global stream is stale');
  assert.equal(state.controller.snapshot().proofFresh, false);
  assert.equal(renderer.presents, presentsBeforeHoldoverCandidate,
    'a globally stale candidate must be discarded before it copies over the held pixels');
  assert.deepEqual({
    epoch: state.controller.snapshot().epoch,
    sequence: state.controller.snapshot().sequence,
    presentationOrdinal: state.controller.snapshot().presentationOrdinal
  }, heldPresentation, 'a rejected candidate must not replace the held presentation metadata');
  assert.equal(renderer.discardedPreparedFrames, 1,
    'a globally stale prepared texture must be explicitly discarded');
  assert.equal(state.metrics.filter(({ event }) => event === 'presented').length,
    presentedMetricsBeforeHoldoverCandidate,
    'a globally stale candidate must not emit presented telemetry');
  assert.equal(state.metrics.filter(({ event }) => event === 'holdover_release_deferred').length, 1);

  globalStreamFresh = true;
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 2, presentationOrdinal: 23, visualAgeMillis: 5, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('globally-fresh-keyframe', closed), {
    epoch: 5, sequence: 2, presentationOrdinal: 23, visualAgeMillis: 5, offeredAt: 100
  });
  await tick();

  const resumed = state.controller.snapshot();
  assert.equal(resumed.surfaceVisible, true);
  assert.equal(resumed.presentationState, 'visible');
  assert.equal(resumed.visualHoldover, false);
  assert.equal(resumed.proofFresh, true);
  assert.equal(resumed.rendererGeneration, generation);
  assert.equal(resumed.surfaceTransitions, transitions,
    'fresh HDR must replace the holdover without an SDR/HDR visibility transition');
  assert.equal(renderer.disposed, false);
  assert.equal(state.metrics.filter(({ event }) => event === 'first_presented').length, 1,
    'reconnect recovery must not make a second first-presented claim');
  assert.equal(state.metrics.filter(({ event }) => event === 'presented').length,
    presentedMetricsBeforeHoldoverCandidate + 1,
    'only the newly copied keyframe may restore fresh presentation telemetry');
  assert.equal(state.surfaces.some(({ visible }, index) => index > 0 && !visible), false,
    'scroll, stale watchdog, reconnect, and keyframe wait must never expose SDR');
  assert.deepEqual(closed, [
    'initial-clone',
    'offscreen-fresh-clone',
    'fresh-keyframe-clone',
    'globally-fresh-keyframe-clone'
  ]);
});

test('holdover stays passive when global authority is lost during compositor confirmation', async () => {
  let globalStreamFresh = true;
  let revokeAtPaint = 0;
  const state = harness({
    autoRender: true,
    canReleaseHoldover: () => globalStreamFresh,
    onPostPresentPaintStart: (_requiredFrames, count) => {
      if (count === revokeAtPaint) globalStreamFresh = false;
    }
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 8, sequence: 40, presentationOrdinal: 40, visualAgeMillis: 10, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('race-initial', closed), {
    epoch: 8, sequence: 40, presentationOrdinal: 40, visualAgeMillis: 10, offeredAt: 100
  });
  await tick();

  const renderer = state.renderers[0];
  const generation = state.controller.snapshot().rendererGeneration;
  const transitions = state.controller.snapshot().surfaceTransitions;
  const presentsBeforeRace = renderer.presents;
  state.controller.holdLastPresentation('control_socket_reconnecting');
  revokeAtPaint = renderer.postPresentPaints + 1;
  state.controller.noteSDRFrame({
    epoch: 8, sequence: 41, presentationOrdinal: 41, visualAgeMillis: 5, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('race-candidate', closed), {
    epoch: 8, sequence: 41, presentationOrdinal: 41, visualAgeMillis: 5, offeredAt: 100
  });
  await tick();

  const heldRace = state.controller.snapshot();
  assert.equal(renderer.presents, presentsBeforeRace + 1,
    'the candidate authorized at copy time should reach the continuous canvas once');
  assert.deepEqual({
    epoch: heldRace.epoch,
    sequence: heldRace.sequence,
    presentationOrdinal: heldRace.presentationOrdinal
  }, { epoch: 8, sequence: 41, presentationOrdinal: 41 },
  'metadata must describe pixels already copied before authority changed');
  assert.equal(heldRace.visualHoldover, true);
  assert.equal(heldRace.proofFresh, false);
  assert.equal(heldRace.presentationState, 'holdover');
  assert.equal(state.metrics.filter(({ event }) => event === 'presented').length, 0);
  assert.equal(state.metrics.findLast(({ event }) => event === 'holdover_release_deferred').detail.stage,
    'after_compositor');

  globalStreamFresh = true;
  revokeAtPaint = 0;
  state.controller.noteSDRFrame({
    epoch: 8, sequence: 42, presentationOrdinal: 42, visualAgeMillis: 5, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('race-recovered', closed), {
    epoch: 8, sequence: 42, presentationOrdinal: 42, visualAgeMillis: 5, offeredAt: 100
  });
  await tick();

  const resumed = state.controller.snapshot();
  assert.equal(resumed.visualHoldover, false);
  assert.equal(resumed.proofFresh, true);
  assert.equal(resumed.rendererGeneration, generation);
  assert.equal(resumed.surfaceTransitions, transitions);
  assert.equal(renderer.disposed, false);
  assert.deepEqual(closed, ['race-initial-clone', 'race-candidate-clone', 'race-recovered-clone']);
});

test('activation-copy races retain the pixels actually copied while holdover stays passive', async () => {
  for (const lossStage of ['activation_present', 'activation_compositor']) {
    let globalStreamFresh = true;
    let revokeAtPresent = 0;
    let revokeAtPaint = 0;
    let state;
    state = harness({
      autoRender: true,
      canReleaseHoldover: () => globalStreamFresh,
      onPresent: () => {
        const renderer = state.renderers[0];
        if (renderer.presents + 1 === revokeAtPresent) globalStreamFresh = false;
      },
      onPostPresentPaintStart: (_requiredFrames, count) => {
        if (count === revokeAtPaint) globalStreamFresh = false;
      }
    });
    const closed = [];
    state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
    await tick();
    state.controller.noteSDRFrame({
      epoch: 9, sequence: 60, presentationOrdinal: 60, visualAgeMillis: 10, renderedAt: 100
    });
    state.controller.offerFrame(fakeFrame(`${lossStage}-initial`, closed), {
      epoch: 9, sequence: 60, presentationOrdinal: 60, visualAgeMillis: 10, offeredAt: 100
    });
    await tick();

    const renderer = state.renderers[0];
    assert.equal(state.controller.holdLastPresentation('waiting_for_next_keyframe'), true);
    // Force the otherwise rare recovery branch where an established surface
    // still needs the bounded activation/request-patch copy.
    state.controller.requestPatchPresented = false;
    const presentsBefore = renderer.presents;
    const rendersBefore = renderer.renders.length;
    if (lossStage === 'activation_present') revokeAtPresent = presentsBefore + 1;
    else revokeAtPaint = renderer.postPresentPaints + 1;

    state.controller.noteSDRFrame({
      epoch: 9, sequence: 61, presentationOrdinal: 61, visualAgeMillis: 5, renderedAt: 100
    });
    state.controller.offerFrame(fakeFrame(`${lossStage}-candidate`, closed), {
      epoch: 9, sequence: 61, presentationOrdinal: 61, visualAgeMillis: 5, offeredAt: 100
    });
    await tick();

    const held = state.controller.snapshot();
    assert.equal(renderer.presents, presentsBefore + 1,
      `${lossStage} must copy exactly the activation pixels already submitted`);
    assert.equal(renderer.renders.length, rendersBefore + 1,
      `${lossStage} must not prepare or copy the full target after authority loss`);
    assert.deepEqual({
      epoch: held.epoch,
      sequence: held.sequence,
      presentationOrdinal: held.presentationOrdinal
    }, { epoch: 9, sequence: 61, presentationOrdinal: 61 },
    `${lossStage} metadata must identify the activation pixels now on the canvas`);
    assert.equal(held.surfaceVisible, true);
    assert.equal(held.visualHoldover, true);
    assert.equal(held.proofFresh, false);
    assert.equal(held.presentationState, 'holdover');
    assert.equal(held.settlementPending, false,
      `${lossStage} must not leave a timer that later disposes the held surface`);
    assert.equal(state.metrics.filter(({ event }) => event === 'presented').length, 0);
    assert.equal(state.metrics.findLast(({ event }) => event === 'holdover_release_deferred').detail.stage,
      lossStage === 'activation_present' ? 'after_activation_copy' : 'after_activation_compositor');
    assert.equal(renderer.disposed, false);
    assert.deepEqual(closed, [`${lossStage}-initial-clone`, `${lossStage}-candidate-clone`]);
  }
});

test('a completed GPU frame is not reported as displayed when freshness rejects it', async () => {
  const state = harness();
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('stale', closed), {
    epoch: 4,
    sequence: 18,
    presentationOrdinal: 18,
    visualAgeMillis: 30,
    offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.metrics.some((metric) => metric.event === 'first_presented'), false);
  assert.equal(state.controller.firstPresented, false);
  assert.deepEqual(closed, ['stale-clone']);
});

test('a frame that becomes stale before the revealed canvas paints is not reported as shown', async () => {
  const paint = deferred();
  const state = harness({ waitForPaint: () => paint.promise });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 30, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('paint-race', closed), {
    epoch: 4,
    sequence: 20,
    presentationOrdinal: 20,
    visualAgeMillis: 30,
    offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, false, 'an unpainted WebGPU surface is never revealed');
  assert.equal(state.metrics.some((metric) => metric.event === 'first_presented'), false);
  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 30, renderedAt: 100 });
  paint.resolve();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.renderers[0].presents, 0, 'stale staging must be discarded before the swapchain copy');
  assert.equal(state.renderers[0].discardedPreparedFrames, 1);
  assert.equal(state.metrics.some((metric) => metric.event === 'first_presented'), false);
  assert.equal(state.controller.firstPresented, false);
  assert.deepEqual(closed, ['paint-race-clone']);
});

test('one staging texture presents before the pending GPU render starts', async () => {
  const firstPaint = deferred();
  const secondPaint = deferred();
  const paints = [firstPaint, secondPaint];
  const state = harness({ waitForPaint: () => paints.shift().promise });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 5, sequence: 30, presentationOrdinal: 30, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('paint-first', closed), {
    epoch: 5, sequence: 30, presentationOrdinal: 30, visualAgeMillis: 20, offeredAt: 100
  });
  state.controller.noteSDRFrame({ epoch: 5, sequence: 31, presentationOrdinal: 31, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('paint-pending', closed), {
    epoch: 5, sequence: 31, presentationOrdinal: 31, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  assert.equal(state.renderers[0].renders.length, 1, 'the staging texture cannot be overwritten before presentation');
  assert.equal(state.controller.snapshot().inFlight, true);
  assert.equal(state.controller.snapshot().pending, true);
  assert.equal(state.controller.snapshot().ownedFrameCount, 2);
  assert.equal(state.controller.presented, null, 'presentation authority waits for paint');
  assert.deepEqual(closed, []);

  firstPaint.resolve();
  await tick();
  assert.equal(state.renderers[0].presents, 2);
  assert.equal(state.renderers[0].renders.length, 3);
  assert.equal(state.controller.presented.sequence, 30);
  assert.deepEqual(closed, ['paint-first-clone']);

  state.renderers[0].renders[2].operation.resolve(state.renderers[0].renders[2].metadata);
  await tick();
  assert.equal(state.controller.snapshot().paintPending, true);
  secondPaint.resolve();
  await tick();
  assert.equal(state.controller.presented.sequence, 31);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  state.controller.dispose('test_complete');
  assert.deepEqual(closed, ['paint-first-clone', 'paint-pending-clone']);
});

test('a missing paint callback drops only that frame and immediately renders the newest retry', async () => {
  const firstPaint = deferred();
  const secondPaint = deferred();
  const paints = [firstPaint, secondPaint];
  const paintWaitTimeoutMillis = 400;
  const state = harness({
    paintWaitTimeoutMillis,
    waitForPaint: () => paints.shift().promise
  });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 30, presentationOrdinal: 30, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('paint-stalled', closed), {
    epoch: 5, sequence: 30, presentationOrdinal: 30, visualAgeMillis: 20, offeredAt: 100
  });
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 31, presentationOrdinal: 31, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('paint-retry', closed), {
    epoch: 5, sequence: 31, presentationOrdinal: 31, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();

  let snapshot = state.controller.snapshot();
  assert.equal(snapshot.active, true);
  assert.equal(snapshot.ready, true);
  assert.equal(snapshot.surfaceVisible, false);
  assert.equal(snapshot.presentationState, 'acquiring');
  assert.equal(snapshot.paintPending, true);
  assert.equal(snapshot.paintWaitTimeoutPending, true);
  assert.equal(snapshot.gpuCompletionTimeoutPending, false);
  assert.equal(snapshot.inFlight, true);
  assert.equal(snapshot.pending, true);
  assert.equal(snapshot.rendered, 1);
  assert.equal(snapshot.failures, 0);
  assert.equal(snapshot.ownedFrameCount, 2);
  assert.equal(state.renderers[0].presents, 0);

  const paintTimeout = state.timers.findLast((timer) =>
    !timer.cancelled && timer.millis === paintWaitTimeoutMillis);
  assert.ok(paintTimeout);
  paintTimeout.callback();
  await tick();

  snapshot = state.controller.snapshot();
  assert.equal(snapshot.active, true, 'a missed paint is not a terminal renderer failure');
  assert.equal(snapshot.ready, true);
  assert.equal(snapshot.surfaceVisible, false);
  assert.equal(snapshot.presentationState, 'acquiring');
  assert.equal(snapshot.paintPending, false);
  assert.equal(snapshot.paintWaitTimeoutPending, false);
  assert.equal(snapshot.inFlight, true, 'the newest queued frame should render immediately');
  assert.equal(snapshot.pending, false);
  assert.equal(snapshot.failures, 0);
  assert.equal(snapshot.ownedFrameCount, 1);
  assert.equal(state.renderers[0].renders.length, 2);
  assert.equal(state.renderers[0].discardedPreparedFrames, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'paint_wait_timeout').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'fallback'), false);
  assert.equal(state.metrics.some(({ event }) => event === 'session_summary'), false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['paint-stalled-clone']);

  firstPaint.resolve();
  await tick();
  assert.equal(state.renderers[0].presents, 0, 'a late paint callback cannot present the discarded frame');
  assert.equal(state.metrics.filter(({ event }) => event === 'paint_wait_timeout').length, 1);
  assert.deepEqual(closed, ['paint-stalled-clone']);

  state.renderers[0].renders[1].operation.resolve(state.renderers[0].renders[1].metadata);
  await tick();
  const retryPaintTimeout = state.timers.findLast((timer) =>
    !timer.cancelled && timer.millis === paintWaitTimeoutMillis);
  assert.ok(retryPaintTimeout);
  secondPaint.resolve();
  await tick();
  snapshot = state.controller.snapshot();
  assert.equal(retryPaintTimeout.cancelled, true);
  assert.equal(snapshot.surfaceVisible, true);
  assert.equal(snapshot.paintWaitTimeoutPending, false);
  assert.equal(snapshot.ownedFrameCount, 0);
  assert.equal(state.controller.presented.sequence, 31);
  assert.equal(state.renderers[0].presents, 2);
  assert.equal(state.metrics.filter(({ event }) => event === 'first_presented').length, 1);
  assert.deepEqual(closed, ['paint-stalled-clone', 'paint-retry-clone']);
});

test('a lone resume frame requests one bounded retry when its paint callback disappears', async () => {
  const firstPaint = deferred();
  const retryPaint = deferred();
  const paints = [firstPaint, retryPaint];
  const closed = [];
  let state;
  state = harness({
    paintWaitTimeoutMillis: 400,
    waitForPaint: () => paints.shift().promise,
    onRecoveryRequest: (reason) => {
      assert.equal(reason, 'paint_wait_timeout');
      state.controller.offerFrame(fakeFrame('lone-frame-retry', closed), {
        epoch: 5, sequence: 40, presentationOrdinal: 40, visualAgeMillis: 20, offeredAt: 100
      });
    }
  });
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 40, presentationOrdinal: 40, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('lone-frame', closed), {
    epoch: 5, sequence: 40, presentationOrdinal: 40, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  assert.equal(state.controller.snapshot().pending, false, 'the incident starts without a second decoder frame');
  const firstTimeout = state.timers.findLast((timer) => !timer.cancelled && timer.millis === 400);
  assert.ok(firstTimeout);
  firstTimeout.callback();
  await tick();

  assert.equal(state.recoveryRequests.length, 1);
  assert.equal(state.recoveryRequests[0].reason, 'paint_wait_timeout');
  assert.equal(state.controller.snapshot().paintRecoveryRequested, true);
  assert.equal(state.renderers[0].renders.length, 2, 'the recovery request must fill the empty mailbox');
  assert.equal(state.controller.snapshot().inFlight, true);
  assert.deepEqual(closed, ['lone-frame-clone']);

  firstPaint.resolve();
  await tick();
  assert.equal(state.renderers[0].presents, 0, 'the lost callback remains inert after the bounded retry starts');
  state.renderers[0].renders[1].operation.resolve(state.renderers[0].renders[1].metadata);
  await tick();
  retryPaint.resolve();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.presented.sequence, 40);
  assert.equal(state.renderers[0].presents, 2);
  assert.equal(state.recoveryRequests.length, 1);
  assert.deepEqual(closed, ['lone-frame-clone', 'lone-frame-retry-clone']);
});

test('the lone-frame paint retry fails closed instead of forming a recovery loop', async () => {
  const paints = [deferred(), deferred()];
  const closed = [];
  let state;
  state = harness({
    paintWaitTimeoutMillis: 400,
    waitForPaint: () => paints.shift().promise,
    onRecoveryRequest: () => {
      state.controller.offerFrame(fakeFrame('bounded-retry', closed), {
        epoch: 5, sequence: 41, presentationOrdinal: 41, visualAgeMillis: 20, offeredAt: 100
      });
    }
  });
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 41, presentationOrdinal: 41, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('bounded-original', closed), {
    epoch: 5, sequence: 41, presentationOrdinal: 41, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  state.timers.findLast((timer) => !timer.cancelled && timer.millis === 400).callback();
  await tick();
  state.renderers[0].renders[1].operation.resolve(state.renderers[0].renders[1].metadata);
  await tick();
  const retryTimeout = state.timers.findLast((timer) => !timer.cancelled && timer.millis === 400);
  assert.ok(retryTimeout);
  retryTimeout.callback();
  await tick();
  assert.equal(state.recoveryRequests.length, 1);
  assert.equal(state.renderers[0].renders.length, 2, 'a failed retry must not create another frame');
  assert.equal(state.controller.snapshot().inFlight, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().failures, 1);
  assert.equal(state.statuses.at(-1).status, 'failed');
  assert.equal(state.statuses.at(-1).reason, 'paint_recovery_exhausted');
  assert.deepEqual(closed, ['bounded-original-clone', 'bounded-retry-clone']);
});

test('a rejected paint wait fails closed instead of leaving an active invisible renderer', async () => {
  const closed = [];
  const state = harness({
    autoRender: true,
    waitForPaint: () => Promise.reject(new Error('synthetic paint rejection'))
  });
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 42, presentationOrdinal: 42, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('paint-rejected', closed), {
    epoch: 5, sequence: 42, presentationOrdinal: 42, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();

  const snapshot = state.controller.snapshot();
  assert.equal(snapshot.active, false);
  assert.equal(snapshot.surfaceVisible, false);
  assert.equal(snapshot.failures, 1);
  assert.equal(state.recoveryRequests.length, 0);
  assert.equal(state.renderers[0].disposed, true);
  assert.equal(state.statuses.at(-1).status, 'failed');
  assert.equal(state.statuses.at(-1).reason, 'paint_wait_failed');
  assert.equal(state.metrics.filter(({ event }) => event === 'paint_wait_failed').length, 1);
  assert.deepEqual(closed, ['paint-rejected-clone']);
});

test('disposing a pending paint wait cancels its deadline and late signals stay inert', async () => {
  const paint = deferred();
  const paintWaitTimeoutMillis = 400;
  const state = harness({ paintWaitTimeoutMillis, waitForPaint: () => paint.promise });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 32, presentationOrdinal: 32, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('paint-disposed', closed), {
    epoch: 5, sequence: 32, presentationOrdinal: 32, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  const paintTimeout = state.timers.findLast((timer) =>
    !timer.cancelled && timer.millis === paintWaitTimeoutMillis);
  assert.ok(paintTimeout);

  state.controller.dispose('document_hidden');
  assert.equal(paintTimeout.cancelled, true);
  assert.equal(state.controller.snapshot().paintWaitTimeoutPending, false);
  assert.equal(state.controller.snapshot().paintPending, false);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.deepEqual(closed, ['paint-disposed-clone']);

  paintTimeout.callback();
  paint.resolve();
  await tick();
  assert.equal(state.metrics.some(({ event }) => event === 'paint_wait_timeout'), false);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.equal(state.renderers[0].presents, 0);
  assert.deepEqual(closed, ['paint-disposed-clone']);
});

test('a missing paint callback on an established HDR surface reveals authoritative SDR', async () => {
  const state = harness({ autoRender: true, paintWaitTimeoutMillis: 400 });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 33, presentationOrdinal: 33, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('paint-visible', closed), {
    epoch: 5, sequence: 33, presentationOrdinal: 33, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);

  const stalledPaint = deferred();
  state.controller.waitForPaint = () => stalledPaint.promise;
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 34, presentationOrdinal: 34, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('paint-visible-stalled', closed), {
    epoch: 5, sequence: 34, presentationOrdinal: 34, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  const paintTimeout = state.timers.findLast((timer) => !timer.cancelled && timer.millis === 400);
  assert.ok(paintTimeout);
  paintTimeout.callback();
  await tick();

  const snapshot = state.controller.snapshot();
  assert.equal(snapshot.active, true);
  assert.equal(snapshot.failures, 0);
  assert.equal(snapshot.surfaceVisible, false);
  assert.equal(snapshot.presentationState, 'fallback_latched');
  assert.equal(state.surfaces.at(-1).reason, 'paint_wait_timeout');
  assert.equal(state.renderers[0].discardedPreparedFrames, 1);
  assert.deepEqual(closed, ['paint-visible-clone', 'paint-visible-stalled-clone']);

  stalledPaint.resolve();
  await tick();
  assert.equal(state.renderers[0].presents, 2, 'late paint cannot restore a timed-out HDR frame');
});

test('a missing pre-copy paint callback preserves an established keyframe-wait holdover', async () => {
  const state = harness({ autoRender: true, paintWaitTimeoutMillis: 400 });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 6, sequence: 50, presentationOrdinal: 50, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('holdover-paint-visible', closed), {
    epoch: 6, sequence: 50, presentationOrdinal: 50, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();

  const renderer = state.renderers[0];
  const presentsBeforeCandidate = renderer.presents;
  const held = state.controller.snapshot();
  assert.equal(state.controller.holdLastPresentation('waiting_for_next_keyframe'), true);
  const stalledPaint = deferred();
  state.controller.waitForPaint = () => stalledPaint.promise;
  state.controller.noteSDRFrame({
    epoch: 6, sequence: 51, presentationOrdinal: 51, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('holdover-paint-stalled', closed), {
    epoch: 6, sequence: 51, presentationOrdinal: 51, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  const paintTimeout = state.timers.findLast((timer) => !timer.cancelled && timer.millis === 400);
  assert.ok(paintTimeout);
  paintTimeout.callback();
  await tick();

  const snapshot = state.controller.snapshot();
  assert.equal(snapshot.active, true);
  assert.equal(snapshot.surfaceVisible, true,
    'a candidate that never reached copy must not expose SDR beneath the holdover');
  assert.equal(snapshot.presentationState, 'holdover');
  assert.equal(snapshot.visualHoldover, true);
  assert.equal(snapshot.proofFresh, false);
  assert.equal(renderer.presents, presentsBeforeCandidate,
    'the timed-out candidate must not replace held pixels');
  assert.deepEqual({
    epoch: snapshot.epoch,
    sequence: snapshot.sequence,
    presentationOrdinal: snapshot.presentationOrdinal
  }, {
    epoch: held.epoch,
    sequence: held.sequence,
    presentationOrdinal: held.presentationOrdinal
  }, 'the timed-out candidate must not replace held proof metadata');
  assert.equal(renderer.discardedPreparedFrames, 1);

  stalledPaint.resolve();
  await tick();
  assert.equal(renderer.presents, presentsBeforeCandidate,
    'a late callback cannot revive a candidate discarded during holdover');
  assert.deepEqual(closed, ['holdover-paint-visible-clone', 'holdover-paint-stalled-clone']);
});

test('coordinated commit paints matching SDR before atomically presenting prepared HDR', async () => {
  const order = [];
  const activeConfigGeneration = 12;
  const coordinatedPaint = deferred();
  const state = harness({ autoRender: true, onPresent: () => order.push('hdr') });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 40, configGeneration: 11, presentationOrdinal: 40,
    visualAgeMillis: 940, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 5, sequence: 40, configGeneration: 11, presentationOrdinal: 40,
    visualAgeMillis: 940, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.canCoordinateSDRFrame(), true);
  const transitionsBefore = state.controller.snapshot().surfaceTransitions;
  state.controller.waitForPaint = () => coordinatedPaint.promise;
  state.controller.offerFrame(fakeFrame('coordinated', closed), {
    epoch: 5, sequence: 41, configGeneration: activeConfigGeneration,
    presentationOrdinal: 41, timestamp: 410,
    visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: (_frame, candidate) => {
      if (candidate.configGeneration !== activeConfigGeneration) return false;
      order.push('sdr');
      return {
        epoch: 5,
        sequence: 41,
        configGeneration: activeConfigGeneration,
        presentationOrdinal: 314,
        timestamp: 411,
        visualAgeMillis: 12,
        renderedAt: 150,
        offeredAt: 150,
        selectedDisplayBoost: 2,
        completionMillis: 999,
        displayReadyMillis: 999
      };
    }
  });
  await tick();
  assert.equal(state.controller.presented.sequence, 40);
  assert.equal(state.controller.currentSDR.sequence, 40);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  coordinatedPaint.resolve();
  await tick();
  assert.deepEqual(order.slice(-2), ['sdr', 'hdr']);
  assert.equal(state.controller.presented.sequence, 41);
  assert.equal(state.controller.presented.configGeneration, activeConfigGeneration);
  assert.equal(state.controller.presented.presentationOrdinal, 314);
  assert.equal(state.controller.presented.timestamp, 411);
  assert.equal(state.controller.presented.visualAgeMillis, 12);
  assert.equal(state.controller.presented.renderedAt, 150);
  assert.equal(state.controller.presented.offeredAt, 150);
  assert.equal(state.controller.presented.selectedDisplayBoost, 4,
    'the render result remains authoritative for the submitted boost');
  assert.equal(state.controller.presented.completionMillis, 8,
    'the render result remains authoritative for GPU timing');
  assert.equal(state.controller.presented.displayReadyMillis, 16,
    'commit metadata cannot overwrite renderer display timing');
  assert.equal(state.controller.currentSDR.sequence, 41);
  assert.equal(state.controller.currentSDR.configGeneration, activeConfigGeneration);
  assert.equal(state.controller.currentSDR.presentationOrdinal, 314);
  assert.equal(state.controller.currentSDR.renderedAt, 150);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitionsBefore);
  assert.equal(state.paintChecks.length, 0);
  assert.deepEqual(closed, ['initial-clone', 'coordinated-clone']);
});

test('a stale config generation rejects coordinated commit without touching either visible surface', async () => {
  const coordinatedPaint = deferred();
  const state = harness({ autoRender: true });
  const closed = [];
  const activeConfigGeneration = 15;
  let rejectedConfigGeneration = 0;
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 5, sequence: 50, configGeneration: activeConfigGeneration,
    presentationOrdinal: 50, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 5, sequence: 50, configGeneration: activeConfigGeneration,
    presentationOrdinal: 50, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  const presentsBefore = state.renderers[0].presents;
  const transitionsBefore = state.controller.snapshot().surfaceTransitions;
  state.controller.waitForPaint = () => coordinatedPaint.promise;
  state.controller.offerFrame(fakeFrame('superseded', closed), {
    epoch: 5, sequence: 51, configGeneration: activeConfigGeneration - 1,
    presentationOrdinal: 51, visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: (_frame, candidate) => {
      rejectedConfigGeneration = candidate.configGeneration;
      return candidate.configGeneration === activeConfigGeneration ? candidate : false;
    }
  });
  await tick();
  coordinatedPaint.resolve();
  await tick();
  assert.equal(state.renderers[0].discardedPreparedFrames, 1);
  assert.equal(state.renderers[0].presents, presentsBefore);
  assert.equal(state.controller.presented.sequence, 50);
  assert.equal(state.controller.presented.configGeneration, activeConfigGeneration);
  assert.equal(state.controller.currentSDR.sequence, 50);
  assert.equal(state.controller.currentSDR.configGeneration, activeConfigGeneration);
  assert.equal(rejectedConfigGeneration, activeConfigGeneration - 1);
  assert.equal(state.controller.snapshot().surfaceTransitions, transitionsBefore);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.deepEqual(closed, ['initial-clone', 'superseded-clone']);
});

test('a coordinated present failure leaves the new SDR authoritative and fails closed once', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 5, sequence: 60, presentationOrdinal: 60, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 5, sequence: 60, presentationOrdinal: 60, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  state.renderers[0].present = () => { throw new Error('synthetic present error'); };
  state.controller.offerFrame(fakeFrame('present-failure', closed), {
    epoch: 5, sequence: 61, presentationOrdinal: 61, visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: () => {
      return {
        epoch: 5, sequence: 61, presentationOrdinal: 61, visualAgeMillis: 20, renderedAt: 100
      };
    }
  });
  await tick();
  assert.equal(state.controller.currentSDR.sequence, 61);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().failures, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'fallback').length, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'session_summary').length, 1);
  assert.deepEqual(closed, ['initial-clone', 'present-failure-clone']);
});

test('a stalled GPU completion fails closed to the newest coalesced SDR frame without leaks', async () => {
  const state = harness();
  const closed = [];
  const commits = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 6, sequence: 70, presentationOrdinal: 70, visualAgeMillis: 20, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 6, sequence: 70, presentationOrdinal: 70, visualAgeMillis: 20, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(Object.assign({}, state.renderers[0].renders[0].metadata, {
    completionMillis: 8,
    displayReadyMillis: 16,
    decodedFrameToDisplayReadyMillis: 18,
    selectedDisplayBoost: 4
  }));
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);

  state.controller.offerFrame(fakeFrame('hung', closed), {
    epoch: 6, sequence: 71, presentationOrdinal: 71, visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: () => {
      commits.push('hung');
      return { epoch: 6, sequence: 71, presentationOrdinal: 71, visualAgeMillis: 20, renderedAt: 100 };
    }
  });
  const hungOperation = state.renderers[0].renders[2].operation;
  state.controller.offerFrame(fakeFrame('pending-old', closed), {
    epoch: 6, sequence: 72, presentationOrdinal: 72, visualAgeMillis: 20, offeredAt: 100
  }, {
    commitSDR: () => {
      commits.push('pending-old');
      return { epoch: 6, sequence: 72, presentationOrdinal: 72, visualAgeMillis: 20, renderedAt: 100 };
    }
  });
  state.controller.offerFrame(fakeFrame('pending-new', closed), {
    epoch: 6, sequence: 73, presentationOrdinal: 73, visualAgeMillis: 10, offeredAt: 100
  }, {
    commitSDR: () => {
      commits.push('pending-new');
      return { epoch: 6, sequence: 73, presentationOrdinal: 73, visualAgeMillis: 10, renderedAt: 100 };
    }
  });
  assert.deepEqual(closed, ['initial-clone', 'pending-old-clone']);
  assert.equal(state.controller.snapshot().ownedFrameCount, 2);
  assert.equal(state.controller.snapshot().gpuCompletionTimeoutPending, true);
  const watchdog = state.timers.findLast((timer) => !timer.cancelled &&
    timer.millis === CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  assert.ok(watchdog);
  watchdog.callback();

  assert.deepEqual(commits, ['pending-new'], 'the replaceable pending slot is the freshest fallback source');
  assert.equal(state.controller.currentSDR.sequence, 73);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.equal(state.controller.snapshot().gpuCompletionTimeoutPending, false);
  assert.equal(state.renderers[0].disposed, true);
  assert.equal(state.metrics.filter(({ event }) => event === 'gpu_completion_timeout').length, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'fallback').length, 1);
  assert.equal(state.metrics.filter(({ event }) => event === 'session_summary').length, 1);
  assert.deepEqual(closed, [
    'initial-clone',
    'pending-old-clone',
    'pending-new-clone',
    'hung-clone'
  ]);

  hungOperation.resolve({
    epoch: 6, sequence: 71, presentationOrdinal: 71, selectedDisplayBoost: 4
  });
  watchdog.callback();
  await tick();
  assert.deepEqual(commits, ['pending-new']);
  assert.equal(state.renderers[0].presents, 2, 'a late GPU signal cannot resurrect the HDR surface');
  assert.equal(closed.filter((label) => label === 'hung-clone').length, 1);
});

test('wake-late GPU completion cannot beat its suspended watchdog callback', async () => {
  const state = harness();
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({
    epoch: 13, sequence: 1, presentationOrdinal: 1, visualAgeMillis: 10, renderedAt: 100
  });
  state.controller.offerFrame(fakeFrame('wake-late-gpu', closed), {
    epoch: 13, sequence: 1, presentationOrdinal: 1, visualAgeMillis: 10, offeredAt: 100
  });
  const operation = state.renderers[0].renders[0].operation;
  state.setWallClock(1000 + CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  operation.resolve(Object.assign({}, state.renderers[0].renders[0].metadata, {
    selectedDisplayBoost: 4,
    gpuCompleted: true
  }));
  await tick();

  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.metrics.filter(({ event }) => event === 'gpu_completion_timeout').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'first_presented'), false);
  assert.deepEqual(closed, ['wake-late-gpu-clone']);
});

test('GPU timeout fallback tries the pending commit before the older in-flight commit', async () => {
  const state = harness();
  const closed = [];
  const commits = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.offerFrame(fakeFrame('older-in-flight', closed), {
    epoch: 7, sequence: 80, presentationOrdinal: 80
  }, {
    commitSDR: () => {
      commits.push('older-in-flight');
      return { epoch: 7, sequence: 80, presentationOrdinal: 80, renderedAt: 100 };
    }
  });
  state.controller.offerFrame(fakeFrame('newer-ineligible', closed), {
    epoch: 7, sequence: 81, presentationOrdinal: 81
  }, {
    commitSDR: () => {
      commits.push('newer-ineligible');
      return false;
    }
  });
  const watchdog = state.timers.findLast((timer) => !timer.cancelled &&
    timer.millis === CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  watchdog.callback();
  assert.deepEqual(commits, ['newer-ineligible', 'older-in-flight']);
  assert.equal(state.controller.currentSDR.sequence, 80);
  assert.equal(state.controller.snapshot().active, false);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.deepEqual(closed, ['newer-ineligible-clone', 'older-in-flight-clone']);
});

test('a completed GPU render cancels its controller watchdog and cannot time out later', async () => {
  const state = harness();
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 8, sequence: 90, presentationOrdinal: 90, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('completed', closed), {
    epoch: 8, sequence: 90, presentationOrdinal: 90, offeredAt: 100
  });
  const watchdog = state.timers.findLast((timer) => !timer.cancelled &&
    timer.millis === CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  state.renderers[0].renders[0].operation.resolve(Object.assign({}, state.renderers[0].renders[0].metadata, {
    selectedDisplayBoost: 4
  }));
  await tick();
  assert.equal(watchdog.cancelled, true);
  assert.equal(state.controller.snapshot().gpuCompletionTimeoutPending, false);
  assert.equal(state.controller.snapshot().active, true);
  watchdog.callback();
  assert.equal(state.controller.snapshot().active, true);
  assert.equal(state.metrics.some(({ event }) => event === 'gpu_completion_timeout'), false);
  assert.deepEqual(closed, ['completed-clone']);
  state.controller.dispose('test_complete');
});

test('a prepared boost mismatch is discarded while the newest pending level continues', async () => {
  const state = harness();
  const closed = [];
  const commits = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 9, sequence: 100, presentationOrdinal: 100, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), {
    epoch: 9, sequence: 100, presentationOrdinal: 100, offeredAt: 100
  });
  state.renderers[0].renders[0].operation.resolve(Object.assign({}, state.renderers[0].renders[0].metadata, {
    selectedDisplayBoost: 4
  }));
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);

  state.controller.offerFrame(fakeFrame('old-level', closed), {
    epoch: 9, sequence: 101, presentationOrdinal: 101, offeredAt: 100
  }, {
    commitSDR: () => {
      commits.push('old-level');
      return { epoch: 9, sequence: 101, presentationOrdinal: 101, renderedAt: 100 };
    }
  });
  assert.equal(state.controller.setDisplayBoost(3), true);
  assert.equal(state.controller.snapshot().surfaceVisible, false,
    'the in-flight old boost remained topmost after it was superseded');
  assert.equal(state.surfaces.at(-1).reason, 'boost_superseded');
  state.controller.offerFrame(fakeFrame('new-level', closed), {
    epoch: 9, sequence: 102, presentationOrdinal: 102, offeredAt: 100
  }, {
    commitSDR: () => {
      commits.push('new-level');
      return { epoch: 9, sequence: 102, presentationOrdinal: 102, renderedAt: 100 };
    }
  });
  state.renderers[0].renders[2].operation.resolve(Object.assign({}, state.renderers[0].renders[2].metadata, {
    selectedDisplayBoost: 4
  }));
  await tick();
  assert.deepEqual(commits, []);
  assert.equal(state.renderers[0].discardedPreparedFrames, 1);
  assert.equal(state.renderers[0].presents, 2);
  assert.equal(state.renderers[0].renders.length, 4, 'the pending frame starts after stale staging is discarded');

  state.renderers[0].renders[3].operation.resolve(Object.assign({}, state.renderers[0].renders[3].metadata, {
    selectedDisplayBoost: 3,
    intendedOutputPeak: 3
  }));
  await tick();
  assert.deepEqual(commits, ['new-level']);
  assert.equal(state.controller.currentSDR.sequence, 102);
  assert.equal(state.controller.presented.sequence, 102);
  assert.equal(state.controller.presented.selectedDisplayBoost, 3);
  assert.equal(state.renderers[0].presents, 3);
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.deepEqual(closed, ['initial-clone', 'old-level-clone', 'new-level-clone']);
  state.controller.dispose('test_complete');
});

test('live boost changes update one uniform and reveal SDR until the redraw', async () => {
  const state = harness({ autoRender: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 3, sequence: 7, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('rendering', closed), { epoch: 3, sequence: 7, visualAgeMillis: 20, offeredAt: 100 });
  await tick();
  const transitions = state.controller.snapshot().surfaceTransitions;
  assert.equal(state.controller.setDisplayBoost(3), true);
  assert.deepEqual(state.renderers[0].boosts, [4, 3]);
  assert.equal(state.controller.snapshot().selectedDisplayBoost, 3);
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().fallbackKind, 'refresh');
  assert.equal(state.controller.snapshot().surfaceTransitions, transitions + 1);
  assert.equal(state.renderers.length, 1);
  assert.equal(state.controller.setDisplayBoost(5), true, '5x is part of the public ladder');
  assert.equal(state.controller.setDisplayBoost(8), true, 'retired values normalize to the 4x default');
  assert.deepEqual(state.renderers[0].boosts, [4, 3, 5, 4]);
  assert.deepEqual(closed, ['rendering-clone']);
});

test('a failed live boost write immediately fails closed to SDR', async () => {
  const state = harness({ autoRender: true, boostWriteError: true });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 3, sequence: 7, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('rendering', closed), {
    epoch: 3, sequence: 7, presentationOrdinal: 7, visualAgeMillis: 20, offeredAt: 100
  });
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);

  assert.equal(state.controller.setDisplayBoost(3), false);
  const snapshot = state.controller.snapshot();
  assert.equal(snapshot.selectedDisplayBoost, 4);
  assert.equal(snapshot.surfaceVisible, false);
  assert.equal(snapshot.active, false);
  assert.equal(snapshot.ready, false);
  assert.equal(snapshot.failures, 1);
  assert.equal(state.renderers[0].disposed, true);
  assert.ok(state.metrics.some(({ event, detail }) =>
    event === 'boost_change_failed' && detail.requestedDisplayBoost === 3));
  assert.ok(state.statuses.some(({ status }) => status === 'failed'));
  assert.deepEqual(closed, ['rendering-clone']);
});

test('every live boost choice redraws the authoritative SDR canvas without recreating the renderer', async () => {
  const state = harness({ autoRender: true });
  const sourceClosed = [];
  const cloneClosed = [];
  class CanvasVideoFrame {
    constructor(source, init) {
      this.source = source;
      this.timestamp = init.timestamp;
    }
    clone() {
      return { close() { cloneClosed.push('clone'); } };
    }
    close() {
      sourceClosed.push(this.timestamp);
    }
  }
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  for (const [index, boost] of CLIENT_HDR_DISPLAY_BOOSTS.entries()) {
    state.controller.setDisplayBoost(boost);
    assert.equal(offerClientHDRCanvasFrame(state.controller, state.canvas, {
      epoch: 8,
      sequence: index + 1,
      presentationOrdinal: index + 1,
      timestamp: index + 100,
      visualAgeMillis: 20,
      renderedAt: 100,
      offeredAt: 100,
      offeredWallMillis: Date.now()
    }, { VideoFrame: CanvasVideoFrame }), true);
    await tick();
  }
  assert.equal(state.renderers.length, 1);
  assert.equal(state.renderers[0].renders.length, CLIENT_HDR_DISPLAY_BOOSTS.length + 1);
  assert.deepEqual(state.renderers[0].boosts, [4, 2, 3, 4, 5, 6]);
  assert.equal(state.renderers[0].renders[0].options.activationFrame, true);
  assert.equal(state.renderers[0].renders[0].options.requestPatch, true,
    'the private 1x activation must request EDR for the first selected target');
  assert.equal(state.renderers[0].renders[1].options.activationFrame, false);
  assert.equal(state.renderers[0].renders[1].options.requestPatch, false);
  assert.ok(state.renderers[0].renders.slice(2).every((render) => render.options.activationFrame === false),
    '1x remains an internal activation pass rather than a public target');
  assert.ok(state.renderers[0].renders.slice(2).every((render) => render.options.requestPatch === false),
    'the request patch must disappear from subsequent full targets');
  assert.equal(state.controller.snapshot().requestPatchPresented, true);
  assert.equal(sourceClosed.length, CLIENT_HDR_DISPLAY_BOOSTS.length);
  assert.equal(cloneClosed.length, CLIENT_HDR_DISPLAY_BOOSTS.length);
  state.controller.dispose('test_complete');
});

test('a rejected canvas retry records the authoritative SDR watermark exactly once', () => {
  let notes = 0;
  let sourceCloses = 0;
  class CanvasVideoFrame {
    close() { sourceCloses += 1; }
  }
  const controller = {
    offerFrame() { return false; },
    noteSDRFrame() { notes += 1; }
  };
  assert.equal(offerClientHDRCanvasFrame(controller, {}, {
    epoch: 8, sequence: 1, presentationOrdinal: 1, timestamp: 100
  }, { VideoFrame: CanvasVideoFrame }), false);
  assert.equal(notes, 1);
  assert.equal(sourceCloses, 1);

  class ThrowingCanvasVideoFrame {
    constructor() { throw new Error('synthetic canvas frame failure'); }
  }
  assert.equal(offerClientHDRCanvasFrame(controller, {}, {
    epoch: 8, sequence: 2, presentationOrdinal: 2, timestamp: 200
  }, { VideoFrame: ThrowingCanvasVideoFrame }), false);
  assert.equal(notes, 2, 'constructor failure must not lose or duplicate the SDR watermark');
  assert.equal(sourceCloses, 1);
});

test('an accepted coordinated canvas retry defers its SDR watermark until the atomic commit', () => {
  let notes = 0;
  let sourceCloses = 0;
  let receivedOptions = null;
  class CanvasVideoFrame {
    close() { sourceCloses += 1; }
  }
  const controller = {
    offerFrame(_frame, _metadata, options) {
      receivedOptions = options;
      return true;
    },
    noteSDRFrame() { notes += 1; }
  };
  const options = { commitSDR() { return true; } };
  assert.equal(offerClientHDRCanvasFrame(controller, {}, {
    epoch: 8, sequence: 3, presentationOrdinal: 3, timestamp: 300
  }, { VideoFrame: CanvasVideoFrame }, options), true);
  assert.equal(receivedOptions, options);
  assert.equal(notes, 0);
  assert.equal(sourceCloses, 1);
});

test('a soft freshness fallback recovers with one exact copied target and two compositor opportunities', async () => {
  const state = harness();
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), { epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, offeredAt: 100 });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);

  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 20, renderedAt: 100 });
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().presentationState, 'fallback_latched');
  assert.equal(state.controller.snapshot().fallbackKind, 'soft');

  state.controller.offerFrame(fakeFrame('recovery', closed), { epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 20, offeredAt: 100 });
  state.renderers[0].renders[2].operation.resolve(state.renderers[0].renders[2].metadata);
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().presentationState, 'visible');
  assert.equal(state.controller.snapshot().recoveryPaintCheckPending, false);
  assert.equal(state.renderers[0].postPresentPaints, 3,
    'the recovery target must use the same two-opportunity visible handoff');
  assert.deepEqual(closed, ['initial-clone', 'recovery-clone']);
});

test('disposing during visible soft-recovery settling cannot resurrect HDR or leak a frame', async () => {
  const recoveryPaint = deferred();
  const state = harness({ postPresentPaintGates: [Promise.resolve(), Promise.resolve(), recoveryPaint.promise] });
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('initial', closed), { epoch: 4, sequence: 20, presentationOrdinal: 20, visualAgeMillis: 20, offeredAt: 100 });
  state.renderers[0].renders[0].operation.resolve(state.renderers[0].renders[0].metadata);
  await tick();
  state.controller.noteSDRFrame({ epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 20, renderedAt: 100 });
  state.controller.offerFrame(fakeFrame('recovery', closed), { epoch: 4, sequence: 22, presentationOrdinal: 22, visualAgeMillis: 20, offeredAt: 100 });
  state.renderers[0].renders[2].operation.resolve(state.renderers[0].renders[2].metadata);
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, true);
  assert.equal(state.controller.snapshot().presentationState, 'settling');
  assert.equal(state.controller.snapshot().recoveryPaintCheckPending, false);
  state.controller.dispose('hidden');
  recoveryPaint.resolve();
  await tick();
  assert.equal(state.controller.snapshot().surfaceVisible, false);
  assert.equal(state.controller.snapshot().rendererActive, false);
  assert.equal(state.metrics.filter(({ event }) => event === 'first_presented').length, 1,
    'the disposed recovery target cannot create another presented success');
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  assert.deepEqual(closed, ['initial-clone', 'recovery-clone']);
});

test('twenty enable-disable cycles dispose one renderer and close every pending frame per cycle', async () => {
  const state = harness({ deferredInitialize: true });
  const closed = [];
  for (let index = 0; index < 20; index += 1) {
    assert.equal(state.controller.start({ canvas: state.canvas, width: 720, height: 1482 }), true);
    const renderer = state.renderers.at(-1);
    state.controller.offerFrame(fakeFrame(`cycle-${index}`, closed), { epoch: index + 1, sequence: 1 });
    state.controller.dispose('cycle_complete');
    renderer.initializeDeferred.resolve({ canvasEncoding: 'srgb-linear' });
    await tick();
    assert.equal(renderer.disposed, true);
  }
  assert.equal(state.renderers.length, 20);
  assert.equal(closed.length, 20);
  assert.equal(state.controller.snapshot().rendererActive, false);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
});

test('disposing an in-flight frame releases it immediately and does not double-close after settlement', async () => {
  const state = harness();
  const closed = [];
  state.controller.start({ canvas: state.canvas, width: 720, height: 1482 });
  await tick();
  state.controller.offerFrame(fakeFrame('in-flight', closed), { epoch: 1, sequence: 1 });
  const operation = state.renderers[0].renders[0].operation;
  state.controller.dispose('hidden');
  assert.equal(state.renderers[0].disposed, true);
  assert.deepEqual(closed, ['in-flight-clone']);
  assert.equal(state.controller.snapshot().ownedFrameCount, 0);
  operation.reject(new Error('renderer_disposed'));
  await tick();
  assert.deepEqual(closed, ['in-flight-clone']);
});

test('prepared GPU ownership transfers into the first renderer session and is disposed on background', async () => {
  const created = [], surfaces = [];
  const controller = new ClientHDRController({
    rendererFactory: () => {
      const renderer = {
        prepareCalls: 0, initializeCalls: 0, disposed: false,
        prepare() { this.prepareCalls++; return Promise.resolve(); },
        initialize() { this.initializeCalls++; return Promise.resolve({}); },
        dispose() { this.disposed = true; }
      };
      created.push(renderer);
      return renderer;
    },
    onSurface: visible => surfaces.push(visible)
  });
  assert.equal(controller.prepare(), true);
  assert.equal(controller.prepare(), true);
  await tick();
  assert.equal(created.length, 1);
  assert.equal(created[0].prepareCalls, 1);
  assert.equal(controller.snapshot().active, false);
  assert.equal(surfaces.includes(true), false);
  assert.equal(controller.start({canvas: {getContext() {}}, width: 540, height: 1112}), true);
  await tick();
  assert.equal(created.length, 1);
  assert.equal(created[0].initializeCalls, 1);
  assert.equal(created[0].disposed, false);
  assert.equal(controller.snapshot().ready, true);
  assert.equal(surfaces.includes(true), false, 'prepared resources have no presentation authority');
  controller.dispose('hidden');
  assert.equal(created[0].disposed, true);
  controller.prepare();
  const abandoned = created[1];
  controller.dispose('hidden_before_picture');
  await tick();
  assert.equal(abandoned.disposed, true);
  assert.equal(controller.preparingRenderer, null);
});

test('stalled GPU preparation is bounded and late completion cannot revive its ownership', async () => {
  const gate = deferred(), timers = [];
  let disposed = 0;
  const controller = new ClientHDRController({
    rendererFactory: () => ({prepare: () => gate.promise, dispose: () => {disposed++;}}),
    setTimer: (callback, millis) => {const timer = {callback, millis}; timers.push(timer); return timer;},
    clearTimer: timer => {timer.cleared = true;}
  });
  controller.prepare();
  assert.equal(timers[0].millis, CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS);
  timers[0].callback();
  assert.equal(disposed, 1);
  assert.equal(controller.preparingRenderer, null);
  gate.resolve();
  await tick();
  assert.equal(controller.preparingRenderer, null);
  assert.equal(controller.snapshot().active, false);
});
