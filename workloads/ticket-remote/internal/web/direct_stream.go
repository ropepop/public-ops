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
	liveFrameMaxAge             = 2000 * time.Millisecond
	liveFreshMaxAge             = 1000 * time.Millisecond
	liveOKMaxAge                = 1500 * time.Millisecond
	warmStartFrameFreshness     = 750 * time.Millisecond
	warmStartKeyFreshness       = 750 * time.Millisecond
	bridgeForwardFrameMaxAge    = 750 * time.Millisecond
	phoneClockCalibrationMaxAge = 5 * time.Second
	phoneClockFutureTolerance   = 250 * time.Millisecond
	phoneClockFutureAdjustMax   = 2 * time.Second
	phoneClockUncertainty       = 100 * time.Millisecond
	streamStartupGrace          = 8 * time.Second
	latestFrameMode             = "live_latest_after_keyframe"
	tsf2HeaderBytes             = 29
	tsf2Magic                   = uint32(0x54534632)
	tsf2FlagKeyframe            = 1
	freshnessLiveFresh          = "LIVE_FRESH"
	freshnessLiveOK             = "LIVE_OK"
	freshnessDegraded           = "DEGRADED"
	freshnessStale              = "STALE"
)

var (
	nonMediaErrorBrowserEvents = map[string]struct{}{
		"decoder_error":                     {},
		"h264_decoder_mode":                 {},
		"h264_decoder_recovery_avc_adapter": {},
		"h264_decoder_recovery_reset":       {},
		"h264_server_recover_requested":     {},
		"server_stale_frames":               {},
		"stale_video_frames":                {},
		"video_stream_restart":              {},
		"websocket_error":                   {},
	}
	browserMediaProblemPrefixes = []string{"decoder_", "h264_", "invalid_tsf2_"}
	browserMediaProblemMarkers  = []string{"error", "failed", "unsupported", "invalid", "empty_frame", "timeout"}
)

type directStreamHub struct {
	mu sync.Mutex

	activeVideoClients int
	videoConnections   uint64
	phoneReconnects    uint64
	phoneStartTimeouts uint64

	codec       string
	transport   string
	width       int
	height      int
	rootCapture bool
	streamEpoch uint64

	lastConfig []byte

	framesForwarded             uint64
	keyframesForwarded          uint64
	deltaFramesForwarded        uint64
	sourceFramesReceived        uint64
	droppedStaleFrames          uint64
	droppedInvalidFrames        uint64
	droppedWrongEpochFrames     uint64
	droppedUncalibratedFrames   uint64
	droppedForwardAgeFrames     uint64
	droppedTimestampFrames      uint64
	droppedFutureClockFrames    uint64
	lastConfigAt                time.Time
	lastFrameAt                 time.Time
	lastKeyFrameAt              time.Time
	lastVideoClientAt           time.Time
	lastFrameEpoch              uint64
	lastKeyFrameEpoch           uint64
	lastFrameSequence           uint64
	lastKeyFrameSequence        uint64
	lastFrame                   []byte
	lastKeyFrame                []byte
	lastFrameVisualAgeMillis    int64
	lastKeyFrameVisualAgeMillis int64
	lastFrameVisualAgeKnown     bool
	lastKeyFrameVisualAgeKnown  bool

	lastPhoneUptimeMillis       int64
	lastPhoneClockBridgeAt      time.Time
	lastPhoneClockCalibrationAt time.Time
	phoneClockCalibrations      uint64
	phoneClockFutureAdjustments uint64
	lastFutureClockAdjustmentAt time.Time

	lastBrowserMediaError string
	lastBrowserEvent      clientTelemetryEvent
	recentBrowserEvents   []clientTelemetryEvent
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
	ok        bool
	keyFrame  bool
	epoch     uint64
	sequence  uint64
	timestamp uint64
}

