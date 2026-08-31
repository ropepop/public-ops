package web

import (
	"strings"
	"testing"
)

func TestTicketViewerHDRForegroundCoordinatorFencesRestoresAndWaitsForFreshSDR(t *testing.T) {
	source := ticketAppSource(t)
	coordinator := substringBetween(t, source,
		"function reportExperimentalMediaForegroundRecovery(attempt, phase, reason) {",
		"  function scheduleExperimentalMediaCapabilityRetry(reason, attempt, forceCanvasReset) {")

	runTicketJavaScript(t, `
const CLIENT_HDR_ENGINE = 'client_webgpu_v2';
const CLIENT_HDR_PIPELINE = 'webgpu-mainthread-edr-v2';
const assetVersion = 'asset-current';
const pageVersion = 'ticket-remote-current';
const document = {
  visibilityState: 'visible',
  body: { classList: { contains() { return false; } } },
  hasFocus() { return true; }
};
let wallNow = 100000;
const originalDateNow = Date.now;
Date.now = () => wallNow;
const timers = [];
function setTimeout(callback, millis) {
  const timer = { callback, millis, cancelled: false };
  timers.push(timer);
  return timer;
}
function clearTimeout(timer) { if (timer) timer.cancelled = true; }
function flush() { return new Promise((resolve) => setImmediate(resolve)); }
let experimentalMediaForegroundRecoverySequence = 0;
let experimentalMediaForegroundRecovery = null;
let experimentalMediaForegroundRecoveryTimer = null;
let experimentalMediaForegroundRecoveryDeadlineTimer = null;
let experimentalMediaForegroundRecoveredGeneration = -1;
let experimentalMediaForegroundReturnConfirmationTimer = null;
let experimentalMediaForegroundReturnConfirmationSequence = 0;
let experimentalMediaLifecycleLastResumeWallAt = 0;
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRegionGeneration = 0;
let experimentalMediaPresentationRecoveryPending = false;
let experimentalMediaPresentationRecoveryReason = '';
let experimentalMediaLifecycleGeneration = 4;
let experimentalMediaLifecycleArmed = false;
let experimentalMediaForegroundPulseWallAt = wallNow;
let experimentalMediaForegroundSuspensionGap = false;
let experimentalMediaCanvasGeneration = 9;
let experimentalMediaCanvasResetGeneration = 4;
let experimentalMediaCapabilityReady = true;
let experimentalClientCapabilityAllowed = true;
let experimentalClientHDRFailed = false;
let experimentalMediaResumeRetryArmed = false;
let experimentalMediaRendererRetryTimer = null;
let experimentalMediaCapabilityRetryTimer = null;
let experimentalMediaStartPending = null;
let hasRenderedFrame = true;
let lastRenderedPresentationOrdinal = 10;
let authoritativeSDRRenderSerial = 10;
let currentStreamEpoch = 7;
let lastRenderedFrameEpoch = 7;
let lastRenderedFrameSequence = 30;
const experimentalMediaForegroundRecoveryWindowMillis = 12000;
const experimentalMediaForegroundRecoveryRetryDelays = Object.freeze([0, 250, 750, 1500, 3000]);
const experimentalMediaForegroundSuspensionGapMillis = 2500;
const experimentalMediaForegroundReturnConfirmationMillis = 500;
let experimentalMediaForegroundCanvasStabilityMillis = 1000;
const experimentalMediaCapabilityFetchTimeoutMillis = 3000;
const experimentalMediaPreferenceController = { enabled: true };
const experimentalMediaState = { enabled: true };
let controllerActive = true;
let controllerReady = true;
let closes = 0;
let starts = 0;
let versionChecks = 0;
let versionResult = true;
let fetchDeferred = null;
let capabilityDeferred = null;
let capabilityApplies = 0;
let localCapabilitySupported = true;
let documentFocused = true;
const metrics = [];
let experimentalClientHDRController = {
  snapshot() { return { active: controllerActive, ready: controllerReady, surfaceVisible: true }; }
};
function clientHDRMeasurement(event, _a, _b, detail) { metrics.push({ event, detail }); }
function streamHasFreshRenderedFrame() { return true; }
function experimentalHDRSurfacePresentationAllowed() { return true; }
function experimentalMediaDocumentHasFocus() { return documentFocused; }
function controlCodePresentationPriorityActive() { return false; }
function refreshExperimentalClientCapability() {
  return { supported: localCapabilitySupported, videoFrame: true, mainThreadCanvas: true, webgpu: true,
    dynamicRangeLimit: true, highDynamicRange: localCapabilitySupported };
}
async function discoverExperimentalMediaCapability() { experimentalMediaCapabilityReady = true; }
async function fetchExperimentalMediaCapability() {
  if (capabilityDeferred) return capabilityDeferred.promise;
  return { response: { ok: true }, payload: { allowed: true } };
}
function applyExperimentalMediaCapabilityPayload(response, payload) {
  capabilityApplies += 1;
  experimentalMediaCapabilityReady = Boolean(response && response.ok && payload && payload.allowed);
  experimentalClientCapabilityAllowed = experimentalMediaCapabilityReady;
  return experimentalMediaCapabilityReady;
}
function checkServerVersion() { versionChecks += 1; return versionResult; }
async function fetch() {
  if (fetchDeferred) return fetchDeferred.promise;
  return { ok: true, async json() { return { serverVersion: pageVersion, assetVersion }; } };
}
function closeExperimentalMedia() { closes += 1; controllerActive = false; }
function cancelExperimentalMediaStart() { experimentalMediaStartPending = null; }
function clearExperimentalMediaDynamicRangeRecovery() {}
function armExperimentalMediaDynamicRangeRecovery() { return true; }
function armExperimentalMediaLifecycleResume() {
  if (!experimentalMediaLifecycleArmed) {
    experimentalMediaLifecycleGeneration += 1;
    experimentalMediaLifecycleArmed = true;
  }
  invalidateExperimentalMediaForegroundRecovery('lifecycle_backgrounded');
}
function resumeExperimentalMediaForLifecycle(reason) {
  experimentalMediaLifecycleArmed = false;
  return beginExperimentalMediaForegroundRecovery(reason, { forceCanvasReset: true });
}
function scheduleExperimentalMediaStart(reason, options) {
  starts += 1;
  experimentalMediaCanvasResetGeneration = experimentalMediaLifecycleGeneration;
  controllerActive = true;
  controllerReady = false;
  return Boolean(reason && options && options.forceCanvasReset);
}
function deferred() {
  let resolve;
  const promise = new Promise((done) => { resolve = done; });
  return { promise, resolve };
}
function check(value, message) { if (!value) throw new Error(message); }
`+coordinator+`

(async () => {
  check(beginExperimentalMediaForegroundRecovery('pageshow_persisted') === true,
    'persisted restore did not create a foreground attempt');
  const first = experimentalMediaForegroundRecovery;
  check(first && closes === 1 && first.baselineSDRRenderSerial === 10,
    'foreground attempt did not retire the old active surface and capture its SDR baseline');
  const versionMetric = metrics.find((metric) => metric.detail &&
    metric.detail.attemptId === first.id && metric.detail.recoveryPhase === 'version_check');
  check(versionMetric && versionMetric.detail.triggerSet === 'pageshow_persisted' &&
    versionMetric.detail.versionOutcome === 'pending' && versionMetric.detail.capabilityOutcome === 'pending',
    'foreground attempt did not emit structured version/capability facts');
  clearExperimentalMediaForegroundRecoveryTimer();
  await reconcileExperimentalMediaForegroundRecovery(first);
  check(versionChecks === 1 && starts === 0 && first.phase === 'fresh_sdr_wait',
    'foreground recovery started HDR from the retained pre-return SDR frame: ' + JSON.stringify({
      versionChecks, starts, phase: first.phase, versionOutcome: first.versionOutcome,
      capabilityOutcome: first.capabilityOutcome
    }));

  const firstDeadlineWallAt = first.deadlineWallAt;
  wallNow += 250;
  check(beginExperimentalMediaForegroundRecovery('focus') === true &&
    experimentalMediaForegroundRecovery === first,
    'clustered foreground signals created a duplicate attempt');
  check(first.canvasNotBeforeWallAt === wallNow + experimentalMediaForegroundCanvasStabilityMillis &&
    first.deadlineWallAt === firstDeadlineWallAt,
    'clustered focus did not extend only the stability gate inside the original outer deadline');
  clearExperimentalMediaForegroundRecoveryTimer();
  authoritativeSDRRenderSerial = 11;
  lastRenderedFrameEpoch = 6;
  await reconcileExperimentalMediaForegroundRecovery(first);
  check(starts === 0 && first.phase === 'fresh_sdr_wait',
    'an SDR draw from an older stream epoch incorrectly admitted HDR initialization');
  clearExperimentalMediaForegroundRecoveryTimer();
  authoritativeSDRRenderSerial = 12;
  lastRenderedFrameEpoch = currentStreamEpoch;
  await reconcileExperimentalMediaForegroundRecovery(first);
  check(starts === 0 && !first.canvasStarted && first.phase === 'capability_wait' &&
    first.reportedReason === 'foreground_stability_wait',
    'the first fresh current-epoch SDR frame bypassed the stable-foreground gate');
  clearExperimentalMediaForegroundRecoveryTimer();
  wallNow = first.canvasNotBeforeWallAt;
  await reconcileExperimentalMediaForegroundRecovery(first);
  check(starts === 1 && first.canvasStarted && experimentalMediaCanvasResetGeneration === 4,
    'the stable foreground did not start exactly one fresh HDR canvas');
  experimentalMediaForegroundCanvasStabilityMillis = 0;
  clearExperimentalMediaForegroundRecoveryTimer();
  check(beginExperimentalMediaForegroundRecovery('visibility_resume') === true && starts === 1,
    'a clustered visible event restarted an in-progress renderer');
  check(first.triggers.join(',') === 'pageshow_persisted,focus,visibility_resume',
    'clustered return triggers were not retained on the owning attempt');
  check(completeExperimentalMediaForegroundRecovery('first_presented') === true &&
    experimentalMediaForegroundRecovery === null &&
    experimentalMediaForegroundRecoveredGeneration === 4,
    'matching first presentation did not complete the foreground attempt');
  const activeMetric = metrics.findLast((metric) => metric.detail &&
    metric.detail.attemptId === first.id && metric.detail.recoveryPhase === 'active');
  check(activeMetric && activeMetric.detail.triggerSet === 'pageshow_persisted,focus,visibility_resume' &&
    activeMetric.detail.versionOutcome === 'match' && activeMetric.detail.capabilityOutcome === 'ready',
    'first presentation did not close the matching structured foreground attempt');

  controllerActive = false;
  controllerReady = false;
  authoritativeSDRRenderSerial = 20;
  fetchDeferred = deferred();
  beginExperimentalMediaForegroundRecovery('old_attempt');
  const stale = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  const staleReconcile = reconcileExperimentalMediaForegroundRecovery(stale);
  await flush();
  invalidateExperimentalMediaForegroundRecovery('new_foreground_attempt');
  fetchDeferred.resolve({ ok: true, async json() { return { assetVersion: 'stale' }; } });
  await staleReconcile;
  check(stale.cancelled && experimentalMediaForegroundRecovery === null && starts === 1,
    'a late version response resurrected a superseded foreground attempt');

  fetchDeferred = null;
  experimentalMediaCapabilityReady = false;
  capabilityDeferred = deferred();
  const appliesBeforeStaleCapability = capabilityApplies;
  beginExperimentalMediaForegroundRecovery('stale_capability_attempt');
  const staleCapability = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  const staleCapabilityReconcile = reconcileExperimentalMediaForegroundRecovery(staleCapability);
  await flush();
  invalidateExperimentalMediaForegroundRecovery('newer_than_capability');
  capabilityDeferred.resolve({ response: { ok: true }, payload: { allowed: true } });
  await staleCapabilityReconcile;
  check(staleCapability.cancelled && capabilityApplies === appliesBeforeStaleCapability &&
    experimentalMediaForegroundRecovery === null && starts === 1,
    'a late capability response mutated state after its foreground attempt was fenced');
  capabilityDeferred = null;
  experimentalMediaCapabilityReady = true;

  fetchDeferred = deferred();
  wallNow = 150000;
  beginExperimentalMediaForegroundRecovery('network_online');
  const deadSocket = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  const deadSocketReconcile = reconcileExperimentalMediaForegroundRecovery(deadSocket);
  await flush();
  const outerDeadline = timers.findLast((timer) =>
    !timer.cancelled && timer.millis === experimentalMediaForegroundRecoveryWindowMillis);
  check(outerDeadline, 'dead livez request did not retain an independent 12-second attempt deadline');
  wallNow = deadSocket.deadlineWallAt;
  outerDeadline.callback();
  check(deadSocket.cancelled && experimentalMediaForegroundRecovery === null,
    'a never-resolving livez request stranded recovery past its outer deadline');
  const deadSocketSafeMetric = metrics.findLast((metric) => metric.detail &&
    metric.detail.attemptId === deadSocket.id && metric.detail.recoveryPhase === 'safe_sdr');
  check(deadSocketSafeMetric && deadSocketSafeMetric.detail.reason === 'foreground_deadline_exhausted',
    'dead livez expiry did not record terminal authoritative SDR');
  fetchDeferred.resolve({ ok: true, async json() { return { assetVersion }; } });
  await deadSocketReconcile;
  check(experimentalMediaForegroundRecovery === null,
    'late livez completion resurrected an attempt after its outer deadline');

  fetchDeferred = null;
  wallNow = 175000;
  beginExperimentalMediaForegroundRecovery('focus');
  const wakeLatePresentation = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  wallNow = wakeLatePresentation.deadlineWallAt;
  check(completeExperimentalMediaForegroundRecovery('first_presented') === false &&
    wakeLatePresentation.cancelled && experimentalMediaForegroundRecovery === null,
    'a wake-late first presentation cancelled the suspended outer deadline and claimed success');
  check(!metrics.some((metric) => metric.detail &&
    metric.detail.attemptId === wakeLatePresentation.id && metric.detail.recoveryPhase === 'active'),
    'wake-late first presentation emitted an active recovery milestone');

  for (const phase of [
    'version_check', 'capability_wait', 'fresh_sdr_wait', 'initializing', 'settling', 'active'
  ]) {
    wallNow += 100;
    experimentalMediaLifecycleArmed = false;
    beginExperimentalMediaForegroundRecovery('pageshow');
    const backgrounded = experimentalMediaForegroundRecovery;
    backgrounded.phase = phase;
    const ownedTimers = timers.filter((timer) => !timer.cancelled);
    armExperimentalMediaLifecycleResume();
    check(backgrounded.cancelled && experimentalMediaForegroundRecovery === null,
      'backgrounding did not fence phase ' + phase);
    for (const timer of ownedTimers) timer.callback();
    check(experimentalMediaForegroundRecovery === null,
      'late timer callback resurrected backgrounded phase ' + phase);
  }

  experimentalMediaLifecycleArmed = false;
  fetchDeferred = deferred();
  wallNow = 200000;
  experimentalMediaForegroundPulseWallAt = wallNow;
  const suspensionGenerationBaseline = experimentalMediaLifecycleGeneration;
  beginExperimentalMediaForegroundRecovery('pre_suspension_attempt');
  const preSuspension = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  const suspendedReconcile = reconcileExperimentalMediaForegroundRecovery(preSuspension);
  await flush();
  wallNow += 30000;
  noteExperimentalMediaForegroundPulse();
  const postSuspension = experimentalMediaForegroundRecovery;
  check(preSuspension.cancelled && postSuspension && postSuspension.id !== preSuspension.id &&
    experimentalMediaLifecycleGeneration === suspensionGenerationBaseline + 1 &&
    postSuspension.deadlineWallAt === wallNow + experimentalMediaForegroundRecoveryWindowMillis,
    'suspension pulse did not fence the pre-gap attempt and create a fresh bounded lifecycle: ' + JSON.stringify({
      preCancelled: preSuspension.cancelled,
      preID: preSuspension.id,
      postID: postSuspension && postSuspension.id,
      lifecycleGeneration: experimentalMediaLifecycleGeneration,
      suspensionGenerationBaseline,
      deadlineWallAt: postSuspension && postSuspension.deadlineWallAt,
      expectedDeadlineWallAt: wallNow + experimentalMediaForegroundRecoveryWindowMillis
    }));
  fetchDeferred.resolve({ ok: true, async json() { return { assetVersion: 'late-pre-gap' }; } });
  await suspendedReconcile;
  check(experimentalMediaForegroundRecovery === postSuspension,
    'late pre-suspension version response regained foreground ownership');
  invalidateExperimentalMediaForegroundRecovery('test_next_case');

  fetchDeferred = null;
  localCapabilitySupported = false;
  versionResult = true;
  authoritativeSDRRenderSerial = 30;
  beginExperimentalMediaForegroundRecovery('transient_capability');
  const transient = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  await reconcileExperimentalMediaForegroundRecovery(transient);
  check(foregroundRecoveryCurrent(transient) && starts === 1 &&
    transient.phase === 'capability_wait',
    'transient local HDR capability failed closed instead of retaining the bounded attempt');
  clearExperimentalMediaForegroundRecoveryTimer();
  localCapabilitySupported = true;
  authoritativeSDRRenderSerial = 31;
  await reconcileExperimentalMediaForegroundRecovery(transient);
  check(starts === 2 && transient.canvasStarted,
    'bounded coordinator retry did not recover capability without a media-query event');
  clearExperimentalMediaForegroundRecoveryTimer();
  completeExperimentalMediaForegroundRecovery('first_presented');

  versionResult = false;
  const versionChecksBeforeMismatch = versionChecks;
  beginExperimentalMediaForegroundRecovery('old_asset');
  const mismatch = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  await reconcileExperimentalMediaForegroundRecovery(mismatch);
  await reconcileExperimentalMediaForegroundRecovery(mismatch);
  check(mismatch.reloadRequested && mismatch.cancelled &&
    experimentalMediaForegroundRecovery === null && starts === 2 &&
    versionChecks === versionChecksBeforeMismatch + 1,
    'asset mismatch did not cancel recovery before an old renderer could start');

  versionResult = true;
  documentFocused = false;
  wallNow += 100;
  authoritativeSDRRenderSerial = 40;
  beginExperimentalMediaForegroundRecovery('stale_document_focus', { forceCanvasReset: true });
  const staleFocus = experimentalMediaForegroundRecovery;
  clearExperimentalMediaForegroundRecoveryTimer();
  authoritativeSDRRenderSerial = 41;
  await reconcileExperimentalMediaForegroundRecovery(staleFocus);
  check(foregroundRecoveryCurrent(staleFocus) && !staleFocus.canvasStarted && starts === 2 &&
    staleFocus.phase === 'capability_wait' && staleFocus.reportedReason === 'foreground_stability_wait',
    'stale document focus admitted a canvas without paint-scoped foreground evidence');
  clearExperimentalMediaForegroundRecoveryTimer();
  const staleFocusDeadline = staleFocus.deadlineWallAt;
  check(beginExperimentalMediaForegroundRecovery(
    'explicit_region_unblock',
    { forceCanvasReset: true, foregroundConfirmed: true }
  ) === true && experimentalMediaForegroundRecovery === staleFocus &&
    staleFocus.foregroundPaintConfirmed === true && staleFocus.deadlineWallAt === staleFocusDeadline,
    'explicit stream-region return did not promote the owning attempt without extending its deadline');
  clearExperimentalMediaForegroundRecoveryTimer();
  await reconcileExperimentalMediaForegroundRecovery(staleFocus);
  check(starts === 3 && staleFocus.canvasStarted,
    'paint-scoped foreground evidence did not bypass stale iOS document focus');
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
}).finally(() => { Date.now = originalDateNow; });
`)
}

