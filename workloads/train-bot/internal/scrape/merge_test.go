package scrape

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSnapshotFileMergesPrimaryTimesAndSecondaryStops(t *testing.T) {
	date := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	depA := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	arrA := time.Date(2026, 2, 26, 9, 0, 0, 0, time.UTC)
	depB := time.Date(2026, 2, 26, 8, 5, 0, 0, time.UTC)
	arrB := time.Date(2026, 2, 26, 9, 5, 0, 0, time.UTC)

	schedules := []RawSchedule{
		{
			SourceName: "source_a",
			Trains: []RawTrain{{
				ID:          "t1",
				ServiceDate: "2026-02-26",
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: depA,
				ArrivalAt:   arrA,
			}},
		},
		{
			SourceName: "source_b",
			Trains: []RawTrain{{
				ID:          "t1",
				ServiceDate: "2026-02-26",
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: depB,
				ArrivalAt:   arrB,
				Stops:       []RawStop{{StationName: "Riga", Seq: 1}, {StationName: "Jelgava", Seq: 2}},
			}},
		},
	}

	snapshot, stats, err := BuildSnapshotFile(date, schedules)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Trains) != 1 {
		t.Fatalf("expected 1 train, got %d", len(snapshot.Trains))
	}
	if snapshot.Trains[0].DepartureAt != depA.Format(time.RFC3339) {
		t.Fatalf("expected primary departure time, got %s", snapshot.Trains[0].DepartureAt)
	}
	if len(snapshot.Trains[0].Stops) != 2 {
		t.Fatalf("expected stops from secondary source")
	}
	if stats.StopsFilledFromB < 2 {
		t.Fatalf("expected stops filled from secondary, got %d", stats.StopsFilledFromB)
	}
}

func TestBuildSnapshotFileMergesByTrainNumberAcrossSources(t *testing.T) {
	date := time.Date(2026, 2, 26, 0, 0, 0, 0, time.UTC)
	depA := time.Date(2026, 2, 26, 8, 0, 0, 0, time.UTC)
	arrA := time.Date(2026, 2, 26, 9, 0, 0, 0, time.UTC)
	depB := time.Date(2026, 2, 26, 8, 2, 0, 0, time.UTC)
	arrB := time.Date(2026, 2, 26, 9, 2, 0, 0, time.UTC)

	schedules := []RawSchedule{
		{
			SourceName: "vivi_pdf",
			Trains: []RawTrain{{
				TrainNumber: "6501",
				ServiceDate: "2026-02-26",
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: depA,
				ArrivalAt:   arrA,
			}},
		},
		{
			SourceName: "vivi_gtfs",
			Trains: []RawTrain{{
				ID:          "trip-6501",
				TrainNumber: "6501",
				ServiceDate: "2026-02-26",
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: depB,
				ArrivalAt:   arrB,
			}},
		},
	}

	snapshot, _, err := BuildSnapshotFile(date, schedules)
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}
	if len(snapshot.Trains) != 1 {
		t.Fatalf("expected 1 merged train, got %d", len(snapshot.Trains))
	}
	if snapshot.Trains[0].DepartureAt != depA.Format(time.RFC3339) {
		t.Fatalf("expected PDF (primary) departure, got %s", snapshot.Trains[0].DepartureAt)
	}
	if !strings.Contains(snapshot.Trains[0].ID, "train-6501") {
		t.Fatalf("expected service-date scoped train number id, got %s", snapshot.Trains[0].ID)
	}
}

func TestSnapshotToDomainPreservesTrainStopsByTrain(t *testing.T) {
	snapshot := SnapshotFile{
		SourceVersion: "snapshot-test",
		Trains: []SnapshotTrain{
			{
				ID:          "train-1",
				ServiceDate: "2026-02-26",
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: "2026-02-26T08:00:00Z",
				ArrivalAt:   "2026-02-26T09:00:00Z",
				Stops: []SnapshotStop{
					{StationName: "Riga", Seq: 1, DepartureAt: "2026-02-26T08:00:00Z"},
					{StationName: "Jelgava", Seq: 2, ArrivalAt: "2026-02-26T09:00:00Z"},
				},
			},
		},
	}

	trains, stopsByTrain, err := SnapshotToDomain(snapshot)
	if err != nil {
		t.Fatalf("convert snapshot: %v", err)
	}
	if len(trains) != 1 {
		t.Fatalf("expected 1 train, got %d", len(trains))
	}
	if trains[0].ID != "train-1" || trains[0].SourceVersion != "snapshot-test" {
		t.Fatalf("unexpected train conversion: %+v", trains[0])
	}
	if len(stopsByTrain["train-1"]) != 2 {
		t.Fatalf("expected 2 stops for train-1, got %d", len(stopsByTrain["train-1"]))
	}
	if stopsByTrain["train-1"][0].TrainInstanceID != "train-1" {
		t.Fatalf("expected train id to be preserved on stops")
	}
}
