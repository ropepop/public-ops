import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import * as core from './ticket-action-v3-core.mjs';
import * as phoneCore from './phone-control-core.mjs';

const source = readFileSync(new URL('./ticket-app-source.js', import.meta.url), 'utf8');
function between(start, end) {
  const from = source.indexOf(start), to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, start);
  return source.slice(from, to);
}

function harness({ hdrReady = true } = {}) {
  const context = vm.createContext({ ...core, ...phoneCore, NativeDate: Date, hdrReady });
  vm.runInContext(`
    let now = 10000, wall = 1800000000000, arrivedAt = now;
    const Date = { now: () => wall, parse: NativeDate.parse };
    const performance = { now: () => now };
    let timerId = 0; const timers = new Map();
    function setTimeout(callback, delay) { const id = ++timerId; timers.set(id, { callback, at: now + delay }); return id; }
    function clearTimeout(id) { timers.delete(id); }
    let hasRenderedFrame = true, connected = true, clockKnown = true, configured = true, spacetimeStateFresh = true;
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
      controlClock: { serverUpperAtReceipt: wall, receivedMonotonic: now, receivedWall: wall },
      phoneControlState: { sessionId: 'pc-test', sessionGeneration: '1', contextRevision: 'pc-test:1',
        observationSequence: '1', view: 'unactivated_detail', ready: true, busy: false,
        observedAt: new NativeDate(wall).toISOString(), expiresAt: new NativeDate(wall + 3000).toISOString(),
        leftBasisPoints: 1000, topBasisPoints: 7000, rightBasisPoints: 9000, bottomBasisPoints: 8000 },
      ticketAction: { actionId: 'synthetic-proof', target: 'prove_current', status: 'succeeded',
        currentView: 'latest_unactivated', streamEpoch: 7, frameSequence: 10, expiresAt },
      ticketSliderRegion: { proofActionId: 'synthetic-proof', streamEpoch: 7, frameSequence: 10,
        expiresAt, leftBasisPoints: 1000, topBasisPoints: 7000, rightBasisPoints: 9000, bottomBasisPoints: 8000 }
    };
    function currentRenderedFreshness() { return { visualAgeMillis: 700 + now - arrivedAt }; }
    function streamHasFreshRenderedFrame() { return connected && clockKnown && configured && 700 + now - arrivedAt <= 3000; }
    function healthyOneFPSVisualContinuity() { return connected && clockKnown && configured && 700 + now - arrivedAt <= 2000; }
    const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
    const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
    let hdrSequence = hdrReady ? 10 : 9, proofChecks = 0;
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
    function renderTicketActionV3Controls() { renderTicketRegisterOverlay(currentState, busy, codeBusy, streamHasFreshRenderedFrame()); }
    let ticketActionV3LastUserMessage = '';
    function ticketActionV3Id() { return 'synthetic-registration'; }
    ${between('function currentPhoneControlTime(state = currentState) {', '  function ticketActionV3LocalRequestIsBusy() {')}
    async function registerCurrentTicket(_source, options) {
      assertStrictAdmission(options.proofSnapshot);
      admissions++;
      if (rejectAdmission) return false;
      submissions.push({ sequence: lastRenderedFrameSequence, age: 700 + now - arrivedAt });
      return true;
    }
    function assertStrictAdmission(snapshot) {
      if (!spacetimeStateFresh || busy || codeBusy || quotaBlocked ||
        !ticketRegisterSliderProofStillMatches(snapshot)) throw new Error('unsafe admission');
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
      renew() { currentState.phoneControlState.observationSequence = '2';
        currentState.phoneControlState.observedAt = new NativeDate(wall).toISOString();
        currentState.phoneControlState.expiresAt = new NativeDate(wall + 3000).toISOString();
        renderTicketActionV3Controls(); },
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
        if (kind === 'disconnect') spacetimeStateFresh = false;
        if (kind === 'clock') clockKnown = false;
        if (kind === 'config') activeFeedbackConfigGeneration++;
        if (kind === 'epoch') currentStreamEpoch++;
        if (kind === 'proof') currentState.phoneControlState.contextRevision = 'pc-test:2';
        if (kind === 'region') currentState.phoneControlState.leftBasisPoints++;
        if (kind === 'expiry') currentState.phoneControlState.expiresAt = new NativeDate(wall).toISOString();
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

test('healthy one-second pictures preserve a real overlay and gesture across the former freshness deadline', async () => {
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

test('source observation expiry stops a gesture even while pictures continue', async () => {
  for (const elapsed of [2999, 3000, 3001]) {
    const api = harness(); api.emit('pointerdown'); api.progress(); api.advance(elapsed);
    api.arrive(); api.emit('pointerup'); await settle();
    assert.equal(api.sample().submissions.length, elapsed < 3000 ? 1 : 0);
  }
});

test('delayed HDR and stale video do not delay a qualified gesture with fresh phone state', async () => {
  for (const kind of ['pointer', 'keyboard']) {
    const api = harness({ hdrReady: false });
    api.advance(2800); api.renew();
    api.emit(kind === 'pointer' ? 'pointerdown' : 'keydown'); api.progress();
    api.advance(800); api.emit(kind === 'pointer' ? 'pointerup' : 'keyup'); await settle();
    assert.equal(api.sample().submissions.length, 1);
    assert.equal(api.sample().proofChecks, 0);
    api.emit('change'); api.presentHDR(); await settle();
    assert.equal(api.sample().submissions.length, 1);
  }
});

test('changed context, geometry, connection, policy, or cancelled gesture cannot submit later', async () => {
  for (const reason of ['disconnect', 'proof', 'region', 'expiry', 'resize', 'quota', 'busy', 'codeBusy',
    'blur', 'pointercancel', 'secondPointer']) {
    const api = harness({ hdrReady: false });
    api.emit('pointerdown'); api.progress();
    if (reason === 'pointercancel') api.emit('pointercancel');
    else if (reason === 'secondPointer') api.emit('pointerdown', { pointerId: 2, isPrimary: false });
    else api.invalidate(reason);
    api.emit('pointerup'); api.arrive(); api.presentHDR(); await settle();
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
