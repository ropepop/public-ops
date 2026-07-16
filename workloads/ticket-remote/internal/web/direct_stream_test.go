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
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	delta := testTSF2FrameWithTimestamp(1, 2, false, 1001)
	if !hub.recordFrame(key) {
		t.Fatal("keyframe should be accepted for latest-frame broadcast")
	}
	if !hub.recordFrame(delta) {
		t.Fatal("delta frame should be accepted for live latest-frame broadcast")
	}
	hub.recordClientTelemetry("h264_decoder_error", "bad keyframe")

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["path"] != "https_websocket_h264" {
		t.Fatalf("unexpected path %v", snapshot["path"])
	}
	if snapshot["codec"] != "avc1.42E01E" || snapshot["transport"] != "h264-annexb" {
		t.Fatalf("unexpected stream config %#v", snapshot)
	}
	if snapshot["activeVideoClients"] != 1 ||
		snapshot["framesForwarded"] != uint64(2) ||
		snapshot["keyframesForwarded"] != uint64(1) ||
		snapshot["deltaFramesForwarded"] != uint64(1) ||
		snapshot["sourceFramesReceived"] != uint64(2) ||
		snapshot["droppedStaleFrames"] != uint64(0) {
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
	if meta := parseTSF2(keyFrame); !meta.ok || meta.epoch != 1 || meta.sequence != 1 || !meta.keyFrame {
		t.Fatalf("warm keyframe mismatch: %x", keyFrame)
	}
}

func TestDirectStreamStartupTraceRecordsAndCompletes(t *testing.T) {
	hub := newDirectStreamHub()
	traceID := hub.beginStartupTrace("session-a", "index_prewarm")
	if traceID == "" {
		t.Fatal("startup trace id should be set")
	}
	hub.recordStartupPhase("video_socket_accepted", "session=session-a")
	hub.recordStartupPhaseOnce("first_forwarded_keyframe", "sequence=1")
	hub.recordStartupPhaseOnce("first_forwarded_keyframe", "sequence=2")
	hub.completeStartupTrace("browser_first_rendered_frame", `{"frameSequence":1}`)

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	raw, ok := snapshot["startupTrace"].(map[string]any)
	if !ok {
		t.Fatalf("startup trace missing from snapshot: %#v", snapshot["startupTrace"])
	}
	if raw["id"] != traceID || raw["complete"] != true || raw["lastPhase"] != "browser_first_rendered_frame" {
		t.Fatalf("unexpected startup trace: %#v", raw)
	}
	phases, ok := raw["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup trace phases missing: %#v", raw["phases"])
	}
	seenKeyframes := 0
	for _, phase := range phases {
		if phase.Name == "first_forwarded_keyframe" {
			seenKeyframes++
		}
	}
	if seenKeyframes != 1 {
		t.Fatalf("first-only keyframe phase count = %d, want 1; phases=%#v", seenKeyframes, phases)
	}
}

