package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/scrape"
	"telegramtrainapp/internal/spacetime"
	"telegramtrainapp/internal/store"
)

type countingStore struct {
	*store.SQLiteStore
	upsertTrainInstancesCalls int
	upsertTrainStopsCalls     int
}

func (s *countingStore) UpsertTrainInstances(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance) error {
	s.upsertTrainInstancesCalls++
	return s.SQLiteStore.UpsertTrainInstances(ctx, serviceDate, sourceVersion, trains)
}

func (s *countingStore) UpsertTrainStops(ctx context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error {
	s.upsertTrainStopsCalls++
	return s.SQLiteStore.UpsertTrainStops(ctx, serviceDate, stopsByTrain)
}

type cleanupFailingStore struct {
	*store.SQLiteStore
	cleanupCalls int
	cleanupErr   error
}

func (s *cleanupFailingStore) CleanupExpired(ctx context.Context, now time.Time, retention time.Duration, loc *time.Location) (store.CleanupResult, error) {
	s.cleanupCalls++
	return store.CleanupResult{}, s.cleanupErr
}

type scriptedProvider struct {
	calls    int
	outcomes []error
}

func (p *scriptedProvider) Name() string {
	return "scripted"
}

func (p *scriptedProvider) Fetch(_ context.Context, serviceDate time.Time) (scrape.RawSchedule, error) {
	p.calls++
	if len(p.outcomes) > 0 {
		err := p.outcomes[0]
		p.outcomes = p.outcomes[1:]
		if err != nil {
			return scrape.RawSchedule{}, err
		}
	}

	departure := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 8, 0, 0, 0, serviceDate.Location())
	arrival := departure.Add(45 * time.Minute)
	serviceDateText := serviceDate.In(serviceDate.Location()).Format("2006-01-02")

	return scrape.RawSchedule{
		SourceName: "scripted",
		FetchedAt:  serviceDate.UTC(),
		Trains: []scrape.RawTrain{
			{
				ID:          "scripted-train",
				TrainNumber: "1",
				ServiceDate: serviceDateText,
				FromStation: "Riga",
				ToStation:   "Jelgava",
				DepartureAt: departure,
				ArrivalAt:   arrival,
				Stops: []scrape.RawStop{
					{
						StationName: "Riga",
						Seq:         1,
						DepartureAt: &departure,
					},
					{
						StationName: "Jelgava",
						Seq:         2,
						ArrivalAt:   &arrival,
					},
				},
			},
		},
	}, nil
}

func TestRunnerTriggersDailyCatchupAfterCutoff(t *testing.T) {
	ctx := context.Background()
	_, _, _, runner, reader, provider := setupRunnerFixture(t, nil)
	now := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 3, 5)

	runner.tick(ctx, now)

	if provider.calls != 1 {
		t.Fatalf("expected 1 scrape call, got %d", provider.calls)
	}
	if !reader.IsFreshFor(now) {
		t.Fatalf("expected read model to be fresh after catch-up scrape")
	}
	if got := reader.AccessContext(now).LoadedServiceDate; got != now.Format("2006-01-02") {
		t.Fatalf("expected loaded service date %s, got %s", now.Format("2006-01-02"), got)
	}
	if runner.lastDailyScrapeAttemptDate != "" {
		t.Fatalf("expected attempt state to reset after success, got %q", runner.lastDailyScrapeAttemptDate)
	}
	if !runner.nextDailyRetryAt.IsZero() {
		t.Fatalf("expected retry deadline to reset after success, got %s", runner.nextDailyRetryAt)
	}
	if runner.dailyRetryBackoff != 0 {
		t.Fatalf("expected retry backoff to reset after success, got %s", runner.dailyRetryBackoff)
	}
	if got := reader.AccessContext(now).LoadedServiceDate; got != now.Format("2006-01-02") {
		t.Fatalf("expected loaded service date %s, got %s", now.Format("2006-01-02"), got)
	}
	staleTrain, err := reader.GetTrain(ctx, "t1")
	if err != nil {
		t.Fatalf("get stale train: %v", err)
	}
	if staleTrain != nil {
		t.Fatalf("expected yesterday fallback train to be garbage collected after successful catch-up")
	}
}

