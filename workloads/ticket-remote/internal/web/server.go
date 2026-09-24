package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

//go:embed static/* diagnostic/* pwa/* welcome/*
var staticFS embed.FS

type Server struct {
	cfg               config.Config
	store             state.Store
	relay             *phone.Relay
	auth              *auth.Validator
	direct            *directStreamHub
	static            fs.FS
	diagnostic        fs.FS
	indexTmpl         *template.Template
	adminTmpl         *template.Template
	hdrDiagnosticTmpl *template.Template

	mu                      sync.Mutex
	clients                 map[*client]struct{}
	relayViewerRefs         map[string]int
	streamPrewarmTimers     map[string]*time.Timer
	streamPrewarmOwners     map[string]string
	streamPageOpenWarmUntil map[string]time.Time
	relayViewerGeneration   uint64

	coldRestartMu      sync.Mutex
	coldRestartState   state.ColdRestartState
	coldRestartBlocked atomic.Bool
	coldRestartCancel  context.CancelFunc
	coldRestartDone    chan struct{}

	stateMu       sync.RWMutex
	cachedState   state.Snapshot
	cachedStateAt time.Time

	phoneStartMu       sync.Mutex
	phoneStartInFlight bool
	lastPhoneStartAt   time.Time

	relayProductMu          sync.Mutex
	lastRelayStreamVerdict  string
	lastRelayDropTotal      uint64
	relayReportWake         chan string
	relayReportCancel       context.CancelFunc
	relayReportDone         chan struct{}
	captureDemandMu         sync.Mutex
	captureDemandSend       func(context.Context, uint64) (phone.CaptureDemandReceipt, error)
	captureDemandSending    bool
	captureDemandCancel     context.CancelFunc
	captureDemandFence      uint64
	captureDemandClosed     bool
	captureDemandEpoch      uint64
	captureDemandConnection uint64
	captureDemandReceipt    phone.CaptureDemandReceipt
	captureDemandExpiry     *time.Timer

	streamDesiredMu      sync.Mutex
	streamDesiredPending *streamDesiredStateUpdate
	streamDesiredClosed  bool
	streamDesiredWake    chan struct{}
	streamDesiredCancel  context.CancelFunc
	streamDesiredDone    chan struct{}

	streamDesiredReleaseMu    sync.Mutex
	streamDesiredReleaseTimer *time.Timer
	streamDesiredReleaseSeq   uint64
	startupRunMu              sync.Mutex
	trialAdmissionMu          sync.Mutex
	startupLeaseMu            sync.Mutex
	streamLifecycleMu         sync.RWMutex
	browserClientLogMu        sync.Mutex
	browserClientLogWindow    time.Time
	browserClientLogCount     int

	backendMu sync.RWMutex
	push      *ticketPush
}

var (
	assetVersionOnce  sync.Once
	assetVersionValue string
)

type client struct {
	conn                  *websocket.Conn
	sessionID             string
	email                 string
	startupTraceID        string
	relayViewerGeneration uint64
	trial                 *trialStreamMeter
	trialPageID           string

	clientLogMu          sync.Mutex
	clientLogWindowStart time.Time
	clientLogCount       int

	videoMu             sync.Mutex
	videoBroadcastReady bool

	videoEpoch               uint64
	videoInFlight            *queuedVideoFrame
	videoConfigGeneration    uint64
	videoConfigWrittenEpoch  uint64
	videoConfigWrittenGen    uint64
	videoWrittenSequence     uint64
	videoWrittenEvidence     []uint64
	videoV2FeedbackReceived  uint64
	videoV2FeedbackDecoded   uint64
	videoV2FeedbackRendered  uint64
	videoV2FeedbackPresented uint64
	videoV2Visibility        string
	videoReceiptSequence     uint64
	videoReceiptDeadlineAt   time.Time
	videoPending             *queuedVideoFrame
	controlQueue             []queuedControlMessage
	controlQueueBytes        int
	writerWake               chan struct{}
	writerDone               chan struct{}
	writerCancel             context.CancelFunc
	writerStartOnce          sync.Once
	writerStopOnce           sync.Once
	writerClosed             bool
	writerCloseReason        string
	onVideoConfigWritten     func(uint64, uint64)
	lastBrowserClockProbeAt  time.Time
	browserClockProbeDropped uint64

	firstVideoFrameRendered bool
	openingSummaryLogged    bool
}

type apiResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Message string         `json:"message,omitempty"`
	State   state.Snapshot `json:"state,omitempty"`
	Phone   phone.Health   `json:"phone,omitempty"`
}

const (
	serverVersion                 = "ticket-remote-2026-09-22-invitations-v232"
	stateLookupTimeout            = 1200 * time.Millisecond
	stateCacheMaxAge              = 30 * time.Second
	maxBrowserClientLogsPerMinute = 60

	streamPrewarmHold             = 30 * time.Second
	streamPageOpenWarmHold        = 30 * time.Minute
	publicOpenGraceHold           = 30 * time.Second
	streamDesiredIdleReleaseGrace = 60 * time.Second
	streamStartDedupe             = 2 * time.Second
	defaultFiniteCookieTTL        = auth.DefaultServerSessionTTL
	relayReportHeartbeat          = 3 * time.Second
	relayReportCoalesceWindow     = 75 * time.Millisecond
	maxBrowserSocketConnections   = 64
	maxBrowserSocketsPerIdentity  = 8
	maxBrowserSocketsPerSession   = 4
)

