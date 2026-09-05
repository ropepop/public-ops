package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

const (
	backgroundKeyframeMinInterval      = 2500 * time.Millisecond
	backgroundKeyframeRequirementLimit = 64
)

type pendingBackgroundKeyframeRequest struct {
	reason         string
	requirement    string
	startupTraceID []string
}

func (s *Server) activePhoneBackend() config.PhoneBackend {
	s.backendMu.RLock()
	defer s.backendMu.RUnlock()
	return config.PhoneBackend{
		ID:         s.cfg.Phone.BackendID,
		AttachName: s.cfg.Phone.AttachName,
		BaseURL:    s.cfg.Phone.BaseURL,
	}
}

func (s *Server) configuredPhoneBackends() []config.PhoneBackend {
	s.backendMu.RLock()
	defer s.backendMu.RUnlock()
	return append([]config.PhoneBackend(nil), s.cfg.Phone.Backends...)
}

func (s *Server) setActivePhoneBackend(backend config.PhoneBackend) {
	s.backendMu.Lock()
	defer s.backendMu.Unlock()
	s.cfg.Phone.BackendID = backend.ID
	s.cfg.Phone.AttachName = backend.AttachName
	s.cfg.Phone.BaseURL = strings.TrimRight(backend.BaseURL, "/")
}

func (s *Server) withActivePhoneBackend(snapshot state.Snapshot, health phone.Health) state.Snapshot {
	backend := s.activePhoneBackend()
	if backend.ID == "" {
		return snapshot
	}
	desiredState := health.StreamState
	if desiredState == "" {
		desiredState = "idle"
	}
	if snapshot.Phone != nil && snapshot.Phone.ID == backend.ID {
		phoneState := *snapshot.Phone
		if phoneState.AttachName == "" {
			phoneState.AttachName = backend.AttachName
		}
		if phoneState.BaseURL == "" {
			phoneState.BaseURL = backend.BaseURL
		}
		if phoneState.DesiredState == "" {
			phoneState.DesiredState = desiredState
		}
		if phoneState.LastError == "" {
			phoneState.LastError = health.LastError
		}
		if phoneState.LastSeenAt == "" {
			phoneState.LastSeenAt = health.LastSeenAt
		}
		snapshot.Phone = &phoneState
		return snapshot
	}
	snapshot.Phone = &state.PhoneBackend{
		ID:           backend.ID,
		AttachName:   backend.AttachName,
		BaseURL:      backend.BaseURL,
		DesiredState: desiredState,
		LastError:    health.LastError,
		LastSeenAt:   health.LastSeenAt,
	}
	return snapshot
}

func (s *Server) withFreshActivePhoneHealth(ctx context.Context, snapshot state.Snapshot, health phone.Health) state.Snapshot {
	snapshot = s.withActivePhoneBackend(snapshot, health)
	backend := s.activePhoneBackend()
	if backend.ID == "" || strings.TrimSpace(backend.BaseURL) == "" || snapshot.Phone == nil || snapshot.Phone.ID != backend.ID {
		return snapshot
	}
	healthJSON, err := fetchPhoneBackendHealthJSON(ctx, backend)
	if err != nil {
		s.recordRuntimeErrorAsync("admin_phone_health_refresh_failed", backend.ID, err, map[string]any{"backendId": backend.ID})
		return snapshot
	}
	phoneState := *snapshot.Phone
	phoneState.HealthJSON = healthJSON
	phoneState.LastError = ""
	phoneState.LastSeenAt = time.Now().UTC().Format(time.RFC3339)
	snapshot.Phone = &phoneState
	return snapshot
}

func fetchPhoneBackendHealthJSON(ctx context.Context, backend config.PhoneBackend) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(backend.BaseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("base URL is empty")
	}
	if healthJSON, err := fetchPhoneBackendHealthJSONAt(ctx, baseURL+"/api/v1/upstream/health"); err == nil {
		return healthJSON, nil
	}
	return fetchPhoneBackendHealthJSONAt(ctx, baseURL+"/api/v1/health")
}

func fetchPhoneBackendHealthJSONAt(ctx context.Context, healthURL string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("health returned %d", resp.StatusCode)
	}
	if !json.Valid(body) {
		return "", fmt.Errorf("health returned invalid JSON")
	}
	return string(body), nil
}

