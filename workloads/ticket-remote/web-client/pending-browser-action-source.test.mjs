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
const ownerSource = between('  function invalidateBrowserActionContext(', '  function currentTicketSliderRegion(');
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
  c.streamHasFreshRenderedFrame = () => c.fresh && c.clock && c.configValid && Number.isFinite(c.age) && c.age >= 0 && c.age <= 3000;
  c.healthyOneFPSVisualContinuity = () => c.healthy && c.clock && c.configValid &&
    Number.isFinite(c.age) && c.age >= 0 && c.age <= 2000;
  c.streamClockBoundIsCurrent = () => c.clock;
  c.currentRenderedFreshness = () => ({ visualAgeMillis: c.age });
  c.clientLog = (...args) => logs.push(args);
  c.renderTicketActionV3Controls = () => {
    c.renderActive = true; order.push('render-start'); c.renderCount++;
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
  c.currentTicketSliderPresentationRegion = c.currentTicketSliderRegion = () => c.regionPresent && c.proofValid ? { contextRevision: 'pc-test:1' } : null;
  c.ticketRegisterSliderPresentationStillMatches = c.ticketRegisterSliderProofStillMatches = () => c.sliderMatches;
  c.ticketActionV3SmartSwitchAction = () => c.switchChoice;
  c.ticketActionV3SmartSwitchForView = view => ({ target: view === 'latest_unactivated' ? 'show_recent_activated' : 'return_to_latest_unactivated' });
  c.switchChoice = { actionId: 'synthetic-switch', currentView: 'latest_unactivated', switchExpiresAt: 'future' };
  c.submitCompletedTicketRegisterSlider = (_proof, valid) => {
    assert.equal(c.renderActive, false); if (!valid()) return false;
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
  c.currentPhoneControlReady = () => c.spacetimeStateFresh && c.proofValid;
  c.cancelTicketRegisterSliderSession = () => {};
  c.currentState.phoneControlState = { contextRevision: 'pc-test:1' };
  c.canvasRegionFingerprint = () => 'synthetic-fingerprint'; c.controlCodeFingerprintRegion = () => ({});
  c.renderControlCodeRequest = () => {};
  c.closeControlCodeDialog = () => { c.codeDialogOpen = false; c.codeDialog.hidden = true; };
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

test('all action kinds submit with direct state even when no fresh video or HDR exists', async () => {
  for (const kind of ['control_code', 'register_current', 'open_latest_and_register', 'show_recent_activated']) {
    const h = harness({ fresh: false, age: 99999, hdr: false });
    if (kind === 'control_code') await h.c.submitControlCodeRequest();
    else await h.c.requestBrowserTicketAction(kind, 'browser_button', 'synthetic');
    await flush(); assert.equal(h.effects.length, 1, kind); assert.equal(h.timers.size, 0);
  }
});

test('authority and lifecycle are rechecked after connection and subscription waits, with no replay', async () => {
  for (const kind of ['code', 'register_current', 'open_latest_and_register', 'show_recent_activated']) {
    for (const stage of ['connection', 'subscription']) {
      for (const revoke of [c => { c.spacetimeStateFresh = false; },
        c => { c.policyBlocked = true; c.switchChoice = null; },
        c => c.invalidateBrowserActionContext('window_blurred')]) {
        const h = harness(); const gate = deferred(); h.c[stage + 'Gate'] = gate.promise;
        const completion = kind === 'code' ? h.c.submitControlCodeRequest() : h.c.requestBrowserTicketAction(kind, 'browser_button', 'synthetic');
        await flush(); revoke(h.c); gate.resolve(); await completion; await flush();
        assert.equal(h.effects.length, 0, kind + '/' + stage);
        h.nextFrame(); h.render(); await flush(); assert.equal(h.effects.length, 0);
      }
    }
  }
});

test('a second code or ticket request cannot pass while the first is awaiting subscription', async () => {
  for (const kind of ['code', 'ticket']) {
    const h = harness(); const gate = deferred(); h.c.subscriptionGate = gate.promise;
    const first = kind === 'code' ? h.c.submitControlCodeRequest() : h.c.requestBrowserTicketAction('register_current', 'browser_button', 'test');
    await flush();
    await h.c.submitControlCodeRequest();
    await h.c.requestBrowserTicketAction('open_latest_and_register', 'browser_button', 'duplicate');
    gate.resolve(); await first; await flush(); assert.equal(h.effects.length, 1);
  }
});

test('changed code digits or registration context are rejected at the final admission boundary', async () => {
  for (const kind of ['code', 'ticket']) {
    const h = harness(); const gate = deferred(); h.c.subscriptionGate = gate.promise;
    const first = kind === 'code' ? h.c.submitControlCodeRequest() : h.c.requestBrowserTicketAction('register_current', 'browser_button', 'test');
    await flush();
    if (kind === 'code') h.c.codeInputRevision++;
    else h.c.proofValid = false;
    gate.resolve(); await first; await flush(); assert.equal(h.effects.length, 0);
  }
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
    h.c.ticketActionV3LastUserMessage = 'Please try again.';
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
