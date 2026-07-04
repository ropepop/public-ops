import { html, reactive } from '@arrow-js/core';

(function () {
  const cfg = window.TICKET_REMOTE_CONFIG || {};
  const pageVersion = cfg.pageVersion || 'ticket-remote-dev';
  const assetVersion = String(cfg.assetVersion || pageVersion || '').trim();

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
  const controlCodeAutoPrepareMinIntervalMs = 45000;
  let spacetimeClient = null;

  function enqueueClientLog(entry) {
    pendingClientLogs.push(compactClientLogEntry(entry));
    if (pendingClientLogs.length > 100) pendingClientLogs.splice(0, pendingClientLogs.length - 100);
    try {
      if (typeof queueMicrotask === 'function') {
        queueMicrotask(() => flushClientLogs());
      }
    } catch (_) {}
  }

  function compactClientLogEntry(entry) {
    const rawEventName = String(entry && entry.event || 'client_event').replace(/[^0-9A-Za-z_-]/g, '_').slice(0, 80) || 'client_event';
    const eventName = compactClientEventName(rawEventName);
    const normalized = Object.assign({}, entry, { event: eventName });
    if (eventName === rawEventName) return normalized;
    if (normalized.detailJson) {
      normalized.detailJson = detailJsonWithOriginalEvent(normalized.detailJson, rawEventName);
    } else if (normalized.detail) {
      normalized.detail = detailTextWithOriginalEvent(normalized.detail, rawEventName);
    } else {
      normalized.detail = safeString({ originalEvent: rawEventName }).slice(0, 1000);
    }
    return normalized;
  }

  function detailJsonWithOriginalEvent(detailJson, rawEventName) {
    try {
      const parsed = JSON.parse(String(detailJson || '{}'));
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        if (!parsed.originalEvent) parsed.originalEvent = rawEventName;
        return safeString(parsed).slice(0, 1000);
      }
    } catch (_) {}
    return safeString({ detail: safeString(detailJson).slice(0, 800), originalEvent: rawEventName }).slice(0, 1000);
  }

  function detailTextWithOriginalEvent(detail, rawEventName) {
    try {
      const parsed = JSON.parse(String(detail || '{}'));
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        if (!parsed.originalEvent) parsed.originalEvent = rawEventName;
        return safeString(parsed).slice(0, 1000);
      }
    } catch (_) {}
    return safeString({ detail: safeString(detail).slice(0, 800), originalEvent: rawEventName }).slice(0, 1000);
  }

  function sampledClientLogDetail(event, detail) {
    if (!sampledClientLogEvents.has(event)) return detail;
    const now = Date.now();
    const previous = sampledClientLogState.get(event) || { at: 0, dropped: 0 };
    if (now - previous.at < sampledClientLogIntervalMs) {
      previous.dropped += 1;
      sampledClientLogState.set(event, previous);
      return null;
    }
    const dropped = previous.dropped || 0;
    sampledClientLogState.set(event, { at: now, dropped: 0 });
    if (!dropped) return detail;
    return safeString({
      detail,
      droppedSinceLast: dropped
    });
  }

  function compactClientEventName(event) {
    switch (event) {
      case 'page_boot':
        return 'browser_opened';
      case 'video_socket_opened':
        return 'stream_opened';
      case 'video_socket_closed':
      case 'video_socket_closed_intentional':
      case 'viewer_idle_disconnected':
      case 'video_stream_paused_hidden':
        return 'stream_closed';
      case 'video_socket_connect_attempt':
      case 'video_stream_restart':
      case 'fresh_video_resume':
      case 'cached_video_resume':
      case 'viewer_idle_resumed':
        return 'stream_started';
      case 'keyframe_request':
        return 'keyframe_requested';
      case 'keyframe_request_failed':
        return 'keyframe_failed';
      case 'stream_recovery_request':
      case 'h264_server_recover_requested':
        return 'stream_recovery_requested';
      case 'activation_resume_start':
      case 'activation_resume_retry':
      case 'activation_resume_deep_recover':
      case 'activation_resume_recovery_decision':
        return 'stream_recovery_requested';
      case 'activation_resume_fresh_frame':
      case 'activation_resume_finish':
        return 'stream_recovered';
      case 'activation_resume_exhausted':
      case 'activation_resume_media_stuck':
        return 'stream_failed';
      case 'activation_resume_merged':
      case 'activation_resume_paused':
      case 'activation_resume_log_limit':
        return 'stream_recovery_ignored';
      case 'stream_recover_request_failed':
        return 'stream_failed';
      case 'stale_video_frames':
      case 'server_stale_frames':
      case 'loading_over_2s':
        return 'stream_stalled';
      case 'control_code_submitted':
        return 'control_code_requested';
      case 'control_code_prepare_complete':
      case 'control_code_auto_prepare_complete':
        return 'control_code_sent';
      case 'control_code_request_failed':
      case 'control_code_prepare_failed':
      case 'control_code_auto_prepare_failed':
      case 'control_code_close_failed':
        return 'control_code_failed';
      case 'control_code_capture_keepalive':
        return 'control_code_capturing';
      case 'control_code_message_ignored':
      case 'control_code_close_ignored':
        return 'control_code_ignored';
      case 'spacetime_connect_failed':
      case 'spacetime_reconnect_failed':
      case 'spacetime_direct_unavailable':
        return 'state_failed';
      case 'spacetime_client_status':
        return 'state_changed';
      default:
        break;
    }
    if (event.includes('recover') || event.includes('recovery')) return event.includes('failed') ? 'stream_failed' : 'stream_recovery_requested';
    if (event.includes('keyframe')) return event.includes('failed') ? 'keyframe_failed' : 'keyframe_requested';
    if (event.includes('control_code') && event.includes('failed')) return 'control_code_failed';
    if (event.includes('video_socket') && event.includes('open')) return 'stream_opened';
    if (event.includes('video_socket') && event.includes('closed')) return 'stream_closed';
    return event;
  }

  function reportClientFault(event, detail) {
    const rawEventName = String(event || 'client_event').replace(/[^0-9A-Za-z_-]/g, '_').slice(0, 80) || 'client_event';
    const eventName = compactClientEventName(rawEventName);
    const detailText = safeString(detail).slice(0, 500);
    const sampledDetail = sampledClientLogDetail(rawEventName, detailText);
    if (sampledDetail == null) return;
    const detailPayload = {
      pageVersion,
      assetVersion,
      detail: sampledDetail,
      visibility: document.visibilityState,
      webCodecs: 'VideoDecoder' in window,
      userAgent: navigator.userAgent
    };
    if (eventName !== rawEventName) detailPayload.originalEvent = rawEventName;
    enqueueClientLog({
      level: 'info',
      event: eventName,
      detail: safeString(detailPayload).slice(0, 1000),
      correlationId: typeof browserTraceId === 'string' ? browserTraceId : '',
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
  let spacetimeDirectUnavailable = false;
  let spacetimeDirectUnavailableLogged = false;
  let directSpacetimeToken = '';
  let directSpacetimeTokenExpiresAt = 0;
  let spacetimeClientScriptPromise = null;
  let spacetimeClientConnectPromise = null;

  if (!cfg.authenticated) {
    startAuthRedirect();
    return;
  }

  if (document.querySelector('[data-admin="true"]')) {
    startAdmin();
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
  const connectionState = requireElement('#connectionState', 'connectionState');
  const statusLine = requireElement('#statusLine', 'statusLine');
  if (!connectionState || !statusLine) return;
  const panel = document.getElementById('panel');
  const presence = requireElement('#presence', 'presence');
  const requestCodeButton = requireElement('#requestControlCode', 'requestControlCode');
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
  const controlCodeCloseHotspot = requireElement('#controlCodeCloseHotspot', 'controlCodeCloseHotspot');
  if (!presence || !requestCodeButton || !codeRequestState || !codeRequestDetail || !codeDialog || !codeForm || !codeDigits || !codeSubmit || !codeDialogClose || !codeError || !codeResultArea || !codeResultImage || !codeResultStatus || !codeResultValue || !codeResultTimer || !codeResultClose || !controlCodeHotspot || !controlCodeCloseHotspot) return;
  const viewerCount = document.getElementById('viewerCount');
  const viewerCountDetail = document.getElementById('viewerCountDetail');
  const presenceState = reactive({ viewers: [], visibleViewerCount: 0 });
  let presenceMounted = false;

  let videoWs = null;
  const activeVideoSockets = new Set();
  let reconnectTimer = null;
  let hiddenVideoCloseTimer = null;
  let hiddenStreamFocusTimer = null;
  let configured = false;
  let streamUnsupported = false;
  let streamSize = { width: 540, height: 1080 };
  let currentState = null;
  let serverClockSkewMs = 0;
  let pointerStart = null;
  let connectedAt = 0;
  let videoSocketCreatedAt = 0;
  let videoConnectedAt = 0;
  let configuredAt = 0;
  let lastFrameAt = 0;
  let lastHiddenAt = 0;
  let lastDecodedFrameAt = 0;
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
  let lastRenderedFrameTimestamp = 0;
  let lastRestartAt = 0;
  let lastRecoveryKeyframeAt = 0;
  let lastKeyframeCommandAt = 0;
  let lastRecoveryDecoderResetAt = 0;
  let lastRecoveryVideoReconnectAt = 0;
  let lastRecoveryServerRecoverAt = 0;
  let firstFrameServerRecoveryAttempts = 0;
  let firstFrameServerRecoveryExhausted = false;
  let decoder = null;
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
  let firstRenderedTraceSent = false;
  let hasRenderedFrame = false;
  let fallbackFrameCanvas = null;
  let fallbackFrameAvailable = false;
  let lastFallbackFrameAt = 0;
  let latestStreamStatus = null;
  let lastStreamStatusAt = 0;
  let codeRequest = null;
  let pendingFrameMetadata = [];
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
  let controlCodeSafeGeneratedFrameRequestID = '';
  let controlCodeSafeGeneratedFrameEpoch = 0;
  let controlCodeSafeGeneratedFrameSequence = 0;
  let controlCodeSafeGeneratedFrameCount = 0;
  let controlCodeFrozenFrameCanvas = null;
  let controlCodeFrozenFrameKey = '';
  let controlCodePreparedCaptureProof = null;
  let controlCodePreparedCaptureDisplayedRequestID = '';
  let controlCodeAutoPrepareInFlight = false;
  let lastControlCodeAutoPrepareAt = 0;
  const localSessionID = String(cfg.sessionId || '').trim();
  const localPublicID = accountPublicId(cfg.email || '');
  const browserTraceId = accountPublicId(localSessionID || localPublicID || pageVersion);
  const ownedControlCodeRequestIDs = new Set();
  const locallyClosedControlCodeRequestIDs = new Set();
  let codeDialogOpen = false;
  let controlCodeDialogScrollLock = null;
  let codeResultTickTimer = null;
  let activeStreamOpenMetric = null;
  let activeControlCodeMetric = null;
  let resumeRecoverySoftTimer = null;
  let resumeRecoveryHardTimer = null;
  let activeResumeFlow = null;
  let pendingResumeFreshFrameFlow = null;
  let activationReconnectBurstTimer = null;
  let videoSocketOpenSeq = 0;
  let lastHiddenWallAt = 0;
  let stableViewport = null;
  let screenEngaged = false;
  let screenWakeLock = null;
  let screenWakeLockRequesting = false;
  let screenWakeLockUnavailableLogged = false;
  let ticketFullscreenAttempted = false;
  let toolbarAnchorLogged = false;
  let idleDisconnected = false;
  let idleDisconnectTimer = null;
  const intentionallyClosedVideoSockets = new WeakSet();
  let lastTouchEndAt = 0;
  let lastTouchEndX = 0;
  let lastTouchEndY = 0;
  const maxTapDurationMs = 450;
  const maxTapTravelPx = 14;
  const streamVerticalPanThresholdPx = 6;
  const streamVerticalPanDominance = 1.1;
  const streamFirstFrameKeyframeMs = 2000;
  const streamLiveFreshMaxAgeMs = 1000;
  const streamLiveOkMaxAgeMs = 1500;
  const streamDegradedMaxAgeMs = 2000;
  const streamStaleKeyframeMs = 2500;
  const streamStaleDecoderResetMs = 5000;
  const streamStaleVideoReconnectMs = 8000;
  const streamStaleServerRecoverMs = 12000;
  const streamDecoderStartupGraceMs = 3500;
  const hiddenVideoCloseDelayMs = 3000;
  const backgroundRecoveryHiddenMs = 30000;
  const oldTabFreshResumeHiddenMs = 5000;
  const resumeVideoReconnectDelayMs = 600;
  const resumeSoftReconnectMs = 1800;
  const resumeHardRecoverMs = 3200;
  const activationReconnectBurstMs = 10000;
  const activationReconnectTickMs = 1000;
  const activationReconnectMaxTicks = 10;
  const activationResumeLogLimit = 32;
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
  const controlCodeCaptureKeyframeRetryMs = 5000;
  const controlCodeCaptureKeyframeRetryLimit = 2;
  const controlCodeGeneratedChipScanStartY = 0.50;
  const controlCodeGeneratedChipScanEndY = 0.61;
  const controlCodeGeneratedChipScanStepY = 0.01;
  const FRAME_ENVELOPE_MAGIC = 0x54534632;
  const FRAME_ENVELOPE_HEADER_BYTES = 29;
  const doubleTapSuppressMs = 420;
  const doubleTapSuppressPx = 28;

  function closeEarlyVideo(reason) {
    const early = window.TICKET_EARLY_VIDEO;
    if (!early || early.claimed) return;
    early.claimed = true;
    early.queue = [];
    const socket = early.ws;
    early.ws = null;
    if (!socket) return;
    try { socket.close(1000, reason || 'app_loaded'); } catch (_) {}
  }

  function claimEarlyVideoSocket() {
    const early = window.TICKET_EARLY_VIDEO;
    if (!early || early.claimed) return null;
    early.claimed = true;
    const socket = early.ws;
    const queued = Array.isArray(early.queue) ? early.queue.slice() : [];
    early.queue = [];
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
    if (event && event.isTrusted === false) return;
    if (idleDisconnected) {
      resumeFromIdleDisconnect(reason || (event && event.type) || 'activity');
      return;
    }
    scheduleViewerIdleDisconnect(reason || (event && event.type) || 'activity');
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
	    clearResumeWatchdogs();
	    clearActivationReconnectBurst();
	    closeEarlyVideo('idle_disconnect');
    closeDirectVideo();
    resetStreamState({ preserveFrame: true });
    if (spacetimeClient && typeof spacetimeClient.close === 'function') {
      spacetimeClient.close();
    }
    spacetimeClient = null;
    releaseScreenWakeLock('idle_disconnect');
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
    idleDisconnected = false;
    document.body.dataset.streamFreshness = 'RECOVERING';
    setConnected('Savienojas');
    setStatus('Atjauno tiešraidi...');
    showStreamRecovery();
    scheduleViewerIdleDisconnect(reason || 'idle_resume');
    beginStreamOpenMetric('old_tab_resume', reason || 'idle_resume', true);
    connectSpacetimeState().catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
    connectDirectVideo();
    publishCurrentStreamFocus(reason || 'idle_resume');
    requestKeyframeDebounced(`${reason || 'idle_resume'}_keyframe`, 0, true);
    requestServerRecoveryDebounced(`${reason || 'idle_resume'}_recover`, true);
	    scheduleResumeWatchdogs(reason || 'idle_resume');
	    publishStreamDebug();
	    clientLog('viewer_idle_resumed', reason || 'idle_resume');
	    startActivationResumeFlow(reason || 'idle_resume', 'idle_resume');
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

  function ticketViewportRect() {
    return visualViewportRect();
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

  function toolbarCollapseAnchorPx() {
    return Math.round(Math.min(96, Math.max(24, viewportHeight() * 0.12)));
  }

  function updateViewportVars() {
    const stageViewport = stableStageViewportRect();
    const dialogViewport = visualViewportRect();
    document.documentElement.style.setProperty('--ticket-stage-height', `${stageViewport.height}px`);
    document.documentElement.style.setProperty('--ticket-viewport-width', `${stageViewport.width}px`);
    document.documentElement.style.setProperty('--ticket-viewport-height', `${stageViewport.height}px`);
    document.documentElement.style.setProperty('--ticket-viewport-left', `${stageViewport.offsetLeft}px`);
    document.documentElement.style.setProperty('--ticket-viewport-top', `${stageViewport.offsetTop}px`);
    document.documentElement.style.setProperty('--ticket-dialog-width', `${dialogViewport.width}px`);
    document.documentElement.style.setProperty('--ticket-dialog-height', `${dialogViewport.height}px`);
    document.documentElement.style.setProperty('--ticket-dialog-left', `${dialogViewport.offsetLeft}px`);
    document.documentElement.style.setProperty('--ticket-dialog-top', `${dialogViewport.offsetTop}px`);
    // --ticket-toolbar-anchor write removed: the CSS no longer references it after the
    // .stage / .stage-page / .shell rules were migrated to min-height:100dvh. The JS-side
    // helper toolbarCollapseAnchorPx() and the toolbarAnchorLogged flag are left in place
    // as dead-but-harmless code so the clientLog('toolbar_collapse_anchor', ...) line
    // history is not lost; they will be removed in the next cleanup pass.
  }

  function firstScreenAnchorTop() {
    return screenEngaged ? toolbarCollapseAnchorPx() : 0;
  }

  function updateDetailsReveal() {
    if (controlCodeDialogScrollLock && controlCodeDialogScrollLock.active) return;
    const revealed = window.scrollY >= Math.max(1, firstScreenAnchorTop() + viewportHeight() * 0.82);
    document.body.classList.toggle('details-visible', revealed);
    if (panel) panel.setAttribute('aria-hidden', revealed ? 'false' : 'true');
  }

  function keepFirstScreenPinned(force) {
    if (force) {
      document.body.classList.remove('details-visible');
      if (panel) panel.setAttribute('aria-hidden', 'true');
    }
    updateDetailsReveal();
  }

  function scheduleFirstScreenPin(force) {
    keepFirstScreenPinned(force);
  }

  function anchorToolbarCollapse(_reason) {
    updateViewportVars();
  }

  async function requestScreenWakeLock(reason) {
    if (!screenEngaged || document.visibilityState === 'hidden' || screenWakeLock || screenWakeLockRequesting) return;
    if (!navigator.wakeLock || typeof navigator.wakeLock.request !== 'function') {
      if (!screenWakeLockUnavailableLogged) {
        screenWakeLockUnavailableLogged = true;
        clientLog('wake_lock_unavailable', reason || 'unsupported');
      }
      return;
    }
    screenWakeLockRequesting = true;
    try {
      const lock = await navigator.wakeLock.request('screen');
      screenWakeLock = lock;
      clientLog('wake_lock_acquired', reason || 'gesture');
      if (lock && typeof lock.addEventListener === 'function') {
        lock.addEventListener('release', () => {
          if (screenWakeLock === lock) {
            screenWakeLock = null;
          }
          clientLog('wake_lock_released', 'browser');
        });
      }
    } catch (error) {
      clientLog('wake_lock_failed', `${reason || 'gesture'}:${error && error.message || 'request failed'}`);
    } finally {
      screenWakeLockRequesting = false;
    }
  }

  function releaseScreenWakeLock(reason) {
    if (!screenWakeLock) return;
    const lock = screenWakeLock;
    screenWakeLock = null;
    if (!lock || typeof lock.release !== 'function') return;
    try {
      Promise.resolve(lock.release()).then(
        () => clientLog('wake_lock_released', reason || 'release'),
        (error) => clientLog('wake_lock_release_failed', `${reason || 'release'}:${error && error.message || 'release failed'}`)
      );
    } catch (error) {
      clientLog('wake_lock_release_failed', `${reason || 'release'}:${error && error.message || 'release failed'}`);
    }
  }

  function requestTicketFullscreen(reason) {
    if (ticketFullscreenAttempted || document.fullscreenElement) return;
    ticketFullscreenAttempted = true;
    const target = stage || document.documentElement;
    const requestFullscreen = target.requestFullscreen
      ? target.requestFullscreen.bind(target)
      : (target.webkitRequestFullscreen ? target.webkitRequestFullscreen.bind(target) : null);
    if (!requestFullscreen) {
      clientLog('fullscreen_unavailable', reason || 'unsupported');
      return;
    }
    try {
      const result = target.requestFullscreen ? requestFullscreen({ navigationUI: 'hide' }) : requestFullscreen();
      Promise.resolve(result).then(
        () => clientLog('fullscreen_requested', reason || 'gesture'),
        (error) => clientLog('fullscreen_failed', `${reason || 'gesture'}:${error && error.message || 'request failed'}`)
      );
    } catch (error) {
      clientLog('fullscreen_failed', `${reason || 'gesture'}:${error && error.message || 'request failed'}`);
    }
  }

  function engageTicketScreen(reason) {
    const firstEngagement = !screenEngaged;
    if (firstEngagement) {
      screenEngaged = true;
      document.body.classList.add('screen-engaged');
      updateViewportVars();
      clientLog('screen_engaged', reason || 'gesture');
    }
    requestTicketFullscreen(reason || 'gesture');
    requestScreenWakeLock(reason || 'gesture');
  }

  function isControlCodeUiEventTarget(target) {
    if (!target || target === document || target === window) return false;
    return Boolean(
      controlCodeHotspot.contains(target) ||
      controlCodeCloseHotspot.contains(target) ||
      requestCodeButton.contains(target) ||
      codeDialog.contains(target) ||
      codeResultArea.contains(target)
    );
  }

  function handleScreenEngagementEvent(event) {
    if (event && event.type === 'keydown' && (event.metaKey || event.ctrlKey || event.altKey)) return;
    if (event && event.isTrusted === false) return;
    if (event && isControlCodeUiEventTarget(event.target)) return;
    engageTicketScreen(event && event.type || 'gesture');
  }

  for (const eventName of ['pointerdown', 'touchend', 'click', 'keydown']) {
    document.addEventListener(eventName, handleScreenEngagementEvent, { capture: true, passive: true });
  }

  function checkServerVersion(payload) {
    const serverVersion = payload && payload.serverVersion;
    const serverAssetVersion = String(payload && payload.assetVersion || '').trim();
    if (serverAssetVersion && assetVersion && serverAssetVersion !== assetVersion) {
      const next = new URL(location.href);
      next.searchParams.set('v', serverAssetVersion);
      location.replace(next.toString());
      return false;
    }
    if (!serverVersion || serverVersion === pageVersion) return true;
    if (!String(serverVersion).startsWith('ticket-remote-')) return true;
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

  function activeVideoRecoveryID() {
    if (activeResumeFlow && !activeResumeFlow.done && activeResumeFlow.id) return activeResumeFlow.id;
    return '';
  }

  function videoOpenRestoreReason(fallback) {
    if (activeResumeFlow && !activeResumeFlow.done) return activeResumeFlow.reason || activeResumeFlow.trigger || fallback || 'resume';
    if (lastHiddenAt > 0 || fallbackFrameAvailable || hasRenderedFrame) return fallback || 'old_page_resume';
    return fallback || 'cold_open';
  }

  function streamURL(reason) {
    const url = new URL('/api/v1/stream', location.href);
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const now = performance.now();
    appendStreamURLParam(url, 'page_version', pageVersion);
    appendStreamURLParam(url, 'asset_version', assetVersion);
    appendStreamURLParam(url, 'visibility', document.visibilityState);
    appendStreamURLParam(url, 'restore_reason', videoOpenRestoreReason(reason));
    appendStreamURLParam(url, 'recovery_id', activeVideoRecoveryID());
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

  function safeWebSocket(url, label) {
    if (typeof WebSocket !== 'function') {
      reportClientFault('websocket_unavailable', label || url);
      return null;
    }
    try {
      return new WebSocket(url);
    } catch (error) {
      reportClientFault('websocket_create_failed', `${label || url}:${error && error.message || 'create failed'}`);
      return null;
    }
  }

  const publicMessageTranslations = new Map([
    ['Phone stream reconnecting', 'Tālruņa straume savienojas no jauna'],
    ['Ticket server is starting', 'Biļetes serveris startējas'],
    ['Ticket server is stopped', 'Biļetes serveris ir apturēts'],
    ['Ticket session is active through root capture', 'Biļetes sesija darbojas ar root ekrāna tveršanu'],
    ['Root capture is idle', 'Root ekrāna tveršana ir gaidstāvē'],
    ['Root shell is unavailable', 'Root komandrinda nav pieejama'],
    ['Root screenrecord capture is available', 'Root ekrāna tveršana ir pieejama'],
    ['Root capture is starting', 'Root ekrāna tveršana startējas'],
    ['Root capture is active', 'Root ekrāna tveršana ir aktīva'],
    ['Root capture is unavailable', 'Root ekrāna tveršana nav pieejama'],
    ['ViVi is not installed from a local Pixel app store yet', 'ViVi vēl nav instalēta no vietējā Pixel lietotņu veikala'],
    ['ViVi launch intent is unavailable', 'ViVi palaišana nav pieejama'],
    ['No visible frame has been sent yet', 'Vēl nav nosūtīts neviens redzams kadrs'],
    ['Unavailable', 'Nav pieejams'],
    ['Connection failed', 'Savienojums neizdevās'],
    ['Video connection failed', 'Video savienojums neizdevās'],
    ['control_mode_removed', 'Kontroles režīms ir aizstāts ar koda pieprasījumiem'],
    ['invalid_code', 'Ievadi 2-8 ciparus'],
    ['rate_limited', 'Minūtē var pieprasīt divus kodus'],
    ['phone_timeout', 'Tālrunis nepaspēja izveidot kodu'],
    ['phone_unavailable', 'Tālrunis pašlaik nav pieejams'],
    ['control_code_result_timeout', 'Tālrunis nepaspēja izveidot kodu'],
    ['control_code_not_generated', 'Tālrunis neatgrieza ģenerētu kodu'],
    ['control_code_submit_returned_no_result', 'ViVi neatgrieza ģenerētu kodu'],
    ['control_code_submit_timeout', 'ViVi neapstiprināja kodu laikā'],
    ['control_code_request_hierarchy_unavailable', 'Tālrunis atjauno biļetes skatu. Mēģini vēlreiz.'],
    ['control_code_request_preflight_cleanup_failed', 'Tālrunis vēl atgriežas pie biļetes. Mēģini vēlreiz.'],
    ['control_code_request_previous_result_cleanup_failed', 'Iepriekšējais kods vēl aizveras. Mēģini vēlreiz.'],
    ['control_code_cleanup_attention_needed', 'Tālrunim vajag mirkli, lai atgrieztos pie biļetes'],
    ['control_code_stream_marker_required', 'Tālrunis nepaguva apstiprināt ģenerēto kodu'],
    ['waiting_for_ticket_reselect', 'Tālrunis vēl izvēlas biļeti. Uzgaidi mirkli.'],
    ['waiting_for_stream_recovery', 'Tiešraide atjaunojas pirms koda pieprasījuma.'],
    ['control_code_recovery_queue_timeout', 'Tālrunis nepaguva atjaunot biļeti. Mēģini vēlreiz.'],
    ['control_code_stream_unstable', 'Tiešraide nav pietiekami stabila koda pieprasījumam.'],
    ['extension_disabled', 'Pagarināšana ir izslēgta']
  ]);

  function localizePublicMessage(value) {
    if (!value) return '';
    const text = String(value);
    const exact = publicMessageTranslations.get(text);
    if (exact) return exact;
    for (const [prefix, translation] of [
      ['Ticket server is listening on ', 'Biļetes serveris klausās uz '],
      ['Ticket server failed to start: ', 'Biļetes serveri neizdevās palaist: '],
      ['Ticket session stopped: ', 'Biļetes sesija apturēta: '],
      ['Root capture stopped: ', 'Root ekrāna tveršana apturēta: '],
      ['Root capture restarting: ', 'Root ekrāna tveršana restartējas: '],
      ['Root capture exited with code ', 'Root ekrāna tveršana aizvērās ar kodu '],
      ['Root capture stream closed during restart', 'Root ekrāna tveršanas straume aizvērās restartēšanas laikā'],
      ['Root capture failed: ', 'Root ekrāna tveršana neizdevās: ']
    ]) {
      if (text.startsWith(prefix)) return translation + text.slice(prefix.length);
    }
    return text;
  }

  function isTechnicalPublicStatusMessage(value) {
    const text = String(value || '').trim();
    return /\b(ffmpeg|h\.?264|h265|h\.?265|root capture|root screenrecord|root shell|screenrecord|codec)\b/i.test(text);
  }

  function decodeBase64UrlJSON(value) {
    const padded = String(value || '').replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(String(value || '').length / 4) * 4, '=');
    return JSON.parse(atob(padded));
  }

  function jwtExpiresAtMillis(token) {
    const parts = String(token || '').split('.');
    if (parts.length !== 3) return 0;
    try {
      const payload = decodeBase64UrlJSON(parts[1]);
      const exp = Number(payload && payload.exp);
      return Number.isFinite(exp) && exp > 0 ? exp * 1000 : 0;
    } catch (_) {
      return 0;
    }
  }

  function rememberSpacetimeToken(token) {
    directSpacetimeToken = String(token || '');
    spacetimeDirectUnavailable = false;
    spacetimeDirectUnavailableLogged = false;
    directSpacetimeTokenExpiresAt = jwtExpiresAtMillis(token);
  }

  function spacetimeTokenExpired(token) {
    const expiresAt = directSpacetimeTokenExpiresAt || jwtExpiresAtMillis(token);
    return expiresAt > 0 && Date.now() + 30000 >= expiresAt;
  }

  function usesDirectSpacetimeAuth() {
    const mode = String((cfg.auth && cfg.auth.mode) || 'spacetime').toLowerCase();
    return !['cloudflare', 'cloudflare-access', 'cf-access', 'dev', 'development', 'none'].includes(mode);
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

  async function finishSpacetimeCallback() {
    location.replace('/');
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
      finishSpacetimeCallback().catch(showAuthError);
      return;
    }
    beginSpacetimeLogin(authReturnTarget()).catch(showAuthError);
  }

  function setStatus(text) {
    if (isTechnicalPublicStatusMessage(text)) return;
    statusLine.textContent = localizePublicMessage(text);
  }

  function sameEmail(left, right) {
    const cleanLeft = String(left || '').trim().toLowerCase();
    const cleanRight = String(right || '').trim().toLowerCase();
    return Boolean(cleanLeft && cleanRight && cleanLeft === cleanRight);
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

  clientLog('page_boot', JSON.stringify({
    pageVersion,
    assetVersion,
    visibility: document.visibilityState,
    webCodecs: 'VideoDecoder' in window
  }));

	  function flushClientLogs() {
	    if (!spacetimeClient || typeof spacetimeClient.appendSafeLog !== 'function') return;
	    if (!pendingClientLogs.length) return;
	    const batch = pendingClientLogs.splice(0, Math.min(20, pendingClientLogs.length));
	    batch.forEach((entry) => {
	      const detailJson = entry.detailJson || safeString({
	        pageVersion,
	        detail: entry.detail,
	        queuedAt: entry.at
	      }).slice(0, 1000);
	      spacetimeClient.appendSafeLog(entry.level || 'info', entry.event || 'client_event', detailJson, entry.correlationId || '')
	        .catch(() => {
	          if (pendingClientLogs.length < 100) pendingClientLogs.unshift(entry);
	        });
	    });
	  }

  function streamResumeSpinnerVisible() {
    return Boolean(streamResumeSpinner && !streamResumeSpinner.hidden);
  }

  function showStreamResumeSpinner() {
    if (!streamResumeSpinner || streamResumeSpinnerVisible()) return;
    streamResumeSpinner.hidden = false;
    publishStreamDebug();
  }

  function hideStreamResumeSpinner() {
    if (!streamResumeSpinner || !streamResumeSpinnerVisible()) return;
    streamResumeSpinner.hidden = true;
    publishStreamDebug();
  }

  function clearPreservedFrame() {
    fallbackFrameAvailable = false;
    lastFallbackFrameAt = 0;
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
    showEmpty(message, false);
    setStatus(message);
    clientLog('h264_unsupported', message);
  }

  function resizeCanvasBox() {
    updateViewportVars();
    const maxWidth = Math.max(1, stage.clientWidth);
    const maxHeight = Math.max(1, stage.clientHeight);
    const scale = Math.min(maxWidth / streamSize.width, maxHeight / streamSize.height);
    const displayWidth = Math.max(1, Math.floor(streamSize.width * scale));
    const displayHeight = Math.max(1, Math.floor(streamSize.height * scale));
    stage.style.setProperty('--stream-width', `${displayWidth}px`);
    stage.style.setProperty('--stream-height', `${displayHeight}px`);
    stage.style.setProperty('--stream-left', `${Math.max(0, Math.floor((maxWidth - displayWidth) / 2))}px`);
    stage.style.setProperty('--stream-top', `${Math.max(0, Math.floor((maxHeight - displayHeight) / 2))}px`);
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

  function beginStreamOpenMetric(kind, reason, force) {
    activeStreamOpenMetric = null;
    return null;
  }

  function finishStreamOpenMetric(phase, ok, detail) {
    activeStreamOpenMetric = null;
    clearResumeWatchdogs();
  }

  function clearResumeWatchdogs() {
    if (resumeRecoverySoftTimer) {
      clearTimeout(resumeRecoverySoftTimer);
      resumeRecoverySoftTimer = null;
    }
    if (resumeRecoveryHardTimer) {
      clearTimeout(resumeRecoveryHardTimer);
      resumeRecoveryHardTimer = null;
    }
  }

	  function streamHasFreshRenderedFrame() {
	    return currentRenderedFreshness(performance.now()).liveLabeled;
	  }

	  function safeResumeLabel(value, fallback) {
	    const label = String(value || fallback || 'unknown')
	      .toLowerCase()
	      .replace(/[0-9]/g, '')
	      .replace(/[^a-z_-]+/g, '_')
	      .replace(/_+/g, '_')
	      .replace(/^_+|_+$/g, '')
	      .slice(0, 48);
	    return label || fallback || 'unknown';
	  }

	  function resumeBooleanLabel(value) {
	    return value ? 'yes' : 'no';
	  }

	  function randomResumeLetters(length) {
	    const alphabet = 'abcdefghijklmnopqrstuvwxyz';
	    const size = Math.max(1, Math.min(24, length || 10));
	    let out = '';
	    try {
	      if (window.crypto && typeof window.crypto.getRandomValues === 'function') {
	        const bytes = new Uint8Array(size);
	        window.crypto.getRandomValues(bytes);
	        for (let i = 0; i < bytes.length; i += 1) out += alphabet[bytes[i] % alphabet.length];
	        return out;
	      }
	    } catch (_) {}
	    for (let i = 0; i < size; i += 1) out += alphabet[Math.floor(Math.random() * alphabet.length) % alphabet.length];
	    return out;
	  }

	  function randomResumeFlowId() {
	    return `resume_${randomResumeLetters(12)}`;
	  }

	  function socketStateLabel(state) {
	    switch (state) {
	      case WebSocket.CONNECTING:
	        return 'connecting';
	      case WebSocket.OPEN:
	        return 'open';
	      case WebSocket.CLOSING:
	        return 'closing';
	      case WebSocket.CLOSED:
	        return 'closed';
	      case -1:
	        return 'none';
	      default:
	        return 'unknown';
	    }
	  }

	  function hiddenDurationBucket(hiddenMs) {
	    if (!Number.isFinite(hiddenMs) || hiddenMs <= 0) return 'none';
	    if (hiddenMs >= backgroundRecoveryHiddenMs) return 'long';
	    if (hiddenMs >= oldTabFreshResumeHiddenMs) return 'old';
	    return 'short';
	  }

	  function renderedFreshnessLabel() {
	    const freshness = currentRenderedFreshness(performance.now());
	    if (!freshness.hasFrame) return 'no_frame';
	    return safeResumeLabel(freshness.streamFreshnessState, 'stale');
	  }

	  function decoderStateLabel() {
	    if (decoderConfigured) return 'configured';
	    return configured ? 'pending' : 'unconfigured';
	  }

	  function resumeDiagnosticSnapshot(detail) {
	    return Object.assign({
	      visibility: safeResumeLabel(document.visibilityState, 'unknown'),
	      focus: typeof document.hasFocus === 'function' ? (document.hasFocus() ? 'focused' : 'blurred') : 'unknown',
	      socket: socketStateLabel(videoSocketState()),
	      frame: renderedFreshnessLabel(),
	      configured: resumeBooleanLabel(configured),
	      decoder: decoderStateLabel(),
	      rendered: resumeBooleanLabel(hasRenderedFrame),
	      fallback: resumeBooleanLabel(fallbackFrameAvailable),
	      stream: streamUnsupported ? 'unsupported' : 'supported'
	    }, detail || {});
	  }

  function mediaSessionStuckOnPreservedFrame() {
    const socketState = videoSocketState();
    if (socketState !== WebSocket.OPEN && socketState !== WebSocket.CONNECTING) return false;
    if (!hasRenderedFrame && !fallbackFrameAvailable) return false;
	    const freshness = currentRenderedFreshness(performance.now());
	    if (freshness.liveLabeled) return false;
    if (freshness.streamFreshnessState !== 'STALE') return false;
    return !configured || !decoderConfigured;
  }

	  function enqueueResumeSafeLog(flow, event, detail) {
	    if (!flow || flow.done) return;
	    if (flow.logCount >= activationResumeLogLimit) {
	      if (!flow.limitLogged) {
	        flow.limitLogged = true;
	        enqueueClientLog({
	          level: 'info',
	          event: 'activation_resume_log_limit',
	          detailJson: safeString({ state: 'limited' }).slice(0, 600),
	          correlationId: flow.id
	        });
	      }
	      return;
	    }
	    flow.logCount += 1;
	    enqueueClientLog({
	      level: 'info',
	      event: safeResumeLabel(event, 'activation_resume_checkpoint'),
	      detailJson: safeString(detail || {}).slice(0, 600),
	      correlationId: flow.id
	    });
	  }

	  function enqueueCompletedResumeSafeLog(flow, event, detail) {
	    if (!flow || flow.freshLogged) return;
	    flow.freshLogged = true;
	    enqueueClientLog({
	      level: 'info',
	      event: safeResumeLabel(event, 'activation_resume_checkpoint'),
	      detailJson: safeString(detail || {}).slice(0, 600),
	      correlationId: flow.id
	    });
	  }

	  function logResumeCheckpoint(event, detail, flow) {
	    const targetFlow = flow || activeResumeFlow;
	    if (!targetFlow) return;
	    enqueueResumeSafeLog(targetFlow, event, resumeDiagnosticSnapshot(detail));
	  }

	  function clearActivationReconnectBurst() {
	    if (activationReconnectBurstTimer) {
	      clearTimeout(activationReconnectBurstTimer);
	      activationReconnectBurstTimer = null;
	    }
	  }

  function recoverySocketReusable(flow) {
    if (!flow || !flow.mediaRecoveryStartedAt) return false;
    if (!videoSocketKeepsStreamActive()) return false;
    return performance.now() - flow.mediaRecoveryStartedAt < recoveryVideoReconnectDebounceMs;
  }

  function noteRecoverySocketReuse(reason, kind, flow) {
    if (!recoverySocketReusable(flow)) return false;
    logResumeCheckpoint('activation_resume_socket_reused', {
      reason: safeResumeLabel(reason, 'media_session_recovery'),
      kind: safeResumeLabel(kind, 'media_session_recovery')
    }, flow);
    return true;
  }

	  function finishActivationResumeFlow(reason, flow) {
	    const targetFlow = flow || activeResumeFlow;
	    if (!targetFlow || targetFlow.done) return;
	    enqueueResumeSafeLog(targetFlow, 'activation_resume_finish', resumeDiagnosticSnapshot({
	      result: safeResumeLabel(reason, 'complete'),
	      phase: targetFlow.phase || 'unknown'
	    }));
	    if (reason === 'fresh_frame') {
	      pendingResumeFreshFrameFlow = null;
	    } else {
	      pendingResumeFreshFrameFlow = {
	        id: targetFlow.id,
	        reason: targetFlow.reason,
	        trigger: targetFlow.trigger,
	        phase: safeResumeLabel(reason, 'complete'),
	        freshLogged: false
	      };
	    }
	    targetFlow.done = true;
	    if (targetFlow === activeResumeFlow) {
	      clearActivationReconnectBurst();
	      activeResumeFlow = null;
	    }
	  }

	  function startActivationResumeFlow(reason, trigger, options) {
	    if (streamUnsupported) return null;
	    const pauseBurst = Boolean(options && options.pauseBurst);
	    let flow = activeResumeFlow;
	    if (flow && !flow.done) {
	      flow.reason = safeResumeLabel(reason, flow.reason);
	      flow.trigger = safeResumeLabel(trigger, flow.trigger);
	      logResumeCheckpoint('activation_resume_merged', {
	        reason: flow.reason,
	        trigger: flow.trigger,
	        phase: pauseBurst ? 'paused' : 'active'
	      }, flow);
	    } else {
	      pendingResumeFreshFrameFlow = null;
	      flow = {
	        id: randomResumeFlowId(),
	        reason: safeResumeLabel(reason, 'activation'),
	        trigger: safeResumeLabel(trigger, 'activation'),
	        startedAt: performance.now(),
	        deadlineAt: performance.now() + activationReconnectBurstMs,
	        attempts: 0,
	        mediaRecoveryStartedAt: 0,
	        logCount: 0,
	        limitLogged: false,
	        done: false,
	        phase: pauseBurst ? 'paused' : 'starting'
	      };
	      activeResumeFlow = flow;
	      logResumeCheckpoint('activation_resume_start', {
	        reason: flow.reason,
	        trigger: flow.trigger,
	        phase: flow.phase
	      }, flow);
	    }
	    if (pauseBurst) return flow;
	    runActivationReconnectBurst(reason || 'activation', flow);
	    return flow;
	  }

	  function activationRetryPhase(attempt) {
	    if (attempt <= 0) return 'initial';
	    if (attempt < 4) return 'early';
	    if (attempt < 8) return 'middle';
	    return 'late';
	  }

	  function runActivationReconnectBurst(reason, flow) {
	    if (!flow || activeResumeFlow !== flow || flow.done) return;
	    clearActivationReconnectBurst();
	    if (streamHasFreshRenderedFrame()) {
	      flow.phase = 'fresh';
	      logResumeCheckpoint('activation_resume_fresh_frame', { result: 'fresh' }, flow);
	      finishActivationResumeFlow('fresh_frame', flow);
	      return;
	    }
	    if (idleDisconnected) {
	      finishActivationResumeFlow('idle_disconnected', flow);
	      return;
	    }
	    if (document.visibilityState !== 'visible') {
	      flow.phase = 'paused';
	      logResumeCheckpoint('activation_resume_paused', { reason: 'hidden' }, flow);
	      return;
	    }
	    const now = performance.now();
	    if (flow.attempts >= activationReconnectMaxTicks || now >= flow.deadlineAt) {
	      flow.phase = 'exhausted';
	      logResumeCheckpoint('activation_resume_exhausted', { result: 'exhausted' }, flow);
	      finishActivationResumeFlow('exhausted', flow);
	      return;
	    }
	    const attempt = flow.attempts;
	    const phase = activationRetryPhase(attempt);
	    flow.phase = phase;
	    logResumeCheckpoint('activation_resume_retry', {
	      phase,
	      action: mediaSessionStuckOnPreservedFrame() ? 'media_deep_recover' : (attempt === 0 ? 'keyframe' : 'socket_reconnect')
	    }, flow);
	    connectSpacetimeState().catch(() => clientLog('spacetime_reconnect_failed', 'activation_resume'));
	    publishCurrentStreamFocus(safeResumeLabel(reason, 'activation'));
	    if (attempt === 0 && !mediaSessionStuckOnPreservedFrame()) {
	      connectDirectVideo();
	      requestKeyframeDebounced(`${reason || 'activation'}_activation_keyframe`, 0, true);
	    } else {
	      recoverFreshMediaSession(reason || 'activation', 'activation_resume', {
	        flow,
	        watchdogs: false,
	        keyframeReason: `${reason || 'activation'}_activation_keyframe`,
	        serverRecoveryReason: `${reason || 'activation'}_activation_recover`,
	        forceServerRecovery: mediaSessionStuckOnPreservedFrame() || attempt >= 2
	      });
	    }
	    flow.attempts += 1;
	    activationReconnectBurstTimer = setTimeout(() => runActivationReconnectBurst(reason, flow), activationReconnectTickMs);
	  }

	  function scheduleResumeWatchdogs(reason) {
    clearResumeWatchdogs();
    resumeRecoverySoftTimer = setTimeout(() => {
      resumeRecoverySoftTimer = null;
      if (idleDisconnected || document.visibilityState !== 'visible') return;
      if (streamHasFreshRenderedFrame()) return;
      if (noteRecoverySocketReuse(reason || 'resume', 'resume_soft_reconnect', activeResumeFlow)) {
        requestKeyframeDebounced(`${reason || 'resume'}_soft_reconnect`, 500);
        return;
      }
      preserveCurrentFrame('resume_soft_reconnect');
      closeDirectVideo();
      resetStreamState({ preserveFrame: true });
      connectDirectVideo();
      requestKeyframeDebounced(`${reason || 'resume'}_soft_reconnect`, 500);
    }, resumeSoftReconnectMs);
    resumeRecoveryHardTimer = setTimeout(() => {
      resumeRecoveryHardTimer = null;
	      if (idleDisconnected || document.visibilityState !== 'visible') return;
	      if (streamHasFreshRenderedFrame()) return;
	      recoverFreshMediaSession(reason || 'resume', 'resume_hard_recover', {
	        flow: activeResumeFlow,
	        watchdogs: false,
	        keyframeReason: `${reason || 'resume'}_hard_recover`,
	        keyframeMinIntervalMs: 500,
	        serverRecoveryReason: `${reason || 'resume'}_hard_recover`,
	        forceServerRecovery: true
	      });
	    }, resumeHardRecoverMs);
	  }

	  function recoverFreshMediaSession(reason, kind, options) {
	    if (idleDisconnected || streamUnsupported) return false;
	    options = options || {};
	    const recoveryReason = safeResumeLabel(reason, 'media_session_recovery');
	    const recoveryKind = safeResumeLabel(kind, 'media_session_recovery');
	    const flow = options.flow || activeResumeFlow;
	    const stuck = mediaSessionStuckOnPreservedFrame();
	    if (flow) {
	      if (stuck) {
	        logResumeCheckpoint('activation_resume_media_stuck', {
	          reason: recoveryReason,
	          kind: recoveryKind,
	          action: 'deep_recover'
	        }, flow);
	      }
	      logResumeCheckpoint('activation_resume_deep_recover', {
	        reason: recoveryReason,
	        kind: recoveryKind,
	        action: 'deep_recover'
	      }, flow);
	    }
    if (noteRecoverySocketReuse(recoveryReason, recoveryKind, flow)) {
      requestKeyframeDebounced(options.keyframeReason || `${reason || 'resume'}_fresh_media`, options.keyframeMinIntervalMs || 0, true);
      if (options.forceServerRecovery) {
        requestServerRecoveryDebounced(options.serverRecoveryReason || `${reason || 'resume'}_fresh_media_recover`, true);
      }
      if (options.watchdogs !== false) {
        scheduleResumeWatchdogs(reason || 'resume');
      }
      return true;
    }
    if (flow) flow.mediaRecoveryStartedAt = performance.now();
	    closeEarlyVideo(reason || 'media_session_recovery');
	    if (hiddenVideoCloseTimer) {
	      clearTimeout(hiddenVideoCloseTimer);
	      hiddenVideoCloseTimer = null;
	    }
	    if (hiddenStreamFocusTimer) {
	      clearTimeout(hiddenStreamFocusTimer);
	      hiddenStreamFocusTimer = null;
	    }
	    preserveCurrentFrame(`fresh_media_session:${reason || 'unknown'}`);
	    closeDirectVideo();
	    resetStreamState({ preserveFrame: true });
	    showStreamRecovery();
	    beginStreamOpenMetric(kind || 'media_session_recovery', reason || 'resume', true);
	    connectDirectVideo();
	    requestKeyframeDebounced(options.keyframeReason || `${reason || 'resume'}_fresh_media`, options.keyframeMinIntervalMs || 0, true);
	    if (options.forceServerRecovery) {
	      requestServerRecoveryDebounced(options.serverRecoveryReason || `${reason || 'resume'}_fresh_media_recover`, true);
	    }
	    if (options.watchdogs !== false) {
	      scheduleResumeWatchdogs(reason || 'resume');
	    }
	    return true;
	  }

	  function forceFreshVideoResume(reason, kind) {
	    if (idleDisconnected || streamUnsupported) return;
	    const now = performance.now();
	    const frameAgeMs = lastFrameAt > 0 ? now - lastFrameAt : -1;
    clientLog('fresh_video_resume', JSON.stringify({
      reason,
      kind,
      configured,
      frameAgeMs: Math.round(frameAgeMs),
      socketState: videoSocketState(),
	      hasRenderedFrame,
	      fallbackFrameAvailable
	    }));
	    recoverFreshMediaSession(reason || 'fresh_resume', kind || 'old_tab_resume', {
	      keyframeReason: `${reason || 'resume'}_fresh_socket`,
	      keyframeMinIntervalMs: 500
	    });
	  }

  function restoreCachedVideoForFreshFrame(reason, kind) {
    if (idleDisconnected || streamUnsupported) return;
    const now = performance.now();
    const frameAgeMs = lastFrameAt > 0 ? now - lastFrameAt : -1;
    clientLog('cached_video_resume', JSON.stringify({
      reason,
      kind,
      configured,
        frameAgeMs: Math.round(frameAgeMs),
        socketState: videoSocketState(),
        hasRenderedFrame,
        fallbackFrameAvailable
      }));
	    recoverFreshMediaSession(reason || 'cached_resume', kind || 'old_tab_resume', {
	      keyframeReason: `${reason || 'resume'}_cached_keyframe`,
	      serverRecoveryReason: `${reason || 'resume'}_cached_recover`,
	      forceServerRecovery: true
	    });
	  }

  function connect() {
    if (idleDisconnected) return;
    clearTimeout(reconnectTimer);
    keepFirstScreenPinned();
    setConnected('Savienojas');
    connectedAt = performance.now();
    if (!hasRenderedFrame) {
      beginStreamOpenMetric('cold_open', 'connect', false);
    }
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
    if (!hasRenderedFrame) {
      scheduleResumeWatchdogs('cold_open');
    }
  }

  function resetStreamState(options) {
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
      clearPreservedFrame();
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
    if (hiddenStreamFocusTimer) {
      clearTimeout(hiddenStreamFocusTimer);
      hiddenStreamFocusTimer = null;
    }
    if (controlCodeKeepsVideoAliveWhileHidden()) {
      publishStreamFocus(true, reason || 'hidden_control_code_capture');
      return;
    }
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

  function connectDirectVideo() {
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
      clientLog('early_video_connecting_grace', '');
      setTimeout(connectDirectVideo, 250);
      return;
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
	    const socket = safeWebSocket(streamURL('connect_direct_video'), 'video');
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
	    resetFirstFrameServerRecovery();
	    showStreamWaiting('Saņem video konfigurāciju...');
	    requestKeyframe(reason || 'video_socket_open');
  }

  function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {
    if (!socket) return false;
    videoWs = socket;
    activeVideoSockets.add(socket);
    socket.binaryType = 'arraybuffer';
    publishCurrentStreamFocus(reason || 'video_socket_adopted');
    socket.onopen = () => noteVideoSocketOpen(socket, reason || 'video_socket_open');
    socket.onmessage = (event) => {
      if (idleDisconnected || videoWs !== socket) return;
      handleVideoSocketMessage(event).catch((error) => {
        sendVideoClientLog('video_message_failed', error && error.message || 'message failed');
        requestKeyframe('video_message_failed');
      });
    };
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
    queuedMessages.forEach((queued) => {
      handleVideoSocketMessage(queued).catch((error) => {
        sendVideoClientLog('video_message_failed', error && error.message || 'queued message failed');
      });
    });
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

  function sendVideoSocketClientLog(event, detail) {
    clientLog(event, safeString(detail).slice(0, 500));
  }

  function requestKeyframe(reason, force) {
    const now = performance.now();
    if (!force && now - lastKeyframeCommandAt < keyframeCommandMinIntervalMs) return false;
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
    if (!force && now - lastRecoveryKeyframeAt < minIntervalMs) return false;
    if (!requestKeyframe(reason, force)) return false;
    lastRecoveryKeyframeAt = now;
    return true;
  }

  function requestServerRecoveryDebounced(reason, force) {
    const now = performance.now();
    if (!force && now - lastRecoveryServerRecoverAt < recoveryServerRecoverDebounceMs) return false;
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
      }).slice(0, 600),
      correlationId: activeResumeFlow ? activeResumeFlow.id : ''
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
    if (decoder) {
      try { decoder.close(); } catch (_) {}
      decoder = null;
    }
    decoderConfigured = false;
  }

  function resetDecoderForRecovery(reason) {
    if (!lastDecoderConfig) return false;
    const now = performance.now();
    if (now - lastRecoveryDecoderResetAt < recoveryDecoderResetDebounceMs) return false;
    lastRecoveryDecoderResetAt = now;
    preserveCurrentFrame(`decoder_recovery:${reason || 'unknown'}`);
    sendVideoClientLog('h264_decoder_recovery_reset', reason);
    configureDecoder(lastDecoderConfig, { preserveFrame: true, preserveSequence: true, requestReason: reason, preferAvc: decoderMode === 'avc' })
      .catch((error) => sendVideoClientLog('decoder_recovery_config_failed', error && error.message || 'decoder recovery failed'));
    return true;
  }

  function publishStreamDebug() {
    window.ticketStreamDebug = {
      pageVersion,
      configured,
      streamReady: document.body.dataset.streamReady,
      transport: 'https-websocket-h264',
      codec: decoderConfigured ? 'h264' : '',
      decoderMode,
      currentStreamEpoch,
      lastPacketAt,
      lastDecodedFrameAt,
      lastPacketSequence,
      lastAcceptedFrameSequence,
      lastAcceptedFrameTimestamp,
      lastRenderedFrameEpoch,
      lastRenderedFrameSequence,
      lastRenderedFrameTimestamp,
      needsKeyFrame,
      firstFrameReceived,
      hasRenderedFrame,
      hasFallbackFrame: fallbackFrameAvailable,
      lastFallbackFrameAt,
      streamResumeSpinnerVisible: streamResumeSpinnerVisible(),
      latestStreamStatus,
      controlCodeCapture: lastControlCodeCaptureDebug
    };
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
    if (frame.sequence && frame.sequence <= lastAcceptedFrameSequence) {
      return false;
    }
    if (needsKeyFrame && frame.kind !== 'key') {
      return false;
    }
    if (frame.kind === 'key') needsKeyFrame = false;
    if (frame.sequence) lastAcceptedFrameSequence = frame.sequence;
    if (frame.timestamp) lastAcceptedFrameTimestamp = frame.timestamp;
    const captureWallMillis = frame.timestamp ? frame.timestamp / 1000 : 0;
    lastAcceptedFrameReceivedAt = now;
    lastAcceptedFrameVisualAgeMillis = captureWallMillis > 0 ? Math.max(0, Date.now() - captureWallMillis) : 0;
    return true;
  }

  function queueFrameMetadata(frame) {
    pendingFrameMetadata.push({
      epoch: Number(frame && frame.epoch || currentStreamEpoch || 0),
      sequence: Number(frame && frame.sequence || 0),
      timestamp: Number(frame && frame.timestamp || 0),
      receivedAt: lastAcceptedFrameReceivedAt,
      queuedAt: performance.now()
    });
    if (pendingFrameMetadata.length > 120) pendingFrameMetadata.splice(0, pendingFrameMetadata.length - 120);
  }

  function shiftFrameMetadata() {
    return pendingFrameMetadata.shift() || {
      epoch: currentStreamEpoch,
      sequence: lastAcceptedFrameSequence,
      timestamp: lastAcceptedFrameTimestamp,
      receivedAt: lastAcceptedFrameReceivedAt,
      queuedAt: lastAcceptedFrameQueuedAt
    };
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
    if (!acceptFreshFrame(frame)) return;
    lastAcceptedFrameQueuedAt = performance.now();
    if (decoderMode === 'avc') {
      decodeAvcFrame(frame);
      return;
    }
    try {
      decoder.decode(new EncodedVideoChunk({ type: frame.kind, timestamp: frame.timestamp, data: frame.data }));
      queueFrameMetadata(frame);
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
      decoder.decode(new EncodedVideoChunk({ type: frame.kind, timestamp: frame.timestamp, data: converted.sample }));
      queueFrameMetadata(frame);
    } catch (error) {
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

  function renderDecodedFrame(frame, source) {
    const frameMetadata = shiftFrameMetadata();
    try {
      ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
      lastFrameAt = performance.now();
      lastDecodedFrameAt = lastFrameAt;
      lastRenderedFrameReceivedAt = lastAcceptedFrameReceivedAt;
      lastRenderedFrameQueuedAt = lastAcceptedFrameQueuedAt;
      lastRenderedFrameRenderedAt = lastFrameAt;
      lastRenderedFrameVisualAgeMillis = lastAcceptedFrameVisualAgeMillis + Math.max(0, lastFrameAt - lastAcceptedFrameReceivedAt);
      lastRenderedFrameEpoch = Number(frameMetadata.epoch || 0);
      lastRenderedFrameSequence = Number(frameMetadata.sequence || 0);
      lastRenderedFrameTimestamp = Number(frameMetadata.timestamp || 0);
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
        sendVideoSocketClientLog('stream_first_rendered_frame', firstFrameDetail);
      }
      finishStreamOpenMetric('first_fresh_frame', true, firstFrameDetail);
      maybePrepareControlCodeResultFrame();
      maybeCaptureControlCodeResultImage();
      hideEmpty();
      updateStreamFreshnessStatus('frame_rendered');
      updateControlCodeSubmitAvailability();
      publishStreamDebug();
    } catch (error) {
      sendVideoClientLog('decoded_frame_render_failed', `${source || 'decoder'}:${error && error.message || 'draw failed'}`);
      needsKeyFrame = true;
      preserveCurrentFrame('decoded_frame_render_failed');
      showStreamRecovery();
      requestKeyframe('decoded_frame_render_failed');
    } finally {
      try { frame.close(); } catch (_) {}
    }
  }

  async function configureDecoder(config, options) {
    options = options || {};
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
    const decoderConfig = { codec, codedWidth: width, codedHeight: height, avc: { format: 'annexb' } };
    let supported = false;
    if (!preferAvc) {
      try {
        const result = await VideoDecoder.isConfigSupported(decoderConfig);
        supported = Boolean(result && result.supported);
      } catch (error) {
        supported = false;
      }
    }
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
    lastDecoderConfig = { ...config };
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
    streamSize = { width, height };
    currentStreamEpoch = Number(config.streamEpoch || 0);
    lastAcceptedFrameSequence = options.preserveSequence ? previousSequence : 0;
    lastAcceptedFrameTimestamp = options.preserveSequence ? previousTimestamp : 0;
    pendingFrameMetadata = [];
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
    decoder = new VideoDecoder({
      output: (frame) => {
        renderDecodedFrame(frame, 'annexb');
      },
      error: (error) => {
        sendVideoClientLog('decoder_error', error && error.message || 'decoder error');
        needsKeyFrame = true;
        switchToAvcAdapter('decoder_error');
      }
    });
    decoder.configure(decoderConfig);
    decoderConfigured = true;
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
    decoder = new VideoDecoder({
      output: (frame) => {
        renderDecodedFrame(frame, 'avc');
      },
      error: (error) => {
        sendVideoClientLog('decoder_error', error && error.message || 'decoder error');
        needsKeyFrame = true;
        resetDecoderForRecovery('decoder_error_avc');
        requestKeyframe('decoder_error_avc');
      }
    });
    try {
      decoder.configure({ codec, codedWidth: width, codedHeight: height, description });
    } catch (error) {
      closeDecoder();
      showUnsupported('Šī pārlūkprogramma nevar atvērt H.264 biļetes video.');
      sendVideoClientLog('h264_avc_config_failed', error && error.message || 'avc config failed');
      return;
    }
    decoderConfigured = true;
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
	    if (!usesDirectSpacetimeAuth()) {
	      throw new Error('Direct SpacetimeAuth is disabled for this ticket session.');
	    }
	    const response = await fetch('/api/v1/auth/session', { cache: 'no-store' });
	    const payload = await response.json().catch(() => ({}));
	    if (payload && payload.authenticated && payload.spacetime && payload.spacetime.authRequired) {
	      beginSpacetimeLogin(authReturnTarget());
	      throw new Error('Direct SpacetimeAuth session refresh required.');
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

  async function connectSpacetimeState() {
    if (idleDisconnected) return;
    if (!usesDirectSpacetimeAuth() || spacetimeClient || spacetimeDirectUnavailable) return;
    if (spacetimeClientConnectPromise) return spacetimeClientConnectPromise;
    spacetimeClientConnectPromise = (async () => {
      if (idleDisconnected || !usesDirectSpacetimeAuth() || spacetimeClient || spacetimeDirectUnavailable) return;
      let token = '';
      try {
        await loadSpacetimeClientScript();
        token = await spacetimeToken();
      } catch (error) {
        spacetimeDirectUnavailable = true;
        if (!spacetimeDirectUnavailableLogged) {
          spacetimeDirectUnavailableLogged = true;
          clientLog('spacetime_direct_unavailable', error && error.message);
        }
        return;
      }
      if (spacetimeClient) return;
      const st = cfg.spacetime || {};
      spacetimeClient = window.TicketSpacetime.create({
        host: st.host || 'https://maincloud.spacetimedb.com',
        database: st.database || '',
        token,
        ticketId: cfg.ticketId || 'vivi-default',
        sessionId: cfg.sessionId || '',
        email: cfg.email || ''
      }, {
        onState: (state) => {
          currentState = state;
          rememberServerClock(currentState);
          renderState();
        },
        onStatus: (status, detail) => {
          spacetimeClientStatus = status;
          if (status === 'live') {
            flushClientLogs();
          }
          updateControlCodeSubmitAvailability();
          if (detail) clientLog('spacetime_client_status', `${status}:${detail}`);
        }
      });
      spacetimeClient.connect();
    })();
    try {
      await spacetimeClientConnectPromise;
    } finally {
      spacetimeClientConnectPromise = null;
    }
  }

  async function runSpacetimeMutation(action, reason) {
    await connectSpacetimeState();
    if (!spacetimeClient) throw new Error('Spacetime connection is unavailable.');
    await action(spacetimeClient);
    flushClientLogs();
  }

  function publishStreamFocus(active, reason) {
    runSpacetimeMutation((client) => client.setStreamFocus(Boolean(active), reason || (active ? 'browser_visible' : 'browser_hidden')), reason || 'stream_focus')
      .catch((error) => clientLog('stream_focus_failed', `${reason || 'focus'}:${error && error.message || 'failed'}`));
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
    codeResultArea.hidden = !visible;
    document.body.classList.toggle('control-code-result-visible', Boolean(visible));
    updateControlCodeSubmitAvailability();
  }

  function clearControlCodeResultCapture() {
    if (controlCodeResultCaptureTimer) {
      clearTimeout(controlCodeResultCaptureTimer);
      controlCodeResultCaptureTimer = null;
    }
    controlCodeResultCaptureRequestID = '';
    controlCodeResultCapturedRequestID = '';
    controlCodeCaptureAckInFlightRequestID = '';
    pendingControlCodeBaselineFrameFingerprint = null;
    controlCodeBaselineFrameFingerprint = null;
    controlCodeBaselineRequestID = '';
    lastControlCodeCaptureDebug = null;
    lastControlCodeCaptureKeyframeRequestAt = 0;
    lastControlCodeCaptureKeyframeRetryCount = 0;
    resetControlCodeSafeGeneratedFrame('clear_capture');
    clearControlCodeFrozenCandidateFrame();
    clearControlCodePreparedCapture();
    codeResultImage.hidden = true;
    codeResultImage.removeAttribute('src');
    publishStreamDebug();
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
    const keyboard = canvasRegionFingerprint({
      x: 0.08,
      y: 0.62,
      width: 0.84,
      height: 0.34
    });
    const dialog = canvasRegionFingerprint({
      x: 0.16,
      y: 0.38,
      width: 0.68,
      height: 0.22
    });
    const dialogUpper = canvasRegionFingerprint({
      x: 0.16,
      y: 0.30,
      width: 0.68,
      height: 0.22
    });
    const inputLine = canvasRegionFingerprint({
      x: 0.24,
      y: 0.52,
      width: 0.52,
      height: 0.045
    });
    const inputLineUpper = canvasRegionFingerprint({
      x: 0.24,
      y: 0.41,
      width: 0.52,
      height: 0.045
    });
    const dimOverlay = canvasRegionFingerprint({
      x: 0.08,
      y: 0.30,
      width: 0.84,
      height: 0.44
    });
    const okButton = canvasRegionFingerprint({
      x: 0.64,
      y: 0.51,
      width: 0.18,
      height: 0.07
    });
    const okButtonUpper = canvasRegionFingerprint({
      x: 0.64,
      y: 0.43,
      width: 0.18,
      height: 0.07
    });
    const keyboardVisible = Boolean(keyboard &&
      keyboard.lightCellRatio >= 0.58 &&
      keyboard.mean >= 150 &&
      keyboard.contrastScore <= 95);
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
    const okButtonLowerOrangeRatio = regionOrangeCellRatio({
      x: 0.64,
      y: 0.51,
      width: 0.18,
      height: 0.07
    });
    const okButtonUpperOrangeRatio = regionOrangeCellRatio({
      x: 0.64,
      y: 0.43,
      width: 0.18,
      height: 0.07
    });
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
    const popupVisible = dialogVisible && (okButtonVisible || inputLineVisible);
    const popupKeyboardVisible = dialogVisible && keyboardVisible;
    return {
      keyboardVisible: popupKeyboardVisible,
      popupVisible: dialogVisible && (okButtonVisible || inputLineVisible),
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
      popupInputLineDarkCellRatio: inputLine ? Math.round(Number(inputLine.darkCellRatio || 0) * 100) / 100 : 0,
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

  function sampleControlCodeResultChipRegion(yRatio) {
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
    try {
      imageData = ctx.getImageData(x, y, Math.min(width, canvas.width - x), Math.min(height, canvas.height - y));
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
    for (let row = 0; row < rows; row++) {
      let rowDark = 0;
      for (let col = 0; col < cols; col++) {
        const px = Math.max(0, Math.min(sampleWidth - 1, Math.round((col + 0.5) * sampleWidth / cols)));
        const py = Math.max(0, Math.min(sampleHeight - 1, Math.round((row + 0.5) * sampleHeight / rows)));
        const offset = (py * sampleWidth + px) * 4;
        const red = data[offset] || 0;
        const green = data[offset + 1] || 0;
        const blue = data[offset + 2] || 0;
        const luminance = Math.round((red * 299 + green * 587 + blue * 114) / 1000);
        if (luminance <= 80) {
          dark++;
          rowDark++;
        }
        if (luminance >= 175) {
          light++;
        }
        sampled++;
      }
      if (rowDark >= 32) {
        chipRows++;
      }
    }
    const chipDarkRatio = sampled ? dark / sampled : 0;
    const chipLightRatio = sampled ? light / sampled : 0;
    const chipScore = Math.max(0, (chipRows * 10) + (chipDarkRatio * 80) - (chipLightRatio * 20));
    return {
      chipVisible: chipRows >= 4 && chipDarkRatio >= 0.34 && chipLightRatio <= 0.62 && chipScore >= 34,
      chipDarkRatio: Math.round(chipDarkRatio * 100) / 100,
      chipLightRatio: Math.round(chipLightRatio * 100) / 100,
      chipRows,
      chipY: Math.round(Number(yRatio || 0) * 1000) / 1000,
      chipScore: Math.round(chipScore * 10) / 10
    };
  }

  function controlCodeResultChipProof() {
    let bestChip = emptyControlCodeResultChipProof();
    for (let yRatio = controlCodeGeneratedChipScanStartY; yRatio <= controlCodeGeneratedChipScanEndY + 0.0001; yRatio += controlCodeGeneratedChipScanStepY) {
      const candidate = sampleControlCodeResultChipRegion(yRatio);
      if (!bestChip || candidate.chipScore > bestChip.chipScore) {
        bestChip = candidate;
      }
    }
    return bestChip;
  }

  function controlCodeGeneratedFrameProof() {
    const chip = controlCodeResultChipProof();
    const resultBar = canvasRegionFingerprint({
      x: 0.14,
      y: chip.chipY || 0.55,
      width: 0.72,
      height: 0.06
    });
    const codeArea = canvasRegionFingerprint({
      x: 0.18,
      y: Math.max(0.12, chip.chipY - 0.34),
      width: 0.64,
      height: 0.30
    });
    const generatedBarVisible = Boolean(resultBar &&
      Number(resultBar.darkCellRatio || 0) >= 0.24 &&
      Number(resultBar.lightCellRatio || 0) >= 0.22 &&
      Number(resultBar.contrastScore || 0) >= 60);
    const generatedCodeVisible = Boolean(codeArea &&
      Number(codeArea.darkCellRatio || 0) >= 0.06 &&
      Number(codeArea.lightCellRatio || 0) >= 0.18 &&
      Number(codeArea.contrastScore || 0) >= 42);
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

  function controlCodeTrustedPhonePostSubmitProof(resultProof) {
    resultProof = String(resultProof || '').trim();
    return resultProof === 'phone_visual_root_confirmed' ||
      resultProof === 'phone_visual';
  }

  const controlCodeSafeGeneratedFrameRequiredCount = 1;
  const controlCodeTrustedProofSafeGeneratedFrameRequiredCount = 1;

  function beginControlCodeMetric(digitCount) {
    activeControlCodeMetric = null;
    return null;
  }

  function noteControlCodeMetricPhase(phase, request, ok, detail) {
    return;
  }

  function noteControlCodeRequestMetric(request) {
    return;
  }

  function finishControlCodeMetric(outcome, ok, detail) {
    activeControlCodeMetric = null;
  }

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
    controlCodePreparedCaptureProof = null;
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
    const options = arguments.length > 1 ? arguments[1] : null;
    const allowProvisional = Boolean(options && options.allowProvisional);
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
    if (!request || (!allowProvisional && request.status !== 'succeeded')) {
      proof.candidateRejectedReason = 'request_not_succeeded';
      return proof;
    }
    if (allowProvisional && request.status !== 'running' && request.status !== 'succeeded') {
      proof.candidateRejectedReason = 'request_not_running';
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
    if ((!markerEpoch || !markerSequence) && !allowProvisional) {
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
    const trustedPhonePostSubmitProof = controlCodeTrustedPhonePostSubmitProof(proof.resultProof);
    proof.fingerprintDifferenceScore = Math.round(Number(difference.score || 0) * 10) / 10;
    proof.fingerprintChangedCells = Number(difference.changedCells || 0);
    const popupProof = controlCodePopupFrameProof();
    proof.popupKeyboardVisible = popupProof.keyboardVisible;
    proof.popupVisible = popupProof.popupVisible;
    proof.popupGhostVisible = popupProof.dialogGhostVisible;
    proof.dimOverlayVisible = popupProof.dimOverlayVisible;
    proof.unsafeOverlayVisible = popupProof.unsafeOverlayVisible;
    proof.popupLightCellRatio = popupProof.popupLightCellRatio;
    proof.popupDarkCellRatio = popupProof.popupDarkCellRatio;
    proof.popupContrastScore = popupProof.popupContrastScore;
    proof.dimOverlayMean = popupProof.dimOverlayMean;
    proof.dimOverlayContrastScore = popupProof.dimOverlayContrastScore;
    proof.keyboardLightCellRatio = popupProof.keyboardLightCellRatio;
    proof.keyboardMean = popupProof.keyboardMean;
    proof.keyboardContrastScore = popupProof.keyboardContrastScore;
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
    proof.generatedVisible = generatedProof.generatedVisible;
    proof.generatedChipVisible = generatedProof.generatedChipVisible;
    proof.generatedChipDarkRatio = generatedProof.generatedChipDarkRatio;
    proof.generatedChipLightRatio = generatedProof.generatedChipLightRatio;
    proof.generatedChipRows = generatedProof.generatedChipRows;
    proof.generatedChipY = generatedProof.generatedChipY;
    proof.generatedChipScore = generatedProof.generatedChipScore;
    proof.generatedBarVisible = generatedProof.generatedBarVisible;
    proof.generatedCodeVisible = generatedProof.generatedCodeVisible;
    proof.generatedBarDarkCellRatio = generatedProof.generatedBarDarkCellRatio;
    proof.generatedBarLightCellRatio = generatedProof.generatedBarLightCellRatio;
    proof.generatedBarContrastScore = generatedProof.generatedBarContrastScore;
    proof.generatedCodeDarkCellRatio = generatedProof.generatedCodeDarkCellRatio;
    proof.generatedCodeLightCellRatio = generatedProof.generatedCodeLightCellRatio;
    proof.generatedCodeContrastScore = generatedProof.generatedCodeContrastScore;
    proof.generatedCodeScore = generatedProof.generatedCodeScore;
    const browserTrustedGeneratedVisible = generatedProof.generatedVisible ||
      Boolean(trustedPhonePostSubmitProof &&
        generatedProof.generatedChipVisible &&
        (proof.fingerprintDifferenceScore >= controlCodeFingerprintDifferenceThreshold ||
          proof.fingerprintChangedCells >= controlCodeFingerprintChangedCellsThreshold));
    const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof &&
      markerEpoch &&
      markerSequence &&
      renderedEpoch === markerEpoch &&
      renderedSequence >= markerSequence &&
      (request.status === 'succeeded' || allowProvisional));
    proof.trustedPhoneMarkerFrame = trustedPhoneMarkerFrame;
    proof.browserTrustedGeneratedVisible = browserTrustedGeneratedVisible;
    if (!browserTrustedGeneratedVisible && trustedPhoneMarkerFrame) {
      proof.generatedMarkerOnlyRejected = true;
    }
    if (!proof.browserTrustedGeneratedVisible) {
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
    proof.accepted = true;
    proof.candidateAccepted = true;
    proof.candidateRejectedReason = '';
    proof.acceptedReason = markerEpoch && markerSequence
      ? 'candidate_frame_at_or_after_phone_marker_and_generated_visual'
      : 'browser_prepared_generated_frame_before_marker';
    proof.provisional = allowProvisional && (!markerEpoch || !markerSequence || request.status !== 'succeeded');
    return proof;
  }

  function controlCodePreparedProofUsable(request, proof) {
    if (!request || !proof || !proof.accepted) return false;
    const requestID = String(request.requestId || '').trim();
    if (!requestID || String(proof.requestId || '').trim() !== requestID) return false;
    if (!controlCodeFrozenCandidateFrameForProof(proof)) return false;
    if (request.status !== 'succeeded') return false;
    const markerEpoch = Number(request.resultFrameEpoch || request.streamEpoch || 0);
    const markerSequence = Number(request.resultMinFrameSequence || request.minFrameSequence || request.frameSequence || 0);
    if (!markerEpoch || !markerSequence) return false;
    if (Number(proof.candidateFrameEpoch || 0) !== markerEpoch) return false;
    if (Number(proof.candidateFrameSequence || 0) < markerSequence) return false;
    proof.markerEpoch = markerEpoch;
    proof.markerSequence = markerSequence;
    proof.acceptedReason = 'candidate_frame_at_or_after_phone_marker_and_generated_visual';
    proof.provisional = false;
    return true;
  }

  function displayControlCodeResultImage(requestID, proof, capturedImage, outcome) {
    if (!requestID || !capturedImage) return false;
    codeResultImage.src = capturedImage;
    setControlCodeResultVisible(true);
    codeResultImage.hidden = false;
    codeResultStatus.textContent = '';
    codeResultStatus.hidden = true;
    codeResultValue.hidden = true;
    codeResultValue.textContent = '';
    codeResultValue.style.display = '';
    codeResultTimer.hidden = true;
    codeResultTimer.textContent = '';
    codeResultArea.dataset.status = 'succeeded';
    codeResultArea.style.background = '#000';
    if (controlCodePreparedCaptureDisplayedRequestID !== requestID) {
      controlCodePreparedCaptureDisplayedRequestID = requestID;
      finishControlCodeMetric(outcome || 'browser_capture_displayed', true, {
        candidateFrameEpoch: Number(proof.candidateFrameEpoch || 0),
        candidateFrameSequence: Number(proof.candidateFrameSequence || 0),
        safeGeneratedFrameCount: controlCodeSafeGeneratedFrameCount,
        fingerprintDifferenceScore: proof.fingerprintDifferenceScore,
        fingerprintChangedCells: proof.fingerprintChangedCells,
        provisional: Boolean(proof.provisional)
      });
    }
    lastControlCodeCaptureDebug = Object.assign({}, proof, {
      accepted: proof.accepted,
      candidateAccepted: true,
      fingerprintDifferenceScore: proof.fingerprintDifferenceScore,
      capturedNaturalWidth: canvas.width,
      capturedNaturalHeight: canvas.height,
      controlCodeSafeGeneratedFrameCount,
      controlCodeFrozenFrameKey,
      capturedAt: Date.now()
    });
    publishStreamDebug();
    return true;
  }

  function controlCodeResultDisplayedForRequest(requestID) {
    requestID = String(requestID || '').trim();
    return Boolean(requestID &&
      controlCodePreparedCaptureDisplayedRequestID === requestID &&
      !codeResultArea.hidden &&
      !codeResultImage.hidden &&
      Boolean(codeResultImage.currentSrc || codeResultImage.src));
  }

  function maybePrepareControlCodeResultFrame() {
    if (!codeRequest || codeRequest.status !== 'running') return false;
    const requestID = String(codeRequest.requestId || '').trim();
    if (!requestID || requestID.startsWith('pending:')) return false;
    if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
    if (controlCodePreparedCaptureProof &&
      String(controlCodePreparedCaptureProof.requestId || '').trim() === requestID &&
      controlCodeFrozenCandidateFrameForProof(controlCodePreparedCaptureProof)) {
      return true;
    }
    const proof = controlCodeCandidateFrameProof(codeRequest, { allowProvisional: true });
    if (!proof.accepted) {
      if (proof.candidateRejectedReason === 'generated_frame_not_visible' ||
        proof.candidateRejectedReason === 'candidate_matches_pre_request_frame') {
        lastControlCodeCaptureDebug = Object.assign({}, proof, {
          accepted: false,
          candidateAccepted: false,
          preparedAt: Date.now()
        });
      }
      return false;
    }
    controlCodePreparedCaptureProof = Object.assign({}, proof, {
      preparedAt: Date.now()
    });
    const capturedImage = captureControlCodeResultImage(proof);
    if (capturedImage) {
      displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');
    }
    return true;
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
    publishStreamDebug();
  }

  function noteControlCodeCandidateRejected(proof) {
    proof = proof || {};
    const now = performance.now();
    const reason = String(proof.candidateRejectedReason || proof.reason || 'candidate_rejected');
    if (
      lastControlCodeCaptureKeyframeRetryCount < controlCodeCaptureKeyframeRetryLimit &&
      now - lastControlCodeCaptureKeyframeRequestAt >= controlCodeCaptureKeyframeRetryMs
    ) {
      lastControlCodeCaptureKeyframeRequestAt = now;
      if (requestKeyframeDebounced(`control_code_candidate_rejected_${reason}`, controlCodeCaptureKeyframeRetryMs)) {
        lastControlCodeCaptureKeyframeRetryCount += 1;
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

  async function captureControlCodeResultScreenshot(request, proof) {
    if (!request || !hasRenderedFrame || !canvas.width || !canvas.height) return false;
    if (!proof || !proof.accepted) return false;
    const requestID = String(request.requestId || '').trim();
    if (!requestID) return false;
    if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
    try {
      if (!controlCodeFrozenCandidateFrameForProof(proof)) return false;
      const capturedImage = captureControlCodeResultImage(proof);
      if (!capturedImage) return false;
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');
      controlCodeCaptureAckInFlightRequestID = requestID;
      try {
        await confirmControlCodeBrowserCapture(request, proof);
        controlCodeResultCapturedRequestID = requestID;
      } finally {
        if (controlCodeCaptureAckInFlightRequestID === requestID) {
          controlCodeCaptureAckInFlightRequestID = '';
        }
      }
      if (!codeRequest || String(codeRequest.requestId || '').trim() !== requestID || codeRequest.status !== 'succeeded') {
        return false;
      }
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      return true;
    } catch (error) {
      if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;
      reportClientFault('control_code_browser_capture_failed', error);
      if (controlCodePreparedCaptureDisplayedRequestID !== requestID) {
        finishControlCodeMetric('browser_capture_failed', false, {
          error: error && error.message || 'capture failed'
        });
        failControlCodeResultScreenshotWait();
      }
      return false;
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
    finishControlCodeMetric('browser_capture_wait_failed', false, {
      requestKey: codeRequest && codeRequest.requestId ? accountPublicId(String(codeRequest.requestId)) : ''
    });
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
    if (controlCodePreparedProofUsable(codeRequest, controlCodePreparedCaptureProof)) {
      if (controlCodeResultCaptureTimer) {
        clearTimeout(controlCodeResultCaptureTimer);
        controlCodeResultCaptureTimer = null;
      }
      controlCodeResultCaptureRequestID = '';
      captureControlCodeResultScreenshot(codeRequest, controlCodePreparedCaptureProof);
      return true;
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

  function waitForControlCodeResultScreenshot(request) {
    const requestID = String(request && request.requestId || '').trim();
    if (!requestID) return;
    if (controlCodeResultCapturedRequestID === requestID) return;
    if (locallyClosedControlCodeRequestIDs.has(requestID)) return;
    const resultAlreadyDisplayed = controlCodePreparedCaptureDisplayedRequestID === requestID &&
      !codeResultArea.hidden &&
      !codeResultImage.hidden &&
      Boolean(codeResultImage.currentSrc || codeResultImage.src);
    if (controlCodeResultCaptureRequestID !== requestID) {
      if (controlCodeResultCaptureTimer) clearTimeout(controlCodeResultCaptureTimer);
      controlCodeResultCaptureTimer = null;
      controlCodeResultCaptureRequestID = requestID;
      if (!resultAlreadyDisplayed) {
        codeResultImage.hidden = true;
        codeResultImage.removeAttribute('src');
      }
    }
    if (resultAlreadyDisplayed) {
      codeResultArea.dataset.status = 'succeeded';
      codeResultArea.style.background = '#000';
      codeResultStatus.hidden = true;
      codeResultStatus.textContent = '';
      codeResultValue.hidden = true;
      codeResultValue.textContent = '';
      codeResultValue.style.display = '';
      codeResultTimer.hidden = true;
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
    requestKeyframeDebounced('control_code_result_wait_start', controlCodeCaptureKeyframeRetryMs);
    if (maybeCaptureControlCodeResultImage()) return;
    const tick = () => {
      if (!codeRequest || codeRequest.requestId !== requestID || codeRequest.status !== 'succeeded') {
        if (controlCodeResultCaptureTimer) clearTimeout(controlCodeResultCaptureTimer);
        controlCodeResultCaptureTimer = null;
        controlCodeResultCaptureRequestID = '';
        return;
      }
      if (maybeCaptureControlCodeResultImage()) return;
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
    codeRequest = request || codeRequest;
    const current = codeRequest;
    const currentRequestID = String(current && current.requestId || '').trim();
    const busy = current && (current.status === 'queued' || current.status === 'running');
    codeRequestState.textContent = controlCodeStatusText(current && current.status, current && current.reason);
    codeRequestDetail.textContent = controlCodeDetailText(current);
    updateControlCodeSubmitAvailability();
    if (requestID && !requestID.startsWith('pending:') && current && (busy || current.status === 'succeeded')) {
      rememberControlCodeBaselineFrame(requestID);
    }
    if (busy) {
      keepControlCodeVideoAlive('control_code_request_active');
      if (current.status === 'running') {
        requestKeyframeDebounced('control_code_running', controlCodeCaptureKeyframeRetryMs);
        maybePrepareControlCodeResultFrame();
      }
      if (controlCodeResultDisplayedForRequest(currentRequestID)) {
        scheduleControlCodeTicker(current);
        return;
      }
    }
    if (current) {
      noteControlCodeRequestMetric(current);
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
      finishControlCodeMetric('request_failed', false, {
        reason: String(current.reason || current.message || 'failed')
      });
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
    document.body.classList.toggle('details-visible', Boolean(visible));
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
    if (!streamReadyForControlCode()) {
      codeError.textContent = '';
      setStatus(liveFrameReadyForControlCode()
        ? 'Savienojas ar vadības kanālu pirms koda pieprasījuma.'
        : 'Gaida svaigu tiešraides kadru pirms koda pieprasījuma.');
      refreshControlCodeReadiness('control_code_wait_for_ready');
      return;
    }
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
    runSpacetimeMutation((client) => client.prepareControlCode('dialog_open'), 'control_code_prepare')
      .then(() => clientLog('control_code_prepare_complete', 'spacetime'))
      .catch((error) => clientLog('control_code_prepare_failed', error && error.message || 'prepare failed'));
    setTimeout(() => {
      updateViewportVars();
      codeDigits.focus({ preventScroll: true });
    }, 30);
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

  async function submitControlCodeRequest() {
    const digits = sanitizeControlDigits(codeDigits.value);
    codeDigits.value = digits;
    if (digits.length < 2 || digits.length > 8) {
      codeError.textContent = 'Ievadi 2-8 ciparus.';
      return;
    }
    if (!streamReadyForControlCode()) {
      codeError.textContent = liveFrameReadyForControlCode()
        ? 'Pagaidi, līdz vadības savienojums ir gatavs.'
        : 'Pagaidi, līdz tiešraides kadrs atkal ir svaigs.';
      refreshControlCodeReadiness('control_code_submit_wait_for_ready');
      return;
    }
    codeError.textContent = '';
    codeSubmit.disabled = true;
    pendingControlCodeBaselineFrameFingerprint = canvasRegionFingerprint(controlCodeFingerprintRegion());
    beginControlCodeMetric(digits.length);
    const submittedAt = performance.now();
    try {
      await runSpacetimeMutation((client) => client.requestControlCode(digits), 'control_code_request');
      const mutationLatencyMs = Math.round(performance.now() - submittedAt);
      noteControlCodeMetricPhase('request_mutation_complete', null, true, {
        mutationLatencyMs
      });
      requestKeyframeDebounced('control_code_request_submitted', 0, true);
      clientLog('control_code_submitted', JSON.stringify({
        digitCount: digits.length,
        mutationLatencyMs,
        viewportHeight: window.innerHeight,
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
      finishControlCodeMetric('request_submit_failed', false, {
        error: error && error.message || 'request failed'
      });
      codeError.textContent = localizePublicMessage(error && error.message || 'Pieprasījums neizdevās');
    } finally {
      updateControlCodeSubmitAvailability();
    }
  }

  async function closeCurrentControlCode(openNext) {
    const requestID = codeRequest && codeRequest.requestId;
    if (requestID) {
      if (!ownedControlCodeRequestIDs.has(String(requestID))) {
        clientLog('control_code_close_ignored', 'not_owned');
        setControlCodeResultVisible(false);
        return;
      }
      locallyClosedControlCodeRequestIDs.add(String(requestID));
      setControlCodeResultVisible(false);
      clearControlCodeResultCapture();
      scheduleControlCodeTicker(null);
      if (codeRequest && String(codeRequest.requestId || '').trim() === String(requestID)) {
        codeRequest = null;
      }
      finishControlCodeMetric('closed_by_browser', false, {
        requestKey: accountPublicId(String(requestID))
      });
      try {
        await runSpacetimeMutation((client) => client.closeControlCode(requestID, 'browser_closed'), 'control_code_close');
      } catch (error) {
        clientLog('control_code_close_failed', error && error.message || 'close failed');
      }
    } else {
      setControlCodeResultVisible(false);
    }
    if (openNext) openControlCodeDialog();
  }

  function requestControlCodeFromHotspot(event) {
    if (event) {
      event.preventDefault();
      event.stopPropagation();
    }
    const busy = codeRequest && (codeRequest.status === 'queued' || codeRequest.status === 'running');
    if (busy || codeDialogOpen) return;
    if (!codeResultArea.hidden && codeRequest) {
      closeCurrentControlCode(false);
      return;
    }
    if (!streamReadyForControlCode()) {
      setStatus(liveFrameReadyForControlCode()
        ? 'Savienojas ar vadības kanālu pirms koda pieprasījuma.'
        : 'Gaida svaigu tiešraides kadru pirms koda pieprasījuma.');
      if (!liveFrameReadyForControlCode()) reconnectVideoForRecovery('control_code_hotspot_wait_for_live_frame');
      refreshControlCodeReadiness('control_code_hotspot_wait_for_ready');
      return;
    }
    openControlCodeDialog();
  }

  function closeControlCodeFromHotspot(event) {
    if (event) {
      event.preventDefault();
      event.stopPropagation();
    }
    if (codeDialogOpen || codeResultArea.hidden) return;
    closeCurrentControlCode(false);
  }

  function configureStreamInfo(config) {
    if (config.width && config.height && !configured) {
      preserveCurrentFrame('configure_stream_info');
      canvas.width = config.width;
      canvas.height = config.height;
      redrawPreservedFrame();
      streamSize = { width: config.width, height: config.height };
      resizeCanvasBox();
    }
    if (config.type === 'config' && videoWs && videoWs.readyState === WebSocket.OPEN) {
      configureDecoder(config).catch((error) => sendVideoClientLog('decoder_config_failed', error && error.message || 'config failed'));
    }
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

  function latestOwnedControlCodeRequest(state) {
    const requests = Array.isArray(state && state.controlCodeRequests) ? state.controlCodeRequests : [];
    return requests
      .filter((request) => isOwnedControlCodeRequest(request) && controlCodeRequestIsStillRelevant(request))
      .sort((a, b) => controlCodeRequestSortTime(b) - controlCodeRequestSortTime(a))[0] || null;
  }

  function renderState() {
    const state = currentState;
    if (!state) return;
    rememberServerClock(state);
    const viewers = activeViewerPresence(state);
    const visibleViewerCount = Number.isFinite(Number(state.viewerCount)) ? Number(state.viewerCount) : viewers.length;
    renderPanelSummary(viewers, visibleViewerCount);
    const relayStatus = relayReportToStreamStatus(state.relayCurrentReport);
    if (relayStatus) handleStreamStatus(relayStatus);
    const ownedRequest = latestOwnedControlCodeRequest(state);
    if (ownedRequest) {
      renderControlCodeRequest(ownedRequest);
    } else {
      renderControlCodeRequest(controlCodeRequestIsStillRelevant(codeRequest) ? codeRequest : null);
    }
    if (!relayStatus || String(relayStatus.streamVerdict || '') === 'live') {
      setStatus('Tiešraide rāda biļeti.');
    }
    renderPresence(viewers, visibleViewerCount);
  }

  function rememberServerClock(state) {
    const parsed = Date.parse(state && state.serverTime);
    if (Number.isFinite(parsed)) {
      serverClockSkewMs = parsed - Date.now();
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

  function renderPanelSummary(viewers, visibleViewerCount) {
    renderViewerSummary(viewers, visibleViewerCount);
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
    presenceState.viewers = active.map((viewer, index) => ({
      key: `${viewer.publicId || viewer.label || 'viewer'}-${index}`,
      label: viewer.label || `Skatītājs ${index + 1}`
    }));
    if (presenceMounted) return;
    presence.textContent = '';
    document.documentElement.dataset.ticketUi = "arrow";
    html`
      <div class="presence-header">
        <span>Skatītāji</span>
        <strong>${() => `${presenceState.visibleViewerCount} lapā`}</strong>
      </div>
      ${() => presenceState.viewers.length ? html`
        <div class="presence-list">
          ${() => presenceState.viewers.map((viewer) => html`
            <div class="presence-item">
              <span class="presence-email">${viewer.label}</span>
              <span class="presence-mark">skatās</span>
            </div>
          `.key(viewer.key))}
        </div>
      ` : ''}
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
  controlCodeCloseHotspot.addEventListener('click', closeControlCodeFromHotspot);
  controlCodeHotspot.addEventListener('touchend', requestControlCodeFromHotspot);
  controlCodeCloseHotspot.addEventListener('touchend', closeControlCodeFromHotspot);
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

  function point(event) {
    const rect = canvas.getBoundingClientRect();
    const width = canvas.width;
    const height = canvas.height;
    return {
      x: Math.round(((event.clientX - rect.left) / rect.width) * width),
      y: Math.round(((event.clientY - rect.top) / rect.height) * height)
    };
  }

  function releaseCanvasPointer(pointerId) {
    if (typeof canvas.releasePointerCapture !== 'function') return;
    try {
      canvas.releasePointerCapture(pointerId);
    } catch (_) {}
  }

  canvas.addEventListener('pointerdown', (event) => {
    if (!configured) return;
    if (event.button != null && event.button !== 0) return;
    const start = point(event);
    pointerStart = {
      ...start,
      clientX: event.clientX,
      clientY: event.clientY,
      pointerId: event.pointerId,
      pointerType: event.pointerType || 'mouse',
      at: performance.now()
    };
    if (event.pointerType !== 'touch' && event.cancelable) {
      event.preventDefault();
    }
    if (typeof canvas.setPointerCapture === 'function') {
      try {
        canvas.setPointerCapture(event.pointerId);
      } catch (_) {}
    }
  });

  canvas.addEventListener('pointermove', (event) => {
    if (!pointerStart || pointerStart.pointerId !== event.pointerId) return;
    if (pointerStart.pointerType === 'mouse') return;
    const dx = event.clientX - pointerStart.clientX;
    const dy = event.clientY - pointerStart.clientY;
    if (Math.abs(dy) >= streamVerticalPanThresholdPx && Math.abs(dy) > Math.abs(dx) * streamVerticalPanDominance) {
      releaseCanvasPointer(event.pointerId);
      pointerStart = null;
      clientLog('stream_vertical_scroll', 'allowed');
    }
  });

  canvas.addEventListener('pointerup', (event) => {
    if (!pointerStart || !configured) return;
    if (pointerStart.pointerId !== event.pointerId) return;
    const end = point(event);
    const distance = Math.hypot(end.x - pointerStart.x, end.y - pointerStart.y);
    const heldMs = performance.now() - pointerStart.at;
    if (distance < maxTapTravelPx && heldMs <= maxTapDurationMs) {
      event.preventDefault();
      setStatus('Kontroles kodu pieprasi ar pogu zem biļetes.');
    } else {
      if (event.cancelable) event.preventDefault();
      clientLog('blocked_gesture', distance < maxTapTravelPx ? 'long_press' : 'swipe');
    }
    releaseCanvasPointer(event.pointerId);
    pointerStart = null;
  });

  canvas.addEventListener('pointercancel', (event) => {
    if (pointerStart && pointerStart.pointerId === event.pointerId) {
      releaseCanvasPointer(event.pointerId);
    }
    if (!pointerStart || pointerStart.pointerId === event.pointerId) {
      pointerStart = null;
    }
  });
  canvas.addEventListener('dblclick', (event) => event.preventDefault());
  function blockStreamGesture(event) {
    if (event.cancelable) {
      event.preventDefault();
    }
  }
  function blockDoubleTapZoom(event) {
    if (event.changedTouches && event.changedTouches.length > 0) {
      const touch = event.changedTouches[0];
      const now = performance.now();
      const nearLastTouch = now - lastTouchEndAt < doubleTapSuppressMs
        && Math.hypot(touch.clientX - lastTouchEndX, touch.clientY - lastTouchEndY) < doubleTapSuppressPx;
      if (nearLastTouch && event.cancelable) {
        event.preventDefault();
      }
      lastTouchEndAt = now;
      lastTouchEndX = touch.clientX;
      lastTouchEndY = touch.clientY;
    }
  }
  canvas.addEventListener('touchend', blockDoubleTapZoom, { passive: false });
  for (const eventName of ['gesturestart', 'gesturechange', 'gestureend']) {
    canvas.addEventListener(eventName, blockStreamGesture, { passive: false });
    document.addEventListener(eventName, blockStreamGesture, { passive: false });
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
      (document.visibilityState === 'visible' && (typeof document.hasFocus !== 'function' || document.hasFocus()));
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
    const liveLabeled = streamFreshnessState === 'LIVE_FRESH' || streamFreshnessState === 'LIVE_OK';
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

  function updateStreamFreshnessStatus(reason) {
    const freshness = currentRenderedFreshness(performance.now());
    document.body.dataset.streamFreshness = freshness.streamFreshnessState;
    document.body.dataset.streamLive = freshness.liveLabeled ? 'true' : 'false';
	    if (!freshness.liveLabeled && (reason || hasRenderedFrame)) {
	      showStreamResumeSpinner();
	    } else if (freshness.liveLabeled) {
	      hideStreamResumeSpinner();
	      if (activeResumeFlow && !activeResumeFlow.done) {
	        logResumeCheckpoint('activation_resume_fresh_frame', {
	          reason: safeResumeLabel(reason, 'frame_rendered'),
	          result: 'fresh'
	        });
	        finishActivationResumeFlow('fresh_frame');
	      } else if (pendingResumeFreshFrameFlow && !pendingResumeFreshFrameFlow.freshLogged) {
	        enqueueCompletedResumeSafeLog(pendingResumeFreshFrameFlow, 'activation_resume_fresh_frame', resumeDiagnosticSnapshot({
	          reason: safeResumeLabel(reason, 'frame_rendered'),
	          result: 'late_fresh',
	          phase: pendingResumeFreshFrameFlow.phase || 'complete'
	        }));
	        pendingResumeFreshFrameFlow = null;
	      }
	    }
    updateControlCodeSubmitAvailability();
    return freshness;
  }

  function liveFrameReadyForControlCode() {
    const freshness = currentRenderedFreshness(performance.now());
    return Boolean(hasRenderedFrame && freshness.liveLabeled);
  }

  function spacetimeReadyForControlCode() {
    return Boolean(spacetimeClient && spacetimeClientStatus === 'live');
  }

  function streamReadyForControlCode() {
    return liveFrameReadyForControlCode() && spacetimeReadyForControlCode();
  }

  function refreshControlCodeReadiness(reason) {
    if (!liveFrameReadyForControlCode()) {
      connectDirectVideo();
      requestServerRecoveryDebounced(reason || 'control_code_wait_for_live_frame');
    }
    if (!spacetimeReadyForControlCode()) {
      connectSpacetimeState().catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
    }
    updateControlCodeSubmitAvailability();
  }

  function maybeAutoPrepareControlCode(reason) {
    if (document.visibilityState === 'hidden') return;
    if (codeDialogOpen || !codeResultArea.hidden) return;
    if (controlCodeAutoPrepareInFlight || !streamReadyForControlCode()) return;
    const busy = codeRequest && (codeRequest.status === 'queued' || codeRequest.status === 'running');
    if (busy) return;
    const now = performance.now();
    if (lastControlCodeAutoPrepareAt && now - lastControlCodeAutoPrepareAt < controlCodeAutoPrepareMinIntervalMs) return;
    lastControlCodeAutoPrepareAt = now;
    controlCodeAutoPrepareInFlight = true;
    runSpacetimeMutation((client) => client.prepareControlCode(reason || 'page_ready_control_code'), 'control_code_auto_prepare')
      .then(() => clientLog('control_code_auto_prepare_complete', reason || 'page_ready_control_code'))
      .catch((error) => clientLog('control_code_auto_prepare_failed', error && error.message || 'prepare failed'))
      .finally(() => {
        controlCodeAutoPrepareInFlight = false;
      });
  }

  function updateControlCodeSubmitAvailability() {
    const busy = codeRequest && (codeRequest.status === 'queued' || codeRequest.status === 'running');
    const unavailable = Boolean(busy) || !streamReadyForControlCode();
    codeSubmit.disabled = unavailable || !codeDialogOpen;
    requestCodeButton.disabled = unavailable;
    const hotspotUnavailable = unavailable && codeResultArea.hidden;
    controlCodeHotspot.disabled = hotspotUnavailable;
    controlCodeHotspot.setAttribute('aria-disabled', hotspotUnavailable ? 'true' : 'false');
    if (!unavailable) maybeAutoPrepareControlCode('page_ready_control_code');
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
      if (document.visibilityState === 'hidden') {
        pauseVideoWhileHidden('chase_live_stream_hidden');
      }
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
	    const hiddenPerfMs = lastHiddenAt > 0 ? now - lastHiddenAt : 0;
	    const hiddenWallMs = lastHiddenWallAt > 0 ? Date.now() - lastHiddenWallAt : 0;
	    const hiddenMs = Math.max(hiddenPerfMs, hiddenWallMs);
	    const frameAgeMs = lastFrameAt > 0 ? now - lastFrameAt : null;
	    const longHidden = hiddenMs >= backgroundRecoveryHiddenMs;
	    const oldHiddenTab = hiddenMs >= oldTabFreshResumeHiddenMs;
	    const videoStale = configured && (lastFrameAt === 0 || (frameAgeMs !== null && frameAgeMs > streamStaleVideoReconnectMs));
	    const cacheRestored = reason === 'pageshow_persisted' || (typeof document !== 'undefined' && document.wasDiscarded === true);
	    const connectingTooLong = videoWs && videoWs.readyState === WebSocket.CONNECTING && videoSocketCreatedAt > 0 && now - videoSocketCreatedAt > resumeSoftReconnectMs;
	    const resumeFlow = startActivationResumeFlow(reason || 'visibility_resume', 'visibility_resume', { pauseBurst: true });
	    logResumeCheckpoint('activation_resume_recovery_decision', {
	      reason: safeResumeLabel(reason, 'visibility_resume'),
	      hidden: hiddenDurationBucket(hiddenMs),
	      cache: resumeBooleanLabel(cacheRestored),
	      stale: resumeBooleanLabel(videoStale),
	      connecting: resumeBooleanLabel(connectingTooLong),
	      action: longHidden || oldHiddenTab || cacheRestored || connectingTooLong ? 'cached_restore' : 'watch'
	    }, resumeFlow);
	    lastHiddenAt = 0;
	    lastHiddenWallAt = 0;
    if (longHidden || oldHiddenTab || videoStale || cacheRestored || connectingTooLong) {
      clientLog('visibility_resume_recovery', JSON.stringify({
        reason,
        hiddenMs: Math.round(hiddenMs),
        hiddenPerfMs: Math.round(hiddenPerfMs),
        hiddenWallMs: Math.round(hiddenWallMs),
        oldHiddenTab,
        cacheRestored,
        connectingTooLong,
        configured,
        frameAgeMs: frameAgeMs === null ? null : Math.round(frameAgeMs),
        videoState: videoWs ? videoWs.readyState : -1
      }));
    }
    if (hiddenVideoCloseTimer) {
      clearTimeout(hiddenVideoCloseTimer);
      hiddenVideoCloseTimer = null;
    }
    if (fallbackFrameAvailable) {
      redrawPreservedFrame();
      if (longHidden || videoStale) showStreamRecovery();
    }
    if (screenEngaged) {
      requestScreenWakeLock(reason || 'visibility_visible');
    }
    scheduleFirstScreenPin(false);
    connectSpacetimeState().catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
    publishCurrentStreamFocus(reason || 'visibility_visible');
	    if (longHidden || oldHiddenTab || cacheRestored || connectingTooLong) {
	      restoreCachedVideoForFreshFrame(reason || 'visibility_resume', 'old_tab_resume');
	      runActivationReconnectBurst(reason || 'visibility_resume', resumeFlow);
	      return;
	    }
    if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {
      beginStreamOpenMetric('old_tab_resume', reason || 'visibility_visible', true);
      connectDirectVideo();
      scheduleResumeWatchdogs(reason || 'visibility_visible');
    } else if (videoWs.readyState === WebSocket.OPEN && (longHidden || videoStale)) {
      requestKeyframe('visibility_resume');
    }
    if (videoStale) {
      setTimeout(() => {
        const age = lastFrameAt > 0 ? performance.now() - lastFrameAt : Infinity;
        if (document.visibilityState === 'visible' && configured && age > streamStaleVideoReconnectMs) {
          reconnectVideoForRecovery('visibility_resume_stale');
        }
      }, resumeVideoReconnectDelayMs);
	    }
	    chaseLiveStream();
	    runActivationReconnectBurst(reason || 'visibility_resume', resumeFlow);
	  }

  window.addEventListener('resize', resizeCanvasBox);
  window.addEventListener('scroll', updateDetailsReveal, { passive: true });
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', resizeCanvasBox);
    window.visualViewport.addEventListener('scroll', resizeCanvasBox);
  }
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      noteViewerActivity(null, 'visibility_visible');
      if (hiddenStreamFocusTimer) {
        clearTimeout(hiddenStreamFocusTimer);
        hiddenStreamFocusTimer = null;
      }
      recoverAfterVisibilityResume('visibility_resume');
	    } else if (document.visibilityState === 'hidden') {
	      const flow = startActivationResumeFlow('visibility_hidden', 'visibility_hidden', { pauseBurst: true });
	      logResumeCheckpoint('activation_visibility_hidden', { reason: 'hidden' }, flow);
	      lastHiddenAt = performance.now();
	      lastHiddenWallAt = Date.now();
	      clearResumeWatchdogs();
	      clearActivationReconnectBurst();
	      releaseScreenWakeLock('visibility_hidden');
	      releaseStreamFocusAfterHiddenGrace('visibility_hidden');
	      pauseVideoWhileHidden('visibility_hidden');
	    }
	  });
	  window.addEventListener('pageshow', (event) => {
	    noteViewerActivity(event, 'pageshow');
	    startActivationResumeFlow(event.persisted ? 'pageshow_persisted' : 'pageshow', 'pageshow');
	    if (screenEngaged) {
	      requestScreenWakeLock('pageshow');
	    }
    scheduleFirstScreenPin(true);
    if (event.persisted || lastHiddenAt > 0 || (typeof document !== 'undefined' && document.wasDiscarded === true)) recoverAfterVisibilityResume(event.persisted ? 'pageshow_persisted' : 'pageshow');
    chaseLiveStream();
  });
  window.addEventListener('focus', () => {
    noteViewerActivity(null, 'focus');
    if (idleDisconnected) {
      resumeFromIdleDisconnect('focus');
      return;
	    }
	    chaseLiveStream();
	    publishCurrentStreamFocus('focus');
	    startActivationResumeFlow('focus', 'focus');
	  });
	  window.addEventListener('pagehide', (event) => {
	    const flow = startActivationResumeFlow(event && event.persisted ? 'pagehide_cached' : 'pagehide', 'pagehide', { pauseBurst: true });
	    logResumeCheckpoint('activation_pagehide', {
	      cache: resumeBooleanLabel(Boolean(event && event.persisted))
	    }, flow);
	    closeEarlyVideo('pagehide');
	    clearResumeWatchdogs();
	    clearActivationReconnectBurst();
    if (hiddenStreamFocusTimer) {
      clearTimeout(hiddenStreamFocusTimer);
      hiddenStreamFocusTimer = null;
    }
    lastHiddenAt = performance.now();
    lastHiddenWallAt = Date.now();
    if (event && event.persisted) {
      preserveCurrentFrame('pagehide_cached');
      publishStreamFocus(false, 'pagehide_cached');
      return;
    }
    publishStreamFocus(false, 'pagehide');
    closeDirectVideo();
    if (spacetimeClient && typeof spacetimeClient.disconnectPresence === 'function') {
      spacetimeClient.disconnectPresence();
    }
  });
  window.addEventListener('load', () => scheduleFirstScreenPin(true));
  setInterval(() => {
    if (idleDisconnected) return;
    if (spacetimeClient && typeof spacetimeClient.heartbeat === 'function') {
      const active = currentStreamFocusActive();
      spacetimeClient.heartbeat(active, active ? 'browser_stream_heartbeat' : 'browser_no_stream_heartbeat');
    }
  }, 15000);
  setInterval(chaseLiveStream, 1000);
  updateViewportVars();
  scheduleFirstScreenPin(true);
  updateDetailsReveal();
  resizeCanvasBox();
  scheduleViewerIdleDisconnect('initial_load');
	  showQuietStreamLoading();
	  connectSpacetimeState().catch((error) => clientLog('spacetime_connect_failed', error && error.message));
	  connect();
	  startActivationResumeFlow('initial_load', 'initial_load');

	  async function startAdmin() {
    const memberForm = document.getElementById('memberForm');
    const memberEmail = document.getElementById('memberEmail');
    const memberRole = document.getElementById('memberRole');
    const membersEl = document.getElementById('adminMembers');
    const stateEl = document.getElementById('adminState');
    const notice = document.getElementById('adminNotice');
    const memberSummary = document.getElementById('adminMemberSummary');
    const sessionSummary = document.getElementById('adminSessionSummary');
    const phoneState = document.getElementById('adminPhoneState');
    const phoneDetail = document.getElementById('adminPhoneDetail');
    const streamState = document.getElementById('adminStreamState');
    const streamDetail = document.getElementById('adminStreamDetail');
    const controlState = document.getElementById('adminControlState');
    const controlDetail = document.getElementById('adminControlDetail');
    const safetyState = document.getElementById('adminSafetyState');
    const safetyDetail = document.getElementById('adminSafetyDetail');
    const backendSummary = document.getElementById('adminBackendSummary');
    const backendList = document.getElementById('adminBackendList');
    const ticketSummary = document.getElementById('adminTicketSummary');
    const ticketState = document.getElementById('adminTicketState');
    const ticketDetail = document.getElementById('adminTicketDetail');
    const ticketResult = document.getElementById('adminTicketResult');
    const ticketResultDetail = document.getElementById('adminTicketResultDetail');
    const ticketReselect = document.getElementById('adminTicketReselect');
    const simSetup = document.querySelector('[data-simulator-setup="true"]');
    const simSetupSummary = document.getElementById('simSetupSummary');
    const simSetupPackages = document.getElementById('simSetupPackages');
    const simSetupScreenshot = document.getElementById('simSetupScreenshot');
    const simSetupRefreshButton = document.getElementById('simSetupRefresh');
    const simSetupTextForm = document.getElementById('simSetupTextForm');
    const simSetupText = document.getElementById('simSetupText');
    const simSetupLastInput = document.getElementById('simSetupLastInput');
    const requiredAdminElements = [
      memberForm,
      memberEmail,
      memberRole,
      membersEl,
      stateEl,
      notice,
      memberSummary,
      sessionSummary,
      phoneState,
      phoneDetail,
      streamState,
      streamDetail,
      controlState,
      controlDetail,
      safetyState,
      safetyDetail,
      backendSummary,
      backendList,
      ticketSummary,
      ticketState,
      ticketDetail,
      ticketResult,
      ticketResultDetail,
      ticketReselect
    ];
    if (requiredAdminElements.some((element) => !element)) {
      reportClientFault('missing_admin_dom', 'admin shell incomplete');
      showFatalPage('Admin lapa nav pilnībā ielādējusies. Mēģini pārlādēt lapu.');
      return;
    }
    let simSetupDisplay = { width: 720, height: 1280 };
    let simSetupPointer = null;
    let simSetupLongPressTimer = null;
    const simSetupTapMaxDistance = 12;
    const simSetupLongPressDelayMs = 650;
    const adminRefreshMs = 5000;
    let adminLoadInFlight = null;
    let adminActionDepth = 0;
    let activeBackendId = '';

    async function load(options) {
      const quiet = Boolean(options && options.quiet);
      if (adminLoadInFlight) {
        try {
          await adminLoadInFlight;
        } catch (error) {
          if (!quiet) throw error;
        }
        return;
      }
      adminLoadInFlight = (async () => {
        const [stateResponse, backendResponse] = await Promise.all([
          fetch('/api/v1/admin/state', { cache: 'no-store' }),
          fetch('/api/v1/admin/phone/backends', { cache: 'no-store' })
        ]);
        const payload = await stateResponse.json();
        const backendsPayload = await backendResponse.json();
        if (!stateResponse.ok || !payload.ok) throw new Error(payload.message || 'load failed');
        if (!backendResponse.ok || !backendsPayload.ok) throw new Error(backendsPayload.message || 'backend load failed');
        renderAdmin(payload.state, payload.phone, backendsPayload);
        if (simSetup && simulatorSetupActive()) {
          loadSimulatorSetup().catch((error) => renderSimulatorSetupError(error.message || 'Simulator control unavailable'));
        }
      })();
      try {
        await adminLoadInFlight;
      } catch (error) {
        if (!quiet) throw error;
      } finally {
        adminLoadInFlight = null;
      }
    }

    function renderAdmin(state, phone, backendsPayload) {
      const phoneRecord = state.phone || {};
      const phoneHealth = parsePhoneHealth(phoneRecord.statusJson || phoneRecord.healthJson);
      renderStatus(state, phone, phoneHealth);
      renderTicketSelection(phoneHealth);
      renderBackends(backendsPayload);
      membersEl.textContent = '';
      activeMembers(state).forEach((member) => {
        const row = document.createElement('div');
        row.className = 'admin-member';
        const main = document.createElement('div');
        main.className = 'admin-member-main';
        const email = document.createElement('span');
        email.className = 'admin-member-email';
        email.textContent = member.email;
        const publicId = document.createElement('span');
        publicId.className = 'admin-member-public-id';
        publicId.textContent = member.publicId || '----';
        const updated = document.createElement('span');
        updated.className = 'admin-muted';
        updated.textContent = relativeTime(member.updatedAt);
        main.append(email, publicId, updated);
        const role = document.createElement('span');
        role.className = `admin-pill ${member.role || 'member'}`;
        role.textContent = member.role;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.textContent = 'Remove';
        remove.disabled = member.role === 'owner';
        remove.addEventListener('click', async () => {
          await runAdminAction(remove, 'Removing member...', async () => {
            await apiFetch(`/api/v1/admin/members?email=${encodeURIComponent(member.email)}`, { method: 'DELETE', cache: 'no-store' });
            showNotice('Member removed');
            await load();
          });
        });
        row.append(main, role, remove);
        membersEl.appendChild(row);
      });
      stateEl.textContent = JSON.stringify({ state, phone, phoneBackends: backendsPayload }, null, 2);
    }

    memberForm.addEventListener('submit', async (event) => {
      event.preventDefault();
      await runAdminAction(memberForm.querySelector('button[type="submit"]'), 'Adding member...', async () => {
        await apiFetch('/api/v1/admin/members', {
          method: 'POST',
          cache: 'no-store',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ email: memberEmail.value, role: memberRole.value })
        });
        memberEmail.value = '';
        showNotice('Member saved');
        await load();
      });
    });

    ticketReselect.addEventListener('click', async () => {
      await runAdminAction(ticketReselect, 'Requesting...', async () => {
        const response = await apiFetch('/api/v1/admin/ticket/reselect-latest', {
          method: 'POST',
          cache: 'no-store'
        });
        let payload = {};
        try {
          payload = await response.json();
        } catch (_) {}
        showNotice(payload.commandId ? `Latest ticket reselect request sent · ${payload.commandId}` : 'Latest ticket reselect request sent');
        await load();
      });
    });

    function renderStatus(state, phone, phoneHealth) {
      const members = activeMembers(state);
      const viewers = state.viewers || [];
      const activeViewers = viewers.filter((viewer) => viewer.connected !== false);
      const phoneRecord = state.phone || {};
      const rootCapture = phoneHealth.rootCapture || {};
      const pipeline = phoneHealth.streamPipeline || {};
      const inputGate = phoneHealth.inputGate || {};
      const lockdown = phoneHealth.notificationLockdown || {};
      const controlCode = phoneHealth.controlCodeRequest || {};
      const streamLive = rootCapture.active || phoneHealth.streamVerdict === 'live' || pipeline.streamVerdict === 'live';

      memberSummary.textContent = `${members.length} member${members.length === 1 ? '' : 's'} configured`;
      sessionSummary.textContent = `${activeViewers.length} viewer${activeViewers.length === 1 ? '' : 's'} on page`;

      phoneState.textContent = phone && phone.connected ? 'Connected' : phoneRecord.desiredState || 'Idle';
      phoneDetail.textContent = `${phoneRecord.attachName || phoneRecord.id || 'Pixel'} · seen ${relativeTime(phoneRecord.lastSeenAt || (phone && phone.lastSeenAt))}`;

      streamState.textContent = streamLive ? 'Live' : (phoneHealth.streamActive ? 'Starting' : 'Idle');
      streamDetail.textContent = rootCapture.message || (streamLive ? 'Ticket stream is live' : pipeline.secureWindowCaptureBypassMessage) || 'Waiting for viewers';

      controlState.textContent = controlCode.status || 'Ready';
      controlDetail.textContent = controlCode.reason || 'Requests are handled by the phone one at a time';

      safetyState.textContent = lockdown.active ? 'Locked down' : 'Ready';
      safetyDetail.textContent = inputGate.reason
        ? `Input gate: ${inputGate.reason}`
        : (lockdown.reason || 'Tap-only controls');

    }

    function renderTicketSelection(phoneHealth) {
      const vivi = phoneHealth.viviState || {};
      const reselect = phoneHealth.latestTicketReselect || {};
      const stateName = vivi.state || 'UNKNOWN_VIVI';
      const ticketId = vivi.ticketId || '';
      const observed = durationAgo(vivi.observedAgoMillis);
      const source = vivi.source || 'unknown';
      const reason = vivi.reason || 'unknown';
      const selected = stateName === 'TICKET_DETAIL';
      ticketSummary.textContent = selected
        ? `Selected: ${ticketId || 'ticket detail'}`
        : `ViVi state: ${humanState(stateName)}`;
      ticketState.textContent = ticketId || humanState(stateName);
      ticketDetail.textContent = `${selected ? 'Ticket detail' : humanState(stateName)} · ${observed ? `seen ${observed}` : 'not observed'} · ${source} · ${reason}`;

      const reselectStatus = String(reselect.status || '').toLowerCase();
      const reselectActive = Boolean(reselect.active);
      ticketReselect.disabled = reselectActive;
      if (reselectStatus && reselectStatus !== 'idle') {
        const started = durationAgo(reselect.startedAgoMillis);
        const completed = durationAgo(reselect.completedAgoMillis);
        const fresh = durationAgo(reselect.freshFrameAgoMillis);
        if (reselectStatus === 'pending') {
          ticketResult.textContent = reselectActive ? 'Pending' : 'Failed';
          ticketResultDetail.textContent = reselectActive
            ? `Phone is selecting the latest ticket${started ? ` · started ${started}` : ''}`
            : `Phone did not finish ticket selection${started ? ` · started ${started}` : ''}`;
          return;
        }
        if (reselectStatus === 'succeeded') {
          ticketResult.textContent = fresh ? 'Ready' : 'Pending';
          ticketResultDetail.textContent = fresh
            ? `Fresh ticket stream confirmed ${fresh}`
            : `Ticket selected${completed ? ` ${completed}` : ''}; waiting for fresh stream frame`;
          return;
        }
        if (reselectStatus === 'failed') {
          ticketResult.textContent = 'Failed';
          ticketResultDetail.textContent = `${reselect.reason || 'Ticket reselect failed'}${completed ? ` · ${completed}` : ''}`;
          return;
        }
      }

      const latest = latestTicketReselectEvent(phoneHealth.recentEvents || []);
      if (!latest) {
        ticketResult.textContent = 'No request yet';
        ticketResultDetail.textContent = 'Use this only when the phone should forget the remembered ticket and pick again.';
        return;
      }
      const event = latest.event || '';
      const ok = event.indexOf('succeeded') >= 0 || event.indexOf('requested') >= 0;
      const failed = event.indexOf('failed') >= 0 || event.indexOf('blocked') >= 0;
      ticketResult.textContent = failed ? 'Failed' : (ok ? 'Accepted' : humanState(event));
      ticketResultDetail.textContent = `${humanState(event)} · ${durationAgo(latest.atAgoMillis) || 'just now'}${latest.detail ? ` · ${latest.detail}` : ''}`;
    }

    function latestTicketReselectEvent(events) {
      for (let index = events.length - 1; index >= 0; index -= 1) {
        const item = events[index] || {};
        if (String(item.event || '').indexOf('latest_ticket_reselect') === 0) return item;
      }
      return null;
    }

    function humanState(value) {
      return String(value || 'unknown')
        .replace(/_/g, ' ')
        .toLowerCase()
        .replace(/\b\w/g, (letter) => letter.toUpperCase());
    }

    function activeMembers(state) {
      return (state.members || []).filter((member) => member.active !== false);
    }

    function renderBackends(payload) {
      const activeId = payload.activeBackendId || '';
      activeBackendId = activeId;
      const backends = payload.backends || [];
      const active = backends.find((backend) => backend.id === activeId);
      backendSummary.textContent = active
        ? `Active: ${active.attachName || active.id}`
        : 'No active backend selected';
      backendList.textContent = '';
      backends.forEach((backend) => {
        const row = document.createElement('div');
        row.className = `admin-backend ${backend.active ? 'active' : ''}`;
        const main = document.createElement('div');
        main.className = 'admin-backend-main';
        const name = document.createElement('strong');
        name.textContent = backend.attachName || backend.id;
        const detail = document.createElement('span');
        detail.className = 'admin-muted';
        const relay = backend.relay || {};
        const state = backend.active
          ? `${relay.streamState || 'idle'}${relay.connected ? ' · connected' : ''}`
          : (backend.healthOk ? 'reachable' : 'not reachable');
        detail.textContent = `${state} · ${backend.baseUrl || ''}`;
        main.append(name, detail);

        const badge = document.createElement('span');
        badge.className = `admin-pill ${backend.active ? 'owner' : ''}`;
        badge.textContent = backend.active ? 'active' : (backend.healthOk ? 'ready' : 'offline');

        const button = document.createElement('button');
        button.type = 'button';
        button.textContent = backend.active ? 'Selected' : 'Use';
        button.disabled = backend.active;
        button.addEventListener('click', async () => {
          await runAdminAction(button, 'Switching...', async () => {
            await apiFetch('/api/v1/admin/phone/backend', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ backendId: backend.id })
            });
            showNotice(`Switched to ${backend.attachName || backend.id}`);
            await load();
          });
        });

        row.append(main, badge, button);
        backendList.appendChild(row);
      });
      renderSimulatorAvailability(active);
    }

    function simulatorSetupActive() {
      return Boolean(simSetup && activeBackendId && activeBackendId === (cfg.simulatorSetupBackendId || 'android-sim'));
    }

    function renderSimulatorAvailability(activeBackend) {
      if (!simSetup) return;
      const enabled = simulatorSetupActive();
      simSetup.classList.toggle('is-disabled', !enabled);
      simSetup.querySelectorAll('button, input').forEach((control) => {
        control.disabled = !enabled;
      });
      if (!enabled) {
        const label = activeBackend ? (activeBackend.attachName || activeBackend.id) : 'selected backend';
        renderSimulatorSetupError(`Simulator control is available only when the Android simulator backend is active. Current backend: ${label}.`);
        if (simSetupPackages) simSetupPackages.textContent = '';
        if (simSetupScreenshot) simSetupScreenshot.removeAttribute('src');
        setSimulatorLastInput('Simulator backend is not active');
      }
    }

    async function loadSimulatorSetup() {
      if (!simSetup || !simulatorSetupActive()) return;
      const response = await fetch('/api/v1/admin/phone/setup/status', { cache: 'no-store' });
      const payload = await response.json();
      if (!response.ok || !payload.ok) throw new Error(payload.message || payload.error || 'Simulator control unavailable');
      renderSimulatorSetup(payload);
    }

    function renderSimulatorSetup(payload) {
      const display = payload.display || {};
      if (display.width && display.height) simSetupDisplay = display;
      const displayLabel = `${simSetupDisplay.width}x${simSetupDisplay.height}${simSetupDisplay.density ? ` · ${simSetupDisplay.density} dpi` : ''}`;
      simSetupSummary.textContent = payload.connected
        ? `Connected · ${displayLabel}${payload.message ? ` · ${payload.message}` : ''}`
        : payload.error || 'Simulator is not connected';
      simSetupPackages.textContent = '';
      const packages = payload.packages || {};
      [
        ['vivi', 'ViVi'],
        ['accrescent', 'Accrescent'],
        ['aurora', 'Aurora'],
        ['controller', 'Controller']
      ].forEach(([key, label]) => {
        const info = packages[key] || {};
        const pill = document.createElement('span');
        pill.className = `admin-pill ${info.installed ? 'owner' : ''}`;
        pill.textContent = `${label}: ${info.installed ? 'installed' : 'missing'}`;
        simSetupPackages.appendChild(pill);
      });
      if (payload.connected && simSetupScreenshot) {
        refreshSimulatorScreenshot();
      }
    }

    function renderSimulatorSetupError(message) {
      if (!simSetupSummary) return;
      simSetupSummary.textContent = message;
    }

    function refreshSimulatorScreenshot(delayMs) {
      if (!simSetupScreenshot) return;
      const refresh = () => {
        simSetupScreenshot.src = `/api/v1/admin/phone/setup/screenshot?t=${Date.now()}`;
      };
      if (delayMs && delayMs > 0) {
        setTimeout(refresh, delayMs);
        return;
      }
      refresh();
    }

    function setSimulatorLastInput(message, failed) {
      if (!simSetupLastInput) return;
      simSetupLastInput.textContent = message;
      simSetupLastInput.classList.toggle('admin-error', Boolean(failed));
    }

    async function postSimulatorInput(body, label) {
      await apiFetch('/api/v1/admin/phone/setup/input', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      setSimulatorLastInput(label || 'Input sent');
      refreshSimulatorScreenshot();
      refreshSimulatorScreenshot(450);
      refreshSimulatorScreenshot(1100);
      setTimeout(() => loadSimulatorSetup().catch((error) => renderSimulatorSetupError(error.message || 'Simulator control unavailable')), 1200);
    }

    function simulatorScreenPoint(event) {
      if (!simSetupScreenshot) return null;
      const rect = simSetupScreenshot.getBoundingClientRect();
      if (!rect.width || !rect.height) return null;
      const width = simSetupDisplay.width || simSetupScreenshot.naturalWidth || rect.width;
      const height = simSetupDisplay.height || simSetupScreenshot.naturalHeight || rect.height;
      const x = Math.max(0, Math.min(width - 1, Math.round(((event.clientX - rect.left) / rect.width) * width)));
      const y = Math.max(0, Math.min(height - 1, Math.round(((event.clientY - rect.top) / rect.height) * height)));
      return { x, y, at: Date.now() };
    }

    function simulatorPointDistance(a, b) {
      if (!a || !b) return 0;
      const dx = a.x - b.x;
      const dy = a.y - b.y;
      return Math.sqrt(dx * dx + dy * dy);
    }

    function clearSimulatorLongPressTimer() {
      if (simSetupLongPressTimer) {
        clearTimeout(simSetupLongPressTimer);
        simSetupLongPressTimer = null;
      }
    }

    function simulatorGestureDuration(start, end) {
      return Math.max(50, Math.min(1000, Math.round(((end && end.at) || Date.now()) - ((start && start.at) || Date.now()))));
    }

    function simulatorKeyInput(event) {
      if (event.ctrlKey || event.metaKey || event.altKey) return null;
      switch (event.key) {
        case 'Backspace':
          return { body: { type: 'key', key: 'delete' }, label: 'Delete sent' };
        case 'Enter':
          return { body: { type: 'key', key: 'enter' }, label: 'Enter sent' };
        case ' ':
        case 'Spacebar':
          return { body: { type: 'key', key: 'space' }, label: 'Space sent' };
        case 'Tab':
          return { body: { type: 'key', key: 'tab' }, label: 'Tab sent' };
        case 'Escape':
          return { body: { type: 'key', key: 'escape' }, label: 'Escape sent' };
        default:
          break;
      }
      if (event.key && event.key.length === 1) {
        return { body: { type: 'text', text: event.key }, label: 'Text sent' };
      }
      return null;
    }

    if (simSetup) {
      if (simSetupRefreshButton) {
        simSetupRefreshButton.addEventListener('click', async () => {
          await runAdminAction(simSetupRefreshButton, 'Refreshing...', async () => {
            await loadSimulatorSetup();
            refreshSimulatorScreenshot();
            setSimulatorLastInput('Screen refreshed');
          });
        });
      }
      simSetup.querySelectorAll('[data-sim-open]').forEach((button) => {
        button.addEventListener('click', async () => {
          await runAdminAction(button, 'Opening...', async () => {
            await apiFetch('/api/v1/admin/phone/setup/open', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ target: button.dataset.simOpen })
            });
            setSimulatorLastInput(`${button.textContent.trim()} opened`);
            refreshSimulatorScreenshot();
            refreshSimulatorScreenshot(650);
            setTimeout(() => loadSimulatorSetup().catch((error) => renderSimulatorSetupError(error.message || 'Simulator control unavailable')), 900);
          });
        });
      });
      simSetup.querySelectorAll('[data-sim-key]').forEach((button) => {
        button.addEventListener('click', async () => {
          await runAdminAction(button, 'Sending...', async () => {
            await postSimulatorInput({ type: 'key', key: button.dataset.simKey }, `${button.textContent.trim()} sent`);
          });
        });
      });
      if (simSetupTextForm) {
        simSetupTextForm.addEventListener('submit', async (event) => {
          event.preventDefault();
          await runAdminAction(simSetupTextForm.querySelector('button[type="submit"]'), 'Typing...', async () => {
            await postSimulatorInput({ type: 'text', text: simSetupText.value }, 'Text sent');
            simSetupText.value = '';
          });
        });
      }
      if (simSetupScreenshot) {
        simSetupScreenshot.addEventListener('pointerdown', (event) => {
          if (event.button !== undefined && event.button !== 0) return;
          const point = simulatorScreenPoint(event);
          if (!point) return;
          event.preventDefault();
          simSetupScreenshot.focus({ preventScroll: true });
          try { simSetupScreenshot.setPointerCapture(event.pointerId); } catch (_) {}
          simSetupPointer = {
            id: event.pointerId,
            start: point,
            last: point,
            longPressSent: false
          };
          clearSimulatorLongPressTimer();
          simSetupLongPressTimer = setTimeout(() => {
            if (!simSetupPointer || simSetupPointer.id !== event.pointerId) return;
            simSetupPointer.longPressSent = true;
            postSimulatorInput({ type: 'long_press', x: point.x, y: point.y, durationMs: simSetupLongPressDelayMs }, 'Long press sent')
              .catch((error) => {
                setSimulatorLastInput(error.message || 'Long press failed', true);
                showNotice(error.message || 'Long press failed', true);
              });
          }, simSetupLongPressDelayMs);
        });
        simSetupScreenshot.addEventListener('pointermove', (event) => {
          if (!simSetupPointer || simSetupPointer.id !== event.pointerId) return;
          const point = simulatorScreenPoint(event);
          if (!point) return;
          simSetupPointer.last = point;
          if (simulatorPointDistance(simSetupPointer.start, point) > simSetupTapMaxDistance) {
            clearSimulatorLongPressTimer();
          }
        });
        simSetupScreenshot.addEventListener('pointerup', async (event) => {
          if (!simSetupPointer || simSetupPointer.id !== event.pointerId) return;
          event.preventDefault();
          clearSimulatorLongPressTimer();
          const pointer = simSetupPointer;
          simSetupPointer = null;
          try { simSetupScreenshot.releasePointerCapture(event.pointerId); } catch (_) {}
          if (pointer.longPressSent) return;
          const end = simulatorScreenPoint(event) || pointer.last || pointer.start;
          const distance = simulatorPointDistance(pointer.start, end);
          const body = distance > simSetupTapMaxDistance
            ? { type: 'drag', startX: pointer.start.x, startY: pointer.start.y, endX: end.x, endY: end.y, durationMs: simulatorGestureDuration(pointer.start, end) }
            : { type: 'tap', x: end.x, y: end.y };
          const label = distance > simSetupTapMaxDistance ? 'Swipe sent' : 'Tap sent';
          await postSimulatorInput(body, label).catch((error) => {
            setSimulatorLastInput(error.message || `${label} failed`, true);
            showNotice(error.message || `${label} failed`, true);
          });
        });
        simSetupScreenshot.addEventListener('pointercancel', (event) => {
          if (simSetupPointer && simSetupPointer.id === event.pointerId) {
            simSetupPointer = null;
            clearSimulatorLongPressTimer();
          }
        });
        simSetupScreenshot.addEventListener('keydown', async (event) => {
          const input = simulatorKeyInput(event);
          if (!input) return;
          event.preventDefault();
          await postSimulatorInput(input.body, input.label).catch((error) => {
            setSimulatorLastInput(error.message || 'Keyboard input failed', true);
            showNotice(error.message || 'Keyboard input failed', true);
          });
        });
      }
      setInterval(() => {
        loadSimulatorSetup().catch((error) => renderSimulatorSetupError(error.message || 'Simulator control unavailable'));
      }, 3500);
    }

    function extractJsonObjectField(raw, field) {
      const text = String(raw || '');
      const key = `"${field}"`;
      const keyIndex = text.indexOf(key);
      if (keyIndex < 0) return null;
      const colonIndex = text.indexOf(':', keyIndex + key.length);
      if (colonIndex < 0) return null;
      let start = colonIndex + 1;
      while (start < text.length && /\s/.test(text[start])) start += 1;
      if (text[start] !== '{') return null;
      let depth = 0;
      let inString = false;
      let escaped = false;
      for (let index = start; index < text.length; index += 1) {
        const char = text[index];
        if (inString) {
          if (escaped) {
            escaped = false;
          } else if (char === '\\') {
            escaped = true;
          } else if (char === '"') {
            inString = false;
          }
          continue;
        }
        if (char === '"') {
          inString = true;
        } else if (char === '{') {
          depth += 1;
        } else if (char === '}') {
          depth -= 1;
          if (depth === 0) {
            try {
              return JSON.parse(text.slice(start, index + 1));
            } catch (_) {
              return null;
            }
          }
        }
      }
      return null;
    }

    function extractJsonStringField(raw, field) {
      const pattern = new RegExp(`"${field}"\\s*:\\s*"((?:\\\\.|[^"\\\\])*)"`);
      const match = String(raw || '').match(pattern);
      if (!match) return '';
      try {
        return JSON.parse(`"${match[1]}"`);
      } catch (_) {
        return match[1];
      }
    }

    function extractJsonBooleanField(raw, field) {
      const pattern = new RegExp(`"${field}"\\s*:\\s*(true|false)`);
      const match = String(raw || '').match(pattern);
      if (!match) return null;
      return match[1] === 'true';
    }

    function parsePartialPhoneHealth(raw) {
      const partial = {};
      ['latestTicketReselect', 'controlCodeRequest', 'inputGate', 'viviState', 'rootCapture', 'streamPipeline', 'notificationLockdown'].forEach((field) => {
        const value = extractJsonObjectField(raw, field);
        if (value) partial[field] = value;
      });
      const streamActive = extractJsonBooleanField(raw, 'streamActive');
      if (streamActive !== null) partial.streamActive = streamActive;
      const streamVerdict = extractJsonStringField(raw, 'streamVerdict');
      if (streamVerdict) partial.streamVerdict = streamVerdict;
      const sessionState = extractJsonStringField(raw, 'sessionState');
      if (sessionState) partial.sessionState = sessionState;
      return partial;
    }

    function parsePhoneHealth(raw) {
      if (!raw) return {};
      try {
        const parsed = JSON.parse(raw);
        return parsed && parsed.data ? parsed.data : parsed;
      } catch (_) {
        return parsePartialPhoneHealth(raw);
      }
    }

    function relativeTime(value) {
      if (!value) return 'never';
      const at = Date.parse(value);
      if (!Number.isFinite(at)) return value;
      const seconds = Math.max(0, Math.round((Date.now() - at) / 1000));
      if (seconds < 5) return 'just now';
      if (seconds < 60) return `${seconds}s ago`;
      const minutes = Math.round(seconds / 60);
      if (minutes < 60) return `${minutes}m ago`;
      const hours = Math.round(minutes / 60);
      if (hours < 24) return `${hours}h ago`;
      const days = Math.round(hours / 24);
      return `${days}d ago`;
    }

    function durationAgo(value) {
      if (value === null || value === undefined || value === '') return '';
      const millis = Number(value);
      if (!Number.isFinite(millis)) return '';
      const seconds = Math.max(0, Math.round(millis / 1000));
      if (seconds < 5) return 'just now';
      if (seconds < 60) return `${seconds}s ago`;
      const minutes = Math.round(seconds / 60);
      if (minutes < 60) return `${minutes}m ago`;
      const hours = Math.round(minutes / 60);
      if (hours < 24) return `${hours}h ago`;
      const days = Math.round(hours / 24);
      return `${days}d ago`;
    }


    function showNotice(message, error = false) {
      notice.textContent = message;
      notice.classList.toggle('error', error);
      notice.hidden = false;
    }

    async function apiFetch(url, options) {
      const response = await fetch(url, options);
      if (response.ok) return response;
      let message = `${response.status} ${response.statusText}`.trim();
      try {
        const payload = await response.json();
        message = payload.message || payload.error || message;
      } catch (_) {}
      throw new Error(message);
    }

    async function runAdminAction(button, pending, action) {
      const original = button ? button.textContent : '';
      const wasDisabled = button ? button.disabled : false;
      adminActionDepth += 1;
      try {
        if (button) {
          button.disabled = true;
          button.textContent = pending;
        }
        await action();
      } catch (error) {
        showNotice(error.message || 'Action failed', true);
      } finally {
        if (button) {
          button.textContent = original;
          button.disabled = wasDisabled;
        }
        adminActionDepth = Math.max(0, adminActionDepth - 1);
      }
    }

    load().catch((error) => {
      showNotice(error.message || 'Load failed', true);
      stateEl.textContent = error.stack || error.message;
    });
    setInterval(() => {
      if (document.visibilityState === 'hidden') return;
      if (adminActionDepth > 0) return;
      load({ quiet: true });
    }, adminRefreshMs);
  }

  // ============================================================
  // Test harness: enabled by visiting ticket.jolkins.id.lv#test-harness
  // Captures every clientLog event with millisecond timestamps,
  // times each control-code request end-to-end, and offers an
  // automated 93-minute loop that opens the dialog, submits a
  // control code, waits for the result, and repeats every 60s.
  // Exposes window.__ticketTest for inspection.
  //
  // Gated by process.env.TICKET_APP_DEV: the harness is stripped from
  // the production build (TICKET_APP_MODE=prod) so end users get the
  // slim bundle. Build the dev bundle with TICKET_APP_MODE=dev.
  // ============================================================
  if (process.env.TICKET_APP_DEV && (() => {
    const harnessEnabled = (() => {
      try {
        return (typeof window !== 'undefined')
          && (window.location.hash === '#test-harness'
              || window.location.hash === '#harness'
              || /[?&]test=1\b/.test(window.location.search));
      } catch (_) { return false; }
    })();
    return harnessEnabled;
  })()) {
    const harnessState = {
      startedAt: performance.now(),
      pageLoadAt: 0,
      firstFrameAt: 0,
      firstDecodedFrameAt: 0,
      streamStatusHistory: [],
      clientLogHistory: [],
      controlCodeRequests: [],
      keyframeEvents: [],
      phoneEngineStates: [],
      autoTestRunning: false,
      autoTestStopRequested: false,
      autoTestStartedAt: 0,
      autoTestResults: [],
    };
    const HARNESS_MAX_HISTORY = 5000;
    function harnessPushHistory(arr, item) {
      arr.push(item);
      if (arr.length > HARNESS_MAX_HISTORY) arr.shift();
    }
    function harnessTimestamp() {
      return Math.round(performance.now() - harnessState.startedAt);
    }
    const originalClientLog = clientLog;
    clientLog = function(event, detail) {
      const entry = { t: harnessTimestamp(), event, detail: typeof detail === 'string' ? detail : (function(){ try { return JSON.stringify(detail); } catch(_){ return String(detail); } })() };
      harnessPushHistory(harnessState.clientLogHistory, entry);
      try { originalClientLog(event, detail); } catch (_) {}
      try { updateHarnessPanel(); } catch (_) {}
    };
    const healthPollInterval = setInterval(async () => {
      try {
        const ds = relayReportToStreamStatus(currentState && currentState.relayCurrentReport);
        if (ds) {
          harnessPushHistory(harnessState.phoneEngineStates, {
            t: harnessTimestamp(),
            streamVerdict: ds.streamVerdict,
            phoneStreamState: ds.phoneStreamState,
            phoneConnected: ds.phoneConnected,
            phoneDesired: ds.phoneDesired,
            streamActive: ds.streamActive,
            lastFrameAgoMillis: ds.lastFrameAgoMillis,
            activeVideoClients: ds.activeVideoClients,
          });
          try { updateHarnessPanel(); } catch (_) {}
        }
      } catch (_) {}
    }, 1000);
    // Hook first-frame detection
    const origNoteFirstFrame = function(){
      if (harnessState.firstFrameAt === 0) {
        harnessState.firstFrameAt = harnessTimestamp();
      }
    };
    // Track first decoded frame
    const origRenderDecoded = renderDecodedFrame;
    // (We can't easily wrap the function without breaking closures; instead we
    // poll for firstFrameReceived via the existing setter. The capture below
    // catches it after the fact.)
    const firstFrameWatcher = setInterval(() => {
      if (firstFrameReceived && harnessState.firstDecodedFrameAt === 0) {
        harnessState.firstDecodedFrameAt = harnessTimestamp();
        clearInterval(firstFrameWatcher);
        try { updateHarnessPanel(); } catch (_) {}
      }
    }, 100);
    // Track stream_status
    const origHandleStreamStatus = handleStreamStatus;
    // (cannot wrap without breaking closure scope; record in handleStreamStatus inline)
    // Track control-code request lifecycle
    function harnessWrapControlCodeLifecycle() {
      const origSubmit = submitControlCodeRequest;
      // We can't directly wrap because of closure; lifecycle timings are
      // captured by watching the local request state.
    }
    // Inject a wrapper that records each control_code_submitted event with
    // extra state: at submit time, record T0. When the codeRequest state
    // transitions, record the rest.
    const origRenderControlCode = renderControlCodeRequest;
    // We instrument via a polling loop that watches codeRequest.status.
    let lastObservedCodeRequestId = '';
    let activeCodeRequestStartedAt = 0;
    const controlCodeWatcher = setInterval(() => {
      const cur = codeRequest;
      const id = cur && cur.requestId ? String(cur.requestId) : '';
      const status = cur && cur.status ? String(cur.status) : '';
      if (id && id !== lastObservedCodeRequestId) {
        // New request
        if (lastObservedCodeRequestId !== '') {
          // Previous request changed under us; close it out
          const prev = harnessState.controlCodeRequests[harnessState.controlCodeRequests.length - 1];
          if (prev) prev.finalStatus = 'changed';
        }
        lastObservedCodeRequestId = id;
        activeCodeRequestStartedAt = harnessTimestamp();
        harnessPushHistory(harnessState.controlCodeRequests, {
          requestId: id,
          t_observed: activeCodeRequestStartedAt,
          initialStatus: status,
          transitions: [],
        });
        try { updateHarnessPanel(); } catch (_) {}
      }
      if (id && status) {
        const last = harnessState.controlCodeRequests[harnessState.controlCodeRequests.length - 1];
        if (last && last.requestId === id) {
          const lastTransition = last.transitions[last.transitions.length - 1];
          if (!lastTransition || lastTransition.status !== status) {
            last.transitions.push({ t: harnessTimestamp(), status });
            if (last.initialStatus === 'succeeded' || status === 'succeeded' || status === 'failed') {
              last.finalStatus = status;
              last.t_completed = harnessTimestamp();
              last.t_total_ms = last.t_completed - last.t_observed;
            }
            try { updateHarnessPanel(); } catch (_) {}
          }
        }
      }
      if (!id && lastObservedCodeRequestId !== '') {
        const last = harnessState.controlCodeRequests[harnessState.controlCodeRequests.length - 1];
        if (last) {
          last.finalStatus = last.finalStatus || 'cleared';
          last.t_completed = last.t_completed || harnessTimestamp();
          last.t_total_ms = last.t_total_ms || (last.t_completed - last.t_observed);
        }
        lastObservedCodeRequestId = '';
        try { updateHarnessPanel(); } catch (_) {}
      }
    }, 200);

    // Build the harness UI
    function ensureHarnessPanel() {
      let panel = document.getElementById('__ticketHarnessPanel');
      if (panel) return panel;
      panel = document.createElement('div');
      panel.id = '__ticketHarnessPanel';
      panel.style.cssText = 'position:fixed;left:8px;bottom:8px;width:480px;max-height:60vh;overflow:auto;background:rgba(0,0,0,0.85);color:#eef3f8;font:12px/1.4 ui-monospace,Menlo,monospace;padding:10px 12px;border:1px solid #4f8cff;border-radius:6px;z-index:2147483647;box-sizing:border-box;';
      panel.innerHTML = `
        <div style="font-weight:bold;color:#4f8cff;margin-bottom:6px;">Ticket Test Harness</div>
        <div id="__ticketHarnessSummary" style="margin-bottom:8px;white-space:pre;"></div>
        <div id="__ticketHarnessLastRequests" style="margin-bottom:8px;white-space:pre;"></div>
        <div id="__ticketHarnessPhone" style="margin-bottom:8px;white-space:pre;color:#fbbf24;"></div>
        <div style="display:flex;gap:6px;flex-wrap:wrap;">
          <button id="__ticketHarnessStart" type="button" style="padding:4px 8px;background:#2167d5;color:#fff;border:1px solid #4f8cff;border-radius:4px;cursor:pointer;">Start 93-min auto-test (60s interval)</button>
          <button id="__ticketHarnessStop" type="button" style="padding:4px 8px;background:#7f1d1d;color:#fff;border:1px solid #ef4444;border-radius:4px;cursor:pointer;">Stop</button>
          <button id="__ticketHarnessExport" type="button" style="padding:4px 8px;background:#374151;color:#fff;border:1px solid #6b7280;border-radius:4px;cursor:pointer;">Export JSON</button>
          <button id="__ticketHarnessClear" type="button" style="padding:4px 8px;background:#374151;color:#fff;border:1px solid #6b7280;border-radius:4px;cursor:pointer;">Clear log</button>
        </div>
        <div id="__ticketHarnessLog" style="margin-top:8px;max-height:200px;overflow:auto;white-space:pre;color:#94a3b8;"></div>
      `;
      document.body.appendChild(panel);
      document.getElementById('__ticketHarnessStart').addEventListener('click', startHarnessAutoTest);
      document.getElementById('__ticketHarnessStop').addEventListener('click', () => { harnessState.autoTestStopRequested = true; });
      document.getElementById('__ticketHarnessExport').addEventListener('click', exportHarnessJson);
      document.getElementById('__ticketHarnessClear').addEventListener('click', () => {
        harnessState.clientLogHistory.length = 0;
        harnessState.controlCodeRequests.length = 0;
        harnessState.phoneEngineStates.length = 0;
        harnessState.streamStatusHistory.length = 0;
        updateHarnessPanel();
      });
      return panel;
    }
    function updateHarnessPanel() {
      ensureHarnessPanel();
      const summary = document.getElementById('__ticketHarnessSummary');
      const lastReq = document.getElementById('__ticketHarnessLastRequests');
      const phone = document.getElementById('__ticketHarnessPhone');
      const log = document.getElementById('__ticketHarnessLog');
      const pageLoadMs = harnessState.firstFrameAt;
      const firstDecodedMs = harnessState.firstDecodedFrameAt;
      const completedReqs = harnessState.controlCodeRequests.filter(r => r.t_total_ms != null);
      const totalReqs = harnessState.controlCodeRequests.length;
      const maxReqMs = completedReqs.length ? Math.max(...completedReqs.map(r => r.t_total_ms)) : 0;
      const avgReqMs = completedReqs.length ? Math.round(completedReqs.reduce((a, r) => a + r.t_total_ms, 0) / completedReqs.length) : 0;
      const reqsOver5s = completedReqs.filter(r => r.t_total_ms > 5000).length;
      summary.textContent =
        `page→firstFrame: ${pageLoadMs ? pageLoadMs + 'ms' : '—'}    ` +
        `page→firstDecoded: ${firstDecodedMs ? firstDecodedMs + 'ms' : '—'}\n` +
        `requests: ${totalReqs} total / ${completedReqs.length} completed\n` +
        `max req ms: ${maxReqMs}    avg req ms: ${avgReqMs}    reqs > 5s: ${reqsOver5s}`;
      const last5 = harnessState.controlCodeRequests.slice(-5).reverse();
      lastReq.textContent = last5.map(r => {
        const total = r.t_total_ms != null ? r.t_total_ms + 'ms' : 'pending';
        const transitions = (r.transitions || []).map(t => `${t.status}@${t.t}ms`).join(' → ');
        return `${r.requestId.slice(-6)} ${total}\n  ${transitions}`;
      }).join('\n');
      const lastPhone = harnessState.phoneEngineStates.slice(-3);
      phone.textContent = lastPhone.map(p => `${p.t}ms  verdict=${p.streamVerdict}  phoneStreamState=${p.phoneStreamState}  lastFrameAgo=${p.lastFrameAgoMillis}ms`).join('\n');
      const lastLogs = harnessState.clientLogHistory.slice(-20);
      log.textContent = lastLogs.map(l => `${l.t}ms  ${l.event}  ${l.detail ? l.detail.slice(0, 80) : ''}`).join('\n');
    }
    function startHarnessAutoTest() {
      if (harnessState.autoTestRunning) return;
      harnessState.autoTestRunning = true;
      harnessState.autoTestStopRequested = false;
      harnessState.autoTestStartedAt = harnessTimestamp();
      harnessState.autoTestResults = [];
      const intervalMs = 60000;
      const totalMs = 93 * 60 * 1000;
      const endAt = harnessState.autoTestStartedAt + totalMs;
      // Pick a per-iteration digit pattern to avoid rate-limit collisions
      // (the server allows 2 per 60s per email; the harness itself is one
      // request per minute, but the user may submit manually in parallel).
      function pickDigits() {
        const cycle = ['11111', '22222', '33333', '44444', '55555', '66666', '77777', '88888', '99999', '00000'];
        return cycle[harnessState.controlCodeRequests.length % cycle.length];
      }
      const tick = async () => {
        if (harnessState.autoTestStopRequested) {
          harnessState.autoTestRunning = false;
          try { updateHarnessPanel(); } catch (_) {}
          return;
        }
        if (harnessTimestamp() >= endAt) {
          harnessState.autoTestRunning = false;
          try { updateHarnessPanel(); } catch (_) {}
          try { document.title = 'DONE: ' + (document.title || ''); } catch (_) {}
          return;
        }
        // Cleanup: close any leftover result window from a previous iteration.
        try {
          if (codeResultArea && !codeResultArea.hidden) {
            closeCurrentControlCode(false);
          }
          if (codeDialogOpen) {
            closeControlCodeDialog();
          }
        } catch (_) {}
        // Wait for state to fully settle. Use yield-via-rAF so the browser
        // can paint and the agent-browser command isn't blocked.
        let preWait = 0;
        while ((codeRequest || codeDialogOpen || (codeResultArea && !codeResultArea.hidden)) && preWait < 45000 && !harnessState.autoTestStopRequested) {
          await new Promise(r => requestAnimationFrame(() => setTimeout(r, 250)));
          preWait += 250;
        }
        if (harnessState.autoTestStopRequested) {
          harnessState.autoTestRunning = false;
          try { updateHarnessPanel(); } catch (_) {}
          return;
        }
        // Open the dialog and submit. Use rAF yields between each interaction.
        try { openControlCodeDialog(); } catch (_) {}
        await new Promise(r => requestAnimationFrame(() => setTimeout(r, 200)));
        try {
          codeDigits.value = pickDigits();
          const submitBtn = document.getElementById('controlCodeSubmit');
          if (submitBtn) submitBtn.click();
        } catch (_) {}
        // Wait for the request to fully complete. The phone-side ViVi
        // automation can take 5-20s in the worst case, plus the 30s
        // cleanup window, so allow 90s.
        const requestDeadline = harnessTimestamp() + 90000;
        while (harnessTimestamp() < requestDeadline && !harnessState.autoTestStopRequested) {
          const cur = codeRequest;
          if (!cur || (cur.status !== 'running' && cur.status !== 'queued')) {
            // Request finished. Give the UI 2s to settle.
            await new Promise(r => requestAnimationFrame(() => setTimeout(r, 2000)));
            break;
          }
          await new Promise(r => requestAnimationFrame(() => setTimeout(r, 500)));
        }
        try { updateHarnessPanel(); } catch (_) {}
        // Schedule the next tick
        setTimeout(tick, intervalMs);
      };
      tick();
    }
    function exportHarnessJson() {
      const data = {
        capturedAt: new Date().toISOString(),
        userAgent: navigator.userAgent,
        url: window.location.href,
        harnessStartedAtT: harnessState.startedAt,
        firstFrameAt: harnessState.firstFrameAt,
        firstDecodedFrameAt: harnessState.firstDecodedFrameAt,
        clientLogHistory: harnessState.clientLogHistory,
        controlCodeRequests: harnessState.controlCodeRequests,
        phoneEngineStates: harnessState.phoneEngineStates,
        autoTestResults: harnessState.autoTestResults,
        summary: {
          totalRequests: harnessState.controlCodeRequests.length,
          completedRequests: harnessState.controlCodeRequests.filter(r => r.t_total_ms != null).length,
          maxRequestMs: Math.max(0, ...harnessState.controlCodeRequests.filter(r => r.t_total_ms != null).map(r => r.t_total_ms)),
          avgRequestMs: (() => {
            const a = harnessState.controlCodeRequests.filter(r => r.t_total_ms != null);
            return a.length ? Math.round(a.reduce((s, r) => s + r.t_total_ms, 0) / a.length) : 0;
          })(),
          requestsOver5s: harnessState.controlCodeRequests.filter(r => r.t_total_ms > 5000).length,
        },
      };
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `ticket-harness-${new Date().toISOString().replace(/[:.]/g, '-')}.json`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 5000);
    }
    // Initialize the panel
    setTimeout(() => { try { ensureHarnessPanel(); updateHarnessPanel(); } catch (_) {} }, 100);
    // Expose for inspection
    window.__ticketTest = harnessState;
    window.__ticketTestHarness = { state: harnessState, export: exportHarnessJson, start: startHarnessAutoTest, stop: () => { harnessState.autoTestStopRequested = true; } };
  }
})();
