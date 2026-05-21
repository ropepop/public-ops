package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

const defaultOIDCIssuer = "https://auth.spacetimedb.com/oidc"
const defaultNoViewerStopDelay = 8 * time.Second

type Config struct {
	BindAddr            string
	Port                int
	Production          bool
	PublicBaseURL       string
	TicketID            string
	TicketDisplayName   string
	BootstrapAdminEmail string
	CookieName          string
	CookieTTL           time.Duration
	Access              auth.AccessConfig
	State               state.StoreConfig
	Phone               PhoneConfig
}

type PhoneConfig struct {
	BackendID         string
	AttachName        string
	BaseURL           string
	BrokerBaseURL     string
	Backends          []PhoneBackend
	DefaultBackendID  string
	ActiveBackendFile string
	RequestTimeout    time.Duration
	ReconnectMinDelay time.Duration
	ReconnectMaxDelay time.Duration
	NoViewerStopDelay time.Duration
}

type PhoneBackend struct {
	ID         string `json:"id"`
	AttachName string `json:"attachName"`
	BaseURL    string `json:"baseUrl"`
}

func (cfg PhoneConfig) RelayConfig() phone.RelayConfig {
	return phone.RelayConfig{
		BackendID:         cfg.BackendID,
		AttachName:        cfg.AttachName,
		BaseURL:           cfg.BaseURL,
		RequestTimeout:    cfg.RequestTimeout,
		ReconnectMinDelay: cfg.ReconnectMinDelay,
		ReconnectMaxDelay: cfg.ReconnectMaxDelay,
		NoViewerStopDelay: cfg.NoViewerStopDelay,
	}
}

