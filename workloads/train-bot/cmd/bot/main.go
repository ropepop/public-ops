package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/bot"
	"telegramtrainapp/internal/config"
	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/jobs"
	"telegramtrainapp/internal/recovery"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/scrape"
	"telegramtrainapp/internal/spacetime"
	"telegramtrainapp/internal/store"
	"telegramtrainapp/internal/util"
	appversion "telegramtrainapp/internal/version"
	"telegramtrainapp/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	lockPath := resolveSingleInstanceLockPath(cfg)
	lockFile, err := acquireSingleInstanceLock(lockPath)
	if err != nil {
		log.Fatalf("single instance lock: %v", err)
	}
	if lockFile != nil {
		defer releaseSingleInstanceLock(lockFile)
		log.Printf("single-instance lock acquired: %s", lockPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	loc := util.MustLoadLocation(cfg.Timezone)

	runtime, err := openRuntime(cfg, loc)
	if err != nil {
		log.Fatalf("runtime: %v", err)
	}
	st := runtime.store
	schedules := runtime.schedules
	defer func() {
		if err := st.Close(); err != nil {
			log.Printf("store close failed: %v", err)
		}
	}()

	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	if runtime.stateStore != nil {
		if err := runtime.stateStore.PublishRuntimeConfig(ctx, cfg.ScraperDailyHour); err != nil {
			log.Printf("publish spacetime runtime config failed: %v", err)
		}
	}
	if loader, ok := schedules.(interface {
		LoadForAccess(context.Context, time.Time) error
	}); ok {
		if err := loader.LoadForAccess(ctx, time.Now().In(loc)); err != nil {
			log.Printf("initial schedule bootstrap deferred: %v", err)
		}
	}

	reportsSvc := reports.NewService(
		st,
		time.Duration(cfg.CooldownMin)*time.Minute,
		time.Duration(cfg.DedupeSec)*time.Second,
	)
	rides := ride.NewService(st)
	appSvc := trainapp.NewService(st, schedules, rides, reportsSvc, loc, cfg.FeatureStationCheckin)
	if runtime.stateStore != nil {
		result, healErr := recovery.MaybeSelfHeal(ctx, appSvc, runtime.stateStore, time.Now().In(loc))
		switch {
		case healErr == nil && result.Synced:
			log.Printf(
				"spacetime schedule self-heal restored serviceDate=%s stations=%d trains=%d stops=%d",
				result.ServiceDate,
				result.Stations,
				result.Trains,
				result.Stops,
			)
		case healErr != nil && !errors.Is(healErr, schedule.ErrUnavailable):
			log.Printf("spacetime schedule self-heal failed serviceDate=%s: %v", result.ServiceDate, healErr)
		}
	}
	catalog := i18n.NewCatalog()
	client := bot.NewClient(cfg.BotToken, time.Duration(cfg.HTTPTimeoutSec)*time.Second)
	webBaseURL := strings.TrimRight(strings.TrimSpace(cfg.TrainWebPublicBaseURL), "/")
	notifier := bot.NewNotifier(client, st, catalog, loc, webBaseURL, cfg.ReportDumpChatID)

	var scraperJob *scrape.Orchestrator
	timeout := time.Duration(cfg.HTTPTimeoutSec) * time.Second
	if strings.TrimSpace(cfg.ScraperViviPageURL) == "" || strings.TrimSpace(cfg.ScraperViviGTFSURL) == "" {
		log.Printf("vivi scraper URLs are empty; runtime scraper disabled")
	} else {
		scraperOutputDir := strings.TrimSpace(cfg.ScraperOutputDir)
		if scraperOutputDir == "" {
			scraperOutputDir = cfg.ScheduleDir
		}
		providers := []scrape.Provider{
			scrape.NewViviGTFSProvider("vivi_gtfs", cfg.ScraperViviGTFSURL, cfg.ScraperUserAgent, timeout),
			scrape.NewViviPDFProvider("vivi_pdf", cfg.ScraperViviPageURL, cfg.ScraperUserAgent, timeout),
		}
		scraperJob = scrape.NewOrchestrator(providers, scraperOutputDir, cfg.ScraperMinTrains)
	}

	var bundleSync interface {
		PublishActiveBundle(ctx context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error
	}
	bundleSync = st
	bundlePublisher := web.NewStaticBundlePublisher(cfg.TrainWebBundleDir, appSvc, loc, bundleSync)

	jobRunner := jobs.NewRunner(
		st,
		schedules,
		bundlePublisher,
		time.Duration(cfg.DataRetentionHrs)*time.Hour,
		loc,
		scraperJob,
		cfg.ScraperDailyHour,
		cfg.RuntimeSnapshotGCEnabled,
	)

	service := bot.NewService(
		client,
		notifier,
		st,
		schedules,
		rides,
		reportsSvc,
		catalog,
		loc,
		cfg.LongPollTimeout,
		cfg.FeatureStationCheckin,
		webBaseURL,
	)

	var webServer *web.Server
	if cfg.TrainWebEnabled {
		webServer, err = web.NewServer(cfg, appSvc, catalog, loc)
		if err != nil {
			log.Fatalf("train web server: %v", err)
		}
		webServer.SetNotifier(trainWebRideNotifier{
			schedules: schedules,
			notifier:  notifier,
		})
		log.Printf("train web enabled: %s", webServer.AppURL())
	}

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	type componentResult struct {
		name string
		err  error
	}

	results := make(chan componentResult, 4)
	components := 0
	startComponent := func(name string, fn func(context.Context) error) {
		components++
		go func() {
			results <- componentResult{name: name, err: fn(runCtx)}
		}()
	}

	startComponent("jobs", jobRunner.Run)
	startComponent("bot", service.Start)
	startComponent("notifier", notifier.Run)
	if webServer != nil {
		startComponent("train web", webServer.Run)
	}

	log.Printf("train bot started (%s)", appversion.Display())

	var firstErr error
	for components > 0 {
		result := <-results
		components--
		if result.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", result.name, result.err)
			runCancel()
		}
	}
	if firstErr != nil {
		log.Fatalf("train bot stopped with error: %v", firstErr)
	}
}

type trainWebRideNotifier struct {
	schedules schedule.ReadModel
	notifier  trainAlertNotifier
}

type trainAlertNotifier interface {
	DispatchRideAlert(ctx context.Context, payload bot.RideAlertPayload, now time.Time) error
	DispatchStationSighting(ctx context.Context, event domain.StationSighting, now time.Time) error
}

func (n trainWebRideNotifier) NotifyRideUsers(ctx context.Context, reporterID int64, trainID string, signal domain.SignalType, now time.Time) error {
	if n.notifier == nil {
		return nil
	}
	payload := bot.RideAlertPayload{
		TrainID:    trainID,
		Signal:     signal,
		ReportedAt: now,
		ReporterID: reporterID,
	}
	if n.schedules != nil {
		train, err := n.schedules.GetTrain(ctx, trainID)
		if err != nil {
			return err
		}
		if train != nil {
			payload.FromStation = train.FromStation
			payload.ToStation = train.ToStation
			payload.DepartureAt = train.DepartureAt
			payload.ArrivalAt = train.ArrivalAt
		}
	}
	return n.notifier.DispatchRideAlert(ctx, payload, now)
}

func (n trainWebRideNotifier) NotifyStationSighting(ctx context.Context, event domain.StationSighting, now time.Time) error {
	if n.notifier == nil {
		return nil
	}
	return n.notifier.DispatchStationSighting(ctx, event, now)
}

func resolveSingleInstanceLockPath(cfg config.Config) string {
	return strings.TrimSpace(cfg.SingleInstanceLockPath)
}

type runtimeComponents struct {
	store      *store.RoutedStore
	stateStore *store.SpacetimeStore
	schedules  schedule.ReadModel
}

func openRuntime(cfg config.Config, loc *time.Location) (runtimeComponents, error) {
	client, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              cfg.TrainRuntimeSpacetimeHost,
		Database:          cfg.TrainRuntimeSpacetimeDatabase,
		Issuer:            cfg.TrainRuntimeSpacetimeOIDCIssuer,
		Audience:          cfg.TrainRuntimeSpacetimeOIDCAudience,
		JWTPrivateKeyFile: cfg.TrainRuntimeSpacetimeJWTPrivateKeyFile,
		ServiceSubject:    cfg.TrainRuntimeSpacetimeServiceSubject,
		ServiceRoles:      cfg.TrainRuntimeSpacetimeServiceRoles,
		TokenTTL:          time.Duration(cfg.TrainRuntimeSpacetimeTokenTTLSec) * time.Second,
		HTTPTimeout:       time.Duration(cfg.HTTPTimeoutSec) * time.Second,
	})
	if err != nil {
		return runtimeComponents{}, err
	}
	stateStore := store.NewSpacetimeStore(client)
	scheduleCachePath := deriveScheduleCachePath(cfg.ScheduleDir)
	scheduleStore, err := store.NewSQLiteStore(scheduleCachePath)
	if err != nil {
		return runtimeComponents{}, fmt.Errorf("schedule cache sqlite: %w", err)
	}
	routedStore := store.NewRoutedStore(scheduleStore, stateStore)
	readModel := schedule.NewManager(routedStore, cfg.ScheduleDir, loc, cfg.ScraperDailyHour)
	return runtimeComponents{
		store:      routedStore,
		stateStore: stateStore,
		schedules:  readModel,
	}, nil
}

func deriveScheduleCachePath(scheduleDir string) string {
	dir := strings.TrimSpace(scheduleDir)
	if dir == "" {
		return filepath.Clean("train-runtime-cache.db")
	}
	return filepath.Join(filepath.Dir(dir), "train-runtime-cache.db")
}

func acquireSingleInstanceLock(lockPath string) (*os.File, error) {
	if strings.TrimSpace(lockPath) == "" {
		return nil, nil
	}
	dir := filepath.Dir(lockPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create lock dir: %w", err)
		}
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("another train-bot instance is already running")
		}
		return nil, fmt.Errorf("acquire lock: %w", err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	return f, nil
}

func releaseSingleInstanceLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