func TestTicketViewerHDRLifecycleHandlersClassifyPersistedAndClusteredReturns(t *testing.T) {
	source := ticketAppSource(t)
	handlers := substringBetween(t, source,
		"document.addEventListener('visibilitychange', () => {",
		"  window.addEventListener('load', () => keepFirstScreenPinned(true));")

	runTicketJavaScript(t, `
const handlers = {};
const order = [];
const document = {
  visibilityState: 'visible',
  wasDiscarded: false,
  addEventListener(name, callback) { handlers[name] = callback; }
};
const window = { addEventListener(name, callback) { handlers[name] = callback; } };
const performance = { now() { return 1000; } };
let experimentalMediaLifecycleArmed = false;
let experimentalMediaLifecycleResumeAttemptID = 0;
let experimentalMediaForegroundRecovery = null;
let foregroundAttemptSequence = 0;
let hiddenDecoderTransientLogged = false;
let hiddenStreamFocusTimer = null;
let lastHiddenAt = 0;
let lastHiddenWallAt = 0;
let hasRenderedFrame = true;
let idleDisconnected = false;
let resumedFromIdle = false;
let experimentalMediaState = { enabled: true };
let experimentalMediaPreferenceController = { enabled: true };
let spacetimeClient = null;
const ticketCurrentProofVisualState = { resumePending: false };
function scheduleStreamFeedback() {}
function foregroundRecoveryCurrent(attempt) {
  return Boolean(attempt && experimentalMediaForegroundRecovery === attempt && !attempt.cancelled);
}
function resumeExperimentalMediaForLifecycle(reason) {
  const resumesArmedLifecycle = experimentalMediaLifecycleArmed;
  experimentalMediaLifecycleArmed = false;
  order.push('resume:' + reason);
  if (!foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) {
    experimentalMediaForegroundRecovery = { id: ++foregroundAttemptSequence, cancelled: false };
  }
  if (resumesArmedLifecycle) {
    experimentalMediaLifecycleResumeAttemptID = experimentalMediaForegroundRecovery.id;
  }
  return true;
}
function refreshMemberLimitProjection() {}
function noteViewerActivity() { return resumedFromIdle; }
function clearTimeout() {}
function recoverAfterVisibilityResume(reason) { order.push('stream:' + reason); }
function armExperimentalMediaLifecycleResume() {
  order.push('arm');
  experimentalMediaLifecycleArmed = true;
  experimentalMediaLifecycleResumeAttemptID = 0;
  if (experimentalMediaForegroundRecovery) experimentalMediaForegroundRecovery.cancelled = true;
  experimentalMediaForegroundRecovery = null;
}
function closeExperimentalMedia() { order.push('close'); }
function pauseActivationResumeLifecycle() { return {}; }
function logResumeCheckpoint() {}
function clearActivationReconnectBurst() {}
function pauseHiddenStreamAfterGrace() {}
function streamHasFreshRenderedFrame() { return true; }
function keepFirstScreenPinned() {}
function chaseLiveStream() {}
function followActivationResumeLifecycle() {}
function recoverExperimentalMediaForFocusOnlyLifecycle() { order.push('focus-infer'); return false; }
function noteExperimentalMediaForegroundPulse() { order.push('focus-pulse'); return false; }
function scheduleExperimentalMediaForegroundReturnConfirmation(reason) {
  order.push('confirm:' + reason);
  return true;
}
function publishCurrentStreamFocus() {}
function closeEarlyVideo() {}
function preserveCurrentFrame() {}
function closeDirectVideo() {}
function check(value, message) { if (!value) throw new Error(message); }
`+handlers+`

const firstAttemptBaseline = foregroundAttemptSequence;
handlers.pageshow({ persisted: true, isTrusted: true });
check(order[0] === 'arm' && order[1] === 'resume:pageshow_persisted',
  'persisted pageshow without a prior hide did not establish a fresh lifecycle before resume');
check(order.filter((item) => item === 'arm').length === 1,
  'persisted pageshow armed more than one lifecycle');
check(foregroundAttemptSequence === firstAttemptBaseline + 1,
  'persisted pageshow created more than one foreground attempt');

order.length = 0;
experimentalMediaLifecycleArmed = false;
experimentalMediaLifecycleResumeAttemptID = 0;
const oldOrdinaryPageAttempt = { id: ++foregroundAttemptSequence, cancelled: false };
experimentalMediaForegroundRecovery = oldOrdinaryPageAttempt;
const ordinaryPageAttemptBaseline = foregroundAttemptSequence;
handlers.pageshow({ persisted: false, isTrusted: true });
check(oldOrdinaryPageAttempt.cancelled && order[0] === 'arm' && order[1] === 'resume:pageshow' &&
  foregroundAttemptSequence === ordinaryPageAttemptBaseline + 1,
  'ordinary pageshow reused an old active HDR surface instead of creating a fresh return attempt');

order.length = 0;
experimentalMediaLifecycleArmed = false;
experimentalMediaForegroundRecovery = { id: ++foregroundAttemptSequence, cancelled: false };
experimentalMediaLifecycleResumeAttemptID = experimentalMediaForegroundRecovery.id;
const clusteredOrdinaryPageAttemptBaseline = foregroundAttemptSequence;
handlers.pageshow({ persisted: false, isTrusted: true });
check(order.filter((item) => item === 'arm').length === 0 &&
  order.includes('resume:pageshow') &&
  foregroundAttemptSequence === clusteredOrdinaryPageAttemptBaseline,
  'ordinary pageshow replaced the fresh attempt already owned by its return cluster');

order.length = 0;
experimentalMediaLifecycleArmed = false;
experimentalMediaLifecycleResumeAttemptID = 0;
experimentalMediaForegroundRecovery = null;
document.visibilityState = 'hidden';
handlers.visibilitychange();
const clusteredAttemptBaseline = foregroundAttemptSequence;
document.visibilityState = 'visible';
handlers.visibilitychange();
handlers.pageshow({ persisted: true, isTrusted: true });
handlers.focus();
check(order.filter((item) => item === 'arm').length === 1,
  'visibility/persisted-pageshow/focus cluster armed more than one lifecycle');
check(order.filter((item) => item.startsWith('resume:')).join(',') ===
  'resume:visibility_resume,resume:pageshow_persisted,resume:focus',
  'foreground handlers lost their explicit recovery reasons');
check(order.indexOf('focus-infer') < order.indexOf('resume:focus'),
  'focus-only inference no longer runs before clustered resume deduplication');
check(foregroundAttemptSequence === clusteredAttemptBaseline + 1,
  'hidden/visible/persisted-pageshow/focus created more than one foreground attempt');

order.length = 0;
experimentalMediaLifecycleArmed = false;
experimentalMediaLifecycleResumeAttemptID = 0;
experimentalMediaForegroundRecovery = null;
document.visibilityState = 'visible';
const persistedAttemptBaseline = foregroundAttemptSequence;
handlers.pageshow({ persisted: true, isTrusted: true });
handlers.focus();
handlers.visibilitychange();
check(order.filter((item) => item === 'arm').length === 1 &&
  order.filter((item) => item.startsWith('resume:')).join(',') ===
    'resume:pageshow_persisted,resume:focus,resume:visibility_resume',
  'pageshow/focus/visibility permutation did not converge on one persisted lifecycle');
check(foregroundAttemptSequence === persistedAttemptBaseline + 1,
  'persisted pageshow/focus/visibility created more than one foreground attempt');

const returnSignals = {
  visibility() { handlers.visibilitychange(); },
  pageshow() { handlers.pageshow({ persisted: true, isTrusted: true }); },
  focus() { handlers.focus(); }
};
const returnReasons = {
  visibility: 'resume:visibility_resume',
  pageshow: 'resume:pageshow_persisted',
  focus: 'resume:focus'
};
for (const permutation of [
  ['visibility', 'pageshow', 'focus'],
  ['visibility', 'focus', 'pageshow'],
  ['pageshow', 'visibility', 'focus'],
  ['pageshow', 'focus', 'visibility'],
  ['focus', 'visibility', 'pageshow'],
  ['focus', 'pageshow', 'visibility']
]) {
  order.length = 0;
  experimentalMediaLifecycleArmed = false;
  experimentalMediaLifecycleResumeAttemptID = 0;
  experimentalMediaForegroundRecovery = null;
  document.visibilityState = 'visible';
  const attemptBaseline = foregroundAttemptSequence;
  armExperimentalMediaLifecycleResume();
  for (const signal of permutation) returnSignals[signal]();
  check(order.filter((item) => item === 'arm').length === 1,
    'return permutation armed more than one lifecycle: ' + permutation.join(','));
  check(foregroundAttemptSequence === attemptBaseline + 1,
    'return permutation created more than one foreground attempt: ' + permutation.join(','));
  check(order.filter((item) => item.startsWith('resume:')).join(',') ===
    permutation.map((signal) => returnReasons[signal]).join(','),
    'return permutation lost or reordered coordinator triggers: ' + permutation.join(','));
}

order.length = 0;
experimentalMediaLifecycleArmed = false;
experimentalMediaLifecycleResumeAttemptID = 0;
experimentalMediaForegroundRecovery = { id: ++foregroundAttemptSequence, cancelled: false };
const shortReturnAttemptBaseline = foregroundAttemptSequence;
handlers.blur();
check(order.join(',') === 'arm,close' && experimentalMediaForegroundRecovery === null,
  'a short Home-screen blur did not immediately fence and retire the old HDR surface');
handlers.focus();
check(order.includes('resume:focus') && order.includes('confirm:focus') &&
  foregroundAttemptSequence === shortReturnAttemptBaseline + 1,
  'the matching focus did not create one fresh attempt and arm trailing confirmation');
`)
}

