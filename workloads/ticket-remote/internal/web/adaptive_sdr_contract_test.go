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
	allIntraSDRCaptureMode         = "root_hardware_h264"
	allIntraSDRCaptureSource       = "root_display_capture"
	allIntraSDRCaptureMethod       = "app_process_mediacodec_surface_secure_screen_capture"
	allIntraSDRWidth               = 994
	allIntraSDRHeight              = 2046
	allIntraSDRSourceWidth         = 1080
	allIntraSDRSourceHeight        = 2424
	allIntraSDRSourceLeftCrop      = 4
	allIntraSDRSourceTopCrop       = 200
	allIntraSDRSourceRightCrop     = 3
	allIntraSDRSourceBottomCrop    = 3
	allIntraSDRSourceVisibleWidth  = 1073
	allIntraSDRSourceVisibleHeight = 2221
	allIntraSDRBitrate             = 8_000_000
	allIntraSDRFeedbackVersion     = 1
	allIntraSDRQualityProfile      = "hardware_h264_crisp_all_intra_1fps"
	allIntraSDRColorCorrection     = "red_blue_swap_high_brightness_sdr_gpu_paint_r1.08_g1.05_b1.03"
	allIntraSDRColorStandard       = "bt709_limited_sdr"
)

var allIntraSDRConfigFixture = []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","captureMode":"root_hardware_h264","captureSource":"root_display_capture","captureMethod":"app_process_mediacodec_surface_secure_screen_capture","width":994,"height":2046,"rootCapture":true,"sourceWidth":1080,"sourceHeight":2424,"sourceLeftCrop":4,"sourceTopCrop":200,"sourceRightCrop":3,"sourceBottomCrop":3,"sourceVisibleWidth":1073,"sourceVisibleHeight":2221,"bitrate":8000000,"qualityProfile":"hardware_h264_crisp_all_intra_1fps","fps":1,"feedbackVersion":1,"sourceFps":1,"keyframeIntervalFrames":1,"frameDependencyMode":"all_intra","colorCorrection":"red_blue_swap_high_brightness_sdr_gpu_paint_r1.08_g1.05_b1.03","colorStandard":"bt709_limited_sdr","streamEpoch":7,"phoneUptimeMillis":10000}`)

var invalidAllIntraSDRConfigFixture = []byte(`{"type":"config","codec":"avc1.42C028","transport":"hardware-h264-annexb","captureMode":"root_hardware_h264","captureSource":"root_display_capture","captureMethod":"app_process_mediacodec_surface_secure_screen_capture","width":994,"height":2046,"rootCapture":true,"sourceWidth":1080,"sourceHeight":2424,"sourceLeftCrop":4,"sourceTopCrop":200,"sourceRightCrop":3,"sourceBottomCrop":3,"sourceVisibleWidth":1073,"sourceVisibleHeight":2221,"bitrate":8000000,"qualityProfile":"hardware_h264_crisp_all_intra_1fps","fps":10,"feedbackVersion":1,"sourceFps":10,"keyframeIntervalFrames":10,"frameDependencyMode":"all_intra","colorCorrection":"red_blue_swap_high_brightness_sdr_gpu_paint_r1.08_g1.05_b1.03","colorStandard":"bt709_limited_sdr","streamEpoch":8,"phoneUptimeMillis":11000}`)

