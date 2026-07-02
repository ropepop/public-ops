package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

const (
	adminOperationalEventsDefaultLimit = 80
	adminOperationalEventsMaxLimit     = 200
	serviceEventBodyLimitBytes         = 16 * 1024
)

var (
	productDigitsPattern = regexp.MustCompile(`\b\d{2,8}\b`)
	productURLPattern    = regexp.MustCompile(`https?://[^\s"']+`)
)

type productEventInput struct {
	Source        string         `json:"source"`
	Category      string         `json:"category"`
	Action        string         `json:"action"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason"`
	RequestID     string         `json:"requestId"`
	CommandID     string         `json:"commandId"`
	BackendID     string         `json:"backendId"`
	CorrelationID string         `json:"correlationId"`
	Sensitive     bool           `json:"sensitive"`
	SafeState     map[string]any `json:"safeState"`
	Count         int64          `json:"count"`
	FirstSeenAt   string         `json:"firstSeenAt"`
	LastSeenAt    string         `json:"lastSeenAt"`
}

type adminOperationalEvent struct {
	ID            string         `json:"id"`
	CreatedAt     string         `json:"createdAt"`
	Source        string         `json:"source"`
	Level         string         `json:"level"`
	Event         string         `json:"event"`
	Category      string         `json:"category"`
	Action        string         `json:"action"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason,omitempty"`
	RequestID     string         `json:"requestId,omitempty"`
	CommandID     string         `json:"commandId,omitempty"`
	BackendID     string         `json:"backendId,omitempty"`
	CorrelationID string         `json:"correlationId,omitempty"`
	Sensitive     bool           `json:"sensitive,omitempty"`
	Count         int64          `json:"count,omitempty"`
	Summary       string         `json:"summary"`
	SafeState     map[string]any `json:"safeState,omitempty"`
}

func (s *Server) recordProductEventAsync(input productEventInput) {
	go s.recordProductEvent(input)
}

func (s *Server) recordProductEvent(input productEventInput) {
	if s == nil || s.store == nil {
		return
	}
	source := cleanProductToken(input.Source, "ticket_remote")
	category := cleanProductToken(input.Category, "runtime")
	action := cleanProductToken(input.Action, "event")
	status := cleanProductToken(input.Status, "ok")
	level := "info"
	if status == "failed" || status == "error" || strings.Contains(status, "timeout") {
		level = "warn"
	}
	correlationID := firstNonEmpty(input.CorrelationID, input.RequestID, input.CommandID)
	detail := map[string]any{
		"source":    source,
		"category":  category,
		"action":    action,
		"status":    status,
		"sensitive": input.Sensitive,
	}
	if input.Reason != "" {
		detail["reason"] = redactProductText(input.Reason)
	}
	if input.RequestID != "" {
		detail["requestId"] = cleanRuntimeCorrelationID(input.RequestID)
	}
	if input.CommandID != "" {
		detail["commandId"] = cleanRuntimeCorrelationID(input.CommandID)
	}
	if input.BackendID != "" {
		detail["backendId"] = cleanProductToken(input.BackendID, "backend")
	}
	if input.Count > 0 {
		detail["count"] = input.Count
	}
	if input.FirstSeenAt != "" {
		detail["firstSeenAt"] = safeRuntimeLogText(input.FirstSeenAt)
	}
	if input.LastSeenAt != "" {
		detail["lastSeenAt"] = safeRuntimeLogText(input.LastSeenAt)
	}
	if len(input.SafeState) > 0 {
		detail["safeState"] = safeProductMap(input.SafeState)
	}
	body, err := json.Marshal(detail)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	_ = s.store.AppendSafeOperationalLog(ctx, state.SafeOperationalLogInput{
		TicketID:      s.cfg.TicketID,
		Source:        source,
		Level:         level,
		Event:         "product_" + category + "_" + action,
		CorrelationID: cleanRuntimeCorrelationID(correlationID),
		DetailJSON:    string(body),
		Now:           time.Now(),
	})
}

