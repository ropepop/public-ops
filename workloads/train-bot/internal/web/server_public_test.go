package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/store"
)

type publicSnapshot struct {
	SourceVersion string                `json:"source_version"`
	Trains        []publicSnapshotTrain `json:"trains"`
}

type publicSnapshotTrain struct {
	ID          string               `json:"id"`
	ServiceDate string               `json:"service_date"`
	FromStation string               `json:"from_station"`
	ToStation   string               `json:"to_station"`
	DepartureAt string               `json:"departure_at"`
	ArrivalAt   string               `json:"arrival_at"`
	Stops       []publicSnapshotStop `json:"stops"`
}

type publicSnapshotStop struct {
	StationName string   `json:"station_name"`
	Seq         int      `json:"seq"`
	ArrivalAt   string   `json:"arrival_at,omitempty"`
	DepartureAt string   `json:"departure_at,omitempty"`
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
}

func TestServeHTTPPublicIncidentsShell(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest("GET", "/pixel-stack/train/incidents", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("unexpected public incidents status: got %d body=%s", res.Code, res.Body.String())
	}
	if body := res.Body.String(); !strings.Contains(body, `mode: "public-incidents"`) {
		t.Fatalf("public incidents shell missing public-incidents mode: %s", body)
	}
}

func TestServeHTTPPublicShellRoutes(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")
	cases := []struct {
		path string
		mode string
	}{
		{path: "/pixel-stack/train", mode: "public-incidents"},
		{path: "/pixel-stack/train/app", mode: "mini-app"},
		{path: "/pixel-stack/train/feed", mode: "public-dashboard"},
		{path: "/pixel-stack/train/departures", mode: "public-dashboard"},
		{path: "/pixel-stack/train/stations", mode: "public-stations"},
		{path: "/pixel-stack/train/map", mode: "public-network-map"},
		{path: "/pixel-stack/train/incidents", mode: "public-incidents"},
		{path: "/pixel-stack/train/t/train-next-0", mode: "public-train"},
		{path: "/pixel-stack/train/t/train-next-0/map", mode: "public-map"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		res := httptest.NewRecorder()
		server.ServeHTTP(res, req)
		if res.Code != 200 {
			t.Fatalf("%s unexpected status: got %d body=%s", tc.path, res.Code, res.Body.String())
		}
		if body := res.Body.String(); !strings.Contains(body, `mode: "`+tc.mode+`"`) {
			t.Fatalf("%s shell missing %s mode: %s", tc.path, tc.mode, body)
		}
	}
}

func TestServeHTTPPublicIncidentsReturnsLivePayload(t *testing.T) {
	t.Parallel()

	server := newPublicDataServer(t, "https://example.test/pixel-stack/train")

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/incidents?limit=0", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public incidents status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Incidents []any `json:"incidents"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public incidents: %v", err)
	}
	if payload.Incidents == nil {
		t.Fatalf("expected incidents payload, got %+v", payload)
	}
}

