package config

import "testing"

func TestLoadParsesAllowlist(t *testing.T) {
	t.Setenv("CHATGPT_ALLOWED_TELEGRAM_IDS", "123, 456, nope")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := cfg.AllowedTelegramIDs[123]; !ok {
		t.Fatal("missing allowed id 123")
	}
	if _, ok := cfg.AllowedTelegramIDs[456]; !ok {
		t.Fatal("missing allowed id 456")
	}
	if _, ok := cfg.AllowedTelegramIDs[0]; ok {
		t.Fatal("unexpected zero id")
	}
}
