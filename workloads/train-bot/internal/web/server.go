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
	"log"
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
	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/schedule"
)

//go:embed static/*
var staticFS embed.FS

const (
	maxJSONBodyBytes   = 64 * 1024
	defaultPublicLimit = 60
	maxPublicLimit     = 2000
)

type RideNotifier interface {
	NotifyRideUsers(ctx context.Context, reporterID int64, trainID string, signal domain.SignalType, now time.Time) error
	NotifyStationSighting(ctx context.Context, event domain.StationSighting, now time.Time) error
}

type Server struct {
	cfg                 config.Config
	app                 *trainapp.Service
	catalog             *i18n.Catalog
	loc                 *time.Location
	now                 func() time.Time
	pathPrefix          string
	sessionSecret       []byte
	testLogin           *testLoginBroker
	spacetime           *spacetimeTokenIssuer
	spacetimeIssueToken func(telegramAuth, time.Time) (spacetimeIssuedToken, error)
	telegramLogin       *telegramweb.LoginVerifier
	authConfigRate      *clientRateLimiter
	authCompleteRate    *clientRateLimiter
	notifier            RideNotifier
	static              fs.FS
	release             releaseInfo
	pageTemplate        *template.Template
	publicEdgeCache     *publicEdgeCache
	bundleStore         *staticBundleStore
}

type pageData struct {
	BasePath                string
	PublicBaseURL           string
	Mode                    string
	TrainID                 string
	HTMLLang                string
	StationCheckin          bool
	MiniAppRefreshMs        int
	PublicRefreshMs         int
	ExternalTrainMapEnabled bool
	ExternalTrainMapBaseURL string
	ExternalTrainMapWsURL   string
	ExternalTrainGraphURL   string
	SpacetimeHost           string
	SpacetimeDatabase       string
	PublicEdgeCacheEnabled  bool
	BundleManifestURL       string
	BundleVersion           string
	BundleServiceDate       string
	BundleGeneratedAt       string
	BundleTransformVersion  string
	ScheduleJSON            template.JS
	ScriptNonce             string
	TelegramWebAppScript    bool
	AppCSSURL               string
	LeafletCSSURL           string
	LeafletJSURL            string
	ExternalFeedJSURL       string
	AppJSURL                string
}

const (
	deferredMapMessage = "Train map is temporarily unavailable while the simplified train app release is being rebuilt."
	retiredRideMessage = "Ride check-in, saved routes, and personal ride tools were removed from the simplified train app."
	legacyBasePath     = "/pixel-stack/train"
)

