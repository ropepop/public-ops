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
let lastRenderedFrameVisualAgeKnown = false;
let lastRenderedFrameVisualAgeConservative = false;
let lastRenderedFrameEnvelopeVersion = '';
let lastRenderedFrameEpoch = 0;
let lastRenderedFrameSequence = 0;
let lastRenderedFrameConfigGeneration = 0;
let lastRenderedPresentationOrdinal = 0;
let authoritativeSDRRenderSerial = 0;
let lastRenderedKeyframeSequence = 0;
let lastRenderedFrameTimestamp = 0;
let firstFrameReceived = false;
let hasRenderedFrame = false;
let firstRenderedTraceSent = true;
let needsKeyFrame = false;
let currentState = null;
let activeFeedbackVersion = 0;
let activeFeedbackConfigGeneration = 0;
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
function controlCodeCapturePriorityActive() { return false; }
function controlCodeHDRFreezeTargetActive() { return false; }
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

func TestTicketViewerHDRSeedRequiresFreshPositiveSDRWatermarkDuringControlCode(t *testing.T) {
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
let lastRenderedFrameConfigGeneration = 0;
let lastRenderedPresentationOrdinal = 0;
let lastRenderedFrameTimestamp = 1000;
let offers = 0;
let lastMetadata = null;
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRecoveryPending = false;
const experimentalClientHDRController = { snapshot() { return { active: true }; } };
function streamHasFreshRenderedFrame() { return streamFresh; }
function controlCodeHDRFreezeTargetActive() { return false; }
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
lastRenderedFrameConfigGeneration = 8;
lastRenderedPresentationOrdinal = 9;
pageRegionAllowed = false;
check(offerCurrentSDRFrameToClientHDR('offscreen') === false && offers === 0,
  'a page region hidden by details or a control-code result seeded HDR');
pageRegionAllowed = true;
controlPriority = true;
check(offerCurrentSDRFrameToClientHDR('control') === true && offers === 1,
  'control-code execution blocked an HDR offer from the authoritative SDR frame');
check(lastMetadata.epoch === 7 && lastMetadata.sequence === 42 && lastMetadata.configGeneration === 8 &&
  lastMetadata.presentationOrdinal === 9,
  'the control-code HDR offer lost its authoritative SDR watermark');
controlPriority = false;
check(offerCurrentSDRFrameToClientHDR('fresh') === true && offers === 2,
  'a fresh positive SDR watermark did not seed HDR');
check(lastMetadata.epoch === 7 && lastMetadata.sequence === 42 && lastMetadata.configGeneration === 8 &&
  lastMetadata.presentationOrdinal === 9,
  'the accepted HDR seed lost its authoritative watermark');
`)
}

func TestTicketViewerHDRSurfaceRemainsAvailableDuringControlCodePriority(t *testing.T) {
	source := ticketAppSource(t)
	controllerBody := substringBetween(t, source,
		"function ensureExperimentalClientHDRController() {",
		"  function connectExperimentalClientHDR(options) {")
	for _, needle := range []string{
		"canRevealSurface: () => Boolean(",
		"onSurface: (visible, _presented, reason) => {",
		"showExperimentalClientHDRSurface(visible, reason);",
	} {
		if !strings.Contains(controllerBody, needle) {
			t.Fatalf("HDR surface owner is missing %q", needle)
		}
	}
	if strings.Contains(controllerBody, "!controlCodeCapturePriorityActive()") {
		t.Fatal("control-code execution still revokes the otherwise valid HDR surface")
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
		"  function setDetailsPanelVisible(visible) {")
	if strings.Contains(controlBody, "synchronizeExperimentalHDRSurfaceRegion(") ||
		strings.Contains(controlBody, "control_code_priority_cleared") {
		t.Fatal("queued or running control-code rendering still retires and restarts HDR")
	}
}

func TestTicketViewerControlCodeSubmitPreservesHDRBeforeMutation(t *testing.T) {
	source := ticketAppSource(t)
	submit := substringBetween(t, source,
		"async function submitControlCodeRequest() {",
		"  async function closeCurrentControlCode(openNext) {")
	setInFlight := strings.Index(submit, "controlCodeSubmitInFlight = true;")
	mutation := strings.Index(submit, "client.requestControlCode(digits, fastRevision")
	if setInFlight < 0 || mutation < 0 || setInFlight > mutation {
		t.Fatal("control-code submission must establish its in-flight state before mutation")
	}
	for _, forbidden := range []string{
		"revealAuthoritativeSDRForControlCodeRequest();",
		"control_code_request_priority",
		"synchronizeExperimentalHDRSurfaceRegion(",
	} {
		if strings.Contains(submit, forbidden) {
			t.Fatalf("control-code submission still retires HDR before execution: %q", forbidden)
		}
	}
}

func TestTicketViewerDetailsVisibilityKeepsPresentedHDRSurfaceAlive(t *testing.T) {
	source := ticketAppSource(t)
	presentation := substringBetween(t, source,
		"function experimentalHDRSurfacePresentationAllowed() {",
		"  function experimentalMediaDocumentHasFocus() {")
	actionGate := substringBetween(t, source,
		"function clientHDRConsequentialControlProofReady() {",
		"  function currentTicketSliderRegion(state = currentState) {")
	updateDetails := substringBetween(t, source,
		"function updateDetailsReveal() {",
		"  function keepFirstScreenPinned(force) {")
	runTicketJavaScript(t, `
const classes = new Set();
const attributes = new Map();
const canvasIdentity = { id: 'canvas-1' };
const rendererIdentity = { id: 'renderer-1' };
let scrollIntoViewCalls = 0;
const document = {
  visibilityState: 'visible',
  body: { dataset: {}, classList: {
    contains(value) { return classes.has(value); },
    toggle(value, enabled) { if (enabled) classes.add(value); else classes.delete(value); }
  } }
};
const window = { scrollY: 0 };
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const codeResultArea = { dataset: {} };
function controlCodeExactHDRResultVisible() {
  return classes.has('control-code-result-visible') &&
    codeResultArea.dataset.presentation === 'exact-hdr';
}
let experimentalMediaStreamRegionVisible = true;
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
let experimentalMediaCanvas = {
  identity: canvasIdentity,
  dataset: { clientHdrSurface: 'visible', clientHdrSurfaceReason: 'fresh' },
  get hidden() { return attributes.has('hidden'); },
  set hidden(value) { if (value) attributes.set('hidden', ''); else attributes.delete('hidden'); },
  setAttribute(name, value) { attributes.set(name, String(value)); },
  getAttribute(name) { return attributes.has(name) ? attributes.get(name) : null; },
  hasAttribute(name) { return attributes.has(name); },
  scrollIntoView() { scrollIntoViewCalls += 1; }
};
experimentalMediaCanvas.setAttribute('aria-hidden', 'false');
const originalCanvas = experimentalMediaCanvas;
const regionChanges = [];
let controllerRegionVisible = true;
const controllerSnapshot = {
  active: true,
  ready: true,
  rendererActive: true,
  firstPresented: true,
  surfaceVisible: true,
  visualHoldover: false,
  proofFresh: true,
  rendererGeneration: 7,
  presentationOrdinal: 31,
  presentationState: 'visible',
  epoch: 5,
  sequence: 31,
  rendererIdentity
};
const experimentalClientHDRController = {
  setStreamRegionVisible(visible) {
    const next = Boolean(visible);
    if (next === controllerRegionVisible) return false;
    controllerRegionVisible = next;
    regionChanges.push(next);
    return true;
  },
  snapshot() { return { ...controllerSnapshot, streamRegionVisible: controllerRegionVisible }; },
  ensureExactProof(epoch, sequence) {
    return controllerSnapshot.proofFresh === true &&
      controllerSnapshot.epoch === epoch && controllerSnapshot.sequence === sequence;
  }
};
let controlCodeDialogScrollLock = null;
const panel = null;
function viewportHeight() { return 100; }
function updateControlCodeSubmitAvailability() {}
function ticketActionV3StreamSnapshot() { return { epoch: 5, sequence: 31 }; }
function streamHasFreshRenderedFrame() { return true; }
function check(value, message) { if (!value) throw new Error(message); }
`+presentation+actionGate+updateDetails+`

updateDetailsReveal();
check(experimentalHDRSurfacePresentationAllowed() === true &&
  experimentalMediaStreamRegionVisible === true,
  'the initially visible ticket did not retain HDR authority');
window.scrollY = 90;
updateDetailsReveal();
check(classes.has('details-visible') && experimentalHDRSurfacePresentationAllowed() === true,
  'scrolling to details revoked the already-presented HDR surface');
check(experimentalMediaStreamRegionVisible === false &&
  document.body.dataset.clientHdrStreamRegion === 'out-of-view' &&
  regionChanges.join(',') === 'false',
  'scrolling away did not record viewport state without a lifecycle restart');
check(experimentalMediaCanvas === originalCanvas &&
  experimentalMediaCanvas.identity === canvasIdentity &&
  experimentalMediaCanvas.hidden === false &&
  !experimentalMediaCanvas.hasAttribute('hidden') &&
  experimentalMediaCanvas.getAttribute('aria-hidden') === 'false' &&
  experimentalMediaCanvas.dataset.clientHdrSurface === 'visible',
  'details scroll did not retain and restore the same owned HDR canvas');
check(experimentalClientHDRController.snapshot().rendererGeneration === 7 &&
  experimentalClientHDRController.snapshot().rendererIdentity === rendererIdentity &&
  experimentalClientHDRController.snapshot().surfaceVisible === true &&
  clientHDRConsequentialControlProofReady() === true &&
  revealAuthoritativeSDRForConsequentialControl() === true &&
  scrollIntoViewCalls === 0,
  'details scroll recreated, hid, pinned, or made the fresh panel controls unusable');
window.scrollY = 0;
updateDetailsReveal();
check(!classes.has('details-visible') && experimentalHDRSurfacePresentationAllowed() === true &&
  experimentalMediaStreamRegionVisible === true &&
  document.body.dataset.clientHdrStreamRegion === 'visible' &&
  regionChanges.join(',') === 'false,true' &&
  experimentalMediaCanvas === originalCanvas &&
  experimentalMediaCanvas.dataset.clientHdrSurface === 'visible' &&
  experimentalMediaCanvas.getAttribute('aria-hidden') === 'false' &&
  experimentalClientHDRController.snapshot().rendererGeneration === 7,
  'scrolling back did not reuse the same HDR surface');

classes.add('control-code-result-visible');
check(experimentalHDRSurfacePresentationAllowed() === false,
  'an ordinary SDR control-code result failed to revoke unrelated HDR');
codeResultArea.dataset.presentation = 'exact-hdr';
check(experimentalHDRSurfacePresentationAllowed() === true,
  'an exact-HDR control-code result revoked its owned HDR canvas');
`)

	if strings.Contains(presentation, "details-visible") ||
		!strings.Contains(presentation, "controlCodeExactHDRResultVisible()") {
		t.Fatal("scroll state is still mixed with control-code HDR authority")
	}
	for _, forbidden := range []string{
		"synchronizeExperimentalHDRSurfaceRegion(",
		"closeExperimentalMedia(",
		"beginExperimentalMediaForegroundRecovery(",
	} {
		if strings.Contains(updateDetails, forbidden) {
			t.Fatalf("scrolling still tears down or recreates HDR: %q", forbidden)
		}
	}
	if !strings.Contains(updateDetails, "noteExperimentalMediaStreamRegionVisibility(") {
		t.Fatal("scrolling no longer records the stream viewport state")
	}
	controllerBody := substringBetween(t, source,
		"function ensureExperimentalClientHDRController() {",
		"  function connectExperimentalClientHDR(options) {")
	for _, needle := range []string{
		"experimentalHDRSurfacePresentationAllowed()",
		"document.body.dataset.experimentalMedia = 'fallback-sdr';",
		"experimentalClientHDRController.setStreamRegionVisible(experimentalMediaStreamRegionVisible);",
	} {
		if !strings.Contains(controllerBody, needle) {
			t.Fatalf("HDR surface controller lost continuity state: missing %q", needle)
		}
	}
}

func TestTicketViewerHDRHoldoverReleaseRequiresLiveTransportAndServerAuthority(t *testing.T) {
	source := ticketAppSource(t)
	release := substringBetween(t, source,
		"function clientHDRHoldoverReleaseAllowed(presentation) {",
		"  function experimentalMediaDocumentHasFocus() {")

	runTicketJavaScript(t, `
const WebSocket = { OPEN: 1, CONNECTING: 0, CLOSED: 3 };
const performance = { now() { return 1000; } };
const document = { visibilityState: 'visible' };
const window = { navigator: { onLine: true } };
let foreground = true;
let idleDisconnected = false;
let streamUnsupported = false;
let experimentalMediaLifecycleArmed = false;
let directSpacetimeAuth = true;
let spacetimeStateFresh = true;
let spacetimeClientStatus = 'live';
let videoWs = { readyState: WebSocket.OPEN };
let status = {
  phoneDesired: true,
  phoneConnected: true,
  phoneStreamState: 'streaming',
  activeVideoClients: 1,
  stale: false
};
let freshness = { liveLabeled: true, actionFresh: true };
let lastRenderedFrameEpoch = 7;
let lastRenderedFrameSequence = 42;
let lastRenderedPresentationOrdinal = 19;
let currentStreamEpoch = 7;
function viewerIsForeground() { return foreground; }
function usesDirectSpacetimeAuth() { return directSpacetimeAuth; }
function freshStreamStatus() { return status; }
function streamStatusStale(value) { return Boolean(value && value.stale); }
function currentRenderedFreshness() { return freshness; }
function check(value, message) { if (!value) throw new Error(message); }
`+release+`

const candidate = { epoch: 7, sequence: 42, presentationOrdinal: 19 };
check(clientHDRHoldoverReleaseAllowed(candidate) === true,
  'a globally current live candidate was not admitted');
experimentalMediaLifecycleArmed = true;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'an armed blur lifecycle released holdover before fresh focus recovery');
experimentalMediaLifecycleArmed = false;
window.navigator.onLine = false;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'an open video socket released a buffered frame while the browser was offline');
window.navigator.onLine = true;
spacetimeClientStatus = 'reconnecting';
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'a live video frame escaped holdover while SpaceTime was reconnecting');
spacetimeClientStatus = 'offline';
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'a live video frame escaped holdover while SpaceTime was offline');
spacetimeClientStatus = 'live';
spacetimeStateFresh = false;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'a buffered frame released holdover before the fresh SpaceTime snapshot');
directSpacetimeAuth = false;
check(clientHDRHoldoverReleaseAllowed(candidate) === true,
  'development mode incorrectly required direct SpaceTime authority');
