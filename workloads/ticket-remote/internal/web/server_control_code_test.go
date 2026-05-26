package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	other := dialTicketControlClient(t, httpServer, "other@jolkins.id.lv")
	defer other.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
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

	phoneResults <- `{"type":"ticket_state_event","ticketState":"generated_result","eventSeq":7,"requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"frameSequence":77,"minFrameSequence":77,"reason":"generated","totalDurationMillis":321,"phases":{"phone_command_received":0,"popup_ready":184,"digits_typed":312,"ok_tapped":455,"result_first_visible":2988,"result_marker_requested":3015}}`
	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"type":"control_code_request"`) ||
		!strings.Contains(privateResult, `"status":"succeeded"`) ||
		!strings.Contains(privateResult, `"value":"12345"`) ||
		!strings.Contains(privateResult, `"streamEpoch":42`) ||
		!strings.Contains(privateResult, `"frameSequence":77`) ||
		!strings.Contains(privateResult, `"minFrameSequence":77`) ||
		!strings.Contains(privateResult, `"markerReceivedAt"`) ||
		!strings.Contains(privateResult, `"totalDurationMillis":321`) ||
		!strings.Contains(privateResult, `"phone_command_received":0`) ||
		!strings.Contains(privateResult, `"popup_ready":184`) ||
		!strings.Contains(privateResult, `"digits_typed":312`) ||
		!strings.Contains(privateResult, `"ok_tapped":455`) ||
		!strings.Contains(privateResult, `"result_first_visible":2988`) ||
		!strings.Contains(privateResult, `"result_marker_requested":3015`) ||
		!strings.Contains(privateResult, `"cleanupPending":true`) {
		t.Fatalf("requester did not receive private result: %s", privateResult)
	}
	if strings.Contains(privateResult, `"imageBase64"`) ||
		strings.Contains(privateResult, `"imageMime"`) ||
		!strings.Contains(privateResult, `"captureRequired":true`) ||
		!strings.Contains(privateResult, `"captureReason":"waiting_for_browser_capture"`) {
		t.Fatalf("marker result should require browser capture without image bytes: %s", privateResult)
	}
	capture := postControlCodeCaptureRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", response.Request.ID, 42, 77)
	if !capture.OK || capture.Request.CaptureAcknowledgedAt == "" {
		t.Fatalf("browser capture ack failed: %#v", capture)
	}
	ack := waitForPhoneMessageText(t, messages, `"type":"control_code_browser_capture"`)
	if !strings.Contains(ack, `"requestId":"`+response.Request.ID+`"`) || !strings.Contains(ack, `"ok":true`) {
		t.Fatalf("phone capture ack mismatch: %s", ack)
	}
	assertNoBrowserMessageContaining(t, other, response.Request.ID, 250*time.Millisecond)
	assertNoBrowserMessageContaining(t, other, `"popup_ready"`, 250*time.Millisecond)
	assertNoBrowserMessageContaining(t, other, `"totalDurationMillis"`, 250*time.Millisecond)
}

func TestControlCodePhoneRootImageResultIsRejectedAndNeverExposesImageBytes(t *testing.T) {
	messages := make(chan string, 30)
	phoneResults := make(chan string, 30)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	other := dialTicketControlClient(t, httpServer, "other@jolkins.id.lv")
	defer other.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="
	phoneResults <- `{"type":"control_code_result","requestId":"` + response.Request.ID + `","ok":true,"reason":"generated","value":"12345","resultProof":"phone_root_image","imageMime":"image/png","imageBase64":"` + tinyPNGBase64 + `","totalDurationMillis":321,"phases":{"phone_command_received":0,"result_image_png_ready":318},"cleanupPending":true}`

	privateResult := waitForBrowserMessage(t, requester, `"reason":"control_code_phone_image_disabled"`)
	if !strings.Contains(privateResult, `"type":"control_code_request"`) ||
		!strings.Contains(privateResult, `"status":"failed"`) ||
		!strings.Contains(privateResult, `"reason":"control_code_phone_image_disabled"`) ||
		strings.Contains(privateResult, `"resultProof":"phone_root_image"`) ||
		strings.Contains(privateResult, `"imageMime":"image/png"`) ||
		strings.Contains(privateResult, `"imageBase64"`) ||
		strings.Contains(privateResult, tinyPNGBase64) {
		t.Fatalf("phone image result should fail without exposing image bytes: %s", privateResult)
	}
	ack := waitForPhoneMessageText(t, messages, `"type":"control_code_result_ack"`)
	if !strings.Contains(ack, `"requestId":"`+response.Request.ID+`"`) ||
		!strings.Contains(ack, `"ok":false`) ||
		!strings.Contains(ack, `"reason":"control_code_phone_image_disabled"`) {
		t.Fatalf("phone image rejection ack mismatch: %s", ack)
	}
	assertNoBrowserMessageContaining(t, other, response.Request.ID, 250*time.Millisecond)
	assertNoBrowserMessageContaining(t, other, tinyPNGBase64, 250*time.Millisecond)
}

func TestControlCodeRequestRetriesWhenPhoneDoesNotAcceptDispatch(t *testing.T) {
	messages := make(chan string, 20)
	phoneResults := make(chan string, 20)
	phoneServer := newTicketPhoneControlCodeTestServerWithOptions(t, messages, phoneResults, ticketPhoneControlCodeTestOptions{
		skipGenerateHealthAccepts: 1,
	})
	defer phoneServer.Close()

	store := newTicketMemoryStore(t, phoneServer.URL)
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:         "pixel",
		AttachName:        "Pixel",
		BaseURL:           phoneServer.URL,
		ReconnectMinDelay: 10 * time.Millisecond,
		ReconnectMaxDelay: 10 * time.Millisecond,
		NoViewerStopDelay: time.Hour,
	})
	defer relay.Close()
	server := newTicketWebServer(t, store, relay, phoneServer.URL)
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	firstCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(firstCommand, `"requestId":"`+response.Request.ID+`"`) {
		t.Fatalf("first phone command mismatch: %s", firstCommand)
	}
	secondCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(secondCommand, `"requestId":"`+response.Request.ID+`"`) ||
		!strings.Contains(secondCommand, `"dispatchAttempt":2`) {
		t.Fatalf("retry phone command mismatch: %s", secondCommand)
	}

	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"frameSequence":77,"minFrameSequence":77,"reason":"generated","resultProof":"phone_visual_raw_ticket_after_submit"}`
	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"requestId":"`+response.Request.ID+`"`) ||
		!strings.Contains(privateResult, `"resultProof":"phone_visual_raw_ticket_after_submit"`) {
		t.Fatalf("requester did not receive private result after retry: %s", privateResult)
	}
}

func TestControlCodeRequestHoldsTicketLeaseUntilPhoneCleanup(t *testing.T) {
	messages := make(chan string, 20)
	phoneResults := make(chan string, 20)
	phoneServer := newTicketPhoneControlCodeTestServer(t, messages, phoneResults)
	defer phoneServer.Close()
	leaseEvents := make(chan brokerLeaseEvent, 8)
	broker := newTicketLeaseBrokerRecorder(t, leaseEvents)
	defer broker.Close()

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
	server.cfg.Phone.BrokerBaseURL = broker.URL
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	controlLeaseID := "control-code:" + response.Request.ID
	acquire := expectBrokerLeaseEventWithLease(t, leaseEvents, "/api/v1/phone/leases/ticket", controlLeaseID)
	if acquire.LeaseID != "control-code:"+response.Request.ID || acquire.RequestID != response.Request.ID || acquire.Reason != "control_code_request" {
		t.Fatalf("control-code lease acquire = %#v request=%#v", acquire, response.Request)
	}
	phoneCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	for _, snippet := range []string{
		`"owner":"ticket"`,
		`"app":"vivi"`,
		`"flow":"control_code"`,
		`"requestId":"` + response.Request.ID + `"`,
	} {
		if !strings.Contains(phoneCommand, snippet) {
			t.Fatalf("phone command missing %q: %s", snippet, phoneCommand)
		}
	}

	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"frameSequence":76,"minFrameSequence":77,"reason":"generated","resultProof":"phone_root"}`
	waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	capture := postControlCodeCaptureRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", response.Request.ID, 42, 77)
	if !capture.OK {
		t.Fatalf("browser capture failed: %#v", capture)
	}
	waitForPhoneMessageText(t, messages, `"type":"control_code_browser_capture"`)

	select {
	case event := <-leaseEvents:
		if event.Path == "/api/v1/phone/leases/ticket/release" {
			t.Fatalf("ticket lease released before phone cleanup: %#v", event)
		}
	case <-time.After(150 * time.Millisecond):
	}
	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + response.Request.ID + `","ok":true,"reason":"ticket_detail"}`
	release := expectBrokerLeaseEventWithLease(t, leaseEvents, "/api/v1/phone/leases/ticket/release", controlLeaseID)
	if release.LeaseID != "control-code:"+response.Request.ID || release.RequestID != response.Request.ID {
		t.Fatalf("control-code lease release = %#v", release)
	}
}

