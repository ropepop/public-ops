package housekeeper

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Snapshot struct {
	Healthy            bool      `json:"healthy"`
	LastAttempt        time.Time `json:"last_attempt,omitempty"`
	LastSuccess        time.Time `json:"last_success,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	Message            string    `json:"message,omitempty"`
	TorrentCount       int       `json:"torrent_count"`
	Admitted           int       `json:"admitted"`
	Waiting            int       `json:"waiting"`
	Rejected           int       `json:"rejected"`
	Kept               int       `json:"kept"`
	Unmanaged          int       `json:"unmanaged"`
	AdmittedThisPoll   int       `json:"admitted_this_poll"`
	DeletionsRequested int       `json:"deletions_requested"`
	UsedBytes          int64     `json:"used_bytes"`
	ReservedBytes      int64     `json:"reserved_bytes"`
	CommittedBytes     int64     `json:"committed_bytes"`
	SoftCapBytes       int64     `json:"soft_cap_bytes"`
}

type StatusStore struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func NewStatusStore() *StatusStore {
	return &StatusStore{snapshot: Snapshot{Message: "waiting for first reconciliation"}}
}

func (s *StatusStore) RecordSuccess(snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snapshot
	s.snapshot.Healthy = true
	s.snapshot.LastError = ""
}

func (s *StatusStore) RecordFailure(at time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Healthy = false
	s.snapshot.LastAttempt = at
	s.snapshot.LastError = err.Error()
	s.snapshot.Message = "reconciliation failed closed"
}

func (s *StatusStore) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *StatusStore) Handler(now Clock, maxAge time.Duration) http.Handler {
	if now == nil {
		now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		snapshot := s.Snapshot()
		healthy := snapshot.Healthy && !snapshot.LastSuccess.IsZero() && now().Sub(snapshot.LastSuccess) <= maxAge
		writer.Header().Set("Content-Type", "application/json")
		if !healthy {
			writer.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"healthy":      healthy,
			"last_success": snapshot.LastSuccess,
			"message":      snapshot.Message,
		})
	})
	mux.HandleFunc("GET /status", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(s.Snapshot())
	})
	return mux
}
