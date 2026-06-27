package web

import (
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

type bridgeCommandStore struct {
	state.Store
	mu           sync.Mutex
	commands     []state.StreamCommand
	pendingReads int
	acks         []state.StreamCommandAckInput
	phoneReports []state.PhoneCurrentReportInput
	codeUpdates  []state.ControlCodeRequestUpdateInput
	logs         []state.SafeOperationalLogInput
}

func (s *bridgeCommandStore) Backend() string {
	return "spacetime"
}

func (s *bridgeCommandStore) PendingStreamCommands(_ context.Context, ticketID string, backendID string, limit uint32, now time.Time) ([]state.StreamCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingReads++
	var out []state.StreamCommand
	for _, command := range s.commands {
		if command.TicketID != ticketID || command.BackendID != backendID || command.Status != "pending" {
			continue
		}
		expiresAt, _ := time.Parse(time.RFC3339, command.ExpiresAt)
		if !expiresAt.IsZero() && !now.Before(expiresAt) {
			continue
		}
		out = append(out, command)
		if uint32(len(out)) >= limit {
			break
		}
	}
	return out, nil
}

func (s *bridgeCommandStore) StreamCommandSignal(_ context.Context, ticketID string, backendID string) (state.StreamCommandSignal, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var revision string
	var updatedAt string
	var pending uint32
	now := time.Now()
	for _, command := range s.commands {
		if command.TicketID != ticketID || command.BackendID != backendID || command.Status != "pending" {
			continue
		}
		expiresAt, _ := time.Parse(time.RFC3339, command.ExpiresAt)
		if !expiresAt.IsZero() && !now.Before(expiresAt) {
			continue
		}
		pending++
		revision = command.Revision
		updatedAt = command.UpdatedAt
	}
	return state.StreamCommandSignal{
		ID:           ticketID + ":" + backendID,
		TicketID:     ticketID,
		BackendID:    backendID,
		Revision:     revision,
		PendingCount: pending,
		UpdatedAt:    updatedAt,
	}, true, nil
}

func (s *bridgeCommandStore) AckStreamCommand(_ context.Context, input state.StreamCommandAckInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acks = append(s.acks, input)
	for index, command := range s.commands {
		if command.ID == input.CommandID {
			s.commands[index].Status = input.Status
			s.commands[index].PayloadJSON = "{}"
			s.commands[index].UpdatedAt = input.Now.UTC().Format(time.RFC3339)
		}
	}
	return nil
}

func (s *bridgeCommandStore) UpdatePhoneCurrentReport(_ context.Context, input state.PhoneCurrentReportInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phoneReports = append(s.phoneReports, input)
	return nil
}

func (s *bridgeCommandStore) UpdateControlCodeRequest(_ context.Context, input state.ControlCodeRequestUpdateInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codeUpdates = append(s.codeUpdates, input)
	return nil
}

func (s *bridgeCommandStore) AppendSafeOperationalLog(_ context.Context, input state.SafeOperationalLogInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, input)
	return nil
}

func TestStreamCommandBridgeDispatchesPendingCommandAndAcks(t *testing.T) {
	messages := make(chan map[string]any, 4)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone command websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			_, body, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode phone command: %v", err)
				return
			}
			messages <- payload
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := &bridgeCommandStore{Store: state.NewMemoryStore()}
	store.commands = []state.StreamCommand{{
		ID:          "cmd-keyframe-1",
		TicketID:    "vivi-default",
		BackendID:   "pixel",
		CommandType: "keyframe",
		Status:      "pending",
		Revision:    "rev-1",
		Reason:      "browser_request",
		PayloadJSON: `{"reason":"browser_request"}`,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}}
	relay := phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "spacetime",
			DevEmail: "ticket@jolkins.id.lv",
		},
		State: configStateForBridgeTest(),
		Phone: config.PhoneConfig{
			BackendID:     "pixel",
			AttachName:    "Pixel",
			BaseURL:       phoneServer.URL,
			BrokerBaseURL: phoneServer.URL,
			Backends:      []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL}},
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	payload := waitForBridgePhonePayload(t, messages)
	if payload["type"] != "keyframe" || payload["commandId"] != "cmd-keyframe-1" {
		t.Fatalf("phone payload = %#v", payload)
	}
	waitForBridgeAck(t, store, "cmd-keyframe-1")
	waitForBridgePhoneReport(t, store, "cmd-keyframe-1")
}

