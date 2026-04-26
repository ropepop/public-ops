package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"satiksmebot/internal/bot"
	"satiksmebot/internal/catalog"
	"satiksmebot/internal/config"
	"satiksmebot/internal/live"
	"satiksmebot/internal/reports"
	"satiksmebot/internal/runtime"
	"satiksmebot/internal/spacetime"
	"satiksmebot/internal/store"
	"satiksmebot/internal/telegram"
	"satiksmebot/internal/version"
	"satiksmebot/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	runtimeState := runtime.New(
		time.Now().UTC(),
		cfg.SatiksmeWebEnabled,
		net.JoinHostPort(cfg.SatiksmeWebBindAddr, fmt.Sprintf("%d", cfg.SatiksmeWebPort)),
	)

	lockPath := resolveSingleInstanceLockPath(cfg)
	lockFile, err := acquireSingleInstanceLock(lockPath)
	if err != nil {
		fatalf(runtimeState, "single instance lock: %v", err)
	}
	if lockFile != nil {
		defer releaseSingleInstanceLock(lockFile)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		fatalf(runtimeState, "load timezone: %v", err)
	}
	if err := verifyConfiguredSpacetimeSchema(ctx, cfg, spacetime.VerifyExpectedSchema); err != nil {
		fatalf(runtimeState, "spacetime schema preflight: %v", err)
	}

	var (
		st          store.Store
		sqliteStore *store.SQLiteStore
		syncer      *spacetime.Syncer
	)
	if cfg.SatiksmeRuntimeSpacetimeEnabled {
		syncer, err = spacetime.NewSyncer(spacetime.SyncConfig{
			Host:              cfg.SatiksmeRuntimeSpacetimeHost,
			Database:          cfg.SatiksmeRuntimeSpacetimeDatabase,
			Issuer:            cfg.SatiksmeRuntimeSpacetimeOIDCIssuer,
			Audience:          cfg.SatiksmeRuntimeSpacetimeOIDCAudience,
			JWTPrivateKeyFile: cfg.SatiksmeRuntimeSpacetimeJWTPrivateKeyFile,
			ServiceSubject:    cfg.SatiksmeRuntimeSpacetimeServiceSubject,
			ServiceRoles:      cfg.SatiksmeRuntimeSpacetimeServiceRoles,
			TokenTTL:          time.Duration(cfg.SatiksmeRuntimeSpacetimeTokenTTLSec) * time.Second,
			HTTPTimeout:       time.Duration(cfg.HTTPTimeoutSec) * time.Second,
		})
		if err != nil {
			fatalf(runtimeState, "configure spacetime syncer: %v", err)
		}
		st = store.NewSpacetimeStore(syncer)
	} else {
		sqliteStore, err = store.NewSQLiteStore(cfg.DBPath)
		if err != nil {
			fatalf(runtimeState, "store: %v", err)
		}
		defer sqliteStore.Close()
		st = sqliteStore
	}
	if err := st.Migrate(ctx); err != nil {
		fatalf(runtimeState, "migrate: %v", err)
	}

	httpClient := &http.Client{Timeout: time.Duration(cfg.HTTPTimeoutSec) * time.Second}
	catalogManager := catalog.NewManager(catalog.Settings{
		StopsURL:     cfg.SourceStopsURL,
		RoutesURL:    cfg.SourceRoutesURL,
		GTFSURL:      cfg.SourceGTFSURL,
		MirrorDir:    cfg.CatalogMirrorDir,
		OutputPath:   cfg.CatalogOutputPath,
		RefreshAfter: time.Duration(cfg.CatalogRefreshHours) * time.Hour,
		HTTPClient:   httpClient,
		RuntimeState: runtimeState,
	})
	if _, err := catalogManager.LoadOrRefresh(ctx, false); err != nil {
		fatalf(runtimeState, "catalog load: %v", err)
	}
	runtimeState.UpdateCatalog(catalogManager.Status())
	bundlePublisher := web.NewStaticBundlePublisher(cfg.SatiksmeWebBundleDir, syncer)
	transportPublisher := live.NewSnapshotPublisher(cfg.SatiksmeWebLiveSnapshotDir, 1000)
	retryBundlePublish := false
	if _, err := bundlePublisher.PublishCatalog(ctx, catalogManager.Current(), time.Now().UTC()); err != nil {
		retryBundlePublish = true
		log.Printf("initial bundle publish failed: %v", err)
	}

	reportsSvc := reports.NewService(
		st,
		time.Duration(cfg.ReportCooldownMinutes)*time.Minute,
		time.Duration(cfg.ReportDedupeSeconds)*time.Second,
		time.Duration(cfg.ReportVisibilityMinutes)*time.Minute,
	)

	telegramClient := telegram.NewClient(cfg.BotToken, time.Duration(cfg.HTTPTimeoutSec)*time.Second)
	var webServer *web.Server
	var appURL string
	var publicURL string
	dumpDispatcher := bot.NewDumpDispatcher(telegramClient, st, runtimeState, cfg.ReportDumpChat, time.Second, loc)

	if cfg.SatiksmeWebEnabled {
		webServer, err = web.NewServer(cfg, catalogManager, reportsSvc, dumpDispatcher, st, runtimeState, loc)
		if err != nil {
			fatalf(runtimeState, "web server: %v", err)
		}
		appURL = webServer.AppURL()
		publicURL = webServer.PublicURL()
	}

	botService := bot.NewService(telegramClient, cfg.LongPollTimeout, appURL, publicURL, cfg.ReportsChannelURL, runtimeState)

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	type componentResult struct {
		name string
		err  error
	}
	results := make(chan componentResult, 5)
	components := 0
	start := func(name string, fn func(context.Context) error) {
		components++
		go func() {
			results <- componentResult{name: name, err: fn(runCtx)}
		}()
	}

	start("catalog", func(ctx context.Context) error {
		return catalogRefreshLoop(ctx, catalogManager, bundlePublisher, runtimeState, time.Duration(cfg.CatalogRefreshHours)*time.Hour, retryBundlePublish)
	})
	start("cleanup", func(ctx context.Context) error {
		return cleanupLoop(ctx, st, time.Duration(cfg.DataRetentionHours)*time.Hour, time.Duration(cfg.CleanupIntervalMinutes)*time.Minute)
	})
	start("telegram-bot", botService.Start)
	start("report-dump", dumpDispatcher.Run)
	if webServer != nil {
		start("web", webServer.Run)
	}
	if syncer != nil && transportPublisher.Enabled() {
		transportSettings := live.TransportRuntimeSettings{
			SourceURL:          cfg.LiveVehiclesSourceURL,
			HTTPClient:         httpClient,
			Catalog:            catalogManager,
			StateStore:         syncer,
			Publisher:          transportPublisher,
			ViewerWindow:       time.Duration(cfg.SatiksmeLiveViewerWindowSec) * time.Second,
			ViewerGracePeriod:  time.Duration(cfg.SatiksmeLiveViewerGraceSec) * time.Second,
			PollInterval:       time.Duration(cfg.SatiksmeLiveTransportPollBaseSec) * time.Second,
			MaxPollInterval:    time.Duration(cfg.SatiksmeLiveTransportPollMaxUnchangedSec) * time.Second,
			IdleCheckInterval:  time.Duration(cfg.SatiksmeLiveTransportPollBaseSec) * time.Second,
			CleanupInterval:    5 * time.Minute,
			CleanupRetention:   time.Hour,
			FailureBackoffStep: 10 * time.Second,
			FailureBackoffMax:  20 * time.Second,
		}
		start("live-transport", func(ctx context.Context) error {
			return live.RunTransportSnapshotLoop(ctx, transportSettings)
		})
		start("live-transport-cleanup", func(ctx context.Context) error {
			return live.RunLiveViewerCleanupLoop(ctx, transportSettings)
		})
	}

	log.Printf("satiksme bot started (%s)", version.Display())

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
		fatalf(runtimeState, "satiksme bot stopped with error: %v", firstErr)
	}
}

