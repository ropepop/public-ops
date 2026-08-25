package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ticketremote/internal/state"
)

const (
	serviceEventBodyLimitBytes = 16 * 1024
	safeEventTextMaxBytes      = 240
	safeEventMaxFields         = 32
	browserClientLogBodyBytes  = 4 * 1024
	auditWriteTimeout          = 750 * time.Millisecond
	streamStartupTraceTarget   = 5 * time.Second
	streamStartupTraceMaxAge   = 2 * time.Minute
	streamStartupTraceMaxSteps = 32
)

var (
	sensitiveEventText   = regexp.MustCompile(`(?i)https?://[^\s"']+|\b(?:bearer|token|password|secret|cookie|authorization|prompt)\b|\b\d{2,}\b`)
	sensitiveClientToken = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
	sensitiveEmail       = regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`)
	sensitiveIPAddress   = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	sensitivePrivatePath = regexp.MustCompile(`(?i)(?:/Users/|/home/|/root/|[A-Z]:\\Users\\)[^\s"']*`)

	// The browser compacts its raw diagnostics to this fixed vocabulary before
	// sending. Exact admission prevents a client from minting unique event names
	// to evade central minute-bucket sampling.
	browserClientLogEvents = map[string]struct{}{
		"blocked_gesture":                       {},
		"browser_opened":                        {},
		"browser_configured":                    {},
		"browser_first_frame_decoded":           {},
		"canvas_context_unavailable":            {},
		"control_code_browser_capture_ack_sent": {},
		"control_code_candidate_accepted":       {},
		"control_code_candidate_rejected":       {},
		"control_code_capturing":                {},
		"control_code_close_local_only":         {},
		"control_code_decoder_backlog_reset":    {},
		"control_code_failed":                   {},
		"control_code_frame_displayed":          {},
		"control_code_frame_frozen":             {},
		"control_code_frame_painted":            {},
		"control_code_frame_retry_requested":    {},
		"control_code_ignored":                  {},
		"control_code_marker_frame_waiting":     {},
		"control_code_marker_received":          {},
		"control_code_requested":                {},
		"control_code_sent":                     {},
		"decoded_frame_render_failed":           {},
		"decoder_decode_failed":                 {},
		"decoder_error":                         {},
		"decoder_transient_hidden":              {},
		"direct_video_websocket_error":          {},
		"early_video_connecting_grace":          {},
		"early_video_connecting_grace_skipped":  {},
		"fullscreen_failed":                     {},
		"fullscreen_requested":                  {},
		"fullscreen_unavailable":                {},
		"h264_avc_adapter_empty_frame":          {},
		"h264_avc_config_failed":                {},
		"h264_decoder_mode":                     {},
		"h264_unsupported":                      {},
		"invalid_tsf2_frame":                    {},
		"keyframe_failed":                       {},
		"keyframe_requested":                    {},
		"missing_ticket_dom":                    {},
		"runtime_error":                         {},
		"screen_engaged":                        {},
		"state_changed":                         {},
		"state_failed":                          {},
		"stream_closed":                         {},
		"stream_failed":                         {},
		"stream_first_rendered_frame":           {},
		"stream_focus_failed":                   {},
		"stream_frame_preserve_failed":          {},
		"stream_frame_restore_failed":           {},
		"stream_opened":                         {},
		"stream_recovery_requested":             {},
		"stream_recovered":                      {},
		"stream_stalled":                        {},
		"stream_started":                        {},
		"stream_vertical_scroll":                {},
		"unhandled_rejection":                   {},
		"video_message_failed":                  {},
		"video_socket_create_failed":            {},
		"viewer_idle_visible_keepalive":         {},
		"wake_lock_acquired":                    {},
		"wake_lock_failed":                      {},
		"wake_lock_release_failed":              {},
		"wake_lock_released":                    {},
		"wake_lock_unavailable":                 {},
		"websocket_create_failed":               {},
		"websocket_unavailable":                 {},
	}
)

type browserClientLogInput struct {
	Type   string `json:"type"`
	Event  string `json:"event"`
	Detail string `json:"detail"`
}

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
	Name                      string    `json:"name"`
	Detail                    string    `json:"detail,omitempty"`
	At                        time.Time `json:"at"`
	ElapsedMillis             int64     `json:"elapsedMillis"`
	SourceAtEpochMillis       *int64    `json:"sourceAtEpochMillis,omitempty"`
	SourceAtPerformanceMillis *float64  `json:"sourceAtPerformanceMillis,omitempty"`
}

type streamStartupTraceSourceTime struct {
	epochMillis       int64
	performanceMillis float64
}

