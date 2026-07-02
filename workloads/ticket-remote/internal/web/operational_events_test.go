package web

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestAdminOperationalEventsRequiresAdminAndRedactsPrivateDetail(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	if err := server.store.AppendSafeOperationalLog(context.Background(), state.SafeOperationalLogInput{
		TicketID:      "vivi-default",
		Source:        "pixel",
		Level:         "info",
		Event:         "control_code_result",
		CorrelationID: "req-safe",
		DetailJSON:    `{"category":"control_code","action":"generated","status":"succeeded","reason":"value 123456 and 87654321 from https://private.example/code","requestId":"req-safe","safeState":{"digits":"123456","label":"ticket 87654321","url":"https://private.example/code"}}`,
		Now:           time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	memberReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/operational-events", nil)
	memberReq.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	memberRec := httptest.NewRecorder()
	server.ServeHTTP(memberRec, memberReq)
	if memberRec.Code != http.StatusForbidden {
		t.Fatalf("member status = %d body = %s", memberRec.Code, memberRec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/operational-events?category=control_code&limit=10", nil)
	adminReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	adminRec := httptest.NewRecorder()
	server.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin status = %d body = %s", adminRec.Code, adminRec.Body.String())
	}
	body := adminRec.Body.String()
	for _, forbidden := range []string{"123456", "87654321", "private.example", "detailJson", "detailJSON"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("admin events exposed private detail %q in %s", forbidden, body)
		}
	}
	var payload struct {
		OK     bool                    `json:"ok"`
		Events []adminOperationalEvent `json:"events"`
	}
	if err := json.Unmarshal(adminRec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || len(payload.Events) != 1 {
		t.Fatalf("admin events payload = %#v", payload)
	}
	event := payload.Events[0]
	if event.Category != "control_code" || event.Action != "generated" || event.RequestID != "req-safe" {
		t.Fatalf("admin event = %#v", event)
	}
	if _, ok := event.SafeState["digits"]; ok {
		t.Fatalf("admin safe state exposed digits: %#v", event.SafeState)
	}
}

func TestServiceEventEndpointRequiresTokenAndStoresSanitizedEvent(t *testing.T) {
	disabled := newTicketSetupTestServer(t, "pixel")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/service-events", strings.NewReader(`{"source":"phone_broker","category":"broker","action":"lease_acquired"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	disabled.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled service-events status = %d body = %s", rec.Code, rec.Body.String())
	}

	store := state.NewMemoryStore()
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
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone:         config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
		ServiceEvents: config.ServiceEventsConfig{Token: "secret"},
	}, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/service-events", strings.NewReader(`{"source":"phone_broker","category":"broker","action":"lease_acquired"}`))
	badRec := httptest.NewRecorder()
	server.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusForbidden {
		t.Fatalf("bad token status = %d body = %s", badRec.Code, badRec.Body.String())
	}

	body := []byte(`{"source":"phone_broker","category":"broker","action":"lease_acquired","status":"ok","reason":"lease 123456 and 87654321 at https://private.example","safeState":{"token":"secret-token","url":"https://private.example","count":3}}`)
	goodReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/service-events", bytes.NewReader(body))
	goodReq.Header.Set("Authorization", "Bearer secret")
	goodRec := httptest.NewRecorder()
	server.ServeHTTP(goodRec, goodReq)
	if goodRec.Code != http.StatusAccepted {
		t.Fatalf("good token status = %d body = %s", goodRec.Code, goodRec.Body.String())
	}

	logs, err := store.ListSafeOperationalLogs(context.Background(), state.SafeOperationalLogQueryInput{TicketID: "vivi-default", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %#v", logs)
	}
	log := logs[0]
	if log.Source != "phone_broker" || log.Event != "product_broker_lease_acquired" {
		t.Fatalf("stored log = %#v", log)
	}
	for _, forbidden := range []string{"123456", "87654321", "private.example", "secret-token"} {
		if strings.Contains(log.DetailJSON, forbidden) {
			t.Fatalf("stored service event exposed %q in %s", forbidden, log.DetailJSON)
		}
	}
}

func TestPixelTraceEventIsStoredPrivatelyAndConsumed(t *testing.T) {
	store := newTicketMemoryStore(t, "http://phone.test")
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")

	if handled := server.handlePhoneText([]byte(`{"type":"ticket_trace_event","event":"control_code_result","level":"info","detail":"value=123456","correlationId":"trace-safe","streamState":"streaming","sessionState":"live"}`)); !handled {
		t.Fatal("pixel trace event was not consumed")
	}
	var logs []state.SafeOperationalLog
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		logs, err = store.ListSafeOperationalLogs(context.Background(), state.SafeOperationalLogQueryInput{
			TicketID: "vivi-default",
			Source:   "pixel",
			Limit:    10,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %#v", logs)
	}
	log := logs[0]
	if log.Event != "control_code_result" || log.CorrelationID != "trace-safe" {
		t.Fatalf("pixel trace log = %#v", log)
	}
	if !strings.Contains(log.DetailJSON, "value=123456") {
		t.Fatalf("private pixel trace detail should retain raw private context, got %s", log.DetailJSON)
	}
}
