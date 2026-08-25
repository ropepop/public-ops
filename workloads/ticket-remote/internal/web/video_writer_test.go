package web

import (
	"sync/atomic"
	"testing"
	"time"
)

func streamFeedbackFixture(epoch, received, decoded, rendered, keyframe uint64, queue, age int) []byte {
	return []byte(`{"type":"stream_feedback","version":1,"epoch":` +
		itoaUint(epoch) + `,"receivedSequence":` + itoaUint(received) +
		`,"decodedSequence":` + itoaUint(decoded) + `,"renderedSequence":` + itoaUint(rendered) +
		`,"renderedKeyframeSequence":` + itoaUint(keyframe) + `,"decoderQueueSize":` +
		itoaInt(queue) + `,"renderedVisualAgeMillis":` + itoaInt(age) + `,"visibility":"visible"}`)
}

func noteTestFrameWritten(c *client, epoch, sequence uint64, keyFrame bool, writtenAt time.Time) {
	frame := testTSF2FrameWithTimestamp(epoch, sequence, keyFrame, sequence*1000)
	c.videoMu.Lock()
	configGeneration := c.videoConfigGeneration
	probeGeneration := c.videoProbeGeneration
	c.videoMu.Unlock()
	c.noteVideoFrameWrittenAt(queuedVideoFrame{
		data: frame, meta: parseTSF2(frame), queuedAt: writtenAt,
		configGeneration: configGeneration, probeGeneration: probeGeneration,
	}, writtenAt)
}

func noteTestKeyframeWritten(c *client, epoch, sequence uint64, writtenAt time.Time) {
	noteTestFrameWritten(c, epoch, sequence, true, writtenAt)
}

func itoaUint(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = digits[value%10]
		value /= 10
	}
	return string(buf[i:])
}

func itoaInt(value int) string {
	if value < 0 {
		return "-" + itoaUint(uint64(-value))
	}
	return itoaUint(uint64(value))
}

func TestStreamFeedbackIsBoundedAndEntersKeyframeOnly(t *testing.T) {
	client := &client{videoEpoch: 7, videoDeliveryMode: videoDeliveryFull}
	now := time.Unix(1_700_000_000, 0)
	if !client.acceptStreamFeedback(streamFeedbackFixture(7, 120, 120, 119, 111, 8, 2_500), now) {
		t.Fatal("first feedback sample should be accepted")
	}
	if got := client.deliveryMode(); got != videoDeliveryKeyframeOnly {
		t.Fatalf("hard decoder pressure mode = %q, want keyframe_only", got)
	}
	client.videoMu.Lock()
	if client.videoLastWrittenSeq != 0 || client.videoReadyForDelta {
		client.videoMu.Unlock()
		t.Fatalf("keyframe-only transition must reset delta readiness: seq=%d ready=%v", client.videoLastWrittenSeq, client.videoReadyForDelta)
	}
	client.videoMu.Unlock()

	key := testTSF2FrameWithTimestamp(7, 121, true, 121000)
	client.noteVideoFrameWritten(queuedVideoFrame{data: key, meta: parseTSF2(key), queuedAt: now})
	if got := client.deliveryMode(); got != videoDeliveryKeyframeOnly {
		t.Fatalf("natural keyframe auto-promoted slow viewer to %q", got)
	}
	if client.acceptStreamFeedback(streamFeedbackFixture(7, 121, 121, 120, 121, 1, 200), now.Add(100*time.Millisecond)) {
		t.Fatal("feedback faster than four samples per second should be rejected")
	}
	if client.acceptStreamFeedback(streamFeedbackFixture(7, 119, 119, 119, 111, 1, 200), now.Add(300*time.Millisecond)) {
		t.Fatal("backwards cumulative feedback should be rejected")
	}
}

func TestStreamFeedbackVisualAgeAloneKeepsFullDelivery(t *testing.T) {
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryFull,
		feedbackVisibility: "visible",
	}
	now := time.Unix(1_700_000_000, 0)
	for i, age := range []int{1_100, 1_600, 2_500} {
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, uint64(120+i), uint64(120+i), uint64(120+i), 111, 0, age),
			now.Add(time.Duration(i)*time.Second),
		) {
			t.Fatalf("advancing age feedback sample %d was rejected", i+1)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryFull {
		t.Fatalf("advancing age feedback changed delivery mode to %q, want full", got)
	}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{viewer}); demand != "full" || maxFPS != 10 {
		t.Fatalf("advancing age feedback demand=%q maxFps=%d, want full/10", demand, maxFPS)
	}
}