func NewServer(cfg config.Config, store state.Store, relay *phone.Relay) (*Server, error) {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	diagnosticSub, err := fs.Sub(staticFS, "diagnostic")
	if err != nil {
		return nil, err
	}
	relayReportCtx, relayReportCancel := context.WithCancel(context.Background())
	streamDesiredCtx, streamDesiredCancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:                 cfg,
		store:               store,
		relay:               relay,
		auth:                auth.NewValidator(cfg.Access),
		direct:              newDirectStreamHub(),
		static:              staticSub,
		diagnostic:          diagnosticSub,
		indexTmpl:           template.Must(template.New("index").Parse(indexHTML)),
		adminTmpl:           template.Must(template.New("admin").Parse(adminHTML)),
		hdrDiagnosticTmpl:   template.Must(template.New("hdr-diagnostic").Parse(hdrDiagnosticHTML)),
		clients:             map[*client]struct{}{},
		relayViewerRefs:     map[string]int{},
		streamPrewarmTimers: map[string]*time.Timer{},
		streamPrewarmOwners: map[string]string{},
		relayReportWake:     make(chan string, 1),
		relayReportCancel:   relayReportCancel,
		relayReportDone:     make(chan struct{}),
		captureDemandSend:   relay.SendCaptureDemand,
		streamDesiredWake:   make(chan struct{}, 1),
		streamDesiredCancel: streamDesiredCancel,
		streamDesiredDone:   make(chan struct{}),
	}
	relay.SetHandlers(s.handlePhoneMessage, s.handlePhoneDisconnect)
	if err := s.startColdRestartControl(); err != nil {
		relayReportCancel()
		streamDesiredCancel()
		return nil, err
	}
	// Pixel owns Spacetime command execution. The server writes durable commands
	// and uses the direct bridge relay only for video transport.
	go s.relayReportLoop(relayReportCtx)
	go s.streamDesiredStateLoop(streamDesiredCtx)
	if err := s.startTicketPush(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Server) Close() {
	if s.push != nil && s.push.cancel != nil {
		s.push.cancel()
		<-s.push.done
	}
	if s.coldRestartCancel != nil {
		s.coldRestartCancel()
		<-s.coldRestartDone
	}
	s.startupLeaseMu.Lock()
	s.mu.Lock()
	for key, timer := range s.streamPrewarmTimers {
		timer.Stop()
		delete(s.streamPrewarmTimers, key)
	}
	s.streamPrewarmOwners = nil
	s.streamPageOpenWarmUntil = nil
	s.mu.Unlock()
	s.startupLeaseMu.Unlock()
	s.cancelIdleStreamDesiredRelease()
	s.closeOrdinaryCaptureDemand()
	s.stopStreamDesiredStateWriter()
	if s.relayReportCancel != nil {
		s.relayReportCancel()
		select {
		case <-s.relayReportDone:
		case <-time.After(streamControlWriteTimeout + time.Second):
		}
	}
	if s.relay != nil {
		s.relay.Close()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.redirectHTTPToHTTPS(w, r) {
		return
	}
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	if path == "/api/v1/activity" {
		writeNoStoreHeaders(w)
	}
	if !s.requestOriginAllowed(r) {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "bad_origin", Message: "Request origin is not allowed."})
		return
	}
	publicAsset, publicContentType := publicInstallationAsset(r.URL.Path)
	switch {
	case (path == "/" && r.URL.Query().Has("invite")) || path == "/invite":
		s.handleInvitationLanding(w, r)
	case path == "/manifest.webmanifest" && r.URL.Query().Has("invite"):
		s.handleInvitationManifest(w, r)
	case path == "/api/v1/invite/open":
		s.handleInvitationOpen(w, r)
	case path == "/api/v1/invite/start":
		s.handleTrialStart(w, r)
	case path == "/api/v1/invite/status":
		s.handleTrialStatus(w, r)
	case path == "/api/v1/admin/invitations":
		s.withAdmin(w, r, s.handleAdminInvitations)
	case path == "/api/v1/admin/invitations/revoke":
		s.withAdmin(w, r, s.handleAdminInvitationRevoke)
	case path == "/ticket-notifications-sw.js":
		s.handleNotificationWorker(w, r)
	case publicAsset != "":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, err := staticFS.ReadFile(publicAsset)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeSecurityHeaders(w, "")
		w.Header().Set("Content-Type", publicContentType)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	case path == "/api/v1/livez":
		setReleaseHeaders(w)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "serverVersion": serverVersion, "assetVersion": assetVersion()})
	case path == "/api/v1/auth/start":
		s.handleAuthStart(w, r)
	case path == "/api/v1/auth/config":
		s.handleAuthConfig(w, r)
	case path == "/api/v1/auth/session":
		s.handleAuthSession(w, r)
	case path == "/api/v1/auth/logout":
		s.handleAuthLogout(w, r)
	case path == "/api/v1/health":
		s.withAdmin(w, r, func(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, snapshot state.Snapshot) {
			s.handleHealth(w, r, snapshot)
		})
	case strings.HasPrefix(path, "/static/"):
		writeNoStoreHeaders(w)
		if s.serveGuestAsset(w, r) {
			return
		}
		s.withMember(w, r, func(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, _ state.Snapshot) {
			http.StripPrefix("/static/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
		})
	case retiredTicketRoute(path):
		handleRetiredTicketRoute(w)
	case path == "/api/v1/stream":
		s.handleBrowserSocket(w, r)
	case path == "/api/v1/stream/prewarm":
		if _, _, err := s.guestFromRequest(r); err == nil {
			// Guest streaming begins at the socket, without the member page's
			// long-lived prewarm lease.
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		s.withMember(w, r, s.handleStreamPrewarmHTTP)
	case path == "/api/v1/internal/service-events":
		s.handleServiceEvent(w, r)
	case path == "/api/v1/activity":
		if _, _, err := s.guestFromRequest(r); err == nil {
			// Trial usage has its own server-authorized stream meter. Offline
			// member activity batches must never charge a trial.
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
		s.handlePageActivity(w, r)
	case path == "/api/v1/admin/statistics":
		writeNoStoreHeaders(w)
		s.withAdmin(w, r, s.handleAdminStatistics)
	case path == "/api/v1/admin/monitoring":
		writeNoStoreHeaders(w)
		s.withAdmin(w, r, s.handleMonitoring)
	case path == "/api/v1/admin/notifications":
		writeNoStoreHeaders(w)
		s.withAdmin(w, r, s.handleNotifications)
	case path == "/api/v1/admin/state":
		s.withAdmin(w, r, s.handleAdminState)
	case path == "/api/v1/admin/stream/cold-restart":
		s.withOwner(w, r, s.handleColdRestart)
	case path == "/api/v1/admin/members":
		s.withAdmin(w, r, s.handleAdminMembers)
	case path == "/api/v1/admin/phone/backends":
		s.withAdmin(w, r, s.handleAdminPhoneBackends)
	case path == "/api/v1/admin/phone/backend":
		s.withAdmin(w, r, s.handleAdminPhoneBackend)
	case path == "/api/v1/admin/ticket/reselect-latest/schedule":
		s.withAdmin(w, r, s.handleAdminTicketReselectLatestSchedule)
	case path == "/owner/hdr-diagnostic":
		s.withOwner(w, r, s.handleHDRDiagnosticPage)
	case path == "/owner/hdr-diagnostic/app.js":
		s.withOwner(w, r, s.handleHDRDiagnosticScript)
	case path == "/admin":
		s.handleAdminShell(w, r)
	case path == "/auth/callback":
		s.handleAuthCallback(w, r)
	case path == "/":
		s.handleIndexShell(w, r)
	default:
		http.NotFound(w, r)
	}
}

func publicInstallationAsset(path string) (string, string) {
	switch path {
	case "/manifest.webmanifest":
		return "pwa/manifest.webmanifest", "application/manifest+json"
	case "/pwa/icon-192.png", "/pwa/icon-512.png", "/pwa/icon-maskable.png", "/pwa/apple-touch-icon.png":
		return strings.TrimPrefix(path, "/"), "image/png"
	case "/pwa/welcome.js":
		return "pwa/welcome.js", "text/javascript; charset=utf-8"
	case "/pwa/install-guide.css":
		return "pwa/install-guide.css", "text/css; charset=utf-8"
	case "/pwa/install-apple.png", "/pwa/install-firefox.png", "/pwa/install-chrome-choice.png", "/pwa/install-chrome-confirm.png":
		return "static/" + strings.TrimPrefix(path, "/pwa/"), "image/png"
	case "/pwa/install-safari-steps.jpg", "/pwa/install-safari-add.jpg":
		return "static/" + strings.TrimPrefix(path, "/pwa/"), "image/jpeg"
	default:
		return "", ""
	}
}

func (s *Server) handleHDRDiagnosticPage(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, _ state.Snapshot) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	nonce := randomID()
	s.writeHTMLHeaders(w, nonce)
	const knownHDRReferenceOrigin = "https://ccameron-chromium.github.io"
	csp := w.Header().Get("Content-Security-Policy")
	w.Header().Set("Content-Security-Policy", strings.Replace(
		csp,
		"img-src 'self' data: blob:",
		"img-src 'self' data: blob: "+knownHDRReferenceOrigin,
		1,
	))
	_ = s.hdrDiagnosticTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"Nonce":        nonce,
	})
}

func (s *Server) handleHDRDiagnosticScript(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, _ state.Snapshot) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := fs.ReadFile(s.diagnostic, "hdr-diagnostic.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeNoStoreHeaders(w)
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(body)
}

func retiredTicketRoute(path string) bool {
	switch path {
	case "/api/v1/session",
		"/api/v1/me",
		"/api/v1/state",
		"/api/v1/client-log",
		"/api/v1/control-code/request",
		"/api/v1/control-code/prepare",
		"/api/v1/control-code/capture",
		"/api/v1/control-code/close",
		"/api/v1/control/claim",
		"/api/v1/control/extend",
		"/api/v1/control/release",
		"/api/v1/admin/control/revoke",
		"/api/v1/admin/ticket/reselect-latest":
		return true
	default:
		return false
	}
}

func handleRetiredTicketRoute(w http.ResponseWriter) {
	writeJSON(w, http.StatusGone, apiResponse{
		OK:      false,
		Error:   "route_retired",
		Message: "This retired Ticket route has been replaced by the direct Spacetime flow.",
	})
}

func (s *Server) handleIndexShell(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("enterInvite") == "1" {
		s.handleWelcome(w, r)
		return
	}
	startupRun := newStartupRunOrigin()
	openedAt := time.Now()
	if s.usesSpacetimeAuth() {
		id, sessionID, snapshot, ok := s.identifyMemberFromRequest(w, r, memberLookupOptions{
			optional:     true,
			cachedFirst:  true,
			prewarm:      "index_auth_prewarm",
			startupRun:   startupRun,
			openedAt:     openedAt,
			writeSession: true,
		})
		if ok {
			if sessionID == "" {
				sessionID = s.sessionID(w, r)
			}
			s.handleIndex(w, r, id, sessionID, snapshot, startupRun)
			return
		}
		if s.handleGuestIndex(w, r) {
			return
		}
		if invitationToken(cookieValue(r, invitationCookie)) != "" {
			s.handleInvitationLanding(w, r)
			return
		}
		s.handleWelcome(w, r)
		return
	}
	id, sessionID, snapshot, ok := s.identifyMemberFromRequest(w, r, memberLookupOptions{
		writeSession: true,
		cachedFirst:  true,
		prewarm:      "index_auth_prewarm",
		startupRun:   startupRun,
		openedAt:     openedAt,
	})
	if !ok {
		return
	}
	s.handleIndex(w, r, id, sessionID, snapshot, startupRun)
}

func (s *Server) handleAdminShell(w http.ResponseWriter, r *http.Request) {
	if s.usesSpacetimeAuth() {
		id, sessionID, snapshot, ok := s.identifyMemberFromRequest(nil, r, memberLookupOptions{optional: true})
		if !ok {
			s.handleUnauthIndex(w, r)
			return
		}
		if !snapshot.IsAdmin(id.Email) {
			writeErrorPage(w, http.StatusForbidden, "Admin access is required.")
			return
		}
		if sessionID == "" {
			sessionID = s.sessionID(w, r)
		}
		s.handleAdminPage(w, r, id, sessionID, snapshot)
		return
	}
	s.withAdmin(w, r, s.handleAdminPage)
}

func (s *Server) handleUnauthIndex(w http.ResponseWriter, r *http.Request) {
	// Signed-out pages have no ticket DOM and must not load the viewer app to log in.
	writeNoStoreHeaders(w)
	returnTo := safeReturnPath(r.URL.RequestURI())
	http.Redirect(w, r, "/api/v1/auth/start?returnTo="+url.QueryEscape(returnTo), http.StatusFound)
}

