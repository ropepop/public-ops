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
	if err := st.InsertAreaReport(ctx, model.AreaReport{
		ID:           "area-1",
		UserID:       11,
		Latitude:     56.9501,
		Longitude:    24.1102,
		RadiusMeters: 500,
		Description:  "kontrole starp pieturām",
		ScopeKey:     "56950:24110:500:kontrole-starp-pieturam",
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("InsertAreaReport() error = %v", err)
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
	areaItems, err := st.ListAreaReportsSince(ctx, now.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListAreaReportsSince() error = %v", err)
	}
	if len(areaItems) != 1 || areaItems[0].RadiusMeters != 500 {
		t.Fatalf("areaItems = %+v, want one 500 m report", areaItems)
	}

	result, err := st.CleanupExpired(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	if result.StopSightingsDeleted != 1 || result.VehicleSightingsDeleted != 1 || result.AreaReportsDeleted != 1 {
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

func TestSQLiteStoreChatAnalyzerQueueLifecycle(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	if err := st.SetChatAnalyzerCheckpoint(ctx, "chat:1", 10, now); err != nil {
		t.Fatalf("SetChatAnalyzerCheckpoint() error = %v", err)
	}
	if err := st.SetChatAnalyzerCheckpoint(ctx, "chat:1", 8, now.Add(time.Minute)); err != nil {
		t.Fatalf("SetChatAnalyzerCheckpoint(lower) error = %v", err)
	}
	checkpoint, found, err := st.GetChatAnalyzerCheckpoint(ctx, "chat:1")
	if err != nil {
		t.Fatalf("GetChatAnalyzerCheckpoint() error = %v", err)
	}
	if !found || checkpoint != 10 {
		t.Fatalf("checkpoint = %d found=%v, want 10 true", checkpoint, found)
	}

	item := model.ChatAnalyzerMessage{
		ID:             "chat:1:11",
		ChatID:         "chat:1",
		MessageID:      11,
		SenderID:       777,
		SenderStableID: "telegram:777",
		SenderNickname: "Amber Scout 111",
		Text:           "kontrole pie pieturas",
		MessageDate:    now,
		ReceivedAt:     now,
		Status:         model.ChatAnalyzerMessagePending,
	}
	inserted, err := st.EnqueueChatAnalyzerMessage(ctx, item)
	if err != nil || !inserted {
		t.Fatalf("EnqueueChatAnalyzerMessage() inserted=%v err=%v, want true nil", inserted, err)
	}
	inserted, err = st.EnqueueChatAnalyzerMessage(ctx, item)
	if err != nil || inserted {
		t.Fatalf("duplicate EnqueueChatAnalyzerMessage() inserted=%v err=%v, want false nil", inserted, err)
	}
	count, err := st.CountChatAnalyzerMessagesBySenderSince(ctx, "chat:1", 777, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CountChatAnalyzerMessagesBySenderSince() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("sender count = %d, want 1", count)
	}
	pending, err := st.ListPendingChatAnalyzerMessages(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingChatAnalyzerMessages() error = %v", err)
	}
	if len(pending) != 1 || pending[0].ID != item.ID || pending[0].Text != item.Text {
		t.Fatalf("pending = %+v, want queued message", pending)
	}
	batch := model.ChatAnalyzerBatch{
		ID:            "batch-1",
		Status:        model.ChatAnalyzerBatchCompleted,
		DryRun:        true,
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
		MessageCount:  1,
		ReportCount:   1,
		WouldApply:    1,
		Model:         "openrouter/free",
		SelectedModel: "qwen/free-picked",
		ResultJSON:    `{"reports":[],"votes":[],"ignored":[]}`,
	}
	if err := st.SaveChatAnalyzerBatch(ctx, batch); err != nil {
		t.Fatalf("SaveChatAnalyzerBatch() error = %v", err)
	}
	if err := st.MarkChatAnalyzerMessageProcessedInBatch(ctx, item.ID, model.ChatAnalyzerMessageApplied, `{"action":"sighting"}`, "stop-1", "sighting:stop:3012", batch.ID, "", now.Add(time.Second)); err != nil {
		t.Fatalf("MarkChatAnalyzerMessageProcessedInBatch() error = %v", err)
	}
	var savedBatchID string
	if err := st.db.QueryRowContext(ctx, `SELECT batch_id FROM chat_analyzer_messages WHERE id = ?`, item.ID).Scan(&savedBatchID); err != nil {
		t.Fatalf("query message batch id: %v", err)
	}
	if savedBatchID != batch.ID {
		t.Fatalf("batch_id = %q, want %q", savedBatchID, batch.ID)
	}
	var selectedModel string
	if err := st.db.QueryRowContext(ctx, `SELECT selected_model FROM chat_analyzer_batches WHERE id = ?`, batch.ID).Scan(&selectedModel); err != nil {
		t.Fatalf("query batch audit: %v", err)
	}
	if selectedModel != batch.SelectedModel {
		t.Fatalf("selected model = %q, want %q", selectedModel, batch.SelectedModel)
	}
	pending, err = st.ListPendingChatAnalyzerMessages(ctx, 10)
	if err != nil {
		t.Fatalf("ListPendingChatAnalyzerMessages(after mark) error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending after mark = %d, want 0", len(pending))
	}
	applied, err := st.CountChatAnalyzerAppliedByTargetSince(ctx, "sighting:stop:3012", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CountChatAnalyzerAppliedByTargetSince() error = %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied count = %d, want 1", applied)
	}
	if _, err := st.CleanupExpired(ctx, now.Add(time.Hour)); err != nil {
		t.Fatalf("CleanupExpired() error = %v", err)
	}
	applied, err = st.CountChatAnalyzerAppliedByTargetSince(ctx, "sighting:stop:3012", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CountChatAnalyzerAppliedByTargetSince(after cleanup) error = %v", err)
	}
	if applied != 0 {
		t.Fatalf("applied count after cleanup = %d, want 0", applied)
	}
}

func TestSQLiteFailStaleChatAnalyzerBatchesOnlyClosesOldRunningRows(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for _, batch := range []model.ChatAnalyzerBatch{
		{ID: "stale", Status: model.ChatAnalyzerBatchRunning, StartedAt: now.Add(-time.Hour), MessageCount: 3},
		{ID: "fresh", Status: model.ChatAnalyzerBatchRunning, StartedAt: now.Add(-time.Minute), MessageCount: 2},
		{ID: "complete", Status: model.ChatAnalyzerBatchCompleted, StartedAt: now.Add(-time.Hour), FinishedAt: now.Add(-50 * time.Minute), MessageCount: 1},
	} {
		if err := st.SaveChatAnalyzerBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := st.FailStaleChatAnalyzerBatches(ctx, now.Add(-15*time.Minute), now, "interrupted before completion")
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	var status, finishedAt, lastError string
	var errorCount int
	if err := st.db.QueryRowContext(ctx, `SELECT status, finished_at, error_count, last_error FROM chat_analyzer_batches WHERE id = 'stale'`).Scan(&status, &finishedAt, &errorCount, &lastError); err != nil {
		t.Fatal(err)
	}
	if status != string(model.ChatAnalyzerBatchFailed) || finishedAt == "" || errorCount != 3 || lastError != "interrupted before completion" {
		t.Fatalf("stale batch = status=%q finished=%q errors=%d lastError=%q", status, finishedAt, errorCount, lastError)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM chat_analyzer_batches WHERE id = 'fresh'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(model.ChatAnalyzerBatchRunning) {
		t.Fatalf("fresh status = %q, want running", status)
	}
}

func TestSQLiteExpireStaleChatAnalyzerMessagesOnlyClosesOldPendingRows(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for _, item := range []model.ChatAnalyzerMessage{
		{ID: "stale", ChatID: "chat:1", MessageID: 1, Text: "old", MessageDate: now.Add(-48 * time.Hour), ReceivedAt: now.Add(-48 * time.Hour), Status: model.ChatAnalyzerMessagePending},
		{ID: "fresh", ChatID: "chat:1", MessageID: 2, Text: "new", MessageDate: now.Add(-time.Hour), ReceivedAt: now.Add(-time.Hour), Status: model.ChatAnalyzerMessagePending},
		{ID: "done", ChatID: "chat:1", MessageID: 3, Text: "old done", MessageDate: now.Add(-48 * time.Hour), ReceivedAt: now.Add(-48 * time.Hour), Status: model.ChatAnalyzerMessageIgnored},
	} {
		if _, err := st.EnqueueChatAnalyzerMessage(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := st.ExpireStaleChatAnalyzerMessages(ctx, now.Add(-24*time.Hour), now, `{"kind":"ignored"}`)
	if err != nil {
		t.Fatal(err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}
	pending, err := st.ListPendingChatAnalyzerMessages(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "fresh" {
		t.Fatalf("pending = %+v, want only fresh", pending)
	}
	var status, analysisJSON string
	if err := st.db.QueryRowContext(ctx, `SELECT status, analysis_json FROM chat_analyzer_messages WHERE id = 'stale'`).Scan(&status, &analysisJSON); err != nil {
		t.Fatal(err)
	}
	if status != string(model.ChatAnalyzerMessageIgnored) || analysisJSON != `{"kind":"ignored"}` {
		t.Fatalf("stale row = status=%q analysis=%q", status, analysisJSON)
	}
}

func TestSQLiteFinalizeChatAnalyzerBatchIsAtomic(t *testing.T) {
	ctx := context.Background()
	st, err := NewSQLiteStore(filepath.Join(t.TempDir(), "satiksme.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	item := model.ChatAnalyzerMessage{ID: "message-1", ChatID: "chat:1", MessageID: 1, Text: "test", MessageDate: now, ReceivedAt: now, Status: model.ChatAnalyzerMessagePending}
	if _, err := st.EnqueueChatAnalyzerMessage(ctx, item); err != nil {
		t.Fatal(err)
	}
	batch := model.ChatAnalyzerBatch{ID: "batch-1", Status: model.ChatAnalyzerBatchCompleted, StartedAt: now, FinishedAt: now.Add(time.Minute), MessageCount: 2}
	outcomes := []ChatAnalyzerMessageOutcome{
		{ID: item.ID, Status: model.ChatAnalyzerMessageIgnored, BatchID: batch.ID, ProcessedAt: batch.FinishedAt},
		{ID: "missing", Status: model.ChatAnalyzerMessageIgnored, BatchID: batch.ID, ProcessedAt: batch.FinishedAt},
	}
	if err := st.FinalizeChatAnalyzerBatch(ctx, batch, outcomes); err == nil {
		t.Fatal("FinalizeChatAnalyzerBatch() error = nil, want missing-message rollback")
	}
	var status string
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM chat_analyzer_messages WHERE id = ?`, item.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(model.ChatAnalyzerMessagePending) {
		t.Fatalf("message status after rollback = %q, want pending", status)
	}
	var batches int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat_analyzer_batches WHERE id = ?`, batch.ID).Scan(&batches); err != nil {
		t.Fatal(err)
	}
	if batches != 0 {
		t.Fatalf("batch count after rollback = %d, want 0", batches)
	}

	batch.MessageCount = 1
	if err := st.FinalizeChatAnalyzerBatch(ctx, batch, outcomes[:1]); err != nil {
		t.Fatal(err)
	}
	var batchStatus, batchID string
	if err := st.db.QueryRowContext(ctx, `SELECT status, batch_id FROM chat_analyzer_messages WHERE id = ?`, item.ID).Scan(&status, &batchID); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT status FROM chat_analyzer_batches WHERE id = ?`, batch.ID).Scan(&batchStatus); err != nil {
		t.Fatal(err)
	}
	if status != string(model.ChatAnalyzerMessageIgnored) || batchID != batch.ID || batchStatus != string(model.ChatAnalyzerBatchCompleted) {
		t.Fatalf("final state = message %q batchId %q batch %q", status, batchID, batchStatus)
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
	if err := st.InsertAreaReport(ctx, model.AreaReport{
		ID:           "area-1",
		UserID:       42,
		Latitude:     56.95012,
		Longitude:    24.11034,
		RadiusMeters: 500,
		Description:  "starp pieturām pie tuneļa",
		ScopeKey:     "56950:24110:500:starp-pieturam-pie-tunela",
		CreatedAt:    now.Add(45 * time.Second),
	}); err != nil {
		t.Fatalf("InsertAreaReport() error = %v", err)
	}
	if err := st.RecordIncidentVote(ctx, model.IncidentVote{
		IncidentID: "stop:3012",
		UserID:     42,
		Nickname:   "Amber Scout 101",
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  now.Add(time.Minute),
		UpdatedAt:  now.Add(2 * time.Minute),
	}, model.IncidentVoteEvent{
		ID:         "event-1",
		IncidentID: "stop:3012",
		UserID:     42,
		Nickname:   "Amber Scout 101",
		Value:      model.IncidentVoteOngoing,
		Source:     model.IncidentVoteSourceMapReport,
		CreatedAt:  now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("RecordIncidentVote() error = %v", err)
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
	if err := st.InsertStopSighting(ctx, model.StopSighting{
		ID:        "bad-stop",
		StopID:    "3012",
		UserID:    0,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("InsertStopSighting(bad) error = %v", err)
	}
	if err := st.InsertVehicleSighting(ctx, model.VehicleSighting{
		ID:               "bad-veh",
		StopID:           "3012",
		UserID:           0,
		Mode:             "bus",
		RouteLabel:       "22",
		Direction:        "a-b",
		Destination:      "Lidosta",
		DepartureSeconds: 600,
		LiveRowID:        "bad-live",
		ScopeKey:         "live:bus:22:a-b:bad-live",
		CreatedAt:        now,
	}); err != nil {
		t.Fatalf("InsertVehicleSighting(bad) error = %v", err)
	}
	if err := st.InsertAreaReport(ctx, model.AreaReport{
		ID:           "bad-area",
		UserID:       0,
		Latitude:     56.95012,
		Longitude:    24.11034,
		RadiusMeters: 500,
		Description:  "bad",
		ScopeKey:     "bad-area",
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("InsertAreaReport(bad) error = %v", err)
	}
	if err := st.RecordIncidentVote(ctx, model.IncidentVote{
		IncidentID: "stop:bad",
		UserID:     0,
		Nickname:   "Bad Identity",
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, model.IncidentVoteEvent{
		ID:         "bad-event",
		IncidentID: "stop:bad",
		UserID:     0,
		Nickname:   "Bad Identity",
		Value:      model.IncidentVoteOngoing,
		Source:     model.IncidentVoteSourceMapReport,
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("RecordIncidentVote(bad) error = %v", err)
	}
	if err := st.InsertIncidentComment(ctx, model.IncidentComment{
		ID:         "bad-comment",
		IncidentID: "stop:bad",
		UserID:     0,
		Nickname:   "Bad Identity",
		Body:       "bad",
		CreatedAt:  now,
	}); err != nil {
		t.Fatalf("InsertIncidentComment(bad) error = %v", err)
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
	if len(snapshot.AreaReports) != 1 || snapshot.AreaReports[0].Description != "starp pieturām pie tuneļa" {
		t.Fatalf("snapshot.AreaReports = %+v", snapshot.AreaReports)
	}
	if len(snapshot.IncidentVotes) != 1 || snapshot.IncidentVotes[0].Value != model.IncidentVoteOngoing {
		t.Fatalf("snapshot.IncidentVotes = %+v", snapshot.IncidentVotes)
	}
	if len(snapshot.IncidentVoteEvents) != 1 || snapshot.IncidentVoteEvents[0].ID != "event-1" {
		t.Fatalf("snapshot.IncidentVoteEvents = %+v", snapshot.IncidentVoteEvents)
	}
	if len(snapshot.IncidentComments) != 1 || snapshot.IncidentComments[0].Body != "Kontrole pie tirgus" {
		t.Fatalf("snapshot.IncidentComments = %+v", snapshot.IncidentComments)
	}
	if len(snapshot.ReportDumpItems) != 1 || snapshot.ReportDumpItems[0].LastError != "telegram timeout" {
		t.Fatalf("snapshot.ReportDumpItems = %+v", snapshot.ReportDumpItems)
	}
}