func TestStreamFeedbackSourceStallKeepsFullAndRequestsOneKeyframe(t *testing.T) {
	requests := make(chan string, 2)
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 120,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
		feedbackVisibility:  "visible",
		onVideoKeyframeNeeded: func(reason string, _ uint64) {
			requests <- reason
		},
	}
	now := time.Unix(1_700_000_000, 0)
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 120, 120, 120, 111, 0, 100), now) {
		t.Fatal("baseline feedback was rejected")
	}
	for i, age := range []int{2_100, 2_600, 3_100} {
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, 120, 120, 120, 111, 0, age),
			now.Add(time.Duration(i+1)*500*time.Millisecond),
		) {
			t.Fatalf("source-stall feedback sample %d was rejected", i+1)
		}
	}
	select {
	case reason := <-requests:
		if reason != "source_stall" {
			t.Fatalf("source-stall keyframe reason=%q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("source stall did not request one keyframe")
	}
	select {
	case reason := <-requests:
		t.Fatalf("source stall requested a duplicate keyframe: %q", reason)
	case <-time.After(20 * time.Millisecond):
	}
	if got := viewer.deliveryMode(); got != videoDeliveryFull {
		t.Fatalf("source-stall feedback mode=%q, want full", got)
	}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{viewer}); demand != "full" || maxFPS != 10 {
		t.Fatalf("source-stall feedback demand=%q maxFps=%d, want full/10", demand, maxFPS)
	}
	viewer.videoMu.Lock()
	if viewer.videoLastWrittenSeq != 120 || !viewer.videoReadyForDelta || viewer.videoReadyEpoch != 7 {
		viewer.videoMu.Unlock()
		t.Fatalf(
			"source stall reset live GOP evidence: sequence=%d ready=%t epoch=%d",
			viewer.videoLastWrittenSeq,
			viewer.videoReadyForDelta,
			viewer.videoReadyEpoch,
		)
	}
	viewer.videoMu.Unlock()

	// A keyframe write completes the writer's ordinary pending request, but it
	// does not end the source-stall episode until browser feedback proves actual
	// receive/decode/render progress.
	viewer.noteVideoFrameWrittenAt(queuedVideoFrame{
		meta:             tsf2Metadata{ok: true, keyFrame: true, epoch: 7, sequence: 121},
		configGeneration: 0,
	}, now.Add(1750*time.Millisecond))
	if !viewer.acceptStreamFeedback(
		streamFeedbackFixture(7, 120, 120, 120, 111, 0, 3_600),
		now.Add(2*time.Second),
	) {
		t.Fatal("post-keyframe stationary feedback was rejected")
	}
	select {
	case reason := <-requests:
		t.Fatalf("keyframe write retriggered the same source-stall episode: %q", reason)
	case <-time.After(20 * time.Millisecond):
	}

	if !viewer.acceptStreamFeedback(
		streamFeedbackFixture(7, 121, 121, 121, 121, 0, 100),
		now.Add(2500*time.Millisecond),
	) {
		t.Fatal("progress feedback was rejected")
	}
	for i, age := range []int{2_100, 2_600} {
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, 121, 121, 121, 121, 0, age),
			now.Add(time.Duration(i+6)*500*time.Millisecond),
		) {
			t.Fatalf("new source-stall feedback sample %d was rejected", i+1)
		}
	}
	select {
	case reason := <-requests:
		if reason != "source_stall" {
			t.Fatalf("new source-stall episode reason=%q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("actual sequence progress did not reset source-stall request latching")
	}
}

func TestStreamFeedbackAdvancingIngressStalledRenderEntersKeyframeOnly(t *testing.T) {
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryFull,
		feedbackVisibility: "visible",
	}
	now := time.Unix(1_700_000_000, 0)
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 120, 120, 120, 111, 0, 100), now) {
		t.Fatal("baseline feedback was rejected")
	}
	for i, age := range []int{2_100, 2_600} {
		sequence := uint64(121 + i)
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, sequence, sequence, 120, 111, 0, age),
			now.Add(time.Duration(i+1)*500*time.Millisecond),
		) {
			t.Fatalf("browser-render-stall feedback sample %d was rejected", i+1)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryKeyframeOnly {
		t.Fatalf("browser-render-stall mode=%q, want keyframe_only", got)
	}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{viewer}); demand != "keyframe_only" || maxFPS != 1 {
		t.Fatalf("browser-render-stall demand=%q maxFps=%d, want keyframe_only/1", demand, maxFPS)
	}
}

func TestStreamFeedbackKeyframeOnlyRecoversAtStaticCadence(t *testing.T) {
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryKeyframeOnly,
		feedbackVisibility: "visible",
	}
	now := time.Unix(1_700_000_000, 0)
	for i, age := range []int{450, 1_050, 1_750} {
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, 121, 121, 121, 121, 0, age),
			now.Add(time.Duration(i)*500*time.Millisecond),
		) {
			t.Fatalf("static-cadence recovery sample %d was rejected", i+1)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("static-cadence recovery mode=%q, want probe", got)
	}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{viewer}); demand != "full" || maxFPS != 10 {
		t.Fatalf("probe demand=%q maxFps=%d, want full/10", demand, maxFPS)
	}
}

func TestStreamFeedbackStaleKeyframesCannotPromote(t *testing.T) {
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryKeyframeOnly,
		feedbackVisibility: "visible",
	}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3; i++ {
		sequence := uint64(121 + i)
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, sequence, sequence, sequence, sequence, 0, 2_100),
			now.Add(time.Duration(i)*500*time.Millisecond),
		) {
			t.Fatalf("stale keyframe feedback sample %d was rejected", i+1)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryKeyframeOnly {
		t.Fatalf("stale keyframes promoted delivery mode to %q, want keyframe_only", got)
	}
}

