package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ticketremote/internal/state"
)

const (
	streamControlWriteTimeout      = 2 * time.Second
	streamCommandTTL               = 2 * time.Minute
	streamKeyframeCommandTTL       = 30 * time.Second
	latestTicketReselectCommandTTL = 10 * time.Minute
)

func (s *Server) publishStreamDesiredStateAsync(active bool, viewerCount int, reason string, source string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		if err := s.publishStreamDesiredState(ctx, active, viewerCount, reason, source); err != nil {
			s.recordRuntimeErrorAsync("stream_desired_state_publish_failed", source, err, map[string]any{
				"reason":      reason,
				"active":      active,
				"viewerCount": viewerCount,
			})
		}
	}()
}

func (s *Server) publishStreamDesiredState(ctx context.Context, active bool, viewerCount int, reason string, source string) error {
	if s.store == nil {
		return nil
	}
	if viewerCount < 0 {
		viewerCount = 0
	}
	backend := s.activePhoneBackend()
	now := time.Now()
	revision := streamControlRevision(now)
	err := s.store.SetStreamDesiredState(ctx, state.StreamDesiredStateInput{
		TicketID:      s.cfg.TicketID,
		BackendID:     backend.ID,
		DesiredActive: active,
		ViewerCount:   uint32(viewerCount),
		Reason:        cleanStreamControlText(reason, "stream_state"),
		Revision:      revision,
		UpdatedBy:     cleanStreamControlText(source, "ticket_remote"),
		Now:           now,
	})
	if err != nil {
		return err
	}
	s.recordRuntimeEventForSourceAsync(cleanStreamControlText(source, "ticket_remote"), "info", "stream_desired_state_publish_ok", revision, map[string]any{
		"desiredActive": active,
		"viewerCount":   viewerCount,
		"backendId":     backend.ID,
		"reason":        reason,
		"revision":      revision,
	})
	return nil
}

func (s *Server) publishRelayCurrentReportAsync(reason string) {
	if s == nil || s.relayReportWake == nil {
		return
	}
	reason = cleanStreamControlText(reason, "relay_state_changed")
	select {
	case s.relayReportWake <- reason:
	default:
		// A report is already queued. The shared reporter will publish the
		// latest aggregate state after the short coalescing window.
	}
}

func (s *Server) relayReportLoop(ctx context.Context) {
	defer close(s.relayReportDone)
	heartbeat := time.NewTicker(relayReportHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case reason := <-s.relayReportWake:
			reason = s.coalesceRelayReportReason(ctx, reason)
			s.publishRelayCurrentReportFromLoop(ctx, reason)
		case <-heartbeat.C:
			if s.streamDemandStillPresent() {
				s.publishRelayCurrentReportFromLoop(ctx, "video_socket_heartbeat")
			}
		}
	}
}

func (s *Server) coalesceRelayReportReason(ctx context.Context, reason string) string {
	timer := time.NewTimer(relayReportCoalesceWindow)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return reason
		case next := <-s.relayReportWake:
			reason = next
		case <-timer.C:
			return reason
		}
	}
}

func (s *Server) publishRelayCurrentReportFromLoop(ctx context.Context, reason string) {
	writeCtx, cancel := context.WithTimeout(ctx, streamControlWriteTimeout)
	defer cancel()
	if err := s.publishRelayCurrentReport(writeCtx, time.Now(), reason); err != nil && ctx.Err() == nil {
		s.recordRuntimeErrorAsync("relay_report_publish_failed", reason, err, map[string]any{"reason": reason})
	}
}

