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
	"ticketremote/internal/state"
)

type Config struct {
	BindAddr            string
	Port                int
	PublicBaseURL       string
	TicketID            string
	TicketDisplayName   string
	BootstrapAdminEmail string
	CookieName          string
	CookieTTL           time.Duration
	Access              auth.AccessConfig
	State               state.StoreConfig
	Phone               PhoneConfig
	SimulatorSetup      SimulatorSetupConfig
}

type PhoneConfig struct {
	BackendID         string
	AttachName        string
	BaseURL           string
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

type SimulatorSetupConfig struct {
	BackendID string
	ADBTarget string
	ADBPath   string
	Timeout   time.Duration
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
		PublicBaseURL:       strings.TrimRight(getenv("TICKET_REMOTE_PUBLIC_BASE_URL", "https://ticket.jolkins.id.lv"), "/"),
		TicketID:            getenv("TICKET_REMOTE_TICKET_ID", "vivi-default"),
		TicketDisplayName:   getenv("TICKET_REMOTE_TICKET_DISPLAY_NAME", "ViVi timed ticket"),
		BootstrapAdminEmail: normalizeEmail(getenv("TICKET_REMOTE_BOOTSTRAP_ADMIN_EMAIL", "ticket@jolkins.id.lv")),
		CookieName:          getenv("TICKET_REMOTE_COOKIE_NAME", "ticket_remote_session"),
		CookieTTL:           getenvDuration("TICKET_REMOTE_COOKIE_TTL", 30*24*time.Hour),
		Access: auth.AccessConfig{
			Mode:     getenv("TICKET_REMOTE_AUTH_MODE", "spacetime"),
			DevEmail: normalizeEmail(getenv("TICKET_REMOTE_DEV_EMAIL", "ticket@jolkins.id.lv")),
			HTTPTimeout: getenvDuration(
				"TICKET_REMOTE_AUTH_HTTP_TIMEOUT",
				10*time.Second,
			),
			AuthCookieName: getenv("TICKET_REMOTE_AUTH_COOKIE_NAME", "ticket_remote_auth"),
			TeamDomain:     strings.TrimRight(getenv("TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN", ""), "/"),
			Audience:       getenv("TICKET_REMOTE_CF_ACCESS_AUDIENCE", ""),
			OIDCIssuer:     strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_AUTH_ISSUER", "https://auth.spacetimedb.com/oidc"), "/"),
			OIDCClientID:   getenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", ""),
			OIDCScope:      getenv("TICKET_REMOTE_SPACETIME_AUTH_SCOPE", "openid profile email"),
			OIDCRedirect:   strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_AUTH_REDIRECT_URL", ""), "/"),
		},
		State: state.StoreConfig{
			Backend:              getenv("TICKET_REMOTE_STATE_BACKEND", "auto"),
			TicketID:             getenv("TICKET_REMOTE_TICKET_ID", "vivi-default"),
			SpacetimeHost:        strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_HOST", "https://maincloud.spacetimedb.com"), "/"),
			SpacetimeDatabase:    getenv("TICKET_REMOTE_SPACETIME_DATABASE", ""),
			SpacetimeBearerToken: getenv("TICKET_REMOTE_SPACETIME_BEARER_TOKEN", ""),
			SpacetimeIssuer:      getenv("TICKET_REMOTE_SPACETIME_OIDC_ISSUER", "ticket-remote-runtime"),
			SpacetimeAudience:    getenv("TICKET_REMOTE_SPACETIME_OIDC_AUDIENCE", "spacetimedb"),
			SpacetimeKeyFile:     getenv("TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE", ""),
			ServiceSubject:       getenv("TICKET_REMOTE_SPACETIME_SERVICE_SUBJECT", "service:ticket-remote"),
			ServiceRoles:         splitCSV(getenv("TICKET_REMOTE_SPACETIME_SERVICE_ROLES", "ticketremote_service")),
			TokenTTL:             getenvDuration("TICKET_REMOTE_SPACETIME_TOKEN_TTL", 5*time.Minute),
			HTTPTimeout:          getenvDuration("TICKET_REMOTE_SPACETIME_HTTP_TIMEOUT", 10*time.Second),
			AuthIssuer:           strings.TrimRight(getenv("TICKET_REMOTE_SPACETIME_AUTH_ISSUER", "https://auth.spacetimedb.com/oidc"), "/"),
			AuthAudience:         getenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", ""),
		},
		Phone: PhoneConfig{
			BackendID:         activePhone.ID,
			AttachName:        activePhone.AttachName,
			BaseURL:           activePhone.BaseURL,
			Backends:          phoneBackends,
			DefaultBackendID:  defaultPhoneID,
			ActiveBackendFile: activeBackendFile,
			RequestTimeout:    getenvDuration("TICKET_REMOTE_PHONE_REQUEST_TIMEOUT", 10*time.Second),
			ReconnectMinDelay: getenvDuration("TICKET_REMOTE_PHONE_RECONNECT_MIN_DELAY", 500*time.Millisecond),
			ReconnectMaxDelay: getenvDuration("TICKET_REMOTE_PHONE_RECONNECT_MAX_DELAY", 5*time.Second),
			NoViewerStopDelay: getenvDuration("TICKET_REMOTE_PHONE_NO_VIEWER_STOP_DELAY", 20*time.Second),
		},
		SimulatorSetup: SimulatorSetupConfig{
			BackendID: getenv("TICKET_REMOTE_SIMULATOR_SETUP_BACKEND_ID", "android-sim"),
			ADBTarget: getenv("TICKET_REMOTE_SIMULATOR_SETUP_ADB_TARGET", "ticket_android_sim:5555"),
			ADBPath:   getenv("TICKET_REMOTE_SIMULATOR_SETUP_ADB_PATH", "adb"),
			Timeout:   getenvDuration("TICKET_REMOTE_SIMULATOR_SETUP_TIMEOUT", 8*time.Second),
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
	if len(cfg.Phone.Backends) == 0 {
		return Config{}, fmt.Errorf("at least one ticket phone backend is required")
	}
	if cfg.SimulatorSetup.BackendID == "" {
		return Config{}, fmt.Errorf("TICKET_REMOTE_SIMULATOR_SETUP_BACKEND_ID is required")
	}
	if cfg.SimulatorSetup.ADBTarget == "" {
		return Config{}, fmt.Errorf("TICKET_REMOTE_SIMULATOR_SETUP_ADB_TARGET is required")
	}
	if cfg.SimulatorSetup.ADBPath == "" {
		return Config{}, fmt.Errorf("TICKET_REMOTE_SIMULATOR_SETUP_ADB_PATH is required")
	}
	return cfg, nil
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
