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
	BotToken                                  string
	DBPath                                    string
	SingleInstanceLockPath                    string
	Timezone                                  string
	LongPollTimeout                           int
	HTTPTimeoutSec                            int
	DataRetentionHours                        int
	ReportVisibilityMinutes                   int
	ReportCooldownMinutes                     int
	ReportDedupeSeconds                       int
	SatiksmeWebEnabled                        bool
	SatiksmeWebBindAddr                       string
	SatiksmeWebPort                           int
	SatiksmeWebPublicBaseURL                  string
	SatiksmeWebSessionSecretFile              string
	SatiksmeWebTelegramBotUsername            string
	SatiksmeWebTelegramClientID               string
	SatiksmeWebTelegramClientSecretFile       string
	SatiksmeWebTelegramAuthMaxAgeSec          int
	SatiksmeWebTelegramAuthStateTTLSec        int
	SatiksmeWebBundleDir                      string
	SatiksmeWebLiveSnapshotDir                string
	SatiksmeWebLiveViewerHeartbeatEnabled     bool
	SatiksmeWebSpacetimeEnabled               bool
	SatiksmeWebSpacetimeHost                  string
	SatiksmeWebSpacetimeDatabase              string
	SatiksmeWebSpacetimeOIDCIssuer            string
	SatiksmeWebSpacetimeOIDCAudience          string
	SatiksmeWebSpacetimeJWTPrivateKeyFile     string
	SatiksmeWebSpacetimeTokenTTLSec           int
	SatiksmeWebSpacetimeDirectOnly            bool
	SatiksmeRuntimeSpacetimeEnabled           bool
	SatiksmeRuntimeSpacetimeHost              string
	SatiksmeRuntimeSpacetimeDatabase          string
	SatiksmeRuntimeSpacetimeOIDCIssuer        string
	SatiksmeRuntimeSpacetimeOIDCAudience      string
	SatiksmeRuntimeSpacetimeJWTPrivateKeyFile string
	SatiksmeRuntimeSpacetimeTokenTTLSec       int
	SatiksmeRuntimeSpacetimeServiceSubject    string
	SatiksmeRuntimeSpacetimeServiceRoles      []string
	SatiksmeLiveViewerWindowSec               int
	SatiksmeLiveViewerGraceSec                int
	SatiksmeLiveTransportPollBaseSec          int
	SatiksmeLiveTransportPollMaxUnchangedSec  int
	SatiksmeChatAnalyzerEnabled               bool
	SatiksmeChatAnalyzerAPIID                 int
	SatiksmeChatAnalyzerAPIHash               string
	SatiksmeChatAnalyzerSessionFile           string
	SatiksmeChatAnalyzerChatID                string
	SatiksmeChatAnalyzerPollInterval          time.Duration
	SatiksmeChatAnalyzerBatchLimit            int
	SatiksmeChatAnalyzerMinConfidence         float64
	SatiksmeChatAnalyzerDryRun                bool
	SatiksmeChatAnalyzerProcessStartMinute    int
	SatiksmeChatAnalyzerProcessEndMinute      int
	SatiksmeChatAnalyzerProcessInterval       time.Duration
	SatiksmeChatAnalyzerModelProvider         string
	SatiksmeChatAnalyzerModelBaseURL          string
	SatiksmeChatAnalyzerModelName             string
	SatiksmeChatAnalyzerModelAPIKey           string
	SatiksmeChatAnalyzerGoogleAPIKey          string
	SatiksmeChatAnalyzerGoogleModelAuto       bool
	SatiksmeChatAnalyzerGoogleModelsURL       string
	SatiksmeChatAnalyzerGoogleModelPolicy     string
	SatiksmeChatAnalyzerModelTimeout          time.Duration
	SatiksmeChatAnalyzerModelNativeOllama     bool
	SatiksmeChatAnalyzerModelCallDelay        time.Duration
	SatiksmeChatAnalyzerRetryBaseDelay        time.Duration
	SatiksmeChatAnalyzerRetryMaxDelay         time.Duration
	ReportDumpChat                            string
	ReportsChannelURL                         string
	CatalogMirrorDir                          string
	CatalogOutputPath                         string
	CatalogRefreshHours                       int
	CleanupIntervalMinutes                    int
	LiveVehiclesSourceURL                     string
	SourceStopsURL                            string
	SourceRoutesURL                           string
	SourceGTFSURL                             string
}

type CatalogOnly struct {
	HTTPTimeoutSec      int
	CatalogMirrorDir    string
	CatalogOutputPath   string
	CatalogRefreshHours int
	SourceStopsURL      string
	SourceRoutesURL     string
	SourceGTFSURL       string
}