func NewServer(cfg config.Config, appSvc *trainapp.Service, catalog *i18n.Catalog, loc *time.Location) (*Server, error) {
	pathPrefix := "/pixel-stack/train"
	if strings.TrimSpace(cfg.TrainWebPublicBaseURL) != "" {
		parsed, err := url.Parse(cfg.TrainWebPublicBaseURL)
		if err != nil {
			return nil, fmt.Errorf("parse TRAIN_WEB_PUBLIC_BASE_URL: %w", err)
		}
		if strings.TrimSpace(parsed.Path) == "" || parsed.Path == "/" {
			pathPrefix = ""
		}
		if strings.TrimSpace(parsed.Path) != "" && parsed.Path != "/" {
			pathPrefix = strings.TrimRight(parsed.Path, "/")
		}
	}

	staticFiles := mustStaticSubFS()
	release, err := newReleaseInfo(staticFiles)
	if err != nil {
		return nil, err
	}

	server := &Server{
		cfg:              cfg,
		app:              appSvc,
		catalog:          catalog,
		loc:              loc,
		now:              time.Now,
		pathPrefix:       pathPrefix,
		authConfigRate:   newClientRateLimiter(authConfigRequestsPerMinute, authRateLimitWindow),
		authCompleteRate: newClientRateLimiter(authCompleteRequestsPerMinute, authRateLimitWindow),
		static:           staticFiles,
		release:          release,
		pageTemplate: template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="{{.HTMLLang}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="robots" content="noindex, noarchive">
  <title>vivi kontrole bot</title>
  <link rel="stylesheet" href="{{.AppCSSURL}}">
  <link rel="stylesheet" href="{{.LeafletCSSURL}}">
  <script nonce="{{.ScriptNonce}}">
    window.TRAIN_APP_CONFIG = {
      basePath: {{.BasePath}},
      publicBaseURL: {{.PublicBaseURL}},
      mode: {{.Mode}},
      trainId: {{.TrainID}},
      stationCheckinEnabled: {{if .StationCheckin}}true{{else}}false{{end}},
      miniAppRefreshMs: {{.MiniAppRefreshMs}},
      publicRefreshMs: {{.PublicRefreshMs}},
      externalTrainMapEnabled: {{if .ExternalTrainMapEnabled}}true{{else}}false{{end}},
      externalTrainMapBaseURL: {{.ExternalTrainMapBaseURL}},
      externalTrainMapWsURL: {{.ExternalTrainMapWsURL}},
      externalTrainGraphURL: {{.ExternalTrainGraphURL}},
      spacetimeHost: {{.SpacetimeHost}},
      spacetimeDatabase: {{.SpacetimeDatabase}},
      publicEdgeCacheEnabled: {{if .PublicEdgeCacheEnabled}}true{{else}}false{{end}},
      bundleManifestURL: {{.BundleManifestURL}},
      bundleVersion: {{.BundleVersion}},
      bundleServiceDate: {{.BundleServiceDate}},
      bundleFreshness: {
        generatedAt: {{.BundleGeneratedAt}},
        transformVersion: {{.BundleTransformVersion}}
      },
      schedule: {{.ScheduleJSON}}
    };
  </script>
  {{if .TelegramWebAppScript}}<script src="https://telegram.org/js/telegram-web-app.js"></script>{{end}}
  <script defer src="{{.LeafletJSURL}}"></script>
  <script defer src="{{.ExternalFeedJSURL}}"></script>
  <script defer src="{{.AppJSURL}}"></script>
</head>
<body>
  <div id="app"></div>
</body>
</html>`)),
	}
	if cfg.TrainWebEnabled {
		secret, err := loadSessionSecret(cfg.TrainWebSessionSecretFile)
		if err != nil {
			return nil, err
		}
		server.sessionSecret = secret
		if botID := strings.TrimSpace(server.telegramBotID()); botID != "" {
			verifier, verifierErr := telegramweb.NewLoginVerifier(telegramweb.LoginVerifierConfig{
				ClientID:    botID,
				AllowedSkew: 30 * time.Second,
			})
			if verifierErr != nil {
				return nil, verifierErr
			}
			server.telegramLogin = verifier
		}
	}
	if cfg.TrainWebTestLoginEnabled && !productionTrainPublicBaseURL(cfg.TrainWebPublicBaseURL) {
		broker, err := newTestLoginBroker(cfg)
		if err != nil {
			return nil, err
		}
		server.testLogin = broker
	}
	if cfg.TrainWebEnabled {
		issuer, err := newSpacetimeTokenIssuer(cfg)
		if err != nil {
			return nil, err
		}
		server.spacetime = issuer
	}
	if cfg.TrainWebPublicEdgeCacheEnabled {
		cache, err := newPublicEdgeCache(
			cfg.TrainWebPublicEdgeCacheStateFile,
			cfg.TrainWebPublicEdgeCacheTTLSec,
			release.AppJSHash,
		)
		if err != nil {
			return nil, err
		}
		server.publicEdgeCache = cache
	}
	if strings.TrimSpace(cfg.TrainWebBundleDir) != "" {
		server.bundleStore = newStaticBundleStore(cfg.TrainWebBundleDir)
	}
	return server, nil
}

func productionTrainPublicBaseURL(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return true
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return false
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return false
	case "vilciens.kontrole.info":
		return true
	}
	return !strings.HasSuffix(host, ".test")
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
	body, err = stripTrainAppTestHarness(body)
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
		return stripTrainAppTestHarness(body)
	}
	return body, nil
}

func cleanStaticAssetName(name string) string {
	return strings.TrimPrefix(path.Clean("/"+name), "/")
}

func stripTrainAppTestHarness(body []byte) ([]byte, error) {
	source := string(body)
	var err error
	source, err = stripNamedJSFunction(source, "resetStateForTest")
	if err != nil {
		return nil, err
	}
	start := strings.Index(source, "\n  if (typeof module === \"object\" && module.exports) {\n    const exported = {};")
	end := strings.LastIndex(source, "\n    module.exports = exported;\n  }\n})();")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("train app test harness markers not found")
	}
	next := source[:start] + "\n})();\n"
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

func (s *Server) SetNotifier(notifier RideNotifier) {
	s.notifier = notifier
}

func (s *Server) AppURL() string {
	if !s.cfg.TrainWebEnabled {
		return ""
	}
	return strings.TrimRight(s.cfg.TrainWebPublicBaseURL, "/") + "/app"
}

func (s *Server) Enabled() bool {
	return s.cfg.TrainWebEnabled
}

func (s *Server) Run(ctx context.Context) error {
	if !s.cfg.TrainWebEnabled {
		return nil
	}

	httpServer := &http.Server{
		Addr:              net.JoinHostPort(s.cfg.TrainWebBindAddr, strconv.Itoa(s.cfg.TrainWebPort)),
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen train web server: %w", err)
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
		s.writeError(w, http.StatusBadRequest, "bad path")
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
	if s.serveHTTPForBasePath(w, r, path, basePath) {
		return
	}
	if basePath != "" && basePath != legacyBasePath && s.serveHTTPForBasePath(w, r, path, legacyBasePath) {
		return
	}
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	http.NotFound(w, r)
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

func (s *Server) serveHTTPForBasePath(w http.ResponseWriter, r *http.Request, path string, basePath string) bool {
	switch {
	case path == strings.TrimRight(basePath+"/robots.txt", "/"):
		s.serveRobotsTxt(w, r)
		return true
	case path == strings.TrimRight(basePath+"/oidc/.well-known/openid-configuration", "/"):
		if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return true
		}
		s.handleSpacetimeOpenIDConfiguration(w, r)
		return true
	case path == strings.TrimRight(basePath+"/oidc/jwks.json", "/"):
		if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return true
		}
		s.handleSpacetimeJWKS(w, r)
		return true
	case path == basePath || path == "":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-network-map", ""))
		return true
	case path == basePath+"/app":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "mini-app", ""))
		return true
	case path == basePath+"/stations":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-stations", ""))
		return true
	case path == basePath+"/incidents":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-incidents", ""))
		return true
	case path == basePath+"/events":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-incidents", ""))
		return true
	case path == basePath+"/map":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-network-map", ""))
		return true
	case path == basePath+"/feed":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-dashboard", ""))
		return true
	case path == basePath+"/departures":
		s.serveShellPage(w, r, http.StatusOK, s.newPageData(basePath, "public-dashboard", ""))
		return true
	case strings.HasPrefix(path, basePath+"/t/") && strings.HasSuffix(path, "/map"):
		if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return true
		}
		trainID := strings.TrimSuffix(strings.TrimPrefix(path, basePath+"/t/"), "/map")
		trainID = strings.Trim(trainID, "/")
		if trainID == "" || strings.Contains(trainID, "/") {
			s.setNoStoreHeaders(w)
			s.setNoIndexHeaders(w)
			http.NotFound(w, r)
			return true
		}
		s.servePublicTrainShell(w, r, basePath, "public-map", trainID)
		return true
	case strings.HasPrefix(path, basePath+"/t/"):
		if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
			return true
		}
		trainID := strings.TrimPrefix(path, basePath+"/t/")
		if trainID == "" || strings.Contains(trainID, "/") {
			s.setNoStoreHeaders(w)
			s.setNoIndexHeaders(w)
			http.NotFound(w, r)
			return true
		}
		s.servePublicTrainShell(w, r, basePath, "public-train", trainID)
		return true
	case strings.HasPrefix(path, basePath+"/assets/"):
		s.serveAsset(w, r, basePath)
		return true
	case strings.HasPrefix(path, basePath+"/api/v1/"):
		s.handleAPI(w, r, strings.TrimPrefix(path, basePath+"/api/v1"))
		return true
	default:
		return false
	}
}

func (s *Server) serveRobotsTxt(w http.ResponseWriter, r *http.Request) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
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

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, route string) {
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().In(s.loc)
	if !s.allowUnsafeAPIRequest(w, r) {
		return
	}
	switch {
	case route == "/internal/health":
		s.handleInternalHealth(w, r, now)
	case route == "/health":
		s.handleHealth(w, r, now)
	case route == "/ready":
		s.handleReady(w, r, now)
	case route == "/messages":
		s.handleMessages(w, r)
	case route == "/public/incidents":
		s.handlePublicIncidents(w, r, now)
	case strings.HasPrefix(route, "/public/incidents/"):
		incidentID := strings.TrimPrefix(route, "/public/incidents/")
		incidentID = strings.Trim(incidentID, "/")
		s.handlePublicIncidentDetail(w, r, incidentID, now)
	case route == "/public/feed":
		s.handlePublicDashboard(w, r, now)
	case route == "/public/dashboard":
		s.handlePublicDashboard(w, r, now)
	case route == "/public/service-day-trains":
		s.handlePublicServiceDayTrains(w, r, now)
	case route == "/public/map":
		s.handlePublicMap(w, r, now)
	case route == "/public/stations":
		s.handlePublicStations(w, r, now)
	case route == "/public/route-checkin-routes":
		s.handlePublicRouteCheckInRoutes(w, r, now)
	case strings.HasPrefix(route, "/public/stations/") && strings.HasSuffix(route, "/departures"):
		trimmed := strings.TrimPrefix(route, "/public/stations/")
		stationID := strings.TrimSuffix(trimmed, "/departures")
		stationID = strings.Trim(stationID, "/")
		s.handlePublicStationDepartures(w, r, stationID, now)
	case strings.HasPrefix(route, "/public/trains/") && strings.HasSuffix(route, "/stops"):
		trainID := strings.TrimSuffix(strings.TrimPrefix(route, "/public/trains/"), "/stops")
		trainID = strings.Trim(trainID, "/")
		s.handlePublicTrainStops(w, r, trainID, now)
	case strings.HasPrefix(route, "/public/trains/"):
		s.handlePublicTrain(w, r, strings.TrimPrefix(route, "/public/trains/"), now)
	case route == "/auth/telegram/config":
		s.handleAuthTelegramConfig(w, r, now)
	case route == "/auth/telegram/complete":
		s.handleAuthTelegramComplete(w, r, now)
	case route == "/auth/telegram":
		s.handleAuthTelegram(w, r, now)
	case route == "/auth/spacetime":
		if !s.allowMethods(w, r, http.MethodPost) {
			return
		}
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleAuthSpacetime(w, r, claims, now)
	case route == "/auth/logout":
		s.handleAuthLogout(w, r, now)
	case route == "/auth/test":
		s.handleAuthTest(w, r, now)
	case route == "/session":
		s.handleSession(w, r, now)
	case route == "/me":
		if !s.allowMethods(w, r, http.MethodGet) {
			return
		}
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleMe(w, r, claims, now)
	case route == "/route-checkins/current":
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleCurrentRouteCheckIn(w, r, claims, now)
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
	case strings.HasPrefix(route, "/windows/"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleWindowTrains(w, r, claims, strings.TrimPrefix(route, "/windows/"), now)
	case route == "/stations":
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleStations(w, r, claims, now)
	case strings.HasPrefix(route, "/stations/") && strings.HasSuffix(route, "/sighting-destinations"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trimmed := strings.TrimPrefix(route, "/stations/")
		stationID := strings.TrimSuffix(trimmed, "/sighting-destinations")
		stationID = strings.Trim(stationID, "/")
		s.handleStationSightingDestinations(w, r, claims, stationID, now)
	case strings.HasPrefix(route, "/stations/") && strings.HasSuffix(route, "/departures"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trimmed := strings.TrimPrefix(route, "/stations/")
		stationID := strings.TrimSuffix(trimmed, "/departures")
		stationID = strings.Trim(stationID, "/")
		s.handleStationDepartures(w, r, claims, stationID, now)
	case strings.HasPrefix(route, "/stations/") && strings.HasSuffix(route, "/sightings"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trimmed := strings.TrimPrefix(route, "/stations/")
		stationID := strings.TrimSuffix(trimmed, "/sightings")
		stationID = strings.Trim(stationID, "/")
		s.handleStationSighting(w, r, claims, stationID, now)
	case strings.HasPrefix(route, "/stations/") && strings.HasSuffix(route, "/reports"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trimmed := strings.TrimPrefix(route, "/stations/")
		stationID := strings.TrimSuffix(trimmed, "/reports")
		stationID = strings.Trim(stationID, "/")
		s.handleStationReport(w, r, claims, stationID, now)
	case route == "/location-reports":
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleLocationReport(w, r, claims, now)
	case route == "/routes/destinations":
		s.writeRetired(w, retiredRideMessage)
	case route == "/routes/trains":
		s.writeRetired(w, retiredRideMessage)
	case route == "/favorites":
		s.writeRetired(w, retiredRideMessage)
	case route == "/checkins/current":
		s.writeRetired(w, retiredRideMessage)
	case route == "/checkins/current/undo":
		s.writeRetired(w, retiredRideMessage)
	case strings.HasPrefix(route, "/trains/") && strings.HasSuffix(route, "/status"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trainID := strings.TrimSuffix(strings.TrimPrefix(route, "/trains/"), "/status")
		trainID = strings.Trim(trainID, "/")
		s.handleTrainStatus(w, r, claims, trainID, now)
	case strings.HasPrefix(route, "/trains/") && strings.HasSuffix(route, "/stops"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trainID := strings.TrimSuffix(strings.TrimPrefix(route, "/trains/"), "/stops")
		trainID = strings.Trim(trainID, "/")
		s.handleTrainStops(w, r, claims, trainID, now)
	case strings.HasPrefix(route, "/trains/") && strings.HasSuffix(route, "/timeline"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trainID := strings.TrimSuffix(strings.TrimPrefix(route, "/trains/"), "/timeline")
		trainID = strings.Trim(trainID, "/")
		s.handleTrainTimeline(w, r, claims, trainID, now)
	case strings.HasPrefix(route, "/trains/") && strings.HasSuffix(route, "/reports"):
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		trainID := strings.TrimSuffix(strings.TrimPrefix(route, "/trains/"), "/reports")
		trainID = strings.Trim(trainID, "/")
		s.handleTrainReport(w, r, claims, trainID, now)
	case strings.HasPrefix(route, "/trains/") && strings.HasSuffix(route, "/mute"):
		s.writeRetired(w, retiredRideMessage)
	case route == "/settings":
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleSettings(w, r, claims)
	default:
		s.writeError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) servePublicTrainShell(w http.ResponseWriter, r *http.Request, basePath string, mode string, trainID string) {
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().In(s.loc)
	exists, err := s.publicTrainShellExists(r.Context(), trainID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if !exists {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.serveShell(w, http.StatusOK, s.newPageData(basePath, mode, trainID))
}

func (s *Server) publicTrainShellExists(ctx context.Context, trainID string, now time.Time) (bool, error) {
	cleanTrainID := strings.TrimSpace(trainID)
	if cleanTrainID == "" {
		return false, nil
	}
	if data, ok, err := s.bundleData(); err != nil {
		return false, err
	} else if ok {
		_, exists := data.trainsByID[cleanTrainID]
		return exists, nil
	}
	if !s.appScheduleContext(now).Available {
		return true, nil
	}
	if _, err := s.app.PublicTrain(ctx, cleanTrainID, now); err != nil {
		if errors.Is(err, trainapp.ErrNotFound) {
			return false, nil
		}
		return true, nil
	}
	return true, nil
}

func (s *Server) allowUnsafeAPIRequest(w http.ResponseWriter, r *http.Request) bool {
	if !unsafeHTTPMethod(r.Method) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "cross-site", "same-site":
		s.writeError(w, http.StatusForbidden, "cross-site request forbidden")
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !s.sameOriginRequest(r, origin) {
		s.writeError(w, http.StatusForbidden, "cross-site request forbidden")
		return false
	}
	if strings.TrimSpace(r.Header.Get("Origin")) == "" {
		if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" && !s.sameOriginRequest(r, referer) {
			s.writeError(w, http.StatusForbidden, "cross-site request forbidden")
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
		requestOrigin(s.cfg.TrainWebPublicBaseURL),
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleInternalHealth(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	if !isLocalRequest(r) {
		http.NotFound(w, r)
		return
	}
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	s.writeJSON(w, http.StatusOK, s.healthPayload(now, true))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	_, scheduleErr := s.appScheduleAvailability()
	scheduleCtx := s.appScheduleContext(now)
	ready, _ := scheduleReadiness(scheduleCtx, s.appLoadedServiceDate(), scheduleErr)
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	s.writeJSON(w, status, map[string]any{
		"ok":    ready,
		"ready": ready,
	})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	lang, ok := parsePublicMessagesLanguage(r.URL.Query().Get("lang"))
	if !ok {
		s.writeError(w, http.StatusBadRequest, "unsupported language")
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, s.now(), publicEdgeCacheMessagesRoute(string(lang)))
	if handled {
		return
	}
	s.writePublicJSON(w, http.StatusOK, map[string]any{
		"lang":     lang,
		"messages": s.catalog.Messages(lang),
	}, decision)
}

func parsePublicMessagesLanguage(raw string) (domain.Language, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return domain.DefaultLanguage, true
	}
	if strings.EqualFold(trimmed, string(domain.LanguageLV)) {
		return domain.LanguageLV, true
	}
	if strings.EqualFold(trimmed, string(domain.LanguageEN)) {
		return domain.LanguageEN, true
	}
	return domain.DefaultLanguage, false
}

func parsePublicLimit(r *http.Request) (int, bool) {
	values, ok := r.URL.Query()["limit"]
	if !ok {
		return defaultPublicLimit, true
	}
	if len(values) != 1 {
		return 0, false
	}
	raw := strings.TrimSpace(values[0])
	if raw == "" {
		return defaultPublicLimit, true
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 || parsed > maxPublicLimit {
		return 0, false
	}
	return parsed, true
}

func (s *Server) handlePublicIncidents(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	limit, ok := parsePublicLimit(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheIncidentsRoute(limit))
	if handled {
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	items, err := s.app.OngoingIncidents(r.Context(), now, viewerID, limit)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, map[string]any{
		"generatedAt": now.UTC(),
		"incidents":   items,
		"schedule":    s.appScheduleContext(now),
	}, decision)
}

func (s *Server) handlePublicIncidentDetail(w http.ResponseWriter, r *http.Request, incidentID string, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheIncidentDetailRoute(incidentID))
	if handled {
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	item, err := s.app.IncidentDetail(r.Context(), incidentID, now, viewerID)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, s.withSchedulePayload(item, now), decision)
}

func (s *Server) handlePublicDashboard(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	limit, ok := parsePublicLimit(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheDashboardRoute(limit))
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicDashboard(now, limit); err != nil {
		s.writeAppError(w, err)
		return
	} else if ok {
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	payload, err := s.app.PublicDashboardPayload(r.Context(), now, limit)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, payload, decision)
}

func (s *Server) handlePublicServiceDayTrains(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheServiceDayTrainsRoute())
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicServiceDayTrains(now); err != nil {
		s.writeAppError(w, err)
		return
	} else if ok {
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	payload, err := s.app.PublicServiceDayPayload(r.Context(), now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, payload, decision)
}

func (s *Server) handlePublicMap(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheNetworkMapRoute())
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicNetworkMap(r.Context(), now); err != nil {
		s.writeAppError(w, err)
		return
	} else if ok {
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	item, err := s.app.NetworkMap(r.Context(), now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, s.withSchedulePayload(item, now), decision)
}

func (s *Server) handlePublicStations(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheStationSearchRoute(r.URL.Query().Get("q")))
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicStations(now, r.URL.Query().Get("q")); err != nil {
		s.writeAppError(w, err)
		return
	} else if ok {
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	items, err := s.app.Stations(r.Context(), now, r.URL.Query().Get("q"))
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, map[string]any{
		"stations": items,
		"schedule": s.appScheduleContext(now),
	}, decision)
}

func (s *Server) handlePublicStationDepartures(w http.ResponseWriter, r *http.Request, stationID string, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheStationDeparturesRoute(stationID))
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicStationDepartures(now, stationID); err != nil {
		s.writeAppError(w, err)
		return
	} else if ok {
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	item, err := s.app.PublicStationDepartures(r.Context(), now, stationID, 8)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, s.withSchedulePayload(item, now), decision)
}

func (s *Server) handlePublicTrain(w http.ResponseWriter, r *http.Request, trainID string, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheTrainRoute(trainID))
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicTrain(now, trainID); err != nil {
		s.writeAppError(w, err)
		return
	} else if ok {
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	item, err := s.app.PublicTrain(r.Context(), trainID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, s.withSchedulePayload(item, now), decision)
}

func (s *Server) handlePublicTrainStops(w http.ResponseWriter, r *http.Request, trainID string, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheTrainStopsRoute(trainID))
	if handled {
		return
	}
	item, err := s.app.TrainStops(r.Context(), 0, now, trainID)
	if err == nil {
		s.writePublicJSON(w, http.StatusOK, s.withSchedulePayload(item, now), decision)
		return
	}
	if payload, ok, bundleErr := s.bundlePublicTrainStops(now, trainID); bundleErr != nil {
		s.writeAppError(w, bundleErr)
		return
	} else if ok {
		s.applyBundleTrainCardRiderCount(r.Context(), payload, trainID, now)
		s.writePublicJSON(w, http.StatusOK, payload, decision)
		return
	}
	s.writeAppError(w, err)
}

func (s *Server) handlePublicRouteCheckInRoutes(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	if r.URL.RawQuery != "" {
		s.writeError(w, http.StatusBadRequest, "invalid query")
		return
	}
	routes, err := s.app.RouteCheckInRoutes(r.Context(), now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writePublicJSON(w, http.StatusOK, map[string]any{
		"routes":                 routes,
		"defaultDurationMinutes": trainapp.RouteCheckInDefaultMinutes,
		"minDurationMinutes":     trainapp.RouteCheckInMinMinutes,
		"maxDurationMinutes":     trainapp.RouteCheckInMaxMinutes,
		"schedule":               s.appScheduleContext(now),
	}, nil)
}

func (s *Server) handleAuthTelegramConfig(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodGet) {
		return
	}
	if strings.TrimSpace(s.telegramBotID()) == "" || s.telegramLogin == nil {
		s.writeError(w, http.StatusServiceUnavailable, "Telegram Login is not configured")
		return
	}
	if ok, retryAfter := s.authConfigRate.allow(authRateLimitKey(r), now); !ok {
		setAuthRetryAfter(w, retryAfter)
		s.writeError(w, http.StatusTooManyRequests, "too many login requests")
		return
	}
	nonceClaims, cookie, err := issueLoginNonceCookie(
		s.sessionSecret,
		time.Duration(s.cfg.TrainWebTelegramAuthStateTTLSec)*time.Second,
		now,
	)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cookie.Path = s.cookiePath()
	http.SetCookie(w, cookie)
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"clientId":      s.telegramBotID(),
		"nonce":         nonceClaims.Nonce,
		"requestAccess": []string{},
		"origin":        s.telegramOrigin(),
		"redirectUri":   s.telegramRedirectURI(),
	})
}

func (s *Server) handleAuthTelegramComplete(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.allowMethods(w, r, http.MethodPost) {
		return
	}
	if !s.requireJSONContentType(w, r) {
		return
	}
	if ok, retryAfter := s.authCompleteRate.allow(authRateLimitKey(r), now); !ok {
		setAuthRetryAfter(w, retryAfter)
		s.writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var body struct {
		IDToken    string         `json:"idToken"`
		InitData   string         `json:"initData"`
		WidgetAuth map[string]any `json:"widgetAuth"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	var auth telegramAuth
	var err error
	switch {
	case strings.TrimSpace(body.IDToken) != "":
		nonceCookie, cookieErr := r.Cookie(loginNonceCookieName)
		if cookieErr != nil {
			s.writeError(w, http.StatusUnauthorized, "missing login nonce")
			return
		}
		loginNonce, parseErr := parseLoginNonce(s.sessionSecret, nonceCookie.Value, now)
		if parseErr != nil {
			s.writeError(w, http.StatusUnauthorized, "invalid login nonce")
			return
		}
		if s.telegramLogin == nil {
			s.writeError(w, http.StatusServiceUnavailable, "Telegram Login is not configured")
			return
		}
		claims, verifyErr := s.telegramLogin.VerifyIDToken(r.Context(), strings.TrimSpace(body.IDToken), loginNonce.Nonce, now)
		if verifyErr != nil {
			s.writeError(w, http.StatusUnauthorized, "invalid Telegram login")
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
				LanguageCode: string(domain.DefaultLanguage),
			},
		}
	case len(body.WidgetAuth) > 0:
		s.writeError(w, http.StatusBadRequest, "legacy Telegram Login widget payloads are not supported")
		return
	case strings.TrimSpace(body.InitData) != "":
		auth, err = s.initDataAuthFromPayload(body.InitData, now)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "invalid Telegram login")
			return
		}
	default:
		s.writeError(w, http.StatusBadRequest, "missing Telegram login payload")
		return
	}
	if err := verifyTelegramAuthAge(auth, time.Duration(s.cfg.TrainWebTelegramAuthMaxAgeSec)*time.Second, now); err != nil {
		s.writeError(w, http.StatusUnauthorized, "invalid Telegram login")
		return
	}
	resolvedLanguage := s.resolveSignedInLanguage(r.Context(), auth.User.ID, auth.User.LanguageCode)
	auth.User.LanguageCode = string(resolvedLanguage)
	cookie, err := issueSessionCookie(s.sessionSecret, auth, now)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cookie.Path = s.cookiePath()
	http.SetCookie(w, cookie)
	http.SetCookie(w, clearLoginNonceCookie(s.cookiePath()))
	payload, err := s.authPayload(auth, resolvedLanguage, now)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleAuthTelegram(w http.ResponseWriter, r *http.Request, _ time.Time) {
	if !s.allowMethods(w, r, http.MethodPost) {
		return
	}
	s.writeRetired(w, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete.")
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request, _ time.Time) {
	if !s.allowMethods(w, r, http.MethodPost) {
		return
	}
	http.SetCookie(w, clearSessionCookie(s.cookiePath()))
	http.SetCookie(w, clearLoginNonceCookie(s.cookiePath()))
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthTest(w http.ResponseWriter, r *http.Request, now time.Time) {
	if s.testLogin == nil {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !s.allowMethods(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	_, meta, err := s.testLogin.Consume(body.Ticket, now)
	if err != nil {
		log.Printf(
			"train web test auth rejected user=%d nonce=%s expires_at=%s err=%v",
			s.testLogin.userID,
			meta.NonceHash,
			meta.ExpiresAt.UTC().Format(time.RFC3339),
			err,
		)
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if s.app != nil {
		accepted, consumeErr := s.app.ConsumeTestLoginTicket(r.Context(), meta.NonceHash, s.testLogin.userID, meta.ExpiresAt)
		if consumeErr != nil {
			s.writeError(w, http.StatusInternalServerError, consumeErr.Error())
			return
		}
		if !accepted {
			reuseErr := errors.New("test login ticket already used")
			log.Printf(
				"train web test auth rejected user=%d nonce=%s expires_at=%s err=%v",
				s.testLogin.userID,
				meta.NonceHash,
				meta.ExpiresAt.UTC().Format(time.RFC3339),
				reuseErr,
			)
			s.writeError(w, http.StatusUnauthorized, reuseErr.Error())
			return
		}
	}
	if s.app != nil {
		if err := s.app.ResetTestUser(r.Context(), s.testLogin.userID); err != nil {
			s.writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	resolvedLanguage := domain.DefaultLanguage
	if s.app != nil {
		settings, err := s.app.UserSettings(r.Context(), s.testLogin.userID)
		if err != nil {
			log.Printf(
				"train web test auth: falling back to default language for user=%d after reset lookup failed: %v",
				s.testLogin.userID,
				err,
			)
		} else {
			resolvedLanguage = settings.Language
		}
	}
	nickname := domain.GenericNickname(s.testLogin.userID)
	if s.app != nil {
		nickname = s.app.Nickname(s.testLogin.userID)
	}
	auth := telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           s.testLogin.userID,
			FirstName:    nickname,
			LanguageCode: string(resolvedLanguage),
		},
	}
	if err := s.writeAuthenticatedSession(w, auth, resolvedLanguage, now); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf(
		"train web test auth accepted user=%d nonce=%s expires_at=%s",
		s.testLogin.userID,
		meta.NonceHash,
		meta.ExpiresAt.UTC().Format(time.RFC3339),
	)
}

func (s *Server) resolveSignedInLanguage(ctx context.Context, userID int64, preferred string) domain.Language {
	resolvedLanguage := trainapp.ParseLanguage(preferred)
	if s.app == nil {
		return resolvedLanguage
	}
	persistedLanguage, err := s.app.ResolveSignedInLanguage(ctx, userID, preferred)
	if err != nil {
		log.Printf(
			"train web auth: falling back to requested language for user=%d after settings lookup failed: %v",
			userID,
			err,
		)
		return resolvedLanguage
	}
	return persistedLanguage
}

func (s *Server) writeAuthenticatedSession(w http.ResponseWriter, auth telegramAuth, resolvedLanguage domain.Language, now time.Time) error {
	auth.User.LanguageCode = string(resolvedLanguage)
	cookie, err := issueSessionCookie(s.sessionSecret, auth, now)
	if err != nil {
		return err
	}
	cookie.Path = s.cookiePath()
	http.SetCookie(w, cookie)
	payload, err := s.authPayload(auth, resolvedLanguage, now)
	if err != nil {
		return err
	}
	s.writeJSON(w, http.StatusOK, payload)
	return nil
}

func (s *Server) authPayload(auth telegramAuth, resolvedLanguage domain.Language, now time.Time) (map[string]any, error) {
	payload := map[string]any{
		"ok":            true,
		"authenticated": true,
		"userId":        auth.User.ID,
		"stableUserId":  fmt.Sprintf("telegram:%d", auth.User.ID),
		"firstName":     strings.TrimSpace(auth.User.FirstName),
		"nickname":      s.nicknameForUser(auth.User.ID),
		"lang":          string(resolvedLanguage),
		"language":      string(resolvedLanguage),
		"baseUrl":       s.cfg.TrainWebPublicBaseURL,
	}
	if s.spacetime != nil {
		spacetimePayload, err := s.spacetimeSessionPayload(auth, now)
		if err != nil {
			return nil, err
		}
		payload["spacetime"] = spacetimePayload
	}
	return payload, nil
}

func (s *Server) spacetimeSessionPayload(auth telegramAuth, now time.Time) (map[string]any, error) {
	issueToken := s.spacetime.issueTelegramToken
	if s.spacetimeIssueToken != nil {
		issueToken = s.spacetimeIssueToken
	}
	token, err := issueToken(auth, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"enabled":   true,
		"host":      s.cfg.TrainWebSpacetimeHost,
		"database":  s.cfg.TrainWebSpacetimeDatabase,
		"token":     token.Token,
		"expiresAt": token.ExpiresAt.UTC().Format(time.RFC3339),
		"issuer":    s.spacetime.issuer,
		"audience":  s.spacetime.audience,
	}, nil
}

func (s *Server) handleAuthSpacetime(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	if s.spacetime == nil {
		s.writeJSON(w, http.StatusOK, map[string]any{
			"ok": true,
			"spacetime": map[string]any{
				"enabled": false,
			},
		})
		return
	}
	language := strings.TrimSpace(claims.Language)
	if language == "" {
		language = string(domain.DefaultLanguage)
	}
	auth := telegramAuth{
		AuthDate: time.Unix(claims.IssuedAt, 0).UTC(),
		User: telegramUser{
			ID:           claims.UserID,
			LanguageCode: language,
		},
	}
	payload, err := s.spacetimeSessionPayload(auth, now)
	if err != nil {
		log.Printf("train web Spacetime token issue failed: %v", err)
		s.writeError(w, http.StatusInternalServerError, "failed to issue token")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"spacetime": payload,
	})
}

func (s *Server) cookiePath() string {
	if s.pathPrefix != "" {
		return s.pathPrefix
	}
	return "/"
}

func (s *Server) telegramBotID() string {
	clientID := strings.TrimSpace(s.cfg.TrainWebTelegramClientID)
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
	publicURL := strings.TrimRight(strings.TrimSpace(s.cfg.TrainWebPublicBaseURL), "/")
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
	publicURL := strings.TrimSpace(s.cfg.TrainWebPublicBaseURL)
	if publicURL == "" {
		return ""
	}
	if strings.HasSuffix(publicURL, "/") {
		return publicURL
	}
	return publicURL + "/"
}

func (s *Server) initDataAuthFromPayload(initData string, now time.Time) (telegramAuth, error) {
	return telegramweb.ValidateInitData(
		strings.TrimSpace(initData),
		strings.TrimSpace(s.cfg.BotToken),
		time.Duration(s.cfg.TrainWebTelegramAuthMaxAgeSec)*time.Second,
		now,
	)
}

func (s *Server) nicknameForUser(userID int64) string {
	if s.app == nil {
		return domain.GenericNickname(userID)
	}
	return s.app.Nickname(userID)
}

func (s *Server) handleSpacetimeOpenIDConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.spacetime == nil {
		http.NotFound(w, r)
		return
	}
	s.writeJSON(w, http.StatusOK, s.spacetime.openIDConfiguration())
}

func (s *Server) handleSpacetimeJWKS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.spacetime == nil {
		http.NotFound(w, r)
		return
	}
	s.writeJSON(w, http.StatusOK, s.spacetime.jwks())
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	language := trainapp.ParseLanguage(claims.Language)
	settings, err := s.app.UserSettings(r.Context(), claims.UserID)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	language = settings.Language
	routeCheckIn, err := s.app.CurrentRouteCheckIn(r.Context(), claims.UserID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"userId":        claims.UserID,
		"stableUserId":  fmt.Sprintf("telegram:%d", claims.UserID),
		"nickname":      s.nicknameForUser(claims.UserID),
		"lang":          string(language),
		"language":      string(language),
		"baseUrl":       s.cfg.TrainWebPublicBaseURL,
		"settings":      settings,
		"routeCheckIn":  routeCheckIn,
		"schedule":      s.appScheduleContext(now),
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if claims, ok := s.optionalSession(r, now); ok {
		s.handleMe(w, r, claims, now)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": false,
		"baseUrl":       s.cfg.TrainWebPublicBaseURL,
		"schedule":      s.appScheduleContext(now),
	})
}

func (s *Server) handleIncidentVote(w http.ResponseWriter, r *http.Request, claims sessionClaims, incidentID string, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	value, ok := domain.ParseIncidentVoteValue(body.Value)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "invalid vote value")
		return
	}
	summary, err := s.app.VoteIncident(r.Context(), incidentID, claims.UserID, value, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if s.publicEdgeCache != nil {
		s.publicEdgeCache.noteIncidentUpdated(incidentID)
	}
	s.writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleIncidentComment(w http.ResponseWriter, r *http.Request, claims sessionClaims, incidentID string, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Body string `json:"body"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	comment, err := s.app.AddIncidentComment(r.Context(), incidentID, claims.UserID, body.Body, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if s.publicEdgeCache != nil {
		s.publicEdgeCache.noteIncidentUpdated(incidentID)
	}
	s.writeJSON(w, http.StatusOK, comment)
}

func (s *Server) handleWindowTrains(w http.ResponseWriter, r *http.Request, claims sessionClaims, windowID string, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.app.WindowTrains(r.Context(), claims.UserID, now, strings.TrimSpace(windowID))
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"trains":   items,
		"schedule": s.appScheduleContext(now),
	})
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

func (s *Server) handleStations(w http.ResponseWriter, r *http.Request, _ sessionClaims, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.app.Stations(r.Context(), now, r.URL.Query().Get("q"))
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"stations": items,
		"schedule": s.appScheduleContext(now),
	})
}

func (s *Server) handleStationSightingDestinations(w http.ResponseWriter, r *http.Request, _ sessionClaims, stationID string, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.app.StationSightingDestinations(r.Context(), now, stationID)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"stations": items,
		"schedule": s.appScheduleContext(now),
	})
}

func (s *Server) handleStationDepartures(w http.ResponseWriter, r *http.Request, claims sessionClaims, stationID string, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	items, err := s.app.StationDepartures(r.Context(), claims.UserID, now, stationID, 2*time.Hour, 2*time.Hour)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, s.withSchedulePayload(items, now))
}

func (s *Server) handleStationSighting(w http.ResponseWriter, r *http.Request, claims sessionClaims, stationID string, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		DestinationStationID string `json:"destinationStationId"`
		TrainID              string `json:"trainId"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	var destinationStationID *string
	if trimmed := strings.TrimSpace(body.DestinationStationID); trimmed != "" {
		destinationStationID = &trimmed
	}
	var trainID *string
	if trimmed := strings.TrimSpace(body.TrainID); trimmed != "" {
		trainID = &trimmed
	}
	result, err := s.app.SubmitStationSighting(r.Context(), claims.UserID, stationID, destinationStationID, trainID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if s.publicEdgeCache != nil && result.Accepted && result.Event != nil {
		matchedTrainID := ""
		if result.Event.MatchedTrainInstanceID != nil {
			matchedTrainID = strings.TrimSpace(*result.Event.MatchedTrainInstanceID)
		}
		s.publicEdgeCache.noteStationSightingAccepted(stationID, matchedTrainID, result.IncidentID)
	}
	if result.Accepted && result.Event != nil && s.notifier != nil {
		if err := s.notifier.NotifyStationSighting(r.Context(), *result.Event, now); err != nil {
			s.writeAppError(w, err)
			return
		}
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStationReport(w http.ResponseWriter, r *http.Request, claims sessionClaims, stationID string, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	result, err := s.app.SubmitStationReport(r.Context(), claims.UserID, stationID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if s.publicEdgeCache != nil && result.Accepted {
		s.publicEdgeCache.noteIncidentUpdated(result.IncidentID)
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleLocationReport(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		RadiusMeters int     `json:"radiusMeters"`
		Description  string  `json:"description"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	description := strings.Join(strings.Fields(strings.TrimSpace(body.Description)), " ")
	if description == "" {
		s.writeError(w, http.StatusBadRequest, "description is required")
		return
	}
	if len([]rune(description)) > reports.MaxLocationReportDescriptionLen {
		s.writeError(w, http.StatusBadRequest, "description is too long")
		return
	}
	if body.Latitude < -90 || body.Latitude > 90 || body.Longitude < -180 || body.Longitude > 180 {
		s.writeError(w, http.StatusBadRequest, "invalid coordinates")
		return
	}
	result, err := s.app.SubmitAreaReport(r.Context(), claims.UserID, body.Latitude, body.Longitude, body.RadiusMeters, description, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if s.publicEdgeCache != nil && result.Accepted {
		s.publicEdgeCache.noteIncidentUpdated(result.IncidentID)
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRouteDestinations(w http.ResponseWriter, r *http.Request, _ sessionClaims, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	originID := strings.TrimSpace(r.URL.Query().Get("originStationId"))
	if originID == "" {
		s.writeError(w, http.StatusBadRequest, "originStationId is required")
		return
	}
	items, err := s.app.ReachableDestinations(r.Context(), now, originID, r.URL.Query().Get("q"))
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"stations": items,
		"schedule": s.appScheduleContext(now),
	})
}

func (s *Server) handleRouteTrains(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	originID := strings.TrimSpace(r.URL.Query().Get("originStationId"))
	destinationID := strings.TrimSpace(r.URL.Query().Get("destinationStationId"))
	if originID == "" || destinationID == "" {
		s.writeError(w, http.StatusBadRequest, "originStationId and destinationStationId are required")
		return
	}
	items, err := s.app.RouteTrains(r.Context(), claims.UserID, now, originID, destinationID, 18*time.Hour)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"trains":   items,
		"schedule": s.appScheduleContext(now),
	})
}

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request, claims sessionClaims) {
	switch r.Method {
	case http.MethodGet:
		items, err := s.app.FavoriteRoutes(r.Context(), claims.UserID)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"favorites": items})
	case http.MethodPut:
		var body struct {
			FromStationID string `json:"fromStationId"`
			ToStationID   string `json:"toStationId"`
		}
		if !s.decodeJSON(w, r, &body) {
			return
		}
		if err := s.app.SaveFavoriteRoute(r.Context(), claims.UserID, strings.TrimSpace(body.FromStationID), strings.TrimSpace(body.ToStationID)); err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		var body struct {
			FromStationID string `json:"fromStationId"`
			ToStationID   string `json:"toStationId"`
		}
		if !s.decodeJSON(w, r, &body) {
			return
		}
		if err := s.app.DeleteFavoriteRoute(r.Context(), claims.UserID, strings.TrimSpace(body.FromStationID), strings.TrimSpace(body.ToStationID)); err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCurrentCheckIn(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	switch r.Method {
	case http.MethodGet:
		item, err := s.app.CurrentRide(r.Context(), claims.UserID, now)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"currentRide": item})
	case http.MethodPut:
		var body struct {
			TrainID           string `json:"trainId"`
			BoardingStationID string `json:"boardingStationId"`
			Source            string `json:"source"`
		}
		if !s.decodeJSON(w, r, &body) {
			return
		}
		var stationID *string
		if trimmed := strings.TrimSpace(body.BoardingStationID); trimmed != "" {
			stationID = &trimmed
		}
		checkInSource := strings.ToLower(strings.TrimSpace(body.Source))
		var err error
		if checkInSource == "map" {
			err = s.app.CheckInMap(r.Context(), claims.UserID, strings.TrimSpace(body.TrainID), stationID, now)
		} else {
			err = s.app.CheckIn(r.Context(), claims.UserID, strings.TrimSpace(body.TrainID), stationID, now)
		}
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		item, err := s.app.CurrentRide(r.Context(), claims.UserID, now)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"currentRide": item})
	case http.MethodDelete:
		if err := s.app.Checkout(r.Context(), claims.UserID, now); err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleCurrentRouteCheckIn(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	switch r.Method {
	case http.MethodGet:
		item, err := s.app.CurrentRouteCheckIn(r.Context(), claims.UserID, now)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, routeCheckInPayload(item, now))
	case http.MethodPost:
		var body struct {
			RouteID         string `json:"routeId"`
			DurationMinutes int    `json:"durationMinutes"`
		}
		if !s.decodeJSON(w, r, &body) {
			return
		}
		item, err := s.app.StartRouteCheckIn(r.Context(), claims.UserID, body.RouteID, body.DurationMinutes, now)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, routeCheckInPayload(item, now))
	case http.MethodDelete:
		if err := s.app.CheckoutRouteCheckIn(r.Context(), claims.UserID); err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, routeCheckInPayload(nil, now))
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func routeCheckInPayload(item *domain.RouteCheckIn, now time.Time) map[string]any {
	payload := map[string]any{
		"routeCheckIn":           item,
		"defaultDurationMinutes": trainapp.RouteCheckInDefaultMinutes,
		"minDurationMinutes":     trainapp.RouteCheckInMinMinutes,
		"maxDurationMinutes":     trainapp.RouteCheckInMaxMinutes,
	}
	if item != nil {
		remaining := item.ExpiresAt.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		payload["remainingSeconds"] = int(remaining.Seconds())
	}
	return payload
}

func (s *Server) handleUndoCheckOut(w http.ResponseWriter, r *http.Request, claims sessionClaims, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ok, err := s.app.UndoCheckout(r.Context(), claims.UserID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"restored": ok})
}

func (s *Server) handleTrainStatus(w http.ResponseWriter, r *http.Request, claims sessionClaims, trainID string, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, err := s.app.TrainStatus(r.Context(), claims.UserID, trainID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, s.withSchedulePayload(item, now))
}

func (s *Server) handleTrainStops(w http.ResponseWriter, r *http.Request, claims sessionClaims, trainID string, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, err := s.app.TrainStops(r.Context(), claims.UserID, now, trainID)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, s.withSchedulePayload(item, now))
}

func (s *Server) handleTrainTimeline(w http.ResponseWriter, r *http.Request, claims sessionClaims, trainID string, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, err := s.app.TrainStatus(r.Context(), claims.UserID, trainID, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"timeline": item.Timeline,
		"schedule": s.appScheduleContext(now),
	})
}

func (s *Server) handleTrainReport(w http.ResponseWriter, r *http.Request, claims sessionClaims, trainID string, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Signal string `json:"signal"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	signal, ok := reports.ParseSignal(body.Signal)
	if !ok {
		s.writeError(w, http.StatusBadRequest, "unsupported report signal")
		return
	}
	result, err := s.app.SubmitReport(r.Context(), claims.UserID, trainID, signal, now)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	if s.publicEdgeCache != nil && result.Accepted {
		s.publicEdgeCache.noteReportAccepted(trainID, result.IncidentID)
	}
	if result.Accepted && s.notifier != nil {
		if err := s.notifier.NotifyRideUsers(r.Context(), claims.UserID, trainID, signal, now); err != nil {
			s.writeAppError(w, err)
			return
		}
	}
	s.writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleTrainMute(w http.ResponseWriter, r *http.Request, claims sessionClaims, trainID string, now time.Time) {
	if r.Method != http.MethodPut {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		DurationMinutes int `json:"durationMinutes"`
	}
	if !s.decodeJSON(w, r, &body) {
		return
	}
	if body.DurationMinutes <= 0 {
		body.DurationMinutes = 30
	}
	if err := s.app.MuteTrain(r.Context(), claims.UserID, trainID, now, time.Duration(body.DurationMinutes)*time.Minute); err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request, claims sessionClaims) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.app.UserSettings(r.Context(), claims.UserID)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, settings)
	case http.MethodPatch:
		var body struct {
			AlertsEnabled *bool  `json:"alertsEnabled"`
			AlertStyle    string `json:"alertStyle"`
			Language      string `json:"language"`
		}
		if !s.decodeJSON(w, r, &body) {
			return
		}
		if body.AlertsEnabled != nil {
			if err := s.app.SetAlertsEnabled(r.Context(), claims.UserID, *body.AlertsEnabled); err != nil {
				s.writeAppError(w, err)
				return
			}
		}
		if strings.TrimSpace(body.AlertStyle) != "" {
			style, err := trainapp.ParseAlertStyle(body.AlertStyle)
			if err != nil {
				s.writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := s.app.SetAlertStyle(r.Context(), claims.UserID, style); err != nil {
				s.writeAppError(w, err)
				return
			}
		}
		if strings.TrimSpace(body.Language) != "" {
			if err := s.app.SetLanguage(r.Context(), claims.UserID, trainapp.ParseLanguage(body.Language)); err != nil {
				s.writeAppError(w, err)
				return
			}
		}
		settings, err := s.app.UserSettings(r.Context(), claims.UserID)
		if err != nil {
			s.writeAppError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, settings)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) requireSession(w http.ResponseWriter, r *http.Request, now time.Time) (sessionClaims, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "missing session")
		return sessionClaims{}, false
	}
	claims, err := parseSession(s.sessionSecret, cookie.Value, now)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "invalid session")
		return sessionClaims{}, false
	}
	return claims, true
}

func (s *Server) serveShell(w http.ResponseWriter, status int, data pageData) {
	scriptNonce, err := newCSPNonce()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "failed to prepare page")
		return
	}
	data.ScriptNonce = scriptNonce
	s.setShellSecurityHeaders(w, scriptNonce, data.TelegramWebAppScript)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.setNoStoreHeaders(w)
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	s.setNoIndexHeaders(w)
	w.WriteHeader(status)
	_ = s.pageTemplate.Execute(w, data)
}

func (s *Server) serveShellPage(w http.ResponseWriter, r *http.Request, status int, data pageData) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	s.serveShell(w, status, data)
}

func (s *Server) allowMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, method := range methods {
		if r.Method == method {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
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

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *Server) requireJSONContentType(w http.ResponseWriter, r *http.Request) bool {
	contentType := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if strings.EqualFold(contentType, "application/json") {
		return true
	}
	s.writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
	return false
}

func (s *Server) writeAppError(w http.ResponseWriter, err error) {
	var rateLimitErr *reports.RateLimitError
	switch {
	case errors.As(err, &rateLimitErr):
		if rateLimitErr.Remaining > 0 {
			w.Header().Set("Retry-After", strconv.FormatInt(int64(rateLimitErr.Remaining.Round(time.Second).Seconds()), 10))
		}
		s.writeError(w, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, trainapp.ErrNotFound):
		s.writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, trainapp.ErrCheckInUnavailable):
		s.writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, schedule.ErrUnavailable):
		s.writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		s.writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (s *Server) writeError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]any{"error": strings.TrimSpace(message)})
}

