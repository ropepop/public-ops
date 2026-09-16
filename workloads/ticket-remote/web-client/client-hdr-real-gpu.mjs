import {
  CLIENT_HDR_ALLOWED_BOOSTS,
  CLIENT_HDR_CANVAS_FORMAT,
  CLIENT_HDR_FALLBACK_COLOR_SPACE,
  CLIENT_HDR_INTERNAL_IDENTITY_BOOST,
  CLIENT_HDR_LINEAR_COLOR_SPACE,
  CLIENT_HDR_SHADER
} from './client-hdr-renderer.mjs';
import { SLIDER_WAVE_SHADER } from './slider-hdr-wave.mjs';
import { drawHDRContrastFixture, HDR_CONTRAST_GRAYS } from './client-hdr-contrast-fixture.mjs';

async function checkWave(device, path) {
  const canvas = document.createElement('canvas');
  canvas.width = 3; canvas.height = 1;
  const context = canvas.getContext('webgpu');
  const buffers = [];
  try {
    context.configure({ device, format: CLIENT_HDR_CANVAS_FORMAT, colorSpace: path.colorSpace,
      alphaMode: 'premultiplied', toneMapping: { mode: 'extended' },
      usage: GPUTextureUsage.RENDER_ATTACHMENT | GPUTextureUsage.COPY_SRC });
    if (context.getConfiguration().alphaMode !== 'premultiplied') throw new Error('wave_alpha_mode');
    const module = device.createShaderModule({ code: SLIDER_WAVE_SHADER });
    const pipeline = await device.createRenderPipelineAsync({ layout: 'auto',
      vertex: { module, entryPoint: 'vertexMain' },
      fragment: { module, entryPoint: 'fragmentMain', targets: [{ format: CLIENT_HDR_CANVAS_FORMAT }] } });
    const output = [];
    for (const boost of CLIENT_HDR_ALLOWED_BOOSTS) {
      const uniform = device.createBuffer({ size: 16, usage: GPUBufferUsage.UNIFORM | GPUBufferUsage.COPY_DST });
      const readback = device.createBuffer({ size: 256, usage: GPUBufferUsage.MAP_READ | GPUBufferUsage.COPY_DST });
      buffers.push(uniform, readback);
      device.queue.writeBuffer(uniform, 0, new Float32Array([boost, 3, 1, path.encoded ? 1 : 0]));
      const group = device.createBindGroup({ layout: pipeline.getBindGroupLayout(0), entries: [{ binding: 0, resource: { buffer: uniform } }] });
      const texture = context.getCurrentTexture(), commands = device.createCommandEncoder();
      const pass = commands.beginRenderPass({ colorAttachments: [{ view: texture.createView(), loadOp: 'clear', storeOp: 'store', clearValue: { r: 0, g: 0, b: 0, a: 0 } }] });
      pass.setPipeline(pipeline); pass.setBindGroup(0, group); pass.draw(3); pass.end();
      commands.copyTextureToBuffer({ texture }, { buffer: readback, bytesPerRow: 256 }, [3, 1]);
      device.queue.submit([commands.finish()]);
      await readback.mapAsync(GPUMapMode.READ);
      const values = Array.from(new Uint16Array(readback.getMappedRange()).slice(0, 12), halfToFloat);
      readback.unmap();
      const alpha = values[7], white = values[4] / alpha;
      if (!closeEnough(alpha, 0.19, 0.001) || !closeEnough(white, path.encoded ? extendedSrgb(boost) : boost) ||
          values[3] !== 0 || values[11] !== 0) throw new Error('wave_gradient_readback');
      output.push({ boost, alpha, white });
    }
    return output;
  } finally {
    for (const buffer of buffers) buffer.destroy();
    context.unconfigure();
  }
}