type streamStartupTrace struct {
	ID, SessionID     string
	RunOrigin         string
	Reason            string
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

func (s *Server) recordAuditAsync(ticketID, actor, event string, payload map[string]any, now time.Time) {
	if s == nil || s.store == nil {
		return
	}
	// Sanitize and copy before handing the payload to a goroutine. This both
	// avoids caller mutation races and keeps Audit on the same privacy contract
	// as every other central operational event.
	safePayload := safeRuntimeLogDetail(payload)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), auditWriteTimeout)
		defer cancel()
		if err := s.store.Audit(ctx, ticketID, actor, event, safePayload, now); err != nil {
			s.recordRuntimeErrorAsync("audit_failed", event, err, map[string]any{"event": event})
		}
	}()
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
		redactOperationalLogText(input.Reason),
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
		key = safeRuntimeLogKey(key)
		if runtimeLogDetailKeyIsSensitive(key) {
			continue
		}
		switch typed := value.(type) {
		case nil, bool, int, int32, int64, uint, uint32, uint64, float32, float64, json.Number:
			out[key] = typed
		case string:
			if metric, ok := safeRuntimeNumericMetric(key, typed); ok {
				out[key] = metric
			} else {
				out[key] = redactOperationalLogText(typed)
			}
		case error:
			out[key] = safeRuntimeLogError(typed)
		default:
			out[key] = "present"
		}
	}
	return out
}

func safeRuntimeNumericMetric(key, value string) (int64, bool) {
	normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
	metricKey := false
	for _, suffix := range []string{
		"age", "agemillis", "bytes", "clients", "count", "duration", "durationmillis",
		"epoch", "frames", "height", "length", "millis", "restarts", "sequence", "timestamp",
		"timestampmillis", "width",
	} {
		if strings.HasSuffix(normalized, suffix) {
			metricKey = true
			break
		}
	}
	if !metricKey || value == "" || len(value) > 16 {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > 9_999_999_999_999_999 {
		return 0, false
	}
	return parsed, true
}

func decodeBrowserClientLog(data []byte) (string, map[string]any, string, bool) {
	if len(data) == 0 || len(data) > browserClientLogBodyBytes {
		return "", nil, "", false
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var input browserClientLogInput
	if err := decoder.Decode(&input); err != nil || input.Type != "client_log" {
		return "", nil, "", false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", nil, "", false
	}
	event := strings.TrimSpace(input.Event)
	if _, allowed := browserClientLogEvents[event]; !allowed || len(input.Detail) > state.SafeOperationalLogDetailMaxBytes {
		return "", nil, "", false
	}
	detail := safeBrowserClientLogDetail(input.Detail)
	body, err := json.Marshal(detail)
	if err != nil {
		return "", nil, "", false
	}
	return event, detail, safeRuntimeLogText(string(body)), true
}

func safeBrowserClientLogDetail(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil || input == nil {
		return map[string]any{"detail": redactBrowserClientText(raw)}
	}
	out := make(map[string]any, min(len(input), 16))
	for key, value := range input {
		if len(out) == 16 || key == "detail" || browserClientDetailKeyIsSensitive(key) {
			continue
		}
		cleanKey := safeRuntimeLogKey(key)
		switch typed := value.(type) {
		case nil, bool, float64, json.Number:
			out[cleanKey] = typed
		case string:
			out[cleanKey] = redactBrowserClientText(typed)
		default:
			out[cleanKey] = "present"
		}
	}
	if value, ok := input["detail"]; ok {
		switch typed := value.(type) {
		case string:
			out["detail"] = sanitizeBrowserClientDetailString(typed)
		case nil, bool, float64, json.Number:
			out["detail"] = typed
		default:
			out["detail"] = "present"
		}
	}
	return out
}

func browserStartupSourceTime(detail map[string]any, now time.Time) (streamStartupTraceSourceTime, bool) {
	epochValue, epochOK := detail["sourceAtEpochMillis"].(float64)
	performanceValue, performanceOK := detail["sourceAtPerformanceMillis"].(float64)
	if !epochOK || !performanceOK || math.IsNaN(epochValue) || math.IsInf(epochValue, 0) ||
		math.IsNaN(performanceValue) || math.IsInf(performanceValue, 0) {
		return streamStartupTraceSourceTime{}, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	maxClockDeltaMillis := int64((24 * time.Hour) / time.Millisecond)
	nowMillis := now.UnixMilli()
	if epochValue <= 0 || epochValue < float64(nowMillis-maxClockDeltaMillis) || epochValue > float64(nowMillis+maxClockDeltaMillis) ||
		performanceValue < 0 || performanceValue > float64((7*24*time.Hour)/time.Millisecond) {
		return streamStartupTraceSourceTime{}, false
	}
	epochMillis := int64(math.Round(epochValue))
	return streamStartupTraceSourceTime{
		epochMillis:       epochMillis,
		performanceMillis: performanceValue,
	}, true
}

func sanitizeBrowserClientDetailString(raw string) string {
	var nested map[string]any
	if err := json.Unmarshal([]byte(raw), &nested); err == nil && nested != nil {
		safe := make(map[string]any, min(len(nested), 16))
		for key, value := range nested {
			if len(safe) == 16 || browserClientDetailKeyIsSensitive(key) {
				continue
			}
			cleanKey := safeRuntimeLogKey(key)
			switch typed := value.(type) {
			case nil, bool, float64, json.Number:
				safe[cleanKey] = typed
			case string:
				safe[cleanKey] = redactBrowserClientText(typed)
			default:
				safe[cleanKey] = "present"
			}
		}
		if body, err := json.Marshal(safe); err == nil {
			raw = string(body)
		}
	}
	return redactBrowserClientText(raw)
}

func browserClientDetailKeyIsSensitive(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.TrimSpace(key)))
	if key == "webcodecs" {
		return false
	}
	if key == "code" || key == "image" || key == "payload" || key == "raw" {
		return true
	}
	return operationalLogDetailKeyIsSensitive(key)
}

func runtimeLogDetailKeyIsSensitive(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(strings.TrimSpace(key)))
	if key == "code" || key == "value" || key == "image" || key == "payload" || key == "raw" || key == "detailjson" || key == "row" {
		return true
	}
	return operationalLogDetailKeyIsSensitive(key)
}

