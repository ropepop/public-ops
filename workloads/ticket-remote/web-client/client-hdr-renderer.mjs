export const CLIENT_HDR_CANVAS_FORMAT = 'rgba16float';
export const CLIENT_HDR_LINEAR_COLOR_SPACE = 'srgb-linear';
export const CLIENT_HDR_FALLBACK_COLOR_SPACE = 'srgb';
export const CLIENT_HDR_ALLOWED_BOOSTS = Object.freeze([2, 3, 4, 5, 6]);
export const CLIENT_HDR_DEFAULT_BOOST = 4;
export const CLIENT_HDR_INTERNAL_IDENTITY_BOOST = 1;
export const CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS = 1500;
export const CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS = 2000;

export const CLIENT_HDR_SHADER = `
struct VertexOutput {
  @builtin(position) position: vec4<f32>,
  @location(0) uv: vec2<f32>,
}

struct HDRParams {
  options: vec4<f32>,
  picture: vec4<f32>,
  // Visible right/bottom bounds, then the source pixel used outside the picture.
  fill: vec4<f32>,
}

@group(0) @binding(0) var sourceFrame: texture_external;
@group(0) @binding(1) var sourceSampler: sampler;
@group(0) @binding(2) var<uniform> hdr: HDRParams;

@vertex
fn vertexMain(@builtin(vertex_index) index: u32) -> VertexOutput {
  var positions = array<vec2<f32>, 3>(
    vec2<f32>(-1.0, -1.0),
    vec2<f32>(3.0, -1.0),
    vec2<f32>(-1.0, 3.0)
  );
  var vertexOut: VertexOutput;
  let vertexPosition = positions[index];
  vertexOut.position = vec4<f32>(vertexPosition, 0.0, 1.0);
  vertexOut.uv = vec2<f32>((vertexPosition.x + 1.0) * 0.5, 1.0 - ((vertexPosition.y + 1.0) * 0.5));
  return vertexOut;
}

fn srgbToLinear(value: vec3<f32>) -> vec3<f32> {
  let low = value / vec3<f32>(12.92);
  let high = pow((value + vec3<f32>(0.055)) / vec3<f32>(1.055), vec3<f32>(2.4));
  return select(high, low, value <= vec3<f32>(0.04045));
}

fn linearToExtendedSrgb(value: vec3<f32>) -> vec3<f32> {
  let safe = max(value, vec3<f32>(0.0));
  let low = safe * vec3<f32>(12.92);
  let high = vec3<f32>(1.055) * pow(safe, vec3<f32>(1.0 / 2.4)) - vec3<f32>(0.055);
  return select(high, low, safe <= vec3<f32>(0.0031308));
}

fn encodeCanvas(linear: vec3<f32>) -> vec3<f32> {
  return select(linear, linearToExtendedSrgb(linear), hdr.options.y > 0.5);
}

@fragment
fn fragmentMain(fragmentIn: VertexOutput) -> @location(0) vec4<f32> {
  let inside = fragmentIn.uv.x >= hdr.picture.x && fragmentIn.uv.y >= hdr.picture.y &&
    fragmentIn.uv.x < hdr.fill.x && fragmentIn.uv.y < hdr.fill.y;
  let pictureUV = (fragmentIn.uv - hdr.picture.xy) / hdr.picture.zw;
  let sourceUV = select(hdr.fill.zw, pictureUV, inside);
  let encodedRGB = clamp(
    textureSampleBaseClampToEdge(sourceFrame, sourceSampler, sourceUV).rgb,
    vec3<f32>(0.0),
    vec3<f32>(1.0)
  );
  let linearRGB = srgbToLinear(encodedRGB);
  // One linear-light multiplier preserves gray/white contrast and RGB ratios.
  // The external texture is sRGB encoded; decode once before applying gain.
  let mappedLinearRGB = linearRGB * hdr.options.x;
  let edrRequestSample = hdr.options.z > 0.5 &&
    inside && pictureUV.x >= 0.0 && pictureUV.x < 0.002 &&
    pictureUV.y >= 0.0 && pictureUV.y < 0.002;
  let finalLinearRGB = select(mappedLinearRGB, vec3<f32>(1.25), edrRequestSample);
  return vec4<f32>(encodeCanvas(finalLinearRGB), 1.0);
}
`;

