package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"satiksmebot/internal/model"
)

func TestSQLiteStoreRoundTripAndCleanup(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-1",
		StopID:    "3012",
		UserID:    11,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID:               "veh-1",
		StopID:           "3012",
		UserID:           11,
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 68542,
		LiveRowID:        "78648",
		ScopeKey:         "live:3012:78648",
		CreatedAt:        now,
	}); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
	}

	stopItems, err := st.ListStopSightingsSince(ctx, now.Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatalf("ListStopSightingsSince() error = %v", err)
	}
	if len(stopItems) != 1 {
		t.Fatalf("len(stopItems) = %d, want 1", len(stopItems))
	}
	vehicleItems, err := st.ListVehicleSightingsSince(ctx, now.Add(-time.Hour), "3012", 10)
	if err != nil {
		t.Fatalf("ListVehicleSightingsSince() error = %v", err)
	}
	if len(vehicleItems) != 1 {
		t.Fatalf("len(vehicleItems) = %d, want 1", len(vehicleItems))
	}

	result, err := st.CleanupExpired(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	if result.StopSightingsDeleted != 1 || result.VehicleSightingsDeleted != 1 {
		t.Fatalf("CleanupExpired() = %+v", result)
	}
}

func TestSQLiteStoreStopReportDedupeClaimBlocksOnlySameScopeInsideWindow(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 4, 25, 1, 45, 0, 0, time.UTC)
	const userID int64 = 566
	insertStop := func(id string, stopID string, at time.Time) error {
		incidentID := "stop:" + stopID
		vote, event := testIncidentVoteAction(id, incidentID, userID, at)
		return st.InsertStopSightingWithVote(ctx, model.StopSighting{
			ID:        id,
			StopID:    stopID,
			UserID:    userID,
			CreatedAt: at,
		}, vote, event, 90*time.Second)
	}

	if err := insertStop("stop-1", "3012", now); err != nil {
		t.Fatalf("first InsertStopSightingWithVote() error = %v", err)
	}
	if err := insertStop("stop-duplicate", "3012", now.Add(20*time.Second)); !errors.Is(err, ErrDuplicateReport) {
		t.Fatalf("duplicate InsertStopSightingWithVote() error = %v, want ErrDuplicateReport", err)
	}
	if err := insertStop("stop-other", "4242", now.Add(30*time.Second)); err != nil {
		t.Fatalf("different stop InsertStopSightingWithVote() error = %v", err)
	}
	if err := insertStop("stop-after-window", "3012", now.Add(91*time.Second)); err != nil {
		t.Fatalf("after-window InsertStopSightingWithVote() error = %v", err)
	}

	sightings, err := st.ListStopSightingsSince(ctx, now.Add(-time.Minute), "", 0)
	if err != nil {
		t.Fatalf("ListStopSightingsSince() error = %v", err)
	}
	if len(sightings) != 3 {
		t.Fatalf("len(sightings) = %d, want 3", len(sightings))
	}
	events, err := st.ListIncidentVoteEvents(ctx, "stop:3012", now.Add(-time.Minute), 0)
	if err != nil {
		t.Fatalf("ListIncidentVoteEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 for the original and after-window reports", len(events))
	}
}

