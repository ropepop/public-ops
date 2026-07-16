package web

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

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

func newTicketMemoryStore(t *testing.T, phoneURL string) state.Store {
	t.Helper()
	store := NewMemoryStore()
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
