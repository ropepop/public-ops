package broker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

func TestNewRequiresUpstreamURL(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error when UpstreamBaseURL is empty")
	}
}

func TestNewAppliesDefaultTicketGrace(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if b.cfg.TicketGrace != defaultTicketGrace {
		t.Fatalf("ticket grace = %s, want %s", b.cfg.TicketGrace, defaultTicketGrace)
	}
}

func TestHandlerExposesTicketAPIs(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	state := getState(t, server.URL)
	if state["currentOwner"] != "none" {
		t.Fatalf("idle current owner = %#v, want none", state["currentOwner"])
	}
	if state["ticketViewers"].(float64) != 0 {
		t.Fatalf("ticketViewers = %#v, want 0", state["ticketViewers"])
	}
}

func TestUpdateTicketPresenceSetsTicketActive(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.UpdateTicketPresence(context.Background(), TicketPresenceInput{Viewers: 1}); err != nil {
		t.Fatalf("UpdateTicketPresence: %v", err)
	}
	snap := b.Snapshot(time.Now())
	if snap.CurrentOwner != "ticket" {
		t.Fatalf("currentOwner = %q, want ticket", snap.CurrentOwner)
	}
	if !snap.TicketActive {
		t.Fatal("TicketActive = false, want true while viewers present")
	}
}

func TestUpdateTicketPresenceClearsAfterGrace(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 30 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := b.UpdateTicketPresence(context.Background(), TicketPresenceInput{Viewers: 1}); err != nil {
		t.Fatalf("UpdateTicketPresence: %v", err)
	}
	if err := b.UpdateTicketPresence(context.Background(), TicketPresenceInput{Viewers: 0}); err != nil {
		t.Fatalf("UpdateTicketPresence: %v", err)
	}
	if !b.ticketActiveLocked(time.Now()) {
		t.Fatal("ticket should still be active during grace")
	}
	time.Sleep(60 * time.Millisecond)
	if b.ticketActiveLocked(time.Now()) {
		t.Fatal("ticket should be inactive after grace")
	}
}

func TestTicketPresenceHTTPClearsState(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	postJSON(t, server.URL+"/api/v1/ticket/presence", `{"viewers":1}`)
	state := getState(t, server.URL)
	if state["currentOwner"] != "ticket" {
		t.Fatalf("after presence=1 currentOwner = %#v", state["currentOwner"])
	}

	postJSON(t, server.URL+"/api/v1/ticket/presence", `{"viewers":0}`)
	time.Sleep(50 * time.Millisecond)
	state = getState(t, server.URL)
	if state["currentOwner"] != "none" {
		t.Fatalf("after grace expired currentOwner = %#v", state["currentOwner"])
	}
}

func TestTicketLeaseAcquireAndRelease(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp := postJSON(t, server.URL+"/api/v1/phone/leases/ticket", `{"reason":"control_lease","ttlMillis":2000}`)
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode lease: %v", err)
	}
	lease, ok := payload["lease"].(map[string]any)
	if !ok {
		t.Fatalf("lease not in response: %#v", payload)
	}
	if lease["owner"] != "ticket" {
		t.Fatalf("lease.owner = %#v, want ticket", lease["owner"])
	}
	if lease["reason"] != "control_lease" {
		t.Fatalf("lease.reason = %#v", lease["reason"])
	}
	leaseID, _ := lease["id"].(string)
	if leaseID == "" {
		t.Fatal("lease id is empty")
	}
	state := getState(t, server.URL)
	if state["currentOwner"] != "ticket" {
		t.Fatalf("after acquire currentOwner = %#v", state["currentOwner"])
	}
	if state["leaseReason"] != "control_lease" {
		t.Fatalf("leaseReason = %#v", state["leaseReason"])
	}

	body := `{"leaseId":"` + leaseID + `"}`
	resp = postJSON(t, server.URL+"/api/v1/phone/leases/ticket/release", body)
	resp.Body.Close()
	state = getState(t, server.URL)
	if reason, _ := state["leaseReason"].(string); reason != "" {
		t.Fatalf("after release leaseReason = %q", reason)
	}
}