func TestRunnerRetriesDailyCatchupAfterFailure(t *testing.T) {
	ctx := context.Background()
	_, _, _, runner, reader, provider := setupRunnerFixture(t, []error{errors.New("boom"), nil})
	firstAttempt := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 3, 5)
	beforeRetry := firstAttempt.Add(14 * time.Minute)
	retryAt := firstAttempt.Add(15 * time.Minute)
	serviceDate := firstAttempt.Format("2006-01-02")

	runner.tick(ctx, firstAttempt)

	if provider.calls != 1 {
		t.Fatalf("expected 1 scrape call after initial failure, got %d", provider.calls)
	}
	if reader.IsFreshFor(firstAttempt) {
		t.Fatalf("expected read model to remain stale after failed catch-up")
	}
	if runner.lastDailyScrapeAttemptDate != serviceDate {
		t.Fatalf("expected attempt date %s, got %s", serviceDate, runner.lastDailyScrapeAttemptDate)
	}
	if !runner.nextDailyRetryAt.Equal(retryAt) {
		t.Fatalf("expected next retry at %s, got %s", retryAt, runner.nextDailyRetryAt)
	}
	if runner.dailyRetryBackoff != 15*time.Minute {
		t.Fatalf("expected 15m retry backoff, got %s", runner.dailyRetryBackoff)
	}

	runner.tick(ctx, beforeRetry)
	if provider.calls != 1 {
		t.Fatalf("expected no retry before backoff deadline, got %d calls", provider.calls)
	}

	runner.tick(ctx, retryAt)
	if provider.calls != 2 {
		t.Fatalf("expected retry at backoff deadline, got %d calls", provider.calls)
	}
	if !reader.IsFreshFor(retryAt) {
		t.Fatalf("expected read model to be fresh after successful retry")
	}
	if runner.lastDailyScrapeAttemptDate != "" {
		t.Fatalf("expected attempt state to reset after retry success, got %q", runner.lastDailyScrapeAttemptDate)
	}
	if !runner.nextDailyRetryAt.IsZero() {
		t.Fatalf("expected retry deadline to reset after retry success, got %s", runner.nextDailyRetryAt)
	}
	if runner.dailyRetryBackoff != 0 {
		t.Fatalf("expected retry backoff to reset after retry success, got %s", runner.dailyRetryBackoff)
	}
}

func TestRunnerCapsDailyRetryBackoffAtSixtyMinutes(t *testing.T) {
	ctx := context.Background()
	_, _, _, runner, _, provider := setupRunnerFixture(t, []error{
		errors.New("boom-1"),
		errors.New("boom-2"),
		errors.New("boom-3"),
		errors.New("boom-4"),
	})
	firstAttempt := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 3, 5)
	secondAttempt := firstAttempt.Add(15 * time.Minute)
	thirdAttempt := secondAttempt.Add(30 * time.Minute)
	fourthAttempt := thirdAttempt.Add(60 * time.Minute)

	runner.tick(ctx, firstAttempt)
	if runner.dailyRetryBackoff != 15*time.Minute || !runner.nextDailyRetryAt.Equal(secondAttempt) {
		t.Fatalf("expected first retry backoff 15m at %s, got backoff=%s retry=%s", secondAttempt, runner.dailyRetryBackoff, runner.nextDailyRetryAt)
	}

	runner.tick(ctx, secondAttempt)
	if runner.dailyRetryBackoff != 30*time.Minute || !runner.nextDailyRetryAt.Equal(thirdAttempt) {
		t.Fatalf("expected second retry backoff 30m at %s, got backoff=%s retry=%s", thirdAttempt, runner.dailyRetryBackoff, runner.nextDailyRetryAt)
	}

	runner.tick(ctx, thirdAttempt)
	if runner.dailyRetryBackoff != 60*time.Minute || !runner.nextDailyRetryAt.Equal(fourthAttempt) {
		t.Fatalf("expected third retry backoff 60m at %s, got backoff=%s retry=%s", fourthAttempt, runner.dailyRetryBackoff, runner.nextDailyRetryAt)
	}

	nextExpected := fourthAttempt.Add(60 * time.Minute)
	runner.tick(ctx, fourthAttempt)
	if provider.calls != 4 {
		t.Fatalf("expected 4 scrape attempts, got %d", provider.calls)
	}
	if runner.dailyRetryBackoff != 60*time.Minute {
		t.Fatalf("expected backoff cap to remain 60m, got %s", runner.dailyRetryBackoff)
	}
	if !runner.nextDailyRetryAt.Equal(nextExpected) {
		t.Fatalf("expected capped retry deadline %s, got %s", nextExpected, runner.nextDailyRetryAt)
	}
}

func TestRunnerDoesNotRescrapeWhileScheduleIsFresh(t *testing.T) {
	ctx := context.Background()
	_, _, _, runner, reader, provider := setupRunnerFixture(t, nil)
	firstAttempt := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 3, 5)
	laterSameDay := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 4, 5)

	runner.tick(ctx, firstAttempt)
	if provider.calls != 1 {
		t.Fatalf("expected initial catch-up scrape, got %d calls", provider.calls)
	}
	if !reader.IsFreshFor(laterSameDay) {
		t.Fatalf("expected read model to stay fresh later the same day")
	}

	runner.tick(ctx, laterSameDay)
	if provider.calls != 1 {
		t.Fatalf("expected no extra scrape while fresh, got %d calls", provider.calls)
	}
}

