package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"ticketremote/internal/state"
)

const (
	serviceEventBodyLimitBytes = 16 * 1024
	safeEventTextMaxBytes      = 240
	safeEventMaxFields         = 32
	streamStartupTraceTarget   = 5 * time.Second
	streamStartupTraceMaxAge   = 2 * time.Minute
	streamStartupTraceMaxSteps = 32
)

var sensitiveEventText = regexp.MustCompile(`\b\d{2,8}\b|https?://[^\s"']+`)

type productEventInput struct {
	Source        string         `json:"source"`
	Category      string         `json:"category"`
	Action        string         `json:"action"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason"`
	CommandID     string         `json:"commandId"`
	BackendID     string         `json:"backendId"`
	CorrelationID string         `json:"correlationId"`
	SafeState     map[string]any `json:"safeState"`
	Count         int64          `json:"count"`
}

type streamStartupTracePhase struct {
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

type streamStartupTrace struct {
	ID, SessionID     string
	LastPhase         string
	StartedAt, LastAt time.Time
	Complete          bool
	Phases            []streamStartupTracePhase
}

func (s *Server) recordRuntimeEventForSourceAsync(source, level, event, id string, detail map[string]any) {
	if s != nil && s.store != nil {
		go s.recordRuntimeEventForSource(source, level, event, id, detail)
	}
}

func (s *Server) recordRuntimeErrorAsync(event, id string, err error, detail map[string]any) {
	if err == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["error"] = safeRuntimeLogError(err)
	s.recordRuntimeEventForSourceAsync("ticket_remote", "warn", event, id, detail)
}

func (s *Server) recordRuntimeEventForSource(source, level, event, id string, detail map[string]any) {
	if s == nil || s.store == nil {
		return
	}
	source, level = cleanStreamControlText(source, "ticket_remote"), cleanStreamControlText(level, "info")
	event = compactRuntimeEventName(cleanStreamControlText(event, "runtime_event"), level)
	body, err := json.Marshal(safeRuntimeLogDetail(detail))
	if err != nil {
		body = []byte("{}")
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	_ = s.store.AppendSafeOperationalLog(ctx, state.SafeOperationalLogInput{
		ID: state.NewSafeOperationalLogID(source, event, id, now), TicketID: s.cfg.TicketID,
		Source: source, Level: level, Event: event, CorrelationID: cleanStreamControlText(id, ""),
		DetailJSON: state.ClampSafeOperationalLogDetail(string(body)), Now: now,
	})
}

func (s *Server) recordProductEvent(input productEventInput) {
	category, action := cleanStreamControlText(input.Category, "runtime"), cleanStreamControlText(input.Action, "event")
	status := cleanStreamControlText(input.Status, "ok")
	detail := map[string]any{"category": category, "action": action, "status": status}
	for key, value := range input.SafeState {
		detail["state_"+cleanStreamControlText(key, "field")] = value
	}
	detail["reason"], detail["backendId"], detail["count"] =
		sensitiveEventText.ReplaceAllString(safeRuntimeLogText(input.Reason), "[redacted]"),
		cleanStreamControlText(input.BackendID, ""), input.Count
	level := "info"
	if eventFailed(action, status) {
		level = "warn"
	}
	s.recordRuntimeEventForSource(cleanStreamControlText(input.Source, "ticket_remote"), level,
		category+"_"+action, firstNonEmpty(input.CorrelationID, input.CommandID), detail)
}
func (s *Server) handleServiceEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	expected := strings.TrimSpace(s.cfg.ServiceEvents.Token)
	provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if expected == "" {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "service_events_disabled"})
		return
	}
	if len(expected) != len(provided) || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, serviceEventBodyLimitBytes))
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "forbidden"})
		return
	}
	var input productEventInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, serviceEventBodyLimitBytes)).Decode(&input); err != nil || strings.TrimSpace(input.Source) == "" || strings.TrimSpace(input.Category) == "" || strings.TrimSpace(input.Action) == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request"})
		return
	}
	s.recordProductEvent(input)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handlePixelTraceEvent(msg map[string]any) bool {
	if strings.TrimSpace(stringFromAny(msg["type"])) != "ticket_trace_event" {
		return false
	}
	detail := make(map[string]any, 24)
	for _, key := range []string{
		"detail", "streamState", "sessionState", "streamActive", "captureMode", "videoClients",
		"frameSequence", "sentFrames", "lastFreshFrameAgeMillis", "phoneUptimeMillis",
		"hardwareH264State", "hardwareH264Active", "hardwareH264Available", "hardwareH264Restarts",
		"hardwareH264LastExitReason", "hardwareH264LastFrameAgeMillis", "hardwareH264HelperState",
		"hardwareH264Visibility", "lastStreamRecoveryResult", "lastStreamRecoveryFailureReason",
		"lastStreamRecoveryAgeMillis", "streamWatchdogStage", "lastStreamWatchdogReason",
		"lastVideoClientAgeMillis", "timestampMillis",
	} {
		if value, ok := msg[key]; ok {
			detail[key] = value
		}
	}
	s.recordRuntimeEventForSourceAsync("pixel", cleanStreamControlText(stringFromAny(msg["level"]), "info"),
		cleanStreamControlText(stringFromAny(msg["event"]), "pixel_event"),
		firstNonEmpty(stringFromAny(msg["correlationId"]), stringFromAny(msg["traceId"])), detail)
	return true
}