func TestControlCodeAsyncFlowAcceptsDigitLengthsTwoThroughEight(t *testing.T) {
	messages := make(chan string, 100)
	phoneResults := make(chan string, 100)
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

	for length := 2; length <= 8; length++ {
		digits := strings.Repeat(fmt.Sprintf("%d", length%10), length)
		email := fmt.Sprintf("ticket+len%d@jolkins.id.lv", length)
		sessionID := fmt.Sprintf("session-len-%d", length)
		if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", email, state.RoleMember); err != nil {
			t.Fatal(err)
		}
		response := postControlCodeRequestWithSession(t, httpServer.URL, email, sessionID, digits)
		phoneCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
		if !strings.Contains(phoneCommand, `"requestId":"`+response.Request.ID+`"`) || !strings.Contains(phoneCommand, `"digits":"`+digits+`"`) {
			t.Fatalf("length %d phone command mismatch: %s", length, phoneCommand)
		}

		streamEpoch := int64(100 + length)
		minFrameSequence := int64(200 + length)
		phoneResults <- fmt.Sprintf(`{"type":"ticket_state_event","ticketState":"generated_result","eventSeq":%d,"requestId":"%s","value":"%s","streamEpoch":%d,"frameSequence":%d,"minFrameSequence":%d,"reason":"generated"}`,
			length,
			response.Request.ID, digits, streamEpoch, minFrameSequence, minFrameSequence)
		waitForControlCodeServerStatus(t, server, response.Request.ID, controlCodeSucceeded)
		server.codeMu.Lock()
		got := server.codeRequests[response.Request.ID]
		if got == nil || got.Value != digits {
			server.codeMu.Unlock()
			t.Fatalf("length %d marker result mismatch: %#v", length, got)
		}
		server.codeMu.Unlock()

		capture := postControlCodeCaptureRaw(t, httpServer.URL, email, sessionID, response.Request.ID, streamEpoch, minFrameSequence)
		if !capture.OK {
			t.Fatalf("length %d browser capture ack failed: %#v", length, capture)
		}
		phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + response.Request.ID + `","ok":true,"reason":"return_to_raw_complete"}`
		waitForControlCodeServerIdle(t, server, response.Request.ID)
	}
}

