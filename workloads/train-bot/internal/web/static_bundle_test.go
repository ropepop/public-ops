package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/store"
)

type captureBundleSync struct {
	version       string
	serviceDate   string
	generatedAt   time.Time
	sourceVersion string
}

func TestProductionAppBundleDoesNotExposeTestHarnessMarker(t *testing.T) {
	t.Parallel()

	body, err := fs.ReadFile(mustStaticSubFS(), "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	for _, forbidden := range []string{"__test__", `"__" + "test__"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("production app.js exposes the test harness marker %q", forbidden)
		}
	}
}

func TestProductionStaticJSDoesNotReferenceSourceMaps(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"static/app.js", "static/external-feed.js", "static/live-client.js", "static/vendor/leaflet.js"} {
		body, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(body), "sourceMappingURL=") {
			t.Fatalf("%s references a source map that is not publicly served", path)
		}
	}
}

func (c *captureBundleSync) PublishActiveBundle(_ context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error {
	c.version = version
	c.serviceDate = serviceDate
	c.generatedAt = generatedAt
	c.sourceVersion = sourceVersion
	return nil
}

func TestStaticBundlePublisherWritesVersionedBundleAndFeedsServer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.February, 26, 8, 0, 0, 0, loc)
	appSvc := newStaticBundleTestService(t, now, loc)
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	syncer := &captureBundleSync{}
	publisher := NewStaticBundlePublisher(bundleDir, appSvc, loc, syncer)

	manifest, err := publisher.PublishManifest(ctx, now)
	if err != nil {
		t.Fatalf("publish static bundle: %v", err)
	}
	if manifest == nil {
		t.Fatalf("expected manifest")
	}
	if manifest.ServiceDate != "2026-02-26" {
		t.Fatalf("unexpected service date: %q", manifest.ServiceDate)
	}
	if manifest.Version == "" {
		t.Fatalf("expected bundle version")
	}
	if syncer.version != manifest.Version || syncer.serviceDate != manifest.ServiceDate {
		t.Fatalf("unexpected sync payload: %+v", syncer)
	}

	store := newStaticBundleStore(bundleDir)
	active, err := store.activeState()
	if err != nil {
		t.Fatalf("read active state: %v", err)
	}
	if active == nil {
		t.Fatalf("expected active state")
	}
	if active.ManifestPath != filepath.ToSlash(filepath.Join("bundles", manifest.Version, "manifest.json")) {
		t.Fatalf("unexpected manifest path: %q", active.ManifestPath)
	}
	if _, err := os.Stat(filepath.Join(bundleDir, manifest.Version, manifest.Slices.TrainGraph)); err != nil {
		t.Fatalf("missing graph slice: %v", err)
	}
	trainSliceBody, err := os.ReadFile(filepath.Join(bundleDir, manifest.Version, manifest.Slices.Trains))
	if err != nil {
		t.Fatalf("read train slice: %v", err)
	}
	if strings.Contains(string(trainSliceBody), "sourceVersion") {
		t.Fatalf("public train slice exposes per-train sourceVersion: %s", trainSliceBody)
	}
	if manifest.SourceVersion == "" || manifest.Freshness.Source == "" {
		t.Fatalf("in-memory manifest should retain source metadata for internal sync: %+v", manifest)
	}
	manifestBody, err := os.ReadFile(filepath.Join(bundleDir, manifest.Version, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if strings.Contains(string(manifestBody), "sourceVersion") {
		t.Fatalf("public manifest exposes sourceVersion: %s", manifestBody)
	}
	activeBody, err := os.ReadFile(filepath.Join(bundleDir, "active.json"))
	if err != nil {
		t.Fatalf("read active bundle pointer: %v", err)
	}
	if strings.Contains(string(activeBody), "sourceVersion") {
		t.Fatalf("public active bundle pointer exposes sourceVersion: %s", activeBody)
	}

	server := newStaticBundleTestServer(t, appSvc, bundleDir)
	server.now = func() time.Time { return now }

	shellReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/map", nil)
	shellRes := httptest.NewRecorder()
	server.ServeHTTP(shellRes, shellReq)
	if shellRes.Code != http.StatusOK {
		t.Fatalf("unexpected shell status: got %d body=%s", shellRes.Code, shellRes.Body.String())
	}
	shellBody := shellRes.Body.String()
	if !strings.Contains(shellBody, `bundleManifestURL: "/pixel-stack/train/assets/bundles/`+manifest.Version+`/manifest.json"`) {
		t.Fatalf("expected bundle manifest bootstrap, body=%s", shellBody)
	}
	if !strings.Contains(shellBody, `externalTrainGraphURL: "/pixel-stack/train/assets/bundles/`+manifest.Version+`/train-graph.json"`) {
		t.Fatalf("expected bundled graph bootstrap, body=%s", shellBody)
	}
	if strings.Contains(shellBody, "sourceVersion") {
		t.Fatalf("public shell exposes sourceVersion: %s", shellBody)
	}

	activeReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/assets/bundles/active.json", nil)
	activeRes := httptest.NewRecorder()
	server.ServeHTTP(activeRes, activeReq)
	if activeRes.Code != http.StatusOK {
		t.Fatalf("unexpected active bundle pointer status: got %d body=%s", activeRes.Code, activeRes.Body.String())
	}
	if got := activeRes.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("active bundle pointer Cache-Control = %q, want no-store", got)
	}
	if got := activeRes.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("active bundle pointer X-Robots-Tag = %q, want noindex, noarchive", got)
	}

	manifestReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/assets/bundles/"+manifest.Version+"/manifest.json", nil)
	manifestRes := httptest.NewRecorder()
	server.ServeHTTP(manifestRes, manifestReq)
	if manifestRes.Code != http.StatusOK {
		t.Fatalf("unexpected bundle manifest status: got %d body=%s", manifestRes.Code, manifestRes.Body.String())
	}
	if got := manifestRes.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("versioned bundle manifest Cache-Control = %q, want immutable", got)
	}
	if got := manifestRes.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
		t.Fatalf("versioned bundle manifest X-Robots-Tag = %q, want noindex, noarchive", got)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		bundleRootReq := httptest.NewRequest(method, "/pixel-stack/train/assets/bundles/", nil)
		bundleRootRes := httptest.NewRecorder()
		server.ServeHTTP(bundleRootRes, bundleRootReq)
		if bundleRootRes.Code != http.StatusNotFound {
			t.Fatalf("%s bundle root status = %d, want 404 body=%s", method, bundleRootRes.Code, bundleRootRes.Body.String())
		}
		if got := bundleRootRes.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
			t.Fatalf("%s bundle root Cache-Control = %q", method, got)
		}
		if got := bundleRootRes.Header().Get("X-Robots-Tag"); got != "noindex, noarchive" {
			t.Fatalf("%s bundle root X-Robots-Tag = %q, want noindex, noarchive", method, got)
		}
	}

	mapReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/map", nil)
	mapRes := httptest.NewRecorder()
	server.ServeHTTP(mapRes, mapReq)
	if mapRes.Code != http.StatusOK {
		t.Fatalf("unexpected public map status: got %d body=%s", mapRes.Code, mapRes.Body.String())
	}
	if !strings.Contains(mapRes.Body.String(), `"recentSightings":[]`) || !strings.Contains(mapRes.Body.String(), `"sameDaySightings":[]`) {
		t.Fatalf("expected empty sighting arrays in bundled public map, body=%s", mapRes.Body.String())
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/dashboard?limit=1", nil)
	dashboardRes := httptest.NewRecorder()
	server.ServeHTTP(dashboardRes, dashboardReq)
	if dashboardRes.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: got %d body=%s", dashboardRes.Code, dashboardRes.Body.String())
	}
	if strings.Contains(dashboardRes.Body.String(), "sourceVersion") {
		t.Fatalf("public dashboard exposes per-train sourceVersion: %s", dashboardRes.Body.String())
	}
	var payload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
		Schedule struct {
			Available            bool   `json:"available"`
			EffectiveServiceDate string `json:"effectiveServiceDate"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(dashboardRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode dashboard payload: %v", err)
	}
	if len(payload.Trains) != 1 || payload.Trains[0].Train.ID != "t1" {
		t.Fatalf("unexpected dashboard trains: %+v", payload.Trains)
	}
	if payload.Trains[0].Riders != 0 {
		t.Fatalf("expected dashboard rider field in bundle payload, got %+v", payload.Trains[0])
	}
	if !payload.Schedule.Available || payload.Schedule.EffectiveServiceDate != manifest.ServiceDate {
		t.Fatalf("unexpected schedule payload: %+v", payload.Schedule)
	}

	trainReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/trains/t1", nil)
	trainRes := httptest.NewRecorder()
	server.ServeHTTP(trainRes, trainReq)
	if trainRes.Code != http.StatusOK {
		t.Fatalf("unexpected public train status: got %d body=%s", trainRes.Code, trainRes.Body.String())
	}
	if strings.Contains(trainRes.Body.String(), "sourceVersion") {
		t.Fatalf("public train payload exposes sourceVersion: %s", trainRes.Body.String())
	}
	var trainPayload struct {
		Riders int `json:"riders"`
		Train  struct {
			ID string `json:"id"`
		} `json:"train"`
	}
	if err := json.Unmarshal(trainRes.Body.Bytes(), &trainPayload); err != nil {
		t.Fatalf("decode public train payload: %v", err)
	}
	if trainPayload.Train.ID != "t1" || trainPayload.Riders != 0 {
		t.Fatalf("expected public train rider field in bundle payload, got %+v", trainPayload)
	}
}

func TestStaticBundlePublisherRemovesOldPublicBundleVersions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.February, 26, 8, 0, 0, 0, loc)
	appSvc := newStaticBundleTestService(t, now, loc)
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	oldVersionDir := filepath.Join(bundleDir, "2026-01-01-oldbundle")
	if err := os.MkdirAll(oldVersionDir, 0o755); err != nil {
		t.Fatalf("create old bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oldVersionDir, "manifest.json"), []byte(`{"sourceVersion":"stale"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write old manifest: %v", err)
	}

	publisher := NewStaticBundlePublisher(bundleDir, appSvc, loc, nil)
	manifest, err := publisher.PublishManifest(ctx, now)
	if err != nil {
		t.Fatalf("publish static bundle: %v", err)
	}
	if manifest == nil || manifest.Version == "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if _, err := os.Stat(oldVersionDir); !os.IsNotExist(err) {
		t.Fatalf("old public bundle dir still exists, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(bundleDir, manifest.Version, "manifest.json")); err != nil {
		t.Fatalf("active bundle manifest missing after cleanup: %v", err)
	}
}

