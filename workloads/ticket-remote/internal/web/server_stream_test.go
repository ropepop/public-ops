package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

type blockingFirstStartCommandStore struct {
	state.Store
	started chan<- state.StreamCommandInput
	release <-chan struct{}
	blocked atomic.Bool
}

func (s *blockingFirstStartCommandStore) AppendStreamCommand(ctx context.Context, input state.StreamCommandInput) error {
	if input.CommandType == "start" && !s.blocked.Swap(true) {
		select {
		case s.started <- input:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.AppendStreamCommand(ctx, input)
}

func TestBrowserVideoSocketContextParsesOldPageQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stream?page_version=page-1&asset_version=asset-1&visibility=visible&restore_reason=pageshow&recovery_id=recover-1&frame_age_ms=13000&hidden_age_ms=8000&has_frame=1&configured=1&open_seq=7", nil)
	detail := browserVideoSocketContext(req)
	for key, want := range map[string]any{
		"pageVersion":     "page-1",
		"assetVersion":    "asset-1",
		"visibility":      "visible",
		"restoreReason":   "pageshow",
		"recoveryId":      "recover-1",
		"frameAgeMillis":  "13000",
		"hiddenAgeMillis": "8000",
		"hasFrame":        "1",
		"configured":      "1",
		"openSeq":         "7",
	} {
		if detail[key] != want {
			t.Fatalf("%s = %#v, want %#v in %#v", key, detail[key], want, detail)
		}
	}
}

func TestBrowserVideoSocketRequiresConfiguredSameOriginBeforeWake(t *testing.T) {
	phoneServer := httptest.NewServer(http.NotFoundHandler())
	defer phoneServer.Close()
	server, ticketServer, relay := newTicketRecoveryTestServer(t, phoneServer.URL)
	defer server.Close()
	defer ticketServer.Close()

	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	for _, test := range []struct {
		name   string
		origin string
	}{
		{name: "missing"},
		{name: "cross_origin", origin: "https://attacker.example"},
		{name: "wrong_scheme", origin: "https://ticket.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
			if test.origin != "" {
				header.Set("Origin", test.origin)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, response, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", &websocket.DialOptions{HTTPHeader: header})
			if conn != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "unexpected acceptance")
				t.Fatal("socket with invalid origin was accepted")
			}
			if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
				t.Fatalf("invalid origin response = %#v err=%v, want 403", response, err)
			}
		})
	}
	if health := relay.Snapshot(); health.Viewers != 0 || health.Desired {
		t.Fatalf("rejected origins woke the phone relay: %#v", health)
	}
}

func TestStreamPrewarmStartsPhoneRelayThroughWebsocket(t *testing.T) {
	phoneCommands := make(chan string, 8)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone stream websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stream/prewarm", strings.NewReader("{}"))
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("prewarm status = %d body = %s", rec.Code, rec.Body.String())
	}
	waitForPhoneSignalCounts(t, phoneCommands, map[string]int{"start": 1, "keyframe": 1}, "prewarm stream commands")
}

func TestStreamPrewarmQueuesStartBeforeKeyframe(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneTraceHeaders := make(chan string, 1)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		select {
		case phoneTraceHeaders <- r.Header.Get("X-Ticket-Startup-Trace"):
		default:
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)

	server.prewarmStreamForSession("ordered-startup", "ordered_test")
	readCommand := func() string {
		t.Helper()
		select {
		case message := <-phoneCommands:
			return message
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for ordered prewarm command")
			return ""
		}
	}
	firstMessage := readCommand()
	secondMessage := readCommand()
	first := phoneSignalType(firstMessage)
	second := phoneSignalType(secondMessage)
	if first != "start" || second != "keyframe" {
		t.Fatalf("prewarm commands were not ordered start then keyframe: first=%q second=%q", first, second)
	}
	var firstPayload map[string]any
	if err := json.Unmarshal([]byte(firstMessage), &firstPayload); err != nil {
		t.Fatalf("decode prewarm start payload: %v", err)
	}
	traceID, _ := firstPayload["traceId"].(string)
	if !strings.HasPrefix(traceID, "startup_") || len(traceID) != len("startup_")+8 {
		t.Fatalf("prewarm start did not carry opaque startup trace correlation: %#v", firstPayload)
	}
	select {
	case headerTraceID := <-phoneTraceHeaders:
		if headerTraceID != traceID {
			t.Fatalf("private relay startup trace = %q, durable start trace = %q", headerTraceID, traceID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for private relay startup trace header")
	}
}

func TestWarmPrewarmSkipsRedundantStartAndQueuesOneEarlyKeyframe(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.NotFoundHandler())
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	relay.AddViewer()
	defer relay.RemoveViewer()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":7,"phoneUptimeMillis":10000}`))
	if !server.direct.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10000)) {
		t.Fatal("fresh same-epoch frame was not accepted")
	}
	server.backgroundKeyframeMu.Lock()
	server.lastBackgroundKeyframeAt = time.Now()
	server.backgroundKeyframeMu.Unlock()

	server.queuePrewarmStreamCommandsForHealth(
		"warm_test",
		"startup_deadbeef",
		"",
		phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"},
		server.direct.configGenerationSnapshot(),
	)
	waitForPhoneMessage(t, phoneCommands, `"type":"keyframe"`)
	server.queuePrewarmStreamCommandsForHealth(
		"warm_test_duplicate",
		"startup_deadbeef",
		"",
		phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"},
		server.direct.configGenerationSnapshot(),
	)
	if extras := countPhoneSignalTypesWithin(phoneCommands, 250*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("warm prewarm ignored recent-background or same-trace dedupe: %v", extras)
	}
}

func TestWarmReconnectConfigSkipsStartAndQueuesOneEarlyKeyframe(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.NotFoundHandler())
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	relay.AddViewer()
	defer relay.RemoveViewer()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)

	baselineGeneration := server.direct.configGenerationSnapshot()
	go func() {
		time.Sleep(25 * time.Millisecond)
		setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":8,"phoneUptimeMillis":10000}`))
	}()
	server.queuePrewarmStreamCommandsForHealth(
		"warm_reconnect_test",
		"startup_deadbeef",
		"",
		phone.Health{Connected: false, Desired: true, Viewers: 1, StreamState: "connecting"},
		baselineGeneration,
	)
	server.queuePrewarmStreamCommandsForHealth(
		"warm_reconnect_duplicate",
		"startup_cafebabe",
		"",
		phone.Health{Connected: false, Desired: true, Viewers: 1, StreamState: "connecting"},
		baselineGeneration,
	)
	waitForPhoneMessage(t, phoneCommands, `"type":"keyframe"`)
	if extras := countPhoneSignalTypesWithin(phoneCommands, 250*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("warm reconnect emitted redundant commands: %v", extras)
	}
}