func TestTicketViewerHDRFocusOnlyReturnIsFreshAndClustered(t *testing.T) {
	source := ticketAppSource(t)
	pulse := substringBetween(t, source,
		"function noteExperimentalMediaForegroundPulse() {",
		"  function scheduleExperimentalMediaCapabilityRetry(reason, attempt, forceCanvasReset) {")
	focus := substringBetween(t, source,
		"function recoverExperimentalMediaForFocusOnlyLifecycle() {",
		"  function mountExperimentalMediaControl() {")

	runTicketJavaScript(t, `
let wallNow = 50000;
const originalDateNow = Date.now;
Date.now = () => wallNow;
const document = { visibilityState: 'visible' };
const experimentalMediaPreferenceController = { enabled: true };
let experimentalMediaLifecycleArmed = false;
let experimentalMediaLifecycleLastResumeWallAt = 0;
const experimentalMediaLifecycleReturnClusterMillis = 250;
let experimentalMediaForegroundPulseWallAt = wallNow - 30000;
let experimentalMediaForegroundSuspensionGap = false;
const experimentalMediaForegroundSuspensionGapMillis = 2500;
let experimentalMediaForegroundRecovery = null;
let experimentalClientHDRController = null;
let arms = 0;
let closes = 0;
let resumes = 0;
function foregroundRecoveryCurrent() { return false; }
function armExperimentalMediaLifecycleResume() {
  arms += 1;
  experimentalMediaLifecycleArmed = true;
  experimentalMediaLifecycleLastResumeWallAt = 0;
}
function closeExperimentalMedia() { closes += 1; }
function experimentalMediaDocumentHasFocus() { return true; }
let experimentalMediaPresentationRecoveryPending = false;
function requestExperimentalHDRPresentationRegionRecovery() { return false; }
function resumeExperimentalMediaForLifecycle(reason) {
  if (reason !== 'foreground_pulse_gap') throw new Error('focus gap used the wrong recovery reason');
  resumes += 1;
  experimentalMediaLifecycleArmed = false;
  experimentalMediaLifecycleLastResumeWallAt = wallNow;
}
function check(value, message) { if (!value) throw new Error(message); }
`+pulse+focus+`

check(noteExperimentalMediaForegroundPulse() === true && arms === 1 && closes === 1 && resumes === 1,
  'a 30-second focus-only suspension did not force one fresh lifecycle');
experimentalMediaLifecycleArmed = false;
experimentalMediaForegroundSuspensionGap = false;
experimentalMediaForegroundPulseWallAt = wallNow;
experimentalMediaLifecycleLastResumeWallAt = wallNow;
check(noteExperimentalMediaForegroundPulse() === false &&
  recoverExperimentalMediaForFocusOnlyLifecycle() === false && arms === 1 && closes === 1 && resumes === 1,
  'focus inside the same positive-event cluster created a second lifecycle');
wallNow += experimentalMediaLifecycleReturnClusterMillis + 1;
check(recoverExperimentalMediaForFocusOnlyLifecycle() === true && arms === 2 && closes === 2 &&
  resumes === 1 && experimentalMediaLifecycleArmed,
  'a short standalone focus return reused the pre-return HDR surface');
Date.now = originalDateNow;
`)
}

