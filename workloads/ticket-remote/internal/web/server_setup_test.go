package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestRelayViewerCountTracksUniqueBrowserSessions(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "pixel")

	server.addRelayViewer("session-a")
	server.addRelayViewer("session-a")
	server.addRelayViewer("session-b")

	if got := server.relay.Snapshot().Viewers; got != 2 {
		t.Fatalf("relay viewers after two unique sessions = %d, want 2", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 2 {
		t.Fatalf("relay viewers after closing one socket from session-a = %d, want 2", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after closing session-a = %d, want 1", got)
	}
	server.removeRelayViewer("missing-session")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after closing an unknown session = %d, want 1", got)
	}
	server.removeRelayViewer("session-b")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after closing all sessions = %d, want 0", got)
	}
}

func TestPublicHTTPRedirectsToHTTPS(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "pixel",
			AttachName:       "Pixel",
			BaseURL:          "http://pixel.test",
			DefaultBackendID: "pixel",
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://ticket.jolkins.id.lv/", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "https://ticket.jolkins.id.lv/" {
		t.Fatalf("Location = %q", got)
	}
}

func TestHTTPSResponsesIncludeSafetyHeaders(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "pixel",
			AttachName:       "Pixel",
			BaseURL:          "http://pixel.test",
			DefaultBackendID: "pixel",
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	required := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains; preload",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "no-referrer",
		"Permissions-Policy":        "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=()",
	}
	for header, want := range required {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("%s = %q want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, snippet := range []string{"default-src 'self'", "script-src 'self' 'unsafe-eval'", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "connect-src 'self' https: wss:"} {
		if !strings.Contains(csp, snippet) {
			t.Fatalf("CSP missing %q: %s", snippet, csp)
		}
	}
	if !strings.Contains(rec.Body.String(), `nonce="`) {
		t.Fatalf("expected rendered scripts to carry CSP nonce")
	}
}

func TestOwnerSimulatorSetupWorksWhenViviMissing(t *testing.T) {
	server, runner := newSimulatorSetupTestServer(t, "android-sim")

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/phone/setup/status", nil)
	statusReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	statusRec := httptest.NewRecorder()
	server.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status code = %d body = %s", statusRec.Code, statusRec.Body.String())
	}
	var status struct {
		Packages map[string]struct {
			Installed bool `json:"installed"`
		} `json:"packages"`
	}
	if err := json.NewDecoder(statusRec.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Packages["vivi"].Installed {
		t.Fatalf("ViVi should be missing in setup status: %#v", status.Packages)
	}
	if !status.Packages["accrescent"].Installed || !status.Packages["aurora"].Installed || !status.Packages["controller"].Installed {
		t.Fatalf("setup packages missing: %#v", status.Packages)
	}

	screenshotReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/phone/setup/screenshot", nil)
	screenshotReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	screenshotRec := httptest.NewRecorder()
	server.ServeHTTP(screenshotRec, screenshotReq)
	if screenshotRec.Code != http.StatusOK || screenshotRec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("screenshot status=%d content-type=%q body=%q", screenshotRec.Code, screenshotRec.Header().Get("Content-Type"), screenshotRec.Body.String())
	}
	if got := screenshotRec.Body.String(); got != fakePNG {
		t.Fatalf("screenshot bytes = %q", got)
	}

	inputReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/phone/setup/input", strings.NewReader(`{"type":"tap","x":12,"y":34}`))
	inputReq.Header.Set("Content-Type", "application/json")
	inputReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	inputRec := httptest.NewRecorder()
	server.ServeHTTP(inputRec, inputReq)
	if inputRec.Code != http.StatusOK {
		t.Fatalf("input status = %d body = %s", inputRec.Code, inputRec.Body.String())
	}
	if !runner.called("shell", "input", "tap", "12", "34") {
		t.Fatalf("tap command was not sent, calls=%#v", runner.callsSnapshot())
	}
}

func TestOwnerSimulatorControlSupportsGeneralInputs(t *testing.T) {
	server, runner := newSimulatorSetupTestServer(t, "android-sim")
	cases := []struct {
		name string
		body string
		call []string
	}{
		{
			name: "drag",
			body: `{"type":"drag","startX":10,"startY":20,"endX":40,"endY":80,"durationMs":250}`,
			call: []string{"shell", "input", "swipe", "10", "20", "40", "80", "250"},
		},
		{
			name: "long_press",
			body: `{"type":"long_press","x":12,"y":34,"durationMs":700}`,
			call: []string{"shell", "input", "swipe", "12", "34", "12", "34", "700"},
		},
		{
			name: "app_switch",
			body: `{"type":"key","key":"app_switch"}`,
			call: []string{"shell", "input", "keyevent", "KEYCODE_APP_SWITCH"},
		},
		{
			name: "wake",
			body: `{"type":"key","key":"wake"}`,
			call: []string{"shell", "input", "keyevent", "KEYCODE_WAKEUP"},
		},
		{
			name: "delete",
			body: `{"type":"key","key":"delete"}`,
			call: []string{"shell", "input", "keyevent", "KEYCODE_DEL"},
		},
		{
			name: "space",
			body: `{"type":"key","key":"space"}`,
			call: []string{"shell", "input", "keyevent", "KEYCODE_SPACE"},
		},
		{
			name: "text",
			body: `{"type":"text","text":"Vivi Latvija 123"}`,
			call: []string{"shell", "input", "text", "Vivi%sLatvija%s123"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/phone/setup/input", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("input status = %d body = %s", rec.Code, rec.Body.String())
			}
			if !runner.called(tc.call...) {
				t.Fatalf("%s command was not sent, calls=%#v", tc.name, runner.callsSnapshot())
			}
		})
	}
}

