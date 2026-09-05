package phone

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
)

func TestRelaySendsInitialClockProbeBeforeLongTicker(t *testing.T) {
	requestSeen := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var request map[string]any
			if json.Unmarshal(data, &request) != nil || request["type"] != "clock_probe" {
				continue
			}
			requestSeen <- request
			response, _ := json.Marshal(map[string]any{
				"type":                     "clock_probe_result",
				"probeId":                  request["probeId"],
				"serverSendUnixMicros":     request["serverSendUnixMicros"],
				"phoneReceiveUptimeMicros": 10_000_000,
				"phoneSendUptimeMicros":    10_001_000,
			})
			_ = conn.Write(r.Context(), websocket.MessageText, response)
		}
	}))
	defer server.Close()

	results := make(chan *ClockProbeResult, 1)
	relay := NewRelay(RelayConfig{
		// A one-hour periodic interval makes this test fail unless connection
		// startup sends the first probe immediately.
		BaseURL: server.URL, ClockProbeInterval: time.Hour,
		LivenessIdle: time.Second, ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
	})
	relay.SetHandlers(func(message Message) {
		if message.ClockProbe != nil {
			results <- message.ClockProbe
		}
	}, nil)
	relay.AddViewer()
	defer relay.Close()

	select {
	case request := <-requestSeen:
		probeID, _ := request["probeId"].(string)
		if !validClockProbeID(probeID) || request["serverSendUnixMicros"].(float64) <= 0 {
			t.Fatalf("invalid relay clock probe request: %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not send a clock probe")
	}
	select {
	case result := <-results:
		if !validClockProbeID(result.ProbeID) || result.ServerSendUnixMicros <= 0 ||
			result.ServerReceiveUnixMicros < result.ServerSendUnixMicros ||
			result.PhoneReceiveUptimeMicros != 10_000_000 || result.PhoneSendUptimeMicros != 10_001_000 {
			t.Fatalf("invalid validated clock probe result: %#v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not publish the validated clock probe result")
	}
}

func TestClockProbeIDValidationIsStrictAndBounded(t *testing.T) {
	if !validClockProbeID("probe-1.a:b_c") {
		t.Fatal("safe bounded probe id was rejected")
	}
	for _, value := range []string{"", "bad id", "bad/segment", strings.Repeat("a", ClockProbeIDMaxBytes+1)} {
		if validClockProbeID(value) {
			t.Fatalf("unsafe probe id %q was accepted", value)
		}
	}
}

func TestRelayCaptureDemandIsStrictSerializedAndConnectionScoped(t *testing.T) {
	connections := make(chan struct{}, 3)
	demands := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		connections <- struct{}{}
		for {
			typ, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var payload map[string]any
			if json.Unmarshal(data, &payload) == nil && payload["type"] == "capture_demand" {
				demands <- payload
			}
		}
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL: server.URL, ClockProbeInterval: time.Hour, LivenessIdle: time.Hour,
		ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour,
	})
	if _, err := relay.SendCaptureDemand(context.Background(), 7); err == nil {
		t.Fatal("capture demand was written without a current phone connection")
	}
	relay.AddViewer()
	defer relay.Close()
	select {
	case <-connections:
	case <-time.After(2 * time.Second):
		t.Fatal("initial phone connection was not established")
	}
	waitRelayConnected(t, relay)

	first, err := relay.SendCaptureDemand(context.Background(), 7)
	if err != nil {
		t.Fatalf("send first capture demand: %v", err)
	}
	second, err := relay.SendCaptureDemand(context.Background(), 7)
	if err != nil {
		t.Fatalf("send second capture demand: %v", err)
	}
	if first.Generation != 1 || second.Generation != 2 || first.ConnectionGeneration == 0 || second.ConnectionGeneration != first.ConnectionGeneration {
		t.Fatalf("capture demand generations are not connection scoped: first=%#v second=%#v", first, second)
	}
	for generation := 1; generation <= 2; generation++ {
		select {
		case demand := <-demands:
			if len(demand) != 5 || demand["type"] != "capture_demand" || demand["version"] != float64(1) ||
				demand["streamEpoch"] != float64(7) || demand["generation"] != float64(generation) ||
				demand["ttlMillis"] != float64(CaptureDemandTTL.Milliseconds()) {
				t.Fatalf("invalid strict capture demand %d: %#v", generation, demand)
			}
		case <-time.After(time.Second):
			t.Fatalf("capture demand %d was not received", generation)
		}
	}

	relay.Reconnect("test capture demand generation reset")
	select {
	case <-connections:
	case <-time.After(2 * time.Second):
		t.Fatal("replacement phone connection was not established")
	}
	waitRelayConnected(t, relay)
	replacement, err := relay.SendCaptureDemand(context.Background(), 8)
	if err != nil {
		t.Fatalf("send replacement-connection capture demand: %v", err)
	}
	if replacement.Generation != 1 || replacement.ConnectionGeneration == first.ConnectionGeneration {
		t.Fatalf("replacement connection did not fence/reset generation: first=%#v replacement=%#v", first, replacement)
	}
}

