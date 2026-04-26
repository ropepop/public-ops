package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
)

type trainInstancesCall struct {
	serviceDate   string
	sourceVersion string
	trains        []domain.TrainInstance
}

type trainStopsCall struct {
	serviceDate  string
	stopsByTrain map[string][]domain.TrainStop
}

type recordingStore struct {
	Store

	upsertTrainInstancesCalls []trainInstancesCall
	upsertTrainStopsCalls     []trainStopsCall
	deleteTrainDataCalls      []string

	upsertTrainInstancesErr error
	upsertTrainStopsErr     error
	deleteTrainDataErr      error
	deleteTrainDataResult   CleanupResult
}

func (s *recordingStore) UpsertTrainInstances(_ context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance) error {
	copied := append([]domain.TrainInstance(nil), trains...)
	s.upsertTrainInstancesCalls = append(s.upsertTrainInstancesCalls, trainInstancesCall{
		serviceDate:   serviceDate,
		sourceVersion: sourceVersion,
		trains:        copied,
	})
	return s.upsertTrainInstancesErr
}

func (s *recordingStore) UpsertTrainStops(_ context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error {
	copied := make(map[string][]domain.TrainStop, len(stopsByTrain))
	for trainID, stops := range stopsByTrain {
		copied[trainID] = append([]domain.TrainStop(nil), stops...)
	}
	s.upsertTrainStopsCalls = append(s.upsertTrainStopsCalls, trainStopsCall{
		serviceDate:  serviceDate,
		stopsByTrain: copied,
	})
	return s.upsertTrainStopsErr
}

func (s *recordingStore) DeleteTrainDataByServiceDate(_ context.Context, serviceDate string) (CleanupResult, error) {
	s.deleteTrainDataCalls = append(s.deleteTrainDataCalls, serviceDate)
	return s.deleteTrainDataResult, s.deleteTrainDataErr
}

func TestRoutedStoreMirrorsScheduleImportsIntoStateStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheduleStore := &recordingStore{}
	stateStore := &recordingStore{}
	store := NewRoutedStore(scheduleStore, stateStore)

	departureAt := time.Date(2026, time.April, 9, 1, 22, 0, 0, time.UTC)
	arrivalAt := time.Date(2026, time.April, 9, 2, 44, 0, 0, time.UTC)
	trains := []domain.TrainInstance{{
		ID:            "2026-04-09-train-6502",
		ServiceDate:   "2026-04-09",
		FromStation:   "Tukums II",
		ToStation:     "Riga",
		DepartureAt:   departureAt,
		ArrivalAt:     arrivalAt,
		SourceVersion: "agg-2026-04-09",
	}}
	stopsByTrain := map[string][]domain.TrainStop{
		"2026-04-09-train-6502": {{
			TrainInstanceID: "2026-04-09-train-6502",
			StationID:       "riga",
			StationName:     "Riga",
			Seq:             1,
		}},
	}

	if err := store.UpsertTrainInstances(ctx, "2026-04-09", "agg-2026-04-09", trains); err != nil {
		t.Fatalf("UpsertTrainInstances: %v", err)
	}
	if err := store.UpsertTrainStops(ctx, "2026-04-09", stopsByTrain); err != nil {
		t.Fatalf("UpsertTrainStops: %v", err)
	}

	if got := len(scheduleStore.upsertTrainInstancesCalls); got != 1 {
		t.Fatalf("expected schedule store train upsert once, got %d", got)
	}
	if got := len(stateStore.upsertTrainInstancesCalls); got != 1 {
		t.Fatalf("expected state store train upsert once, got %d", got)
	}
	if !reflect.DeepEqual(scheduleStore.upsertTrainInstancesCalls[0], stateStore.upsertTrainInstancesCalls[0]) {
		t.Fatalf("expected mirrored train upsert calls, schedule=%+v state=%+v", scheduleStore.upsertTrainInstancesCalls[0], stateStore.upsertTrainInstancesCalls[0])
	}

	if got := len(scheduleStore.upsertTrainStopsCalls); got != 1 {
		t.Fatalf("expected schedule store stop upsert once, got %d", got)
	}
	if got := len(stateStore.upsertTrainStopsCalls); got != 1 {
		t.Fatalf("expected state store stop upsert once, got %d", got)
	}
	if !reflect.DeepEqual(scheduleStore.upsertTrainStopsCalls[0], stateStore.upsertTrainStopsCalls[0]) {
		t.Fatalf("expected mirrored stop upsert calls, schedule=%+v state=%+v", scheduleStore.upsertTrainStopsCalls[0], stateStore.upsertTrainStopsCalls[0])
	}
}

