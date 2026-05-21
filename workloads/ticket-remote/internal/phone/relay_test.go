package phone

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestRelayAcceptsLargePhoneFrames(t *testing.T) {
	largeFrame := bytes.Repeat([]byte{0x5a}, 96*1024)
	receivedHTTPStart := make(chan struct{}, 1)
	receivedStart := make(chan struct{}, 1)
	receivedKeyframe := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			receivedHTTPStart <- struct{}{}
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")

			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, err = conn.Read(ctx)
			if err != nil {
				t.Errorf("read start command: %v", err)
				return
			}
			receivedStart <- struct{}{}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, err = conn.Read(ctx)
			if err != nil {
				t.Errorf("read keyframe command: %v", err)
				return
			}
			receivedKeyframe <- struct{}{}
			if err := conn.Write(ctx, websocket.MessageBinary, largeFrame); err != nil {
				t.Errorf("write large frame: %v", err)
				return
			}
			<-ctx.Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
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
	case <-receivedStart:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not send start command")
	}
	select {
	case <-receivedKeyframe:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not request keyframe on video channel")
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

func TestRelayDelaysPhoneStopAcrossBriefViewerGap(t *testing.T) {
	startCommands := make(chan struct{}, 1)
	stopCommands := make(chan struct{}, 1)
	httpStopRequests := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 700*time.Millisecond)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			startCommands <- struct{}{}
			_, data, err := conn.Read(ctx)
			if err == nil && bytes.Contains(data, []byte(`"type":"stop"`)) {
				stopCommands <- struct{}{}
			}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			<-ctx.Done()
		case "/api/v1/session/stop":
			httpStopRequests <- struct{}{}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
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
	case <-startCommands:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay did not start phone session")
	}
	relay.RemoveViewer()
	time.Sleep(20 * time.Millisecond)
	relay.AddViewer()
	defer relay.Close()

	select {
	case <-stopCommands:
		t.Fatal("phone session stopped during brief viewer gap")
	case <-time.After(120 * time.Millisecond):
	}
	select {
	case <-httpStopRequests:
		t.Fatal("idle stop should use the existing websocket, not HTTP")
	default:
	}

	relay.RemoveViewer()

	select {
	case <-stopCommands:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("phone session was not stopped after idle grace")
	}
	select {
	case <-httpStopRequests:
		t.Fatal("idle stop should use the existing websocket, not HTTP")
	default:
	}
}

func TestRelayAddViewerWaitsForIdleStopCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session", "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			<-r.Context().Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
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

func TestRelayStopsPhoneImmediatelyWhenNoViewerDelayIsZero(t *testing.T) {
	startCommands := make(chan struct{}, 1)
	stopCommands := make(chan struct{}, 1)
	httpStopRequests := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			startCommands <- struct{}{}
			_, data, err := conn.Read(ctx)
			if err == nil && bytes.Contains(data, []byte(`"type":"stop"`)) {
				stopCommands <- struct{}{}
			}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			<-ctx.Done()
		case "/api/v1/session/stop":
			httpStopRequests <- struct{}{}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
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
	case <-startCommands:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("relay did not start phone session")
	}
	relay.RemoveViewer()
	defer relay.Close()

	select {
	case <-stopCommands:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("phone session was not stopped immediately after the last viewer left")
	}
	select {
	case <-httpStopRequests:
		t.Fatal("idle stop should use the existing websocket, not HTTP")
	default:
	}
}

func TestRelayWebsocketStartDoesNotWaitForHTTPStart(t *testing.T) {
	httpStartRequests := make(chan struct{}, 1)
	websocketStartRequests := make(chan struct{}, 1)
	keyframeRequests := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			httpStartRequests <- struct{}{}
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, err = conn.Read(ctx)
			if err != nil {
				t.Errorf("read start command: %v", err)
				return
			}
			websocketStartRequests <- struct{}{}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, err = conn.Read(ctx)
			if err != nil {
				t.Errorf("read keyframe command: %v", err)
				return
			}
			keyframeRequests <- struct{}{}
			<-ctx.Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
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
	case <-websocketStartRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not send websocket start")
	}
	select {
	case <-keyframeRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not request video keyframe")
	}
	select {
	case <-httpStartRequests:
		t.Fatal("relay should not use HTTP session start in the viewer path")
	case <-time.After(120 * time.Millisecond):
	}
}

func TestRelayAddViewerRestartsDesiredButDisconnectedLoop(t *testing.T) {
	websocketStartRequests := make(chan struct{}, 1)
	keyframeRequests := make(chan struct{}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			websocketStartRequests <- struct{}{}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			keyframeRequests <- struct{}{}
			<-ctx.Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
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
	defer relay.Close()
	relay.mu.Lock()
	relay.desired = true
	relay.connected = false
	relay.cancelLoop = nil
	relay.mu.Unlock()

	relay.AddViewer()

	select {
	case <-websocketStartRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not reconnect desired disconnected phone after viewer join")
	}
	select {
	case <-keyframeRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not request keyframe after reconnecting desired disconnected phone")
	}
}

func TestRelayReconnectUsesWebsocketStartOnly(t *testing.T) {
	httpStartRequests := make(chan struct{}, 1)
	websocketStartRequests := make(chan struct{}, 2)
	keyframeRequests := make(chan struct{}, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			httpStartRequests <- struct{}{}
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			websocketStartRequests <- struct{}{}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			keyframeRequests <- struct{}{}
			<-ctx.Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
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
	case <-websocketStartRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("initial control websocket did not start")
	}
	select {
	case <-keyframeRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("initial video websocket did not request keyframe")
	}
	relay.Reconnect("test_recovery_timeout")

	select {
	case <-websocketStartRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("control websocket did not reconnect after recovery")
	}
	select {
	case <-keyframeRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("video websocket did not request keyframe after recovery")
	}
	select {
	case <-httpStartRequests:
		t.Fatal("relay should not use HTTP session start during reconnect")
	case <-time.After(120 * time.Millisecond):
	}
}

func TestRelaySwitchBackendUpdatesSnapshot(t *testing.T) {
	oldBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/session/stop" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
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