func TestStreamFeedbackAdvancingKeyframesRecoverNormally(t *testing.T) {
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryKeyframeOnly,
		feedbackVisibility: "visible",
	}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 2; i++ {
		sequence := uint64(121 + i)
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, sequence, sequence, sequence, sequence, 0, 200),
			now.Add(time.Duration(i)*500*time.Millisecond),
		) {
			t.Fatalf("advancing keyframe feedback sample %d was rejected", i+1)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("advancing keyframe recovery mode=%q, want probe", got)
	}
}

func TestDeltaGapSchedulesOneFreshKeyframeUntilAKeyframeIsWritten(t *testing.T) {
	var requests atomic.Int32
	type keyframeRequest struct {
		reason   string
		sequence uint64
	}
	reasons := make(chan keyframeRequest, 4)
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 10,
		onVideoKeyframeNeeded: func(reason string, sequence uint64) {
			requests.Add(1)
			reasons <- keyframeRequest{reason: reason, sequence: sequence}
		},
	}

	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 12, false, 12000))
	select {
	case request := <-reasons:
		if request.reason != "sequence_gap" || request.sequence != 1 {
			t.Fatalf("gap keyframe request=%#v, want sequence_gap generation 1", request)
		}
	case <-time.After(time.Second):
		t.Fatal("sequence gap did not schedule a fresh keyframe")
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 13, false, 13000))
	time.Sleep(20 * time.Millisecond)
	if got := requests.Load(); got != 1 {
		t.Fatalf("repeated deltas scheduled %d keyframes while already awaiting one, want 1", got)
	}

	noteTestKeyframeWritten(viewer, 7, 20, time.Now())
	viewer.setVideoDeliveryMode(videoDeliveryFull)
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 22, false, 22000))
	select {
	case request := <-reasons:
		if request.reason != "sequence_gap" || request.sequence != 2 {
			t.Fatalf("second gap keyframe request=%#v, want sequence_gap generation 2", request)
		}
	case <-time.After(time.Second):
		t.Fatal("new GOP gap did not schedule a fresh keyframe")
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("keyframe request count=%d, want one per broken GOP", got)
	}
}

func TestContiguousDeltaBehindInFlightWriteKeepsWriterFull(t *testing.T) {
	requests := make(chan string, 1)
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 10,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
		onVideoKeyframeNeeded: func(reason string, _ uint64) {
			requests <- reason
		},
	}

	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, false, 11000))
	first, ok := viewer.nextVideoWriteItem()
	if !ok || first.frame == nil || first.frame.meta.sequence != 11 {
		t.Fatalf("first write item = %#v, want delta 11", first)
	}

	// Model delta 11 being removed from the queue while conn.Write is still
	// blocked. Delta 12 is contiguous with that in-flight write even though the
	// queued tail is temporarily empty and the last successful write is 10.
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 12, false, 12000))
	select {
	case reason := <-requests:
		t.Fatalf("contiguous in-flight delivery requested keyframe: %s", reason)
	default:
	}
	if got := viewer.deliveryMode(); got != videoDeliveryFull {
		t.Fatalf("contiguous in-flight delivery mode = %q, want full", got)
	}

	viewer.noteVideoFrameWrittenAt(*first.frame, time.Unix(1_700_000_000, 0))
	second, ok := viewer.nextVideoWriteItem()
	if !ok || second.frame == nil || second.frame.meta.sequence != 12 {
		t.Fatalf("second write item = %#v, want delta 12", second)
	}
	viewer.noteVideoFrameWrittenAt(*second.frame, time.Unix(1_700_000_000, int64(time.Millisecond)))

	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoInFlight || viewer.videoLastWrittenSeq != 12 || viewer.videoDeliveryMode != videoDeliveryFull {
		t.Fatalf(
			"settled writer inFlight=%t lastWritten=%d mode=%q, want false/12/full",
			viewer.videoInFlight,
			viewer.videoLastWrittenSeq,
			viewer.videoDeliveryMode,
		)
	}
	if len(viewer.videoWrittenEvidence) != 2 || !viewer.videoWrittenEvidence[0].decodable || !viewer.videoWrittenEvidence[1].decodable {
		t.Fatalf("successful contiguous evidence = %#v, want two decodable frames", viewer.videoWrittenEvidence)
	}
}

