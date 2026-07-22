package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateSessionAtomicallyPreservesCurrentSessionOnFailure(t *testing.T) {
	target := filepath.Join(t.TempDir(), "chat.session")
	if err := os.WriteFile(target, []byte("working-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("authorization rejected")
	err := updateSessionAtomically(target, func(staged string) error {
		if err := os.WriteFile(staged, []byte("partial-session"), 0o600); err != nil {
			t.Fatal(err)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("updateSessionAtomically() error = %v, want %v", err, wantErr)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "working-session" {
		t.Fatalf("current session = %q, want preserved working session", got)
	}
}

func TestUpdateSessionAtomicallyPromotesValidatedSessionWithPrivateMode(t *testing.T) {
	target := filepath.Join(t.TempDir(), "chat.session")
	if err := updateSessionAtomically(target, func(staged string) error {
		return os.WriteFile(staged, []byte("authorized-session"), 0o644)
	}); err != nil {
		t.Fatalf("updateSessionAtomically() error = %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "authorized-session" {
		t.Fatalf("promoted session = %q", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if gotMode := info.Mode().Perm(); gotMode != 0o600 {
		t.Fatalf("session mode = %o, want 600", gotMode)
	}
}

func TestUpdateSessionAtomicallyRejectsEmptyAuthorizedSession(t *testing.T) {
	target := filepath.Join(t.TempDir(), "chat.session")
	if err := os.WriteFile(target, []byte("working-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := updateSessionAtomically(target, func(staged string) error {
		return os.WriteFile(staged, nil, 0o600)
	})
	if err == nil {
		t.Fatal("updateSessionAtomically() error = nil, want empty session rejection")
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "working-session" {
		t.Fatalf("current session = %q, want preserved working session", got)
	}
}

func TestMemorySessionStorageNeverWritesBackToDisk(t *testing.T) {
	target := filepath.Join(t.TempDir(), "chat.session")
	if err := os.WriteFile(target, []byte("disk-session"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage := &memorySessionStorage{data: []byte("disk-session")}
	if err := storage.StoreSession(context.Background(), []byte("connection-refresh")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "disk-session" {
		t.Fatalf("validation storage changed disk session: %q", got)
	}
}

func TestSecretFileEnvironmentReadsPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api-hash")
	if err := os.WriteFile(path, []byte("private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_ANALYZER_SECRET_FILE", path)
	got, err := secretFileEnvironment("TEST_ANALYZER_SECRET_FILE")
	if err != nil {
		t.Fatal(err)
	}
	if got != "private-value" {
		t.Fatalf("secret value = %q", got)
	}
}
