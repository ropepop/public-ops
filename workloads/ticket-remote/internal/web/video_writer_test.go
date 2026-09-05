package web

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func streamFeedbackFixture(epoch, received, decoded, rendered, renderedKeyframe uint64, queue, age int) []byte {
	payload, _ := json.Marshal(streamFeedback{
		Type: "stream_feedback", Version: 1, Epoch: epoch,
		ReceivedSequence: received, DecodedSequence: decoded, RenderedSequence: rendered,
		RenderedKeyframeSequence: renderedKeyframe, DecoderQueueSize: int64(queue),
		RenderedVisualAgeMillis: int64(age), Visibility: "visible",
	})
	return payload
}

func streamFeedbackV2Fixture(epoch, generation, received, decoded, rendered, presented uint64, ageKnown bool) []byte {
	payload, _ := json.Marshal(streamFeedback{
		Type: "stream_feedback", Version: 2, Epoch: epoch, ConfigGeneration: generation,
		ReceivedSequence: received, DecodedSequence: decoded, RenderedSequence: rendered,
		PresentedSequence: presented, RenderedKeyframeSequence: rendered,
		RenderedVisualAgeMillis: 10, AgeKnown: ageKnown, Visibility: "visible",
	})
	return payload
}

func resultPriorityFixture(phase string, generation, epoch, minSequence uint64) []byte {
	payload, _ := json.Marshal(resultPriority{
		Type: "result_priority", Version: 1, Phase: phase,
		ConfigGeneration: generation, Epoch: epoch, MinSequence: minSequence,
	})
	return payload
}

func queuedTestFrame(epoch, sequence uint64, keyFrame bool, configGeneration uint64) queuedVideoFrame {
	data := testTSF2FrameWithTimestamp(epoch, sequence, keyFrame, sequence*1000)
	return queuedVideoFrame{
		data: data, meta: parseTSF2(data), queuedAt: time.Now(), configGeneration: configGeneration,
	}
}

func noteTestFrameWritten(c *client, epoch, sequence uint64, writtenAt time.Time) {
	c.videoMu.Lock()
	generation := c.videoConfigGeneration
	c.videoMu.Unlock()
	frame := queuedTestFrame(epoch, sequence, true, generation)
	c.noteVideoFrameWrittenAt(frame, writtenAt)
}

func noteTestKeyframeWritten(c *client, epoch, sequence uint64, writtenAt time.Time) {
	noteTestFrameWritten(c, epoch, sequence, writtenAt)
}

func TestStreamFeedbackIsStrictBoundedAndAdvisoryOnly(t *testing.T) {
	viewer := &client{videoEpoch: 7}
	now := time.Unix(1_700_000_000, 0)
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 10, 10, 10, 10, 99, 99_000), now) {
		t.Fatal("valid cumulative feedback was rejected")
	}
	if viewer.lastFeedbackQueue != feedbackMaxQueueSize || viewer.lastFeedbackAge != feedbackMaxAgeMillis {
		t.Fatalf("feedback was not clamped: queue=%d age=%d", viewer.lastFeedbackQueue, viewer.lastFeedbackAge)
	}
	if viewer.acceptStreamFeedback(streamFeedbackFixture(7, 11, 11, 11, 11, 0, 10), now.Add(100*time.Millisecond)) {
		t.Fatal("feedback faster than the four-per-second boundary was accepted")
	}
	if viewer.acceptStreamFeedback(streamFeedbackFixture(8, 11, 11, 11, 11, 0, 10), now.Add(time.Second)) {
		t.Fatal("wrong-epoch feedback was accepted")
	}
	if viewer.acceptStreamFeedback(streamFeedbackFixture(7, 9, 11, 11, 11, 0, 10), now.Add(2*time.Second)) {
		t.Fatal("backwards primary cumulative sequence was accepted")
	}
	// Rolling browsers may report the historical field differently. It remains
	// schema-compatible but cannot control independent all-intra delivery.
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 11, 11, 11, 1, 0, 10), now.Add(3*time.Second)) {
		t.Fatal("rolling renderedKeyframeSequence compatibility was lost")
	}
	if viewer.acceptStreamFeedback([]byte(`{"type":"stream_feedback","version":1,"epoch":7,"receivedSequence":12,"decodedSequence":12,"renderedSequence":12,"renderedKeyframeSequence":12,"decoderQueueSize":0,"renderedVisualAgeMillis":1,"unknown":true}`), now.Add(4*time.Second)) {
		t.Fatal("unknown feedback field was accepted")
	}
	valid := streamFeedbackFixture(7, 12, 12, 12, 12, 0, 1)
	if viewer.acceptStreamFeedback(append(valid, []byte(` {}`)...), now.Add(5*time.Second)) {
		t.Fatal("trailing feedback JSON was accepted")
	}
	if len(viewer.videoQueue) != 0 {
		t.Fatal("advisory feedback changed media delivery")
	}
}

