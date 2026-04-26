package jobs

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/scrape"
	"telegramtrainapp/internal/spacetime"
	"telegramtrainapp/internal/store"
)

const (
	dailyRetryInitialBackoff = 15 * time.Minute
	dailyRetryMaxBackoff     = 60 * time.Minute
	cleanupInterval          = 15 * time.Minute
)

type Runner struct {
	store                      store.Store
	readModel                  schedule.ReadModel
	bundlePublisher            BundlePublisher
	retention                  time.Duration
	loc                        *time.Location
	scraper                    *scrape.Orchestrator
	scraperDailyHour           int
	runtimeSnapshotGCEnabled   bool
	lastDailyScrapeAttemptDate string
	nextDailyRetryAt           time.Time
	dailyRetryBackoff          time.Duration
	lastFreshnessWarn          string
	lastCleanupAt              time.Time
}

type BundlePublisher interface {
	Publish(ctx context.Context, now time.Time) error
}

type readModelBootstrapper interface {
	LoadForAccess(ctx context.Context, now time.Time) error
}

func NewRunner(
	st store.Store,
	readModel schedule.ReadModel,
	bundlePublisher BundlePublisher,
	retention time.Duration,
	loc *time.Location,
	scraperJob *scrape.Orchestrator,
	scraperDailyHour int,
	runtimeSnapshotGCEnabled bool,
) *Runner {
	if scraperDailyHour < 0 || scraperDailyHour > 23 {
		scraperDailyHour = 3
	}
	return &Runner{
		store:                    st,
		readModel:                readModel,
		bundlePublisher:          bundlePublisher,
		retention:                retention,
		loc:                      loc,
		scraper:                  scraperJob,
		scraperDailyHour:         scraperDailyHour,
		runtimeSnapshotGCEnabled: runtimeSnapshotGCEnabled,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	now := time.Now()
	date := now.In(r.loc).Format("2006-01-02")
	startupLoaded := false
	r.refreshReadModel(ctx, now)
	if r.scraper != nil {
		var err error
		startupLoaded, err = r.runScrape(ctx, now, "scrape_startup")
		if err != nil {
			return err
		}
	}
	if startupLoaded {
		_ = r.store.UpsertDailyMetric(ctx, date, "schedule_startup_load_success", 1)
	} else {
		available, err := false, error(nil)
		if r.readModel != nil {
			available, err = r.readModel.Availability()
		}
		if err != nil || !available {
			if err != nil {
				log.Printf("schedule startup load failed: %v", err)
			}
			_ = r.store.UpsertDailyMetric(ctx, date, "schedule_startup_load_success", 0)
		} else {
			_ = r.store.UpsertDailyMetric(ctx, date, "schedule_startup_load_success", 1)
			if err := r.publishBundle(ctx, now); err != nil {
				return err
			}
		}
	}
	return r.loop(ctx)
}

func (r *Runner) Start(ctx context.Context) {
	go func() {
		if err := r.Run(ctx); err != nil {
			log.Printf("job runner stopped: %v", err)
		}
	}()
}

func (r *Runner) loop(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if err := r.tick(ctx, now); err != nil {
				return err
			}
		}
	}
}

