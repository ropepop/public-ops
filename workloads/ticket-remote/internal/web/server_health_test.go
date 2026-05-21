package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestHealthUsesFreshCachedStateWithoutStateLookup(t *testing.T) {
	memoryStore := state.NewMemoryStore()
	if err := memoryStore.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	store := &cachedHealthStore{Store: memoryStore}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "pixel",
			AttachName:       "Pixel",
			BaseURL:          "http://pixel.test",
			DefaultBackendID: "pixel",
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.snapshotWithCache(context.Background(), time.Now(), relay.Snapshot(), 0); err != nil {
		t.Fatal(err)
	}
	store.failSnapshot = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK                  bool     `json:"ok"`
		Reasons             []string `json:"reasons"`
		StateBackendFresh   bool     `json:"stateBackendFresh"`
		StateBackendWarning string   `json:"stateBackendWarning"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK {
		t.Fatalf("cached health should remain ok: %#v", payload)
	}
	if len(payload.Reasons) != 0 {
		t.Fatalf("cached health should not report hard reasons: %#v", payload.Reasons)
	}
	if !payload.StateBackendFresh {
		t.Fatalf("stateBackendFresh should report true when a fresh cached snapshot is used")
	}
	if payload.StateBackendWarning != "" {
		t.Fatalf("fresh cached health should not warn: %q", payload.StateBackendWarning)
	}
	if strings.Contains(payload.StateBackendWarning, "Stack backtrace") || strings.Contains(payload.StateBackendWarning, "\n") {
		t.Fatalf("warning was not sanitized: %q", payload.StateBackendWarning)
	}
}

func TestMemberRouteRejectsStaleCachedStateWhenFreshLookupFails(t *testing.T) {
	memoryStore := state.NewMemoryStore()
	if err := memoryStore.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	store := &cachedHealthStore{Store: memoryStore}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "pixel",
			AttachName:       "Pixel",
			BaseURL:          "http://pixel.test",
			DefaultBackendID: "pixel",
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.snapshotWithCache(context.Background(), time.Now(), relay.Snapshot(), 0); err != nil {
		t.Fatal(err)
	}
	server.stateMu.Lock()
	server.cachedStateAt = time.Now().Add(-(stateCacheMaxAge + time.Second))
	server.stateMu.Unlock()
	store.failSnapshot = true

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("member route should reject stale cached state; status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Ticket state is unavailable.") {
		t.Fatalf("stale cached state rejection body = %s", rec.Body.String())
	}
}

type cachedHealthStore struct {
	state.Store
	failSnapshot bool
}

func (s *cachedHealthStore) Backend() string {
	return "spacetime"
}

func (s *cachedHealthStore) Snapshot(ctx context.Context, ticketID string, now time.Time) (state.Snapshot, error) {
	if s.failSnapshot {
		return state.Snapshot{}, errors.New("state lookup failed\n\nStack backtrace:\n  0: internal")
	}
	snapshot, err := s.Store.Snapshot(ctx, ticketID, now)
	snapshot.StateBackend = s.Backend()
	return snapshot, err
}
