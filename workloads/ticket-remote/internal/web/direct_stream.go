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
	warmStartFrameFreshness = 2500 * time.Millisecond
	warmStartKeyFreshness   = 1500 * time.Millisecond
	tsf2HeaderBytes         = 29
	tsf2Magic               = uint32(0x54534632)
	tsf2FlagKeyframe        = 1
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

	framesForwarded    uint64
	keyframesForwarded uint64
	lastConfigAt       time.Time
	lastFrameAt        time.Time
	lastKeyFrameAt     time.Time
	lastVideoClientAt  time.Time
	lastFrameEpoch     uint64
	lastKeyFrameEpoch  uint64
	lastFrame          []byte
	lastKeyFrame       []byte

	lastBrowserMediaError string
	lastBrowserEvent      clientTelemetryEvent
	recentBrowserEvents   []clientTelemetryEvent
	lastPhoneStartError   string
	lastPhoneStartErrorAt time.Time
}

type tsf2Metadata struct {
	ok       bool
	keyFrame bool
	epoch    uint64
	sequence uint64
}

type clientTelemetryEvent struct {
	Event  string `json:"event"`
	Detail string `json:"detail,omitempty"`
	At     string `json:"at"`
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
		Type        string `json:"type"`
		Codec       string `json:"codec"`
		Transport   string `json:"transport"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		RootCapture bool   `json:"rootCapture"`
		StreamEpoch uint64 `json:"streamEpoch"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Type != "config" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.codec = payload.Codec
	h.transport = payload.Transport
	h.width = payload.Width
	h.height = payload.Height
	h.rootCapture = payload.RootCapture
	h.streamEpoch = payload.StreamEpoch
	h.lastConfig = append(h.lastConfig[:0], raw...)
	h.lastConfigAt = time.Now()
}

func (h *directStreamHub) recordFrame(frame []byte) {
	if len(frame) == 0 {
		return
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	meta := parseTSF2(frame)
	h.framesForwarded++
	h.lastFrameAt = now
	if meta.ok {
		h.lastFrameEpoch = meta.epoch
	}
	h.lastFrame = append(h.lastFrame[:0], frame...)
	h.lastBrowserMediaError = ""
	if frameIsKeyframe(frame) {
		h.keyframesForwarded++
		h.lastKeyFrameAt = now
		if meta.ok {
			h.lastKeyFrameEpoch = meta.epoch
		}
		h.lastKeyFrame = append(h.lastKeyFrame[:0], frame...)
	}
}

func (h *directStreamHub) warmStart() (config []byte, keyFrame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if !h.warmStartAllowedLocked(now) {
		return nil, nil
	}
	if len(h.lastConfig) > 0 {
		config = append([]byte(nil), h.lastConfig...)
	}
	if len(h.lastKeyFrame) > 0 && h.lastKeyFrameEpoch == h.streamEpoch && now.Sub(h.lastKeyFrameAt) <= warmStartKeyFreshness {
		keyFrame = append([]byte(nil), h.lastKeyFrame...)
	}
	return config, keyFrame
}

func (h *directStreamHub) warmStartAllowedLocked(now time.Time) bool {
	if h.streamEpoch == 0 || len(h.lastConfig) == 0 || h.lastFrameAt.IsZero() {
		return false
	}
	if h.lastFrameEpoch != 0 && h.lastFrameEpoch != h.streamEpoch {
		return false
	}
	return now.Sub(h.lastFrameAt) <= warmStartFrameFreshness
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
	switch event {
	case "decoder_error":
		return false
	case "h264_decoder_mode",
		"h264_decoder_recovery_avc_adapter",
		"h264_decoder_recovery_reset",
		"h264_server_recover_requested",
		"server_stale_frames",
		"stale_video_frames",
		"video_stream_restart",
		"websocket_error":
		return false
	}
	if event == "direct_video_websocket_error" {
		return true
	}
	for _, prefix := range []string{"decoder_", "h264_", "invalid_tsf2_"} {
		if !strings.HasPrefix(event, prefix) {
			continue
		}
		for _, marker := range []string{
			"error",
			"failed",
			"unsupported",
			"invalid",
			"empty_frame",
			"timeout",
		} {
			if strings.Contains(event, marker) {
				return true
			}
		}
	}
	return false
}

func (h *directStreamHub) snapshot(now time.Time, phoneHealth phone.Health) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	verdict := h.streamVerdictLocked(now, phoneHealth)
	return map[string]any{
		"path":                     "https_websocket_h264",
		"streamVerdict":            verdict,
		"codec":                    h.codec,
		"transport":                h.transport,
		"width":                    h.width,
		"height":                   h.height,
		"rootCapture":              h.rootCapture,
		"streamEpoch":              h.streamEpoch,
		"activeVideoClients":       h.activeVideoClients,
		"videoConnections":         h.videoConnections,
		"phoneReconnects":          h.phoneReconnects,
		"phoneStartTimeouts":       h.phoneStartTimeouts,
		"framesForwarded":          h.framesForwarded,
		"keyframesForwarded":       h.keyframesForwarded,
		"lastConfigAt":             timeString(h.lastConfigAt),
		"lastConfigAgoMillis":      ageSinceMillis(now, h.lastConfigAt),
		"lastFrameAt":              timeString(h.lastFrameAt),
		"lastFrameAgoMillis":       ageSinceMillis(now, h.lastFrameAt),
		"lastKeyFrameAt":           timeString(h.lastKeyFrameAt),
		"lastKeyFrameAgoMillis":    ageSinceMillis(now, h.lastKeyFrameAt),
		"lastVideoClientAt":        timeString(h.lastVideoClientAt),
		"lastVideoClientAgoMillis": ageSinceMillis(now, h.lastVideoClientAt),
		"phoneConnected":           phoneHealth.Connected,
		"phoneDesired":             phoneHealth.Desired,
		"phoneViewers":             phoneHealth.Viewers,
		"phoneStreamState":         phoneHealth.StreamState,
		"phoneLastError":           phoneHealth.LastError,
		"phoneStartError":          h.lastPhoneStartError,
		"phoneStartErrorAt":        timeString(h.lastPhoneStartErrorAt),
		"phoneStartErrorAgoMillis": ageSinceMillis(now, h.lastPhoneStartErrorAt),
		"browserMediaError":        h.lastBrowserMediaError,
		"lastBrowserEvent":         h.lastBrowserEvent,
		"recentBrowserEvents":      append([]clientTelemetryEvent(nil), h.recentBrowserEvents...),
	}
}