func TestVideoWriterKeepsOnlyNewestIndependentFrame(t *testing.T) {
	viewer := &client{videoEpoch: 7}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 10, true, 10_000))
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 40, true, 40_000))
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 41, false, 41_000))
	viewer.videoMu.Lock()
	if len(viewer.videoQueue) != 1 || viewer.videoQueue[0].meta.sequence != 40 || !viewer.videoQueue[0].meta.keyFrame {
		viewer.videoMu.Unlock()
		t.Fatalf("pending queue is not newest independent frame: %#v", viewer.videoQueue)
	}
	viewer.videoMu.Unlock()
	item, ok := viewer.nextVideoWriteItem()
	if !ok || item.frame == nil || item.frame.meta.sequence != 40 {
		t.Fatalf("popped frame = %#v, want independent sequence 40", item)
	}
	viewer.noteVideoFrameWritten(*item.frame)
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 20, true, 20_000))
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if len(viewer.videoQueue) != 0 {
		t.Fatal("older frame regressed the successfully written sequence")
	}
}

func TestConfigPrecedesWarmFrameAndFencesStaleCompletion(t *testing.T) {
	viewer := &client{}
	warmConfig := []byte(`{"type":"config","streamEpoch":7}`)
	warmFrame := testTSF2FrameWithTimestamp(7, 10, true, 10_000)
	configAccepted, frameAccepted := viewer.enqueuePhoneConfig(warmConfig, warmFrame)
	if !configAccepted || !frameAccepted {
		t.Fatalf("warm admission config=%t frame=%t", configAccepted, frameAccepted)
	}
	configItem, ok := viewer.nextVideoWriteItem()
	if !ok || configItem.frame != nil || configItem.messageType == 0 {
		t.Fatalf("first item = %#v, want config", configItem)
	}
	warmItem, ok := viewer.nextVideoWriteItem()
	if !ok || warmItem.frame == nil || warmItem.frame.meta.sequence != 10 {
		t.Fatalf("second item = %#v, want warm frame", warmItem)
	}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":8}`))
	viewer.noteVideoFrameWritten(*warmItem.frame)
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoEpoch != 8 || viewer.videoWrittenEpoch != 0 || viewer.videoWrittenSequence != 0 || len(viewer.videoWrittenEvidence) != 0 {
		t.Fatalf("stale completion crossed config fence: epoch=%d written=%d/%d evidence=%d", viewer.videoEpoch, viewer.videoWrittenEpoch, viewer.videoWrittenSequence, len(viewer.videoWrittenEvidence))
	}
}

func TestStaleWarmSnapshotCannotReplaceLiveConfig(t *testing.T) {
	viewer := &client{}
	expected := viewer.videoConfigGenerationSnapshot()
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":2}`))
	config, frame, stale := viewer.enqueueWarmStart(
		[]byte(`{"type":"config","streamEpoch":1}`),
		testTSF2FrameWithTimestamp(1, 10, true, 10_000), expected,
	)
	if config || frame || !stale {
		t.Fatalf("stale warm result config=%t frame=%t stale=%t", config, frame, stale)
	}
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoEpoch != 2 || len(viewer.controlQueue) != 1 || viewer.controlQueue[0].epoch != 2 || len(viewer.videoQueue) != 0 {
		t.Fatalf("live config was replaced: epoch=%d controls=%#v frames=%#v", viewer.videoEpoch, viewer.controlQueue, viewer.videoQueue)
	}
}

func TestSuccessfulWriteEvidenceSurvivesNewerFrames(t *testing.T) {
	viewer := &client{videoEpoch: 7}
	now := time.Unix(1_700_000_000, 0)
	noteTestFrameWritten(viewer, 7, 10, now)
	noteTestFrameWritten(viewer, 7, 40, now.Add(time.Millisecond))
	for _, sequence := range []float64{10, 40} {
		if !viewer.browserFrameMarkerMatchesSuccessfulWrite(map[string]any{"frameEpoch": float64(7), "frameSequence": sequence}) {
			t.Fatalf("successful independent frame %.0f lost write evidence", sequence)
		}
	}
	if viewer.browserFrameMarkerMatchesSuccessfulWrite(map[string]any{"frameEpoch": float64(7), "frameSequence": float64(20)}) {
		t.Fatal("unwritten sequence was accepted")
	}
}

