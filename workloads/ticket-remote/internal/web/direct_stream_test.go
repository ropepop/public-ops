package web

import (
	"context"
	"encoding/json"
	"fmt"
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

func testCanonicalSourceConfig(raw []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	payload["codec"] = canonicalStreamCodec
	payload["transport"] = canonicalStreamTransport
	payload["captureMode"] = canonicalStreamCaptureMode
	payload["captureSource"] = canonicalStreamCaptureSource
	payload["captureMethod"] = canonicalStreamCaptureMethod
	payload["rootCapture"] = true
	payload["width"] = canonicalStreamWidth
	payload["height"] = canonicalStreamHeight
	payload["sourceWidth"] = canonicalStreamSourceWidth
	payload["sourceHeight"] = canonicalStreamSourceHeight
	payload["sourceLeftCrop"] = canonicalStreamLeftCrop
	payload["sourceTopCrop"] = canonicalStreamTopCrop
	payload["sourceRightCrop"] = canonicalStreamRightCrop
	payload["sourceBottomCrop"] = canonicalStreamBottomCrop
	payload["sourceVisibleWidth"] = canonicalStreamVisibleWidth
	payload["sourceVisibleHeight"] = canonicalStreamVisibleHeight
	payload["bitrate"] = canonicalStreamBitrate
	payload["qualityProfile"] = canonicalStreamQualityProfile
	payload["colorCorrection"] = canonicalStreamColorCorrection
	payload["colorStandard"] = canonicalStreamColorStandard
	strict, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return strict
}

func testAllIntraConfig(raw []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(testCanonicalSourceConfig(raw), &payload); err != nil {
		return nil
	}
	payload["frameDependencyMode"] = frameDependencyModeAllIntra
	payload["fps"] = 1
	payload["sourceFps"] = 1
	payload["keyframeIntervalFrames"] = 1
	strict, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return strict
}

func setTestAllIntraConfig(hub *directStreamHub, raw []byte) bool {
	return hub.setConfig(testAllIntraConfig(raw))
}

func TestDirectStreamTracksConfigFramesAndTelemetry(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	second := testTSF2FrameWithTimestamp(1, 2, true, 1001)
	if !hub.recordFrame(key) {
		t.Fatal("keyframe should be accepted for latest-frame broadcast")
	}
	if !hub.recordFrame(second) {
		t.Fatal("second independent frame should be accepted for live latest-frame broadcast")
	}
	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["path"] != "https_websocket_h264" {
		t.Fatalf("unexpected path %v", snapshot["path"])
	}
	if snapshot["codec"] != canonicalStreamCodec || snapshot["transport"] != canonicalStreamTransport ||
		snapshot["captureMode"] != canonicalStreamCaptureMode || snapshot["rootCapture"] != true ||
		snapshot["width"] != canonicalStreamWidth || snapshot["height"] != canonicalStreamHeight {
		t.Fatalf("unexpected stream config %#v", snapshot)
	}
	if snapshot["activeVideoClients"] != 1 ||
		snapshot["framesForwarded"] != uint64(2) ||
		snapshot["keyframesForwarded"] != uint64(2) ||
		snapshot["deltaFramesForwarded"] != uint64(0) ||
		snapshot["sourceFramesReceived"] != uint64(2) ||
		snapshot["droppedStaleFrames"] != uint64(0) {
		t.Fatalf("unexpected counters %#v", snapshot)
	}
	if _, exists := snapshot["browserMediaError"]; exists {
		t.Fatal("viewer-local error leaked into shared source health")
	}
	if snapshot["streamVerdict"] == "live" || snapshot["live"] != false || snapshot["continuity"] != true {
		t.Fatalf("legacy TSF2 frame gained public live authority instead of continuity-only status: %#v", snapshot)
	}
	if snapshot["phoneConnected"] != true || snapshot["phoneStreamState"] != "streaming" {
		t.Fatalf("phone state missing %#v", snapshot)
	}
	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"transport":"hardware-h264-annexb"`) ||
		!strings.Contains(string(config), `"captureMode":"root_hardware_h264"`) {
		t.Fatalf("warm config missing: %q", string(config))
	}
	if meta := parseTSF2(keyFrame); !meta.ok || meta.epoch != 1 || meta.sequence != 2 || !meta.keyFrame {
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
	correlationID, _ := raw["correlationId"].(string)
	if correlationID != startupTraceCorrelationID(traceID) || !strings.HasPrefix(correlationID, "startup_") || len(correlationID) != len("startup_")+8 {
		t.Fatalf("startup trace snapshot correlation = %q, want bounded derivative of %q", correlationID, traceID)
	}
	if correlationID == traceID || strings.Contains(correlationID, "session-a") {
		t.Fatalf("startup trace snapshot correlation exposed an internal identifier: %q", correlationID)
	}
	if raw["reason"] != "index_prewarm" {
		t.Fatalf("startup trace reason = %#v, want index_prewarm", raw["reason"])
	}
	phases, ok := raw["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup trace phases missing: %#v", raw["phases"])
	}
	if len(phases) == 0 {
		t.Fatal("startup trace should contain timestamped phases")
	}
	previousElapsed := int64(-1)
	seenKeyframes := 0
	for _, phase := range phases {
		if phase.At.IsZero() || phase.ElapsedMillis < 0 || phase.ElapsedMillis < previousElapsed {
			t.Fatalf("invalid startup phase timing: %#v", phase)
		}
		previousElapsed = phase.ElapsedMillis
		if phase.Name == "first_forwarded_keyframe" {
			seenKeyframes++
		}
	}
	if seenKeyframes != 1 {
		t.Fatalf("first-only keyframe phase count = %d, want 1; phases=%#v", seenKeyframes, phases)
	}
	wire, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal startup trace: %v", err)
	}
	if !strings.Contains(string(wire), `"reason":"index_prewarm"`) ||
		!strings.Contains(string(wire), `"at":`) ||
		!strings.Contains(string(wire), `"elapsedMillis":`) {
		t.Fatalf("startup trace timing fields missing from wire payload: %s", wire)
	}
}

func TestForwardedFrameDoesNotCompleteStartupTraceBeforeBrowserPaint(t *testing.T) {
	hub := newDirectStreamHub()
	traceID := hub.beginStartupTrace("session-a", "video_socket_open")
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	server := &Server{direct: hub, clients: map[*client]struct{}{}}
	server.handlePhoneMessage(phone.Message{
		Binary:                    testTSF2FrameWithTimestamp(1, 1, true, 1000),
		StartupTraceCorrelationID: startupTraceCorrelationID(traceID),
	})

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	raw, ok := snapshot["startupTrace"].(map[string]any)
	if !ok {
		t.Fatalf("startup trace missing after forwarded frame: %#v", snapshot["startupTrace"])
	}
	if raw["id"] != traceID || raw["complete"] != false || raw["lastPhase"] != "first_keyframe_received_by_relay" {
		t.Fatalf("relay receipt should be nonterminal without a viewer writer: %#v", raw)
	}
	phases, ok := raw["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup phases missing: %#v", raw["phases"])
	}
	for _, phase := range phases {
		if phase.Name == "first_forwarded_keyframe" || phase.Name == "first_forwarded_frame" {
			t.Fatalf("frame was marked forwarded without a successful viewer write: %#v", phases)
		}
	}

	hub.completeStartupTrace("browser_first_rendered_frame", `{"frameSequence":1}`)
	snapshot = hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	raw, ok = snapshot["startupTrace"].(map[string]any)
	if !ok || raw["complete"] != true || raw["lastPhase"] != "browser_first_rendered_frame" {
		t.Fatalf("browser paint should complete forwarded startup trace: %#v", snapshot["startupTrace"])
	}
}

func TestRelayKeyframeReceiptCannotCrossStartupTraceCorrelation(t *testing.T) {
	hub := newDirectStreamHub()
	traceA := hub.startStartupTrace("session-a", "first_navigation")
	correlationA := startupTraceCorrelationID(traceA)
	traceB := hub.startStartupTrace("session-b", "replacement_navigation")
	correlationB := startupTraceCorrelationID(traceB)
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	server := &Server{direct: hub, clients: map[*client]struct{}{}}

	server.handlePhoneMessage(phone.Message{
		Binary:                    testTSF2FrameWithTimestamp(1, 1, true, 1000),
		StartupTraceCorrelationID: correlationA,
	})
	server.handlePhoneMessage(phone.Message{
		Binary: testTSF2FrameWithTimestamp(1, 2, true, 1001),
	})
	server.handlePhoneMessage(phone.Message{
		Binary:                    testTSF2FrameWithTimestamp(1, 3, true, 1002),
		StartupTraceCorrelationID: "startup_deadbeef",
	})
	snapshot := hub.snapshot(time.Now(), phone.Health{})
	raw := snapshot["startupTrace"].(map[string]any)
	if raw["id"] != traceB {
		t.Fatalf("replacement trace changed: %#v", raw)
	}
	for _, phase := range raw["phases"].([]streamStartupTracePhase) {
		if phase.Name == "first_keyframe_received_by_relay" {
			t.Fatalf("stale, empty, or mismatched relay correlation marked trace B: %#v", raw["phases"])
		}
	}

	server.handlePhoneMessage(phone.Message{
		Binary:                    testTSF2FrameWithTimestamp(1, 4, true, 1003),
		StartupTraceCorrelationID: correlationB,
	})
	snapshot = hub.snapshot(time.Now(), phone.Health{})
	raw = snapshot["startupTrace"].(map[string]any)
	seen := false
	for _, phase := range raw["phases"].([]streamStartupTracePhase) {
		if phase.Name == "first_keyframe_received_by_relay" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("matching relay correlation did not mark trace B: %#v", raw["phases"])
	}
}

func TestBrowserStartupRunOriginRequiresOnePrivateOriginAndTheFixedProtocol(t *testing.T) {
	valid := "ticket.startup.0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		name      string
		protocols string
		want      string
		clear     bool
	}{
		{name: "valid", protocols: "ticket.video.v1, " + valid, want: valid},
		{name: "missing", clear: true},
		{name: "origin_without_fixed_protocol", protocols: valid, clear: true},
		{name: "malformed", protocols: "ticket.video.v1, ticket.startup.bad", clear: true},
		{name: "ambiguous", protocols: "ticket.video.v1, " + valid + ", " + valid, clear: true},
		{name: "unknown", protocols: "ticket.video.v1, " + valid + ", other.protocol", clear: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://ticket.test/api/v1/stream", nil)
			if test.protocols != "" {
				req.Header.Set("Sec-WebSocket-Protocol", test.protocols)
			}
			got := browserStartupRunOrigin(req)
			if got.origin != test.want || got.clearRelayCorrelation != test.clear {
				t.Fatalf("origin evidence = %#v, want origin=%q clear=%t", got, test.want, test.clear)
			}
		})
	}
}

func TestOrphanedRelayCorrelationClearPreservesTheActiveStartupOwner(t *testing.T) {
	hub := newDirectStreamHub()
	relay := phone.NewRelay(phone.RelayConfig{})
	defer relay.Close()
	server := &Server{direct: hub, relay: relay}

	relay.SetStartupTraceCorrelationID("startup_aaaaaaaa")
	server.clearOrphanedRelayStartupCorrelation()
	if got := relay.StartupTraceCorrelationID(); got != "" {
		t.Fatalf("orphaned correlation was retained: %q", got)
	}

	traceB := hub.startStartupTrace("session-b", "current_navigation")
	correlationB := startupTraceCorrelationID(traceB)
	relay.SetStartupTraceCorrelationID(correlationB)
	server.clearOrphanedRelayStartupCorrelation()
	if got := relay.StartupTraceCorrelationID(); got != correlationB {
		t.Fatalf("active trace owner was cleared: %q", got)
	}

	relay.SetStartupTraceCorrelationID("startup_aaaaaaaa")
	server.clearOrphanedRelayStartupCorrelation()
	if got := relay.StartupTraceCorrelationID(); got != "" {
		t.Fatalf("correlation not owned by the active trace was retained: %q", got)
	}

	hub.completeStartupTraceForTrace(traceB, "browser_first_rendered_frame", "test complete")
	relay.SetStartupTraceCorrelationID(correlationB)
	server.clearOrphanedRelayStartupCorrelation()
	if got := relay.StartupTraceCorrelationID(); got != "" {
		t.Fatalf("valid stale correlation survived without an active owner: %q", got)
	}
}

func TestBrowserStartupMarkersJoinTheServerTrace(t *testing.T) {
	hub := newDirectStreamHub()
	server := &Server{direct: hub}
	client := &client{sessionID: "session-a"}
	client.startupTraceID = hub.beginStartupTrace(client.sessionID, "index_prewarm")
	noteTestKeyframeWritten(client, 7, 1, time.Now())
	hub.recordStartupPhaseForTrace(client.startupTraceID, "video_socket_accepted", "")
	sourceEpochMillis := time.Now().UnixMilli()
	sourcePerformanceByEvent := map[string]float64{
		"browser_opened":              0,
		"browser_configured":          11.25,
		"browser_first_frame_decoded": 22.5,
		"stream_first_rendered_frame": 33.75,
	}
	sendLifecycleMarker := func(event string) {
		t.Helper()
		detail, err := json.Marshal(map[string]any{
			"source":                    "browser_lifecycle",
			"sourceAtEpochMillis":       sourceEpochMillis,
			"sourceAtPerformanceMillis": sourcePerformanceByEvent[event],
			"frameEpoch":                7,
			"frameSequence":             1,
		})
		if err != nil {
			t.Fatal(err)
		}
		envelope, err := json.Marshal(map[string]any{
			"type": "client_log", "event": event, "detail": string(detail),
		})
		if err != nil {
			t.Fatal(err)
		}
		server.handleVideoStreamMessage(context.Background(), client, envelope)
	}
	for _, event := range []string{"browser_opened", "browser_configured", "browser_first_frame_decoded", "stream_first_rendered_frame"} {
		sendLifecycleMarker(event)
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	raw, ok := snapshot["startupTrace"].(map[string]any)
	if !ok || raw["complete"] != true {
		t.Fatalf("browser paint should complete startup trace: %#v", snapshot["startupTrace"])
	}
	if raw["phaseOrder"] != "server_receipt" || raw["sourceClockSemantics"] != "independent_diagnostic_clock" {
		t.Fatalf("startup trace ordering semantics are ambiguous: %#v", raw)
	}
	phases, ok := raw["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup phases missing: %#v", raw["phases"])
	}
	want := []string{"browser_navigation_started", "browser_configured", "browser_first_frame_decoded", "browser_first_frame_painted"}
	seen := map[string]streamStartupTracePhase{}
	for _, phase := range phases {
		seen[phase.Name] = phase
	}
	performanceByPhase := map[string]float64{
		"browser_navigation_started":  sourcePerformanceByEvent["browser_opened"],
		"browser_configured":          sourcePerformanceByEvent["browser_configured"],
		"browser_first_frame_decoded": sourcePerformanceByEvent["browser_first_frame_decoded"],
		"browser_first_frame_painted": sourcePerformanceByEvent["stream_first_rendered_frame"],
	}
	for _, name := range want {
		phase, ok := seen[name]
		if !ok {
			t.Fatalf("startup phase %q missing: %#v", name, phases)
		}
		if phase.SourceAtEpochMillis == nil || *phase.SourceAtEpochMillis != sourceEpochMillis {
			t.Fatalf("startup phase %q source epoch = %#v, want %d", name, phase.SourceAtEpochMillis, sourceEpochMillis)
		}
		if phase.SourceAtPerformanceMillis == nil || *phase.SourceAtPerformanceMillis != performanceByPhase[name] {
			t.Fatalf("startup phase %q source performance = %#v, want %v", name, phase.SourceAtPerformanceMillis, performanceByPhase[name])
		}
		if phase.At.IsZero() {
			t.Fatalf("startup phase %q lost its server receipt timestamp: %#v", name, phase)
		}
	}
	phaseIndex := map[string]int{}
	for index, phase := range phases {
		phaseIndex[phase.Name] = index
	}
	if phaseIndex["browser_navigation_started"] <= phaseIndex["video_socket_accepted"] {
		t.Fatalf("late browser navigation receipt was silently reordered: %#v", phases)
	}
}

func TestBrowserStartupSourceTimeRejectsUntrustedClockValues(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	valid := map[string]any{
		"sourceAtEpochMillis":       float64(now.UnixMilli() - 5),
		"sourceAtPerformanceMillis": 125.5,
	}
	if source, ok := browserStartupSourceTime(valid, now); !ok || source.epochMillis != now.UnixMilli()-5 || source.performanceMillis != 125.5 {
		t.Fatalf("valid browser source time rejected: %#v ok=%t", source, ok)
	}
	for name, detail := range map[string]map[string]any{
		"missing performance": {"sourceAtEpochMillis": float64(now.UnixMilli())},
		"stale epoch": {
			"sourceAtEpochMillis":       float64(now.Add(-25 * time.Hour).UnixMilli()),
			"sourceAtPerformanceMillis": 10.0,
		},
		"huge epoch": {
			"sourceAtEpochMillis":       1e300,
			"sourceAtPerformanceMillis": 10.0,
		},
		"unbounded performance": {
			"sourceAtEpochMillis":       float64(now.UnixMilli()),
			"sourceAtPerformanceMillis": float64((8 * 24 * time.Hour) / time.Millisecond),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if source, ok := browserStartupSourceTime(detail, now); ok {
				t.Fatalf("untrusted browser source time accepted: %#v", source)
			}
		})
	}
}

func TestStartupTraceWriterMarkerIgnoresOldViewerTrace(t *testing.T) {
	hub := newDirectStreamHub()
	oldTraceID := hub.beginStartupTrace("old-session", "video_socket_open")
	newTraceID := hub.beginStartupTrace("new-session", "video_socket_open")
	if oldTraceID == newTraceID {
		t.Fatal("different sessions should start different startup traces")
	}

	hub.recordStartupPhaseOnceForTrace(oldTraceID, "first_forwarded_keyframe", "sequence=1")
	hub.recordStartupPhaseOnceForTrace("", "first_forwarded_frame", "empty_trace")
	hub.recordStartupPhaseOnceForTrace(newTraceID, "first_forwarded_keyframe", "sequence=2")

	raw := hub.startupTraceSnapshot(time.Now())
	if raw["id"] != newTraceID || raw["lastPhase"] != "first_forwarded_keyframe" {
		t.Fatalf("unexpected current startup trace: %#v", raw)
	}
	phases, ok := raw["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup trace phases missing: %#v", raw["phases"])
	}
	forwarded := 0
	for _, phase := range phases {
		if phase.Name == "first_forwarded_frame" {
			t.Fatalf("empty originating trace ID marked the current startup trace: %#v", phases)
		}
		if phase.Name != "first_forwarded_keyframe" {
			continue
		}
		forwarded++
		if !strings.Contains(phase.Detail, "sequence=2") {
			t.Fatalf("old viewer marked the current startup trace: %#v", phases)
		}
	}
	if forwarded != 1 {
		t.Fatalf("forwarded keyframe markers = %d, want 1: %#v", forwarded, phases)
	}
}

func TestAuthenticatedIndexMarkerCannotCrossConcurrentTraceReplacement(t *testing.T) {
	hub := newDirectStreamHub()
	oldTraceID := hub.startStartupTrace("old-session", "authenticated_index_accepted")
	releaseOldMarker := make(chan struct{})
	oldMarkerDone := make(chan struct{})
	go func() {
		defer close(oldMarkerDone)
		<-releaseOldMarker
		hub.recordStartupPhaseOnceForTrace(oldTraceID, "authenticated_index_accepted", "membership=current")
	}()

	currentTraceID := hub.startStartupTrace("current-session", "authenticated_index_accepted")
	close(releaseOldMarker)
	<-oldMarkerDone

	raw := hub.startupTraceSnapshot(time.Now())
	if raw["id"] != currentTraceID {
		t.Fatalf("current startup trace = %#v, want %q", raw["id"], currentTraceID)
	}
	phases, _ := raw["phases"].([]streamStartupTracePhase)
	for _, phase := range phases {
		if phase.Name == "authenticated_index_accepted" {
			t.Fatalf("late authenticated marker crossed into the replacement trace: %#v", phases)
		}
	}
}

func TestAuthenticatedReloadReplacesUnfinishedSameSessionTraceAndSocketJoinsIt(t *testing.T) {
	hub := newDirectStreamHub()
	firstRun := newStartupRunOrigin()
	failedTraceID := hub.startStartupTraceForRun("same-session", firstRun, "authenticated_index_accepted")
	hub.recordStartupPhaseForTrace(failedTraceID, "prewarm_accepted", "first-navigation")

	reloadRun := newStartupRunOrigin()
	reloadTraceID := hub.startStartupTraceForRun("same-session", reloadRun, "authenticated_index_accepted")
	if reloadTraceID == failedTraceID {
		t.Fatal("an authenticated reload must replace an unfinished same-session startup trace")
	}
	hub.recordStartupPhaseOnceForTrace(reloadTraceID, "authenticated_index_accepted", "membership=current")
	if staleSocketTraceID := hub.joinStartupTraceForRun("same-session", firstRun, "video_socket_open"); staleSocketTraceID != "" {
		t.Fatalf("stale navigation socket joined replacement trace %q", staleSocketTraceID)
	}
	socketTraceID := hub.joinStartupTraceForRun("same-session", reloadRun, "video_socket_open")
	if socketTraceID != reloadTraceID {
		t.Fatalf("video socket trace = %q, want reload trace %q", socketTraceID, reloadTraceID)
	}

	raw := hub.startupTraceSnapshot(time.Now())
	phases, _ := raw["phases"].([]streamStartupTracePhase)
	seenAuthenticated := false
	seenSocketJoin := false
	for _, phase := range phases {
		switch phase.Name {
		case "authenticated_index_accepted":
			seenAuthenticated = true
		case "startup_trace_joined":
			seenSocketJoin = strings.Contains(phase.Detail, "video_socket_open")
		case "prewarm_accepted":
			t.Fatalf("replacement trace retained the failed navigation's phase: %#v", phases)
		}
	}
	if !seenAuthenticated || !seenSocketJoin {
		t.Fatalf("reload trace did not retain index-to-socket continuity: %#v", phases)
	}
}

func TestLateAuthenticatedPrewarmCannotMarkReplacementNavigation(t *testing.T) {
	hub := newDirectStreamHub()
	server := &Server{direct: hub}
	oldTraceID := hub.startStartupTrace("same-session", "authenticated_index_accepted")
	currentTraceID := hub.startStartupTrace("same-session", "authenticated_index_accepted")

	server.prewarmStreamForSession("same-session", "late_membership_result", oldTraceID)

	raw := hub.startupTraceSnapshot(time.Now())
	if raw["id"] != currentTraceID {
		t.Fatalf("current startup trace = %#v, want %q", raw["id"], currentTraceID)
	}
	phases, _ := raw["phases"].([]streamStartupTracePhase)
	for _, phase := range phases {
		if phase.Name == "prewarm_accepted" {
			t.Fatalf("late membership prewarm marked the replacement navigation: %#v", phases)
		}
	}
}

func TestBrowserStartupMarkersRespectOriginatingTrace(t *testing.T) {
	hub := newDirectStreamHub()
	server := &Server{direct: hub}
	oldClient := &client{sessionID: "old-session"}
	oldClient.startupTraceID = hub.beginStartupTrace(oldClient.sessionID, "video_socket_open")
	currentClient := &client{sessionID: "current-session"}
	currentClient.startupTraceID = hub.beginStartupTrace(currentClient.sessionID, "video_socket_open")
	noteTestKeyframeWritten(oldClient, 7, 1, time.Now())
	noteTestKeyframeWritten(currentClient, 7, 1, time.Now())

	markers := [][]byte{
		[]byte(`{"type":"client_log","event":"browser_first_frame_decoded","detail":"{\"frameEpoch\":7,\"frameSequence\":1}"}`),
		[]byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":7,\"frameSequence\":1}"}`),
	}
	for _, marker := range markers {
		server.handleVideoStreamMessage(context.Background(), oldClient, marker)
	}
	raw := hub.startupTraceSnapshot(time.Now())
	if raw["id"] != currentClient.startupTraceID || raw["complete"] == true {
		t.Fatalf("old browser completed the current startup trace: %#v", raw)
	}
	phases, _ := raw["phases"].([]streamStartupTracePhase)
	for _, phase := range phases {
		if phase.Name == "browser_first_frame_decoded" || phase.Name == "browser_first_frame_painted" || phase.Name == "browser_first_rendered_frame" {
			t.Fatalf("old browser marked the current startup trace: %#v", phases)
		}
	}

	for _, marker := range markers {
		server.handleVideoStreamMessage(context.Background(), currentClient, marker)
	}
	raw = hub.startupTraceSnapshot(time.Now())
	if raw["id"] != currentClient.startupTraceID || raw["complete"] != true || raw["lastPhase"] != "browser_first_rendered_frame" {
		t.Fatalf("originating browser did not complete its startup trace: %#v", raw)
	}
}