func TestTicketViewerHDRForegroundReturnConfirmationRepairsLateBlur(t *testing.T) {
	source := ticketAppSource(t)
	confirmation := substringBetween(t, source,
		"function scheduleExperimentalMediaForegroundReturnConfirmation(reason) {",
		"  function noteExperimentalMediaForegroundPulse() {")

	runTicketJavaScript(t, `
const experimentalMediaPreferenceController = { enabled: true };
const experimentalMediaForegroundReturnConfirmationMillis = 500;
const document = { visibilityState: 'visible', hasFocus() { return false; } };
let experimentalMediaForegroundReturnConfirmationTimer = null;
let experimentalMediaForegroundReturnConfirmationSequence = 0;
let experimentalMediaLifecycleGeneration = 10;
let experimentalMediaLifecycleArmed = false;
let experimentalMediaPresentationRegionBlocked = false;
let experimentalMediaPresentationRecoveryPending = false;
let experimentalMediaForegroundRecovery = { id: 1, cancelled: false };
let documentFocused = true;
const timers = [];
const paints = [];
const resumes = [];
const queues = [];
const regionRequests = [];
function setTimeout(callback, millis) {
  const timer = { callback, millis, cancelled: false };
  timers.push(timer);
  return timer;
}
function clearTimeout(timer) { if (timer) timer.cancelled = true; }
const window = {
  requestAnimationFrame(callback) {
    paints.push(callback);
    return paints.length;
  }
};
function experimentalHDRSurfacePresentationAllowed() { return true; }
function controlCodePresentationPriorityActive() { return false; }
function foregroundRecoveryCurrent(attempt) {
  return Boolean(attempt && attempt === experimentalMediaForegroundRecovery && !attempt.cancelled);
}
function queueExperimentalMediaForegroundRecovery(attempt, delay) {
  queues.push({ attempt, delay });
  return true;
}
function requestExperimentalHDRPresentationRegionRecovery(reason) {
  regionRequests.push(reason);
  experimentalMediaPresentationRecoveryPending = false;
  experimentalMediaForegroundRecovery = { id: 100 + regionRequests.length, cancelled: false };
  return true;
}
function resumeExperimentalMediaForLifecycle(reason) {
  resumes.push(reason);
  experimentalMediaLifecycleArmed = false;
  experimentalMediaForegroundRecovery = { id: 10 + resumes.length, cancelled: false };
  return true;
}
function check(value, message) { if (!value) throw new Error(message); }
`+confirmation+`

check(scheduleExperimentalMediaForegroundReturnConfirmation('focus') === true &&
  timers.length === 1 && timers[0].millis === 500,
  'focus did not arm the bounded trailing foreground confirmation');
experimentalMediaForegroundRecovery.cancelled = true;
experimentalMediaForegroundRecovery = null;
experimentalMediaLifecycleGeneration += 1;
experimentalMediaLifecycleArmed = true;
timers[0].callback();
check(resumes.length === 0 && paints.length === 1,
  'wall-clock confirmation claimed foreground before an actual animation frame');
paints.shift()();
check(resumes.join(',') === 'return_confirm:focus' &&
  foregroundRecoveryCurrent(experimentalMediaForegroundRecovery) &&
  experimentalMediaForegroundRecovery.foregroundPaintConfirmed === true && queues.length === 1,
  'a late blur after focus stranded SDR instead of starting a fresh confirmed lifecycle');

check(scheduleExperimentalMediaForegroundReturnConfirmation('pageshow') === true,
  'an active clustered return did not arm confirmation');
const activeTimer = timers[1];
activeTimer.callback();
check(queues.length === 1 && paints.length === 1,
  'active attempt advanced before its trailing animation frame');
paints.shift()();
check(resumes.length === 1 && queues.length === 2 && queues[1].delay === 0,
  'trailing confirmation duplicated an active attempt instead of waking it');

experimentalMediaForegroundRecovery = null;
experimentalMediaPresentationRecoveryPending = true;
check(scheduleExperimentalMediaForegroundReturnConfirmation('stream_region') === true,
  'visible-region return did not arm confirmation');
timers[2].callback();
check(regionRequests.length === 0 && paints.length === 1,
  'pending presentation region advanced before its trailing animation frame');
paints.shift()();
check(regionRequests.join(',') === 'return_confirm:stream_region' &&
  queues.length === 3 && foregroundRecoveryCurrent(experimentalMediaForegroundRecovery) &&
  experimentalMediaForegroundRecovery.foregroundPaintConfirmed === true,
  'trailing confirmation did not promote pending region recovery into one fresh attempt');

experimentalMediaForegroundRecovery = null;
experimentalMediaLifecycleArmed = true;
delete window.requestAnimationFrame;
check(scheduleExperimentalMediaForegroundReturnConfirmation('missing_paint') === true,
  'missing-paint confirmation setup unexpectedly failed');
timers[3].callback();
check(resumes.length === 1 && regionRequests.length === 1 &&
  experimentalMediaForegroundRecovery === null && paints.length === 0,
  'missing animation-frame evidence started HDR instead of remaining safely in SDR');

window.requestAnimationFrame = (callback) => { paints.push(callback); return paints.length; };
check(scheduleExperimentalMediaForegroundReturnConfirmation('replaced_old') === true,
  'first replaceable confirmation was not scheduled');
const replacedTimer = timers[4];
check(scheduleExperimentalMediaForegroundReturnConfirmation('replaced_new') === true && replacedTimer.cancelled,
  'new return signal did not cancel the older confirmation timer');
const paintBaseline = paints.length;
replacedTimer.callback();
check(paints.length === paintBaseline,
  'superseded confirmation timer scheduled a stale foreground paint');
`)
}