func TestControlCodeFrameReadyRequiresBrowserCaptureBeforeCleanup(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://phone.test"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://phone.test"}), "http://phone.test")
	req := &controlCodeRequest{
		ID:          "req-marker",
		SessionID:   "requester-session",
		Email:       "ticket@jolkins.id.lv",
		Digits:      "12345",
		Status:      controlCodeRunning,
		RequestedAt: time.Now().Add(-time.Second),
		StartedAt:   time.Now().Add(-time.Second),
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeRunning = req.ID
	server.codeMu.Unlock()

	server.markControlCodeFrameReady(req.ID, "12345", 42, 77, 77, 321, map[string]int64{"popup_opened": 50})

	server.codeMu.Lock()
	defer server.codeMu.Unlock()
	got := server.codeRequests[req.ID]
	if got.Status != controlCodeSucceeded || !got.CleanupPending || !got.CaptureRequired {
		t.Fatalf("frame-ready marker should wait for browser capture before cleanup: %#v", got)
	}
}

func TestControlCodeRawReturnBeforeBrowserCaptureFailsResult(t *testing.T) {
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	request := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)

	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + request.Request.ID + `","streamEpoch":42,"minFrameSequence":77,"reason":"generated"}`
	waitForBrowserMessage(t, requester, `"status":"succeeded"`)

	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + request.Request.ID + `","ok":true,"reason":"return_to_raw_complete"}`
	waitForBrowserMessage(t, requester, `"cleanupReason":"return_to_raw_complete"`)

	server.codeMu.Lock()
	got := server.codeRequests[request.Request.ID]
	server.codeMu.Unlock()
	if got == nil || got.Status != controlCodeFailed || got.Reason != "result_window_closed_before_capture" {
		t.Fatalf("raw return before browser capture must fail the result: %#v", got)
	}
}

func TestControlCodeRawReturnWhileRunningDoesNotExposeInternalCleanupReason(t *testing.T) {
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	request := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)

	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + request.Request.ID + `","ok":true,"reason":"return_to_raw_complete"}`
	result := waitForBrowserMessage(t, requester, `"status":"failed"`)
	if strings.Contains(result, `"reason":"return_to_raw_complete"`) {
		t.Fatalf("raw return cleanup marker must not leak as a failed request reason: %s", result)
	}
	if !strings.Contains(result, `"reason":"control_code_not_generated"`) {
		t.Fatalf("raw return before generated frame should use a user-facing failure reason: %s", result)
	}
}

