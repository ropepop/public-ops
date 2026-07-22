package runtime

import (
	"sync"
	"time"
)

type CatalogStatus struct {
	Loaded             bool
	LoadedFromFallback bool
	GeneratedAt        time.Time
	LastRefreshAttempt time.Time
	LastRefreshSuccess time.Time
	LastRefreshError   string
	StopCount          int
	RouteCount         int
}

type TelegramStatus struct {
	LastSuccessAt     time.Time
	LastErrorAt       time.Time
	ConsecutiveErrors int
	LastError         string
	LastUpdateID      int64
}

type DumpStatus struct {
	Pending       int
	LastSuccessAt time.Time
	LastError     string
	LastAttemptAt time.Time
}

type ChatAnalyzerStatus struct {
	Enabled                      bool
	DryRun                       bool
	SessionState                 string
	LastCollectAttempt           time.Time
	LastCollectSuccess           time.Time
	LastCollectErrorAt           time.Time
	ConsecutiveCollectErrors     int
	LastCollectedCount           int
	LastSkippedStale             int
	SkippedStaleTotal            int64
	LastExpiredPending           int
	ExpiredPendingTotal          int64
	LastMaintenanceAttempt       time.Time
	LastMaintenanceSuccess       time.Time
	LastMaintenanceErrorAt       time.Time
	ConsecutiveMaintenanceErrors int
	LastMaintenanceErrorCode     string
	LastProcessAttempt           time.Time
	LastProcessSuccess           time.Time
	LastProcessErrorAt           time.Time
	ConsecutiveProcessErrors     int
	LastBatchID                  string
	LastBatchStatus              string
	SelectedModel                string
	LastErrorCode                string
	ModelCircuitOpenUntil        time.Time
	StaleBatchesRecovered        int
}

type State struct {
	mu sync.RWMutex

	startedAt      time.Time
	webEnabled     bool
	webListening   bool
	webBindAddr    string
	lastFatalError string
	catalog        CatalogStatus
	telegram       TelegramStatus
	dump           DumpStatus
	chatAnalyzer   ChatAnalyzerStatus
}

func (s *State) ConfigureChatAnalyzer(enabled, dryRun bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatAnalyzer.Enabled = enabled
	s.chatAnalyzer.DryRun = dryRun
	if !enabled {
		s.chatAnalyzer.SessionState = "disabled"
	} else if s.chatAnalyzer.SessionState == "" || s.chatAnalyzer.SessionState == "disabled" {
		s.chatAnalyzer.SessionState = "unchecked"
	}
}

func (s *State) RecordChatAnalyzerCollection(at time.Time, count, skippedStale int, sessionState, errorCode string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at = at.UTC()
	s.chatAnalyzer.LastCollectAttempt = at
	s.chatAnalyzer.LastCollectedCount = count
	s.chatAnalyzer.LastSkippedStale = skippedStale
	if skippedStale > 0 {
		s.chatAnalyzer.SkippedStaleTotal += int64(skippedStale)
	}
	if sessionState != "" {
		s.chatAnalyzer.SessionState = sessionState
	}
	if errorCode == "" {
		s.chatAnalyzer.LastCollectSuccess = at
		s.chatAnalyzer.LastCollectErrorAt = time.Time{}
		s.chatAnalyzer.ConsecutiveCollectErrors = 0
		if s.chatAnalyzer.LastBatchStatus != "failed" {
			s.chatAnalyzer.LastErrorCode = ""
		}
		return
	}
	s.chatAnalyzer.LastCollectErrorAt = at
	s.chatAnalyzer.ConsecutiveCollectErrors++
	s.chatAnalyzer.LastErrorCode = errorCode
}

