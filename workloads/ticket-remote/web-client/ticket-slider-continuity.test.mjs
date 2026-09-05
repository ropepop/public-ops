import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import * as core from './ticket-action-v3-core.mjs';

const source = readFileSync(new URL('./ticket-app-source.js', import.meta.url), 'utf8');
function between(start, end) {
  const from = source.indexOf(start), to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, start);
  return source.slice(from, to);
}

function harness() {
  const context = vm.createContext({ ...core, NativeDate: Date });
  vm.runInContext(`
    let now = 10000, wall = 1800000000000, arrivedAt = now;
    const Date = { now: () => wall, parse: NativeDate.parse };
    const performance = { now: () => now };
    let timerId = 0; const timers = new Map();
    function setTimeout(callback, delay) { const id = ++timerId; timers.set(id, { callback, at: now + delay }); return id; }
    function clearTimeout(id) { timers.delete(id); }
    let connected = true, clockKnown = true, configured = true, spacetimeStateFresh = true;
    let currentStreamEpoch = 7, lastRenderedFrameEpoch = 7, lastRenderedFrameSequence = 10;
    let lastAcceptedFrameSequence = 10, activeFeedbackConfigGeneration = 8;
    let ticketSliderLayoutRevision = 0, ticketSliderVisualRevision = 0, serverClockSkewMs = 0;
    let busy = false, codeBusy = false, quotaBlocked = false, controlCodeSubmitInFlight = false;
    let pendingBrowserAction = null, browserActionContextRevision = 0;
    const document = { visibilityState: 'visible' };
    const WebSocket = { OPEN: 1 }, videoWs = { get readyState() { return connected ? 1 : 3; } };
    const idleDisconnected = false, streamUnsupported = false;
    function streamClockBoundIsCurrent() { return clockKnown; }
    let ticketSliderRegionExpiryTimer = null;
    const ticketCurrentProofVisualState = {}, ticketCurrentProofLastRequestAt = 0;
    const ticketCurrentProofRequestCooldownMs = 3000, ticketCurrentProofRenewBeforeMs = 1000;
    const streamLiveOkMaxAgeMs = 2000;
    const expiresAt = new NativeDate(wall + 60000).toISOString();
    const currentState = {
      ticketAction: { actionId: 'synthetic-proof', target: 'prove_current', status: 'succeeded',
        currentView: 'latest_unactivated', streamEpoch: 7, frameSequence: 10, expiresAt },
      ticketSliderRegion: { proofActionId: 'synthetic-proof', streamEpoch: 7, frameSequence: 10,
        expiresAt, leftBasisPoints: 1000, topBasisPoints: 7000, rightBasisPoints: 9000, bottomBasisPoints: 8000 }
    };
    function currentRenderedFreshness() { return { visualAgeMillis: 700 + now - arrivedAt }; }
    function streamHasFreshRenderedFrame() { return connected && clockKnown && configured && 700 + now - arrivedAt <= 1250; }
    function healthyOneFPSVisualContinuity() { return connected && clockKnown && configured && 700 + now - arrivedAt <= 2000; }
    const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
    const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
    let hdrSequence = 10, proofChecks = 0;
    const experimentalClientHDRController = {
      snapshot() { return { active: true, surfaceVisible: true, presentationState: 'visible',
        visualHoldover: false, proofFresh: true, epoch: 7, sequence: hdrSequence }; },
      ensureExactProof(epoch, sequence) { proofChecks++; return epoch === 7 && sequence === hdrSequence; }
    };
    const logs = [], submissions = [], listeners = new Map(), windowListeners = new Map();
    let rejectAdmission = false, admissions = 0;
    function element() { return { hidden: false, disabled: false, value: '0', dataset: {},
      style: { removeProperty(name) { delete this[name]; } },
      setAttribute() {}, removeAttribute() {},
      getBoundingClientRect() { return { left: 0, top: 0, width: 100, height: 100 }; },
      addEventListener(name, callback) { listeners.set(name, callback); } }; }
    const ticketLocalRegisterSlider = element(), ticketRegisterOverlay = element(), canvas = element(), stage = element();
    const window = { addEventListener(name, callback) { windowListeners.set(name, callback); } };
    const ticketLocalRegisterSliderState = { inFlight: false, session: null, ignoreChange: false };
    function clientLog(event, reason) { logs.push({ event, reason }); }
    function maybeRequestTicketCurrentProof() {}
    function activationPolicyBlocked() { return quotaBlocked; }
    function ticketActionV3LocalRequestIsBusy() { return busy; }
    function ticketActionV3Busy() { return busy; }
    function controlCodeRequestOccupiesQueue() { return codeBusy; }
    function renderTicketActionV3Controls() { reconcilePendingBrowserAction(); renderTicketRegisterOverlay(currentState, busy, codeBusy, streamHasFreshRenderedFrame()); }
    let ticketActionV3LastUserMessage = '';
    function ticketActionV3Id() { return 'synthetic-registration'; }
    ${between('function ticketActionV3RegistrationProofIsFresh(action) {', '  function ticketActionV3LocalRequestIsBusy() {')}
    async function registerCurrentTicket(_source, options) {
      assertStrictAdmission(options.proofSnapshot);
      admissions++;
      if (rejectAdmission) return false;
      submissions.push({ sequence: lastRenderedFrameSequence, age: 700 + now - arrivedAt });
      return true;
    }
    function assertStrictAdmission(snapshot) {
      if (!spacetimeStateFresh || busy || codeBusy || quotaBlocked || !streamHasFreshRenderedFrame() ||
        !ticketRegisterSliderProofStillMatches(snapshot) || !clientHDRConsequentialControlProofReady()) throw new Error('unsafe admission');
    }
    ${between('async function submitCompletedTicketRegisterSlider(proofSnapshot, browserIntentValid) {', "  ticketViewSwitchButton.addEventListener('click'")}
    function advance(ms) {
      const end = now + ms;
      while (true) {
        const next = [...timers.entries()].filter(([, timer]) => timer.at <= end).sort((a,b) => a[1].at - b[1].at)[0];
        if (!next) break;
        wall += next[1].at - now; now = next[1].at; timers.delete(next[0]); next[1].callback();
      }
      wall += end - now; now = end; renderTicketActionV3Controls();
    }
    function emit(name, overrides = {}) {
      const event = { pointerId: 1, pointerType: 'mouse', button: 0, isPrimary: true,
        clientX: name === 'pointerdown' ? 10 : 90, clientY: 10, key: 'End', preventDefault() {}, ...overrides };
      return listeners.get(name)(event);
    }
    renderTicketActionV3Controls();
    globalThis.api = {
      advance, emit,
      arrive(hdrReady = true) { arrivedAt = now; lastRenderedFrameSequence++; if (hdrReady) hdrSequence = lastRenderedFrameSequence; renderTicketActionV3Controls(); },
      presentHDR() { hdrSequence = lastRenderedFrameSequence; renderTicketActionV3Controls(); },
      sample: () => ({ hidden: ticketRegisterOverlay.hidden, disabled: ticketLocalRegisterSlider.disabled,
        value: ticketLocalRegisterSlider.value, state: ticketRegisterOverlay.dataset.registrationState,
        session: Boolean(ticketLocalRegisterSliderState.session),
        pending: Boolean(pendingBrowserAction),
        submissions: submissions.slice(), admissions, proofChecks, logs: logs.slice() }),
      rejectAdmission() { rejectAdmission = true; },
      progress(value = '100') { ticketLocalRegisterSlider.value = value; },
      invalidate(kind) {
        if (kind === 'disconnect') connected = false;
        if (kind === 'clock') clockKnown = false;
        if (kind === 'config') activeFeedbackConfigGeneration++;
        if (kind === 'epoch') currentStreamEpoch++;
        if (kind === 'proof') currentState.ticketAction.actionId = 'other-proof';
        if (kind === 'region') currentState.ticketSliderRegion.leftBasisPoints++;
        if (kind === 'expiry') currentState.ticketSliderRegion.expiresAt = new NativeDate(wall).toISOString();
        if (kind === 'resize') { ticketSliderLayoutRevision++; cancelTicketRegisterSliderSession('viewport_resize'); }
        if (kind === 'visual') ticketSliderVisualRevision++;
        if (kind === 'quota') quotaBlocked = true;
        if (kind === 'busy') busy = true;
        if (kind === 'codeBusy') codeBusy = true;
        if (kind === 'blur') windowListeners.get('blur')();
        renderTicketActionV3Controls();
      }
    };
  `, context);
  return context.api;
}