const LEVELS = Object.freeze([CLIENT_HDR_INTERNAL_IDENTITY_BOOST, ...CLIENT_HDR_ALLOWED_BOOSTS]);
const SOURCE_WIDTH = 360;
const SOURCE_HEIGHT = 300;
const ROW_BYTES = Math.ceil(SOURCE_WIDTH * 8 / 256) * 256;
const SOURCE_SAMPLES = Object.freeze([
  { label: 'dark-gray', encoded: [64, 64, 64] },
  { label: 'light-gray', encoded: [160, 160, 160] },
  { label: 'black', encoded: [0, 0, 0] },
  { label: 'ticket-near-black', encoded: [16, 8, 4] },
  { label: 'ticket-mid-gray', encoded: [128, 128, 128] },
  { label: 'ticket-red', encoded: [141, 44, 37] },
  { label: 'ticket-orange', encoded: [230, 130, 6] },
  { label: 'ticket-green', encoded: [50, 180, 80] },
  { label: 'ticket-blue', encoded: [50, 100, 200] },
  { label: 'red', encoded: [255, 0, 0] },
  { label: 'green', encoded: [0, 255, 0] },
  { label: 'blue', encoded: [0, 0, 255] },
  { label: 'cyan', encoded: [0, 255, 255] },
  { label: 'magenta', encoded: [255, 0, 255] },
  { label: 'yellow', encoded: [255, 255, 0] },
  { label: 'orange', encoded: [255, 128, 0] },
  { label: 'mixed-color', encoded: [64, 128, 224] },
  { label: 'white', encoded: [255, 255, 255] },
  ...HDR_CONTRAST_GRAYS.map((gray) => ({ label: `footer-gray-${gray}`, encoded: [gray, gray, gray] }))
]);
const MUST_ENTER_EDR_AT_SIX = new Set(['ticket-red', 'ticket-orange', 'ticket-green', 'ticket-blue']);

class UnsupportedPathError extends Error {}

function halfToFloat(bits) {
  const sign = (bits & 0x8000) ? -1 : 1;
  const exponent = (bits >>> 10) & 0x1f;
  const fraction = bits & 0x03ff;
  if (exponent === 0) return sign * 2 ** -14 * (fraction / 1024);
  if (exponent === 0x1f) return fraction ? Number.NaN : sign * Number.POSITIVE_INFINITY;
  return sign * 2 ** (exponent - 15) * (1 + fraction / 1024);
}

function extendedSrgb(linear) {
  return linear <= 0.0031308 ? linear * 12.92 : 1.055 * linear ** (1 / 2.4) - 0.055;
}

function extendedSrgbToLinear(encoded) {
  return encoded <= 0.04045 ? encoded / 12.92 : ((encoded + 0.055) / 1.055) ** 2.4;
}

function srgbByteToLinear(byte) {
  const encoded = byte / 255;
  return encoded <= 0.04045 ? encoded / 12.92 : ((encoded + 0.055) / 1.055) ** 2.4;
}

function closeEnough(actual, expected, tolerance = 0.02) {
  return Number.isFinite(actual) && Math.abs(actual - expected) <= tolerance;
}

function compilationLocation(message) {
  const line = Number(message && message.lineNum);
  const column = Number(message && message.linePos);
  const offset = Number(message && message.offset);
  const length = Number(message && message.length);
  return {
    line: Number.isFinite(line) && line > 0 ? line : null,
    column: Number.isFinite(column) && column > 0 ? column : null,
    offset: Number.isFinite(offset) && offset >= 0 ? offset : null,
    length: Number.isFinite(length) && length >= 0 ? length : null
  };
}

function compilationFailure(message) {
  const location = compilationLocation(message);
  const label = location.line !== null
    ? `${location.line}:${location.column === null ? 1 : location.column}`
    : 'unknown-location';
  const error = new Error(`shader_compilation_failed:${label}:${message && message.message || message}`);
  error.compilationLocation = location;
  return error;
}

function configuredExactly(context, colorSpace) {
  if (!context || typeof context.getConfiguration !== 'function') return false;
  const configuration = context.getConfiguration();
  return Boolean(configuration &&
    configuration.format === CLIENT_HDR_CANVAS_FORMAT &&
    configuration.colorSpace === colorSpace &&
    configuration.toneMapping && configuration.toneMapping.mode === 'extended');
}

function makeSampleFrame() {
  if (typeof VideoFrame !== 'function') throw new UnsupportedPathError('video_frame_unavailable');
  const source = document.createElement('canvas');
  drawHDRContrastFixture(source);
  const context = source.getContext('2d', { alpha: false, colorSpace: 'srgb' });
  if (!context) throw new UnsupportedPathError('source_canvas_unavailable');
  const pixels = context.createImageData(SOURCE_SAMPLES.length, 1);
  SOURCE_SAMPLES.forEach((sample, index) => {
    const offset = index * 4;
    pixels.data[offset] = sample.encoded[0];
    pixels.data[offset + 1] = sample.encoded[1];
    pixels.data[offset + 2] = sample.encoded[2];
    pixels.data[offset + 3] = 255;
  });
  context.putImageData(pixels, 0, SOURCE_HEIGHT - 1);
  return {
    frame: new VideoFrame(source, { timestamp: 0 }),
    pixels: context.getImageData(0, 0, SOURCE_WIDTH, SOURCE_HEIGHT).data
  };
}

