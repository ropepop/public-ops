import {
  CLIENT_HDR_ALLOWED_BOOSTS,
  CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS,
  CLIENT_HDR_REQUEST_PATCH_EDGE,
  CLIENT_HDR_REQUEST_PATCH_PEAK,
  ClientHDRRenderer
} from './client-hdr-renderer.mjs';

export const LEGACY_CLIENT_HDR_ENGINE = 'client_webgpu_v1';
export const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
// Source-compatibility alias while generated clients age out. It no longer
// names a server runtime; every legacy selection resolves to browser v2.
export const CLIENT_HDR_PIPELINE = 'webgpu-mainthread-edr-v2';
export const CLIENT_HDR_PRESENTATION_KIND = 'sdr_to_edr';
export const CLIENT_HDR_TARGET_DISPLAY_BOOST = 4;
export const CLIENT_HDR_PAINT_WAIT_TIMEOUT_MILLIS = 2000;
export const CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS = 8000;
export const CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS = 2000;
export const CLIENT_HDR_DISPLAY_BOOSTS = CLIENT_HDR_ALLOWED_BOOSTS;
const CLIENT_HDR_HOLDOVER_SETTLEMENT_CANCEL_REASON = 'hdr_holdover_settlement_superseded';

export function normalizeClientHDRDisplayBoost(value) {
  const boost = Number(value);
  return CLIENT_HDR_DISPLAY_BOOSTS.includes(boost) ? boost : CLIENT_HDR_TARGET_DISPLAY_BOOST;
}

export function normalizeHDREngine(value) {
  return CLIENT_HDR_ENGINE;
}

export function resolveCapabilityHDREngine(allowedEngines, selectedEngine) {
  const clientAllowed = Array.isArray(allowedEngines) && allowedEngines.includes(CLIENT_HDR_ENGINE);
  return clientAllowed ? CLIENT_HDR_ENGINE : '';
}

export function clientHDREngineProjectionDecision(projection, previouslyAvailable = false) {
  const ownerProjectionAvailable = Boolean(projection && projection.ownerProjectionAvailable);
  return {
    ownerProjectionAvailable,
    roleLost: Boolean(previouslyAvailable && !ownerProjectionAvailable),
    engine: CLIENT_HDR_ENGINE
  };
}

export function clientHDRCapability(environment = globalThis) {
  const navigatorValue = environment && environment.navigator;
  const canvasPrototype = environment && environment.HTMLCanvasElement && environment.HTMLCanvasElement.prototype;
  const highDynamicRange = Boolean(environment && typeof environment.matchMedia === 'function' &&
    environment.matchMedia('(dynamic-range: high)').matches);
  const dynamicRangeLimit = Boolean(environment && environment.CSS && typeof environment.CSS.supports === 'function' &&
    environment.CSS.supports('dynamic-range-limit', 'no-limit'));
  const mainThreadCanvas = Boolean(canvasPrototype && typeof canvasPrototype.getContext === 'function');
  return {
    supported: Boolean(
      environment &&
      typeof environment.VideoFrame === 'function' &&
      mainThreadCanvas &&
      navigatorValue && navigatorValue.gpu &&
      highDynamicRange && dynamicRangeLimit
    ),
    videoFrame: Boolean(environment && typeof environment.VideoFrame === 'function'),
    mainThreadCanvas,
    webgpu: Boolean(navigatorValue && navigatorValue.gpu),
    highDynamicRange,
    dynamicRangeLimit
  };
}

