package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

const (
	controlCodeQueued    = "queued"
	controlCodeRunning   = "running"
	controlCodeSucceeded = "succeeded"
	controlCodeFailed    = "failed"
	controlCodeExpired   = "expired"
	controlCodeClosed    = "closed"

	controlCodeRateLimit        = 2
	controlCodeRateWindow       = 60 * time.Second
	controlCodeResultTTL        = 60 * time.Second
	controlCodePhoneSendTTL     = 2 * time.Second
	controlCodePhoneResultWait  = 105 * time.Second
	controlCodePhoneCleanupWait = 30 * time.Second
	controlCodePhoneAcceptWait  = 900 * time.Millisecond
	controlCodePhoneAcceptPoll  = 50 * time.Millisecond
	controlCodePhoneAcceptHTTP  = 250 * time.Millisecond
	controlCodeRequestPruneAge  = 5 * time.Minute
	controlCodePrepareRelayHold = 12 * time.Second
	controlCodeRelayConnectWait = 1500 * time.Millisecond
	controlCodeRelayReadyWait   = 8 * time.Second
	controlCodeNotifySendTTL    = 500 * time.Millisecond
)

var controlCodeDigitsPattern = regexp.MustCompile(`^[0-9]{2,8}$`)

type controlCodeRequest struct {
	ID                      string
	SessionID               string
	Email                   string
	Digits                  string
	Status                  string
	Reason                  string
	Message                 string
	Value                   string
	StreamEpoch             int64
	FrameSequence           int64
	MinFrameSequence        int64
	ResultProof             string
	ResultFrameEpoch        int64
	ResultMinFrameSequence  int64
	ResultProofAt           time.Time
	RequestedAt             time.Time
	StartedAt               time.Time
	CompletedAt             time.Time
	MarkerReceivedAt        time.Time
	CaptureRequired         bool
	CaptureAcknowledgedAt   time.Time
	CaptureRejectedAt       time.Time
	CaptureReason           string
	CaptureFrameEpoch       int64
	CaptureFrameSequence    int64
	ResultWindowClosedAt    time.Time
	CleanupFrameEpoch       int64
	CleanupMinFrameSequence int64
	TotalDurationMillis     int64
	Phases                  map[string]int64
	CleanupPending          bool
	CleanupCompletedAt      time.Time
	CleanupReason           string
	CleanupOK               bool
}

func (req *controlCodeRequest) latestActivityTime() time.Time {
	if !req.CompletedAt.IsZero() {
		return req.CompletedAt
	}
	if !req.StartedAt.IsZero() {
		return req.StartedAt
	}
	return req.RequestedAt
}

type controlCodeRequestView struct {
	ID                      string           `json:"requestId"`
	SessionID               string           `json:"sessionId,omitempty"`
	Status                  string           `json:"status"`
	Reason                  string           `json:"reason,omitempty"`
	Message                 string           `json:"message,omitempty"`
	Value                   string           `json:"value,omitempty"`
	StreamEpoch             int64            `json:"streamEpoch,omitempty"`
	FrameSequence           int64            `json:"frameSequence,omitempty"`
	MinFrameSequence        int64            `json:"minFrameSequence,omitempty"`
	ResultProof             string           `json:"resultProof,omitempty"`
	ResultFrameEpoch        int64            `json:"resultFrameEpoch,omitempty"`
	ResultMinFrameSequence  int64            `json:"resultMinFrameSequence,omitempty"`
	ResultProofAt           string           `json:"resultProofAt,omitempty"`
	RequestedAt             string           `json:"requestedAt,omitempty"`
	StartedAt               string           `json:"startedAt,omitempty"`
	CompletedAt             string           `json:"completedAt,omitempty"`
	MarkerReceivedAt        string           `json:"markerReceivedAt,omitempty"`
	CaptureRequired         bool             `json:"captureRequired,omitempty"`
	CaptureAcknowledgedAt   string           `json:"captureAcknowledgedAt,omitempty"`
	CaptureRejectedAt       string           `json:"captureRejectedAt,omitempty"`
	CaptureReason           string           `json:"captureReason,omitempty"`
	CaptureFrameEpoch       int64            `json:"captureFrameEpoch,omitempty"`
	CaptureFrameSequence    int64            `json:"captureFrameSequence,omitempty"`
	ResultWindowClosedAt    string           `json:"resultWindowClosedAt,omitempty"`
	CleanupFrameEpoch       int64            `json:"cleanupFrameEpoch,omitempty"`
	CleanupMinFrameSequence int64            `json:"cleanupMinFrameSequence,omitempty"`
	ResultExpiresAt         string           `json:"resultExpiresAt,omitempty"`
	ResultRemainingMS       int64            `json:"resultRemainingMs,omitempty"`
	QueuePosition           int              `json:"queuePosition,omitempty"`
	TotalDurationMillis     int64            `json:"totalDurationMillis,omitempty"`
	Phases                  map[string]int64 `json:"phases,omitempty"`
	CleanupPending          bool             `json:"cleanupPending,omitempty"`
	CleanupReason           string           `json:"cleanupReason,omitempty"`
	CleanupOK               *bool            `json:"cleanupOk,omitempty"`
}

type controlCodeRelayPreparationHealth struct {
	RequestID            string `json:"requestId,omitempty"`
	Reason               string `json:"reason,omitempty"`
	OK                   bool   `json:"ok"`
	Result               string `json:"result,omitempty"`
	DurationMillis       int64  `json:"durationMillis,omitempty"`
	TimeoutMillis        int64  `json:"timeoutMillis,omitempty"`
	DirectStartAttempted bool   `json:"directStartAttempted"`
	DirectStartCompleted bool   `json:"directStartCompleted"`
	DirectStartOK        bool   `json:"directStartOk"`
	SocketConnected      bool   `json:"socketConnected"`
	CompletedAt          string `json:"completedAt,omitempty"`
	CompletedAgoMillis   int64  `json:"completedAgoMillis,omitempty"`

	completedAt time.Time
}

func cleanControlCodeDigits(value string) string {
	return strings.TrimSpace(value)
}

func validControlCodeDigits(value string) bool {
	return controlCodeDigitsPattern.MatchString(value)
}

func controlCodeRelayLeaseID(requestID string) string {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ""
	}
	return "control-code:" + requestID
}

func controlCodePrepareRelayLeaseID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = randomID()
	}
	return sessionID
}

func (s *Server) retainControlCodeRelay(requestID string) {
	leaseID := controlCodeRelayLeaseID(requestID)
	if leaseID == "" {
		return
	}
	hold := controlCodePhoneResultWait + controlCodePhoneCleanupWait + 5*time.Second
	s.acquireTicketPhoneLeaseAsync(leaseID, requestID, "control_code_request", hold)
	s.retainRelayViewerForPrewarm(leaseID, hold)
}

