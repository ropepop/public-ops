import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { ticketActionV3ExplicitResultForDisplay } from './ticket-action-v3-core.mjs';

const source = readFileSync(new URL('./ticket-app-source.js', import.meta.url), 'utf8');
function between(start, end) {
  const from = source.indexOf(start), to = source.indexOf(end, from);
  assert(from >= 0 && to > from, `Actual source boundary missing: ${start}`);
  return source.slice(from, to);
}
const ownerSource = between('  function clientHDRConsequentialControlProofReady()', '  function currentTicketSliderRegion(');
const codeSource = between('  async function submitControlCodeRequest()', '  async function closeCurrentControlCode(');
const ticketSource = between('  async function requestTicketActionV3(', '  function selectServerClockSample(');
const busySource = between('  function controlCodeRequestOccupiesPhone(', '  function updateControlCodeSubmitAvailability(');
const flush = async () => { for (let i = 0; i < 16; i++) await Promise.resolve(); };
function deferred() { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; }

function harness({ age = 1300, fresh = false, hdr = false } = {}) {
  const timers = new Map(), effects = [], logs = [], order = [], callbacks = [];
  let timerId = 0;
  const c = {
    now: 100, age, fresh, healthy: true, clock: true, configValid: true, epoch: 7, config: 9,
    intentValid: true, intentBusy: false, policyBlocked: false, proofValid: true, sliderMatches: true,
    regionPresent: true, queueBusy: false, localBusy: false, interactionBusy: false,
    renderActive: false, renderCount: 0, pendingBrowserAction: null, browserActionContextRevision: 0,
    ticketSliderVisualRevision: 1, lastRenderedFrameSequence: 100, streamLiveOkMaxAgeMs: 2000,
    spacetimeStateFresh: true, serverClockSkewMs: 0, ticketActionV3LastUserMessage: '',
    configured: true, idleDisconnected: false, streamUnsupported: false,
    videoWs: { readyState: 1 }, WebSocket: { OPEN: 1 }, navigator: { onLine: true },
    ticketActionV3LastUserAction: null, ticketActionV3LastUserActionId: '',
    currentState: { ticketAction: { actionId: 'synthetic-proof', target: 'prove_current', status: 'succeeded',
      currentView: 'latest_unactivated', streamEpoch: 7, frameSequence: 100 }, ticketActions: [] },
    document: { visibilityState: 'visible' }, window: { innerHeight: 800 },
    performance: { now: () => c.now }, Date, Promise,
    setTimeout: (callback, delay) => { const id = ++timerId; timers.set(id, { callback, delay, at: c.now + delay }); return id; },
    clearTimeout: id => timers.delete(id),
    codeError: { textContent: '' }, codeDigits: { value: '12' }, codeSubmit: { textContent: 'Submit' },
    codeDialog: { hidden: false }, codeDialogOpen: true, codeInputRevision: 0,
    controlCodeSubmitInFlight: false, controlCodeCleanupPendingRequestID: '', codeRequest: null, pendingControlCodeBaselineFrameFingerprint: null,
    controlCodeFastState: { status: 'ready' }, localPublicID: 'test',
    activateTicketButton: { textContent: 'Register' }, requestTicketResetAndActivateButton: { textContent: 'Open and register' },
    ticketViewSwitchButton: { textContent: 'Switch' }, ticketLocalRegisterSlider: { value: '100' },
    ticketActionV3LocalRequestState: {}, CLIENT_HDR_ENGINE: 'test-hdr',
    experimentalMediaState: { enabled: hdr, engine: 'test-hdr' },
    hdrSnapshot: { active: true, surfaceVisible: true, visualHoldover: false, proofFresh: true,
      presentationState: 'visible', epoch: 7, sequence: 100 }, exactProof: true,
    connectionGate: Promise.resolve(), subscriptionGate: Promise.resolve(),
  };
  c.experimentalClientHDRController = hdr ? { snapshot: () => c.hdrSnapshot, ensureExactProof: () => c.exactProof } : null;
  c.ticketActionV3StreamSnapshot = () => ({ fresh: c.streamHasFreshRenderedFrame(), epoch: c.epoch, configGeneration: c.config, sequence: c.lastRenderedFrameSequence });
  c.streamHasFreshRenderedFrame = () => c.fresh && c.clock && c.configValid && Number.isFinite(c.age) && c.age >= 0 && c.age <= 1250;
  c.healthyOneFPSVisualContinuity = () => c.healthy && c.clock && c.configValid &&
    Number.isFinite(c.age) && c.age >= 0 && c.age <= 2000;
  c.streamClockBoundIsCurrent = () => c.clock;
  c.currentRenderedFreshness = () => ({ visualAgeMillis: c.age });
  c.clientLog = (...args) => logs.push(args);
  c.renderTicketActionV3Controls = () => {
    c.renderActive = true; order.push('render-start'); c.renderCount++;
    c.reconcilePendingBrowserAction();
    order.push('render-end'); c.renderActive = false;
  };
  c.ticketActionV3Busy = action => ['queued', 'pending', 'running'].includes(action?.status);
  c.ticketActionV3LocalRequestIsBusy = () => c.localBusy;
  c.controlCodeRequestExpiryTime = () => 0;
  c.isOwnedControlCodeRequest = () => true;
  c.controlCodeRequestIsStillRelevant = () => true;
  c.ticketInteractionIsBusy = () => c.interactionBusy;
  c.activationPolicyBlocked = () => c.policyBlocked;
  c.memberLimitBlocked = () => c.policyBlocked;
  c.isTicketActionV3RegistrationProofPresentable = () => c.proofValid;
  c.ticketActionV3RegistrationProofIsFresh = () => c.proofValid && c.streamHasFreshRenderedFrame();
  c.ticketRegisterSliderPresentationStream = c.ticketActionV3StreamSnapshot;
  c.currentTicketSliderPresentationRegion = c.currentTicketSliderRegion = () => c.regionPresent ? {} : null;
  c.ticketRegisterSliderPresentationStillMatches = c.ticketRegisterSliderProofStillMatches = () => c.sliderMatches;
  c.ticketActionV3SmartSwitchAction = () => c.switchChoice;
  c.ticketActionV3SmartSwitchForView = view => ({ target: view === 'latest_unactivated' ? 'show_recent_activated' : 'return_to_latest_unactivated' });
  c.switchChoice = { actionId: 'synthetic-switch', currentView: 'latest_unactivated', switchExpiresAt: 'future' };
  c.submitCompletedTicketRegisterSlider = (_proof, valid) => {
    assert.equal(c.renderActive, false); if (!valid() || !c.revealAuthoritativeSDRForConsequentialControl()) return false;
    effects.push({ kind: 'slider' }); return true;
  };
  c.ticketActionV3Id = () => 'synthetic-new-action';
  c.beginTicketActionV3LocalRequest = () => { if (c.localBusy) return false; c.localBusy = true; return true; };
  c.settleTicketActionV3LocalRequest = () => { c.localBusy = false; };
  c.scheduleTicketActionV3Reconcile = () => {};
  c.ticketActionV3RequestArgs = value => value;
  c.localizePublicMessage = value => String(value);
  c.sanitizeControlDigits = value => String(value).replace(/\D/g, '');
  c.updateControlCodeSubmitAvailability = () => {};
  c.controlCodeFastRevisionForRequest = () => 'synthetic-fast';
  c.controlCodeFastStateFresh = () => true;
  c.canvasRegionFingerprint = () => 'synthetic-fingerprint'; c.controlCodeFingerprintRegion = () => ({});
  c.renderControlCodeRequest = () => {};
  c.closeControlCodeDialog = () => { c.codeDialogOpen = false; c.codeDialog.hidden = true; c.cancelPendingBrowserAction('code_dialog_closed', 'control_code'); };
  c.setStatus = () => {};
  c.runSpacetimeMutation = async callback => {
    await c.connectionGate;
    const client = {
      requestTicketActionV3: async (args, beforeSubmit) => {
        callbacks.push(beforeSubmit); await c.subscriptionGate; beforeSubmit();
        assert.equal(c.renderActive, false); effects.push({ kind: 'ticket', target: args.target, source: args.source });
      },
      requestControlCode: async (_digits, _fast, beforeSubmit) => {
        callbacks.push(beforeSubmit); await c.subscriptionGate; beforeSubmit();
        assert.equal(c.controlCodeSubmitInFlight, true); assert.equal(c.renderActive, false);
        effects.push({ kind: 'code' });
      },
    };
    return callback(client);
  };
  vm.runInNewContext(ownerSource + codeSource + ticketSource + busySource, c);
  return { c, timers, effects, logs, order, callbacks,
    render: () => c.renderTicketActionV3Controls(),
    nextFrame: () => { c.lastRenderedFrameSequence++; c.age = 700; c.fresh = true; c.hdrSnapshot.sequence = c.lastRenderedFrameSequence; },
    advance: millis => { c.now += millis; for (const [id, timer] of [...timers]) if (timer.at <= c.now) { timers.delete(id); timer.callback(); } },
    intent: (overrides = {}) => ({ kind: 'ticket', button: c.activateTicketButton,
      valid: () => c.intentValid, busy: () => c.intentBusy,
      submit: valid => { assert.equal(c.renderActive, false); assert(valid()); effects.push({ kind: 'intent' }); return true; }, ...overrides }),
  };
}

