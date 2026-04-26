package web

import (
	"context"
	"encoding/json"
	"fmt"
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

	dashboardReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/train/api/v1/public/dashboard?limit=1", nil)
	dashboardRes := httptest.NewRecorder()
	server.ServeHTTP(dashboardRes, dashboardReq)
	if dashboardRes.Code != http.StatusOK {
		t.Fatalf("unexpected dashboard status: got %d body=%s", dashboardRes.Code, dashboardRes.Body.String())
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

func TestStaticBundleServerPreservesRiderCountWhenTrainStopsFallbackUsesBundle(t *testing.T) {
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
	if payload.TrainCard.Riders != 1 {
		t.Fatalf("expected live rider count in public train stops payload, got %d", payload.TrainCard.Riders)
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
