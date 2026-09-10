import { ClientHDRController, clientHDRCapability, normalizeClientHDRDisplayBoost } from './client-hdr-core.mjs';
import { exactResultMatches } from './exact-result.mjs';
import { MAX_PICTURE_AGE_MS } from './media-session.mjs';

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
    this.visible = true;
    this.regionVisible = true;
    this.ordinal = 0;
    this.generation = 0;
    this.hdrBlocked = false;
  }

  size(width, height) {
    const { canvas } = this.elements;
    if (canvas.width === width && canvas.height === height) return;
    canvas.width = width;
    canvas.height = height;
    this.rendered = null;
    this.restartHDR();
    this.handlers.onLayout?.();
  }

  setPreference(enabled, boost) {
    const changed = this.enabled !== Boolean(enabled);
    const boostChanged = this.boost !== normalizeClientHDRDisplayBoost(boost);
    this.enabled = Boolean(enabled);
    this.boost = normalizeClientHDRDisplayBoost(boost);
    if (!this.enabled) {
      this.controller?.dispose();
      this.controller = null;
      this.surface(false);
    } else if (changed) {
      this.hdrBlocked = false;
      this.restartHDR();
    } else if (boostChanged) {
      this.controller?.setDisplayBoost(this.boost);
      this.seedHDR();
    }
  }

  setVisible(visible, regionVisible = this.regionVisible) {
    const returning = visible && !this.visible;
    const returningRegion = visible && regionVisible && !this.regionVisible;
    this.visible = visible;
    this.regionVisible = regionVisible;
    this.controller?.setDocumentVisible(visible);
    this.controller?.setStreamRegionVisible(regionVisible);
    if (!visible) this.controller?.holdLastPresentation('page_hidden');
    if (returning || (returningRegion && !this.controller)) this.restartHDR();
    else if (returningRegion) this.seedHDR();
  }

  surface(visible) {
    const { hdrCanvas, resultArea, resultImage } = this.elements;
    hdrCanvas.hidden = !this.enabled;
    hdrCanvas.dataset.clientHdrSurface = visible && this.enabled ? 'visible' : 'standby';
    hdrCanvas.setAttribute('aria-hidden', visible && this.enabled ? 'false' : 'true');
    document.body.dataset.experimentalMedia = visible && this.enabled ? 'hdr-client-webgpu-preview' : 'fallback-sdr';
    if (!visible && this.frozen?.displayed) {
      resultArea.dataset.presentation = 'sdr';
      resultImage.hidden = false;
    }
  }

  restartHDR() {
    this.generation++;
    this.controller?.dispose();
    this.controller = null;
    this.surface(false);
    if (!this.enabled || !this.visible || !this.regionVisible || this.hdrBlocked || !clientHDRCapability().supported) return;
    const old = this.elements.hdrCanvas;
    const canvas = old.cloneNode(false);
    canvas.width = this.elements.canvas.width;
    canvas.height = this.elements.canvas.height;
    old.replaceWith(canvas);
    this.elements.hdrCanvas = canvas;
    const generation = this.generation;
    const controller = new ClientHDRController({
      canRevealSurface: () => this.enabled && this.visible && this.regionVisible,
      canReleaseHoldover: () => !this.frozen || Boolean(this.frozen.presenting),
      onSurface: (visible) => { if (generation === this.generation) this.surface(visible); },
      onStatus: (status, reason) => {
        if (generation !== this.generation) return;
        if (status === 'ready') this.seedHDR();
        if (status === 'failed') {
          this.hdrBlocked = true;
          this.surface(false);
          this.handlers.onFailure?.(reason || 'hdr_failed');
        }
      },
      onRecoveryRequest: () => this.seedHDR(),
      onMetric: (event, snapshot) => {
        if (generation !== this.generation) return;
        if (event === 'presented' && snapshot.proofFresh && this.rendered &&
          snapshot.epoch === this.rendered.epoch && snapshot.sequence === this.rendered.sequence) {
          this.handlers.onRendered(this.rendered, true);
          this.handlers.onHDRHealthy?.();
        }
      }
    });
    this.controller = controller;
    controller.setStreamRegionVisible(this.regionVisible);
    controller.start({ canvas, width: canvas.width, height: canvas.height, boost: this.boost });
    this.seedHDR();
  }

  recoverHDR() {
    this.hdrBlocked = false;
    this.restartHDR();
  }

  seedHDR() {
    if (this.frozen || !this.latest || this.handlers.age(this.latest.metadata) > MAX_PICTURE_AGE_MS) return;
    this.offer(this.latest.frame, this.latest.metadata);
  }

  receive(frame, metadata) {
    this.latest?.frame.close();
    this.latest = { frame: frame.clone(), metadata };
    if (this.frozen) return;
    this.offer(frame, metadata);
  }

  offer(frame, metadata) {
    if (!this.visible || this.handlers.age(metadata) > MAX_PICTURE_AGE_MS) return false;
    const generation = this.generation;
    const candidate = { ...metadata, offeredAt: performance.now(),
      visualAgeMillis: this.handlers.age(metadata), presentationOrdinal: this.ordinal + 1 };
    const commit = (ownedFrame) => {
      if (generation !== this.generation || (this.frozen && !samePicture(this.frozen.metadata, metadata))) return false;
      return this.draw(ownedFrame, metadata);
    };
    if (this.controller?.snapshot().ready && this.controller.offerFrame(frame, candidate, { commitSDR: commit })) return true;
    const rendered = this.draw(frame, metadata);
    if (rendered && this.controller) {
      this.controller.noteSDRFrame(rendered);
      this.controller.offerFrame(frame, { ...candidate, ...rendered });
    }
    return Boolean(rendered);
  }

  draw(frame, metadata) {
    if (!this.visible || this.handlers.age(metadata) > MAX_PICTURE_AGE_MS) return false;
    if (this.rendered && this.rendered.epoch === metadata.epoch &&
      this.rendered.configGeneration === metadata.configGeneration && this.rendered.sequence > metadata.sequence) return false;
    const { canvas } = this.elements;
    this.context.drawImage(frame, 0, 0, canvas.width, canvas.height);
    this.rendered = { ...metadata, visualAgeMillis: this.handlers.age(metadata),
      renderedAt: performance.now(), presentationOrdinal: ++this.ordinal };
    this.handlers.onRendered(this.rendered);
    const rendered = this.rendered;
    const generation = this.generation;
    void paint().then(() => {
      if (generation === this.generation && this.visible && this.regionVisible &&
        samePicture(rendered, this.rendered) && this.handlers.age(rendered) <= MAX_PICTURE_AGE_MS &&
        !this.controller?.snapshot().surfaceVisible) this.handlers.onRendered(rendered, true);
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
      metadata, presenting: true, displayed: false };
    this.frozen = frozen;
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
        if (snapshot.proofFresh && snapshot.epoch === metadata.epoch && snapshot.sequence === metadata.sequence) {
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
      this.controller?.holdLastPresentation('control_code_result');
      return true;
    } finally {
      captured.close();
      if (!frozen.displayed && this.frozen === frozen) this.closeResult();
    }
  }

  closeResult() {
    this.frozen = null;
    const { resultArea, resultImage } = this.elements;
    resultArea.hidden = true;
    resultImage.hidden = true;
    resultImage.removeAttribute('src');
    delete resultArea.dataset.presentation;
    document.body.classList.remove('control-code-result-visible');
    if (this.latest && this.handlers.age(this.latest.metadata) <= MAX_PICTURE_AGE_MS) {
      this.draw(this.latest.frame, this.latest.metadata);
      this.seedHDR();
    }
  }

  dispose() {
    this.generation++;
    this.controller?.dispose();
    this.controller = null;
    this.latest?.frame.close();
    this.latest = null;
  }

  clearForColdRestart() {
    this.dispose();
    this.rendered = null;
    this.context.clearRect(0, 0, this.elements.canvas.width, this.elements.canvas.height);
    this.surface(false);
  }
}