directSpacetimeAuth = true;
spacetimeStateFresh = true;
videoWs = { readyState: WebSocket.CLOSED };
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'an age-fresh frame escaped holdover while its video socket was closed');
videoWs = { readyState: WebSocket.OPEN };
status.phoneStreamState = 'waiting_keyframe';
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'waiting-keyframe server state released holdover');
status.phoneStreamState = 'streaming';
status = null;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'missing server authority released holdover');
status = { phoneDesired: true, phoneConnected: true, phoneStreamState: 'streaming', activeVideoClients: 1, stale: false };
status.stale = true;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'server-stale status released holdover');
status.stale = false;
status.activeVideoClients = 0;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'zero active viewers released holdover');
status.activeVideoClients = 1;
status.phoneConnected = false;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'a disconnected phone released holdover');
status.phoneConnected = true;
freshness = { liveLabeled: true, actionFresh: false };
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'continuity-only pixels released holdover without action freshness');
freshness = { liveLabeled: true, actionFresh: true };
lastRenderedFrameSequence = 43;
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'a mismatched rendered watermark released holdover');
lastRenderedFrameSequence = 42;
document.visibilityState = 'hidden';
check(clientHDRHoldoverReleaseAllowed(candidate) === false,
  'a hidden document released holdover');
`)

	statusHandler := substringBetween(t, source,
		"onStatus: (status, detail) => {",
		"      });\n      spacetimeClient = client;")
	for _, required := range []string{
		"status === 'connecting'",
		"status === 'reconnecting'",
		"status === 'offline'",
		"markSpacetimeStateUnconfirmed(`spacetime_${status}`);",
		"reconcileClientHDRStreamContinuity(`spacetime_${status}`, 'sdr_stream_unavailable');",
	} {
		if !strings.Contains(statusHandler, required) {
			t.Fatalf("SpaceTime authority transition can leave live-looking HDR: missing %q", required)
		}
	}
	markIndex := strings.Index(statusHandler, "markSpacetimeStateUnconfirmed(`spacetime_${status}`);")
	holdIndex := strings.Index(statusHandler, "reconcileClientHDRStreamContinuity(`spacetime_${status}`, 'sdr_stream_unavailable');")
	publishIndex := strings.Index(statusHandler, "publishSpacetimeClientStatus(status);")
	if markIndex < 0 || holdIndex < markIndex || publishIndex < holdIndex {
		t.Fatal("SpaceTime connecting/reconnecting/offline must revoke proof, hold the picture, then publish status")
	}
}

func TestTicketViewerSpinnerReplacesHDRHoldoverNotice(t *testing.T) {
	template := ticketIndexTemplate(t)
	source := ticketAppSource(t)
	css := ticketAppCSS(t)
	canvasIndex := strings.Index(template, `<canvas id="experimentalMediaCanvas"`)
	spinnerIndex := strings.Index(template, `<img id="streamResumeSpinner" class="stream-resume-spinner"`)
	resultIndex := strings.Index(template, `<div id="controlCodeResultArea"`)
	if canvasIndex < 0 || spinnerIndex <= canvasIndex || resultIndex <= spinnerIndex {
		t.Fatal("the recovery spinner must be a stage-page sibling above HDR and below the result overlay")
	}
	if !strings.Contains(template, "body.control-code-result-visible .stream-resume-spinner") {
		t.Fatal("the recovery spinner must stay hidden over a frozen control-code result")
	}
	if !staticCSSContains(css, ".stream-resume-spinner { z-index: 4 }") {
		t.Fatal("the recovery spinner must remain above the z-index 2 HDR surface")
	}
	for _, removed := range []string{"hdrHoldoverNotice", "hdr-holdover-notice", "Savienojas — gaida svaigu kadru"} {
		if strings.Contains(template, removed) || strings.Contains(source, removed) {
			t.Fatalf("removed fresh-frame notice returned: %q", removed)
		}
	}
}

func TestTicketViewerConsequentialControlsTreatHDRHoldoverAsPassive(t *testing.T) {
	source := ticketAppSource(t)
	gate := substringBetween(t, source,
		"function clientHDRConsequentialControlProofReady() {",
		"  function currentTicketSliderRegion(state = currentState) {")
	request := substringBetween(t, source,
		"async function requestTicketActionV3(target, source, reason, expectedInteractionRevision = '', options = {}) {",
		"  async function registerCurrentTicket(source, options = {}) {")
	actionProofPolicy := substringBetween(t, source,
		"function ticketActionV3RequiresFreshRenderedFrame(target) {",
		"  function currentTicketSliderRegion(state = currentState) {")
	sliderHandlers := substringBetween(t, source,
		"ticketLocalRegisterSlider.addEventListener('pointerdown', (event) => {",
		"  ticketLocalRegisterSlider.addEventListener('blur', () => cancelTicketRegisterSliderSession('slider_blurred'));")

	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
let controllerSnapshot = {
  active: true,
  surfaceVisible: true,
  visualHoldover: false,
  proofFresh: true,
  presentationState: 'visible',
  epoch: 7,
  sequence: 42
};
let exactProof = true;
let exactProofCalls = 0;
const experimentalClientHDRController = {
  snapshot() { return { ...controllerSnapshot }; },
  ensureExactProof() { exactProofCalls += 1; return exactProof; }
};
function ticketActionV3StreamSnapshot() { return { epoch: 7, sequence: 42 }; }
function streamHasFreshRenderedFrame() { return true; }
function check(value, message) { if (!value) throw new Error(message); }
`+gate+`

check(clientHDRConsequentialControlProofReady() === true &&
  revealAuthoritativeSDRForConsequentialControl() === true && exactProofCalls === 1,
  'fresh exact HDR proof did not authorize a consequential control');
controllerSnapshot.visualHoldover = true;
controllerSnapshot.proofFresh = false;
check(clientHDRConsequentialControlProofReady() === false &&
  revealAuthoritativeSDRForConsequentialControl() === false && exactProofCalls === 1,
  'held HDR remained active visual authority for a consequential control');
controllerSnapshot.active = false;
check(clientHDRConsequentialControlProofReady() === true &&
  revealAuthoritativeSDRForConsequentialControl() === true && exactProofCalls === 1,
  'authoritative SDR fallback was incorrectly blocked by inactive HDR');
controllerSnapshot.active = true;
controllerSnapshot.surfaceVisible = false;
check(revealAuthoritativeSDRForConsequentialControl() === true && exactProofCalls === 1,
  'authoritative SDR fallback was incorrectly required to prove hidden HDR');
`)

	runTicketJavaScript(t, `
let revealAllowed = false;
let localRequests = 0;
let mutations = 0;
let renders = 0;
let ticketActionV3LastUserMessage = '';
let ticketActionV3LastUserActionId = '';
let ticketActionV3LastUserAction = null;
let currentState = { ticketAction: null };
let spacetimeStateFresh = false;
const ticketActionV3LocalRequestState = {};
function revealAuthoritativeSDRForConsequentialControl() { return revealAllowed; }
function renderTicketActionV3Controls() { renders += 1; }
function ticketActionV3Busy() { return false; }
function ticketActionV3LocalRequestIsBusy() { return false; }
function ticketActionV3Id() { return 'action-1'; }
function beginTicketActionV3LocalRequest() { localRequests += 1; return true; }
function settleTicketActionV3LocalRequest() {}
function ticketActionV3RequestArgs(value) { return value; }
function runSpacetimeMutation() { mutations += 1; return Promise.resolve(); }
function scheduleTicketActionV3Reconcile() {}
function clientLog() {}
function localizePublicMessage(value) { return value; }
`+actionProofPolicy+request+`

(async () => {
  const staleStateAccepted = await requestTicketActionV3(
    'open_latest_unactivated', 'browser_button', 'test_holdover'
  );
  if (staleStateAccepted !== false || localRequests !== 0 || mutations !== 0 ||
      !ticketActionV3LastUserMessage.includes('SpaceTime')) {
    throw new Error('semantic open escaped the fresh durable-state gate');
  }

  spacetimeStateFresh = true;
  for (const target of ['open_latest_unactivated', 'redetect_latest']) {
    const accepted = await requestTicketActionV3(target, 'browser_button', 'semantic_action');
    if (accepted !== true) throw new Error(target + ' incorrectly required a rendered frame');
  }
  if (localRequests !== 2 || mutations !== 2) {
    throw new Error('semantic actions did not reach the one durable mutation path');
  }

  for (const target of ['open_latest_and_register', 'register_current',
    'show_recent_activated', 'return_to_latest_unactivated']) {
    const accepted = await requestTicketActionV3(target, 'browser_button', 'picture_relative');
    if (accepted !== false) throw new Error(target + ' escaped the exact rendered-frame gate');
  }
  if (localRequests !== 2 || mutations !== 2 ||
      !ticketActionV3LastUserMessage.includes('svaigu tiešraides kadru')) {
    throw new Error('picture-relative holdover rejection changed durable state');
  }

  revealAllowed = true;
  const accepted = await requestTicketActionV3(
    'register_current', 'browser_button', 'fresh_exact_frame'
  );
  if (accepted !== true || localRequests !== 3 || mutations !== 3) {
    throw new Error('fresh exact rendered proof did not admit registration');
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
`)

	runTicketJavaScript(t, `
const handlers = {};
let revealAllowed = false;
let cancelled = 0;
let sessions = 0;
const currentState = {};
const pendingBrowserAction = null;
const ticketLocalRegisterSliderState = { inFlight: false, session: null, ignoreChange: false };
const ticketLocalRegisterSlider = {
  value: '75',
  addEventListener(name, callback) { handlers[name] = callback; },
  getBoundingClientRect() { return { left: 0, width: 100 }; }
};
function revealAuthoritativeSDRForConsequentialControl() { return revealAllowed; }
function cancelTicketRegisterSliderSession() { cancelled += 1; return true; }
function beginTicketLocalRegisterSliderSession() { sessions += 1; return true; }
function currentTicketRegisterSliderPresentationProof() { return null; }
function finishTicketRegisterSliderSession() { return Promise.resolve(false); }
function updateTicketLocalRegisterSliderPointerDirection() { return ''; }
function clientLog() {}
function check(value, message) { if (!value) throw new Error(message); }
`+sliderHandlers+`

for (const [name, event] of [
  ['pointerdown', { isPrimary: true, pointerType: 'touch', pointerId: 1, clientX: 10, clientY: 10,
    preventDefault() { this.prevented = true; } }],
  ['keydown', { key: 'ArrowRight', preventDefault() { this.prevented = true; } }],
  ['change', {}]
]) {
  handlers[name](event);
  if (name !== 'change' && event.prevented !== true) throw new Error(name + ' was not cancelled');
}

check(sessions === 0 && ticketLocalRegisterSlider.value === '0' && cancelled === 3,
  'holdover started a pointer, keyboard, or native-change slider session');
`)

	renderControls := substringBetween(t, source,
		"function renderTicketActionV3Controls(state = currentState) {",
		"  async function requestTicketActionV3(target, source, reason, expectedInteractionRevision = '', options = {}) {")
	for _, needle := range []string{
		"const hdrControlReady = clientHDRConsequentialControlProofReady();",
		"spacetimeStateFresh && !blockingBusy && !controlBusy, openReason",
		"spacetimeStateFresh && presentationReady && !blockingBusy",
		"presentationReady && !blockingBusy && !controlBusy && registerReady",
		"renderTicketRegisterOverlay(state, blockingBusy, controlBusy)",
		"spacetimeStateFresh && switchAvailable && ticketViewSwitchButton.dataset.target && presentationReady",
	} {
		if !strings.Contains(renderControls, needle) {
			t.Fatalf("consequential controls lost the HDR holdover gate: missing %q", needle)
		}
	}
	report := substringBetween(t, source,
		"function reportClientHDRMetric(event, detail) {",
		"  function refreshExperimentalClientCapability() {")
	if !strings.Contains(report, "event === 'presentation_holdover'") ||
		!strings.Contains(report, "event === 'presented'") ||
		!strings.Contains(report, "renderTicketActionV3Controls(currentState);") {
		t.Fatal("control disabled state is not refreshed when HDR enters or leaves holdover")
	}
}