func TestWarmReconnectSocketBeforeConfigQueuesOneDurableKeyframe(t *testing.T) {
	server, phoneSignals, _ := newTicketVideoStreamTestServer(t)
	baselineGeneration := server.direct.configGenerationSnapshot()

	server.queuePrewarmStreamCommandsForHealth(
		"warm_socket_before_config",
		"startup_deadbeef",
		"",
		phone.Health{Connected: false, Desired: true, Viewers: 1, StreamState: "connecting"},
		baselineGeneration,
	)
	server.handlePhoneText(testAllIntraConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":12,"phoneUptimeMillis":10000}`)))

	waitForPhoneMessage(t, phoneSignals, `"type":"keyframe"`)
	if extras := countPhoneSignalTypesWithin(phoneSignals, 250*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("socket-before-config race emitted redundant commands: %v", extras)
	}
}

func TestWarmReconnectConnectedBeforeConfigQueuesOneDurableKeyframe(t *testing.T) {
	server, phoneSignals, _ := newTicketVideoStreamTestServer(t)
	baselineGeneration := server.direct.configGenerationSnapshot()

	go func() {
		time.Sleep(25 * time.Millisecond)
		server.handlePhoneText(testAllIntraConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":13,"phoneUptimeMillis":10000}`)))
	}()
	server.queuePrewarmStreamCommandsForHealth(
		"warm_connected_before_config",
		"startup_deadbeef",
		"",
		phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"},
		baselineGeneration,
	)

	waitForPhoneMessage(t, phoneSignals, `"type":"keyframe"`)
	if extras := countPhoneSignalTypesWithin(phoneSignals, 250*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("connected-before-config race emitted redundant commands: %v", extras)
	}
}

func TestWarmRelayReconnectConfigUsesKeyframeOnlyPath(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		config := testAllIntraConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"rootCapture":true,"streamEpoch":11,"phoneUptimeMillis":10000}`))
		if err := conn.Write(r.Context(), websocket.MessageText, config); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)

	server.prewarmStreamForSession("real-warm-reconnect", "real_warm_reconnect")
	waitForPhoneMessage(t, phoneCommands, `"type":"keyframe"`)
	if extras := countPhoneSignalTypesWithin(phoneCommands, 250*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("real warm relay reconnect emitted redundant commands: %v", extras)
	}
	health := relay.Snapshot()
	if !health.Connected || health.StreamState != "streaming" {
		t.Fatalf("real relay health did not reach streaming: %#v", health)
	}
	if phoneRelayNeedsSocketWakeKeyframe(health) {
		t.Fatalf("actual connected relay snapshot would queue a duplicate socket wake keyframe: %#v", health)
	}
}

func TestWarmReconnectWithoutActiveConfigFallsBackToOrderedStart(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.NotFoundHandler())
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	relay.AddViewer()
	defer relay.RemoveViewer()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)

	server.queuePrewarmStreamCommandsForHealth(
		"cold_after_probe",
		"startup_deadbeef",
		"",
		phone.Health{Connected: false, Desired: true, Viewers: 1, StreamState: "connecting"},
		server.direct.configGenerationSnapshot(),
	)
	first := phoneSignalType(waitForPhoneMessageText(t, phoneCommands, `"type":"start"`))
	second := phoneSignalType(waitForPhoneMessageText(t, phoneCommands, `"type":"keyframe"`))
	if first != "start" || second != "keyframe" {
		t.Fatalf("reconnect probe timeout changed ordered startup: first=%q second=%q", first, second)
	}
	if extras := countPhoneSignalTypesWithin(phoneCommands, 250*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("reconnect timeout emitted duplicate commands: %v", extras)
	}
}

func TestStreamingRelayStateDoesNotQueueSocketWakeKeyframe(t *testing.T) {
	if phoneRelayNeedsSocketWakeKeyframe(phone.Health{Connected: true, Desired: true, StreamState: "streaming"}) {
		t.Fatal("connected relay streaming state must not queue a duplicate socket wake keyframe")
	}
	if !phoneRelayNeedsSocketWakeKeyframe(phone.Health{Connected: true, Desired: true, StreamState: "connecting"}) {
		t.Fatal("non-streaming relay state should still request a wake keyframe")
	}
}

func TestWarmPrewarmWithStaleRelayFrameKeepsStartBeforeKeyframe(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.NotFoundHandler())
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	relay.AddViewer()
	defer relay.RemoveViewer()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)

	server.queuePrewarmStreamCommandsForHealth(
		"stale_warm_test",
		"startup_deadbeef",
		"",
		phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"},
		server.direct.configGenerationSnapshot(),
	)
	first := phoneSignalType(waitForPhoneMessageText(t, phoneCommands, `"type":"start"`))
	second := phoneSignalType(waitForPhoneMessageText(t, phoneCommands, `"type":"keyframe"`))
	if first != "start" || second != "keyframe" {
		t.Fatalf("stale warm evidence changed ordered startup: first=%q second=%q", first, second)
	}
}

func TestSocketBeforeCachedPrewarmKeepsOneOrderedStartAndKeyframe(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	commandStarted := make(chan state.StreamCommandInput, 1)
	releaseCommand := make(chan struct{})
	store := &blockingFirstStartCommandStore{
		Store:   newTicketMemoryStore(t, phoneServer.URL),
		started: commandStarted,
		release: releaseCommand,
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()

	const sessionID = "socket-before-prewarm"
	runOrigin := newStartupRunOrigin()
	traceID := server.direct.startStartupTraceForRun(sessionID, runOrigin, "cached_index")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialStreamTestClientForRun(t, ctx, ticketServer.URL, sessionID, runOrigin)
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	select {
	case <-commandStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("socket-led start did not reach blocked durable write")
	}
	prewarmDone := make(chan struct{})
	go func() {
		server.prewarmStreamForSession(sessionID, "cached_membership_complete", traceID)
		close(prewarmDone)
	}()
	if pending := countPhoneSignalTypesWithin(phoneCommands, 150*time.Millisecond); pending["start"] != 0 || pending["keyframe"] != 0 {
		t.Fatalf("media command committed before blocked start was released: %v", pending)
	}

	close(releaseCommand)
	first := waitForPhoneMessageText(t, phoneCommands, `"type":"start"`)
	second := waitForPhoneMessageText(t, phoneCommands, `"type":"keyframe"`)
	if phoneSignalType(first) != "start" || phoneSignalType(second) != "keyframe" {
		t.Fatalf("socket-led commands were not ordered: first=%s second=%s", first, second)
	}
	if extras := countPhoneSignalTypesWithin(phoneCommands, 200*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("socket/prewarm race emitted duplicate commands: %v", extras)
	}
	select {
	case <-prewarmDone:
	case <-time.After(2 * time.Second):
		t.Fatal("cached prewarm did not finish after ordered startup write")
	}
}

func TestCrossTracePrewarmSharesBlockedStartBarrier(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)
	commandStarted := make(chan state.StreamCommandInput, 1)
	releaseCommand := make(chan struct{})
	store := &blockingFirstStartCommandStore{
		Store:   newTicketMemoryStore(t, phoneServer.URL),
		started: commandStarted,
		release: releaseCommand,
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	server.addRelayViewer("barrier-demand")
	defer server.removeRelayViewer("barrier-demand")

	traceA := server.direct.startStartupTrace("session-a", "trace_a")
	server.queuePrewarmStreamCommands("trace_a", startupTraceCorrelationID(traceA), traceA)
	select {
	case <-commandStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first trace start did not reach blocked durable write")
	}
	traceB := server.direct.startStartupTrace("session-b", "trace_b")
	server.queuePrewarmStreamCommands("trace_b", startupTraceCorrelationID(traceB), traceB)
	server.sendBrowserVideoWarmStart(&client{startupTraceID: traceB})
	if pending := countPhoneSignalTypesWithin(phoneCommands, 150*time.Millisecond); pending["start"] != 0 || pending["keyframe"] != 0 {
		t.Fatalf("second trace overtook blocked global start barrier: %v", pending)
	}

	close(releaseCommand)
	first := waitForPhoneMessageText(t, phoneCommands, `"type":"start"`)
	second := waitForPhoneMessageText(t, phoneCommands, `"type":"keyframe"`)
	if phoneSignalType(first) != "start" || phoneSignalType(second) != "keyframe" {
		t.Fatalf("cross-trace commands were not ordered: first=%s second=%s", first, second)
	}
	if extras := countPhoneSignalTypesWithin(phoneCommands, 200*time.Millisecond); extras["start"] != 0 || extras["keyframe"] != 0 {
		t.Fatalf("cross-trace startup emitted duplicate commands: %v", extras)
	}
}

func TestPrewarmCommandMarkersStayWithOriginatingTrace(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

	commandStarted := make(chan state.StreamCommandInput, 1)
	releaseCommand := make(chan struct{})
	store := &blockingFirstStartCommandStore{
		Store:   newTicketMemoryStore(t, phoneServer.URL),
		started: commandStarted,
		release: releaseCommand,
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	defer server.releaseRetainedRelayViewer("origin-session")

	server.prewarmStreamForSession("origin-session", "blocked_origin")
	var startInput state.StreamCommandInput
	select {
	case startInput = <-commandStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("originating start command did not reach the blocked store")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(startInput.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode originating start payload: %v", err)
	}
	originCorrelation, _ := payload["traceId"].(string)
	originSnapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	originTrace := originSnapshot["startupTrace"].(map[string]any)
	if originCorrelation == "" || originTrace["correlationId"] != originCorrelation {
		t.Fatalf("blocked start payload correlation = %q, origin trace = %#v", originCorrelation, originTrace)
	}

	currentTraceID := server.direct.beginStartupTrace("replacement-session", "replacement_trace")
	close(releaseCommand)
	waitForPhoneMessage(t, phoneCommands, `"type":"keyframe"`)

	currentSnapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	currentTrace := currentSnapshot["startupTrace"].(map[string]any)
	if currentTrace["id"] != currentTraceID {
		t.Fatalf("replacement startup trace changed unexpectedly: %#v", currentTrace)
	}
	phases, _ := currentTrace["phases"].([]streamStartupTracePhase)
	for _, phase := range phases {
		if (phase.Name == "stream_start_command_queued" || phase.Name == "keyframe_command_queued" || phase.Name == "spacetime_command_written") && strings.Contains(phase.Detail, "blocked_origin") {
			t.Fatalf("originating prewarm command marked the replacement trace: %#v", phases)
		}
	}
}

func TestRepeatedPrewarmRefreshesNextRelayHandshakeTrace(t *testing.T) {
	phoneCommands := make(chan string, 16)
	phoneTraceHeaders := make(chan string, 4)
	releaseFirstConnection := make(chan struct{})
	var connectionCount atomic.Int32
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		connection := connectionCount.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		phoneTraceHeaders <- r.Header.Get("X-Ticket-Startup-Trace")
		if connection == 1 {
			select {
			case <-releaseFirstConnection:
				_ = conn.Close(websocket.StatusInternalError, "force trace refresh reconnect")
			case <-r.Context().Done():
			}
			return
		}
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: 10 * time.Millisecond, ReconnectMaxDelay: 10 * time.Millisecond,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	defer server.releaseRetainedRelayViewer("same-session")

	server.prewarmStreamForSession("same-session", "first_trace")
	firstSnapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	firstTrace := firstSnapshot["startupTrace"].(map[string]any)
	firstCorrelation, _ := firstTrace["correlationId"].(string)
	select {
	case header := <-phoneTraceHeaders:
		if header != firstCorrelation {
			t.Fatalf("first relay header = %q, trace correlation = %q", header, firstCorrelation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first prewarm relay handshake did not connect")
	}

	server.direct.completeStartupTrace("test_complete", "first_trace")
	server.phoneStartMu.Lock()
	server.lastPhoneHTTPStartAt = time.Time{}
	server.phoneStartMu.Unlock()
	server.prewarmStreamForSession("same-session", "second_trace")
	secondSnapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	secondTrace := secondSnapshot["startupTrace"].(map[string]any)
	secondCorrelation, _ := secondTrace["correlationId"].(string)
	if secondCorrelation == "" || secondCorrelation == firstCorrelation {
		t.Fatalf("second prewarm correlation = %q, first = %q", secondCorrelation, firstCorrelation)
	}
	close(releaseFirstConnection)

	select {
	case header := <-phoneTraceHeaders:
		if header != secondCorrelation {
			t.Fatalf("reconnected relay header = %q, latest trace correlation = %q", header, secondCorrelation)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not reconnect with the repeated prewarm trace")
	}
}

func TestAuthenticatedIndexPrewarmsPhoneRelayBeforeBrowserVideoSocket(t *testing.T) {
	phoneCommands := make(chan string, 8)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone stream websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d body = %s", rec.Code, rec.Body.String())
	}
	waitForPhoneSignalCounts(t, phoneCommands, map[string]int{"start": 1, "keyframe": 1}, "authenticated index prewarm commands")
}

func TestStreamPrewarmStartsPhoneByHTTPWithoutWaitingForWebsocketRelay(t *testing.T) {
	phoneCommands := make(chan string, 8)
	releaseWebsocket := make(chan struct{})

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			select {
			case <-releaseWebsocket:
			case <-r.Context().Done():
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	defer close(releaseWebsocket)
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/stream/prewarm", strings.NewReader("{}"))
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("prewarm status = %d body = %s", rec.Code, rec.Body.String())
	}

	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
}

func TestStreamPrewarmDoesNotDuplicateHTTPStartWhileStartIsInFlight(t *testing.T) {
	phoneCommands := make(chan string, 8)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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

	server.prewarmStreamForSession("same-session", "index_page_prewarm")
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
	server.prewarmStreamForSession("same-session", "page_boot")
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case message := <-phoneCommands:
			if strings.Contains(message, `"type":"start"`) {
				t.Fatalf("second prewarm duplicated the Spacetime phone start command: %s", message)
			}
		case <-deadline:
			return
		}
	}
}

func TestStreamPrewarmHTTPStartAllowsSlowPixelWake(t *testing.T) {
	phoneCommands := make(chan string, 8)
	releaseWebsocket := make(chan struct{})

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			select {
			case <-releaseWebsocket:
			case <-r.Context().Done():
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	defer close(releaseWebsocket)
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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

	server.prewarmStreamForSession("same-session", "index_page_prewarm")
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
}

func TestAuthenticatedIndexPrewarmWaitsForCurrentMembership(t *testing.T) {
	phoneCommands := make(chan string, 8)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone stream websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

	blockingStore := &blockingSnapshotStore{
		Store:           newTicketMemoryStore(t, phoneServer.URL),
		snapshotStarted: make(chan struct{}),
		releaseSnapshot: make(chan struct{}),
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, blockingStore, relay, phoneServer.URL)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		done <- rec
	}()

	select {
	case <-blockingStore.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("index request did not reach state lookup")
	}
	select {
	case command := <-phoneCommands:
		t.Fatalf("phone command was sent before current membership completed: %s", command)
	case <-time.After(250 * time.Millisecond):
	}
	close(blockingStore.releaseSnapshot)
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("index status = %d body = %s", rec.Code, rec.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("index response did not finish after state lookup was released")
	}
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
}

func TestAuthenticatedIndexSessionCookiePrewarmStartsPhone(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:           "spacetime",
			AuthCookieName: "ticket_remote_auth",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := server.auth.IssueServerSession(auth.Identity{
		Email:         "ticket@jolkins.id.lv",
		Subject:       "user_123",
		EmailVerified: true,
	}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("index status = %d body = %s", rec.Code, rec.Body.String())
	}
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
}

func TestAuthenticatedIndexUsesCachedStateBeforeStoreRefresh(t *testing.T) {
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := &blockingSnapshotStore{
		Store:           newTicketMemoryStore(t, phoneServer.URL),
		snapshotStarted: make(chan struct{}),
		releaseSnapshot: make(chan struct{}),
	}
	defer close(store.releaseSnapshot)
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
	freshSnapshot, err := store.Store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server.cacheSnapshot(freshSnapshot)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		done <- rec
	}()

	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("index status = %d body = %s", rec.Code, rec.Body.String())
		}
	case <-time.After(350 * time.Millisecond):
		t.Fatal("authenticated index waited for store refresh despite fresh cached state")
	}
}

func TestAuthenticatedIndexCachedPageDefersPrewarmUntilFreshMembership(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

	baseStore := newTicketMemoryStore(t, phoneServer.URL)
	blockingStore := &blockingSnapshotStore{
		Store:           baseStore,
		snapshotStarted: make(chan struct{}),
		releaseSnapshot: make(chan struct{}),
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, blockingStore, relay, phoneServer.URL)
	freshSnapshot, err := baseStore.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server.cacheSnapshot(freshSnapshot)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	startedAt := time.Now()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached index status = %d body = %s", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(startedAt); elapsed > 350*time.Millisecond {
		t.Fatalf("cached index waited for fresh prewarm membership: %s", elapsed)
	}
	select {
	case <-blockingStore.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("cached index did not start a fresh membership check")
	}
	select {
	case command := <-phoneCommands:
		t.Fatalf("phone prewarm started before fresh membership completed: %s", command)
	case <-time.After(150 * time.Millisecond):
	}
	if got := relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers before fresh membership completed = %d, want 0", got)
	}

	close(blockingStore.releaseSnapshot)
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
	traceSnapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	trace, ok := traceSnapshot["startupTrace"].(map[string]any)
	if !ok {
		t.Fatalf("startup trace missing after cached-index prewarm: %#v", traceSnapshot["startupTrace"])
	}
	phases, ok := trace["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup trace phases missing: %#v", trace["phases"])
	}
	seen := map[string]bool{}
	for _, phase := range phases {
		seen[phase.Name] = true
	}
	if !seen["authenticated_index_accepted"] || !seen["prewarm_accepted"] {
		t.Fatalf("cached-index prewarm lost authentication phase: %#v", phases)
	}
}

func TestCachedMembershipPrewarmStillRunsWhenUnrelatedSessionReplacesLatestTrace(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

	baseStore := newTicketMemoryStore(t, phoneServer.URL)
	blockingStore := &blockingSnapshotStore{
		Store:           baseStore,
		snapshotStarted: make(chan struct{}),
		releaseSnapshot: make(chan struct{}),
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, blockingStore, relay, phoneServer.URL)
	freshSnapshot, err := baseStore.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server.cacheSnapshot(freshSnapshot)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached index status = %d body = %s", rec.Code, rec.Body.String())
	}
	select {
	case <-blockingStore.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("cached index did not start a fresh membership check")
	}
	unrelatedTraceID := server.direct.startStartupTraceForRun(
		"unrelated-session",
		newStartupRunOrigin(),
		"unrelated_authenticated_index",
	)
	close(blockingStore.releaseSnapshot)
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)

	snapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	trace, ok := snapshot["startupTrace"].(map[string]any)
	if !ok || trace["id"] != unrelatedTraceID {
		t.Fatalf("unrelated current trace was replaced by cached prewarm: %#v", snapshot["startupTrace"])
	}
	phases, _ := trace["phases"].([]streamStartupTracePhase)
	for _, phase := range phases {
		if phase.Name == "authenticated_index_accepted" || phase.Name == "prewarm_accepted" || phase.Name == "spacetime_command_written" {
			t.Fatalf("cached prewarm contaminated unrelated trace: %#v", phases)
		}
	}
}

func TestRemovedMemberCachedPageCannotPrewarmPhone(t *testing.T) {
	phoneCommands := make(chan string, 8)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

	store := newTicketMemoryStore(t, phoneServer.URL)
	memberEmail := "removed@example.com"
	cached, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", memberEmail, state.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
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
			Mode:              "spacetime",
			AuthCookieName:    "ticket_remote_auth",
			SessionSigningKey: "test-signing-key",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	server.cacheSnapshot(cached)
	if _, err := store.RemoveMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", memberEmail); err != nil {
		t.Fatal(err)
	}
	token, _, err := server.auth.IssueServerSession(auth.Identity{Email: memberEmail}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached page status = %d body = %s", rec.Code, rec.Body.String())
	}
	select {
	case command := <-phoneCommands:
		t.Fatalf("removed member triggered phone prewarm: %s", command)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestVideoSocketWaitsForCurrentMembershipBeforePhoneWake(t *testing.T) {
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone stream websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := &blockingSnapshotStore{
		Store:           newTicketMemoryStore(t, phoneServer.URL),
		snapshotStarted: make(chan struct{}),
		releaseSnapshot: make(chan struct{}),
	}
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
	freshSnapshot, err := store.Store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server.cacheSnapshot(freshSnapshot)
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connReady := make(chan *websocket.Conn, 1)
	go func() {
		connReady <- dialStreamTestClient(t, ctx, ticketServer.URL, "cached-fast-video")
	}()

	select {
	case <-store.snapshotStarted:
	case <-time.After(time.Second):
		t.Fatal("video socket did not start current membership lookup")
	}
	select {
	case conn := <-connReady:
		_ = conn.Close(websocket.StatusNormalClosure, "unexpected early connection")
		t.Fatal("video socket was accepted from cached membership")
	case <-time.After(250 * time.Millisecond):
	}
	close(store.releaseSnapshot)

	var conn *websocket.Conn
	select {
	case conn = <-connReady:
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
	case <-time.After(2 * time.Second):
		t.Fatal("video socket did not connect after current membership completed")
	}
}

func TestStreamPrewarmHoldIsOnlyAStartupBridge(t *testing.T) {
	if streamPrewarmHold < 30*time.Second {
		t.Fatalf("stream prewarm hold = %s, want enough warm time for reloads and short reconnects", streamPrewarmHold)
	}
	if streamPrewarmHold >= streamDesiredIdleReleaseGrace {
		t.Fatalf("stream prewarm hold = %s, want below idle release grace %s", streamPrewarmHold, streamDesiredIdleReleaseGrace)
	}
}

func TestVideoWarmStartKeyFrameIsShared(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	keyFrame := testTSF2FrameWithTimestamp(1, 1, true, 10000)
	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))
	server.direct.recordFrame(keyFrame)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	got := readNextBinaryFrame(t, ctx, viewerConn)
	meta := parseTSF2(got)
	if !meta.ok || !meta.keyFrame || meta.epoch != 1 || meta.sequence != 1 {
		t.Fatalf("non-controller warm keyframe mismatch: got %x", got)
	}
}

func TestProvisionalWarmConfigIsSentWithoutStaleKeyframeWhileRelayDisconnected(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1}`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	readCtx, readCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer readCancel()
	for {
		typ, data, err := viewerConn.Read(readCtx)
		if err != nil {
			t.Fatal("browser stream did not receive provisional decoder config")
		}
		if typ == websocket.MessageBinary {
			t.Fatalf("browser stream received stale warm frame without a fresh keyframe: %x", data)
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["type"] == "config" {
			if msg["provisional"] != true || int64(msg["streamEpoch"].(float64)) != 0 {
				t.Fatalf("warm config should be provisional with epoch 0: %s", data)
			}
			return
		}
	}
}

func TestVideoClientLogsDoNotMutateSharedSourceHealth(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":7}`))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	_ = readNextTextMessageOfType(t, ctx, viewerConn, "config")
	if err := viewerConn.Write(ctx, websocket.MessageText, []byte(`{"type":"client_log","event":"stream_started","detail":"123"}`)); err != nil {
		t.Fatalf("write client log: %v", err)
	}

	time.Sleep(25 * time.Millisecond)
	snapshot := server.direct.snapshot(time.Now(), phone.Health{})
	if _, exists := snapshot["recentBrowserEvents"]; exists {
		t.Fatalf("viewer-local log leaked into shared source health: %#v", snapshot)
	}
	if _, exists := snapshot["browserMediaError"]; exists {
		t.Fatalf("viewer-local error leaked into shared source verdict: %#v", snapshot)
	}
}

func TestDirectSpacetimePresenceRejectsControlSocket(t *testing.T) {
	memoryStore := NewMemoryStore()
	if err := memoryStore.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://127.0.0.1:1",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	store := &spacetimeBackendCountingStore{Store: memoryStore}
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
			Mode:              "spacetime",
			OIDCIssuer:        "https://auth.spacetimedb.com/oidc",
			OIDCClientID:      "client_test",
			OIDCScope:         "openid profile email",
			OIDCRedirect:      "http://ticket.test/auth/callback",
			AuthCookieName:    "ticket_remote_auth",
			SessionSigningKey: "test-signing-key",
		},
		State: state.StoreConfig{
			Backend:           "spacetime",
			SpacetimeDatabase: "ticket-remote-prod-v3",
		},
		Phone: config.PhoneConfig{
			BackendID:  "pixel",
			AttachName: "Pixel",
			BaseURL:    "http://127.0.0.1:1",
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: "http://127.0.0.1:1"}},
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	if !server.usesDirectSpacetimePresence() {
		t.Fatal("test server should use direct Spacetime presence")
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()

	token, _, err := server.auth.IssueServerSession(auth.Identity{
		Email:         "ticket@jolkins.id.lv",
		Subject:       "user_123",
		EmailVerified: true,
	}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	header := http.Header{"Cookie": []string{"ticket_remote_auth=" + token}}
	conn, response, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "test complete")
		t.Fatal("direct Spacetime mode must reject the removed control socket")
	}
	if response == nil {
		t.Fatalf("control socket response missing, want %d", http.StatusGone)
	}
	if response.StatusCode != http.StatusGone {
		t.Fatalf("control socket response status = %d, want %d", response.StatusCode, http.StatusGone)
	}
}

