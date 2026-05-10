package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	controlCodePhoneResultWait  = 75 * time.Second
	controlCodePhoneCleanupWait = 20 * time.Second
	controlCodeRequestPruneAge  = 5 * time.Minute
)

var controlCodeDigitsPattern = regexp.MustCompile(`^[0-9]{2,9}$`)

type controlCodeRequest struct {
	ID                  string
	SessionID           string
	Email               string
	Digits              string
	Status              string
	Reason              string
	Message             string
	Value               string
	ImageMime           string
	ImageBase64         string
	RequestedAt         time.Time
	StartedAt           time.Time
	CompletedAt         time.Time
	TotalDurationMillis int64
	CleanupPending      bool
	CleanupCompletedAt  time.Time
	CleanupReason       string
	CleanupOK           bool
}

type controlCodeRequestView struct {
	ID                  string `json:"requestId"`
	Status              string `json:"status"`
	Reason              string `json:"reason,omitempty"`
	Message             string `json:"message,omitempty"`
	Value               string `json:"value,omitempty"`
	ImageMime           string `json:"imageMime,omitempty"`
	ImageBase64         string `json:"imageBase64,omitempty"`
	RequestedAt         string `json:"requestedAt,omitempty"`
	StartedAt           string `json:"startedAt,omitempty"`
	CompletedAt         string `json:"completedAt,omitempty"`
	ResultExpiresAt     string `json:"resultExpiresAt,omitempty"`
	ResultRemainingMS   int64  `json:"resultRemainingMs,omitempty"`
	QueuePosition       int    `json:"queuePosition,omitempty"`
	TotalDurationMillis int64  `json:"totalDurationMillis,omitempty"`
	CleanupPending      bool   `json:"cleanupPending,omitempty"`
}

func cleanControlCodeDigits(value string) string {
	return strings.TrimSpace(value)
}

func validControlCodeDigits(value string) bool {
	return controlCodeDigitsPattern.MatchString(value)
}

func (s *Server) createControlCodeRequest(email string, sessionID string, digits string, now time.Time) (controlCodeRequestView, string, error) {
	digits = cleanControlCodeDigits(digits)
	if !validControlCodeDigits(digits) {
		return controlCodeRequestView{}, "invalid_code", fmt.Errorf("control code must contain 2-9 digits")
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
	if req == nil || req.Status != controlCodeRunning {
		s.codeMu.Unlock()
		return
	}
	digits := req.Digits
	s.codeMu.Unlock()

	if !s.relay.Snapshot().Connected {
		s.completeControlCodeRequest(requestID, false, "phone_unavailable", "", "", "", 0, false)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), controlCodePhoneSendTTL)
	err := s.relay.SendJSON(ctx, map[string]any{
		"type":      "generate_control_code",
		"requestId": requestID,
		"digits":    digits,
	})
	cancel()
	if err != nil {
		s.completeControlCodeRequest(requestID, false, "phone_unavailable", "", "", "", 0, false)
		return
	}
	go s.timeoutControlCodeRequest(requestID, time.Now().Add(controlCodePhoneResultWait))
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
	s.completeControlCodeRequest(requestID, false, "phone_timeout", "", "", "", 0, false)
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
	imageMime, _ := msg["imageMime"].(string)
	imageBase64, _ := msg["imageBase64"].(string)
	cleanupPending, _ := msg["cleanupPending"].(bool)
	totalDurationMillis := int64(0)
	switch raw := msg["totalDurationMillis"].(type) {
	case float64:
		totalDurationMillis = int64(raw)
	case int64:
		totalDurationMillis = raw
	case json.Number:
		totalDurationMillis, _ = raw.Int64()
	}
	s.completeControlCodeRequest(requestID, ok, reason, value, imageMime, imageBase64, totalDurationMillis, cleanupPending)
	return true
}