func TestControlCodePrepareSendsPhonePrepareWithoutQueueingRequest(t *testing.T) {
	messages := make(chan string, 10)
	phoneResults := make(chan string, 10)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodePrepare(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session")
	if !response.OK {
		t.Fatalf("prepare failed: %#v", response)
	}
	phoneCommand := waitForPhoneMessageText(t, messages, `"type":"prepare_control_code"`)
	if !strings.Contains(phoneCommand, `"reason":"dialog_open"`) {
		t.Fatalf("phone prepare command mismatch: %s", phoneCommand)
	}

	server.codeMu.Lock()
	queued := len(server.codeQueue)
	running := server.codeRunning
	server.codeMu.Unlock()
	if queued != 0 || running != "" {
		t.Fatalf("prepare should not queue a control-code request, queued=%d running=%q", queued, running)
	}
}

func TestControlCodeRequestWaitsForSocketAfterDirectPhoneStart(t *testing.T) {
	messages := make(chan string, 20)
	startRequests := make(chan struct{}, 5)
	phoneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			startRequests <- struct{}{}
			w.WriteHeader(http.StatusOK)
		case "/api/v1/session":
			time.Sleep(controlCodeRelayConnectWait + 300*time.Millisecond)
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept phone control websocket: %v", err)
				return
			}
			defer conn.Close(websocket.StatusNormalClosure, "test complete")
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

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")

	select {
	case <-startRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("control-code relay prep did not call direct phone session start")
	}
	phoneCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(phoneCommand, `"requestId":"`+response.Request.ID+`"`) || !strings.Contains(phoneCommand, `"digits":"12345"`) {
		t.Fatalf("phone command mismatch after delayed socket readiness: %s", phoneCommand)
	}
}

func TestControlCodeResultRoutesOnlyToRequestingSession(t *testing.T) {
	messages := make(chan string, 10)
	phoneResults := make(chan string, 10)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	sameEmailOtherSession := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "other-session")
	defer sameEmailOtherSession.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)

	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"frameSequence":77,"minFrameSequence":77,"reason":"generated","totalDurationMillis":321}`

	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"streamEpoch":42`) ||
		!strings.Contains(privateResult, `"frameSequence":77`) ||
		!strings.Contains(privateResult, `"minFrameSequence":77`) ||
		!strings.Contains(privateResult, `"captureRequired":true`) {
		t.Fatalf("requesting session should receive marker result awaiting browser capture: %s", privateResult)
	}
	assertNoBrowserMessageContaining(t, sameEmailOtherSession, response.Request.ID, 250*time.Millisecond)
}

func TestControlCodeFrameReadyMovesRequestToSucceededAndRoutesOnlyToRequester(t *testing.T) {
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	otherSession := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "other-session")
	defer otherSession.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"frameSequence":76,"minFrameSequence":77,"reason":"generated","resultProof":"phone_visual","resultProofAt":"2026-05-22T10:23:45.123Z","totalDurationMillis":432,"phases":{"popup_opened":50}}`

	result := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	for _, snippet := range []string{
		`"value":"12345"`,
		`"reason":"generated"`,
		`"resultProof":"phone_visual"`,
		`"resultFrameEpoch":42`,
		`"resultMinFrameSequence":77`,
		`"resultProofAt":"2026-05-22T10:23:45.123Z"`,
		`"cleanupPending":true`,
	} {
		if !strings.Contains(result, snippet) {
			t.Fatalf("marker update missing %q: %s", snippet, result)
		}
	}
	for _, stale := range []string{`"source":"browser_canvas_local"`} {
		if strings.Contains(result, stale) {
			t.Fatalf("marker update should not include browser capture field %q: %s", stale, result)
		}
	}
	assertNoBrowserMessageContaining(t, otherSession, response.Request.ID, 250*time.Millisecond)
}

func TestControlCodeFrameReadyPreservesPhonePostSubmitVisualProof(t *testing.T) {
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"frameSequence":76,"minFrameSequence":77,"reason":"generated","resultProof":"phone_visual_raw_ticket_after_submit","resultProofAt":"2026-05-22T10:23:45.123Z"}`

	result := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(result, `"resultProof":"phone_visual_raw_ticket_after_submit"`) {
		t.Fatalf("marker update must preserve trusted post-submit phone proof: %s", result)
	}
}

func TestControlCodeFrameReadyRequiresBrowserPostBeforePhoneCleanup(t *testing.T) {
	messages := make(chan string, 30)
	phoneResults := make(chan string, 30)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"minFrameSequence":77,"reason":"generated"}`
	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"cleanupPending":true`) ||
		!strings.Contains(privateResult, `"captureRequired":true`) ||
		strings.Contains(privateResult, `"source":"browser_canvas_local"`) ||
		strings.Contains(privateResult, `"imageBase64"`) ||
		strings.Contains(privateResult, `"imageMime"`) {
		t.Fatalf("requester did not receive capture-required result: %s", privateResult)
	}
	capture := postControlCodeCaptureRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", response.Request.ID, 42, 77)
	if !capture.OK || capture.Request.CaptureAcknowledgedAt == "" {
		t.Fatalf("browser capture ack failed: %#v", capture)
	}
	waitForPhoneMessageText(t, messages, `"type":"control_code_browser_capture"`)
}

