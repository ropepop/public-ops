package web

import (
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
		t.Fatalf("hard visual pressure mode = %q, want keyframe_only", got)
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

func TestStreamFeedbackRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	base := streamFeedbackFixture(7, 1, 1, 1, 1, 0, 0)
	if _, ok := decodeStreamFeedback(append(base[:len(base)-1], []byte(`,"unexpected":true}`)...)); ok {
		t.Fatal("unknown feedback fields must be rejected")
	}
	if _, ok := decodeStreamFeedback(append(base, []byte(` {}`)...)); ok {
		t.Fatal("trailing feedback JSON must be rejected")
	}
}
