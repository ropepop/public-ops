package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
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

const smokeRequestHeader = "X-Satiksme-Smoke"

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

type Server struct {
	cfg            config.Config
	reports        *reports.Service
	catalog        CatalogReader
	store          store.Store
	dump           *bot.DumpDispatcher
	runtimeState   *runtime.State
	release        releaseInfo
	loc            *time.Location
	liveHTTPClient *http.Client
	pathPrefix     string
	sessionSecret  []byte
	spacetime      *spacetimeTokenIssuer
	telegramLogin  *telegramweb.LoginVerifier
	static         fs.FS
	pageTemplate   *template.Template
	bundleStore    *staticBundleStore
}

type pageData struct {
	AppCSSURL       string
	AppJSURL        string
	LiveClientJSURL string
	ConfigJS        template.JS
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
		cfg:            cfg,
		reports:        reportsSvc,
		catalog:        catalog,
		store:          st,
		dump:           dump,
		runtimeState:   runtimeState,
		release:        release,
		loc:            loc,
		liveHTTPClient: &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second},
		pathPrefix:     pathPrefix,
		static:         static,
		pageTemplate: template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="lv">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Kontrole</title>
  <link rel="stylesheet" href="{{.AppCSSURL}}">
  <link rel="stylesheet" href="https://unpkg.com/leaflet@1.9.4/dist/leaflet.css" crossorigin="">
  <script src="https://telegram.org/js/telegram-web-app.js"></script>
  <script>window.SATIKSME_APP_CONFIG = {{.ConfigJS}};</script>
  <script defer src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js" crossorigin=""></script>
  {{if .LiveClientJSURL}}<script defer src="{{.LiveClientJSURL}}"></script>{{end}}
  <script defer src="{{.AppJSURL}}"></script>
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
	return sub
}

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
	s.setReleaseHeaders(w)
	path := strings.TrimRight(r.URL.Path, "/")
	basePath := strings.TrimRight(s.pathPrefix, "/")
	switch {
	case path == strings.TrimRight(basePath+"/oidc/.well-known/openid-configuration", "/"):
		s.handleSpacetimeOpenIDConfiguration(w, r)
	case path == strings.TrimRight(basePath+"/oidc/jwks.json", "/"):
		s.handleSpacetimeJWKS(w, r)
	case path == basePath || path == "":
		s.serveShell(w, "public")
	case path == basePath+"/incidents":
		s.serveShell(w, "public-incidents")
	case path == basePath+"/-incidents":
		s.serveShell(w, "public-incidents")
	case path == basePath+"/app":
		s.serveShell(w, "public")
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
		http.NotFound(w, r)
	}
}