func (s *Server) cachePhoneStatusUpdate(input state.PhoneInput, health phone.Health) {
	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	snapshot, ok := s.cachedSnapshot(now)
	if !ok || snapshot.Ticket.ID == "" {
		return
	}
	phoneID := strings.TrimSpace(input.BackendID)
	if phoneID == "" {
		phoneID = "pixel"
	}
	attachName := strings.TrimSpace(input.AttachName)
	if attachName == "" {
		attachName = phoneID
	}
	desiredState := strings.TrimSpace(input.DesiredState)
	if desiredState == "" {
		desiredState = "idle"
	}
	snapshot.Phone = &state.PhoneBackend{
		ID:           phoneID,
		AttachName:   attachName,
		BaseURL:      input.BaseURL,
		DesiredState: desiredState,
		HealthJSON:   input.HealthJSON,
		LastError:    input.LastError,
		LastSeenAt:   now.UTC().Format(time.RFC3339),
	}
	snapshot = s.withActivePhoneBackend(snapshot, health)
	s.cacheSnapshot(snapshot)
}

func (s *Server) probePhoneBackend(ctx context.Context, backend config.PhoneBackend) (bool, int, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(backend.BaseURL), "/")
	if baseURL == "" {
		return false, 0, fmt.Errorf("base URL is empty")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, baseURL+"/api/v1/health", nil)
	if err != nil {
		return false, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, resp.StatusCode, fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return true, resp.StatusCode, nil
}

func (s *Server) maybeRequestPhoneStart(data map[string]any, reason string) {
	if data == nil {
		return
	}
	if streamActive, _ := data["streamActive"].(bool); streamActive {
		return
	}
	relayHealth := s.relay.Snapshot()
	if relayHealth.Viewers <= 0 || !relayHealth.Desired || !relayHealth.Connected {
		return
	}
	now := time.Now()
	s.phoneStartMu.Lock()
	if !s.lastPhoneStartAttempt.IsZero() && now.Sub(s.lastPhoneStartAttempt) < 10*time.Second {
		s.phoneStartMu.Unlock()
		return
	}
	s.lastPhoneStartAttempt = now
	s.phoneStartMu.Unlock()
	s.appendStreamCommandAsync("start", reason, map[string]any{
		"source": "ticket_remote",
	}, streamCommandTTL)
}

func (s *Server) wakePhoneStreamFromVideoSocketOpen(reason, startupTraceCorrelationID, originatingTraceID string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "video_socket_open"
	}
	relayHealth := s.relay.Snapshot()
	if relayHealth.Viewers <= 0 {
		return
	}
	if !relayHealth.Desired || !relayHealth.Connected {
		s.direct.recordStartupPhaseForTrace(originatingTraceID, "video_socket_wake_start_queued", reason)
		s.queuePrewarmStreamCommands(reason, startupTraceCorrelationID, originatingTraceID)
		s.relay.EnsureActive("video_socket_open:" + reason)
		return
	}
	if phoneRelayNeedsSocketWakeKeyframe(relayHealth) {
		s.direct.recordStartupPhaseForTrace(originatingTraceID, "video_socket_wake_keyframe_queued", reason)
		s.requestPhoneKeyframe(reason, startupTraceCorrelationID, originatingTraceID)
	}
}

func phoneRelayNeedsSocketWakeKeyframe(relayHealth phone.Health) bool {
	return strings.TrimSpace(relayHealth.StreamState) != "streaming"
}

func (s *Server) requestPhoneKeyframe(reason string, startupTraceID ...string) {
	s.requestPhoneKeyframeWithRequirement(reason, "", startupTraceID...)
}

// requestPhoneConfigKeyframe gives a decoder-reset configuration one fresh
// keyframe request even when an unrelated background nudge just ran. The
// epoch-scoped requirement still shares the ordinary in-flight gate and
// cooldown with repeated copies of the same configuration.
func (s *Server) requestPhoneConfigKeyframe(streamEpoch uint64) {
	requirement := phoneConfigKeyframeRequirement(streamEpoch)
	s.requestPhoneKeyframeWithRequirement("phone_config_active_viewer", requirement)
}

func phoneConfigKeyframeRequirement(streamEpoch uint64) string {
	return fmt.Sprintf("phone_config_active_viewer:%d", streamEpoch)
}

