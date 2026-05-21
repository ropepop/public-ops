package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"pixelops/shared/telegramweb"
	"satiksmebot/internal/bot"
	"satiksmebot/internal/config"
	"satiksmebot/internal/live"
	"satiksmebot/internal/model"
	"satiksmebot/internal/reports"
	"satiksmebot/internal/runtime"
	"satiksmebot/internal/store"
	"satiksmebot/internal/version"
)

//go:embed static/*
var staticFS embed.FS

const maxJSONBodyBytes = 64 * 1024

type CatalogReader interface {
	Current() *model.Catalog
	Status() runtime.CatalogStatus
}

type catalogStopFinder interface {
	FindStop(stopID string) (model.Stop, bool)
}

type catalogPayloadReader interface {
	CatalogJSON() []byte
	CatalogETag() string
}

type liveViewerStateStore interface {
	SetLiveViewerState(ctx context.Context, sessionID string, page string, visible bool) error
}

type Server struct {
	cfg              config.Config
	reports          *reports.Service
	catalog          CatalogReader
	store            store.Store
	dump             *bot.DumpDispatcher
	runtimeState     *runtime.State
	release          releaseInfo
	loc              *time.Location
	liveHTTPClient   *http.Client
	pathPrefix       string
	sessionSecret    []byte
	spacetime        *spacetimeTokenIssuer
	telegramLogin    *telegramweb.LoginVerifier
	authConfigRate   *clientRateLimiter
	authCompleteRate *clientRateLimiter
	static           fs.FS
	pageTemplate     *template.Template
	bundleStore      *staticBundleStore
}

type pageData struct {
	AppCSSURL       string
	AppJSURL        string
	LeafletCSSURL   string
	LeafletJSURL    string
	LiveClientJSURL string
	ConfigJS        template.JS
	ScriptNonce     string
}