test('actual shared owner submits immediately only with fresh matching proof and no wait timer', () => {
  const h = harness({ fresh: true, age: 700, hdr: true });
  assert.equal(h.c.runBrowserActionWhenFresh(h.intent()), true);
  assert.equal(h.effects.length, 1); assert.equal(h.timers.size, 0); assert.equal(h.c.pendingBrowserAction, null);
});

test('between-frame intent owns one slot; duplicate clicks cannot replace it; dispatch is after render', async () => {
  const h = harness();
  assert.equal(h.c.runBrowserActionWhenFresh(h.intent()), false);
  const first = h.c.pendingBrowserAction;
  assert.equal(h.c.runBrowserActionWhenFresh(h.intent({ submit: () => assert.fail('duplicate submitted') })), false);
  assert.equal(h.c.pendingBrowserAction, first); assert.equal(h.timers.size, 1); assert.equal(h.effects.length, 0);
  h.nextFrame(); h.render(); h.render();
  assert.equal(h.effects.length, 0, 'no dispatch from inside the render stack');
  await flush(); assert.equal(h.effects.length, 1); assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.timers.size, 0);
  h.render(); h.nextFrame(); h.render(); h.advance(5000); await flush(); assert.equal(h.effects.length, 1);
});

test('one fixed 1100ms deadline does not expire with the held picture and never renews', async () => {
  for (const age of [0, 700, 900, 1175, 1251, 1300, 1900, 1999, 1999.9, 2000]) {
    const h = harness({ age }); h.c.runBrowserActionWhenFresh(h.intent());
    const pending = h.c.pendingBrowserAction;
    assert.equal(h.timers.size, 1); assert.equal([...h.timers.values()][0].delay, 1100);
    assert.equal(pending.deadline, h.c.now + 1100);
    h.advance(1099); h.c.age += 1099; h.render(); await flush();
    assert.equal(h.c.pendingBrowserAction, pending); assert.equal(h.effects.length, 0);
    assert.equal(h.timers.size, 1, 'observing expiry does not create another timer');
    h.advance(1); assert.equal(h.effects.length, 0); assert.equal(h.c.pendingBrowserAction, null);
    assert.equal(h.c.activateTicketButton.textContent, 'Register'); assert(h.c.ticketActionV3LastUserMessage.length > 0);
    h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0, 'timeout cannot replay');
  }
  for (const age of [NaN, Infinity, -1, 2000.1, 2100]) {
    const h = harness({ age }); h.c.runBrowserActionWhenFresh(h.intent()); assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.effects.length, 0);
  }
});