func TestLiveFramesAreShared(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()
	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controllerRunOrigin := newStartupRunOrigin()
	server.direct.startStartupTraceForRun("controller-session", controllerRunOrigin, "controller_test")
	controllerConn := dialStreamTestClientForRun(t, ctx, ticketServer.URL, "controller-session", controllerRunOrigin)
	defer controllerConn.Close(websocket.StatusNormalClosure, "test complete")
	viewerRunOrigin := newStartupRunOrigin()
	server.direct.startStartupTraceForRun("viewer-session", viewerRunOrigin, "viewer_test")
	viewerConn := dialStreamTestClientForRun(t, ctx, ticketServer.URL, "viewer-session", viewerRunOrigin)
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")
	clientDeadline := time.Now().Add(time.Second)
	for {
		clients := server.clientSnapshot()
		ready := len(clients) >= 2
		for _, live := range clients {
			ready = ready && live.readyForVideoBroadcast()
		}
		if ready {
			break
		}
		if time.Now().After(clientDeadline) {
			t.Fatalf("browser clients were not config-ready before broadcast: %d", len(clients))
		}
		time.Sleep(5 * time.Millisecond)
	}

	frame := testTSF2KeyFrameWithEpoch(1, 77, true)
	server.broadcastFrame(frame)

	if got := readNextBinaryFrame(t, ctx, controllerConn); !bytes.Equal(got, frame) {
		t.Fatalf("controller frame = %q", string(got))
	}
	if got := readNextBinaryFrame(t, ctx, viewerConn); !bytes.Equal(got, frame) {
		t.Fatalf("viewer frame = %q", string(got))
	}
	controllerMarkers := []string{
		`{"type":"client_log","event":"browser_first_frame_decoded","detail":"{\"frameEpoch\":1,\"frameSequence\":77}"}`,
		`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":1,\"frameSequence\":77}"}`,
	}
	for _, marker := range controllerMarkers {
		if err := controllerConn.Write(ctx, websocket.MessageText, []byte(marker)); err != nil {
			t.Fatalf("write old browser lifecycle marker: %v", err)
		}
	}
	if err := controllerConn.Close(websocket.StatusNormalClosure, "old viewer complete"); err != nil {
		t.Fatalf("close old browser connection: %v", err)
	}
	oldViewerDeadline := time.Now().Add(time.Second)
	for {
		oldViewerPresent := false
		for _, liveClient := range server.clientSnapshot() {
			if liveClient.sessionID == "controller-session" {
				oldViewerPresent = true
			}
		}
		if !oldViewerPresent {
			break
		}
		if time.Now().After(oldViewerDeadline) {
			t.Fatal("old browser connection did not finish processing lifecycle markers")
		}
		time.Sleep(5 * time.Millisecond)
	}
	oldViewerSnapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
	oldViewerTrace, ok := oldViewerSnapshot["startupTrace"].(map[string]any)
	if !ok {
		t.Fatalf("current startup trace missing after old viewer closed: %#v", oldViewerSnapshot["startupTrace"])
	}
	if oldViewerTrace["complete"] == true {
		t.Fatalf("old viewer completed the current startup trace: %#v", oldViewerTrace)
	}
	oldViewerPhases, _ := oldViewerTrace["phases"].([]streamStartupTracePhase)
	for _, phase := range oldViewerPhases {
		if phase.Name == "browser_first_frame_decoded" || phase.Name == "browser_first_frame_painted" || phase.Name == "browser_first_rendered_frame" {
			t.Fatalf("old viewer marked the current startup trace: %#v", oldViewerPhases)
		}
	}

	for _, marker := range controllerMarkers {
		if err := viewerConn.Write(ctx, websocket.MessageText, []byte(marker)); err != nil {
			t.Fatalf("write browser lifecycle marker: %v", err)
		}
	}

	deadline := time.Now().Add(time.Second)
	for {
		snapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
		trace, ok := snapshot["startupTrace"].(map[string]any)
		if !ok {
			t.Fatalf("startup trace missing after viewer writes: %#v", snapshot["startupTrace"])
		}
		phases, ok := trace["phases"].([]streamStartupTracePhase)
		if !ok {
			t.Fatalf("startup trace phases missing after viewer writes: %#v", trace)
		}
		if trace["complete"] == true {
			phaseIndex := map[string]int{}
			for index, phase := range phases {
				if _, exists := phaseIndex[phase.Name]; !exists {
					phaseIndex[phase.Name] = index
				}
			}
			ordered := []string{
				"video_socket_accepted",
				"first_forwarded_keyframe",
				"first_forwarded_frame",
				"browser_first_frame_decoded",
				"browser_first_frame_painted",
				"browser_first_rendered_frame",
			}
			for index, phase := range ordered {
				got, exists := phaseIndex[phase]
				if !exists {
					t.Fatalf("startup phase %q missing after viewer write and paint: %#v", phase, phases)
				}
				if index > 0 && got <= phaseIndex[ordered[index-1]] {
					t.Fatalf("startup phases are not causally ordered %v: %#v", ordered, phases)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("viewer writer and browser paint did not complete startup trace: %#v", phases)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIndependentFramesAcceptSourceGapAndRejectDelta(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()
	server.handlePhoneText(testAllIntraConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`)))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	coldViewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "cold-viewer-session")

	server.handlePhoneMessage(phone.Message{Binary: testTSF2FrameWithTimestamp(1, 78, false, 10000)})

	readCtx, readCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	for {
		typ, _, err := coldViewerConn.Read(readCtx)
		if err != nil {
			break
		}
		if typ == websocket.MessageBinary {
			t.Fatal("unexpected delta frame reached a viewer")
		}
	}
	readCancel()
	_ = coldViewerConn.Close(websocket.StatusNormalClosure, "test complete")

	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")
	readyDeadline := time.Now().Add(time.Second)
	var viewer *client
	for {
		for _, live := range server.clientSnapshot() {
			if live.sessionID == "viewer-session" && live.readyForVideoBroadcast() {
				viewer = live
				break
			}
		}
		if viewer != nil {
			break
		}
		if time.Now().After(readyDeadline) {
			t.Fatal("viewer was not config-ready before keyframe broadcast")
		}
		time.Sleep(5 * time.Millisecond)
	}

	firstFrame := testTSF2FrameWithTimestamp(1, 79, true, 10001)
	gapFrame := testTSF2FrameWithTimestamp(1, 95, true, 10002)
	server.handlePhoneMessage(phone.Message{Binary: firstFrame})
	if got := readNextBinaryFrame(t, ctx, viewerConn); parseTSF2(got).sequence != 79 || !parseTSF2(got).keyFrame {
		t.Fatalf("viewer independent frame = %x", got)
	}
	feedback := streamFeedbackV2Fixture(1, viewer.videoConfigGenerationSnapshot(), 79, 79, 79, 79, true)
	if err := viewerConn.Write(ctx, websocket.MessageText, feedback); err != nil {
		t.Fatalf("ack first independent frame: %v", err)
	}
	ackDeadline := time.Now().Add(time.Second)
	for {
		viewer.videoMu.Lock()
		awaiting := viewer.videoReceiptAwaiting
		viewer.videoMu.Unlock()
		if !awaiting {
			break
		}
		if time.Now().After(ackDeadline) {
			t.Fatal("v2 receipt did not release the next independent frame")
		}
		time.Sleep(time.Millisecond)
	}
	server.handlePhoneMessage(phone.Message{Binary: gapFrame})
	if got := readNextTSF2Sequence(t, ctx, viewerConn, 95); !parseTSF2(got).keyFrame {
		t.Fatalf("viewer source-gap frame = %x", got)
	}
}

func TestVideoStreamDoesNotSendStreamStatus(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))
	server.direct.recordFrame(testTSF2FrameWithTimestamp(1, 1, true, 10000))

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	_ = readNextTextMessageOfType(t, ctx, viewerConn, "config")
	readCtx, readCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer readCancel()
	typ, data, err := viewerConn.Read(readCtx)
	if err == nil && typ == websocket.MessageText && strings.Contains(string(data), `"stream_status"`) {
		t.Fatalf("video stream must not send stream_status messages: %s", data)
	}
}

func TestLiveKeyframeCannotReachBrowserBeforeStartupConfigIsQueued(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()
	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))

	server.startupRunMu.Lock()
	locked := true
	defer func() {
		if locked {
			server.startupRunMu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn := dialStreamTestClient(t, ctx, ticketServer.URL, "config-gate-session")
	defer conn.Close(websocket.StatusNormalClosure, "test complete")
	deadline := time.Now().Add(time.Second)
	for len(server.clientSnapshot()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("browser client was not registered behind startup gate")
		}
		time.Sleep(5 * time.Millisecond)
	}

	server.broadcastFrame(testTSF2KeyFrameWithEpoch(1, 1, true))
	client := server.clientSnapshot()[0]
	client.videoMu.Lock()
	writerStartedBeforeConfig := client.writerWake != nil
	queuedBeforeConfig := len(client.videoQueue)
	client.videoMu.Unlock()
	if writerStartedBeforeConfig || queuedBeforeConfig != 0 {
		t.Fatalf("pre-config live keyframe started writer=%t queued=%d", writerStartedBeforeConfig, queuedBeforeConfig)
	}

	server.startupRunMu.Unlock()
	locked = false
	_ = readNextTextMessageOfType(t, ctx, conn, "config")
	server.broadcastFrame(testTSF2KeyFrameWithEpoch(1, 2, true))
	frame := readNextBinaryFrame(t, ctx, conn)
	if got := parseTSF2(frame).sequence; got != 2 {
		t.Fatalf("first browser keyframe sequence = %d, want post-config sequence 2", got)
	}
}

func TestLegacyVideoSocketCloseBeforeFirstPaintKeepsPublicOpenGrace(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn := dialStreamTestClient(t, ctx, ticketServer.URL, "legacy-session")
	if err := conn.Close(websocket.StatusNormalClosure, "close before first paint"); err != nil {
		t.Fatalf("close legacy browser socket: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		server.mu.Lock()
		viewerRefs := server.relayViewerRefs["legacy-session"]
		graceRetained := server.streamPrewarmTimers["legacy-session"] != nil
		server.mu.Unlock()
		if len(server.clientSnapshot()) == 0 && viewerRefs == 1 && graceRetained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy browser socket cleanup incomplete: refs=%d grace=%t", viewerRefs, graceRetained)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after legacy socket closed before first paint = %d, want grace viewer", got)
	}
	server.releaseRelayViewerPublicOpenGrace("legacy-session", "test_cleanup", "")
	if got := relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after legacy grace release = %d, want 0", got)
	}
}

func TestUnboundSameSessionSocketCannotReplaceOrReleaseCurrentRunGrace(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	const sessionID = "shared-session"
	runOrigin := newStartupRunOrigin()
	server.direct.startStartupTraceForRun(sessionID, runOrigin, "authenticated_index_accepted")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current := dialStreamTestClientForRun(t, ctx, ticketServer.URL, sessionID, runOrigin)
	defer current.Close(websocket.StatusNormalClosure, "test complete")
	legacy := dialStreamTestClient(t, ctx, ticketServer.URL, sessionID)

	server.mu.Lock()
	currentTimer := server.streamPrewarmTimers[sessionID]
	server.mu.Unlock()
	if currentTimer == nil {
		t.Fatal("current startup run did not retain public-open grace")
	}
	if err := legacy.Write(ctx, websocket.MessageText, []byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameSequence\":1}"}`)); err != nil {
		t.Fatalf("write legacy first-paint marker: %v", err)
	}
	if err := legacy.Close(websocket.StatusNormalClosure, "stale socket complete"); err != nil {
		t.Fatalf("close legacy socket: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		legacyPresent := false
		for _, live := range server.clientSnapshot() {
			if live.sessionID == sessionID && live.startupTraceID == "" {
				legacyPresent = true
			}
		}
		server.mu.Lock()
		retainedTimer := server.streamPrewarmTimers[sessionID]
		viewerRefs := server.relayViewerRefs[sessionID]
		server.mu.Unlock()
		if !legacyPresent && viewerRefs == 2 {
			if retainedTimer != currentTimer {
				t.Fatal("unbound same-session socket replaced the current run grace timer")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy same-session cleanup incomplete: present=%t refs=%d", legacyPresent, viewerRefs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCompletedRunSecondSocketCannotRecreatePublicOpenGrace(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	const sessionID = "shared-complete-session"
	runOrigin := newStartupRunOrigin()
	server.direct.startStartupTraceForRun(sessionID, runOrigin, "authenticated_index_accepted")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	painted := dialStreamTestClientForRun(t, ctx, ticketServer.URL, sessionID, runOrigin)
	defer painted.Close(websocket.StatusNormalClosure, "test complete")
	unpainted := dialStreamTestClientForRun(t, ctx, ticketServer.URL, sessionID, runOrigin)
	writerEvidenceDeadline := time.Now().Add(time.Second)
	for {
		clients := server.clientSnapshot()
		seeded := len(clients) >= 2
		for _, live := range clients {
			if live.sessionID == sessionID && live.startupTraceID != "" {
				noteTestKeyframeWritten(live, 7, 1, time.Now())
			} else {
				seeded = false
			}
		}
		if seeded {
			break
		}
		if time.Now().After(writerEvidenceDeadline) {
			t.Fatal("painted socket did not register for writer evidence")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := painted.Write(ctx, websocket.MessageText, []byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":7,\"frameSequence\":1}"}`)); err != nil {
		t.Fatalf("write first-paint marker: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot := server.direct.snapshot(time.Now(), relay.Snapshot())
		trace, _ := snapshot["startupTrace"].(map[string]any)
		server.mu.Lock()
		retained := server.streamPrewarmTimers[sessionID] != nil
		server.mu.Unlock()
		if trace["complete"] == true && !retained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first socket did not complete and release grace: trace=%#v retained=%t", trace, retained)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := unpainted.Close(websocket.StatusNormalClosure, "unpainted sibling closed"); err != nil {
		t.Fatalf("close second same-run socket: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		unpaintedPresent := false
		for _, live := range server.clientSnapshot() {
			if live.sessionID == sessionID && !live.firstVideoFrameRendered {
				unpaintedPresent = true
			}
		}
		server.mu.Lock()
		retained := server.streamPrewarmTimers[sessionID] != nil
		viewerRefs := server.relayViewerRefs[sessionID]
		server.mu.Unlock()
		if !unpaintedPresent && viewerRefs == 1 {
			if retained {
				t.Fatal("completed startup run recreated grace from its unpainted sibling socket")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("second socket cleanup incomplete: present=%t refs=%d retained=%t", unpaintedPresent, viewerRefs, retained)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestVideoRecoverStreamWithConnectedRelayOnlyRequestsKeyframe(t *testing.T) {
	phoneSignals := make(chan string, 64)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneSignals)

	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    phoneServer.URL,
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
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
			BaseURL:    phoneServer.URL,
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL}},
		},
	}, &recordingTicketStore{Store: store, commandSink: phoneSignals}, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}, "Origin": []string{"http://ticket.test"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	videoConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser video websocket: %v", err)
	}
	defer videoConn.Close(websocket.StatusNormalClosure, "test complete")

	waitForPhoneSignal(t, phoneSignals, "keyframe", "initial phone keyframe")
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)
	server.direct.mu.Lock()
	server.direct.lastVideoClientAt = time.Now().Add(-15 * time.Second)
	server.direct.mu.Unlock()
	if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"test_stale"}`)); err != nil {
		t.Fatalf("write recover_stream: %v", err)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "recover_stream", 250*time.Millisecond); got != 0 {
		t.Fatalf("recover_stream text on media socket should be ignored; got=%d", got)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "start", 250*time.Millisecond); got != 0 {
		t.Fatalf("connected recovery should not restart the phone stream; starts=%d", got)
	}
}

func TestVideoRecoverDuringStartupGraceOnlyRequestsKeyframe(t *testing.T) {
	_, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"first_frame_pending"}`)); err != nil {
		t.Fatal(err)
	}

	got := countPhoneSignalTypesWithin(phoneSignals, 300*time.Millisecond)
	if got["recover_stream"] != 0 {
		t.Fatalf("startup recovery text should be ignored on media socket; all signals=%v", got)
	}
	if got["start"] != 0 {
		t.Fatalf("startup recovery should not restart phone stream; signals=%v", got)
	}
}

func TestVideoKeyframeRequestsAreRateLimitedPerViewer(t *testing.T) {
	_, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 5; i++ {
		if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"keyframe","reason":"spam"}`)); err != nil {
			t.Fatal(err)
		}
	}

	got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond)
	if got != 0 {
		t.Fatalf("keyframe text on media socket should be ignored; got=%d", got)
	}
}

func TestVideoKeyframeRequestsDuringStartupBypassPerViewerCooldown(t *testing.T) {
	_, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"keyframe","reason":"startup_first"}`)); err != nil {
		t.Fatal(err)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond); got != 0 {
		t.Fatalf("startup keyframe text on media socket should be ignored; got=%d", got)
	}

	if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"keyframe","reason":"startup_second"}`)); err != nil {
		t.Fatal(err)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond); got != 0 {
		t.Fatalf("second startup keyframe text on media socket should be ignored; got=%d", got)
	}
}

