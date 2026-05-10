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

	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestControlCodeRequestQueuesPhoneCommandAndRoutesResultPrivately(t *testing.T) {
	messages := make(chan string, 10)
	phoneResults := make(chan string, 10)
	phoneServer := newTicketPhoneControlCodeTestServer(t, messages, phoneResults)
	defer phoneServer.Close()

	store := newTicketMemoryStore(t, phoneServer.URL)
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "other@jolkins.id.lv", state.RoleMember); err != nil {
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
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	requester := dialTicketControlClient(t, httpServer, "ticket@jolkins.id.lv")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	other := dialTicketControlClient(t, httpServer, "other@jolkins.id.lv")
	defer other.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "12345")
	if response.Request.ID == "" {
		t.Fatalf("expected request id in %#v", response)
	}
	if response.Request.Status != "queued" && response.Request.Status != "running" {
		t.Fatalf("expected queued or running request, got %#v", response.Request)
	}
	phoneCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(phoneCommand, `"requestId":"`+response.Request.ID+`"`) || !strings.Contains(phoneCommand, `"digits":"12345"`) {
		t.Fatalf("phone command mismatch: %s", phoneCommand)
	}

	phoneResults <- `{"type":"control_code_result","requestId":"` + response.Request.ID + `","ok":true,"imageMime":"image/png","imageBase64":"cG5n","value":"12345","reason":"generated","totalDurationMillis":321}`

	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"type":"control_code_request"`) ||
		!strings.Contains(privateResult, `"status":"succeeded"`) ||
		!strings.Contains(privateResult, `"imageBase64":"cG5n"`) {
		t.Fatalf("requester did not receive private result: %s", privateResult)
	}
	assertNoBrowserMessageContaining(t, other, response.Request.ID, 250*time.Millisecond)
}

func TestControlCodeResultDeliveryKeepsQueueBlockedUntilPhoneCleanup(t *testing.T) {
	messages := make(chan string, 20)
	phoneResults := make(chan string, 20)
	phoneServer := newTicketPhoneControlCodeTestServer(t, messages, phoneResults)
	defer phoneServer.Close()

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

	requester := dialTicketControlClient(t, httpServer, "ticket@jolkins.id.lv")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	first := postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "12345")
	firstCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(firstCommand, `"requestId":"`+first.Request.ID+`"`) {
		t.Fatalf("first phone command mismatch: %s", firstCommand)
	}
	second := postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "67890")

	phoneResults <- `{"type":"control_code_result","requestId":"` + first.Request.ID + `","ok":true,"imageMime":"image/png","imageBase64":"cG5n","reason":"generated","cleanupPending":true}`
	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"cleanupPending":true`) || !strings.Contains(privateResult, `"imageBase64":"cG5n"`) {
		t.Fatalf("requester did not receive cleanup-pending result: %s", privateResult)
	}
	assertNoPhoneMessageContaining(t, messages, `"requestId":"`+second.Request.ID+`"`, 250*time.Millisecond)

	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + first.Request.ID + `","ok":true,"reason":"ticket_detail"}`
	secondCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(secondCommand, `"requestId":"`+second.Request.ID+`"`) || !strings.Contains(secondCommand, `"digits":"67890"`) {
		t.Fatalf("second phone command should start after cleanup: %s", secondCommand)
	}
}

