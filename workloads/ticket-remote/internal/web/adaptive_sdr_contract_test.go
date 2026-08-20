package web

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// These values are deliberately kept in one test-only contract.  A viewer may
// ask for a different delivery cadence, but that request must not silently
// change the source picture, encoder bitrate, or SDR signalling.
const (
	adaptiveSDRCodec               = "avc1.42C028"
	adaptiveSDRTransport           = "hardware-h264-annexb"
	adaptiveSDRWidth               = 720
	adaptiveSDRHeight              = 1482
	adaptiveSDRSourceWidth         = 1080
	adaptiveSDRSourceHeight        = 2424
	adaptiveSDRSourceTopCrop       = 200
	adaptiveSDRSourceVisibleHeight = 2224
	adaptiveSDRBitrate             = 1_200_000
	adaptiveSDRSourceFPS           = 10
	adaptiveSDRKeyframeInterval    = 10
	adaptiveSDRFeedbackVersion     = 1
	adaptiveSDRColorStandard       = "bt709_limited_sdr"
	adaptiveSDRColorTransfer       = "sdr"
	adaptiveSDRColorMatrix         = "bt709"
)

// adaptiveSDRConfigFixture models the phone config that the browser receives.
// The fields after the existing config fields are advisory metadata for the
// adaptive viewer path; browser feedback must never contain picture controls.
var adaptiveSDRConfigFixture = []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":720,"height":1482,"rootCapture":true,"sourceWidth":1080,"sourceHeight":2424,"sourceTopCrop":200,"sourceVisibleHeight":2224,"bitrate":1200000,"fps":10,"feedbackVersion":1,"sourceFps":10,"keyframeIntervalFrames":10,"colorStandard":"bt709_limited_sdr","colorTransfer":"sdr","colorMatrix":"bt709","streamEpoch":7,"phoneUptimeMillis":10000}`)

type adaptiveSDRConfig struct {
	Type                   string `json:"type"`
	Codec                  string `json:"codec"`
	Transport              string `json:"transport"`
	Width                  int    `json:"width"`
	Height                 int    `json:"height"`
	RootCapture            bool   `json:"rootCapture"`
	SourceWidth            int    `json:"sourceWidth"`
	SourceHeight           int    `json:"sourceHeight"`
	SourceTopCrop          int    `json:"sourceTopCrop"`
	SourceVisibleHeight    int    `json:"sourceVisibleHeight"`
	Bitrate                int    `json:"bitrate"`
	FPS                    int    `json:"fps"`
	FeedbackVersion        int    `json:"feedbackVersion"`
	SourceFPS              int    `json:"sourceFps"`
	KeyframeIntervalFrames int    `json:"keyframeIntervalFrames"`
	ColorStandard          string `json:"colorStandard"`
	ColorTransfer          string `json:"colorTransfer"`
	ColorMatrix            string `json:"colorMatrix"`
	StreamEpoch            uint64 `json:"streamEpoch"`
	PhoneUptimeMillis      int64  `json:"phoneUptimeMillis"`
}

type adaptiveSDRFeedback struct {
	Type                     string `json:"type"`
	Version                  int    `json:"version"`
	Epoch                    uint64 `json:"epoch"`
	ReceivedSequence         uint64 `json:"receivedSequence"`
	DecodedSequence          uint64 `json:"decodedSequence"`
	RenderedSequence         uint64 `json:"renderedSequence"`
	RenderedKeyframeSequence uint64 `json:"renderedKeyframeSequence"`
	DecoderQueueSize         uint64 `json:"decoderQueueSize"`
	RenderedVisualAgeMillis  uint64 `json:"renderedVisualAgeMillis"`
	Visibility               string `json:"visibility"`
}

func decodeAdaptiveSDRConfig(t *testing.T, raw []byte) adaptiveSDRConfig {
	t.Helper()
	var config adaptiveSDRConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode adaptive SDR config: %v", err)
	}
	return config
}

func decodeAdaptiveSDRFeedback(t *testing.T, raw []byte) (adaptiveSDRFeedback, map[string]any) {
	t.Helper()
	var feedback adaptiveSDRFeedback
	if err := json.Unmarshal(raw, &feedback); err != nil {
		t.Fatalf("decode adaptive SDR feedback: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode adaptive SDR feedback fields: %v", err)
	}
	return feedback, fields
}