func TestFullCadenceEncoderBatchFitsViewerQueueWithoutRecovery(t *testing.T) {
	var requests atomic.Int32
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 5152,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
		onVideoKeyframeNeeded: func(string, uint64) {
			requests.Add(1)
		},
	}

	// MediaCodec may release a full-cadence batch before the writer goroutine
	// gets scheduled. The bounded viewer queue must retain that normal burst in
	// order instead of misclassifying its final delta as overflow recovery.
	for sequence := uint64(5153); sequence <= 5164; sequence++ {
		viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, sequence, false, sequence*1000))
	}

	viewer.videoMu.Lock()
	if len(viewer.videoQueue) != videoQueueMaxFrames ||
		viewer.videoDeliveryMode != videoDeliveryFull ||
		viewer.videoKeyframeRequestPending ||
		viewer.videoKeyframeRequestSequence != 0 {
		viewer.videoMu.Unlock()
		t.Fatalf(
			"full-cadence batch queue=%d mode=%q pending=%t requests=%d, want %d/full/false/0",
			len(viewer.videoQueue),
			viewer.videoDeliveryMode,
			viewer.videoKeyframeRequestPending,
			viewer.videoKeyframeRequestSequence,
			videoQueueMaxFrames,
		)
	}
	viewer.videoMu.Unlock()

	base := time.Unix(1_700_000_000, 0)
	for sequence := uint64(5153); sequence <= 5164; sequence++ {
		item, ok := viewer.nextVideoWriteItem()
		if !ok || item.frame == nil || item.frame.meta.sequence != sequence {
			t.Fatalf("full-cadence write item at sequence %d = %#v", sequence, item)
		}
		viewer.noteVideoFrameWrittenAt(*item.frame, base.Add(time.Duration(sequence-5153)*time.Millisecond))
	}
	if _, ok := viewer.nextVideoWriteItem(); ok {
		t.Fatal("full-cadence batch retained unexpected work after ordered drain")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("full-cadence batch requested %d recovery keyframes, want 0", got)
	}
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoLastWrittenSeq != 5164 || viewer.videoDeliveryMode != videoDeliveryFull || viewer.videoInFlight {
		t.Fatalf(
			"settled full-cadence batch last=%d mode=%q inFlight=%t, want 5164/full/false",
			viewer.videoLastWrittenSeq,
			viewer.videoDeliveryMode,
			viewer.videoInFlight,
		)
	}
}

func TestThirteenthUndrainedDeltaTriggersOneBoundedQueueRecovery(t *testing.T) {
	type keyframeRequest struct {
		reason   string
		sequence uint64
	}
	requests := make(chan keyframeRequest, 2)
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 5152,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
		onVideoKeyframeNeeded: func(reason string, sequence uint64) {
			requests <- keyframeRequest{reason: reason, sequence: sequence}
		},
	}

	for sequence := uint64(5153); sequence <= 5164; sequence++ {
		viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, sequence, false, sequence*1000))
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 5165, false, 5165000))

	viewer.videoMu.Lock()
	queueLength := len(viewer.videoQueue)
	mode := viewer.videoDeliveryMode
	pending := viewer.videoKeyframeRequestPending
	requestSequence := viewer.videoKeyframeRequestSequence
	viewer.videoMu.Unlock()
	if queueLength != 0 || mode != videoDeliveryKeyframeOnly || !pending || requestSequence != 1 {
		t.Fatalf(
			"overflow boundary queue=%d mode=%q pending=%t requests=%d, want 0/keyframe_only/true/1",
			queueLength,
			mode,
			pending,
			requestSequence,
		)
	}
	select {
	case request := <-requests:
		if request.reason != "queue_overflow" || request.sequence != 1 {
			t.Fatalf("overflow keyframe request=%#v, want queue_overflow generation 1", request)
		}
	case <-time.After(time.Second):
		t.Fatal("thirteenth undrained delta did not request a recovery keyframe")
	}

	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 5166, false, 5166000))
	select {
	case request := <-requests:
		t.Fatalf("broken overflow GOP requested more than one keyframe: %#v", request)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestDeltaQueuesBehindInFlightRecoveryKeyframe(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		mode                  videoDeliveryMode
		probeGeneration       uint64
		probeAwaiting         bool
		wantModeAfterKeyframe videoDeliveryMode
	}{
		{name: "awaiting_keyframe", mode: videoDeliveryAwaitingKeyframe, wantModeAfterKeyframe: videoDeliveryFull},
		{name: "probe", mode: videoDeliveryProbe, probeGeneration: 3, probeAwaiting: true, wantModeAfterKeyframe: videoDeliveryProbe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viewer := &client{
				videoEpoch:                 7,
				videoDeliveryMode:          tc.mode,
				videoProbeGeneration:       tc.probeGeneration,
				videoProbeAwaitingKeyframe: tc.probeAwaiting,
			}
			viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 10, true, 10000))
			keyframe, ok := viewer.nextVideoWriteItem()
			if !ok || keyframe.frame == nil || !keyframe.frame.meta.keyFrame {
				t.Fatalf("recovery write item = %#v, want keyframe", keyframe)
			}

			viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, false, 11000))
			viewer.videoMu.Lock()
			if len(viewer.videoQueue) != 1 || viewer.videoQueue[0].meta.sequence != 11 {
				viewer.videoMu.Unlock()
				t.Fatalf("delta behind in-flight keyframe queue = %#v, want sequence 11", viewer.videoQueue)
			}
			viewer.videoMu.Unlock()

			viewer.noteVideoFrameWrittenAt(*keyframe.frame, time.Unix(1_700_000_000, 0))
			if got := viewer.deliveryMode(); got != tc.wantModeAfterKeyframe {
				t.Fatalf("mode after recovery keyframe = %q, want %q", got, tc.wantModeAfterKeyframe)
			}
			delta, ok := viewer.nextVideoWriteItem()
			if !ok || delta.frame == nil || delta.frame.meta.sequence != 11 {
				t.Fatalf("queued recovery delta = %#v, want sequence 11", delta)
			}
			viewer.noteVideoFrameWrittenAt(*delta.frame, time.Unix(1_700_000_000, int64(time.Millisecond)))
			viewer.videoMu.Lock()
			defer viewer.videoMu.Unlock()
			if viewer.videoLastWrittenSeq != 11 || viewer.videoInFlight {
				t.Fatalf("settled recovery writer last=%d inFlight=%t", viewer.videoLastWrittenSeq, viewer.videoInFlight)
			}
		})
	}
}