func (s *Server) serveShell(w http.ResponseWriter, mode string) {
	s.setNoStoreHeaders(w)
	basePath := strings.TrimRight(s.pathPrefix, "/")
	bundleActiveURL := basePath + "/bundles/active.json"
	if s.bundleStore == nil {
		bundleActiveURL = ""
	}
	browserSpacetimeEnabled := s.browserSpacetimeConfigured()
	browserDirectDataEnabled := s.browserDirectDataEnabled()
	liveSnapshotLookupEnabled := s.browserLiveSnapshotLookupEnabled()
	cfg := map[string]any{
		"basePath":          basePath,
		"publicBaseURL":     s.cfg.SatiksmeWebPublicBaseURL,
		"language":          defaultAppLanguage,
		"mode":              mode,
		"reportsChannelURL": s.cfg.ReportsChannelURL,
		"bundleActiveURL":   bundleActiveURL,
		"liveVehiclesURL":   basePath + "/api/v1/public/live-vehicles",
	}
	if browserSpacetimeEnabled {
		cfg["spacetimeEnabled"] = browserDirectDataEnabled
		cfg["spacetimeDirectOnly"] = browserDirectDataEnabled
		cfg["spacetimeHost"] = s.cfg.SatiksmeWebSpacetimeHost
		cfg["spacetimeDatabase"] = s.cfg.SatiksmeWebSpacetimeDatabase
		cfg["liveTransportRealtimeEnabled"] = browserDirectDataEnabled
		cfg["liveTransportSnapshotLookupEnabled"] = liveSnapshotLookupEnabled
	}
	raw, _ := json.Marshal(cfg)
	liveClientURL := ""
	if browserSpacetimeEnabled && s.release.LiveClientHash != "" {
		liveClientURL = s.release.AssetURL(basePath, "live-client.js")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.pageTemplate.Execute(w, pageData{
		AppCSSURL:       s.release.AssetURL(basePath, "app.css"),
		AppJSURL:        s.release.AssetURL(basePath, "app.js"),
		LiveClientJSURL: liveClientURL,
		ConfigJS:        template.JS(raw),
	})
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
	return s.browserSpacetimeConfigured() && s.release.LiveClientHash != ""
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	assetPath := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/assets/")
	version := strings.TrimSpace(r.URL.Query().Get("v"))
	if version != "" && version == s.release.AssetHash(assetPath) {
		s.setImmutableHeaders(w)
	} else {
		s.setNoStoreHeaders(w)
	}
	http.StripPrefix(basePath+"/assets/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, route string) {
	now := time.Now().In(s.loc)
	switch {
	case route == "/health":
		s.handleHealth(w, r)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	now := time.Now().UTC()
	catalogStatus := runtime.CatalogStatus{}
	if s.catalog != nil {
		catalogStatus = s.catalog.Status()
	}
	stale := s.catalogStale(catalogStatus, now)
	dbWritable, dbError := s.dbStatus(r.Context())
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
	if !dbWritable {
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

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       ok,
		"degraded": len(reasons) > 0,
		"reasons":  reasons,
		"version": map[string]any{
			"display":   version.Display(),
			"commit":    s.release.Commit,
			"buildTime": s.release.BuildTime,
			"dirty":     s.release.Dirty,
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
	})
}

func (s *Server) handlePublicCatalog(w http.ResponseWriter, r *http.Request) {
	s.setRevalidateHeaders(w)
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	if payloadReader, ok := s.catalog.(catalogPayloadReader); ok {
		if etag := payloadReader.CatalogETag(); etag != "" {
			if strings.TrimSpace(r.Header.Get("If-None-Match")) == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
		}
		if payload := payloadReader.CatalogJSON(); len(payload) > 0 {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
			return
		}
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (s *Server) handlePublicIncidents(w http.ResponseWriter, r *http.Request, now time.Time) {
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
	limit := parseIncidentLimit(r)
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	items, err := s.reports.ListActiveIncidents(r.Context(), catalog, now, viewerID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"generatedAt": now.UTC(),
		"incidents":   items,
	})
}

func (s *Server) handlePublicIncidentDetail(w http.ResponseWriter, r *http.Request, incidentID string, now time.Time) {
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
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	item, err := s.reports.IncidentDetail(r.Context(), catalog, incidentID, now, viewerID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handlePublicSightings(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	stopID := strings.TrimSpace(r.URL.Query().Get("stopId"))
	limit := parseSightingsLimit(r, 100)
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, stopID, now, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, visible)
}

func (s *Server) handlePublicMap(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, "", now, parseSightingsLimit(r, 300))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	stopIncidents, liveVehicles, err := s.publicMapState(r.Context(), catalog, visible, now, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.PublicMapPayload{
		GeneratedAt:   catalog.GeneratedAt,
		Stops:         catalog.Stops,
		Sightings:     visible,
		StopIncidents: stopIncidents,
		LiveVehicles:  liveVehicles,
	})
}

func (s *Server) handlePublicMapLive(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, "", now, parseSightingsLimit(r, 300))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	stopIncidents, liveVehicles, err := s.publicMapState(r.Context(), catalog, visible, now, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, model.PublicLiveMapPayload{
		GeneratedAt:   catalog.GeneratedAt,
		Sightings:     visible,
		StopIncidents: stopIncidents,
		LiveVehicles:  liveVehicles,
	})
}

func (s *Server) handlePublicLiveVehicles(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	visible, err := s.reports.VisibleSightings(r.Context(), catalog, "", now, parseSightingsLimit(r, 300))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	viewerID := int64(0)
	if claims, ok := s.optionalSession(r, now); ok {
		viewerID = claims.UserID
	}
	incidents, err := s.reports.ListMapVisibleIncidents(r.Context(), catalog, now, viewerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"liveVehicles": liveVehicles,
	})
}

func (s *Server) publicMapState(ctx context.Context, catalog *model.Catalog, visible model.VisibleSightings, now time.Time, viewerID int64) ([]model.IncidentSummary, []model.LiveVehicle, error) {
	incidents, err := s.reports.ListMapVisibleIncidents(ctx, catalog, now, viewerID)
	if err != nil {
		return nil, nil, err
	}
	stopIncidents := make([]model.IncidentSummary, 0, len(incidents))
	vehicleIncidents := make([]model.IncidentSummary, 0, len(incidents))
	for _, incident := range incidents {
		switch incident.Scope {
		case "stop":
			stopIncidents = append(stopIncidents, incident)
		case "vehicle":
			vehicleIncidents = append(vehicleIncidents, incident)
		}
	}
	liveVehicles, err := s.publicLiveVehicles(ctx, catalog, visible, now, vehicleIncidents)
	if err != nil {
		return stopIncidents, []model.LiveVehicle{}, nil
	}
	return stopIncidents, liveVehicles, nil
}

func (s *Server) publicLiveVehicles(ctx context.Context, catalog *model.Catalog, visible model.VisibleSightings, now time.Time, incidents []model.IncidentSummary) ([]model.LiveVehicle, error) {
	liveVehicles, err := live.FetchVehicles(ctx, s.liveHTTPClient, "", catalog, now)
	if err != nil {
		return nil, err
	}
	live.ApplyVehicleSightingCounts(liveVehicles, visible.VehicleSightings)
	live.ApplyVehicleIncidents(liveVehicles, incidents)
	return liveVehicles, nil
}

func (s *Server) handleAuthTelegramConfig(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if strings.TrimSpace(s.telegramBotID()) == "" || s.telegramLogin == nil {
		writeError(w, http.StatusServiceUnavailable, "Telegram Login is not configured")
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
		"ok":          true,
		"clientId":    s.telegramBotID(),
		"nonce":       nonceClaims.Nonce,
		"scopes":      []string{"profile"},
		"origin":      s.telegramOrigin(),
		"redirectUri": s.telegramRedirectURI(),
	})
}

func (s *Server) handleAuthTelegramComplete(w http.ResponseWriter, r *http.Request, now time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		IDToken    string         `json:"idToken"`
		InitData   string         `json:"initData"`
		WidgetAuth map[string]any `json:"widgetAuth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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
			writeError(w, http.StatusUnauthorized, verifyErr.Error())
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
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
	case initData != "":
		auth, err = s.initDataAuthFromPayload(initData, now)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "missing Telegram login payload")
		return
	}

	if err := verifyTelegramAuthAge(auth, time.Duration(s.cfg.SatiksmeWebTelegramAuthMaxAgeSec)*time.Second, now); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
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
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeError(w, http.StatusGone, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete")
}

func (s *Server) handleDeprecatedAuthTelegramCallback(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeError(w, http.StatusGone, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete")
}

func (s *Server) handleDeprecatedAuthTelegram(w http.ResponseWriter, r *http.Request) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeError(w, http.StatusGone, "Telegram browser login now uses /api/v1/auth/telegram/config and /api/v1/auth/telegram/complete")
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request, _ time.Time) {
	s.setNoStoreHeaders(w)
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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

func (s *Server) telegramCallbackURL() string {
	return strings.TrimRight(s.cfg.SatiksmeWebPublicBaseURL, "/") + "/api/v1/auth/telegram/callback"
}

func (s *Server) telegramCompleteURL() string {
	return strings.TrimRight(s.cfg.SatiksmeWebPublicBaseURL, "/") + "/api/v1/auth/telegram/complete"
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

func (s *Server) telegramLoginURL() (string, error) {
	botID := s.telegramBotID()
	origin := s.telegramOrigin()
	callbackURL := s.telegramCallbackURL()
	if botID == "" || origin == "" || callbackURL == "" {
		return "", errors.New("telegram login is not configured")
	}
	params := url.Values{}
	params.Set("bot_id", botID)
	params.Set("origin", origin)
	params.Set("embed", "1")
	params.Set("return_to", callbackURL)
	return (&url.URL{
		Scheme:   "https",
		Host:     "oauth.telegram.org",
		Path:     "/auth",
		RawQuery: params.Encode(),
	}).String(), nil
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

func (s *Server) initDataAuthFromPayload(initData string, now time.Time) (telegramAuth, error) {
	return telegramweb.ValidateInitData(
		strings.TrimSpace(initData),
		strings.TrimSpace(s.cfg.BotToken),
		time.Duration(s.cfg.SatiksmeWebTelegramAuthMaxAgeSec)*time.Second,
		now,
	)
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
	if s.bundleStore == nil {
		s.setNoStoreHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.bundleStore.invalidate()
	s.setNoStoreHeaders(w)
	http.ServeFile(w, r, filepath.Join(s.cfg.SatiksmeWebBundleDir, "active.json"))
}

func (s *Server) serveBundleAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	if s.bundleStore == nil {
		s.setNoStoreHeaders(w)
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/bundles/")
	clean := filepath.Clean(relative)
	if clean == "." || strings.HasPrefix(clean, "..") {
		s.setNoStoreHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setImmutableHeaders(w)
	http.ServeFile(w, r, filepath.Join(s.cfg.SatiksmeWebBundleDir, "bundles", clean))
}

func (s *Server) serveLiveSnapshotActive(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.cfg.SatiksmeWebLiveSnapshotDir) == "" {
		s.setNoStoreHeaders(w)
		http.NotFound(w, r)
		return
	}
	s.setNoStoreHeaders(w)
	http.ServeFile(w, r, filepath.Join(s.cfg.SatiksmeWebLiveSnapshotDir, "active.json"))
}

func (s *Server) serveLiveSnapshotAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	if strings.TrimSpace(s.cfg.SatiksmeWebLiveSnapshotDir) == "" {
		s.setNoStoreHeaders(w)
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/transport/live/")
	clean := filepath.Clean(relative)
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, string(filepath.Separator)+".") {
		s.setNoStoreHeaders(w)
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
		http.NotFound(w, r)
		return
	}
	s.setImmutableHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
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
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	catalog := s.catalog.Current()
	stop, ok := s.findStop(catalog, payload.StopID)
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown stop")
		return
	}
	options := reports.SubmitOptions{Hidden: isSmokeRequest(r)}
	result, item, err := s.reports.SubmitStopSightingWithOptions(r.Context(), claims.UserID, payload.StopID, now, options)
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
	limit := parseSightingsLimit(r, 100)
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
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	payload.StopID = ""
	if strings.TrimSpace(payload.Mode) == "" || strings.TrimSpace(payload.RouteLabel) == "" {
		writeError(w, http.StatusBadRequest, "mode and routeLabel are required")
		return
	}
	options := reports.SubmitOptions{Hidden: isSmokeRequest(r)}
	result, item, err := s.reports.SubmitVehicleSightingWithOptions(r.Context(), claims.UserID, payload, now, options)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if result.Accepted && s.dump != nil && item != nil && !item.Hidden {
		s.dump.EnqueueVehicle(item)
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
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
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
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	catalog := s.catalog.Current()
	if catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog unavailable")
		return
	}
	comment, err := s.reports.AddIncidentComment(r.Context(), catalog, incidentID, claims.UserID, payload.Body, now)
	if err != nil {
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

func isSmokeRequest(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(smokeRequestHeader)), "1")
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

func parseSightingsLimit(r *http.Request, fallback int) int {
	limit := fallback
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	return limit
}

func parseIncidentLimit(r *http.Request) int {
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 2000 {
			return parsed
		}
	}
	return 0
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

func (s *Server) setReleaseHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Satiksme-Bot-Commit", s.release.Commit)
	w.Header().Set("X-Satiksme-Bot-Build-Time", s.release.BuildTime)
	w.Header().Set("X-Satiksme-Bot-Instance", s.release.Instance)
	w.Header().Set("X-Satiksme-Bot-App-Js", s.release.AppJSHash)
	w.Header().Set("X-Satiksme-Bot-App-Css", s.release.AppCSSHash)
	w.Header().Set("X-Satiksme-Bot-Live-Client", s.release.LiveClientHash)
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