const settle = async () => { for (let i = 0; i < 6; i++) await Promise.resolve(); };

test('healthy one-second pictures preserve a real overlay and gesture across the strict freshness deadline', async () => {
  for (const interval of [950, 1000, 1100]) {
    const api = harness();
    assert.equal(api.sample().hidden, false);
    api.advance(100); api.emit('pointerdown'); api.progress('60');
    api.advance(451);
    assert.equal(api.sample().hidden, false);
    assert.equal(api.sample().session, true);
    assert.equal(api.sample().value, '60');
    api.advance(interval - 551); api.arrive();
    assert.equal(api.sample().session, true);
    api.emit('pointerup'); await settle();
    assert.equal(api.sample().submissions.length, 1);
    assert.equal(api.sample().submissions[0].age, 700);
  }
});

test('qualified release in the gap waits locally for one matching fresh HDR paint, including native change', async () => {
  for (const kind of ['pointer', 'keyboard']) {
    const api = harness();
    api.advance(600); api.emit(kind === 'pointer' ? 'pointerdown' : 'keydown'); api.progress();
    api.advance(200); api.emit(kind === 'pointer' ? 'pointerup' : 'keyup');
    api.emit('change'); api.emit(kind === 'pointer' ? 'pointerup' : 'keyup');
    if (kind === 'pointer') {
      api.emit('pointermove', { clientX: 0, clientY: 80 });
      api.emit('pointermove', { clientX: 9, clientY: 10 });
    }
    assert.equal(api.sample().state, 'waiting_fresh_frame');
    assert.equal(api.sample().submissions.length, 0);
    assert.equal(api.sample().proofChecks, 0);
    api.advance(250); api.arrive(false);
    assert.equal(api.sample().pending, true);
    assert.equal(api.sample().proofChecks, 0);
    api.presentHDR(); api.presentHDR();
    assert.equal(api.sample().submissions.length, 0, 'render must finish before the single claimed admission');
    await settle();
    assert.equal(api.sample().submissions.length, 1);
    assert.equal(api.sample().submissions[0].age, 700);
    api.emit('change'); api.presentHDR(); await settle();
    assert.equal(api.sample().submissions.length, 1);
  }
});