func TestStreamCommandBridgeBacksOffWhenIdle(t *testing.T) {
	server := &Server{
		direct: newDirectStreamHub(),
		relay:  phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}),
	}
	if got := server.nextStreamCommandBridgePollDelay(false, nil); got != streamCommandBridgeIdlePollInterval {
		t.Fatalf("idle bridge poll delay = %v, want %v", got, streamCommandBridgeIdlePollInterval)
	}
	if got := server.nextStreamCommandBridgePollDelay(true, nil); got != streamCommandBridgeFastPollInterval {
		t.Fatalf("command bridge poll delay = %v, want %v", got, streamCommandBridgeFastPollInterval)
	}
	if got := server.nextStreamCommandBridgePollDelay(false, map[string]streamCommandBridgeAttempt{"cmd": {failures: 1}}); got != streamCommandBridgeFastPollInterval {
		t.Fatalf("retry bridge poll delay = %v, want %v", got, streamCommandBridgeFastPollInterval)
	}
}

func TestStreamCommandBridgeSkipsPrivateReadWhenSignalIsIdle(t *testing.T) {
	store := &bridgeCommandStore{Store: state.NewMemoryStore()}
	server := &Server{
		cfg:    config.Config{TicketID: "vivi-default"},
		direct: newDirectStreamHub(),
		relay:  phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}),
	}
	hadCommands := server.pollPendingStreamCommands(store, map[string]streamCommandBridgeAttempt{}, map[string]string{})
	if hadCommands {
		t.Fatal("idle signal should not report commands")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.pendingReads != 0 {
		t.Fatalf("idle signal triggered private pending-command read: %d", store.pendingReads)
	}
}

func TestStreamCommandBridgeKeepsControlCodePayloadOutOfLogs(t *testing.T) {
	messages := make(chan map[string]any, 4)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone command websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			_, body, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode phone command: %v", err)
				return
			}
			messages <- payload
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := &bridgeCommandStore{Store: state.NewMemoryStore()}
	store.commands = []state.StreamCommand{{
		ID:          "request-1:generate_control_code",
		TicketID:    "vivi-default",
		BackendID:   "pixel",
		CommandType: "generate_control_code",
		Status:      "pending",
		Revision:    "rev-1",
		Reason:      "control_code_request",
		PayloadJSON: `{"type":"generate_control_code","requestId":"request-1","digits":"12345"}`,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Millisecond,
		ReconnectMaxDelay: time.Millisecond,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "spacetime",
			DevEmail: "ticket@jolkins.id.lv",
		},
		State: configStateForBridgeTest(),
		Phone: config.PhoneConfig{
			BackendID:     "pixel",
			AttachName:    "Pixel",
			BaseURL:       phoneServer.URL,
			BrokerBaseURL: phoneServer.URL,
			Backends:      []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL}},
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	payload := waitForBridgePhonePayload(t, messages)
	if payload["digits"] != "12345" {
		t.Fatalf("phone payload did not carry digits to Pixel: %#v", payload)
	}
	waitForBridgeAck(t, store, "request-1:generate_control_code")

	store.mu.Lock()
	defer store.mu.Unlock()
	for _, entry := range store.logs {
		if strings.Contains(entry.DetailJSON, "12345") || strings.Contains(entry.DetailJSON, "digits") {
			t.Fatalf("bridge log leaked control-code payload: %#v", entry)
		}
	}
}

