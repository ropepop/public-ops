package store

import (
	"context"
	"errors"
	"time"

	"satiksmebot/internal/model"
)

var (
	ErrDuplicateReport            = errors.New("duplicate report")
	ErrReportVoteIncidentMismatch = errors.New("report and vote incident mismatch")
)

type CleanupResult struct {
	StopSightingsDeleted    int64
	VehicleSightingsDeleted int64
	AreaReportsDeleted      int64
}

type ReportDumpItem struct {
	ID            string
	Payload       string
	Attempts      int
	CreatedAt     time.Time
	NextAttemptAt time.Time
	LastAttemptAt time.Time
	LastError     string
}

type Store interface {
	Migrate(ctx context.Context) error
	HealthCheck(ctx context.Context) error
	InsertStopSighting(ctx context.Context, sighting model.StopSighting) error
	GetLastStopSightingByUserScope(ctx context.Context, userID int64, stopID string) (*model.StopSighting, error)
	ListStopSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.StopSighting, error)
	InsertVehicleSighting(ctx context.Context, sighting model.VehicleSighting) error
	GetLastVehicleSightingByUserScope(ctx context.Context, userID int64, scopeKey string) (*model.VehicleSighting, error)
	ListVehicleSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.VehicleSighting, error)
	InsertAreaReport(ctx context.Context, report model.AreaReport) error
	GetLastAreaReportByUserScope(ctx context.Context, userID int64, scopeKey string) (*model.AreaReport, error)
	ListAreaReportsSince(ctx context.Context, since time.Time, limit int) ([]model.AreaReport, error)
	UpsertIncidentVote(ctx context.Context, vote model.IncidentVote) error
	RecordIncidentVote(ctx context.Context, vote model.IncidentVote, event model.IncidentVoteEvent) error
	ListIncidentVotes(ctx context.Context, incidentID string) ([]model.IncidentVote, error)
	ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]model.IncidentVoteEvent, error)
	CountMapReportsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error)
	CountIncidentVoteEventsByUserSince(ctx context.Context, userID int64, source model.IncidentVoteSource, since time.Time) (int, error)
	InsertIncidentComment(ctx context.Context, comment model.IncidentComment) error
	ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]model.IncidentComment, error)
	EnqueueReportDump(ctx context.Context, item ReportDumpItem) error
	PeekNextReportDump(ctx context.Context) (*ReportDumpItem, error)
	NextReportDump(ctx context.Context, now time.Time) (*ReportDumpItem, error)
	DeleteReportDump(ctx context.Context, id string) error
	UpdateReportDumpFailure(ctx context.Context, id string, attempts int, nextAttemptAt, lastAttemptAt time.Time, lastError string) error
	PendingReportDumpCount(ctx context.Context) (int, error)
	CleanupExpired(ctx context.Context, cutoff time.Time) (CleanupResult, error)
}

type ChatAnalyzerStore interface {
	GetChatAnalyzerCheckpoint(ctx context.Context, chatID string) (lastMessageID int64, found bool, err error)
	SetChatAnalyzerCheckpoint(ctx context.Context, chatID string, lastMessageID int64, updatedAt time.Time) error
	EnqueueChatAnalyzerMessage(ctx context.Context, item model.ChatAnalyzerMessage) (inserted bool, err error)
	ListPendingChatAnalyzerMessages(ctx context.Context, limit int) ([]model.ChatAnalyzerMessage, error)
	MarkChatAnalyzerMessageProcessed(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, lastError string, processedAt time.Time) error
	MarkChatAnalyzerMessageProcessedInBatch(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, batchID, lastError string, processedAt time.Time) error
	SaveChatAnalyzerBatch(ctx context.Context, batch model.ChatAnalyzerBatch) error
	CountChatAnalyzerMessagesBySenderSince(ctx context.Context, chatID string, senderID int64, since time.Time) (int, error)
	CountChatAnalyzerAppliedByTargetSince(ctx context.Context, targetKey string, since time.Time) (int, error)
}

// ChatAnalyzerMessageExpiryStore is an optional optimized maintenance path.
// Stores without it are maintained through the portable list/mark operations
// already present in the production Spacetime module.
type ChatAnalyzerMessageExpiryStore interface {
	ExpireStaleChatAnalyzerMessages(ctx context.Context, messageDateBefore, processedAt time.Time, analysisJSON string) (int, error)
}

// ChatAnalyzerBatchRecoveryStore marks persisted running batches as failed
// after an interrupted process. A store that does not expose this operation
// must not persist a non-terminal batch record.
type ChatAnalyzerBatchRecoveryStore interface {
	FailStaleChatAnalyzerBatches(ctx context.Context, startedBefore, finishedAt time.Time, reason string) (int, error)
}

type ChatAnalyzerMessageOutcome struct {
	ID               string                          `json:"id"`
	Status           model.ChatAnalyzerMessageStatus `json:"status"`
	AnalysisJSON     string                          `json:"analysisJson"`
	AppliedActionID  string                          `json:"appliedActionId"`
	AppliedTargetKey string                          `json:"appliedTargetKey"`
	BatchID          string                          `json:"batchId"`
	LastError        string                          `json:"lastError"`
	ProcessedAt      time.Time                       `json:"processedAt"`
}

// ChatAnalyzerBatchFinalizer persists a terminal batch and all of its message
// outcomes in one transaction, so a completed batch can never leave pending
// messages behind.
type ChatAnalyzerBatchFinalizer interface {
	FinalizeChatAnalyzerBatch(ctx context.Context, batch model.ChatAnalyzerBatch, outcomes []ChatAnalyzerMessageOutcome) error
}
