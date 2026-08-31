package web

import (
	"strings"
	"testing"
)

func TestTicketViewerHDRTelemetryNamesCompositorOpportunitiesPrecisely(t *testing.T) {
	source := ticketAppSource(t)
	fields := substringBetween(t, source,
		"const clientHDRFieldsByPhase = {",
		"  function boundedClientLogJSON(detail, maximumBytes) {")
	for _, required := range []string{
		"'compositorOpportunitiesCompleted'",
		"'postPresentSource'",
		"'postPresentOpportunityCount'",
	} {
		if !strings.Contains(fields, required) {
			t.Fatalf("HDR telemetry phase fields lost compositor fact %q", required)
		}
	}
	for _, misleading := range []string{"'paintCompleted'", "'paint_completion'"} {
		if strings.Contains(fields, misleading) {
			t.Fatalf("HDR telemetry phase fields still claim physical paint through %q", misleading)
		}
	}
}

func TestTicketViewerHDRClientTelemetryFilterRetainsAttemptScopedFailureFacts(t *testing.T) {
	source := ticketAppSource(t)
	filter := substringBetween(t, source,
		"const clientHDRFieldsByPhase = {",
		"  const navigationEntry = performance.getEntriesByType('navigation')[0];")

	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const assetVersion = 'asset-current';
const pageVersion = 'ticket-remote-current';
const document = { visibilityState: 'visible' };
const window = { VideoDecoder() {} };
const navigator = { gpu: {} };
const logs = [];
function enqueueClientLog(entry) { logs.push(JSON.parse(entry.detailJson)); }
function check(value, message) { if (!value) throw new Error(message); }
`+filter+`

const base = {
  assetVersion,
  engine: CLIENT_HDR_ENGINE,
  pipeline: 'webgpu-mainthread-edr-v2',
  attemptId: 41,
  recoveryPhase: 'settling',
  triggerSet: 'dynamic_range_capability_unavailable',
  streamEpoch: 8,
  streamSequence: 17,
  lifecycleGeneration: 6,
  canvasGeneration: 7,
  rendererGeneration: 8,
  retryOrdinal: 1,
  startReason: 'renderer_failure'
};
clientHDRMeasurement('experimental_hdr_diagnostic', undefined, undefined, Object.assign({}, base, {
  phase: 'compositor_settlement_result',
  settlementDeadlineMillis: 2000,
  settlementTimedOut: false,
  postPresentSource: 'animation_frame',
  postPresentOpportunityCount: 2,
  compositorOpportunitiesCompleted: true
}));
clientHDRMeasurement('experimental_hdr_diagnostic', undefined, undefined, Object.assign({}, base, {
  phase: 'gpu_completion_timeout',
  gpuCompletionTimeoutMillis: 1500,
  presentationOrdinal: 17
}));
clientHDRMeasurement('experimental_hdr_diagnostic', undefined, undefined, Object.assign({}, base, {
  phase: 'renderer_init_timeout',
  rendererInitTimeoutMillis: 8000,
  rendererInitElapsedMillis: 12000,
  rendererInitCheckSource: 'completion',
  surfaceVisible: false
}));
clientHDRMeasurement('experimental_hdr_surface_reset', undefined, undefined, Object.assign({}, base, {
  phase: 'surface_reset',
  canvasReplaced: true,
  continuousSurface: true,
  reason: 'device_lost'
}));

check(logs.length === 4, 'client HDR filter dropped a diagnostic phase');
for (const detail of logs) {
  check(detail.attemptId === 41 && detail.lifecycleGeneration === 6 &&
    detail.canvasGeneration === 7 && detail.rendererGeneration === 8 &&
    detail.retryOrdinal === 1,
    'client phase filter stripped attempt or generation authority: ' + JSON.stringify(detail));
  check(detail.triggerSet === 'dynamic_range_capability_unavailable',
    'client phase filter stripped the long lifecycle trigger');
}
check(logs[0].postPresentSource === 'animation_frame' &&
  logs[0].postPresentOpportunityCount === 2 &&
  logs[0].compositorOpportunitiesCompleted === true,
  'client compositor result lost its decisive observable facts');
check(logs[1].gpuCompletionTimeoutMillis === 1500,
  'client GPU timeout lost its bounded deadline');
check(logs[2].rendererInitTimeoutMillis === 8000 &&
  logs[2].rendererInitElapsedMillis === 12000 &&
  logs[2].rendererInitCheckSource === 'completion',
  'client renderer timeout lost its bounded deadline');
check(logs[3].canvasReplaced === true && logs[3].continuousSurface === true,
  'client surface reset lost fresh-canvas continuity facts');
`)
}

func TestTicketViewerRetriesRejectedHDROfferFromAuthoritativeSDRCanvas(t *testing.T) {
	source := ticketAppSource(t)
	renderFrame := substringBetween(t, source,
		"function decodedFrameHDRMetadata(frameMetadata, presentationOrdinal, renderedAt) {",
		"  async function configureDecoder(config, options) {")

	runTicketJavaScript(t, `
let now = 100;
const wallStart = 1000000;
const performance = { now: () => ++now };
const originalDateNow = Date.now;
Date.now = () => wallStart + now;
const window = {};
let serverClockHasLiveSample = true;
let serverClockSkewMs = 0;
let currentStreamEpoch = 7;
let decoderGeneration = 3;
let lastFrameAt = 0;
let lastDecodedFrameAt = 0;
let lastDecodedFrameSequence = 0;
let lastAcceptedFrameSequence = 4;
let lastAcceptedFrameReceivedAt = 90;
let lastAcceptedFrameQueuedAt = 95;
let lastRenderedFrameReceivedAt = 0;
let lastRenderedFrameQueuedAt = 0;
let lastRenderedFrameRenderedAt = 0;
let lastRenderedFrameVisualAgeMillis = 0;
let lastRenderedFrameEpoch = 0;
let lastRenderedFrameSequence = 0;
let lastRenderedPresentationOrdinal = 0;
let authoritativeSDRRenderSerial = 0;
let lastRenderedKeyframeSequence = 0;
let lastRenderedFrameTimestamp = 0;
let firstFrameReceived = false;
let hasRenderedFrame = false;
let firstRenderedTraceSent = true;
let needsKeyFrame = false;
let currentState = null;
let directOfferAccepted = false;
let canvasOfferAccepted = true;
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRecoveryPending = false;
let sourceCloses = 0;
let canvasOffers = 0;
let notes = 0;
let order = [];
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
const experimentalClientHDRController = {
  canCoordinateSDRFrame() { return false; },
  offerFrame(_frame, metadata) {
    order.push('direct:' + metadata.sequence);
    return directOfferAccepted;
  },
  noteSDRFrame(metadata) {
    notes += 1;
    order.push('note:' + metadata.sequence);
  },
  snapshot() { return { active: true }; },
  markSDRStale() {}
};
const canvas = { width: 720, height: 1482 };
const ctx = { drawImage() { order.push('draw'); } };
function offerClientHDRCanvasFrame(controller, offeredCanvas, metadata) {
  if (offeredCanvas !== canvas) throw new Error('retry used the wrong SDR canvas');
  canvasOffers += 1;
  order.push('canvas:' + metadata.sequence);
  controller.noteSDRFrame(metadata);
  return canvasOfferAccepted;
}
function metadata(sequence) {
  return {
    epoch: 7,
    sequence,
    timestamp: (wallStart + now) * 1000,
    visualAgeMillis: 10,
    visualAgeKnown: true,
    receivedAt: 90,
    queuedAt: 95
  };
}
function frame() { return { close() { sourceCloses += 1; } }; }
function check(value, message) { if (!value) throw new Error(message); }
function controlCodePresentationPriorityActive() { return false; }
function experimentalHDRSurfacePresentationAllowed() { return true; }
function shiftFrameMetadata() { throw new Error('explicit metadata was ignored'); }
function resetFirstFrameServerRecovery() {}
function sendVideoSocketClientLog() {}
function maybeCaptureControlCodeResultImage() {}
function hideEmpty() {}
function updateStreamFreshnessStatus() {}
function noteExperimentalMediaForegroundFrame() {}
function renderTicketInteraction() {}
function observeTicketCurrentProofFrame() {}
function updateControlCodeSubmitAvailability() {}
function publishStreamDebug() {}
function scheduleStreamFeedback() {}
function sendVideoClientLog() {}
function preserveCurrentFrame() {}
function showStreamRecovery() {}
function requestKeyframe() {}
`+renderFrame+`

renderDecodedFrame(frame(), 'annexb', metadata(5));
check(order.join(',') === 'draw,direct:5,canvas:5,note:5',
  'rejected HDR frame was not retried from the newly authoritative SDR canvas');
check(canvasOffers === 1 && notes === 1 && sourceCloses === 1,
  'canvas retry duplicated the SDR watermark or source-frame close');
check(lastRenderedFrameEpoch === 7 && lastRenderedFrameSequence === 5 && hasRenderedFrame,
  'canvas retry ran before authoritative SDR metadata was committed');

order = [];
directOfferAccepted = true;
renderDecodedFrame(frame(), 'annexb', metadata(6));
check(order.join(',') === 'draw,direct:6,note:6',
  'an accepted HDR frame should not create a duplicate canvas retry');
check(canvasOffers === 1 && notes === 2 && sourceCloses === 2,
  'accepted HDR offer changed retry or ownership counts');

order = [];
directOfferAccepted = false;
canvasOfferAccepted = false;
renderDecodedFrame(frame(), 'annexb', metadata(7));
check(order.join(',') === 'draw,direct:7,canvas:7,note:7',
  'a rejected canvas retry duplicated the authoritative SDR watermark');
check(canvasOffers === 2 && notes === 3 && sourceCloses === 3,
  'failed canvas retry changed watermark or source-frame ownership counts');
Date.now = originalDateNow;
`)
}

func TestTicketViewerHDRSeedRequiresFreshPositiveSDRWatermark(t *testing.T) {
	source := ticketAppSource(t)
	seedHelper := substringBetween(t, source,
		"function offerCurrentSDRFrameToClientHDR(reason) {",
		"  function syncExperimentalMediaSelectors() {")

	runTicketJavaScript(t, `
let now = 100;
const performance = { now: () => ++now };
const window = {};
const canvas = { width: 720, height: 1482 };
function VideoFrame() {}
let hasRenderedFrame = true;
let streamFresh = false;
let controlPriority = false;
let pageRegionAllowed = true;
let lastRenderedFrameEpoch = 0;
let currentStreamEpoch = 0;
let lastRenderedFrameSequence = 0;
let lastRenderedPresentationOrdinal = 0;
let lastRenderedFrameTimestamp = 1000;
let offers = 0;
let lastMetadata = null;
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRecoveryPending = false;
const experimentalClientHDRController = { snapshot() { return { active: true }; } };
function streamHasFreshRenderedFrame() { return streamFresh; }
function controlCodePresentationPriorityActive() { return controlPriority; }
function experimentalHDRSurfacePresentationAllowed() { return pageRegionAllowed; }
function lastRenderedVisualAge() { return 12; }
function offerClientHDRCanvasFrame(controller, offeredCanvas, metadata, environment) {
  if (controller !== experimentalClientHDRController || offeredCanvas !== canvas || environment !== window) {
    throw new Error('seed used the wrong owner');
  }
  offers += 1;
  lastMetadata = metadata;
  return true;
}
function clientLog() {}
function check(value, message) { if (!value) throw new Error(message); }
`+seedHelper+`

check(offerCurrentSDRFrameToClientHDR('stale') === false && offers === 0,
  'a preserved or reconnecting SDR canvas seeded HDR');
streamFresh = true;
check(offerCurrentSDRFrameToClientHDR('zero') === false && offers === 0,
  'a zero stream watermark seeded HDR');
lastRenderedFrameEpoch = 7;
currentStreamEpoch = 7;
lastRenderedFrameSequence = 42;
lastRenderedPresentationOrdinal = 9;
pageRegionAllowed = false;
check(offerCurrentSDRFrameToClientHDR('offscreen') === false && offers === 0,
  'a page region hidden by details or a control-code result seeded HDR');
pageRegionAllowed = true;
controlPriority = true;
check(offerCurrentSDRFrameToClientHDR('control') === false && offers === 0,
  'control-code priority allowed an HDR seed');
controlPriority = false;
check(offerCurrentSDRFrameToClientHDR('fresh') === true && offers === 1,
  'a fresh positive SDR watermark did not seed HDR');
check(lastMetadata.epoch === 7 && lastMetadata.sequence === 42 &&
  lastMetadata.presentationOrdinal === 9,
  'the accepted HDR seed lost its authoritative watermark');
`)
}

func TestTicketViewerHDRSurfaceYieldsImmediatelyToControlCodePriority(t *testing.T) {
	source := ticketAppSource(t)
	controllerBody := substringBetween(t, source,
		"function ensureExperimentalClientHDRController() {",
		"  function connectExperimentalClientHDR(options) {")
	for _, needle := range []string{
		"canRevealSurface: () => Boolean(",
		"!controlCodePresentationPriorityActive()",
		"onSurface: (visible, _presented, reason) => {",
		"showExperimentalClientHDRSurface(visible, reason);",
	} {
		if !strings.Contains(controllerBody, needle) {
			t.Fatalf("HDR surface does not yield to control-code priority: missing %q", needle)
		}
	}
	for _, obsolete := range []string{"onActivationSurface", "hdr-edr-activation"} {
		if strings.Contains(controllerBody, obsolete) {
			t.Fatalf("obsolete HDR activation handshake remains in browser integration: %q", obsolete)
		}
	}
	if !strings.Contains(source, "replacement.style.removeProperty('dynamic-range-limit');") {
		t.Fatal("a replacement HDR canvas can inherit the previous unrestricted inline range")
	}
	controlBody := substringBetween(t, source,
		"function renderControlCodeRequest(request) {",
		"  function updateControlCodeSubmitAvailability() {")
	for _, needle := range []string{
		"controlCodePresentationPriorityActive()",
		"synchronizeExperimentalHDRSurfaceRegion(",
		"controlCodePriority ? 'control_code_priority' : 'control_code_priority_cleared'",
	} {
		if !strings.Contains(controlBody, needle) {
			t.Fatalf("control-code priority does not immediately revoke HDR: missing %q", needle)
		}
	}
}

func TestTicketViewerControlCodeSubmitRevokesExactHDRBeforeMutation(t *testing.T) {
	source := ticketAppSource(t)
	helper := substringBetween(t, source,
		"function revealAuthoritativeSDRForControlCodeRequest() {",
		"  function currentTicketSliderRegion(state = currentState) {")
	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
const regionTransitions = [];
function synchronizeExperimentalHDRSurfaceRegion(blocked, reason) {
  regionTransitions.push({ blocked, reason });
  return true;
}
function check(value, message) { if (!value) throw new Error(message); }
`+helper+`
revealAuthoritativeSDRForControlCodeRequest();
check(regionTransitions.length === 1 && regionTransitions[0].blocked === true &&
  regionTransitions[0].reason === 'control_code_request_priority',
  'an exact HDR frame stayed visible after control-code submission began');
`)

	submit := substringBetween(t, source,
		"async function submitControlCodeRequest() {",
		"  async function closeCurrentControlCode(openNext) {")
	setInFlight := strings.Index(submit, "controlCodeSubmitInFlight = true;")
	revoke := strings.Index(submit, "revealAuthoritativeSDRForControlCodeRequest();")
	mutation := strings.Index(submit, "await runSpacetimeMutation((client) => client.requestControlCode")
	if setInFlight < 0 || revoke < 0 || mutation < 0 || !(setInFlight < revoke && revoke < mutation) {
		t.Fatal("control-code submission must synchronously reveal SDR before its mutation begins")
	}
	priority := substringBetween(t, source,
		"function controlCodePresentationPriorityActive() {",
		"  function scheduleDecodedFrame(frame, source) {")
	if !strings.Contains(priority, "if (controlCodeSubmitInFlight) return true;") {
		t.Fatal("in-flight control-code submission does not block a concurrent HDR reveal")
	}
}

