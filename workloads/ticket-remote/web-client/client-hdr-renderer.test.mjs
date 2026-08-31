import assert from 'node:assert/strict';
import test from 'node:test';
import {
  CLIENT_HDR_ALLOWED_BOOSTS,
  CLIENT_HDR_ACTIVATION_MAPPING_MODEL,
  CLIENT_HDR_COLOR_EXPANSION_EXPONENT,
  CLIENT_HDR_DEFAULT_BOOST,
  CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS,
  CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS,
  CLIENT_HDR_INTERNAL_IDENTITY_BOOST,
  CLIENT_HDR_MAPPING_MODEL,
  CLIENT_HDR_REQUEST_PATCH_EDGE,
  CLIENT_HDR_REQUEST_PATCH_PEAK,
  ClientHDRRenderer,
  clientHDRSourceColorSpace,
  isClientHDRBoost,
  mapClientHDRLinearRGB,
  mapClientHDRLuminance
} from './client-hdr-renderer.mjs';

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((resolveValue, rejectValue) => {
    resolve = resolveValue;
    reject = rejectValue;
  });
  return { promise, resolve, reject };
}

function tick() {
  return new Promise((resolve) => setImmediate(resolve));
}

function fakeGPU(options = {}) {
  const completionGates = Array.isArray(options.completionGates)
    ? Array.from(options.completionGates)
    : null;
  const timers = [];
  const setTimer = (callback, millis) => {
    const timer = { callback, millis, cancelled: false };
    timers.push(timer);
    return timer;
  };
  const clearTimer = (timer) => { if (timer) timer.cancelled = true; };
  const stats = {
    displayTransitions: [],
    canvasConfigurations: [],
    contextUnconfigureCount: 0,
    deviceDestroyCount: 0,
    bufferDestroyCount: 0,
    textureDestroyCount: 0,
    canvasTextureRequests: 0,
    textureDescriptors: [],
    textureCopies: [],
    pipelines: [],
    shaderCode: '',
    bindGroups: [],
    passes: [],
    submits: 0,
    externalTextureDescriptors: []
  };
  stats.pendingDisplayCallbacks = [];
  let currentConfiguration = null;
  let displayPaintRequests = 0;
  const context = {
    configure(configuration) {
      stats.displayTransitions.push(`configure:${configuration.colorSpace}`);
      stats.canvasConfigurations.push(configuration);
      if (options.rejectLinear && configuration.colorSpace === 'srgb-linear') throw new TypeError('unsupported color space');
      currentConfiguration = configuration;
    },
    getConfiguration: options.noConfigurationProbe ? undefined : () => currentConfiguration,
    getCurrentTexture() {
      stats.canvasTextureRequests += 1;
      return { tag: 'canvas-texture', createView() { return { tag: 'canvas-view' }; } };
    },
    unconfigure() { stats.contextUnconfigureCount += 1; currentConfiguration = null; }
  };
  const buffers = [];
  const makeBuffer = (descriptor) => {
    const buffer = {
      descriptor,
      destroyed: false,
      destroy() { this.destroyed = true; stats.bufferDestroyCount += 1; }
    };
    buffers.push(buffer);
    return buffer;
  };
  const pass = (descriptor) => ({
    descriptor,
    pipeline: null,
    bindGroup: null,
    setPipeline(pipeline) { this.pipeline = pipeline; },
    setBindGroup(_index, bindGroup) { this.bindGroup = bindGroup; },
    draw() {},
    end() { stats.passes.push(this); }
  });
  const device = {
    lost: new Promise(() => {}),
    queue: {
      writes: [],
      writeBuffer(buffer, offset, values) {
        if (options.boostWriteError && this.writes.length > 0) throw new Error('synthetic boost write error');
        this.writes.push({ buffer, offset, values: Array.from(values) });
      },
      submit() {
        if (options.submitError) throw new Error('synthetic submit error');
        stats.submits += 1;
      },
      onSubmittedWorkDone() {
        if (completionGates) return completionGates.shift() || Promise.resolve();
        return options.completionGate || Promise.resolve();
      }
    },
    createShaderModule(descriptor) {
      stats.shaderCode = String(descriptor.code || '');
      return { async getCompilationInfo() { return { messages: options.compilationMessages || [] }; } };
    },
    createRenderPipeline(descriptor) {
      const pipeline = {
        entryPoint: descriptor.fragment.entryPoint,
        getBindGroupLayout() { return { entryPoint: descriptor.fragment.entryPoint }; }
      };
      stats.pipelines.push(pipeline);
      return pipeline;
    },
    async createRenderPipelineAsync(descriptor) { return this.createRenderPipeline(descriptor); },
    createSampler() { return { tag: 'sampler' }; },
    createBuffer: makeBuffer,
    createTexture(descriptor) {
      stats.textureDescriptors.push(descriptor);
      return {
        tag: 'staging-texture',
        descriptor,
        destroyed: false,
        createView() { return { tag: 'staging-view' }; },
        destroy() {
          if (this.destroyed) return;
          this.destroyed = true;
          stats.textureDestroyCount += 1;
        }
      };
    },
    importExternalTexture(descriptor) {
      stats.externalTextureDescriptors.push(descriptor);
      return { tag: 'external-texture' };
    },
    createBindGroup(descriptor) {
      stats.bindGroups.push(descriptor);
      return { descriptor };
    },
    createCommandEncoder() {
      return {
        beginRenderPass(descriptor) { return pass(descriptor); },
        copyTextureToTexture(source, destination, size) {
          stats.textureCopies.push({ source, destination, size });
        },
        finish() { return { tag: 'commands' }; }
      };
    },
    pushErrorScope() {},
    async popErrorScope() {
      if (options.presentValidationError && stats.textureCopies.length > 0) return options.presentValidationError;
      if (options.renderValidationError && stats.submits > 0) return options.renderValidationError;
      if (options.validationError && stats.textureDescriptors.length > 0) return options.validationError;
      return null;
    },
    addEventListener() {},
    removeEventListener() {},
    destroy() { stats.deviceDestroyCount += 1; }
  };
  const environment = {
    navigator: { gpu: { async requestAdapter() {
      return { async requestDevice() {
        if (options.deviceGate) await options.deviceGate;
        return device;
      } };
    } } },
    GPUBufferUsage: { UNIFORM: 1, COPY_DST: 2 },
    GPUTextureUsage: { COPY_SRC: 1, COPY_DST: 2, RENDER_ATTACHMENT: 16 },
    getComputedStyle(target) {
      const requested = target.style.values['dynamic-range-limit'] || '';
      const overrideKey = requested === 'standard' ? 'computedStandard' : 'computedNoLimit';
      const value = Object.prototype.hasOwnProperty.call(options, overrideKey)
        ? String(options[overrideKey])
        : requested;
      stats.displayTransitions.push(`flush:${value}`);
      return { getPropertyValue() { return value; } };
    },
    requestAnimationFrame(callback) {
      displayPaintRequests += 1;
      stats.displayTransitions.push('request-paint');
      if (typeof options.onDisplayPaintRequest === 'function') {
        options.onDisplayPaintRequest(displayPaintRequests);
      }
      const stalled = options.stallDisplayRefresh ||
        (options.stallDisplayRefreshAfterStandard && displayPaintRequests > 2) ||
        (Number.isFinite(Number(options.stallDisplayRefreshAfterRequests)) &&
          displayPaintRequests > Number(options.stallDisplayRefreshAfterRequests));
      if (stalled) {
        stats.pendingDisplayCallbacks.push(callback);
      } else queueMicrotask(() => {
        stats.displayTransitions.push('paint');
        callback(0);
      });
      return 17;
    },
    cancelAnimationFrame() { stats.displayTransitions.push('cancel-paint'); },
    performance: { now: () => 0 }
  };
  const canvas = {
    width: 1,
    height: 1,
    style: { values: {}, setProperty(name, value) {
      this.values[name] = value;
      stats.displayTransitions.push(`style:${name}:${value}`);
    } },
    getContext(kind) { return kind === 'webgpu' ? context : null; }
  };
  return { environment, canvas, context, device, buffers, stats, timers, setTimer, clearTimer };
}