func TestDirectStreamNormalDecoderTelemetryDoesNotMarkMediaError(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
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

func TestDirectStreamWarmStartSendsProvisionalConfigBeforeFreshKeyFrame(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))

	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"streamEpoch":0`) || !strings.Contains(string(config), `"provisional":true`) || len(keyFrame) != 0 {
		t.Fatalf("warm start without a fresh keyframe should return provisional config only: config=%q key=%x", string(config), keyFrame)
	}

	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	hub.recordFrame(key)
	config, keyFrame = hub.warmStart()
	if !strings.Contains(string(config), `"streamEpoch":1`) || parseTSF2(keyFrame).sequence != 1 {
		t.Fatalf("warm start with a fresh keyframe should return config and keyframe")
	}
}

func TestDirectStreamRecoveryTelemetryDoesNotMarkMediaError(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
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
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	hub.recordFrame(key)

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["activeVideoClients"] != 0 {
		t.Fatalf("test setup expected no active video clients: %#v", snapshot)
	}
	if snapshot["streamVerdict"] != "live" {
		t.Fatalf("fresh streaming frames should stay live during client count race, got %q", snapshot["streamVerdict"])
	}
}

func TestDirectStreamWarmStartRejectsStoppedStream(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":0}`))
	hub.recordFrame(testTSF2KeyFrameWithEpoch(0, 1, true))

	if config, keyFrame := hub.warmStart(); len(config) > 0 || len(keyFrame) > 0 {
		t.Fatalf("stopped stream should not warm-start stale media: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamWarmStartKeepsStaleKeyFrameOutButStillPreconfiguresDecoder(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))
	hub.recordFrame(testTSF2KeyFrameWithEpoch(7, 1, true))
	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-10 * time.Second)
	hub.lastKeyFrameAt = time.Now().Add(-10 * time.Second)
	hub.mu.Unlock()

	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"streamEpoch":0`) || len(keyFrame) > 0 {
		t.Fatalf("stale stream should only warm-start provisional decoder config: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamDoesNotWarmStartKeyFramePastForwardAgeBudget(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000))
	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-800 * time.Millisecond)
	hub.lastKeyFrameAt = time.Now().Add(-800 * time.Millisecond)
	hub.mu.Unlock()

	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"streamEpoch":0`) || len(keyFrame) > 0 {
		t.Fatalf("warm start must not replay a keyframe older than the 750ms forward budget: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamVerdictNeverLabelsOverTwoSecondsAsLive(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000))
	now := time.Now()
	hub.mu.Lock()
	hub.lastFrameAt = now.Add(-2100 * time.Millisecond)
	hub.lastKeyFrameAt = now.Add(-2100 * time.Millisecond)
	hub.lastFrameVisualAgeMillis = int64((2100 * time.Millisecond) / time.Millisecond)
	hub.lastFrameVisualAgeKnown = true
	hub.mu.Unlock()

	snapshot := hub.snapshot(now, phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["streamVerdict"] == "live" {
		t.Fatalf("frames older than 2s must not be labeled live: %#v", snapshot)
	}
}

func TestDirectStreamReportsFreshnessStateFromVisualAge(t *testing.T) {
	cases := []struct {
		name          string
		visualAge     time.Duration
		wantFreshness string
		wantLive      bool
	}{
		{name: "fresh", visualAge: 1000 * time.Millisecond, wantFreshness: "LIVE_FRESH", wantLive: true},
		{name: "ok", visualAge: 1500 * time.Millisecond, wantFreshness: "LIVE_OK", wantLive: true},
		{name: "degraded", visualAge: 2000 * time.Millisecond, wantFreshness: "DEGRADED", wantLive: true},
		{name: "stale", visualAge: 2001 * time.Millisecond, wantFreshness: "STALE", wantLive: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := newDirectStreamHub()
			hub.addVideoClient()
			hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
			if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
				t.Fatal("fresh calibrated frame should be forwarded")
			}
			now := time.Now()
			hub.mu.Lock()
			hub.lastFrameAt = now
			hub.lastKeyFrameAt = now
			hub.lastFrameVisualAgeMillis = int64(tc.visualAge / time.Millisecond)
			hub.lastKeyFrameVisualAgeMillis = int64(tc.visualAge / time.Millisecond)
			hub.lastFrameVisualAgeKnown = true
			hub.lastKeyFrameVisualAgeKnown = true
			hub.mu.Unlock()

			status := hub.streamStatus(now, phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
			if status["freshnessState"] != tc.wantFreshness {
				t.Fatalf("freshnessState = %#v want %q; status=%#v", status["freshnessState"], tc.wantFreshness, status)
			}
			if status["live"] != tc.wantLive {
				t.Fatalf("live = %#v want %v; status=%#v", status["live"], tc.wantLive, status)
			}
			if tc.wantLive && status["streamVerdict"] != "live" {
				t.Fatalf("fresh frame should be live, got %#v", status)
			}
			if !tc.wantLive && status["streamVerdict"] == "live" {
				t.Fatalf("stale frame must not be live, got %#v", status)
			}
		})
	}
}

func TestDirectStreamPhoneClockResyncBudget(t *testing.T) {
	if phoneClockCalibrationMaxAge > 5*time.Second {
		t.Fatalf("phone clock calibration max age = %s, want <= 5s", phoneClockCalibrationMaxAge)
	}
	if phoneClockUncertainty > 250*time.Millisecond {
		t.Fatalf("phone clock uncertainty = %s, want <= 250ms", phoneClockUncertainty)
	}
}

func TestDirectStreamStatusServerTimeKeepsSubsecondPrecision(t *testing.T) {
	hub := newDirectStreamHub()
	now := time.Date(2026, 5, 13, 1, 2, 3, 123456789, time.UTC)

	status := hub.streamStatus(now, phone.Health{})

	if got, _ := status["serverTime"].(string); !strings.Contains(got, ".123456789Z") {
		t.Fatalf("serverTime = %q, want RFC3339Nano with subsecond precision", got)
	}
}

func TestDirectStreamDoesNotForwardFramesWithoutPhoneClockCalibration(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))

	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("frame without current phone clock calibration must not be forwarded as live media")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["streamVerdict"] == "live" {
		t.Fatalf("uncalibrated stream must not be labeled live: %#v", snapshot)
	}
}