func TestTicketLeaseAcquireEmitsServiceEvent(t *testing.T) {
	events := make(chan map[string]any, 4)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer sink.Close()

	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{
		UpstreamBaseURL: upstream.URL,
		TicketGrace:     25 * time.Millisecond,
		EventSink:       EventSinkConfig{URL: sink.URL, Token: "secret"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	if _, err := b.AcquireTicketLease(context.Background(), TicketLeaseInput{
		LeaseID:   "lease-test",
		RequestID: "req-test",
		Reason:    "control_code",
		TTL:       10 * time.Second,
	}); err != nil {
		t.Fatalf("AcquireTicketLease: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event["action"] != "lease_acquired" {
				continue
			}
			if event["source"] != "phone_broker" || event["category"] != "broker" || event["requestId"] != "req-test" {
				t.Fatalf("event = %#v", event)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for lease_acquired event")
		}
	}
}

func TestUnavailableEventSinkDoesNotBlockTicketLease(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{
		UpstreamBaseURL: upstream.URL,
		TicketGrace:     25 * time.Millisecond,
		EventSink:       EventSinkConfig{URL: "http://127.0.0.1:1", Token: "secret"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	started := time.Now()
	if _, err := b.AcquireTicketLease(context.Background(), TicketLeaseInput{
		LeaseID: "lease-fast",
		Reason:  "control_code",
		TTL:     10 * time.Second,
	}); err != nil {
		t.Fatalf("AcquireTicketLease: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("AcquireTicketLease blocked on unavailable sink for %s", elapsed)
	}
}

func TestChatGPTRoutesAreNotExposed(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	for _, path := range []string{
		"/api/v1/phone/leases/chatgpt",
		"/api/v1/phone/leases/chatgpt/release",
		"/api/v1/chatgpt/run-text-job",
	} {
		resp := postJSON(t, server.URL+path, `{}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestTicketLeaseRejectsTTLOutOfRange(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	cases := []struct {
		name string
		ttl  int64
		want string
	}{
		{"too short clamps to min", 100, ""},
		{"too long clamps to max", int64(20 * time.Minute / time.Millisecond), ""},
		{"default when zero", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"reason":"x","ttlMillis":` + intToString(tc.ttl) + `}`
			resp := postJSON(t, server.URL+"/api/v1/phone/leases/ticket", body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

func TestProxySessionStopAndStart(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp := postJSON(t, server.URL+"/api/v1/session/start", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session/start status = %d", resp.StatusCode)
	}
	resp = postJSON(t, server.URL+"/api/v1/session/stop", `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("session/stop status = %d", resp.StatusCode)
	}
}

func TestHealthExposesUpstreamControlCodeRequest(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	upstream.setControlCodeRequest(map[string]any{
		"requestId": "req-1",
		"status":    "running",
		"reason":    "",
		"value":     "11111",
	})
	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode health: %v body=%s", err, body)
	}
	ctrl, ok := payload["controlCodeRequest"].(map[string]any)
	if !ok {
		t.Fatalf("controlCodeRequest missing from health: %s", body)
	}
	if ctrl["requestId"] != "req-1" {
		t.Fatalf("controlCodeRequest.requestId = %#v", ctrl["requestId"])
	}
}

func TestHealthReportsUpstreamErrorWhenDown(t *testing.T) {
	b, err := New(Config{UpstreamBaseURL: "http://127.0.0.1:1", TicketGrace: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	upstream, _ := payload["upstream"].(map[string]any)
	if upstream == nil {
		t.Fatalf("upstream missing: %s", body)
	}
	if upstream["ok"] != false {
		t.Fatalf("upstream.ok = %#v, want false", upstream["ok"])
	}
}

func TestStrictHealthFailsWhenUpstreamUnavailable(t *testing.T) {
	b, err := New(Config{UpstreamBaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/health?strict=1")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body := readBody(t, resp)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["ok"] != false {
		t.Fatalf("payload.ok = %#v, want false", payload["ok"])
	}
}

func TestStrictHealthAllowsNormalSlowUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		time.Sleep(300 * time.Millisecond)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	defer upstream.Close()

	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/health?strict=1")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", resp.StatusCode, readBody(t, resp))
	}
}

func TestProxyWebsocketRelaysMessages(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL, TicketGrace: 25 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()
	wsURL := strings.Replace(server.URL, "http", "ws", 1) + "/api/v1/session"

	client, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial client ws: %v", err)
	}
	defer client.Close(websocket.StatusNormalClosure, "test done")

	if err := client.Write(context.Background(), websocket.MessageText, []byte("ping-from-client")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	upstreamConn := upstream.acceptNext(t)
	defer upstreamConn.Close(websocket.StatusNormalClosure, "test done")

	_, data, err := upstreamConn.Read(context.Background())
	if err != nil {
		t.Fatalf("upstream read: %v", err)
	}
	if string(data) != "ping-from-client" {
		t.Fatalf("upstream got %q, want ping-from-client", string(data))
	}
	if err := upstreamConn.Write(context.Background(), websocket.MessageText, []byte("pong-from-upstream")); err != nil {
		t.Fatalf("upstream write: %v", err)
	}
	_, data, err = client.Read(context.Background())
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(data) != "pong-from-upstream" {
		t.Fatalf("client got %q, want pong-from-upstream", string(data))
	}
}

func TestProxyWebsocketClosedEventIncludesTransferDetail(t *testing.T) {
	events := make(chan map[string]any, 16)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode event: %v", err)
		}
		events <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer sink.Close()

	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{
		UpstreamBaseURL: upstream.URL,
		TicketGrace:     25 * time.Millisecond,
		EventSink:       EventSinkConfig{URL: sink.URL, Token: "secret"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)
	server := httptest.NewServer(b.Handler())
	defer server.Close()
	wsURL := strings.Replace(server.URL, "http", "ws", 1) + "/api/v1/session"

	client, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial client ws: %v", err)
	}
	if err := client.Write(context.Background(), websocket.MessageText, []byte("ping-from-client")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	upstreamConn := upstream.acceptNext(t)
	defer upstreamConn.Close(websocket.StatusNormalClosure, "test done")

	if _, data, err := upstreamConn.Read(context.Background()); err != nil {
		t.Fatalf("upstream read: %v", err)
	} else if string(data) != "ping-from-client" {
		t.Fatalf("upstream got %q, want ping-from-client", string(data))
	}
	if err := upstreamConn.Write(context.Background(), websocket.MessageText, []byte("pong-from-upstream")); err != nil {
		t.Fatalf("upstream write: %v", err)
	}
	if _, data, err := client.Read(context.Background()); err != nil {
		t.Fatalf("client read: %v", err)
	} else if string(data) != "pong-from-upstream" {
		t.Fatalf("client got %q, want pong-from-upstream", string(data))
	}
	_ = client.Close(websocket.StatusNormalClosure, "test done")

	event := waitForBrokerEvent(t, events, "upstream_proxy_closed")
	if event["source"] != "phone_broker" || event["category"] != "broker" {
		t.Fatalf("event identity = %#v", event)
	}
	safeState, ok := event["safeState"].(map[string]any)
	if !ok {
		t.Fatalf("safeState missing from event: %#v", event)
	}
	if safeState["path"] != "/api/v1/session" {
		t.Fatalf("path = %#v", safeState["path"])
	}
	for _, key := range []string{
		"durationMillis",
		"closeSide",
		"closeStatus",
		"closeOperation",
		"clientToUpstreamMessages",
		"clientToUpstreamBytes",
		"upstreamToClientMessages",
		"upstreamToClientBytes",
	} {
		if _, ok := safeState[key]; !ok {
			t.Fatalf("safeState missing %q: %#v", key, safeState)
		}
	}
	if got, _ := safeState["clientToUpstreamMessages"].(float64); got < 1 {
		t.Fatalf("clientToUpstreamMessages = %#v, want at least 1", safeState["clientToUpstreamMessages"])
	}
	if got, _ := safeState["upstreamToClientMessages"].(float64); got < 1 {
		t.Fatalf("upstreamToClientMessages = %#v, want at least 1", safeState["upstreamToClientMessages"])
	}
}

func TestSnapshotJSONRedactionStripsInternalFields(t *testing.T) {
	upstream := newFakeUpstream(t)
	defer upstream.Close()
	b, err := New(Config{UpstreamBaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(b.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/v1/state")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	for _, forbidden := range []string{"rigassatiksme", "rsLogin", "queueDepth", "jobs", "runningJobID"} {
		if strings.Contains(strings.ToLower(string(body)), strings.ToLower(forbidden)) {
			t.Fatalf("state body leaked %q: %s", forbidden, body)
		}
	}
}

func getState(t *testing.T, baseURL string) map[string]any {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/v1/state")
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode state: %v body=%s", err, body)
	}
	state, ok := payload["state"].(map[string]any)
	if !ok {
		t.Fatalf("state missing: %s", body)
	}
	return state
}

func waitForBrokerEvent(t *testing.T, events <-chan map[string]any, action string) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event["action"] == action {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for broker event %q", action)
		}
	}
}

func postJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return body
}

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