func TestBrowserStartupMarkersSurviveDiagnosticRateLimit(t *testing.T) {
	hub := newDirectStreamHub()
	server := &Server{direct: hub, browserClientLogWindow: time.Now(), browserClientLogCount: maxBrowserClientLogsPerMinute}
	client := &client{
		sessionID:            "session-a",
		clientLogWindowStart: time.Now(),
		clientLogCount:       maxBrowserClientLogsPerMinute,
	}
	client.startupTraceID = hub.beginStartupTrace(client.sessionID, "index_prewarm")
	server.handleVideoStreamMessage(context.Background(), client, []byte(`{"type":"client_log","event":"browser_configured","detail":"{\"mode\":\"annexb\"}"}`))

	snapshot := hub.snapshot(time.Now(), phone.Health{})
	raw, ok := snapshot["startupTrace"].(map[string]any)
	if !ok {
		t.Fatalf("startup trace missing: %#v", snapshot["startupTrace"])
	}
	phases, ok := raw["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup phases missing: %#v", raw["phases"])
	}
	for _, phase := range phases {
		if phase.Name == "browser_configured" {
			return
		}
	}
	t.Fatalf("browser lifecycle marker was dropped by diagnostic rate limit: %#v", phases)
}

func TestPendingStartupTraceElapsedUsesSnapshotTime(t *testing.T) {
	hub := newDirectStreamHub()
	hub.beginStartupTrace("session-a", "index_prewarm")
	startedAt := hub.startupTrace.StartedAt

	snapshot := hub.startupTraceSnapshot(startedAt.Add(streamStartupTraceTarget + time.Second))
	if got, ok := snapshot["elapsedMillis"].(int64); !ok || got < durationMillis(streamStartupTraceTarget+time.Second) {
		t.Fatalf("pending startup elapsed = %#v, want at least %d ms", snapshot["elapsedMillis"], durationMillis(streamStartupTraceTarget+time.Second))
	}
	if snapshot["overBudget"] != true {
		t.Fatalf("pending startup should be over budget at snapshot time: %#v", snapshot)
	}
}