test('all action kinds survive held age over two seconds and consume one fresh successor within 1100ms', async () => {
  // A deterministic trace using the observed 1175ms base and 895ms first-fresh
  // sample interval; this does not infer the precise live cancellation time.
  for (const kind of ['control_code', 'register_current', 'open_latest_and_register',
    'show_recent_activated', 'return_to_latest_unactivated', 'slider']) {
    const h = harness({ age: 1175, hdr: true });
    if (kind === 'return_to_latest_unactivated') h.c.switchChoice.currentView = 'recent_activated';
    if (kind === 'control_code') await h.c.submitControlCodeRequest();
    else h.c.requestBrowserTicketAction(kind === 'slider' ? 'register_current' : kind,
      kind === 'slider' ? 'browser_slider' : 'browser_button', 'synthetic', kind === 'slider' ? {} : null);
    const pending = h.c.pendingBrowserAction;
    assert(pending, kind); assert.equal(pending.deadline, 1200);
    h.advance(825); h.c.age = 2000; h.render(); await flush();
    assert.equal(h.c.pendingBrowserAction, pending, `${kind}: old age-capped deadline must not cancel`);
    h.advance(70); h.c.age = 2070; h.c.proofValid = false; h.render(); await flush();
    assert.equal(h.c.healthyOneFPSVisualContinuity(), false);
    assert.equal(h.c.streamHasFreshRenderedFrame(), false);
    assert.equal(h.c.pendingBrowserAction, pending, `${kind}: held proof grants no authority and does not cancel the wait`);
    assert.equal(h.effects.length, 0); assert.equal(h.timers.size, 1);
    h.c.proofValid = true; h.nextFrame(); h.render();
    assert.equal(h.effects.length, 0, 'dispatch remains outside rendering');
    await flush(); assert.equal(h.effects.length, 1, kind); assert.equal(h.c.pendingBrowserAction, null);
    assert.equal(h.timers.size, 0); h.nextFrame(); h.render(); h.advance(5000); await flush();
    assert.equal(h.effects.length, 1, `${kind}: consumed intent cannot replay`);
  }
});

