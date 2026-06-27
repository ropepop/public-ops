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

func TestBrowserHeartbeatKeepsPhoneBackendActive(t *testing.T) {
	phoneCommands := make(chan string, 8)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			<-ctx.Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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
	storeForServer := state.Store(store)
	if sink := ticketStreamCommandSink(phoneServer.URL); sink != nil {
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
			BaseURL:    phoneServer.URL,
			Backends:   []config.PhoneBackend{{ID: "pixel", AttachName: "Pixel", BaseURL: phoneServer.URL}},
		},
	}, storeForServer, relay)
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

	controlConn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser control websocket: %v", err)
	}
	defer controlConn.Close(websocket.StatusNormalClosure, "test complete")
	if err := controlConn.Write(ctx, websocket.MessageText, []byte(`{"type":"activity"}`)); err != nil {
		t.Fatalf("send browser activity: %v", err)
	}

	got := waitForPhoneMessageText(t, phoneCommands, `"type":"activity"`)
	if !strings.Contains(got, `"reason":"browser_activity"`) {
		t.Fatalf("stream activity command = %s", got)
	}
}

func TestBrowserControlSocketAloneDoesNotStartPhoneBackend(t *testing.T) {
	phoneCommands := make(chan string, 8)

	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()
			<-ctx.Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer phoneServer.Close()
	registerTicketStreamCommandSink(t, phoneServer.URL, phoneCommands)

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

	if got := relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("control socket should not count as a stream viewer, got %d", got)
	}
	select {
	case message := <-phoneCommands:
		if strings.Contains(message, `"type":"start"`) {
			t.Fatalf("control socket without video viewer started the phone stream: %s", message)
		}
	case <-time.After(250 * time.Millisecond):
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

func TestAuthenticatedIndexPrewarmStartsBeforeStateLookupCompletes(t *testing.T) {
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
	waitForPhoneMessage(t, phoneCommands, `"type":"start"`)
	close(blockingStore.releaseSnapshot)
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("index status = %d body = %s", rec.Code, rec.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("index response did not finish after state lookup was released")
	}
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

func TestVideoSocketUsesCachedStateBeforeStoreRefresh(t *testing.T) {
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
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	connReady := make(chan *websocket.Conn, 1)
	go func() {
		connReady <- dialStreamTestClient(t, ctx, ticketServer.URL, "cached-fast-video")
	}()

	var conn *websocket.Conn
	select {
	case conn = <-connReady:
		defer conn.Close(websocket.StatusNormalClosure, "test complete")
	case <-time.After(350 * time.Millisecond):
		t.Fatal("video socket waited for store refresh despite fresh cached state")
	}
}

func TestStreamPrewarmHoldIsOnlyAStartupBridge(t *testing.T) {
	if streamPrewarmHold > 5*time.Second {
		t.Fatalf("stream prewarm hold = %s, want <= 5s", streamPrewarmHold)
	}
}

func TestStreamRecoveryRateLimitsStayInsideFreshnessContract(t *testing.T) {
	if recoveryRequestGlobalInterval > 1500*time.Millisecond {
		t.Fatalf("global stream recovery interval = %s, want <= 1.5s so stale recovery can complete inside 2s", recoveryRequestGlobalInterval)
	}
	if recoveryRequestPerClientInterval > 1500*time.Millisecond {
		t.Fatalf("per-client stream recovery interval = %s, want <= 1.5s so stale recovery can complete inside 2s", recoveryRequestPerClientInterval)
	}
}

func TestVideoWarmStartKeyFrameIsSharedDuringControlSession(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	keyFrame := testTSF2FrameWithTimestamp(1, 1, true, 10000)
	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))
	server.direct.recordFrame(keyFrame)
	setTestControlGate(server, "controller-session", "ticket@jolkins.id.lv")

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

	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1}`))

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

func TestVideoWarmConfigIsSentBeforePresenceHeartbeatFinishes(t *testing.T) {
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
	blockingStore := &blockingHeartbeatStore{
		Store:            store,
		heartbeatStarted: make(chan struct{}),
		releaseHeartbeat: make(chan struct{}),
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
	}, blockingStore, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()
	defer close(blockingStore.releaseHeartbeat)

	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":7}`))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	readCtx, readCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer readCancel()
	msg := readNextTextMessageOfType(t, readCtx, viewerConn, "config")
	if msg["provisional"] != true || int64(msg["streamEpoch"].(float64)) != 0 {
		t.Fatalf("warm config should be provisional with epoch 0: %#v", msg)
	}
	select {
	case <-blockingStore.heartbeatStarted:
	case <-time.After(time.Second):
		t.Fatal("presence heartbeat did not start after warm config")
	}
}