func (s *Server) requestPhoneKeyframeWithRequirement(reason, requirement string, startupTraceID ...string) {
	if !perViewerKeyframeRequired(reason) && s.liveStreamSuppressesBackgroundCommand("keyframe", reason, time.Now()) {
		return
	}
	background := backgroundKeyframeDedupEligible(reason)
	if background && strings.TrimSpace(requirement) != "" {
		request := pendingBackgroundKeyframeRequest{
			reason:         reason,
			requirement:    strings.TrimSpace(requirement),
			startupTraceID: append([]string(nil), startupTraceID...),
		}
		accepted, dispatchNow := s.enqueueRequiredBackgroundKeyframe(request, time.Now())
		if !accepted {
			s.direct.recordRelayTelemetry("keyframe_duplicate_suppressed", cleanStreamControlText(reason, "keyframe"))
			return
		}
		s.direct.recordKeyframeRequested()
		if dispatchNow {
			s.dispatchBackgroundKeyframe(request)
		}
		return
	}
	if background && !s.beginBackgroundKeyframe(time.Now(), requirement) {
		s.direct.recordRelayTelemetry("keyframe_duplicate_suppressed", cleanStreamControlText(reason, "keyframe"))
		return
	}
	s.direct.recordKeyframeRequested()
	go func() {
		if background {
			defer s.finishBackgroundKeyframe()
		}
		if err := s.sendPhoneKeyframe(reason, startupTraceID...); err != nil {
			s.recordRuntimeErrorAsync("phone_keyframe_request_failed", reason, err, map[string]any{"reason": reason})
		}
	}()
}

func (s *Server) enqueueRequiredBackgroundKeyframe(request pendingBackgroundKeyframeRequest, now time.Time) (bool, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	request.requirement = strings.TrimSpace(request.requirement)
	if request.requirement == "" {
		return false, false
	}
	s.backgroundKeyframeMu.Lock()
	defer s.backgroundKeyframeMu.Unlock()
	if s.backgroundKeyframeNeeds == nil {
		s.backgroundKeyframeNeeds = map[string]time.Time{}
	}
	if s.backgroundKeyframeActive == nil {
		s.backgroundKeyframeActive = map[string]struct{}{}
	}
	for key, requestedAt := range s.backgroundKeyframeNeeds {
		if _, active := s.backgroundKeyframeActive[key]; active {
			continue
		}
		if !now.Before(requestedAt) && now.Sub(requestedAt) >= backgroundKeyframeMinInterval {
			delete(s.backgroundKeyframeNeeds, key)
		}
	}
	if _, active := s.backgroundKeyframeActive[request.requirement]; active {
		return false, false
	}
	if requestedAt, exists := s.backgroundKeyframeNeeds[request.requirement]; exists &&
		(now.Before(requestedAt) || now.Sub(requestedAt) < backgroundKeyframeMinInterval) {
		return false, false
	}
	if len(s.backgroundKeyframeNeeds) >= backgroundKeyframeRequirementLimit || len(s.backgroundKeyframePending) >= backgroundKeyframeRequirementLimit {
		return false, false
	}
	s.backgroundKeyframeNeeds[request.requirement] = now
	s.backgroundKeyframeActive[request.requirement] = struct{}{}
	s.lastBackgroundKeyframeAt = now
	if s.backgroundKeyframeInFlight {
		s.backgroundKeyframePending = append(s.backgroundKeyframePending, request)
		return true, false
	}
	s.backgroundKeyframeInFlight = true
	return true, true
}

func (s *Server) dispatchBackgroundKeyframe(request pendingBackgroundKeyframeRequest) {
	go func() {
		defer s.finishBackgroundKeyframe(request.requirement)
		if err := s.sendPhoneKeyframe(request.reason, request.startupTraceID...); err != nil {
			s.recordRuntimeErrorAsync("phone_keyframe_request_failed", request.reason, err, map[string]any{"reason": request.reason})
		}
	}()
}

func (s *Server) requestPhoneKeyframeNow(reason string) error {
	if !perViewerKeyframeRequired(reason) && s.liveStreamSuppressesBackgroundCommand("keyframe", reason, time.Now()) {
		return nil
	}
	background := backgroundKeyframeDedupEligible(reason)
	if background && !s.beginBackgroundKeyframe(time.Now()) {
		s.direct.recordRelayTelemetry("keyframe_duplicate_suppressed", cleanStreamControlText(reason, "keyframe"))
		return nil
	}
	if background {
		defer s.finishBackgroundKeyframe()
	}
	s.direct.recordKeyframeRequested()
	return s.sendPhoneKeyframe(reason)
}

