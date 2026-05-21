package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/bot"
	"satiksmebot/internal/config"
	"satiksmebot/internal/model"
	"satiksmebot/internal/reports"
	"satiksmebot/internal/runtime"
	"satiksmebot/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type staticCatalog struct {
	catalog     *model.Catalog
	status      runtime.CatalogStatus
	catalogJSON []byte
	etag        string
}

func (s staticCatalog) Current() *model.Catalog { return s.catalog }
func (s staticCatalog) Status() runtime.CatalogStatus {
	return s.status
}
func (s staticCatalog) FindStop(stopID string) (model.Stop, bool) {
	for _, stop := range s.catalog.Stops {
		if stop.ID == stopID {
			return stop, true
		}
	}
	return model.Stop{}, false
}
func (s staticCatalog) CatalogJSON() []byte { return s.catalogJSON }
func (s staticCatalog) CatalogETag() string { return s.etag }

type liveViewerHeartbeatStore struct {
	store.Store
	calls []liveViewerHeartbeatCall
}

type failingHealthStore struct {
	store.Store
	err   error
	calls int
}

func (s *failingHealthStore) HealthCheck(context.Context) error {
	s.calls++
	return s.err
}

type liveViewerHeartbeatCall struct {
	SessionID string
	Page      string
	Visible   bool
}

func (s *liveViewerHeartbeatStore) SetLiveViewerState(_ context.Context, sessionID string, page string, visible bool) error {
	s.calls = append(s.calls, liveViewerHeartbeatCall{
		SessionID: sessionID,
		Page:      page,
		Visible:   visible,
	})
	return nil
}

func TestPublicResponsesApplySecurityHeadersWithoutDebugHeaders(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, path := range []string{
		"/",
		"/api/v1/health",
		"/api/v1/auth/telegram/config",
		"/assets/app.js",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 body=%s", path, rec.Code, rec.Body.String())
		}
		if path == "/" {
			if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
				t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
			}
			if !strings.Contains(rec.Body.String(), `<meta name="robots" content="noindex, noarchive">`) {
				t.Fatalf("%s body missing robots meta tag: %s", path, rec.Body.String())
			}
		}
		for _, header := range []string{
			"Strict-Transport-Security",
			"Content-Security-Policy",
			"X-Frame-Options",
			"X-Content-Type-Options",
			"Referrer-Policy",
			"Permissions-Policy",
		} {
			if rec.Header().Get(header) == "" {
				t.Fatalf("%s missing security header %s", path, header)
			}
		}
		for name := range rec.Header() {
			if strings.HasPrefix(strings.ToLower(name), "x-satiksme-bot-") {
				t.Fatalf("%s exposed debug header %s=%q", path, name, rec.Header().Get(name))
			}
		}
	}
}

func TestPublicLiveViewerHeartbeatDisabledByDefault(t *testing.T) {
	server := newHardeningTestServer(t)
	liveStore := &liveViewerHeartbeatStore{Store: server.store}
	server.store = liveStore

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
		reqBody := strings.NewReader(`{"sessionId":"viewer-1","page":"public","visible":true}`)
		req := httptest.NewRequest(method, "/api/v1/public/live-viewer", reqBody)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s live viewer status = %d, want 404 body=%s", method, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s live viewer Cache-Control = %q", method, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s live viewer X-Robots-Tag = %q", method, got)
		}
	}
	if len(liveStore.calls) != 0 {
		t.Fatalf("live viewer calls = %d, want 0", len(liveStore.calls))
	}
}

func TestPublicLiveViewerHeartbeatRequiresExplicitEnablement(t *testing.T) {
	server := newHardeningTestServer(t)
	server.cfg.SatiksmeWebLiveViewerHeartbeatEnabled = true
	liveStore := &liveViewerHeartbeatStore{Store: server.store}
	server.store = liveStore

	reqBody := strings.NewReader(`{"sessionId":"viewer-1","page":"public","visible":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/live-viewer", reqBody)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("live viewer status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	if len(liveStore.calls) != 1 {
		t.Fatalf("live viewer calls = %d, want 1", len(liveStore.calls))
	}
	if got := liveStore.calls[0]; got.SessionID != "viewer-1" || got.Page != "public" || !got.Visible {
		t.Fatalf("live viewer call mismatch: %#v", got)
	}
}

func TestPublicLiveViewerHeartbeatRequiresJSONContentType(t *testing.T) {
	server := newHardeningTestServer(t)
	server.cfg.SatiksmeWebLiveViewerHeartbeatEnabled = true
	liveStore := &liveViewerHeartbeatStore{Store: server.store}
	server.store = liveStore

	reqBody := strings.NewReader(`{"sessionId":"viewer-1","page":"public","visible":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/public/live-viewer", reqBody)
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("live viewer status = %d, want 415 body=%s", rec.Code, rec.Body.String())
	}
	if len(liveStore.calls) != 0 {
		t.Fatalf("live viewer calls = %d, want 0", len(liveStore.calls))
	}
}

func TestShellConfigDoesNotEnablePublicLiveViewerHeartbeatByDefault(t *testing.T) {
	server := newHardeningTestServer(t)
	server.cfg.SatiksmeWebSpacetimeEnabled = true
	server.cfg.SatiksmeWebSpacetimeHost = "https://maincloud.spacetimedb.com"
	server.cfg.SatiksmeWebSpacetimeDatabase = "satiksme"

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "liveTransportViewerHeartbeatEnabled") {
		t.Fatalf("shell enabled live viewer heartbeat without server store: %s", rec.Body.String())
	}

	server.store = &liveViewerHeartbeatStore{Store: server.store}
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), "liveTransportViewerHeartbeatEnabled") {
		t.Fatalf("shell enabled live viewer heartbeat without explicit config: %s", rec.Body.String())
	}
}

func TestShellConfigEnablesPublicLiveViewerHeartbeatOnlyWhenConfigured(t *testing.T) {
	server := newHardeningTestServer(t)
	server.cfg.SatiksmeWebLiveViewerHeartbeatEnabled = true
	server.cfg.SatiksmeWebSpacetimeEnabled = true
	server.cfg.SatiksmeWebSpacetimeHost = "https://maincloud.spacetimedb.com"
	server.cfg.SatiksmeWebSpacetimeDatabase = "satiksme"
	server.store = &liveViewerHeartbeatStore{Store: server.store}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"liveTransportViewerHeartbeatEnabled":true`) {
		t.Fatalf("shell did not enable live viewer heartbeat with server store: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"liveTransportViewerHeartbeatURL":"/api/v1/public/live-viewer"`) {
		t.Fatalf("shell missing live viewer heartbeat endpoint with server store: %s", rec.Body.String())
	}
}

func TestProductionAppBundleDoesNotExposeTestHarnessMarker(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(mustStaticSubFS(), "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	for _, forbidden := range []string{
		"__test__",
		`"__" + "test__"`,
		"resetStateForTest",
	} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("production app.js exposes the test harness marker %q", forbidden)
		}
	}
}

func TestProductionStaticJSDoesNotReferenceSourceMaps(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"static/app.js", "static/live-client.js", "static/leaflet/leaflet.js"} {
		body, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(body), "sourceMappingURL=") {
			t.Fatalf("%s references a source map that is not publicly served", path)
		}
	}
}

func TestPublicHealthIsMinimal(t *testing.T) {
	server := newHardeningTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(health) error = %v", err)
	}
	if payload["ok"] != true {
		t.Fatalf("health ok = %#v, want true", payload["ok"])
	}
	for _, forbidden := range []string{
		"assets",
		"bundle",
		"catalog",
		"catalogStops",
		"db",
		"liveSnapshot",
		"reasons",
		"reportDump",
		"runtime",
		"telegram",
		"version",
		"web",
	} {
		if _, ok := payload[forbidden]; ok {
			t.Fatalf("public health exposed %q in payload %#v", forbidden, payload)
		}
	}
}

func TestStaticRoutesRejectTestAssetsUnsafePathsAndUnsupportedMethods(t *testing.T) {
	server := newHardeningTestServer(t)
	cases := []struct {
		method string
		path   string
		want   int
	}{
		{method: http.MethodGet, path: "/assets/app.test.js", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/assets/app.js.map", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/assets/%2e%2e/app.js", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/assets//app.js", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/assets%5capp.js", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/assets/app.js/", want: http.StatusNotFound},
		{method: http.MethodGet, path: "/api%2fv1%2fpublic%2fcatalog", want: http.StatusBadRequest},
		{method: http.MethodGet, path: "/api%5cv1%5cpublic%5ccatalog", want: http.StatusBadRequest},
		{method: http.MethodPost, path: "/", want: http.StatusMethodNotAllowed},
		{method: http.MethodPost, path: "/assets/app.js", want: http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s %s status = %d, want %d body=%s", tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
		}
		if tc.want >= http.StatusBadRequest {
			if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
				t.Fatalf("%s %s Cache-Control = %q", tc.method, tc.path, got)
			}
			if got := rec.Header().Get("CDN-Cache-Control"); got != "no-store" {
				t.Fatalf("%s %s CDN-Cache-Control = %q", tc.method, tc.path, got)
			}
		}
	}
}

