package web

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/phone"
)

const (
	allIntraSDRCodec               = "avc1.42C028"
	allIntraSDRTransport           = "hardware-h264-annexb"
	allIntraSDRWidth               = 994
	allIntraSDRHeight              = 2046
	allIntraSDRSourceWidth         = 1080
	allIntraSDRSourceHeight        = 2424
	allIntraSDRSourceTopCrop       = 200
	allIntraSDRSourceVisibleHeight = 2224
	allIntraSDRBitrate             = 8_000_000
	allIntraSDRFeedbackVersion     = 1
	allIntraSDRColorStandard       = "bt709_limited_sdr"
	allIntraSDRColorTransfer       = "sdr"
	allIntraSDRColorMatrix         = "bt709"
)

var allIntraSDRConfigFixture = []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":994,"height":2046,"rootCapture":true,"sourceWidth":1080,"sourceHeight":2424,"sourceTopCrop":200,"sourceVisibleHeight":2224,"bitrate":8000000,"fps":1,"feedbackVersion":1,"sourceFps":1,"keyframeIntervalFrames":1,"frameDependencyMode":"all_intra","colorStandard":"bt709_limited_sdr","colorTransfer":"sdr","colorMatrix":"bt709","streamEpoch":7,"phoneUptimeMillis":10000}`)

var invalidAllIntraSDRConfigFixture = []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","width":994,"height":2046,"rootCapture":true,"sourceWidth":1080,"sourceHeight":2424,"sourceTopCrop":200,"sourceVisibleHeight":2224,"bitrate":8000000,"fps":10,"feedbackVersion":1,"sourceFps":10,"keyframeIntervalFrames":10,"frameDependencyMode":"all_intra","colorStandard":"bt709_limited_sdr","colorTransfer":"sdr","colorMatrix":"bt709","streamEpoch":8,"phoneUptimeMillis":11000}`)

type allIntraSDRConfig struct {
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
	FrameDependencyMode    string `json:"frameDependencyMode"`
	ColorStandard          string `json:"colorStandard"`
	ColorTransfer          string `json:"colorTransfer"`
	ColorMatrix            string `json:"colorMatrix"`
	StreamEpoch            uint64 `json:"streamEpoch"`
	PhoneUptimeMillis      int64  `json:"phoneUptimeMillis"`
}

func decodeAllIntraSDRConfig(t *testing.T, raw []byte) allIntraSDRConfig {
	t.Helper()
	var config allIntraSDRConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("decode all-intra SDR config: %v", err)
	}
	return config
}

func TestAllIntraSDRPictureAndCadenceContract(t *testing.T) {
	config := decodeAllIntraSDRConfig(t, allIntraSDRConfigFixture)
	if config.Type != "config" || !config.RootCapture ||
		config.Codec != allIntraSDRCodec || config.Transport != allIntraSDRTransport ||
		config.Width != allIntraSDRWidth || config.Height != allIntraSDRHeight ||
		config.SourceWidth != allIntraSDRSourceWidth || config.SourceHeight != allIntraSDRSourceHeight ||
		config.SourceTopCrop != allIntraSDRSourceTopCrop || config.SourceVisibleHeight != allIntraSDRSourceVisibleHeight ||
		config.Bitrate != allIntraSDRBitrate {
		t.Fatalf("unexpected fixed picture contract: %+v", config)
	}
	if config.FrameDependencyMode != frameDependencyModeAllIntra || config.FPS != 1 || config.SourceFPS != 1 || config.KeyframeIntervalFrames != 1 {
		t.Fatalf("unexpected fixed all-intra cadence: %+v", config)
	}
	if config.FeedbackVersion != allIntraSDRFeedbackVersion ||
		config.ColorStandard != allIntraSDRColorStandard || config.ColorTransfer != allIntraSDRColorTransfer || config.ColorMatrix != allIntraSDRColorMatrix {
		t.Fatalf("unexpected SDR signaling: %+v", config)
	}
}

func TestAllIntraBrowserConfigForwardingPreservesExactContract(t *testing.T) {
	forwarded := browserVideoConfigMessage(allIntraSDRConfigFixture)
	config := decodeAllIntraSDRConfig(t, forwarded)
	if config.FrameDependencyMode != frameDependencyModeAllIntra || config.FPS != 1 || config.SourceFPS != 1 || config.KeyframeIntervalFrames != 1 {
		t.Fatalf("relay changed the 1/1/1 all-intra tuple: %+v", config)
	}
	var fields map[string]any
	if err := json.Unmarshal(forwarded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"serverVersion", "assetVersion", "feedbackVersion"} {
		if _, ok := fields[field]; !ok {
			t.Fatalf("forwarded config missing %s: %#v", field, fields)
		}
	}
}

