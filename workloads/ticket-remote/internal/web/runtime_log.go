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
	source = cleanStreamControlText(source, "ticket_remote")
	level = cleanStreamControlText(level, "info")
	rawEvent := cleanStreamControlText(event, "runtime_event")
	event = compactRuntimeEventName(source, level, rawEvent)
	if event != rawEvent {
		copied := make(map[string]any, len(detail)+1)
		for key, value := range detail {
			copied[key] = value
		}
		copied["originalEvent"] = rawEvent
		detail = copied
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
		ID:            state.NewSafeOperationalLogID(source, event, correlationID, time.Now()),
		TicketID:      s.cfg.TicketID,
		Source:        source,
		Level:         level,
		Event:         event,
		CorrelationID: cleanRuntimeCorrelationID(correlationID),
		DetailJSON:    state.ClampSafeOperationalLogDetail(body),
		Now:           time.Now(),
	})
}

func compactRuntimeEventName(source string, level string, event string) string {
	failed := productEventFailed(event, level)
	switch event {
	case "client_log":
		return "browser_event"
	case "page_boot":
		return "browser_opened"
	case "video_socket_open", "video_socket_opened", "relay_viewer_added":
		return "stream_opened"
	case "video_socket_closed", "video_socket_closed_intentional", "relay_viewer_removed", "viewer_idle_disconnected", "video_stream_paused_hidden", "stream_desired_state_idle_released":
		return "stream_closed"
	case "video_socket_connect_attempt", "video_stream_restart", "fresh_video_resume", "cached_video_resume", "viewer_idle_resumed":
		return "stream_started"
	case "keyframe_request", "stream_keyframe_command_queued", "keyframe_while_phone_disconnected", "h264_first_frame_nudge":
		return "keyframe_requested"
	case "keyframe_request_failed", "phone_keyframe_request_failed":
		return "keyframe_failed"
	case "stream_recovery_request", "h264_server_recover_requested", "stream_recovery_requested":
		return "stream_recovery_requested"
	case "activation_resume_start", "activation_resume_retry", "activation_resume_deep_recover", "activation_resume_recovery_decision":
		return "stream_recovery_requested"
	case "activation_resume_fresh_frame", "activation_resume_finish":
		return "stream_recovered"
	case "activation_resume_exhausted", "activation_resume_media_stuck":
		return "stream_failed"
	case "stream_recovery_ignored_no_viewers", "stream_recovery_suppressed_startup_grace", "stream_recovery_suppressed_rate_limit":
		return "stream_recovery_ignored"
	case "activation_resume_merged", "activation_resume_paused", "activation_resume_log_limit":
		return "stream_recovery_ignored"
	case "stream_recover_request_failed":
		return "stream_failed"
	case "stale_video_frames", "server_stale_frames", "loading_over_2s":
		return "stream_stalled"
	case "stream_desired_state_publish_ok":
		return "stream_changed"
	case "phone_stream_disconnected":
		return "phone_disconnected"
	case "stream_command_read_failed":
		return "command_failed"
	case "control_code_submitted":
		return "control_code_requested"
	case "control_code_prepare_complete", "control_code_auto_prepare_complete":
		return "control_code_sent"
	case "control_code_prepare_failed", "control_code_auto_prepare_failed", "control_code_request_failed", "control_code_close_failed", "control_code_result_timeout", "control_code_result_socket_closed":
		return "control_code_failed"
	case "control_code_capture_keepalive":
		return "control_code_capturing"
	case "control_code_close_ignored", "control_code_message_ignored":
		return "control_code_ignored"
	case "spacetime_connect_failed", "spacetime_reconnect_failed", "spacetime_direct_unavailable":
		return "state_failed"
	case "spacetime_client_status":
		return "state_changed"
	case "cleanup_completed":
		return "cleanup_completed"
	}
	if strings.Contains(event, "control_code") {
		if failed {
			return "control_code_failed"
		}
		return event
	}
	if strings.Contains(event, "recover") || strings.Contains(event, "recovery") {
		if failed {
			return "stream_failed"
		}
		return "stream_recovery_requested"
	}
	if strings.Contains(event, "keyframe") {
		if failed {
			return "keyframe_failed"
		}
		return "keyframe_requested"
	}
	if strings.Contains(event, "command") {
		if failed {
			return "command_failed"
		}
		return event
	}
	if strings.Contains(event, "socket") && strings.Contains(event, "open") {
		return "stream_opened"
	}
	if strings.Contains(event, "socket") && strings.Contains(event, "closed") {
		return "stream_closed"
	}
	if strings.Contains(source, "browser") && failed {
		return "browser_failed"
	}
	return event
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