func TestTicketViewerDetailsVisibilityRevokesAndReacquiresHDR(t *testing.T) {
	source := ticketAppSource(t)
	helper := substringBetween(t, source,
		"function experimentalHDRSurfacePresentationAllowed() {",
		"  function ensureExperimentalClientHDRController() {")
	keepPinned := substringBetween(t, source,
		"function keepFirstScreenPinned(force) {",
		"  function checkServerVersion(payload) {")
	clearCapture := substringBetween(t, source,
		"function clearControlCodeResultCapture() {",
		"  function clearControlCodeRequestLocalState(reason) {")
	runTicketJavaScript(t, `
const classes = new Set(['details-visible']);
const document = {
  visibilityState: 'visible',
  body: { classList: {
    contains(value) { return classes.has(value); },
    remove(value) { classes.delete(value); }
  } }
};
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const experimentalMediaPreferenceController = { enabled: true };
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRegionGeneration = 0;
let experimentalMediaPresentationRecoveryPending = false;
let experimentalMediaPresentationRecoveryReason = '';
let experimentalMediaResumeRetryArmed = false;
let controlPriority = false;
let controlCodeResultCaptureTimer = null;
let controlCodeResultCaptureRequestID = '';
let controlCodeResultCapturedRequestID = 'captured';
let controlCodeCaptureAckInFlightRequestID = 'ack';
let controlCodeResultCaptureStartedAt = 10;
let lastControlCodeMarkerReceivedLogKey = 'received';
let lastControlCodeMarkerWaitingLogKey = 'waiting';
let lastControlCodeCandidateRejectedLogKey = 'rejected';
let pendingControlCodeBaselineFrameFingerprint = {};
let controlCodeBaselineFrameFingerprint = {};
let controlCodeBaselineRequestID = 'baseline';
let lastControlCodeCaptureDebug = {};
let lastControlCodeCaptureKeyframeRequestAt = 10;
let lastControlCodeCaptureKeyframeRetryCount = 1;
let invalidations = [];
let closes = [];
let begins = [];
let updateDetailsCalls = 0;
const panel = null;
const codeResultImage = { hidden: false, removeAttribute() {} };
function controlCodePresentationPriorityActive() {
  return controlPriority || Boolean(controlCodeResultCaptureRequestID) ||
    classes.has('control-code-result-visible');
}
function invalidateExperimentalMediaForegroundRecovery(reason) { invalidations.push(reason); }
function closeExperimentalMedia(options) { closes.push(options); }
function beginExperimentalMediaForegroundRecovery(reason, options) {
  begins.push({ reason, options });
  return true;
}
function updateDetailsReveal() { updateDetailsCalls += 1; }
function clearTimeout() {}
function resetControlCodeSafeGeneratedFrame() {}
function clearControlCodeFrozenCandidateFrame() {}
function clearControlCodePreparedCapture() {}
function publishStreamDebug() {}
function check(value, message) { if (!value) throw new Error(message); }
`+helper+keepPinned+clearCapture+`
check(synchronizeExperimentalHDRSurfaceRegion(false, 'already_visible') === false &&
  invalidations.length === 0 && closes.length === 0 && begins.length === 0,
  'an unblocked no-op region update restarted HDR recovery');
check(experimentalHDRSurfacePresentationAllowed() === false,
  'details view was incorrectly eligible for HDR presentation');
synchronizeExperimentalHDRSurfaceRegion(true, 'details_visible');
check(experimentalMediaPresentationRegionBlocked &&
  experimentalMediaPresentationRecoveryPending &&
  experimentalMediaPresentationRegionGeneration === 1 &&
  invalidations.join(',') === 'presentation_region_blocked' &&
  closes.length === 1 && closes[0].keepEnabled === true && begins.length === 0,
  'details view did not synchronously retire HDR and leave authoritative SDR');
synchronizeExperimentalHDRSurfaceRegion(true, 'details_visible_repeat');
check(invalidations.length === 1 && closes.length === 1 && begins.length === 0,
  'repeated offscreen observations disposed the same HDR surface twice');

classes.add('control-code-result-visible');
classes.delete('details-visible');
check(experimentalHDRSurfacePresentationAllowed() === false,
  'control-code overlay failed to retain presentation-region ownership');
synchronizeExperimentalHDRSurfaceRegion(
  !experimentalHDRSurfacePresentationAllowed(),
  'details_hidden'
);
check(experimentalMediaPresentationRegionBlocked && begins.length === 0 &&
  experimentalMediaPresentationRecoveryPending,
  'hiding details started HDR behind the remaining control-code overlay');
check(requestExperimentalHDRPresentationRegionRecovery('offscreen_frame') === false && begins.length === 0,
  'an offscreen SDR frame consumed the fresh-canvas recovery');

classes.delete('control-code-result-visible');
check(experimentalHDRSurfacePresentationAllowed() === true,
  'visible stream could not reacquire HDR');
synchronizeExperimentalHDRSurfaceRegion(
  false,
  'control_code_result_hidden',
  { foregroundConfirmed: true }
);
check(!experimentalMediaPresentationRegionBlocked &&
  !experimentalMediaPresentationRecoveryPending &&
  experimentalMediaPresentationRegionGeneration === 2 &&
  experimentalMediaResumeRetryArmed && begins.length === 1 &&
  begins[0].reason === 'control_code_result_hidden' &&
  begins[0].options.forceCanvasReset === true &&
  begins[0].options.foregroundConfirmed === true,
  'returning to the stream did not start exactly one fresh-canvas HDR recovery: ' + JSON.stringify({
    blocked: experimentalMediaPresentationRegionBlocked,
    pending: experimentalMediaPresentationRecoveryPending,
    generation: experimentalMediaPresentationRegionGeneration,
    retryArmed: experimentalMediaResumeRetryArmed,
    begins
  }));

classes.add('details-visible');
synchronizeExperimentalHDRSurfaceRegion(true, 'details_visible_again');
const forcedPinBeginBaseline = begins.length;
keepFirstScreenPinned(true);
check(!classes.has('details-visible') && begins.length === forcedPinBeginBaseline + 1 &&
  begins.at(-1).reason === 'details_hidden' &&
  begins.at(-1).options.foregroundConfirmed === false && updateDetailsCalls === 1,
  'forced return to the ticket removed CSS state without unblocking fresh HDR recovery');
keepFirstScreenPinned(true);
check(begins.length === forcedPinBeginBaseline + 1 && updateDetailsCalls === 2,
  'repeated forced pinning created a duplicate presentation-region recovery');

controlPriority = true;
const controlPriorityInvalidationBaseline = invalidations.length;
synchronizeExperimentalHDRSurfaceRegion(false, 'control_code_priority');
check(experimentalMediaPresentationRegionBlocked &&
  invalidations.length === controlPriorityInvalidationBaseline + 1 &&
  begins.length === forcedPinBeginBaseline + 1,
  'queued or active control-code priority did not retain authoritative SDR');
controlPriority = false;
synchronizeExperimentalHDRSurfaceRegion(false, 'control_code_settled');
check(!experimentalMediaPresentationRegionBlocked &&
  begins.length === forcedPinBeginBaseline + 2 &&
  begins.at(-1).reason === 'control_code_settled' &&
  begins.at(-1).options.foregroundConfirmed === false,
  'control-code settlement did not release exactly one fresh HDR recovery');

controlCodeResultCaptureRequestID = 'capture-1';
const captureInvalidationBaseline = invalidations.length;
synchronizeExperimentalHDRSurfaceRegion(false, 'control_code_priority');
check(experimentalMediaPresentationRegionBlocked &&
  invalidations.length === captureInvalidationBaseline + 1,
  'control-code capture did not retain authoritative SDR');
const captureClearBeginBaseline = begins.length;
clearControlCodeResultCapture();
check(controlCodeResultCaptureRequestID === '' &&
  !experimentalMediaPresentationRegionBlocked &&
  begins.length === captureClearBeginBaseline + 1 &&
  begins.at(-1).reason === 'control_code_capture_cleared' &&
  begins.at(-1).options.foregroundConfirmed === false,
  'clearing control-code capture priority did not start exactly one fresh HDR attempt');
`)

	controllerBody := substringBetween(t, source,
		"function ensureExperimentalClientHDRController() {",
		"  function connectExperimentalClientHDR(options) {")
	for _, needle := range []string{
		"experimentalHDRSurfacePresentationAllowed()",
		"setExperimentalMediaStatus('Parastā straume — gaida svaigu HDR kadru…');",
	} {
		if !strings.Contains(controllerBody, needle) {
			t.Fatalf("HDR surface controller lost visible-region authority: missing %q", needle)
		}
	}
	updateDetails := substringBetween(t, source,
		"function updateDetailsReveal() {",
		"  function keepFirstScreenPinned(force) {")
	classToggle := strings.Index(updateDetails, "document.body.classList.toggle('details-visible', revealed);")
	syncRegion := strings.Index(updateDetails, "synchronizeExperimentalHDRSurfaceRegion(")
	visibleAuthority := strings.Index(updateDetails, "!experimentalHDRSurfacePresentationAllowed()")
	if classToggle < 0 || syncRegion < classToggle {
		t.Fatal("scrolling to details does not synchronize CSS visibility with HDR authority")
	}
	if visibleAuthority < syncRegion {
		t.Fatal("scrolling details does not derive the shared region owner after updating CSS visibility")
	}
	offer := substringBetween(t, source,
		"function offerCurrentSDRFrameToClientHDR(reason) {",
		"  function syncExperimentalMediaSelectors() {")
	if !strings.Contains(offer, "!experimentalHDRSurfacePresentationAllowed()") {
		t.Fatal("an offscreen or CSS-hidden stream can seed a false HDR success")
	}
}