func TestDirectStreamNormalDecoderTelemetryDoesNotMarkMediaError(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	hub.recordFrame(key)
	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if _, exists := snapshot["browserMediaError"]; exists {
		t.Fatalf("browser diagnostics should not be in source health: %#v", snapshot)
	}
	if snapshot["streamVerdict"] == "live" || snapshot["continuity"] != true {
		t.Fatalf("normal decoder telemetry should keep TSF2 continuity without live authority: %#v", snapshot)
	}
}

func TestDirectStreamWarmStartSendsProvisionalConfigBeforeFreshKeyFrame(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))

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

func TestDirectStreamWarmEncoderReusableRequiresFreshSameEpochStream(t *testing.T) {
	hub := newDirectStreamHub()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("fresh same-epoch frame was not accepted")
	}
	livePhone := phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"}
	if !hub.warmEncoderReusable(time.Now(), livePhone) {
		t.Fatal("fresh same-epoch streaming encoder should be reusable")
	}

	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-3100 * time.Millisecond)
	hub.lastFrameReceivedAt = hub.lastFrameAt
	hub.mu.Unlock()
	if hub.warmEncoderReusable(time.Now(), livePhone) {
		t.Fatal("stale encoder evidence must not skip the ordered cold start")
	}
	if hub.warmEncoderReusable(time.Now(), phone.Health{Connected: true, Desired: false, Viewers: 1, StreamState: "streaming"}) {
		t.Fatal("released stream demand must not be treated as reusable")
	}
}