func TestSimulatorSetupRequiresOwner(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "android-sim")
	for _, tc := range []struct {
		email string
		role  string
	}{
		{"admin@example.com", state.RoleAdmin},
		{"member@example.com", state.RoleMember},
	} {
		t.Run(tc.role, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/phone/setup/status", nil)
			req.Header.Set("X-Ticket-Remote-Email", tc.email)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status for %s = %d body = %s", tc.email, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSimulatorSetupRequiresActiveSimulatorBackend(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "pixel")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/phone/setup/status", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestSimulatorSetupRejectsUnsafeInputs(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "android-sim")
	cases := []string{
		`{"type":"pinch","x":1,"y":1}`,
		`{"type":"tap","x":9000,"y":1}`,
		`{"type":"swipe","startX":1,"startY":1,"endX":1,"endY":1200,"durationMs":300}`,
		`{"type":"swipe","startX":1,"startY":1,"endX":2,"endY":2,"durationMs":5000}`,
		`{"type":"long_press","x":9000,"y":1,"durationMs":650}`,
		`{"type":"long_press","x":1,"y":1,"durationMs":2000}`,
		`{"type":"key","key":"power"}`,
		`{"type":"text","text":"hello; reboot"}`,
	}
	cases = append(cases, `{"type":"text","text":"`+strings.Repeat("a", setupTextMaxRunes+1)+`"}`)
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/phone/setup/input", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s status = %d response = %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestSimulatorSetupOpenShortcuts(t *testing.T) {
	server, runner := newSimulatorSetupTestServer(t, "android-sim")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/phone/setup/open", strings.NewReader(`{"target":"aurora-vivi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !runner.called("shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", "market://details?id=com.pv.vivi", "-p", setupPackageAurora) {
		t.Fatalf("aurora intent was not sent, calls=%#v", runner.callsSnapshot())
	}
}

func TestAdminPageShowsSimulatorSetupOnlyForOwner(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "android-sim")
	ownerReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	ownerReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerRec := httptest.NewRecorder()
	server.ServeHTTP(ownerRec, ownerReq)
	ownerBody := ownerRec.Body.String()
	if ownerRec.Code != http.StatusOK || !strings.Contains(ownerBody, `data-simulator-setup="true"`) || !strings.Contains(ownerBody, `Owner simulator control`) || !strings.Contains(ownerBody, `data-sim-key="app_switch"`) || !strings.Contains(ownerBody, `data-sim-key="delete"`) || !strings.Contains(ownerBody, `data-sim-key="space"`) {
		t.Fatalf("owner admin page status=%d body=%s", ownerRec.Code, ownerRec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminReq.Header.Set("X-Ticket-Remote-Email", "admin@example.com")
	adminRec := httptest.NewRecorder()
	server.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin page status = %d body = %s", adminRec.Code, adminRec.Body.String())
	}
	if strings.Contains(adminRec.Body.String(), `data-simulator-setup="true"`) {
		t.Fatalf("non-owner admin page should not render simulator setup: %s", adminRec.Body.String())
	}
}

func TestTicketViewerAdminLinkOnlyShowsForAdmins(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "pixel")

	memberReq := httptest.NewRequest(http.MethodGet, "/", nil)
	memberReq.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	memberRec := httptest.NewRecorder()
	server.ServeHTTP(memberRec, memberReq)
	if memberRec.Code != http.StatusOK {
		t.Fatalf("member page status = %d body = %s", memberRec.Code, memberRec.Body.String())
	}
	if strings.Contains(memberRec.Body.String(), `class="admin-link"`) {
		t.Fatalf("member viewer should not render an unusable admin link: %s", memberRec.Body.String())
	}

	ownerReq := httptest.NewRequest(http.MethodGet, "/", nil)
	ownerReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerRec := httptest.NewRecorder()
	server.ServeHTTP(ownerRec, ownerReq)
	if ownerRec.Code != http.StatusOK {
		t.Fatalf("owner page status = %d body = %s", ownerRec.Code, ownerRec.Body.String())
	}
	if !strings.Contains(ownerRec.Body.String(), `class="admin-link"`) {
		t.Fatalf("owner viewer should render admin link: %s", ownerRec.Body.String())
	}
}

func TestAdminSimulatorControlStaticAssetsWirePointerAndKeyboard(t *testing.T) {
	body, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)
	for _, snippet := range []string{
		"pointerdown",
		"pointerup",
		"keydown",
		"type: 'tap'",
		"type: 'drag'",
		"type: 'long_press'",
		"key: 'delete'",
		"key: 'space'",
	} {
		if !strings.Contains(js, snippet) {
			t.Fatalf("admin simulator control JS missing %q", snippet)
		}
	}
}