func TestServeHTTPPublicStationsAndDepartures(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	destinationID := "jelgava"
	matchedTrainID := "train-past"
	if err := st.InsertStationSighting(context.Background(), storeStationSighting("station-sighting-public", "riga", &destinationID, &matchedTrainID, 77, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}

	stationsReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/stations?q=ri", nil)
	stationsRes := httptest.NewRecorder()
	server.ServeHTTP(stationsRes, stationsReq)
	if stationsRes.Code != 200 {
		t.Fatalf("unexpected public stations status: got %d body=%s", stationsRes.Code, stationsRes.Body.String())
	}
	var stationsPayload struct {
		Stations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stations"`
	}
	if err := json.Unmarshal(stationsRes.Body.Bytes(), &stationsPayload); err != nil {
		t.Fatalf("decode public stations: %v", err)
	}
	if len(stationsPayload.Stations) == 0 || stationsPayload.Stations[0].ID != "riga" {
		t.Fatalf("unexpected public stations payload: %+v", stationsPayload.Stations)
	}

	departuresReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/stations/riga/departures", nil)
	departuresRes := httptest.NewRecorder()
	server.ServeHTTP(departuresRes, departuresReq)
	if departuresRes.Code != 200 {
		t.Fatalf("unexpected public departures status: got %d body=%s", departuresRes.Code, departuresRes.Body.String())
	}
	var departuresPayload struct {
		Station struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"station"`
		LastDeparture *struct {
			TrainCard struct {
				Train struct {
					ID string `json:"id"`
				} `json:"train"`
			} `json:"trainCard"`
		} `json:"lastDeparture"`
		Upcoming []struct {
			TrainCard struct {
				Train struct {
					ID string `json:"id"`
				} `json:"train"`
			} `json:"trainCard"`
		} `json:"upcoming"`
		RecentSightings []struct {
			StationID string `json:"stationId"`
		} `json:"recentSightings"`
	}
	if err := json.Unmarshal(departuresRes.Body.Bytes(), &departuresPayload); err != nil {
		t.Fatalf("decode public departures: %v", err)
	}
	if departuresPayload.Station.ID != "riga" {
		t.Fatalf("unexpected station payload: %+v", departuresPayload.Station)
	}
	if departuresPayload.LastDeparture == nil {
		t.Fatalf("expected lastDeparture in public response")
	}
	dayEnd := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), now.Location())
	expectedUpcoming := 0
	for i := 0; i < 10 && expectedUpcoming < 8; i++ {
		if !now.Add(time.Duration(i+1) * 15 * time.Minute).After(dayEnd) {
			expectedUpcoming++
		}
	}
	if len(departuresPayload.Upcoming) != expectedUpcoming {
		t.Fatalf("expected %d upcoming departures for the remainder of the service day, got %d", expectedUpcoming, len(departuresPayload.Upcoming))
	}
	if len(departuresPayload.RecentSightings) != 1 || departuresPayload.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected recent station sighting in departures payload, got %+v", departuresPayload.RecentSightings)
	}

	privateReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/stations?q=ri", nil)
	privateRes := httptest.NewRecorder()
	server.ServeHTTP(privateRes, privateReq)
	if privateRes.Code != 401 {
		t.Fatalf("expected private stations endpoint to require auth, got %d body=%s", privateRes.Code, privateRes.Body.String())
	}
}

func TestServeHTTPPublicMapReturnsLivePayload(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	destinationID := "jelgava"
	matchedTrainID := "train-past"
	if err := st.InsertStationSighting(context.Background(), storeStationSighting("station-sighting-public-map", "riga", &destinationID, &matchedTrainID, 77, now.Add(-2*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/map", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public map status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Stations []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stations"`
		RecentSightings []struct {
			StationID              string  `json:"stationId"`
			MatchedTrainInstanceID *string `json:"matchedTrainInstanceId"`
		} `json:"recentSightings"`
		SameDaySightings []struct {
			StationID string `json:"stationId"`
		} `json:"sameDaySightings"`
		Schedule struct {
			Available            bool   `json:"available"`
			EffectiveServiceDate string `json:"effectiveServiceDate"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public map: %v", err)
	}
	hasRiga := false
	for _, station := range payload.Stations {
		if station.ID == "riga" {
			hasRiga = true
			break
		}
	}
	if !hasRiga {
		t.Fatalf("unexpected station payload: %+v", payload.Stations)
	}
	if !payload.Schedule.Available || payload.Schedule.EffectiveServiceDate == "" {
		t.Fatalf("unexpected schedule payload: %+v", payload.Schedule)
	}
	if len(payload.RecentSightings) != 1 || payload.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected recent station sighting in network map payload, got %+v", payload.RecentSightings)
	}
	if payload.RecentSightings[0].MatchedTrainInstanceID == nil || *payload.RecentSightings[0].MatchedTrainInstanceID != "train-past" {
		t.Fatalf("expected matched train in recent sightings, got %+v", payload.RecentSightings[0])
	}
	if len(payload.SameDaySightings) != 1 || payload.SameDaySightings[0].StationID != "riga" {
		t.Fatalf("expected same-day station sighting in network map payload, got %+v", payload.SameDaySightings)
	}
}

func TestServeHTTPStationSightingDestinationsReturnsLivePayload(t *testing.T) {
	t.Parallel()

	server, _, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/stations/riga/sighting-destinations", nil)
	req.AddCookie(testSessionCookie(t, server, 77, "en", now))
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected sighting destinations status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Stations []struct {
			ID string `json:"id"`
		} `json:"stations"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode sighting destinations: %v", err)
	}
	if len(payload.Stations) == 0 {
		t.Fatalf("expected station sighting destinations, got %+v", payload)
	}
}

func newPublicDataServer(t *testing.T, publicBaseURL string) *Server {
	server, _, _ := newPublicDataServerWithStore(t, publicBaseURL)
	return server
}

func newPublicDataServerWithStore(t *testing.T, publicBaseURL string) (*Server, *store.SQLiteStore, time.Time) {
	return newPublicDataServerWithStoreAndTrainCount(t, publicBaseURL, 10)
}

func newPublicDataServerWithStoreAndTrainCount(t *testing.T, publicBaseURL string, futureTrainCount int) (*Server, *store.SQLiteStore, time.Time) {
	t.Helper()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := stableRigaMidday(time.Now().In(loc))
	serviceDate := now.Format("2006-01-02")

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "train-session-secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	privateKeyPath := filepath.Join(dir, "spacetime-test.key")
	if err := os.WriteFile(privateKeyPath, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatalf("write spacetime private key: %v", err)
	}
	dbPath := filepath.Join(dir, "train-bot.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotPath := filepath.Join(dir, serviceDate+".json")
	trains := []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-past", serviceDate, "Riga", "Jelgava", now.Add(-20*time.Minute)),
	}
	for i := 0; i < futureTrainCount; i++ {
		trains = append(trains, buildPublicSnapshotTrain(
			"train-next-"+strconv.Itoa(i),
			serviceDate,
			"Riga",
			"Stop "+strconv.Itoa(i),
			now.Add(time.Duration(i+1)*15*time.Minute),
		))
	}
	payload, err := json.Marshal(publicSnapshot{
		SourceVersion: "server-public-test",
		Trains:        trains,
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, dir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	appSvc := trainapp.NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)
	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              publicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, appSvc, i18n.NewCatalog(), loc)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server.now = func() time.Time { return now }
	return server, st, now
}

func newPublicDataServerWithLoadedSnapshot(t *testing.T, publicBaseURL string, now time.Time, loadAt time.Time, trains []publicSnapshotTrain) (*Server, *store.SQLiteStore) {
	t.Helper()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	dir := t.TempDir()
	secretPath := filepath.Join(dir, "train-session-secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	privateKeyPath := filepath.Join(dir, "spacetime-test.key")
	if err := os.WriteFile(privateKeyPath, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatalf("write spacetime private key: %v", err)
	}
	dbPath := filepath.Join(dir, "train-bot.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	serviceDate := loadAt.In(loc).Format("2006-01-02")
	if len(trains) == 0 {
		trains = []publicSnapshotTrain{
			buildPublicSnapshotTrain("train-fallback", serviceDate, "Riga", "Jelgava", loadAt.Add(time.Hour)),
		}
	}
	snapshotPath := filepath.Join(dir, serviceDate+".json")
	payload, err := json.Marshal(publicSnapshot{
		SourceVersion: "server-public-fallback-test",
		Trains:        trains,
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, dir, loc, 3)
	if err := manager.LoadToday(ctx, loadAt.In(loc)); err != nil {
		t.Fatalf("load schedule: %v", err)
	}

	appSvc := trainapp.NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)
	server, err := NewServer(config.Config{
		BotToken:                           "bot-token",
		TrainWebEnabled:                    true,
		TrainWebBindAddr:                   "127.0.0.1",
		TrainWebPort:                       9317,
		TrainWebPublicBaseURL:              publicBaseURL,
		TrainWebSessionSecretFile:          secretPath,
		TrainWebTelegramAuthMaxAgeSec:      300,
		TrainWebSpacetimeHost:              "https://stdb.example.test",
		TrainWebSpacetimeDatabase:          "train-bot",
		TrainWebSpacetimeOIDCAudience:      "train-bot-web",
		TrainWebSpacetimeJWTPrivateKeyFile: privateKeyPath,
		TrainWebSpacetimeTokenTTLSec:       24 * 60 * 60,
	}, appSvc, i18n.NewCatalog(), loc)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	server.now = func() time.Time { return now }
	return server, st
}

func TestServeHTTPPublicDashboardLimitZeroReturnsAllTodayTrains(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := stableRigaMidday(time.Now().In(loc))
	serviceDate := now.Format("2006-01-02")
	trains := make([]publicSnapshotTrain, 0, 75)
	for i := 0; i < 75; i++ {
		trains = append(trains, buildPublicSnapshotTrain(
			"train-bulk-"+strconv.Itoa(i),
			serviceDate,
			"Riga",
			"Stop "+strconv.Itoa(i),
			now.Add(time.Duration(i+1)*time.Second),
		))
	}
	server, st := newAuthenticatedDataServerWithTrains(t, "https://example.test/pixel-stack/train", now, trains)
	if err := st.CheckInUser(context.Background(), 44, "train-bulk-0", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	defaultReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard", nil)
	defaultRes := httptest.NewRecorder()
	server.ServeHTTP(defaultRes, defaultReq)
	if defaultRes.Code != 200 {
		t.Fatalf("unexpected default dashboard status: got %d body=%s", defaultRes.Code, defaultRes.Body.String())
	}
	var defaultPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(defaultRes.Body.Bytes(), &defaultPayload); err != nil {
		t.Fatalf("decode default dashboard payload: %v", err)
	}
	if len(defaultPayload.Trains) != 60 {
		t.Fatalf("expected default dashboard limit of 60, got %d", len(defaultPayload.Trains))
	}
	if defaultPayload.Trains[0].Train.ID != "train-bulk-0" || defaultPayload.Trains[0].Riders != 1 {
		t.Fatalf("expected rider count for first dashboard train, got %+v", defaultPayload.Trains[0])
	}

	allReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	allRes := httptest.NewRecorder()
	server.ServeHTTP(allRes, allReq)
	if allRes.Code != 200 {
		t.Fatalf("unexpected all dashboard status: got %d body=%s", allRes.Code, allRes.Body.String())
	}
	var allPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(allRes.Body.Bytes(), &allPayload); err != nil {
		t.Fatalf("decode limit=0 dashboard payload: %v", err)
	}
	if len(allPayload.Trains) != 75 {
		t.Fatalf("expected limit=0 dashboard to return all 75 trains, got %d", len(allPayload.Trains))
	}
	if allPayload.Trains[0].Train.ID != "train-bulk-0" || allPayload.Trains[0].Riders != 1 {
		t.Fatalf("expected rider count for full dashboard payload, got %+v", allPayload.Trains[0])
	}
}

func TestServeHTTPPublicServiceDayTrainsIncludesDepartedTrainsOutsideDashboardWindow(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := stableRigaMidday(time.Now().In(loc))
	serviceDate := now.Format("2006-01-02")
	server, st := newAuthenticatedDataServerWithTrains(t, "https://example.test/pixel-stack/train", now, []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-past", serviceDate, "Riga", "Jelgava", now.Add(-45*time.Minute)),
		buildPublicSnapshotTrain("train-next", serviceDate, "Riga", "Tukums", now.Add(15*time.Minute)),
	})
	if err := st.CheckInUser(context.Background(), 44, "train-next", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	dashboardReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/dashboard?limit=0", nil)
	dashboardRes := httptest.NewRecorder()
	server.ServeHTTP(dashboardRes, dashboardReq)
	if dashboardRes.Code != 200 {
		t.Fatalf("unexpected dashboard status: got %d body=%s", dashboardRes.Code, dashboardRes.Body.String())
	}
	var dashboardPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(dashboardRes.Body.Bytes(), &dashboardPayload); err != nil {
		t.Fatalf("decode dashboard payload: %v", err)
	}
	if len(dashboardPayload.Trains) != 1 || dashboardPayload.Trains[0].Train.ID != "train-next" {
		t.Fatalf("expected dashboard to keep only the future train, got %+v", dashboardPayload.Trains)
	}
	if dashboardPayload.Trains[0].Riders != 1 {
		t.Fatalf("expected rider count on dashboard payload, got %+v", dashboardPayload.Trains[0])
	}

	serviceDayReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/service-day-trains", nil)
	serviceDayRes := httptest.NewRecorder()
	server.ServeHTTP(serviceDayRes, serviceDayReq)
	if serviceDayRes.Code != 200 {
		t.Fatalf("unexpected service day trains status: got %d body=%s", serviceDayRes.Code, serviceDayRes.Body.String())
	}
	var serviceDayPayload struct {
		Trains []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
	}
	if err := json.Unmarshal(serviceDayRes.Body.Bytes(), &serviceDayPayload); err != nil {
		t.Fatalf("decode service day payload: %v", err)
	}
	if len(serviceDayPayload.Trains) != 2 {
		t.Fatalf("expected service day trains to include both departures, got %d", len(serviceDayPayload.Trains))
	}
	if serviceDayPayload.Trains[0].Train.ID != "train-past" || serviceDayPayload.Trains[1].Train.ID != "train-next" {
		t.Fatalf("unexpected service day train order: %+v", serviceDayPayload.Trains)
	}
	if serviceDayPayload.Trains[1].Riders != 1 {
		t.Fatalf("expected rider count on service day payload, got %+v", serviceDayPayload.Trains[1])
	}
}

func buildPublicSnapshotTrain(id string, serviceDate string, fromStation string, toStation string, departureAt time.Time) publicSnapshotTrain {
	arrivalAt := departureAt.Add(45 * time.Minute)
	return publicSnapshotTrain{
		ID:          id,
		ServiceDate: serviceDate,
		FromStation: fromStation,
		ToStation:   toStation,
		DepartureAt: departureAt.Format(time.RFC3339),
		ArrivalAt:   arrivalAt.Format(time.RFC3339),
		Stops: []publicSnapshotStop{
			{
				StationName: fromStation,
				Seq:         1,
				DepartureAt: departureAt.Format(time.RFC3339),
				Latitude:    publicFloatPtr(56.9496),
				Longitude:   publicFloatPtr(24.1052),
			},
			{
				StationName: toStation,
				Seq:         2,
				ArrivalAt:   arrivalAt.Format(time.RFC3339),
				Latitude:    publicFloatPtr(56.6511),
				Longitude:   publicFloatPtr(23.7128),
			},
		},
	}
}

func TestServeHTTPPublicTrainStopsIncludesCoordinatesAndSightings(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	destinationID := "stop-0"
	matchedTrainID := "train-next-0"
	if err := st.InsertStationSighting(context.Background(), storeStationSighting("station-sighting-map", "riga", &destinationID, &matchedTrainID, 91, now.Add(-1*time.Minute))); err != nil {
		t.Fatalf("insert station sighting: %v", err)
	}
	if err := st.CheckInUser(context.Background(), 44, "train-next-0", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/trains/train-next-0/stops", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("unexpected public train stops status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		TrainCard struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trainCard"`
		Train struct {
			ID string `json:"id"`
		} `json:"train"`
		Stops []struct {
			StationID string   `json:"stationId"`
			Latitude  *float64 `json:"latitude"`
			Longitude *float64 `json:"longitude"`
		} `json:"stops"`
		StationSightings []struct {
			MatchedTrainInstanceID *string `json:"matchedTrainInstanceId"`
		} `json:"stationSightings"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public train stops: %v", err)
	}
	if payload.Train.ID != "train-next-0" {
		t.Fatalf("unexpected train id: %q", payload.Train.ID)
	}
	if payload.TrainCard.Train.ID != "train-next-0" || payload.TrainCard.Riders != 1 {
		t.Fatalf("expected train card riders in stops payload, got %+v", payload.TrainCard)
	}
	if len(payload.Stops) != 2 {
		t.Fatalf("expected 2 stops, got %d", len(payload.Stops))
	}
	if payload.Stops[0].StationID != "riga" || payload.Stops[0].Latitude == nil || payload.Stops[0].Longitude == nil {
		t.Fatalf("expected coordinates on first stop, got %+v", payload.Stops[0])
	}
	if len(payload.StationSightings) != 1 || payload.StationSightings[0].MatchedTrainInstanceID == nil || *payload.StationSightings[0].MatchedTrainInstanceID != "train-next-0" {
		t.Fatalf("expected matched station sighting in stops payload, got %+v", payload.StationSightings)
	}
}

func TestServeHTTPPublicTrainIncludesRiderCount(t *testing.T) {
	t.Parallel()

	server, st, now := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	if err := st.CheckInUser(context.Background(), 44, "train-next-0", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/public/trains/train-next-0", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("unexpected public train status: got %d body=%s", res.Code, res.Body.String())
	}

	var payload struct {
		Riders int `json:"riders"`
		Train  struct {
			ID string `json:"id"`
		} `json:"train"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode public train payload: %v", err)
	}
	if payload.Train.ID != "train-next-0" || payload.Riders != 1 {
		t.Fatalf("expected public train rider count, got %+v", payload)
	}
}

func TestServeHTTPHealthIncludesReleaseMetadata(t *testing.T) {
	t.Parallel()

	server, _, _ := newPublicDataServerWithStore(t, "https://example.test/pixel-stack/train")
	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/health", nil)
	res := httptest.NewRecorder()

	server.ServeHTTP(res, req)

	if res.Code != 200 {
		t.Fatalf("unexpected health status: got %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected cache-control: %q", got)
	}
	if got := res.Header().Get("X-Train-Bot-Commit"); got != server.release.Commit {
		t.Fatalf("unexpected commit header: got %q want %q", got, server.release.Commit)
	}

	var payload struct {
		Ready           bool   `json:"ready"`
		ReadinessReason string `json:"readinessReason"`
		Schedule        struct {
			Available      bool `json:"available"`
			FallbackActive bool `json:"fallbackActive"`
			SameDayFresh   bool `json:"sameDayFresh"`
		} `json:"schedule"`
		Version struct {
			Commit    string `json:"commit"`
			BuildTime string `json:"buildTime"`
			Dirty     string `json:"dirty"`
		} `json:"version"`
		Runtime struct {
			InstanceID string `json:"instanceId"`
		} `json:"runtime"`
		Assets struct {
			AppJSSha256  string `json:"appJsSha256"`
			AppCSSSha256 string `json:"appCssSha256"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if payload.Version.Commit != server.release.Commit {
		t.Fatalf("unexpected version.commit: got %q want %q", payload.Version.Commit, server.release.Commit)
	}
	if payload.Version.BuildTime != server.release.BuildTime {
		t.Fatalf("unexpected version.buildTime: got %q want %q", payload.Version.BuildTime, server.release.BuildTime)
	}
	if payload.Version.Dirty != server.release.Dirty {
		t.Fatalf("unexpected version.dirty: got %q want %q", payload.Version.Dirty, server.release.Dirty)
	}
	if payload.Runtime.InstanceID != server.release.Instance {
		t.Fatalf("unexpected runtime.instanceId: got %q want %q", payload.Runtime.InstanceID, server.release.Instance)
	}
	if payload.Assets.AppJSSha256 != server.release.AppJSHash {
		t.Fatalf("unexpected assets.appJsSha256: got %q want %q", payload.Assets.AppJSSha256, server.release.AppJSHash)
	}
	if payload.Assets.AppCSSSha256 != server.release.AppCSSHash {
		t.Fatalf("unexpected assets.appCssSha256: got %q want %q", payload.Assets.AppCSSSha256, server.release.AppCSSHash)
	}
	if !payload.Ready || payload.ReadinessReason != "same-day schedule loaded" {
		t.Fatalf("expected ready same-day health payload, got %+v", payload)
	}
	if !payload.Schedule.Available || !payload.Schedule.SameDayFresh || payload.Schedule.FallbackActive {
		t.Fatalf("unexpected healthy schedule payload: %+v", payload.Schedule)
	}
}

func TestServeHTTPReadyReturnsOKDuringAllowedFallback(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	loadAt := time.Date(2026, 2, 27, 23, 30, 0, 0, loc)
	now := time.Date(2026, 2, 28, 2, 59, 0, 0, loc)
	serviceDate := loadAt.Format("2006-01-02")
	server, _ := newPublicDataServerWithLoadedSnapshot(t, "https://example.test/pixel-stack/train", now, loadAt, []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-fallback", serviceDate, "Riga", "Jelgava", time.Date(2026, 2, 28, 1, 30, 0, 0, loc)),
	})

	req := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/ready", nil)
	res := httptest.NewRecorder()
	server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected fallback readiness to succeed, got %d body=%s", res.Code, res.Body.String())
	}
	var payload struct {
		Ready                  bool   `json:"ready"`
		ReadinessReason        string `json:"readinessReason"`
		ScheduleAvailable      bool   `json:"scheduleAvailable"`
		ScheduleFallbackActive bool   `json:"scheduleFallbackActive"`
		ScheduleSameDayFresh   bool   `json:"scheduleSameDayFresh"`
		Schedule               struct {
			RequestedServiceDate string `json:"requestedServiceDate"`
			EffectiveServiceDate string `json:"effectiveServiceDate"`
			LoadedServiceDate    string `json:"loadedServiceDate"`
			FallbackActive       bool   `json:"fallbackActive"`
			CutoffHour           int    `json:"cutoffHour"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode ready payload: %v", err)
	}
	if !payload.Ready || payload.ReadinessReason != "previous-day fallback active before cutoff" {
		t.Fatalf("unexpected fallback readiness payload: %+v", payload)
	}
	if !payload.ScheduleAvailable || !payload.ScheduleFallbackActive || payload.ScheduleSameDayFresh {
		t.Fatalf("unexpected fallback flags: %+v", payload)
	}
	if payload.Schedule.RequestedServiceDate != "2026-02-28" || payload.Schedule.EffectiveServiceDate != serviceDate || payload.Schedule.LoadedServiceDate != serviceDate {
		t.Fatalf("unexpected fallback schedule dates: %+v", payload.Schedule)
	}
	if !payload.Schedule.FallbackActive || payload.Schedule.CutoffHour != 3 {
		t.Fatalf("unexpected fallback schedule context: %+v", payload.Schedule)
	}
}

func TestServeHTTPHealthAndReadyExposeStaleScheduleAfterCutoff(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	loadAt := time.Date(2026, 2, 27, 23, 30, 0, 0, loc)
	now := time.Date(2026, 2, 28, 4, 0, 0, 0, loc)
	serviceDate := loadAt.Format("2006-01-02")
	server, _ := newPublicDataServerWithLoadedSnapshot(t, "https://example.test/pixel-stack/train", now, loadAt, []publicSnapshotTrain{
		buildPublicSnapshotTrain("train-stale", serviceDate, "Riga", "Jelgava", time.Date(2026, 2, 28, 1, 30, 0, 0, loc)),
	})

	healthReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/health", nil)
	healthRes := httptest.NewRecorder()
	server.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("expected liveness endpoint to stay up, got %d body=%s", healthRes.Code, healthRes.Body.String())
	}

	var healthPayload struct {
		Ready                  bool   `json:"ready"`
		ReadinessReason        string `json:"readinessReason"`
		ScheduleAvailable      bool   `json:"scheduleAvailable"`
		ScheduleFallbackActive bool   `json:"scheduleFallbackActive"`
		ScheduleSameDayFresh   bool   `json:"scheduleSameDayFresh"`
		LoadedServiceDate      string `json:"loadedServiceDate"`
		StaleLoadedServiceDate string `json:"staleLoadedServiceDate"`
	}
	if err := json.Unmarshal(healthRes.Body.Bytes(), &healthPayload); err != nil {
		t.Fatalf("decode health payload: %v", err)
	}
	if healthPayload.Ready {
		t.Fatalf("expected stale after-cutoff health to be not ready, got %+v", healthPayload)
	}
	if !strings.Contains(healthPayload.ReadinessReason, "outside the active service window") {
		t.Fatalf("expected stale readiness reason, got %+v", healthPayload)
	}
	if healthPayload.ScheduleAvailable || healthPayload.ScheduleFallbackActive || healthPayload.ScheduleSameDayFresh {
		t.Fatalf("expected stale schedule flags to be false, got %+v", healthPayload)
	}
	if healthPayload.LoadedServiceDate != serviceDate || healthPayload.StaleLoadedServiceDate != serviceDate {
		t.Fatalf("expected stale loaded service date to be surfaced, got %+v", healthPayload)
	}

	readyReq := httptest.NewRequest("GET", "/pixel-stack/train/api/v1/ready", nil)
	readyRes := httptest.NewRecorder()
	server.ServeHTTP(readyRes, readyReq)
	if readyRes.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected stale after-cutoff readiness to fail, got %d body=%s", readyRes.Code, readyRes.Body.String())
	}
}

func publicFloatPtr(v float64) *float64 {
	return &v
}

func stableRigaMidday(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
}

func storeStationSighting(id string, stationID string, destinationStationID *string, matchedTrainID *string, userID int64, createdAt time.Time) domain.StationSighting {
	return domain.StationSighting{
		ID:                     id,
		StationID:              stationID,
		DestinationStationID:   destinationStationID,
		MatchedTrainInstanceID: matchedTrainID,
		UserID:                 userID,
		CreatedAt:              createdAt,
	}
}