func fatalf(runtimeState *runtime.State, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	runtimeState.SetFatalError(message)
	log.Fatal(message)
}

func catalogRefreshLoop(ctx context.Context, manager *catalog.Manager, bundlePublisher *web.StaticBundlePublisher, runtimeState *runtime.State, interval time.Duration, retryBundlePublish bool) error {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	retryInterval := 15 * time.Second
	nextDelay := interval
	if retryBundlePublish {
		nextDelay = retryInterval
	}
	timer := time.NewTimer(nextDelay)
	defer timer.Stop()
	resetTimer := func(delay time.Duration) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(delay)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if retryBundlePublish {
				if bundlePublisher != nil {
					if _, err := bundlePublisher.PublishCatalog(ctx, manager.Current(), time.Now().UTC()); err != nil {
						log.Printf("bundle publish failed: %v", err)
						runtimeState.UpdateCatalog(manager.Status())
						resetTimer(retryInterval)
						continue
					}
				}
				retryBundlePublish = false
				runtimeState.UpdateCatalog(manager.Status())
				resetTimer(interval)
				continue
			}
			result, err := manager.Refresh(ctx, false)
			if err != nil {
				log.Printf("catalog refresh failed: %v", err)
			} else if bundlePublisher != nil {
				if _, bundleErr := bundlePublisher.PublishCatalog(ctx, result, time.Now().UTC()); bundleErr != nil {
					log.Printf("bundle publish failed: %v", bundleErr)
					retryBundlePublish = true
					runtimeState.UpdateCatalog(manager.Status())
					resetTimer(retryInterval)
					continue
				}
			}
			runtimeState.UpdateCatalog(manager.Status())
			resetTimer(interval)
		}
	}
}

func cleanupLoop(ctx context.Context, st store.Store, retention, interval time.Duration) error {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_, _ = st.CleanupExpired(ctx, time.Now().UTC().Add(-retention))
		}
	}
}

func resolveSingleInstanceLockPath(cfg config.Config) string {
	if strings.TrimSpace(cfg.SingleInstanceLockPath) != "" {
		return cfg.SingleInstanceLockPath
	}
	if cfg.SatiksmeRuntimeSpacetimeEnabled {
		return "./satiksme_bot.runtime.lock"
	}
	return cfg.DBPath + ".lock"
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
			return nil, fmt.Errorf("another satiksme-bot instance is already running")
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
