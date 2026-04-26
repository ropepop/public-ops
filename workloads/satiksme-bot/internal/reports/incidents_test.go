package reports

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

func newIncidentTestService(t *testing.T) (context.Context, *store.SQLiteStore, *Service) {
	t.Helper()

	ctx := context.Background()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return ctx, st, NewService(st, 3*time.Minute, 90*time.Second, 30*time.Minute)
}

func TestListActiveIncidentsReturns24HourHistoryAndResolvedState(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)

	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    11,
		CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertStopSighting(recent) error = %v", err)
	}
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-old",
		StopID:    "9999",
		UserID:    12,
		CreatedAt: now.Add(-25 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertStopSighting(old) error = %v", err)
	}
	for _, vote := range []model.IncidentVote{
		{
			IncidentID: StopIncidentID("3012"),
			UserID:     21,
			Nickname:   "Amber Scout 121",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-40 * time.Minute),
			UpdatedAt:  now.Add(-40 * time.Minute),
		},
		{
			IncidentID: StopIncidentID("3012"),
			UserID:     22,
			Nickname:   "Amber Scout 122",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-30 * time.Minute),
			UpdatedAt:  now.Add(-30 * time.Minute),
		},
	} {
		if err := st.UpsertIncidentVote(ctx, vote); err != nil {
			t.Fatalf("UpsertIncidentVote() error = %v", err)
		}
	}

	items, err := svc.ListActiveIncidents(ctx, &model.Catalog{
		Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
	}, now, 0, 20)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != StopIncidentID("3012") {
		t.Fatalf("items[0].ID = %q", items[0].ID)
	}
	if !items[0].Resolved {
		t.Fatalf("items[0].Resolved = false, want true")
	}
	if items[0].Active {
		t.Fatalf("items[0].Active = true, want false")
	}
	if items[0].Votes.Cleared != 2 {
		t.Fatalf("items[0].Votes.Cleared = %d, want 2", items[0].Votes.Cleared)
	}
}

func TestIncidentDetailIgnoresVotesAndCommentsOlderThan24Hours(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)
	incidentID := StopIncidentID("3012")

	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    11,
		CreatedAt: now.Add(-2 * time.Hour),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}
	for _, vote := range []model.IncidentVote{
		{
			IncidentID: incidentID,
			UserID:     31,
			Nickname:   "Amber Scout 131",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-26 * time.Hour),
			UpdatedAt:  now.Add(-26 * time.Hour),
		},
		{
			IncidentID: incidentID,
			UserID:     32,
			Nickname:   "Amber Scout 132",
			Value:      model.IncidentVoteOngoing,
			CreatedAt:  now.Add(-20 * time.Minute),
			UpdatedAt:  now.Add(-20 * time.Minute),
		},
	} {
		if err := st.UpsertIncidentVote(ctx, vote); err != nil {
			t.Fatalf("UpsertIncidentVote() error = %v", err)
		}
	}
	for _, comment := range []model.IncidentComment{
		{
			ID:         "comment-old",
			IncidentID: incidentID,
			UserID:     41,
			Nickname:   "Amber Scout 141",
			Body:       "old",
			CreatedAt:  now.Add(-25 * time.Hour),
		},
		{
			ID:         "comment-recent",
			IncidentID: incidentID,
			UserID:     42,
			Nickname:   "Amber Scout 142",
			Body:       "recent",
			CreatedAt:  now.Add(-15 * time.Minute),
		},
	} {
		if err := st.InsertIncidentComment(ctx, comment); err != nil {
			t.Fatalf("InsertIncidentComment() error = %v", err)
		}
	}

	detail, err := svc.IncidentDetail(ctx, &model.Catalog{
		Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
	}, incidentID, now, 32)
	if err != nil {
		t.Fatalf("IncidentDetail() error = %v", err)
	}
	if detail == nil {
		t.Fatalf("IncidentDetail() = nil")
	}
	if detail.Summary.Votes.Cleared != 0 || detail.Summary.Votes.Ongoing != 1 {
		t.Fatalf("detail.Summary.Votes = %+v", detail.Summary.Votes)
	}
	if detail.Summary.Resolved {
		t.Fatalf("detail.Summary.Resolved = true, want false")
	}
	if len(detail.Comments) != 1 || detail.Comments[0].ID != "comment-recent" {
		t.Fatalf("detail.Comments = %#v", detail.Comments)
	}
}