func TestSourceFreshnessAndStopClearPendingFrame(t *testing.T) {
	viewer := &client{}
	viewer.videoQueue = []queuedVideoFrame{{
		meta: tsf2Metadata{ok: true, keyFrame: true, epoch: 1, sequence: 1},
		data: []byte{1}, queuedAt: time.Now().Add(-100 * time.Millisecond), visualAge: 1200 * time.Millisecond,
	}}
	viewer.videoQueueBytes = 1
	if item, ok := viewer.nextVideoWriteItem(); ok || item.frame != nil {
		t.Fatalf("expired frame was emitted: %#v", item)
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(1, 2, true, 2_000))
	viewer.stopVideoWriter()
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if len(viewer.videoQueue) != 0 || viewer.videoQueueBytes != 0 || viewer.videoInFlight {
		t.Fatalf("stop retained media: frames=%d bytes=%d inFlight=%t", len(viewer.videoQueue), viewer.videoQueueBytes, viewer.videoInFlight)
	}
}

func TestPerViewerPendingFramesStayIsolated(t *testing.T) {
	slow := &client{videoEpoch: 7}
	fast := &client{videoEpoch: 7}
	slow.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 10, true, 10_000))
	fast.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 10, true, 10_000))
	if item, ok := slow.nextVideoWriteItem(); !ok || item.frame == nil {
		t.Fatal("slow viewer did not start its independent write")
	}
	slow.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 30, true, 30_000))
	fast.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 40, true, 40_000))
	slow.videoMu.Lock()
	slowSequence := slow.videoQueue[0].meta.sequence
	slow.videoMu.Unlock()
	fast.videoMu.Lock()
	fastSequence := fast.videoQueue[0].meta.sequence
	fast.videoMu.Unlock()
	if slowSequence != 30 || fastSequence != 40 {
		t.Fatalf("viewer queues interfered: slow=%d fast=%d", slowSequence, fastSequence)
	}
}

func TestControlQueueRemainsBoundedAndPreservesConfig(t *testing.T) {
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	for i := 0; i < controlQueueMaxMessages*3; i++ {
		viewer.enqueueControl([]byte(`{"type":"status"}`))
	}
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if len(viewer.controlQueue) > controlQueueMaxMessages || viewer.controlQueueBytes > controlQueueMaxBytes {
		t.Fatalf("control queue exceeded bounds: messages=%d bytes=%d", len(viewer.controlQueue), viewer.controlQueueBytes)
	}
	foundConfig := false
	for _, item := range viewer.controlQueue {
		foundConfig = foundConfig || item.config
	}
	if !foundConfig {
		t.Fatal("bounded pressure evicted the decoder config")
	}
}

func TestFrameWriteDeadlineUsesRemainingAbsoluteFreshnessBudget(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	frame := queuedVideoFrame{queuedAt: now.Add(-100 * time.Millisecond), visualAge: 200 * time.Millisecond}
	want := now.Add(liveFreshMaxAge - 300*time.Millisecond)
	if got := videoFrameWriteDeadline(frame, now); !got.Equal(want) {
		t.Fatalf("frame write deadline = %s, want absolute source deadline %s", got, want)
	}
	if remaining := videoFrameWriteDeadline(frame, now).Sub(now); remaining <= 250*time.Millisecond {
		t.Fatalf("feasible slow link retained obsolete 250ms cutoff: %s", remaining)
	}
	futureQueued := queuedVideoFrame{queuedAt: now.Add(time.Hour)}
	if got := videoFrameWriteDeadline(futureQueued, now); !got.Equal(now.Add(liveFreshMaxAge)) {
		t.Fatalf("frame write deadline exceeded bounded freshness window: %s", got)
	}
}

func TestSuccessfulWriteWinsDeadlineRace(t *testing.T) {
	if got := videoWriteFailureReason(nil, true); got != "" {
		t.Fatalf("nil WebSocket write error became %q after its deadline raced", got)
	}
	if got := videoWriteFailureReason(errors.New("write failed"), false); got != "write_failed" {
		t.Fatalf("ordinary write failure reason = %q", got)
	}
	if got := videoWriteFailureReason(errors.New("deadline exceeded"), true); got != "write_timeout" {
		t.Fatalf("timed-out write failure reason = %q", got)
	}
}