func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	nonce := randomID()
	writeHTMLHeaders(w, nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_ = welcomeTmpl.Execute(w, map[string]any{
		"AssetVersion":    assetVersion(),
		"AuthURL":         "/api/v1/auth/start?returnTo=" + url.QueryEscape(safeReturnPath(r.URL.RequestURI())),
		"Nonce":           nonce,
		"ManifestURL":     "/manifest.webmanifest",
		"InvitationEntry": r.URL.Query().Get("enterInvite") == "1",
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, snapshot state.Snapshot) {
	setReleaseHeaders(w)
	phoneHealth := s.relay.Snapshot()
	snapshot = s.withActivePhoneBackend(snapshot, phoneHealth)
	stateBackendFresh := snapshot.Ticket.ID != ""
	ok := snapshot.Ticket.ID != ""
	reasons := []string{}
	stateBackendWarning := ""
	if snapshot.StateBackend == "memory" {
		reasons = append(reasons, "state backend is memory; configure SpacetimeDB for production")
	}
	streamNow := time.Now()
	healthSnapshot := redactSnapshotForHealth(snapshot)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  ok,
		"serverVersion":       serverVersion,
		"reasons":             reasons,
		"stateBackendFresh":   stateBackendFresh,
		"stateBackendWarning": stateBackendWarning,
		"state":               healthSnapshot,
		"phone":               phoneHealth,
		"activePhoneBackend":  s.activePhoneBackend(),
		"directStream":        s.direct.snapshot(streamNow, phoneHealth),
		"pageOpenWarm":        s.pageOpenWarmSnapshot(streamNow),
		"experimentalMedia": map[string]any{
			"enabled":         true,
			"browserOnly":     true,
			"pipelineVersion": "webgpu-mainthread-edr-v2",
		},
	})
}

func (s *Server) handleStreamPrewarmHTTP(w http.ResponseWriter, r *http.Request, _ auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1024))
	s.prewarmStreamForSession(sessionID, "stream_prewarm")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":         true,
		"prewarmMs":  int64(streamPrewarmHold / time.Millisecond),
		"streamMode": "root_hardware_h264",
	})
}

func (s *Server) prewarmStreamForSession(sessionID string, reason string, existingStartupTraceID ...string) {
	cleanSessionID := strings.TrimSpace(sessionID)
	if cleanSessionID == "" {
		cleanSessionID = randomID()
	}
	cleanReason := strings.TrimSpace(reason)
	if cleanReason == "" {
		cleanReason = "stream_prewarm"
	}
	startupTraceID := ""
	traceContextProvided := len(existingStartupTraceID) > 0
	if len(existingStartupTraceID) > 0 {
		startupTraceID = strings.TrimSpace(existingStartupTraceID[0])
	}
	if startupTraceID != "" && !s.direct.openingActive(startupTraceID) {
		if s.direct.openingActiveForSession(cleanSessionID) {
			return
		}
		startupTraceID = ""
	}
	if startupTraceID == "" && !traceContextProvided {
		startupTraceID = s.direct.beginOpening(cleanSessionID, cleanReason)
	}

	prewarmID := streamPrewarmRelayLeaseID(cleanSessionID)
	s.retainRelayViewerForOpening(prewarmID, streamPrewarmHold, "prewarm", startupTraceID)
	// Start once; configured viewer receipts then drive capture credits.
	s.ensurePhoneStreamStarted(cleanReason)
}

func streamPrewarmRelayLeaseID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return randomID()
	}
	return sessionID
}

// A viewer or authenticated page can request an immediate start. Once configured,
// per-viewer receipt credit drives all ordinary capture through capture_demand.go.
func (s *Server) ensurePhoneStreamStarted(reason string) {
	if s.relay == nil || s.relay.Snapshot().Viewers == 0 {
		return
	}
	if s.direct.warmEncoderReusable(time.Now(), s.relay.Snapshot()) {
		s.requestOrdinaryCaptureIfUseful()
		return
	}
	s.phoneStartMu.Lock()
	if s.phoneStartInFlight || time.Since(s.lastPhoneStartAt) < streamStartDedupe {
		s.phoneStartMu.Unlock()
		return
	}
	s.phoneStartInFlight = true
	s.lastPhoneStartAt = time.Now()
	s.phoneStartMu.Unlock()
	go func() {
		defer func() {
			s.phoneStartMu.Lock()
			s.phoneStartInFlight = false
			s.phoneStartMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		if err := s.appendPhoneStart(ctx, reason); err != nil {
			s.recordRuntimeErrorAsync("stream_command_publish_failed", "start", err, map[string]any{"reason": reason})
		}
	}()
}

func redactSnapshotForHealth(snapshot state.Snapshot) state.Snapshot {
	snapshot.PageActivityDaily = nil
	snapshot.ActionActivityDaily = nil
	snapshot.ActionStatisticsStartedAt = ""
	snapshot.MemberHDRBoosts = nil
	snapshot.MemberHDRPreferences = nil
	snapshot.Members = append([]state.Member(nil), snapshot.Members...)
	for index := range snapshot.Members {
		snapshot.Members[index].AccountScopeID = ""
	}
	if snapshot.Phone == nil {
		return snapshot
	}
	phone := *snapshot.Phone
	phone.HealthJSON = redactControlCodeHealthJSON(phone.HealthJSON)
	snapshot.Phone = &phone
	return snapshot
}

func redactControlCodeHealthJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	redactControlCodeHealthValue(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return string(encoded)
}

func redactControlCodeHealthValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if controlCode, ok := typed["controlCodeRequest"].(map[string]any); ok {
			delete(controlCode, "requestId")
			delete(controlCode, "value")
			delete(controlCode, "digits")
			delete(controlCode, "totalDurationMillis")
			delete(controlCode, "phases")
			delete(controlCode, "imageBase64")
			delete(controlCode, "imageMime")
		}
		if event, ok := typed["ticketStateEvent"].(map[string]any); ok {
			delete(event, "requestId")
			delete(event, "value")
			delete(event, "totalDurationMillis")
			delete(event, "phases")
		}
		for _, child := range typed {
			redactControlCodeHealthValue(child)
		}
	case []any:
		for _, child := range typed {
			redactControlCodeHealthValue(child)
		}
	}
}

func (s *Server) snapshotWithCache(ctx context.Context, now time.Time, phoneHealth phone.Health, timeout time.Duration) (state.Snapshot, error) {
	stateCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		stateCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	snapshot, err := s.store.Snapshot(stateCtx, s.cfg.TicketID, now)
	if err != nil {
		if cached, ok := s.cachedSnapshot(now); ok {
			return s.withActivePhoneBackend(cached, phoneHealth), err
		}
		return state.Snapshot{}, err
	}
	snapshot = s.withActivePhoneBackend(snapshot, phoneHealth)
	s.cacheSnapshot(snapshot)
	return snapshot, nil
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot, startupRun string) {
	nonce := randomID()
	config := s.publicBrowserConfig(id, sessionID, snapshot, true)
	config["registrationComplete"] = r.URL.Query().Get("registered") == "1"
	_, active := snapshot.Member(id.Email)
	config["experimentalMediaCandidate"] = active
	if active {
		enabled, known := snapshot.HDRPreferenceForAccountScope(ticketAccountScopeID(id.Email))
		config["experimentalMediaBootstrap"] = map[string]any{
			"accountScopeId": ticketAccountScopeID(id.Email),
			"enabled":        enabled, "preferenceKnown": known,
			"capability": experimentalMediaCapability(id, snapshot),
		}
	}
	if startupRun = boundedStartupRunOrigin(startupRun); startupRun != "" {
		config["startupRunOrigin"] = startupRun
	}
	s.writeHTMLHeaders(w, nonce)
	_ = s.indexTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"ConfigJSON":   template.JS(mustJSON(config)),
		"IsAdmin":      snapshot.IsAdmin(id.Email),
		"Nonce":        nonce,
		"ManifestURL":  "/manifest.webmanifest",
	})
}

func (s *Server) publicBrowserConfig(id auth.Identity, sessionID string, snapshot state.Snapshot, authenticated bool) map[string]any {
	email := id.Email
	accountScopeID := ""
	if authenticated && strings.TrimSpace(email) != "" {
		accountScopeID = ticketAccountScopeID(email)
	}
	authMode := s.publicAuthMode()
	return map[string]any{
		"publicBaseUrl":  s.cfg.PublicBaseURL,
		"authenticated":  authenticated,
		"email":          email,
		"accountScopeId": accountScopeID,
		"sessionId":      sessionID,
		"stateBackend":   snapshot.StateBackend,
		"ticketId":       s.cfg.TicketID,
		"backendId":      s.activePhoneBackend().ID,
		"pageVersion":    serverVersion,
		"serverTime":     time.Now().UTC().Format(time.RFC3339Nano),
		"assetVersion":   assetVersion(),
		"auth": map[string]any{
			"mode":           authMode,
			"issuer":         strings.TrimRight(s.cfg.Access.OIDCIssuer, "/"),
			"clientId":       s.cfg.Access.OIDCClientID,
			"scope":          strings.TrimSpace(s.cfg.Access.OIDCScope),
			"redirectUrl":    s.cfg.Access.OIDCRedirect,
			"authorizeUrl":   strings.TrimRight(s.cfg.Access.OIDCIssuer, "/") + "/auth",
			"tokenUrl":       strings.TrimRight(s.cfg.Access.OIDCIssuer, "/") + "/token",
			"logoutUrl":      strings.TrimRight(s.cfg.Access.OIDCIssuer, "/") + "/session/end",
			"authCookieName": s.cfg.Access.AuthCookieName,
		},
		"spacetime": map[string]any{
			"host":     s.cfg.State.SpacetimeHost,
			"database": s.cfg.State.SpacetimeDatabase,
		},
	}
}

