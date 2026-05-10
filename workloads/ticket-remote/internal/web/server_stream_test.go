package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestBrowserHeartbeatKeepsPhoneBackendActive(t *testing.T) {
	phoneStart := make(chan struct{}, 1)
	phoneActivity := make(chan string, 1)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			for {
				ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				_, data, err := conn.Read(ctx)
				cancel()
				if err != nil {
					return
				}
				if bytes.Contains(data, []byte(`"type":"start"`)) {
					select {
					case phoneStart <- struct{}{}:
					default:
					}
				}
				if bytes.Contains(data, []byte(`"type":"activity"`)) {
					phoneActivity <- string(data)
					return
				}
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			<-ctx.Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := state.NewMemoryStore()
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
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	videoConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser video websocket: %v", err)
	}
	defer videoConn.Close(websocket.StatusNormalClosure, "test complete")

	select {
	case <-phoneStart:
	case <-time.After(3 * time.Second):
		t.Fatal("phone backend did not receive relay start")
	}

	controlConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser control websocket: %v", err)
	}
	defer controlConn.Close(websocket.StatusNormalClosure, "test complete")
	if err := controlConn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat"}`)); err != nil {
		t.Fatalf("send browser heartbeat: %v", err)
	}

	select {
	case got := <-phoneActivity:
		if !strings.Contains(got, "public_heartbeat") {
			t.Fatalf("phone activity message = %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("browser heartbeat was not forwarded to phone activity")
	}
}

func TestBrowserControlSocketStartsPhoneBackendBeforeVideoJoin(t *testing.T) {
	phoneStart := make(chan struct{}, 1)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if bytes.Contains(data, []byte(`"type":"start"`)) {
				phoneStart <- struct{}{}
			}
			<-ctx.Done()
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			_, _, _ = conn.Read(ctx)
			<-ctx.Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := state.NewMemoryStore()
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
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controlConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser control websocket: %v", err)
	}
	defer controlConn.Close(websocket.StatusNormalClosure, "test complete")

	select {
	case <-phoneStart:
	case <-time.After(3 * time.Second):
		t.Fatal("phone backend did not receive relay start from browser control socket")
	}
}

func TestVideoWarmStartKeyFrameIsSharedDuringControlSession(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	keyFrame := testTSF2KeyFrame()
	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1}`))
	server.direct.recordFrame(keyFrame)
	setTestControlGate(server, "controller-session", "ticket@jolkins.id.lv")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	got := readNextBinaryFrame(t, ctx, viewerConn)
	if !bytes.Equal(got, keyFrame) {
		t.Fatalf("non-controller warm keyframe mismatch: got %x want %x", got, keyFrame)
	}
}

func TestLiveFramesAreSharedDuringControlSession(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controllerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "controller-session")
	defer controllerConn.Close(websocket.StatusNormalClosure, "test complete")
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	setTestControlGate(server, "controller-session", "ticket@jolkins.id.lv")
	frame := []byte("shared live frame")
	server.broadcastFrame(frame)

	if got := readNextBinaryFrame(t, ctx, controllerConn); !bytes.Equal(got, frame) {
		t.Fatalf("controller frame = %q", string(got))
	}
	if got := readNextBinaryFrame(t, ctx, viewerConn); !bytes.Equal(got, frame) {
		t.Fatalf("viewer frame = %q", string(got))
	}
}

func TestVideoStreamSendsStreamStatus(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1}`))
	server.direct.recordFrame(testTSF2KeyFrame())

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	msg := readNextTextMessageOfType(t, ctx, viewerConn, "stream_status")
	if msg["framesForwarded"].(float64) < 1 {
		t.Fatalf("stream_status framesForwarded = %#v", msg["framesForwarded"])
	}
	if msg["lastFrameAgoMillis"].(float64) < 0 {
		t.Fatalf("stream_status lastFrameAgoMillis = %#v", msg["lastFrameAgoMillis"])
	}
	if _, ok := msg["phoneStreamState"].(string); !ok {
		t.Fatalf("stream_status missing phoneStreamState: %#v", msg)
	}
}

func TestVideoRecoverStreamRequestsPhoneStartAndKeyframe(t *testing.T) {
	phoneStart := make(chan struct{}, 4)
	phoneVideoKeyframe := make(chan struct{}, 4)
	phoneControlKeyframe := make(chan struct{}, 4)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			for {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				_, data, err := conn.Read(ctx)
				cancel()
				if err != nil {
					return
				}
				if bytes.Contains(data, []byte(`"type":"start"`)) {
					phoneStart <- struct{}{}
				}
				if bytes.Contains(data, []byte(`"type":"keyframe"`)) {
					phoneControlKeyframe <- struct{}{}
				}
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			for {
				ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
				_, data, err := conn.Read(ctx)
				cancel()
				if err != nil {
					return
				}
				if bytes.Contains(data, []byte(`"type":"keyframe"`)) {
					phoneVideoKeyframe <- struct{}{}
				}
			}
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()

	store := state.NewMemoryStore()
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
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	videoConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser video websocket: %v", err)
	}
	defer videoConn.Close(websocket.StatusNormalClosure, "test complete")

	waitForSignal(t, phoneStart, "initial phone start")
	waitForSignal(t, phoneVideoKeyframe, "initial phone video keyframe")
	if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"test_stale"}`)); err != nil {
		t.Fatalf("write recover_stream: %v", err)
	}
	waitForSignal(t, phoneStart, "recovery phone start")
	waitForSignal(t, phoneControlKeyframe, "recovery phone keyframe")
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
	if got != 1 {
		t.Fatalf("keyframe requests forwarded = %d want 1", got)
	}
}

func TestVideoRecoveryRequestsAreRateLimitedGlobally(t *testing.T) {
	_, phoneSignals, videoConn := newTicketVideoStreamTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for i := 0; i < 3; i++ {
		if err := videoConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"spam"}`)); err != nil {
			t.Fatal(err)
		}
	}

	got := countPhoneSignalTypesWithin(phoneSignals, 250*time.Millisecond)
	if got["start"] != 1 {
		t.Fatalf("stream recovery start requests forwarded = %d want 1", got["start"])
	}
	if got["keyframe"] != 1 {
		t.Fatalf("stream recovery keyframe requests forwarded = %d want 1", got["keyframe"])
	}
}

