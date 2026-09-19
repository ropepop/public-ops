package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	streamOpeningMaxAge        = 2 * time.Minute
)

var (
	sensitiveEventText   = regexp.MustCompile(`(?i)https?://[^\s"']+|\b(?:bearer|token|password|secret|cookie|authorization|prompt)\b|\b\d{2,}\b`)
	sensitiveClientToken = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
	sensitiveEmail       = regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`)
	sensitiveIPAddress   = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	sensitivePrivatePath = regexp.MustCompile(`(?i)(?:/Users/|/home/|/root/|[A-Z]:\\Users\\)[^\s"']*`)
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

// The opening owns only the bounded startup lease. Diagnostic phases cannot
// extend its authority or complete a newer browser opening.
type streamOpening struct {
	ID, SessionID, RunOrigin string
	StartedAt                time.Time
	Complete                 bool
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
	if (event != "ticket_v2_failure" && event != "ticket_opening") || len(input.Detail) > state.SafeOperationalLogDetailMaxBytes {
		return "", nil, "", false
	}
	var inputDetail map[string]any
	if json.Unmarshal([]byte(input.Detail), &inputDetail) != nil {
		return "", nil, "", false
	}
	// One bounded opening summary and failure reports have fixed scalar fields.
	detail := map[string]any{}
	if event == "ticket_opening" {
		for _, key := range []string{"firstPresentedMillis", "tenPresentedMillis", "presentedFrames", "reconnects"} {
			if value, ok := inputDetail[key].(float64); ok && value >= 0 && value <= 120000 {
				detail[key] = value
			}
		}
		kind, _ := inputDetail["openingClass"].(string)
		if kind == "warm" || kind == "cold" || kind == "recovery" || kind == "unknown" {
			detail["openingClass"] = kind
		}
	} else {
		for _, key := range []string{"reason", "errorKind", "pageVersion"} {
			if value, ok := inputDetail[key].(string); ok {
				detail[key] = redactOperationalLogText(value)
			}
		}
		for key, allowed := range map[string][]string{
			"hdrStage":      {"capability", "initialize", "render", "present", "device", "unknown"},
			"hdrReason":     {"unsupported", "initialization_failed", "initialization_timeout", "render_failed", "present_failed", "device_lost", "gpu_error", "display_timeout", "unknown"},
			"browserFamily": {"safari", "chromium", "firefox", "other"},
			"visibility":    {"visible", "hidden"},
		} {
			if value, ok := inputDetail[key].(string); ok && slices.Contains(allowed, value) {
				detail[key] = value
			}
		}
		for key, bounds := range map[string][2]float64{"hdrBoost": {2, 6}, "mediaAgeMillis": {0, 120000}} {
			if value, ok := inputDetail[key].(float64); ok && value >= bounds[0] && value <= bounds[1] && value == float64(int64(value)) {
				detail[key] = value
			}
		}
		if value, ok := inputDetail["hdrRecovering"].(bool); ok {
			detail["hdrRecovering"] = value
		}
	}
	body, err := json.Marshal(detail)
	if err != nil {
		return "", nil, "", false
	}
	return event, detail, safeRuntimeLogText(string(body)), true
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

func (h *directStreamHub) beginOpening(sessionID, reason string) string {
	return h.beginOpeningWithMode(sessionID, "", reason, false)
}

func (h *directStreamHub) startOpeningForRun(sessionID, runOrigin, reason string) string {
	return h.beginOpeningWithMode(sessionID, boundedStartupRunOrigin(runOrigin), reason, true)
}

func (h *directStreamHub) joinOpeningForRun(sessionID, runOrigin, reason string) string {
	now := time.Now()
	sessionID = safeRuntimeTraceID("session", sessionID)
	runOrigin = boundedStartupRunOrigin(runOrigin)
	if sessionID == "" || runOrigin == "" {
		return ""
	}
	reason = cleanStreamControlText(reason, "stream_startup")
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.opening
	if trace.ID == "" || trace.Complete || now.Sub(trace.StartedAt) > streamOpeningMaxAge ||
		trace.SessionID != sessionID || trace.RunOrigin != runOrigin {
		return ""
	}
	return trace.ID
}

func (h *directStreamHub) beginOpeningWithMode(sessionID, runOrigin, reason string, replace bool) string {
	now := time.Now()
	sessionID = safeRuntimeTraceID("session", sessionID)
	reason = cleanStreamControlText(reason, "stream_startup")
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.opening
	if !replace && trace.ID != "" && !trace.Complete && now.Sub(trace.StartedAt) <= streamOpeningMaxAge &&
		(sessionID == "" || trace.SessionID == "" || sessionID == trace.SessionID) {
		return trace.ID
	}
	*trace = streamOpening{
		ID:        "stream:" + now.UTC().Format("20060102T150405.000000000Z") + ":" + randomID(),
		SessionID: trimLogField(sessionID, 96), RunOrigin: runOrigin,
		StartedAt: now,
	}
	return trace.ID
}

func (h *directStreamHub) completeOpening(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id != "" && h.opening.ID == id {
		h.opening.Complete = true
	}
}

func (h *directStreamHub) openingActive(traceID string) bool {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opening.ID == traceID && !h.opening.Complete && now.Sub(h.opening.StartedAt) <= streamOpeningMaxAge
}

func (h *directStreamHub) openingActiveForSession(sessionID string) bool {
	sessionID = safeRuntimeTraceID("session", sessionID)
	if sessionID == "" {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.opening
	return trace.ID != "" && !trace.Complete && trace.SessionID == sessionID &&
		now.Sub(trace.StartedAt) <= streamOpeningMaxAge
}

// withActiveOpening keeps validation and its bounded in-memory lease
// mutation in one trace critical section. Without this guard, a sibling
// browser could complete the trace after validation but before the lease was
// installed, resurrecting a grace hold after first paint.
func (h *directStreamHub) withActiveOpening(traceID string, action func()) bool {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" || action == nil {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.opening
	if trace.ID != traceID || trace.Complete || now.Sub(trace.StartedAt) > streamOpeningMaxAge {
		return false
	}
	action()
	return true
}

func (h *directStreamHub) withoutActiveOpeningForSession(sessionID string, action func()) bool {
	sessionID = safeRuntimeTraceID("session", sessionID)
	if sessionID == "" || action == nil {
		return false
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	trace := &h.opening
	if trace.ID != "" && !trace.Complete && trace.SessionID == sessionID &&
		now.Sub(trace.StartedAt) <= streamOpeningMaxAge {
		return false
	}
	action()
	return true
}