async function initializedRenderer(options = {}) {
  const gpu = fakeGPU(options);
  let clock = 100;
  let wallClock = 1000;
  const metrics = [];
  const failures = [];
  const renderer = new ClientHDRRenderer({
    environment: gpu.environment,
    now: () => clock,
    wallNow: () => wallClock,
    setTimer: gpu.setTimer,
    clearTimer: gpu.clearTimer,
    onMetric: (event, detail) => metrics.push({ event, detail }),
    onFailure: (reason) => failures.push(reason)
  });
  const result = await renderer.initialize({ canvas: gpu.canvas, width: 720, height: 1482, boost: 6 });
  return {
    ...gpu,
    renderer,
    result,
    metrics,
    failures,
    setClock(value) { clock = value; },
    setWallClock(value) { wallClock = value; }
  };
}

test('versioned black-anchored hue expansion supports exactly the public 2x through 6x ladder', () => {
  assert.deepEqual(CLIENT_HDR_ALLOWED_BOOSTS, [2, 3, 4, 5, 6]);
  assert.equal(CLIENT_HDR_DEFAULT_BOOST, 4);
  assert.equal(CLIENT_HDR_INTERNAL_IDENTITY_BOOST, 1);
  assert.equal(CLIENT_HDR_MAPPING_MODEL, 'black_anchored_hue_expansion_v3');
  assert.equal(CLIENT_HDR_COLOR_EXPANSION_EXPONENT, 3);
  assert.equal(CLIENT_HDR_REQUEST_PATCH_PEAK, 1.25);
  assert.equal(CLIENT_HDR_REQUEST_PATCH_EDGE, 0.002);
  assert.equal(CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS, 1500);
  assert.equal(CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS, 2000);
  for (const boost of CLIENT_HDR_ALLOWED_BOOSTS) {
    assert.equal(isClientHDRBoost(boost), true);
    assert.equal(mapClientHDRLuminance(0, boost), 0);
    assert.equal(mapClientHDRLuminance(1, boost), boost);
    assert.ok(mapClientHDRLuminance(0.25, boost) > 0.25,
      `ordinary colors must receive HDR gain at ${boost}x`);
  }
  assert.equal(isClientHDRBoost(CLIENT_HDR_INTERNAL_IDENTITY_BOOST), false,
    'the private identity pass must never become a selectable boost');
  for (const invalid of [0, 1.5, 7, 8, 10, 12, 14, 16, Number.NaN, 'invalid']) {
    assert.equal(isClientHDRBoost(invalid), false);
    assert.throws(() => mapClientHDRLuminance(0.25, invalid), /hdr_boost_invalid/);
  }
});

