import { ClientHDRController, clientHDRCapability, normalizeClientHDRDisplayBoost } from './client-hdr-core.mjs';
import { exactResultMatches } from './exact-result.mjs';
import { MAX_PICTURE_AGE_MS } from './media-session.mjs';
import { paintTicketSlider } from './ticket-slider-painter.mjs';

const paint = () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
const samePicture = (left, right) => left && right && left.epoch === right.epoch &&
  left.sequence === right.sequence && left.configGeneration === right.configGeneration;

export class Presentation {
  constructor(elements, handlers) {
    this.elements = elements;
    this.handlers = handlers;
    this.context = elements.canvas.getContext('2d', { alpha: false });
    this.latest = null;
    this.rendered = null;
    this.frozen = null;
    this.controller = null;
    this.enabled = false;
    this.boost = 4;
    this.visible = !document.hidden;
    this.ordinal = 0;
    this.generation = 0;
    this.hdrBlocked = false;
    this.failure = '';
    this.holdover = null;
    this.displayedHDR = null;
    this.recovering = false;
    this.recoveryStartedAt = 0;
    this.hdrFollowupTimer = null;
    this.hdrFollowupDue = false;
    this.slider = null;
    this.sliderAnimation = null;
    this.composition = null;
    this.backgroundCanvas = null;
    this.backgroundRGB = null;
    this.layout = null;
    this.layoutFallback = false;
  }

  sampleBackground() {
    try {
      const { canvas } = this.elements;
      if (!this.backgroundCanvas) {
        this.backgroundCanvas = document.createElement('canvas');
        this.backgroundCanvas.width = this.backgroundCanvas.height = 1;
      }
      const context = this.backgroundCanvas.getContext('2d', { willReadFrequently: true });
      // Sample the quiet lower-left edge, outside both the ticket and Android bar.
      // Crop the SDR canvas, not VideoFrame (whose source crop WebKit ignores).
      context.drawImage(canvas, Math.floor(canvas.width * 0.01), Math.floor(canvas.height * 0.98), 1, 1, 0, 0, 1, 1);
      this.backgroundRGB = [...context.getImageData(0, 0, 1, 1).data].slice(0, 3);
      if (!this.controller?.surfaceVisible && !this.holdover) this.updateBackground();
    } catch { /* A cosmetic fill must not interrupt ticket presentation. */ }
  }

  updateBackground() {
    if (!this.backgroundRGB) return;
    const color = `rgb(${this.backgroundRGB.join(' ')})`;
    document.documentElement.style.setProperty('--ticket-picture-background', color);
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', color);
  }

  setLayout({ stageWidth, stageHeight, left, top, width, height }) {
    const scale = Math.min(Math.max(1, window.devicePixelRatio || 1), 4096 / stageWidth, 4096 / stageHeight);
    const next = {
      width: Math.max(1, Math.round(stageWidth * scale)),
      height: Math.max(1, Math.round(stageHeight * scale)),
      picture: {
        left: left / stageWidth, top: top / stageHeight,
        width: width / stageWidth, height: height / stageHeight,
        right: (left + Math.max(0, width - 1)) / stageWidth,
        bottom: (top + Math.max(0, height - 15)) / stageHeight
      }
    };
    if (JSON.stringify(next) === JSON.stringify(this.layout)) return;
    this.layout = next;
    this.restartHDR({ layoutChanged: true });
  }