func TestDeltasQueueBehindQueuedAndInFlightRecoveryKeyframe(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mode            videoDeliveryMode
		probeGeneration uint64
		probeAwaiting   bool
		wantMode        videoDeliveryMode
	}{
		{name: "awaiting_keyframe", mode: videoDeliveryAwaitingKeyframe, wantMode: videoDeliveryFull},
		{name: "probe", mode: videoDeliveryProbe, probeGeneration: 3, probeAwaiting: true, wantMode: videoDeliveryProbe},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan string, 1)
			viewer := &client{
				videoEpoch:                 7,
				videoDeliveryMode:          tc.mode,
				videoProbeGeneration:       tc.probeGeneration,
				videoProbeAwaitingKeyframe: tc.probeAwaiting,
				onVideoKeyframeNeeded: func(reason string, _ uint64) {
					requests <- reason
				},
			}

			viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 10, true, 10000))
			// The writer has not popped the requested keyframe yet. Multiple
			// contiguous deltas must remain behind that queued GOP anchor.
			viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, false, 11000))
			viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 12, false, 12000))
			viewer.videoMu.Lock()
			if len(viewer.videoQueue) != 3 || !viewer.videoQueue[0].meta.keyFrame || viewer.videoQueue[2].meta.sequence != 12 {
				viewer.videoMu.Unlock()
				t.Fatalf("queued recovery GOP = %#v, want keyframe 10 then deltas 11/12", viewer.videoQueue)
			}
			viewer.videoMu.Unlock()

			keyframe, ok := viewer.nextVideoWriteItem()
			if !ok || keyframe.frame == nil || !keyframe.frame.meta.keyFrame {
				t.Fatalf("recovery write item = %#v, want keyframe", keyframe)
			}
			// While the keyframe write is in flight, another contiguous delta
			// must remain behind the already admitted deltas.
			viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 13, false, 13000))
			select {
			case reason := <-requests:
				t.Fatalf("contiguous recovery GOP requested keyframe: %s", reason)
			default:
			}
			if got := viewer.deliveryMode(); got != tc.mode {
				t.Fatalf("contiguous recovery GOP mode = %q, want %q", got, tc.mode)
			}

			viewer.noteVideoFrameWrittenAt(*keyframe.frame, time.Unix(1_700_000_000, 0))
			for sequence := uint64(11); sequence <= 13; sequence++ {
				item, ok := viewer.nextVideoWriteItem()
				if !ok || item.frame == nil || item.frame.meta.sequence != sequence {
					t.Fatalf("recovery write item after keyframe = %#v, want delta %d", item, sequence)
				}
				viewer.noteVideoFrameWrittenAt(*item.frame, time.Unix(1_700_000_000, int64(sequence-10)*int64(time.Millisecond)))
			}
			viewer.videoMu.Lock()
			defer viewer.videoMu.Unlock()
			if viewer.videoInFlight || viewer.videoLastWrittenSeq != 13 || viewer.videoDeliveryMode != tc.wantMode {
				t.Fatalf(
					"settled recovery writer inFlight=%t last=%d mode=%q, want false/13/%s",
					viewer.videoInFlight,
					viewer.videoLastWrittenSeq,
					viewer.videoDeliveryMode,
					tc.wantMode,
				)
			}
		})
	}
}

func TestFortySecondsContiguousFramesRemainFullAcrossInFlightWrites(t *testing.T) {
	requests := make(chan string, 1)
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 10,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
		onVideoKeyframeNeeded: func(reason string, _ uint64) {
			requests <- reason
		},
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, false, 11000))
	inFlight, ok := viewer.nextVideoWriteItem()
	if !ok || inFlight.frame == nil {
		t.Fatalf("initial write item = %#v, want delta", inFlight)
	}
	base := time.Unix(1_700_000_000, 0)
	for sequence := uint64(12); sequence <= 410; sequence++ {
		viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, sequence, false, sequence*1000))
		viewer.noteVideoFrameWrittenAt(*inFlight.frame, base.Add(time.Duration(sequence-12)*100*time.Millisecond))
		inFlight, ok = viewer.nextVideoWriteItem()
		if !ok || inFlight.frame == nil || inFlight.frame.meta.sequence != sequence {
			t.Fatalf("write item at sequence %d = %#v", sequence, inFlight)
		}
	}
	viewer.noteVideoFrameWrittenAt(*inFlight.frame, base.Add(40*time.Second))
	select {
	case reason := <-requests:
		t.Fatalf("contiguous traffic requested keyframe: %s", reason)
	default:
	}
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoDeliveryMode != videoDeliveryFull || viewer.videoLastWrittenSeq != 410 || viewer.videoInFlight {
		t.Fatalf(
			"forty-second writer mode=%q last=%d inFlight=%t, want full/410/false",
			viewer.videoDeliveryMode,
			viewer.videoLastWrittenSeq,
			viewer.videoInFlight,
		)
	}
}