func TestStartupKeyframeWaitsForConnectingRelay(t *testing.T) {
	var videoDials atomic.Int32
	firstVideoDialed := make(chan struct{}, 1)
	releaseVideoAccept := make(chan struct{})

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			videoDials.Add(1)
			select {
			case firstVideoDialed <- struct{}{}:
			default:
			}
			select {
			case <-releaseVideoAccept:
			case <-r.Context().Done():
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, make(chan string, 4))
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	server, ticketServer, relay := newTicketRecoveryTestServer(t, phoneServer.URL)
	defer ticketServer.Close()
	defer relay.Close()

	server.retainRelayViewerForPrewarm("test-visible-page", streamPrewarmHold)
	select {
	case <-firstVideoDialed:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not begin phone connection")
	}

	if err := server.requestPhoneKeyframeNow("startup_connecting"); err != nil {
		t.Fatalf("startup keyframe returned error: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	close(releaseVideoAccept)

	if got := videoDials.Load(); got != 1 {
		t.Fatalf("startup keyframe restarted connecting relay: video dials = %d, want 1", got)
	}
}

func TestStartupRecoveryDoesNotRestartConnectingRelay(t *testing.T) {
	var videoDials atomic.Int32
	firstVideoDialed := make(chan struct{}, 1)
	releaseVideoAccept := make(chan struct{})

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			videoDials.Add(1)
			select {
			case firstVideoDialed <- struct{}{}:
			default:
			}
			select {
			case <-releaseVideoAccept:
			case <-r.Context().Done():
				return
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, make(chan string, 4))
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	server, ticketServer, relay := newTicketRecoveryTestServer(t, phoneServer.URL)
	defer ticketServer.Close()
	defer relay.Close()

	server.direct.addVideoClient()
	server.retainRelayViewerForPrewarm("test-visible-page", streamPrewarmHold)
	select {
	case <-firstVideoDialed:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not begin phone connection")
	}

	server.requestPhoneRecovery("startup_pending")
	time.Sleep(150 * time.Millisecond)
	close(releaseVideoAccept)

	if got := videoDials.Load(); got != 1 {
		t.Fatalf("startup recovery restarted connecting relay: video dials = %d, want 1", got)
	}
}

func TestPhoneConfigForActiveViewerRequestsFreshKeyframe(t *testing.T) {
	server, phoneSignals, _ := newTicketVideoStreamTestServer(t)
	now := time.Now()
	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000}`))
	server.direct.mu.Lock()
	server.direct.streamEpoch = 7
	server.direct.lastFrameAt = now
	server.direct.lastFrameEpoch = 7
	server.direct.lastFrameSequence = 77
	server.direct.lastFrameVisualAgeMillis = 100
	server.direct.lastFrameVisualAgeKnown = true
	server.direct.mu.Unlock()
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)

	config := testAllIntraConfig([]byte(`{"type":"config","streamEpoch":42}`))
	server.handlePhoneText(config)
	server.handlePhoneText(config)
	waitForPhoneSignal(t, phoneSignals, "keyframe", "phone config viewer-required keyframe")
	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond); got != 0 {
		t.Fatalf("repeated phone configs bypassed keyframe coalescing: %d extra requests", got)
	}
	newEpochConfig := testAllIntraConfig([]byte(`{"type":"config","streamEpoch":43}`))
	server.handlePhoneText(newEpochConfig)
	waitForPhoneSignal(t, phoneSignals, "keyframe", "new config epoch viewer-required keyframe")
}

func TestViewerRequiredKeyframePayloadUsesExistingRequesterScope(t *testing.T) {
	for _, reason := range []string{
		"phone_config_active_viewer",
		"browser_video_provisional_config",
		"browser_video_config_needed",
	} {
		payload := phoneKeyframeCommandPayload(reason, "startup_1234abcd")
		if payload["source"] != "browser" || payload["traceId"] != "startup_1234abcd" {
			t.Fatalf("viewer-required reason %q changed existing payload=%#v", reason, payload)
		}
		if _, exists := payload["viewerRequired"]; exists {
			t.Fatalf("viewer-required reason %q introduced a new wire field: %#v", reason, payload)
		}
	}
	for _, reason := range []string{"stale_video_frames", "stream_prewarm", "relay_watchdog"} {
		payload := phoneKeyframeCommandPayload(reason)
		if payload["source"] != "ticket_remote" {
			t.Fatalf("ordinary reason %q lost backward-compatible source: %#v", reason, payload)
		}
	}
}

func TestStreamFeedbackTransitionsAreDiagnosticOnly(t *testing.T) {
	hub := newDirectStreamHub()
	viewer := &client{videoEpoch: 7, videoLastWrittenSeq: 120}
	server := &Server{
		direct:  hub,
		clients: map[*client]struct{}{viewer: {}},
	}
	now := time.Unix(1_700_000_000, 0)
	server.handleStreamFeedback(viewer, streamFeedbackFixture(7, 120, 120, 120, 111, 0, 100), now)
	server.handleStreamFeedback(viewer, streamFeedbackFixture(7, 126, 125, 120, 1, 6, 3_100), now.Add(500*time.Millisecond))

	feedback := viewer.feedbackSnapshot()
	if feedback["feedbackCause"] != "decoder_queue_hard" || feedback["feedbackState"] != "congested" {
		t.Fatalf("per-viewer diagnostic pressure state=%#v", feedback)
	}
	viewer.videoMu.Lock()
	if len(viewer.videoQueue) != 0 || len(viewer.controlQueue) != 0 || viewer.videoLastWrittenSeq != 120 {
		viewer.videoMu.Unlock()
		t.Fatal("feedback changed fixed all-intra delivery")
	}
	viewer.videoMu.Unlock()
	server.handleStreamFeedback(viewer, streamFeedbackFixture(7, 127, 127, 127, 0, 0, 100), now.Add(time.Second))
	feedback = viewer.feedbackSnapshot()
	if feedback["feedbackCause"] != "healthy" || feedback["feedbackState"] != "flowing" {
		t.Fatalf("per-viewer diagnostic recovery state=%#v", feedback)
	}
}

func TestRequiredKeyframeWaitsBehindFinishingRequest(t *testing.T) {
	server, phoneSignals, _ := newTicketVideoStreamTestServer(t)

	server.backgroundKeyframeMu.Lock()
	server.backgroundKeyframeInFlight = true
	server.backgroundKeyframeMu.Unlock()

	const requirement = "phone_config_active_viewer:42"
	server.requestPhoneKeyframeWithRequirement("phone_config_active_viewer", requirement)
	server.requestPhoneKeyframeWithRequirement("phone_config_active_viewer", requirement)

	server.backgroundKeyframeMu.Lock()
	pending := len(server.backgroundKeyframePending)
	_, active := server.backgroundKeyframeActive[requirement]
	server.backgroundKeyframeMu.Unlock()
	if pending != 1 || !active {
		t.Fatalf("required keyframe was not retained exactly once behind finishing request: pending=%d active=%t", pending, active)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 100*time.Millisecond); got != 0 {
		t.Fatalf("deferred required keyframe ran before the active request finished: %d", got)
	}

	server.finishBackgroundKeyframe()
	waitForPhoneSignal(t, phoneSignals, "keyframe", "deferred required keyframe")
	waitForStartupCommandIdle(t, server)
	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond); got != 0 {
		t.Fatalf("duplicate retained requirement scheduled %d extra keyframes", got)
	}
}

func TestPhoneConfigReplaysMatchingCachedKeyframeToEveryViewer(t *testing.T) {
	hub := newDirectStreamHub()
	viewerA := &client{}
	viewerB := &client{}
	server := &Server{
		direct: hub,
		clients: map[*client]struct{}{
			viewerA: {},
			viewerB: {},
		},
	}
	config := testAllIntraConfig([]byte(`{"type":"config","streamEpoch":42,"phoneUptimeMillis":10000}`))
	hub.setConfig(config)
	keyframe := testTSF2FrameWithTimestamp(42, 77, true, 10000)
	if !hub.recordFrame(keyframe) {
		t.Fatal("matching cached keyframe fixture was rejected")
	}

	server.handlePhoneText(config)
	for index, viewer := range []*client{viewerA, viewerB} {
		viewer.videoMu.Lock()
		if len(viewer.controlQueue) != 1 || !viewer.controlQueue[0].config || viewer.controlQueue[0].epoch != 42 {
			viewer.videoMu.Unlock()
			t.Fatalf("viewer %d config queue mismatch: %#v", index, viewer.controlQueue)
		}
		if len(viewer.videoQueue) != 1 {
			viewer.videoMu.Unlock()
			t.Fatalf("viewer %d did not receive cached keyframe: %#v", index, viewer.videoQueue)
		}
		meta := viewer.videoQueue[0].meta
		viewer.videoMu.Unlock()
		if !meta.ok || !meta.keyFrame || meta.epoch != 42 || meta.sequence != 77 {
			t.Fatalf("viewer %d cached keyframe mismatch: %#v", index, meta)
		}
	}
}

func TestVideoRecoveryRequestsAreRateLimitedGlobally(t *testing.T) {
	server, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	server.direct.mu.Lock()
	server.direct.lastVideoClientAt = time.Now().Add(-15 * time.Second)
	server.direct.mu.Unlock()

	for i := 0; i < 3; i++ {
		if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"spam"}`)); err != nil {
			t.Fatal(err)
		}
	}

	got := countPhoneSignalTypesWithin(phoneSignals, 250*time.Millisecond)
	if got["start"] != 0 {
		t.Fatalf("connected stream recovery should not restart phone stream; signals=%v", got)
	}
	if got["recover_stream"] != 0 {
		t.Fatalf("stream recovery text should be ignored on media socket; signals=%v", got)
	}
}

