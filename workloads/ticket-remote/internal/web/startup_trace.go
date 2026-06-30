package web

import (
	"strings"
	"time"
)

const (
	streamStartupTraceTarget    = 5 * time.Second
	streamStartupTraceMaxAge    = 2 * time.Minute
	streamStartupTraceMaxPhases = 32
)

type streamStartupTracePhase struct {
	Name                string `json:"name"`
	Detail              string `json:"detail,omitempty"`
	At                  string `json:"at"`
	SinceStartMillis    int64  `json:"sinceStartMillis"`
	SincePreviousMillis int64  `json:"sincePreviousMillis"`
}

type streamStartupTrace struct {
	ID        string
	SessionID string
	Reason    string
	StartedAt time.Time
	LastAt    time.Time
	Complete  bool
	Phases    []streamStartupTracePhase
}

func (h *directStreamHub) beginStartupTrace(sessionID string, reason string) string {
	now := time.Now()
	sessionID = trimLogField(strings.TrimSpace(sessionID), 96)
	reason = trimLogField(strings.TrimSpace(reason), 160)
	if reason == "" {
		reason = "stream_startup"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.startupTrace.ID != "" &&
		!h.startupTrace.Complete &&
		now.Sub(h.startupTrace.StartedAt) <= streamStartupTraceMaxAge &&
		(sessionID == "" || h.startupTrace.SessionID == "" || sessionID == h.startupTrace.SessionID) {
		h.addStartupTracePhaseLocked(now, "startup_trace_joined", reason, false)
		return h.startupTrace.ID
	}
	traceID := "stream:" + now.UTC().Format("20060102T150405.000000000Z") + ":" + randomID()
	h.startupTrace = streamStartupTrace{
		ID:        traceID,
		SessionID: sessionID,
		Reason:    reason,
		StartedAt: now,
		LastAt:    now,
	}
	h.addStartupTracePhaseLocked(now, "startup_trace_started", reason, false)
	return traceID
}

func (h *directStreamHub) recordStartupPhase(name string, detail string) {
	h.recordStartupPhaseWithMode(name, detail, false, false)
}

func (h *directStreamHub) recordStartupPhaseOnce(name string, detail string) {
	h.recordStartupPhaseWithMode(name, detail, true, false)
}

func (h *directStreamHub) completeStartupTrace(name string, detail string) {
	h.recordStartupPhaseWithMode(name, detail, true, true)
}

func (h *directStreamHub) recordStartupPhaseWithMode(name string, detail string, once bool, complete bool) {
	name = trimLogField(strings.TrimSpace(name), 96)
	if name == "" {
		return
	}
	now := time.Now()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.startupTrace.ID == "" {
		return
	}
	if h.startupTrace.Complete {
		return
	}
	if now.Sub(h.startupTrace.StartedAt) > streamStartupTraceMaxAge {
		return
	}
	phaseAdded := h.addStartupTracePhaseLocked(now, name, detail, once)
	if complete && phaseAdded {
		h.startupTrace.Complete = true
	}
}

func (h *directStreamHub) addStartupTracePhaseLocked(now time.Time, name string, detail string, once bool) bool {
	if h.startupTrace.ID == "" {
		return false
	}
	name = trimLogField(strings.TrimSpace(name), 96)
	if name == "" {
		return false
	}
	if once {
		for _, phase := range h.startupTrace.Phases {
			if phase.Name == name {
				return false
			}
		}
	}
	detail = trimLogField(strings.TrimSpace(detail), 500)
	startedAt := h.startupTrace.StartedAt
	if startedAt.IsZero() {
		startedAt = now
		h.startupTrace.StartedAt = now
	}
	previousAt := h.startupTrace.LastAt
	if previousAt.IsZero() {
		previousAt = startedAt
	}
	sinceStart := now.Sub(startedAt)
	if sinceStart < 0 {
		sinceStart = 0
	}
	sincePrevious := now.Sub(previousAt)
	if sincePrevious < 0 {
		sincePrevious = 0
	}
	h.startupTrace.Phases = append(h.startupTrace.Phases, streamStartupTracePhase{
		Name:                name,
		Detail:              detail,
		At:                  now.UTC().Format(time.RFC3339Nano),
		SinceStartMillis:    durationMillis(sinceStart),
		SincePreviousMillis: durationMillis(sincePrevious),
	})
	if len(h.startupTrace.Phases) > streamStartupTraceMaxPhases {
		h.startupTrace.Phases = append([]streamStartupTracePhase(nil), h.startupTrace.Phases[len(h.startupTrace.Phases)-streamStartupTraceMaxPhases:]...)
	}
	h.startupTrace.LastAt = now
	return true
}

func (h *directStreamHub) startupTraceSnapshot(now time.Time) map[string]any {
	if now.IsZero() {
		now = time.Now()
	}
	trace := h.startupTrace
	if trace.ID == "" {
		return nil
	}
	elapsed := trace.LastAt.Sub(trace.StartedAt)
	if trace.LastAt.IsZero() || trace.LastAt.Before(trace.StartedAt) {
		elapsed = now.Sub(trace.StartedAt)
	}
	if elapsed < 0 {
		elapsed = 0
	}
	lastPhase := ""
	if len(trace.Phases) > 0 {
		lastPhase = trace.Phases[len(trace.Phases)-1].Name
	}
	return map[string]any{
		"id":            trace.ID,
		"sessionId":     trace.SessionID,
		"reason":        trace.Reason,
		"startedAt":     timeString(trace.StartedAt),
		"lastAt":        timeString(trace.LastAt),
		"ageMillis":     ageSinceMillis(now, trace.StartedAt),
		"elapsedMillis": durationMillis(elapsed),
		"targetMillis":  durationMillis(streamStartupTraceTarget),
		"overBudget":    elapsed > streamStartupTraceTarget,
		"complete":      trace.Complete,
		"lastPhase":     lastPhase,
		"phaseCount":    len(trace.Phases),
		"phases":        append([]streamStartupTracePhase(nil), trace.Phases...),
	}
}