func (s *Server) handleServiceEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	expected := strings.TrimSpace(s.cfg.ServiceEvents.Token)
	if expected == "" {
		writeJSON(w, http.StatusNotFound, apiResponse{OK: false, Error: "service_events_disabled", Message: "Service event ingestion is not enabled."})
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" || token != expected {
		_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, serviceEventBodyLimitBytes))
		writeJSON(w, http.StatusForbidden, apiResponse{OK: false, Error: "forbidden", Message: "Service event token is invalid."})
		return
	}
	var input productEventInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, serviceEventBodyLimitBytes)).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: "Invalid service event JSON."})
		return
	}
	if strings.TrimSpace(input.Source) == "" || strings.TrimSpace(input.Category) == "" || strings.TrimSpace(input.Action) == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{OK: false, Error: "bad_request", Message: "source, category, and action are required."})
		return
	}
	s.recordProductEvent(input)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleAdminOperationalEvents(w http.ResponseWriter, r *http.Request, _ auth.Identity, _ string, _ state.Snapshot) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "state_unavailable", Message: "Ticket state is unavailable."})
		return
	}
	limit := uint32FromString(r.URL.Query().Get("limit"), adminOperationalEventsDefaultLimit)
	if limit == 0 || limit > adminOperationalEventsMaxLimit {
		limit = adminOperationalEventsDefaultLimit
	}
	since := time.Now().Add(-2 * time.Hour)
	if raw := strings.TrimSpace(r.URL.Query().Get("since")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			since = parsed
		}
	}
	rows, err := s.store.ListSafeOperationalLogs(r.Context(), state.SafeOperationalLogQueryInput{
		TicketID: s.cfg.TicketID,
		Source:   cleanOptionalProductToken(r.URL.Query().Get("source")),
		Level:    cleanOptionalProductToken(r.URL.Query().Get("level")),
		Event:    cleanOptionalProductToken(r.URL.Query().Get("event")),
		Since:    since,
		Limit:    limit,
	})
	if err != nil {
		s.recordRuntimeErrorAsync("admin_operational_events_failed", "", err, nil)
		writeJSON(w, http.StatusServiceUnavailable, apiResponse{OK: false, Error: "events_unavailable", Message: "Operational events are unavailable."})
		return
	}
	categoryFilter := cleanOptionalProductToken(r.URL.Query().Get("category"))
	events := make([]adminOperationalEvent, 0, len(rows))
	for _, row := range rows {
		event := adminOperationalEventFromLog(row)
		if categoryFilter != "" && event.Category != categoryFilter {
			continue
		}
		events = append(events, event)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "events": events, "since": since.UTC().Format(time.RFC3339)})
}

func (s *Server) handlePixelTraceEvent(msg map[string]any) bool {
	msgType, _ := msg["type"].(string)
	if strings.TrimSpace(msgType) != "ticket_trace_event" {
		return false
	}
	event := cleanProductToken(productMessageString(msg["event"]), "pixel_event")
	level := cleanProductToken(productMessageString(msg["level"]), "info")
	category, action := categoryActionFromEvent(event, "pixel")
	detail := map[string]any{
		"source":    "pixel",
		"category":  category,
		"action":    action,
		"status":    level,
		"sensitive": true,
	}
	for _, key := range []string{
		"detail",
		"streamState",
		"sessionState",
		"streamActive",
		"captureMode",
		"videoClients",
		"frameSequence",
		"sentFrames",
		"timestampMillis",
	} {
		if value, ok := msg[key]; ok {
			detail[key] = value
		}
	}
	correlationID := firstNonEmpty(productMessageString(msg["correlationId"]), productMessageString(msg["traceId"]))
	s.recordRuntimeEventForSourceAsync("pixel", level, event, correlationID, detail)
	return true
}

func productMessageString(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func adminOperationalEventFromLog(row state.SafeOperationalLog) adminOperationalEvent {
	detail := parseLogDetail(row.DetailJSON)
	category := stringField(detail, "category")
	action := stringField(detail, "action")
	if category == "" || action == "" {
		category, action = categoryActionFromEvent(row.Event, row.Source)
	}
	status := stringField(detail, "status")
	if status == "" {
		status = row.Level
	}
	count := int64Field(detail, "count")
	if count == 0 {
		count = int64Field(detail, "sampledCount")
	}
	safeState, _ := detail["safeState"].(map[string]any)
	out := adminOperationalEvent{
		ID:            row.ID,
		CreatedAt:     row.CreatedAt,
		Source:        cleanProductToken(row.Source, "unknown"),
		Level:         cleanProductToken(row.Level, "info"),
		Event:         cleanProductToken(row.Event, "event"),
		Category:      cleanProductToken(category, "runtime"),
		Action:        cleanProductToken(action, "event"),
		Status:        cleanProductToken(status, "info"),
		Reason:        redactProductText(stringField(detail, "reason")),
		RequestID:     cleanRuntimeCorrelationID(stringField(detail, "requestId")),
		CommandID:     cleanRuntimeCorrelationID(stringField(detail, "commandId")),
		BackendID:     cleanOptionalProductToken(stringField(detail, "backendId")),
		CorrelationID: cleanRuntimeCorrelationID(row.CorrelationID),
		Sensitive:     boolField(detail, "sensitive"),
		Count:         count,
		SafeState:     safeProductMap(safeState),
	}
	out.Summary = operationalEventSummary(out)
	return out
}

func parseLogDetail(raw string) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return map[string]any{}
	}
	return parsed
}

