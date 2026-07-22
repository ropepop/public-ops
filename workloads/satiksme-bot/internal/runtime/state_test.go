package runtime

import (
	"testing"
	"time"
)

func TestChatAnalyzerStatusTracksSanitizedOperationalState(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	state := New(now, true, "127.0.0.1:8080")
	state.ConfigureChatAnalyzer(true, true)
	state.RecordChatAnalyzerCollection(now, 0, 0, "unauthorized", "session_unauthorized")
	status := state.ChatAnalyzerStatus()
	if !status.Enabled || !status.DryRun || status.SessionState != "unauthorized" || status.ConsecutiveCollectErrors != 1 {
		t.Fatalf("collection status = %+v", status)
	}
	state.RecordChatAnalyzerCollection(now.Add(time.Minute), 4, 3, "authorized", "")
	state.RecordChatAnalyzerProcess(now.Add(2*time.Minute), "batch-safe-id", "completed", "gemma-model", "", time.Time{})
	state.RecordChatAnalyzerStaleRecovery(2)
	state.RecordChatAnalyzerExpiredPending(5)
	state.RecordChatAnalyzerMaintenance(now.Add(3*time.Minute), "pending_list_failed")
	status = state.ChatAnalyzerStatus()
	if status.SessionState != "authorized" || status.ConsecutiveCollectErrors != 0 || status.LastCollectedCount != 4 {
		t.Fatalf("recovered collection status = %+v", status)
	}
	if status.LastSkippedStale != 3 || status.SkippedStaleTotal != 3 {
		t.Fatalf("stale collection telemetry = %+v", status)
	}
	if status.LastBatchStatus != "completed" || status.SelectedModel != "gemma-model" || status.StaleBatchesRecovered != 2 {
		t.Fatalf("process status = %+v", status)
	}
	if status.LastExpiredPending != 5 || status.ExpiredPendingTotal != 5 {
		t.Fatalf("expired pending status = %+v", status)
	}
	state.RecordChatAnalyzerExpiredPending(0)
	status = state.ChatAnalyzerStatus()
	if status.LastExpiredPending != 0 || status.ExpiredPendingTotal != 5 {
		t.Fatalf("zero-expiry maintenance status = %+v", status)
	}
	if status.ConsecutiveMaintenanceErrors != 1 || status.LastMaintenanceErrorCode != "pending_list_failed" {
		t.Fatalf("maintenance error status = %+v", status)
	}
	state.RecordChatAnalyzerMaintenance(now.Add(4*time.Minute), "")
	status = state.ChatAnalyzerStatus()
	if status.ConsecutiveMaintenanceErrors != 0 || status.LastMaintenanceErrorCode != "" || status.LastMaintenanceSuccess.IsZero() {
		t.Fatalf("recovered maintenance status = %+v", status)
	}
}
