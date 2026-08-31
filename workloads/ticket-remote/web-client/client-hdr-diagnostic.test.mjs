import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CLIENT_HDR_DIAGNOSTIC_CANVAS_FORMAT,
  CLIENT_HDR_DIAGNOSTIC_COLORS,
  CLIENT_HDR_DIAGNOSTIC_SHADER,
  renderClientHDRDiagnosticPatches
} from './client-hdr-diagnostic.mjs';

function diagnosticHarness(options = {}) {
  const styleValues = new Map();
  const submissions = [];
  const configuration = { current: null };
  const pass = {
    setPipeline() {},
    setBindGroup() {},
    draw(count) { submissions.push({ draw: count }); },
    end() {}
  };
  const context = {
    configure(value) { configuration.current = value; },
    getConfiguration() { return configuration.current; },
    unconfigure() { configuration.current = null; },
    getCurrentTexture() { return { createView: () => ({}) }; }
  };
  const device = {
    queue: {
      writeBuffer() {},
      submit(commands) { submissions.push({ commands }); },
      onSubmittedWorkDone: async () => {}
    },
    createShaderModule: () => ({ getCompilationInfo: async () => ({ messages: options.compilationMessages || [] }) }),
    createRenderPipelineAsync: async () => ({ getBindGroupLayout: () => ({}) }),
    createBuffer: () => ({ destroy() {} }),
    createBindGroup: () => ({}),
    createCommandEncoder: () => ({
      beginRenderPass: () => pass,
      finish: () => ({})
    }),
    destroy() {}
  };
  const environment = {
    navigator: {
      gpu: { requestAdapter: async () => ({ requestDevice: async () => device }) }
    },
    GPUTextureUsage: { RENDER_ATTACHMENT: 16 },
    GPUBufferUsage: { UNIFORM: 64, COPY_DST: 8 },
    requestAnimationFrame(callback) { queueMicrotask(() => callback(0)); return 1; },
    getComputedStyle(canvas) {
      return { getPropertyValue: (name) => canvas.style.values.get(name) || '' };
    }
  };
  const canvas = {
    width: 0,
    height: 0,
    offsetWidth: 720,
    style: {
      values: styleValues,
      setProperty(name, value) { styleValues.set(name, value); }
    },
    getContext: (kind) => kind === 'webgpu' ? context : null
  };
  return { canvas, context, environment, submissions, styleValues };
}

test('diagnostic shader emits fixed 1x, 2x, and 4x patches for every diagnostic hue', () => {
  assert.match(CLIENT_HDR_DIAGNOSTIC_SHADER, /var<uniform> diagnosticParams:/);
  assert.doesNotMatch(CLIENT_HDR_DIAGNOSTIC_SHADER, /var<uniform> diagnostic:/);
  for (const snippet of ['peak = 1.0', 'peak = 2.0', 'peak = 4.0', 'linearToExtendedSrgb']) {
    assert.match(CLIENT_HDR_DIAGNOSTIC_SHADER, new RegExp(snippet.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
  for (const color of [
    'vec3<f32>(1.0, 0.0, 0.0)',
    'vec3<f32>(0.0, 1.0, 0.0)',
    'vec3<f32>(0.0, 0.0, 1.0)',
    'vec3<f32>(0.0, 1.0, 1.0)',
    'vec3<f32>(1.0, 0.0, 1.0)',
    'vec3<f32>(1.0, 1.0, 0.0)',
    'vec3<f32>(1.0, 0.21404114, 0.0)',
    'vec3<f32>(1.0, 1.0, 1.0)'
  ]) {
    assert.match(CLIENT_HDR_DIAGNOSTIC_SHADER,
      new RegExp(color.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
  assert.deepEqual(CLIENT_HDR_DIAGNOSTIC_COLORS,
    ['red', 'green', 'blue', 'cyan', 'magenta', 'yellow', 'orange', 'white']);
  assert.doesNotMatch(CLIENT_HDR_DIAGNOSTIC_SHADER, /\b(?:input|output|patch)\b/);
  assert.equal(CLIENT_HDR_DIAGNOSTIC_CANVAS_FORMAT, 'rgba16float');
});

test('diagnostic patches configure extended WebGPU and settle at no-limit', async () => {
  const state = diagnosticHarness();
  const result = await renderClientHDRDiagnosticPatches(state.canvas, {
    environment: state.environment,
    width: 720,
    height: 180
  });
  assert.deepEqual(result.intendedPeaks, [1, 2, 4]);
  assert.deepEqual(result.intendedColors,
    ['red', 'green', 'blue', 'cyan', 'magenta', 'yellow', 'orange', 'white']);
  assert.equal(result.dynamicRangeLimit, 'no-limit');
  assert.equal(result.canvasEncoding, 'srgb-linear');
  assert.equal(state.context.getConfiguration().format, 'rgba16float');
  assert.equal(state.context.getConfiguration().toneMapping.mode, 'extended');
  assert.equal(state.styleValues.get('dynamic-range-limit'), 'no-limit');
  assert.ok(state.submissions.some((entry) => entry.draw === 3));
  result.dispose();
  assert.equal(state.context.getConfiguration(), null);
});

test('diagnostic patches fail closed when no-limit is not applied', async () => {
  const state = diagnosticHarness();
  state.environment.getComputedStyle = () => ({ getPropertyValue: () => 'standard' });
  await assert.rejects(
    renderClientHDRDiagnosticPatches(state.canvas, { environment: state.environment }),
    /hdr_diagnostic_no_limit_unavailable/
  );
  assert.equal(state.context.getConfiguration(), null);
});

test('diagnostic shader compilation failure reports Safari line and column', async () => {
  const state = diagnosticHarness({
    compilationMessages: [{ type: 'error', message: 'reserved identifier', lineNum: 22, linePos: 7 }]
  });
  await assert.rejects(
    renderClientHDRDiagnosticPatches(state.canvas, { environment: state.environment }),
    /hdr_diagnostic_shader_failed:22:7:reserved identifier/
  );
  assert.equal(state.context.getConfiguration(), null);
});