func ticketAccountScopeID(email string) string {
	normalized := []byte(strings.TrimSpace(email))
	for index, value := range normalized {
		if value >= 'A' && value <= 'Z' {
			normalized[index] = value + ('a' - 'A')
		}
	}
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	nonce := randomID()
	s.writeHTMLHeaders(w, nonce)
	member, _ := snapshot.Member(id.Email)
	isOwner := member.Role == state.RoleOwner
	tab := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tab")))
	pageTitle := "Overview"
	switch tab {
	case "tickets":
		pageTitle = "Tickets"
	case "members":
		pageTitle = "Members"
	case "statistics":
		pageTitle = "Statistics"
	case "settings":
		pageTitle = "Settings"
	case "account":
		if isOwner {
			pageTitle = "ViVi account"
		} else {
			tab = "overview"
		}
	default:
		tab = "overview"
	}
	pageData := map[string]any{
		"AssetVersion": assetVersion(),
		"Email":        id.Email,
		"IsOwner":      isOwner,
		"Tab":          tab,
		"PageTitle":    pageTitle,
		"IsStatistics": tab == "statistics",
		"Nonce":        nonce,
	}
	if tab == "overview" {
		zone, err := time.LoadLocation("Europe/Riga")
		if err != nil {
			zone = time.UTC
		}
		pageData["PageOpenedAt"] = time.Now().In(zone).Format("15:04 MST")
	}
	if tab == "statistics" {
		pageData["StatisticsJSON"] = template.JS(mustJSON(adminStatisticsPayload(snapshot)))
		_ = s.adminTmpl.Execute(w, pageData)
		return
	}
	members := make([]adminMemberPageRow, 0, len(snapshot.Members))
	ownerCount := activeOwnerCount(snapshot)
	for _, item := range snapshot.Members {
		if item.Active {
			canRemove := isOwner || item.Role == state.RoleMember
			if item.Role == state.RoleOwner && ownerCount <= 1 {
				canRemove = false
			}
			members = append(members, adminMemberPageRow{Member: item, CanRemove: canRemove})
		}
	}
	pageData["Members"] = members
	if tab == "members" {
		_ = s.adminTmpl.Execute(w, pageData)
		return
	}
	activeBackend := s.activePhoneBackend()
	if tab == "tickets" || tab == "settings" || tab == "account" {
		pageData["AdminConfigJSON"] = template.JS(mustJSON(map[string]any{
			"ticketId":  s.cfg.TicketID,
			"backendId": activeBackend.ID,
			"isOwner":   isOwner,
		}))
	}
	if tab == "tickets" {
		for key, value := range s.phoneSchedulePageData(snapshot, time.Now()) {
			pageData[key] = value
		}
	}
	if tab == "overview" || tab == "settings" {
		phoneHealth := s.relay.Snapshot()
		pageData["Phone"] = phoneHealth
		pageData["ActiveBackend"] = activeBackend.ID
		viewers := 0
		for _, viewer := range snapshot.Viewers {
			if viewer.Connected {
				viewers++
			}
		}
		pageData["ViewerCount"] = viewers
		if tab == "settings" {
			pageData["Backends"] = s.configuredPhoneBackends()
			rawStateSnapshot := snapshot
			rawStateSnapshot.PageActivityDaily = nil
			rawStateSnapshot.ActionActivityDaily = nil
			rawStateSnapshot.ActionStatisticsStartedAt = ""
			rawStateSnapshot.Members = append([]state.Member(nil), snapshot.Members...)
			for index := range rawStateSnapshot.Members {
				rawStateSnapshot.Members[index].AccountScopeID = ""
			}
			pageData["RawState"] = mustJSON(map[string]any{"state": rawStateSnapshot, "phone": phoneHealth})
		}
	}
	_ = s.adminTmpl.Execute(w, pageData)
}

type adminMemberPageRow struct {
	state.Member
	CanRemove bool
}

func (s *Server) handleAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !s.usesSpacetimeAuth() {
		writeErrorPage(w, http.StatusBadRequest, "SpacetimeAuth login is not enabled.")
		return
	}
	if strings.TrimSpace(s.cfg.Access.OIDCClientID) == "" {
		writeErrorPage(w, http.StatusServiceUnavailable, "SpacetimeAuth client is not configured.")
		return
	}
	verifier := randomID() + randomID()
	stateValue := randomID()
	returnTo := safeReturnPath(r.URL.Query().Get("returnTo"))
	maxAge := int((10 * time.Minute).Seconds())
	s.setPrivateAuthCookie(w, authFlowCookie("verifier"), verifier, maxAge)
	s.setPrivateAuthCookie(w, authFlowCookie("state"), stateValue, maxAge)
	s.setPrivateAuthCookie(w, authFlowCookie("return_to"), returnTo, maxAge)
	s.setPrivateAuthCookie(w, authFlowCookie("invite_hash"), "", -1)
	if r.URL.Query().Get("invite") == "1" {
		if guest, err := s.auth.ValidateGuestSession(cookieValue(r, trialCookie), time.Now()); err == nil {
			s.closeTrialClients(guest.InvitationID)
		}
		if invite, err := s.invitationFromRequest(r); err == nil && invite.RevokedAt == "" && invite.RedeemedAt == "" {
			s.setPrivateAuthCookie(w, authFlowCookie("invite_hash"), invitationFingerprint(cookieValue(r, invitationCookie)), maxAge)
		}
	}

	next, err := url.Parse(strings.TrimRight(s.cfg.Access.OIDCIssuer, "/") + "/auth")
	if err != nil {
		writeErrorPage(w, http.StatusServiceUnavailable, "SpacetimeAuth issuer is invalid.")
		return
	}
	next.RawQuery = ""
	q := next.Query()
	q.Set("response_type", "code")
	q.Set("client_id", s.cfg.Access.OIDCClientID)
	q.Set("redirect_uri", s.cfg.Access.OIDCRedirect)
	q.Set("scope", strings.TrimSpace(s.cfg.Access.OIDCScope))
	q.Set("state", stateValue)
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	next.RawQuery = q.Encode()
	http.Redirect(w, r, next.String(), http.StatusFound)
}

