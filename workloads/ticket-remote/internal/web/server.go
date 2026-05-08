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
	"log"
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

	mu              sync.Mutex
	clients         map[*client]struct{}
	relayViewerRefs map[string]int

	gateMu sync.RWMutex
	gate   *controlGate

	stateMu       sync.RWMutex
	cachedState   state.Snapshot
	cachedStateAt time.Time

	quickClaimMu   sync.RWMutex
	lastQuickClaim quickClaimDiagnostic

	phoneStartMu          sync.Mutex
	lastPhoneStartAttempt time.Time

	backendMu   sync.RWMutex
	setupMu     sync.Mutex
	setupRunner simulatorSetupRunner
}

type client struct {
	conn      *websocket.Conn
	sessionID string
	email     string
	page      string
	video     bool
	sendMu    sync.Mutex
}

type controlGate struct {
	sessionID string
	email     string
	expiresAt time.Time
}

type quickClaimDiagnostic struct {
	At        string `json:"at,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Email     string `json:"email,omitempty"`
	InputID   string `json:"inputId,omitempty"`
	Action    string `json:"action,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Forwarded bool   `json:"forwarded"`
}

type apiResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Message string         `json:"message,omitempty"`
	State   state.Snapshot `json:"state,omitempty"`
	Phone   phone.Health   `json:"phone,omitempty"`
}

const (
	serverVersion      = "ticket-remote-2026-05-08-browser-tap-queue-v1"
	stateLookupTimeout = 1200 * time.Millisecond
	stateCacheMaxAge   = 30 * time.Second
)

func NewServer(cfg config.Config, store state.Store, relay *phone.Relay) (*Server, error) {
	if cfg.SimulatorSetup.BackendID == "" {
		cfg.SimulatorSetup.BackendID = "android-sim"
	}
	if cfg.SimulatorSetup.ADBTarget == "" {
		cfg.SimulatorSetup.ADBTarget = "ticket_android_sim:5555"
	}
	if cfg.SimulatorSetup.ADBPath == "" {
		cfg.SimulatorSetup.ADBPath = "adb"
	}
	if cfg.SimulatorSetup.Timeout <= 0 {
		cfg.SimulatorSetup.Timeout = 8 * time.Second
	}
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:             cfg,
		store:           store,
		relay:           relay,
		auth:            auth.NewValidator(cfg.Access),
		direct:          newDirectStreamHub(),
		static:          staticSub,
		indexTmpl:       template.Must(template.New("index").Parse(indexHTML)),
		adminTmpl:       template.Must(template.New("admin").Parse(adminHTML)),
		authTmpl:        template.Must(template.New("auth").Parse(authRedirectHTML)),
		clients:         map[*client]struct{}{},
		relayViewerRefs: map[string]int{},
	}
	s.setupRunner = commandSimulatorSetupRunner{
		adbPath: cfg.SimulatorSetup.ADBPath,
		target:  cfg.SimulatorSetup.ADBTarget,
		timeout: cfg.SimulatorSetup.Timeout,
	}
	relay.SetHandlers(s.handlePhoneMessage, s.handlePhoneDisconnect)
	go s.stateTicker()
	return s, nil
}

func (s *Server) Close() {
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "serverVersion": serverVersion})
	case path == "/api/v1/auth/config":
		s.handleAuthConfig(w, r)
	case path == "/api/v1/auth/session":
		s.handleAuthSession(w, r)
	case path == "/api/v1/auth/logout":
		s.handleAuthLogout(w, r)
	case path == "/api/v1/health":
		s.withMember(w, r, func(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, _ state.Snapshot) {
			s.handleHealth(w, r)
		})
	case strings.HasPrefix(path, "/static/"):
		writeNoStoreHeaders(w)
		http.StripPrefix("/static/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
	case path == "/api/v1/session":
		s.handleBrowserSocket(w, r, false)
	case path == "/api/v1/stream":
		s.handleBrowserSocket(w, r, true)
	case path == "/api/v1/me":
		s.withMember(w, r, s.handleMe)
	case path == "/api/v1/state":
		s.withMember(w, r, s.handleState)
	case path == "/api/v1/control/claim":
		s.withMember(w, r, s.handleClaimControl)
	case path == "/api/v1/control/extend":
		s.withMember(w, r, s.handleExtendControl)
	case path == "/api/v1/control/release":
		s.withMember(w, r, s.handleReleaseControl)
	case path == "/api/v1/client-log":
		s.withMember(w, r, s.handleClientLog)
	case path == "/api/v1/admin/state":
		s.withAdmin(w, r, s.handleAdminState)
	case path == "/api/v1/admin/members":
		s.withAdmin(w, r, s.handleAdminMembers)
	case path == "/api/v1/admin/control/revoke":
		s.withAdmin(w, r, s.handleAdminRevokeControl)
	case path == "/api/v1/admin/phone/backends":
		s.withAdmin(w, r, s.handleAdminPhoneBackends)
	case path == "/api/v1/admin/phone/backend":
		s.withAdmin(w, r, s.handleAdminPhoneBackend)
	case path == "/api/v1/admin/phone/setup/status":
		s.withOwnerSimulatorSetup(w, r, s.handleAdminPhoneSetupStatus)
	case path == "/api/v1/admin/phone/setup/screenshot":
		s.withOwnerSimulatorSetup(w, r, s.handleAdminPhoneSetupScreenshot)
	case path == "/api/v1/admin/phone/setup/input":
		s.withOwnerSimulatorSetup(w, r, s.handleAdminPhoneSetupInput)
	case path == "/api/v1/admin/phone/setup/open":
		s.withOwnerSimulatorSetup(w, r, s.handleAdminPhoneSetupOpen)
	case path == "/admin":
		s.handleAdminShell(w, r)
	case path == "/auth/callback":
		s.handleIndexShell(w, r)
	case path == "/":
		s.handleIndexShell(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleIndexShell(w http.ResponseWriter, r *http.Request) {
	if s.usesSpacetimeAuth() {
		id, sessionID, snapshot, ok := s.optionalMember(r)
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
	s.withMember(w, r, s.handleIndex)
}

func (s *Server) handleAdminShell(w http.ResponseWriter, r *http.Request) {
	if s.usesSpacetimeAuth() {
		id, sessionID, snapshot, ok := s.optionalMember(r)
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
	writeNoStoreHeaders(w)
	_ = s.authTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"ConfigJSON":   template.JS(mustJSON(s.publicBrowserConfig(auth.Identity{}, "", state.Snapshot{}, false))),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	phoneHealth := s.relay.Snapshot()
	snapshot, err := s.snapshotForHealth(r.Context(), now, phoneHealth)
	stateBackendFresh := err == nil
	ok := err == nil || snapshot.Ticket.ID != ""
	reasons := []string{}
	stateBackendWarning := ""
	if err != nil {
		if snapshot.Ticket.ID == "" {
			reasons = append(reasons, publicHealthError(err))
		} else {
			stateBackendWarning = publicHealthError(err)
		}
	}
	if snapshot.StateBackend == "memory" {
		reasons = append(reasons, "state backend is memory; configure SpacetimeDB for production")
	}
	streamNow := time.Now()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                  ok,
		"serverVersion":       serverVersion,
		"reasons":             reasons,
		"stateBackendFresh":   stateBackendFresh,
		"stateBackendWarning": stateBackendWarning,
		"state":               snapshot,
		"phone":               phoneHealth,
		"activePhoneBackend":  s.activePhoneBackend(),
		"directStream":        s.direct.snapshot(streamNow, phoneHealth),
		"controlGate":         s.controlGateSnapshot(streamNow),
		"quickClaim":          s.quickClaimSnapshot(),
	})
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
	writeNoStoreHeaders(w)
	_ = s.indexTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"ConfigJSON":   template.JS(mustJSON(s.publicBrowserConfig(id, sessionID, snapshot, true))),
		"IsAdmin":      snapshot.IsAdmin(id.Email),
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
	writeNoStoreHeaders(w)
	member, _ := snapshot.Member(id.Email)
	_ = s.adminTmpl.Execute(w, map[string]any{
		"AssetVersion": assetVersion(),
		"Email":        id.Email,
		"IsOwner":      member.Role == state.RoleOwner,
		"ConfigJSON":   template.JS(mustJSON(s.publicBrowserConfig(id, sessionID, snapshot, true))),
	})
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
		id, sessionID, snapshot, ok := s.optionalMember(r)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, apiResponse{OK: false, Error: "auth_required", Message: "SpacetimeAuth login is required."})
			return
		}
		if sessionID == "" {
			sessionID = s.sessionID(w, r)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"authenticated": true,
			"email":         id.Email,
			"sessionId":     sessionID,
			"state":         snapshot,
			"spacetime":     s.directSpacetimeSessionFromRequest(r),
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
		s.setAuthCookie(w, sessionToken, int(s.cfg.CookieTTL.Seconds()))
		sessionID := s.sessionID(w, r)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"authenticated": true,
			"email":         id.Email,
			"sessionId":     sessionID,
			"state":         snapshot,
			"session": map[string]any{
				"expiresAt": sessionExpiresAt.UTC().Format(time.RFC3339),
			},
			"spacetime": map[string]any{
				"host":     s.cfg.State.SpacetimeHost,
				"database": s.cfg.State.SpacetimeDatabase,
				"token":    token,
			},
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: s.relay.Snapshot()})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: s.relay.Snapshot()})
}