func TestFeedbackV2ConfigAndReceiptCreditGateExactlyOneFrame(t *testing.T) {
	viewer := &client{}
	config := []byte(`{"type":"config","streamEpoch":7}`)
	frame := testTSF2FrameWithTimestamp(7, 10, true, 10_000)
	configAccepted, frameAccepted := viewer.enqueuePhoneConfig(config, frame)
	if !configAccepted || !frameAccepted {
		t.Fatalf("config/warm frame admission = %t/%t", configAccepted, frameAccepted)
	}
	configItem, ok := viewer.nextVideoWriteItem()
	if !ok || configItem.frame != nil {
		t.Fatalf("first writer item is not config: %#v", configItem)
	}
	var advertised map[string]any
	if err := json.Unmarshal(configItem.data, &advertised); err != nil || advertised["feedbackVersion"] != float64(2) || advertised["feedbackConfigGeneration"] != float64(1) {
		t.Fatalf("config did not advertise generation-fenced v2 feedback: %s", configItem.data)
	}
	warmItem, ok := viewer.nextVideoWriteItem()
	if !ok || warmItem.frame == nil || warmItem.frame.meta.sequence != 10 {
		t.Fatalf("first credit did not allow one frame: %#v", warmItem)
	}
	now := time.Now()
	viewer.noteVideoFrameWrittenAt(*warmItem.frame, now)
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 20, true, 20_000))
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 30, true, 30_000))
	if item, ok := viewer.nextVideoWriteItem(); ok || item.frame != nil {
		t.Fatalf("second frame escaped before receipt credit: %#v", item)
	}

	// v1 remains diagnostic-only even when it names the outstanding sequence.
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 999, 999, 999, 999, 0, 10), now) {
		t.Fatal("compatible v1 diagnostics were rejected")
	}
	if item, ok := viewer.nextVideoWriteItem(); ok || item.frame != nil {
		t.Fatalf("v1 diagnostic released delivery credit: %#v", item)
	}
	if viewer.acceptStreamFeedback(streamFeedbackV2Fixture(7, 2, 10, 10, 10, 10, true), now.Add(time.Millisecond)) {
		t.Fatal("wrong config generation released delivery credit")
	}
	// The exact receipt ACK must release before the 250ms telemetry rate limit.
	if !viewer.acceptStreamFeedback(streamFeedbackV2Fixture(7, 1, 10, 10, 10, 10, true), now.Add(2*time.Millisecond)) {
		t.Fatal("exact receipt ACK was lost to the diagnostic rate limit")
	}
	viewer.videoMu.Lock()
	protocolReceived := viewer.videoV2FeedbackReceived
	viewer.videoMu.Unlock()
	if protocolReceived != 10 {
		t.Fatalf("rate-limited receipt did not advance v2 protocol fence: %d", protocolReceived)
	}
	pending, ok := viewer.nextVideoWriteItem()
	if !ok || pending.frame == nil || pending.frame.meta.sequence != 30 {
		t.Fatalf("newest pending frame was not released after exact receipt: %#v", pending)
	}
}

func TestFeedbackV2AcceptsOnlyExactInFlightReceiptBeforeWriteBookkeeping(t *testing.T) {
	viewer := &client{}
	config := []byte(`{"type":"config","streamEpoch":7}`)
	frame := testTSF2FrameWithTimestamp(7, 10, true, 10_000)
	configAccepted, frameAccepted := viewer.enqueuePhoneConfig(config, frame)
	if !configAccepted || !frameAccepted {
		t.Fatalf("config/warm frame admission = %t/%t", configAccepted, frameAccepted)
	}
	configItem, ok := viewer.nextVideoWriteItem()
	if !ok || configItem.control == nil {
		t.Fatalf("first writer item is not config: %#v", configItem)
	}
	viewer.noteVideoConfigWritten(*configItem.control)
	warmItem, ok := viewer.nextVideoWriteItem()
	if !ok || warmItem.frame == nil || warmItem.frame.meta.sequence != 10 {
		t.Fatalf("warm frame did not enter the write boundary: %#v", warmItem)
	}

	// Simulate the browser reader racing the writer bookkeeping after the
	// underlying socket write has completed. Only this exact in-flight identity
	// is a possible early receipt.
	now := time.Now()
	outcome := viewer.acceptStreamFeedbackOutcome(
		streamFeedbackV2Fixture(7, 1, 10, 10, 10, 10, true), now,
	)
	if !outcome.accepted || !outcome.receiptReleased {
		t.Fatalf("exact early receipt was rejected: %#v", outcome)
	}
	if !viewer.canUseOrdinaryCapture(7) {
		t.Fatal("exact early receipt did not restore capture credit")
	}
	if viewer.acceptStreamFeedback(
		streamFeedbackV2Fixture(7, 1, 11, 11, 11, 11, true), now.Add(time.Second),
	) {
		t.Fatal("future receipt beyond the exact in-flight frame was accepted")
	}

	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 20, true, 20_000))
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 30, true, 30_000))
	viewer.videoMu.Lock()
	if !viewer.videoInFlight || len(viewer.videoQueue) != 1 || viewer.videoQueue[0].meta.sequence != 30 {
		viewer.videoMu.Unlock()
		t.Fatalf("early receipt broke one-in-flight plus newest-pending bounds: inFlight=%t queue=%#v", viewer.videoInFlight, viewer.videoQueue)
	}
	viewer.videoMu.Unlock()

	viewer.noteVideoFrameWrittenAt(*warmItem.frame, now.Add(2*time.Millisecond))
	viewer.videoMu.Lock()
	awaiting := viewer.videoReceiptAwaiting
	written := viewer.videoWrittenSequence
	viewer.videoMu.Unlock()
	if awaiting || written != 10 {
		t.Fatalf("writer armed debt after an early receipt: awaiting=%t written=%d", awaiting, written)
	}
	pending, ok := viewer.nextVideoWriteItem()
	if !ok || pending.frame == nil || pending.frame.meta.sequence != 30 {
		t.Fatalf("newest pending frame was not released after bookkeeping: %#v", pending)
	}
}