func TestVideoClientLogsAreHandledBeforePresenceHeartbeatFinishes(t *testing.T) {
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
	blockingStore := &blockingHeartbeatStore{
		Store:            store,
		heartbeatStarted: make(chan struct{}),
		releaseHeartbeat: make(chan struct{}),
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
	}, blockingStore, relay)
	if err != nil {
		t.Fatal(err)
	}
	ticketServer := httptest.NewServer(server)
	defer ticketServer.Close()
	defer relay.Close()
	defer close(blockingStore.releaseHeartbeat)

	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":7}`))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	readCtx, readCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer readCancel()
	_ = readNextTextMessageOfType(t, readCtx, viewerConn, "config")
	select {
	case <-blockingStore.heartbeatStarted:
	case <-time.After(time.Second):
		t.Fatal("presence heartbeat did not start")
	}
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	if err := viewerConn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"client_log","event":"stream_first_packet_ms","detail":"123"}`)); err != nil {
		writeCancel()
		t.Fatalf("write client log: %v", err)
	}
	writeCancel()

	time.Sleep(350 * time.Millisecond)
	snapshot := server.direct.snapshot(time.Now(), phone.Health{})
	if event, ok := snapshot["lastBrowserEvent"].(clientTelemetryEvent); ok && event.Event == "stream_first_packet_ms" {
		t.Fatalf("video client log should not be accepted on the media socket: %#v", event)
	}
}

func TestPresenceUpdatesUseBackgroundTimeoutLongerThanPageLookup(t *testing.T) {
	if presenceUpdateTimeout <= stateLookupTimeout {
		t.Fatalf("presence updates run outside the user path and should tolerate slower Spacetime calls: presence=%s lookup=%s", presenceUpdateTimeout, stateLookupTimeout)
	}
	if presenceUpdateTimeout > 5*time.Second {
		t.Fatalf("presence timeout should stay bounded: %s", presenceUpdateTimeout)
	}
}

func TestSpacetimePresenceFallbackWritesForExistingServerSession(t *testing.T) {
	memoryStore := state.NewMemoryStore()
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
			SpacetimeDatabase: "ticket-remote-prod-v2",
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
	conn, _, err := websocket.Dial(ctx, wsBase+"/api/v1/session", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("dial browser control websocket: %v", err)
	}
	_ = readNextTextMessageOfType(t, ctx, conn, "state")

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat","reason":"direct_presence_active"}`)); err != nil {
		t.Fatalf("write direct heartbeat: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := store.heartbeats.Load(); got != 0 {
		t.Fatalf("direct Spacetime heartbeat should not write through server, got %d writes", got)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat","reason":"spacetime_direct_unavailable","presenceFallback":true}`)); err != nil {
		t.Fatalf("write fallback heartbeat: %v", err)
	}
	waitForAtomicCount(t, &store.heartbeats, 1, time.Second, "fallback heartbeat")

	_ = conn.Close(websocket.StatusNormalClosure, "test complete")
	waitForAtomicCount(t, &store.disconnects, 1, time.Second, "fallback disconnect")
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
	frame := testTSF2KeyFrameWithEpoch(1, 77, true)
	server.broadcastFrame(frame)

	if got := readNextBinaryFrame(t, ctx, controllerConn); !bytes.Equal(got, frame) {
		t.Fatalf("controller frame = %q", string(got))
	}
	if got := readNextBinaryFrame(t, ctx, viewerConn); !bytes.Equal(got, frame) {
		t.Fatalf("viewer frame = %q", string(got))
	}
}

func TestDeltaFramesWaitForKeyframeThenStayLive(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()
	server.handlePhoneText([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))

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
			t.Fatal("delta frame should not be delivered before the viewer has a keyframe")
		}
	}
	readCancel()
	_ = coldViewerConn.Close(websocket.StatusNormalClosure, "test complete")

	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	keyFrame := testTSF2FrameWithTimestamp(1, 79, true, 10001)
	deltaFrame := testTSF2FrameWithTimestamp(1, 80, false, 10002)
	server.handlePhoneMessage(phone.Message{Binary: keyFrame})
	if got := readNextBinaryFrame(t, ctx, viewerConn); parseTSF2(got).sequence != 79 || !parseTSF2(got).keyFrame {
		t.Fatalf("viewer keyframe = %x", got)
	}
	server.handlePhoneMessage(phone.Message{Binary: deltaFrame})
	if got := readNextBinaryFrame(t, ctx, viewerConn); parseTSF2(got).sequence != 80 || parseTSF2(got).keyFrame {
		t.Fatalf("viewer delta frame = %x", got)
	}
}