func (r *Runner) tick(ctx context.Context, now time.Time) error {
	if r.lastCleanupAt.IsZero() || now.Sub(r.lastCleanupAt) >= cleanupInterval {
		r.lastCleanupAt = now
		res, err := r.store.CleanupExpired(ctx, now, r.retention, r.loc)
		if err != nil {
			if isFatalRuntimeSchemaError(err) {
				return err
			}
			log.Printf("cleanup failed: %v", err)
		} else {
			date := now.In(r.loc).Format("2006-01-02")
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_checkins_deleted", res.CheckinsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_subscriptions_deleted", res.SubscriptionsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_reports_deleted", res.ReportsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_train_stops_deleted", res.TrainStopsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_trains_deleted", res.TrainsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_feed_events_deleted", res.FeedEventsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_feed_imports_deleted", res.FeedImportsDeleted)
			_ = r.store.UpsertDailyMetric(ctx, date, "cleanup_import_chunks_deleted", res.ImportChunksDeleted)
		}
	}

	if err := r.ensureDailyScheduleFreshness(ctx, now); err != nil {
		return err
	}

	localNow := now.In(r.loc)
	date := localNow.Format("2006-01-02")
	if localNow.Hour() == 4 && localNow.Minute() == 45 {
		if r.lastFreshnessWarn != date {
			r.lastFreshnessWarn = date
			if r.readModel == nil || !r.readModel.IsFreshFor(now) {
				_ = r.store.UpsertDailyMetric(ctx, date, "schedule_freshness_warning", 1)
			} else {
				_ = r.store.UpsertDailyMetric(ctx, date, "schedule_freshness_warning", 0)
			}
		}
	}
	return nil
}

func (r *Runner) ensureDailyScheduleFreshness(ctx context.Context, now time.Time) error {
	if r.scraper == nil {
		return nil
	}
	if r.readModel != nil && r.readModel.IsFreshFor(now) {
		r.resetDailyRetryState()
		return nil
	}

	localNow := now.In(r.loc)
	serviceDate := localNow.Format("2006-01-02")
	dailyCutoff := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), r.scraperDailyHour, 0, 0, 0, r.loc)
	if localNow.Before(dailyCutoff) {
		return nil
	}

	if serviceDate != r.lastDailyScrapeAttemptDate {
		r.lastDailyScrapeAttemptDate = serviceDate
		ok, err := r.runScrape(ctx, now, "scrape_daily_catchup")
		if err != nil {
			return err
		}
		if ok {
			r.resetDailyRetryState()
		} else {
			r.scheduleNextDailyRetry(now)
		}
		return nil
	}

	if r.nextDailyRetryAt.IsZero() || now.Before(r.nextDailyRetryAt) {
		return nil
	}

	ok, err := r.runScrape(ctx, now, "scrape_daily_catchup")
	if err != nil {
		return err
	}
	if ok {
		r.resetDailyRetryState()
		return nil
	}
	r.scheduleNextDailyRetry(now)
	return nil
}

func (r *Runner) resetDailyRetryState() {
	r.lastDailyScrapeAttemptDate = ""
	r.nextDailyRetryAt = time.Time{}
	r.dailyRetryBackoff = 0
}

func (r *Runner) scheduleNextDailyRetry(now time.Time) {
	if r.dailyRetryBackoff <= 0 {
		r.dailyRetryBackoff = dailyRetryInitialBackoff
	} else {
		r.dailyRetryBackoff *= 2
		if r.dailyRetryBackoff > dailyRetryMaxBackoff {
			r.dailyRetryBackoff = dailyRetryMaxBackoff
		}
	}
	r.nextDailyRetryAt = now.Add(r.dailyRetryBackoff)
}

