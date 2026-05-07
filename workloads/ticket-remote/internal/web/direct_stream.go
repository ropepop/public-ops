package web

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"ticketremote/internal/phone"
)

type directStreamHub struct {
	mu sync.Mutex

	activeVideoClients int
	videoConnections   uint64
	phoneReconnects    uint64

	codec       string
	transport   string
	width       int
	height      int
	rootCapture bool

	lastConfig []byte

	framesForwarded    uint64
	keyframesForwarded uint64
	lastConfigAt       time.Time
	lastFrameAt        time.Time
	lastKeyFrameAt     time.Time
	lastVideoClientAt  time.Time
	lastFrame          []byte
	lastKeyFrame       []byte

	lastBrowserMediaError string
	lastBrowserEvent      clientTelemetryEvent
	recentBrowserEvents   []clientTelemetryEvent
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

func (h *directStreamHub) setConfig(raw []byte) {
	var payload struct {
		Type        string `json:"type"`
		Codec       string `json:"codec"`
		Transport   string `json:"transport"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		RootCapture bool   `json:"rootCapture"`
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
	h.framesForwarded++
	h.lastFrameAt = now
	h.lastFrame = append(h.lastFrame[:0], frame...)
	h.lastBrowserMediaError = ""
	if frameIsKeyframe(frame) {
		h.keyframesForwarded++
		h.lastKeyFrameAt = now
		h.lastKeyFrame = append(h.lastKeyFrame[:0], frame...)
	}
}

func (h *directStreamHub) warmStart() (config []byte, keyFrame []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.lastConfig) > 0 {
		config = append([]byte(nil), h.lastConfig...)
	}
	if len(h.lastKeyFrame) > 0 {
		keyFrame = append([]byte(nil), h.lastKeyFrame...)
	}
	return config, keyFrame
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
	if strings.Contains(event, "media") || strings.Contains(event, "video") || strings.Contains(event, "h264") || strings.Contains(event, "decode") || strings.Contains(event, "decoder") {
		h.lastBrowserMediaError = detail
	}
	h.recentBrowserEvents = append(h.recentBrowserEvents, telemetry)
	if len(h.recentBrowserEvents) > 12 {
		h.recentBrowserEvents = append([]clientTelemetryEvent(nil), h.recentBrowserEvents[len(h.recentBrowserEvents)-12:]...)
	}
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
		"activeVideoClients":       h.activeVideoClients,
		"videoConnections":         h.videoConnections,
		"phoneReconnects":          h.phoneReconnects,
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
		"phoneConnected":        phoneHealth.Connected,
		"phoneDesired":          phoneHealth.Desired,
		"phoneStreamState":      phoneHealth.StreamState,
		"phoneViewers":          phoneHealth.Viewers,
		"phoneLastError":        phoneHealth.LastError,
	}
}

func (h *directStreamHub) streamVerdictLocked(now time.Time, phoneHealth phone.Health) string {
	frameAge := ageSinceMillis(now, h.lastFrameAt)
	keyFrameAge := ageSinceMillis(now, h.lastKeyFrameAt)
	hasMediaError := strings.TrimSpace(h.lastBrowserMediaError) != ""
	switch {
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
	if len(frame) >= 5 && frame[0] == 'T' && frame[1] == 'S' && frame[2] == 'F' && frame[3] == '2' {
		return frame[4]&1 == 1
	}
	return len(frame) > 0 && frame[0] == 1
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