function finiteNumber(value, fallback = 0) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

export function isClientHDRBoost(value) {
  const boost = Number(value);
  return Number.isFinite(boost) && CLIENT_HDR_ALLOWED_BOOSTS.includes(boost);
}

function requireClientHDRBoost(value) {
  const boost = Number(value);
  if (!isClientHDRBoost(boost)) throw new Error('hdr_boost_invalid');
  return boost;
}

function gpuErrorReason(prefix, error) {
  const detail = String(error && error.message || error || 'validation_failed').replace(/\s+/g, '_').slice(0, 48);
  return `${prefix}:${detail}`.slice(0, 80);
}

async function withValidationScope(device, reason, operation) {
  const scoped = Boolean(device && typeof device.pushErrorScope === 'function' && typeof device.popErrorScope === 'function');
  let scopeOpen = false;
  if (scoped) {
    device.pushErrorScope('validation');
    scopeOpen = true;
  }
  try {
    const result = await operation();
    if (scopeOpen) {
      scopeOpen = false;
      const validationError = await device.popErrorScope();
      if (validationError) throw new Error(gpuErrorReason(reason, validationError));
    }
    return result;
  } catch (error) {
    if (scopeOpen) {
      try { await device.popErrorScope(); } catch (_) {}
    }
    throw error;
  }
}

async function assertShaderCompilation(module) {
  if (!module || typeof module.getCompilationInfo !== 'function') return;
  const info = await module.getCompilationInfo();
  const error = info && Array.isArray(info.messages)
    ? info.messages.find((message) => message && message.type === 'error')
    : null;
  if (error) {
    const line = Number(error.lineNum);
    const column = Number(error.linePos);
    const location = Number.isFinite(line) && line > 0
      ? `${line}:${Number.isFinite(column) && column > 0 ? column : 1}:`
      : '';
    throw new Error(gpuErrorReason('shader_compilation_failed', `${location}${error.message || error}`));
  }
}

function configurationMatches(context, candidate, requiredUsage) {
  if (!context || typeof context.getConfiguration !== 'function') return false;
  const configuration = context.getConfiguration();
  return Boolean(configuration &&
    configuration.format === CLIENT_HDR_CANVAS_FORMAT &&
    configuration.colorSpace === candidate.colorSpace &&
    (finiteNumber(configuration.usage) & requiredUsage) === requiredUsage &&
    configuration.toneMapping && configuration.toneMapping.mode === 'extended');
}

function applyDynamicRangeLimit(environment, canvas, value) {
  const requested = String(value || 'standard');
  if (canvas && canvas.style && typeof canvas.style.setProperty === 'function') {
    canvas.style.setProperty('dynamic-range-limit', requested);
  }
  let computed = requested;
  try {
    if (environment && typeof environment.getComputedStyle === 'function') {
      computed = String(environment.getComputedStyle(canvas).getPropertyValue('dynamic-range-limit')).trim();
    } else if (canvas) {
      // Reading layout forces WebKit to commit the preceding style change even
      // when getComputedStyle is unavailable in a test or older browser.
      void canvas.offsetWidth;
    }
  } catch (_) {}
  return computed || requested;
}