async function runPath(path) {
  const canvas = document.createElement('canvas');
  canvas.width = SOURCE_WIDTH;
  canvas.height = SOURCE_HEIGHT * LEVELS.length;
  canvas.dataset.path = path.label;
  canvas.style.setProperty('dynamic-range-limit', 'no-limit');
  document.getElementById('surfaces').append(canvas);
  const context = canvas.getContext('webgpu');
  if (!context) throw new Error('webgpu_canvas_unavailable');
  const adapter = await navigator.gpu.requestAdapter();
  if (!adapter) throw new UnsupportedPathError('webgpu_adapter_unavailable');
  const device = await adapter.requestDevice();
  const paramsBuffers = [];
  let readbackBuffer = null;
  let frame = null;
  try {
    try {
      context.configure({
        device,
        format: CLIENT_HDR_CANVAS_FORMAT,
        usage: GPUTextureUsage.RENDER_ATTACHMENT | GPUTextureUsage.COPY_SRC,
        alphaMode: 'opaque',
        colorSpace: path.colorSpace,
        toneMapping: { mode: 'extended' }
      });
      if (!configuredExactly(context, path.colorSpace)) throw new Error('configuration_mismatch');
    } catch (error) {
      throw new UnsupportedPathError(String(error && error.message || error));
    }

    const module = device.createShaderModule({ code: CLIENT_HDR_SHADER });
    if (typeof module.getCompilationInfo === 'function') {
      const info = await module.getCompilationInfo();
      const shaderError = info.messages.find((message) => message.type === 'error');
      if (shaderError) throw compilationFailure(shaderError);
    }
    const pipeline = await device.createRenderPipelineAsync({
      layout: 'auto',
      vertex: { module, entryPoint: 'vertexMain' },
      fragment: { module, entryPoint: 'fragmentMain', targets: [{ format: CLIENT_HDR_CANVAS_FORMAT }] },
      primitive: { topology: 'triangle-list' }
    });
    const sampler = device.createSampler({ minFilter: 'linear', magFilter: 'linear' });
    const source = makeSampleFrame();
    frame = source.frame;
    const externalTexture = device.importExternalTexture({ source: frame, colorSpace: CLIENT_HDR_FALLBACK_COLOR_SPACE });
    const bindGroups = LEVELS.map((level) => {
      const paramsBuffer = device.createBuffer({
        size: 16,
        usage: GPUBufferUsage.UNIFORM | GPUBufferUsage.COPY_DST
      });
      paramsBuffers.push(paramsBuffer);
      device.queue.writeBuffer(paramsBuffer, 0, new Float32Array([level, path.encoded ? 1 : 0, 0, 0]));
      return device.createBindGroup({
        layout: pipeline.getBindGroupLayout(0),
        entries: [
          { binding: 0, resource: externalTexture },
          { binding: 1, resource: sampler },
          { binding: 2, resource: { buffer: paramsBuffer } }
        ]
      });
    });
    readbackBuffer = device.createBuffer({
      size: ROW_BYTES * canvas.height,
      usage: GPUBufferUsage.COPY_DST | GPUBufferUsage.MAP_READ
    });

    device.pushErrorScope('validation');
    const encoder = device.createCommandEncoder();
    const texture = context.getCurrentTexture();
    const view = texture.createView();
    bindGroups.forEach((bindGroup, index) => {
      const pass = encoder.beginRenderPass({
        colorAttachments: [{
          view,
          clearValue: { r: 0, g: 0, b: 0, a: 1 },
          loadOp: index === 0 ? 'clear' : 'load',
          storeOp: 'store'
        }]
      });
      pass.setPipeline(pipeline);
      pass.setBindGroup(0, bindGroup);
      pass.setViewport(0, index * SOURCE_HEIGHT, SOURCE_WIDTH, SOURCE_HEIGHT, 0, 1);
      pass.setScissorRect(0, index * SOURCE_HEIGHT, SOURCE_WIDTH, SOURCE_HEIGHT);
      pass.draw(3);
      pass.end();
    });
    encoder.copyTextureToBuffer(
      { texture },
      { buffer: readbackBuffer, bytesPerRow: ROW_BYTES, rowsPerImage: canvas.height },
      [SOURCE_WIDTH, canvas.height, 1]
    );
    device.queue.submit([encoder.finish()]);
    frame.close();
    frame = null;
    await device.queue.onSubmittedWorkDone();
    const validationError = await device.popErrorScope();
    if (validationError) throw validationError;

    await readbackBuffer.mapAsync(GPUMapMode.READ);
    const data = new DataView(readbackBuffer.getMappedRange());
    const pixels = LEVELS.map((_level, row) => SOURCE_SAMPLES.map((_sample, column) => {
      const offset = ((row + 1) * SOURCE_HEIGHT - 1) * ROW_BYTES + column * 8;
      return [0, 2, 4, 6].map((channelOffset) => halfToFloat(data.getUint16(offset + channelOffset, true)));
    }));
    pixels.forEach((row, levelIndex) => {
      const level = LEVELS[levelIndex];
      row.forEach((pixel, sampleIndex) => {
        const sample = SOURCE_SAMPLES[sampleIndex];
        const sourceLinearRGB = sample.encoded.map(srgbByteToLinear);
        const mappedLinearRGB = sourceLinearRGB.map((channel) => channel * level);
        for (let channel = 0; channel < 3; channel += 1) {
          const expected = path.encoded ? extendedSrgb(mappedLinearRGB[channel]) : mappedLinearRGB[channel];
          if (!closeEnough(pixel[channel], expected, 0.025)) {
            throw new Error(`float_output_mismatch:${path.label}:${levelIndex}:${sampleIndex}:${channel}:${pixel[channel]}`);
          }
        }
        if (!closeEnough(pixel[3], 1, 0.002)) {
          throw new Error(`alpha_output_mismatch:${path.label}:${levelIndex}:${sampleIndex}`);
        }
        const actualLinearRGB = path.encoded
          ? pixel.slice(0, 3).map(extendedSrgbToLinear)
          : pixel.slice(0, 3);
        const sourcePeak = Math.max(...sourceLinearRGB);
        const outputPeak = Math.max(...actualLinearRGB);
        if (sourcePeak === 0) {
          if (!actualLinearRGB.every((channel) => closeEnough(channel, 0, 0.002))) {
            throw new Error(`black_floor_lifted:${path.label}:${levelIndex}:${sample.label}`);
          }
        } else if (level === 1) {
          if (!actualLinearRGB.every((channel, index) => closeEnough(channel, sourceLinearRGB[index], 0.025))) {
            throw new Error(`identity_changed:${path.label}:${sample.label}`);
          }
        } else {
          if (!(outputPeak > sourcePeak)) {
            throw new Error(`non_black_color_not_lifted:${path.label}:${level}:${sample.label}`);
          }
          if (outputPeak > level + 0.05) {
            throw new Error(`selected_peak_exceeded:${path.label}:${level}:${sample.label}`);
          }
          if (level === 6 && MUST_ENTER_EDR_AT_SIX.has(sample.label) && !(outputPeak > 1)) {
            throw new Error(`ticket_color_did_not_enter_edr:${path.label}:${sample.label}`);
          }
        }
      });
      if (!(row[1][0] > row[0][0])) throw new Error(`gray_separation_lost:${path.label}:${levelIndex}`);
    });
    // Independent contrast oracle: after dividing by measured white, every
    // source pixel must match SDR, including antialiased 12px lettering.
    // This checks external-texture conversion as well as the production shader.
    let maxRelativeError = 0;
    let maxSourceByteError = 0;
    for (let row = 0; row < LEVELS.length; row += 1) {
      const white = pixels[row][17][0];
      const whiteLinear = path.encoded ? extendedSrgbToLinear(white) : white;
      if (!closeEnough(whiteLinear, LEVELS[row], 0.015)) throw new Error(`white_peak_changed:${path.label}:${row}`);
      for (let y = 0; y < SOURCE_HEIGHT; y += 1) {
        for (let x = 0; x < SOURCE_WIDTH; x += 1) {
          for (let channel = 0; channel < 3; channel += 1) {
            const offset = (row * SOURCE_HEIGHT + y) * ROW_BYTES + x * 8 + channel * 2;
            const value = halfToFloat(data.getUint16(offset, true));
            const linear = path.encoded ? extendedSrgbToLinear(value) : value;
            const expected = srgbByteToLinear(source.pixels[(y * SOURCE_WIDTH + x) * 4 + channel]);
            const error = Math.abs(linear / whiteLinear - expected);
            const byteError = Math.abs(extendedSrgb(linear / whiteLinear) - extendedSrgb(expected)) * 255;
            maxRelativeError = Math.max(maxRelativeError, error);
            maxSourceByteError = Math.max(maxSourceByteError, byteError);
            // Allow one source byte of external-texture rounding plus float16
            // encoding error; still require each near-white step to stay distinct.
            if (!Number.isFinite(error) || byteError > 1.5) throw new Error(`pixel_contrast_changed:${path.label}:${row}:${x}:${y}:${channel}:${error}:byteError=${byteError}`);
          }
        }
      }
      const grays = pixels[row].slice(18).map((pixel) => path.encoded ? extendedSrgbToLinear(pixel[0]) : pixel[0]);
      if (!grays.every((gray, index) => gray < whiteLinear && (index === 0 || gray > grays[index - 1]))) {
        throw new Error(`pale_gray_separation_lost:${path.label}:${row}`);
      }
    }
    readbackBuffer.unmap();
    return {
      label: path.label,
      result: 'passed',
      colorSpace: path.colorSpace,
      encoding: path.encoded ? 'extended-srgb' : 'linear-light',
      hdrWave: await checkWave(device, path),
      levels: Array.from(LEVELS),
      contrast: { pixelsChecked: SOURCE_WIDTH * SOURCE_HEIGHT * LEVELS.length, maxRelativeError, maxSourceByteError, smallestTextPixels: 12 },
      samples: SOURCE_SAMPLES.map((sample) => sample.label),
      output: pixels.map((row) => row.map((pixel) => pixel.slice(0, 3).map((value) => Number(value.toFixed(4)))))
    };
  } finally {
    try { if (frame) frame.close(); } catch (_) {}
    try { if (readbackBuffer && readbackBuffer.mapState === 'mapped') readbackBuffer.unmap(); } catch (_) {}
    try { if (readbackBuffer) readbackBuffer.destroy(); } catch (_) {}
    for (const paramsBuffer of paramsBuffers) {
      try { paramsBuffer.destroy(); } catch (_) {}
    }
    try { context.unconfigure(); } catch (_) {}
    try { device.destroy(); } catch (_) {}
  }
}