func (s *Server) publishRelayCurrentReport(ctx context.Context, now time.Time, reason string) error {
	if s.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	backend := s.activePhoneBackend()
	status := s.direct.streamStatus(now, s.relay.Snapshot())
	status["reportReason"] = cleanStreamControlText(reason, "relay_report")
	for key, value := range s.streamAutoRecoveryStatus(now) {
		status[key] = value
	}
	s.noteRelayProductState(status, now, reason)
	statusJSON, err := json.Marshal(compactRelayCurrentReportStatus(status))
	if err != nil {
		return err
	}
	return s.store.UpdateRelayCurrentReport(ctx, state.RelayCurrentReportInput{
		TicketID:        s.cfg.TicketID,
		BackendID:       backend.ID,
		VideoClients:    uint32FromAny(status["activeVideoClients"]),
		StreamVerdict:   cleanStreamControlText(stringFromAny(status["streamVerdict"]), "unknown"),
		LastFrameAt:     stringFromAny(status["lastFrameAt"]),
		FramesForwarded: stringFromAny(status["framesForwarded"]),
		StatusJSON:      string(statusJSON),
		Now:             now,
	})
}

func compactRelayCurrentReportStatus(status map[string]any) map[string]any {
	compact := make(map[string]any, len(status))
	for key, value := range status {
		if key == "startupTrace" || strings.HasSuffix(key, "AgoMillis") {
			continue
		}
		compact[key] = value
	}
	return compact
}

func (s *Server) noteRelayProductState(status map[string]any, now time.Time, reason string) {
	verdict := cleanStreamControlText(stringFromAny(status["streamVerdict"]), "unknown")
	dropReasons, _ := status["dropReasons"].(map[string]uint64)
	var dropTotal uint64
	for _, value := range dropReasons {
		dropTotal += value
	}
	s.relayProductMu.Lock()
	lastVerdict := s.lastRelayStreamVerdict
	lastDropTotal := s.lastRelayDropTotal
	if verdict != "" {
		s.lastRelayStreamVerdict = verdict
	}
	if dropTotal > s.lastRelayDropTotal {
		s.lastRelayDropTotal = dropTotal
	}
	s.relayProductMu.Unlock()
	backend := s.activePhoneBackend()
	if lastVerdict != "" && verdict != lastVerdict {
		go s.recordProductEvent(productEventInput{
			Source:    "ticket_remote_relay",
			Category:  "stream",
			Action:    "verdict_changed",
			Status:    verdict,
			Reason:    reason,
			BackendID: backend.ID,
			SafeState: map[string]any{
				"previousVerdict":    lastVerdict,
				"videoClients":       uint32FromAny(status["activeVideoClients"]),
				"lastFrameAgoMillis": uint32FromAny(status["lastFrameAgoMillis"]),
				"framesForwarded":    stringFromAny(status["framesForwarded"]),
				"reportedAt":         now.UTC().Format(time.RFC3339),
			},
		})
	}
	if dropTotal > lastDropTotal && (lastDropTotal == 0 || dropTotal-lastDropTotal >= 20) {
		go s.recordProductEvent(productEventInput{
			Source:    "ticket_remote_relay",
			Category:  "stream",
			Action:    "frame_drop_threshold",
			Status:    "warn",
			Reason:    reason,
			BackendID: backend.ID,
			Count:     int64(dropTotal - lastDropTotal),
			SafeState: map[string]any{
				"dropTotal":          dropTotal,
				"dropReasons":        fmt.Sprint(dropReasons),
				"lastFrameAgoMillis": uint32FromAny(status["lastFrameAgoMillis"]),
				"framesForwarded":    stringFromAny(status["framesForwarded"]),
			},
		})
	}
}

func (s *Server) publishPhoneCurrentReport(ctx context.Context, now time.Time, reason string) error {
	if s.store == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	backend := s.activePhoneBackend()
	health := s.relay.Snapshot()
	status := map[string]any{
		"source":           "ticket_remote_relay",
		"reason":           cleanStreamControlText(reason, "relay_report"),
		"relayConnected":   health.Connected,
		"relayDesired":     health.Desired,
		"relayViewers":     health.Viewers,
		"relayStreamState": health.StreamState,
	}
	for key, value := range s.streamAutoRecoveryStatus(now) {
		status[key] = value
	}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return s.store.UpdatePhoneCurrentReport(ctx, state.PhoneCurrentReportInput{
		TicketID:      s.cfg.TicketID,
		BackendID:     backend.ID,
		StreamState:   cleanStreamControlText(health.StreamState, "unknown"),
		DesiredActive: health.Desired,
		StatusJSON:    string(statusJSON),
		Now:           now,
	})
}