func TestTrueDeltaGapBehindInFlightWriteRequestsOneKeyframe(t *testing.T) {
	type keyframeRequest struct {
		reason   string
		sequence uint64
	}
	requests := make(chan keyframeRequest, 2)
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 10,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
		onVideoKeyframeNeeded: func(reason string, sequence uint64) {
			requests <- keyframeRequest{reason: reason, sequence: sequence}
		},
	}

	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, false, 11000))
	first, ok := viewer.nextVideoWriteItem()
	if !ok || first.frame == nil || first.frame.meta.sequence != 11 {
		t.Fatalf("first write item = %#v, want delta 11", first)
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 13, false, 13000))

	select {
	case request := <-requests:
		if request.reason != "sequence_gap" || request.sequence != 1 {
			t.Fatalf("true in-flight gap request = %#v, want sequence_gap generation 1", request)
		}
	case <-time.After(time.Second):
		t.Fatal("true in-flight gap did not request a keyframe")
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 14, false, 14000))
	select {
	case request := <-requests:
		t.Fatalf("true broken GOP requested more than once: %#v", request)
	case <-time.After(20 * time.Millisecond):
	}
	viewer.noteVideoFrameWrittenAt(*first.frame, time.Unix(1_700_000_000, 0))
	if got := viewer.deliveryMode(); got != videoDeliveryKeyframeOnly {
		t.Fatalf("true in-flight gap mode = %q, want keyframe_only", got)
	}
}

func TestConfigAndStopClearInFlightContinuity(t *testing.T) {
	viewer := &client{
		videoEpoch:          7,
		videoDeliveryMode:   videoDeliveryFull,
		videoLastWrittenSeq: 10,
		videoReadyForDelta:  true,
		videoReadyEpoch:     7,
	}
	viewer.enqueueVideoFrame(testTSF2FrameWithTimestamp(7, 11, false, 11000))
	oldWrite, ok := viewer.nextVideoWriteItem()
	if !ok || oldWrite.frame == nil {
		t.Fatalf("old write item = %#v, want delta", oldWrite)
	}
	viewer.enqueueControl([]byte(`{"type":"config","codec":"avc1.42E01E","streamEpoch":8}`))

	viewer.videoMu.Lock()
	if viewer.videoInFlight {
		viewer.videoMu.Unlock()
		t.Fatal("new config retained old in-flight continuity")
	}
	viewer.videoMu.Unlock()
	viewer.noteVideoFrameWrittenAt(*oldWrite.frame, time.Unix(1_700_000_000, 0))

	viewer.videoMu.Lock()
	if viewer.videoEpoch != 8 || viewer.videoWrittenSequence != 0 || len(viewer.videoWrittenEvidence) != 0 {
		viewer.videoMu.Unlock()
		t.Fatalf(
			"late old write changed new config: epoch=%d written=%d evidence=%d",
			viewer.videoEpoch,
			viewer.videoWrittenSequence,
			len(viewer.videoWrittenEvidence),
		)
	}
	viewer.videoMu.Unlock()

	stopping := &client{videoEpoch: 9, videoDeliveryMode: videoDeliveryFull, videoLastWrittenSeq: 20}
	stopping.enqueueVideoFrame(testTSF2FrameWithTimestamp(9, 21, false, 21000))
	if item, ok := stopping.nextVideoWriteItem(); !ok || item.frame == nil {
		t.Fatalf("stopping write item = %#v, want delta", item)
	}
	stopping.stopVideoWriter()
	stopping.videoMu.Lock()
	defer stopping.videoMu.Unlock()
	if stopping.videoInFlight {
		t.Fatal("writer stop retained in-flight continuity")
	}
}

func TestWarmSnapshotCannotOverwriteNewerLiveConfig(t *testing.T) {
	viewer := &client{}
	expectedGeneration := viewer.videoConfigGenerationSnapshot()
	liveConfig := []byte(`{"type":"config","codec":"avc1.42E01E","streamEpoch":2}`)
	viewer.enqueueControl(liveConfig)
	warmConfig := []byte(`{"type":"config","codec":"avc1.42E01E","streamEpoch":1}`)
	warmKeyframe := testTSF2FrameWithTimestamp(1, 10, true, 10000)

	configSent, keyframeSent, stale := viewer.enqueueWarmStart(warmConfig, warmKeyframe, expectedGeneration)
	if configSent || keyframeSent || !stale {
		t.Fatalf("stale warm result config=%t keyframe=%t stale=%t", configSent, keyframeSent, stale)
	}
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoEpoch != 2 || viewer.videoConfigGeneration != expectedGeneration+1 {
		t.Fatalf("live config was replaced: epoch=%d generation=%d", viewer.videoEpoch, viewer.videoConfigGeneration)
	}
	if len(viewer.controlQueue) != 1 || viewer.controlQueue[0].epoch != 2 {
		t.Fatalf("live control queue was replaced by stale warm config: %#v", viewer.controlQueue)
	}
	if len(viewer.videoQueue) != 0 {
		t.Fatalf("stale warm keyframe entered live queue: %#v", viewer.videoQueue)
	}
}

