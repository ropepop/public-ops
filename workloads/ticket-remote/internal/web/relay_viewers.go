package web

import (
	"fmt"
	"strings"
	"time"
)

func (s *Server) tryAddClient(c *client) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.clients) >= maxBrowserSocketConnections {
		return false
	}
	identityConnections := 0
	sessionConnections := 0
	for existing := range s.clients {
		if strings.EqualFold(strings.TrimSpace(existing.email), strings.TrimSpace(c.email)) {
			identityConnections++
		}
		if strings.TrimSpace(existing.sessionID) == strings.TrimSpace(c.sessionID) {
			sessionConnections++
		}
	}
	if identityConnections >= maxBrowserSocketsPerIdentity || sessionConnections >= maxBrowserSocketsPerSession {
		return false
	}
	s.clients[c] = struct{}{}
	return true
}

func (s *Server) removeClient(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, c)
}

func (s *Server) addRelayViewer(sessionID string) {
	s.streamLifecycleMu.Lock()
	defer s.streamLifecycleMu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		s.relay.AddViewer()
		viewers := s.relay.Snapshot().Viewers
		s.direct.recordStartupPhase("relay_viewer_added", fmt.Sprintf("viewers=%d", viewers))
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "relay_viewer_added", "", map[string]any{
			"viewerCount": viewers,
			"session":     false,
		})
		if viewers > 0 {
			s.cancelIdleStreamDesiredRelease()
			s.publishStreamDesiredStateAsync(true, viewers, "relay_viewer_added", "ticket_remote_relay")
		}
		s.publishRelayCurrentReportAsync("relay_viewer_added")
		return
	}
	viewerCount := 0
	s.mu.Lock()
	previous := s.relayViewerRefs[sessionID]
	s.relayViewerRefs[sessionID] = previous + 1
	viewerCount = len(s.relayViewerRefs)
	s.mu.Unlock()
	if previous == 0 {
		s.relay.AddViewer()
		s.direct.recordStartupPhase("relay_viewer_added", fmt.Sprintf("viewers=%d", viewerCount))
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "relay_viewer_added", safeRuntimeTraceID("browser", sessionID), map[string]any{
			"viewerCount": viewerCount,
			"session":     true,
		})
		if viewerCount > 0 {
			s.cancelIdleStreamDesiredRelease()
			s.publishStreamDesiredStateAsync(true, viewerCount, "relay_viewer_added", "ticket_remote_relay")
		}
		s.publishRelayCurrentReportAsync("relay_viewer_added")
	}
}

func (s *Server) retainRelayViewerForPrewarm(sessionID string, hold time.Duration) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if s.retainRelaysForDuration(sessionID, hold, true, "prewarm") {
		s.direct.recordStartupPhase("prewarm_lease_retained", fmt.Sprintf("hold_ms=%d", durationMillis(hold)))
		s.addRelayViewer(sessionID)
	}
}

func (s *Server) releaseRetainedRelayViewer(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if s.retainRelaysForDuration(sessionID, 0, false, "release") {
		s.removeRelayViewer(sessionID)
		return true
	}
	return false
}

func (s *Server) retainRelayViewerForPublicOpenGrace(sessionID string, hold time.Duration, reason string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if hold <= 0 {
		hold = publicOpenGraceHold
	}
	reason = cleanStreamControlText(reason, "public_open_grace")
	added := s.retainRelaysForDuration(sessionID, hold, true, "public_open_grace")
	s.direct.recordStartupPhase("public_open_grace_retained", fmt.Sprintf("reason=%s hold_ms=%d added=%t", reason, durationMillis(hold), added))
	s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "public_open_grace_retained", safeRuntimeTraceID("browser", sessionID), map[string]any{
		"reason":     reason,
		"holdMillis": durationMillis(hold),
		"added":      added,
	})
	if added {
		s.addRelayViewer(sessionID)
	}
}