func (s *Server) writeDeferred(w http.ResponseWriter, message string) {
	s.writeJSON(w, http.StatusGone, map[string]any{
		"error":   "temporarily unavailable",
		"message": strings.TrimSpace(message),
	})
}

func (s *Server) writeRetired(w http.ResponseWriter, message string) {
	s.writeJSON(w, http.StatusGone, map[string]any{
		"error":   "removed",
		"message": strings.TrimSpace(message),
	})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Vary", "Accept-Encoding")
	s.setNoStoreHeaders(w)
	s.setNoIndexHeaders(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) newPageData(basePath string, mode string, trainID string) pageData {
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().In(s.loc)
	scheduleJSON := mustTemplateJSON(s.appScheduleContext(now))
	bundleManifestURL := ""
	bundleVersion := ""
	bundleServiceDate := ""
	bundleGeneratedAt := ""
	bundleTransformVersion := ""
	graphURL := ""
	if s.bundleStore != nil {
		bundleManifestURL = s.bundleStore.bundleAssetURL(basePath)
		if metadata, err := s.bundleStore.bundleMetadata(); err == nil && metadata != nil {
			bundleVersion = metadata.Version
			bundleServiceDate = metadata.ServiceDate
			bundleGeneratedAt = metadata.GeneratedAt
			bundleTransformVersion = metadata.TransformVersion
		}
		if manifest, err := s.bundleStore.activeManifest(); err == nil && manifest != nil {
			graphURL = strings.TrimRight(basePath, "/") + "/assets/" + filepath.ToSlash(filepath.Join("bundles", manifest.Version, manifest.Slices.TrainGraph))
		}
	}
	return pageData{
		BasePath:                basePath,
		PublicBaseURL:           s.cfg.TrainWebPublicBaseURL,
		Mode:                    mode,
		TrainID:                 trainID,
		HTMLLang:                strings.ToLower(string(domain.DefaultLanguage)),
		StationCheckin:          s.app.StationCheckinEnabled(),
		MiniAppRefreshMs:        30_000,
		PublicRefreshMs:         60_000,
		ExternalTrainMapEnabled: s.cfg.ExternalTrainMapEnabled,
		ExternalTrainMapBaseURL: s.cfg.ExternalTrainMapBaseURL,
		ExternalTrainMapWsURL:   s.cfg.ExternalTrainMapWsURL,
		ExternalTrainGraphURL:   graphURL,
		SpacetimeHost:           "",
		SpacetimeDatabase:       "",
		PublicEdgeCacheEnabled:  s.cfg.TrainWebPublicEdgeCacheEnabled,
		BundleManifestURL:       bundleManifestURL,
		BundleVersion:           bundleVersion,
		BundleServiceDate:       bundleServiceDate,
		BundleGeneratedAt:       bundleGeneratedAt,
		BundleTransformVersion:  bundleTransformVersion,
		ScheduleJSON:            scheduleJSON,
		TelegramWebAppScript:    mode == "mini-app",
		AppCSSURL:               s.release.AssetURL(basePath, "app.css"),
		LeafletCSSURL:           s.release.AssetURL(basePath, "vendor/leaflet.css"),
		LeafletJSURL:            s.release.AssetURL(basePath, "vendor/leaflet.js"),
		ExternalFeedJSURL:       s.release.AssetURL(basePath, "external-feed.js"),
		AppJSURL:                s.release.AssetURL(basePath, "app.js"),
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	assetPath := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/assets/")
	if excludedTrainPublicAsset(assetPath) || !s.allowAssetQuery(r, assetPath) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setNoIndexHeaders(w)
	if s.bundleStore != nil && strings.HasPrefix(assetPath, "bundles/") {
		s.serveBundleAsset(w, r, strings.TrimPrefix(assetPath, "bundles/"))
		return
	}
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

func (s *Server) serveBundleAsset(w http.ResponseWriter, r *http.Request, relativePath string) {
	if !s.allowMethods(w, r, http.MethodGet, http.MethodHead) {
		return
	}
	cleanRelativePath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+relativePath)), "/")
	if cleanRelativePath == "" || cleanRelativePath == "." || strings.HasPrefix(cleanRelativePath, "..") || excludedGenericPublicAsset(cleanRelativePath) {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if s.bundleStore == nil || strings.TrimSpace(s.bundleStore.dir) == "" {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	assetPath := filepath.Join(s.bundleStore.dir, cleanRelativePath)
	info, err := os.Stat(assetPath)
	if err != nil || info.IsDir() {
		s.setNoStoreHeaders(w)
		s.setNoIndexHeaders(w)
		http.NotFound(w, r)
		return
	}
	if cleanRelativePath == "active.json" {
		s.setNoStoreHeaders(w)
	} else {
		s.setImmutableHeaders(w)
	}
	s.setNoIndexHeaders(w)
	w.Header().Set("Vary", "Accept-Encoding")
	http.ServeFile(w, r, assetPath)
}

func excludedTrainPublicAsset(assetPath string) bool {
	cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+assetPath)), "/")
	return cleanPath == "live-client.js" || excludedGenericPublicAsset(cleanPath)
}