func TestAllIntraSourceConfigIsStrictAndFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"missing_mode", []byte(`{"type":"config","fps":1,"sourceFps":1,"keyframeIntervalFrames":1,"streamEpoch":7}`)},
		{"unknown_mode", []byte(`{"type":"config","frameDependencyMode":"gop","fps":1,"sourceFps":1,"keyframeIntervalFrames":1,"streamEpoch":7}`)},
		{"wrong_tuple", invalidAllIntraSDRConfigFixture},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hub := newDirectStreamHub()
			if hub.setConfig(browserVideoConfigMessage(tc.raw)) {
				t.Fatal("invalid source config was accepted")
			}
			if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, true, 10_000)) {
				t.Fatal("frame escaped an invalid source config")
			}
			status := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
			if status["allIntraConfigValid"] != false || status["streamVerdict"] != "invalid_source_config" || status["allIntraConfigMismatchCount"] != uint64(1) {
				t.Fatalf("strict rejection missing from health: %#v", status)
			}
		})
	}
}

func TestInvalidAllIntraConfigNeverReachesViewer(t *testing.T) {
	viewer := &client{}
	server := &Server{direct: newDirectStreamHub(), clients: map[*client]struct{}{viewer: {}}}
	if !server.handlePhoneText(invalidAllIntraSDRConfigFixture) {
		t.Fatal("phone config message was not handled")
	}
	viewer.videoMu.Lock()
	queued := len(viewer.controlQueue)
	ready := viewer.videoBroadcastReady
	viewer.videoMu.Unlock()
	if queued != 0 || ready {
		t.Fatalf("invalid source config reached browser: controls=%d ready=%t", queued, ready)
	}
}

func TestAllIntraRelayRejectsUnexpectedDelta(t *testing.T) {
	hub := newDirectStreamHub()
	if !hub.setConfig(allIntraSDRConfigFixture) {
		t.Fatal("valid all-intra config was rejected")
	}
	hub.recordPhoneClock(10_000, time.Now())
	if hub.recordFrame(testTSF2FrameWithTimestamp(7, 1, false, 10_000)) {
		t.Fatal("unexpected delta was forwarded")
	}
	status := hub.snapshot(time.Now(), phone.Health{Connected: true, Desired: true, Viewers: 1, StreamState: "streaming"})
	if status["droppedUnexpectedDeltaFrames"] != uint64(1) || status["deltaFramesForwarded"] != uint64(0) {
		t.Fatalf("unexpected delta did not fail closed: %#v", status)
	}
}

func TestAdaptiveCadencePublisherWasRemoved(t *testing.T) {
	if _, err := os.Stat("adaptive_delivery.go"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adaptive delivery file still exists or could not be checked: %v", err)
	}
	for _, name := range []string{"server.go", "video_writer.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"publishAdaptiveStreamCadence", "adaptiveStreamCadenceDemand", "videoDeliveryProbe", "videoDeliveryKeyframeOnly"} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("%s retains dormant adaptive symbol %q", name, forbidden)
			}
		}
	}
}

func TestStreamFeedbackWireContractRemainsAdvisoryOnly(t *testing.T) {
	raw := []byte(`{"type":"stream_feedback","version":1,"epoch":7,"receivedSequence":1234,"decodedSequence":1232,"renderedSequence":1231,"renderedKeyframeSequence":1225,"decoderQueueSize":2,"renderedVisualAgeMillis":140,"visibility":"visible"}`)
	feedback, ok := decodeStreamFeedback(raw)
	if !ok || feedback.RenderedKeyframeSequence != 1225 || feedback.DecoderQueueSize != 2 || feedback.RenderedVisualAgeMillis != 140 {
		t.Fatalf("rolling feedback contract was not accepted: %+v ok=%t", feedback, ok)
	}
	if _, ok := decodeStreamFeedback([]byte(`{"type":"stream_feedback","version":1,"epoch":7,"decoderQueue":1}`)); ok {
		t.Fatal("renamed or incomplete feedback schema was accepted")
	}
}

func TestAllIntraBrowserSourceKeepsSafetyMarkers(t *testing.T) {
	source, err := os.ReadFile("../../web-client/ticket-app-source.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"stream_feedback", "renderedKeyframeSequence", "frameDependencyMode", "all_intra",
		"all_intra_delta_rejected", "all_intra_config_rejected", "decoderQueueSize", "renderedVisualAgeMillis",
	} {
		if !strings.Contains(string(source), marker) {
			t.Fatalf("browser source is missing all-intra safety marker %q", marker)
		}
	}
}