func TestPublicReadAPIRoutesRejectUnsupportedMethods(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, path := range []string{
		"/api/v1/public/catalog",
		"/api/v1/public/sightings",
		"/api/v1/public/incidents",
		"/api/v1/public/incidents/stop:3012",
		"/api/v1/public/map",
		"/api/v1/public/map-live",
		"/api/v1/public/live-vehicles",
	} {
		headReq := httptest.NewRequest(http.MethodHead, path, nil)
		headRec := httptest.NewRecorder()
		server.ServeHTTP(headRec, headReq)
		if headRec.Code != http.StatusOK && headRec.Code != http.StatusNotFound {
			t.Fatalf("HEAD %s status = %d, want 200 or 404 body=%s", path, headRec.Code, headRec.Body.String())
		}
		for _, method := range []string{http.MethodPost, http.MethodOptions} {
			req := httptest.NewRequest(method, path, nil)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s status = %d, want 405 body=%s", method, path, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("%s %s Allow = %q, want GET, HEAD", method, path, got)
			}
			if strings.Contains(rec.Body.String(), "liveVehicles") || strings.Contains(rec.Body.String(), "incidents") || strings.Contains(rec.Body.String(), "sightings") {
				t.Fatalf("%s %s returned public JSON body on unsupported method: %s", method, path, rec.Body.String())
			}
		}
	}
}

func TestPublicIncidentLimitRejectsInvalidValues(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, query := range []string{
		"limit=abc",
		"limit=-1",
		"limit=2001",
		"limit=",
		"limit=1&limit=999",
		"limit=&limit=1",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/incidents?"+query, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 body=%s", query, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q", query, got)
		}
	}

	for _, value := range []string{"0", "1", "2000"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/incidents?limit="+value, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("limit=%s status = %d, want 200 body=%s", value, rec.Code, rec.Body.String())
		}
	}
}

func TestPublicSightingsLimitRejectsInvalidValues(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, path := range []string{
		"/api/v1/public/sightings",
		"/api/v1/public/map",
		"/api/v1/public/map-live",
		"/api/v1/public/live-vehicles",
	} {
		for _, query := range []string{
			"limit=abc",
			"limit=-1",
			"limit=0",
			"limit=501",
			"limit=1&limit=2",
		} {
			req := httptest.NewRequest(http.MethodGet, path+"?"+query, nil)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s?%s status = %d, want 400 body=%s", path, query, rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
				t.Fatalf("%s?%s Cache-Control = %q", path, query, got)
			}
		}

		for _, value := range []string{"1", "500"} {
			req := httptest.NewRequest(http.MethodGet, path+"?limit="+value, nil)
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s?limit=%s status = %d, want 200 body=%s", path, value, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestPublicReadEndpointsRejectUnexpectedQueryKeys(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, path := range []string{
		"/api/v1/public/catalog?cv=bogus",
		"/api/v1/public/incidents?limit=1&cv=bogus",
		"/api/v1/public/incidents/stop:3012?debug=1",
		"/api/v1/public/sightings?stopId=3012&stopId=3013",
		"/api/v1/public/sightings?stopId=3012&cacheVersion=bogus",
		"/api/v1/public/map?limit=1&date=2026-05-10",
		"/api/v1/public/map-live?limit=1&date=2026-05-10&cv=bogus",
		"/api/v1/public/live-vehicles?limit=1&cacheVersion=bogus",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400 body=%s", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
	}
}

func TestPublicCannotSubmitAndAuthenticatedSessionCan(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	now := time.Date(2026, 3, 10, 18, 55, 0, 0, time.UTC)
	testCatalog := &model.Catalog{
		GeneratedAt: now.Add(-10 * time.Minute),
		Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12}},
		Routes:      []model.Route{{Label: "1", Mode: "tram", Name: "Imanta"}},
	}
	catalogJSON, err := json.Marshal(testCatalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog: testCatalog,
		status: runtime.CatalogStatus{
			Loaded:             true,
			GeneratedAt:        testCatalog.GeneratedAt,
			LastRefreshAttempt: now.Add(-10 * time.Minute),
			LastRefreshSuccess: now.Add(-10 * time.Minute),
			StopCount:          len(testCatalog.Stops),
			RouteCount:         len(testCatalog.Routes),
		},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(now.Add(-time.Hour), true, "127.0.0.1:9318")
	runtimeState.UpdateCatalog(catalogReader.status)
	runtimeState.RecordTelegramSuccess(now.Add(-2*time.Minute), 101)
	runtimeState.RecordDumpSuccess(now.Add(-time.Minute), 0)
	runtimeState.SetWebListening(true)

	cfg := config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ts := httptest.NewServer(server)
	defer ts.Close()

	reportBody := bytes.NewBufferString(`{"stopId":"3012"}`)
	resp, err := http.Post(ts.URL+"/api/v1/reports/stop", "application/json", reportBody)
	if err != nil {
		t.Fatalf("public report POST error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("public report status = %d, want 401", resp.StatusCode)
	}

	sessionCookie := authenticateTestSession(t, server, ts.URL, 99, time.Now().UTC())

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/reports/vehicle", bytes.NewBufferString(`{"mode":"tram","routeLabel":"1","direction":"b-a","departureSeconds":68420}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(sessionCookie)
	httpClient := &http.Client{}
	vehicleResp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("vehicle POST error = %v", err)
	}
	defer vehicleResp.Body.Close()
	if vehicleResp.StatusCode != http.StatusOK {
		t.Fatalf("vehicle status = %d, want 200", vehicleResp.StatusCode)
	}

	liveVehicleReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/reports/vehicle", bytes.NewBufferString(`{"stopId":"754","mode":"bus","routeLabel":"10","direction":"b-a","destination":"Abrenes iela","departureSeconds":46406}`))
	if err != nil {
		t.Fatalf("NewRequest(live vehicle) error = %v", err)
	}
	liveVehicleReq.Header.Set("Content-Type", "application/json")
	liveVehicleReq.AddCookie(sessionCookie)
	liveVehicleResp, err := httpClient.Do(liveVehicleReq)
	if err != nil {
		t.Fatalf("live vehicle POST error = %v", err)
	}
	defer liveVehicleResp.Body.Close()
	if liveVehicleResp.StatusCode != http.StatusOK {
		t.Fatalf("live vehicle status = %d, want 200", liveVehicleResp.StatusCode)
	}

	areaReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/reports/area", bytes.NewBufferString(`{"latitude":56.95012,"longitude":24.11034,"radiusMeters":750,"description":"kontrole starp pieturām"}`))
	if err != nil {
		t.Fatalf("NewRequest(area) error = %v", err)
	}
	areaReq.Header.Set("Content-Type", "application/json")
	areaReq.AddCookie(sessionCookie)
	areaResp, err := httpClient.Do(areaReq)
	if err != nil {
		t.Fatalf("area POST error = %v", err)
	}
	defer areaResp.Body.Close()
	if areaResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(areaResp.Body)
		t.Fatalf("area status = %d, want 200 body=%s", areaResp.StatusCode, body)
	}
	var areaResult model.ReportResult
	if err := json.NewDecoder(areaResp.Body).Decode(&areaResult); err != nil {
		t.Fatalf("Decode(area result) error = %v", err)
	}
	if !areaResult.Accepted || !strings.HasPrefix(areaResult.IncidentID, "area:") {
		t.Fatalf("area result = %+v, want accepted area incident", areaResult)
	}

	sightingsResp, err := http.Get(ts.URL + "/api/v1/public/sightings")
	if err != nil {
		t.Fatalf("GET sightings error = %v", err)
	}
	defer sightingsResp.Body.Close()
	var payload model.VisibleSightings
	if err := json.NewDecoder(sightingsResp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(payload.VehicleSightings) != 2 {
		t.Fatalf("len(payload.VehicleSightings) = %d, want 2", len(payload.VehicleSightings))
	}
	if len(payload.AreaReports) != 1 || payload.AreaReports[0].RadiusMeters != 500 {
		t.Fatalf("payload.AreaReports = %+v, want one capped area report", payload.AreaReports)
	}
	sawEmptyDestination := false
	for _, item := range payload.VehicleSightings {
		if item.StopID != "" {
			t.Fatalf("expected standalone vehicle sighting without stop linkage, got %#v", item)
		}
		if item.RouteLabel == "1" && item.Destination == "" {
			sawEmptyDestination = true
		}
	}
	if !sawEmptyDestination {
		t.Fatalf("expected vehicle sighting without destination in public payload, got %#v", payload.VehicleSightings)
	}

	filteredSightingsResp, err := http.Get(ts.URL + "/api/v1/public/sightings?stopId=3012")
	if err != nil {
		t.Fatalf("GET filtered sightings error = %v", err)
	}
	defer filteredSightingsResp.Body.Close()
	var filteredPayload model.VisibleSightings
	if err := json.NewDecoder(filteredSightingsResp.Body).Decode(&filteredPayload); err != nil {
		t.Fatalf("Decode(filtered) error = %v", err)
	}
	if len(filteredPayload.VehicleSightings) != 0 {
		t.Fatalf("len(filteredPayload.VehicleSightings) = %d, want 0", len(filteredPayload.VehicleSightings))
	}
	if len(filteredPayload.AreaReports) != 0 {
		t.Fatalf("len(filteredPayload.AreaReports) = %d, want 0", len(filteredPayload.AreaReports))
	}

	recentReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/reports/recent?stopId=3012&limit=20", nil)
	if err != nil {
		t.Fatalf("NewRequest(recent) error = %v", err)
	}
	recentReq.AddCookie(sessionCookie)
	recentResp, err := httpClient.Do(recentReq)
	if err != nil {
		t.Fatalf("GET recent reports error = %v", err)
	}
	defer recentResp.Body.Close()
	var recentPayload model.VisibleSightings
	if err := json.NewDecoder(recentResp.Body).Decode(&recentPayload); err != nil {
		t.Fatalf("Decode(recent) error = %v", err)
	}
	if len(recentPayload.VehicleSightings) != 0 {
		t.Fatalf("len(recentPayload.VehicleSightings) = %d, want 0", len(recentPayload.VehicleSightings))
	}
	if len(recentPayload.AreaReports) != 0 {
		t.Fatalf("len(recentPayload.AreaReports) = %d, want 0", len(recentPayload.AreaReports))
	}
}