func excludedGenericPublicAsset(assetPath string) bool {
	cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+assetPath)), "/")
	name := strings.ToLower(filepath.Base(cleanPath))
	return strings.HasSuffix(name, ".test.js") || strings.HasSuffix(name, ".test.css") || strings.HasSuffix(name, ".map")
}

func (s *Server) setReleaseHeaders(w http.ResponseWriter) {
	s.setSecurityHeaders(w)
}

func (s *Server) setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy("", false))
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), payment=(), usb=(), geolocation=(self)")
}

func (s *Server) setShellSecurityHeaders(w http.ResponseWriter, scriptNonce string, allowTelegramScript bool) {
	s.setSecurityHeaders(w)
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(scriptNonce, allowTelegramScript))
}

func (s *Server) contentSecurityPolicy(scriptNonce string, allowTelegramScript bool) string {
	scriptSources := []string{"'self'"}
	frameSources := []string{"'none'"}
	connectSources := []string{"'self'"}
	if s.cfg.ExternalTrainMapEnabled {
		addCSPURLSource(&connectSources, s.cfg.ExternalTrainMapBaseURL)
		addCSPURLSource(&connectSources, s.cfg.ExternalTrainMapWsURL)
	}
	addCSPURLSource(&connectSources, s.cfg.TrainWebSpacetimeHost)
	if strings.TrimSpace(scriptNonce) != "" {
		scriptSources = append(scriptSources, "'nonce-"+strings.TrimSpace(scriptNonce)+"'")
	}
	if allowTelegramScript {
		scriptSources = append(scriptSources, "https://telegram.org")
		frameSources = []string{"https://telegram.org"}
	}
	return strings.Join([]string{
		"default-src 'self'",
		"base-uri 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"script-src " + strings.Join(scriptSources, " "),
		"style-src 'self'",
		"img-src 'self' data: https://*.tile.openstreetmap.org",
		"connect-src " + strings.Join(connectSources, " "),
		"frame-src " + strings.Join(frameSources, " "),
	}, "; ")
}