func TestTicketViewerControlCodeEntryPointsArePassiveDuringHDRHoldover(t *testing.T) {
	source := ticketAppSource(t)
	openDialog := substringBetween(t, source,
		"function openControlCodeDialog() {",
		"  function closeControlCodeDialog() {")
	hotspot := substringBetween(t, source,
		"function requestControlCodeFromHotspot(event) {",
		"  async function submitControlCodeRequest() {")
	submit := substringBetween(t, source,
		"async function submitControlCodeRequest() {",
		"  async function closeCurrentControlCode(openNext) {")
	availability := substringBetween(t, source,
		"function updateControlCodeSubmitAvailability() {",
		"  function reconnectVideoForRecovery(reason) {")
	for name, body := range map[string]string{"dialog": openDialog, "hotspot": hotspot} {
		if strings.Contains(body, "revealAuthoritativeSDRForConsequentialControl()") {
			t.Fatalf("passive control-code %s must not reveal or require exact SDR authority", name)
		}
	}
	if !strings.Contains(openDialog, "if (!controlCodeDialogEntryReady()) {") ||
		!strings.Contains(submit, "if (!browserIntentValid() || controlCodeMutationLaneBusy() || !revealAuthoritativeSDRForConsequentialControl()) {") {
		t.Fatal("dialog entry must use healthy continuity while submit retains exact fresh proof")
	}
	for _, required := range []string{
		"const hdrControlReady = clientHDRConsequentialControlProofReady();",
		"codeSubmit.disabled = !codeDialogOpen || busy || limitBlocked || !dialogEntryReady || !digitsValid;",
		"const dialogEntryReady = (streamActionFresh && hdrControlReady) || healthyOneFPSVisualContinuity();",
		"requestCodeButton.disabled = busy || limitBlocked || !dialogEntryReady || codeDialogOpen || !codeResultArea.hidden;",
		"const hotspotUnavailable = busy || limitBlocked || !dialogEntryReady || sliderOwnsHotspot ||",
	} {
		if !strings.Contains(availability, required) {
			t.Fatalf("control-code availability lost passive HDR holdover gating: missing %q", required)
		}
	}

	runTicketJavaScript(t, `
let hdrReady = false;
let codeDialogOpen = false;
let controlCodeSubmitInFlight = false;
const pendingBrowserAction = null;
const codeDigits = { value: '42' };
const codeSubmit = {
  disabled: false,
  textContent: '',
  setAttribute() {},
  removeAttribute() {}
};
const requestCodeButton = { disabled: false };
const controlCodeHotspot = { disabled: false, setAttribute() {} };
const codeResultArea = { hidden: true };
function renderControlCodeFastStateDataset() {}
function controlCodeMutationLaneBusy() { return false; }
function memberLimitBlocked() { return false; }
function streamHasFreshRenderedFrame() { return true; }
function clientHDRConsequentialControlProofReady() { return hdrReady; }
function healthyOneFPSVisualContinuity() { return false; }
function sanitizeControlDigits(value) { return String(value || '').replace(/\D/g, ''); }
function ticketRegisterOverlayOccupiesHotspot() { return false; }
function check(value, message) { if (!value) throw new Error(message); }
`+availability+`

updateControlCodeSubmitAvailability();
check(codeSubmit.disabled && requestCodeButton.disabled && controlCodeHotspot.disabled,
  'held HDR left a control-code surface visibly actionable');
hdrReady = true;
updateControlCodeSubmitAvailability();
check(!requestCodeButton.disabled && !controlCodeHotspot.disabled,
  'authoritative SDR unnecessarily blocked control-code entry');
codeDialogOpen = true;
updateControlCodeSubmitAvailability();
check(!codeSubmit.disabled && controlCodeHotspot.disabled,
  'fresh proof did not enable dialog submit or suppress its background hotspot');
`)
}