func TestPublicReportEndpointsIgnoreSmokeHeader(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	now := time.Date(2026, 3, 10, 18, 55, 0, 0, time.UTC)
	testCatalog := &model.Catalog{
		GeneratedAt: now.Add(-10 * time.Minute),
		Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12}},
		Routes:      []model.Route{{Label: "SMOKE", Mode: "bus", Name: "Smoke route"}},
	}
	catalogJSON, err := json.Marshal(testCatalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog: testCatalog,
		status: runtime.CatalogStatus{
			Loaded:             true,
			GeneratedAt:        testCatalog.GeneratedAt,
			LastRefreshAttempt: now.Add(-10 * time.Minute),
			LastRefreshSuccess: now.Add(-10 * time.Minute),
			StopCount:          len(testCatalog.Stops),
			RouteCount:         len(testCatalog.Routes),
		},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(now.Add(-time.Hour), true, "127.0.0.1:9318")
	runtimeState.UpdateCatalog(catalogReader.status)
	runtimeState.RecordTelegramSuccess(now.Add(-2*time.Minute), 101)
	runtimeState.RecordDumpSuccess(now.Add(-time.Minute), 0)
	runtimeState.SetWebListening(true)

	cfg := config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}
	dump := bot.NewDumpDispatcher(nil, st, runtimeState, "@satiksme_bot_reports", time.Second, time.UTC)
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), dump, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ts := httptest.NewServer(server)
	defer ts.Close()

	sessionCookie := authenticateTestSession(t, server, ts.URL, 199, time.Now().UTC())

	httpClient := &http.Client{}

	stopReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/reports/stop", bytes.NewBufferString(`{"stopId":"3012"}`))
	if err != nil {
		t.Fatalf("NewRequest(stop) error = %v", err)
	}
	stopReq.Header.Set("Content-Type", "application/json")
	stopReq.Header.Set("X-Satiksme-Smoke", "1")
	stopReq.AddCookie(sessionCookie)
	stopResp, err := httpClient.Do(stopReq)
	if err != nil {
		t.Fatalf("stop POST error = %v", err)
	}
	defer stopResp.Body.Close()
	if stopResp.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d, want 200", stopResp.StatusCode)
	}

	vehicleReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/reports/vehicle", bytes.NewBufferString(`{"stopId":"3012","mode":"bus","routeLabel":"SMOKE","direction":"a-b","destination":"Smoke Destination 199","departureSeconds":86340,"liveRowId":"smoke-199"}`))
	if err != nil {
		t.Fatalf("NewRequest(vehicle) error = %v", err)
	}
	vehicleReq.Header.Set("Content-Type", "application/json")
	vehicleReq.Header.Set("X-Satiksme-Smoke", "1")
	vehicleReq.AddCookie(sessionCookie)
	vehicleResp, err := httpClient.Do(vehicleReq)
	if err != nil {
		t.Fatalf("vehicle POST error = %v", err)
	}
	defer vehicleResp.Body.Close()
	if vehicleResp.StatusCode != http.StatusOK {
		t.Fatalf("vehicle status = %d, want 200", vehicleResp.StatusCode)
	}

	publicSightingsResp, err := http.Get(ts.URL + "/api/v1/public/sightings?stopId=3012&limit=20")
	if err != nil {
		t.Fatalf("GET public sightings error = %v", err)
	}
	defer publicSightingsResp.Body.Close()
	var publicSightings model.VisibleSightings
	if err := json.NewDecoder(publicSightingsResp.Body).Decode(&publicSightings); err != nil {
		t.Fatalf("Decode(public sightings) error = %v", err)
	}
	if len(publicSightings.StopSightings) != 1 {
		t.Fatalf("len(publicSightings.StopSightings) = %d, want 1", len(publicSightings.StopSightings))
	}

	publicIncidentsResp, err := http.Get(ts.URL + "/api/v1/public/incidents?limit=20")
	if err != nil {
		t.Fatalf("GET public incidents error = %v", err)
	}
	defer publicIncidentsResp.Body.Close()
	var publicIncidents struct {
		Incidents []model.IncidentSummary `json:"incidents"`
	}
	if err := json.NewDecoder(publicIncidentsResp.Body).Decode(&publicIncidents); err != nil {
		t.Fatalf("Decode(public incidents) error = %v", err)
	}
	if len(publicIncidents.Incidents) == 0 {
		t.Fatalf("public incidents = empty, want smoke header ignored")
	}

	recentReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/reports/recent?stopId=3012&limit=20", nil)
	if err != nil {
		t.Fatalf("NewRequest(recent) error = %v", err)
	}
	recentReq.AddCookie(sessionCookie)
	recentResp, err := httpClient.Do(recentReq)
	if err != nil {
		t.Fatalf("GET recent reports error = %v", err)
	}
	defer recentResp.Body.Close()
	var recent model.VisibleSightings
	if err := json.NewDecoder(recentResp.Body).Decode(&recent); err != nil {
		t.Fatalf("Decode(recent reports) error = %v", err)
	}
	if len(recent.StopSightings) != 1 {
		t.Fatalf("len(recent.StopSightings) = %d, want 1", len(recent.StopSightings))
	}
	if len(recent.VehicleSightings) != 0 {
		t.Fatalf("len(recent.VehicleSightings) = %d, want 0 for stop-filtered recent", len(recent.VehicleSightings))
	}

	recentGlobalReq, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/reports/recent?limit=20", nil)
	if err != nil {
		t.Fatalf("NewRequest(recent global) error = %v", err)
	}
	recentGlobalReq.AddCookie(sessionCookie)
	recentGlobalResp, err := httpClient.Do(recentGlobalReq)
	if err != nil {
		t.Fatalf("GET global recent reports error = %v", err)
	}
	defer recentGlobalResp.Body.Close()
	var recentGlobal model.VisibleSightings
	if err := json.NewDecoder(recentGlobalResp.Body).Decode(&recentGlobal); err != nil {
		t.Fatalf("Decode(global recent reports) error = %v", err)
	}
	if len(recentGlobal.VehicleSightings) != 1 {
		t.Fatalf("len(recentGlobal.VehicleSightings) = %d, want 1", len(recentGlobal.VehicleSightings))
	}
	if recentGlobal.VehicleSightings[0].Destination != "Smoke Destination 199" {
		t.Fatalf("recent global vehicle destination = %q", recentGlobal.VehicleSightings[0].Destination)
	}

	pending, err := st.PendingReportDumpCount(ctx)
	if err != nil {
		t.Fatalf("PendingReportDumpCount() error = %v", err)
	}
	if pending != 2 {
		t.Fatalf("pending dump count = %d, want 2", pending)
	}
}