  setSlider(slider) {
    const regionKeys = ['leftBasisPoints', 'topBasisPoints', 'rightBasisPoints', 'bottomBasisPoints',
      'sessionId', 'sessionGeneration', 'contextRevision'];
    const next = slider ? { ...slider, region: Object.fromEntries(regionKeys
      .filter(key => slider.region[key] !== undefined).map(key => [key, slider.region[key]])),
      offset: Math.max(0, Math.min(1, Number(slider.offset) || 0)) } : null;
    if (JSON.stringify(next) === JSON.stringify(this.slider)) return;
    const context = value => value ? JSON.stringify(value.region) : '';
    const contextChanged = context(next) !== context(this.slider);
    if (!this.frozen && contextChanged) this.controller?.holdLastPresentation({ keepProof: true });
    this.slider = next;
    this.stopSliderAnimation();
    // A changed control can invalidate an unfinished source presentation. That
    // source still needs its ordinary proof before animation can use the surface.
    if (contextChanged && this.controller?.active && this.latest && !this.frozen &&
      (!this.controller.confirmed || !samePicture(this.latest.metadata, this.controller.presented)) &&
      this.handlers.age(this.latest.metadata) <= MAX_PICTURE_AGE_MS) this.offer(this.latest.frame, this.latest.metadata);
    else this.repaintSlider();
    this.animateSlider();
  }

  stopSliderAnimation() {
    if (this.sliderAnimation !== null) cancelAnimationFrame(this.sliderAnimation);
    this.sliderAnimation = null;
  }

  animateSlider() {
    if (this.sliderAnimation !== null || !this.slider || this.slider.reducedMotion || !this.visible ||
      this.frozen || !this.latest || this.handlers.age(this.latest.metadata) > MAX_PICTURE_AGE_MS) return;
    this.sliderAnimation = requestAnimationFrame(() => {
      this.sliderAnimation = null;
      this.repaintSlider();
      this.animateSlider();
    });
  }

  repaintSlider() {
    if (!this.visible || this.frozen || !this.latest || !samePicture(this.latest.metadata, this.rendered)) return;
    this.offer(this.latest.frame, this.latest.metadata, { visualOnly: true, restoreRaw: !this.slider });
  }

  compose(frame) {
    if (!this.slider || this.frozen) return frame;
    const { width, height } = this.elements.canvas;
    this.composition ||= document.createElement('canvas');
    if (this.composition.width !== width) this.composition.width = width;
    if (this.composition.height !== height) this.composition.height = height;
    paintTicketSlider(this.composition.getContext('2d', { alpha: false }), frame,
      width, height, this.slider, performance.now());
    return this.composition;
  }

  size(width, height) {
    const { canvas } = this.elements;
    if (canvas.width === width && canvas.height === height) return;
    canvas.width = width;
    canvas.height = height;
    this.rendered = null;
    const generation = this.generation;
    this.handlers.onLayout?.();
    if (this.generation === generation) this.restartHDR();
  }

  setPreference(enabled, boost) {
    const changed = this.enabled !== Boolean(enabled);
    const boostChanged = this.boost !== normalizeClientHDRDisplayBoost(boost);
    this.enabled = Boolean(enabled);
    this.boost = normalizeClientHDRDisplayBoost(boost);
    if (!this.enabled) {
      this.cancelHDRRecovery();
      this.generation++;
      this.controller?.dispose();
      this.controller = null;
      this.releaseHoldover();
      this.displayedHDR = null;
      this.recovering = false;
      this.layoutFallback = false;
      this.hdrBlocked = false;
      this.failure = '';
      delete document.body.dataset.hdrFailure;
      this.surface(false);
    } else if (changed) {
      this.hdrBlocked = false;
      this.failure = '';
      delete document.body.dataset.hdrFailure;
      if (this.visible) this.recoverHDR({ foregroundReturn: true });
      else this.restartHDR();
    } else if (boostChanged) {
      if (this.hdrBlocked) {
        this.hdrBlocked = false;
        this.restartHDR();
        return;
      }
      this.controller?.setDisplayBoost(this.boost);
      this.seedHDR();
    }
  }

  setVisible(visible, { foregroundReturn = false } = {}) {
    const returning = visible && !this.visible;
    this.visible = visible;
    this.controller?.setDocumentVisible(visible);
    if (!visible) {
      this.cancelHDRRecovery();
      this.stopSliderAnimation();
      this.controller?.suspend();
    }
    if (visible && (returning || foregroundReturn)) {
      this.recoverHDR({ foregroundReturn: true });
      this.repaintSlider();
      this.animateSlider();
    }
  }

