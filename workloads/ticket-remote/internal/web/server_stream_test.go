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
	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":1,"phoneUptimeMillis":10000}`))
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

func TestVideoClientLogsAreIgnoredOnVideoSocket(t *testing.T) {
	server, ticketServer, relay := newStreamSharingTestServer(t)
	defer ticketServer.Close()
	defer relay.Close()

	server.direct.setConfig([]byte(`{"type":"config","codec":"avc1.42E01E","transport":"h264-annexb","width":540,"height":1212,"streamEpoch":7}`))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

	_ = readNextTextMessageOfType(t, ctx, viewerConn, "config")
	if err := viewerConn.Write(ctx, websocket.MessageText, []byte(`{"type":"client_log","event":"stream_first_packet_ms","detail":"123"}`)); err != nil {
		t.Fatalf("write client log: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	snapshot := server.direct.snapshot(time.Now(), phone.Health{})
	event, ok := snapshot["lastBrowserEvent"].(clientTelemetryEvent)
	if ok && event.Event == "stream_first_packet_ms" {
		t.Fatalf("video client log must not be accepted on the media socket after Spacetime safe-log cutover: %#v", snapshot["lastBrowserEvent"])
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	controllerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "controller-session")
	defer controllerConn.Close(websocket.StatusNormalClosure, "test complete")
	viewerConn := dialStreamTestClient(t, ctx, ticketServer.URL, "viewer-session")
	defer viewerConn.Close(websocket.StatusNormalClosure, "test complete")

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
	if got := readNextTSF2Sequence(t, ctx, viewerConn, 80); parseTSF2(got).keyFrame {
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

func TestLiveStreamSuppressesBackgroundRecoveryCommands(t *testing.T) {
	server, phoneSignals, _ := newTicketVideoStreamTestServer(t)

	now := time.Now()
	server.direct.mu.Lock()
	server.direct.streamEpoch = 7
	server.direct.lastFrameAt = now
	server.direct.lastKeyFrameAt = now
	server.direct.lastFrameEpoch = 7
	server.direct.lastKeyFrameEpoch = 7
	server.direct.lastFrameSequence = 42
	server.direct.lastKeyFrameSequence = 42
	server.direct.lastFrameVisualAgeMillis = 100
	server.direct.lastKeyFrameVisualAgeMillis = 100
	server.direct.lastFrameVisualAgeKnown = true
	server.direct.lastKeyFrameVisualAgeKnown = true
	server.direct.lastBrowserMediaError = ""
	server.direct.mu.Unlock()
	drainPhoneSignals(phoneSignals, 150*time.Millisecond)

	if err := server.requestPhoneKeyframeNow("stale_video_frames"); err != nil {
		t.Fatalf("background keyframe suppression returned error: %v", err)
	}
	server.requestPhoneRecovery("stale_video_frames")

	if got := countPhoneSignalsWithin(phoneSignals, "keyframe", 250*time.Millisecond); got != 0 {
		t.Fatalf("live stream allowed background keyframe commands: %d", got)
	}
	if got := countPhoneSignalsWithin(phoneSignals, "recover_stream", 250*time.Millisecond); got != 0 {
		t.Fatalf("live stream allowed background recovery commands: %d", got)
	}

	if err := server.requestPhoneKeyframeNow("control_code_result_marker_low_latency"); err != nil {
		t.Fatalf("control-code keyframe returned error: %v", err)
	}
	waitForPhoneSignal(t, phoneSignals, "keyframe", "control-code keyframe")
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