func (s *Server) releaseControlCodeRelay(requestID string) {
	if leaseID := controlCodeRelayLeaseID(requestID); leaseID != "" {
		s.releaseRetainedRelayViewer(leaseID)
		s.releaseTicketPhoneLeaseAsync(leaseID, requestID)
	}
}

func (s *Server) waitForPhoneRelayConnected(reason string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = controlCodeRelayConnectWait
	}
	s.relay.EnsureActive(reason)
	deadline := time.Now().Add(timeout)
	for {
		if s.relay.Snapshot().Connected {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *Server) PrepareControlCodeRelay(requestID string) bool {
	s.retainControlCodeRelay(requestID)
	result := s.preparePhoneRelayForControlCodeDetailed("control_code_request", controlCodeRelayReadyWait)
	result.RequestID = strings.TrimSpace(requestID)
	s.rememberControlCodeRelayPreparation(result)
	return result.OK
}

func (s *Server) preparePhoneRelayForControlCode(reason string, timeout time.Duration) bool {
	result := s.preparePhoneRelayForControlCodeDetailed(reason, timeout)
	s.rememberControlCodeRelayPreparation(result)
	return result.OK
}

func (s *Server) preparePhoneRelayForControlCodeDetailed(reason string, timeout time.Duration) (result controlCodeRelayPreparationHealth) {
	if timeout <= 0 {
		timeout = controlCodeRelayConnectWait
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "control_code_request"
	}
	result = controlCodeRelayPreparationHealth{
		Reason:        reason,
		TimeoutMillis: int64(timeout / time.Millisecond),
	}
	startedAt := time.Now()
	defer func() {
		completedAt := time.Now()
		result.DurationMillis = int64(completedAt.Sub(startedAt) / time.Millisecond)
		result.SocketConnected = s.relay.Snapshot().Connected
		result.CompletedAt = completedAt.UTC().Format(time.RFC3339Nano)
		result.completedAt = completedAt
	}()
	s.appendStreamCommandAsync("prepare_control_code", reason, map[string]any{
		"type":   "prepare_control_code",
		"owner":  "ticket",
		"app":    "vivi",
		"flow":   "control_code",
		"source": "ticket_remote",
	}, streamCommandTTL)
	result.OK = true
	result.Result = "queued_spacetime_direct"
	return result
}

func (s *Server) rememberControlCodeRelayPreparation(result controlCodeRelayPreparationHealth) {
	s.controlCodeRelayPrepMu.Lock()
	s.lastControlCodeRelayPrep = result
	s.controlCodeRelayPrepMu.Unlock()
}

func (s *Server) controlCodeRelayPreparationSnapshot(now time.Time) controlCodeRelayPreparationHealth {
	s.controlCodeRelayPrepMu.RLock()
	snapshot := s.lastControlCodeRelayPrep
	s.controlCodeRelayPrepMu.RUnlock()
	if !snapshot.completedAt.IsZero() {
		snapshot.CompletedAgoMillis = int64(now.Sub(snapshot.completedAt) / time.Millisecond)
		if snapshot.CompletedAgoMillis < 0 {
			snapshot.CompletedAgoMillis = 0
		}
	}
	return snapshot
}

func (s *Server) createControlCodeRequest(email string, sessionID string, digits string, now time.Time) (controlCodeRequestView, string, error) {
	digits = cleanControlCodeDigits(digits)
	if !validControlCodeDigits(digits) {
		return controlCodeRequestView{}, "invalid_code", fmt.Errorf("control code must contain 2-8 digits")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	req := &controlCodeRequest{
		ID:          randomID(),
		SessionID:   strings.TrimSpace(sessionID),
		Email:       email,
		Digits:      digits,
		Status:      controlCodeQueued,
		RequestedAt: now.UTC(),
	}

	s.codeMu.Lock()
	if s.codeRequests == nil {
		s.codeRequests = map[string]*controlCodeRequest{}
	}
	if s.codeRate == nil {
		s.codeRate = map[string][]time.Time{}
	}
	s.pruneControlCodeRequestsLocked(now)
	recent := s.codeRate[email][:0]
	for _, at := range s.codeRate[email] {
		if now.Sub(at) < controlCodeRateWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= controlCodeRateLimit {
		s.codeRate[email] = recent
		s.codeMu.Unlock()
		return controlCodeRequestView{}, "rate_limited", fmt.Errorf("control code can be requested twice per minute")
	}
	recent = append(recent, now.UTC())
	s.codeRate[email] = recent
	s.codeRequests[req.ID] = req
	s.codeQueue = append(s.codeQueue, req.ID)
	view := s.controlCodeViewLocked(req, now)
	s.codeMu.Unlock()

	s.notifyControlCodeRequest(email, sessionID, view)
	s.startNextControlCodeRequest()
	return view, "", nil
}

func (s *Server) startNextControlCodeRequest() {
	var req *controlCodeRequest
	var view controlCodeRequestView
	now := time.Now()
	s.codeMu.Lock()
	if s.codeRunning != "" {
		s.codeMu.Unlock()
		return
	}
	for len(s.codeQueue) > 0 {
		id := s.codeQueue[0]
		s.codeQueue = s.codeQueue[1:]
		candidate := s.codeRequests[id]
		if candidate == nil || candidate.Status != controlCodeQueued {
			continue
		}
		candidate.Status = controlCodeRunning
		candidate.StartedAt = now.UTC()
		s.codeRunning = candidate.ID
		req = candidate
		view = s.controlCodeViewLocked(candidate, now)
		break
	}
	s.codeMu.Unlock()
	if req == nil {
		return
	}
	s.notifyControlCodeRequest(req.Email, req.SessionID, view)
	go s.dispatchControlCodeRequest(req.ID)
}

func (s *Server) dispatchControlCodeRequest(requestID string) {
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil || (req.Status != controlCodeRunning && req.Status != controlCodeSucceeded) {
		s.codeMu.Unlock()
		return
	}
	digits := req.Digits
	s.codeMu.Unlock()

	if !s.PrepareControlCodeRelay(requestID) {
		s.completeControlCodeRequest(requestID, false, "phone_unavailable", "", 0, nil, false)
		return
	}

	serverSentAt := time.Now().UTC()
	command := map[string]any{
		"type":            "generate_control_code",
		"owner":           "ticket",
		"app":             "vivi",
		"flow":            "control_code",
		"requestId":       requestID,
		"digits":          digits,
		"serverSentAt":    serverSentAt.Format(time.RFC3339Nano),
		"dispatchAttempt": 1,
	}
	if ok, reason := s.sendControlCodeCommandWithAcceptance(requestID, command); !ok {
		s.completeControlCodeRequest(requestID, false, reason, "", 0, nil, false)
		return
	}
	go s.timeoutControlCodeRequest(requestID, time.Now().Add(controlCodePhoneResultWait))
}

func (s *Server) sendControlCodeCommandWithAcceptance(requestID string, command map[string]any) (bool, string) {
	if err := s.sendControlCodeCommand(command); err != nil {
		return false, "phone_unavailable"
	}
	return true, ""
}

func (s *Server) sendControlCodeCommand(command map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), controlCodePhoneSendTTL)
	defer cancel()
	_, err := s.appendStreamCommand(ctx, "generate_control_code", "control_code_request", command, controlCodePhoneResultWait+controlCodePhoneCleanupWait)
	return err
}

func (s *Server) controlCodeRequestStillRunning(requestID string) bool {
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	req := s.codeRequests[strings.TrimSpace(requestID)]
	return req != nil && req.Status == controlCodeRunning
}

func (s *Server) timeoutControlCodeRequest(requestID string, deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil || req.Status != controlCodeRunning {
		s.codeMu.Unlock()
		return
	}
	s.codeMu.Unlock()
	s.completeControlCodeRequest(requestID, false, "phone_timeout", "", 0, nil, false)
}

func (s *Server) handleControlCodePhoneResult(msg map[string]any) bool {
	msgType, _ := msg["type"].(string)
	if msgType == "control_code_cleanup_complete" {
		requestID, _ := msg["requestId"].(string)
		ok, _ := msg["ok"].(bool)
		if accepted, hasAccepted := msg["accepted"].(bool); hasAccepted {
			ok = accepted
		}
		reason, _ := msg["reason"].(string)
		s.completeControlCodeCleanup(requestID, ok, reason)
		return true
	}
	if msgType == "control_code_frame_ready" {
		requestID, _ := msg["requestId"].(string)
		value, _ := msg["value"].(string)
		phases := controlCodePhasesFromMessage(msg["phases"])
		totalDurationMillis := controlCodeInt64FromMessage(msg["totalDurationMillis"])
		streamEpoch := controlCodeInt64FromMessage(msg["streamEpoch"])
		frameSequence := controlCodeInt64FromMessage(msg["frameSequence"])
		minFrameSequence := controlCodeInt64FromMessage(msg["minFrameSequence"])
		resultProof, _ := msg["resultProof"].(string)
		resultProofAt, _ := msg["resultProofAt"].(string)
		s.publishSpacetimeControlCodePhoneMarker(requestID, streamEpoch, frameSequence, minFrameSequence, resultProof, resultProofAt)
		s.updateSpacetimeControlCodeRequestAsync(
			requestID,
			controlCodeSucceeded,
			"generated",
			"",
			streamEpoch,
			frameSequence,
			minFrameSequence,
			streamEpoch,
			minFrameSequence,
			resultProof,
			resultProofAt,
			true,
		)
		s.markControlCodeFrameReadyWithProof(requestID, value, streamEpoch, frameSequence, minFrameSequence, totalDurationMillis, phases, resultProof, resultProofAt)
		return true
	}
	if msgType != "control_code_result" {
		return false
	}
	requestID, _ := msg["requestId"].(string)
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return true
	}
	ok, _ := msg["ok"].(bool)
	if accepted, hasAccepted := msg["accepted"].(bool); hasAccepted {
		ok = accepted
	}
	reason, _ := msg["reason"].(string)
	value, _ := msg["value"].(string)
	cleanupPending, _ := msg["cleanupPending"].(bool)
	phases := controlCodePhasesFromMessage(msg["phases"])
	totalDurationMillis := controlCodeInt64FromMessage(msg["totalDurationMillis"])
	resultProof, _ := msg["resultProof"].(string)
	if ok && cleanControlCodeResultProof(resultProof) == "phone_root_image" {
		const reason = "control_code_phone_image_disabled"
		s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeFailed, reason, reason, 0, 0, 0, 0, 0, "", "", true)
		s.completeControlCodeRequest(requestID, false, reason, "", totalDurationMillis, phases, true)
		go s.sendControlCodeResultAckUntilCleanup(requestID, false, reason)
		return true
	}
	if ok {
		ok = false
		if strings.TrimSpace(reason) == "" || strings.TrimSpace(reason) == "generated" || strings.TrimSpace(reason) == "control_code_screenshot_capture_failed" {
			reason = "control_code_stream_marker_required"
		}
		cleanupPending = true
	}
	s.updateSpacetimeControlCodeRequestAsync(requestID, controlCodeFailed, reason, reason, 0, 0, 0, 0, 0, "", "", cleanupPending)
	s.completeControlCodeRequest(requestID, ok, reason, value, totalDurationMillis, phases, cleanupPending)
	return true
}