type allIntraSDRConfig struct {
	Type                   string `json:"type"`
	Codec                  string `json:"codec"`
	Transport              string `json:"transport"`
	CaptureMode            string `json:"captureMode"`
	CaptureSource          string `json:"captureSource"`
	CaptureMethod          string `json:"captureMethod"`
	Width                  int    `json:"width"`
	Height                 int    `json:"height"`
	RootCapture            bool   `json:"rootCapture"`
	SourceWidth            int    `json:"sourceWidth"`
	SourceHeight           int    `json:"sourceHeight"`
	SourceLeftCrop         int    `json:"sourceLeftCrop"`
	SourceTopCrop          int    `json:"sourceTopCrop"`
	SourceRightCrop        int    `json:"sourceRightCrop"`
	SourceBottomCrop       int    `json:"sourceBottomCrop"`
	SourceVisibleWidth     int    `json:"sourceVisibleWidth"`
	SourceVisibleHeight    int    `json:"sourceVisibleHeight"`
	Bitrate                int    `json:"bitrate"`
	QualityProfile         string `json:"qualityProfile"`
	ColorCorrection        string `json:"colorCorrection"`
	FPS                    int    `json:"fps"`
	FeedbackVersion        int    `json:"feedbackVersion"`
	SourceFPS              int    `json:"sourceFps"`
	KeyframeIntervalFrames int    `json:"keyframeIntervalFrames"`
	FrameDependencyMode    string `json:"frameDependencyMode"`
	ColorStandard          string `json:"colorStandard"`
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

func allIntraSDRConfigWith(t *testing.T, field string, value any) []byte {
	t.Helper()
	var config map[string]any
	if err := json.Unmarshal(allIntraSDRConfigFixture, &config); err != nil {
		t.Fatal(err)
	}
	if value == nil {
		delete(config, field)
	} else {
		config[field] = value
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAllIntraSDRPictureAndCadenceContract(t *testing.T) {
	config := decodeAllIntraSDRConfig(t, allIntraSDRConfigFixture)
	if config.Type != "config" || !config.RootCapture ||
		config.Codec != allIntraSDRCodec || config.Transport != allIntraSDRTransport ||
		config.CaptureMode != allIntraSDRCaptureMode ||
		config.CaptureSource != allIntraSDRCaptureSource || config.CaptureMethod != allIntraSDRCaptureMethod ||
		config.Width != allIntraSDRWidth || config.Height != allIntraSDRHeight ||
		config.SourceWidth != allIntraSDRSourceWidth || config.SourceHeight != allIntraSDRSourceHeight ||
		config.SourceLeftCrop != allIntraSDRSourceLeftCrop || config.SourceTopCrop != allIntraSDRSourceTopCrop ||
		config.SourceRightCrop != allIntraSDRSourceRightCrop || config.SourceBottomCrop != allIntraSDRSourceBottomCrop ||
		config.SourceVisibleWidth != allIntraSDRSourceVisibleWidth || config.SourceVisibleHeight != allIntraSDRSourceVisibleHeight ||
		config.Bitrate != allIntraSDRBitrate || config.QualityProfile != allIntraSDRQualityProfile ||
		config.ColorCorrection != allIntraSDRColorCorrection {
		t.Fatalf("unexpected fixed picture contract: %+v", config)
	}
	if config.FrameDependencyMode != frameDependencyModeAllIntra || config.FPS != 1 || config.SourceFPS != 1 || config.KeyframeIntervalFrames != 1 {
		t.Fatalf("unexpected fixed all-intra cadence: %+v", config)
	}
	if config.FeedbackVersion != allIntraSDRFeedbackVersion || config.ColorStandard != allIntraSDRColorStandard {
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
	t.Run("accept_implicit_tsf2", func(t *testing.T) {
		if !newDirectStreamHub().setConfig(allIntraSDRConfigFixture) {
			t.Fatal("canonical source config without frameEnvelope was not retained as TSF2 compatibility")
		}
	})

	for _, envelope := range []string{frameEnvelopeTSF2, frameEnvelopeTSF3} {
		t.Run("accept_"+envelope, func(t *testing.T) {
			raw := allIntraSDRConfigWith(t, "frameEnvelope", envelope)
			if !newDirectStreamHub().setConfig(raw) {
				t.Fatalf("canonical %s source config was rejected", envelope)
			}
		})
	}

	tests := []struct {
		name string
		raw  []byte
	}{
		{"missing_mode", []byte(`{"type":"config","fps":1,"sourceFps":1,"keyframeIntervalFrames":1,"streamEpoch":7}`)},
		{"unknown_mode", []byte(`{"type":"config","frameDependencyMode":"gop","fps":1,"sourceFps":1,"keyframeIntervalFrames":1,"streamEpoch":7}`)},
		{"wrong_tuple", invalidAllIntraSDRConfigFixture},
		{"missing_codec", allIntraSDRConfigWith(t, "codec", nil)},
		{"wrong_codec", allIntraSDRConfigWith(t, "codec", "avc1.42E01E")},
		{"missing_transport", allIntraSDRConfigWith(t, "transport", nil)},
		{"wrong_transport", allIntraSDRConfigWith(t, "transport", "h264-annexb")},
		{"wrong_capture_mode", allIntraSDRConfigWith(t, "captureMode", "software_h264")},
		{"missing_capture_mode", allIntraSDRConfigWith(t, "captureMode", nil)},
		{"missing_capture_source", allIntraSDRConfigWith(t, "captureSource", nil)},
		{"wrong_capture_source", allIntraSDRConfigWith(t, "captureSource", "media_projection")},
		{"missing_capture_method", allIntraSDRConfigWith(t, "captureMethod", nil)},
		{"wrong_capture_method", allIntraSDRConfigWith(t, "captureMethod", "software_capture")},
		{"missing_root_capture", allIntraSDRConfigWith(t, "rootCapture", nil)},
		{"not_root_capture", allIntraSDRConfigWith(t, "rootCapture", false)},
		{"missing_width", allIntraSDRConfigWith(t, "width", nil)},
		{"wrong_width", allIntraSDRConfigWith(t, "width", 992)},
		{"missing_height", allIntraSDRConfigWith(t, "height", nil)},
		{"wrong_height", allIntraSDRConfigWith(t, "height", 2044)},
		{"missing_source_width", allIntraSDRConfigWith(t, "sourceWidth", nil)},
		{"wrong_source_width", allIntraSDRConfigWith(t, "sourceWidth", 1079)},
		{"missing_source_height", allIntraSDRConfigWith(t, "sourceHeight", nil)},
		{"wrong_source_height", allIntraSDRConfigWith(t, "sourceHeight", 2423)},
		{"missing_source_left_crop", allIntraSDRConfigWith(t, "sourceLeftCrop", nil)},
		{"wrong_source_left_crop", allIntraSDRConfigWith(t, "sourceLeftCrop", 3)},
		{"missing_source_top_crop", allIntraSDRConfigWith(t, "sourceTopCrop", nil)},
		{"wrong_source_top_crop", allIntraSDRConfigWith(t, "sourceTopCrop", 199)},
		{"missing_source_right_crop", allIntraSDRConfigWith(t, "sourceRightCrop", nil)},
		{"wrong_source_right_crop", allIntraSDRConfigWith(t, "sourceRightCrop", 2)},
		{"missing_source_bottom_crop", allIntraSDRConfigWith(t, "sourceBottomCrop", nil)},
		{"wrong_source_bottom_crop", allIntraSDRConfigWith(t, "sourceBottomCrop", 2)},
		{"missing_source_visible_width", allIntraSDRConfigWith(t, "sourceVisibleWidth", nil)},
		{"wrong_source_visible_width", allIntraSDRConfigWith(t, "sourceVisibleWidth", 1072)},
		{"missing_source_visible_height", allIntraSDRConfigWith(t, "sourceVisibleHeight", nil)},
		{"wrong_source_visible_height", allIntraSDRConfigWith(t, "sourceVisibleHeight", 2220)},
		{"missing_bitrate", allIntraSDRConfigWith(t, "bitrate", nil)},
		{"wrong_bitrate", allIntraSDRConfigWith(t, "bitrate", 7_999_999)},
		{"missing_quality_profile", allIntraSDRConfigWith(t, "qualityProfile", nil)},
		{"wrong_quality_profile", allIntraSDRConfigWith(t, "qualityProfile", "hardware_h264_crisp")},
		{"missing_color_correction", allIntraSDRConfigWith(t, "colorCorrection", nil)},
		{"wrong_color_correction", allIntraSDRConfigWith(t, "colorCorrection", "none")},
		{"missing_color_standard", allIntraSDRConfigWith(t, "colorStandard", nil)},
		{"wrong_color_standard", allIntraSDRConfigWith(t, "colorStandard", "bt709")},
		{"missing_frame_dependency_mode", allIntraSDRConfigWith(t, "frameDependencyMode", nil)},
		{"wrong_frame_dependency_mode", allIntraSDRConfigWith(t, "frameDependencyMode", "gop")},
		{"missing_fps", allIntraSDRConfigWith(t, "fps", nil)},
		{"wrong_fps", allIntraSDRConfigWith(t, "fps", 2)},
		{"missing_source_fps", allIntraSDRConfigWith(t, "sourceFps", nil)},
		{"wrong_source_fps", allIntraSDRConfigWith(t, "sourceFps", 2)},
		{"missing_keyframe_interval", allIntraSDRConfigWith(t, "keyframeIntervalFrames", nil)},
		{"wrong_keyframe_interval", allIntraSDRConfigWith(t, "keyframeIntervalFrames", 2)},
		{"missing_stream_epoch", allIntraSDRConfigWith(t, "streamEpoch", nil)},
		{"unsupported_envelope", allIntraSDRConfigWith(t, "frameEnvelope", "tsf4")},
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