func TestDirectStreamWarmEncoderConfigMustArriveDuringReconnectProbe(t *testing.T) {
	hub := newDirectStreamHub()
	baseline := hub.configGenerationSnapshot()
	if hub.warmEncoderConfigReceivedAfter(baseline) {
		t.Fatal("empty stream must not satisfy the reconnect probe")
	}
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":0,"phoneUptimeMillis":10000}`))
	if hub.warmEncoderConfigReceivedAfter(baseline) {
		t.Fatal("provisional zero-epoch config must not satisfy the reconnect probe")
	}
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":9,"phoneUptimeMillis":10000}`))
	if !hub.warmEncoderConfigReceivedAfter(baseline) {
		t.Fatal("new positive-epoch config should prove an active warm reconnect")
	}
	if hub.warmEncoderConfigReceivedAfter(hub.configGenerationSnapshot()) {
		t.Fatal("an older config must not satisfy a later reconnect probe")
	}
}

func TestDirectStreamRecoveryTelemetryDoesNotMarkMediaError(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	hub.recordFrame(key)
	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if _, exists := snapshot["recentBrowserEvents"]; exists {
		t.Fatalf("viewer-local recovery telemetry leaked into source health: %#v", snapshot)
	}
	if snapshot["streamVerdict"] == "live" || snapshot["continuity"] != true {
		t.Fatalf("recovery telemetry should keep TSF2 continuity without live authority: %#v", snapshot)
	}
}

