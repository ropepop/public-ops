import { Presentation } from './presentation.mjs';
import { ClientHDRRenderer, CLIENT_HDR_ALLOWED_BOOSTS } from './client-hdr-renderer.mjs';

const result = document.getElementById('result');
const source = document.createElement('canvas');
source.width = 994; source.height = 2046;
const ctx = source.getContext('2d', { alpha: false });
for (const [i, color] of ['#263030', '#ff4646', '#ffffff', '#888888', '#101010', '#ff9200'].entries()) {
  ctx.fillStyle = color; ctx.fillRect(0, i * 341, source.width, 341);
}
const failures = [];
const presentation = new Presentation({
  canvas: document.getElementById('screen'), hdrCanvas: document.getElementById('experimentalMediaCanvas'),
  resultArea: document.getElementById('resultArea'), resultImage: document.getElementById('resultImage')
}, {
  age: metadata => metadata ? performance.now() - metadata.capturedAt : Infinity,
  onRendered() {}, onFailure: reason => failures.push(reason)
});
let sequence = 0;
function picture() {
  const capturedAt = performance.now();
  const frame = new VideoFrame(source, { timestamp: Math.round(capturedAt * 1000) });
  try { presentation.receive(frame, { epoch: 1, sequence: ++sequence, configGeneration: 1, capturedAt }); }
  finally { frame.close(); }
}
function check(condition, message) { if (!condition) throw Error(message); }
async function verifyStableCanvas() {
  const canvas = document.createElement('canvas');
  canvas.style.cssText = 'width:248px;height:511px;dynamic-range-limit:no-limit';
  document.body.append(canvas);
  const renderer = new ClientHDRRenderer();
  const frame = new VideoFrame(source, { timestamp: 0 });
  let readback;
  const timings = [], rounds = [], references = new Map();
  const percentile = (values, fraction) => [...values].sort((a, b) => a - b)[Math.ceil(values.length * fraction) - 1];
  try {
    await renderer.initialize({ canvas, width: source.width, height: source.height, boost: 4 });
    // Test-only read access: capture the actual presented texture before expiry.
    renderer.context.configure({ ...renderer.context.getConfiguration(), usage: GPUTextureUsage.RENDER_ATTACHMENT | GPUTextureUsage.COPY_DST | GPUTextureUsage.COPY_SRC });
    const configure = renderer.context.configure.bind(renderer.context);
    let reconfigurations = 0;
    renderer.context.configure = configuration => { reconfigurations++; configure(configuration); };
    readback = renderer.device.createBuffer({ size: 6 * 256, usage: GPUBufferUsage.COPY_DST | GPUBufferUsage.MAP_READ });
    const resources = ['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].map(key => renderer[key]);
    for (let round = 0; round < 3; round++) {
      const samples = [];
      for (const boost of CLIENT_HDR_ALLOWED_BOOSTS) {
        renderer.setBoost(boost);
        for (let index = 0; index < 10; index++) {
          const start = performance.now();
          await renderer.render(frame);
          const presented = renderer.present();
          const texture = renderer.context.getCurrentTexture(), commands = renderer.device.createCommandEncoder();
          for (let row = 0; row < 6; row++) commands.copyTextureToBuffer(
            { texture, origin: [0, row * 341 + 170, 0] },
            { buffer: readback, offset: row * 256, bytesPerRow: 256 }, [1, 1]);
          renderer.device.queue.submit([commands.finish()]);
          await presented;
          await renderer.waitForCompositorSettlement();
          const elapsed = performance.now() - start;
          if (index >= 2) { samples.push(elapsed); timings.push(elapsed); }
          await readback.mapAsync(GPUMapMode.READ);
          const actual = Array.from(new Uint16Array(readback.getMappedRange())).filter((_, i) => i % 128 < 4);
          readback.unmap();
          check(actual.every((value, i) => i % 4 !== 3 || value === 0x3c00) && actual.some((value, i) => i % 4 !== 3 && value > 0x3c00), 'blank presented texture');
          if (!references.has(boost)) references.set(boost, actual);
          check(actual.every((value, i) => Math.abs(value - references.get(boost)[i]) <= 1), 'update changed canvas pixels');
          check(reconfigurations === 0, 'update reconfigured the visible canvas');
          check(['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].every((key, i) => renderer[key] === resources[i]), 'update replaced GPU resources');
        }
      }
      rounds.push(percentile(samples, 0.5));
    }
    return { samples: timings.length, rounds,
      medianMillis: percentile(timings, 0.5), p95Millis: percentile(timings, 0.95),
      identicalPresentedTexturePixels: true, resourcesReused: true, reconfigurations };
  } finally {
    const context = renderer.context;
    readback?.destroy(); renderer.dispose(); frame.close(); canvas.remove();
    check(!renderer.device && !renderer.stagingTexture && !context?.getConfiguration(), 'presentation resources leaked');
  }
}
async function settled() {
  const deadline = performance.now() + 4000;
  while (presentation.recovering || !presentation.controller?.snapshot().displayConfirmed) {
    if (performance.now() > deadline) throw Error(`presentation deadline: ${failures.at(-1) || 'no presentation'}`);
    await new Promise(requestAnimationFrame);
  }
}
document.getElementById('run').addEventListener('click', async event => {
  event.target.disabled = true;
  failures.length = 0;
  const timings = [];
  try {
    const stableCanvas = await verifyStableCanvas();
    presentation.setPreference(true, 4); picture(); await settled();
    const openingMillis = Number(document.body.dataset.hdrRecoveryMillis);
    for (let i = 0; i < 10; i++) {
      const old = presentation.elements.hdrCanvas;
      const renderer = presentation.controller.renderer;
      presentation.setVisible(false);
      const start = performance.now();
      presentation.setVisible(true);
      const replacement = presentation.controller;
      presentation.setVisible(true); presentation.recoverHDR();
      check(presentation.controller === replacement, 'duplicate return started another renderer');
      check(presentation.holdover?.canvas === old && !renderer.disposed, 'previous surface released early');
      picture(); await settled();
      timings.push(Math.round(performance.now() - start));
      check(!presentation.holdover && renderer.disposed, 'old renderer was not released');
      check(document.querySelectorAll('#experimentalMediaHoldover').length === 0, 'orphaned surface');
      check(presentation.controller.snapshot().proofFresh, 'replacement not fresh');
    }
    const request = { requestId: 'fixture', status: 'succeeded', captureRequired: true,
      resultFrameEpoch: '1', resultMinFrameSequence: String(sequence), resultMarkerRevision: `1:${sequence}` };
    check(await presentation.presentResult(request, () => true), 'exact result rejected');
    presentation.setVisible(false); presentation.setVisible(true); await settled();
    check(presentation.elements.resultArea.dataset.presentation === 'exact-hdr', 'result lost HDR on return');
    check(!presentation.controller.snapshot().proofFresh, 'retained result claimed live authority');
    presentation.closeResult(); picture(); await settled();
    check(failures.length === 0, failures.join(','));
    const sorted = [...timings].sort((a, b) => a - b);
    result.textContent = JSON.stringify({ passed: true, stableCanvas, openingMillis, returns: timings.length,
      medianMillis: sorted[4], p95Millis: sorted[9], timings, frozenResult: 'passed',
      colorSpace: document.body.dataset.hdrColorSpace }, null, 2);
  } catch (error) { result.textContent = JSON.stringify({ passed: false, error: String(error), failures }); }
  finally { event.target.disabled = false; }
});
window.addEventListener('pagehide', () => presentation.dispose());
