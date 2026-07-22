package reports

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	if len(detail.Comments) != 1 || detail.Comments[0].ID != publicIncidentCommentID(incidentID, "comment-recent", now.Add(-15*time.Minute)) {
		t.Fatalf("detail.Comments = %#v", detail.Comments)
	}
}

func TestPublicIncidentFallbackRedactsReporterNicknames(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)
	catalog := &model.Catalog{
		Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}},
	}
	incidentID := StopIncidentID("3012")

	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    11,
		CreatedAt: now.Add(-20 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}
	if _, err := svc.VoteIncident(ctx, catalog, incidentID, 22, model.IncidentVoteOngoing, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("VoteIncident() error = %v", err)
	}
	comment, err := svc.AddIncidentComment(ctx, catalog, incidentID, 33, "vēl stāv", now.Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("AddIncidentComment() error = %v", err)
	}
	if comment.Nickname != publicIncidentActorLabel {
		t.Fatalf("comment.Nickname = %q, want public label", comment.Nickname)
	}

	active, err := svc.ListActiveIncidents(ctx, catalog, now, 0, 0)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("len(active) = %d, want 1", len(active))
	}
	if active[0].LastReporter != publicIncidentActorLabel {
		t.Fatalf("active[0].LastReporter = %q, want public label", active[0].LastReporter)
	}

	visible, err := svc.ListMapVisibleIncidents(ctx, catalog, now, 0)
	if err != nil {
		t.Fatalf("ListMapVisibleIncidents() error = %v", err)
	}
	if len(visible) != 1 {
		t.Fatalf("len(visible) = %d, want 1", len(visible))
	}
	if visible[0].LastReporter != publicIncidentActorLabel {
		t.Fatalf("visible[0].LastReporter = %q, want public label", visible[0].LastReporter)
	}

	detail, err := svc.IncidentDetail(ctx, catalog, incidentID, now, 0)
	if err != nil {
		t.Fatalf("IncidentDetail() error = %v", err)
	}
	if detail.Summary.LastReporter != publicIncidentActorLabel {
		t.Fatalf("detail.Summary.LastReporter = %q, want public label", detail.Summary.LastReporter)
	}
	for _, event := range detail.Events {
		if event.Nickname != publicIncidentActorLabel {
			t.Fatalf("event %q Nickname = %q, want public label", event.ID, event.Nickname)
		}
		if event.ID != "" {
			t.Fatalf("event ID = %q, want omitted public ID", event.ID)
		}
		if strings.Contains(event.ID, "stop-recent") || strings.Contains(event.ID, "channel:") {
			t.Fatalf("event ID exposes raw source ID: %q", event.ID)
		}
		if event.Kind != "" {
			t.Fatalf("event Kind = %q, want omitted public source kind", event.Kind)
		}
	}
	if len(detail.Comments) != 1 {
		t.Fatalf("len(detail.Comments) = %d, want 1", len(detail.Comments))
	}
	if detail.Comments[0].Nickname != publicIncidentActorLabel {
		t.Fatalf("detail.Comments[0].Nickname = %q, want public label", detail.Comments[0].Nickname)
	}
}

func TestPublicAreaIncidentIDsDoNotExposeUserText(t *testing.T) {
	ctx, _, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)
	_, _, err := svc.SubmitAreaReport(ctx, 44, model.AreaReportInput{
		Latitude:     56.9532,
		Longitude:    24.1534,
		RadiusMeters: 250,
		Description:  "Kontrole pie centra",
	}, now)
	if err != nil {
		t.Fatalf("SubmitAreaReport() error = %v", err)
	}

	active, err := svc.ListActiveIncidents(ctx, &model.Catalog{}, now, 0, 0)
	if err != nil {
		t.Fatalf("ListActiveIncidents() error = %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("len(active) = %d, want 1", len(active))
	}
	if !strings.HasPrefix(active[0].ID, "area:pub-") {
		t.Fatalf("area incident ID = %q, want opaque public ID", active[0].ID)
	}
	if strings.Contains(active[0].ID, "kontrole") || strings.Contains(active[0].ID, "centra") || strings.Contains(active[0].ID, "56953") {
		t.Fatalf("area incident ID exposes source text or location bucket: %q", active[0].ID)
	}
	if active[0].Area == nil || active[0].Area.Description != "Kontrole pie centra" {
		t.Fatalf("area public description should remain in area payload, got %+v", active[0].Area)
	}
}

func TestAddIncidentCommentCapsUserCommentActions(t *testing.T) {
	ctx, st, svc := newIncidentTestService(t)
	now := time.Date(2026, 3, 20, 18, 55, 0, 0, time.UTC)
	catalog := &model.Catalog{Stops: []model.Stop{{ID: "3012", Name: "Centrāltirgus"}}}
	incidentID := StopIncidentID("3012")
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-recent",
		StopID:    "3012",
		UserID:    11,
		CreatedAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}

	for index := 0; index < 10; index++ {
		if _, err := svc.AddIncidentComment(ctx, catalog, incidentID, 77, fmt.Sprintf("comment %d", index), now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatalf("AddIncidentComment(%d) error = %v", index, err)
		}
	}
	var rateErr *RateLimitError
	if _, err := svc.AddIncidentComment(ctx, catalog, incidentID, 77, "one too many", now.Add(10*time.Minute)); !errors.As(err, &rateErr) || rateErr.Reason != "comment_action_limit" {
		t.Fatalf("AddIncidentComment(limit) error = %v, want comment_action_limit", err)
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
