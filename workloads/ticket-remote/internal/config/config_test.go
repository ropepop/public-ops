package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadUsesPersistedActivePhoneBackend(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	if err := os.WriteFile(activeFile, []byte(`{"backendId":"pixel"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TICKET_REMOTE_PHONE_BACKENDS", "android-sim|Android simulator|http://sim:9388;pixel|Pixel|http://pixel:9388")
	t.Setenv("TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID", "android-sim")
	t.Setenv("TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE", activeFile)
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.BackendID != "pixel" || cfg.Phone.AttachName != "Pixel" || cfg.Phone.BaseURL != "http://pixel:9388" {
		t.Fatalf("active phone backend = %#v", cfg.Phone)
	}
	if cfg.Phone.DefaultBackendID != "android-sim" {
		t.Fatalf("default backend = %q", cfg.Phone.DefaultBackendID)
	}
	if len(cfg.Phone.Backends) != 2 {
		t.Fatalf("backends = %#v", cfg.Phone.Backends)
	}
	if cfg.SimulatorSetup.BackendID != "android-sim" || cfg.SimulatorSetup.ADBTarget != "ticket_android_sim:5555" {
		t.Fatalf("simulator setup config = %#v", cfg.SimulatorSetup)
	}
}

func TestWriteActivePhoneBackendID(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "state", "active-phone-backend.json")
	if err := WriteActivePhoneBackendID(activeFile, "android-sim"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TICKET_REMOTE_PHONE_BACKENDS", "android-sim|Android simulator|http://sim:9388;pixel|Pixel|http://pixel:9388")
	t.Setenv("TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE", activeFile)
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phone.BackendID != "android-sim" {
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

func TestCloudflareAccessAuthModeRequiresAccessConfig(t *testing.T) {
	t.Setenv("TICKET_REMOTE_AUTH_MODE", "cloudflare")

	if _, err := Load(); err == nil {
		t.Fatal("expected Cloudflare Access auth mode to require Access settings")
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
