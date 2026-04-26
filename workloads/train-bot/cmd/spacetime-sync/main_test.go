package main

import (
	"strings"
	"testing"
	"time"

	"telegramtrainapp/internal/recovery"
)

func TestDefaultServiceDateUsesTargetTimezone(t *testing.T) {
	t.Parallel()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.April, 8, 22, 30, 0, 0, time.UTC)
	if got := defaultServiceDate(now, loc); got != "2026-04-09" {
		t.Fatalf("unexpected service date: %s", got)
	}
}

func TestRenderSyncSummaryIncludesCounts(t *testing.T) {
	t.Parallel()

	summary := renderSyncSummary(recovery.SyncResult{
		ServiceDate:      "2026-04-09",
		SourceVersion:    "agg-2026-04-09-vivi_gtfs",
		Stations:         135,
		Trains:           306,
		Stops:            5101,
		ExistingStations: 0,
		ExistingTrains:   0,
		ExistingStops:    0,
		Synced:           true,
	}, false)

	for _, snippet := range []string{
		"status: synced",
		"service date: 2026-04-09",
		"source version: agg-2026-04-09-vivi_gtfs",
		"local snapshot: stations=135 trains=306 stops=5101",
		"spacetime before: stations=0 trains=0 stops=0",
	} {
		if !strings.Contains(summary, snippet) {
			t.Fatalf("summary missing %q:\n%s", snippet, summary)
		}
	}
}