func (s *Server) cancelIdleStreamDesiredRelease() {
	s.streamDesiredReleaseMu.Lock()
	defer s.streamDesiredReleaseMu.Unlock()
	s.streamDesiredReleaseSeq++
	if s.streamDesiredReleaseTimer != nil {
		s.streamDesiredReleaseTimer.Stop()
		s.streamDesiredReleaseTimer = nil
	}
}

func (s *Server) scheduleIdleStreamDesiredRelease(reason string) {
	if s.store == nil {
		return
	}
	if s.streamDemandStillPresent() {
		s.cancelIdleStreamDesiredRelease()
		return
	}
	reason = cleanStreamControlText(reason, "relay_no_video_clients")
	s.streamDesiredReleaseMu.Lock()
	s.streamDesiredReleaseSeq++
	seq := s.streamDesiredReleaseSeq
	if s.streamDesiredReleaseTimer != nil {
		s.streamDesiredReleaseTimer.Stop()
	}
	s.streamDesiredReleaseTimer = time.AfterFunc(streamDesiredIdleReleaseGrace, func() {
		s.streamDesiredReleaseMu.Lock()
		if seq != s.streamDesiredReleaseSeq {
			s.streamDesiredReleaseMu.Unlock()
			return
		}
		s.streamDesiredReleaseTimer = nil
		s.streamDesiredReleaseMu.Unlock()
		s.releaseStreamDesiredIfNoVideoClients(reason)
	})
	s.streamDesiredReleaseMu.Unlock()
}

func (s *Server) releaseStreamDesiredIfNoVideoClients(reason string) bool {
	s.streamLifecycleMu.Lock()
	defer s.streamLifecycleMu.Unlock()
	if s.store == nil {
		return false
	}
	if s.streamDemandStillPresent() {
		return false
	}
	reason = cleanStreamControlText(reason, "relay_no_video_clients")
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	if err := s.publishStreamDesiredState(ctx, false, 0, reason, "ticket_remote_relay"); err != nil {
		s.recordRuntimeErrorAsync("stream_desired_state_idle_release_failed", reason, err, map[string]any{"reason": reason})
		return false
	}
	s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "stream_desired_state_idle_released", reason, map[string]any{
		"reason": reason,
	})
	if err := s.publishRelayCurrentReport(ctx, time.Now(), reason); err != nil {
		s.recordRuntimeErrorAsync("relay_report_idle_release_failed", reason, err, map[string]any{"reason": reason})
	}
	if err := s.publishPhoneCurrentReport(ctx, time.Now(), reason); err != nil {
		s.recordRuntimeErrorAsync("phone_current_report_idle_release_failed", reason, err, map[string]any{"reason": reason})
	}
	return true
}

func (s *Server) streamDemandStillPresent() bool {
	if s.direct.activeVideoClientCount() > 0 {
		return true
	}
	if s.relay != nil && s.relay.Snapshot().Viewers > 0 {
		return true
	}
	return false
}

func (s *Server) appendStreamCommandAsync(commandType string, reason string, payload map[string]any, ttl time.Duration) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		if _, err := s.appendStreamCommand(ctx, commandType, reason, payload, ttl); err != nil {
			s.recordRuntimeErrorAsync("stream_command_publish_failed", commandType, err, map[string]any{
				"commandType": cleanStreamControlText(commandType, "command"),
				"reason":      cleanStreamControlText(reason, "stream_command"),
			})
		}
	}()
}

