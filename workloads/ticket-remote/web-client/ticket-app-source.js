/* Current Ticket page: live picture, oval only after that picture, swipe to register, in-app fresh unused ticket, control-code request. Start from CURRENT.md. Generated output is internal/web/static/app.js. */
import { html, reactive } from '@arrow-js/core';
import { ExperimentalHDRPreferenceController } from './experimental-hdr-preference.mjs';
import { ClientHDRBoostPreferenceController } from './client-hdr-boost-preference.mjs';
import {
  CLIENT_HDR_DISPLAY_BOOSTS,
  CLIENT_HDR_ENGINE,
  CLIENT_HDR_PIPELINE,
  CLIENT_HDR_PRESENTATION_KIND,
  CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS,
  CLIENT_HDR_TARGET_DISPLAY_BOOST,
  ClientHDRController,
  clientHDRCapability,
  clientHDREngineProjectionDecision,
  normalizeClientHDRDisplayBoost,
  offerClientHDRCanvasFrame,
  resolveCapabilityHDREngine
} from './client-hdr-core.mjs';
import {
  TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT,
  beginTicketActionV3LocalRequest,
  beginTicketLocalRegisterSliderSession,
  cancelTicketLocalRegisterSliderSession,
  completeTicketLocalRegisterSliderSession,
  handleTicketLocalRegisterSliderChange,
  isTicketActionV3RegistrationProofFresh,
  observeTicketActionV3LocalRequest,
  releaseTicketLocalRegisterSliderOnTerminal,
  rebaseTicketCurrentProofDetectorFromAction,
  resetTicketLocalRegisterSliderState,
  settleTicketActionV3LocalRequest,
  ticketCurrentProofFingerprintChanged,
  ticketCurrentProofRequestNeeded,
  ticketControlCodeVisualRecoveryRequired,
  ticketActionV3ActivationTerminalMessage,
  ticketActionV3ExplicitResultForDisplay,
  ticketActionV3IsExpectedEmptyRedetect,
  ticketActionV3LocalRequestBusy,
  ticketActionV3OccupiesPhone,
  ticketActionV3RequestArgs,
  ticketActionV3SmartSwitchAction,
  ticketActionV3SmartSwitchForView,
  ticketMemberLimitBlocks,
  ticketMemberLimitClockNow,
  ticketMemberLimitCountdown,
  ticketLocalRegisterSliderProofMatches,
  ticketLocalRegisterSliderProofSnapshot,
  updateTicketLocalRegisterSliderPointerDirection,
  updateTicketMemberLimitClock,
  ticketSliderRegionV3ForAction,
  ticketSliderRegionV3Layout
} from './ticket-action-v3-core.mjs';

(function () {
  const cfg = window.TICKET_REMOTE_CONFIG || {};
  const pageVersion = cfg.pageVersion || 'ticket-remote-dev';
  const assetVersion = String(cfg.assetVersion || pageVersion || '').trim();
  let serverVersionReloadTarget = '';
  const startupRunOrigin = /^ticket\.startup\.[0-9a-f]{32}$/.test(String(cfg.startupRunOrigin || '').trim())
    ? String(cfg.startupRunOrigin).trim()
    : '';

  function safeString(value) {
    if (value == null) return '';
    if (value instanceof Error) return value.message || value.name || 'error';
    if (typeof value === 'object') {
      try {
        return JSON.stringify(value);
      } catch (_) {
        return String(value);
      }
    }
    return String(value);
  }

  const pendingClientLogs = [];
  const sampledClientLogState = new Map();
  const sampledClientLogEvents = new Set(['control_code_capture_keepalive', 'stream_command_dispatched']);
  const sampledClientLogIntervalMs = 60000;
  let spacetimeClient = null;

  function compactClientEventName(value) {
    const event = String(value || 'client_event').replace(/[^0-9A-Za-z_-]/g, '_').slice(0, 80) || 'client_event';
    if (event === 'page_boot') return 'browser_opened';
    if (/video_socket.*open/.test(event)) return 'stream_opened';
    if (/video_socket.*closed|viewer_idle_disconnected|video_stream_paused_hidden|activation_visibility_hidden|activation_pagehide/.test(event)) return 'stream_closed';
    if (/video_socket_connect_attempt|video_stream_restart|fresh_video_resume|cached_video_resume|viewer_idle_resumed|activation_resume_(start|finish)/.test(event)) return 'stream_started';
    if (/keyframe/.test(event)) return /failed/.test(event) ? 'keyframe_failed' : 'keyframe_requested';
    if (/activation_resume_fresh_frame/.test(event)) return 'stream_recovered';
    if (/recover|recovery/.test(event)) return /failed|exhausted/.test(event) ? 'stream_failed' : 'stream_recovery_requested';
    if (/stale_video_frames|server_stale_frames|loading_over_2s/.test(event)) return 'stream_stalled';
    if (event === 'control_code_submitted') return 'control_code_requested';
    if (/control_code.*prepare.*complete/.test(event)) return 'control_code_sent';
    if (/control_code.*failed/.test(event)) return 'control_code_failed';
    if (event === 'control_code_capture_keepalive') return 'control_code_capturing';
    if (/control_code.*ignored/.test(event)) return 'control_code_ignored';
    if (/spacetime.*failed|spacetime_direct_unavailable/.test(event)) return 'state_failed';
    if (event === 'spacetime_client_status') return 'state_changed';
    return event;
  }

  function compactClientLogEntry(entry) {
    const raw = String(entry && entry.event || 'client_event');
    const event = compactClientEventName(raw);
    const normalized = Object.assign({}, entry, { event });
    if (event === raw) return normalized;
    let detail = normalized.detailJson || normalized.detail || '';
    try {
      const parsed = JSON.parse(String(detail || '{}'));
      detail = parsed && typeof parsed === 'object' && !Array.isArray(parsed)
        ? Object.assign({ originalEvent: raw }, parsed)
        : { originalEvent: raw, detail: safeString(detail).slice(0, 800) };
    } catch (_) {
      detail = { originalEvent: raw, detail: safeString(detail).slice(0, 800) };
    }
    normalized.detailJson = safeString(detail).slice(0, 1000);
    delete normalized.detail;
    return normalized;
  }

  function sampledClientLogDetail(event, detail) {
    if (!sampledClientLogEvents.has(event)) return detail;
    const now = Date.now();
    const previous = sampledClientLogState.get(event) || 0;
    if (now - previous < sampledClientLogIntervalMs) return null;
    sampledClientLogState.set(event, now);
    return detail;
  }

  function enqueueClientLog(entry) {
    const compacted = compactClientLogEntry(entry);
    pendingClientLogs.push(compacted);
    if (pendingClientLogs.length > 100) pendingClientLogs.splice(0, pendingClientLogs.length - 100);
    if (typeof queueMicrotask === 'function') queueMicrotask(flushClientLogs);
  }

  function reportClientFault(event, detail) {
    const raw = String(event || 'client_event').replace(/[^0-9A-Za-z_-]/g, '_').slice(0, 80) || 'client_event';
    const sampled = sampledClientLogDetail(raw, safeString(detail).slice(0, 500));
    if (sampled == null) return;
    enqueueClientLog({
      level: 'info',
      event: raw,
      detailJson: safeString({
        pageVersion,
        assetVersion,
        detail: sampled,
        visibility: document.visibilityState,
        webCodecs: 'VideoDecoder' in window
      }).slice(0, 1000),
      at: Date.now()
    });
  }

  function escapeHTML(value) {
    return String(value || '').replace(/[&<>"']/g, (ch) => ({
      '&': '&amp;',
      '<': '&lt;',
      '>': '&gt;',
      '"': '&quot;',
      "'": '&#39;'
    }[ch]));
  }

  function showFatalPage(message) {
    try {
      document.body.className = 'auth-error-page';
      document.body.innerHTML = [
        '<main class="auth-shell">',
        '<section class="auth-panel">',
        `<p class="auth-status" role="alert">${escapeHTML(message || 'Biļetes lapa pašlaik nevar ielādēties.')}</p>`,
        '<button id="retryAuth" class="primary" type="button">Mēģināt vēlreiz</button>',
        '</section>',
        '</main>'
      ].join('');
      const retry = document.getElementById('retryAuth');
      if (retry) retry.addEventListener('click', () => location.reload());
    } catch (_) {}
  }

  window.addEventListener('error', (event) => {
    reportClientFault('runtime_error', event && (event.message || event.error));
  });
  window.addEventListener('unhandledrejection', (event) => {
    reportClientFault('unhandled_rejection', event && event.reason);
  });

  if ('scrollRestoration' in history) {
    history.scrollRestoration = 'manual';
  }
  normalizeAssetVersionURL();

  let spacetimeClientStatus = 'idle';
  let spacetimeStateFresh = false;
  let spacetimeStateRefreshTimer = null;
  let spacetimeStateRefreshStartedAt = 0;
  const spacetimeStateRefreshTimeoutMs = 5000;
  let spacetimeDirectUnavailableLogged = false;
  let directSpacetimeToken = '';
  let directSpacetimeTokenExpiresAt = 0;
  let spacetimeClientScriptPromise = null;
  let spacetimeClientConnectPromise = null;
  let spacetimeExpiredTokenRefreshPromise = null;
  let activityTickInFlight = false;
  let activityTickTimer = null;
  const activityTickIntervalMs = 5000;
  const activityTickMaximumDelayMs = 1000;

  if (document.body) document.body.dataset.spacetimeConnection = 'idle';

  if (!cfg.authenticated) {
    startAuthRedirect();
    return;
  }

  function requireElement(selector, label) {
    const element = selector.startsWith('#') ? document.getElementById(selector.slice(1)) : document.querySelector(selector);
    if (!element) {
      reportClientFault('missing_ticket_dom', label || selector);
      showFatalPage('Biļetes lapa nav pilnībā ielādējusies. Mēģini pārlādēt lapu.');
    }
    return element;
  }

  const stage = requireElement('.stage', 'stage');
  const stagePage = stage && stage.closest ? stage.closest('.stage-page') : null;
  const canvas = requireElement('#screen', 'screen');
  if (!stage || !canvas || typeof canvas.getContext !== 'function') return;
  const ctx = canvas.getContext('2d', { alpha: false });
  if (!ctx) {
    reportClientFault('canvas_context_unavailable', '2d');
    showFatalPage('Šajā pārlūkā biļetes attēlu nevar atvērt. Mēģini pārlādēt lapu.');
    return;
  }
  const emptyState = requireElement('#emptyState', 'emptyState');
  const startStreamButton = requireElement('#startStream', 'startStream');
  const emptyMessage = requireElement('#emptyMessage', 'emptyMessage');
  if (!emptyState || !startStreamButton || !emptyMessage) return;
  const streamResumeSpinner = document.getElementById('streamResumeSpinner');
  let experimentalMediaCanvas = document.getElementById('experimentalMediaCanvas');
  const hdrHoldoverNotice = document.getElementById('hdrHoldoverNotice');
  const experimentalMediaMount = document.getElementById('experimentalMediaMount');
  const connectionState = requireElement('#connectionState', 'connectionState');
  const statusLine = requireElement('#statusLine', 'statusLine');
  if (!connectionState || !statusLine) return;
  const panel = document.getElementById('panel');
  const presence = requireElement('#presence', 'presence');
  const requestCodeButton = requireElement('#requestControlCode', 'requestControlCode');
  const requestTicketResetButton = requireElement('#requestTicketReset', 'requestTicketReset');
  const requestTicketResetAndActivateButton = requireElement('#requestTicketResetAndActivate', 'requestTicketResetAndActivate');
  const activateTicketButton = requireElement('#activateTicket', 'activateTicket');
  const ticketRegisterOverlay = requireElement('#ticketRegisterOverlay', 'ticketRegisterOverlay');
  const ticketLocalRegisterSlider = requireElement('#ticketLocalRegisterSlider', 'ticketLocalRegisterSlider');
  const ticketViewSwitchButton = requireElement('#ticketViewSwitch', 'ticketViewSwitch');
  const ticketViewSwitchDetail = requireElement('#ticketViewSwitchDetail', 'ticketViewSwitchDetail');
  const ticketResetDetail = requireElement('#ticketResetDetail', 'ticketResetDetail');
  const ticketActivationAt = requireElement('#ticketActivationAt', 'ticketActivationAt');
  const ticketActivationTimer = requireElement('#ticketActivationTimer', 'ticketActivationTimer');
  const ticketLimitMode = requireElement('#ticketLimitMode', 'ticketLimitMode');
  const ticketRegistrationLimitUsage = requireElement('#ticketRegistrationLimitUsage', 'ticketRegistrationLimitUsage');
  const ticketRegistrationLimitDetail = requireElement('#ticketRegistrationLimitDetail', 'ticketRegistrationLimitDetail');
  const ticketControlCodeLimitUsage = requireElement('#ticketControlCodeLimitUsage', 'ticketControlCodeLimitUsage');
  const ticketControlCodeLimitDetail = requireElement('#ticketControlCodeLimitDetail', 'ticketControlCodeLimitDetail');
  const codeRequestState = requireElement('#codeRequestState', 'codeRequestState');
  const codeRequestDetail = requireElement('#codeRequestDetail', 'codeRequestDetail');
  const codeDialog = requireElement('#controlCodeDialog', 'controlCodeDialog');
  const codeForm = requireElement('#controlCodeForm', 'controlCodeForm');
  const codeDigits = requireElement('#controlCodeDigits', 'controlCodeDigits');
  const codeSubmit = requireElement('#controlCodeSubmit', 'controlCodeSubmit');
  const codeDialogClose = requireElement('#controlCodeDialogClose', 'controlCodeDialogClose');
  const codeError = requireElement('#controlCodeError', 'controlCodeError');
  const codeResultArea = requireElement('#controlCodeResultArea', 'controlCodeResultArea');
  const codeResultImage = requireElement('#controlCodeResultImage', 'controlCodeResultImage');
  const codeResultStatus = requireElement('#controlCodeResultStatus', 'controlCodeResultStatus');
  const codeResultValue = requireElement('#controlCodeResultValue', 'controlCodeResultValue');
  const codeResultTimer = requireElement('#controlCodeResultTimer', 'controlCodeResultTimer');
  const codeResultClose = requireElement('#closeControlCodeResult', 'closeControlCodeResult');
  const controlCodeHotspot = requireElement('#controlCodeHotspot', 'controlCodeHotspot');
  if (!presence || !requestCodeButton || !requestTicketResetButton || !requestTicketResetAndActivateButton || !activateTicketButton || !ticketRegisterOverlay || !ticketLocalRegisterSlider || !ticketViewSwitchButton || !ticketViewSwitchDetail || !ticketResetDetail || !ticketActivationAt || !ticketActivationTimer || !ticketLimitMode || !ticketRegistrationLimitUsage || !ticketRegistrationLimitDetail || !ticketControlCodeLimitUsage || !ticketControlCodeLimitDetail || !codeRequestState || !codeRequestDetail || !codeDialog || !codeForm || !codeDigits || !codeSubmit || !codeDialogClose || !codeError || !codeResultArea || !codeResultImage || !codeResultStatus || !codeResultValue || !codeResultTimer || !codeResultClose || !controlCodeHotspot) return;
  const viewerCount = document.getElementById('viewerCount');
  const viewerCountDetail = document.getElementById('viewerCountDetail');
  const presenceListAnchorKey = 'viewer-list-anchor';
  const presenceState = reactive({ viewers: [], visibleViewerCount: 0, hasVisibleRows: false });
  let presenceMounted = false;

  let videoWs = null;
  const experimentalMediaState = reactive({
    enabled: false,
    status: 'Izslēgts',
    preferenceStatus: 'Saglabātais HDR iestatījums: izslēgts.',
    label: 'HDR skats',
    engine: CLIENT_HDR_ENGINE,
    engineStatus: 'HDR apstrāde: šī pārlūkprogramma.',
    boostSelectorAllowed: false,
    engineSaving: false,
    displayBoost: CLIENT_HDR_TARGET_DISPLAY_BOOST,
    boostStatus: `Pārlūka HDR spilgtums: ${CLIENT_HDR_TARGET_DISPLAY_BOOST}×.`,
    boostSaving: false
  });
  let experimentalMediaMounted = false;
  let experimentalMediaCapabilityReady = false;
  let experimentalClientCapabilityAllowed = false;
  let experimentalMediaEngineProjectionObserved = false;
  let experimentalMediaBoostProjectionObserved = false;
  let experimentalMediaOwnerProjectionAvailable = false;
  let experimentalMediaAccountProjectionAvailable = false;
  let experimentalClientCapability = clientHDRCapability(window);
  let experimentalClientHDRController = null;
  let experimentalClientHDRFailed = false;
  let experimentalMediaResumeRetryArmed = false;
  let experimentalMediaLifecycleGeneration = 0;
  let experimentalMediaLifecycleArmed = false;
  let experimentalMediaLifecycleResumeAttemptID = 0;
  let experimentalMediaForegroundPulseWallAt = Date.now();
  let experimentalMediaForegroundSuspensionGap = false;
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
  let experimentalMediaStreamRegionVisible = true;
  let experimentalMediaCanvasGeneration = 0;
  let experimentalMediaCanvasResetGeneration = -1;
  let experimentalMediaStartGeneration = 0;
  let experimentalMediaStartPending = null;
  let experimentalMediaCapabilityRetryTimer = null;
  let experimentalMediaRendererRetryTimer = null;
  let experimentalMediaActiveFailureRecoveryTimer = null;
  let experimentalMediaCapabilityDiscoveryPromise = null;
  let experimentalMediaCapabilityDiscoveryRetryTimer = null;
  let experimentalMediaCapabilityDiscoveryAttempt = 0;
  let experimentalMediaDynamicRangeRecoveryQuery = null;
  let experimentalMediaDynamicRangeRecoveryListener = null;
  let experimentalMediaLastStartReason = 'initial';
  let experimentalClientHDRMetricFrames = 0;
  let experimentalClientHDRGPUCompletionFrames = 0;
  let experimentalClientHDRPaintWaitFailures = 0;
  let experimentalClientHDRCloneFailures = 0;
  let experimentalMediaCanvasContextKind = '';
  let experimentalMediaPipeline = '';
  const experimentalMediaVisibleSettleFrames = 2;
  const experimentalMediaVisibleSettleTimeoutMillis = 250;
  const experimentalMediaForegroundSuspensionGapMillis = 2500;
  const experimentalMediaLifecycleReturnClusterMillis = 250;
  const experimentalMediaForegroundReturnConfirmationMillis = 500;
  const experimentalMediaForegroundCanvasStabilityMillis = 1000;
  const experimentalMediaCapabilityFetchTimeoutMillis = 3000;
  const experimentalMediaCapabilityRetryDelays = Object.freeze([250, 1000]);
  const experimentalMediaForegroundRecoveryWindowMillis = 12000;
  const experimentalMediaForegroundRecoveryRetryDelays = Object.freeze([0, 250, 750, 1500, 3000]);
  const experimentalMediaPreferenceController = new ExperimentalHDRPreferenceController({
    applyEnabled: (enabled, meta) => applyExperimentalMediaPreference(enabled, meta),
    persistEnabled: (enabled) => runSpacetimeMutation(
      (client) => client.setHDRPreference(Boolean(enabled)),
      'hdr_preference_write'
    ),
    onStatus: (snapshot) => {
      experimentalMediaState.preferenceStatus = experimentalHDRPreferenceStatus(snapshot);
      if (document.body) document.body.dataset.hdrPreference = String(snapshot && snapshot.phase || 'default');
    },
    onFailure: (failure) => {
      clientLog('state_failed', String(failure && failure.code || 'hdr_preference_write_failed'));
    }
  });
  const experimentalHDRBoostPreferenceController = new ClientHDRBoostPreferenceController({
    applyBoost: (boost, meta) => applyExperimentalHDRBoost(boost, meta),
    persistBoost: (boost) => runSpacetimeMutation(
      (client) => client.setHDRDisplayBoost(boost),
      'hdr_boost_write'
    ),
    onStatus: (snapshot) => {
      experimentalMediaState.boostSaving = Boolean(snapshot && snapshot.inFlight);
      experimentalMediaState.boostStatus = experimentalHDRBoostStatus(snapshot);
      if (document.body) document.body.dataset.hdrBoostPreference = String(snapshot && snapshot.phase || 'default');
    },
    onFailure: (failure) => {
      clientLog('state_failed', String(failure && failure.code || 'hdr_boost_write_failed'));
    }
  });
  const activeVideoSockets = new Set();
  let reconnectTimer = null;
  let hiddenVideoCloseTimer = null;
  let hiddenStreamFocusTimer = null;
  let configured = false;
  let streamUnsupported = false;
  let streamSize = { width: 540, height: 1080, sourceHeight: 2424, sourceTopCrop: 200, sourceVisibleHeight: 2224 };
  let currentState = null;
  let serverClockSkewMs = 0;
  let serverClockHasLiveSample = false;
  const ticketActionV3LocalRequestState = { actionId: '', reducerSettled: false, observed: false };
  let ticketActionV3LastUserActionId = '';
  let ticketActionV3LastUserAction = null;
  let ticketActionV3LastUserMessage = '';
  let ticketActionV3ReconcileTimer = null;
  const ticketActionV3ReconcileIntervalMs = 1000;
  let ticketViewSwitchExpiryTimer = null;
  const ticketLocalRegisterSliderState = {
    inFlight: false,
    session: null,
    actionId: '',
    latchedProof: null,
    ignoreChange: false
  };
  let ticketSliderLayoutRevision = 0;
  let ticketSliderVisualRevision = 0;
  let ticketLimitPresentationTimer = null;
  let ticketMemberLimitClock = null;
  let ticketSliderRegionExpiryTimer = null;
  const ticketCurrentProofRenewBeforeMs = 15_000;
  let ticketCurrentProofInFlight = false;
  let ticketCurrentProofLastActionId = '';
  let ticketCurrentProofRequestedScope = '';
  let ticketCurrentProofLastRequestAt = 0;
  let ticketCurrentProofLastSampleAt = 0;
  const ticketCurrentProofVisualState = {
    rebasedActionId: '',
    fingerprint: null,
    candidateFingerprint: null,
    stableChangeCount: 0,
    changePending: false,
    resumePending: true
  };
  const ticketCurrentProofSampleIntervalMs = 1000;
  const ticketCurrentProofRequestCooldownMs = 3000;
  const ticketCurrentProofFingerprintCanvas = document.createElement('canvas');
  ticketCurrentProofFingerprintCanvas.width = 8;
  ticketCurrentProofFingerprintCanvas.height = 12;
  const ticketCurrentProofFingerprintContext = ticketCurrentProofFingerprintCanvas.getContext('2d', {
    alpha: false,
    willReadFrequently: true
  });
  let connectedAt = 0;
  let videoSocketCreatedAt = 0;
  let videoConnectedAt = 0;
  let configuredAt = 0;
  let lastFrameAt = 0;
  let lastHiddenAt = 0;
  let lastDecodedFrameAt = 0;
  let lastDecodedFrameSequence = 0;
  let lastPacketAt = 0;
  let lastPacketSequenceAdvancedAt = 0;
  let lastAcceptedFrameReceivedAt = 0;
  let lastAcceptedFrameVisualAgeMillis = 0;
  let lastAcceptedFrameQueuedAt = 0;
  let lastRenderedFrameReceivedAt = 0;
  let lastRenderedFrameQueuedAt = 0;
  let lastRenderedFrameRenderedAt = 0;
  let lastRenderedFrameVisualAgeMillis = 0;
  let lastRenderedFrameEpoch = 0;
  let lastRenderedFrameSequence = 0;
  let lastRenderedPresentationOrdinal = 0;
  // Unlike the stream-local presentation ordinal, this watermark deliberately
  // survives decoder/socket resets so a foreground admission can prove that
  // the authoritative SDR canvas was painted after the return boundary.
  let authoritativeSDRRenderSerial = 0;
  let lastRenderedFrameTimestamp = 0;
  let lastRestartAt = 0;
  let lastRecoveryKeyframeAt = 0;
  let lastKeyframeCommandAt = 0;
  let lastRecoveryDecoderResetAt = 0;
  let lastRecoveryVideoReconnectAt = 0;
  let lastRecoveryVideoReconnectSeq = -1;
  let lastRecoveryServerRecoverAt = 0;
  let firstFrameServerRecoveryAttempts = 0;
  let firstFrameServerRecoveryExhausted = false;
  let decoder = null;
  let decoderGeneration = 0;
  let decoderConfigureGeneration = 0;
  let decoderConfigured = false;
  let decoderMode = 'annexb';
  let avcAdapterTried = false;
  let avcDescription = null;
  let avcSps = null;
  let avcPps = null;
  let lastDecoderConfig = null;
  let needsKeyFrame = true;
  let currentStreamEpoch = 0;
  let lastPacketSequence = 0;
  let lastPacketTimestamp = 0;
  let lastAcceptedFrameSequence = 0;
  let lastAcceptedFrameTimestamp = 0;
  let firstFrameReceived = false;
  let firstDecodedTraceSent = false;
  let firstRenderedTraceSent = false;
  let hasRenderedFrame = false;
  let fallbackFrameCanvas = null;
  let fallbackFrameAvailable = false;
  let lastFallbackFrameAt = 0;
  let latestStreamStatus = null;
  let lastStreamStatusAt = 0;
  let codeRequest = null;
  let controlCodeSubmitInFlight = false;
  let controlCodeCleanupPendingRequestID = '';
  let controlCodeFastState = null;
  let pendingFrameMetadata = [];
  const pendingFrameMetadataByTimestamp = new Map();
  let pendingFrameMetadataCount = 0;
  let pendingPresentedFrame = null;
  let presentationFrameHandle = null;
  let presentationCoalescedFrames = 0;
  let decoderRejectedFrames = 0;
  let resyncDroppedFrames = 0;
  let feedbackTimer = null;
  let lastFeedbackSentAt = 0;
  let feedbackSentCount = 0;
  let feedbackSendFailureCount = 0;
  let feedbackImmediateKey = '';
  let controlCodeResultCaptureTimer = null;
  let controlCodeResultCaptureRequestID = '';
  let controlCodeResultCapturedRequestID = '';
  let controlCodeCaptureAckInFlightRequestID = '';
  let pendingControlCodeBaselineFrameFingerprint = null;
  let controlCodeBaselineFrameFingerprint = null;
  let controlCodeBaselineRequestID = '';
  let lastControlCodeCaptureDebug = null;
  let lastControlCodeCaptureKeyframeRequestAt = 0;
  let lastControlCodeCaptureKeyframeRetryCount = 0;
  let lastControlCodeLowLatencyFrameKey = '';
  let lastControlCodeDecoderBacklogResetKey = '';
  let controlCodeResultCaptureStartedAt = 0;
  let lastControlCodeMarkerReceivedLogKey = '';
  let lastControlCodeMarkerWaitingLogKey = '';
  let lastControlCodeCandidateRejectedLogKey = '';
  let controlCodeSafeGeneratedFrameRequestID = '';
  let controlCodeSafeGeneratedFrameEpoch = 0;
  let controlCodeSafeGeneratedFrameSequence = 0;
  let controlCodeSafeGeneratedFrameCount = 0;
  let controlCodeFrozenFrameCanvas = null;
  let controlCodeFrozenFrameKey = '';
  let controlCodeHDRFreezeTarget = null;
  let controlCodePreparedCaptureDisplayedRequestID = '';
  let controlCodeFastStateExpiryTimer = null;
  let lastRenderedControlCodeRequestSignature = '';
  const localSessionID = String(cfg.sessionId || '').trim();
  const localPublicID = accountPublicId(cfg.email || '');
  const ownedControlCodeRequestIDs = new Set();
  const locallyClosedControlCodeRequestIDs = new Set();
  let codeDialogOpen = false;
  let controlCodeDialogScrollLock = null;
  let codeResultTickTimer = null;
  let activeResumeFlow = null;
  let hiddenDecoderTransientLogged = false;
  let activationReconnectBurstTimer = null;
  let videoSocketOpenSeq = 0;
  let lastHiddenWallAt = 0;
  let stableViewport = null;
  let idleDisconnected = false;
  let idleDisconnectTimer = null;
  let streamLiveStaleGraceTimer = null;
  const intentionallyClosedVideoSockets = new WeakSet();
  const streamFirstFrameKeyframeMs = 2000;
  const streamLiveFreshMaxAgeMs = 1250;
  const streamLiveOkMaxAgeMs = 2000;
  const streamDegradedMaxAgeMs = 3000;
  const streamLiveStaleGraceMs = 500;
  const streamStaleKeyframeMs = 3000;
  const streamStaleDecoderResetMs = 5000;
  const streamStaleVideoReconnectMs = 8000;
  const streamStaleServerRecoverMs = 12000;
  // A reset normally completes well before this window.  If the durable row is
  // still queued/preparing after the window, keep the browser usable and show
  // the recoverable attention state instead of locking every control forever.
  const ticketInteractionPreparingStaleAfterMs = 2 * 60 * 1000;
  const controlCodeRequestMissingRowStaleAfterMs = 2 * 60 * 1000;
  const streamDecoderStartupGraceMs = 3500;
  const hiddenVideoCloseDelayMs = 3000;
  const backgroundRecoveryHiddenMs = 30000;
  const oldTabFreshResumeHiddenMs = 5000;
  const resumeSoftReconnectMs = 1800;
  const activationReconnectBurstMs = 10000;
  const activationReconnectFirstRetryMs = 150;
  const activationReconnectTickMs = 500;
  const activationReconnectMaxTicks = 10;
  const firstFrameServerRecoveryMaxAttempts = 2;
  const idleDisconnectMs = 15 * 60 * 1000;
  const recoveryKeyframeDebounceMs = 2000;
  const keyframeCommandMinIntervalMs = 2500;
  const recoveryDecoderResetDebounceMs = 5000;
  const recoveryVideoReconnectDebounceMs = 8000;
  const recoveryServerRecoverDebounceMs = 12000;
  const controlCodeFingerprintGridWidth = 12;
  const controlCodeFingerprintGridHeight = 16;
  const controlCodeFingerprintDifferenceThreshold = 14;
  const controlCodeFingerprintChangedCellsThreshold = 14;
  const controlCodeCapturePollMs = 100;
  const controlCodeResultInitialKeyframeDelayMs = 1250;
  const controlCodeCaptureKeyframeRetryMs = 5000;
  const controlCodeCaptureKeyframeRetryLimit = 2;
  const controlCodeLowLatencyVisualAgeMs = 1250;
  const controlCodeLowLatencyDecodeQueueLimit = 1;
  const controlCodeResultImageReadyTimeoutMs = 1200;
  const controlCodeResultPaintFrameTimeoutMs = 500;
  // The ViVi generated-code strip is a full-width dark panel immediately below the Aztec
  // graphic. Recent releases moved it upward slightly, so scan a narrow band around that
  // anchor and require several rows to be dark across most of the strip. The Aztec itself
  // must never satisfy this proof.
  const controlCodeGeneratedChipScanStartY = 0.30;
  const controlCodeGeneratedChipScanEndY = 0.50;
  const controlCodeGeneratedChipScanStepY = 0.005;
  const FRAME_ENVELOPE_MAGIC = 0x54534632;
  const FRAME_ENVELOPE_HEADER_BYTES = 29;
  const streamFeedbackVersion = 1;
  const streamFeedbackIntervalMs = 500;
  const streamFeedbackHiddenIntervalMs = 2000;
  const streamDecoderQueueHardLimit = 4;
  const streamIngressFrameMaxAgeMs = 1250;
  const streamIngressMetadataLimit = 32;

  function closeEarlyVideo(reason) {
    const early = window.TICKET_EARLY_VIDEO;
    if (!early || early.claimed) return;
    early.claimed = true;
    early.queue = [];
    early.config = null;
    early.queueBytes = 0;
    const socket = early.ws;
    early.ws = null;
    if (!socket) return;
    try { socket.close(1000, reason || 'app_loaded'); } catch (_) {}
  }

  function claimableEarlyVideoQueue(early) {
    const queued = Array.isArray(early && early.queue) ? early.queue.slice() : [];
    if (!queued.length) return queued;
    const independentFrame = queued[queued.length - 1];
    const metadata = independentFrame && independentFrame.meta;
    const receivedAt = Number(independentFrame && independentFrame.receivedAt);
    const maxAgeMillis = Math.max(0, Number(early && early.maxFrameAgeMs || 1250));
    const receivedAgeMillis = Number.isFinite(receivedAt) ? Math.max(0, performance.now() - receivedAt) : Number.POSITIVE_INFINITY;
    if (!metadata || !metadata.key || !independentFrame.data || independentFrame.data.byteLength > 2 * 1024 * 1024 || receivedAgeMillis > maxAgeMillis) return [];
    return [independentFrame];
  }

  function claimEarlyVideoSocket() {
    const early = window.TICKET_EARLY_VIDEO;
    if (!early || early.claimed) return null;
    early.claimed = true;
    const socket = early.ws;
    const queued = claimableEarlyVideoQueue(early);
    if (early.config) queued.unshift(early.config);
    early.queue = [];
    early.config = null;
    early.queueBytes = 0;
    early.ws = null;
    if (!socket || early.error || early.closed || socket.readyState === WebSocket.CLOSED || socket.readyState === WebSocket.CLOSING) {
      if (socket) {
        try { socket.close(1000, 'early_video_unusable'); } catch (_) {}
      }
      return null;
    }
    return { socket, queued, openedAt: Number(early.openedAt || 0) };
  }

  function scheduleViewerIdleDisconnect(reason) {
    if (idleDisconnected) return;
    if (idleDisconnectTimer) {
      clearTimeout(idleDisconnectTimer);
      idleDisconnectTimer = null;
    }
    idleDisconnectTimer = setTimeout(() => expireViewerIdle(reason || 'idle_timeout'), idleDisconnectMs);
  }

  function noteViewerActivity(event, reason) {
    if (event && event.isTrusted === false) return false;
    if (idleDisconnected) {
      return resumeFromIdleDisconnect(reason || (event && event.type) || 'activity');
    }
    scheduleViewerIdleDisconnect(reason || (event && event.type) || 'activity');
    return false;
  }

  function expireViewerIdle(reason) {
    if (idleDisconnected) return;
    if (document.visibilityState === 'visible') {
      scheduleViewerIdleDisconnect('visible_idle_keepalive');
      clientLog('viewer_idle_visible_keepalive', reason || 'idle_timeout');
      if (!streamHasFreshRenderedFrame()) {
        recoverAfterVisibilityResume('visible_idle_keepalive');
      }
      return;
    }
    idleDisconnected = true;
    if (idleDisconnectTimer) {
      clearTimeout(idleDisconnectTimer);
      idleDisconnectTimer = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (hiddenVideoCloseTimer) {
      clearTimeout(hiddenVideoCloseTimer);
      hiddenVideoCloseTimer = null;
    }
	    if (hiddenStreamFocusTimer) {
	      clearTimeout(hiddenStreamFocusTimer);
	      hiddenStreamFocusTimer = null;
	    }
	    clearActivationReconnectBurst();
	    closeEarlyVideo('idle_disconnect');
    closeDirectVideo();
    resetStreamState({ preserveFrame: true });
    if (spacetimeClient && typeof spacetimeClient.close === 'function') {
      spacetimeClient.close();
    }
    spacetimeClient = null;
    setConnected('Apturēts');
    setStatus('Straume apturēta pēc 15 minūtēm bez darbības.');
    showEmpty('Straume ir apturēta pēc 15 minūtēm bez darbības. Pieskaries Sākt, lai turpinātu.', true);
    document.body.dataset.streamFreshness = 'IDLE_DISCONNECTED';
    publishStreamDebug();
    clientLog('viewer_idle_disconnected', reason || 'idle_timeout');
  }

  function resumeFromIdleDisconnect(reason) {
    if (!idleDisconnected || streamUnsupported) return false;
    if (document.visibilityState === 'hidden' && !controlCodeKeepsVideoAliveWhileHidden()) return false;
    lastHiddenAt = 0;
    lastHiddenWallAt = 0;
    idleDisconnected = false;
    document.body.dataset.streamFreshness = 'RECOVERING';
    setConnected('Savienojas');
    setStatus('Atjauno tiešraidi...');
    showStreamRecovery();
    scheduleViewerIdleDisconnect(reason || 'idle_resume');
    const stateRefresh = refreshSpacetimeState(reason || 'idle_resume');
    stateRefresh.catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
    connectDirectVideo();
    publishCurrentStreamFocus(reason || 'idle_resume');
    requestServerRecoveryDebounced(`${reason || 'idle_resume'}_recover`, true);
	    publishStreamDebug();
	    clientLog('viewer_idle_resumed', reason || 'idle_resume');
	    const resumeFlow = startActivationResumeFlow(reason || 'idle_resume', 'idle_resume');
    if (resumeFlow) resumeFlow.lifecycleResumeStarted = true;
	    return true;
	  }

  function layoutViewportRect() {
    const fallbackWidth = Math.max(1, Math.round(window.innerWidth || document.documentElement.clientWidth || 1));
    const fallbackHeight = Math.max(1, Math.round(window.innerHeight || document.documentElement.clientHeight || 1));
    return { width: fallbackWidth, height: fallbackHeight, offsetLeft: 0, offsetTop: 0 };
  }

  function visualViewportRect() {
    const fallback = layoutViewportRect();
    if (window.visualViewport) {
      return {
        width: Math.max(1, Math.round(window.visualViewport.width || fallback.width)),
        height: Math.max(1, Math.round(window.visualViewport.height || fallback.height)),
        offsetLeft: Math.round(window.visualViewport.offsetLeft || 0),
        offsetTop: Math.round(window.visualViewport.offsetTop || 0)
      };
    }
    return fallback;
  }

	  function keyboardLikelyOpen(layout, visual) {
	    const active = document.activeElement;
	    const inputFocused = active && (active === codeDigits || codeDialog.contains(active));
	    const dialogVisible = codeDialogOpen && !codeDialog.hidden;
	    return Boolean(
	      codeDialogOpen &&
	      (inputFocused || dialogVisible) &&
	      visual.height > 0 &&
	      layout.height - visual.height >= Math.max(120, layout.height * 0.18)
	    );
  }

  function stableStageViewportRect() {
    const layout = layoutViewportRect();
    const visual = visualViewportRect();
    const keyboardOpen = keyboardLikelyOpen(layout, visual);
    if (!stableViewport || !keyboardOpen) {
      stableViewport = {
        width: Math.max(layout.width, visual.width),
        height: Math.max(layout.height, visual.height),
        offsetLeft: keyboardOpen ? 0 : visual.offsetLeft,
        offsetTop: keyboardOpen ? 0 : visual.offsetTop
      };
    } else {
      stableViewport = {
        width: Math.max(stableViewport.width, layout.width),
        height: Math.max(stableViewport.height, layout.height),
        offsetLeft: 0,
        offsetTop: 0
      };
    }
    document.body.classList.toggle('keyboard-active', keyboardOpen);
    return keyboardOpen ? stableViewport : visual;
  }

  function viewportHeight() {
    return stableStageViewportRect().height;
  }

  function updateViewportVars() {
    const stageViewport = stableStageViewportRect();
    const dialogViewport = visualViewportRect();
    document.documentElement.style.setProperty('--ticket-stage-height', `${stageViewport.height}px`);
    document.documentElement.style.setProperty('--ticket-viewport-width', `${stageViewport.width}px`);
    document.documentElement.style.setProperty('--ticket-viewport-height', `${stageViewport.height}px`);
    document.documentElement.style.setProperty('--ticket-hotspot-width', `${stageViewport.width * 0.5}px`);
    document.documentElement.style.setProperty('--ticket-hotspot-height', `${stageViewport.height * 0.25}px`);
    document.documentElement.style.setProperty('--ticket-viewport-left', `${stageViewport.offsetLeft}px`);
    document.documentElement.style.setProperty('--ticket-viewport-top', `${stageViewport.offsetTop}px`);
    document.documentElement.style.setProperty('--ticket-dialog-width', `${dialogViewport.width}px`);
    document.documentElement.style.setProperty('--ticket-dialog-height', `${dialogViewport.height}px`);
    document.documentElement.style.setProperty('--ticket-dialog-left', `${dialogViewport.offsetLeft}px`);
    document.documentElement.style.setProperty('--ticket-dialog-top', `${dialogViewport.offsetTop}px`);
  }

  function updateDetailsReveal() {
    if (controlCodeDialogScrollLock && controlCodeDialogScrollLock.active) return;
    const height = viewportHeight();
    const revealed = window.scrollY >= Math.max(1, height * 0.82);
    document.body.classList.toggle('details-visible', revealed);
    noteExperimentalMediaStreamRegionVisibility(
      !revealed,
      revealed ? 'details_visible' : 'details_hidden'
    );
    // The panel naturally follows the full-height stream. Its contents must
    // remain available as soon as the user scrolls to them; details-visible is
    // only a stream-stage presentation state, never an accessibility gate.
    if (panel) panel.setAttribute('aria-hidden', 'false');
    updateControlCodeSubmitAvailability();
  }

  function keepFirstScreenPinned(force) {
    if (force) {
      document.body.classList.remove('details-visible');
      if (panel) panel.setAttribute('aria-hidden', 'false');
    }
    updateDetailsReveal();
  }

  function checkServerVersion(payload) {
    const serverVersion = payload && payload.serverVersion;
    const serverAssetVersion = String(payload && payload.assetVersion || '').trim();
    if (serverAssetVersion && assetVersion && serverAssetVersion !== assetVersion) {
      if (serverVersionReloadTarget) return false;
      serverVersionReloadTarget = serverAssetVersion;
      const next = new URL(location.href);
      next.searchParams.set('v', serverAssetVersion);
      location.replace(next.toString());
      return false;
    }
    if (!serverVersion || serverVersion === pageVersion) return true;
    if (!String(serverVersion).startsWith('ticket-remote-')) return true;
    if (serverVersionReloadTarget) return false;
    serverVersionReloadTarget = String(serverVersion);
    const next = new URL(location.href);
    next.searchParams.set('v', serverVersion);
    location.replace(next.toString());
    return false;
  }

  function normalizeAssetVersionURL() {
    if (!assetVersion || !window.history || typeof history.replaceState !== 'function') return;
    try {
      const next = new URL(location.href);
      if (next.searchParams.get('v') === assetVersion) return;
      next.searchParams.set('v', assetVersion);
      history.replaceState(history.state, document.title, next.toString());
    } catch (_) {}
  }

  document.body.dataset.videoPath = 'https-h264';

  function appendStreamURLParam(url, key, value) {
    if (value === null || value === undefined || value === '') return;
    url.searchParams.set(key, String(value).slice(0, 120));
  }

  function streamURL(reason) {
    const url = new URL('/api/v1/stream', location.href);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const now = performance.now();
    appendStreamURLParam(url, 'page_version', pageVersion);
    appendStreamURLParam(url, 'asset_version', assetVersion);
    appendStreamURLParam(url, 'visibility', document.visibilityState);
    const resuming = activeResumeFlow && !activeResumeFlow.done ? activeResumeFlow : null;
    const restoreReason = resuming
      ? resuming.reason || resuming.trigger || reason || 'resume'
      : reason || (lastHiddenAt > 0 || fallbackFrameAvailable || hasRenderedFrame ? 'old_page_resume' : 'cold_open');
    appendStreamURLParam(url, 'restore_reason', restoreReason);
    appendStreamURLParam(url, 'recovery_id', resuming && resuming.id);
    appendStreamURLParam(url, 'frame_age_ms', lastFrameAt > 0 ? Math.round(now - lastFrameAt) : '');
    appendStreamURLParam(url, 'hidden_age_ms', lastHiddenAt > 0 ? Math.round(now - lastHiddenAt) : '');
    appendStreamURLParam(url, 'has_frame', hasRenderedFrame ? '1' : '0');
    appendStreamURLParam(url, 'configured', configured ? '1' : '0');
    appendStreamURLParam(url, 'open_seq', videoSocketOpenSeq);
    return url.toString();
  }

  function setConnected(text) {
    connectionState.textContent = text;
  }

  function videoSocketProtocols() {
    return startupRunOrigin ? ['ticket.video.v1', startupRunOrigin] : ['ticket.video.v1'];
  }

  function safeWebSocket(url, label, protocols) {
    if (typeof WebSocket !== 'function') {
      reportClientFault('websocket_unavailable', label || url);
      return null;
    }
    try {
      return new WebSocket(url, protocols);
    } catch (error) {
      reportClientFault('websocket_create_failed', `${label || url}:${error && error.message || 'create failed'}`);
      return null;
    }
  }

  function setExperimentalMediaStatus(status) {
    experimentalMediaState.status = String(status || 'Izslēgts');
  }

  function experimentalHDRPreferenceStatus(snapshot) {
    const enabled = Boolean(snapshot && snapshot.enabled);
    switch (String(snapshot && snapshot.phase || 'default')) {
    case 'saving':
      return 'Saglabā HDR izvēli kontā…';
    case 'saved':
      return 'HDR izvēle saglabāta; gaida konta apstiprinājumu.';
    case 'failed':
      return 'HDR izvēle darbojas šajā sesijā, bet kontā netika saglabāta.';
    case 'synced':
      return `Saglabātais HDR iestatījums: ${enabled ? 'ieslēgts' : 'izslēgts'}.`;
    default:
      return 'Saglabātais HDR iestatījums: izslēgts.';
    }
  }

  function experimentalHDREngineStatus(engine, options) {
    options = options || {};
    if (options.saving) return 'Saglabā HDR apstrādes vietu kontā…';
    if (engine === CLIENT_HDR_ENGINE) {
      if (!experimentalClientCapabilityAllowed) return 'Pārlūka HDR šim kontam nav pieejams.';
      if (!experimentalClientCapability.supported) return 'Pārlūka HDR šajā ierīcē nav pieejams; redzama parastā straume.';
      return `HDR apstrāde: šī pārlūkprogramma (WebGPU, ${experimentalMediaState.displayBoost}×).`;
    }
    return 'HDR apstrāde: šī pārlūkprogramma.';
  }

  function experimentalHDRBoostStatus(snapshot) {
    const boost = normalizeClientHDRDisplayBoost(snapshot && snapshot.boost);
    switch (String(snapshot && snapshot.phase || 'default')) {
    case 'saving':
      return `Saglabā pārlūka HDR ${boost}× spilgtumu kontā…`;
    case 'saved':
      return `Pārlūka HDR ${boost}× saglabāts; gaida konta apstiprinājumu.`;
    case 'failed':
      return `Pārlūka HDR paliek ${boost}× šajā sesijā, bet kontā netika saglabāts.`;
    case 'synced':
      return `Saglabātais pārlūka HDR spilgtums: ${boost}×.`;
    default:
      return `Pārlūka HDR spilgtums: ${boost}×.`;
    }
  }

  const clientHDRDiagnosticMetricEvents = new Set([
    'settlement_started',
    'settlement_deadline_exceeded',
    'compositor_settlement_started',
    'compositor_settlement_result',
    'gpu_completion_timeout',
    'presentation_holdover',
    'holdover_release_deferred',
    'stream_region_visibility'
  ]);

  function reportClientHDRMetric(event, detail) {
    const recoveryAttempt = foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)
      ? experimentalMediaForegroundRecovery
      : null;
    const controllerSnapshot = experimentalClientHDRController &&
      typeof experimentalClientHDRController.snapshot === 'function'
      ? experimentalClientHDRController.snapshot()
      : null;
    const metric = Object.assign({
      assetVersion,
      engine: CLIENT_HDR_ENGINE,
      pipeline: CLIENT_HDR_PIPELINE,
      lifecycleGeneration: experimentalMediaLifecycleGeneration,
      canvasGeneration: experimentalMediaCanvasGeneration,
      rendererGeneration: Number(controllerSnapshot && controllerSnapshot.rendererGeneration || 0),
      attemptId: Number(recoveryAttempt && recoveryAttempt.id || 0),
      recoveryPhase: String(recoveryAttempt && recoveryAttempt.phase || '').slice(0, 40),
      triggerSet: String(recoveryAttempt && recoveryAttempt.triggers && recoveryAttempt.triggers.join(',') || '').slice(0, 120),
      streamEpoch: Number(lastRenderedFrameEpoch || 0),
      streamSequence: Number(lastRenderedFrameSequence || 0),
      retryOrdinal: Number(recoveryAttempt && recoveryAttempt.retryOrdinal || 0),
      startReason: String(experimentalMediaLastStartReason || 'unknown').slice(0, 80)
    }, detail || {});
    if (typeof performance !== 'undefined' && performance.memory && Number.isFinite(Number(performance.memory.usedJSHeapSize))) {
      metric.jsHeapBytes = Math.max(0, Math.round(Number(performance.memory.usedJSHeapSize)));
    }
    if (event === 'presentation_holdover' || event === 'holdover_release_deferred' ||
      event === 'presented' || event === 'first_presented' || event === 'surface_transition') {
      renderTicketActionV3Controls(currentState);
    }
    syncExperimentalClientHDRHoldoverNotice(event);
    if (event === 'fallback' || event === 'session_summary') {
      observeControlCodeHDRPresentationMetric(event, metric);
    }
    if (event === 'fallback') {
      clientHDRMeasurement('experimental_media_fallback', undefined, undefined,
        Object.assign({ phase: event, engine: CLIENT_HDR_ENGINE }, metric, {
          reason: String(metric.reason || 'client_hdr_failed').slice(0, 80)
        }));
      return;
    }
    if (event === 'first_presented') {
      if (!completeExperimentalMediaForegroundRecovery('first_presented')) {
        // The renderer may finish after a suspended attempt's wall budget or
        // after its authority was superseded. Never leave that unowned canvas
        // visible and never report it as a successful foreground recovery.
        const unownedSnapshot = experimentalClientHDRController &&
          typeof experimentalClientHDRController.snapshot === 'function'
          ? experimentalClientHDRController.snapshot()
          : null;
        if (unownedSnapshot && unownedSnapshot.active) {
          closeExperimentalMedia({
            keepEnabled: true,
            status: 'Parastā straume — HDR atjaunošana pārsniedza termiņu.'
          });
          experimentalMediaState.enabled = true;
        }
        return;
      }
      experimentalMediaResumeRetryArmed = false;
      observeControlCodeHDRPresentationMetric(event, metric);
      if (experimentalMediaRendererRetryTimer) {
        clearTimeout(experimentalMediaRendererRetryTimer);
        experimentalMediaRendererRetryTimer = null;
      }
      clientHDRMeasurement(
        'experimental_hdr_first_image_shown',
        metric.firstShownMillis,
        metric.displayReadyMillis,
        Object.assign({ phase: event }, metric)
      );
      return;
    }
    if (event === 'presented') {
      if (metric.surfaceVisible && !metric.visualHoldover) {
        if (document.body) document.body.dataset.experimentalMedia = 'hdr-client-webgpu-preview';
        setExperimentalMediaStatus(`HDR pārlūkā — ${experimentalMediaState.displayBoost}× WebGPU`);
      }
      observeControlCodeHDRPresentationMetric(event, metric);
      experimentalClientHDRMetricFrames += 1;
      if (experimentalClientHDRMetricFrames % 30 !== 0) return;
    }
    if (event === 'gpu_completion') {
      experimentalClientHDRGPUCompletionFrames += 1;
      if (experimentalClientHDRGPUCompletionFrames % 30 !== 0) return;
    }
    if (event === 'paint_wait_timeout' || event === 'paint_wait_failed' || event === 'renderer_init_timeout') {
      experimentalClientHDRPaintWaitFailures += 1;
      if (experimentalClientHDRPaintWaitFailures !== 1 && experimentalClientHDRPaintWaitFailures % 30 !== 0) return;
      clientHDRMeasurement('experimental_hdr_diagnostic', undefined, undefined,
        Object.assign({ phase: event, engine: CLIENT_HDR_ENGINE }, metric));
      return;
    }
    if (event === 'frame_clone_failed') {
      experimentalClientHDRCloneFailures += 1;
      if (experimentalClientHDRCloneFailures !== 1 && experimentalClientHDRCloneFailures % 30 !== 0) return;
      clientHDRMeasurement('experimental_hdr_diagnostic', undefined, undefined,
        Object.assign({ phase: event, engine: CLIENT_HDR_ENGINE }, metric));
      return;
    }
    if (clientHDRDiagnosticMetricEvents.has(event)) {
      if (event === 'settlement_started' && recoveryAttempt) {
        reportExperimentalMediaForegroundRecovery(recoveryAttempt, 'settling', event);
      }
      clientHDRMeasurement('experimental_hdr_diagnostic', undefined, metric.displayReadyMillis,
        Object.assign({ phase: event, engine: CLIENT_HDR_ENGINE }, metric));
      return;
    }
    const metricEvent = {
      edr_activation_presented: 'experimental_hdr_activation_presented',
      renderer_ready: 'experimental_hdr_renderer_ready',
      presented: 'experimental_hdr_presented',
      session_summary: 'experimental_hdr_session_summary',
      surface_transition: 'experimental_hdr_surface_transition',
      surface_reset: 'experimental_hdr_surface_reset',
      boost_changed: 'experimental_hdr_boost_changed',
      boost_change_failed: 'experimental_hdr_boost_failed',
      gpu_completion: 'experimental_hdr_gpu_completion'
    }[event];
    if (metricEvent) clientHDRMeasurement(metricEvent, undefined, metric.displayReadyMillis, Object.assign({ phase: event }, metric));
  }

  function refreshExperimentalClientCapability() {
    experimentalClientCapability = clientHDRCapability(window);
    experimentalMediaState.engineStatus = experimentalHDREngineStatus(experimentalMediaState.engine);
    return experimentalClientCapability;
  }

  function prepareExperimentalMediaCanvas(width, height, contextKind, options) {
    if (!experimentalMediaCanvas || !experimentalMediaCanvas.parentNode) return null;
    options = options || {};
    const requestedKind = String(contextKind || '');
    const replaceForKind = experimentalMediaCanvasContextKind && requestedKind &&
      experimentalMediaCanvasContextKind !== requestedKind;
    const replaceForLifecycle = Boolean(options.forceCanvasReset) &&
      experimentalMediaCanvasResetGeneration !== experimentalMediaLifecycleGeneration;
    if (replaceForKind || replaceForLifecycle) {
      const replacement = experimentalMediaCanvas.cloneNode(false);
      if (replacement.style && typeof replacement.style.removeProperty === 'function') {
        replacement.style.removeProperty('dynamic-range-limit');
      }
      replacement.hidden = true;
      delete replacement.dataset.clientHdrSurface;
      delete replacement.dataset.clientHdrSurfaceReason;
      replacement.setAttribute('aria-hidden', 'true');
      experimentalMediaCanvas.replaceWith(replacement);
      experimentalMediaCanvas = replacement;
      experimentalMediaCanvasGeneration += 1;
      experimentalMediaCanvasContextKind = '';
      if (replaceForLifecycle) experimentalMediaCanvasResetGeneration = experimentalMediaLifecycleGeneration;
      reportClientHDRMetric('surface_reset', {
        reason: String(options.reason || (replaceForLifecycle ? 'lifecycle' : 'context_kind')).slice(0, 80),
        canvasReplaced: true
      });
    }
    experimentalMediaCanvas.width = Math.max(1, Math.round(Number(width || canvas.width || 1)));
    experimentalMediaCanvas.height = Math.max(1, Math.round(Number(height || canvas.height || 1)));
    if (requestedKind) experimentalMediaCanvasContextKind = requestedKind;
    return experimentalMediaCanvas;
  }

  function showExperimentalClientHDRHoldoverNotice() {
    if (!hdrHoldoverNotice) return false;
    hdrHoldoverNotice.hidden = false;
    return true;
  }

  function hideExperimentalClientHDRHoldoverNotice() {
    if (!hdrHoldoverNotice) return false;
    hdrHoldoverNotice.hidden = true;
    return true;
  }

  function syncExperimentalClientHDRHoldoverNotice(event) {
    if (event === 'presentation_holdover' || event === 'holdover_release_deferred') {
      return showExperimentalClientHDRHoldoverNotice();
    }
    if (event === 'presented' || event === 'first_presented' ||
      event === 'fallback' || event === 'session_summary') {
      return hideExperimentalClientHDRHoldoverNotice();
    }
    return false;
  }

  function showExperimentalClientHDRSurface(visible, reason) {
    handleControlCodeHDRSurfaceChange(Boolean(visible), reason);
    if (!experimentalMediaCanvas) return;
    const current = experimentalMediaState.enabled && experimentalMediaState.engine === CLIENT_HDR_ENGINE;
    if (!current) {
      hideExperimentalClientHDRHoldoverNotice();
      experimentalMediaCanvas.hidden = true;
      delete experimentalMediaCanvas.dataset.clientHdrSurface;
      delete experimentalMediaCanvas.dataset.clientHdrSurfaceReason;
      experimentalMediaCanvas.setAttribute('aria-hidden', 'true');
      return;
    }
    experimentalMediaCanvas.hidden = false;
    experimentalMediaCanvas.dataset.clientHdrSurface = visible ? 'visible' : 'standby';
    experimentalMediaCanvas.dataset.clientHdrSurfaceReason = String(reason || (visible ? 'fresh' : 'fallback')).slice(0, 80);
    experimentalMediaCanvas.setAttribute('aria-hidden', visible ? 'false' : 'true');
    if (!visible) hideExperimentalClientHDRHoldoverNotice();
  }

  function controlCodeExactHDRResultVisible() {
    return Boolean(
      document.body && document.body.classList &&
      document.body.classList.contains('control-code-result-visible') &&
      codeResultArea && codeResultArea.dataset.presentation === 'exact-hdr'
    );
  }

  function experimentalHDRSurfacePresentationAllowed() {
    if (!document.body || !document.body.classList) return false;
    return !document.body.classList.contains('control-code-result-visible') ||
      controlCodeExactHDRResultVisible();
  }

  function noteExperimentalMediaStreamRegionVisibility(visible, reason) {
    const next = Boolean(visible);
    experimentalMediaStreamRegionVisible = next;
    if (document.body && document.body.dataset) {
      document.body.dataset.clientHdrStreamRegion = next ? 'visible' : 'out-of-view';
    }
    const controller = experimentalClientHDRController;
    if (!controller || typeof controller.setStreamRegionVisible !== 'function') return false;
    return controller.setStreamRegionVisible(next, reason);
  }

  function clientHDRHoldoverReleaseAllowed(presentation) {
    const epoch = Number(presentation && presentation.epoch || 0);
    const sequence = Number(presentation && presentation.sequence || 0);
    const presentationOrdinal = Number(presentation && presentation.presentationOrdinal || 0);
    const freshness = currentRenderedFreshness(performance.now());
    const status = freshStreamStatus(performance.now());
    const spacetimeAuthorityCurrent = !usesDirectSpacetimeAuth() ||
      (spacetimeStateFresh === true && spacetimeClientStatus === 'live');
    return Boolean(
      document.visibilityState === 'visible' && viewerIsForeground() &&
      !experimentalMediaLifecycleArmed &&
      !idleDisconnected && !streamUnsupported &&
      window.navigator.onLine !== false && spacetimeAuthorityCurrent &&
      videoWs && videoWs.readyState === WebSocket.OPEN &&
      status && status.phoneDesired !== false && status.phoneConnected !== false &&
      String(status.phoneStreamState || '') === 'streaming' &&
      Number(status.activeVideoClients || 0) > 0 && !streamStatusStale(status) &&
      freshness.liveLabeled && epoch > 0 && sequence > 0 && presentationOrdinal > 0 &&
      Number(lastRenderedFrameEpoch || 0) === epoch &&
      Number(lastRenderedFrameSequence || 0) === sequence &&
      Number(lastRenderedPresentationOrdinal || 0) === presentationOrdinal &&
      (Number(currentStreamEpoch || 0) <= 0 || Number(currentStreamEpoch) === epoch)
    );
  }

  function experimentalMediaDocumentHasFocus() {
    if (document.visibilityState !== 'visible') return false;
    if (typeof document.hasFocus !== 'function') return true;
    try {
      return document.hasFocus();
    } catch (_) {
      return false;
    }
  }

  function requestExperimentalHDRPresentationRegionRecovery(reason, options) {
    options = options || {};
    if (!experimentalMediaPreferenceController.enabled ||
      experimentalMediaState.engine !== CLIENT_HDR_ENGINE) return false;
    experimentalMediaPresentationRecoveryPending = true;
    experimentalMediaPresentationRecoveryReason = String(
      reason || experimentalMediaPresentationRecoveryReason || 'stream_region_visible'
    ).slice(0, 80);
    if (experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() ||
      document.visibilityState !== 'visible' ||
      controlCodeHDRFreezeTargetActive()) return false;
    experimentalMediaPresentationRecoveryPending = false;
    experimentalMediaState.enabled = true;
    experimentalMediaResumeRetryArmed = true;
    return beginExperimentalMediaForegroundRecovery(
      experimentalMediaPresentationRecoveryReason,
      {
        forceCanvasReset: true,
        foregroundConfirmed: Boolean(options.foregroundConfirmed)
      }
    );
  }

  function synchronizeExperimentalHDRSurfaceRegion(blocked, reason, options) {
    options = options || {};
    const nextBlocked = Boolean(blocked);
    const regionReason = String(
      reason || (nextBlocked ? 'hdr_surface_occluded' : 'stream_region_visible')
    ).slice(0, 80);
    if (nextBlocked) {
      experimentalMediaPresentationRecoveryPending = Boolean(
        experimentalMediaPreferenceController.enabled &&
        experimentalMediaState.engine === CLIENT_HDR_ENGINE
      );
      experimentalMediaPresentationRecoveryReason = regionReason;
      if (experimentalMediaPresentationRegionBlocked) return true;
      experimentalMediaPresentationRegionBlocked = true;
      experimentalMediaPresentationRegionGeneration += 1;
      invalidateExperimentalMediaForegroundRecovery('presentation_region_blocked');
      if (experimentalMediaState.enabled && experimentalMediaState.engine === CLIENT_HDR_ENGINE) {
        closeExperimentalMedia({
          keepEnabled: true,
          status: 'Parastā straume — HDR apturēts, kamēr biļete nav redzama.'
        });
        experimentalMediaState.enabled = true;
      }
      return true;
    }
    const wasBlocked = experimentalMediaPresentationRegionBlocked;
    if (!wasBlocked && !experimentalMediaPresentationRecoveryPending) return false;
    if (wasBlocked) {
      experimentalMediaPresentationRegionBlocked = false;
      experimentalMediaPresentationRegionGeneration += 1;
    }
    return requestExperimentalHDRPresentationRegionRecovery(regionReason, {
      // Only an explicit scroll/dismissal caller may supply foreground
      // evidence. Automatic control-priority or server-state transitions must
      // still wait for focus or the bounded paint confirmation.
      foregroundConfirmed: Boolean(wasBlocked && options.foregroundConfirmed)
    });
  }

  function ensureExperimentalClientHDRController() {
    if (experimentalClientHDRController) return experimentalClientHDRController;
    experimentalClientHDRController = new ClientHDRController({
      maxSequenceLag: 1,
      maxAgeDeltaMillis: 250,
      canRevealSurface: () => Boolean(
        experimentalMediaState.enabled &&
        experimentalMediaState.engine === CLIENT_HDR_ENGINE &&
        !experimentalMediaPresentationRegionBlocked &&
        !experimentalMediaPresentationRecoveryPending &&
        experimentalHDRSurfacePresentationAllowed()
      ),
      onSurface: (visible, _presented, reason) => {
        if (!experimentalMediaCanvas) return;
        const current = experimentalMediaState.enabled && experimentalMediaState.engine === CLIENT_HDR_ENGINE;
        showExperimentalClientHDRSurface(visible, reason);
        if (visible && current) {
          if (document.body) document.body.dataset.experimentalMedia = 'hdr-client-webgpu-preview';
          setExperimentalMediaStatus(`HDR pārlūkā — ${experimentalMediaState.displayBoost}× WebGPU`);
        } else if (current && document.body) {
          document.body.dataset.experimentalMedia = 'fallback-sdr';
          setExperimentalMediaStatus('Parastā straume — gaida svaigu HDR kadru…');
        }
      },
      onStatus: (status, reason) => {
        if (status === 'starting') setExperimentalMediaStatus('Sagatavo HDR pārlūkā…');
        if (status === 'ready') {
          setExperimentalMediaStatus('Gaida svaigu HDR kadru pārlūkā…');
          if (hasRenderedFrame && typeof streamHasFreshRenderedFrame === 'function' && streamHasFreshRenderedFrame()) {
            offerCurrentSDRFrameToClientHDR('renderer_ready_sdr_seed');
          }
        }
        if (status === 'failed') {
          experimentalClientHDRFailed = true;
          setExperimentalMediaStatus('Parastā straume — pārlūka HDR nav pieejams.');
          if (document.body) document.body.dataset.experimentalMedia = 'fallback-sdr';
          // A failure inside an in-progress recovery consumes that attempt's
          // one fresh-surface retry. A device loss after first_presented no
          // longer has an owning attempt, so start a new bounded coordinator
          // attempt instead of waiting for an unrelated lifecycle event.
          if (!scheduleExperimentalMediaActiveFailureRecovery(reason || 'renderer_failed')) {
            scheduleExperimentalMediaRendererRetry(reason || 'renderer_failed');
          }
        }
      },
      onRecoveryRequest: (reason) => {
        if (reason === 'paint_wait_timeout') offerCurrentSDRFrameToClientHDR(reason);
      },
      canReleaseHoldover: clientHDRHoldoverReleaseAllowed,
      onMetric: reportClientHDRMetric
    });
    experimentalClientHDRController.setStreamRegionVisible(experimentalMediaStreamRegionVisible);
    return experimentalClientHDRController;
  }

  function connectExperimentalClientHDR(options) {
    options = options || {};
    if (!experimentalMediaState.enabled || experimentalMediaState.engine !== CLIENT_HDR_ENGINE) return false;
    if (document.visibilityState !== 'visible' || experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() || controlCodeHDRFreezeTargetActive()) return false;
    const existingController = experimentalClientHDRController;
    const existingSnapshot = existingController && existingController.snapshot();
    if (existingSnapshot && existingSnapshot.active) {
      existingController.setDocumentVisible(true);
      return true;
    }
    hideExperimentalMediaCanvas();
    experimentalMediaPipeline = CLIENT_HDR_PIPELINE;
    refreshExperimentalClientCapability();
    if (!experimentalClientCapabilityAllowed || !experimentalClientCapability.supported) {
      setExperimentalMediaStatus('Parastā straume — pārlūka HDR šajā ierīcē nav pieejams.');
      if (document.body) document.body.dataset.experimentalMedia = 'fallback-sdr';
      return false;
    }
    if (experimentalClientHDRFailed) {
      setExperimentalMediaStatus('Parastā straume — izslēdz un ieslēdz HDR, lai mēģinātu vēlreiz.');
      return false;
    }
    const width = Math.max(1, Number(canvas.width || streamSize.width || 1));
    const height = Math.max(1, Number(canvas.height || streamSize.height || 1));
    const hdrCanvas = prepareExperimentalMediaCanvas(width, height, 'webgpu', {
      forceCanvasReset: Boolean(options.forceCanvasReset),
      reason: options.reason
    });
    if (!hdrCanvas) {
      experimentalClientHDRFailed = true;
      setExperimentalMediaStatus('Parastā straume — HDR virsma nav pieejama.');
      scheduleExperimentalMediaRendererRetry('hdr_canvas_unavailable');
      return false;
    }
    experimentalClientHDRMetricFrames = 0;
    experimentalClientHDRGPUCompletionFrames = 0;
    experimentalClientHDRPaintWaitFailures = 0;
    experimentalClientHDRCloneFailures = 0;
    ensureExperimentalClientHDRController().setDocumentVisible(document.visibilityState === 'visible');
    return ensureExperimentalClientHDRController().start({
      canvas: hdrCanvas,
      width,
      height,
      boost: experimentalMediaState.displayBoost
    });
  }

  function offerCurrentSDRFrameToClientHDR(reason) {
    const controller = experimentalClientHDRController;
    if (!controller || !controller.snapshot().active || !hasRenderedFrame ||
      canvas.width <= 0 || canvas.height <= 0 || typeof VideoFrame !== 'function' ||
      experimentalMediaPresentationRegionBlocked || experimentalMediaPresentationRecoveryPending ||
      !experimentalHDRSurfacePresentationAllowed() ||
      controlCodeHDRFreezeTargetActive() ||
      typeof streamHasFreshRenderedFrame !== 'function' || !streamHasFreshRenderedFrame() ||
      !(Number(lastRenderedFrameEpoch || 0) > 0) || !(Number(lastRenderedFrameSequence || 0) > 0) ||
      !(Number(lastRenderedPresentationOrdinal || 0) > 0)) return false;
    const offeredAt = performance.now();
    const timestamp = Math.max(0, Math.round(Number(lastRenderedFrameTimestamp || Date.now() * 1000)));
    const metadata = {
      epoch: Number(lastRenderedFrameEpoch || currentStreamEpoch || 0),
      sequence: Number(lastRenderedFrameSequence || 0),
      presentationOrdinal: Number(lastRenderedPresentationOrdinal || 0),
      timestamp,
      visualAgeMillis: Math.max(0, lastRenderedVisualAge(offeredAt)),
      renderedAt: offeredAt,
      offeredAt,
      offeredWallMillis: Date.now(),
      redrawReason: String(reason || 'boost_changed')
    };
    const offered = offerClientHDRCanvasFrame(controller, canvas, metadata, window);
    if (!offered) {
      clientLog('state_failed', 'hdr_boost_redraw_failed');
    }
    return offered;
  }

  function syncExperimentalMediaSelectors() {
    const engine = document.getElementById('experimentalMediaEngine');
    if (engine && engine.value !== experimentalMediaState.engine) {
      engine.value = experimentalMediaState.engine;
    }
    const boost = document.getElementById('experimentalMediaHDRBoost');
    const displayBoost = String(experimentalMediaState.displayBoost);
    if (boost && boost.value !== displayBoost) {
      boost.value = displayBoost;
    }
  }

  function applyExperimentalHDRBoost(value, meta) {
    const next = normalizeClientHDRDisplayBoost(value);
    experimentalMediaState.displayBoost = next;
    experimentalMediaState.engineStatus = experimentalHDREngineStatus(experimentalMediaState.engine);
    syncExperimentalMediaSelectors();
    if (!experimentalClientHDRController) return;
    const applied = experimentalClientHDRController.setDisplayBoost(next);
    const snapshot = experimentalClientHDRController.snapshot();
    if (applied && snapshot.active && hasRenderedFrame &&
      String(meta && meta.reason || '') !== 'default') {
      offerCurrentSDRFrameToClientHDR(meta && meta.reason);
    }
  }

  function observeExperimentalHDREngine(state) {
    const projection = state && state.memberHDREngine;
    const decision = clientHDREngineProjectionDecision(projection, experimentalMediaOwnerProjectionAvailable);
    const next = decision.engine;
    experimentalMediaEngineProjectionObserved = decision.ownerProjectionAvailable;
    experimentalMediaOwnerProjectionAvailable = decision.ownerProjectionAvailable;
    const changed = next !== experimentalMediaState.engine;
    experimentalMediaState.engineSaving = false;
    experimentalMediaState.engineStatus = experimentalHDREngineStatus(next);
    if (!changed) {
      syncExperimentalMediaSelectors();
      return;
    }
    const keepEnabled = experimentalMediaState.enabled;
    closeExperimentalMedia({ keepEnabled, status: keepEnabled ? 'Maina HDR apstrādes vietu…' : 'Izslēgts' });
    experimentalMediaState.engine = next;
    syncExperimentalMediaSelectors();
    if (keepEnabled) {
      experimentalMediaState.enabled = true;
      beginExperimentalMediaForegroundRecovery('engine_projection_changed', {
        forceCanvasReset: true
      });
    }
  }

  function observeExperimentalHDRBoost(state) {
    const projection = state && state.memberHDRBoost;
    const accountProjectionAvailable = Boolean(projection && projection.accountProjectionAvailable);
    experimentalMediaBoostProjectionObserved = accountProjectionAvailable;
    experimentalMediaAccountProjectionAvailable = accountProjectionAvailable;
    experimentalMediaState.boostSelectorAllowed = accountProjectionAvailable;
    if (!accountProjectionAvailable) {
      return;
    }
    experimentalHDRBoostPreferenceController.observe(projection.selectedDisplayBoost);
  }

  function chooseExperimentalHDRBoost(value) {
    if (!experimentalMediaState.boostSelectorAllowed || experimentalMediaState.engine !== CLIENT_HDR_ENGINE) return;
    experimentalHDRBoostPreferenceController.choose(value);
  }

  function applyExperimentalMediaPreference(enabled, meta) {
    const shouldEnable = Boolean(enabled && experimentalMediaCapabilityReady);
    if (!shouldEnable) {
      if (!enabled) invalidateExperimentalMediaForegroundRecovery('preference_disabled');
      experimentalMediaResumeRetryArmed = false;
      clearExperimentalMediaDynamicRangeRecovery();
      cancelExperimentalMediaStart();
      if (experimentalMediaState.enabled) {
        closeExperimentalMedia({ status: 'Izslēgts' });
      } else {
        experimentalMediaState.enabled = false;
      }
      return;
    }
    if (experimentalMediaState.enabled) return;
    experimentalClientHDRFailed = false;
    experimentalMediaResumeRetryArmed = true;
    // A real OFF -> ON transition owns a new WebKit presentation surface even
    // when it happens without a page lifecycle. Matching account projections
    // do not reach this branch because the preference is already enabled.
    experimentalMediaCanvasResetGeneration = -1;
    experimentalMediaState.enabled = true;
    const preferenceReason = String(meta && meta.reason || 'projection');
    const startReason = preferenceReason === 'user'
      ? 'preference_user_enable'
      : 'preference_projection_restore';
    beginExperimentalMediaForegroundRecovery(startReason, { forceCanvasReset: true });
  }

  function hideExperimentalMediaCanvas() {
    hideExperimentalClientHDRHoldoverNotice();
    if (experimentalMediaCanvas) {
      experimentalMediaCanvas.hidden = true;
      delete experimentalMediaCanvas.dataset.clientHdrSurface;
      delete experimentalMediaCanvas.dataset.clientHdrSurfaceReason;
      experimentalMediaCanvas.setAttribute('aria-hidden', 'true');
    }
    if (document.body) document.body.dataset.experimentalMedia = 'fallback-sdr';
  }

  function closeExperimentalMedia(options) {
    options = options || {};
    cancelExperimentalMediaStart();
    if (experimentalMediaActiveFailureRecoveryTimer) {
      clearTimeout(experimentalMediaActiveFailureRecoveryTimer);
      experimentalMediaActiveFailureRecoveryTimer = null;
    }
    if (experimentalMediaRendererRetryTimer) {
      clearTimeout(experimentalMediaRendererRetryTimer);
      experimentalMediaRendererRetryTimer = null;
    }
    if (experimentalClientHDRController) experimentalClientHDRController.dispose(options.status || 'disabled');
    experimentalMediaPipeline = '';
    hideExperimentalMediaCanvas();
    if (!options.keepEnabled) {
      experimentalMediaState.enabled = false;
      experimentalMediaResumeRetryArmed = false;
    }
    setExperimentalMediaStatus(options.status || 'Izslēgts');
  }

  function cancelExperimentalMediaStart() {
    const pending = experimentalMediaStartPending;
    experimentalMediaStartPending = null;
    experimentalMediaStartGeneration += 1;
    if (pending) {
      pending.cancelled = true;
      if (pending.fallbackTimer) clearTimeout(pending.fallbackTimer);
      const cancelPaint = window && window.cancelAnimationFrame;
      if (typeof cancelPaint === 'function') {
        for (const handle of pending.frameHandles) {
          try { cancelPaint(handle); } catch (_) {}
        }
      }
    }
    if (experimentalMediaCapabilityRetryTimer) {
      clearTimeout(experimentalMediaCapabilityRetryTimer);
      experimentalMediaCapabilityRetryTimer = null;
    }
  }

  function clearExperimentalMediaDynamicRangeRecovery() {
    const query = experimentalMediaDynamicRangeRecoveryQuery;
    const listener = experimentalMediaDynamicRangeRecoveryListener;
    experimentalMediaDynamicRangeRecoveryQuery = null;
    experimentalMediaDynamicRangeRecoveryListener = null;
    if (!query || !listener) return;
    try {
      if (typeof query.removeEventListener === 'function') query.removeEventListener('change', listener);
      else if (typeof query.removeListener === 'function') query.removeListener(listener);
    } catch (_) {}
  }

  function armExperimentalMediaDynamicRangeRecovery(options) {
    options = options || {};
    if (experimentalMediaDynamicRangeRecoveryListener) return true;
    if (!window || typeof window.matchMedia !== 'function') return false;
    let query;
    try { query = window.matchMedia('(dynamic-range: high)'); } catch (_) { return false; }
    if (!query) return false;
    const listener = (event) => {
      if ((!experimentalMediaPreferenceController.enabled && !experimentalMediaState.enabled) ||
        document.visibilityState !== 'visible') return;
      const available = Boolean(event && typeof event.matches === 'boolean'
        ? event.matches
        : query.matches);
      const trigger = available
        ? 'dynamic_range_capability_available'
        : 'dynamic_range_capability_unavailable';
      const attempt = experimentalMediaForegroundRecovery;
      if (!available && foregroundRecoveryCurrent(attempt)) {
        // A disappearing local HDR capability immediately revokes the current
        // surface and attempt. The replacement attempt may keep probing until
        // its bounded foreground deadline, but SDR remains authoritative.
        invalidateExperimentalMediaForegroundRecovery('dynamic_range_capability_unavailable');
      }
      beginExperimentalMediaForegroundRecovery(trigger, {
        forceCanvasReset: true
      });
    };
    experimentalMediaDynamicRangeRecoveryQuery = query;
    experimentalMediaDynamicRangeRecoveryListener = listener;
    try {
      if (typeof query.addEventListener === 'function') query.addEventListener('change', listener);
      else if (typeof query.addListener === 'function') query.addListener(listener);
      else {
        clearExperimentalMediaDynamicRangeRecovery();
        return false;
      }
    } catch (_) {
      clearExperimentalMediaDynamicRangeRecovery();
      return false;
    }
    if (query.matches && !options.onlyFutureChange) listener(query);
    return true;
  }

  function reportExperimentalMediaForegroundRecovery(attempt, phase, reason) {
    if (!attempt) return;
    const nextPhase = String(phase || 'unknown').slice(0, 40);
    const nextReason = String(reason || attempt.reason || 'foreground').slice(0, 80);
    if (attempt.reportedPhase === nextPhase && attempt.reportedReason === nextReason) return;
    attempt.phase = nextPhase;
    attempt.reportedPhase = nextPhase;
    attempt.reportedReason = nextReason;
    const snapshot = experimentalClientHDRController &&
      typeof experimentalClientHDRController.snapshot === 'function'
      ? experimentalClientHDRController.snapshot()
      : null;
    clientHDRMeasurement('experimental_hdr_diagnostic', undefined, undefined, {
      assetVersion,
      engine: CLIENT_HDR_ENGINE,
      pipeline: CLIENT_HDR_PIPELINE,
      phase: 'foreground_recovery',
      attemptId: Number(attempt.id || 0),
      recoveryPhase: nextPhase,
      triggerSet: String(attempt.triggers && attempt.triggers.join(',') || attempt.reason || '').slice(0, 120),
      reason: nextReason,
      versionOutcome: String(attempt.versionOutcome || 'pending').slice(0, 40),
      capabilityOutcome: String(attempt.capabilityOutcome || 'pending').slice(0, 40),
      streamEpoch: Number(lastRenderedFrameEpoch || 0),
      streamSequence: Number(lastRenderedFrameSequence || 0),
      baselineSDRRenderSerial: Number(attempt.baselineSDRRenderSerial || 0),
      sdrRenderSerial: Number(authoritativeSDRRenderSerial || 0),
      lifecycleGeneration: attempt.lifecycleGeneration,
      presentationRegionGeneration: attempt.presentationRegionGeneration,
      presentationRegionBlocked: experimentalMediaPresentationRegionBlocked,
      presentationRecoveryPending: experimentalMediaPresentationRecoveryPending,
      foregroundPaintConfirmed: Boolean(attempt.foregroundPaintConfirmed),
      foregroundStabilityWaitMillis: Math.max(
        0,
        Number(attempt.canvasNotBeforeWallAt || 0) - Date.now()
      ),
      canvasGeneration: experimentalMediaCanvasGeneration,
      rendererGeneration: Number(snapshot && snapshot.rendererGeneration || 0),
      presentationState: String(snapshot && snapshot.presentationState || '').slice(0, 40),
      surfaceVisible: Boolean(snapshot && snapshot.surfaceVisible),
      retryOrdinal: Number(attempt.retryOrdinal || 0),
      recoveryElapsedMillis: Math.max(0, Date.now() - Number(attempt.startedWallAt || Date.now())),
      recoveryRemainingMillis: Math.max(0, Number(attempt.deadlineWallAt || 0) - Date.now()),
      startReason: String(attempt.reason || 'foreground').slice(0, 80)
    });
  }

  function clearExperimentalMediaForegroundRecoveryTimer() {
    if (!experimentalMediaForegroundRecoveryTimer) return;
    clearTimeout(experimentalMediaForegroundRecoveryTimer);
    experimentalMediaForegroundRecoveryTimer = null;
  }

  function clearExperimentalMediaForegroundRecoveryDeadlineTimer() {
    if (!experimentalMediaForegroundRecoveryDeadlineTimer) return;
    clearTimeout(experimentalMediaForegroundRecoveryDeadlineTimer);
    experimentalMediaForegroundRecoveryDeadlineTimer = null;
  }

  function armExperimentalMediaForegroundRecoveryDeadline(attempt) {
    clearExperimentalMediaForegroundRecoveryDeadlineTimer();
    if (!foregroundRecoveryCurrent(attempt)) return false;
    const remaining = Math.max(0, Number(attempt.deadlineWallAt || 0) - Date.now());
    experimentalMediaForegroundRecoveryDeadlineTimer = setTimeout(() => {
      experimentalMediaForegroundRecoveryDeadlineTimer = null;
      if (!foregroundRecoveryCurrent(attempt)) return;
      failExperimentalMediaForegroundRecovery(attempt, 'foreground_deadline_exhausted');
    }, remaining);
    return true;
  }

  function foregroundRecoveryCurrent(attempt) {
    return Boolean(attempt && experimentalMediaForegroundRecovery === attempt &&
      attempt.id === experimentalMediaForegroundRecoverySequence && !attempt.cancelled &&
      attempt.presentationRegionGeneration === experimentalMediaPresentationRegionGeneration);
  }

  function invalidateExperimentalMediaForegroundRecovery(reason) {
    const attempt = experimentalMediaForegroundRecovery;
    clearExperimentalMediaForegroundRecoveryTimer();
    clearExperimentalMediaForegroundRecoveryDeadlineTimer();
    experimentalMediaForegroundRecovery = null;
    experimentalMediaForegroundRecoverySequence += 1;
    if (!attempt) return;
    attempt.cancelled = true;
    if (attempt.versionAbortController) {
      try { attempt.versionAbortController.abort(); } catch (_) {}
      attempt.versionAbortController = null;
    }
    reportExperimentalMediaForegroundRecovery(
      attempt,
      reason === 'lifecycle_backgrounded' ? 'backgrounded' : 'cancelled',
      reason || 'superseded'
    );
  }

  function completeExperimentalMediaForegroundRecovery(reason) {
    const attempt = experimentalMediaForegroundRecovery;
    if (!foregroundRecoveryCurrent(attempt)) return false;
    if (Date.now() >= Number(attempt.deadlineWallAt || 0)) {
      failExperimentalMediaForegroundRecovery(attempt, 'foreground_deadline_exhausted_before_present');
      return false;
    }
    reportExperimentalMediaForegroundRecovery(attempt, 'active', reason || 'presented');
    experimentalMediaForegroundRecoveredGeneration = attempt.lifecycleGeneration;
    experimentalMediaForegroundSuspensionGap = false;
    clearExperimentalMediaForegroundRecoveryTimer();
    clearExperimentalMediaForegroundRecoveryDeadlineTimer();
    experimentalMediaForegroundRecovery = null;
    attempt.cancelled = true;
    return true;
  }

  function failExperimentalMediaForegroundRecovery(attempt, reason) {
    if (!foregroundRecoveryCurrent(attempt)) return false;
    reportExperimentalMediaForegroundRecovery(attempt, 'safe_sdr', reason || 'foreground_recovery_failed');
    clearExperimentalMediaForegroundRecoveryTimer();
    clearExperimentalMediaForegroundRecoveryDeadlineTimer();
    if (attempt.versionAbortController) {
      try { attempt.versionAbortController.abort(); } catch (_) {}
      attempt.versionAbortController = null;
    }
    experimentalMediaForegroundRecovery = null;
    attempt.cancelled = true;
    closeExperimentalMedia({
      keepEnabled: true,
      status: 'Parastā straume — HDR atjaunošana neizdevās.'
    });
    experimentalMediaState.enabled = true;
    armExperimentalMediaDynamicRangeRecovery({ onlyFutureChange: true });
    return true;
  }

  function foregroundAttemptHasFreshSDR(attempt) {
    const renderedEpoch = Number(lastRenderedFrameEpoch || 0);
    const activeEpoch = Number(currentStreamEpoch || 0);
    return Boolean(
      foregroundRecoveryCurrent(attempt) &&
      hasRenderedFrame &&
      Number(authoritativeSDRRenderSerial || 0) > Number(attempt.baselineSDRRenderSerial || 0) &&
      renderedEpoch > 0 &&
      activeEpoch > 0 &&
      renderedEpoch === activeEpoch &&
      Number(lastRenderedFrameSequence || 0) > 0 &&
      typeof streamHasFreshRenderedFrame === 'function' && streamHasFreshRenderedFrame()
    );
  }

  function foregroundRecoveryRetryDelay(attempt) {
    const index = Math.min(
      Math.max(0, Number(attempt && attempt.retryIndex || 0)),
      experimentalMediaForegroundRecoveryRetryDelays.length - 1
    );
    if (attempt) attempt.retryIndex = index + 1;
    return experimentalMediaForegroundRecoveryRetryDelays[index];
  }

  function queueExperimentalMediaForegroundRecovery(attempt, delay) {
    if (!foregroundRecoveryCurrent(attempt) || experimentalMediaForegroundRecoveryTimer) return false;
    const remaining = Math.max(0, attempt.deadlineWallAt - Date.now());
    const boundedDelay = Math.min(remaining, Math.max(0, Number(delay || 0)));
    experimentalMediaForegroundRecoveryTimer = setTimeout(() => {
      experimentalMediaForegroundRecoveryTimer = null;
      reconcileExperimentalMediaForegroundRecovery(attempt);
    }, boundedDelay);
    return true;
  }

  async function checkExperimentalMediaForegroundVersion(attempt) {
    if (!foregroundRecoveryCurrent(attempt)) return false;
    const controller = typeof AbortController === 'function' ? new AbortController() : null;
    attempt.versionAbortController = controller;
    let timeout = null;
    try {
      const versionRequest = fetch('/api/v1/livez', {
        cache: 'no-store',
        credentials: 'same-origin',
        signal: controller ? controller.signal : undefined
      });
      const timeoutRequest = new Promise((_, reject) => {
        timeout = setTimeout(() => {
          try { if (controller) controller.abort(); } catch (_) {}
          reject(new Error('version_check_timeout'));
        }, experimentalMediaCapabilityFetchTimeoutMillis);
      });
      const response = await Promise.race([versionRequest, timeoutRequest]);
      if (!response.ok) {
        attempt.versionOutcome = 'server_unavailable';
        reportExperimentalMediaForegroundRecovery(attempt, 'version_check', 'version_server_unavailable');
        return false;
      }
      const payload = await response.json();
      if (!foregroundRecoveryCurrent(attempt)) return false;
      const serverAssetVersion = String(payload && payload.assetVersion || '').trim();
      const serverVersion = String(payload && payload.serverVersion || '').trim();
      const mismatchVersion = serverAssetVersion && assetVersion && serverAssetVersion !== assetVersion
        ? serverAssetVersion
        : (serverVersion && serverVersion !== pageVersion && serverVersion.startsWith('ticket-remote-')
          ? serverVersion
          : '');
      if (mismatchVersion) {
        attempt.versionOutcome = 'mismatch';
        let alreadyReloadedForVersion = false;
        try {
          alreadyReloadedForVersion = new URL(location.href).searchParams.get('v') === mismatchVersion;
        } catch (_) {}
        if (alreadyReloadedForVersion) {
          reportExperimentalMediaForegroundRecovery(attempt, 'safe_sdr', 'asset_version_mismatch_after_reload');
          invalidateExperimentalMediaForegroundRecovery('asset_version_mismatch_after_reload');
          return false;
        }
      }
      if (!checkServerVersion(payload)) {
        attempt.reloadRequested = true;
        invalidateExperimentalMediaForegroundRecovery('asset_version_mismatch');
        return false;
      }
      attempt.versionOutcome = 'match';
      attempt.versionChecked = true;
      return true;
    } catch (_) {
      if (foregroundRecoveryCurrent(attempt)) {
        attempt.versionOutcome = 'request_failed';
        reportExperimentalMediaForegroundRecovery(attempt, 'version_check', 'version_request_failed');
      }
      return false;
    } finally {
      if (timeout) clearTimeout(timeout);
      if (attempt.versionAbortController === controller) attempt.versionAbortController = null;
    }
  }

  async function reconcileExperimentalMediaForegroundRecovery(attempt) {
    if (!foregroundRecoveryCurrent(attempt) || attempt.reconcileRunning) return false;
    if (document.visibilityState !== 'visible' || !experimentalMediaPreferenceController.enabled) return false;
    if (experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() ||
      controlCodeHDRFreezeTargetActive()) return false;
    if (Date.now() >= attempt.deadlineWallAt) {
      failExperimentalMediaForegroundRecovery(attempt, 'foreground_deadline_exhausted');
      return false;
    }
    attempt.reconcileRunning = true;
    try {
      const activeController = experimentalClientHDRController;
      if (activeController && typeof activeController.checkSettlementDeadline === 'function') {
        activeController.checkSettlementDeadline('foreground_recovery');
      }
      if (!attempt.versionChecked) {
        reportExperimentalMediaForegroundRecovery(attempt, 'version_check', attempt.reason);
        if (!await checkExperimentalMediaForegroundVersion(attempt)) return false;
      }
      if (!foregroundRecoveryCurrent(attempt)) return false;
      if (!attempt.serverCapabilityChecked || !experimentalMediaCapabilityReady) {
        attempt.capabilityOutcome = 'server_pending';
        reportExperimentalMediaForegroundRecovery(attempt, 'capability_wait', 'server_capability');
        let capabilityResponse = null;
        try {
          capabilityResponse = await fetchExperimentalMediaCapability();
        } catch (_) {}
        if (!foregroundRecoveryCurrent(attempt)) return false;
        if (!capabilityResponse || !applyExperimentalMediaCapabilityPayload(
            capabilityResponse.response,
            capabilityResponse.payload,
            attempt.id
          )) {
          attempt.capabilityOutcome = 'server_unavailable';
          reportExperimentalMediaForegroundRecovery(attempt, 'capability_wait', 'server_capability_unavailable');
          return false;
        }
        attempt.capabilityOutcome = 'server_ready';
        attempt.serverCapabilityChecked = true;
      }
      const capability = refreshExperimentalClientCapability();
      if (!experimentalClientCapabilityAllowed || !capability.supported) {
        attempt.capabilityOutcome = 'browser_unavailable';
        reportExperimentalMediaForegroundRecovery(
          attempt,
          'capability_wait',
          'browser_capability_unavailable'
        );
        // WebKit can temporarily report any part of the local HDR stack as
        // absent while a suspended PWA is waking. Re-probe the complete local
        // contract until this attempt's wall deadline instead of latching one
        // early observation as a permanent failure.
        return false;
      }
      attempt.capabilityOutcome = 'ready';
      if (!foregroundAttemptHasFreshSDR(attempt)) {
        reportExperimentalMediaForegroundRecovery(attempt, 'fresh_sdr_wait', attempt.reason);
        return false;
      }
      if ((!experimentalMediaDocumentHasFocus() && !attempt.foregroundPaintConfirmed) ||
        Date.now() < Number(attempt.canvasNotBeforeWallAt || 0)) {
        reportExperimentalMediaForegroundRecovery(attempt, 'capability_wait', 'foreground_stability_wait');
        return false;
      }
      attempt.freshSDREpoch = Number(lastRenderedFrameEpoch || 0);
      attempt.freshSDRSequence = Number(lastRenderedFrameSequence || 0);
      const snapshot = experimentalClientHDRController && experimentalClientHDRController.snapshot();
      if (snapshot && snapshot.active) {
        reportExperimentalMediaForegroundRecovery(
          attempt,
          snapshot.presentationState === 'settling' ? 'settling' : 'initializing',
          attempt.reason
        );
        return true;
      }
      if (!attempt.canvasStarted) {
        attempt.canvasStarted = true;
        experimentalClientHDRFailed = false;
        experimentalMediaResumeRetryArmed = true;
        experimentalMediaCanvasResetGeneration = -1;
        reportExperimentalMediaForegroundRecovery(attempt, 'initializing', attempt.reason);
        scheduleExperimentalMediaStart(`foreground_recovery:${attempt.reason}`, {
          forceCanvasReset: true
        });
      } else if (experimentalClientHDRFailed && !experimentalMediaRendererRetryTimer &&
        !experimentalMediaStartPending) {
        failExperimentalMediaForegroundRecovery(attempt, 'renderer_retry_exhausted');
      } else if (!experimentalMediaStartPending && !experimentalMediaCapabilityRetryTimer &&
        !experimentalMediaRendererRetryTimer && !attempt.restartUsed) {
        attempt.restartUsed = true;
        attempt.canvasStarted = false;
        experimentalMediaCanvasResetGeneration = -1;
        reportExperimentalMediaForegroundRecovery(attempt, 'capability_wait', 'settled_start_retry');
      }
      return true;
    } finally {
      attempt.reconcileRunning = false;
      if (foregroundRecoveryCurrent(attempt)) {
        queueExperimentalMediaForegroundRecovery(attempt, foregroundRecoveryRetryDelay(attempt));
      }
    }
  }

  function beginExperimentalMediaForegroundRecovery(reason, options) {
    options = options || {};
    if (!experimentalMediaPreferenceController.enabled || document.visibilityState !== 'visible') return false;
    if (experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() ||
      controlCodeHDRFreezeTargetActive()) {
      experimentalMediaPresentationRecoveryPending = true;
      experimentalMediaPresentationRecoveryReason = String(reason || 'foreground_region_wait').slice(0, 80);
      return false;
    }
    const current = experimentalMediaForegroundRecovery;
    if (foregroundRecoveryCurrent(current) && current.lifecycleGeneration === experimentalMediaLifecycleGeneration) {
      experimentalMediaForegroundSuspensionGap = false;
      const triggerWallAt = Date.now();
      current.reason = String(reason || current.reason || 'foreground').slice(0, 80);
      current.lastTriggerWallAt = triggerWallAt;
      if (!current.canvasStarted) {
        current.canvasNotBeforeWallAt = Math.max(
          Number(current.canvasNotBeforeWallAt || 0),
          triggerWallAt + experimentalMediaForegroundCanvasStabilityMillis
        );
      }
      if (options.foregroundConfirmed) current.foregroundPaintConfirmed = true;
      if (!Array.isArray(current.triggers)) current.triggers = [];
      if (reason && !current.triggers.includes(String(reason))) {
        current.triggers.push(String(reason).slice(0, 80));
      }
      queueExperimentalMediaForegroundRecovery(current, 0);
      return true;
    }
    invalidateExperimentalMediaForegroundRecovery('new_foreground_attempt');
    const startedWallAt = Date.now();
    const attempt = {
      id: ++experimentalMediaForegroundRecoverySequence,
      lifecycleGeneration: experimentalMediaLifecycleGeneration,
      presentationRegionGeneration: experimentalMediaPresentationRegionGeneration,
      baselineSDRRenderSerial: Number(authoritativeSDRRenderSerial || 0),
      startedWallAt,
      lastTriggerWallAt: startedWallAt,
      canvasNotBeforeWallAt: startedWallAt + experimentalMediaForegroundCanvasStabilityMillis,
      deadlineWallAt: startedWallAt + experimentalMediaForegroundRecoveryWindowMillis,
      reason: String(reason || 'foreground').slice(0, 80),
      triggers: [String(reason || 'foreground').slice(0, 80)],
      phase: 'version_check',
      reportedPhase: '',
      reportedReason: '',
      versionChecked: false,
      versionOutcome: 'pending',
      serverCapabilityChecked: false,
      capabilityOutcome: 'pending',
      versionAbortController: null,
      reloadRequested: false,
      canvasStarted: false,
      restartUsed: false,
      retryOrdinal: 0,
      retryIndex: 0,
      foregroundPaintConfirmed: Boolean(
        options.foregroundConfirmed || experimentalMediaDocumentHasFocus()
      ),
      reconcileRunning: false,
      cancelled: false
    };
    experimentalMediaPresentationRecoveryPending = false;
    experimentalMediaForegroundRecovery = attempt;
    armExperimentalMediaForegroundRecoveryDeadline(attempt);
    experimentalMediaForegroundSuspensionGap = false;
    experimentalMediaState.enabled = true;
    if (options.forceCanvasReset) experimentalMediaCanvasResetGeneration = -1;
    cancelExperimentalMediaStart();
    armExperimentalMediaDynamicRangeRecovery({ onlyFutureChange: true });
    // A restored page must re-admit the current release rather than inheriting
    // a capability verdict from the pre-suspension WebKit process.
    experimentalMediaCapabilityReady = false;
    experimentalClientCapabilityAllowed = false;
    if (experimentalClientHDRController) {
      closeExperimentalMedia({ keepEnabled: true, status: 'Sagatavo svaigu HDR virsmu…' });
      experimentalMediaState.enabled = true;
    }
    reportExperimentalMediaForegroundRecovery(attempt, 'version_check', attempt.reason);
    queueExperimentalMediaForegroundRecovery(attempt, 0);
    return true;
  }

  function noteExperimentalMediaForegroundFrame() {
    if (experimentalMediaPresentationRecoveryPending) {
      requestExperimentalHDRPresentationRegionRecovery('presentation_region_fresh_sdr');
    }
    const attempt = experimentalMediaForegroundRecovery;
    if (!foregroundRecoveryCurrent(attempt)) return false;
    return queueExperimentalMediaForegroundRecovery(attempt, 0);
  }

  function scheduleExperimentalMediaForegroundReturnConfirmation(reason) {
    if (!experimentalMediaPreferenceController.enabled) return false;
    const sequence = ++experimentalMediaForegroundReturnConfirmationSequence;
    const observedLifecycleGeneration = experimentalMediaLifecycleGeneration;
    const confirmationReason = String(reason || 'foreground_return').slice(0, 64);
    if (experimentalMediaForegroundReturnConfirmationTimer) {
      clearTimeout(experimentalMediaForegroundReturnConfirmationTimer);
      experimentalMediaForegroundReturnConfirmationTimer = null;
    }
    experimentalMediaForegroundReturnConfirmationTimer = setTimeout(() => {
      experimentalMediaForegroundReturnConfirmationTimer = null;
      if (sequence !== experimentalMediaForegroundReturnConfirmationSequence ||
        !experimentalMediaPreferenceController.enabled ||
        document.visibilityState !== 'visible' ||
        experimentalMediaPresentationRegionBlocked ||
        !experimentalHDRSurfacePresentationAllowed() ||
        controlCodeHDRFreezeTargetActive()) return;
      const requestPaint = window && window.requestAnimationFrame;
      if (typeof requestPaint !== 'function') return;
      try {
        requestPaint(() => {
          if (sequence !== experimentalMediaForegroundReturnConfirmationSequence ||
            !experimentalMediaPreferenceController.enabled ||
            document.visibilityState !== 'visible' ||
            experimentalMediaPresentationRegionBlocked ||
            !experimentalHDRSurfacePresentationAllowed() ||
            controlCodeHDRFreezeTargetActive()) return;
          if (experimentalMediaLifecycleArmed ||
            experimentalMediaLifecycleGeneration !== observedLifecycleGeneration) {
            resumeExperimentalMediaForLifecycle(`return_confirm:${confirmationReason}`);
          }
          if (experimentalMediaPresentationRecoveryPending) {
            requestExperimentalHDRPresentationRegionRecovery(`return_confirm:${confirmationReason}`);
          }
          const current = experimentalMediaForegroundRecovery;
          if (!foregroundRecoveryCurrent(current)) return;
          current.foregroundPaintConfirmed = true;
          queueExperimentalMediaForegroundRecovery(current, 0);
        });
      } catch (_) {}
    }, experimentalMediaForegroundReturnConfirmationMillis);
    return true;
  }

  function noteExperimentalMediaForegroundPulse() {
    if (document.visibilityState !== 'visible') return false;
    const now = Date.now();
    const previousPulseWallAt = experimentalMediaForegroundPulseWallAt;
    const detectedGap = now - previousPulseWallAt >= experimentalMediaForegroundSuspensionGapMillis;
    if (detectedGap) {
      experimentalMediaForegroundSuspensionGap = true;
    }
    experimentalMediaForegroundPulseWallAt = now;
    const controller = experimentalClientHDRController;
    if (controller && typeof controller.checkSettlementDeadline === 'function') {
      controller.checkSettlementDeadline('foreground_pulse');
    }
    const currentAttempt = experimentalMediaForegroundRecovery;
    if (detectedGap && foregroundRecoveryCurrent(currentAttempt)) {
      const attemptPredatesSuspension = now - Number(currentAttempt.startedWallAt || 0) >=
        now - Number(previousPulseWallAt || 0);
      if (!attemptPredatesSuspension) {
        // An explicit visibility/pageshow return created this attempt after the
        // last pre-suspension pulse. Consume the duplicate gap signal into it.
        experimentalMediaForegroundSuspensionGap = false;
        queueExperimentalMediaForegroundRecovery(currentAttempt, 0);
        return false;
      }
      // No lifecycle event arrived before this pulse. The surviving attempt
      // predates the suspension and therefore cannot own a trustworthy WebGPU
      // surface or capability response; fence it and start a new generation.
      armExperimentalMediaLifecycleResume();
      closeExperimentalMedia({ keepEnabled: true, status: 'HDR skats atjaunojas.' });
      resumeExperimentalMediaForLifecycle('foreground_pulse_gap');
      return false;
    }
    if (detectedGap && experimentalMediaPreferenceController.enabled && !experimentalMediaLifecycleArmed &&
      !foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) {
      armExperimentalMediaLifecycleResume();
      closeExperimentalMedia({ keepEnabled: true, status: 'HDR skats atjaunojas.' });
      resumeExperimentalMediaForLifecycle('foreground_pulse_gap');
    }
    if (experimentalMediaLifecycleArmed && experimentalMediaPreferenceController.enabled &&
      experimentalMediaDocumentHasFocus()) {
      resumeExperimentalMediaForLifecycle('foreground_pulse_armed');
    }
    if (experimentalMediaPresentationRecoveryPending) {
      requestExperimentalHDRPresentationRegionRecovery('foreground_pulse_region');
    }
    return experimentalMediaForegroundSuspensionGap;
  }

  function scheduleExperimentalMediaCapabilityRetry(reason, attempt, forceCanvasReset) {
    if (experimentalMediaCapabilityRetryTimer) return false;
    if (attempt >= experimentalMediaCapabilityRetryDelays.length) {
      armExperimentalMediaDynamicRangeRecovery();
      return false;
    }
    const delay = experimentalMediaCapabilityRetryDelays[attempt];
    const foregroundRecoveryID = experimentalMediaForegroundRecovery &&
      experimentalMediaForegroundRecovery.id;
    experimentalMediaCapabilityRetryTimer = setTimeout(() => {
      experimentalMediaCapabilityRetryTimer = null;
      if (foregroundRecoveryID && (!experimentalMediaForegroundRecovery ||
        experimentalMediaForegroundRecovery.id !== foregroundRecoveryID)) return;
      scheduleExperimentalMediaStart(reason || 'capability_retry', {
        capabilityAttempt: attempt + 1,
        forceCanvasReset: Boolean(forceCanvasReset)
      });
    }, delay);
    return true;
  }

  function scheduleExperimentalMediaRendererRetry(reason) {
    if (!experimentalMediaResumeRetryArmed || experimentalMediaRendererRetryTimer ||
      !experimentalMediaState.enabled || document.visibilityState !== 'visible' ||
      experimentalMediaPresentationRegionBlocked || !experimentalHDRSurfacePresentationAllowed() ||
      controlCodeHDRFreezeTargetActive()) return false;
    experimentalMediaResumeRetryArmed = false;
    const foregroundAttempt = foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)
      ? experimentalMediaForegroundRecovery
      : null;
    const foregroundRecoveryID = foregroundAttempt && foregroundAttempt.id;
    if (foregroundAttempt) {
      foregroundAttempt.retryOrdinal += 1;
      reportExperimentalMediaForegroundRecovery(
        foregroundAttempt,
        'initializing',
        `surface_retry_scheduled:${String(reason || 'failed').slice(0, 48)}`
      );
    }
    experimentalMediaRendererRetryTimer = setTimeout(() => {
      experimentalMediaRendererRetryTimer = null;
      if (foregroundRecoveryID && (!experimentalMediaForegroundRecovery ||
        experimentalMediaForegroundRecovery.id !== foregroundRecoveryID)) return;
      if (!experimentalMediaState.enabled || document.visibilityState !== 'visible' ||
        experimentalMediaPresentationRegionBlocked || !experimentalHDRSurfacePresentationAllowed() ||
        controlCodeHDRFreezeTargetActive()) return;
      experimentalClientHDRFailed = false;
      // The only automatic renderer retry must not reuse the surface that just
      // failed. Reset the lifecycle marker so the existing one-per-attempt
      // replacement gate creates one new WebKit compositor association.
      experimentalMediaCanvasResetGeneration = -1;
      scheduleExperimentalMediaStart(`renderer_retry:${String(reason || 'failed').slice(0, 48)}`, {
        forceCanvasReset: true
      });
    }, 250);
    return true;
  }

  function scheduleExperimentalMediaActiveFailureRecovery(reason) {
    if (!experimentalMediaPreferenceController.enabled || !experimentalMediaState.enabled ||
      document.visibilityState !== 'visible' || experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() || controlCodeHDRFreezeTargetActive()) return false;
    if (foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) return false;
    if (experimentalMediaActiveFailureRecoveryTimer) return true;
    experimentalMediaActiveFailureRecoveryTimer = setTimeout(() => {
      experimentalMediaActiveFailureRecoveryTimer = null;
      if (!experimentalMediaPreferenceController.enabled || !experimentalMediaState.enabled ||
        document.visibilityState !== 'visible' ||
        experimentalMediaPresentationRegionBlocked || !experimentalHDRSurfacePresentationAllowed() ||
        controlCodeHDRFreezeTargetActive() ||
        foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) return;
      experimentalMediaResumeRetryArmed = true;
      beginExperimentalMediaForegroundRecovery('renderer_failure', {
        forceCanvasReset: true
      });
    }, 0);
    return true;
  }

  function scheduleExperimentalMediaStart(reason, options) {
    options = options || {};
    if (!experimentalMediaState.enabled || !experimentalMediaCapabilityReady ||
      document.visibilityState !== 'visible' || experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() || controlCodeHDRFreezeTargetActive()) return false;
    const controller = experimentalClientHDRController;
    const snapshot = controller && controller.snapshot();
    if (snapshot && snapshot.active) {
      controller.setDocumentVisible(true);
      return true;
    }
    if (experimentalMediaStartPending) return true;

    const generation = ++experimentalMediaStartGeneration;
    const pending = {
      generation,
      reason: String(reason || 'connect').slice(0, 80),
      capabilityAttempt: Math.max(0, Number(options.capabilityAttempt || 0)),
      forceCanvasReset: Boolean(options.forceCanvasReset),
      frameHandles: [],
      fallbackTimer: null,
      cancelled: false
    };
    experimentalMediaStartPending = pending;

    const settle = () => {
      if (experimentalMediaStartPending !== pending || pending.cancelled || generation !== experimentalMediaStartGeneration) return;
      experimentalMediaStartPending = null;
      if (pending.fallbackTimer) {
        clearTimeout(pending.fallbackTimer);
        pending.fallbackTimer = null;
      }
      if (!experimentalMediaState.enabled || document.visibilityState !== 'visible') return;
      const capability = refreshExperimentalClientCapability();
      if (!experimentalClientCapabilityAllowed || !capability.supported) {
        setExperimentalMediaStatus('Parastā straume — gaida pārlūka HDR iespēju.');
        if (document.body) document.body.dataset.experimentalMedia = 'fallback-sdr';
        const transientDynamicRange = capability.videoFrame && capability.mainThreadCanvas && capability.webgpu &&
          capability.dynamicRangeLimit && !capability.highDynamicRange;
        if (transientDynamicRange) {
          scheduleExperimentalMediaCapabilityRetry(
            pending.reason,
            pending.capabilityAttempt,
            pending.forceCanvasReset
          );
        }
        return;
      }
      experimentalMediaLastStartReason = pending.reason;
      const lifecycleReset = experimentalMediaLifecycleGeneration > 0 &&
        experimentalMediaCanvasResetGeneration !== experimentalMediaLifecycleGeneration;
      connectExperimentalClientHDR({
        forceCanvasReset: pending.forceCanvasReset || lifecycleReset,
        reason: pending.reason
      });
    };

    const waitVisibleFrames = (remaining) => {
      if (experimentalMediaStartPending !== pending || pending.cancelled || generation !== experimentalMediaStartGeneration) return;
      if (!experimentalMediaState.enabled || document.visibilityState !== 'visible') {
        cancelExperimentalMediaStart();
        return;
      }
      if (remaining <= 0) {
        settle();
        return;
      }
      const requestPaint = window && window.requestAnimationFrame;
      if (typeof requestPaint !== 'function') return;
      try {
        const handle = requestPaint(() => waitVisibleFrames(remaining - 1));
        pending.frameHandles.push(handle);
      } catch (_) {}
    };

    pending.fallbackTimer = setTimeout(settle, experimentalMediaVisibleSettleTimeoutMillis);
    waitVisibleFrames(experimentalMediaVisibleSettleFrames);
    return true;
  }

  function connectExperimentalMedia(reason, options) {
    if (!experimentalMediaState.enabled) return false;
    return scheduleExperimentalMediaStart(reason || 'connect', options);
  }

  function armExperimentalMediaLifecycleResume() {
    if (experimentalMediaPreferenceController.enabled || experimentalMediaState.enabled) {
      if (!experimentalMediaLifecycleArmed) {
        experimentalMediaLifecycleGeneration += 1;
        experimentalMediaLifecycleArmed = true;
        experimentalMediaLifecycleResumeAttemptID = 0;
        experimentalMediaLifecycleLastResumeWallAt = 0;
      }
      experimentalMediaForegroundPulseWallAt = Date.now();
      invalidateExperimentalMediaForegroundRecovery('lifecycle_backgrounded');
      experimentalMediaResumeRetryArmed = true;
      cancelExperimentalMediaStart();
      if (experimentalMediaRendererRetryTimer) {
        clearTimeout(experimentalMediaRendererRetryTimer);
        experimentalMediaRendererRetryTimer = null;
      }
    }
  }

  function resumeExperimentalMediaForLifecycle(reason) {
    const resumesArmedLifecycle = experimentalMediaLifecycleArmed;
    if (document.visibilityState === 'visible') {
      experimentalMediaLifecycleArmed = false;
    }
    if (!experimentalMediaPreferenceController.enabled) {
      experimentalMediaLifecycleResumeAttemptID = 0;
      experimentalMediaResumeRetryArmed = false;
      cancelExperimentalMediaStart();
      return false;
    }
    if (document.visibilityState !== 'visible') return false;
    experimentalMediaLifecycleLastResumeWallAt = Date.now();
    experimentalMediaState.enabled = true;
    if (foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) {
      queueExperimentalMediaForegroundRecovery(experimentalMediaForegroundRecovery, 0);
      return true;
    }
    const controller = experimentalClientHDRController;
    const snapshot = controller && controller.snapshot();
    if (snapshot && snapshot.active &&
      experimentalMediaForegroundRecoveredGeneration === experimentalMediaLifecycleGeneration) {
      controller.setDocumentVisible(true);
      return true;
    }
    if (experimentalClientHDRFailed) {
      if (!experimentalMediaResumeRetryArmed) return false;
      experimentalClientHDRFailed = false;
    }
    const started = beginExperimentalMediaForegroundRecovery(reason || 'lifecycle_resume', {
      forceCanvasReset: true
    });
    if (started && resumesArmedLifecycle &&
      foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) {
      experimentalMediaLifecycleResumeAttemptID = experimentalMediaForegroundRecovery.id;
    }
    return started;
  }

  function recoverExperimentalMediaForFocusOnlyLifecycle() {
    if (document.visibilityState !== 'visible' || !experimentalMediaPreferenceController.enabled ||
      experimentalMediaLifecycleArmed) return false;
    if (foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)) return false;
    const now = Date.now();
    if (experimentalMediaLifecycleLastResumeWallAt > 0 &&
      now - experimentalMediaLifecycleLastResumeWallAt <= experimentalMediaLifecycleReturnClusterMillis) {
      return false;
    }
    experimentalMediaForegroundSuspensionGap = false;
    armExperimentalMediaLifecycleResume();
    if (typeof closeExperimentalMedia === 'function') {
      closeExperimentalMedia({ keepEnabled: true, status: 'HDR skats apturēts fonā.' });
    }
    return true;
  }

  function mountExperimentalMediaControl() {
    if (!experimentalMediaMount || experimentalMediaMounted) return;
    experimentalMediaMount.hidden = false;
    experimentalMediaMount.textContent = '';
    html`
      <section class="experimental-media-control" aria-label="HDR skats">
        <label class="experimental-media-toggle">
          <input id="experimentalMediaToggle" type="checkbox" checked="${() => experimentalMediaState.enabled}">
          <span>${() => experimentalMediaState.label}</span>
        </label>
        <label class="experimental-media-engine" hidden="${() => !experimentalMediaState.boostSelectorAllowed}">
          <span>Pārlūka spilgtums</span>
          <select id="experimentalMediaHDRBoost">
            ${CLIENT_HDR_DISPLAY_BOOSTS.map((boost) => html`<option value="${boost}">${boost}×</option>`.key(boost))}
          </select>
        </label>
        <p class="experimental-media-detail" aria-live="polite">${() => experimentalMediaState.status}</p>
        <p class="experimental-media-detail" data-hdr-preference-status>${() => experimentalMediaState.preferenceStatus}</p>
        <p class="experimental-media-detail" data-hdr-engine-status>${() => experimentalMediaState.engineStatus}</p>
        <p class="experimental-media-detail" data-hdr-boost-status hidden="${() => experimentalMediaState.engine !== CLIENT_HDR_ENGINE}">${() => experimentalMediaState.boostStatus}</p>
      </section>
    `(experimentalMediaMount);
    const toggle = document.getElementById('experimentalMediaToggle');
    if (toggle) {
      toggle.addEventListener('change', () => {
        experimentalMediaPreferenceController.choose(Boolean(toggle.checked));
      });
    }
    const boost = document.getElementById('experimentalMediaHDRBoost');
    if (boost) {
      boost.addEventListener('change', () => {
        chooseExperimentalHDRBoost(boost.value);
      });
    }
    syncExperimentalMediaSelectors();
    experimentalMediaMounted = true;
  }

  function fetchExperimentalMediaCapability() {
    const controller = typeof AbortController === 'function' ? new AbortController() : null;
    const request = { cache: 'no-store' };
    if (controller) request.signal = controller.signal;
    let timeout = null;
    const deadline = new Promise((_resolve, reject) => {
      timeout = setTimeout(() => {
        if (controller) {
          try { controller.abort(); } catch (_) {}
        }
        reject(new Error('capability_fetch_timeout'));
      }, experimentalMediaCapabilityFetchTimeoutMillis);
    });
    const operation = (async () => {
      const response = await fetch('/api/v1/experimental-media/capability', request);
      const payload = await response.json();
      return { response, payload };
    })();
    return Promise.race([operation, deadline]).finally(() => {
      if (timeout) clearTimeout(timeout);
    });
  }

  function applyExperimentalMediaCapabilityPayload(response, payload, foregroundAttemptID) {
    if (foregroundAttemptID && (!experimentalMediaForegroundRecovery ||
      experimentalMediaForegroundRecovery.id !== foregroundAttemptID)) return false;
    if (!response || !response.ok || !payload || !payload.allowed) return false;
    const advertisedEngines = Array.isArray(payload.allowedEngines) ? payload.allowedEngines : [];
    const advertisedBoosts = Array.isArray(payload.allowedDisplayBoosts)
      ? payload.allowedDisplayBoosts.map(Number)
      : [];
    const boostContractMatches = CLIENT_HDR_DISPLAY_BOOSTS.every((boost, index) => advertisedBoosts[index] === boost) &&
      advertisedBoosts.length === CLIENT_HDR_DISPLAY_BOOSTS.length;
    experimentalClientCapabilityAllowed = advertisedEngines.includes(CLIENT_HDR_ENGINE) &&
      payload.clientPipeline === CLIENT_HDR_PIPELINE &&
      payload.presentationKind === CLIENT_HDR_PRESENTATION_KIND &&
      Number(payload.targetDisplayBoost) === CLIENT_HDR_TARGET_DISPLAY_BOOST &&
      boostContractMatches;
    experimentalMediaState.boostSelectorAllowed = experimentalMediaAccountProjectionAvailable;
    if (!experimentalMediaEngineProjectionObserved) {
      experimentalMediaState.engine = resolveCapabilityHDREngine(advertisedEngines, payload.selectedEngine);
    }
    if (!experimentalMediaBoostProjectionObserved) {
      experimentalHDRBoostPreferenceController.observe(payload.selectedDisplayBoost);
    }
    experimentalMediaCapabilityReady = experimentalClientCapabilityAllowed;
    if (!experimentalMediaCapabilityReady) return false;
    experimentalMediaCapabilityDiscoveryAttempt = 0;
    experimentalMediaState.engineStatus = experimentalHDREngineStatus(experimentalMediaState.engine);
    mountExperimentalMediaControl();
    applyExperimentalMediaPreference(experimentalMediaPreferenceController.enabled, { reason: 'projection' });
    return true;
  }

  async function discoverExperimentalMediaCapability(options) {
    options = options || {};
    if (!experimentalMediaMount || !experimentalMediaCanvas) return;
    if (experimentalMediaCapabilityDiscoveryPromise) return experimentalMediaCapabilityDiscoveryPromise;
    if (experimentalMediaCapabilityDiscoveryRetryTimer) {
      if (!options.forceImmediate) return;
      clearTimeout(experimentalMediaCapabilityDiscoveryRetryTimer);
      experimentalMediaCapabilityDiscoveryRetryTimer = null;
    }
    const attempt = Math.max(0, Number(options.attempt !== undefined
      ? options.attempt
      : experimentalMediaCapabilityDiscoveryAttempt));
    let shouldRetry = false;
    const operation = (async () => {
      const capability = await fetchExperimentalMediaCapability();
      const response = capability.response;
      const payload = capability.payload;
      if (!response.ok) {
        shouldRetry = true;
        return;
      }
      applyExperimentalMediaCapabilityPayload(response, payload, Number(options.foregroundAttemptID || 0));
    })();
    experimentalMediaCapabilityDiscoveryPromise = operation;
    try {
      await operation;
    } catch (_) {
      shouldRetry = true;
    } finally {
      if (experimentalMediaCapabilityDiscoveryPromise === operation) {
        experimentalMediaCapabilityDiscoveryPromise = null;
      }
      if (shouldRetry &&
        attempt < experimentalMediaCapabilityRetryDelays.length &&
        document.visibilityState === 'visible') {
        experimentalMediaCapabilityDiscoveryAttempt = attempt + 1;
        experimentalMediaCapabilityDiscoveryRetryTimer = setTimeout(() => {
          experimentalMediaCapabilityDiscoveryRetryTimer = null;
          discoverExperimentalMediaCapability({
            reason: options.reason || 'capability_fetch_retry',
            attempt: attempt + 1
          });
        }, experimentalMediaCapabilityRetryDelays[attempt]);
      } else if (shouldRetry && attempt >= experimentalMediaCapabilityRetryDelays.length) {
        // Keep later lifecycle/online triggers eligible for a fresh bounded
        // discovery budget without continuing a background retry loop.
        experimentalMediaCapabilityDiscoveryAttempt = 0;
      }
    }
  }

  if (cfg.experimentalMediaCandidate === true) discoverExperimentalMediaCapability();

  const publicMessageTranslations = new Map([
    ['Ticket server is starting', 'Biļetes serveris startējas'],
    ['Ticket server is stopped', 'Biļetes serveris ir apturēts'],
    ['Root shell is unavailable', 'Root komandrinda nav pieejama'],
    ['ViVi is not installed from a local Pixel app store yet', 'ViVi vēl nav instalēta no vietējā Pixel lietotņu veikala'],
    ['ViVi launch intent is unavailable', 'ViVi palaišana nav pieejama'],
    ['No visible frame has been sent yet', 'Vēl nav nosūtīts neviens redzams kadrs'],
    ['Unavailable', 'Nav pieejams'],
    ['invalid_code', 'Ievadi 2-8 ciparus'],
    ['request_in_progress', 'Iepriekšējais koda pieprasījums vēl tiek pabeigts'],
    ['ticket_action_in_progress', 'Tālrunis izpilda iepriekšējo biļetes darbību'],
    ['ticket_mutation_in_progress', 'Tālrunis pabeidz iepriekšējo biļetes darbību. Mēģini vēlreiz pēc mirkļa.'],
    ['ticket_action_interaction_revision_unproved', 'Biļetes vizuālais apstiprinājums vairs nav aktuāls. Sagaidi svaigu apstiprinājumu.'],
    ['Spacetime connection is not ready', 'Vadības kanāls vēl savienojas. Mēģini vēlreiz.'],
    ['Spacetime connection failed', 'Neizdevās savienot vadības kanālu. Mēģini vēlreiz.'],
    ['Spacetime connection closed', 'Vadības kanāls pārtrauca savienojumu. Mēģini vēlreiz.'],
    ['rate_limited', 'Minūtē var pieprasīt divus kodus'],
    ['phone_unavailable', 'Tālrunis pašlaik nav pieejams'],
    ['control_code_result_timeout', 'Tālrunis nepaspēja izveidot kodu'],
    ['control_code_not_generated', 'Tālrunis neatgrieza ģenerētu kodu'],
    ['control_code_cleanup_attention_needed', 'Tālrunim vajag mirkli, lai atgrieztos pie biļetes'],
    ['control_code_stream_marker_required', 'Tālrunis nepaguva apstiprināt ģenerēto kodu'],
    ['waiting_for_ticket_reselect', 'Tālrunis vēl izvēlas biļeti. Uzgaidi mirkli.'],
    ['waiting_for_stream_recovery', 'Tiešraide atjaunojas pirms koda pieprasījuma.'],
    ['control_code_recovery_queue_timeout', 'Tālrunis nepaguva atjaunot biļeti. Mēģini vēlreiz.'],
    ['control_code_stream_unstable', 'Tiešraide nav pietiekami stabila koda pieprasījumam.'],
    ['fast_not_ready', 'Tālrunis vēl sagatavo ātro koda ceļu. Mēģini vēlreiz pēc mirkļa.']
  ]);

  function localizePublicMessage(value) {
    if (!value) return '';
    const text = String(value);
    const exact = publicMessageTranslations.get(text);
    if (exact) return exact;
    for (const [prefix, translation] of [
      ['Ticket server is listening on ', 'Biļetes serveris klausās uz '],
      ['Ticket server failed to start: ', 'Biļetes serveri neizdevās palaist: '],
      ['Ticket session stopped: ', 'Biļetes sesija apturēta: ']
    ]) {
      if (text.startsWith(prefix)) return translation + text.slice(prefix.length);
    }
    return text;
  }

  function jwtExpiresAtMillis(token) {
    const parts = String(token || '').split('.');
    if (parts.length !== 3) return 0;
    try {
      const encoded = parts[1].replace(/-/g, '+').replace(/_/g, '/');
      const payload = JSON.parse(atob(encoded.padEnd(Math.ceil(encoded.length / 4) * 4, '=')));
      const exp = Number(payload && payload.exp);
      return Number.isFinite(exp) && exp > 0 ? exp * 1000 : 0;
    } catch (_) {
      return 0;
    }
  }

  function rememberSpacetimeToken(token) {
    directSpacetimeToken = String(token || '');
    spacetimeDirectUnavailableLogged = false;
    directSpacetimeTokenExpiresAt = jwtExpiresAtMillis(token);
  }

  function spacetimeTokenExpired(token) {
    const expiresAt = directSpacetimeTokenExpiresAt || jwtExpiresAtMillis(token);
    return expiresAt > 0 && Date.now() + 30000 >= expiresAt;
  }

  function publishSpacetimeClientStatus(status) {
    const normalized = String(status || 'offline');
    const safeStatus = ({
      idle: 'idle',
      connecting: 'connecting',
      live: 'live',
      reconnecting: 'reconnecting',
      offline: 'offline',
      heartbeat_failed: 'degraded'
    })[normalized] || 'offline';
    const previousStatus = spacetimeClientStatus;
    spacetimeClientStatus = normalized;
    if (document.body) document.body.dataset.spacetimeConnection = safeStatus;
    if (previousStatus !== normalized && typeof refreshUserActivityTickSchedule === 'function') {
      refreshUserActivityTickSchedule();
    }
  }

  function clearSpacetimeStateRefreshTimer() {
    if (!spacetimeStateRefreshTimer) return;
    clearTimeout(spacetimeStateRefreshTimer);
    spacetimeStateRefreshTimer = null;
  }

  function renderTicketStateAsUnconfirmed() {
    renderTicketInteraction(null);
  }

  function markSpacetimeStateUnconfirmed(reason) {
    spacetimeStateFresh = false;
    spacetimeStateRefreshStartedAt = Date.now();
    clearSpacetimeStateRefreshTimer();
    renderTicketStateAsUnconfirmed();
    spacetimeStateRefreshTimer = setTimeout(() => {
      spacetimeStateRefreshTimer = null;
      if (!spacetimeStateFresh) {
        document.body.dataset.ticketStateFresh = 'false';
        renderTicketStateAsUnconfirmed();
        clientLog('ticket_state_unconfirmed', reason || 'spacetime_snapshot_timeout');
      }
    }, spacetimeStateRefreshTimeoutMs);
    document.body.dataset.ticketStateFresh = 'false';
  }

  function markSpacetimeStateFresh() {
    spacetimeStateFresh = true;
    spacetimeStateRefreshStartedAt = 0;
    clearSpacetimeStateRefreshTimer();
    document.body.dataset.ticketStateFresh = 'true';
  }

  function usesDirectSpacetimeAuth() {
    const mode = String((cfg.auth && cfg.auth.mode) || 'spacetime').toLowerCase();
    return !['dev', 'development', 'none'].includes(mode);
  }

  function safeAuthReturnTo(value) {
    const fallback = '/';
    const raw = String(value || '').trim();
    if (!raw) return fallback;
    try {
      const parsed = new URL(raw, location.origin);
      if (parsed.origin !== location.origin || parsed.pathname === '/auth/callback') {
        return fallback;
      }
      return `${parsed.pathname}${parsed.search}${parsed.hash}` || fallback;
    } catch (_) {
      return fallback;
    }
  }

  function authReturnTarget() {
    if (location.pathname === '/auth/callback') {
      return '/';
    }
    return safeAuthReturnTo(`${location.pathname}${location.search}${location.hash}`);
  }

  async function beginSpacetimeLogin(returnTo) {
    const next = new URL('/api/v1/auth/start', location.origin);
    next.searchParams.set('returnTo', safeAuthReturnTo(returnTo || authReturnTarget()));
    location.assign(next.toString());
  }

  function clearLocalAuthState() {
    directSpacetimeToken = '';
    directSpacetimeTokenExpiresAt = 0;
  }

  function showAuthError(error) {
    clearLocalAuthState();
    document.body.className = 'auth-error-page';
    document.body.innerHTML = [
      '<main class="auth-shell">',
      '<section class="auth-panel">',
      `<p id="authStatus" class="auth-status" role="alert">${escapeHTML(error && error.message ? error.message : 'Pierakstīšanās neizdevās.')}</p>`,
      '<button id="retryAuth" class="primary" type="button">Mēģināt vēlreiz</button>',
      '</section>',
      '</main>'
    ].join('');
    const button = document.getElementById('retryAuth');
    if (button) {
      button.addEventListener('click', () => {
        button.disabled = true;
        beginSpacetimeLogin(authReturnTarget()).catch(showAuthError);
      });
    }
  }

  function startAuthRedirect() {
    document.body.className = 'auth-redirect-page';
    document.body.innerHTML = '';
    if (location.pathname === '/auth/callback') {
      location.replace('/');
      return;
    }
    beginSpacetimeLogin(authReturnTarget()).catch(showAuthError);
  }

  function setStatus(text) {
    if (/\b(ffmpeg|h\.?264|h265|h\.?265|root capture|root screenrecord|root shell|screenrecord|codec)\b/i.test(String(text || '').trim())) return;
    statusLine.textContent = localizePublicMessage(text);
  }

  function accountPublicId(email) {
    const normalized = String(email || '').trim().toLowerCase();
    let hash = 2166136261 >>> 0;
    for (let i = 0; i < normalized.length; i += 1) {
      hash ^= normalized.charCodeAt(i) & 0xff;
      hash = Math.imul(hash, 16777619) >>> 0;
    }
    return hash.toString(36).padStart(4, '0').slice(0, 4);
  }

  function clientLog(event, detail) {
    reportClientFault(event, detail);
  }

  const clientHDRFieldsByPhase = {
    first_presented: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'canvasEncoding', 'configurationColorSpace',
      'toneMappingMode', 'configurationDynamicRangeLimit', 'mappingModel', 'colorExpansionExponent',
      'sourceColorSpace', 'continuousSurface', 'selectedDisplayBoost', 'intendedOutputPeak',
      'edrRequestPatchIntended', 'intendedRequestPatchPeak', 'intendedRequestPatchEdge',
      'gpuCompleted', 'compositorOpportunitiesCompleted', 'postPresentSource',
      'postPresentOpportunityCount', 'firstShownMillis', 'queueDelayMillis',
      'decodedFrameToSubmitMillis', 'completionMillis', 'displayReadyMillis',
      'decodedFrameToDisplayReadyMillis', 'epoch', 'sequence', 'sequenceLag',
      'ageDeltaMillis', 'coalesced', 'dropped', 'failures', 'lifecycleGeneration',
      'canvasGeneration', 'rendererGeneration', 'attemptId', 'recoveryPhase', 'triggerSet',
      'streamEpoch', 'streamSequence', 'retryOrdinal', 'startReason'
    ],
    presented: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'canvasEncoding', 'configurationColorSpace',
      'configurationDynamicRangeLimit', 'mappingModel', 'colorExpansionExponent', 'sourceColorSpace',
      'continuousSurface', 'selectedDisplayBoost', 'intendedOutputPeak', 'gpuCompleted',
      'compositorOpportunitiesCompleted', 'postPresentSource', 'postPresentOpportunityCount',
      'queueDelayMillis',
      'decodedFrameToSubmitMillis', 'decodedFrameToDisplayReadyMillis', 'epoch',
      'sequence', 'sequenceLag', 'ageDeltaMillis', 'coalesced', 'failures'
    ],
    session_summary: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'targetDisplayBoost', 'selectedDisplayBoost', 'offered', 'rendered',
      'coalesced', 'dropped', 'failures', 'rendererActive', 'ownedFrameCount',
      'pending', 'inFlight', 'surfaceVisible', 'canvasEncoding', 'presentationState',
      'paintPending', 'paintWaitTimeoutMillis', 'paintWaitTimeoutPending',
      'paintRecoveryRequested', 'surfaceTransitions', 'reason', 'lifecycleGeneration',
      'canvasGeneration', 'rendererGeneration', 'attemptId', 'recoveryPhase', 'triggerSet',
      'streamEpoch', 'streamSequence', 'retryOrdinal', 'startReason'
    ],
    foreground_recovery: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'attemptId', 'recoveryPhase', 'triggerSet',
      'reason', 'versionOutcome', 'capabilityOutcome', 'streamEpoch', 'streamSequence',
      'baselineSDRRenderSerial', 'sdrRenderSerial', 'lifecycleGeneration', 'canvasGeneration',
      'presentationRegionGeneration', 'presentationRegionBlocked', 'presentationRecoveryPending',
      'foregroundPaintConfirmed', 'foregroundStabilityWaitMillis',
      'rendererGeneration', 'presentationState', 'surfaceVisible', 'retryOrdinal',
      'recoveryElapsedMillis', 'recoveryRemainingMillis', 'startReason'
    ],
    renderer_ready: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'targetDisplayBoost', 'selectedDisplayBoost',
      'intendedOutputPeak', 'mappingModel', 'colorExpansionExponent', 'continuousSurface', 'active', 'ready',
      'edrRequestPatchIntended', 'intendedRequestPatchPeak', 'intendedRequestPatchEdge',
      'canvasEncoding', 'configurationColorSpace', 'toneMappingMode', 'configurationDynamicRangeLimit',
      'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration', 'attemptId', 'recoveryPhase',
      'triggerSet', 'streamEpoch', 'streamSequence', 'retryOrdinal', 'startReason'
    ],
    renderer_init_timeout: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'rendererInitTimeoutMillis',
      'rendererInitElapsedMillis', 'rendererInitCheckSource', 'active', 'ready',
      'rendererActive', 'surfaceVisible', 'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration',
      'attemptId', 'recoveryPhase', 'triggerSet', 'streamEpoch', 'streamSequence', 'retryOrdinal', 'startReason'
    ],
    surface_reset: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'reason', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'canvasReplaced', 'continuousSurface', 'attemptId', 'recoveryPhase',
      'triggerSet', 'streamEpoch', 'streamSequence', 'retryOrdinal', 'startReason'
    ],
    surface_transition: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'toSurface', 'reason',
      'presentationState', 'recoveryFreshStreak', 'surfaceTransitions', 'sequenceLag',
      'ageDeltaMillis', 'fallbackDurationMillis', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'attemptId', 'recoveryPhase', 'triggerSet', 'streamEpoch',
      'streamSequence', 'retryOrdinal', 'startReason'
    ],
    presentation_holdover: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'reason', 'selectedDisplayBoost',
      'surfaceVisible', 'presentationState', 'firstPresented', 'visualHoldover',
      'visualHoldoverReason', 'proofFresh', 'streamRegionVisible', 'epoch', 'sequence',
      'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration', 'streamEpoch',
      'streamSequence', 'startReason'
    ],
    holdover_release_deferred: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'reason', 'selectedDisplayBoost',
      'surfaceVisible', 'presentationState', 'firstPresented', 'visualHoldover',
      'visualHoldoverReason', 'proofFresh', 'streamRegionVisible', 'epoch', 'sequence',
      'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration', 'streamEpoch',
      'streamSequence', 'startReason', 'stage'
    ],
    stream_region_visibility: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'streamRegionVisible',
      'surfaceVisible', 'presentationState', 'firstPresented', 'visualHoldover',
      'proofFresh', 'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration',
      'streamEpoch', 'streamSequence', 'startReason'
    ],
    boost_changed: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'previousDisplayBoost',
      'canvasEncoding', 'surfaceVisible', 'presentationState'
    ],
    boost_change_failed: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'requestedDisplayBoost',
      'canvasEncoding', 'surfaceVisible', 'reason'
    ],
    gpu_completion: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'intendedOutputPeak',
      'canvasEncoding', 'configurationColorSpace', 'configurationDynamicRangeLimit', 'mappingModel',
      'colorExpansionExponent', 'sourceColorSpace', 'continuousSurface', 'gpuCompleted',
      'edrRequestPatchIntended', 'intendedRequestPatchPeak', 'intendedRequestPatchEdge',
      'completionMillis', 'sequence', 'failures', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'attemptId', 'recoveryPhase', 'triggerSet', 'streamEpoch',
      'streamSequence', 'retryOrdinal', 'startReason'
    ],
    settlement_started: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'attemptId', 'recoveryPhase', 'triggerSet',
      'selectedDisplayBoost', 'streamEpoch', 'streamSequence', 'lifecycleGeneration',
      'canvasGeneration', 'rendererGeneration', 'settlementTimeoutMillis', 'settlementElapsedMillis',
      'settlementPending', 'presentationState', 'surfaceVisible', 'epoch', 'sequence', 'retryOrdinal',
      'startReason'
    ],
    compositor_settlement_started: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'attemptId', 'recoveryPhase', 'triggerSet',
      'selectedDisplayBoost', 'streamEpoch', 'streamSequence', 'lifecycleGeneration',
      'canvasGeneration', 'rendererGeneration', 'settlementDeadlineMillis',
      'postPresentOpportunityTarget', 'presentationState', 'surfaceVisible', 'retryOrdinal', 'startReason'
    ],
    settlement_deadline_exceeded: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'attemptId', 'recoveryPhase', 'triggerSet',
      'reason', 'streamEpoch', 'streamSequence', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'settlementTimeoutMillis', 'settlementElapsedMillis',
      'settlementCheckSource', 'settlementPending', 'presentationState', 'surfaceVisible',
      'epoch', 'sequence', 'retryOrdinal', 'startReason'
    ],
    compositor_settlement_result: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'attemptId', 'recoveryPhase', 'triggerSet',
      'streamEpoch', 'streamSequence', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'settlementDeadlineMillis', 'settlementTimedOut', 'postPresentSource',
      'postPresentOpportunityCount', 'compositorOpportunitiesCompleted', 'presentationState',
      'surfaceVisible', 'retryOrdinal', 'startReason'
    ],
    gpu_completion_timeout: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'attemptId', 'recoveryPhase', 'triggerSet',
      'streamEpoch', 'streamSequence', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'gpuCompletionTimeoutMillis', 'epoch', 'sequence',
      'presentationOrdinal', 'retryOrdinal', 'startReason'
    ],
    paint_wait_timeout: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'canvasEncoding',
      'paintWaitTimeoutMillis', 'epoch', 'sequence', 'presentationOrdinal',
      'pending', 'inFlight', 'paintPending', 'surfaceVisible', 'presentationState',
      'rendered', 'dropped', 'failures', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'attemptId', 'recoveryPhase', 'triggerSet', 'streamEpoch',
      'streamSequence', 'retryOrdinal', 'startReason'
    ],
    paint_wait_failed: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'canvasEncoding',
      'paintWaitTimeoutMillis', 'epoch', 'sequence', 'presentationOrdinal',
      'pending', 'inFlight', 'paintPending', 'surfaceVisible', 'presentationState',
      'rendered', 'dropped', 'failures', 'lifecycleGeneration', 'canvasGeneration',
      'rendererGeneration', 'attemptId', 'recoveryPhase', 'triggerSet', 'streamEpoch',
      'streamSequence', 'retryOrdinal', 'startReason'
    ],
    frame_clone_failed: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'selectedDisplayBoost', 'pending', 'inFlight',
      'surfaceVisible', 'presentationState', 'offered', 'dropped', 'failures',
      'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration', 'attemptId',
      'recoveryPhase', 'triggerSet', 'streamEpoch', 'streamSequence', 'retryOrdinal', 'startReason'
    ],
    fallback: [
      'assetVersion', 'engine', 'pipeline', 'phase', 'canvasEncoding', 'selectedDisplayBoost',
      'rendererActive', 'ownedFrameCount', 'pending', 'inFlight', 'failures', 'reason',
      'lifecycleGeneration', 'canvasGeneration', 'rendererGeneration', 'attemptId', 'recoveryPhase',
      'triggerSet', 'streamEpoch', 'streamSequence', 'retryOrdinal', 'settlementPending',
      'settlementElapsedMillis', 'startReason'
    ]
  };

  function boundedClientLogJSON(detail, maximumBytes) {
    const entries = Object.entries(detail || {});
    while (entries.length) {
      const encoded = JSON.stringify(Object.fromEntries(entries));
      if (encoded.length <= maximumBytes) return encoded;
      entries.pop();
    }
    return '{}';
  }

  function clientHDRMeasurement(event, firstShownMillis, decodeMillis, measurements) {
    const measured = Object.assign({}, measurements && typeof measurements === 'object' ? measurements : {});
    if (Number.isFinite(firstShownMillis)) measured.firstShownMillis = Math.max(0, Math.round(firstShownMillis));
    if (Number.isFinite(decodeMillis)) measured.decodeMillis = Math.max(0, Math.round(decodeMillis));
    const phase = String(measured.phase || '');
    const phaseFields = measured.engine === CLIENT_HDR_ENGINE && clientHDRFieldsByPhase[phase];
    const detail = phaseFields ? { assetVersion } : {
      pageVersion,
      assetVersion,
      visibility: document.visibilityState,
      webCodecs: 'VideoDecoder' in window,
      webgpu: Boolean(navigator && navigator.gpu)
    };
    if (phaseFields) {
      for (const key of phaseFields) {
        if (Object.prototype.hasOwnProperty.call(measured, key)) detail[key] = measured[key];
      }
    } else {
      Object.assign(detail, measured);
    }
    enqueueClientLog({
      level: 'info',
      event: String(event || '').slice(0, 80),
      detailJson: boundedClientLogJSON(detail, 1000),
      at: Date.now()
    });
  }

  const navigationEntry = performance.getEntriesByType('navigation')[0];
  const navigationStartPerformanceMillis = Number(navigationEntry && navigationEntry.startTime || 0);
  sendVideoSocketClientLog('browser_opened', {
    source: 'navigation_timing',
    visibility: document.visibilityState,
    webCodecs: 'VideoDecoder' in window
  }, navigationStartPerformanceMillis);

  function flushClientLogs() {
    if (!videoWs || videoWs.readyState !== WebSocket.OPEN || !pendingClientLogs.length) return;
    const batch = pendingClientLogs.splice(0, Math.min(20, pendingClientLogs.length));
    for (let index = 0; index < batch.length; index += 1) {
      const entry = batch[index];
      const detailJson = (entry.detailJson || safeString({
        pageVersion,
        detail: entry.detail,
        queuedAt: entry.at
      })).slice(0, 1000);
      try {
        videoWs.send(JSON.stringify({
          type: 'client_log',
          event: String(entry.event || 'client_event').slice(0, 80),
          detail: detailJson
        }));
      } catch (_) {
        pendingClientLogs.unshift(...batch.slice(index));
        if (pendingClientLogs.length > 100) pendingClientLogs.splice(100);
        return;
      }
    }
  }

  function showStreamResumeSpinner() {
    if (!streamResumeSpinner || !streamResumeSpinner.hidden) return;
    streamResumeSpinner.hidden = false;
    publishStreamDebug();
  }

  function hideStreamResumeSpinner() {
    if (!streamResumeSpinner || streamResumeSpinner.hidden) return;
    streamResumeSpinner.hidden = true;
    publishStreamDebug();
  }

  function preserveCurrentFrame(reason) {
    if (!hasRenderedFrame || lastFrameAt <= 0 || canvas.width <= 0 || canvas.height <= 0) {
      return fallbackFrameAvailable;
    }
    try {
      if (!fallbackFrameCanvas) {
        fallbackFrameCanvas = document.createElement('canvas');
      }
      if (fallbackFrameCanvas.width !== canvas.width || fallbackFrameCanvas.height !== canvas.height) {
        fallbackFrameCanvas.width = canvas.width;
        fallbackFrameCanvas.height = canvas.height;
      }
      const fallbackCtx = fallbackFrameCanvas.getContext('2d', { alpha: false });
      fallbackCtx.imageSmoothingEnabled = false;
      fallbackCtx.drawImage(canvas, 0, 0, canvas.width, canvas.height);
      fallbackFrameAvailable = true;
      lastFallbackFrameAt = performance.now();
      return true;
    } catch (error) {
      clientLog('stream_frame_preserve_failed', `${reason || 'unknown'}:${error && error.message || 'copy failed'}`);
      return fallbackFrameAvailable;
    }
  }

  function redrawPreservedFrame() {
    if (!fallbackFrameAvailable || !fallbackFrameCanvas || canvas.width <= 0 || canvas.height <= 0) return false;
    try {
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(fallbackFrameCanvas, 0, 0, canvas.width, canvas.height);
      hasRenderedFrame = true;
      return true;
    } catch (error) {
      clientLog('stream_frame_restore_failed', error && error.message || 'restore failed');
      return false;
    }
  }

  function showEmpty(message, showStart) {
    clearStreamLiveStaleGrace();
    hideStreamResumeSpinner();
    emptyMessage.textContent = localizePublicMessage(message);
    startStreamButton.hidden = !showStart;
    emptyState.hidden = false;
    document.body.dataset.streamReady = 'false';
    document.body.dataset.streamLive = 'false';
    document.body.dataset.streamFreshness = 'WAITING';
    keepFirstScreenPinned();
  }

  function showStreamWaiting(message) {
    if (hasRenderedFrame || fallbackFrameAvailable) {
      preserveCurrentFrame('stream_waiting');
      redrawPreservedFrame();
      emptyState.hidden = true;
      document.body.dataset.streamReady = 'true';
      setStatus(message);
      showStreamResumeSpinner();
      keepFirstScreenPinned();
      return;
    }
    showQuietStreamLoading();
  }

  function showQuietStreamLoading() {
    redrawPreservedFrame();
    emptyMessage.textContent = '';
    startStreamButton.hidden = true;
    emptyState.hidden = true;
    document.body.dataset.streamReady = 'true';
    updateStreamFreshnessStatus('stream_recovery');
    showStreamResumeSpinner();
    keepFirstScreenPinned();
  }

  function showStreamRecovery() {
    preserveCurrentFrame('stream_recovery');
    showQuietStreamLoading();
  }

  function hideEmpty() {
    const wasEmptyVisible = !emptyState.hidden;
    emptyState.hidden = true;
    document.body.dataset.streamReady = 'true';
    if (!streamStatusStale(freshStreamStatus(performance.now()) || latestStreamStatus)) {
      hideStreamResumeSpinner();
    }
    if (wasEmptyVisible) keepFirstScreenPinned();
  }

  function showUnsupported(message) {
    streamUnsupported = true;
    configured = false;
    updateStreamFreshnessStatus('stream_unsupported');
    showEmpty(message, false);
    setStatus(message);
    clientLog('h264_unsupported', message);
  }

  function resizeCanvasBox() {
    ticketSliderLayoutRevision += 1;
    cancelTicketRegisterSliderSession('viewport_changed');
    updateViewportVars();
    const maxWidth = Math.max(1, stage.clientWidth);
    const maxHeight = Math.max(1, stage.clientHeight);
    const scale = Math.min(maxWidth / streamSize.width, maxHeight / streamSize.height);
    const displayWidth = Math.max(1, Math.floor(streamSize.width * scale));
    const displayHeight = Math.max(1, Math.floor(streamSize.height * scale));
    const streamLayout = stagePage || stage;
    streamLayout.style.setProperty('--stream-width', `${displayWidth}px`);
    streamLayout.style.setProperty('--stream-height', `${displayHeight}px`);
    streamLayout.style.setProperty('--stream-left', `${Math.max(0, Math.floor((maxWidth - displayWidth) / 2))}px`);
    streamLayout.style.setProperty('--stream-top', `${Math.max(0, Math.floor((maxHeight - displayHeight) / 2))}px`);
    if (currentState) renderTicketActionV3Controls(currentState);
  }

  function videoSocketState() {
    return videoWs ? videoWs.readyState : -1;
  }

  function videoSocketKeepsStreamActive() {
    const state = videoSocketState();
    return state === WebSocket.OPEN || state === WebSocket.CONNECTING;
  }

  function currentStreamFocusActive() {
    if (idleDisconnected || streamUnsupported) return false;
    if (controlCodeKeepsVideoAliveWhileHidden()) return true;
    if (document.visibilityState === 'hidden') return false;
    return videoSocketKeepsStreamActive();
  }

  function publishCurrentStreamFocus(reason) {
    publishStreamFocus(currentStreamFocusActive(), reason || 'stream_focus_state');
  }

  function clearActivationReconnectBurst() {
    if (activationReconnectBurstTimer) clearTimeout(activationReconnectBurstTimer);
    activationReconnectBurstTimer = null;
  }

  function streamHasFreshRenderedFrame() {
    return currentRenderedFreshness(performance.now()).liveLabeled;
  }

  function safeResumeLabel(value, fallback) {
    return String(value || fallback || 'unknown')
      .toLowerCase()
      .replace(/[0-9]/g, '')
      .replace(/[^a-z_-]+/g, '_')
      .replace(/_+/g, '_')
      .replace(/^_+|_+$/g, '')
      .slice(0, 48) || fallback || 'unknown';
  }

  function resumeBooleanLabel(value) { return value ? 'yes' : 'no'; }

  function hiddenDurationBucket(hiddenMs) {
    if (!Number.isFinite(hiddenMs) || hiddenMs <= 0) return 'none';
    if (hiddenMs >= backgroundRecoveryHiddenMs) return 'long';
    if (hiddenMs >= oldTabFreshResumeHiddenMs) return 'old';
    return 'short';
  }

  function mediaSessionStuckOnPreservedFrame() {
    if (!videoSocketKeepsStreamActive() || (!hasRenderedFrame && !fallbackFrameAvailable)) return false;
    const freshness = currentRenderedFreshness(performance.now());
    return !freshness.liveLabeled && freshness.streamFreshnessState === 'STALE' && (!configured || !decoderConfigured);
  }

  function resumeDiagnosticSnapshot(detail) {
    return Object.assign({
      visibility: safeResumeLabel(document.visibilityState, 'unknown'),
      socket: ['connecting', 'open', 'closing', 'closed'][videoSocketState()] || 'none',
      fresh: resumeBooleanLabel(streamHasFreshRenderedFrame()),
      configured: resumeBooleanLabel(configured),
      decoder: resumeBooleanLabel(decoderConfigured)
    }, detail || {});
  }

  function logResumeCheckpoint(event, detail, flow) {
    const target = flow || activeResumeFlow;
    if (!target || target.done || target.logs >= 6) return;
    target.logs += 1;
    enqueueClientLog({
      level: 'info',
      event: compactClientEventName(event),
      detailJson: safeString(resumeDiagnosticSnapshot(detail)).slice(0, 600)
    });
  }

  function finishActivationResumeFlow(reason, flow) {
    const target = flow || activeResumeFlow;
    if (!target || target.done) return;
    logResumeCheckpoint(reason === 'fresh_frame' ? 'activation_resume_fresh_frame' : 'activation_resume_finish', {
      result: safeResumeLabel(reason, 'complete'),
      elapsedMs: Math.round(performance.now() - target.startedAt)
    }, target);
    target.done = true;
    if (target === activeResumeFlow) {
      clearActivationReconnectBurst();
      activeResumeFlow = null;
    }
  }

  function startActivationResumeFlow(reason, trigger, options) {
    if (streamUnsupported) return null;
    const paused = Boolean(options && options.pauseBurst);
    const nextReason = safeResumeLabel(reason, 'activation');
    const nextTrigger = safeResumeLabel(trigger, 'activation');
    let flow = activeResumeFlow;
    if (
      flow && !flow.done && flow.trigger === 'initial_load' &&
      (nextReason === 'pageshow' || nextReason === 'focus')
    ) {
      return flow;
    }
    if (!flow || flow.done) {
      flow = {
        id: `resume_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`,
        reason: nextReason,
        trigger: nextTrigger,
        startedAt: performance.now(),
        attempts: 0,
        logs: 0,
        done: false,
        paused,
        lifecycleResumeStarted: false
      };
      activeResumeFlow = flow;
      logResumeCheckpoint('activation_resume_start', { trigger: flow.trigger, paused: resumeBooleanLabel(paused) }, flow);
    } else {
      flow.reason = nextReason;
      flow.trigger = nextTrigger;
      flow.paused = paused;
    }
    if (paused) {
      clearActivationReconnectBurst();
    } else {
      runActivationReconnectBurst(flow.reason, flow);
    }
    return flow;
  }

  function followActivationResumeLifecycle(reason, trigger) {
    const current = activeResumeFlow;
    if (current && !current.done) return current;
    const flow = startActivationResumeFlow(reason, trigger);
    if (flow) flow.lifecycleResumeStarted = true;
    return flow;
  }

  function claimActivationResumeLifecycle(reason, trigger) {
    const current = activeResumeFlow;
    if (current && !current.done && current.lifecycleResumeStarted) return null;
    const resetPausedBudget = Boolean(current && !current.done && current.paused);
    const flow = startActivationResumeFlow(reason, trigger, { pauseBurst: true });
    if (flow) {
      if (resetPausedBudget && flow === current) {
        flow.startedAt = performance.now();
        flow.attempts = 0;
        flow.logs = 0;
        logResumeCheckpoint('activation_resume_start', {
          trigger: flow.trigger,
          paused: 'true',
          budget: 'reset'
        }, flow);
      }
      flow.lifecycleResumeStarted = true;
    }
    return flow;
  }

  function pauseActivationResumeLifecycle(reason, trigger) {
    const flow = startActivationResumeFlow(reason, trigger, { pauseBurst: true });
    if (flow) flow.lifecycleResumeStarted = false;
    return flow;
  }

  function runActivationReconnectBurst(reason, flow) {
    if (!flow || flow !== activeResumeFlow || flow.done) return;
    clearActivationReconnectBurst();
    if (streamHasFreshRenderedFrame()) {
      finishActivationResumeFlow('fresh_frame', flow);
      return;
    }
    if (idleDisconnected || document.visibilityState !== 'visible') {
      flow.paused = true;
      return;
    }
    if (flow.attempts >= activationReconnectMaxTicks || performance.now() - flow.startedAt >= activationReconnectBurstMs) {
      requestServerRecoveryDebounced(`${reason || 'resume'}_exhausted`, true);
      finishActivationResumeFlow('exhausted', flow);
      return;
    }
    flow.paused = false;
    connectSpacetimeState().catch(() => clientLog('spacetime_reconnect_failed', 'activation_resume'));
    publishCurrentStreamFocus(reason || 'activation');
    const initialLoad = flow.trigger === 'initial_load';
    if (flow.attempts === 0 && !mediaSessionStuckOnPreservedFrame()) {
      connectDirectVideo({ skipEarlyGrace: !initialLoad });
      if (!initialLoad) {
        requestKeyframeDebounced(`${reason || 'activation'}_keyframe`, 0, true);
      }
    } else {
      recoverFreshMediaSession(reason || 'activation', 'activation_resume', {
        forceServerRecovery: mediaSessionStuckOnPreservedFrame(),
        skipEarlyGrace: flow.trigger !== 'initial_load',
        waitForInitialSocket: flow.trigger === 'initial_load'
      });
    }
    flow.attempts += 1;
    activationReconnectBurstTimer = setTimeout(
      () => runActivationReconnectBurst(reason, flow),
      flow.attempts === 1 ? activationReconnectFirstRetryMs : activationReconnectTickMs
    );
  }

  function initialVideoSocketNeedsAdoption() {
    if (videoWs && (videoWs.readyState === WebSocket.CONNECTING || videoWs.readyState === WebSocket.OPEN)) return true;
    if (videoWs) return false;
    const early = window.TICKET_EARLY_VIDEO;
    if (!early || early.claimed || early.closed || early.error || !early.ws) return false;
    return early.ws.readyState === WebSocket.CONNECTING || early.ws.readyState === WebSocket.OPEN;
  }

  function recoverFreshMediaSession(reason, kind, options) {
    if (idleDisconnected || streamUnsupported) return false;
    options = options || {};
    if (options.waitForInitialSocket && initialVideoSocketNeedsAdoption()) {
      // During the first-load burst, let the head-opened socket finish its
      // handshake (or let connectDirectVideo adopt it) instead of replacing
      // a healthy CONNECTING socket on the 150 ms retry.
      connectDirectVideo({ skipEarlyGrace: false });
      return true;
    }
    const now = performance.now();
    const reusable = !options.forceReconnect
      && videoSocketKeepsStreamActive()
      && lastRecoveryVideoReconnectSeq === videoSocketOpenSeq
      && now - lastRecoveryVideoReconnectAt < recoveryVideoReconnectDebounceMs;
    if (reusable) {
      requestKeyframeDebounced(options.keyframeReason || `${reason || 'resume'}_keyframe`, 0, true);
    } else {
      lastRecoveryVideoReconnectAt = now;
      closeEarlyVideo(reason || 'media_session_recovery');
      if (hiddenVideoCloseTimer) clearTimeout(hiddenVideoCloseTimer);
      if (hiddenStreamFocusTimer) clearTimeout(hiddenStreamFocusTimer);
      hiddenVideoCloseTimer = null;
      hiddenStreamFocusTimer = null;
      preserveCurrentFrame(`media_recovery:${reason || 'unknown'}`);
      closeDirectVideo();
      resetStreamState({ preserveFrame: true });
      showStreamRecovery();
      connectDirectVideo({ skipEarlyGrace: Boolean(options.skipEarlyGrace) });
      lastRecoveryVideoReconnectSeq = videoSocketOpenSeq;
      requestKeyframeDebounced(options.keyframeReason || `${reason || 'resume'}_keyframe`, 0, true);
      clientLog('fresh_video_resume', safeString({ reason, kind }));
    }
    if (options.forceServerRecovery) {
      requestServerRecoveryDebounced(options.serverRecoveryReason || `${reason || 'resume'}_recover`, true);
    }
    return true;
  }

  function connect() {
    if (idleDisconnected) return;
    clearTimeout(reconnectTimer);
    keepFirstScreenPinned();
    setConnected('Savienojas');
    connectedAt = performance.now();
    connectSpacetimeState().then(() => {
      publishCurrentStreamFocus('public_connected');
      setConnected('Savienots');
      if (!streamUnsupported) {
        showStreamWaiting(configured ? 'Gaida tiešraides kadru...' : 'Gaida biļetes straumi...');
      }
      flushClientLogs();
      connectDirectVideo();
    }).catch((error) => {
      setConnected('Savienojuma kļūme');
      if (!streamUnsupported) {
        showStreamRecovery();
      }
      clientLog('spacetime_connect_failed', error && error.message || 'connect failed');
      reconnectTimer = setTimeout(connect, 1500);
    });
    connectDirectVideo();
    // The page initializer starts the first-load resume flow. Keep a
    // reconnect flow available only after that flow has finished; starting a
    // second burst here races the head-opened socket and can skip its short
    // adoption grace before the app has installed its handlers.
    if (!hasRenderedFrame && !activeResumeFlow) {
      startActivationResumeFlow('cold_open', 'initial_load');
    }
  }

  function resetStreamState(options) {
    clearStreamLiveStaleGrace();
    cancelTicketRegisterSliderSession('stream_reset');
    const preserveFrame = Boolean(options && options.preserveFrame);
    if (preserveFrame) {
      preserveCurrentFrame('reset_stream_state');
    }
    configured = false;
    configuredAt = 0;
    videoSocketCreatedAt = 0;
    videoConnectedAt = 0;
    lastFrameAt = 0;
    lastDecodedFrameAt = 0;
    lastPacketAt = 0;
    lastPacketSequenceAdvancedAt = 0;
    firstFrameReceived = false;
    needsKeyFrame = true;
    currentStreamEpoch = 0;
    lastPacketSequence = 0;
    lastPacketTimestamp = 0;
    lastAcceptedFrameSequence = 0;
    lastAcceptedFrameTimestamp = 0;
    firstDecodedTraceSent = false;
    lastAcceptedFrameReceivedAt = 0;
    lastAcceptedFrameVisualAgeMillis = 0;
    lastAcceptedFrameQueuedAt = 0;
    // A reconnect may preserve the old canvas while the next stream epoch is
    // negotiated.  The old rendered proof must not be compared with the new
    // epoch or it can hide a valid slider forever (or authorize controls over
    // the wrong picture).  Keep the pixels if requested, but require a fresh
    // frame to re-establish proof metadata.
    lastRenderedFrameReceivedAt = 0;
    lastRenderedFrameQueuedAt = 0;
    lastRenderedFrameRenderedAt = 0;
    lastRenderedFrameVisualAgeMillis = 0;
    lastRenderedFrameEpoch = 0;
    lastRenderedFrameSequence = 0;
    lastRenderedPresentationOrdinal = 0;
    lastRenderedFrameTimestamp = 0;
    lastDecodedFrameSequence = 0;
    firstRenderedTraceSent = false;
    resetFirstFrameServerRecovery();
    latestStreamStatus = null;
    lastStreamStatusAt = 0;
    decoderMode = 'annexb';
    avcAdapterTried = false;
    avcDescription = null;
    avcSps = null;
    avcPps = null;
    if (!preserveFrame) {
      hasRenderedFrame = false;
      fallbackFrameAvailable = false;
      lastFallbackFrameAt = 0;
    }
    closeDecoder();
    if (preserveFrame) {
      redrawPreservedFrame();
    }
  }

  function restartStream(reason, options) {
    if (streamUnsupported) return;
    const now = performance.now();
    if (now - lastRestartAt < 5000) return;
    lastRestartAt = now;
    clientLog('video_stream_restart', reason);
    const preserveFrame = Boolean(options && options.preserveFrame) || hasRenderedFrame || fallbackFrameAvailable;
    preserveCurrentFrame(`restart_stream:${reason || 'unknown'}`);
    closeDirectVideo();
    resetStreamState({ preserveFrame });
    showStreamRecovery();
    setTimeout(connectDirectVideo, 250);
  }

  function closeDirectVideo() {
    if (hiddenVideoCloseTimer) {
      clearTimeout(hiddenVideoCloseTimer);
      hiddenVideoCloseTimer = null;
    }
    if (hiddenStreamFocusTimer) {
      clearTimeout(hiddenStreamFocusTimer);
      hiddenStreamFocusTimer = null;
    }
    preserveCurrentFrame('close_direct_video');
    closeDecoder();
    const sockets = new Set(activeVideoSockets);
    if (videoWs) sockets.add(videoWs);
    videoWs = null;
    videoSocketCreatedAt = 0;
    sockets.forEach((socket) => {
      intentionallyClosedVideoSockets.add(socket);
      try { socket.close(1000, 'client_closed'); } catch (_) {}
    });
  }

  function controlCodeKeepsVideoAliveWhileHidden() {
    if (!codeRequest) return false;
    const status = String(codeRequest.status || '');
    if (status === 'queued' || status === 'running') return true;
    if (status !== 'succeeded') return false;
    const requestID = String(codeRequest.requestId || '').trim();
    if (!requestID) return false;
    if (controlCodeResultCapturedRequestID === requestID) return false;
    return controlCodeResultCaptureRequestID === requestID || !codeResultArea.hidden;
  }

  function keepControlCodeVideoAlive(reason) {
    if (!controlCodeKeepsVideoAliveWhileHidden()) return;
    if (hiddenVideoCloseTimer) {
      clearTimeout(hiddenVideoCloseTimer);
      hiddenVideoCloseTimer = null;
    }
    sendVideoClientLog('control_code_capture_keepalive', reason || 'control_code');
    if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {
      connectDirectVideo();
      return;
    }
    // During a control-code request, do NOT call requestKeyframe here.
    // The previous behavior asked the phone for a new keyframe every
    // keepalive tick (every ~1s), which forced the phone to re-encode
    // and the browser decoder to reconfigure, destabilizing both the
    // stream and the phone-side ViVi automation. The video socket is
    // already open; that's enough to keep the relay warm. Use the
    // debounced recovery path if the stream is actually degraded.
    if (videoWs.readyState === WebSocket.OPEN && reason && reason.startsWith('stale')) {
      requestKeyframeDebounced('control_code_capture_keepalive:' + reason, 4000);
    }
  }

  function pauseVideoWhileHidden(reason) {
    if (document.visibilityState !== 'hidden') return;
    if (controlCodeKeepsVideoAliveWhileHidden()) {
      keepControlCodeVideoAlive(reason || 'hidden_control_code_keepalive');
      return;
    }
    if (hiddenVideoCloseTimer) return;
    hiddenVideoCloseTimer = setTimeout(() => {
      hiddenVideoCloseTimer = null;
      if (document.visibilityState !== 'hidden') return;
      if (controlCodeKeepsVideoAliveWhileHidden()) {
        keepControlCodeVideoAlive(reason || 'hidden_control_code_keepalive');
        return;
      }
      if (!videoWs) return;
      clientLog('video_stream_paused_hidden', reason);
      preserveCurrentFrame(reason || 'hidden_video_pause');
      closeDirectVideo();
    }, hiddenVideoCloseDelayMs);
  }

  function releaseStreamFocusAfterHiddenGrace(reason) {
    if (controlCodeKeepsVideoAliveWhileHidden()) {
      if (hiddenStreamFocusTimer) {
        clearTimeout(hiddenStreamFocusTimer);
        hiddenStreamFocusTimer = null;
      }
      publishStreamFocus(true, reason || 'hidden_control_code_capture');
      return;
    }
    if (hiddenStreamFocusTimer) return;
    hiddenStreamFocusTimer = setTimeout(() => {
      hiddenStreamFocusTimer = null;
      if (document.visibilityState !== 'hidden') return;
      if (controlCodeKeepsVideoAliveWhileHidden()) {
        publishStreamFocus(true, reason || 'hidden_control_code_capture');
        return;
      }
      publishStreamFocus(false, reason || 'visibility_hidden');
    }, hiddenVideoCloseDelayMs);
  }

  function pauseHiddenStreamAfterGrace(reason) {
    releaseStreamFocusAfterHiddenGrace(reason || 'visibility_hidden');
    pauseVideoWhileHidden(reason || 'visibility_hidden');
  }

  function connectDirectVideo(options) {
    options = options || {};
    if (idleDisconnected) return;
    if (document.visibilityState === 'hidden' && !controlCodeKeepsVideoAliveWhileHidden()) {
      pauseVideoWhileHidden('connect_direct_video_hidden');
      return;
    }
    if (hiddenVideoCloseTimer) {
      clearTimeout(hiddenVideoCloseTimer);
      hiddenVideoCloseTimer = null;
    }
    if (videoWs && (videoWs.readyState === WebSocket.OPEN || videoWs.readyState === WebSocket.CONNECTING)) return;
    const early = claimEarlyVideoSocket();
    if (early && adoptVideoSocket(early.socket, early.queued, early.openedAt, 'early_video_socket')) {
      document.body.dataset.videoPath = 'https-h264';
      return;
    }
    // If the pre-script early socket is still CONNECTING (common on slow mobile
    // cold loads), give it a short grace window before opening a brand-new
    // socket. The retry path will adopt it once it upgrades to OPEN.
    const earlyPeek = window.TICKET_EARLY_VIDEO;
    if (earlyPeek && !earlyPeek.claimed && !earlyPeek.closed && !earlyPeek.error
        && earlyPeek.ws && earlyPeek.ws.readyState === WebSocket.CONNECTING) {
      if (options.skipEarlyGrace) {
        clientLog('early_video_connecting_grace_skipped', 'fast_resume');
        closeEarlyVideo('fast_resume');
      } else {
      clientLog('early_video_connecting_grace', '');
      setTimeout(connectDirectVideo, 250);
      return;
      }
    }
	    closeDirectVideo();
	    document.body.dataset.videoPath = 'https-h264';
	    videoSocketCreatedAt = performance.now();
    clientLog('video_socket_connect_attempt', JSON.stringify({
      path: 'https-h264',
      configured,
      visibility: document.visibilityState,
      frameAgeMs: lastFrameAt > 0 ? Math.round(performance.now() - lastFrameAt) : null
    }));
    videoSocketOpenSeq += 1;
	    const socket = safeWebSocket(streamURL('connect_direct_video'), 'video', videoSocketProtocols());
	    if (!socket) {
      clientLog('video_socket_create_failed', 'safe_websocket_unavailable');
	      showStreamRecovery();
	      setTimeout(connectDirectVideo, 1500);
	      return;
    }
    adoptVideoSocket(socket, [], 0, 'video_socket_open');
  }

  function noteVideoSocketOpen(socket, reason) {
    if (idleDisconnected || videoWs !== socket) {
      intentionallyClosedVideoSockets.add(socket);
      try { socket.close(1000, 'stale_video_socket'); } catch (_) {}
      return;
    }
	    if (videoConnectedAt <= 0) videoConnectedAt = performance.now();
    clientLog('video_socket_opened', JSON.stringify({
      reason: reason || 'video_socket_open',
      openWaitMs: videoSocketCreatedAt > 0 ? Math.round(performance.now() - videoSocketCreatedAt) : null,
      readyState: socket.readyState,
      visibility: document.visibilityState
    }));
	  flushClientLogs();
	    resetFirstFrameServerRecovery();
	    showStreamWaiting('Saņem video konfigurāciju...');
	    scheduleStreamFeedback('video_socket_open');
  }

  function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {
    if (!socket) return false;
    videoWs = socket;
    activeVideoSockets.add(socket);
    socket.binaryType = 'arraybuffer';
    publishCurrentStreamFocus(reason || 'video_socket_adopted');
    let videoMessageChain = Promise.resolve();
    function queueVideoSocketMessage(event, queued) {
      videoMessageChain = videoMessageChain.then(() => {
        if (idleDisconnected || videoWs !== socket) return;
        return handleVideoSocketMessage(event);
      }).catch((error) => {
        if (idleDisconnected || videoWs !== socket) return;
        sendVideoClientLog('video_message_failed', error && error.message || (queued ? 'queued message failed' : 'message failed'));
        if (!queued) requestKeyframe('video_message_failed');
      });
    }
    socket.onopen = () => noteVideoSocketOpen(socket, reason || 'video_socket_open');
    socket.onmessage = (event) => queueVideoSocketMessage(event, false);
    socket.onclose = (event) => {
      activeVideoSockets.delete(socket);
      if (videoWs === socket) videoWs = null;
      publishCurrentStreamFocus('video_socket_closed');
      if (intentionallyClosedVideoSockets.has(socket)) {
        clientLog('video_socket_closed_intentional', JSON.stringify({
          code: event && event.code,
          wasClean: event && event.wasClean
        }));
        intentionallyClosedVideoSockets.delete(socket);
        return;
      }
	      clientLog('video_socket_closed', JSON.stringify({
        code: event && event.code,
        wasClean: event && event.wasClean,
        configured,
        frameAgeMs: lastFrameAt > 0 ? Math.round(performance.now() - lastFrameAt) : null,
        visibility: document.visibilityState
	      }));
	      lastFeedbackSentAt = 0;
      reconcileClientHDRStreamContinuity('video_socket_closed', 'sdr_stream_unavailable');
      resetStreamState({ preserveFrame: true });
      showStreamRecovery();
      if (viewerIsForeground()) {
        setTimeout(connectDirectVideo, 1000);
      }
    };
    socket.onerror = () => {
      if (intentionallyClosedVideoSockets.has(socket)) return;
      clientLog('direct_video_websocket_error', JSON.stringify({
        readyState: socket.readyState,
        configured,
        frameAgeMs: lastFrameAt > 0 ? Math.round(performance.now() - lastFrameAt) : null
      }));
    };
    if (socket.readyState === WebSocket.OPEN) {
      if (openedAt > 0) videoConnectedAt = openedAt;
      noteVideoSocketOpen(socket, reason || 'early_video_socket_open');
    }
    queuedMessages.forEach((queued) => queueVideoSocketMessage(queued, true));
    return true;
  }

  function sendVideoClientLog(event, detail) {
    let safeDetail = '';
    if (detail != null && typeof detail === 'object') {
      try {
        safeDetail = JSON.stringify(detail);
      } catch (_) {
        safeDetail = String(detail);
      }
    } else {
      safeDetail = String(detail || '');
    }
    clientLog(event, safeDetail);
  }

  function clampFeedbackNumber(value, max) {
    const numeric = Number(value);
    if (!Number.isFinite(numeric) || numeric < 0) return 0;
    return Math.min(max, Math.round(numeric));
  }

  function sendStreamFeedback(reason, immediate) {
    if (!videoWs || videoWs.readyState !== WebSocket.OPEN) return false;
    const now = performance.now();
    const interval = document.visibilityState === 'hidden' ? streamFeedbackHiddenIntervalMs : streamFeedbackIntervalMs;
    if (!immediate && now - lastFeedbackSentAt < interval) return false;
    const freshness = currentRenderedFreshness(now);
    const decoderQueue = clampFeedbackNumber(decoder && decoder.decodeQueueSize, 32);
    const payload = {
      type: 'stream_feedback',
      version: streamFeedbackVersion,
      epoch: Number(currentStreamEpoch || 0),
      receivedSequence: Number(lastAcceptedFrameSequence || lastPacketSequence || 0),
      decodedSequence: Number(lastDecodedFrameSequence || lastRenderedFrameSequence || 0),
      renderedSequence: Number(lastRenderedFrameSequence || 0),
      // Keep the legacy field on the wire during the rolling upgrade. Every
      // accepted frame is now independently decodable, so the rendered frame
      // sequence is also the latest rendered keyframe sequence.
      renderedKeyframeSequence: Number(lastRenderedFrameSequence || 0),
      decoderQueueSize: decoderQueue,
      renderedVisualAgeMillis: clampFeedbackNumber(freshness.visualAgeMillis, 60000),
      visibility: document.visibilityState === 'hidden' ? 'hidden' : 'visible'
    };
    try {
      videoWs.send(JSON.stringify(payload));
      lastFeedbackSentAt = now;
      feedbackSentCount += 1;
      feedbackImmediateKey = immediate ? String(reason || 'immediate') : feedbackImmediateKey;
      return true;
    } catch (_) {
      feedbackSendFailureCount += 1;
      return false;
    }
  }

  function scheduleStreamFeedback(reason) {
    sendStreamFeedback(reason || 'transition', true);
  }

  function reportDecoderError(error, mode) {
    if (document.visibilityState === 'hidden') {
      if (hiddenDecoderTransientLogged) return;
      hiddenDecoderTransientLogged = true;
      sendVideoClientLog('decoder_transient_hidden', {
        mode: safeResumeLabel(mode, 'unknown'),
        state: 'hidden_transient'
      });
      return;
    }
    sendVideoClientLog('decoder_error', error && error.message || 'decoder error');
  }

  function browserLifecycleSourceTime(sourceAtPerformanceMillis) {
    const performanceMillis = Number.isFinite(Number(sourceAtPerformanceMillis))
      ? Number(sourceAtPerformanceMillis)
      : performance.now();
    const timeOrigin = Number.isFinite(Number(performance.timeOrigin))
      ? Number(performance.timeOrigin)
      : Date.now() - performance.now();
    return {
      sourceAtEpochMillis: Math.round(timeOrigin + performanceMillis),
      sourceAtPerformanceMillis: Number(performanceMillis.toFixed(3))
    };
  }

  function sendVideoSocketClientLog(event, detail, sourceAtPerformanceMillis) {
    const timing = browserLifecycleSourceTime(sourceAtPerformanceMillis);
    const safeDetail = detail != null && typeof detail === 'object' && !Array.isArray(detail)
      ? detail
      : { detail: safeString(detail).slice(0, 500) };
    const payload = Object.assign({
      pageVersion,
      assetVersion,
      visibility: document.visibilityState,
      webCodecs: 'VideoDecoder' in window
    }, safeDetail, timing);
    enqueueClientLog({
      level: 'info',
      event: String(event || 'client_event').slice(0, 80),
      detailJson: safeString(payload).slice(0, 1000),
      at: timing.sourceAtEpochMillis
    });
  }

  function liveStreamSuppressesBackgroundRequest(reason) {
    const cleanReason = String(reason || '').toLowerCase();
    if (cleanReason.includes('control_code')) return false;
    return streamHasFreshRenderedFrame();
  }

  function initialLoadDefersBrowserKeyframe(reason) {
    const cleanReason = String(reason || '').toLowerCase();
    if (cleanReason.includes('control_code')) return false;
    const flow = activeResumeFlow;
    if (!flow || flow.done || flow.trigger !== 'initial_load' || hasRenderedFrame) return false;
    const startedAt = Number(flow.startedAt);
    if (!Number.isFinite(startedAt)) return false;
    return Math.max(0, performance.now() - startedAt) < streamFirstFrameKeyframeMs;
  }

  function requestKeyframe(reason, force) {
    if (liveStreamSuppressesBackgroundRequest(reason)) return false;
    if (initialLoadDefersBrowserKeyframe(reason)) return false;
    const now = performance.now();
    if (!force && lastKeyframeCommandAt > 0 && now - lastKeyframeCommandAt < keyframeCommandMinIntervalMs) return false;
    lastKeyframeCommandAt = now;
    clientLog('keyframe_request', JSON.stringify({
      reason: reason || 'browser_request',
      force: Boolean(force),
      configured,
      videoState: videoWs ? videoWs.readyState : -1,
      frameAgeMs: lastFrameAt > 0 ? Math.round(performance.now() - lastFrameAt) : null
    }));
    runSpacetimeMutation((client) => client.requestKeyframe(reason || 'browser_request'), reason || 'keyframe')
      .catch((error) => clientLog('keyframe_request_failed', `${reason || 'keyframe'}:${error && error.message || 'failed'}`));
    return true;
  }

  function requestKeyframeDebounced(reason, minIntervalMs, force) {
    const now = performance.now();
    if (!force && lastRecoveryKeyframeAt > 0 && now - lastRecoveryKeyframeAt < minIntervalMs) return false;
    if (!requestKeyframe(reason, force)) return false;
    lastRecoveryKeyframeAt = now;
    return true;
  }

  function requestServerRecoveryDebounced(reason, force) {
    if (liveStreamSuppressesBackgroundRequest(reason)) return false;
    const now = performance.now();
    if (!force && lastRecoveryServerRecoverAt > 0 && now - lastRecoveryServerRecoverAt < recoveryServerRecoverDebounceMs) return false;
    lastRecoveryServerRecoverAt = now;
    clientLog('stream_recovery_request', JSON.stringify({
      reason: reason || 'browser_recovery',
      force: Boolean(force),
      configured,
      videoState: videoWs ? videoWs.readyState : -1,
      frameAgeMs: lastFrameAt > 0 ? Math.round(performance.now() - lastFrameAt) : null
    }));
    runSpacetimeMutation((client) => client.recoverStream(reason || 'browser_recovery'), reason || 'recover_stream')
      .catch((error) => clientLog('stream_recover_request_failed', `${reason || 'recover'}:${error && error.message || 'failed'}`));
    return true;
  }

  function resetFirstFrameServerRecovery() {
    firstFrameServerRecoveryAttempts = 0;
    firstFrameServerRecoveryExhausted = false;
  }

  function logFirstFrameServerRecoveryExhausted(phase) {
    enqueueClientLog({
      level: 'info',
      event: 'h264_first_frame_recovery_exhausted',
      detailJson: safeString({
        phase: safeResumeLabel(phase, 'first_frame_pending'),
        result: 'exhausted'
      }).slice(0, 600)
    });
  }

  function requestFirstFrameServerRecovery(reason, phase) {
    if (firstFrameServerRecoveryExhausted) return false;
    if (firstFrameServerRecoveryAttempts >= firstFrameServerRecoveryMaxAttempts) {
      firstFrameServerRecoveryExhausted = true;
      logFirstFrameServerRecoveryExhausted(phase);
      return false;
    }
    if (!requestServerRecoveryDebounced(reason, false)) return false;
    firstFrameServerRecoveryAttempts += 1;
    return true;
  }

  function closeDecoder() {
    // Chromium may still deliver output callbacks that were queued before close(). Tag every
    // decoder instance so a result-marker reset cannot render those stale frames through the
    // replacement decoder's metadata queue.
    decoderConfigureGeneration += 1;
    decoderGeneration += 1;
    if (decoder) {
      try { decoder.close(); } catch (_) {}
      decoder = null;
    }
    if (pendingPresentedFrame) {
      try { pendingPresentedFrame.frame.close(); } catch (_) {}
      pendingPresentedFrame = null;
    }
    if (presentationFrameHandle != null) {
      if (typeof cancelAnimationFrame === 'function') cancelAnimationFrame(presentationFrameHandle);
      else clearTimeout(presentationFrameHandle);
      presentationFrameHandle = null;
    }
    clearFrameMetadata();
    decoderConfigured = false;
  }

  function resetDecoderForRecovery(reason) {
    if (!lastDecoderConfig) return false;
    const now = performance.now();
    if (now - lastRecoveryDecoderResetAt < recoveryDecoderResetDebounceMs) return false;
    lastRecoveryDecoderResetAt = now;
    preserveCurrentFrame(`decoder_recovery:${reason || 'unknown'}`);
    sendVideoClientLog('h264_decoder_recovery_reset', reason);
    scheduleStreamFeedback('decoder_reset');
    configureDecoder(lastDecoderConfig, { preserveFrame: true, preserveSequence: true, requestReason: reason, preferAvc: decoderMode === 'avc' })
      .catch((error) => sendVideoClientLog('decoder_recovery_config_failed', error && error.message || 'decoder recovery failed'));
    return true;
  }

  function controlCodeDecoderBacklogReason() {
    if (!decoder || !decoderConfigured || !hasRenderedFrame) return '';
    const now = performance.now();
    const freshness = currentRenderedFreshness(now);
    const visualAgeMillis = Number(freshness.visualAgeMillis || 0);
    const decodeQueueSize = Number(decoder.decodeQueueSize || 0);
    if (visualAgeMillis > controlCodeLowLatencyVisualAgeMs) {
      return `visual_age_${Math.round(visualAgeMillis)}ms`;
    }
    if (decodeQueueSize > controlCodeLowLatencyDecodeQueueLimit) {
      return `decode_queue_${decodeQueueSize}`;
    }
    return '';
  }

  function controlCodeDecoderResetConfig() {
    const config = lastDecoderConfig || {};
    const codec = String(config.codec || '');
    const codedWidth = Number(config.width || canvas.width || 0);
    const codedHeight = Number(config.height || canvas.height || 0);
    if (!codec || !codedWidth || !codedHeight) return null;
    if (decoderMode === 'avc') {
      if (!avcDescription) return null;
      return { codec, codedWidth, codedHeight, description: avcDescription, optimizeForLatency: true };
    }
    return { codec, codedWidth, codedHeight, avc: { format: 'annexb' }, optimizeForLatency: true };
  }

  function resetControlCodeDecoderBacklog(requestID, reason, force) {
    requestID = String(requestID || '').trim();
    const resetKey = `${requestID}:${reason || 'control_code'}`;
    if (!requestID || lastControlCodeDecoderBacklogResetKey === resetKey) return false;
    if (!decoder || !decoderConfigured || typeof decoder.close !== 'function') return false;
    const backlogReason = controlCodeDecoderBacklogReason();
    if (!force && !backlogReason) return false;
    const resetConfig = controlCodeDecoderResetConfig();
    if (!resetConfig) return false;
    try {
      preserveCurrentFrame(`control_code_decoder_backlog:${reason || backlogReason}`);
      // A VideoDecoder.reset() can leave already-scheduled output callbacks from the
      // previous instance draining on Chromium. The generated-result marker must drop
      // those callbacks, not merely return the same decoder to an unconfigured state.
      closeDecoder();
      clearFrameMetadata();
      const decoderInstanceGeneration = decoderGeneration;
      decoder = new VideoDecoder({
        output: (frame) => {
          if (decoderInstanceGeneration !== decoderGeneration) {
            try { frame.close(); } catch (_) {}
            return;
          }
          scheduleDecodedFrame(frame, decoderMode === 'avc' ? 'avc' : 'annexb');
        },
        error: (error) => {
          if (decoderInstanceGeneration !== decoderGeneration) return;
          reportDecoderError(error, decoderMode === 'avc' ? 'avc' : 'annexb');
          needsKeyFrame = true;
          if (decoderMode === 'avc') {
            resetDecoderForRecovery('control_code_decoder_recreated_error');
            requestKeyframe('control_code_decoder_recreated_error');
          } else {
            switchToAvcAdapter('control_code_decoder_recreated_error');
          }
        }
      });
      decoder.configure(resetConfig);
      decoderConfigured = true;
      needsKeyFrame = true;
      lastAcceptedFrameSequence = Number(lastRenderedFrameSequence || 0);
      lastAcceptedFrameTimestamp = Number(lastRenderedFrameTimestamp || 0);
      lastAcceptedFrameReceivedAt = 0;
      lastAcceptedFrameQueuedAt = 0;
      lastAcceptedFrameVisualAgeMillis = 0;
      lastControlCodeDecoderBacklogResetKey = resetKey;
      sendVideoClientLog('control_code_decoder_backlog_reset', JSON.stringify({
        requestKey: accountPublicId(requestID),
        reason: reason || 'control_code',
        backlogReason: backlogReason || 'forced_result_marker',
        forced: Boolean(force),
        renderedFrameEpoch: Number(lastRenderedFrameEpoch || 0),
        renderedFrameSequence: Number(lastRenderedFrameSequence || 0)
      }));
      return true;
    } catch (error) {
      decoderConfigured = false;
      sendVideoClientLog('control_code_decoder_backlog_reset_failed', error && error.message || 'reset failed');
      return false;
    }
  }

  function requestControlCodeLowLatencyFrame(requestID, reason) {
    requestID = String(requestID || '').trim();
    const requestKey = `${requestID}:${reason || 'control_code_low_latency_frame'}`;
    if (!requestID || lastControlCodeLowLatencyFrameKey === requestKey) return false;
    if (!codeRequest || String(codeRequest.requestId || '').trim() !== requestID) return false;
    const status = String(codeRequest.status || '');
    if (status !== 'queued' && status !== 'running' && status !== 'succeeded') return false;
    lastControlCodeLowLatencyFrameKey = requestKey;
    // Keep the live socket intact for the control-code flow. Reconnecting here
    // preserves the popup/old frame while the new socket warms up, which can make the
    // browser miss the short generated-result window and force phone-side cleanup.
    // Only clear a genuinely stale decoder backlog after the generated-result marker exists.
    // The request-start path does no decoder reset or speculative keyframe work.
    const backlogReset = resetControlCodeDecoderBacklog(requestID, reason || 'control_code_low_latency', false);
    if (backlogReset) {
      // Keep the metadata queue in lockstep with the replacement decoder.
      clearFrameMetadata();
    }
    return requestKeyframeDebounced(reason || 'control_code_low_latency_frame', 0, true);
  }

  function publishStreamDebug() {
    const controlCodeCapture = lastControlCodeCaptureDebug;
    window.ticketStreamDebug = {
      pageVersion,
      configured,
      streamReady: document.body.dataset.streamReady,
      transport: 'https-websocket-h264',
      codec: decoderConfigured ? 'h264' : '',
      decoderMode,
      currentStreamEpoch,
      frameDependencyMode: String(lastDecoderConfig && lastDecoderConfig.frameDependencyMode || ''),
      lastPacketAt,
      lastDecodedFrameAt,
      lastPacketSequence,
      lastAcceptedFrameSequence,
      lastAcceptedFrameTimestamp,
      lastRenderedFrameEpoch,
      lastRenderedFrameSequence,
      lastRenderedFrameTimestamp,
      feedbackVersion: streamFeedbackVersion,
      sourceFps: Number(lastDecoderConfig && (lastDecoderConfig.sourceFps || lastDecoderConfig.fps) || 1),
      keyframeIntervalFrames: Number(lastDecoderConfig && lastDecoderConfig.keyframeIntervalFrames || 1),
      decoderQueue: Number(decoder && decoder.decodeQueueSize || 0),
      visualAgeMillis: Math.round(Number(lastRenderedFrameVisualAgeMillis || 0)),
      presentationCoalescedFrames,
      decoderRejectedFrames,
      resyncDroppedFrames,
      feedbackSentCount,
      feedbackSendFailureCount,
      needsKeyFrame,
      firstFrameReceived,
      hasRenderedFrame,
      hasFallbackFrame: fallbackFrameAvailable,
      lastFallbackFrameAt,
      streamResumeSpinnerVisible: Boolean(streamResumeSpinner && !streamResumeSpinner.hidden),
      latestStreamStatus,
      controlCodeCapture: lastControlCodeCaptureDebug
    };
    // Keep the capture decision inspectable without exposing the request id,
    // entered digits, or any rendered ticket content. This is also available
    // in restricted browser automation contexts where custom window fields
    // are not visible.
    if (document.body) {
      const captureStage = controlCodeCapture
        ? (controlCodeCapture.candidateAccepted
          ? 'accepted'
          : String(controlCodeCapture.candidateRejectedReason || 'waiting'))
        : 'idle';
      document.body.dataset.controlCodeCaptureStage = captureStage.slice(0, 80);
      document.body.dataset.controlCodeMarkerEpoch = String(Number(controlCodeCapture && controlCodeCapture.markerEpoch || 0));
      document.body.dataset.controlCodeMarkerSequence = String(Number(controlCodeCapture && controlCodeCapture.markerSequence || 0));
      document.body.dataset.controlCodeCandidateEpoch = String(Number(controlCodeCapture && controlCodeCapture.candidateFrameEpoch || 0));
      document.body.dataset.controlCodeCandidateSequence = String(Number(controlCodeCapture && controlCodeCapture.candidateFrameSequence || 0));
      document.body.dataset.controlCodeChipVisible = String(Boolean(controlCodeCapture && controlCodeCapture.generatedChipVisible));
      document.body.dataset.controlCodeCodeVisible = String(Boolean(controlCodeCapture && controlCodeCapture.generatedCodeVisible));
      document.body.dataset.controlCodeChipY = String(Number(controlCodeCapture && controlCodeCapture.generatedChipY || 0));
      document.body.dataset.controlCodeChipDarkRatio = String(Number(controlCodeCapture && controlCodeCapture.generatedChipDarkRatio || 0));
      document.body.dataset.controlCodeChipLightRatio = String(Number(controlCodeCapture && controlCodeCapture.generatedChipLightRatio || 0));
      document.body.dataset.controlCodeChipRows = String(Number(controlCodeCapture && controlCodeCapture.generatedChipRows || 0));
      document.body.dataset.controlCodeChipScore = String(Number(controlCodeCapture && controlCodeCapture.generatedChipScore || 0));
      document.body.dataset.controlCodePopupVisible = String(Boolean(controlCodeCapture && controlCodeCapture.popupVisible));
      document.body.dataset.controlCodePopupKeyboardVisible = String(Boolean(controlCodeCapture && (controlCodeCapture.popupKeyboardVisible || controlCodeCapture.keyboardVisible)));
      document.body.dataset.controlCodePopupGhostVisible = String(Boolean(controlCodeCapture && (controlCodeCapture.popupGhostVisible || controlCodeCapture.dialogGhostVisible)));
      document.body.dataset.controlCodePopupUnsafe = String(Boolean(controlCodeCapture && controlCodeCapture.unsafeOverlayVisible));
      document.body.dataset.controlCodeKeyboardLight = String(Number(controlCodeCapture && controlCodeCapture.keyboardLightCellRatio || 0));
      document.body.dataset.controlCodeKeyboardMean = String(Number(controlCodeCapture && controlCodeCapture.keyboardMean || 0));
      document.body.dataset.controlCodeKeyboardContrast = String(Number(controlCodeCapture && controlCodeCapture.keyboardContrastScore || 0));
      document.body.dataset.controlCodePopupLight = String(Number(controlCodeCapture && controlCodeCapture.popupLightCellRatio || 0));
      document.body.dataset.controlCodePopupDark = String(Number(controlCodeCapture && controlCodeCapture.popupDarkCellRatio || 0));
      document.body.dataset.controlCodePopupContrast = String(Number(controlCodeCapture && controlCodeCapture.popupContrastScore || 0));
      document.body.dataset.controlCodeDimMean = String(Number(controlCodeCapture && controlCodeCapture.dimOverlayMean || 0));
      document.body.dataset.controlCodeDimContrast = String(Number(controlCodeCapture && controlCodeCapture.dimOverlayContrastScore || 0));
      document.body.dataset.controlCodeOkOrange = String(Number(controlCodeCapture && controlCodeCapture.okButtonOrangeRatio || 0));
      document.body.dataset.controlCodeRendered = String(Boolean(hasRenderedFrame));
      document.body.dataset.streamLastAcceptedSequence = String(Number(lastAcceptedFrameSequence || 0));
      document.body.dataset.streamLastRenderedSequence = String(Number(lastRenderedFrameSequence || 0));
      document.body.dataset.streamFrameSequenceLag = String(Math.max(0, Number(lastAcceptedFrameSequence || 0) - Number(lastRenderedFrameSequence || 0)));
      document.body.dataset.streamPendingMetadata = String(Array.isArray(pendingFrameMetadata) ? pendingFrameMetadata.length : 0);
      document.body.dataset.streamDecoderQueue = String(Number(decoder && decoder.decodeQueueSize || 0));
      document.body.dataset.streamRenderedVisualAge = String(Math.round(Number(lastRenderedFrameVisualAgeMillis || 0)));
      document.body.dataset.streamAcceptedVisualAge = String(Math.round(Number(lastAcceptedFrameVisualAgeMillis || 0)));
    }
  }

  function readUint64(view, offset) {
    return view.getUint32(offset) * 4294967296 + view.getUint32(offset + 4);
  }

  function parseFrameEnvelope(raw) {
    const data = new Uint8Array(raw);
    const view = new DataView(raw);
    if (data.byteLength >= FRAME_ENVELOPE_HEADER_BYTES && view.getUint32(0) === FRAME_ENVELOPE_MAGIC) {
      const flags = view.getUint8(4);
      return {
        version: 'tsf2',
        kind: (flags & 1) === 1 ? 'key' : 'delta',
        epoch: readUint64(view, 5),
        sequence: readUint64(view, 13),
        timestamp: readUint64(view, 21),
        data: data.slice(FRAME_ENVELOPE_HEADER_BYTES)
      };
    }
    sendVideoClientLog('invalid_tsf2_frame', `bytes=${data.byteLength}`);
    showUnsupported('Video stream sent an invalid frame. Refresh and try again.');
    return null;
  }

  function isAppleWebKit() {
    const ua = navigator.userAgent || '';
    return /\b(iPad|iPhone|iPod)\b/.test(ua) || (/\bSafari\b/.test(ua) && !/\bChrome|Chromium|CriOS|FxiOS|Edg\//.test(ua));
  }

  function findStartCode(data, from) {
    for (let i = from; i + 3 < data.length; i += 1) {
      if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 1) return { index: i, length: 3 };
      if (i + 4 < data.length && data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 0 && data[i + 3] === 1) return { index: i, length: 4 };
    }
    return null;
  }

  function annexBNalUnits(data) {
    const units = [];
    let start = findStartCode(data, 0);
    while (start) {
      const nalStart = start.index + start.length;
      const next = findStartCode(data, nalStart);
      const nalEnd = next ? next.index : data.length;
      if (nalEnd > nalStart) {
        units.push(data.slice(nalStart, nalEnd));
      }
      start = next;
    }
    return units;
  }

  function avcDescriptionFromParameterSets(sps, pps) {
    if (!sps || sps.length < 4 || !pps || pps.length < 1) return null;
    const size = 11 + sps.length + pps.length;
    const out = new Uint8Array(size);
    let offset = 0;
    out[offset++] = 1;
    out[offset++] = sps[1];
    out[offset++] = sps[2];
    out[offset++] = sps[3];
    out[offset++] = 0xff;
    out[offset++] = 0xe1;
    out[offset++] = (sps.length >> 8) & 0xff;
    out[offset++] = sps.length & 0xff;
    out.set(sps, offset);
    offset += sps.length;
    out[offset++] = 1;
    out[offset++] = (pps.length >> 8) & 0xff;
    out[offset++] = pps.length & 0xff;
    out.set(pps, offset);
    return out;
  }

  function annexBToAvcSample(data) {
    const units = annexBNalUnits(data);
    if (!units.length) return null;
    let total = 0;
    for (const unit of units) total += 4 + unit.length;
    const out = new Uint8Array(total);
    let offset = 0;
    for (const unit of units) {
      out[offset++] = (unit.length >>> 24) & 0xff;
      out[offset++] = (unit.length >>> 16) & 0xff;
      out[offset++] = (unit.length >>> 8) & 0xff;
      out[offset++] = unit.length & 0xff;
      out.set(unit, offset);
      offset += unit.length;
    }
    return { sample: out, units };
  }

  function rememberAvcParameterSets(units) {
    for (const unit of units) {
      if (!unit.length) continue;
      const type = unit[0] & 0x1f;
      if (type === 7) avcSps = unit;
      if (type === 8) avcPps = unit;
    }
    if (!avcDescription && avcSps && avcPps) {
      avcDescription = avcDescriptionFromParameterSets(avcSps, avcPps);
    }
    return avcDescription;
  }

  function acceptFreshFrame(frame) {
    if (!frame) return false;
    const now = performance.now();
    lastPacketAt = now;
    if (frame.sequence && frame.sequence > lastPacketSequence) {
      lastPacketSequence = frame.sequence;
      lastPacketSequenceAdvancedAt = now;
    }
    if (frame.timestamp) {
      lastPacketTimestamp = frame.timestamp;
    }
    if (currentStreamEpoch && frame.epoch && frame.epoch !== currentStreamEpoch) {
      return false;
    }
    const frameDependencyMode = String(lastDecoderConfig && lastDecoderConfig.frameDependencyMode || '').toLowerCase();
    const allIntraConfigValid = frameDependencyMode === 'all_intra' &&
      Number(lastDecoderConfig && lastDecoderConfig.fps) === 1 &&
      Number(lastDecoderConfig && lastDecoderConfig.sourceFps) === 1 &&
      Number(lastDecoderConfig && lastDecoderConfig.keyframeIntervalFrames) === 1;
    if (!allIntraConfigValid) {
      needsKeyFrame = true;
      sendVideoClientLog('invalid_tsf2_frame', {
        reason: 'all_intra_config_rejected',
        epoch: Number(frame.epoch || 0),
        sequence: Number(frame.sequence || 0)
      });
      requestKeyframeDebounced('all_intra_config_rejected', recoveryKeyframeDebounceMs, false);
      scheduleStreamFeedback('all_intra_config_rejected');
      return false;
    }
    if (frame.kind !== 'key') {
      needsKeyFrame = true;
      sendVideoClientLog('invalid_tsf2_frame', {
        reason: 'all_intra_delta_rejected',
        epoch: Number(frame.epoch || 0),
        sequence: Number(frame.sequence || 0)
      });
      requestKeyframeDebounced('all_intra_delta_rejected', recoveryKeyframeDebounceMs, false);
      scheduleStreamFeedback('all_intra_delta_rejected');
      return false;
    }
    if (frame.sequence && frame.sequence <= lastAcceptedFrameSequence) {
      return false;
    }
    const frameEpoch = Number(frame.epoch || 0);
    if (!currentStreamEpoch && frameEpoch > 0) {
      // A warm join can intentionally receive a provisional decoder config
      // with epoch 0 while the server waits for a fresh independent frame.
      currentStreamEpoch = frameEpoch;
      if (lastDecoderConfig && Number(lastDecoderConfig.streamEpoch || 0) === 0) {
        lastDecoderConfig = { ...lastDecoderConfig, streamEpoch: frameEpoch, provisional: false };
      }
    }
    needsKeyFrame = false;
    if (frame.sequence) lastAcceptedFrameSequence = frame.sequence;
    if (frame.timestamp) lastAcceptedFrameTimestamp = frame.timestamp;
    const captureWallMillis = frame.timestamp ? frame.timestamp / 1000 : 0;
    lastAcceptedFrameReceivedAt = now;
    const calibratedServerNowMillis = Date.now() + (serverClockHasLiveSample ? serverClockSkewMs : 0);
    lastAcceptedFrameVisualAgeMillis = captureWallMillis > 0 ? Math.max(0, calibratedServerNowMillis - captureWallMillis) : 0;
    return true;
  }

  function queueFrameMetadata(frame) {
    const metadata = {
      epoch: Number(frame && frame.epoch || currentStreamEpoch || 0),
      sequence: Number(frame && frame.sequence || 0),
      timestamp: Number(frame && frame.timestamp || 0),
      keyFrame: frame && frame.kind === 'key',
      visualAgeMillis: Number(lastAcceptedFrameVisualAgeMillis || 0),
      visualAgeKnown: Boolean(serverClockHasLiveSample),
      receivedAt: lastAcceptedFrameReceivedAt,
      queuedAt: performance.now()
    };
    pendingFrameMetadata.push(metadata);
    const timestampKey = String(metadata.timestamp || 0);
    const bucket = pendingFrameMetadataByTimestamp.get(timestampKey) || [];
    bucket.push(metadata);
    pendingFrameMetadataByTimestamp.set(timestampKey, bucket);
    pendingFrameMetadataCount += 1;
    while (pendingFrameMetadataCount > streamIngressMetadataLimit) {
      const oldest = pendingFrameMetadata.shift();
      if (!oldest) break;
      const oldestBucket = pendingFrameMetadataByTimestamp.get(String(oldest.timestamp || 0));
      if (oldestBucket) {
        const index = oldestBucket.indexOf(oldest);
        if (index >= 0) oldestBucket.splice(index, 1);
        if (!oldestBucket.length) pendingFrameMetadataByTimestamp.delete(String(oldest.timestamp || 0));
      }
      pendingFrameMetadataCount -= 1;
    }
  }

  function shiftFrameMetadata(timestamp, allowFallback) {
    const timestampKey = String(Number(timestamp || 0));
    const bucket = pendingFrameMetadataByTimestamp.get(timestampKey);
    if (bucket && bucket.length) {
      const exact = bucket.shift();
      if (!bucket.length) pendingFrameMetadataByTimestamp.delete(timestampKey);
      const index = pendingFrameMetadata.indexOf(exact);
      if (index >= 0) pendingFrameMetadata.splice(index, 1);
      pendingFrameMetadataCount = Math.max(0, pendingFrameMetadataCount - 1);
      return exact;
    }
    if (allowFallback === false) return null;
    pendingFrameMetadataCount = Math.max(0, pendingFrameMetadataCount - (pendingFrameMetadata.length ? 1 : 0));
    return pendingFrameMetadata.shift() || {
      epoch: currentStreamEpoch,
      sequence: lastAcceptedFrameSequence,
      timestamp: lastAcceptedFrameTimestamp,
      visualAgeMillis: Number(lastAcceptedFrameVisualAgeMillis || 0),
      visualAgeKnown: Boolean(serverClockHasLiveSample),
      receivedAt: lastAcceptedFrameReceivedAt,
      queuedAt: lastAcceptedFrameQueuedAt
    };
  }

  function discardFrameMetadata(timestamp) {
    const timestampKey = String(Number(timestamp || 0));
    const bucket = pendingFrameMetadataByTimestamp.get(timestampKey);
    if (!bucket || !bucket.length) return;
    const discarded = bucket.pop();
    if (!bucket.length) pendingFrameMetadataByTimestamp.delete(timestampKey);
    const index = pendingFrameMetadata.indexOf(discarded);
    if (index >= 0) pendingFrameMetadata.splice(index, 1);
    pendingFrameMetadataCount = Math.max(0, pendingFrameMetadataCount - 1);
  }

  function clearFrameMetadata() {
    pendingFrameMetadata = [];
    pendingFrameMetadataByTimestamp.clear();
    pendingFrameMetadataCount = 0;
  }

  function controlCodeCapturePriorityActive() {
    if (controlCodeSubmitInFlight) return true;
    const request = codeRequest;
    if (!request) return false;
    const status = String(request.status || '');
    if (status === 'queued' || status === 'running') return true;
    const requestID = String(request.requestId || '').trim();
    return Boolean(requestID && controlCodeResultCaptureRequestID === requestID && controlCodeResultCapturedRequestID !== requestID);
  }

  function controlCodeHDRFreezeTargetActive() {
    return Boolean(controlCodeHDRFreezeTarget && controlCodeHDRFreezeTarget.requestId);
  }

  function controlCodeHDRFreezeTargetMatches(requestID, epoch, sequence) {
    const target = controlCodeHDRFreezeTarget;
    return Boolean(
      target &&
      target.requestId === String(requestID || '').trim() &&
      target.epoch === Number(epoch || 0) &&
      target.sequence === Number(sequence || 0)
    );
  }

  function settleControlCodeHDRFreezeWaiters(target, exact, reason) {
    if (!target || !target.waiters) return;
    const waiters = Array.from(target.waiters);
    target.waiters.clear();
    for (const waiter of waiters) {
      if (waiter.timer) clearTimeout(waiter.timer);
      try { waiter.resolve(Boolean(exact)); } catch (_) {}
    }
    if (!exact) target.failureReason = String(reason || 'exact_hdr_unavailable').slice(0, 80);
  }

  function clearControlCodeHDRFreezeTarget(reason) {
    const target = controlCodeHDRFreezeTarget;
    if (!target) return false;
    controlCodeHDRFreezeTarget = null;
    settleControlCodeHDRFreezeWaiters(target, false, reason || 'exact_hdr_cleared');
    return true;
  }

  function latchControlCodeHDRFreezeTarget(proof) {
    const requestID = String(proof && proof.requestId || '').trim();
    const epoch = Number(proof && proof.candidateFrameEpoch || 0);
    const sequence = Number(proof && proof.candidateFrameSequence || 0);
    const controller = experimentalClientHDRController;
    const snapshot = controller && typeof controller.snapshot === 'function' ? controller.snapshot() : null;
    if (!requestID || !(epoch > 0) || !(sequence > 0) ||
      !experimentalMediaState.enabled || experimentalMediaState.engine !== CLIENT_HDR_ENGINE ||
      !snapshot || !snapshot.active || experimentalMediaPresentationRegionBlocked ||
      experimentalMediaPresentationRecoveryPending || !experimentalHDRSurfacePresentationAllowed()) {
      return false;
    }
    if (controlCodeHDRFreezeTarget && !controlCodeHDRFreezeTargetMatches(requestID, epoch, sequence)) {
      clearControlCodeHDRFreezeTarget('exact_hdr_target_superseded');
    }
    if (!controlCodeHDRFreezeTarget) {
      controlCodeHDRFreezeTarget = {
        requestId: requestID,
        epoch,
        sequence,
        deadlineWallAt: Date.now() + CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS,
        exactPresented: false,
        failureReason: '',
        waiters: new Set()
      };
    }
    const target = controlCodeHDRFreezeTarget;
    if (snapshot.surfaceVisible && snapshot.presentationState === 'visible' &&
      Number(snapshot.epoch || 0) === epoch && Number(snapshot.sequence || 0) === sequence &&
      typeof controller.ensureExactProof === 'function' && controller.ensureExactProof(epoch, sequence)) {
      target.exactPresented = true;
      settleControlCodeHDRFreezeWaiters(target, true, 'exact_hdr_already_presented');
    }
    return true;
  }

  function observeControlCodeHDRPresentationMetric(event, detail) {
    const target = controlCodeHDRFreezeTarget;
    if (!target) return false;
    if (event === 'first_presented' || event === 'presented') {
      const epoch = Number(detail && detail.epoch || 0);
      const sequence = Number(detail && detail.sequence || 0);
      if (!controlCodeHDRFreezeTargetMatches(target.requestId, epoch, sequence) ||
        !detail || detail.surfaceVisible !== true || detail.presentationState !== 'visible') return false;
      const controller = experimentalClientHDRController;
      if (!controller || typeof controller.ensureExactProof !== 'function' ||
        !controller.ensureExactProof(epoch, sequence)) return false;
      target.exactPresented = true;
      target.failureReason = '';
      settleControlCodeHDRFreezeWaiters(target, true, 'exact_hdr_presented');
      return true;
    }
    if (event === 'fallback' || event === 'session_summary') {
      target.failureReason = String(detail && detail.reason || event).slice(0, 80);
      settleControlCodeHDRFreezeWaiters(target, false, target.failureReason);
    }
    return false;
  }

  function waitForControlCodeExactHDRPresentation(requestID, proof) {
    const epoch = Number(proof && proof.candidateFrameEpoch || 0);
    const sequence = Number(proof && proof.candidateFrameSequence || 0);
    const target = controlCodeHDRFreezeTarget;
    if (!target || !controlCodeHDRFreezeTargetMatches(requestID, epoch, sequence)) {
      return Promise.resolve(false);
    }
    if (target.exactPresented) return Promise.resolve(true);
    if (target.failureReason) return Promise.resolve(false);
    const remainingMillis = Math.max(0, Math.min(
      CLIENT_HDR_SETTLEMENT_TIMEOUT_MILLIS,
      Number(target.deadlineWallAt || 0) - Date.now()
    ));
    if (!(remainingMillis > 0)) {
      forceControlCodeResultSDRFallback('exact_hdr_timeout');
      return Promise.resolve(false);
    }
    return new Promise((resolve) => {
      const waiter = { resolve, timer: null };
      target.waiters.add(waiter);
      waiter.timer = setTimeout(() => {
        target.waiters.delete(waiter);
        forceControlCodeResultSDRFallback('exact_hdr_timeout');
        resolve(false);
      }, remainingMillis);
    });
  }

  function scheduleDecodedFrame(frame, source) {
    if (!frame) return;
    const decodedAtPerformanceMillis = performance.now();
    const metadata = shiftFrameMetadata(frame.timestamp, false);
    if (!metadata) {
      try { frame.close(); } catch (_) {}
      decoderRejectedFrames += 1;
      return;
    }
    lastDecodedFrameSequence = Number(metadata.sequence || lastDecodedFrameSequence || 0);
    if (!firstDecodedTraceSent) {
      firstDecodedTraceSent = true;
      sendVideoSocketClientLog('browser_first_frame_decoded', {
        frameEpoch: Number(metadata.epoch || 0),
        frameSequence: Number(metadata.sequence || 0),
        frameTimestamp: Number(metadata.timestamp || 0)
      }, decodedAtPerformanceMillis);
    }
    if (pendingPresentedFrame) {
      const priority = controlCodeCapturePriorityActive();
      const previous = pendingPresentedFrame;
      pendingPresentedFrame = null;
      if (priority) {
        renderDecodedFrame(previous.frame, previous.source, previous.metadata);
      } else {
        try { previous.frame.close(); } catch (_) {}
        presentationCoalescedFrames += 1;
      }
    }
    pendingPresentedFrame = { frame, source, metadata };
    if (presentationFrameHandle != null) return;
    const present = () => {
      presentationFrameHandle = null;
      const pending = pendingPresentedFrame;
      pendingPresentedFrame = null;
      if (!pending) return;
      renderDecodedFrame(pending.frame, pending.source, pending.metadata);
    };
    if (typeof requestAnimationFrame === 'function') {
      presentationFrameHandle = requestAnimationFrame(present);
    } else {
      presentationFrameHandle = setTimeout(present, 16);
    }
  }

  async function handleVideoSocketMessage(event) {
    if (typeof event.data === 'string') {
      let msg;
      try { msg = JSON.parse(event.data); } catch (_) { return; }
      if (!checkServerVersion(msg)) return;
      if (msg.type === 'config') {
        await configureDecoder(msg);
      }
      return;
    }
    if (!configured) return;
    const frame = parseFrameEnvelope(event.data);
    if (!acceptFreshFrame(frame)) {
      decoderRejectedFrames += 1;
      if (frame && frame.kind !== 'key' && needsKeyFrame) resyncDroppedFrames += 1;
      return;
    }
    lastAcceptedFrameQueuedAt = performance.now();
    const sourceFrameTooOld = serverClockHasLiveSample && Number(lastAcceptedFrameVisualAgeMillis || 0) > streamIngressFrameMaxAgeMs;
    if (Number(decoder && decoder.decodeQueueSize || 0) > streamDecoderQueueHardLimit || sourceFrameTooOld) {
      clearFrameMetadata();
      needsKeyFrame = true;
      const hardReason = sourceFrameTooOld ? 'visual_age_overflow' : 'decoder_queue_overflow';
      if (!resetDecoderForRecovery(hardReason)) {
        requestKeyframe(hardReason);
        scheduleStreamFeedback(hardReason);
      }
      return;
    }
    if (decoderMode === 'avc') {
      decodeAvcFrame(frame);
      return;
    }
    try {
      queueFrameMetadata(frame);
      try {
        decoder.decode(new EncodedVideoChunk({ type: frame.kind, timestamp: frame.timestamp, data: frame.data }));
      } catch (error) {
        discardFrameMetadata(frame.timestamp);
        throw error;
      }
    } catch (error) {
      sendVideoClientLog('decoder_decode_failed', error && error.message || 'decode failed');
      needsKeyFrame = true;
      if (!avcAdapterTried) {
        switchToAvcAdapter('decoder_decode_failed');
      } else {
        requestKeyframe('decoder_decode_failed');
      }
    }
  }

  function decodeAvcFrame(frame) {
    const converted = annexBToAvcSample(frame.data);
    if (!converted) {
      sendVideoClientLog('h264_avc_adapter_empty_frame', `sequence=${frame.sequence || 0}`);
      needsKeyFrame = true;
      requestKeyframe('h264_avc_adapter_empty_frame');
      return;
    }
    if (frame.kind === 'key') {
      rememberAvcParameterSets(converted.units);
    }
    if (!decoder) {
      if (frame.kind !== 'key' || !avcDescription) {
        needsKeyFrame = true;
        requestKeyframe('h264_avc_adapter_waiting_sps_pps');
        return;
      }
      configureAvcDecoderFromDescription(lastDecoderConfig || {}, avcDescription);
    }
    try {
      queueFrameMetadata(frame);
      decoder.decode(new EncodedVideoChunk({ type: frame.kind, timestamp: frame.timestamp, data: converted.sample }));
    } catch (error) {
      discardFrameMetadata(frame.timestamp);
      sendVideoClientLog('decoder_decode_failed', error && error.message || 'decode failed');
      needsKeyFrame = true;
      requestKeyframe('h264_avc_decode_failed');
    }
  }

  function handleStreamStatus(msg) {
    latestStreamStatus = msg;
    lastStreamStatusAt = performance.now();
    const freshness = updateStreamFreshnessStatus('stream_status');
    if (hasRenderedFrame && (streamStatusStale(msg) || !freshness.liveLabeled)) {
      preserveCurrentFrame('stream_status_stale');
      redrawPreservedFrame();
      showStreamResumeSpinner();
    } else if (hasRenderedFrame) {
      hideStreamResumeSpinner();
    }
    // Surface the server's phone-engine readiness verdict to the user.
    // The server reports streamVerdict in {live, idle, preparing_phone,
    // waiting_keyframe, stale_recovering, browser_decode_recovering,
    // timing_uncertain}. When the phone is still warming up, the user
    // sees a meaningful status instead of a generic spinner.
    const verdict = String(msg.streamVerdict || '');
    if (!hasRenderedFrame || !freshness.liveLabeled) {
      if (verdict === 'preparing_phone' || verdict === 'waiting_keyframe' || verdict === 'stale_recovering') {
        const phoneStreamState = String(msg.phoneStreamState || '');
        if (phoneStreamState !== 'streaming') {
          setStatus('Tālrunis gatavojas...');
        }
      }
    }
    publishStreamDebug();
  }

  function decodedFrameHDRMetadata(frameMetadata, presentationOrdinal, renderedAt) {
    const captureWallMillis = Number(frameMetadata && frameMetadata.timestamp || 0) / 1000;
    const metadataAge = frameMetadata && frameMetadata.visualAgeKnown
      ? Math.max(0, Number(frameMetadata.visualAgeMillis || 0))
      : (captureWallMillis > 0 && serverClockHasLiveSample
        ? Math.max(0, Date.now() + serverClockSkewMs - captureWallMillis)
        : 0);
    const receivedAt = Number(frameMetadata && frameMetadata.receivedAt || lastAcceptedFrameReceivedAt || renderedAt);
    return {
      epoch: Number(frameMetadata && frameMetadata.epoch || 0),
      sequence: Number(frameMetadata && frameMetadata.sequence || 0),
      presentationOrdinal: Number(presentationOrdinal || 0),
      timestamp: Number(frameMetadata && frameMetadata.timestamp || 0),
      visualAgeMillis: metadataAge + Math.max(0, renderedAt - receivedAt),
      renderedAt,
      offeredAt: renderedAt,
      offeredWallMillis: Date.now()
    };
  }

  function clientHDRCanCoordinateDecodedFrame() {
    return Boolean(
      experimentalClientHDRController && experimentalMediaState.enabled &&
      experimentalMediaState.engine === CLIENT_HDR_ENGINE &&
      !experimentalMediaPresentationRegionBlocked && !experimentalMediaPresentationRecoveryPending &&
      experimentalHDRSurfacePresentationAllowed() &&
      !controlCodeCapturePriorityActive() &&
      !controlCodeHDRFreezeTargetActive() &&
      typeof experimentalClientHDRController.canCoordinateSDRFrame === 'function' &&
      experimentalClientHDRController.canCoordinateSDRFrame()
    );
  }

  function coordinatedDecodedFrameCanCommit(candidate) {
    const epoch = Number(candidate && candidate.epoch || 0);
    const sequence = Number(candidate && candidate.sequence || 0);
    const activeEpoch = Number(currentStreamEpoch || epoch);
    if (!(epoch > 0) || !(sequence > 0) || (activeEpoch > 0 && activeEpoch !== epoch)) return false;
    if (Number(lastRenderedFrameEpoch || 0) === epoch && Number(lastRenderedFrameSequence || 0) >= sequence) return false;
    return true;
  }

  function renderedDecodedFrameCanCommit(candidate, expectedDecoderGeneration, expectedSDRRenderSerial) {
    const epoch = Number(candidate && candidate.epoch || 0);
    const sequence = Number(candidate && candidate.sequence || 0);
    const presentationOrdinal = Number(candidate && candidate.presentationOrdinal || 0);
    const timestamp = Number(candidate && candidate.timestamp || 0);
    return Boolean(
      expectedDecoderGeneration === decoderGeneration &&
      expectedSDRRenderSerial === authoritativeSDRRenderSerial &&
      epoch > 0 && sequence > 0 && presentationOrdinal > 0 &&
      Number(lastRenderedFrameEpoch || 0) === epoch &&
      Number(lastRenderedFrameSequence || 0) === sequence &&
      Number(lastRenderedPresentationOrdinal || 0) === presentationOrdinal &&
      Number(lastRenderedFrameTimestamp || 0) === timestamp
    );
  }

  function renderDecodedFrame(frame, source) {
    const frameMetadata = (arguments.length > 2 && arguments[2]) || shiftFrameMetadata(frame && frame.timestamp);
    const renderOptions = (arguments.length > 3 && arguments[3]) || {};
    const closeFrameWhenDone = renderOptions.closeFrame !== false;
    const controlCodeCapturePriority = controlCodeCapturePriorityActive();
    const controlCodeHDRFrozen = controlCodeHDRFreezeTargetActive();
    if (!renderOptions.coordinatedCommit && !controlCodeCapturePriority &&
      !controlCodeHDRFrozen && clientHDRCanCoordinateDecodedFrame()) {
      const offeredAt = performance.now();
      const coordinatedDecoderGeneration = decoderGeneration;
      const hdrMetadata = decodedFrameHDRMetadata(
        frameMetadata,
        Number(lastRenderedPresentationOrdinal || 0) + 1,
        offeredAt
      );
      const accepted = experimentalClientHDRController.offerFrame(frame, hdrMetadata, {
        commitSDR: (ownedFrame, candidate) => {
          if (coordinatedDecoderGeneration !== decoderGeneration) return false;
          if (!coordinatedDecodedFrameCanCommit(candidate)) return false;
          const committed = renderDecodedFrame(ownedFrame, source, frameMetadata, {
            coordinatedCommit: true,
            closeFrame: false
          });
          if (!committed || typeof committed !== 'object') return false;
          Object.assign(candidate, committed);
          return committed;
        }
      });
      if (accepted) {
        try { frame.close(); } catch (_) {}
        return hdrMetadata;
      }
    }
    try {
      ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
      lastRenderedPresentationOrdinal += 1;
      authoritativeSDRRenderSerial += 1;
      lastFrameAt = performance.now();
      lastDecodedFrameAt = lastFrameAt;
      lastDecodedFrameSequence = Number(frameMetadata.sequence || lastAcceptedFrameSequence || 0);
      lastRenderedFrameReceivedAt = Number(frameMetadata.receivedAt || lastAcceptedFrameReceivedAt || lastFrameAt);
      lastRenderedFrameQueuedAt = Number(frameMetadata.queuedAt || lastAcceptedFrameQueuedAt || lastFrameAt);
      lastRenderedFrameRenderedAt = lastFrameAt;
      lastRenderedFrameEpoch = Number(frameMetadata.epoch || 0);
      lastRenderedFrameSequence = Number(frameMetadata.sequence || 0);
      lastRenderedFrameTimestamp = Number(frameMetadata.timestamp || 0);
      const hdrMetadata = decodedFrameHDRMetadata(frameMetadata, lastRenderedPresentationOrdinal, lastFrameAt);
      lastRenderedFrameVisualAgeMillis = hdrMetadata.visualAgeMillis;
      if (typeof experimentalClientHDRController !== 'undefined' && experimentalClientHDRController &&
        typeof experimentalMediaState !== 'undefined' && experimentalMediaState.enabled &&
        experimentalMediaState.engine === CLIENT_HDR_ENGINE &&
        !experimentalMediaPresentationRegionBlocked && !experimentalMediaPresentationRecoveryPending &&
        experimentalHDRSurfacePresentationAllowed()) {
        if (!renderOptions.coordinatedCommit && !controlCodeHDRFreezeTargetActive()) {
          const priorityDecoderGeneration = decoderGeneration;
          const prioritySDRRenderSerial = authoritativeSDRRenderSerial;
          const priorityCommitOptions = controlCodeCapturePriority ? {
            commitSDR: (_ownedFrame, candidate) => {
              if (!renderedDecodedFrameCanCommit(
                candidate,
                priorityDecoderGeneration,
                prioritySDRRenderSerial
              )) return false;
              return Object.assign({}, hdrMetadata);
            }
          } : undefined;
          const hdrOffered = experimentalClientHDRController.offerFrame(frame, hdrMetadata, priorityCommitOptions);
          if (hdrOffered) {
            if (!controlCodeCapturePriority) experimentalClientHDRController.noteSDRFrame(hdrMetadata);
          } else if (experimentalClientHDRController.snapshot().active) {
            offerClientHDRCanvasFrame(
              experimentalClientHDRController,
              canvas,
              hdrMetadata,
              window,
              priorityCommitOptions
            );
          }
        }
      }
      firstFrameReceived = true;
      hasRenderedFrame = true;
      resetFirstFrameServerRecovery();
      const firstFrameDetail = {
        visualAgeMillis: Math.round(lastRenderedFrameVisualAgeMillis),
        frameEpoch: lastRenderedFrameEpoch,
        frameSequence: lastRenderedFrameSequence,
        browserReceiveToDecodeMillis: lastRenderedFrameQueuedAt > 0 && lastRenderedFrameReceivedAt > 0
          ? Math.round(Math.max(0, lastRenderedFrameQueuedAt - lastRenderedFrameReceivedAt))
          : -1,
        decodeToRenderMillis: lastRenderedFrameQueuedAt > 0
          ? Math.round(Math.max(0, lastRenderedFrameRenderedAt - lastRenderedFrameQueuedAt))
          : -1
      };
      if (!firstRenderedTraceSent) {
        firstRenderedTraceSent = true;
        sendVideoSocketClientLog('stream_first_rendered_frame', firstFrameDetail, lastFrameAt);
      }
      maybeCaptureControlCodeResultImage();
      hideEmpty();
      updateStreamFreshnessStatus('frame_rendered');
      noteExperimentalMediaForegroundFrame();
      renderTicketInteraction(currentState && currentState.ticketInteraction);
      observeTicketCurrentProofFrame();
      updateControlCodeSubmitAvailability();
      publishStreamDebug();
      scheduleStreamFeedback('frame_presented');
      return hdrMetadata;
    } catch (error) {
      sendVideoClientLog('decoded_frame_render_failed', `${source || 'decoder'}:${error && error.message || 'draw failed'}`);
      needsKeyFrame = true;
      preserveCurrentFrame('decoded_frame_render_failed');
      showStreamRecovery();
      requestKeyframe('decoded_frame_render_failed');
      if (renderOptions.coordinatedCommit && experimentalClientHDRController) {
        experimentalClientHDRController.markSDRStale('coordinated_sdr_commit_failed');
      }
      return false;
    } finally {
      if (closeFrameWhenDone) {
        try { frame.close(); } catch (_) {}
      }
    }
  }

  async function configureDecoder(config, options) {
    options = options || {};
    const configureGeneration = ++decoderConfigureGeneration;
    const frameDependencyMode = String(config.frameDependencyMode || '').toLowerCase();
    if (frameDependencyMode !== 'all_intra' || Number(config.fps) !== 1 ||
      Number(config.sourceFps) !== 1 || Number(config.keyframeIntervalFrames) !== 1) {
      lastDecoderConfig = { ...config, frameDependencyMode };
      needsKeyFrame = true;
      sendVideoClientLog('invalid_tsf2_frame', { reason: 'all_intra_config_rejected' });
      closeDecoder();
      showStreamRecovery();
      scheduleStreamFeedback('all_intra_config_rejected');
      return;
    }
    if (!('VideoDecoder' in window) || !('EncodedVideoChunk' in window)) {
      showUnsupported('Šī pārlūkprogramma neatbalsta H.264 video dekodēšanu šajā lapā.');
      return;
    }
    const codec = String(config.codec || '');
    const transport = String(config.transport || '');
    const h264 = codec.startsWith('avc1') || transport === 'h264-annexb' || transport === 'ffmpeg-h264-annexb';
    if (!h264) {
      showUnsupported('Šī straume nav H.264 video.');
      return;
    }
    const width = Number(config.width || 0);
    const height = Number(config.height || 0);
    if (!width || !height) {
      showUnsupported('Video konfigurācija nav pilnīga.');
      return;
    }
    const preferAvc = Boolean(options.preferAvc) || isAppleWebKit();
    const decoderConfig = {
      codec,
      codedWidth: width,
      codedHeight: height,
      avc: { format: 'annexb' },
      optimizeForLatency: true
    };
    let supported = false;
    if (!preferAvc) {
      try {
        const result = await VideoDecoder.isConfigSupported(decoderConfig);
        supported = Boolean(result && result.supported);
      } catch (error) {
        supported = false;
      }
    }
    if (configureGeneration !== decoderConfigureGeneration) return;
    if (!supported && !preferAvc) {
      showUnsupported('Šī pārlūkprogramma nevar atvērt H.264 biļetes video.');
      return;
    }
    const previousSequence = lastAcceptedFrameSequence;
    const previousTimestamp = lastAcceptedFrameTimestamp;
    const shouldPreserveFrame = Boolean(options.preserveFrame) || hasRenderedFrame || fallbackFrameAvailable;
    if (shouldPreserveFrame) {
      preserveCurrentFrame('configure_decoder');
    }
    lastDecoderConfig = { ...config, frameDependencyMode: 'all_intra' };
    closeDecoder();
    decoderMode = preferAvc ? 'avc' : 'annexb';
    if (preferAvc) {
      avcAdapterTried = true;
      avcDescription = null;
      avcSps = null;
      avcPps = null;
    }
    canvas.width = width;
    canvas.height = height;
    ctx.imageSmoothingEnabled = false;
    if (shouldPreserveFrame) {
      redrawPreservedFrame();
    }
    streamSize = {
      width,
      height,
      sourceHeight: Number(config.sourceHeight || height) || height,
      sourceTopCrop: Math.max(0, Number(config.sourceTopCrop || 0) || 0),
      sourceVisibleHeight: Number(config.sourceVisibleHeight || config.sourceHeight || height) || height
    };
    currentStreamEpoch = Number(config.streamEpoch || 0);
    lastAcceptedFrameSequence = options.preserveSequence ? previousSequence : 0;
    lastAcceptedFrameTimestamp = options.preserveSequence ? previousTimestamp : 0;
    clearFrameMetadata();
    lastDecodedFrameSequence = options.preserveSequence ? previousSequence : 0;
    needsKeyFrame = true;
    configured = true;
    configuredAt = performance.now();
    firstFrameReceived = false;
    resizeCanvasBox();
    if (preferAvc) {
      decoderConfigured = false;
      publishStreamDebug();
      showStreamWaiting('Gaida pirmo video kadru...');
      requestKeyframe(options.requestReason || 'config_received_avc');
      keepFirstScreenPinned();
      sendVideoClientLog('h264_decoder_mode', 'avc_adapter');
      return;
    }
    const decoderInstanceGeneration = decoderGeneration;
    decoder = new VideoDecoder({
      output: (frame) => {
        if (decoderInstanceGeneration !== decoderGeneration) {
          try { frame.close(); } catch (_) {}
          return;
        }
        scheduleDecodedFrame(frame, 'annexb');
      },
      error: (error) => {
        if (decoderInstanceGeneration !== decoderGeneration) return;
        reportDecoderError(error, 'annexb');
        needsKeyFrame = true;
        switchToAvcAdapter('decoder_error');
      }
    });
    decoder.configure(decoderConfig);
    const configuredAtPerformanceMillis = performance.now();
    decoderConfigured = true;
    sendVideoSocketClientLog('browser_configured', {
      codec,
      width,
      height,
      streamEpoch: currentStreamEpoch,
      mode: 'annexb'
    }, configuredAtPerformanceMillis);
    publishStreamDebug();
    showStreamWaiting('Gaida pirmo video kadru...');
    requestKeyframe(options.requestReason || 'config_received');
    keepFirstScreenPinned();
  }

  function configureAvcDecoderFromDescription(config, description) {
    const width = Number(config.width || canvas.width || 0);
    const height = Number(config.height || canvas.height || 0);
    const codec = String(config.codec || 'avc1.42C028');
    preserveCurrentFrame('configure_avc_decoder');
    closeDecoder();
    decoderMode = 'avc';
    const decoderInstanceGeneration = decoderGeneration;
    decoder = new VideoDecoder({
      output: (frame) => {
        if (decoderInstanceGeneration !== decoderGeneration) {
          try { frame.close(); } catch (_) {}
          return;
        }
        scheduleDecodedFrame(frame, 'avc');
      },
      error: (error) => {
        if (decoderInstanceGeneration !== decoderGeneration) return;
        reportDecoderError(error, 'avc');
        needsKeyFrame = true;
        resetDecoderForRecovery('decoder_error_avc');
        requestKeyframe('decoder_error_avc');
      }
    });
    try {
      decoder.configure({ codec, codedWidth: width, codedHeight: height, description, optimizeForLatency: true });
    } catch (error) {
      closeDecoder();
      showUnsupported('Šī pārlūkprogramma nevar atvērt H.264 biļetes video.');
      sendVideoClientLog('h264_avc_config_failed', error && error.message || 'avc config failed');
      return;
    }
    const configuredAtPerformanceMillis = performance.now();
    decoderConfigured = true;
    sendVideoSocketClientLog('browser_configured', {
      codec,
      width,
      height,
      streamEpoch: currentStreamEpoch,
      mode: 'avc'
    }, configuredAtPerformanceMillis);
    sendVideoClientLog('h264_decoder_mode', 'avc_adapter_configured');
    publishStreamDebug();
  }

  function switchToAvcAdapter(reason) {
    if (!lastDecoderConfig) {
      requestKeyframe(reason);
      return;
    }
    if (avcAdapterTried && decoderMode === 'avc') {
      resetDecoderForRecovery(reason);
      requestKeyframe(reason);
      return;
    }
    avcAdapterTried = true;
    sendVideoClientLog('h264_decoder_recovery_avc_adapter', reason);
    configureDecoder(lastDecoderConfig, {
      preserveFrame: true,
      preserveSequence: false,
      preferAvc: true,
      requestReason: `${reason}_avc_adapter`
    }).catch((error) => sendVideoClientLog('decoder_recovery_config_failed', error && error.message || 'decoder recovery failed'));
  }

	  async function fetchAuthSessionToken() {
	    if (!usesDirectSpacetimeAuth()) throw new Error('Direct ticket state is disabled.');
	    const response = await fetch('/api/v1/auth/session', { cache: 'no-store' });
	    const payload = await response.json().catch(() => ({}));
	    if (payload && payload.authenticated && payload.spacetime && payload.spacetime.authRequired) {
	      if (String((cfg.auth && cfg.auth.mode) || '').toLowerCase() === 'spacetime') beginSpacetimeLogin(authReturnTarget());
	      throw new Error('Direct ticket session refresh required.');
	    }
	    if (!response.ok || !payload.ok || !payload.spacetime || !payload.spacetime.token) {
	      throw new Error(payload.message || payload.error || 'SpacetimeAuth session is unavailable.');
	    }
    rememberSpacetimeToken(payload.spacetime.token);
    return payload.spacetime.token;
  }

  async function spacetimeToken() {
    if (directSpacetimeToken && !spacetimeTokenExpired(directSpacetimeToken)) return directSpacetimeToken;
    if (directSpacetimeToken) {
      clearLocalAuthState();
    }
    return fetchAuthSessionToken();
  }

  async function loadSpacetimeClientScript() {
    if (window.TicketSpacetime) return window.TicketSpacetime;
    if (spacetimeClientScriptPromise) return spacetimeClientScriptPromise;
    spacetimeClientScriptPromise = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      const assetVersion = encodeURIComponent(String(cfg.assetVersion || pageVersion || Date.now()));
      script.src = `/static/spacetime-client.js?v=${assetVersion}`;
      script.async = true;
      script.onload = () => {
        if (window.TicketSpacetime) {
          resolve(window.TicketSpacetime);
          return;
        }
        reject(new Error('Spacetime client did not initialize.'));
      };
      script.onerror = () => reject(new Error('Spacetime client failed to load.'));
      document.head.appendChild(script);
    });
    return spacetimeClientScriptPromise;
  }

  function recoverExpiredSpacetimeConnection(client, reason) {
    if (client !== spacetimeClient || spacetimeExpiredTokenRefreshPromise) return false;
    if (!directSpacetimeToken || !spacetimeTokenExpired(directSpacetimeToken)) return false;

    spacetimeClient = null;
    publishSpacetimeClientStatus('offline');
    try {
      if (client && typeof client.close === 'function') client.close();
    } catch (_) {}
    clearLocalAuthState();
    spacetimeDirectUnavailableLogged = false;

    spacetimeExpiredTokenRefreshPromise = (async () => {
      try {
        await fetchAuthSessionToken();
        if (!idleDisconnected) await connectSpacetimeState();
      } catch (error) {
        clientLog('spacetime_token_refresh_failed', `${reason || 'connection_failed'}:${error && error.message || 'failed'}`);
      } finally {
        spacetimeExpiredTokenRefreshPromise = null;
      }
    })();
    return true;
  }

  async function connectSpacetimeState() {
    if (idleDisconnected) return;
    if (!usesDirectSpacetimeAuth() || spacetimeClient) return;
    if (spacetimeClientConnectPromise) return spacetimeClientConnectPromise;
    spacetimeClientConnectPromise = (async () => {
      if (idleDisconnected || !usesDirectSpacetimeAuth() || spacetimeClient) return;
      let token = '';
      try {
        await loadSpacetimeClientScript();
        token = await spacetimeToken();
      } catch (error) {
        if (!spacetimeDirectUnavailableLogged) {
          spacetimeDirectUnavailableLogged = true;
          clientLog('spacetime_direct_unavailable', error && error.message);
        }
		setTimeout(() => { if (!idleDisconnected && !spacetimeClient) connectSpacetimeState().catch(() => {}); }, 1000);
        return;
      }
      if (spacetimeClient) return;
      const st = cfg.spacetime || {};
      const client = window.TicketSpacetime.create({
        host: st.host || 'https://maincloud.spacetimedb.com',
        database: st.database || '',
        token,
        ticketId: cfg.ticketId || 'vivi-default',
        backendId: cfg.backendId || 'pixel',
        sessionId: cfg.sessionId || '',
        email: cfg.email || '',
        accountScopeId: cfg.accountScopeId || ''
      }, {
        onState: (state) => {
          currentState = state;
          rememberServerClock(currentState);
          if (spacetimeStateFresh) {
            observeExperimentalHDREngine(state);
            observeExperimentalHDRBoost(state);
            experimentalMediaPreferenceController.observe(state && state.memberHDR ? state.memberHDR.enabled : null);
          }
          renderState();
        },
        onSnapshotApplied: () => {
          markSpacetimeStateFresh();
        },
        onStatus: (status, detail) => {
          if (client !== spacetimeClient) return;
          if (status === 'hdr_refresh_failed') {
            clientLog('state_failed', 'hdr_preference_refresh_failed');
            return;
          }
          if (status === 'hdr_engine_refresh_failed') {
            clientLog('state_failed', 'hdr_engine_refresh_failed');
            return;
          }
          if (status === 'hdr_boost_refresh_failed') {
            clientLog('state_failed', 'hdr_boost_refresh_failed');
            return;
          }
          if (status === 'connecting' || status === 'reconnecting' || status === 'offline') {
            markSpacetimeStateUnconfirmed(`spacetime_${status}`);
            reconcileClientHDRStreamContinuity(`spacetime_${status}`, 'sdr_stream_unavailable');
          }
          publishSpacetimeClientStatus(status);
          if (status === 'live') {
    flushClientLogs();
          }
          updateControlCodeSubmitAvailability();
          if (detail) clientLog('spacetime_client_status', `${status}:${detail}`);
          if (status === 'offline' || status === 'reconnecting') {
            recoverExpiredSpacetimeConnection(client, status);
          }
        }
      });
      spacetimeClient = client;
      try {
        client.connect();
      } catch (error) {
        if (spacetimeClient === client) spacetimeClient = null;
        publishSpacetimeClientStatus('offline');
        try { client.close(); } catch (_) {}
        throw error;
      }
    })();
    try {
      await spacetimeClientConnectPromise;
    } finally {
      spacetimeClientConnectPromise = null;
    }
  }

  async function refreshSpacetimeState(reason) {
    if (idleDisconnected || !usesDirectSpacetimeAuth()) return;
    markSpacetimeStateUnconfirmed(reason || 'spacetime_state_refresh');
    if (spacetimeClient && typeof spacetimeClient.refresh === 'function') {
      spacetimeClient.refresh();
      return;
    }
    await connectSpacetimeState();
  }

  function refreshSpacetimeStateAfterResume(reason) {
    const status = String(spacetimeClientStatus || 'idle');
    const liveSubscription = Boolean(spacetimeClient && spacetimeStateFresh &&
      !['idle', 'connecting', 'reconnecting', 'offline'].includes(status));
    if (liveSubscription) {
      // A healthy direct subscription is already receiving the database
      // projection. Rebuilding it on every focus/visibility event caused the
      // controls to disappear until a brand-new snapshot arrived.
      renderState();
      clientLog('spacetime_resume_reused', reason || 'visibility_resume');
      return Promise.resolve(false);
    }
    return refreshSpacetimeState(reason || 'visibility_resume').then(() => true);
  }

  async function runSpacetimeMutation(action, reason) {
    await connectSpacetimeState();
    if (!spacetimeClient) throw new Error('Spacetime connection is unavailable.');
    await action(spacetimeClient);
    flushClientLogs();
  }

  function userActivityTickEligible() {
    return document.visibilityState === 'visible' &&
      !idleDisconnected &&
      window.navigator.onLine !== false &&
      spacetimeClientStatus === 'live' &&
      Boolean(spacetimeClient && typeof spacetimeClient.recordActivityTick === 'function');
  }

  async function recordUserActivityTick() {
    if (activityTickInFlight || !userActivityTickEligible()) return false;
    const client = spacetimeClient;
    activityTickInFlight = true;
    try {
      await client.recordActivityTick();
      return true;
    } catch (_) {
      return false;
    } finally {
      activityTickInFlight = false;
    }
  }

  function clearUserActivityTickTimer() {
    if (activityTickTimer === null) return;
    clearTimeout(activityTickTimer);
    activityTickTimer = null;
  }

  function refreshUserActivityTickSchedule() {
    clearUserActivityTickTimer();
    if (!userActivityTickEligible()) return false;
    const scheduledAt = Date.now();
    activityTickTimer = setTimeout(() => {
      activityTickTimer = null;
      const elapsed = Date.now() - scheduledAt;
      const wokeLate = elapsed > activityTickIntervalMs + activityTickMaximumDelayMs;
      if (elapsed < activityTickIntervalMs || wokeLate || !userActivityTickEligible()) {
        refreshUserActivityTickSchedule();
        return;
      }
      refreshUserActivityTickSchedule();
      void recordUserActivityTick();
    }, activityTickIntervalMs);
    return true;
  }

  function publishStreamFocus(active, reason) {
    runSpacetimeMutation((client) => client.setStreamFocus(Boolean(active), reason || (active ? 'browser_visible' : 'browser_hidden')), reason || 'stream_focus')
      .catch((error) => clientLog('stream_focus_failed', `${reason || 'focus'}:${error && error.message || 'failed'}`));
  }

  function refreshMemberLimitProjection(reason) {
    runSpacetimeMutation((client) => client.refreshLimitState(), reason || 'member_limit_refresh')
      .catch((error) => clientLog('member_limit_refresh_failed', `${reason || 'refresh'}:${error && error.message || 'failed'}`));
  }

  function sanitizeControlDigits(value) {
    return String(value || '').replace(/\D/g, '');
  }

  function controlCodeStatusText(status, reason) {
    switch (status) {
    case 'queued':
      return localizePublicMessage(reason || 'waiting_for_stream_recovery');
    case 'running':
      return 'Tālrunis veido kodu';
    case 'succeeded':
      return 'Kods gatavs';
    case 'failed':
      return localizePublicMessage(reason || 'Kodu neizdevās izveidot');
    case 'expired':
      return 'Kods paslēpts';
    case 'closed':
      return 'Kods aizvērts';
    default:
      return 'Gatavs';
    }
  }

  function controlCodeStatusRank(status) {
    if (status === 'queued') return 1;
    if (status === 'running') return 2;
    if (status === 'succeeded' || status === 'failed' || status === 'expired' || status === 'closed') return 4;
    return 0;
  }

  function controlCodeDetailText(request) {
    if (!request) return 'Ievadi 2-8 ciparus, tālrunis kodu izveidos automātiski.';
    if (request.status === 'queued') {
      const position = Number(request.queuePosition || 0);
      if (position > 1) return `Rindā: ${position}. vieta`;
      return localizePublicMessage(request.reason || request.message || 'waiting_for_stream_recovery');
    }
    if (request.status === 'running') return 'Tālrunis īsi atver koda logu un atgriezīsies pie biļetes.';
    if (request.status === 'succeeded') return 'Rezultāts redzams tikai tev 60 sekundes vai līdz to aizvērsi.';
    if (request.status === 'failed') return localizePublicMessage(request.reason || request.message || 'Kodu neizdevās izveidot');
    if (request.status === 'expired' || request.status === 'closed') return 'Vari pieprasīt jaunu kodu.';
    return 'Ievadi 2-8 ciparus, tālrunis kodu izveidos automātiski.';
  }

  function scheduleControlCodeTicker(request) {
    if (codeResultTickTimer) {
      clearInterval(codeResultTickTimer);
      codeResultTickTimer = null;
    }
    codeResultTimer.textContent = '';
    if (!request || request.status !== 'succeeded') return;
    const expiresAt = Date.parse(request.resultExpiresAt || '');
    if (!Number.isFinite(expiresAt)) return;
    const requestID = request.requestId;
    const refresh = () => {
      if (!codeRequest || codeRequest.requestId !== requestID || codeRequest.status !== 'succeeded') {
        if (codeResultTickTimer) {
          clearInterval(codeResultTickTimer);
          codeResultTickTimer = null;
        }
        return;
      }
      const remainingMs = expiresAt - (Date.now() + serverClockSkewMs);
      if (remainingMs <= 0) {
        if (codeResultTickTimer) {
          clearInterval(codeResultTickTimer);
          codeResultTickTimer = null;
        }
        codeResultTimer.textContent = '';
        closeCurrentControlCode(false);
        return;
      }
      codeResultTimer.textContent = `${Math.ceil(remainingMs / 1000)}s`;
    };
    refresh();
    codeResultTickTimer = setInterval(refresh, 1000);
  }

  function setControlCodeResultVisible(visible) {
    const wasVisible = document.body.classList.contains('control-code-result-visible');
    if (visible) {
      // The controls live below the full-height stream stage. A control-code
      // request can therefore finish while that stage is above the viewport.
      // Move the stage back into view in the same task that reveals the local
      // result so the paint handshake never acknowledges an off-screen image.
      document.body.classList.remove('details-visible');
      if (panel) panel.setAttribute('aria-hidden', 'true');
      stage.scrollIntoView({ block: 'start', inline: 'nearest', behavior: 'auto' });
    }
    codeResultArea.hidden = !visible;
    document.body.classList.toggle('control-code-result-visible', Boolean(visible));
    if (Boolean(visible) !== wasVisible) {
      synchronizeExperimentalHDRSurfaceRegion(
        !experimentalHDRSurfacePresentationAllowed(),
        visible ? 'control_code_result_visible' : 'control_code_result_hidden',
        { foregroundConfirmed: !visible }
      );
    }
    updateControlCodeSubmitAvailability();
  }

  function forceControlCodeResultSDRFallback(reason) {
    const fallbackReason = String(reason || 'exact_hdr_unavailable').slice(0, 80);
    const hadTarget = clearControlCodeHDRFreezeTarget(fallbackReason);
    const wasExactResult = codeResultArea.dataset.presentation === 'exact-hdr';
    delete codeResultArea.dataset.presentation;
    const controller = experimentalClientHDRController;
    if (controller && typeof controller.markSDRStale === 'function') {
      controller.markSDRStale(`control_code_result_${fallbackReason}`);
    } else {
      showExperimentalClientHDRSurface(false, fallbackReason);
    }
    if (wasExactResult && !codeResultArea.hidden) {
      codeResultImage.hidden = false;
      codeResultArea.style.background = '#000';
      synchronizeExperimentalHDRSurfaceRegion(true, 'control_code_result_sdr_fallback');
    }
    return hadTarget || wasExactResult;
  }

  function handleControlCodeHDRSurfaceChange(visible, reason) {
    if (visible) return;
    const exactResultVisible = controlCodeExactHDRResultVisible();
    if (!controlCodeHDRFreezeTarget && !exactResultVisible) return;
    clearControlCodeHDRFreezeTarget(reason || 'exact_hdr_surface_hidden');
    if (!exactResultVisible) return;
    delete codeResultArea.dataset.presentation;
    codeResultImage.hidden = false;
    codeResultArea.style.background = '#000';
    synchronizeExperimentalHDRSurfaceRegion(true, 'control_code_exact_hdr_lost');
  }

  function clearControlCodeResultCapture() {
    const releasedHDRFreeze = clearControlCodeHDRFreezeTarget('clear_capture');
    if (controlCodeResultCaptureTimer) {
      clearTimeout(controlCodeResultCaptureTimer);
      controlCodeResultCaptureTimer = null;
    }
    controlCodeResultCaptureRequestID = '';
    controlCodeResultCapturedRequestID = '';
    controlCodeCaptureAckInFlightRequestID = '';
    controlCodeResultCaptureStartedAt = 0;
    lastControlCodeMarkerReceivedLogKey = '';
    lastControlCodeMarkerWaitingLogKey = '';
    lastControlCodeCandidateRejectedLogKey = '';
    pendingControlCodeBaselineFrameFingerprint = null;
    controlCodeBaselineFrameFingerprint = null;
    controlCodeBaselineRequestID = '';
    lastControlCodeCaptureDebug = null;
    lastControlCodeCaptureKeyframeRequestAt = 0;
    lastControlCodeCaptureKeyframeRetryCount = 0;
    resetControlCodeSafeGeneratedFrame('clear_capture');
    clearControlCodeFrozenCandidateFrame();
    clearControlCodePreparedCapture();
    delete codeResultArea.dataset.presentation;
    codeResultImage.hidden = true;
    codeResultImage.removeAttribute('src');
    const regionRecoveryStarted = synchronizeExperimentalHDRSurfaceRegion(
      !experimentalHDRSurfacePresentationAllowed(),
      'control_code_capture_cleared'
    );
    if (releasedHDRFreeze && experimentalHDRSurfacePresentationAllowed() &&
      experimentalMediaPreferenceController.enabled && document.visibilityState === 'visible' &&
      !experimentalMediaPresentationRegionBlocked && !experimentalMediaPresentationRecoveryPending &&
      !regionRecoveryStarted) {
      beginExperimentalMediaForegroundRecovery('control_code_result_released', {
        forceCanvasReset: true,
        foregroundConfirmed: true
      });
    }
    publishStreamDebug();
  }

  function clearControlCodeRequestLocalState(reason) {
    const requestID = String(codeRequest && codeRequest.requestId || '').trim();
    if (controlCodeCleanupPendingRequestID &&
      (!requestID || controlCodeCleanupPendingRequestID === requestID)) {
      controlCodeCleanupPendingRequestID = '';
    }
    codeRequest = null;
    clearControlCodeResultCapture();
    scheduleControlCodeTicker(null);
    if (requestID) clientLog('control_code_request_cleared', reason || 'authoritative_state');
  }

  function controlCodeFingerprintRegion() {
    return {
      x: 0.12,
      y: 0.16,
      width: 0.76,
      height: 0.36
    };
  }

  function canvasRegionFingerprint(region) {
    if (!hasRenderedFrame || !canvas.width || !canvas.height || !region) return null;
    const width = Math.max(1, Math.round(Number(region.width || 0) * canvas.width));
    const height = Math.max(1, Math.round(Number(region.height || 0) * canvas.height));
    const x = Math.max(0, Math.min(canvas.width - width, Math.round(Number(region.x || 0) * canvas.width)));
    const y = Math.max(0, Math.min(canvas.height - height, Math.round(Number(region.y || 0) * canvas.height)));
    try {
      const values = [];
      let total = 0;
      let darkCells = 0;
      let lightCells = 0;
      for (let row = 0; row < controlCodeFingerprintGridHeight; row++) {
        for (let col = 0; col < controlCodeFingerprintGridWidth; col++) {
          const px = Math.max(0, Math.min(canvas.width - 1, x + Math.round((col + 0.5) * width / controlCodeFingerprintGridWidth)));
          const py = Math.max(0, Math.min(canvas.height - 1, y + Math.round((row + 0.5) * height / controlCodeFingerprintGridHeight)));
          const pixel = ctx.getImageData(px, py, 1, 1).data;
          const luminance = Math.round((pixel[0] * 0.299) + (pixel[1] * 0.587) + (pixel[2] * 0.114));
          values.push(luminance);
          total += luminance;
          if (luminance <= 80) darkCells++;
          if (luminance >= 175) lightCells++;
        }
      }
      const mean = values.length ? total / values.length : 0;
      let varianceTotal = 0;
      values.forEach((value) => {
        const delta = value - mean;
        varianceTotal += delta * delta;
      });
      return {
        values,
        mean,
        contrastScore: values.length ? Math.sqrt(varianceTotal / values.length) : 0,
        darkCellRatio: values.length ? darkCells / values.length : 0,
        lightCellRatio: values.length ? lightCells / values.length : 0,
        frameEpoch: lastRenderedFrameEpoch,
        frameSequence: lastRenderedFrameSequence,
        at: Date.now(),
        region: { x, y, width, height }
      };
    } catch (error) {
      reportClientFault('control_code_fingerprint_failed', error);
      return null;
    }
  }

  function fingerprintDifferenceScore(left, right) {
    if (!left || !right || !Array.isArray(left.values) || !Array.isArray(right.values)) {
      return { score: 0, changedCells: 0 };
    }
    const length = Math.min(left.values.length, right.values.length);
    if (!length) return { score: 0, changedCells: 0 };
    let total = 0;
    let changedCells = 0;
    for (let index = 0; index < length; index++) {
      const delta = Math.abs(Number(left.values[index] || 0) - Number(right.values[index] || 0));
      total += delta;
      if (delta >= 24) changedCells++;
    }
    return {
      score: total / length,
      changedCells
    };
  }

  function controlCodePopupFrameProof() {
    const sample = (x, y, width, height) => canvasRegionFingerprint({ x, y, width, height });
    function regionOrangeCellRatio(region) {
      if (!hasRenderedFrame || !canvas.width || !canvas.height || !region) return 0;
      const width = Math.max(1, Math.round(Number(region.width || 0) * canvas.width));
      const height = Math.max(1, Math.round(Number(region.height || 0) * canvas.height));
      const x = Math.max(0, Math.min(canvas.width - width, Math.round(Number(region.x || 0) * canvas.width)));
      const y = Math.max(0, Math.min(canvas.height - height, Math.round(Number(region.y || 0) * canvas.height)));
      try {
        const imageData = ctx.getImageData(x, y, Math.min(width, canvas.width - x), Math.min(height, canvas.height - y));
        const cols = 10;
        const rows = 5;
        let sampled = 0;
        let orange = 0;
        for (let row = 0; row < rows; row++) {
          for (let col = 0; col < cols; col++) {
            const px = Math.max(0, Math.min(imageData.width - 1, Math.round((col + 0.5) * imageData.width / cols)));
            const py = Math.max(0, Math.min(imageData.height - 1, Math.round((row + 0.5) * imageData.height / rows)));
            const offset = (py * imageData.width + px) * 4;
            const red = imageData.data[offset] || 0;
            const green = imageData.data[offset + 1] || 0;
            const blue = imageData.data[offset + 2] || 0;
            if (red >= 155 && green >= 80 && green <= 190 && blue <= 95 && red - green >= 20 && green - blue >= 25) {
              orange++;
            }
            sampled++;
          }
        }
        return sampled ? orange / sampled : 0;
      } catch (error) {
        reportClientFault('control_code_popup_orange_proof_failed', error);
        return 0;
      }
    }
    const keyboard = sample(0.08, 0.62, 0.84, 0.34);
    const dialog = sample(0.16, 0.38, 0.68, 0.22);
    const dialogUpper = sample(0.16, 0.30, 0.68, 0.22);
    const inputLine = sample(0.24, 0.52, 0.52, 0.045);
    const inputLineUpper = sample(0.24, 0.41, 0.52, 0.045);
    const dimOverlay = sample(0.08, 0.30, 0.84, 0.44);
    const okButton = sample(0.64, 0.51, 0.18, 0.07);
    const okButtonUpper = sample(0.64, 0.43, 0.18, 0.07);
    const keyboardVisible = Boolean(keyboard &&
      keyboard.lightCellRatio >= 0.08 &&
      keyboard.lightCellRatio <= 0.35 &&
      keyboard.darkCellRatio >= 0.12 &&
      keyboard.mean >= 75 &&
      keyboard.mean <= 155 &&
      keyboard.contrastScore <= 100);
    const dialogLowerVisible = Boolean(dialog &&
      dialog.lightCellRatio >= 0.42 &&
      dialog.mean >= 118 &&
      dialog.darkCellRatio <= 0.30 &&
      dialog.contrastScore <= 98);
    const dialogUpperVisible = Boolean(dialogUpper &&
      dialogUpper.lightCellRatio >= 0.42 &&
      dialogUpper.mean >= 118 &&
      dialogUpper.darkCellRatio <= 0.30 &&
      dialogUpper.contrastScore <= 98);
    const dialogVisible = dialogLowerVisible || dialogUpperVisible;
    const dialogProof = dialogUpperVisible ? dialogUpper : dialog;
    const inputLineVisible = Boolean(inputLine &&
      inputLine.darkCellRatio >= 0.08 &&
      inputLine.contrastScore >= 18) || Boolean(inputLineUpper &&
      inputLineUpper.darkCellRatio >= 0.08 &&
      inputLineUpper.contrastScore >= 18);
    const okButtonLowerOrangeRatio = regionOrangeCellRatio({ x: 0.64, y: 0.51, width: 0.18, height: 0.07 });
    const okButtonUpperOrangeRatio = regionOrangeCellRatio({ x: 0.64, y: 0.43, width: 0.18, height: 0.07 });
    const okButtonOrangeRatio = Math.max(okButtonLowerOrangeRatio, okButtonUpperOrangeRatio);
    const dialogGhostVisible = Boolean(dialogProof &&
      dialogProof.lightCellRatio >= 0.24 &&
      dialogProof.mean >= 82 &&
      dialogProof.darkCellRatio <= 0.30 &&
      dialogProof.contrastScore <= 106 &&
      okButtonOrangeRatio >= 0.03);
    const dimOverlayVisible = Boolean(dimOverlay &&
      dimOverlay.mean >= 68 &&
      dimOverlay.mean <= 205 &&
      dimOverlay.lightCellRatio >= 0.10 &&
      dimOverlay.darkCellRatio >= 0.08 &&
      dimOverlay.contrastScore <= 112);
    const okButtonVisible = okButtonOrangeRatio >= 0.08 || Boolean(okButton &&
      okButton.mean >= 105 &&
      okButton.mean <= 220 &&
      okButton.contrastScore <= 85 &&
      okButton.lightCellRatio >= 0.18) || Boolean(okButtonUpper &&
      okButtonUpper.mean >= 105 &&
      okButtonUpper.mean <= 220 &&
      okButtonUpper.contrastScore <= 85 &&
      okButtonUpper.lightCellRatio >= 0.18);
    // The ordinary ticket body also looks like a bright dialog in these samples. Require
    // the updated ViVi dialog's orange OK control before declaring an unsafe popup; the
    // input-line fallback was accepting the Aztec pattern as a stale dialog.
    const popupVisible = dialogVisible && okButtonVisible && okButtonOrangeRatio >= 0.03;
    const popupKeyboardVisible = dialogVisible && keyboardVisible;
    return {
      keyboardVisible: popupKeyboardVisible,
      popupVisible,
      dialogGhostVisible,
      dimOverlayVisible,
      unsafeOverlayVisible: popupVisible || popupKeyboardVisible || dialogGhostVisible || (dimOverlayVisible && (popupVisible || dialogGhostVisible || popupKeyboardVisible)),
      keyboardLightCellRatio: keyboard ? Math.round(Number(keyboard.lightCellRatio || 0) * 100) / 100 : 0,
      keyboardMean: keyboard ? Math.round(Number(keyboard.mean || 0) * 10) / 10 : 0,
      keyboardContrastScore: keyboard ? Math.round(Number(keyboard.contrastScore || 0) * 10) / 10 : 0,
      popupLightCellRatio: dialogProof ? Math.round(Number(dialogProof.lightCellRatio || 0) * 100) / 100 : 0,
      popupDarkCellRatio: dialogProof ? Math.round(Number(dialogProof.darkCellRatio || 0) * 100) / 100 : 0,
      popupContrastScore: dialogProof ? Math.round(Number(dialogProof.contrastScore || 0) * 10) / 10 : 0,
      dimOverlayMean: dimOverlay ? Math.round(Number(dimOverlay.mean || 0) * 10) / 10 : 0,
      dimOverlayContrastScore: dimOverlay ? Math.round(Number(dimOverlay.contrastScore || 0) * 10) / 10 : 0,
      okButtonOrangeRatio: Math.round(okButtonOrangeRatio * 100) / 100,
      okButtonVisible,
      inputLineVisible
    };
  }

  function emptyControlCodeResultChipProof() {
    return {
      chipVisible: false,
      chipDarkRatio: 0,
      chipLightRatio: 0,
      chipRows: 0,
      chipY: 0,
      chipScore: 0
    };
  }

  function sampleControlCodeResultChipRegion(yRatio, sampledFrame) {
    if (!canvas.width || !canvas.height) {
      return emptyControlCodeResultChipProof();
    }
    const x = Math.max(0, Math.round(canvas.width * 0.14));
    const y = Math.max(0, Math.round(canvas.height * yRatio));
    const width = Math.max(1, Math.round(canvas.width * 0.72));
    const height = Math.max(1, Math.round(canvas.height * 0.06));
    const cols = 52;
    const rows = 12;
    let imageData;
    let sampleOffsetX = 0;
    let sampleOffsetY = 0;
    try {
      if (sampledFrame && sampledFrame.imageData) {
        imageData = sampledFrame.imageData;
        sampleOffsetX = x - Number(sampledFrame.x || 0);
        sampleOffsetY = y - Number(sampledFrame.y || 0);
      } else {
        imageData = ctx.getImageData(x, y, Math.min(width, canvas.width - x), Math.min(height, canvas.height - y));
      }
    } catch (error) {
      reportClientFault('control_code_chip_proof_failed', error);
      return emptyControlCodeResultChipProof();
    }
    const data = imageData.data;
    const sampleWidth = imageData.width;
    const sampleHeight = imageData.height;
    let sampled = 0;
    let dark = 0;
    let light = 0;
    let chipRows = 0;
    // The updated ViVi result strip is rendered as a dark panel spanning almost the entire
    // sampled width. Requiring dark cells across most columns separates it from the ordinary
    // Aztec pattern, which only occupies isolated cells in each row.
    const minimumDarkCellsPerRow = 35;
    for (let row = 0; row < rows; row++) {
      let rowDark = 0;
      for (let col = 0; col < cols; col++) {
        const px = Math.max(0, Math.min(sampleWidth - 1, sampleOffsetX + Math.round((col + 0.5) * width / cols)));
        const py = Math.max(0, Math.min(sampleHeight - 1, sampleOffsetY + Math.round((row + 0.5) * height / rows)));
        const offset = (py * sampleWidth + px) * 4;
        const red = data[offset] || 0;
        const green = data[offset + 1] || 0;
        const blue = data[offset + 2] || 0;
        const luminance = Math.round((red * 299 + green * 587 + blue * 114) / 1000);
        if (luminance <= 100) {
          dark++;
          rowDark++;
        }
        if (luminance >= 145) {
          light++;
        }
        sampled++;
      }
      if (rowDark >= minimumDarkCellsPerRow) {
        chipRows++;
      }
    }
    const chipDarkRatio = sampled ? dark / sampled : 0;
    const chipLightRatio = sampled ? light / sampled : 0;
    const chipScore = Math.max(0, (chipRows * 10) + (chipDarkRatio * 80) - (chipLightRatio * 20));
    return {
      chipVisible: chipRows >= 4 && chipDarkRatio >= 0.42 && chipLightRatio >= 0.10 &&
        chipLightRatio <= 0.60 && chipScore >= 38,
      chipDarkRatio: Math.round(chipDarkRatio * 100) / 100,
      chipLightRatio: Math.round(chipLightRatio * 100) / 100,
      chipRows,
      chipY: Math.round(Number(yRatio || 0) * 1000) / 1000,
      chipScore: Math.round(chipScore * 10) / 10
    };
  }

  function controlCodeResultChipProof() {
    const scanX = Math.max(0, Math.round(canvas.width * 0.14));
    const scanY = Math.max(0, Math.round(canvas.height * controlCodeGeneratedChipScanStartY));
    const scanWidth = Math.max(1, Math.min(canvas.width - scanX, Math.round(canvas.width * 0.72)));
    const scanHeight = Math.max(1, Math.min(
      canvas.height - scanY,
      Math.round(canvas.height * (controlCodeGeneratedChipScanEndY - controlCodeGeneratedChipScanStartY + 0.06))
    ));
    let sampledFrame = null;
    try {
      // Read the whole candidate strip once. The previous implementation issued one
      // getImageData call for every y position, which made result detection compete
      // with video decoding and delayed the browser acknowledgement.
      sampledFrame = {
        imageData: ctx.getImageData(scanX, scanY, scanWidth, scanHeight),
        x: scanX,
        y: scanY
      };
    } catch (error) {
      reportClientFault('control_code_chip_scan_failed', error);
    }
    let bestChip = emptyControlCodeResultChipProof();
    for (let yRatio = controlCodeGeneratedChipScanStartY; yRatio <= controlCodeGeneratedChipScanEndY + 0.0001; yRatio += controlCodeGeneratedChipScanStepY) {
      const candidate = sampleControlCodeResultChipRegion(yRatio, sampledFrame);
      if (!bestChip || candidate.chipScore > bestChip.chipScore) {
        bestChip = candidate;
      }
    }
    return bestChip;
  }

  function controlCodeGeneratedFrameProof() {
    const chip = controlCodeResultChipProof();
    const resultBar = canvasRegionFingerprint({ x: 0.14, y: chip.chipY || 0.55, width: 0.72, height: 0.06 });
    const codeArea = canvasRegionFingerprint({ x: 0.18, y: Math.max(0.12, chip.chipY - 0.34), width: 0.64, height: 0.30 });
    const generatedBarVisible = Boolean(resultBar &&
      Number(resultBar.darkCellRatio || 0) >= 0.20 &&
      Number(resultBar.lightCellRatio || 0) >= 0.16 &&
      Number(resultBar.contrastScore || 0) >= 45);
    const generatedCodeVisible = Boolean(codeArea &&
      Number(codeArea.darkCellRatio || 0) >= 0.04 &&
      Number(codeArea.lightCellRatio || 0) >= 0.14 &&
      Number(codeArea.contrastScore || 0) >= 35);
    const generatedCodeScore = codeArea
      ? (Number(codeArea.darkCellRatio || 0) * 100) + (Number(codeArea.lightCellRatio || 0) * 40) + Number(codeArea.contrastScore || 0)
      : 0;
    return {
      generatedVisible: chip.chipVisible && generatedCodeVisible,
      generatedChipVisible: chip.chipVisible,
      generatedChipDarkRatio: chip.chipDarkRatio,
      generatedChipLightRatio: chip.chipLightRatio,
      generatedChipRows: chip.chipRows,
      generatedChipY: chip.chipY,
      generatedChipScore: chip.chipScore,
      generatedBarVisible,
      generatedCodeVisible,
      generatedBarDarkCellRatio: resultBar ? Math.round(Number(resultBar.darkCellRatio || 0) * 100) / 100 : 0,
      generatedBarLightCellRatio: resultBar ? Math.round(Number(resultBar.lightCellRatio || 0) * 100) / 100 : 0,
      generatedBarContrastScore: resultBar ? Math.round(Number(resultBar.contrastScore || 0) * 10) / 10 : 0,
      generatedCodeDarkCellRatio: codeArea ? Math.round(Number(codeArea.darkCellRatio || 0) * 100) / 100 : 0,
      generatedCodeLightCellRatio: codeArea ? Math.round(Number(codeArea.lightCellRatio || 0) * 100) / 100 : 0,
      generatedCodeContrastScore: codeArea ? Math.round(Number(codeArea.contrastScore || 0) * 10) / 10 : 0,
      generatedCodeScore: Math.round(generatedCodeScore * 10) / 10
    };
  }

  function rememberControlCodeBaselineFrame(requestID) {
    requestID = String(requestID || '').trim();
    if (!requestID) return false;
    if (controlCodeBaselineRequestID === requestID && controlCodeBaselineFrameFingerprint) return true;
    controlCodeBaselineRequestID = requestID;
    controlCodeBaselineFrameFingerprint = pendingControlCodeBaselineFrameFingerprint || canvasRegionFingerprint(controlCodeFingerprintRegion());
    pendingControlCodeBaselineFrameFingerprint = null;
    lastControlCodeCaptureKeyframeRequestAt = 0;
    lastControlCodeCaptureKeyframeRetryCount = 0;
    resetControlCodeSafeGeneratedFrame('baseline');
    clearControlCodeFrozenCandidateFrame();
    clearControlCodePreparedCapture();
    lastControlCodeCaptureDebug = {
      requestId: requestID,
      baselineCaptured: Boolean(controlCodeBaselineFrameFingerprint),
      baselineFrameEpoch: controlCodeBaselineFrameFingerprint ? controlCodeBaselineFrameFingerprint.frameEpoch : 0,
      baselineFrameSequence: controlCodeBaselineFrameFingerprint ? controlCodeBaselineFrameFingerprint.frameSequence : 0,
      candidateAccepted: false,
      candidateRejectedReason: controlCodeBaselineFrameFingerprint ? 'waiting_for_marker' : 'baseline_missing'
    };
    publishStreamDebug();
    return Boolean(controlCodeBaselineFrameFingerprint);
  }

  function controlCodeRenderedFrameEpoch() {
    if (lastRenderedFrameEpoch) return lastRenderedFrameEpoch;
    if (hasRenderedFrame && currentStreamEpoch) return currentStreamEpoch;
    return 0;
  }

  function controlCodeRenderedFrameSequence() {
    if (lastRenderedFrameSequence) return lastRenderedFrameSequence;
    if (hasRenderedFrame && lastAcceptedFrameSequence) return lastAcceptedFrameSequence;
    return 0;
  }

  function controlCodeMarkerReady(request) {
    if (!request || request.status !== 'succeeded') return false;
    const markerEpoch = Number(request.resultFrameEpoch || request.streamEpoch || 0);
    const markerSequence = Number(request.resultMinFrameSequence || request.minFrameSequence || request.frameSequence || 0);
    if (!markerEpoch || !markerSequence || !hasRenderedFrame) return false;
    const renderedEpoch = controlCodeRenderedFrameEpoch();
    const renderedSequence = controlCodeRenderedFrameSequence();
    return renderedEpoch === markerEpoch && renderedSequence >= markerSequence;
  }

  function controlCodeMarkerReceivedAgeMillis(request) {
    const raw = request && (request.resultProofAt || request.markerReceivedAt || request.completedAt || request.startedAt);
    const parsed = Date.parse(raw || '');
    if (!Number.isFinite(parsed)) return 0;
    return Math.max(0, Math.round(Date.now() + serverClockSkewMs - parsed));
  }

  function controlCodeCaptureTrace(event, request, proof, detail) {
    const requestID = String((proof && proof.requestId) || (request && request.requestId) || '').trim();
    const payload = Object.assign({
      requestKey: requestID ? accountPublicId(requestID) : '',
      status: String(request && request.status || ''),
      markerEpoch: Number((proof && proof.markerEpoch) || (request && (request.resultFrameEpoch || request.streamEpoch)) || 0),
      markerSequence: Number((proof && proof.markerSequence) || (request && (request.resultMinFrameSequence || request.minFrameSequence || request.frameSequence)) || 0),
      candidateFrameEpoch: Number(proof && proof.candidateFrameEpoch || controlCodeRenderedFrameEpoch() || 0),
      candidateFrameSequence: Number(proof && proof.candidateFrameSequence || controlCodeRenderedFrameSequence() || 0),
      safeGeneratedFrameCount: Number(proof && proof.safeGeneratedFrameCount || controlCodeSafeGeneratedFrameCount || 0),
      keyframeRetryCount: Number(lastControlCodeCaptureKeyframeRetryCount || 0)
    }, detail || {});
    clientLog(event, JSON.stringify(payload));
  }

  function controlCodePhoneGeneratedProofKind(resultProof) {
    resultProof = String(resultProof || '').trim();
    if (resultProof === 'phone_visual_generated_inline') return 'inline';
    if (resultProof === 'phone_visual_generated_with_close') return 'with_close';
    return '';
  }

  // A single generated-looking frame can be a transition frame. Require two
  // distinct rendered frames even when the phone has independently confirmed
  // the generated surface.
  const controlCodeSafeGeneratedFrameRequiredCount = 2;
  const controlCodeTrustedProofSafeGeneratedFrameRequiredCount = 2;

  function controlCodeCandidateFrameKey(proof) {
    return [
      String(proof && proof.requestId || '').trim(),
      Number(proof && proof.candidateFrameEpoch || 0),
      Number(proof && proof.candidateFrameSequence || 0)
    ].join(':');
  }

  function resetControlCodeSafeGeneratedFrame(reason) {
    controlCodeSafeGeneratedFrameRequestID = '';
    controlCodeSafeGeneratedFrameEpoch = 0;
    controlCodeSafeGeneratedFrameSequence = 0;
    controlCodeSafeGeneratedFrameCount = 0;
    if (lastControlCodeCaptureDebug) {
      lastControlCodeCaptureDebug.safeGeneratedFrameResetReason = reason || '';
    }
  }

  function noteControlCodeSafeGeneratedFrame(proof) {
    const requestID = String(proof && proof.requestId || '').trim();
    const epoch = Number(proof && proof.candidateFrameEpoch || 0);
    const sequence = Number(proof && proof.candidateFrameSequence || 0);
    if (!requestID || !epoch || !sequence) {
      resetControlCodeSafeGeneratedFrame('safe_frame_missing_metadata');
      return 0;
    }
    if (requestID !== controlCodeSafeGeneratedFrameRequestID || epoch !== controlCodeSafeGeneratedFrameEpoch) {
      controlCodeSafeGeneratedFrameRequestID = requestID;
      controlCodeSafeGeneratedFrameEpoch = epoch;
      controlCodeSafeGeneratedFrameSequence = sequence;
      controlCodeSafeGeneratedFrameCount = 1;
      return controlCodeSafeGeneratedFrameCount;
    }
    if (sequence > controlCodeSafeGeneratedFrameSequence) {
      controlCodeSafeGeneratedFrameSequence = sequence;
      controlCodeSafeGeneratedFrameCount += 1;
    }
    return controlCodeSafeGeneratedFrameCount;
  }

  function clearControlCodeFrozenCandidateFrame() {
    controlCodeFrozenFrameCanvas = null;
    controlCodeFrozenFrameKey = '';
  }

  function clearControlCodePreparedCapture() {
    controlCodePreparedCaptureDisplayedRequestID = '';
  }

  function freezeControlCodeCandidateFrame(proof) {
    if (!proof || !hasRenderedFrame || !canvas.width || !canvas.height) return false;
    const frozen = document.createElement('canvas');
    frozen.width = canvas.width;
    frozen.height = canvas.height;
    const frozenContext = frozen.getContext('2d', { alpha: false });
    if (!frozenContext) return false;
    frozenContext.drawImage(canvas, 0, 0, canvas.width, canvas.height);
    controlCodeFrozenFrameCanvas = frozen;
    controlCodeFrozenFrameKey = controlCodeCandidateFrameKey(proof);
    proof.frozenFrameKey = controlCodeFrozenFrameKey;
    proof.frozenFrameAvailable = true;
    return true;
  }

  function controlCodeFrozenCandidateFrameForProof(proof) {
    if (!proof || !controlCodeFrozenFrameCanvas || !controlCodeFrozenFrameKey) return null;
    if (controlCodeCandidateFrameKey(proof) !== controlCodeFrozenFrameKey) return null;
    if (controlCodeFrozenFrameCanvas.width !== canvas.width || controlCodeFrozenFrameCanvas.height !== canvas.height) return null;
    return controlCodeFrozenFrameCanvas;
  }

  function controlCodeCandidateFrameProof(request) {
    const requestID = String(request && request.requestId || '').trim();
    const markerEpoch = Number(request && (request.resultFrameEpoch || request.streamEpoch) || 0);
    const markerSequence = Number(request && (request.resultMinFrameSequence || request.minFrameSequence || request.frameSequence) || 0);
    const proof = {
      accepted: false,
      requestId: requestID,
      resultProof: String(request && request.resultProof || '').trim(),
      markerEpoch,
      markerSequence,
      markerReceivedAgeMillis: controlCodeMarkerReceivedAgeMillis(request),
      candidateFrameEpoch: controlCodeRenderedFrameEpoch(),
      candidateFrameSequence: controlCodeRenderedFrameSequence(),
      candidateAccepted: false,
      candidateRejectedReason: '',
      fingerprintDifferenceScore: 0,
      fingerprintChangedCells: 0,
      safeGeneratedFrameCount: controlCodeSafeGeneratedFrameCount,
      frozenFrameKey: controlCodeFrozenFrameKey,
      keyframeRetryCount: lastControlCodeCaptureKeyframeRetryCount
    };
    if (!request || request.status !== 'succeeded') {
      proof.candidateRejectedReason = 'request_not_succeeded';
      return proof;
    }
    if (request.cleanupCompletedAt) {
      proof.candidateRejectedReason = 'result_window_closed_before_capture';
      return proof;
    }
    if (!requestID) {
      proof.candidateRejectedReason = 'request_id_missing';
      return proof;
    }
    if (!markerEpoch || !markerSequence) {
      proof.candidateRejectedReason = 'marker_waiting';
      return proof;
    }
    if (!hasRenderedFrame) {
      proof.candidateRejectedReason = 'frame_waiting';
      return proof;
    }
    const renderedEpoch = controlCodeRenderedFrameEpoch();
    const renderedSequence = controlCodeRenderedFrameSequence();
    if (markerEpoch && markerSequence && (renderedEpoch !== markerEpoch || renderedSequence < markerSequence)) {
      proof.candidateRejectedReason = 'frame_before_marker';
      return proof;
    }
    const candidateFingerprint = canvasRegionFingerprint(controlCodeFingerprintRegion());
    const difference = fingerprintDifferenceScore(controlCodeBaselineFrameFingerprint, candidateFingerprint);
    const phoneGeneratedProofKind = controlCodePhoneGeneratedProofKind(proof.resultProof);
    const trustedPhonePostSubmitProof = Boolean(phoneGeneratedProofKind);
    proof.phoneGeneratedProofKind = phoneGeneratedProofKind;
    proof.fingerprintDifferenceScore = Math.round(Number(difference.score || 0) * 10) / 10;
    proof.fingerprintChangedCells = Number(difference.changedCells || 0);
    const popupProof = controlCodePopupFrameProof();
    Object.assign(proof, popupProof, {
      popupKeyboardVisible: popupProof.keyboardVisible,
      popupGhostVisible: popupProof.dialogGhostVisible
    });
    if (popupProof.unsafeOverlayVisible) {
      resetControlCodeSafeGeneratedFrame('unsafe_overlay');
      proof.safeGeneratedFrameCount = controlCodeSafeGeneratedFrameCount;
      if (popupProof.keyboardVisible) {
        proof.candidateRejectedReason = 'control_popup_keyboard_frame';
      } else if (popupProof.popupVisible) {
        proof.candidateRejectedReason = 'control_popup_frame';
      } else {
        proof.candidateRejectedReason = 'control_popup_fade_frame';
      }
      return proof;
    }
    if (trustedPhonePostSubmitProof) {
      proof.trustedPhonePostSubmitProof = true;
    }
    const generatedProof = controlCodeGeneratedFrameProof();
    Object.assign(proof, generatedProof);
    const frameChangedFromBaseline = Boolean(controlCodeBaselineFrameFingerprint &&
      (proof.fingerprintDifferenceScore >= controlCodeFingerprintDifferenceThreshold ||
        proof.fingerprintChangedCells >= controlCodeFingerprintChangedCellsThreshold));
    proof.frameChangedFromBaseline = frameChangedFromBaseline;
    // The current ViVi flow shows the entered digits in a dark confirmation strip.
    // Pixel must close that strip before it emits the inline proof, because the
    // generated Aztec appears only afterwards. The browser therefore requires a
    // clean, changed Aztec frame for inline mode and explicitly rejects the strip.
    // A genuinely separate legacy result surface keeps its stricter strip proof.
    const browserTrustedGeneratedVisible = Boolean(trustedPhonePostSubmitProof &&
      frameChangedFromBaseline && (
        (phoneGeneratedProofKind === 'inline' &&
          generatedProof.generatedCodeVisible &&
          !generatedProof.generatedChipVisible) ||
        (phoneGeneratedProofKind === 'with_close' &&
          generatedProof.generatedVisible &&
          generatedProof.generatedChipVisible)
      ));
    const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof &&
      markerEpoch &&
      markerSequence &&
      renderedEpoch === markerEpoch &&
      renderedSequence >= markerSequence &&
      request.status === 'succeeded');
    proof.trustedPhoneMarkerFrame = trustedPhoneMarkerFrame;
    proof.browserTrustedGeneratedVisible = browserTrustedGeneratedVisible;
    const browserTrustedResultVisible = browserTrustedGeneratedVisible;
    proof.browserTrustedResultVisible = browserTrustedResultVisible;
    if (!browserTrustedResultVisible && trustedPhoneMarkerFrame) {
      proof.generatedMarkerOnlyRejected = true;
    }
    if (!proof.browserTrustedResultVisible) {
      resetControlCodeSafeGeneratedFrame('generated_not_visible');
      proof.safeGeneratedFrameCount = controlCodeSafeGeneratedFrameCount;
      if (controlCodeBaselineFrameFingerprint &&
        proof.fingerprintDifferenceScore < controlCodeFingerprintDifferenceThreshold &&
        proof.fingerprintChangedCells < controlCodeFingerprintChangedCellsThreshold) {
        proof.candidateRejectedReason = 'candidate_matches_pre_request_frame';
        return proof;
      }
      proof.candidateRejectedReason = 'generated_frame_not_visible';
      return proof;
    }
    const safeFrameCount = noteControlCodeSafeGeneratedFrame(proof);
    proof.safeGeneratedFrameCount = safeFrameCount;
    const requiredSafeFrameCount = trustedPhonePostSubmitProof ?
      controlCodeTrustedProofSafeGeneratedFrameRequiredCount :
      controlCodeSafeGeneratedFrameRequiredCount;
    proof.requiredSafeGeneratedFrameCount = requiredSafeFrameCount;
    if (safeFrameCount < requiredSafeFrameCount) {
      proof.candidateRejectedReason = 'generated_frame_not_stable';
      return proof;
    }
    if (!freezeControlCodeCandidateFrame(proof)) {
      proof.candidateRejectedReason = 'candidate_frame_freeze_failed';
      return proof;
    }
    proof.hdrFreezeTargetLatched = latchControlCodeHDRFreezeTarget(proof);
    proof.accepted = true;
    proof.candidateAccepted = true;
    proof.candidateRejectedReason = '';
    proof.acceptedReason = `candidate_frame_at_or_after_phone_marker_and_generated_visual:${phoneGeneratedProofKind}`;
    proof.provisional = false;
    controlCodeCaptureTrace('control_code_frame_frozen', request, proof, {
      acceptedReason: proof.acceptedReason,
      provisional: Boolean(proof.provisional)
    });
    controlCodeCaptureTrace('control_code_candidate_accepted', request, proof, {
      acceptedReason: proof.acceptedReason,
      provisional: Boolean(proof.provisional)
    });
    return proof;
  }

  function clearUnpaintedControlCodeResultImage(requestID) {
    if (controlCodePreparedCaptureDisplayedRequestID === requestID) return;
    if (controlCodeHDRFreezeTargetActive() || codeResultArea.dataset.presentation === 'exact-hdr') {
      forceControlCodeResultSDRFallback('result_paint_incomplete');
    }
    setControlCodeResultVisible(false);
    delete codeResultArea.dataset.presentation;
    codeResultImage.hidden = true;
    codeResultImage.removeAttribute('src');
    codeResultArea.dataset.status = 'waiting';
    codeResultArea.style.background = '';
  }

  function waitForControlCodeResultImageReady(image) {
    const ready = () => Boolean(image && image.complete && image.naturalWidth > 0 && image.naturalHeight > 0);
    return new Promise((resolve) => {
      let settled = false;
      let timer = null;
      let decodeSettled = typeof image.decode !== 'function';
      const finish = (value) => {
        if (settled) return;
        settled = true;
        if (timer) clearTimeout(timer);
        image.removeEventListener('load', onLoad);
        image.removeEventListener('error', onError);
        resolve(Boolean(value));
      };
      const finishWhenReady = () => {
        if (decodeSettled && ready()) finish(true);
      };
      const onLoad = () => finishWhenReady();
      const onError = () => finish(false);
      image.addEventListener('load', onLoad, { once: true });
      image.addEventListener('error', onError, { once: true });
      timer = setTimeout(() => finish(false), controlCodeResultImageReadyTimeoutMs);
      if (typeof image.decode === 'function') {
        try {
          image.decode()
            .then(() => {
              decodeSettled = true;
              finishWhenReady();
            })
            .catch(() => {
              // A browser may reject decode() for an already decoded data URL.
              // Fall back only when load state and natural dimensions prove it.
              decodeSettled = true;
              finishWhenReady();
            });
        } catch (_) {
          decodeSettled = true;
          finishWhenReady();
        }
      } else {
        finishWhenReady();
      }
    });
  }

  function controlCodeResultPaintReady(requestID, presentation) {
    if (!requestID || document.visibilityState !== 'visible') return false;
    if (!document.documentElement.contains(codeResultArea) || !document.documentElement.contains(codeResultImage)) return false;
    if (codeResultArea.hidden) return false;
    const areaRect = codeResultArea.getBoundingClientRect();
    if (areaRect.width <= 0 || areaRect.height <= 0) return false;
    const viewportWidth = Math.max(0, Number(window.innerWidth || document.documentElement.clientWidth || 0));
    const viewportHeight = Math.max(0, Number(window.innerHeight || document.documentElement.clientHeight || 0));
    const visibleWidth = Math.min(areaRect.right, viewportWidth) - Math.max(areaRect.left, 0);
    const visibleHeight = Math.min(areaRect.bottom, viewportHeight) - Math.max(areaRect.top, 0);
    if (viewportWidth <= 0 || viewportHeight <= 0 || visibleWidth <= 0 || visibleHeight <= 0) return false;
    const areaStyle = window.getComputedStyle(codeResultArea);
    if (areaStyle.display === 'none' || areaStyle.visibility === 'hidden' || Number(areaStyle.opacity || 1) <= 0) return false;
    if (presentation === 'exact-hdr') {
      const target = controlCodeHDRFreezeTarget;
      const controller = experimentalClientHDRController;
      const snapshot = controller && typeof controller.snapshot === 'function' ? controller.snapshot() : null;
      if (!target || target.requestId !== requestID || !target.exactPresented ||
        codeResultArea.dataset.presentation !== 'exact-hdr' || !codeResultImage.hidden ||
        !experimentalMediaCanvas || experimentalMediaCanvas.hidden ||
        !snapshot || snapshot.surfaceVisible !== true || snapshot.presentationState !== 'visible' ||
        Number(snapshot.epoch || 0) !== target.epoch || Number(snapshot.sequence || 0) !== target.sequence ||
        typeof controller.ensureExactProof !== 'function' ||
        !controller.ensureExactProof(target.epoch, target.sequence)) return false;
      const hdrRect = experimentalMediaCanvas.getBoundingClientRect();
      if (hdrRect.width <= 0 || hdrRect.height <= 0) return false;
      const hdrStyle = window.getComputedStyle(experimentalMediaCanvas);
      return hdrStyle.display !== 'none' && hdrStyle.visibility !== 'hidden' && Number(hdrStyle.opacity || 1) > 0;
    }
    if (codeResultImage.hidden || !codeResultImage.complete ||
      codeResultImage.naturalWidth <= 0 || codeResultImage.naturalHeight <= 0) return false;
    const imageRect = codeResultImage.getBoundingClientRect();
    if (imageRect.width <= 0 || imageRect.height <= 0) return false;
    const imageStyle = window.getComputedStyle(codeResultImage);
    return imageStyle.display !== 'none' && imageStyle.visibility !== 'hidden' && Number(imageStyle.opacity || 1) > 0;
  }

  function waitForControlCodePaintFrame() {
    return new Promise((resolve) => {
      let settled = false;
      let frameID = 0;
      let timer = null;
      const finish = (painted) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        if (!painted && frameID) cancelAnimationFrame(frameID);
        resolve(Boolean(painted));
      };
      timer = setTimeout(() => finish(false), controlCodeResultPaintFrameTimeoutMs);
      frameID = requestAnimationFrame(() => finish(true));
    });
  }

  function revealControlCodeResultImageAtomically(requestID, presentation) {
    return new Promise((resolve) => {
      let settled = false;
      let frameID = 0;
      let timer = null;
      const finish = (revealed) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        if (!revealed && frameID) cancelAnimationFrame(frameID);
        resolve(Boolean(revealed));
      };
      timer = setTimeout(() => finish(false), controlCodeResultPaintFrameTimeoutMs);
      frameID = requestAnimationFrame(() => {
        if (document.visibilityState !== 'visible' ||
            locallyClosedControlCodeRequestIDs.has(requestID) ||
            !codeRequest ||
            String(codeRequest.requestId || '').trim() !== requestID ||
            codeRequest.status !== 'succeeded' ||
            !codeResultImage.complete ||
            codeResultImage.naturalWidth <= 0 ||
            codeResultImage.naturalHeight <= 0) {
          finish(false);
          return;
        }
        if (presentation === 'exact-hdr') {
          const target = controlCodeHDRFreezeTarget;
          const controller = experimentalClientHDRController;
          if (!target || target.requestId !== requestID || !target.exactPresented ||
            !controller || typeof controller.ensureExactProof !== 'function' ||
            !controller.ensureExactProof(target.epoch, target.sequence)) {
            finish(false);
            return;
          }
          codeResultArea.dataset.presentation = 'exact-hdr';
          codeResultImage.hidden = true;
        } else {
          delete codeResultArea.dataset.presentation;
          codeResultImage.hidden = false;
        }
        setControlCodeResultVisible(true);
        finish(true);
      });
    });
  }

  async function displayControlCodeResultImage(requestID, proof, capturedImage, outcome) {
    if (!requestID || !capturedImage) return false;
    setControlCodeResultVisible(false);
    codeResultImage.hidden = true;
    codeResultImage.removeAttribute('src');
    codeResultImage.src = capturedImage;
    codeResultStatus.textContent = '';
    codeResultStatus.hidden = true;
    codeResultValue.hidden = true;
    codeResultValue.textContent = '';
    codeResultValue.style.display = '';
    codeResultTimer.hidden = false;
    codeResultTimer.textContent = '';
    codeResultArea.dataset.status = 'succeeded';
    codeResultArea.style.background = '#000';
    let painted = false;
    try {
      if (!await waitForControlCodeResultImageReady(codeResultImage)) return false;
      let presentation = await waitForControlCodeExactHDRPresentation(requestID, proof)
        ? 'exact-hdr'
        : 'sdr';
      if (presentation !== 'exact-hdr') forceControlCodeResultSDRFallback('exact_hdr_unavailable');
      let revealed = await revealControlCodeResultImageAtomically(requestID, presentation);
      if (revealed && controlCodeResultPaintReady(requestID, presentation)) {
        revealed = await waitForControlCodePaintFrame() &&
          await waitForControlCodePaintFrame() &&
          controlCodeResultPaintReady(requestID, presentation);
      } else {
        revealed = false;
      }
      if (!revealed && presentation === 'exact-hdr') {
        forceControlCodeResultSDRFallback('exact_hdr_paint_incomplete');
        presentation = 'sdr';
        revealed = await revealControlCodeResultImageAtomically(requestID, presentation);
        if (revealed && controlCodeResultPaintReady(requestID, presentation)) {
          revealed = await waitForControlCodePaintFrame() &&
            await waitForControlCodePaintFrame() &&
            controlCodeResultPaintReady(requestID, presentation);
        } else {
          revealed = false;
        }
      }
      if (!revealed) return false;
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      painted = true;
      if (controlCodePreparedCaptureDisplayedRequestID !== requestID) {
        controlCodePreparedCaptureDisplayedRequestID = requestID;
        controlCodeCaptureTrace('control_code_frame_painted', { requestId: requestID, status: 'succeeded' }, proof, {
          outcome: outcome || 'browser_capture_painted',
          presentation,
          provisional: false
        });
        // Compatibility event: this now means the image survived the paint
        // handshake, not merely that its data URL was assigned.
        controlCodeCaptureTrace('control_code_frame_displayed', { requestId: requestID, status: 'succeeded' }, proof, {
          outcome: outcome || 'browser_capture_displayed',
          presentation,
          provisional: false
        });
      }
      lastControlCodeCaptureDebug = Object.assign({}, proof, {
        accepted: proof.accepted,
        candidateAccepted: true,
        fingerprintDifferenceScore: proof.fingerprintDifferenceScore,
        capturedNaturalWidth: codeResultImage.naturalWidth,
        capturedNaturalHeight: codeResultImage.naturalHeight,
        capturedRenderedWidth: Math.round(codeResultImage.getBoundingClientRect().width),
        capturedRenderedHeight: Math.round(codeResultImage.getBoundingClientRect().height),
        controlCodeSafeGeneratedFrameCount,
        controlCodeFrozenFrameKey,
        resultPresentation: presentation,
        capturedAt: Date.now()
      });
      publishStreamDebug();
      return true;
    } finally {
      if (!painted) clearUnpaintedControlCodeResultImage(requestID);
    }
  }

  function controlCodeResultDisplayedForRequest(requestID) {
    requestID = String(requestID || '').trim();
    const exactHDR = codeResultArea.dataset.presentation === 'exact-hdr' &&
      controlCodeHDRFreezeTarget && controlCodeHDRFreezeTarget.requestId === requestID &&
      controlCodeHDRFreezeTarget.exactPresented;
    return Boolean(requestID &&
      controlCodePreparedCaptureDisplayedRequestID === requestID &&
      !codeResultArea.hidden &&
      ((exactHDR && codeResultImage.hidden) ||
        (!codeResultImage.hidden && Boolean(codeResultImage.currentSrc || codeResultImage.src))));
  }

  function noteControlCodeMarkerWaiting(request) {
    const requestID = String(request && request.requestId || '').trim();
    const markerEpoch = Number(request && (request.resultFrameEpoch || request.streamEpoch) || 0);
    const markerSequence = Number(request && (request.resultMinFrameSequence || request.minFrameSequence || request.frameSequence) || 0);
    lastControlCodeCaptureDebug = {
      requestId: requestID,
      resultProof: String(request && request.resultProof || '').trim(),
      markerEpoch,
      markerSequence,
      markerReceivedAgeMillis: controlCodeMarkerReceivedAgeMillis(request),
      candidateFrameEpoch: controlCodeRenderedFrameEpoch(),
      candidateFrameSequence: controlCodeRenderedFrameSequence(),
      candidateAccepted: false,
      candidateRejectedReason: 'marker_waiting',
      keyframeRetryCount: lastControlCodeCaptureKeyframeRetryCount
    };
    const logKey = [
      requestID,
      markerEpoch,
      markerSequence,
      lastControlCodeCaptureDebug.candidateFrameEpoch,
      lastControlCodeCaptureDebug.candidateFrameSequence
    ].join(':');
    if (logKey !== lastControlCodeMarkerWaitingLogKey) {
      lastControlCodeMarkerWaitingLogKey = logKey;
      controlCodeCaptureTrace('control_code_marker_frame_waiting', request, lastControlCodeCaptureDebug, {
        reason: 'frame_before_marker',
        captureWaitMs: controlCodeResultCaptureStartedAt ? Math.round(performance.now() - controlCodeResultCaptureStartedAt) : 0
      });
    }
    publishStreamDebug();
  }

  function noteControlCodeCandidateRejected(proof) {
    proof = proof || {};
    const now = performance.now();
    const reason = String(proof.candidateRejectedReason || proof.reason || 'candidate_rejected');
    const logKey = [
      String(proof.requestId || ''),
      reason,
      Number(proof.candidateFrameEpoch || 0),
      Number(proof.candidateFrameSequence || 0)
    ].join(':');
    if (logKey !== lastControlCodeCandidateRejectedLogKey) {
      lastControlCodeCandidateRejectedLogKey = logKey;
      controlCodeCaptureTrace('control_code_candidate_rejected', codeRequest, proof, {
        reason,
        captureWaitMs: controlCodeResultCaptureStartedAt ? Math.round(now - controlCodeResultCaptureStartedAt) : 0
      });
    }
    const firstRetryReady = !controlCodeResultCaptureStartedAt ||
      now - controlCodeResultCaptureStartedAt >= controlCodeResultInitialKeyframeDelayMs;
    const retryReady = lastControlCodeCaptureKeyframeRequestAt > 0
      ? now - lastControlCodeCaptureKeyframeRequestAt >= controlCodeCaptureKeyframeRetryMs
      : firstRetryReady;
    if (
      lastControlCodeCaptureKeyframeRetryCount < controlCodeCaptureKeyframeRetryLimit &&
      retryReady
    ) {
      if (requestKeyframeDebounced(`control_code_candidate_rejected_${reason}`, controlCodeCaptureKeyframeRetryMs)) {
        lastControlCodeCaptureKeyframeRequestAt = now;
        lastControlCodeCaptureKeyframeRetryCount += 1;
        controlCodeCaptureTrace('control_code_frame_retry_requested', codeRequest, proof, {
          reason,
          retrySource: 'candidate_rejected',
          captureWaitMs: controlCodeResultCaptureStartedAt ? Math.round(now - controlCodeResultCaptureStartedAt) : 0
        });
      }
    }
    lastControlCodeCaptureDebug = Object.assign({}, proof, {
      accepted: false,
      candidateAccepted: false,
      candidateRejectedReason: reason,
      keyframeRetryCount: lastControlCodeCaptureKeyframeRetryCount,
      rejectedAt: Date.now()
    });
    publishStreamDebug();
  }

  async function confirmControlCodeBrowserCapture(request, proof) {
    const requestID = String(request && request.requestId || '').trim();
    if (!requestID || !proof || !proof.accepted) return false;
    await runSpacetimeMutation((client) => client.confirmControlCodeBrowserCapture(
      requestID,
      Number(proof.candidateFrameEpoch || 0),
      Number(proof.candidateFrameSequence || 0),
      String(proof.acceptedReason || 'candidate_frame_at_or_after_phone_marker_and_generated_visual')
    ), 'control_code_browser_capture');
    controlCodeCaptureTrace('control_code_browser_capture_ack_sent', request, proof, {
      acceptedReason: String(proof.acceptedReason || 'candidate_frame_at_or_after_phone_marker_and_generated_visual')
    });
    return true;
  }

  function captureControlCodeResultImage(proof) {
    const sourceCanvas = controlCodeFrozenCandidateFrameForProof(proof);
    if (!sourceCanvas) return '';
    const captureCanvas = document.createElement('canvas');
    captureCanvas.width = sourceCanvas.width;
    captureCanvas.height = sourceCanvas.height;
    const captureContext = captureCanvas.getContext('2d', { alpha: false });
    if (!captureContext) return '';
    captureContext.imageSmoothingEnabled = false;
    captureContext.fillStyle = '#000';
    captureContext.fillRect(0, 0, captureCanvas.width, captureCanvas.height);
    captureContext.drawImage(sourceCanvas, 0, 0, captureCanvas.width, captureCanvas.height);
    return captureCanvas.toDataURL('image/png');
  }

  function scheduleControlCodeResultCaptureRetry(requestID) {
    requestID = String(requestID || '').trim();
    if (!requestID || controlCodeResultCaptureTimer || locallyClosedControlCodeRequestIDs.has(requestID)) return;
    controlCodeResultCaptureTimer = setTimeout(() => {
      controlCodeResultCaptureTimer = null;
      if (!codeRequest || String(codeRequest.requestId || '').trim() !== requestID || codeRequest.status !== 'succeeded') return;
      waitForControlCodeResultScreenshot(codeRequest);
    }, controlCodeCapturePollMs);
  }

  async function captureControlCodeResultScreenshot(request, proof) {
    if (!request || request.status !== 'succeeded' || !hasRenderedFrame || !canvas.width || !canvas.height) return false;
    if (!proof || !proof.accepted) return false;
    const requestID = String(request.requestId || '').trim();
    if (!requestID) return false;
    if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
    let capturedAndAcknowledged = false;
    controlCodeCaptureAckInFlightRequestID = requestID;
    try {
      if (!controlCodeFrozenCandidateFrameForProof(proof)) return false;
      const capturedImage = captureControlCodeResultImage(proof);
      if (!capturedImage) return false;
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      const painted = await displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');
      if (!painted) return false;
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      await confirmControlCodeBrowserCapture(request, proof);
      controlCodeResultCapturedRequestID = requestID;
      capturedAndAcknowledged = true;
      if (!codeRequest || String(codeRequest.requestId || '').trim() !== requestID || codeRequest.status !== 'succeeded') {
        return false;
      }
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      return true;
    } catch (error) {
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      reportClientFault('control_code_browser_capture_failed', error);
      if (controlCodePreparedCaptureDisplayedRequestID !== requestID) {
        failControlCodeResultScreenshotWait();
      }
      return false;
    } finally {
      if (controlCodeCaptureAckInFlightRequestID === requestID) {
        controlCodeCaptureAckInFlightRequestID = '';
      }
      if (!capturedAndAcknowledged && !locallyClosedControlCodeRequestIDs.has(requestID)) {
        scheduleControlCodeResultCaptureRetry(requestID);
      }
    }
  }

  function failControlCodeResultScreenshotWait() {
    if (controlCodeResultCaptureTimer) {
      clearTimeout(controlCodeResultCaptureTimer);
      controlCodeResultCaptureTimer = null;
    }
    controlCodeResultCaptureRequestID = '';
    codeResultImage.hidden = true;
    codeResultImage.removeAttribute('src');
    codeResultArea.dataset.status = 'failed';
    codeResultArea.style.background = '';
    setControlCodeResultVisible(true);
    codeResultStatus.hidden = false;
    codeResultStatus.textContent = 'Koda attēlu neizdevās parādīt. Mēģini vēlreiz.';
    codeResultValue.hidden = true;
    codeResultValue.textContent = '';
    codeResultValue.style.display = '';
    codeResultTimer.hidden = false;
  }

  function maybeCaptureControlCodeResultImage() {
    if (!codeRequest || codeRequest.status !== 'succeeded') return false;
    const requestID = String(codeRequest.requestId || '').trim();
    if (!requestID || controlCodeResultCapturedRequestID === requestID) return false;
    if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
    if (controlCodeCaptureAckInFlightRequestID === requestID) return true;
    if (!controlCodeMarkerReady(codeRequest)) {
      noteControlCodeMarkerWaiting(codeRequest);
      return false;
    }
    const proof = controlCodeCandidateFrameProof(codeRequest);
    if (!proof.accepted) {
      noteControlCodeCandidateRejected(proof);
      return false;
    }
    if (controlCodeResultCaptureTimer) {
      clearTimeout(controlCodeResultCaptureTimer);
      controlCodeResultCaptureTimer = null;
    }
    controlCodeResultCaptureRequestID = '';
    captureControlCodeResultScreenshot(codeRequest, proof);
    return true;
  }

  function maybeRequestControlCodeResultWaitKeyframe(requestID, reason) {
    const now = performance.now();
    if (!controlCodeResultCaptureStartedAt) return false;
    if (now - controlCodeResultCaptureStartedAt < controlCodeResultInitialKeyframeDelayMs) return false;
    if (lastControlCodeCaptureKeyframeRetryCount >= controlCodeCaptureKeyframeRetryLimit) return false;
    if (lastControlCodeCaptureKeyframeRequestAt > 0 &&
      now - lastControlCodeCaptureKeyframeRequestAt < controlCodeCaptureKeyframeRetryMs) {
      return false;
    }
    if (!requestKeyframeDebounced(reason || 'control_code_result_wait_retry', controlCodeCaptureKeyframeRetryMs)) {
      return false;
    }
    lastControlCodeCaptureKeyframeRequestAt = now;
    lastControlCodeCaptureKeyframeRetryCount += 1;
    controlCodeCaptureTrace('control_code_frame_retry_requested', codeRequest || { requestId: requestID }, lastControlCodeCaptureDebug, {
      reason: reason || 'control_code_result_wait_retry',
      retrySource: 'result_wait',
      captureWaitMs: Math.round(now - controlCodeResultCaptureStartedAt)
    });
    return true;
  }

  function waitForControlCodeResultScreenshot(request) {
    const requestID = String(request && request.requestId || '').trim();
    if (!requestID) return;
    if (controlCodeResultCapturedRequestID === requestID) return;
    if (locallyClosedControlCodeRequestIDs.has(requestID)) return;
    const resultAlreadyDisplayed = controlCodeResultDisplayedForRequest(requestID);
    if (controlCodeResultCaptureRequestID !== requestID) {
      if (controlCodeResultCaptureTimer) clearTimeout(controlCodeResultCaptureTimer);
      controlCodeResultCaptureTimer = null;
      controlCodeResultCaptureRequestID = requestID;
      controlCodeResultCaptureStartedAt = performance.now();
      lastControlCodeMarkerReceivedLogKey = '';
      lastControlCodeMarkerWaitingLogKey = '';
      lastControlCodeCandidateRejectedLogKey = '';
      if (!resultAlreadyDisplayed) {
        codeResultImage.hidden = true;
        codeResultImage.removeAttribute('src');
      }
    }
    const markerEpoch = Number(request && (request.resultFrameEpoch || request.streamEpoch) || 0);
    const markerSequence = Number(request && (request.resultMinFrameSequence || request.minFrameSequence || request.frameSequence) || 0);
    const markerLogKey = [requestID, markerEpoch, markerSequence].join(':');
    if (markerLogKey !== lastControlCodeMarkerReceivedLogKey) {
      lastControlCodeMarkerReceivedLogKey = markerLogKey;
      controlCodeCaptureTrace('control_code_marker_received', request, null, {
        markerEpoch,
        markerSequence
      });
    }
    if (resultAlreadyDisplayed) {
      codeResultArea.dataset.status = 'succeeded';
      codeResultArea.style.background = '#000';
      codeResultStatus.hidden = true;
      codeResultStatus.textContent = '';
      codeResultValue.hidden = true;
      codeResultValue.textContent = '';
      codeResultValue.style.display = '';
      codeResultTimer.hidden = false;
    } else {
      codeResultArea.dataset.status = 'waiting';
      codeResultArea.style.background = '';
      codeResultStatus.hidden = true;
      codeResultStatus.textContent = '';
      codeResultValue.hidden = true;
      codeResultValue.textContent = '';
      codeResultValue.style.display = '';
      codeResultTimer.hidden = true;
      codeResultTimer.textContent = '';
      setControlCodeResultVisible(false);
    }
    keepControlCodeVideoAlive('control_code_wait_reconnect');
    if (maybeCaptureControlCodeResultImage()) return;
    requestControlCodeLowLatencyFrame(requestID, 'control_code_result_marker_low_latency');
    maybeRequestControlCodeResultWaitKeyframe(requestID, 'control_code_result_wait_retry');
    const tick = () => {
      if (!codeRequest || codeRequest.requestId !== requestID || codeRequest.status !== 'succeeded') {
        if (controlCodeResultCaptureTimer) clearTimeout(controlCodeResultCaptureTimer);
        controlCodeResultCaptureTimer = null;
        controlCodeResultCaptureRequestID = '';
        controlCodeResultCaptureStartedAt = 0;
        return;
      }
      if (maybeCaptureControlCodeResultImage()) return;
      maybeRequestControlCodeResultWaitKeyframe(requestID, 'control_code_result_wait_retry');
      controlCodeResultCaptureTimer = setTimeout(tick, controlCodeCapturePollMs);
    };
    if (!controlCodeResultCaptureTimer) controlCodeResultCaptureTimer = setTimeout(tick, controlCodeCapturePollMs);
  }

  function rememberOwnedControlCodeRequest(request) {
    const requestID = String(request && request.requestId || '').trim();
    if (requestID) ownedControlCodeRequestIDs.add(requestID);
  }

  function isOwnedControlCodeRequest(request) {
    const requestID = String(request && request.requestId || '').trim();
    if (!requestID) return false;
    const ownerPublicID = String(request && request.ownerPublicId || '').trim();
    if (ownerPublicID && localPublicID && ownerPublicID !== localPublicID) return false;
    const requestSessionID = String(request && request.sessionId || '').trim();
    if (requestSessionID && localSessionID && requestSessionID !== localSessionID) return false;
    return ownedControlCodeRequestIDs.has(requestID) ||
      (ownerPublicID && localPublicID && ownerPublicID === localPublicID) ||
      (requestSessionID && localSessionID && requestSessionID === localSessionID) ||
      Boolean(codeRequest && codeRequest.requestId === requestID);
  }

  function normalizedControlCodeRequestSignature(request) {
    if (!request) return 'none';
    const fields = [
      'requestId', 'ownerPublicId', 'sessionId', 'status', 'reason', 'message',
      'queuePosition', 'requestedAt', 'updatedAt', 'expiresAt', 'resultExpiresAt',
      'resultProof', 'resultProofAt', 'captureRequired', 'captureAcknowledged',
      'cleanupPending', 'streamEpoch', 'frameSequence', 'minFrameSequence',
      'resultFrameEpoch', 'resultMinFrameSequence', 'captureFrameEpoch',
      'captureFrameSequence'
    ];
    return fields.map((field) => `${field}=${safeString(request[field])}`).join('|');
  }

  function renderControlCodeRequest(request) {
    if (request && !isOwnedControlCodeRequest(request)) {
      clientLog('control_code_message_ignored', 'not_requesting_session');
      return;
    }
    const requestID = String(request && request.requestId || '').trim();
    if (requestID && locallyClosedControlCodeRequestIDs.has(requestID) && request.status !== 'closed' && request.status !== 'expired') {
      clientLog('control_code_message_ignored', 'locally_closed');
      return;
    }
    if (requestID) rememberOwnedControlCodeRequest(request);
    if (request && codeRequest && request.requestId === codeRequest.requestId) {
      const incomingRank = controlCodeStatusRank(request.status);
      const currentRank = controlCodeStatusRank(codeRequest.status);
      if (incomingRank < currentRank) {
        request = codeRequest;
      }
    }
    const nextRequest = request || codeRequest;
    const renderSignature = normalizedControlCodeRequestSignature(nextRequest);
    codeRequest = nextRequest;
    updateControlCodeSubmitAvailability();
    if (renderSignature === lastRenderedControlCodeRequestSignature) return;
    lastRenderedControlCodeRequestSignature = renderSignature;
    const current = codeRequest;
    const currentRequestID = String(current && current.requestId || '').trim();
    if (current && ['succeeded', 'failed', 'closed', 'expired'].includes(String(current.status || ''))) {
      controlCodeSubmitInFlight = false;
    }
    const busy = current && (current.status === 'queued' || current.status === 'running');
    codeRequestState.textContent = controlCodeStatusText(current && current.status, current && current.reason);
    codeRequestDetail.textContent = controlCodeDetailText(current);
    if (requestID && !requestID.startsWith('pending:') && current && (busy || current.status === 'succeeded')) {
      rememberControlCodeBaselineFrame(requestID);
    }
    if (busy) {
      keepControlCodeVideoAlive('control_code_request_active');
      if (controlCodeResultDisplayedForRequest(currentRequestID)) {
        scheduleControlCodeTicker(current);
        return;
      }
      // The submission path captured the raw-ticket baseline before the
      // reducer call. Keep that baseline through queued/running renders so the
      // later exact phone marker can be compared with the generated result.
      // A full capture reset here used to erase both the pending baseline and
      // the real-request baseline before the succeeded row arrived.
      setControlCodeResultVisible(false);
      clearUnpaintedControlCodeResultImage(currentRequestID);
      scheduleControlCodeTicker(current);
      return;
    }
    if (!current || current.status === 'closed' || current.status === 'expired') {
      setControlCodeResultVisible(false);
      clearControlCodeResultCapture();
      delete codeResultArea.dataset.status;
      codeResultStatus.hidden = false;
      codeResultStatus.textContent = '';
      codeResultValue.hidden = true;
      codeResultValue.textContent = '';
      codeResultValue.style.display = '';
      codeResultArea.style.background = '';
      codeResultTimer.hidden = false;
      codeResultTimer.textContent = '';
      scheduleControlCodeTicker(null);
      return;
    }
    if (current.status === 'succeeded') {
      waitForControlCodeResultScreenshot(current);
      scheduleControlCodeTicker(current);
      return;
    }
    if (current.status === 'failed') {
      setControlCodeResultVisible(true);
      clearControlCodeResultCapture();
      codeResultArea.dataset.status = 'failed';
      codeResultStatus.textContent = controlCodeStatusText('failed', current.reason || current.message);
      codeResultStatus.hidden = false;
      codeResultValue.hidden = true;
      codeResultValue.textContent = '';
      codeResultValue.style.display = '';
      codeResultArea.style.background = '';
      codeResultTimer.hidden = false;
      codeResultTimer.textContent = '';
      scheduleControlCodeTicker(null);
      return;
    }
    setControlCodeResultVisible(false);
    clearControlCodeResultCapture();
    scheduleControlCodeTicker(null);
  }

  function setDetailsPanelVisible(visible) {
    const revealed = Boolean(visible);
    document.body.classList.toggle('details-visible', revealed);
    noteExperimentalMediaStreamRegionVisibility(
      !revealed,
      revealed ? 'details_visible' : 'details_hidden'
    );
    if (panel) panel.setAttribute('aria-hidden', visible ? 'false' : 'true');
  }

  function lockControlCodeDialogScroll() {
    if (controlCodeDialogScrollLock && controlCodeDialogScrollLock.active) return;
    const detailsVisible = document.body.classList.contains('details-visible');
    controlCodeDialogScrollLock = {
      active: true,
      detailsVisible
    };
  }

  function unlockControlCodeDialogScroll() {
    const lock = controlCodeDialogScrollLock;
    if (!lock || !lock.active) return;
    lock.active = false;
    setDetailsPanelVisible(lock.detailsVisible);
    controlCodeDialogScrollLock = null;
    updateDetailsReveal();
  }

  function settleCodeDialogScrollUnlock() {
    if (!controlCodeDialogScrollLock || !controlCodeDialogScrollLock.active) return;
    if (codeDialogOpen) return;
    unlockControlCodeDialogScroll();
  }

  function openControlCodeDialog() {
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      setStatus('Sagaidi svaigu tiešraides kadru, pirms pieprasi kontroles kodu.');
      updateControlCodeSubmitAvailability();
      return false;
    }
    if (controlCodeMutationLaneBusy()) return;
    if (document.fullscreenElement && typeof document.exitFullscreen === 'function') {
      try {
        document.exitFullscreen().catch(() => {});
      } catch (_) {}
    }
    lockControlCodeDialogScroll();
    codeDialogOpen = true;
    document.body.classList.add('code-dialog-open');
    updateViewportVars();
    codeDialog.hidden = false;
    codeError.textContent = '';
    codeDigits.value = '';
    updateControlCodeSubmitAvailability();
    setTimeout(() => {
      updateViewportVars();
      codeDigits.focus({ preventScroll: true });
    }, 30);
    return true;
  }

  function closeControlCodeDialog() {
    codeDialogOpen = false;
    if (codeDialog.contains(document.activeElement) && typeof document.activeElement.blur === 'function') {
      try {
        document.activeElement.blur();
      } catch (_) {}
    }
    document.body.classList.remove('code-dialog-open', 'keyboard-active');
    codeDialog.hidden = true;
    codeError.textContent = '';
    updateViewportVars();
    resizeCanvasBox();
    updateControlCodeSubmitAvailability();
    settleCodeDialogScrollUnlock();
  }

  function ticketRegisterOverlayOccupiesHotspot() {
    if (ticketRegisterOverlay.hidden) return false;
    const sliderRect = ticketRegisterOverlay.getBoundingClientRect();
    const hotspotRect = controlCodeHotspot.getBoundingClientRect();
    return sliderRect.width > 0 && sliderRect.height > 0 &&
      hotspotRect.width > 0 && hotspotRect.height > 0 &&
      sliderRect.left < hotspotRect.right && sliderRect.right > hotspotRect.left &&
      sliderRect.top < hotspotRect.bottom && sliderRect.bottom > hotspotRect.top;
  }

  function requestControlCodeFromHotspot(event) {
    if (event) {
      event.preventDefault();
      event.stopPropagation();
    }
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      setStatus('Sagaidi svaigu tiešraides kadru, pirms pieprasi kontroles kodu.');
      updateControlCodeSubmitAvailability();
      return false;
    }
    if (codeDialogOpen || !codeDialog.hidden || !codeResultArea.hidden || ticketRegisterOverlayOccupiesHotspot()) return;
    if (controlCodeMutationLaneBusy() || memberLimitBlocked('control_code')) return;
    openControlCodeDialog();
  }

  async function submitControlCodeRequest() {
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      codeError.textContent = 'Sagaidi svaigu tiešraides kadru, pirms pieprasi kontroles kodu.';
      updateControlCodeSubmitAvailability();
      return false;
    }
    const digits = sanitizeControlDigits(codeDigits.value);
    codeDigits.value = digits;
    if (digits.length < 2 || digits.length > 8) {
      codeError.textContent = 'Ievadi 2-8 ciparus.';
      return;
    }
    if (controlCodeMutationLaneBusy()) {
      codeError.textContent = localizePublicMessage(
        controlCodeRequestOccupiesQueue() ? 'request_in_progress' : 'ticket_action_in_progress'
      );
      return;
    }
    const fastRevision = controlCodeFastRevisionForRequest();
    codeError.textContent = '';
    controlCodeSubmitInFlight = true;
    updateControlCodeSubmitAvailability();
    pendingControlCodeBaselineFrameFingerprint = canvasRegionFingerprint(controlCodeFingerprintRegion());
    const submittedAt = performance.now();
    try {
      await runSpacetimeMutation((client) => client.requestControlCode(digits, fastRevision), 'control_code_request');
      const mutationLatencyMs = Math.round(performance.now() - submittedAt);
      clientLog('control_code_submitted', JSON.stringify({
        digitCount: digits.length,
        mutationLatencyMs,
        viewportHeight: window.innerHeight,
        fastState: String(controlCodeFastState && controlCodeFastState.status || 'missing'),
        fastReady: controlCodeFastStateFresh(),
        fastRevisionSent: Boolean(fastRevision),
        immediate: true,
      }));
      renderControlCodeRequest({
        requestId: `pending:${Date.now()}`,
        ownerPublicId: localPublicID,
        status: 'queued',
        reason: 'requested',
        requestedAt: new Date().toISOString(),
        updatedAt: new Date().toISOString()
      });
      closeControlCodeDialog();
      setStatus('Pieprasījums nosūtīts.');
    } catch (error) {
      clientLog('control_code_request_failed', error && error.message || 'request failed');
      codeError.textContent = localizePublicMessage(error && error.message || 'Pieprasījums neizdevās');
    } finally {
      controlCodeSubmitInFlight = false;
      updateControlCodeSubmitAvailability();
    }
  }

  async function closeCurrentControlCode(openNext) {
    const request = codeRequest;
    const requestID = String(request && request.requestId || '').trim();
    const canCloseRequest = Boolean(requestID && (
      ownedControlCodeRequestIDs.has(String(requestID)) || isOwnedControlCodeRequest(request)
    ));
    if (requestID) {
      // The result overlay is requester-local. Clear it first even if a delayed
      // subscription update has not restored the ownership cache yet.
      locallyClosedControlCodeRequestIDs.add(String(requestID));
      if (request && request.status === 'succeeded' && request.cleanupPending === true) {
        // The phone is available again only after badge-cross cleanup publishes
        // a fresh fast-ready state. Keep a local admission barrier while that
        // cleanup is still in flight.
        controlCodeCleanupPendingRequestID = requestID;
      } else {
        // A completed request already has its post-cleanup fast state. Do not
        // leave a local barrier behind when the browser simply dismisses the
        // finished result after cleanup has completed.
        controlCodeCleanupPendingRequestID = '';
      }
    }
    setControlCodeResultVisible(false);
    clearControlCodeResultCapture();
    scheduleControlCodeTicker(null);
    if (requestID && codeRequest && String(codeRequest.requestId || '').trim() === requestID) {
      codeRequest = null;
    }
    if (requestID && canCloseRequest) {
      try {
        await runSpacetimeMutation((client) => client.closeControlCode(requestID, 'browser_closed'), 'control_code_close');
      } catch (error) {
        clientLog('control_code_close_failed', error && error.message || 'close failed');
      }
    } else if (requestID) {
      clientLog('control_code_close_local_only', 'not_owned');
    }
    if (openNext) openControlCodeDialog();
  }

  function relayReportToStreamStatus(report) {
    if (!report) return null;
    let detail = {};
    try {
      detail = JSON.parse(report.statusJson || '{}') || {};
    } catch (_) {
      detail = {};
    }
    return Object.assign({}, detail, {
      type: 'stream_status',
      streamVerdict: String(report.streamVerdict || detail.streamVerdict || ''),
      activeVideoClients: Number(report.videoClients ?? detail.activeVideoClients ?? 0),
      lastFrameAgoMillis: Number(report.lastFrameAgoMillis ?? detail.lastFrameAgoMillis ?? 0),
      framesForwarded: Number(report.framesForwarded ?? detail.framesForwarded ?? 0),
      phoneStreamState: String(detail.phoneStreamState || ''),
      phoneConnected: detail.phoneConnected,
      phoneDesired: detail.phoneDesired,
      updatedAt: String(report.updatedAt || detail.serverTime || '')
    });
  }

  function controlCodeRequestExpiryTime(request) {
    const status = String(request && request.status || '');
    const expiryValue = status === 'succeeded'
      ? (request && (request.resultExpiresAt || request.expiresAt))
      : (request && request.expiresAt);
    const parsed = Date.parse(expiryValue || '');
    return Number.isFinite(parsed) ? parsed : 0;
  }

  function controlCodeRequestSortTime(request) {
    const updatedAt = Date.parse(request && request.updatedAt || '');
    if (Number.isFinite(updatedAt)) return updatedAt;
    const requestedAt = Date.parse(request && request.requestedAt || '');
    return Number.isFinite(requestedAt) ? requestedAt : 0;
  }

  function controlCodeRequestIsStillRelevant(request) {
    if (!request) return false;
    const requestID = String(request.requestId || '').trim();
    if (requestID && locallyClosedControlCodeRequestIDs.has(requestID)) return false;
    const status = String(request.status || '');
    if (status === 'closed' || status === 'expired') return false;
    const expiresAt = controlCodeRequestExpiryTime(request);
    if (!expiresAt) return true;
    return (Date.now() + serverClockSkewMs) <= expiresAt + 1000;
  }

  function controlCodeVisualRecoveryRequired() {
    if (controlCodeCleanupPendingRequestID) return true;
    const requests = Array.isArray(currentState && currentState.controlCodeRequests)
      ? currentState.controlCodeRequests
      : [];
    const candidates = codeRequest && !requests.some((request) =>
      request && String(request.requestId || '') === String(codeRequest.requestId || '')
    ) ? [...requests, codeRequest] : requests;
    return ticketControlCodeVisualRecoveryRequired(candidates.filter((request) =>
      isOwnedControlCodeRequest(request)
    ));
  }

  function latestOwnedControlCodeRequest(state) {
    const requests = Array.isArray(state && state.controlCodeRequests) ? state.controlCodeRequests : [];
    return requests
      .filter((request) => isOwnedControlCodeRequest(request) && controlCodeRequestIsStillRelevant(request))
      .sort((a, b) => controlCodeRequestSortTime(b) - controlCodeRequestSortTime(a))[0] || null;
  }

  function reconcileControlCodeCleanupBarrier(state) {
    const pendingRequestID = String(controlCodeCleanupPendingRequestID || '').trim();
    const requests = Array.isArray(state && state.controlCodeRequests) ? state.controlCodeRequests : null;
    if (!pendingRequestID || !requests) return false;
    const authoritativeRequestStillPresent = requests.some((request) =>
      request &&
      String(request.requestId || '').trim() === pendingRequestID
    );
    if (authoritativeRequestStillPresent) return false;
    controlCodeCleanupPendingRequestID = '';
    clientLog('control_code_cleanup_barrier_cleared', 'authoritative_request_absent');
    return true;
  }

  function renderState() {
    const state = currentState;
    if (!state) return;
    if (observeTicketActionV3LocalRequest(ticketActionV3LocalRequestState, state.ticketAction)) {
      if (ticketActionV3ReconcileTimer) {
        clearTimeout(ticketActionV3ReconcileTimer);
        ticketActionV3ReconcileTimer = null;
      }
      clientLog('ticket_action_v3_authoritative', String(state.ticketAction && state.ticketAction.target || 'observed'));
    }
    rememberServerClock(state);
    controlCodeFastState = state && state.controlCodeFastState || null;
    renderControlCodeFastStateDataset();
    scheduleControlCodeFastStateExpiryCheck();
    const viewers = activeViewerPresence(state);
    const visibleViewerCount = Number.isFinite(Number(state.viewerCount)) ? Number(state.viewerCount) : viewers.length;
    renderViewerSummary(viewers, visibleViewerCount);
    const relayStatus = relayReportToStreamStatus(state.relayCurrentReport);
    if (relayStatus) handleStreamStatus(relayStatus);
    reconcileControlCodeCleanupBarrier(state);
    const ownedRequest = latestOwnedControlCodeRequest(state);
    if (ownedRequest) {
      renderControlCodeRequest(ownedRequest);
    } else {
      const requestRows = Array.isArray(state && state.controlCodeRequests)
        ? state.controlCodeRequests
        : null;
      const localRequestID = String(codeRequest && codeRequest.requestId || '').trim();
      const localRequestStillPresent = Boolean(requestRows && localRequestID && requestRows.some((request) =>
        request && String(request.requestId || '').trim() === localRequestID &&
        isOwnedControlCodeRequest(request) &&
        request.status !== 'closed' && request.status !== 'expired'
      ));
      // A browser can be reopened after the phone has already timed out or closed a
      // request. Do not let a locally retained request keep the code button locked
      // after the authoritative Spacetime snapshot has removed it. Keep a very recent
      // optimistic queued/running row briefly so the reducer subscription can catch up.
      if (requestRows && codeRequest && !localRequestStillPresent) {
        const localStatus = String(codeRequest.status || '');
        const localUpdatedAt = Date.parse(String(codeRequest.updatedAt || codeRequest.requestedAt || ''));
        const localAge = Number.isFinite(localUpdatedAt)
          ? Math.max(0, Date.now() + serverClockSkewMs - localUpdatedAt)
          : Number.POSITIVE_INFINITY;
        const terminalWithoutFailure = ['succeeded', 'closed', 'expired'].includes(String(codeRequest.status || ''));
        const terminal = terminalWithoutFailure || localStatus === 'failed';
        const stale = localAge >= controlCodeRequestMissingRowStaleAfterMs;
        if (terminal || stale) {
          clearControlCodeRequestLocalState(terminal ? `missing_terminal_${localStatus || 'unknown'}` : 'missing_stale');
        }
      }
      renderControlCodeRequest(controlCodeRequestIsStillRelevant(codeRequest) ? codeRequest : null);
    }
    if (!relayStatus || String(relayStatus.streamVerdict || '') === 'live') {
      setStatus('Tiešraide rāda biļeti.');
    }
    renderTicketInteraction(spacetimeStateFresh ? state.ticketInteraction : null);
    renderTicketActionV3Controls(state);
    renderPresence(viewers, visibleViewerCount);
  }

  function ticketInteractionPreparingIsStale(interaction, now) {
    const status = String(interaction && interaction.status || '');
    if (status !== 'reset_queued' && status !== 'preparing') return false;
    const updatedAt = Date.parse(String(interaction && interaction.updatedAt || ''));
    if (!Number.isFinite(updatedAt)) return false;
    const currentTime = Number.isFinite(now) ? now : Date.now() + serverClockSkewMs;
    return currentTime - updatedAt >= ticketInteractionPreparingStaleAfterMs;
  }

  function ticketInteractionForDisplay(interaction) {
    if (!interaction || !ticketInteractionPreparingIsStale(interaction)) return interaction;
    return Object.assign({}, interaction, {
      status: 'needs_attention',
      reason: 'ticket_reset_stale'
    });
  }

  function ticketInteractionIsBusy(interaction) {
    const display = ticketInteractionForDisplay(interaction);
    return ['reset_queued', 'preparing', 'control_active', 'completing'].includes(String(display && display.status || ''));
  }

  function memberLimits(state = currentState) {
    if (state === currentState && !spacetimeStateFresh) return null;
    return state && state.memberLimits || null;
  }

  function memberLimitBlocked(kind, state = currentState) {
    return ticketMemberLimitBlocks(memberLimits(state), kind);
  }

  function activationPolicyBlocked(state = currentState) {
    return memberLimitBlocked('registration', state);
  }

  function activationPolicyReason(state = currentState) {
    const limits = memberLimits(state);
    return String(limits && limits.registrationReason || 'activation_policy_unavailable');
  }

  function activationPolicyMessage(state = currentState) {
    const reason = activationPolicyReason(state);
    if (reason === 'registration_interval') return 'Starp reģistrācijām jāgaida 30 sekundes.';
    if (reason === 'registration_hour_limit') return 'Pēdējās stundas 10 reģistrāciju limits ir sasniegts.';
    if (reason === 'registration_allowed' || reason === 'limits_bypassed') return '';
    return 'SpaceTime reģistrācijas lēmums vēl nav pieejams.';
  }

  function renderMemberLimits(state = currentState) {
    const limits = memberLimits(state);
    if (!limits) {
      if (ticketLimitPresentationTimer) {
        clearTimeout(ticketLimitPresentationTimer);
        ticketLimitPresentationTimer = null;
      }
      ticketLimitMode.textContent = 'Nav pieejams';
      ticketRegistrationLimitUsage.textContent = '—';
      ticketRegistrationLimitDetail.textContent = 'Gaida SpaceTime stāvokli.';
      ticketControlCodeLimitUsage.textContent = '—';
      ticketControlCodeLimitDetail.textContent = 'Gaida SpaceTime stāvokli.';
      return;
    }
    ticketMemberLimitClock = updateTicketMemberLimitClock(
      ticketMemberLimitClock,
      limits,
      performance.now()
    );
    const registrationCount = Math.max(0, Number(limits.registrationCount || 0));
    const registrationLimit = Math.max(1, Number(limits.registrationLimit || 10));
    const controlCodeCount = Math.max(0, Number(limits.controlCodeCount || 0));
    const controlCodeLimit = Math.max(1, Number(limits.controlCodeLimit || 2));
    const now = ticketMemberLimitClockNow(ticketMemberLimitClock, performance.now());
    if (!Number.isFinite(now)) {
      ticketLimitMode.textContent = 'Nav pieejams';
      ticketRegistrationLimitUsage.textContent = '—';
      ticketRegistrationLimitDetail.textContent = 'Gaida SpaceTime stāvokli.';
      ticketControlCodeLimitUsage.textContent = '—';
      ticketControlCodeLimitDetail.textContent = 'Gaida SpaceTime stāvokli.';
      return;
    }
    ticketLimitMode.textContent = limits.effectiveLimited === false ? 'Neierobežots režīms' : 'Parastie limiti';
    ticketRegistrationLimitUsage.textContent = `${registrationCount} / ${registrationLimit} pēdējās 60 minūtēs`;
    ticketControlCodeLimitUsage.textContent = `${controlCodeCount} / ${controlCodeLimit} pēdējās 60 sekundēs`;
    if (limits.effectiveLimited === false) {
      ticketRegistrationLimitDetail.textContent = 'Admina darbības tiek auditētas, bet kvotu nepatērē.';
      ticketControlCodeLimitDetail.textContent = 'Kontroles kodu kvota šim admina kontam netiek piemērota.';
      return;
    }
    const registrationRetryText = ticketMemberLimitCountdown(limits.registrationRetryAt || limits.registrationNextReleaseAt, now);
    const registrationReleaseText = ticketMemberLimitCountdown(limits.registrationNextReleaseAt, now);
    if (limits.registrationAllowed === true) {
      ticketRegistrationLimitDetail.textContent = registrationReleaseText && registrationCount > 0
        ? `Pieejams tagad; nākamā stundas vieta ${registrationReleaseText}.`
        : 'Pieejams tagad; starp reģistrācijām jābūt vismaz 30 sekundēm.';
    } else {
      ticketRegistrationLimitDetail.textContent = `${activationPolicyMessage(state)}${registrationRetryText ? ` Pieejams ${registrationRetryText}.` : ''}`;
    }
    const controlRetryText = ticketMemberLimitCountdown(limits.controlCodeRetryAt, now);
    ticketControlCodeLimitDetail.textContent = limits.controlCodeAllowed === true
      ? 'Pieejams tagad.'
      : `Limits sasniegts.${controlRetryText ? ` Pieejams ${controlRetryText}.` : ''}`;
    const futureTargets = [limits.registrationRetryAt, limits.registrationNextReleaseAt, limits.controlCodeRetryAt]
      .map((value) => Date.parse(String(value || '')))
      .filter((value) => Number.isFinite(value) && value > now);
    if (futureTargets.length && !ticketLimitPresentationTimer) {
      ticketLimitPresentationTimer = setTimeout(() => {
        ticketLimitPresentationTimer = null;
        renderMemberLimits(currentState);
      }, 1000);
    }
  }

  function renderTicketInteraction(_interaction) {
    if (panel) panel.dataset.ticketInteractionStatus = 'ticket_action_v3';
    ticketActivationAt.textContent = '';
    ticketActivationTimer.textContent = '';
    renderMemberLimits(spacetimeStateFresh ? currentState : null);
  }

  function ticketActionV3Id() {
    return `ticket_action_v3_${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
  }

  function ticketActionV3Busy(action) {
    return ticketActionV3OccupiesPhone(action);
  }

  function ticketActionV3RegistrationProofIsFresh(action) {
    return isTicketActionV3RegistrationProofFresh(
      action,
      ticketActionV3StreamSnapshot(),
      Date.now() + serverClockSkewMs
    );
  }

  function ticketActionV3StreamSnapshot() {
    return {
      fresh: streamHasFreshRenderedFrame(),
      epoch: Number(currentStreamEpoch || lastRenderedFrameEpoch || 0),
      sequence: Number(lastRenderedFrameSequence || lastAcceptedFrameSequence || 0)
    };
  }

  function clientHDRConsequentialControlProofReady() {
    if (!experimentalClientHDRController || !experimentalMediaState.enabled ||
      experimentalMediaState.engine !== CLIENT_HDR_ENGINE) return true;
    const snapshot = experimentalClientHDRController.snapshot();
    if (!snapshot || !snapshot.active || !snapshot.surfaceVisible) return true;
    return snapshot.visualHoldover !== true && snapshot.proofFresh === true;
  }

  function revealAuthoritativeSDRForConsequentialControl() {
    if (!experimentalClientHDRController || !experimentalMediaState.enabled ||
      experimentalMediaState.engine !== CLIENT_HDR_ENGINE) return true;
    const snapshot = experimentalClientHDRController.snapshot();
    if (!snapshot || !snapshot.active || !snapshot.surfaceVisible) return true;
    if (!clientHDRConsequentialControlProofReady()) return false;
    const stream = ticketActionV3StreamSnapshot();
    return experimentalClientHDRController.ensureExactProof(stream.epoch, stream.sequence);
  }

  function currentTicketSliderRegion(state = currentState) {
    return ticketSliderRegionV3ForAction(
      state && state.ticketAction || null,
      state && state.ticketSliderRegion || null,
      ticketActionV3StreamSnapshot(),
      Date.now() + serverClockSkewMs
    );
  }

  function currentTicketRegisterSliderProof(state = currentState) {
    return ticketLocalRegisterSliderProofSnapshot(
      state && state.ticketAction || null,
      state && state.ticketSliderRegion || null,
      ticketActionV3StreamSnapshot(),
      ticketSliderLayoutRevision,
      ticketSliderVisualRevision,
      Date.now() + serverClockSkewMs
    );
  }

  function ticketRegisterSliderProofStillMatches(snapshot, state = currentState) {
    return ticketLocalRegisterSliderProofMatches(
      snapshot,
      state && state.ticketAction || null,
      state && state.ticketSliderRegion || null,
      ticketActionV3StreamSnapshot(),
      ticketSliderLayoutRevision,
      ticketSliderVisualRevision,
      Date.now() + serverClockSkewMs
    );
  }

  function suppressTicketRegisterSliderChangeForPointerEvent() {
    ticketLocalRegisterSliderState.ignoreChange = true;
    setTimeout(() => {
      ticketLocalRegisterSliderState.ignoreChange = false;
    }, 0);
  }

  function cancelTicketRegisterSliderSession(reason, pointerId = null) {
    if (ticketLocalRegisterSliderState.inFlight) return false;
    if (!cancelTicketLocalRegisterSliderSession(ticketLocalRegisterSliderState, pointerId)) return false;
    suppressTicketRegisterSliderChangeForPointerEvent();
    ticketLocalRegisterSlider.value = '0';
    clientLog('ticket_slider_cancelled', reason || 'cancelled');
    return true;
  }

  function setTicketRegisterOverlayLayout(region) {
    const layout = ticketSliderRegionV3Layout(
      region,
      canvas.getBoundingClientRect(),
      stage.getBoundingClientRect()
    );
    if (!layout || layout.width < 30 || layout.height < 8) return null;
    ticketRegisterOverlay.style.left = `${layout.left}px`;
    ticketRegisterOverlay.style.top = `${layout.top}px`;
    ticketRegisterOverlay.style.width = `${layout.width}px`;
    ticketRegisterOverlay.style.height = `${layout.height}px`;
    return layout;
  }

  function clearTicketRegisterOverlay() {
    ticketRegisterOverlay.hidden = true;
    ticketLocalRegisterSlider.disabled = true;
    if (!ticketLocalRegisterSliderState.inFlight) ticketLocalRegisterSlider.value = '0';
    ticketRegisterOverlay.removeAttribute('aria-busy');
    ticketRegisterOverlay.dataset.registrationState = 'hidden';
    for (const property of ['left', 'top', 'width', 'height']) {
      ticketRegisterOverlay.style.removeProperty(property);
    }
  }

  function renderTicketRegisterOverlay(state, busy, controlBusy, registerReady) {
    if (ticketSliderRegionExpiryTimer) {
      clearTimeout(ticketSliderRegionExpiryTimer);
      ticketSliderRegionExpiryTimer = null;
    }
    if (ticketLocalRegisterSliderState.inFlight && ticketLocalRegisterSliderState.latchedProof) {
      if (!setTicketRegisterOverlayLayout(ticketLocalRegisterSliderState.latchedProof)) {
        clearTicketRegisterOverlay();
        return null;
      }
      ticketRegisterOverlay.hidden = false;
      ticketRegisterOverlay.dataset.registrationState = 'registering';
      ticketRegisterOverlay.setAttribute('aria-busy', 'true');
      ticketLocalRegisterSlider.disabled = true;
      ticketLocalRegisterSlider.value = '100';
      ticketLocalRegisterSlider.setAttribute('aria-label', 'Biļetes reģistrācija notiek tālrunī');
      return ticketLocalRegisterSliderState.latchedProof;
    }
    const region = currentTicketSliderRegion(state);
    if (ticketLocalRegisterSliderState.session &&
      !ticketRegisterSliderProofStillMatches(ticketLocalRegisterSliderState.session.snapshot, state)) {
      cancelTicketRegisterSliderSession('proof_changed');
    }
    if (!region || !registerReady || busy || controlBusy || !configured) {
      if (ticketLocalRegisterSliderState.session) {
        cancelTicketRegisterSliderSession('slider_became_unavailable');
      }
      clearTicketRegisterOverlay();
      return null;
    }
    if (!setTicketRegisterOverlayLayout(region)) {
      clearTicketRegisterOverlay();
      return null;
    }
    ticketRegisterOverlay.hidden = false;
    ticketRegisterOverlay.dataset.registrationState = 'ready';
    ticketRegisterOverlay.removeAttribute('aria-busy');
    ticketLocalRegisterSlider.setAttribute('aria-label', 'Velc pa labi vismaz 8 pikseļus mazāk nekā 45 grādu leņķī, lai reģistrētu atvērto biļeti; velc uz augšu vai leju, lai ritinātu lapu');
    ticketLocalRegisterSlider.disabled = Boolean(ticketLocalRegisterSliderState.inFlight);
    const expiresAt = Date.parse(String(region.expiresAt || ''));
    const delay = expiresAt - (Date.now() + serverClockSkewMs);
    if (Number.isFinite(delay) && delay > 0) {
      const cooldownDelay = ticketCurrentProofLastRequestAt + ticketCurrentProofRequestCooldownMs - Date.now();
      const renewalDelay = Math.max(0, delay - ticketCurrentProofRenewBeforeMs, cooldownDelay);
      ticketSliderRegionExpiryTimer = setTimeout(() => {
        ticketSliderRegionExpiryTimer = null;
        ticketCurrentProofVisualState.resumePending = true;
        renderTicketActionV3Controls(currentState);
        maybeRequestTicketCurrentProof('slider_region_renewal');
      }, Math.max(25, Math.min(renewalDelay + 25, 60_000)));
    }
    return region;
  }

  function ticketActionV3LocalRequestIsBusy() {
    return ticketActionV3LocalRequestBusy(ticketActionV3LocalRequestState);
  }

  function sampleTicketCurrentProofFingerprint() {
    if (!ticketCurrentProofFingerprintContext || !hasRenderedFrame || !canvas.width || !canvas.height) return null;
    try {
      ticketCurrentProofFingerprintContext.drawImage(
        canvas,
        0,
        0,
        ticketCurrentProofFingerprintCanvas.width,
        ticketCurrentProofFingerprintCanvas.height
      );
      const pixels = ticketCurrentProofFingerprintContext.getImageData(
        0,
        0,
        ticketCurrentProofFingerprintCanvas.width,
        ticketCurrentProofFingerprintCanvas.height
      ).data;
      const values = [];
      for (let index = 0; index < pixels.length; index += 4) {
        const cell = index / 4;
        const column = cell % ticketCurrentProofFingerprintCanvas.width;
        const row = Math.floor(cell / ticketCurrentProofFingerprintCanvas.width);
        // Ignore the central upper ticket-code area. The ordinary rotating
        // Aztec animation is not a meaningful Ticket view change and must not
        // cause background re-detection or make controls flicker.
        if (row >= 1 && row <= 5 && column >= 2 && column <= 5) continue;
        values.push(Math.round(pixels[index] * 0.299 + pixels[index + 1] * 0.587 + pixels[index + 2] * 0.114));
      }
      return {
        epoch: Number(lastRenderedFrameEpoch || currentStreamEpoch || 0),
        sequence: Number(lastRenderedFrameSequence || 0),
        values
      };
    } catch (error) {
      clientLog('ticket_current_proof_sample_failed', error && error.message || 'sample failed');
      return null;
    }
  }

  function observeTicketCurrentProofFrame() {
    if (document.visibilityState !== 'visible' || !streamHasFreshRenderedFrame()) return;
    const now = Date.now();
    const epoch = Number(lastRenderedFrameEpoch || currentStreamEpoch || 0);
    if (!(epoch > 0)) return;
    if (ticketCurrentProofVisualState.fingerprint && ticketCurrentProofVisualState.fingerprint.epoch !== epoch) {
      ticketCurrentProofVisualState.fingerprint = null;
      ticketCurrentProofVisualState.candidateFingerprint = null;
      ticketCurrentProofVisualState.stableChangeCount = 0;
      ticketCurrentProofVisualState.changePending = false;
    }
    if (now - ticketCurrentProofLastSampleAt >= ticketCurrentProofSampleIntervalMs) {
      ticketCurrentProofLastSampleAt = now;
      const sample = sampleTicketCurrentProofFingerprint();
      if (sample) {
        if (!ticketCurrentProofVisualState.fingerprint) {
          ticketCurrentProofVisualState.fingerprint = sample;
        } else if (ticketCurrentProofFingerprintChanged(ticketCurrentProofVisualState.fingerprint.values, sample.values)) {
          if (ticketCurrentProofVisualState.candidateFingerprint &&
            !ticketCurrentProofFingerprintChanged(ticketCurrentProofVisualState.candidateFingerprint.values, sample.values)) {
            ticketCurrentProofVisualState.stableChangeCount += 1;
          } else {
            ticketCurrentProofVisualState.candidateFingerprint = sample;
            ticketCurrentProofVisualState.stableChangeCount = 1;
          }
          if (ticketCurrentProofVisualState.stableChangeCount >= 2) {
            ticketCurrentProofVisualState.fingerprint = sample;
            ticketCurrentProofVisualState.candidateFingerprint = null;
            ticketCurrentProofVisualState.changePending = true;
            ticketSliderVisualRevision += 1;
            cancelTicketRegisterSliderSession('visual_view_changed');
          }
        } else {
          ticketCurrentProofVisualState.candidateFingerprint = null;
          ticketCurrentProofVisualState.stableChangeCount = 0;
        }
      }
    }
    maybeRequestTicketCurrentProof('frame_observed');
  }

  async function maybeRequestTicketCurrentProof(reason) {
    if (!spacetimeStateFresh) return false;
    const stream = ticketActionV3StreamSnapshot();
    const proofScope = `${String(cfg.backendId || 'pixel')}:${Number(stream.epoch || 0)}`;
    const currentAction = currentState && currentState.ticketAction || null;
    const currentActionId = String(currentAction && currentAction.actionId || '').trim();
    const rebaseCandidate = String(currentAction && currentAction.phase || '') === 'complete' && currentActionId &&
      currentActionId !== String(ticketCurrentProofVisualState.rebasedActionId || '').trim() &&
      ticketSliderRegionV3ForAction(
        currentAction,
        currentState && currentState.ticketSliderRegion || null,
        stream,
        Date.now() + serverClockSkewMs
      );
    const rebased = Boolean(rebaseCandidate) && rebaseTicketCurrentProofDetectorFromAction(
      ticketCurrentProofVisualState,
      {
        action: currentAction,
        region: rebaseCandidate,
        stream,
        sample: sampleTicketCurrentProofFingerprint(),
        now: Date.now() + serverClockSkewMs
      }
    );
    if (rebased) ticketCurrentProofRequestedScope = proofScope;
    if (ticketCurrentProofInFlight ||
      Date.now() - ticketCurrentProofLastRequestAt < ticketCurrentProofRequestCooldownMs) return false;
    const ownUnknownAwaitingChange = Boolean(ticketCurrentProofLastActionId &&
      String(currentAction && currentAction.actionId || '') === ticketCurrentProofLastActionId &&
      String(currentAction && currentAction.target || '') === 'prove_current' &&
      ['succeeded', 'failed', 'needs_attention'].includes(String(currentAction && currentAction.status || '')) &&
      String(currentAction && currentAction.currentView || '') === 'unknown');
    if (!ticketCurrentProofRequestNeeded({
      visible: document.visibilityState === 'visible',
      stream,
      action: currentAction,
      region: currentState && currentState.ticketSliderRegion || null,
      now: Date.now() + serverClockSkewMs,
      requestedEpoch: ticketCurrentProofRequestedScope === proofScope ? Number(stream.epoch || 0) : 0,
      stableChangeCount: ticketCurrentProofVisualState.changePending ? 2 : ticketCurrentProofVisualState.stableChangeCount,
      resumed: ticketCurrentProofVisualState.resumePending,
      renewBeforeMs: ticketCurrentProofRenewBeforeMs,
      // A failed or completed control-code request can leave a visual cleanup
      // checkpoint on the phone. Only the explicit Open action is allowed to
      // recover it; an automatic proof must not reclaim the phone lane first.
      recoveryRequired: controlCodeVisualRecoveryRequired(),
      // A reconnect caused by this exact failed proof is not itself a visual
      // change. Wait for two agreeing changed frames before trying again.
      unknownAwaitingChange: ownUnknownAwaitingChange
    })) return false;
    ticketCurrentProofInFlight = true;
    ticketCurrentProofLastRequestAt = Date.now();
    const actionId = ticketActionV3Id();
    ticketCurrentProofLastActionId = actionId;
    try {
      const accepted = await requestTicketActionV3(
        'prove_current',
        'browser_auto_proof',
        'ticket_action_requested',
        '',
        { quiet: true, actionId }
      );
      if (accepted) {
        ticketCurrentProofRequestedScope = proofScope;
        ticketCurrentProofVisualState.resumePending = false;
        ticketCurrentProofVisualState.stableChangeCount = 0;
        ticketCurrentProofVisualState.candidateFingerprint = null;
        ticketCurrentProofVisualState.changePending = false;
        clientLog('ticket_current_proof_requested', reason || 'automatic');
      }
      if (!accepted && ticketCurrentProofLastActionId === actionId) {
        ticketCurrentProofLastActionId = '';
      }
      return accepted;
    } finally {
      ticketCurrentProofInFlight = false;
    }
  }

  function scheduleTicketActionV3Reconcile(reason) {
    if (ticketActionV3ReconcileTimer || !ticketActionV3LocalRequestIsBusy() ||
      ticketActionV3LocalRequestState.reducerSettled !== true) return;
    ticketActionV3ReconcileTimer = setTimeout(async () => {
      ticketActionV3ReconcileTimer = null;
      if (!ticketActionV3LocalRequestIsBusy()) return;
      try {
        await refreshSpacetimeState(reason || 'ticket_action_v3_reconcile');
      } catch (error) {
        clientLog('ticket_action_v3_refresh_failed', error && error.message || 'refresh failed');
      }
      if (ticketActionV3LocalRequestIsBusy()) {
        scheduleTicketActionV3Reconcile(reason);
      }
    }, ticketActionV3ReconcileIntervalMs);
  }

  function renderTicketActionV3Controls(state = currentState) {
    const action = state && state.ticketAction || null;
    const sliderActionRows = Array.isArray(state && state.ticketActions) ? [...state.ticketActions] : [];
    if (action && !sliderActionRows.some((row) => String(row && row.actionId || '') === String(action.actionId || ''))) {
      sliderActionRows.push(action);
    }
    const completedSliderAction = releaseTicketLocalRegisterSliderOnTerminal(
      ticketLocalRegisterSliderState,
      sliderActionRows
    );
    if (completedSliderAction) {
      ticketLocalRegisterSlider.value = '0';
      ticketLocalRegisterSlider.disabled = true;
      ticketRegisterOverlay.removeAttribute('aria-busy');
      clientLog('ticket_slider_terminal', String(completedSliderAction.status || 'terminal'));
    }
    const busy = ticketActionV3LocalRequestIsBusy() || ticketActionV3Busy(action);
    const backgroundProofBusy = Boolean(!ticketActionV3LocalRequestIsBusy() && ticketActionV3Busy(action) &&
      String(action && action.target || '') === 'prove_current');
    const blockingBusy = busy && !backgroundProofBusy;
    const observedUserAction = ticketActionV3ExplicitResultForDisplay(
      state && state.ticketActions || [],
      ticketActionV3LastUserActionId,
      ticketActionV3LastUserAction
    );
    if (observedUserAction && observedUserAction !== ticketActionV3LastUserAction) {
      ticketActionV3LastUserAction = observedUserAction;
      ticketActionV3LastUserMessage = '';
    }
    const statusAction = ticketActionV3LastUserAction || action;
    const statusBusy = ticketActionV3LastUserAction || ticketActionV3LastUserMessage
      ? ticketActionV3Busy(ticketActionV3LastUserAction)
      : blockingBusy;
    const controlBusy = controlCodeRequestOccupiesQueue();
    const region = currentTicketSliderRegion(state);
    const proofReady = spacetimeStateFresh && ticketActionV3RegistrationProofIsFresh(action);
    const proveCurrentReady = String(action && action.target || '') !== 'prove_current' || Boolean(region);
    const registerReady = proofReady && proveCurrentReady && !activationPolicyBlocked(state);
    const hdrControlReady = clientHDRConsequentialControlProofReady();
    const connectionReason = 'Gaida dzīvu SpaceTime savienojumu.';
    const hdrControlReason = 'Sagaidi svaigu tiešraides kadru, pirms vadi tālruni.';
    const phoneBusyReason = backgroundProofBusy
      ? 'Tālrunis pabeidz pašreizējā skata vizuālo pārbaudi.'
      : 'Tālrunis izpilda iepriekšējo biļetes darbību.';
    const controlBusyReason = 'Tālrunis izpilda kontroles koda darbību.';
    function setTicketButtonGate(button, enabled, reason) {
      button.disabled = !enabled;
      const detail = enabled ? '' : String(reason || 'Darbība pašlaik nav pieejama.');
      button.dataset.disabledReason = detail;
      if (detail) button.title = detail;
      else button.removeAttribute('title');
    }
    const openReason = !spacetimeStateFresh
      ? connectionReason
      : (!hdrControlReady ? hdrControlReason : (controlBusy ? controlBusyReason : (blockingBusy ? phoneBusyReason : '')));
    const activationReason = openReason || (activationPolicyBlocked(state) ? activationPolicyMessage(state) : '');
    const registerReason = !spacetimeStateFresh
      ? connectionReason
      : (!hdrControlReady
        ? hdrControlReason
        : (controlBusy
        ? controlBusyReason
        : (blockingBusy
          ? phoneBusyReason
          : (activationPolicyBlocked(state)
            ? activationPolicyMessage(state)
            : (proofReady && !region
              ? 'Atvērtā biļete ir apstiprināta; atjauno reģistrācijas slīdņa novietojumu.'
              : 'Vispirms vizuāli apstiprini atvērtu nereģistrētu biļeti.')))));
    setTicketButtonGate(requestTicketResetButton,
      spacetimeStateFresh && hdrControlReady && !blockingBusy && !controlBusy, openReason);
    setTicketButtonGate(requestTicketResetAndActivateButton,
      spacetimeStateFresh && hdrControlReady && !blockingBusy && !controlBusy && !activationPolicyBlocked(state), activationReason);
    setTicketButtonGate(activateTicketButton,
      hdrControlReady && !blockingBusy && !controlBusy && registerReady, registerReason);
    renderTicketRegisterOverlay(state, blockingBusy, controlBusy,
      hdrControlReady && registerReady && Boolean(region));
    for (const button of [requestTicketResetButton, requestTicketResetAndActivateButton, activateTicketButton]) {
      button.setAttribute('aria-busy', blockingBusy ? 'true' : 'false');
    }
    const switchAction = ticketActionV3SmartSwitchAction(
      sliderActionRows,
      Date.now() + serverClockSkewMs
    );
    const switchCurrentView = String(switchAction && switchAction.currentView || 'unknown');
    const switchExpiresAt = Date.parse(String(switchAction && switchAction.switchExpiresAt || ''));
    const switchAvailable = Boolean(switchAction);
    ticketViewSwitchButton.dataset.target = '';
    ticketViewSwitchButton.disabled = true;
    ticketViewSwitchButton.setAttribute('aria-busy', blockingBusy ? 'true' : 'false');
    if (ticketViewSwitchExpiryTimer) {
      clearTimeout(ticketViewSwitchExpiryTimer);
      ticketViewSwitchExpiryTimer = null;
    }
    if (Number.isFinite(switchExpiresAt) && switchExpiresAt > Date.now() + serverClockSkewMs) {
      ticketViewSwitchExpiryTimer = setTimeout(() => {
        ticketViewSwitchExpiryTimer = null;
        renderTicketActionV3Controls(currentState);
      }, Math.min(switchExpiresAt - (Date.now() + serverClockSkewMs) + 25, 60_000));
    }
    const smartSwitch = ticketActionV3SmartSwitchForView(switchCurrentView);
    ticketViewSwitchButton.textContent = smartSwitch.label;
    ticketViewSwitchButton.dataset.target = smartSwitch.target;
    if (switchAvailable && ticketViewSwitchButton.dataset.target && hdrControlReady && !blockingBusy && !controlBusy) {
      ticketViewSwitchButton.disabled = false;
      ticketViewSwitchButton.removeAttribute('title');
      ticketViewSwitchDetail.textContent = 'Var pārslēgt skatu bez biļetes atkārtotas reģistrēšanas.';
    } else if (!hdrControlReady) {
      ticketViewSwitchDetail.textContent = hdrControlReason;
    } else if (blockingBusy) {
      ticketViewSwitchDetail.textContent = phoneBusyReason;
    } else if (controlBusy) {
      ticketViewSwitchDetail.textContent = controlBusyReason;
    } else if (Number.isFinite(switchExpiresAt) && switchExpiresAt <= Date.now() + serverClockSkewMs) {
      ticketViewSwitchDetail.textContent = 'Skata pārslēgšanas laiks ir beidzies.';
    } else if (Number.isFinite(switchExpiresAt) && switchExpiresAt > Date.now() + serverClockSkewMs) {
      ticketViewSwitchDetail.textContent = 'Atver un vizuāli apstiprini jaunāko nereģistrēto biļeti, lai pārslēgtu skatu.';
    } else {
      ticketViewSwitchDetail.textContent = 'Nav nesen reģistrētas biļetes, uz kuru pārslēgties.';
    }
    if (ticketViewSwitchButton.disabled) ticketViewSwitchButton.title = ticketViewSwitchDetail.textContent;
    const statusTarget = String(statusAction && statusAction.target || '');
    const statusView = String(statusAction && statusAction.currentView || 'unknown');
    const activationTerminalMessage = ticketActionV3ActivationTerminalMessage(statusAction);
    if (ticketActionV3LastUserMessage) {
      ticketResetDetail.textContent = ticketActionV3LastUserMessage;
    } else if (statusAction && statusAction.status === 'succeeded' && statusTarget === 'redetect_latest') {
      ticketResetDetail.textContent = 'Jaunākā biļete ir veiksmīgi atkārtoti noteikta.';
    } else if (ticketActionV3IsExpectedEmptyRedetect(statusAction)) {
      ticketResetDetail.textContent = 'Biļetes nav atrastas.';
    } else if (statusAction && statusAction.status === 'succeeded' && statusView === 'latest_unactivated') {
      ticketResetDetail.textContent = 'Atvērtā nereģistrētā biļete ir vizuāli apstiprināta.';
    } else if (statusAction && statusAction.status === 'succeeded' && statusView === 'activated_current') {
      ticketResetDetail.textContent = 'Atvērtā biļete ir veiksmīgi reģistrēta un vizuāli apstiprināta.';
    } else if (statusAction && statusAction.status === 'succeeded' && statusView === 'recent_activated') {
      ticketResetDetail.textContent = 'Nesen aktivizētā biļete ir vizuāli apstiprināta.';
    } else if (statusAction && statusAction.status === 'succeeded') {
      ticketResetDetail.textContent = 'Biļetes darbība ir veiksmīgi pabeigta.';
    } else if (statusAction && statusAction.status === 'queued') {
      ticketResetDetail.textContent = 'Darbība gaida vienīgajā tālruņa rindas vietā…';
    } else if (statusBusy) {
      ticketResetDetail.textContent = statusTarget === 'register_current'
        ? 'Tālrunis sagatavo tieši šo biļeti…'
        : 'Tālrunis izpilda biļetes darbību…';
    } else if (backgroundProofBusy) {
      ticketResetDetail.textContent = 'Pašreizējais skats tiek pārbaudīts fonā; atvēršanas darbības ir pieejamas.';
    } else if (activationTerminalMessage) {
      ticketResetDetail.textContent = activationTerminalMessage;
    } else if (statusAction && statusAction.status === 'needs_attention') {
      ticketResetDetail.textContent = 'Tālruņa skats jāpārbauda. Darbība netika atkārtota.';
    } else if (statusAction && statusAction.status === 'failed') {
      ticketResetDetail.textContent = 'Biļetes darbība droši apstājās bez nepierādītas darbības.';
    } else if (!statusAction) {
      ticketResetDetail.textContent = 'Pieejams, kad tālrunis ir gatavs.';
    }
    updateControlCodeSubmitAvailability();
    maybeRequestTicketCurrentProof('state_rendered');
  }

  async function requestTicketActionV3(target, source, reason, expectedInteractionRevision = '', options = {}) {
    if (target !== 'prove_current' && !revealAuthoritativeSDRForConsequentialControl()) {
      if (options.quiet !== true) {
        ticketActionV3LastUserMessage = 'Sagaidi svaigu tiešraides kadru, pirms vadi tālruni.';
        renderTicketActionV3Controls(currentState);
      }
      return false;
    }
    const currentAction = currentState && currentState.ticketAction;
    const backgroundProofBusy = ticketActionV3Busy(currentAction) &&
      String(currentAction && currentAction.target || '') === 'prove_current';
    if (ticketActionV3LocalRequestIsBusy() || (ticketActionV3Busy(currentAction) && !backgroundProofBusy)) return false;
    const actionId = String(options.actionId || ticketActionV3Id()).trim();
    if (!actionId) return false;
    const activation = target === 'open_latest_and_register' || target === 'register_current';
    if (!beginTicketActionV3LocalRequest(ticketActionV3LocalRequestState, actionId)) return false;
    if (options.quiet !== true) {
      ticketActionV3LastUserActionId = actionId;
      ticketActionV3LastUserAction = {
        actionId,
        target,
        status: 'pending',
        currentView: 'unknown'
      };
      ticketActionV3LastUserMessage = '';
    }
    renderTicketActionV3Controls(currentState);
    try {
      await runSpacetimeMutation((client) => client.requestTicketActionV3(ticketActionV3RequestArgs({
        actionId,
        target,
        source,
        reason,
        attemptId: activation ? actionId : '',
        expectedInteractionRevision: target === 'register_current' ? expectedInteractionRevision : ''
      })), `ticket_action_v3_${target}`);
      settleTicketActionV3LocalRequest(ticketActionV3LocalRequestState, true);
      scheduleTicketActionV3Reconcile(`ticket_action_v3_${target}_reconcile`);
      clientLog('ticket_action_v3_requested', target);
      return true;
    } catch (error) {
      settleTicketActionV3LocalRequest(ticketActionV3LocalRequestState, false);
      if (options.quiet !== true) {
        ticketActionV3LastUserAction = {
          actionId,
          target,
          status: 'failed',
          currentView: 'unknown'
        };
        ticketActionV3LastUserMessage = localizePublicMessage(error && error.message || 'Biļetes darbību neizdevās nosūtīt.');
      }
      clientLog('ticket_action_v3_failed', error && error.message || target);
      return false;
    } finally {
      renderTicketActionV3Controls(currentState);
    }
  }

  async function registerCurrentTicket(source, options = {}) {
    const proofAction = currentState && currentState.ticketAction || null;
    if (!ticketActionV3RegistrationProofIsFresh(proofAction) ||
      (String(proofAction && proofAction.target || '') === 'prove_current' && !currentTicketSliderRegion(currentState))) {
      ticketActionV3LastUserActionId = '';
      ticketActionV3LastUserAction = null;
      ticketActionV3LastUserMessage = 'Sagaidi svaigu vizuālu nereģistrētās biļetes apstiprinājumu.';
      renderTicketActionV3Controls(currentState);
      return false;
    }
    if (source === 'browser_slider' && !ticketRegisterSliderProofStillMatches(options.proofSnapshot, currentState)) {
      ticketActionV3LastUserActionId = '';
      ticketActionV3LastUserAction = null;
      ticketActionV3LastUserMessage = 'Biļetes attēls mainījās vilkšanas laikā. Velc vēlreiz pēc svaiga apstiprinājuma.';
      renderTicketActionV3Controls(currentState);
      return false;
    }
    const revision = String(proofAction.actionId || '').trim();
    if (!revision) return false;
    return requestTicketActionV3(
      'register_current',
      source,
      source === 'browser_slider' ? 'ticket_slider_completed' : 'ticket_register_button',
      revision,
      source === 'browser_slider' ? { actionId: options.actionId } : {}
    );
  }

  function selectServerClockSample(state) {
    const liveServerTime = Date.parse(String(state && state.serverTime || ''));
    if (Number.isFinite(liveServerTime)) {
      return { timestamp: liveServerTime, source: 'live' };
    }
    if (serverClockHasLiveSample) return null;
    const memberLimitServerAt = Date.parse(String(state && state.memberLimits && state.memberLimits.serverAt || ''));
    if (Number.isFinite(memberLimitServerAt)) {
      return { timestamp: memberLimitServerAt, source: 'member_limits' };
    }
    const eligibilityServerAt = Date.parse(String(
      state && state.activationEligibility && state.activationEligibility.serverAt || ''
    ));
    if (Number.isFinite(eligibilityServerAt)) {
      return { timestamp: eligibilityServerAt, source: 'eligibility' };
    }
    return null;
  }

  function rememberServerClock(state) {
    const sample = selectServerClockSample(state);
    if (!sample) return;
    serverClockSkewMs = sample.timestamp - Date.now();
    if (sample.source === 'live') {
      serverClockHasLiveSample = true;
    }
  }

  function activeViewers(viewers) {
    return (viewers || []).filter((viewer) => viewer && viewer.connected !== false);
  }

  function activeViewerPresence(state) {
    const publicPresence = Array.isArray(state && state.viewerPresence) ? state.viewerPresence : [];
    if (publicPresence.length) {
      return publicPresence.map((viewer, index) => ({
        publicId: String(viewer && viewer.publicId || ''),
        label: String(viewer && (viewer.publicId || viewer.label) || `Skatītājs ${index + 1}`)
      }));
    }
    return activeViewers(state && state.viewers || []).map((_viewer, index) => ({
      label: `Skatītājs ${index + 1}`
    }));
  }

  function renderViewerSummary(viewers, visibleViewerCount) {
    const count = Number.isFinite(Number(visibleViewerCount)) ? Number(visibleViewerCount) : activeViewers(viewers).length;
    if (viewerCount) viewerCount.textContent = String(count);
    if (viewerCountDetail) viewerCountDetail.textContent = count === 1 ? 'cilvēks lapā' : 'cilvēki lapā';
  }

  function renderPresence(viewers, visibleViewerCount) {
    const active = activeViewers(viewers);
    const countValue = Number.isFinite(Number(visibleViewerCount)) ? Number(visibleViewerCount) : active.length;
    presenceState.visibleViewerCount = countValue;
    const nextViewers = active.map((viewer, index) => ({
      key: `${viewer.publicId || viewer.label || 'viewer'}-${index}`,
      label: viewer.label || `Skatītājs ${index + 1}`,
      mark: 'skatās'
    }));
    if (!nextViewers.length && countValue > 0) {
      nextViewers.push({
        key: 'viewer-identifiers-pending',
        label: 'Identifikatori atjaunojas',
        mark: 'gaida'
      });
    }
    presenceState.hasVisibleRows = nextViewers.length > 0;
    // Arrow 1.0.6 can race its deferred keyed-list cleanup when a live list
    // briefly becomes empty and is repopulated during a reconnect. Keep one
    // invisible keyed row mounted so the list always has a stable overlap.
    nextViewers.push({
      key: presenceListAnchorKey,
      label: '',
      mark: '',
      hidden: true
    });
    presenceState.viewers = nextViewers;
    if (presenceMounted) return;
    presence.textContent = '';
    document.documentElement.dataset.ticketUi = "arrow";
    html`
      <div class="presence-header">
        <span>Skatītāji</span>
        <strong>${() => `${presenceState.visibleViewerCount} lapā`}</strong>
      </div>
      <div class="presence-list" hidden="${() => !presenceState.hasVisibleRows}">
        ${() => presenceState.viewers.map((viewer) => html`
          <div class="presence-item" hidden="${viewer.hidden === true}">
            <span class="presence-email">${viewer.label}</span>
            <span class="presence-mark">${viewer.mark}</span>
          </div>
        `.key(viewer.key))}
      </div>
    `(presence);
    presenceMounted = true;
  }

  codeDigits.addEventListener('input', () => {
    const cleaned = sanitizeControlDigits(codeDigits.value);
    if (codeDigits.value !== cleaned) codeDigits.value = cleaned;
    codeError.textContent = '';
    updateControlCodeSubmitAvailability();
    updateViewportVars();
  });
  codeDigits.addEventListener('focus', updateViewportVars);
  codeDigits.addEventListener('blur', () => {
    setTimeout(() => {
      updateViewportVars();
      resizeCanvasBox();
    }, 80);
  });
  requestCodeButton.addEventListener('click', () => openControlCodeDialog());
  controlCodeHotspot.addEventListener('click', requestControlCodeFromHotspot);
  requestTicketResetButton.addEventListener('click', () => requestTicketActionV3(
    'open_latest_unactivated', 'browser_button', 'ticket_open_latest_unactivated'
  ));
  requestTicketResetAndActivateButton.addEventListener('click', () => requestTicketActionV3(
    'open_latest_and_register', 'browser_button', 'ticket_open_latest_and_register'
  ));
  activateTicketButton.addEventListener('click', () => registerCurrentTicket('browser_button'));
  async function submitCompletedTicketRegisterSlider(proofSnapshot) {
    const actionId = ticketActionV3Id();
    const submitted = await handleTicketLocalRegisterSliderChange({
      slider: ticketLocalRegisterSlider,
      state: ticketLocalRegisterSliderState,
      actionId,
      proofSnapshot,
      submitRegisterCurrent: (source, exactActionId, exactProof) => registerCurrentTicket(source, {
        actionId: exactActionId,
        proofSnapshot: exactProof
      }),
      render: () => renderTicketActionV3Controls(currentState)
    });
    if (!submitted && !ticketLocalRegisterSliderState.inFlight) ticketLocalRegisterSlider.value = '0';
    return submitted;
  }

  async function finishTicketRegisterSliderSession(event, kind) {
    const session = ticketLocalRegisterSliderState.session;
    if (!session || session.kind !== kind || ticketLocalRegisterSliderState.inFlight) return false;
    if (kind === 'pointer' && Number(event && event.pointerId) !== session.pointerId) return false;
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      if (event && typeof event.preventDefault === 'function') event.preventDefault();
      cancelTicketRegisterSliderSession('hdr_proof_not_fresh', event && event.pointerId);
      ticketLocalRegisterSlider.value = '0';
      return false;
    }
    const proofMatches = ticketRegisterSliderProofStillMatches(session.snapshot, currentState);
    const completedProof = completeTicketLocalRegisterSliderSession(ticketLocalRegisterSliderState, {
      pointerId: event && event.pointerId,
      pointerClientX: event && event.clientX,
      pointerClientY: event && event.clientY,
      progress: Number(ticketLocalRegisterSlider.value || 0),
      proofMatches
    });
    if (kind === 'pointer') suppressTicketRegisterSliderChangeForPointerEvent();
    if (!completedProof) {
      ticketLocalRegisterSlider.value = '0';
      if (!proofMatches) clientLog('ticket_slider_cancelled', 'proof_changed_before_completion');
      return false;
    }
    ticketLocalRegisterSlider.value = '100';
    return submitCompletedTicketRegisterSlider(completedProof);
  }

  ticketLocalRegisterSlider.addEventListener('pointerdown', (event) => {
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      event.preventDefault();
      cancelTicketRegisterSliderSession('hdr_proof_not_fresh');
      ticketLocalRegisterSlider.value = '0';
      return;
    }
    if (ticketLocalRegisterSliderState.inFlight) return;
    if (event.isPrimary === false) {
      cancelTicketRegisterSliderSession('secondary_pointer_down');
      return;
    }
    if (event.pointerType === 'mouse' && event.button !== 0) {
      cancelTicketRegisterSliderSession('non_primary_mouse_button');
      return;
    }
    const snapshot = currentTicketRegisterSliderProof(currentState);
    const sliderRect = ticketLocalRegisterSlider.getBoundingClientRect();
    if (!beginTicketLocalRegisterSliderSession(ticketLocalRegisterSliderState, {
      kind: 'pointer',
      pointerId: event.pointerId,
      pointerStartClientX: event.clientX,
      pointerStartClientY: event.clientY,
      pointerTrackLeftClientX: sliderRect.left,
      pointerTrackWidth: sliderRect.width,
      snapshot
    })) return;
    ticketLocalRegisterSliderState.ignoreChange = true;
  });
  ticketLocalRegisterSlider.addEventListener('pointermove', (event) => {
    if (updateTicketLocalRegisterSliderPointerDirection(ticketLocalRegisterSliderState, {
      pointerId: event.pointerId,
      pointerClientX: event.clientX,
      pointerClientY: event.clientY
    }) === 'scroll') {
      clientLog('ticket_slider_cancelled', 'page_scroll_direction');
    }
  }, { passive: true });
  ticketLocalRegisterSlider.addEventListener('input', () => {
    // The local range authorizes one durable action only after release. Never
    // turn progress events into a second phone-control protocol.
  });
  ticketLocalRegisterSlider.addEventListener('pointerup', (event) => {
    finishTicketRegisterSliderSession(event, 'pointer').catch((error) => {
      clientLog('ticket_slider_submit_failed', error && error.message || 'submit failed');
    });
  });
  ticketLocalRegisterSlider.addEventListener('pointercancel', (event) => {
    cancelTicketRegisterSliderSession('pointer_cancelled', event.pointerId);
  });
  ticketLocalRegisterSlider.addEventListener('keydown', (event) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown'].includes(event.key)) return;
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      event.preventDefault();
      cancelTicketRegisterSliderSession('hdr_proof_not_fresh');
      ticketLocalRegisterSlider.value = '0';
      return;
    }
    if (ticketLocalRegisterSliderState.inFlight || ticketLocalRegisterSliderState.session) return;
    beginTicketLocalRegisterSliderSession(ticketLocalRegisterSliderState, {
      kind: 'keyboard',
      snapshot: currentTicketRegisterSliderProof(currentState)
    });
  });
  ticketLocalRegisterSlider.addEventListener('keyup', (event) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End', 'PageUp', 'PageDown'].includes(event.key)) return;
    finishTicketRegisterSliderSession(event, 'keyboard').catch((error) => {
      clientLog('ticket_slider_submit_failed', error && error.message || 'submit failed');
    });
  });
  ticketLocalRegisterSlider.addEventListener('change', () => {
    if (!revealAuthoritativeSDRForConsequentialControl()) {
      cancelTicketRegisterSliderSession('hdr_proof_not_fresh');
      ticketLocalRegisterSlider.value = '0';
      return;
    }
    if (ticketLocalRegisterSliderState.ignoreChange || ticketLocalRegisterSliderState.inFlight ||
      ticketLocalRegisterSliderState.session ||
      Number(ticketLocalRegisterSlider.value || 0) < TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT
    ) return;
    const snapshot = currentTicketRegisterSliderProof(currentState);
    if (!beginTicketLocalRegisterSliderSession(ticketLocalRegisterSliderState, {
      kind: 'keyboard',
      snapshot
    })) {
      ticketLocalRegisterSlider.value = '0';
      ticketActionV3LastUserMessage = 'Slīdņa apstiprinājums vairs nav svaigs. Sagaidi atjaunotu slīdni un mēģini vēlreiz.';
      clientLog('ticket_slider_cancelled', 'change_session_unavailable');
      renderTicketActionV3Controls(currentState);
      return;
    }
    finishTicketRegisterSliderSession(null, 'keyboard').catch((error) => {
      clientLog('ticket_slider_submit_failed', error && error.message || 'submit failed');
    });
  });
  ticketLocalRegisterSlider.addEventListener('blur', () => cancelTicketRegisterSliderSession('slider_blurred'));
  window.addEventListener('blur', () => cancelTicketRegisterSliderSession('window_blurred'));
  ticketViewSwitchButton.addEventListener('click', () => {
    const target = String(ticketViewSwitchButton.dataset.target || '');
    if (target) requestTicketActionV3(target, 'browser_smart_switch', `ticket_${target}`);
  });
  codeDialogClose.addEventListener('click', closeControlCodeDialog);
  codeDialog.addEventListener('click', (event) => {
    if (event.target === codeDialog) closeControlCodeDialog();
  });
  codeForm.addEventListener('submit', (event) => {
    event.preventDefault();
    submitControlCodeRequest();
  });
  codeResultArea.addEventListener('click', (event) => {
    if (event.target === codeResultClose) return;
    event.preventDefault();
  });
  codeResultClose.addEventListener('click', (event) => {
    event.preventDefault();
    event.stopPropagation();
    closeCurrentControlCode(false);
  });
  codeResultClose.addEventListener('keydown', (event) => {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    event.stopPropagation();
    closeCurrentControlCode(false);
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && codeDialogOpen) closeControlCodeDialog();
  });
  startStreamButton.addEventListener('click', () => {
    if (idleDisconnected) {
      resumeFromIdleDisconnect('manual_start');
      return;
    }
    restartStream('manual_start');
  });

  for (const eventName of ['pointerdown', 'touchend', 'click', 'keydown', 'scroll', 'focus']) {
    const target = eventName === 'focus' ? window : document;
    target.addEventListener(eventName, (event) => noteViewerActivity(event, eventName), { capture: true, passive: true });
  }

  function freshStreamStatus(now) {
    if (!latestStreamStatus || lastStreamStatusAt <= 0) return null;
    if (now - lastStreamStatusAt > 3500) return null;
    return latestStreamStatus;
  }

  function backendLooksRecoverable(status) {
    if (!status || status.phoneDesired === false) return false;
    if (status.phoneConnected === false) return true;
    const streamState = String(status.phoneStreamState || '');
    return streamState !== '' && streamState !== 'streaming';
  }

  function viewerIsForeground() {
    return controlCodeKeepsVideoAliveWhileHidden() ||
      document.visibilityState === 'visible';
  }

  function serverFrameAge(status) {
    if (!status) return -1;
    const value = Number(status.lastFrameAgoMillis);
    return Number.isFinite(value) ? value : -1;
  }

  function streamStatusStale(status) {
    return Boolean(status && status.activeVideoClients > 0 && serverFrameAge(status) > streamStaleKeyframeMs);
  }

  function freshnessStateForVisualAge(ageMs) {
    if (!Number.isFinite(ageMs) || ageMs < 0) return 'STALE';
    if (ageMs <= streamLiveFreshMaxAgeMs) return 'LIVE_FRESH';
    if (ageMs <= streamLiveOkMaxAgeMs) return 'LIVE_OK';
    if (ageMs <= streamDegradedMaxAgeMs) return 'DEGRADED';
    return 'STALE';
  }

  function currentRenderedFreshness(now) {
    now = Number.isFinite(now) ? now : performance.now();
    const hasFrame = hasRenderedFrame && lastRenderedFrameRenderedAt > 0;
    const visualAgeMillis = hasFrame ? lastRenderedVisualAge(now) : -1;
    const browserReceiveToDecodeMillis = hasFrame && lastRenderedFrameReceivedAt > 0 && lastRenderedFrameQueuedAt > 0
      ? Math.max(0, lastRenderedFrameQueuedAt - lastRenderedFrameReceivedAt)
      : -1;
    const decodeToRenderMillis = hasFrame && lastRenderedFrameQueuedAt > 0
      ? Math.max(0, lastRenderedFrameRenderedAt - lastRenderedFrameQueuedAt)
      : -1;
    const decoderQueueDelayMillis = browserReceiveToDecodeMillis >= 0 && decodeToRenderMillis >= 0
      ? browserReceiveToDecodeMillis + decodeToRenderMillis
      : -1;
    const streamFreshnessState = hasFrame ? freshnessStateForVisualAge(visualAgeMillis) : 'STALE';
    const liveLabeled = streamFreshnessState === 'LIVE_FRESH'
      || streamFreshnessState === 'LIVE_OK'
      || streamFreshnessState === 'DEGRADED';
    return {
      hasFrame,
      visualAgeMillis,
      browserReceiveToDecodeMillis,
      decodeToRenderMillis,
      decoderQueueDelayMillis,
      streamFreshnessState,
      liveLabeled
    };
  }

  function lastRenderedVisualAge(now) {
    if (!hasRenderedFrame || lastRenderedFrameRenderedAt <= 0 || !Number.isFinite(lastRenderedFrameVisualAgeMillis)) {
      return -1;
    }
    now = Number.isFinite(now) ? now : performance.now();
    return Math.max(0, lastRenderedFrameVisualAgeMillis + (now - lastRenderedFrameRenderedAt));
  }

  function clearStreamLiveStaleGrace() {
    if (!streamLiveStaleGraceTimer) return;
    clearTimeout(streamLiveStaleGraceTimer);
    streamLiveStaleGraceTimer = null;
  }

  function streamLiveStaleGraceAllowed(freshness, reason) {
    if (reason !== 'stream_status' || !freshness || !freshness.hasFrame) return false;
    if (document.body.dataset.streamLive !== 'true') return false;
    if (idleDisconnected || streamUnsupported || !viewerIsForeground()) return false;
    if (!videoWs || videoWs.readyState !== WebSocket.OPEN) return false;
    const status = freshStreamStatus(performance.now());
    if (!status || status.phoneDesired === false || status.phoneConnected === false) return false;
    if (String(status.phoneStreamState || '') !== 'streaming') return false;
    if (Number(status.activeVideoClients || 0) <= 0) return false;
    return !streamStatusStale(status);
  }

  function streamPresentationLive(freshness, reason) {
    if (freshness.liveLabeled) {
      clearStreamLiveStaleGrace();
      return true;
    }
    if (!streamLiveStaleGraceAllowed(freshness, reason)) {
      clearStreamLiveStaleGrace();
      return false;
    }
    if (!streamLiveStaleGraceTimer) {
      streamLiveStaleGraceTimer = setTimeout(() => {
        streamLiveStaleGraceTimer = null;
        updateStreamFreshnessStatus('stream_stale_grace_expired');
      }, streamLiveStaleGraceMs);
    }
    return true;
  }

  function clientHDRSDRUnavailable(freshness, reason) {
    if (!freshness || !freshness.hasFrame) return true;
    if (idleDisconnected || streamUnsupported || !viewerIsForeground()) return true;
    if (!videoWs || videoWs.readyState !== WebSocket.OPEN) return true;
    const status = freshStreamStatus(performance.now());
    if (!status) return reason === 'stream_status';
    if (status.phoneDesired === false || status.phoneConnected === false) return true;
    if (String(status.phoneStreamState || '') !== 'streaming') return true;
    return Number(status.activeVideoClients || 0) <= 0;
  }

  function clientHDRStreamInterruptionCanHold(reason) {
    if (reason === 'stream_unsupported' || streamUnsupported || idleDisconnected ||
      !viewerIsForeground() || document.visibilityState !== 'visible') return false;
    const status = freshStreamStatus(performance.now());
    if (status && status.phoneDesired === false) return false;
    return true;
  }

  function reconcileClientHDRStreamContinuity(reason, fallbackReason) {
    const controller = experimentalClientHDRController;
    if (!controller || !experimentalMediaState.enabled ||
      experimentalMediaState.engine !== CLIENT_HDR_ENGINE) return false;
    if (clientHDRStreamInterruptionCanHold(reason) &&
      typeof controller.holdLastPresentation === 'function' &&
      controller.holdLastPresentation(fallbackReason)) {
      showExperimentalClientHDRHoldoverNotice();
      if (document.body) document.body.dataset.experimentalMedia = 'hdr-client-webgpu-holdover';
      setExperimentalMediaStatus('HDR pārlūkā — saglabāts spilgtais kadrs; gaida svaigu kadru…');
      return true;
    }
    controller.markSDRStale(fallbackReason);
    return false;
  }

  function updateStreamFreshnessStatus(reason) {
    const freshness = currentRenderedFreshness(performance.now());
    const presentationLive = streamPresentationLive(freshness, reason);
    const hdrFallbackReason = clientHDRSDRUnavailable(freshness, reason)
      ? 'sdr_stream_unavailable'
      : (!presentationLive ? 'sdr_stream_not_live' : '');
    if (hdrFallbackReason) reconcileClientHDRStreamContinuity(reason, hdrFallbackReason);
    document.body.dataset.streamFreshness = freshness.streamFreshnessState;
	    document.body.dataset.streamLive = presentationLive ? 'true' : 'false';
	    if (!freshness.liveLabeled && (reason || hasRenderedFrame)) {
	      showStreamResumeSpinner();
	    } else if (freshness.liveLabeled) {
	      hideStreamResumeSpinner();
	      if (activeResumeFlow && !activeResumeFlow.done) finishActivationResumeFlow('fresh_frame');
	    }
    updateControlCodeSubmitAvailability();
    return freshness;
  }

  function controlCodeFastStateExpiryMillis(state) {
    const expiresAt = Date.parse(state && state.expiresAt || '');
    return Number.isFinite(expiresAt) ? expiresAt : 0;
  }

  function controlCodeFastStateFresh(state) {
    state = state || controlCodeFastState;
    if (!state || String(state.status || '') !== 'fast_ready') return false;
    if (!String(state.revision || '').trim()) return false;
    if (state.rawTicketConfirmed !== true || state.cleanupClear !== true || state.streamLive !== true) return false;
    const expiresAt = controlCodeFastStateExpiryMillis(state);
    return Boolean(expiresAt && expiresAt > Date.now() + serverClockSkewMs + 750);
  }

  function controlCodeFastRevisionForRequest() {
    const state = controlCodeFastState || {};
    const revision = String(state.revision || '').trim();
    return revision && controlCodeFastStateFresh(state) ? revision : '';
  }

  function renderControlCodeFastStateDataset() {
    document.body.dataset.controlCodeFastState = String(controlCodeFastState && controlCodeFastState.status || 'missing');
    document.body.dataset.controlCodeFastReady = controlCodeFastStateFresh() ? 'true' : 'false';
    if (controlCodeCleanupPendingRequestID && controlCodeFastStateFresh()) {
      controlCodeCleanupPendingRequestID = '';
    }
  }

  function scheduleControlCodeFastStateExpiryCheck() {
    if (controlCodeFastStateExpiryTimer) {
      clearTimeout(controlCodeFastStateExpiryTimer);
      controlCodeFastStateExpiryTimer = null;
    }
    const expiresAt = controlCodeFastStateExpiryMillis(controlCodeFastState);
    if (!expiresAt) return;
    const refreshAt = expiresAt - serverClockSkewMs - 750 + 25;
    const delayMs = Math.max(0, refreshAt - Date.now());
    controlCodeFastStateExpiryTimer = setTimeout(() => {
      controlCodeFastStateExpiryTimer = null;
      renderControlCodeFastStateDataset();
      updateControlCodeSubmitAvailability();
    }, Math.min(delayMs, 60_000));
  }

  function controlCodeRequestOccupiesPhone(request) {
    if (!request) return false;
    const expiresAt = controlCodeRequestExpiryTime(request);
    if (expiresAt && Date.now() + serverClockSkewMs > expiresAt + 1000) return false;
    const status = String(request.status || '');
    if (status === 'closed' || status === 'expired' || status === 'failed') return false;
    if (status === 'queued' || status === 'running') return true;
    if (status !== 'succeeded') return false;
    if (request.cleanupPending === true) return true;
    return request.captureRequired === true && request.captureAcknowledged !== true;
  }

  function controlCodeRequestOccupiesQueue() {
    if (controlCodeCleanupPendingRequestID) return true;
    if (controlCodeSubmitInFlight) return true;
    const requestsAvailable = Array.isArray(currentState && currentState.controlCodeRequests);
    const requests = requestsAvailable ? currentState.controlCodeRequests : [];
    const localRequestID = String(codeRequest && codeRequest.requestId || '').trim();
    const localRequestIsPresent = Boolean(!requestsAvailable || !localRequestID || requests.some((request) =>
      request && String(request.requestId || '').trim() === localRequestID &&
      request.status !== 'closed' && request.status !== 'expired'
    ));
    if (localRequestIsPresent && controlCodeRequestOccupiesPhone(codeRequest)) return true;
    return requests.some((request) =>
      isOwnedControlCodeRequest(request) &&
      controlCodeRequestIsStillRelevant(request) &&
      controlCodeRequestOccupiesPhone(request)
    );
  }

  function controlCodeMutationLaneBusy() {
    return controlCodeRequestOccupiesQueue() ||
      ticketInteractionIsBusy(currentState && currentState.ticketInteraction) ||
      ticketActionV3LocalRequestIsBusy() ||
      ticketActionV3Busy(currentState && currentState.ticketAction);
  }

  function updateControlCodeSubmitAvailability() {
    renderControlCodeFastStateDataset();
    // Reset/reselect and an active slider claim occupy the same phone mutation
    // lane as control-code generation.  Use the authoritative interaction row
    // here so the UI cannot offer a request that the reducer will reject.
    const busy = controlCodeMutationLaneBusy();
    const limitBlocked = memberLimitBlocked('control_code');
    const hdrControlReady = clientHDRConsequentialControlProofReady();
    const digitCount = sanitizeControlDigits(codeDigits.value).length;
    const digitsValid = digitCount >= 2 && digitCount <= 8;
    codeSubmit.disabled = !codeDialogOpen || busy || limitBlocked || !hdrControlReady || !digitsValid;
    codeSubmit.textContent = controlCodeSubmitInFlight ? 'Nosūta…' : 'Izveidot kodu';
    if (controlCodeSubmitInFlight) {
      codeSubmit.setAttribute('aria-busy', 'true');
    } else {
      codeSubmit.removeAttribute('aria-busy');
    }
    requestCodeButton.disabled = busy || limitBlocked || !hdrControlReady;
    const sliderOwnsHotspot = ticketRegisterOverlayOccupiesHotspot();
    const hotspotUnavailable = busy || limitBlocked || !hdrControlReady || sliderOwnsHotspot ||
      codeDialogOpen || !codeResultArea.hidden;
    controlCodeHotspot.disabled = hotspotUnavailable;
    controlCodeHotspot.setAttribute('aria-disabled', hotspotUnavailable ? 'true' : 'false');
  }

  function reconnectVideoForRecovery(reason) {
    const now = performance.now();
    if (now - lastRecoveryVideoReconnectAt < recoveryVideoReconnectDebounceMs) return false;
    lastRecoveryVideoReconnectAt = now;
    restartStream(reason, { preserveFrame: true });
    return true;
  }

  function decoderStartupGraceActive(now) {
    if (hasRenderedFrame || configuredAt <= 0) return false;
    if (now - configuredAt > streamDecoderStartupGraceMs) return false;
    return decoderMode === 'avc' || avcAdapterTried || lastPacketAt > 0;
  }

  function streamRecoveryDetail(values) {
    const detail = {};
    for (const [key, value] of Object.entries(values || {})) {
      if (typeof value === 'number') {
        detail[key] = Math.round(value);
      } else {
        detail[key] = value;
      }
    }
    return detail;
  }

  function chaseLiveStream() {
    if (idleDisconnected) return;
    if (streamUnsupported) return;
    if (!viewerIsForeground()) {
      if (document.visibilityState === 'hidden') pauseHiddenStreamAfterGrace('chase_live_stream_hidden');
      return;
    }
    const hadStream = configured || lastDecodedFrameAt > 0 || lastPacketAt > 0 || latestStreamStatus;
    if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {
      connectDirectVideo();
      if (hadStream) {
        requestServerRecoveryDebounced('foreground_video_socket_closed');
      }
      return;
    }
    if (videoWs.readyState !== WebSocket.OPEN) return;
    const now = performance.now();
    const status = freshStreamStatus(now);
    const serverAge = serverFrameAge(status);
    const serverStale = serverAge > streamStaleKeyframeMs && status && status.activeVideoClients > 0;
    const backendInactive = backendLooksRecoverable(status);

    if (!configured) {
      const pendingSince = videoConnectedAt || connectedAt;
      const pendingAge = pendingSince > 0 ? now - pendingSince : 0;
      if (pendingAge > streamFirstFrameKeyframeMs) {
        if (requestKeyframeDebounced('h264_first_frame_nudge', recoveryKeyframeDebounceMs)) {
          clientLog('loading_over_2s', 'h264_first_frame_pending');
        }
      }
      if (pendingAge > streamStaleDecoderResetMs && mediaSessionStuckOnPreservedFrame()) {
        recoverFreshMediaSession('h264_first_frame_pending', 'first_frame_pending', {
          flow: activeResumeFlow,
          watchdogs: false,
          keyframeReason: 'h264_first_frame_pending_keyframe',
          serverRecoveryReason: 'h264_first_frame_pending_recover',
          forceServerRecovery: true
        });
        return;
      }
      if (pendingAge > streamStaleServerRecoverMs || backendInactive) {
        requestFirstFrameServerRecovery('h264_start_pending', 'unconfigured');
      }
      return;
    }

    if (lastDecodedFrameAt === 0 && configuredAt > 0) {
      const firstFrameAge = now - configuredAt;
      if (firstFrameAge > streamFirstFrameKeyframeMs) {
        requestKeyframeDebounced('first_frame_timeout', recoveryKeyframeDebounceMs);
      }
      if (decoderStartupGraceActive(now)) {
        return;
      }
      if (firstFrameAge > streamStaleDecoderResetMs) {
        resetDecoderForRecovery('first_frame_decoder_reset');
      }
      if (firstFrameAge > streamStaleVideoReconnectMs) {
        reconnectVideoForRecovery('first_frame_video_reconnect');
      }
      if (firstFrameAge > streamStaleServerRecoverMs || backendInactive) {
        requestFirstFrameServerRecovery('first_frame_server_recover', 'configured');
      }
      return;
    }

    const decodedAge = lastDecodedFrameAt > 0 ? now - lastDecodedFrameAt : 0;
    const freshness = currentRenderedFreshness(now);
    const renderedVisualAge = freshness.hasFrame ? Number(freshness.visualAgeMillis || 0) : lastRenderedVisualAge(now);
    const sequenceStalledAge = lastPacketSequenceAdvancedAt > 0 ? now - lastPacketSequenceAdvancedAt : 0;
    const sequenceStalled = lastPacketAt > 0 && sequenceStalledAge > streamStaleKeyframeMs && decodedAge > streamStaleKeyframeMs;
    const localStaleAge = Math.max(decodedAge, renderedVisualAge, sequenceStalled ? sequenceStalledAge : 0);
    const serverStaleAge = serverStale ? serverAge : 0;
    const detail = streamRecoveryDetail({
      decodedAge,
      renderedVisualAge,
      serverAge,
      sequenceStalledAge,
      localStaleAge,
      visualAgeMillis: freshness.visualAgeMillis,
      browserReceiveToDecodeMillis: freshness.browserReceiveToDecodeMillis,
      decodeToRenderMillis: freshness.decodeToRenderMillis,
      decoderQueueDelayMillis: freshness.decoderQueueDelayMillis,
      streamFreshnessState: freshness.streamFreshnessState,
      liveLabeled: freshness.liveLabeled,
      activeVideoClients: status ? status.activeVideoClients : 0,
      backendInactive
    });
    if (localStaleAge <= streamStaleKeyframeMs) {
      if (serverStaleAge > streamStaleKeyframeMs) {
        if (requestKeyframeDebounced('server_stale_frames', recoveryKeyframeDebounceMs)) {
          sendVideoClientLog('server_stale_frames', detail);
        }
        if (serverStaleAge > streamStaleServerRecoverMs) {
          requestServerRecoveryDebounced('server_stale_frames');
        }
      }
      if (backendInactive) {
        requestServerRecoveryDebounced('backend_inactive');
      }
      return;
    }

    if (localStaleAge > streamStaleKeyframeMs) {
      if (requestKeyframeDebounced('stale_video_frames', recoveryKeyframeDebounceMs)) {
        sendVideoClientLog('stale_video_frames', detail);
      }
    }
    if (localStaleAge > streamStaleDecoderResetMs || (lastPacketAt > lastDecodedFrameAt && decodedAge > streamStaleDecoderResetMs)) {
      resetDecoderForRecovery('stale_decoder_recovery');
    }
    if (localStaleAge > streamStaleVideoReconnectMs) {
      reconnectVideoForRecovery('stale_video_frames');
    }
    if (Math.max(localStaleAge, serverStaleAge) > streamStaleServerRecoverMs || backendInactive) {
      requestServerRecoveryDebounced('stale_frames_server_recover');
    }
  }

	  function recoverAfterVisibilityResume(reason) {
	    if (idleDisconnected) {
	      resumeFromIdleDisconnect(reason || 'visibility_resume');
	      return;
	    }
	    const now = performance.now();
	    const hiddenMs = Math.max(lastHiddenAt > 0 ? now - lastHiddenAt : 0, lastHiddenWallAt > 0 ? Date.now() - lastHiddenWallAt : 0);
	    const longHidden = hiddenMs >= backgroundRecoveryHiddenMs;
	    const oldHiddenTab = hiddenMs >= oldTabFreshResumeHiddenMs;
	    const videoStale = configured && (lastFrameAt === 0 || now - lastFrameAt > streamStaleVideoReconnectMs);
	    const cacheRestored = reason === 'pageshow_persisted' || (typeof document !== 'undefined' && document.wasDiscarded === true);
	    const connectingTooLong = videoWs && videoWs.readyState === WebSocket.CONNECTING && videoSocketCreatedAt > 0 && now - videoSocketCreatedAt > resumeSoftReconnectMs;
	    const reusePersistedSocket = reason === 'pageshow_persisted'
	      && hiddenMs < hiddenVideoCloseDelayMs
	      && videoSocketKeepsStreamActive()
	      && !videoStale
	      && !connectingTooLong;
	    const hardRestore = longHidden || oldHiddenTab || connectingTooLong || (cacheRestored && !reusePersistedSocket);
	    const resumeFlow = claimActivationResumeLifecycle(reason || 'visibility_resume', 'visibility_resume');
    if (!resumeFlow) return;
	    logResumeCheckpoint('activation_resume_recovery_decision', {
	      reason: safeResumeLabel(reason, 'visibility_resume'),
	      hidden: hiddenDurationBucket(hiddenMs),
	      cache: resumeBooleanLabel(cacheRestored),
	      stale: resumeBooleanLabel(videoStale),
	      connecting: resumeBooleanLabel(connectingTooLong),
	      action: hardRestore ? 'cached_restore' : (reusePersistedSocket ? 'cached_reuse' : 'watch')
	    }, resumeFlow);
	    lastHiddenAt = 0;
	    lastHiddenWallAt = 0;
    if (hiddenVideoCloseTimer) {
      clearTimeout(hiddenVideoCloseTimer);
      hiddenVideoCloseTimer = null;
    }
	    if (hiddenStreamFocusTimer) {
	      clearTimeout(hiddenStreamFocusTimer);
	      hiddenStreamFocusTimer = null;
	    }
    if (fallbackFrameAvailable) {
      redrawPreservedFrame();
      if (longHidden || videoStale) showStreamRecovery();
    }
    keepFirstScreenPinned(false);
    refreshSpacetimeStateAfterResume(reason || 'visibility_resume').catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
    publishCurrentStreamFocus(reason || 'visibility_visible');
	    if (reusePersistedSocket) {
	      lastRecoveryVideoReconnectAt = now;
	      lastRecoveryVideoReconnectSeq = videoSocketOpenSeq;
	    }
	    if (hardRestore) {
	      recoverFreshMediaSession(reason || 'visibility_resume', 'old_tab_resume', {
	        forceReconnect: connectingTooLong || (cacheRestored && !reusePersistedSocket),
	        skipEarlyGrace: true,
	        keyframeReason: `${reason || 'resume'}_cached_keyframe`
	      });
	      resumeFlow.paused = false;
	      resumeFlow.attempts = 1;
	      clearActivationReconnectBurst();
	      activationReconnectBurstTimer = setTimeout(
	        () => runActivationReconnectBurst(reason || 'visibility_resume', resumeFlow),
	        activationReconnectFirstRetryMs
	      );
	      return;
    }
    if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {
      connectDirectVideo();
    }
    if (videoStale) {
      reconnectVideoForRecovery('visibility_resume_stale');
	    }
	    chaseLiveStream();
	    runActivationReconnectBurst(reason || 'visibility_resume', resumeFlow);
	  }

  function preserveExperimentalClientHDRForNetworkResume() {
    if (!experimentalMediaPreferenceController.enabled || !experimentalMediaState.enabled ||
      experimentalMediaState.engine !== CLIENT_HDR_ENGINE ||
      document.visibilityState !== 'visible' || experimentalMediaPresentationRegionBlocked ||
      !experimentalHDRSurfacePresentationAllowed() ||
      controlCodeHDRFreezeTargetActive()) return false;
    const controller = experimentalClientHDRController;
    const snapshot = controller && typeof controller.snapshot === 'function'
      ? controller.snapshot()
      : null;
    if (!snapshot || !snapshot.active || !snapshot.ready || !snapshot.rendererActive ||
      !snapshot.firstPresented || !snapshot.surfaceVisible ||
      typeof controller.holdLastPresentation !== 'function') return false;
    // A socket interruption can leave an old recovery request queued even
    // though this continuous surface is still valid.  Clear only that
    // unblocked/presentation-authorized pending state; true region or control
    // authority loss was rejected above.
    experimentalMediaPresentationRecoveryPending = false;
    experimentalMediaPresentationRecoveryReason = '';
    controller.setDocumentVisible(true);
    return controller.holdLastPresentation('network_online_waiting_keyframe');
  }

  function recoverExperimentalMediaAfterNetworkOnline() {
    if (!experimentalMediaPreferenceController.enabled) return false;
    const preserved = preserveExperimentalClientHDRForNetworkResume();
    if (!preserved) {
      beginExperimentalMediaForegroundRecovery('network_online', { forceCanvasReset: true });
    }
    refreshSpacetimeStateAfterResume('network_online')
      .catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
    if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {
      connectDirectVideo({ skipEarlyGrace: true });
    }
    chaseLiveStream();
    requestKeyframeDebounced('network_online_keyframe', 0, true);
    return preserved;
  }

  window.addEventListener('resize', resizeCanvasBox);
  window.addEventListener('scroll', () => {
    updateDetailsReveal();
  }, { passive: true });
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', resizeCanvasBox);
    window.visualViewport.addEventListener('scroll', updateViewportVars, { passive: true });
  }
  document.addEventListener('visibilitychange', () => {
    if (typeof refreshUserActivityTickSchedule === 'function') refreshUserActivityTickSchedule();
    scheduleStreamFeedback('visibility_change');
    if (document.visibilityState === 'visible') {
	      if (typeof resumeExperimentalMediaForLifecycle === 'function') resumeExperimentalMediaForLifecycle('visibility_resume');
	      if (typeof scheduleExperimentalMediaForegroundReturnConfirmation === 'function') {
	        scheduleExperimentalMediaForegroundReturnConfirmation('visibility_resume');
	      }
	      hiddenDecoderTransientLogged = false;
      refreshMemberLimitProjection('visibility_resume_limit_refresh');
      ticketCurrentProofVisualState.resumePending = true;
      noteViewerActivity(null, 'visibility_visible');
      if (hiddenStreamFocusTimer) {
        clearTimeout(hiddenStreamFocusTimer);
        hiddenStreamFocusTimer = null;
      }
      recoverAfterVisibilityResume('visibility_resume');
	    } else if (document.visibilityState === 'hidden') {
	      if (typeof armExperimentalMediaLifecycleResume === 'function') armExperimentalMediaLifecycleResume();
	      if (typeof experimentalMediaState !== 'undefined' && experimentalMediaState.enabled && typeof closeExperimentalMedia === 'function') {
	        closeExperimentalMedia({ keepEnabled: true, status: 'HDR skats apturēts fonā.' });
	      }
	      hiddenDecoderTransientLogged = false;
	      const flow = pauseActivationResumeLifecycle('visibility_hidden', 'visibility_hidden');
	      logResumeCheckpoint('activation_visibility_hidden', { reason: 'hidden' }, flow);
	      lastHiddenAt = performance.now();
	      lastHiddenWallAt = Date.now();
	      clearActivationReconnectBurst();
      pauseHiddenStreamAfterGrace('visibility_hidden');
	      if (!hasRenderedFrame || !streamHasFreshRenderedFrame()) {
	        logResumeCheckpoint('activation_visibility_hidden_cold_shutdown', { reason: 'cold_open' }, flow);
	      }
	    }
  });
  window.addEventListener('pageshow', (event) => {
	    if (typeof refreshUserActivityTickSchedule === 'function') refreshUserActivityTickSchedule();
	    const foregroundAttempt = typeof foregroundRecoveryCurrent === 'function' &&
	      foregroundRecoveryCurrent(experimentalMediaForegroundRecovery)
	      ? experimentalMediaForegroundRecovery
	      : null;
	    const returnAlreadyClustered = Boolean(foregroundAttempt &&
	      Number(foregroundAttempt.id || 0) === Number(experimentalMediaLifecycleResumeAttemptID || 0));
	    if (!experimentalMediaLifecycleArmed && !returnAlreadyClustered &&
	      typeof armExperimentalMediaLifecycleResume === 'function') {
	      armExperimentalMediaLifecycleResume();
	    }
	    if (typeof resumeExperimentalMediaForLifecycle === 'function') {
	      resumeExperimentalMediaForLifecycle(event && event.persisted ? 'pageshow_persisted' : 'pageshow');
	    }
	    if (typeof scheduleExperimentalMediaForegroundReturnConfirmation === 'function') {
	      scheduleExperimentalMediaForegroundReturnConfirmation(event && event.persisted ? 'pageshow_persisted' : 'pageshow');
	    }
	    ticketCurrentProofVisualState.resumePending = true;
	    const resumedFromIdle = noteViewerActivity(event, 'pageshow');
    keepFirstScreenPinned(true);
    const restored = event.persisted || lastHiddenAt > 0 || (typeof document !== 'undefined' && document.wasDiscarded === true);
    if (!resumedFromIdle) {
      if (restored) {
        recoverAfterVisibilityResume(event.persisted ? 'pageshow_persisted' : 'pageshow');
      } else {
        followActivationResumeLifecycle('pageshow', 'pageshow');
      }
    }
    chaseLiveStream();
  });
  window.addEventListener('online', () => {
    if (typeof refreshUserActivityTickSchedule === 'function') refreshUserActivityTickSchedule();
    recoverExperimentalMediaAfterNetworkOnline();
  });
  window.addEventListener('offline', () => {
    if (typeof refreshUserActivityTickSchedule === 'function') refreshUserActivityTickSchedule();
    if (usesDirectSpacetimeAuth()) markSpacetimeStateUnconfirmed('network_offline');
    reconcileClientHDRStreamContinuity('network_offline', 'sdr_stream_unavailable');
  });
  window.addEventListener('blur', () => {
    if (typeof armExperimentalMediaLifecycleResume === 'function') armExperimentalMediaLifecycleResume();
    // A browser-chrome or keyboard-navigation blur can arrive while the page
    // remains visible. Preserve its pixels as passive holdover, but keep the
    // lifecycle armed so the next focus creates a fresh renderer. Real hidden
    // and page lifecycle signals still close immediately.
    if (document.visibilityState === 'visible') {
      reconcileClientHDRStreamContinuity('window_blur_visible', 'window_blur_visible');
      return;
    }
    if (typeof experimentalMediaState !== 'undefined' && experimentalMediaState.enabled &&
      typeof closeExperimentalMedia === 'function') {
      closeExperimentalMedia({ keepEnabled: true, status: 'HDR skats apturēts fonā.' });
    }
  });
  window.addEventListener('focus', () => {
    noteExperimentalMediaForegroundPulse();
    if (typeof recoverExperimentalMediaForFocusOnlyLifecycle === 'function') {
      recoverExperimentalMediaForFocusOnlyLifecycle();
    }
    if (typeof resumeExperimentalMediaForLifecycle === 'function') resumeExperimentalMediaForLifecycle('focus');
    if (typeof scheduleExperimentalMediaForegroundReturnConfirmation === 'function') {
      scheduleExperimentalMediaForegroundReturnConfirmation('focus');
    }
    if (noteViewerActivity(null, 'focus')) return;
    const focusRestored = document.visibilityState === 'visible' && (
      lastHiddenAt > 0 ||
      lastHiddenWallAt > 0
    );
    if (focusRestored) {
      ticketCurrentProofVisualState.resumePending = true;
      recoverAfterVisibilityResume('focus');
      return;
    }
    chaseLiveStream();
    publishCurrentStreamFocus('focus');
    followActivationResumeLifecycle('focus', 'focus');
  });
	  window.addEventListener('pagehide', (event) => {
	    if (typeof clearUserActivityTickTimer === 'function') clearUserActivityTickTimer();
	    if (typeof armExperimentalMediaLifecycleResume === 'function') armExperimentalMediaLifecycleResume();
	    if (typeof closeExperimentalMedia === 'function') closeExperimentalMedia({
	      keepEnabled: true,
	      status: 'HDR skats apturēts fonā.'
	    });
	    const flow = pauseActivationResumeLifecycle(event && event.persisted ? 'pagehide_cached' : 'pagehide', 'pagehide');
	    logResumeCheckpoint('activation_pagehide', {
	      cache: resumeBooleanLabel(Boolean(event && event.persisted))
	    }, flow);
	    closeEarlyVideo('pagehide');
	    clearActivationReconnectBurst();
    lastHiddenAt = performance.now();
    lastHiddenWallAt = Date.now();
    if (event && event.persisted) {
      preserveCurrentFrame('pagehide_cached');
	      pauseHiddenStreamAfterGrace('pagehide_cached');
      return;
    }
	    if (hiddenStreamFocusTimer) {
	      clearTimeout(hiddenStreamFocusTimer);
	      hiddenStreamFocusTimer = null;
	    }
    publishStreamFocus(false, 'pagehide');
    closeDirectVideo();
    if (spacetimeClient && typeof spacetimeClient.disconnectPresence === 'function') {
      spacetimeClient.disconnectPresence();
    }
  });
  window.addEventListener('load', () => keepFirstScreenPinned(true));
  setInterval(() => {
    if (idleDisconnected) return;
    if (spacetimeClient && typeof spacetimeClient.heartbeat === 'function') {
      const active = currentStreamFocusActive();
      spacetimeClient.heartbeat(active, active ? 'browser_stream_heartbeat' : 'browser_no_stream_heartbeat');
    }
  }, 15000);
  feedbackTimer = setInterval(() => sendStreamFeedback('interval', false), 500);
  setInterval(() => {
    noteExperimentalMediaForegroundPulse();
    chaseLiveStream();
  }, 1000);
  updateViewportVars();
  keepFirstScreenPinned(true);
  updateDetailsReveal();
  resizeCanvasBox();
  scheduleViewerIdleDisconnect('initial_load');
	  showQuietStreamLoading();
	  connectSpacetimeState().catch((error) => clientLog('spacetime_connect_failed', error && error.message));
	  connect();
	  if (!activeResumeFlow) {
	    startActivationResumeFlow('initial_load', 'initial_load');
	  }

})();