test('immediate exact fresh authority preserves admission without relay continuity; stale entry does not', async () => {
  for (const kind of ['control_code', 'register_current', 'open_latest_and_register', 'show_recent_activated']) {
    const h = harness({ age: 700, fresh: true, hdr: true }); h.c.healthy = false;
    if (kind === 'control_code') await h.c.submitControlCodeRequest();
    else await h.c.requestBrowserTicketAction(kind, 'browser_button', 'synthetic');
    await flush(); assert.equal(h.effects.length, 1, kind); assert.equal(h.timers.size, 0);
    assert.equal(h.c.pendingBrowserAction, null);
  }
  const stale = harness(); stale.c.healthy = false;
  assert.equal(stale.c.runBrowserActionWhenFresh(stale.intent()), false);
  assert.equal(stale.c.pendingBrowserAction, null); assert.equal(stale.effects.length, 0);
});

test('queued policy and relay loss wait while stale, then cancel at fresh consumption without replay', async () => {
  for (const change of [c => { c.intentValid = false; }, c => { c.healthy = false; }]) {
    const h = harness(); h.c.runBrowserActionWhenFresh(h.intent()); const pending = h.c.pendingBrowserAction;
    change(h.c); h.render(); await flush(); assert.equal(h.c.pendingBrowserAction, pending);
    assert.equal(h.effects.length, 0); h.nextFrame(); h.render(); await flush();
    assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.effects.length, 0);
    h.c.intentValid = true; h.c.healthy = true; h.nextFrame(); h.render(); await flush();
    assert.equal(h.effects.length, 0);
  }
});

test('queued actions recheck relay continuity after both database waits', async () => {
  for (const kind of ['control_code', 'register_current', 'open_latest_and_register', 'show_recent_activated']) {
    for (const stage of ['connection', 'subscription']) {
      const h = harness({ hdr: true }); const gate = deferred(); h.c[stage + 'Gate'] = gate.promise;
      if (kind === 'control_code') await h.c.submitControlCodeRequest();
      else h.c.requestBrowserTicketAction(kind, 'browser_button', 'synthetic');
      h.nextFrame(); h.render(); await flush();
      assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.effects.length, 0);
      h.c.healthy = false; gate.resolve(); await flush();
      assert.equal(h.effects.length, 0, `${kind}/${stage}`);
      assert.equal(h.c.controlCodeSubmitInFlight, false); assert.equal(h.c.localBusy, false);
      h.c.healthy = true; h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0);
    }
  }
});