function waitForDisplayRefresh(
  environment,
  setTimer,
  clearTimer,
  requiredFrames = 2,
  timeoutMillis = CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS
) {
  const requestFrame = environment && environment.requestAnimationFrame;
  const frameTarget = Math.max(1, Math.round(finiteNumber(requiredFrames, 2)));
  const deadlineMillis = Math.max(1, Math.round(finiteNumber(
    timeoutMillis,
    CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS
  )));
  let resolveWait;
  let rejectWait;
  const promise = new Promise((resolve, reject) => {
    resolveWait = resolve;
    rejectWait = reject;
  });
  const wait = {
    settled: false,
    timer: null,
    frames: new Set(),
    completedFrames: 0,
    result: null,
    cancel: null,
    promise
  };
  const cancelFrames = () => {
    if (!environment || typeof environment.cancelAnimationFrame !== 'function') {
      wait.frames.clear();
      return;
    }
    for (const frame of wait.frames) {
      try { environment.cancelAnimationFrame(frame); } catch (_) {}
    }
    wait.frames.clear();
  };
  const settle = (source, rejection = '') => {
    if (wait.settled) return false;
    wait.settled = true;
    if (wait.timer !== null) {
      try { clearTimer(wait.timer); } catch (_) {}
      wait.timer = null;
    }
    cancelFrames();
    wait.result = {
      postPresentSource: String(source || 'failed'),
      postPresentOpportunityCount: wait.completedFrames,
      compositorOpportunitiesCompleted: source === 'animation_frame' && wait.completedFrames === frameTarget,
      settlementDeadlineMillis: deadlineMillis,
      settlementTimedOut: source === 'timeout'
    };
    if (rejection) rejectWait(new Error(String(rejection).slice(0, 80)));
    else resolveWait(wait.result);
    return true;
  };
  wait.cancel = (reason = 'renderer_disposed') => settle('cancelled', reason);
  if (typeof requestFrame !== 'function') {
    settle('unavailable');
    return wait;
  }
  const waitFrames = () => {
    if (wait.settled) return;
    let frame = null;
    try {
      frame = requestFrame(() => {
        if (frame !== null) wait.frames.delete(frame);
        if (wait.settled) return;
        wait.completedFrames += 1;
        if (wait.completedFrames === frameTarget) settle('animation_frame');
        else waitFrames();
      });
      if (!wait.settled) wait.frames.add(frame);
    } catch (_) {
      settle('failed');
    }
  };
  try {
    waitFrames();
    if (!wait.settled) {
      const timer = setTimer(() => settle('timeout'), deadlineMillis);
      wait.timer = timer;
      if (wait.settled) {
        try { clearTimer(timer); } catch (_) {}
        wait.timer = null;
      }
    }
  } catch (_) {
    settle('failed');
  }
  return wait;
}

export class ClientHDRRenderer {
  constructor(options = {}) {
    this.environment = options.environment || globalThis;
    this.wallNow = options.wallNow || (() => Date.now());
    this.setTimer = options.setTimer || ((callback, millis) => setTimeout(callback, millis));
    this.clearTimer = options.clearTimer || ((timer) => clearTimeout(timer));
    this.gpuCompletionTimeoutMillis = Math.max(1, Math.round(finiteNumber(
      options.gpuCompletionTimeoutMillis,
      CLIENT_HDR_GPU_COMPLETION_TIMEOUT_MILLIS
    )));
    this.compositorSettlementTimeoutMillis = Math.max(1, Math.round(finiteNumber(
      options.compositorSettlementTimeoutMillis,
      CLIENT_HDR_DISPLAY_REFRESH_TIMEOUT_MILLIS
    )));
    this.onFailure = options.onFailure || (() => {});
    this.disposed = false;
    this.failed = false;
    this.canvas = null;
    this.context = null;
    this.canvasConfiguration = null;
    this.device = null;
    this.preparation = null;
    this.paramsBuffer = null;
    this.stagingTexture = null;
    this.prepared = false;
    this.presenting = false;
    this.boost = CLIENT_HDR_DEFAULT_BOOST;
    this.picture = { left: 0, top: 0, width: 1, height: 1, right: 1, bottom: 1 };
    this.encodeOutput = false;
    this.onUncapturedError = null;
    this.completionWaits = new Set();
    this.compositorSettlementWaits = new Set();
  }