func (s *Server) beginBackgroundKeyframe(now time.Time, requirement ...string) bool {
	if now.IsZero() {
		now = time.Now()
	}
	requirementKey := ""
	if len(requirement) > 0 {
		requirementKey = strings.TrimSpace(requirement[0])
	}
	s.backgroundKeyframeMu.Lock()
	defer s.backgroundKeyframeMu.Unlock()
	if s.backgroundKeyframeInFlight {
		return false
	}
	insideGlobalCooldown := !s.lastBackgroundKeyframeAt.IsZero() &&
		(now.Before(s.lastBackgroundKeyframeAt) || now.Sub(s.lastBackgroundKeyframeAt) < backgroundKeyframeMinInterval)
	if requirementKey == "" {
		if insideGlobalCooldown {
			return false
		}
	} else {
		if s.backgroundKeyframeNeeds == nil {
			s.backgroundKeyframeNeeds = map[string]time.Time{}
		}
		for key, requestedAt := range s.backgroundKeyframeNeeds {
			if !now.Before(requestedAt) && now.Sub(requestedAt) >= backgroundKeyframeMinInterval {
				delete(s.backgroundKeyframeNeeds, key)
			}
		}
		if requestedAt, exists := s.backgroundKeyframeNeeds[requirementKey]; exists &&
			(now.Before(requestedAt) || now.Sub(requestedAt) < backgroundKeyframeMinInterval) {
			return false
		}
		if len(s.backgroundKeyframeNeeds) >= backgroundKeyframeRequirementLimit {
			return false
		}
	}
	s.backgroundKeyframeInFlight = true
	s.lastBackgroundKeyframeAt = now
	if requirementKey != "" {
		s.backgroundKeyframeNeeds[requirementKey] = now
	}
	return true
}

func (s *Server) finishBackgroundKeyframe(requirement ...string) {
	completedRequirement := ""
	if len(requirement) > 0 {
		completedRequirement = strings.TrimSpace(requirement[0])
	}
	s.backgroundKeyframeMu.Lock()
	if completedRequirement != "" {
		delete(s.backgroundKeyframeActive, completedRequirement)
	}
	if len(s.backgroundKeyframePending) == 0 {
		s.backgroundKeyframeInFlight = false
		s.backgroundKeyframeMu.Unlock()
		return
	}
	next := s.backgroundKeyframePending[0]
	s.backgroundKeyframePending[0] = pendingBackgroundKeyframeRequest{}
	s.backgroundKeyframePending = s.backgroundKeyframePending[1:]
	if len(s.backgroundKeyframePending) == 0 {
		s.backgroundKeyframePending = nil
	}
	s.backgroundKeyframeMu.Unlock()
	s.dispatchBackgroundKeyframe(next)
}

// A newly connected browser may have missed the short-lived cached keyframe
// even though another viewer keeps the upstream stream live. That viewer
// still needs one decodable keyframe; the existing background gate below
// coalesces concurrent joins so this does not become a keyframe storm.
func perViewerKeyframeRequired(reason string) bool {
	cleanReason := strings.ToLower(cleanStreamControlText(reason, ""))
	switch cleanReason {
	case "browser_warm_prewarm", "browser_video_provisional_config", "browser_video_config_needed", "phone_config_active_viewer":
		return true
	default:
		return false
	}
}

func backgroundKeyframeDedupEligible(reason string) bool {
	cleanReason := strings.ToLower(cleanStreamControlText(reason, "keyframe"))
	if strings.Contains(cleanReason, "control_code") {
		return false
	}
	return backgroundStreamCommandRequiresDemand("keyframe", cleanReason)
}

