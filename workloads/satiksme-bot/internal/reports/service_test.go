package reports

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

func TestSubmitVehicleSightingUsesFallbackScopeWithoutLiveID(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	input := model.VehicleReportInput{
		Mode:             "tram",
		RouteLabel:       "1",
		Direction:        "b-a",
		Destination:      "Imanta",
		DepartureSeconds: 68420,
	}

	result, item, err := svc.SubmitVehicleSighting(ctx, 5, input, now)
	if err != nil {
		t.Fatalf("SubmitVehicleSighting() error = %v", err)
	}
	if !result.Accepted || item == nil {
		t.Fatalf("expected accepted report, got %+v item=%v", result, item)
	}
	if want := "fallback:tram:1:b-a:imanta"; item.ScopeKey != want {
		t.Fatalf("ScopeKey = %q, want %q", item.ScopeKey, want)
	}
	if item.StopID != "" {
		t.Fatalf("StopID = %q, want empty", item.StopID)
	}
}

func TestPublicVehicleSightingsAndIncidentsRedactLiveRowID(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	result, item, err := svc.SubmitVehicleSighting(ctx, 5, model.VehicleReportInput{
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 68420,
		LiveRowID:        "67133",
	}, now)
	if err != nil || !result.Accepted {
		t.Fatalf("SubmitVehicleSighting() = %+v, err=%v", result, err)
	}

	visible, err := svc.VisibleSightings(ctx, &model.Catalog{}, "", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("VisibleSightings() error = %v", err)
	}
	if len(visible.VehicleSightings) != 1 {
		t.Fatalf("visible.VehicleSightings = %+v, want 1", visible.VehicleSightings)
	}
	if got := visible.VehicleSightings[0].LiveRowID; got != "" {
		t.Fatalf("public vehicle sighting liveRowId = %q, want empty", got)
	}
	if !strings.HasPrefix(visible.VehicleSightings[0].ID, "vehicle-report:pub-") || strings.Contains(visible.VehicleSightings[0].ID, item.ID) {
		t.Fatalf("public vehicle sighting ID = %q, want non-raw public ID for %q", visible.VehicleSightings[0].ID, item.ID)
	}

	incidents, err := svc.ListActiveIncidents(ctx, &model.Catalog{}, now.Add(time.Minute), 0, 20)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if len(incidents) != 1 || incidents[0].Vehicle == nil {
		t.Fatalf("incidents = %+v, want vehicle incident", incidents)
	}
	if incidents[0].SubjectID != "" || incidents[0].Vehicle.ScopeKey != "" || incidents[0].Vehicle.LiveRowID != "" {
		t.Fatalf("public vehicle incident exposes raw keys: subjectID=%q vehicle=%+v", incidents[0].SubjectID, incidents[0].Vehicle)
	}
}

func TestPublicStopSightingsUseOpaquePublicIDs(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	result, item, err := svc.SubmitStopSighting(ctx, 5, "3012", now)
	if err != nil || !result.Accepted {
		t.Fatalf("SubmitStopSighting() = %+v, err=%v", result, err)
	}

	visible, err := svc.VisibleSightings(ctx, &model.Catalog{}, "", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("VisibleSightings() error = %v", err)
	}
	if len(visible.StopSightings) != 1 {
		t.Fatalf("visible.StopSightings = %+v, want 1", visible.StopSightings)
	}
	if !strings.HasPrefix(visible.StopSightings[0].ID, "stop-report:pub-") || strings.Contains(visible.StopSightings[0].ID, item.ID) {
		t.Fatalf("public stop sighting ID = %q, want non-raw public ID for %q", visible.StopSightings[0].ID, item.ID)
	}
}

func TestSubmitVehicleSightingIgnoresLegacyStopID(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	result, item, err := svc.SubmitVehicleSighting(ctx, 9, model.VehicleReportInput{
		StopID:           "3012",
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 68542,
	}, now)
	if err != nil {
		t.Fatalf("SubmitVehicleSighting() error = %v", err)
	}
	if !result.Accepted || item == nil {
		t.Fatalf("expected accepted report, got %+v item=%v", result, item)
	}
	if item.StopID != "" {
		t.Fatalf("StopID = %q, want empty", item.StopID)
	}
}