func waitRelayConnected(t *testing.T, relay *Relay) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !relay.Snapshot().Connected {
		if time.Now().After(deadline) {
			t.Fatal("relay did not publish connected state")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRelayCarriesOnlyBoundedStartupTraceOnPrivateVideoHandshake(t *testing.T) {
	type requestEvidence struct {
		header   string
		rawQuery string
	}
	connected := make(chan requestEvidence, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		connected <- requestEvidence{
			header:   r.Header.Get("X-Ticket-Startup-Trace"),
			rawQuery: r.URL.RawQuery,
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept video websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	relay.AddViewer("startup_deadbeef")
	defer relay.Close()

	select {
	case evidence := <-connected:
		if evidence.header != "startup_deadbeef" {
			t.Fatalf("startup trace header = %q, want startup_deadbeef", evidence.header)
		}
		if evidence.rawQuery != "" {
			t.Fatalf("private video handshake exposed query data: %q", evidence.rawQuery)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not connect to the private video websocket")
	}
}

func TestCleanStartupTraceCorrelationID(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "valid", value: "startup_deadbeef", want: "startup_deadbeef"},
		{name: "trimmed", value: "  startup_0123abcd  ", want: "startup_0123abcd"},
		{name: "uppercase", value: "startup_DEADBEEF"},
		{name: "raw session", value: "session-user@example.com"},
		{name: "short", value: "startup_deadbee"},
		{name: "long", value: "startup_deadbeef0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := cleanStartupTraceCorrelationID(test.value); got != test.want {
				t.Fatalf("cleanStartupTraceCorrelationID(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestRelayStartupTraceCorrelationClearsOnlyItsExpectedOwner(t *testing.T) {
	relay := NewRelay(RelayConfig{})
	relay.SetStartupTraceCorrelationID("startup_aaaaaaaa")
	if relay.ClearStartupTraceCorrelationIDIf("startup_bbbbbbbb") {
		t.Fatal("stale correlation cleared the current owner")
	}
	if got := relay.StartupTraceCorrelationID(); got != "startup_aaaaaaaa" {
		t.Fatalf("current owner after stale clear = %q", got)
	}
	relay.SetStartupTraceCorrelationID("startup_bbbbbbbb")
	if relay.ClearStartupTraceCorrelationIDIf("startup_aaaaaaaa") {
		t.Fatal("late clear erased the replacement owner")
	}
	relay.SetStartupTraceCorrelationID("")
	if got := relay.StartupTraceCorrelationID(); got != "startup_bbbbbbbb" {
		t.Fatalf("empty setter erased the replacement owner: %q", got)
	}
	if !relay.ClearStartupTraceCorrelationIDIf("startup_bbbbbbbb") {
		t.Fatal("matching owner did not clear")
	}
	if got := relay.StartupTraceCorrelationID(); got != "" {
		t.Fatalf("correlation after matching clear = %q", got)
	}
}

func TestRelayCorrelationRefreshDoesNotRestartInFlightMediaHandshake(t *testing.T) {
	headers := make(chan string, 4)
	releaseFirst := make(chan struct{})
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		attempt := attempts.Add(1)
		headers <- r.Header.Get("X-Ticket-Startup-Trace")
		select {
		case <-releaseFirst:
		case <-r.Context().Done():
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(attempt)}); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		RequestTimeout:    2 * time.Second,
		ReconnectMinDelay: time.Second,
		ReconnectMaxDelay: time.Second,
	})
	messages := make(chan Message, 1)
	relay.SetHandlers(func(msg Message) {
		if len(msg.Binary) > 0 {
			messages <- msg
		}
	}, nil)
	relay.AddViewer("startup_aaaaaaaa")
	defer relay.Close()

	select {
	case header := <-headers:
		if header != "startup_aaaaaaaa" {
			t.Fatalf("first in-flight startup trace = %q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first private video handshake did not start")
	}
	relay.SetStartupTraceCorrelationID("startup_bbbbbbbb")
	close(releaseFirst)

	select {
	case message := <-messages:
		if message.StartupTraceCorrelationID != "startup_bbbbbbbb" {
			t.Fatalf("installed connection startup owner = %q, want latest correlation", message.StartupTraceCorrelationID)
		}
		if message.ConnectionStartupTraceCorrelationID != "startup_aaaaaaaa" {
			t.Fatalf("installed connection handshake correlation = %q, want original in-flight header", message.ConnectionStartupTraceCorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight private video handshake did not complete promptly")
	}
	select {
	case header := <-headers:
		t.Fatalf("correlation-only refresh restarted the media handshake with header %q", header)
	case <-time.After(100 * time.Millisecond):
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("private video handshake attempts = %d, want exactly one", got)
	}
}

func TestRelayEnsureActiveDoesNotRestartInFlightMediaHandshake(t *testing.T) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		attempts.Add(1)
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		RequestTimeout:    2 * time.Second,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	relay.AddViewer()
	defer relay.Close()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first private media handshake did not start")
	}
	if !relay.EnsureActive("parallel browser wake") {
		t.Fatal("active desired connection loop was not recognized")
	}
	select {
	case <-started:
		t.Fatal("ensure-active restarted the in-flight private media handshake")
	case <-time.After(100 * time.Millisecond):
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("private media handshake attempts = %d, want exactly one", got)
	}
	close(release)
}

func TestRelaySupersededDialCannotReplaceNewerReconnect(t *testing.T) {
	type dialRecord struct {
		attempt int32
		conn    *websocket.Conn
	}
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		startupTraceCorrelationID := r.Header.Get("X-Ticket-Startup-Trace")
		requests <- startupTraceCorrelationID
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		marker := byte(1)
		if startupTraceCorrelationID == "startup_bbbbbbbb" {
			marker = 2
		}
		if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{marker}); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		RequestTimeout:    2 * time.Second,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	defer relay.Close()
	oldDialStarted := make(chan struct{})
	releaseOldDial := make(chan struct{})
	oldDialReleased := false
	defer func() {
		if !oldDialReleased {
			close(releaseOldDial)
		}
	}()
	dialStarted := make(chan int32, 4)
	dialed := make(chan dialRecord, 4)
	var dialAttempts atomic.Int32
	dialWebsocket := relay.dialWebsocket
	relay.dialWebsocket = func(ctx context.Context, targetURL string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error) {
		attempt := dialAttempts.Add(1)
		dialStarted <- attempt
		if attempt == 1 {
			close(oldDialStarted)
			<-releaseOldDial
			// Model a transport that completed after its caller canceled. The
			// relay must still reject this older successful connection.
			ctx = context.Background()
		}
		conn, response, err := dialWebsocket(ctx, targetURL, options)
		if err == nil {
			dialed <- dialRecord{attempt: attempt, conn: conn}
		}
		return conn, response, err
	}
	messages := make(chan Message, 4)
	relay.SetHandlers(func(message Message) {
		if len(message.Binary) > 0 {
			messages <- message
		}
	}, nil)
	relay.SetStartupTraceCorrelationID("startup_aaaaaaaa")
	relay.mu.Lock()
	relay.viewers = 1
	relay.desired = true
	relay.mu.Unlock()
	oldDone := make(chan error, 1)
	go func() {
		oldDone <- relay.connectOnce(context.Background())
	}()

	select {
	case <-oldDialStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("old private media dial did not start")
	}
	if attempt := <-dialStarted; attempt != 1 {
		t.Fatalf("first dial attempt = %d, want 1", attempt)
	}
	relay.SetStartupTraceCorrelationID("startup_bbbbbbbb")
	select {
	case attempt := <-dialStarted:
		t.Fatalf("passive startup trace refresh unexpectedly started dial %d", attempt)
	case <-time.After(100 * time.Millisecond):
	}

	relay.Reconnect("test newer reconnect")
	select {
	case attempt := <-dialStarted:
		if attempt != 2 {
			t.Fatalf("reconnect dial attempt = %d, want 2", attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newer reconnect dial did not start")
	}
	select {
	case startupTraceCorrelationID := <-requests:
		if startupTraceCorrelationID != "startup_bbbbbbbb" {
			t.Fatalf("newer reconnect header = %q, want startup_bbbbbbbb", startupTraceCorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newer reconnect did not reach the private media server")
	}
	var newConn *websocket.Conn
	select {
	case record := <-dialed:
		if record.attempt != 2 {
			t.Fatalf("first completed dial = %d, want newer attempt 2", record.attempt)
		}
		newConn = record.conn
	case <-time.After(2 * time.Second):
		t.Fatal("newer reconnect dial did not complete")
	}
	select {
	case message := <-messages:
		if len(message.Binary) != 1 || message.Binary[0] != 2 {
			t.Fatalf("newer reconnect message = %v, want marker 2", message.Binary)
		}
		if message.StartupTraceCorrelationID != "startup_bbbbbbbb" || message.ConnectionStartupTraceCorrelationID != "startup_bbbbbbbb" {
			t.Fatalf("newer reconnect correlations = current %q connection %q", message.StartupTraceCorrelationID, message.ConnectionStartupTraceCorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("newer reconnect did not install")
	}

	close(releaseOldDial)
	oldDialReleased = true
	select {
	case startupTraceCorrelationID := <-requests:
		if startupTraceCorrelationID != "startup_aaaaaaaa" {
			t.Fatalf("released old dial header = %q, want startup_aaaaaaaa", startupTraceCorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("released old dial did not complete its handshake")
	}
	select {
	case record := <-dialed:
		if record.attempt != 1 {
			t.Fatalf("late completed dial = %d, want old attempt 1", record.attempt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("released old dial did not return")
	}
	select {
	case err := <-oldDone:
		if err != nil {
			t.Fatalf("superseded old dial returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("superseded old dial did not finish")
	}
	relay.mu.Lock()
	installedConn := relay.videoConn
	relay.mu.Unlock()
	if installedConn != newConn {
		t.Fatal("superseded old dial replaced the newer installed connection")
	}
	select {
	case message := <-messages:
		t.Fatalf("superseded old dial forwarded a message: %#v", message)
	case <-time.After(100 * time.Millisecond):
	}
	if got := dialAttempts.Load(); got != 2 {
		t.Fatalf("private media dial attempts = %d, want exactly 2", got)
	}
}

func TestRelayMessagesKeepTheCorrelationOfTheirOwnPrivateConnection(t *testing.T) {
	headers := make(chan string, 2)
	sendCurrentFrame := make(chan struct{}, 1)
	sendLateOldFrames := make(chan struct{}, 1)
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		attempt := attempts.Add(1)
		headers <- r.Header.Get("X-Ticket-Startup-Trace")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(attempt)}); err != nil {
			return
		}
		if attempt == 1 {
			select {
			case <-sendCurrentFrame:
				if err := conn.Write(r.Context(), websocket.MessageBinary, []byte{byte(attempt), 2}); err != nil {
					return
				}
			case <-r.Context().Done():
				return
			}
			<-sendLateOldFrames
			lateCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_ = conn.Write(lateCtx, websocket.MessageText, []byte(`{"type":"config","streamEpoch":1}`))
			_ = conn.Write(lateCtx, websocket.MessageBinary, []byte{0xaa})
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	messages := make(chan Message, 3)
	var disconnects atomic.Int32
	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		RequestTimeout:    2 * time.Second,
		ReconnectMinDelay: 10 * time.Millisecond,
		ReconnectMaxDelay: 10 * time.Millisecond,
	})
	relay.SetHandlers(func(msg Message) {
		if len(msg.Binary) > 0 {
			messages <- msg
		}
	}, func(error) {
		disconnects.Add(1)
	})
	relay.AddViewer("startup_aaaaaaaa")
	defer relay.Close()

	if header := <-headers; header != "startup_aaaaaaaa" {
		t.Fatalf("first private connection correlation = %q", header)
	}
	first := <-messages
	if first.StartupTraceCorrelationID != "startup_aaaaaaaa" {
		t.Fatalf("first message correlation = %q", first.StartupTraceCorrelationID)
	}
	if first.ConnectionStartupTraceCorrelationID != "startup_aaaaaaaa" {
		t.Fatalf("first message connection correlation = %q", first.ConnectionStartupTraceCorrelationID)
	}
	relay.SetStartupTraceCorrelationID("startup_bbbbbbbb")
	if first.StartupTraceCorrelationID != "startup_aaaaaaaa" {
		t.Fatalf("mutable relay state changed the first connection origin: %q", first.StartupTraceCorrelationID)
	}
	sendCurrentFrame <- struct{}{}
	select {
	case current := <-messages:
		if current.StartupTraceCorrelationID != "startup_bbbbbbbb" {
			t.Fatalf("installed connection message correlation = %q, want current startup owner", current.StartupTraceCorrelationID)
		}
		if current.ConnectionStartupTraceCorrelationID != "startup_aaaaaaaa" {
			t.Fatalf("installed connection handshake evidence = %q, want original connection correlation", current.ConnectionStartupTraceCorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("installed private connection did not forward a frame after startup ownership changed")
	}
	relay.Reconnect("test latest startup origin")

	select {
	case header := <-headers:
		if header != "startup_bbbbbbbb" {
			t.Fatalf("replacement private connection correlation = %q", header)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement private connection did not connect")
	}
	select {
	case second := <-messages:
		if second.StartupTraceCorrelationID != "startup_bbbbbbbb" {
			t.Fatalf("replacement message correlation = %q", second.StartupTraceCorrelationID)
		}
		if second.ConnectionStartupTraceCorrelationID != "startup_bbbbbbbb" {
			t.Fatalf("replacement message connection correlation = %q", second.ConnectionStartupTraceCorrelationID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("replacement private connection did not forward a message")
	}
	sendLateOldFrames <- struct{}{}
	select {
	case late := <-messages:
		t.Fatalf("replaced private connection forwarded a late frame: %#v", late)
	case <-time.After(150 * time.Millisecond):
	}
	if got := disconnects.Load(); got != 0 {
		t.Fatalf("replaced private connection triggered %d disconnect recoveries", got)
	}
}

func TestRelayAcceptsLargePhoneFramesFromVideoPath(t *testing.T) {
	largeFrame := bytes.Repeat([]byte{0x5a}, 96*1024)
	videoConnected := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			videoConnected <- struct{}{}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := conn.Write(ctx, websocket.MessageBinary, largeFrame); err != nil {
				t.Errorf("write large frame: %v", err)
				return
			}
			<-ctx.Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	gotFrame := make(chan Message, 1)
	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	relay.SetHandlers(func(msg Message) {
		if len(msg.Binary) > 0 {
			gotFrame <- msg
		}
	}, nil)

	relay.AddViewer()
	defer relay.Close()

	select {
	case <-videoConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not connect to the video websocket")
	}
	select {
	case msg := <-gotFrame:
		if !bytes.Equal(msg.Binary, largeFrame) {
			t.Fatalf("large frame mismatch: got %d bytes", len(msg.Binary))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not forward large frame")
	}
}

func TestRelayUsesCanonicalVideoMessageCap(t *testing.T) {
	exact := bytes.Repeat([]byte{0x5a}, int(MaxVideoMessageBytes))
	oversize := bytes.Repeat([]byte{0x6b}, int(MaxVideoMessageBytes)+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		if err := conn.Write(r.Context(), websocket.MessageBinary, exact); err != nil {
			return
		}
		_ = conn.Write(r.Context(), websocket.MessageBinary, oversize)
		<-r.Context().Done()
	}))
	defer server.Close()

	messages := make(chan int, 2)
	disconnected := make(chan error, 1)
	relay := NewRelay(RelayConfig{BaseURL: server.URL, ReconnectMinDelay: time.Hour, ReconnectMaxDelay: time.Hour})
	relay.SetHandlers(func(message Message) {
		if len(message.Binary) > 0 {
			messages <- len(message.Binary)
		}
	}, func(err error) { disconnected <- err })
	relay.AddViewer()
	defer relay.Close()

	select {
	case size := <-messages:
		if size != int(MaxVideoMessageBytes) {
			t.Fatalf("first admitted message size = %d, want %d", size, MaxVideoMessageBytes)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("message at the canonical cap was not admitted")
	}
	select {
	case <-disconnected:
	case <-time.After(2 * time.Second):
		t.Fatal("message above the canonical cap did not terminate the source connection")
	}
	select {
	case size := <-messages:
		t.Fatalf("oversize message reached the handler with %d bytes", size)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestReconnectJitterStaysWithinEqualJitterBounds(t *testing.T) {
	for _, delay := range []time.Duration{time.Millisecond, 500 * time.Millisecond, 5 * time.Second} {
		for range 100 {
			got := jitterReconnectDelay(delay)
			if got < delay/2 || got > delay {
				t.Fatalf("jitterReconnectDelay(%s) = %s, want [%s,%s]", delay, got, delay/2, delay)
			}
		}
	}
}

func TestRelayLivenessPingKeepsResponsiveQuietConnection(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL: server.URL, ReconnectMinDelay: 10 * time.Millisecond, ReconnectMaxDelay: 20 * time.Millisecond,
		LivenessIdle: 20 * time.Millisecond, LivenessTimeout: 100 * time.Millisecond,
	})
	relay.reconnectJitter = func(delay time.Duration) time.Duration { return delay }
	relay.AddViewer()
	defer relay.Close()

	deadline := time.Now().Add(time.Second)
	for !relay.Snapshot().Connected && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !relay.Snapshot().Connected {
		t.Fatal("relay did not establish the quiet connection")
	}
	time.Sleep(150 * time.Millisecond)
	if got := attempts.Load(); got != 1 || !relay.Snapshot().Connected {
		t.Fatalf("responsive ping/pong connection was recycled: attempts=%d health=%#v", got, relay.Snapshot())
	}
}

func TestRelayLivenessTimeoutReconnectsStalledQuietConnection(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		// Deliberately do not read: the peer cannot process and answer Ping.
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL: server.URL, ReconnectMinDelay: 5 * time.Millisecond, ReconnectMaxDelay: 10 * time.Millisecond,
		LivenessIdle: 15 * time.Millisecond, LivenessTimeout: 25 * time.Millisecond,
	})
	relay.reconnectJitter = func(delay time.Duration) time.Duration { return delay }
	relay.AddViewer()
	defer relay.Close()

	deadline := time.Now().Add(2 * time.Second)
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := attempts.Load(); got < 2 {
		t.Fatalf("stalled quiet connection was not replaced; attempts=%d health=%#v", got, relay.Snapshot())
	}
}

func TestRelayDoesNotUseControlOrSessionEndpoints(t *testing.T) {
	videoConnected := make(chan struct{}, 1)
	unexpected := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			videoConnected <- struct{}{}
			<-r.Context().Done()
		case "/api/v1/session", "/api/v1/session/start", "/api/v1/session/stop":
			unexpected <- r.URL.Path
			w.WriteHeader(http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	relay.AddViewer()
	defer relay.Close()

	select {
	case <-videoConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not connect to the video websocket")
	}
	select {
	case path := <-unexpected:
		t.Fatalf("relay used removed control/session endpoint %s", path)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRelayDelaysVideoCloseAcrossBriefViewerGap(t *testing.T) {
	videoConnected := make(chan struct{}, 1)
	videoClosed := make(chan struct{}, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept video websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		videoConnected <- struct{}{}
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				break
			}
		}
		videoClosed <- struct{}{}
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: 80 * time.Millisecond,
	})
	relay.AddViewer()
	select {
	case <-videoConnected:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay did not connect to video")
	}
	relay.RemoveViewer()
	time.Sleep(20 * time.Millisecond)
	relay.AddViewer()
	defer relay.Close()

	select {
	case <-videoClosed:
		t.Fatal("video socket closed during brief viewer gap")
	case <-time.After(120 * time.Millisecond):
	}

	relay.RemoveViewer()
	select {
	case <-videoClosed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("video socket was not closed after idle grace")
	}
}

func TestRelayAddViewerWaitsForIdleStopCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept video websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	done := make(chan struct{})
	relay.mu.Lock()
	relay.idleStopping = true
	relay.idleStopDone = done
	relay.mu.Unlock()

	addDone := make(chan struct{})
	go func() {
		relay.AddViewer()
		close(addDone)
	}()
	select {
	case <-addDone:
		t.Fatal("viewer add returned while idle stop was still in flight")
	case <-time.After(80 * time.Millisecond):
	}
	relay.finishIdleStop(done)
	select {
	case <-addDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("viewer add did not resume after idle stop completed")
	}
	if snapshot := relay.Snapshot(); snapshot.Viewers != 1 || !snapshot.Desired {
		t.Fatalf("viewer was not added after idle stop completed: %#v", snapshot)
	}
}

func TestRelayStopsVideoImmediatelyWhenNoViewerDelayIsZero(t *testing.T) {
	videoConnected := make(chan struct{}, 1)
	videoClosed := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept video websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		videoConnected <- struct{}{}
		_, _, _ = conn.Read(r.Context())
		videoClosed <- struct{}{}
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: 0,
	})
	relay.AddViewer()
	select {
	case <-videoConnected:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay did not connect to video")
	}
	relay.RemoveViewer()
	defer relay.Close()

	deadline := time.After(time.Second)
	for {
		if !relay.Snapshot().Connected {
			break
		}
		select {
		case <-deadline:
			t.Fatal("relay stayed connected after the last viewer left")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRelayAddViewerRestartsDesiredButDisconnectedLoop(t *testing.T) {
	videoConnected := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/stream" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept video websocket: %v", err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
		videoConnected <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	defer relay.Close()
	relay.mu.Lock()
	relay.desired = true
	relay.connected = false
	relay.cancelLoop = nil
	relay.mu.Unlock()

	relay.AddViewer()
	select {
	case <-videoConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not reconnect desired disconnected video path after viewer join")
	}
}

func TestRelayReconnectsVideoSocketOnly(t *testing.T) {
	videoConnected := make(chan struct{}, 2)
	unexpected := make(chan string, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			videoConnected <- struct{}{}
			<-r.Context().Done()
		case "/api/v1/session", "/api/v1/session/start", "/api/v1/session/stop":
			unexpected <- r.URL.Path
			w.WriteHeader(http.StatusTeapot)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	relay := NewRelay(RelayConfig{
		BaseURL:           server.URL,
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
	})
	relay.AddViewer()
	defer relay.Close()

	select {
	case <-videoConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("initial video websocket did not connect")
	}
	relay.Reconnect("test_recovery_timeout")
	select {
	case <-videoConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("video websocket did not reconnect after recovery")
	}
	select {
	case path := <-unexpected:
		t.Fatalf("relay used removed control/session endpoint %s", path)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRelaySwitchBackendUpdatesSnapshotWithoutStopRequest(t *testing.T) {
	oldBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("switching an idle backend should not call old backend path %s", r.URL.Path)
	}))
	defer oldBackend.Close()

	relay := NewRelay(RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    oldBackend.URL + "/",
	})
	relay.SwitchBackend(Backend{
		ID:         "lab-pixel",
		AttachName: "Lab Pixel",
		BaseURL:    "http://lab.test/",
	})

	snapshot := relay.Snapshot()
	if snapshot.BackendID != "lab-pixel" {
		t.Fatalf("backend id = %q", snapshot.BackendID)
	}
	if snapshot.AttachName != "Lab Pixel" {
		t.Fatalf("attach name = %q", snapshot.AttachName)
	}
	if snapshot.BaseURL != "http://lab.test" {
		t.Fatalf("base URL = %q", snapshot.BaseURL)
	}
	if snapshot.Connected || snapshot.StreamState != "idle" {
		t.Fatalf("unexpected switched relay health: %#v", snapshot)
	}
}
