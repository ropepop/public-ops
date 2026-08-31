import { html, reactive } from '@arrow-js/core';
import { renderClientHDRDiagnosticPatches } from './client-hdr-diagnostic.mjs';

const mount = document.getElementById('hdrDiagnosticMount');
if (mount) {
  const knownHDRReferenceURL = 'https://ccameron-chromium.github.io/hdr-jpeg/pescariello0-hlg.avif';
  const mediaQuery = typeof window.matchMedia === 'function'
    ? window.matchMedia('(dynamic-range: high)')
    : null;
  const state = reactive({
    highDynamicRange: Boolean(mediaQuery && mediaQuery.matches),
    dynamicRangeLimit: Boolean(window.CSS && typeof window.CSS.supports === 'function' &&
      window.CSS.supports('dynamic-range-limit', 'no-limit')),
    patchStatus: 'Preparing WebGPU patches…',
    patchEncoding: '—',
    imageStatus: 'Loading the known 10-bit Rec.2020 HLG AVIF reference…'
  });

  html`
    <section class="card" aria-labelledby="capability-heading">
      <h2 id="capability-heading">Browser signals</h2>
      <dl class="status-grid">
        <dt>High dynamic-range media query</dt><dd>${() => state.highDynamicRange ? 'yes' : 'no'}</dd>
        <dt>CSS unrestricted range</dt><dd>${() => state.dynamicRangeLimit ? 'supported' : 'unavailable'}</dd>
        <dt>WebGPU patches</dt><dd>${() => state.patchStatus}</dd>
        <dt>Canvas encoding</dt><dd>${() => state.patchEncoding}</dd>
      </dl>
    </section>
    <section class="card" aria-labelledby="patch-heading">
      <h2 id="patch-heading">WebGPU intended color peaks</h2>
      <p class="muted">Each row requests the same saturated color at 1×, 2×, and 4× output. This checks whether bright colors as well as white enter extended range; measured brightness must still be checked on the physical screen.</p>
      <div class="patch-grid">
        <div class="patch-row-labels" aria-hidden="true"><span>Red</span><span>Green</span><span>Blue</span><span>Cyan</span><span>Magenta</span><span>Yellow</span><span>Orange</span><span>White</span></div>
        <div class="patch-display">
          <canvas id="hdrDiagnosticPatches" width="720" height="480" aria-label="WebGPU red, green, blue, cyan, magenta, yellow, orange, and white patches at one, two, and four times SDR white"></canvas>
          <div class="patch-labels" aria-hidden="true"><span>1×</span><span>2×</span><span>4×</span></div>
        </div>
      </div>
    </section>
    <section class="card" aria-labelledby="image-heading">
      <h2 id="image-heading">Known HDR image comparison</h2>
      <p class="muted">The same known HLG AVIF is shown once limited to SDR and once unrestricted. You can replace it with a known gain-map JPEG, AVIF, HEIF, or HEIC from this device; local files are never uploaded. The source is <a href="https://ccameron-chromium.github.io/hdr-jpeg/" target="_blank" rel="noreferrer">the public HDR JPEG/AVIF test page</a>.</p>
      <input id="hdrDiagnosticFile" type="file" accept="image/jpeg,image/avif,image/heif,image/heic,.jpg,.jpeg,.avif,.heif,.heic">
      <p class="muted" aria-live="polite">${() => state.imageStatus}</p>
      <div class="image-grid">
        <figure><figcaption>Standard range</figcaption><img id="hdrDiagnosticStandardImage" class="reference-standard" src="https://ccameron-chromium.github.io/hdr-jpeg/pescariello0-hlg.avif" alt="Known HLG HDR reference limited to standard range"></figure>
        <figure><figcaption>Unrestricted range</figcaption><img id="hdrDiagnosticHDRImage" class="reference-hdr" src="https://ccameron-chromium.github.io/hdr-jpeg/pescariello0-hlg.avif" alt="Known HLG HDR reference with unrestricted dynamic range"></figure>
      </div>
    </section>
  `(mount);

  document.documentElement.dataset.ticketHdrDiagnosticUi = 'arrow';
  const patchCanvas = document.getElementById('hdrDiagnosticPatches');
  const fileInput = document.getElementById('hdrDiagnosticFile');
  const standardImage = document.getElementById('hdrDiagnosticStandardImage');
  const hdrImage = document.getElementById('hdrDiagnosticHDRImage');
  let diagnostic = null;
  let selectedURL = '';

  const showReference = async (url, status) => {
    standardImage.hidden = true;
    hdrImage.hidden = true;
    standardImage.src = url;
    hdrImage.src = url;
    try {
      await Promise.all([standardImage.decode(), hdrImage.decode()]);
      standardImage.hidden = false;
      hdrImage.hidden = false;
      state.imageStatus = status;
      return true;
    } catch (_) {
      state.imageStatus = 'The HDR reference could not be decoded in this browser.';
      return false;
    }
  };

  Promise.resolve().then(async () => {
    try {
      diagnostic = await renderClientHDRDiagnosticPatches(patchCanvas, { environment: window });
      state.patchEncoding = diagnostic.canvasEncoding;
      state.patchStatus = 'presented';
    } catch (error) {
      state.patchStatus = String(error && error.message || error || 'unavailable').slice(0, 80);
    }
  });
  Promise.resolve().then(() => showReference(
    knownHDRReferenceURL,
    'Known 10-bit Rec.2020 HLG AVIF decoded from the public reference set.'
  ));

  if (mediaQuery && typeof mediaQuery.addEventListener === 'function') {
    mediaQuery.addEventListener('change', (event) => { state.highDynamicRange = Boolean(event.matches); });
  }
  if (fileInput && standardImage && hdrImage) {
    fileInput.addEventListener('change', async () => {
      if (selectedURL) {
        URL.revokeObjectURL(selectedURL);
        selectedURL = '';
      }
      const file = fileInput.files && fileInput.files[0];
      if (!file) {
        await showReference(
          knownHDRReferenceURL,
          'Known 10-bit Rec.2020 HLG AVIF decoded from the public reference set.'
        );
        return;
      }
      selectedURL = URL.createObjectURL(file);
      await showReference(selectedURL, `Decoded locally: ${file.name}`);
    });
  }

  window.addEventListener('pagehide', () => {
    if (diagnostic && typeof diagnostic.dispose === 'function') diagnostic.dispose();
    diagnostic = null;
    if (selectedURL) URL.revokeObjectURL(selectedURL);
    selectedURL = '';
  }, { once: true });
}