func (s *Server) handleAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if authError := strings.TrimSpace(r.URL.Query().Get("error")); authError != "" {
		writeErrorPage(w, http.StatusUnauthorized, firstNonEmpty(r.URL.Query().Get("error_description"), authError))
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	receivedState := strings.TrimSpace(r.URL.Query().Get("state"))
	verifier := cookieValue(r, authFlowCookie("verifier"))
	expectedState := cookieValue(r, authFlowCookie("state"))
	returnTo := safeReturnPath(cookieValue(r, authFlowCookie("return_to")))
	inviteHash := cookieValue(r, authFlowCookie("invite_hash"))
	s.clearAuthFlowCookies(w)
	if code == "" || verifier == "" || expectedState == "" || receivedState != expectedState {
		writeErrorPage(w, http.StatusUnauthorized, "Login callback did not match this browser. Start sign-in again.")
		return
	}
	idToken, err := s.exchangeAuthCode(r.Context(), code, verifier)
	if err != nil {
		writeErrorPage(w, http.StatusUnauthorized, err.Error())
		return
	}
	id, err := s.auth.ValidateOIDCJWT(r.Context(), idToken)
	if err != nil {
		writeErrorPage(w, http.StatusUnauthorized, err.Error())
		return
	}
	if inviteHash != "" {
		if err := s.redeemInvitationHash(r.Context(), inviteHash, id); err != nil {
			writeErrorPage(w, http.StatusForbidden, "This invitation cannot grant access. Sign in with an existing account or ask an administrator.")
			return
		}
	}
	snapshot, err := s.store.Snapshot(r.Context(), s.cfg.TicketID, time.Now())
	if err != nil {
		writeErrorPage(w, http.StatusServiceUnavailable, "Ticket state is unavailable.")
		return
	}
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	s.cacheSnapshot(snapshot)
	if _, ok := snapshot.Member(id.Email); !ok {
		writeErrorPage(w, http.StatusForbidden, fmt.Sprintf("No ticket has been assigned to your account (%s). Ask the ticket owner or an administrator to give you access.", id.Email))
		return
	}
	sessionToken, _, err := s.auth.IssueServerSession(id, s.cfg.CookieTTL, time.Now())
	if err != nil {
		writeErrorPage(w, http.StatusInternalServerError, "Ticket session could not be created.")
		return
	}
	s.setAuthCookie(w, sessionToken, s.authCookieMaxAge())
	if inviteHash != "" {
		s.setPrivateAuthCookie(w, trialCookie, "", -1)
		s.setPrivateAuthCookie(w, invitationCookie, "", -1)
		returnTo = "/?registered=1"
	}
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func (s *Server) exchangeAuthCode(ctx context.Context, code string, verifier string) (string, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("client_id", s.cfg.Access.OIDCClientID)
	body.Set("code", code)
	body.Set("redirect_uri", s.cfg.Access.OIDCRedirect)
	body.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.Access.OIDCIssuer, "/")+"/token", strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("SpacetimeAuth token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	var payload struct {
		IDToken          string `json:"id_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&payload); err != nil {
		return "", fmt.Errorf("SpacetimeAuth token response was invalid: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.TrimSpace(payload.IDToken) == "" {
		if payload.ErrorDescription != "" {
			return "", errors.New(payload.ErrorDescription)
		}
		if payload.Error != "" {
			return "", errors.New(payload.Error)
		}
		return "", fmt.Errorf("SpacetimeAuth token exchange failed with HTTP %d", resp.StatusCode)
	}
	return strings.TrimSpace(payload.IDToken), nil
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeNoStoreHeaders(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"auth":      s.publicBrowserConfig(auth.Identity{}, "", state.Snapshot{}, false)["auth"],
		"spacetime": s.publicBrowserConfig(auth.Identity{}, "", state.Snapshot{}, false)["spacetime"],
	})
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id, sessionID, snapshot, ok := s.identifyMemberFromRequest(nil, r, memberLookupOptions{optional: true})
		if !ok {
			if s.handleGuestAuthSession(w, r) {
				return
			}
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "auth_required", Message: "SpacetimeAuth login is required."})
			return
		}
		if sessionID == "" {
			sessionID = s.sessionID(w, r)
		}
		s.setPrivateAuthCookie(w, "ticket_remote_direct_spacetime", "", -1)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":             true,
			"authenticated":  true,
			"email":          id.Email,
			"accountScopeId": ticketAccountScopeID(id.Email),
			"sessionId":      sessionID,
			"state":          snapshot.PublicForMember(id.Email),
			"spacetime":      s.directSpacetimeSessionFromRequest(r, id.Email),
		})
	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 32*1024))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: "Auth payload was too large."})
			return
		}
		var req struct {
			IDToken string `json:"idToken"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: "Auth payload was invalid."})
			return
		}
		token := strings.TrimSpace(req.IDToken)
		id, err := s.auth.ValidateOIDCJWT(r.Context(), token)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "auth_invalid", Message: err.Error()})
			return
		}
		inviteToken := invitationToken(cookieValue(r, invitationCookie))
		if inviteToken != "" {
			if err := s.redeemInvitationHash(r.Context(), invitationFingerprint(inviteToken), id); err != nil {
				writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "invitation_unavailable", Message: "This invitation cannot grant access. Sign in with an existing account or ask an administrator."})
				return
			}
		}
		snapshot, err := s.store.Snapshot(r.Context(), s.cfg.TicketID, time.Now())
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "state_unavailable", Message: "Ticket state is unavailable."})
			return
		}
		snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
		s.cacheSnapshot(snapshot)
		if _, ok := snapshot.Member(id.Email); !ok {
			writeJSON(w, http.StatusForbidden, apiResponse{
				OK:      false,
				Error:   "not_member",
				Message: fmt.Sprintf("No ticket has been assigned to your account (%s). Ask the ticket owner or an administrator to give you access.", id.Email),
			})
			return
		}
		sessionToken, sessionExpiresAt, err := s.auth.IssueServerSession(id, s.cfg.CookieTTL, time.Now())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "session_failed", Message: "Ticket session could not be created."})
			return
		}
		s.setAuthCookie(w, sessionToken, s.authCookieMaxAge())
		if inviteToken != "" {
			s.setPrivateAuthCookie(w, trialCookie, "", -1)
			s.setPrivateAuthCookie(w, invitationCookie, "", -1)
		}
		sessionID := s.sessionID(w, r)
		session := map[string]any{
			"expires": !sessionExpiresAt.IsZero(),
		}
		if !sessionExpiresAt.IsZero() {
			session["expiresAt"] = sessionExpiresAt.UTC().Format(time.RFC3339)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"authenticated": true,
			"email":         id.Email,
			"sessionId":     sessionID,
			"state":         snapshot.PublicForMember(id.Email),
			"session":       session,
			"spacetime":     s.directSpacetimeSessionFromRequest(r, id.Email),
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.setAuthCookie(w, "", -1)
	s.setPrivateAuthCookie(w, trialCookie, "", -1)
	s.setPrivateAuthCookie(w, invitationCookie, "", -1)
	s.setPrivateAuthCookie(w, "ticket_remote_direct_spacetime", "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	relayHealth := s.relay.Snapshot()
	snapshot = s.withFreshActivePhoneHealth(r.Context(), snapshot, relayHealth)
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: relayHealth})
}

func (s *Server) handleAdminMembers(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, current state.Snapshot) {
	switch r.Method {
	case http.MethodPost:
		if adminFormRequest(r) && r.FormValue("action") == "remove" {
			s.removeAdminMember(w, r, id.Email, r.FormValue("email"))
			return
		}
		var req struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if adminFormRequest(r) {
			req.Email, req.Role = r.FormValue("email"), r.FormValue("role")
		} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: err.Error()})
			return
		}
		snapshot, err := s.store.UpsertMember(r.Context(), s.cfg.TicketID, id.Email, req.Email, req.Role)
		s.writeStateMutation(w, r, id.Email, "member_upsert", snapshot, err)
	case http.MethodDelete:
		s.removeAdminMember(w, r, id.Email, r.URL.Query().Get("email"))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) removeAdminMember(w http.ResponseWriter, r *http.Request, actor string, email string) {
	snapshot, err := s.store.RemoveMember(r.Context(), s.cfg.TicketID, actor, strings.TrimSpace(email))
	s.writeStateMutation(w, r, actor, "member_remove", snapshot, err)
}

func activeOwnerCount(snapshot state.Snapshot) int {
	count := 0
	for _, member := range snapshot.Members {
		if member.Active && member.Role == state.RoleOwner {
			count++
		}
	}
	return count
}

func (s *Server) handleAdminPhoneBackends(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	active := s.activePhoneBackend()
	backends := s.configuredPhoneBackends()
	type backendStatus struct {
		ID         string       `json:"id"`
		AttachName string       `json:"attachName"`
		BaseURL    string       `json:"baseUrl"`
		Active     bool         `json:"active"`
		Relay      phone.Health `json:"relay,omitempty"`
		HealthOK   bool         `json:"healthOk"`
		StatusCode int          `json:"statusCode,omitempty"`
		Error      string       `json:"error,omitempty"`
	}
	statuses := make([]backendStatus, 0, len(backends))
	relayHealth := s.relay.Snapshot()
	for _, backend := range backends {
		probeOK, statusCode, probeErr := s.probePhoneBackend(r.Context(), backend)
		item := backendStatus{
			ID:         backend.ID,
			AttachName: backend.AttachName,
			BaseURL:    backend.BaseURL,
			Active:     backend.ID == active.ID,
			HealthOK:   probeOK,
			StatusCode: statusCode,
		}
		if item.Active {
			item.Relay = relayHealth
		}
		if probeErr != nil {
			item.Error = probeErr.Error()
		}
		statuses = append(statuses, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"activeBackendId": active.ID,
		"backends":        statuses,
	})
}

func (s *Server) handleAdminPhoneBackend(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		BackendID string `json:"backendId"`
	}
	if adminFormRequest(r) {
		req.BackendID = r.FormValue("backendId")
	} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: err.Error()})
		return
	}
	backend, ok := config.FindPhoneBackend(s.configuredPhoneBackends(), strings.TrimSpace(req.BackendID))
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "unknown_backend", Message: "Unknown phone backend."})
		return
	}
	previous := s.activePhoneBackend()
	now := time.Now()
	if err := config.WriteActivePhoneBackendID(s.cfg.Phone.ActiveBackendFile, backend.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "persist_backend", Message: err.Error()})
		return
	}
	s.setActivePhoneBackend(backend)
	s.relay.SwitchBackend(phone.Backend{ID: backend.ID, AttachName: backend.AttachName, BaseURL: backend.BaseURL})
	relayHealth := s.relay.Snapshot()
	snapshot, err := s.store.UpdatePhone(r.Context(), state.PhoneInput{
		TicketID:     s.cfg.TicketID,
		BackendID:    backend.ID,
		AttachName:   backend.AttachName,
		BaseURL:      backend.BaseURL,
		DesiredState: relayHealth.StreamState,
		HealthJSON:   "",
		LastError:    relayHealth.LastError,
		Now:          now,
	})
	if err != nil {
		s.recordRuntimeErrorAsync("backend_switch_phone_state_update_failed", backend.ID, err, map[string]any{"backendId": backend.ID})
	}
	snapshot = s.withActivePhoneBackend(snapshot, relayHealth)
	s.recordAuditAsync(s.cfg.TicketID, id.Email, "phone_backend_switched", map[string]any{
		"from": previous.ID,
		"to":   backend.ID,
	}, now)
	s.cacheSnapshot(snapshot)
	if redirectAdminForm(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"state":              snapshot,
		"phone":              relayHealth,
		"activePhoneBackend": backend,
	})
}

