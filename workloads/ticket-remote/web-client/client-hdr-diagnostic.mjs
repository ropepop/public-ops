import {
  CLIENT_HDR_CANVAS_FORMAT,
  CLIENT_HDR_FALLBACK_COLOR_SPACE,
  CLIENT_HDR_LINEAR_COLOR_SPACE
} from './client-hdr-renderer.mjs';

export const CLIENT_HDR_DIAGNOSTIC_CANVAS_FORMAT = CLIENT_HDR_CANVAS_FORMAT;
export const CLIENT_HDR_DIAGNOSTIC_LINEAR_COLOR_SPACE = CLIENT_HDR_LINEAR_COLOR_SPACE;
export const CLIENT_HDR_DIAGNOSTIC_FALLBACK_COLOR_SPACE = CLIENT_HDR_FALLBACK_COLOR_SPACE;
export const CLIENT_HDR_DIAGNOSTIC_COLORS = Object.freeze([
  'red', 'green', 'blue', 'cyan', 'magenta', 'yellow', 'orange', 'white'
]);

export const CLIENT_HDR_DIAGNOSTIC_SHADER = `
struct VertexOutput {
  @builtin(position) position: vec4<f32>,
  @location(0) uv: vec2<f32>,
}

struct DiagnosticParams {
  options: vec4<f32>,
}

@group(0) @binding(0) var<uniform> diagnosticParams: DiagnosticParams;

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

fn linearToExtendedSrgb(value: vec3<f32>) -> vec3<f32> {
  let safe = max(value, vec3<f32>(0.0));
  let low = safe * vec3<f32>(12.92);
  let high = vec3<f32>(1.055) * pow(safe, vec3<f32>(1.0 / 2.4)) - vec3<f32>(0.055);
  return select(high, low, safe <= vec3<f32>(0.0031308));
}

@fragment
fn fragmentMain(fragmentIn: VertexOutput) -> @location(0) vec4<f32> {
  var peak = 1.0;
  if (fragmentIn.uv.x >= (1.0 / 3.0) && fragmentIn.uv.x < (2.0 / 3.0)) {
    peak = 2.0;
  } else if (fragmentIn.uv.x >= (2.0 / 3.0)) {
    peak = 4.0;
  }
  let rowPosition = fragmentIn.uv.y * 8.0;
  var hueRGB = vec3<f32>(1.0, 0.0, 0.0);
  if (rowPosition >= 1.0 && rowPosition < 2.0) {
    hueRGB = vec3<f32>(0.0, 1.0, 0.0);
  } else if (rowPosition >= 2.0 && rowPosition < 3.0) {
    hueRGB = vec3<f32>(0.0, 0.0, 1.0);
  } else if (rowPosition >= 3.0 && rowPosition < 4.0) {
    hueRGB = vec3<f32>(0.0, 1.0, 1.0);
  } else if (rowPosition >= 4.0 && rowPosition < 5.0) {
    hueRGB = vec3<f32>(1.0, 0.0, 1.0);
  } else if (rowPosition >= 5.0 && rowPosition < 6.0) {
    hueRGB = vec3<f32>(1.0, 1.0, 0.0);
  } else if (rowPosition >= 6.0 && rowPosition < 7.0) {
    hueRGB = vec3<f32>(1.0, 0.21404114, 0.0);
  } else if (rowPosition >= 7.0) {
    hueRGB = vec3<f32>(1.0, 1.0, 1.0);
  }
  let linear = hueRGB * peak;
  let encoded = select(linear, linearToExtendedSrgb(linear), diagnosticParams.options.x > 0.5);
  return vec4<f32>(encoded, 1.0);
}
`;

function waitForPaints(environment, count = 2) {
  const requestPaint = environment && environment.requestAnimationFrame;
  if (typeof requestPaint !== 'function') return Promise.resolve('unavailable');
  return new Promise((resolve) => {
    const wait = (remaining) => {
      requestPaint(() => {
        if (remaining <= 1) resolve('paint');
        else wait(remaining - 1);
      });
    };
    wait(Math.max(1, Math.round(Number(count) || 1)));
  });
}

function applyDynamicRangeLimit(environment, canvas, value) {
  canvas.style.setProperty('dynamic-range-limit', value);
  if (!environment || typeof environment.getComputedStyle !== 'function') {
    void canvas.offsetWidth;
    return value;
  }
  return String(environment.getComputedStyle(canvas).getPropertyValue('dynamic-range-limit')).trim() || value;
}

function configurationMatches(context, candidate, usage) {
  if (!context || typeof context.getConfiguration !== 'function') return true;
  const configuration = context.getConfiguration();
  return Boolean(configuration &&
    configuration.format === CLIENT_HDR_DIAGNOSTIC_CANVAS_FORMAT &&
    configuration.colorSpace === candidate.colorSpace &&
    (Number(configuration.usage) & usage) === usage &&
    configuration.toneMapping && configuration.toneMapping.mode === 'extended');
}