func TestRunnerPublishesTrainStopsCleanupMetric(t *testing.T) {
	ctx := context.Background()
	dbPath, _, _, runner, _, provider := setupRunnerFixture(t, nil)
	now := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 2, 30)

	runner.tick(ctx, now)

	if provider.calls != 0 {
		t.Fatalf("expected no scrape before daily cutoff, got %d calls", provider.calls)
	}
	if got := queryDailyMetric(t, dbPath, now.Format("2006-01-02"), "cleanup_train_stops_deleted"); got != 0 {
		t.Fatalf("expected cleanup_train_stops_deleted=0 before successful same-day load, got %d", got)
	}
	if got := queryDailyMetric(t, dbPath, now.Format("2006-01-02"), "cleanup_feed_events_deleted"); got != 0 {
		t.Fatalf("expected cleanup_feed_events_deleted=0 before successful same-day load, got %d", got)
	}
	if got := queryDailyMetric(t, dbPath, now.Format("2006-01-02"), "cleanup_feed_imports_deleted"); got != 0 {
		t.Fatalf("expected cleanup_feed_imports_deleted=0 before successful same-day load, got %d", got)
	}
	if got := queryDailyMetric(t, dbPath, now.Format("2006-01-02"), "cleanup_import_chunks_deleted"); got != 0 {
		t.Fatalf("expected cleanup_import_chunks_deleted=0 before successful same-day load, got %d", got)
	}
}

func TestRunnerBacksOffCleanupAfterFailure(t *testing.T) {
	ctx := context.Background()
	_, baseStore, _, _, reader, _ := setupRunnerFixture(t, nil)
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	st := &cleanupFailingStore{
		SQLiteStore: baseStore,
		cleanupErr:  errors.New("boom"),
	}
	runner := NewRunner(st, reader, nil, 24*time.Hour, loc, nil, 3, true)
	firstTick := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 2, 30)

	runner.tick(ctx, firstTick)
	if st.cleanupCalls != 1 {
		t.Fatalf("expected 1 cleanup attempt after first failure, got %d", st.cleanupCalls)
	}

	runner.tick(ctx, firstTick.Add(time.Minute))
	if st.cleanupCalls != 1 {
		t.Fatalf("expected cleanup failure to respect cleanup interval backoff, got %d calls", st.cleanupCalls)
	}
}

func TestRunnerReturnsFatalSchemaErrorForCleanup(t *testing.T) {
	ctx := context.Background()
	_, baseStore, _, _, reader, _ := setupRunnerFixture(t, nil)
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	st := &cleanupFailingStore{
		SQLiteStore: baseStore,
		cleanupErr:  spacetime.ErrLiveSchemaOutdated,
	}
	runner := NewRunner(st, reader, nil, 24*time.Hour, loc, nil, 3, true)
	firstTick := mustLoadLocationTime(t, "Europe/Riga", 2026, 2, 28, 2, 30)

	err = runner.tick(ctx, firstTick)
	if st.cleanupCalls != 1 {
		t.Fatalf("expected first cleanup attempt, got %d", st.cleanupCalls)
	}
	if !errors.Is(err, spacetime.ErrLiveSchemaOutdated) {
		t.Fatalf("expected live schema error, got %v", err)
	}
}

func TestRunnerRunSuccessfulStartupScrapeSkipsFollowupLoad(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	baseStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "runner-start.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := baseStore.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = baseStore.Close()
	})

	counting := &countingStore{SQLiteStore: baseStore}
	scheduleDir := t.TempDir()
	provider := &scriptedProvider{}
	orchestrator := scrape.NewOrchestrator([]scrape.Provider{provider}, scheduleDir, 1)
	reader := schedule.NewProjectionReader(counting, loc, 3)
	runner := NewRunner(counting, reader, nil, 24*time.Hour, loc, orchestrator, 3, true)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	expectedDate := time.Now().In(loc).Format("2006-01-02")
	go func() {
		errCh <- runner.Run(ctx)
	}()
	waitForRunnerStartup(t, func() bool {
		return provider.calls == 1
	})
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("runner run: %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("expected one startup scrape, got %d", provider.calls)
	}
	if counting.upsertTrainInstancesCalls != 1 {
		t.Fatalf("expected one train instance import, got %d", counting.upsertTrainInstancesCalls)
	}
	if counting.upsertTrainStopsCalls != 1 {
		t.Fatalf("expected one train stop import, got %d", counting.upsertTrainStopsCalls)
	}
	if got := reader.LoadedServiceDate(); got != expectedDate {
		t.Fatalf("expected loaded service date %s, got %s", expectedDate, got)
	}
}