func (s *Server) handleClaimControl(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.store.ClaimControl(r.Context(), s.cfg.TicketID, sessionID, id.Email, time.Now())
	s.writeStateMutation(w, r, id.Email, "control_claim", snapshot, err)
}

func (s *Server) handleExtendControl(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.store.ExtendControl(r.Context(), s.cfg.TicketID, sessionID, id.Email, time.Now())
	s.writeStateMutation(w, r, id.Email, "control_extend", snapshot, err)
}

func (s *Server) handleReleaseControl(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.store.ReleaseControl(r.Context(), s.cfg.TicketID, sessionID, id.Email, "user_released", time.Now())
	s.writeStateMutation(w, r, id.Email, "control_release", snapshot, err)
}

func (s *Server) handleClientLog(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: "Client log was too large."})
		return
	}
	var payload struct {
		Event  string `json:"event"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		s.direct.recordClientTelemetry(payload.Event, payload.Detail)
	}
	log.Printf("ticket client log email=%s session=%s ua=%q body=%s", id.Email, sessionID, r.UserAgent(), strings.TrimSpace(string(body)))
	writeJSON(w, http.StatusOK, apiResponse{OK: true})
}

func (s *Server) handleAdminState(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, snapshot state.Snapshot) {
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: s.relay.Snapshot()})
}

func (s *Server) handleAdminMembers(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: err.Error()})
			return
		}
		snapshot, err := s.store.UpsertMember(r.Context(), s.cfg.TicketID, id.Email, req.Email, req.Role)
		s.writeStateMutation(w, r, id.Email, "member_upsert", snapshot, err)
	case http.MethodDelete:
		email := strings.TrimSpace(r.URL.Query().Get("email"))
		snapshot, err := s.store.RemoveMember(r.Context(), s.cfg.TicketID, id.Email, email)
		s.writeStateMutation(w, r, id.Email, "member_remove", snapshot, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAdminRevokeControl(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	snapshot, err := s.store.RevokeControl(r.Context(), s.cfg.TicketID, id.Email, "admin_revoked", time.Now())
	s.writeStateMutation(w, r, id.Email, "control_revoke", snapshot, err)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	hadControl := snapshot.ActiveControl != nil
	snapshot, err := s.store.RevokeControl(r.Context(), s.cfg.TicketID, id.Email, "phone_backend_switched", now)
	if err != nil {
		s.writeStateMutation(w, r, id.Email, "phone_backend_switch", snapshot, err)
		return
	}
	if err := config.WriteActivePhoneBackendID(s.cfg.Phone.ActiveBackendFile, backend.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{OK: false, Error: "persist_backend", Message: err.Error()})
		return
	}
	if hadControl {
		s.sendPhoneControlExit("phone_backend_switched")
		s.clearControlGate()
	}
	s.setActivePhoneBackend(backend)
	s.relay.SwitchBackend(phone.Backend{ID: backend.ID, AttachName: backend.AttachName, BaseURL: backend.BaseURL})
	relayHealth := s.relay.Snapshot()
	snapshot, err = s.store.UpdatePhone(r.Context(), state.PhoneInput{
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
		log.Printf("ticket backend switch phone state update failed: %v", err)
	}
	snapshot = s.withActivePhoneBackend(snapshot, relayHealth)
	if auditErr := s.store.Audit(r.Context(), s.cfg.TicketID, id.Email, "phone_backend_switched", map[string]any{
		"from": previous.ID,
		"to":   backend.ID,
	}, now); auditErr != nil {
		log.Printf("ticket backend switch audit failed: %v", auditErr)
	}
	s.cacheSnapshot(snapshot)
	s.rememberControlGate(snapshot, now)
	s.broadcastState()
	s.broadcastPhoneStatus("reconnecting", "Phone backend switched")
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"state":              snapshot,
		"phone":              relayHealth,
		"activePhoneBackend": backend,
	})
}

func (s *Server) handleBrowserSocket(w http.ResponseWriter, r *http.Request, video bool) {
	id, sessionID, snapshot, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c := &client{conn: conn, sessionID: sessionID, email: id.Email, page: "ticket", video: video}
	s.addClient(c)
	s.addRelayViewer(sessionID)
	if video {
		s.direct.addVideoClient()
	}
	if heartbeat, err := s.store.HeartbeatPresence(r.Context(), state.PresenceInput{
		TicketID:    s.cfg.TicketID,
		SessionID:   sessionID,
		Email:       id.Email,
		DisplayName: id.Email,
		Page:        "ticket",
		Connected:   true,
		Now:         time.Now(),
	}); err == nil {
		heartbeat = s.withActivePhoneBackend(heartbeat, s.relay.Snapshot())
		s.cacheSnapshot(heartbeat)
		s.rememberControlGate(heartbeat, time.Now())
		snapshot = heartbeat
	} else {
		log.Printf("ticket presence heartbeat failed for %s: %v", id.Email, err)
	}
	relaySnapshot := s.relay.Snapshot()
	snapshot = s.withActivePhoneBackend(snapshot, relaySnapshot)
	c.sendJSON(context.Background(), map[string]any{"type": "state", "state": snapshot, "phone": relaySnapshot, "serverVersion": serverVersion})
	if video {
		if configFrame, keyFrame := s.direct.warmStart(); len(configFrame) > 0 {
			c.sendText(context.Background(), configFrame)
			if len(keyFrame) > 0 {
				c.sendBinary(context.Background(), keyFrame)
			} else {
				s.requestPhoneKeyframe("browser_video_warm_start")
			}
		} else {
			s.requestPhoneKeyframe("browser_video_config_needed")
		}
	}
	statusCtx, stopStatus := context.WithCancel(r.Context())
	if video {
		go s.streamStatusLoop(statusCtx, c)
	}
	defer func() {
		stopStatus()
		s.removeClient(c)
		if video {
			s.direct.removeVideoClient()
		}
		s.removeRelayViewer(sessionID)
		if snapshot, err := s.store.DisconnectPresence(context.Background(), s.cfg.TicketID, sessionID, time.Now()); err == nil {
			snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
			s.cacheSnapshot(snapshot)
			s.rememberControlGate(snapshot, time.Now())
		} else {
			log.Printf("ticket presence disconnect failed for %s: %v", c.email, err)
		}
		s.broadcastState()
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
		if video {
			s.handleVideoStreamMessage(r.Context(), c, data)
			continue
		}
		s.handleClientMessage(r.Context(), c, data)
	}
}

func (s *Server) handleVideoStreamMessage(ctx context.Context, c *client, data []byte) {
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)
	switch msgType {
	case "keyframe":
		s.requestPhoneKeyframe("browser_h264_request")
	case "recover_stream":
		reason, _ := msg["reason"].(string)
		s.requestPhoneRecovery(reason)
	case "client_log":
		event, _ := msg["event"].(string)
		detail, _ := msg["detail"].(string)
		s.direct.recordClientTelemetry(event, detail)
	default:
		s.handleClientMessage(ctx, c, data)
	}
}

func (s *Server) streamStatusLoop(ctx context.Context, c *client) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.sendJSON(ctx, s.direct.streamStatus(now, s.relay.Snapshot()))
		}
	}
}

func (s *Server) handleClientMessage(ctx context.Context, c *client, data []byte) {
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	msgType, _ := msg["type"].(string)
	now := time.Now()
	inputID, _ := msg["inputId"].(string)
	switch msgType {
	case "heartbeat":
		relaySnapshot := s.relay.Snapshot()
		snapshot, err := s.store.HeartbeatPresence(ctx, state.PresenceInput{
			TicketID:    s.cfg.TicketID,
			SessionID:   c.sessionID,
			Email:       c.email,
			DisplayName: c.email,
			Page:        c.page,
			Connected:   true,
			Now:         now,
		})
		if err != nil {
			log.Printf("ticket presence heartbeat failed for %s: %v", c.email, err)
			if cached, ok := s.cachedSnapshot(now); ok {
				cached = s.withActivePhoneBackend(cached, relaySnapshot)
				c.sendJSON(ctx, map[string]any{"type": "state", "state": cached, "phone": relaySnapshot})
			}
			return
		}
		snapshot = s.withActivePhoneBackend(snapshot, relaySnapshot)
		s.cacheSnapshot(snapshot)
		s.rememberControlGate(snapshot, now)
		_ = s.relay.SendJSON(ctx, map[string]any{"type": "activity", "reason": "public_heartbeat"})
		c.sendJSON(ctx, map[string]any{"type": "state", "state": snapshot, "phone": relaySnapshot})
	case "state_refresh":
		snapshot, err := s.store.Snapshot(ctx, s.cfg.TicketID, now)
		if err != nil {
			if cached, ok := s.cachedSnapshot(now); ok {
				c.sendJSON(ctx, map[string]any{"type": "state", "state": cached, "phone": s.relay.Snapshot()})
			}
			return
		}
		snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
		s.cacheSnapshot(snapshot)
		s.rememberControlGate(snapshot, now)
		c.sendJSON(ctx, map[string]any{"type": "state", "state": snapshot, "phone": s.relay.Snapshot()})
	case "tap":
		active, allowed := s.activeControlGateAllows(c.sessionID, c.email, now)
		if !active || !allowed {
			go func() {
				_ = s.store.Audit(context.Background(), s.cfg.TicketID, c.email, "input_ignored", map[string]any{"reason": "not_active_controller"}, time.Now())
			}()
			sendTapInputResult(ctx, c, inputID, false, "not_active_controller")
			return
		}
		if err := s.relay.SendText(ctx, data); err != nil {
			go func() {
				_ = s.store.Audit(context.Background(), s.cfg.TicketID, c.email, "input_ignored", map[string]any{
					"reason": "phone_unavailable",
					"error":  err.Error(),
				}, time.Now())
			}()
			sendTapInputResult(ctx, c, inputID, false, "phone_unavailable")
			return
		}
		sendTapInputResult(ctx, c, inputID, true, "forwarded")
	case "quick_claim_tap":
		s.handleQuickClaimTap(ctx, c, msg, inputID, now)
	case "activity":
		_ = s.relay.SendText(ctx, data)
	case "keyframe":
		s.requestPhoneKeyframe("browser_request")
	case "swipe", "long_press", "longpress", "hold":
		_ = s.store.Audit(ctx, s.cfg.TicketID, c.email, "input_ignored", map[string]any{"reason": msgType}, now)
		c.sendJSON(ctx, map[string]any{"type": "input", "inputId": inputID, "accepted": false, "reason": "blocked_gesture"})
	default:
	}
}

func (s *Server) handlePhoneMessage(msg phone.Message) {
	if len(msg.Text) > 0 {
		s.handlePhoneText(msg.Text)
		s.broadcastText(msg.Text)
		return
	}
	if len(msg.Binary) > 0 {
		s.direct.recordFrame(msg.Binary)
		s.broadcastFrame(msg.Binary)
	}
}

func (s *Server) handlePhoneDisconnect(err error) {
	if err != nil {
		log.Printf("ticket phone disconnected: %v", err)
	}
	s.direct.recordPhoneReconnect()
	s.broadcastPhoneStatus("reconnecting", "Phone stream reconnecting")
}

func (s *Server) handleQuickClaimTap(ctx context.Context, c *client, msg map[string]any, inputID string, now time.Time) {
	if strings.TrimSpace(inputID) == "" {
		inputID = fmt.Sprintf("%s-%d", c.sessionID, now.UnixNano())
		msg["inputId"] = inputID
	}
	if strings.TrimSpace(fmt.Sprint(msg["snapTarget"])) != "control_code_button" {
		s.rejectQuickClaim(ctx, c, inputID, "remote_snap_target_unsupported", "unsupported_snap_target", now)
		return
	}
	if _, ok := msg["x"].(float64); !ok {
		s.rejectQuickClaim(ctx, c, inputID, "bad_request", "missing_x", now)
		return
	}
	if _, ok := msg["y"].(float64); !ok {
		s.rejectQuickClaim(ctx, c, inputID, "bad_request", "missing_y", now)
		return
	}

	active, allowed := s.activeControlGateAllows(c.sessionID, c.email, now)
	if active && !allowed {
		_ = s.store.Audit(context.Background(), s.cfg.TicketID, c.email, "quick_claim_ignored", map[string]any{"reason": "not_controller"}, now)
		s.rejectQuickClaim(ctx, c, inputID, "not_controller", "active_control_other_email", now)
		return
	}

	action := "active_same_email"
	if !active {
		snapshot, err := s.store.ClaimControl(ctx, s.cfg.TicketID, c.sessionID, c.email, now)
		snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
		if snapshot.Ticket.ID != "" {
			s.cacheSnapshot(snapshot)
			s.rememberControlGate(snapshot, now)
			s.broadcastSnapshot(snapshot)
		}
		if err != nil {
			if snapshot.IsController(c.sessionID, c.email, now) {
				action = "claimed_elsewhere_same_email"
			} else {
				reason := errorCode(err)
				if reason == "error" {
					reason = "control_claim_failed"
				}
				s.rejectQuickClaim(ctx, c, inputID, reason, "claim_failed", now)
				return
			}
		} else {
			action = "claimed"
		}
	}

	msg["type"] = "tap"
	body, err := json.Marshal(msg)
	if err != nil {
		s.rejectQuickClaim(ctx, c, inputID, "bad_request", "marshal_failed", now)
		return
	}
	if err := s.relay.SendText(ctx, body); err != nil {
		_ = s.store.Audit(context.Background(), s.cfg.TicketID, c.email, "quick_claim_ignored", map[string]any{"reason": "phone_unavailable", "error": err.Error()}, now)
		s.rejectQuickClaim(ctx, c, inputID, "phone_unavailable", "phone_unavailable", now)
		return
	}
	s.recordQuickClaim(quickClaimDiagnostic{
		At:        now.UTC().Format(time.RFC3339),
		SessionID: c.sessionID,
		Email:     c.email,
		InputID:   inputID,
		Action:    action,
		Reason:    "forwarded",
		Forwarded: true,
	})
	sendTapInputResult(ctx, c, inputID, true, "forwarded")
}

func (s *Server) rejectQuickClaim(ctx context.Context, c *client, inputID string, reason string, action string, now time.Time) {
	if reason == "" {
		reason = "quick_claim_rejected"
	}
	s.recordQuickClaim(quickClaimDiagnostic{
		At:        now.UTC().Format(time.RFC3339),
		SessionID: c.sessionID,
		Email:     c.email,
		InputID:   inputID,
		Action:    action,
		Reason:    reason,
		Forwarded: false,
	})
	sendTapInputResult(ctx, c, inputID, false, reason)
}

func sendTapInputResult(ctx context.Context, c *client, inputID string, accepted bool, reason string) {
	if reason == "" {
		if accepted {
			reason = "forwarded"
		} else {
			reason = "rejected"
		}
	}
	c.sendJSON(ctx, map[string]any{
		"type":     "input_result",
		"inputId":  inputID,
		"kind":     "tap",
		"accepted": accepted,
		"reason":   reason,
	})
}

func (s *Server) handlePhoneText(raw []byte) {
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	now := time.Now()
	if msgType, _ := msg["type"].(string); msgType == "config" {
		s.direct.setConfig(raw)
	} else if msgType == "health" {
		data, _ := msg["data"].(map[string]any)
		healthJSON := string(raw)
		backend := s.activePhoneBackend()
		if snapshot, err := s.store.UpdatePhone(context.Background(), state.PhoneInput{
			TicketID:     s.cfg.TicketID,
			BackendID:    backend.ID,
			AttachName:   backend.AttachName,
			BaseURL:      backend.BaseURL,
			DesiredState: "streaming",
			HealthJSON:   healthJSON,
			Now:          now,
		}); err == nil {
			snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
			s.cacheSnapshot(snapshot)
			s.rememberControlGate(snapshot, now)
		} else {
			log.Printf("ticket phone state update failed: %v", err)
		}
		streamActive, _ := data["streamActive"].(bool)
		inactivityActive, _ := data["inactivityActive"].(bool)
		s.maybeRequestPhoneStart(data, "phone_health")
		if !streamActive && !inactivityActive {
			snapshot, err := s.store.Snapshot(context.Background(), s.cfg.TicketID, now)
			if err == nil && snapshot.ActiveControl != nil {
				_, _ = s.store.ReleaseControl(context.Background(), s.cfg.TicketID, "", "", "phone_left_ticket", now)
				s.broadcastState()
			}
		}
	}
}

func (s *Server) writeStateMutation(w http.ResponseWriter, r *http.Request, actor string, event string, snapshot state.Snapshot, err error) {
	if err != nil {
		log.Printf("ticket state mutation %s failed for %s: %v", event, actor, err)
		status := http.StatusConflict
		if errors.Is(err, state.ErrForbidden) || errors.Is(err, state.ErrNotMember) {
			status = http.StatusForbidden
		}
		writeJSON(w, status, apiResponse{OK: false, Error: errorCode(err), Message: err.Error(), State: snapshot})
		return
	}
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	s.cacheSnapshot(snapshot)
	s.rememberControlGate(snapshot, time.Now())
	if err := s.store.Audit(r.Context(), s.cfg.TicketID, actor, event, nil, time.Now()); err != nil {
		log.Printf("ticket audit %s failed for %s: %v", event, actor, err)
	}
	s.broadcastState()
	writeJSON(w, http.StatusOK, apiResponse{OK: true, State: snapshot, Phone: s.relay.Snapshot()})
}

func (s *Server) withMember(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, auth.Identity, string, state.Snapshot)) {
	id, sessionID, snapshot, ok := s.identifyMember(w, r)
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

func (s *Server) withOwnerSimulatorSetup(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request, auth.Identity, string, state.Snapshot)) {
	id, sessionID, snapshot, ok := s.identifyMember(w, r)
	if !ok {
		return
	}
	member, ok := snapshot.Member(id.Email)
	if !ok || member.Role != state.RoleOwner {
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "owner_required", Message: "Owner access is required."})
		return
	}
	if s.activePhoneBackend().ID != s.cfg.SimulatorSetup.BackendID {
		writeJSON(w, http.StatusConflict, apiResponse{OK: false, Error: "inactive_backend", Message: "Simulator control is available only when the Android simulator backend is active."})
		return
	}
	next(w, r, id, sessionID, snapshot)
}

func (s *Server) identifyMember(w http.ResponseWriter, r *http.Request) (auth.Identity, string, state.Snapshot, bool) {
	id, err := s.auth.IdentityFromRequest(r.Context(), r)
	if err != nil {
		writeErrorPage(w, http.StatusUnauthorized, "SpacetimeAuth login is required.")
		return auth.Identity{}, "", state.Snapshot{}, false
	}
	sessionID := s.sessionID(w, r)
	now := time.Now()
	snapshot, err := s.snapshotWithCache(r.Context(), now, s.relay.Snapshot(), stateLookupTimeout)
	if err != nil {
		log.Printf("ticket state lookup failed for %s: %s", id.Email, publicHealthError(err))
		if snapshot.Ticket.ID == "" {
			writeErrorPage(w, http.StatusServiceUnavailable, "Ticket state is unavailable.")
			return auth.Identity{}, "", state.Snapshot{}, false
		}
	}
	s.rememberControlGate(snapshot, now)
	if _, ok := snapshot.Member(id.Email); !ok {
		writeErrorPage(w, http.StatusForbidden, fmt.Sprintf("The signed-in email %s is not linked to this ticket.", id.Email))
		return auth.Identity{}, "", snapshot, false
	}
	return id, sessionID, snapshot, true
}

func (s *Server) optionalMember(r *http.Request) (auth.Identity, string, state.Snapshot, bool) {
	id, err := s.auth.IdentityFromRequest(r.Context(), r)
	if err != nil {
		return auth.Identity{}, "", state.Snapshot{}, false
	}
	now := time.Now()
	snapshot, err := s.snapshotWithCache(r.Context(), now, s.relay.Snapshot(), stateLookupTimeout)
	if err != nil {
		if snapshot.Ticket.ID == "" {
			return auth.Identity{}, "", state.Snapshot{}, false
		}
	}
	s.rememberControlGate(snapshot, now)
	if _, ok := snapshot.Member(id.Email); !ok {
		return auth.Identity{}, "", snapshot, false
	}
	return id, s.sessionIDNoWrite(r), snapshot, true
}

func (s *Server) sessionID(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(s.cfg.CookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return cookie.Value
	}
	sessionID := randomID()
	http.SetCookie(w, &http.Cookie{
		Name:     s.cfg.CookieName,
		Value:    sessionID,
		Path:     "/",
		MaxAge:   int(s.cfg.CookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
	return sessionID
}

func (s *Server) sessionIDNoWrite(r *http.Request) string {
	if cookie, err := r.Cookie(s.cfg.CookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		return cookie.Value
	}
	return ""
}

func (s *Server) setAuthCookie(w http.ResponseWriter, token string, maxAge int) {
	cookieName := strings.TrimSpace(s.cfg.Access.AuthCookieName)
	if cookieName == "" {
		cookieName = "ticket_remote_auth"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   strings.HasPrefix(s.cfg.PublicBaseURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) authTokenFromRequest(r *http.Request) string {
	cookieName := strings.TrimSpace(s.cfg.Access.AuthCookieName)
	if cookieName == "" {
		cookieName = "ticket_remote_auth"
	}
	if cookie, err := r.Cookie(cookieName); err == nil {
		return strings.TrimSpace(cookie.Value)
	}
	return ""
}

func (s *Server) directSpacetimeSessionFromRequest(r *http.Request) map[string]any {
	out := map[string]any{
		"host":     s.cfg.State.SpacetimeHost,
		"database": s.cfg.State.SpacetimeDatabase,
	}
	token := s.authTokenFromRequest(r)
	if token != "" && !auth.IsServerSessionToken(token) {
		out["token"] = token
	}
	return out
}

func (s *Server) usesSpacetimeAuth() bool {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Access.Mode))
	return mode == "" || mode == "spacetime" || mode == "spacetimeauth" || mode == "oidc"
}

func (s *Server) publicAuthMode() string {
	mode := strings.ToLower(strings.TrimSpace(s.cfg.Access.Mode))
	switch mode {
	case "cloudflare", "cloudflare-access", "cf-access":
		return "cloudflare"
	case "dev", "development", "none":
		return mode
	default:
		return "spacetime"
	}
}

func (s *Server) addClient(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[c] = struct{}{}
}

func (s *Server) removeClient(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, c)
}

func (s *Server) addRelayViewer(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		s.relay.AddViewer()
		return
	}
	s.mu.Lock()
	previous := s.relayViewerRefs[sessionID]
	s.relayViewerRefs[sessionID] = previous + 1
	s.mu.Unlock()
	if previous == 0 {
		s.relay.AddViewer()
	}
}

func (s *Server) removeRelayViewer(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		s.relay.RemoveViewer()
		return
	}
	removeFromRelay := false
	s.mu.Lock()
	if count, ok := s.relayViewerRefs[sessionID]; !ok {
		removeFromRelay = false
	} else if count <= 1 {
		delete(s.relayViewerRefs, sessionID)
		removeFromRelay = true
	} else {
		s.relayViewerRefs[sessionID] = count - 1
	}
	s.mu.Unlock()
	if removeFromRelay {
		s.relay.RemoveViewer()
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
		if !c.video {
			continue
		}
		c.sendBinary(context.Background(), data)
	}
}

func (s *Server) broadcastState() {
	now := time.Now()
	snapshot, err := s.store.Snapshot(context.Background(), s.cfg.TicketID, now)
	if err != nil {
		log.Printf("ticket state broadcast failed: %v", err)
		if cached, ok := s.cachedSnapshot(now); ok {
			s.rememberControlGate(cached, now)
			payload, _ := json.Marshal(map[string]any{"type": "state", "state": cached, "phone": s.relay.Snapshot()})
			s.broadcastText(payload)
		}
		return
	}
	snapshot = s.withActivePhoneBackend(snapshot, s.relay.Snapshot())
	s.cacheSnapshot(snapshot)
	s.rememberControlGate(snapshot, now)
	payload, _ := json.Marshal(map[string]any{"type": "state", "state": snapshot, "phone": s.relay.Snapshot()})
	s.broadcastText(payload)
}

func (s *Server) broadcastSnapshot(snapshot state.Snapshot) {
	if snapshot.Ticket.ID == "" {
		return
	}
	payload, _ := json.Marshal(map[string]any{"type": "state", "state": snapshot, "phone": s.relay.Snapshot()})
	s.broadcastText(payload)
}

func (s *Server) broadcastCachedState(now time.Time) {
	snapshot, ok := s.cachedSnapshot(now)
	if !ok {
		s.broadcastState()
		return
	}
	s.rememberControlGate(snapshot, now)
	payload, _ := json.Marshal(map[string]any{"type": "state", "state": snapshot, "phone": s.relay.Snapshot()})
	s.broadcastText(payload)
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
	if snapshot.ActiveControl != nil {
		control := *snapshot.ActiveControl
		snapshot.ActiveControl = &control
	}
	adjustSnapshotTime(&snapshot, now)
	return snapshot, true
}

func (s *Server) recordQuickClaim(diag quickClaimDiagnostic) {
	s.quickClaimMu.Lock()
	s.lastQuickClaim = diag
	s.quickClaimMu.Unlock()
}

func (s *Server) quickClaimSnapshot() quickClaimDiagnostic {
	s.quickClaimMu.RLock()
	diag := s.lastQuickClaim
	s.quickClaimMu.RUnlock()
	return diag
}

func adjustSnapshotTime(snapshot *state.Snapshot, now time.Time) {
	snapshot.ServerTime = now.UTC().Format(time.RFC3339)
	if snapshot.ActiveControl == nil {
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, snapshot.ActiveControl.ExpiresAt)
	if err != nil || !now.Before(expiresAt) {
		snapshot.ActiveControl = nil
		return
	}
	snapshot.ActiveControl.RemainingMS = int64(expiresAt.Sub(now) / time.Millisecond)
}

func (s *Server) rememberControlGate(snapshot state.Snapshot, now time.Time) {
	s.gateMu.Lock()
	previous := s.gate
	var next *controlGate
	if snapshot.ActiveControl == nil {
		s.gate = nil
	} else if expiresAt, err := time.Parse(time.RFC3339, snapshot.ActiveControl.ExpiresAt); err == nil && now.Before(expiresAt) {
		next = &controlGate{
			sessionID: snapshot.ActiveControl.SessionID,
			email:     strings.ToLower(strings.TrimSpace(snapshot.ActiveControl.Email)),
			expiresAt: expiresAt,
		}
		s.gate = next
	} else {
		s.gate = nil
	}
	ended := previous != nil && next == nil
	s.gateMu.Unlock()
	if ended {
		s.notifyPhoneControlExit("control_session_ended")
	}
}

func (s *Server) clearControlGate() {
	s.gateMu.Lock()
	s.gate = nil
	s.gateMu.Unlock()
}

func (s *Server) notifyPhoneControlExit(reason string) {
	go s.sendPhoneControlExit(reason)
}

func (s *Server) sendPhoneControlExit(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "control_session_ended"
	}
	now := time.Now()
	_ = s.store.Audit(context.Background(), s.cfg.TicketID, "ticket_remote", "phone_control_exit_requested", map[string]any{
		"reason": reason,
	}, now)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.relay.SendControlExit(ctx, reason); err != nil {
		log.Printf("ticket phone control exit notify failed reason=%s: %v", reason, err)
		return
	}
	log.Printf("ticket phone control exit notified reason=%s", reason)
}

func (s *Server) controlGateAllows(sessionID string, email string, now time.Time) bool {
	s.gateMu.RLock()
	gate := s.gate
	s.gateMu.RUnlock()
	if gate == nil || !now.Before(gate.expiresAt) {
		return true
	}
	return strings.ToLower(strings.TrimSpace(gate.email)) == strings.ToLower(strings.TrimSpace(email))
}

func (s *Server) activeControlGateAllows(sessionID string, email string, now time.Time) (bool, bool) {
	s.gateMu.RLock()
	gate := s.gate
	s.gateMu.RUnlock()
	if gate == nil || !now.Before(gate.expiresAt) {
		return false, false
	}
	return true, strings.ToLower(strings.TrimSpace(gate.email)) == strings.ToLower(strings.TrimSpace(email))
}

func (s *Server) controlGateSnapshot(now time.Time) map[string]any {
	s.gateMu.RLock()
	gate := s.gate
	s.gateMu.RUnlock()
	if gate == nil || !now.Before(gate.expiresAt) {
		return map[string]any{"active": false}
	}
	return map[string]any{
		"active":    true,
		"sessionId": gate.sessionID,
		"email":     gate.email,
		"expiresAt": gate.expiresAt.UTC().Format(time.RFC3339),
	}
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

func (s *Server) activePhoneBackend() config.PhoneBackend {
	s.backendMu.RLock()
	defer s.backendMu.RUnlock()
	return config.PhoneBackend{
		ID:         s.cfg.Phone.BackendID,
		AttachName: s.cfg.Phone.AttachName,
		BaseURL:    s.cfg.Phone.BaseURL,
	}
}

func (s *Server) configuredPhoneBackends() []config.PhoneBackend {
	s.backendMu.RLock()
	defer s.backendMu.RUnlock()
	return append([]config.PhoneBackend(nil), s.cfg.Phone.Backends...)
}

func (s *Server) setActivePhoneBackend(backend config.PhoneBackend) {
	s.backendMu.Lock()
	defer s.backendMu.Unlock()
	s.cfg.Phone.BackendID = backend.ID
	s.cfg.Phone.AttachName = backend.AttachName
	s.cfg.Phone.BaseURL = strings.TrimRight(backend.BaseURL, "/")
}

func (s *Server) withActivePhoneBackend(snapshot state.Snapshot, health phone.Health) state.Snapshot {
	backend := s.activePhoneBackend()
	if backend.ID == "" {
		return snapshot
	}
	desiredState := health.StreamState
	if desiredState == "" {
		desiredState = "idle"
	}
	if snapshot.Phone != nil && snapshot.Phone.ID == backend.ID {
		phoneState := *snapshot.Phone
		if phoneState.AttachName == "" {
			phoneState.AttachName = backend.AttachName
		}
		if phoneState.BaseURL == "" {
			phoneState.BaseURL = backend.BaseURL
		}
		if phoneState.DesiredState == "" {
			phoneState.DesiredState = desiredState
		}
		if phoneState.LastError == "" {
			phoneState.LastError = health.LastError
		}
		if phoneState.LastSeenAt == "" {
			phoneState.LastSeenAt = health.LastSeenAt
		}
		snapshot.Phone = &phoneState
		return snapshot
	}
	snapshot.Phone = &state.PhoneBackend{
		ID:           backend.ID,
		AttachName:   backend.AttachName,
		BaseURL:      backend.BaseURL,
		DesiredState: desiredState,
		LastError:    health.LastError,
		LastSeenAt:   health.LastSeenAt,
	}
	return snapshot
}

func (s *Server) probePhoneBackend(ctx context.Context, backend config.PhoneBackend) (bool, int, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(backend.BaseURL), "/")
	if baseURL == "" {
		return false, 0, fmt.Errorf("base URL is empty")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, baseURL+"/api/v1/health", nil)
	if err != nil {
		return false, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, resp.StatusCode, fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return true, resp.StatusCode, nil
}

func (s *Server) broadcastPhoneStatus(stateText string, message string) {
	payload, _ := json.Marshal(map[string]any{"type": "phone", "state": stateText, "message": message, "phone": s.relay.Snapshot()})
	s.broadcastText(payload)
}

func (s *Server) maybeRequestPhoneStart(data map[string]any, reason string) {
	if data == nil {
		return
	}
	if streamActive, _ := data["streamActive"].(bool); streamActive {
		return
	}
	relayHealth := s.relay.Snapshot()
	if relayHealth.Viewers <= 0 || !relayHealth.Desired || !relayHealth.Connected {
		return
	}
	now := time.Now()
	s.phoneStartMu.Lock()
	if !s.lastPhoneStartAttempt.IsZero() && now.Sub(s.lastPhoneStartAttempt) < 10*time.Second {
		s.phoneStartMu.Unlock()
		return
	}
	s.lastPhoneStartAttempt = now
	s.phoneStartMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.relay.SendJSON(ctx, map[string]any{"type": "start", "reason": reason}); err != nil {
			log.Printf("ticket phone stream restart request failed: %v", err)
		}
	}()
}

func (s *Server) requestPhoneKeyframe(reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.relay.SendJSON(ctx, map[string]any{"type": "keyframe", "reason": reason}); err != nil {
			log.Printf("ticket phone keyframe request failed: %v", err)
		}
	}()
}

func (s *Server) requestPhoneRecovery(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "browser_stream_recovery"
	}
	s.requestPhoneKeyframe(reason)
	relayHealth := s.relay.Snapshot()
	if relayHealth.Viewers <= 0 || !relayHealth.Desired || !relayHealth.Connected {
		return
	}
	now := time.Now()
	s.phoneStartMu.Lock()
	if !s.lastPhoneStartAttempt.IsZero() && now.Sub(s.lastPhoneStartAttempt) < 10*time.Second {
		s.phoneStartMu.Unlock()
		return
	}
	s.lastPhoneStartAttempt = now
	s.phoneStartMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.relay.SendJSON(ctx, map[string]any{"type": "start", "reason": reason}); err != nil {
			log.Printf("ticket phone stream recovery request failed: %v", err)
		}
	}()
}

func (s *Server) maybeRequestPhoneStartFromSnapshot(snapshot state.Snapshot) {
	if snapshot.Phone == nil || strings.TrimSpace(snapshot.Phone.HealthJSON) == "" {
		return
	}
	var msg struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(snapshot.Phone.HealthJSON), &msg); err != nil || msg.Type != "health" {
		return
	}
	s.maybeRequestPhoneStart(msg.Data, "state_tick")
}

func (s *Server) stateTicker() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var lastRefresh time.Time
	for now := range ticker.C {
		if len(s.clientSnapshot()) > 0 {
			if lastRefresh.IsZero() || now.Sub(lastRefresh) >= 15*time.Second {
				s.broadcastState()
				if snapshot, ok := s.cachedSnapshot(now); ok {
					s.maybeRequestPhoneStartFromSnapshot(snapshot)
				}
				lastRefresh = now
				continue
			}
			if snapshot, ok := s.cachedSnapshot(now); ok {
				s.broadcastCachedState(now)
				s.maybeRequestPhoneStartFromSnapshot(snapshot)
			} else {
				s.broadcastState()
			}
		}
	}
}

func (c *client) sendJSON(ctx context.Context, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		return
	}
	c.sendText(ctx, body)
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

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	writeNoStoreHeaders(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeErrorPage(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeNoStoreHeaders(w)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><title>Ticket</title><body style=\"font-family:system-ui;margin:40px;background:#0b0f17;color:#eef3fb\"><h1>%d</h1><p>%s</p></body>", status, template.HTMLEscapeString(message))
}

func writeNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Surrogate-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
	w.Header().Set("Clear-Site-Data", "\"cache\"")
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
	if release := strings.TrimSpace(os.Getenv("ARBUZAS_RELEASE_ID")); release != "" {
		return release
	}
	return fmt.Sprintf("%d", time.Now().Unix())
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, state.ErrForbidden):
		return "forbidden"
	case errors.Is(err, state.ErrNotMember):
		return "not_member"
	case errors.Is(err, state.ErrControlClaimed):
		return "control_claimed"
	case errors.Is(err, state.ErrNoControl):
		return "no_control"
	case errors.Is(err, state.ErrNotController):
		return "not_controller"
	case errors.Is(err, state.ErrAlreadyExtended):
		return "already_extended"
	default:
		return "error"
	}
}

const indexHTML = `<!doctype html>
<html lang="lv">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover">
  <meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, max-age=0">
  <meta http-equiv="Pragma" content="no-cache">
  <meta http-equiv="Expires" content="0">
  <title>Biļete</title>
  <link rel="icon" href="data:,">
  <link rel="stylesheet" href="/static/app.css?v={{.AssetVersion}}">
  <script>
    window.TICKET_REMOTE_CONFIG = {{.ConfigJSON}};
  </script>
  <script defer src="/static/spacetime-client.js?v={{.AssetVersion}}"></script>
  <script defer src="/static/app.js?v={{.AssetVersion}}"></script>
</head>
<body>
  <main class="shell">
    <section class="stage-page" aria-label="Pixel straume">
      <div class="stage">
        <canvas id="screen" width="540" height="1080" aria-label="ViVi biļetes straume"></canvas>
        <img id="quickClaimSpinner" class="quick-claim-spinner" src="/static/quick-claim-spinner.svg?v={{.AssetVersion}}" alt="" aria-hidden="true" draggable="false" hidden>
        <img id="streamResumeSpinner" class="stream-resume-spinner" src="/static/quick-claim-spinner.svg?v={{.AssetVersion}}" alt="" aria-hidden="true" draggable="false" hidden>
        <div id="emptyState" class="empty-state">
          <div class="empty-inner">
            <button id="startStream" class="primary" type="button" hidden>Sākt</button>
            <div id="emptyMessage" class="empty-message" aria-live="polite"></div>
          </div>
        </div>
      </div>
    </section>
    <aside id="panel" class="panel" aria-label="Straumes vadīklas" aria-hidden="true">
      <div class="identity">
        <span id="connectionState">Savienojas</span>
        {{if .IsAdmin}}
        <a href="/admin" class="admin-link">Admin</a>
        {{end}}
      </div>
      <div class="panel-summary" aria-label="Biļetes stāvoklis">
        <section class="panel-summary-item control-summary">
          <span class="panel-label">Kontrole</span>
          <strong id="controlOwner">Brīva</strong>
          <span id="controlMode" class="panel-detail">Pieejama ikvienam lapā</span>
        </section>
        <section class="panel-summary-item">
          <span class="panel-label">Laiks</span>
          <strong id="timer" class="timer" hidden>45 s</strong>
          <span id="controlTimeDetail" class="panel-detail">Nav aktīvas kontroles</span>
        </section>
        <section class="panel-summary-item">
          <span class="panel-label">Lapā</span>
          <strong id="viewerCount">0</strong>
          <span id="viewerCountDetail" class="panel-detail">cilvēki lapā</span>
        </section>
        <section class="panel-summary-item stream-summary">
          <span class="panel-label">Straume</span>
          <strong id="streamStateLabel">Savienojas</strong>
          <span id="streamStateDetail" class="panel-detail">Gatavo biļetes attēlu</span>
        </section>
      </div>
      <div class="control-row">
        <button id="claimControl" class="primary" type="button">Kontroles kods</button>
        <button id="extendControl" type="button" hidden>Pagarināt</button>
        <button id="releaseControl" type="button" hidden>Beigt</button>
      </div>
      <div id="statusLine" class="status-line"></div>
      <div id="presence" class="presence"></div>
    </aside>
  </main>
</body>
</html>`

const authRedirectHTML = `<!doctype html>
<html lang="lv">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover">
  <meta http-equiv="Cache-Control" content="no-store, no-cache, must-revalidate, max-age=0">
  <meta http-equiv="Pragma" content="no-cache">
  <meta http-equiv="Expires" content="0">
  <title>Biļete</title>
  <link rel="icon" href="data:,">
  <link rel="stylesheet" href="/static/app.css?v={{.AssetVersion}}">
  <script>
    window.TICKET_REMOTE_CONFIG = {{.ConfigJSON}};
  </script>
  <script defer src="/static/app.js?v={{.AssetVersion}}"></script>
</head>
<body class="auth-redirect-page"></body>
</html>`

const adminHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Ticket Admin</title>
  <link rel="icon" href="data:,">
  <link rel="stylesheet" href="/static/app.css?v={{.AssetVersion}}">
  <script>
    window.TICKET_REMOTE_CONFIG = {{.ConfigJSON}};
  </script>
  <script defer src="/static/spacetime-client.js?v={{.AssetVersion}}"></script>
  <script defer src="/static/app.js?v={{.AssetVersion}}"></script>
</head>
<body class="admin-page">
  <main class="admin-shell" data-admin="true">
    <header class="admin-header">
      <div>
        <p class="admin-eyebrow">Ticket remote</p>
        <h1>Admin</h1>
      </div>
      <a href="/" class="admin-stream-link">Stream</a>
    </header>

    <section class="admin-status-grid" aria-label="Ticket status">
      <article class="admin-status-item">
        <span class="admin-label">Phone</span>
        <strong id="adminPhoneState">Loading</strong>
        <span id="adminPhoneDetail" class="admin-muted"></span>
      </article>
      <article class="admin-status-item">
        <span class="admin-label">Stream</span>
        <strong id="adminStreamState">Loading</strong>
        <span id="adminStreamDetail" class="admin-muted"></span>
      </article>
      <article class="admin-status-item">
        <span class="admin-label">Control</span>
        <strong id="adminControlState">Loading</strong>
        <span id="adminControlDetail" class="admin-muted"></span>
      </article>
      <article class="admin-status-item">
        <span class="admin-label">Safety</span>
        <strong id="adminSafetyState">Loading</strong>
        <span id="adminSafetyDetail" class="admin-muted"></span>
      </article>
    </section>

    <section class="admin-section admin-backend-section">
      <div class="admin-section-header">
        <div>
          <h2>Device backend</h2>
          <p id="adminBackendSummary" class="admin-muted">Loading device backends</p>
        </div>
      </div>
      <div id="adminBackendList" class="admin-backend-list"></div>
    </section>

    {{if .IsOwner}}
    <section class="admin-section admin-simulator-setup" data-simulator-setup="true">
      <div class="admin-section-header">
        <div>
          <h2>Owner simulator control</h2>
          <p id="simSetupSummary" class="admin-muted">Loading simulator control</p>
        </div>
        <button type="button" id="simSetupRefresh">Refresh</button>
      </div>
      <div id="simSetupPackages" class="admin-sim-packages"></div>
      <div class="admin-sim-actions" aria-label="Simulator controls">
        <button type="button" data-sim-open="home">Home</button>
        <button type="button" data-sim-key="back">Back</button>
        <button type="button" data-sim-key="enter">Enter</button>
        <button type="button" data-sim-key="app_switch">App switch</button>
        <button type="button" data-sim-key="wake">Wake</button>
        <button type="button" data-sim-key="delete">Delete</button>
        <button type="button" data-sim-key="space">Space</button>
        <button type="button" data-sim-open="accrescent">Accrescent</button>
        <button type="button" data-sim-open="aurora-vivi">Aurora ViVi</button>
        <button type="button" data-sim-open="controller">Controller</button>
      </div>
      <form id="simSetupTextForm" class="admin-sim-text">
        <input id="simSetupText" type="text" maxlength="256" autocomplete="off" placeholder="Text for simulator">
        <button type="submit">Type</button>
      </form>
      <p id="simSetupLastInput" class="admin-muted admin-sim-result">Waiting for input</p>
      <div class="admin-sim-screen">
        <img id="simSetupScreenshot" alt="Android simulator screen" tabindex="0" draggable="false">
      </div>
    </section>
    {{end}}

    <section class="admin-section admin-members-section">
      <div class="admin-section-header">
        <div>
          <h2>Members</h2>
          <p id="adminMemberSummary" class="admin-muted">Loading members</p>
        </div>
      </div>
      <form id="memberForm" class="member-form">
        <label>
          <span>Email</span>
          <input id="memberEmail" type="email" placeholder="email@example.com" autocomplete="email" required>
        </label>
        <label>
          <span>Role</span>
          <select id="memberRole">
            <option value="member">member</option>
            <option value="admin">admin</option>
            <option value="owner">owner</option>
          </select>
        </label>
        <button class="primary" type="submit">Add</button>
      </form>
      <div id="adminNotice" class="admin-notice" role="status" aria-live="polite" hidden></div>
      <div id="adminMembers" class="admin-list"></div>
    </section>

    <section class="admin-section admin-control-section">
      <div class="admin-section-header">
        <div>
          <h2>Session</h2>
          <p id="adminSessionSummary" class="admin-muted">Loading session state</p>
        </div>
      </div>
      <button id="adminRevoke" type="button">Revoke control</button>
    </section>

    <details class="admin-section admin-raw">
      <summary>Raw state</summary>
      <pre id="adminState" class="admin-state"></pre>
    </details>
  </main>
</body>
</html>`
