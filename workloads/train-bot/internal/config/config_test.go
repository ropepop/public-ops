package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withEnv(key, value string, fn func()) {
	old, had := os.LookupEnv(key)
	_ = os.Setenv(key, value)
	defer func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	}()
	fn()
}

func withRuntimeEnv(t *testing.T, fn func()) {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "runtime.key")
	writeRSAPrivateKey(t, keyPath)
	withEnv("BOT_TOKEN", "token", func() {
		withEnv("TRAIN_RUNTIME_SPACETIME_HOST", "https://stdb.example.test", func() {
			withEnv("TRAIN_RUNTIME_SPACETIME_DATABASE", "train-bot", func() {
				withEnv("TRAIN_RUNTIME_SPACETIME_JWT_PRIVATE_KEY_FILE", keyPath, func() {
					withEnv("TRAIN_RUNTIME_SPACETIME_OIDC_AUDIENCE", "train-bot-web", fn)
				})
			})
		})
	})
}

func TestLoadFailsWithoutBotToken(t *testing.T) {
	withEnv("BOT_TOKEN", "", func() {
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "BOT_TOKEN") {
			t.Fatalf("expected BOT_TOKEN error, got %v", err)
		}
	})
}

func TestLoadFailsOnInvalidIntegerEnv(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("LONG_POLL_TIMEOUT", "abc", func() {
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "LONG_POLL_TIMEOUT") {
				t.Fatalf("expected LONG_POLL_TIMEOUT parse error, got %v", err)
			}
		})
	})
}

func TestLoadFailsOnInvalidBooleanEnv(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("FEATURE_STATION_CHECKIN", "not-bool", func() {
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "FEATURE_STATION_CHECKIN") {
				t.Fatalf("expected FEATURE_STATION_CHECKIN parse error, got %v", err)
			}
		})
	})
}

func TestLoadFailsOnInvalidReportDumpChatID(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("REPORT_DUMP_CHAT_ID", "bad", func() {
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "REPORT_DUMP_CHAT_ID") {
				t.Fatalf("expected REPORT_DUMP_CHAT_ID parse error, got %v", err)
			}
		})
	})
}

func TestLoadRequiresRuntimeSpacetimeConfig(t *testing.T) {
	withEnv("BOT_TOKEN", "token", func() {
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "TRAIN_RUNTIME_SPACETIME_HOST") {
			t.Fatalf("expected runtime spacetime host error, got %v", err)
		}
	})
}

func TestLoadAcceptsRuntimeSpacetimeConfig(t *testing.T) {
	withRuntimeEnv(t, func() {
		cfg, err := Load()
		if err != nil {
			t.Fatalf("expected valid runtime config, got %v", err)
		}
		if cfg.TrainRuntimeSpacetimeHost != "https://stdb.example.test" {
			t.Fatalf("unexpected runtime host: %q", cfg.TrainRuntimeSpacetimeHost)
		}
		if cfg.TrainRuntimeSpacetimeDatabase != "train-bot" {
			t.Fatalf("unexpected runtime database: %q", cfg.TrainRuntimeSpacetimeDatabase)
		}
		if cfg.TrainRuntimeSpacetimeOIDCAudience != "train-bot-web" {
			t.Fatalf("unexpected runtime audience: %q", cfg.TrainRuntimeSpacetimeOIDCAudience)
		}
		if cfg.SingleInstanceLockPath != filepath.Join(".", "data", "train-bot.lock") {
			t.Fatalf("unexpected default lock path: %q", cfg.SingleInstanceLockPath)
		}
		if cfg.TrainWebPublicEdgeCacheStateFile != filepath.Join(".", "data", "train-bot.public-edge-cache.json") {
			t.Fatalf("unexpected edge cache state file: %q", cfg.TrainWebPublicEdgeCacheStateFile)
		}
		if cfg.TrainWebBundleDir != filepath.Join(".", "data", "public-bundles") {
			t.Fatalf("unexpected bundle dir: %q", cfg.TrainWebBundleDir)
		}
	})
}

func TestLoadUsesScheduleDirForDerivedPaths(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("SCHEDULE_DIR", "/tmp/train/runtime/data/schedules", func() {
			cfg, err := Load()
			if err != nil {
				t.Fatalf("expected valid config, got %v", err)
			}
			if cfg.SingleInstanceLockPath != "/tmp/train/runtime/data/train-bot.lock" {
				t.Fatalf("unexpected derived lock path: %q", cfg.SingleInstanceLockPath)
			}
			if cfg.TrainWebPublicEdgeCacheStateFile != "/tmp/train/runtime/data/train-bot.public-edge-cache.json" {
				t.Fatalf("unexpected derived edge cache file: %q", cfg.TrainWebPublicEdgeCacheStateFile)
			}
			if cfg.TrainWebBundleDir != "/tmp/train/runtime/data/public-bundles" {
				t.Fatalf("unexpected derived bundle dir: %q", cfg.TrainWebBundleDir)
			}
		})
	})
}

