package recovery

import (
	"context"
	"testing"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/domain"
)

type fakeBundleProvider struct {
	base  *trainapp.StaticBundleBase
	err   error
	calls []time.Time
}

func (p *fakeBundleProvider) StaticBundleBase(_ context.Context, now time.Time) (*trainapp.StaticBundleBase, error) {
	p.calls = append(p.calls, now)
	return p.base, p.err
}

type replaceCall struct {
	serviceDate   string
	sourceVersion string
	stations      []domain.Station
	trains        []domain.TrainInstance
	stops         []domain.TrainStop
}

type fakeScheduleTarget struct {
	existingStations int
	existingTrains   int
	existingStops    int
	replaceErr       error
	replaceCalls     []replaceCall
}

func (t *fakeScheduleTarget) ScheduleCounts(context.Context, string) (int, int, int, error) {
	return t.existingStations, t.existingTrains, t.existingStops, nil
}

func (t *fakeScheduleTarget) ReplaceScheduleSnapshot(_ context.Context, serviceDate string, sourceVersion string, stations []domain.Station, trains []domain.TrainInstance, stops []domain.TrainStop) (int, int, int, error) {
	t.replaceCalls = append(t.replaceCalls, replaceCall{
		serviceDate:   serviceDate,
		sourceVersion: sourceVersion,
		stations:      append([]domain.Station(nil), stations...),
		trains:        append([]domain.TrainInstance(nil), trains...),
		stops:         append([]domain.TrainStop(nil), stops...),
	})
	if t.replaceErr != nil {
		return 0, 0, 0, t.replaceErr
	}
	return len(stations), len(trains), len(stops), nil
}

func TestMaybeSelfHealSyncsWhenSpacetimeScheduleIsEmpty(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 9, 18, 30, 0, 0, time.FixedZone("EET", 3*60*60))
	provider := &fakeBundleProvider{
		base: &trainapp.StaticBundleBase{
			ServiceDate:   "2026-04-09",
			SourceVersion: "agg-2026-04-09",
			Stations: []domain.Station{{
				ID:   "riga",
				Name: "Riga",
			}},
			Trains: []domain.TrainInstance{{
				ID:            "train-1",
				ServiceDate:   "2026-04-09",
				FromStation:   "Riga",
				ToStation:     "Jelgava",
				DepartureAt:   now.UTC(),
				ArrivalAt:     now.Add(40 * time.Minute).UTC(),
				SourceVersion: "agg-2026-04-09",
			}},
			Stops: []domain.TrainStop{{
				TrainInstanceID: "train-1",
				StationID:       "riga",
				StationName:     "Riga",
				Seq:             1,
			}},
		},
	}
	target := &fakeScheduleTarget{}

	result, err := MaybeSelfHeal(context.Background(), provider, target, now)
	if err != nil {
		t.Fatalf("MaybeSelfHeal: %v", err)
	}
	if !result.Synced {
		t.Fatalf("expected sync result, got %+v", result)
	}
	if len(target.replaceCalls) != 1 {
		t.Fatalf("expected one replace call, got %d", len(target.replaceCalls))
	}
	if target.replaceCalls[0].serviceDate != "2026-04-09" {
		t.Fatalf("unexpected service date: %+v", target.replaceCalls[0])
	}
}