  surface(visible) {
    const { hdrCanvas, resultArea, resultImage } = this.elements;
    if (visible) this.layoutFallback = false;
    hdrCanvas.hidden = !this.enabled;
    hdrCanvas.dataset.clientHdrSurface = visible && this.enabled ? 'visible' : 'standby';
    hdrCanvas.setAttribute('aria-hidden', visible && this.enabled ? 'false' : 'true');
    document.body.dataset.experimentalMedia = (visible || this.holdover) && this.enabled
      ? 'hdr-client-webgpu-preview' : this.recovering ? 'hdr-recovering' : 'fallback-sdr';
    document.body.dataset.hdrRecovering = String(this.recovering);
    document.body.dataset.hdrLayoutFallback = String(this.layoutFallback);
    if (!this.holdover) this.updateBackground();
    if (!visible && !this.holdover && (this.frozen?.displayed || (this.frozen?.presenting && !resultArea.hidden))) {
      resultArea.dataset.presentation = this.recovering && !this.layoutFallback ? 'recovering' : 'sdr';
      resultImage.hidden = this.recovering && !this.layoutFallback;
    }
  }

  restartHDR({ layoutChanged = false } = {}) {
    if (!this.enabled || this.hdrBlocked) return;
    this.generation++;
    if (layoutChanged) {
      this.layoutFallback = true;
      this.controller?.dispose();
      this.releaseHoldover();
    } else if (this.displayedHDR && !this.holdover) {
      this.controller?.suspend();
      this.holdover = { canvas: this.elements.hdrCanvas, controller: this.controller };
      this.holdover.canvas.id = 'experimentalMediaHoldover';
    } else this.controller?.dispose();
    this.controller = null;
    this.displayedHDR = null;
    this.recovering = this.enabled;
    this.recoveryStartedAt = performance.now();
    document.body.dataset.hdrStatus = 'starting';
    const old = this.elements.hdrCanvas;
    const canvas = old.cloneNode(false);
    canvas.id = 'experimentalMediaCanvas';
    canvas.dataset.clientHdrSurface = 'standby';
    canvas.setAttribute('aria-hidden', 'true');
    canvas.width = this.layout?.width || this.elements.canvas.width;
    canvas.height = this.layout?.height || this.elements.canvas.height;
    if (old === this.holdover?.canvas) old.before(canvas);
    else old.replaceWith(canvas);
    this.elements.hdrCanvas = canvas;
    this.surface(false);
    if (!clientHDRCapability().supported) {
      this.fallbackHDR('hdr_unsupported');
      return;
    }
    if (!this.visible || this.hdrBlocked) return;
    const generation = this.generation;
    const controller = new ClientHDRController({
      canRevealSurface: () => this.enabled && this.visible,
      canReleaseHoldover: (candidate) => Boolean(candidate.visualOnly
        ? !this.frozen && !this.holdover && samePicture(candidate, this.rendered) &&
          (candidate.restoreRaw || this.handlers.age(candidate) <= MAX_PICTURE_AGE_MS)
        : candidate.retainedResult
        ? this.frozen?.displayed && samePicture(this.frozen.metadata, candidate)
        : this.handlers.age(candidate) <= MAX_PICTURE_AGE_MS &&
          (!this.frozen || (this.frozen.presenting && samePicture(this.frozen.metadata, candidate)))),
      onSurface: (visible, boost) => { if (generation === this.generation) this.surface(visible, boost); },
      onStatus: (status, reason) => {
        if (generation !== this.generation) return;
        document.body.dataset.hdrStatus = status;
        if (reason) document.body.dataset.hdrFailure = reason;
        if (status === 'ready') this.seedHDR();
        if (status === 'failed') {
          this.fallbackHDR(reason || 'hdr_failed');
        }
      },
      onMetric: (event, snapshot) => {
        if (generation !== this.generation) return;
        if (event !== 'presented' || !snapshot.displayConfirmed) return;
        if (this.recovering) {
          document.body.dataset.hdrRecoveryMillis = String(Math.round(performance.now() - this.recoveryStartedAt));
        }
        document.body.dataset.hdrColorSpace = controller.renderer.encodeOutput ? 'srgb' : 'srgb-linear';
        this.displayedHDR = this.frozen?.metadata || this.rendered;
        this.recovering = false;
        this.failure = '';
        delete document.body.dataset.hdrFailure;
        this.releaseHoldover();
        this.surface(true);
        if (this.frozen?.displayed) {
          this.elements.resultArea.dataset.presentation = 'exact-hdr';
          this.elements.resultImage.hidden = true;
        }
        this.handlers.onHDRHealthy?.();
        if (snapshot.proofFresh && this.rendered &&
          snapshot.epoch === this.rendered.epoch && snapshot.sequence === this.rendered.sequence) {
          this.handlers.onRendered(this.rendered, true);
        }
        // Input received during activation cannot animate the surface yet. Apply
        // its latest state now without submitting the source for another proof.
        if (this.slider) this.repaintSlider();
        this.followupHDR();
      }
    });
    this.controller = controller;
    controller.start({ canvas, width: canvas.width, height: canvas.height, boost: this.boost,
      picture: this.layout?.picture });
    this.seedHDR();
  }