func (s *Server) handleBrowserSocket(w http.ResponseWriter, r *http.Request) {
	if !s.browserWebSocketOriginAllowed(r) {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "bad_origin", Message: "WebSocket origin is not allowed."})
		return
	}
	// A video connection wakes the phone, so it must use a current membership
	// lookup rather than the short-lived page cache.
	id, sessionID, _, ok := s.identifyMemberFromRequest(nil, r, memberLookupOptions{optional: true, requireFresh: true})
	var trial *trialStreamMeter
	trialAdmissionHeld := false
	if !ok {
		guest, invite, err := s.guestFromRequest(r)
		if err != nil || invite.ActiveSessionID != guest.SessionID || (invite.Status != "trial_active" && invite.ResultDeliveryUntilMS <= time.Now().UnixMilli()) {
			writeJSON(w, http.StatusUnauthorized, apiResponse{Error: "trial_or_member_required"})
			return
		}
		s.trialAdmissionMu.Lock()
		trialAdmissionHeld = true
		defer func() {
			if trialAdmissionHeld {
				s.trialAdmissionMu.Unlock()
			}
		}()
		store, _ := s.invitationStore()
		invite, err = store.TrialStatus(r.Context(), s.cfg.TicketID, guest.InvitationID, guest.SessionID)
		if err != nil || invite.ActiveSessionID != guest.SessionID || invite.RevokedAt != "" || invite.RedeemedAt != "" {
			writeJSON(w, 403, apiResponse{Error: "trial_unavailable"})
			return
		}
		pageID := r.URL.Query().Get("trial_page")
		if len(pageID) != 32 {
			writeJSON(w, 400, apiResponse{Error: "trial_page_required"})
			return
		}
		for _, existing := range s.clientSnapshot() {
			if existing.trial != nil && existing.trial.input.InvitationID == guest.InvitationID && existing.trialPageID != pageID {
				writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "needsTakeover": true})
				return
			}
		}
		s.closeTrialClients(guest.InvitationID)
		// Read the latest lease sequence after closing the previous socket.
		invite, err = store.TrialStatus(r.Context(), s.cfg.TicketID, guest.InvitationID, guest.SessionID)
		if err != nil {
			writeJSON(w, 403, apiResponse{Error: "trial_unavailable"})
			return
		}
		trial = newTrialStreamMeter(store, s.cfg.TicketID, guest, invite)
		id = auth.Identity{Subject: guest.ActorID()}
		sessionID = guest.SessionID
	} else if sessionID == "" {
		sessionID = s.sessionID(w, r)
	}
	startupRun := browserStartupRunOrigin(r)
	openingClass := "cold"
	if s.direct.warmEncoderReusable(time.Now(), s.relay.Snapshot()) {
		openingClass = "warm"
	} else if s.direct.currentStreamEpoch() != 0 {
		openingClass = "recovery"
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The explicit schemeful PublicBaseURL check above is authoritative.
		// Avoid a second Host-only check that is incorrect behind a trusted
		// reverse proxy or Cloudflare Tunnel.
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
		Subprotocols:       []string{"ticket.video.v1"},
	})
	if err != nil {
		return
	}
	startupTraceID := ""
	videoSocketDetail := browserVideoSocketContext(r)
	initialVisibility := ""
	if visibility, _ := videoSocketDetail["visibility"].(string); visibility == "visible" || visibility == "hidden" {
		initialVisibility = visibility
	}
	c := &client{
		conn: conn, sessionID: sessionID, email: id.Email, videoV2Visibility: initialVisibility,
		trial:       trial,
		trialPageID: r.URL.Query().Get("trial_page"),
		onVideoConfigWritten: func(uint64, uint64) {
			s.requestOrdinaryCaptureIfUseful()
		},
	}
	if trial != nil {
		c.email = id.Subject
	}
	if !s.tryAddClient(c) {
		_ = conn.Close(websocket.StatusPolicyViolation, "connection limit reached")
		return
	}
	if trialAdmissionHeld {
		s.trialAdmissionMu.Unlock()
		trialAdmissionHeld = false
	}
	s.startupRunMu.Lock()
	traceID := safeRuntimeTraceID("browser", sessionID)
	startupTraceID = s.direct.joinOpeningForRun(sessionID, startupRun.origin, "video_socket_open")
	c.startupTraceID = startupTraceID

	detail := map[string]any{
		"video":   true,
		"version": serverVersion,
	}
	for key, value := range videoSocketDetail {
		detail[key] = value
	}
	s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "video_socket_open", traceID, detail)
	c.relayViewerGeneration = s.addRelayViewer(sessionID)
	if trial == nil {
		s.retainRelayViewerForOpening(sessionID, publicOpenGraceHold, "public_open_grace", startupTraceID)
	}
	s.cancelIdleStreamDesiredRelease()
	s.direct.addVideoClient()

	s.ensurePhoneStreamStarted("video_socket_open")
	openingMessage, _ := json.Marshal(map[string]string{"type": "opening", "openingClass": openingClass})
	c.enqueueControl(openingMessage)
	s.sendBrowserVideoWarmStart(c)
	c.startVideoWriter()
	s.startupRunMu.Unlock()
	s.publishRelayCurrentReportAsync("video_socket_open")
	socketCtx, cancelSocket := context.WithCancel(r.Context())
	socketActivity := make(chan struct{}, 1)
	socketLivenessDone := make(chan struct{})
	go func() {
		defer close(socketLivenessDone)
		if err := browserVideoLivenessLoop(
			socketCtx,
			conn,
			socketActivity,
			browserVideoLivenessIdle,
			browserVideoLivenessTimeout,
		); err != nil {
			// Liveness belongs to this viewer only. Closing the connection
			// unblocks its reader and enters the normal per-viewer teardown.
			_ = conn.CloseNow()
		}
	}()
	defer func() {
		c.stopVideoWriter()
		if c.trial != nil {
			c.trial.pause(true)
		}
		closeReason := c.videoWriterCloseReason()
		if closeReason == "" {
			closeReason = "reader_closed"
		}
		s.removeClient(c)
		s.direct.removeVideoClient()

		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "video_socket_closed", traceID, map[string]any{
			"activeVideoClients": s.direct.activeVideoClientCount(),
			"reason":             closeReason,
		})
		if !c.firstVideoFrameRendered && c.trial == nil {
			s.retainRelayViewerForOpening(sessionID, publicOpenGraceHold, "public_open_grace", startupTraceID)
		}
		s.publishRelayCurrentReportAsync("video_socket_closed")
		s.scheduleIdleStreamDesiredRelease("relay_no_video_clients")
		s.removeRelayViewerGeneration(sessionID, c.relayViewerGeneration)
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
	}()
	defer func() {
		cancelSocket()
		<-socketLivenessDone
	}()
	for {
		typ, data, err := conn.Read(socketCtx)
		receivedAt := time.Now()
		if err != nil {
			return
		}
		signalBrowserVideoActivity(socketActivity)
		if typ != websocket.MessageText {
			continue
		}
		s.handleVideoStreamMessage(r.Context(), c, data, receivedAt)
	}
}

func browserVideoSocketContext(r *http.Request) map[string]any {
	out := map[string]any{}
	if r == nil || r.URL == nil {
		return out
	}
	query := r.URL.Query()
	setText := func(outKey string, queryKey string) {
		value := strings.TrimSpace(query.Get(queryKey))
		if value == "" {
			return
		}
		out[outKey] = safeRuntimeLogText(value)
	}
	setText("pageVersion", "page_version")
	setText("assetVersion", "asset_version")
	setText("visibility", "visibility")
	setText("restoreReason", "restore_reason")
	setText("recoveryId", "recovery_id")
	setText("frameAgeMillis", "frame_age_ms")
	setText("hiddenAgeMillis", "hidden_age_ms")
	setText("hasFrame", "has_frame")
	setText("configured", "configured")
	setText("openSeq", "open_seq")
	return out
}

type browserStartupRunOriginEvidence struct {
	origin                string
	clearRelayCorrelation bool
}

func browserStartupRunOrigin(r *http.Request) browserStartupRunOriginEvidence {
	if r == nil {
		return browserStartupRunOriginEvidence{clearRelayCorrelation: true}
	}
	hasVideoProtocol := false
	origins := make([]string, 0, 1)
	invalidToken := false
	for _, header := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, candidate := range strings.Split(header, ",") {
			candidate = strings.TrimSpace(candidate)
			switch {
			case candidate == "ticket.video.v1":
				hasVideoProtocol = true
			case boundedStartupRunOrigin(candidate) != "":
				origins = append(origins, boundedStartupRunOrigin(candidate))
			case candidate != "":
				invalidToken = true
			}
		}
	}
	if hasVideoProtocol && len(origins) == 1 && !invalidToken {
		return browserStartupRunOriginEvidence{origin: origins[0]}
	}
	return browserStartupRunOriginEvidence{clearRelayCorrelation: true}
}

func (s *Server) sendBrowserVideoWarmStart(c *client) {
	expected := c.videoConfigGenerationSnapshot()
	configFrame, keyFrame := s.direct.warmStart()
	if len(configFrame) == 0 {
		return
	}
	c.enqueueConfigAndKeyframe(configFrame, keyFrame, &expected)
	// Config completion wakes capture when this viewer has no cached picture.
}