test('black-anchored hue expansion has literal goldens, exact 1x identity, and monotonic finite output', () => {
  assert.equal(mapClientHDRLuminance(0.26635560480286247, 6), 1.072251204593734);
  assert.equal(mapClientHDRLuminance(0.7912979403326302, 6), 4.7118217989269375);
  for (const value of [0, 0.001, 0.01, 0.18, 0.25, 0.5, 0.9, 1]) {
    assert.equal(mapClientHDRLuminance(value, 1), value, `1x changed ${value}`);
  }
  for (const boost of CLIENT_HDR_ALLOWED_BOOSTS) {
    assert.deepEqual(mapClientHDRLinearRGB([0, 0, 0], boost), [0, 0, 0]);
    const source = [0.36, 0.18, 0.09];
    const mapped = mapClientHDRLinearRGB(source, boost);
    for (let channel = 0; channel < source.length; channel += 1) {
      assert.ok(mapped[channel] > source[channel],
        `non-black channel ${channel} did not receive gain at ${boost}x`);
    }
    const highlightSource = [1, 0.9, 0.8];
    const highlightMapped = mapClientHDRLinearRGB(highlightSource, boost);
    assert.ok(highlightMapped.every((value) => Number.isFinite(value) && value <= boost));
    assert.ok(Math.abs(highlightMapped[0] / highlightMapped[1] - highlightSource[0] / highlightSource[1]) < 1e-12);
    assert.ok(Math.abs(highlightMapped[1] / highlightMapped[2] - highlightSource[1] / highlightSource[2]) < 1e-12);
    assert.ok(Math.abs(mapped[0] / mapped[1] - source[0] / source[1]) < 1e-12);
    assert.ok(Math.abs(mapped[1] / mapped[2] - source[1] / source[2]) < 1e-12);
    assert.equal(mapClientHDRLuminance(0, boost), 0);
    assert.equal(mapClientHDRLuminance(1, boost), boost);
  }
  assert.ok(mapClientHDRLuminance(0.001, 6) > 0.001,
    'near-black colors must receive a bounded positive gain');
  assert.ok(mapClientHDRLuminance(0.001, 6) < 0.0011,
    'the black anchor must prevent a washed near-black floor');
  assert.ok(mapClientHDRLinearRGB([1e-8, 0, 0], 6)[0] > 1e-8,
    'the CPU mirror must match the shader for every positive non-black value');
  let previous = -Infinity;
  for (let step = 0; step <= 10000; step += 1) {
    const mapped = mapClientHDRLuminance(step / 10000, 6);
    assert.ok(Number.isFinite(mapped));
    assert.ok(mapped >= previous, `curve regressed at ${step}`);
    assert.ok(mapped <= 6);
    previous = mapped;
  }
});

test('real ticket red, orange, primaries, secondaries, white, and mixed colors all gain without changing hue', () => {
  const srgb8ToLinear = (value) => {
    const encoded = value / 255;
    return encoded <= 0.04045
      ? encoded / 12.92
      : ((encoded + 0.055) / 1.055) ** 2.4;
  };
  const colors = [
    { name: 'ticket red', rgb: [141, 44, 37].map(srgb8ToLinear), mustEnterEDRAt: 6 },
    { name: 'ticket orange', rgb: [230, 130, 6].map(srgb8ToLinear), mustEnterEDRAt: 2 },
    { name: 'ticket green', rgb: [50, 180, 80].map(srgb8ToLinear), mustEnterEDRAt: 6 },
    { name: 'ticket blue', rgb: [50, 100, 200].map(srgb8ToLinear), mustEnterEDRAt: 6 },
    { name: 'ticket mid-gray', rgb: [128, 128, 128].map(srgb8ToLinear) },
    { name: 'ticket near-black', rgb: [16, 8, 4].map(srgb8ToLinear) },
    { name: 'red', rgb: [0.25, 0, 0] },
    { name: 'green', rgb: [0, 0.25, 0] },
    { name: 'blue', rgb: [0, 0, 0.25] },
    { name: 'cyan', rgb: [0, 0.25, 0.25] },
    { name: 'magenta', rgb: [0.25, 0, 0.25] },
    { name: 'yellow', rgb: [0.25, 0.25, 0] },
    { name: 'white', rgb: [1, 1, 1] },
    { name: 'mixed', rgb: [0.36, 0.18, 0.09] }
  ];
  for (const { name, rgb, mustEnterEDRAt } of colors) {
    assert.deepEqual(mapClientHDRLinearRGB(rgb, 1), rgb, `${name} changed at 1x`);
    for (const boost of CLIENT_HDR_ALLOWED_BOOSTS) {
      const mapped = mapClientHDRLinearRGB(rgb, boost);
      for (let channel = 0; channel < 3; channel += 1) {
        if (rgb[channel] === 0) {
          assert.equal(mapped[channel], 0, `${name} gained an absent channel`);
        } else {
          assert.ok(mapped[channel] > rgb[channel],
            `${name} channel ${channel} did not gain at ${boost}x`);
        }
      }
      const sourcePeak = Math.max(...rgb);
      const outputPeak = Math.max(...mapped);
      const sharedGain = outputPeak / sourcePeak;
      assert.ok(sharedGain > 1 && sharedGain <= boost, `${name} gain escaped the selected bound`);
      for (let channel = 0; channel < 3; channel += 1) {
        if (rgb[channel] > 0) {
          assert.ok(Math.abs(mapped[channel] / rgb[channel] - sharedGain) < 1e-12,
            `${name} did not preserve hue on channel ${channel}`);
        }
      }
      if (mustEnterEDRAt && boost >= mustEnterEDRAt) {
        assert.ok(outputPeak > 1, `${name} did not enter EDR at ${boost}x`);
      }
    }
  }
});

test('the activation request patch is bounded while every pixel outside it remains exact 1x SDR', () => {
  const inside = { requestPatch: true, uv: [0.001, 0.001] };
  const outside = { requestPatch: true, uv: [0.002, 0.001] };
  assert.deepEqual(mapClientHDRLinearRGB([0, 0, 0], 6, inside), [1.25, 1.25, 1.25]);
  assert.deepEqual(mapClientHDRLinearRGB([0, 0, 0], 6, outside), [0, 0, 0]);
  assert.deepEqual(mapClientHDRLinearRGB([0, 0, 0], 6), [0, 0, 0]);
  assert.deepEqual(mapClientHDRLinearRGB([0.4, 0.2, 0.1], 1, inside), [1.25, 1.25, 1.25]);
  assert.deepEqual(mapClientHDRLinearRGB([0.4, 0.2, 0.1], 1, outside), [0.4, 0.2, 0.1]);
  assert.deepEqual(mapClientHDRLinearRGB([0.4, 0.2, 0.1], 1), [0.4, 0.2, 0.1]);
  assert.ok(mapClientHDRLinearRGB([0, 0, 0], 6, inside).every((value) => value > 1 && value <= 1.25));
});