func (s *Server) appendStreamRecoveryCommandAsync(reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		commandID, err := s.appendStreamCommand(ctx, "recover_stream", reason, map[string]any{
			"source": "ticket_remote",
		}, streamCommandTTL)
		if err != nil {
			s.noteStreamAutoRecoveryResult("failed", reason, commandID, err)
			return
		}
		if commandID == "" {
			s.noteStreamAutoRecoveryResult("suppressed_no_demand", reason, "", nil)
		}
	}()
}

func (s *Server) appendStreamCommand(ctx context.Context, commandType string, reason string, payload map[string]any, ttl time.Duration) (string, error) {
	if s.store == nil {
		return "", nil
	}
	guardDemand := backgroundStreamCommandRequiresDemand(commandType, reason)
	if guardDemand {
		s.streamLifecycleMu.RLock()
		defer s.streamLifecycleMu.RUnlock()
		if !s.streamDemandStillPresent() {
			s.direct.recordClientTelemetry("stream_command_suppressed_no_demand", cleanStreamControlText(commandType, "command"))
			return "", nil
		}
	}
	now := time.Now()
	backend := s.activePhoneBackend()
	revision := streamControlRevision(now)
	commandID := fmt.Sprintf("%s:%s:%s:%s", cleanStreamControlText(s.cfg.TicketID, "ticket"), cleanStreamControlText(backend.ID, "pixel"), revision, cleanStreamControlText(commandType, "command"))
	payloadJSON := "{}"
	if len(payload) > 0 {
		body, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		payloadJSON = string(body)
	}
	if ttl <= 0 {
		ttl = streamCommandTTL
	}
	err := s.store.AppendStreamCommand(ctx, state.StreamCommandInput{
		TicketID:    s.cfg.TicketID,
		BackendID:   backend.ID,
		CommandID:   commandID,
		CommandType: cleanStreamControlText(commandType, "command"),
		Revision:    revision,
		Reason:      cleanStreamControlText(reason, "stream_command"),
		PayloadJSON: payloadJSON,
		TTL:         ttl,
		Now:         now,
	})
	event := productEventInput{
		Source:        "ticket_remote_service",
		Category:      "command",
		Action:        "queued",
		Status:        "ok",
		Reason:        cleanStreamControlText(reason, "stream_command"),
		CommandID:     commandID,
		BackendID:     backend.ID,
		CorrelationID: commandID,
		SafeState:     map[string]any{"commandType": cleanStreamControlText(commandType, "command")},
	}
	if err == nil {
		event.SafeState["ttlMillis"] = ttl.Milliseconds()
		event.SafeState["revision"] = revision
	} else {
		event.Action, event.Status, event.Reason = "queue_failed", "failed", safeRuntimeLogError(err)
	}
	go s.recordProductEvent(event)
	if err == nil && (commandType == "start" || commandType == "keyframe" || commandType == "recover_stream") {
		s.direct.recordStartupPhase("spacetime_command_written", fmt.Sprintf("type=%s reason=%s id=%s", commandType, reason, commandID))
	}
	return commandID, err
}

func backgroundStreamCommandRequiresDemand(commandType string, reason string) bool {
	commandType = strings.ToLower(strings.TrimSpace(commandType))
	reason = strings.ToLower(strings.TrimSpace(reason))
	if strings.Contains(reason, "control_code") {
		return false
	}
	switch commandType {
	case "start", "keyframe", "recover_stream":
		return true
	default:
		return false
	}
}

func (s *Server) beginStreamAutoRecovery(reason string, now time.Time) bool {
	if now.IsZero() {
		now = time.Now()
	}
	reason = cleanStreamControlText(reason, "browser_stream_recovery")
	s.streamRecoveryMu.Lock()
	defer s.streamRecoveryMu.Unlock()
	if !s.lastStreamRecoveryAt.IsZero() && now.Sub(s.lastStreamRecoveryAt) < streamRecoveryCommandCooldown {
		return false
	}
	s.lastStreamRecoveryAt = now
	s.lastStreamRecoveryStage = "queued"
	s.lastStreamRecoveryAction = "stream_recovery"
	s.lastStreamRecoveryResult = "started"
	s.lastStreamRecoveryReason = reason
	s.lastStreamRecoveryFailure = ""
	s.lastStreamRecoveryCommandID = ""
	return true
}

