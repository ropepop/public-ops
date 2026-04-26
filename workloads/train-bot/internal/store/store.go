package store

import (
	"context"
	"errors"
	"time"

	"telegramtrainapp/internal/domain"
)

var ErrCleanupUnsupported = errors.New("cleanup unsupported")

type CleanupResult struct {
	CheckinsDeleted         int64
	SubscriptionsDeleted    int64
	ReportsDeleted          int64
	StationSightingsDeleted int64
	TrainStopsDeleted       int64
	TrainsDeleted           int64
	FeedEventsDeleted       int64
	FeedImportsDeleted      int64
	ImportChunksDeleted     int64
}

type Store interface {
	Close() error
	Migrate(ctx context.Context) error
	UpsertTrainInstances(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance) error
	UpsertTrainStops(ctx context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error
	ListTrainInstancesByDate(ctx context.Context, serviceDate string) ([]domain.TrainInstance, error)
	ListTrainInstancesByWindow(ctx context.Context, serviceDate string, start, end time.Time) ([]domain.TrainInstance, error)
	ListStationWindowTrains(ctx context.Context, serviceDate string, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error)
	ListRouteWindowTrains(ctx context.Context, serviceDate string, fromStationID string, toStationID string, start, end time.Time) ([]domain.RouteWindowTrain, error)
	ListStationsByDate(ctx context.Context, serviceDate string) ([]domain.Station, error)
	ListReachableDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error)
	ListTerminalDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error)
	GetStationByID(ctx context.Context, stationID string) (*domain.Station, error)
	ListTrainStops(ctx context.Context, trainID string) ([]domain.TrainStop, error)
	TrainHasStops(ctx context.Context, trainID string) (bool, error)
	GetTrainInstanceByID(ctx context.Context, id string) (*domain.TrainInstance, error)

	EnsureUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error)
	GetUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error)
	HasUserSettings(ctx context.Context, userID int64) (bool, error)
	SetAlertsEnabled(ctx context.Context, userID int64, enabled bool) error
	SetAlertStyle(ctx context.Context, userID int64, style domain.AlertStyle) error
	ToggleAlertStyle(ctx context.Context, userID int64) (domain.AlertStyle, error)
	SetLanguage(ctx context.Context, userID int64, lang domain.Language) error
	ResetTestUser(ctx context.Context, userID int64) error
	ConsumeTestLoginTicket(ctx context.Context, nonceHash string, userID int64, expiresAt time.Time) (bool, error)

	CheckInUser(ctx context.Context, userID int64, trainID string, checkedInAt, autoCheckoutAt time.Time) error
	CheckInUserAtStation(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error
	GetActiveCheckIn(ctx context.Context, userID int64, now time.Time) (*domain.CheckIn, error)
	CheckoutUser(ctx context.Context, userID int64) error
	UndoCheckoutUser(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error
	SetTrainMute(ctx context.Context, userID int64, trainID string, until time.Time) error
	IsTrainMuted(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error)
	CountActiveCheckins(ctx context.Context, trainID string, now time.Time) (int, error)
	ListActiveCheckinUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error)

	UpsertSubscription(ctx context.Context, userID int64, trainID string, expiresAt time.Time) error
	DeactivateSubscription(ctx context.Context, userID int64, trainID string) error
	HasActiveSubscription(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error)
	ListActiveSubscriptionUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error)

	UpsertFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error
	DeleteFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error
	ListFavoriteRoutes(ctx context.Context, userID int64) ([]domain.FavoriteRoute, error)
	ListAllFavoriteRoutes(ctx context.Context) ([]domain.FavoriteRoute, error)

	InsertReportEvent(ctx context.Context, e domain.ReportEvent) error
	GetLastReportByUserTrain(ctx context.Context, userID int64, trainID string) (*domain.ReportEvent, error)
	ListReportsSince(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.ReportEvent, error)
	ListRecentReports(ctx context.Context, trainID string, limit int) ([]domain.ReportEvent, error)
	ListRecentReportEvents(ctx context.Context, since time.Time, limit int) ([]domain.ReportEvent, error)
	InsertStationSighting(ctx context.Context, e domain.StationSighting) error
	GetLastStationSightingByUserScope(ctx context.Context, userID int64, stationID string, destinationStationID *string) (*domain.StationSighting, error)
	ListRecentStationSightings(ctx context.Context, since time.Time, limit int) ([]domain.StationSighting, error)
	ListRecentStationSightingsByStation(ctx context.Context, stationID string, since time.Time, limit int) ([]domain.StationSighting, error)
	ListRecentStationSightingsByTrain(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.StationSighting, error)
	UpsertIncidentVote(ctx context.Context, vote domain.IncidentVote) error
	InsertIncidentVoteEvent(ctx context.Context, vote domain.IncidentVoteEvent) error
	ListIncidentVotes(ctx context.Context, incidentID string) ([]domain.IncidentVote, error)
	ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]domain.IncidentVoteEvent, error)
	InsertIncidentComment(ctx context.Context, comment domain.IncidentComment) error
	ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]domain.IncidentComment, error)

	CleanupExpired(ctx context.Context, now time.Time, retention time.Duration, loc *time.Location) (CleanupResult, error)
	DeleteTrainDataByServiceDate(ctx context.Context, serviceDate string) (CleanupResult, error)
	UpsertDailyMetric(ctx context.Context, metricDate string, key string, value int64) error
}
