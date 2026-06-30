package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func normalizeStaticJSForContains(value string) string {
	normalized := make([]rune, 0, len(value))
	for _, r := range strings.ToLower(value) {
		switch {
		case unicode.IsSpace(r):
			continue
		case r == '"' || r == '`':
			normalized = append(normalized, '\'')
		default:
			normalized = append(normalized, r)
		}
	}
	return string(normalized)
}

func staticContains(source, snippet string) bool {
	return strings.Contains(normalizeStaticJSForContains(source), normalizeStaticJSForContains(snippet))
}

func staticCSSContains(source, snippet string) bool {
	normalizedSource := strings.ReplaceAll(normalizeStaticJSForContains(source), "'", "")
	normalizedSnippet := strings.ReplaceAll(normalizeStaticJSForContains(snippet), "'", "")
	return strings.Contains(normalizedSource, normalizedSnippet)
}

func TestRelayViewerCountTracksUniqueBrowserSessions(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

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

func TestSpacetimeClientFiltersExpiredViewerPresence(t *testing.T) {
	body, err := staticFS.ReadFile("static/spacetime-client.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)
	for _, snippet := range []string{
		"function rowExpiresAfter(row, nowMs)",
		"Date.parse(String(row && (row.expiresAt || row.expires_at) || \"\"))",
		"row.connected !== false && rowExpiresAfter(row, nowMs)",
		"viewerCount: viewers.length",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("Spacetime client must hide expired viewer presence, missing %q", snippet)
		}
	}
}

func TestSpacetimeConnectionHooksDoNotCreateViewerPresence(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	start := strings.Index(module, "pub fn identity_connected")
	end := strings.Index(module, "pub fn ticketremote_register_service_identity")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("Spacetime connection hook block not found")
	}
	chunk := module[start:end]
	for _, forbidden := range []string{
		"upsertPresence(",
		"disconnectPresence(",
		"connectionSessionId(ctx)",
	} {
		if strings.Contains(chunk, forbidden) {
			t.Fatalf("raw Spacetime connect/disconnect hooks must not change viewer presence, found %q", forbidden)
		}
	}
	for _, required := range []string{
		"pub fn identity_connected(_ctx: &ReducerContext) {}",
		"pub fn identity_disconnected(_ctx: &ReducerContext) {}",
		"pub fn ticketremote_member_set_stream_focus(",
	} {
		source := chunk
		if required == "pub fn ticketremote_member_set_stream_focus(" {
			source = module
		}
		if !strings.Contains(source, required) {
			t.Fatalf("connection hook block missing %q", required)
		}
	}
}