func TestFeedbackV2RejectsIllogicalPresentationWithoutReleasingCredit(t *testing.T) {
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	_, _ = viewer.nextVideoWriteItem()
	frame := queuedTestFrame(7, 10, true, viewer.videoConfigGenerationSnapshot())
	viewer.noteVideoFrameWrittenAt(frame, time.Now())
	invalid := streamFeedbackV2Fixture(7, 1, 10, 10, 9, 10, true)
	if viewer.acceptStreamFeedback(invalid, time.Now()) {
		t.Fatal("presented sequence ahead of rendered was accepted")
	}
	viewer.videoMu.Lock()
	awaiting := viewer.videoReceiptAwaiting
	viewer.videoMu.Unlock()
	if !awaiting {
		t.Fatal("invalid feedback released receipt credit")
	}
}

func TestFeedbackV2ReceiptUsesIndependentLivenessTimeout(t *testing.T) {
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	_, _ = viewer.nextVideoWriteItem()
	frame := queuedTestFrame(7, 10, true, viewer.videoConfigGenerationSnapshot())
	writtenAt := time.Now()
	frame.queuedAt = writtenAt.Add(-1200 * time.Millisecond)
	viewer.noteVideoFrameWrittenAt(frame, writtenAt)
	viewer.videoMu.Lock()
	receiptDeadline := viewer.videoReceiptDeadlineAt
	viewer.videoMu.Unlock()
	if want := writtenAt.Add(videoReceiptLivenessTimeout); !receiptDeadline.Equal(want) {
		t.Fatalf("receipt deadline = %s, want independent liveness deadline %s", receiptDeadline, want)
	}
	// The complete-message receipt remains useful as transport credit after the
	// picture's source freshness has elapsed. It does not grant presentation
	// authority; that remains a browser-side age decision.
	ackAt := writtenAt.Add(150 * time.Millisecond)
	if outcome := viewer.acceptStreamFeedbackOutcome(
		streamFeedbackV2Fixture(7, 1, 10, 0, 0, 0, true), ackAt,
	); !outcome.accepted || !outcome.receiptReleased {
		t.Fatalf("late-but-live receipt did not restore credit: %#v", outcome)
	}
	viewer.videoMu.Lock()
	closedAfterReceipt := viewer.writerClosed
	awaitingAfterReceipt := viewer.videoReceiptAwaiting
	viewer.videoMu.Unlock()
	if closedAfterReceipt || awaitingAfterReceipt {
		t.Fatalf("valid receipt left viewer closed or indebted: closed=%t awaiting=%t", closedAfterReceipt, awaitingAfterReceipt)
	}

	viewer.noteVideoFrameWrittenAt(queuedTestFrame(7, 20, true, viewer.videoConfigGenerationSnapshot()), writtenAt)
	viewer.closeExpiredVideoReceipt()
	viewer.videoMu.Lock()
	closedBeforeLivenessDeadline := viewer.writerClosed
	awaitingBeforeLivenessDeadline := viewer.videoReceiptAwaiting
	viewer.videoMu.Unlock()
	if closedBeforeLivenessDeadline || !awaitingBeforeLivenessDeadline {
		t.Fatalf("source expiry closed receipt debt before its liveness deadline: closed=%t awaiting=%t", closedBeforeLivenessDeadline, awaitingBeforeLivenessDeadline)
	}
	viewer.videoMu.Lock()
	viewer.videoReceiptDeadlineAt = time.Now().Add(-time.Millisecond)
	viewer.videoQueue = []queuedVideoFrame{queuedTestFrame(7, 30, true, viewer.videoConfigGeneration)}
	viewer.videoQueueBytes = len(viewer.videoQueue[0].data)
	viewer.videoMu.Unlock()
	viewer.closeExpiredVideoReceipt()
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if !viewer.writerClosed || viewer.writerCloseReason != "receipt_timeout" || viewer.videoReceiptAwaiting || len(viewer.videoQueue) != 0 {
		t.Fatalf("expired receipt did not close and clear viewer: closed=%t reason=%q awaiting=%t queue=%d", viewer.writerClosed, viewer.writerCloseReason, viewer.videoReceiptAwaiting, len(viewer.videoQueue))
	}
}

