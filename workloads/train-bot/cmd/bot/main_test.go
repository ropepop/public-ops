package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"telegramtrainapp/internal/bot"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/store"
)

type notifierTestSnapshot struct {
	SourceVersion string                      `json:"source_version"`
	Trains        []notifierTestSnapshotTrain `json:"trains"`
}

type notifierTestSnapshotTrain struct {
	ID          string                     `json:"id"`
	ServiceDate string                     `json:"service_date"`
	FromStation string                     `json:"from_station"`
	ToStation   string                     `json:"to_station"`
	DepartureAt string                     `json:"departure_at"`
	ArrivalAt   string                     `json:"arrival_at"`
	Stops       []notifierTestSnapshotStop `json:"stops"`
}

type notifierTestSnapshotStop struct {
	StationName string `json:"station_name"`
	Seq         int    `json:"seq"`
	ArrivalAt   string `json:"arrival_at,omitempty"`
	DepartureAt string `json:"departure_at,omitempty"`
}

type captureTrainAlertNotifier struct {
	rideCalls    int
	ridePayload  bot.RideAlertPayload
	stationCalls int
	stationEvent domain.StationSighting
}

func (n *captureTrainAlertNotifier) DispatchRideAlert(_ context.Context, payload bot.RideAlertPayload, _ time.Time) error {
	n.rideCalls++
	n.ridePayload = payload
	return nil
}

func (n *captureTrainAlertNotifier) DispatchStationSighting(_ context.Context, event domain.StationSighting, _ time.Time) error {
	n.stationCalls++
	n.stationEvent = event
	return nil
}

func TestTrainWebRideNotifierNotifyRideUsersIncludesScheduleData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, loc)

	manager := newNotifierTestManager(t, ctx, loc, now, notifierTestSnapshot{
		SourceVersion: "ride-adapter-test",
		Trains: []notifierTestSnapshotTrain{
			buildNotifierSnapshotTrain("train-exact", now.Format("2006-01-02"), "Riga", "Cesis", now.Add(10*time.Minute)),
		},
	})

	capture := &captureTrainAlertNotifier{}
	notifier := trainWebRideNotifier{
		schedules: manager,
		notifier:  capture,
	}

	if err := notifier.NotifyRideUsers(ctx, 901, "train-exact", domain.SignalInspectionInCar, now); err != nil {
		t.Fatalf("notify ride users: %v", err)
	}

	if capture.rideCalls != 1 {
		t.Fatalf("expected one ride dispatch, got %d", capture.rideCalls)
	}
	if capture.ridePayload.TrainID != "train-exact" {
		t.Fatalf("unexpected train id: %+v", capture.ridePayload)
	}
	if capture.ridePayload.FromStation != "Riga" || capture.ridePayload.ToStation != "Cesis" {
		t.Fatalf("expected schedule route in payload, got %+v", capture.ridePayload)
	}
	if capture.ridePayload.Signal != domain.SignalInspectionInCar || capture.ridePayload.ReporterID != 901 {
		t.Fatalf("expected signal/reporter to propagate, got %+v", capture.ridePayload)
	}
	if capture.ridePayload.DepartureAt.IsZero() || capture.ridePayload.ArrivalAt.IsZero() {
		t.Fatalf("expected departure and arrival times in payload, got %+v", capture.ridePayload)
	}
}

func TestTrainWebRideNotifierNotifyStationSightingForwardsEvent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	capture := &captureTrainAlertNotifier{}
	notifier := trainWebRideNotifier{notifier: capture}
	matchedTrainID := "train-exact"
	event := domain.StationSighting{
		ID:                     "station-sighting-1",
		StationID:              "riga",
		StationName:            "Riga",
		DestinationStationName: "Cesis",
		MatchedTrainInstanceID: &matchedTrainID,
		UserID:                 100,
		CreatedAt:              now,
	}

	if err := notifier.NotifyStationSighting(context.Background(), event, now); err != nil {
		t.Fatalf("notify station sighting: %v", err)
	}

	if capture.stationCalls != 1 {
		t.Fatalf("expected one station dispatch, got %d", capture.stationCalls)
	}
	if capture.stationEvent.ID != event.ID || capture.stationEvent.StationID != event.StationID {
		t.Fatalf("expected event to be forwarded unchanged, got %+v", capture.stationEvent)
	}
	if capture.stationEvent.MatchedTrainInstanceID == nil || *capture.stationEvent.MatchedTrainInstanceID != matchedTrainID {
		t.Fatalf("expected matched train to be forwarded, got %+v", capture.stationEvent)
	}
}

func newNotifierTestManager(t *testing.T, ctx context.Context, loc *time.Location, now time.Time, snapshot notifierTestSnapshot) *schedule.Manager {
	t.Helper()

	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "train-bot.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snapshotDir := t.TempDir()
	snapshotPath := filepath.Join(snapshotDir, now.Format("2006-01-02")+".json")
	payload, err := json.Marshal(snapshot)
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
	return manager
}

func buildNotifierSnapshotTrain(id string, serviceDate string, fromStation string, toStation string, departureAt time.Time) notifierTestSnapshotTrain {
	arrivalAt := departureAt.Add(45 * time.Minute)
	return notifierTestSnapshotTrain{
		ID:          id,
		ServiceDate: serviceDate,
		FromStation: fromStation,
		ToStation:   toStation,
		DepartureAt: departureAt.Format(time.RFC3339),
		ArrivalAt:   arrivalAt.Format(time.RFC3339),
		Stops: []notifierTestSnapshotStop{
			{
				StationName: fromStation,
				Seq:         1,
				DepartureAt: departureAt.Format(time.RFC3339),
			},
			{
				StationName: toStation,
				Seq:         2,
				ArrivalAt:   arrivalAt.Format(time.RFC3339),
			},
		},
	}
}

func TestPrepareRuntimeCachePathCreatesWritableParent(t *testing.T) {
	want := filepath.Join(t.TempDir(), "state", "train-runtime-cache.db")
	got, err := prepareRuntimeCachePath(want)
	if err != nil {
		t.Fatalf("prepare runtime cache path: %v", err)
	}
	if got != want {
		t.Fatalf("runtime cache path = %q, want %q", got, want)
	}
	probe := filepath.Join(filepath.Dir(got), "write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		t.Fatalf("runtime cache parent is not writable: %v", err)
	}
}

func TestPrepareRuntimeCachePathRejectsBlankPath(t *testing.T) {
	if _, err := prepareRuntimeCachePath("  "); err == nil {
		t.Fatalf("expected blank runtime cache path to fail")
	}
}