func TestRelayViewerCountPublishesPhoneBrokerPresence(t *testing.T) {
	presenceUpdates := make(chan int, 4)
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/phone/leases/ticket" || r.URL.Path == "/api/v1/phone/leases/ticket/release" {
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path != "/api/v1/ticket/presence" {
			t.Fatalf("broker path = %s", r.URL.Path)
		}
		var req struct {
			Viewers int `json:"viewers"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode presence: %v", err)
		}
		presenceUpdates <- req.Viewers
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer broker.Close()

	server := newTicketSetupTestServer(t, "pixel")
	server.cfg.Phone.BrokerBaseURL = broker.URL

	server.addRelayViewer("session-a")
	expectBrokerPresence(t, presenceUpdates, 1)

	server.addRelayViewer("session-b")
	expectBrokerPresence(t, presenceUpdates, 2)

	server.removeRelayViewer("session-a")
	expectBrokerPresence(t, presenceUpdates, 1)

	server.removeRelayViewer("session-b")
	expectBrokerPresence(t, presenceUpdates, 0)
}

func TestRelayViewerPublishesPhoneBrokerTicketLeaseLifecycle(t *testing.T) {
	events := make(chan brokerLeaseEvent, 8)
	broker := newTicketLeaseBrokerRecorder(t, events)
	defer broker.Close()

	server := newTicketSetupTestServer(t, "pixel")
	server.cfg.Phone.BrokerBaseURL = broker.URL

	server.addRelayViewer("session-a")
	acquire := expectBrokerLeaseEvent(t, events, "/api/v1/phone/leases/ticket")
	if acquire.LeaseID != "viewer:session-a" || acquire.RequestID != "session-a" || acquire.Reason != "stream_viewer" || acquire.TTLMillis <= 0 {
		t.Fatalf("viewer lease acquire = %#v", acquire)
	}

	server.removeRelayViewer("session-a")
	release := expectBrokerLeaseEvent(t, events, "/api/v1/phone/leases/ticket/release")
	if release.LeaseID != "viewer:session-a" {
		t.Fatalf("viewer lease release = %#v", release)
	}
}

type brokerLeaseEvent struct {
	Path      string
	LeaseID   string `json:"leaseId"`
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
	TTLMillis int64  `json:"ttlMillis"`
}

func newTicketLeaseBrokerRecorder(t *testing.T, events chan<- brokerLeaseEvent) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ticket/presence":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/v1/phone/leases/ticket", "/api/v1/phone/leases/ticket/release":
			var event brokerLeaseEvent
			if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
				t.Fatalf("decode broker lease event: %v", err)
			}
			event.Path = r.URL.Path
			events <- event
			_, _ = w.Write([]byte(`{"ok":true,"state":{"currentOwner":"ticket"},"lease":{"id":"` + event.LeaseID + `","owner":"ticket"}}`))
		default:
			t.Fatalf("broker path = %s", r.URL.Path)
		}
	}))
}

func expectBrokerLeaseEvent(t *testing.T, events <-chan brokerLeaseEvent, path string) brokerLeaseEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Path == path {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for broker lease event %s", path)
		}
	}
}

func expectBrokerLeaseEventWithLease(t *testing.T, events <-chan brokerLeaseEvent, path string, leaseID string) brokerLeaseEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Path == path && event.LeaseID == leaseID {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for broker lease event %s %s", path, leaseID)
		}
	}
}

func expectBrokerPresence(t *testing.T, updates <-chan int, want int) {
	t.Helper()
	select {
	case got := <-updates:
		if got != want {
			t.Fatalf("broker presence = %d, want %d", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for broker presence %d", want)
	}
}

func TestRelayPrewarmUsesBrowserSessionLease(t *testing.T) {
	if got := streamPrewarmRelayLeaseID(" session-a "); got != "session-a" {
		t.Fatalf("stream prewarm lease = %q, want session-a", got)
	}
	if got := controlCodePrepareRelayLeaseID(" session-a "); got != "session-a" {
		t.Fatalf("control-code prepare lease = %q, want session-a", got)
	}
}

func TestRelayPrewarmDoesNotDoubleCountActiveBrowserSession(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPrewarm(streamPrewarmRelayLeaseID("session-a"), time.Hour)
	server.retainRelayViewerForPrewarm(controlCodePrepareRelayLeaseID("session-a"), time.Hour)

	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after active session prewarm = %d, want 1", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after active socket closes while prewarm retained = %d, want 1", got)
	}
	server.releaseRetainedRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after prewarm release = %d, want 0", got)
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
	for _, snippet := range []string{"default-src 'self'", "script-src 'self' 'unsafe-eval'", "style-src 'self' 'nonce-", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "connect-src 'self' https: wss:"} {
		if !strings.Contains(csp, snippet) {
			t.Fatalf("CSP missing %q: %s", snippet, csp)
		}
	}
	if !strings.Contains(rec.Body.String(), `nonce="`) {
		t.Fatalf("expected rendered scripts to carry CSP nonce")
	}
}

func TestVersionedStaticAssetsAreCacheable(t *testing.T) {
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
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
	}, store, phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/static/app.js?v=test-release", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("static app status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") || !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("static app cache-control = %q", got)
	}
	if got := rec.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("static app must not clear browser cache, got %q", got)
	}
}

func TestUnversionedStaticAssetsAreNotLongLived(t *testing.T) {
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
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
	}, store, phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/static/app.js", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("static app status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") || strings.Contains(got, "immutable") {
		t.Fatalf("unversioned static app cache-control = %q", got)
	}
	if got := rec.Header().Get("CDN-Cache-Control"); !strings.Contains(got, "no-store") || strings.Contains(got, "immutable") {
		t.Fatalf("unversioned static app CDN cache-control = %q", got)
	}
}

func TestAssetVersionIsStableDuringProcess(t *testing.T) {
	first := assetVersion()
	time.Sleep(1100 * time.Millisecond)
	second := assetVersion()
	if first == "" || second == "" {
		t.Fatalf("asset version must not be empty: first=%q second=%q", first, second)
	}
	if first != second {
		t.Fatalf("asset version changed within one process: first=%q second=%q", first, second)
	}
}

func TestAdminPageDoesNotExposeRetiredDeviceSetup(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	ownerReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	ownerReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerRec := httptest.NewRecorder()
	server.ServeHTTP(ownerRec, ownerReq)
	ownerBody := ownerRec.Body.String()
	if ownerRec.Code != http.StatusOK {
		t.Fatalf("owner admin page status=%d body=%s", ownerRec.Code, ownerRec.Body.String())
	}
	for _, forbidden := range []string{`data-` + `sim` + `ulator-setup="true"`, `Owner ` + `sim` + `ulator control`, `data-sim-key=`, `/api/v1/admin/phone/setup`} {
		if strings.Contains(ownerBody, forbidden) {
			t.Fatalf("admin page must not render retired device setup %q: %s", forbidden, ownerBody)
		}
	}
}