func TestRunnerRunDoesNotFallbackToSnapshotLoadAfterStartupScrapeFailure(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	baseStore, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "runner-start-fallback.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := baseStore.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = baseStore.Close()
	})

	counting := &countingStore{SQLiteStore: baseStore}
	scheduleDir := t.TempDir()
	now := time.Now().In(loc)
	writeSnapshotFile(t, scheduleDir, now)
	provider := &scriptedProvider{outcomes: []error{errors.New("startup scrape failed")}}
	orchestrator := scrape.NewOrchestrator([]scrape.Provider{provider}, scheduleDir, 1)
	reader := schedule.NewProjectionReader(counting, loc, 3)
	runner := NewRunner(counting, reader, nil, 24*time.Hour, loc, orchestrator, 3, true)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- runner.Run(ctx)
	}()
	waitForRunnerStartup(t, func() bool {
		return provider.calls == 1
	})
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("runner run: %v", err)
	}

	if provider.calls != 1 {
		t.Fatalf("expected one failed startup scrape, got %d", provider.calls)
	}
	if counting.upsertTrainInstancesCalls != 0 {
		t.Fatalf("expected no fallback train import from snapshot file, got %d", counting.upsertTrainInstancesCalls)
	}
	if counting.upsertTrainStopsCalls != 0 {
		t.Fatalf("expected no fallback stop import from snapshot file, got %d", counting.upsertTrainStopsCalls)
	}
	if got := reader.LoadedServiceDate(); got != "" {
		t.Fatalf("expected no loaded service date after failed startup scrape, got %s", got)
	}
}

func setupRunnerFixture(t *testing.T, outcomes []error) (string, *store.SQLiteStore, string, *Runner, *schedule.ProjectionReader, *scriptedProvider) {
	t.Helper()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "runner.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	scheduleDir := t.TempDir()
	yesterday := time.Date(2026, 2, 27, 9, 0, 0, 0, loc)
	writeSnapshotFile(t, scheduleDir, yesterday)

	manager := schedule.NewManager(st, scheduleDir, loc, 3)
	if err := manager.LoadToday(context.Background(), yesterday); err != nil {
		t.Fatalf("load yesterday snapshot: %v", err)
	}
	reader := schedule.NewProjectionReader(st, loc, 3)

	provider := &scriptedProvider{outcomes: append([]error(nil), outcomes...)}
	orchestrator := scrape.NewOrchestrator([]scrape.Provider{provider}, scheduleDir, 1)
	runner := NewRunner(st, reader, nil, 24*time.Hour, loc, orchestrator, 3, true)

	return dbPath, st, scheduleDir, runner, reader, provider
}

func writeSnapshotFile(t *testing.T, dir string, now time.Time) {
	t.Helper()

	serviceDate := now.Format("2006-01-02")
	content := fmt.Sprintf(`{
  "source_version":"snapshot-test",
  "trains":[
    {
      "id":"t1",
      "service_date":"%s",
      "from_station":"Riga",
      "to_station":"Jelgava",
      "departure_at":"%s",
      "arrival_at":"%s",
      "stops":[
        {"station_name":"Riga","seq":1,"departure_at":"%s"},
        {"station_name":"Jelgava","seq":2,"arrival_at":"%s"}
      ]
    }
  ]
}`,
		serviceDate,
		now.Add(-30*time.Minute).Format(time.RFC3339),
		now.Add(15*time.Minute).Format(time.RFC3339),
		now.Add(-30*time.Minute).Format(time.RFC3339),
		now.Add(15*time.Minute).Format(time.RFC3339),
	)

	path := filepath.Join(dir, serviceDate+".json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

func mustLoadLocationTime(t *testing.T, timezone string, year int, month time.Month, day int, hour int, minute int) time.Time {
	t.Helper()

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return time.Date(year, month, day, hour, minute, 0, 0, loc)
}

func queryDailyMetric(t *testing.T, dbPath string, metricDate string, key string) int64 {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	var value int64
	if err := db.QueryRow(`
		SELECT value
		FROM daily_metrics
		WHERE metric_date = ? AND key = ?
	`, metricDate, key).Scan(&value); err != nil {
		t.Fatalf("query daily metric %s/%s: %v", metricDate, key, err)
	}
	return value
}

func waitForRunnerStartup(t *testing.T, ready func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for runner startup")
}