func TestTicketViewerControlCodeResultRevokesHDRBeforeReveal(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"function setControlCodeResultVisible(visible) {",
		"function clearControlCodeResultCapture() {")
	revoke := strings.Index(body, "synchronizeExperimentalHDRSurfaceRegion(")
	classify := strings.Index(body, "document.body.classList.toggle('control-code-result-visible', Boolean(visible));")
	reveal := strings.Index(body, "codeResultArea.hidden = !visible;")
	if reveal < 0 || classify < reveal || revoke < classify {
		t.Fatal("control-code result must classify its overlay and transfer HDR authority in the same task")
	}

	runTicketJavaScript(t, `
const classes = new Set();
const document = { body: { classList: {
  contains(value) { return classes.has(value); },
  remove(value) { classes.delete(value); },
  toggle(value, enabled) { if (enabled) classes.add(value); else classes.delete(value); }
} } };
const codeResultArea = { hidden: true };
const panel = null;
let scrolls = 0;
let updates = 0;
const stage = { scrollIntoView() { scrolls += 1; } };
const regionTransitions = [];
function experimentalHDRSurfacePresentationAllowed() {
  return !classes.has('details-visible') && !classes.has('control-code-result-visible');
}
function synchronizeExperimentalHDRSurfaceRegion(blocked, reason, options) {
  regionTransitions.push({ blocked, reason, options: options || {} });
  return true;
}
function updateControlCodeSubmitAvailability() { updates += 1; }
function check(value, message) { if (!value) throw new Error(message); }
`+body+`

setControlCodeResultVisible(false);
check(regionTransitions.length === 0 && updates === 1,
  'an already-hidden control-code result restarted HDR recovery');
setControlCodeResultVisible(true);
check(regionTransitions.length === 1 && regionTransitions[0].blocked === true &&
  regionTransitions[0].reason === 'control_code_result_visible' && scrolls === 1,
  'visible control-code result did not take presentation-region authority exactly once');
setControlCodeResultVisible(true);
check(regionTransitions.length === 1,
  'repeated visible result restarted presentation-region recovery');
setControlCodeResultVisible(false);
check(regionTransitions.length === 2 && regionTransitions[1].blocked === false &&
  regionTransitions[1].reason === 'control_code_result_hidden' &&
  regionTransitions[1].options.foregroundConfirmed === true,
  'result dismissal did not release presentation-region authority exactly once');
setControlCodeResultVisible(false);
check(regionTransitions.length === 2,
  'repeated hidden result restarted fresh-canvas HDR recovery');
`)
}

