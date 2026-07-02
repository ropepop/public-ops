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
	logs    chan<- state.SafeOperationalLogInput
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

func (s *streamDesiredRecordingStore) AppendSafeOperationalLog(ctx context.Context, input state.SafeOperationalLogInput) error {
	if err := s.Store.AppendSafeOperationalLog(ctx, input); err != nil {
		return err
	}
	if s.logs != nil {
		select {
		case s.logs <- input:
		default:
		}
	}
	return nil
}

func newStreamControlTestServer(t *testing.T, recorder *streamDesiredRecordingStore) *Server {
	t.Helper()
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
	if recorder == nil {
		recorder = &streamDesiredRecordingStore{}
	}
	recorder.Store = store
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
	}, recorder, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func waitForSafeLog(t *testing.T, logs <-chan state.SafeOperationalLogInput, event string) state.SafeOperationalLogInput {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case got := <-logs:
			if got.Event == event {
				return got
			}
		case <-deadline:
			t.Fatalf("timed out waiting for safe operational log %q", event)
		}
	}
}

func TestRelayViewerPublishesDesiredActiveAndTrace(t *testing.T) {
	desired := make(chan state.StreamDesiredStateInput, 4)
	logs := make(chan state.SafeOperationalLogInput, 8)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{desired: desired, logs: logs})

	server.addRelayViewer("session-a")

	select {
	case got := <-desired:
		if !got.DesiredActive {
			t.Fatalf("desired active = false, want true: %#v", got)
		}
		if got.ViewerCount != 1 {
			t.Fatalf("viewer count = %d, want 1", got.ViewerCount)
		}
		if got.Reason != "relay_viewer_added" {
			t.Fatalf("reason = %q, want relay_viewer_added", got.Reason)
		}
		if got.UpdatedBy != "ticket_remote_relay" {
			t.Fatalf("updated by = %q, want ticket_remote_relay", got.UpdatedBy)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for active desired-state update")
	}

	got := waitForSafeLog(t, logs, "relay_viewer_added")
	if got.Source != "ticket_remote_relay" {
		t.Fatalf("log source = %q, want ticket_remote_relay", got.Source)
	}
	if !strings.Contains(got.DetailJSON, `"viewerCount":1`) {
		t.Fatalf("viewer trace missing viewer count: %s", got.DetailJSON)
	}
}

func TestKeyframeWhileDisconnectedWritesWarningTrace(t *testing.T) {
	logs := make(chan state.SafeOperationalLogInput, 16)
	server := newStreamControlTestServer(t, &streamDesiredRecordingStore{logs: logs})

	server.addRelayViewer("session-b")
	if err := server.sendPhoneKeyframe("test_keyframe"); err != nil {
		t.Fatal(err)
	}

	got := waitForSafeLog(t, logs, "keyframe_while_phone_disconnected")
	if got.Level != "warn" {
		t.Fatalf("log level = %q, want warn", got.Level)
	}
	if !strings.Contains(got.DetailJSON, `"streamState"`) {
		t.Fatalf("keyframe warning trace missing stream state: %s", got.DetailJSON)
	}
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

func TestPhoneCurrentReportIncludesStreamRecoveryStatus(t *testing.T) {
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
	phoneReports := make(chan state.PhoneCurrentReportInput, 1)
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
	}, &streamDesiredRecordingStore{Store: store, phone: phoneReports}, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	if !server.beginStreamAutoRecovery("stale_frame", time.Now()) {
		t.Fatal("expected recovery status to start")
	}
	if err := server.publishPhoneCurrentReport(context.Background(), time.Now(), "test_recovery_status"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-phoneReports:
		for _, needle := range []string{
			`"currentRecoveryStage":"queued"`,
			`"lastWatchdogAction":"recover_stream_command"`,
			`"lastRecoveryResult":"started"`,
			`"lastRecoveryReason":"stale_frame"`,
		} {
			if !strings.Contains(got.StatusJSON, needle) {
				t.Fatalf("phone status json missing %s: %s", needle, got.StatusJSON)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for phone-current report")
	}
}