func TestControlCodeFrameReadyIsIdempotentForOwningSession(t *testing.T) {
	messages := make(chan string, 40)
	phoneResults := make(chan string, 40)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"minFrameSequence":77,"reason":"generated"}`
	first := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(first, `"value":"12345"`) {
		t.Fatalf("first marker result mismatch: %s", first)
	}
	server.markControlCodeFrameReady(response.Request.ID, "12345", 42, 77, 77, 500, nil)
	server.codeMu.Lock()
	got := server.codeRequests[response.Request.ID]
	server.codeMu.Unlock()
	if got == nil || got.Status != controlCodeSucceeded || got.Value != "12345" {
		t.Fatalf("duplicate marker should leave the successful result intact: %#v", got)
	}
}

func TestControlCodePhoneImageResultDoesNotSucceed(t *testing.T) {
	messages := make(chan string, 30)
	phoneResults := make(chan string, 30)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	phoneResults <- `{"type":"control_code_result","requestId":"` + response.Request.ID + `","ok":true,"reason":"generated","value":"12345","imageMime":"image/png","imageBase64":"legacy-phone-png"}`

	failed := waitForBrowserMessage(t, requester, `"reason":"control_code_stream_marker_required"`)
	if !strings.Contains(failed, `"status":"failed"`) || strings.Contains(failed, `"imageBase64"`) || strings.Contains(failed, `"imageMime"`) {
		t.Fatalf("legacy phone image result should not succeed or expose image bytes: %s", failed)
	}
}

func TestControlCodeFrameReadyDoesNotExposeMarkerToOtherSession(t *testing.T) {
	messages := make(chan string, 30)
	phoneResults := make(chan string, 30)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	otherSession := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "other-session")
	defer otherSession.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	response := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + response.Request.ID + `","value":"12345","streamEpoch":42,"minFrameSequence":77}`
	waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	assertNoBrowserMessageContaining(t, otherSession, response.Request.ID, 250*time.Millisecond)
}

func TestControlCodeMarkerResultKeepsQueueBlockedUntilPhoneCleanup(t *testing.T) {
	messages := make(chan string, 30)
	phoneResults := make(chan string, 30)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	first := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	second := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "67890")
	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + first.Request.ID + `","value":"12345","streamEpoch":42,"minFrameSequence":77}`
	result := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(result, `"cleanupPending":true`) {
		t.Fatalf("marker result should stay cleanup-pending: %s", result)
	}
	assertNoPhoneMessageContaining(t, messages, `"type":"control_code_browser_capture"`, 250*time.Millisecond)
	assertNoPhoneMessageContaining(t, messages, `"requestId":"`+second.Request.ID+`"`, 250*time.Millisecond)

	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + first.Request.ID + `","ok":true,"reason":"ticket_detail"}`
	secondCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(secondCommand, `"requestId":"`+second.Request.ID+`"`) {
		t.Fatalf("second request should start after failed capture cleanup: %s", secondCommand)
	}
}

func TestControlCodeLatestRequestOnReconnectIsSessionScoped(t *testing.T) {
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
	req := &controlCodeRequest{
		ID:          "req-reconnect",
		SessionID:   "requester-session",
		Email:       "ticket@jolkins.id.lv",
		Digits:      "12345",
		Status:      controlCodeSucceeded,
		RequestedAt: time.Now().Add(-5 * time.Second),
		CompletedAt: time.Now(),
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeMu.Unlock()
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	privateResult := waitForBrowserMessage(t, requester, `"requestId":"req-reconnect"`)
	if strings.Contains(privateResult, `"imageBase64"`) || strings.Contains(privateResult, `"imageMime"`) {
		t.Fatalf("latest browser-local result should not include image bytes: %s", privateResult)
	}

	sameEmailOtherSession := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "other-session")
	defer sameEmailOtherSession.Close(websocket.StatusNormalClosure, "test complete")
	assertNoBrowserMessageContaining(t, sameEmailOtherSession, `"requestId":"req-reconnect"`, 250*time.Millisecond)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	first := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	firstCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(firstCommand, `"requestId":"`+first.Request.ID+`"`) {
		t.Fatalf("first phone command mismatch: %s", firstCommand)
	}
	second := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "67890")

	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + first.Request.ID + `","streamEpoch":42,"minFrameSequence":77,"reason":"generated"}`
	privateResult := waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	if !strings.Contains(privateResult, `"cleanupPending":true`) ||
		!strings.Contains(privateResult, `"captureRequired":true`) ||
		strings.Contains(privateResult, `"imageBase64"`) {
		t.Fatalf("requester did not receive browser-capture-pending result: %s", privateResult)
	}
	assertNoPhoneMessageContaining(t, messages, `"type":"control_code_browser_capture"`, 250*time.Millisecond)
	assertNoPhoneMessageContaining(t, messages, `"requestId":"`+second.Request.ID+`"`, 250*time.Millisecond)

	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + first.Request.ID + `","ok":true,"reason":"ticket_detail"}`
	secondCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(secondCommand, `"requestId":"`+second.Request.ID+`"`) || !strings.Contains(secondCommand, `"digits":"67890"`) {
		t.Fatalf("second phone command should start after cleanup: %s", secondCommand)
	}
}

