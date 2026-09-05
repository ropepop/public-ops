package web

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"ticketremote/internal/phone"
)

type captureDemandCall struct {
	epoch      uint64
	generation uint64
}

func newCaptureDemandTestServer(t *testing.T) (*Server, chan captureDemandCall) {
	t.Helper()
	hub := newDirectStreamHub()
	if !hub.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":7,"phoneUptimeMillis":10000}`))) {
		t.Fatal("capture-demand test config was rejected")
	}
	calls := make(chan captureDemandCall, 16)
	var generation atomic.Uint64
	server := &Server{direct: hub, clients: map[*client]struct{}{}}
	server.captureDemandSend = func(ctx context.Context, epoch uint64) (phone.CaptureDemandReceipt, error) {
		value := generation.Add(1)
		select {
		case calls <- captureDemandCall{epoch: epoch, generation: value}:
		case <-ctx.Done():
			return phone.CaptureDemandReceipt{}, ctx.Err()
		}
		now := time.Now()
		return phone.CaptureDemandReceipt{
			StreamEpoch: epoch, Generation: value, ConnectionGeneration: 1,
			SentAt: now, ExpiresAt: now.Add(phone.CaptureDemandTTL),
		}, nil
	}
	t.Cleanup(server.closeOrdinaryCaptureDemand)
	return server, calls
}

func newCaptureDemandViewer(server *Server, epoch uint64, visibility string) *client {
	viewer := &client{
		videoEpoch: epoch, videoConfigGeneration: 1, videoFeedbackVersion: 2,
		videoV2Visibility: visibility,
	}
	viewer.onVideoConfigWritten = func(uint64, uint64) { server.requestOrdinaryCaptureIfUseful() }
	server.clients[viewer] = struct{}{}
	return viewer
}

func markCaptureDemandConfigWritten(viewer *client, epoch uint64, generation uint64) {
	viewer.noteVideoConfigWritten(queuedControlMessage{config: true, epoch: epoch, generation: generation})
}

func waitCaptureDemandCall(t *testing.T, calls <-chan captureDemandCall) captureDemandCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for capture demand")
		return captureDemandCall{}
	}
}

func requireNoCaptureDemandCall(t *testing.T, calls <-chan captureDemandCall) {
	t.Helper()
	select {
	case call := <-calls:
		t.Fatalf("unexpected capture demand: %#v", call)
	case <-time.After(30 * time.Millisecond):
	}
}