func TestTicketViewerHDRDynamicRangeChangesStayOnTheForegroundCoordinator(t *testing.T) {
	source := ticketAppSource(t)
	capabilityMonitor := substringBetween(t, source,
		"function armExperimentalMediaDynamicRangeRecovery(options) {",
		"  function reportExperimentalMediaForegroundRecovery(attempt, phase, reason) {")

	runTicketJavaScript(t, `
const listeners = new Set();
const query = {
  matches: true,
  addEventListener(name, listener) { if (name === 'change') listeners.add(listener); },
  removeEventListener(name, listener) { if (name === 'change') listeners.delete(listener); },
  emit(matches) {
    this.matches = matches;
    for (const listener of Array.from(listeners)) listener({ matches });
  }
};
const window = { matchMedia() { return query; } };
const document = { visibilityState: 'visible' };
const experimentalMediaPreferenceController = { enabled: true };
const experimentalMediaState = { enabled: true };
let experimentalMediaDynamicRangeRecoveryQuery = null;
let experimentalMediaDynamicRangeRecoveryListener = null;
let experimentalMediaForegroundRecovery = { id: 4 };
let invalidations = 0;
const begins = [];
function clearExperimentalMediaDynamicRangeRecovery() {
  throw new Error('a capability transition removed the persistent monitor');
}
function foregroundRecoveryCurrent(attempt) { return attempt === experimentalMediaForegroundRecovery; }
function invalidateExperimentalMediaForegroundRecovery(reason) {
  invalidations += 1;
  if (reason !== 'dynamic_range_capability_unavailable') throw new Error('wrong invalidation reason');
  experimentalMediaForegroundRecovery = null;
}
function beginExperimentalMediaForegroundRecovery(reason, options) {
  begins.push({ reason, options });
  experimentalMediaForegroundRecovery = { id: 5 + begins.length };
  return true;
}
function check(value, message) { if (!value) throw new Error(message); }
`+capabilityMonitor+`

check(armExperimentalMediaDynamicRangeRecovery({ onlyFutureChange: true }) === true && listeners.size === 1,
  'the enabled HDR path did not retain one capability monitor');
query.emit(false);
check(invalidations === 1 && begins.length === 1 &&
  begins[0].reason === 'dynamic_range_capability_unavailable' &&
  begins[0].options.forceCanvasReset === true && listeners.size === 1,
  'capability loss did not revoke the attempt and start one safe bounded recovery');
query.emit(true);
check(begins.length === 2 && begins[1].reason === 'dynamic_range_capability_available' &&
  listeners.size === 1,
  'capability return bypassed the coordinator or removed the persistent monitor');
`)
}