func TestSubmitStopSightingAppliesDedupeAndCooldown(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)

	result, _, err := svc.SubmitStopSighting(ctx, 7, "3012", now)
	if err != nil || !result.Accepted {
		t.Fatalf("first SubmitStopSighting() = %+v, err=%v", result, err)
	}
	result, _, err = svc.SubmitStopSighting(ctx, 7, "3012", now.Add(30*time.Second))
	if err != nil || !result.Deduped || result.Accepted || result.RateLimited {
		t.Fatalf("duplicate SubmitStopSighting() = %+v, err=%v", result, err)
	}
	events, err := st.ListIncidentVoteEvents(ctx, StopIncidentID("3012"), now.Add(-time.Hour), 0)
	if err != nil {
		t.Fatalf("ListIncidentVoteEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1 after deduped report", len(events))
	}
	sightings, err := st.ListStopSightingsSince(ctx, now.Add(-time.Hour), "3012", 0)
	if err != nil {
		t.Fatalf("ListStopSightingsSince() error = %v", err)
	}
	if len(sightings) != 1 {
		t.Fatalf("len(sightings) = %d, want 1 after deduped report", len(sightings))
	}
	result, _, err = svc.SubmitStopSighting(ctx, 7, "3012", now.Add(2*time.Minute))
	if err != nil || !result.RateLimited || result.Reason != "same_vote" || result.Accepted {
		t.Fatalf("same vote SubmitStopSighting() = %+v, err=%v", result, err)
	}
}

func TestSubmitVehicleSightingBlocksSameVehicleInspectionWithinThirtyMinutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	input := model.VehicleReportInput{
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 320,
	}

	result, _, err := svc.SubmitVehicleSighting(ctx, 7, input, now)
	if err != nil || !result.Accepted {
		t.Fatalf("first SubmitVehicleSighting() = %+v, err=%v", result, err)
	}
	result, _, err = svc.SubmitVehicleSighting(ctx, 7, input, now.Add(10*time.Minute))
	if err != nil || !result.RateLimited || result.Reason != "same_vote" || result.Accepted {
		t.Fatalf("same vehicle SubmitVehicleSighting() = %+v, err=%v", result, err)
	}
}

func TestSubmitVehicleSightingDedupesRapidDuplicateBeforeLogging(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	input := model.VehicleReportInput{
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 320,
		LiveRowID:        "live-22-a-b-1",
	}

	result, _, err := svc.SubmitVehicleSighting(ctx, 7, input, now)
	if err != nil || !result.Accepted {
		t.Fatalf("first SubmitVehicleSighting() = %+v, err=%v", result, err)
	}
	result, _, err = svc.SubmitVehicleSighting(ctx, 7, input, now.Add(20*time.Second))
	if err != nil || !result.Deduped || result.Accepted || result.RateLimited {
		t.Fatalf("duplicate SubmitVehicleSighting() = %+v, err=%v", result, err)
	}
	events, err := st.ListIncidentVoteEvents(ctx, VehicleIncidentID(VehicleScopeKey(input)), now.Add(-time.Hour), 0)
	if err != nil {
		t.Fatalf("ListIncidentVoteEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1 after deduped report", len(events))
	}
	sightings, err := st.ListVehicleSightingsSince(ctx, now.Add(-time.Hour), "", 0)
	if err != nil {
		t.Fatalf("ListVehicleSightingsSince() error = %v", err)
	}
	if len(sightings) != 1 {
		t.Fatalf("len(sightings) = %d, want 1 after deduped report", len(sightings))
	}
}