func TestControlCodeCleanupAttentionKeepsSuccessfulResultVisible(t *testing.T) {
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	request := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)

	phoneResults <- `{"type":"control_code_frame_ready","requestId":"` + request.Request.ID + `","streamEpoch":42,"minFrameSequence":77,"reason":"generated"}`
	waitForBrowserMessage(t, requester, `"status":"succeeded"`)
	postControlCodeCaptureRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", request.Request.ID, 42, 77)
	waitForPhoneMessageText(t, messages, `"type":"control_code_browser_capture"`)

	phoneResults <- `{"type":"control_code_cleanup_complete","requestId":"` + request.Request.ID + `","ok":false,"reason":"control_code_cleanup_attention_needed"}`
	update := waitForBrowserMessage(t, requester, `"cleanupReason":"control_code_cleanup_attention_needed"`)
	if !strings.Contains(update, `"status":"succeeded"`) || strings.Contains(update, `"imageBase64"`) {
		t.Fatalf("cleanup attention must keep generated result visible: %s", update)
	}
	if strings.Contains(update, `"status":"failed"`) {
		t.Fatalf("cleanup attention must not turn generated result into failure: %s", update)
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

	requester := dialTicketControlClientWithSession(t, httpServer, "ticket@jolkins.id.lv", "requester-session")
	defer requester.Close(websocket.StatusNormalClosure, "test complete")
	waitForPhoneMessage(t, messages, `"type":"start"`)

	first := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "12345")
	firstCommand := waitForPhoneMessageText(t, messages, `"type":"generate_control_code"`)
	if !strings.Contains(firstCommand, `"requestId":"`+first.Request.ID+`"`) {
		t.Fatalf("first phone command mismatch: %s", firstCommand)
	}
	second := postControlCodeRequestWithSession(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", "67890")

	view, ok := server.closeControlCodeRequest("ticket@jolkins.id.lv", "requester-session", first.Request.ID, time.Now())
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

	accepted := postControlCodeRequestRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "56164989")
	if !accepted.OK || accepted.Request.ID == "" {
		t.Fatalf("expected 8 digit code to be accepted, got %#v", accepted)
	}

	for _, digits := range []string{"1", "123456789", "1234567890", "12A4", "12 34", ""} {
		failure := postControlCodeRequestFailure(t, httpServer.URL, "ticket@jolkins.id.lv", digits)
		if failure.Error != "invalid_code" {
			t.Fatalf("digits %q error = %q, want invalid_code", digits, failure.Error)
		}
	}
}

