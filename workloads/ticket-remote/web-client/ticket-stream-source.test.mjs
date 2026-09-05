import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import { transformSync } from 'esbuild';
import { CLIENT_HDR_ENGINE, ClientHDRController, normalizeClientHDRDisplayBoost, offerClientHDRCanvasFrame } from './client-hdr-core.mjs';
import * as ticketActionCore from './ticket-action-v3-core.mjs';

const source = readFileSync(new URL('./ticket-app-source.js', import.meta.url), 'utf8');
const earlySource = readFileSync(new URL('../internal/web/static/index.html.tmpl', import.meta.url), 'utf8');
const spacetimeClientSource = readFileSync(new URL('./src/index.ts', import.meta.url), 'utf8');

function between(start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from);
  assert.ok(from >= 0 && to > from, `missing source range ${start}`);
  return source.slice(from, to);
}

function writeUint64(view, offset, value) {
  const numeric = BigInt(value);
  view.setUint32(offset, Number(numeric >> 32n));
  view.setUint32(offset + 4, Number(numeric & 0xffffffffn));
}

function parserHarness() {
  const logs = [];
  const context = vm.createContext({
    ArrayBuffer,
    DataView,
    Number,
    Uint8Array,
    logs,
    sendVideoClientLog(event, detail) { logs.push({ event, detail }); },
    showUnsupported() {}
  });
  vm.runInContext(`
    const FRAME_ENVELOPE_MAGIC = 0x54534632;
    const FRAME_ENVELOPE_HEADER_BYTES = 29;
    const streamMaxVideoPayloadBytes = 2 * 1024 * 1024;
    ${between('function readUint64(view, offset) {', '  function isAppleWebKit() {')}
    globalThis.frameAPI = { parseFrameEnvelope };
  `, context);
  return context;
}

test('TSF2 and TSF3 parse every shared header field safely', () => {
  const context = parserHarness();
  const tsf2 = new ArrayBuffer(30);
  const tsf2View = new DataView(tsf2);
  tsf2View.setUint32(0, 0x54534632);
  tsf2View.setUint8(4, 1);
  writeUint64(tsf2View, 5, 7);
  writeUint64(tsf2View, 13, 9);
  writeUint64(tsf2View, 21, 1_700_000_000_000_000);
  new Uint8Array(tsf2)[29] = 0x65;
  const legacy = context.frameAPI.parseFrameEnvelope(tsf2);
  assert.equal(legacy.version, 'tsf2');
  assert.equal(legacy.epoch, 7);
  assert.equal(legacy.sequence, 9);
  assert.deepEqual(Array.from(legacy.data), [0x65]);

  const fields = [7, 10, 3, 4, 1_700_000_000_000_000, 1_700_000_000_010_000,
    1_700_000_000_020_000, 1_700_000_000_030_000, 1_700_000_000_040_000, 5, 25_000];
  const tsf3 = new ArrayBuffer(95);
  const tsf3View = new DataView(tsf3);
  tsf3View.setUint32(0, 0x54534633);
  tsf3View.setUint8(4, 1);
  fields.forEach((value, index) => writeUint64(tsf3View, 5 + index * 8, value));
  new Uint8Array(tsf3).set([0x65, 0x88], 93);
  const current = context.frameAPI.parseFrameEnvelope(tsf3);
  assert.equal(current.version, 'tsf3');
  assert.equal(current.attempt, 3);
  assert.equal(current.codecGeneration, 4);
  assert.equal(current.captureStart, fields[4]);
  assert.equal(current.captureComplete, fields[5]);
  assert.equal(current.codecInput, fields[6]);
  assert.equal(current.codecOutput, fields[7]);
  assert.equal(current.emission, fields[8]);
  assert.equal(current.calibrationGeneration, 5);
  assert.equal(current.uncertainty, 25_000);
  assert.equal(current.timestamp, current.captureStart);
  assert.deepEqual(Array.from(current.data), [0x65, 0x88]);
});

test('every uint64 field fails closed above JavaScript safe integer range', () => {
  const context = parserHarness();
  for (const unsafeOffset of [5, 13, 21]) {
    const raw = new ArrayBuffer(30);
    const view = new DataView(raw);
    view.setUint32(0, 0x54534632);
    view.setUint8(4, 1);
    for (const offset of [5, 13, 21]) writeUint64(view, offset, 1);
    view.setUint32(unsafeOffset, 0x200000);
    assert.equal(context.frameAPI.parseFrameEnvelope(raw), null);
  }
  for (const unsafeOffset of [5, 13, 21, 29, 37, 45, 53, 61, 69, 77, 85]) {
    const raw = new ArrayBuffer(94);
    const view = new DataView(raw);
    view.setUint32(0, 0x54534633);
    view.setUint8(4, 1);
    for (let index = 0; index < 11; index += 1) writeUint64(view, 5 + index * 8, 1);
    view.setUint32(unsafeOffset, 0x200000);
    assert.equal(context.frameAPI.parseFrameEnvelope(raw), null);
  }
  assert.equal(context.logs.length, 14);
});

test('valid stale receipt ACKs immediately while malformed, wrong epoch, and wrong generation do not', async () => {
  const acceptFrame = between('function acceptFreshFrame(frame) {', '  function queueFrameMetadata(frame) {');
  const handleMessage = between('async function handleVideoSocketMessage(event) {', '  function decodeAvcFrame(frame) {');
  const context = vm.createContext({ ArrayBuffer, Number, Uint8Array, console });
  vm.runInContext(`
    let now = 100;
    const wallStart = 1_000_000;
    const performance = { now: () => now };
    const Date = { now: () => wallStart + now };
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 7;
    let feedbackReceivedSequence = 0;
    let currentStreamEpoch = 1;
    let lastDecoderConfig = { streamEpoch: 1, frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1, keyframeIntervalFrames: 1 };
    let lastPacketSequence = 0;
    let lastPacketSequenceAdvancedAt = 0;
    let lastPacketTimestamp = 0;
    let lastAcceptedFrameSequence = 0;
    let lastAcceptedFrameTimestamp = 0;
    let lastReceivedFrameSequence = 0;
    let lastReceivedFrameConfigGeneration = 0;
    let lastAcceptedFrameReceivedAt = 0;
    let lastAcceptedFrameVisualAgeMillis = 0;
    let lastAcceptedFrameVisualAgeKnown = false;
    let lastAcceptedFrameVisualAgeConservative = false;
    let lastAcceptedFrameEnvelopeVersion = '';
    let lastAcceptedFrameConfigGeneration = 0;
    let lastAcceptedFrameQueuedAt = 0;
    let needsKeyFrame = false;
    let configured = true;
    let decoderMode = 'annexb';
    let decoderRejectedFrames = 0;
    let staleIngressDroppedFrames = 0;
    let lastStaleIngressDropAt = 0;
    let resyncDroppedFrames = 0;
    let avcAdapterTried = true;
    let videoWs = null;
    const streamDecoderQueueHardLimit = 4;
    const streamIngressFrameMaxAgeMs = 1250;
    const recoveryKeyframeDebounceMs = 2000;
    const frames = [];
    const decoded = [];
    const resetReasons = [];
    const keyframeReasons = [];
    let metadataClears = 0;
    let feedbackCount = 0;
    const decoder = { decodeQueueSize: 0, decode() { decoded.push(lastAcceptedFrameSequence); } };
    class EncodedVideoChunk { constructor(value) { Object.assign(this, value); } }
    function parseFrameEnvelope() { return frames.shift() || null; }
    function streamClockServerUpperAt() { return Math.round((wallStart + now) * 1000); }
    function requestKeyframeDebounced() { return true; }
    function requestKeyframe(reason) { keyframeReasons.push(reason); return true; }
    function scheduleStreamFeedback() { feedbackCount += 1; }
    function publishStreamDebug() {}
    function queueFrameMetadata() {}
    function clearFrameMetadata() { metadataClears += 1; }
    function resetDecoderForRecovery(reason) { resetReasons.push(reason); return true; }
    function resetVideoReconnectBackoff() {}
    function decodeAvcFrame() { throw new Error('unexpected AVC path'); }
    function sendVideoClientLog() {}
    function switchToAvcAdapter() {}
    ${acceptFrame}
    ${handleMessage}
    globalThis.streamTest = { frames, decoder, decoded, resetReasons, keyframeReasons,
      get metadataClears() { return metadataClears; },
      get staleIngressDroppedFrames() { return staleIngressDroppedFrames; },
      get decoderRejectedFrames() { return decoderRejectedFrames; },
      get feedbackCount() { return feedbackCount; },
      get feedbackReceivedSequence() { return feedbackReceivedSequence; },
      get acceptedVisualAgeMillis() { return lastAcceptedFrameVisualAgeMillis; },
      get acceptedVisualAgeConservative() { return lastAcceptedFrameVisualAgeConservative; },
      commitFrameReceipt,
      handleVideoSocketMessage };
  `, context);
  context.streamTest.frames.push({
    version: 'tsf3', kind: 'key', epoch: 1, sequence: 1,
    captureStart: (1_000_000 + 100 - 1400) * 1000, uncertainty: 100_000,
    timestamp: (1_000_000 + 100 - 1400) * 1000, data: new Uint8Array([1])
  });
  await context.streamTest.handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  assert.equal(context.streamTest.staleIngressDroppedFrames, 1);
  assert.equal(context.streamTest.decoderRejectedFrames, 1);
  assert.equal(context.streamTest.metadataClears, 0);
  assert.deepEqual(Array.from(context.streamTest.resetReasons), []);
  assert.deepEqual(Array.from(context.streamTest.keyframeReasons), []);
  assert.equal(context.streamTest.feedbackReceivedSequence, 1);
  assert.equal(context.streamTest.feedbackCount, 1);

  context.streamTest.frames.push({
    version: 'tsf3', kind: 'key', epoch: 99, sequence: 2,
    captureStart: (1_000_000 + 100) * 1000, uncertainty: 0,
    timestamp: (1_000_000 + 100) * 1000, data: new Uint8Array([9])
  });
  await context.streamTest.handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  context.streamTest.frames.push(null);
  await context.streamTest.handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  assert.equal(context.streamTest.feedbackReceivedSequence, 1);
  assert.equal(context.streamTest.feedbackCount, 1);

  context.streamTest.frames.push({
    version: 'tsf3', kind: 'key', epoch: 1, sequence: 2,
    captureStart: (1_000_000 + 100 - 100) * 1000, uncertainty: 100_000,
    timestamp: (1_000_000 + 100 - 100) * 1000, data: new Uint8Array([2])
  });
  await context.streamTest.handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  assert.deepEqual(Array.from(context.streamTest.decoded), [2]);
  assert.equal(context.streamTest.acceptedVisualAgeMillis, 200);
  assert.equal(context.streamTest.acceptedVisualAgeConservative, true);
  assert.equal(context.streamTest.feedbackReceivedSequence, 2);

  assert.equal(context.streamTest.commitFrameReceipt({}, {
    epoch: 1, sequence: 3, configGeneration: 6
  }), false);
  assert.equal(context.streamTest.feedbackReceivedSequence, 2);

  context.streamTest.decoder.decodeQueueSize = 5;
  context.streamTest.frames.push({
    version: 'tsf3', kind: 'key', epoch: 1, sequence: 3,
    captureStart: (1_000_000 + 100) * 1000, uncertainty: 0,
    timestamp: (1_000_000 + 100) * 1000, data: new Uint8Array([3])
  });
  await context.streamTest.handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  assert.deepEqual(Array.from(context.streamTest.resetReasons), ['decoder_queue_overflow']);
  assert.equal(context.streamTest.metadataClears, 1);
  assert.equal(context.streamTest.feedbackReceivedSequence, 3);
});

test('continuity stays available while consequential action authority fails closed', () => {
  const context = vm.createContext({ Number });
  vm.runInContext(`
    let now = 1100;
    const performance = { now: () => now };
    let hasRenderedFrame = true;
    let lastRenderedFrameRenderedAt = 1000;
    let lastRenderedFrameVisualAgeMillis = 100;
    let lastRenderedFrameVisualAgeKnown = false;
    let lastRenderedFrameVisualAgeConservative = false;
    let lastRenderedFrameReceivedAt = 900;
    let lastRenderedFrameQueuedAt = 950;
    let lastRenderedFrameEpoch = 7;
    let lastRenderedFrameSequence = 10;
    let currentStreamEpoch = 7;
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 8;
    let lastRenderedFrameConfigGeneration = 8;
    let feedbackRenderedSequence = 10;
    let clockCurrent = true;
    const streamLiveFreshMaxAgeMs = 1250;
    const streamLiveOkMaxAgeMs = 2000;
    const streamDegradedMaxAgeMs = 3000;
    function streamClockBoundIsCurrent() { return clockCurrent; }
    ${between('function freshnessStateForVisualAge(ageMs) {', '  function clearStreamContinuityStaleGrace() {')}
    globalThis.freshnessAPI = {
      currentRenderedFreshness,
      setSourceAge(known, conservative, age) {
        lastRenderedFrameVisualAgeKnown = known;
        lastRenderedFrameVisualAgeConservative = conservative;
        lastRenderedFrameVisualAgeMillis = age;
      },
      setClockCurrent(value) { clockCurrent = value; },
      setGeneration(value) { lastRenderedFrameConfigGeneration = value; },
      setRenderedAck(sequence) { feedbackRenderedSequence = sequence; },
      setEpoch(value) { lastRenderedFrameEpoch = value; }
    };
  `, context);

  const legacy = context.freshnessAPI.currentRenderedFreshness(1100);
  assert.equal(legacy.visualAgeKnown, false);
  assert.equal(legacy.visualAgeMillis, -1);
  assert.equal(legacy.continuityAgeMillis, 100);
  assert.equal(legacy.continuityPresentable, true);
  assert.equal(legacy.liveLabeled, false);
  assert.equal(legacy.actionFresh, false);

  context.freshnessAPI.setSourceAge(true, true, 100);
  const fresh = context.freshnessAPI.currentRenderedFreshness(1100);
  assert.equal(fresh.continuityPresentable, true);
  assert.equal(fresh.liveLabeled, true);
  assert.equal(fresh.actionFresh, true);
  context.freshnessAPI.setSourceAge(true, true, 1400);
  const liveOK = context.freshnessAPI.currentRenderedFreshness(1100);
  assert.equal(liveOK.streamFreshnessState, 'LIVE_OK');
  assert.equal(liveOK.continuityPresentable, true);
  assert.equal(liveOK.liveLabeled, false);
  assert.equal(liveOK.actionFresh, false);
  context.freshnessAPI.setSourceAge(true, true, 2400);
  const degraded = context.freshnessAPI.currentRenderedFreshness(1100);
  assert.equal(degraded.streamFreshnessState, 'DEGRADED');
  assert.equal(degraded.continuityPresentable, true);
  assert.equal(degraded.liveLabeled, false);
  context.freshnessAPI.setSourceAge(true, true, 3100);
  assert.equal(context.freshnessAPI.currentRenderedFreshness(1100).continuityPresentable, false);
  context.freshnessAPI.setSourceAge(true, true, 100);
  context.freshnessAPI.setGeneration(9);
  assert.equal(context.freshnessAPI.currentRenderedFreshness(1100).actionFresh, false);
  context.freshnessAPI.setGeneration(8);
  context.freshnessAPI.setRenderedAck(9);
  assert.equal(context.freshnessAPI.currentRenderedFreshness(1100).actionFresh, false);
  context.freshnessAPI.setRenderedAck(10);
  context.freshnessAPI.setClockCurrent(false);
  const unbounded = context.freshnessAPI.currentRenderedFreshness(1100);
  assert.equal(unbounded.continuityPresentable, true);
  assert.equal(unbounded.liveLabeled, false);
  assert.equal(unbounded.actionFresh, false);
  context.freshnessAPI.setClockCurrent(true);
  context.freshnessAPI.setEpoch(6);
  assert.equal(context.freshnessAPI.currentRenderedFreshness(1100).actionFresh, false);
});