func TestSubmitAreaReportCapsRadiusAndBuildsAreaIncident(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	normalizedDefault, err := NormalizeAreaReportInput(model.AreaReportInput{
		Latitude:    56.9501234,
		Longitude:   24.1103456,
		Description: "kontrole starp pieturām",
	})
	if err != nil {
		t.Fatalf("NormalizeAreaReportInput() error = %v", err)
	}
	if normalizedDefault.RadiusMeters != 100 {
		t.Fatalf("default RadiusMeters = %d, want 100", normalizedDefault.RadiusMeters)
	}
	input := model.AreaReportInput{
		Latitude:     56.9501234,
		Longitude:    24.1103456,
		RadiusMeters: 1200,
		Description:  "  kontrole   starp pieturām pie tuneļa  ",
	}

	result, item, err := svc.SubmitAreaReport(ctx, 17, input, now)
	if err != nil {
		t.Fatalf("SubmitAreaReport() error = %v", err)
	}
	if !result.Accepted || item == nil {
		t.Fatalf("SubmitAreaReport() = %+v item=%v, want accepted", result, item)
	}
	if item.RadiusMeters != 500 {
		t.Fatalf("RadiusMeters = %d, want capped 500", item.RadiusMeters)
	}
	if item.Description != "kontrole starp pieturām pie tuneļa" {
		t.Fatalf("Description = %q", item.Description)
	}
	if result.IncidentID != AreaIncidentID(item.ScopeKey) {
		t.Fatalf("IncidentID = %q, want %q", result.IncidentID, AreaIncidentID(item.ScopeKey))
	}
	for _, fragment := range []string{"56950", "24110", "kontrole"} {
		if strings.Contains(result.IncidentID, fragment) {
			t.Fatalf("IncidentID = %q exposes area fragment %q", result.IncidentID, fragment)
		}
	}

	visible, err := svc.VisibleSightings(ctx, &model.Catalog{}, "", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("VisibleSightings() error = %v", err)
	}
	if len(visible.AreaReports) != 1 || visible.AreaReports[0].RadiusMeters != 500 {
		t.Fatalf("visible.AreaReports = %+v, want capped report", visible.AreaReports)
	}
	if !strings.HasPrefix(visible.AreaReports[0].ID, "area-report:pub-") || strings.Contains(visible.AreaReports[0].ID, item.ID) {
		t.Fatalf("visible.AreaReports[0].ID = %q, want non-raw public area report id for %q", visible.AreaReports[0].ID, item.ID)
	}
	if visible.AreaReports[0].Latitude != 56.95 || visible.AreaReports[0].Longitude != 24.11 {
		t.Fatalf("visible area coordinates = (%f,%f), want public-rounded coordinates", visible.AreaReports[0].Latitude, visible.AreaReports[0].Longitude)
	}
	filtered, err := svc.VisibleSightings(ctx, &model.Catalog{}, "3012", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("VisibleSightings(filtered) error = %v", err)
	}
	if len(filtered.AreaReports) != 0 {
		t.Fatalf("filtered.AreaReports = %+v, want no area reports for stop view", filtered.AreaReports)
	}

	incidents, err := svc.ListActiveIncidents(ctx, &model.Catalog{}, now.Add(time.Minute), 17, 0)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if len(incidents) != 1 || incidents[0].Scope != IncidentScopeArea || incidents[0].Area == nil {
		t.Fatalf("incidents = %+v, want area incident", incidents)
	}
	if incidents[0].Area.Description != "kontrole starp pieturām pie tuneļa" {
		t.Fatalf("Area.Description = %q", incidents[0].Area.Description)
	}
	if incidents[0].SubjectID != "" || incidents[0].Area.ScopeKey != "" {
		t.Fatalf("area incident exposes scope keys: SubjectID=%q Area.ScopeKey=%q", incidents[0].SubjectID, incidents[0].Area.ScopeKey)
	}
	if incidents[0].Area.Latitude != 56.95 || incidents[0].Area.Longitude != 24.11 {
		t.Fatalf("incident area coordinates = (%f,%f), want public-rounded coordinates", incidents[0].Area.Latitude, incidents[0].Area.Longitude)
	}
	if incidents[0].Votes.UserValue != model.IncidentVoteOngoing {
		t.Fatalf("Votes.UserValue = %q, want ONGOING", incidents[0].Votes.UserValue)
	}
}