func NewServer(
	cfg config.Config,
	catalog CatalogReader,
	reportsSvc *reports.Service,
	dump *bot.DumpDispatcher,
	st store.Store,
	runtimeState *runtime.State,
	loc *time.Location,
) (*Server, error) {
	pathPrefix := ""
	if strings.TrimSpace(cfg.SatiksmeWebPublicBaseURL) != "" {
		parsed, err := url.Parse(cfg.SatiksmeWebPublicBaseURL)
		if err != nil {
			return nil, fmt.Errorf("parse SATIKSME_WEB_PUBLIC_BASE_URL: %w", err)
		}
		if strings.TrimSpace(parsed.Path) != "" && parsed.Path != "/" {
			pathPrefix = strings.TrimRight(parsed.Path, "/")
		}
	}

	static := mustStaticSubFS()
	release, err := newReleaseInfo(static)
	if err != nil {
		return nil, err
	}

	server := &Server{
		cfg:              cfg,
		reports:          reportsSvc,
		catalog:          catalog,
		store:            st,
		dump:             dump,
		runtimeState:     runtimeState,
		release:          release,
		loc:              loc,
		liveHTTPClient:   &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second},
		pathPrefix:       pathPrefix,
		authConfigRate:   newClientRateLimiter(authConfigRequestsPerMinute, authRateLimitWindow),
		authCompleteRate: newClientRateLimiter(authCompleteRequestsPerMinute, authRateLimitWindow),
		static:           static,
		pageTemplate: template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="lv">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="robots" content="noindex, noarchive">
  <title>Kontrole</title>
  <link rel="stylesheet" href="{{.AppCSSURL}}">
  {{if .LeafletCSSURL}}<link rel="stylesheet" href="{{.LeafletCSSURL}}">{{end}}
  <script data-cfasync="false" nonce="{{.ScriptNonce}}">window.SATIKSME_APP_CONFIG = {{.ConfigJS}};</script>
  {{if .LeafletJSURL}}<script data-cfasync="false" defer src="{{.LeafletJSURL}}"></script>{{end}}
  {{if .LiveClientJSURL}}<script data-cfasync="false" defer src="{{.LiveClientJSURL}}"></script>{{end}}
  <script data-cfasync="false" defer src="{{.AppJSURL}}"></script>
</head>
<body>
  <div id="app"></div>
</body>
</html>`)),
	}
	if cfg.SatiksmeWebEnabled {
		secret, secretErr := loadSessionSecret(cfg.SatiksmeWebSessionSecretFile)
		if secretErr != nil {
			return nil, secretErr
		}
		server.sessionSecret = secret
		if strings.TrimSpace(server.telegramBotID()) == "" {
			return nil, errors.New("SATIKSME_WEB_TELEGRAM_CLIENT_ID must not be empty")
		}
		verifier, verifierErr := telegramweb.NewLoginVerifier(telegramweb.LoginVerifierConfig{
			ClientID:    server.telegramBotID(),
			AllowedSkew: 30 * time.Second,
		})
		if verifierErr != nil {
			return nil, verifierErr
		}
		server.telegramLogin = verifier
		if cfg.SatiksmeWebSpacetimeEnabled {
			issuer, issuerErr := newSpacetimeTokenIssuer(cfg)
			if issuerErr != nil {
				return nil, issuerErr
			}
			server.spacetime = issuer
		}
	}
	if strings.TrimSpace(cfg.SatiksmeWebBundleDir) != "" {
		server.bundleStore = newStaticBundleStore(cfg.SatiksmeWebBundleDir)
	}
	return server, nil
}

func mustStaticSubFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return productionStaticFS{sub: sub}
}

type productionStaticFS struct {
	sub fs.FS
}

func (p productionStaticFS) Open(name string) (fs.File, error) {
	clean := cleanStaticAssetName(name)
	if clean != "app.js" {
		return p.sub.Open(name)
	}
	body, err := fs.ReadFile(p.sub, name)
	if err != nil {
		return nil, err
	}
	body, err = stripSatiksmeAppTestHarness(body)
	if err != nil {
		return nil, err
	}
	return &memoryStaticFile{
		Reader: bytes.NewReader(body),
		name:   path.Base(clean),
		size:   int64(len(body)),
	}, nil
}

func (p productionStaticFS) ReadFile(name string) ([]byte, error) {
	body, err := fs.ReadFile(p.sub, name)
	if err != nil {
		return nil, err
	}
	if cleanStaticAssetName(name) == "app.js" {
		return stripSatiksmeAppTestHarness(body)
	}
	return body, nil
}

func cleanStaticAssetName(name string) string {
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}

func stripSatiksmeAppTestHarness(body []byte) ([]byte, error) {
	source := string(body)
	var err error
	source, err = stripNamedJSFunction(source, "resetStateForTest")
	if err != nil {
		return nil, err
	}
	start := strings.Index(source, "\n  var exported = {};\n  if (typeof module === \"object\" && module.exports) {")
	end := strings.LastIndex(source, "\n  return exported;\n});")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("satiksme app test harness markers not found")
	}
	next := source[:start] + "\n  return {};\n});\n"
	return []byte(next), nil
}

func stripNamedJSFunction(source string, name string) (string, error) {
	start := strings.Index(source, "\n  function "+name+"(")
	if start < 0 {
		return "", fmt.Errorf("function %s marker not found", name)
	}
	openOffset := strings.Index(source[start:], "{")
	if openOffset < 0 {
		return "", fmt.Errorf("function %s opening brace not found", name)
	}
	depth := 0
	for index := start + openOffset; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end := index + 1
				if end < len(source) && source[end] == '\n' {
					end++
				}
				return source[:start] + source[end:], nil
			}
		}
	}
	return "", fmt.Errorf("function %s closing brace not found", name)
}

type memoryStaticFile struct {
	*bytes.Reader
	name string
	size int64
}

func (f *memoryStaticFile) Stat() (fs.FileInfo, error) {
	return memoryStaticFileInfo{name: f.name, size: f.size}, nil
}

func (f *memoryStaticFile) Close() error {
	return nil
}

type memoryStaticFileInfo struct {
	name string
	size int64
}

func (i memoryStaticFileInfo) Name() string       { return i.name }
func (i memoryStaticFileInfo) Size() int64        { return i.size }
func (i memoryStaticFileInfo) Mode() fs.FileMode  { return 0o444 }
func (i memoryStaticFileInfo) ModTime() time.Time { return time.Time{} }
func (i memoryStaticFileInfo) IsDir() bool        { return false }
func (i memoryStaticFileInfo) Sys() any           { return nil }

func (s *Server) AppURL() string {
	if !s.cfg.SatiksmeWebEnabled {
		return ""
	}
	return strings.TrimRight(s.cfg.SatiksmeWebPublicBaseURL, "/")
}

func (s *Server) PublicURL() string {
	return strings.TrimRight(s.cfg.SatiksmeWebPublicBaseURL, "/")
}

func (s *Server) Run(ctx context.Context) error {
	if !s.cfg.SatiksmeWebEnabled {
		return nil
	}
	httpServer := &http.Server{
		Addr:              net.JoinHostPort(s.cfg.SatiksmeWebBindAddr, strconv.Itoa(s.cfg.SatiksmeWebPort)),
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen satiksme web server: %w", err)
	}
	if s.runtimeState != nil {
		s.runtimeState.SetWebListening(true)
		defer s.runtimeState.SetWebListening(false)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) || err == nil {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.setSecurityHeaders(w)
	if unsafePublicPath(r) {
		writeError(w, http.StatusBadRequest, "bad path")
		return
	}
	basePath := strings.TrimRight(s.pathPrefix, "/")
	if trailingSlashAlias(r.URL.Path, basePath) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case path == strings.TrimRight(basePath+"/robots.txt", "/"):
		s.serveRobotsTxt(w, r)
	case path == strings.TrimRight(basePath+"/oidc/.well-known/openid-configuration", "/"):
		if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return
		}
		s.handleSpacetimeOpenIDConfiguration(w, r)
	case path == strings.TrimRight(basePath+"/oidc/jwks.json", "/"):
		if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return
		}
		s.handleSpacetimeJWKS(w, r)
	case path == basePath || path == "":
		s.serveShellPage(w, r, "public")
	case path == basePath+"/incidents":
		s.serveShellPage(w, r, "public-incidents")
	case path == basePath+"/-incidents":
		s.serveShellPage(w, r, "public-incidents")
	case path == basePath+"/app":
		s.serveShellPage(w, r, "public")
	case path == basePath+"/bundles/active.json":
		s.serveBundleActive(w, r)
	case strings.HasPrefix(path, basePath+"/bundles/"):
		s.serveBundleAsset(w, r, basePath)
	case path == basePath+"/transport/live/active.json":
		s.serveLiveSnapshotActive(w, r)
	case strings.HasPrefix(path, basePath+"/transport/live/"):
		s.serveLiveSnapshotAsset(w, r, basePath)
	case strings.HasPrefix(path, basePath+"/assets/"):
		s.serveAsset(w, r, basePath)
	case strings.HasPrefix(path, basePath+"/api/v1/"):
		s.handleAPI(w, r, strings.TrimPrefix(path, basePath+"/api/v1"))
	default:
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
	}
}

func trailingSlashAlias(requestPath string, basePath string) bool {
	if requestPath == "/" {
		return false
	}
	if basePath != "" && requestPath == basePath+"/" {
		return false
	}
	return strings.HasSuffix(requestPath, "/")
}

func (s *Server) serveRobotsTxt(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
}

func (s *Server) serveShell(w http.ResponseWriter, mode string) {
	s.setNoStoreHeaders(w)
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	s.setNoIndexHeaders(w)
	scriptNonce, err := newCSPNonce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare page")
		return
	}
	s.setShellSecurityHeaders(w, scriptNonce)
	basePath := strings.TrimRight(s.pathPrefix, "/")
	bundleActiveURL := basePath + "/bundles/active.json"
	if s.bundleStore == nil {
		bundleActiveURL = ""
	}
	browserDirectDataEnabled := s.browserDirectDataEnabled()
	liveSnapshotLookupEnabled := s.browserLiveSnapshotLookupEnabled()
	exposeSpacetimeConfig := browserDirectDataEnabled
	cfg := map[string]any{
		"basePath":          basePath,
		"publicBaseURL":     s.cfg.SatiksmeWebPublicBaseURL,
		"language":          defaultAppLanguage,
		"mode":              mode,
		"reportsChannelURL": s.cfg.ReportsChannelURL,
		"bundleActiveURL":   bundleActiveURL,
		"liveVehiclesURL":   basePath + "/api/v1/public/live-vehicles",
	}
	if exposeSpacetimeConfig {
		cfg["spacetimeEnabled"] = browserDirectDataEnabled
		cfg["spacetimeDirectOnly"] = s.cfg.SatiksmeWebSpacetimeDirectOnly
		cfg["spacetimeHost"] = s.cfg.SatiksmeWebSpacetimeHost
		cfg["spacetimeDatabase"] = s.cfg.SatiksmeWebSpacetimeDatabase
		cfg["liveTransportRealtimeEnabled"] = browserDirectDataEnabled
	}
	if liveSnapshotLookupEnabled {
		cfg["liveTransportSnapshotLookupEnabled"] = liveSnapshotLookupEnabled
	}
	if s.browserLiveViewerHeartbeatEnabled() {
		cfg["liveTransportViewerHeartbeatEnabled"] = true
		cfg["liveTransportViewerHeartbeatURL"] = basePath + "/api/v1/public/live-viewer"
	}
	raw, _ := json.Marshal(cfg)
	liveClientURL := ""
	if exposeSpacetimeConfig && s.release.LiveClientHash != "" {
		liveClientURL = s.release.AssetURL(basePath, "live-client.js")
	}
	leafletCSSURL := ""
	leafletJSURL := ""
	if mode != "public-incidents" {
		leafletCSSURL = s.release.AssetURL(basePath, "leaflet/leaflet.css")
		leafletJSURL = s.release.AssetURL(basePath, "leaflet/leaflet.js")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.pageTemplate.Execute(w, pageData{
		AppCSSURL:       s.release.AssetURL(basePath, "app.css"),
		AppJSURL:        s.release.AssetURL(basePath, "app.js"),
		LeafletCSSURL:   leafletCSSURL,
		LeafletJSURL:    leafletJSURL,
		LiveClientJSURL: liveClientURL,
		ConfigJS:        template.JS(raw),
		ScriptNonce:     scriptNonce,
	})
}

func (s *Server) serveShellPage(w http.ResponseWriter, r *http.Request, mode string) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	s.serveShell(w, mode)
}

func (s *Server) browserSpacetimeConfigured() bool {
	return s.cfg.SatiksmeWebSpacetimeEnabled &&
		strings.TrimSpace(s.cfg.SatiksmeWebSpacetimeHost) != "" &&
		strings.TrimSpace(s.cfg.SatiksmeWebSpacetimeDatabase) != ""
}

func (s *Server) browserDirectDataEnabled() bool {
	return s.browserSpacetimeConfigured() && s.cfg.SatiksmeWebSpacetimeDirectOnly
}

func (s *Server) browserLiveSnapshotLookupEnabled() bool {
	return s.browserLiveSnapshotFileEnabled() ||
		(s.browserDirectDataEnabled() && s.release.LiveClientHash != "")
}

func (s *Server) browserLiveSnapshotFileEnabled() bool {
	return strings.TrimSpace(s.cfg.SatiksmeWebLiveSnapshotDir) != ""
}

func (s *Server) browserLiveViewerHeartbeatEnabled() bool {
	if !s.cfg.SatiksmeWebLiveViewerHeartbeatEnabled {
		return false
	}
	_, ok := s.store.(liveViewerStateStore)
	return ok
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	assetPath := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/assets/")
	if excludedGenericPublicAsset(assetPath) || s.excludedPublicAsset(assetPath) || !s.allowAssetQuery(r, assetPath) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setNoIndexHeaders(w)
	version := strings.TrimSpace(r.URL.Query().Get("v"))
	if version != "" && version == s.release.AssetHash(assetPath) {
		s.setImmutableHeaders(w)
	} else {
		s.setNoStoreHeaders(w)
		r = requestWithoutRange(r)
	}
	w.Header().Set("Vary", "Accept-Encoding")
	http.StripPrefix(basePath+"/assets/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
}

func (s *Server) excludedPublicAsset(assetPath string) bool {
	cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+assetPath)), "/")
	return cleanPath == "live-client.js" && !s.browserDirectDataEnabled()
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, route string) {
	now := time.Now().In(s.loc)
	if !s.allowUnsafeAPIRequest(w, r) {
		return
	}
	switch {
	case route == "/health":
		s.handleHealth(w, r)
	case route == "/livez":
		s.handleLivez(w, r)
	case route == "/internal/health":
		s.handleInternalHealth(w, r)
	case route == "/public/incidents":
		s.handlePublicIncidents(w, r, now)
	case route == "/public/catalog":
		s.handlePublicCatalog(w, r)
	case strings.HasPrefix(route, "/public/incidents/"):
		s.handlePublicIncidentDetail(w, r, strings.Trim(strings.TrimPrefix(route, "/public/incidents/"), "/"), now)
	case route == "/public/sightings":
		s.handlePublicSightings(w, r, now)
	case route == "/public/map":
		s.handlePublicMap(w, r, now)
	case route == "/public/map-live":
		s.handlePublicMapLive(w, r, now)
	case route == "/public/live-vehicles":
		s.handlePublicLiveVehicles(w, r, now)
	case route == "/public/live-viewer":
		s.handlePublicLiveViewer(w, r)
	case route == "/auth/telegram/start":
		s.handleDeprecatedAuthTelegramStart(w, r)
	case route == "/auth/telegram/callback":
		s.handleDeprecatedAuthTelegramCallback(w, r)
	case route == "/auth/telegram/complete":
		s.handleAuthTelegramComplete(w, r, now)
	case route == "/auth/telegram/config":
		s.handleAuthTelegramConfig(w, r, now)
	case route == "/auth/telegram":
		s.handleDeprecatedAuthTelegram(w, r)
	case route == "/auth/logout":
		s.handleAuthLogout(w, r, now)
	case route == "/me":
		if !allowMethods(w, r, http.MethodGet) {
			return
		}
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.setNoStoreHeaders(w)
		payload, err := s.authPayloadFromClaims(claims, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, payload)
	case route == "/reports/stop":
		s.handleStopReport(w, r, now)
	case route == "/reports/recent":
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleRecentReports(w, r, claims, now)
	case route == "/reports/vehicle":
		s.handleVehicleReport(w, r, now)
	case route == "/reports/area":
		s.handleAreaReport(w, r, now)
	case strings.HasPrefix(route, "/incidents/") && strings.HasSuffix(route, "/votes"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		incidentID := strings.TrimSuffix(strings.TrimPrefix(route, "/incidents/"), "/votes")
		incidentID = strings.Trim(incidentID, "/")
		s.handleIncidentVote(w, r, claims, incidentID, now)
	case strings.HasPrefix(route, "/incidents/") && strings.HasSuffix(route, "/comments"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		incidentID := strings.TrimSuffix(strings.TrimPrefix(route, "/incidents/"), "/comments")
		incidentID = strings.Trim(incidentID, "/")
		s.handleIncidentComment(w, r, claims, incidentID, now)
	default:
		s.setNoStoreHeaders(w)
		http.NotFound(w, r)
	}
}

func (s *Server) allowUnsafeAPIRequest(w http.ResponseWriter, r *http.Request) bool {
	if !unsafeHTTPMethod(r.Method) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "cross-site", "same-site":
		writeError(w, http.StatusForbidden, "cross-site request forbidden")
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !s.sameOriginRequest(r, origin) {
		writeError(w, http.StatusForbidden, "cross-site request forbidden")
		return false
	}
	if strings.TrimSpace(r.Header.Get("Origin")) == "" {
		if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" && !s.sameOriginRequest(r, referer) {
			writeError(w, http.StatusForbidden, "cross-site request forbidden")
			return false
		}
	}
	return true
}

func unsafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) sameOriginRequest(r *http.Request, raw string) bool {
	origin := requestOrigin(raw)
	if origin == "" {
		return false
	}
	for _, allowed := range []string{
		requestOrigin(s.PublicURL()),
		requestHostOrigin(r),
	} {
		if allowed != "" && origin == allowed {
			return true
		}
	}
	return false
}

func requestOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
}

func requestHostOrigin(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		first := strings.ToLower(strings.TrimSpace(strings.Split(forwarded, ",")[0]))
		if first == "http" || first == "https" {
			scheme = first
		}
	}
	return scheme + "://" + strings.ToLower(host)
}

type healthSnapshot struct {
	ok      bool
	reasons []string
	payload map[string]any
}

type healthSnapshotOptions struct {
	checkStore bool
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	now := time.Now().UTC()
	snapshot := s.healthSnapshot(r.Context(), now, healthSnapshotOptions{})
	status := http.StatusOK
	if !snapshot.ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"ok":       snapshot.ok,
		"degraded": len(snapshot.reasons) > 0,
	})
}

func (s *Server) handleLivez(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleInternalHealth(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	if !isLocalRequest(r) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snapshot := s.healthSnapshot(r.Context(), time.Now().UTC(), healthSnapshotOptions{checkStore: true})
	status := http.StatusOK
	if !snapshot.ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, snapshot.payload)
}

func (s *Server) healthSnapshot(ctx context.Context, now time.Time, options healthSnapshotOptions) healthSnapshot {
	catalogStatus := runtime.CatalogStatus{}
	if s.catalog != nil {
		catalogStatus = s.catalog.Status()
	}
	stale := s.catalogStale(catalogStatus, now)
	dbWritable, dbError := true, ""
	if options.checkStore {
		dbWritable, dbError = s.dbStatus(ctx)
	}
	var telegramStatus runtime.TelegramStatus
	var dumpStatus runtime.DumpStatus
	startedAt := time.Time{}
	lastFatalError := ""
	webEnabled := s.cfg.SatiksmeWebEnabled
	webListening := false
	webBindAddr := net.JoinHostPort(s.cfg.SatiksmeWebBindAddr, strconv.Itoa(s.cfg.SatiksmeWebPort))
	if s.runtimeState != nil {
		telegramStatus = s.runtimeState.TelegramStatus()
		dumpStatus = s.runtimeState.DumpStatus()
		startedAt = s.runtimeState.StartedAt()
		lastFatalError = s.runtimeState.LastFatalError()
		webEnabled, webListening, webBindAddr = s.runtimeState.WebStatus()
	}

	reasons := make([]string, 0, 6)
	ok := true
	if !catalogStatus.Loaded {
		ok = false
		reasons = append(reasons, "catalog_unavailable")
	}
	if options.checkStore && !dbWritable {
		ok = false
		reasons = append(reasons, "db_unwritable")
	}
	if webEnabled && !webListening {
		ok = false
		reasons = append(reasons, "web_not_listening")
	}
	if strings.TrimSpace(lastFatalError) != "" {
		ok = false
		reasons = append(reasons, "fatal_error")
	}
	if strings.TrimSpace(catalogStatus.LastRefreshError) != "" {
		reasons = append(reasons, "catalog_refresh_error")
	}
	if catalogStatus.LoadedFromFallback {
		reasons = append(reasons, "catalog_fallback")
	}
	if stale {
		reasons = append(reasons, "catalog_stale")
	}
	if telegramStatus.ConsecutiveErrors > 0 {
		reasons = append(reasons, "telegram_errors")
	}
	if dumpStatus.LastError != "" {
		reasons = append(reasons, "report_dump_error")
	}
	if dumpStatus.Pending > 0 {
		reasons = append(reasons, "report_dump_pending")
	}

	uptimeSeconds := int64(0)
	if !startedAt.IsZero() {
		uptimeSeconds = int64(now.Sub(startedAt).Seconds())
	}
	bundlePayload := any(nil)
	if s.bundleStore != nil {
		if metadata, err := s.bundleStore.bundleMetadata(); err == nil && metadata != nil {
			bundlePayload = metadata
		}
	}
	liveSnapshotPayload := any(nil)
	if strings.TrimSpace(s.cfg.SatiksmeWebLiveSnapshotDir) != "" {
		if metadata, err := live.ReadSnapshotActiveState(s.cfg.SatiksmeWebLiveSnapshotDir); err == nil && metadata != nil {
			liveSnapshotPayload = metadata
		}
	}

	payload := map[string]any{
		"ok":       ok,
		"degraded": len(reasons) > 0,
		"reasons":  reasons,
		"version": map[string]any{
			"display":      version.Display(),
			"commit":       s.release.Commit,
			"buildTime":    s.release.BuildTime,
			"dirty":        s.release.Dirty,
			"releaseId":    s.release.ReleaseID,
			"sourceSha256": s.release.SourceSHA256,
		},
		"runtime": map[string]any{
			"instanceId":     s.release.Instance,
			"startedAt":      optionalTimeValue(startedAt),
			"uptimeSeconds":  uptimeSeconds,
			"lastFatalError": emptyToNil(lastFatalError),
		},
		"assets": map[string]any{
			"appJsSha256":      s.release.AppJSHash,
			"appCssSha256":     s.release.AppCSSHash,
			"liveClientSha256": s.release.LiveClientHash,
		},
		"catalog": map[string]any{
			"loaded":             catalogStatus.Loaded,
			"loadedFromFallback": catalogStatus.LoadedFromFallback,
			"generatedAt":        optionalTimeValue(catalogStatus.GeneratedAt),
			"lastRefreshAttempt": optionalTimeValue(catalogStatus.LastRefreshAttempt),
			"lastRefreshSuccess": optionalTimeValue(catalogStatus.LastRefreshSuccess),
			"lastRefreshError":   emptyToNil(catalogStatus.LastRefreshError),
			"stopCount":          catalogStatus.StopCount,
			"routeCount":         catalogStatus.RouteCount,
			"stale":              stale,
			"ageSeconds":         optionalAgeSeconds(catalogStatus.GeneratedAt, now),
		},
		"telegram": map[string]any{
			"lastSuccessAt":     optionalTimeValue(telegramStatus.LastSuccessAt),
			"lastErrorAt":       optionalTimeValue(telegramStatus.LastErrorAt),
			"consecutiveErrors": telegramStatus.ConsecutiveErrors,
			"lastError":         emptyToNil(telegramStatus.LastError),
			"lastUpdateId":      telegramStatus.LastUpdateID,
		},
		"reportDump": map[string]any{
			"pending":       dumpStatus.Pending,
			"lastSuccessAt": optionalTimeValue(dumpStatus.LastSuccessAt),
			"lastAttemptAt": optionalTimeValue(dumpStatus.LastAttemptAt),
			"lastError":     emptyToNil(dumpStatus.LastError),
		},
		"db": map[string]any{
			"writable": dbWritable,
			"error":    emptyToNil(dbError),
		},
		"web": map[string]any{
			"enabled":                            webEnabled,
			"listening":                          webListening,
			"bindAddr":                           webBindAddr,
			"publicBaseUrl":                      s.PublicURL(),
			"telegramAuthMode":                   "login_js",
			"runtimeSpacetimeEnabled":            s.cfg.SatiksmeRuntimeSpacetimeEnabled,
			"browserSpacetimeEnabled":            s.browserSpacetimeConfigured(),
			"browserDirectDataEnabled":           s.browserDirectDataEnabled(),
			"liveTransportSnapshotLookupEnabled": s.browserLiveSnapshotLookupEnabled(),
			"liveTransportRealtimeEnabled":       s.browserDirectDataEnabled(),
		},
		"bundle":       bundlePayload,
		"liveSnapshot": liveSnapshotPayload,
		"catalogStops": catalogStatus.StopCount,
	}
	return healthSnapshot{ok: ok, reasons: reasons, payload: payload}
}

func (s *Server) handlePublicCatalog(w http.ResponseWriter, r *http.Request) {
	s.setNoIndexHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r) {
		return
	}
	s.setRevalidateHeaders(w)
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	if payloadReader, ok := s.catalog.(catalogPayloadReader); ok {
		if etag := payloadReader.CatalogETag(); etag != "" {
			if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
				w.Header().Set("Vary", "Accept-Encoding")
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
		}
	}
	writeJSON(w, http.StatusOK, publicCatalog(catalog))
}

func (s *Server) handlePublicIncidents(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r, "limit") {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	limit, ok := parseIncidentLimit(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	items, err := s.reports.ListActiveIncidents(r.Context(), catalog, now, viewerID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load incidents")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generatedAt": publicTimestamp(now),
		"incidents":   items,
	})
}

func (s *Server) handlePublicIncidentDetail(w http.ResponseWriter, r *http.Request, incidentID string, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r) {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	item, err := s.reports.IncidentDetail(r.Context(), catalog, incidentID, now, viewerID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "unable to load incident")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handlePublicSightings(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r, "limit", "stopId") {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	stopID := strings.TrimSpace(r.URL.Query().Get("stopId"))
	limit, ok := parseSightingsLimit(r, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, stopID, now, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load sightings")
		return
	}
	writeJSON(w, http.StatusOK, visible)
}

func (s *Server) handlePublicMap(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r, "limit") {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	limit, ok := parseSightingsLimit(r, 300)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, "", now, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load map")
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	stopIncidents, areaIncidents, liveVehicles, err := s.publicMapState(r.Context(), catalog, visible, now, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load map")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		GeneratedAt   time.Time               `json:"generatedAt"`
		Stops         []publicBundleStop      `json:"stops"`
		Sightings     model.VisibleSightings  `json:"sightings"`
		StopIncidents []model.IncidentSummary `json:"stopIncidents,omitempty"`
		AreaIncidents []model.IncidentSummary `json:"areaIncidents,omitempty"`
		LiveVehicles  []model.LiveVehicle     `json:"liveVehicles"`
	}{
		GeneratedAt:   publicTimestamp(catalog.GeneratedAt),
		Stops:         publicBundleStops(catalog.Stops),
		Sightings:     visible,
		StopIncidents: stopIncidents,
		AreaIncidents: areaIncidents,
		LiveVehicles:  liveVehicles,
	})
}

func (s *Server) handlePublicMapLive(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r, "limit") {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	limit, ok := parseSightingsLimit(r, 300)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, "", now, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load map")
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	stopIncidents, areaIncidents, liveVehicles, err := s.publicMapState(r.Context(), catalog, visible, now, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load map")
		return
	}
	writeJSON(w, http.StatusOK, model.PublicLiveMapPayload{
		GeneratedAt:   publicTimestamp(catalog.GeneratedAt),
		Sightings:     visible,
		StopIncidents: stopIncidents,
		AreaIncidents: areaIncidents,
		LiveVehicles:  liveVehicles,
	})
}

func (s *Server) handlePublicLiveVehicles(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if !s.allowPublicQuery(w, r, "limit") {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	limit, ok := parseSightingsLimit(r, 300)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, "", now, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load live vehicles")
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	incidents, err := s.reports.ListMapVisibleIncidents(r.Context(), catalog, now, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "unable to load live vehicles")
		return
	}
	vehicleIncidents := make([]model.IncidentSummary, 0, len(incidents))
	for _, incident := range incidents {
		if incident.Scope == "vehicle" {
			vehicleIncidents = append(vehicleIncidents, incident)
		}
	}
	liveVehicles, err := s.publicLiveVehicles(r.Context(), catalog, visible, now, vehicleIncidents)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "live vehicles unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"liveVehicles": liveVehicles,
	})
}

func (s *Server) handlePublicLiveViewer(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if !s.browserLiveViewerHeartbeatEnabled() {
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
		return
	}
	liveStore, ok := s.store.(liveViewerStateStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "live viewer heartbeat unavailable")
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
		Page      string `json:"page"`
		Visible   bool   `json:"visible"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid live viewer heartbeat")
		return
	}
	sessionID := strings.TrimSpace(req.SessionID)
	page := strings.TrimSpace(req.Page)
	if sessionID == "" || len(sessionID) > 160 {
		writeError(w, http.StatusBadRequest, "invalid live viewer session")
		return
	}
	if page == "" {
		page = "public"
	}
	if len(page) > 80 {
		writeError(w, http.StatusBadRequest, "invalid live viewer page")
		return
	}
	if err := liveStore.SetLiveViewerState(r.Context(), sessionID, page, req.Visible); err != nil {
		writeError(w, http.StatusServiceUnavailable, "live viewer heartbeat failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) publicMapState(ctx context.Context, catalog *model.Catalog, visible model.VisibleSightings, now time.Time, viewerID int64) ([]model.IncidentSummary, []model.IncidentSummary, []model.LiveVehicle, error) {
	incidents, err := s.reports.ListMapVisibleIncidents(ctx, catalog, now, viewerID)
	if err != nil {
		return nil, nil, nil, err
	}
	stopIncidents := make([]model.IncidentSummary, 0, len(incidents))
	areaIncidents := make([]model.IncidentSummary, 0, len(incidents))
	vehicleIncidents := make([]model.IncidentSummary, 0, len(incidents))
	for _, incident := range incidents {
		switch incident.Scope {
		case "stop":
			stopIncidents = append(stopIncidents, incident)
		case "area":
			areaIncidents = append(areaIncidents, incident)
		case "vehicle":
			vehicleIncidents = append(vehicleIncidents, incident)
		}
	}
	liveVehicles, err := s.publicLiveVehicles(ctx, catalog, visible, now, vehicleIncidents)
	if err != nil {
		return stopIncidents, areaIncidents, []model.LiveVehicle{}, nil
	}
	return stopIncidents, areaIncidents, liveVehicles, nil
}

func (s *Server) publicLiveVehicles(ctx context.Context, catalog *model.Catalog, visible model.VisibleSightings, now time.Time, incidents []model.IncidentSummary) ([]model.LiveVehicle, error) {
	liveVehicles, err := live.FetchVehicles(ctx, s.liveHTTPClient, "", catalog, now)
	if err != nil {
		return nil, err
	}
	live.ApplyVehicleSightingCounts(liveVehicles, visible.VehicleSightings)
	live.ApplyVehicleIncidents(liveVehicles, incidents)
	return live.PublicLiveVehicles(liveVehicles), nil
}

func (s *Server) handleAuthTelegramConfig(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	if strings.TrimSpace(s.telegramBotID()) == "" || s.telegramLogin == nil {
		writeError(w, http.StatusServiceUnavailable, "Telegram Login is not configured")
		return
	}
	if ok, retryAfter := s.authConfigRate.allow(authRateLimitKey(r), now); !ok {
		setAuthRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "too many login requests")
		return
	}
	nonceClaims, cookie, err := issueLoginNonceCookie(
		s.sessionSecret,
		time.Duration(s.cfg.SatiksmeWebTelegramAuthStateTTLSec)*time.Second,
		now,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cookie.Path = s.cookiePath()
	http.SetCookie(w, cookie)
	http.SetCookie(w, clearLoginStateCookie(s.cookiePath()))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"clientId":      s.telegramBotID(),
		"nonce":         nonceClaims.Nonce,
		"requestAccess": []string{},
		"origin":        s.telegramOrigin(),
		"redirectUri":   s.telegramRedirectURI(),
	})
}