test('healthy one-FPS continuity permits local code intent without premature submission', async () => {
  const context = vm.createContext({ Date, Math, Number, Promise, console });
  vm.runInContext(`
    let monotonicNow = 1100;
    let wallNow = 1_800_000_000_000;
    Date.now = () => wallNow;
    const performance = { now: () => monotonicNow };
    function setTimeout() { return 1; }
    function clearTimeout() {}
    const WebSocket = { OPEN: 1 };
    let videoWs = { readyState: WebSocket.OPEN };
    const classList = { add() {}, remove() {} };
    const document = {
      visibilityState: 'visible', fullscreenElement: null, activeElement: null,
      body: { classList }
    };
    const navigator = { onLine: true };
    const streamLiveOkMaxAgeMs = 2000;
    const streamCurrentReportMaxAgeMs = 3500;
    const streamCurrentReportMaxSequenceLag = 4;
    const configured = true;
    function streamClockBoundIsCurrent() { return currentFreshness.clockBoundCurrent; }
    let idleDisconnected = false;
    let streamUnsupported = false;
    let serverClockSkewMs = 0;
    let lastDecoderConfig = {
      frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1,
      keyframeIntervalFrames: 1, streamEpoch: 7
    };
    let lastRenderedFrameEnvelopeVersion = 'tsf3';
    let lastRenderedFrameEpoch = 7;
    let lastRenderedFrameSequence = 10;
    let currentStreamEpoch = 7;
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 8;
    let lastRenderedFrameConfigGeneration = 8;
    let feedbackRenderedSequence = 10;
    let lastStreamStatusAt = monotonicNow;
    let latestStreamStatus = {
      updatedAt: new Date(wallNow - 100).toISOString(),
      phoneDesired: true, phoneConnected: true, phoneStreamState: 'streaming',
      activeVideoClients: 1, phoneClockBoundedCalibrated: true,
      continuity: true, allIntraConfigValid: true, freshnessState: 'LIVE_OK', liveOKMaxAgeMillis: 2000,
      lastFrameVisualAgeKnown: true, lastFrameVisualAgeMillis: 1250,
      frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1,
      keyframeIntervalFrames: 1, streamEpoch: 7, lastFrameSequence: 10
    };
    let currentFreshness = {
      hasFrame: true, streamFreshnessState: 'LIVE_OK', visualAgeKnown: true,
      visualAgeConservative: true, clockBoundCurrent: true, visualAgeMillis: 1400
    };
    let strictFresh = false;
    let hdrReady = false;
    let busy = false;
    let quotaBlocked = false;
    let sliderOverlap = false;
    let reducerCalls = 0;
    let codeDialogOpen = false;
    let controlCodeSubmitInFlight = false;
    let pendingBrowserAction = null, browserActionContextRevision = 0, codeInputRevision = 0;
    let pendingControlCodeBaselineFrameFingerprint = null;
    const localPublicID = 'member';
    const canvas = {};
    const codeDialog = { hidden: true };
    const codeResultArea = { hidden: true };
    const codeError = { textContent: '' };
    const codeDigits = { value: '42', focus() {} };
    const codeSubmit = { disabled: false, textContent: '', setAttribute() {}, removeAttribute() {} };
    const requestCodeButton = { disabled: false };
    const controlCodeHotspot = { disabled: false, setAttribute() {} };
    function freshStreamStatus(now) {
      if (!latestStreamStatus || now - lastStreamStatusAt > streamCurrentReportMaxAgeMs) return null;
      return latestStreamStatus;
    }
    function streamClockServerUpperAt() { return wallNow * 1000; }
    function currentRenderedFreshness() { return currentFreshness; }
    function streamHasFreshRenderedFrame() { return strictFresh; }
    function clientHDRConsequentialControlProofReady() { return hdrReady; }
    function revealAuthoritativeSDRForConsequentialControl() { return strictFresh && hdrReady; }
    function controlCodeMutationLaneBusy() { return busy; }
    const currentState = {}, ticketSliderVisualRevision = 0;
    const spacetimeStateFresh = true;
    function ticketActionV3StreamSnapshot() { return { epoch: 7, configGeneration: 8 }; }
    function renderTicketActionV3Controls() { reconcilePendingBrowserAction(); updateControlCodeSubmitAvailability(); }
    function ticketActionV3LocalRequestIsBusy() { return false; }
    let ticketActionV3LastUserMessage = '';
    ${between('function browserActionContext() {', '  function currentBrowserSwitchAction() {')}
    function memberLimitBlocked() { return quotaBlocked; }
    function ticketRegisterOverlayOccupiesHotspot() { return sliderOverlap; }
    function sanitizeControlDigits(value) { return String(value || '').replace(/\\D/g, ''); }
    function renderControlCodeFastStateDataset() {}
    function setStatus() {}
    function lockControlCodeDialogScroll() {}
    function updateViewportVars() {}
    function resizeCanvasBox() {}
    function controlCodeFastRevisionForRequest() { return ''; }
    function canvasRegionFingerprint() { return 'fingerprint'; }
    function controlCodeFingerprintRegion() { return {}; }
    function clientLog() {}
    function localizePublicMessage(value) { return value; }
    function renderControlCodeRequest() {}
    function closeControlCodeDialog() { codeDialogOpen = false; codeDialog.hidden = true; }
    function runSpacetimeMutation(callback) {
      callback({ requestControlCode(_digits, _revision, beforeSubmit) { beforeSubmit(); reducerCalls += 1; } });
      return Promise.resolve();
    }
    ${between('function healthyOneFPSVisualContinuity(freshness, now) {', '  function lastRenderedVisualAge(now) {')}
    ${between('function openControlCodeDialog() {', '  function closeControlCodeDialog() {')}
    ${between('function requestControlCodeFromHotspot(event) {', '  async function submitControlCodeRequest() {')}
    ${between('async function submitControlCodeRequest() {', '  async function closeCurrentControlCode(openNext) {')}
    ${between('function updateControlCodeSubmitAvailability() {', '  function reconnectVideoForRecovery(reason) {')}
    globalThis.holdoverAPI = {
      healthyOneFPSVisualContinuity, controlCodeDialogEntryReady, openControlCodeDialog,
      requestControlCodeFromHotspot, submitControlCodeRequest, updateControlCodeSubmitAvailability,
      resetDialog() { codeDialogOpen = false; codeDialog.hidden = true; },
      cancelPending() { cancelPendingBrowserAction('fixture_cancel'); },
      setFreshness(value) { currentFreshness = { ...currentFreshness, ...value }; },
      setStrict(value) { strictFresh = value; hdrReady = value; },
      setDigits(value) { codeDigits.value = value; },
      setStatusField(key, value) { latestStreamStatus[key] = value; },
      setReportAge(age) { latestStreamStatus.updatedAt = new Date(wallNow - age).toISOString(); },
      setLocalField(key, value) {
        if (key === 'videoOpen') videoWs.readyState = value ? WebSocket.OPEN : 3;
        if (key === 'epoch') lastRenderedFrameEpoch = value;
        if (key === 'sequence') lastRenderedFrameSequence = value;
      },
      controls() { return { requestDisabled: requestCodeButton.disabled, hotspotDisabled: controlCodeHotspot.disabled, submitDisabled: codeSubmit.disabled, dialogOpen: codeDialogOpen }; },
      reducerCalls: () => reducerCalls
    };
  `, context);

  const api = context.holdoverAPI;
  for (const visualAgeMillis of [1251, 1400, 1999]) {
    api.setFreshness({ visualAgeMillis });
    assert.equal(api.healthyOneFPSVisualContinuity(), true, `healthy jitter age ${visualAgeMillis} lost continuity`);
  }
  api.setReportAge(3000);
  assert.equal(api.healthyOneFPSVisualContinuity(), true, 'current durable report interval caused a between-frame flap');
  api.setReportAge(100);
  api.setFreshness({ visualAgeMillis: 1400 });
  api.updateControlCodeSubmitAvailability();
  assert.deepEqual({ ...api.controls() }, { requestDisabled: false, hotspotDisabled: false, submitDisabled: true, dialogOpen: false });
  assert.equal(api.openControlCodeDialog(), true);
  api.resetDialog();
  assert.equal(api.requestControlCodeFromHotspot({ preventDefault() {}, stopPropagation() {} }), true);
  assert.equal(await api.submitControlCodeRequest(), false);
  assert.equal(api.reducerCalls(), 0, 'LIVE_OK dialog entry reached the reducer');

  api.cancelPending();
  api.setStrict(true);
  api.setDigits('42');
  api.setFreshness({ streamFreshnessState: 'LIVE_FRESH', visualAgeMillis: 100 });
  api.updateControlCodeSubmitAvailability();
  assert.equal(api.controls().submitDisabled, false, `fresh exact proof did not restore submit: ${JSON.stringify(api.controls())}`);
  await api.submitControlCodeRequest();
  assert.equal(api.reducerCalls(), 1, 'fresh recovery did not produce exactly one reducer call');
});

function deferredAdmissionHarness() {
  const clientMethod = (start, end) => {
    const from = spacetimeClientSource.indexOf(start);
    const to = spacetimeClientSource.indexOf(end, from);
    assert.ok(from >= 0 && to > from, `missing client method ${start}`);
    return spacetimeClientSource.slice(from, to);
  };
  const clientClass = transformSync(`class AdmissionClient {
    cfg = { ticketId: 'synthetic-ticket', sessionId: 'synthetic-session' };
    backendId() { return 'synthetic-backend'; }
    whenLive() { reachedLiveWait(); return liveConnection; }
    reducer(name) { return (args) => calls.push(name === 'memberRequestControlCode' ? 'control_code' : args.target); }
    ${clientMethod('  requestControlCode(digits:', '  recordActivityTick()')}
    ${clientMethod('  requestTicketActionV3(args:', '  scheduleTicketActionV3(args:')}
    ${clientMethod('  private async callReducer(name:', '  private streamAction(name:')}
  }`, { loader: 'ts', target: 'es2022' }).code;
  const context = vm.createContext({ ...ticketActionCore, Date, Math, Number, Promise });
  vm.runInContext(`
    let now = 1000;
    const performance = { now: () => now };
    let connect;
    const connection = new Promise(resolve => { connect = resolve; });
    let connectLive;
    const liveConnection = new Promise(resolve => { connectLive = resolve; });
    let reachedLiveWait;
    const liveWaitStarted = new Promise(resolve => { reachedLiveWait = resolve; });
    const calls = [];
    ${clientClass}
    const spacetimeClient = new AdmissionClient();
    function connectSpacetimeState() { return connection; }
    function flushClientLogs() {}
    const configured = true, idleDisconnected = false, streamUnsupported = false;
    const WebSocket = { OPEN: 1 }, videoWs = { readyState: 1 };
    let spacetimeStateFresh = true;
    let hasRenderedFrame = true;
    let lastRenderedFrameRenderedAt = now;
    let lastRenderedFrameVisualAgeKnown = true;
    let lastRenderedFrameVisualAgeConservative = true;
    let lastRenderedFrameVisualAgeMillis = 1200;
    let lastRenderedFrameReceivedAt = now;
    let lastRenderedFrameQueuedAt = now;
    let lastRenderedFrameEpoch = 7;
    let lastRenderedFrameSequence = 10;
    let currentStreamEpoch = 7;
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 8;
    let lastRenderedFrameConfigGeneration = 8;
    let feedbackRenderedSequence = 10;
    const streamLiveFreshMaxAgeMs = 1250;
    const streamLiveOkMaxAgeMs = 2000;
    const streamDegradedMaxAgeMs = 3000;
    let clockCurrent = true;
    function streamClockBoundIsCurrent() { return clockCurrent; }
    const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
    const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
    let hdrExact = true;
    const experimentalClientHDRController = {
      snapshot() { return { active: true, surfaceVisible: true, visualHoldover: false, proofFresh: true,
        presentationState: 'visible', epoch: lastRenderedFrameEpoch, sequence: lastRenderedFrameSequence }; },
      ensureExactProof() { return hdrExact; }
    };
    let serverClockSkewMs = 0;
    let ticketSliderLayoutRevision = 0;
    let ticketSliderVisualRevision = 0;
    const expiresAt = new Date(Date.now() + 60_000).toISOString();
    const currentState = {
      ticketAction: { actionId: 'proved-ticket', target: 'prove_current', status: 'succeeded',
        currentView: 'latest_unactivated', streamEpoch: 7, frameSequence: 10, expiresAt },
      ticketSliderRegion: { proofActionId: 'proved-ticket', streamEpoch: 7, frameSequence: 10,
        expiresAt, leftBasisPoints: 1000, topBasisPoints: 2000, rightBasisPoints: 9000, bottomBasisPoints: 3000 }
    };
    const ticketActionV3LocalRequestState = { actionId: '' };
    function ticketActionV3LocalRequestIsBusy() { return ticketActionV3LocalRequestBusy(ticketActionV3LocalRequestState); }
    let ticketActionV3LastUserMessage = '';
    let ticketActionV3LastUserActionId = '';
    let ticketActionV3LastUserAction = null;
    function renderTicketActionV3Controls() {}
    function scheduleTicketActionV3Reconcile() {}
    function clientLog() {}
    function localizePublicMessage(value) { return value; }
    const codeError = { textContent: '' };
    const codeDigits = { value: '0'.repeat(2) };
    const document = { visibilityState: 'visible' };
    const codeDialog = { hidden: false }, codeSubmit = {};
    let codeDialogOpen = true;
    function memberLimitBlocked() { return false; }
    function activationPolicyBlocked() { return false; }
    function controlCodeRequestOccupiesQueue() { return false; }
    function healthyOneFPSVisualContinuity() { return streamHasFreshRenderedFrame(); }
    const window = { innerHeight: 1000 };
    const controlCodeFastState = {};
    let controlCodeSubmitInFlight = false;
    let pendingBrowserAction = null, browserActionContextRevision = 0, codeInputRevision = 0;
    let pendingControlCodeBaselineFrameFingerprint;
    const localPublicID = 'synthetic-member';
    function sanitizeControlDigits(value) { return value; }
    function controlCodeMutationLaneBusy() { return false; }
    function controlCodeFastRevisionForRequest() { return ''; }
    function controlCodeFastStateFresh() { return false; }
    function updateControlCodeSubmitAvailability() {}
    function canvasRegionFingerprint() { return 'sanitized'; }
    function controlCodeFingerprintRegion() { return {}; }
    function renderControlCodeRequest() {}
    function closeControlCodeDialog() {}
    function setStatus() {}
    const ticketLocalRegisterSlider = { value: '0', disabled: false };
    const ticketLocalRegisterSliderState = { inFlight: false };
    function cancelTicketRegisterSliderSession() {}
    function suppressTicketRegisterSliderChangeForPointerEvent() {}
    ${between('function streamHasFreshRenderedFrame() {', '  function safeResumeLabel(value, fallback) {')}
    ${between('function freshnessStateForVisualAge(ageMs) {', '  function healthyOneFPSVisualContinuity(freshness, now) {')}
    ${between('function lastRenderedVisualAge(now) {', '  function clearStreamContinuityStaleGrace() {')}
    ${between('async function runSpacetimeMutation(action, reason) {', '  function userActivityTickEligible() {')}
    ${between('async function submitControlCodeRequest() {', '  async function closeCurrentControlCode(openNext) {')}
    ${between('function ticketActionV3Id() {', '  function suppressTicketRegisterSliderChangeForPointerEvent() {')}
    ${between('async function requestTicketActionV3(target, source, reason, expectedInteractionRevision', '  function selectServerClockSample(state) {')}
    ${between('async function submitCompletedTicketRegisterSlider(proofSnapshot, browserIntentValid) {', "  ticketLocalRegisterSlider.addEventListener('pointerdown'")}
    globalThis.admission = {
      connect() { connect(); connectLive(); },
      connectPage: connect,
      waitForLive: () => liveWaitStarted,
      calls: () => Array.from(calls),
      error: () => codeError.textContent || ticketActionV3LastUserMessage,
      latched: () => controlCodeSubmitInFlight || ticketActionV3LocalRequestIsBusy() || ticketLocalRegisterSliderState.inFlight,
      age: () => currentRenderedFreshness(now).visualAgeMillis,
      expireFrame() { now += 51; },
      reachBoundary() { now += 50; },
      changeEpoch() { currentStreamEpoch += 1; },
      changeConfig() { activeFeedbackConfigGeneration += 1; },
      expireClock() { clockCurrent = false; },
      loseHDRProof() { hdrExact = false; },
      loseState() { spacetimeStateFresh = false; },
      changeTicket() { currentState.ticketAction = { ...currentState.ticketAction, actionId: 'different-ticket' }; },
      expireTicketProof() { currentState.ticketAction.expiresAt = new Date(Date.now() - 1).toISOString(); },
      changeRegion() { currentState.ticketSliderRegion.leftBasisPoints += 1; },
      resize() { ticketSliderLayoutRevision += 1; },
      changeVisual() { ticketSliderVisualRevision += 1; },
      submit(target) {
        if (target === 'control_code') return submitControlCodeRequest();
        if (target === 'register_current') return registerCurrentTicket('browser_button');
        if (target === 'pointer_slider' || target === 'keyboard_slider') {
          const kind = target === 'pointer_slider' ? 'pointer' : 'keyboard';
          const snapshot = currentTicketRegisterSliderPresentationProof(currentState);
          if (!beginTicketLocalRegisterSliderSession(ticketLocalRegisterSliderState, {
            kind, pointerId: 1, pointerStartClientX: 10, pointerStartClientY: 10,
            pointerTrackLeftClientX: 0, pointerTrackWidth: 100, snapshot
          })) throw new Error('failed to prepare synthetic gesture');
          ticketLocalRegisterSlider.value = '100';
          return finishTicketRegisterSliderSession({ pointerId: 1, clientX: 90, clientY: 10 }, kind);
        }
        return requestTicketActionV3(target, 'browser_button', 'synthetic-admission');
      }
    };
  `, context);
  return context.admission;
}