func TestPhoneRecoveryCommandQueueIsServerRateLimited(t *testing.T) {
	phoneSignals := make(chan string, 64)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(phoneServer.Close)
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneSignals)

	server, ticketServer, relay := newTicketRecoveryTestServer(t, phoneServer.URL)
	t.Cleanup(ticketServer.Close)
	t.Cleanup(relay.Close)

	server.retainRelayViewerForPrewarm("test-visible-page", streamPrewarmHold)
	server.requestPhoneRecovery("stale_frame")
	server.requestPhoneRecovery("stale_frame_repeat")

	waitForPhoneSignalCounts(t, phoneSignals, map[string]int{"recover_stream": 1}, "first recovery command")
	if got := countPhoneSignalsWithin(phoneSignals, "recover_stream", 250*time.Millisecond); got != 0 {
		t.Fatalf("server recovery cooldown allowed duplicate recover_stream commands: %d", got)
	}
}

func TestBackgroundKeyframeQueueCoalescesAcrossReconnectingViewers(t *testing.T) {
	server := &Server{}
	now := time.Now()
	if !server.beginBackgroundKeyframe(now) {
		t.Fatal("first background keyframe should acquire the queue gate")
	}
	if server.beginBackgroundKeyframe(now.Add(time.Second)) {
		t.Fatal("replacement viewer queued a keyframe while the first write was in flight")
	}
	server.finishBackgroundKeyframe()
	if server.beginBackgroundKeyframe(now.Add(2 * time.Second)) {
		t.Fatal("replacement viewer queued a keyframe inside the cooldown")
	}
	if !server.beginBackgroundKeyframe(now.Add(backgroundKeyframeMinInterval)) {
		t.Fatal("a later page generation should be allowed after the cooldown")
	}
	server.finishBackgroundKeyframe()
	if backgroundStreamCommandRequiresDemand("keyframe", "control_code_result_marker_low_latency") {
		t.Fatal("interactive control-code keyframes must bypass the background gate")
	}
	if !perViewerKeyframeRequired("phone_config_active_viewer") || perViewerKeyframeRequired("frame_sequence_gap") || perViewerKeyframeRequired("viewer_sequence_gap") {
		t.Fatal("only configuration recovery should bypass global live suppression")
	}
	if !backgroundKeyframeDedupEligible("phone_config_active_viewer") {
		t.Fatal("configuration recovery must retain global request coalescing")
	}

	server = &Server{}
	if !server.beginBackgroundKeyframe(now) {
		t.Fatal("generic background request should acquire a fresh gate")
	}
	server.finishBackgroundKeyframe()
	if !server.beginBackgroundKeyframe(now.Add(time.Millisecond), "phone_config_active_viewer:42") {
		t.Fatal("new config epoch requirement was suppressed by an unrelated cooldown")
	}
	server.finishBackgroundKeyframe()
	if server.beginBackgroundKeyframe(now.Add(2*time.Millisecond), "phone_config_active_viewer:42") {
		t.Fatal("repeated config epoch bypassed keyframe coalescing")
	}
	if !server.beginBackgroundKeyframe(now.Add(3*time.Millisecond), "phone_config_active_viewer:43") {
		t.Fatal("new config epoch did not supersede the previous epoch cooldown")
	}
	server.finishBackgroundKeyframe()
}

