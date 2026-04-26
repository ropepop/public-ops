package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken                               string
	SingleInstanceLockPath                 string
	Timezone                               string
	ScheduleDir                            string
	LongPollTimeout                        int
	HTTPTimeoutSec                         int
	DataRetentionHrs                       int
	CooldownMin                            int
	DedupeSec                              int
	TrainWebEnabled                        bool
	TrainWebBindAddr                       string
	TrainWebPort                           int
	TrainWebPublicBaseURL                  string
	TrainWebSessionSecretFile              string
	TrainWebTelegramAuthMaxAgeSec          int
	TrainWebTestLoginEnabled               bool
	TrainWebTestUserID                     int64
	TrainWebTestTicketSecretFile           string
	TrainWebTestTicketTTLSec               int
	TrainWebSpacetimeHost                  string
	TrainWebSpacetimeDatabase              string
	TrainWebSpacetimeOIDCIssuer            string
	TrainWebSpacetimeOIDCAudience          string
	TrainWebSpacetimeJWTPrivateKeyFile     string
	TrainWebSpacetimeTokenTTLSec           int
	TrainWebPublicEdgeCacheEnabled         bool
	TrainWebPublicEdgeCacheTTLSec          int
	TrainWebPublicEdgeCacheStateFile       string
	TrainWebBundleDir                      string
	TrainRuntimeSpacetimeHost              string
	TrainRuntimeSpacetimeDatabase          string
	TrainRuntimeSpacetimeOIDCIssuer        string
	TrainRuntimeSpacetimeOIDCAudience      string
	TrainRuntimeSpacetimeJWTPrivateKeyFile string
	TrainRuntimeSpacetimeTokenTTLSec       int
	TrainRuntimeSpacetimeServiceSubject    string
	TrainRuntimeSpacetimeServiceRoles      []string
	ExternalTrainMapEnabled                bool
	ExternalTrainMapBaseURL                string
	ExternalTrainMapWsURL                  string
	FeatureStationCheckin                  bool
	ScraperViviPageURL                     string
	ScraperViviGTFSURL                     string
	ScraperDailyHour                       int
	ScraperMinTrains                       int
	ScraperOutputDir                       string
	ScraperUserAgent                       string
	RuntimeSnapshotGCEnabled               bool
	ReportDumpChatID                       int64
}

