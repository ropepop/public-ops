package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
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
}

func TestSmokeReportsStayOutOfPublicViewsAndDumpQueue(t *testing.T) {
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
	stopReq.Header.Set(smokeRequestHeader, "1")
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
	vehicleReq.Header.Set(smokeRequestHeader, "1")
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
	if len(publicSightings.StopSightings) != 0 || len(publicSightings.VehicleSightings) != 0 {
		t.Fatalf("public sightings leaked smoke reports: %#v", publicSightings)
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
	if len(publicIncidents.Incidents) != 0 {
		t.Fatalf("public incidents leaked smoke reports: %#v", publicIncidents.Incidents)
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
	if pending != 0 {
		t.Fatalf("pending dump count = %d, want 0", pending)
	}
}

func TestHealthAndCatalogExposeReleaseMetadata(t *testing.T) {
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
		Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12}},
		Routes:      []model.Route{{Label: "22", Mode: "bus", Name: "Lidosta"}},
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

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", healthRec.Code)
	}
	if healthRec.Header().Get("X-Satiksme-Bot-Instance") == "" {
		t.Fatalf("missing X-Satiksme-Bot-Instance header")
	}
	if healthRec.Header().Get("X-Satiksme-Bot-App-Js") == "" ||
		healthRec.Header().Get("X-Satiksme-Bot-App-Css") == "" ||
		healthRec.Header().Get("X-Satiksme-Bot-Live-Client") == "" {
		t.Fatalf("missing asset hash headers")
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
	runtimePayload := health["runtime"].(map[string]any)
	if runtimePayload["instanceId"] != healthRec.Header().Get("X-Satiksme-Bot-Instance") {
		t.Fatalf("runtime.instanceId = %#v, want %q", runtimePayload["instanceId"], healthRec.Header().Get("X-Satiksme-Bot-Instance"))
	}
	assetsPayload := health["assets"].(map[string]any)
	if assetsPayload["liveClientSha256"] != healthRec.Header().Get("X-Satiksme-Bot-Live-Client") {
		t.Fatalf("assets.liveClientSha256 = %#v, want %q", assetsPayload["liveClientSha256"], healthRec.Header().Get("X-Satiksme-Bot-Live-Client"))
	}
	catalogPayload := health["catalog"].(map[string]any)
	if catalogPayload["loadedFromFallback"] != true {
		t.Fatalf("catalog.loadedFromFallback = %#v, want true", catalogPayload["loadedFromFallback"])
	}
	if catalogPayload["lastRefreshError"] != "upstream timeout" {
		t.Fatalf("catalog.lastRefreshError = %#v", catalogPayload["lastRefreshError"])
	}
	telegramPayload := health["telegram"].(map[string]any)
	if telegramPayload["consecutiveErrors"] != float64(1) {
		t.Fatalf("telegram.consecutiveErrors = %#v, want 1", telegramPayload["consecutiveErrors"])
	}
	dumpPayload := health["reportDump"].(map[string]any)
	if dumpPayload["pending"] != float64(3) {
		t.Fatalf("reportDump.pending = %#v, want 3", dumpPayload["pending"])
	}
	if _, ok := health["liveDepartures"]; ok {
		t.Fatalf("health unexpectedly exposes liveDepartures: %#v", health["liveDepartures"])
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
	if !bytes.Equal(catalogRec.Body.Bytes(), catalogJSON) {
		t.Fatalf("catalog body mismatch")
	}

	notModifiedReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/catalog", nil)
	notModifiedReq.Header.Set("If-None-Match", catalogReader.etag)
	notModifiedRec := httptest.NewRecorder()
	server.ServeHTTP(notModifiedRec, notModifiedReq)
	if notModifiedRec.Code != http.StatusNotModified {
		t.Fatalf("conditional catalog status = %d, want 304", notModifiedRec.Code)
	}

	liveDeparturesReq := httptest.NewRequest(http.MethodGet, "/api/v1/live/departures?stopId=3012", nil)
	liveDeparturesRec := httptest.NewRecorder()
	server.ServeHTTP(liveDeparturesRec, liveDeparturesReq)
	if liveDeparturesRec.Code != http.StatusNotFound {
		t.Fatalf("live departures status = %d, want 404", liveDeparturesRec.Code)
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
		body := rec.Body.String()
		if !strings.Contains(body, "<title>Kontrole</title>") {
			t.Fatalf("%s body missing updated title: %s", path, body)
		}
		if !strings.Contains(body, `"mode":"public-incidents"`) {
			t.Fatalf("%s body missing public-incidents mode: %s", path, body)
		}
		if strings.Contains(body, `"/-incidents"`) {
			t.Fatalf("%s body unexpectedly exposes legacy incidents path", path)
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
	if !strings.Contains(body, `"spacetimeHost":"https://maincloud.spacetimedb.com"`) ||
		!strings.Contains(body, `"spacetimeDatabase":"db123"`) {
		t.Fatalf("shell config missing Spacetime target: %s", body)
	}
	if !strings.Contains(body, `"spacetimeEnabled":false`) ||
		!strings.Contains(body, `"liveTransportRealtimeEnabled":false`) {
		t.Fatalf("shell should keep full direct data disabled when direct-only is false: %s", body)
	}
	liveClientIndex := strings.Index(body, "/assets/live-client.js")
	appIndex := strings.Index(body, "/assets/app.js")
	if liveClientIndex < 0 || appIndex < 0 || liveClientIndex > appIndex {
		t.Fatalf("shell should load live-client.js before app.js: %s", body)
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

	vehiclesReq := httptest.NewRequest(http.MethodGet, "/api/v1/public/live-vehicles", nil)
	vehiclesRec := httptest.NewRecorder()
	server.ServeHTTP(vehiclesRec, vehiclesReq)
	if vehiclesRec.Code != http.StatusOK {
		t.Fatalf("live vehicles status = %d, want 200", vehiclesRec.Code)
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
			Stops:       []model.Stop{{ID: "3012", Name: "Centrāltirgus", Latitude: 56.94, Longitude: 24.12}},
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
		ScopeKey:         reports.VehicleScopeKey(model.VehicleReportInput{StopID: "3012", Mode: "bus", RouteLabel: "22", Direction: "a-b", Destination: "Lidosta", DepartureSeconds: 68420}),
		CreatedAt:        now.Add(-70 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
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
	var payload model.PublicMapPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(payload.StopIncidents) != 1 {
		t.Fatalf("len(payload.StopIncidents) = %d, want 1", len(payload.StopIncidents))
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
	if err := os.WriteFile(filepath.Join(snapshotDir, "active.json"), []byte("{\"version\":\"snapshot-123\"}\n"), 0o644); err != nil {
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

	assetReq := httptest.NewRequest(http.MethodGet, "/transport/live/snapshot-123.json.js", nil)
	assetRec := httptest.NewRecorder()
	server.ServeHTTP(assetRec, assetReq)
	if assetRec.Code != http.StatusOK {
		t.Fatalf("snapshot asset status = %d, want 200", assetRec.Code)
	}
	if assetRec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("snapshot asset Cache-Control = %q", assetRec.Header().Get("Cache-Control"))
	}
	if !bytes.Equal(assetRec.Body.Bytes(), []byte("{\"vehicles\":[]}\n")) {
		t.Fatalf("snapshot asset body mismatch: %q", assetRec.Body.Bytes())
	}
	if assetRec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("snapshot asset Content-Type = %q", assetRec.Header().Get("Content-Type"))
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
}
