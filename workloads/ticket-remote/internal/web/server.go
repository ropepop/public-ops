package web

import (
	"context"
	"crypto/rand"
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
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

//go:embed static/*
var staticFS embed.FS

type Server struct {
	cfg       config.Config
	store     state.Store
	relay     *phone.Relay
	auth      *auth.Validator
	direct    *directStreamHub
	static    fs.FS
	indexTmpl *template.Template
	adminTmpl *template.Template
	authTmpl  *template.Template

	mu                  sync.Mutex
	clients             map[*client]struct{}
	relayViewerRefs     map[string]int
	streamPrewarmTimers map[string]*time.Timer

	stateMu       sync.RWMutex
	cachedState   state.Snapshot
	cachedStateAt time.Time

	pixelEventMu          sync.RWMutex
	lastPixelTicketEvent  pixelTicketEvent
	lastPixelTicketHealth string

	phoneStartMu           sync.Mutex
	lastPhoneStartAttempt  time.Time
	phoneHTTPStartInFlight bool
	lastPhoneHTTPStartAt   time.Time

	streamRecoveryMu            sync.Mutex
	lastStreamRecoveryAt        time.Time
	lastStreamRecoveryStage     string
	lastStreamRecoveryAction    string
	lastStreamRecoveryResult    string
	lastStreamRecoveryReason    string
	lastStreamRecoveryFailure   string
	lastStreamRecoveryCommandID string

	relayProductMu         sync.Mutex
	lastRelayStreamVerdict string
	lastRelayDropTotal     uint64
	relayReportWake        chan string
	relayReportCancel      context.CancelFunc
	relayReportDone        chan struct{}

	streamDesiredReleaseMu    sync.Mutex
	streamDesiredReleaseTimer *time.Timer
	streamDesiredReleaseSeq   uint64
	streamLifecycleMu         sync.RWMutex
	browserClientLogMu        sync.Mutex
	browserClientLogWindow    time.Time
	browserClientLogCount     int

	backendMu sync.RWMutex
}

var (
	assetVersionOnce  sync.Once
	assetVersionValue string
)

type client struct {
	conn      *websocket.Conn
	sessionID string
	email     string
	sendMu    sync.Mutex

	clientLogMu          sync.Mutex
	clientLogWindowStart time.Time
	clientLogCount       int

	videoMu              sync.Mutex
	videoWriteActive     bool
	videoPendingFrame    []byte
	videoPendingKeyFrame bool
	videoPendingAt       time.Time
	videoReadyForDelta   bool
	videoReadyEpoch      uint64

	firstVideoFrameRendered bool
}

type apiResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Message string         `json:"message,omitempty"`
	State   state.Snapshot `json:"state,omitempty"`
	Phone   phone.Health   `json:"phone,omitempty"`
}

const (
	serverVersion                 = "ticket-remote-2026-07-11-painted-browser-captured-control-code-v120"
	stateLookupTimeout            = 1200 * time.Millisecond
	stateCacheMaxAge              = 30 * time.Second
	maxBrowserClientLogsPerMinute = 60

	streamRecoveryCommandCooldown = 10 * time.Second
	streamPrewarmHold             = 30 * time.Second
	publicOpenGraceHold           = 30 * time.Second
	streamDesiredIdleReleaseGrace = 60 * time.Second
	streamPrewarmHTTPStartTimeout = 5 * time.Second
	streamPrewarmHTTPStartDedupe  = 2 * time.Second
	videoWriteTimeout             = 250 * time.Millisecond
	videoPendingFrameMaxAge       = 250 * time.Millisecond
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
	relayReportCtx, relayReportCancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:                 cfg,
		store:               store,
		relay:               relay,
		auth:                auth.NewValidator(cfg.Access),
		direct:              newDirectStreamHub(),
		static:              staticSub,
		indexTmpl:           template.Must(template.New("index").Parse(indexHTML)),
		adminTmpl:           template.Must(template.New("admin").Parse(adminHTML)),
		authTmpl:            template.Must(template.New("auth").Parse(authRedirectHTML)),
		clients:             map[*client]struct{}{},
		relayViewerRefs:     map[string]int{},
		streamPrewarmTimers: map[string]*time.Timer{},
		relayReportWake:     make(chan string, 1),
		relayReportCancel:   relayReportCancel,
		relayReportDone:     make(chan struct{}),
	}
	relay.SetHandlers(s.handlePhoneMessage, s.handlePhoneDisconnect)
	// Pixel owns Spacetime command execution. The server writes durable commands
	// and uses the direct bridge relay only for video transport.
	go s.relayReportLoop(relayReportCtx)
	return s, nil
}