func Load() (Config, error) {
	longPollTimeout, err := envOrIntStrict("LONG_POLL_TIMEOUT", 30)
	if err != nil {
		return Config{}, err
	}
	httpTimeoutSec, err := envOrIntStrict("HTTP_TIMEOUT_SEC", 15)
	if err != nil {
		return Config{}, err
	}
	dataRetentionHrs, err := envOrIntStrict("DATA_RETENTION_HOURS", 24)
	if err != nil {
		return Config{}, err
	}
	cooldownMin, err := envOrIntStrict("REPORT_COOLDOWN_MINUTES", 3)
	if err != nil {
		return Config{}, err
	}
	dedupeSec, err := envOrIntStrict("REPORT_DEDUPE_SECONDS", 90)
	if err != nil {
		return Config{}, err
	}
	trainWebEnabled, err := envOrBoolStrict("TRAIN_WEB_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	trainWebPort, err := envOrIntStrict("TRAIN_WEB_PORT", 9317)
	if err != nil {
		return Config{}, err
	}
	trainWebTelegramAuthMaxAgeSec, err := envOrIntStrict("TRAIN_WEB_TELEGRAM_AUTH_MAX_AGE_SEC", 300)
	if err != nil {
		return Config{}, err
	}
	trainWebTestLoginEnabled, err := envOrBoolStrict("TRAIN_WEB_TEST_LOGIN_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	trainWebTestUserID, err := envOrInt64Strict("TRAIN_WEB_TEST_USER_ID", 0)
	if err != nil {
		return Config{}, err
	}
	trainWebTestTicketTTLSec, err := envOrIntStrict("TRAIN_WEB_TEST_TICKET_TTL_SEC", 60)
	if err != nil {
		return Config{}, err
	}
	trainWebSpacetimeTokenTTLSec, err := envOrIntStrict("TRAIN_WEB_SPACETIME_TOKEN_TTL_SEC", 24*60*60)
	if err != nil {
		return Config{}, err
	}
	trainWebPublicEdgeCacheEnabled, err := envOrBoolStrict("TRAIN_WEB_PUBLIC_EDGE_CACHE_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	trainWebPublicEdgeCacheTTLSec, err := envOrIntStrict("TRAIN_WEB_PUBLIC_EDGE_CACHE_TTL_SEC", 30*24*60*60)
	if err != nil {
		return Config{}, err
	}
	trainRuntimeSpacetimeTokenTTLSec, err := envOrIntStrict("TRAIN_RUNTIME_SPACETIME_TOKEN_TTL_SEC", 15*60)
	if err != nil {
		return Config{}, err
	}
	externalTrainMapEnabled, err := envOrBoolStrict("EXTERNAL_TRAINMAP_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	featureStationCheckin, err := envOrBoolStrict("FEATURE_STATION_CHECKIN", true)
	if err != nil {
		return Config{}, err
	}
	scraperDailyHour, err := envOrIntStrict("SCRAPER_DAILY_HOUR", 3)
	if err != nil {
		return Config{}, err
	}
	scraperMinTrains, err := envOrIntStrict("SCRAPER_MIN_TRAINS", 1)
	if err != nil {
		return Config{}, err
	}
	runtimeSnapshotGCEnabled, err := envOrBoolStrict("TRAIN_RUNTIME_SNAPSHOT_GC_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	reportDumpChatID, err := envOrInt64Strict("REPORT_DUMP_CHAT_ID", 0)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		BotToken:                               os.Getenv("BOT_TOKEN"),
		SingleInstanceLockPath:                 strings.TrimSpace(envOr("SINGLE_INSTANCE_LOCK_PATH", "")),
		Timezone:                               envOr("TZ", "Europe/Riga"),
		ScheduleDir:                            envOr("SCHEDULE_DIR", "./data/schedules"),
		LongPollTimeout:                        longPollTimeout,
		HTTPTimeoutSec:                         httpTimeoutSec,
		DataRetentionHrs:                       dataRetentionHrs,
		CooldownMin:                            cooldownMin,
		DedupeSec:                              dedupeSec,
		TrainWebEnabled:                        trainWebEnabled,
		TrainWebBindAddr:                       envOr("TRAIN_WEB_BIND_ADDR", "127.0.0.1"),
		TrainWebPort:                           trainWebPort,
		TrainWebPublicBaseURL:                  strings.TrimRight(strings.TrimSpace(envOr("TRAIN_WEB_PUBLIC_BASE_URL", "")), "/"),
		TrainWebSessionSecretFile:              strings.TrimSpace(envOr("TRAIN_WEB_SESSION_SECRET_FILE", "")),
		TrainWebTelegramAuthMaxAgeSec:          trainWebTelegramAuthMaxAgeSec,
		TrainWebTestLoginEnabled:               trainWebTestLoginEnabled,
		TrainWebTestUserID:                     trainWebTestUserID,
		TrainWebTestTicketSecretFile:           strings.TrimSpace(envOr("TRAIN_WEB_TEST_TICKET_SECRET_FILE", "")),
		TrainWebTestTicketTTLSec:               trainWebTestTicketTTLSec,
		TrainWebSpacetimeHost:                  strings.TrimRight(strings.TrimSpace(envOr("TRAIN_WEB_SPACETIME_HOST", "")), "/"),
		TrainWebSpacetimeDatabase:              strings.TrimSpace(envOr("TRAIN_WEB_SPACETIME_DATABASE", "")),
		TrainWebSpacetimeOIDCIssuer:            strings.TrimRight(strings.TrimSpace(envOr("TRAIN_WEB_SPACETIME_OIDC_ISSUER", "")), "/"),
		TrainWebSpacetimeOIDCAudience:          strings.TrimSpace(envOr("TRAIN_WEB_SPACETIME_OIDC_AUDIENCE", "train-bot-web")),
		TrainWebSpacetimeJWTPrivateKeyFile:     strings.TrimSpace(envOr("TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE", "")),
		TrainWebSpacetimeTokenTTLSec:           trainWebSpacetimeTokenTTLSec,
		TrainWebPublicEdgeCacheEnabled:         trainWebPublicEdgeCacheEnabled,
		TrainWebPublicEdgeCacheTTLSec:          trainWebPublicEdgeCacheTTLSec,
		TrainWebPublicEdgeCacheStateFile:       strings.TrimSpace(envOr("TRAIN_WEB_PUBLIC_EDGE_CACHE_STATE_FILE", "")),
		TrainWebBundleDir:                      strings.TrimSpace(envOr("TRAIN_WEB_BUNDLE_DIR", "")),
		TrainRuntimeSpacetimeHost:              strings.TrimRight(strings.TrimSpace(envOr("TRAIN_RUNTIME_SPACETIME_HOST", "")), "/"),
		TrainRuntimeSpacetimeDatabase:          strings.TrimSpace(envOr("TRAIN_RUNTIME_SPACETIME_DATABASE", "")),
		TrainRuntimeSpacetimeOIDCIssuer:        strings.TrimRight(strings.TrimSpace(envOr("TRAIN_RUNTIME_SPACETIME_OIDC_ISSUER", "")), "/"),
		TrainRuntimeSpacetimeOIDCAudience:      strings.TrimSpace(envOr("TRAIN_RUNTIME_SPACETIME_OIDC_AUDIENCE", "train-bot-web")),
		TrainRuntimeSpacetimeJWTPrivateKeyFile: strings.TrimSpace(envOr("TRAIN_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE", "")),
		TrainRuntimeSpacetimeTokenTTLSec:       trainRuntimeSpacetimeTokenTTLSec,
		TrainRuntimeSpacetimeServiceSubject:    strings.TrimSpace(envOr("TRAIN_RUNTIME_SPACETIME_SERVICE_SUBJECT", "service:train-bot")),
		TrainRuntimeSpacetimeServiceRoles:      parseCSV(envOr("TRAIN_RUNTIME_SPACETIME_SERVICE_ROLES", "train_service")),
		ExternalTrainMapEnabled:                externalTrainMapEnabled,
		ExternalTrainMapBaseURL:                strings.TrimRight(strings.TrimSpace(envOr("EXTERNAL_TRAINMAP_BASE_URL", "https://trainmap.vivi.lv")), "/"),
		ExternalTrainMapWsURL:                  strings.TrimSpace(envOr("EXTERNAL_TRAINMAP_WS_URL", "wss://trainmap.pv.lv/ws")),
		FeatureStationCheckin:                  featureStationCheckin,
		ScraperViviPageURL:                     envOr("SCRAPER_VIVI_PAGE_URL", "https://www.vivi.lv/lv/informacija-pasazieriem/"),
		ScraperViviGTFSURL:                     envOr("SCRAPER_VIVI_GTFS_URL", "https://www.vivi.lv/uploads/GTFS.zip"),
		ScraperDailyHour:                       scraperDailyHour,
		ScraperMinTrains:                       scraperMinTrains,
		ScraperOutputDir:                       strings.TrimSpace(envOr("SCRAPER_OUTPUT_DIR", "")),
		ScraperUserAgent:                       envOr("SCRAPER_USER_AGENT", "telegram-train-bot-scraper/1.0"),
		RuntimeSnapshotGCEnabled:               runtimeSnapshotGCEnabled,
		ReportDumpChatID:                       reportDumpChatID,
	}

	if cfg.BotToken == "" {
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	}
	if cfg.HTTPTimeoutSec <= cfg.LongPollTimeout {
		cfg.HTTPTimeoutSec = cfg.LongPollTimeout + 10
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("invalid TZ: %w", err)
	}
	if cfg.ScraperMinTrains < 1 {
		cfg.ScraperMinTrains = 1
	}
	if cfg.ScraperDailyHour < 0 || cfg.ScraperDailyHour > 23 {
		cfg.ScraperDailyHour = 3
	}
	if cfg.TrainWebPort <= 0 || cfg.TrainWebPort > 65535 {
		return Config{}, fmt.Errorf("TRAIN_WEB_PORT must be between 1 and 65535, got %d", cfg.TrainWebPort)
	}
	if cfg.TrainWebTelegramAuthMaxAgeSec <= 0 {
		return Config{}, fmt.Errorf("TRAIN_WEB_TELEGRAM_AUTH_MAX_AGE_SEC must be positive, got %d", cfg.TrainWebTelegramAuthMaxAgeSec)
	}
	if cfg.TrainWebEnabled {
		if cfg.TrainWebPublicBaseURL == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_PUBLIC_BASE_URL is required when TRAIN_WEB_ENABLED=true")
		}
		if cfg.TrainWebSessionSecretFile == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_SESSION_SECRET_FILE is required when TRAIN_WEB_ENABLED=true")
		}
		if cfg.TrainWebSpacetimeHost == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_SPACETIME_HOST is required when TRAIN_WEB_ENABLED=true")
		}
		if cfg.TrainWebSpacetimeDatabase == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_SPACETIME_DATABASE is required when TRAIN_WEB_ENABLED=true")
		}
		if cfg.TrainWebSpacetimeJWTPrivateKeyFile == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE is required when TRAIN_WEB_ENABLED=true")
		}
		if cfg.TrainWebSpacetimeOIDCAudience == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_SPACETIME_OIDC_AUDIENCE is required when TRAIN_WEB_ENABLED=true")
		}
		if cfg.TrainWebSpacetimeTokenTTLSec <= 0 {
			return Config{}, fmt.Errorf("TRAIN_WEB_SPACETIME_TOKEN_TTL_SEC must be positive, got %d", cfg.TrainWebSpacetimeTokenTTLSec)
		}
		if cfg.TrainWebSpacetimeTokenTTLSec > 24*60*60 {
			return Config{}, fmt.Errorf("TRAIN_WEB_SPACETIME_TOKEN_TTL_SEC must be at most 86400, got %d", cfg.TrainWebSpacetimeTokenTTLSec)
		}
	}
	if cfg.TrainWebTestLoginEnabled && !cfg.TrainWebEnabled {
		return Config{}, fmt.Errorf("TRAIN_WEB_TEST_LOGIN_ENABLED requires TRAIN_WEB_ENABLED=true")
	}
	if cfg.TrainWebTestLoginEnabled {
		if cfg.TrainWebTestUserID <= 0 {
			return Config{}, fmt.Errorf("TRAIN_WEB_TEST_USER_ID must be positive when TRAIN_WEB_TEST_LOGIN_ENABLED=true")
		}
		if cfg.TrainWebTestTicketSecretFile == "" {
			return Config{}, fmt.Errorf("TRAIN_WEB_TEST_TICKET_SECRET_FILE is required when TRAIN_WEB_TEST_LOGIN_ENABLED=true")
		}
		if cfg.TrainWebTestTicketTTLSec <= 0 {
			return Config{}, fmt.Errorf("TRAIN_WEB_TEST_TICKET_TTL_SEC must be positive, got %d", cfg.TrainWebTestTicketTTLSec)
		}
	}
	if cfg.TrainWebPublicEdgeCacheEnabled && !cfg.TrainWebEnabled {
		return Config{}, fmt.Errorf("TRAIN_WEB_PUBLIC_EDGE_CACHE_ENABLED requires TRAIN_WEB_ENABLED=true")
	}
	if cfg.TrainWebPublicEdgeCacheTTLSec <= 0 {
		return Config{}, fmt.Errorf("TRAIN_WEB_PUBLIC_EDGE_CACHE_TTL_SEC must be positive, got %d", cfg.TrainWebPublicEdgeCacheTTLSec)
	}
	if cfg.TrainWebPublicEdgeCacheStateFile == "" {
		cfg.TrainWebPublicEdgeCacheStateFile = defaultTrainWebPublicEdgeCacheStateFile(cfg.ScheduleDir)
	}
	if cfg.TrainWebBundleDir == "" {
		cfg.TrainWebBundleDir = defaultTrainWebBundleDir(cfg.ScheduleDir)
	}
	if cfg.SingleInstanceLockPath == "" {
		cfg.SingleInstanceLockPath = defaultSingleInstanceLockPath(cfg.ScheduleDir)
	}
	if cfg.TrainRuntimeSpacetimeHost == "" {
		return Config{}, fmt.Errorf("TRAIN_RUNTIME_SPACETIME_HOST is required")
	}
	if cfg.TrainRuntimeSpacetimeDatabase == "" {
		return Config{}, fmt.Errorf("TRAIN_RUNTIME_SPACETIME_DATABASE is required")
	}
	if cfg.TrainRuntimeSpacetimeJWTPrivateKeyFile == "" {
		return Config{}, fmt.Errorf("TRAIN_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE is required")
	}
	if cfg.TrainRuntimeSpacetimeOIDCAudience == "" {
		return Config{}, fmt.Errorf("TRAIN_RUNTIME_SPACETIME_OIDC_AUDIENCE is required")
	}
	if cfg.TrainRuntimeSpacetimeTokenTTLSec <= 0 {
		return Config{}, fmt.Errorf("TRAIN_RUNTIME_SPACETIME_TOKEN_TTL_SEC must be positive, got %d", cfg.TrainRuntimeSpacetimeTokenTTLSec)
	}
	if cfg.TrainRuntimeSpacetimeTokenTTLSec > 24*60*60 {
		return Config{}, fmt.Errorf("TRAIN_RUNTIME_SPACETIME_TOKEN_TTL_SEC must be at most 86400, got %d", cfg.TrainRuntimeSpacetimeTokenTTLSec)
	}
	if len(cfg.TrainRuntimeSpacetimeServiceRoles) == 0 {
		cfg.TrainRuntimeSpacetimeServiceRoles = []string{"train_service"}
	}
	if cfg.ExternalTrainMapEnabled {
		if cfg.ExternalTrainMapBaseURL == "" {
			return Config{}, fmt.Errorf("EXTERNAL_TRAINMAP_BASE_URL is required when EXTERNAL_TRAINMAP_ENABLED=true")
		}
		if cfg.ExternalTrainMapWsURL == "" {
			return Config{}, fmt.Errorf("EXTERNAL_TRAINMAP_WS_URL is required when EXTERNAL_TRAINMAP_ENABLED=true")
		}
	}
	return cfg, nil
}