func (s *Server) completeControlCodeRequest(requestID string, ok bool, reason string, value string, imageMime string, imageBase64 string, totalDurationMillis int64, cleanupPending bool) {
	now := time.Now()
	var req *controlCodeRequest
	var view controlCodeRequestView
	var resultExpiresAt time.Time
	var email, sessionID string
	shouldStartNext := false
	shouldWaitForCleanup := false
	imageBase64 = strings.TrimSpace(imageBase64)
	if ok && imageBase64 == "" {
		ok = false
		if strings.TrimSpace(reason) == "" || strings.TrimSpace(reason) == "generated" {
			reason = "control_code_image_capture_failed"
		}
		cleanupPending = false
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
		req.ImageMime = strings.TrimSpace(imageMime)
		req.ImageBase64 = imageBase64
		if req.ImageMime == "" && req.ImageBase64 != "" {
			req.ImageMime = "image/png"
		}
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
	if ok && !req.CompletedAt.IsZero() {
		resultExpiresAt = req.CompletedAt.Add(controlCodeResultTTL)
	}
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
}

func (s *Server) timeoutControlCodeCleanup(requestID string, deadline time.Time) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	s.completeControlCodeCleanup(requestID, false, "control_code_cleanup_timeout")
}

func (s *Server) completeControlCodeCleanup(requestID string, ok bool, reason string) {
	requestID = strings.TrimSpace(requestID)
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
	if req.CleanupPending {
		req.CleanupPending = false
		req.CleanupOK = ok
		req.CleanupReason = strings.TrimSpace(reason)
		req.CleanupCompletedAt = now.UTC()
		if req.Status == controlCodeSucceeded && !ok && req.Reason == "generated" {
			req.Reason = "control_code_cleanup_attention_needed"
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
		req.Reason = strings.TrimSpace(reason)
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
}

func (s *Server) closeControlCodeRequest(email string, requestID string, now time.Time) (controlCodeRequestView, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	requestID = strings.TrimSpace(requestID)
	shouldStartNext := false
	s.codeMu.Lock()
	req := s.codeRequests[requestID]
	if req == nil || req.Email != email {
		s.codeMu.Unlock()
		return controlCodeRequestView{}, false
	}
	if req.Status == controlCodeRunning && !req.CleanupPending {
		view := s.controlCodeViewLocked(req, now)
		s.codeMu.Unlock()
		return view, true
	}
	req.Status = controlCodeClosed
	req.ImageBase64 = ""
	req.ImageMime = ""
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
		ID:                  req.ID,
		Status:              req.Status,
		Reason:              req.Reason,
		Message:             req.Message,
		Value:               req.Value,
		TotalDurationMillis: req.TotalDurationMillis,
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
	view.CleanupPending = req.CleanupPending
	if req.Status == controlCodeQueued {
		for index, id := range s.codeQueue {
			if id == req.ID {
				view.QueuePosition = index + 1
				break
			}
		}
	}
	if req.Status == controlCodeSucceeded {
		view.ImageMime = req.ImageMime
		view.ImageBase64 = req.ImageBase64
		if !req.CompletedAt.IsZero() {
			expiresAt := req.CompletedAt.Add(controlCodeResultTTL).UTC()
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
	for _, c := range s.clientSnapshot() {
		if c.video {
			continue
		}
		if strings.ToLower(strings.TrimSpace(c.email)) != email {
			continue
		}
		c.sendJSON(context.Background(), map[string]any{"type": "control_code_request", "request": view})
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
		s.expireControlCodeRequestLocked(req, now)
		if req.Status == controlCodeClosed || req.Status == controlCodeExpired {
			continue
		}
		candidateTime := req.CompletedAt
		if candidateTime.IsZero() {
			candidateTime = req.StartedAt
		}
		if candidateTime.IsZero() {
			candidateTime = req.RequestedAt
		}
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
		log.Printf("ticket control code audit failed for %s: %v", id.Email, err)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "request": view})
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
	if now.Before(req.CompletedAt.Add(controlCodeResultTTL)) {
		return false
	}
	req.Status = controlCodeExpired
	req.ImageBase64 = ""
	req.ImageMime = ""
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
	view, ok := s.closeControlCodeRequest(id.Email, req.RequestID, time.Now())
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not_found", "message": "Control code request was not found."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "request": view})
}