func TestDirectStreamFreshTSF2FramesRetainContinuityDuringClientCountRace(t *testing.T) {
	hub := newDirectStreamHub()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
	key := testTSF2FrameWithTimestamp(1, 1, true, 1000)
	hub.recordFrame(key)

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})

	if snapshot["activeVideoClients"] != 0 {
		t.Fatalf("test setup expected no active video clients: %#v", snapshot)
	}
	if snapshot["streamVerdict"] == "live" || snapshot["live"] != false || snapshot["continuity"] != true {
		t.Fatalf("fresh TSF2 frame lost continuity or gained authority during client count race: %#v", snapshot)
	}
}

func TestDirectStreamWarmStartRejectsStoppedStream(t *testing.T) {
	hub := newDirectStreamHub()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":0}`))
	hub.recordFrame(testTSF2KeyFrameWithEpoch(0, 1, true))

	if config, keyFrame := hub.warmStart(); len(config) > 0 || len(keyFrame) > 0 {
		t.Fatalf("stopped stream should not warm-start stale media: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamWarmStartKeepsStaleKeyFrameOutButStillPreconfiguresDecoder(t *testing.T) {
	hub := newDirectStreamHub()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))
	hub.recordFrame(testTSF2KeyFrameWithEpoch(7, 1, true))
	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-10 * time.Second)
	hub.lastFrameReceivedAt = hub.lastFrameAt
	hub.mu.Unlock()

	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"streamEpoch":0`) || len(keyFrame) > 0 {
		t.Fatalf("stale stream should only warm-start provisional decoder config: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamDoesNotWarmStartKeyFramePastForwardAgeBudget(t *testing.T) {
	hub := newDirectStreamHub()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000))
	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-1300 * time.Millisecond)
	hub.lastFrameReceivedAt = hub.lastFrameAt
	hub.mu.Unlock()

	config, keyFrame := hub.warmStart()
	if !strings.Contains(string(config), `"streamEpoch":0`) || len(keyFrame) > 0 {
		t.Fatalf("warm start must not replay a keyframe older than the 1250ms forward budget: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamVerdictNeverLabelsOverThreeSecondsAsLive(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000))
	now := time.Now()
	hub.mu.Lock()
	hub.lastFrameAt = now.Add(-3100 * time.Millisecond)
	hub.lastFrameReceivedAt = hub.lastFrameAt
	hub.lastFrameVisualAgeMillis = int64((3100 * time.Millisecond) / time.Millisecond)
	hub.lastFrameVisualAgeKnown = true
	hub.mu.Unlock()

	snapshot := hub.snapshot(now, phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["streamVerdict"] == "live" {
		t.Fatalf("frames older than 3s must not be labeled live: %#v", snapshot)
	}
}

func TestDirectStreamReportsFreshnessStateFromVisualAge(t *testing.T) {
	cases := []struct {
		name           string
		visualAge      time.Duration
		wantFreshness  string
		wantLive       bool
		wantContinuity bool
		wantVerdict    string
	}{
		{name: "fresh", visualAge: 1250 * time.Millisecond, wantFreshness: "LIVE_FRESH", wantLive: true, wantContinuity: true, wantVerdict: "live"},
		{name: "fresh_plus_one", visualAge: 1251 * time.Millisecond, wantFreshness: "LIVE_OK", wantLive: false, wantContinuity: true, wantVerdict: "stale_recovering"},
		{name: "ok", visualAge: 2000 * time.Millisecond, wantFreshness: "LIVE_OK", wantLive: false, wantContinuity: true, wantVerdict: "stale_recovering"},
		{name: "ok_plus_one", visualAge: 2001 * time.Millisecond, wantFreshness: "DEGRADED", wantLive: false, wantContinuity: true, wantVerdict: "stale_recovering"},
		{name: "degraded", visualAge: 3000 * time.Millisecond, wantFreshness: "DEGRADED", wantLive: false, wantContinuity: true, wantVerdict: "stale_recovering"},
		{name: "stale", visualAge: 3001 * time.Millisecond, wantFreshness: "STALE", wantLive: false, wantContinuity: false, wantVerdict: "stale_recovering"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub := newDirectStreamHub()
			hub.addVideoClient()
			setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))
			recordTestBoundedPhoneClock(t, hub, 10_000_000)
			if !hub.recordFrame(testTSF3Frame(7, 1, true, 10_000_000)) {
				t.Fatal("fresh calibrated frame should be forwarded")
			}
			now := time.Now()
			hub.mu.Lock()
			hub.lastFrameAt = now
			hub.lastFrameVisualAgeMillis = int64(tc.visualAge / time.Millisecond)
			hub.lastFrameVisualAgeKnown = true
			hub.mu.Unlock()

			status := hub.streamStatus(now, phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
			if status["freshnessState"] != tc.wantFreshness {
				t.Fatalf("freshnessState = %#v want %q; status=%#v", status["freshnessState"], tc.wantFreshness, status)
			}
			if status["live"] != tc.wantLive {
				t.Fatalf("live = %#v want %v; status=%#v", status["live"], tc.wantLive, status)
			}
			if status["continuity"] != tc.wantContinuity {
				t.Fatalf("continuity = %#v want %v; status=%#v", status["continuity"], tc.wantContinuity, status)
			}
			if status["streamVerdict"] != tc.wantVerdict {
				t.Fatalf("streamVerdict = %#v want %q; status=%#v", status["streamVerdict"], tc.wantVerdict, status)
			}
		})
	}
}

func TestDirectStreamRequiresStrictAllIntraAndRejectsDelta(t *testing.T) {
	allIntra := newDirectStreamHub()
	allIntra.addVideoClient()
	if !allIntra.setConfig(browserVideoConfigMessage(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000}`)))) {
		t.Fatal("valid all-intra config was rejected")
	}
	if !allIntra.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("all-intra keyframe should be forwarded")
	}
	if allIntra.recordFrame(testTSF2FrameWithTimestamp(7, 2, false, 10000)) {
		t.Fatal("advertised all-intra delta must be rejected by the relay")
	}
	status := allIntra.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if status["frameDependencyMode"] != frameDependencyModeAllIntra || status["fps"] != 1 || status["sourceFps"] != 1 || status["keyframeIntervalFrames"] != 1 || status["allIntraConfigAdvertised"] != true || status["allIntraConfigValid"] != true {
		t.Fatalf("all-intra status contract is incomplete: %#v", status)
	}
	if status["framesForwarded"] != uint64(1) || status["deltaFramesForwarded"] != uint64(0) || status["droppedUnexpectedDeltaFrames"] != uint64(1) {
		t.Fatalf("unexpected all-intra delta accounting: %#v", status)
	}
	dropReasons, ok := status["dropReasons"].(map[string]uint64)
	if !ok || dropReasons["unexpected_delta"] != uint64(1) {
		t.Fatalf("unexpected-delta reason is missing: %#v", status)
	}

	missingMode := newDirectStreamHub()
	if missingMode.setConfig(browserVideoConfigMessage(testCanonicalSourceConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"fps":1,"sourceFps":1,"keyframeIntervalFrames":1}`)))) {
		t.Fatal("config without the explicit all-intra mode was accepted")
	}
	if missingMode.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("frame escaped a missing dependency contract")
	}
	missingStatus := missingMode.snapshot(time.Now(), phone.Health{})
	if missingStatus["frameDependencyMode"] != "" || missingStatus["allIntraConfigValid"] != false || missingStatus["deltaFramesForwarded"] != uint64(0) {
		t.Fatalf("missing dependency mode did not fail closed: %#v", missingStatus)
	}

	unknown := newDirectStreamHub()
	unknown.addVideoClient()
	if unknown.setConfig(browserVideoConfigMessage(testCanonicalSourceConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameDependencyMode":"gop","fps":1,"sourceFps":1,"keyframeIntervalFrames":1}`)))) {
		t.Fatal("unknown nonempty dependency mode must be rejected instead of receiving legacy compatibility")
	}
	if unknown.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("frames from an unknown dependency contract must fail closed")
	}
	unknownStatus := unknown.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if unknownStatus["frameDependencyMode"] != "gop" || unknownStatus["allIntraConfigValid"] != false || unknownStatus["streamVerdict"] != "invalid_source_config" || unknownStatus["allIntraConfigMismatchCount"] != uint64(1) || unknownStatus["droppedAllIntraConfigFrames"] != uint64(1) {
		t.Fatalf("unknown dependency mode was not visibly rejected: %#v", unknownStatus)
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
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))

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
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 8700)) {
		t.Fatal("bridge must drop phone frames older than the 1250ms forwarding budget")
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
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7}`))

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
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

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
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("initial calibrated frame should be forwarded")
	}
	hub.mu.Lock()
	hub.lastPhoneClockCalibrationAt = time.Now().Add(-(phoneClockCalibrationMaxAge + time.Second))
	hub.mu.Unlock()

	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 2, true, 10001)) {
		t.Fatal("continuous accepted frames should refresh phone clock calibration instead of going uncalibrated")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedUncalibratedFrames"] != uint64(0) || snapshot["framesForwarded"] != uint64(2) {
		t.Fatalf("accepted frame should keep calibration alive: %#v", snapshot)
	}
}

func TestDirectStreamDoesNotCalibrateDelayedFirstFrameToArrivalTimeAfterGap(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	hub.mu.Lock()
	hub.lastPhoneClockCalibrationAt = time.Now().Add(-(phoneClockCalibrationMaxAge + time.Second))
	hub.lastFrameAt = time.Time{}
	hub.lastFrameVisualAgeKnown = false
	hub.mu.Unlock()

	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 16500)) {
		t.Fatal("a frame after a quiet gap must not establish its own arrival-time clock mapping")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["droppedUncalibratedFrames"] != uint64(1) || snapshot["framesForwarded"] != uint64(0) {
		t.Fatalf("gap frame must remain uncalibrated until a fresh phone clock sample arrives: %#v", snapshot)
	}
	if snapshot["phoneClockCalibrated"] != false || snapshot["streamVerdict"] == "live" {
		t.Fatalf("gap frame must not restore live calibrated status by itself: %#v", snapshot)
	}
}

func TestDirectStreamTSF3RewritesEveryStageToWallClock(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	if !hub.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))) {
		t.Fatal("strict TSF3 config should be accepted")
	}
	recordTestBoundedPhoneClock(t, hub, 10_000_000)
	original := testTSF3Frame(7, 1, true, 10_000_000)
	forwarded, ok := hub.recordFrameForBroadcast(original)
	if !ok {
		t.Fatal("fresh calibrated TSF3 frame should be forwarded")
	}
	meta := parseTSF2(forwarded)
	if !meta.ok || meta.version != 3 || meta.headerBytes != tsf3HeaderBytes {
		t.Fatalf("forwarded TSF3 metadata invalid: %#v", meta)
	}
	if meta.captureAttemptID != 101 || meta.codecGeneration != 5 {
		t.Fatalf("TSF3 identity fields changed: %#v", meta)
	}
	stages := []uint64{meta.captureStartMicros, meta.captureCompleteMicros, meta.codecInputMicros, meta.codecOutputMicros, meta.recordEmissionMicros}
	if stages[0] < wallClockMicrosFloor {
		t.Fatalf("TSF3 stages were not rewritten to UTC wall time: %#v", stages)
	}
	for index := 1; index < len(stages); index++ {
		if stages[index]-stages[index-1] != 1_000 {
			t.Fatalf("TSF3 stage spacing changed: %#v", stages)
		}
	}
	if meta.calibrationGeneration == 0 || meta.uncertaintyMicros > uint64(phoneClockUncertaintyMax/time.Microsecond) {
		t.Fatalf("TSF3 calibration evidence invalid: generation=%d uncertainty=%d", meta.calibrationGeneration, meta.uncertaintyMicros)
	}
	if got := forwarded[tsf3HeaderBytes:]; string(got) != string(original[tsf3HeaderBytes:]) {
		t.Fatalf("TSF3 payload changed: got=%x want=%x", got, original[tsf3HeaderBytes:])
	}
}

func TestBoundedPhoneClockMappingUsesNTPInterval(t *testing.T) {
	offset, uncertainty, ok := boundedPhoneClockMapping(phone.ClockProbeResult{
		ServerSendUnixMicros:     1_000_000,
		PhoneReceiveUptimeMicros: 100_000,
		PhoneSendUptimeMicros:    102_000,
		ServerReceiveUnixMicros:  1_010_000,
	})
	if !ok || offset != 904_000 || uncertainty != 4_000 {
		t.Fatalf("bounded mapping = offset %d uncertainty %d ok=%t", offset, uncertainty, ok)
	}
}

func TestDirectStreamTSF3RequiresFreshBoundedClockProbe(t *testing.T) {
	hub := newDirectStreamHub()
	if !hub.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))) {
		t.Fatal("strict TSF3 config should be accepted")
	}
	frame := testTSF3Frame(7, 1, true, 10_000_000)
	if hub.recordFrame(frame) {
		t.Fatal("one-way config clock sample granted TSF3 freshness authority")
	}
	recordTestBoundedPhoneClock(t, hub, 10_000_000)
	if !hub.recordFrame(frame) {
		t.Fatal("fresh bounded four-timestamp probe did not grant TSF3 authority")
	}
	hub.mu.Lock()
	hub.lastBoundedPhoneClockAt = time.Now().Add(-(phoneClockCalibrationMaxAge + time.Millisecond))
	hub.mu.Unlock()
	if hub.recordFrame(testTSF3Frame(7, 2, true, 10_001_000)) {
		t.Fatal("accepted frames prolonged expired bounded TSF3 clock authority")
	}
}

func TestServerAppliesInitialPhoneClockProbeBeforeFirstTSF3Frame(t *testing.T) {
	hub := newDirectStreamHub()
	if !hub.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))) {
		t.Fatal("strict TSF3 config should be accepted")
	}
	server := &Server{direct: hub, clients: map[*client]struct{}{}}
	captureUptimeMicros := int64(10_000_000)
	nowMicros := time.Now().UnixMicro()
	server.handlePhoneMessage(phone.Message{ClockProbe: &phone.ClockProbeResult{
		ProbeID:                  "initial-probe",
		ServerSendUnixMicros:     nowMicros - 2_000,
		PhoneReceiveUptimeMicros: captureUptimeMicros + 5_000,
		PhoneSendUptimeMicros:    captureUptimeMicros + 6_000,
		ServerReceiveUnixMicros:  nowMicros,
	}})
	server.handlePhoneMessage(phone.Message{Binary: testTSF3Frame(7, 1, true, uint64(captureUptimeMicros))})

	status := hub.snapshot(time.Now(), phone.Health{})
	if status["framesForwarded"] != uint64(1) || status["lastFrameSequence"] != uint64(1) {
		t.Fatalf("initial bounded probe did not authorize the first TSF3 frame: %#v", status)
	}
}

func TestDirectStreamRejectsUnboundedClockProbeUncertainty(t *testing.T) {
	hub := newDirectStreamHub()
	now := time.Now().UnixMicro()
	if hub.recordBoundedPhoneClock(phone.ClockProbeResult{
		ServerSendUnixMicros:     now - int64(5*time.Second/time.Microsecond),
		PhoneReceiveUptimeMicros: 10_000_000,
		PhoneSendUptimeMicros:    10_001_000,
		ServerReceiveUnixMicros:  now,
	}) {
		t.Fatal("clock probe wider than the uncertainty budget was accepted")
	}
	snapshot := hub.snapshot(time.Now(), phone.Health{})
	if snapshot["phoneClockBoundedCalibrated"] != false || snapshot["phoneClockProbeRejections"] != uint64(1) {
		t.Fatalf("rejected bounded probe diagnostics missing: %#v", snapshot)
	}
}

func TestDirectStreamTSF3RejectsMalformedStagesAndTSF2StillWorks(t *testing.T) {
	tsf3 := newDirectStreamHub()
	if !tsf3.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))) {
		t.Fatal("strict TSF3 config should be accepted")
	}
	malformed := testTSF3Frame(7, 1, true, 10_000_000)
	putUint64(malformed[53:61], 10_010_000)
	putUint64(malformed[61:69], 10_003_000)
	if tsf3.recordFrame(malformed) {
		t.Fatal("out-of-order TSF3 stage timestamps must be rejected")
	}

	tsf2 := newDirectStreamHub()
	if !setTestAllIntraConfig(tsf2, []byte(`{"type":"config","streamEpoch":8,"phoneUptimeMillis":20000}`)) {
		t.Fatal("TSF2 config without an explicit envelope must remain compatible")
	}
	if !tsf2.recordFrame(testTSF2FrameWithTimestamp(8, 1, true, 20000)) {
		t.Fatal("TSF2 frame must remain compatible")
	}
}

func TestDirectStreamAdmissionIsStrictAndEpochFencesTheCache(t *testing.T) {
	hub := newDirectStreamHub()
	config := func(epoch, uptime uint64) bool {
		raw := fmt.Sprintf(`{"type":"config","streamEpoch":%d,"phoneUptimeMillis":%d,"frameDependencyMode":"all_intra","fps":1,"sourceFps":1,"keyframeIntervalFrames":1}`, epoch, uptime)
		return hub.setConfig(testAllIntraConfig([]byte(raw)))
	}
	if !config(7, 10_000) || !hub.recordFrame(testTSF2FrameWithTimestamp(7, 5, true, 10_000)) {
		t.Fatal("initial epoch/frame should be accepted")
	}
	for _, sequence := range []uint64{5, 4} {
		if hub.recordFrame(testTSF2FrameWithTimestamp(7, sequence, true, 10_001)) {
			t.Fatalf("non-increasing sequence %d was accepted", sequence)
		}
	}
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 9, true, 10_002)) {
		t.Fatal("a sequence gap to a newer independent frame should be accepted")
	}
	if !config(8, 10_003) {
		t.Fatal("newer epoch config should be accepted")
	}
	_, warmFrame := hub.warmStart()
	if len(warmFrame) != 0 {
		t.Fatal("old epoch frame remained in the warm cache")
	}
	status := hub.streamStatus(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if status["lastFrameSequence"] != uint64(0) || status["streamVerdict"] == "live" {
		t.Fatalf("new epoch status reused old visual evidence: %#v", status)
	}
	if config(7, 10_004) {
		t.Fatal("regressed config epoch was accepted without a phone restart")
	}
	if !hub.recordFrame(testTSF2FrameWithTimestamp(8, 1, true, 10_003)) {
		t.Fatal("first sequence in the new epoch should be accepted")
	}
	status = hub.streamStatus(time.Now(), phone.Health{})
	if status["droppedNonMonotonicFrames"] != uint64(2) || status["regressedConfigDropCount"] != uint64(1) {
		t.Fatalf("strict admission counters missing: %#v", status)
	}
}

func TestDirectStreamCanonicalPayloadCapHasExactTSF2AndTSF3Boundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		envelope string
		frame    func() []byte
	}{
		{name: "tsf2", envelope: "tsf2", frame: func() []byte { return testTSF2FrameWithTimestamp(7, 1, true, 10_000) }},
		{name: "tsf3", envelope: "tsf3", frame: func() []byte { return testTSF3Frame(7, 1, true, 10_000_000) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			hub := newDirectStreamHub()
			config := fmt.Sprintf(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":%q,"frameDependencyMode":"all_intra","fps":1,"sourceFps":1,"keyframeIntervalFrames":1}`, test.envelope)
			if !hub.setConfig(testAllIntraConfig([]byte(config))) {
				t.Fatal("config should be accepted")
			}
			if test.envelope == "tsf3" {
				recordTestBoundedPhoneClock(t, hub, 10_000_000)
			}
			exact := test.frame()
			meta := parseTSF2(exact)
			exact = append(exact, make([]byte, int(phone.MaxVideoPayloadBytes)-(len(exact)-meta.headerBytes))...)
			if !hub.recordFrame(exact) {
				t.Fatalf("exact 2 MiB payload was rejected; total=%d", len(exact))
			}
			oversize := append(append([]byte(nil), exact...), 0)
			if hub.recordFrame(oversize) {
				t.Fatal("payload above the canonical 2 MiB cap was accepted")
			}
			status := hub.streamStatus(time.Now(), phone.Health{})
			if status["droppedOversizeFrames"] != uint64(1) {
				t.Fatalf("oversize drop was not reported: %#v", status)
			}
		})
	}
}