func TestControlCodeCloseRunningRequestDoesNotReleasePhoneQueue(t *testing.T) {
	messages := make(chan string, 20)
	phoneResults := make(chan string, 20)
	phoneServer := newTicketPhoneControlCodeTestServer(t, messages, phoneResults)
	defer phoneServer.Close()

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

	requester := dialTicketControlClient(t, httpServer, "ticket@jolkins.id.lv")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	first := postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "12345")
	firstCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(firstCommand, `"requestId":"`+first.Request.ID+`"`) {
		t.Fatalf("first phone command mismatch: %s", firstCommand)
	}
	second := postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "67890")

	view, ok := server.closeControlCodeRequest("ticket@jolkins.id.lv", first.Request.ID, time.Now())
	if !ok {
		t.Fatal("expected close to find running request")
	}
	if view.Status != controlCodeRunning {
		t.Fatalf("close should leave running request active, got %q", view.Status)
	}
	server.codeMu.Lock()
	running := server.codeRunning
	status := server.codeRequests[first.Request.ID].Status
	server.codeMu.Unlock()
	if running != first.Request.ID {
		t.Fatalf("codeRunning = %q, want first request %q", running, first.Request.ID)
	}
	if status != controlCodeRunning {
		t.Fatalf("first status = %q, want running", status)
	}
	assertNoPhoneMessageContaining(t, messages, `"requestId":"`+second.Request.ID+`"`, 250*time.Millisecond)

	phoneResults <- `{"type":"control_code_result","requestId":"` + first.Request.ID + `","ok":false,"reason":"control_code_not_generated"}`
	waitForBrowserMessage(t, requester, `"status":"failed"`)
}

func TestControlCodeRequestRateLimitsThirdAcceptedRequestInSixtySeconds(t *testing.T) {
	store := newTicketMemoryStore(t, "http://127.0.0.1:1")
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           "http://127.0.0.1:1",
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, "http://127.0.0.1:1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "11")
	postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "22")
	failure := postControlCodeRequestFailure(t, httpServer.URL, "ticket@jolkins.id.lv", "33")
	if failure.Error != "rate_limited" {
		t.Fatalf("expected rate_limited, got %#v", failure)
	}
}

func TestControlCodeRequestFailsFastWhenPhoneUnavailable(t *testing.T) {
	store := newTicketMemoryStore(t, "http://127.0.0.1:1")
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           "http://127.0.0.1:1",
		ReconnectMinDelay: time.Hour,
		ReconnectMaxDelay: time.Hour,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, "http://127.0.0.1:1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	response := postControlCodeRequest(t, httpServer.URL, "ticket@jolkins.id.lv", "44")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.codeMu.Lock()
		req := server.codeRequests[response.Request.ID]
		running := server.codeRunning
		server.codeMu.Unlock()
		if req != nil && req.Status == controlCodeFailed {
			if req.Reason != "phone_unavailable" {
				t.Fatalf("reason = %q, want phone_unavailable", req.Reason)
			}
			if running != "" {
				t.Fatalf("codeRunning = %q, want cleared", running)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("request did not fail fast when phone unavailable")
}

func TestControlCodeRequestRejectsNonNumericOrWrongLengthCodes(t *testing.T) {
	store := newTicketMemoryStore(t, "http://127.0.0.1:1")
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	accepted := postControlCodeRequestRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "561649898")
	if !accepted.OK || accepted.Request.ID == "" {
		t.Fatalf("expected 9 digit code to be accepted, got %#v", accepted)
	}

	for _, digits := range []string{"1", "1234567890", "12A4", "12 34", ""} {
		failure := postControlCodeRequestFailure(t, httpServer.URL, "ticket@jolkins.id.lv", digits)
		if failure.Error != "invalid_code" {
			t.Fatalf("digits %q error = %q, want invalid_code", digits, failure.Error)
		}
	}
}

func TestControlCodeCloseClearsVisibleRequesterResult(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	req := &controlCodeRequest{
		ID:          "req-close",
		SessionID:   "session",
		Email:       "ticket@jolkins.id.lv",
		Digits:      "1234",
		Status:      controlCodeSucceeded,
		RequestedAt: time.Now(),
		CompletedAt: time.Now(),
		ImageMime:   "image/png",
		ImageBase64: "cG5n",
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeMu.Unlock()

	view, ok := server.closeControlCodeRequest("ticket@jolkins.id.lv", req.ID, time.Now())
	if !ok {
		t.Fatal("expected close to find request")
	}
	if view.Status != controlCodeClosed {
		t.Fatalf("status = %q, want closed", view.Status)
	}
	if view.ImageBase64 != "" {
		t.Fatalf("closed request still exposed image: %#v", view)
	}
}

func TestControlCodeSucceededViewExpiresAfterSixtySeconds(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	req := &controlCodeRequest{
		ID:          "req-expire",
		SessionID:   "session",
		Email:       "ticket@jolkins.id.lv",
		Digits:      "1234",
		Status:      controlCodeSucceeded,
		RequestedAt: now.Add(-5 * time.Second),
		CompletedAt: now,
		ImageMime:   "image/png",
		ImageBase64: "cG5n",
	}
	server.codeMu.Lock()
	view := server.controlCodeViewLocked(req, now)
	expiredView := server.controlCodeViewLocked(req, now.Add(61*time.Second))
	server.codeMu.Unlock()

	if view.Status != controlCodeSucceeded {
		t.Fatalf("fresh status = %q, want succeeded", view.Status)
	}
	if view.ResultExpiresAt == "" {
		t.Fatalf("fresh successful result should include expiry timestamp: %#v", view)
	}
	expiresAt, err := time.Parse(time.RFC3339, view.ResultExpiresAt)
	if err != nil {
		t.Fatalf("parse result expiry: %v", err)
	}
	if !expiresAt.Equal(now.Add(60 * time.Second)) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, now.Add(60*time.Second))
	}
	if expiredView.Status != controlCodeExpired {
		t.Fatalf("expired status = %q, want expired", expiredView.Status)
	}
	if expiredView.ImageBase64 != "" {
		t.Fatalf("expired result still exposed image: %#v", expiredView)
	}
}

