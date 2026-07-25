package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestAdminPhoneBackendSwitchPersistsAndUpdatesRelay(t *testing.T) {
	simHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer simHealth.Close()
	pixelHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer pixelHealth.Close()

	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	store := NewMemoryStore()
	handler, relay := newBackendSwitchServer(t, store, activeFile, simHealth.URL, pixelHealth.URL)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/phone/backend", strings.NewReader(`{"backendId":"pixel"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch status = %d body = %s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile(activeFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"backendId": "pixel"`) {
		t.Fatalf("active backend file = %s", raw)
	}
	relayHealth := relay.Snapshot()
	if relayHealth.BackendID != "pixel" || relayHealth.BaseURL != pixelHealth.URL {
		t.Fatalf("relay health = %#v", relayHealth)
	}
	snapshot, err := store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phone == nil || snapshot.Phone.ID != "pixel" {
		t.Fatalf("state phone = %#v", snapshot.Phone)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRec.Code, healthRec.Body.String())
	}
	var health map[string]any
	if err := json.NewDecoder(healthRec.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	active, _ := health["activePhoneBackend"].(map[string]any)
	if active["id"] != "pixel" {
		t.Fatalf("health active backend = %#v", active)
	}
}

func TestAdminPhoneBackendSwitchRequiresAdmin(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	store := NewMemoryStore()
	handler, _ := newBackendSwitchServer(t, store, activeFile, "http://lab.test", "http://pixel.test")
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "member@example.com", state.RoleMember); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/phone/backend", strings.NewReader(`{"backendId":"pixel"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin switch status = %d body = %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(activeFile); !os.IsNotExist(err) {
		t.Fatalf("active backend file should not be written, stat err=%v", err)
	}
}

func TestAdminPhoneBackendsListsHealth(t *testing.T) {
	simHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer simHealth.Close()
	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	store := NewMemoryStore()
	handler, _ := newBackendSwitchServer(t, store, activeFile, simHealth.URL, "http://127.0.0.1:1")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/phone/backends", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("backends status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		ActiveBackendID string `json:"activeBackendId"`
		Backends        []struct {
			ID       string `json:"id"`
			Active   bool   `json:"active"`
			HealthOK bool   `json:"healthOk"`
		} `json:"backends"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.ActiveBackendID != "lab-pixel" || len(payload.Backends) != 2 {
		t.Fatalf("payload = %#v", payload)
	}
	if !payload.Backends[0].Active || !payload.Backends[0].HealthOK {
		t.Fatalf("lab backend health = %#v", payload.Backends[0])
	}
}

func TestAdminStateIncludesFreshActivePhoneHealth(t *testing.T) {
	activeHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/upstream/health" {
			_, _ = w.Write([]byte(`{"serverVersion":"ticket-stream-test","latestTicketReselect":{"status":"succeeded","phase":"ready","proofSource":"self_proof_root_hardware_h264","freshFrameAgoMillis":42}}`))
			return
		}
		if r.URL.Path == "/api/v1/health" {
			_, _ = w.Write([]byte(`{"ok":true,"upstream":{"ok":true}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer activeHealth.Close()
	pixelHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer pixelHealth.Close()

	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	store := NewMemoryStore()
	handler, _ := newBackendSwitchServer(t, store, activeFile, activeHealth.URL, pixelHealth.URL)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/state", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin state status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		OK    bool           `json:"ok"`
		State state.Snapshot `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.State.Phone == nil {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(payload.State.Phone.HealthJSON, `"latestTicketReselect"`) ||
		!strings.Contains(payload.State.Phone.HealthJSON, `"self_proof_root_hardware_h264"`) {
		t.Fatalf("admin state phone health = %s", payload.State.Phone.HealthJSON)
	}
}

func TestAdminTicketReselectLatestQueuesForceCommand(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	memory := NewMemoryStore()
	store := &capturingStreamCommandStore{Store: memory}
	handler, _ := newBackendSwitchServer(t, store, activeFile, "http://lab.test", "http://pixel.test")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ticket/reselect-latest", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("reselect status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(store.commands) != 1 {
		t.Fatalf("commands = %#v, want one", store.commands)
	}
	command := store.commands[0]
	if command.CommandType != "force_ticket_reselect" || command.BackendID != "lab-pixel" {
		t.Fatalf("command = %#v", command)
	}
	if command.TTL != 10*time.Minute {
		t.Fatalf("command TTL = %s, want 10 minutes", command.TTL)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(command.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != "force_ticket_reselect" || payload["source"] != "ticket_remote_admin" {
		t.Fatalf("payload = %#v", payload)
	}
	var response struct {
		OK        bool   `json:"ok"`
		CommandID string `json:"commandId"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.CommandID == "" || response.CommandID != command.CommandID {
		t.Fatalf("response = %#v command = %#v", response, command)
	}
}

func TestAdminTicketReselectLatestRequiresAdmin(t *testing.T) {
	activeFile := filepath.Join(t.TempDir(), "active-phone-backend.json")
	memory := NewMemoryStore()
	store := &capturingStreamCommandStore{Store: memory}
	handler, _ := newBackendSwitchServer(t, store, activeFile, "http://lab.test", "http://pixel.test")
	if _, err := memory.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "member@example.com", state.RoleMember); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ticket/reselect-latest", nil)
	req.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin reselect status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(store.commands) != 0 {
		t.Fatalf("non-admin queued commands = %#v", store.commands)
	}
}

