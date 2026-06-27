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
		_, _, _ = conn.Read(r.Context())
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