test('source color-space metric is bounded and contains only standardized metadata', () => {
  assert.equal(clientHDRSourceColorSpace({ colorSpace: {
    primaries: 'bt709', transfer: 'iec61966-2-1', matrix: 'rgb', fullRange: true
  } }), 'primaries=bt709;transfer=iec61966-2-1;matrix=rgb;range=full');
  const sanitized = clientHDRSourceColorSpace({ colorSpace: {
    primaries: 'unsafe value / with spaces', transfer: null, matrix: undefined, fullRange: false
  } });
  assert.equal(sanitized, 'primaries=unsafe-value-with-spaces;transfer=unknown;matrix=unknown;range=limited');
  assert.ok(sanitized.length <= 120);
  assert.equal(clientHDRSourceColorSpace({}), 'primaries=unknown;transfer=unknown;matrix=unknown;range=unknown');
  assert.equal(clientHDRSourceColorSpace({ get colorSpace() { throw new Error('unavailable'); } }),
    'primaries=unknown;transfer=unknown;matrix=unknown;range=unknown');
});

test('renderer starts no-limit and uses black-anchored hue expansion on the exact linear HDR canvas', async () => {
  const state = await initializedRenderer();
  assert.equal(state.result.canvasEncoding, 'srgb-linear');
  assert.equal(state.result.displayBoost, 6);
  assert.equal(state.result.intendedOutputPeak, 6);
  assert.equal(state.result.mappingModel, CLIENT_HDR_MAPPING_MODEL);
  assert.equal(state.result.colorExpansionExponent, 3);
  assert.equal(state.result.edrRequestPatchIntended, true);
  assert.equal(state.result.intendedRequestPatchPeak, 1.25);
  assert.equal(state.result.intendedRequestPatchEdge, 0.002);
  assert.equal(state.result.continuousSurface, true);
  assert.equal(state.stats.canvasConfigurations[0].format, 'rgba16float');
  assert.equal(state.stats.canvasConfigurations[0].colorSpace, 'srgb-linear');
  assert.equal(state.stats.canvasConfigurations[0].toneMapping.mode, 'extended');
  assert.equal(state.stats.canvasConfigurations[0].alphaMode, 'opaque');
  assert.equal(state.stats.canvasConfigurations[0].usage, 18);
  assert.equal(state.canvas.style.values['dynamic-range-limit'], 'no-limit');
  assert.equal(state.result.configurationDynamicRangeLimit, 'no-limit');
  assert.equal(state.result.configurationColorSpace, 'srgb-linear');
  assert.equal(state.result.toneMappingMode, 'extended');
  assert.deepEqual(state.stats.displayTransitions, [
    'style:dynamic-range-limit:no-limit',
    'flush:no-limit',
    'configure:srgb-linear'
  ]);
  assert.deepEqual(state.stats.pipelines.map((pipeline) => pipeline.entryPoint), ['fragmentMain']);
  assert.match(state.stats.shaderCode, /const COLOR_EXPANSION_EXPONENT: f32 = 3\.0/);
  assert.match(state.stats.shaderCode, /fn colorGain\(/);
  assert.match(state.stats.shaderCode, /let sourcePeak = max\(linearRGB\.r, max\(linearRGB\.g, linearRGB\.b\)\)/);
  assert.match(state.stats.shaderCode, /let blackDistance = clamp\(1\.0 - sourcePeak, 0\.0, 1\.0\)/);
  assert.match(state.stats.shaderCode, /1\.0 - pow\(blackDistance, COLOR_EXPANSION_EXPONENT\)/);
  assert.match(state.stats.shaderCode, /linearRGB \* colorGain\(linearRGB, hdr\.options\.x\)/);
  assert.doesNotMatch(state.stats.shaderCode, /REC709_LUMA|sourceLuminance|dot\(linearRGB/);
  assert.match(state.stats.shaderCode, /let edrRequestSample = hdr\.options\.z > 0\.5 &&/);
  assert.doesNotMatch(state.stats.shaderCode, /hdr\.options\.z > 0\.5 && hdr\.options\.x > 1\.0/);
  assert.match(state.stats.shaderCode, /vec3<f32>\(1\.25\)/);
  assert.match(state.stats.shaderCode, /linearToExtendedSrgb/);
  assert.doesNotMatch(state.stats.shaderCode, /\b(?:input|output|patch)\b/);
  assert.doesNotMatch(state.stats.shaderCode, /applyNeutralGrayContrast|applyEDRActivation/);
  assert.doesNotMatch(state.stats.shaderCode, /fragmentAnalysis|fragmentDiagnostic/);
  assert.equal(state.stats.canvasTextureRequests, 0, 'initialization must not write an identity or activation frame');
  assert.equal(state.buffers.length, 1);
  assert.equal(state.buffers[0].descriptor.size, 16);
  assert.equal(state.stats.textureDescriptors.length, 1);
  assert.equal(state.stats.textureDescriptors[0].format, 'rgba16float');
  assert.equal(state.stats.textureDescriptors[0].usage, 17);
  assert.equal(state.stats.submits, 0, 'the first GPU submission must belong to the selected source frame');
  state.renderer.dispose();
});

test('renderer fails before WebGPU configuration when unrestricted range is unavailable', async () => {
  const gpu = fakeGPU({ computedNoLimit: 'standard' });
  const renderer = new ClientHDRRenderer({ environment: gpu.environment });
  await assert.rejects(
    renderer.initialize({ canvas: gpu.canvas, width: 720, height: 1482, boost: 6 }),
    /hdr_no_limit_dynamic_range_unavailable/
  );
  assert.deepEqual(gpu.stats.displayTransitions, [
    'style:dynamic-range-limit:no-limit',
    'flush:standard'
  ]);
  assert.equal(gpu.stats.canvasConfigurations.length, 0);
  assert.equal(gpu.stats.canvasTextureRequests, 0);
  renderer.dispose();
});

test('renderer falls back to explicitly encoded extended sRGB', async () => {
  const state = await initializedRenderer({ rejectLinear: true });
  assert.equal(state.result.canvasEncoding, 'srgb-encoded');
  assert.deepEqual(state.stats.canvasConfigurations.map((configuration) => configuration.colorSpace), ['srgb-linear', 'srgb']);
  const params = state.device.queue.writes.at(-1).values;
  assert.deepEqual(params, [6, 1, 0, 0]);
  state.renderer.dispose();
});

test('boost changes update one uniform without reconfiguring or rebuilding GPU resources', async () => {
  const state = await initializedRenderer();
  const configurations = state.stats.canvasConfigurations.length;
  const pipelines = state.stats.pipelines.length;
  const buffers = state.buffers.length;
  const initialWrites = state.device.queue.writes.length;
  assert.equal(state.renderer.setBoost(3), 3);
  assert.deepEqual(state.device.queue.writes.at(-1).values, [3, 0, 0, 0]);
  assert.equal(state.stats.canvasConfigurations.length, configurations);
  assert.equal(state.stats.pipelines.length, pipelines);
  assert.equal(state.buffers.length, buffers);
  assert.equal(state.device.queue.writes.length, initialWrites + 1);
  assert.equal(state.renderer.setBoost(3), 3);
  assert.equal(state.device.queue.writes.length, initialWrites + 1, 'reselecting the same level is idempotent');
  assert.equal(state.renderer.setBoost(5), 5);
  assert.deepEqual(state.device.queue.writes.at(-1).values, [5, 0, 0, 0]);
  assert.throws(() => state.renderer.setBoost(1), /hdr_boost_invalid/);
  assert.throws(() => state.renderer.setBoost(8), /hdr_boost_invalid/);
  assert.equal(state.renderer.boost, 5);
  state.renderer.dispose();
});

test('a failed boost write keeps the renderer level transactional and retryable', async () => {
  const state = await initializedRenderer({ boostWriteError: true });
  assert.equal(state.renderer.boost, 6);
  assert.throws(() => state.renderer.setBoost(3), /synthetic boost write error/);
  assert.equal(state.renderer.boost, 6);
  assert.equal(state.device.queue.writes.length, 1);
  assert.throws(() => state.renderer.setBoost(3), /synthetic boost write error/,
    'the failed level must not become an idempotent no-op');
  assert.equal(state.renderer.boost, 6);
  state.renderer.dispose();
});

test('renderer fails closed when the exact canvas configuration cannot be inspected', async () => {
  const gpu = fakeGPU({ noConfigurationProbe: true });
  const renderer = new ClientHDRRenderer({ environment: gpu.environment });
  await assert.rejects(
    renderer.initialize({ canvas: gpu.canvas, width: 720, height: 1482, boost: 6 }),
    /hdr_canvas_extended_mode_unavailable/
  );
  renderer.dispose();
});

test('renderer rejects the private identity and retired boost values before creating a GPU device', async () => {
  for (const boost of [1, 8, 10, 12, 14, 16]) {
    const gpu = fakeGPU();
    const renderer = new ClientHDRRenderer({ environment: gpu.environment });
    await assert.rejects(
      renderer.initialize({ canvas: gpu.canvas, width: 720, height: 1482, boost }),
      /hdr_boost_invalid/
    );
    assert.equal(gpu.stats.canvasConfigurations.length, 0);
    renderer.dispose();
  }
});

test('disposal during asynchronous device creation cannot resurrect or leak the renderer', async () => {
  let releaseDevice;
  const deviceGate = new Promise((resolve) => { releaseDevice = resolve; });
  const gpu = fakeGPU({ deviceGate });
  const renderer = new ClientHDRRenderer({ environment: gpu.environment });
  const initialization = renderer.initialize({ canvas: gpu.canvas, width: 720, height: 1482, boost: 6 });
  await tick();
  renderer.dispose();
  releaseDevice();
  await assert.rejects(initialization, /renderer_disposed/);
  assert.equal(gpu.stats.canvasConfigurations.length, 0);
  assert.equal(gpu.stats.bufferDestroyCount, 0);
  assert.equal(gpu.stats.deviceDestroyCount, 1);
  renderer.dispose();
  assert.equal(gpu.stats.deviceDestroyCount, 1, 'repeated cleanup must not double-destroy a late device');
});

test('shader compilation reports Safari line and column while failures clean partial GPU resources', async () => {
  const compilation = fakeGPU({
    compilationMessages: [{ type: 'error', message: 'synthetic shader error', lineNum: 47, linePos: 9 }]
  });
  const compilationRenderer = new ClientHDRRenderer({ environment: compilation.environment });
  await assert.rejects(
    compilationRenderer.initialize({ canvas: compilation.canvas, width: 720, height: 1482, boost: 6 }),
    /shader_compilation_failed:47:9:synthetic_shader_error/
  );
  assert.equal(compilation.stats.bufferDestroyCount, 0);
  compilationRenderer.dispose();

  const validation = fakeGPU({ validationError: new Error('synthetic validation error') });
  const validationRenderer = new ClientHDRRenderer({ environment: validation.environment });
  await assert.rejects(
    validationRenderer.initialize({ canvas: validation.canvas, width: 720, height: 1482, boost: 6 }),
    /pipeline_validation_failed/
  );
  assert.equal(validation.stats.bufferDestroyCount, 1);
  validationRenderer.dispose();
  assert.equal(validation.stats.bufferDestroyCount, 1, 'partial resources must not be destroyed twice');
});

test('one canvas presents an SDR-identity activation before reusing staging for the full target', async () => {
  const state = await initializedRenderer();
  const frame = { colorSpace: {
    primaries: 'bt709', transfer: 'iec61966-2-1', matrix: 'rgb', fullRange: true
  } };

  const activation = await state.renderer.render(frame, {
    epoch: 2, sequence: 1, offeredWallMillis: Date.now()
  }, { activationFrame: true, requestPatch: true });
  assert.equal(activation.activationFrame, true);
  assert.equal(activation.activationIdentity, true);
  assert.equal(activation.displayBoost, 1);
  assert.equal(activation.selectedDisplayBoost, 6);
  assert.equal(activation.intendedOutputPeak, 1.25);
  assert.equal(activation.mappingModel, CLIENT_HDR_ACTIVATION_MAPPING_MODEL);
  assert.deepEqual(state.device.queue.writes.at(-1).values, [1, 0, 1, 0]);
  state.renderer.present();
  await state.renderer.waitForPresentCompletion();
  const activationPaint = await state.renderer.waitForPresentedCompositorOpportunities(2);
  assert.equal(activationPaint.postPresentOpportunityCount, 2);

  const target = await state.renderer.render(frame, {
    epoch: 2, sequence: 1, offeredWallMillis: Date.now()
  }, { activationFrame: false, requestPatch: false });
  assert.equal(target.activationFrame, false);
  assert.equal(target.activationIdentity, false);
  assert.equal(target.displayBoost, 6);
  assert.equal(target.selectedDisplayBoost, 6);
  assert.equal(target.intendedOutputPeak, 6);
  assert.equal(target.mappingModel, CLIENT_HDR_MAPPING_MODEL);
  assert.deepEqual(state.device.queue.writes.at(-1).values, [6, 0, 0, 0]);
  state.renderer.present();
  await state.renderer.waitForPresentCompletion();
  const targetPaint = await state.renderer.waitForPresentedCompositorOpportunities(1);
  assert.equal(targetPaint.postPresentOpportunityCount, 1);
  assert.equal(state.stats.canvasConfigurations.length, 1,
    'the same no-limit canvas must remain configured across both stages');
  assert.equal(state.stats.textureDescriptors.length, 1,
    'the existing staging texture must be reused rather than replaced');
  assert.equal(state.stats.textureCopies.length, 2);
  state.renderer.dispose();
});

test('render becomes authoritative only after GPU completion and keeps its submitted boost label', async () => {
  const completion = deferred();
  const state = await initializedRenderer({ completionGate: completion.promise });
  state.setClock(100);
  const frame = { colorSpace: {
    primaries: 'bt709', transfer: 'iec61966-2-1', matrix: 'rgb', fullRange: true
  } };
  const resultPromise = state.renderer.render(frame, {
    epoch: 1,
    sequence: 1,
    offeredWallMillis: Date.now()
  }, { requestPatch: true });
  assert.equal(state.stats.submits, 1);
  assert.equal(state.stats.passes.length, 1);
  assert.equal(state.stats.passes[0].descriptor.colorAttachments[0].view.tag, 'staging-view');
  assert.equal(state.stats.canvasTextureRequests, 0, 'preparation cannot mutate the visible canvas');
  assert.equal(state.metrics.some((metric) => metric.detail && metric.detail.gpuCompleted === true), false);
  assert.equal(state.metrics.some((metric) =>
    metric.detail && metric.detail.compositorOpportunitiesCompleted === true), false);
  assert.equal(typeof resultPromise.then, 'function');
  let settled = false;
  resultPromise.finally(() => { settled = true; });
  await tick();
  assert.equal(settled, false);
  assert.equal(state.metrics.some((metric) => metric.event === 'gpu_completion'), false);
  assert.equal(state.stats.externalTextureDescriptors[0].colorSpace, 'srgb');
  state.renderer.setBoost(3);
  state.setClock(135);
  completion.resolve();
  const result = await resultPromise;
  assert.equal(result.displayBoost, 6);
  assert.equal(result.gpuCompleted, true);
  assert.equal(result.compositorOpportunitiesCompleted, false);
  assert.equal(result.edrRequestPatchIntended, true);
  assert.equal(result.intendedRequestPatchPeak, 1.25);
  assert.equal(result.intendedRequestPatchEdge, 0.002);
  assert.equal(result.sourceColorSpace, 'primaries=bt709;transfer=iec61966-2-1;matrix=rgb;range=full');
  assert.equal(result.completionMillis, 35);
  assert.equal(result.decodedFrameToDisplayReadyMillis >= 35, true);
  const completionMetric = state.metrics.find((metric) => metric.event === 'gpu_completion');
  assert.ok(completionMetric);
  assert.equal(completionMetric.detail.completionMillis, 35);
  assert.equal(completionMetric.detail.sequence, 1);
  assert.equal(completionMetric.detail.displayBoost, 6, 'completion stays labeled with the submitted frame level');
  assert.equal(completionMetric.detail.sourceColorSpace, result.sourceColorSpace);
  const presented = state.renderer.present();
  await tick();
  assert.equal(presented.selectedDisplayBoost, 6);
  assert.equal(state.metrics.find((metric) => metric.event === 'present_completion').detail.selectedDisplayBoost, 6,
    'presentation completion stays labeled with the staged frame, not a newer uniform value');
  assert.equal(state.metrics.some((metric) => metric.event === 'compositor_opportunities_completed'), false,
    'GPU presentation completion cannot claim later animation-frame opportunities');
  assert.equal(state.stats.submits, 2);
  assert.equal(state.stats.canvasTextureRequests, 1);
  assert.equal(state.stats.textureCopies.length, 1);
  assert.equal(state.stats.textureCopies[0].source.texture.tag, 'staging-texture');
  assert.equal(state.stats.textureCopies[0].destination.texture.tag, 'canvas-texture');
  const opportunities = await state.renderer.waitForPresentedCompositorOpportunities();
  assert.equal(opportunities.gpuCompleted, true);
  assert.equal(opportunities.compositorOpportunitiesCompleted, true);
  assert.equal(opportunities.postPresentSource, 'animation_frame');
  assert.equal(opportunities.postPresentOpportunityCount, 2);
  assert.equal(opportunities.settlementDeadlineMillis, CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS);
  assert.equal(opportunities.settlementTimedOut, false);
  assert.equal(state.metrics.filter((metric) =>
    metric.event === 'compositor_opportunities_completed').length, 1);
  const settlementStarted = state.metrics.find((metric) => metric.event === 'compositor_settlement_started');
  const settlementResult = state.metrics.find((metric) => metric.event === 'compositor_settlement_result');
  assert.equal(settlementStarted.detail.settlementDeadlineMillis, CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS);
  assert.equal(settlementStarted.detail.postPresentOpportunityTarget, 2);
  assert.equal(settlementResult.detail.postPresentSource, 'animation_frame');
  assert.equal(settlementResult.detail.compositorOpportunitiesCompleted, true);
  assert.deepEqual(state.renderer.lastCompositorSettlementResult, settlementResult.detail);
  state.renderer.dispose();
  assert.equal(state.stats.textureDestroyCount, 1);
});

test('post-present compositor opportunities cannot begin before the swapchain copy completes', async () => {
  const copyCompleted = deferred();
  const state = await initializedRenderer({
    completionGates: [Promise.resolve(), copyCompleted.promise]
  });
  await state.renderer.render({}, { epoch: 1, sequence: 2, offeredWallMillis: Date.now() }, {
    requestPatch: true
  });
  state.renderer.present();
  const paintPromise = state.renderer.waitForPresentedCompositorOpportunities();
  await tick();
  assert.equal(state.stats.displayTransitions.filter((entry) => entry === 'request-paint').length, 0,
    'a slow swapchain copy cannot consume pre-completion animation frames');
  assert.equal(state.metrics.some((metric) => metric.event === 'compositor_opportunities_completed'), false);

  copyCompleted.resolve();
  const opportunities = await paintPromise;
  assert.equal(state.stats.displayTransitions.filter((entry) => entry === 'request-paint').length, 2);
  assert.equal(opportunities.postPresentSource, 'animation_frame');
  assert.equal(opportunities.postPresentOpportunityCount, 2);
  assert.equal(opportunities.gpuCompleted, true);
  assert.equal(opportunities.compositorOpportunitiesCompleted, true);
  state.renderer.dispose();
});

test('a stalled compositor settlement expires independently and ignores late animation frames', async () => {
  const state = await initializedRenderer({ stallDisplayRefresh: true });
  await state.renderer.render({}, { epoch: 3, sequence: 20, offeredWallMillis: Date.now() }, {
    requestPatch: true
  });
  state.renderer.present();
  const settlementPromise = state.renderer.waitForPresentedCompositorOpportunities();
  await tick();
  const deadline = state.timers.findLast((timer) => !timer.cancelled);
  assert.ok(deadline);
  assert.equal(deadline.millis, CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS);
  assert.equal(state.renderer.compositorSettlementWaits.size, 1);
  assert.equal(state.stats.pendingDisplayCallbacks.length, 1);

  state.setClock(2100);
  deadline.callback();
  await assert.rejects(settlementPromise, /hdr_presented_display_refresh_timeout/);
  assert.equal(state.renderer.compositorSettlementWaits.size, 0);
  assert.equal(deadline.cancelled, true);
  assert.equal(state.renderer.lastCompositorSettlementResult.postPresentSource, 'timeout');
  assert.equal(state.renderer.lastCompositorSettlementResult.postPresentOpportunityCount, 0);
  assert.equal(state.renderer.lastCompositorSettlementResult.compositorOpportunitiesCompleted, false);
  assert.equal(state.renderer.lastCompositorSettlementResult.settlementTimedOut, true);
  assert.equal(state.metrics.filter(({ event }) => event === 'compositor_settlement_result').length, 1);
  assert.equal(state.metrics.some(({ event }) => event === 'compositor_opportunities_completed'), false);

  const metricCount = state.metrics.length;
  for (const callback of state.stats.pendingDisplayCallbacks) callback(0);
  await tick();
  assert.equal(state.metrics.length, metricCount, 'a late animation frame cannot change a timed-out result');
  assert.equal(state.renderer.lastCompositorSettlementResult.postPresentSource, 'timeout');
  state.renderer.dispose();
});

test('disposing a stalled compositor settlement fences its deadline and late callbacks', async () => {
  const state = await initializedRenderer({ stallDisplayRefresh: true });
  await state.renderer.render({}, { epoch: 3, sequence: 21, offeredWallMillis: Date.now() });
  state.renderer.present();
  const settlementPromise = state.renderer.waitForPresentedCompositorOpportunities();
  await tick();
  const deadline = state.timers.findLast((timer) => !timer.cancelled);
  assert.ok(deadline);
  assert.equal(state.renderer.compositorSettlementWaits.size, 1);
  state.renderer.dispose();
  await assert.rejects(settlementPromise, /renderer_disposed/);
  assert.equal(deadline.cancelled, true);
  assert.equal(state.renderer.compositorSettlementWaits.size, 0);
  assert.equal(state.renderer.lastCompositorSettlementResult, null);

  const metricCount = state.metrics.length;
  for (const callback of state.stats.pendingDisplayCallbacks) callback(0);
  await tick();
  assert.equal(state.metrics.length, metricCount, 'late compositor callbacks must be inert after disposal');
  assert.equal(state.stats.textureDestroyCount, 1);
  assert.equal(state.stats.deviceDestroyCount, 1);
});

test('render validation failure never resolves as a presentable frame', async () => {
  const state = await initializedRenderer({ renderValidationError: new Error('synthetic render validation error') });
  await assert.rejects(
    state.renderer.render({}, { epoch: 1, sequence: 1, offeredWallMillis: Date.now() }),
    /render_validation_failed/
  );
  assert.equal(state.failures.some((reason) => reason.includes('render_validation_failed')), true);
  assert.equal(state.metrics.some((metric) => metric.event === 'gpu_completion'), false);
  state.renderer.dispose();
});

test('render validation failure does not wait for a stalled GPU completion signal', async () => {
  const completion = deferred();
  const state = await initializedRenderer({
    renderValidationError: new Error('synthetic early validation error'),
    completionGate: completion.promise
  });
  const result = state.renderer.render({}, { epoch: 1, sequence: 1, offeredWallMillis: Date.now() });
  await assert.rejects(result, /render_validation_failed/);
  assert.equal(state.failures.some((reason) => reason.includes('render_validation_failed')), true);
  assert.equal(state.metrics.some((metric) => metric.event === 'gpu_completion'), false);
  completion.resolve();
  state.renderer.dispose();
});

test('a stalled GPU completion expires at the bounded hold and ignores a late signal', async () => {
  const completion = deferred();
  const state = await initializedRenderer({ completionGate: completion.promise });
  const result = state.renderer.render({}, {
    epoch: 2, sequence: 10, offeredWallMillis: Date.now()
  });
  const watchdog = state.timers.find((timer) => !timer.cancelled);
  assert.ok(watchdog);
  assert.equal(watchdog.millis, CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  assert.equal(state.renderer.completionWaits.size, 1);
  watchdog.callback();
  await assert.rejects(result, /gpu_completion_timeout/);
  assert.deepEqual(state.failures, ['gpu_completion_timeout']);
  assert.equal(state.renderer.completionWaits.size, 0);
  assert.equal(watchdog.cancelled, true);
  assert.equal(state.renderer.prepared, false);
  assert.equal(state.metrics.some(({ event }) => event === 'gpu_completion'), false);

  completion.resolve();
  await tick();
  assert.deepEqual(state.failures, ['gpu_completion_timeout']);
  assert.equal(state.renderer.prepared, false, 'late completion cannot make a timed-out frame presentable');
  state.renderer.dispose();
  assert.equal(state.stats.textureDestroyCount, 1);
});

test('wake-late GPU completion cannot beat the renderer wall deadline', async () => {
  const completion = deferred();
  const state = await initializedRenderer({ completionGate: completion.promise });
  const result = state.renderer.render({}, {
    epoch: 2, sequence: 12, offeredWallMillis: Date.now()
  });
  state.setWallClock(1000 + CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  completion.resolve();

  await assert.rejects(result, /gpu_completion_timeout/);
  assert.deepEqual(state.failures, ['gpu_completion_timeout']);
  assert.equal(state.renderer.prepared, false);
  assert.equal(state.metrics.some(({ event }) => event === 'gpu_completion'), false);
  state.renderer.dispose();
});

test('disposing a stalled renderer cancels its completion hold and releases resources once', async () => {
  const completion = deferred();
  const state = await initializedRenderer({ completionGate: completion.promise });
  const result = state.renderer.render({}, {
    epoch: 2, sequence: 11, offeredWallMillis: Date.now()
  });
  const watchdog = state.timers.find((timer) => !timer.cancelled);
  assert.ok(watchdog);
  state.renderer.dispose();
  await assert.rejects(result, /renderer_disposed/);
  assert.equal(watchdog.cancelled, true);
  assert.equal(state.renderer.completionWaits.size, 0);
  assert.equal(state.stats.textureDestroyCount, 1);
  assert.equal(state.stats.bufferDestroyCount, 1);
  assert.equal(state.stats.deviceDestroyCount, 1);
  assert.deepEqual(state.failures, [], 'intentional disposal is not reported as a GPU failure');
  completion.resolve();
  await tick();
  state.renderer.dispose();
  assert.equal(state.stats.textureDestroyCount, 1);
  assert.equal(state.stats.deviceDestroyCount, 1);
});

test('a stalled present completion is bounded and reports one terminal failure', async () => {
  const presentCompletion = deferred();
  const state = await initializedRenderer({
    completionGates: [Promise.resolve(), presentCompletion.promise]
  });
  await state.renderer.render({}, { epoch: 2, sequence: 12, offeredWallMillis: Date.now() });
  state.renderer.present();
  const watchdog = state.timers.findLast((timer) => !timer.cancelled);
  assert.ok(watchdog);
  assert.equal(watchdog.millis, CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS);
  watchdog.callback();
  await tick();
  assert.deepEqual(state.failures, ['present_completion_timeout']);
  assert.equal(state.renderer.completionWaits.size, 0);
  presentCompletion.resolve();
  await tick();
  assert.deepEqual(state.failures, ['present_completion_timeout']);
  state.renderer.dispose();
  assert.equal(state.stats.textureDestroyCount, 1);
});

test('present validation errors fail closed after the staged copy is submitted', async () => {
  const state = await initializedRenderer({
    presentValidationError: new Error('synthetic present validation error')
  });
  await state.renderer.render({}, { epoch: 2, sequence: 13, offeredWallMillis: Date.now() });
  state.renderer.present();
  await tick();
  assert.equal(state.failures.length, 1);
  assert.match(state.failures[0], /present_validation_failed/);
  assert.equal(state.stats.textureCopies.length, 1);
  state.renderer.dispose();
});

test('a failed submit surfaces the error and disposal releases the reduced resource set', async () => {
  const state = await initializedRenderer({ submitError: true });
  await assert.rejects(
    state.renderer.render({}, { epoch: 1, sequence: 1, offeredWallMillis: Date.now() }),
    /render_submit_failed/
  );
  state.renderer.dispose();
  assert.equal(state.stats.bufferDestroyCount, 1);
  assert.equal(state.stats.contextUnconfigureCount >= 1, true);
  assert.equal(state.stats.deviceDestroyCount, 1);
});