func TestTicketViewerKeepsSafariOnCodeRequestPath(t *testing.T) {
	jsBody, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssBody, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	spinnerBody, err := staticFS.ReadFile("static/quick-claim-spinner.svg")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBody)
	css := string(cssBody)
	spinner := string(spinnerBody)
	for _, snippet := range []string{
		"const requestCodeButton = requireElement('#requestControlCode', 'requestControlCode')",
		"const codeDialog = requireElement('#controlCodeDialog', 'controlCodeDialog')",
		"const codeDigits = requireElement('#controlCodeDigits', 'controlCodeDigits')",
		"function sanitizeControlDigits(value)",
		"function renderControlCodeRequest(request)",
		"function submitControlCodeRequest()",
		"function closeCurrentControlCode(openNext)",
		"postJSON('/api/v1/control-code/request', { digits })",
		"postJSON('/api/v1/control-code/close', { requestId: requestID })",
		"msg.type !== 'control_code_request'",
		"codeResultArea.addEventListener('click'",
		"setStatus('Kontroles kodu pieprasi ar pogu zem biļetes.')",
		"window.TicketSpacetime.create",
		"/api/v1/auth/session",
		"/api/v1/auth/start",
		"startAuthRedirect()",
		"beginSpacetimeLogin",
		"window.addEventListener('error'",
		"window.addEventListener('unhandledrejection'",
		"function requireElement(selector, label)",
		"function showFatalPage(message)",
		"function safeWebSocket(url, label)",
		"function renderDecodedFrame(frame, source)",
		"control_message_failed",
		"video_message_failed",
		"decoded_frame_render_failed",
		"missing_admin_dom",
		"finishSpacetimeCallback().catch(showAuthError)",
		"location.replace('/')",
		"let screenEngaged = false",
		"let screenWakeLock = null",
		"function engageTicketScreen(reason)",
		"function requestScreenWakeLock(reason)",
		"navigator.wakeLock.request('screen')",
		"function releaseScreenWakeLock(reason)",
		"function requestTicketFullscreen(reason)",
		"requestFullscreen({ navigationUI: 'hide' })",
		"function ticketViewportRect()",
		"window.visualViewport.offsetLeft",
		"window.visualViewport.offsetTop",
		"--ticket-viewport-width",
		"--ticket-viewport-height",
		"--ticket-viewport-left",
		"--ticket-viewport-top",
		"function toolbarCollapseAnchorPx()",
		"Math.min(96, Math.max(24, viewportHeight() * 0.12))",
		"clientLog('toolbar_collapse_anchor'",
		"document.body.classList.add('screen-engaged')",
		"for (const eventName of ['pointerdown', 'touchend', 'click', 'keydown'])",
		"const streamResumeSpinner = document.getElementById('streamResumeSpinner')",
		"function sameEmail(left, right)",
		"function renderPanelSummary(viewers, visibleViewerCount)",
		"function renderViewerSummary(viewers, visibleViewerCount)",
		"function isTechnicalPublicStatusMessage(value)",
		"isTechnicalPublicStatusMessage(msg.data.message)",
		"const streamLive = rootCapture.active || phoneHealth.streamVerdict === 'live' || pipeline.streamVerdict === 'live'",
		"streamState.textContent = streamLive ? 'Live'",
		"streamLive ? 'Ticket stream is live'",
		"function activeViewers(viewers)",
		"function activeViewerPresence(state)",
		"function preserveCurrentFrame(reason)",
		"function redrawPreservedFrame()",
		"const wasEmptyVisible = !emptyState.hidden",
		"if (wasEmptyVisible) keepFirstScreenPinned()",
		"function showStreamResumeSpinner()",
		"function streamStatusStale(status)",
		"preserveCurrentFrame('stream_status_stale')",
		"if (!streamStatusStale(freshStreamStatus(performance.now()) || latestStreamStatus))",
		"streamResumeSpinnerVisible: streamResumeSpinnerVisible()",
		"hasFallbackFrame: fallbackFrameAvailable",
		"preserveCurrentFrame('configure_decoder')",
		"redrawPreservedFrame()",
		"stage.style.setProperty('--stream-left'",
		"stage.style.setProperty('--stream-top'",
		"const streamVerticalPanThresholdPx = 6",
		"const streamVerticalPanDominance = 1.1",
		"const FRAME_ENVELOPE_MAGIC = 0x54534632",
		"const FRAME_ENVELOPE_HEADER_BYTES = 29",
		"invalid_tsf2_frame",
		"function annexBNalUnits(data)",
		"function annexBToAvcSample(data)",
		"function configureAvcDecoderFromDescription(config, description)",
		"sendVideoClientLog('h264_decoder_recovery_avc_adapter', reason)",
		"new VideoDecoder({",
		"new EncodedVideoChunk({ type: frame.kind",
		"avc: { format: 'annexb' }",
		"ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)",
		"String(serverVersion).startsWith('ticket-remote-')",
		"let lastPacketAt = 0",
		"let lastDecodedFrameAt = 0",
		"let lastPacketSequenceAdvancedAt = 0",
		"let latestStreamStatus = null",
		"const streamStaleKeyframeMs = 2500",
		"const streamStaleDecoderResetMs = 5000",
		"const streamStaleVideoReconnectMs = 8000",
		"const streamStaleServerRecoverMs = 12000",
		"const streamDecoderStartupGraceMs = 3500",
		"const hiddenVideoCloseDelayMs = 3000",
		"function pauseVideoWhileHidden(reason)",
		"video_stream_paused_hidden",
		"document.visibilityState === 'hidden'",
		"function showStreamWaiting(message)",
		"function handleStreamStatus(msg)",
		"function resetDecoderForRecovery(reason)",
		"function decoderStartupGraceActive(now)",
		"function requestServerRecoveryDebounced(reason)",
		"function chaseLiveStream()",
		"function viewerIsForeground()",
		"document.hasFocus()",
		"requestServerRecoveryDebounced('foreground_video_socket_closed')",
		"send({ type: 'recover_stream', reason })",
		"window.addEventListener('focus'",
		"msg.type === 'stream_status'",
		"sendVideoSignal({ type: 'recover_stream', reason })",
		"restartStream(reason, { preserveFrame: true })",
		"setInterval(chaseLiveStream, 1000)",
		"requestKeyframeDebounced('h264_first_frame_nudge'",
		"if (decoderStartupGraceActive(now))",
		"send({ type: 'heartbeat', reason: 'public_connected' })",
		"send({ type: 'heartbeat', reason: 'public_heartbeat' })",
		"streamVerticalPanThresholdPx",
		"clientLog('stream_vertical_scroll', 'allowed')",
		"canvas.addEventListener('dblclick'",
		"canvas.addEventListener('touchend', blockDoubleTapZoom, { passive: false })",
		"document.addEventListener(eventName, blockStreamGesture, { passive: false })",
	} {
		if !strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"touch-action: pan-y",
		"scroll-snap-type: y proximity",
		"body.screen-engaged",
		"scroll-snap-type: none",
		"--ticket-viewport-width",
		"--ticket-viewport-height",
		"--ticket-viewport-left",
		"--ticket-viewport-top",
		"--ticket-dialog-height",
		"--ticket-toolbar-anchor",
		"overscroll-behavior: none",
		"-webkit-touch-callout: none",
		"-webkit-tap-highlight-color: transparent",
		".stream-resume-spinner",
		".control-code-hotspot",
		".control-code-close-hotspot",
		".control-code-result",
		".code-dialog",
		".code-dialog-field input",
		".panel-summary",
		".panel-summary-item",
		".presence-header",
		"left: calc(var(--stream-left, 0px) + 20px)",
		"top: calc(var(--stream-top, 0px) + 20px)",
		"pointer-events: none",
		"font-variant-numeric: tabular-nums",
		"streamResumeSpinnerRotate",
	} {
		if !strings.Contains(css, snippet) {
			t.Fatalf("ticket viewer CSS missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"left: 50%",
		"top: 50%",
		"margin-left: -27px",
		"margin-top: -27px",
		"background: rgba(2, 3, 4",
	} {
		if strings.Contains(css, snippet) {
			t.Fatalf("ticket viewer stream resume spinner should use top-left quick-spinner styling, found %q", snippet)
		}
	}
	hotspotStart := strings.Index(css, ".control-code-hotspot {")
	if hotspotStart < 0 {
		t.Fatalf("ticket viewer CSS missing isolated control-code hotspot block")
	}
	hotspotEnd := strings.Index(css[hotspotStart:], ".control-code-close-hotspot {")
	if hotspotEnd < 0 {
		t.Fatalf("ticket viewer CSS missing control-code close hotspot block")
	}
	hotspotBlock := css[hotspotStart : hotspotStart+hotspotEnd]
	for _, snippet := range []string{
		"width: 50vw",
		"height: 25vh",
	} {
		if !strings.Contains(hotspotBlock, snippet) {
			t.Fatalf("control-code hotspot block missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		`/static/quick-claim-spinner.svg`,
		`id="streamResumeSpinner"`,
		`id="controlCodeResultArea"`,
		`class="control-code-result"`,
		`id="controlCodeHotspot"`,
		`id="controlCodeCloseHotspot"`,
		`id="requestControlCode"`,
		`id="controlCodeDialog"`,
		`id="controlCodeForm"`,
		`id="controlCodeDigits"`,
		`inputmode="numeric"`,
		`pattern="[0-9]*"`,
		`minlength="2"`,
		`maxlength="9"`,
		`id="closeControlCodeResult"`,
		`id="viewerCount"`,
		`id="viewerCountDetail"`,
		`name="theme-color" content="#020304"`,
		`aria-hidden="true"`,
		`draggable="false" hidden`,
	} {
		if !strings.Contains(indexHTML, snippet) {
			t.Fatalf("ticket viewer HTML missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"<svg",
		"viewBox=\"0 0 64 64\"",
		"fill=\"none\"",
		"feDropShadow",
	} {
		if !strings.Contains(spinner, snippet) {
			t.Fatalf("quick-claim spinner asset missing %q", snippet)
		}
	}
	if !strings.Contains(indexHTML, "maximum-scale=1, user-scalable=no") {
		t.Fatalf("ticket viewer viewport should disable Safari double-tap zoom")
	}
	for _, snippet := range []string{
		`class="panel-summary-item stream-summary"`,
		`.stream-summary`,
		`id="streamStateLabel"`,
		`id="streamStateDetail"`,
		`Pieejama ikvienam lapā`,
		`Biļetes attēls ir aktīvs`,
		`function renderStreamSummary()`,
	} {
		if strings.Contains(indexHTML, snippet) || strings.Contains(js, snippet) || strings.Contains(css, snippet) {
			t.Fatalf("ticket viewer should not expose stale stream summary or copy %q", snippet)
		}
	}
	if !strings.Contains(authRedirectHTML, `name="theme-color" content="#020304"`) {
		t.Fatalf("ticket auth redirect shell should keep the same dark browser theme color")
	}
	if strings.Contains(js, "['touchstart', 'touchmove']") {
		t.Fatalf("ticket viewer should not block all touch movement; vertical scroll must remain available")
	}
	if !strings.Contains(serverVersion, "control-code-request") {
		t.Fatalf("ticket page version should be bumped for control-code request rollout, got %q", serverVersion)
	}
	for _, snippet := range []string{
		`id="extendControl"`,
		`Pagarināt`,
		`const extendButton`,
		`memberExtendControl`,
		`control.extended ? 'Pagarināts laiks'`,
		`already_extended`,
	} {
		if strings.Contains(indexHTML, snippet) || strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer should not expose extension UI or logic %q", snippet)
		}
	}
	for _, snippet := range []string{
		"spacetimeLogin",
		"Pieraksties ar e-pastu",
		"Pierakstīties ar e-pastu",
		"Ja e-pasta saite atveras",
		"send({ type: 'activity', reason: 'public_connected' })",
		"send({ type: 'activity', reason: 'public_heartbeat' })",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer must auto-start SpacetimeAuth instead of showing the old login panel: %q", snippet)
		}
	}
	for _, forbidden := range []struct {
		label string
		body  string
	}{
		{"indexHTML", indexHTML},
		{"app.js", js},
		{"app.css", css},
	} {
		for _, snippet := range []string{
			"claimDialog",
			"showModal",
			"claim-dialog",
			"confirmClaim",
			"claimControl",
			"releaseControl",
			"quickClaimControl",
			"queueQuickClaimTap",
			"quickClaimQueuedOrInFlight",
			"quick_claim_tap",
			"runControlMutation",
			"/api/v1/control/claim",
			"/api/v1/control/release",
			"controlOwner",
			"controlMode",
			"controlTimeDetail",
			"inputQueue",
			"Priv\u0101ta kontroles koda sesija",
			"privacyOverlay",
			"isPrivacyCovered",
			"send({ type: 'tap', x: options.tap.x",
			"RTCPeerConnection",
			"webrtc_ice_config",
			"webrtcVideo",
			"Savieno WebRTC video",
			"TURN",
			"renderPngFrame",
			"isPngStream",
			"control.sessionId === cfg.sessionId && control.email === cfg.email",
			"createImageBitmap",
			"legacy_frame_in_tsf2_stream",
			"version: 'legacy'",
			"configuredFrameEnvelope",
			"|| 'legacy'",
			"left = '-10000px'",
			"MediaProjection fallback",
			"AV1",
			"showUnsupported('Video straume neatnāca laikā. Tālrunim vajag uzmanību.')",
			"Atbalstīti ir tikai pieskārieni.",
			"localStorage.getItem",
			"localStorage.setItem",
			"localStorage.removeItem",
			"sessionStorage.getItem",
			"sessionStorage.setItem",
			"sessionStorage.removeItem",
			"ticket_remote_spacetime_token",
			"ticket_remote_spacetime_token_expires_at",
			"ticket_remote_pkce_verifier",
			"ticket_remote_pkce_state",
			"mozBrightness",
			"AmbientLightSensor",
			"screen.brightness",
			"setBrightness",
		} {
			if strings.Contains(forbidden.body, snippet) {
				t.Fatalf("%s should not contain stale control dialog snippet %q", forbidden.label, snippet)
			}
		}
	}
	if !strings.Contains(serverVersion, "control-code-request") {
		t.Fatalf("ticket page version should be bumped for control-code request rollout, got %q", serverVersion)
	}
	if strings.Contains(indexHTML, `id="webrtcVideo"`) || !strings.Contains(indexHTML, `id="screen"`) {
		t.Fatalf("ticket viewer must render HTTPS H.264 on the canvas, not WebRTC video")
	}
	stageStart := strings.Index(indexHTML, `<section class="stage-page"`)
	panelStart := strings.Index(indexHTML, `<aside id="panel"`)
	resultStart := strings.Index(indexHTML, `id="controlCodeResultArea"`)
	if stageStart < 0 || panelStart < 0 || resultStart < 0 {
		t.Fatalf("ticket viewer missing stage, panel, or control-code result markup")
	}
	if !(stageStart < resultStart && resultStart < panelStart) {
		t.Fatalf("control-code result must render in the stream stage, not the lower panel")
	}
	resultCSSStart := strings.Index(css, ".control-code-result {")
	if resultCSSStart < 0 {
		t.Fatalf("ticket viewer CSS missing control-code result overlay")
	}
	resultCSSEnd := strings.Index(css[resultCSSStart:], ".control-code-result[hidden]")
	if resultCSSEnd < 0 {
		t.Fatalf("ticket viewer CSS missing control-code result hidden rule")
	}
	resultCSS := css[resultCSSStart : resultCSSStart+resultCSSEnd]
	for _, snippet := range []string{
		"position: fixed",
		"left: var(--ticket-viewport-left",
		"top: var(--ticket-viewport-top",
		"width: var(--ticket-viewport-width",
		"height: var(--ticket-viewport-height",
		"z-index: 7",
		"place-items: center",
		"padding: 0",
	} {
		if !strings.Contains(resultCSS, snippet) {
			t.Fatalf("control-code result overlay CSS missing %q", snippet)
		}
	}
	imageCSSStart := strings.Index(css, ".control-code-image {")
	if imageCSSStart < 0 {
		t.Fatalf("ticket viewer CSS missing control-code image block")
	}
	imageCSSEnd := strings.Index(css[imageCSSStart:], ".control-code-value {")
	if imageCSSEnd < 0 {
		t.Fatalf("ticket viewer CSS missing control-code value block")
	}
	imageCSS := css[imageCSSStart : imageCSSStart+imageCSSEnd]
	for _, snippet := range []string{
		"width: var(--stream-width",
		"height: var(--stream-height",
		"max-width: none",
		"object-fit: fill",
	} {
		if !strings.Contains(imageCSS, snippet) {
			t.Fatalf("control-code success image CSS missing %q", snippet)
		}
	}
	if !strings.Contains(css, `.control-code-result[data-status="succeeded"] .control-code-result-status`) ||
		!strings.Contains(css, `.control-code-result[data-status="succeeded"] .control-code-value`) {
		t.Fatalf("successful control-code overlay must hide text chrome around the image")
	}
}

func TestTicketViewerCodeDialogUsesNumericRequestFlow(t *testing.T) {
	body, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)
	for _, snippet := range []string{
		"sanitizeControlDigits(codeDigits.value)",
		"controlCodeStatusRank(request.status)",
		"digits.length < 2 || digits.length > 9",
		"postJSON('/api/v1/control-code/request', { digits })",
		"renderControlCodeRequest(payload.request)",
		"closeCurrentControlCode(false)",
		"scheduleControlCodeTicker(current)",
		"codeResultStatus.hidden = true",
		"codeResultValue.hidden = true",
		"codeResultTimer.hidden = true",
		"postJSON('/api/v1/control-code/close'",
		"const viewers = activeViewerPresence(state)",
		"viewer.label || `Skatītājs ${index + 1}`",
		"controlCodeHotspot.addEventListener('click', requestControlCodeFromHotspot)",
		"controlCodeCloseHotspot.addEventListener('click', closeControlCodeFromHotspot)",
		"codeDialog.addEventListener('click'",
		"event.key === 'Escape'",
	} {
		if !strings.Contains(js, snippet) {
			t.Fatalf("control-code request flow missing %q", snippet)
		}
	}
	hotspotStart := strings.Index(js, "function requestControlCodeFromHotspot(event)")
	if hotspotStart < 0 {
		t.Fatalf("control-code request hotspot handler missing")
	}
	hotspotEnd := strings.Index(js[hotspotStart:], "function closeControlCodeFromHotspot(event)")
	if hotspotEnd < 0 {
		t.Fatalf("control-code close hotspot handler missing")
	}
	hotspotHandler := js[hotspotStart : hotspotStart+hotspotEnd]
	if !strings.Contains(hotspotHandler, "closeCurrentControlCode(false)") {
		t.Fatalf("top-left hotspot should close visible result without immediately opening a new request")
	}
	if strings.Contains(hotspotHandler, "closeCurrentControlCode(true)") {
		t.Fatalf("top-left hotspot should not immediately reopen the request dialog after closing a visible result")
	}
	resultClickStart := strings.Index(js, "codeResultArea.addEventListener('click'")
	if resultClickStart < 0 {
		t.Fatalf("control-code result click handler missing")
	}
	resultClickEnd := strings.Index(js[resultClickStart:], "codeResultClose.addEventListener('click'")
	if resultClickEnd < 0 {
		t.Fatalf("control-code result close handler missing")
	}
	resultClickHandler := js[resultClickStart : resultClickStart+resultClickEnd]
	if strings.Contains(resultClickHandler, "closeCurrentControlCode") {
		t.Fatalf("control-code result body clicks must not close the overlay")
	}
	for _, snippet := range []string{
		"pointerStart.claimZone",
		"showQuickClaimSpinner",
		"quickClaimControl",
		"type: 'quick_claim_tap'",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("control-code request flow should not keep old quick-claim code %q", snippet)
		}
	}
}

func TestTicketSpacetimeModuleDisablesOldControlMutations(t *testing.T) {
	moduleBody, err := os.ReadFile("../../spacetimedb/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	module := string(moduleBody)
	for _, snippet := range []string{
		"const CONTROL_MS = 90 * 1000",
		"throw new SenderError('control_mode_removed')",
		"return stateError(tx, ticket.id, 'control_mode_removed', now)",
		"throw new SenderError('extension_disabled')",
		"return stateError(tx, ticket.id, 'extension_disabled', now)",
	} {
		if !strings.Contains(module, snippet) {
			t.Fatalf("SpacetimeDB module missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"control_claimed",
		"CONTROL_EXTENDED_MS",
		"control_extended",
		"already_extended",
		"extended: true",
	} {
		if strings.Contains(module, snippet) {
			t.Fatalf("SpacetimeDB module should not keep extension behavior %q", snippet)
		}
	}
}

func TestTicketSpacetimeMemberReducersUseServerClockAndConnectionID(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "spacetimedb", "src", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	module := string(body)
	for _, reducer := range []string{
		"member_heartbeat_presence",
		"member_disconnect_presence",
		"member_claim_control",
		"member_release_control",
		"member_revoke_control",
		"member_upsert_member",
		"member_remove_member",
	} {
		start := strings.Index(module, `name: named('`+reducer+`')`)
		if start < 0 {
			t.Fatalf("missing reducer %s", reducer)
		}
		chunk := module[start:min(len(module), start+700)]
		if strings.Contains(chunk, "now: t.string()") {
			t.Fatalf("%s must not accept browser-supplied now", reducer)
		}
		if strings.Contains(chunk, "sessionId: t.string()") {
			t.Fatalf("%s must not accept browser-supplied sessionId", reducer)
		}
	}
	for _, snippet := range []string{"ctx.timestamp", "ctx.connectionId", "serverNow(ctx)", "connectionSessionId(ctx)"} {
		if !strings.Contains(module, snippet) {
			t.Fatalf("module missing %q", snippet)
		}
	}
}

func TestTicketSpacetimeLiveStateIsRedacted(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "spacetimedb", "src", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	module := string(body)
	if !strings.Contains(module, "function publicSnapshot(") {
		t.Fatalf("SpacetimeDB module must define a redacted public snapshot")
	}
	start := strings.Index(module, "function publicSnapshot(")
	chunk := module[start:min(len(module), start+1600)]
	for _, forbidden := range []string{"members:", "viewers:", "sessionId:", "baseUrl:", "healthJson:", "lastError:"} {
		if strings.Contains(chunk, forbidden) {
			t.Fatalf("public snapshot must not expose %q in %s", forbidden, chunk)
		}
	}
	for _, required := range []string{"viewerCount:", "ownerEmail:", "desiredState:", "stateBackend:"} {
		if !strings.Contains(chunk, required) {
			t.Fatalf("public snapshot missing %q in %s", required, chunk)
		}
	}
	for _, required := range []string{"viewerPresence:", "label: `Skatītājs ${index + 1}`"} {
		if !strings.Contains(chunk, required) {
			t.Fatalf("public snapshot missing anonymized presence %q in %s", required, chunk)
		}
	}
	if !strings.Contains(module, "serialize(publicSnapshot(tx, id, now))") {
		t.Fatalf("live_state must be written from the redacted public snapshot")
	}
}

func TestSpacetimeReducersUseEmailWideControlOwnership(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "spacetimedb", "src", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, snippet := range []string{
		"const actorEmail = cleanEmail(args.email);",
		"if (actorEmail && active.email !== actorEmail) return stateError(tx, ticket.id, 'not_controller', now);",
	} {
		if !strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB reducer missing email-wide ownership snippet %q", snippet)
		}
	}
	for _, snippet := range []string{
		"active.sessionId !== args.sessionId || active.email !== cleanEmail(args.email)",
		"args.sessionId && (active.sessionId !== args.sessionId || active.email !== cleanEmail(args.email))",
	} {
		if strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB reducer should not require same browser session: %q", snippet)
		}
	}
}

func TestSpacetimeAuthDirectClientContract(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "spacetimedb", "src", "index.ts"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, snippet := range []string{
		"{ name: named('live_state'), public: true }",
		"clientEmailFromAuth(ctx, ticketId)",
		"payload.email_verified !== true",
		"export const memberHeartbeatPresence",
		"writeLiveState(ctx, ticket.id, now)",
	} {
		if !strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB auth/direct-client contract missing %q", snippet)
		}
	}
	jsBody, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	clientBody, err := staticFS.ReadFile("static/spacetime-client.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBody)
	for _, snippet := range []string{
		"startAuthRedirect()",
		"beginSpacetimeLogin",
		"/api/v1/auth/start",
		"clearLocalAuthState()",
		"/api/v1/auth/session",
		"postJSON('/api/v1/control-code/request', { digits })",
		"postJSON('/api/v1/control-code/close', { requestId: requestID })",
		"usesDirectSpacetimeAuth()",
		"apiFetch('/api/v1/admin/members'",
		"apiFetch(`/api/v1/admin/members?email=${encodeURIComponent(member.email)}`",
		"activeMembers(state)",
		"send({ type: 'state_refresh'",
		"adminRefreshMs = 5000",
	} {
		if !strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer SpacetimeAuth JS missing %q", snippet)
		}
	}
	authRedirectIndex := strings.Index(js, "if (!cfg.authenticated)")
	spacetimeUnavailableIndex := strings.Index(js, "let spacetimeDirectUnavailable = false")
	if authRedirectIndex < 0 || spacetimeUnavailableIndex < 0 {
		t.Fatalf("ticket viewer SpacetimeAuth JS missing auth redirect or direct state initialization")
	}
	if spacetimeUnavailableIndex > authRedirectIndex {
		t.Fatalf("SpacetimeAuth callback state must be initialized before unauthenticated redirect starts")
	}
	for _, forbidden := range []string{
		"runSpacetimeMutation((client) => client.upsertMember",
		"runSpacetimeMutation((client) => client.removeMember",
		"runSpacetimeMutation((client) => client.revokeControl",
	} {
		if strings.Contains(string(jsBody), forbidden) {
			t.Fatalf("admin mutations must go through ticket_remote so server state stays synchronized: %q", forbidden)
		}
	}
	for _, snippet := range []string{
		"DbConnection.builder()",
		"memberHeartbeatPresence",
		"ticketremoteLiveState",
	} {
		if !strings.Contains(string(clientBody), snippet) {
			t.Fatalf("ticket Spacetime browser bundle missing %q", snippet)
		}
	}
	for _, forbidden := range []string{
		"claimControl()",
		"releaseControl(",
		"revokeControl(",
		"memberClaimControl",
	} {
		if strings.Contains(string(clientBody), forbidden) {
			t.Fatalf("ticket Spacetime browser bundle should not expose old control wrapper %q", forbidden)
		}
	}
	if strings.Contains(indexHTML, "Cloudflare Access") || strings.Contains(string(jsBody), "Cloudflare Access") {
		t.Fatalf("ticket viewer must not mention Cloudflare Access login")
	}
	if strings.Contains(string(jsBody), "spacetimeLogin") || strings.Contains(string(jsBody), "Pierakstīties ar e-pastu") {
		t.Fatalf("ticket viewer must auto-start SpacetimeAuth instead of showing a local login panel")
	}
}

func TestAdminMembersRouteAddsAndRemovesMember(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "pixel")

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"new.member@example.com","role":"member"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	addRec := httptest.NewRecorder()
	server.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", addRec.Code, addRec.Body.String())
	}
	var added apiResponse
	if err := json.NewDecoder(addRec.Body).Decode(&added); err != nil {
		t.Fatal(err)
	}
	if _, ok := added.State.Member("new.member@example.com"); !ok {
		t.Fatalf("added member missing from state: %#v", added.State.Members)
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/members?email=new.member%40example.com", nil)
	removeReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	removeRec := httptest.NewRecorder()
	server.ServeHTTP(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove status = %d body = %s", removeRec.Code, removeRec.Body.String())
	}
	var removed apiResponse
	if err := json.NewDecoder(removeRec.Body).Decode(&removed); err != nil {
		t.Fatal(err)
	}
	if _, ok := removed.State.Member("new.member@example.com"); ok {
		t.Fatalf("removed member still active in state: %#v", removed.State.Members)
	}
}