  recoverHDR({ foregroundReturn = false } = {}) {
    if (!this.visible || !this.enabled) return;
    if (foregroundReturn) {
      this.cancelHDRRecovery();
      this.hdrFollowupTimer = setTimeout(() => {
        this.hdrFollowupTimer = null;
        this.hdrFollowupDue = true;
        this.followupHDR();
      }, 1000);
      this.hdrBlocked = false;
      this.failure = '';
      delete document.body.dataset.hdrFailure;
    } else if (this.hdrBlocked || (this.recovering && this.controller?.active)) return;
    this.restartHDR();
  }

  cancelHDRRecovery() {
    clearTimeout(this.hdrFollowupTimer);
    this.hdrFollowupTimer = null;
    this.hdrFollowupDue = false;
    if (this.controller) this.controller.reassertPending = false;
  }

  followupHDR() {
    if (!this.hdrFollowupDue || !this.visible || !this.enabled) return;
    // A slow initial activation owns the surface until it settles or fails.
    if (this.recovering && this.controller?.active) return;
    this.hdrFollowupDue = false;
    if (this.controller?.active && this.controller.activated) {
      this.controller.reassertHDR();
      this.seedHDR();
    } else {
      this.hdrBlocked = false;
      this.failure = '';
      delete document.body.dataset.hdrFailure;
      this.restartHDR();
    }
  }

  fallbackHDR(reason) {
    this.generation++;
    this.hdrBlocked = true;
    this.failure = reason;
    this.recovering = false;
    this.displayedHDR = null;
    this.controller?.dispose();
    this.controller = null;
    this.releaseHoldover();
    // Prepare the latest ordinary picture before revealing it. A frozen code
    // already has its exact SDR image; never replace it with the live stream.
    if (!this.frozen && this.latest) this.draw(this.compose(this.latest.frame), this.latest.metadata,
      { visualOnly: samePicture(this.latest.metadata, this.rendered) });
    document.body.dataset.hdrStatus = 'failed';
    document.body.dataset.hdrFailure = reason;
    this.surface(false);
    this.handlers.onFailure?.(reason);
    this.followupHDR();
  }

  releaseHoldover() {
    if (!this.holdover) return;
    this.holdover.controller?.dispose();
    this.holdover.canvas.remove();
    this.holdover = null;
  }

  visiblePicture() {
    if (this.enabled && this.recovering && !this.layoutFallback) return null;
    return this.enabled && this.displayedHDR ? this.displayedHDR : this.rendered;
  }

  seedHDR() {
    if (this.frozen) {
      if (this.frozen.displayed) {
        const frozen = this.frozen;
        this.controller?.offerFrame(frozen.frame, frozen.metadata, {
          retainedResult: true,
          commitSDR: () => this.frozen === frozen ? frozen.metadata : false
        });
      }
      return;
    }
    if (!this.latest || this.handlers.age(this.latest.metadata) > MAX_PICTURE_AGE_MS) return;
    this.offer(this.latest.frame, this.latest.metadata);
  }