func controlCodeInt64FromMessage(raw any) int64 {
	switch typed := raw.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func controlCodeFrameString(value int64) string {
	if value <= 0 {
		return "0"
	}
	return fmt.Sprintf("%d", value)
}

func (s *Server) updateSpacetimeControlCodeRequestAsync(requestID string, status string, reason string, message string, streamEpoch int64, frameSequence int64, minFrameSequence int64, resultFrameEpoch int64, resultMinFrameSequence int64, resultProof string, resultProofAt string, cleanupPending bool) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || s.store == nil {
		return
	}
	go func() {
		if err := s.updateSpacetimeControlCodeRequest(requestID, status, reason, message, streamEpoch, frameSequence, minFrameSequence, resultFrameEpoch, resultMinFrameSequence, resultProof, resultProofAt, cleanupPending); err != nil {
			s.recordRuntimeErrorAsync("control_code_spacetime_update_failed", requestID, err, map[string]any{
				"requestId": requestID,
				"status":    status,
				"reason":    reason,
			})
		}
	}()
}

func (s *Server) updateSpacetimeControlCodeRequest(requestID string, status string, reason string, message string, streamEpoch int64, frameSequence int64, minFrameSequence int64, resultFrameEpoch int64, resultMinFrameSequence int64, resultProof string, resultProofAt string, cleanupPending bool) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || s.store == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), streamControlWriteTimeout)
	defer cancel()
	return s.store.UpdateControlCodeRequest(ctx, state.ControlCodeRequestUpdateInput{
		TicketID:               s.cfg.TicketID,
		RequestID:              requestID,
		Status:                 strings.TrimSpace(status),
		Reason:                 strings.TrimSpace(reason),
		Message:                strings.TrimSpace(message),
		StreamEpoch:            controlCodeFrameString(streamEpoch),
		FrameSequence:          controlCodeFrameString(frameSequence),
		MinFrameSequence:       controlCodeFrameString(minFrameSequence),
		ResultFrameEpoch:       controlCodeFrameString(resultFrameEpoch),
		ResultMinFrameSequence: controlCodeFrameString(resultMinFrameSequence),
		ResultProof:            cleanControlCodeResultProof(resultProof),
		ResultProofAt:          strings.TrimSpace(resultProofAt),
		CleanupPending:         cleanupPending,
		Now:                    time.Now(),
	})
}