test('a stale click requires a genuinely newer frame, and expired or mismatched HDR proof cannot dispatch', async () => {
  const h = harness({ hdr: true }); h.c.runBrowserActionWhenFresh(h.intent());
  h.c.age = 700; h.c.fresh = true; h.render(); await flush(); assert.equal(h.effects.length, 0);
  h.nextFrame(); h.c.hdrSnapshot.sequence--; h.render(); await flush(); assert.equal(h.effects.length, 0);
  h.c.hdrSnapshot.sequence = h.c.lastRenderedFrameSequence; h.render();
  h.c.age = 1251; await flush(); assert.equal(h.effects.length, 0, 'expiry before the microtask still blocks');
  h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 1);
});

test('fresh SDR with mismatched HDR waits for the same frame exact presentation instead of requiring another frame', async () => {
  const h = harness({ age: 700, fresh: true, hdr: true }); h.c.hdrSnapshot.sequence--;
  h.c.runBrowserActionWhenFresh(h.intent()); assert.equal(h.c.pendingBrowserAction.requiresNewFrame, false);
  h.c.hdrSnapshot.sequence = h.c.lastRenderedFrameSequence; h.render(); await flush(); assert.equal(h.effects.length, 1);
});

test('context, clock, config and lifecycle changes cancel immediately without replay', async () => {
  const changes = [
    c => { c.epoch++; }, c => { c.config++; }, c => { c.ticketSliderVisualRevision++; },
    c => { c.currentState.ticketAction.actionId += '-changed'; }, c => { c.currentState.ticketAction.currentView = 'recent_activated'; },
    c => { c.currentState.ticketAction.status = 'failed'; }, c => { c.currentState.ticketAction.frameSequence++; },
    c => { c.currentState.ticketAction.streamEpoch++; }, c => { c.intentBusy = true; },
    c => { c.clock = false; }, c => { c.configured = false; }, c => { c.idleDisconnected = true; },
    c => { c.streamUnsupported = true; }, c => { c.videoWs = null; }, c => { c.videoWs.readyState = 3; },
    c => { c.navigator.onLine = false; },
    c => { c.document.visibilityState = 'hidden'; }, c => { c.spacetimeStateFresh = false; },
    c => c.invalidateBrowserActionContext('stream_reset'), c => c.invalidateBrowserActionContext('window_blurred'),
  ];
  for (const change of changes) {
    const h = harness(); h.c.runBrowserActionWhenFresh(h.intent()); change(h.c); h.render();
    assert.equal(h.c.pendingBrowserAction, null); h.nextFrame(); h.render(); h.advance(5000); await flush(); assert.equal(h.effects.length, 0);
  }
});

test('cancellation after a render scheduled dispatch revokes its microtask and restores the button', async () => {
  for (const cancel of [c => c.invalidateBrowserActionContext('stream_config_changed'),
    c => { c.clock = false; }, c => { c.now = c.pendingBrowserAction.deadline; }]) {
    const h = harness(); h.c.runBrowserActionWhenFresh(h.intent()); h.nextFrame(); h.render(); cancel(h.c);
    await flush(); assert.equal(h.effects.length, 0); assert.equal(h.c.pendingBrowserAction, null);
    assert.equal(h.c.activateTicketButton.textContent, 'Register');
  }
});

test('actual register, open/register and fixed switch wrappers use the shared owner', async () => {
  for (const target of ['register_current', 'open_latest_and_register', 'show_recent_activated', 'return_to_latest_unactivated']) {
    const h = harness(); if (target === 'return_to_latest_unactivated') h.c.switchChoice.currentView = 'recent_activated';
    h.c.requestBrowserTicketAction(target, 'browser_button', 'synthetic'); assert(h.c.pendingBrowserAction);
    assert.equal(h.effects.length, 0); h.nextFrame(); h.render(); await flush();
    assert.deepEqual(h.effects, [{ kind: 'ticket', target, source: 'browser_button' }]);
  }
});