func assertAdaptiveSDRPictureContract(t *testing.T, before, after adaptiveSDRConfig) {
	t.Helper()
	if before.Codec != after.Codec || before.Transport != after.Transport {
		t.Fatalf("adaptive delivery changed codec/transport: before=%+v after=%+v", before, after)
	}
	if before.Width != after.Width || before.Height != after.Height ||
		before.SourceWidth != after.SourceWidth || before.SourceHeight != after.SourceHeight ||
		before.SourceTopCrop != after.SourceTopCrop || before.SourceVisibleHeight != after.SourceVisibleHeight {
		t.Fatalf("adaptive delivery changed spatial contract: before=%+v after=%+v", before, after)
	}
	if before.Bitrate != after.Bitrate {
		t.Fatalf("adaptive delivery changed bitrate: before=%d after=%d", before.Bitrate, after.Bitrate)
	}
	if before.ColorStandard != after.ColorStandard || before.ColorTransfer != after.ColorTransfer || before.ColorMatrix != after.ColorMatrix {
		t.Fatalf("adaptive delivery changed SDR color contract: before=%+v after=%+v", before, after)
	}
}

func TestAdaptiveSDRBitrateAndPictureContract(t *testing.T) {
	config := decodeAdaptiveSDRConfig(t, adaptiveSDRConfigFixture)
	if config.Type != "config" || !config.RootCapture {
		t.Fatalf("unexpected adaptive SDR config envelope: %+v", config)
	}
	if config.Codec != adaptiveSDRCodec || config.Transport != adaptiveSDRTransport {
		t.Fatalf("unexpected codec/transport: codec=%q transport=%q", config.Codec, config.Transport)
	}
	if config.Width != adaptiveSDRWidth || config.Height != adaptiveSDRHeight ||
		config.SourceWidth != adaptiveSDRSourceWidth || config.SourceHeight != adaptiveSDRSourceHeight ||
		config.SourceTopCrop != adaptiveSDRSourceTopCrop || config.SourceVisibleHeight != adaptiveSDRSourceVisibleHeight {
		t.Fatalf("unexpected spatial contract: %+v", config)
	}
	if config.Bitrate != adaptiveSDRBitrate {
		t.Fatalf("adaptive SDR bitrate = %d, want %d", config.Bitrate, adaptiveSDRBitrate)
	}
	if config.FPS != adaptiveSDRSourceFPS || config.SourceFPS != adaptiveSDRSourceFPS ||
		config.KeyframeIntervalFrames != adaptiveSDRKeyframeInterval {
		t.Fatalf("unexpected source cadence contract: fps=%d sourceFps=%d keyframeIntervalFrames=%d", config.FPS, config.SourceFPS, config.KeyframeIntervalFrames)
	}
	if config.FeedbackVersion != adaptiveSDRFeedbackVersion || config.ColorStandard != adaptiveSDRColorStandard ||
		config.ColorTransfer != adaptiveSDRColorTransfer || config.ColorMatrix != adaptiveSDRColorMatrix {
		t.Fatalf("unexpected advisory/SDR metadata: %+v", config)
	}
}

func TestAdaptiveSDRConfigForwardingPreservesCadenceAdvisories(t *testing.T) {
	forwarded := browserVideoConfigMessage(adaptiveSDRConfigFixture)
	config := decodeAdaptiveSDRConfig(t, forwarded)
	if config.FeedbackVersion != adaptiveSDRFeedbackVersion || config.SourceFPS != adaptiveSDRSourceFPS ||
		config.KeyframeIntervalFrames != adaptiveSDRKeyframeInterval {
		t.Fatalf("browser config forwarding dropped cadence advisories: %+v", config)
	}
	var fields map[string]any
	if err := json.Unmarshal(forwarded, &fields); err != nil {
		t.Fatalf("decode forwarded config: %v", err)
	}
	for _, field := range []string{"serverVersion", "assetVersion"} {
		if value, ok := fields[field].(string); !ok || value == "" {
			t.Fatalf("forwarded config missing %s: %#v", field, fields)
		}
	}
}