func categoryActionFromEvent(event string, source string) (string, string) {
	clean := cleanProductToken(event, "event")
	cleanSource := cleanProductToken(source, "")
	if strings.HasPrefix(clean, "product_") {
		parts := strings.SplitN(strings.TrimPrefix(clean, "product_"), "_", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	switch {
	case strings.Contains(clean, "control_code"):
		return "control_code", clean
	case strings.Contains(clean, "ticket_reselect"):
		return "ticket_reselect", clean
	case strings.Contains(clean, "stream") || strings.Contains(clean, "keyframe") || strings.Contains(clean, "recovery"):
		return "stream", clean
	case strings.Contains(clean, "browser") || strings.Contains(cleanSource, "browser"):
		return "browser", clean
	case strings.Contains(clean, "bridge") || strings.Contains(cleanSource, "bridge"):
		return "bridge", clean
	case strings.Contains(clean, "broker") || strings.Contains(cleanSource, "broker"):
		return "broker", clean
	case strings.Contains(clean, "failed") || strings.Contains(clean, "error") || strings.Contains(clean, "timeout") || strings.Contains(clean, "blocked"):
		return "errors", clean
	default:
		return "runtime", clean
	}
}

func operationalEventSummary(event adminOperationalEvent) string {
	parts := []string{humanOperationalLabel(event.Category), humanOperationalLabel(event.Action), humanOperationalLabel(event.Status)}
	if event.Reason != "" {
		parts = append(parts, event.Reason)
	}
	if event.Count > 1 {
		parts = append(parts, "count "+safeRuntimeLogText(jsonNumberText(event.Count)))
	}
	return strings.Join(parts, " · ")
}

func humanOperationalLabel(value string) string {
	return strings.ReplaceAll(cleanProductToken(value, "event"), "_", " ")
}

func safeProductMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		cleanKey := cleanProductToken(key, "field")
		if cleanKey == "detail_json" || cleanKey == "payload_json" || cleanKey == "row" || cleanKey == "image" || cleanKey == "token" || cleanKey == "digits" || cleanKey == "value" {
			continue
		}
		output[cleanKey] = safeProductValue(value)
	}
	if len(output) == 0 {
		return nil
	}
	return output
}

func safeProductValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case bool:
		return typed
	case int:
		return typed
	case int64:
		return typed
	case uint64:
		return typed
	case float64:
		return typed
	case json.Number:
		return typed.String()
	case string:
		return redactProductText(typed)
	default:
		return "present"
	}
}

func redactProductText(value string) string {
	text := safeRuntimeLogText(value)
	text = productURLPattern.ReplaceAllString(text, "[url]")
	text = productDigitsPattern.ReplaceAllStringFunc(text, func(match string) string {
		if len(match) >= 2 && len(match) <= 8 {
			return "[redacted]"
		}
		return match
	})
	return text
}

func cleanProductToken(value string, fallback string) string {
	return cleanStreamControlText(value, fallback)
}

func cleanOptionalProductToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return cleanProductToken(value, "")
}

func stringField(values map[string]any, key string) string {
	value, _ := values[key]
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func boolField(values map[string]any, key string) bool {
	value, _ := values[key]
	typed, _ := value.(bool)
	return typed
}

func int64Field(values map[string]any, key string) int64 {
	value, _ := values[key]
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func uint32FromString(value string, fallback uint32) uint32 {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var parsed uint64
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if ch < '0' || ch > '9' {
			return fallback
		}
		parsed = parsed*10 + uint64(ch-'0')
		if parsed > 100000 {
			return fallback
		}
	}
	return uint32(parsed)
}

func jsonNumberText(value int64) string {
	body, _ := json.Marshal(value)
	return string(body)
}