func TestTicketViewerControlCodeResultVisibilityUsesExactHDRPresentationMode(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"function setControlCodeResultVisible(visible) {",
		"function clearControlCodeResultCapture() {")
	classify := strings.Index(body, "document.body.classList.toggle('control-code-result-visible', Boolean(visible));")
	reveal := strings.Index(body, "codeResultArea.hidden = !visible;")
	if reveal < 0 || classify < reveal {
		t.Fatal("control-code result must classify its overlay after updating local visibility")
	}
	for _, required := range []string{
		"synchronizeExperimentalHDRSurfaceRegion(",
		"!experimentalHDRSurfacePresentationAllowed()",
		"visible ? 'control_code_result_visible' : 'control_code_result_hidden'",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("result visibility does not derive HDR authority from its presentation mode: missing %q", required)
		}
	}

	runTicketJavaScript(t, `
const classes = new Set();
const document = { body: { classList: {
  contains(value) { return classes.has(value); },
  remove(value) { classes.delete(value); },
  toggle(value, enabled) { if (enabled) classes.add(value); else classes.delete(value); }
} } };
const codeResultArea = { hidden: true, dataset: {} };
const panel = null;
let scrolls = 0;
let updates = 0;
const regionTransitions = [];
const stage = { scrollIntoView() { scrolls += 1; } };
function experimentalHDRSurfacePresentationAllowed() {
  return !classes.has('details-visible') &&
    (!classes.has('control-code-result-visible') || codeResultArea.dataset.presentation === 'exact-hdr');
}
function synchronizeExperimentalHDRSurfaceRegion(blocked, reason, options) {
  regionTransitions.push({ blocked, reason, options: options || {} });
  return true;
}
function updateControlCodeSubmitAvailability() { updates += 1; }
function check(value, message) { if (!value) throw new Error(message); }
`+body+`

setControlCodeResultVisible(false);
check(updates === 1 && !classes.has('control-code-result-visible'),
  'an already-hidden result changed HDR presentation state');
codeResultArea.dataset.presentation = 'exact-hdr';
setControlCodeResultVisible(true);
check(!codeResultArea.hidden && classes.has('control-code-result-visible') && scrolls === 1 &&
  regionTransitions.length === 1 && regionTransitions[0].blocked === false,
  'exact-HDR result did not preserve the matching HDR surface');
setControlCodeResultVisible(false);
check(codeResultArea.hidden && !classes.has('control-code-result-visible') &&
  regionTransitions.length === 2 && regionTransitions[1].blocked === false,
  'result dismissal did not clear its local overlay classification');
delete codeResultArea.dataset.presentation;
setControlCodeResultVisible(true);
check(regionTransitions.length === 3 && regionTransitions[2].blocked === true,
  'ordinary SDR result did not hide an unrelated HDR surface');
`)
}