func (s *Server) handleVideoStreamMessage(ctx context.Context, c *client, data []byte, received ...time.Time) {
	receivedAt := time.Now()
	if len(received) > 0 && !received[0].IsZero() {
		receivedAt = received[0]
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "client_log":
		event, detail, _, ok := decodeBrowserClientLog(data)
		level := "warn"
		if event == "ticket_opening" {
			if c.openingSummaryLogged {
				return
			}
			c.openingSummaryLogged = true
			level = "info"
		}
		if ok && c.allowClientLog(receivedAt) && s.allowBrowserClientLog(receivedAt) {
			s.recordRuntimeEventForSourceAsync("ticket_remote_browser", level, event, safeRuntimeTraceID("browser", c.sessionID), detail)
		}
		return
	case "stream_feedback":
		s.handleStreamFeedback(c, data)
		return
	case "clock_probe":
		if response, ok := c.browserClockProbeResponse(data, receivedAt); ok {
			c.enqueueControl(response)
		}
		return
	default:
		return
	}
}

func (s *Server) handleStreamFeedback(c *client, data []byte) {
	if s == nil || c == nil {
		return
	}
	outcome := c.acceptStreamFeedbackOutcome(data)
	if c.trial != nil {
		c.videoMu.Lock()
		hidden := c.videoV2Visibility != "visible"
		c.videoMu.Unlock()
		if hidden {
			c.trial.pause(false)
		}
	}
	if outcome.receiptReleased || outcome.becameVisible {
		s.requestOrdinaryCaptureIfUseful()
	}
	if outcome.presented && !c.firstVideoFrameRendered {
		c.firstVideoFrameRendered = true
		s.direct.completeOpening(c.startupTraceID)
		s.releaseOpeningWarm(c.sessionID, c.startupTraceID)
	}
}

func (c *client) allowClientLog(now time.Time) bool {
	c.clientLogMu.Lock()
	defer c.clientLogMu.Unlock()
	if c.clientLogWindowStart.IsZero() || now.Sub(c.clientLogWindowStart) >= time.Minute || now.Before(c.clientLogWindowStart) {
		c.clientLogWindowStart = now
		c.clientLogCount = 0
	}
	if c.clientLogCount >= maxBrowserClientLogsPerMinute {
		return false
	}
	c.clientLogCount++
	return true
}

func (s *Server) allowBrowserClientLog(now time.Time) bool {
	s.browserClientLogMu.Lock()
	defer s.browserClientLogMu.Unlock()
	if s.browserClientLogWindow.IsZero() || now.Sub(s.browserClientLogWindow) >= time.Minute || now.Before(s.browserClientLogWindow) {
		s.browserClientLogWindow = now
		s.browserClientLogCount = 0
	}
	if s.browserClientLogCount >= maxBrowserClientLogsPerMinute {
		return false
	}
	s.browserClientLogCount++
	return true
}

func (s *Server) handlePhoneMessage(msg phone.Message) {
	s.streamLifecycleMu.RLock()
	defer s.streamLifecycleMu.RUnlock()
	if s.coldRestartBlocked.Load() {
		return
	}
	s.observeOrdinaryCaptureConnection(msg.ConnectionGeneration)
	if msg.ClockProbe != nil {
		s.direct.recordBoundedPhoneClock(*msg.ClockProbe)
		return
	}
	if len(msg.Text) > 0 {
		if s.handlePhoneText(msg.Text) {
			return
		}
		s.broadcastText(msg.Text)
		return
	}
	if len(msg.Binary) > 0 {
		s.completeOrdinaryCaptureOpportunity()
		if frame, ok := s.direct.recordFrameForBroadcast(msg.Binary); ok {
			s.broadcastFrame(frame)
		}
		s.requestOrdinaryCaptureIfUseful()
	}
}

func (s *Server) handlePhoneDisconnect(err error) {
	s.fenceOrdinaryCaptureDemand(0)
	if err != nil && !expectedPhoneDisconnect(err) {
		s.recordRuntimeErrorAsync("phone_stream_disconnected", "", err, nil)
	}
	s.direct.recordPhoneReconnect()
	s.publishRelayCurrentReportAsync("phone_stream_disconnected")
	if s.direct.activeVideoClientCount() == 0 {
		go s.releaseStreamDesiredIfNoVideoClients("phone_stream_disconnected")
	} else {
		s.scheduleIdleStreamDesiredRelease("phone_stream_disconnected")
	}
}

func expectedPhoneDisconnect(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return websocket.CloseStatus(err) == websocket.StatusNormalClosure
}

func (s *Server) handlePhoneText(raw []byte) bool {
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return false
	}
	now := time.Now()
	msgType, _ := msg["type"].(string)
	if msgType != "config" {
		if phoneUptimeMillis := controlCodeInt64FromMessage(msg["phoneUptimeMillis"]); phoneUptimeMillis > 0 {
			s.direct.recordPhoneClock(phoneUptimeMillis, now)
		}
	}
	if msgType, _ := msg["type"].(string); msgType == "ticket_state_event" {

		return true
	}
	if msgType == "config" {
		previousEpoch := s.direct.currentStreamEpoch()
		raw = browserVideoConfigMessage(raw)
		if !s.direct.setConfig(raw) {

			return true
		}
		if currentEpoch := s.direct.currentStreamEpoch(); currentEpoch != previousEpoch {
			s.fenceOrdinaryCaptureDemand(currentEpoch)
		}

		_, cachedKeyFrame := s.direct.warmStart()
		for _, c := range s.clientSnapshot() {
			c.enqueueConfigAndKeyframe(raw, cachedKeyFrame, nil)
		}
		return true
	} else if msgType == "health" {
		data, _ := msg["data"].(map[string]any)
		if phoneUptimeMillis := controlCodeInt64FromMessage(data["phoneUptimeMillis"]); phoneUptimeMillis > 0 {
			s.direct.recordPhoneClock(phoneUptimeMillis, now)
		}

		return true
	}
	return false
}

func browserVideoConfigMessage(raw []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return raw
	}
	if payload["type"] != "config" {
		return raw
	}
	payload["serverVersion"] = serverVersion
	payload["assetVersion"] = assetVersion()
	payload["feedbackVersion"] = 2
	frameDependencyMode, _ := payload["frameDependencyMode"].(string)
	if strings.EqualFold(strings.TrimSpace(frameDependencyMode), frameDependencyModeAllIntra) {
		payload["frameDependencyMode"] = frameDependencyModeAllIntra
	}
	next, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return next
}

func (s *Server) writeStateMutation(w http.ResponseWriter, r *http.Request, actor string, event string, snapshot state.Snapshot, err error) {
	if err != nil {
		s.recordRuntimeErrorAsync("state_mutation_failed", event, err, map[string]any{"event": event})
		status := http.StatusConflict
		if errors.Is(err, state.ErrForbidden) || errors.Is(err, state.ErrNotMember) {
			status = http.StatusForbidden
		} else if errors.Is(err, state.ErrInvalidRole) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, apiResponse{OK: false, Error: errorCode(err), Message: err.Error(), State: snapshot})
		return
	}
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	s.cacheSnapshot(snapshot)
	s.recordAuditAsync(s.cfg.TicketID, actor, event, nil, time.Now())
	if redirectAdminForm(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: s.relay.Snapshot()})
}

func adminFormRequest(r *http.Request) bool {
	return strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded")
}

func redirectAdminForm(w http.ResponseWriter, r *http.Request) bool {
	if !adminFormRequest(r) {
		return false
	}
	target := "/admin"
	switch r.URL.Path {
	case "/api/v1/admin/members":
		target += "?tab=members"
	case "/api/v1/admin/phone/backend":
		target += "?tab=settings"
	case "/api/v1/admin/ticket/reselect-latest/schedule":
		target += "?tab=tickets"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
	return true
}

func (s *Server) withMember(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, auth.Identity, string, state.Snapshot)) {
	id, sessionID, snapshot, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	next(w, r, id, sessionID, snapshot)
}

func (s *Server) withMemberCachedFirst(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, auth.Identity, string, state.Snapshot)) {
	id, sessionID, snapshot, ok := s.identifyMemberCachedFirst(w, r)
	if !ok {
		return
	}
	next(w, r, id, sessionID, snapshot)
}

func (s *Server) withAdmin(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, auth.Identity, string, state.Snapshot)) {
	id, sessionID, snapshot, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	if !snapshot.IsAdmin(id.Email) {
		writeErrorPage(w, http.StatusForbidden, "Admin access is required.")
		return
	}
	next(w, r, id, sessionID, snapshot)
}

func (s *Server) withOwner(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, auth.Identity, string, state.Snapshot)) {
	id, sessionID, snapshot, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	member, memberOK := snapshot.Member(id.Email)
	if !memberOK || member.Role != state.RoleOwner {
		http.NotFound(w, r)
		return
	}
	next(w, r, id, sessionID, snapshot)
}

func (s *Server) identifyMember(w http.ResponseWriter, r *http.Request) (auth.Identity, string, state.Snapshot, bool) {
	return s.identifyMemberFromRequest(w, r, memberLookupOptions{
		writeSession: true,
		requireFresh: true,
	})
}