  prepare() {
    if (this.disposed) return Promise.reject(new Error('renderer_disposed'));
    if (this.preparation) return this.preparation;
    this.preparation = (async () => {
      const navigatorValue = this.environment && this.environment.navigator;
      if (!navigatorValue || !navigatorValue.gpu) throw new Error('webgpu_unavailable');
      const adapter = await navigatorValue.gpu.requestAdapter();
      if (this.disposed) throw new Error('renderer_disposed');
      if (!adapter) throw new Error('webgpu_adapter_unavailable');
      const device = await adapter.requestDevice();
      if (this.disposed) {
        try { device.destroy(); } catch (_) {}
        throw new Error('renderer_disposed');
      }
      this.device = device;
      await this.createResources();
      if (this.disposed) { this.releaseGPUResources(); throw new Error('renderer_disposed'); }
      this.installDeviceFailureHandlers();
    })();
    return this.preparation;
  }

  async initialize({ canvas, width, height, boost = CLIENT_HDR_DEFAULT_BOOST, picture }) {
    if (this.disposed) throw new Error('renderer_disposed');
    if (!canvas || typeof canvas.getContext !== 'function') throw new Error('webgpu_canvas_unavailable');
    this.boost = requireClientHDRBoost(boost);
    if (picture) this.picture = picture;
    this.canvas = canvas;
    this.canvas.width = Math.max(1, Math.round(finiteNumber(width, canvas.width || 1)));
    this.canvas.height = Math.max(1, Math.round(finiteNumber(height, canvas.height || 1)));
    // The fresh surface is unrestricted before WebGPU configuration. It stays
    // behind authoritative SDR until the controller has prepared an exact
    // SDR-identity activation frame for the first visible swapchain copy.
    const configurationDynamicRangeLimit = applyDynamicRangeLimit(
      this.environment,
      this.canvas,
      'no-limit'
    );
    if (configurationDynamicRangeLimit !== 'no-limit') {
      throw new Error('hdr_no_limit_dynamic_range_unavailable');
    }
    this.context = canvas.getContext('webgpu');
    if (!this.context) throw new Error('webgpu_canvas_unavailable');
    await this.prepare();
    if (this.disposed || this.failed) throw new Error('renderer_not_ready');
    const textureUsage = this.environment.GPUTextureUsage;
    if (!textureUsage) throw new Error('webgpu_usage_constants_unavailable');
    const canvasUsage = textureUsage.RENDER_ATTACHMENT | textureUsage.COPY_DST;
    const candidates = [
      { colorSpace: CLIENT_HDR_LINEAR_COLOR_SPACE, encodeOutput: false },
      { colorSpace: CLIENT_HDR_FALLBACK_COLOR_SPACE, encodeOutput: true }
    ];
    let configured = null;
    for (const candidate of candidates) {
      const configuration = {
        device: this.device,
        format: CLIENT_HDR_CANVAS_FORMAT,
        alphaMode: 'opaque',
        colorSpace: candidate.colorSpace,
        toneMapping: { mode: 'extended' },
        usage: canvasUsage
      };
      try {
        await withValidationScope(this.device, 'canvas_configuration_failed', async () => {
          this.context.configure(configuration);
        });
        if (!configurationMatches(this.context, candidate, canvasUsage)) throw new Error('configuration_mismatch');
        this.canvasConfiguration = configuration;
        configured = candidate;
        break;
      } catch (_) {
        try { this.context.unconfigure(); } catch (_) {}
      }
    }
    if (!configured) throw new Error('hdr_canvas_extended_mode_unavailable');
    this.encodeOutput = configured.encodeOutput;
    try {
      await withValidationScope(this.device, 'pipeline_validation_failed', async () => {
        this.stagingTexture = this.device.createTexture({
          size: { width: this.canvas.width, height: this.canvas.height, depthOrArrayLayers: 1 },
          format: CLIENT_HDR_CANVAS_FORMAT,
          usage: textureUsage.RENDER_ATTACHMENT | textureUsage.COPY_SRC
        });
      });
    } catch (error) {
      this.releaseGPUResources();
      throw error;
    }
    if (this.disposed) {
      this.releaseGPUResources();
      throw new Error('renderer_disposed');
    }
    this.writeParameters();

  }