func (s *Server) handleAuthTelegramComplete(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	if !requireJSONContentType(w, r) {
		return
	}
	if ok, retryAfter := s.authCompleteRate.allow(authRateLimitKey(r), now); !ok {
		setAuthRetryAfter(w, retryAfter)
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var payload struct {
		IDToken    string         `json:"idToken"`
		InitData   string         `json:"initData"`
		WidgetAuth map[string]any `json:"widgetAuth"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	idToken := strings.TrimSpace(payload.IDToken)
	initData := strings.TrimSpace(payload.InitData)
	var auth telegramAuth
	var err error

	switch {
	case idToken != "":
		nonceCookie, cookieErr := r.Cookie(loginNonceCookieName)
		if cookieErr != nil {
			writeError(w, http.StatusUnauthorized, "missing login nonce")
			return
		}
		loginNonce, parseErr := parseLoginNonce(s.sessionSecret, nonceCookie.Value, now)
		if parseErr != nil {
			writeError(w, http.StatusUnauthorized, "invalid login nonce")
			return
		}
		if s.telegramLogin == nil {
			writeError(w, http.StatusServiceUnavailable, "Telegram Login is not configured")
			return
		}
		claims, verifyErr := s.telegramLogin.VerifyIDToken(r.Context(), idToken, loginNonce.Nonce, now)
		if verifyErr != nil {
			writeError(w, http.StatusUnauthorized, "invalid Telegram login")
			return
		}
		firstName := strings.TrimSpace(claims.Name)
		if firstName == "" {
			firstName = strings.TrimSpace(claims.PreferredUsername)
		}
		auth = telegramAuth{
			AuthDate: claims.AuthDate,
			User: telegramUser{
				ID:           claims.TelegramID,
				FirstName:    firstName,
				Username:     strings.TrimSpace(claims.PreferredUsername),
				PhotoURL:     strings.TrimSpace(claims.PictureURL),
				LanguageCode: defaultAppLanguage,
			},
		}
	case len(payload.WidgetAuth) > 0:
		auth, err = s.widgetAuthFromPayload(payload.WidgetAuth, now)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid Telegram login")
			return
		}
	case initData != "":
		auth, err = s.initDataAuthFromPayload(initData, now)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid Telegram login")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "missing Telegram login payload")
		return
	}

	if err := verifyTelegramAuthAge(auth, time.Duration(s.cfg.SatiksmeWebTelegramAuthMaxAgeSec)*time.Second, now); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid Telegram login")
		return
	}
	cookie, err := issueSessionCookie(s.sessionSecret, auth, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cookie.Path = s.cookiePath()
	http.SetCookie(w, cookie)
	http.SetCookie(w, clearLoginNonceCookie(s.cookiePath()))
	http.SetCookie(w, clearLoginStateCookie(s.cookiePath()))
	payloadBody, err := s.authPayloadFromAuth(auth)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payloadBody)
}

func (s *Server) handleDeprecatedAuthTelegramStart(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	writeError(w, http.StatusGone, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete")
}

func (s *Server) handleDeprecatedAuthTelegramCallback(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodGet) {
		return
	}
	writeError(w, http.StatusGone, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete")
}

func (s *Server) handleDeprecatedAuthTelegram(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	writeError(w, http.StatusGone, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete")
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request, _ time.Time) {
	s.setNoStoreHeaders(w)
	if !allowMethods(w, r, http.MethodPost) {
		return
	}
	http.SetCookie(w, clearSessionCookie(s.cookiePath()))
	http.SetCookie(w, clearLoginStateCookie(s.cookiePath()))
	http.SetCookie(w, clearLoginNonceCookie(s.cookiePath()))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) authPayloadFromClaims(claims sessionClaims, now time.Time) (map[string]any, error) {
	nickname := model.GenericNickname(claims.UserID)
	auth := telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           claims.UserID,
			FirstName:    nickname,
			LanguageCode: claims.Language,
		},
	}
	return s.authPayloadFromAuth(auth)
}

func (s *Server) authPayloadFromAuth(auth telegramAuth) (map[string]any, error) {
	language := sessionLanguageCode(auth.User.LanguageCode)
	nickname := model.GenericNickname(auth.User.ID)
	firstName := strings.TrimSpace(auth.User.FirstName)
	if firstName == "" {
		firstName = nickname
	}
	payload := map[string]any{
		"ok":            true,
		"authenticated": true,
		"userId":        auth.User.ID,
		"stableUserId":  model.TelegramStableID(auth.User.ID),
		"firstName":     firstName,
		"nickname":      nickname,
		"language":      language,
		"baseUrl":       s.cfg.SatiksmeWebPublicBaseURL,
	}
	return payload, nil
}

func (s *Server) cookiePath() string {
	if s.pathPrefix != "" {
		return s.pathPrefix
	}
	return "/"
}

func (s *Server) defaultReturnTo() string {
	if s.pathPrefix != "" {
		return s.pathPrefix
	}
	return "/"
}

func (s *Server) telegramBotID() string {
	clientID := strings.TrimSpace(s.cfg.SatiksmeWebTelegramClientID)
	if clientID != "" {
		return clientID
	}
	botToken := strings.TrimSpace(s.cfg.BotToken)
	if botToken == "" {
		return ""
	}
	parts := strings.SplitN(botToken, ":", 2)
	if len(parts) != 2 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func (s *Server) telegramOrigin() string {
	publicURL := strings.TrimRight(strings.TrimSpace(s.cfg.SatiksmeWebPublicBaseURL), "/")
	if publicURL == "" {
		return ""
	}
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return publicURL
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (s *Server) telegramRedirectURI() string {
	publicURL := strings.TrimSpace(s.cfg.SatiksmeWebPublicBaseURL)
	if publicURL == "" {
		return ""
	}
	if strings.HasSuffix(publicURL, "/") {
		return publicURL
	}
	return publicURL + "/"
}

func (s *Server) widgetAuthFromPayload(payload map[string]any, now time.Time) (telegramAuth, error) {
	values := url.Values{}
	for key, value := range payload {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values.Set(key, widgetAuthPayloadValue(value))
	}
	return telegramweb.ValidateLoginWidget(values, strings.TrimSpace(s.cfg.BotToken), time.Duration(s.cfg.SatiksmeWebTelegramAuthMaxAgeSec)*time.Second, now)
}

func widgetAuthPayloadValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case uint:
		return strconv.FormatUint(uint64(typed), 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	case uint32:
		return strconv.FormatUint(uint64(typed), 10)
	case uint16:
		return strconv.FormatUint(uint64(typed), 10)
	case uint8:
		return strconv.FormatUint(uint64(typed), 10)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func (s *Server) initDataAuthFromPayload(initData string, now time.Time) (telegramAuth, error) {
	return telegramweb.ValidateInitData(
		strings.TrimSpace(initData),
		strings.TrimSpace(s.cfg.BotToken),
		time.Duration(s.cfg.SatiksmeWebTelegramAuthMaxAgeSec)*time.Second,
		now,
	)
}

func (s *Server) sanitizeReturnTo(raw string) string {
	defaultPath := s.defaultReturnTo()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultPath
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil {
		return defaultPath
	}
	if parsed.IsAbs() || strings.TrimSpace(parsed.Host) != "" || strings.TrimSpace(parsed.Scheme) != "" || parsed.User != nil {
		return defaultPath
	}
	path := strings.TrimSpace(parsed.Path)
	if path == "" {
		path = defaultPath
	}
	if !strings.HasPrefix(path, "/") {
		return defaultPath
	}
	if s.pathPrefix != "" && path != s.pathPrefix && !strings.HasPrefix(path, s.pathPrefix+"/") {
		return defaultPath
	}
	parsed.Scheme = ""
	parsed.Host = ""
	parsed.User = nil
	parsed.Path = path
	parsed.Fragment = ""
	result := parsed.String()
	if strings.TrimSpace(result) == "" {
		return defaultPath
	}
	return result
}

func (s *Server) authRedirectURL(returnTo string, status string) string {
	target, err := url.Parse(s.sanitizeReturnTo(returnTo))
	if err != nil {
		return s.defaultReturnTo()
	}
	values := target.Query()
	values.Set("tgAuth", strings.TrimSpace(status))
	target.RawQuery = values.Encode()
	target.Fragment = ""
	return target.String()
}

func (s *Server) serveBundleActive(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if s.bundleStore == nil {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if hasQueryString(r) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.bundleStore.invalidate()
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	w.Header().Set("Vary", "Accept-Encoding")
	http.ServeFile(w, r, filepath.Join(s.cfg.SatiksmeWebBundleDir, "active.json"))
}

func (s *Server) serveBundleAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if s.bundleStore == nil {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if hasQueryString(r) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/bundles/")
	clean := filepath.Clean(relative)
	if clean == "." || strings.HasPrefix(clean, "..") || excludedGenericPublicAsset(clean) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	fullPath := filepath.Join(s.cfg.SatiksmeWebBundleDir, "bundles", clean)
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setImmutableHeaders(w)
	s.setNoIndexHeaders(w)
	w.Header().Set("Vary", "Accept-Encoding")
	http.ServeFile(w, r, fullPath)
}

func (s *Server) serveLiveSnapshotActive(w http.ResponseWriter, r *http.Request) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if strings.TrimSpace(s.cfg.SatiksmeWebLiveSnapshotDir) == "" {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if hasQueryString(r) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	active, err := live.ReadSnapshotActiveState(s.cfg.SatiksmeWebLiveSnapshotDir)
	if err != nil {
		s.setNoStoreHeaders(w)
		writeError(w, http.StatusInternalServerError, "live snapshot active unavailable")
		return
	}
	if active == nil {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	writeJSON(w, http.StatusOK, map[string]string{
		"version": active.Version,
		"path":    active.Path,
	})
}

func (s *Server) serveLiveSnapshotAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	if !allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if strings.TrimSpace(s.cfg.SatiksmeWebLiveSnapshotDir) == "" {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if hasQueryString(r) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/transport/live/")
	clean := filepath.Clean(relative)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".") || excludedGenericPublicAsset(clean) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if clean == "active.json" {
		s.serveLiveSnapshotActive(w, r)
		return
	}
	fullPath := filepath.Join(s.cfg.SatiksmeWebLiveSnapshotDir, clean)
	info, err := os.Stat(fullPath)
	if err != nil || info.IsDir() {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")
	r = requestWithoutRange(r)
	http.ServeFile(w, r, fullPath)
}

func (s *Server) handleStopReport(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.requireSession(w, r, now)
	if !ok {
		return
	}
	var payload struct {
		StopID string `json:"stopId"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	catalog := s.catalog.Current()
	stop, ok := s.findStop(catalog, payload.StopID)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown stop")
		return
	}
	result, item, err := s.reports.SubmitStopSighting(r.Context(), claims.UserID, payload.StopID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result.Accepted && s.dump != nil && item != nil && !item.Hidden {
		s.dump.EnqueueStop(stop, item)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRecentReports(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	stopID := strings.TrimSpace(r.URL.Query().Get("stopId"))
	limit, ok := parseSightingsLimit(r, 100)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	visible, err := s.reports.UserSightings(r.Context(), catalog, claims.UserID, stopID, now, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, visible)
}

func (s *Server) handleVehicleReport(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.requireSession(w, r, now)
	if !ok {
		return
	}
	var payload model.VehicleReportInput
	if !decodeJSON(w, r, &payload) {
		return
	}
	payload.StopID = ""
	if strings.TrimSpace(payload.Mode) == "" || strings.TrimSpace(payload.RouteLabel) == "" {
		writeError(w, http.StatusBadRequest, "mode and routeLabel are required")
		return
	}
	result, item, err := s.reports.SubmitVehicleSighting(r.Context(), claims.UserID, payload, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result.Accepted && s.dump != nil && item != nil && !item.Hidden {
		s.dump.EnqueueVehicle(item)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleAreaReport(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claims, ok := s.requireSession(w, r, now)
	if !ok {
		return
	}
	var payload model.AreaReportInput
	if !decodeJSON(w, r, &payload) {
		return
	}
	if _, err := reports.NormalizeAreaReportInput(payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, item, err := s.reports.SubmitAreaReport(r.Context(), claims.UserID, payload, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result.Accepted && s.dump != nil && item != nil && !item.Hidden {
		s.dump.EnqueueArea(item)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleIncidentVote(w http.ResponseWriter, r *http.Request, claims sessionClaims, incidentID string, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Value string `json:"value"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	value, ok := model.ParseIncidentVoteValue(payload.Value)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid vote value")
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	summary, err := s.reports.VoteIncident(r.Context(), catalog, incidentID, claims.UserID, value, now)
	if err != nil {
		var rateErr *reports.RateLimitError
		if errors.As(err, &rateErr) {
			writeError(w, http.StatusTooManyRequests, rateErr.Error())
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleIncidentComment(w http.ResponseWriter, r *http.Request, claims sessionClaims, incidentID string, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Body string `json:"body"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	comment, err := s.reports.AddIncidentComment(r.Context(), catalog, incidentID, claims.UserID, payload.Body, now)
	if err != nil {
		var rateErr *reports.RateLimitError
		if errors.As(err, &rateErr) {
			writeError(w, http.StatusTooManyRequests, rateErr.Error())
			return
		}
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, comment)
}

func (s *Server) requireSession(w http.ResponseWriter, r *http.Request, now time.Time) (sessionClaims, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "missing session")
		return sessionClaims{}, false
	}
	claims, err := parseSession(s.sessionSecret, cookie.Value, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid session")
		return sessionClaims{}, false
	}
	return claims, true
}

func (s *Server) optionalSession(r *http.Request, now time.Time) (sessionClaims, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return sessionClaims{}, false
	}
	claims, err := parseSession(s.sessionSecret, cookie.Value, now)
	if err != nil {
		return sessionClaims{}, false
	}
	return claims, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if strings.EqualFold(contentType, "application/json") {
		return true
	}
	writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
	return false
}

func (s *Server) findStop(catalog *model.Catalog, stopID string) (model.Stop, bool) {
	if finder, ok := s.catalog.(catalogStopFinder); ok {
		if stop, found := finder.FindStop(stopID); found {
			return stop, true
		}
	}
	return findStop(catalog, stopID)
}

func findStop(catalog *model.Catalog, stopID string) (model.Stop, bool) {
	return model.FindStopByAnyID(catalog, stopID)
}

func parseSightingsLimit(r *http.Request, fallback int) (int, bool) {
	values, ok := r.URL.Query()["limit"]
	if !ok {
		return fallback, true
	}
	if len(values) != 1 {
		return 0, false
	}
	raw := strings.TrimSpace(values[0])
	if raw == "" {
		return fallback, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 || parsed > 500 {
		return 0, false
	}
	return parsed, true
}

func parseIncidentLimit(r *http.Request) (int, bool) {
	values, ok := r.URL.Query()["limit"]
	if !ok {
		return 0, true
	}
	if len(values) != 1 {
		return 0, false
	}
	raw := strings.TrimSpace(values[0])
	if raw == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 || parsed > 2000 {
		return 0, false
	}
	return parsed, true
}

func (s *Server) allowPublicQuery(w http.ResponseWriter, r *http.Request, allowedKeys ...string) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	allowed := make(map[string]bool, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = true
	}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			s.setNoStoreHeaders(w)
			writeError(w, http.StatusBadRequest, "invalid query")
			return false
		}
	}
	return true
}

func (s *Server) dbStatus(ctx context.Context) (bool, string) {
	if s.store == nil {
		return false, "store unavailable"
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.store.HealthCheck(checkCtx); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func (s *Server) catalogStale(status runtime.CatalogStatus, now time.Time) bool {
	if !status.Loaded || status.GeneratedAt.IsZero() {
		return false
	}
	refreshAfter := time.Duration(s.cfg.CatalogRefreshHours) * time.Hour
	if refreshAfter <= 0 {
		refreshAfter = 24 * time.Hour
	}
	return now.After(status.GeneratedAt.Add(refreshAfter * 2))
}

func (s *Server) setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Strict-Transport-Security", "max-age=31536000")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), fullscreen=(self)")
	h.Set("Content-Security-Policy", s.contentSecurityPolicy(""))
}

func (s *Server) setShellSecurityHeaders(w http.ResponseWriter, scriptNonce string) {
	s.setSecurityHeaders(w)
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(scriptNonce))
}

func (s *Server) contentSecurityPolicy(scriptNonce string) string {
	connectSources := []string{"'self'"}
	if s.browserDirectDataEnabled() {
		addCSPConnectSources(&connectSources, s.cfg.SatiksmeWebSpacetimeHost)
	}
	scriptSources := []string{"'self'"}
	if strings.TrimSpace(scriptNonce) != "" {
		scriptSources = append(scriptSources, "'nonce-"+strings.TrimSpace(scriptNonce)+"'")
	}
	scriptSources = append(scriptSources, "https://oauth.telegram.org")
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self' https://oauth.telegram.org",
		"script-src " + strings.Join(scriptSources, " "),
		"style-src 'self'",
		"img-src 'self' data: blob: https://*.tile.openstreetmap.org",
		"font-src 'self' data:",
		"connect-src " + strings.Join(connectSources, " "),
		"frame-src https://oauth.telegram.org https://telegram.org",
		"worker-src 'self' blob:",
	}, "; ")
}

func newCSPNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate CSP nonce: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(raw[:]), nil
}

func addCSPConnectSources(sources *[]string, raw string) {
	origin := cspOrigin(raw)
	if origin == "" {
		return
	}
	appendUniqueString(sources, origin)
	parsed, err := url.Parse(origin)
	if err != nil || parsed == nil || parsed.Host == "" {
		return
	}
	switch parsed.Scheme {
	case "https":
		appendUniqueString(sources, "wss://"+parsed.Host)
	case "http":
		appendUniqueString(sources, "ws://"+parsed.Host)
	}
}

func cspOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func appendUniqueString(values *[]string, next string) {
	next = strings.TrimSpace(next)
	if next == "" {
		return
	}
	for _, existing := range *values {
		if existing == next {
			return
		}
	}
	*values = append(*values, next)
}

func (s *Server) setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
}

func (s *Server) setRevalidateHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("CDN-Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=0, must-revalidate")
}