func (s *Server) identifyMemberCachedFirst(w http.ResponseWriter, r *http.Request) (auth.Identity, string, state.Snapshot, bool) {
	return s.identifyMemberFromRequest(w, r, memberLookupOptions{
		writeSession: true,
		cachedFirst:  true,
	})
}

type memberLookupOptions struct {
	writeSession bool
	cachedFirst  bool
	optional     bool
	prewarm      string
	startupRun   string
	openedAt     time.Time
	requireFresh bool
}

func (s *Server) identifyMemberFromRequest(w http.ResponseWriter, r *http.Request, opts memberLookupOptions) (auth.Identity, string, state.Snapshot, bool) {
	id, err := s.auth.IdentityFromRequest(r.Context(), r)
	if err != nil {
		if !opts.optional {
			writeErrorPage(w, http.StatusUnauthorized, "SpacetimeAuth login is required.")
		}
		return auth.Identity{}, "", state.Snapshot{}, false
	}

	sessionID := s.sessionIDNoWrite(r)
	if opts.writeSession && w != nil {
		sessionID = s.sessionID(w, r)
	}
	now := time.Now()
	if opts.cachedFirst {
		if cachedSnapshot, ok := s.cachedSnapshot(now); ok {
			if _, memberOK := cachedSnapshot.Member(id.Email); memberOK {
				s.refreshServerSessionCookie(w, r, id, opts, now)
				// The cached snapshot keeps the page responsive, but it is not
				// authoritative enough to wake the phone.  Re-check membership in
				// the background and only then begin the stream prewarm.  This
				// overlaps page/bootstrap work with startup without allowing a
				// removed member's cached row to retain a relay viewer.
				if strings.TrimSpace(opts.prewarm) != "" {
					scheduleReason := opts.prewarm
					sessionForPrewarm := sessionID
					s.startupRunMu.Lock()
					startupTraceID := s.direct.startOpeningForRun(sessionID, opts.startupRun, "authenticated_index_accepted")
					s.startupRunMu.Unlock()
					go s.prewarmAfterFreshMembership(id, sessionForPrewarm, scheduleReason, startupTraceID, opts.openedAt)
				}
				return id, sessionID, cachedSnapshot, true
			}
		}
	}
	snapshot, err := s.snapshotWithCache(r.Context(), now, s.relay.Snapshot(), stateLookupTimeout)
	if err != nil {
		s.recordRuntimeErrorAsync("ticket_state_lookup_failed", sessionID, err, nil)
		if opts.requireFresh || snapshot.Ticket.ID == "" {
			if !opts.optional {
				writeErrorPage(w, http.StatusServiceUnavailable, "Ticket state is unavailable.")
			}
			return auth.Identity{}, "", state.Snapshot{}, false
		}
	}
	if _, ok := snapshot.Member(id.Email); !ok {
		if !opts.optional {
			writeErrorPage(w, http.StatusForbidden, fmt.Sprintf("No ticket has been assigned to your account (%s). Ask the ticket owner or an administrator to give you access.", id.Email))
		}
		return auth.Identity{}, "", snapshot, false
	}
	s.refreshServerSessionCookie(w, r, id, opts, now)
	if err == nil && strings.TrimSpace(opts.prewarm) != "" {
		s.startupRunMu.Lock()
		defer s.startupRunMu.Unlock()
		startupTraceID := s.direct.startOpeningForRun(sessionID, opts.startupRun, "authenticated_index_accepted")

		s.prewarmStreamForSession(sessionID, opts.prewarm, startupTraceID)
		s.retainRelayViewerForPageOpen(sessionID, opts.openedAt, time.Now())
	}
	return id, sessionID, snapshot, true
}

// prewarmAfterFreshMembership preserves the fast cached index path without
// treating cached membership as authorization to wake the phone.  The lookup
// intentionally bypasses snapshotWithCache, whose fallback is useful for page
// availability but would violate the prewarm trust boundary.
func (s *Server) prewarmAfterFreshMembership(id auth.Identity, sessionID string, reason string, startupTraceID string, openedAt time.Time) {
	if s == nil || s.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	reason = cleanStreamControlText(reason, "index_auth_prewarm")
	traceID := safeRuntimeTraceID("browser", sessionID)
	ctx, cancel := context.WithTimeout(context.Background(), stateLookupTimeout)
	defer cancel()
	snapshot, err := s.store.Snapshot(ctx, s.cfg.TicketID, time.Now())
	if err != nil {
		s.recordRuntimeErrorAsync("ticket_state_prewarm_lookup_failed", traceID, err, map[string]any{
			"reason": reason,
		})
		return
	}
	if _, ok := snapshot.Member(id.Email); !ok {
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_prewarm_rejected_membership", traceID, map[string]any{
			"reason": reason,
		})
		return
	}
	s.startupRunMu.Lock()
	defer s.startupRunMu.Unlock()

	s.prewarmStreamForSession(sessionID, reason, startupTraceID)
	s.retainRelayViewerForPageOpen(sessionID, openedAt, time.Now())
}

func (s *Server) redirectHTTPToHTTPS(w http.ResponseWriter, r *http.Request) bool {
	publicURL, err := url.Parse(s.cfg.PublicBaseURL)
	if err != nil || publicURL.Scheme != "https" || publicURL.Host == "" {
		return false
	}
	if !sameHost(r.Host, publicURL.Host) && !sameHost(r.Header.Get("X-Forwarded-Host"), publicURL.Host) {
		return false
	}
	if isLocalHost(r.Host) {
		return false
	}
	proto := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")))
	if proto == "" {
		proto = strings.ToLower(strings.TrimSpace(r.URL.Scheme))
	}
	if proto == "https" {
		return false
	}
	target := *r.URL
	target.Scheme = "https"
	target.Host = publicURL.Host
	http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
	return true
}

func sameHost(left string, right string) bool {
	leftHost := hostOnly(left)
	rightHost := hostOnly(right)
	return leftHost != "" && rightHost != "" && strings.EqualFold(leftHost, rightHost)
}

func hostOnly(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func isLocalHost(hostport string) bool {
	switch strings.ToLower(hostOnly(hostport)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func (s *Server) clientSnapshot() []*client {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		out = append(out, c)
	}
	return out
}

func (s *Server) broadcastText(data []byte) {
	for _, c := range s.clientSnapshot() {
		c.sendText(context.Background(), data)
	}
}

func (s *Server) broadcastFrame(data []byte) {
	for _, c := range s.clientSnapshot() {
		if c.readyForVideoBroadcast() {
			c.sendBinaryLatest(context.Background(), data)
		}
	}
}

func (s *Server) cacheSnapshot(snapshot state.Snapshot) {
	if snapshot.Ticket.ID == "" {
		return
	}
	s.stateMu.Lock()
	s.cachedState = snapshot
	s.cachedStateAt = time.Now()
	s.stateMu.Unlock()
}

func (s *Server) cachedSnapshot(now time.Time) (state.Snapshot, bool) {
	s.stateMu.RLock()
	snapshot := s.cachedState
	cachedAt := s.cachedStateAt
	s.stateMu.RUnlock()
	if snapshot.Ticket.ID == "" {
		return state.Snapshot{}, false
	}
	if !cachedAt.IsZero() && now.Sub(cachedAt) > stateCacheMaxAge {
		return state.Snapshot{}, false
	}
	snapshot.ServerTime = now.UTC().Format(time.RFC3339)
	return snapshot, true
}

func (s *Server) requestOriginAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	return s.configuredOriginAllowed(origin)
}

// A browser media socket changes durable stream demand and wakes the phone.
// Require its schemeful Origin even though ordinary GET navigation may omit it.
func (s *Server) browserWebSocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	return origin != "" && s.configuredOriginAllowed(origin)
}

func (s *Server) configuredOriginAllowed(origin string) bool {
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" || originURL.User != nil || originURL.Path != "" || originURL.RawQuery != "" || originURL.Fragment != "" {
		return false
	}
	if !strings.EqualFold(originURL.Scheme, "http") && !strings.EqualFold(originURL.Scheme, "https") {
		return false
	}
	publicURL, err := url.Parse(strings.TrimSpace(s.cfg.PublicBaseURL))
	return err == nil &&
		publicURL.Scheme != "" &&
		publicURL.Host != "" &&
		sameSchemefulOrigin(originURL, publicURL)
}

func sameSchemefulOrigin(left *url.URL, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectiveOriginPort(left) == effectiveOriginPort(right)
}

func effectiveOriginPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func randomID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

func mustJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func assetVersion() string {
	assetVersionOnce.Do(func() {
		if release := strings.TrimSpace(os.Getenv("ARBUZAS_RELEASE_ID")); release != "" {
			assetVersionValue = release
			return
		}
		assetVersionValue = serverVersion
	})
	return assetVersionValue
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, state.ErrForbidden):
		return "forbidden"
	case errors.Is(err, state.ErrNotMember):
		return "not_member"
	case errors.Is(err, state.ErrInvalidRole):
		return "bad_role"
	case errors.Is(err, state.ErrOwnerProtected):
		return "owner_protected"
	default:
		return "error"
	}
}