func (s *Server) publishSpacetimeControlCodePhoneMarker(requestID string, streamEpoch int64, frameSequence int64, minFrameSequence int64, resultProof string, resultProofAt string) {
	if requestID = strings.TrimSpace(requestID); requestID == "" {
		return
	}
	if strings.TrimSpace(resultProof) == "" {
		resultProof = "phone_visual"
	}
	if err := s.updateSpacetimeControlCodeRequest(
		requestID,
		controlCodeRunning,
		"phone_generated_marker",
		"",
		streamEpoch,
		frameSequence,
		minFrameSequence,
		streamEpoch,
		minFrameSequence,
		resultProof,
		resultProofAt,
		false,
	); err != nil {
		s.recordRuntimeErrorAsync("control_code_marker_update_failed", requestID, err, map[string]any{"requestId": requestID})
	}
}

func controlCodePhasesFromMessage(raw any) map[string]int64 {
	values, ok := raw.(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	phases := make(map[string]int64, len(values))
	for name, value := range values {
		cleanName := strings.TrimSpace(name)
		if cleanName == "" {
			continue
		}
		switch typed := value.(type) {
		case float64:
			phases[cleanName] = int64(typed)
		case int64:
			phases[cleanName] = typed
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				phases[cleanName] = parsed
			}
		}
	}
	if len(phases) == 0 {
		return nil
	}
	return phases
}

func cleanControlCodeResultProof(value string) string {
	switch strings.TrimSpace(value) {
	case "phone_root":
		return "phone_root"
	case "phone_visual":
		return "phone_visual"
	case "phone_visual_root_confirmed":
		return "phone_visual_root_confirmed"
	case "phone_visual_raw_ticket_after_submit":
		return "phone_visual_raw_ticket_after_submit"
	case "phone_root_image":
		return "phone_root_image"
	case "browser_frame":
		return "browser_frame"
	default:
		return ""
	}
}

func parseControlCodeResultProofAt(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func controlCodeResultExpiryBase(req *controlCodeRequest) time.Time {
	if req == nil || req.CompletedAt.IsZero() {
		return time.Time{}
	}
	if req.CaptureRequired && req.CaptureAcknowledgedAt.IsZero() {
		return time.Time{}
	}
	if !req.CaptureAcknowledgedAt.IsZero() {
		return req.CaptureAcknowledgedAt
	}
	return req.CompletedAt
}

func controlCodeResultExpiresAt(req *controlCodeRequest) time.Time {
	base := controlCodeResultExpiryBase(req)
	if base.IsZero() {
		return time.Time{}
	}
	return base.Add(controlCodeResultTTL)
}

func (s *Server) markControlCodeFrameReady(requestID string, value string, streamEpoch int64, frameSequence int64, minFrameSequence int64, totalDurationMillis int64, phases map[string]int64) {
	s.markControlCodeFrameReadyWithProof(requestID, value, streamEpoch, frameSequence, minFrameSequence, totalDurationMillis, phases, "", "")
}

func (s *Server) markControlCodeFrameReadyWithProof(requestID string, value string, streamEpoch int64, frameSequence int64, minFrameSequence int64, totalDurationMillis int64, phases map[string]int64, resultProof string, resultProofAt string) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	now := time.Now()
	var email, sessionID string
	var view controlCodeRequestView
	var resultExpiresAt time.Time
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil || req.Status != controlCodeRunning {
		s.codeMu.Unlock()
		return
	}
	s.completeControlCodeFromMarkerLocked(req, value, "generated", streamEpoch, frameSequence, minFrameSequence, totalDurationMillis, phases, resultProof, resultProofAt, now)
	email = req.Email
	sessionID = req.SessionID
	view = s.controlCodeViewLocked(req, now)
	resultExpiresAt = controlCodeResultExpiresAt(req)
	s.codeMu.Unlock()

	s.notifyControlCodeRequest(email, sessionID, view)
	if !resultExpiresAt.IsZero() {
		go s.expireControlCodeRequestAt(requestID, resultExpiresAt)
	}
}

func (s *Server) completeControlCodeFromMarker(requestID string, value string, reason string, streamEpoch int64, frameSequence int64, minFrameSequence int64, totalDurationMillis int64, phases map[string]int64) {
	s.completeControlCodeFromMarkerWithProof(requestID, value, reason, streamEpoch, frameSequence, minFrameSequence, totalDurationMillis, phases, "", "")
}

func (s *Server) completeControlCodeFromMarkerWithProof(requestID string, value string, reason string, streamEpoch int64, frameSequence int64, minFrameSequence int64, totalDurationMillis int64, phases map[string]int64, resultProof string, resultProofAt string) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	now := time.Now()
	var email, sessionID string
	var view controlCodeRequestView
	var resultExpiresAt time.Time
	var notify bool
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil {
		s.codeMu.Unlock()
		return
	}
	if req.Status != controlCodeRunning && req.Status != controlCodeSucceeded {
		s.codeMu.Unlock()
		return
	}
	if req.Status == controlCodeSucceeded && !req.CompletedAt.IsZero() {
		s.codeMu.Unlock()
		return
	}
	s.completeControlCodeFromMarkerLocked(req, value, reason, streamEpoch, frameSequence, minFrameSequence, totalDurationMillis, phases, resultProof, resultProofAt, now)
	email = req.Email
	sessionID = req.SessionID
	view = s.controlCodeViewLocked(req, now)
	notify = true
	resultExpiresAt = controlCodeResultExpiresAt(req)
	s.codeMu.Unlock()

	if notify {
		s.notifyControlCodeRequest(email, sessionID, view)
	}
	if !resultExpiresAt.IsZero() {
		go s.expireControlCodeRequestAt(requestID, resultExpiresAt)
	}
}

func (s *Server) completeControlCodeFromMarkerLocked(req *controlCodeRequest, value string, reason string, streamEpoch int64, frameSequence int64, minFrameSequence int64, totalDurationMillis int64, phases map[string]int64, resultProof string, resultProofAt string, now time.Time) {
	if req == nil {
		return
	}
	req.Status = controlCodeSucceeded
	req.Reason = strings.TrimSpace(reason)
	if req.Reason == "" || req.Reason == "control_code_frame_ready" {
		req.Reason = "generated"
	}
	if cleanValue := strings.TrimSpace(value); cleanValue != "" || req.Value == "" {
		req.Value = cleanValue
	}
	if frameSequence == 0 {
		frameSequence = minFrameSequence
	}
	if minFrameSequence == 0 {
		minFrameSequence = frameSequence
	}
	if streamEpoch > 0 {
		req.StreamEpoch = streamEpoch
	}
	if frameSequence > 0 {
		req.FrameSequence = frameSequence
	}
	if minFrameSequence > 0 {
		req.MinFrameSequence = minFrameSequence
	}
	if cleanProof := cleanControlCodeResultProof(resultProof); cleanProof != "" {
		req.ResultProof = cleanProof
		req.ResultFrameEpoch = streamEpoch
		req.ResultMinFrameSequence = minFrameSequence
		if parsedProofAt, ok := parseControlCodeResultProofAt(resultProofAt); ok {
			req.ResultProofAt = parsedProofAt
		} else if req.ResultProofAt.IsZero() {
			req.ResultProofAt = now.UTC()
		}
	}
	if req.CompletedAt.IsZero() {
		req.CompletedAt = now.UTC()
	}
	if req.MarkerReceivedAt.IsZero() {
		req.MarkerReceivedAt = now.UTC()
	}
	req.CaptureRequired = true
	req.CaptureAcknowledgedAt = time.Time{}
	req.CaptureRejectedAt = time.Time{}
	req.CaptureReason = "waiting_for_browser_capture"
	req.CaptureFrameEpoch = 0
	req.CaptureFrameSequence = 0
	if totalDurationMillis > 0 || req.TotalDurationMillis == 0 {
		req.TotalDurationMillis = totalDurationMillis
	}
	if len(phases) > 0 {
		req.Phases = phases
	}
	req.CleanupPending = true
}