func (s *Server) Close() {
	s.cancelIdleStreamDesiredRelease()
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
	if !s.requestOriginAllowed(r) {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "bad_origin", Message: "Request origin is not allowed."})
		return
	}
	switch {
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
		s.withMemberCachedFirst(w, r, func(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, snapshot state.Snapshot) {
			s.handleHealth(w, r, snapshot)
		})
	case strings.HasPrefix(path, "/static/"):
		if strings.TrimSpace(r.URL.Query().Get("v")) == "" {
			writeNoStoreHeaders(w)
		} else {
			writeStaticAssetHeaders(w)
		}
		http.StripPrefix("/static/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
	case retiredTicketRoute(path):
		handleRetiredTicketRoute(w)
	case path == "/api/v1/stream":
		s.handleBrowserSocket(w, r)
	case path == "/api/v1/stream/prewarm":
		s.withMember(w, r, s.handleStreamPrewarmHTTP)
	case path == "/api/v1/internal/service-events":
		s.handleServiceEvent(w, r)
	case path == "/api/v1/admin/state":
		s.withAdmin(w, r, s.handleAdminState)
	case path == "/api/v1/admin/members":
		s.withAdmin(w, r, s.handleAdminMembers)
	case path == "/api/v1/admin/phone/backends":
		s.withAdmin(w, r, s.handleAdminPhoneBackends)
	case path == "/api/v1/admin/phone/backend":
		s.withAdmin(w, r, s.handleAdminPhoneBackend)
	case path == "/api/v1/admin/ticket/reselect-latest":
		s.withAdmin(w, r, s.handleAdminTicketReselectLatest)
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
		"/api/v1/admin/control/revoke":
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
	if s.usesSpacetimeAuth() {
		id, sessionID, snapshot, ok := s.identifyMemberFromRequest(w, r, memberLookupOptions{
			optional:     true,
			cachedFirst:  true,
			prewarm:      "index_auth_prewarm",
			writeSession: true,
		})
		if ok {
			if sessionID == "" {
				sessionID = s.sessionID(w, r)
			}
			s.handleIndex(w, r, id, sessionID, snapshot)
			return
		}
		s.handleUnauthIndex(w)
		return
	}
	id, sessionID, snapshot, ok := s.identifyMemberFromRequest(w, r, memberLookupOptions{
		writeSession: true,
		cachedFirst:  true,
		prewarm:      "index_auth_prewarm",
	})
	if !ok {
		return
	}
	s.handleIndex(w, r, id, sessionID, snapshot)
}

func (s *Server) handleAdminShell(w http.ResponseWriter, r *http.Request) {
	if s.usesSpacetimeAuth() {
		id, sessionID, snapshot, ok := s.identifyMemberFromRequest(nil, r, memberLookupOptions{optional: true})
		if !ok {
			s.handleUnauthIndex(w)
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

func (s *Server) handleUnauthIndex(w http.ResponseWriter) {
	nonce := randomID()
	s.writeHTMLHeaders(w, nonce)
	_ = s.authTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"ConfigJSON":   template.JS(mustJSON(s.publicBrowserConfig(auth.Identity{}, "", state.Snapshot{}, false))),
		"Nonce":        nonce,
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

func (s *Server) prewarmStreamForSession(sessionID string, reason string) {
	cleanSessionID := strings.TrimSpace(sessionID)
	if cleanSessionID == "" {
		cleanSessionID = randomID()
	}
	cleanReason := strings.TrimSpace(reason)
	if cleanReason == "" {
		cleanReason = "stream_prewarm"
	}
	s.direct.beginStartupTrace(cleanSessionID, cleanReason)
	s.direct.recordStartupPhase("prewarm_accepted", cleanReason)
	prewarmID := streamPrewarmRelayLeaseID(cleanSessionID)
	s.retainRelayViewerForPrewarm(prewarmID, streamPrewarmHold)
	s.startPhoneSessionForPrewarm(cleanReason)
	s.requestPhoneKeyframe(cleanReason)
}

func streamPrewarmRelayLeaseID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return randomID()
	}
	return sessionID
}

func (s *Server) startPhoneSessionForPrewarm(reason string) {
	now := time.Now()
	s.phoneStartMu.Lock()
	if !s.lastPhoneHTTPStartAt.IsZero() && now.Sub(s.lastPhoneHTTPStartAt) < streamPrewarmHTTPStartDedupe {
		s.phoneStartMu.Unlock()
		s.direct.recordStartupPhase("stream_start_dedupe", reason)
		return
	}
	s.lastPhoneHTTPStartAt = now
	s.phoneStartMu.Unlock()
	s.direct.recordStartupPhase("stream_start_command_queued", reason)
	s.appendStreamCommandAsync("start", reason, map[string]any{
		"source": "ticket_remote",
	}, streamCommandTTL)
}

func publicHealthError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	for _, marker := range []string{"\n\nStack backtrace:", "\nStack backtrace:", "Stack backtrace:"} {
		if index := strings.Index(text, marker); index >= 0 {
			text = text[:index]
			break
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	const maxHealthErrorLength = 240
	if len(text) > maxHealthErrorLength {
		return strings.TrimSpace(text[:maxHealthErrorLength]) + "..."
	}
	return text
}

func redactSnapshotForHealth(snapshot state.Snapshot) state.Snapshot {
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

func (s *Server) snapshotForHealth(ctx context.Context, now time.Time, phoneHealth phone.Health) (state.Snapshot, error) {
	return s.snapshotWithCache(ctx, now, phoneHealth, stateLookupTimeout)
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

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	nonce := randomID()
	s.writeHTMLHeaders(w, nonce)
	_ = s.indexTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"ConfigJSON":   template.JS(mustJSON(s.publicBrowserConfig(id, sessionID, snapshot, true))),
		"IsAdmin":      snapshot.IsAdmin(id.Email),
		"Nonce":        nonce,
	})
}

func (s *Server) publicBrowserConfig(id auth.Identity, sessionID string, snapshot state.Snapshot, authenticated bool) map[string]any {
	email := id.Email
	authMode := s.publicAuthMode()
	return map[string]any{
		"publicBaseUrl": s.cfg.PublicBaseURL,
		"authenticated": authenticated,
		"email":         email,
		"sessionId":     sessionID,
		"stateBackend":  snapshot.StateBackend,
		"ticketId":      s.cfg.TicketID,
		"pageVersion":   serverVersion,
		"assetVersion":  assetVersion(),
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

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	nonce := randomID()
	s.writeHTMLHeaders(w, nonce)
	member, _ := snapshot.Member(id.Email)
	members := make([]state.Member, 0, len(snapshot.Members))
	viewers := 0
	for _, item := range snapshot.Members {
		if item.Active {
			members = append(members, item)
		}
	}
	for _, viewer := range snapshot.Viewers {
		if viewer.Connected {
			viewers++
		}
	}
	phoneHealth := s.relay.Snapshot()
	activeBackend := s.activePhoneBackend()
	_ = s.adminTmpl.Execute(w, map[string]any{
		"AssetVersion":  assetVersion(),
		"Email":         id.Email,
		"IsOwner":       member.Role == state.RoleOwner,
		"Members":       members,
		"ViewerCount":   viewers,
		"Phone":         phoneHealth,
		"Backends":      s.configuredPhoneBackends(),
		"ActiveBackend": activeBackend.ID,
		"RawState":      mustJSON(map[string]any{"state": snapshot, "phone": phoneHealth}),
		"Nonce":         nonce,
	})
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
	snapshot, err := s.store.Snapshot(r.Context(), s.cfg.TicketID, time.Now())
	if err != nil {
		writeErrorPage(w, http.StatusServiceUnavailable, "Ticket state is unavailable.")
		return
	}
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	s.cacheSnapshot(snapshot)
	if _, ok := snapshot.Member(id.Email); !ok {
		writeErrorPage(w, http.StatusForbidden, fmt.Sprintf("The signed-in email %s is not linked to this ticket.", id.Email))
		return
	}
	sessionToken, _, err := s.auth.IssueServerSession(id, s.cfg.CookieTTL, time.Now())
	if err != nil {
		writeErrorPage(w, http.StatusInternalServerError, "Ticket session could not be created.")
		return
	}
	s.setAuthCookie(w, sessionToken, s.authCookieMaxAge())
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
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "auth_required", Message: "SpacetimeAuth login is required."})
			return
		}
		if sessionID == "" {
			sessionID = s.sessionID(w, r)
		}
		s.setPrivateAuthCookie(w, "ticket_remote_direct_spacetime", "", -1)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"authenticated": true,
			"email":         id.Email,
			"sessionId":     sessionID,
			"state":         snapshot.PublicForMember(id.Email),
			"spacetime":     s.directSpacetimeSessionFromRequest(r, id.Email),
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
				Message: fmt.Sprintf("The signed-in email %s is not linked to this ticket.", id.Email),
			})
			return
		}
		sessionToken, sessionExpiresAt, err := s.auth.IssueServerSession(id, s.cfg.CookieTTL, time.Now())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "session_failed", Message: "Ticket session could not be created."})
			return
		}
		s.setAuthCookie(w, sessionToken, s.authCookieMaxAge())
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
	s.setPrivateAuthCookie(w, "ticket_remote_direct_spacetime", "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	relayHealth := s.relay.Snapshot()
	snapshot = s.withFreshActivePhoneHealth(r.Context(), snapshot, relayHealth)
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: relayHealth})
}