func TestHealthRedactsControlCodePhoneHealth(t *testing.T) {
	phoneURL := "http://127.0.0.1:1"
	store := newTicketMemoryStore(t, phoneURL)
	server := newTicketWebServer(t, store, phone.NewRelay(phone.RelayConfig{BaseURL: phoneURL}), phoneURL)
	_, err := store.UpdatePhone(context.Background(), state.PhoneInput{
		TicketID:     "vivi-default",
		BackendID:    "pixel",
		AttachName:   "Pixel",
		BaseURL:      phoneURL,
		DesiredState: "streaming",
		HealthJSON:   `{"type":"health","data":{"controlCodeRequest":{"requestId":"req-private","status":"succeeded","reason":"generated","value":"561649898","digits":"561649898","totalDurationMillis":321,"phases":{"popup_ready":184},"imageMime":"image/png","imageBase64":"PRIVATE_IMAGE"},"ticketStateEvent":{"ticketState":"generated_result","requestId":"req-private","value":"561649898","totalDurationMillis":321,"phases":{"popup_ready":184}},"streamActive":true}}`,
		Now:          time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"561649898", "PRIVATE_IMAGE", "req-private", `"digits"`, `"imageBase64"`, `"imageMime"`, `"totalDurationMillis"`, `"phases"`, "popup_ready"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("health response leaked private control-code field %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "controlCodeRequest") || !strings.Contains(body, "ticketStateEvent") {
		t.Fatalf("health response should keep non-sensitive control-code diagnostics: %s", body)
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
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeMu.Unlock()

	view, ok := server.closeControlCodeRequest("ticket@jolkins.id.lv", "session", req.ID, time.Now())
	if !ok {
		t.Fatal("expected close to find request")
	}
	if view.Status != controlCodeClosed {
		t.Fatalf("status = %q, want closed", view.Status)
	}
}

func TestControlCodeCloseRequiresRequestingSession(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	req := &controlCodeRequest{
		ID:          "req-close-session",
		SessionID:   "requester-session",
		Email:       "ticket@jolkins.id.lv",
		Digits:      "1234",
		Status:      controlCodeSucceeded,
		RequestedAt: time.Now(),
		CompletedAt: time.Now(),
	}
	server.codeMu.Lock()
	server.codeRequests[req.ID] = req
	server.codeMu.Unlock()

	otherClose := postControlCodeCloseRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "other-session", req.ID)
	if otherClose.OK {
		t.Fatalf("different session with same email should not close requester result: %#v", otherClose)
	}

	requesterClose := postControlCodeCloseRaw(t, httpServer.URL, "ticket@jolkins.id.lv", "requester-session", req.ID)
	if !requesterClose.OK {
		t.Fatal("requesting session should close its result")
	}
	if requesterClose.Request.Status != controlCodeClosed {
		t.Fatalf("status = %q, want closed", requesterClose.Request.Status)
	}
}

func TestControlCodeSucceededViewExpiresAfterSixtySeconds(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	capturedAt := now.Add(5 * time.Second)
	req := &controlCodeRequest{
		ID:                    "req-expire",
		SessionID:             "session",
		Email:                 "ticket@jolkins.id.lv",
		Digits:                "1234",
		Status:                controlCodeSucceeded,
		RequestedAt:           now.Add(-5 * time.Second),
		CompletedAt:           now,
		CaptureAcknowledgedAt: capturedAt,
	}
	server.codeMu.Lock()
	view := server.controlCodeViewLocked(req, now)
	expiredView := server.controlCodeViewLocked(req, capturedAt.Add(61*time.Second))
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
	if !expiresAt.Equal(capturedAt.Add(60 * time.Second)) {
		t.Fatalf("expiresAt = %s, want %s", expiresAt, capturedAt.Add(60*time.Second))
	}
	if expiredView.Status != controlCodeExpired {
		t.Fatalf("expired status = %q, want expired", expiredView.Status)
	}
}

func TestControlCodePendingBrowserCaptureDoesNotExpireOrStartDisplayTTL(t *testing.T) {
	server := newTicketWebServer(t, newTicketMemoryStore(t, "http://127.0.0.1:1"), phone.NewRelay(phone.RelayConfig{BaseURL: "http://127.0.0.1:1"}), "http://127.0.0.1:1")
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	req := &controlCodeRequest{
		ID:              "req-capture-pending",
		SessionID:       "session",
		Email:           "ticket@jolkins.id.lv",
		Digits:          "1234",
		Status:          controlCodeSucceeded,
		RequestedAt:     now.Add(-2 * time.Minute),
		CompletedAt:     now.Add(-70 * time.Second),
		CaptureRequired: true,
		CaptureReason:   "waiting_for_browser_capture",
		CleanupPending:  true,
	}
	server.codeMu.Lock()
	view := server.controlCodeViewLocked(req, now)
	server.codeMu.Unlock()

	if view.Status != controlCodeSucceeded {
		t.Fatalf("status = %q, want succeeded while browser capture is pending", view.Status)
	}
	if !view.CaptureRequired || view.CaptureReason != "waiting_for_browser_capture" {
		t.Fatalf("capture state changed before browser proof: %#v", view)
	}
	if view.ResultExpiresAt != "" || view.ResultRemainingMS != 0 {
		t.Fatalf("pending browser capture should not expose a result countdown: %#v", view)
	}
}

func TestControlCodeSucceededRequestExpiresStoredValueWithoutBrowserView(t *testing.T) {
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
	if expired.Value != "" {
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

type ticketPhoneControlCodeTestOptions struct {
	skipGenerateHealthAccepts int
}

func newTicketPhoneControlCodeTestServer(t *testing.T, messages chan<- string, results <-chan string) *httptest.Server {
	return newTicketPhoneControlCodeTestServerWithOptions(t, messages, results, ticketPhoneControlCodeTestOptions{})
}

func newTicketPhoneControlCodeTestServerWithOptions(t *testing.T, messages chan<- string, results <-chan string, options ticketPhoneControlCodeTestOptions) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	controlCodeRequest := map[string]any{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/session/start":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/health":
			mu.Lock()
			request := cloneMapForControlCodeTest(controlCodeRequest)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":                 true,
				"controlCodeRequest": request,
			})
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
				message := string(data)
				var payload map[string]any
				if err := json.Unmarshal(data, &payload); err == nil && payload["type"] == "generate_control_code" {
					mu.Lock()
					if options.skipGenerateHealthAccepts > 0 {
						options.skipGenerateHealthAccepts--
					} else {
						controlCodeRequest = map[string]any{
							"requestId": payload["requestId"],
							"status":    "running",
							"reason":    "accepted",
						}
					}
					mu.Unlock()
				}
				messages <- message
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

func cloneMapForControlCodeTest(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func dialTicketControlClient(t *testing.T, server *httptest.Server, email string) *websocket.Conn {
	t.Helper()
	return dialTicketControlClientWithSession(t, server, email, "")
}

func dialTicketControlClientWithSession(t *testing.T, server *httptest.Server, email string, sessionID string) *websocket.Conn {
	t.Helper()
	header := http.Header{"X-Ticket-Remote-Email": []string{email}}
	if sessionID != "" {
		header.Set("Cookie", "ticket_remote_session="+sessionID)
	}
	conn, _, err := websocket.Dial(context.Background(), wsURL(server, "/api/v1/session"), &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatal(err)
	}
	prewarmTicketStreamForSession(t, server.URL, email, sessionID)
	return conn
}

func prewarmTicketStreamForSession(t *testing.T, serverURL string, email string, sessionID string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/stream/prewarm", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", email)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "ticket_remote_session", Value: sessionID})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("prewarm status = %d body = %s", resp.StatusCode, string(body))
	}
}

