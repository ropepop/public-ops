package config

import "testing"

func TestLoadBumpsHTTPTimeoutAboveLongPoll(t *testing.T) {
	t.Setenv("BOT_TOKEN", "test-token")
	t.Setenv("SUBSCRIPTION_BOT_WEB_ENABLED", "false")
	t.Setenv("LONG_POLL_TIMEOUT", "30")
	t.Setenv("HTTP_TIMEOUT_SEC", "30")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.HTTPTimeoutSec, 40; got != want {
		t.Fatalf("HTTPTimeoutSec = %d, want %d", got, want)
	}
}
