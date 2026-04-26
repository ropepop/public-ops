package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/recovery"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/spacetime"
	"telegramtrainapp/internal/store"
)

type syncConfig struct {
	serviceDate      string
	scheduleDir      string
	timezone         string
	scraperDailyHour int
	dryRun           bool

	spacetimeHost     string
	spacetimeDatabase string
	spacetimeIssuer   string
	spacetimeAudience string
	spacetimeKeyFile  string
	serviceSubject    string
	serviceRoles      []string
	tokenTTL          time.Duration
	httpTimeout       time.Duration
}

func main() {
	cfg := parseFlags()
	loc, err := time.LoadLocation(strings.TrimSpace(cfg.timezone))
	if err != nil {
		log.Fatalf("invalid timezone: %v", err)
	}
	if strings.TrimSpace(cfg.serviceDate) == "" {
		cfg.serviceDate = defaultServiceDate(time.Now(), loc)
	}
	if err := run(context.Background(), os.Stdout, cfg, loc); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, stdout io.Writer, cfg syncConfig, loc *time.Location) error {
	if strings.TrimSpace(cfg.scheduleDir) == "" {
		return fmt.Errorf("schedule directory is required")
	}
	if strings.TrimSpace(cfg.spacetimeHost) == "" {
		return fmt.Errorf("spacetime host is required")
	}
	if strings.TrimSpace(cfg.spacetimeDatabase) == "" {
		return fmt.Errorf("spacetime database is required")
	}
	if strings.TrimSpace(cfg.spacetimeKeyFile) == "" {
		return fmt.Errorf("spacetime key file is required")
	}

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              strings.TrimSpace(cfg.spacetimeHost),
		Database:          strings.TrimSpace(cfg.spacetimeDatabase),
		Issuer:            strings.TrimSpace(cfg.spacetimeIssuer),
		Audience:          strings.TrimSpace(cfg.spacetimeAudience),
		JWTPrivateKeyFile: strings.TrimSpace(cfg.spacetimeKeyFile),
		ServiceSubject:    firstNonEmpty(strings.TrimSpace(cfg.serviceSubject), "service:train-bot"),
		ServiceRoles:      cfg.serviceRoles,
		TokenTTL:          cfg.tokenTTL,
		HTTPTimeout:       cfg.httpTimeout,
	})
	if err != nil {
		return fmt.Errorf("configure spacetime syncer: %w", err)
	}

	scheduleCachePath := deriveScheduleCachePath(cfg.scheduleDir)
	scheduleStore, err := store.NewSQLiteStore(scheduleCachePath)
	if err != nil {
		return fmt.Errorf("open schedule cache sqlite: %w", err)
	}
	defer func() {
		_ = scheduleStore.Close()
	}()
	if err := scheduleStore.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate schedule cache sqlite: %w", err)
	}

	manager := schedule.NewManager(scheduleStore, cfg.scheduleDir, loc, cfg.scraperDailyHour)
	if err := manager.LoadServiceDate(ctx, cfg.serviceDate); err != nil {
		return fmt.Errorf("load service date %s: %w", cfg.serviceDate, err)
	}

	appSvc := trainapp.NewService(
		scheduleStore,
		manager,
		ride.NewService(scheduleStore),
		reports.NewService(scheduleStore, 3*time.Minute, 90*time.Second),
		loc,
		false,
	)
	stateStore := store.NewSpacetimeStore(syncer)
	if err := stateStore.PublishRuntimeConfig(ctx, cfg.scraperDailyHour); err != nil {
		return fmt.Errorf("publish runtime config: %w", err)
	}
	result, err := recovery.SyncServiceDate(ctx, appSvc, stateStore, loc, cfg.serviceDate, cfg.dryRun)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, renderSyncSummary(result, cfg.dryRun))
	return nil
}