func TestLiveStreamSuppressesBackgroundRecoveryCommands(t *testing.T) {
	server, phoneSignals, _ := newTicketVideoStreamTestServer(t)
	relayDeadline := time.Now().Add(3 * time.Second)
	for {
		health := server.relay.Snapshot()
		if health.Desired && health.Connected {
			break
		}
		if time.Now().After(relayDeadline) {
			t.Fatalf("phone relay did not become live for suppression fixture: desired=%t connected=%t", health.Desired, health.Connected)
		}
		time.Sleep(5 * time.Millisecond)
	}

	now := time.Now()
	setTestAllIntraConfig(server.direct, []byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000,"frameEnvelope":"tsf3"}`))
	recordTestBoundedPhoneClock(t, server.direct, 10_000_000)
	server.direct.mu.Lock()
	server.direct.streamEpoch = 7
	server.direct.lastFrameAt = now
	server.direct.lastFrameEpoch = 7
	server.direct.lastFrameSequence = 42
	server.direct.lastFrameVisualAgeMillis = 100
	server.direct.lastFrameVisualAgeKnown = true
	server.direct.mu.Unlock()
	server.backgroundKeyframeMu.Lock()
	server.backgroundKeyframeInFlight = false
	server.lastBackgroundKeyframeAt = time.Time{}
	server.backgroundKeyframeMu.Unlock()
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)
	if status := server.direct.streamStatus(time.Now(), server.relay.Snapshot()); status["live"] != true || status["streamVerdict"] != "live" {
		t.Fatalf("strict TSF3 suppression fixture is not authoritatively live: %#v", status)
	}

	if err := server.requestPhoneKeyframeNow("stale_video_frames"); err != nil {
		t.Fatalf("background keyframe suppression returned error: %v", err)
	}
	server.requestPhoneRecovery("stale_video_frames")

	if got := collectPhoneSignalsWithin(phoneSignals, 250*time.Millisecond); len(got["keyframe"]) != 0 {
		t.Fatalf("live stream allowed background keyframe commands: %#v", got)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "recover_stream", 250*time.Millisecond); got != 0 {
		t.Fatalf("live stream allowed background recovery commands: %d", got)
	}
	if err := server.requestPhoneKeyframeNow("phone_config_active_viewer"); err != nil {
		t.Fatalf("configuration-required keyframe returned error: %v", err)
	}
	waitForPhoneSignal(t, phoneSignals, "keyframe", "configuration-required keyframe")
	if err := server.requestPhoneKeyframeNow("phone_config_active_viewer"); err != nil {
		t.Fatalf("coalesced configuration keyframe returned error: %v", err)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond); got != 0 {
		t.Fatalf("configuration keyframe bypassed request coalescing: %d", got)
	}
	server.backgroundKeyframeMu.Lock()
	server.backgroundKeyframeInFlight = false
	server.lastBackgroundKeyframeAt = time.Time{}
	server.backgroundKeyframeMu.Unlock()

	if err := server.requestPhoneKeyframeNow("browser_video_provisional_config"); err != nil {
		t.Fatalf("new viewer warm-start keyframe returned error: %v", err)
	}
	waitForPhoneSignal(t, phoneSignals, "keyframe", "new viewer warm-start keyframe")

	if err := server.requestPhoneKeyframeNow("control_code_result_marker_low_latency"); err != nil {
		t.Fatalf("control-code keyframe returned error: %v", err)
	}
	waitForPhoneSignal(t, phoneSignals, "keyframe", "control-code keyframe")

	// A held picture just outside LIVE_FRESH remains useful continuity, but it
	// must not suppress a recovery command as though it still had live authority.
	now = time.Now()
	server.direct.mu.Lock()
	server.direct.lastFrameAt = now
	server.direct.lastFrameReceivedAt = now
	server.direct.lastFrameVisualAgeMillis = durationMillis(liveFreshMaxAge) + 1
	server.direct.lastFrameVisualAgeKnown = true
	server.direct.mu.Unlock()
	status := server.direct.streamStatus(now, server.relay.Snapshot())
	if status["freshnessState"] != freshnessLiveOK || status["live"] != false ||
		status["continuity"] != true || status["streamVerdict"] == "live" {
		t.Fatalf("continuity-only frame retained live recovery authority: %#v", status)
	}
	server.backgroundKeyframeMu.Lock()
	server.backgroundKeyframeInFlight = false
	server.lastBackgroundKeyframeAt = time.Time{}
	server.backgroundKeyframeMu.Unlock()
	if err := server.requestPhoneKeyframeNow("stale_video_frames"); err != nil {
		t.Fatalf("continuity-only recovery keyframe returned error: %v", err)
	}
	waitForPhoneSignal(t, phoneSignals, "keyframe", "continuity-only recovery keyframe")
}

func newTicketVideoStreamTestServer(t *testing.T) (*Server, <-chan string, *websocket.Conn) {
	t.Helper()
	phoneSignals := make(chan string, 64)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(phoneServer.Close)
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneSignals)

	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    phoneServer.URL,
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	t.Cleanup(relay.Close)
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
			BaseURL:    phoneServer.URL,
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL}},
		},
	}, &recordingTicketStore{Store: store, commandSink: phoneSignals}, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	t.Cleanup(ticketServer.Close)

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}, "Origin": []string{"http://ticket.test"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	videoConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser video websocket: %v", err)
	}
	t.Cleanup(func() {
		_ = videoConn.Close(websocket.StatusNormalClosure, "test complete")
	})
	waitForPhoneSignal(t, phoneSignals, "keyframe", "initial phone keyframe")
	waitForStartupCommandIdle(t, server)
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)
	return server, phoneSignals, videoConn
}

func waitForStartupCommandIdle(t *testing.T, server *Server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		server.phoneStartMu.Lock()
		startInFlight := server.phoneHTTPStartInFlight
		server.phoneStartMu.Unlock()
		server.startupSequenceMu.Lock()
		sequenceInFlight := len(server.startupKeyframeSequences) > 0
		server.startupSequenceMu.Unlock()
		server.backgroundKeyframeMu.Lock()
		keyframeInFlight := server.backgroundKeyframeInFlight
		server.backgroundKeyframeMu.Unlock()
		if !startInFlight && !sequenceInFlight && !keyframeInFlight {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"startup commands did not settle: start=%t sequence=%t keyframe=%t",
				startInFlight,
				sequenceInFlight,
				keyframeInFlight,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newTicketRecoveryTestServer(t *testing.T, phoneBaseURL string) (*Server, *httptest.Server, *phone.Relay) {
	t.Helper()
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    phoneBaseURL,
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneBaseURL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	storeForServer := state.Store(store)
	if sink := ticketStreamCommandSink(phoneBaseURL); sink != nil {
		storeForServer = &recordingTicketStore{Store: store, commandSink: sink}
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
			BackendID:  "pixel",
			AttachName: "Pixel",
			BaseURL:    phoneBaseURL,
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneBaseURL}},
		},
	}, storeForServer, relay)
	if err != nil {
		t.Fatal(err)
	}
	return server, httptest.NewServer(server), relay
}

func readPhoneSignals(ctx context.Context, conn *websocket.Conn, phoneSignals chan<- string) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			return
		}
		if bytes.Contains(data, []byte(`"type":"start"`)) {
			select {
			case phoneSignals <- "start":
			default:
			}
		}
		if bytes.Contains(data, []byte(`"type":"keyframe"`)) {
			select {
			case phoneSignals <- "keyframe":
			default:
			}
		}
		if bytes.Contains(data, []byte(`"type":"recover_stream"`)) {
			select {
			case phoneSignals <- "recover_stream":
			default:
			}
		}
	}
}

func phoneSignalType(message string) string {
	message = strings.TrimSpace(message)
	switch {
	case message == "start" || strings.Contains(message, `"type":"start"`):
		return "start"
	case message == "keyframe" || strings.Contains(message, `"type":"keyframe"`):
		return "keyframe"
	case message == "recover_stream" || strings.Contains(message, `"type":"recover_stream"`):
		return "recover_stream"
	case message == "activity" || strings.Contains(message, `"type":"activity"`):
		return "activity"
	default:
		return message
	}
}

func waitForPhoneSignal(t *testing.T, phoneSignals <-chan string, signal string, label string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case got := <-phoneSignals:
			if phoneSignalType(got) == signal {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", label)
		}
	}
}

func drainPhoneSignals(phoneSignals <-chan string, quietFor time.Duration) {
	timer := time.NewTimer(quietFor)
	defer timer.Stop()
	for {
		select {
		case <-phoneSignals:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quietFor)
		case <-timer.C:
			return
		}
	}
}

func waitForPhoneSignalCounts(t *testing.T, phoneSignals <-chan string, want map[string]int, label string) {
	t.Helper()
	got := map[string]int{}
	deadline := time.After(3 * time.Second)
	for {
		complete := true
		for signal, count := range want {
			if got[signal] < count {
				complete = false
				break
			}
		}
		if complete {
			return
		}
		select {
		case signal := <-phoneSignals:
			got[phoneSignalType(signal)]++
		case <-deadline:
			t.Fatalf("timed out waiting for %s; got %v want %v", label, got, want)
		}
	}
}

func drainHTTPStarts(ch <-chan struct{}, quietFor time.Duration) {
	timer := time.NewTimer(quietFor)
	defer timer.Stop()
	for {
		select {
		case <-ch:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(quietFor)
		case <-timer.C:
			return
		}
	}
}

func countPhoneSignalsWithin(phoneSignals <-chan string, signal string, duration time.Duration) int {
	return countPhoneSignalTypesWithin(phoneSignals, duration)[signal]
}

func collectPhoneSignalsWithin(phoneSignals <-chan string, duration time.Duration) map[string][]string {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	collected := map[string][]string{}
	for {
		select {
		case got := <-phoneSignals:
			typeName := phoneSignalType(got)
			collected[typeName] = append(collected[typeName], got)
		case <-timer.C:
			return collected
		}
	}
}

func countPhoneSignalTypesWithin(phoneSignals <-chan string, duration time.Duration) map[string]int {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	counts := map[string]int{}
	for {
		select {
		case got := <-phoneSignals:
			counts[phoneSignalType(got)]++
		case <-timer.C:
			return counts
		}
	}
}

func newStreamSharingTestServer(t *testing.T) (*Server, *httptest.Server, *phone.Relay) {
	t.Helper()
	store := NewMemoryStore()
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
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	return server, httptest.NewServer(server), relay
}

type spacetimeBackendCountingStore struct {
	state.Store
}

func (s *spacetimeBackendCountingStore) Backend() string {
	return "spacetime"
}

func (s *spacetimeBackendCountingStore) IssueMemberToken(context.Context, string) (string, string, error) {
	return "sidecar-member-token", time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339), nil
}

func waitForAtomicCount(t *testing.T, counter *atomic.Int32, want int32, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if got := counter.Load(); got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s count = %d, want at least %d", label, counter.Load(), want)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type blockingSnapshotStore struct {
	state.Store
	snapshotStarted chan struct{}
	releaseSnapshot chan struct{}
}

func (s *blockingSnapshotStore) Snapshot(ctx context.Context, ticketID string, now time.Time) (state.Snapshot, error) {
	select {
	case <-s.snapshotStarted:
	default:
		close(s.snapshotStarted)
	}
	select {
	case <-s.releaseSnapshot:
	case <-ctx.Done():
		return state.Snapshot{}, ctx.Err()
	}
	return s.Store.Snapshot(ctx, ticketID, now)
}

func dialStreamTestClient(t *testing.T, ctx context.Context, serverURL string, sessionID string) *websocket.Conn {
	return dialStreamTestClientForRun(t, ctx, serverURL, sessionID, "")
}

func dialStreamTestClientForRun(t *testing.T, ctx context.Context, serverURL string, sessionID string, runOrigin string) *websocket.Conn {
	t.Helper()
	wsBase := "ws" + strings.TrimPrefix(serverURL, "http")
	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}, "Origin": []string{"http://ticket.test"}}
	header.Add("Cookie", "ticket_remote_session="+sessionID)
	options := &websocket.DialOptions{HTTPHeader: header}
	if runOrigin != "" {
		options.Subprotocols = []string{"ticket.video.v1", runOrigin}
	}
	conn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", options)
	if err != nil {
		t.Fatalf("dial browser video websocket: %v", err)
	}
	if runOrigin != "" && conn.Subprotocol() != "ticket.video.v1" {
		t.Fatalf("negotiated video subprotocol = %q; private startup origin must not be echoed", conn.Subprotocol())
	}
	return conn
}

func readNextBinaryFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) []byte {
	t.Helper()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read websocket frame: %v", err)
		}
		if typ == websocket.MessageBinary {
			return data
		}
	}
}

func readNextTSF2Sequence(t *testing.T, ctx context.Context, conn *websocket.Conn, sequence uint64) []byte {
	t.Helper()
	for {
		frame := readNextBinaryFrame(t, ctx, conn)
		meta := parseTSF2(frame)
		if !meta.ok {
			continue
		}
		if meta.sequence == sequence {
			return frame
		}
		if meta.sequence > sequence {
			t.Fatalf("read TSF2 sequence %d before %d", meta.sequence, sequence)
		}
	}
}

func readNextTextMessageOfType(t *testing.T, ctx context.Context, conn *websocket.Conn, msgType string) map[string]any {
	t.Helper()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read websocket message: %v", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["type"] == msgType {
			return msg
		}
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func testTSF2KeyFrame() []byte {
	return append([]byte{'T', 'S', 'F', '2', 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{0x65, 0x88}...)
}
