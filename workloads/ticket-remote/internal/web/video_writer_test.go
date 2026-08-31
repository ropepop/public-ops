package web

import (
	"encoding/json"
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

func TestFrameWriteDeadlineRemainsShorterThanControlDeadline(t *testing.T) {
	if videoFrameWriteTimeout != 250*time.Millisecond {
		t.Fatalf("binary frame write timeout = %s, want 250ms", videoFrameWriteTimeout)
	}
	if streamControlWriteTimeout != 2*time.Second || videoFrameWriteTimeout >= streamControlWriteTimeout {
		t.Fatalf("write deadlines no longer isolate slow media: frame=%s control=%s", videoFrameWriteTimeout, streamControlWriteTimeout)
	}
}