func (s *Server) handleAdminMembers(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	switch r.Method {
	case http.MethodPost:
		if adminFormRequest(r) && r.FormValue("action") == "remove" {
			snapshot, err := s.store.RemoveMember(r.Context(), s.cfg.TicketID, id.Email, r.FormValue("email"))
			s.writeStateMutation(w, r, id.Email, "member_remove", snapshot, err, false)
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
		s.writeStateMutation(w, r, id.Email, "member_upsert", snapshot, err, false)
	case http.MethodDelete:
		email := strings.TrimSpace(r.URL.Query().Get("email"))
		snapshot, err := s.store.RemoveMember(r.Context(), s.cfg.TicketID, id.Email, email)
		s.writeStateMutation(w, r, id.Email, "member_remove", snapshot, err, false)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
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

func (s *Server) handleAdminTicketReselectLatest(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1024))
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "state_unavailable", Message: "Ticket state is unavailable."})
		return
	}
	backend := s.activePhoneBackend()
	if strings.TrimSpace(backend.ID) == "" {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "phone_backend_unavailable", Message: "No active phone backend is configured."})
		return
	}
	if strings.TrimSpace(backend.BaseURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "phone_backend_unavailable", Message: "The active phone backend has no connection URL."})
		return
	}
	now := time.Now()
	payload := map[string]any{
		"type":      "force_ticket_reselect",
		"source":    "ticket_remote_admin",
		"reason":    "admin_force_latest_ticket_reselect",
		"backendId": backend.ID,
	}
	ctx, cancel := context.WithTimeout(r.Context(), streamControlWriteTimeout)
	defer cancel()
	commandID, err := s.appendStreamCommand(ctx, "force_ticket_reselect", "admin_force_latest_ticket_reselect", payload, streamCommandTTL)
	if err != nil {
		s.recordRuntimeErrorAsync("latest_ticket_reselect_command_failed", backend.ID, err, map[string]any{"backendId": backend.ID})
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "command_failed", Message: "Latest ticket reselect could not be requested."})
		return
	}
	s.recordAuditAsync(s.cfg.TicketID, id.Email, "latest_ticket_reselect_requested", map[string]any{
		"commandId": commandID,
		"backendId": backend.ID,
	}, now)
	s.recordRuntimeEventForSourceAsync("ticket_remote_admin", "info", "latest_ticket_reselect_requested", commandID, map[string]any{
		"commandId": commandID,
		"backendId": backend.ID,
	})
	relayHealth := s.relay.Snapshot()
	snapshot = s.withActivePhoneBackend(snapshot, relayHealth)
	s.cacheSnapshot(snapshot)
	if redirectAdminForm(w, r) {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":        true,
		"commandId": commandID,
		"state":     snapshot,
		"phone":     relayHealth,
	})
}