func TestTicketViewerHDRForegroundVersionMismatchPerformsCacheBustingReload(t *testing.T) {
	source := ticketAppSource(t)
	versionCheck := substringBetween(t, source,
		"function checkServerVersion(payload) {",
		"  function normalizeAssetVersionURL() {")

	runTicketJavaScript(t, `
const pageVersion = 'ticket-remote-current';
const assetVersion = 'asset-old';
let serverVersionReloadTarget = '';
const replacements = [];
const location = {
  href: 'https://ticket.jolkins.id.lv/?v=asset-old',
  replace(value) { replacements.push(value); }
};
function check(value, message) { if (!value) throw new Error(message); }
`+versionCheck+`

check(checkServerVersion({ serverVersion: pageVersion, assetVersion: 'asset-new' }) === false,
  'asset mismatch was accepted by the old page');
check(replacements.length === 1 && new URL(replacements[0]).searchParams.get('v') === 'asset-new',
  'asset mismatch did not request one cache-busting replacement');
check(checkServerVersion({ serverVersion: pageVersion, assetVersion: 'asset-newer' }) === false &&
  replacements.length === 1,
  'clustered version mismatch requested more than one reload');
serverVersionReloadTarget = '';
check(checkServerVersion({ serverVersion: pageVersion, assetVersion }) === true && replacements.length === 1,
  'matching foreground version caused an unnecessary reload');
`)
}