func TestPublicHealthIsMinimalAndDetailedHealthIsLocalOnly(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	now := time.Date(2026, 3, 10, 18, 55, 0, 0, time.UTC)
	testCatalog := &model.Catalog{
		GeneratedAt: now.Add(-45 * time.Minute),
		Stops:       []model.Stop{{ID: "3012", LiveID: "4126", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12, NearbyStopIDs: []string{"3013"}}},
		Routes:      []model.Route{{Label: "22", Mode: "bus", Name: "Lidosta", StopIDs: []string{"3012"}}},
	}
	catalogJSON, err := json.Marshal(testCatalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog: testCatalog,
		status: runtime.CatalogStatus{
			Loaded:             true,
			LoadedFromFallback: true,
			GeneratedAt:        testCatalog.GeneratedAt,
			LastRefreshAttempt: now.Add(-5 * time.Minute),
			LastRefreshError:   "upstream timeout",
			StopCount:          len(testCatalog.Stops),
			RouteCount:         len(testCatalog.Routes),
		},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(now.Add(-2*time.Hour), true, "127.0.0.1:9318")
	runtimeState.UpdateCatalog(catalogReader.status)
	runtimeState.RecordTelegramError(now.Add(-30*time.Second), "telegram timeout")
	runtimeState.RecordDumpError(now.Add(-20*time.Second), "send failed", 3)
	runtimeState.SetWebListening(true)

	cfg := config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
		CatalogRefreshHours:              24,
	}
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.release.Commit = "abcdef123456"
	server.release.Dirty = "clean"
	server.release.ReleaseID = "release-20260511T120000Z"
	server.release.SourceSHA256 = "4e07408562bedb8b60ce05c1decfe3ad16b72230950de01f640b7e4729b49fce"

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthRec.Code)
	}
	for _, header := range []string{
		"X-Satiksme-Bot-Instance",
		"X-Satiksme-Bot-App-Js",
		"X-Satiksme-Bot-App-Css",
		"X-Satiksme-Bot-Live-Client",
		"X-Satiksme-Bot-Build-Time",
		"X-Satiksme-Bot-Commit",
	} {
		if got := healthRec.Header().Get(header); got != "" {
			t.Fatalf("public health header %s = %q, want empty", header, got)
		}
	}

	var health map[string]any
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("Unmarshal(health) error = %v", err)
	}
	if health["ok"] != true {
		t.Fatalf("health ok = %#v, want true", health["ok"])
	}
	if health["degraded"] != true {
		t.Fatalf("health degraded = %#v, want true", health["degraded"])
	}
	for _, key := range []string{"runtime", "assets", "catalog", "telegram", "reportDump", "db", "web", "bundle", "liveSnapshot", "version", "catalogStops"} {
		if _, ok := health[key]; ok {
			t.Fatalf("public health unexpectedly exposes %q: %#v", key, health[key])
		}
	}
	for _, path := range []string{"/api/v1/health", "/api/v1/livez"} {
		headReq := httptest.NewRequest(http.MethodHead, path, nil)
		headRec := httptest.NewRecorder()
		server.ServeHTTP(headRec, headReq)
		if headRec.Code != http.StatusOK {
			t.Fatalf("HEAD %s status = %d, want 200", path, headRec.Code)
		}

		optionsReq := httptest.NewRequest(http.MethodOptions, path, nil)
		optionsRec := httptest.NewRecorder()
		server.ServeHTTP(optionsRec, optionsReq)
		if optionsRec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("OPTIONS %s status = %d, want 405", path, optionsRec.Code)
		}
		if optionsRec.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("OPTIONS %s Allow = %q, want GET, HEAD", path, optionsRec.Header().Get("Allow"))
		}
	}

	internalReq := httptest.NewRequest(http.MethodGet, "/api/v1/internal/health", nil)
	internalReq.RemoteAddr = "127.0.0.1:48123"
	internalRec := httptest.NewRecorder()
	server.ServeHTTP(internalRec, internalReq)
	if internalRec.Code != http.StatusOK {
		t.Fatalf("internal health status = %d, want 200", internalRec.Code)
	}
	var internalHealth map[string]any
	if err := json.Unmarshal(internalRec.Body.Bytes(), &internalHealth); err != nil {
		t.Fatalf("Unmarshal(internal health) error = %v", err)
	}
	if _, ok := internalHealth["runtime"].(map[string]any); !ok {
		t.Fatalf("internal health missing runtime payload: %#v", internalHealth)
	}
	versionPayload := internalHealth["version"].(map[string]any)
	if versionPayload["commit"] != "abcdef123456" {
		t.Fatalf("version.commit = %#v, want abcdef123456", versionPayload["commit"])
	}
	if versionPayload["dirty"] != "clean" {
		t.Fatalf("version.dirty = %#v, want clean", versionPayload["dirty"])
	}
	if versionPayload["releaseId"] != "release-20260511T120000Z" {
		t.Fatalf("version.releaseId = %#v, want release-20260511T120000Z", versionPayload["releaseId"])
	}
	if versionPayload["sourceSha256"] != "4e07408562bedb8b60ce05c1decfe3ad16b72230950de01f640b7e4729b49fce" {
		t.Fatalf("version.sourceSha256 = %#v, want release source hash", versionPayload["sourceSha256"])
	}
	assetsPayload := internalHealth["assets"].(map[string]any)
	if assetsPayload["liveClientSha256"] == "" {
		t.Fatalf("internal health missing live client asset hash")
	}
	catalogPayload := internalHealth["catalog"].(map[string]any)
	if catalogPayload["loadedFromFallback"] != true {
		t.Fatalf("catalog.loadedFromFallback = %#v, want true", catalogPayload["loadedFromFallback"])
	}
	if catalogPayload["lastRefreshError"] != "upstream timeout" {
		t.Fatalf("catalog.lastRefreshError = %#v", catalogPayload["lastRefreshError"])
	}
	telegramPayload := internalHealth["telegram"].(map[string]any)
	if telegramPayload["consecutiveErrors"] != float64(1) {
		t.Fatalf("telegram.consecutiveErrors = %#v, want 1", telegramPayload["consecutiveErrors"])
	}
	dumpPayload := internalHealth["reportDump"].(map[string]any)
	if dumpPayload["pending"] != float64(3) {
		t.Fatalf("reportDump.pending = %#v, want 3", dumpPayload["pending"])
	}

	publicInternalReq := httptest.NewRequest(http.MethodGet, "/api/v1/internal/health", nil)
	publicInternalReq.RemoteAddr = "203.0.113.10:48123"
	publicInternalRec := httptest.NewRecorder()
	server.ServeHTTP(publicInternalRec, publicInternalReq)
	if publicInternalRec.Code != http.StatusNotFound {
		t.Fatalf("public internal health status = %d, want 404", publicInternalRec.Code)
	}

	livezReq := httptest.NewRequest(http.MethodGet, "/api/v1/livez", nil)
	livezRec := httptest.NewRecorder()
	server.ServeHTTP(livezRec, livezReq)
	if livezRec.Code != http.StatusOK {
		t.Fatalf("livez status = %d, want 200", livezRec.Code)
	}
	var livez map[string]any
	if err := json.Unmarshal(livezRec.Body.Bytes(), &livez); err != nil {
		t.Fatalf("Unmarshal(livez) error = %v", err)
	}
	if livez["ok"] != true {
		t.Fatalf("livez ok = %#v, want true", livez["ok"])
	}
	for _, key := range []string{"runtime", "assets", "catalog", "telegram", "reportDump", "db", "web", "bundle", "liveSnapshot", "version"} {
		if _, ok := livez[key]; ok {
			t.Fatalf("livez unexpectedly exposes %q: %#v", key, livez[key])
		}
	}

	catalogReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/catalog", nil)
	catalogRec := httptest.NewRecorder()
	server.ServeHTTP(catalogRec, catalogReq)
	if catalogRec.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, want 200", catalogRec.Code)
	}
	if catalogRec.Header().Get("ETag") != catalogReader.etag {
		t.Fatalf("catalog ETag = %q, want %q", catalogRec.Header().Get("ETag"), catalogReader.etag)
	}
	if catalogRec.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("catalog Cache-Control = %q", catalogRec.Header().Get("Cache-Control"))
	}
	if catalogRec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("catalog Vary = %q, want Accept-Encoding", catalogRec.Header().Get("Vary"))
	}
	var publicCatalogPayload map[string]any
	if err := json.Unmarshal(catalogRec.Body.Bytes(), &publicCatalogPayload); err != nil {
		t.Fatalf("Unmarshal(public catalog) error = %v", err)
	}
	stops, _ := publicCatalogPayload["stops"].([]any)
	if len(stops) != 1 {
		t.Fatalf("public catalog stops = %#v", publicCatalogPayload["stops"])
	}
	stop, _ := stops[0].(map[string]any)
	for _, forbidden := range []string{"liveId", "nearbyStopIds"} {
		if _, ok := stop[forbidden]; ok {
			t.Fatalf("public catalog stop exposes %q: %#v", forbidden, stop)
		}
	}
	if stop["id"] != "3012" || stop["name"] != "Centrāltirgus" {
		t.Fatalf("public catalog stop = %#v", stop)
	}

	notModifiedReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/catalog", nil)
	notModifiedReq.Header.Set("If-None-Match", catalogReader.etag)
	notModifiedRec := httptest.NewRecorder()
	server.ServeHTTP(notModifiedRec, notModifiedReq)
	if notModifiedRec.Code != http.StatusNotModified {
		t.Fatalf("conditional catalog status = %d, want 304", notModifiedRec.Code)
	}
	if notModifiedRec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("conditional catalog Vary = %q, want Accept-Encoding", notModifiedRec.Header().Get("Vary"))
	}
	if notModifiedRec.Header().Get("X-Robots-Tag") != "noindex, noarchive" {
		t.Fatalf("conditional catalog X-Robots-Tag = %q, want noindex, noarchive", notModifiedRec.Header().Get("X-Robots-Tag"))
	}
	if notModifiedRec.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("conditional catalog Cache-Control = %q", notModifiedRec.Header().Get("Cache-Control"))
	}

	liveDeparturesReq := httptest.NewRequest(http.MethodGet, "/api/v1/live/departures?stopId=3012", nil)
	liveDeparturesRec := httptest.NewRecorder()
	server.ServeHTTP(liveDeparturesRec, liveDeparturesReq)
	if liveDeparturesRec.Code != http.StatusNotFound {
		t.Fatalf("live departures status = %d, want 404", liveDeparturesRec.Code)
	}
}

