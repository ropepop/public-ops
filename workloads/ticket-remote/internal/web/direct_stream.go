package web

import (
	"encoding/binary"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"ticketremote/internal/phone"
)

const (
	liveFrameMaxAge                = 3000 * time.Millisecond
	liveFreshMaxAge                = 1250 * time.Millisecond
	liveOKMaxAge                   = 2000 * time.Millisecond
	warmStartFrameFreshness        = 1250 * time.Millisecond
	warmStartKeyFreshness          = 1250 * time.Millisecond
	bridgeForwardFrameMaxAge       = 1250 * time.Millisecond
	phoneClockCalibrationMaxAge    = 5 * time.Second
	phoneClockFutureTolerance      = 250 * time.Millisecond
	phoneClockFutureAdjustMax      = 2 * time.Second
	phoneClockUncertainty          = 100 * time.Millisecond
	phoneClockUncertaintyMax       = 2 * time.Second
	streamStartupGrace             = 8 * time.Second
	latestFrameMode                = "live_latest_all_intra"
	tsf2HeaderBytes                = 29
	tsf2Magic                      = uint32(0x54534632)
	tsf3HeaderBytes                = 93
	tsf3Magic                      = uint32(0x54534633)
	tsf2FlagKeyframe               = 1
	freshnessLiveFresh             = "LIVE_FRESH"
	freshnessLiveOK                = "LIVE_OK"
	freshnessDegraded              = "DEGRADED"
	freshnessStale                 = "STALE"
	frameDependencyModeAllIntra    = "all_intra"
	frameEnvelopeTSF2              = "tsf2"
	frameEnvelopeTSF3              = "tsf3"
	canonicalStreamCodec           = "avc1.42C028"
	canonicalStreamTransport       = "hardware-h264-annexb"
	canonicalStreamCaptureMode     = "root_hardware_h264"
	canonicalStreamCaptureSource   = "root_display_capture"
	canonicalStreamCaptureMethod   = "app_process_mediacodec_surface_secure_screen_capture"
	canonicalStreamWidth           = 994
	canonicalStreamHeight          = 2046
	canonicalStreamSourceWidth     = 1080
	canonicalStreamSourceHeight    = 2424
	canonicalStreamLeftCrop        = 4
	canonicalStreamTopCrop         = 200
	canonicalStreamRightCrop       = 3
	canonicalStreamBottomCrop      = 3
	canonicalStreamVisibleWidth    = 1073
	canonicalStreamVisibleHeight   = 2221
	canonicalStreamBitrate         = 8_000_000
	canonicalStreamQualityProfile  = "hardware_h264_crisp_all_intra_1fps"
	canonicalStreamColorCorrection = "red_blue_swap_high_brightness_sdr_gpu_paint_r1.08_g1.05_b1.03"
	canonicalStreamColorStandard   = "bt709_limited_sdr"
)

type directStreamHub struct {
	mu sync.Mutex

	activeVideoClients int
	videoConnections   uint64
	phoneReconnects    uint64
	phoneStartTimeouts uint64

	codec                  string
	transport              string
	captureMode            string
	width                  int
	height                 int
	rootCapture            bool
	streamEpoch            uint64
	configGeneration       uint64
	frameDependencyMode    string
	fps                    int
	sourceFPS              int
	keyframeIntervalFrames int
	frameEnvelope          string
	allIntraConfigValid    bool

	lastConfig []byte

	framesForwarded              uint64
	keyframesForwarded           uint64
	sourceFramesReceived         uint64
	droppedStaleFrames           uint64
	droppedInvalidFrames         uint64
	droppedWrongEpochFrames      uint64
	droppedUncalibratedFrames    uint64
	droppedForwardAgeFrames      uint64
	droppedTimestampFrames       uint64
	droppedFutureClockFrames     uint64
	droppedUnexpectedDeltaFrames uint64
	droppedNonMonotonicFrames    uint64
	droppedOversizeFrames        uint64
	droppedRegressedConfigs      uint64
	allIntraConfigMismatches     uint64
	droppedAllIntraConfigFrames  uint64
	lastConfigAt                 time.Time
	lastFrameAt                  time.Time
	lastFrameReceivedAt          time.Time
	lastFrameSourceEstimateAt    time.Time
	lastFrameUncertaintyMicros   uint64
	lastVideoClientAt            time.Time
	lastFrameEpoch               uint64
	lastFrameSequence            uint64
	lastAdmittedFrameEpoch       uint64
	lastAdmittedFrameSequence    uint64
	lastFrame                    []byte
	lastFrameVisualAgeMillis     int64
	lastFrameVisualAgeKnown      bool

	lastPhoneUptimeMillis       int64
	lastPhoneClockBridgeAt      time.Time
	lastPhoneClockCalibrationAt time.Time
	lastBoundedPhoneClockAt     time.Time
	phoneClockOffsetMicros      int64
	phoneClockOffsetKnown       bool
	phoneClockBounded           bool
	phoneClockCalibrations      uint64
	phoneClockProbeRejections   uint64
	phoneClockCalibrationGen    uint64
	phoneClockUncertaintyMicros uint64
	phoneClockFutureAdjustments uint64
	lastFutureClockAdjustmentAt time.Time

	lastRelayEvent        relayTelemetryEvent
	recentRelayEvents     []relayTelemetryEvent
	lastPhoneStartError   string
	lastPhoneStartErrorAt time.Time

	lastPixelTicketEvent   pixelTicketEvent
	lastPixelTicketEventAt time.Time
	stalePixelTicketEvents uint64

	warmConfigSends         uint64
	warmKeyFrameSends       uint64
	lastWarmConfigSentAt    time.Time
	lastWarmKeyFrameSentAt  time.Time
	lastWarmStartMissAt     time.Time
	lastKeyframeRequestedAt time.Time
	startupTrace            streamStartupTrace
}

type tsf2Metadata struct {
	ok                    bool
	keyFrame              bool
	version               int
	headerBytes           int
	epoch                 uint64
	sequence              uint64
	timestamp             uint64
	captureAttemptID      uint64
	codecGeneration       uint64
	captureStartMicros    uint64
	captureCompleteMicros uint64
	codecInputMicros      uint64
	codecOutputMicros     uint64
	recordEmissionMicros  uint64
	calibrationGeneration uint64
	uncertaintyMicros     uint64
}

type frameWallTiming struct {
	captureStartMicros    uint64
	captureCompleteMicros uint64
	codecInputMicros      uint64
	codecOutputMicros     uint64
	recordEmissionMicros  uint64
	calibrationGeneration uint64
	uncertaintyMicros     uint64
}

type relayTelemetryEvent struct {
	Event  string `json:"event"`
	Detail string `json:"detail,omitempty"`
	At     string `json:"at"`
}