func TestAdminMembersRouteRequiresAdmin(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "pixel")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"blocked@example.com","role":"member"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin add status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestDevAdminMemberDeleteUsesConfiguredIdentityNotTargetEmailQuery(t *testing.T) {
	server, _ := newSimulatorSetupTestServer(t, "pixel")

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"delete.target@example.com","role":"member"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addRec := httptest.NewRecorder()
	server.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", addRec.Code, addRec.Body.String())
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/members?email=delete.target%40example.com", nil)
	removeRec := httptest.NewRecorder()
	server.ServeHTTP(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove status = %d body = %s", removeRec.Code, removeRec.Body.String())
	}
	var removed apiResponse
	if err := json.NewDecoder(removeRec.Body).Decode(&removed); err != nil {
		t.Fatal(err)
	}
	if _, ok := removed.State.Member("delete.target@example.com"); ok {
		t.Fatalf("removed member still active in state: %#v", removed.State.Members)
	}
}

func TestSpacetimeAuthUnauthenticatedAdminServesRedirectShell(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:           "spacetime",
			OIDCIssuer:     "https://auth.spacetimedb.com/oidc",
			OIDCClientID:   "client_test",
			OIDCScope:      "openid profile email",
			OIDCRedirect:   "http://ticket.test/auth/callback",
			AuthCookieName: "ticket_remote_auth",
		},
		Phone: config.PhoneConfig{
			BackendID:         "pixel",
			AttachName:        "Pixel",
			BaseURL:           "http://pixel.test",
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin unauth status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`window.TICKET_REMOTE_CONFIG`,
		`"authenticated":false`,
		`/static/app.js`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("admin unauth shell missing %q in %s", expected, body)
		}
	}
	if strings.Contains(body, "Admin access is required") || strings.Contains(body, `class="admin-shell"`) {
		t.Fatalf("unauthenticated admin should receive auth redirect shell, got %s", body)
	}
}