func (r *Runner) runScrape(ctx context.Context, now time.Time, metricPrefix string) (bool, error) {
	if r.scraper == nil {
		return false, nil
	}
	localNow := now.In(r.loc)
	date := localNow.Format("2006-01-02")
	serviceDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, r.loc)

	result, err := r.scraper.Run(ctx, serviceDate)
	if err != nil {
		log.Printf("%s scrape failed: %v", metricPrefix, err)
		_ = r.store.UpsertDailyMetric(ctx, date, metricPrefix+"_success", 0)
		_ = r.store.UpsertDailyMetric(ctx, date, "scrape_warning", 1)
		return false, nil
	}
	trains, stopsByTrain, err := scrape.SnapshotToDomain(result.Snapshot)
	if err != nil {
		log.Printf("%s snapshot conversion failed: %v", metricPrefix, err)
		_ = r.store.UpsertDailyMetric(ctx, date, metricPrefix+"_success", 0)
		_ = r.store.UpsertDailyMetric(ctx, date, "scrape_warning", 1)
		return false, nil
	}
	if err := r.importTrainData(ctx, date, result.Snapshot.SourceVersion, trains, stopsByTrain); err != nil {
		log.Printf("%s import failed: %v", metricPrefix, err)
		_ = r.store.UpsertDailyMetric(ctx, date, metricPrefix+"_success", 0)
		_ = r.store.UpsertDailyMetric(ctx, date, "scrape_warning", 1)
		return false, nil
	}
	r.refreshReadModel(ctx, now)

	_ = r.store.UpsertDailyMetric(ctx, date, metricPrefix+"_success", 1)
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_warning", 0)
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_providers_tried", int64(result.Stats.ProvidersTried))
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_providers_succeeded", int64(result.Stats.ProvidersSucceeded))
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_trains_merged", int64(result.Stats.TrainsMerged))
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_trains_dropped", int64(result.Stats.TrainsDropped))
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_conflicts_resolved", int64(result.Stats.ConflictsResolved))
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_stops_filled_from_secondary", int64(result.Stats.StopsFilledFromB))
	_ = r.store.UpsertDailyMetric(ctx, date, "scrape_last_success_unix", now.UTC().Unix())

	if strings.TrimSpace(result.OutputPath) != "" {
		log.Printf("%s scrape succeeded (debug snapshot: %s)", metricPrefix, result.OutputPath)
	} else {
		log.Printf("%s scrape succeeded (imported directly into runtime projections)", metricPrefix)
	}
	if err := r.publishBundle(ctx, now); err != nil {
		if isFatalRuntimeSchemaError(err) {
			return false, err
		}
		log.Printf("static bundle publish failed: %v", err)
	}
	if err := r.cleanupPreviousServiceDate(ctx, localNow); err != nil {
		if isFatalRuntimeSchemaError(err) {
			return false, err
		}
		log.Printf("%s cleanup previous service date failed: %v", metricPrefix, err)
	}
	return true, nil
}

func (r *Runner) refreshReadModel(ctx context.Context, now time.Time) {
	loader, ok := r.readModel.(readModelBootstrapper)
	if !ok || loader == nil {
		return
	}
	if err := loader.LoadForAccess(ctx, now); err != nil {
		log.Printf("schedule read model refresh failed: %v", err)
	}
}

func (r *Runner) publishBundle(ctx context.Context, now time.Time) error {
	if r.bundlePublisher == nil {
		return nil
	}
	return r.bundlePublisher.Publish(ctx, now)
}

func (r *Runner) cleanupPreviousServiceDate(ctx context.Context, localNow time.Time) error {
	dailyCutoff := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), r.scraperDailyHour, 0, 0, 0, r.loc)
	if localNow.Before(dailyCutoff) {
		return nil
	}
	serviceDate := localNow.AddDate(0, 0, -1).Format("2006-01-02")
	if serviceDate == "" {
		return nil
	}
	if _, err := r.store.DeleteTrainDataByServiceDate(ctx, serviceDate); err != nil {
		return err
	}
	return nil
}

type trainDataImporter interface {
	ImportTrainData(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance, stopsByTrain map[string][]domain.TrainStop) error
}

func (r *Runner) importTrainData(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance, stopsByTrain map[string][]domain.TrainStop) error {
	if importer, ok := r.store.(trainDataImporter); ok {
		return importer.ImportTrainData(ctx, serviceDate, sourceVersion, trains, stopsByTrain)
	}
	if err := r.store.UpsertTrainInstances(ctx, serviceDate, sourceVersion, trains); err != nil {
		return err
	}
	return r.store.UpsertTrainStops(ctx, serviceDate, stopsByTrain)
}

func isFatalRuntimeSchemaError(err error) bool {
	return errors.Is(err, spacetime.ErrLiveSchemaOutdated)
}
