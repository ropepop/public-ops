package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/store"
)

type testSnapshot struct {
	SourceVersion string              `json:"source_version"`
	Trains        []testSnapshotTrain `json:"trains"`
}

type testSnapshotTrain struct {
	ID          string             `json:"id"`
	ServiceDate string             `json:"service_date"`
	FromStation string             `json:"from_station"`
	ToStation   string             `json:"to_station"`
	DepartureAt string             `json:"departure_at"`
	ArrivalAt   string             `json:"arrival_at"`
	Stops       []testSnapshotStop `json:"stops"`
}

type testSnapshotStop struct {
	StationName string   `json:"station_name"`
	Seq         int      `json:"seq"`
	ArrivalAt   string   `json:"arrival_at,omitempty"`
	DepartureAt string   `json:"departure_at,omitempty"`
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
}

func TestPublicDashboardPayloadBuildsFromScheduleStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	baseStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer baseStore.Close()
	if err := baseStore.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-dashboard-provider",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("snapshot-train-1", serviceDate, "Riga", "Jelgava", now.Add(10*time.Minute)),
			buildSnapshotTrain("snapshot-train-2", serviceDate, "Riga", "Liepaja", now.Add(20*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(baseStore, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		baseStore,
		manager,
		ride.NewService(baseStore),
		reports.NewService(baseStore, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	publicPayload, err := service.PublicDashboardPayload(ctx, now, 1)
	if err != nil {
		t.Fatalf("public dashboard payload: %v", err)
	}

	body, err := json.Marshal(publicPayload)
	if err != nil {
		t.Fatalf("marshal public dashboard payload: %v", err)
	}
	var decoded struct {
		GeneratedAt time.Time `json:"generatedAt"`
		Trains      []struct {
			Riders int `json:"riders"`
			Train  struct {
				ID string `json:"id"`
			} `json:"train"`
		} `json:"trains"`
		Schedule schedule.AccessContext `json:"schedule"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode public dashboard payload: %v", err)
	}
	if got := len(decoded.Trains); got != 1 {
		t.Fatalf("expected 1 dashboard train after limit, got %d", got)
	}
	if got := decoded.Trains[0].Train.ID; got != "snapshot-train-1" {
		t.Fatalf("expected first snapshot train payload, got %q", got)
	}
	if got := decoded.Trains[0].Riders; got != 0 {
		t.Fatalf("expected rider count 0 from store-backed dashboard, got %d", got)
	}
	if !decoded.GeneratedAt.Equal(now.UTC()) {
		t.Fatalf("expected generatedAt %s, got %s", now.UTC(), decoded.GeneratedAt)
	}
	if !decoded.Schedule.Available || decoded.Schedule.EffectiveServiceDate != serviceDate {
		t.Fatalf("unexpected schedule payload: %+v", decoded.Schedule)
	}
}

func TestPublicDashboardPayloadReturnsScheduleErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 26, 12, 0, 0, 0, loc)

	baseStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer baseStore.Close()
	if err := baseStore.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	manager := schedule.NewManager(baseStore, t.TempDir(), loc, 3)

	service := NewService(
		baseStore,
		manager,
		ride.NewService(baseStore),
		reports.NewService(baseStore, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	_, err = service.PublicDashboardPayload(ctx, now, 0)
	if !errors.Is(err, schedule.ErrUnavailable) {
		t.Fatalf("expected schedule unavailable error, got %v", err)
	}
}

func TestPublicStationDeparturesSplitsLastAndUpcoming(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	trains := []testSnapshotTrain{
		buildSnapshotTrain("train-past", serviceDate, "Riga", "Jelgava", now.Add(-20*time.Minute)),
	}
	for i := 0; i < 10; i++ {
		trains = append(trains, buildSnapshotTrain(
			fmt.Sprintf("train-next-%02d", i),
			serviceDate,
			"Riga",
			fmt.Sprintf("Stop %02d", i),
			now.Add(time.Duration(i+1)*15*time.Minute),
		))
	}
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test",
		Trains:        trains,
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	if _, err := service.SubmitStationSighting(ctx, 7, "riga", nil, nil, now); err != nil {
		t.Fatalf("submit station sighting: %v", err)
	}

	view, err := service.PublicStationDepartures(ctx, now, "riga", 8)
	if err != nil {
		t.Fatalf("public station departures: %v", err)
	}
	if view.Station.ID != "riga" {
		t.Fatalf("unexpected station id: %q", view.Station.ID)
	}
	if view.LastDeparture == nil {
		t.Fatalf("expected last departure")
	}
	if view.LastDeparture.TrainCard.Train.ID != "train-past" {
		t.Fatalf("unexpected last departure train: %q", view.LastDeparture.TrainCard.Train.ID)
	}
	if len(view.Upcoming) != 8 {
		t.Fatalf("expected 8 upcoming departures, got %d", len(view.Upcoming))
	}
	if view.Upcoming[0].TrainCard.Train.ID != "train-next-00" {
		t.Fatalf("unexpected first upcoming train: %q", view.Upcoming[0].TrainCard.Train.ID)
	}
	if view.Upcoming[7].TrainCard.Train.ID != "train-next-07" {
		t.Fatalf("unexpected last included upcoming train: %q", view.Upcoming[7].TrainCard.Train.ID)
	}
	if len(view.RecentSightings) != 1 || view.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected recent station sighting in public response, got %+v", view.RecentSightings)
	}
}

func TestSubmitReportAllowsDirectSignedInTrainContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-direct-report",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-direct-report", serviceDate, "Riga", "Jelgava", now.Add(15*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	result, err := service.SubmitReport(ctx, 55, "train-direct-report", domain.SignalInspectionStarted, now)
	if err != nil {
		t.Fatalf("submit report: %v", err)
	}
	if !result.Accepted {
		t.Fatalf("expected direct report to be accepted, got %+v", result)
	}
}

func buildSnapshotTrain(id string, serviceDate string, fromStation string, toStation string, departureAt time.Time) testSnapshotTrain {
	arrivalAt := departureAt.Add(45 * time.Minute)
	return testSnapshotTrain{
		ID:          id,
		ServiceDate: serviceDate,
		FromStation: fromStation,
		ToStation:   toStation,
		DepartureAt: departureAt.Format(time.RFC3339),
		ArrivalAt:   arrivalAt.Format(time.RFC3339),
		Stops: []testSnapshotStop{
			{
				StationName: fromStation,
				Seq:         1,
				DepartureAt: departureAt.Format(time.RFC3339),
				Latitude:    testFloatPtr(56.9496),
				Longitude:   testFloatPtr(24.1052),
			},
			{
				StationName: toStation,
				Seq:         2,
				ArrivalAt:   arrivalAt.Format(time.RFC3339),
				Latitude:    testFloatPtr(56.6511),
				Longitude:   testFloatPtr(23.7128),
			},
		},
	}
}

func TestCheckInRejectsExpiredDeparture(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-expired-checkin",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-expired", serviceDate, "Riga", "Jelgava", now.Add(-90*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	err = service.CheckIn(ctx, 77, "train-expired", nil, now)
	if err == nil {
		t.Fatalf("expected expired departure check-in to fail")
	}
	if err != ErrCheckInUnavailable {
		t.Fatalf("expected ErrCheckInUnavailable, got %v", err)
	}

	currentRide, err := service.CurrentRide(ctx, 77, now)
	if err != nil {
		t.Fatalf("current ride after rejected check-in: %v", err)
	}
	if currentRide != nil {
		t.Fatalf("expected no active ride after rejected check-in, got %+v", currentRide)
	}
}

func TestCheckInRejectsExpiredBoardingStationDeparture(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-expired-station-checkin",
		Trains: []testSnapshotTrain{
			{
				ID:          "train-station-window-expired",
				ServiceDate: serviceDate,
				FromStation: "Aizkraukle",
				ToStation:   "Tukums",
				DepartureAt: now.Add(-70 * time.Minute).Format(time.RFC3339),
				ArrivalAt:   now.Add(30 * time.Minute).Format(time.RFC3339),
				Stops: []testSnapshotStop{
					{StationName: "Aizkraukle", Seq: 1, DepartureAt: now.Add(-70 * time.Minute).Format(time.RFC3339)},
					{StationName: "Riga", Seq: 2, ArrivalAt: now.Add(-20 * time.Minute).Format(time.RFC3339), DepartureAt: now.Add(-18 * time.Minute).Format(time.RFC3339)},
					{StationName: "Tukums", Seq: 3, ArrivalAt: now.Add(30 * time.Minute).Format(time.RFC3339)},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	boardingStationID := "riga"
	err = service.CheckIn(ctx, 77, "train-station-window-expired", &boardingStationID, now)
	if err == nil {
		t.Fatalf("expected expired station departure check-in to fail")
	}
	if err != ErrCheckInUnavailable {
		t.Fatalf("expected ErrCheckInUnavailable, got %v", err)
	}

	currentRide, err := service.CurrentRide(ctx, 77, now)
	if err != nil {
		t.Fatalf("current ride after rejected station check-in: %v", err)
	}
	if currentRide != nil {
		t.Fatalf("expected no active ride after rejected station check-in, got %+v", currentRide)
	}
}

func TestCheckInMapAllowsExpiredDepartureAndKeepsRideActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-map-expired-checkin",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-expired-map", serviceDate, "Riga", "Jelgava", now.Add(-90*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	if err := service.CheckInMap(ctx, 77, "train-expired-map", nil, now); err != nil {
		t.Fatalf("map check-in should allow expired departure: %v", err)
	}

	currentRide, err := service.CurrentRide(ctx, 77, now)
	if err != nil {
		t.Fatalf("current ride after map check-in: %v", err)
	}
	if currentRide == nil || currentRide.CheckIn == nil {
		t.Fatalf("expected active ride after relaxed map check-in, got %+v", currentRide)
	}
	if currentRide.CheckIn.TrainInstanceID != "train-expired-map" {
		t.Fatalf("expected train-expired-map current ride, got %+v", currentRide.CheckIn)
	}
	if !currentRide.CheckIn.AutoCheckoutAt.After(now.Add(5 * time.Hour)) {
		t.Fatalf("expected relaxed map check-in to use a fallback auto checkout, got %+v", currentRide.CheckIn)
	}
}

func TestCheckInMapAllowsExpiredBoardingStationDepartureAndPreservesStation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-map-expired-station-checkin",
		Trains: []testSnapshotTrain{
			{
				ID:          "train-station-window-map",
				ServiceDate: serviceDate,
				FromStation: "Aizkraukle",
				ToStation:   "Tukums",
				DepartureAt: now.Add(-70 * time.Minute).Format(time.RFC3339),
				ArrivalAt:   now.Add(30 * time.Minute).Format(time.RFC3339),
				Stops: []testSnapshotStop{
					{StationName: "Aizkraukle", Seq: 1, DepartureAt: now.Add(-70 * time.Minute).Format(time.RFC3339)},
					{StationName: "Riga", Seq: 2, ArrivalAt: now.Add(-20 * time.Minute).Format(time.RFC3339), DepartureAt: now.Add(-18 * time.Minute).Format(time.RFC3339)},
					{StationName: "Tukums", Seq: 3, ArrivalAt: now.Add(30 * time.Minute).Format(time.RFC3339)},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	boardingStationID := "riga"
	if err := service.CheckInMap(ctx, 77, "train-station-window-map", &boardingStationID, now); err != nil {
		t.Fatalf("map check-in should allow an expired boarding station departure: %v", err)
	}

	currentRide, err := service.CurrentRide(ctx, 77, now)
	if err != nil {
		t.Fatalf("current ride after relaxed station check-in: %v", err)
	}
	if currentRide == nil || currentRide.CheckIn == nil {
		t.Fatalf("expected active ride after relaxed station check-in, got %+v", currentRide)
	}
	if currentRide.BoardingStationID != "riga" {
		t.Fatalf("expected boarding station to stay riga, got %+v", currentRide)
	}
	if currentRide.BoardingStationName != "Riga" {
		t.Fatalf("expected boarding station name Riga, got %+v", currentRide)
	}
}

func TestStationSightingDestinationsReturnTerminalStopsOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-sighting-destinations",
		Trains: []testSnapshotTrain{
			{
				ID:          "train-terminal-a",
				ServiceDate: serviceDate,
				FromStation: "Riga",
				ToStation:   "Cesis",
				DepartureAt: now.Add(10 * time.Minute).Format(time.RFC3339),
				ArrivalAt:   now.Add(2 * time.Hour).Format(time.RFC3339),
				Stops: []testSnapshotStop{
					{StationName: "Riga", Seq: 1, DepartureAt: now.Add(10 * time.Minute).Format(time.RFC3339)},
					{StationName: "Sigulda", Seq: 2, ArrivalAt: now.Add(50 * time.Minute).Format(time.RFC3339)},
					{StationName: "Cesis", Seq: 3, ArrivalAt: now.Add(2 * time.Hour).Format(time.RFC3339)},
				},
			},
			{
				ID:          "train-terminal-loop",
				ServiceDate: serviceDate,
				FromStation: "Riga",
				ToStation:   "Riga",
				DepartureAt: now.Add(20 * time.Minute).Format(time.RFC3339),
				ArrivalAt:   now.Add(3 * time.Hour).Format(time.RFC3339),
				Stops: []testSnapshotStop{
					{StationName: "Riga", Seq: 1, DepartureAt: now.Add(20 * time.Minute).Format(time.RFC3339)},
					{StationName: "Sigulda", Seq: 2, ArrivalAt: now.Add(70 * time.Minute).Format(time.RFC3339)},
					{StationName: "Riga", Seq: 3, ArrivalAt: now.Add(3 * time.Hour).Format(time.RFC3339)},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	destinations, err := service.StationSightingDestinations(ctx, now, "riga")
	if err != nil {
		t.Fatalf("station sighting destinations: %v", err)
	}
	if len(destinations) != 2 {
		t.Fatalf("expected 2 terminal destinations, got %d: %+v", len(destinations), destinations)
	}
	got := make(map[string]struct{}, len(destinations))
	for _, destination := range destinations {
		got[destination.ID] = struct{}{}
	}
	if _, ok := got["cesis"]; !ok {
		t.Fatalf("expected cesis terminal destination, got %+v", destinations)
	}
	if _, ok := got["riga"]; !ok {
		t.Fatalf("expected repeated-stop riga terminal destination, got %+v", destinations)
	}
	if _, ok := got["sigulda"]; ok {
		t.Fatalf("expected intermediate sigulda to be excluded, got %+v", destinations)
	}
}

func TestStationDeparturesUseBidirectionalWindowAndSightingContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-station-window",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-too-old", serviceDate, "Riga", "Old", now.Add(-3*time.Hour)),
			buildSnapshotTrain("train-past-window", serviceDate, "Riga", "Past", now.Add(-90*time.Minute)),
			buildSnapshotTrain("train-soon-window", serviceDate, "Riga", "Soon", now.Add(20*time.Minute)),
			buildSnapshotTrain("train-too-late", serviceDate, "Riga", "Late", now.Add(3*time.Hour)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	if err := st.InsertStationSighting(ctx, domain.StationSighting{
		ID:        "past-sighting",
		StationID: "riga",
		UserID:    91,
		CreatedAt: now.Add(-80 * time.Minute),
	}); err != nil {
		t.Fatalf("insert past station sighting: %v", err)
	}
	if err := st.InsertStationSighting(ctx, domain.StationSighting{
		ID:        "soon-sighting",
		StationID: "riga",
		UserID:    92,
		CreatedAt: now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("insert soon station sighting: %v", err)
	}

	view, err := service.StationDepartures(ctx, 0, now, "riga", 2*time.Hour, 2*time.Hour)
	if err != nil {
		t.Fatalf("station departures: %v", err)
	}
	if len(view.Trains) != 2 {
		t.Fatalf("expected 2 station departures in +/-2h window, got %d: %+v", len(view.Trains), view.Trains)
	}
	if view.Trains[0].TrainCard.Train.ID != "train-past-window" {
		t.Fatalf("unexpected first train in station window: %+v", view.Trains[0])
	}
	if view.Trains[1].TrainCard.Train.ID != "train-soon-window" {
		t.Fatalf("unexpected second train in station window: %+v", view.Trains[1])
	}
	if view.Trains[0].SightingCount != 1 || len(view.Trains[0].SightingContext) != 1 {
		t.Fatalf("expected one nearby sighting for past-window train, got %+v", view.Trains[0])
	}
	if view.Trains[1].SightingCount != 1 || len(view.Trains[1].SightingContext) != 1 {
		t.Fatalf("expected one nearby sighting for soon-window train, got %+v", view.Trains[1])
	}
}

func TestSubmitStationSightingRejectsSelectedTrainOutsideStationWindow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-station-selection",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-late-selection", serviceDate, "Riga", "Late", now.Add(3*time.Hour)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	trainID := "train-late-selection"
	if _, err := service.SubmitStationSighting(ctx, 13, "riga", nil, &trainID, now); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for selected train outside +/-2h station window, got %v", err)
	}
}

func TestSubmitStationSightingFallsBackToDestinationMatching(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-destination-fallback",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-destination-fallback", serviceDate, "Riga", "Jelgava", now.Add(20*time.Minute)),
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	destinationID := "jelgava"
	result, err := service.SubmitStationSighting(ctx, 14, "riga", &destinationID, nil, now)
	if err != nil {
		t.Fatalf("submit station sighting fallback: %v", err)
	}
	if result.Event == nil || result.Event.MatchedTrainInstanceID == nil || *result.Event.MatchedTrainInstanceID != "train-destination-fallback" {
		t.Fatalf("expected destination fallback to match train-destination-fallback, got %+v", result)
	}
}

func TestTrainStopsIncludesCoordinatesAndMatchedSightings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	departureAt := now.Add(20 * time.Minute)
	arrivalAt := departureAt.Add(45 * time.Minute)
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-map",
		Trains: []testSnapshotTrain{
			{
				ID:          "train-map",
				ServiceDate: serviceDate,
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: departureAt.Format(time.RFC3339),
				ArrivalAt:   arrivalAt.Format(time.RFC3339),
				Stops: []testSnapshotStop{
					{
						StationName: "Riga",
						Seq:         1,
						DepartureAt: departureAt.Format(time.RFC3339),
						Latitude:    testFloatPtr(56.9496),
						Longitude:   testFloatPtr(24.1052),
					},
					{
						StationName: "Jelgava",
						Seq:         2,
						ArrivalAt:   arrivalAt.Format(time.RFC3339),
						Latitude:    testFloatPtr(56.6511),
						Longitude:   testFloatPtr(23.7128),
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	destinationID := "jelgava"
	trainID := "train-map"
	result, err := service.SubmitStationSighting(ctx, 11, "riga", &destinationID, &trainID, now)
	if err != nil {
		t.Fatalf("submit station sighting: %v", err)
	}
	if !result.Accepted || result.Event == nil {
		t.Fatalf("expected accepted station sighting, got %+v", result)
	}
	if result.Event.MatchedTrainInstanceID == nil || *result.Event.MatchedTrainInstanceID != "train-map" {
		t.Fatalf("expected matched train id train-map, got %+v", result.Event)
	}
	if err := st.CheckInUser(ctx, 22, "train-map", now.Add(-2*time.Minute), now.Add(30*time.Minute)); err != nil {
		t.Fatalf("seed active checkin: %v", err)
	}

	view, err := service.TrainStops(ctx, 22, now, "train-map")
	if err != nil {
		t.Fatalf("train stops: %v", err)
	}
	if view.Train.ID != "train-map" {
		t.Fatalf("unexpected train id: %q", view.Train.ID)
	}
	if view.TrainCard.Train.ID != "train-map" || view.TrainCard.Riders != 1 {
		t.Fatalf("expected train card riders for map header, got %+v", view.TrainCard)
	}
	if len(view.Stops) != 2 {
		t.Fatalf("expected 2 stops, got %d", len(view.Stops))
	}
	if view.Stops[0].Latitude == nil || view.Stops[0].Longitude == nil {
		t.Fatalf("expected coordinates on first stop, got %+v", view.Stops[0])
	}
	if len(view.StationSightings) != 1 || view.StationSightings[0].MatchedTrainInstanceID == nil || *view.StationSightings[0].MatchedTrainInstanceID != "train-map" {
		t.Fatalf("expected matched station sighting on train view, got %+v", view.StationSightings)
	}
}

func TestNetworkMapIncludesCoordinateStationsAndSameDaySightings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)
	serviceDate := now.Format("2006-01-02")

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, serviceDate+".json")
	payload, err := json.Marshal(testSnapshot{
		SourceVersion: "service-test-network-map",
		Trains: []testSnapshotTrain{
			buildSnapshotTrain("train-network-map", serviceDate, "Riga", "Jelgava", now.Add(20*time.Minute)),
			{
				ID:          "train-missing-coords",
				ServiceDate: serviceDate,
				FromStation: "NoCoordsStart",
				ToStation:   "NoCoordsEnd",
				DepartureAt: now.Add(40 * time.Minute).Format(time.RFC3339),
				ArrivalAt:   now.Add(80 * time.Minute).Format(time.RFC3339),
				Stops: []testSnapshotStop{
					{StationName: "NoCoordsStart", Seq: 1, DepartureAt: now.Add(40 * time.Minute).Format(time.RFC3339)},
					{StationName: "NoCoordsEnd", Seq: 2, ArrivalAt: now.Add(80 * time.Minute).Format(time.RFC3339)},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(snapshotPath, payload, 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	manager := schedule.NewManager(st, snapshotDir, loc, 3)
	if err := manager.LoadToday(ctx, now); err != nil {
		t.Fatalf("load today: %v", err)
	}

	service := NewService(
		st,
		manager,
		ride.NewService(st),
		reports.NewService(st, 3*time.Minute, 90*time.Second),
		loc,
		true,
	)

	destinationID := "jelgava"
	if _, err := service.SubmitStationSighting(ctx, 19, "riga", &destinationID, nil, now); err != nil {
		t.Fatalf("submit station sighting: %v", err)
	}
	if err := st.InsertStationSighting(ctx, domain.StationSighting{
		ID:        "station-sighting-hidden",
		StationID: "riga",
		UserID:    20,
		CreatedAt: now.Add(-90 * time.Minute),
	}); err != nil {
		t.Fatalf("insert older same-day sighting: %v", err)
	}

	view, err := service.NetworkMap(ctx, now)
	if err != nil {
		t.Fatalf("network map: %v", err)
	}
	if len(view.Stations) != 2 {
		t.Fatalf("expected only coordinate stations in network map, got %d: %+v", len(view.Stations), view.Stations)
	}
	if len(view.RecentSightings) != 1 || view.RecentSightings[0].StationID != "riga" {
		t.Fatalf("expected recent sightings in network map, got %+v", view.RecentSightings)
	}
	if len(view.SameDaySightings) != 2 {
		t.Fatalf("expected same-day sightings in network map, got %+v", view.SameDaySightings)
	}
}

func testFloatPtr(v float64) *float64 {
	return &v
}