func operationalLogDetailKeyIsSensitive(key string) bool {
	key = strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.TrimSpace(key)))
	for _, marker := range []string{
		"token", "password", "secret", "authorization", "cookie", "digits", "controlcode",
		"imagebase64", "payloadjson", "prompt", "telegram", "userid", "chatid", "email",
		"session", "jwt", "credential", "privatekey", "apikey", "resulttext", "ocr", "rawpayload",
	} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func safeRuntimeLogKey(value string) string {
	value = cleanStreamControlText(value, "field")
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, value)
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

func redactBrowserClientText(value string) string {
	return redactOperationalLogText(value)
}

func redactOperationalLogText(value string) string {
	value = safeRuntimeLogText(value)
	for _, pattern := range []*regexp.Regexp{
		sensitiveEventText,
		sensitiveClientToken,
		sensitiveEmail,
		sensitiveIPAddress,
		sensitivePrivatePath,
	} {
		value = pattern.ReplaceAllString(value, "[redacted]")
	}
	return value
}

func safeRuntimeLogError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return "timeout"
		}
		return "network_error"
	}

	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "rate limit"), strings.Contains(text, "too many requests"):
		return "rate_limited"
	case strings.Contains(text, "unauthorized"), strings.Contains(text, "forbidden"), strings.Contains(text, "permission denied"), strings.Contains(text, "authentication"):
		return "authorization_failed"
	case strings.Contains(text, "not found"):
		return "not_found"
	case strings.Contains(text, "conflict"), strings.Contains(text, "already exists"):
		return "conflict"
	case strings.Contains(text, "invalid"), strings.Contains(text, "bad request"), strings.Contains(text, "validation"):
		return "invalid_request"
	case strings.Contains(text, "connection refused"), strings.Contains(text, "no such host"), strings.Contains(text, "unreachable"), strings.Contains(text, "tls"), strings.Contains(text, "x509"):
		return "network_error"
	default:
		return "operation_failed"
	}
}

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

// startupTraceCorrelationID is a short, opaque derivative of the in-memory
// startup trace ID. It is safe to carry through the durable command payload and
// Pixel trace events without exposing a browser session, ticket data, or an
// authentication value.
func startupTraceCorrelationID(traceID string) string {
	return safeRuntimeTraceID("startup", traceID)
}

func newStartupRunOrigin() string {
	return "ticket.startup." + randomID()
}

func boundedStartupRunOrigin(value string) string {
	clean := strings.TrimSpace(value)
	if len(clean) != len("ticket.startup.")+32 || !strings.HasPrefix(clean, "ticket.startup.") {
		return ""
	}
	for _, char := range strings.TrimPrefix(clean, "ticket.startup.") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return ""
		}
	}
	return clean
}

