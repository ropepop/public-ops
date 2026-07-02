package web

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"ticketremote/internal/state"
)

func (s *Server) recordRuntimeEventAsync(level, event, correlationID string, detail map[string]any) {
	s.recordRuntimeEventForSourceAsync("ticket_remote", level, event, correlationID, detail)
}

func (s *Server) recordRuntimeEventForSourceAsync(source, level, event, correlationID string, detail map[string]any) {
	if s == nil || s.store == nil {
		return
	}
	go s.recordRuntimeEventForSource(source, level, event, correlationID, detail)
}

func (s *Server) recordRuntimeErrorAsync(event, correlationID string, err error, detail map[string]any) {
	if err == nil {
		return
	}
	if detail == nil {
		detail = map[string]any{}
	}
	detail["error"] = safeRuntimeLogError(err)
	s.recordRuntimeEventAsync("warn", event, correlationID, detail)
}

func (s *Server) recordRuntimeEvent(level, event, correlationID string, detail map[string]any) {
	s.recordRuntimeEventForSource("ticket_remote", level, event, correlationID, detail)
}

func (s *Server) recordRuntimeEventForSource(source, level, event, correlationID string, detail map[string]any) {
	if s == nil || s.store == nil {
		return
	}
	body := "{}"
	if len(detail) > 0 {
		if raw, err := json.Marshal(safeRuntimeLogDetail(detail)); err == nil {
			body = string(raw)
		}
	}
	if len(body) > 2048 {
		body = `{"truncated":true}`
	}
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	_ = s.store.AppendSafeOperationalLog(ctx, state.SafeOperationalLogInput{
		TicketID:      s.cfg.TicketID,
		Source:        cleanStreamControlText(source, "ticket_remote"),
		Level:         cleanStreamControlText(level, "info"),
		Event:         cleanStreamControlText(event, "runtime_event"),
		CorrelationID: cleanRuntimeCorrelationID(correlationID),
		DetailJSON:    body,
		Now:           time.Now(),
	})
}

func safeRuntimeTraceID(prefix string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	prefix = cleanStreamControlText(prefix, "trace")
	return fmt.Sprintf("%s_%08x", prefix, h.Sum32())
}

func cleanRuntimeCorrelationID(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return cleanStreamControlText(value, "")
}

func safeRuntimeLogDetail(detail map[string]any) map[string]any {
	clean := make(map[string]any, len(detail))
	for key, value := range detail {
		clean[cleanStreamControlText(key, "field")] = safeRuntimeLogValue(value)
	}
	return clean
}

func safeRuntimeLogValue(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return safeRuntimeLogText(typed)
	case error:
		return safeRuntimeLogError(typed)
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
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return "present"
		}
		return safeRuntimeLogText(string(raw))
	}
}

func safeRuntimeLogError(err error) string {
	if err == nil {
		return ""
	}
	return safeRuntimeLogText(publicHealthError(err))
}

func safeRuntimeLogText(value string) string {
	text := strings.Join(strings.Fields(value), " ")
	if len(text) > 240 {
		return text[:240]
	}
	return text
}