func waitCaptureDemandOutstanding(t *testing.T, server *Server, generation uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		server.captureDemandMu.Lock()
		got := server.captureDemandReceipt.Generation
		server.captureDemandMu.Unlock()
		if got == generation {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture demand generation %d never became outstanding; got %d", generation, got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestOrdinaryCaptureDemandRequiresSuccessfulConfigWriteAndCoalescesViewers(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	viewer := newCaptureDemandViewer(server, 7, "visible")
	server.requestOrdinaryCaptureIfUseful()
	requireNoCaptureDemandCall(t, calls)

	markCaptureDemandConfigWritten(viewer, 7, 1)
	first := waitCaptureDemandCall(t, calls)
	if first.epoch != 7 || first.generation != 1 {
		t.Fatalf("first capture demand = %#v", first)
	}
	waitCaptureDemandOutstanding(t, server, 1)
	for range 10 {
		server.requestOrdinaryCaptureIfUseful()
	}
	requireNoCaptureDemandCall(t, calls)
}

func TestOrdinaryCaptureDemandV2ReceiptReleasesNextAndV1DoesNot(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	viewer := newCaptureDemandViewer(server, 7, "visible")
	markCaptureDemandConfigWritten(viewer, 7, 1)
	_ = waitCaptureDemandCall(t, calls)
	waitCaptureDemandOutstanding(t, server, 1)
	server.completeOrdinaryCaptureOpportunity()
	viewer.noteVideoFrameWrittenAt(queuedTestFrame(7, 10, true, 1), time.Now())

	server.handleStreamFeedback(viewer, streamFeedbackFixture(7, 10, 10, 10, 10, 0, 10), time.Now())
	requireNoCaptureDemandCall(t, calls)
	server.handleStreamFeedback(viewer, streamFeedbackV2Fixture(7, 1, 10, 10, 10, 10, true), time.Now().Add(time.Millisecond))
	second := waitCaptureDemandCall(t, calls)
	if second.epoch != 7 || second.generation != 2 {
		t.Fatalf("receipt-triggered capture demand = %#v", second)
	}
}

func TestOrdinaryCaptureDemandFastViewerIsolatedFromSlowViewer(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	fast := newCaptureDemandViewer(server, 7, "visible")
	slow := newCaptureDemandViewer(server, 7, "visible")
	markCaptureDemandConfigWritten(fast, 7, 1)
	markCaptureDemandConfigWritten(slow, 7, 1)
	_ = waitCaptureDemandCall(t, calls)
	waitCaptureDemandOutstanding(t, server, 1)
	server.completeOrdinaryCaptureOpportunity()
	fast.noteVideoFrameWrittenAt(queuedTestFrame(7, 10, true, 1), time.Now())
	slow.noteVideoFrameWrittenAt(queuedTestFrame(7, 10, true, 1), time.Now())

	server.handleStreamFeedback(fast, streamFeedbackV2Fixture(7, 1, 10, 10, 10, 10, true), time.Now())
	second := waitCaptureDemandCall(t, calls)
	if second.generation != 2 {
		t.Fatalf("fast viewer did not drive aggregate demand: %#v", second)
	}
	slow.videoMu.Lock()
	slowAwaiting := slow.videoReceiptAwaiting
	slow.videoMu.Unlock()
	if !slowAwaiting {
		t.Fatal("slow viewer unexpectedly lost its independent receipt debt")
	}
}

func TestOrdinaryCaptureDemandPausesHiddenOnlyAndResumesOnV2Visible(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	viewer := newCaptureDemandViewer(server, 7, "hidden")
	markCaptureDemandConfigWritten(viewer, 7, 1)
	requireNoCaptureDemandCall(t, calls)

	visible := streamFeedbackV2Fixture(7, 1, 0, 0, 0, 0, false)
	server.handleStreamFeedback(viewer, visible, time.Now())
	if call := waitCaptureDemandCall(t, calls); call.epoch != 7 {
		t.Fatalf("visible return did not resume current epoch: %#v", call)
	}
}

func TestRejectedPhoneFrameCompletesAndRetriesOrdinaryCaptureOpportunity(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	viewer := newCaptureDemandViewer(server, 7, "visible")
	markCaptureDemandConfigWritten(viewer, 7, 1)
	_ = waitCaptureDemandCall(t, calls)
	waitCaptureDemandOutstanding(t, server, 1)

	server.handlePhoneMessage(phone.Message{Binary: []byte("malformed")})
	second := waitCaptureDemandCall(t, calls)
	if second.generation != 2 {
		t.Fatalf("rejected source result did not create one replacement opportunity: %#v", second)
	}
}

func TestOrdinaryCaptureDemandRetriesAfterExactNoBinaryExpiry(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	var generation atomic.Uint64
	server.captureDemandSend = func(ctx context.Context, epoch uint64) (phone.CaptureDemandReceipt, error) {
		value := generation.Add(1)
		select {
		case calls <- captureDemandCall{epoch: epoch, generation: value}:
		case <-ctx.Done():
			return phone.CaptureDemandReceipt{}, ctx.Err()
		}
		now := time.Now()
		return phone.CaptureDemandReceipt{
			StreamEpoch: epoch, Generation: value, ConnectionGeneration: 1,
			SentAt: now, ExpiresAt: now.Add(20 * time.Millisecond),
		}, nil
	}
	viewer := newCaptureDemandViewer(server, 7, "visible")
	markCaptureDemandConfigWritten(viewer, 7, 1)
	if first := waitCaptureDemandCall(t, calls); first.generation != 1 {
		t.Fatalf("first no-result demand = %#v", first)
	}
	if retry := waitCaptureDemandCall(t, calls); retry.generation != 2 || retry.epoch != 7 {
		t.Fatalf("TTL expiry did not issue one fenced retry: %#v", retry)
	}
}

func TestOrdinaryCaptureDemandEpochFenceClearsOldOpportunity(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	viewer := newCaptureDemandViewer(server, 7, "visible")
	markCaptureDemandConfigWritten(viewer, 7, 1)
	_ = waitCaptureDemandCall(t, calls)
	waitCaptureDemandOutstanding(t, server, 1)

	server.fenceOrdinaryCaptureDemand(8)
	if !server.direct.setConfig(testAllIntraConfig([]byte(`{"type":"config","streamEpoch":8,"phoneUptimeMillis":10001}`))) {
		t.Fatal("replacement capture-demand epoch was rejected")
	}
	viewer.videoMu.Lock()
	viewer.videoEpoch = 8
	viewer.videoConfigGeneration = 2
	viewer.videoConfigWrittenEpoch = 0
	viewer.videoConfigWrittenGen = 0
	viewer.videoMu.Unlock()
	markCaptureDemandConfigWritten(viewer, 8, 2)
	replacement := waitCaptureDemandCall(t, calls)
	if replacement.epoch != 8 || replacement.generation != 2 {
		t.Fatalf("new epoch did not fence old aggregate opportunity: %#v", replacement)
	}
}

func TestOrdinaryCaptureDemandPhoneReconnectFencesOldOpportunity(t *testing.T) {
	server, calls := newCaptureDemandTestServer(t)
	server.observeOrdinaryCaptureConnection(1)
	viewer := newCaptureDemandViewer(server, 7, "visible")
	markCaptureDemandConfigWritten(viewer, 7, 1)
	_ = waitCaptureDemandCall(t, calls)
	waitCaptureDemandOutstanding(t, server, 1)

	server.observeOrdinaryCaptureConnection(2)
	server.captureDemandMu.Lock()
	receipt := server.captureDemandReceipt
	connection := server.captureDemandConnection
	server.captureDemandMu.Unlock()
	if receipt.Generation != 0 || connection != 2 {
		t.Fatalf("replacement phone connection did not fence old demand: receipt=%#v connection=%d", receipt, connection)
	}
}