func TestDirectStreamDropsPhoneFramesPastForwardAgeBudget(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 9000)) {
		t.Fatal("bridge must drop phone frames older than the 750ms forwarding budget")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedStaleFrames"] != uint64(1) {
		t.Fatalf("stale phone frame drop was not tracked: %#v", snapshot)
	}
	if snapshot["droppedForwardAgeFrames"] != uint64(1) {
		t.Fatalf("forward-age drop was not tracked separately: %#v", snapshot)
	}
	dropReasons, ok := snapshot["dropReasons"].(map[string]uint64)
	if !ok || dropReasons["forward_age"] != uint64(1) {
		t.Fatalf("forward-age drop reason missing: %#v", snapshot)
	}
}

func TestDirectStreamTracksUncalibratedFrameDrops(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))

	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("uncalibrated phone frame must not be forwarded")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedUncalibratedFrames"] != uint64(1) {
		t.Fatalf("uncalibrated drop was not tracked separately: %#v", snapshot)
	}
	dropReasons, ok := snapshot["dropReasons"].(map[string]uint64)
	if !ok || dropReasons["uncalibrated"] != uint64(1) {
		t.Fatalf("uncalibrated drop reason missing: %#v", snapshot)
	}
}

func TestDirectStreamTracksTimestampFrameDrops(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 0)) {
		t.Fatal("frame without a phone capture timestamp must not be forwarded")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedTimestampFrames"] != uint64(1) {
		t.Fatalf("timestamp drop was not tracked separately: %#v", snapshot)
	}
	dropReasons, ok := snapshot["dropReasons"].(map[string]uint64)
	if !ok || dropReasons["timestamp"] != uint64(1) {
		t.Fatalf("timestamp drop reason missing: %#v", snapshot)
	}
}

func TestDirectStreamRefreshesPhoneClockCalibrationFromAcceptedFrames(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("initial calibrated frame should be forwarded")
	}
	hub.mu.Lock()
	hub.lastPhoneClockCalibrationAt = time.Now().Add(-(phoneClockCalibrationMaxAge + time.Second))
	hub.mu.Unlock()

	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 2, false, 10001)) {
		t.Fatal("continuous accepted frames should refresh phone clock calibration instead of going uncalibrated")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedUncalibratedFrames"] != uint64(0) || snapshot["framesForwarded"] != uint64(2) {
		t.Fatalf("accepted frame should keep calibration alive: %#v", snapshot)
	}
}

func TestDirectStreamRecalibratesFromTimestampedFrameAfterGap(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	hub.mu.Lock()
	hub.lastPhoneClockCalibrationAt = time.Now().Add(-(phoneClockCalibrationMaxAge + time.Second))
	hub.lastFrameAt = time.Time{}
	hub.lastFrameVisualAgeKnown = false
	hub.mu.Unlock()

	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 16500)) {
		t.Fatal("timestamped frame after a quiet gap should refresh phone clock calibration")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedUncalibratedFrames"] != uint64(0) || snapshot["framesForwarded"] != uint64(1) {
		t.Fatalf("gap recovery frame should be accepted: %#v", snapshot)
	}
	if snapshot["phoneClockCalibrated"] != true || snapshot["streamVerdict"] != "live" {
		t.Fatalf("gap recovery frame should restore live calibrated status: %#v", snapshot)
	}
}

func TestDirectStreamAdjustsBoundedFutureClockSkewInsteadOfDroppingFreshFrames(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 11000)) {
		t.Fatal("bounded future skew should adjust clock calibration and forward the fresh frame")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedFutureClockFrames"] != uint64(0) || snapshot["futureClockAdjustments"] != uint64(1) {
		t.Fatalf("future skew adjustment was not tracked correctly: %#v", snapshot)
	}
	if snapshot["framesForwarded"] != uint64(1) {
		t.Fatalf("future-adjusted frame should be forwarded: %#v", snapshot)
	}
}

func TestDirectStreamRewritesFrameTimestampToEstimatedCaptureWallClock(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

	forwarded, ok := hub.recordFrameForBroadcast(testTSF2FrameWithTimestamp(7, 1, true, 10000))
	if !ok {
		t.Fatal("fresh calibrated frame should be forwarded")
	}
	meta := parseTSF2(forwarded)
	if !meta.ok {
		t.Fatal("forwarded frame lost TSF2 metadata")
	}
	nowMicros := uint64(time.Now().UnixMicro())
	if meta.timestamp+250_000 < nowMicros || meta.timestamp > nowMicros+250_000 {
		t.Fatalf("forwarded timestamp = %d, want close to bridge wall clock %d", meta.timestamp, nowMicros)
	}
}