func TestPublicReadModelStoreShortCircuitsReportScans(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 30, 15, 0, 0, 123456789, time.UTC)
	st := &publicReadModelStoreStub{
		sightings: model.VisibleSightings{
			StopSightings: []model.PublicStopSighting{{
				ID:        "stop-public",
				StopID:    "3012",
				StopName:  "Centraltirgus",
				CreatedAt: now.Add(-time.Minute),
			}},
		},
		incidents: []model.IncidentSummary{
			{
				ID:           "stop:3012",
				Scope:        "stop",
				SubjectName:  "Centraltirgus",
				LastReportAt: now.Add(-time.Minute),
				LastReporter: "Amber Scout 171",
				Active:       true,
			},
			{
				ID:           "stop:9999",
				Scope:        "stop",
				SubjectName:  "Old",
				LastReportAt: now.Add(-2 * time.Minute),
				LastReporter: "Amber Scout 172",
				Resolved:     true,
			},
		},
		detail: &model.IncidentDetail{
			Summary: model.IncidentSummary{
				ID:           "stop:3012",
				Scope:        "stop",
				SubjectName:  "Centraltirgus",
				LastReportAt: now.Add(-time.Minute),
				LastReporter: "Amber Scout 171",
				Active:       true,
			},
			Events: []model.IncidentEvent{
				{
					ID:        "event-private",
					Kind:      "vote",
					Name:      "Kontrole",
					Nickname:  "Amber Scout 173",
					CreatedAt: now.Add(-time.Minute),
				},
			},
			Comments: []model.IncidentComment{
				{
					ID:         "comment-private",
					IncidentID: "stop:3012",
					Nickname:   "Amber Scout 174",
					Body:       "vēl stāv",
					CreatedAt:  now.Add(-30 * time.Second),
				},
			},
		},
	}
	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)

	visible, err := svc.VisibleSightings(ctx, &model.Catalog{}, " 3012 ", now, 7)
	if err != nil {
		t.Fatalf("VisibleSightings() error = %v", err)
	}
	if st.sightingsStopID != "3012" || st.sightingsLimit != 7 {
		t.Fatalf("ListPublicSightings args = (%q, %d), want (3012, 7)", st.sightingsStopID, st.sightingsLimit)
	}
	if len(visible.StopSightings) != 1 || visible.StopSightings[0].ID != "stop-public" {
		t.Fatalf("VisibleSightings() = %+v, want public read model payload", visible)
	}

	active, err := svc.ListActiveIncidents(ctx, &model.Catalog{}, now, 77, 5)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if st.incidentsViewerID != 77 || st.incidentsLimit != 5 {
		t.Fatalf("ListPublicIncidents args = (%d, %d), want (77, 5)", st.incidentsViewerID, st.incidentsLimit)
	}
	if len(active) != 2 {
		t.Fatalf("len(active) = %d, want 2", len(active))
	}
	if active[0].LastReporter != publicIncidentActorLabel || active[1].LastReporter != publicIncidentActorLabel {
		t.Fatalf("active reporters = %q, %q; want public labels", active[0].LastReporter, active[1].LastReporter)
	}
	if active[0].LastReportAt.Nanosecond() != 0 {
		t.Fatalf("active[0].LastReportAt = %s, want second-granularity public timestamp", active[0].LastReportAt.Format(time.RFC3339Nano))
	}

	mapVisible, err := svc.ListMapVisibleIncidents(ctx, &model.Catalog{}, now, 77)
	if err != nil {
		t.Fatalf("ListMapVisibleIncidents() error = %v", err)
	}
	if st.incidentsViewerID != 77 || st.incidentsLimit != 0 {
		t.Fatalf("ListPublicIncidents map args = (%d, %d), want (77, 0)", st.incidentsViewerID, st.incidentsLimit)
	}
	if len(mapVisible) != 1 || mapVisible[0].ID != "stop:3012" {
		t.Fatalf("mapVisible = %+v, want only unresolved public incident", mapVisible)
	}
	if mapVisible[0].LastReporter != publicIncidentActorLabel {
		t.Fatalf("mapVisible[0].LastReporter = %q, want public label", mapVisible[0].LastReporter)
	}

	detail, err := svc.IncidentDetail(ctx, &model.Catalog{}, " stop:3012 ", now, 77)
	if err != nil {
		t.Fatalf("IncidentDetail() error = %v", err)
	}
	if st.detailIncidentID != "stop:3012" || st.detailViewerID != 77 {
		t.Fatalf("GetPublicIncidentDetail args = (%q, %d), want (stop:3012, 77)", st.detailIncidentID, st.detailViewerID)
	}
	if detail.Summary.ID != "stop:3012" {
		t.Fatalf("IncidentDetail() = %+v, want public detail", detail)
	}
	if detail.Summary.LastReporter != publicIncidentActorLabel {
		t.Fatalf("detail.Summary.LastReporter = %q, want public label", detail.Summary.LastReporter)
	}
	if detail.Summary.LastReportAt.Nanosecond() != 0 {
		t.Fatalf("detail.Summary.LastReportAt = %s, want second-granularity public timestamp", detail.Summary.LastReportAt.Format(time.RFC3339Nano))
	}
	if len(detail.Events) != 1 || detail.Events[0].Nickname != publicIncidentActorLabel {
		t.Fatalf("detail.Events = %+v, want public labels", detail.Events)
	}
	if detail.Events[0].CreatedAt.Nanosecond() != 0 {
		t.Fatalf("detail.Events[0].CreatedAt = %s, want second-granularity public timestamp", detail.Events[0].CreatedAt.Format(time.RFC3339Nano))
	}
	if len(detail.Comments) != 1 || detail.Comments[0].Nickname != publicIncidentActorLabel {
		t.Fatalf("detail.Comments = %+v, want public labels", detail.Comments)
	}
	if detail.Comments[0].CreatedAt.Nanosecond() != 0 {
		t.Fatalf("detail.Comments[0].CreatedAt = %s, want second-granularity public timestamp", detail.Comments[0].CreatedAt.Format(time.RFC3339Nano))
	}
}