func (s *State) RecordChatAnalyzerProcess(at time.Time, batchID, batchStatus, selectedModel, errorCode string, circuitOpenUntil time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at = at.UTC()
	s.chatAnalyzer.LastProcessAttempt = at
	s.chatAnalyzer.LastBatchID = batchID
	s.chatAnalyzer.LastBatchStatus = batchStatus
	s.chatAnalyzer.SelectedModel = selectedModel
	s.chatAnalyzer.ModelCircuitOpenUntil = circuitOpenUntil.UTC()
	if errorCode == "" && batchStatus != "failed" {
		s.chatAnalyzer.LastProcessSuccess = at
		s.chatAnalyzer.LastProcessErrorAt = time.Time{}
		s.chatAnalyzer.ConsecutiveProcessErrors = 0
		if s.chatAnalyzer.ConsecutiveCollectErrors == 0 {
			s.chatAnalyzer.LastErrorCode = ""
		}
		return
	}
	s.chatAnalyzer.LastProcessErrorAt = at
	s.chatAnalyzer.ConsecutiveProcessErrors++
	if errorCode == "" {
		errorCode = "batch_failed"
	}
	s.chatAnalyzer.LastErrorCode = errorCode
}

func (s *State) RecordChatAnalyzerStaleRecovery(count int) {
	if s == nil || count <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatAnalyzer.StaleBatchesRecovered += count
}

func (s *State) RecordChatAnalyzerExpiredPending(count int) {
	if s == nil || count < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chatAnalyzer.LastExpiredPending = count
	if count > 0 {
		s.chatAnalyzer.ExpiredPendingTotal += int64(count)
	}
}

func (s *State) RecordChatAnalyzerMaintenance(at time.Time, errorCode string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	at = at.UTC()
	s.chatAnalyzer.LastMaintenanceAttempt = at
	if errorCode == "" {
		s.chatAnalyzer.LastMaintenanceSuccess = at
		s.chatAnalyzer.LastMaintenanceErrorAt = time.Time{}
		s.chatAnalyzer.ConsecutiveMaintenanceErrors = 0
		s.chatAnalyzer.LastMaintenanceErrorCode = ""
		return
	}
	s.chatAnalyzer.LastMaintenanceErrorAt = at
	s.chatAnalyzer.ConsecutiveMaintenanceErrors++
	s.chatAnalyzer.LastMaintenanceErrorCode = errorCode
}

func (s *State) ChatAnalyzerStatus() ChatAnalyzerStatus {
	if s == nil {
		return ChatAnalyzerStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.chatAnalyzer
}

func New(startedAt time.Time, webEnabled bool, webBindAddr string) *State {
	return &State{
		startedAt:   startedAt.UTC(),
		webEnabled:  webEnabled,
		webBindAddr: webBindAddr,
	}
}

func (s *State) StartedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.startedAt
}

func (s *State) SetWebListening(listening bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webListening = listening
}

func (s *State) WebStatus() (enabled bool, listening bool, bindAddr string) {
	if s == nil {
		return false, false, ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.webEnabled, s.webListening, s.webBindAddr
}

func (s *State) SetFatalError(message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastFatalError = message
}

func (s *State) LastFatalError() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastFatalError
}

func (s *State) UpdateCatalog(status CatalogStatus) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalog = status
}

func (s *State) CatalogStatus() CatalogStatus {
	if s == nil {
		return CatalogStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}

func (s *State) RecordTelegramSuccess(at time.Time, lastUpdateID int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telegram.LastSuccessAt = at.UTC()
	s.telegram.ConsecutiveErrors = 0
	s.telegram.LastError = ""
	s.telegram.LastErrorAt = time.Time{}
	s.telegram.LastUpdateID = lastUpdateID
}

func (s *State) RecordTelegramError(at time.Time, message string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telegram.ConsecutiveErrors++
	s.telegram.LastError = message
	if !at.IsZero() {
		s.telegram.LastErrorAt = at.UTC()
	}
}

func (s *State) TelegramStatus() TelegramStatus {
	if s == nil {
		return TelegramStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.telegram
}

func (s *State) RecordDumpAttempt(at time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dump.LastAttemptAt = at.UTC()
}

func (s *State) RecordDumpSuccess(at time.Time, pending int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dump.LastAttemptAt = at.UTC()
	s.dump.LastSuccessAt = at.UTC()
	s.dump.LastError = ""
	s.dump.Pending = pending
}

func (s *State) RecordDumpError(at time.Time, message string, pending int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dump.LastAttemptAt = at.UTC()
	s.dump.LastError = message
	s.dump.Pending = pending
}

func (s *State) SetDumpPending(pending int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dump.Pending = pending
}

func (s *State) DumpStatus() DumpStatus {
	if s == nil {
		return DumpStatus{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dump
}