func (s *Server) completeControlCodeRequest(requestID string, ok bool, reason string, value string, totalDurationMillis int64, phases map[string]int64, cleanupPending bool) {
	now := time.Now()
	var req *controlCodeRequest
	var view controlCodeRequestView
	var resultExpiresAt time.Time
	var email, sessionID string
	shouldStartNext := false
	shouldWaitForCleanup := false
	if ok {
		ok = false
		if strings.TrimSpace(reason) == "" || strings.TrimSpace(reason) == "generated" || strings.TrimSpace(reason) == "control_code_screenshot_capture_failed" {
			reason = "control_code_stream_marker_required"
		}
		cleanupPending = true
	}
	s.codeMu.Lock()
	req = s.codeRequests[requestID]
	if req == nil || req.Status != controlCodeRunning {
		if req != nil && req.Status == controlCodeClosed && s.codeRunning == requestID && !cleanupPending {
			s.codeRunning = ""
			shouldStartNext = true
		}
		s.codeMu.Unlock()
		if shouldStartNext {
			s.startNextControlCodeRequest()
		}
		return
	}
	if ok {
		req.Status = controlCodeSucceeded
		req.Value = strings.TrimSpace(value)
	} else {
		req.Status = controlCodeFailed
	}
	req.Reason = strings.TrimSpace(reason)
	if req.Reason == "" {
		if ok {
			req.Reason = "generated"
		} else {
			req.Reason = "failed"
		}
	}
	req.CompletedAt = now.UTC()
	req.TotalDurationMillis = totalDurationMillis
	req.Phases = phases
	req.CleanupPending = cleanupPending
	if s.codeRunning == requestID && !req.CleanupPending {
		s.codeRunning = ""
		shouldStartNext = true
	} else if req.CleanupPending {
		shouldWaitForCleanup = true
	}
	view = s.controlCodeViewLocked(req, now)
	email = req.Email
	sessionID = req.SessionID
	resultExpiresAt = controlCodeResultExpiresAt(req)
	s.codeMu.Unlock()

	s.notifyControlCodeRequest(email, sessionID, view)
	if ok && !resultExpiresAt.IsZero() {
		go s.expireControlCodeRequestAt(requestID, resultExpiresAt)
	}
	if shouldWaitForCleanup {
		go s.timeoutControlCodeCleanup(requestID, time.Now().Add(controlCodePhoneCleanupWait))
	}
	if shouldStartNext {
		s.startNextControlCodeRequest()
	}
	if !shouldWaitForCleanup {
		s.releaseControlCodeRelay(requestID)
	}
}

func (s *Server) timeoutControlCodeCleanup(requestID string, deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	s.completeControlCodeCleanup(requestID, false, "control_code_cleanup_timeout")
}

func (s *Server) completeControlCodeCleanup(requestID string, ok bool, reason string) {
	s.completeControlCodeCleanupWithFrame(requestID, ok, reason, 0, 0)
}

func (s *Server) completeControlCodeCleanupWithFrame(requestID string, ok bool, reason string, streamEpoch int64, minFrameSequence int64) {
	requestID = strings.TrimSpace(requestID)
	reason = strings.TrimSpace(reason)
	now := time.Now()
	var shouldStartNext bool
	var email, sessionID string
	var view controlCodeRequestView
	var notify bool
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil {
		s.codeMu.Unlock()
		return
	}
	if req.Status == controlCodeSucceeded && req.CaptureRequired && req.CaptureAcknowledgedAt.IsZero() && reason == "control_code_cleanup_timeout" {
		req.Status = controlCodeFailed
		req.Reason = "result_window_closed_before_capture"
		req.Value = ""
		req.CaptureRequired = false
		req.CaptureRejectedAt = now.UTC()
		req.CaptureReason = req.Reason
		if req.ResultWindowClosedAt.IsZero() {
			req.ResultWindowClosedAt = now.UTC()
		}
		req.CleanupPending = false
		req.CleanupOK = ok
		req.CleanupReason = reason
		req.CleanupCompletedAt = now.UTC()
		req.CompletedAt = now.UTC()
		if streamEpoch > 0 {
			req.CleanupFrameEpoch = streamEpoch
		}
		if minFrameSequence > 0 {
			req.CleanupMinFrameSequence = minFrameSequence
		}
		if s.codeRunning == requestID {
			s.codeRunning = ""
			shouldStartNext = true
		}
		email = req.Email
		sessionID = req.SessionID
		view = s.controlCodeViewLocked(req, now)
		notify = true
		s.codeMu.Unlock()
		if notify {
			s.notifyControlCodeRequest(email, sessionID, view)
		}
		if shouldStartNext {
			s.startNextControlCodeRequest()
		}
		s.releaseControlCodeRelay(requestID)
		return
	}
	// The phone has returned to the raw ticket screen while a
	// successful result is awaiting browser capture. Previously this
	// immediately failed the request with "result_window_closed_before_capture",
	// which caused a race on slow browser captures (the 17s ViVi
	// automation plus the slow browser-side capture pipeline often
	// exceeded the phone's cleanup window). Now we record the cleanup
	// state but keep the request open so the browser can still
	// acknowledge the capture. The 30s controlCodePhoneCleanupWait
	// timeout bounds the worst case.
	if req.Status == controlCodeSucceeded && req.CaptureRequired && req.CaptureAcknowledgedAt.IsZero() {
		if streamEpoch > 0 {
			req.CleanupFrameEpoch = streamEpoch
		}
		if minFrameSequence > 0 {
			req.CleanupMinFrameSequence = minFrameSequence
		}
		req.ResultWindowClosedAt = now.UTC()
		req.CaptureReason = "phone_returned_to_raw_ticket_pending_browser_capture"
		email = req.Email
		sessionID = req.SessionID
		view = s.controlCodeViewLocked(req, now)
		notify = req.Status != controlCodeClosed
	}
	if req.CleanupPending {
		req.CleanupPending = false
		req.CleanupOK = ok
		req.CleanupReason = reason
		// Only mark CleanupCompletedAt when the browser has already
		// acknowledged the capture, or when the request was never
		// capture-required (it didn't need a browser frame). If the
		// capture is still pending, leave CleanupCompletedAt zero so
		// confirmControlCodeBrowserCapture keeps accepting captures
		// until the 30s controlCodePhoneCleanupWait timeout fires.
		if req.CaptureAcknowledgedAt.IsZero() && (req.CaptureRequired || !req.MarkerReceivedAt.IsZero()) {
			// Defer; the browser still has time.
		} else {
			req.CleanupCompletedAt = now.UTC()
		}
		if streamEpoch > 0 {
			req.CleanupFrameEpoch = streamEpoch
		}
		if minFrameSequence > 0 {
			req.CleanupMinFrameSequence = minFrameSequence
		}
		email = req.Email
		sessionID = req.SessionID
		view = s.controlCodeViewLocked(req, now)
		notify = req.Status != controlCodeClosed
	}
	if s.codeRunning == requestID {
		s.codeRunning = ""
		shouldStartNext = true
	}
	if req.Status == controlCodeRunning {
		req.Status = controlCodeFailed
		req.Reason = reason
		if req.Reason == "return_to_raw_complete" {
			req.Reason = "control_code_not_generated"
		}
		if req.Reason == "" {
			req.Reason = "control_code_cleanup_finished_without_result"
		}
		req.CompletedAt = now.UTC()
		email = req.Email
		sessionID = req.SessionID
		view = s.controlCodeViewLocked(req, now)
		notify = true
	}
	s.codeMu.Unlock()
	if notify {
		s.notifyControlCodeRequest(email, sessionID, view)
	}
	if shouldStartNext {
		s.startNextControlCodeRequest()
	}
	s.releaseControlCodeRelay(requestID)
}

