package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BindAddr           string
	Port               int
	BotToken           string
	LongPollTimeout    int
	HTTPTimeout        time.Duration
	AllowedTelegramIDs map[int64]struct{}
	BrokerBaseURL      string
	DefaultProjectName string
	JobRetention       time.Duration
	OCREnabled         bool
	OCRPollInterval    time.Duration
	TesseractPath      string
	SpacetimeHost      string
	SpacetimeDatabase  string
	SpacetimeToken     string
	SpacetimeKeyFile   string
	SpacetimeIssuer    string
	SpacetimeAudience  string
	SpacetimeSubject   string
	SpacetimeRoles     []string
	SpacetimeTokenTTL  time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		BindAddr:           env("CHATGPT_BROKER_BIND_ADDR", "0.0.0.0"),
		Port:               envInt("CHATGPT_BROKER_PORT", 9348),
		BotToken:           strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		LongPollTimeout:    envInt("LONG_POLL_TIMEOUT", 30),
		HTTPTimeout:        envDuration("HTTP_TIMEOUT", 180*time.Second),
		AllowedTelegramIDs: parseInt64Set(env("CHATGPT_ALLOWED_TELEGRAM_IDS", "")),
		BrokerBaseURL:      strings.TrimRight(env("CHATGPT_BROKER_BASE_URL", "http://127.0.0.1:9348"), "/"),
		DefaultProjectName: strings.TrimSpace(env("CHATGPT_PROJECT_NAME", "")),
		JobRetention:       envDuration("CHATGPT_JOB_RETENTION", 24*time.Hour),
		OCREnabled:         envBool("CHATGPT_OCR_ENABLED", true),
		OCRPollInterval:    envDuration("CHATGPT_OCR_POLL_INTERVAL", 3*time.Second),
		TesseractPath:      strings.TrimSpace(env("CHATGPT_TESSERACT_PATH", "tesseract")),
		SpacetimeHost:      strings.TrimRight(env("CHATGPT_SPACETIME_HOST", "https://maincloud.spacetimedb.com"), "/"),
		SpacetimeDatabase:  strings.TrimSpace(env("CHATGPT_SPACETIME_DATABASE", "")),
		SpacetimeToken:     strings.TrimSpace(os.Getenv("CHATGPT_SPACETIME_BEARER_TOKEN")),
		SpacetimeKeyFile:   strings.TrimSpace(env("CHATGPT_SPACETIME_JWT_PRIVATE_KEY_FILE", "")),
		SpacetimeIssuer:    strings.TrimSpace(env("CHATGPT_SPACETIME_OIDC_ISSUER", "chatgpt-broker-runtime")),
		SpacetimeAudience:  strings.TrimSpace(env("CHATGPT_SPACETIME_OIDC_AUDIENCE", "spacetimedb")),
		SpacetimeSubject:   strings.TrimSpace(env("CHATGPT_SPACETIME_SERVICE_SUBJECT", "service:chatgpt-broker")),
		SpacetimeRoles:     parseStringList(env("CHATGPT_SPACETIME_SERVICE_ROLES", "")),
		SpacetimeTokenTTL:  envDurationSpecial("CHATGPT_SPACETIME_TOKEN_TTL", 5*time.Minute),
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("CHATGPT_BROKER_PORT out of range")
	}
	if cfg.HTTPTimeout <= 0 {
		return Config{}, fmt.Errorf("HTTP_TIMEOUT must be positive")
	}
	if cfg.JobRetention <= 0 {
		return Config{}, fmt.Errorf("CHATGPT_JOB_RETENTION must be positive")
	}
	if cfg.OCRPollInterval <= 0 {
		return Config{}, fmt.Errorf("CHATGPT_OCR_POLL_INTERVAL must be positive")
	}
	return cfg, nil
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
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

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDurationSpecial(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	if value == "never" {
		return -1
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseInt64Set(raw string) map[int64]struct{} {
	out := map[int64]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		parsed, err := strconv.ParseInt(part, 10, 64)
		if err == nil && parsed > 0 {
			out[parsed] = struct{}{}
		}
	}
	return out
}

func parseStringList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