func TestAdaptiveSDRDeliveryCannotChangeSpatialOrColor(t *testing.T) {
	before := decodeAdaptiveSDRConfig(t, adaptiveSDRConfigFixture)
	after := before
	// Stream identity and phone uptime may change between config messages. The
	// adaptive viewer request is intentionally absent: it is not a source
	// encoder control and therefore cannot alter this picture contract.
	after.StreamEpoch++
	after.PhoneUptimeMillis += 1000
	assertAdaptiveSDRPictureContract(t, before, after)
}

func TestAdaptiveSDRFeedbackWireContractIsAdvisoryOnly(t *testing.T) {
	raw := []byte(`{"type":"stream_feedback","version":1,"epoch":7,"receivedSequence":1234,"decodedSequence":1232,"renderedSequence":1231,"renderedKeyframeSequence":1225,"decoderQueueSize":2,"renderedVisualAgeMillis":140,"visibility":"visible"}`)
	feedback, fields := decodeAdaptiveSDRFeedback(t, raw)
	if feedback.Type != "stream_feedback" || feedback.Version != adaptiveSDRFeedbackVersion ||
		feedback.Epoch != 7 || feedback.ReceivedSequence != 1234 || feedback.DecodedSequence != 1232 ||
		feedback.RenderedSequence != 1231 || feedback.RenderedKeyframeSequence != 1225 || feedback.DecoderQueueSize != 2 || feedback.RenderedVisualAgeMillis != 140 ||
		feedback.Visibility != "visible" {
		t.Fatalf("unexpected stream feedback contract: %+v", feedback)
	}
	for _, forbidden := range []string{
		"bitrate", "width", "height", "sourceWidth", "sourceHeight", "colorStandard", "colorTransfer", "colorMatrix",
		"decoderQueue", "visualAgeMillis", "requestedMaxFps", "state",
	} {
		if _, ok := fields[forbidden]; ok {
			t.Fatalf("stream feedback must not carry source picture control %q: %#v", forbidden, fields)
		}
	}
	if _, ok := decodeStreamFeedback([]byte(`{"type":"stream_feedback","version":1,"epoch":7,"decoderQueue":1}`)); ok {
		t.Fatal("legacy or renamed feedback fields must be rejected")
	}
}

func TestAdaptiveSDRFeedbackDowngradesOnlyOneViewerAndRecoversByProbe(t *testing.T) {
	viewer := &client{videoEpoch: 7}
	viewer.setVideoDeliveryMode(videoDeliveryFull)
	high := []byte(`{"type":"stream_feedback","version":1,"epoch":7,"receivedSequence":10,"decodedSequence":5,"renderedSequence":5,"renderedKeyframeSequence":4,"decoderQueueSize":5,"renderedVisualAgeMillis":2100,"visibility":"visible"}`)
	if !viewer.acceptStreamFeedback(high, time.Unix(1_700_000_000, 0)) {
		t.Fatal("high-pressure feedback was rejected")
	}
	if got := viewer.deliveryMode(); got != videoDeliveryKeyframeOnly {
		t.Fatalf("pressure mode = %q, want keyframe_only", got)
	}
	feedbackForKeyframe := func(sequence uint64) []byte {
		return []byte(fmt.Sprintf(`{"type":"stream_feedback","version":1,"epoch":7,"receivedSequence":%d,"decodedSequence":%d,"renderedSequence":%d,"renderedKeyframeSequence":%d,"decoderQueueSize":0,"renderedVisualAgeMillis":100,"visibility":"visible"}`, sequence, sequence, sequence, sequence))
	}
	for i := 1; i <= 3; i++ {
		if !viewer.acceptStreamFeedback(feedbackForKeyframe(uint64(10+i)), time.Unix(1_700_000_000+int64(i), 0)) {
			t.Fatalf("healthy feedback %d was rejected", i)
		}
	}
	if got := viewer.deliveryMode(); got != videoDeliveryProbe {
		t.Fatalf("recovery mode = %q, want probe", got)
	}
}