func TestPublicHealthDoesNotDependOnWritableStore(t *testing.T) {
	server := newHardeningTestServer(t)
	failingStore := &failingHealthStore{
		Store: server.store,
		err:   errors.New("live snapshot state timeout"),
	}
	server.store = failingStore

	publicReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	publicRec := httptest.NewRecorder()
	server.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusOK {
		t.Fatalf("public health status = %d, want 200 body=%s", publicRec.Code, publicRec.Body.String())
	}
	if failingStore.calls != 0 {
		t.Fatalf("public health called store HealthCheck %d times, want 0", failingStore.calls)
	}
	var publicHealth map[string]any
	if err := json.Unmarshal(publicRec.Body.Bytes(), &publicHealth); err != nil {
		t.Fatalf("Unmarshal(public health) error = %v", err)
	}
	if publicHealth["ok"] != true {
		t.Fatalf("public health ok = %#v, want true", publicHealth["ok"])
	}

	internalReq := httptest.NewRequest(http.MethodGet, "/api/v1/internal/health", nil)
	internalReq.RemoteAddr = "127.0.0.1:48123"
	internalRec := httptest.NewRecorder()
	server.ServeHTTP(internalRec, internalReq)
	if internalRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("internal health status = %d, want 503 body=%s", internalRec.Code, internalRec.Body.String())
	}
	if failingStore.calls != 1 {
		t.Fatalf("internal health called store HealthCheck %d times, want 1", failingStore.calls)
	}
	var internalHealth map[string]any
	if err := json.Unmarshal(internalRec.Body.Bytes(), &internalHealth); err != nil {
		t.Fatalf("Unmarshal(internal health) error = %v", err)
	}
	if internalHealth["ok"] != false {
		t.Fatalf("internal health ok = %#v, want false", internalHealth["ok"])
	}
	reasons, ok := internalHealth["reasons"].([]any)
	if !ok {
		t.Fatalf("internal health reasons = %#v, want array", internalHealth["reasons"])
	}
	found := false
	for _, reason := range reasons {
		if reason == "db_unwritable" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("internal health reasons = %#v, want db_unwritable", reasons)
	}
}

func TestPublicResponsesIncludeSafetyHeaders(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	server, err := NewServer(config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}, staticCatalog{catalog: &model.Catalog{}, status: runtime.CatalogStatus{Loaded: true}}, nil, nil, nil, runtime.New(time.Now().UTC(), true, "127.0.0.1:9318"), time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	wantHeaders := map[string]string{
		"Strict-Transport-Security": "max-age=31536000",
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Permissions-Policy":        "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), fullscreen=(self)",
	}
	for header, want := range wantHeaders {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, fragment := range []string{"default-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, fragment) {
			t.Fatalf("Content-Security-Policy missing %q: %q", fragment, csp)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("Content-Security-Policy still allows inline scripts: %q", csp)
	}
	if strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Fatalf("Content-Security-Policy still allows inline styles: %q", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-") {
		t.Fatalf("Content-Security-Policy missing script nonce: %q", csp)
	}
	if !strings.Contains(rec.Body.String(), `nonce="`) {
		t.Fatalf("shell missing script nonce: %s", rec.Body.String())
	}
	for _, header := range []string{"X-Satiksme-Bot-Instance", "X-Satiksme-Bot-App-Js", "X-Satiksme-Bot-App-Css", "X-Satiksme-Bot-Live-Client"} {
		if got := rec.Header().Get(header); got != "" {
			t.Fatalf("%s = %q, want empty", header, got)
		}
	}
}

func TestMissingPublicPathsAreNoStoreAndNoIndex(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, path := range []string{
		"/.well-known/security.txt",
		"/sitemap.xml",
		"/service-worker.js",
		"/manifest.json",
		"/favicon.ico",
		"/site.webmanifest",
		"/apple-touch-icon.png",
		"/apple-touch-icon-precomposed.png",
		"/deploy-validation-missing-path",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 body=%s", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
	}
}

func TestRobotsTxtDeniesIndexing(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req := httptest.NewRequest(method, "/robots.txt", nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s /robots.txt status = %d, want 200 body=%s", method, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
			t.Fatalf("%s /robots.txt Cache-Control = %q, want no-store", method, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s /robots.txt X-Robots-Tag = %q, want noindex, noarchive", method, got)
		}
		if method == http.MethodGet {
			body := strings.ToLower(rec.Body.String())
			if !strings.Contains(body, "user-agent: *") || !strings.Contains(body, "disallow: /") {
				t.Fatalf("robots.txt does not deny indexing: %q", rec.Body.String())
			}
		}
	}
}

func TestStaticAssetsUseSecurityAndNoIndexHeaders(t *testing.T) {
	server := newHardeningTestServer(t)
	for _, path := range []string{"/assets/app.js", "/assets/app.css"} {
		req := httptest.NewRequest(http.MethodHead, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200 body=%s", path, rec.Code, rec.Body.String())
		}
		for _, header := range []string{
			"Strict-Transport-Security",
			"Content-Security-Policy",
			"X-Frame-Options",
			"X-Content-Type-Options",
			"Referrer-Policy",
			"Permissions-Policy",
		} {
			if got := rec.Header().Get(header); got == "" {
				t.Fatalf("%s missing %s", path, header)
			}
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
		if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Fatalf("%s Vary = %q, want Accept-Encoding", path, got)
		}
	}
}

func TestStaticAssetCacheHeadersPreserveNoStoreForRangesAndVaryForCompression(t *testing.T) {
	server := newHardeningTestServer(t)

	versionedReq := httptest.NewRequest(http.MethodGet, "/assets/app.js?v="+server.release.AssetHash("app.js"), nil)
	versionedReq.Header.Set("Accept-Encoding", "gzip")
	versionedRec := httptest.NewRecorder()
	server.ServeHTTP(versionedRec, versionedReq)
	if versionedRec.Code != http.StatusOK {
		t.Fatalf("versioned asset status = %d, want 200 body=%s", versionedRec.Code, versionedRec.Body.String())
	}
	if got := versionedRec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("versioned asset Cache-Control = %q", got)
	}
	if got := versionedRec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("versioned asset Vary = %q", got)
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rangeReq.Header.Set("Range", "bytes=0-63")
	rangeRec := httptest.NewRecorder()
	server.ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusOK {
		t.Fatalf("unversioned range asset status = %d, want 200 body=%s", rangeRec.Code, rangeRec.Body.String())
	}
	if got := rangeRec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unversioned range asset Cache-Control = %q", got)
	}
	if got := rangeRec.Header().Get("Content-Range"); got != "" {
		t.Fatalf("unversioned range asset Content-Range = %q", got)
	}
	if got := rangeRec.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("unversioned range asset Vary = %q", got)
	}
}