test('released slider keeps its frozen geometry after the old picture expires until one fresh successor', async () => {
  const api = harness();
  api.emit('pointerdown'); api.progress(); api.advance(800); api.emit('pointerup');
  api.advance(850); // Held picture is now 2350 ms old; the release is still within 1100 ms.
  assert.equal(api.sample().pending, true);
  assert.equal(api.sample().state, 'waiting_fresh_frame');
  assert.equal(api.sample().hidden, false);
  assert.equal(api.sample().value, '100');
  assert.equal(api.sample().submissions.length, 0);
  api.advance(45); api.arrive(); await settle();
  assert.equal(api.sample().submissions.length, 1);
  assert.equal(api.sample().submissions[0].age, 700);
  api.advance(2000); api.arrive(); await settle();
  assert.equal(api.sample().submissions.length, 1);
});

test('pending completion expires or cancels on changed proof, uncertainty, policy, or interruption without submitting', async () => {
  for (const reason of ['timeout', 'disconnect', 'clock', 'config', 'epoch', 'proof', 'region', 'expiry',
    'resize', 'visual', 'quota', 'busy', 'codeBusy', 'blur', 'pointercancel', 'secondPointer']) {
    const api = harness();
    api.emit('pointerdown'); api.progress(); api.advance(800); api.emit('pointerup');
    assert.equal(api.sample().pending, true, reason);
    if (reason === 'timeout') api.advance(1100);
    else if (reason === 'pointercancel') api.emit('pointercancel');
    else if (reason === 'secondPointer') api.emit('pointerdown', { pointerId: 2, isPrimary: false });
    else api.invalidate(reason);
    api.arrive(); api.presentHDR(); await settle();
    assert.equal(api.sample().session, false, reason);
    assert.equal(api.sample().submissions.length, 0, reason);
  }
});

test('tap, reverse and vertical travel cannot become pending registration', async () => {
  for (const end of [{ clientX: 10, clientY: 10 }, { clientX: 0, clientY: 10 }, { clientX: 40, clientY: 50 }]) {
    const api = harness(); api.emit('pointerdown'); api.advance(800); api.emit('pointerup', end);
    api.arrive(); await settle();
    assert.equal(api.sample().pending, false);
    assert.equal(api.sample().submissions.length, 0);
  }
  const keyboard = harness();
  keyboard.emit('keydown'); keyboard.progress('24'); keyboard.advance(800); keyboard.emit('keyup');
  keyboard.arrive(); await settle();
  assert.equal(keyboard.sample().pending, false);
  assert.equal(keyboard.sample().submissions.length, 0);
});

test('a failed admission consumes the local completion and is never replayed by later frames', async () => {
  const api = harness(); api.rejectAdmission();
  api.emit('pointerdown'); api.progress(); api.advance(800); api.emit('pointerup');
  api.arrive(); await settle();
  assert.equal(api.sample().admissions, 1);
  assert.equal(api.sample().submissions.length, 0);
  api.arrive(); api.presentHDR(); api.emit('change'); await settle();
  assert.equal(api.sample().admissions, 1);
  assert.equal(api.sample().session, false);
});