func TestDirectStreamAdjustsBoundedFutureClockSkewInsteadOfDroppingFreshFrames(t *testing.T) {
	hub := newDirectStreamHub()
	hub.addVideoClient()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

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
	setTestAllIntraConfig(hub, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))

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

func TestDirectStreamTracksLatestIndependentFrameAcrossSequenceGap(t *testing.T) {
	hub := newDirectStreamHub()
	setTestAllIntraConfig(hub, []byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":41000}`))
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 41, true, 41000)) {
		t.Fatal("first independent frame should be accepted")
	}
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 99, true, 41001)) {
		t.Fatal("independent source sequence gap should be accepted")
	}

	snapshot := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if snapshot["lastFrameSequence"] != uint64(99) ||
		snapshot["lastKeyFrameSequence"] != uint64(99) ||
		snapshot["deltaFramesForwarded"] != uint64(0) ||
		snapshot["droppedStaleFrames"] != uint64(0) {
		t.Fatalf("latest frame sequences missing: %#v", snapshot)
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

func testTSF3Frame(epoch uint64, sequence uint64, keyFrame bool, captureStartMicros uint64) []byte {
	frame := make([]byte, tsf3HeaderBytes)
	copy(frame[0:4], []byte("TSF3"))
	if keyFrame {
		frame[4] = tsf2FlagKeyframe
	}
	values := []uint64{
		epoch,
		sequence,
		101,
		5,
		captureStartMicros,
		captureStartMicros + 1_000,
		captureStartMicros + 2_000,
		captureStartMicros + 3_000,
		captureStartMicros + 4_000,
		77,
		88,
	}
	for index, value := range values {
		start := 5 + index*8
		putUint64(frame[start:start+8], value)
	}
	return append(frame, 0x65, 0x88)
}

func recordTestBoundedPhoneClock(t *testing.T, hub *directStreamHub, captureUptimeMicros int64) {
	t.Helper()
	nowMicros := time.Now().UnixMicro()
	if !hub.recordBoundedPhoneClock(phone.ClockProbeResult{
		ProbeID:                  "test-probe",
		ServerSendUnixMicros:     nowMicros - 2_000,
		PhoneReceiveUptimeMicros: captureUptimeMicros + 5_000,
		PhoneSendUptimeMicros:    captureUptimeMicros + 6_000,
		ServerReceiveUnixMicros:  nowMicros,
	}) {
		t.Fatal("bounded phone clock fixture was rejected")
	}
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
