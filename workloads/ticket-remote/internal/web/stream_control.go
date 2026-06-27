package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"ticketremote/internal/state"
)

const (
	streamControlWriteTimeout = 2 * time.Second
	streamCommandTTL          = 2 * time.Minute
	streamKeyframeCommandTTL  = 30 * time.Second
)

func (s *Server) publishStreamDesiredStateAsync(active bool, viewerCount int, reason string, source string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		if err := s.publishStreamDesiredState(ctx, active, viewerCount, reason, source); err != nil {
			log.Printf("ticket stream desired-state publish failed: %v", err)
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
	return s.store.SetStreamDesiredState(ctx, state.StreamDesiredStateInput{
		TicketID:      s.cfg.TicketID,
		BackendID:     backend.ID,
		DesiredActive: active,
		ViewerCount:   uint32(viewerCount),
		Reason:        cleanStreamControlText(reason, "stream_state"),
		Revision:      streamControlRevision(now),
		UpdatedBy:     cleanStreamControlText(source, "ticket_remote"),
		Now:           now,
	})
}

func (s *Server) publishRelayCurrentReportAsync(reason string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		if err := s.publishRelayCurrentReport(ctx, time.Now(), reason); err != nil {
			log.Printf("ticket relay report publish failed: %v", err)
		}
	}()
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
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return s.store.UpdateRelayCurrentReport(ctx, state.RelayCurrentReportInput{
		TicketID:           s.cfg.TicketID,
		BackendID:          backend.ID,
		VideoClients:       uint32FromAny(status["activeVideoClients"]),
		StreamVerdict:      cleanStreamControlText(stringFromAny(status["streamVerdict"]), "unknown"),
		LastFrameAgoMillis: uint32FromAny(status["lastFrameAgoMillis"]),
		FramesForwarded:    stringFromAny(status["framesForwarded"]),
		StatusJSON:         string(statusJSON),
		Now:                now,
	})
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
	if s.direct.activeVideoClientCount() > 0 {
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
	if s.store == nil {
		return false
	}
	if s.direct.activeVideoClientCount() > 0 {
		return false
	}
	reason = cleanStreamControlText(reason, "relay_no_video_clients")
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	if err := s.publishStreamDesiredState(ctx, false, 0, reason, "ticket_remote_relay"); err != nil {
		log.Printf("ticket stream desired-state idle release failed: %v", err)
		return false
	}
	if err := s.publishRelayCurrentReport(ctx, time.Now(), reason); err != nil {
		log.Printf("ticket relay report idle release publish failed: %v", err)
	}
	if err := s.publishPhoneCurrentReport(ctx, time.Now(), reason); err != nil {
		log.Printf("ticket phone current report idle release publish failed: %v", err)
	}
	return true
}

func (s *Server) appendStreamCommandAsync(commandType string, reason string, payload map[string]any, ttl time.Duration) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
		defer cancel()
		if _, err := s.appendStreamCommand(ctx, commandType, reason, payload, ttl); err != nil {
			log.Printf("ticket stream command publish failed type=%s: %v", commandType, err)
		}
	}()
}

func (s *Server) appendStreamCommand(ctx context.Context, commandType string, reason string, payload map[string]any, ttl time.Duration) (string, error) {
	if s.store == nil {
		return "", nil
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
	return commandID, err
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
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