  async createResources() {
    const bufferUsage = this.environment.GPUBufferUsage;
    const textureUsage = this.environment.GPUTextureUsage;
    if (!bufferUsage || !textureUsage) throw new Error('webgpu_usage_constants_unavailable');
    let paramsBuffer = null;
    try {
      const resources = await withValidationScope(this.device, 'pipeline_validation_failed', async () => {
        const module = this.device.createShaderModule({ code: CLIENT_HDR_SHADER });
        await assertShaderCompilation(module);
        const descriptor = {
          layout: 'auto',
          vertex: { module, entryPoint: 'vertexMain' },
          fragment: { module, entryPoint: 'fragmentMain', targets: [{ format: CLIENT_HDR_CANVAS_FORMAT }] },
          primitive: { topology: 'triangle-list' }
        };
        const pipeline = typeof this.device.createRenderPipelineAsync === 'function'
          ? await this.device.createRenderPipelineAsync(descriptor)
          : this.device.createRenderPipeline(descriptor);
        const sourceSampler = this.device.createSampler({ minFilter: 'linear', magFilter: 'linear' });
        paramsBuffer = this.device.createBuffer({
          size: 48,
          usage: bufferUsage.UNIFORM | bufferUsage.COPY_DST
        });
        return { module, pipeline, sourceSampler, paramsBuffer };
      });
      if (this.disposed) throw new Error('renderer_disposed');
      Object.assign(this, resources);
    } catch (error) {
      try { if (paramsBuffer) paramsBuffer.destroy(); } catch (_) {}
      throw error;
    }
  }

  installDeviceFailureHandlers() {
    const initializedDevice = this.device;
    this.onUncapturedError = (event) => {
      try { if (event && typeof event.preventDefault === 'function') event.preventDefault(); } catch (_) {}
      this.reportFailure(gpuErrorReason('uncaptured_gpu_error', event && event.error));
    };
    if (typeof initializedDevice.addEventListener === 'function') {
      initializedDevice.addEventListener('uncapturederror', this.onUncapturedError);
    }
    if (initializedDevice.lost && typeof initializedDevice.lost.then === 'function') {
      initializedDevice.lost.then((info) => {
        if (this.disposed || this.device !== initializedDevice || (info && info.reason === 'destroyed')) return;
        this.reportFailure('device_lost');
      }).catch(() => {});
    }
  }

  reportFailure(reason) {
    if (this.disposed || this.failed) return;
    this.failed = true;
    this.onFailure(String(reason || 'render_failed').slice(0, 80));
  }

