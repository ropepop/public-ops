package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var configEnvironmentKeys = []string{
	"QBITTORRENT_URL",
	"QBITTORRENT_USERNAME",
	"QBITTORRENT_PASSWORD_FILE",
	"DOWNLOAD_PATH",
	"SOFT_CAP_BYTES",
	"MIN_COMPLETED_AGE",
	"MIN_RATIO",
	"POLL_INTERVAL",
	"REQUEST_TIMEOUT",
	"HEALTH_ADDR",
	"HEALTH_MAX_AGE",
}

func TestLoadDefaultsToNoAuthentication(t *testing.T) {
	clearConfigEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Username != "" || cfg.PasswordFile != "" {
		t.Fatalf("default authentication = %q/%q, want disabled", cfg.Username, cfg.PasswordFile)
	}
	if cfg.SoftCapBytes != 24*1024*1024*1024 || cfg.MinAge != 24*time.Hour || cfg.MinRatio != 1 {
		t.Fatalf("unexpected policy defaults: %#v", cfg)
	}
}

func TestAuthenticationConfigurationMustBePaired(t *testing.T) {
	clearConfigEnvironment(t)
	t.Setenv("QBITTORRENT_USERNAME", "operator")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("Load() error = %v, want paired-auth error", err)
	}
}

func TestReadPasswordFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("test-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Username: "operator", PasswordFile: path}
	password, err := cfg.ReadPassword()
	if err != nil {
		t.Fatalf("ReadPassword() error = %v", err)
	}
	if password != "test-secret" {
		t.Fatalf("ReadPassword() = %q", password)
	}
}

func TestRejectsNonFiniteRatio(t *testing.T) {
	clearConfigEnvironment(t)
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("MIN_RATIO", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "finite") {
				t.Fatalf("Load() error = %v, want finite-ratio error", err)
			}
		})
	}
	if err := (Config{
		QBitURL:        "http://qbit",
		DownloadPath:   "/downloads",
		SoftCapBytes:   1,
		MinRatio:       math.NaN(),
		PollInterval:   time.Second,
		RequestTimeout: time.Second,
		HealthAddr:     ":1",
		HealthMaxAge:   time.Second,
	}).Validate(); err == nil {
		t.Fatal("Validate() accepted NaN ratio")
	}
}

func clearConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range configEnvironmentKeys {
		t.Setenv(key, "")
	}
}