func TestLoadBumpsHTTPTimeoutAboveLongPoll(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("LONG_POLL_TIMEOUT", "30", func() {
			withEnv("HTTP_TIMEOUT_SEC", "30", func() {
				cfg, err := Load()
				if err != nil {
					t.Fatalf("expected valid config, got %v", err)
				}
				if cfg.HTTPTimeoutSec != 40 {
					t.Fatalf("expected HTTP timeout bump to 40, got %d", cfg.HTTPTimeoutSec)
				}
			})
		})
	})
}

func TestLoadRequiresTrainWebFieldsWhenEnabled(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("TRAIN_WEB_ENABLED", "true", func() {
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "TRAIN_WEB_PUBLIC_BASE_URL") {
				t.Fatalf("expected TRAIN_WEB_PUBLIC_BASE_URL error, got %v", err)
			}
		})
	})
}

func TestLoadRequiresTrainWebSpacetimeFieldsWhenWebEnabled(t *testing.T) {
	withRuntimeEnv(t, func() {
		secretPath := filepath.Join(t.TempDir(), "session.secret")
		if err := os.WriteFile(secretPath, []byte("session-secret-value"), 0o600); err != nil {
			t.Fatalf("write secret: %v", err)
		}
		withEnv("TRAIN_WEB_ENABLED", "true", func() {
			withEnv("TRAIN_WEB_PUBLIC_BASE_URL", "https://example.test/pixel-stack/train", func() {
				withEnv("TRAIN_WEB_SESSION_SECRET_FILE", secretPath, func() {
					_, err := Load()
					if err == nil || !strings.Contains(err.Error(), "TRAIN_WEB_SPACETIME_HOST") {
						t.Fatalf("expected TRAIN_WEB_SPACETIME_HOST error, got %v", err)
					}
				})
			})
		})
	})
}

func TestLoadAcceptsTrainWebConfigWhenEnabled(t *testing.T) {
	withRuntimeEnv(t, func() {
		dir := t.TempDir()
		secretPath := filepath.Join(dir, "session.secret")
		if err := os.WriteFile(secretPath, []byte("session-secret-value"), 0o600); err != nil {
			t.Fatalf("write secret: %v", err)
		}
		webKeyPath := filepath.Join(dir, "web.key")
		writeRSAPrivateKey(t, webKeyPath)
		withEnv("TRAIN_WEB_ENABLED", "true", func() {
			withEnv("TRAIN_WEB_PUBLIC_BASE_URL", "https://example.test/pixel-stack/train", func() {
				withEnv("TRAIN_WEB_SESSION_SECRET_FILE", secretPath, func() {
					withEnv("TRAIN_WEB_SPACETIME_HOST", "https://stdb.example.test", func() {
						withEnv("TRAIN_WEB_SPACETIME_DATABASE", "train-bot", func() {
							withEnv("TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE", webKeyPath, func() {
								cfg, err := Load()
								if err != nil {
									t.Fatalf("expected valid web config, got %v", err)
								}
								if !cfg.TrainWebEnabled {
									t.Fatalf("expected train web enabled")
								}
								if cfg.TrainWebSpacetimeHost != "https://stdb.example.test" {
									t.Fatalf("unexpected web spacetime host: %q", cfg.TrainWebSpacetimeHost)
								}
								if cfg.TrainWebSpacetimeTokenTTLSec != 86400 {
									t.Fatalf("unexpected web spacetime ttl: %d", cfg.TrainWebSpacetimeTokenTTLSec)
								}
							})
						})
					})
				})
			})
		})
	})
}

func TestLoadRejectsTestLoginWithoutWeb(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("TRAIN_WEB_TEST_LOGIN_ENABLED", "true", func() {
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "TRAIN_WEB_TEST_LOGIN_ENABLED requires TRAIN_WEB_ENABLED=true") {
				t.Fatalf("expected test login web gating error, got %v", err)
			}
		})
	})
}