func Load() (Config, error) {
	cfg, err := loadCommon()
	if err != nil {
		return Config{}, err
	}
	cfg.BotToken = strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if cfg.BotToken == "" {
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	}
	if cfg.SatiksmeWebEnabled {
		if cfg.SatiksmeWebPublicBaseURL == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_PUBLIC_BASE_URL is required when SATIKSME_WEB_ENABLED=true")
		}
		if cfg.SatiksmeWebSessionSecretFile == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SESSION_SECRET_FILE is required when SATIKSME_WEB_ENABLED=true")
		}
		if cfg.SatiksmeWebTelegramBotUsername == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_TELEGRAM_BOT_USERNAME is required when SATIKSME_WEB_ENABLED=true")
		}
	}
	return cfg, nil
}

func LoadCatalogOnly() (CatalogOnly, error) {
	cfg, err := loadCommon()
	if err != nil {
		return CatalogOnly{}, err
	}
	return CatalogOnly{
		HTTPTimeoutSec:      cfg.HTTPTimeoutSec,
		CatalogMirrorDir:    cfg.CatalogMirrorDir,
		CatalogOutputPath:   cfg.CatalogOutputPath,
		CatalogRefreshHours: cfg.CatalogRefreshHours,
		SourceStopsURL:      cfg.SourceStopsURL,
		SourceRoutesURL:     cfg.SourceRoutesURL,
		SourceGTFSURL:       cfg.SourceGTFSURL,
	}, nil
}