func TestStaticBundlePublisherDoesNotMutateImmutableManifestForSameVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.February, 26, 8, 0, 0, 0, loc)
	appSvc := newStaticBundleTestService(t, now, loc)
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	publisher := NewStaticBundlePublisher(bundleDir, appSvc, loc, nil)

	first, err := publisher.PublishManifest(ctx, now)
	if err != nil {
		t.Fatalf("publish first static bundle: %v", err)
	}
	firstBody, err := os.ReadFile(filepath.Join(bundleDir, first.Version, "manifest.json"))
	if err != nil {
		t.Fatalf("read first manifest: %v", err)
	}

	second, err := publisher.PublishManifest(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("publish second static bundle: %v", err)
	}
	secondBody, err := os.ReadFile(filepath.Join(bundleDir, second.Version, "manifest.json"))
	if err != nil {
		t.Fatalf("read second manifest: %v", err)
	}
	if first.Version != second.Version {
		t.Fatalf("version changed for unchanged bundle input: first=%q second=%q", first.Version, second.Version)
	}
	if string(firstBody) != string(secondBody) {
		t.Fatalf("immutable manifest changed for same version:\nfirst=%s\nsecond=%s", firstBody, secondBody)
	}
}

func TestStaticBundleServerBucketsRiderCountWhenTrainStopsFallbackUsesBundle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.February, 26, 8, 0, 0, 0, loc)
	appSvc, _ := newStaticBundleTestServiceWithStore(t, now, loc)

	bundleDir := filepath.Join(t.TempDir(), "bundles")
	publisher := NewStaticBundlePublisher(bundleDir, appSvc, loc, &captureBundleSync{})
	if _, err := publisher.PublishManifest(ctx, now); err != nil {
		t.Fatalf("publish static bundle: %v", err)
	}

	fallbackStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "fallback.db"))
	if err != nil {
		t.Fatalf("new fallback store: %v", err)
	}
	t.Cleanup(func() {
		_ = fallbackStore.Close()
	})
	if err := fallbackStore.Migrate(ctx); err != nil {
		t.Fatalf("migrate fallback store: %v", err)
	}
	if err := fallbackStore.CheckInUser(ctx, 44, "t1", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed fallback active checkin: %v", err)
	}
	emptyScheduleDir := filepath.Join(t.TempDir(), "empty-schedules")
	if err := os.MkdirAll(emptyScheduleDir, 0o755); err != nil {
		t.Fatalf("create empty schedule dir: %v", err)
	}
	fallbackSvc := trainapp.NewService(
		fallbackStore,
		schedule.NewManager(fallbackStore, emptyScheduleDir, loc, 3),
		ride.NewService(fallbackStore),
		reports.NewService(fallbackStore, 3*time.Minute, 90*time.Second),
		loc,
		false,
	)

	server := newStaticBundleTestServer(t, fallbackSvc, bundleDir)
	server.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/trains/t1/stops", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected train stops status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		TrainCard struct {
			Riders int `json:"riders"`
		} `json:"trainCard"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode train stops payload: %v", err)
	}
	if payload.TrainCard.Riders != 0 {
		t.Fatalf("expected single rider hidden in public train stops payload, got %d", payload.TrainCard.Riders)
	}
}

func TestStaticBundlePublicNetworkMapKeepsLiveStationSightings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.February, 26, 8, 0, 0, 0, loc)
	appSvc, st := newStaticBundleTestServiceWithStore(t, now, loc)
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	publisher := NewStaticBundlePublisher(bundleDir, appSvc, loc, &captureBundleSync{})
	if _, err := publisher.PublishManifest(ctx, now); err != nil {
		t.Fatalf("publish static bundle: %v", err)
	}
	destinationID := "jelgava"
	matchedTrainID := "t1"
	if err := st.InsertStationSighting(ctx, storeStationSighting("bundle-station-sighting", "riga", &destinationID, &matchedTrainID, 77, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}

	server := newStaticBundleTestServer(t, appSvc, bundleDir)
	server.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/map", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public map status: got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Stations []struct {
			ID string `json:"id"`
		} `json:"stations"`
		RecentSightings []struct {
			StationID              string  `json:"stationId"`
			MatchedTrainInstanceID *string `json:"matchedTrainInstanceId"`
		} `json:"recentSightings"`
		SameDaySightings []struct {
			StationID string `json:"stationId"`
		} `json:"sameDaySightings"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public map: %v", err)
	}
	if len(payload.Stations) == 0 {
		t.Fatalf("expected bundled stations in public map payload")
	}
	if len(payload.RecentSightings) != 1 || payload.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected live recent sighting with bundled map, got %+v", payload.RecentSightings)
	}
	if payload.RecentSightings[0].MatchedTrainInstanceID == nil || *payload.RecentSightings[0].MatchedTrainInstanceID != "t1" {
		t.Fatalf("expected matched train in live recent sighting, got %+v", payload.RecentSightings[0])
	}
	if len(payload.SameDaySightings) != 1 || payload.SameDaySightings[0].StationID != "riga" {
		t.Fatalf("expected live same-day sighting with bundled map, got %+v", payload.SameDaySightings)
	}
}

