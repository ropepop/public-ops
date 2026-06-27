package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestLegacyControlRoutesReturnGone(t *testing.T) {
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

	for _, path := range []string{
		"/api/v1/control/claim",
		"/api/v1/control/extend",
		"/api/v1/control/release",
		"/api/v1/admin/control/revoke",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString("{}"))
			req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != http.StatusGone {
				t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
			}
			var body apiResponse
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.OK || body.Error != "control_mode_removed" {
				t.Fatalf("expected control_mode_removed response, got %#v", body)
			}
		})
	}
}

func TestLegacyControlReleaseDoesNotNotifyPhoneControlExit(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		case "/api/v1/session/start", "/api/v1/session", "/api/v1/session/stop":
			t.Errorf("removed direct phone-control path was used: %s", r.URL.Path)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, messages)

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
	removed := postControlFailure(t, server, nil, "/api/v1/control/release")
	if removed.Error != "control_mode_removed" {
		t.Fatalf("expected control_mode_removed, got %#v", removed)
	}
	select {
	case message := <-messages:
		if strings.Contains(message, `"type":"control_exit"`) {
			t.Fatalf("legacy release sent phone control_exit: %s", message)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestControlGateEndTransitionNotifiesPhoneControlExit(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		case "/api/v1/session/start", "/api/v1/session", "/api/v1/session/stop":
			t.Errorf("removed direct phone-control path was used: %s", r.URL.Path)
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, messages)

	store := newTicketMemoryStore(t, phoneServer.URL)
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

	now := time.Now()
	server.rememberControlGate(state.Snapshot{ActiveControl: &state.ControlSession{
		SessionID: "session",
		Email:     "ticket@jolkins.id.lv",
		ExpiresAt: now.Add(time.Minute).UTC().Format(time.RFC3339),
	}}, now)
	server.rememberControlGate(state.Snapshot{}, now.Add(time.Second))
	waitForPhoneMessage(t, messages, `"type":"control_exit"`)
}

func TestQuickClaimTapRejectsBecauseControlModeWasRemoved(t *testing.T) {
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

	err = conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"quick_claim_tap","inputId":"quick-1","x":20,"y":20,"snapTarget":"control_code_button"}`))
	if err != nil {
		t.Fatal(err)
	}
	response := waitForBrowserMessage(t, conn, `"inputId":"quick-1"`)
	if !strings.Contains(response, `"type":"input_result"`) || !strings.Contains(response, `"accepted":false`) || !strings.Contains(response, `"reason":"control_mode_removed"`) {
		t.Fatalf("expected removed quick claim input_result, got %s", response)
	}
	if diag := server.quickClaimSnapshot(); diag.Forwarded || diag.InputID != "quick-1" || diag.Action != "control_mode_removed" {
		t.Fatalf("legacy diagnostic should not record forwarded quick claim: %#v", diag)
	}
	select {
	case message := <-messages:
		if strings.Contains(message, `"type":"tap"`) {
			t.Fatalf("quick claim was forwarded to phone: %s", message)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTapRejectedEvenForLegacyActiveControl(t *testing.T) {
	messages := make(chan string, 10)
	phoneServer := newTicketPhoneTestServer(t, messages)
	defer phoneServer.Close()

	store := newTicketMemoryStore(t, phoneServer.URL)
	snapshot, err := store.ClaimControl(context.Background(), "vivi-default", "ticket-session", "ticket@jolkins.id.lv", time.Now())
	if err != nil {
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
	server.rememberControlGate(snapshot, time.Now())
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	conn, _, err := websocket.Dial(context.Background(), wsURL(httpServer, "/api/v1/session"), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "test complete")

	if err := conn.Write(context.Background(), websocket.MessageText, []byte(`{"type":"tap","inputId":"tap-1","x":101,"y":202}`)); err != nil {
		t.Fatal(err)
	}
	response := waitForBrowserMessage(t, conn, `"inputId":"tap-1"`)
	if !strings.Contains(response, `"type":"input_result"`) || !strings.Contains(response, `"accepted":false`) || !strings.Contains(response, `"reason":"control_mode_removed"`) {
		t.Fatalf("expected removed tap input_result, got %s", response)
	}
	select {
	case message := <-messages:
		if strings.Contains(message, `"type":"tap"`) {
			t.Fatalf("legacy tap was forwarded to phone: %s", message)
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

func waitForPhoneMessageTextTimeout(t *testing.T, messages <-chan string, snippet string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
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

var ticketStreamCommandSinkRegistry = struct {
	sync.Mutex
	sinks map[string]chan<- string
}{sinks: map[string]chan<- string{}}

type recordingTicketStore struct {
	state.Store
	commandSink chan<- string
}

func registerTicketStreamCommandSink(t *testing.T, phoneURL string, sink chan<- string) {
	t.Helper()
	cleanURL := strings.TrimRight(strings.TrimSpace(phoneURL), "/")
	if cleanURL == "" {
		return
	}
	ticketStreamCommandSinkRegistry.Lock()
	ticketStreamCommandSinkRegistry.sinks[cleanURL] = sink
	ticketStreamCommandSinkRegistry.Unlock()
	t.Cleanup(func() {
		ticketStreamCommandSinkRegistry.Lock()
		delete(ticketStreamCommandSinkRegistry.sinks, cleanURL)
		ticketStreamCommandSinkRegistry.Unlock()
	})
}

func ticketStreamCommandSink(phoneURL string) chan<- string {
	cleanURL := strings.TrimRight(strings.TrimSpace(phoneURL), "/")
	ticketStreamCommandSinkRegistry.Lock()
	defer ticketStreamCommandSinkRegistry.Unlock()
	return ticketStreamCommandSinkRegistry.sinks[cleanURL]
}

func (s *recordingTicketStore) AppendStreamCommand(ctx context.Context, input state.StreamCommandInput) error {
	if err := s.Store.AppendStreamCommand(ctx, input); err != nil {
		return err
	}
	if s.commandSink != nil {
		message := streamCommandInputTestMessage(input)
		select {
		case s.commandSink <- message:
		default:
		}
	}
	return nil
}

func streamCommandInputTestMessage(input state.StreamCommandInput) string {
	payload := map[string]any{}
	if strings.TrimSpace(input.PayloadJSON) != "" {
		_ = json.Unmarshal([]byte(input.PayloadJSON), &payload)
	}
	if _, ok := payload["type"]; !ok {
		payload["type"] = input.CommandType
	}
	if _, ok := payload["reason"]; !ok && input.Reason != "" {
		payload["reason"] = input.Reason
	}
	payload["commandId"] = input.CommandID
	payload["revision"] = input.Revision
	payload["commandType"] = input.CommandType
	body, _ := json.Marshal(payload)
	return string(body)
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

func newTicketMemoryStore(t *testing.T, phoneURL string) state.Store {
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
	if sink := ticketStreamCommandSink(phoneURL); sink != nil {
		return &recordingTicketStore{Store: store, commandSink: sink}
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

type controlFailureResponse struct {
	Error   string
	Message string
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

func postControlFailure(t *testing.T, handler http.Handler, cookies []*http.Cookie, path string) controlFailureResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString("{}"))
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	req.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Fatalf("%s status = %d body = %s", path, rec.Code, rec.Body.String())
	}
	var body apiResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.OK {
		t.Fatalf("%s returned ok unexpectedly: %#v", path, body)
	}
	return controlFailureResponse{
		Error:   body.Error,
		Message: body.Message,
		State:   body.State,
		Cookies: rec.Result().Cookies(),
	}
}