function finiteNumber(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function closeFrame(frame) {
  if (!frame || typeof frame.close !== 'function') return;
  try { frame.close(); } catch (_) {}
}

function releaseCandidate(candidate) {
  if (!candidate || candidate.released) return;
  candidate.released = true;
  closeFrame(candidate.frame);
}

export function offerClientHDRCanvasFrame(controller, canvas, metadata = {}, environment = globalThis, options = {}) {
  const VideoFrameConstructor = environment && environment.VideoFrame;
  if (!controller || !canvas || typeof VideoFrameConstructor !== 'function') return false;
  const timestamp = Math.max(0, Math.round(finiteNumber(metadata.timestamp)));
  let frame = null;
  let offered = false;
  try {
    frame = new VideoFrameConstructor(canvas, { timestamp });
    offered = controller.offerFrame(frame, metadata, options);
    return offered;
  } catch (_) {
    return false;
  } finally {
    closeFrame(frame);
    if (!offered || typeof options.commitSDR !== 'function') {
      try { controller.noteSDRFrame(metadata); } catch (_) {}
    }
  }
}

export function clientHDRFreshness(presented, current, now, limits = {}) {
  if (!presented || !current) return { fresh: false, reason: 'missing_watermark' };
  const presentedEpoch = finiteNumber(presented.epoch);
  const currentEpoch = finiteNumber(current.epoch);
  if (!(presentedEpoch > 0) || presentedEpoch !== currentEpoch) {
    return { fresh: false, reason: 'epoch_mismatch' };
  }
  const presentedConfigGeneration = finiteNumber(presented.configGeneration);
  const currentConfigGeneration = finiteNumber(current.configGeneration);
  if (presentedConfigGeneration !== currentConfigGeneration) {
    return {
      fresh: false,
      reason: 'config_generation_mismatch',
      presentedConfigGeneration,
      currentConfigGeneration
    };
  }
  const presentedSequence = finiteNumber(presented.sequence);
  const currentSequence = finiteNumber(current.sequence);
  const sourceSequenceLag = currentSequence - presentedSequence;
  const presentedOrdinal = finiteNumber(presented.presentationOrdinal);
  const currentOrdinal = finiteNumber(current.presentationOrdinal);
  const sequenceLag = presentedOrdinal > 0 && currentOrdinal > 0
    ? currentOrdinal - presentedOrdinal
    : sourceSequenceLag;
  const maxSequenceLag = finiteNumber(limits.maxSequenceLag, 1);
  if (sequenceLag < 0 || sequenceLag > maxSequenceLag) {
    return { fresh: false, reason: 'sequence_lag', sequenceLag, sourceSequenceLag };
  }
  const clockNow = finiteNumber(now);
  const presentedAge = Math.max(0,
    finiteNumber(presented.visualAgeMillis) + Math.max(0, clockNow - finiteNumber(presented.offeredAt, clockNow))
  );
  const currentAge = Math.max(0,
    finiteNumber(current.visualAgeMillis) + Math.max(0, clockNow - finiteNumber(current.renderedAt, clockNow))
  );
  const ageDeltaMillis = presentedAge - currentAge;
  const maxAgeDeltaMillis = finiteNumber(limits.maxAgeDeltaMillis, 250);
  if (ageDeltaMillis > maxAgeDeltaMillis) {
    return { fresh: false, reason: 'visual_age', sequenceLag, sourceSequenceLag, ageDeltaMillis };
  }
  return { fresh: true, reason: 'fresh', sequenceLag, sourceSequenceLag, ageDeltaMillis };
}

export class ClientHDRController {
  constructor(options = {}) {
    this.rendererFactory = options.rendererFactory || ((rendererOptions) => new ClientHDRRenderer(rendererOptions));
    this.rendererEnvironment = options.rendererEnvironment || globalThis;
    this.now = options.now || (() => performance.now());
    this.wallNow = options.wallNow || (() => Date.now());
    this.setTimer = options.setTimer || ((callback, millis) => setTimeout(callback, millis));
    this.clearTimer = options.clearTimer || ((timer) => clearTimeout(timer));
    this.waitForPaint = options.waitForPaint || (() => new Promise((resolve) => {
      const requestPaint = this.rendererEnvironment && this.rendererEnvironment.requestAnimationFrame;
      if (typeof requestPaint === 'function') requestPaint(() => resolve());
      else this.setTimer(resolve, 0);
    }));
    this.schedulePaintCheck = options.schedulePaintCheck || ((callback) => {
      const requestPaint = this.rendererEnvironment && this.rendererEnvironment.requestAnimationFrame;
      if (typeof requestPaint === 'function') {
        return { kind: 'animation_frame', handle: requestPaint(callback) };
      }
      return { kind: 'timer', handle: this.setTimer(callback, 0) };
    });
    this.cancelPaintCheck = options.cancelPaintCheck || ((scheduled) => {
      if (!scheduled) return;
      if (scheduled.kind === 'animation_frame') {
        const cancelPaint = this.rendererEnvironment && this.rendererEnvironment.cancelAnimationFrame;
        if (typeof cancelPaint === 'function') cancelPaint(scheduled.handle);
        return;
      }
      this.clearTimer(scheduled.handle);
    });
    this.onSurface = options.onSurface || (() => {});
    this.canRevealSurface = options.canRevealSurface || (() => true);
    this.onStatus = options.onStatus || (() => {});
    this.onMetric = options.onMetric || (() => {});
    this.onRecoveryRequest = options.onRecoveryRequest || (() => {});
    this.canReleaseHoldover = options.canReleaseHoldover || (() => true);
    this.maxSequenceLag = finiteNumber(options.maxSequenceLag, 1);
    this.maxAgeDeltaMillis = finiteNumber(options.maxAgeDeltaMillis, 250);
    this.gpuCompletionTimeoutMillis = Math.max(1, Math.round(finiteNumber(
      options.gpuCompletionTimeoutMillis,
      CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS
    )));
    this.paintWaitTimeoutMillis = Math.max(1, Math.round(finiteNumber(
      options.paintWaitTimeoutMillis,
      CLIENT_HDR_PAINT_WAIT_TIMEOUT_MILLIS
    )));
    this.rendererInitTimeoutMillis = Math.max(1, Math.round(finiteNumber(
      options.rendererInitTimeoutMillis,
      CLIENT_HDR_RENDERER_INIT_TIMEOUT_MILLIS
    )));
    this.settlementTimeoutMillis = Math.max(1, Math.round(finiteNumber(
      options.settlementTimeoutMillis,
      CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS
    )));
    this.generation = 0;
    this.renderer = null;
    this.rendererInitTimeout = null;
    this.active = false;
    this.ready = false;
    this.documentVisible = true;
    this.inFlight = null;
    this.inFlightTimeout = null;
    this.paintWait = null;
    this.pending = null;
    this.currentSDR = null;
    this.presented = null;
    this.pendingPresentation = null;
    this.freshness = null;
    this.surfaceVisible = false;
    this.enabledAt = 0;
    this.firstPresented = false;
    this.requestPatchPresented = false;
    this.paintRecoveryRequested = false;
    this.canvasEncoding = '';
    this.rendererMetrics = { gpuCompleted: false, compositorOpportunitiesCompleted: false };
    this.selectedDisplayBoost = CLIENT_HDR_TARGET_DISPLAY_BOOST;
    this.presentationState = 'standby';
    this.recoveryFreshStreak = 0;
    this.presentationRevision = 0;
    this.surfaceTransitions = 0;
    this.fallbackStartedAt = 0;
    this.fallbackKind = '';
    this.visualHoldover = false;
    this.visualHoldoverReason = '';
    this.streamRegionVisible = true;
    this.recoveryPaintCheck = null;
    this.settlementWatchdog = null;
    this.settlementStartedWallAt = 0;
    this.counters = this.newCounters();
  }

  newCounters() {
    return { offered: 0, rendered: 0, coalesced: 0, dropped: 0, failures: 0 };
  }

  start({ canvas, width, height, boost = CLIENT_HDR_TARGET_DISPLAY_BOOST }) {
    if (!canvas || typeof canvas.getContext !== 'function') {
      this.fail('main_thread_canvas_unavailable');
      return false;
    }
    this.dispose('restart');
    const generation = ++this.generation;
    let renderer;
    try {
      renderer = this.rendererFactory({
        environment: this.rendererEnvironment,
        now: this.now,
        wallNow: this.wallNow,
        setTimer: this.setTimer,
        clearTimer: this.clearTimer,
        gpuCompletionTimeoutMillis: this.gpuCompletionTimeoutMillis,
        onFailure: (reason) => {
          if (renderer === this.renderer && generation === this.generation) this.fail(reason);
        },
        onMetric: (event, detail) => {
          if (renderer !== this.renderer || generation !== this.generation) return;
          this.captureRendererMetric(detail);
          this.onMetric(event, Object.assign(this.snapshot(), detail || {}));
        }
      });
    } catch (_) {
      this.fail('renderer_start_failed');
      return false;
    }
    this.renderer = renderer;
    this.active = true;
    this.ready = false;
    this.currentSDR = null;
    this.presented = null;
    this.pendingPresentation = null;
    this.freshness = null;
    this.surfaceVisible = false;
    this.enabledAt = this.now();
    this.firstPresented = false;
    this.requestPatchPresented = false;
    this.paintRecoveryRequested = false;
    this.canvasEncoding = '';
    this.rendererMetrics = { gpuCompleted: false, compositorOpportunitiesCompleted: false };
    this.selectedDisplayBoost = normalizeClientHDRDisplayBoost(boost);
    this.presentationState = 'acquiring';
    this.recoveryFreshStreak = 0;
    this.presentationRevision += 1;
    this.surfaceTransitions = 0;
    this.fallbackStartedAt = 0;
    this.fallbackKind = '';
    this.visualHoldover = false;
    this.visualHoldoverReason = '';
    this.cancelSettlementWatchdog();
    this.counters = this.newCounters();
    this.onSurface(false, null, 'starting');
    this.onStatus('starting');
    if (!this.armRendererInitTimeout(renderer, generation)) return false;
    Promise.resolve(renderer.initialize({
      canvas,
      width: Math.max(1, Math.round(finiteNumber(width, canvas.width || 1))),
      height: Math.max(1, Math.round(finiteNumber(height, canvas.height || 1))),
      boost: this.selectedDisplayBoost
    })).then((result) => {
      const initWatchdog = this.rendererInitTimeout;
      if (initWatchdog && initWatchdog.renderer === renderer &&
        initWatchdog.generation === generation &&
        this.wallNow() - initWatchdog.startedWallAt >= this.rendererInitTimeoutMillis) {
        this.cancelRendererInitTimeout(renderer);
        if (renderer === this.renderer && generation === this.generation && this.active && !this.ready) {
          this.onMetric('renderer_init_timeout', Object.assign(this.snapshot(), {
            rendererInitTimeoutMillis: this.rendererInitTimeoutMillis,
            rendererInitElapsedMillis: Math.max(0, this.wallNow() - initWatchdog.startedWallAt),
            rendererInitCheckSource: 'completion'
          }));
          this.fail('renderer_init_timeout');
        }
        return;
      }
      this.cancelRendererInitTimeout(renderer);
      if (renderer !== this.renderer || generation !== this.generation || !this.active) {
        try { renderer.dispose(); } catch (_) {}
        return;
      }
      this.ready = true;
      this.canvasEncoding = String(result && result.canvasEncoding || '');
      this.captureRendererMetric(result);
      if (typeof renderer.setBoost === 'function') renderer.setBoost(this.selectedDisplayBoost);
      this.onStatus('ready');
      this.onMetric('renderer_ready', this.snapshot());
      this.dispatchPending();
    }).catch((error) => {
      this.cancelRendererInitTimeout(renderer);
      if (renderer === this.renderer && generation === this.generation) {
        this.fail(String(error && error.message || 'renderer_init_failed').slice(0, 80));
      }
    });
    return true;
  }

  captureRendererMetric(detail) {
    if (!detail || typeof detail !== 'object') return;
    if (detail.canvasEncoding) this.canvasEncoding = String(detail.canvasEncoding);
    if (detail.sourceColorSpace) {
      this.rendererMetrics.sourceColorSpace = String(detail.sourceColorSpace).slice(0, 120);
    }
    for (const key of ['gpuCompleted', 'compositorOpportunitiesCompleted']) {
      if (detail[key] === true) this.rendererMetrics[key] = true;
    }
    if (typeof detail.edrRequestPatchIntended === 'boolean') {
      this.rendererMetrics.edrRequestPatchIntended = detail.edrRequestPatchIntended;
    }
    if (typeof detail.continuousSurface === 'boolean') {
      this.rendererMetrics.continuousSurface = detail.continuousSurface;
    }
    for (const key of [
      'intendedOutputPeak', 'colorExpansionExponent', 'intendedRequestPatchPeak', 'intendedRequestPatchEdge',
      'submitMillis', 'completionMillis', 'displayReadyMillis', 'postPresentOpportunityCount'
    ]) {
      if (Number.isFinite(Number(detail[key]))) this.rendererMetrics[key] = Number(detail[key]);
    }
    for (const key of [
      'configurationColorSpace', 'toneMappingMode', 'configurationDynamicRangeLimit',
      'mappingModel', 'postPresentSource'
    ]) {
      if (detail[key] !== undefined && detail[key] !== null) this.rendererMetrics[key] = String(detail[key]).slice(0, 40);
    }
  }

  cancelRendererInitTimeout(renderer = null) {
    const watchdog = this.rendererInitTimeout;
    if (!watchdog || (renderer && watchdog.renderer !== renderer)) return false;
    this.rendererInitTimeout = null;
    if (watchdog.handle !== null) {
      try { this.clearTimer(watchdog.handle); } catch (_) {}
      watchdog.handle = null;
    }
    return true;
  }

  armRendererInitTimeout(renderer, generation) {
    this.cancelRendererInitTimeout();
    if (renderer !== this.renderer || generation !== this.generation || !this.active) return false;
    const watchdog = { renderer, generation, startedWallAt: this.wallNow(), handle: null };
    this.rendererInitTimeout = watchdog;
    try {
      const handle = this.setTimer(() => {
        if (this.rendererInitTimeout !== watchdog) return;
        this.rendererInitTimeout = null;
        watchdog.handle = null;
        if (renderer !== this.renderer || generation !== this.generation || !this.active || this.ready) return;
        this.onMetric('renderer_init_timeout', Object.assign(this.snapshot(), {
          rendererInitTimeoutMillis: this.rendererInitTimeoutMillis
        }));
        this.fail('renderer_init_timeout');
      }, this.rendererInitTimeoutMillis);
      watchdog.handle = handle;
      if (this.rendererInitTimeout !== watchdog) {
        try { this.clearTimer(handle); } catch (_) {}
        watchdog.handle = null;
      }
      return true;
    } catch (_) {
      if (this.rendererInitTimeout === watchdog) this.rendererInitTimeout = null;
      this.fail('renderer_init_watchdog_failed');
      return false;
    }
  }

  setDisplayBoost(value) {
    const boost = normalizeClientHDRDisplayBoost(value);
    if (boost === this.selectedDisplayBoost) return true;
    const previous = this.selectedDisplayBoost;
    if (this.surfaceVisible) this.latchFallback('boost_superseded');
    try {
      if (this.renderer && this.ready && typeof this.renderer.setBoost === 'function') {
        this.renderer.setBoost(boost);
      }
    } catch (error) {
      const reason = String(error && error.message || 'boost_change_failed').slice(0, 80);
      this.onMetric('boost_change_failed', Object.assign(this.snapshot(), {
        requestedDisplayBoost: boost,
        reason
      }));
      this.fail(reason);
      return false;
    }
    this.selectedDisplayBoost = boost;
    this.onMetric('boost_changed', Object.assign(this.snapshot(), {
      previousDisplayBoost: previous
    }));
    return true;
  }

  setDocumentVisible(visible) {
    this.documentVisible = Boolean(visible);
    if (!this.documentVisible) {
      this.latchFallback('document_hidden');
    }
  }

  setStreamRegionVisible(visible) {
    const next = Boolean(visible);
    if (this.streamRegionVisible === next) return false;
    this.streamRegionVisible = next;
    this.onMetric('stream_region_visibility', Object.assign(this.snapshot(), {
      streamRegionVisible: next
    }));
    return true;
  }

  noteSDRFrame(metadata = {}) {
    if (this.checkSettlementDeadline('sdr_frame')) return false;
    this.currentSDR = this.sdrMetadata(metadata);
    this.updateSurface({ sourceAdvanced: true });
    return true;
  }

  cancelSettlementWatchdog() {
    const watchdog = this.settlementWatchdog;
    this.settlementWatchdog = null;
    this.settlementStartedWallAt = 0;
    if (!watchdog || watchdog.handle === null) return Boolean(watchdog);
    try { this.clearTimer(watchdog.handle); } catch (_) {}
    watchdog.handle = null;
    return true;
  }

  armSettlementWatchdog(renderer, generation, revision, presentation) {
    this.cancelSettlementWatchdog();
    if (renderer !== this.renderer || generation !== this.generation || !this.active ||
      revision !== this.presentationRevision || this.presentationState !== 'settling') return false;
    const startedWallAt = this.wallNow();
    const watchdog = { renderer, generation, revision, presentation, startedWallAt, handle: null };
    this.settlementWatchdog = watchdog;
    this.settlementStartedWallAt = startedWallAt;
    this.onMetric('settlement_started', Object.assign(this.snapshot(), {
      settlementTimeoutMillis: this.settlementTimeoutMillis,
      epoch: finiteNumber(presentation && presentation.epoch),
      sequence: finiteNumber(presentation && presentation.sequence)
    }));
    try {
      const handle = this.setTimer(() => {
        if (this.settlementWatchdog !== watchdog) return;
        this.expireSettlementWatchdog('timer');
      }, this.settlementTimeoutMillis);
      watchdog.handle = handle;
      if (this.settlementWatchdog !== watchdog) {
        try { this.clearTimer(handle); } catch (_) {}
        watchdog.handle = null;
      }
      return true;
    } catch (_) {
      this.cancelSettlementWatchdog();
      this.fail('settlement_watchdog_failed');
      return false;
    }
  }

  expireSettlementWatchdog(source = 'external') {
    const watchdog = this.settlementWatchdog;
    if (!watchdog || watchdog.renderer !== this.renderer || watchdog.generation !== this.generation ||
      watchdog.revision !== this.presentationRevision || !this.active) {
      return false;
    }
    const elapsedMillis = Math.max(0, this.wallNow() - watchdog.startedWallAt);
    this.onMetric('settlement_deadline_exceeded', Object.assign(this.snapshot(), {
      settlementTimeoutMillis: this.settlementTimeoutMillis,
      settlementElapsedMillis: elapsedMillis,
      settlementCheckSource: String(source || 'external').slice(0, 40),
      epoch: finiteNumber(watchdog.presentation && watchdog.presentation.epoch),
      sequence: finiteNumber(watchdog.presentation && watchdog.presentation.sequence)
    }));
    this.cancelSettlementWatchdog();
    this.fail('settlement_deadline_exceeded');
    return true;
  }

  checkSettlementDeadline(source = 'external') {
    const watchdog = this.settlementWatchdog;
    // A newer SDR watermark can latch the visible surface back to SDR while
    // the compositor promise for the copied target is still unresolved. The
    // owned frame and renderer remain pending in that state, so the outer wall
    // deadline must stay enforceable until success or disposal cancels it.
    if (!watchdog) return false;
    if (this.wallNow() - watchdog.startedWallAt < this.settlementTimeoutMillis) return false;
    return this.expireSettlementWatchdog(source);
  }

  sdrMetadata(metadata = {}) {
    return {
      epoch: finiteNumber(metadata.epoch),
      sequence: finiteNumber(metadata.sequence),
      configGeneration: finiteNumber(metadata.configGeneration),
      presentationOrdinal: finiteNumber(metadata.presentationOrdinal),
      visualAgeMillis: Math.max(0, finiteNumber(metadata.visualAgeMillis)),
      renderedAt: finiteNumber(metadata.renderedAt, this.now())
    };
  }

  markSDRStale(reason = 'sdr_stale') {
    this.visualHoldover = false;
    this.visualHoldoverReason = '';
    this.currentSDR = null;
    this.freshness = { fresh: false, reason: String(reason || 'sdr_stale') };
    this.latchFallback(this.freshness.reason);
  }

  holdLastPresentation(reason = 'stream_recovering') {
    const holdoverReason = String(reason || 'stream_recovering').slice(0, 80);
    if (!this.active || !this.renderer || !this.documentVisible || !this.firstPresented ||
      !this.surfaceVisible || !this.presented || !this.surfaceRevealAllowed()) return false;
    const changed = !this.visualHoldover || this.visualHoldoverReason !== holdoverReason;
    this.cancelRecoveryPaintCheck();
    // A transport interruption can arrive after the next frame has already
    // copied but before its compositor confirmation resolves.  That copied
    // frame is now the visual holdover; an older settlement deadline must not
    // tear down the reusable bright surface while recovery waits for a new
    // globally-authorized keyframe.
    this.cancelSettlementWatchdog();
    this.currentSDR = null;
    this.freshness = { fresh: false, reason: holdoverReason };
    this.presentationState = 'holdover';
    this.recoveryFreshStreak = 0;
    this.fallbackStartedAt = 0;
    this.fallbackKind = '';
    this.visualHoldover = true;
    this.visualHoldoverReason = holdoverReason;
    // The renderer also owns the requestAnimationFrame/timeout wait that is
    // nested underneath the controller watchdog. Cancel that wait without
    // disposing the renderer so its rejection cannot later tear down the
    // retained canvas.
    try {
      if (typeof this.renderer.cancelCompositorSettlementWaits === 'function') {
        this.renderer.cancelCompositorSettlementWaits(CLIENT_HDR_HOLDOVER_SETTLEMENT_CANCEL_REASON);
      }
    } catch (_) {}
    if (changed) {
      this.onMetric('presentation_holdover', Object.assign(this.snapshot(), {
        reason: holdoverReason
      }));
    }
    return true;
  }

  canCoordinateSDRFrame() {
    return Boolean(
      this.active && this.ready && this.documentVisible && this.surfaceVisible &&
      this.presentationState === 'visible' && this.renderer &&
      typeof this.renderer.present === 'function'
    );
  }

  offerFrame(sourceFrame, metadata = {}, options = {}) {
    if (!this.active || !this.documentVisible || !sourceFrame || typeof sourceFrame.clone !== 'function') return false;
    let frame;
    try {
      frame = sourceFrame.clone();
    } catch (_) {
      this.counters.dropped += 1;
      this.onMetric('frame_clone_failed', this.snapshot());
      return false;
    }
    const candidate = {
      frame,
      epoch: finiteNumber(metadata.epoch),
      sequence: finiteNumber(metadata.sequence),
      configGeneration: finiteNumber(metadata.configGeneration),
      presentationOrdinal: finiteNumber(metadata.presentationOrdinal),
      timestamp: finiteNumber(metadata.timestamp),
      visualAgeMillis: Math.max(0, finiteNumber(metadata.visualAgeMillis)),
      offeredAt: finiteNumber(metadata.offeredAt, this.now()),
      offeredWallMillis: finiteNumber(metadata.offeredWallMillis, Date.now()),
      commitSDR: typeof options.commitSDR === 'function' ? options.commitSDR : null
    };
    this.counters.offered += 1;
    if (this.pending) {
      releaseCandidate(this.pending);
      this.counters.coalesced += 1;
    }
    this.pending = candidate;
    this.dispatchPending();
    return Boolean(this.active && !candidate.released);
  }

  commitCoordinatedSDR(candidate) {
    if (!candidate || !candidate.commitSDR) return this.sdrMetadata(candidate || {});
    if (candidate.sdrCommitted) return candidate.committedSDRMetadata || this.sdrMetadata(candidate);
    const committed = candidate.commitSDR(candidate.frame, candidate);
    if (committed === false) return false;
    candidate.sdrCommitted = true;
    candidate.skipHDRCommit = Boolean(committed && typeof committed === 'object' && committed.skipHDRCommit);
    candidate.committedPresentationMetadata = committed && typeof committed === 'object'
      ? Object.assign({}, committed)
      : null;
    const committedMetadata = candidate.committedPresentationMetadata || {};
    candidate.committedSDRMetadata = this.sdrMetadata({
      epoch: finiteNumber(committedMetadata.epoch, candidate.epoch),
      sequence: finiteNumber(committedMetadata.sequence, candidate.sequence),
      configGeneration: finiteNumber(committedMetadata.configGeneration, candidate.configGeneration),
      presentationOrdinal: finiteNumber(committedMetadata.presentationOrdinal, candidate.presentationOrdinal),
      visualAgeMillis: finiteNumber(committedMetadata.visualAgeMillis, candidate.visualAgeMillis),
      renderedAt: finiteNumber(committedMetadata.renderedAt, this.now())
    });
    this.currentSDR = candidate.committedSDRMetadata;
    return candidate.committedSDRMetadata;
  }

  mergeCommittedPresentation(presentation, candidate) {
    const committed = candidate && candidate.committedPresentationMetadata;
    if (!presentation || !committed) return presentation;
    for (const key of ['epoch', 'sequence', 'configGeneration', 'presentationOrdinal', 'timestamp']) {
      if (Number.isFinite(Number(committed[key]))) presentation[key] = Number(committed[key]);
    }
    if (Number.isFinite(Number(committed.visualAgeMillis))) {
      presentation.visualAgeMillis = Math.max(0, Number(committed.visualAgeMillis));
    }
    const hasRenderedAt = Number.isFinite(Number(committed.renderedAt));
    if (hasRenderedAt) presentation.renderedAt = Number(committed.renderedAt);
    if (Number.isFinite(Number(committed.offeredAt))) {
      presentation.offeredAt = Number(committed.offeredAt);
    } else if (hasRenderedAt) {
      presentation.offeredAt = presentation.renderedAt;
    }
    return presentation;
  }

  cancelInFlightTimeout(candidate = null) {
    const watchdog = this.inFlightTimeout;
    if (!watchdog || (candidate && watchdog.candidate !== candidate)) return false;
    this.inFlightTimeout = null;
    if (watchdog.handle !== null) {
      try { this.clearTimer(watchdog.handle); } catch (_) {}
      watchdog.handle = null;
    }
    return true;
  }

  armInFlightTimeout(renderer, generation, candidate) {
    this.cancelInFlightTimeout();
    if (renderer !== this.renderer || generation !== this.generation ||
      !this.active || this.inFlight !== candidate) return false;
    const watchdog = { renderer, generation, candidate, startedWallAt: this.wallNow(), handle: null };
    this.inFlightTimeout = watchdog;
    try {
      const handle = this.setTimer(() => {
        if (this.inFlightTimeout !== watchdog) return;
        this.inFlightTimeout = null;
        watchdog.handle = null;
        if (renderer !== this.renderer || generation !== this.generation ||
          !this.active || this.inFlight !== candidate) return;
        this.fail('gpu_completion_timeout');
      }, this.gpuCompletionTimeoutMillis);
      watchdog.handle = handle;
      if (this.inFlightTimeout !== watchdog) {
        try { this.clearTimer(handle); } catch (_) {}
        watchdog.handle = null;
      }
      return true;
    } catch (_) {
      if (this.inFlightTimeout === watchdog) this.inFlightTimeout = null;
      this.fail('gpu_completion_watchdog_failed');
      return false;
    }
  }

  cancelPaintWait(candidate = null, source = 'cancelled') {
    const wait = this.paintWait;
    if (!wait || (candidate && wait.candidate !== candidate)) return false;
    return wait.settle(source);
  }

  waitForPaintOpportunity(candidate) {
    this.cancelPaintWait(null, 'superseded');
    return new Promise((resolve) => {
      const wait = {
        candidate,
        settled: false,
        timer: null,
        settle: null
      };
      const settle = (source) => {
        if (wait.settled) return false;
        wait.settled = true;
        if (wait.timer !== null) {
          try { this.clearTimer(wait.timer); } catch (_) {}
          wait.timer = null;
        }
        if (this.paintWait === wait) this.paintWait = null;
        resolve(String(source || 'paint'));
        return true;
      };
      wait.settle = settle;
      this.paintWait = wait;
      try {
        wait.timer = this.setTimer(() => settle('timeout'), this.paintWaitTimeoutMillis);
      } catch (_) {
        settle('watchdog_failed');
        return;
      }
      let paintOperation;
      try {
        paintOperation = this.waitForPaint();
      } catch (_) {
        settle('paint_failed');
        return;
      }
      Promise.resolve(paintOperation).then(
        () => settle('paint'),
        () => settle('paint_failed')
      );
    });
  }

  discardPreparedCandidate(renderer, candidate, reason) {
    try {
      if (renderer && typeof renderer.discardPreparedFrame === 'function') renderer.discardPreparedFrame();
    } catch (_) {}
    this.counters.dropped += 1;
    this.onMetric(reason || 'coordinated_frame_superseded', Object.assign(this.snapshot(), {
      epoch: candidate && candidate.epoch,
      sequence: candidate && candidate.sequence
    }));
  }

  presentationMetadata(result, candidate) {
    return {
      epoch: finiteNumber(result && result.epoch, candidate.epoch),
      sequence: finiteNumber(result && result.sequence, candidate.sequence),
      configGeneration: finiteNumber(result && result.configGeneration, candidate.configGeneration),
      presentationOrdinal: finiteNumber(result && result.presentationOrdinal, candidate.presentationOrdinal),
      timestamp: finiteNumber(result && result.timestamp, candidate.timestamp),
      visualAgeMillis: Math.max(0, finiteNumber(result && result.visualAgeMillis, candidate.visualAgeMillis)),
      offeredAt: finiteNumber(result && result.offeredAt, candidate.offeredAt),
      renderedAt: finiteNumber(result && result.renderedAt),
      queueDelayMillis: Math.max(0, finiteNumber(result && result.queueDelayMillis)),
      submitMillis: Math.max(0, finiteNumber(result && result.submitMillis)),
      completionMillis: Math.max(0, finiteNumber(result && result.completionMillis)),
      displayReadyMillis: Math.max(0, finiteNumber(result && result.displayReadyMillis)),
      decodedFrameToSubmitMillis: Math.max(0, finiteNumber(result && result.decodedFrameToSubmitMillis)),
      decodedFrameToDisplayReadyMillis: Math.max(0, finiteNumber(result && result.decodedFrameToDisplayReadyMillis)),
      selectedDisplayBoost: normalizeClientHDRDisplayBoost(result && result.selectedDisplayBoost),
      edrRequestPatchIntended: typeof (result && result.edrRequestPatchIntended) === 'boolean'
        ? result.edrRequestPatchIntended
        : candidate.requestPatch === true,
      intendedRequestPatchPeak: Math.max(0, finiteNumber(
        result && result.intendedRequestPatchPeak,
        candidate.requestPatch ? CLIENT_HDR_REQUEST_PATCH_PEAK : 0
      )),
      intendedRequestPatchEdge: Math.max(0, finiteNumber(
        result && result.intendedRequestPatchEdge,
        candidate.requestPatch ? CLIENT_HDR_REQUEST_PATCH_EDGE : 0
      )),
      gpuCompleted: Boolean(result && result.gpuCompleted),
      compositorOpportunitiesCompleted: false
    };
  }

  dispatchPending() {
    if (!this.active || !this.ready || this.inFlight || !this.pending || !this.renderer) return;
    const renderer = this.renderer;
    const generation = this.generation;
    const candidate = this.pending;
    this.pending = null;
    this.inFlight = candidate;
    const presentationAborted = {};
    const isCurrent = (revision = null) => Boolean(
      renderer === this.renderer && generation === this.generation && this.active &&
      this.inFlight === candidate && (revision === null || revision === this.presentationRevision)
    );
    const renderStage = async (renderOptions) => {
      let operation;
      try {
        operation = renderer.render(candidate.frame, candidate, renderOptions);
        if (!this.armInFlightTimeout(renderer, generation, candidate)) {
          try { await Promise.resolve(operation); } catch (_) {}
          return presentationAborted;
        }
      } catch (error) {
        this.cancelInFlightTimeout(candidate);
        throw error;
      }
      const result = await Promise.resolve(operation);
      const completionWatchdog = this.inFlightTimeout;
      if (completionWatchdog && completionWatchdog.renderer === renderer &&
        completionWatchdog.generation === generation && completionWatchdog.candidate === candidate &&
        this.wallNow() - completionWatchdog.startedWallAt >= this.gpuCompletionTimeoutMillis) {
        this.cancelInFlightTimeout(candidate);
        throw new Error('gpu_completion_timeout');
      }
      this.cancelInFlightTimeout(candidate);
      if (!isCurrent()) return presentationAborted;
      this.captureRendererMetric(result);
      return result;
    };
    const presentPrepared = async (revision) => {
      if (typeof renderer.present !== 'function') throw new Error('renderer_present_unavailable');
      renderer.present();
      if (typeof renderer.waitForPresentCompletion !== 'function') {
        throw new Error('renderer_present_completion_unavailable');
      }
      const result = await Promise.resolve(renderer.waitForPresentCompletion());
      if (!isCurrent(revision)) return presentationAborted;
      if (!result || result.gpuCompleted !== true) {
        throw new Error('renderer_present_completion_unconfirmed');
      }
      this.captureRendererMetric(result);
      return result;
    };
    const waitForCompositor = async (revision, requiredFrames) => {
      if (typeof renderer.waitForPresentedCompositorOpportunities !== 'function') {
        throw new Error('renderer_compositor_opportunities_unavailable');
      }
      const result = await renderer.waitForPresentedCompositorOpportunities(requiredFrames);
      if (!isCurrent(revision)) return presentationAborted;
      const source = String(result && result.postPresentSource || '');
      const count = Math.max(0, Math.round(finiteNumber(result && result.postPresentOpportunityCount)));
      if (source !== 'animation_frame' || count !== requiredFrames ||
        !result || result.gpuCompleted !== true || result.compositorOpportunitiesCompleted !== true) {
        throw new Error('hdr_presented_display_refresh_failed');
      }
      return result;
    };
    const freshnessFor = (presentation) => {
      const freshness = clientHDRFreshness(presentation, this.currentSDR, this.now(), {
        maxSequenceLag: this.maxSequenceLag,
        maxAgeDeltaMillis: this.maxAgeDeltaMillis
      });
      this.freshness = freshness;
      return freshness;
    };
    const authorityCurrent = (presentation, requireVisible = false) => Boolean(
      this.documentVisible && !candidate.skipHDRCommit &&
      presentation.selectedDisplayBoost === this.selectedDisplayBoost &&
      (!requireVisible || this.surfaceVisible) && this.surfaceRevealAllowed()
    );
    const deferHoldoverRelease = (presentation, stage, discardPrepared, copyCompleted = false) => {
      if (!this.visualHoldover || this.holdoverReleaseAllowed(presentation)) return false;
      // If authority changed only after present(), these pixels are already on
      // the continuous canvas.  Track their identity while keeping them
      // passive/unproven; otherwise preserve the older held identity.
      if (copyCompleted) this.presented = presentation;
      this.cancelSettlementWatchdog();
      this.presentationState = 'holdover';
      this.freshness = { fresh: false, reason: 'holdover_release_not_authorized' };
      if (this.pendingPresentation === presentation) this.pendingPresentation = null;
      if (discardPrepared) {
        this.discardPreparedCandidate(renderer, candidate, 'holdover_release_not_authorized');
      }
      this.onMetric('holdover_release_deferred', Object.assign(this.snapshot(), {
        reason: 'global_stream_not_fresh',
        stage: String(stage || 'unknown')
      }));
      return true;
    };

    const run = async () => {
      const activationRequired = !this.firstPresented ||
        (!this.requestPatchPresented && this.selectedDisplayBoost > 1);
      const requestPatch = activationRequired && !this.requestPatchPresented && this.selectedDisplayBoost > 1;
      candidate.activationFrame = activationRequired;
      candidate.requestPatch = requestPatch;
      this.rendererMetrics.edrRequestPatchIntended = requestPatch;
      this.rendererMetrics.intendedRequestPatchPeak = requestPatch ? CLIENT_HDR_REQUEST_PATCH_PEAK : 0;
      this.rendererMetrics.intendedRequestPatchEdge = requestPatch ? CLIENT_HDR_REQUEST_PATCH_EDGE : 0;
      const initialResult = await renderStage({ activationFrame: activationRequired, requestPatch });
      if (initialResult === presentationAborted) return;
      this.counters.rendered += 1;
      candidate.completed = true;
      let completedPresentation = this.presentationMetadata(initialResult, candidate);
      if (completedPresentation.selectedDisplayBoost !== this.selectedDisplayBoost) {
        this.discardPreparedCandidate(renderer, candidate, 'prepared_boost_superseded');
        return;
      }
      const revision = ++this.presentationRevision;
      const revealStartedAt = this.now();
      this.pendingPresentation = completedPresentation;
      const paintSource = await this.waitForPaintOpportunity(candidate);
      if (!isCurrent(revision)) return;
      if (paintSource !== 'paint') {
        const reason = paintSource === 'timeout' ? 'paint_wait_timeout' : 'paint_wait_failed';
        const requestRecovery = reason === 'paint_wait_timeout' && !this.firstPresented &&
          !this.pending && !this.paintRecoveryRequested;
        const recoveryExhausted = reason === 'paint_wait_timeout' && !this.firstPresented &&
          !this.pending && this.paintRecoveryRequested;
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        this.discardPreparedCandidate(renderer, candidate, reason);
        // A replacement candidate that misses its pre-copy paint opportunity
        // has not changed the held canvas. Keep an established transient
        // holdover bright and passive; ordinary live presentation retains the
        // existing fail-closed behavior.
        if (this.surfaceVisible && !this.visualHoldover) this.latchFallback(reason);
        if (reason === 'paint_wait_failed') {
          this.fail(reason);
        } else if (requestRecovery) {
          this.paintRecoveryRequested = true;
          try { this.onRecoveryRequest(reason, this.snapshot()); } catch (_) {}
        } else if (recoveryExhausted) {
          this.fail('paint_recovery_exhausted');
        }
        return;
      }
      if (completedPresentation.selectedDisplayBoost !== this.selectedDisplayBoost) {
        this.discardPreparedCandidate(renderer, candidate, 'prepared_boost_superseded');
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        return;
      }
      const committedSDR = this.commitCoordinatedSDR(candidate);
      if (committedSDR === false) {
        this.discardPreparedCandidate(renderer, candidate, 'coordinated_frame_superseded');
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        return;
      }
      this.mergeCommittedPresentation(completedPresentation, candidate);
      if (candidate.skipHDRCommit) {
        this.discardPreparedCandidate(renderer, candidate, 'coordinated_hdr_bypassed');
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        this.latchFallback('control_code_priority');
        return;
      }
      const preparedFreshness = freshnessFor(completedPresentation);
      if (!preparedFreshness.fresh) {
        this.discardPreparedCandidate(renderer, candidate, `prepared_${preparedFreshness.reason}`);
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        // The page may revoke an expiring SDR watermark while a boost redraw
        // is preparing. Discard that obsolete candidate without hardening the
        // refresh already owned by the boost; a fresh picture must still pass
        // every normal presentation gate before it can replace the surface.
        if (this.fallbackKind === 'refresh' && preparedFreshness.reason === 'missing_watermark') return;
        this.latchFallback(preparedFreshness.reason);
        return;
      }
      if (!authorityCurrent(completedPresentation)) throw new Error('presentation_authority_revoked');
      // Rendering above only prepares a staging texture.  Once a valid HDR
      // image is being held, global stream authority must be current before a
      // prepared candidate is allowed to replace those visible pixels.
      if (deferHoldoverRelease(completedPresentation, 'prepared', true)) return;

      if (activationRequired) {
        const activationCopy = await presentPrepared(revision);
        if (activationCopy === presentationAborted) return;
        // present() has already changed the continuous canvas. During a
        // holdover, its metadata must follow those physical pixels even while
        // proof remains false.
        if (this.visualHoldover) this.presented = completedPresentation;
        if (deferHoldoverRelease(completedPresentation, 'after_activation_copy', false, true)) return;
        if (!authorityCurrent(completedPresentation)) throw new Error('presentation_authority_revoked');
        const activationFreshness = freshnessFor(completedPresentation);
        if (!activationFreshness.fresh) {
          if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
          this.latchFallback(activationFreshness.reason);
          return;
        }
        if (!this.visualHoldover) this.presented = completedPresentation;
        if (this.presentationState === 'fallback_latched' && this.fallbackKind === 'hard') {
          this.recoveryFreshStreak += 1;
          if (this.recoveryFreshStreak < 2) {
            if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
            return;
          }
        }
        this.cancelRecoveryPaintCheck();
        this.recoveryFreshStreak = 0;
        this.presentationState = 'settling';
        this.fallbackStartedAt = 0;
        this.fallbackKind = '';
        this.setSurface(true, Object.assign({}, completedPresentation, activationFreshness), 'activation_copied');
        if (!this.surfaceVisible) throw new Error('presentation_authority_revoked');
        if (completedPresentation.edrRequestPatchIntended) this.requestPatchPresented = true;
        if (!this.armSettlementWatchdog(renderer, generation, revision, completedPresentation)) return;
        const activationPaint = await waitForCompositor(revision, 2);
        if (activationPaint === presentationAborted) return;
        if (this.checkSettlementDeadline('activation_compositor_completion')) return;
        this.captureRendererMetric(activationPaint);
        if (!authorityCurrent(completedPresentation, true)) throw new Error('presentation_authority_revoked');
        const settledActivationFreshness = freshnessFor(completedPresentation);
        if (!settledActivationFreshness.fresh) throw new Error(settledActivationFreshness.reason);
        if (deferHoldoverRelease(completedPresentation, 'after_activation_compositor', false, true)) return;
        this.rendererMetrics.activationCompositorOpportunitiesCompleted = true;
        this.rendererMetrics.activationPostPresentSource = String(activationPaint.postPresentSource || '');
        this.rendererMetrics.activationPostPresentOpportunityCount = Math.max(0, Math.round(finiteNumber(
          activationPaint.postPresentOpportunityCount
        )));
        this.onMetric('edr_activation_presented', Object.assign(this.snapshot(), {
          activationFrame: true,
          activationIdentity: true,
          activationIntendedOutputPeak: completedPresentation.intendedRequestPatchPeak || 1,
          epoch: completedPresentation.epoch,
          sequence: completedPresentation.sequence
        }));

        if (!authorityCurrent(completedPresentation, true)) throw new Error('presentation_authority_revoked');
        this.rendererMetrics.gpuCompleted = false;
        this.rendererMetrics.compositorOpportunitiesCompleted = false;
        const targetResult = await renderStage({ activationFrame: false, requestPatch: false });
        if (targetResult === presentationAborted) return;
        if (this.checkSettlementDeadline('target_gpu_completion')) return;
        const targetPresentation = this.presentationMetadata(targetResult, candidate);
        this.mergeCommittedPresentation(targetPresentation, candidate);
        if (targetPresentation.selectedDisplayBoost !== this.selectedDisplayBoost ||
          !authorityCurrent(targetPresentation, true)) {
          throw new Error('presentation_authority_revoked');
        }
        const targetPreparedFreshness = freshnessFor(targetPresentation);
        if (!targetPreparedFreshness.fresh) throw new Error(targetPreparedFreshness.reason);
        completedPresentation = targetPresentation;
        this.pendingPresentation = completedPresentation;
      }

      // Re-check after every asynchronous preparation boundary.  present()
      // performs the actual swap synchronously, so this is the final authority
      // gate before the held canvas can change.
      if (deferHoldoverRelease(completedPresentation, 'before_target_copy', true)) return;
      const targetCopy = await presentPrepared(revision);
      if (targetCopy === presentationAborted) return;
      if (this.visualHoldover) this.presented = completedPresentation;
      if (deferHoldoverRelease(completedPresentation, 'after_target_copy', false, true)) return;
      if (activationRequired && this.checkSettlementDeadline('target_copy_completion')) return;
      if (!authorityCurrent(completedPresentation, activationRequired)) {
        if (activationRequired) throw new Error('presentation_authority_revoked');
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        this.latchFallback(!this.documentVisible ? 'document_hidden' : 'presentation_authority_revoked');
        return;
      }
      const copiedFreshness = freshnessFor(completedPresentation);
      if (!copiedFreshness.fresh) {
        if (activationRequired) throw new Error(copiedFreshness.reason);
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        this.latchFallback(copiedFreshness.reason);
        return;
      }
      if (!this.visualHoldover) this.presented = completedPresentation;
      if (!activationRequired) {
        if (this.presentationState === 'fallback_latched' && this.fallbackKind === 'hard') {
          this.recoveryFreshStreak += 1;
          if (this.recoveryFreshStreak < 2) {
            if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
            return;
          }
        }
        this.cancelRecoveryPaintCheck();
        this.recoveryFreshStreak = 0;
        this.presentationState = 'settling';
        this.fallbackStartedAt = 0;
        this.fallbackKind = '';
        this.setSurface(true, Object.assign({}, completedPresentation, copiedFreshness), 'target_copied');
        if (!this.surfaceVisible) return;
        if (!this.armSettlementWatchdog(renderer, generation, revision, completedPresentation)) return;
      }
      const targetOpportunityTarget = activationRequired ? 1 : 2;
      const targetPaint = await waitForCompositor(revision, targetOpportunityTarget);
      if (targetPaint === presentationAborted) return;
      if (this.checkSettlementDeadline('target_compositor_completion')) return;
      this.captureRendererMetric(targetPaint);
      this.cancelSettlementWatchdog();
      if (!authorityCurrent(completedPresentation, true)) {
        if (activationRequired) throw new Error('presentation_authority_revoked');
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        this.latchFallback(!this.documentVisible ? 'document_hidden' : 'presentation_authority_revoked');
        return;
      }
      const paintedFreshness = freshnessFor(completedPresentation);
      if (!paintedFreshness.fresh) {
        if (activationRequired) throw new Error(paintedFreshness.reason);
        if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
        this.latchFallback(paintedFreshness.reason);
        return;
      }
      const paintWaitMillis = Math.max(0, this.now() - revealStartedAt);
      completedPresentation.displayReadyMillis += paintWaitMillis;
      completedPresentation.decodedFrameToDisplayReadyMillis += paintWaitMillis;
      completedPresentation.compositorOpportunitiesCompleted = true;
      completedPresentation.postPresentSource = String(targetPaint.postPresentSource || '');
      completedPresentation.postPresentOpportunityCount = Math.max(0, Math.round(finiteNumber(
        targetPaint.postPresentOpportunityCount
      )));
      this.rendererMetrics.compositorOpportunitiesCompleted = true;
      this.rendererMetrics.targetCompositorOpportunitiesCompleted = true;
      this.rendererMetrics.targetPostPresentSource = completedPresentation.postPresentSource;
      this.rendererMetrics.targetPostPresentOpportunityCount = completedPresentation.postPresentOpportunityCount;
      // Global transport/status authority can change while the compositor
      // confirmation is pending.  Keep proof false and identify the copied
      // pixels accurately if that happens; do not claim this copy live.
      if (deferHoldoverRelease(completedPresentation, 'after_compositor', false, true)) return;
      this.presented = completedPresentation;
      this.presentationState = 'visible';
      this.fallbackStartedAt = 0;
      this.fallbackKind = '';
      this.visualHoldover = false;
      this.visualHoldoverReason = '';
      if (this.pendingPresentation === completedPresentation) this.pendingPresentation = null;
      this.recordPresentedMetric(completedPresentation);
    };

    run().catch((error) => {
      this.cancelInFlightTimeout(candidate);
      if (isCurrent()) {
        const failureReason = String(error && error.message || 'render_failed').slice(0, 80);
        const supersededHoldoverSettlement = this.visualHoldover && this.firstPresented &&
          this.surfaceVisible && this.presented && (
            failureReason === CLIENT_HDR_HOLDOVER_SETTLEMENT_CANCEL_REASON ||
            failureReason === 'hdr_presented_display_refresh_timeout'
          );
        if (supersededHoldoverSettlement) {
          // The canvas was already copied before this compositor wait began.
          // Keep those pixels and their identity, but never promote them to
          // fresh proof or emit a presented-success metric.
          this.cancelSettlementWatchdog();
          this.pendingPresentation = null;
          this.currentSDR = null;
          this.presentationState = 'holdover';
          this.freshness = {
            fresh: false,
            reason: this.visualHoldoverReason || 'stream_recovering'
          };
          this.onMetric('holdover_settlement_superseded', Object.assign(this.snapshot(), {
            reason: failureReason
          }));
        } else {
          this.fail(failureReason);
        }
      }
    }).finally(() => {
      this.cancelInFlightTimeout(candidate);
      this.cancelPaintWait(candidate);
      releaseCandidate(candidate);
      if (this.inFlight === candidate) this.inFlight = null;
      if (renderer === this.renderer && generation === this.generation && this.active) this.dispatchPending();
    });
  }

  recordPresentedMetric(presentation) {
    const first = !this.firstPresented;
    this.firstPresented = true;
    this.onMetric(first ? 'first_presented' : 'presented', Object.assign(this.snapshot(), {
      selectedDisplayBoost: presentation.selectedDisplayBoost,
      firstShownMillis: first ? Math.max(0, this.now() - this.enabledAt) : undefined
    }));
  }

  updateSurface(options = {}) {
    if (!this.active || !this.ready || !this.documentVisible) {
      this.latchFallback('inactive');
      return false;
    }
    const freshness = clientHDRFreshness(this.presented, this.currentSDR, this.now(), {
      maxSequenceLag: this.maxSequenceLag,
      maxAgeDeltaMillis: this.maxAgeDeltaMillis
    });
    this.freshness = freshness;
    if (!freshness.fresh) {
      this.recoveryFreshStreak = 0;
      if (this.presented && this.currentSDR) {
        this.latchFallback(freshness.reason);
      }
      return false;
    }
    if (!options.submittedFrame) return this.surfaceVisible;
    if (this.presentationState === 'fallback_latched') {
      if (this.fallbackKind === 'soft') {
        this.scheduleSoftRecovery();
        return false;
      }
      if (this.fallbackKind !== 'refresh') {
        this.recoveryFreshStreak += 1;
        if (this.recoveryFreshStreak < 2) return false;
      }
    }
    this.cancelRecoveryPaintCheck();
    this.recoveryFreshStreak = 0;
    this.presentationState = 'visible';
    this.fallbackStartedAt = 0;
    this.fallbackKind = '';
    if (!this.surfaceRevealAllowed()) {
      this.latchFallback('surface_reveal_blocked');
      return false;
    }
    this.setSurface(true, Object.assign({}, this.presented, freshness), 'fresh');
    return true;
  }

  scheduleSoftRecovery() {
    if (this.recoveryPaintCheck) return;
    const generation = this.generation;
    const token = {};
    const callback = () => {
      if (!this.recoveryPaintCheck || this.recoveryPaintCheck.token !== token) return;
      this.recoveryPaintCheck = null;
      if (!this.active || generation !== this.generation || this.surfaceVisible ||
        this.presentationState !== 'fallback_latched' || this.fallbackKind !== 'soft') return;
      const freshness = clientHDRFreshness(this.presented, this.currentSDR, this.now(), {
        maxSequenceLag: this.maxSequenceLag,
        maxAgeDeltaMillis: this.maxAgeDeltaMillis
      });
      this.freshness = freshness;
      if (!freshness.fresh) {
        this.onMetric('soft_recovery_stale', Object.assign(this.snapshot(), { reason: freshness.reason }));
        return;
      }
      this.recoveryFreshStreak = 0;
      this.presentationState = 'visible';
      this.fallbackStartedAt = 0;
      this.fallbackKind = '';
      if (!this.surfaceRevealAllowed()) {
        this.latchFallback('surface_reveal_blocked');
        return;
      }
      this.setSurface(true, Object.assign({}, this.presented, freshness), 'fresh');
      this.recordPresentedMetric(this.presented);
    };
    const handle = this.schedulePaintCheck(callback);
    this.recoveryPaintCheck = { token, handle };
    this.onMetric('soft_recovery_deferred', this.snapshot());
  }

  cancelRecoveryPaintCheck() {
    const scheduled = this.recoveryPaintCheck;
    if (!scheduled) return;
    this.recoveryPaintCheck = null;
    try { this.cancelPaintCheck(scheduled.handle); } catch (_) {}
  }

  ensureExactProof(epoch, sequence) {
    const exact = Boolean(
      this.surfaceVisible && !this.visualHoldover && this.presentationState === 'visible' &&
      this.freshness && this.freshness.fresh && this.presented &&
      finiteNumber(this.presented.epoch) === finiteNumber(epoch) &&
      finiteNumber(this.presented.sequence) === finiteNumber(sequence)
    );
    if (!exact && !this.visualHoldover) this.latchFallback('proof_mismatch');
    return exact;
  }

  setSurface(visible, presented, reason) {
    const next = Boolean(visible);
    if (this.surfaceVisible === next) return false;
    this.surfaceVisible = next;
    this.surfaceTransitions += 1;
    this.onSurface(next, presented, String(reason || (next ? 'fresh' : 'fallback')));
    this.onMetric('surface_transition', Object.assign(this.snapshot(), {
      selectedDisplayBoost: normalizeClientHDRDisplayBoost(presented && presented.selectedDisplayBoost),
      toSurface: next ? 'hdr' : 'sdr',
      reason: String(reason || '').slice(0, 80)
    }));
    return true;
  }

  surfaceRevealAllowed() {
    try {
      return this.canRevealSurface(this.snapshot()) !== false;
    } catch (_) {
      return false;
    }
  }

  holdoverReleaseAllowed(presentation) {
    try {
      return this.canReleaseHoldover(presentation, this.snapshot()) !== false;
    } catch (_) {
      return false;
    }
  }

  latchFallback(reason) {
    const fallbackReason = String(reason || 'fallback');
    const transientFreshnessMismatch = fallbackReason === 'missing_watermark' ||
      fallbackReason === 'epoch_mismatch' || fallbackReason === 'sequence_lag' ||
      fallbackReason === 'visual_age' || fallbackReason.startsWith('prepared_epoch_mismatch') ||
      fallbackReason.startsWith('prepared_sequence_lag') ||
      fallbackReason.startsWith('prepared_visual_age');
    if (this.visualHoldover && transientFreshnessMismatch) return false;
    this.cancelRecoveryPaintCheck();
    const nextKind = reason === 'sequence_lag' || reason === 'visual_age'
      ? 'soft'
      : reason === 'boost_superseded' ? 'refresh' : 'hard';
    if (this.presentationState !== 'fallback_latched') {
      this.presentationState = 'fallback_latched';
      this.fallbackStartedAt = this.now();
      this.fallbackKind = nextKind;
    } else if (nextKind === 'hard') {
      this.fallbackKind = 'hard';
    }
    this.recoveryFreshStreak = 0;
    this.visualHoldover = false;
    this.visualHoldoverReason = '';
    this.setSurface(false, this.presented, reason);
    return true;
  }

  fail(reason) {
    const failureReason = String(reason || 'failed');
    if (failureReason === 'gpu_completion_timeout') {
      const timedOut = this.inFlight;
      this.onMetric('gpu_completion_timeout', Object.assign(this.snapshot(), {
        gpuCompletionTimeoutMillis: this.gpuCompletionTimeoutMillis,
        epoch: finiteNumber(timedOut && timedOut.epoch),
        sequence: finiteNumber(timedOut && timedOut.sequence),
        presentationOrdinal: finiteNumber(timedOut && timedOut.presentationOrdinal)
      }));
    }
    const candidates = [this.pending, this.inFlight];
    for (const candidate of candidates) {
      if (!candidate || !candidate.commitSDR || candidate.sdrCommitted) continue;
      try {
        if (this.commitCoordinatedSDR(candidate)) break;
      } catch (_) {}
    }
    this.counters.failures += 1;
    this.onMetric('fallback', Object.assign(this.snapshot(), { reason: failureReason }));
    this.onStatus('failed', failureReason);
    this.dispose(failureReason);
  }

  dispose(reason = 'disabled') {
    this.cancelRendererInitTimeout();
    this.cancelSettlementWatchdog();
    this.cancelRecoveryPaintCheck();
    this.cancelInFlightTimeout();
    this.cancelPaintWait();
    const hadSession = Boolean(this.active || this.renderer || this.counters.offered || this.counters.rendered || this.counters.failures);
    if (this.pending) {
      releaseCandidate(this.pending);
      this.pending = null;
      this.counters.dropped += 1;
    }
    if (this.inFlight) {
      const inFlight = this.inFlight;
      releaseCandidate(inFlight);
      this.inFlight = null;
      if (!inFlight.completed) this.counters.dropped += 1;
    }
    this.presentationRevision += 1;
    this.pendingPresentation = null;
    this.presentationState = 'standby';
    this.recoveryFreshStreak = 0;
    this.fallbackKind = '';
    this.visualHoldover = false;
    this.visualHoldoverReason = '';
    this.setSurface(false, this.presented, reason);
    const renderer = this.renderer;
    this.renderer = null;
    this.active = false;
    this.ready = false;
    if (hadSession) this.onMetric('session_summary', Object.assign(this.snapshot(), { reason: String(reason || 'disabled') }));
    try { if (renderer) renderer.dispose(); } catch (_) {}
  }

  snapshot() {
    return Object.assign({
      engine: CLIENT_HDR_ENGINE,
      pipeline: CLIENT_HDR_PIPELINE,
      presentationKind: CLIENT_HDR_PRESENTATION_KIND,
      targetDisplayBoost: CLIENT_HDR_TARGET_DISPLAY_BOOST,
      selectedDisplayBoost: this.selectedDisplayBoost,
      canvasEncoding: this.canvasEncoding,
      active: this.active,
      ready: this.ready,
      surfaceVisible: this.surfaceVisible,
      presentationState: this.presentationState,
      fallbackKind: this.fallbackKind,
      firstPresented: this.firstPresented,
      visualHoldover: this.visualHoldover,
      visualHoldoverReason: this.visualHoldoverReason,
      proofFresh: Boolean(this.freshness && this.freshness.fresh &&
        this.presentationState === 'visible' && !this.visualHoldover),
      streamRegionVisible: this.streamRegionVisible,
      recoveryFreshStreak: this.recoveryFreshStreak,
      recoveryPaintCheckPending: Boolean(this.recoveryPaintCheck),
      paintPending: Boolean(this.pendingPresentation),
      surfaceTransitions: this.surfaceTransitions,
      fallbackDurationMillis: this.fallbackStartedAt > 0 ? Math.max(0, this.now() - this.fallbackStartedAt) : 0,
      pending: Boolean(this.pending),
      inFlight: Boolean(this.inFlight),
      gpuCompletionTimeoutMillis: this.gpuCompletionTimeoutMillis,
      gpuCompletionTimeoutPending: Boolean(this.inFlightTimeout),
      paintWaitTimeoutMillis: this.paintWaitTimeoutMillis,
      paintWaitTimeoutPending: Boolean(this.paintWait),
      paintRecoveryRequested: this.paintRecoveryRequested,
      requestPatchPresented: this.requestPatchPresented,
      coordinatedCommit: Boolean(this.inFlight && this.inFlight.commitSDR),
      offered: this.counters.offered,
      rendered: this.counters.rendered,
      coalesced: this.counters.coalesced,
      dropped: this.counters.dropped,
      failures: this.counters.failures,
      rendererActive: Boolean(this.renderer),
      rendererGeneration: this.generation,
      rendererInitTimeoutMillis: this.rendererInitTimeoutMillis,
      rendererInitTimeoutPending: Boolean(this.rendererInitTimeout),
      settlementTimeoutMillis: this.settlementTimeoutMillis,
      settlementPending: Boolean(this.settlementWatchdog),
      settlementElapsedMillis: this.settlementStartedWallAt > 0
        ? Math.max(0, this.wallNow() - this.settlementStartedWallAt)
        : 0,
      ownedFrameCount: Number(Boolean(this.pending && !this.pending.released)) +
        Number(Boolean(this.inFlight && !this.inFlight.released)),
      epoch: finiteNumber(this.presented && this.presented.epoch),
      sequence: finiteNumber(this.presented && this.presented.sequence),
      configGeneration: finiteNumber(this.presented && this.presented.configGeneration),
      presentationOrdinal: finiteNumber(this.presented && this.presented.presentationOrdinal),
      sequenceLag: finiteNumber(this.freshness && this.freshness.sequenceLag),
      sourceSequenceLag: finiteNumber(this.freshness && this.freshness.sourceSequenceLag),
      ageDeltaMillis: finiteNumber(this.freshness && this.freshness.ageDeltaMillis),
      queueDelayMillis: finiteNumber(this.presented && this.presented.queueDelayMillis),
      submitMillis: finiteNumber(this.presented && this.presented.submitMillis),
      completionMillis: finiteNumber(this.presented && this.presented.completionMillis),
      displayReadyMillis: finiteNumber(this.presented && this.presented.displayReadyMillis),
      decodedFrameToSubmitMillis: finiteNumber(this.presented && this.presented.decodedFrameToSubmitMillis),
      decodedFrameToDisplayReadyMillis: finiteNumber(this.presented && this.presented.decodedFrameToDisplayReadyMillis)
    }, this.rendererMetrics);
  }
}
