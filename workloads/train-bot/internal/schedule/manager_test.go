package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/store"
)

func setupScheduleStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "schedule.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

type failingScheduleStore struct {
	*store.SQLiteStore
	failStops error
}

func (s *failingScheduleStore) UpsertTrainStops(ctx context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error {
	if s.failStops != nil {
		return s.failStops
	}
	return s.SQLiteStore.UpsertTrainStops(ctx, serviceDate, stopsByTrain)
}

func TestManagerListStationsAndStationWindow(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 2, 26, 8, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")
	dir := t.TempDir()
	path := filepath.Join(dir, serviceDate+".json")
	payload := `{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"` + serviceDate + `",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-02-26T07:30:00+02:00",
      "arrival_at":"2026-02-26T08:45:00+02:00",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"2026-02-26T07:30:00+02:00"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"2026-02-26T08:15:00+02:00"}
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	mgr := NewManager(st, dir, loc, 3)
	if err := mgr.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}
	stations, err := mgr.ListStations(ctx, now)
	if err != nil {
		t.Fatalf("list stations: %v", err)
	}
	if len(stations) != 2 {
		t.Fatalf("expected 2 stations, got %d", len(stations))
	}
	trains, err := mgr.ListByStationWindow(ctx, now, "jelgava", 2*time.Hour)
	if err != nil {
		t.Fatalf("list by station window: %v", err)
	}
	if len(trains) != 1 {
		t.Fatalf("expected 1 train, got %d", len(trains))
	}
	if trains[0].Train.ID != "t1" {
		t.Fatalf("expected train t1, got %s", trains[0].Train.ID)
	}

	destinations, err := mgr.ListReachableDestinations(ctx, now, "riga")
	if err != nil {
		t.Fatalf("list reachable destinations: %v", err)
	}
	if len(destinations) == 0 {
		t.Fatalf("expected reachable destinations from riga")
	}

	terminalDestinations, err := mgr.ListTerminalDestinations(ctx, now, "riga")
	if err != nil {
		t.Fatalf("list terminal destinations: %v", err)
	}
	if len(terminalDestinations) != 1 || terminalDestinations[0].ID != "jelgava" {
		t.Fatalf("expected jelgava terminal destination, got %+v", terminalDestinations)
	}

	routeTrains, err := mgr.ListRouteWindowTrains(ctx, now, "riga", "jelgava", 4*time.Hour)
	if err != nil {
		t.Fatalf("list route trains: %v", err)
	}
	if len(routeTrains) != 1 {
		t.Fatalf("expected 1 route train, got %d", len(routeTrains))
	}
}

func TestLoadTodayFallbackToStoredData(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 2, 25, 9, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            "train-2026-02-25-1",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   now.Add(30 * time.Minute),
		ArrivalAt:     now.Add(90 * time.Minute),
		SourceVersion: "seed",
	}
	if err := st.UpsertTrainInstances(ctx, serviceDate, "seed", []domain.TrainInstance{train}); err != nil {
		t.Fatalf("upsert seed: %v", err)
	}

	mgr := NewManager(st, filepath.Join(t.TempDir(), "missing-dir"), loc, 3)
	if err := mgr.LoadToday(ctx, now); err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	available, _ := mgr.Availability()
	if !available {
		t.Fatalf("expected availability true from stored fallback")
	}
	if !mgr.IsFreshFor(now) {
		t.Fatalf("expected schedule freshness true from stored fallback")
	}
	if got := mgr.LoadedServiceDate(); got != serviceDate {
		t.Fatalf("expected loaded service date %s, got %s", serviceDate, got)
	}

	list, err := mgr.ListByWindow(ctx, now, "today")
	if err != nil {
		t.Fatalf("list by window: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one train, got %d", len(list))
	}
}

func TestLoadTodayKeepsExistingServiceDateWhenStopPublishFails(t *testing.T) {
	ctx := context.Background()
	baseStore := setupScheduleStore(t)
	defer baseStore.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 3, 25, 9, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            "train-2026-03-25-1",
		ServiceDate:   serviceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   now.Add(30 * time.Minute),
		ArrivalAt:     now.Add(90 * time.Minute),
		SourceVersion: "seed",
	}
	if err := baseStore.UpsertTrainInstances(ctx, serviceDate, "seed", []domain.TrainInstance{train}); err != nil {
		t.Fatalf("seed train instances: %v", err)
	}

	failingStore := &failingScheduleStore{
		SQLiteStore: baseStore,
		failStops:   errors.New("spacetime staged import failed"),
	}
	dir := t.TempDir()
	path := filepath.Join(dir, serviceDate+".json")
	payload := `{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"train-2026-03-25-1",
      "service_date":"` + serviceDate + `",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-03-25T09:30:00+02:00",
      "arrival_at":"2026-03-25T10:45:00+02:00",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"2026-03-25T09:30:00+02:00"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"2026-03-25T10:45:00+02:00"}
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	mgr := NewManager(failingStore, dir, loc, 3)
	if err := mgr.LoadToday(ctx, now); err != nil {
		t.Fatalf("expected stored service date to stay available, got %v", err)
	}
	available, lastErr := mgr.Availability()
	if !available {
		t.Fatalf("expected schedule availability to stay true")
	}
	if lastErr != nil {
		t.Fatalf("expected store-first load to skip snapshot reload errors, got %v", lastErr)
	}
	if got := mgr.LoadedServiceDate(); got != serviceDate {
		t.Fatalf("expected loaded service date %s, got %s", serviceDate, got)
	}
}

func TestLoadTodayRejectsMismatchedServiceDateSnapshot(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, 2, 27, 9, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")
	dir := t.TempDir()
	path := filepath.Join(dir, serviceDate+".json")

	// Snapshot file name matches the target day, but embedded trains are from the previous day.
	payload := `{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"2026-02-26",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-02-26T07:30:00+02:00",
      "arrival_at":"2026-02-26T08:45:00+02:00"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	mgr := NewManager(st, dir, loc, 3)
	if err := mgr.LoadToday(ctx, now); err == nil {
		t.Fatalf("expected mismatch load failure")
	}
	available, lastErr := mgr.Availability()
	if available {
		t.Fatalf("expected schedule unavailable")
	}
	if lastErr == nil {
		t.Fatalf("expected last error to be tracked")
	}
	if _, err := mgr.ListByWindow(ctx, now, "today"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestManagerReadApisRejectStaleLoadedServiceDate(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	yesterday := time.Date(2026, 2, 27, 9, 0, 0, 0, loc)
	today := yesterday.Add(24 * time.Hour)
	serviceDate := yesterday.Format("2006-01-02")
	dir := t.TempDir()
	path := filepath.Join(dir, serviceDate+".json")
	payload := `{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"` + serviceDate + `",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-02-27T07:30:00+02:00",
      "arrival_at":"2026-02-27T08:45:00+02:00",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"2026-02-27T07:30:00+02:00"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"2026-02-27T08:15:00+02:00"}
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	mgr := NewManager(st, dir, loc, 3)
	if err := mgr.LoadToday(ctx, yesterday); err != nil {
		t.Fatalf("load yesterday: %v", err)
	}
	if mgr.IsFreshFor(today) {
		t.Fatalf("expected stale manager after day rollover")
	}
	if got := mgr.LoadedServiceDate(); got != serviceDate {
		t.Fatalf("expected loaded service date %s, got %s", serviceDate, got)
	}

	if _, err := mgr.ListByWindow(ctx, today, "today"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected stale ListByWindow to return ErrUnavailable, got %v", err)
	}
	if _, err := mgr.ListStations(ctx, today); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected stale ListStations to return ErrUnavailable, got %v", err)
	}
	if _, err := mgr.ListByStationWindow(ctx, today, "riga", 2*time.Hour); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected stale ListByStationWindow to return ErrUnavailable, got %v", err)
	}
	if _, err := mgr.ListReachableDestinations(ctx, today, "riga"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected stale ListReachableDestinations to return ErrUnavailable, got %v", err)
	}
	if _, err := mgr.ListRouteWindowTrains(ctx, today, "riga", "jelgava", 2*time.Hour); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected stale ListRouteWindowTrains to return ErrUnavailable, got %v", err)
	}
}