async function main() {
  if (!navigator.gpu) throw new Error('webgpu_unavailable');
  const paths = [
    { label: 'linear-canvas', colorSpace: CLIENT_HDR_LINEAR_COLOR_SPACE, encoded: false },
    { label: 'encoded-srgb-canvas', colorSpace: CLIENT_HDR_FALLBACK_COLOR_SPACE, encoded: true }
  ];
  const results = [];
  for (const path of paths) {
    try {
      results.push(await runPath(path));
    } catch (error) {
      results.push({
        label: path.label,
        result: error instanceof UnsupportedPathError ? 'unsupported' : 'failed',
        reason: String(error && error.message || error),
        compilationLocation: error && error.compilationLocation || undefined
      });
    }
  }
  const complete = results.every((entry) => entry.result === 'passed');
  const failed = results.some((entry) => entry.result === 'failed');
  const usable = results.some((entry) => entry.result === 'passed');
  // Safari 27 acceptance is strict: both the linear canvas and explicit
  // extended-sRGB fallback must pass. One usable path is diagnostic evidence,
  // not a passing result for this compatibility harness.
  const status = complete ? 'passed' : failed || usable ? 'failed' : 'unsupported';
  document.body.dataset.clientHdrGpuTest = status;
  const result = document.getElementById('result');
  result.dataset.result = status;
  result.textContent = JSON.stringify({ complete, usable, status, results }, null, 2);
  globalThis.clientHDRRealGPUResult = { complete, usable, status, results };
}

main().catch((error) => {
  document.body.dataset.clientHdrGpuTest = 'failed';
  const result = document.getElementById('result');
  result.dataset.result = 'failed';
  result.textContent = String(error && error.stack || error);
  globalThis.clientHDRRealGPUResult = { complete: false, error: String(error && error.message || error) };
});