func TestDirectStreamTracksLatestFrameSequenceAndForwardsDeltas(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"hardware-h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":41000}`))
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 41, true, 41000)) {
		t.Fatal("keyframe should be accepted")
	}
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 42, false, 41001)) {
		t.Fatal("delta frame should be forwarded")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["lastFrameSequence"] != uint64(42) ||
		snapshot["lastKeyFrameSequence"] != uint64(41) ||
		snapshot["deltaFramesForwarded"] != uint64(1) ||
		snapshot["droppedStaleFrames"] != uint64(0) {
		t.Fatalf("latest frame sequences missing: %#v", snapshot)
	}
}

func TestLatestVideoPendingFramePrefersQueuedKeyFrameOverNewerDelta(t *testing.T) {
	key := testTSF2KeyFrameWithEpoch(7, 41, true)
	delta := testTSF2KeyFrameWithEpoch(7, 42, false)

	frame, keyFrame := chooseLatestPendingVideoFrame(key, true, delta, false)
	if string(frame) != string(key) || !keyFrame {
		t.Fatalf("queued keyframe should not be replaced by a delta frame")
	}

	frame, keyFrame = chooseLatestPendingVideoFrame(delta, false, key, true)
	if string(frame) != string(key) || !keyFrame {
		t.Fatalf("new keyframe should replace a queued delta frame")
	}
}

func TestSlowViewerDropsDeltaBacklogUntilNextKeyFrame(t *testing.T) {
	now := time.Now()
	viewer := &client{
		videoWriteActive:   true,
		videoReadyForDelta: true,
		videoReadyEpoch:    7,
	}
	firstDelta := testTSF2FrameWithTimestamp(7, 42, false, 41042)
	secondDelta := testTSF2FrameWithTimestamp(7, 43, false, 41043)
	nextKey := testTSF2FrameWithTimestamp(7, 44, true, 41044)

	viewer.videoMu.Lock()
	viewer.queuePendingVideoFrameLocked(firstDelta, false, now)
	if len(viewer.videoPendingFrame) == 0 || viewer.videoPendingKeyFrame {
		viewer.videoMu.Unlock()
		t.Fatal("first delta should be the only pending frame while a slow viewer write is active")
	}
	viewer.queuePendingVideoFrameLocked(secondDelta, false, now.Add(10*time.Millisecond))
	if len(viewer.videoPendingFrame) != 0 || viewer.videoReadyForDelta || viewer.videoReadyEpoch != 0 {
		viewer.videoMu.Unlock()
		t.Fatalf("second queued delta must clear backlog and require a fresh keyframe: pending=%d ready=%v epoch=%d", len(viewer.videoPendingFrame), viewer.videoReadyForDelta, viewer.videoReadyEpoch)
	}
	viewer.noteVideoKeyFrameLocked(parseTSF2(nextKey))
	viewer.queuePendingVideoFrameLocked(nextKey, true, now.Add(20*time.Millisecond))
	if len(viewer.videoPendingFrame) == 0 || !viewer.videoPendingKeyFrame || !viewer.videoReadyForDelta || viewer.videoReadyEpoch != 7 {
		viewer.videoMu.Unlock()
		t.Fatalf("next keyframe should restart the slow viewer from a decodable point: pending=%d key=%v ready=%v epoch=%d", len(viewer.videoPendingFrame), viewer.videoPendingKeyFrame, viewer.videoReadyForDelta, viewer.videoReadyEpoch)
	}
	viewer.videoMu.Unlock()
}

func TestVideoPendingFrameAgeBudgetIsHardCapped(t *testing.T) {
	now := time.Now()
	if videoPendingFrameStale(now.Add(-249*time.Millisecond), now) {
		t.Fatal("pending frame just under the 250ms hard cap should still be usable")
	}
	if !videoPendingFrameStale(now.Add(-251*time.Millisecond), now) {
		t.Fatal("pending frame over the 250ms hard cap must be dropped")
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
	return testTSF2FrameWithTimestamp(epoch, sequence, keyFrame, sequence)
}

func testTSF2FrameWithTimestamp(epoch uint64, sequence uint64, keyFrame bool, timestampMillis uint64) []byte {
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
	putUint64(frame[21:29], timestampMillis*1000)
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
	store := NewMemoryStore()
	backends := []config.PhoneBackend{
		{ID: "lab-pixel", AttachName: "Lab Pixel", BaseURL: "http://lab.test"},
		{ID: "pixel", AttachName: "Pixel", BaseURL: "http://phone.test"},
	}
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "lab-pixel",
		PhoneBaseURL:    "http://lab.test",
		PhoneAttachName: "Lab Pixel",
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
			BackendID:         "lab-pixel",
			AttachName:        "Lab Pixel",
			BaseURL:           "http://lab.test",
			Backends:          backends,
			DefaultBackendID:  "lab-pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "lab-pixel",
		AttachName: "Lab Pixel",
		BaseURL:    "http://lab.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return server
}