  boundedGPUCompletion(completion, timeoutReason = 'gpu_completion_timeout') {
    const startedWallAt = this.wallNow();
    let resolveWait;
    let rejectWait;
    const promise = new Promise((resolve, reject) => {
      resolveWait = resolve;
      rejectWait = reject;
    });
    const wait = {
      settled: false,
      timer: null,
      cancel: (reason = 'renderer_disposed') => {
        if (wait.settled) return false;
        wait.settled = true;
        if (wait.timer !== null) {
          try { this.clearTimer(wait.timer); } catch (_) {}
          wait.timer = null;
        }
        this.completionWaits.delete(wait);
        rejectWait(new Error(String(reason || 'renderer_disposed').slice(0, 80)));
        return true;
      }
    };
    const settle = (callback, value) => {
      if (wait.settled) return;
      wait.settled = true;
      if (wait.timer !== null) {
        try { this.clearTimer(wait.timer); } catch (_) {}
        wait.timer = null;
      }
      this.completionWaits.delete(wait);
      callback(value);
    };
    this.completionWaits.add(wait);
    try {
      const timer = this.setTimer(() => {
        settle(rejectWait, new Error(String(timeoutReason || 'gpu_completion_timeout').slice(0, 80)));
      }, this.gpuCompletionTimeoutMillis);
      wait.timer = timer;
      if (wait.settled) {
        try { this.clearTimer(timer); } catch (_) {}
        wait.timer = null;
      }
    } catch (error) {
      settle(rejectWait, new Error(gpuErrorReason('gpu_completion_watchdog_failed', error)));
    }
    Promise.resolve(completion).then(
      (value) => {
        if (this.wallNow() - startedWallAt >= this.gpuCompletionTimeoutMillis) {
          settle(rejectWait, new Error(String(timeoutReason || 'gpu_completion_timeout').slice(0, 80)));
          return;
        }
        settle(resolveWait, value);
      },
      (error) => settle(rejectWait, new Error(gpuErrorReason('gpu_completion_failed', error)))
    );
    return { promise, cancel: wait.cancel };
  }

  cancelGPUCompletionWaits(reason = 'renderer_disposed') {
    for (const wait of Array.from(this.completionWaits)) wait.cancel(reason);
  }

  async waitForCompositorSettlement(requiredFrames = 2) {
    const wait = waitForDisplayRefresh(
      this.environment, this.setTimer, this.clearTimer,
      requiredFrames, this.compositorSettlementTimeoutMillis
    );
    this.compositorSettlementWaits.add(wait);
    try {
      const result = await wait.promise;
      if (this.disposed) throw new Error('renderer_disposed');
      if (!result.compositorOpportunitiesCompleted) {
        throw new Error(`hdr_display_refresh_${result.postPresentSource}`);
      }
    } finally {
      this.compositorSettlementWaits.delete(wait);
    }
  }

  cancelCompositorSettlementWaits(reason = 'renderer_disposed') {
    for (const wait of Array.from(this.compositorSettlementWaits)) wait.cancel(reason);
    this.compositorSettlementWaits.clear();
  }

  writeParameters(boost = this.boost, requestPatch = false, frame = null) {
    if (!this.device || !this.paramsBuffer) throw new Error('renderer_not_ready');
    const { left, top, width, height, right, bottom } = this.picture;
    const sourceWidth = frame?.displayWidth || frame?.codedWidth || frame?.width || 1;
    const sourceHeight = frame?.displayHeight || frame?.codedHeight || frame?.height || 1;
    this.device.queue.writeBuffer(this.paramsBuffer, 0, new Float32Array([
      boost, this.encodeOutput ? 1 : 0, requestPatch === true ? 1 : 0, 0,
      left, top, width, height,
      right, bottom,
      (Math.floor(sourceWidth * 0.01) + 0.5) / sourceWidth,
      (Math.floor(sourceHeight * 0.98) + 0.5) / sourceHeight
    ]));
  }

  setBoost(value) {
    if (this.disposed) throw new Error('renderer_disposed');
    const boost = requireClientHDRBoost(value);
    if (boost === this.boost) return this.boost;
    this.writeParameters(boost);
    this.boost = boost;
    return this.boost;
  }

  createSourceBindGroup(frame) {
    const externalTexture = this.device.importExternalTexture({ source: frame, colorSpace: CLIENT_HDR_FALLBACK_COLOR_SPACE });
    return this.device.createBindGroup({
      layout: this.pipeline.getBindGroupLayout(0),
      entries: [
        { binding: 0, resource: externalTexture },
        { binding: 1, resource: this.sourceSampler },
        { binding: 2, resource: { buffer: this.paramsBuffer } }
      ]
    });
  }

