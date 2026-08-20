package web

// publishAdaptiveStreamCadence gives a compatible Pixel implementation an
// aggregate temporal demand hint. It is deliberately best-effort: the relay
// still forwards the one existing H.264/TSF2 stream, and older Pixel builds
// simply ignore this private command while retaining their safe cadence.
func (s *Server) publishAdaptiveStreamCadence(reason string) {
	if s == nil {
		return
	}
	clients := s.clientSnapshot()
	if len(clients) == 0 {
		s.streamCadenceMu.Lock()
		s.lastStreamCadenceDemand = ""
		s.lastStreamCadenceMaxFPS = 0
		s.streamCadenceMu.Unlock()
		return
	}

	demand, maxFPS := adaptiveStreamCadenceDemand(clients)
	s.streamCadenceMu.Lock()
	if demand == s.lastStreamCadenceDemand && maxFPS == s.lastStreamCadenceMaxFPS {
		s.streamCadenceMu.Unlock()
		return
	}
	s.lastStreamCadenceDemand = demand
	s.lastStreamCadenceMaxFPS = maxFPS
	s.streamCadenceMu.Unlock()

	s.appendStreamCommandAsync("stream_cadence", reason, map[string]any{
		"type":    "stream_cadence",
		"version": 1,
		"demand":  demand,
		"maxFps":  maxFPS,
	}, streamCommandTTL)
}

// adaptiveStreamCadenceDemand is intentionally independent of command
// persistence. It makes the source-demand contract testable without needing a
// live SpacetimeDB client and keeps a constrained viewer from affecting a
// healthy visible viewer.
func adaptiveStreamCadenceDemand(clients []*client) (string, int) {
	if len(clients) == 0 {
		return "", 0
	}
	visibleFull := false
	for _, c := range clients {
		c.feedbackMu.Lock()
		visibility := c.feedbackVisibility
		c.feedbackMu.Unlock()
		c.videoMu.Lock()
		mode := c.videoDeliveryMode
		c.videoMu.Unlock()
		if visibility == "hidden" {
			continue
		}
		if mode == videoDeliveryFull || mode == videoDeliveryProbe || mode == videoDeliveryAwaitingKeyframe || mode == "" {
			visibleFull = true
			break
		}
	}

	if visibleFull {
		return "full", 10
	}
	return "keyframe_only", 1
}