func TestSQLiteStoreVehicleReportDedupeClaimBlocksOnlySameScopeInsideWindow(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 4, 25, 1, 45, 0, 0, time.UTC)
	const userID int64 = 566
	insertVehicle := func(id string, scopeKey string, at time.Time) error {
		incidentID := "vehicle:" + scopeKey
		vote, event := testIncidentVoteAction(id, incidentID, userID, at)
		return st.InsertVehicleSightingWithVote(ctx, model.VehicleSighting{
			ID:               id,
			UserID:           userID,
			Mode:             "bus",
			RouteLabel:       "22",
			Direction:        "a-b",
			Destination:      "Lidosta",
			DepartureSeconds: 320,
			LiveRowID:        id,
			ScopeKey:         scopeKey,
			CreatedAt:        at,
		}, vote, event, 90*time.Second)
	}

	if err := insertVehicle("vehicle-1", "live:bus:22:a-b:1", now); err != nil {
		t.Fatalf("first InsertVehicleSightingWithVote() error = %v", err)
	}
	if err := insertVehicle("vehicle-duplicate", "live:bus:22:a-b:1", now.Add(20*time.Second)); !errors.Is(err, ErrDuplicateReport) {
		t.Fatalf("duplicate InsertVehicleSightingWithVote() error = %v, want ErrDuplicateReport", err)
	}
	if err := insertVehicle("vehicle-other", "live:bus:22:a-b:2", now.Add(30*time.Second)); err != nil {
		t.Fatalf("different vehicle InsertVehicleSightingWithVote() error = %v", err)
	}
	if err := insertVehicle("vehicle-after-window", "live:bus:22:a-b:1", now.Add(91*time.Second)); err != nil {
		t.Fatalf("after-window InsertVehicleSightingWithVote() error = %v", err)
	}

	sightings, err := st.ListVehicleSightingsSince(ctx, now.Add(-time.Minute), "", 0)
	if err != nil {
		t.Fatalf("ListVehicleSightingsSince() error = %v", err)
	}
	if len(sightings) != 3 {
		t.Fatalf("len(sightings) = %d, want 3", len(sightings))
	}
	events, err := st.ListIncidentVoteEvents(ctx, "vehicle:live:bus:22:a-b:1", now.Add(-time.Minute), 0)
	if err != nil {
		t.Fatalf("ListIncidentVoteEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("len(events) = %d, want 2 for the original and after-window reports", len(events))
	}
}

func TestSQLiteStoreReportDumpQueueLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	item := ReportDumpItem{
		ID:            "dump-1",
		Payload:       "payload",
		CreatedAt:     now,
		NextAttemptAt: now,
	}
	if err := st.EnqueueReportDump(ctx, item); err != nil {
		t.Fatalf("EnqueueReportDump() error = %v", err)
	}

	pending, err := st.PendingReportDumpCount(ctx)
	if err != nil {
		t.Fatalf("PendingReportDumpCount() error = %v", err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}

	loaded, err := st.NextReportDump(ctx, now)
	if err != nil {
		t.Fatalf("NextReportDump() error = %v", err)
	}
	if loaded == nil || loaded.ID != item.ID || loaded.Payload != item.Payload {
		t.Fatalf("NextReportDump() = %+v", loaded)
	}

	lastAttemptAt := now.Add(30 * time.Second)
	nextAttemptAt := now.Add(2 * time.Minute)
	if err := st.UpdateReportDumpFailure(ctx, item.ID, 1, nextAttemptAt, lastAttemptAt, "telegram timeout"); err != nil {
		t.Fatalf("UpdateReportDumpFailure() error = %v", err)
	}

	beforeRetry, err := st.NextReportDump(ctx, now.Add(90*time.Second))
	if err != nil {
		t.Fatalf("NextReportDump(before retry) error = %v", err)
	}
	if beforeRetry != nil {
		t.Fatalf("NextReportDump(before retry) = %+v, want nil", beforeRetry)
	}

	peekItem, err := st.PeekNextReportDump(ctx)
	if err != nil {
		t.Fatalf("PeekNextReportDump() error = %v", err)
	}
	if peekItem == nil {
		t.Fatalf("PeekNextReportDump() = nil")
	}
	if !peekItem.NextAttemptAt.Equal(nextAttemptAt) || peekItem.Attempts != 1 {
		t.Fatalf("PeekNextReportDump() = %+v", peekItem)
	}

	retryItem, err := st.NextReportDump(ctx, nextAttemptAt)
	if err != nil {
		t.Fatalf("NextReportDump(retry) error = %v", err)
	}
	if retryItem == nil {
		t.Fatalf("NextReportDump(retry) = nil")
	}
	if retryItem.Attempts != 1 || retryItem.LastError != "telegram timeout" {
		t.Fatalf("retryItem = %+v", retryItem)
	}
	if !retryItem.LastAttemptAt.Equal(lastAttemptAt) || !retryItem.NextAttemptAt.Equal(nextAttemptAt) {
		t.Fatalf("retry timing = %+v", retryItem)
	}

	if err := st.DeleteReportDump(ctx, item.ID); err != nil {
		t.Fatalf("DeleteReportDump() error = %v", err)
	}
	pending, err = st.PendingReportDumpCount(ctx)
	if err != nil {
		t.Fatalf("PendingReportDumpCount(after delete) error = %v", err)
	}
	if pending != 0 {
		t.Fatalf("pending after delete = %d, want 0", pending)
	}
}

