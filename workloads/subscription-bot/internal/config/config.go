package config

import (
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BotToken               string
	TelegramBotUsername    string
	HTTPTimeoutSec         int
	LongPollTimeout        int
	E2EMode                bool
	WebEnabled             bool
	WebShellEnabled        bool
	WebBindAddr            string
	WebPort                int
	WebPublicBaseURL       string
	SessionSecretFile      string
	TelegramAuthMaxAgeSec  int
	DBPath                 string
	SingleInstanceLockPath string
	Timezone               string
	PlatformFeeBps         int
	GraceDays              int
	RenewalLeadDays        int
	ReminderDays           []int
	PaymentProvider        string
	DefaultPayAsset        string
	DefaultPayNetwork      string
	AllowedPayAssets       []string
	AllowedPayNetworks     []string
	NowPaymentsAPIBaseURL  string
	NowPaymentsAPIKey      string
	NowPaymentsIPNSecret   string
	RequiredConfirmations  int
	OperatorIDs            map[int64]struct{}
	QuoteTTL               time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		BotToken:               strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		TelegramBotUsername:    strings.TrimSpace(getenvString("SUBSCRIPTION_BOT_TELEGRAM_BOT_USERNAME", getenvString("SUBSCRIPTION_BOT_BOT_USERNAME", ""))),
		HTTPTimeoutSec:         getenvInt("HTTP_TIMEOUT_SEC", 20),
		LongPollTimeout:        getenvInt("LONG_POLL_TIMEOUT", 30),
		E2EMode:                getenvBool("SUBSCRIPTION_BOT_E2E_MODE", false),
		WebEnabled:             getenvBool("SUBSCRIPTION_BOT_WEB_ENABLED", true),
		WebShellEnabled:        getenvBool("SUBSCRIPTION_BOT_WEB_SHELL_ENABLED", true),
		WebBindAddr:            getenvString("SUBSCRIPTION_BOT_WEB_BIND_ADDR", "127.0.0.1"),
		WebPort:                getenvInt("SUBSCRIPTION_BOT_WEB_PORT", 9320),
		WebPublicBaseURL:       strings.TrimRight(getenvString("SUBSCRIPTION_BOT_WEB_PUBLIC_BASE_URL", "https://farel-subscription-bot.jolkins.id.lv/pixel-stack/subscription"), "/"),
		SessionSecretFile:      getenvString("SUBSCRIPTION_BOT_SESSION_SECRET_FILE", "./state/subscription-bot.session.secret"),
		TelegramAuthMaxAgeSec:  getenvInt("SUBSCRIPTION_BOT_TELEGRAM_AUTH_MAX_AGE_SEC", 300),
		DBPath:                 getenvString("SUBSCRIPTION_BOT_DB_PATH", "./state/subscription_bot.db"),
		SingleInstanceLockPath: getenvString("SUBSCRIPTION_BOT_SINGLE_INSTANCE_LOCK_PATH", "./state/subscription-bot.instance.lock"),
		Timezone:               getenvString("TZ", "Europe/Riga"),
		PlatformFeeBps:         getenvInt("SUBSCRIPTION_BOT_PLATFORM_FEE_BPS", 1000),
		GraceDays:              getenvInt("SUBSCRIPTION_BOT_GRACE_DAYS", 3),
		RenewalLeadDays:        getenvInt("SUBSCRIPTION_BOT_RENEWAL_LEAD_DAYS", 7),
		ReminderDays:           parseReminderDays(getenvString("SUBSCRIPTION_BOT_REMINDER_DAYS", "7,3,1")),
		PaymentProvider:        strings.TrimSpace(getenvString("SUBSCRIPTION_BOT_PAYMENT_PROVIDER", "sandbox")),
		DefaultPayAsset:        strings.ToUpper(strings.TrimSpace(getenvString("SUBSCRIPTION_BOT_DEFAULT_PAY_ASSET", "USDC"))),
		DefaultPayNetwork:      strings.TrimSpace(getenvString("SUBSCRIPTION_BOT_DEFAULT_PAY_NETWORK", "solana")),
		AllowedPayAssets:       parseCSVUpper(getenvString("SUBSCRIPTION_BOT_ALLOWED_PAY_ASSETS", "USDC,USDT,SOL,ETH,BTC")),
		AllowedPayNetworks:     parseCSVLower(getenvString("SUBSCRIPTION_BOT_ALLOWED_PAY_NETWORKS", "solana,tron,base,bitcoin")),
		NowPaymentsAPIBaseURL:  strings.TrimRight(getenvString("SUBSCRIPTION_BOT_NOWPAYMENTS_API_BASE_URL", "https://api.nowpayments.io"), "/"),
		NowPaymentsAPIKey:      strings.TrimSpace(getenvString("SUBSCRIPTION_BOT_NOWPAYMENTS_API_KEY", "")),
		NowPaymentsIPNSecret:   strings.TrimSpace(getenvString("SUBSCRIPTION_BOT_NOWPAYMENTS_IPN_SECRET", "")),
		RequiredConfirmations:  getenvInt("SUBSCRIPTION_BOT_REQUIRED_CONFIRMATIONS", 3),
		OperatorIDs:            parseOperatorIDs(getenvString("SUBSCRIPTION_BOT_OPERATOR_IDS", "")),
		QuoteTTL:               time.Duration(getenvInt("SUBSCRIPTION_BOT_QUOTE_TTL_SEC", 900)) * time.Second,
	}

	if cfg.BotToken == "" {
		return Config{}, fmt.Errorf("BOT_TOKEN is required")
	}
	if cfg.HTTPTimeoutSec <= cfg.LongPollTimeout {
		cfg.HTTPTimeoutSec = cfg.LongPollTimeout + 10
	}
	if cfg.PlatformFeeBps < 0 {
		return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_PLATFORM_FEE_BPS must be >= 0")
	}
	if cfg.GraceDays < 0 {
		return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_GRACE_DAYS must be >= 0")
	}
	if cfg.RenewalLeadDays < 0 {
		return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_RENEWAL_LEAD_DAYS must be >= 0")
	}
	if cfg.RequiredConfirmations < 0 {
		return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_REQUIRED_CONFIRMATIONS must be >= 0")
	}
	if cfg.WebEnabled && len(strings.TrimSpace(cfg.SessionSecretFile)) == 0 {
		return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_SESSION_SECRET_FILE is required when web is enabled")
	}
	if cfg.WebEnabled && cfg.TelegramBotUsername == "" {
		return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_TELEGRAM_BOT_USERNAME is required when web is enabled")
	}
	if strings.EqualFold(cfg.PaymentProvider, "nowpayments") {
		if cfg.NowPaymentsAPIKey == "" {
			return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_NOWPAYMENTS_API_KEY is required when SUBSCRIPTION_BOT_PAYMENT_PROVIDER=nowpayments")
		}
		if cfg.NowPaymentsIPNSecret == "" {
			return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_NOWPAYMENTS_IPN_SECRET is required when SUBSCRIPTION_BOT_PAYMENT_PROVIDER=nowpayments")
		}
		if !cfg.WebEnabled {
			return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_WEB_ENABLED must be true when SUBSCRIPTION_BOT_PAYMENT_PROVIDER=nowpayments")
		}
		if len(cfg.OperatorIDs) == 0 {
			return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_OPERATOR_IDS must include at least one operator when SUBSCRIPTION_BOT_PAYMENT_PROVIDER=nowpayments")
		}
		if err := validatePublicBaseURL(cfg.WebPublicBaseURL); err != nil {
			return Config{}, fmt.Errorf("SUBSCRIPTION_BOT_WEB_PUBLIC_BASE_URL must be a public callback URL when SUBSCRIPTION_BOT_PAYMENT_PROVIDER=nowpayments: %w", err)
		}
	}
	return cfg, nil
}

func validatePublicBaseURL(raw string) error {
	parsed, err := neturl.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("expected https URL")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("missing host")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("localhost is not a public host")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return fmt.Errorf("loopback IP is not a public host")
	}
	return nil
}

func getenvString(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvInt(key string, fallback int) int {
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

func getenvBool(key string, fallback bool) bool {
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

func parseOperatorIDs(raw string) map[int64]struct{} {
	out := make(map[int64]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func parseReminderDays(raw string) []int {
	values := make([]int, 0, 3)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		day, err := strconv.Atoi(part)
		if err != nil {
			continue
		}
		if day >= 0 {
			values = append(values, day)
		}
	}
	if len(values) == 0 {
		return []int{7, 3, 1}
	}
	return values
}

func parseCSVUpper(raw string) []string {
	return parseCSV(raw, strings.ToUpper)
}

func parseCSVLower(raw string) []string {
	return parseCSV(raw, strings.ToLower)
}

func parseCSV(raw string, normalize func(string) string) []string {
	values := make([]string, 0)
	seen := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if normalize != nil {
			part = normalize(part)
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		values = append(values, part)
	}
	return values
}
