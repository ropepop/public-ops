package broker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chatgptbroker/internal/spacetime"
)

type Queue interface {
	SubmitJob(ctx context.Context, id, chatID, userID, chatHash, userHash, project, prompt string, retention time.Duration) (spacetime.Job, error)
	ListJobs(ctx context.Context) ([]spacetime.Job, error)
	GetJob(ctx context.Context, id string) (spacetime.Job, bool, error)
	RequestCancel(ctx context.Context, id string) (spacetime.Job, error)
	Notifications(ctx context.Context) ([]spacetime.Notification, error)
	MarkNotified(ctx context.Context, jobID string) error
	RecordEvent(ctx context.Context, input spacetime.EventInput) error
}

type Server struct {
	queue       Queue
	projectName string
	retention   time.Duration
}

func NewServer(queue Queue, projectName string, retention time.Duration) *Server {
	if retention <= 0 {
		retention = 24 * time.Hour
	}
	return &Server{
		queue:       queue,
		projectName: strings.TrimSpace(projectName),
		retention:   retention,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/api/v1/jobs", s.handleJobs)
	mux.HandleFunc("/api/v1/jobs/", s.handleJobAction)
	mux.HandleFunc("/api/v1/events", s.handleEvents)
	mux.HandleFunc("/api/v1/notifications", s.handleNotifications)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        s.queue != nil,
		"project":   s.projectName,
		"spacetime": s.queue != nil,
		"personal":  true,
	})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "spacetime_not_configured"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		jobs, err := s.queue.ListJobs(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": jobs})
	case http.MethodPost:
		var req struct {
			TelegramChatID int64  `json:"telegramChatId"`
			TelegramUserID int64  `json:"telegramUserId"`
			Prompt         string `json:"prompt"`
			ProjectName    string `json:"projectName"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
			return
		}
		prompt := strings.TrimSpace(req.Prompt)
		if prompt == "" || req.TelegramChatID == 0 || req.TelegramUserID == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "prompt_and_telegram_ids_required"})
			return
		}
		projectName := strings.TrimSpace(req.ProjectName)
		if projectName == "" {
			projectName = s.projectName
		}
		jobID := "cg-" + time.Now().UTC().Format("20060102T150405") + "-" + randomHex(4)
		chatID := strconv.FormatInt(req.TelegramChatID, 10)
		userID := strconv.FormatInt(req.TelegramUserID, 10)
		job, err := s.queue.SubmitJob(
			r.Context(),
			jobID,
			chatID,
			userID,
			hashID(chatID),
			hashID(userID),
			projectName,
			prompt,
			s.retention,
		)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "job": job})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleJobAction(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "spacetime_not_configured"})
		return
	}
	id, action, ok := parseJobAction(strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not_found"})
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		job, found, err := s.queue.GetJob(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "not_found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": job})
	case action == "cancel" && r.Method == http.MethodPost:
		job, err := s.queue.RequestCancel(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job": job})
	case action == "notified" && r.Method == http.MethodPost:
		if err := s.queue.MarkNotified(r.Context(), id); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "spacetime_not_configured"})
		return
	}
	var req struct {
		Component       string `json:"component"`
		Level           string `json:"level"`
		Kind            string `json:"kind"`
		JobID           string `json:"jobId"`
		AttemptID       string `json:"attemptId"`
		PublicText      string `json:"publicText"`
		SafeDetailsJSON string `json:"safeDetailsJson"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "bad_request"})
		return
	}
	if strings.TrimSpace(req.Kind) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "event_kind_required"})
		return
	}
	if strings.TrimSpace(req.SafeDetailsJSON) == "" {
		req.SafeDetailsJSON = "{}"
	}
	if err := s.queue.RecordEvent(r.Context(), spacetime.EventInput{
		Component:       req.Component,
		Level:           req.Level,
		Kind:            req.Kind,
		JobID:           req.JobID,
		AttemptID:       req.AttemptID,
		PublicText:      req.PublicText,
		SafeDetailsJSON: req.SafeDetailsJSON,
		Retention:       s.retention,
	}); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.queue == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": "spacetime_not_configured"})
		return
	}
	notifications, err := s.queue.Notifications(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": safeError(err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "notifications": notifications})
}

func parseJobAction(path string) (id string, action string, ok bool) {
	path = strings.Trim(path, "/")
	if path == "" {
		return "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func hashID(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func randomHex(size int) string {
	if size <= 0 {
		size = 4
	}
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 240 {
		text = text[:240]
	}
	return text
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