func TestTicketViewerHDRForegroundRendererRetryIsSingleAndAttemptFenced(t *testing.T) {
	source := ticketAppSource(t)
	retry := substringBetween(t, source,
		"function scheduleExperimentalMediaRendererRetry(reason) {",
		"  function scheduleExperimentalMediaStart(reason, options) {")

	runTicketJavaScript(t, `
const document = { visibilityState: 'visible' };
let experimentalMediaResumeRetryArmed = true;
let experimentalMediaRendererRetryTimer = null;
let experimentalMediaState = { enabled: true };
let experimentalMediaForegroundRecovery = { id: 11, retryOrdinal: 0 };
let experimentalClientHDRFailed = true;
let experimentalMediaCanvasResetGeneration = 4;
let experimentalMediaPresentationRegionBlocked = false;
const timers = [];
const starts = [];
function setTimeout(callback, millis) {
  const timer = { callback, millis };
  timers.push(timer);
  return timer;
}
function scheduleExperimentalMediaStart(reason, options) { starts.push({ reason, options }); }
function experimentalHDRSurfacePresentationAllowed() { return true; }
function controlCodePresentationPriorityActive() { return false; }
function foregroundRecoveryCurrent(attempt) {
  return Boolean(attempt && experimentalMediaForegroundRecovery &&
    attempt.id === experimentalMediaForegroundRecovery.id);
}
function reportExperimentalMediaForegroundRecovery() {}
function check(value, message) { if (!value) throw new Error(message); }
`+retry+`

check(scheduleExperimentalMediaRendererRetry('device_lost') === true,
  'first renderer failure did not receive its bounded retry');
check(scheduleExperimentalMediaRendererRetry('device_lost_repeat') === false && timers.length === 1,
  'one failure scheduled more than one renderer retry');
check(timers[0].millis === 250, 'renderer retry lost its bounded delay');
timers[0].callback();
check(starts.length === 1 && starts[0].reason === 'renderer_retry:device_lost' &&
  starts[0].options.forceCanvasReset === true && experimentalMediaCanvasResetGeneration === -1,
  'renderer retry did not force exactly one fresh canvas');
check(scheduleExperimentalMediaRendererRetry('third_failure') === false,
  'exhausted renderer retry budget formed a loop');

experimentalMediaResumeRetryArmed = true;
experimentalMediaRendererRetryTimer = null;
experimentalMediaCanvasResetGeneration = 7;
experimentalClientHDRFailed = true;
experimentalMediaForegroundRecovery = { id: 12, retryOrdinal: 0 };
check(scheduleExperimentalMediaRendererRetry('stale_attempt') === true,
  'second foreground attempt did not receive its own retry budget');
const staleTimer = timers[1];
experimentalMediaForegroundRecovery = { id: 13, retryOrdinal: 0 };
staleTimer.callback();
check(starts.length === 1 && experimentalMediaCanvasResetGeneration === 7 && experimentalClientHDRFailed,
  'late retry callback crossed into a newer foreground attempt');
`)
}