func TestReceiptAckWinningDeadlineRaceKeepsWriterRunnable(t *testing.T) {
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	config, ok := viewer.nextVideoWriteItem()
	if !ok || config.control == nil {
		t.Fatalf("configuration did not reach writer: %#v", config)
	}
	viewer.noteVideoConfigWritten(*config.control)
	frame := queuedTestFrame(7, 10, true, viewer.videoConfigGenerationSnapshot())
	viewer.noteVideoFrameWrittenAt(frame, time.Now())
	outcome := viewer.acceptStreamFeedbackOutcome(
		streamFeedbackV2Fixture(7, 1, 10, 0, 0, 0, true), time.Now(),
	)
	if !outcome.receiptReleased {
		t.Fatalf("exact receipt did not win simulated deadline race: %#v", outcome)
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 20, true, 20_000))
	if viewer.closeExpiredVideoReceipt() {
		t.Fatal("deadline path closed after the exact receipt had already cleared its debt")
	}
	if !viewer.videoWriterHasRunnableWork() {
		t.Fatal("ACK-winning race left the open writer with no runnable next frame")
	}
	next, ok := viewer.nextVideoWriteItem()
	if !ok || next.frame == nil || next.frame.meta.sequence != 20 {
		t.Fatalf("ACK-winning race stranded the newest pending picture: %#v", next)
	}
}

func TestNewerProofFrameCanReplacePendingOrdinaryFrame(t *testing.T) {
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	_, _ = viewer.nextVideoWriteItem()
	viewer.noteVideoFrameWrittenAt(queuedTestFrame(7, 10, true, 1), time.Now())
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 20, true, 20_000))
	// Proof priority is not encoded on TSF2/TSF3 today. Its later independent
	// sequence still replaces the sole pending ordinary frame without disturbing
	// the already-written receipt debt.
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 30, true, 30_000))
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if len(viewer.videoQueue) != 1 || viewer.videoQueue[0].meta.sequence != 30 ||
		!viewer.videoReceiptAwaiting || viewer.videoReceiptSequence != 10 {
		t.Fatalf("newer proof candidate did not replace pending ordinary frame: queue=%#v debt=%d", viewer.videoQueue, viewer.videoReceiptSequence)
	}
}

func TestResultPriorityUsesOneViewerLocalSlotAndExactMarker(t *testing.T) {
	now := time.Now()
	viewer := &client{
		videoEpoch: 7, videoConfigGeneration: 2, videoFeedbackVersion: 2,
		videoConfigWrittenEpoch: 7, videoConfigWrittenGen: 2,
	}
	other := &client{
		videoEpoch: 7, videoConfigGeneration: 2, videoFeedbackVersion: 2,
		videoConfigWrittenEpoch: 7, videoConfigWrittenGen: 2,
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 10, true, 10_000))
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityArm, 2, 7, 0), now) {
		t.Fatal("current requester-local arm was rejected")
	}
	viewer.videoMu.Lock()
	activeAfterFiveSeconds := viewer.resultPriorityActiveLocked(now.Add(6 * time.Second))
	armDeadline := viewer.videoResultPriorityUntil
	viewer.videoMu.Unlock()
	if !activeAfterFiveSeconds || !armDeadline.Equal(now.Add(resultPriorityWorkflowWindow)) {
		t.Fatalf("workflow reservation expired before a long-running request could produce a marker: active=%t deadline=%s", activeAfterFiveSeconds, armDeadline)
	}
	if viewer.canUseOrdinaryCapture(7) {
		t.Fatal("armed result viewer retained ordinary capture demand")
	}
	if !other.canUseOrdinaryCapture(7) {
		t.Fatal("one viewer's result priority changed another viewer's credit")
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, true, 11_000))
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 12, true, 12_000))
	viewer.videoMu.Lock()
	queued := len(viewer.videoQueue)
	queuedSequence := uint64(0)
	if queued == 1 {
		queuedSequence = viewer.videoQueue[0].meta.sequence
	}
	viewer.videoMu.Unlock()
	if queued != 1 || queuedSequence != 12 {
		t.Fatalf("arm did not retain only the newest candidate: count=%d sequence=%d", queued, queuedSequence)
	}
	if item, ok := viewer.nextVideoWriteItem(); ok || item.frame != nil {
		t.Fatalf("frame escaped before the exact result marker: %#v", item)
	}
	markerAt := now.Add(6 * time.Second)
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 12), markerAt) {
		t.Fatal("current exact result marker was rejected")
	}
	viewer.videoMu.Lock()
	markDeadline := viewer.videoResultPriorityUntil
	viewer.videoMu.Unlock()
	if !markDeadline.Equal(markerAt.Add(resultPriorityDeliveryWindow)) {
		t.Fatalf("exact-result delivery budget did not begin at marker: deadline=%s", markDeadline)
	}
	item, ok := viewer.nextVideoWriteItem()
	if !ok || item.frame == nil || item.frame.meta.sequence != 12 {
		t.Fatalf("exact marked result candidate was not released: %#v", item)
	}
}