test('phone-changing submissions recheck 1.25-second and exact HDR authority after connection awaits', async () => {
  const targets = ['control_code', 'register_current', 'open_latest_and_register',
    'show_recent_activated', 'return_to_latest_unactivated', 'pointer_slider', 'keyboard_slider'];
  for (const target of targets) {
    for (const waitStage of ['page_connect', 'client_live']) {
      for (const invalidation of ['expireFrame', 'changeEpoch', 'changeConfig', 'expireClock', 'loseHDRProof']) {
        const api = deferredAdmissionHarness();
        const pending = api.submit(target);
        if (waitStage === 'client_live') {
          api.connectPage();
          await api.waitForLive();
        }
        const label = `${target}: ${invalidation} during ${waitStage}`;
        assert.deepEqual(Array.from(api.calls()), [], `${label} submitted before connection completed`);
        api[invalidation]();
        api.connect();
        await pending;
        assert.deepEqual(Array.from(api.calls()), [], `${label} was admitted`);
        assert.equal(api.latched(), false, `${label} retained its local latch`);
        assert.ok(api.error(), `${label} was silently rejected`);
      }
    }
    const api = deferredAdmissionHarness();
    const pending = api.submit(target);
    api.reachBoundary();
    assert.equal(api.age(), 1250);
    api.connect();
    await pending;
    assert.deepEqual(Array.from(api.calls()), [target.endsWith('_slider') ? 'register_current' : target],
      `${target} did not admit exactly once at the existing 1250 ms boundary`);
  }
});

test('registration rechecks the original ticket and completed slider proof after connection awaits', async () => {
  for (const target of ['register_current', 'pointer_slider', 'keyboard_slider']) {
    const invalidations = ['changeTicket', 'expireTicketProof', 'loseState'];
    if (target.endsWith('_slider')) invalidations.push('changeRegion', 'resize', 'changeVisual');
    for (const waitStage of ['page_connect', 'client_live']) {
      for (const invalidation of invalidations) {
        const api = deferredAdmissionHarness();
        const pending = api.submit(target);
        if (waitStage === 'client_live') {
          api.connectPage();
          await api.waitForLive();
        }
        const label = `${target}: ${invalidation} during ${waitStage}`;
        api[invalidation]();
        api.connect();
        await pending;
        assert.deepEqual(Array.from(api.calls()), [], `${label} was admitted`);
        assert.equal(api.latched(), false, `${label} retained its local latch`);
        assert.ok(api.error(), `${label} was silently rejected`);
      }
    }
  }
});

test('non-activating opening and automatic proof retain their existing admission policy', async () => {
  for (const target of ['open_latest_unactivated', 'redetect_latest', 'prove_current']) {
    const api = deferredAdmissionHarness();
    const pending = api.submit(target);
    api.expireFrame();
    api.connect();
    await pending;
    assert.deepEqual(Array.from(api.calls()), [target]);
  }
});

test('one-FPS status and recovery paths keep the spinner calm between real frame arrivals', () => {
  const context = vm.createContext({ Math, Number });
  vm.runInContext(`
    let monotonicNow = 20_000;
    let wallNow = 1_800_000_000_000;
    const Date = { now: () => wallNow, parse: (value) => Number(value) };
    const performance = { now: () => monotonicNow };
    let timerID = 0;
    const timers = new Map();
    function setTimeout(callback, delay) {
      const id = ++timerID;
      timers.set(id, { callback, at: monotonicNow + delay });
      return id;
    }
    function clearTimeout(id) { timers.delete(id); }
    let streamActionFreshnessExpiryTimer = null;
    let streamClockBoundAt = monotonicNow;
    const streamClockBoundMaxAgeMs = 15000;
    const WebSocket = { OPEN: 1 };
    let videoWs = { readyState: WebSocket.OPEN };
    const navigator = { onLine: true };
    const streamResumeSpinner = { hidden: true };
    const emptyMessage = { textContent: '' };
    const startStreamButton = { hidden: true };
    const emptyState = { hidden: true };
    const document = { visibilityState: 'visible', body: { dataset: {} } };
    const streamLiveFreshMaxAgeMs = 1250;
    const streamLiveOkMaxAgeMs = 2000;
    const streamDegradedMaxAgeMs = 3000;
    const streamCurrentReportMaxAgeMs = 3500;
    const streamCurrentReportMaxSequenceLag = 4;
    const streamStaleKeyframeMs = 3000;
    let hasRenderedFrame = true;
    let lastRenderedFrameRenderedAt = monotonicNow;
    let lastRenderedFrameVisualAgeMillis = 700;
    let lastRenderedFrameVisualAgeKnown = true;
    let lastRenderedFrameVisualAgeConservative = true;
    let lastRenderedFrameReceivedAt = monotonicNow;
    let lastRenderedFrameQueuedAt = monotonicNow;
    let lastRenderedFrameEpoch = 7;
    let lastRenderedFrameSequence = 10;
    let currentStreamEpoch = 7;
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 8;
    let lastRenderedFrameConfigGeneration = 8;
    let feedbackRenderedSequence = 10;
    let lastStreamStatusAt = monotonicNow;
    let serverClockSkewMs = -2714;
    let idleDisconnected = false;
    let streamUnsupported = false;
    let activeResumeFlow = null;
    let lastDecoderConfig = {
      frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1,
      keyframeIntervalFrames: 1, streamEpoch: 7
    };
    let lastRenderedFrameEnvelopeVersion = 'tsf3';
    let latestStreamStatus;
    const element = () => ({ disabled: false, dataset: {}, setAttribute() {}, removeAttribute() {} });
    const activateTicketButton = element();
    const requestTicketResetButton = element();
    const requestTicketResetAndActivateButton = element();
    const ticketViewSwitchButton = element();
    const ticketLocalRegisterSlider = element();
    const ticketRegisterOverlay = element();
    const ticketViewSwitchDetail = element();
    const ticketResetDetail = element();
    const ticketLocalRegisterSliderState = {};
    let ticketViewSwitchExpiryTimer = null;
    let ticketActionV3LastUserActionId = '';
    let ticketActionV3LastUserAction = null;
    let ticketActionV3LastUserMessage = '';
    const currentState = { ticketAction: { actionId: 'proof', status: 'succeeded', target: 'prove_current', currentView: 'latest_unactivated' } };
    const spacetimeStateFresh = true;
    function releaseTicketLocalRegisterSliderOnTerminal() { return false; }
    function ticketActionV3LocalRequestIsBusy() { return false; }
    function ticketActionV3Busy() { return false; }
    function ticketActionV3ExplicitResultForDisplay() { return null; }
    function controlCodeRequestOccupiesQueue() { return false; }
    function currentTicketSliderRegion() { return {}; }
    function currentTicketSliderPresentationRegion() { return {}; }
    function ticketRegisterSliderPresentationStream() { return {}; }
    function isTicketActionV3RegistrationProofPresentable() { return true; }
    const pendingBrowserAction = null, controlCodeSubmitInFlight = false;
    function reconcilePendingBrowserAction() {}
    function ticketActionV3RegistrationProofIsFresh() { return true; }
    function activationPolicyBlocked() { return false; }
    function renderTicketRegisterOverlay(_state, _busy, _codeBusy) {
      ticketLocalRegisterSlider.disabled = !(streamHasFreshRenderedFrame() || healthyOneFPSVisualContinuity());
    }
    function ticketActionV3SmartSwitchAction() { return { currentView: 'latest_unactivated' }; }
    function ticketActionV3SmartSwitchForView() { return { label: 'Switch', target: 'recent_activated' }; }
    function ticketActionV3ActivationTerminalMessage() { return ''; }
    function ticketActionV3IsExpectedEmptyRedetect() { return false; }
    function maybeRequestTicketCurrentProof() {}
    function currentStatus(overrides = {}) {
      const sourceAge = lastRenderedFrameVisualAgeMillis + (monotonicNow - lastRenderedFrameRenderedAt);
      const freshnessState = sourceAge <= streamLiveFreshMaxAgeMs ? 'LIVE_FRESH' : 'LIVE_OK';
      return Object.assign({
        updatedAt: String(wallNow), phoneDesired: true, phoneConnected: true,
        phoneStreamState: 'streaming', activeVideoClients: 1,
        phoneClockBoundedCalibrated: true, continuity: true, allIntraConfigValid: true,
        freshnessState, liveOKMaxAgeMillis: 2000,
        lastFrameVisualAgeKnown: true,
        lastFrameVisualAgeMillis: sourceAge,
        frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1,
        keyframeIntervalFrames: 1, streamEpoch: 7,
        lastFrameSequence: lastRenderedFrameSequence, lastFrameAgoMillis: 0,
        streamVerdict: freshnessState === 'LIVE_FRESH' ? 'live' : 'stale_recovering'
      }, overrides);
    }
    latestStreamStatus = currentStatus();
    function streamClockBoundIsCurrent(now = monotonicNow) {
      return streamClockBoundAt > 0 && now >= streamClockBoundAt && now - streamClockBoundAt <= streamClockBoundMaxAgeMs;
    }
    function streamClockServerUpperAt() { return wallNow * 1000; }
    function freshStreamStatus(now) {
      return latestStreamStatus && now - lastStreamStatusAt <= streamCurrentReportMaxAgeMs
        ? latestStreamStatus : null;
    }
    function streamStatusStale(status) {
      return Boolean(status && status.activeVideoClients > 0 && Number(status.lastFrameAgoMillis) > streamStaleKeyframeMs);
    }
    function publishStreamDebug() {}
    function preserveCurrentFrame() { return true; }
    function redrawPreservedFrame() { return true; }
    function keepFirstScreenPinned() {}
    function setStatus() {}
    function updateControlCodeSubmitAvailability() {}
    function streamPresentationContinuity(freshness) { return freshness.continuityPresentable; }
    function clientHDRSDRUnavailable() { return false; }
    function reconcileClientHDRStreamContinuity() {}
    function finishActivationResumeFlow() {}
    function streamHasFreshRenderedFrame() { return currentRenderedFreshness(monotonicNow).actionFresh; }
    function clientHDRConsequentialControlProofReady() { return true; }
    ${between('function showStreamResumeSpinner() {', '  function preserveCurrentFrame(reason) {')}
    ${between('function showStreamWaiting(message) {', '  function hideEmpty() {')}
    ${between('function freshnessStateForVisualAge(ageMs) {', '  function clearStreamContinuityStaleGrace() {')}
    ${between('function scheduleStreamActionFreshnessExpiry(freshness, now) {', '  function controlCodeFastStateExpiryMillis(state) {')}
    ${between('function renderTicketActionV3Controls(state = currentState) {', '  async function requestTicketActionV3(')}
    ${between('function handleStreamStatus(msg) {', '  function decodedFrameHDRMetadata(frameMetadata, presentationOrdinal, renderedAt) {')}
    function advance(milliseconds) {
      const destination = monotonicNow + milliseconds;
      while (true) {
        const next = [...timers.entries()].filter(([, timer]) => timer.at <= destination)
          .sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        const elapsed = next[1].at - monotonicNow;
        monotonicNow += elapsed;
        wallNow += elapsed;
        timers.delete(next[0]);
        next[1].callback();
      }
      wallNow += destination - monotonicNow;
      monotonicNow = destination;
    }
    function arriveFrameAfter(delay) {
      const previousArrival = lastRenderedFrameRenderedAt;
      advance(delay);
      lastRenderedFrameRenderedAt = monotonicNow;
      lastRenderedFrameReceivedAt = monotonicNow;
      lastRenderedFrameQueuedAt = monotonicNow;
      lastRenderedFrameVisualAgeMillis = 700;
      lastRenderedFrameSequence += 1;
      feedbackRenderedSequence = lastRenderedFrameSequence;
      latestStreamStatus = currentStatus();
      lastStreamStatusAt = monotonicNow;
      updateStreamFreshnessStatus('frame_rendered');
      return monotonicNow - previousArrival;
    }
    updateStreamFreshnessStatus('frame_rendered');
    globalThis.spinnerAPI = {
      advance, arriveFrameAfter,
      sampleEveryPath() {
        latestStreamStatus = currentStatus();
        lastStreamStatusAt = monotonicNow;
        updateStreamFreshnessStatus('stream_status');
        if (!streamResumeSpinner.hidden) return 'update';
        showStreamWaiting('waiting');
        if (!streamResumeSpinner.hidden) return 'waiting';
        showQuietStreamLoading();
        if (!streamResumeSpinner.hidden) return 'recovery';
        handleStreamStatus(currentStatus());
        return streamResumeSpinner.hidden ? '' : 'handler';
      },
      freshness: () => currentRenderedFreshness(monotonicNow),
      dialogEntryReady: () => controlCodeDialogEntryReady(),
      status: () => currentStatus(),
      spinnerHidden: () => streamResumeSpinner.hidden,
      controls: () => ({ register: activateTicketButton.disabled, openAndRegister: requestTicketResetAndActivateButton.disabled,
        switch: ticketViewSwitchButton.disabled, slider: ticketLocalRegisterSlider.disabled, open: requestTicketResetButton.disabled }),
      timers: () => timers.size,
      expireClockSoon() { streamClockBoundAt = monotonicNow - streamClockBoundMaxAgeMs + 100; updateStreamFreshnessStatus('clock_calibrated'); },
      recalibrateClock() { streamClockBoundAt = monotonicNow; updateStreamFreshnessStatus('clock_calibrated'); },
      invalidateConfiguration() { activeFeedbackConfigGeneration += 1; updateStreamFreshnessStatus('config_accepted'); },
      restoreConfiguration() { activeFeedbackConfigGeneration = lastRenderedFrameConfigGeneration; updateStreamFreshnessStatus('config_accepted'); },
      losePictureTiming() { lastRenderedFrameVisualAgeKnown = false; updateStreamFreshnessStatus('frame_rendered'); },
      clearPicture() { hasRenderedFrame = false; updateStreamFreshnessStatus('feedback_reset'); },
      handleStatus(overrides) { handleStreamStatus(currentStatus(overrides)); },
      handleLaggingRelayStatus() {
        handleStreamStatus(currentStatus({
          freshnessState: 'LIVE_OK', streamVerdict: 'stale_recovering',
          lastFrameVisualAgeMillis: 1400,
          lastFrameSequence: lastRenderedFrameSequence - 2,
          lastFrameAgoMillis: 3694
        }));
      },
      handleReportExpiredBeyondTTL() {
        handleStreamStatus(currentStatus({ updatedAt: String(wallNow - streamCurrentReportMaxAgeMs - 1) }));
      }
    };
  `, context);

  const api = context.spinnerAPI;
  for (const interval of [950, 1000, 1050, 1100]) {
    let elapsed = 0;
    for (const sampleAt of [0, 250, 550, 700, interval - 1]) {
      api.advance(sampleAt - elapsed);
      elapsed = sampleAt;
      const freshness = api.freshness();
      for (const [control, disabled] of Object.entries(api.controls())) {
        assert.equal(disabled, false,
          control + ' did not reflect picture authority before the next render or watchdog');
      }
      const status = api.status();
      assert.equal(freshness.visualAgeMillis, 700 + sampleAt);
      assert.equal(status.lastFrameVisualAgeMillis, freshness.visualAgeMillis);
      assert.equal(status.freshnessState, freshness.streamFreshnessState);
      assert.equal(status.streamVerdict, sampleAt <= 550 ? 'live' : 'stale_recovering');
      assert.equal(api.dialogEntryReady(), true, `passive entry closed at ${sampleAt} ms of ${interval} ms interval`);
      assert.equal(freshness.actionFresh, sampleAt <= 550,
        `strict action authority was wrong at ${sampleAt} ms of ${interval} ms interval`);
      assert.equal(api.sampleEveryPath(), '',
        `spinner flapped at ${sampleAt} ms of ${interval} ms frame interval`);
    }
    assert.equal(api.arriveFrameAfter(interval - elapsed), interval,
      `frame arrived outside its exact ${interval} ms cadence`);
  }
  const fresh = api.freshness();
  assert.equal(fresh.streamFreshnessState, 'LIVE_FRESH');
  assert.equal(fresh.actionFresh, true);
  assert.equal(api.timers(), 1, 'one current frame retained more than one expiry timer');
  api.advance(400);
  api.arriveFrameAfter(0);
  api.advance(151);
  assert.equal(api.controls().register, false, 'the replaced frame deadline disabled its fresh successor');
  assert.equal(api.timers(), 1);
  api.expireClockSoon();
  api.advance(100);
  assert.equal(api.controls().register, false, 'clock authority ended before its inclusive deadline');
  api.advance(1);
  assert.equal(api.controls().register, true, 'clock expiry did not revoke the enabled controls');
  assert.equal(api.timers(), 0);
  api.recalibrateClock();
  assert.equal(api.controls().register, false, 'new clock authority did not refresh a still-fresh picture');
  api.invalidateConfiguration();
  assert.equal(api.controls().register, true);
  assert.equal(api.timers(), 0, 'configuration invalidation retained a frame expiry timer');
  api.restoreConfiguration();
  assert.equal(api.controls().register, false);
  assert.equal(api.timers(), 1);
  api.handleLaggingRelayStatus();
  assert.equal(api.spinnerHidden(), true,
    'an older relay sequence overrode the newer validated local continuity frame');
  for (const [reason, overrides] of [
    ['disconnect', { phoneConnected: false }],
    ['configuration mismatch', { frameEnvelope: 'tsf2' }],
    ['stale report', { updatedAt: String(1_799_999_990_000) }],
    ['changed identity', { lastFrameSequence: 99 }]
  ]) {
    assert.equal(api.sampleEveryPath(), '', `valid fresh state did not reset before ${reason}`);
    api.handleStatus(overrides);
    assert.equal(api.spinnerHidden(), false, `fresh picture hid recovery during ${reason}`);
  }
  assert.equal(api.sampleEveryPath(), '', 'valid fresh state did not reset before report TTL check');
  api.handleReportExpiredBeyondTTL();
  assert.equal(api.spinnerHidden(), false,
    'durable business clock skew masked a relay report older than its fixed TTL');
  api.losePictureTiming();
  assert.equal(api.controls().register, true, 'unknown picture timing left registration enabled');
  assert.equal(api.timers(), 0, 'unknown picture timing retained its previous expiry timer');
  api.clearPicture();
  assert.equal(api.controls().register, true, 'reset picture retained registration authority');
  assert.equal(api.timers(), 0);
});

