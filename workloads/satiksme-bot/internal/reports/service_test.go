package reports

import (
	"context"
	"errors"
	"path/filepath"
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