func (s *Server) noteStreamAutoRecoveryResult(result string, reason string, commandID string, err error) {
	now := time.Now()
	reason = cleanStreamControlText(reason, "browser_stream_recovery")
	result = cleanStreamControlText(result, "unknown")
	failure := ""
	if err != nil {
		failure = safeRuntimeLogError(err)
	}
	s.streamRecoveryMu.Lock()
	s.lastStreamRecoveryAt = now
	s.lastStreamRecoveryStage = result
	s.lastStreamRecoveryResult = result
	if reason != "" {
		s.lastStreamRecoveryReason = reason
	}
	s.lastStreamRecoveryFailure = failure
	if commandID != "" {
		s.lastStreamRecoveryCommandID = cleanStreamControlText(commandID, "")
	}
	s.streamRecoveryMu.Unlock()
	event := "stream_failed"
	if result == "succeeded" {
		event = "stream_recovered"
	}
	if result == "succeeded" {
		s.recordRuntimeEventForSourceAsync("ticket_remote", "info", event, commandID, map[string]any{"reason": reason})
	} else if err != nil {
		s.recordRuntimeErrorAsync(event, commandID, err, map[string]any{"reason": reason})
	}
}

func (s *Server) streamAutoRecoveryStatus(now time.Time) map[string]any {
	if now.IsZero() {
		now = time.Now()
	}
	s.streamRecoveryMu.Lock()
	defer s.streamRecoveryMu.Unlock()
	stage := s.lastStreamRecoveryStage
	if stage == "" {
		stage = "idle"
	}
	result := s.lastStreamRecoveryResult
	if result == "" {
		result = "none"
	}
	action := s.lastStreamRecoveryAction
	if action == "" {
		action = "none"
	}
	var ageMillis any
	if !s.lastStreamRecoveryAt.IsZero() {
		ageMillis = uint32(now.Sub(s.lastStreamRecoveryAt) / time.Millisecond)
	}
	return map[string]any{
		"currentRecoveryStage":       stage,
		"lastWatchdogAction":         action,
		"lastRecoveryResult":         result,
		"lastRecoveryReason":         s.lastStreamRecoveryReason,
		"lastRecoveryAgeMillis":      ageMillis,
		"lastRecoveryFailureReason":  s.lastStreamRecoveryFailure,
		"lastRecoveryCommandId":      s.lastStreamRecoveryCommandID,
		"recoveryCommandCooldownSec": uint32(streamRecoveryCommandCooldown / time.Second),
	}
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return fmt.Sprint(value)
	}
}

func uint32FromAny(value any) uint32 {
	switch typed := value.(type) {
	case int:
		if typed <= 0 {
			return 0
		}
		if typed > int(^uint32(0)) {
			return ^uint32(0)
		}
		return uint32(typed)
	case int64:
		if typed <= 0 {
			return 0
		}
		if typed > int64(^uint32(0)) {
			return ^uint32(0)
		}
		return uint32(typed)
	case uint64:
		if typed > uint64(^uint32(0)) {
			return ^uint32(0)
		}
		return uint32(typed)
	case float64:
		if typed <= 0 {
			return 0
		}
		if typed > float64(^uint32(0)) {
			return ^uint32(0)
		}
		return uint32(typed)
	case uint32:
		return typed
	default:
		return 0
	}
}

func streamControlRevision(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return fmt.Sprintf("%d-%s", now.UTC().UnixNano(), randomID())
}

func cleanStreamControlText(value string, fallback string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		clean = strings.TrimSpace(fallback)
	}
	if clean == "" {
		return "unknown"
	}
	clean = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-' || r == ':' || r == '.':
			return r
		default:
			return '_'
		}
	}, clean)
	if len(clean) > 180 {
		return clean[:180]
	}
	return clean
}