func TestTicketViewerAdminLinkOnlyShowsForAdmins(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

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
	serverGoBody, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBody)
	css := string(cssBody)
	spinner := string(spinnerBody)
	serverGo := string(serverGoBody)
	for _, snippet := range []string{
		"requestCodeButton = requireElement('#requestControlCode', 'requestControlCode')",
		"codeDialog = requireElement('#controlCodeDialog', 'controlCodeDialog')",
		"codeDigits = requireElement('#controlCodeDigits', 'controlCodeDigits')",
		"function sanitizeControlDigits(value)",
		"function renderControlCodeRequest(request)",
		"ownedControlCodeRequestIDs=new Set",
		"ownedControlCodeRequestIDs.has(String(requestID))",
		"control_code_message_ignored",
		"function closeCurrentControlCode(openNext)",
		"client.requestControlCode(digits)",
		"client.closeControlCode(requestID,\"browser_closed\")",
		"ownerPublicId:localPublicID",
		"codeResultArea.addEventListener('click'",
		"setStatus('Kontroles kodu pieprasi ar pogu zem biļetes.')",
		"window.TicketSpacetime.create",
		"/api/v1/auth/session",
		"/api/v1/auth/start",
		"beginSpacetimeLogin(authReturnTarget())",
		"beginSpacetimeLogin",
		"window.addEventListener('error'",
		"window.addEventListener('unhandledrejection'",
		"function requireElement(selector, label)",
		"function showFatalPage(message)",
		"control_code_request_failed",
		"video_message_failed",
		"decoded_frame_render_failed",
		"location.pathname==='/auth/callback'",
		"location.replace('/')",
		"screenEngaged=false",
		"screenWakeLock=null",
		"function engageTicketScreen(reason)",
		"function requestScreenWakeLock(reason)",
		"navigator.wakeLock.request('screen')",
		"if(!screenWakeLock)return;const lock=screenWakeLock",
		"function openControlCodeDialog()",
		"if(!streamReadyForControlCode())",
		"document.exitFullscreen().catch",
		"function layoutViewportRect()",
		"function toolbarCollapseAnchorPx()",
		"Math.min(96,Math.max(24,viewportHeight()*.12))",
		"function firstScreenAnchorTop()",
		"streamLive=rootCapture.active||phoneHealth.streamVerdict==='live'||pipeline.streamVerdict==='live'",
		"function activeViewers(viewers)",
		"function preserveCurrentFrame(reason)",
		"function redrawPreservedFrame()",
		"function streamStatusStale(status)",
		"preserveCurrentFrame('stream_status_stale')",
		"preserveCurrentFrame('configure_decoder')",
		"Math.abs(dy)>=streamVerticalPanThresholdPx&&Math.abs(dy)>Math.abs(dx)*streamVerticalPanDominance",
		"invalid_tsf2_frame",
		"function findStartCode(data,from)",
		"function configureDecoder(config, options)",
		"sendVideoClientLog('h264_decoder_recovery_avc_adapter', reason)",
		"function connectDirectVideo()",
		"function switchToAvcAdapter(reason)",
		"intentionallyClosedVideoSockets",
		"activeVideoSockets",
		"function closeEarlyVideo(reason)",
		"function claimEarlyVideoSocket()",
		"claimEarlyVideoSocket()",
		"new VideoDecoder({",
		"avc: { format: 'annexb' }",
		"const preferAvc=Boolean(options.preferAvc)",
		"requestReason:`${reason}_avc_adapter`",
		"configure_avc_decoder",
		"ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)",
		"const capturedImage=captureControlCodeResultImage(proof)",
		"await confirmControlCodeBrowserCapture(request,proof)",
		"codeResultImage.src=capturedImage",
		"const sourceCanvas=controlCodeFrozenCandidateFrameForProof(proof)",
		"captureCanvas.width=sourceCanvas.width",
		"captureCanvas.height=sourceCanvas.height",
		"captureContext.drawImage(sourceCanvas,0,0,captureCanvas.width,captureCanvas.height)",
		"function controlCodeMarkerReady(request)",
		"function waitForControlCodeResultScreenshot(request)",
		"status === 'succeeded'",
		"String(serverVersion).startsWith('ticket-remote-')",
		"lastPacketAt=0",
		"lastDecodedFrameAt=0",
		"latestStreamStatus=null",
		"function handleScreenEngagementEvent(event)",
		"relayReportToStreamStatus(state.relayCurrentReport)",
		"function resetDecoderForRecovery(reason)",
		"requestReason:reason",
		"resetDecoderForRecovery(\"first_frame_decoder_reset\")",
		"function pauseVideoWhileHidden(reason)",
		"function controlCodeKeepsVideoAliveWhileHidden()",
		"control_code_capture_keepalive",
		"control_code_wait_reconnect",
		"publishStreamFocus(true,'public_connected')",
		"spacetimeClient.heartbeat(true)",
		"window.visualViewport",
		"clientLog('stream_vertical_scroll', 'allowed')",
		"canvas.addEventListener('dblclick'",
		"idleDisconnected=false",
		"idleDisconnectTimer=null",
		"function expireViewerIdle(reason)",
		"closeEarlyVideo('idle_disconnect')",
		"resetStreamState({preserveFrame:true})",
		"showEmpty('Straume ir apturēta pēc 15 minūtēm bez darbības. Pieskaries Sākt, lai turpinātu.', true)",
		"document.body.dataset.streamFreshness='IDLE_DISCONNECTED'",
		"startStreamButton.addEventListener('click'",
		"function resumeFromIdleDisconnect(reason)",
		"resumeFromIdleDisconnect('manual_start')",
		"normalizeAssetVersionURL()",
		"function normalizeAssetVersionURL()",
		"history.replaceState(history.state, document.title, next.toString())",
		"serverAssetVersion && assetVersion",
		"searchParams.set('v'",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer JS missing %q", snippet)
		}
	}
	foundFirstFrameSignals := false
	for _, snippet := range []string{
		"first_frame_timeout",
		"first_frame_decoder_reset",
		"first_frame_video_reconnect",
		"first_frame_server_recover",
	} {
		if strings.Contains(js, snippet) {
			foundFirstFrameSignals = true
			break
		}
	}
	if !foundFirstFrameSignals {
		t.Fatalf("ticket viewer JS missing first-frame recovery signals")
	}
	for _, snippet := range []string{
		"canvas.toBlob",
		"FileReader",
		"uploadControlCodeCapture",
		"controlCodeCapturedImages",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS must not upload browser image bytes: found %q", snippet)
		}
	}
	for _, snippet := range []string{
		"controlCodeResultImage",
		"lastRenderedFrameEpoch",
		"lastRenderedFrameSequence",
		"minFrameSequence",
		`captureCanvas.toDataURL("image/png")`,
		"confirmControlCodeBrowserCapture",
		"client.confirmControlCodeBrowserCapture(",
		"captureControlCodeResultScreenshot",
	} {
		if !strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS missing local marker-frame screenshot behavior: %q", snippet)
		}
	}
	if strings.Contains(js, "Gaida koda attēlu") {
		t.Fatalf("ticket viewer must not show interim waiting text over the stream while capturing the control-code frame")
	}
	for _, snippet := range []string{
		"touch-action:pan-y",
		"scroll-snap-type:y proximity",
		"body.screen-engaged",
		"scroll-snap-type:none",
		"--ticket-viewport-width",
		"--ticket-viewport-height",
		"--ticket-viewport-left",
		"--ticket-viewport-top",
		"--ticket-dialog-height",
		"--ticket-toolbar-anchor",
		"overscroll-behavior-y:contain",
		"overscroll-behavior:none",
		"-webkit-touch-callout:none",
		"-webkit-tap-highlight-color:transparent",
		".stream-resume-spinner",
		".control-code-hotspot",
		".control-code-close-hotspot",
		".control-code-result",
		".code-dialog",
		".code-dialog-field input",
		".panel-summary",
		".panel-summary-item",
		".presence-header",
		"left:calc(var(--stream-left,0px) + 20px)",
		"top:calc(var(--stream-top,0px) + 20px)",
		"pointer-events:none",
		"font-variant-numeric:tabular-nums",
		"streamResumeSpinnerRotate",
	} {
		if !staticCSSContains(css, snippet) {
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
	hotspotStart := strings.Index(css, ".control-code-hotspot{left:0;")
	if hotspotStart < 0 {
		hotspotStart = strings.Index(css, ".control-code-hotspot,.control-code-close-hotspot{")
		if hotspotStart < 0 {
			hotspotStart = strings.Index(css, ".control-code-hotspot{")
		}
	}
	if hotspotStart < 0 {
		t.Fatalf("ticket viewer CSS missing isolated control-code hotspot block")
	}
	hotspotEnd := strings.Index(css[hotspotStart:], "}")
	if hotspotEnd < 0 {
		t.Fatalf("ticket viewer CSS missing complete control-code hotspot rule")
	}
	hotspotBlock := css[hotspotStart : hotspotStart+hotspotEnd]
	for _, snippet := range []string{
		"width:50vw",
		"height:25vh",
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
	if !strings.Contains(serverVersion, "browser-captured-control-code") {
		t.Fatalf("ticket page version should name the browser-captured control-code path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "root-image") || strings.Contains(serverVersion, "phone-image") {
		t.Fatalf("ticket page version should not name the superseded phone-image path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "emu"+"lator") || strings.Contains(serverVersion, "sim"+"ulator") || strings.Contains(serverVersion, "android-"+"sim") {
		t.Fatalf("ticket page version should not name retired device paths, got %q", serverVersion)
	}
	if !strings.Contains(indexHTML, `<script nonce="{{.Nonce}}" defer src="/static/app.js?v={{.AssetVersion}}"></script>`) {
		t.Fatalf("ticket viewer must keep the app script versioned and cacheable")
	}
	for _, snippet := range []string{
		`<style nonce="{{.Nonce}}">`,
		`.control-code-hotspot`,
		`opacity: 1;`,
		`background: rgba(0, 0, 0, 0.001);`,
		`color: transparent;`,
		`font-size: 0;`,
	} {
		if !strings.Contains(indexHTML, snippet) {
			t.Fatalf("ticket viewer HTML must keep hotspot hit testing alive, missing %q", snippet)
		}
	}
	if !strings.Contains(serverGo, "assetVersionValue = serverVersion") {
		t.Fatalf("ticket asset fallback version must follow the page version so public caches cannot keep an old app.js")
	}
	if strings.Contains(indexHTML, `/static/spacetime-client.js`) || strings.Contains(adminHTML, `/static/spacetime-client.js`) {
		t.Fatalf("ticket pages must not block first video frame behind the Spacetime client script")
	}
	for _, snippet := range []string{
		"function usesDirectSpacetimeAuth()",
		"function loadSpacetimeClientScript()",
		"/static/spacetime-client.js?v=${",
		"function connectSpacetimeState()",
		"spacetimeToken()",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer should defer Spacetime until video is active, missing %q", snippet)
		}
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
	if !strings.Contains(serverVersion, "browser-captured-control-code") {
		t.Fatalf("ticket page version should name the browser-captured control-code path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "root-image") || strings.Contains(serverVersion, "phone-image") {
		t.Fatalf("ticket page version should not name the superseded phone-image path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "emu"+"lator") || strings.Contains(serverVersion, "sim"+"ulator") || strings.Contains(serverVersion, "android-"+"sim") {
		t.Fatalf("ticket page version should not name retired device paths, got %q", serverVersion)
	}
	if strings.Contains(indexHTML, `id="webrtcVideo"`) || !strings.Contains(indexHTML, `id="screen"`) {
		t.Fatalf("ticket viewer must render HTTPS H.264 on the canvas, not WebRTC video")
	}
	if strings.Contains(js, "decoderMode !== 'avc'") {
		t.Fatalf("latest-keyframe decoder reset must apply to every WebCodecs decoder mode")
	}
	for _, snippet := range []string{
		"function lastRenderedVisualAge(now)",
		"const renderedVisualAge = freshness.hasFrame ? Number(freshness.visualAgeMillis || 0) : lastRenderedVisualAge(now)",
		"const localStaleAge = Math.max(decodedAge, renderedVisualAge, sequenceStalled ? sequenceStalledAge : 0)",
		"renderedVisualAge,",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer stale detection must use current rendered-frame age, missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"const streamLiveFreshMaxAgeMs = 1e3",
		"const streamLiveOkMaxAgeMs = 1500",
		"const streamDegradedMaxAgeMs = 2e3",
		"function freshnessStateForVisualAge(ageMs)",
		"function currentRenderedFreshness(now)",
		"function updateStreamFreshnessStatus(reason)",
		"document.body.dataset.streamFreshness",
		"document.body.dataset.streamLive",
		"visualAgeMillis:",
		"browserReceiveToDecodeMillis:",
		"decodeToRenderMillis:",
		"decoderQueueDelayMillis:",
		"streamFreshnessState:",
		"liveLabeled:",
		"LIVE_FRESH",
		"LIVE_OK",
		"DEGRADED",
		"STALE",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer JS missing freshness contract snippet %q", snippet)
		}
	}
	if !staticCSSContains(css, `.stream-resume-spinner`) {
		t.Fatalf("ticket viewer CSS missing stream recovery spinner")
	}
	for _, snippet := range []string{
		`body[data-stream-freshness="STALE"] #screen`,
		`body[data-stream-freshness="STALE"] .stage::after`,
		`body[data-stream-live="false"] .stage::after`,
		`filter:`,
		`opacity:.62`,
		`Straume atjaunojas`,
	} {
		if staticCSSContains(css, snippet) {
			t.Fatalf("ticket viewer CSS must not alter the stream picture during recovery, found %q", snippet)
		}
	}
	for _, snippet := range []string{
		`Straume atjaunojas`,
		`showStreamWaiting('Atjauno straumi...')`,
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS must keep stream recovery quiet over the video, found %q", snippet)
		}
	}
	if !strings.Contains(js, "function showStreamRecovery()") {
		t.Fatalf("ticket viewer JS missing quiet stream recovery helper")
	}
	if !strings.Contains(js, "function showQuietStreamLoading()") {
		t.Fatalf("ticket viewer JS missing quiet stream loading helper")
	}
	if strings.Contains(js, `showEmpty("Savienojas...",false)`) {
		t.Fatalf("ticket viewer JS must keep initial stream loading quiet over the video")
	}
	if strings.Contains(js, "renderControlCodeRequest(codeRequest);\n    setStatus('Tiešraide rāda biļeti.');") {
		t.Fatalf("ticket viewer must not unconditionally label the stream live")
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
		resultCSSStart = strings.Index(css, ".control-code-result{")
	}
	if resultCSSStart < 0 {
		t.Fatalf("ticket viewer CSS missing control-code result overlay")
	}
	resultCSSEnd := strings.Index(css[resultCSSStart:], ".control-code-result[hidden]")
	if resultCSSEnd < 0 {
		t.Fatalf("ticket viewer CSS missing control-code result hidden rule")
	}
	resultCSS := css[resultCSSStart : resultCSSStart+resultCSSEnd]
	for _, snippet := range []string{
		"position: absolute",
		"inset: 0",
		"width: 100%",
		"height: 100%",
		"z-index: 7",
		"place-items: center",
		"padding: 0",
	} {
		if !staticCSSContains(resultCSS, snippet) {
			t.Fatalf("control-code result overlay CSS missing %q", snippet)
		}
	}
	if !staticCSSContains(css, ".control-code-image") || !strings.Contains(indexHTML, "controlCodeResultImage") {
		t.Fatalf("ticket viewer must include the private local control-code result image surface")
	}
	if !staticCSSContains(css, `.control-code-result[data-status="succeeded"] .control-code-result-status`) ||
		!staticCSSContains(css, `.control-code-result[data-status="succeeded"] .panel-detail`) {
		t.Fatalf("successful control-code overlay must hide non-result chrome around the numeric marker")
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
		"ownerPublicId:localPublicID",
		"locallyClosedControlCodeRequestIDs.add(String(requestID))",
		"return String(value || '').replace(/\\D/g, '')",
		"digits.length < 2 || digits.length > 8",
		"client.requestControlCode(digits)",
		"renderControlCodeRequest({requestId:`pending:${Date.now()}`",
		"closeCurrentControlCode(false)",
		"scheduleControlCodeTicker(current)",
		"codeResultValue.hidden=true",
		"codeResultTimer.hidden=false",
		"client.closeControlCode(requestID,\"browser_closed\")",
		"publicPresence=Array.isArray(state&&state.viewerPresence)",
		"activeViewers(state&&state.viewers||[])",
		"controlCodeHotspot.addEventListener('click', requestControlCodeFromHotspot)",
		"controlCodeCloseHotspot.addEventListener('click', closeControlCodeFromHotspot)",
		"controlCodeHotspot.disabled=hotspotUnavailable",
		"controlCodeHotspot.setAttribute('aria-disabled',hotspotUnavailable?'true':'false')",
		"codeDialog.addEventListener('click'",
		"event.key==='Escape'",
		"requestKeyframeDebounced('control_code_request_submitted', 0, true)",
	} {
		if !staticContains(js, snippet) {
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
	if !strings.Contains(hotspotHandler, "reconnectVideoForRecovery(\"control_code_hotspot_wait_for_live_frame\")") {
		t.Fatalf("top-left hotspot should force local video recovery when the rendered frame is stale")
	}
	if strings.Contains(hotspotHandler, "closeCurrentControlCode(true)") {
		t.Fatalf("top-left hotspot should not immediately reopen the request dialog after closing a visible result")
	}
	resultClickStart := strings.Index(js, `codeResultArea.addEventListener("click"`)
	if resultClickStart < 0 {
		t.Fatalf("control-code result click handler missing")
	}
	resultClickEnd := strings.Index(js[resultClickStart:], `codeResultClose.addEventListener("click"`)
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
		"replace(/\\D/g, '').slice(0, 9)",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("control-code request flow should not keep old quick-claim code %q", snippet)
		}
	}
}

func TestTicketSpacetimeModuleDisablesOldControlMutations(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, snippet := range []string{
		"pub fn ticketremote_member_claim_control(",
		"Err(\"control_mode_removed\".into())",
		"Err(\"extension_disabled\".into())",
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
		"stateError(",
	} {
		if strings.Contains(module, snippet) {
			t.Fatalf("SpacetimeDB module should not keep extension behavior %q", snippet)
		}
	}
}

func TestTicketSpacetimeMemberReducersUseServerClockAndConnectionID(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, reducer := range []string{
		"ticketremote_member_set_stream_focus",
		"ticketremote_member_request_control_code",
		"ticketremote_member_confirm_control_code_browser_capture",
		"ticketremote_member_close_control_code",
		"ticketremote_member_claim_control",
		"ticketremote_member_release_control",
		"ticketremote_member_revoke_control",
		"ticketremote_member_upsert_member",
		"ticketremote_member_remove_member",
	} {
		start := strings.Index(module, "pub fn "+reducer+"(")
		if start < 0 {
			t.Fatalf("missing reducer %s", reducer)
		}
		end := strings.Index(module[start+1:], "\n#[spacetimedb::reducer]")
		if end < 0 {
			end = min(len(module)-start, 900)
		} else {
			end++
		}
		chunk := module[start : start+end]
		if strings.Contains(chunk, "nowArg") {
			t.Fatalf("%s must not accept browser-supplied now", reducer)
		}
	}
	for _, snippet := range []string{"ctx.timestamp", "ctx.connection_id()", "fn now(ctx: &ReducerContext)", "fn connection_session_id(ctx: &ReducerContext)"} {
		if !strings.Contains(module, snippet) {
			t.Fatalf("module missing %q", snippet)
		}
	}
}

func TestTicketSpacetimePublicTablesAreRedacted(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	if strings.Contains(module, "ticketremote_live_state") || strings.Contains(module, "stateJson") {
		t.Fatalf("SpacetimeDB module must not publish the old public JSON snapshot")
	}
	viewerChunk := rustItemChunk(t, module, "#[spacetimedb::table(accessor = ticketremote_viewer_public, public")
	for _, forbidden := range []string{"pub email:", "pub displayName:", "pub page:", "pub sessionId:"} {
		if strings.Contains(viewerChunk, forbidden) {
			t.Fatalf("public viewer table must not expose %q in %s", forbidden, viewerChunk)
		}
	}
	for _, required := range []string{"pub id: String", "pub ticketId: String", "pub publicId: String", "pub label: String", "pub expiresAt: String"} {
		if !strings.Contains(viewerChunk, required) {
			t.Fatalf("public viewer table missing %q in %s", required, viewerChunk)
		}
	}
	phoneChunk := rustItemChunk(t, module, "#[spacetimedb::table(accessor = ticketremote_phone_status, public")
	for _, forbidden := range []string{"pub baseUrl:", "pub healthJson:", "pub lastError:"} {
		if strings.Contains(phoneChunk, forbidden) {
			t.Fatalf("public phone table must not expose %q in %s", forbidden, phoneChunk)
		}
	}
	if !strings.Contains(module, "public_presence_row_id(") {
		t.Fatalf("public viewer rows must use opaque row ids")
	}
}

func TestSpacetimeReducersUseEmailWideControlOwnership(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, snippet := range []string{
		"let actor = clean_email(&email);",
		"if !actor.is_empty() && clean_email(&row.email) != actor",
	} {
		if !strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB reducer missing email-wide ownership snippet %q", snippet)
		}
	}
	for _, snippet := range []string{
		"!actor.is_empty() && row.sessionId",
		"row.sessionId != _sessionId",
	} {
		if strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB reducer should not require same browser session: %q", snippet)
		}
	}
}

func TestSpacetimeAuthDirectClientContract(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, snippet := range []string{
		"#[spacetimedb::table(accessor = ticketremote_ticket_summary, public)]",
		"#[spacetimedb::table(accessor = ticketremote_viewer_public, public",
		"#[spacetimedb::table(accessor = ticketremote_phone_status, public)]",
		"client_email_from_auth(ctx, &ticket.id)",
		"payload.get(\"email_verified\").and_then(|v| v.as_bool()) != Some(true)",
		"pub fn ticketremote_member_set_stream_focus",
		"let session_id = non_empty(&sessionId, &connection_session_id(ctx));",
		"upsert_presence(\n            ctx,\n            &ticket.id,\n            &session_id,\n            &email",
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
		"beginSpacetimeLogin(authReturnTarget())",
		"beginSpacetimeLogin",
		"/api/v1/auth/start",
		"clearLocalAuthState()",
		"/api/v1/auth/session",
		"client.requestControlCode(digits)",
		"client.closeControlCode(requestID,\"browser_closed\")",
		"usesDirectSpacetimeAuth()",
		"publishStreamFocus(true,'public_connected')",
		"spacetimeClient.heartbeat(true)",
		"let spacetimeClientConnectPromise=null",
		"if(spacetimeClientConnectPromise)return spacetimeClientConnectPromise",
		"spacetimeClientConnectPromise=(async()=>",
		"loadSpacetimeClientScript()",
		"document.head.appendChild(script)",
		`document.documentElement.dataset.ticketUi="arrow"`,
		"apiFetch('/api/v1/admin/members'",
		"apiFetch(`/api/v1/admin/members?email=${encodeURIComponent(member.email)}`",
		"activeMembers(state)",
		"adminRefreshMs=5e3",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer SpacetimeAuth JS missing %q", snippet)
		}
	}
	authRedirectIndex := strings.Index(normalizeStaticJSForContains(js), "if(!cfg.authenticated)")
	spacetimeUnavailableIndex := strings.Index(normalizeStaticJSForContains(js), "spacetimedirectunavailable")
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
		"memberSetStreamFocus",
		"ticketremote_ticket_summary",
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"onApplied(()=>{if(!applied){applied=true;this.attachStateListeners(connection);}this.publishFocusedState();this.heartbeat(true);})",
	} {
		if !staticContains(string(clientBody), snippet) {
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
	server := newTicketSetupTestServer(t, "pixel")

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
	server := newTicketSetupTestServer(t, "pixel")

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
	server := newTicketSetupTestServer(t, "pixel")

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
	directStore := &spacetimeBackendCountingStore{Store: store}
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
		State: state.StoreConfig{
			Backend:           "spacetime",
			SpacetimeHost:     "https://maincloud.spacetimedb.com",
			SpacetimeDatabase: "ticket-remote-prod-v3",
		},
		Phone: config.PhoneConfig{
			BackendID:         "pixel",
			AttachName:        "Pixel",
			BaseURL:           "http://pixel.test",
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, directStore, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !server.usesDirectSpacetimePresence() {
		t.Fatal("test server should use direct Spacetime presence")
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
	if spacetimePayload["authRequired"] != true {
		t.Fatalf("missing direct Spacetime cookie should require a direct auth refresh: %#v", spacetimePayload)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthReq.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRec.Code, healthRec.Body.String())
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexReq.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	indexRec := httptest.NewRecorder()
	server.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("index status = %d body = %s", indexRec.Code, indexRec.Body.String())
	}
	if !strings.Contains(indexRec.Body.String(), `"authenticated":true`) {
		t.Fatalf("authenticated index should render ticket shell, got %s", indexRec.Body.String())
	}
	if len(indexRec.Result().Cookies()) == 0 {
		t.Fatalf("authenticated index should establish a browser session cookie")
	}
}

func TestNonExpiringServerSessionRefreshesAuthCookieOnCachedIndex(t *testing.T) {
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
		CookieTTL:     config.DurationNever,
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
	token, expiresAt, err := server.auth.IssueServerSession(auth.Identity{
		Email:         "ticket@jolkins.id.lv",
		Subject:       "user_123",
		EmailVerified: true,
	}, config.DurationNever, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !expiresAt.IsZero() {
		t.Fatalf("expiresAt = %s, want no expiry", expiresAt)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	first := httptest.NewRecorder()
	server.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first index status = %d body = %s", first.Code, first.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == "ticket_remote_session" {
			req.AddCookie(cookie)
		}
	}
	second := httptest.NewRecorder()
	server.ServeHTTP(second, req)
	if second.Code != http.StatusOK {
		t.Fatalf("cached index status = %d body = %s", second.Code, second.Body.String())
	}
	var refreshedAuth, refreshedSession bool
	for _, cookie := range second.Result().Cookies() {
		if cookie.Name == "ticket_remote_auth" && cookie.MaxAge == nonExpiringCookieMaxAge {
			refreshedAuth = true
		}
		if cookie.Name == "ticket_remote_session" && cookie.MaxAge == nonExpiringCookieMaxAge {
			refreshedSession = true
		}
	}
	if !refreshedAuth {
		t.Fatalf("cached index did not refresh non-expiring auth cookie: %#v", second.Result().Cookies())
	}
	if !refreshedSession {
		t.Fatalf("cached index did not refresh non-expiring session cookie: %#v", second.Result().Cookies())
	}
}

func newTicketSetupTestServer(t *testing.T, activeBackendID string) *Server {
	t.Helper()
	store := state.NewMemoryStore()
	activeURL := "http://pixel.test"
	activeName := "Pixel"
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
				{ID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
			},
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