func TestTicketViewerControlCodeCaptureRevokesHDRBeforePolling(t *testing.T) {
	source := ticketAppSource(t)
	waitForScreenshot := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")

	runTicketJavaScript(t, `
let controlCodeResultCapturedRequestID = '';
const locallyClosedControlCodeRequestIDs = new Set();
let controlCodePreparedCaptureDisplayedRequestID = '';
let controlCodeResultCaptureRequestID = '';
let controlCodeResultCaptureTimer = null;
let controlCodeResultCaptureStartedAt = 0;
let lastControlCodeMarkerReceivedLogKey = '';
let lastControlCodeMarkerWaitingLogKey = '';
let lastControlCodeCandidateRejectedLogKey = '';
const performance = { now: () => 100 };
const codeResultArea = { hidden: true, dataset: {}, style: {} };
const codeResultImage = {
  hidden: true,
  currentSrc: '',
  src: '',
  removeAttribute() { events.push('clear'); }
};
const codeResultStatus = { hidden: false, textContent: '' };
const codeResultValue = { hidden: false, textContent: '', style: {} };
const codeResultTimer = { hidden: false, textContent: '' };
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
const events = [];
function synchronizeExperimentalHDRSurfaceRegion(blocked, reason) {
  events.push('region:' + blocked + ':' + reason);
  return true;
}
function controlCodeCaptureTrace() { events.push('trace'); }
function setControlCodeResultVisible() { events.push('hide'); }
function keepControlCodeVideoAlive() { events.push('keep'); }
function maybeCaptureControlCodeResultImage() {
  events.push('capture');
  return true;
}
function check(value, message) { if (!value) throw new Error(message); }
`+waitForScreenshot+`
const request = { requestId: 'req-1', resultFrameEpoch: 7, resultMinFrameSequence: 42 };
waitForControlCodeResultScreenshot(request);
check(controlCodeResultCaptureRequestID === 'req-1',
  'control-code result capture did not become authoritative');
check(events[0] === 'region:true:control_code_priority',
  'HDR was not revoked before control-code capture setup');
check(events.indexOf('region:true:control_code_priority') < events.indexOf('keep') &&
  events.indexOf('region:true:control_code_priority') < events.indexOf('capture'),
  'HDR was not revoked before control-code polling or capture');
waitForControlCodeResultScreenshot(request);
check(events.filter((event) => event === 'region:true:control_code_priority').length === 1,
  'the same control-code capture revoked HDR more than once');
`)
}

