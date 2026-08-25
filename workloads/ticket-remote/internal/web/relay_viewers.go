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
	s.nextVideoKeyframeOwnerID++
	if s.nextVideoKeyframeOwnerID == 0 {
		s.nextVideoKeyframeOwnerID++
	}
	c.videoKeyframeRequirementID = s.nextVideoKeyframeOwnerID
	s.clients[c] = struct{}{}
	return true
}

func (s *Server) removeClient(c *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, c)
}

func (s *Server) addRelayViewer(sessionID string, startupTraceCorrelationID ...string) {
	s.streamLifecycleMu.Lock()
	defer s.streamLifecycleMu.Unlock()
	s.addRelayViewerLocked(sessionID, startupTraceCorrelationID...)
}

func (s *Server) addRelayViewerLocked(sessionID string, startupTraceCorrelationID ...string) {
	traceContextProvided := len(startupTraceCorrelationID) > 1
	originatingTraceID := ""
	if len(startupTraceCorrelationID) > 0 && strings.TrimSpace(startupTraceCorrelationID[0]) != "" {
		s.relay.SetStartupTraceCorrelationID(startupTraceCorrelationID[0])
	}
	if len(startupTraceCorrelationID) > 1 {
		originatingTraceID = strings.TrimSpace(startupTraceCorrelationID[1])
	}
	recordPhase := func(name, detail string) {
		if originatingTraceID != "" {
			s.direct.recordStartupPhaseForTrace(originatingTraceID, name, detail)
			return
		}
		if traceContextProvided {
			return
		}
		s.direct.recordStartupPhase(name, detail)
	}
	recordPhaseOnce := func(name, detail string) {
		if originatingTraceID != "" {
			s.direct.recordStartupPhaseOnceForTrace(originatingTraceID, name, detail)
			return
		}
		if traceContextProvided {
			return
		}
		s.direct.recordStartupPhaseOnce(name, detail)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		relayHealth := s.relay.Snapshot()
		if !relayHealth.Connected {
			recordPhaseOnce("private_relay_connect_started", fmt.Sprintf("viewers=%d desired=%t", relayHealth.Viewers+1, relayHealth.Desired))
		}
		if len(startupTraceCorrelationID) > 0 && strings.TrimSpace(startupTraceCorrelationID[0]) != "" {
			s.relay.AddViewer(startupTraceCorrelationID[0])
		} else {
			s.relay.AddViewer()
		}
		viewers := s.relay.Snapshot().Viewers
		recordPhase("relay_viewer_added", fmt.Sprintf("viewers=%d", viewers))
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
		relayHealth := s.relay.Snapshot()
		if !relayHealth.Connected {
			recordPhaseOnce("private_relay_connect_started", fmt.Sprintf("viewers=%d desired=%t", viewerCount, relayHealth.Desired))
		}
		if len(startupTraceCorrelationID) > 0 && strings.TrimSpace(startupTraceCorrelationID[0]) != "" {
			s.relay.AddViewer(startupTraceCorrelationID[0])
		} else {
			s.relay.AddViewer()
		}
		recordPhase("relay_viewer_added", fmt.Sprintf("viewers=%d", viewerCount))
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

func (s *Server) retainRelayViewerForPrewarm(sessionID string, hold time.Duration, startupTraceCorrelationID ...string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.startupLeaseMu.Lock()
	defer s.startupLeaseMu.Unlock()
	s.streamLifecycleMu.Lock()
	defer s.streamLifecycleMu.Unlock()
	correlationID := ""
	if len(startupTraceCorrelationID) > 0 {
		correlationID = strings.TrimSpace(startupTraceCorrelationID[0])
	}
	originatingTraceID := ""
	traceContextProvided := len(startupTraceCorrelationID) > 1
	if traceContextProvided {
		originatingTraceID = strings.TrimSpace(startupTraceCorrelationID[1])
	}
	retained := false
	accepted := true
	install := func(allowOwnerReplacement bool) {
		if correlationID != "" {
			s.relay.SetStartupTraceCorrelationID(correlationID)
		}
		retained = s.retainRelaysForDurationInternal(sessionID, hold, true, "prewarm", allowOwnerReplacement, originatingTraceID)
	}
	if originatingTraceID != "" {
		accepted = s.direct.withActiveStartupTrace(originatingTraceID, func() { install(true) })
	} else if traceContextProvided {
		accepted = s.direct.withoutActiveStartupTraceForSession(sessionID, func() { install(false) })
	} else {
		install(false)
	}
	if !accepted {
		return
	}
	if retained {
		detail := fmt.Sprintf("hold_ms=%d", durationMillis(hold))
		if originatingTraceID != "" {
			s.direct.recordStartupPhaseForTrace(originatingTraceID, "prewarm_lease_retained", detail)
		} else {
			s.direct.recordStartupPhase("prewarm_lease_retained", detail)
		}
		s.addRelayViewerLocked(sessionID, startupTraceCorrelationID...)
	}
}

func (s *Server) releaseRetainedRelayViewer(sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	s.startupLeaseMu.Lock()
	defer s.startupLeaseMu.Unlock()
	if s.retainRelaysForDuration(sessionID, 0, false, "release") {
		s.removeRelayViewer(sessionID)
		return true
	}
	return false
}

func (s *Server) retainRelayViewerForPublicOpenGrace(sessionID string, hold time.Duration, reason string, startupTraceID ...string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.startupLeaseMu.Lock()
	defer s.startupLeaseMu.Unlock()
	originatingTraceID := ""
	traceContextProvided := len(startupTraceID) > 0
	if len(startupTraceID) > 0 {
		originatingTraceID = strings.TrimSpace(startupTraceID[0])
	}
	traceBound := originatingTraceID != ""
	if hold <= 0 {
		hold = publicOpenGraceHold
	}
	reason = cleanStreamControlText(reason, "public_open_grace")
	var added bool
	accepted := true
	if traceBound {
		accepted = s.direct.withActiveStartupTrace(originatingTraceID, func() {
			added = s.retainRelaysForDurationInternal(sessionID, hold, true, "public_open_grace", true, originatingTraceID)
		})
	} else if traceContextProvided {
		accepted = s.direct.withoutActiveStartupTraceForSession(sessionID, func() {
			added = s.retainRelaysForDurationInternal(sessionID, hold, true, "public_open_grace", false, "")
		})
	} else {
		accepted = s.direct.withoutActiveStartupTraceForSession(sessionID, func() {
			added = s.retainRelaysForDurationInternal(sessionID, hold, true, "public_open_grace", false)
		})
	}
	if !accepted {
		return
	}
	detail := fmt.Sprintf("reason=%s hold_ms=%d added=%t", reason, durationMillis(hold), added)
	if traceBound {
		s.direct.recordStartupPhaseForTrace(originatingTraceID, "public_open_grace_retained", detail)
	} else if !traceContextProvided {
		s.direct.recordStartupPhase("public_open_grace_retained", detail)
	}
	s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "public_open_grace_retained", safeRuntimeTraceID("browser", sessionID), map[string]any{
		"reason":     reason,
		"holdMillis": durationMillis(hold),
		"added":      added,
	})
	if added {
		if traceBound {
			s.addRelayViewer(sessionID, "", originatingTraceID)
		} else if traceContextProvided {
			s.addRelayViewer(sessionID, "", "")
		} else {
			s.addRelayViewer(sessionID)
		}
	}
}

func (s *Server) releaseRelayViewerPublicOpenGrace(sessionID string, reason string, startupTraceID ...string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	s.startupLeaseMu.Lock()
	defer s.startupLeaseMu.Unlock()
	originatingTraceID := ""
	traceContextProvided := len(startupTraceID) > 0
	if len(startupTraceID) > 0 {
		originatingTraceID = strings.TrimSpace(startupTraceID[0])
	}
	traceBound := originatingTraceID != ""
	reason = cleanStreamControlText(reason, "public_open_grace_released")
	var released bool
	accepted := true
	if traceBound {
		released = s.retainRelaysForDuration(sessionID, 0, false, "release", originatingTraceID)
	} else if traceContextProvided {
		accepted = s.direct.withoutActiveStartupTraceForSession(sessionID, func() {
			released = s.retainRelaysForDuration(sessionID, 0, false, "release", "")
		})
	} else {
		accepted = s.direct.withoutActiveStartupTraceForSession(sessionID, func() {
			released = s.retainRelaysForDuration(sessionID, 0, false, "release")
		})
	}
	if !accepted {
		return
	}
	if released {
		s.removeRelayViewer(sessionID)
		detail := fmt.Sprintf("reason=%s", reason)
		if traceBound {
			s.direct.recordStartupPhaseForTrace(originatingTraceID, "public_open_grace_released", detail)
		} else if !traceContextProvided {
			s.direct.recordStartupPhase("public_open_grace_released", detail)
		}
		s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "public_open_grace_released", safeRuntimeTraceID("browser", sessionID), map[string]any{
			"reason": reason,
		})
	}
}