func Load() (Config, error) {
	legacyPhone := PhoneBackend{
		ID:         getenv("TICKET_REMOTE_PHONE_BACKEND_ID", "pixel"),
		AttachName: getenv("TICKET_REMOTE_PHONE_ATTACH_NAME", "Pixel"),
		BaseURL:    strings.TrimRight(getenv("TICKET_REMOTE_PHONE_BASE_URL", "http://127.0.0.1:9388"), "/"),
	}
	phoneBackends := parsePhoneBackends(getenv("TICKET_REMOTE_PHONE_BACKENDS", ""))
	if len(phoneBackends) == 0 {
		phoneBackends = []PhoneBackend{legacyPhone}
	}
	defaultPhoneID := getenv("TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID", phoneBackends[0].ID)
	activeBackendFile := getenv("TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE", "/srv/ticket-remote/state/active-phone-backend.json")
	activePhoneID := strings.TrimSpace(readActivePhoneBackendID(activeBackendFile))
	if activePhoneID == "" {
		activePhoneID = defaultPhoneID
	}
	activePhone, ok := FindPhoneBackend(phoneBackends, activePhoneID)
	if !ok {
		activePhone, ok = FindPhoneBackend(phoneBackends, defaultPhoneID)
	}
	if !ok && len(phoneBackends) > 0 {
		activePhone = phoneBackends[0]
	}

	cfg := Config{
		BindAddr:            getenv("TICKET_REMOTE_BIND_ADDR", "0.0.0.0"),
		Port:                getenvInt("TICKET_REMOTE_PORT", 9338),
		Production:          getenvBool("TICKET_REMOTE_PRODUCTION", false),
		PublicBaseURL:       strings.TrimRight(getenv("TICKET_REMOTE_PUBLIC_BASE_URL", "https://ticket.jolkins.id.lv"), "/"),
		TicketID:            getenv("TICKET_REMOTE_TICKET_ID", state.DefaultTicketID),
		TicketDisplayName:   getenv("TICKET_REMOTE_TICKET_DISPLAY_NAME", state.DefaultTicketName),
		BootstrapAdminEmail: normalizeEmail(getenv("TICKET_REMOTE_BOOTSTRAP_ADMIN_EMAIL", "ticket@jolkins.id.lv")),
		CookieName:          getenv("TICKET_REMOTE_COOKIE_NAME", "ticket_remote_session"),
		CookieTTL:           getenvDuration("TICKET_REMOTE_COOKIE_TTL", 30*24*time.Hour),
		Access: auth.AccessConfig{
			Mode:              getenv("TICKET_REMOTE_AUTH_MODE", "spacetime"),
			DevEmail:          normalizeEmail(getenv("TICKET_REMOTE_DEV_EMAIL", "ticket@jolkins.id.lv")),
			HTTPTimeout:       getenvDuration("TICKET_REMOTE_AUTH_HTTP_TIMEOUT", auth.DefaultHTTPTimeout),
			AuthCookieName:    getenv("TICKET_REMOTE_AUTH_COOKIE_NAME", "ticket_remote_auth"),
			SessionSigningKey: getenv("TICKET_REMOTE_SESSION_SIGNING_KEY", ""),
			TeamDomain:        strings.TrimRight(getenv("TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN", ""), "/"),
			Audience:          getenv("TICKET_REMOTE_CF_ACCESS_AUDIENCE", ""),
			OIDCIssuer:        strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_AUTH_ISSUER", defaultOIDCIssuer), "/"),
			OIDCClientID:      getenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", ""),
			OIDCScope:         getenv("TICKET_REMOTE_SPACETIME_AUTH_SCOPE", "openid profile email"),
			OIDCRedirect:      strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_AUTH_REDIRECT_URL", ""), "/"),
		},
		State: state.StoreConfig{
			Backend:              getenv("TICKET_REMOTE_STATE_BACKEND", "auto"),
			TicketID:             getenv("TICKET_REMOTE_TICKET_ID", state.DefaultTicketID),
			SpacetimeHost:        strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_HOST", "https://maincloud.spacetimedb.com"), "/"),
			SpacetimeDatabase:    getenv("TICKET_REMOTE_SPACETIME_DATABASE", ""),
			SpacetimeBearerToken: getenv("TICKET_REMOTE_SPACETIME_BEARER_TOKEN", ""),
			SpacetimeIssuer:      getenv("TICKET_REMOTE_SPACETIME_OIDC_ISSUER", state.DefaultSpacetimeIssuer),
			SpacetimeAudience:    getenv("TICKET_REMOTE_SPACETIME_OIDC_AUDIENCE", state.DefaultSpacetimeAudience),
			SpacetimeKeyFile:     getenv("TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE", ""),
			ServiceSubject:       getenv("TICKET_REMOTE_SPACETIME_SERVICE_SUBJECT", state.DefaultSpacetimeServiceSubject),
			ServiceRoles:         splitCSV(getenv("TICKET_REMOTE_SPACETIME_SERVICE_ROLES", state.DefaultSpacetimeServiceRole)),
			TokenTTL:             getenvDuration("TICKET_REMOTE_SPACETIME_TOKEN_TTL", state.DefaultSpacetimeTokenTTL),
			HTTPTimeout:          getenvDuration("TICKET_REMOTE_SPACETIME_HTTP_TIMEOUT", state.DefaultSpacetimeHTTPTimeout),
			AuthIssuer:           strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_AUTH_ISSUER", "https://auth.spacetimedb.com/oidc"), "/"),
			AuthAudience:         getenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", ""),
		},
		Phone: PhoneConfig{
			BackendID:         activePhone.ID,
			AttachName:        activePhone.AttachName,
			BaseURL:           activePhone.BaseURL,
			BrokerBaseURL:     strings.TrimRight(getenv("TICKET_REMOTE_PHONE_BROKER_URL", ""), "/"),
			Backends:          phoneBackends,
			DefaultBackendID:  defaultPhoneID,
			ActiveBackendFile: activeBackendFile,
			RequestTimeout:    getenvDuration("TICKET_REMOTE_PHONE_REQUEST_TIMEOUT", phone.DefaultRequestTimeout),
			ReconnectMinDelay: getenvDuration("TICKET_REMOTE_PHONE_RECONNECT_MIN_DELAY", phone.DefaultReconnectMinDelay),
			ReconnectMaxDelay: getenvDuration("TICKET_REMOTE_PHONE_RECONNECT_MAX_DELAY", phone.DefaultReconnectMaxDelay),
			NoViewerStopDelay: getenvDuration("TICKET_REMOTE_PHONE_NO_VIEWER_STOP_DELAY", defaultNoViewerStopDelay),
		},
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("TICKET_REMOTE_PORT out of range: %d", cfg.Port)
	}
	if cfg.TicketID == "" {
		return Config{}, fmt.Errorf("TICKET_REMOTE_TICKET_ID is required")
	}
	if cfg.BootstrapAdminEmail == "" {
		return Config{}, fmt.Errorf("TICKET_REMOTE_BOOTSTRAP_ADMIN_EMAIL is required")
	}
	if cfg.Access.OIDCRedirect == "" {
		cfg.Access.OIDCRedirect = cfg.PublicBaseURL + "/auth/callback"
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Access.Mode)) {
	case "", "spacetime", "spacetimeauth", "oidc":
		if cfg.Access.OIDCClientID == "" {
			return Config{}, fmt.Errorf("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID is required when SpacetimeAuth is enabled")
		}
		if cfg.Access.OIDCIssuer == "" {
			return Config{}, fmt.Errorf("TICKET_REMOTE_SPACETIME_AUTH_ISSUER is required when SpacetimeAuth is enabled")
		}
		if strings.TrimSpace(cfg.Access.SessionSigningKey) == "" {
			return Config{}, fmt.Errorf("TICKET_REMOTE_SESSION_SIGNING_KEY is required when SpacetimeAuth is enabled")
		}
	case "cloudflare", "cloudflare-access", "cf-access":
		if cfg.Access.TeamDomain == "" {
			return Config{}, fmt.Errorf("TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN is required when Cloudflare Access auth is enabled")
		}
		if cfg.Access.Audience == "" {
			return Config{}, fmt.Errorf("TICKET_REMOTE_CF_ACCESS_AUDIENCE is required when Cloudflare Access auth is enabled")
		}
	case "dev", "development", "none":
	default:
		return Config{}, fmt.Errorf("unsupported TICKET_REMOTE_AUTH_MODE %q", cfg.Access.Mode)
	}
	if cfg.Phone.BaseURL == "" {
		return Config{}, fmt.Errorf("TICKET_REMOTE_PHONE_BASE_URL is required")
	}
	if cfg.Production {
		if err := validateProductionConfig(cfg); err != nil {
			return Config{}, err
		}
	}
	if len(cfg.Phone.Backends) == 0 {
		return Config{}, fmt.Errorf("at least one ticket phone backend is required")
	}
	return cfg, nil
}