func TestTicketViewerHDRPreferenceActivationOwnsFreshCanvasAndCancelsStaleStart(t *testing.T) {
	source := ticketAppSource(t)
	applyPreference := substringBetween(t, source,
		"function applyExperimentalMediaPreference(enabled, meta) {",
		"  function hideExperimentalMediaCanvas() {")
	if !strings.Contains(source,
		"applyEnabled: (enabled, meta) => applyExperimentalMediaPreference(enabled, meta)") {
		t.Fatal("HDR preference metadata must reach the browser activation owner")
	}

	runTicketJavaScript(t, `
let experimentalMediaCapabilityReady = true;
let experimentalMediaResumeRetryArmed = false;
let experimentalMediaCanvasResetGeneration = 3;
let experimentalMediaState = { enabled: false };
let experimentalMediaPipeline = '';
const starts = [];
function clearExperimentalMediaDynamicRangeRecovery() {}
function cancelExperimentalMediaStart() {}
function invalidateExperimentalMediaForegroundRecovery() {}
function closeExperimentalMedia() { experimentalMediaState.enabled = false; }
function beginExperimentalMediaForegroundRecovery(reason, options) {
  starts.push({ reason, options });
  return true;
}
function check(value, message) { if (!value) throw new Error(message); }
`+applyPreference+`

applyExperimentalMediaPreference(true, { reason: 'user' });
check(experimentalMediaState.enabled && experimentalMediaCanvasResetGeneration === -1 &&
  experimentalMediaResumeRetryArmed,
  'manual OFF-to-ON did not own a fresh HDR recovery budget');
check(starts.length === 1 && starts[0].reason === 'preference_user_enable' &&
  starts[0].options.forceCanvasReset === true,
  'manual enable did not start one fresh-canvas foreground attempt');
applyExperimentalMediaPreference(true, { reason: 'projection' });
check(starts.length === 1, 'matching true projection restarted the enabled attempt');

applyExperimentalMediaPreference(false, { reason: 'user' });
check(!experimentalMediaState.enabled && !experimentalMediaResumeRetryArmed,
  'manual OFF did not cancel HDR recovery');
experimentalMediaCanvasResetGeneration = 3;
applyExperimentalMediaPreference(true, { reason: 'projection' });
check(starts.length === 2 && starts[1].reason === 'preference_projection_restore' &&
  starts[1].options.forceCanvasReset === true,
  'saved true projection did not receive one fresh-canvas foreground attempt');
`)
}
func TestTicketViewerHDRLifecycleResumeIsIdempotentAndPreferenceOwned(t *testing.T) {
	source := ticketAppSource(t)
	lifecycleHelpers := substringBetween(t, source,
		"function armExperimentalMediaLifecycleResume() {",
		"  function mountExperimentalMediaControl() {")

	runTicketJavaScript(t, `
const document = { visibilityState: 'visible' };
let experimentalMediaLifecycleGeneration = 0;
let experimentalMediaLifecycleArmed = false;
let experimentalMediaForegroundPulseWallAt = 0;
let experimentalMediaForegroundSuspensionGap = false;
let experimentalMediaForegroundRecovery = null;
let experimentalMediaResumeRetryArmed = false;
let experimentalClientHDRFailed = false;
let experimentalMediaRendererRetryTimer = null;
let experimentalMediaState = { enabled: true };
let experimentalMediaPreferenceController = { enabled: true };
let experimentalClientHDRController = null;
let experimentalMediaForegroundRecoveredGeneration = -1;
const experimentalMediaFocusRecoveryDebounceMillis = 750;
let invalidations = 0;
let cancellations = 0;
let queued = 0;
let visibleUpdates = 0;
let closes = 0;
const starts = [];
function invalidateExperimentalMediaForegroundRecovery() {
  invalidations += 1;
  experimentalMediaForegroundRecovery = null;
}
function cancelExperimentalMediaStart() { cancellations += 1; }
function clearTimeout() {}
function foregroundRecoveryCurrent(attempt) { return Boolean(attempt && attempt.current); }
function queueExperimentalMediaForegroundRecovery() { queued += 1; return true; }
function beginExperimentalMediaForegroundRecovery(reason, options) {
  starts.push({ reason, options });
  experimentalMediaForegroundRecovery = { current: true };
  return true;
}
function closeExperimentalMedia() { closes += 1; }
function check(value, message) { if (!value) throw new Error(message); }
`+lifecycleHelpers+`

armExperimentalMediaLifecycleResume();
check(experimentalMediaLifecycleGeneration === 1 && experimentalMediaLifecycleArmed &&
  experimentalMediaResumeRetryArmed && invalidations === 1 && cancellations === 1,
  'background lifecycle did not invalidate and arm exactly one foreground attempt');
armExperimentalMediaLifecycleResume();
check(experimentalMediaLifecycleGeneration === 1,
  'clustered background events advanced the lifecycle twice');

check(resumeExperimentalMediaForLifecycle('visibility_resume') === true &&
  starts.length === 1 && starts[0].reason === 'visibility_resume' &&
  starts[0].options.forceCanvasReset === true,
  'visible return did not begin one named fresh-canvas attempt');
check(resumeExperimentalMediaForLifecycle('pageshow') === true &&
  starts.length === 1 && queued === 1,
  'clustered pageshow did not join the active foreground attempt');

experimentalMediaForegroundRecovery = null;
experimentalClientHDRController = {
  snapshot() { return { active: true }; },
  setDocumentVisible(value) { if (value) visibleUpdates += 1; }
};
experimentalMediaForegroundRecoveredGeneration = experimentalMediaLifecycleGeneration;
check(resumeExperimentalMediaForLifecycle('focus') === true &&
  visibleUpdates === 1 && starts.length === 1,
  'healthy matching-generation renderer was not reused idempotently');

experimentalClientHDRController = null;
experimentalMediaPreferenceController.enabled = false;
check(resumeExperimentalMediaForLifecycle('focus') === false,
  'saved disabled preference started HDR during resume');
document.visibilityState = 'hidden';
experimentalMediaPreferenceController.enabled = true;
check(resumeExperimentalMediaForLifecycle('visibility_resume') === false,
  'hidden event started a foreground renderer');
`)
}
func TestTicketViewerRetriesColdCapabilityDiscoveryAndRestoresSavedHDR(t *testing.T) {
	source := ticketAppSource(t)
	discovery := substringBetween(t, source,
		"function fetchExperimentalMediaCapability() {",
		"  if (cfg.experimentalMediaCandidate === true) discoverExperimentalMediaCapability();")

	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const CLIENT_HDR_PIPELINE = 'webgpu-mainthread-edr-v2';
const CLIENT_HDR_PRESENTATION_KIND = 'browser-edr';
const CLIENT_HDR_TARGET_DISPLAY_BOOST = 4;
const CLIENT_HDR_DISPLAY_BOOSTS = Object.freeze([2, 3, 4, 5, 6]);
const document = { visibilityState: 'visible' };
const experimentalMediaMount = {};
const experimentalMediaCanvas = {};
const experimentalMediaCapabilityFetchTimeoutMillis = 3000;
const experimentalMediaCapabilityRetryDelays = Object.freeze([250, 1000]);
let experimentalMediaCapabilityDiscoveryPromise = null;
let experimentalMediaCapabilityDiscoveryRetryTimer = null;
let experimentalMediaCapabilityDiscoveryAttempt = 0;
let experimentalMediaAccountProjectionAvailable = true;
let experimentalMediaEngineProjectionObserved = false;
let experimentalMediaBoostProjectionObserved = false;
let experimentalClientCapabilityAllowed = false;
let experimentalMediaCapabilityReady = false;
let experimentalMediaState = { engine: CLIENT_HDR_ENGINE, boostSelectorAllowed: false, engineStatus: '' };
let mounted = 0;
let applyCalls = 0;
let applyMeta = null;
let fetchCalls = 0;
let observedBoost = 0;
const timers = [];
const experimentalMediaPreferenceController = { enabled: true };
const experimentalHDRBoostPreferenceController = { observe(boost) { observedBoost = Number(boost); } };
function clearTimeout(timer) { if (timer) timer.cancelled = true; }
function setTimeout(callback, millis) {
  const timer = { callback, millis, cancelled: false };
  timers.push(timer);
  return timer;
}
function resolveCapabilityHDREngine() { return CLIENT_HDR_ENGINE; }
function experimentalHDREngineStatus() { return 'ready'; }
function mountExperimentalMediaControl() { mounted += 1; }
function applyExperimentalMediaPreference(enabled, meta) {
  if (enabled) applyCalls += 1;
  applyMeta = meta || null;
}
async function fetch() {
  fetchCalls += 1;
  return {
    ok: true,
    async json() {
	  if (fetchCalls === 1) return new Promise(() => {});
	  if (fetchCalls === 2) throw new Error('synthetic malformed response');
      return {
        allowed: true,
        allowedEngines: [CLIENT_HDR_ENGINE],
        allowedDisplayBoosts: Array.from(CLIENT_HDR_DISPLAY_BOOSTS),
        clientPipeline: CLIENT_HDR_PIPELINE,
        presentationKind: CLIENT_HDR_PRESENTATION_KIND,
        targetDisplayBoost: CLIENT_HDR_TARGET_DISPLAY_BOOST,
        selectedEngine: CLIENT_HDR_ENGINE,
        selectedDisplayBoost: 5
      };
    }
  };
}
function check(value, message) { if (!value) throw new Error(message); }
`+discovery+`

(async () => {
  const initial = discoverExperimentalMediaCapability({ reason: 'cold_open' });
  await new Promise((resolve) => setImmediate(resolve));
  const fetchDeadline = timers.find((timer) => !timer.cancelled && timer.millis === 3000);
  check(fetchCalls === 1 && fetchDeadline,
    'hung cold-start capability response body did not receive a deadline');
  fetchDeadline.callback();
  await initial;
  const retry = timers.find((timer) => !timer.cancelled && timer.millis === 250);
  check(retry, 'timed-out cold-start capability lookup did not schedule its first bounded retry');
  retry.callback();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  const parseRetry = timers.find((timer) => !timer.cancelled && timer.millis === 1000);
  check(fetchCalls === 2 && !experimentalMediaCapabilityReady && parseRetry,
    'malformed capability response did not schedule the second bounded retry');
  parseRetry.callback();
  await new Promise((resolve) => setImmediate(resolve));
  await new Promise((resolve) => setImmediate(resolve));
  check(fetchCalls === 3 && experimentalMediaCapabilityReady,
    'successful second retry did not restore browser HDR capability');
  check(experimentalMediaState.boostSelectorAllowed && observedBoost === 5,
    'successful member capability did not expose and restore the account boost');
  check(mounted === 1 && applyCalls === 1 && applyMeta && applyMeta.reason === 'projection',
    'successful retry did not reapply the saved enabled HDR preference exactly once');
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
`)
}

func TestTicketViewerHDRBoostSelectorUsesMemberProjectionNotOwnerEngine(t *testing.T) {
	source := ticketAppSource(t)
	observe := substringBetween(t, source,
		"function observeExperimentalHDREngine(state) {",
		"  function applyExperimentalMediaPreference(enabled, meta) {")

	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
let experimentalMediaOwnerProjectionAvailable = false;
let experimentalMediaEngineProjectionObserved = false;
let experimentalMediaBoostProjectionObserved = false;
let experimentalMediaAccountProjectionAvailable = false;
let experimentalMediaState = {
  engine: CLIENT_HDR_ENGINE,
  boostSelectorAllowed: false,
  engineSaving: false,
  engineStatus: 'ready',
  enabled: true
};
const observedBoosts = [];
const chosenBoosts = [];
const experimentalHDRBoostPreferenceController = {
  observe(boost) { observedBoosts.push(Number(boost)); },
  choose(boost) { chosenBoosts.push(Number(boost)); }
};
function experimentalHDREngineStatus() { return 'ready'; }
function syncExperimentalMediaSelectors() {}
function closeExperimentalMedia() { throw new Error('owner diagnostic changed member presentation'); }
function beginExperimentalMediaForegroundRecovery() { throw new Error('owner diagnostic restarted member HDR'); }
function clientHDREngineProjectionDecision(projection, previouslyAvailable) {
  const ownerProjectionAvailable = Boolean(projection && projection.ownerProjectionAvailable);
  return {
    ownerProjectionAvailable,
    roleLost: Boolean(previouslyAvailable && !ownerProjectionAvailable),
    engine: CLIENT_HDR_ENGINE
  };
}
function check(value, message) { if (!value) throw new Error(message); }
`+observe+`

observeExperimentalHDREngine({ memberHDREngine: { ownerProjectionAvailable: false } });
observeExperimentalHDRBoost({
  memberHDRBoost: { accountProjectionAvailable: true, selectedDisplayBoost: 4 }
});
check(!experimentalMediaOwnerProjectionAvailable && experimentalMediaEngineProjectionObserved === false,
  'ordinary member unexpectedly received the owner diagnostic projection');
check(experimentalMediaAccountProjectionAvailable && experimentalMediaBoostProjectionObserved &&
  experimentalMediaState.boostSelectorAllowed && observedBoosts.join(',') === '4',
  'ordinary member account projection did not expose the HDR boost selector');
chooseExperimentalHDRBoost(5);
check(chosenBoosts.join(',') === '5',
  'ordinary member could not choose an advertised HDR boost');

observeExperimentalHDREngine({ memberHDREngine: { ownerProjectionAvailable: true } });
observeExperimentalHDREngine({ memberHDREngine: { ownerProjectionAvailable: false } });
check(experimentalMediaState.boostSelectorAllowed && experimentalMediaAccountProjectionAvailable,
  'owner diagnostic projection loss revoked the ordinary member HDR controls');

observeExperimentalHDRBoost({
  memberHDRBoost: { accountProjectionAvailable: false, selectedDisplayBoost: 6 }
});
chooseExperimentalHDRBoost(6);
check(!experimentalMediaState.boostSelectorAllowed && !experimentalMediaBoostProjectionObserved &&
  chosenBoosts.join(',') === '5',
  'missing member account projection left a stale writable boost selector');

observeExperimentalHDRBoost({
  memberHDRBoost: { accountProjectionAvailable: true, selectedDisplayBoost: 6 }
});
chooseExperimentalHDRBoost(6);
check(experimentalMediaState.boostSelectorAllowed && observedBoosts.join(',') === '4,6' &&
  chosenBoosts.join(',') === '5,6',
  'restored member account projection did not restore the HDR boost selector');
`)
}

func TestTicketViewerHDRControlUsesGeneralAvailabilityWording(t *testing.T) {
	source := ticketAppSource(t)
	control := substringBetween(t, source,
		"function mountExperimentalMediaControl() {",
		"  function fetchExperimentalMediaCapability() {")
	for _, required := range []string{
		"label: 'HDR skats'",
		"aria-label=\"HDR skats\"",
		"!experimentalMediaState.boostSelectorAllowed",
		"CLIENT_HDR_DISPLAY_BOOSTS.map",
		"Pārlūka spilgtums",
	} {
		if !strings.Contains(source, required) && !strings.Contains(control, required) {
			t.Fatalf("general-availability HDR control is missing %q", required)
		}
	}
	if strings.Contains(strings.ToLower(source), "eksperiment") {
		t.Fatal("general-availability HDR still contains user-facing experimental wording")
	}
	if strings.Contains(source, "boostStatus: 'Pārlūka HDR spilgtums: 6×.'") {
		t.Fatal("HDR control still hard-codes the retired 6x default")
	}
}

func TestTicketViewerHDRSurfaceIsIsolatedAndHiddenUntilFullTarget(t *testing.T) {
	template := ticketIndexTemplate(t)
	standby := substringBetween(t, template,
		`#experimentalMediaCanvas[data-client-hdr-surface="standby"] {`,
		`#experimentalMediaCanvas[data-client-hdr-surface="visible"] {`)
	if !strings.Contains(standby, "visibility: hidden;") || !strings.Contains(standby, "z-index: 0;") {
		t.Fatalf("HDR standby CSS must hide the unpresented surface: %q", standby)
	}
	visible := substringBetween(t, template,
		`#experimentalMediaCanvas[data-client-hdr-surface="visible"] {`,
		`#experimentalMediaCanvas[hidden] {`)
	if !strings.Contains(visible, "visibility: visible;") || !strings.Contains(visible, "z-index: 2;") {
		t.Fatalf("fresh HDR CSS must promote the surface above SDR: %q", visible)
	}
	hidden := substringBetween(t, template,
		`#experimentalMediaCanvas[hidden] {`,
		"    .experimental-media-control {")
	if !strings.Contains(hidden, "display: none;") {
		t.Fatalf("disabled HDR CSS must remove the surface: %q", hidden)
	}
	if !strings.Contains(template, ".stage-page {\n      position: relative;") ||
		!strings.Contains(template, "position: absolute;") ||
		!strings.Contains(template, "dynamic-range-limit: no-limit;") {
		t.Fatalf("HDR canvas must use an independent no-limit stage-page overlay")
	}
	stageEnd := strings.Index(template, "      </div>\n      <canvas id=\"experimentalMediaCanvas\"")
	if stageEnd < 0 {
		t.Fatalf("HDR canvas must be a sibling of the transitioned stage, not its descendant")
	}
	if !strings.Contains(template, "body.details-visible #experimentalMediaCanvas") ||
		!strings.Contains(template, "body.control-code-result-visible #experimentalMediaCanvas") ||
		!strings.Contains(template, "body.details-visible #ticketRegisterOverlay") ||
		!strings.Contains(template, "body.control-code-result-visible #ticketRegisterOverlay") {
		t.Fatalf("independent HDR overlay must fail closed for details and control-code views")
	}
	if !strings.Contains(template, "</div>\n      <canvas id=\"experimentalMediaCanvas\"") ||
		!strings.Contains(template, "</canvas>\n      <label id=\"ticketRegisterOverlay\"") {
		t.Fatalf("registration control must remain above the independent HDR canvas")
	}

	source := ticketAppSource(t)
	show := substringBetween(t, source,
		"function showExperimentalClientHDRSurface(visible, reason) {",
		"  function ensureExperimentalClientHDRController() {")
	for _, needle := range []string{
		"experimentalMediaCanvas.hidden = false;",
		"visible ? 'visible' : 'standby'",
		"visible ? 'false' : 'true'",
	} {
		if !strings.Contains(show, needle) {
			t.Fatalf("HDR surface owner does not keep the prepared surface safely staged: missing %q", needle)
		}
	}
	if !strings.Contains(source, "const streamLayout = stagePage || stage;") ||
		!strings.Contains(source, "streamLayout.style.setProperty('--stream-left'") {
		t.Fatalf("independent HDR overlay does not share the authoritative stream geometry")
	}
}

func TestTicketViewerHDRCanvasIsReplacedOncePerGenuineLifecycle(t *testing.T) {
	source := ticketAppSource(t)
	canvasHelpers := substringBetween(t, source,
		"function refreshExperimentalClientCapability() {",
		"  function showExperimentalClientHDRSurface(visible, reason) {")

	runTicketJavaScript(t, `
let replacements = 0;
function makeCanvas(name) {
  const parent = { name: name + '-parent' };
  return {
    name,
    parentNode: parent,
    hidden: false,
    width: 1,
    height: 1,
    dataset: { clientHdrSurface: 'old', clientHdrSurfaceReason: 'old' },
    attributes: {},
    cloneNode() { return makeCanvas(name + '-replacement'); },
    setAttribute(key, value) { this.attributes[key] = value; },
    replaceWith(replacement) {
      replacements += 1;
      replacement.parentNode = this.parentNode;
      this.parentNode = null;
    }
  };
}
let experimentalMediaCanvas = makeCanvas('initial');
let experimentalMediaCanvasContextKind = 'webgpu';
let experimentalMediaCanvasResetGeneration = -1;
let experimentalMediaLifecycleGeneration = 1;
let experimentalMediaCanvasGeneration = 0;
let experimentalClientCapability = {};
let experimentalMediaState = { engine: 'client_webgpu_v2' };
const canvas = { width: 720, height: 1482 };
const metrics = [];
function clientHDRCapability() { return { supported: true }; }
function experimentalHDREngineStatus() { return 'ready'; }
function reportClientHDRMetric(event, detail) { metrics.push({ event, detail }); }
function check(value, message) { if (!value) throw new Error(message); }
`+canvasHelpers+`

const first = prepareExperimentalMediaCanvas(720, 1482, 'webgpu', {
  forceCanvasReset: true,
  reason: 'lifecycle_resume'
});
check(first && replacements === 1 && experimentalMediaCanvasGeneration === 1,
  'first lifecycle did not replace the stale WebKit canvas');
check(experimentalMediaCanvasResetGeneration === 1 && metrics.length === 1,
  'first lifecycle replacement was not recorded');

prepareExperimentalMediaCanvas(720, 1482, 'webgpu', {
  forceCanvasReset: true,
  reason: 'clustered_focus'
});
check(replacements === 1 && metrics.length === 1,
  'clustered foreground events replaced the same lifecycle twice');

experimentalMediaCanvasResetGeneration = -1;
prepareExperimentalMediaCanvas(720, 1482, 'webgpu', {
  forceCanvasReset: true,
  reason: 'preference_user_enable'
});
check(replacements === 2 && experimentalMediaCanvasGeneration === 2 &&
  experimentalMediaCanvasResetGeneration === 1 && metrics.length === 2,
  'a real manual re-enable did not replace the prior canvas in the same lifecycle');
prepareExperimentalMediaCanvas(720, 1482, 'webgpu', {
  forceCanvasReset: true,
  reason: 'projection_echo'
});
check(replacements === 2 && metrics.length === 2,
  'a redundant enabled projection replaced the manual HDR canvas');

experimentalMediaLifecycleGeneration = 2;
prepareExperimentalMediaCanvas(720, 1482, 'webgpu', {
  forceCanvasReset: true,
  reason: 'next_lifecycle'
});
check(replacements === 3 && experimentalMediaCanvasGeneration === 3 &&
  experimentalMediaCanvasResetGeneration === 2 && metrics.length === 3,
  'next genuine lifecycle did not receive exactly one fresh canvas');
`)
}

func TestTicketViewerReportsRecoverableHDRResumeDiagnostics(t *testing.T) {
	source := ticketAppSource(t)
	report := substringBetween(t, source,
		"function reportClientHDRMetric(event, detail) {",
		"  function prepareExperimentalMediaCanvas(width, height, contextKind, options) {")
	for _, needle := range []string{
		"edr_activation_presented: 'experimental_hdr_activation_presented'",
		"event === 'paint_wait_timeout'",
		"event === 'paint_wait_failed'",
		"event === 'renderer_init_timeout'",
		"event === 'frame_clone_failed'",
		"surface_reset: 'experimental_hdr_surface_reset'",
		"clientHDRMeasurement('experimental_hdr_diagnostic'",
	} {
		if !strings.Contains(report, needle) {
			t.Fatalf("recoverable browser HDR resume diagnostic is not routed to production telemetry: missing %q", needle)
		}
	}
	if !strings.Contains(source, "'paintPending', 'paintWaitTimeoutMillis', 'paintWaitTimeoutPending'") {
		t.Fatal("HDR session summary must expose whether the post-resume paint handoff remained pending")
	}
}
