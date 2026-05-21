package web

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/phone"
)

func TestPixelTicketStateEventUpdatesHealthAndRejectsStaleEvents(t *testing.T) {
	store := newTicketMemoryStore(t, "http://phone.test")
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")

	if handled := server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":2,"ticketState":"raw_ticket","reason":"return_to_raw_complete","streamEpoch":9,"frameSequence":41,"requestId":"req-1","phoneUptimeMillis":1000}`)); !handled {
		t.Fatal("pixel ticket event was not handled")
	}
	if handled := server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":1,"ticketState":"generated_result","reason":"stale","streamEpoch":9,"frameSequence":99,"requestId":"req-1","phoneUptimeMillis":900}`)); !handled {
		t.Fatal("stale pixel ticket event should still be consumed")
	}

	direct := server.direct.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if direct["pixelTicketState"] != "raw_ticket" {
		t.Fatalf("stale event overwrote latest ticket state: %#v", direct)
	}
	if direct["pixelTicketEventSeq"] != int64(2) {
		t.Fatalf("ticket event sequence not tracked: %#v", direct)
	}

	snapshot, err := store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phone == nil || !strings.Contains(snapshot.Phone.HealthJSON, `"ticketState":"raw_ticket"`) || strings.Contains(snapshot.Phone.HealthJSON, `"generated_result"`) {
		t.Fatalf("stored phone health is not aligned with accepted Pixel event: %#v", snapshot.Phone)
	}
}

func TestPixelTicketStateEventAcceptsSequenceResetForNewStreamEpoch(t *testing.T) {
	store := newTicketMemoryStore(t, "http://phone.test")
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":5,"ticketState":"generated_result","reason":"old_epoch","streamEpoch":20,"frameSequence":9}`))
	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":1,"ticketState":"raw_ticket","reason":"new_pixel_service_epoch","streamEpoch":21,"frameSequence":1}`))

	direct := server.direct.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if direct["pixelTicketState"] != "raw_ticket" || direct["pixelTicketEventSeq"] != int64(1) || direct["pixelTicketEventStreamEpoch"] != int64(21) {
		t.Fatalf("new stream epoch did not reset Pixel event ordering: %#v", direct)
	}
}

func TestPixelTicketStateEventCompletesCleanupOnlyAfterRawTicketFrame(t *testing.T) {
	messages := make(chan string, 10)
	phoneResults := make(chan string, 10)
	phoneServer := newTicketPhoneControlCodeTestServer(t, messages, phoneResults)
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

	req := &controlCodeRequest{
		ID:             "req-cleanup",
		SessionID:      "requester-session",
		Email:          "ticket@jolkins.id.lv",
		Status:         controlCodeSucceeded,
		Reason:         "generated",
		RequestedAt:    time.Now().Add(-time.Second),
		StartedAt:      time.Now().Add(-time.Second),
		CompletedAt:    time.Now(),
		CleanupPending: true,
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeRunning = req.ID
	server.codeMu.Unlock()

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":3,"ticketState":"returning_raw","reason":"marker_delivered","requestId":"req-cleanup","streamEpoch":8,"frameSequence":12}`))
	server.codeMu.Lock()
	if !server.codeRequests[req.ID].CleanupPending || server.codeRunning == "" {
		server.codeMu.Unlock()
		t.Fatal("returning_raw event must keep cleanup pending and block the next request")
	}
	server.codeMu.Unlock()

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":4,"ticketState":"raw_ticket","reason":"return_to_raw_complete","requestId":"req-cleanup","streamEpoch":8,"frameSequence":15}`))
	server.codeMu.Lock()
	defer server.codeMu.Unlock()
	got := server.codeRequests[req.ID]
	if got.CleanupPending || server.codeRunning != "" || !got.CleanupOK || got.CleanupReason != "return_to_raw_complete" {
		t.Fatalf("raw_ticket event did not clear cleanup: running=%q request=%#v", server.codeRunning, got)
	}
}

func TestPixelGeneratedResultEventCompletesControlCodeFromMarker(t *testing.T) {
	messages := make(chan string, 10)
	phoneResults := make(chan string, 10)
	phoneServer := newTicketPhoneControlCodeTestServer(t, messages, phoneResults)
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

	req := &controlCodeRequest{
		ID:          "req-marker-event",
		SessionID:   "requester-session",
		Email:       "ticket@jolkins.id.lv",
		Status:      controlCodeRunning,
		RequestedAt: time.Now().Add(-time.Second),
		StartedAt:   time.Now().Add(-time.Second),
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeRunning = req.ID
	server.codeMu.Unlock()

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":7,"ticketState":"generated_result","reason":"generated","requestId":"req-marker-event","value":"12345","streamEpoch":8,"frameSequence":44,"minFrameSequence":44,"totalDurationMillis":321,"phases":{"phone_command_received":0,"popup_ready":184,"digits_typed":312,"ok_tapped":455,"result_first_visible":2988,"result_marker_requested":3015}}`))

	server.codeMu.Lock()
	defer server.codeMu.Unlock()
	got := server.codeRequests[req.ID]
	if got.Status != controlCodeSucceeded ||
		got.Value != "12345" ||
		got.StreamEpoch != 8 ||
		got.FrameSequence != 44 ||
		got.MinFrameSequence != 44 ||
		got.TotalDurationMillis != 321 ||
		got.Phases["phone_command_received"] != 0 ||
		got.Phases["popup_ready"] != 184 ||
		got.Phases["digits_typed"] != 312 ||
		got.Phases["ok_tapped"] != 455 ||
		got.Phases["result_first_visible"] != 2988 ||
		got.Phases["result_marker_requested"] != 3015 ||
		got.MarkerReceivedAt.IsZero() ||
		!got.CleanupPending {
		t.Fatalf("generated-result marker did not complete the request without browser capture: %#v", got)
	}
}

func TestPixelTicketEventIsMergedIntoSubsequentPhoneHealth(t *testing.T) {
	store := newTicketMemoryStore(t, "http://phone.test")
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":5,"ticketState":"generated_result","reason":"generated","requestId":"req-2","streamEpoch":10,"frameSequence":55}`))
	server.handlePhoneText([]byte(`{"type":"health","data":{"streamActive":true,"streamPipeline":{"captureMode":"root_hardware_h264"}}}`))

	snapshot, err := store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var health map[string]any
	if err := json.Unmarshal([]byte(snapshot.Phone.HealthJSON), &health); err != nil {
		t.Fatal(err)
	}
	data, _ := health["data"].(map[string]any)
	event, _ := data["ticketStateEvent"].(map[string]any)
	if event["ticketState"] != "generated_result" || event["reason"] != "generated" {
		t.Fatalf("health did not retain latest Pixel ticket event: %#v", health)
	}
}