test('healthy one-FPS continuity rejects stale reports, disconnects, and changed frame identity', () => {
  const context = vm.createContext({ Date, Math, Number });
  vm.runInContext(`
    let wallNow = 1_800_000_000_000;
    const performance = { now: () => 1000 };
    const WebSocket = { OPEN: 1 };
    let videoWs = { readyState: WebSocket.OPEN };
    const document = { visibilityState: 'visible' };
    const navigator = { onLine: true };
    const streamLiveOkMaxAgeMs = 2000;
    const streamCurrentReportMaxAgeMs = 3500;
    const streamCurrentReportMaxSequenceLag = 4;
    let idleDisconnected = false;
    let streamUnsupported = false;
    let serverClockSkewMs = 0;
    let lastDecoderConfig = { frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1, keyframeIntervalFrames: 1, streamEpoch: 7 };
    let lastRenderedFrameEnvelopeVersion = 'tsf3';
    let lastRenderedFrameEpoch = 7;
    let lastRenderedFrameSequence = 10;
    let currentStreamEpoch = 7;
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 8;
    let lastRenderedFrameConfigGeneration = 8;
    let feedbackRenderedSequence = 10;
    let status = {
      updatedAt: new Date(wallNow - 100).toISOString(), phoneDesired: true, phoneConnected: true,
      phoneStreamState: 'streaming', activeVideoClients: 1, phoneClockBoundedCalibrated: true,
      continuity: true, allIntraConfigValid: true, freshnessState: 'LIVE_OK', liveOKMaxAgeMillis: 2000,
      lastFrameVisualAgeKnown: true, lastFrameVisualAgeMillis: 1250,
      frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1,
      keyframeIntervalFrames: 1, streamEpoch: 7, lastFrameSequence: 10
    };
    const freshness = { hasFrame: true, streamFreshnessState: 'LIVE_OK', visualAgeKnown: true, visualAgeConservative: true, clockBoundCurrent: true, visualAgeMillis: 1400 };
    Date.now = () => wallNow;
    function freshStreamStatus() { return status; }
    function currentRenderedFreshness() { return freshness; }
    function streamClockServerUpperAt() { return wallNow * 1000; }
    ${between('function healthyOneFPSVisualContinuity(freshness, now) {', '  function lastRenderedVisualAge(now) {')}
    globalThis.negativeAPI = {
      check: () => healthyOneFPSVisualContinuity(),
      set(key, value) { status[key] = value; },
      setOnline(value) { navigator.onLine = value; },
      reset() {
        navigator.onLine = true;
        status.updatedAt = new Date(wallNow - 100).toISOString(); status.phoneConnected = true;
        status.streamEpoch = 7; status.lastFrameSequence = 10; status.frameEnvelope = 'tsf3';
        status.continuity = true; status.allIntraConfigValid = true; status.freshnessState = 'LIVE_OK';
        status.liveOKMaxAgeMillis = 2000;
      }
    };
  `, context);
  const api = context.negativeAPI;
  assert.equal(api.check(), true);
  api.setOnline(false);
  assert.equal(api.check(), false, 'offline browser retained healthy continuity');
  for (const [field, invalid] of [
    ['updatedAt', new Date(1_799_999_990_000).toISOString()],
    ['phoneConnected', false],
    ['continuity', false],
    ['allIntraConfigValid', false],
    ['freshnessState', 'DEGRADED'],
    ['liveOKMaxAgeMillis', 2500],
    ['streamEpoch', 8],
    ['lastFrameSequence', 20],
    ['frameEnvelope', 'tsf2']
  ]) {
    api.reset();
    api.set(field, invalid);
    assert.equal(api.check(), false, `${field} mismatch retained healthy continuity`);
  }
});

test('feedback v2 contract and clock interval execute with exact wire semantics', () => {
  const sent = [];
  const timers = [];
  const freshnessUpdates = [];
  const context = vm.createContext({ Date: { now: () => 1_000_000 }, JSON, Math, Number, sent, timers, freshnessUpdates });
  vm.runInContext(`
    const WebSocket = { OPEN: 1 };
    const document = { visibilityState: 'visible' };
    const performance = { now: () => 100 };
    function setTimeout(callback) { timers.push(callback); return timers.length; }
    function clearTimeout() {}
    const videoWs = { readyState: WebSocket.OPEN, send(value) { sent.push(JSON.parse(value)); } };
    let decoder = { decodeQueueSize: 2 };
    let streamClockProbeCounter = 0;
    let pendingStreamClockProbe = null;
    let streamClockProbeTimer = null;
    let streamClockBoundConfigGeneration = 0;
    let streamClockBoundAt = 0;
    let streamClockServerUpperUnixMicros = 0;
    let streamClockOffsetMidpointMicros = 0;
    let streamClockOffsetHalfWidthMicros = 0;
    let activeFeedbackVersion = 0;
    let activeFeedbackConfigGeneration = 0;
    let claimedEarlyFeedbackState = null;
    let feedbackReceivedSequence = 0;
    let feedbackDecodedSequence = 0;
    let feedbackRenderedSequence = 0;
    let feedbackPresentedSequence = 0;
    let feedbackRenderedKeyframeSequence = 0;
    let lastReceivedFrameSequence = 0;
    let lastReceivedFrameConfigGeneration = 0;
    let lastAcceptedFrameConfigGeneration = 0;
    let lastDecodedFrameConfigGeneration = 0;
    let lastRenderedFrameConfigGeneration = 0;
    let lastPresentedFrameConfigGeneration = 0;
    let controlCodeResultPriorityPhase = '';
    let controlCodeResultPriorityConfigGeneration = 0;
    let controlCodeResultPriorityEpoch = 0;
    let controlCodeResultPriorityMinSequence = 0;
    let controlCodeResultPriorityDeadlineAt = 0;
    const controlCodeResultPriorityArmWindowMs = 5 * 60 * 1000;
    const controlCodeResultPriorityMarkWindowMs = 5000;
    let lastFeedbackSentAt = 0;
    let feedbackSentCount = 0;
    let feedbackSendFailureCount = 0;
    let feedbackImmediateKey = '';
    let currentStreamEpoch = 7;
    const streamFeedbackIntervalMs = 500;
    const streamFeedbackHiddenIntervalMs = 2000;
    const streamClockProbeIntervalMs = 5000;
    const streamClockProbeRetryMs = 3000;
    const streamClockBoundMaxAgeMs = 15000;
    function clampFeedbackNumber(value, max) {
      const numeric = Number(value);
      if (!Number.isFinite(numeric) || numeric < 0) return 0;
      return Math.min(max, Math.round(numeric));
    }
    function currentRenderedFreshness() { return { visualAgeMillis: 123, visualAgeKnown: true }; }
    function updateStreamFreshnessStatus(reason) { freshnessUpdates.push(reason); }
    function publishStreamDebug() {}
    ${between('function clearStreamClockBound() {', '  function reportDecoderError(error, mode) {')}
    globalThis.feedbackAPI = {
      activateStreamFeedbackContract,
      handleStreamClockProbeResult,
      streamClockServerUpperAt,
      scheduleStreamFeedback,
      setWatermarks(received, decoded, rendered, presented) {
        feedbackReceivedSequence = received;
        feedbackDecodedSequence = decoded;
        feedbackRenderedSequence = rendered;
        feedbackPresentedSequence = presented;
        feedbackRenderedKeyframeSequence = rendered;
        lastRenderedFrameConfigGeneration = activeFeedbackConfigGeneration;
      },
      setClaimedEarly(value) { claimedEarlyFeedbackState = value; },
      state() { return { activeFeedbackConfigGeneration, feedbackReceivedSequence, feedbackDecodedSequence,
        feedbackRenderedSequence, feedbackPresentedSequence, streamClockBoundConfigGeneration }; }
    };
  `, context);

  assert.equal(context.feedbackAPI.activateStreamFeedbackContract({ feedbackVersion: 2, feedbackConfigGeneration: 8 }), true);
  assert.equal(freshnessUpdates.at(-1), 'feedback_reset', 'configuration reset did not refresh action authority');
  assert.deepEqual(JSON.parse(JSON.stringify(sent[0])), {
    type: 'stream_feedback', version: 2, epoch: 7, receivedSequence: 0, decodedSequence: 0,
    renderedSequence: 0, renderedKeyframeSequence: 0, decoderQueueSize: 2,
    renderedVisualAgeMillis: 0, visibility: 'visible', configGeneration: 8,
    presentedSequence: 0, ageKnown: false
  });
  assert.equal(sent[1].type, 'clock_probe');
  assert.equal(sent[1].configGeneration, 8);
  assert.match(sent[1].probeId, /^[A-Za-z0-9._:-]{1,64}$/);

  context.feedbackAPI.setWatermarks(12, 11, 10, 9);
  context.feedbackAPI.scheduleStreamFeedback('received');
  assert.deepEqual(JSON.parse(JSON.stringify(sent.at(-1))), {
    type: 'stream_feedback', version: 2, epoch: 7, receivedSequence: 12, decodedSequence: 11,
    renderedSequence: 10, renderedKeyframeSequence: 10, decoderQueueSize: 2,
    renderedVisualAgeMillis: 123, visibility: 'visible', configGeneration: 8,
    presentedSequence: 9, ageKnown: true
  });

  const probe = sent.find((message) => message.type === 'clock_probe');
  const clientReceive = probe.clientSendUnixMicros + 2000;
  assert.equal(context.feedbackAPI.handleStreamClockProbeResult({
    type: 'clock_probe_result', version: 1, probeId: probe.probeId, configGeneration: 8,
    clientSendUnixMicros: probe.clientSendUnixMicros,
    serverReceiveUnixMicros: probe.clientSendUnixMicros + 600,
    serverSendUnixMicros: probe.clientSendUnixMicros + 800
  }, clientReceive, 100), true);
  assert.equal(context.feedbackAPI.streamClockServerUpperAt(110), clientReceive + 600 + 10_000);
  assert.equal(freshnessUpdates.at(-1), 'clock_calibrated', 'new clock bound did not refresh action authority');

  assert.equal(context.feedbackAPI.activateStreamFeedbackContract({ feedbackVersion: 2, feedbackConfigGeneration: 9 }), true);
  assert.deepEqual(JSON.parse(JSON.stringify(context.feedbackAPI.state())), {
    activeFeedbackConfigGeneration: 9, feedbackReceivedSequence: 0, feedbackDecodedSequence: 0,
    feedbackRenderedSequence: 0, feedbackPresentedSequence: 0, streamClockBoundConfigGeneration: 0
  });
  assert.equal(sent.at(-2).type, 'stream_feedback');
  assert.equal(sent.at(-2).configGeneration, 9);

  context.feedbackAPI.setClaimedEarly({ version: 2, configGeneration: 10, receivedSequence: 5, epoch: 7 });
  assert.equal(context.feedbackAPI.activateStreamFeedbackContract({ feedbackVersion: 2, feedbackConfigGeneration: 10 }), true);
  assert.equal(sent.at(-2).receivedSequence, 5);
  assert.equal(sent.at(-2).configGeneration, 10);
});

test('presented sequence is result-only and remains bound to exact config and epoch', () => {
  const context = vm.createContext({ Number });
  vm.runInContext(`
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 5;
    let currentStreamEpoch = 7;
    let lastPresentedFrameConfigGeneration = 0;
    let feedbackRenderedSequence = 12;
    let feedbackPresentedSequence = 0;
    let priorityClearCount = 0;
    const feedbackReasons = [];
    function scheduleStreamFeedback(reason) { feedbackReasons.push(reason); }
    function clearControlCodeResultPriority() { priorityClearCount += 1; }
    function publishStreamDebug() {}
    ${between('function commitControlCodeFeedbackPresentation(proof) {', '  function renderDecodedFrame(frame, source) {')}
    globalThis.presentationAPI = {
      commitControlCodeFeedbackPresentation,
      state() { return { feedbackPresentedSequence, lastPresentedFrameConfigGeneration,
        feedbackReasons: [...feedbackReasons], priorityClearCount }; },
      changeGeneration(value) { activeFeedbackConfigGeneration = value; },
      changeVersion(value) { activeFeedbackVersion = value; }
    };
  `, context);

  // Ordinary canvas render does not claim the stronger presentation stage.
  assert.equal(context.presentationAPI.state().feedbackPresentedSequence, 0);
  assert.equal(context.presentationAPI.commitControlCodeFeedbackPresentation({
    candidateFrameEpoch: 7, candidateFrameSequence: 12, candidateFrameConfigGeneration: 5
  }), true);
  assert.deepEqual(JSON.parse(JSON.stringify(context.presentationAPI.state())), {
    feedbackPresentedSequence: 12,
    lastPresentedFrameConfigGeneration: 5,
    feedbackReasons: ['control_code_result_presented'],
    priorityClearCount: 1
  });

  context.presentationAPI.changeGeneration(6);
  assert.equal(context.presentationAPI.commitControlCodeFeedbackPresentation({
    candidateFrameEpoch: 7, candidateFrameSequence: 13, candidateFrameConfigGeneration: 5
  }), false);
  assert.equal(context.presentationAPI.state().lastPresentedFrameConfigGeneration, 5);
  context.presentationAPI.changeVersion(1);
  assert.equal(context.presentationAPI.commitControlCodeFeedbackPresentation({}), true);
  assert.equal(context.presentationAPI.state().feedbackPresentedSequence, 12);
  assert.match(source, /if \(!commitControlCodeFeedbackPresentation\(proof\)\) return false;\s*painted = true;/);
});

