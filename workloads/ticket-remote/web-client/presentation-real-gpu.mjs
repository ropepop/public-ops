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
const feedback = [];
const presentation = new Presentation({
  canvas: document.getElementById('screen'), hdrCanvas: document.getElementById('experimentalMediaCanvas'),
  resultArea: document.getElementById('resultArea'), resultImage: document.getElementById('resultImage')
}, {
  age: metadata => metadata ? performance.now() - metadata.capturedAt : Infinity,
  onRendered: (metadata, shown) => feedback.push({ ...metadata, shown }), onFailure: reason => failures.push(reason)
});
let sequence = 0;
function picture(input) {
  const capturedAt = performance.now();
  const frame = input || new VideoFrame(source, { timestamp: Math.round(capturedAt * 1000) });
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
async function idle() {
  const deadline = performance.now() + 4000;
  while (presentation.controller?.inFlight || presentation.controller?.pending) {
    check(performance.now() < deadline, 'slider presentation deadline');
    await new Promise(requestAnimationFrame);
  }
}
async function verifyQuietFollowup() {
  await idle();
  const controller = presentation.controller, renderer = controller.renderer;
  const canvas = presentation.elements.hdrCanvas, frozen = presentation.frozen;
  check(presentation.hdrFollowupTimer !== null, 'follow-up completed before fixture instrumentation');
  const resources = ['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].map(key => renderer[key]);
  const configuration = { ...renderer.canvasConfiguration, usage: renderer.canvasConfiguration.usage | GPUTextureUsage.COPY_SRC };
  // Test-only readback permission must survive the real recovery configure call.
  renderer.canvasConfiguration = configuration;
  renderer.context.configure(configuration);
  const configure = renderer.context.configure.bind(renderer.context);
  const present = renderer.present.bind(renderer), render = renderer.render.bind(renderer);
  const samples = [];
  let reconfigurations = 0, identityFrames = 0;
  const readback = renderer.device.createBuffer({ size: 6 * 256, usage: GPUBufferUsage.COPY_DST | GPUBufferUsage.MAP_READ });
  renderer.context.configure = value => {
    check(value === configuration, 'follow-up changed HDR configuration');
    reconfigurations++; configure(value);
  };
  renderer.render = (frame, options) => {
    if (options?.activationFrame) identityFrames++;
    return render(frame, options);
  };
  renderer.present = async options => {
    const done = present(options);
    const texture = renderer.context.getCurrentTexture(), commands = renderer.device.createCommandEncoder();
    for (let row = 0; row < 6; row++) commands.copyTextureToBuffer(
      { texture, origin: [0, row * 341 + 170, 0] },
      { buffer: readback, offset: row * 256, bytesPerRow: 256 }, [1, 1]);
    renderer.device.queue.submit([commands.finish()]);
    await done;
    await readback.mapAsync(GPUMapMode.READ);
    samples.push(Array.from(new Uint16Array(readback.getMappedRange())).filter((_, index) => index % 128 < 4));
    readback.unmap();
  };
  try {
    presentation.seedHDR(); await idle();
    const deadline = performance.now() + 4000;
    while (presentation.hdrFollowupTimer !== null || presentation.hdrFollowupDue ||
      controller.reassertPending || controller.inFlight || controller.pending) {
      check(performance.now() < deadline, 'quiet follow-up deadline');
      await new Promise(requestAnimationFrame);
    }
    check(reconfigurations === 1 && identityFrames === 0, 'follow-up was not one boosted reassertion');
    check(samples.length >= 2 && samples.every(sample => sample.every((value, index) =>
      Math.abs(value - samples[0][index]) <= 1)), 'follow-up changed presented pixels');
    check(samples[0].every((value, index) => index % 4 !== 3 || value === 0x3c00) &&
      samples[0].some((value, index) => index % 4 !== 3 && value > 0x3c00), 'follow-up lost HDR picture');
    check(presentation.controller === controller && presentation.elements.hdrCanvas === canvas &&
      ['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].every((key, index) => renderer[key] === resources[index]),
    'follow-up replaced canvas or GPU resources');
    check(presentation.frozen === frozen, 'follow-up replaced the frozen result');
    return { reconfigurations, identityFrames, identicalPresentedTexturePixels: true, sameCanvasAndResources: true };
  } finally {
    renderer.present = present; renderer.render = render; renderer.context.configure = configure;
    readback.destroy();
  }
}
async function verifySliderComposition() {
  const region = { leftBasisPoints: 1000, topBasisPoints: 3500, rightBasisPoints: 9000, bottomBasisPoints: 4000 };
  const x = source.width * 0.1, y = source.height * 0.35, w = source.width * 0.8, h = source.height * 0.05;
  ctx.fillStyle = '#fff'; ctx.fillRect(0, 0, source.width, source.height);
  // Deliberately square native corners make the sampled replacement observable.
  ctx.fillStyle = '#252b2f'; ctx.fillRect(x, y, w, h);
  picture(); await idle();
  const controller = presentation.controller, renderer = controller.renderer;
  const canvas = presentation.elements.hdrCanvas;
  const resources = ['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].map(key => renderer[key]);
  // Test-only texture read access; no production frame reconfigures the context.
  renderer.context.configure({ ...renderer.context.getConfiguration(), usage: GPUTextureUsage.RENDER_ATTACHMENT | GPUTextureUsage.COPY_DST | GPUTextureUsage.COPY_SRC });
  const configure = renderer.context.configure.bind(renderer.context), present = renderer.present.bind(renderer);
  let reconfigurations = 0, requestRead = false, pixels;
  const completions = [], dragTimings = [];
  const points = [[x + 1, y - 15], [x + 1, y + 1], [x + h / 2, y + h * 0.2], [x + w - h / 2, y + h * 0.2]];
  const readback = renderer.device.createBuffer({ size: points.length * 256, usage: GPUBufferUsage.COPY_DST | GPUBufferUsage.MAP_READ });
  renderer.context.configure = configuration => { reconfigurations++; configure(configuration); };
  renderer.present = async options => {
    const done = present(options), read = requestRead;
    requestRead = false;
    if (read) {
      const commands = renderer.device.createCommandEncoder(), texture = renderer.context.getCurrentTexture();
      points.forEach(([px, py], index) => commands.copyTextureToBuffer(
        { texture, origin: [Math.floor(px), Math.floor(py), 0] },
        { buffer: readback, offset: index * 256, bytesPerRow: 256 }, [1, 1]));
      renderer.device.queue.submit([commands.finish()]);
    }
    await done;
    completions.push(performance.now());
    if (read) {
      await readback.mapAsync(GPUMapMode.READ);
      const values = new Uint16Array(readback.getMappedRange());
      pixels = points.map((_, index) => Array.from(values.slice(index * 128, index * 128 + 4)));
      readback.unmap();
    }
  };
  const same = (left, right) => left.every((value, index) => Math.abs(value - right[index]) <= 1);
  const descriptor = (offset, reducedMotion = true) => ({ region, offset, state: 'ready', reducedMotion });
  const percentile = (values, fraction) => [...values].sort((a, b) => a - b)[Math.ceil(values.length * fraction) - 1];
  try {
    const original = presentation.rendered, feedbackStart = feedback.length;
    presentation.setSlider(descriptor(0, false));
    const start = performance.now();
    while (performance.now() - start < 250) await new Promise(requestAnimationFrame);
    check(presentation.rendered === original && feedback.length === feedbackStart, 'animation changed source authority');
    let sourceUpdates = 0;
    while (performance.now() - start < 2250) {
      if (performance.now() - start >= (sourceUpdates + 1) * 1000) { picture(); sourceUpdates++; }
      await new Promise(requestAnimationFrame);
    }
    presentation.setSlider(descriptor(0)); await idle();
    check(sourceUpdates === 2, 'fixture did not exercise one source frame per second');
    check(feedback.length === feedbackStart + sourceUpdates * 2, 'local animation generated source feedback');
    check(presentation.elements.hdrCanvas === canvas && presentation.controller === controller, 'animation replaced HDR owner');
    check(['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].every((key, index) => renderer[key] === resources[index]), 'slider replaced GPU resources');
    check(reconfigurations === 0, 'slider reconfigured the visible HDR canvas');
    const sourceProof = presentation.rendered, sourceFeedback = feedback.length;
    let leftPixels, rightPixels;
    for (let index = 0; index <= 12; index++) {
      requestRead = true;
      const at = performance.now();
      presentation.setSlider({ ...descriptor(index / 12), state: 'dragging' });
      await idle();
      dragTimings.push(performance.now() - at);
      check(same(pixels[0], pixels[1]), 'replacement backing differs from the neighboring card in the actual HDR texture');
      check(pixels[0].slice(0, 3).some(value => value > 0x3c00), 'slider card lost HDR output');
      if (index >= 3) check(same(pixels[0], pixels[2]), 'shrinking slider did not reveal the HDR card behind the thumb');
      if (index === 0) leftPixels = pixels;
      if (index === 12) rightPixels = pixels;
    }
    check(same(leftPixels[2], rightPixels[3]) && same(rightPixels[0], rightPixels[2]) &&
      !same(leftPixels[2], rightPixels[2]), 'drag did not move the rendered knob');
    check(presentation.rendered === sourceProof && feedback.length === sourceFeedback, 'drag changed source freshness or feedback');
    check(!controller.ensureExactProof(1, sequence), 'decorated pixels were accepted as an exact result');
    requestRead = true;
    const request = { requestId: 'slider-raw', status: 'succeeded', captureRequired: true,
      resultFrameEpoch: '1', resultMinFrameSequence: String(sequence), resultMarkerRevision: `1:${sequence}` };
    check(await presentation.presentResult(request, () => true), 'raw slider result rejected');
    check(!same(pixels[0], pixels[1]) && same(pixels[1], pixels[2]) && same(pixels[2], pixels[3]), 'exact result retained local decoration');
    check(presentation.frozen.metadata.capturedAt === sourceProof.capturedAt, 'raw result changed capture time');
    presentation.setSlider(null); presentation.closeResult(); await idle();

    const coverProof = presentation.rendered, coverFeedback = feedback.length;
    requestRead = true;
    presentation.setSlider({ region, state: 'cover', reducedMotion: true }); await idle();
    const coveredWhite = pixels;
    check(pixels.every(pixel => same(pixel, pixels[0])), 'HDR cover retained a slider or differed from the card');
    check(presentation.rendered === coverProof && feedback.length === coverFeedback, 'cover changed source authority');
    ctx.fillStyle = '#ddd'; ctx.fillRect(0, 0, source.width, source.height);
    ctx.fillStyle = '#c06090'; ctx.fillRect(x, y, w, h);
    requestRead = true; picture(); sourceUpdates++; await idle();
    const updatedCoverProof = presentation.rendered, updatedCoverFeedback = feedback.length;
    check(pixels.every(pixel => same(pixel, pixels[0])) && !same(pixels[0], coveredWhite[0]), 'new source picture did not update the HDR cover');
    check(updatedCoverProof.sequence === coverProof.sequence + 1 && updatedCoverFeedback === coverFeedback + 2,
      'covered source update lost its normal freshness feedback');
    check(!controller.ensureExactProof(1, sequence), 'covered pixels were accepted as an exact result');
    requestRead = true; presentation.setSlider(null); await idle();
    check(!same(pixels[0], pixels[1]) && same(pixels[1], pixels[2]) && same(pixels[2], pixels[3]), 'cover removal did not reveal the updated source marker');
    check(presentation.rendered === updatedCoverProof && feedback.length === updatedCoverFeedback, 'cover removal changed source authority');
    check(presentation.elements.hdrCanvas === canvas && presentation.controller === controller &&
      ['device', 'pipeline', 'paramsBuffer', 'stagingTexture'].every((key, index) => renderer[key] === resources[index]) &&
      reconfigurations === 0, 'cover changed HDR canvas or resources');
    const cover = { backingMatchesCard: true, sourceUpdateVisible: true, rawRestored: true,
      sourceAuthorityUnchanged: true, sameCanvasAndResources: true, reconfigurations };

    const colorSamples = [];
    const half = bits => {
      const exponent = (bits >> 10) & 31, fraction = bits & 1023;
      return (bits & 0x8000 ? -1 : 1) * (exponent ? 2 ** (exponent - 15) * (1 + fraction / 1024) : 2 ** -14 * fraction / 1024);
    };
    for (const [luma, u, v] of [[16, 128, 128], [32, 128, 128], [64, 128, 128], [128, 128, 128],
      [190, 128, 128], [235, 128, 128], [128, 90, 180], [180, 150, 60]]) {
      presentation.setSlider(null); await idle();
      const size = source.width * source.height, planar = new Uint8Array(size * 1.5);
      planar.fill(luma, 0, size); planar.fill(u, size, size * 1.25); planar.fill(v, size * 1.25);
      const frame = new VideoFrame(planar, { format: 'I420', codedWidth: source.width, codedHeight: source.height,
        timestamp: Math.round(performance.now() * 1000),
        colorSpace: { primaries: 'bt709', transfer: 'bt709', matrix: 'bt709', fullRange: false } });
      requestRead = true; picture(frame); await idle();
      const raw = pixels[0].slice(0, 3);
      requestRead = true; presentation.setSlider(descriptor(0)); await idle();
      const composed = pixels[0].slice(0, 3);
      colorSamples.push({ yuv: [luma, u, v], raw: raw.map(half), composed: composed.map(half),
        maxHalfSteps: Math.max(...raw.map((value, index) => Math.abs(value - composed[index]))),
        maxChannelDifference: Math.max(...raw.map((value, index) => Math.abs(half(value) - half(composed[index])))) });
    }
    const yuvColorComparison = { samples: colorSamples,
      maxChannelDifference: Math.max(...colorSamples.map(value => value.maxChannelDifference)),
      withinOneHalfFloatStep: colorSamples.every(value => value.maxHalfSteps <= 1) };
    check(yuvColorComparison.withinOneHalfFloatStep, `outside-slider BT709 source colors changed: ${JSON.stringify(yuvColorComparison)}`);
    presentation.setSlider(null); await idle();
    const intervals = completions.slice(1).map((value, index) => value - completions[index]);
    return { sourceResolution: [source.width, source.height], sourceUpdates, presentations: completions.length,
      medianIntervalMillis: percentile(intervals, 0.5), p95IntervalMillis: percentile(intervals, 0.95),
      dragSamples: dragTimings.length, dragMedianMillis: percentile(dragTimings, 0.5), dragP95Millis: percentile(dragTimings, 0.95),
      backingMatchesCard: true, knobMoved: true, rawExactResult: true, sourceAuthorityUnchanged: true,
      sameCanvasAndResources: true, reconfigurations, cover, yuvColorComparison };
  } finally {
    presentation.setSlider(null);
    await idle();
    renderer.present = present;
    renderer.context.configure = configure;
    readback.destroy();
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
    const quietFollowup = await verifyQuietFollowup();
    const sliderComposition = await verifySliderComposition();
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
    const frozenFollowup = await verifyQuietFollowup();
    check(presentation.elements.resultArea.dataset.presentation === 'exact-hdr', 'result lost HDR on return');
    check(!presentation.controller.snapshot().proofFresh, 'retained result claimed live authority');
    presentation.closeResult(); picture(); await settled();
    check(failures.length === 0, failures.join(','));
    const sorted = [...timings].sort((a, b) => a - b);
    result.textContent = JSON.stringify({ passed: true, stableCanvas, sliderComposition, quietFollowup, frozenFollowup, openingMillis, returns: timings.length,
      medianMillis: sorted[4], p95Millis: sorted[9], timings, frozenResult: 'passed',
      colorSpace: document.body.dataset.hdrColorSpace }, null, 2);
  } catch (error) { result.textContent = JSON.stringify({ passed: false, error: String(error), failures }); }
  finally { event.target.disabled = false; }
});
window.addEventListener('pagehide', () => presentation.dispose());