func (h *directStreamHub) beginStartupTrace(sessionID, reason string) string {
	return h.beginStartupTraceWithMode(sessionID, "", reason, false)
}

// startStartupTrace always begins a new navigation run. Downstream work for
// that navigation continues to use beginStartupTrace so the authenticated
// index, prewarm, and video socket still join one trace.
func (h *directStreamHub) startStartupTrace(sessionID, reason string) string {
	return h.beginStartupTraceWithMode(sessionID, "", reason, true)
}

func (h *directStreamHub) startStartupTraceForRun(sessionID, runOrigin, reason string) string {
	return h.beginStartupTraceWithMode(sessionID, boundedStartupRunOrigin(runOrigin), reason, true)
}

func (h *directStreamHub) joinStartupTraceForRun(sessionID, runOrigin, reason string) string {
	now := time.Now()
	sessionID = safeRuntimeTraceID("session", sessionID)
	runOrigin = boundedStartupRunOrigin(runOrigin)
	if sessionID == "" || runOrigin == "" {
		return ""
	}
	reason = cleanStreamControlText(reason, "stream_startup")
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID == "" || trace.Complete || now.Sub(trace.StartedAt) > streamStartupTraceMaxAge ||
		trace.SessionID != sessionID || trace.RunOrigin != runOrigin {
		return ""
	}
	h.addStartupTracePhaseLocked(now, "startup_trace_joined", reason, false, nil)
	return trace.ID
}

func (h *directStreamHub) beginStartupTraceWithMode(sessionID, runOrigin, reason string, replace bool) string {
	now := time.Now()
	sessionID = safeRuntimeTraceID("session", sessionID)
	reason = cleanStreamControlText(reason, "stream_startup")
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if !replace && trace.ID != "" && !trace.Complete && now.Sub(trace.StartedAt) <= streamStartupTraceMaxAge &&
		(sessionID == "" || trace.SessionID == "" || sessionID == trace.SessionID) {
		h.addStartupTracePhaseLocked(now, "startup_trace_joined", reason, false, nil)
		return trace.ID
	}
	*trace = streamStartupTrace{
		ID:        "stream:" + now.UTC().Format("20060102T150405.000000000Z") + ":" + randomID(),
		SessionID: trimLogField(sessionID, 96), RunOrigin: runOrigin,
		Reason: reason, StartedAt: now, LastAt: now,
	}
	h.addStartupTracePhaseLocked(now, "startup_trace_started", reason, false, nil)
	return trace.ID
}

func (h *directStreamHub) recordStartupPhase(name, detail string)     { h.tracePhase(name, detail, 0) }
func (h *directStreamHub) recordStartupPhaseOnce(name, detail string) { h.tracePhase(name, detail, 1) }
func (h *directStreamHub) completeStartupTrace(name, detail string)   { h.tracePhase(name, detail, 3) }

func (h *directStreamHub) recordStartupPhaseForTrace(traceID, name, detail string) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return
	}
	h.tracePhaseForTrace(traceID, name, detail, 0, nil)
}

func (h *directStreamHub) recordStartupPhaseOnceForTrace(traceID, name, detail string) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return
	}
	h.tracePhaseForTrace(traceID, name, detail, 1, nil)
}

func (h *directStreamHub) recordStartupPhaseOnceForTraceWithSource(traceID, name, detail string, source streamStartupTraceSourceTime) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return
	}
	h.tracePhaseForTrace(traceID, name, detail, 1, &source)
}

func (h *directStreamHub) recordStartupPhaseOnceForCorrelation(correlationID, name, detail string) bool {
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID == "" || trace.Complete || now.Sub(trace.StartedAt) > streamStartupTraceMaxAge ||
		startupTraceCorrelationID(trace.ID) != correlationID {
		return false
	}
	return h.addStartupTracePhaseLocked(now, name, detail, true, nil)
}

func (h *directStreamHub) completeStartupTraceForTrace(traceID, name, detail string) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return
	}
	h.tracePhaseForTrace(traceID, name, detail, 3, nil)
}

func (h *directStreamHub) completeStartupTraceForTraceWithSource(traceID, name, detail string, source streamStartupTraceSourceTime) {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return
	}
	h.tracePhaseForTrace(traceID, name, detail, 3, &source)
}

func (h *directStreamHub) startupTraceActive(traceID string) bool {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startupTrace.ID == traceID && !h.startupTrace.Complete && now.Sub(h.startupTrace.StartedAt) <= streamStartupTraceMaxAge
}