func TestIncidentShellRoutesRenderPublicIncidentsMode(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	testCatalog := &model.Catalog{}
	catalogJSON, err := json.Marshal(testCatalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog:     testCatalog,
		status:      runtime.CatalogStatus{Loaded: true},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(time.Now().UTC(), true, "127.0.0.1:9318")
	runtimeState.SetWebListening(true)

	cfg := config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	for _, path := range []string{"/incidents", "/-incidents"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<meta name="robots" content="noindex, noarchive">`) {
			t.Fatalf("%s body missing robots meta tag: %s", path, body)
		}
		if !strings.Contains(body, "<title>Kontrole</title>") {
			t.Fatalf("%s body missing updated title: %s", path, body)
		}
		if !strings.Contains(body, `"mode":"public-incidents"`) {
			t.Fatalf("%s body missing public-incidents mode: %s", path, body)
		}
		if strings.Contains(body, `"/-incidents"`) {
			t.Fatalf("%s body unexpectedly exposes legacy incidents path", path)
		}
		if strings.Contains(body, "unpkg.com/leaflet") || strings.Contains(body, "/assets/leaflet/leaflet.js") {
			t.Fatalf("%s incidents shell should not load Leaflet: %s", path, body)
		}
		if strings.Contains(body, "telegram.org/js/telegram-login") || strings.Contains(body, "telegram-web-app.js") {
			t.Fatalf("%s incidents shell should not load Telegram scripts before login: %s", path, body)
		}
	}
}

func TestShellConfigEnablesBrowserLiveSnapshotLookup(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "spacetime.key")
	if err := writeTestRSAKey(keyPath); err != nil {
		t.Fatalf("WriteFile(spacetime.key) error = %v", err)
	}

	server, err := NewServer(config.Config{
		BotToken:                              "bot-token",
		SatiksmeWebEnabled:                    true,
		SatiksmeWebBindAddr:                   "127.0.0.1",
		SatiksmeWebPort:                       9318,
		SatiksmeWebPublicBaseURL:              "https://kontrole.info",
		SatiksmeWebSessionSecretFile:          secretPath,
		SatiksmeWebTelegramBotUsername:        "kontrolebot",
		SatiksmeWebTelegramClientID:           "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec:      300,
		SatiksmeWebSpacetimeEnabled:           true,
		SatiksmeWebSpacetimeHost:              "https://maincloud.spacetimedb.com",
		SatiksmeWebSpacetimeDatabase:          "db123",
		SatiksmeWebSpacetimeOIDCIssuer:        "https://kontrole.info/oidc",
		SatiksmeWebSpacetimeOIDCAudience:      "satiksme-bot-web",
		SatiksmeWebSpacetimeJWTPrivateKeyFile: keyPath,
		SatiksmeWebSpacetimeTokenTTLSec:       86400,
		SatiksmeWebSpacetimeDirectOnly:        false,
		SatiksmeWebLiveSnapshotDir:            t.TempDir(),
	}, staticCatalog{}, nil, nil, nil, nil, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("shell status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"liveTransportSnapshotLookupEnabled":true`) {
		t.Fatalf("shell config missing live snapshot lookup: %s", body)
	}
	for _, forbidden := range []string{`"spacetimeHost"`, `"spacetimeDatabase"`, `"spacetimeEnabled"`, `"liveTransportRealtimeEnabled"`, "/assets/live-client.js"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("snapshot shell unexpectedly exposes direct Spacetime config %s: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `/assets/leaflet/leaflet.css`) ||
		!strings.Contains(body, `/assets/leaflet/leaflet.js`) {
		t.Fatalf("map shell should load self-hosted Leaflet assets: %s", body)
	}
	if strings.Contains(body, "unpkg.com/leaflet") {
		t.Fatalf("map shell should not load Leaflet from unpkg: %s", body)
	}
	if strings.Contains(body, "telegram.org/js/telegram-login") || strings.Contains(body, "telegram-web-app.js") {
		t.Fatalf("shell should not load Telegram scripts before login: %s", body)
	}
	if !strings.Contains(body, "/assets/app.js") {
		t.Fatalf("shell should load app.js: %s", body)
	}

	assetRec := httptest.NewRecorder()
	server.ServeHTTP(assetRec, httptest.NewRequest(http.MethodGet, "/assets/live-client.js", nil))
	if assetRec.Code != http.StatusNotFound {
		t.Fatalf("snapshot-only live client asset status = %d, want 404", assetRec.Code)
	}
	if got := assetRec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("snapshot-only live client asset Cache-Control = %q", got)
	}
	if got := assetRec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("snapshot-only live client asset X-Robots-Tag = %q", got)
	}
}

func TestLegacyDirectOnlyFlagNoLongerBlocksWebsiteRoutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	bundleDir := filepath.Join(t.TempDir(), "public-bundles")
	versionDir := filepath.Join(bundleDir, "bundles", "bundle-123")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "active.json"), []byte("{\"version\":\"bundle-123\",\"generatedAt\":\"2026-03-30T00:00:00Z\",\"transformVersion\":\"satiksme-static-v1\",\"manifestPath\":\"bundles/bundle-123/manifest.json\"}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(active.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "manifest.json"), []byte("{\"version\":\"bundle-123\",\"generatedAt\":\"2026-03-30T00:00:00Z\",\"transformVersion\":\"satiksme-static-v1\",\"counts\":{\"stops\":1,\"routes\":0},\"slices\":{\"stops\":\"stops.json\",\"routes\":\"routes.json\"}}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(manifest.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "stops.json"), []byte("[{\"id\":\"3012\",\"name\":\"Centrāltirgus\"}]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(stops.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "routes.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(routes.json) error = %v", err)
	}

	now := time.Now().UTC()
	catalog := &model.Catalog{
		GeneratedAt: now,
		Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
	}
	catalogJSON, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog:     catalog,
		status:      runtime.CatalogStatus{Loaded: true, GeneratedAt: now, StopCount: 1},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(now, true, "127.0.0.1:9318")
	runtimeState.SetWebListening(true)

	cfg := config.Config{
		BotToken:                           "bot-token",
		SatiksmeWebEnabled:                 true,
		SatiksmeWebBindAddr:                "127.0.0.1",
		SatiksmeWebPort:                    9318,
		SatiksmeWebPublicBaseURL:           "https://kontrole.info",
		SatiksmeWebSessionSecretFile:       secretPath,
		SatiksmeWebTelegramClientID:        "123456789",
		SatiksmeWebTelegramBotUsername:     "kontrolebot",
		SatiksmeWebTelegramAuthMaxAgeSec:   300,
		SatiksmeWebTelegramAuthStateTTLSec: 600,
		SatiksmeWebBundleDir:               bundleDir,
		SatiksmeWebSpacetimeDirectOnly:     true,
	}
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.liveHTTPClient = &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		}),
	}

	for path, want := range map[string]int{
		"/api/v1/public/catalog":   http.StatusOK,
		"/api/v1/public/sightings": http.StatusOK,
		"/api/v1/public/incidents": http.StatusOK,
		"/api/v1/public/map":       http.StatusOK,
		"/api/v1/public/map-live":  http.StatusOK,
		"/api/v1/reports/recent":   http.StatusUnauthorized,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s status = %d, want %d", path, rec.Code, want)
		}
	}

	bundleReq := httptest.NewRequest(http.MethodGet, "/bundles/active.json", nil)
	bundleRec := httptest.NewRecorder()
	server.ServeHTTP(bundleRec, bundleReq)
	if bundleRec.Code != http.StatusOK {
		t.Fatalf("bundle status = %d, want 200", bundleRec.Code)
	}
	if !strings.Contains(bundleRec.Body.String(), "\"version\":\"bundle-123\"") {
		t.Fatalf("bundle body missing active version: %s", bundleRec.Body.String())
	}
	if bundleRec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("active bundle Vary = %q, want Accept-Encoding", bundleRec.Header().Get("Vary"))
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/bundles/bundle-123/manifest.json", nil)
	manifestRec := httptest.NewRecorder()
	server.ServeHTTP(manifestRec, manifestReq)
	if manifestRec.Code != http.StatusOK {
		t.Fatalf("bundle manifest status = %d, want 200", manifestRec.Code)
	}
	if manifestRec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("bundle manifest Vary = %q, want Accept-Encoding", manifestRec.Header().Get("Vary"))
	}
	trailingManifestReq := httptest.NewRequest(http.MethodGet, "/bundles/bundle-123/manifest.json/", nil)
	trailingManifestRec := httptest.NewRecorder()
	server.ServeHTTP(trailingManifestRec, trailingManifestReq)
	if trailingManifestRec.Code != http.StatusNotFound {
		t.Fatalf("trailing slash bundle manifest status = %d, want 404", trailingManifestRec.Code)
	}

	vehiclesReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/live-vehicles", nil)
	vehiclesRec := httptest.NewRecorder()
	server.ServeHTTP(vehiclesRec, vehiclesReq)
	if vehiclesRec.Code != http.StatusOK {
		t.Fatalf("live vehicles status = %d, want 200", vehiclesRec.Code)
	}
}

