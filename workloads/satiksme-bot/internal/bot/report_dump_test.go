package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

func TestFormatVehicleReportDoesNotLeakReporterIdentity(t *testing.T) {
	dispatcher := &DumpDispatcher{loc: time.FixedZone("Riga", 2*60*60)}
	message := dispatcher.formatVehicle(model.VehicleSighting{
		UserID:      123456,
		Mode:        "tram",
		RouteLabel:  "1",
		Direction:   "b-a",
		Destination: "Imanta",
		CreatedAt:   time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
	})
	if strings.Contains(message, "123456") {
		t.Fatalf("report dump leaked reporter id: %s", message)
	}
	if !strings.Contains(message, "tramvajs 1") {
		t.Fatalf("expected mode/route in dump message: %s", message)
	}
	if strings.Contains(message, "Pietura:") {
		t.Fatalf("vehicle dump still mentions a stop: %s", message)
	}
}

func TestPendingCountCachesStoreReads(t *testing.T) {
	st := &dumpStoreStub{pending: 3}
	dispatcher := &DumpDispatcher{store: st}

	first := dispatcher.pendingCount(context.Background())
	second := dispatcher.pendingCount(context.Background())

	if first != 3 || second != 3 {
		t.Fatalf("pendingCount() = %d/%d, want 3/3", first, second)
	}
	if st.pendingCalls != 1 {
		t.Fatalf("PendingReportDumpCount() called %d times, want 1", st.pendingCalls)
	}
}

func TestAdjustPendingUpdatesCachedCountWithoutStoreRefresh(t *testing.T) {
	st := &dumpStoreStub{pending: 2}
	dispatcher := &DumpDispatcher{store: st}

	if pending := dispatcher.pendingCount(context.Background()); pending != 2 {
		t.Fatalf("pendingCount() = %d, want 2", pending)
	}
	if pending := dispatcher.adjustPending(1); pending != 3 {
		t.Fatalf("adjustPending(+1) = %d, want 3", pending)
	}
	if pending := dispatcher.pendingCount(context.Background()); pending != 3 {
		t.Fatalf("pendingCount() after adjust = %d, want 3", pending)
	}
	if st.pendingCalls != 1 {
		t.Fatalf("PendingReportDumpCount() called %d times after adjust, want 1", st.pendingCalls)
	}
}

func TestRefreshPendingCountBypassesCachedCount(t *testing.T) {
	st := &dumpStoreStub{pending: 2}
	dispatcher := &DumpDispatcher{store: st}

	if pending := dispatcher.pendingCount(context.Background()); pending != 2 {
		t.Fatalf("pendingCount() = %d, want 2", pending)
	}
	st.pending = 0
	if pending := dispatcher.refreshPendingCount(context.Background()); pending != 0 {
		t.Fatalf("refreshPendingCount() = %d, want 0", pending)
	}
	if pending := dispatcher.pendingCount(context.Background()); pending != 0 {
		t.Fatalf("pendingCount() after refresh = %d, want 0", pending)
	}
	if st.pendingCalls != 2 {
		t.Fatalf("PendingReportDumpCount() called %d times, want 2", st.pendingCalls)
	}
}

func TestNextWakeDelaySleepsUntilNextAttempt(t *testing.T) {
	now := time.Now().UTC()
	st := &dumpStoreStub{
		peekItem: &store.ReportDumpItem{
			ID:            "dump-1",
			NextAttemptAt: now.Add(250 * time.Millisecond),
		},
	}
	dispatcher := &DumpDispatcher{store: st}

	delay, err := dispatcher.nextWakeDelay(context.Background())
	if err != nil {
		t.Fatalf("nextWakeDelay() error = %v", err)
	}
	if delay < 150*time.Millisecond || delay > 250*time.Millisecond {
		t.Fatalf("nextWakeDelay() = %v, want roughly 150ms-250ms", delay)
	}
}

func TestNextWakeDelayParksWhenQueueIsEmpty(t *testing.T) {
	dispatcher := &DumpDispatcher{store: &dumpStoreStub{}}

	delay, err := dispatcher.nextWakeDelay(context.Background())
	if err != nil {
		t.Fatalf("nextWakeDelay() error = %v", err)
	}
	if delay != noDumpWakeDelay {
		t.Fatalf("nextWakeDelay() = %v, want %v", delay, noDumpWakeDelay)
	}
}