func TestSubmitMapReportsCapsAtFivePerThirtyMinutes(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	for index := 0; index < 5; index++ {
		result, _, err := svc.SubmitStopSighting(ctx, 7, string(rune('a'+index)), now.Add(time.Duration(index)*time.Minute))
		if err != nil || !result.Accepted {
			t.Fatalf("SubmitStopSighting(%d) = %+v, err=%v", index, result, err)
		}
	}
	result, _, err := svc.SubmitStopSighting(ctx, 7, "z", now.Add(6*time.Minute))
	if err != nil {
		t.Fatalf("sixth SubmitStopSighting() error = %v", err)
	}
	if result.Accepted || !result.RateLimited || result.Reason != "map_report_limit" {
		t.Fatalf("sixth SubmitStopSighting() = %+v, want map_report_limit", result)
	}
	count, err := st.CountMapReportsByUserSince(ctx, 7, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CountMapReportsByUserSince() error = %v", err)
	}
	if count != 5 {
		t.Fatalf("count = %d, want 5", count)
	}
}

func TestVoteIncidentAllowsStateChangesLogsEventsAndCapsVoteActions(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	catalog := &model.Catalog{Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}}}
	incidentID := StopIncidentID("3012")
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "legacy",
		StopID:    "3012",
		UserID:    99,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}

	if _, err := svc.VoteIncident(ctx, catalog, incidentID, 7, model.IncidentVoteOngoing, now); err != nil {
		t.Fatalf("VoteIncident(ongoing) error = %v", err)
	}
	summary, err := svc.VoteIncident(ctx, catalog, incidentID, 7, model.IncidentVoteCleared, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("VoteIncident(cleared) error = %v", err)
	}
	if summary.Ongoing != 0 || summary.Cleared != 1 || summary.UserValue != model.IncidentVoteCleared {
		t.Fatalf("summary = %+v", summary)
	}
	events, err := st.ListIncidentVoteEvents(ctx, incidentID, now.Add(-time.Hour), 0)
	if err != nil {
		t.Fatalf("ListIncidentVoteEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2", len(events))
	}

	value := model.IncidentVoteCleared
	for index := 2; index < voteActionLimit; index++ {
		if value == model.IncidentVoteOngoing {
			value = model.IncidentVoteCleared
		} else {
			value = model.IncidentVoteOngoing
		}
		if _, err := svc.VoteIncident(ctx, catalog, incidentID, 7, value, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatalf("VoteIncident(%d) error = %v", index, err)
		}
	}
	var rateErr *RateLimitError
	if _, err := svc.VoteIncident(ctx, catalog, incidentID, 7, model.IncidentVoteOngoing, now.Add(voteActionLimit*time.Minute)); !errors.As(err, &rateErr) || rateErr.Reason != "vote_action_limit" {
		t.Fatalf("VoteIncident(limit) error = %v, want vote_action_limit", err)
	}
}