  receive(frame, metadata) {
    this.latest?.frame.close();
    this.latest = { frame: frame.clone(), metadata };
    if (this.frozen) return;
    this.offer(frame, metadata);
    this.animateSlider();
  }

  offer(frame, metadata, { visualOnly = false, restoreRaw = false } = {}) {
    if (!this.visible || (!restoreRaw && this.handlers.age(metadata) > MAX_PICTURE_AGE_MS)) return false;
    const generation = this.generation;
    const candidate = { ...metadata, offeredAt: performance.now(),
      visualAgeMillis: this.handlers.age(metadata), presentationOrdinal: this.ordinal + 1 };
    const commit = (ownedFrame) => {
      if (generation !== this.generation || (this.frozen && !samePicture(this.frozen.metadata, metadata))) return false;
      return this.draw(ownedFrame, metadata, { visualOnly, restoreRaw });
    };
    const source = this.compose(frame);
    const decorated = source !== frame;
    // A canvas is reused for SDR composition; only HDR needs an immutable frame
    // snapshot while its asynchronous rendering owns a clone.
    let hdrFrame = frame;
    try {
      if (source !== frame && this.controller?.active) {
        try { hdrFrame = new VideoFrame(source, { timestamp: frame.timestamp || 0 }); }
        catch {
          this.controller.fail('slider_frame_conversion_failed');
          return false;
        }
      }
      const options = { commitSDR: commit, visualOnly, restoreRaw, decorated };
      if (this.controller?.snapshot().ready && this.controller.offerFrame(hdrFrame, candidate, options)) return true;
      const rendered = this.draw(source, metadata, { visualOnly, restoreRaw });
      if (rendered && this.controller && !visualOnly) {
        this.controller.noteSDRFrame(rendered);
        this.controller.offerFrame(hdrFrame, { ...candidate, ...rendered }, { decorated });
      }
      return Boolean(rendered);
    } finally {
      if (hdrFrame !== frame) hdrFrame.close();
    }
  }

  draw(frame, metadata, { visualOnly = false, restoreRaw = false } = {}) {
    if (!this.visible || (!restoreRaw && this.handlers.age(metadata) > MAX_PICTURE_AGE_MS)) return false;
    if (visualOnly && (this.frozen || !samePicture(metadata, this.rendered))) return false;
    if (this.rendered && this.rendered.epoch === metadata.epoch &&
      this.rendered.configGeneration === metadata.configGeneration && this.rendered.sequence > metadata.sequence) return false;
    const { canvas } = this.elements;
    this.context.drawImage(frame, 0, 0, canvas.width, canvas.height);
    if (visualOnly) return this.rendered;
    this.sampleBackground();
    this.rendered = { ...metadata, visualAgeMillis: this.handlers.age(metadata),
      renderedAt: performance.now(), presentationOrdinal: ++this.ordinal };
    this.handlers.onRendered(this.rendered);
    const rendered = this.rendered;
    const generation = this.generation;
    void paint().then(() => {
      if (generation === this.generation && this.visible &&
        samePicture(rendered, this.rendered) && this.handlers.age(rendered) <= MAX_PICTURE_AGE_MS &&
        !this.holdover && !this.recovering && !this.controller?.snapshot().surfaceVisible) this.handlers.onRendered(rendered, true);
    });
    return this.rendered;
  }