func addCSPURLSource(sources *[]string, raw string) {
	if sources == nil {
		return
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "wss" {
		return
	}
	source := scheme + "://" + strings.ToLower(parsed.Host)
	for _, existing := range *sources {
		if existing == source {
			return
		}
	}
	*sources = append(*sources, source)
}

func newCSPNonce() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate CSP nonce: %w", err)
	}
	return base64.RawStdEncoding.EncodeToString(raw[:]), nil
}

func (s *Server) setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
}

func (s *Server) setNoIndexHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Robots-Tag", "noindex, noarchive")
}

func (s *Server) setImmutableHeaders(w http.ResponseWriter) {
	s.setImmutableHeadersWithTTL(w, 31536000)
}

func (s *Server) setPublicCacheHeadersWithTTL(w http.ResponseWriter, ttlSec int) {
	if ttlSec <= 0 {
		ttlSec = 60
	}
	value := fmt.Sprintf("public, max-age=%d", ttlSec)
	w.Header().Set("Cache-Control", value)
	w.Header().Set("CDN-Cache-Control", value)
}

func (s *Server) setImmutableHeadersWithTTL(w http.ResponseWriter, ttlSec int) {
	value := fmt.Sprintf("public, max-age=%d, immutable", ttlSec)
	w.Header().Set("Cache-Control", value)
	w.Header().Set("CDN-Cache-Control", value)
	w.Header().Set("Cloudflare-CDN-Cache-Control", value)
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

func (s *Server) bundleData() (*staticBundleData, bool, error) {
	if s.bundleStore == nil {
		return nil, false, nil
	}
	data, err := s.bundleStore.loadData()
	if err != nil {
		return nil, false, err
	}
	if data == nil {
		return nil, false, nil
	}
	return data, true, nil
}

func (s *Server) bundlePublicDashboard(now time.Time, limit int) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	return data.publicDashboard(now, limit), true, nil
}