func (s *Server) setImmutableHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("CDN-Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=31536000, immutable")
}

func requestWithoutRange(r *http.Request) *http.Request {
	if strings.TrimSpace(r.Header.Get("Range")) == "" {
		return r
	}
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Del("Range")
	return clone
}

func (s *Server) setNoIndexHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Robots-Tag", "noindex, noarchive")
}

func (s *Server) allowAssetQuery(r *http.Request, assetPath string) bool {
	if r.URL.RawQuery == "" {
		return true
	}
	query := r.URL.Query()
	values, ok := query["v"]
	if !ok || len(query) != 1 || len(values) != 1 {
		return false
	}
	return strings.TrimSpace(values[0]) != "" && strings.TrimSpace(values[0]) == s.release.AssetHash(assetPath)
}

func excludedGenericPublicAsset(assetPath string) bool {
	cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+assetPath)), "/")
	name := strings.ToLower(filepath.Base(cleanPath))
	return strings.HasSuffix(name, ".test.js") || strings.HasSuffix(name, ".test.css") || strings.HasSuffix(name, ".map")
}

func allowMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func unsafePublicPath(r *http.Request) bool {
	escaped := r.URL.EscapedPath()
	lowerEscaped := strings.ToLower(escaped)
	if strings.Contains(lowerEscaped, "%2f") || strings.Contains(lowerEscaped, "%5c") {
		return true
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return true
	}
	for _, candidate := range []string{r.URL.Path, escaped, decoded} {
		if strings.Contains(candidate, "//") || strings.Contains(candidate, "\\") {
			return true
		}
		for _, segment := range strings.Split(candidate, "/") {
			if segment == "." || segment == ".." {
				return true
			}
		}
	}
	return false
}

func hasQueryString(r *http.Request) bool {
	return r.URL.RawQuery != ""
}

func isLocalRequest(r *http.Request) bool {
	host := strings.TrimSpace(r.RemoteAddr)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func optionalTimeValue(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at.UTC()
}

func optionalAgeSeconds(at, now time.Time) any {
	if at.IsZero() {
		return nil
	}
	seconds := int(now.Sub(at.UTC()).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func publicTimestamp(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Second)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Robots-Tag", "noindex, noarchive")
	w.Header().Set("Vary", "Accept-Encoding")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": message})
}