test('changed registration/slider proof, quota or selected switch cannot retarget a pending click', async () => {
  for (const setup of [
    { target: 'register_current', change: c => { c.proofValid = false; } },
    { target: 'register_current', change: c => { c.regionPresent = false; } },
    { target: 'register_current', slider: {}, change: c => { c.sliderMatches = false; } },
    { target: 'open_latest_and_register', change: c => { c.policyBlocked = true; } },
    { target: 'show_recent_activated', change: c => { c.switchChoice.currentView = 'recent_activated'; } },
    { target: 'show_recent_activated', change: c => { c.switchChoice = { ...c.switchChoice, actionId: 'different-switch' }; } },
    { target: 'show_recent_activated', change: c => { c.switchChoice.switchExpiresAt = 'changed'; } },
  ]) {
    const h = harness(); h.c.requestBrowserTicketAction(setup.target, 'browser_button', 'synthetic', setup.slider);
    assert(h.c.pendingBrowserAction); setup.change(h.c); h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0);
    assert.equal(h.c.pendingBrowserAction, null);
    if (setup.slider) assert.equal(h.c.ticketLocalRegisterSlider.value, '0');
  }
});

test('actual code wrapper freezes input and transfers ownership into its own in-flight gate', async () => {
  const h = harness(); await h.c.submitControlCodeRequest();
  assert.equal(h.c.pendingBrowserAction.kind, 'control_code'); await h.c.submitControlCodeRequest(); assert.equal(h.timers.size, 1);
  const gate = deferred(); h.c.subscriptionGate = gate.promise;
  h.nextFrame(); h.render(); await flush();
  assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.c.controlCodeSubmitInFlight, true); assert.equal(h.effects.length, 0);
  assert.equal(await h.c.submitControlCodeRequest(), false);
  gate.resolve(); await flush(); assert.deepEqual(h.effects, [{ kind: 'code' }]); assert.equal(h.c.controlCodeSubmitInFlight, false);
});

test('code input edit, dialog close and quota change cancel without submission or later replay', async () => {
  for (const change of [c => { c.codeDigits.value = '34'; }, c => { c.codeInputRevision++; },
    c => { c.codeDialog.hidden = true; }, c => { c.codeDialogOpen = false; }, c => { c.policyBlocked = true; }]) {
    const h = harness(); await h.c.submitControlCodeRequest(); change(h.c); h.nextFrame(); h.render(); await flush();
    assert.equal(h.effects.length, 0); assert.equal(h.c.pendingBrowserAction, null); assert(h.c.codeError.textContent.length > 0);
  }
});

test('actual code/ticket before-submit callbacks recheck both connection and subscription waits', async () => {
  for (const kind of ['code','register_current','open_latest_and_register','show_recent_activated']) {
    for (const stage of ['connection','subscription']) {
      for (const revoke of [c => { c.age = 1251; }, c => { c.hdrSnapshot.sequence--; },
        c => { c.exactProof = false; }, c => c.invalidateBrowserActionContext('window_blurred')]) {
        const h = harness({ age: 700, fresh: true, hdr: true });
        const gate = deferred(); h.c[stage + 'Gate'] = gate.promise;
        const completion = kind === 'code' ? h.c.submitControlCodeRequest() : h.c.requestBrowserTicketAction(kind, 'browser_button', 'synthetic');
        await flush(); revoke(h.c); gate.resolve(); await completion; await flush();
        assert.equal(h.effects.length, 0, `${kind}/${stage}`); assert.equal(h.c.pendingBrowserAction, null);
        assert.equal(h.c.controlCodeSubmitInFlight, false); assert.equal(h.c.localBusy, false);
        h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0, 'failed admission is never replayed');
      }
    }
  }
});