func TestPoppedWarmFrameCannotRegressNewerLiveConfigEvidence(t *testing.T) {
	var forwarded atomic.Int32
	viewer := &client{onVideoFrameWritten: func(tsf2Metadata) { forwarded.Add(1) }}
	expectedGeneration := viewer.videoConfigGenerationSnapshot()
	warmConfig := []byte(`{"type":"config","codec":"avc1.42E01E","streamEpoch":1}`)
	warmKeyframe := testTSF2FrameWithTimestamp(1, 10, true, 10000)

	configSent, keyframeSent, stale := viewer.enqueueWarmStart(warmConfig, warmKeyframe, expectedGeneration)
	if !configSent || !keyframeSent || stale {
		t.Fatalf("warm result config=%t keyframe=%t stale=%t", configSent, keyframeSent, stale)
	}
	if item, ok := viewer.nextVideoWriteItem(); !ok || item.frame != nil {
		t.Fatalf("first warm item = %#v, want decoder config", item)
	}
	warmItem, ok := viewer.nextVideoWriteItem()
	if !ok || warmItem.frame == nil {
		t.Fatalf("second warm item = %#v, want cached keyframe", warmItem)
	}

	// Model the keyframe being popped for a socket write while a newer live
	// configuration arrives. A successful late write from the warm snapshot
	// must not become evidence for the newer decoder generation.
	liveConfig := []byte(`{"type":"config","codec":"avc1.42E01E","streamEpoch":2}`)
	viewer.enqueueControl(liveConfig)
	viewer.noteVideoFrameWrittenAt(*warmItem.frame, time.Unix(1_700_000_000, 0))

	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoEpoch != 2 || viewer.videoConfigGeneration != expectedGeneration+2 {
		t.Fatalf("live generation regressed: epoch=%d generation=%d", viewer.videoEpoch, viewer.videoConfigGeneration)
	}
	if viewer.videoWrittenEpoch != 0 || viewer.videoWrittenSequence != 0 || viewer.videoWrittenKeyframeSequence != 0 {
		t.Fatalf(
			"stale warm write became live evidence: epoch=%d sequence=%d keyframe=%d",
			viewer.videoWrittenEpoch,
			viewer.videoWrittenSequence,
			viewer.videoWrittenKeyframeSequence,
		)
	}
	if viewer.videoReadyForDelta {
		t.Fatal("stale warm keyframe made the newer decoder generation delta-ready")
	}
	if got := forwarded.Load(); got != 0 {
		t.Fatalf("stale warm write fired %d current-generation callbacks", got)
	}
}

func TestBrowserMarkerEvidenceSurvivesANewerWrittenKeyframe(t *testing.T) {
	viewer := &client{videoEpoch: 7, videoDeliveryMode: videoDeliveryFull}
	now := time.Unix(1_700_000_000, 0)
	noteTestKeyframeWritten(viewer, 7, 10, now)
	noteTestFrameWritten(viewer, 7, 11, false, now.Add(time.Millisecond))
	noteTestKeyframeWritten(viewer, 7, 20, now.Add(2*time.Millisecond))

	for _, sequence := range []float64{10, 11, 20} {
		if !viewer.browserFrameMarkerMatchesSuccessfulWrite(map[string]any{
			"frameEpoch": float64(7), "frameSequence": sequence,
		}) {
			t.Fatalf("successful written frame %.0f was lost after a newer keyframe", sequence)
		}
	}
	if viewer.browserFrameMarkerMatchesSuccessfulWrite(map[string]any{
		"frameEpoch": float64(7), "frameSequence": float64(15),
	}) {
		t.Fatal("unwritten frame inside the sequence range was accepted")
	}
}

func TestProbeWaitsForRequestedKeyframeThenTwoHealthySeconds(t *testing.T) {
	reasons := make(chan string, 1)
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryKeyframeOnly,
		feedbackVisibility: "visible",
		onVideoKeyframeNeeded: func(reason string, _ uint64) {
			reasons <- reason
		},
	}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 2; i++ {
		sequence := uint64(121 + i)
		if !viewer.acceptStreamFeedback(
			streamFeedbackFixture(7, sequence, sequence, sequence, sequence, 0, 1_750),
			now.Add(time.Duration(i)*500*time.Millisecond),
		) {
			t.Fatalf("keyframe-only recovery sample %d was rejected", i+1)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("recovery mode=%q, want probe", got)
	}
	select {
	case reason := <-reasons:
		if reason != "probe_transition" {
			t.Fatalf("probe keyframe reason=%q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("probe transition did not request a fresh keyframe")
	}

	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 123, 123, 123, 122, 0, 1_750), now.Add(time.Second)) {
		t.Fatal("delayed probe-keyframe feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("probe fell back before its requested keyframe arrived: %q", got)
	}
	viewer.feedbackMu.Lock()
	probeSinceBeforeWrite := viewer.feedbackProbeSince
	viewer.feedbackMu.Unlock()
	if !probeSinceBeforeWrite.IsZero() {
		t.Fatalf("probe stability clock started before keyframe write: %v", probeSinceBeforeWrite)
	}

	keyframeWrittenAt := now.Add(1500 * time.Millisecond)
	noteTestKeyframeWritten(viewer, 7, 124, keyframeWrittenAt)
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 124, 124, 124, 124, 0, 1_750), now.Add(1750*time.Millisecond)) {
		t.Fatal("first post-keyframe probe feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("probe promoted before two healthy seconds: %q", got)
	}
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 125, 125, 125, 124, 0, 1_750), now.Add(3750*time.Millisecond)) {
		t.Fatal("two-second probe feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryFull {
		t.Fatalf("healthy probe mode=%q, want full", got)
	}
}