func testIncidentVoteAction(id string, incidentID string, userID int64, at time.Time) (model.IncidentVote, model.IncidentVoteEvent) {
	return model.IncidentVote{
			IncidentID: incidentID,
			UserID:     userID,
			Nickname:   "Silver Scout 566",
			Value:      model.IncidentVoteOngoing,
			CreatedAt:  at,
			UpdatedAt:  at,
		}, model.IncidentVoteEvent{
			ID:         "event-" + id,
			IncidentID: incidentID,
			UserID:     userID,
			Nickname:   "Silver Scout 566",
			Value:      model.IncidentVoteOngoing,
			Source:     model.IncidentVoteSourceMapReport,
			CreatedAt:  at,
		}
}

func TestExportSQLiteStateSnapshotMapsCurrentModelRecords(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "satiksme.db")
	st, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 3, 31, 9, 30, 0, 0, time.UTC)
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "stop-1",
		StopID:    "3012",
		UserID:    42,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertStopSighting() error = %v", err)
	}
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID:               "veh-1",
		StopID:           "3012",
		UserID:           42,
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 600,
		LiveRowID:        "live-1",
		ScopeKey:         "live:bus:22:a-b:live-1",
		CreatedAt:        now.Add(30 * time.Second),
	}); err != nil {
		t.Fatalf("InsertVehicleSighting() error = %v", err)
	}
	if err := st.UpsertIncidentVote(ctx, model.IncidentVote{
		IncidentID: "stop:3012",
		UserID:     42,
		Nickname:   "Amber Scout 101",
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  now.Add(time.Minute),
		UpdatedAt:  now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("UpsertIncidentVote() error = %v", err)
	}
	if err := st.InsertIncidentComment(ctx, model.IncidentComment{
		ID:         "comment-1",
		IncidentID: "stop:3012",
		UserID:     42,
		Nickname:   "Amber Scout 101",
		Body:       "Kontrole pie tirgus",
		CreatedAt:  now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("InsertIncidentComment() error = %v", err)
	}
	if err := st.EnqueueReportDump(ctx, ReportDumpItem{
		ID:            "dump-1",
		Payload:       "{\"incidentId\":\"stop:3012\"}",
		Attempts:      1,
		CreatedAt:     now.Add(4 * time.Minute),
		NextAttemptAt: now.Add(5 * time.Minute),
		LastAttemptAt: now.Add(4 * time.Minute),
		LastError:     "telegram timeout",
	}); err != nil {
		t.Fatalf("EnqueueReportDump() error = %v", err)
	}

	snapshot, err := ExportSQLiteStateSnapshot(ctx, dbPath, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("ExportSQLiteStateSnapshot() error = %v", err)
	}

	if len(snapshot.StopSightings) != 1 || snapshot.StopSightings[0].ID != "stop-1" {
		t.Fatalf("snapshot.StopSightings = %+v", snapshot.StopSightings)
	}
	if len(snapshot.VehicleSightings) != 1 || snapshot.VehicleSightings[0].ScopeKey != "live:bus:22:a-b:live-1" {
		t.Fatalf("snapshot.VehicleSightings = %+v", snapshot.VehicleSightings)
	}
	if len(snapshot.IncidentVotes) != 1 || snapshot.IncidentVotes[0].Value != model.IncidentVoteOngoing {
		t.Fatalf("snapshot.IncidentVotes = %+v", snapshot.IncidentVotes)
	}
	if len(snapshot.IncidentComments) != 1 || snapshot.IncidentComments[0].Body != "Kontrole pie tirgus" {
		t.Fatalf("snapshot.IncidentComments = %+v", snapshot.IncidentComments)
	}
	if len(snapshot.ReportDumpItems) != 1 || snapshot.ReportDumpItems[0].LastError != "telegram timeout" {
		t.Fatalf("snapshot.ReportDumpItems = %+v", snapshot.ReportDumpItems)
	}
}