func compactRuntimeEventName(event, level string) string {
	switch event {
	case "video_socket_open", "video_socket_opened", "relay_viewer_added":
		return "stream_opened"
	case "video_socket_closed", "video_socket_closed_intentional", "relay_viewer_removed", "viewer_idle_disconnected", "stream_desired_state_idle_released":
		return "stream_closed"
	case "keyframe_request", "stream_keyframe_command_queued", "keyframe_while_phone_disconnected", "h264_first_frame_nudge":
		return "keyframe_requested"
	case "stream_desired_state_publish_ok":
		return "stream_changed"
	}
	if strings.Contains(event, "keyframe") {
		if eventFailed(event, level) {
			return "keyframe_failed"
		}
		return "keyframe_requested"
	}
	if strings.Contains(event, "recover") {
		if eventFailed(event, level) {
			return "stream_failed"
		}
		return "stream_recovery_requested"
	}
	return event
}

func eventFailed(event, level string) bool {
	return level == "warn" || level == "failed" || level == "error" ||
		strings.Contains(event, "failed") || strings.Contains(event, "timeout") || strings.Contains(event, "error")
}

func safeRuntimeLogDetail(input map[string]any) map[string]any {
	out := make(map[string]any, min(len(input), safeEventMaxFields))
	for key, value := range input {
		if len(out) == safeEventMaxFields {
			break
		}
		key = cleanStreamControlText(key, "field")
		switch key {
		case "token", "password", "digits", "value", "image", "image_base64", "payload_json", "detail_json", "row":
			continue
		}
		switch typed := value.(type) {
		case nil, bool, int, int32, int64, uint, uint32, uint64, float32, float64, json.Number:
			out[key] = typed
		case string:
			out[key] = safeRuntimeLogText(typed)
		case error:
			out[key] = safeRuntimeLogError(typed)
		default:
			out[key] = "present"
		}
	}
	return out
}

func safeRuntimeLogError(err error) string { return safeRuntimeLogText(publicHealthError(err)) }

func safeRuntimeLogText(value string) string {
	return trimLogField(strings.Join(strings.Fields(value), " "), safeEventTextMaxBytes)
}

func safeRuntimeTraceID(prefix, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	hash := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s_%x", cleanStreamControlText(prefix, "trace"), hash[:4])
}

func (h *directStreamHub) beginStartupTrace(sessionID, reason string) string {
	now := time.Now()
	sessionID = safeRuntimeTraceID("session", sessionID)
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID != "" && !trace.Complete && now.Sub(trace.StartedAt) <= streamStartupTraceMaxAge &&
		(sessionID == "" || trace.SessionID == "" || sessionID == trace.SessionID) {
		h.addStartupTracePhaseLocked(now, "startup_trace_joined", reason, false)
		return trace.ID
	}
	*trace = streamStartupTrace{
		ID:        "stream:" + now.UTC().Format("20060102T150405.000000000Z") + ":" + randomID(),
		SessionID: trimLogField(sessionID, 96), StartedAt: now, LastAt: now,
	}
	h.addStartupTracePhaseLocked(now, "startup_trace_started", reason, false)
	return trace.ID
}

func (h *directStreamHub) recordStartupPhase(name, detail string)     { h.tracePhase(name, detail, 0) }
func (h *directStreamHub) recordStartupPhaseOnce(name, detail string) { h.tracePhase(name, detail, 1) }
func (h *directStreamHub) completeStartupTrace(name, detail string)   { h.tracePhase(name, detail, 3) }

// tracePhase modes: 0 records, 1 deduplicates, 3 deduplicates and completes.
func (h *directStreamHub) tracePhase(name, detail string, mode uint8) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID == "" || trace.Complete || now.Sub(trace.StartedAt) > streamStartupTraceMaxAge {
		return
	}
	if h.addStartupTracePhaseLocked(now, name, detail, mode&1 != 0) && mode&2 != 0 {
		trace.Complete = true
	}
}

func (h *directStreamHub) addStartupTracePhaseLocked(now time.Time, name, detail string, once bool) bool {
	trace := &h.startupTrace
	name = trimLogField(name, 96)
	if trace.ID == "" || name == "" {
		return false
	}
	if once && slices.ContainsFunc(trace.Phases, func(phase streamStartupTracePhase) bool {
		return phase.Name == name
	}) {
		return false
	}
	trace.Phases = append(trace.Phases, streamStartupTracePhase{
		Name: name, Detail: trimLogField(detail, 240),
	})
	if len(trace.Phases) > streamStartupTraceMaxSteps {
		trace.Phases = trace.Phases[len(trace.Phases)-streamStartupTraceMaxSteps:]
	}
	trace.LastAt = now
	trace.LastPhase = name
	return true
}

func (h *directStreamHub) startupTraceSnapshot(_ time.Time) map[string]any {
	trace := h.startupTrace
	if trace.ID == "" {
		return nil
	}
	elapsed := max(trace.LastAt.Sub(trace.StartedAt), 0)
	return map[string]any{
		"id": trace.ID, "startedAt": timeString(trace.StartedAt),
		"elapsedMillis": durationMillis(elapsed),
		"targetMillis":  durationMillis(streamStartupTraceTarget), "overBudget": elapsed > streamStartupTraceTarget,
		"complete": trace.Complete, "lastPhase": trace.LastPhase,
		"phases": append([]streamStartupTracePhase(nil), trace.Phases...),
	}
}