func (s *Server) releaseRelayViewerPublicOpenGrace(sessionID string, reason string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	reason = cleanStreamControlText(reason, "public_open_grace_released")
	if s.releaseRetainedRelayViewer(sessionID) {
		s.direct.recordStartupPhase("public_open_grace_released", fmt.Sprintf("reason=%s", reason))
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "public_open_grace_released", safeRuntimeTraceID("browser", sessionID), map[string]any{
			"reason": reason,
		})
	}
}

func (s *Server) retainRelaysForDuration(sessionID string, hold time.Duration, retain bool, reason string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if retain {
		if hold <= 0 {
			hold = streamPrewarmHold
		}
	}
	shouldChangeViewer := false
	var timer *time.Timer
	s.mu.Lock()
	if s.streamPrewarmTimers == nil {
		s.streamPrewarmTimers = map[string]*time.Timer{}
	}
	if retain {
		reason = cleanStreamControlText(reason, "prewarm")
		if existing := s.streamPrewarmTimers[sessionID]; existing != nil {
			existing.Stop()
		} else {
			shouldChangeViewer = true
		}
		timer = time.AfterFunc(hold, func() {
			shouldRemoveViewer := false
			s.mu.Lock()
			if s.streamPrewarmTimers[sessionID] == timer {
				delete(s.streamPrewarmTimers, sessionID)
				shouldRemoveViewer = true
			}
			s.mu.Unlock()
			if shouldRemoveViewer {
				if reason == "public_open_grace" {
					s.direct.recordStartupPhase("public_open_grace_expired", fmt.Sprintf("hold_ms=%d", durationMillis(hold)))
					s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "public_open_grace_expired", safeRuntimeTraceID("browser", sessionID), map[string]any{
						"holdMillis": durationMillis(hold),
					})
				}
				s.removeRelayViewer(sessionID)
			}
		})
		s.streamPrewarmTimers[sessionID] = timer
	} else {
		if existing := s.streamPrewarmTimers[sessionID]; existing != nil {
			existing.Stop()
			delete(s.streamPrewarmTimers, sessionID)
			shouldChangeViewer = true
		}
	}
	s.mu.Unlock()
	return shouldChangeViewer
}

func (s *Server) removeRelayViewer(sessionID string) {
	s.streamLifecycleMu.Lock()
	defer s.streamLifecycleMu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		s.relay.RemoveViewer()
		viewers := s.relay.Snapshot().Viewers
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "relay_viewer_removed", "", map[string]any{
			"viewerCount": viewers,
			"session":     false,
		})
		if viewers > 0 {
			s.publishStreamDesiredStateAsync(true, viewers, "relay_viewer_removed", "ticket_remote_relay")
		} else {
			s.scheduleIdleStreamDesiredRelease("relay_viewer_removed")
		}
		s.publishRelayCurrentReportAsync("relay_viewer_removed")
		return
	}
	removeFromRelay := false
	viewerCount := 0
	s.mu.Lock()
	if count, ok := s.relayViewerRefs[sessionID]; !ok {
		removeFromRelay = false
	} else if count <= 1 {
		delete(s.relayViewerRefs, sessionID)
		removeFromRelay = true
	} else {
		s.relayViewerRefs[sessionID] = count - 1
	}
	viewerCount = len(s.relayViewerRefs)
	s.mu.Unlock()
	if removeFromRelay {
		s.relay.RemoveViewer()
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "relay_viewer_removed", safeRuntimeTraceID("browser", sessionID), map[string]any{
			"viewerCount": viewerCount,
			"session":     true,
		})
		if viewerCount > 0 {
			s.publishStreamDesiredStateAsync(true, viewerCount, "relay_viewer_removed", "ticket_remote_relay")
		} else {
			s.scheduleIdleStreamDesiredRelease("relay_viewer_removed")
		}
		s.publishRelayCurrentReportAsync("relay_viewer_removed")
	}
}