func parseFlags() syncConfig {
	hostFlag := flag.String("spacetime-host", strings.TrimSpace(os.Getenv("TRAIN_RUNTIME_SPACETIME_HOST")), "SpacetimeDB host")
	databaseFlag := flag.String("spacetime-database", strings.TrimSpace(os.Getenv("TRAIN_RUNTIME_SPACETIME_DATABASE")), "SpacetimeDB database identity or name")
	issuerFlag := flag.String("spacetime-issuer", firstEnv("TRAIN_RUNTIME_SPACETIME_OIDC_ISSUER", "TRAIN_WEB_SPACETIME_OIDC_ISSUER"), "OIDC issuer used to sign runtime service tokens")
	audienceFlag := flag.String("spacetime-audience", firstEnv("TRAIN_RUNTIME_SPACETIME_OIDC_AUDIENCE", "TRAIN_WEB_SPACETIME_OIDC_AUDIENCE"), "OIDC audience for runtime service tokens")
	keyFileFlag := flag.String("spacetime-key-file", firstEnv("TRAIN_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE", "TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE"), "RSA private key used to sign runtime service tokens")
	serviceSubjectFlag := flag.String("service-subject", firstEnv("TRAIN_RUNTIME_SPACETIME_SERVICE_SUBJECT"), "service token subject")
	serviceRolesFlag := flag.String("service-roles", firstEnv("TRAIN_RUNTIME_SPACETIME_SERVICE_ROLES"), "comma-separated service roles")
	tokenTTLFlag := flag.Int("token-ttl-sec", envOrInt("TRAIN_RUNTIME_SPACETIME_TOKEN_TTL_SEC", 900), "service token lifetime in seconds")
	httpTimeoutFlag := flag.Int("http-timeout-sec", envOrInt("HTTP_TIMEOUT_SEC", 45), "HTTP timeout in seconds")
	serviceDateFlag := flag.String("service-date", "", "service date to sync in YYYY-MM-DD (defaults to local today)")
	scheduleDirFlag := flag.String("schedule-dir", strings.TrimSpace(os.Getenv("SCHEDULE_DIR")), "schedule snapshot directory")
	timezoneFlag := flag.String("timezone", envOr("TZ", "Europe/Riga"), "timezone used to resolve service dates")
	scraperDailyHourFlag := flag.Int("scraper-daily-hour", envOrInt("SCRAPER_DAILY_HOUR", 3), "cutoff hour used by the local schedule manager")
	dryRunFlag := flag.Bool("dry-run", false, "print the sync plan without writing to Spacetime")
	flag.Parse()

	return syncConfig{
		serviceDate:       strings.TrimSpace(*serviceDateFlag),
		scheduleDir:       strings.TrimSpace(*scheduleDirFlag),
		timezone:          strings.TrimSpace(*timezoneFlag),
		scraperDailyHour:  *scraperDailyHourFlag,
		dryRun:            *dryRunFlag,
		spacetimeHost:     strings.TrimSpace(*hostFlag),
		spacetimeDatabase: strings.TrimSpace(*databaseFlag),
		spacetimeIssuer:   strings.TrimSpace(*issuerFlag),
		spacetimeAudience: strings.TrimSpace(*audienceFlag),
		spacetimeKeyFile:  strings.TrimSpace(*keyFileFlag),
		serviceSubject:    strings.TrimSpace(*serviceSubjectFlag),
		serviceRoles:      parseCSV(firstNonEmpty(*serviceRolesFlag, "train_service")),
		tokenTTL:          time.Duration(*tokenTTLFlag) * time.Second,
		httpTimeout:       time.Duration(*httpTimeoutFlag) * time.Second,
	}
}

func renderSyncSummary(result recovery.SyncResult, dryRun bool) string {
	status := "synced"
	if dryRun {
		status = "dry-run"
	} else if !result.Synced {
		status = "skipped"
	}
	return fmt.Sprintf(
		"status: %s\nservice date: %s\nsource version: %s\nlocal snapshot: stations=%d trains=%d stops=%d\nspacetime before: stations=%d trains=%d stops=%d\n",
		status,
		strings.TrimSpace(result.ServiceDate),
		firstNonEmpty(strings.TrimSpace(result.SourceVersion), "snapshot-unknown"),
		result.Stations,
		result.Trains,
		result.Stops,
		result.ExistingStations,
		result.ExistingTrains,
		result.ExistingStops,
	)
}

func defaultServiceDate(now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return now.In(loc).Format("2006-01-02")
}

func deriveScheduleCachePath(scheduleDir string) string {
	dir := strings.TrimSpace(scheduleDir)
	if dir == "" {
		return filepath.Clean("train-runtime-cache.db")
	}
	return filepath.Join(filepath.Dir(dir), "train-runtime-cache.db")
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envOr(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseCSV(raw string) []string {
	parts := strings.Split(strings.TrimSpace(raw), ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