func TestResultPriorityIsStrictGenerationFencedAndPrivacyFree(t *testing.T) {
	viewer := &client{videoEpoch: 7, videoConfigGeneration: 2, videoFeedbackVersion: 2}
	now := time.Now()
	invalid := [][]byte{
		resultPriorityFixture(resultPriorityArm, 1, 7, 0),
		resultPriorityFixture(resultPriorityArm, 2, 8, 0),
		resultPriorityFixture(resultPriorityMark, 2, 7, 0),
		[]byte(`{"type":"result_priority","version":1,"phase":"arm","configGeneration":2,"epoch":7,"requestId":"private"}`),
		[]byte(`{"type":"result_priority","version":1,"phase":"arm","configGeneration":2,"epoch":7} {}`),
	}
	for _, payload := range invalid {
		if viewer.acceptResultPriority(payload, now) {
			t.Fatalf("invalid or private result-priority payload was accepted: %s", payload)
		}
	}
	if viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 20), now) {
		t.Fatal("marker without an active arm was accepted")
	}
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityArm, 2, 7, 0), now) {
		t.Fatal("valid result-priority arm was rejected")
	}
	viewer.videoMu.Lock()
	armDeadline := viewer.videoResultPriorityUntil
	priorityTimer := viewer.videoResultPriorityTimer
	viewer.videoMu.Unlock()
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityArm, 2, 7, 0), now.Add(time.Second)) {
		t.Fatal("identical arm retransmission was rejected")
	}
	viewer.videoMu.Lock()
	if !viewer.videoResultPriorityUntil.Equal(armDeadline) || viewer.videoResultPriorityTimer != priorityTimer {
		viewer.videoMu.Unlock()
		t.Fatal("identical arm extended its bounded deadline or allocated another timer")
	}
	viewer.videoMu.Unlock()
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 20), now.Add(2*time.Second)) {
		t.Fatal("marker after active arm was rejected")
	}
	viewer.videoMu.Lock()
	markDeadline := viewer.videoResultPriorityUntil
	viewer.videoMu.Unlock()
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 20), now.Add(3*time.Second)) {
		t.Fatal("identical marker retransmission was rejected")
	}
	if viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 21), now.Add(3*time.Second)) {
		t.Fatal("conflicting marker changed an active reservation")
	}
	if viewer.acceptResultPriority(resultPriorityFixture(resultPriorityArm, 2, 7, 0), now.Add(3*time.Second)) {
		t.Fatal("active marked reservation was restarted")
	}
	viewer.videoMu.Lock()
	if viewer.videoResultPrioritySeq != 20 || !viewer.videoResultPriorityUntil.Equal(markDeadline) ||
		viewer.videoResultPriorityTimer != priorityTimer {
		viewer.videoMu.Unlock()
		t.Fatal("duplicate or conflicting input changed marked reservation")
	}
	viewer.videoMu.Unlock()
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":8}`))
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoResultPriorityPhase != "" || viewer.videoResultPriorityEpoch != 0 ||
		viewer.videoResultPriorityGen != 0 || !viewer.videoResultPriorityUntil.IsZero() {
		t.Fatal("new decoder config did not fence result priority")
	}
}