func (s *Server) sendControlCodeBrowserCaptureAck(requestID string, ok bool, reason string, frameEpoch int64, frameSequence int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), controlCodePhoneSendTTL)
	defer cancel()
	_, err := s.appendStreamCommand(ctx, "control_code_browser_capture", strings.TrimSpace(reason), map[string]any{
		"type":                   "control_code_browser_capture",
		"owner":                  "ticket",
		"app":                    "vivi",
		"flow":                   "control_code",
		"requestId":              requestID,
		"ok":                     ok,
		"accepted":               ok,
		"reason":                 strings.TrimSpace(reason),
		"candidateFrameEpoch":    frameEpoch,
		"candidateFrameSequence": frameSequence,
		"sentAt":                 time.Now().UTC().Format(time.RFC3339Nano),
	}, streamCommandTTL)
	if err != nil {
		s.recordRuntimeErrorAsync("control_code_browser_capture_ack_failed", requestID, err, map[string]any{
			"requestId": requestID,
			"reason":    reason,
		})
	}
	return err
}

func (s *Server) controlCodeBrowserCaptureAckStillNeeded(requestID string) bool {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	req := s.codeRequests[requestID]
	return req != nil && req.CleanupPending
}

func (s *Server) sendControlCodeBrowserCaptureAckUntilCleanup(requestID string, ok bool, reason string, frameEpoch int64, frameSequence int64) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	delay := 150 * time.Millisecond
	for attempt := 1; ; attempt++ {
		if !s.controlCodeBrowserCaptureAckStillNeeded(requestID) {
			return
		}
		if attempt == 1 || attempt%5 == 0 {
			s.retainControlCodeRelay(requestID)
			s.relay.EnsureActive("control_code_browser_capture_ack")
		}
		_ = s.sendControlCodeBrowserCaptureAck(requestID, ok, reason, frameEpoch, frameSequence)
		time.Sleep(delay)
		if delay < time.Second {
			delay += 150 * time.Millisecond
		}
	}
}

func (s *Server) sendControlCodeResultAck(requestID string, ok bool, reason string) error {
	ctx, cancel := context.WithTimeout(context.Background(), controlCodePhoneSendTTL)
	defer cancel()
	_, err := s.appendStreamCommand(ctx, "control_code_result_ack", strings.TrimSpace(reason), map[string]any{
		"type":      "control_code_result_ack",
		"owner":     "ticket",
		"app":       "vivi",
		"flow":      "control_code",
		"requestId": requestID,
		"ok":        ok,
		"accepted":  ok,
		"reason":    strings.TrimSpace(reason),
		"sentAt":    time.Now().UTC().Format(time.RFC3339Nano),
	}, streamCommandTTL)
	if err != nil {
		s.recordRuntimeErrorAsync("control_code_result_ack_failed", requestID, err, map[string]any{
			"requestId": requestID,
			"reason":    reason,
		})
	}
	return err
}

func (s *Server) sendControlCodeResultAckUntilCleanup(requestID string, ok bool, reason string) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	delay := 150 * time.Millisecond
	for attempt := 1; ; attempt++ {
		if !s.controlCodeBrowserCaptureAckStillNeeded(requestID) {
			return
		}
		if attempt == 1 || attempt%5 == 0 {
			s.retainControlCodeRelay(requestID)
			s.relay.EnsureActive("control_code_result_ack")
		}
		_ = s.sendControlCodeResultAck(requestID, ok, reason)
		time.Sleep(delay)
		if delay < time.Second {
			delay += 150 * time.Millisecond
		}
	}
}

func (s *Server) closeControlCodeRequest(email string, sessionID string, requestID string, now time.Time) (controlCodeRequestView, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	sessionID = strings.TrimSpace(sessionID)
	requestID = strings.TrimSpace(requestID)
	shouldStartNext := false
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil || req.Email != email || strings.TrimSpace(req.SessionID) != sessionID {
		s.codeMu.Unlock()
		return controlCodeRequestView{}, false
	}
	if req.Status == controlCodeRunning {
		view := s.controlCodeViewLocked(req, now)
		s.codeMu.Unlock()
		return view, true
	}
	if req.Status == controlCodeSucceeded && req.CaptureRequired && req.CaptureAcknowledgedAt.IsZero() {
		req.CaptureRequired = false
		req.CaptureRejectedAt = now.UTC()
		req.CaptureReason = "browser_capture_closed"
		req.ResultWindowClosedAt = now.UTC()
	}
	req.Status = controlCodeClosed
	req.Value = ""
	if s.codeRunning == requestID && !req.CleanupPending {
		s.codeRunning = ""
		shouldStartNext = true
	}
	view := s.controlCodeViewLocked(req, now)
	s.codeMu.Unlock()
	s.notifyControlCodeRequest(req.Email, req.SessionID, view)
	if shouldStartNext {
		s.startNextControlCodeRequest()
	}
	return view, true
}