func TestMissingPublicBundleAssetUsesNoStoreHeaders(t *testing.T) {
	server := newHardeningTestServer(t)
	bundleDir := filepath.Join(t.TempDir(), "public-bundles")
	versionDir := filepath.Join(bundleDir, "bundles", "bundle-123")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "active.json"), []byte("{\"version\":\"bundle-123\"}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(active.json) error = %v", err)
	}
	server.cfg.SatiksmeWebBundleDir = bundleDir
	server.bundleStore = newStaticBundleStore(bundleDir)

	for _, path := range []string{"/bundles/bundle-123/missing.json", "/bundles/bundle-123/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 body=%s", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
		if got := rec.Header().Get("CDN-Cache-Control"); got != "no-store" {
			t.Fatalf("%s CDN-Cache-Control = %q", path, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
	}
}

func TestBundleActiveErrorsUseNoStoreAndNoIndexHeaders(t *testing.T) {
	server := newHardeningTestServer(t)

	for _, path := range []string{"/bundles/active.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404 body=%s", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s Cache-Control = %q", path, got)
		}
		if got := rec.Header().Get("CDN-Cache-Control"); got != "no-store" {
			t.Fatalf("%s CDN-Cache-Control = %q", path, got)
		}
		if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s X-Robots-Tag = %q, want noindex, noarchive", path, got)
		}
	}

	bundleDir := t.TempDir()
	server.cfg.SatiksmeWebBundleDir = bundleDir
	server.bundleStore = newStaticBundleStore(bundleDir)
	req := httptest.NewRequest(http.MethodGet, "/bundles/active.json?debug=1", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("active bundle query status = %d, want 404 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("active bundle query Cache-Control = %q", got)
	}
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("active bundle query X-Robots-Tag = %q, want noindex, noarchive", got)
	}
}

func TestPublicIncidentsReturn24HourHistoryAndResolvedItems(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	now := time.Now().UTC()
	catalogReader := staticCatalog{
		catalog: &model.Catalog{
			GeneratedAt: now,
			Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
		},
		status: runtime.CatalogStatus{Loaded: true},
	}
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    7,
		CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}
	for _, vote := range []model.IncidentVote{
		{
			IncidentID: reports.StopIncidentID("3012"),
			UserID:     11,
			Nickname:   "Amber Scout 111",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-30 * time.Minute),
			UpdatedAt:  now.Add(-30 * time.Minute),
		},
		{
			IncidentID: reports.StopIncidentID("3012"),
			UserID:     12,
			Nickname:   "Amber Scout 112",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-20 * time.Minute),
			UpdatedAt:  now.Add(-20 * time.Minute),
		},
	} {
		if err := st.UpsertIncidentVote(ctx, vote); err != nil {
			t.Fatalf("UpsertIncidentVote() error = %v", err)
		}
	}

	cfg := config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtime.New(now, true, "127.0.0.1:9318"), time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/incidents", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload struct {
		Incidents []model.IncidentSummary `json:"incidents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(payload.Incidents) != 1 {
		t.Fatalf("len(payload.Incidents) = %d, want 1", len(payload.Incidents))
	}
	if !payload.Incidents[0].Resolved {
		t.Fatalf("payload.Incidents[0].Resolved = false, want true")
	}
	if payload.Incidents[0].Votes.Cleared != 2 {
		t.Fatalf("payload.Incidents[0].Votes = %+v", payload.Incidents[0].Votes)
	}
}

func TestPublicMapIncludesStopIncidentsAndVehicleIncidentAttachments(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	now := time.Now().UTC()
	catalogReader := staticCatalog{
		catalog: &model.Catalog{
			GeneratedAt: now,
			Stops:       []model.Stop{{ID: "3012", LiveID: "4126", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12, NearbyStopIDs: []string{"3013"}}},
		},
		status: runtime.CatalogStatus{Loaded: true},
	}
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    7,
		CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID:               "veh-recent",
		StopID:           "3012",
		UserID:           8,
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 68420,
		LiveRowID:        "67133",
		ScopeKey:         reports.VehicleScopeKey(model.VehicleReportInput{StopID: "3012", Mode: "bus", RouteLabel: "22", Direction: "a-b", Destination: "Lidosta", DepartureSeconds: 68420, LiveRowID: "67133"}),
		CreatedAt:        now.Add(-70 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
	}
	if err := st.InsertAreaReport(ctx, model.AreaReport{
		ID:           "area-recent",
		UserID:       9,
		Latitude:     56.9485,
		Longitude:    24.1211,
		RadiusMeters: 500,
		Description:  "kontrole starp pieturām",
		ScopeKey:     reports.AreaScopeKey(model.AreaReportInput{Latitude: 56.9485, Longitude: 24.1211, RadiusMeters: 500, Description: "kontrole starp pieturām"}),
		CreatedAt:    now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertAreaReport() error = %v", err)
	}

	cfg := config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}
	server, err := NewServer(cfg, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtime.New(now, true, "127.0.0.1:9318"), time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	server.liveHTTPClient = &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					"2,22,24121150,56948109,,270,I,67133,a-b,3012,30,\n",
				)),
				Header: make(http.Header),
			}, nil
		}),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/map", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, forbidden := range []string{`"liveId"`, `"nearbyStopIds"`, `"liveRowId"`, `"scopeKey"`} {
		if bytes.Contains(rec.Body.Bytes(), []byte(forbidden)) {
			t.Fatalf("public map payload exposes %s: %s", forbidden, rec.Body.Bytes())
		}
	}
	var payload model.PublicMapPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(payload.StopIncidents) != 1 {
		t.Fatalf("len(payload.StopIncidents) = %d, want 1", len(payload.StopIncidents))
	}
	if len(payload.AreaIncidents) != 1 || payload.AreaIncidents[0].Area == nil {
		t.Fatalf("payload.AreaIncidents = %+v, want one area incident", payload.AreaIncidents)
	}
	if len(payload.Sightings.AreaReports) != 1 {
		t.Fatalf("len(payload.Sightings.AreaReports) = %d, want 1", len(payload.Sightings.AreaReports))
	}
	if len(payload.LiveVehicles) != 1 {
		t.Fatalf("len(payload.LiveVehicles) = %d, want 1", len(payload.LiveVehicles))
	}
	if len(payload.LiveVehicles[0].Incidents) != 1 {
		t.Fatalf("len(payload.LiveVehicles[0].Incidents) = %d, want 1", len(payload.LiveVehicles[0].Incidents))
	}
	if payload.LiveVehicles[0].Incidents[0].Scope != "vehicle" {
		t.Fatalf("payload.LiveVehicles[0].Incidents[0].Scope = %q", payload.LiveVehicles[0].Incidents[0].Scope)
	}

	vehiclesReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/live-vehicles", nil)
	vehiclesRec := httptest.NewRecorder()
	server.ServeHTTP(vehiclesRec, vehiclesReq)
	if vehiclesRec.Code != http.StatusOK {
		t.Fatalf("live vehicles status = %d, want 200", vehiclesRec.Code)
	}
	var livePayload struct {
		LiveVehicles []model.LiveVehicle `json:"liveVehicles"`
	}
	if err := json.Unmarshal(vehiclesRec.Body.Bytes(), &livePayload); err != nil {
		t.Fatalf("Unmarshal(live vehicles) error = %v", err)
	}
	if len(livePayload.LiveVehicles) != 1 || len(livePayload.LiveVehicles[0].Incidents) != 1 {
		t.Fatalf("livePayload = %#v", livePayload)
	}
	if got := livePayload.LiveVehicles[0].LiveRowID; got != "" {
		t.Fatalf("public live vehicle liveRowId = %q, want empty", got)
	}
	if got := livePayload.LiveVehicles[0].VehicleCode; got != "" {
		t.Fatalf("public live vehicle vehicleCode = %q, want empty", got)
	}
	if strings.Contains(livePayload.LiveVehicles[0].ID, "67133") {
		t.Fatalf("public live vehicle id exposes raw live row id: %q", livePayload.LiveVehicles[0].ID)
	}
	if livePayload.LiveVehicles[0].UpdatedAt.Nanosecond() != 0 {
		t.Fatalf("public live vehicle updatedAt keeps subsecond precision: %s", livePayload.LiveVehicles[0].UpdatedAt)
	}
	if livePayload.LiveVehicles[0].Latitude != 56.94811 || livePayload.LiveVehicles[0].Longitude != 24.12115 {
		t.Fatalf("public live vehicle coordinates = %.8f, %.8f; want rounded", livePayload.LiveVehicles[0].Latitude, livePayload.LiveVehicles[0].Longitude)
	}
	if len(livePayload.LiveVehicles[0].Incidents) == 1 &&
		livePayload.LiveVehicles[0].Incidents[0].Vehicle != nil &&
		livePayload.LiveVehicles[0].Incidents[0].Vehicle.LiveRowID != "" {
		t.Fatalf("public live vehicle incident exposes liveRowId: %+v", livePayload.LiveVehicles[0].Incidents[0].Vehicle)
	}

	mapLiveReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/map-live", nil)
	mapLiveRec := httptest.NewRecorder()
	server.ServeHTTP(mapLiveRec, mapLiveReq)
	if mapLiveRec.Code != http.StatusOK {
		t.Fatalf("map-live status = %d, want 200", mapLiveRec.Code)
	}
	var mapLivePayload model.PublicLiveMapPayload
	if err := json.Unmarshal(mapLiveRec.Body.Bytes(), &mapLivePayload); err != nil {
		t.Fatalf("Unmarshal(map-live) error = %v", err)
	}
	if len(mapLivePayload.LiveVehicles) != 1 {
		t.Fatalf("len(mapLivePayload.LiveVehicles) = %d, want 1", len(mapLivePayload.LiveVehicles))
	}
	if len(mapLivePayload.StopIncidents) != 1 {
		t.Fatalf("len(mapLivePayload.StopIncidents) = %d, want 1", len(mapLivePayload.StopIncidents))
	}
	if len(mapLivePayload.AreaIncidents) != 1 {
		t.Fatalf("len(mapLivePayload.AreaIncidents) = %d, want 1", len(mapLivePayload.AreaIncidents))
	}
	if bytes.Contains(mapLiveRec.Body.Bytes(), []byte(`"stops"`)) {
		t.Fatalf("map-live payload unexpectedly includes stops: %s", mapLiveRec.Body.Bytes())
	}
}

func TestLiveSnapshotRoutesExposeExpectedCacheHeaders(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}
	snapshotDir := filepath.Join(t.TempDir(), "transport", "live")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(snapshotDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "active.json"), []byte(`{"version":"snapshot-123","path":"transport/live/snapshot-123.json.js","hash":"internal-hash","publishedAt":"2026-03-30T01:45:05Z","vehicleCount":12}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(active.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(snapshotDir, "snapshot-123.json.js"), []byte("{\"vehicles\":[]}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(snapshot-123.json.js) error = %v", err)
	}

	server, err := NewServer(config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
		SatiksmeWebLiveSnapshotDir:       snapshotDir,
	}, staticCatalog{}, nil, nil, nil, nil, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	activeReq := httptest.NewRequest(http.MethodGet, "/transport/live/active.json", nil)
	activeRec := httptest.NewRecorder()
	server.ServeHTTP(activeRec, activeReq)
	if activeRec.Code != http.StatusOK {
		t.Fatalf("active snapshot status = %d, want 200", activeRec.Code)
	}
	if activeRec.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("active snapshot Cache-Control = %q", activeRec.Header().Get("Cache-Control"))
	}
	if activeRec.Header().Get("X-Robots-Tag") != "noindex, noarchive" {
		t.Fatalf("active snapshot X-Robots-Tag = %q", activeRec.Header().Get("X-Robots-Tag"))
	}
	var activePayload map[string]any
	if err := json.Unmarshal(activeRec.Body.Bytes(), &activePayload); err != nil {
		t.Fatalf("Unmarshal(active snapshot) error = %v", err)
	}
	if activePayload["version"] != "snapshot-123" || activePayload["path"] != "transport/live/snapshot-123.json.js" {
		t.Fatalf("active snapshot payload = %#v", activePayload)
	}
	for _, forbidden := range []string{"hash", "publishedAt", "vehicleCount", "lastSuccessAt", "lastAttemptAt", "status", "consecutiveFailures"} {
		if _, ok := activePayload[forbidden]; ok {
			t.Fatalf("active snapshot exposes %q in payload %#v", forbidden, activePayload)
		}
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/transport/live/snapshot-123.json.js", nil)
	assetRec := httptest.NewRecorder()
	server.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("snapshot asset status = %d, want 200", assetRec.Code)
	}
	if assetRec.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("snapshot asset Cache-Control = %q", assetRec.Header().Get("Cache-Control"))
	}
	if !bytes.Equal(assetRec.Body.Bytes(), []byte("{\"vehicles\":[]}\n")) {
		t.Fatalf("snapshot asset body mismatch: %q", assetRec.Body.Bytes())
	}
	if assetRec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("snapshot asset Content-Type = %q", assetRec.Header().Get("Content-Type"))
	}
	if assetRec.Header().Get("X-Robots-Tag") != "noindex, noarchive" {
		t.Fatalf("snapshot asset X-Robots-Tag = %q", assetRec.Header().Get("X-Robots-Tag"))
	}

	rangeReq := httptest.NewRequest(http.MethodGet, "/transport/live/snapshot-123.json.js", nil)
	rangeReq.Header.Set("Range", "bytes=0-4")
	rangeRec := httptest.NewRecorder()
	server.ServeHTTP(rangeRec, rangeReq)
	if rangeRec.Code != http.StatusOK {
		t.Fatalf("range snapshot status = %d, want 200", rangeRec.Code)
	}
	if rangeRec.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("range snapshot Cache-Control = %q", rangeRec.Header().Get("Cache-Control"))
	}
	if rangeRec.Header().Get("Content-Range") != "" {
		t.Fatalf("range snapshot Content-Range = %q", rangeRec.Header().Get("Content-Range"))
	}
	if !bytes.Equal(rangeRec.Body.Bytes(), []byte("{\"vehicles\":[]}\n")) {
		t.Fatalf("range snapshot body mismatch: %q", rangeRec.Body.Bytes())
	}

	queryReq := httptest.NewRequest(http.MethodGet, "/transport/live/snapshot-123.json.js?cache=split", nil)
	queryRec := httptest.NewRecorder()
	server.ServeHTTP(queryRec, queryReq)
	if queryRec.Code != http.StatusNotFound {
		t.Fatalf("query snapshot status = %d, want 404", queryRec.Code)
	}
	if queryRec.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("query snapshot Cache-Control = %q", queryRec.Header().Get("Cache-Control"))
	}
	if queryRec.Header().Get("X-Robots-Tag") != "noindex, noarchive" {
		t.Fatalf("query snapshot X-Robots-Tag = %q", queryRec.Header().Get("X-Robots-Tag"))
	}
	trailingActiveReq := httptest.NewRequest(http.MethodGet, "/transport/live/active.json/", nil)
	trailingActiveRec := httptest.NewRecorder()
	server.ServeHTTP(trailingActiveRec, trailingActiveReq)
	if trailingActiveRec.Code != http.StatusNotFound {
		t.Fatalf("trailing active snapshot status = %d, want 404", trailingActiveRec.Code)
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/transport/live/missing.json", nil)
	missingRec := httptest.NewRecorder()
	server.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("missing snapshot status = %d, want 404", missingRec.Code)
	}
	if missingRec.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("missing snapshot Cache-Control = %q", missingRec.Header().Get("Cache-Control"))
	}
	if missingRec.Header().Get("X-Robots-Tag") != "noindex, noarchive" {
		t.Fatalf("missing snapshot X-Robots-Tag = %q", missingRec.Header().Get("X-Robots-Tag"))
	}
}