func TestListMapVisibleIncidentsTracksResolutionThreshold(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)
	incidentID := StopIncidentID("3012")
	catalog := &model.Catalog{
		Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
	}

	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    11,
		CreatedAt: now.Add(-90 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}

	visible, err := svc.ListMapVisibleIncidents(ctx, catalog, now, 0)
	if err != nil {
		t.Fatalf("ListMapVisibleIncidents(initial) error = %v", err)
	}
	if len(visible) != 1 {
		t.Fatalf("len(visible initial) = %d, want 1", len(visible))
	}

	for _, vote := range []model.IncidentVote{
		{
			IncidentID: incidentID,
			UserID:     51,
			Nickname:   "Amber Scout 151",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-20 * time.Minute),
			UpdatedAt:  now.Add(-20 * time.Minute),
		},
		{
			IncidentID: incidentID,
			UserID:     52,
			Nickname:   "Amber Scout 152",
			Value:      model.IncidentVoteCleared,
			CreatedAt:  now.Add(-10 * time.Minute),
			UpdatedAt:  now.Add(-10 * time.Minute),
		},
	} {
		if err := st.UpsertIncidentVote(ctx, vote); err != nil {
			t.Fatalf("UpsertIncidentVote(clear) error = %v", err)
		}
	}

	visible, err = svc.ListMapVisibleIncidents(ctx, catalog, now, 0)
	if err != nil {
		t.Fatalf("ListMapVisibleIncidents(cleared) error = %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("len(visible cleared) = %d, want 0", len(visible))
	}

	if err := st.UpsertIncidentVote(ctx, model.IncidentVote{
		IncidentID: incidentID,
		UserID:     52,
		Nickname:   "Amber Scout 152",
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  now.Add(-10 * time.Minute),
		UpdatedAt:  now.Add(-5 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertIncidentVote(reopen) error = %v", err)
	}

	visible, err = svc.ListMapVisibleIncidents(ctx, catalog, now, 52)
	if err != nil {
		t.Fatalf("ListMapVisibleIncidents(reopened) error = %v", err)
	}
	if len(visible) != 1 {
		t.Fatalf("len(visible reopened) = %d, want 1", len(visible))
	}
	if visible[0].Votes.Cleared != 1 || visible[0].Votes.Ongoing != 1 {
		t.Fatalf("visible[0].Votes = %+v", visible[0].Votes)
	}
	if visible[0].Votes.UserValue != model.IncidentVoteOngoing {
		t.Fatalf("visible[0].Votes.UserValue = %q", visible[0].Votes.UserValue)
	}
}

func TestListActiveIncidentsDoesNotTruncateAtOldIncidentCap(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)
	stops := make([]model.Stop, 0, 405)

	for index := 0; index < 405; index += 1 {
		stopID := fmt.Sprintf("%04d", 3000+index)
		stops = append(stops, model.Stop{ID: stopID, Name: "Stop " + stopID})
		if err := st.InsertStopSighting(ctx, model.StopSighting{
			ID:        fmt.Sprintf("stop-%d", index),
			StopID:    stopID,
			UserID:    int64(index + 1),
			CreatedAt: now.Add(-time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatalf("InsertStopSighting(%d) error = %v", index, err)
		}
	}

	items, err := svc.ListActiveIncidents(ctx, &model.Catalog{Stops: stops}, now, 0, 0)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if len(items) != 405 {
		t.Fatalf("len(items) = %d, want 405", len(items))
	}
}