func TestControlCodeSucceededRequestExpiresStoredImageWithoutBrowserView(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	req := &controlCodeRequest{
		ID:          "req-expire-background",
		SessionID:   "session",
		Email:       "ticket@jolkins.id.lv",
		Digits:      "561649898",
		Status:      controlCodeSucceeded,
		RequestedAt: time.Now().Add(-70 * time.Second),
		CompletedAt: time.Now().Add(-61 * time.Second),
		Value:       "561649898",
		ImageMime:   "image/png",
		ImageBase64: "cG5n",
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeMu.Unlock()

	server.expireControlCodeRequestAt(req.ID, req.CompletedAt.Add(controlCodeResultTTL))

	server.codeMu.Lock()
	expired := server.codeRequests[req.ID]
	server.codeMu.Unlock()
	if expired == nil {
		t.Fatal("expired request should retain short-lived metadata")
	}
	if expired.Status != controlCodeExpired {
		t.Fatalf("status = %q, want expired", expired.Status)
	}
	if expired.ImageBase64 != "" || expired.ImageMime != "" || expired.Value != "" {
		t.Fatalf("expired request retained private result data: %#v", expired)
	}
}

func TestControlCodePruneDropsOldInactiveRequestsOnly(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	now := time.Now()
	oldClosed := &controlCodeRequest{
		ID:          "old-closed",
		Email:       "ticket@jolkins.id.lv",
		Status:      controlCodeClosed,
		RequestedAt: now.Add(-10 * time.Minute),
		CompletedAt: now.Add(-9 * time.Minute),
	}
	oldFailed := &controlCodeRequest{
		ID:          "old-failed",
		Email:       "ticket@jolkins.id.lv",
		Status:      controlCodeFailed,
		RequestedAt: now.Add(-10 * time.Minute),
		CompletedAt: now.Add(-9 * time.Minute),
	}
	queued := &controlCodeRequest{
		ID:          "queued",
		Email:       "ticket@jolkins.id.lv",
		Status:      controlCodeQueued,
		RequestedAt: now.Add(-10 * time.Minute),
	}
	running := &controlCodeRequest{
		ID:          "running",
		Email:       "ticket@jolkins.id.lv",
		Status:      controlCodeRunning,
		RequestedAt: now.Add(-10 * time.Minute),
		StartedAt:   now.Add(-9 * time.Minute),
	}
	server.codeMu.Lock()
	server.codeRequests[oldClosed.ID] = oldClosed
	server.codeRequests[oldFailed.ID] = oldFailed
	server.codeRequests[queued.ID] = queued
	server.codeRequests[running.ID] = running
	server.codeQueue = []string{oldClosed.ID, queued.ID}
	server.codeRunning = running.ID
	server.pruneControlCodeRequestsLocked(now)
	_, hasOldClosed := server.codeRequests[oldClosed.ID]
	_, hasOldFailed := server.codeRequests[oldFailed.ID]
	_, hasQueued := server.codeRequests[queued.ID]
	_, hasRunning := server.codeRequests[running.ID]
	queue := append([]string(nil), server.codeQueue...)
	server.codeMu.Unlock()

	if hasOldClosed || hasOldFailed {
		t.Fatalf("old inactive requests were not pruned: closed=%v failed=%v", hasOldClosed, hasOldFailed)
	}
	if !hasQueued || !hasRunning {
		t.Fatalf("active requests should not be pruned: queued=%v running=%v", hasQueued, hasRunning)
	}
	if len(queue) != 1 || queue[0] != queued.ID {
		t.Fatalf("queue = %#v, want only queued request", queue)
	}
}

func newTicketPhoneControlCodeTestServer(t *testing.T, messages chan<- string, results <-chan string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			go func() {
				for result := range results {
					_ = conn.Write(context.Background(), websocket.MessageText, []byte(result))
				}
			}()
			for {
				_, data, err := conn.Read(r.Context())
				if err != nil {
					return
				}
				messages <- string(data)
			}
		case "/api/v1/stream":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone video websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
			_, _, _ = conn.Read(r.Context())
			<-r.Context().Done()
		case "/api/v1/session/stop":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
}

func dialTicketControlClient(t *testing.T, server *httptest.Server, email string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(context.Background(), wsURL(server, "/api/v1/session"), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ticket-Remote-Email": []string{email}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

type controlCodeRequestResponse struct {
	OK      bool                   `json:"ok"`
	Error   string                 `json:"error"`
	Message string                 `json:"message"`
	Request controlCodeRequestView `json:"request"`
}

func postControlCodeRequest(t *testing.T, serverURL string, email string, digits string) controlCodeRequestResponse {
	t.Helper()
	response := postControlCodeRequestRaw(t, serverURL, email, digits)
	if !response.OK || response.Request.ID == "" {
		t.Fatalf("request failed unexpectedly: %#v", response)
	}
	return response
}

func postControlCodeRequestFailure(t *testing.T, serverURL string, email string, digits string) controlCodeRequestResponse {
	t.Helper()
	response := postControlCodeRequestRaw(t, serverURL, email, digits)
	if response.OK {
		t.Fatalf("request succeeded unexpectedly: %#v", response)
	}
	return response
}

func postControlCodeRequestRaw(t *testing.T, serverURL string, email string, digits string) controlCodeRequestResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"digits": digits})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/control-code/request", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", email)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded controlCodeRequestResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func assertNoBrowserMessageContaining(t *testing.T, conn *websocket.Conn, snippet string, wait time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageText && strings.Contains(string(data), snippet) {
			t.Fatalf("unexpected browser message containing %q: %s", snippet, string(data))
		}
	}
}

func assertNoPhoneMessageContaining(t *testing.T, messages <-chan string, snippet string, wait time.Duration) {
	t.Helper()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case message := <-messages:
			if strings.Contains(message, snippet) {
				t.Fatalf("unexpected phone message containing %q: %s", snippet, message)
			}
		case <-timer.C:
			return
		}
	}
}