func (s *Server) sendPhoneKeyframe(reason string, startupTraceID ...string) error {
	relayHealth := s.relay.Snapshot()
	if relayHealth.Viewers > 0 && !relayHealth.Connected {
		s.direct.recordRelayTelemetry("keyframe_waiting_phone_connect", reason)
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "warn", "keyframe_while_phone_disconnected", cleanStreamControlText(reason, "keyframe"), map[string]any{
			"reason":       reason,
			"viewerCount":  relayHealth.Viewers,
			"relayDesired": relayHealth.Desired,
			"streamState":  relayHealth.StreamState,
			"lastError":    relayHealth.LastError,
		})
		if cleanStreamControlText(reason, "keyframe") == "browser_video_provisional_config" {
			return nil
		}
	}
	originatingTraceID := ""
	traceContextProvided := len(startupTraceID) > 1
	if len(startupTraceID) > 1 {
		originatingTraceID = strings.TrimSpace(startupTraceID[1])
	}
	if originatingTraceID != "" {
		s.direct.recordStartupPhaseForTrace(originatingTraceID, "keyframe_command_queued", reason)
	} else if !traceContextProvided {
		s.direct.recordStartupPhase("keyframe_command_queued", reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := phoneKeyframeCommandPayload(reason, startupTraceID...)
	_, err := s.appendStreamCommand(ctx, "keyframe", reason, payload, streamKeyframeCommandTTL, originatingTraceID)
	return err
}

func phoneKeyframeCommandPayload(reason string, startupTraceID ...string) map[string]any {
	source := "ticket_remote"
	if perViewerKeyframeRequired(reason) {
		source = "browser"
	}
	payload := map[string]any{
		"source": source,
	}
	if len(startupTraceID) > 0 && strings.TrimSpace(startupTraceID[0]) != "" {
		payload["traceId"] = startupTraceID[0]
	}
	return payload
}

func (s *Server) requestPhoneRecovery(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "browser_stream_recovery"
	}
	now := time.Now()
	relayHealth := s.relay.Snapshot()
	if relayHealth.Viewers <= 0 {
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_recovery_ignored_no_viewers", cleanStreamControlText(reason, "recover"), map[string]any{
			"reason": reason,
		})
		return
	}
	if s.liveStreamSuppressesBackgroundCommand("recover_stream", reason, now) {
		s.direct.recordRelayTelemetry("stream_recovery_live_suppressed", reason)
		return
	}
	s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_recovery_requested", cleanStreamControlText(reason, "recover"), map[string]any{
		"reason":      reason,
		"viewerCount": relayHealth.Viewers,
		"connected":   relayHealth.Connected,
		"desired":     relayHealth.Desired,
		"streamState": relayHealth.StreamState,
	})
	if s.direct.startupGraceActive(now) && !relayHealth.Connected {
		s.direct.recordRelayTelemetry("stream_recovery_start_suppressed", reason)
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_recovery_suppressed_startup_grace", cleanStreamControlText(reason, "recover"), map[string]any{
			"reason":    reason,
			"connected": relayHealth.Connected,
		})
		return
	}
	if !s.beginStreamAutoRecovery(reason, now) {
		s.direct.recordRelayTelemetry("stream_recovery_command_suppressed", reason)
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_recovery_suppressed_rate_limit", cleanStreamControlText(reason, "recover"), map[string]any{
			"reason": reason,
		})
		return
	}
	if relayHealth.Connected && s.connectedRecoveryShouldStayKeyframeOnly(reason, now) {
		s.direct.recordRelayTelemetry("stream_recovery_keyframe_only", reason)
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_recovery_keyframe_only", cleanStreamControlText(reason, "recover"), map[string]any{
			"reason":      reason,
			"viewerCount": relayHealth.Viewers,
			"streamState": relayHealth.StreamState,
		})
		s.requestPhoneKeyframe("stream_recovery:" + reason)
		return
	}
	if relayHealth.Connected {
		s.appendStreamRecoveryCommandAsync(reason)
		s.relay.Reconnect("media_recovery:" + reason)
		return
	}
	s.appendStreamRecoveryCommandAsync(reason)
	s.relay.EnsureActive("browser_recovery:" + reason)
}

func (s *Server) liveStreamSuppressesBackgroundCommand(commandType string, reason string, now time.Time) bool {
	if s == nil || s.direct == nil || s.relay == nil {
		return false
	}
	cleanReason := strings.ToLower(cleanStreamControlText(reason, ""))
	if strings.Contains(cleanReason, "control_code") {
		return false
	}
	if commandType != "keyframe" && commandType != "recover_stream" && commandType != "start" {
		return false
	}
	status := s.direct.streamStatus(now, s.relay.Snapshot())
	live, _ := status["live"].(bool)
	activeClients := uint32FromAny(status["activeVideoClients"])
	if live && activeClients > 0 {
		s.direct.recordRelayTelemetry(commandType+"_live_suppressed", cleanReason)
		return true
	}
	return false
}

func (s *Server) connectedRecoveryShouldStayKeyframeOnly(reason string, now time.Time) bool {
	if s.direct.startupGraceActive(now) {
		return true
	}
	reason = strings.ToLower(cleanStreamControlText(reason, "recover"))
	for _, token := range []string{
		"activation",
		"cached",
		"cold_open",
		"first_frame",
		"fresh_media",
		"fresh_socket",
		"h264_start",
		"hard_recover",
		"pageshow",
		"resume",
	} {
		if strings.Contains(reason, token) {
			return true
		}
	}
	return false
}