func (s *Server) bundlePublicServiceDayTrains(now time.Time) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	return data.publicServiceDayTrains(now), true, nil
}

func (s *Server) bundlePublicNetworkMap(ctx context.Context, now time.Time) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	payload := data.publicNetworkMap(now)
	live, err := s.app.NetworkMap(ctx, now)
	if err != nil {
		if payload["recentSightings"] == nil {
			payload["recentSightings"] = []domain.StationSighting{}
		}
		if payload["sameDaySightings"] == nil {
			payload["sameDaySightings"] = []domain.StationSighting{}
		}
		return payload, true, nil
	}
	if live != nil {
		recentSightings := live.RecentSightings
		if recentSightings == nil {
			recentSightings = []domain.StationSighting{}
		}
		sameDaySightings := live.SameDaySightings
		if sameDaySightings == nil {
			sameDaySightings = []domain.StationSighting{}
		}
		payload["recentSightings"] = recentSightings
		payload["sameDaySightings"] = sameDaySightings
	}
	return payload, true, nil
}

func (s *Server) bundlePublicStations(now time.Time, query string) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	return data.searchStations(now, query), true, nil
}

func (s *Server) bundlePublicStationDepartures(now time.Time, stationID string) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	payload := data.publicStationDepartures(now, stationID, 8)
	if payload == nil {
		return nil, false, nil
	}
	return payload, true, nil
}