func loadCommon() (Config, error) {
	longPollTimeout, err := envOrIntStrict("LONG_POLL_TIMEOUT", 30)
	if err != nil {
		return Config{}, err
	}
	httpTimeoutSec, err := envOrIntStrict("HTTP_TIMEOUT_SEC", 20)
	if err != nil {
		return Config{}, err
	}
	dataRetentionHours, err := envOrIntStrict("DATA_RETENTION_HOURS", 24)
	if err != nil {
		return Config{}, err
	}
	reportVisibilityMinutes, err := envOrIntStrict("REPORT_VISIBILITY_MINUTES", 30)
	if err != nil {
		return Config{}, err
	}
	reportCooldownMinutes, err := envOrIntStrict("REPORT_COOLDOWN_MINUTES", 3)
	if err != nil {
		return Config{}, err
	}
	reportDedupeSeconds, err := envOrIntStrict("REPORT_DEDUPE_SECONDS", 90)
	if err != nil {
		return Config{}, err
	}
	webEnabled, err := envOrBoolStrict("SATIKSME_WEB_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	webPort, err := envOrIntStrict("SATIKSME_WEB_PORT", 9318)
	if err != nil {
		return Config{}, err
	}
	authMaxAge, err := envOrIntStrict("SATIKSME_WEB_TELEGRAM_AUTH_MAX_AGE_SEC", 300)
	if err != nil {
		return Config{}, err
	}
	authStateTTL, err := envOrIntStrict("SATIKSME_WEB_TELEGRAM_AUTH_STATE_TTL_SEC", 600)
	if err != nil {
		return Config{}, err
	}
	webSpacetimeEnabled, err := envOrBoolStrict("SATIKSME_WEB_SPACETIME_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	webLiveViewerHeartbeatEnabled, err := envOrBoolStrict("SATIKSME_WEB_LIVE_VIEWER_HEARTBEAT_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	webSpacetimeTokenTTLSec, err := envOrIntStrict("SATIKSME_WEB_SPACETIME_TOKEN_TTL_SEC", 24*60*60)
	if err != nil {
		return Config{}, err
	}
	webSpacetimeDirectOnly, err := envOrBoolStrict("SATIKSME_WEB_SPACETIME_DIRECT_ONLY", webSpacetimeEnabled)
	if err != nil {
		return Config{}, err
	}
	runtimeSpacetimeEnabled, err := envOrBoolStrict("SATIKSME_RUNTIME_SPACETIME_ENABLED", webSpacetimeEnabled)
	if err != nil {
		return Config{}, err
	}
	runtimeSpacetimeTokenTTLSec, err := envOrIntStrict("SATIKSME_RUNTIME_SPACETIME_TOKEN_TTL_SEC", 15*60)
	if err != nil {
		return Config{}, err
	}
	liveViewerWindowSec, err := envOrIntStrict("SATIKSME_LIVE_VIEWER_WINDOW_SEC", 75)
	if err != nil {
		return Config{}, err
	}
	liveViewerGraceSec, err := envOrIntStrict("SATIKSME_LIVE_VIEWER_GRACE_SEC", 10)
	if err != nil {
		return Config{}, err
	}
	liveTransportPollBaseSec, err := envOrIntStrict("SATIKSME_LIVE_TRANSPORT_POLL_BASE_SEC", 5)
	if err != nil {
		return Config{}, err
	}
	liveTransportPollMaxUnchangedSec, err := envOrIntStrict("SATIKSME_LIVE_TRANSPORT_POLL_MAX_UNCHANGED_SEC", 30)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerEnabled, err := envOrBoolStrict("SATIKSME_CHAT_ANALYZER_ENABLED", false)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerAPIID, err := envOrIntStrict("SATIKSME_CHAT_ANALYZER_API_ID", 0)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerPollInterval, err := envOrDurationStrict("SATIKSME_CHAT_ANALYZER_POLL_INTERVAL", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerBatchLimit, err := envOrIntStrict("SATIKSME_CHAT_ANALYZER_BATCH_LIMIT", 250)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerMinConfidence, err := envOrFloatStrict("SATIKSME_CHAT_ANALYZER_MIN_CONFIDENCE", 0.82)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerDryRun, err := envOrBoolStrict("SATIKSME_CHAT_ANALYZER_DRY_RUN", true)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerProcessStart, err := envOrClockMinuteStrict("SATIKSME_CHAT_ANALYZER_PROCESS_START", "08:00")
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerProcessEnd, err := envOrClockMinuteStrict("SATIKSME_CHAT_ANALYZER_PROCESS_END", "20:00")
	if err != nil {
		return Config{}, err
	}
	if chatAnalyzerProcessEnd == chatAnalyzerProcessStart {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_PROCESS_END must differ from SATIKSME_CHAT_ANALYZER_PROCESS_START")
	}
	chatAnalyzerProcessInterval, err := envOrDurationStrict("SATIKSME_CHAT_ANALYZER_PROCESS_INTERVAL", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerModelProvider := strings.ToLower(strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_MODEL_PROVIDER", "google")))
	if chatAnalyzerModelProvider == "" {
		chatAnalyzerModelProvider = "google"
	}
	defaultModelBaseURL := "https://generativelanguage.googleapis.com/v1beta/openai"
	defaultModelName := "auto"
	defaultGoogleAuto := true
	if chatAnalyzerModelProvider == "openrouter" {
		defaultModelBaseURL = "https://openrouter.ai/api/v1"
		defaultModelName = "openrouter/free"
		defaultGoogleAuto = false
	}
	chatAnalyzerGoogleModelAuto, err := envOrBoolStrict("SATIKSME_CHAT_ANALYZER_GOOGLE_MODEL_AUTO", defaultGoogleAuto)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerModelTimeout, err := envOrDurationStrict("SATIKSME_CHAT_ANALYZER_MODEL_TIMEOUT", 180*time.Second)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerModelNativeOllama, err := envOrBoolStrict("SATIKSME_CHAT_ANALYZER_MODEL_NATIVE_OLLAMA", false)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerModelCallDelay, err := envOrDurationStrict("SATIKSME_CHAT_ANALYZER_MODEL_CALL_DELAY", 0)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerRetryBaseDelay, err := envOrDurationStrict("SATIKSME_CHAT_ANALYZER_RETRY_BASE_DELAY", time.Minute)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerRetryMaxDelay, err := envOrDurationStrict("SATIKSME_CHAT_ANALYZER_RETRY_MAX_DELAY", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	chatAnalyzerGoogleAPIKey := strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY", ""))
	chatAnalyzerModelAPIKey := strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_MODEL_API_KEY", ""))
	if chatAnalyzerModelProvider == "google" && chatAnalyzerModelAPIKey == "" {
		chatAnalyzerModelAPIKey = chatAnalyzerGoogleAPIKey
	}
	refreshHours, err := envOrIntStrict("SATIKSME_CATALOG_REFRESH_HOURS", 24)
	if err != nil {
		return Config{}, err
	}
	cleanupIntervalMinutes, err := envOrIntStrict("SATIKSME_CLEANUP_INTERVAL_MINUTES", 10)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		DBPath:                                    envOr("DB_PATH", "./satiksme_bot.db"),
		SingleInstanceLockPath:                    envOr("SINGLE_INSTANCE_LOCK_PATH", ""),
		Timezone:                                  envOr("TZ", "Europe/Riga"),
		LongPollTimeout:                           longPollTimeout,
		HTTPTimeoutSec:                            httpTimeoutSec,
		DataRetentionHours:                        dataRetentionHours,
		ReportVisibilityMinutes:                   reportVisibilityMinutes,
		ReportCooldownMinutes:                     reportCooldownMinutes,
		ReportDedupeSeconds:                       reportDedupeSeconds,
		SatiksmeWebEnabled:                        webEnabled,
		SatiksmeWebBindAddr:                       envOr("SATIKSME_WEB_BIND_ADDR", "127.0.0.1"),
		SatiksmeWebPort:                           webPort,
		SatiksmeWebPublicBaseURL:                  strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_WEB_PUBLIC_BASE_URL", "")), "/"),
		SatiksmeWebSessionSecretFile:              strings.TrimSpace(envOr("SATIKSME_WEB_SESSION_SECRET_FILE", "")),
		SatiksmeWebTelegramBotUsername:            strings.TrimSpace(strings.TrimPrefix(envOr("SATIKSME_WEB_TELEGRAM_BOT_USERNAME", ""), "@")),
		SatiksmeWebTelegramClientID:               strings.TrimSpace(envOr("SATIKSME_WEB_TELEGRAM_CLIENT_ID", "")),
		SatiksmeWebTelegramClientSecretFile:       strings.TrimSpace(envOr("SATIKSME_WEB_TELEGRAM_CLIENT_SECRET_FILE", "")),
		SatiksmeWebTelegramAuthMaxAgeSec:          authMaxAge,
		SatiksmeWebTelegramAuthStateTTLSec:        authStateTTL,
		SatiksmeWebBundleDir:                      strings.TrimSpace(envOr("SATIKSME_WEB_BUNDLE_DIR", "")),
		SatiksmeWebLiveSnapshotDir:                strings.TrimSpace(envOr("SATIKSME_WEB_LIVE_SNAPSHOT_DIR", "")),
		SatiksmeWebLiveViewerHeartbeatEnabled:     webLiveViewerHeartbeatEnabled,
		SatiksmeWebSpacetimeEnabled:               webSpacetimeEnabled,
		SatiksmeWebSpacetimeHost:                  strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_WEB_SPACETIME_HOST", "")), "/"),
		SatiksmeWebSpacetimeDatabase:              strings.TrimSpace(envOr("SATIKSME_WEB_SPACETIME_DATABASE", "")),
		SatiksmeWebSpacetimeOIDCIssuer:            strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_WEB_SPACETIME_OIDC_ISSUER", "")), "/"),
		SatiksmeWebSpacetimeOIDCAudience:          strings.TrimSpace(envOr("SATIKSME_WEB_SPACETIME_OIDC_AUDIENCE", "satiksme-bot-web")),
		SatiksmeWebSpacetimeJWTPrivateKeyFile:     strings.TrimSpace(envOr("SATIKSME_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE", "")),
		SatiksmeWebSpacetimeTokenTTLSec:           webSpacetimeTokenTTLSec,
		SatiksmeWebSpacetimeDirectOnly:            webSpacetimeDirectOnly,
		SatiksmeRuntimeSpacetimeEnabled:           runtimeSpacetimeEnabled,
		SatiksmeRuntimeSpacetimeHost:              strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_RUNTIME_SPACETIME_HOST", "")), "/"),
		SatiksmeRuntimeSpacetimeDatabase:          strings.TrimSpace(envOr("SATIKSME_RUNTIME_SPACETIME_DATABASE", "")),
		SatiksmeRuntimeSpacetimeOIDCIssuer:        strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_RUNTIME_SPACETIME_OIDC_ISSUER", "")), "/"),
		SatiksmeRuntimeSpacetimeOIDCAudience:      strings.TrimSpace(envOr("SATIKSME_RUNTIME_SPACETIME_OIDC_AUDIENCE", "")),
		SatiksmeRuntimeSpacetimeJWTPrivateKeyFile: strings.TrimSpace(envOr("SATIKSME_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE", "")),
		SatiksmeRuntimeSpacetimeTokenTTLSec:       runtimeSpacetimeTokenTTLSec,
		SatiksmeRuntimeSpacetimeServiceSubject:    strings.TrimSpace(envOr("SATIKSME_RUNTIME_SPACETIME_SERVICE_SUBJECT", "service:satiksme-bot")),
		SatiksmeRuntimeSpacetimeServiceRoles:      parseCSV(envOr("SATIKSME_RUNTIME_SPACETIME_SERVICE_ROLES", "satiksme_service")),
		SatiksmeLiveViewerWindowSec:               liveViewerWindowSec,
		SatiksmeLiveViewerGraceSec:                liveViewerGraceSec,
		SatiksmeLiveTransportPollBaseSec:          liveTransportPollBaseSec,
		SatiksmeLiveTransportPollMaxUnchangedSec:  liveTransportPollMaxUnchangedSec,
		SatiksmeChatAnalyzerEnabled:               chatAnalyzerEnabled,
		SatiksmeChatAnalyzerAPIID:                 chatAnalyzerAPIID,
		SatiksmeChatAnalyzerAPIHash:               strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_API_HASH", "")),
		SatiksmeChatAnalyzerSessionFile:           strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_SESSION_FILE", "./state/chat-analyzer.session")),
		SatiksmeChatAnalyzerChatID:                strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_CHAT_ID", "")),
		SatiksmeChatAnalyzerPollInterval:          chatAnalyzerPollInterval,
		SatiksmeChatAnalyzerBatchLimit:            chatAnalyzerBatchLimit,
		SatiksmeChatAnalyzerMinConfidence:         chatAnalyzerMinConfidence,
		SatiksmeChatAnalyzerDryRun:                chatAnalyzerDryRun,
		SatiksmeChatAnalyzerProcessStartMinute:    chatAnalyzerProcessStart,
		SatiksmeChatAnalyzerProcessEndMinute:      chatAnalyzerProcessEnd,
		SatiksmeChatAnalyzerProcessInterval:       chatAnalyzerProcessInterval,
		SatiksmeChatAnalyzerModelProvider:         chatAnalyzerModelProvider,
		SatiksmeChatAnalyzerModelBaseURL:          strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_MODEL_BASE_URL", defaultModelBaseURL)), "/"),
		SatiksmeChatAnalyzerModelName:             strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_MODEL_NAME", defaultModelName)),
		SatiksmeChatAnalyzerModelAPIKey:           chatAnalyzerModelAPIKey,
		SatiksmeChatAnalyzerGoogleAPIKey:          chatAnalyzerGoogleAPIKey,
		SatiksmeChatAnalyzerGoogleModelAuto:       chatAnalyzerGoogleModelAuto,
		SatiksmeChatAnalyzerGoogleModelsURL:       strings.TrimRight(strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_GOOGLE_MODELS_URL", "https://generativelanguage.googleapis.com/v1beta/models")), "/"),
		SatiksmeChatAnalyzerGoogleModelPolicy:     strings.TrimSpace(envOr("SATIKSME_CHAT_ANALYZER_GOOGLE_MODEL_POLICY", "free_rpd_parameter")),
		SatiksmeChatAnalyzerModelTimeout:          chatAnalyzerModelTimeout,
		SatiksmeChatAnalyzerModelNativeOllama:     chatAnalyzerModelNativeOllama,
		SatiksmeChatAnalyzerModelCallDelay:        chatAnalyzerModelCallDelay,
		SatiksmeChatAnalyzerRetryBaseDelay:        chatAnalyzerRetryBaseDelay,
		SatiksmeChatAnalyzerRetryMaxDelay:         chatAnalyzerRetryMaxDelay,
		ReportDumpChat:                            strings.TrimSpace(envOr("REPORT_DUMP_CHAT", "@satiksme_bot_reports")),
		ReportsChannelURL:                         strings.TrimSpace(envOr("REPORTS_CHANNEL_URL", "")),
		CatalogMirrorDir:                          envOr("SATIKSME_CATALOG_MIRROR_DIR", "./data/catalog/source"),
		CatalogOutputPath:                         envOr("SATIKSME_CATALOG_OUTPUT_PATH", "./data/catalog/generated/catalog.json"),
		CatalogRefreshHours:                       refreshHours,
		CleanupIntervalMinutes:                    cleanupIntervalMinutes,
		LiveVehiclesSourceURL:                     strings.TrimSpace(envOr("SATIKSME_LIVE_VEHICLES_SOURCE_URL", "https://www.saraksti.lv/gpsdata.ashx?gps")),
		SourceStopsURL:                            envOr("SATIKSME_SOURCE_STOPS_URL", "https://saraksti.rigassatiksme.lv/riga/stops.txt"),
		SourceRoutesURL:                           envOr("SATIKSME_SOURCE_ROUTES_URL", "https://saraksti.rigassatiksme.lv/riga/routes.txt"),
		SourceGTFSURL:                             envOr("SATIKSME_SOURCE_GTFS_URL", "https://data.gov.lv/dati/dataset/6d78358a-0095-4ce3-b119-6cde5d0ac54f/resource/c576c770-a01b-49b0-bdc4-0005a1ec5838/download/marsrutusaraksti02_2026.zip"),
	}

	if cfg.HTTPTimeoutSec <= cfg.LongPollTimeout {
		cfg.HTTPTimeoutSec = cfg.LongPollTimeout + 10
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return Config{}, fmt.Errorf("invalid TZ: %w", err)
	}
	if cfg.SatiksmeWebPort <= 0 || cfg.SatiksmeWebPort > 65535 {
		return Config{}, fmt.Errorf("SATIKSME_WEB_PORT must be between 1 and 65535, got %d", cfg.SatiksmeWebPort)
	}
	if cfg.SatiksmeWebTelegramAuthMaxAgeSec <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_WEB_TELEGRAM_AUTH_MAX_AGE_SEC must be positive")
	}
	if cfg.SatiksmeWebTelegramAuthStateTTLSec <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_WEB_TELEGRAM_AUTH_STATE_TTL_SEC must be positive")
	}
	if cfg.SatiksmeWebBundleDir == "" {
		cfg.SatiksmeWebBundleDir = defaultSatiksmeWebBundleDir(cfg.CatalogOutputPath)
	}
	if cfg.SatiksmeWebLiveSnapshotDir == "" {
		cfg.SatiksmeWebLiveSnapshotDir = filepath.Join(cfg.SatiksmeWebBundleDir, "transport", "live")
	}
	if cfg.CatalogRefreshHours <= 0 {
		cfg.CatalogRefreshHours = 24
	}
	if cfg.CleanupIntervalMinutes <= 0 {
		cfg.CleanupIntervalMinutes = 10
	}
	if cfg.SatiksmeLiveViewerWindowSec <= 0 {
		cfg.SatiksmeLiveViewerWindowSec = 75
	}
	if cfg.SatiksmeLiveViewerGraceSec <= 0 {
		cfg.SatiksmeLiveViewerGraceSec = 10
	}
	if cfg.SatiksmeLiveTransportPollBaseSec <= 0 {
		cfg.SatiksmeLiveTransportPollBaseSec = 5
	}
	if cfg.SatiksmeLiveTransportPollMaxUnchangedSec < cfg.SatiksmeLiveTransportPollBaseSec {
		cfg.SatiksmeLiveTransportPollMaxUnchangedSec = cfg.SatiksmeLiveTransportPollBaseSec
	}
	if cfg.SatiksmeChatAnalyzerBatchLimit <= 0 {
		cfg.SatiksmeChatAnalyzerBatchLimit = 25
	}
	if cfg.SatiksmeChatAnalyzerMinConfidence <= 0 || cfg.SatiksmeChatAnalyzerMinConfidence > 1 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_MIN_CONFIDENCE must be between 0 and 1")
	}
	if cfg.SatiksmeChatAnalyzerPollInterval <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_POLL_INTERVAL must be positive")
	}
	if cfg.SatiksmeChatAnalyzerProcessInterval <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_PROCESS_INTERVAL must be positive")
	}
	if cfg.SatiksmeChatAnalyzerProcessInterval < 5*time.Minute {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_PROCESS_INTERVAL must be at least 5m")
	}
	switch cfg.SatiksmeChatAnalyzerModelProvider {
	case "google", "openrouter", "openai", "ollama":
	default:
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_MODEL_PROVIDER must be one of google, openrouter, openai, ollama")
	}
	if cfg.SatiksmeChatAnalyzerModelProvider != "google" {
		cfg.SatiksmeChatAnalyzerGoogleModelAuto = false
	} else {
		modelBaseLower := strings.ToLower(cfg.SatiksmeChatAnalyzerModelBaseURL)
		if strings.Contains(modelBaseLower, "openrouter.ai") || strings.Contains(modelBaseLower, "ollama") || strings.Contains(modelBaseLower, "satiksme_qwen") {
			cfg.SatiksmeChatAnalyzerModelBaseURL = defaultModelBaseURL
		}
		modelNameLower := strings.ToLower(cfg.SatiksmeChatAnalyzerModelName)
		if strings.Contains(modelNameLower, "openrouter") || strings.Contains(modelNameLower, "qwen") {
			cfg.SatiksmeChatAnalyzerModelName = "auto"
			cfg.SatiksmeChatAnalyzerGoogleModelAuto = true
		}
	}
	if cfg.SatiksmeChatAnalyzerModelTimeout <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_MODEL_TIMEOUT must be positive")
	}
	if cfg.SatiksmeChatAnalyzerModelCallDelay < 0 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_MODEL_CALL_DELAY must not be negative")
	}
	if cfg.SatiksmeChatAnalyzerRetryBaseDelay <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_RETRY_BASE_DELAY must be positive")
	}
	if cfg.SatiksmeChatAnalyzerRetryMaxDelay <= 0 {
		return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_RETRY_MAX_DELAY must be positive")
	}
	if cfg.SatiksmeChatAnalyzerRetryMaxDelay < cfg.SatiksmeChatAnalyzerRetryBaseDelay {
		cfg.SatiksmeChatAnalyzerRetryMaxDelay = cfg.SatiksmeChatAnalyzerRetryBaseDelay
	}
	if cfg.SatiksmeChatAnalyzerEnabled {
		if cfg.SatiksmeChatAnalyzerAPIID <= 0 {
			return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_API_ID is required when SATIKSME_CHAT_ANALYZER_ENABLED=true")
		}
		if cfg.SatiksmeChatAnalyzerAPIHash == "" {
			return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_API_HASH is required when SATIKSME_CHAT_ANALYZER_ENABLED=true")
		}
		if cfg.SatiksmeChatAnalyzerSessionFile == "" {
			return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_SESSION_FILE is required when SATIKSME_CHAT_ANALYZER_ENABLED=true")
		}
		if cfg.SatiksmeChatAnalyzerChatID == "" {
			return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_CHAT_ID is required when SATIKSME_CHAT_ANALYZER_ENABLED=true")
		}
		if cfg.SatiksmeChatAnalyzerModelBaseURL == "" {
			return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_MODEL_BASE_URL is required when SATIKSME_CHAT_ANALYZER_ENABLED=true")
		}
		if cfg.SatiksmeChatAnalyzerModelName == "" {
			return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_MODEL_NAME is required when SATIKSME_CHAT_ANALYZER_ENABLED=true")
		}
		if cfg.SatiksmeChatAnalyzerModelProvider == "google" {
			if cfg.SatiksmeChatAnalyzerModelAPIKey == "" {
				return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY or SATIKSME_CHAT_ANALYZER_MODEL_API_KEY is required when SATIKSME_CHAT_ANALYZER_ENABLED=true and provider is google")
			}
			if cfg.SatiksmeChatAnalyzerGoogleModelAuto && cfg.SatiksmeChatAnalyzerGoogleModelsURL == "" {
				return Config{}, fmt.Errorf("SATIKSME_CHAT_ANALYZER_GOOGLE_MODELS_URL is required when Google model auto-selection is enabled")
			}
		}
	}
	if cfg.ReportsChannelURL == "" && strings.HasPrefix(cfg.ReportDumpChat, "@") {
		cfg.ReportsChannelURL = "https://t.me/" + strings.TrimPrefix(cfg.ReportDumpChat, "@")
	}
	cfg.CatalogMirrorDir = filepath.Clean(cfg.CatalogMirrorDir)
	cfg.CatalogOutputPath = filepath.Clean(cfg.CatalogOutputPath)
	cfg.SatiksmeWebBundleDir = filepath.Clean(cfg.SatiksmeWebBundleDir)
	cfg.SatiksmeWebLiveSnapshotDir = filepath.Clean(cfg.SatiksmeWebLiveSnapshotDir)
	if cfg.SatiksmeWebSpacetimeEnabled {
		if !cfg.SatiksmeWebEnabled {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_ENABLED requires SATIKSME_WEB_ENABLED=true")
		}
		if !cfg.SatiksmeRuntimeSpacetimeEnabled {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_ENABLED requires SATIKSME_RUNTIME_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeWebSpacetimeHost == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_HOST is required when SATIKSME_WEB_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeWebSpacetimeDatabase == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_DATABASE is required when SATIKSME_WEB_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeWebSpacetimeJWTPrivateKeyFile == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE is required when SATIKSME_WEB_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeWebSpacetimeOIDCAudience == "" {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_OIDC_AUDIENCE is required when SATIKSME_WEB_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeWebSpacetimeTokenTTLSec <= 0 {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_TOKEN_TTL_SEC must be positive, got %d", cfg.SatiksmeWebSpacetimeTokenTTLSec)
		}
		if cfg.SatiksmeWebSpacetimeTokenTTLSec > 24*60*60 {
			return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_TOKEN_TTL_SEC must be at most 86400, got %d", cfg.SatiksmeWebSpacetimeTokenTTLSec)
		}
	}
	if cfg.SatiksmeWebSpacetimeDirectOnly && !cfg.SatiksmeWebSpacetimeEnabled {
		return Config{}, fmt.Errorf("SATIKSME_WEB_SPACETIME_DIRECT_ONLY requires SATIKSME_WEB_SPACETIME_ENABLED=true")
	}
	if cfg.SatiksmeRuntimeSpacetimeEnabled {
		if cfg.SatiksmeRuntimeSpacetimeHost == "" {
			cfg.SatiksmeRuntimeSpacetimeHost = cfg.SatiksmeWebSpacetimeHost
		}
		if cfg.SatiksmeRuntimeSpacetimeDatabase == "" {
			cfg.SatiksmeRuntimeSpacetimeDatabase = cfg.SatiksmeWebSpacetimeDatabase
		}
		if cfg.SatiksmeRuntimeSpacetimeOIDCIssuer == "" {
			cfg.SatiksmeRuntimeSpacetimeOIDCIssuer = cfg.SatiksmeWebSpacetimeOIDCIssuer
		}
		if cfg.SatiksmeRuntimeSpacetimeOIDCAudience == "" {
			cfg.SatiksmeRuntimeSpacetimeOIDCAudience = cfg.SatiksmeWebSpacetimeOIDCAudience
		}
		if cfg.SatiksmeRuntimeSpacetimeJWTPrivateKeyFile == "" {
			cfg.SatiksmeRuntimeSpacetimeJWTPrivateKeyFile = cfg.SatiksmeWebSpacetimeJWTPrivateKeyFile
		}
		if cfg.SatiksmeRuntimeSpacetimeHost == "" {
			return Config{}, fmt.Errorf("SATIKSME_RUNTIME_SPACETIME_HOST is required when SATIKSME_RUNTIME_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeRuntimeSpacetimeDatabase == "" {
			return Config{}, fmt.Errorf("SATIKSME_RUNTIME_SPACETIME_DATABASE is required when SATIKSME_RUNTIME_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeRuntimeSpacetimeJWTPrivateKeyFile == "" {
			return Config{}, fmt.Errorf("SATIKSME_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE is required when SATIKSME_RUNTIME_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeRuntimeSpacetimeOIDCAudience == "" {
			return Config{}, fmt.Errorf("SATIKSME_RUNTIME_SPACETIME_OIDC_AUDIENCE is required when SATIKSME_RUNTIME_SPACETIME_ENABLED=true")
		}
		if cfg.SatiksmeRuntimeSpacetimeTokenTTLSec <= 0 {
			return Config{}, fmt.Errorf("SATIKSME_RUNTIME_SPACETIME_TOKEN_TTL_SEC must be positive, got %d", cfg.SatiksmeRuntimeSpacetimeTokenTTLSec)
		}
		if cfg.SatiksmeRuntimeSpacetimeTokenTTLSec > 24*60*60 {
			return Config{}, fmt.Errorf("SATIKSME_RUNTIME_SPACETIME_TOKEN_TTL_SEC must be at most 86400, got %d", cfg.SatiksmeRuntimeSpacetimeTokenTTLSec)
		}
		if len(cfg.SatiksmeRuntimeSpacetimeServiceRoles) == 0 {
			cfg.SatiksmeRuntimeSpacetimeServiceRoles = []string{"satiksme_service"}
		}
	}
	return cfg, nil
}

func defaultSatiksmeWebBundleDir(catalogOutputPath string) string {
	clean := strings.TrimSpace(catalogOutputPath)
	if clean == "" {
		return "./data/public-bundles"
	}
	parent := filepath.Dir(clean)
	if parent == "." || parent == "" {
		return "./data/public-bundles"
	}
	return filepath.Join(filepath.Dir(parent), "public-bundles")
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer, got %q", key, v)
		}
		return n, nil
	}
	return fallback, nil
}

func envOrFloatStrict(key string, fallback float64) (float64, error) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%s must be a number, got %q", key, v)
		}
		return n, nil
	}
	return fallback, nil
}

func envOrDurationStrict(key string, fallback time.Duration) (time.Duration, error) {
	if v := os.Getenv(key); v != "" {
		clean := strings.TrimSpace(v)
		if clean == "" {
			return fallback, nil
		}
		d, err := time.ParseDuration(clean)
		if err == nil {
			return d, nil
		}
		seconds, intErr := strconv.Atoi(clean)
		if intErr != nil {
			return 0, fmt.Errorf("%s must be a duration like 5m or seconds, got %q", key, v)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	return fallback, nil
}

func envOrClockMinuteStrict(key, fallback string) (int, error) {
	raw := strings.TrimSpace(envOr(key, fallback))
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("%s must use HH:MM, got %q", key, raw)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, fmt.Errorf("%s hour must be an integer, got %q", key, raw)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, fmt.Errorf("%s minute must be an integer, got %q", key, raw)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("%s must be a valid 24-hour HH:MM time, got %q", key, raw)
	}
	return hour*60 + minute, nil
}

func envOrBoolStrict(key string, fallback bool) (bool, error) {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true, nil
		case "0", "false", "no", "off":
			return false, nil
		default:
			return false, fmt.Errorf("%s must be a boolean, got %q", key, v)
		}
	}
	return fallback, nil
}