function restoredControlCodeHarness() {
  const context = vm.createContext({ Date, Number, String, Boolean, Math, Set });
  vm.runInContext(`
    const performance = { now: () => 100 };
    let codeRequest = null;
    const localPublicID = 'member';
    const localSessionID = 'session';
    const ownedControlCodeRequestIDs = new Set();
    const locallyClosedControlCodeRequestIDs = new Set();
    let controlCodeSubmitInFlight = false;
    let pendingBrowserAction = null, browserActionContextRevision = 0, codeInputRevision = 0;
    let controlCodeCleanupPendingRequestID = '';
    let lastRenderedControlCodeRequestSignature = '';
    let controlCodeResultCaptureRequestID = '';
    let controlCodeResultCapturedRequestID = '';
    let controlCodeCaptureAckInFlightRequestID = '';
    let controlCodeResultCaptureTimer = null;
    let controlCodeResultCaptureStartedAt = 0;
    let lastControlCodeMarkerReceivedLogKey = '';
    let lastControlCodeMarkerWaitingLogKey = '';
    let lastControlCodeCandidateRejectedLogKey = '';
    let hasRenderedFrame = true;
    let markerReady = false;
    let resultVisible = false;
    const controlCodeCapturePollMs = 100;
    const serverClockSkewMs = 0;
    const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
    const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
    const experimentalClientHDRController = { canCoordinateSDRFrame() { return true; } };
    const experimentalMediaPresentationRegionBlocked = false;
    const experimentalMediaPresentationRecoveryPending = false;
    let timerCount = 0;
    const timers = new Set();
    const calls = { baseline: 0, keepalive: 0, keyframe: 0, retry: 0, marker: 0, candidate: 0, capture: 0, close: 0 };
    const element = () => ({ hidden: true, textContent: '', dataset: {}, style: {}, removeAttribute() {} });
    const codeResultArea = element();
    const codeResultImage = element();
    const codeResultStatus = element();
    const codeResultValue = element();
    function setTimeout() { timerCount += 1; timers.add(timerCount); return timerCount; }
    function clearTimeout(id) { timers.delete(id); }
    function safeString(value) { return String(value ?? ''); }
    function controlCodeStatusRank(status) { return status === 'succeeded' ? 4 : 1; }
    function controlCodeStatusText(status) { return status; }
    function clientLog() {}
    function reconcileControlCodeResultPriority() {}
    function clearControlCodeResultPriority() {}
    function updateControlCodeSubmitAvailability() {}
    function rememberControlCodeBaselineFrame() { calls.baseline += 1; }
    function keepControlCodeVideoAlive() { calls.keepalive += 1; }
    function controlCodeResultDisplayedForRequest() { return resultVisible; }
    function setControlCodeResultVisible(visible) { resultVisible = visible; codeResultArea.hidden = !visible; }
    function clearUnpaintedControlCodeResultImage() {}
    function scheduleControlCodeExpiry() {}
    function controlCodeRequestExpiryTime() { return Date.now() + 60000; }
    function clearControlCodeResultCapture() { controlCodeResultCaptureRequestID = ''; controlCodeResultCapturedRequestID = ''; }
    function firstPositiveSafeInteger(...values) { return values.find((value) => Number.isSafeInteger(value) && value > 0) || 0; }
    function controlCodeCaptureTrace() { calls.marker += 1; }
    function controlCodeMarkerReady() { return markerReady; }
    function noteControlCodeMarkerWaiting() { calls.marker += 1; }
    function controlCodeCandidateFrameProof() { calls.candidate += 1; return { accepted: true }; }
    function noteControlCodeCandidateRejected() {}
    function captureControlCodeResultScreenshot() { calls.capture += 1; return true; }
    function requestControlCodeLowLatencyFrame() { calls.keyframe += 1; }
    function maybeRequestControlCodeResultWaitKeyframe() { calls.retry += 1; }
    function openControlCodeDialog() {}
    function controlCodeHDRFreezeTargetActive() { return false; }
    function experimentalHDRSurfacePresentationAllowed() { return true; }
    async function runSpacetimeMutation(callback) { return callback({ closeControlCode() { calls.close += 1; return Promise.resolve(); } }); }
    ${between('function maybeCaptureControlCodeResultImage() {', '  function maybeRequestControlCodeResultWaitKeyframe(requestID, reason) {')}
    ${between('function controlCodeCapturePriorityActive() {', '  function controlCodeHDRFreezeTargetActive() {')}
    ${between('function clientHDRCanCoordinateDecodedFrame() {', '  function coordinatedDecodedFrameCanCommit(candidate) {')}
    ${between('function controlCodeRequestOccupiesPhone(request) {', '  function controlCodeRequestOccupiesQueue(ignoreSubmitInFlight = false) {')}
    ${between('function waitForControlCodeResultScreenshot(request) {', '  function setDetailsPanelVisible(visible) {')}
    ${between('async function closeCurrentControlCode(openNext) {', '  function relayReportToStreamStatus(report) {')}
    globalThis.restoredCodeAPI = {
      render(overrides = {}) {
        renderControlCodeRequest({ requestId: 'synthetic-request', ownerPublicId: 'member', sessionId: 'session',
          status: 'succeeded', captureRequired: true, captureAcknowledged: true, cleanupPending: false,
          resultFrameEpoch: 7, resultMinFrameSequence: 10, ...overrides });
      },
      frame() { markerReady = true; return maybeCaptureControlCodeResultImage(); },
      showResult() { setControlCodeResultVisible(true); },
      close: () => closeCurrentControlCode(false),
      state: () => ({ calls: { ...calls }, resultVisible, waiting: Boolean(controlCodeResultCaptureRequestID), timerCount,
        pendingTimers: timers.size, capturePriority: controlCodeCapturePriorityActive(), phoneBusy: controlCodeRequestOccupiesPhone(codeRequest),
        hdrCanCoordinate: clientHDRCanCoordinateDecodedFrame() })
    };
  `, context);
  return context.restoredCodeAPI;
}

test('reload never recaptures a completed acknowledged result, while active capture and the existing local result remain intact', async () => {
  for (const cleanupPending of [false, true]) {
    const restored = restoredControlCodeHarness();
    restored.render({ cleanupPending });
    assert.equal(restored.frame(), false, 'a previously acknowledged marker authorized a new browser capture');
    const state = restored.state();
    for (const name of ['baseline', 'keepalive', 'keyframe', 'retry', 'marker', 'candidate', 'capture']) {
      assert.equal(state.calls[name], 0, 'restored acknowledged result restarted ' + name);
    }
    assert.equal(state.waiting, false);
    assert.equal(state.timerCount, 0);
    assert.equal(state.resultVisible, false);
    assert.equal(state.capturePriority, false, 'a completed capture retained local media priority');
    assert.equal(state.hdrCanCoordinate, true, 'a completed capture blocked normal HDR frame coordination');
    assert.equal(state.phoneBusy, cleanupPending, 'suppressing old capture changed phone cleanup authority');
  }

  const active = restoredControlCodeHarness();
  active.render({ captureAcknowledged: false });
  assert.equal(active.state().calls.baseline, 1);
  assert.equal(active.state().waiting, true);
  assert.equal(active.state().capturePriority, true, 'an active unacknowledged capture lost its local media priority');
  assert.equal(active.state().hdrCanCoordinate, false, 'normal HDR coordination bypassed an active capture');
  assert.equal(active.state().calls.keyframe, 1);
  assert.equal(active.frame(), true, 'an unacknowledged result no longer permits its matching fresh capture');
  assert.equal(active.state().calls.capture, 1);
  active.showResult();
  const beforeAcknowledgement = active.state();
  active.render();
  assert.equal(active.state().resultVisible, true, 'the capture acknowledgement hid the requester-local result');
  assert.equal(active.state().calls.keyframe, beforeAcknowledgement.calls.keyframe);
  assert.equal(active.state().pendingTimers, 0, 'acknowledgement left a capture retry scheduled');
  assert.equal(active.state().capturePriority, false, 'acknowledgement kept the obsolete capture priority');
  assert.equal(active.state().hdrCanCoordinate, true, 'acknowledgement did not restore normal HDR coordination');
  assert.equal(active.frame(), false, 'a completed capture was repeated while its acknowledgement settled');
  await active.close();
  active.render();
  assert.equal(active.state().resultVisible, false, 'a late acknowledged row revived a locally closed result');
  assert.equal(active.state().calls.close, 1);
});

test('requester-local result priority exposes no request data and follows the exact result marker', () => {
  const sent = [];
  const context = vm.createContext({ Date, JSON, Number, String, sent });
  vm.runInContext(`
    const WebSocket = { OPEN: 1 };
    let now = 100;
    const performance = { now: () => now };
    const videoWs = { readyState: WebSocket.OPEN, send(value) { sent.push(JSON.parse(value)); } };
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 5;
    let currentStreamEpoch = 7;
    let controlCodeResultPriorityPhase = '';
    let controlCodeResultPriorityConfigGeneration = 0;
    let controlCodeResultPriorityEpoch = 0;
    let controlCodeResultPriorityMinSequence = 0;
    let controlCodeResultPriorityDeadlineAt = 0;
    const controlCodeResultPriorityArmWindowMs = 5 * 60 * 1000;
    const controlCodeResultPriorityMarkWindowMs = 5000;
    let codeRequest = null;
    let controlCodePreparedCaptureDisplayedRequestID = '';
    let controlCodeResultCapturedRequestID = '';
    const localPublicID = 'owner-public';
    const localSessionID = 'owner-session';
    const serverClockSkewMs = 0;
    const ownedControlCodeRequestIDs = new Set();
    const locallyClosedControlCodeRequestIDs = new Set();
    ${between('function resetControlCodeResultPriority() {', '  function reportDecoderError(error, mode) {')}
    ${between('function isOwnedControlCodeRequest(request) {', '  function normalizedControlCodeRequestSignature(request) {')}
    ${between('function controlCodeRequestExpiryTime(request) {', '  function controlCodeVisualRecoveryRequired() {')}
    globalThis.priorityAPI = {
      reconcile: reconcileControlCodeResultPriority,
      clear: clearControlCodeResultPriority,
      reset: resetControlCodeResultPriority,
      firstPositive: firstPositiveSafeInteger,
      setDisplayed(requestID) { controlCodePreparedCaptureDisplayedRequestID = requestID; },
      setCaptured(requestID) { controlCodeResultCapturedRequestID = requestID; },
      advance(milliseconds) { now += milliseconds; },
      sentCount: () => sent.length,
      state: () => ({ phase: controlCodeResultPriorityPhase, deadlineAt: controlCodeResultPriorityDeadlineAt })
    };
  `, context);

  assert.equal(context.priorityAPI.firstPositive('0', 0, '42', '43'), 42);
  assert.equal(context.priorityAPI.firstPositive('0', 0, '0'), 0);

  assert.equal(context.priorityAPI.reconcile({
    requestId: 'real-unowned', ownerPublicId: 'someone-else', sessionId: 'another-session',
    status: 'running', streamEpoch: 7
  }), false);
  assert.equal(context.priorityAPI.reconcile({
    requestId: 'pending:1', ownerPublicId: 'owner-public', status: 'queued', streamEpoch: 7
  }), false);
  assert.equal(sent.length, 0);

  const queued = {
    requestId: 'real-owned', ownerPublicId: 'owner-public', sessionId: 'owner-session',
    status: 'queued', resultFrameEpoch: '0', streamEpoch: '0'
  };
  assert.equal(context.priorityAPI.reconcile(queued), true);
  assert.deepEqual(JSON.parse(JSON.stringify(sent[0])), {
    type: 'result_priority', version: 1, phase: 'arm', configGeneration: 5, epoch: 7
  });
  assert.deepEqual(JSON.parse(JSON.stringify(context.priorityAPI.state())), { phase: 'arm', deadlineAt: 300100 });
  assert.equal(context.priorityAPI.clear(), true);

  const running = {
    requestId: 'real-owned', ownerPublicId: 'owner-public', sessionId: 'owner-session',
    status: 'running', resultFrameEpoch: '0', streamEpoch: '0'
  };
  assert.equal(context.priorityAPI.reconcile(running), true);
  assert.deepEqual(JSON.parse(JSON.stringify(sent[2])), {
    type: 'result_priority', version: 1, phase: 'arm', configGeneration: 5, epoch: 7
  });
  assert.equal(JSON.stringify(sent[2]).includes('real-owned'), false);
  assert.equal(context.priorityAPI.reconcile(running), true);
  assert.equal(sent.length, 3);

  const succeeded = {
    ...running, status: 'succeeded', captureRequired: true, captureAcknowledged: false,
    resultFrameEpoch: '0', streamEpoch: '0',
    resultMinFrameSequence: '0', minFrameSequence: '42', frameSequence: '0'
  };
  assert.equal(context.priorityAPI.reconcile(succeeded), true);
  assert.deepEqual(JSON.parse(JSON.stringify(sent.slice(3))), [
    { type: 'result_priority', version: 1, phase: 'arm', configGeneration: 5, epoch: 7 },
    { type: 'result_priority', version: 1, phase: 'mark', configGeneration: 5, epoch: 7, minSequence: 42 }
  ]);
  assert.deepEqual(JSON.parse(JSON.stringify(context.priorityAPI.state())), { phase: 'mark', deadlineAt: 5100 });
  assert.equal(sent.some((message) => 'requestId' in message || 'ownerPublicId' in message || 'sessionId' in message), false);
  assert.equal(context.priorityAPI.reconcile({ ...succeeded, minFrameSequence: '43' }), false);
  assert.equal(sent.length, 5);
  assert.equal(context.priorityAPI.clear(), true);
  assert.deepEqual(JSON.parse(JSON.stringify(sent.at(-1))), {
    type: 'result_priority', version: 1, phase: 'clear', configGeneration: 5, epoch: 7
  });

  context.priorityAPI.reset();
  assert.equal(context.priorityAPI.reconcile({
    ...succeeded, resultMinFrameSequence: '0', minFrameSequence: '0', frameSequence: '43'
  }), true);
  assert.deepEqual(sent.slice(-2).map((message) => message.phase), ['arm', 'mark']);
  assert.equal(sent.at(-1).minSequence, 43);

  context.priorityAPI.setDisplayed('real-owned');
  assert.equal(context.priorityAPI.reconcile(succeeded), false,
    'a painted result re-armed priority from stale durable capture state');
  assert.equal(sent.at(-1).phase, 'clear');
  const afterPaintClear = context.priorityAPI.sentCount();
  assert.equal(context.priorityAPI.reconcile(succeeded), false);
  assert.equal(context.priorityAPI.sentCount(), afterPaintClear,
    'stale durable state repeatedly transmitted priority after paint');
  assert.deepEqual(JSON.parse(JSON.stringify(context.priorityAPI.state())), { phase: '', deadlineAt: 0 });
  context.priorityAPI.setDisplayed('');
  assert.equal(context.priorityAPI.reconcile(running), true);
  assert.equal(context.priorityAPI.state().phase, 'arm');
  assert.ok(context.priorityAPI.state().deadlineAt > 0);
  context.priorityAPI.setCaptured('real-owned');
  assert.equal(context.priorityAPI.reconcile(running), false,
    'a captured result re-armed priority from stale durable running state');
  assert.deepEqual(JSON.parse(JSON.stringify(context.priorityAPI.state())), { phase: '', deadlineAt: 0 });
  context.priorityAPI.setCaptured('');
  assert.equal(context.priorityAPI.reconcile(running), true);
  context.priorityAPI.reset();
  assert.deepEqual(JSON.parse(JSON.stringify(context.priorityAPI.state())), { phase: '', deadlineAt: 0 },
    'socket/config reset retained a local priority lifetime');
  assert.equal(context.priorityAPI.reconcile(running), true);
  const armDeadline = context.priorityAPI.state().deadlineAt;
  context.priorityAPI.advance(1000);
  assert.equal(context.priorityAPI.reconcile(running), true);
  assert.equal(context.priorityAPI.state().deadlineAt, armDeadline,
    'a duplicate arm extended the local reservation lifetime');
  assert.equal(context.priorityAPI.reconcile(succeeded), true);
  const markDeadline = context.priorityAPI.state().deadlineAt;
  context.priorityAPI.advance(1000);
  assert.equal(context.priorityAPI.reconcile(succeeded), true);
  assert.equal(context.priorityAPI.state().deadlineAt, markDeadline,
    'a duplicate marker extended the local reservation lifetime');
  const expiredRunning = { ...running, expiresAt: new Date(Date.now() - 5000).toISOString() };
  assert.equal(context.priorityAPI.reconcile(expiredRunning), false);
  assert.deepEqual(JSON.parse(JSON.stringify(context.priorityAPI.state())), { phase: '', deadlineAt: 0 });
  const expiredClearCount = context.priorityAPI.sentCount();
  assert.equal(context.priorityAPI.reconcile(expiredRunning), false);
  assert.equal(context.priorityAPI.sentCount(), expiredClearCount,
    'an expired durable request re-armed result priority');
  assert.equal(context.priorityAPI.reconcile({ ...succeeded, captureAcknowledged: true }), false);
  assert.equal(context.priorityAPI.sentCount(), expiredClearCount,
    'an acknowledged result without local capture after reload armed a media reservation');
});