func TestTicketViewerHDREstablishedDeviceLossStartsNewBoundedAttempt(t *testing.T) {
	source := ticketAppSource(t)
	recovery := substringBetween(t, source,
		"function scheduleExperimentalMediaActiveFailureRecovery(reason) {",
		"  function scheduleExperimentalMediaStart(reason, options) {")
	if !strings.Contains(source,
		"if (!scheduleExperimentalMediaActiveFailureRecovery(reason || 'renderer_failed'))") {
		t.Fatal("renderer failure status is not routed through active-session bounded recovery")
	}

	runTicketJavaScript(t, `
const document = { visibilityState: 'visible' };
const experimentalMediaPreferenceController = { enabled: true };
const experimentalMediaState = { enabled: true };
let experimentalMediaForegroundRecovery = null;
let experimentalMediaActiveFailureRecoveryTimer = null;
let experimentalMediaResumeRetryArmed = false;
let experimentalMediaPresentationRegionBlocked = false;
const timers = [];
const begins = [];
function setTimeout(callback, millis) {
  const timer = { callback, millis };
  timers.push(timer);
  return timer;
}
function foregroundRecoveryCurrent(attempt) {
  return Boolean(attempt && attempt === experimentalMediaForegroundRecovery && !attempt.cancelled);
}
function beginExperimentalMediaForegroundRecovery(reason, options) {
  begins.push({ reason, options });
  experimentalMediaForegroundRecovery = { id: begins.length, cancelled: false };
  return true;
}
function experimentalHDRSurfacePresentationAllowed() { return true; }
function controlCodePresentationPriorityActive() { return false; }
function check(value, message) { if (!value) throw new Error(message); }
`+recovery+`

check(scheduleExperimentalMediaActiveFailureRecovery('device_lost') === true && timers.length === 1,
  'established device loss did not schedule a fresh bounded recovery');
check(scheduleExperimentalMediaActiveFailureRecovery('device_lost_repeat') === true && timers.length === 1,
  'duplicate failure notification scheduled more than one recovery');
check(timers[0].millis === 0, 'active failure recovery was not handed to a fresh coordinator turn');
timers[0].callback();
check(begins.length === 1 && begins[0].reason === 'renderer_failure' &&
  begins[0].options.forceCanvasReset === true && experimentalMediaResumeRetryArmed,
  'device loss did not create a fresh-surface coordinator attempt');
check(scheduleExperimentalMediaActiveFailureRecovery('failure_inside_attempt') === false,
  'failure inside an owning attempt bypassed its single renderer retry');
`)
}

func TestTicketViewerHDRStableVisibleStartCancelsStaleCallbacks(t *testing.T) {
	source := ticketAppSource(t)
	cancel := substringBetween(t, source,
		"function cancelExperimentalMediaStart() {",
		"  function clearExperimentalMediaDynamicRangeRecovery() {")
	start := substringBetween(t, source,
		"function scheduleExperimentalMediaStart(reason, options) {",
		"  function connectExperimentalMedia(reason, options) {")

	runTicketJavaScript(t, `
const document = { visibilityState: 'visible', body: { dataset: {} } };
let experimentalMediaState = { enabled: true };
let experimentalMediaCapabilityReady = true;
let experimentalClientHDRController = null;
let experimentalMediaStartGeneration = 0;
let experimentalMediaStartPending = null;
let experimentalMediaCapabilityRetryTimer = null;
let experimentalMediaLastStartReason = 'initial';
let experimentalClientCapabilityAllowed = true;
let experimentalClientCapability = { supported: true };
let experimentalMediaLifecycleGeneration = 2;
let experimentalMediaCanvasResetGeneration = -1;
let experimentalMediaPresentationRegionBlocked = false;
const experimentalMediaVisibleSettleFrames = 2;
const experimentalMediaVisibleSettleTimeoutMillis = 250;
const paints = [];
const timers = [];
let nextHandle = 1;
const starts = [];
const window = {
  requestAnimationFrame(callback) {
    const item = { id: nextHandle++, callback, cancelled: false };
    paints.push(item);
    return item.id;
  },
  cancelAnimationFrame(id) {
    const item = paints.find((candidate) => candidate.id === id);
    if (item) item.cancelled = true;
  }
};
function setTimeout(callback, millis) {
  const timer = { callback, millis, cancelled: false };
  timers.push(timer);
  return timer;
}
function clearTimeout(timer) { if (timer) timer.cancelled = true; }
function runPaint() {
  const item = paints.shift();
  if (!item) throw new Error('expected a visible paint');
  if (!item.cancelled) item.callback();
}
function refreshExperimentalClientCapability() { return experimentalClientCapability; }
function setExperimentalMediaStatus() {}
function clearExperimentalMediaDynamicRangeRecovery() {}
function scheduleExperimentalMediaCapabilityRetry() { throw new Error('unexpected capability retry'); }
function experimentalHDRSurfacePresentationAllowed() { return true; }
function controlCodePresentationPriorityActive() { return false; }
function connectExperimentalClientHDR(options) { starts.push(options); return true; }
function check(value, message) { if (!value) throw new Error(message); }
`+cancel+start+`

check(scheduleExperimentalMediaStart('first_attempt', { forceCanvasReset: true }) === true,
  'visible start was not scheduled');
const staleTimer = experimentalMediaStartPending.fallbackTimer;
runPaint();
check(starts.length === 0, 'renderer started before two visible compositor opportunities');
cancelExperimentalMediaStart();
for (const item of paints.splice(0)) if (!item.cancelled) item.callback();
staleTimer.callback();
check(starts.length === 0, 'cancelled visible-start callbacks resurrected the renderer');

check(scheduleExperimentalMediaStart('replacement', { forceCanvasReset: true }) === true,
  'replacement visible start was not scheduled');
runPaint();
runPaint();
check(starts.length === 1 && starts[0].forceCanvasReset === true &&
  experimentalMediaLastStartReason === 'replacement',
  'replacement did not start exactly once after two visible opportunities');
`)
}
