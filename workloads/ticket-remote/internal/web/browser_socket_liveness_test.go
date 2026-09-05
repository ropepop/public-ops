package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

type browserLivenessTestServer struct {
	server    *httptest.Server
	connected chan struct{}
	messages  chan string
	done      chan struct{}
}

func newBrowserLivenessTestServer(t *testing.T, idle, timeout time.Duration) *browserLivenessTestServer {
	t.Helper()
	testServer := &browserLivenessTestServer{
		connected: make(chan struct{}, 1),
		messages:  make(chan string, 1),
		done:      make(chan struct{}, 1),
	}
	testServer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() {
			_ = conn.CloseNow()
			testServer.done <- struct{}{}
		}()
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		activity := make(chan struct{}, 1)
		livenessDone := make(chan struct{})
		go func() {
			defer close(livenessDone)
			if browserVideoLivenessLoop(ctx, conn, activity, idle, timeout) != nil {
				_ = conn.CloseNow()
			}
		}()
		defer func() {
			cancel()
			<-livenessDone
		}()
		testServer.connected <- struct{}{}
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			signalBrowserVideoActivity(activity)
			if typ == websocket.MessageText {
				testServer.messages <- string(data)
			}
		}
	}))
	t.Cleanup(testServer.server.Close)
	return testServer
}

func (s *browserLivenessTestServer) url() string {
	return "ws" + strings.TrimPrefix(s.server.URL, "http")
}

func TestBrowserVideoLivenessKeepsResponsiveQuietPeer(t *testing.T) {
	const idle = 20 * time.Millisecond
	const timeout = 100 * time.Millisecond
	testServer := newBrowserLivenessTestServer(t, idle, timeout)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, testServer.url(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	select {
	case <-testServer.connected:
	case <-ctx.Done():
		t.Fatal("server did not accept responsive peer")
	}
	time.Sleep(4 * idle)
	if err := conn.Write(ctx, websocket.MessageText, []byte("still-responsive")); err != nil {
		t.Fatalf("responsive quiet peer was closed: %v", err)
	}
	select {
	case got := <-testServer.messages:
		if got != "still-responsive" {
			t.Fatalf("message = %q", got)
		}
	case <-ctx.Done():
		t.Fatal("responsive peer message was not received")
	}
	_ = conn.CloseNow()
	select {
	case <-readDone:
	case <-ctx.Done():
		t.Fatal("responsive peer reader did not stop")
	}
}

func TestBrowserVideoLivenessClosesUnresponsiveQuietPeer(t *testing.T) {
	const idle = 20 * time.Millisecond
	const timeout = 30 * time.Millisecond
	testServer := newBrowserLivenessTestServer(t, idle, timeout)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, testServer.url(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	select {
	case <-testServer.connected:
	case <-ctx.Done():
		t.Fatal("server did not accept unresponsive peer")
	}
	started := time.Now()
	select {
	case <-testServer.done:
		if elapsed := time.Since(started); elapsed > 10*(idle+timeout) {
			t.Fatalf("unresponsive peer close took %s", elapsed)
		}
	case <-ctx.Done():
		t.Fatal("unresponsive quiet peer was not closed")
	}
}

func TestBrowserVideoLivenessCancellationStopsPromptly(t *testing.T) {
	testServer := newBrowserLivenessTestServer(t, time.Hour, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, testServer.url(), nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-testServer.connected:
	case <-ctx.Done():
		t.Fatal("server did not accept cancellation peer")
	}
	if err := conn.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-testServer.done:
	case <-ctx.Done():
		t.Fatal("liveness monitor did not stop with its connection")
	}
}