func TestEnqueueSignalsWakeAfterStoreWrite(t *testing.T) {
	st := &dumpStoreStub{}
	dispatcher := &DumpDispatcher{
		store:  st,
		wakeCh: make(chan struct{}, 1),
	}

	dispatcher.enqueue("queued payload", time.Time{})

	if st.enqueueCalls != 1 {
		t.Fatalf("EnqueueReportDump() called %d times, want 1", st.enqueueCalls)
	}
	select {
	case <-dispatcher.wakeChannel():
	default:
		t.Fatal("expected enqueue to signal the dispatcher wake channel")
	}
}

type dumpStoreStub struct {
	pending      int
	pendingCalls int
	enqueueCalls int
	peekItem     *store.ReportDumpItem
}

func (s *dumpStoreStub) Migrate(context.Context) error { return nil }

func (s *dumpStoreStub) HealthCheck(context.Context) error { return nil }

func (s *dumpStoreStub) InsertStopSighting(context.Context, model.StopSighting) error { return nil }

func (s *dumpStoreStub) GetLastStopSightingByUserScope(context.Context, int64, string) (*model.StopSighting, error) {
	return nil, nil
}

func (s *dumpStoreStub) ListStopSightingsSince(context.Context, time.Time, string, int) ([]model.StopSighting, error) {
	return nil, nil
}

func (s *dumpStoreStub) InsertVehicleSighting(context.Context, model.VehicleSighting) error {
	return nil
}

func (s *dumpStoreStub) GetLastVehicleSightingByUserScope(context.Context, int64, string) (*model.VehicleSighting, error) {
	return nil, nil
}

func (s *dumpStoreStub) ListVehicleSightingsSince(context.Context, time.Time, string, int) ([]model.VehicleSighting, error) {
	return nil, nil
}

func (s *dumpStoreStub) UpsertIncidentVote(context.Context, model.IncidentVote) error { return nil }

func (s *dumpStoreStub) RecordIncidentVote(context.Context, model.IncidentVote, model.IncidentVoteEvent) error {
	return nil
}

func (s *dumpStoreStub) ListIncidentVotes(context.Context, string) ([]model.IncidentVote, error) {
	return nil, nil
}

func (s *dumpStoreStub) ListIncidentVoteEvents(context.Context, string, time.Time, int) ([]model.IncidentVoteEvent, error) {
	return nil, nil
}

func (s *dumpStoreStub) CountMapReportsByUserSince(context.Context, int64, time.Time) (int, error) {
	return 0, nil
}

func (s *dumpStoreStub) CountIncidentVoteEventsByUserSince(context.Context, int64, model.IncidentVoteSource, time.Time) (int, error) {
	return 0, nil
}

func (s *dumpStoreStub) InsertIncidentComment(context.Context, model.IncidentComment) error {
	return nil
}

func (s *dumpStoreStub) ListIncidentComments(context.Context, string, int) ([]model.IncidentComment, error) {
	return nil, nil
}

func (s *dumpStoreStub) EnqueueReportDump(context.Context, store.ReportDumpItem) error {
	s.enqueueCalls++
	return nil
}

func (s *dumpStoreStub) NextReportDump(context.Context, time.Time) (*store.ReportDumpItem, error) {
	return nil, nil
}

func (s *dumpStoreStub) PeekNextReportDump(context.Context) (*store.ReportDumpItem, error) {
	return s.peekItem, nil
}

func (s *dumpStoreStub) DeleteReportDump(context.Context, string) error { return nil }

func (s *dumpStoreStub) UpdateReportDumpFailure(context.Context, string, int, time.Time, time.Time, string) error {
	return nil
}

func (s *dumpStoreStub) PendingReportDumpCount(context.Context) (int, error) {
	s.pendingCalls++
	return s.pending, nil
}

func (s *dumpStoreStub) CleanupExpired(context.Context, time.Time) (store.CleanupResult, error) {
	return store.CleanupResult{}, nil
}
