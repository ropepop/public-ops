import { CLIENT_HDR_ALLOWED_BOOSTS, ClientHDRRenderer } from './client-hdr-renderer.mjs';

export const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
export const CLIENT_HDR_TARGET_DISPLAY_BOOST = 4;
export const CLIENT_HDR_DISPLAY_BOOSTS = CLIENT_HDR_ALLOWED_BOOSTS;

export function normalizeClientHDRDisplayBoost(value) {
  const boost = Number(value);
  return CLIENT_HDR_DISPLAY_BOOSTS.includes(boost) ? boost : CLIENT_HDR_TARGET_DISPLAY_BOOST;
}

export function clientHDRCapability(environment = globalThis) {
  const videoFrame = typeof environment.VideoFrame === 'function';
  const mainThreadCanvas = typeof environment.HTMLCanvasElement?.prototype?.getContext === 'function';
  const webgpu = Boolean(environment.navigator?.gpu);
  const highDynamicRange = Boolean(environment.matchMedia?.('(dynamic-range: high)').matches);
  const dynamicRangeLimit = Boolean(environment.CSS?.supports?.('dynamic-range-limit', 'no-limit'));
  return { supported: videoFrame && mainThreadCanvas && webgpu && highDynamicRange && dynamicRangeLimit,
    videoFrame, mainThreadCanvas, webgpu, highDynamicRange, dynamicRangeLimit };
}

const close = (candidate) => candidate?.frame.close();
const age = (candidate) => candidate ? candidate.visualAgeMillis + Math.max(0, performance.now() - candidate.offeredAt) : Infinity;
const samePicture = (left, right) => left && right && left.epoch === right.epoch &&
  left.sequence === right.sequence && left.configGeneration === right.configGeneration;

// One pending picture and one presentation sequence. The renderer bounds GPU
// and compositor waits. The page alone decides whether to retry a failure.
export class ClientHDRController {
  constructor(options = {}) {
    this.options = options;
    this.renderer = null;
    this.generation = 0;
    this.ready = false;
    this.active = false;
    this.visible = true;
    this.boost = 4;
    this.pending = null;
    this.inFlight = null;
    this.currentSDR = null;
    this.presented = null;
    this.confirmed = false;
    this.activated = false;
    this.surfaceVisible = false;
    this.initTimer = null;
    this.presentationGeneration = 0;
    this.reassertPending = false;
  }

  start({ canvas, width, height, boost = 4, picture }) {
    this.dispose();
    this.active = true;
    this.boost = normalizeClientHDRDisplayBoost(boost);
    const generation = this.generation;
    const renderer = new ClientHDRRenderer({ onFailure: (reason) => {
      if (generation === this.generation && this.visible) this.fail(reason);
    } });
    this.renderer = renderer;
    this.initTimer = setTimeout(() => {
      if (generation === this.generation) this.fail('renderer_init_timeout');
    }, 8000);
    renderer.initialize({ canvas, width, height, boost: this.boost, picture }).then(() => {
      if (generation !== this.generation) return;
      clearTimeout(this.initTimer);
      this.initTimer = null;
      this.ready = true;
      this.options.onStatus?.('ready');
      this.dispatch();
    }).catch(() => {
      if (generation === this.generation && this.visible) this.fail('renderer_init_failed');
    });
  }

  setDisplayBoost(boost) {
    this.boost = normalizeClientHDRDisplayBoost(boost);
    this.confirmed = false;
  }

  setDocumentVisible(visible) {
    this.visible = Boolean(visible);
    if (!visible) this.holdLastPresentation();
  }

  noteSDRFrame(metadata) {
    this.currentSDR = metadata;
  }

  reassertHDR() {
    if (this.active && this.visible) this.reassertPending = true;
  }

  holdLastPresentation({ keepProof = false } = {}) {
    this.presentationGeneration++;
    if (!keepProof) this.confirmed = false;
    close(this.pending);
    this.pending = null;
  }

  offerFrame(frame, metadata, { commitSDR, retainedResult = false, visualOnly = false,
    decorated = false, restoreRaw = false } = {}) {
    if (!this.active || !this.visible) return false;
    // Local animation cannot replace queued stream work or activate a new HDR
    // surface. It may only repaint the picture already on this surface.
    if (visualOnly && (!this.activated || !this.surfaceVisible ||
      (this.pending && !this.pending.visualOnly) || !samePicture(metadata, this.presented) ||
      !samePicture(metadata, this.currentSDR))) return false;
    let owned;
    try { owned = frame.clone(); } catch { return false; }
    close(this.pending);
    this.pending = { ...metadata, frame: owned, boost: this.boost, commitSDR, retainedResult,
      visualOnly, decorated, restoreRaw: visualOnly && restoreRaw && !decorated,
      offeredAt: Number(metadata.offeredAt ?? performance.now()),
      visualAgeMillis: Number(metadata.visualAgeMillis ?? Infinity) };
    this.dispatch();
    return true;
  }