func TestStreamCommandBridgeReadsControlCodeFrameReadyIntoSpacetime(t *testing.T) {
	messages := make(chan map[string]any, 4)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone command websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			_, body, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Errorf("decode phone command: %v", err)
				return
			}
			messages <- payload
			result := map[string]any{
				"type":                   "control_code_frame_ready",
				"requestId":              payload["requestId"],
				"value":                  "result-redacted",
				"streamEpoch":            float64(42),
				"frameSequence":          float64(101),
				"minFrameSequence":       float64(100),
				"resultFrameEpoch":       float64(42),
				"resultMinFrameSequence": float64(100),
				"resultProof":            "browser_stream_marker",
			}
			raw, _ := json.Marshal(result)
			if err := conn.Write(r.Context(), websocket.MessageText, raw); err != nil {
				t.Errorf("write phone result: %v", err)
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test done")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := &bridgeCommandStore{Store: state.NewMemoryStore()}
	store.commands = []state.StreamCommand{{
		ID:          "request-2:generate_control_code",
		TicketID:    "vivi-default",
		BackendID:   "pixel",
		CommandType: "generate_control_code",
		Status:      "pending",
		Revision:    "rev-1",
		Reason:      "control_code_request",
		PayloadJSON: `{"type":"generate_control_code","requestId":"request-2","digits":"12345"}`,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
		ExpiresAt:   time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
	}}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Millisecond,
		ReconnectMaxDelay: time.Millisecond,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "spacetime",
			DevEmail: "ticket@jolkins.id.lv",
		},
		State: configStateForBridgeTest(),
		Phone: config.PhoneConfig{
			BackendID:     "pixel",
			AttachName:    "Pixel",
			BaseURL:       phoneServer.URL,
			BrokerBaseURL: phoneServer.URL,
			Backends:      []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL}},
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	payload := waitForBridgePhonePayload(t, messages)
	if payload["requestId"] != "request-2" {
		t.Fatalf("phone payload requestId = %#v", payload)
	}
	waitForBridgeAck(t, store, "request-2:generate_control_code")
	waitForBridgeControlCodeUpdate(t, store, "request-2", controlCodeRunning)
	update := waitForBridgeControlCodeUpdate(t, store, "request-2", controlCodeSucceeded)
	if update.StreamEpoch != "42" || update.FrameSequence != "101" || update.MinFrameSequence != "100" || !update.CleanupPending {
		t.Fatalf("unexpected succeeded update: %#v", update)
	}
}

func configStateForBridgeTest() state.StoreConfig {
	return state.StoreConfig{
		Backend:              "spacetime",
		SpacetimeDatabase:    "ticket-test",
		SpacetimeBearerToken: "service-token",
	}
}

func waitForBridgePhonePayload(t *testing.T, messages <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case payload := <-messages:
		return payload
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for phone command")
	}
	return nil
}

func waitForBridgeAck(t *testing.T, store *bridgeCommandStore, commandID string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		store.mu.Lock()
		for _, ack := range store.acks {
			if ack.CommandID == commandID && ack.Status == "acknowledged" {
				store.mu.Unlock()
				return
			}
		}
		store.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for bridge ack %s", commandID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForBridgePhoneReport(t *testing.T, store *bridgeCommandStore, commandID string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		store.mu.Lock()
		for _, report := range store.phoneReports {
			if report.LastCommandID == commandID {
				store.mu.Unlock()
				return
			}
		}
		store.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for bridge phone report %s", commandID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForBridgeControlCodeUpdate(t *testing.T, store *bridgeCommandStore, requestID string, status string) state.ControlCodeRequestUpdateInput {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		store.mu.Lock()
		for _, update := range store.codeUpdates {
			if update.RequestID == requestID && update.Status == status {
				store.mu.Unlock()
				return update
			}
		}
		store.mu.Unlock()
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for control-code update %s %s", requestID, status)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