func (s *Server) bundlePublicTrain(now time.Time, trainID string) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	payload := data.publicTrain(now, trainID)
	if payload == nil {
		return nil, false, nil
	}
	return payload, true, nil
}

func (s *Server) bundlePublicTrainStops(now time.Time, trainID string) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	payload := data.trainStops(now, trainID)
	if payload == nil {
		return nil, false, nil
	}
	return payload, true, nil
}

func (s *Server) appScheduleAvailability() (bool, error) {
	return s.app.ScheduleAvailability()
}

func (s *Server) appScheduleContext(now time.Time) schedule.AccessContext {
	return s.app.ScheduleContext(now)
}

func (s *Server) appLoadedServiceDate() string {
	return s.app.LoadedServiceDate()
}

func mustTemplateJSON(value any) template.JS {
	body, err := json.Marshal(value)
	if err != nil {
		return template.JS(`null`)
	}
	return template.JS(body)
}

func (s *Server) withSchedulePayload(item any, now time.Time) map[string]any {
	payload := map[string]any{}
	raw, err := json.Marshal(item)
	if err == nil {
		_ = json.Unmarshal(raw, &payload)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["schedule"] = s.appScheduleContext(now)
	return payload
}

func (s *Server) applyBundleTrainCardRiderCount(ctx context.Context, payload map[string]any, trainID string, now time.Time) {
	if s == nil || s.app == nil || payload == nil {
		return
	}
	users, err := s.app.ListActiveCheckinUsers(ctx, strings.TrimSpace(trainID), now)
	if err != nil {
		return
	}
	switch card := payload["trainCard"].(type) {
	case map[string]any:
		if card != nil {
			card["riders"] = trainapp.PublicRiderCount(len(users))
		}
	case trainapp.TrainCard:
		card.Riders = trainapp.PublicRiderCount(len(users))
		payload["trainCard"] = card
	}
}

func (s *Server) healthPayload(now time.Time, liveness bool) map[string]any {
	scheduleAvailable, scheduleErr := s.appScheduleAvailability()
	scheduleCtx := s.appScheduleContext(now)
	loadedServiceDate := s.appLoadedServiceDate()
	ready, readinessReason := scheduleReadiness(scheduleCtx, loadedServiceDate, scheduleErr)
	payload := map[string]any{
		"ok":                     liveness || ready,
		"ready":                  ready,
		"readinessReason":        readinessReason,
		"now":                    now.UTC().Format(time.RFC3339),
		"scheduleAvailable":      scheduleCtx.Available,
		"loadedServiceDate":      loadedServiceDate,
		"schedule":               scheduleCtx,
		"scheduleFallbackActive": scheduleCtx.FallbackActive,
		"scheduleSameDayFresh":   scheduleCtx.SameDayFresh,
		"version": map[string]any{
			"commit":       s.release.Commit,
			"buildTime":    s.release.BuildTime,
			"dirty":        s.release.Dirty,
			"releaseId":    s.release.ReleaseID,
			"sourceSha256": s.release.SourceSHA256,
		},
		"runtime": map[string]any{
			"instanceId": s.release.Instance,
		},
		"assets": map[string]any{
			"appJsSha256":  s.release.AppJSHash,
			"appCssSha256": s.release.AppCSSHash,
		},
	}
	if s.bundleStore != nil {
		if metadata, err := s.bundleStore.bundleMetadata(); err == nil && metadata != nil {
			payload["bundle"] = metadata
		}
	}
	if scheduleErr != nil {
		payload["scheduleError"] = scheduleErr.Error()
	}
	if !scheduleCtx.Available && scheduleAvailable && strings.TrimSpace(loadedServiceDate) != "" {
		payload["staleLoadedServiceDate"] = loadedServiceDate
	}
	return payload
}

func scheduleReadiness(scheduleCtx schedule.AccessContext, loadedServiceDate string, scheduleErr error) (bool, string) {
	switch {
	case scheduleCtx.SameDayFresh:
		return true, "same-day schedule loaded"
	case scheduleCtx.FallbackActive:
		return true, "previous-day fallback active before cutoff"
	case scheduleCtx.Available:
		return false, "schedule state is degraded"
	case strings.TrimSpace(loadedServiceDate) != "":
		return false, fmt.Sprintf("loaded schedule %s is outside the active service window", strings.TrimSpace(loadedServiceDate))
	case scheduleErr != nil:
		return false, scheduleErr.Error()
	default:
		return false, "schedule unavailable"
	}
}