func TestResultPriorityClearsOnExactPresentationAndBoundedExpiry(t *testing.T) {
	viewer := &client{videoEpoch: 7, videoConfigGeneration: 2, videoFeedbackVersion: 2}
	now := time.Now()
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityArm, 2, 7, 0), now) {
		t.Fatal("valid result arm was rejected")
	}
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 20), now) {
		t.Fatal("valid result marker was rejected")
	}
	viewer.videoMu.Lock()
	viewer.videoWrittenSequence = 20
	viewer.videoMu.Unlock()
	if !viewer.acceptStreamFeedback(streamFeedbackV2Fixture(7, 2, 20, 20, 20, 20, true), now.Add(time.Millisecond)) {
		t.Fatal("exact presentation feedback was rejected")
	}
	viewer.videoMu.Lock()
	if viewer.videoResultPriorityPhase != "" {
		viewer.videoMu.Unlock()
		t.Fatal("exact PresentedSequence did not clear result priority")
	}
	viewer.videoResultPriorityPhase = resultPriorityArm
	viewer.videoResultPriorityEpoch = 7
	viewer.videoResultPriorityGen = 2
	viewer.videoResultPriorityUntil = now.Add(-time.Millisecond)
	active := viewer.resultPriorityActiveLocked(now)
	cleared := viewer.videoResultPriorityPhase == "" && viewer.videoResultPriorityUntil.IsZero()
	viewer.videoMu.Unlock()
	if active || !cleared {
		t.Fatal("expired requester-local attempt remained active")
	}
}

func TestMarkedResultPriorityExpiryClosesOnlyThatViewer(t *testing.T) {
	viewer := &client{videoEpoch: 7, videoConfigGeneration: 2, videoFeedbackVersion: 2}
	other := &client{
		videoEpoch: 7, videoConfigGeneration: 2, videoFeedbackVersion: 2,
		videoConfigWrittenEpoch: 7, videoConfigWrittenGen: 2,
	}
	now := time.Now()
	if !viewer.acceptResultPriority(resultPriorityFixture(resultPriorityArm, 2, 7, 0), now) ||
		!viewer.acceptResultPriority(resultPriorityFixture(resultPriorityMark, 2, 7, 20), now) {
		t.Fatal("could not establish marked result reservation")
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 20, true, 20_000))
	viewer.videoMu.Lock()
	viewer.videoResultPriorityUntil = time.Now().Add(-time.Millisecond)
	viewer.videoMu.Unlock()
	viewer.expireResultPriority()

	viewer.videoMu.Lock()
	closed := viewer.writerClosed
	reason := viewer.writerCloseReason
	queued := len(viewer.videoQueue)
	priority := viewer.videoResultPriorityPhase
	viewer.videoMu.Unlock()
	if !closed || reason != "result_priority_timeout" || queued != 0 || priority != "" {
		t.Fatalf("expired marked result did not close and clear its viewer: closed=%t reason=%q queued=%d priority=%q", closed, reason, queued, priority)
	}
	other.videoMu.Lock()
	otherClosed := other.writerClosed
	other.videoMu.Unlock()
	if otherClosed || !other.canUseOrdinaryCapture(7) {
		t.Fatal("one result timeout changed another viewer")
	}
}

func TestBrowserClockProbeResponseIsGenerationFencedStrictAndRateLimited(t *testing.T) {
	viewer := &client{}
	viewer.enqueueControl([]byte(`{"type":"config","streamEpoch":7}`))
	_, _ = viewer.nextVideoWriteItem()
	receivedAt := time.Now()
	request := []byte(`{"type":"clock_probe","version":1,"probeId":"browser-1","configGeneration":1,"clientSendUnixMicros":1700000000000000}`)
	response, ok := viewer.browserClockProbeResponse(request, receivedAt)
	if !ok {
		t.Fatal("valid browser clock probe was rejected")
	}
	var result map[string]any
	if err := json.Unmarshal(response, &result); err != nil || result["type"] != "clock_probe_result" ||
		result["probeId"] != "browser-1" || result["configGeneration"] != float64(1) ||
		result["clientSendUnixMicros"] != float64(1_700_000_000_000_000) ||
		result["serverReceiveUnixMicros"] != float64(receivedAt.UnixMicro()) ||
		result["serverSendUnixMicros"].(float64) < result["serverReceiveUnixMicros"].(float64) {
		t.Fatalf("invalid browser clock probe response: %s", response)
	}
	if _, ok := viewer.browserClockProbeResponse(request, receivedAt.Add(time.Millisecond)); ok {
		t.Fatal("browser clock probe rate limit was bypassed")
	}
	wrongGeneration := []byte(`{"type":"clock_probe","version":1,"probeId":"browser-2","configGeneration":2,"clientSendUnixMicros":1700000000000000}`)
	if _, ok := viewer.browserClockProbeResponse(wrongGeneration, receivedAt.Add(time.Second)); ok {
		t.Fatal("wrong-generation browser clock probe was accepted")
	}
	unsafeID := []byte(`{"type":"clock_probe","version":1,"probeId":"bad/id","configGeneration":1,"clientSendUnixMicros":1700000000000000}`)
	if _, ok := viewer.browserClockProbeResponse(unsafeID, receivedAt.Add(2*time.Second)); ok {
		t.Fatal("unsafe browser clock probe id was accepted")
	}
}