func validateProductionConfig(cfg Config) error {
	mode := strings.ToLower(strings.TrimSpace(cfg.Access.Mode))
	switch mode {
	case "", "spacetime", "spacetimeauth", "oidc", "cloudflare", "cloudflare-access", "cf-access":
	case "dev", "development", "none":
		return fmt.Errorf("production auth mode %q is not allowed", cfg.Access.Mode)
	default:
		return fmt.Errorf("unsupported production auth mode %q", cfg.Access.Mode)
	}
	backend := strings.ToLower(strings.TrimSpace(cfg.State.Backend))
	if backend != "spacetime" && backend != "spacetimedb" {
		return fmt.Errorf("production state backend must be spacetime, got %q", cfg.State.Backend)
	}
	if strings.TrimSpace(cfg.State.SpacetimeDatabase) == "" {
		return fmt.Errorf("TICKET_REMOTE_SPACETIME_DATABASE is required in production")
	}
	if strings.TrimSpace(cfg.State.SpacetimeBearerToken) == "" && strings.TrimSpace(cfg.State.SpacetimeKeyFile) == "" {
		return fmt.Errorf("TICKET_REMOTE_SPACETIME_BEARER_TOKEN or TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE is required in production")
	}
	return nil
}

func FindPhoneBackend(backends []PhoneBackend, id string) (PhoneBackend, bool) {
	id = strings.TrimSpace(id)
	for _, backend := range backends {
		if backend.ID == id {
			return backend, true
		}
	}
	return PhoneBackend{}, false
}

func WriteActivePhoneBackendID(path string, backendID string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(map[string]string{
		"backendId": strings.TrimSpace(backendID),
		"updatedAt": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parsePhoneBackends(value string) []PhoneBackend {
	var out []PhoneBackend
	for _, entry := range strings.Split(value, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, "|")
		if len(parts) != 3 {
			continue
		}
		backend := PhoneBackend{
			ID:         strings.TrimSpace(parts[0]),
			AttachName: strings.TrimSpace(parts[1]),
			BaseURL:    strings.TrimRight(strings.TrimSpace(parts[2]), "/"),
		}
		if backend.ID == "" || backend.BaseURL == "" {
			continue
		}
		if backend.AttachName == "" {
			backend.AttachName = backend.ID
		}
		out = append(out, backend)
	}
	return out
}

func readActivePhoneBackendID(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var payload struct {
		BackendID string `json:"backendId"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.BackendID)
}

func getenv(key, fallback string) string {
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
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(value); err == nil {
		return parsed
	}
	if hours, err := strconv.Atoi(value); err == nil {
		return time.Duration(hours) * time.Hour
	}
	return fallback
}

func splitCSV(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if clean := strings.TrimSpace(item); clean != "" {
			out = append(out, clean)
		}
	}
	return out
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
