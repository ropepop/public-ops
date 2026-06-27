package broker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeUpstream emulates the ticket_phone_bridge upstream for the broker.
// It supports /api/v1/session/start, /api/v1/session/stop, /api/v1/health, and
// /api/v1/session WebSocket connections.
type fakeUpstream struct {
	*httptest.Server
	mu                 sync.Mutex
	controlCodeRequest map[string]any
	pendingConns       chan *websocket.Conn
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{
		pendingConns: make(chan *websocket.Conn, 4),
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/health":
			f.mu.Lock()
			ctrl := cloneMap(f.controlCodeRequest)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                 true,
				"controlCodeRequest": ctrl,
			})
		case "/api/v1/session":
			conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
			if err != nil {
				t.Errorf("accept session ws: %v", err)
				return
			}
			select {
			case f.pendingConns <- conn:
			default:
				_ = conn.Close(websocket.StatusInternalError, "test queue full")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakeUpstream) setControlCodeRequest(ctrl map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.controlCodeRequest = ctrl
}

func (f *fakeUpstream) acceptNext(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-f.pendingConns:
		return conn
	case <-timeAfter(testingTimeout()):
		// No-op: fall through.
	}
	return nil
}

// testingTimeout returns a short timeout context for acceptNext. The
// helper avoids pulling in time at the top of the file twice.
func testingTimeout() time.Duration {
	return 2 * time.Second
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// timeAfter is a tiny shim so the helper above reads naturally.
func timeAfter(d time.Duration) <-chan time.Time {
	return time.After(d)
}

var _ = context.Background