func defaultRuntimeStateDir(scheduleDir string) string {
	clean := strings.TrimSpace(scheduleDir)
	if clean == "" || clean == "." {
		return "./data"
	}
	parent := filepath.Dir(clean)
	if parent == "." || parent == "" {
		return "./data"
	}
	return parent
}

func defaultSingleInstanceLockPath(scheduleDir string) string {
	return filepath.Join(defaultRuntimeStateDir(scheduleDir), "train-bot.lock")
}

func defaultTrainWebBundleDir(scheduleDir string) string {
	return filepath.Join(defaultRuntimeStateDir(scheduleDir), "public-bundles")
}

func defaultTrainWebPublicEdgeCacheStateFile(scheduleDir string) string {
	return filepath.Join(defaultRuntimeStateDir(scheduleDir), "train-bot.public-edge-cache.json")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrIntStrict(key string, fallback int) (int, error) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err == nil {
			return n, nil
		}
		return 0, fmt.Errorf("%s must be an integer, got %q", key, v)
	}
	return fallback, nil
}

func envOrBoolStrict(key string, fallback bool) (bool, error) {
	if v := os.Getenv(key); v != "" {
		if parsed, ok := parseBool(v); ok {
			return parsed, nil
		}
		return false, fmt.Errorf("%s must be a boolean, got %q", key, v)
	}
	return fallback, nil
}

func envOrInt64Strict(key string, fallback int64) (int64, error) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n, nil
		}
		return 0, fmt.Errorf("%s must be an integer, got %q", key, v)
	}
	return fallback, nil
}

func parseCSV(raw string) []string {
	items := strings.Split(raw, ",")
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func parseBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}