func TestManagerAllowsYesterdayFallbackBeforeCutoff(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	yesterday := time.Date(2026, 2, 27, 23, 30, 0, 0, loc)
	beforeCutoff := time.Date(2026, 2, 28, 1, 15, 0, 0, loc)
	serviceDate := yesterday.Format("2006-01-02")
	dir := t.TempDir()
	path := filepath.Join(dir, serviceDate+".json")
	payload := `{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"` + serviceDate + `",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-02-28T01:30:00+02:00",
      "arrival_at":"2026-02-28T02:15:00+02:00",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"2026-02-28T01:30:00+02:00"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"2026-02-28T02:15:00+02:00"}
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	mgr := NewManager(st, dir, loc, 3)
	if err := mgr.LoadToday(ctx, yesterday); err != nil {
		t.Fatalf("load yesterday: %v", err)
	}

	access := mgr.AccessContext(beforeCutoff)
	if !access.FallbackActive {
		t.Fatalf("expected fallback to be active before cutoff, got %+v", access)
	}
	if access.EffectiveServiceDate != serviceDate {
		t.Fatalf("expected effective service date %s, got %s", serviceDate, access.EffectiveServiceDate)
	}
	if access.SameDayFresh {
		t.Fatalf("expected same-day freshness to remain false during fallback")
	}

	list, err := mgr.ListByWindow(ctx, beforeCutoff, "next_hour")
	if err != nil {
		t.Fatalf("expected fallback ListByWindow to succeed, got %v", err)
	}
	if len(list) != 1 || list[0].ID != "t1" {
		t.Fatalf("unexpected fallback departures: %+v", list)
	}
}

func TestManagerAccessContextRespectsRigaCutoffBoundaries(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	yesterday := time.Date(2026, 2, 27, 23, 30, 0, 0, loc)
	serviceDate := yesterday.Format("2006-01-02")
	dir := t.TempDir()
	path := filepath.Join(dir, serviceDate+".json")
	payload := `{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"` + serviceDate + `",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"2026-02-28T01:30:00+02:00",
      "arrival_at":"2026-02-28T02:15:00+02:00",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"2026-02-28T01:30:00+02:00"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"2026-02-28T02:15:00+02:00"}
      ]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	mgr := NewManager(st, dir, loc, 3)
	if err := mgr.LoadToday(ctx, yesterday); err != nil {
		t.Fatalf("load yesterday: %v", err)
	}

	cases := []struct {
		name          string
		now           time.Time
		wantAvailable bool
		wantFallback  bool
		wantSameDay   bool
		wantEffective string
		wantRequested string
	}{
		{
			name:          "0030",
			now:           time.Date(2026, 2, 28, 0, 30, 0, 0, loc),
			wantAvailable: true,
			wantFallback:  true,
			wantSameDay:   false,
			wantEffective: serviceDate,
			wantRequested: "2026-02-28",
		},
		{
			name:          "0259",
			now:           time.Date(2026, 2, 28, 2, 59, 0, 0, loc),
			wantAvailable: true,
			wantFallback:  true,
			wantSameDay:   false,
			wantEffective: serviceDate,
			wantRequested: "2026-02-28",
		},
		{
			name:          "0300",
			now:           time.Date(2026, 2, 28, 3, 0, 0, 0, loc),
			wantAvailable: false,
			wantFallback:  false,
			wantSameDay:   false,
			wantEffective: "",
			wantRequested: "2026-02-28",
		},
		{
			name:          "0400",
			now:           time.Date(2026, 2, 28, 4, 0, 0, 0, loc),
			wantAvailable: false,
			wantFallback:  false,
			wantSameDay:   false,
			wantEffective: "",
			wantRequested: "2026-02-28",
		},
	}

	for _, tc := range cases {
		access := mgr.AccessContext(tc.now)
		if access.RequestedServiceDate != tc.wantRequested {
			t.Fatalf("%s requested service date: got %s want %s", tc.name, access.RequestedServiceDate, tc.wantRequested)
		}
		if access.LoadedServiceDate != serviceDate {
			t.Fatalf("%s loaded service date: got %s want %s", tc.name, access.LoadedServiceDate, serviceDate)
		}
		if access.Available != tc.wantAvailable || access.FallbackActive != tc.wantFallback || access.SameDayFresh != tc.wantSameDay {
			t.Fatalf("%s unexpected access flags: %+v", tc.name, access)
		}
		if access.EffectiveServiceDate != tc.wantEffective {
			t.Fatalf("%s effective service date: got %s want %s", tc.name, access.EffectiveServiceDate, tc.wantEffective)
		}
		if access.CutoffHour != 3 {
			t.Fatalf("%s cutoff hour: got %d want 3", tc.name, access.CutoffHour)
		}
	}
}