export async function renderClientHDRDiagnosticPatches(canvas, options = {}) {
  const environment = options.environment || globalThis;
  if (!canvas || typeof canvas.getContext !== 'function' || !canvas.style) {
    throw new Error('hdr_diagnostic_canvas_unavailable');
  }
  const navigatorValue = environment && environment.navigator;
  if (!navigatorValue || !navigatorValue.gpu) throw new Error('hdr_diagnostic_webgpu_unavailable');
  if (!environment.GPUTextureUsage || !environment.GPUBufferUsage) {
    throw new Error('hdr_diagnostic_usage_constants_unavailable');
  }

  canvas.width = Math.max(3, Math.round(Number(options.width || canvas.width || 720)));
  canvas.height = Math.max(CLIENT_HDR_DIAGNOSTIC_COLORS.length,
    Math.round(Number(options.height || canvas.height || 480)));
  if (applyDynamicRangeLimit(environment, canvas, 'no-limit') !== 'no-limit') {
    throw new Error('hdr_diagnostic_no_limit_unavailable');
  }

  const context = canvas.getContext('webgpu');
  if (!context) throw new Error('hdr_diagnostic_webgpu_context_unavailable');
  const adapter = await navigatorValue.gpu.requestAdapter();
  if (!adapter) throw new Error('hdr_diagnostic_adapter_unavailable');
  const device = await adapter.requestDevice();
  const usage = environment.GPUTextureUsage.RENDER_ATTACHMENT;
  const candidates = [
    { colorSpace: CLIENT_HDR_DIAGNOSTIC_LINEAR_COLOR_SPACE, encodeOutput: false },
    { colorSpace: CLIENT_HDR_DIAGNOSTIC_FALLBACK_COLOR_SPACE, encodeOutput: true }
  ];
  let selected = null;
  try {
    for (const candidate of candidates) {
      try {
        context.configure({
          device,
          format: CLIENT_HDR_DIAGNOSTIC_CANVAS_FORMAT,
          alphaMode: 'opaque',
          colorSpace: candidate.colorSpace,
          toneMapping: { mode: 'extended' },
          usage
        });
        if (!configurationMatches(context, candidate, usage)) throw new Error('configuration_mismatch');
        selected = candidate;
        break;
      } catch (_) {
        try { context.unconfigure(); } catch (_) {}
      }
    }
    if (!selected) throw new Error('hdr_diagnostic_extended_mode_unavailable');

    const module = device.createShaderModule({ code: CLIENT_HDR_DIAGNOSTIC_SHADER });
    if (typeof module.getCompilationInfo === 'function') {
      const info = await module.getCompilationInfo();
      const shaderError = info && Array.isArray(info.messages)
        ? info.messages.find((message) => message && message.type === 'error')
        : null;
      if (shaderError) {
        const line = Number(shaderError.lineNum);
        const column = Number(shaderError.linePos);
        const location = Number.isFinite(line) && line > 0
          ? `${line}:${Number.isFinite(column) && column > 0 ? column : 1}:`
          : '';
        throw new Error(`hdr_diagnostic_shader_failed:${location}${shaderError.message || shaderError}`.slice(0, 160));
      }
    }
    const pipelineDescriptor = {
      layout: 'auto',
      vertex: { module, entryPoint: 'vertexMain' },
      fragment: {
        module,
        entryPoint: 'fragmentMain',
        targets: [{ format: CLIENT_HDR_DIAGNOSTIC_CANVAS_FORMAT }]
      },
      primitive: { topology: 'triangle-list' }
    };
    const pipeline = typeof device.createRenderPipelineAsync === 'function'
      ? await device.createRenderPipelineAsync(pipelineDescriptor)
      : device.createRenderPipeline(pipelineDescriptor);
    const paramsBuffer = device.createBuffer({
      size: 16,
      usage: environment.GPUBufferUsage.UNIFORM | environment.GPUBufferUsage.COPY_DST
    });
    device.queue.writeBuffer(paramsBuffer, 0, new Float32Array([selected.encodeOutput ? 1 : 0, 0, 0, 0]));
    const bindGroup = device.createBindGroup({
      layout: pipeline.getBindGroupLayout(0),
      entries: [{ binding: 0, resource: { buffer: paramsBuffer } }]
    });

    const encoder = device.createCommandEncoder();
    const pass = encoder.beginRenderPass({
      colorAttachments: [{
        view: context.getCurrentTexture().createView(),
        clearValue: { r: 0, g: 0, b: 0, a: 1 },
        loadOp: 'clear',
        storeOp: 'store'
      }]
    });
    pass.setPipeline(pipeline);
    pass.setBindGroup(0, bindGroup);
    pass.draw(3);
    pass.end();
    device.queue.submit([encoder.finish()]);
    if (device.queue && typeof device.queue.onSubmittedWorkDone === 'function') {
      await device.queue.onSubmittedWorkDone();
    }
    if (await waitForPaints(environment) !== 'paint') throw new Error('hdr_diagnostic_present_paint_unavailable');

    return {
      canvasEncoding: selected.colorSpace === CLIENT_HDR_DIAGNOSTIC_LINEAR_COLOR_SPACE ? 'srgb-linear' : 'srgb-encoded',
      intendedPeaks: [1, 2, 4],
      intendedColors: Array.from(CLIENT_HDR_DIAGNOSTIC_COLORS),
      dynamicRangeLimit: 'no-limit',
      dispose() {
        try { paramsBuffer.destroy(); } catch (_) {}
        try { context.unconfigure(); } catch (_) {}
        try { device.destroy(); } catch (_) {}
      }
    };
  } catch (error) {
    try { context.unconfigure(); } catch (_) {}
    try { device.destroy(); } catch (_) {}
    throw error;
  }
}