type controlCodeRequestResponse struct {
	OK      bool                   `json:"ok"`
	Error   string                 `json:"error"`
	Message string                 `json:"message"`
	Request controlCodeRequestView `json:"request"`
}

type controlCodePrepareResponse struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error"`
	Message string `json:"message"`
	Ready   bool   `json:"ready"`
}

func postControlCodePrepare(t *testing.T, serverURL string, email string, sessionID string) controlCodePrepareResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/control-code/prepare", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", email)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "ticket_remote_session", Value: sessionID})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded controlCodePrepareResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func postControlCodeRequest(t *testing.T, serverURL string, email string, digits string) controlCodeRequestResponse {
	t.Helper()
	response := postControlCodeRequestRaw(t, serverURL, email, digits)
	if !response.OK || response.Request.ID == "" {
		t.Fatalf("request failed unexpectedly: %#v", response)
	}
	return response
}

func postControlCodeRequestWithSession(t *testing.T, serverURL string, email string, sessionID string, digits string) controlCodeRequestResponse {
	t.Helper()
	response := postControlCodeRequestRawWithSession(t, serverURL, email, sessionID, digits)
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
	return postControlCodeRequestRawWithSession(t, serverURL, email, "", digits)
}

func postControlCodeRequestRawWithSession(t *testing.T, serverURL string, email string, sessionID string, digits string) controlCodeRequestResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"digits": digits})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/control-code/request", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", email)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "ticket_remote_session", Value: sessionID})
	}
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

func postControlCodeCloseRaw(t *testing.T, serverURL string, email string, sessionID string, requestID string) controlCodeRequestResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"requestId": requestID})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/control-code/close", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", email)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "ticket_remote_session", Value: sessionID})
	}
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

func postControlCodeCaptureRaw(t *testing.T, serverURL string, email string, sessionID string, requestID string, frameEpoch int64, frameSequence int64) controlCodeRequestResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"requestId":              requestID,
		"candidateFrameEpoch":    frameEpoch,
		"candidateFrameSequence": frameSequence,
		"acceptedReason":         "candidate_frame_at_or_after_phone_marker",
	})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/control-code/capture", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", email)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: "ticket_remote_session", Value: sessionID})
	}
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

func waitForControlCodeServerStatus(t *testing.T, server *Server, requestID string, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.codeMu.Lock()
		req := server.codeRequests[requestID]
		status := ""
		if req != nil {
			status = req.Status
		}
		server.codeMu.Unlock()
		if status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.codeMu.Lock()
	req := server.codeRequests[requestID]
	server.codeMu.Unlock()
	t.Fatalf("control-code request %s did not reach status %q: request=%#v", requestID, want, req)
}

func waitForControlCodeServerIdle(t *testing.T, server *Server, requestID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.codeMu.Lock()
		running := server.codeRunning
		req := server.codeRequests[requestID]
		cleanupPending := req != nil && req.CleanupPending
		server.codeMu.Unlock()
		if running == "" && !cleanupPending {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.codeMu.Lock()
	running := server.codeRunning
	req := server.codeRequests[requestID]
	server.codeMu.Unlock()
	t.Fatalf("control-code server did not return idle for %s: running=%q request=%#v", requestID, running, req)
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