  async dispatch() {
    if (!this.active || !this.ready || this.inFlight || !this.pending) return;
    const candidate = this.pending;
    this.pending = null;
    this.inFlight = candidate;
    const renderer = this.renderer, generation = this.generation;
    const presentationGeneration = this.presentationGeneration;
    const current = () => generation === this.generation && this.active &&
      presentationGeneration === this.presentationGeneration &&
      this.visible && this.boost === candidate.boost &&
      this.options.canRevealSurface?.() !== false &&
      this.options.canReleaseHoldover?.(candidate) !== false &&
      (!candidate.visualOnly || (this.activated && samePicture(candidate, this.presented) &&
        samePicture(candidate, this.currentSDR))) &&
      (candidate.retainedResult || candidate.restoreRaw || (Number.isFinite(age(candidate)) && age(candidate) <= 3000));
    const present = async (boosted) => {
      const reconfigure = boosted && this.reassertPending;
      if (reconfigure) this.reassertPending = false;
      await renderer.present({ reconfigure });
    };
    const copy = async (opportunities, boosted = true) => {
      if (!current()) return false;
      this.presented = candidate;
      this.confirmed = false;
      await present(boosted);
      if (!current()) return false;
      this.surface(true, boosted ? candidate.boost : 1);
      await renderer.waitForCompositorSettlement(opportunities);
      return current();
    };
    try {
      if (!current()) return;
      renderer.setBoost(candidate.boost);
      if (candidate.visualOnly) {
        // The display is already activated and its source picture is unchanged.
        // Keep GPU completion/validation and serialization, without waiting for
        // extra compositor opportunities or generating fresh stream proof.
        await renderer.render(candidate.frame, { activationFrame: false, requestPatch: false });
        if (!current()) return;
        if (candidate.commitSDR?.(candidate.frame, candidate) === false) return;
        await present(true);
        if (current()) this.presented = { ...this.presented, decorated: candidate.decorated };
        return;
      }
      const activate = !this.activated;
      await renderer.render(candidate.frame, { activationFrame: activate, requestPatch: activate });
      if (!current()) return;
      await renderer.waitForCompositorSettlement(1);
      if (!current()) return;
      const committed = candidate.commitSDR?.(candidate.frame, candidate);
      if (committed === false) return;
      this.currentSDR = committed || candidate;
      if (activate) {
        // First expose identity SDR plus the small EDR request patch, then the
        // boosted picture. This preserves the existing display activation order.
        if (!await copy(2, false)) return;
        await renderer.render(candidate.frame, { activationFrame: false, requestPatch: false });
        if (!current()) return;
      }
      if (!await copy(activate ? 1 : 2)) return;
      this.confirmed = true;
      this.activated = true;
      this.options.onMetric?.('presented', this.snapshot());
    } catch (error) {
      if (generation === this.generation && this.visible) {
        this.fail(String(error?.message || 'hdr_render_failed').slice(0, 80));
      }
    } finally {
      if (generation === this.generation) renderer.discardPreparedFrame();
      close(candidate);
      if (this.inFlight === candidate) this.inFlight = null;
      if (generation === this.generation) this.dispatch();
    }
  }

  surface(visible, boost = this.surfaceBoost || 1) {
    this.surfaceVisible = Boolean(visible);
    this.surfaceBoost = visible ? boost : 1;
    this.options.onSurface?.(this.surfaceVisible, this.surfaceBoost);
  }

  ensureExactProof(epoch, sequence) {
    const current = this.snapshot();
    return current.proofFresh && !current.decorated && current.epoch === Number(epoch) && current.sequence === Number(sequence);
  }

  snapshot() {
    const picture = this.presented;
    const sdr = this.currentSDR;
    const matches = picture && sdr && picture.epoch === sdr.epoch &&
      picture.sequence === sdr.sequence && picture.configGeneration === sdr.configGeneration;
    return { active: this.active, ready: this.ready, surfaceVisible: this.surfaceVisible,
      displayConfirmed: Boolean(this.confirmed && this.visible && matches),
      epoch: picture?.epoch || 0, sequence: picture?.sequence || 0, decorated: Boolean(picture?.decorated),
      proofFresh: Boolean(this.confirmed && this.visible && matches &&
        !picture.retainedResult && age(picture) <= 3000) };
  }

  fail(reason) {
    if (!this.active) return;
    this.suspend();
    this.options.onStatus?.('failed', reason);
  }

  // Stop work without destroying the last displayed surface. Presentation owns
  // its lifetime until a replacement is ready (or the display device is lost).
  suspend() {
    this.generation++;
    clearTimeout(this.initTimer);
    this.initTimer = null;
    this.renderer?.cancelGPUCompletionWaits('renderer_suspended');
    this.renderer?.cancelCompositorSettlementWaits('renderer_suspended');
    close(this.pending);
    this.pending = null;
    // The running sequence releases its own frame in finally after cancellation.
    this.inFlight = null;
    this.active = this.ready = this.confirmed = this.activated = false;
    this.reassertPending = false;
  }

  dispose() {
    this.suspend();
    this.renderer?.dispose();
    this.renderer = null;
    this.currentSDR = this.presented = null;
    this.surface(false);
  }
}
