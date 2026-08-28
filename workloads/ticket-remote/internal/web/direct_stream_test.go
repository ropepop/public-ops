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
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
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
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":1,"phoneUptimeMillis":1000}`))
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

func TestDirectStreamExperimentalWarmStartKeepsRealEpochWhileWaitingForKeyFrame(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":720,"height":1482,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":1000}`))

	config, keyFrame := hub.experimentalWarmStart()
	if !strings.Contains(string(config), `"streamEpoch":7`) || strings.Contains(string(config), `"provisional":true`) || len(keyFrame) != 0 {
		t.Fatalf("experimental warm start without a fresh keyframe should keep the real config only: config=%q key=%x", string(config), keyFrame)
	}

	key := testTSF2FrameWithTimestamp(7, 1, true, 1000)
	hub.recordFrame(key)
	config, keyFrame = hub.experimentalWarmStart()
	if !strings.Contains(string(config), `"streamEpoch":7`) || parseTSF2(keyFrame).sequence != 1 {
		t.Fatalf("experimental warm start with a fresh keyframe should return the real config and keyframe")
	}

	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-10 * time.Second)
	hub.lastKeyFrameAt = time.Now().Add(-10 * time.Second)
	hub.mu.Unlock()
	config, keyFrame = hub.experimentalWarmStart()
	if !strings.Contains(string(config), `"streamEpoch":7`) || strings.Contains(string(config), `"provisional":true`) || len(keyFrame) != 0 {
		t.Fatalf("experimental warm start with a stale keyframe should keep the real config only: config=%q key=%x", string(config), keyFrame)
	}
}

func TestDirectStreamWarmEncoderReusableRequiresFreshSameEpochStream(t *testing.T) {
	hub := newDirectStreamHub()
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	if !hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, false, 10000)) {
		t.Fatal("fresh same-epoch frame was not accepted")
	}
	livePhone := phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"}
	if !hub.warmEncoderReusable(time.Now(), livePhone) {
		t.Fatal("fresh same-epoch streaming encoder should be reusable")
	}

	hub.mu.Lock()
	hub.lastFrameAt = time.Now().Add(-3 * time.Second)
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
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":0,"phoneUptimeMillis":10000}`))
	if hub.warmEncoderConfigReceivedAfter(baseline) {
		t.Fatal("provisional zero-epoch config must not satisfy the reconnect probe")
	}
	hub.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":9,"phoneUptimeMillis":10000}`))
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