func TestLoadRequiresTestLoginFieldsWhenEnabled(t *testing.T) {
	withRuntimeEnv(t, func() {
		dir := t.TempDir()
		sessionSecretPath := filepath.Join(dir, "session.secret")
		if err := os.WriteFile(sessionSecretPath, []byte("session-secret-value"), 0o600); err != nil {
			t.Fatalf("write session secret: %v", err)
		}
		webKeyPath := filepath.Join(dir, "web.key")
		writeRSAPrivateKey(t, webKeyPath)
		withEnv("TRAIN_WEB_ENABLED", "true", func() {
			withEnv("TRAIN_WEB_PUBLIC_BASE_URL", "https://example.test/pixel-stack/train", func() {
				withEnv("TRAIN_WEB_SESSION_SECRET_FILE", sessionSecretPath, func() {
					withEnv("TRAIN_WEB_SPACETIME_HOST", "https://stdb.example.test", func() {
						withEnv("TRAIN_WEB_SPACETIME_DATABASE", "train-bot", func() {
							withEnv("TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE", webKeyPath, func() {
								withEnv("TRAIN_WEB_TEST_LOGIN_ENABLED", "true", func() {
									_, err := Load()
									if err == nil || !strings.Contains(err.Error(), "TRAIN_WEB_TEST_USER_ID") {
										t.Fatalf("expected TRAIN_WEB_TEST_USER_ID error, got %v", err)
									}
								})
							})
						})
					})
				})
			})
		})
	})
}

func TestLoadAcceptsTestLoginConfigWhenEnabled(t *testing.T) {
	withRuntimeEnv(t, func() {
		dir := t.TempDir()
		sessionSecretPath := filepath.Join(dir, "session.secret")
		if err := os.WriteFile(sessionSecretPath, []byte("session-secret-value"), 0o600); err != nil {
			t.Fatalf("write session secret: %v", err)
		}
		testSecretPath := filepath.Join(dir, "test-ticket.secret")
		if err := os.WriteFile(testSecretPath, []byte("test-ticket-secret-value"), 0o600); err != nil {
			t.Fatalf("write test ticket secret: %v", err)
		}
		webKeyPath := filepath.Join(dir, "web.key")
		writeRSAPrivateKey(t, webKeyPath)
		withEnv("TRAIN_WEB_ENABLED", "true", func() {
			withEnv("TRAIN_WEB_PUBLIC_BASE_URL", "https://example.test/pixel-stack/train", func() {
				withEnv("TRAIN_WEB_SESSION_SECRET_FILE", sessionSecretPath, func() {
					withEnv("TRAIN_WEB_SPACETIME_HOST", "https://stdb.example.test", func() {
						withEnv("TRAIN_WEB_SPACETIME_DATABASE", "train-bot", func() {
							withEnv("TRAIN_WEB_SPACETIME_JWT_PRIVATE_KEY_FILE", webKeyPath, func() {
								withEnv("TRAIN_WEB_TEST_LOGIN_ENABLED", "true", func() {
									withEnv("TRAIN_WEB_TEST_USER_ID", "7001", func() {
										withEnv("TRAIN_WEB_TEST_TICKET_SECRET_FILE", testSecretPath, func() {
											withEnv("TRAIN_WEB_TEST_TICKET_TTL_SEC", "90", func() {
												cfg, err := Load()
												if err != nil {
													t.Fatalf("expected valid test login config, got %v", err)
												}
												if !cfg.TrainWebTestLoginEnabled {
													t.Fatalf("expected test login enabled")
												}
												if cfg.TrainWebTestUserID != 7001 {
													t.Fatalf("unexpected test user id: %d", cfg.TrainWebTestUserID)
												}
												if cfg.TrainWebTestTicketSecretFile != testSecretPath {
													t.Fatalf("unexpected test ticket secret file: %q", cfg.TrainWebTestTicketSecretFile)
												}
												if cfg.TrainWebTestTicketTTLSec != 90 {
													t.Fatalf("unexpected test ticket ttl: %d", cfg.TrainWebTestTicketTTLSec)
												}
											})
										})
									})
								})
							})
						})
					})
				})
			})
		})
	})
}

func TestLoadRejectsEdgeCacheWithoutWeb(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("TRAIN_WEB_PUBLIC_EDGE_CACHE_ENABLED", "true", func() {
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "TRAIN_WEB_PUBLIC_EDGE_CACHE_ENABLED") {
				t.Fatalf("expected edge cache gating error, got %v", err)
			}
		})
	})
}

func TestLoadAcceptsDisabledExternalTrainMap(t *testing.T) {
	withRuntimeEnv(t, func() {
		withEnv("EXTERNAL_TRAINMAP_ENABLED", "false", func() {
			cfg, err := Load()
			if err != nil {
				t.Fatalf("expected valid config with external map disabled, got %v", err)
			}
			if cfg.ExternalTrainMapEnabled {
				t.Fatalf("expected external train map to be disabled")
			}
		})
	})
}

func writeRSAPrivateKey(t *testing.T, path string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	bytes := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: bytes}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}
