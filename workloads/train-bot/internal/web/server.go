package web

import (
	"context"
	"embed"
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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/schedule"
)

//go:embed static/*
var staticFS embed.FS

type RideNotifier interface {
	NotifyRideUsers(ctx context.Context, reporterID int64, trainID string, signal domain.SignalType, now time.Time) error
	NotifyStationSighting(ctx context.Context, event domain.StationSighting, now time.Time) error
}

type Server struct {
	cfg             config.Config
	app             *trainapp.Service
	catalog         *i18n.Catalog
	loc             *time.Location
	now             func() time.Time
	pathPrefix      string
	sessionSecret   []byte
	testLogin       *testLoginBroker
	spacetime       *spacetimeTokenIssuer
	notifier        RideNotifier
	static          fs.FS
	release         releaseInfo
	pageTemplate    *template.Template
	publicEdgeCache *publicEdgeCache
	bundleStore     *staticBundleStore
}

type pageData struct {
	BasePath                string
	PublicBaseURL           string
	Mode                    string
	TrainID                 string
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
	BundleSourceVersion     string
	BundleTransformVersion  string
	ScheduleJSON            template.JS
	AppCSSURL               string
	LeafletCSSURL           string
	LeafletJSURL            string
	ExternalFeedJSURL       string
	AppJSURL                string
}

