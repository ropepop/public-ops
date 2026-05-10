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
	stopRequests := make(chan struct{}, 1)

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
			ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			<-ctx.Done()
		case "/api/v1/session/stop":
			stopRequests <- struct{}{}
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
	relay.RemoveViewer()
	time.Sleep(20 * time.Millisecond)
	relay.AddViewer()
	defer relay.Close()

	select {
	case <-stopRequests:
		t.Fatal("phone session stopped during brief viewer gap")
	case <-time.After(120 * time.Millisecond):
	}

	relay.RemoveViewer()

	select {
	case <-stopRequests:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("phone session was not stopped after idle grace")
	}
}

func TestRelayStopsPhoneImmediatelyWhenNoViewerDelayIsZero(t *testing.T) {
	stopRequests := make(chan struct{}, 1)

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
			stopRequests <- struct{}{}
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
	relay.RemoveViewer()
	defer relay.Close()

	select {
	case <-stopRequests:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("phone session was not stopped immediately after the last viewer left")
	}
}

func TestRelayWebsocketStartDoesNotWaitForHTTPStartFallback(t *testing.T) {
	httpStartRequests := make(chan struct{}, 1)
	releaseHTTPStart := make(chan struct{})
	websocketStartRequests := make(chan struct{}, 1)
	keyframeRequests := make(chan struct{}, 1)
	defer close(releaseHTTPStart)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			httpStartRequests <- struct{}{}
			select {
			case <-releaseHTTPStart:
			case <-r.Context().Done():
			}
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
		t.Fatal("relay did not send websocket start without waiting for HTTP start")
	}
	select {
	case <-keyframeRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not request video keyframe without waiting for HTTP start")
	}
	select {
	case <-httpStartRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not issue HTTP start fallback")
	}
}

func TestRelayRecoveryStartUsesCallerTimeoutAndReconnects(t *testing.T) {
	startRequests := make(chan struct{}, 2)
	releaseStart := make(chan struct{})
	controlClosed := make(chan struct{}, 1)
	videoClosed := make(chan struct{}, 1)
	defer close(releaseStart)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			startRequests <- struct{}{}
			select {
			case <-releaseStart:
				w.WriteHeader(http.StatusOK)
			case <-r.Context().Done():
			}
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
			<-ctx.Done()
			controlClosed <- struct{}{}
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
			<-ctx.Done()
			videoClosed <- struct{}{}
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
	case <-startRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("initial HTTP start fallback was not issued")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := relay.StartPhoneSession(ctx); err == nil {
		t.Fatal("expected bounded recovery start to time out")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("recovery start ignored caller timeout: %v", elapsed)
	}
	relay.Reconnect("test_recovery_timeout")

	select {
	case <-controlClosed:
	case <-time.After(time.Second):
		t.Fatal("control websocket was not closed during recovery reconnect")
	}
	select {
	case <-videoClosed:
	case <-time.After(time.Second):
		t.Fatal("video websocket was not closed during recovery reconnect")
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
		ID:         "android-sim",
		AttachName: "Android simulator",
		BaseURL:    "http://sim.test/",
	})

	snapshot := relay.Snapshot()
	if snapshot.BackendID != "android-sim" {
		t.Fatalf("backend id = %q", snapshot.BackendID)
	}
	if snapshot.AttachName != "Android simulator" {
		t.Fatalf("attach name = %q", snapshot.AttachName)
	}
	if snapshot.BaseURL != "http://sim.test" {
		t.Fatalf("base URL = %q", snapshot.BaseURL)
	}
	if snapshot.Connected || snapshot.StreamState != "idle" {
		t.Fatalf("unexpected switched relay health: %#v", snapshot)
	}
}
