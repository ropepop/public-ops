package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSpacetimeAccessStoreSaveAndLoad(t *testing.T) {
	var calls []string
	var savedState AccessState
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Fatalf("authorization header = %q, want %q", got, want)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/call/rigassatiksmeqrbot_export_access_state"):
			_ = json.NewEncoder(w).Encode(map[string]any{"state": map[string]any{
				"version": 1,
				"admins":  map[string]bool{"100": true},
				"users":   map[string]any{"200": map[string]any{"userId": "200", "active": true, "dailyLimit": 2}},
			}})
		case strings.HasSuffix(r.URL.Path, "/call/rigassatiksmeqrbot_import_access_state"):
			var args []string
			if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
				t.Fatalf("decode args: %v", err)
			}
			if len(args) != 1 {
				t.Fatalf("args len = %d, want 1", len(args))
			}
			if err := json.Unmarshal([]byte(args[0]), &savedState); err != nil {
				t.Fatalf("saved state is not JSON: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	store, err := NewSpacetimeAccessStore(SpacetimeAccessConfig{
		Host:        server.URL,
		Database:    "qr-db",
		BearerToken: "test-token",
		HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewSpacetimeAccessStore: %v", err)
	}
	state, ok, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok || !state.Admins["100"] || state.Users["200"].DailyLimit != 2 {
		t.Fatalf("unexpected loaded state: ok=%v state=%+v", ok, state)
	}
	state.Groups = map[string]AccessGroup{"crew": {Name: "crew", Active: true, DailyLimit: 5}}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if savedState.Groups["crew"].DailyLimit != 5 {
		t.Fatalf("saved state missing group: %+v", savedState)
	}
	if len(calls) != 2 || calls[0] != "/v1/database/qr-db/call/rigassatiksmeqrbot_export_access_state" || calls[1] != "/v1/database/qr-db/call/rigassatiksmeqrbot_import_access_state" {
		t.Fatalf("unexpected calls: %v", calls)
	}
}

func TestSpacetimeAccessStoreRequiresCompleteConfig(t *testing.T) {
	for _, cfg := range []SpacetimeAccessConfig{
		{Database: "db", BearerToken: "token"},
		{Host: "http://example.invalid", BearerToken: "token"},
		{Host: "http://example.invalid", Database: "db"},
	} {
		if _, err := NewSpacetimeAccessStore(cfg); err == nil {
			t.Fatalf("NewSpacetimeAccessStore(%+v) succeeded, want error", cfg)
		}
	}
}