func (h *directStreamHub) startupTraceCurrent(traceID string) bool {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.startupTrace.ID == traceID && now.Sub(h.startupTrace.StartedAt) <= streamStartupTraceMaxAge
}

func (h *directStreamHub) startupTraceActiveForSession(sessionID string) bool {
	sessionID = safeRuntimeTraceID("session", sessionID)
	if sessionID == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	return trace.ID != "" && !trace.Complete && trace.SessionID == sessionID &&
		now.Sub(trace.StartedAt) <= streamStartupTraceMaxAge
}

// withActiveStartupTrace keeps validation and its bounded in-memory lease
// mutation in one trace critical section. Without this guard, a sibling
// browser could complete the trace after validation but before the lease was
// installed, resurrecting a grace hold after first paint.
func (h *directStreamHub) withActiveStartupTrace(traceID string, action func()) bool {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" || action == nil {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID != traceID || trace.Complete || now.Sub(trace.StartedAt) > streamStartupTraceMaxAge {
		return false
	}
	action()
	return true
}

func (h *directStreamHub) withoutActiveStartupTraceForSession(sessionID string, action func()) bool {
	sessionID = safeRuntimeTraceID("session", sessionID)
	if sessionID == "" || action == nil {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID != "" && !trace.Complete && trace.SessionID == sessionID &&
		now.Sub(trace.StartedAt) <= streamStartupTraceMaxAge {
		return false
	}
	action()
	return true
}

func (h *directStreamHub) activeStartupTraceCorrelationID() string {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID == "" || trace.Complete || now.Sub(trace.StartedAt) > streamStartupTraceMaxAge {
		return ""
	}
	return startupTraceCorrelationID(trace.ID)
}

// tracePhase modes: 0 records, 1 deduplicates, 3 deduplicates and completes.
func (h *directStreamHub) tracePhase(name, detail string, mode uint8) {
	h.tracePhaseForTrace("", name, detail, mode, nil)
}

func (h *directStreamHub) tracePhaseForTrace(traceID, name, detail string, mode uint8, source *streamStartupTraceSourceTime) {
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.startupTrace
	if trace.ID == "" || (traceID != "" && trace.ID != traceID) || trace.Complete || now.Sub(trace.StartedAt) > streamStartupTraceMaxAge {
		return
	}
	if h.addStartupTracePhaseLocked(now, name, detail, mode&1 != 0, source) && mode&2 != 0 {
		trace.Complete = true
	}
}

func (h *directStreamHub) addStartupTracePhaseLocked(now time.Time, name, detail string, once bool, source *streamStartupTraceSourceTime) bool {
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
	phase := streamStartupTracePhase{
		Name: name, Detail: trimLogField(detail, 240), At: now,
		ElapsedMillis: durationMillis(max(now.Sub(trace.StartedAt), 0)),
	}
	if source != nil {
		epochMillis := source.epochMillis
		performanceMillis := source.performanceMillis
		phase.SourceAtEpochMillis = &epochMillis
		phase.SourceAtPerformanceMillis = &performanceMillis
	}
	trace.Phases = append(trace.Phases, phase)
	if len(trace.Phases) > streamStartupTraceMaxSteps {
		trace.Phases = trace.Phases[len(trace.Phases)-streamStartupTraceMaxSteps:]
	}
	trace.LastAt = now
	trace.LastPhase = name
	return true
}

func (h *directStreamHub) startupTraceSnapshot(now time.Time) map[string]any {
	trace := h.startupTrace
	if trace.ID == "" {
		return nil
	}
	// Keep a completed trace frozen at its terminal phase, but let an active
	// trace's elapsed time advance between phase events.  Otherwise an idle
	// startup appears to take zero milliseconds until the next event arrives.
	elapsedAt := trace.LastAt
	if !trace.Complete && !now.IsZero() && now.After(elapsedAt) {
		elapsedAt = now
	}
	elapsed := max(elapsedAt.Sub(trace.StartedAt), 0)
	return map[string]any{
		"id": trace.ID, "startedAt": trace.StartedAt.UTC().Format(time.RFC3339Nano),
		"correlationId": startupTraceCorrelationID(trace.ID),
		"reason":        trace.Reason,
		"elapsedMillis": durationMillis(elapsed),
		"targetMillis":  durationMillis(streamStartupTraceTarget), "overBudget": elapsed > streamStartupTraceTarget,
		"complete": trace.Complete, "lastPhase": trace.LastPhase,
		"phaseOrder":           "server_receipt",
		"sourceClockSemantics": "independent_diagnostic_clock",
		"phases":               append([]streamStartupTracePhase(nil), trace.Phases...),
	}
}