  async presentResult(request, isCurrent) {
    if (this.frozen || !this.visible || !this.latest ||
      !exactResultMatches(request, this.latest.metadata.epoch, this.latest.metadata.sequence) ||
      this.handlers.age(this.latest.metadata) > MAX_PICTURE_AGE_MS) return false;
    const captured = this.latest.frame.clone();
    const metadata = { ...this.latest.metadata };
    const frozen = { requestId: request.requestId, revision: request.resultMarkerRevision,
      metadata, frame: captured, presenting: true, displayed: false };
    this.frozen = frozen;
    this.stopSliderAnimation();
    // A decorated stream frame can have the same sequence as the requested
    // raw result. Fence it before asking for proof of the exact captured pixels.
    this.controller?.holdLastPresentation();
    const { canvas, resultArea, resultImage } = this.elements;
    try {
      const image = document.createElement('canvas');
      image.width = canvas.width;
      image.height = canvas.height;
      image.getContext('2d', { alpha: false }).drawImage(captured, 0, 0, image.width, image.height);
      resultImage.src = image.toDataURL('image/png');
      await resultImage.decode();
      if (!isCurrent(request) || this.frozen !== frozen || !this.visible) return false;
      this.offer(captured, metadata);
      // The renderer reports completed presentation. Do not ask it to prove an
      // exact result before that picture has actually reached the surface.
      const deadline = performance.now() + 2000;
      let exactHDR = false;
      while (this.enabled && this.controller?.snapshot().active && performance.now() < deadline) {
        const snapshot = this.controller.snapshot();
        if (snapshot.proofFresh && !snapshot.decorated && snapshot.epoch === metadata.epoch && snapshot.sequence === metadata.sequence) {
          exactHDR = this.controller.ensureExactProof(metadata.epoch, metadata.sequence);
          break;
        }
        if (!isCurrent(request) || this.frozen !== frozen || !this.visible) return false;
        await paint();
      }
      if (!isCurrent(request) || this.frozen !== frozen || !this.visible) return false;
      resultImage.hidden = exactHDR;
      resultArea.dataset.presentation = exactHDR ? 'exact-hdr' : 'sdr';
      resultArea.dataset.status = 'succeeded';
      resultArea.hidden = false;
      document.body.classList.add('control-code-result-visible');
      window.scrollTo({ top: 0, behavior: 'instant' });
      await paint();
      if (!isCurrent(request) || this.frozen !== frozen || !this.visible ||
        resultArea.getBoundingClientRect().width <= 0) return false;
      frozen.displayed = true;
      frozen.presenting = false;
      this.handlers.onRendered(metadata, true);
      this.controller?.holdLastPresentation();
      return true;
    } finally {
      if (!frozen.displayed && this.frozen === frozen) this.closeResult();
    }
  }

  closeResult() {
    this.controller?.holdLastPresentation();
    this.frozen?.frame.close();
    this.frozen = null;
    const { resultArea, resultImage } = this.elements;
    resultArea.hidden = true;
    resultImage.hidden = true;
    resultImage.removeAttribute('src');
    delete resultArea.dataset.presentation;
    document.body.classList.remove('control-code-result-visible');
    if (this.latest && this.handlers.age(this.latest.metadata) <= MAX_PICTURE_AGE_MS) {
      this.draw(this.compose(this.latest.frame), this.latest.metadata);
      this.seedHDR();
      this.animateSlider();
    } else {
      // A dismissed/expired result is not a live holdover. Erase it even when
      // the source has not supplied a replacement picture yet.
      this.displayedHDR = null;
      this.releaseHoldover();
      this.rendered = null;
      this.context.clearRect(0, 0, this.elements.canvas.width, this.elements.canvas.height);
      this.restartHDR();
    }
  }

  dispose() {
    this.cancelHDRRecovery();
    this.generation++;
    this.stopSliderAnimation();
    this.controller?.dispose();
    this.controller = null;
    this.releaseHoldover();
    this.frozen?.frame.close();
    this.frozen = null;
    this.displayedHDR = null;
    this.recovering = false;
    this.latest?.frame.close();
    this.latest = null;
    this.composition?.remove();
    this.composition = null;
    this.backgroundCanvas?.remove();
    this.backgroundCanvas = null;
  }

  clearForColdRestart() {
    this.dispose();
    this.rendered = null;
    this.context.clearRect(0, 0, this.elements.canvas.width, this.elements.canvas.height);
    this.backgroundRGB = null;
    document.documentElement.style.removeProperty('--ticket-picture-background');
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', '#020304');
    this.surface(false);
  }
}