type pixelTicketEvent struct {
	Type                   string           `json:"type"`
	EventSeq               int64            `json:"eventSeq,omitempty"`
	TicketState            string           `json:"ticketState"`
	Reason                 string           `json:"reason,omitempty"`
	RequestID              string           `json:"requestId,omitempty"`
	StreamEpoch            int64            `json:"streamEpoch,omitempty"`
	FrameSequence          int64            `json:"frameSequence,omitempty"`
	MinFrameSequence       int64            `json:"minFrameSequence,omitempty"`
	ResultProof            string           `json:"resultProof,omitempty"`
	ResultFrameEpoch       int64            `json:"resultFrameEpoch,omitempty"`
	ResultMinFrameSequence int64            `json:"resultMinFrameSequence,omitempty"`
	ResultProofAt          string           `json:"resultProofAt,omitempty"`
	PhoneUptimeMillis      int64            `json:"phoneUptimeMillis,omitempty"`
	TotalDurationMillis    int64            `json:"totalDurationMillis,omitempty"`
	Phases                 map[string]int64 `json:"phases,omitempty"`
	At                     string           `json:"at,omitempty"`
}

func newDirectStreamHub() *directStreamHub {
	return &directStreamHub{}
}

func (h *directStreamHub) addVideoClient() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.activeVideoClients++
	h.videoConnections++
	h.lastVideoClientAt = time.Now()
}

func (h *directStreamHub) removeVideoClient() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.activeVideoClients > 0 {
		h.activeVideoClients--
	}
}

func (h *directStreamHub) activeVideoClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activeVideoClients
}

func (h *directStreamHub) currentStreamEpoch() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.allIntraConfigValid {
		return 0
	}
	return h.streamEpoch
}

func (h *directStreamHub) recordPhoneReconnect() {
	h.mu.Lock()
	h.phoneReconnects++
	h.mu.Unlock()
}

func (h *directStreamHub) recordPhoneStartFailure(err error) {
	if err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.phoneStartTimeouts++
	h.lastPhoneStartError = trimLogField(err.Error(), 500)
	h.lastPhoneStartErrorAt = time.Now()
}