func TestAdminTicketReselectLatestRequiresActiveBackend(t *testing.T) {
	store := &capturingStreamCommandStore{Store: NewMemoryStore()}
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:     "vivi-default",
		DisplayName:  "ViVi timed ticket",
		AdminEmail:   "ticket@jolkins.id.lv",
		AuthIssuer:   "https://issuer.test",
		AuthAudience: "ticket-remote",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{})
	handler, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ticket/reselect-latest", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing backend status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "phone_backend_unavailable") {
		t.Fatalf("missing backend body = %s", rec.Body.String())
	}
	if len(store.commands) != 0 {
		t.Fatalf("missing backend queued commands = %#v", store.commands)
	}
}

func TestHealthReportsActiveBackendWhenStoredPhoneIsStale(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:  "lab-pixel",
		AttachName: "Lab Pixel",
		BaseURL:    "http://lab.test",
	})
	handler, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "lab-pixel",
			AttachName:       "Lab Pixel",
			BaseURL:          "http://lab.test",
			Backends:         []config.PhoneBackend{{ID: "lab-pixel", AttachName: "Lab Pixel", BaseURL: "http://lab.test"}},
			DefaultBackendID: "lab-pixel",
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		State struct {
			Phone *state.PhoneBackend `json:"phone"`
		} `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.State.Phone == nil || payload.State.Phone.ID != "lab-pixel" {
		t.Fatalf("state phone = %#v", payload.State.Phone)
	}
}

type capturingStreamCommandStore struct {
	state.Store
	commands []state.StreamCommandInput
}

func (s *capturingStreamCommandStore) AppendStreamCommand(ctx context.Context, input state.StreamCommandInput) error {
	s.commands = append(s.commands, input)
	return s.Store.AppendStreamCommand(ctx, input)
}

func newBackendSwitchServer(t *testing.T, store state.Store, activeFile string, simURL string, pixelURL string) (http.Handler, *phone.Relay) {
	t.Helper()
	backends := []config.PhoneBackend{
		{ID: "lab-pixel", AttachName: "Lab Pixel", BaseURL: simURL},
		{ID: "pixel", AttachName: "Pixel", BaseURL: pixelURL},
	}
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "lab-pixel",
		PhoneBaseURL:    simURL,
		PhoneAttachName: "Lab Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "lab-pixel",
		AttachName:        "Lab Pixel",
		BaseURL:           simURL,
		RequestTimeout:    50 * time.Millisecond,
		NoViewerStopDelay: time.Hour,
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
			BackendID:         "lab-pixel",
			AttachName:        "Lab Pixel",
			BaseURL:           simURL,
			Backends:          backends,
			DefaultBackendID:  "lab-pixel",
			ActiveBackendFile: activeFile,
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	return server, relay
}
