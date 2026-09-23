import test from 'node:test';
import assert from 'node:assert/strict';
import { ClientHDRRenderer } from './client-hdr-renderer.mjs';

const deferred = () => {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
};

test('validated GPU work completes and releases its deadline', async () => {
  const queue = deferred(), validation = deferred();
  const { renderer, failures } = rendererWith(queue, validation);
  const work = renderer.submitAndWait(() => {}, 'test_submit');
  queue.resolve();
  validation.resolve(null);
  await work;
  assert.equal(renderer.completionWaits.size, 0);
  assert.deepEqual(failures, []);
  renderer.dispose();
});

function rendererWith(queue, validation) {
  const failures = [];
  const renderer = new ClientHDRRenderer({ onFailure: (reason) => failures.push(reason) });
  let pops = 0;
  renderer.device = {
    pushErrorScope() {},
    popErrorScope() { pops++; return validation.promise; },
    queue: { onSubmittedWorkDone: () => queue.promise },
    destroy() {}
  };
  return { renderer, failures, pops: () => pops };
}

test('every frame preserves the configured surface and submits without yielding or replacing resources', async () => {
  const { renderer } = rendererWith({ promise: Promise.resolve() }, { promise: Promise.resolve(null) });
  const events = [], texture = {};
  renderer.canvas = { width: 10, height: 20 };
  renderer.stagingTexture = { destroy() { events.push('release'); } };
  renderer.context = {
    configure() { assert.fail('ordinary presentation must not clear the configured surface'); },
    getCurrentTexture() {
      events.push('texture');
      queueMicrotask(() => events.push('yield'));
      return texture;
    },
    unconfigure() { events.push('unconfigure'); }
  };
  renderer.device.createCommandEncoder = () => ({
    copyTextureToTexture(source, target) {
      assert.equal(source.texture, renderer.stagingTexture);
      assert.equal(target.texture, texture);
      events.push('copy');
    },
    finish: () => ({})
  });
  renderer.device.queue.submit = () => events.push('submit');
  const device = renderer.device, staging = renderer.stagingTexture;
  for (let index = 0; index < 3; index++) {
    events.length = 0;
    renderer.prepared = true;
    await renderer.present();
    assert.deepEqual(events, ['texture', 'copy', 'submit', 'yield']);
    assert.equal(renderer.device, device);
    assert.equal(renderer.stagingTexture, staging);
  }
  renderer.dispose();
  assert.deepEqual(events.slice(-2), ['release', 'unconfigure']);
});

test('HDR reassertion reuses the accepted configuration and copies the prepared frame before yielding', async () => {
  const { renderer, failures } = rendererWith({ promise: Promise.resolve() }, { promise: Promise.resolve(null) });
  const events = [], configurations = [], texture = {}, staging = { destroy() {} };
  const device = renderer.device;
  let acceptedConfiguration;
  renderer.environment = { GPUTextureUsage: { RENDER_ATTACHMENT: 16, COPY_DST: 2, COPY_SRC: 1 } };
  renderer.prepare = async () => {};
  renderer.writeParameters = () => {};
  const context = {
    configure(configuration) {
      configurations.push(configuration);
      if (configuration.colorSpace === 'srgb-linear') throw new Error('unsupported');
      acceptedConfiguration = configuration;
      events.push('configure');
      queueMicrotask(() => events.push('yield'));
    },
    getConfiguration: () => acceptedConfiguration,
    getCurrentTexture() { events.push('texture'); return texture; },
    unconfigure() {}
  };
  const canvas = { width: 10, height: 20, getContext: () => context };
  device.createTexture = () => staging;
  device.createCommandEncoder = () => ({
    copyTextureToTexture(source, target) {
      assert.equal(source.texture, staging);
      assert.equal(target.texture, texture);
      events.push('copy');
    },
    finish: () => ({})
  });
  device.queue.submit = () => events.push('submit');
  await renderer.initialize({ canvas, width: 10, height: 20, boost: 5 });
  assert.equal(configurations.length, 2, 'initialization falls back to accepted encoded sRGB');
  assert.equal(renderer.encodeOutput, true);
  const configuration = acceptedConfiguration;
  events.length = 0;
  renderer.prepared = true;
  await renderer.present({ reconfigure: true });
  assert.deepEqual(events, ['configure', 'texture', 'copy', 'submit', 'yield']);
  assert.equal(configurations[2], configuration);
  assert.equal(renderer.device, device);
  assert.equal(renderer.context, context);
  assert.equal(renderer.stagingTexture, staging);
  assert.equal(renderer.boost, 5);
  assert.equal(renderer.encodeOutput, true);
  assert.deepEqual(failures, []);
  renderer.dispose();
  assert.equal(renderer.canvasConfiguration, null);
});

test('HDR reassertion reports configuration validation failures through the presentation failure path', async () => {
  const { renderer, failures } = rendererWith(
    { promise: Promise.resolve() }, { promise: Promise.resolve({ message: 'invalid configuration' }) }
  );
  renderer.prepared = true;
  renderer.canvas = { width: 10, height: 20 };
  renderer.stagingTexture = {};
  renderer.canvasConfiguration = {};
  renderer.context = { configure() {}, getCurrentTexture: () => ({}), unconfigure() {} };
  renderer.device.createCommandEncoder = () => ({ copyTextureToTexture() {}, finish: () => ({}) });
  renderer.device.queue.submit = () => {};
  await assert.rejects(renderer.present({ reconfigure: true }), /invalid_configuration/);
  assert.equal(renderer.presenting, false);
  assert.equal(renderer.prepared, false);
  assert.equal(renderer.failed, true);
  assert.equal(failures.length, 1);
  assert.match(failures[0], /^present_failed:/);
  renderer.dispose();
});

test('GPU submission waits for queue completion and validation, and rejects invalid work', async () => {
  const queue = deferred(), validation = deferred();
  const { renderer, failures, pops } = rendererWith(queue, validation);
  let completed = false;
  const work = renderer.submitAndWait(() => {}, 'test_submit').then(() => { completed = true; });
  await Promise.resolve();
  assert.equal(completed, false);
  queue.resolve();
  await new Promise(setImmediate);
  assert.equal(pops(), 1);
  assert.equal(completed, false);
  validation.resolve({ message: 'invalid texture' });
  await assert.rejects(work, /invalid_texture/);
  assert.equal(completed, false);
  assert.equal(pops(), 1);
  assert.equal(failures.length, 1);
  renderer.dispose();
});

test('dispose and deadline cancel a pending submission without accepting its late completion', async () => {
  for (const terminate of ['dispose', 'deadline']) {
    const queue = deferred(), validation = deferred();
    const { renderer } = rendererWith(queue, validation);
    let deadline;
    renderer.setTimer = (callback) => { deadline = callback; return 1; };
    renderer.clearTimer = () => {};
    let accepted = false;
    const work = renderer.submitAndWait(() => {}, 'test_submit').then(() => { accepted = true; });
    const rejected = assert.rejects(work, /renderer_disposed|test_submit_timeout/);
    if (terminate === 'dispose') renderer.dispose();
    else deadline();
    await rejected;
    queue.resolve();
    validation.resolve(null);
    await new Promise(setImmediate);
    assert.equal(accepted, false);
    assert.equal(renderer.completionWaits.size, 0);
    renderer.dispose();
  }
});
