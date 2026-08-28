package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadUsesPersistedActivePhoneBackend(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	if err := os.WriteFile(activeFile, []byte(`{"backendId":"pixel"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TICKET_REMOTE_PHONE_BACKENDS", "pixel|Pixel|http://pixel:9388")
	t.Setenv("TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID", "pixel")
	t.Setenv("TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE", activeFile)
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.BackendID != "pixel" || cfg.Phone.AttachName != "Pixel" || cfg.Phone.BaseURL != "http://pixel:9388" {
		t.Fatalf("active phone backend = %#v", cfg.Phone)
	}
	if cfg.Phone.DefaultBackendID != "pixel" {
		t.Fatalf("default backend = %q", cfg.Phone.DefaultBackendID)
	}
	if len(cfg.Phone.Backends) != 1 {
		t.Fatalf("backends = %#v", cfg.Phone.Backends)
	}
	if _, ok := reflect.TypeOf(Config{}).FieldByName("Sim" + "ulatorSetup"); ok {
		t.Fatalf("Config must not expose retired device setup")
	}
}

func TestWriteActivePhoneBackendID(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "state", "active-phone-backend.json")
	if err := WriteActivePhoneBackendID(activeFile, "pixel"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TICKET_REMOTE_PHONE_BACKENDS", "pixel|Pixel|http://pixel:9388")
	t.Setenv("TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE", activeFile)
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.BackendID != "pixel" {
		t.Fatalf("active backend = %q", cfg.Phone.BackendID)
	}
}

func TestConfigHasNoPublicMediaPortConfig(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.BackendID == "" || cfg.Phone.BaseURL == "" {
		t.Fatalf("load failed to keep normal config: %#v", cfg.Phone)
	}
	if _, ok := reflect.TypeOf(Config{}).FieldByName("WebRTC"); ok {
		t.Fatalf("Config must not expose public media port settings")
	}
}

func TestExperimentalHDRTransformerIsPrivateOptionalConfig(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_HDR_TRANSFORMER_URL", "http://ticket_hdr_transformer:9352/")
	t.Setenv("TICKET_REMOTE_HDR_TRANSFORM_TIMEOUT", "1200ms")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExperimentalMedia.HDRTransformerURL != "http://ticket_hdr_transformer:9352" {
		t.Fatalf("HDR transformer URL = %q", cfg.ExperimentalMedia.HDRTransformerURL)
	}
	if cfg.ExperimentalMedia.TransformTimeout != 1200*time.Millisecond {
		t.Fatalf("HDR transformer timeout = %s", cfg.ExperimentalMedia.TransformTimeout)
	}
	if strings.Contains(cfg.ExperimentalMedia.HDRTransformerURL, "ticket.jolkins.id.lv") {
		t.Fatal("HDR transformer must not reuse the public Ticket origin")
	}
}

func TestExperimentalHDRTransformerTimeoutIsBounded(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_HDR_TRANSFORM_TIMEOUT", "30s")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TICKET_REMOTE_HDR_TRANSFORM_TIMEOUT") {
		t.Fatalf("expected bounded HDR transform timeout error, got %v", err)
	}
}

func TestLoadUsesDirectPhoneBridgeAndIgnoresRetiredBrokerSetting(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_PHONE_BACKENDS", "pixel|Pixel|http://ticket_phone_bridge:9388")
	t.Setenv("TICKET_REMOTE_PHONE_BASE_URL", "http://ticket_phone_bridge:9388")
	t.Setenv("TICKET_REMOTE_PHONE_"+"BROKER_URL", "http://phone_"+"broker:9398/")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.BaseURL != "http://ticket_phone_bridge:9388" {
		t.Fatalf("phone bridge URL = %q", cfg.Phone.BaseURL)
	}
	if _, ok := reflect.TypeOf(PhoneConfig{}).FieldByName("Broker" + "BaseURL"); ok {
		t.Fatal("phone config must not expose the retired phone broker")
	}
}

func TestDefaultPhoneNoViewerStopDelayCoversBrowserReload(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.NoViewerStopDelay < 60*time.Second {
		t.Fatalf("default no-viewer stop delay = %s, want enough grace for browser reloads", cfg.Phone.NoViewerStopDelay)
	}
	if cfg.Phone.NoViewerStopDelay > 90*time.Second {
		t.Fatalf("default no-viewer stop delay = %s, want encoder to cool down after the warm reconnect window", cfg.Phone.NoViewerStopDelay)
	}
}

func TestLoadUsesEuropeRigaAsDefaultPhoneTimeZone(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_PHONE_TIME_ZONE", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.TimeZone != "Europe/Riga" {
		t.Fatalf("phone time zone = %q, want Europe/Riga", cfg.Phone.TimeZone)
	}
}

func TestLoadAcceptsConfiguredPhoneTimeZone(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_PHONE_TIME_ZONE", "Europe/Tallinn")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.TimeZone != "Europe/Tallinn" {
		t.Fatalf("phone time zone = %q", cfg.Phone.TimeZone)
	}
}

func TestLoadRejectsInvalidPhoneTimeZone(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_PHONE_TIME_ZONE", "Mars/Olympus")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TICKET_REMOTE_PHONE_TIME_ZONE") {
		t.Fatalf("expected invalid phone time-zone error, got %v", err)
	}
}

func TestCloudflareAccessAuthModeRequiresAccessConfig(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "cloudflare")

	if _, err := Load(); err == nil {
		t.Fatal("expected Cloudflare Access auth mode to require Access settings")
	}
}

func TestSpacetimeAuthModeRequiresSessionSigningKey(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", "client_test")

	if _, err := Load(); err == nil {
		t.Fatal("expected SpacetimeAuth mode to require a session signing key")
	}
}

func TestSpacetimeAuthModeLoadsWithSessionSigningKey(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", "client_test")
	t.Setenv("TICKET_REMOTE_SESSION_SIGNING_KEY", "test-signing-key")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Access.SessionSigningKey != "test-signing-key" {
		t.Fatalf("session signing key was not loaded")
	}
}

func TestLoadParsesNoExpirySessionTTL(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_COOKIE_TTL", "never")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CookieTTL != DurationNever {
		t.Fatalf("cookie TTL = %s, want no-expiry sentinel", cfg.CookieTTL)
	}
}

func TestCloudflareAccessAuthModeLoads(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "cloudflare")
	t.Setenv("TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN", "team.example.cloudflareaccess.com")
	t.Setenv("TICKET_REMOTE_CF_ACCESS_AUDIENCE", "audience-tag")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Access.TeamDomain != "team.example.cloudflareaccess.com" || cfg.Access.Audience != "audience-tag" {
		t.Fatalf("access config = %#v", cfg.Access)
	}
}

func TestProductionModeRejectsDevAuth(t *testing.T) {
	t.Setenv("TICKET_REMOTE_PRODUCTION", "true")
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")
	t.Setenv("TICKET_REMOTE_STATE_BACKEND", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_DATABASE", "ticket_remote")
	t.Setenv("TICKET_REMOTE_SPACETIME_BEARER_TOKEN", "test-token")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production auth mode") {
		t.Fatalf("expected production auth rejection, got %v", err)
	}
}

func TestProductionModeRequiresSpacetimeState(t *testing.T) {
	t.Setenv("TICKET_REMOTE_PRODUCTION", "true")
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", "client_test")
	t.Setenv("TICKET_REMOTE_SESSION_SIGNING_KEY", "test-signing-key")
	t.Setenv("TICKET_REMOTE_STATE_BACKEND", "memory")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production state backend") {
		t.Fatalf("expected production state rejection, got %v", err)
	}
}

func setValidProductionSidecarEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("TICKET_REMOTE_PRODUCTION", "true")
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID", "client_test")
	t.Setenv("TICKET_REMOTE_SESSION_SIGNING_KEY", "test-signing-key")
	t.Setenv("TICKET_REMOTE_STATE_BACKEND", "spacetime")
	t.Setenv("TICKET_REMOTE_SPACETIME_DATABASE", "ticket_remote")
	t.Setenv("TICKET_REMOTE_SPACETIME_CLIENT_URL", "http://ticket_remote_spacetime_sidecar:9340")
	t.Setenv("TICKET_REMOTE_SPACETIME_SIDECAR_WRITE_TOKEN_FILE", "/run/secrets/ticket-remote-sidecar-write-token")
	t.Setenv("TICKET_REMOTE_SPACETIME_BEARER_TOKEN", "")
	t.Setenv("TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE", "")
}

func TestProductionModeRequiresSidecarWriteTokenFile(t *testing.T) {
	setValidProductionSidecarEnvironment(t)
	t.Setenv("TICKET_REMOTE_SPACETIME_SIDECAR_WRITE_TOKEN_FILE", "")
	// Direct Spacetime credentials must no longer satisfy the public service's
	// production write contract.
	t.Setenv("TICKET_REMOTE_SPACETIME_BEARER_TOKEN", "legacy-direct-token")
	t.Setenv("TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE", "/run/secrets/legacy-private-key")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TICKET_REMOTE_SPACETIME_SIDECAR_WRITE_TOKEN_FILE") {
		t.Fatalf("expected sidecar write-token-file requirement, got %v", err)
	}
}

func TestProductionModeLoadsSidecarOnlyWriteCredentials(t *testing.T) {
	setValidProductionSidecarEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.State.SpacetimeClientURL != "http://ticket_remote_spacetime_sidecar:9340" {
		t.Fatalf("sidecar URL = %q", cfg.State.SpacetimeClientURL)
	}
	if cfg.State.SpacetimeSidecarWriteTokenFile != "/run/secrets/ticket-remote-sidecar-write-token" {
		t.Fatalf("sidecar write-token file = %q", cfg.State.SpacetimeSidecarWriteTokenFile)
	}
}