func TestVoteActionLimitIsSharedBetweenWebAndTelegramChatVotes(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	catalog := &model.Catalog{Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}}}
	incidentID := StopIncidentID("3012")
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "legacy",
		StopID:    "3012",
		UserID:    99,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}

	value := model.IncidentVoteOngoing
	for index := 0; index < voteActionLimit; index++ {
		if value == model.IncidentVoteOngoing {
			value = model.IncidentVoteCleared
		} else {
			value = model.IncidentVoteOngoing
		}
		at := now.Add(time.Duration(index) * time.Minute)
		if index%2 == 0 {
			if _, err := svc.VoteIncident(ctx, catalog, incidentID, 7, value, at); err != nil {
				t.Fatalf("VoteIncident(%d) error = %v", index, err)
			}
			continue
		}
		if _, err := svc.RecordIncidentVoteFromSource(ctx, catalog, incidentID, 7, value, model.IncidentVoteSourceTelegramChat, generateID(), at); err != nil {
			t.Fatalf("RecordIncidentVoteFromSource(%d) error = %v", index, err)
		}
	}

	var rateErr *RateLimitError
	if _, err := svc.VoteIncident(ctx, catalog, incidentID, 7, model.IncidentVoteCleared, now.Add(voteActionLimit*time.Minute)); !errors.As(err, &rateErr) || rateErr.Reason != "vote_action_limit" {
		t.Fatalf("VoteIncident(after mixed sources) error = %v, want vote_action_limit", err)
	}
}

func TestIncidentDetailUsesVoteEventsWithLegacyReportFallback(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	catalog := &model.Catalog{Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}}}
	incidentID := StopIncidentID("3012")
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "legacy",
		StopID:    "3012",
		UserID:    99,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}

	detail, err := svc.IncidentDetail(ctx, catalog, incidentID, now, 0)
	if err != nil {
		t.Fatalf("IncidentDetail(legacy) error = %v", err)
	}
	if len(detail.Events) != 1 || detail.Events[0].Name != "Kontrole" {
		t.Fatalf("legacy detail.Events = %#v", detail.Events)
	}
	if detail.Summary.Votes.Ongoing != 0 || detail.Summary.Votes.Cleared != 0 {
		t.Fatalf("legacy votes = %+v, want zero current votes", detail.Summary.Votes)
	}

	if _, _, err := svc.SubmitStopSighting(ctx, 7, "3012", now.Add(time.Minute)); err != nil {
		t.Fatalf("SubmitStopSighting() error = %v", err)
	}
	detail, err = svc.IncidentDetail(ctx, catalog, incidentID, now.Add(2*time.Minute), 0)
	if err != nil {
		t.Fatalf("IncidentDetail(vote event) error = %v", err)
	}
	if detail.Summary.Votes.Ongoing != 1 || detail.Summary.Votes.Cleared != 0 {
		t.Fatalf("votes = %+v, want one ongoing", detail.Summary.Votes)
	}
	if len(detail.Events) != 2 {
		t.Fatalf("len(detail.Events) = %d, want 2", len(detail.Events))
	}
	if detail.Events[1].Name != "Kontrole" {
		t.Fatalf("detail.Events[1] = %#v", detail.Events[1])
	}
}

func TestVisibleSightingsResolvesVehicleStopNameThroughAlias(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID:               "legacy-veh-1",
		StopID:           "432",
		UserID:           11,
		Mode:             "bus",
		RouteLabel:       "15",
		Direction:        "a-b",
		Destination:      "Purvciems",
		DepartureSeconds: 46800,
		ScopeKey:         "legacy:bus:15:a-b:purvciems",
		CreatedAt:        now,
	}); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
	}

	visible, err := svc.VisibleSightings(ctx, &model.Catalog{
		Stops: []model.Stop{{ID: "0432", Name: "Slavu iela"}},
	}, "", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("VisibleSightings() error = %v", err)
	}
	if len(visible.VehicleSightings) != 1 {
		t.Fatalf("len(visible.VehicleSightings) = %d, want 1", len(visible.VehicleSightings))
	}
	if visible.VehicleSightings[0].StopName != "Slavu iela" {
		t.Fatalf("visible.VehicleSightings[0].StopName = %q, want Slavu iela", visible.VehicleSightings[0].StopName)
	}
}

