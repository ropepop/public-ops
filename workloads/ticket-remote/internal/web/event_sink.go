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
	sensitiveEventText           = regexp.MustCompile(`(?i)https?://[^\s"']+|\b(?:bearer|token|password|secret|cookie|authorization|prompt)\b|\b\d{2,}\b`)
	sensitiveClientToken         = regexp.MustCompile(`[A-Za-z0-9+/=_-]{32,}`)
	sensitiveEmail               = regexp.MustCompile(`(?i)[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}`)
	sensitiveIPAddress           = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	sensitivePrivatePath         = regexp.MustCompile(`(?i)(?:/Users/|/home/|/root/|[A-Z]:\\Users\\)[^\s"']*`)
	safeBrowserAssetVersion      = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[a-z0-9][a-z0-9._-]{0,95}$`)
	safeBrowserHDRMapping        = regexp.MustCompile(`^(?:sdr_preserving_(?:highlight_shoulder_v1|chromatic_highlight_shoulder_v2)|black_anchored_hue_expansion_v3|sdr_identity_request_patch_v1)$`)
	safeBrowserHDRRecoveryPhases = map[string]struct{}{
		"active": {}, "backgrounded": {}, "cancelled": {}, "capability_wait": {},
		"fresh_sdr_wait": {}, "initializing": {}, "safe_sdr": {}, "settling": {},
		"version_check": {},
	}
	safeBrowserHDRVersionOutcomes = map[string]struct{}{
		"match": {}, "mismatch": {}, "pending": {}, "request_failed": {}, "server_unavailable": {},
	}
	safeBrowserHDRCapabilityOutcomes = map[string]struct{}{
		"browser_unavailable": {}, "pending": {}, "ready": {}, "server_pending": {},
		"server_ready": {}, "server_unavailable": {},
	}
	safeBrowserHDRRecoveryTriggers = map[string]struct{}{
		"dynamic_range_capability_available": {}, "dynamic_range_capability_unavailable": {},
		"engine_projection_changed": {}, "focus": {}, "foreground": {}, "foreground_pulse_gap": {},
		"lifecycle_resume": {}, "network_online": {}, "pageshow": {}, "pageshow_persisted": {},
		"preference_projection_restore": {}, "preference_user_enable": {}, "projection": {},
		"renderer_failure": {}, "visibility_resume": {},
	}
	safeBrowserHDRReasons = map[string]struct{}{
		"activation_copied": {}, "age_delta": {}, "asset_version_mismatch": {}, "asset_version_mismatch_after_reload": {},
		"browser_capability_unavailable": {}, "client_hdr_failed": {}, "control_code_priority": {},
		"coordinated_frame_superseded": {}, "coordinated_hdr_bypassed": {}, "device_lost": {},
		"disabled": {}, "document_hidden": {}, "dynamic_range_capability_available": {},
		"dynamic_range_capability_unavailable": {}, "engine_projection_changed": {}, "epoch_mismatch": {},
		"failed": {}, "fallback": {}, "first_presented": {}, "focus": {}, "foreground": {},
		"foreground_deadline_exhausted": {}, "foreground_deadline_exhausted_before_present": {},
		"foreground_pulse_gap": {}, "foreground_recovery_failed": {},
		"frame_clone_failed": {}, "fresh": {}, "gpu_completion_cancelled": {}, "gpu_completion_failed": {},
		"gpu_completion_timeout": {}, "gpu_completion_watchdog_failed": {}, "hdr_boost_invalid": {},
		"hdr_canvas_extended_mode_unavailable": {}, "hdr_no_limit_dynamic_range_unavailable": {},
		"hdr_presented_display_refresh_cancelled": {}, "hdr_presented_display_refresh_failed": {},
		"hdr_presented_display_refresh_timeout": {}, "hdr_presented_display_refresh_unavailable": {},
		"lifecycle_backgrounded": {}, "lifecycle_resume": {},
		"main_thread_canvas_unavailable": {}, "missing_watermark": {}, "network_online": {},
		"new_foreground_attempt": {}, "pageshow": {}, "pageshow_persisted": {}, "paint_failed": {},
		"paint_recovery_exhausted": {}, "paint_wait_failed": {}, "paint_wait_timeout": {},
		"preference_disabled": {}, "preference_projection_restore": {}, "preference_user_enable": {},
		"prepared_boost_superseded": {}, "present_completion_failed": {}, "present_completion_timeout": {},
		"present_submit_failed": {}, "present_validation_failed": {}, "presentation_authority_revoked": {},
		"presented": {}, "presented_boost_superseded": {}, "proof_mismatch": {}, "render_failed": {},
		"render_submit_failed": {}, "render_validation_failed": {}, "renderer_compositor_opportunities_unavailable": {},
		"renderer_disposed": {}, "renderer_frame_not_prepared": {}, "renderer_frame_not_presented": {},
		"renderer_init_failed": {}, "renderer_init_timeout": {}, "renderer_init_watchdog_failed": {},
		"renderer_not_ready": {}, "renderer_present_completion_unavailable": {},
		"renderer_present_completion_unconfirmed": {}, "renderer_present_required": {},
		"renderer_present_unavailable": {}, "renderer_retry_exhausted": {}, "renderer_start_failed": {},
		"renderer_failure": {},
		"sdr_stale":        {}, "sequence_lag": {}, "server_capability": {}, "server_capability_unavailable": {},
		"settled_start_retry": {}, "settlement_deadline_exceeded": {}, "settlement_started": {},
		"settlement_watchdog_failed": {}, "shader_compilation_failed": {}, "soft_recovery_deferred": {},
		"soft_recovery_stale": {}, "starting": {}, "superseded": {}, "surface_reveal_blocked": {},
		"target_copied": {}, "uncaptured_gpu_error": {}, "version_request_failed": {},
		"version_server_unavailable": {}, "visibility_resume": {}, "visual_age": {},
		"webgpu_adapter_unavailable": {}, "webgpu_canvas_unavailable": {}, "webgpu_unavailable": {},
		"webgpu_usage_constants_unavailable": {},
	}
	safeBrowserHDRSettlementSources = map[string]struct{}{
		"activation_compositor_completion": {}, "compositor_completion": {}, "external": {},
		"foreground_pulse": {}, "foreground_recovery": {}, "foreground_return": {},
		"sdr_frame": {}, "target_compositor_completion": {}, "target_copy_completion": {},
		"target_gpu_completion": {}, "timer": {},
	}
	safeBrowserHDRPostPresentSources = map[string]struct{}{
		"animation_frame": {}, "cancelled": {}, "failed": {}, "timeout": {}, "unavailable": {},
	}

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
		"experimental_hdr_decode_sample":        {},
		"experimental_hdr_analysis":             {},
		"experimental_hdr_calibration":          {},
		"experimental_hdr_activation_presented": {},
		"experimental_hdr_diagnostic":           {},
		"experimental_hdr_first_image_shown":    {},
		"experimental_hdr_presented":            {},
		"experimental_hdr_renderer_ready":       {},
		"experimental_hdr_session_summary":      {},
		"experimental_hdr_surface_reset":        {},
		"experimental_hdr_surface_transition":   {},
		"experimental_hdr_boost_changed":        {},
		"experimental_hdr_boost_failed":         {},
		"experimental_hdr_gpu_completion":       {},
		"experimental_media_fallback":           {},
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
	detail := safeBrowserClientLogDetail(event, input.Detail)
	body, err := json.Marshal(detail)
	if err != nil {
		return "", nil, "", false
	}
	return event, detail, safeRuntimeLogText(string(body)), true
}

func safeBrowserClientLogDetail(event, raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(raw), &input); err != nil || input == nil {
		return map[string]any{"detail": redactBrowserClientText(raw)}
	}
	out := make(map[string]any, min(len(input), 16))
	for _, key := range browserClientDetailKeyOrder(event, input) {
		value := input[key]
		if len(out) == 16 || key == "detail" || browserClientDetailKeyIsSensitive(key) {
			continue
		}
		cleanKey := safeRuntimeLogKey(key)
		switch typed := value.(type) {
		case nil, bool, float64, json.Number:
			out[cleanKey] = typed
		case string:
			if observable, handled := safeBrowserClientHDRObservableText(input, key, typed); handled {
				out[cleanKey] = observable
			} else {
				out[cleanKey] = safeBrowserClientObservableText(key, typed)
			}
		default:
			out[cleanKey] = "present"
		}
	}
	if value, ok := input["detail"]; ok && len(out) < 16 {
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

func browserClientDetailKeyOrder(event string, input map[string]any) []string {
	phase, _ := input["phase"].(string)
	priority := []string{}
	engine, _ := input["engine"].(string)
	if engine == "client_webgpu_v1" || engine == "client_webgpu_v2" {
		switch {
		case event == "experimental_hdr_first_image_shown" || phase == "first_presented":
			priority = []string{
				"assetVersion", "phase", "attemptId", "recoveryPhase", "selectedDisplayBoost",
				"intendedOutputPeak", "canvasEncoding", "configurationDynamicRangeLimit",
				"continuousSurface", "gpuCompleted", "compositorOpportunitiesCompleted",
				"postPresentSource", "postPresentOpportunityCount",
				"lifecycleGeneration", "canvasGeneration", "rendererGeneration",
			}
		case event == "experimental_hdr_activation_presented" || phase == "edr_activation_presented":
			priority = []string{
				"assetVersion", "phase", "attemptId", "recoveryPhase", "triggerSet",
				"mappingModel", "activationFrame", "activationIntendedOutputPeak",
				"edrRequestPatchIntended", "intendedRequestPatchPeak",
				"gpuCompleted", "postPresentSource", "postPresentOpportunityCount",
				"lifecycleGeneration", "canvasGeneration", "rendererGeneration",
			}
		case phase == "presented":
			priority = []string{
				"assetVersion", "phase", "mappingModel", "selectedDisplayBoost",
				"sourceColorSpace", "canvasEncoding", "configurationColorSpace",
				"configurationDynamicRangeLimit", "gpuCompleted", "compositorOpportunitiesCompleted",
				"postPresentSource", "postPresentOpportunityCount",
				"canvasGeneration", "sequence", "sequenceLag", "ageDeltaMillis",
			}
		case phase == "session_summary":
			priority = []string{
				"assetVersion", "phase", "engine", "pipeline", "selectedDisplayBoost",
				"offered", "rendered", "dropped", "failures", "rendererActive", "surfaceVisible",
				"lifecycleGeneration", "canvasGeneration", "rendererGeneration", "startReason", "reason",
			}
		case phase == "renderer_ready" || phase == "worker_ready":
			priority = []string{
				"assetVersion", "phase", "mappingModel", "colorExpansionExponent", "selectedDisplayBoost", "intendedOutputPeak",
				"edrRequestPatchIntended",
				"canvasEncoding", "configurationColorSpace", "toneMappingMode",
				"configurationDynamicRangeLimit", "continuousSurface", "lifecycleGeneration", "canvasGeneration",
				"rendererGeneration", "startReason",
			}
		case phase == "surface_reset":
			priority = []string{
				"assetVersion", "phase", "engine", "pipeline", "reason", "lifecycleGeneration",
				"canvasGeneration", "rendererGeneration", "canvasReplaced", "continuousSurface", "startReason", "retryOrdinal",
			}
		case phase == "renderer_init_timeout":
			priority = []string{
				"assetVersion", "phase", "engine", "attemptId", "recoveryPhase", "triggerSet",
				"rendererInitTimeoutMillis", "rendererInitElapsedMillis", "rendererInitCheckSource",
				"surfaceVisible", "lifecycleGeneration", "canvasGeneration", "rendererGeneration",
				"retryOrdinal", "startReason", "reason",
			}
		case phase == "surface_transition":
			priority = []string{
				"assetVersion", "phase", "engine", "pipeline", "selectedDisplayBoost", "toSurface", "reason",
				"presentationState", "surfaceTransitions", "sequenceLag", "ageDeltaMillis", "fallbackDurationMillis",
				"lifecycleGeneration", "canvasGeneration", "rendererGeneration", "startReason",
			}
		case phase == "boost_changed":
			priority = []string{
				"assetVersion", "phase", "engine", "pipeline", "mappingModel", "selectedDisplayBoost",
				"previousDisplayBoost", "intendedOutputPeak", "canvasEncoding",
				"configurationDynamicRangeLimit", "continuousSurface", "surfaceVisible", "presentationState",
				"lifecycleGeneration", "canvasGeneration",
			}
		case phase == "boost_change_failed":
			priority = []string{
				"assetVersion", "phase", "engine", "pipeline", "selectedDisplayBoost", "requestedDisplayBoost",
				"canvasEncoding", "configurationDynamicRangeLimit", "continuousSurface", "surfaceVisible",
				"reason", "lifecycleGeneration", "canvasGeneration",
			}
		case phase == "gpu_completion":
			priority = []string{
				"assetVersion", "phase", "mappingModel", "selectedDisplayBoost", "canvasEncoding",
				"edrRequestPatchIntended", "intendedRequestPatchPeak", "sourceColorSpace",
				"gpuCompleted", "completionMillis", "lifecycleGeneration", "canvasGeneration",
				"rendererGeneration", "startReason", "sequence", "ageDeltaMillis",
			}
		case phase == "foreground_recovery":
			priority = []string{
				"assetVersion", "phase", "engine", "attemptId", "recoveryPhase", "triggerSet",
				"reason", "versionOutcome", "capabilityOutcome", "streamEpoch",
				"lifecycleGeneration", "canvasGeneration", "rendererGeneration", "retryOrdinal",
				"presentationRegionGeneration", "presentationRegionBlocked", "presentationRecoveryPending",
				"foregroundPaintConfirmed", "foregroundStabilityWaitMillis",
				"surfaceVisible", "presentationState",
			}
		case phase == "settlement_started":
			priority = []string{
				"assetVersion", "phase", "engine", "attemptId", "recoveryPhase", "triggerSet",
				"streamEpoch", "lifecycleGeneration", "canvasGeneration", "rendererGeneration",
				"retryOrdinal", "settlementTimeoutMillis", "settlementElapsedMillis",
				"settlementPending", "presentationState", "surfaceVisible",
			}
		case phase == "compositor_settlement_started":
			priority = []string{
				"assetVersion", "phase", "engine", "attemptId", "recoveryPhase", "triggerSet",
				"streamEpoch", "lifecycleGeneration", "canvasGeneration", "rendererGeneration",
				"retryOrdinal", "settlementDeadlineMillis", "postPresentOpportunityTarget",
				"presentationState", "surfaceVisible", "startReason",
			}
		case phase == "settlement_deadline_exceeded":
			priority = []string{
				"assetVersion", "phase", "attemptId", "recoveryPhase", "triggerSet", "reason",
				"streamEpoch", "lifecycleGeneration", "canvasGeneration", "rendererGeneration",
				"retryOrdinal", "settlementTimeoutMillis", "settlementElapsedMillis",
				"settlementCheckSource", "settlementTimedOut", "surfaceVisible",
			}
		case phase == "compositor_settlement_result":
			priority = []string{
				"assetVersion", "phase", "attemptId", "recoveryPhase", "triggerSet", "streamEpoch",
				"lifecycleGeneration", "canvasGeneration", "rendererGeneration", "retryOrdinal",
				"settlementDeadlineMillis", "settlementTimedOut", "postPresentSource",
				"postPresentOpportunityCount", "compositorOpportunitiesCompleted", "surfaceVisible",
			}
		case phase == "gpu_completion_timeout":
			priority = []string{
				"assetVersion", "phase", "engine", "attemptId", "recoveryPhase", "triggerSet",
				"streamEpoch", "lifecycleGeneration", "canvasGeneration", "rendererGeneration",
				"retryOrdinal", "gpuCompletionTimeoutMillis", "epoch", "sequence",
				"presentationOrdinal", "startReason",
			}
		case phase == "fallback":
			priority = []string{
				"assetVersion", "phase", "engine", "attemptId", "recoveryPhase", "reason",
				"surfaceVisible", "presentationState", "failures", "lifecycleGeneration",
				"canvasGeneration", "rendererGeneration", "retryOrdinal", "settlementPending",
				"settlementElapsedMillis", "startReason",
			}
		}
	}
	ordered := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, key := range priority {
		if _, ok := input[key]; !ok {
			continue
		}
		ordered = append(ordered, key)
		seen[key] = struct{}{}
	}
	remaining := make([]string, 0, len(input)-len(ordered))
	for key := range input {
		if _, ok := seen[key]; !ok {
			remaining = append(remaining, key)
		}
	}
	slices.Sort(remaining)
	return append(ordered, remaining...)
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
	if key == "accountscopeid" || key == "code" || key == "image" || key == "payload" || key == "raw" {
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

func safeBrowserEnum(value string, allowed map[string]struct{}) string {
	value = strings.TrimSpace(value)
	if _, ok := allowed[value]; ok {
		return value
	}
	return "[redacted]"
}

func safeBrowserHDRTriggerSet(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 8 {
		return "[redacted]"
	}
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part != strings.TrimSpace(part) {
			return "[redacted]"
		}
		if _, ok := safeBrowserHDRRecoveryTriggers[part]; !ok {
			return "[redacted]"
		}
		if _, duplicate := seen[part]; duplicate {
			return "[redacted]"
		}
		seen[part] = struct{}{}
	}
	return strings.Join(parts, ",")
}

func safeBrowserHDRReason(value string) string {
	value = strings.TrimSpace(value)
	if _, ok := safeBrowserHDRReasons[value]; ok {
		return value
	}
	const retryPrefix = "surface_retry_scheduled:"
	if strings.HasPrefix(value, retryPrefix) {
		if _, ok := safeBrowserHDRReasons[strings.TrimPrefix(value, retryPrefix)]; ok {
			return value
		}
	}
	return "[redacted]"
}

func safeBrowserHDRStartReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "unknown" || value == "connect" || value == "capability_retry" {
		return value
	}
	if _, ok := safeBrowserHDRRecoveryTriggers[value]; ok {
		return value
	}
	const foregroundPrefix = "foreground_recovery:"
	if strings.HasPrefix(value, foregroundPrefix) {
		if _, ok := safeBrowserHDRRecoveryTriggers[strings.TrimPrefix(value, foregroundPrefix)]; ok {
			return value
		}
		return "[redacted]"
	}
	const rendererRetryPrefix = "renderer_retry:"
	if strings.HasPrefix(value, rendererRetryPrefix) {
		if _, ok := safeBrowserHDRReasons[strings.TrimPrefix(value, rendererRetryPrefix)]; ok {
			return value
		}
		return "[redacted]"
	}
	return "[redacted]"
}

func safeBrowserClientHDRObservableText(input map[string]any, key, value string) (string, bool) {
	engine, _ := input["engine"].(string)
	if engine != "client_webgpu_v1" && engine != "client_webgpu_v2" {
		return "", false
	}
	switch key {
	case "recoveryPhase":
		return safeBrowserEnum(value, safeBrowserHDRRecoveryPhases), true
	case "versionOutcome":
		return safeBrowserEnum(value, safeBrowserHDRVersionOutcomes), true
	case "capabilityOutcome":
		return safeBrowserEnum(value, safeBrowserHDRCapabilityOutcomes), true
	case "triggerSet":
		return safeBrowserHDRTriggerSet(value), true
	case "reason":
		return safeBrowserHDRReason(value), true
	case "startReason":
		return safeBrowserHDRStartReason(value), true
	case "settlementCheckSource":
		return safeBrowserEnum(value, safeBrowserHDRSettlementSources), true
	case "postPresentSource":
		return safeBrowserEnum(value, safeBrowserHDRPostPresentSources), true
	default:
		return "", false
	}
}

func safeBrowserClientObservableText(key, value string) string {
	value = strings.TrimSpace(value)
	switch key {
	case "assetVersion":
		if safeBrowserAssetVersion.MatchString(value) {
			return value
		}
		return redactBrowserClientText(value)
	case "mappingModel":
		value = strings.ToLower(value)
		if safeBrowserHDRMapping.MatchString(value) {
			return value
		}
		return "[redacted]"
	case "sourceColorSpace":
		// Continue below with a field-specific standards vocabulary.
	default:
		return redactBrowserClientText(value)
	}
	// VideoFrame.colorSpace uses a small standards-defined vocabulary. Preserve
	// those tokens as an observable rendering fact, but reject arbitrary client
	// text so this diagnostic cannot become a channel for private values.
	parts := strings.Split(strings.ToLower(value), ";")
	if len(parts) != 4 {
		return "[redacted]"
	}
	allowed := map[string]map[string]struct{}{
		"primaries": {
			"bt709": {}, "bt470bg": {}, "smpte170m": {}, "smpte240m": {}, "film": {},
			"bt2020": {}, "smpte-st-428-1": {}, "smpte-rp-431-2": {}, "smpte-eg-432-1": {},
			"ebu3213e": {}, "unknown": {},
		},
		"transfer": {
			"bt709": {}, "smpte170m": {}, "smpte240m": {}, "linear": {},
			"iec61966-2-4": {}, "bt1361-ecg": {}, "iec61966-2-1": {}, "bt2020-10": {},
			"bt2020-12": {}, "smpte-st-2084": {}, "smpte-st-428-1": {}, "arib-std-b67": {},
			"pq": {}, "hlg": {}, "unknown": {},
		},
		"matrix": {
			"rgb": {}, "bt709": {}, "fcc": {}, "bt470bg": {}, "smpte170m": {}, "smpte240m": {},
			"ycgco": {}, "bt2020-ncl": {}, "bt2020-cl": {}, "smpte2085": {},
			"chroma-derived-ncl": {}, "chroma-derived-cl": {}, "ictcp": {}, "unknown": {},
		},
		"range": {"full": {}, "limited": {}, "unknown": {}},
	}
	for index, name := range []string{"primaries", "transfer", "matrix", "range"} {
		pair := strings.SplitN(parts[index], "=", 2)
		if len(pair) != 2 || pair[0] != name {
			return "[redacted]"
		}
		if _, ok := allowed[name][pair[1]]; !ok {
			return "[redacted]"
		}
	}
	return strings.Join(parts, ";")
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