test('video reconnect has one jittered scheduler and resets escalation only after a healthy frame', () => {
  const timers = [];
  const fixedMath = Object.assign(Object.create(Math), { random: () => 0.5 });
  const context = vm.createContext({ JSON, Math: fixedMath, Number, timers });
  vm.runInContext(`
    const WebSocket = { CONNECTING: 0, OPEN: 1 };
    let videoReconnectTimer = null;
    let videoReconnectAttempt = 0;
    let idleDisconnected = false;
    let streamUnsupported = false;
    let videoWs = null;
    const videoReconnectBaseDelaysMs = Object.freeze([1000, 2000, 4000, 8000, 12000]);
    const videoReconnectJitterRatio = 0.2;
    let connectCalls = 0;
    function setTimeout(callback, millis) {
      const timer = { callback, millis, cancelled: false };
      timers.push(timer);
      return timer;
    }
    function clearTimeout(timer) { if (timer) timer.cancelled = true; }
    function viewerIsForeground() { return true; }
    function connectDirectVideo() { connectCalls += 1; }
    function clientLog() {}
    ${between('function cancelVideoReconnectSchedule(resetAttempt) {', '  function connectDirectVideo(options) {')}
    globalThis.reconnectAPI = {
      scheduleVideoReconnect,
      resetVideoReconnectBackoff,
      state() { return { videoReconnectAttempt, pending: videoReconnectTimer != null, connectCalls }; }
    };
  `, context);

  assert.equal(context.reconnectAPI.scheduleVideoReconnect('closed'), true);
  assert.equal(context.reconnectAPI.scheduleVideoReconnect('duplicate'), false);
  assert.equal(timers.length, 1);
  assert.equal(timers[0].millis, 1000);
  timers[0].callback();
  assert.equal(context.reconnectAPI.state().connectCalls, 1);

  assert.equal(context.reconnectAPI.scheduleVideoReconnect('closed_again'), true);
  assert.equal(timers[1].millis, 2000);
  context.reconnectAPI.resetVideoReconnectBackoff('fresh_frame');
  assert.equal(timers[1].cancelled, true);
  assert.deepEqual(JSON.parse(JSON.stringify(context.reconnectAPI.state())), {
    videoReconnectAttempt: 0, pending: false, connectCalls: 1
  });
  assert.equal(context.reconnectAPI.scheduleVideoReconnect('after_healthy'), true);
  assert.equal(timers[2].millis, 1000);
  assert.doesNotMatch(source, /setTimeout\(connectDirectVideo,/);
  assert.match(source, /scheduleVideoReconnect\('video_socket_closed'\)/);
  assert.match(source, /scheduleVideoReconnect\('foreground_video_socket_closed'\)/);
});

test('early socket accepts the exact payload cap for TSF2 and TSF3 and rejects one byte more', () => {
  const start = earlySource.indexOf('var earlyMaxPayloadBytes = 2 * 1024 * 1024;');
  const end = earlySource.indexOf('      function streamURL() {', start);
  assert.ok(start >= 0 && end > start);
  const snippet = earlySource.slice(start, end);
  const context = vm.createContext({ ArrayBuffer, DataView, JSON, Number, Uint8Array });
  vm.runInContext(`
    let now = 10;
    const performance = { now: () => now };
    const document = { visibilityState: 'visible' };
    const sent = [];
    const early = { ws: { readyState: 1, send(value) { sent.push(JSON.parse(value)); } }, config: null,
      frameDependencyMode: '', frameEnvelope: 'tsf2', queue: [], queueBytes: 0,
      firstCaptureAt: 0, lastCaptureAt: 0, error: false };
    ${snippet}
    globalThis.earlyAPI = { earlyEnqueue, earlyFrameMetadata, early, sent };
  `, context);

  function frame(version, sequence, payloadBytes) {
    const headerBytes = version === 'tsf3' ? 93 : 29;
    const raw = new ArrayBuffer(headerBytes + payloadBytes);
    const view = new DataView(raw);
    view.setUint32(0, version === 'tsf3' ? 0x54534633 : 0x54534632);
    view.setUint8(4, 1);
    const fields = version === 'tsf3'
      ? [7, sequence, 1, 1, 1_700_000_000_000_000, 1_700_000_000_000_001,
        1_700_000_000_000_002, 1_700_000_000_000_003, 1_700_000_000_000_004, 1, 10]
      : [7, sequence, 1_700_000_000_000_000];
    fields.forEach((value, index) => writeUint64(view, 5 + index * 8, value));
    return raw;
  }

  context.earlyAPI.earlyEnqueue({ data: JSON.stringify({
    type: 'config', streamEpoch: 7, frameDependencyMode: 'all_intra', frameEnvelope: 'tsf3',
    fps: 1, sourceFps: 1, keyframeIntervalFrames: 1, feedbackVersion: 2, feedbackConfigGeneration: 5
  }) });
  context.earlyAPI.earlyEnqueue({ data: frame('tsf3', 1, 2 * 1024 * 1024) });
  assert.equal(context.earlyAPI.early.queue[0].meta.payloadBytes, 2 * 1024 * 1024);
  assert.equal(context.earlyAPI.sent[0].configGeneration, 5);
  assert.equal(context.earlyAPI.sent[1].receivedSequence, 1);
  assert.equal(context.earlyAPI.sent[1].epoch, 7);
  context.earlyAPI.earlyEnqueue({ data: frame('tsf3', 2, 2 * 1024 * 1024 + 1) });
  assert.equal(context.earlyAPI.early.queue[0].meta.sequence, 1);
  assert.equal(context.earlyAPI.sent.length, 2);

  context.earlyAPI.earlyEnqueue({ data: JSON.stringify({
    type: 'config', streamEpoch: 7, frameDependencyMode: 'all_intra', frameEnvelope: 'tsf2',
    fps: 1, sourceFps: 1, keyframeIntervalFrames: 1, feedbackVersion: 1
  }) });
  context.earlyAPI.earlyEnqueue({ data: frame('tsf2', 3, 2 * 1024 * 1024) });
  assert.equal(context.earlyAPI.early.queue[0].meta.payloadBytes, 2 * 1024 * 1024);
  context.earlyAPI.earlyEnqueue({ data: frame('tsf2', 4, 2 * 1024 * 1024 + 1) });
  assert.equal(context.earlyAPI.early.queue[0].meta.sequence, 3);
});

test('browser-local failure cannot trigger shared source recovery without independent server evidence', () => {
  const context = vm.createContext({ JSON });
  vm.runInContext(`
    let now = 1000;
    const performance = { now: () => now };
    let lastRecoveryServerRecoverAt = 0;
    const recoveryServerRecoverDebounceMs = 12000;
    let configured = true;
    let videoWs = null;
    let lastFrameAt = 0;
    let status = null;
    let mutations = 0;
    function liveStreamSuppressesBackgroundRequest() { return false; }
    function freshStreamStatus() { return status; }
    function backendLooksRecoverable(value) { return Boolean(value && value.backendInactive); }
    function streamStatusStale(value) { return Boolean(value && value.serverStale); }
    function clientLog() {}
    function runSpacetimeMutation(callback) {
      mutations += 1;
      callback({ recoverStream() {} });
      return Promise.resolve();
    }
    ${between('function requestServerRecoveryDebounced(reason, force) {', '  function resetFirstFrameServerRecovery() {')}
    globalThis.recoveryAPI = {
      requestServerRecoveryDebounced,
      setStatus(value) { status = value; },
      state() { return { mutations, lastRecoveryServerRecoverAt }; }
    };
  `, context);

  assert.equal(context.recoveryAPI.requestServerRecoveryDebounced('decoder_failed', true), false);
  context.recoveryAPI.setStatus({ backendInactive: false, serverStale: false });
  assert.equal(context.recoveryAPI.requestServerRecoveryDebounced('socket_closed', true), false);
  context.recoveryAPI.setStatus({ backendInactive: true, serverStale: false });
  assert.equal(context.recoveryAPI.requestServerRecoveryDebounced('server_proved_inactive', true), true);
  assert.deepEqual(JSON.parse(JSON.stringify(context.recoveryAPI.state())), {
    mutations: 1, lastRecoveryServerRecoverAt: 1000
  });
});

test('source keeps exact HDR/action gates and local-only browser recovery ownership', () => {
  assert.match(source, /function streamHasContinuityFrame\(\) \{\s*return currentRenderedFreshness\(performance\.now\(\)\)\.continuityPresentable;/);
  assert.match(source, /function streamHasFreshRenderedFrame\(\) \{\s*return currentRenderedFreshness\(performance\.now\(\)\)\.actionFresh;/);
  assert.match(source, /const presentationContinuity = streamPresentationContinuity\(freshness, reason\);\s*const presentationLive = freshness\.liveLabeled;/);
  assert.match(source, /document\.body\.dataset\.streamLive = presentationLive \? 'true' : 'false';\s*document\.body\.dataset\.streamContinuity = presentationContinuity \? 'true' : 'false';/);
  assert.match(source, /function revealAuthoritativeSDRForConsequentialControl\(\) \{\s*if \(!streamHasFreshRenderedFrame\(\)\) return false;/);
  assert.match(source, /freshness\.actionFresh && epoch > 0 && sequence > 0 && presentationOrdinal > 0/);
  assert.match(source, /experimentalClientHDRController\.ensureExactProof\(stream\.epoch, stream\.sequence\)/);
  assert.match(source, /if \(!recoveryStatus \|\|\s*\(!backendLooksRecoverable\(recoveryStatus\) && !streamStatusStale\(recoveryStatus\)\)\) return false;/);
  assert.match(source, /if \(staleIngressFlowing\) \{[\s\S]*return;\s*\}/);
  assert.match(source, /activeFeedbackVersion === 2 && configGeneration !== activeFeedbackConfigGeneration/);
  assert.match(source, /frame\.version === 'tsf3'/);
  assert.doesNotMatch(source, /scheduleFeedbackPresentationMilestone/);
  assert.match(earlySource, /early\.frameEnvelope !== meta\.version/);
});

test('actual startup SDR seed and next decoded frame keep one HDR config generation', async () => {
  let clock = 100;
  const renderedMetadata = [];
  const surfaceReasons = [];
  class FixtureVideoFrame {
    constructor(_source, init = {}) { this.timestamp = Number(init.timestamp || 0); }
    clone() { return new FixtureVideoFrame(null, { timestamp: this.timestamp }); }
    close() {}
  }
  const renderer = {
    currentBoost: 4,
    initialize() { return Promise.resolve({ canvasEncoding: 'srgb-linear', continuousSurface: true }); },
    setBoost(boost) { this.currentBoost = Number(boost); },
    render(_frame, metadata, options = {}) {
      renderedMetadata.push(Object.assign({}, metadata));
      return Promise.resolve(Object.assign({}, metadata, {
        selectedDisplayBoost: this.currentBoost,
        activationFrame: options.activationFrame === true,
        gpuCompleted: true,
        compositorOpportunitiesCompleted: false
      }));
    },
    present() {},
    waitForPresentCompletion() {
      return Promise.resolve({ gpuCompleted: true, compositorOpportunitiesCompleted: false });
    },
    waitForPresentedCompositorOpportunities(requiredFrames) {
      return Promise.resolve({
        postPresentSource: 'animation_frame',
        postPresentOpportunityCount: requiredFrames,
        gpuCompleted: true,
        compositorOpportunitiesCompleted: true
      });
    },
    cancelCompositorSettlementWaits() {},
    discardPreparedFrame() {},
    dispose() {}
  };
  const controller = new ClientHDRController({
    rendererFactory: () => renderer,
    now: () => clock,
    wallNow: () => 1_000 + clock,
    waitForPaint: () => Promise.resolve(),
    canRevealSurface: () => true,
    canReleaseHoldover: () => true,
    onSurface: (_visible, _presentation, reason) => surfaceReasons.push(String(reason || ''))
  });
  const canvas = { width: 720, height: 1482, getContext() { return {}; } };
  assert.equal(controller.start({ canvas, width: canvas.width, height: canvas.height, boost: 4 }), true);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(controller.snapshot().ready, true);

  const context = vm.createContext({
    Number,
    Date,
    VideoFrame: FixtureVideoFrame,
    window: { VideoFrame: FixtureVideoFrame },
    performance: { now: () => clock },
    canvas,
    controller,
    offerClientHDRCanvasFrame
  });
  vm.runInContext(`
    const experimentalClientHDRController = controller;
    let hasRenderedFrame = true;
    let experimentalMediaPresentationRegionBlocked = false;
    let experimentalMediaPresentationRecoveryPending = false;
    let currentStreamEpoch = 7;
    let lastRenderedFrameEpoch = 7;
    let lastRenderedFrameSequence = 41;
    let lastRenderedFrameConfigGeneration = 23;
    let lastRenderedPresentationOrdinal = 41;
    let lastRenderedFrameTimestamp = 410;
    let lastAcceptedFrameReceivedAt = 95;
    function streamHasFreshRenderedFrame() { return true; }
    function controlCodeHDRFreezeTargetActive() { return false; }
    function experimentalHDRSurfacePresentationAllowed() { return true; }
    function lastRenderedVisualAge() { return 20; }
    function clientLog() {}
    ${between('function offerCurrentSDRFrameToClientHDR(reason) {', '  function syncExperimentalMediaSelectors() {')}
    ${between('function decodedFrameHDRMetadata(frameMetadata, presentationOrdinal, renderedAt) {', '  function clientHDRCanCoordinateDecodedFrame() {')}
    globalThis.hdrGenerationFixture = {
      seed() { return offerCurrentSDRFrameToClientHDR('renderer_ready_sdr_seed'); },
      next() {
        return decodedFrameHDRMetadata({
          epoch: 7,
          sequence: 42,
          configGeneration: 23,
          timestamp: 420,
          visualAgeMillis: 20,
          visualAgeKnown: true,
          visualAgeConservative: true,
          envelopeVersion: 'tsf3',
          receivedAt: 95
        }, 42, performance.now());
      }
    };
  `, context);

  assert.equal(context.hdrGenerationFixture.seed(), true);
  for (let index = 0; index < 8; index += 1) {
    await new Promise((resolve) => setImmediate(resolve));
  }
  let snapshot = controller.snapshot();
  assert.equal(snapshot.sequence, 41);
  assert.equal(snapshot.configGeneration, 23);
  assert.equal(snapshot.presentationState, 'visible');

  clock += 10;
  const normalMetadata = context.hdrGenerationFixture.next();
  const normalFrame = new FixtureVideoFrame(null, { timestamp: normalMetadata.timestamp });
  assert.equal(controller.offerFrame(normalFrame, normalMetadata), true);
  assert.equal(controller.noteSDRFrame(normalMetadata), true);
  for (let index = 0; index < 8; index += 1) {
    await new Promise((resolve) => setImmediate(resolve));
  }
  snapshot = controller.snapshot();
  assert.equal(snapshot.sequence, 42);
  assert.equal(snapshot.configGeneration, 23);
  assert.equal(snapshot.presentationState, 'visible');
  assert.equal(snapshot.proofFresh, true);
  assert.ok(renderedMetadata.length >= 2);
  assert.ok(renderedMetadata.every((metadata) => metadata.configGeneration === 23));
  assert.equal(surfaceReasons.includes('config_generation_mismatch'), false);
  controller.dispose('test_complete');
});

test('control-code reserved media suppresses only local watchdog recovery within its exact bounds', () => {
  const watchdog = between('function chaseLiveStream() {', '\n\t  function recoverAfterVisibilityResume(reason) {');
  const priorityGuard = between('function controlCodeMediaReadSuppressed(now) {', '  function reportDecoderError(error, mode) {');
  const context = vm.createContext({ Date, JSON, Number });
  vm.runInContext(`
    let now = 20_000;
    let serverNow = 1_800_000_000_000;
    const serverClockSkewMs = 0;
    let streamClockAvailable = true;
    const performance = { now: () => now };
    const document = { visibilityState: 'visible' };
    const WebSocket = { OPEN: 1, CLOSED: 3, CLOSING: 2 };
    let videoWs = { readyState: WebSocket.OPEN };
    let idleDisconnected = false;
    let streamUnsupported = false;
    let configured = true;
    let configuredAt = 1_000;
    let lastDecodedFrameAt = 10_000;
    let lastPacketAt = 10_000;
    let lastPacketSequenceAdvancedAt = 10_000;
    let lastStaleIngressDropAt = 0;
    let latestStreamStatus = { phoneDesired: true, phoneConnected: true, phoneStreamState: 'streaming',
      activeVideoClients: 1, lastFrameAgoMillis: 0, streamEpoch: 7,
      updatedAt: new Date(serverNow - 100).toISOString() };
    let statusFresh = true;
    let activeFeedbackVersion = 2;
    let activeFeedbackConfigGeneration = 5;
    let currentStreamEpoch = 7;
    let controlCodeResultPriorityPhase = 'arm';
    let controlCodeResultPriorityConfigGeneration = 5;
    let controlCodeResultPriorityEpoch = 7;
    let controlCodeResultPriorityMinSequence = 0;
    let controlCodeResultPriorityDeadlineAt = now + 100_000;
    let controlCodePreparedCaptureDisplayedRequestID = '';
    let controlCodeResultCapturedRequestID = '';
    const locallyClosedControlCodeRequestIDs = new Set();
    let codeRequest = { requestId: 'owned', owner: 'me', status: 'running', streamEpoch: 7 };
    const streamStaleKeyframeMs = 3000;
    const streamStaleDecoderResetMs = 5000;
    const streamStaleVideoReconnectMs = 8000;
    const streamStaleServerRecoverMs = 12000;
    const streamFirstFrameKeyframeMs = 2000;
    const streamCurrentReportMaxAgeMs = 3500;
    const recoveryKeyframeDebounceMs = 2000;
    let keyframes = 0;
    let decoderResets = 0;
    let reconnects = 0;
    let serverRecoveries = 0;
    let clears = 0;
    function viewerIsForeground() { return true; }
    function freshStreamStatus() { return statusFresh ? latestStreamStatus : null; }
    ${between('function serverFrameAge(status) {', '  function streamStatusStale(status) {')}
    ${between('function backendLooksRecoverable(status) {', '  function viewerIsForeground() {')}
    function streamClockServerUpperAt() { return streamClockAvailable ? Math.round(serverNow * 1000) : 0; }
    function scheduleVideoReconnect() { reconnects += 1; }
    function pauseHiddenStreamAfterGrace() {}
    function requestKeyframeDebounced() { keyframes += 1; return true; }
    function resetDecoderForRecovery() { decoderResets += 1; return true; }
    function reconnectVideoForRecovery() { reconnects += 1; return true; }
    function requestServerRecoveryDebounced() { serverRecoveries += 1; return true; }
    function requestFirstFrameServerRecovery() { serverRecoveries += 1; return true; }
    function decoderStartupGraceActive() { return false; }
    function currentRenderedFreshness() {
      return { hasFrame: true, visualAgeMillis: now - lastDecodedFrameAt, browserReceiveToDecodeMillis: 0,
        decodeToRenderMillis: 0, decoderQueueDelayMillis: 0, streamFreshnessState: 'STALE',
        continuityPresentable: false, liveLabeled: false };
    }
    function lastRenderedVisualAge() { return now - lastDecodedFrameAt; }
    function streamRecoveryDetail(values) { return values; }
    function sendVideoClientLog() {}
    function isOwnedControlCodeRequest(request) { return Boolean(request && request.owner === 'me'); }
    function firstPositiveSafeInteger(...values) {
      for (const value of values) {
        const parsed = Number(value);
        if (Number.isSafeInteger(parsed) && parsed > 0) return parsed;
      }
      return 0;
    }
    function clearControlCodeResultPriority() {
      clears += 1;
      controlCodeResultPriorityPhase = '';
      controlCodeResultPriorityConfigGeneration = 0;
      controlCodeResultPriorityEpoch = 0;
      controlCodeResultPriorityMinSequence = 0;
      controlCodeResultPriorityDeadlineAt = 0;
      return true;
    }
    ${between('function controlCodeRequestExpiryTime(request) {', '  function controlCodeVisualRecoveryRequired() {')}
    ${priorityGuard}
    ${watchdog}
    globalThis.reservedWatchdog = {
      chaseLiveStream,
      state: () => ({ keyframes, decoderResets, reconnects, serverRecoveries, clears }),
      startup() { lastDecodedFrameAt = 0; lastPacketAt = 0; lastPacketSequenceAdvancedAt = 0; },
      decoded() { lastDecodedFrameAt = 10_000; lastPacketAt = 10_000; lastPacketSequenceAdvancedAt = 10_000; },
      failBackend() { latestStreamStatus = { ...latestStreamStatus, phoneConnected: false,
        updatedAt: new Date(serverNow - 100).toISOString() }; },
      restoreBackend() { latestStreamStatus = { ...latestStreamStatus, phoneConnected: true,
        phoneStreamState: 'streaming', lastFrameAgoMillis: 0,
        updatedAt: new Date(serverNow - 100).toISOString() }; },
      activate() {
        codeRequest = { requestId: 'owned', owner: 'me', status: 'running', streamEpoch: 7 };
        locallyClosedControlCodeRequestIDs.clear();
        controlCodeResultPriorityPhase = 'arm';
        controlCodeResultPriorityConfigGeneration = 5;
        controlCodeResultPriorityEpoch = 7;
        controlCodeResultPriorityMinSequence = 0;
        controlCodeResultPriorityDeadlineAt = now + 100_000;
      },
      mark() {
        codeRequest = { requestId: 'owned', owner: 'me', status: 'succeeded', captureRequired: true,
          captureAcknowledged: false, resultFrameEpoch: 7, resultMinFrameSequence: 42 };
        controlCodeResultPriorityPhase = 'mark';
        controlCodeResultPriorityConfigGeneration = 5;
        controlCodeResultPriorityEpoch = 7;
        controlCodeResultPriorityMinSequence = 42;
        controlCodeResultPriorityDeadlineAt = now + 5000;
      },
      mismatchGeneration() { controlCodeResultPriorityConfigGeneration = 6; },
      mismatchMarker() { codeRequest = { ...codeRequest, resultMinFrameSequence: 43 }; },
      mismatchEpoch() { codeRequest = { ...codeRequest, resultFrameEpoch: 8 }; },
      closeRequest() { locallyClosedControlCodeRequestIDs.add('owned'); },
      foreignRequest() { codeRequest = { ...codeRequest, owner: 'other' }; },
      unknownServerAge() { latestStreamStatus = { ...latestStreamStatus, lastFrameAgoMillis: Number.MAX_SAFE_INTEGER }; },
      oldStatus() { statusFresh = false; latestStreamStatus = { ...latestStreamStatus, phoneConnected: false }; },
      currentStatus() { statusFresh = true; },
      delayedBackendReport() { statusFresh = true; streamClockAvailable = true;
        latestStreamStatus = { ...latestStreamStatus, phoneConnected: false,
          updatedAt: new Date(serverNow - streamCurrentReportMaxAgeMs - 1).toISOString() }; },
      unavailableClockBackendReport() { statusFresh = true; streamClockAvailable = false;
        latestStreamStatus = { ...latestStreamStatus, phoneConnected: false,
          updatedAt: new Date(serverNow - 100).toISOString() }; },
      mismatchedEpochBackendReport() { statusFresh = true; streamClockAvailable = true;
        latestStreamStatus = { ...latestStreamStatus, phoneConnected: false, streamEpoch: 8,
          updatedAt: new Date(serverNow - 100).toISOString() }; },
      currentBackendReport() { statusFresh = true; streamClockAvailable = true;
        latestStreamStatus = { ...latestStreamStatus, phoneConnected: false, streamEpoch: 7,
          updatedAt: new Date(serverNow - 100).toISOString() }; },
      sourceAge(value, overrides) { statusFresh = true; streamClockAvailable = true;
        latestStreamStatus = { phoneDesired: true, phoneConnected: true, phoneStreamState: 'streaming',
          activeVideoClients: 1, lastFrameAgoMillis: value, streamEpoch: 7,
          updatedAt: new Date(serverNow - 100).toISOString(), ...overrides }; },
      expireRequest() { codeRequest = { ...codeRequest, expiresAt: new Date(Date.now() - 5000).toISOString() }; },
      expire() { controlCodeResultPriorityDeadlineAt = now; }
    };
  `, context);

  const api = context.reservedWatchdog;
  api.chaseLiveStream();
  assert.deepEqual(JSON.parse(JSON.stringify(api.state())), {
    keyframes: 0, decoderResets: 0, reconnects: 0, serverRecoveries: 0, clears: 0
  });
  api.startup();
  api.chaseLiveStream();
  assert.deepEqual(JSON.parse(JSON.stringify(api.state())), {
    keyframes: 0, decoderResets: 0, reconnects: 0, serverRecoveries: 0, clears: 0
  }, 'a replacement decoder waiting on reserved media triggered first-frame recovery');
  api.decoded();
  api.unknownServerAge();
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 0,
    'unknown server frame age manufactured shared recovery during reserved media');
  api.oldStatus();
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 0,
    'an old backend report manufactured shared recovery during reserved media');
  api.delayedBackendReport();
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 0,
    'a newly received but delayed backend report manufactured shared recovery');
  api.unavailableClockBackendReport();
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 0,
    'an unavailable conservative stream clock authorized shared recovery');
  api.mismatchedEpochBackendReport();
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 0,
    'a current report for another stream epoch authorized shared recovery');
  api.currentBackendReport();
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 1, 'fresh independent backend failure was suppressed');
  assert.equal(api.state().decoderResets, 0);
  assert.equal(api.state().reconnects, 0);
  for (const age of [12000, Number.MAX_SAFE_INTEGER, Number.MAX_SAFE_INTEGER + 1, Infinity, NaN, null, undefined, -1]) {
    api.sourceAge(age);
    api.chaseLiveStream();
    assert.equal(api.state().serverRecoveries, 1,
      'an unproved or non-stalled source age authorized shared recovery during reserved media');
  }
  api.sourceAge(12001, { activeVideoClients: 0 });
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 1, 'source age without an active viewer authorized shared recovery');
  api.sourceAge(12001, { updatedAt: new Date(1_800_000_000_000 - 3501).toISOString() });
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 1, 'a delayed source-age report authorized shared recovery');
  api.sourceAge(12001, { streamEpoch: 8 });
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 1, 'another epoch source-age report authorized shared recovery');
  api.sourceAge(12001);
  api.chaseLiveStream();
  assert.equal(api.state().serverRecoveries, 2, 'a trusted finite hub source stall was suppressed');
  assert.equal(api.state().decoderResets, 0);
  assert.equal(api.state().reconnects, 0);
  api.restoreBackend();
  api.activate();
  api.mismatchGeneration();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 1, 'mismatched reservation was not cleared');
  assert.equal(api.state().keyframes, 1);
  assert.equal(api.state().decoderResets, 1);
  assert.equal(api.state().reconnects, 1);
  api.mark();
  api.mismatchMarker();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 2, 'wrong marker sequence retained reserved-media suppression');
  api.mark();
  api.mismatchEpoch();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 3, 'wrong request epoch retained reserved-media suppression');
  api.activate();
  api.closeRequest();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 4, 'locally closed request retained reserved-media suppression');
  api.activate();
  api.foreignRequest();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 5, 'foreign request retained reserved-media suppression');
  api.activate();
  api.expire();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 6, 'expired reservation was not cleared');
  assert.equal(api.state().keyframes, 6);
  assert.equal(api.state().decoderResets, 6);
  assert.equal(api.state().reconnects, 6);
  api.activate();
  api.startup();
  api.expireRequest();
  api.chaseLiveStream();
  assert.equal(api.state().clears, 7, 'an expired durable request retained reserved-media suppression');
  assert.equal(api.state().keyframes, 7, 'expired priority blocked first-frame recovery');
  assert.equal(api.state().decoderResets, 7);
  assert.equal(api.state().reconnects, 7);
});