func newHardeningTestServer(t *testing.T) *Server {
	t.Helper()

	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	secretPath := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile(secret) error = %v", err)
	}

	now := time.Date(2026, 3, 10, 18, 55, 0, 0, time.UTC)
	testCatalog := &model.Catalog{
		GeneratedAt: now.Add(-10 * time.Minute),
		Stops:       []model.Stop{{ID: "3012", Name: "Centraltirgus", Latitude: 56.94, Longitude: 24.12}},
		Routes:      []model.Route{{Label: "1", Mode: "tram", Name: "Imanta"}},
	}
	catalogJSON, err := json.Marshal(testCatalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	sum := sha256.Sum256(catalogJSON)
	catalogReader := staticCatalog{
		catalog: testCatalog,
		status: runtime.CatalogStatus{
			Loaded:             true,
			GeneratedAt:        testCatalog.GeneratedAt,
			LastRefreshAttempt: now.Add(-10 * time.Minute),
			LastRefreshSuccess: now.Add(-10 * time.Minute),
			StopCount:          len(testCatalog.Stops),
			RouteCount:         len(testCatalog.Routes),
		},
		catalogJSON: catalogJSON,
		etag:        `"` + hex.EncodeToString(sum[:]) + `"`,
	}
	runtimeState := runtime.New(now.Add(-time.Hour), true, "127.0.0.1:9318")
	runtimeState.UpdateCatalog(catalogReader.status)
	runtimeState.SetWebListening(true)
	server, err := NewServer(config.Config{
		BotToken:                         "bot-token",
		SatiksmeWebEnabled:               true,
		SatiksmeWebBindAddr:              "127.0.0.1",
		SatiksmeWebPort:                  9318,
		SatiksmeWebPublicBaseURL:         "https://kontrole.info",
		SatiksmeWebSessionSecretFile:     secretPath,
		SatiksmeWebTelegramBotUsername:   "kontrolebot",
		SatiksmeWebTelegramClientID:      "123456789",
		SatiksmeWebTelegramAuthMaxAgeSec: 300,
	}, catalogReader, reports.NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute), nil, st, runtimeState, time.UTC)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	return server
}
