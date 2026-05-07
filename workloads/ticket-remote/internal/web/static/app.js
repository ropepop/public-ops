(function () {
  const cfg = window.TICKET_REMOTE_CONFIG || {};
  const pageVersion = cfg.pageVersion || 'ticket-remote-dev';

  if ('scrollRestoration' in history) {
    history.scrollRestoration = 'manual';
  }

  const spacetimeTokenKey = 'ticket_remote_spacetime_token';
  const spacetimeTokenExpiryKey = 'ticket_remote_spacetime_token_expires_at';
  const pkceVerifierKey = 'ticket_remote_pkce_verifier';
  const pkceStateKey = 'ticket_remote_pkce_state';
  const pkceVerifierSharedKey = 'ticket_remote_pkce_verifier_shared';
  const pkceStateSharedKey = 'ticket_remote_pkce_state_shared';

  if (!cfg.authenticated) {
    startAuthShell();
    return;
  }

  let spacetimeClient = null;
  let spacetimeClientStatus = 'idle';

  if (document.querySelector('[data-admin="true"]')) {
    startAdmin();
    return;
  }

  const stage = document.querySelector('.stage');
  const canvas = document.getElementById('screen');
  const ctx = canvas.getContext('2d', { alpha: false });
  const emptyState = document.getElementById('emptyState');
  const startStreamButton = document.getElementById('startStream');
  const emptyMessage = document.getElementById('emptyMessage');
  const quickClaimSpinner = document.getElementById('quickClaimSpinner');
  const connectionState = document.getElementById('connectionState');
  const statusLine = document.getElementById('statusLine');
  const panel = document.getElementById('panel');
  const presence = document.getElementById('presence');
  const claimButton = document.getElementById('claimControl');
  const extendButton = document.getElementById('extendControl');
  const releaseButton = document.getElementById('releaseControl');
  const timer = document.getElementById('timer');

  let ws = null;
  let videoWs = null;
  let reconnectTimer = null;
  let configured = false;
  let streamUnsupported = false;
  let streamSize = { width: 540, height: 1080 };
  let currentState = null;
  let serverClockSkewMs = 0;
  let pointerStart = null;
  let connectedAt = 0;
  let videoConnectedAt = 0;
  let configuredAt = 0;
  let lastFrameAt = 0;
  let lastDecodedFrameAt = 0;
  let lastPacketAt = 0;
  let lastPacketSequenceAdvancedAt = 0;
  let lastRestartAt = 0;
  let lastRecoveryKeyframeAt = 0;
  let lastRecoveryDecoderResetAt = 0;
  let lastRecoveryVideoReconnectAt = 0;
  let lastRecoveryServerRecoverAt = 0;
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
  let hasRenderedFrame = false;
  let latestStreamStatus = null;
  let lastStreamStatusAt = 0;
  let claimPromise = null;
  let quickClaimSpinnerInputId = '';
  let quickClaimSpinnerTimeout = null;
  let quickClaimSpinnerPending = false;
  let ticketInUseSpinnerActive = false;
  let lastSelfControl = false;
  let lastActiveControlEmail = '';
  let localControlSendGraceUntil = 0;
  let inputSeq = 0;
  let inputInFlight = null;
  const inputQueue = [];
  const inputQueueLimit = 20;
  const inputAckTimeoutMs = 1800;
  const inputRetryLimit = 1;
  const quickClaimSpinnerTimeoutMs = 8000;
  let lastTouchEndAt = 0;
  let lastTouchEndX = 0;
  let lastTouchEndY = 0;
  const maxTapDurationMs = 450;
  const maxTapTravelPx = 14;
  const streamVerticalPanThresholdPx = 6;
  const streamVerticalPanDominance = 1.1;
  const streamFirstFrameKeyframeMs = 2000;
  const streamStaleKeyframeMs = 2500;
  const streamStaleDecoderResetMs = 5000;
  const streamStaleVideoReconnectMs = 8000;
  const streamStaleServerRecoverMs = 12000;
  const streamDecoderStartupGraceMs = 3500;
  const recoveryKeyframeDebounceMs = 2000;
  const recoveryDecoderResetDebounceMs = 5000;
  const recoveryVideoReconnectDebounceMs = 8000;
  const recoveryServerRecoverDebounceMs = 12000;
  const FRAME_ENVELOPE_MAGIC = 0x54534632;
  const FRAME_ENVELOPE_HEADER_BYTES = 29;
  const doubleTapSuppressMs = 420;
  const doubleTapSuppressPx = 28;
  const quickClaimMaxX = 0.25;
  const quickClaimMaxY = 0.25;
  const controlCodeButtonMinX = 0.04;
  const controlCodeButtonMaxX = 0.45;
  const controlCodeButtonMinY = 0.10;
  const controlCodeButtonMaxY = 0.18;

  function viewportHeight() {
    return Math.max(1, Math.round((window.visualViewport && window.visualViewport.height) || window.innerHeight || document.documentElement.clientHeight || 1));
  }

  function updateViewportVars() {
    document.documentElement.style.setProperty('--ticket-stage-height', `${viewportHeight()}px`);
  }

  function updateDetailsReveal() {
    const revealed = window.scrollY >= Math.max(1, viewportHeight() * 0.82);
    document.body.classList.toggle('details-visible', revealed);
    if (panel) panel.setAttribute('aria-hidden', revealed ? 'false' : 'true');
  }

  function keepFirstScreenPinned(force) {
    if (force) {
      document.body.classList.remove('details-visible');
      if (panel) panel.setAttribute('aria-hidden', 'true');
    }
    if (force || !document.body.classList.contains('details-visible')) {
      window.scrollTo({ top: 0, left: 0, behavior: 'auto' });
      updateDetailsReveal();
    }
  }

  function scheduleFirstScreenPin(force) {
    keepFirstScreenPinned(force);
    requestAnimationFrame(() => keepFirstScreenPinned(force));
    setTimeout(() => keepFirstScreenPinned(force), 60);
    setTimeout(() => keepFirstScreenPinned(force), 300);
  }

  function checkServerVersion(payload) {
    const serverVersion = payload && payload.serverVersion;
    if (!serverVersion || serverVersion === pageVersion) return true;
    if (!String(serverVersion).startsWith('ticket-remote-')) return true;
    const next = new URL(location.href);
    next.searchParams.set('v', serverVersion);
    location.replace(next.toString());
    return false;
  }

  async function refreshHealth() {
    try {
      const response = await fetch('/api/v1/health', { cache: 'no-store' });
      const health = await response.json();
      checkServerVersion(health);
      return health;
    } catch (error) {
      clientLog('health_check_failed', error && error.message);
      return null;
    }
  }

  document.body.dataset.videoPath = 'https-h264';

  function socketURL() {
    return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/v1/session';
  }

  function streamURL() {
    return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/v1/stream';
  }

  function setConnected(text) {
    connectionState.textContent = text;
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
    ['control_claimed', 'Kontrole jau ir pārņemta'],
    ['no_control', 'Nav aktīvas kontroles sesijas'],
    ['not_controller', 'Šo kontroles sesiju pārvalda cits lietotājs'],
    ['already_extended', 'Sesija jau ir pagarināta']
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

  function randomBase64Url(bytes) {
    const data = new Uint8Array(bytes);
    crypto.getRandomValues(data);
    return base64Url(data);
  }

  function base64Url(data) {
    let text = '';
    for (let i = 0; i < data.length; i += 1) {
      text += String.fromCharCode(data[i]);
    }
    return btoa(text).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
  }

  async function pkceChallenge(verifier) {
    const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier));
    return base64Url(new Uint8Array(digest));
  }

  function authConfig() {
    const auth = cfg.auth || {};
    return {
      mode: auth.mode || 'spacetime',
      authorizeUrl: auth.authorizeUrl || `${String(auth.issuer || 'https://auth.spacetimedb.com/oidc').replace(/\/$/, '')}/auth`,
      tokenUrl: auth.tokenUrl || `${String(auth.issuer || 'https://auth.spacetimedb.com/oidc').replace(/\/$/, '')}/token`,
      clientId: auth.clientId || '',
      scope: auth.scope || 'openid profile email',
      redirectUrl: auth.redirectUrl || `${location.origin}/auth/callback`
    };
  }

  function usesDirectSpacetimeAuth() {
    const mode = String((cfg.auth && cfg.auth.mode) || 'spacetime').toLowerCase();
    return !['cloudflare', 'cloudflare-access', 'cf-access', 'dev', 'development', 'none'].includes(mode);
  }

  async function beginSpacetimeLogin() {
    const auth = authConfig();
    if (!auth.clientId) {
      throw new Error('SpacetimeAuth client is not configured.');
    }
    const verifier = randomBase64Url(32);
    const state = randomBase64Url(16);
    sessionStorage.setItem(pkceVerifierKey, verifier);
    sessionStorage.setItem(pkceStateKey, state);
    localStorage.setItem(pkceVerifierSharedKey, verifier);
    localStorage.setItem(pkceStateSharedKey, state);
    const challenge = await pkceChallenge(verifier);
    const next = new URL(auth.authorizeUrl);
    next.searchParams.set('response_type', 'code');
    next.searchParams.set('client_id', auth.clientId);
    next.searchParams.set('redirect_uri', auth.redirectUrl);
    next.searchParams.set('scope', auth.scope);
    next.searchParams.set('state', state);
    next.searchParams.set('code_challenge', challenge);
    next.searchParams.set('code_challenge_method', 'S256');
    location.assign(next.toString());
  }

  async function finishSpacetimeCallback(statusEl) {
    const params = new URLSearchParams(location.search);
    const code = params.get('code') || '';
    const receivedState = params.get('state') || '';
    const expectedState = sessionStorage.getItem(pkceStateKey) || localStorage.getItem(pkceStateSharedKey) || '';
    const verifier = sessionStorage.getItem(pkceVerifierKey) || localStorage.getItem(pkceVerifierSharedKey) || '';
    if (!code || !verifier || !expectedState || receivedState !== expectedState) {
      throw new Error('Login callback did not match this browser. Open the newest email link in the same browser you started from, or start sign-in again.');
    }
    const auth = authConfig();
    const body = new URLSearchParams();
    body.set('grant_type', 'authorization_code');
    body.set('client_id', auth.clientId);
    body.set('code', code);
    body.set('redirect_uri', auth.redirectUrl);
    body.set('code_verifier', verifier);
    statusEl.textContent = 'Pabeidz pierakstīšanos...';
    const tokenResponse = await fetch(auth.tokenUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body
    });
    const tokenPayload = await tokenResponse.json().catch(() => ({}));
    if (!tokenResponse.ok || !tokenPayload.id_token) {
      throw new Error(tokenPayload.error_description || tokenPayload.error || 'SpacetimeAuth token exchange failed.');
    }
    const sessionResponse = await fetch('/api/v1/auth/session', {
      method: 'POST',
      cache: 'no-store',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ idToken: tokenPayload.id_token })
    });
    const sessionPayload = await sessionResponse.json().catch(() => ({}));
    if (!sessionResponse.ok || !sessionPayload.ok) {
      throw new Error(sessionPayload.message || sessionPayload.error || 'Ticket session was rejected.');
    }
    localStorage.setItem(spacetimeTokenKey, tokenPayload.id_token);
    if (sessionPayload.spacetime && sessionPayload.spacetime.expiresAt) {
      localStorage.setItem(spacetimeTokenExpiryKey, sessionPayload.spacetime.expiresAt);
    }
    sessionStorage.removeItem(pkceVerifierKey);
    sessionStorage.removeItem(pkceStateKey);
    localStorage.removeItem(pkceVerifierSharedKey);
    localStorage.removeItem(pkceStateSharedKey);
    location.replace('/');
  }

  function startAuthShell() {
    document.body.className = 'auth-page';
    document.body.innerHTML = [
      '<main class="auth-shell">',
      '<section class="auth-panel">',
      '<h1>Biļete</h1>',
      '<p class="auth-copy">Pieraksties ar e-pastu, lai redzētu tiešraides biļeti.</p>',
      '<p class="auth-copy">Ja e-pasta saite atveras jaunā cilnē, pēc apstiprināšanas atgriezies šajā pārlūkā.</p>',
      '<button id="spacetimeLogin" class="primary" type="button">Pierakstīties ar e-pastu</button>',
      '<p id="authStatus" class="auth-status" role="status"></p>',
      '</section>',
      '</main>'
    ].join('');
    const button = document.getElementById('spacetimeLogin');
    const statusEl = document.getElementById('authStatus');
    const isCallback = location.pathname === '/auth/callback';
    if (isCallback) {
      button.disabled = true;
      finishSpacetimeCallback(statusEl)
        .catch((error) => {
          button.disabled = false;
          statusEl.textContent = error && error.message ? error.message : 'Pierakstīšanās neizdevās.';
        });
      return;
    }
    button.addEventListener('click', () => {
      button.disabled = true;
      statusEl.textContent = 'Atver SpacetimeAuth. Atver jaunāko e-pasta saiti šajā pašā pārlūkā vai jaunā cilnē.';
      beginSpacetimeLogin().catch((error) => {
        button.disabled = false;
        statusEl.textContent = error && error.message ? error.message : 'Pierakstīšanās neizdevās.';
      });
    });
  }

  function setStatus(text) {
    statusLine.textContent = localizePublicMessage(text);
  }

  function sameEmail(left, right) {
    const cleanLeft = String(left || '').trim().toLowerCase();
    const cleanRight = String(right || '').trim().toLowerCase();
    return Boolean(cleanLeft && cleanRight && cleanLeft === cleanRight);
  }

  function currentUserOwnsControl(control) {
    return Boolean(control && sameEmail(control.email, cfg.email));
  }

  function refreshQuickClaimSpinner() {
    if (quickClaimSpinner) {
      quickClaimSpinner.hidden = !(quickClaimSpinnerPending || ticketInUseSpinnerActive);
    }
  }

  function setTicketInUseSpinner(active) {
    ticketInUseSpinnerActive = Boolean(active);
    refreshQuickClaimSpinner();
  }

  function showQuickClaimSpinner(inputId) {
    quickClaimSpinnerPending = true;
    if (typeof inputId === 'string') {
      quickClaimSpinnerInputId = inputId;
    }
    refreshQuickClaimSpinner();
    if (quickClaimSpinnerTimeout) {
      clearTimeout(quickClaimSpinnerTimeout);
    }
    quickClaimSpinnerTimeout = setTimeout(() => hideQuickClaimSpinner('', 'timeout'), quickClaimSpinnerTimeoutMs);
  }

  function hideQuickClaimSpinner(inputId, reason) {
    if (inputId && quickClaimSpinnerInputId && inputId !== quickClaimSpinnerInputId) return;
    quickClaimSpinnerPending = false;
    quickClaimSpinnerInputId = '';
    if (quickClaimSpinnerTimeout) {
      clearTimeout(quickClaimSpinnerTimeout);
      quickClaimSpinnerTimeout = null;
    }
    refreshQuickClaimSpinner();
  }

  function clientLog(event, detail) {
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
    fetch('/api/v1/client-log', {
      method: 'POST',
      cache: 'no-store',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        event,
        pageVersion,
        detail: safeDetail.slice(0, 500),
        webCodecs: 'VideoDecoder' in window,
        userAgent: navigator.userAgent
      })
    }).catch(() => {});
  }

  function showEmpty(message, showStart) {
    hideQuickClaimSpinner('', 'stream_empty');
    setTicketInUseSpinner(false);
    emptyMessage.textContent = localizePublicMessage(message);
    startStreamButton.hidden = true;
    emptyState.hidden = false;
    document.body.dataset.streamReady = 'false';
    keepFirstScreenPinned();
  }

  function showStreamWaiting(message) {
    if (hasRenderedFrame) {
      emptyState.hidden = true;
      document.body.dataset.streamReady = 'true';
      setStatus(message);
      keepFirstScreenPinned();
      return;
    }
    showEmpty(message, false);
  }

  function hideEmpty() {
    emptyState.hidden = true;
    document.body.dataset.streamReady = 'true';
    keepFirstScreenPinned();
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

  function connect() {
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
    clearTimeout(reconnectTimer);
    keepFirstScreenPinned();
    setConnected('Savienojas');
    connectedAt = performance.now();
    ws = new WebSocket(socketURL());
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      setConnected('Savienots');
      if (!streamUnsupported) {
        showStreamWaiting(configured ? 'Gaida tiešraides kadru...' : 'Gaida biļetes straumi...');
      }
      connectSpacetimeState().catch((error) => clientLog('spacetime_connect_failed', error && error.message));
      send({ type: 'activity', reason: 'public_connected' });
      connectDirectVideo();
      processInputQueue();
    };
    ws.onmessage = handleMessage;
    ws.onclose = () => {
      setConnected('Savienojas no jauna');
      configured = false;
      streamUnsupported = false;
      keepFirstScreenPinned();
      closeDirectVideo();
      showStreamWaiting('Atjauno straumi...');
      reconnectTimer = setTimeout(connect, 1000);
    };
    ws.onerror = () => {
      setConnected('Savienojuma kļūme');
      if (!streamUnsupported) {
        showStreamWaiting('Atjauno straumi...');
      }
      clientLog('websocket_error', 'socket error');
    };
    connectDirectVideo();
  }

  function resetStreamState(options) {
    const preserveFrame = Boolean(options && options.preserveFrame);
    configured = false;
    configuredAt = 0;
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
    latestStreamStatus = null;
    lastStreamStatusAt = 0;
    decoderMode = 'annexb';
    avcAdapterTried = false;
    avcDescription = null;
    avcSps = null;
    avcPps = null;
    if (!preserveFrame) {
      hasRenderedFrame = false;
    }
    closeDecoder();
  }

  function restartStream(reason, options) {
    if (streamUnsupported) return;
    const now = performance.now();
    if (now - lastRestartAt < 5000) return;
    lastRestartAt = now;
    clientLog('video_stream_restart', reason);
    closeDirectVideo();
    resetStreamState({ preserveFrame: Boolean(options && options.preserveFrame) });
    showStreamWaiting('Atjauno straumi...');
    setTimeout(connectDirectVideo, 250);
  }

  function closeDirectVideo() {
    closeDecoder();
    if (videoWs) {
      try { videoWs.close(); } catch (_) {}
      videoWs = null;
    }
  }

  function connectDirectVideo() {
    if (videoWs && (videoWs.readyState === WebSocket.OPEN || videoWs.readyState === WebSocket.CONNECTING)) return;
    closeDirectVideo();
    document.body.dataset.videoPath = 'https-h264';
    videoWs = new WebSocket(streamURL());
    videoWs.binaryType = 'arraybuffer';
    videoWs.onopen = () => {
      videoConnectedAt = performance.now();
      showStreamWaiting('Saņem video konfigurāciju...');
      requestKeyframe('video_socket_open');
    };
    videoWs.onmessage = handleVideoSocketMessage;
    videoWs.onclose = () => {
      if (ws && ws.readyState !== WebSocket.CLOSED && ws.readyState !== WebSocket.CLOSING) {
        setTimeout(connectDirectVideo, 1000);
      }
    };
    videoWs.onerror = () => {
      clientLog('direct_video_websocket_error', 'socket error');
    };
  }

  function sendVideoSignal(value) {
    if (videoWs && videoWs.readyState === WebSocket.OPEN) {
      videoWs.send(JSON.stringify(value));
      return true;
    }
    return false;
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
    sendVideoSignal({ type: 'client_log', event, detail: safeDetail.slice(0, 500) });
    clientLog(event, detail);
  }

  function requestKeyframe(reason) {
    if (!sendVideoSignal({ type: 'keyframe', reason })) {
      send({ type: 'keyframe', reason });
    }
  }

  function requestKeyframeDebounced(reason, minIntervalMs) {
    const now = performance.now();
    if (now - lastRecoveryKeyframeAt < minIntervalMs) return false;
    lastRecoveryKeyframeAt = now;
    requestKeyframe(reason);
    return true;
  }

  function requestServerRecoveryDebounced(reason) {
    const now = performance.now();
    if (now - lastRecoveryServerRecoverAt < recoveryServerRecoverDebounceMs) return false;
    lastRecoveryServerRecoverAt = now;
    sendVideoClientLog('h264_server_recover_requested', reason);
    if (!sendVideoSignal({ type: 'recover_stream', reason })) {
      send({ type: 'keyframe', reason });
    }
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
      needsKeyFrame,
      firstFrameReceived,
      hasRenderedFrame,
      latestStreamStatus
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
    return true;
  }

  async function handleVideoSocketMessage(event) {
    if (typeof event.data === 'string') {
      let msg;
      try { msg = JSON.parse(event.data); } catch (_) { return; }
      if (!checkServerVersion(msg)) return;
      if (msg.type === 'config') {
        await configureDecoder(msg);
      } else if (msg.type === 'stream_status') {
        handleStreamStatus(msg);
      } else if (msg.type === 'state' || msg.type === 'phone' || msg.type === 'health') {
        handleMessage({ data: event.data });
      }
      return;
    }
    if (!configured) return;
    const frame = parseFrameEnvelope(event.data);
    if (!acceptFreshFrame(frame)) return;
    if (decoderMode === 'avc') {
      decodeAvcFrame(frame);
      return;
    }
    try {
      decoder.decode(new EncodedVideoChunk({ type: frame.kind, timestamp: frame.timestamp, data: frame.data }));
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
    } catch (error) {
      sendVideoClientLog('decoder_decode_failed', error && error.message || 'decode failed');
      needsKeyFrame = true;
      requestKeyframe('h264_avc_decode_failed');
    }
  }

  function handleStreamStatus(msg) {
    latestStreamStatus = msg;
    lastStreamStatusAt = performance.now();
    publishStreamDebug();
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
    streamSize = { width, height };
    currentStreamEpoch = Number(config.streamEpoch || 0);
    lastAcceptedFrameSequence = options.preserveSequence ? previousSequence : 0;
    lastAcceptedFrameTimestamp = options.preserveSequence ? previousTimestamp : 0;
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
        try {
          ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
          lastFrameAt = performance.now();
          lastDecodedFrameAt = lastFrameAt;
          firstFrameReceived = true;
          hasRenderedFrame = true;
          hideEmpty();
          publishStreamDebug();
        } finally {
          frame.close();
        }
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
    closeDecoder();
    decoderMode = 'avc';
    decoder = new VideoDecoder({
      output: (frame) => {
        try {
          ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
          lastFrameAt = performance.now();
          lastDecodedFrameAt = lastFrameAt;
          firstFrameReceived = true;
          hasRenderedFrame = true;
          hideEmpty();
          publishStreamDebug();
        } finally {
          frame.close();
        }
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

  function send(value) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(value));
      return true;
    }
    return false;
  }

  async function fetchAuthSessionToken() {
    if (!usesDirectSpacetimeAuth()) {
      throw new Error('Direct SpacetimeAuth is disabled for this ticket session.');
    }
    const response = await fetch('/api/v1/auth/session', { cache: 'no-store' });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok || !payload.ok || !payload.spacetime || !payload.spacetime.token) {
      throw new Error(payload.message || payload.error || 'SpacetimeAuth session is unavailable.');
    }
    localStorage.setItem(spacetimeTokenKey, payload.spacetime.token);
    if (payload.spacetime.expiresAt) {
      localStorage.setItem(spacetimeTokenExpiryKey, payload.spacetime.expiresAt);
    }
    return payload.spacetime.token;
  }

  async function spacetimeToken() {
    const existing = localStorage.getItem(spacetimeTokenKey) || '';
    if (existing) return existing;
    return fetchAuthSessionToken();
  }

  async function connectSpacetimeState() {
    if (!usesDirectSpacetimeAuth() || spacetimeClient || !window.TicketSpacetime) return;
    const token = await spacetimeToken();
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
        if (detail) clientLog('spacetime_client_status', `${status}:${detail}`);
      }
    });
    spacetimeClient.connect();
  }

  async function syncServerState(reason) {
    send({ type: 'state_refresh', reason: reason || 'spacetime_mutation' });
    const response = await fetch('/api/v1/state', { cache: 'no-store' });
    const payload = await response.json().catch(() => ({}));
    if (response.ok && payload.ok && payload.state) {
      currentState = payload.state;
      rememberServerClock(currentState);
      renderState();
    }
  }

  async function runSpacetimeMutation(action, reason) {
    await connectSpacetimeState();
    if (!spacetimeClient) throw new Error('Spacetime connection is unavailable.');
    await action(spacetimeClient);
    await syncServerState(reason);
  }

  async function runControlMutation(reason, fallbackPath, fallbackBody) {
    return postJSON(fallbackPath, fallbackBody || {});
  }

  function nextInputId() {
    inputSeq += 1;
    return `${cfg.sessionId || 'ticket'}-${Date.now().toString(36)}-${inputSeq}`;
  }

  function inputQueueSize() {
    return inputQueue.length + (inputInFlight ? 1 : 0);
  }

  function queueInput(value) {
    if (inputQueueSize() >= inputQueueLimit) {
      setStatus('Pieskārienu rinda ir pilna. Uzgaidi mirkli un mēģini vēlreiz.');
      return '';
    }
    const inputId = value.inputId || nextInputId();
    inputQueue.push({
      ...value,
      inputId,
      retryCount: value.retryCount || 0
    });
    processInputQueue();
    return inputId;
  }

  function queueTap(screenPoint, options) {
    const value = {
      type: 'tap',
      x: screenPoint.x,
      y: screenPoint.y
    };
    if (options && options.snapTarget) {
      value.snapTarget = options.snapTarget;
    }
    return queueInput(value);
  }

  function queueQuickClaimTap(screenPoint, options) {
    return queueInput({
      type: 'quick_claim_tap',
      x: screenPoint.x,
      y: screenPoint.y,
      snapTarget: options && options.snapTarget ? options.snapTarget : 'control_code_button'
    });
  }

  function inputCanStartWithoutControl(input) {
    return input && input.type === 'quick_claim_tap';
  }

  function currentUserCanSendInput() {
    return currentUserOwnsControl(currentControl(currentState)) || performance.now() < localControlSendGraceUntil;
  }

  function cancelPendingInputs(reason) {
    if (inputInFlight && inputInFlight.timeout) {
      clearTimeout(inputInFlight.timeout);
    }
    const hadPending = Boolean(inputInFlight) || inputQueue.length > 0 || quickClaimSpinnerPending;
    inputInFlight = null;
    inputQueue.length = 0;
    localControlSendGraceUntil = 0;
    hideQuickClaimSpinner('', reason || 'input_cancelled');
    if (hadPending) {
      clientLog('input_queue_cancelled', reason || 'control_lost');
    }
  }

  function processInputQueue() {
    if (inputInFlight || inputQueue.length === 0) return;
    const nextInput = inputQueue[0];
    if (!currentUserCanSendInput() && !inputCanStartWithoutControl(nextInput)) {
      cancelPendingInputs('control_lost_before_send');
      setStatus('Ievade atcelta, jo kontroles režīms vairs nav aktīvs.');
      return;
    }
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      setStatus('Gaida savienojumu, lai nosūtītu pieskārienu.');
      return;
    }
    inputInFlight = inputQueue.shift();
    if (!send(inputInFlight)) {
      inputQueue.unshift(inputInFlight);
      inputInFlight = null;
      setStatus('Gaida savienojumu, lai nosūtītu pieskārienu.');
      return;
    }
    inputInFlight.timeout = setTimeout(() => retryOrDropInput(inputInFlight.inputId), inputAckTimeoutMs);
  }

  function retryOrDropInput(inputId) {
    if (!inputInFlight || inputInFlight.inputId !== inputId) return;
    if (!currentUserCanSendInput() && !inputCanStartWithoutControl(inputInFlight)) {
      cancelPendingInputs('control_lost_retry');
      setStatus('Ievade atcelta, jo kontroles režīms vairs nav aktīvs.');
      return;
    }
    const timedOut = inputInFlight;
    inputInFlight = null;
    if (timedOut.retryCount < inputRetryLimit) {
      timedOut.retryCount += 1;
      delete timedOut.timeout;
      inputQueue.unshift(timedOut);
      setStatus('Pieskāriens netika apstiprināts, mēģina vēlreiz.');
      processInputQueue();
      return;
    }
    hideQuickClaimSpinner(inputId, 'input_timeout');
    setStatus('Pieskāriens netika apstiprināts.');
    processInputQueue();
  }

  function finishInput(inputId, accepted, reason) {
    if (!inputInFlight || inputInFlight.inputId !== inputId) return;
    if (inputInFlight.timeout) {
      clearTimeout(inputInFlight.timeout);
    }
    hideQuickClaimSpinner(inputId, accepted ? 'input_result' : 'input_rejected');
    inputInFlight = null;
    if (!accepted) {
      setStatus(reason === 'not_active_controller'
        ? 'Ievade netiek pieņemta, kamēr nav pārņemts kontroles koda režīms.'
        : 'Pieskāriens netika pieņemts.');
    } else if (inputQueue.length > 0) {
      setStatus(`Nosūta pieskārienus: ${inputQueue.length} gaida.`);
    }
    processInputQueue();
  }

  function handleInputMessage(msg) {
    if (msg.type === 'input_result') {
      finishInput(String(msg.inputId || ''), msg.accepted !== false, msg.reason || '');
      return true;
    }
    if (msg.type === 'input' && msg.accepted === false) {
      finishInput(String(msg.inputId || ''), false, msg.reason || '');
      if (!msg.inputId) {
        setStatus('Ievade netiek pieņemta, kamēr nav pārņemts kontroles koda režīms.');
      }
      return true;
    }
    return msg.type === 'input';
  }

  async function handleMessage(event) {
    if (typeof event.data === 'string') {
      let msg;
      try { msg = JSON.parse(event.data); } catch (_) { return; }
      if (msg.type === 'config') {
        configureStreamInfo(msg);
      } else if (msg.type === 'state') {
        currentState = msg.state;
        rememberServerClock(currentState);
        renderState();
      } else if (msg.type === 'health') {
        if (msg.data && msg.data.message) {
          setStatus(msg.data.message);
          if (msg.data.streamActive === false && !streamUnsupported) {
            showStreamWaiting(`${localizePublicMessage(msg.data.message)} Restartē...`);
          }
        }
      } else if (msg.type === 'phone') {
        setStatus(msg.message || '');
      } else if (handleInputMessage(msg)) {
        return;
      }
      return;
    }
    clientLog('unexpected_binary_frame', 'binary frame arrived on control socket');
  }

  function configureStreamInfo(config) {
    if (config.width && config.height && !configured) {
      canvas.width = config.width;
      canvas.height = config.height;
      streamSize = { width: config.width, height: config.height };
      resizeCanvasBox();
    }
    if (config.type === 'config' && videoWs && videoWs.readyState === WebSocket.OPEN) {
      configureDecoder(config).catch((error) => sendVideoClientLog('decoder_config_failed', error && error.message || 'config failed'));
    }
  }

  function renderState() {
    const state = currentState;
    if (!state) return;
    rememberServerClock(state);
    const control = currentControl(state);
    const selfControl = currentUserOwnsControl(control);
    const otherControl = control && !selfControl;
    const activeControlEmail = control && control.email ? String(control.email).trim().toLowerCase() : '';

    if ((lastSelfControl && !selfControl) || (lastActiveControlEmail && lastActiveControlEmail !== activeControlEmail && !selfControl)) {
      cancelPendingInputs('control_lost_state_update');
    }
    lastSelfControl = Boolean(selfControl);
    lastActiveControlEmail = activeControlEmail;

    setTicketInUseSpinner(Boolean(otherControl));

    claimButton.hidden = Boolean(control);
    extendButton.hidden = !selfControl || control.extended;
    releaseButton.hidden = !selfControl;

    if (control) {
      const remaining = Math.max(0, Math.ceil((control.remainingMs || 0) / 1000));
      timer.hidden = false;
      timer.textContent = `${remaining}s`;
      timer.classList.toggle('urgent', remaining <= 10);
      setStatus(selfControl ? 'Tev ir privāta tālruņa kontrole.' : `${control.email} ir privāta tālruņa kontrole.`);
    } else {
      timer.hidden = true;
      timer.classList.remove('urgent');
      setStatus('Vispārīga skatīšanās');
    }

    renderPresence(state.viewers || []);
  }

  function rememberServerClock(state) {
    const parsed = Date.parse(state && state.serverTime);
    if (Number.isFinite(parsed)) {
      serverClockSkewMs = parsed - Date.now();
    }
  }

  function serverNow() {
    return Date.now() + serverClockSkewMs;
  }

  function currentControl(state) {
    const control = state && state.activeControl;
    if (!control) return null;
    const expiresAt = Date.parse(control.expiresAt);
    if (!Number.isFinite(expiresAt)) return control;
    const remainingMs = Math.max(0, expiresAt - serverNow());
    if (remainingMs <= 0) return null;
    return { ...control, remainingMs };
  }

  function renderPresence(viewers) {
    presence.textContent = '';
    const title = document.createElement('div');
    title.textContent = `${viewers.length} lapā`;
    presence.appendChild(title);
    viewers.forEach((viewer) => {
      const row = document.createElement('div');
      row.className = 'presence-item';
      const email = document.createElement('span');
      email.textContent = viewer.email;
      const mark = document.createElement('span');
      mark.textContent = viewer.sessionId === cfg.sessionId ? 'tu' : 'skatās';
      row.append(email, mark);
      presence.appendChild(row);
    });
  }

  async function postJSON(path, body) {
    const response = await fetch(path, {
      method: 'POST',
      cache: 'no-store',
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : '{}'
    });
    const text = await response.text();
    let payload = {};
    try {
      payload = text ? JSON.parse(text) : {};
    } catch (_) {
      payload = { ok: false, message: text || 'Pieprasījums neizdevās' };
    }
    if (!response.ok || !payload.ok) {
      throw new Error(payload.message || payload.error || 'Pieprasījums neizdevās');
    }
    currentState = payload.state;
    rememberServerClock(currentState);
    renderState();
    return payload;
  }

  async function ensureControl() {
    const control = currentControl(currentState);
    const selfControl = currentUserOwnsControl(control);
    if (selfControl) return;
    if (!claimPromise) {
      claimPromise = runControlMutation('control_claim', '/api/v1/control/claim').finally(() => {
        claimPromise = null;
      });
    }
    await claimPromise;
    localControlSendGraceUntil = performance.now() + 4000;
  }

  function quickClaimControl(options) {
    if (options && options.tap) {
      setStatus('Atver kontroles kodu...');
      return queueQuickClaimTap(options.tap, { snapTarget: options.snapTarget });
    }
    return '';
  }

  async function claimControl(options) {
    await ensureControl();
    if (options && options.tap) {
      return queueTap(options.tap, { snapTarget: options.snapTarget });
    }
    return '';
  }

  claimButton.addEventListener('click', () => claimControl().catch((error) => setStatus(error.message)));
  extendButton.addEventListener('click', () => runControlMutation('control_extend', '/api/v1/control/extend').catch((error) => setStatus(error.message)));
  releaseButton.addEventListener('click', () => {
    cancelPendingInputs('control_release_requested');
    runControlMutation('control_release', '/api/v1/control/release')
      .then(() => cancelPendingInputs('control_release_confirmed'))
      .catch((error) => setStatus(error.message));
  });
  startStreamButton.addEventListener('click', () => restartStream('manual_start'));

  function point(event) {
    const rect = canvas.getBoundingClientRect();
    const width = canvas.width;
    const height = canvas.height;
    return {
      x: Math.round(((event.clientX - rect.left) / rect.width) * width),
      y: Math.round(((event.clientY - rect.top) / rect.height) * height)
    };
  }

  function firstClaimCandidateZone(screenPoint) {
    const width = canvas.width || streamSize.width || 1;
    const height = canvas.height || streamSize.height || 1;
    const relativeX = screenPoint.x / width;
    const relativeY = screenPoint.y / height;
    if (relativeX >= 0 && relativeX <= quickClaimMaxX && relativeY >= 0 && relativeY <= quickClaimMaxY) {
      return 'top_left_quarter';
    }
    if (
      relativeX >= controlCodeButtonMinX &&
      relativeX <= controlCodeButtonMaxX &&
      relativeY >= controlCodeButtonMinY &&
      relativeY <= controlCodeButtonMaxY
    ) {
      return 'control_code_button_geometry';
    }
    return '';
  }

  canvas.addEventListener('pointerdown', (event) => {
    if (!configured) return;
    if (event.button != null && event.button !== 0) return;
    const control = currentControl(currentState);
    const start = point(event);
    const selfControl = currentUserOwnsControl(control);
    pointerStart = {
      ...start,
      clientX: event.clientX,
      clientY: event.clientY,
      pointerId: event.pointerId,
      pointerType: event.pointerType || 'mouse',
      selfControl: Boolean(selfControl),
      otherControl: Boolean(control && !selfControl),
      claimZone: !control ? firstClaimCandidateZone(start) : '',
      at: performance.now()
    };
    if (event.pointerType === 'mouse') {
      event.preventDefault();
      canvas.setPointerCapture(event.pointerId);
    }
  });

  canvas.addEventListener('pointermove', (event) => {
    if (!pointerStart || pointerStart.pointerId !== event.pointerId) return;
    if (pointerStart.pointerType === 'mouse') return;
    const dx = event.clientX - pointerStart.clientX;
    const dy = event.clientY - pointerStart.clientY;
    if (Math.abs(dy) >= streamVerticalPanThresholdPx && Math.abs(dy) > Math.abs(dx) * streamVerticalPanDominance) {
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
      if (pointerStart.selfControl) {
        queueTap(end);
      } else if (pointerStart.otherControl) {
        setStatus('Biļete pašlaik tiek izmantota.');
      } else if (pointerStart.claimZone) {
        showQuickClaimSpinner('');
        const inputId = quickClaimControl({ tap: { x: pointerStart.x, y: pointerStart.y }, snapTarget: 'control_code_button' });
        if (inputId) {
          showQuickClaimSpinner(inputId);
        } else {
          hideQuickClaimSpinner('', 'claim_not_queued');
        }
      } else {
        setStatus('Pirms pieskaries tālrunim, pārņem kontroles koda režīmu.');
      }
    } else {
      if (event.cancelable) event.preventDefault();
      setStatus('Atbalstīti ir tikai pieskārieni.');
      clientLog('blocked_gesture', distance < maxTapTravelPx ? 'long_press' : 'swipe');
    }
    pointerStart = null;
  });

  canvas.addEventListener('pointercancel', () => {
    pointerStart = null;
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

  function serverFrameAge(status) {
    if (!status) return -1;
    const value = Number(status.lastFrameAgoMillis);
    return Number.isFinite(value) ? value : -1;
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

  function chaseLiveStream() {
    if (streamUnsupported) return;
    if (!ws || ws.readyState === WebSocket.CLOSED || ws.readyState === WebSocket.CLOSING) {
      connect();
      return;
    }
    if (ws.readyState !== WebSocket.OPEN) return;
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
      if (pendingAge > streamStaleServerRecoverMs || backendInactive) {
        requestServerRecoveryDebounced('h264_start_pending');
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
        requestServerRecoveryDebounced('first_frame_server_recover');
      }
      return;
    }

    const decodedAge = lastDecodedFrameAt > 0 ? now - lastDecodedFrameAt : 0;
    const sequenceStalledAge = lastPacketSequenceAdvancedAt > 0 ? now - lastPacketSequenceAdvancedAt : 0;
    const sequenceStalled = lastPacketAt > 0 && sequenceStalledAge > streamStaleKeyframeMs && decodedAge > streamStaleKeyframeMs;
    const visibleStaleAge = Math.max(decodedAge, serverStale ? serverAge : 0, sequenceStalled ? sequenceStalledAge : 0);
    if (visibleStaleAge <= streamStaleKeyframeMs && !backendInactive) return;

    if (visibleStaleAge > streamStaleKeyframeMs) {
      if (requestKeyframeDebounced('stale_video_frames', recoveryKeyframeDebounceMs)) {
        clientLog('stale_video_frames', 'fresh_frame_requested');
      }
    }
    if (visibleStaleAge > streamStaleDecoderResetMs || (lastPacketAt > lastDecodedFrameAt && decodedAge > streamStaleDecoderResetMs)) {
      resetDecoderForRecovery('stale_decoder_recovery');
    }
    if (visibleStaleAge > streamStaleVideoReconnectMs) {
      reconnectVideoForRecovery('stale_video_frames');
    }
    if (visibleStaleAge > streamStaleServerRecoverMs || backendInactive) {
      requestServerRecoveryDebounced('stale_frames_server_recover');
    }
  }

  window.addEventListener('resize', resizeCanvasBox);
  window.addEventListener('scroll', updateDetailsReveal, { passive: true });
  if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', resizeCanvasBox);
    window.visualViewport.addEventListener('scroll', resizeCanvasBox);
  }
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      scheduleFirstScreenPin(false);
      refreshHealth();
      connectSpacetimeState().catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));
      connect();
    }
  });
  window.addEventListener('pageshow', () => scheduleFirstScreenPin(true));
  window.addEventListener('pagehide', () => {
    if (spacetimeClient && typeof spacetimeClient.disconnectPresence === 'function') {
      spacetimeClient.disconnectPresence();
    }
  });
  window.addEventListener('load', () => scheduleFirstScreenPin(true));
  setInterval(() => {
    if (spacetimeClient && typeof spacetimeClient.heartbeat === 'function') {
      spacetimeClient.heartbeat(true);
    }
    send({ type: 'activity', reason: 'public_heartbeat' });
  }, 15000);
  setInterval(refreshHealth, 15000);
  setInterval(() => {
    if (currentState && currentState.activeControl) renderState();
  }, 1000);
  setInterval(chaseLiveStream, 1000);
  updateViewportVars();
  scheduleFirstScreenPin(true);
  updateDetailsReveal();
  resizeCanvasBox();
  showEmpty('Savienojas...', false);
  refreshHealth();
  connectSpacetimeState().catch((error) => clientLog('spacetime_connect_failed', error && error.message));
  connect();

  async function startAdmin() {
    const memberForm = document.getElementById('memberForm');
    const memberEmail = document.getElementById('memberEmail');
    const memberRole = document.getElementById('memberRole');
    const membersEl = document.getElementById('adminMembers');
    const stateEl = document.getElementById('adminState');
    const revokeButton = document.getElementById('adminRevoke');
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
    const simSetup = document.querySelector('[data-simulator-setup="true"]');
    const simSetupSummary = document.getElementById('simSetupSummary');
    const simSetupPackages = document.getElementById('simSetupPackages');
    const simSetupScreenshot = document.getElementById('simSetupScreenshot');
    const simSetupRefreshButton = document.getElementById('simSetupRefresh');
    const simSetupTextForm = document.getElementById('simSetupTextForm');
    const simSetupText = document.getElementById('simSetupText');
    const simSetupLastInput = document.getElementById('simSetupLastInput');
    let simSetupDisplay = { width: 720, height: 1280 };
    let simSetupPointer = null;
    let simSetupLongPressTimer = null;
    const simSetupTapMaxDistance = 12;
    const simSetupLongPressDelayMs = 650;

    async function load() {
      const [stateResponse, backendResponse] = await Promise.all([
        fetch('/api/v1/admin/state', { cache: 'no-store' }),
        fetch('/api/v1/admin/phone/backends', { cache: 'no-store' })
      ]);
      const payload = await stateResponse.json();
      const backendsPayload = await backendResponse.json();
      if (!stateResponse.ok || !payload.ok) throw new Error(payload.message || 'load failed');
      if (!backendResponse.ok || !backendsPayload.ok) throw new Error(backendsPayload.message || 'backend load failed');
      renderAdmin(payload.state, payload.phone, backendsPayload);
      if (simSetup) loadSimulatorSetup().catch((error) => renderSimulatorSetupError(error.message || 'Simulator control unavailable'));
    }

    function renderAdmin(state, phone, backendsPayload) {
      const phoneHealth = parsePhoneHealth(state.phone && state.phone.healthJson);
      renderStatus(state, phone, phoneHealth);
      renderBackends(backendsPayload);
      membersEl.textContent = '';
      (state.members || []).forEach((member) => {
        const row = document.createElement('div');
        row.className = 'admin-member';
        const main = document.createElement('div');
        main.className = 'admin-member-main';
        const email = document.createElement('span');
        email.className = 'admin-member-email';
        email.textContent = member.email;
        const updated = document.createElement('span');
        updated.className = 'admin-muted';
        updated.textContent = member.active === false ? 'Inactive' : relativeTime(member.updatedAt);
        main.append(email, updated);
        const role = document.createElement('span');
        role.className = `admin-pill ${member.role || 'member'}`;
        role.textContent = member.role;
        const remove = document.createElement('button');
        remove.type = 'button';
        remove.textContent = 'Remove';
        remove.disabled = member.role === 'owner';
        remove.addEventListener('click', async () => {
          await runAdminAction(remove, 'Removing member...', async () => {
            if (usesDirectSpacetimeAuth()) {
              await runSpacetimeMutation((client) => client.removeMember(member.email), 'admin_member_remove');
            } else {
              await apiFetch(`/api/v1/admin/members?email=${encodeURIComponent(member.email)}`, { method: 'DELETE', cache: 'no-store' });
            }
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
        if (usesDirectSpacetimeAuth()) {
          await runSpacetimeMutation((client) => client.upsertMember(memberEmail.value, memberRole.value), 'admin_member_upsert');
        } else {
          await apiFetch('/api/v1/admin/members', {
            method: 'POST',
            cache: 'no-store',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ email: memberEmail.value, role: memberRole.value })
          });
        }
        memberEmail.value = '';
        showNotice('Member saved');
        await load();
      });
    });

    revokeButton.addEventListener('click', async () => {
      await runAdminAction(revokeButton, 'Revoking control...', async () => {
        await apiFetch('/api/v1/admin/control/revoke', { method: 'POST', cache: 'no-store' });
        showNotice('Control revoked');
        await load();
      });
    });

    function renderStatus(state, phone, phoneHealth) {
      const members = state.members || [];
      const viewers = state.viewers || [];
      const activeViewers = viewers.filter((viewer) => viewer.connected !== false);
      const activeControl = state.activeControl || null;
      const phoneRecord = state.phone || {};
      const rootCapture = phoneHealth.rootCapture || {};
      const pipeline = phoneHealth.streamPipeline || {};
      const inputGate = phoneHealth.inputGate || {};
      const lockdown = phoneHealth.notificationLockdown || {};

      memberSummary.textContent = `${members.length} member${members.length === 1 ? '' : 's'} configured`;
      sessionSummary.textContent = activeControl
        ? `${activeControl.email} has control for ${Math.max(0, Math.ceil((activeControl.remainingMs || 0) / 1000))}s`
        : 'No active control claim';

      phoneState.textContent = phone && phone.connected ? 'Connected' : phoneRecord.desiredState || 'Idle';
      phoneDetail.textContent = `${phoneRecord.attachName || phoneRecord.id || 'Pixel'} · seen ${relativeTime(phoneRecord.lastSeenAt || (phone && phone.lastSeenAt))}`;

      streamState.textContent = rootCapture.active ? 'Live' : (phoneHealth.streamActive ? 'Starting' : 'Idle');
      streamDetail.textContent = rootCapture.message || pipeline.secureWindowCaptureBypassMessage || 'Waiting for viewers';

      controlState.textContent = activeControl ? 'Claimed' : 'Open';
      controlDetail.textContent = activeControl
        ? `${activeControl.email}${activeControl.extended ? ' · extended' : ''}`
        : `${activeViewers.length} viewer${activeViewers.length === 1 ? '' : 's'} on page`;

      safetyState.textContent = lockdown.active ? 'Locked down' : 'Ready';
      safetyDetail.textContent = inputGate.reason
        ? `Input gate: ${inputGate.reason}`
        : (lockdown.reason || 'Tap-only controls');

      revokeButton.disabled = !activeControl;
      revokeButton.classList.toggle('is-danger', Boolean(activeControl));
    }

    function renderBackends(payload) {
      const activeId = payload.activeBackendId || '';
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
    }

    async function loadSimulatorSetup() {
      if (!simSetup) return;
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

    function parsePhoneHealth(raw) {
      if (!raw) return {};
      try {
        const parsed = JSON.parse(raw);
        return parsed && parsed.data ? parsed.data : parsed;
      } catch (_) {
        return {};
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
          button.disabled = wasDisabled || (button.id === 'adminRevoke' && !button.classList.contains('is-danger'));
        }
      }
    }

    load().catch((error) => {
      showNotice(error.message || 'Load failed', true);
      stateEl.textContent = error.stack || error.message;
    });
  }
})();