func TestControlSocketCanRequestStreamRecoveryWhenVideoSocketIsGone(t *testing.T) {
	phoneSignals := make(chan string, 64)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(phoneServer.Close)

	server, ticketServer, relay := newTicketRecoveryTestServer(t, phoneServer.URL)
	t.Cleanup(ticketServer.Close)
	t.Cleanup(relay.Close)

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controlConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser control websocket: %v", err)
	}
	defer controlConn.Close(websocket.StatusNormalClosure, "test complete")

	_ = server
	waitForPhoneSignal(t, phoneSignals, "start", "initial phone start")
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)

	if err := controlConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"control_video_gone"}`)); err != nil {
		t.Fatalf("write recover_stream over control socket: %v", err)
	}
	waitForPhoneSignalCounts(t, phoneSignals, map[string]int{"start": 1, "keyframe": 1}, "control recovery start and keyframe")
}

func TestStreamRecoveryStartsPhoneWhenRelayIsDisconnected(t *testing.T) {
	phoneSignals := make(chan string, 64)
	httpStart := make(chan struct{}, 4)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			select {
			case httpStart <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(phoneServer.Close)

	server, ticketServer, relay := newTicketRecoveryTestServer(t, phoneServer.URL)
	t.Cleanup(ticketServer.Close)
	t.Cleanup(relay.Close)

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
	wsBase := "ws" + strings.TrimPrefix(ticketServer.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controlConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser control websocket: %v", err)
	}
	defer controlConn.Close(websocket.StatusNormalClosure, "test complete")

	waitForPhoneSignal(t, phoneSignals, "start", "initial phone start")
	relay.Reconnect("test_force_disconnected_relay")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot := server.relay.Snapshot(); snapshot.Desired && snapshot.Viewers > 0 && !snapshot.Connected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)
	drainHTTPStarts(httpStart, 150*time.Millisecond)

	if err := controlConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"relay_disconnected"}`)); err != nil {
		t.Fatalf("write recover_stream over control socket: %v", err)
	}

	select {
	case <-httpStart:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery did not call phone HTTP start while relay was disconnected")
	}
	waitForPhoneSignal(t, phoneSignals, "start", "reconnected relay phone start")
}

func newTicketVideoStreamTestServer(t *testing.T) (*Server, <-chan string, *websocket.Conn) {
	t.Helper()
	phoneSignals := make(chan string, 64)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			readPhoneSignals(r.Context(), conn, phoneSignals)
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(phoneServer.Close)

	store := state.NewMemoryStore()
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
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	t.Cleanup(ticketServer.Close)

	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
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
	waitForPhoneSignal(t, phoneSignals, "start", "initial phone start")
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)
	return server, phoneSignals, videoConn
}

func newTicketRecoveryTestServer(t *testing.T, phoneBaseURL string) (*Server, *httptest.Server, *phone.Relay) {
	t.Helper()
	store := state.NewMemoryStore()
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
	}, store, relay)
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
	}
}

func waitForPhoneSignal(t *testing.T, phoneSignals <-chan string, signal string, label string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case got := <-phoneSignals:
			if got == signal {
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
			got[signal]++
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

func countPhoneSignalTypesWithin(phoneSignals <-chan string, duration time.Duration) map[string]int {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	counts := map[string]int{}
	for {
		select {
		case got := <-phoneSignals:
			counts[got]++
		case <-timer.C:
			return counts
		}
	}
}

func newStreamSharingTestServer(t *testing.T) (*Server, *httptest.Server, *phone.Relay) {
	t.Helper()
	store := state.NewMemoryStore()
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

func dialStreamTestClient(t *testing.T, ctx context.Context, serverURL string, sessionID string) *websocket.Conn {
	t.Helper()
	wsBase := "ws" + strings.TrimPrefix(serverURL, "http")
	header := http.Header{"X-Ticket-Remote-Email": []string{"ticket@jolkins.id.lv"}}
	header.Add("Cookie", "ticket_remote_session="+sessionID)
	conn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/stream", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser video websocket: %v", err)
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

func setTestControlGate(server *Server, sessionID string, email string) {
	server.gateMu.Lock()
	defer server.gateMu.Unlock()
	server.gate = &controlGate{
		sessionID: sessionID,
		email:     strings.ToLower(strings.TrimSpace(email)),
		expiresAt: time.Now().Add(time.Minute),
	}
}

func testTSF2KeyFrame() []byte {
	return append([]byte{'T', 'S', 'F', '2', 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1}, []byte{0x65, 0x88}...)
}
