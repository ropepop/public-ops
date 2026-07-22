package chatanalyzer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicSessionStoragePersistsPrivateNonemptySession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.session")
	storage := NewAtomicSessionStorage(path)
	if err := storage.StoreSession(context.Background(), []byte("session-one")); err != nil {
		t.Fatal(err)
	}
	body, err := storage.LoadSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "session-one" {
		t.Fatalf("session = %q", body)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session mode = %o, want 600", info.Mode().Perm())
	}
}

func TestAtomicSessionStorageRejectsEmptyReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.session")
	storage := NewAtomicSessionStorage(path)
	if err := storage.StoreSession(context.Background(), []byte("working")); err != nil {
		t.Fatal(err)
	}
	if err := storage.StoreSession(context.Background(), nil); err == nil {
		t.Fatal("empty StoreSession() error = nil")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "working" {
		t.Fatalf("session after rejected empty write = %q", body)
	}
}