func (s *Server) handleBrowserSocket(w http.ResponseWriter, r *http.Request) {
	// A video connection wakes the phone, so it must use a current membership
	// lookup rather than the short-lived page cache.
	id, sessionID, _, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c := &client{conn: conn, sessionID: sessionID, email: id.Email}
	if !s.tryAddClient(c) {
		_ = conn.Close(websocket.StatusPolicyViolation, "connection limit reached")
		return
	}
	traceID := safeRuntimeTraceID("browser", sessionID)
	s.direct.beginStartupTrace(sessionID, "video_socket_open")
	s.direct.recordStartupPhase("video_socket_accepted", "")
	detail := map[string]any{
		"video":   true,
		"version": serverVersion,
	}
	for key, value := range browserVideoSocketContext(r) {
		detail[key] = value
	}
	s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "video_socket_open", traceID, detail)
	s.addRelayViewer(sessionID)
	s.retainRelayViewerForPublicOpenGrace(sessionID, publicOpenGraceHold, "video_socket_open")
	s.cancelIdleStreamDesiredRelease()
	s.direct.addVideoClient()
	s.direct.recordStartupPhase("video_client_registered", fmt.Sprintf("active=%d", s.direct.activeVideoClientCount()))
	s.wakePhoneStreamFromVideoSocketOpen("video_socket_open")
	s.sendBrowserVideoWarmStart(c)
	s.publishRelayCurrentReportAsync("video_socket_open")
	defer func() {
		s.removeClient(c)
		s.direct.removeVideoClient()
		s.direct.recordStartupPhase("video_socket_closed", fmt.Sprintf("active=%d", s.direct.activeVideoClientCount()))
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "video_socket_closed", traceID, map[string]any{
			"activeVideoClients": s.direct.activeVideoClientCount(),
		})
		if !c.firstVideoFrameRendered {
			s.retainRelayViewerForPublicOpenGrace(sessionID, publicOpenGraceHold, "video_socket_closed_before_first_frame")
		}
		s.publishRelayCurrentReportAsync("video_socket_closed")
		s.scheduleIdleStreamDesiredRelease("relay_no_video_clients")
		s.removeRelayViewer(sessionID)
		_ = conn.Close(websocket.StatusNormalClosure, "closed")
	}()
	for {
		typ, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		s.handleVideoStreamMessage(r.Context(), c, data)
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

func (s *Server) sendBrowserVideoWarmStart(c *client) {
	if configFrame, keyFrame := s.direct.warmStart(); len(configFrame) > 0 {
		c.sendText(context.Background(), configFrame)
		s.direct.recordWarmStartSent(true, len(keyFrame) > 0)
		s.direct.recordStartupPhaseOnce("warm_config_sent", fmt.Sprintf("keyframe=%t", len(keyFrame) > 0))
		if len(keyFrame) > 0 {
			s.direct.recordStartupPhaseOnce("warm_keyframe_sent", "cached_keyframe=true")
			c.noteVideoKeyFrame(keyFrame)
			c.sendBinary(context.Background(), keyFrame)
		} else {
			s.direct.recordStartupPhase("warm_config_sent", "provisional=true")
			s.requestPhoneKeyframe("browser_video_provisional_config")
		}
		return
	}
	s.direct.recordWarmStartSent(false, false)
	s.direct.recordStartupPhase("warm_start_miss", "config=false")
	s.requestPhoneKeyframe("browser_video_config_needed")
}

func (s *Server) handleVideoStreamMessage(ctx context.Context, c *client, data []byte) {
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "client_log":
		event, detail, detailText, ok := decodeBrowserClientLog(data)
		if !ok {
			return
		}
		// This message is both a lifecycle acknowledgement and a diagnostic.
		// Stream readiness must not depend on diagnostic persistence capacity.
		if event == "stream_first_rendered_frame" && !c.firstVideoFrameRendered {
			c.firstVideoFrameRendered = true
			s.direct.completeStartupTrace("browser_first_rendered_frame", detailText)
			s.releaseRelayViewerPublicOpenGrace(c.sessionID, "stream_first_rendered_frame")
		}
		now := time.Now()
		if !c.allowClientLog(now) || !s.allowBrowserClientLog(now) {
			return
		}
		s.direct.recordClientTelemetry(event, detailText)
		detail["video"] = true
		s.recordRuntimeEventForSourceAsync("ticket_remote_browser", "info", event, safeRuntimeTraceID("browser", c.sessionID), detail)
		return
	case "keyframe", "recover_stream":
		return
	default:
		return
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
	if len(msg.Text) > 0 {
		if s.handlePhoneText(msg.Text) {
			return
		}
		s.broadcastText(msg.Text)
		return
	}
	if len(msg.Binary) > 0 {
		if frame, ok := s.direct.recordFrameForBroadcast(msg.Binary); ok {
			meta := parseTSF2(frame)
			if meta.ok {
				if meta.keyFrame {
					s.direct.recordStartupPhaseOnce("first_forwarded_keyframe", fmt.Sprintf("epoch=%d sequence=%d", meta.epoch, meta.sequence))
				}
				s.direct.completeStartupTrace("first_forwarded_frame", fmt.Sprintf("epoch=%d sequence=%d keyframe=%t", meta.epoch, meta.sequence, meta.keyFrame))
			}
			s.broadcastFrame(frame)
		}
	}
}

func (s *Server) handlePhoneDisconnect(err error) {
	if err != nil && !expectedPhoneDisconnect(err) {
		s.recordRuntimeErrorAsync("phone_stream_disconnected", "", err, nil)
	}
	if err != nil {
		s.direct.recordStartupPhase("phone_stream_disconnected", publicHealthError(err))
	} else {
		s.direct.recordStartupPhase("phone_stream_disconnected", "")
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
		stateName, _ := msg["ticketState"].(string)
		reason, _ := msg["reason"].(string)
		s.direct.recordStartupPhase("phone_ticket_state_event", fmt.Sprintf("state=%s reason=%s", stateName, reason))
		s.handlePixelTicketStateEvent(msg)
		return true
	}
	if s.handlePixelTraceEvent(msg) {
		return true
	}
	if msgType == "config" {
		raw = browserVideoConfigMessage(raw)
		s.direct.setConfig(raw)
		s.direct.recordStartupPhaseOnce("phone_config_received", "config=true")
		s.resetVideoDeltaReadiness()
		if s.direct.needsActiveViewerKeyframe(now) {
			s.requestPhoneKeyframe("phone_config_active_viewer")
		}
		s.broadcastText(raw)
		return true
	} else if msgType == "health" {
		data, _ := msg["data"].(map[string]any)
		if data != nil {
			streamVerdict, _ := data["streamVerdict"].(string)
			sessionState, _ := data["sessionState"].(string)
			s.direct.recordStartupPhase("phone_health_received", fmt.Sprintf("session=%s verdict=%s", sessionState, streamVerdict))
		}
		if phoneUptimeMillis := controlCodeInt64FromMessage(data["phoneUptimeMillis"]); phoneUptimeMillis > 0 {
			s.direct.recordPhoneClock(phoneUptimeMillis, now)
		}
		healthJSON := s.mergeLatestPixelTicketEventIntoHealth(string(raw))
		backend := s.activePhoneBackend()
		phoneInput := state.PhoneInput{
			TicketID:     s.cfg.TicketID,
			BackendID:    backend.ID,
			AttachName:   backend.AttachName,
			BaseURL:      backend.BaseURL,
			DesiredState: "streaming",
			HealthJSON:   healthJSON,
			Now:          now,
		}
		if err := s.store.UpdatePhoneStatus(context.Background(), phoneInput); err == nil {
			s.cachePhoneStatusUpdate(phoneInput, s.relay.Snapshot())
		} else {
			s.recordRuntimeErrorAsync("phone_state_update_failed", backend.ID, err, map[string]any{"backendId": backend.ID})
		}
		s.maybeRequestPhoneStart(data, "phone_health")
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
	next, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return next
}

func pixelTicketEventFromMessage(msg map[string]any) (pixelTicketEvent, bool) {
	msgType, _ := msg["type"].(string)
	if strings.TrimSpace(msgType) != "ticket_state_event" {
		return pixelTicketEvent{}, false
	}
	ticketState, _ := msg["ticketState"].(string)
	ticketState = strings.TrimSpace(ticketState)
	if ticketState == "" {
		return pixelTicketEvent{}, false
	}
	reason, _ := msg["reason"].(string)
	requestID, _ := msg["requestId"].(string)
	resultProof, _ := msg["resultProof"].(string)
	resultProofAt, _ := msg["resultProofAt"].(string)
	event := pixelTicketEvent{
		Type:                   "ticket_state_event",
		EventSeq:               controlCodeInt64FromMessage(msg["eventSeq"]),
		TicketState:            ticketState,
		Reason:                 strings.TrimSpace(reason),
		RequestID:              strings.TrimSpace(requestID),
		StreamEpoch:            controlCodeInt64FromMessage(msg["streamEpoch"]),
		FrameSequence:          controlCodeInt64FromMessage(msg["frameSequence"]),
		MinFrameSequence:       controlCodeInt64FromMessage(msg["minFrameSequence"]),
		ResultProof:            cleanControlCodeResultProof(resultProof),
		ResultFrameEpoch:       controlCodeInt64FromMessage(msg["resultFrameEpoch"]),
		ResultMinFrameSequence: controlCodeInt64FromMessage(msg["resultMinFrameSequence"]),
		ResultProofAt:          strings.TrimSpace(resultProofAt),
		PhoneUptimeMillis:      controlCodeInt64FromMessage(msg["phoneUptimeMillis"]),
		TotalDurationMillis:    controlCodeInt64FromMessage(msg["totalDurationMillis"]),
		Phases:                 controlCodePhasesFromMessage(msg["phases"]),
		At:                     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if event.Reason == "" {
		event.Reason = event.TicketState
	}
	return event, true
}

func (s *Server) handlePixelTicketStateEvent(msg map[string]any) {
	event, ok := pixelTicketEventFromMessage(msg)
	if !ok {
		return
	}
	if !s.direct.recordPixelTicketEvent(event) {
		return
	}
	s.rememberPixelTicketEvent(event)
	s.updateStoredPhoneTicketEvent(event)
}

func (s *Server) rememberPixelTicketEvent(event pixelTicketEvent) {
	health := s.mergePixelTicketEventIntoHealth("", event)
	s.pixelEventMu.Lock()
	s.lastPixelTicketEvent = event
	s.lastPixelTicketHealth = health
	s.pixelEventMu.Unlock()
}

func (s *Server) mergeLatestPixelTicketEventIntoHealth(raw string) string {
	s.pixelEventMu.RLock()
	event := s.lastPixelTicketEvent
	s.pixelEventMu.RUnlock()
	if strings.TrimSpace(event.TicketState) == "" {
		return raw
	}
	return s.mergePixelTicketEventIntoHealth(raw, event)
}

func (s *Server) mergePixelTicketEventIntoHealth(raw string, event pixelTicketEvent) string {
	if strings.TrimSpace(event.TicketState) == "" {
		return raw
	}
	payload := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	payload["type"] = "health"
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	data["ticketStateEvent"] = map[string]any{
		"eventSeq":          event.EventSeq,
		"ticketState":       event.TicketState,
		"reason":            event.Reason,
		"requestId":         event.RequestID,
		"streamEpoch":       event.StreamEpoch,
		"frameSequence":     event.FrameSequence,
		"minFrameSequence":  event.MinFrameSequence,
		"phoneUptimeMillis": event.PhoneUptimeMillis,
		"at":                event.At,
	}
	if event.TotalDurationMillis > 0 {
		data["ticketStateEvent"].(map[string]any)["totalDurationMillis"] = event.TotalDurationMillis
	}
	if len(event.Phases) > 0 {
		data["ticketStateEvent"].(map[string]any)["phases"] = event.Phases
	}
	payload["data"] = data
	body, err := json.Marshal(payload)
	if err != nil {
		return raw
	}
	return string(body)
}

func (s *Server) updateStoredPhoneTicketEvent(event pixelTicketEvent) {
	now := time.Now()
	raw := ""
	if cached, ok := s.cachedSnapshot(now); ok && cached.Phone != nil {
		raw = cached.Phone.HealthJSON
	}
	healthJSON := s.mergePixelTicketEventIntoHealth(raw, event)
	backend := s.activePhoneBackend()
	phoneInput := state.PhoneInput{
		TicketID:     s.cfg.TicketID,
		BackendID:    backend.ID,
		AttachName:   backend.AttachName,
		BaseURL:      backend.BaseURL,
		DesiredState: s.relay.Snapshot().StreamState,
		HealthJSON:   healthJSON,
		Now:          now,
	}
	if err := s.store.UpdatePhoneStatus(context.Background(), phoneInput); err == nil {
		s.cachePhoneStatusUpdate(phoneInput, s.relay.Snapshot())
	} else {
		s.recordRuntimeErrorAsync("phone_ticket_state_update_failed", event.RequestID, err, map[string]any{
			"requestId":   event.RequestID,
			"ticketState": event.TicketState,
		})
	}
}

func (s *Server) writeStateMutation(w http.ResponseWriter, r *http.Request, actor string, event string, snapshot state.Snapshot, err error, publicState bool) {
	if err != nil {
		s.recordRuntimeErrorAsync("state_mutation_failed", event, err, map[string]any{"event": event})
		status := http.StatusConflict
		if errors.Is(err, state.ErrForbidden) || errors.Is(err, state.ErrNotMember) {
			status = http.StatusForbidden
		}
		if publicState {
			writeJSON(w, status, map[string]any{
				"ok":      false,
				"error":   errorCode(err),
				"message": err.Error(),
				"state":   snapshot.PublicForMember(actor),
			})
			return
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
	if publicState {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": snapshot.PublicForMember(actor), "phone": s.relay.Snapshot()})
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
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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
			writeErrorPage(w, http.StatusForbidden, fmt.Sprintf("The signed-in email %s is not linked to this ticket.", id.Email))
		}
		return auth.Identity{}, "", snapshot, false
	}
	s.refreshServerSessionCookie(w, r, id, opts, now)
	if err == nil && strings.TrimSpace(opts.prewarm) != "" {
		s.prewarmStreamForSession(sessionID, opts.prewarm)
	}
	return id, sessionID, snapshot, true
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
		c.sendBinaryLatest(context.Background(), data)
	}
}

func (s *Server) resetVideoDeltaReadiness() {
	for _, c := range s.clientSnapshot() {
		c.videoMu.Lock()
		c.videoReadyForDelta = false
		c.videoReadyEpoch = 0
		c.videoMu.Unlock()
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
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		return false
	}
	allowedHosts := []string{r.Host}
	if forwardedHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
		allowedHosts = append(allowedHosts, forwardedHost)
	}
	if publicURL, err := url.Parse(s.cfg.PublicBaseURL); err == nil && publicURL.Host != "" {
		allowedHosts = append(allowedHosts, publicURL.Host)
	}
	for _, host := range allowedHosts {
		if strings.EqualFold(originURL.Host, strings.TrimSpace(host)) {
			return true
		}
	}
	return false
}

func (c *client) sendText(ctx context.Context, value []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.conn.Write(ctx, websocket.MessageText, value)
}

func (c *client) sendBinary(ctx context.Context, value []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.conn.Write(ctx, websocket.MessageBinary, value)
}

func (c *client) sendBinaryLatest(ctx context.Context, value []byte) {
	if len(value) == 0 {
		return
	}
	frame := append([]byte(nil), value...)
	keyFrame := frameIsKeyframe(frame)
	meta := parseTSF2(frame)
	now := time.Now()
	c.videoMu.Lock()
	if !keyFrame {
		if !c.videoReadyForDelta || (meta.ok && c.videoReadyEpoch != 0 && meta.epoch != c.videoReadyEpoch) {
			c.videoMu.Unlock()
			return
		}
	} else {
		c.noteVideoKeyFrameLocked(meta)
	}
	if c.videoWriteActive {
		c.queuePendingVideoFrameLocked(frame, keyFrame, now)
		c.videoMu.Unlock()
		return
	}
	c.videoWriteActive = true
	c.videoMu.Unlock()
	go c.videoWriteLoop(ctx, frame)
}

func (c *client) noteVideoKeyFrame(frame []byte) {
	meta := parseTSF2(frame)
	c.videoMu.Lock()
	c.noteVideoKeyFrameLocked(meta)
	c.videoMu.Unlock()
}

func (c *client) noteVideoKeyFrameLocked(meta tsf2Metadata) {
	c.videoReadyForDelta = true
	if meta.ok {
		c.videoReadyEpoch = meta.epoch
	}
}

func (c *client) queuePendingVideoFrameLocked(frame []byte, keyFrame bool, now time.Time) {
	if keyFrame {
		c.videoPendingFrame = frame
		c.videoPendingKeyFrame = true
		c.videoPendingAt = now
		return
	}
	if len(c.videoPendingFrame) == 0 {
		c.videoPendingFrame = frame
		c.videoPendingKeyFrame = false
		c.videoPendingAt = now
		return
	}
	c.videoPendingFrame = nil
	c.videoPendingKeyFrame = false
	c.videoPendingAt = time.Time{}
	c.videoReadyForDelta = false
	c.videoReadyEpoch = 0
}

func (c *client) videoWriteLoop(ctx context.Context, frame []byte) {
	for {
		writeCtx, cancel := context.WithTimeout(ctx, videoWriteTimeout)
		c.sendBinary(writeCtx, frame)
		err := writeCtx.Err()
		cancel()
		if err != nil {
			_ = c.conn.Close(websocket.StatusPolicyViolation, "video client too slow")
			return
		}
		c.videoMu.Lock()
		if len(c.videoPendingFrame) == 0 {
			c.videoWriteActive = false
			c.videoMu.Unlock()
			return
		}
		if videoPendingFrameStale(c.videoPendingAt, time.Now()) {
			c.videoPendingFrame = nil
			c.videoPendingKeyFrame = false
			c.videoPendingAt = time.Time{}
			c.videoWriteActive = false
			c.videoReadyForDelta = false
			c.videoReadyEpoch = 0
			c.videoMu.Unlock()
			return
		}
		frame = c.videoPendingFrame
		c.videoPendingFrame = nil
		c.videoPendingKeyFrame = false
		c.videoPendingAt = time.Time{}
		c.videoMu.Unlock()
	}
}

func videoPendingFrameStale(pendingAt time.Time, now time.Time) bool {
	return !pendingAt.IsZero() && now.Sub(pendingAt) > videoPendingFrameMaxAge
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
	default:
		return "error"
	}
}