func (h *directStreamHub) streamStatus(now time.Time, phoneHealth phone.Health) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	verdict := h.streamVerdictLocked(now, phoneHealth)
	return map[string]any{
		"type":                  "stream_status",
		"streamVerdict":         verdict,
		"serverTime":            now.UTC().Format(time.RFC3339),
		"framesForwarded":       h.framesForwarded,
		"keyframesForwarded":    h.keyframesForwarded,
		"lastFrameAgoMillis":    ageSinceMillis(now, h.lastFrameAt),
		"lastKeyFrameAgoMillis": ageSinceMillis(now, h.lastKeyFrameAt),
		"activeVideoClients":    h.activeVideoClients,
		"streamEpoch":           h.streamEpoch,
		"phoneConnected":        phoneHealth.Connected,
		"phoneDesired":          phoneHealth.Desired,
		"phoneStreamState":      phoneHealth.StreamState,
		"phoneViewers":          phoneHealth.Viewers,
		"phoneLastError":        phoneHealth.LastError,
		"phoneStartTimeouts":    h.phoneStartTimeouts,
		"phoneStartError":       h.lastPhoneStartError,
	}
}

func (h *directStreamHub) streamVerdictLocked(now time.Time, phoneHealth phone.Health) string {
	frameAge := ageSinceMillis(now, h.lastFrameAt)
	keyFrameAge := ageSinceMillis(now, h.lastKeyFrameAt)
	hasMediaError := strings.TrimSpace(h.lastBrowserMediaError) != ""
	switch {
	case h.activeVideoClients == 0 && frameAge >= 0 && frameAge <= 2500 && phoneHealth.Desired && phoneHealth.Connected && phoneHealth.StreamState == "streaming":
		return "live"
	case h.activeVideoClients == 0:
		return "idle"
	case hasMediaError:
		return "browser_decode_recovering"
	case !phoneHealth.Desired || !phoneHealth.Connected:
		return "preparing_phone"
	case frameAge >= 0 && frameAge <= 2500:
		return "live"
	case keyFrameAge < 0:
		return "waiting_keyframe"
	case frameAge > 2500:
		return "stale_recovering"
	default:
		return "waiting_keyframe"
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
		ok:       true,
		keyFrame: frame[4]&tsf2FlagKeyframe == tsf2FlagKeyframe,
		epoch:    binary.BigEndian.Uint64(frame[5:13]),
		sequence: binary.BigEndian.Uint64(frame[13:21]),
	}
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