const (
	deferredMapMessage       = "Train map is temporarily unavailable while the simplified train app release is being rebuilt."
	retiredRideMessage       = "Ride check-in, saved routes, and personal ride tools were removed from the simplified train app."
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
		cfg:        cfg,
		app:        appSvc,
		catalog:    catalog,
		loc:        loc,
		now:        time.Now,
		pathPrefix: pathPrefix,
		static:     staticFiles,
		release:    release,
		pageTemplate: template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>vivi kontrole bot</title>
  <link rel="stylesheet" href="{{.AppCSSURL}}">
  <link rel="stylesheet" href="{{.LeafletCSSURL}}">
  <script>
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
        sourceVersion: {{.BundleSourceVersion}},
        transformVersion: {{.BundleTransformVersion}}
      },
      schedule: {{.ScheduleJSON}}
    };
  </script>
  <script src="https://telegram.org/js/telegram-web-app.js"></script>
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
	}
	if cfg.TrainWebTestLoginEnabled {
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

func mustStaticSubFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

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
	path := strings.TrimRight(r.URL.Path, "/")
	basePath := strings.TrimRight(s.pathPrefix, "/")
	s.setReleaseHeaders(w)
	switch {
	case path == strings.TrimRight(basePath+"/oidc/.well-known/openid-configuration", "/"):
		s.handleSpacetimeOpenIDConfiguration(w, r)
	case path == strings.TrimRight(basePath+"/oidc/jwks.json", "/"):
		s.handleSpacetimeJWKS(w, r)
	case path == basePath || path == "":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-incidents", ""))
	case path == basePath+"/app":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "mini-app", ""))
	case path == basePath+"/stations":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-stations", ""))
	case path == basePath+"/incidents":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-incidents", ""))
	case path == basePath+"/map":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-network-map", ""))
	case path == basePath+"/feed":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-dashboard", ""))
	case path == basePath+"/departures":
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-dashboard", ""))
	case strings.HasPrefix(path, basePath+"/t/") && strings.HasSuffix(path, "/map"):
		trainID := strings.TrimSuffix(strings.TrimPrefix(path, basePath+"/t/"), "/map")
		trainID = strings.Trim(trainID, "/")
		if trainID == "" || strings.Contains(trainID, "/") {
			http.NotFound(w, r)
			return
		}
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-map", trainID))
	case strings.HasPrefix(path, basePath+"/t/"):
		trainID := strings.TrimPrefix(path, basePath+"/t/")
		if trainID == "" || strings.Contains(trainID, "/") {
			http.NotFound(w, r)
			return
		}
		s.serveShell(w, http.StatusOK, s.newPageData(basePath, "public-train", trainID))
	case strings.HasPrefix(path, basePath+"/assets/"):
		s.serveAsset(w, r, basePath)
	case strings.HasPrefix(path, basePath+"/api/v1/"):
		s.handleAPI(w, r, strings.TrimPrefix(path, basePath+"/api/v1"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, route string) {
	nowFn := s.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().In(s.loc)
	switch {
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
	case route == "/auth/telegram":
		s.handleAuthTelegram(w, r, now)
	case route == "/auth/test":
		s.handleAuthTest(w, r, now)
	case route == "/me":
		claims, ok := s.requireSession(w, r, now)
		if !ok {
			return
		}
		s.handleMe(w, r, claims, now)
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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.writeJSON(w, http.StatusOK, s.healthPayload(now, true))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	payload := s.healthPayload(now, false)
	status := http.StatusOK
	if payload["ready"] != true {
		status = http.StatusServiceUnavailable
	}
	s.writeJSON(w, status, payload)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	lang := trainapp.ParseLanguage(r.URL.Query().Get("lang"))
	decision, handled := s.beginPublicEdgeCache(w, r, s.now(), publicEdgeCacheMessagesRoute(string(lang)))
	if handled {
		return
	}
	s.writePublicJSON(w, http.StatusOK, map[string]any{
		"lang":     lang,
		"messages": s.catalog.Messages(lang),
	}, decision)
}

func (s *Server) handlePublicIncidents(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 60
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			s.writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := 60
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			s.writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	decision, handled := s.beginPublicEdgeCache(w, r, now, publicEdgeCacheNetworkMapRoute())
	if handled {
		return
	}
	if payload, ok, err := s.bundlePublicNetworkMap(now); err != nil {
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
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

func (s *Server) handleAuthTelegram(w http.ResponseWriter, r *http.Request, now time.Time) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		InitData string `json:"initData"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	auth, err := validateTelegramInitData(body.InitData, s.cfg.BotToken, time.Duration(s.cfg.TrainWebTelegramAuthMaxAgeSec)*time.Second, now)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	resolvedLanguage := s.resolveSignedInLanguage(r.Context(), auth.User.ID, auth.User.LanguageCode)
	if err := s.writeAuthenticatedSession(w, auth, resolvedLanguage, now); err != nil {
		s.writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
}

func (s *Server) handleAuthTest(w http.ResponseWriter, r *http.Request, now time.Time) {
	if s.testLogin == nil {
		s.writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
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
	resolvedLanguage := domain.LanguageEN
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
	if s.pathPrefix != "" {
		cookie.Path = s.pathPrefix
	}
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
		"ok":           true,
		"userId":       auth.User.ID,
		"stableUserId": fmt.Sprintf("telegram:%d", auth.User.ID),
		"lang":         string(resolvedLanguage),
		"baseUrl":      s.cfg.TrainWebPublicBaseURL,
	}
	if s.spacetime != nil {
		token, err := s.spacetime.issueTelegramToken(auth, now)
		if err != nil {
			return nil, err
		}
		payload["spacetime"] = map[string]any{
			"enabled":   true,
			"host":      s.cfg.TrainWebSpacetimeHost,
			"database":  s.cfg.TrainWebSpacetimeDatabase,
			"token":     token.Token,
			"expiresAt": token.ExpiresAt.UTC().Format(time.RFC3339),
			"issuer":    s.spacetime.issuer,
			"audience":  s.spacetime.audience,
		}
	}
	return payload, nil
}

func (s *Server) handleSpacetimeOpenIDConfiguration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
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
	if r.Method != http.MethodGet {
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
	settings, err := s.app.UserSettings(r.Context(), claims.UserID)
	if err != nil {
		s.writeAppError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"userId":   claims.UserID,
		"nickname": s.app.Nickname(claims.UserID),
		"settings": settings,
		"schedule": s.appScheduleContext(now),
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
		s.writeError(w, http.StatusUnauthorized, err.Error())
		return sessionClaims{}, false
	}
	return claims, true
}

func (s *Server) serveShell(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.setNoStoreHeaders(w)
	w.WriteHeader(status)
	_ = s.pageTemplate.Execute(w, data)
}

func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dest any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dest); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *Server) writeAppError(w http.ResponseWriter, err error) {
	switch {
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
	s.setNoStoreHeaders(w)
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
	bundleSourceVersion := ""
	bundleTransformVersion := ""
	graphURL := ""
	if s.bundleStore != nil {
		bundleManifestURL = s.bundleStore.bundleAssetURL(basePath)
		if metadata, err := s.bundleStore.bundleMetadata(); err == nil && metadata != nil {
			bundleVersion = metadata.Version
			bundleServiceDate = metadata.ServiceDate
			bundleGeneratedAt = metadata.GeneratedAt
			bundleSourceVersion = metadata.SourceVersion
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
		StationCheckin:          s.app.StationCheckinEnabled(),
		MiniAppRefreshMs:        30_000,
		PublicRefreshMs:         60_000,
		ExternalTrainMapEnabled: s.cfg.ExternalTrainMapEnabled,
		ExternalTrainMapBaseURL: s.cfg.ExternalTrainMapBaseURL,
		ExternalTrainMapWsURL:   s.cfg.ExternalTrainMapWsURL,
		ExternalTrainGraphURL:   graphURL,
		SpacetimeHost:           s.cfg.TrainWebSpacetimeHost,
		SpacetimeDatabase:       s.cfg.TrainWebSpacetimeDatabase,
		PublicEdgeCacheEnabled:  s.cfg.TrainWebPublicEdgeCacheEnabled,
		BundleManifestURL:       bundleManifestURL,
		BundleVersion:           bundleVersion,
		BundleServiceDate:       bundleServiceDate,
		BundleGeneratedAt:       bundleGeneratedAt,
		BundleSourceVersion:     bundleSourceVersion,
		BundleTransformVersion:  bundleTransformVersion,
		ScheduleJSON:            scheduleJSON,
		AppCSSURL:               s.release.AssetURL(basePath, "app.css"),
		LeafletCSSURL:           s.release.AssetURL(basePath, "vendor/leaflet.css"),
		LeafletJSURL:            s.release.AssetURL(basePath, "vendor/leaflet.js"),
		ExternalFeedJSURL:       s.release.AssetURL(basePath, "external-feed.js"),
		AppJSURL:                s.release.AssetURL(basePath, "app.js"),
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	assetPath := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/assets/")
	if s.bundleStore != nil && strings.HasPrefix(assetPath, "bundles/") {
		s.serveBundleAsset(w, r, strings.TrimPrefix(assetPath, "bundles/"))
		return
	}
	version := strings.TrimSpace(r.URL.Query().Get("v"))
	if version != "" && version == s.release.AssetHash(assetPath) {
		s.setImmutableHeaders(w)
	} else {
		s.setNoStoreHeaders(w)
	}
	http.StripPrefix(basePath+"/assets/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
}

func (s *Server) serveBundleAsset(w http.ResponseWriter, r *http.Request, relativePath string) {
	cleanRelativePath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+relativePath)), "/")
	if cleanRelativePath == "" || cleanRelativePath == "." || strings.HasPrefix(cleanRelativePath, "..") {
		http.NotFound(w, r)
		return
	}
	if s.bundleStore == nil || strings.TrimSpace(s.bundleStore.dir) == "" {
		http.NotFound(w, r)
		return
	}
	assetPath := filepath.Join(s.bundleStore.dir, cleanRelativePath)
	info, err := os.Stat(assetPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	s.setImmutableHeaders(w)
	http.ServeFile(w, r, assetPath)
}

func (s *Server) setReleaseHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Train-Bot-Commit", s.release.Commit)
	w.Header().Set("X-Train-Bot-Build-Time", s.release.BuildTime)
	w.Header().Set("X-Train-Bot-Instance", s.release.Instance)
	w.Header().Set("X-Train-Bot-App-Js", s.release.AppJSHash)
}

func (s *Server) setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
}

func (s *Server) setImmutableHeaders(w http.ResponseWriter) {
	s.setImmutableHeadersWithTTL(w, 31536000)
}

func (s *Server) setImmutableHeadersWithTTL(w http.ResponseWriter, ttlSec int) {
	value := fmt.Sprintf("public, max-age=%d, immutable", ttlSec)
	w.Header().Set("Cache-Control", value)
	w.Header().Set("CDN-Cache-Control", value)
	w.Header().Set("Cloudflare-CDN-Cache-Control", value)
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

func (s *Server) bundlePublicNetworkMap(now time.Time) (map[string]any, bool, error) {
	data, ok, err := s.bundleData()
	if err != nil || !ok {
		return nil, ok, err
	}
	return data.publicNetworkMap(now), true, nil
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
			card["riders"] = len(users)
		}
	case trainapp.TrainCard:
		card.Riders = len(users)
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
			"commit":    s.release.Commit,
			"buildTime": s.release.BuildTime,
			"dirty":     s.release.Dirty,
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