func TestProbeIgnoresKeyframeAlreadyInFlightBeforeTransition(t *testing.T) {
	reasons := make(chan string, 1)
	viewer := &client{
		videoEpoch:         7,
		videoDeliveryMode:  videoDeliveryKeyframeOnly,
		feedbackVisibility: "visible",
		onVideoKeyframeNeeded: func(reason string, _ uint64) {
			reasons <- reason
		},
	}
	oldKeyframe := testTSF2FrameWithTimestamp(7, 120, true, 120000)
	viewer.enqueueVideoFrame(oldKeyframe)
	oldItem, ok := viewer.nextVideoWriteItem()
	if !ok || oldItem.frame == nil {
		t.Fatal("pre-probe keyframe did not enter the write path")
	}

	viewer.setVideoDeliveryMode(videoDeliveryProbe)
	select {
	case <-reasons:
	case <-time.After(time.Second):
		t.Fatal("probe transition did not request a keyframe")
	}
	viewer.noteVideoFrameWrittenAt(*oldItem.frame, time.Unix(1_700_000_000, 0))
	viewer.videoMu.Lock()
	if !viewer.videoProbeAwaitingKeyframe || viewer.videoProbeKeyframeSequence != 0 || !viewer.videoKeyframeRequestPending {
		viewer.videoMu.Unlock()
		t.Fatal("pre-probe in-flight keyframe satisfied the probe request")
	}
	viewer.videoMu.Unlock()

	newKeyframe := testTSF2FrameWithTimestamp(7, 121, true, 121000)
	viewer.enqueueVideoFrame(newKeyframe)
	newItem, ok := viewer.nextVideoWriteItem()
	if !ok || newItem.frame == nil {
		t.Fatal("post-transition keyframe did not enter the write path")
	}
	viewer.noteVideoFrameWrittenAt(*newItem.frame, time.Unix(1_700_000_001, 0))
	viewer.videoMu.Lock()
	defer viewer.videoMu.Unlock()
	if viewer.videoProbeAwaitingKeyframe || viewer.videoProbeKeyframeSequence != 121 || viewer.videoKeyframeRequestPending {
		t.Fatalf(
			"post-transition keyframe did not satisfy probe: awaiting=%t sequence=%d pending=%t",
			viewer.videoProbeAwaitingKeyframe,
			viewer.videoProbeKeyframeSequence,
			viewer.videoKeyframeRequestPending,
		)
	}
}

func TestProbeRequiresTwoSecondsAfterRequestedKeyframeIsRendered(t *testing.T) {
	viewer := &client{
		videoEpoch:            7,
		videoDeliveryMode:     videoDeliveryKeyframeOnly,
		feedbackVisibility:    "visible",
		onVideoKeyframeNeeded: func(string, uint64) {},
	}
	viewer.setVideoDeliveryMode(videoDeliveryProbe)
	noteTestKeyframeWritten(viewer, 7, 124, time.Unix(1_700_000_000, 0))
	base := time.Unix(1_700_000_000, 0)

	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 124, 124, 123, 122, 0, 1_750), base.Add(5*time.Second)) {
		t.Fatal("late older-GOP feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("older rendered GOP promoted probe to %q", got)
	}
	viewer.feedbackMu.Lock()
	if !viewer.feedbackProbeSince.IsZero() {
		viewer.feedbackMu.Unlock()
		t.Fatal("older rendered GOP started probe stability clock")
	}
	viewer.feedbackMu.Unlock()

	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 124, 124, 124, 124, 0, 1_750), base.Add(6*time.Second)) {
		t.Fatal("first requested-keyframe feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("first requested-keyframe feedback promoted probe to %q", got)
	}
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 125, 125, 125, 124, 0, 1_750), base.Add(7750*time.Millisecond)) {
		t.Fatal("sub-two-second probe feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("probe promoted before two healthy feedback seconds: %q", got)
	}
	if !viewer.acceptStreamFeedback(streamFeedbackFixture(7, 126, 126, 126, 124, 0, 1_750), base.Add(8*time.Second)) {
		t.Fatal("two-second probe feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryFull {
		t.Fatalf("healthy rendered probe mode=%q, want full", got)
	}
}

func TestStreamFeedbackRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	base := streamFeedbackFixture(7, 1, 1, 1, 1, 0, 0)
	if _, ok := decodeStreamFeedback(append(base[:len(base)-1], []byte(`,"unexpected":true}`)...)); ok {
		t.Fatal("unknown feedback fields must be rejected")
	}
	if _, ok := decodeStreamFeedback(append(base, []byte(` {}`)...)); ok {
		t.Fatal("trailing feedback JSON must be rejected")
	}
}