test('pre-first-decoded stale ingress does not churn the decoder or socket', () => {
  const watchdog = between('function chaseLiveStream() {', '\n\t  function recoverAfterVisibilityResume(reason) {');
  const context = vm.createContext({ JSON });
  vm.runInContext(`
    let now = 10_000;
    const performance = { now: () => now };
    const document = { visibilityState: 'visible' };
    const WebSocket = { OPEN: 1, CLOSED: 3, CLOSING: 2 };
    let videoWs = { readyState: WebSocket.OPEN };
    let idleDisconnected = false;
    let streamUnsupported = false;
    let configured = true;
    let configuredAt = 1_000;
    let lastDecodedFrameAt = 0;
    let lastPacketAt = 9_500;
    let lastPacketSequenceAdvancedAt = 9_500;
    let lastStaleIngressDropAt = 9_500;
    let latestStreamStatus = { backendInactive: false, lastFrameAgoMillis: 0, activeVideoClients: 1 };
    const streamFirstFrameKeyframeMs = 2_000;
    const streamStaleKeyframeMs = 3_000;
    const streamStaleDecoderResetMs = 5_000;
    const streamStaleVideoReconnectMs = 8_000;
    const streamStaleServerRecoverMs = 12_000;
    const recoveryKeyframeDebounceMs = 2_000;
    let keyframes = 0;
    let decoderResets = 0;
    let reconnects = 0;
    let serverRecoveries = 0;
    function viewerIsForeground() { return true; }
    function freshStreamStatus() { return latestStreamStatus; }
    function serverFrameAge(status) { return Number(status && status.lastFrameAgoMillis || 0); }
    function backendLooksRecoverable(status) { return Boolean(status && status.backendInactive); }
    function scheduleVideoReconnect() {}
    function pauseHiddenStreamAfterGrace() {}
    function requestKeyframeDebounced() { keyframes += 1; return true; }
    function decoderStartupGraceActive() { return false; }
    function resetDecoderForRecovery() { decoderResets += 1; return true; }
    function reconnectVideoForRecovery() { reconnects += 1; }
    function requestFirstFrameServerRecovery() { serverRecoveries += 1; return true; }
    function requestServerRecoveryDebounced() { serverRecoveries += 1; return true; }
    function currentRenderedFreshness() { throw new Error('no decoded frame may query rendered freshness'); }
    function controlCodeMediaReadSuppressed() { return false; }
    function lastRenderedVisualAge() { return 0; }
    function streamRecoveryDetail(values) { return values; }
    function sendVideoClientLog() {}
    ${watchdog}
    globalThis.watchdog = {
      chaseLiveStream,
      setBackendInactive(value) { latestStreamStatus.backendInactive = value; },
      clearStaleIngress() { lastStaleIngressDropAt = 0; },
      state() { return { keyframes, decoderResets, reconnects, serverRecoveries }; }
    };
  `, context);

  context.watchdog.chaseLiveStream();
  assert.deepEqual(JSON.parse(JSON.stringify(context.watchdog.state())), {
    keyframes: 0, decoderResets: 0, reconnects: 0, serverRecoveries: 0
  });

  context.watchdog.setBackendInactive(true);
  context.watchdog.chaseLiveStream();
  assert.equal(context.watchdog.state().serverRecoveries, 1);
  assert.equal(context.watchdog.state().decoderResets, 0);
  assert.equal(context.watchdog.state().reconnects, 0);

  context.watchdog.setBackendInactive(false);
  context.watchdog.clearStaleIngress();
  context.watchdog.chaseLiveStream();
  assert.equal(context.watchdog.state().keyframes, 1);
  assert.equal(context.watchdog.state().decoderResets, 1);
  assert.equal(context.watchdog.state().reconnects, 1);
});