func (s *Server) controlCodeViewLocked(req *controlCodeRequest, now time.Time) controlCodeRequestView {
	s.expireControlCodeRequestLocked(req, now)
	view := controlCodeRequestView{
		ID:                      req.ID,
		SessionID:               strings.TrimSpace(req.SessionID),
		Status:                  req.Status,
		Reason:                  req.Reason,
		Message:                 req.Message,
		Value:                   req.Value,
		StreamEpoch:             req.StreamEpoch,
		FrameSequence:           req.FrameSequence,
		MinFrameSequence:        req.MinFrameSequence,
		ResultProof:             req.ResultProof,
		ResultFrameEpoch:        req.ResultFrameEpoch,
		ResultMinFrameSequence:  req.ResultMinFrameSequence,
		CaptureRequired:         req.CaptureRequired,
		CaptureReason:           req.CaptureReason,
		CaptureFrameEpoch:       req.CaptureFrameEpoch,
		CaptureFrameSequence:    req.CaptureFrameSequence,
		CleanupFrameEpoch:       req.CleanupFrameEpoch,
		CleanupMinFrameSequence: req.CleanupMinFrameSequence,
		TotalDurationMillis:     req.TotalDurationMillis,
		Phases:                  req.Phases,
	}
	if !req.RequestedAt.IsZero() {
		view.RequestedAt = req.RequestedAt.UTC().Format(time.RFC3339)
	}
	if !req.StartedAt.IsZero() {
		view.StartedAt = req.StartedAt.UTC().Format(time.RFC3339)
	}
	if !req.CompletedAt.IsZero() {
		view.CompletedAt = req.CompletedAt.UTC().Format(time.RFC3339)
	}
	if !req.MarkerReceivedAt.IsZero() {
		view.MarkerReceivedAt = req.MarkerReceivedAt.UTC().Format(time.RFC3339)
	}
	if !req.CaptureAcknowledgedAt.IsZero() {
		view.CaptureAcknowledgedAt = req.CaptureAcknowledgedAt.UTC().Format(time.RFC3339Nano)
	}
	if !req.CaptureRejectedAt.IsZero() {
		view.CaptureRejectedAt = req.CaptureRejectedAt.UTC().Format(time.RFC3339Nano)
	}
	if !req.ResultWindowClosedAt.IsZero() {
		view.ResultWindowClosedAt = req.ResultWindowClosedAt.UTC().Format(time.RFC3339Nano)
	}
	if !req.ResultProofAt.IsZero() {
		view.ResultProofAt = req.ResultProofAt.UTC().Format(time.RFC3339Nano)
	}
	view.CleanupPending = req.CleanupPending
	if !req.CleanupCompletedAt.IsZero() {
		view.CleanupReason = req.CleanupReason
		cleanupOK := req.CleanupOK
		view.CleanupOK = &cleanupOK
	}
	if req.Status == controlCodeQueued {
		for index, id := range s.codeQueue {
			if id == req.ID {
				view.QueuePosition = index + 1
				break
			}
		}
	}
	if req.Status == controlCodeSucceeded {
		if expiresAt := controlCodeResultExpiresAt(req); !expiresAt.IsZero() {
			expiresAt = expiresAt.UTC()
			view.ResultExpiresAt = expiresAt.Format(time.RFC3339)
			view.ResultRemainingMS = int64(time.Until(expiresAt) / time.Millisecond)
			if now.Before(expiresAt) {
				view.ResultRemainingMS = int64(expiresAt.Sub(now.UTC()) / time.Millisecond)
			}
			if view.ResultRemainingMS < 0 {
				view.ResultRemainingMS = 0
			}
		}
	}
	return view
}

func (s *Server) notifyControlCodeRequest(email string, sessionID string, view controlCodeRequestView) {
	email = strings.ToLower(strings.TrimSpace(email))
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	body, err := json.Marshal(map[string]any{"type": "control_code_request", "request": view})
	if err != nil {
		return
	}
	for _, c := range s.clientSnapshot() {
		if c.video {
			continue
		}
		if strings.ToLower(strings.TrimSpace(c.email)) != email {
			continue
		}
		if strings.TrimSpace(c.sessionID) != sessionID {
			continue
		}
		client := c
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), controlCodeNotifySendTTL)
			defer cancel()
			client.sendText(ctx, body)
		}()
	}
}

func (s *Server) sendLatestControlCodeRequest(ctx context.Context, c *client) {
	now := time.Now()
	email := strings.ToLower(strings.TrimSpace(c.email))
	var view controlCodeRequestView
	found := false
	var latest time.Time
	s.codeMu.Lock()
	s.pruneControlCodeRequestsLocked(now)
	for _, req := range s.codeRequests {
		if req == nil || req.Email != email {
			continue
		}
		if strings.TrimSpace(req.SessionID) != strings.TrimSpace(c.sessionID) {
			continue
		}
		s.expireControlCodeRequestLocked(req, now)
		if req.Status == controlCodeClosed || req.Status == controlCodeExpired {
			continue
		}
		candidateTime := req.latestActivityTime()
		if !found || candidateTime.After(latest) {
			found = true
			latest = candidateTime
			view = s.controlCodeViewLocked(req, now)
		}
	}
	s.codeMu.Unlock()
	if found {
		c.sendJSON(ctx, map[string]any{"type": "control_code_request", "request": view})
	}
}

func (s *Server) handleControlCodeRequestHTTP(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Digits string `json:"digits"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request", "message": "Could not read request."})
		return
	}
	view, code, err := s.createControlCodeRequest(id.Email, sessionID, req.Digits, time.Now())
	if err != nil {
		status := http.StatusBadRequest
		if code == "rate_limited" {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]any{"ok": false, "error": code, "message": err.Error()})
		return
	}
	if err := s.store.Audit(r.Context(), s.cfg.TicketID, id.Email, "control_code_requested", map[string]any{"requestId": view.ID}, time.Now()); err != nil {
		s.recordRuntimeErrorAsync("control_code_audit_failed", view.ID, err, map[string]any{"requestId": view.ID})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "request": view})
}

func (s *Server) handleControlCodePrepareHTTP(w http.ResponseWriter, r *http.Request, _ auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1024))
	prepareLeaseID := controlCodePrepareRelayLeaseID(sessionID)
	s.acquireTicketPhoneLeaseAsync("prepare:"+prepareLeaseID, prepareLeaseID, "control_code_prepare", controlCodePrepareRelayHold)
	s.retainRelayViewerForPrewarm(prepareLeaseID, controlCodePrepareRelayHold)
	if !s.preparePhoneRelayForControlCode("dialog_open", controlCodeRelayConnectWait) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":      true,
			"ready":   false,
			"message": "Phone wake requested.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ready": true})
}

func (s *Server) handleControlCodeCaptureHTTP(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RequestID              string `json:"requestId"`
		CandidateFrameEpoch    int64  `json:"candidateFrameEpoch"`
		CandidateFrameSequence int64  `json:"candidateFrameSequence"`
		AcceptedReason         string `json:"acceptedReason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request", "message": "Could not read request."})
		return
	}
	view, status, code, message, ackPhone, frameEpoch, frameSequence := s.confirmControlCodeBrowserCapture(
		id.Email,
		sessionID,
		req.RequestID,
		req.CandidateFrameEpoch,
		req.CandidateFrameSequence,
		req.AcceptedReason,
		time.Now(),
	)
	if code != "" {
		writeJSON(w, status, map[string]any{"ok": false, "error": code, "message": message, "request": view})
		return
	}
	if ackPhone {
		ackRequestID := strings.TrimSpace(view.ID)
		if ackRequestID == "" {
			ackRequestID = strings.TrimSpace(req.RequestID)
		}
		go s.sendControlCodeBrowserCaptureAckUntilCleanup(ackRequestID, true, "browser_capture_confirmed", frameEpoch, frameSequence)
		if resultExpiresAt, err := time.Parse(time.RFC3339, view.ResultExpiresAt); err == nil {
			go s.expireControlCodeRequestAt(ackRequestID, resultExpiresAt)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "request": view})
}