func TestHiddenSmokeSightingsStayOutOfPublicSightingsButRemainInUserRecent(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
	now := time.Date(2026, 3, 10, 14, 0, 0, 0, time.UTC)
	catalog := &model.Catalog{
		Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
	}

	if _, _, err := svc.SubmitStopSightingWithOptions(ctx, 77, "3012", now, SubmitOptions{Hidden: true}); err != nil {
		t.Fatalf("SubmitStopSightingWithOptions() error = %v", err)
	}
	if _, _, err := svc.SubmitVehicleSightingWithOptions(ctx, 77, model.VehicleReportInput{
		StopID:           "3012",
		Mode:             "bus",
		RouteLabel:       "SMOKE",
		Direction:        "a-b",
		Destination:      "Smoke Destination",
		DepartureSeconds: 86340,
		LiveRowID:        "smoke-77",
	}, now, SubmitOptions{Hidden: true}); err != nil {
		t.Fatalf("SubmitVehicleSightingWithOptions() error = %v", err)
	}

	publicVisible, err := svc.VisibleSightings(ctx, catalog, "3012", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("VisibleSightings() error = %v", err)
	}
	if len(publicVisible.StopSightings) != 0 || len(publicVisible.VehicleSightings) != 0 {
		t.Fatalf("public visible sightings = %#v, want empty", publicVisible)
	}

	userVisible, err := svc.UserSightings(ctx, catalog, 77, "3012", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("UserSightings() error = %v", err)
	}
	if len(userVisible.StopSightings) != 1 {
		t.Fatalf("len(userVisible.StopSightings) = %d, want 1", len(userVisible.StopSightings))
	}
	if len(userVisible.VehicleSightings) != 0 {
		t.Fatalf("len(userVisible.VehicleSightings) = %d, want 0 for stop-filtered recent", len(userVisible.VehicleSightings))
	}

	userGlobalVisible, err := svc.UserSightings(ctx, catalog, 77, "", now.Add(time.Minute), 20)
	if err != nil {
		t.Fatalf("UserSightings(global) error = %v", err)
	}
	if len(userGlobalVisible.StopSightings) != 1 {
		t.Fatalf("len(userGlobalVisible.StopSightings) = %d, want 1", len(userGlobalVisible.StopSightings))
	}
	if len(userGlobalVisible.VehicleSightings) != 1 {
		t.Fatalf("len(userGlobalVisible.VehicleSightings) = %d, want 1", len(userGlobalVisible.VehicleSightings))
	}
	if userGlobalVisible.VehicleSightings[0].Destination != "Smoke Destination" {
		t.Fatalf("userGlobalVisible.VehicleSightings[0].Destination = %q", userGlobalVisible.VehicleSightings[0].Destination)
	}
}

type publicReadModelStoreStub struct {
	store.Store

	sightings       model.VisibleSightings
	sightingsStopID string
	sightingsLimit  int

	incidents         []model.IncidentSummary
	incidentsViewerID int64
	incidentsLimit    int

	detail           *model.IncidentDetail
	detailIncidentID string
	detailViewerID   int64
}

func (s *publicReadModelStoreStub) ListPublicSightings(_ context.Context, stopID string, limit int) (model.VisibleSightings, error) {
	s.sightingsStopID = stopID
	s.sightingsLimit = limit
	return s.sightings, nil
}

func (s *publicReadModelStoreStub) ListPublicIncidents(_ context.Context, viewerID int64, limit int) ([]model.IncidentSummary, error) {
	s.incidentsViewerID = viewerID
	s.incidentsLimit = limit
	return s.incidents, nil
}

func (s *publicReadModelStoreStub) GetPublicIncidentDetail(_ context.Context, incidentID string, viewerID int64) (*model.IncidentDetail, error) {
	s.detailIncidentID = incidentID
	s.detailViewerID = viewerID
	return s.detail, nil
}