func TestVideoStreamDoesNotSendStreamStatus(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))
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
	}, &recordingTicketStore{Store: store, commandSink: phoneSignals}, relay)
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

	time.Sleep(keyframeRequestGlobalInterval + 100*time.Millisecond)
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
	server, _, _ := newTicketVideoStreamTestServer(t)

	server.direct.mu.Lock()
	before := server.direct.lastKeyframeRequestedAt
	server.direct.mu.Unlock()

	server.handlePhoneText([]byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":900,"height":1852,"rootCapture":true,"streamEpoch":42}`))

	server.direct.mu.Lock()
	after := server.direct.lastKeyframeRequestedAt
	server.direct.mu.Unlock()
	if after.IsZero() || !after.After(before) {
		t.Fatalf("phone config did not request a fresh keyframe; before=%v after=%v", before, after)
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

func TestControlSocketCanRequestStreamRecoveryWhenVideoSocketIsGone(t *testing.T) {
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

	server.retainRelayViewerForPrewarm("test-visible-page", streamPrewarmHold)
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)

	if err := controlConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"control_video_gone"}`)); err != nil {
		t.Fatalf("write recover_stream over control socket: %v", err)
	}
	waitForPhoneSignalCounts(t, phoneSignals, map[string]int{"recover_stream": 1}, "control recovery command")
	if got := countPhoneSignalsWithin(phoneSignals, "start", 250*time.Millisecond); got != 0 {
		t.Fatalf("connected control recovery should not restart phone stream; starts=%d", got)
	}
}

func TestStreamRecoveryAfterRelayReconnectDoesNotDuplicateStart(t *testing.T) {
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

	server.retainRelayViewerForPrewarm("test-visible-page", streamPrewarmHold)
	relay.Reconnect("test_force_disconnected_relay")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot := server.relay.Snapshot(); snapshot.Desired && snapshot.Viewers > 0 && !snapshot.Connected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)

	if err := controlConn.Write(ctx, websocket.MessageText, []byte(`{"type":"recover_stream","reason":"relay_disconnected"}`)); err != nil {
		t.Fatalf("write recover_stream over control socket: %v", err)
	}

	waitForPhoneSignal(t, phoneSignals, "recover_stream", "recovered relay command")
	if got := countPhoneSignalsWithin(phoneSignals, "start", 250*time.Millisecond); got != 0 {
		t.Fatalf("recovered relay should use keyframes without duplicate starts; starts=%d", got)
	}
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
	}, &recordingTicketStore{Store: store, commandSink: phoneSignals}, relay)
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
	waitForPhoneSignal(t, phoneSignals, "keyframe", "initial phone keyframe")
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

type blockingHeartbeatStore struct {
	state.Store
	heartbeatStarted chan struct{}
	releaseHeartbeat chan struct{}
}

type spacetimeBackendCountingStore struct {
	state.Store
	heartbeats  atomic.Int32
	disconnects atomic.Int32
}

func (s *spacetimeBackendCountingStore) Backend() string {
	return "spacetime"
}

func (s *spacetimeBackendCountingStore) HeartbeatPresence(ctx context.Context, input state.PresenceInput) (state.Snapshot, error) {
	s.heartbeats.Add(1)
	return s.Store.HeartbeatPresence(ctx, input)
}

func (s *spacetimeBackendCountingStore) DisconnectPresence(ctx context.Context, ticketID string, sessionID string, now time.Time) (state.Snapshot, error) {
	s.disconnects.Add(1)
	return s.Store.DisconnectPresence(ctx, ticketID, sessionID, now)
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

func (s *blockingHeartbeatStore) HeartbeatPresence(ctx context.Context, input state.PresenceInput) (state.Snapshot, error) {
	select {
	case <-s.heartbeatStarted:
	default:
		close(s.heartbeatStarted)
	}
	select {
	case <-s.releaseHeartbeat:
	case <-ctx.Done():
		return state.Snapshot{}, ctx.Err()
	}
	return s.Store.HeartbeatPresence(ctx, input)
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