func TestManagerLoadForAccessFallsBackToYesterdayBeforeCutoff(t *testing.T) {
	ctx := context.Background()
	st := setupScheduleStore(t)
	defer st.Close()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	beforeCutoff := time.Date(2026, 2, 28, 1, 15, 0, 0, loc)
	yesterdayServiceDate := beforeCutoff.AddDate(0, 0, -1).Format("2006-01-02")
	train := domain.TrainInstance{
		ID:            "train-yesterday",
		ServiceDate:   yesterdayServiceDate,
		FromStation:   "Riga",
		ToStation:     "Jelgava",
		DepartureAt:   time.Date(2026, 2, 28, 0, 30, 0, 0, loc),
		ArrivalAt:     time.Date(2026, 2, 28, 1, 45, 0, 0, loc),
		SourceVersion: "seed",
	}
	if err := st.UpsertTrainInstances(ctx, yesterdayServiceDate, "seed", []domain.TrainInstance{train}); err != nil {
		t.Fatalf("seed yesterday trains: %v", err)
	}

	mgr := NewManager(st, filepath.Join(t.TempDir(), "missing-dir"), loc, 3)
	if err := mgr.LoadForAccess(ctx, beforeCutoff); err != nil {
		t.Fatalf("expected startup fallback success, got %v", err)
	}
	access := mgr.AccessContext(beforeCutoff)
	if !access.FallbackActive || access.EffectiveServiceDate != yesterdayServiceDate {
		t.Fatalf("expected startup fallback to yesterday, got %+v", access)
	}
}