func TestMaybeSelfHealSkipsWhenScheduleAlreadyExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.April, 9, 18, 30, 0, 0, time.UTC)
	provider := &fakeBundleProvider{
		base: &trainapp.StaticBundleBase{
			ServiceDate:   "2026-04-09",
			SourceVersion: "agg-2026-04-09",
			Stations:      []domain.Station{{ID: "riga", Name: "Riga"}},
			Trains: []domain.TrainInstance{{
				ID:            "train-1",
				ServiceDate:   "2026-04-09",
				FromStation:   "Riga",
				ToStation:     "Jelgava",
				DepartureAt:   now,
				ArrivalAt:     now.Add(40 * time.Minute),
				SourceVersion: "agg-2026-04-09",
			}},
			Stops: []domain.TrainStop{{TrainInstanceID: "train-1", StationID: "riga", StationName: "Riga", Seq: 1}},
		},
	}
	target := &fakeScheduleTarget{
		existingStations: 12,
		existingTrains:   306,
		existingStops:    5101,
	}

	result, err := MaybeSelfHeal(context.Background(), provider, target, now)
	if err != nil {
		t.Fatalf("MaybeSelfHeal: %v", err)
	}
	if result.Synced {
		t.Fatalf("expected self-heal skip, got %+v", result)
	}
	if result.ExistingTrains != 306 {
		t.Fatalf("unexpected existing count: %+v", result)
	}
	if len(target.replaceCalls) != 0 {
		t.Fatalf("expected no replace calls, got %d", len(target.replaceCalls))
	}
}

func TestSyncServiceDateAnchorsAtMiddayAndSupportsDryRun(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	provider := &fakeBundleProvider{
		base: &trainapp.StaticBundleBase{
			ServiceDate:   "2026-04-09",
			SourceVersion: "agg-2026-04-09",
			Stations:      []domain.Station{{ID: "riga", Name: "Riga"}},
			Trains: []domain.TrainInstance{{
				ID:            "train-1",
				ServiceDate:   "2026-04-09",
				FromStation:   "Riga",
				ToStation:     "Jelgava",
				DepartureAt:   time.Date(2026, time.April, 9, 9, 0, 0, 0, loc),
				ArrivalAt:     time.Date(2026, time.April, 9, 10, 0, 0, 0, loc),
				SourceVersion: "agg-2026-04-09",
			}},
			Stops: []domain.TrainStop{{TrainInstanceID: "train-1", StationID: "riga", StationName: "Riga", Seq: 1}},
		},
	}
	target := &fakeScheduleTarget{}

	result, err := SyncServiceDate(context.Background(), provider, target, loc, "2026-04-09", true)
	if err != nil {
		t.Fatalf("SyncServiceDate: %v", err)
	}
	if result.Synced {
		t.Fatalf("expected dry-run result, got %+v", result)
	}
	if len(target.replaceCalls) != 0 {
		t.Fatalf("expected no replace call during dry-run, got %d", len(target.replaceCalls))
	}
	if len(provider.calls) != 1 {
		t.Fatalf("expected one provider call, got %d", len(provider.calls))
	}
	if got := provider.calls[0].In(loc).Format(time.RFC3339); got != "2026-04-09T12:00:00+03:00" {
		t.Fatalf("unexpected anchor time: %s", got)
	}
}

func TestSyncServiceDateRejectsMismatchedResolvedDate(t *testing.T) {
	t.Parallel()

	loc := time.UTC
	provider := &fakeBundleProvider{
		base: &trainapp.StaticBundleBase{
			ServiceDate:   "2026-04-08",
			SourceVersion: "agg-2026-04-08",
			Stations:      []domain.Station{{ID: "riga", Name: "Riga"}},
			Trains: []domain.TrainInstance{{
				ID:            "train-1",
				ServiceDate:   "2026-04-08",
				FromStation:   "Riga",
				ToStation:     "Jelgava",
				DepartureAt:   time.Date(2026, time.April, 8, 9, 0, 0, 0, loc),
				ArrivalAt:     time.Date(2026, time.April, 8, 10, 0, 0, 0, loc),
				SourceVersion: "agg-2026-04-08",
			}},
			Stops: []domain.TrainStop{{TrainInstanceID: "train-1", StationID: "riga", StationName: "Riga", Seq: 1}},
		},
	}

	_, err := SyncServiceDate(context.Background(), provider, &fakeScheduleTarget{}, loc, "2026-04-09", false)
	if err == nil {
		t.Fatalf("expected mismatch error")
	}
}