func TestSpacetimeAuthServerSessionKeepsAuthenticatedHTTPWorking(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:              "spacetime",
			OIDCIssuer:        "https://auth.spacetimedb.com/oidc",
			OIDCClientID:      "client_test",
			OIDCScope:         "openid profile email",
			OIDCRedirect:      "http://ticket.test/auth/callback",
			AuthCookieName:    "ticket_remote_auth",
			SessionSigningKey: "test-signing-key",
		},
		Phone: config.PhoneConfig{
			BackendID:         "pixel",
			AttachName:        "Pixel",
			BaseURL:           "http://pixel.test",
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := server.auth.IssueServerSession(auth.Identity{
		Email:         "ticket@jolkins.id.lv",
		Subject:       "user_123",
		EmailVerified: true,
	}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	spacetimePayload, _ := payload["spacetime"].(map[string]any)
	if spacetimePayload["token"] != nil {
		t.Fatalf("server session token must not be exposed as direct Spacetime token: %#v", spacetimePayload)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthReq.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRec.Code, healthRec.Body.String())
	}
}

func newSimulatorSetupTestServer(t *testing.T, activeBackendID string) (*Server, *fakeSimulatorSetupRunner) {
	t.Helper()
	store := state.NewMemoryStore()
	activeURL := "http://sim.test"
	activeName := "Android simulator"
	if activeBackendID == "pixel" {
		activeURL = "http://pixel.test"
		activeName = "Pixel"
	}
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  activeBackendID,
		PhoneBaseURL:    activeURL,
		PhoneAttachName: activeName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "admin@example.com", state.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "member@example.com", state.RoleMember); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:  activeBackendID,
		AttachName: activeName,
		BaseURL:    activeURL,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:  activeBackendID,
			AttachName: activeName,
			BaseURL:    activeURL,
			Backends: []config.PhoneBackend{
				{ID: "android-sim", AttachName: "Android simulator", BaseURL: "http://sim.test"},
				{ID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
			},
			DefaultBackendID:  "android-sim",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
		SimulatorSetup: config.SimulatorSetupConfig{
			BackendID: "android-sim",
			ADBTarget: "ticket_android_sim:5555",
			ADBPath:   "adb",
			Timeout:   time.Second,
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	runner := newFakeSimulatorSetupRunner()
	server.setupRunner = runner
	return server, runner
}

const fakePNG = "\x89PNG\r\n\x1a\nfake"

type fakeSimulatorSetupRunner struct {
	mu    sync.Mutex
	calls [][]string
}

func newFakeSimulatorSetupRunner() *fakeSimulatorSetupRunner {
	return &fakeSimulatorSetupRunner{}
}

func (r *fakeSimulatorSetupRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string(nil), args...))
	r.mu.Unlock()
	switch strings.Join(args, "\x00") {
	case "get-state":
		return []byte("device\n"), nil
	case "shell\x00wm\x00size":
		return []byte("Physical size: 1080x1920\nOverride size: 720x1280\n"), nil
	case "shell\x00wm\x00density":
		return []byte("Physical density: 420\nOverride density: 240\n"), nil
	case "shell\x00pm\x00path\x00com.pv.vivi":
		return nil, errFakePackageMissing
	case "shell\x00pm\x00path\x00app.accrescent.client":
		return []byte("package:/data/app/accrescent/base.apk\n"), nil
	case "shell\x00pm\x00path\x00com.aurora.store":
		return []byte("package:/data/app/aurora/base.apk\n"), nil
	case "shell\x00pm\x00path\x00lv.jolkins.pixelorchestrator":
		return []byte("package:/data/app/controller/base.apk\n"), nil
	case "exec-out\x00screencap\x00-p":
		return []byte(fakePNG), nil
	default:
		return []byte("ok\n"), nil
	}
}

func (r *fakeSimulatorSetupRunner) called(args ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	want := strings.Join(args, "\x00")
	for _, call := range r.calls {
		if strings.Join(call, "\x00") == want {
			return true
		}
	}
	return false
}

func (r *fakeSimulatorSetupRunner) callsSnapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, 0, len(r.calls))
	for _, call := range r.calls {
		out = append(out, append([]string(nil), call...))
	}
	return out
}

var errFakePackageMissing = &fakeADBError{"package missing"}

type fakeADBError struct {
	message string
}

func (e *fakeADBError) Error() string {
	return e.message
}
