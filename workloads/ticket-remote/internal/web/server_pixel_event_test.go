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

func TestPixelTicketEventIsMergedIntoSubsequentPhoneHealth(t *testing.T) {
	store := newTicketMemoryStore(t, "http://phone.test")
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")

	server.handlePhoneText([]byte(`{"type":"ticket_state_event","eventSeq":5,"ticketState":"generated_result","reason":"generated","requestId":"req-2","value":"5555","streamEpoch":10,"frameSequence":55}`))
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
	if _, exposed := event["value"]; exposed || strings.Contains(snapshot.Phone.HealthJSON, "5555") {
		t.Fatalf("control code must not be persisted in phone health: %#v", health)
	}
}