func (s *Server) confirmControlCodeBrowserCapture(email string, sessionID string, requestID string, frameEpoch int64, frameSequence int64, reason string, now time.Time) (controlCodeRequestView, int, string, string, bool, int64, int64) {
	email = strings.ToLower(strings.TrimSpace(email))
	sessionID = strings.TrimSpace(sessionID)
	requestID = strings.TrimSpace(requestID)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "candidate_frame_at_or_after_phone_marker"
	}
	s.codeMu.Lock()
	defer s.codeMu.Unlock()
	req := s.codeRequests[requestID]
	if req == nil || req.Email != email || strings.TrimSpace(req.SessionID) != sessionID {
		return controlCodeRequestView{}, http.StatusNotFound, "not_found", "Control code request was not found.", false, 0, 0
	}
	if req.Status != controlCodeSucceeded {
		return s.controlCodeViewLocked(req, now), http.StatusConflict, "request_not_ready", "The generated code is not ready for capture.", false, 0, 0
	}
	// The phone has signaled it returned to the raw ticket (ResultWindowClosedAt
	// is set), but we no longer auto-fail the request on that signal — we let
	// the browser keep trying to capture within the 30s cleanup window. The
	// only hard failure is the cleanup timeout itself, which sets the status
	// to failed via timeoutControlCodeCleanup. So we accept captures up to
	// that timeout.
	if !req.CleanupCompletedAt.IsZero() {
		return s.controlCodeViewLocked(req, now), http.StatusConflict, "result_window_closed_before_capture", "The generated code was closed before this browser captured it.", false, 0, 0
	}
	markerEpoch := req.ResultFrameEpoch
	if markerEpoch == 0 {
		markerEpoch = req.StreamEpoch
	}
	markerSequence := req.ResultMinFrameSequence
	if markerSequence == 0 {
		markerSequence = req.MinFrameSequence
	}
	if markerSequence == 0 {
		markerSequence = req.FrameSequence
	}
	if markerEpoch > 0 && frameEpoch != markerEpoch {
		return s.controlCodeViewLocked(req, now), http.StatusConflict, "frame_before_marker", "The browser frame does not match the generated-code stream.", false, 0, 0
	}
	if markerSequence > 0 && frameSequence < markerSequence {
		return s.controlCodeViewLocked(req, now), http.StatusConflict, "frame_before_marker", "The browser frame is older than the generated code.", false, 0, 0
	}
	if !req.CaptureAcknowledgedAt.IsZero() {
		return s.controlCodeViewLocked(req, now), http.StatusOK, "", "", true, req.CaptureFrameEpoch, req.CaptureFrameSequence
	}
	req.CaptureRequired = false
	req.CaptureAcknowledgedAt = now.UTC()
	req.CaptureReason = reason
	req.CaptureFrameEpoch = frameEpoch
	req.CaptureFrameSequence = frameSequence
	return s.controlCodeViewLocked(req, now), http.StatusOK, "", "", true, frameEpoch, frameSequence
}

func (s *Server) expireControlCodeRequestAt(requestID string, deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C

	now := time.Now()
	var email, sessionID string
	var view controlCodeRequestView
	notify := false
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if s.expireControlCodeRequestLocked(req, now) {
		email = req.Email
		sessionID = req.SessionID
		view = s.controlCodeViewLocked(req, now)
		notify = true
	}
	s.pruneControlCodeRequestsLocked(now)
	s.codeMu.Unlock()
	if notify {
		s.notifyControlCodeRequest(email, sessionID, view)
	}
}

func (s *Server) expireControlCodeRequestLocked(req *controlCodeRequest, now time.Time) bool {
	if req == nil || req.Status != controlCodeSucceeded || req.CompletedAt.IsZero() {
		return false
	}
	expiresAt := controlCodeResultExpiresAt(req)
	if expiresAt.IsZero() || now.Before(expiresAt) {
		return false
	}
	req.Status = controlCodeExpired
	req.Value = ""
	req.Reason = "expired"
	return true
}

func (s *Server) pruneControlCodeRequestsLocked(now time.Time) {
	for _, req := range s.codeRequests {
		s.expireControlCodeRequestLocked(req, now)
	}
	cutoff := now.Add(-controlCodeRequestPruneAge)
	for id, req := range s.codeRequests {
		if req == nil {
			delete(s.codeRequests, id)
			continue
		}
		if req.Status == controlCodeQueued || req.Status == controlCodeRunning || req.CleanupPending || s.codeRunning == id {
			continue
		}
		finalAt := req.CompletedAt
		if finalAt.IsZero() {
			finalAt = req.CleanupCompletedAt
		}
		if finalAt.IsZero() {
			finalAt = req.RequestedAt
		}
		if !finalAt.IsZero() && finalAt.Before(cutoff) {
			delete(s.codeRequests, id)
		}
	}
	if len(s.codeQueue) > 0 {
		kept := s.codeQueue[:0]
		for _, id := range s.codeQueue {
			req := s.codeRequests[id]
			if req != nil && req.Status == controlCodeQueued {
				kept = append(kept, id)
			}
		}
		s.codeQueue = kept
	}
}

func (s *Server) handleControlCodeCloseHTTP(w http.ResponseWriter, r *http.Request, id auth.Identity, sessionID string, _ state.Snapshot) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RequestID string `json:"requestId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request", "message": "Could not read request."})
		return
	}
	view, ok := s.closeControlCodeRequest(id.Email, sessionID, req.RequestID, time.Now())
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not_found", "message": "Control code request was not found."})
		return
	}
	if view.CleanupPending && view.CaptureReason == "browser_capture_closed" {
		go s.sendControlCodeBrowserCaptureAckUntilCleanup(view.ID, false, "browser_capture_closed", 0, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "request": view})
}