func (s *Server) retainRelaysForDuration(sessionID string, hold time.Duration, retain bool, reason string, startupTraceID ...string) bool {
	return s.retainRelaysForDurationInternal(sessionID, hold, retain, reason, false, startupTraceID...)
}

func (s *Server) retainRelaysForDurationInternal(sessionID string, hold time.Duration, retain bool, reason string, allowOwnerReplacement bool, startupTraceID ...string) bool {
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
	traceContextProvided := len(startupTraceID) > 0
	requestedTraceID := ""
	if traceContextProvided {
		requestedTraceID = strings.TrimSpace(startupTraceID[0])
	}
	s.mu.Lock()
	if s.streamPrewarmTimers == nil {
		s.streamPrewarmTimers = map[string]*time.Timer{}
	}
	if s.streamPrewarmOwners == nil {
		s.streamPrewarmOwners = map[string]string{}
	}
	if retain {
		reason = cleanStreamControlText(reason, "prewarm")
		existing := s.streamPrewarmTimers[sessionID]
		existingOwner := s.streamPrewarmOwners[sessionID]
		if existing != nil && existingOwner != requestedTraceID && !allowOwnerReplacement {
			s.mu.Unlock()
			return false
		}
		if existing != nil {
			existing.Stop()
		} else {
			shouldChangeViewer = true
		}
		timer = time.AfterFunc(hold, func() {
			shouldRemoveViewer := false
			s.mu.Lock()
			if s.streamPrewarmTimers[sessionID] == timer {
				delete(s.streamPrewarmTimers, sessionID)
				delete(s.streamPrewarmOwners, sessionID)
				shouldRemoveViewer = true
			}
			s.mu.Unlock()
			if shouldRemoveViewer {
				if reason == "public_open_grace" {
					detail := fmt.Sprintf("hold_ms=%d", durationMillis(hold))
					if len(startupTraceID) > 0 {
						if strings.TrimSpace(startupTraceID[0]) != "" {
							s.direct.recordStartupPhaseForTrace(startupTraceID[0], "public_open_grace_expired", detail)
						}
					} else {
						s.direct.recordStartupPhase("public_open_grace_expired", detail)
					}
					s.recordRuntimeEventForSourceAsync("ticket_remote_relay", "info", "public_open_grace_expired", safeRuntimeTraceID("browser", sessionID), map[string]any{
						"holdMillis": durationMillis(hold),
					})
				}
				s.removeRelayViewer(sessionID)
			}
		})
		s.streamPrewarmTimers[sessionID] = timer
		if traceContextProvided {
			s.streamPrewarmOwners[sessionID] = requestedTraceID
		}
	} else {
		if existing := s.streamPrewarmTimers[sessionID]; existing != nil {
			existingOwner := s.streamPrewarmOwners[sessionID]
			if (traceContextProvided && existingOwner != requestedTraceID) ||
				(!traceContextProvided && existingOwner != "") {
				s.mu.Unlock()
				return false
			}
			existing.Stop()
			delete(s.streamPrewarmTimers, sessionID)
			delete(s.streamPrewarmOwners, sessionID)
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
