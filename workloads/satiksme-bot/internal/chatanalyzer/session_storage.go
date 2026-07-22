package chatanalyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gotd/td/session"
)

// AtomicSessionStorage keeps gotd session refreshes crash-safe and private.
type AtomicSessionStorage struct {
	path string
	mu   sync.Mutex
}

func NewAtomicSessionStorage(path string) *AtomicSessionStorage {
	return &AtomicSessionStorage{path: filepath.Clean(strings.TrimSpace(path))}
}

func (s *AtomicSessionStorage) LoadSession(context.Context) ([]byte, error) {
	if s == nil || s.path == "." || s.path == "" {
		return nil, fmt.Errorf("session file is not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	body, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read Telegram session: %w", err)
	}
	if len(body) == 0 {
		return nil, session.ErrNotFound
	}
	if err := os.Chmod(s.path, 0o600); err != nil {
		return nil, fmt.Errorf("secure Telegram session: %w", err)
	}
	return body, nil
}

func (s *AtomicSessionStorage) StoreSession(_ context.Context, data []byte) error {
	if s == nil || s.path == "." || s.path == "" {
		return fmt.Errorf("session file is not configured")
	}
	if len(data) == 0 {
		return fmt.Errorf("refusing to replace Telegram session with empty data")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Telegram session directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".telegram-session-*")
	if err != nil {
		return fmt.Errorf("create staged Telegram session: %w", err)
	}
	staged := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(staged)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure staged Telegram session: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write staged Telegram session: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync staged Telegram session: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged Telegram session: %w", err)
	}
	if err := os.Rename(staged, s.path); err != nil {
		return fmt.Errorf("commit Telegram session: %w", err)
	}
	committed = true
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open Telegram session directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync Telegram session directory: %w", err)
	}
	return nil
}