func TestTicketViewerControlCodeCaptureKeepsHDRAvailableWhilePolling(t *testing.T) {
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
const events = [];
function firstPositiveSafeInteger(...values) {
  for (const value of values) {
    const parsed = Number(value);
    if (Number.isSafeInteger(parsed) && parsed > 0) return parsed;
  }
  return 0;
}
function controlCodeCaptureTrace() { events.push('trace'); }
function setControlCodeResultVisible() { events.push('hide'); }
function keepControlCodeVideoAlive() { events.push('keep'); }
function maybeCaptureControlCodeResultImage() {
  events.push('capture');
  return true;
}
function controlCodeResultDisplayedForRequest() { return false; }
function check(value, message) { if (!value) throw new Error(message); }
`+waitForScreenshot+`
const request = { requestId: 'req-1', resultFrameEpoch: 7, resultMinFrameSequence: 42 };
waitForControlCodeResultScreenshot(request);
check(controlCodeResultCaptureRequestID === 'req-1',
  'control-code result capture did not become authoritative');
check(events.indexOf('keep') >= 0 && events.indexOf('capture') >= 0,
  'control-code polling did not retain its stream keepalive and capture attempt');
waitForControlCodeResultScreenshot(request);
check(!events.some((event) => event.startsWith('region:')),
  'control-code polling retired HDR instead of offering the same SDR proof frames');
`)
	if strings.Contains(waitForScreenshot, "synchronizeExperimentalHDRSurfaceRegion(") ||
		strings.Contains(waitForScreenshot, "control_code_priority") {
		t.Fatal("control-code polling still owns HDR visibility instead of only capture cadence")
	}
}

func TestTicketViewerControlCodeHDRFreezeTargetLifecycle(t *testing.T) {
	source := ticketAppSource(t)
	freezeHelpers := substringBetween(t, source,
		"function controlCodeHDRFreezeTargetActive() {",
		"  function scheduleDecodedFrame(frame, source) {")
	resultFallbackHelpers := substringBetween(t, source,
		"function forceControlCodeResultSDRFallback(reason) {",
		"  function clearControlCodeResultCapture() {")

	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS = 2000;
const originalDateNow = Date.now;
let wallNow = 1000;
Date.now = () => wallNow;
let controlCodeHDRFreezeTarget = null;
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRecoveryPending = false;
const experimentalMediaState = { enabled: true, engine: CLIENT_HDR_ENGINE };
const codeResultArea = { hidden: false, dataset: { presentation: 'exact-hdr' }, style: {} };
const codeResultImage = { hidden: true };
let exactProofAllowed = false;
let controllerSnapshot = {
  active: true,
  surfaceVisible: false,
  presentationState: 'acquiring',
  epoch: 0,
  sequence: 0
};
const staleReasons = [];
const regionTransitions = [];
const timers = [];
const experimentalClientHDRController = {
  snapshot() { return { ...controllerSnapshot }; },
  ensureExactProof(epoch, sequence) {
    return exactProofAllowed && Number(epoch) > 0 && Number(sequence) > 0;
  },
  markSDRStale(reason) { staleReasons.push(String(reason)); }
};
function experimentalHDRSurfacePresentationAllowed() { return true; }
function controlCodeExactHDRResultVisible() {
  return codeResultArea.dataset.presentation === 'exact-hdr' && !codeResultArea.hidden;
}
function showExperimentalClientHDRSurface() {
  throw new Error('controller fallback path was unexpectedly unavailable');
}
function synchronizeExperimentalHDRSurfaceRegion(blocked, reason) {
  regionTransitions.push({ blocked: Boolean(blocked), reason: String(reason || '') });
  return true;
}
function setTimeout(callback, millis) {
  const timer = { callback, millis, cleared: false };
  timers.push(timer);
  return timer;
}
function clearTimeout(timer) {
  if (timer) timer.cleared = true;
}
function check(value, message) { if (!value) throw new Error(message); }
`+freezeHelpers+resultFallbackHelpers+`

(async () => {
  const exactProof = {
    requestId: 'request-exact',
    candidateFrameEpoch: 7,
    candidateFrameSequence: 42
  };
  check(latchControlCodeHDRFreezeTarget(exactProof) === true &&
    controlCodeHDRFreezeTargetActive() &&
    controlCodeHDRFreezeTargetMatches('request-exact', 7, 42),
    'valid request/epoch/sequence did not latch the HDR freeze target');
  check(await waitForControlCodeExactHDRPresentation('wrong-request', exactProof) === false,
    'a different request id joined the exact-HDR wait');
  check(await waitForControlCodeExactHDRPresentation('request-exact', {
    ...exactProof,
    candidateFrameSequence: 43
  }) === false,
    'a different frame sequence joined the exact-HDR wait');

  const exactTarget = controlCodeHDRFreezeTarget;
  const exactWait = waitForControlCodeExactHDRPresentation('request-exact', exactProof);
  check(exactTarget.waiters.size === 1 && timers.at(-1).millis === 2000,
    'matching exact-HDR wait did not receive one bounded waiter');
  exactProofAllowed = true;
  check(observeControlCodeHDRPresentationMetric('presented', {
    epoch: 7,
    sequence: 41,
    surfaceVisible: true,
    presentationState: 'visible'
  }) === false && !exactTarget.exactPresented && exactTarget.waiters.size === 1,
    'a mismatched sequence promoted or released the exact-HDR target');
  check(observeControlCodeHDRPresentationMetric('presented', {
    epoch: 7,
    sequence: 42,
    surfaceVisible: true,
    presentationState: 'visible'
  }) === true && await exactWait === true && exactTarget.exactPresented &&
    exactTarget.waiters.size === 0,
    'matching request/epoch/sequence did not complete exact-HDR presentation');
  check(clearControlCodeHDRFreezeTarget('exact_result_finished') === true &&
    !controlCodeHDRFreezeTargetActive(),
    'completed exact-HDR target did not clear');

  exactProofAllowed = false;
  codeResultArea.hidden = false;
  codeResultArea.dataset.presentation = 'exact-hdr';
  codeResultImage.hidden = true;
  const timeoutProof = {
    requestId: 'request-timeout',
    candidateFrameEpoch: 8,
    candidateFrameSequence: 50
  };
  check(latchControlCodeHDRFreezeTarget(timeoutProof) === true,
    'timeout target did not latch');
  const timeoutTarget = controlCodeHDRFreezeTarget;
  const timeoutWait = waitForControlCodeExactHDRPresentation('request-timeout', timeoutProof);
  const timeoutTimer = timers.at(-1);
  check(timeoutTarget.waiters.size === 1 && timeoutTimer.millis === 2000,
    'timeout target did not retain one bounded waiter');
  timeoutTimer.callback();
  check(await timeoutWait === false && controlCodeHDRFreezeTarget === null &&
    timeoutTarget.waiters.size === 0,
    'timeout did not resolve to SDR and clear the target/waiter');
  check(codeResultArea.dataset.presentation === undefined && !codeResultImage.hidden &&
    staleReasons.at(-1) === 'control_code_result_exact_hdr_timeout' &&
    regionTransitions.at(-1).blocked === true,
    'timeout did not expose the preloaded SDR fallback');

  codeResultArea.hidden = false;
  codeResultArea.dataset.presentation = 'exact-hdr';
  codeResultImage.hidden = true;
  const surfaceLossProof = {
    requestId: 'request-surface-loss',
    candidateFrameEpoch: 9,
    candidateFrameSequence: 60
  };
  check(latchControlCodeHDRFreezeTarget(surfaceLossProof) === true,
    'surface-loss target did not latch');
  const surfaceLossTarget = controlCodeHDRFreezeTarget;
  const surfaceLossWait = waitForControlCodeExactHDRPresentation('request-surface-loss', surfaceLossProof);
  handleControlCodeHDRSurfaceChange(false, 'device_lost');
  check(await surfaceLossWait === false && controlCodeHDRFreezeTarget === null &&
    surfaceLossTarget.waiters.size === 0 &&
    codeResultArea.dataset.presentation === undefined && !codeResultImage.hidden &&
    regionTransitions.at(-1).reason === 'control_code_exact_hdr_lost',
    'surface loss did not clear the freeze and reveal SDR');

  const clearProof = {
    requestId: 'request-clear',
    candidateFrameEpoch: 10,
    candidateFrameSequence: 70
  };
  check(latchControlCodeHDRFreezeTarget(clearProof) === true,
    'explicit-clear target did not latch');
  const clearTarget = controlCodeHDRFreezeTarget;
  const clearWait = waitForControlCodeExactHDRPresentation('request-clear', clearProof);
  check(clearControlCodeHDRFreezeTarget('browser_closed') === true &&
    await clearWait === false && controlCodeHDRFreezeTarget === null &&
    clearTarget.waiters.size === 0,
    'explicit close cleanup left the target or waiter behind');
  Date.now = originalDateNow;
})().catch((error) => {
  Date.now = originalDateNow;
  console.error(error);
  process.exitCode = 1;
});
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

func TestTicketViewerHDRSurfaceSupportsExactControlCodeResultOverlay(t *testing.T) {
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
	if strings.Contains(template, "body.details-visible #experimentalMediaCanvas") {
		t.Fatal("scrolling to details must not hide the already-presented HDR canvas")
	}
	if !strings.Contains(template, "body.details-visible #ticketRegisterOverlay") ||
		!strings.Contains(template, "body.control-code-result-visible #ticketRegisterOverlay") {
		t.Fatalf("details and result overlays must still hide registration controls")
	}
	if strings.Contains(template, "body.control-code-result-visible #experimentalMediaCanvas") {
		t.Fatal("a visible control-code result still hides the exact frozen HDR surface unconditionally")
	}
	exactResult := substringBetween(t, template,
		`#controlCodeResultArea[data-presentation="exact-hdr"] {`,
		`#experimentalMediaCanvas[hidden] {`)
	for _, required := range []string{
		"background: transparent !important;",
		`#controlCodeResultArea[data-presentation="exact-hdr"] .control-code-image`,
		"visibility: hidden;",
	} {
		if !strings.Contains(exactResult, required) {
			t.Fatalf("exact-HDR result overlay is missing %q", required)
		}
	}
	if strings.Contains(template, "controlCodeResultTimer") || strings.Contains(ticketAppSource(t), "codeResultTimer") {
		t.Fatal("the frozen control-code result must not expose a countdown timer")
	}
	canvasIndex := strings.Index(template, `<canvas id="experimentalMediaCanvas"`)
	resultIndex := strings.Index(template, `<div id="controlCodeResultArea"`)
	registerIndex := strings.Index(template, `<label id="ticketRegisterOverlay"`)
	if canvasIndex < 0 || resultIndex < 0 || registerIndex < 0 ||
		!(canvasIndex < resultIndex && resultIndex < registerIndex) {
		t.Fatalf("exact result controls must be a stage-page sibling above HDR and below registration controls")
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

func TestTicketViewerHDRDescriptionIsStaticAndSimple(t *testing.T) {
	source := ticketAppSource(t)
	if strings.Count(source, `HDR padara biļetes attēlu spilgtāku šajā ekrānā.`) != 1 {
		t.Fatal("HDR must have one plain description")
	}
	for _, obsolete := range []string{"setExperimentalMediaStatus", "experimentalMediaState.engineStatus", "experimentalMediaState.preferenceStatus", "experimentalMediaState.boostStatus"} {
		if strings.Contains(source, obsolete) {
			t.Fatalf("HDR still exposes changing diagnostic text: %s", obsolete)
		}
	}
}