test('a changed HDR boost survives page expiry between one-second pictures without gaining stale authority', async () => {
  async function fixture(options = {}) {
    let clock = 20000;
    let context;
    const timers = new Map();
    const metrics = [];
    let nextTimer = 0;
    let presents = 0;
    let deferredStage = '';
    let completeStage = null;
    async function waitAtStage(stage) {
      if (deferredStage !== stage) return;
      deferredStage = '';
      await new Promise((resolve) => { completeStage = resolve; });
    }
    class Frame {
      constructor(_canvas, init = {}) { this.timestamp = init.timestamp || 1; }
      clone() { return new Frame(null, { timestamp: this.timestamp }); }
      close() {}
    }
    const renderer = {
      boost: 5,
      initialize() { return Promise.resolve({ continuousSurface: true }); },
      setBoost(value) { this.boost = value; },
      async render(_frame, metadata, options = {}) {
        const boost = this.boost;
        await waitAtStage('gpu');
        return { ...metadata, selectedDisplayBoost: boost,
          activationFrame: options.activationFrame === true, gpuCompleted: true };
      },
      present() { presents += 1; },
      waitForPresentCompletion() { return Promise.resolve({ gpuCompleted: true }); },
      waitForPresentedCompositorOpportunities(count) {
        return Promise.resolve({ postPresentSource: 'animation_frame', postPresentOpportunityCount: count,
          gpuCompleted: true, compositorOpportunitiesCompleted: true });
      },
      cancelCompositorSettlementWaits() {}, discardPreparedFrame() {}, dispose() {}
    };
    const setTimer = (callback, delay) => {
      const id = ++nextTimer;
      timers.set(id, { callback, at: clock + delay });
      return id;
    };
    const controller = new ClientHDRController({
      rendererFactory: () => renderer, now: () => clock, wallNow: () => clock,
      setTimer, clearTimer: (id) => timers.delete(id), waitForPaint: () => waitAtStage('paint'),
      canRevealSurface: () => context.api.surfaceAllowed(),
      canReleaseHoldover: (presentation) => context.api.holdoverAllowed(presentation),
      onMetric: (event, detail) => metrics.push({ event, detail })
    });
    context = vm.createContext({
      controller, CLIENT_HDR_ENGINE, normalizeClientHDRDisplayBoost, offerClientHDRCanvasFrame,
      VideoFrame: Frame, window: { VideoFrame: Frame, navigator: { onLine: true } },
      Date: { now: () => clock, parse: Number }, performance: { now: () => clock },
      setTimeout: setTimer, clearTimeout: (id) => timers.delete(id)
    });
    vm.runInContext(`
      const experimentalClientHDRController = controller;
      const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE, displayBoost: 5 };
      const experimentalMediaPresentationRegionBlocked = false;
      const experimentalMediaPresentationRecoveryPending = false;
      const experimentalMediaLifecycleArmed = false;
      const canvas = { width: 720, height: 1482, getContext() { return {}; } };
      const navigator = window.navigator;
      const WebSocket = { OPEN: 1 };
      let videoWs = { readyState: 1 };
      let resultFrozen = false;
      let busy = false;
      const document = { visibilityState: 'visible', body: { dataset: {}, classList: {
        contains: (name) => name === 'control-code-result-visible' && resultFrozen
      } } };
      const codeResultArea = { dataset: { presentation: 'sdr' }, get hidden() { return !resultFrozen; } };
      const codeDialog = { hidden: true };
      const codeDialogOpen = false;
      function controlCodeMutationLaneBusy() { return busy; }
      const pendingBrowserAction = null;
      function memberLimitBlocked() { return false; }
      function lockControlCodeDialogScroll() { throw new Error('busy or frozen result allowed another dialog'); }
      let hasRenderedFrame = true;
      let lastRenderedFrameRenderedAt = performance.now();
      let lastRenderedFrameReceivedAt = performance.now();
      let lastRenderedFrameQueuedAt = performance.now();
      let lastRenderedFrameVisualAgeMillis = 700;
      let lastRenderedFrameVisualAgeKnown = true;
      const lastRenderedFrameVisualAgeConservative = true;
      const lastRenderedFrameEnvelopeVersion = 'tsf3';
      let lastRenderedFrameEpoch = 7;
      let lastRenderedFrameSequence = 10;
      let lastRenderedPresentationOrdinal = 10;
      let lastRenderedFrameTimestamp = 100;
      const currentStreamEpoch = 7;
      const activeFeedbackVersion = 2;
      const activeFeedbackConfigGeneration = 8;
      let lastRenderedFrameConfigGeneration = 8;
      let feedbackRenderedSequence = 10;
      let streamClockBoundAt = performance.now();
      const streamClockBoundMaxAgeMs = 15000;
      let clockCurrent = true;
      let idleDisconnected = false;
      const streamUnsupported = false;
      const activeResumeFlow = null;
      const currentState = null;
      let streamActionFreshnessExpiryTimer = null;
      const streamLiveFreshMaxAgeMs = 1250;
      const streamLiveOkMaxAgeMs = 2000;
      const streamDegradedMaxAgeMs = 3000;
      const streamCurrentReportMaxAgeMs = 3500;
      const streamCurrentReportMaxSequenceLag = 4;
      const streamStaleKeyframeMs = 3000;
      const lastDecoderConfig = { frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1,
        sourceFps: 1, keyframeIntervalFrames: 1, streamEpoch: 7 };
      let status = null;
      function publishStatus() {
        status = { updatedAt: String(Date.now()), phoneDesired: true, phoneConnected: true,
          phoneStreamState: 'streaming', activeVideoClients: 1, phoneClockBoundedCalibrated: true,
          continuity: true, allIntraConfigValid: true, freshnessState: 'LIVE_FRESH',
          liveOKMaxAgeMillis: 2000, lastFrameVisualAgeKnown: true, lastFrameVisualAgeMillis: 700,
          frameEnvelope: 'tsf3', frameDependencyMode: 'all_intra', fps: 1, sourceFps: 1,
          keyframeIntervalFrames: 1, streamEpoch: 7, lastFrameSequence: lastRenderedFrameSequence,
          lastFrameAgoMillis: 0, streamVerdict: 'live' };
      }
      function freshStreamStatus() { return status; }
      function streamStatusStale() { return false; }
      function streamClockBoundIsCurrent() { return clockCurrent; }
      function streamClockServerUpperAt() { return Date.now() * 1000; }
      function viewerIsForeground() { return document.visibilityState === 'visible'; }
      function usesDirectSpacetimeAuth() { return false; }
      function streamHasFreshRenderedFrame() { return currentRenderedFreshness().actionFresh; }
      function streamPresentationContinuity(freshness) { return healthyOneFPSVisualContinuity(freshness); }
      function reconcileStreamResumeSpinner() {}
      function updateControlCodeSubmitAvailability() {}
      function setExperimentalMediaStatus() {}
      function experimentalHDREngineStatus() { return ''; }
      function syncExperimentalMediaSelectors() {}
      function controlCodeHDRFreezeTargetActive() { return resultFrozen; }
      function clientLog() {}
      ${between('function controlCodeExactHDRResultVisible() {', '  function noteExperimentalMediaStreamRegionVisibility(')}
      ${between('function clientHDRHoldoverReleaseAllowed(presentation) {', '  function experimentalMediaDocumentHasFocus() {')}
      ${between('function offerCurrentSDRFrameToClientHDR(reason) {', '  function syncExperimentalMediaSelectors() {')}
      ${between('function applyExperimentalHDRBoost(value, meta) {', '  function observeExperimentalHDREngine(state) {')}
      ${between('function openControlCodeDialog() {', '  function closeControlCodeDialog() {')}
      ${between('function freshnessStateForVisualAge(ageMs) {', '  function clearStreamContinuityStaleGrace() {')}
      ${between('function clientHDRSDRUnavailable(freshness, reason) {', '  function controlCodeFastStateExpiryMillis(state) {')}
      publishStatus();
      globalThis.api = {
        canvas, surfaceAllowed: experimentalHDRSurfacePresentationAllowed,
        holdoverAllowed: clientHDRHoldoverReleaseAllowed,
        choose: (value) => applyExperimentalHDRBoost(value, { reason: 'user' }),
        update: () => updateStreamFreshnessStatus('stream_status'),
        freshness: () => currentRenderedFreshness(),
        seed: () => offerCurrentSDRFrameToClientHDR('test_frame'),
        open: openControlCodeDialog,
        next() {
          lastRenderedFrameSequence += 1; lastRenderedPresentationOrdinal += 1;
          feedbackRenderedSequence = lastRenderedFrameSequence;
          lastRenderedFrameRenderedAt = performance.now();
          lastRenderedFrameReceivedAt = performance.now(); lastRenderedFrameQueuedAt = performance.now();
          streamClockBoundAt = performance.now(); publishStatus();
          updateStreamFreshnessStatus('frame_rendered');
          return offerCurrentSDRFrameToClientHDR('test_frame');
        },
        block(kind) {
          if (kind === 'unknown') lastRenderedFrameVisualAgeKnown = false;
          if (kind === 'clock') clockCurrent = false;
          if (kind === 'config') lastRenderedFrameConfigGeneration = 9;
          if (kind === 'epoch') lastRenderedFrameEpoch = 9;
          if (kind === 'disconnected') videoWs.readyState = 3;
          if (kind === 'hidden') document.visibilityState = 'hidden';
          if (kind === 'result') resultFrozen = true;
          if (kind === 'busy') busy = true;
          if (kind === 'old_picture') lastRenderedFrameVisualAgeMillis = 2100;
          if (kind === 'missing_status') status = null;
          if (kind === 'old_status') status.updatedAt = String(Date.now() - 4000);
        },
        busy: () => busy, result: () => resultFrozen
      };
    `, context);
    const settle = () => new Promise((resolve) => setImmediate(resolve));
    async function advance(delay) {
      const destination = clock + delay;
      for (;;) {
        const next = [...timers.entries()].filter(([, value]) => value.at <= destination)
          .sort((a, b) => a[1].at - b[1].at)[0];
        if (!next) break;
        clock = next[1].at; timers.delete(next[0]); next[1].callback(); await settle();
      }
      clock = destination;
    }
    controller.start({ canvas: context.api.canvas, width: 720, height: 1482, boost: 5 });
    await settle();
    if (options.initialBoost) context.api.choose(options.initialBoost);
    context.api.seed(); context.api.update(); await settle();
    assert.equal(controller.snapshot().presentationState, 'visible');
    return { api: context.api, controller, metrics, advance, settle, presents: () => presents,
      defer(stage) { deferredStage = stage; },
      complete() { assert.equal(typeof completeStage, 'function'); completeStage(); completeStage = null; }
    };
  }

  for (const interval of [950, 1000, 1050, 1100]) {
    const state = await fixture();
    const initial = state.controller.snapshot();
    for (let frame = 0; frame < 5; frame += 1) {
      const presentations = state.presents();
      let elapsed = 0;
      for (const sampleAt of [0, 250, 550, 551, 700, interval - 1]) {
        await state.advance(sampleAt - elapsed);
        elapsed = sampleAt;
        state.api.update();
        const snapshot = state.controller.snapshot();
        const fresh = sampleAt <= 550;
        assert.equal(state.api.freshness().visualAgeMillis, 700 + sampleAt);
        assert.equal(state.api.freshness().actionFresh, fresh,
          `${interval}/${frame}/${sampleAt}: picture authority exceeded 1250 ms`);
        assert.equal(snapshot.proofFresh, fresh);
        assert.equal(state.controller.ensureExactProof(snapshot.epoch, snapshot.sequence), fresh,
          `${interval}/${frame}/${sampleAt}: held HDR authorized exact proof`);
        assert.equal(snapshot.surfaceVisible, true,
          `${interval}/${frame}/${sampleAt}: healthy expiry hid the HDR canvas`);
        assert.equal(snapshot.surfaceTransitions, initial.surfaceTransitions,
          `${interval}/${frame}/${sampleAt}: healthy cadence switched HDR/SDR surfaces`);
        assert.equal(snapshot.rendererGeneration, initial.rendererGeneration);
        assert.equal(state.presents(), presentations, 'expiry redrew the retained picture');
        if (!fresh) assert.equal(state.api.seed(), false, 'expired picture authorized another HDR redraw');
      }
      await state.advance(interval - elapsed);
      assert.equal(state.api.next(), true);
      await state.settle();
      assert.equal(state.controller.snapshot().surfaceVisible, true);
      assert.equal(state.controller.snapshot().proofFresh, true);
      assert.equal(state.controller.snapshot().surfaceTransitions, initial.surfaceTransitions);
    }
    state.controller.dispose('test_complete');
  }

  for (const interval of [950, 1000, 1050, 1100]) {
    const state = await fixture();
    // Choose once during the expired-but-healthy interval, as in the live failure.
    await state.advance(700);
    assert.equal(state.api.freshness().actionFresh, false);
    state.api.choose(2);
    assert.equal(state.controller.snapshot().fallbackKind, 'refresh');
    state.api.update();
    assert.equal(state.api.seed(), false, 'expired source cannot authorize a boost redraw');
    await state.advance(interval - 700);
    assert.equal(state.api.next(), true);
    await state.settle();
    assert.equal(state.controller.snapshot().surfaceVisible, true, `${interval} ms: boost stayed on SDR`);
    assert.equal(state.controller.snapshot().selectedDisplayBoost, 2);
    assert.equal(state.controller.snapshot().presentationState, 'visible');
    assert.ok(state.metrics.some(({ event, detail }) => event === 'presented' && detail.selectedDisplayBoost === 2));
    for (let sample = 0; sample < 4; sample += 1) {
      await state.advance(551);
      assert.equal(state.api.freshness().actionFresh, false);
      assert.equal(state.controller.snapshot().proofFresh, false);
      assert.equal(state.api.seed(), false);
      await state.advance(interval - 551);
      state.api.next(); await state.settle();
      assert.equal(state.controller.snapshot().presentationState, 'visible');
    }
    state.controller.dispose('test_complete');
  }

  for (const { stage, interval } of ['gpu', 'paint'].flatMap((stage) =>
    [950, 1000, 1050, 1100].map((interval) => ({ stage, interval })))) {
    const state = await fixture();
    await state.advance(540);
    assert.equal(state.api.freshness().visualAgeMillis, 1240);
    state.defer(stage);
    state.api.choose(2);
    await state.settle();
    const before = state.presents();
    await state.advance(20); // The actual page timer revokes source authority at 1,251 ms.
    assert.equal(state.api.freshness().actionFresh, false);
    assert.equal(state.controller.snapshot().fallbackKind, 'refresh');
    assert.equal(state.controller.currentSDR, null);
    state.complete(); await state.settle();
    assert.equal(state.presents(), before, `${stage}: expired preparation changed the canvas`);
    assert.equal(state.controller.snapshot().proofFresh, false);
    assert.equal(state.controller.snapshot().fallbackKind, 'refresh',
      `${stage}: expired preparation hardened the pending boost refresh`);
    await state.advance(interval - 560);
    state.api.next(); await state.settle();
    assert.equal(state.controller.snapshot().presentationState, 'visible',
      `${stage}: next fresh picture did not recover the boost`);
    assert.equal(state.controller.snapshot().selectedDisplayBoost, 2);
    state.controller.dispose('test_complete');
  }

  for (const stage of ['gpu', 'paint']) {
    for (const invalid of ['unknown', 'clock', 'config', 'epoch', 'disconnected', 'hidden',
      'old_picture', 'missing_status', 'old_status']) {
      const state = await fixture();
      await state.advance(540); state.defer(stage); state.api.choose(2); await state.settle();
      const before = state.presents();
      await state.advance(20); state.api.block(invalid); state.api.update();
      assert.equal(state.controller.snapshot().fallbackKind, 'hard');
      state.complete(); await state.settle();
      assert.equal(state.presents(), before, `${stage}/${invalid}: revoked candidate changed the canvas`);
      assert.equal(state.controller.snapshot().fallbackKind, 'hard',
        `${stage}/${invalid}: obsolete boost preparation softened a hard failure`);
      assert.equal(state.controller.snapshot().proofFresh, false);
      state.controller.dispose('test_complete');
    }
  }

  for (const invalid of ['unknown', 'clock', 'config', 'epoch', 'disconnected', 'hidden', 'old_picture', 'missing_status', 'old_status']) {
    const state = await fixture();
    await state.advance(700); state.api.choose(2); state.api.block(invalid); state.api.update();
    assert.equal(state.controller.snapshot().fallbackKind, 'hard', `${invalid} kept boost refresh authority`);
    assert.equal(state.controller.snapshot().surfaceVisible, false);
    assert.equal(state.api.seed(), false);
    state.controller.dispose('test_complete');
  }
  const starting = await fixture({ initialBoost: 2 });
  assert.equal(starting.controller.snapshot().selectedDisplayBoost, 2);
  assert.ok(starting.metrics.some(({ event, detail }) => event === 'first_presented' && detail.selectedDisplayBoost === 2));
  const generation = starting.controller.generation;
  starting.api.choose(2); await starting.settle();
  assert.equal(starting.controller.generation, generation, 'matching boost restarted the renderer');
  starting.controller.dispose('test_complete');

  const occupied = await fixture();
  await occupied.advance(700); occupied.api.block('busy'); occupied.api.choose(2); occupied.api.update();
  assert.equal(occupied.api.open(), false, 'boost refresh bypassed the busy admission guard');
  await occupied.advance(300); occupied.api.next(); await occupied.settle();
  assert.equal(occupied.controller.snapshot().presentationState, 'visible');
  assert.equal(occupied.api.busy(), true, 'boost refresh changed action ownership');
  occupied.controller.dispose('test_complete');

  const result = await fixture();
  result.api.block('result');
  const before = result.presents();
  result.api.choose(2); await result.settle();
  assert.equal(result.api.seed(), false);
  assert.equal(result.presents(), before, 'boost changed a frozen result surface');
  assert.equal(result.api.result(), true);
  assert.equal(result.api.open(), false, 'boost refresh bypassed the existing result');
  result.controller.dispose('test_complete');
});