func (h *directStreamHub) setConfig(raw []byte) bool {
	var payload struct {
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
		ColorStandard          string `json:"colorStandard"`
		StreamEpoch            uint64 `json:"streamEpoch"`
		PhoneUptimeMillis      int64  `json:"phoneUptimeMillis"`
		FrameEnvelope          string `json:"frameEnvelope"`
		FrameDependencyMode    string `json:"frameDependencyMode"`
		FPS                    int    `json:"fps"`
		SourceFPS              int    `json:"sourceFps"`
		KeyframeIntervalFrames int    `json:"keyframeIntervalFrames"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Type != "config" {
		return false
	}
	frameEnvelope := strings.ToLower(strings.TrimSpace(payload.FrameEnvelope))
	if frameEnvelope == "" {
		frameEnvelope = frameEnvelopeTSF2
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	previousEpoch := h.streamEpoch
	previousEnvelope := h.frameEnvelope
	phoneRestarted := payload.PhoneUptimeMillis > 0 && h.lastPhoneUptimeMillis > 0 && payload.PhoneUptimeMillis < h.lastPhoneUptimeMillis
	if previousEpoch != 0 && payload.StreamEpoch != 0 && payload.StreamEpoch < previousEpoch && !phoneRestarted {
		h.droppedRegressedConfigs++
		return false
	}
	h.codec = payload.Codec
	h.transport = payload.Transport
	h.captureMode = payload.CaptureMode
	h.width = payload.Width
	h.height = payload.Height
	h.rootCapture = payload.RootCapture
	h.streamEpoch = payload.StreamEpoch
	h.frameDependencyMode = strings.ToLower(strings.TrimSpace(payload.FrameDependencyMode))
	h.fps = payload.FPS
	h.sourceFPS = payload.SourceFPS
	h.keyframeIntervalFrames = payload.KeyframeIntervalFrames
	h.frameEnvelope = frameEnvelope
	h.allIntraConfigValid = payload.Codec == canonicalStreamCodec &&
		payload.Transport == canonicalStreamTransport &&
		payload.CaptureMode == canonicalStreamCaptureMode &&
		payload.CaptureSource == canonicalStreamCaptureSource &&
		payload.CaptureMethod == canonicalStreamCaptureMethod && payload.RootCapture &&
		payload.Width == canonicalStreamWidth && payload.Height == canonicalStreamHeight &&
		payload.SourceWidth == canonicalStreamSourceWidth && payload.SourceHeight == canonicalStreamSourceHeight &&
		payload.SourceLeftCrop == canonicalStreamLeftCrop && payload.SourceTopCrop == canonicalStreamTopCrop &&
		payload.SourceRightCrop == canonicalStreamRightCrop && payload.SourceBottomCrop == canonicalStreamBottomCrop &&
		payload.SourceVisibleWidth == canonicalStreamVisibleWidth && payload.SourceVisibleHeight == canonicalStreamVisibleHeight &&
		payload.Bitrate == canonicalStreamBitrate && payload.QualityProfile == canonicalStreamQualityProfile &&
		payload.ColorCorrection == canonicalStreamColorCorrection &&
		payload.ColorStandard == canonicalStreamColorStandard &&
		h.frameDependencyMode == frameDependencyModeAllIntra &&
		payload.FPS == 1 && payload.SourceFPS == 1 && payload.KeyframeIntervalFrames == 1 &&
		payload.StreamEpoch != 0 && (frameEnvelope == frameEnvelopeTSF2 || frameEnvelope == frameEnvelopeTSF3)
	h.configGeneration++
	h.lastConfigAt = now
	if payload.PhoneUptimeMillis > 0 {
		h.recordPhoneClockLocked(payload.PhoneUptimeMillis, now)
	}
	if !h.allIntraConfigValid {
		h.allIntraConfigMismatches++
		h.lastConfig = nil
		h.clearFrameCacheLocked()
		return false
	}
	if previousEpoch != 0 && (payload.StreamEpoch != previousEpoch || frameEnvelope != previousEnvelope) {
		h.clearFrameCacheLocked()
	}
	h.lastConfig = append(h.lastConfig[:0], raw...)
	return true
}

func (h *directStreamHub) clearFrameCacheLocked() {
	h.lastFrame = nil
	h.lastFrameAt = time.Time{}
	h.lastFrameReceivedAt = time.Time{}
	h.lastFrameSourceEstimateAt = time.Time{}
	h.lastFrameUncertaintyMicros = 0
	h.lastFrameEpoch = 0
	h.lastFrameSequence = 0
	h.lastFrameVisualAgeMillis = 0
	h.lastFrameVisualAgeKnown = false
}

func (h *directStreamHub) recordPhoneClock(phoneUptimeMillis int64, receivedAt time.Time) {
	if phoneUptimeMillis <= 0 {
		return
	}
	if receivedAt.IsZero() {
		receivedAt = time.Now()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordPhoneClockLocked(phoneUptimeMillis, receivedAt)
}

func (h *directStreamHub) recordPhoneClockLocked(phoneUptimeMillis int64, receivedAt time.Time) {
	if phoneUptimeMillis <= 0 || receivedAt.IsZero() {
		return
	}
	if phoneUptimeMillis > (1<<63-1)/1000 {
		return
	}
	phoneUptimeMicros := phoneUptimeMillis * 1000
	receivedMicros := receivedAt.UnixMicro()
	// Android monotonic uptime must be smaller than a current UTC Unix value.
	// Keeping the offset positive also makes all later additions overflow-safe.
	if receivedMicros <= 0 || phoneUptimeMicros >= receivedMicros {
		return
	}
	sampleOffsetMicros := receivedMicros - phoneUptimeMicros
	mappingMissing := !h.phoneClockOffsetKnown || h.lastPhoneUptimeMillis <= 0 || h.lastPhoneClockBridgeAt.IsZero()
	phoneRestarted := !mappingMissing && phoneUptimeMillis < h.lastPhoneUptimeMillis
	if phoneRestarted {
		h.phoneClockBounded = false
		h.lastBoundedPhoneClockAt = time.Time{}
		h.phoneClockOffsetKnown = false
		mappingMissing = true
	}
	if h.phoneClockBounded && !phoneRestarted {
		// One-way config/health samples remain useful for restart detection and
		// TSF2 continuity, but transport delay cannot improve or prolong the
		// bounded four-timestamp authority used by TSF3.
		if phoneUptimeMillis >= h.lastPhoneUptimeMillis {
			h.lastPhoneUptimeMillis = phoneUptimeMillis
			h.lastPhoneClockBridgeAt = time.UnixMicro(h.phoneClockOffsetMicros + phoneUptimeMicros)
		}
		h.lastPhoneClockCalibrationAt = receivedAt
		h.phoneClockCalibrations++
		return
	}
	currentOffsetMicros := int64(0)
	if !mappingMissing {
		currentOffsetMicros = h.phoneClockOffsetMicros
	}
	if mappingMissing || phoneRestarted || sampleOffsetMicros < currentOffsetMicros {
		h.phoneClockOffsetMicros = sampleOffsetMicros
		h.phoneClockOffsetKnown = true
		h.lastPhoneClockBridgeAt = time.UnixMicro(sampleOffsetMicros + phoneUptimeMicros)
		h.phoneClockCalibrationGen++
		h.phoneClockUncertaintyMicros = uint64(phoneClockUncertainty / time.Microsecond)
	} else {
		// A slower sample is evidence of transport delay, not permission to move
		// phone time forward. Preserve the best (smallest) observed offset and
		// carry the bounded spread as uncertainty.
		delaySpread := sampleOffsetMicros - currentOffsetMicros
		uncertainty := time.Duration(delaySpread) * time.Microsecond
		if uncertainty < phoneClockUncertainty {
			uncertainty = phoneClockUncertainty
		}
		if uncertainty > phoneClockUncertaintyMax {
			uncertainty = phoneClockUncertaintyMax
		}
		h.lastPhoneClockBridgeAt = time.UnixMicro(currentOffsetMicros + phoneUptimeMicros)
		h.phoneClockUncertaintyMicros = uint64(uncertainty / time.Microsecond)
	}
	h.lastPhoneUptimeMillis = phoneUptimeMillis
	h.lastPhoneClockCalibrationAt = receivedAt
	h.phoneClockCalibrations++
}

func (h *directStreamHub) recordBoundedPhoneClock(probe phone.ClockProbeResult) bool {
	offsetMicros, uncertaintyMicros, ok := boundedPhoneClockMapping(probe)
	if !ok || uncertaintyMicros > uint64(phoneClockUncertaintyMax/time.Microsecond) {
		h.mu.Lock()
		h.phoneClockProbeRejections++
		h.mu.Unlock()
		return false
	}
	phoneUptimeMillis := probe.PhoneSendUptimeMicros / 1000
	if phoneUptimeMillis <= 0 || offsetMicros > 1<<63-1-probe.PhoneSendUptimeMicros {
		h.mu.Lock()
		h.phoneClockProbeRejections++
		h.mu.Unlock()
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	phoneRestarted := h.lastPhoneUptimeMillis > 0 && phoneUptimeMillis < h.lastPhoneUptimeMillis
	if phoneRestarted {
		h.phoneClockBounded = false
		h.lastBoundedPhoneClockAt = time.Time{}
	}
	h.phoneClockOffsetMicros = offsetMicros
	h.phoneClockOffsetKnown = true
	h.phoneClockBounded = true
	h.lastPhoneUptimeMillis = phoneUptimeMillis
	h.lastPhoneClockBridgeAt = time.UnixMicro(offsetMicros + probe.PhoneSendUptimeMicros)
	h.lastPhoneClockCalibrationAt = time.UnixMicro(probe.ServerReceiveUnixMicros)
	h.lastBoundedPhoneClockAt = h.lastPhoneClockCalibrationAt
	h.phoneClockUncertaintyMicros = uncertaintyMicros
	h.phoneClockCalibrationGen++
	h.phoneClockCalibrations++
	return true
}

func boundedPhoneClockMapping(probe phone.ClockProbeResult) (int64, uint64, bool) {
	if probe.ServerSendUnixMicros <= 0 || probe.ServerReceiveUnixMicros < probe.ServerSendUnixMicros ||
		probe.PhoneReceiveUptimeMicros <= 0 || probe.PhoneSendUptimeMicros < probe.PhoneReceiveUptimeMicros {
		return 0, 0, false
	}
	lower := probe.ServerSendUnixMicros - probe.PhoneReceiveUptimeMicros
	upper := probe.ServerReceiveUnixMicros - probe.PhoneSendUptimeMicros
	// Android uptime is necessarily below contemporary Unix time. Requiring a
	// positive interval also makes midpoint arithmetic overflow-safe.
	if lower <= 0 || upper < lower {
		return 0, 0, false
	}
	width := uint64(upper - lower)
	uncertainty := (width + 1) / 2
	midpoint := lower + int64(width/2)
	return midpoint, uncertainty, true
}

func (h *directStreamHub) recordFrame(frame []byte) bool {
	_, ok := h.recordFrameForBroadcast(frame)
	return ok
}

func (h *directStreamHub) recordFrameForBroadcast(frame []byte) ([]byte, bool) {
	if len(frame) == 0 {
		return nil, false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := parseTSF2(frame)
	h.sourceFramesReceived++
	if !meta.ok {
		h.dropFrameLocked("invalid")
		return nil, false
	}
	if len(frame)-meta.headerBytes > int(phone.MaxVideoPayloadBytes) {
		h.dropFrameLocked("oversize")
		return nil, false
	}
	if meta.ok && h.streamEpoch != 0 && meta.epoch != h.streamEpoch {
		h.dropFrameLocked("wrong_epoch")
		return nil, false
	}
	if !h.allIntraConfigValid {
		h.dropFrameLocked("all_intra_config_mismatch")
		return nil, false
	}
	expectedVersion := 2
	if h.frameEnvelope == frameEnvelopeTSF3 {
		expectedVersion = 3
	}
	if meta.version != expectedVersion || meta.epoch == 0 || meta.sequence == 0 || len(frame) <= meta.headerBytes {
		h.dropFrameLocked("invalid")
		return nil, false
	}
	if !meta.keyFrame {
		h.dropFrameLocked("unexpected_delta")
		return nil, false
	}
	if h.lastAdmittedFrameEpoch == meta.epoch && meta.sequence <= h.lastAdmittedFrameSequence {
		h.dropFrameLocked("non_monotonic")
		return nil, false
	}
	timing, visualAgeMillis, dropReason, ok := h.estimateFrameTimingLocked(meta, now)
	if !ok {
		h.dropFrameLocked(dropReason)
		return nil, false
	}
	if time.Duration(visualAgeMillis)*time.Millisecond > bridgeForwardFrameMaxAge {
		h.dropFrameLocked("forward_age")
		return nil, false
	}
	if meta.version == 2 {
		h.lastPhoneClockCalibrationAt = now
	}
	forwarded := rewriteFrameTimestamps(frame, meta, timing)
	h.framesForwarded++
	h.lastFrameReceivedAt = now
	h.lastFrameSourceEstimateAt = time.UnixMicro(int64(timing.captureStartMicros))
	h.lastFrameUncertaintyMicros = timing.uncertaintyMicros
	h.lastFrameAt = h.lastFrameSourceEstimateAt
	if meta.version == 3 {
		h.lastFrameAt = h.lastFrameAt.Add(-time.Duration(timing.uncertaintyMicros) * time.Microsecond)
	}
	h.lastFrameEpoch = meta.epoch
	h.lastFrameSequence = meta.sequence
	h.lastAdmittedFrameEpoch = meta.epoch
	h.lastAdmittedFrameSequence = meta.sequence
	h.lastFrameVisualAgeMillis = visualAgeMillis
	h.lastFrameVisualAgeKnown = true
	h.lastFrame = append(h.lastFrame[:0], forwarded...)
	h.keyframesForwarded++
	return forwarded, true
}

func (h *directStreamHub) dropFrameLocked(reason string) {
	h.droppedStaleFrames++
	switch reason {
	case "invalid":
		h.droppedInvalidFrames++
	case "wrong_epoch":
		h.droppedWrongEpochFrames++
	case "uncalibrated":
		h.droppedUncalibratedFrames++
	case "forward_age":
		h.droppedForwardAgeFrames++
	case "timestamp":
		h.droppedTimestampFrames++
	case "future_clock":
		h.droppedFutureClockFrames++
	case "unexpected_delta":
		h.droppedUnexpectedDeltaFrames++
	case "non_monotonic":
		h.droppedNonMonotonicFrames++
	case "oversize":
		h.droppedOversizeFrames++
	case "all_intra_config_mismatch":
		h.droppedAllIntraConfigFrames++
	}
}

func (h *directStreamHub) warmStart() (config []byte, keyFrame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if h.streamEpoch == 0 || len(h.lastConfig) == 0 {
		return nil, nil
	}
	if !h.warmKeyFrameAllowedLocked(now) {
		return h.provisionalConfigLocked(), nil
	}
	return append([]byte(nil), h.lastConfig...), append([]byte(nil), h.lastFrame...)
}

// warmEncoderReusable proves that prewarm is joining the same live encoder,
// even when its short-lived cached keyframe is already too old to replay. The
// browser still needs a fresh keyframe, but writing another start command first
// only delays that request and can produce a duplicate keyframe burst.
func (h *directStreamHub) warmEncoderReusable(now time.Time, phoneHealth phone.Health) bool {
	if now.IsZero() {
		now = time.Now()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !phoneHealth.Desired || !phoneHealth.Connected || phoneHealth.StreamState != "streaming" {
		return false
	}
	if h.streamEpoch == 0 || h.lastFrameEpoch != h.streamEpoch {
		return false
	}
	return h.streamVerdictLocked(now, phoneHealth) == "live"
}

func (h *directStreamHub) configGenerationSnapshot() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.configGeneration
}

func (h *directStreamHub) warmEncoderConfigReceivedAfter(generation uint64) bool {
	_, ok := h.warmEncoderConfigEpochAfter(generation)
	return ok
}

func (h *directStreamHub) warmEncoderConfigEpochAfter(generation uint64) (uint64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.configGeneration <= generation || h.streamEpoch == 0 || len(h.lastConfig) == 0 {
		return 0, false
	}
	return h.streamEpoch, true
}

func (h *directStreamHub) provisionalConfigLocked() []byte {
	var payload map[string]any
	if err := json.Unmarshal(h.lastConfig, &payload); err != nil {
		return nil
	}
	if payload["type"] != "config" {
		return nil
	}
	payload["streamEpoch"] = 0
	payload["provisional"] = true
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return raw
}

func (h *directStreamHub) recordWarmStartSent(configSent bool, keyFrameSent bool) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if configSent {
		h.warmConfigSends++
		h.lastWarmConfigSentAt = now
	}
	if keyFrameSent {
		h.warmKeyFrameSends++
		h.lastWarmKeyFrameSentAt = now
	}
	if !configSent {
		h.lastWarmStartMissAt = now
	}
}

func (h *directStreamHub) recordKeyframeRequested() {
	h.mu.Lock()
	h.lastKeyframeRequestedAt = time.Now()
	h.mu.Unlock()
}

func (h *directStreamHub) recordPixelTicketEvent(event pixelTicketEvent) bool {
	if strings.TrimSpace(event.TicketState) == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if event.StreamEpoch > 0 && h.lastPixelTicketEvent.StreamEpoch > 0 && event.StreamEpoch != h.lastPixelTicketEvent.StreamEpoch {
		if event.StreamEpoch < h.lastPixelTicketEvent.StreamEpoch {
			h.stalePixelTicketEvents++
			return false
		}
	} else if event.EventSeq > 0 && h.lastPixelTicketEvent.EventSeq > 0 && event.EventSeq <= h.lastPixelTicketEvent.EventSeq {
		h.stalePixelTicketEvents++
		return false
	}
	h.lastPixelTicketEvent = event
	h.lastPixelTicketEventAt = now
	return true
}

func (h *directStreamHub) warmKeyFrameAllowedLocked(now time.Time) bool {
	receivedAt := h.lastFrameReceiptTimeLocked()
	if h.streamEpoch == 0 || len(h.lastConfig) == 0 || receivedAt.IsZero() {
		return false
	}
	if h.lastFrameEpoch != 0 && h.lastFrameEpoch != h.streamEpoch {
		return false
	}
	frameVisualAgeMillis, frameVisualAgeKnown := h.currentFrameVisualAgeMillisLocked(now)
	return now.Sub(receivedAt) <= warmStartFrameFreshness &&
		frameVisualAgeKnown &&
		time.Duration(frameVisualAgeMillis)*time.Millisecond <= warmStartFrameFreshness &&
		len(h.lastFrame) > 0 &&
		h.lastFrameEpoch == h.streamEpoch &&
		time.Duration(frameVisualAgeMillis)*time.Millisecond <= warmStartKeyFreshness
}

func (h *directStreamHub) estimateFrameTimingLocked(meta tsf2Metadata, now time.Time) (frameWallTiming, int64, string, bool) {
	const maxInt64 = uint64(1<<63 - 1)
	if !meta.ok || meta.timestamp == 0 || meta.timestamp > maxInt64 {
		return frameWallTiming{}, -1, "timestamp", false
	}
	if h.lastPhoneUptimeMillis <= 0 || h.lastPhoneClockBridgeAt.IsZero() {
		return frameWallTiming{}, -1, "uncalibrated", false
	}
	if meta.version == 3 && !h.boundedPhoneClockCalibratedLocked(now) {
		return frameWallTiming{}, -1, "uncalibrated", false
	}
	if meta.version == 2 && !h.phoneClockCalibratedLocked(now) && !h.recentAcceptedFrameLocked(now) {
		return frameWallTiming{}, -1, "uncalibrated", false
	}
	referenceTimestamp := meta.timestamp
	if meta.version == 3 {
		if !validTSF3Stages(meta) {
			return frameWallTiming{}, -1, "timestamp", false
		}
		referenceTimestamp = meta.recordEmissionMicros
	}
	referenceWall, ok := h.phoneTimestampWallLocked(referenceTimestamp)
	if !ok {
		return frameWallTiming{}, -1, "timestamp", false
	}
	referenceAge := now.Sub(referenceWall)
	if referenceAge < -phoneClockFutureTolerance {
		if meta.version == 3 {
			boundedTolerance := phoneClockFutureTolerance + time.Duration(h.phoneClockUncertaintyMicros)*time.Microsecond
			if referenceAge < -boundedTolerance {
				return frameWallTiming{}, -1, "future_clock", false
			}
		} else {
			futureSkew := -referenceAge
			if futureSkew > phoneClockFutureAdjustMax {
				return frameWallTiming{}, -1, "future_clock", false
			}
			adjustBy := futureSkew + phoneClockUncertainty
			h.lastPhoneClockBridgeAt = h.lastPhoneClockBridgeAt.Add(-adjustBy)
			h.lastPhoneClockCalibrationAt = now
			h.phoneClockCalibrationGen++
			h.phoneClockUncertaintyMicros = uint64(phoneClockUncertainty / time.Microsecond)
			h.phoneClockFutureAdjustments++
			h.lastFutureClockAdjustmentAt = now
		}
	}
	captureWall, ok := h.phoneTimestampWallLocked(meta.timestamp)
	if !ok {
		return frameWallTiming{}, -1, "timestamp", false
	}
	age := now.Sub(captureWall)
	if age < 0 {
		age = 0
	}
	timing := frameWallTiming{
		captureStartMicros:    uint64(captureWall.UnixMicro()),
		captureCompleteMicros: uint64(captureWall.UnixMicro()),
		codecInputMicros:      uint64(captureWall.UnixMicro()),
		codecOutputMicros:     uint64(captureWall.UnixMicro()),
		recordEmissionMicros:  uint64(captureWall.UnixMicro()),
		calibrationGeneration: h.phoneClockCalibrationGen,
	}
	if meta.version == 3 {
		timing.uncertaintyMicros = h.phoneClockUncertaintyMicros
		stages := []*uint64{
			&timing.captureStartMicros,
			&timing.captureCompleteMicros,
			&timing.codecInputMicros,
			&timing.codecOutputMicros,
			&timing.recordEmissionMicros,
		}
		monotonic := []uint64{
			meta.captureStartMicros,
			meta.captureCompleteMicros,
			meta.codecInputMicros,
			meta.codecOutputMicros,
			meta.recordEmissionMicros,
		}
		for index, timestamp := range monotonic {
			wall, valid := h.phoneTimestampWallLocked(timestamp)
			if !valid || wall.UnixMicro() < 0 {
				return frameWallTiming{}, -1, "timestamp", false
			}
			*stages[index] = uint64(wall.UnixMicro())
		}
		age += time.Duration(timing.uncertaintyMicros) * time.Microsecond
	}
	return timing, int64(age / time.Millisecond), "", true
}

func (h *directStreamHub) phoneTimestampWallLocked(timestamp uint64) (time.Time, bool) {
	if timestamp == 0 || timestamp > uint64(1<<63-1) || h.lastPhoneUptimeMillis <= 0 || h.lastPhoneClockBridgeAt.IsZero() {
		return time.Time{}, false
	}
	offsetMicros := h.phoneClockOffsetMicros
	if !h.phoneClockOffsetKnown {
		calibratedMicros := h.lastPhoneUptimeMillis * 1000
		offsetMicros = h.lastPhoneClockBridgeAt.UnixMicro() - calibratedMicros
	}
	value := int64(timestamp)
	if (offsetMicros > 0 && value > 1<<63-1-offsetMicros) || (offsetMicros < 0 && value < -offsetMicros) {
		return time.Time{}, false
	}
	return time.UnixMicro(value + offsetMicros), true
}

func validTSF3Stages(meta tsf2Metadata) bool {
	return meta.captureAttemptID > 0 && meta.codecGeneration > 0 &&
		meta.captureStartMicros > 0 &&
		meta.captureStartMicros <= meta.captureCompleteMicros &&
		meta.captureCompleteMicros <= meta.codecInputMicros &&
		meta.codecInputMicros <= meta.codecOutputMicros &&
		meta.codecOutputMicros <= meta.recordEmissionMicros
}

func (h *directStreamHub) phoneClockCalibratedLocked(now time.Time) bool {
	return h.lastPhoneUptimeMillis > 0 &&
		!h.lastPhoneClockBridgeAt.IsZero() &&
		!h.lastPhoneClockCalibrationAt.IsZero() &&
		now.Sub(h.lastPhoneClockCalibrationAt) >= 0 &&
		now.Sub(h.lastPhoneClockCalibrationAt) <= phoneClockCalibrationMaxAge
}

func (h *directStreamHub) boundedPhoneClockCalibratedLocked(now time.Time) bool {
	return h.phoneClockBounded && h.phoneClockOffsetKnown &&
		!h.lastBoundedPhoneClockAt.IsZero() &&
		now.Sub(h.lastBoundedPhoneClockAt) >= 0 &&
		now.Sub(h.lastBoundedPhoneClockAt) <= phoneClockCalibrationMaxAge
}

func (h *directStreamHub) recentAcceptedFrameLocked(now time.Time) bool {
	receivedAt := h.lastFrameReceiptTimeLocked()
	return !receivedAt.IsZero() &&
		now.Sub(receivedAt) >= 0 &&
		now.Sub(receivedAt) <= liveFrameMaxAge
}

func (h *directStreamHub) currentFrameVisualAgeMillisLocked(now time.Time) (int64, bool) {
	return currentVisualAgeMillis(now, h.lastFrameReceiptTimeLocked(), h.lastFrameVisualAgeMillis, h.lastFrameVisualAgeKnown)
}

func (h *directStreamHub) lastFrameReceiptTimeLocked() time.Time {
	if !h.lastFrameReceivedAt.IsZero() {
		return h.lastFrameReceivedAt
	}
	// Compatibility for older in-package fixtures that predate separate source
	// and receipt clocks.
	return h.lastFrameAt
}

func (h *directStreamHub) currentKeyFrameVisualAgeMillisLocked(now time.Time) (int64, bool) {
	return h.currentFrameVisualAgeMillisLocked(now)
}

func currentVisualAgeMillis(now time.Time, observedAt time.Time, observedAgeMillis int64, known bool) (int64, bool) {
	if !known || observedAt.IsZero() || observedAgeMillis < 0 {
		return -1, false
	}
	elapsedMillis := int64(now.Sub(observedAt) / time.Millisecond)
	if elapsedMillis < 0 {
		elapsedMillis = 0
	}
	return observedAgeMillis + elapsedMillis, true
}

func (h *directStreamHub) startupGraceActive(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.lastVideoClientAt.IsZero() && now.Sub(h.lastVideoClientAt) >= 0 && now.Sub(h.lastVideoClientAt) <= streamStartupGrace
}

func (h *directStreamHub) recordRelayTelemetry(event, detail string) {
	event = trimLogField(event, 96)
	if event == "" {
		return
	}
	telemetry := relayTelemetryEvent{
		Event: event, Detail: trimLogField(detail, 500), At: time.Now().UTC().Format(time.RFC3339),
	}
	h.mu.Lock()
	h.lastRelayEvent = telemetry
	h.recentRelayEvents = append(h.recentRelayEvents, telemetry)
	if len(h.recentRelayEvents) > 12 {
		h.recentRelayEvents = append([]relayTelemetryEvent(nil), h.recentRelayEvents[len(h.recentRelayEvents)-12:]...)
	}
	h.mu.Unlock()
}

func durationMillis(value time.Duration) int64 {
	return int64(value / time.Millisecond)
}

func setTelemetryTimeFields(status map[string]any, now time.Time, atKey string, atValue time.Time) {
	status[atKey] = timeString(atValue)
	status[atKey+"AgoMillis"] = ageSinceMillis(now, atValue)
}

func (h *directStreamHub) snapshot(now time.Time, phoneHealth phone.Health) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	status := h.streamStatusPayloadLocked(now, phoneHealth)
	status["path"] = "https_websocket_h264"
	status["warmStartFrameMaxAgeMillis"] = durationMillis(warmStartFrameFreshness)
	status["warmStartKeyFrameMaxAgeMillis"] = durationMillis(warmStartKeyFreshness)
	status["phoneClockCalibrationMaxAgeMillis"] = durationMillis(phoneClockCalibrationMaxAge)
	status["phoneClockCalibrationGeneration"] = h.phoneClockCalibrationGen
	status["phoneClockUncertaintyMillis"] = h.phoneClockUncertaintyMicros / 1000
	status["phoneClockBounded"] = h.phoneClockBounded
	status["phoneClockBoundedCalibrated"] = h.boundedPhoneClockCalibratedLocked(now)
	status["phoneClockProbeRejections"] = h.phoneClockProbeRejections
	setTelemetryTimeFields(status, now, "lastFutureClockAdjustmentAt", h.lastFutureClockAdjustmentAt)
	setTelemetryTimeFields(status, now, "lastPhoneClockCalibrationAt", h.lastPhoneClockCalibrationAt)
	setTelemetryTimeFields(status, now, "lastBoundedPhoneClockAt", h.lastBoundedPhoneClockAt)
	status["lastPhoneUptimeMillis"] = h.lastPhoneUptimeMillis
	status["codec"] = h.codec
	status["transport"] = h.transport
	status["captureMode"] = h.captureMode
	status["width"] = h.width
	status["height"] = h.height
	status["rootCapture"] = h.rootCapture
	status["frameDependencyMode"] = h.frameDependencyMode
	status["fps"] = h.fps
	status["sourceFps"] = h.sourceFPS
	status["keyframeIntervalFrames"] = h.keyframeIntervalFrames
	status["frameEnvelope"] = h.frameEnvelope
	status["allIntraConfigAdvertised"] = h.frameDependencyMode == frameDependencyModeAllIntra
	status["allIntraConfigValid"] = h.allIntraConfigValid
	status["allIntraConfigMismatchCount"] = h.allIntraConfigMismatches
	status["regressedConfigDropCount"] = h.droppedRegressedConfigs
	status["videoConnections"] = h.videoConnections
	status["phoneReconnects"] = h.phoneReconnects
	setTelemetryTimeFields(status, now, "lastConfigAt", h.lastConfigAt)
	setTelemetryTimeFields(status, now, "lastFrameAt", h.lastFrameAt)
	setTelemetryTimeFields(status, now, "lastFrameReceivedAt", h.lastFrameReceiptTimeLocked())
	setTelemetryTimeFields(status, now, "lastFrameSourceEstimateAt", h.lastFrameSourceEstimateAt)
	status["lastFrameUncertaintyMillis"] = h.lastFrameUncertaintyMicros / 1000
	status["lastFrameSequence"] = h.lastFrameSequence
	setTelemetryTimeFields(status, now, "lastKeyFrameAt", h.lastFrameAt)
	status["lastKeyFrameSequence"] = h.lastFrameSequence
	setTelemetryTimeFields(status, now, "lastVideoClientAt", h.lastVideoClientAt)
	status["warmConfigSends"] = h.warmConfigSends
	status["warmKeyFrameSends"] = h.warmKeyFrameSends
	setTelemetryTimeFields(status, now, "lastWarmConfigSentAt", h.lastWarmConfigSentAt)
	setTelemetryTimeFields(status, now, "lastWarmKeyFrameSentAt", h.lastWarmKeyFrameSentAt)
	setTelemetryTimeFields(status, now, "lastWarmStartMissAt", h.lastWarmStartMissAt)
	setTelemetryTimeFields(status, now, "lastKeyframeRequestedAt", h.lastKeyframeRequestedAt)
	status["phoneStartError"] = h.lastPhoneStartError
	setTelemetryTimeFields(status, now, "phoneStartErrorAt", h.lastPhoneStartErrorAt)
	status["pixelTicketEventReason"] = h.lastPixelTicketEvent.Reason
	status["pixelTicketEventRequestId"] = ""
	status["pixelTicketEventStreamEpoch"] = h.lastPixelTicketEvent.StreamEpoch
	status["pixelTicketEventFrameSequence"] = h.lastPixelTicketEvent.FrameSequence
	setTelemetryTimeFields(status, now, "pixelTicketEventAt", h.lastPixelTicketEventAt)
	status["stalePixelTicketEvents"] = h.stalePixelTicketEvents
	status["lastRelayEvent"] = h.lastRelayEvent
	status["recentRelayEvents"] = append([]relayTelemetryEvent(nil), h.recentRelayEvents...)
	status["startupTrace"] = h.startupTraceSnapshot(now)
	return status
}

func (h *directStreamHub) streamStatusPayloadLocked(now time.Time, phoneHealth phone.Health) map[string]any {
	verdict := h.streamVerdictLocked(now, phoneHealth)
	frameVisualAgeMillis, frameVisualAgeKnown := h.currentFrameVisualAgeMillisLocked(now)
	keyFrameVisualAgeMillis, keyFrameVisualAgeKnown := h.currentKeyFrameVisualAgeMillisLocked(now)
	freshnessState := freshnessStateForVisualAgeMillis(frameVisualAgeMillis, frameVisualAgeKnown)
	continuity := verdict == "live" && frameVisualAgeKnown && freshnessState != freshnessStale
	// Public live authority is deliberately narrower than visual continuity.
	// LIVE_OK and DEGRADED may preserve the last picture without interruption,
	// but only LIVE_FRESH may authorize an action or claim a current picture.
	live := continuity && freshnessState == freshnessLiveFresh &&
		h.frameEnvelope == frameEnvelopeTSF3 && h.boundedPhoneClockCalibratedLocked(now)
	if verdict == "live" && !live {
		verdict = "stale_recovering"
	}
	return map[string]any{
		"mode":                            latestFrameMode,
		"frameDependencyMode":             h.frameDependencyMode,
		"fps":                             h.fps,
		"sourceFps":                       h.sourceFPS,
		"keyframeIntervalFrames":          h.keyframeIntervalFrames,
		"frameEnvelope":                   h.frameEnvelope,
		"allIntraConfigAdvertised":        h.frameDependencyMode == frameDependencyModeAllIntra,
		"allIntraConfigValid":             h.allIntraConfigValid,
		"allIntraConfigMismatchCount":     h.allIntraConfigMismatches,
		"regressedConfigDropCount":        h.droppedRegressedConfigs,
		"streamVerdict":                   verdict,
		"freshnessState":                  freshnessState,
		"live":                            live,
		"continuity":                      continuity,
		"liveFrameMaxAgeMillis":           durationMillis(liveFrameMaxAge),
		"liveFreshMaxAgeMillis":           durationMillis(liveFreshMaxAge),
		"liveOKMaxAgeMillis":              durationMillis(liveOKMaxAge),
		"bridgeForwardFrameMaxAgeMillis":  durationMillis(bridgeForwardFrameMaxAge),
		"phoneClockCalibrated":            h.phoneClockCalibratedLocked(now),
		"phoneClockBoundedCalibrated":     h.boundedPhoneClockCalibratedLocked(now),
		"phoneClockCalibrationGeneration": h.phoneClockCalibrationGen,
		"phoneClockUncertaintyMillis":     h.phoneClockUncertaintyMicros / 1000,
		"phoneClockFutureToleranceMillis": durationMillis(phoneClockFutureTolerance),
		"phoneClockFutureAdjustMaxMillis": durationMillis(phoneClockFutureAdjustMax),
		"futureClockAdjustments":          h.phoneClockFutureAdjustments,
		"framesForwarded":                 h.framesForwarded,
		"keyframesForwarded":              h.keyframesForwarded,
		"deltaFramesForwarded":            uint64(0),
		"sourceFramesReceived":            h.sourceFramesReceived,
		"droppedStaleFrames":              h.droppedStaleFrames,
		"droppedInvalidFrames":            h.droppedInvalidFrames,
		"droppedWrongEpochFrames":         h.droppedWrongEpochFrames,
		"droppedUncalibratedFrames":       h.droppedUncalibratedFrames,
		"droppedForwardAgeFrames":         h.droppedForwardAgeFrames,
		"droppedTimestampFrames":          h.droppedTimestampFrames,
		"droppedFutureClockFrames":        h.droppedFutureClockFrames,
		"droppedUnexpectedDeltaFrames":    h.droppedUnexpectedDeltaFrames,
		"droppedNonMonotonicFrames":       h.droppedNonMonotonicFrames,
		"droppedOversizeFrames":           h.droppedOversizeFrames,
		"droppedAllIntraConfigFrames":     h.droppedAllIntraConfigFrames,
		"dropReasons":                     h.dropReasonsLocked(),
		"lastFrameAt":                     timeString(h.lastFrameAt),
		"lastFrameReceivedAt":             timeString(h.lastFrameReceiptTimeLocked()),
		"lastFrameSourceEstimateAt":       timeString(h.lastFrameSourceEstimateAt),
		"lastFrameUncertaintyMillis":      h.lastFrameUncertaintyMicros / 1000,
		"lastFrameAgoMillis":              ageSinceMillis(now, h.lastFrameAt),
		"lastKeyFrameAgoMillis":           ageSinceMillis(now, h.lastFrameAt),
		"lastFrameVisualAgeKnown":         frameVisualAgeKnown,
		"lastFrameVisualAgeMillis":        frameVisualAgeMillis,
		"lastKeyFrameVisualAgeKnown":      keyFrameVisualAgeKnown,
		"lastKeyFrameVisualAgeMillis":     keyFrameVisualAgeMillis,
		"lastFrameSequence":               h.lastFrameSequence,
		"lastKeyFrameSequence":            h.lastFrameSequence,
		"activeVideoClients":              h.activeVideoClients,
		"streamEpoch":                     h.streamEpoch,
		"phoneConnected":                  phoneHealth.Connected,
		"phoneDesired":                    phoneHealth.Desired,
		"phoneStreamState":                phoneHealth.StreamState,
		"phoneViewers":                    phoneHealth.Viewers,
		"phoneLastError":                  phoneHealth.LastError,
		"phoneStartTimeouts":              h.phoneStartTimeouts,
		"phoneStartError":                 h.lastPhoneStartError,
		"pixelTicketState":                h.lastPixelTicketEvent.TicketState,
		"pixelTicketEventSeq":             h.lastPixelTicketEvent.EventSeq,
	}
}

func (h *directStreamHub) streamStatus(now time.Time, phoneHealth phone.Health) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	status := h.streamStatusPayloadLocked(now, phoneHealth)
	status["type"] = "stream_status"
	status["serverTime"] = now.UTC().Format(time.RFC3339Nano)
	return status
}

func (h *directStreamHub) dropReasonsLocked() map[string]uint64 {
	return map[string]uint64{
		"invalid":                   h.droppedInvalidFrames,
		"wrong_epoch":               h.droppedWrongEpochFrames,
		"uncalibrated":              h.droppedUncalibratedFrames,
		"forward_age":               h.droppedForwardAgeFrames,
		"timestamp":                 h.droppedTimestampFrames,
		"future_clock":              h.droppedFutureClockFrames,
		"unexpected_delta":          h.droppedUnexpectedDeltaFrames,
		"non_monotonic":             h.droppedNonMonotonicFrames,
		"oversize":                  h.droppedOversizeFrames,
		"all_intra_config_mismatch": h.droppedAllIntraConfigFrames,
	}
}

func (h *directStreamHub) streamVerdictLocked(now time.Time, phoneHealth phone.Health) string {
	frameAge := ageSinceMillis(now, h.lastFrameReceiptTimeLocked())
	keyFrameAge := ageSinceMillis(now, h.lastFrameReceiptTimeLocked())
	frameVisualAgeMillis, frameVisualAgeKnown := h.currentFrameVisualAgeMillisLocked(now)
	hasFreshVisual := h.streamEpoch != 0 && h.lastFrameEpoch == h.streamEpoch && frameVisualAgeKnown &&
		time.Duration(frameVisualAgeMillis)*time.Millisecond <= liveFrameMaxAge
	switch {
	case !h.allIntraConfigValid:
		return "invalid_source_config"
	case h.activeVideoClients == 0 && hasFreshVisual && phoneHealth.Desired && phoneHealth.Connected && phoneHealth.StreamState == "streaming":
		return "live"
	case h.activeVideoClients == 0:
		return "idle"
	case !phoneHealth.Desired || !phoneHealth.Connected:
		return "preparing_phone"
	case hasFreshVisual:
		return "live"
	case frameAge >= 0 && !frameVisualAgeKnown:
		return "timing_uncertain"
	case keyFrameAge < 0:
		return "waiting_keyframe"
	case frameVisualAgeKnown && time.Duration(frameVisualAgeMillis)*time.Millisecond > liveFrameMaxAge:
		return "stale_recovering"
	default:
		return "waiting_keyframe"
	}
}

func freshnessStateForVisualAgeMillis(visualAgeMillis int64, known bool) string {
	if !known || visualAgeMillis < 0 {
		return freshnessStale
	}
	age := time.Duration(visualAgeMillis) * time.Millisecond
	switch {
	case age <= liveFreshMaxAge:
		return freshnessLiveFresh
	case age <= liveOKMaxAge:
		return freshnessLiveOK
	case age <= liveFrameMaxAge:
		return freshnessDegraded
	default:
		return freshnessStale
	}
}

func parseTSF2(frame []byte) tsf2Metadata {
	if len(frame) < 4 {
		return tsf2Metadata{}
	}
	switch binary.BigEndian.Uint32(frame[0:4]) {
	case tsf2Magic:
		if len(frame) < tsf2HeaderBytes {
			return tsf2Metadata{}
		}
		return tsf2Metadata{
			ok:          true,
			keyFrame:    frame[4]&tsf2FlagKeyframe == tsf2FlagKeyframe,
			version:     2,
			headerBytes: tsf2HeaderBytes,
			epoch:       binary.BigEndian.Uint64(frame[5:13]),
			sequence:    binary.BigEndian.Uint64(frame[13:21]),
			timestamp:   binary.BigEndian.Uint64(frame[21:29]),
		}
	case tsf3Magic:
		if len(frame) < tsf3HeaderBytes {
			return tsf2Metadata{}
		}
		return tsf2Metadata{
			ok:                    true,
			keyFrame:              frame[4]&tsf2FlagKeyframe == tsf2FlagKeyframe,
			version:               3,
			headerBytes:           tsf3HeaderBytes,
			epoch:                 binary.BigEndian.Uint64(frame[5:13]),
			sequence:              binary.BigEndian.Uint64(frame[13:21]),
			captureAttemptID:      binary.BigEndian.Uint64(frame[21:29]),
			codecGeneration:       binary.BigEndian.Uint64(frame[29:37]),
			captureStartMicros:    binary.BigEndian.Uint64(frame[37:45]),
			captureCompleteMicros: binary.BigEndian.Uint64(frame[45:53]),
			codecInputMicros:      binary.BigEndian.Uint64(frame[53:61]),
			codecOutputMicros:     binary.BigEndian.Uint64(frame[61:69]),
			recordEmissionMicros:  binary.BigEndian.Uint64(frame[69:77]),
			calibrationGeneration: binary.BigEndian.Uint64(frame[77:85]),
			uncertaintyMicros:     binary.BigEndian.Uint64(frame[85:93]),
			timestamp:             binary.BigEndian.Uint64(frame[37:45]),
		}
	default:
		return tsf2Metadata{}
	}
}

func rewriteFrameTimestamps(frame []byte, meta tsf2Metadata, timing frameWallTiming) []byte {
	out := append([]byte(nil), frame...)
	if meta.version == 2 && len(out) >= tsf2HeaderBytes && binary.BigEndian.Uint32(out[0:4]) == tsf2Magic {
		binary.BigEndian.PutUint64(out[21:29], timing.captureStartMicros)
	}
	if meta.version == 3 && len(out) >= tsf3HeaderBytes && binary.BigEndian.Uint32(out[0:4]) == tsf3Magic {
		binary.BigEndian.PutUint64(out[37:45], timing.captureStartMicros)
		binary.BigEndian.PutUint64(out[45:53], timing.captureCompleteMicros)
		binary.BigEndian.PutUint64(out[53:61], timing.codecInputMicros)
		binary.BigEndian.PutUint64(out[61:69], timing.codecOutputMicros)
		binary.BigEndian.PutUint64(out[69:77], timing.recordEmissionMicros)
		binary.BigEndian.PutUint64(out[77:85], timing.calibrationGeneration)
		binary.BigEndian.PutUint64(out[85:93], timing.uncertaintyMicros)
	}
	return out
}

func ageSinceMillis(now time.Time, at time.Time) int64 {
	if at.IsZero() {
		return -1
	}
	return int64(now.Sub(at) / time.Millisecond)
}

func timeString(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339Nano)
}

func trimLogField(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
