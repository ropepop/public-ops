package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

type streamDesiredRecordingStore struct {
	state.Store
	desired chan<- state.StreamDesiredStateInput
	phone   chan<- state.PhoneCurrentReportInput
}

func (s *streamDesiredRecordingStore) SetStreamDesiredState(ctx context.Context, input state.StreamDesiredStateInput) error {
	if err := s.Store.SetStreamDesiredState(ctx, input); err != nil {
		return err
	}
	if s.desired != nil {
		select {
		case s.desired <- input:
		default:
		}
	}
	return nil
}

func (s *streamDesiredRecordingStore) UpdatePhoneCurrentReport(ctx context.Context, input state.PhoneCurrentReportInput) error {
	if err := s.Store.UpdatePhoneCurrentReport(ctx, input); err != nil {
		return err
	}
	if s.phone != nil {
		select {
		case s.phone <- input:
		default:
		}
	}
	return nil
}

func TestReleaseStreamDesiredIfNoVideoClientsWritesIdleState(t *testing.T) {
	store := state.NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://127.0.0.1:1",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	desired := make(chan state.StreamDesiredStateInput, 2)
	phoneReports := make(chan state.PhoneCurrentReportInput, 2)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           "http://127.0.0.1:1",
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
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
			BackendID:  "pixel",
			AttachName: "Pixel",
			BaseURL:    "http://127.0.0.1:1",
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}},
		},
	}, &streamDesiredRecordingStore{Store: store, desired: desired, phone: phoneReports}, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	server.direct.addVideoClient()
	if server.releaseStreamDesiredIfNoVideoClients("test_active") {
		t.Fatal("active video client should prevent idle desired-state release")
	}
	select {
	case got := <-desired:
		t.Fatalf("unexpected desired-state release while video client active: %#v", got)
	case <-time.After(50 * time.Millisecond):
	}

	server.direct.removeVideoClient()
	if !server.releaseStreamDesiredIfNoVideoClients("test_idle") {
		t.Fatal("idle release did not publish desired-state update")
	}
	select {
	case got := <-desired:
		if got.DesiredActive {
			t.Fatalf("desired active = true, want false: %#v", got)
		}
		if got.ViewerCount != 0 {
			t.Fatalf("viewer count = %d, want 0", got.ViewerCount)
		}
		if got.Reason != "test_idle" {
			t.Fatalf("reason = %q, want test_idle", got.Reason)
		}
		if got.UpdatedBy != "ticket_remote_relay" {
			t.Fatalf("updated by = %q, want ticket_remote_relay", got.UpdatedBy)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle desired-state update")
	}
	select {
	case got := <-phoneReports:
		if got.StreamState != "idle" {
			t.Fatalf("phone stream state = %q, want idle", got.StreamState)
		}
		if got.DesiredActive {
			t.Fatalf("phone desired active = true, want false: %#v", got)
		}
		if !strings.Contains(got.StatusJSON, `"reason":"test_idle"`) {
			t.Fatalf("phone status json missing idle reason: %s", got.StatusJSON)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for idle phone-current report")
	}
}