func TestRoutedStoreDeleteTrainDataByServiceDateMirrorsStateCleanup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheduleStore := &recordingStore{
		deleteTrainDataResult: CleanupResult{
			TrainStopsDeleted: 10,
			TrainsDeleted:     4,
		},
	}
	stateStore := &recordingStore{
		deleteTrainDataResult: CleanupResult{
			TrainStopsDeleted: 10,
			TrainsDeleted:     4,
			FeedEventsDeleted: 2,
		},
	}
	store := NewRoutedStore(scheduleStore, stateStore)

	result, err := store.DeleteTrainDataByServiceDate(ctx, "2026-04-08")
	if err != nil {
		t.Fatalf("DeleteTrainDataByServiceDate: %v", err)
	}
	if got := len(scheduleStore.deleteTrainDataCalls); got != 1 {
		t.Fatalf("expected schedule store delete once, got %d", got)
	}
	if got := len(stateStore.deleteTrainDataCalls); got != 1 {
		t.Fatalf("expected state store delete once, got %d", got)
	}
	if result.TrainStopsDeleted != 20 || result.TrainsDeleted != 8 || result.FeedEventsDeleted != 2 {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
}

func TestRoutedStoreDoesNotDoubleWriteWhenStoresAreShared(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	shared := &recordingStore{}
	store := NewRoutedStore(shared, shared)

	if err := store.UpsertTrainInstances(ctx, "2026-04-09", "agg-2026-04-09", nil); err != nil {
		t.Fatalf("UpsertTrainInstances: %v", err)
	}
	if err := store.UpsertTrainStops(ctx, "2026-04-09", nil); err != nil {
		t.Fatalf("UpsertTrainStops: %v", err)
	}
	if _, err := store.DeleteTrainDataByServiceDate(ctx, "2026-04-09"); err != nil {
		t.Fatalf("DeleteTrainDataByServiceDate: %v", err)
	}

	if got := len(shared.upsertTrainInstancesCalls); got != 1 {
		t.Fatalf("expected shared store train upsert once, got %d", got)
	}
	if got := len(shared.upsertTrainStopsCalls); got != 1 {
		t.Fatalf("expected shared store stop upsert once, got %d", got)
	}
	if got := len(shared.deleteTrainDataCalls); got != 1 {
		t.Fatalf("expected shared store delete once, got %d", got)
	}
}

func TestRoutedStoreSurfacesScheduleAndStateWriteErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheduleErr := errors.New("schedule write failed")
	stateErr := errors.New("state write failed")
	store := NewRoutedStore(
		&recordingStore{upsertTrainInstancesErr: scheduleErr},
		&recordingStore{upsertTrainInstancesErr: stateErr},
	)

	err := store.UpsertTrainInstances(ctx, "2026-04-09", "agg-2026-04-09", nil)
	if err == nil {
		t.Fatalf("expected combined write error")
	}
	if !errors.Is(err, scheduleErr) {
		t.Fatalf("expected schedule error in result, got %v", err)
	}
	if !errors.Is(err, stateErr) {
		t.Fatalf("expected state error in result, got %v", err)
	}
}