type clientTelemetryEvent struct {
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

func (h *directStreamHub) setConfig(raw []byte) {
	var payload struct {
		Type              string `json:"type"`
		Codec             string `json:"codec"`
		Transport         string `json:"transport"`
		Width             int    `json:"width"`
		Height            int    `json:"height"`
		RootCapture       bool   `json:"rootCapture"`
		StreamEpoch       uint64 `json:"streamEpoch"`
		PhoneUptimeMillis int64  `json:"phoneUptimeMillis"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Type != "config" {
		return
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.codec = payload.Codec
	h.transport = payload.Transport
	h.width = payload.Width
	h.height = payload.Height
	h.rootCapture = payload.RootCapture
	h.streamEpoch = payload.StreamEpoch
	h.lastConfig = append(h.lastConfig[:0], raw...)
	h.lastConfigAt = now
	if payload.PhoneUptimeMillis > 0 {
		h.recordPhoneClockLocked(payload.PhoneUptimeMillis, now)
	}
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
	h.lastPhoneUptimeMillis = phoneUptimeMillis
	h.lastPhoneClockBridgeAt = receivedAt
	h.lastPhoneClockCalibrationAt = receivedAt
	h.phoneClockCalibrations++
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
	keyFrame := frameIsKeyframe(frame)
	h.sourceFramesReceived++
	if !meta.ok {
		h.dropFrameLocked("invalid")
		return nil, false
	}
	if meta.ok && h.streamEpoch != 0 && meta.epoch != h.streamEpoch {
		h.dropFrameLocked("wrong_epoch")
		return nil, false
	}
	captureWall, visualAgeMillis, dropReason, ok := h.estimateFrameCaptureWallLocked(meta, now)
	if !ok {
		h.dropFrameLocked(dropReason)
		return nil, false
	}
	if time.Duration(visualAgeMillis)*time.Millisecond > bridgeForwardFrameMaxAge {
		h.dropFrameLocked("forward_age")
		return nil, false
	}
	h.lastPhoneClockCalibrationAt = now
	forwarded := rewriteTSF2Timestamp(frame, uint64(captureWall.UnixMicro()))
	h.framesForwarded++
	h.lastFrameAt = now
	h.lastFrameEpoch = meta.epoch
	h.lastFrameSequence = meta.sequence
	h.lastFrameVisualAgeMillis = visualAgeMillis
	h.lastFrameVisualAgeKnown = true
	h.lastFrame = append(h.lastFrame[:0], forwarded...)
	if keyFrame {
		h.keyframesForwarded++
		h.lastKeyFrameAt = now
		h.lastKeyFrameEpoch = meta.epoch
		h.lastKeyFrameSequence = meta.sequence
		h.lastKeyFrameVisualAgeMillis = visualAgeMillis
		h.lastKeyFrameVisualAgeKnown = true
		h.lastKeyFrame = append(h.lastKeyFrame[:0], forwarded...)
	} else {
		h.deltaFramesForwarded++
	}
	h.lastBrowserMediaError = ""
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
	return append([]byte(nil), h.lastConfig...), append([]byte(nil), h.lastKeyFrame...)
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
	if h.streamEpoch == 0 || len(h.lastConfig) == 0 || h.lastFrameAt.IsZero() {
		return false
	}
	if h.lastFrameEpoch != 0 && h.lastFrameEpoch != h.streamEpoch {
		return false
	}
	frameVisualAgeMillis, frameVisualAgeKnown := h.currentFrameVisualAgeMillisLocked(now)
	keyFrameVisualAgeMillis, keyFrameVisualAgeKnown := h.currentKeyFrameVisualAgeMillisLocked(now)
	return now.Sub(h.lastFrameAt) <= warmStartFrameFreshness &&
		frameVisualAgeKnown &&
		time.Duration(frameVisualAgeMillis)*time.Millisecond <= warmStartFrameFreshness &&
		len(h.lastKeyFrame) > 0 &&
		h.lastKeyFrameEpoch == h.streamEpoch &&
		now.Sub(h.lastKeyFrameAt) <= warmStartKeyFreshness &&
		keyFrameVisualAgeKnown &&
		time.Duration(keyFrameVisualAgeMillis)*time.Millisecond <= warmStartKeyFreshness
}

func (h *directStreamHub) estimateFrameCaptureWallLocked(meta tsf2Metadata, now time.Time) (time.Time, int64, string, bool) {
	const maxInt64 = uint64(1<<63 - 1)
	if !meta.ok || meta.timestamp == 0 || meta.timestamp > maxInt64 {
		return time.Time{}, -1, "timestamp", false
	}
	if h.lastPhoneUptimeMillis <= 0 || h.lastPhoneClockBridgeAt.IsZero() {
		return time.Time{}, -1, "uncalibrated", false
	}
	framePhoneUptimeMicros := int64(meta.timestamp)
	if !h.phoneClockCalibratedLocked(now) && !h.recentAcceptedFrameLocked(now) {
		h.recordPhoneClockLocked(framePhoneUptimeMicros/1000, now)
	}
	calibratedPhoneUptimeMicros := h.lastPhoneUptimeMillis * 1000
	deltaMicros := framePhoneUptimeMicros - calibratedPhoneUptimeMicros
	captureWall := h.lastPhoneClockBridgeAt.Add(time.Duration(deltaMicros) * time.Microsecond)
	age := now.Sub(captureWall)
	if age < -phoneClockFutureTolerance {
		futureSkew := -age
		if futureSkew > phoneClockFutureAdjustMax {
			return time.Time{}, -1, "future_clock", false
		}
		adjustBy := futureSkew + phoneClockUncertainty
		h.lastPhoneClockBridgeAt = h.lastPhoneClockBridgeAt.Add(-adjustBy)
		h.lastPhoneClockCalibrationAt = now
		h.phoneClockFutureAdjustments++
		h.lastFutureClockAdjustmentAt = now
		captureWall = captureWall.Add(-adjustBy)
		age = now.Sub(captureWall)
	}
	if age < 0 {
		age = 0
	}
	return captureWall, int64(age / time.Millisecond), "", true
}

func (h *directStreamHub) phoneClockCalibratedLocked(now time.Time) bool {
	return h.lastPhoneUptimeMillis > 0 &&
		!h.lastPhoneClockBridgeAt.IsZero() &&
		!h.lastPhoneClockCalibrationAt.IsZero() &&
		now.Sub(h.lastPhoneClockCalibrationAt) >= 0 &&
		now.Sub(h.lastPhoneClockCalibrationAt) <= phoneClockCalibrationMaxAge
}

func (h *directStreamHub) recentAcceptedFrameLocked(now time.Time) bool {
	return !h.lastFrameAt.IsZero() &&
		now.Sub(h.lastFrameAt) >= 0 &&
		now.Sub(h.lastFrameAt) <= liveFrameMaxAge
}

func (h *directStreamHub) currentFrameVisualAgeMillisLocked(now time.Time) (int64, bool) {
	return currentVisualAgeMillis(now, h.lastFrameAt, h.lastFrameVisualAgeMillis, h.lastFrameVisualAgeKnown)
}

func (h *directStreamHub) currentKeyFrameVisualAgeMillisLocked(now time.Time) (int64, bool) {
	return currentVisualAgeMillis(now, h.lastKeyFrameAt, h.lastKeyFrameVisualAgeMillis, h.lastKeyFrameVisualAgeKnown)
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

func (h *directStreamHub) needsActiveViewerKeyframe(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.activeVideoClients > 0 && !h.warmKeyFrameAllowedLocked(now)
}

func (h *directStreamHub) startupGraceActive(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.lastVideoClientAt.IsZero() && now.Sub(h.lastVideoClientAt) >= 0 && now.Sub(h.lastVideoClientAt) <= streamStartupGrace
}

func (h *directStreamHub) recordClientTelemetry(event, detail string) {
	event = trimLogField(event, 96)
	detail = trimLogField(detail, 500)
	if event == "" {
		return
	}
	telemetry := clientTelemetryEvent{
		Event:  event,
		Detail: detail,
		At:     time.Now().UTC().Format(time.RFC3339),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastBrowserEvent = telemetry
	if isBrowserMediaProblem(event) {
		h.lastBrowserMediaError = detail
	}
	h.recentBrowserEvents = append(h.recentBrowserEvents, telemetry)
	if len(h.recentBrowserEvents) > 12 {
		h.recentBrowserEvents = append([]clientTelemetryEvent(nil), h.recentBrowserEvents[len(h.recentBrowserEvents)-12:]...)
	}
}

func isBrowserMediaProblem(event string) bool {
	if _, ok := nonMediaErrorBrowserEvents[event]; ok {
		return false
	}
	if event == "direct_video_websocket_error" {
		return true
	}
	for _, prefix := range browserMediaProblemPrefixes {
		if !strings.HasPrefix(event, prefix) {
			continue
		}
		for _, marker := range browserMediaProblemMarkers {
			if strings.Contains(event, marker) {
				return true
			}
		}
	}
	return false
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
	setTelemetryTimeFields(status, now, "lastFutureClockAdjustmentAt", h.lastFutureClockAdjustmentAt)
	setTelemetryTimeFields(status, now, "lastPhoneClockCalibrationAt", h.lastPhoneClockCalibrationAt)
	status["lastPhoneUptimeMillis"] = h.lastPhoneUptimeMillis
	status["codec"] = h.codec
	status["transport"] = h.transport
	status["width"] = h.width
	status["height"] = h.height
	status["rootCapture"] = h.rootCapture
	status["videoConnections"] = h.videoConnections
	status["phoneReconnects"] = h.phoneReconnects
	setTelemetryTimeFields(status, now, "lastConfigAt", h.lastConfigAt)
	setTelemetryTimeFields(status, now, "lastFrameAt", h.lastFrameAt)
	status["lastFrameSequence"] = h.lastFrameSequence
	setTelemetryTimeFields(status, now, "lastKeyFrameAt", h.lastKeyFrameAt)
	status["lastKeyFrameSequence"] = h.lastKeyFrameSequence
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
	status["browserMediaError"] = h.lastBrowserMediaError
	status["lastBrowserEvent"] = h.lastBrowserEvent
	status["recentBrowserEvents"] = append([]clientTelemetryEvent(nil), h.recentBrowserEvents...)
	status["startupTrace"] = h.startupTraceSnapshot(now)
	return status
}

func (h *directStreamHub) streamStatusPayloadLocked(now time.Time, phoneHealth phone.Health) map[string]any {
	verdict := h.streamVerdictLocked(now, phoneHealth)
	frameVisualAgeMillis, frameVisualAgeKnown := h.currentFrameVisualAgeMillisLocked(now)
	keyFrameVisualAgeMillis, keyFrameVisualAgeKnown := h.currentKeyFrameVisualAgeMillisLocked(now)
	freshnessState := freshnessStateForVisualAgeMillis(frameVisualAgeMillis, frameVisualAgeKnown)
	live := verdict == "live" && frameVisualAgeKnown && freshnessState != freshnessStale
	return map[string]any{
		"mode":                            latestFrameMode,
		"streamVerdict":                   verdict,
		"freshnessState":                  freshnessState,
		"live":                            live,
		"liveFrameMaxAgeMillis":           durationMillis(liveFrameMaxAge),
		"liveFreshMaxAgeMillis":           durationMillis(liveFreshMaxAge),
		"liveOKMaxAgeMillis":              durationMillis(liveOKMaxAge),
		"bridgeForwardFrameMaxAgeMillis":  durationMillis(bridgeForwardFrameMaxAge),
		"phoneClockCalibrated":            h.phoneClockCalibratedLocked(now),
		"phoneClockUncertaintyMillis":     durationMillis(phoneClockUncertainty),
		"phoneClockFutureToleranceMillis": durationMillis(phoneClockFutureTolerance),
		"phoneClockFutureAdjustMaxMillis": durationMillis(phoneClockFutureAdjustMax),
		"futureClockAdjustments":          h.phoneClockFutureAdjustments,
		"framesForwarded":                 h.framesForwarded,
		"keyframesForwarded":              h.keyframesForwarded,
		"deltaFramesForwarded":            h.deltaFramesForwarded,
		"sourceFramesReceived":            h.sourceFramesReceived,
		"droppedStaleFrames":              h.droppedStaleFrames,
		"droppedInvalidFrames":            h.droppedInvalidFrames,
		"droppedWrongEpochFrames":         h.droppedWrongEpochFrames,
		"droppedUncalibratedFrames":       h.droppedUncalibratedFrames,
		"droppedForwardAgeFrames":         h.droppedForwardAgeFrames,
		"droppedTimestampFrames":          h.droppedTimestampFrames,
		"droppedFutureClockFrames":        h.droppedFutureClockFrames,
		"dropReasons":                     h.dropReasonsLocked(),
		"lastFrameAgoMillis":              ageSinceMillis(now, h.lastFrameAt),
		"lastKeyFrameAgoMillis":           ageSinceMillis(now, h.lastKeyFrameAt),
		"lastFrameVisualAgeKnown":         frameVisualAgeKnown,
		"lastFrameVisualAgeMillis":        frameVisualAgeMillis,
		"lastKeyFrameVisualAgeKnown":      keyFrameVisualAgeKnown,
		"lastKeyFrameVisualAgeMillis":     keyFrameVisualAgeMillis,
		"lastFrameSequence":               h.lastFrameSequence,
		"lastKeyFrameSequence":            h.lastKeyFrameSequence,
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
		"invalid":      h.droppedInvalidFrames,
		"wrong_epoch":  h.droppedWrongEpochFrames,
		"uncalibrated": h.droppedUncalibratedFrames,
		"forward_age":  h.droppedForwardAgeFrames,
		"timestamp":    h.droppedTimestampFrames,
		"future_clock": h.droppedFutureClockFrames,
	}
}

func (h *directStreamHub) streamVerdictLocked(now time.Time, phoneHealth phone.Health) string {
	frameAge := ageSinceMillis(now, h.lastFrameAt)
	keyFrameAge := ageSinceMillis(now, h.lastKeyFrameAt)
	hasMediaError := strings.TrimSpace(h.lastBrowserMediaError) != ""
	frameVisualAgeMillis, frameVisualAgeKnown := h.currentFrameVisualAgeMillisLocked(now)
	hasFreshVisual := frameVisualAgeKnown &&
		time.Duration(frameVisualAgeMillis)*time.Millisecond <= liveFrameMaxAge
	switch {
	case h.activeVideoClients == 0 && hasFreshVisual && phoneHealth.Desired && phoneHealth.Connected && phoneHealth.StreamState == "streaming":
		return "live"
	case h.activeVideoClients == 0:
		return "idle"
	case hasMediaError:
		return "browser_decode_recovering"
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

func frameIsKeyframe(frame []byte) bool {
	if meta := parseTSF2(frame); meta.ok {
		return meta.keyFrame
	}
	return len(frame) > 0 && frame[0] == 1
}

func parseTSF2(frame []byte) tsf2Metadata {
	if len(frame) < tsf2HeaderBytes || binary.BigEndian.Uint32(frame[0:4]) != tsf2Magic {
		return tsf2Metadata{}
	}
	return tsf2Metadata{
		ok:        true,
		keyFrame:  frame[4]&tsf2FlagKeyframe == tsf2FlagKeyframe,
		epoch:     binary.BigEndian.Uint64(frame[5:13]),
		sequence:  binary.BigEndian.Uint64(frame[13:21]),
		timestamp: binary.BigEndian.Uint64(frame[21:29]),
	}
}

func rewriteTSF2Timestamp(frame []byte, timestamp uint64) []byte {
	out := append([]byte(nil), frame...)
	if len(out) >= tsf2HeaderBytes && binary.BigEndian.Uint32(out[0:4]) == tsf2Magic {
		binary.BigEndian.PutUint64(out[21:29], timestamp)
	}
	return out
}

func chooseLatestPendingVideoFrame(existing []byte, existingKeyFrame bool, next []byte, nextKeyFrame bool) ([]byte, bool) {
	if len(existing) > 0 && existingKeyFrame && !nextKeyFrame {
		return existing, true
	}
	return next, nextKeyFrame
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
	return at.UTC().Format(time.RFC3339)
}

func trimLogField(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
