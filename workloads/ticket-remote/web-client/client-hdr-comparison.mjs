import { html, reactive } from '@arrow-js/core';
import { ClientHDRRenderer, CLIENT_HDR_ALLOWED_BOOSTS, CLIENT_HDR_DEFAULT_BOOST } from './client-hdr-renderer.mjs';
import { drawHDRContrastFixture } from './client-hdr-contrast-fixture.mjs';

export function mountHDRComparison(mount) {
  const state = reactive({ hdr: false, boost: CLIENT_HDR_DEFAULT_BOOST, busy: false, status: 'Synthetic pale-gray footer.' });
  html`
    <h2>Same-picture contrast comparison</h2>
    <p class="muted">Alternate the same frozen picture in the same position. Compare the palest lettering with its white background at each brightness, including 1× to check ordinary brightness. Use an SDR image for your own comparison; it stays in this page’s memory and is never uploaded or saved.</p>
    <label>Local SDR picture <input id="hdrContrastFile" type="file" accept="image/*" disabled="${() => state.busy}" @change="${(event) => replaceSource(event.target.files?.[0])}"></label>
    <div class="contrast-controls">
      <button id="hdrContrastToggle" type="button" disabled="${() => state.busy}" @click="${() => show(!state.hdr)}">${() => state.hdr ? 'Show SDR' : 'Show HDR'}</button>
      <label>Comparison brightness <select id="hdrContrastBoost" disabled="${() => state.busy}" @change="${(event) => { state.boost = Number(event.target.value); void show(state.hdr); }}">
        ${[1, ...CLIENT_HDR_ALLOWED_BOOSTS].map((boost) => html`<option value="${boost}" selected="${() => state.boost === boost}">${boost}×</option>`)}
      </select></label>
      <button type="button" disabled="${() => state.busy}" @click="${() => { document.getElementById('hdrContrastFile').value = ''; void replaceSource(); }}">Use gray sample</button>
    </div>
    <p class="muted">Compare SDR and HDR at the same brightness, then leave and return to this page. The picture stays in memory while this page is retained.</p>
    <p id="hdrContrastStatus" class="muted" aria-live="polite">${() => `${state.hdr ? `HDR ${state.boost}×` : 'SDR'} · ${state.status}`}</p>
    <div class="contrast-picture">
      <canvas id="hdrContrastSDR" aria-label="Frozen SDR reference"></canvas>
      <canvas id="hdrContrastHDR" aria-label="Same frozen picture in HDR"></canvas>
    </div>
  `(mount);
  const sdr = document.getElementById('hdrContrastSDR');
  const hdr = document.getElementById('hdrContrastHDR');
  let frame = null;
  let renderer = null;
  let generation = 0;

  function pause() {
    generation += 1;
    renderer?.dispose();
    renderer = null;
    hdr.style.opacity = '0';
    state.busy = false;
  }

  function release() {
    pause();
    frame?.close();
    frame = null;
    state.hdr = false;
  }

  async function show(enabled) {
    if (state.busy || !frame || document.hidden) return;
    if (!enabled) {
      hdr.style.opacity = '0';
      state.hdr = false;
      return;
    }
    state.hdr = true;
    state.busy = true;
    const current = generation;
    const currentFrame = frame;
    const currentRenderer = renderer || new ClientHDRRenderer({ environment: window });
    try {
      if (!renderer) {
        renderer = currentRenderer;
        await currentRenderer.initialize({ canvas: hdr, width: sdr.width, height: sdr.height, boost: Math.max(2, state.boost) });
      }
      currentRenderer.setBoost(Math.max(2, state.boost));
      await currentRenderer.render(currentFrame, { activationFrame: true, requestPatch: state.boost > 1 });
      await currentRenderer.present();
      if (current !== generation) return;
      hdr.style.opacity = '1';
      await currentRenderer.waitForCompositorSettlement();
      await currentRenderer.render(currentFrame, { activationFrame: state.boost === 1 });
      await currentRenderer.present();
      await currentRenderer.waitForCompositorSettlement();
      if (current === generation) state.hdr = true;
    } catch (_) {
      if (current === generation) {
        renderer?.dispose();
        renderer = null;
        hdr.style.opacity = '0';
        state.hdr = false;
        state.status = 'HDR could not be presented. SDR is shown; try again with this page visible.';
      }
    } finally {
      if (current === generation) state.busy = false;
    }
  }

  async function replaceSource(file) {
    if (state.busy) return;
    release();
    state.busy = true;
    const current = generation;
    let bitmap = null;
    try {
      if (file) {
        bitmap = await createImageBitmap(file);
        if (current !== generation) return;
        const scale = Math.min(1, 2048 / Math.max(bitmap.width, bitmap.height));
        sdr.width = Math.max(1, Math.round(bitmap.width * scale));
        sdr.height = Math.max(1, Math.round(bitmap.height * scale));
        sdr.getContext('2d', { alpha: false, colorSpace: 'srgb' }).drawImage(bitmap, 0, 0, sdr.width, sdr.height);
      } else drawHDRContrastFixture(sdr);
      frame = new VideoFrame(sdr, { timestamp: 0 });
      state.status = file ? 'Local SDR picture; held only in memory.' : 'Synthetic pale-gray footer.';
    } catch (_) {
      if (current === generation) state.status = 'Picture unavailable. Choose a supported SDR image or use the gray sample.';
    } finally {
      bitmap?.close();
      if (current === generation) state.busy = false;
    }
  }

  void replaceSource();
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) pause();
    else if (frame) void show(state.hdr);
    else void replaceSource();
  });
  window.addEventListener('pagehide', (event) => {
    if (event.persisted) { pause(); return; }
    release();
    sdr.width = sdr.height = 1;
    document.getElementById('hdrContrastFile').value = '';
  });
  window.addEventListener('pageshow', (event) => {
    if (event.persisted) {
      if (frame) void show(state.hdr);
      else void replaceSource();
    }
  });
}