  beginPass(encoder, view, bindGroup) {
    const pass = encoder.beginRenderPass({
      colorAttachments: [{
        view,
        clearValue: { r: 0, g: 0, b: 0, a: 1 },
        loadOp: 'clear',
        storeOp: 'store'
      }]
    });
    pass.setPipeline(this.pipeline);
    pass.setBindGroup(0, bindGroup);
    pass.draw(3);
    pass.end();
  }

  async submitAndWait(operation, reason) {
    const device = this.device;
    if (this.disposed || this.failed || !device) throw new Error('renderer_not_ready');
    try {
      const completion = withValidationScope(device, reason, async () => {
        operation(device);
        // Presentation authority requires actual GPU completion support.
        await device.queue.onSubmittedWorkDone();
      });
      await this.boundedGPUCompletion(completion, `${reason}_timeout`).promise;
      if (this.disposed || this.device !== device) throw new Error('renderer_disposed');
    } catch (error) {
      this.reportFailure(gpuErrorReason(reason, error));
      throw error;
    }
  }

  async render(frame, options = {}) {
    if (!this.context || this.prepared || this.presenting) throw new Error('renderer_present_required');
    const activationFrame = options.activationFrame === true;
    this.writeParameters(activationFrame ? CLIENT_HDR_INTERNAL_IDENTITY_BOOST : this.boost,
      options.requestPatch === true && this.boost > 1, frame);
    await this.submitAndWait((device) => {
      const encoder = device.createCommandEncoder();
      this.beginPass(encoder, this.stagingTexture.createView(), this.createSourceBindGroup(frame));
      device.queue.submit([encoder.finish()]);
    }, 'render_failed');
    this.prepared = true;
  }

  async present({ reconfigure = false } = {}) {
    if (!this.prepared || this.presenting || !this.context || !this.stagingTexture) {
      throw new Error('renderer_frame_not_prepared');
    }
    if (reconfigure && !this.canvasConfiguration) throw new Error('renderer_not_ready');
    this.presenting = true;
    this.prepared = false;
    try {
      await this.submitAndWait((device) => {
        const encoder = device.createCommandEncoder();
        // Only the bounded foreground follow-up reasserts HDR. Configuration
        // clears the canvas, so copy the prepared picture without yielding.
        if (reconfigure) this.context.configure(this.canvasConfiguration);
        encoder.copyTextureToTexture(
          { texture: this.stagingTexture }, { texture: this.context.getCurrentTexture() },
          { width: this.canvas.width, height: this.canvas.height, depthOrArrayLayers: 1 }
        );
        device.queue.submit([encoder.finish()]);
      }, 'present_failed');
    } finally {
      this.presenting = false;
    }
  }

  discardPreparedFrame() {
    if (this.presenting) return false;
    this.prepared = false;
    return true;
  }

  releaseGPUResources() {
    this.cancelGPUCompletionWaits('renderer_disposed');
    this.cancelCompositorSettlementWaits('renderer_disposed');
    try {
      if (this.device && this.onUncapturedError && typeof this.device.removeEventListener === 'function') {
        this.device.removeEventListener('uncapturederror', this.onUncapturedError);
      }
    } catch (_) {}
    try { if (this.paramsBuffer) this.paramsBuffer.destroy(); } catch (_) {}
    try { if (this.stagingTexture) this.stagingTexture.destroy(); } catch (_) {}
    try { if (this.context) this.context.unconfigure(); } catch (_) {}
    try { if (this.device) this.device.destroy(); } catch (_) {}
    this.paramsBuffer = null;
    this.stagingTexture = null;
    this.prepared = false;
    this.presenting = false;
    this.device = null;
    this.context = null;
    this.canvasConfiguration = null;
    this.onUncapturedError = null;
  }

  dispose() {
    this.disposed = true;
    this.releaseGPUResources();
  }
}
