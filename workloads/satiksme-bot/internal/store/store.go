package store

import (
	"context"
	"errors"
	"time"

	"satiksmebot/internal/model"
)

var ErrDuplicateReport = errors.New("duplicate report")

type CleanupResult struct {
	StopSightingsDeleted    int64
	VehicleSightingsDeleted int64
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
