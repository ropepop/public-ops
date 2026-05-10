package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestDirectStreamTracksConfigFramesAndTelemetry(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1}`))
	key := append([]byte{'T', 'S', 'F', '2', 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{0x65, 0x88}...)
	delta := append([]byte{'T', 'S', 'F', '2', 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 2}, []byte{0x41, 0x9a}...)
	hub.recordFrame(key)
	hub.recordFrame(delta)
	hub.recordClientTelemetry("h264_decoder_error", "bad keyframe")

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["path"] != "https_websocket_h264" {
		t.Fatalf("unexpected path %v", snapshot["path"])
	}
	if snapshot["codec"] != "avc1.42E01E" || snapshot["transport"] != "h264-annexb" {
		t.Fatalf("unexpected stream config %#v", snapshot)
	}
	if snapshot["activeVideoClients"] != 1 || snapshot["framesForwarded"] != uint64(2) || snapshot["keyframesForwarded"] != uint64(1) {
		t.Fatalf("unexpected counters %#v", snapshot)
	}
	if snapshot["browserMediaError"] != "bad keyframe" {
		t.Fatalf("media error = %q", snapshot["browserMediaError"])
	}
	if snapshot["streamVerdict"] != "browser_decode_recovering" {
		t.Fatalf("stream verdict = %q", snapshot["streamVerdict"])
	}
	if snapshot["phoneConnected"] != true || snapshot["phoneStreamState"] != "streaming" {
		t.Fatalf("phone state missing %#v", snapshot)
	}
	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"transport":"h264-annexb"`) {
		t.Fatalf("warm config missing: %q", string(config))
	}
	if string(keyFrame) != string(key) {
		t.Fatalf("warm keyframe mismatch")
	}
}

func TestDirectStreamNormalDecoderTelemetryDoesNotMarkMediaError(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1}`))
	key := append([]byte{'T', 'S', 'F', '2', 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{0x65, 0x88}...)
	hub.recordFrame(key)
	hub.recordClientTelemetry("h264_decoder_mode", "avc_adapter_configured")

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["browserMediaError"] != "" {
		t.Fatalf("normal decoder telemetry should not become media error: %#v", snapshot)
	}
	if snapshot["streamVerdict"] != "live" {
		t.Fatalf("normal decoder telemetry should keep stream live, got %q", snapshot["streamVerdict"])
	}
}

func TestDirectStreamRecoveryTelemetryDoesNotMarkMediaError(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1}`))
	key := append([]byte{'T', 'S', 'F', '2', 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{0x65, 0x88}...)
	hub.recordFrame(key)
	for _, event := range []string{
		"decoder_error",
		"h264_decoder_recovery_reset",
		"h264_server_recover_requested",
		"server_stale_frames",
		"stale_video_frames",
		"video_stream_restart",
		"websocket_error",
	} {
		hub.recordClientTelemetry(event, "recovery detail")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["browserMediaError"] != "" {
		t.Fatalf("recovery telemetry should not become media error: %#v", snapshot)
	}
	if snapshot["streamVerdict"] != "live" {
		t.Fatalf("recovery telemetry should keep stream live, got %q", snapshot["streamVerdict"])
	}
}

func TestDirectStreamFreshFramesRemainLiveDuringClientCountRace(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1}`))
	key := append([]byte{'T', 'S', 'F', '2', 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{0x65, 0x88}...)
	hub.recordFrame(key)

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["activeVideoClients"] != 0 {
		t.Fatalf("test setup expected no active video clients: %#v", snapshot)
	}
	if snapshot["streamVerdict"] != "live" {
		t.Fatalf("fresh streaming frames should stay live during client count race, got %q", snapshot["streamVerdict"])
	}
}

func TestDirectStreamWarmStartRejectsStoppedOrStaleFrames(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":0}`))
	hub.recordFrame(testTSF2KeyFrameWithEpoch(0, 1, true))

	if config, keyFrame := hub.warmStart(); len(config) > 0 || len(keyFrame) > 0 {
		t.Fatalf("stopped stream should not warm-start stale media: config=%q key=%x", string(config), keyFrame)
	}

	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))
	hub.recordFrame(testTSF2KeyFrameWithEpoch(7, 1, true))
	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-10 * time.Second)
	hub.lastKeyFrameAt = time.Now().Add(-10 * time.Second)
	hub.mu.Unlock()

	if config, keyFrame := hub.warmStart(); len(config) > 0 || len(keyFrame) > 0 {
		t.Fatalf("old stream should not warm-start stale media: config=%q key=%x", string(config), keyFrame)
	}
}

func TestHealthReportsHTTPSH264Stream(t *testing.T) {
	server := newDirectTestServer(t)

	retiredReq := httptest.NewRequest(http.MethodPost, "/api/v1/webrtc/ice", strings.NewReader("{}"))
	retiredReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	retiredRec := httptest.NewRecorder()
	server.ServeHTTP(retiredRec, retiredReq)
	if retiredRec.Code != http.StatusNotFound {
		t.Fatalf("retired media endpoint status = %d body = %s", retiredRec.Code, retiredRec.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRec.Code, healthRec.Body.String())
	}
	var health map[string]any
	if err := json.NewDecoder(healthRec.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if _, ok := health["directStream"]; !ok {
		t.Fatalf("health missing directStream: %#v", health)
	}
	if _, ok := health["webrtcStream"]; ok {
		t.Fatalf("legacy WebRTC health key should not return: %#v", health)
	}
	direct, ok := health["directStream"].(map[string]any)
	if !ok || direct["path"] != "https_websocket_h264" {
		t.Fatalf("unexpected directStream health: %#v", health)
	}
	if _, ok := direct["streamVerdict"]; !ok {
		t.Fatalf("directStream missing streamVerdict: %#v", direct)
	}
}

func testTSF2KeyFrameWithEpoch(epoch uint64, sequence uint64, keyFrame bool) []byte {
	frame := make([]byte, 29)
	frame[0] = 'T'
	frame[1] = 'S'
	frame[2] = 'F'
	frame[3] = '2'
	if keyFrame {
		frame[4] = 1
	}
	putUint64(frame[5:13], epoch)
	putUint64(frame[13:21], sequence)
	putUint64(frame[21:29], sequence)
	return append(frame, 0x65, 0x88)
}

func putUint64(dst []byte, value uint64) {
	for i := 7; i >= 0; i-- {
		dst[i] = byte(value)
		value >>= 8
	}
}

func newDirectTestServer(t *testing.T) http.Handler {
	t.Helper()
	store := state.NewMemoryStore()
	backends := []config.PhoneBackend{
		{ID: "android-sim", AttachName: "Android simulator", BaseURL: "http://sim.test"},
		{ID: "pixel", AttachName: "Pixel", BaseURL: "http://phone.test"},
	}
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "android-sim",
		PhoneBaseURL:    "http://sim.test",
		PhoneAttachName: "Android simulator",
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
		Phone: config.PhoneConfig{
			BackendID:         "android-sim",
			AttachName:        "Android simulator",
			BaseURL:           "http://sim.test",
			Backends:          backends,
			DefaultBackendID:  "android-sim",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "android-sim",
		AttachName: "Android simulator",
		BaseURL:    "http://sim.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return server
}