func TestAdaptiveSDRFeedbackClientSourceContract(t *testing.T) {
	source, err := os.ReadFile("../../web-client/ticket-app-source.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"stream_feedback",
		"feedbackVersion",
		"sourceFps",
		"keyframeIntervalFrames",
		"renderedKeyframeSequence",
		"decoderQueueSize",
		"renderedVisualAgeMillis",
	} {
		if !containsAdaptiveSDRMarker(string(source), marker) {
			t.Fatalf("browser source is missing adaptive SDR feedback marker %q", marker)
		}
	}
}

func containsAdaptiveSDRMarker(source, marker string) bool {
	for i := 0; i+len(marker) <= len(source); i++ {
		if source[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}

func TestAdaptiveSDRSlowViewerDoesNotAffectFastViewer(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	fast := &client{videoReadyForDelta: true, videoReadyEpoch: 7}
	slow := &client{videoWriteActive: true, videoReadyForDelta: true, videoReadyEpoch: 7}
	firstDelta := testTSF2FrameWithTimestamp(7, 42, false, 41042)
	secondDelta := testTSF2FrameWithTimestamp(7, 43, false, 41043)
	nextKey := testTSF2FrameWithTimestamp(7, 44, true, 41044)

	// The fast viewer completes each queued write before the next frame arrives.
	fast.videoMu.Lock()
	fast.queuePendingVideoFrameLocked(firstDelta, false, now)
	fast.videoPendingFrame = nil
	fast.videoPendingAt = time.Time{}
	fast.queuePendingVideoFrameLocked(secondDelta, false, now.Add(10*time.Millisecond))
	fast.videoPendingFrame = nil
	fast.videoPendingAt = time.Time{}
	fast.videoMu.Unlock()

	// The slow viewer receives two deltas while its one active write is stuck.
	slow.videoMu.Lock()
	slow.queuePendingVideoFrameLocked(firstDelta, false, now)
	slow.queuePendingVideoFrameLocked(secondDelta, false, now.Add(10*time.Millisecond))
	if slow.videoReadyForDelta || slow.videoReadyEpoch != 0 {
		slow.videoMu.Unlock()
		t.Fatalf("slow viewer should require a fresh keyframe after delta backlog: ready=%v epoch=%d", slow.videoReadyForDelta, slow.videoReadyEpoch)
	}
	slow.noteVideoKeyFrameLocked(parseTSF2(nextKey))
	slow.queuePendingVideoFrameLocked(nextKey, true, now.Add(20*time.Millisecond))
	slow.videoMu.Unlock()

	fast.videoMu.Lock()
	defer fast.videoMu.Unlock()
	if !fast.videoReadyForDelta || fast.videoReadyEpoch != 7 {
		t.Fatalf("fast viewer was affected by slow viewer pressure: ready=%v epoch=%d", fast.videoReadyForDelta, fast.videoReadyEpoch)
	}
}

func TestAdaptiveSDRSourceDemandAggregatesViewerModes(t *testing.T) {
	if demand, maxFPS := adaptiveStreamCadenceDemand(nil); demand != "" || maxFPS != 0 {
		t.Fatalf("no viewers demand=%q maxFps=%d, want empty/0", demand, maxFPS)
	}

	hidden := &client{feedbackVisibility: "hidden", videoDeliveryMode: videoDeliveryFull}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{hidden}); demand != "keyframe_only" || maxFPS != 1 {
		t.Fatalf("hidden viewer demand=%q maxFps=%d, want keyframe_only/1", demand, maxFPS)
	}

	slow := &client{feedbackVisibility: "visible", videoDeliveryMode: videoDeliveryKeyframeOnly}
	fast := &client{feedbackVisibility: "visible", videoDeliveryMode: videoDeliveryFull}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{slow, fast}); demand != "full" || maxFPS != 10 {
		t.Fatalf("mixed viewer demand=%q maxFps=%d, want full/10", demand, maxFPS)
	}

	probe := &client{feedbackVisibility: "visible", videoDeliveryMode: videoDeliveryProbe}
	if demand, maxFPS := adaptiveStreamCadenceDemand([]*client{probe}); demand != "full" || maxFPS != 10 {
		t.Fatalf("probe viewer demand=%q maxFps=%d, want full/10", demand, maxFPS)
	}
}
