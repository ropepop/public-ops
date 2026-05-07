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

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestControlRoutesClaimExtendRelease(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://phone.test",
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
		Phone: config.PhoneConfig{BaseURL: "http://phone.test"},
	}, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}))
	if err != nil {
		t.Fatal(err)
	}

	claim := postControl(t, server, nil, "/api/v1/control/claim")
	if claim.State.ActiveControl == nil {
		t.Fatal("expected claimed control session")
	}
	cookies := claim.Cookies

	extend := postControl(t, server, cookies, "/api/v1/control/extend")
	if extend.State.ActiveControl == nil || !extend.State.ActiveControl.Extended {
		t.Fatalf("expected extended control session, got %#v", extend.State.ActiveControl)
	}

	release := postControl(t, server, cookies, "/api/v1/control/release")
	if release.State.ActiveControl != nil {
		t.Fatalf("expected released control session, got %#v", release.State.ActiveControl)
	}
}

func TestControlReleaseNotifiesPhoneControlExit(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			for {
				_, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				messages <- string(data)
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			_, _, _ = conn.Read(r.Context())
			<-r.Context().Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    phoneServer.URL,
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}

	relay.AddViewer()
	waitForPhoneMessage(t, messages, `"type":"start"`)
	claim := postControl(t, server, nil, "/api/v1/control/claim")
	postControl(t, server, claim.Cookies, "/api/v1/control/release")
	waitForPhoneMessage(t, messages, `"type":"control_exit"`)
}

func TestControlGateEndTransitionNotifiesPhoneControlExit(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			for {
				_, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				messages <- string(data)
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			_, _, _ = conn.Read(r.Context())
			<-r.Context().Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := state.NewMemoryStore()
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.relay.Close()
	server.relay.AddViewer()
	waitForPhoneMessage(t, messages, `"type":"start"`)

	now := time.Now()
	server.rememberControlGate(state.Snapshot{ActiveControl: &state.ControlSession{
		SessionID: "session",
		Email:     "ticket@jolkins.id.lv",
		ExpiresAt: now.Add(time.Minute).UTC().Format(time.RFC3339),
	}}, now)
	server.rememberControlGate(state.Snapshot{}, now.Add(time.Second))
	waitForPhoneMessage(t, messages, `"type":"control_exit"`)
}

func TestQuickClaimTapClaimsThenForwardsSnappedTap(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := newTicketPhoneTestServer(t, messages)
	defer phoneServer.Close()

	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(context.Background(), wsURL(httpServer, "/api/v1/session"), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	err = conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"quick_claim_tap","inputId":"quick-1","x":20,"y":20,"snapTarget":"control_code_button"}`))
	if err != nil {
		t.Fatal(err)
	}
	forwarded := waitForPhoneMessageText(t, messages, `"type":"tap"`)
	if !strings.Contains(forwarded, `"snapTarget":"control_code_button"`) || !strings.Contains(forwarded, `"inputId":"quick-1"`) {
		t.Fatalf("forwarded phone message did not preserve snap target/input id: %s", forwarded)
	}
	active, allowed := server.activeControlGateAllows("", "ticket@jolkins.id.lv", time.Now())
	if !active || !allowed {
		t.Fatalf("quick claim did not update local control gate: active=%v allowed=%v", active, allowed)
	}
	if diag := server.quickClaimSnapshot(); !diag.Forwarded || diag.InputID != "quick-1" || diag.Action != "claimed" {
		t.Fatalf("unexpected quick claim diagnostic: %#v", diag)
	}
}

func TestQuickClaimTapRejectsDifferentEmailWithoutForwarding(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := newTicketPhoneTestServer(t, messages)
	defer phoneServer.Close()

	store := newTicketMemoryStore(t, phoneServer.URL)
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "test@jolkins.id.lv", state.RoleMember); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimControl(context.Background(), "vivi-default", "ticket-session", "ticket@jolkins.id.lv", time.Now()); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(context.Background(), wsURL(httpServer, "/api/v1/session"), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ticket-Remote-Email": []string{"test@jolkins.id.lv"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	err = conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"quick_claim_tap","inputId":"quick-2","x":20,"y":20,"snapTarget":"control_code_button"}`))
	if err != nil {
		t.Fatal(err)
	}
	response := waitForBrowserMessage(t, conn, `"inputId":"quick-2"`)
	if !strings.Contains(response, `"accepted":false`) || !strings.Contains(response, `"reason":"not_controller"`) {
		t.Fatalf("expected not_controller rejection, got %s", response)
	}
	select {
	case message := <-messages:
		if strings.Contains(message, `"type":"tap"`) {
			t.Fatalf("different-email quick claim was forwarded to phone: %s", message)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func waitForPhoneMessage(t *testing.T, messages <-chan string, snippet string) {
	t.Helper()
	_ = waitForPhoneMessageText(t, messages, snippet)
}

func waitForPhoneMessageText(t *testing.T, messages <-chan string, snippet string) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case message := <-messages:
			if strings.Contains(message, snippet) {
				return message
			}
		case <-deadline:
			t.Fatalf("timed out waiting for phone message containing %s", snippet)
		}
	}
}

func waitForBrowserMessage(t *testing.T, conn *websocket.Conn, snippet string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read browser websocket: %v", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		message := string(data)
		if strings.Contains(message, snippet) {
			return message
		}
	}
}

func newTicketPhoneTestServer(t *testing.T, messages chan<- string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			for {
				_, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				messages <- string(data)
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			_, _, _ = conn.Read(r.Context())
			<-r.Context().Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTicketMemoryStore(t *testing.T, phoneURL string) *state.MemoryStore {
	t.Helper()
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    phoneURL,
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func newTicketWebServer(t *testing.T, store state.Store, relay *phone.Relay, phoneURL string) *Server {
	t.Helper()
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneURL},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func wsURL(server *httptest.Server, path string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + path
}

type controlResponse struct {
	State   state.Snapshot
	Cookies []*http.Cookie
}

func postControl(t *testing.T, handler http.Handler, cookies []*http.Cookie, path string) controlResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString("{}"))
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
	}
	var body apiResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.OK {
		t.Fatalf("%s returned not ok: %#v", path, body)
	}
	return controlResponse{State: body.State, Cookies: rec.Result().Cookies()}
}