func TestStaticBundlePublicNetworkMapSurvivesLiveOverlayFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.February, 26, 8, 0, 0, 0, loc)
	appSvc, st := newStaticBundleTestServiceWithStore(t, now, loc)
	bundleDir := filepath.Join(t.TempDir(), "bundles")
	publisher := NewStaticBundlePublisher(bundleDir, appSvc, loc, &captureBundleSync{})
	if _, err := publisher.PublishManifest(ctx, now); err != nil {
		t.Fatalf("publish static bundle: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close live store: %v", err)
	}

	server := newStaticBundleTestServer(t, appSvc, bundleDir)
	server.now = func() time.Time { return now }

	req := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/map", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected bundled public map despite live overlay failure, got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Stations []struct {
			ID string `json:"id"`
		} `json:"stations"`
		RecentSightings  []struct{} `json:"recentSightings"`
		SameDaySightings []struct{} `json:"sameDaySightings"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public map: %v", err)
	}
	if len(payload.Stations) == 0 {
		t.Fatalf("expected bundled stations in public map payload")
	}
	if payload.RecentSightings == nil || payload.SameDaySightings == nil {
		t.Fatalf("expected empty sighting arrays after live overlay failure, got recent=%v sameDay=%v", payload.RecentSightings, payload.SameDaySightings)
	}
}

func newStaticBundleTestService(t *testing.T, now time.Time, loc *time.Location) *trainapp.Service {
	t.Helper()

	appSvc, _ := newStaticBundleTestServiceWithStore(t, now, loc)
	return appSvc
}

func newStaticBundleTestServiceWithStore(t *testing.T, now time.Time, loc *time.Location) (*trainapp.Service, *store.SQLiteStore) {
	t.Helper()

	ctx := context.Background()
	db, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "schedule.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	scheduleDir := filepath.Join(t.TempDir(), "schedules")
	if err := os.MkdirAll(scheduleDir, 0o755); err != nil {
		t.Fatalf("create schedule dir: %v", err)
	}
	serviceDate := now.In(loc).Format("2006-01-02")
	latRiga := 56.95
	lngRiga := 24.10
	latJelgava := 56.65
	lngJelgava := 23.72
	snapshot := fmt.Sprintf(`{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"%s",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-02-26T07:30:00+02:00",
      "arrival_at":"2026-02-26T08:15:00+02:00",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"2026-02-26T07:30:00+02:00","latitude":%f,"longitude":%f},
        {"station_name":"Jelgava","seq":2,"arrival_at":"2026-02-26T08:15:00+02:00","latitude":%f,"longitude":%f}
      ]
    }
  ]
}`, serviceDate, latRiga, lngRiga, latJelgava, lngJelgava)
	if err := os.WriteFile(filepath.Join(scheduleDir, serviceDate+".json"), []byte(snapshot), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	mgr := schedule.NewManager(db, scheduleDir, loc, 3)
	if err := mgr.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}
	return trainapp.NewService(
		db,
		mgr,
		ride.NewService(db),
		reports.NewService(db, 3*time.Minute, 90*time.Second),
		loc,
		false,
	), db
}

func newStaticBundleTestServer(t *testing.T, appSvc *trainapp.Service, bundleDir string) *Server {
	t.Helper()

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "train-session-secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	privateKeyPath := filepath.Join(dir, "spacetime-test.key")
	if err := os.WriteFile(privateKeyPath, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatalf("write spacetime private key: %v", err)
	}
	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              "https://example.test/pixel-stack/train",
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
		TrainWebBundleDir:                  bundleDir,
	}, appSvc, i18n.NewCatalog(), time.UTC)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}