test('invalid initial authority does not reserve the slot or send an action', () => {
  for (const change of [c => { c.intentValid = false; }, c => { c.intentBusy = true; },
    c => { c.spacetimeStateFresh = false; }, c => { c.document.visibilityState = 'hidden'; },
    c => { c.clock = false; }, c => { c.configValid = false; }]) {
    const h = harness({ age: 700, fresh: true }); change(h.c);
    assert.equal(h.c.runBrowserActionWhenFresh(h.intent()), false);
    assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.timers.size, 0); assert.equal(h.effects.length, 0);
  }
});

test('the single slot is shared across code and ticket kinds, with scoped cancellation', async () => {
  const h = harness(); await h.c.submitControlCodeRequest(); const first = h.c.pendingBrowserAction;
  assert.equal(h.c.requestBrowserTicketAction('register_current', 'browser_button', 'synthetic'), false);
  assert.equal(h.c.pendingBrowserAction, first); assert.equal(h.timers.size, 1);
  assert.equal(h.c.cancelPendingBrowserAction('slider_cancelled', 'slider'), false);
  assert.equal(h.c.pendingBrowserAction, first);
  assert.equal(h.c.cancelPendingBrowserAction('code_dialog_closed', 'control_code'), true);
  h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0); assert.equal(h.timers.size, 0);
  assert.equal(h.c.codeSubmit.textContent, 'Submit');
});

test('direct cancellation releases rendered gates on one deferred turn without another picture or replay', async () => {
  const h = harness(); h.c.runBrowserActionWhenFresh(h.intent());
  const rendersBefore = h.c.renderCount, sequenceBefore = h.c.lastRenderedFrameSequence;
  assert.equal(h.c.cancelPendingBrowserAction('window_blurred'), true);
  assert.equal(h.c.pendingBrowserAction, null); assert.equal(h.timers.size, 0);
  assert.equal(h.c.renderCount, rendersBefore, 'cancellation must not synchronously re-enter rendering');
  await flush();
  assert.equal(h.c.renderCount, rendersBefore + 1);
  assert.equal(h.c.lastRenderedFrameSequence, sequenceBefore); assert.equal(h.c.fresh, false);
  assert.equal(h.effects.length, 0);
  assert.equal(h.c.cancelPendingBrowserAction('second_pointer'), false);
  await flush(); assert.equal(h.c.renderCount, rendersBefore + 1, 'already-cancelled work queues no duplicate render');
  h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0);
});

test('actual result render keeps cancellation text for a cloned unchanged row and clears semantic changes', async () => {
  const resultRenderSource = between('    const observedUserAction = ticketActionV3ExplicitResultForDisplay(',
    '    const statusAction = ticketActionV3LastUserAction || action;');
  const renderResult = c => vm.runInNewContext(`(() => { ${resultRenderSource} })()`, c);
  const original = { actionId: 'synthetic-explicit-result', status: 'succeeded', phase: 'complete',
    reason: 'synthetic', currentView: 'latest_unactivated' };
  for (const changedField of ['actionId', 'status', 'phase', 'reason', 'currentView']) {
    const h = harness(); h.c.ticketActionV3LastUserAction = original;
    h.c.ticketActionV3LastUserActionId = original.actionId;
    h.c.ticketActionV3ExplicitResultForDisplay = ticketActionV3ExplicitResultForDisplay;
    h.c.runBrowserActionWhenFresh(h.intent()); h.c.cancelPendingBrowserAction('fresh_frame_wait_expired');
    await flush(); const message = h.c.ticketActionV3LastUserMessage; assert(message.length > 0);
    const clone = { ...original }; assert.notEqual(clone, original);
    h.c.state = { ticketActions: [clone] }; renderResult(h.c);
    assert.equal(h.c.ticketActionV3LastUserAction, clone);
    assert.equal(h.c.ticketActionV3LastUserMessage, message, 'subscription clones must not erase the retry message');
    const changed = { ...clone, [changedField]: `${clone[changedField]}-changed` };
    if (changedField === 'actionId') h.c.ticketActionV3LastUserActionId = changed.actionId;
    h.c.state = { ticketActions: [changed] }; renderResult(h.c);
    assert.equal(h.c.ticketActionV3LastUserAction, changed);
    assert.equal(h.c.ticketActionV3LastUserMessage, '', changedField);
    assert.equal(h.effects.length, 0);
  }
});
