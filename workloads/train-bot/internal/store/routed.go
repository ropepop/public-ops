package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"telegramtrainapp/internal/domain"
)

type RoutedStore struct {
	schedule Store
	state    Store
}

type activeBundlePublisher interface {
	PublishActiveBundle(ctx context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error
}

func NewRoutedStore(schedule Store, state Store) *RoutedStore {
	if schedule == nil {
		schedule = state
	}
	if state == nil {
		state = schedule
	}
	return &RoutedStore{
		schedule: schedule,
		state:    state,
	}
}

func (s *RoutedStore) Close() error {
	if s == nil {
		return nil
	}
	if s.schedule == nil && s.state == nil {
		return nil
	}
	if s.schedule == s.state {
		return s.schedule.Close()
	}
	return joinErrors(
		s.schedule.Close(),
		s.state.Close(),
	)
}

func (s *RoutedStore) Migrate(ctx context.Context) error {
	if s.schedule == nil || s.state == nil {
		return fmt.Errorf("routed store is not configured")
	}
	if s.schedule == s.state {
		return s.schedule.Migrate(ctx)
	}
	return joinErrors(
		s.schedule.Migrate(ctx),
		s.state.Migrate(ctx),
	)
}

func (s *RoutedStore) UpsertTrainInstances(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance) error {
	if s.schedule == s.state {
		return s.schedule.UpsertTrainInstances(ctx, serviceDate, sourceVersion, trains)
	}
	return joinErrors(
		s.schedule.UpsertTrainInstances(ctx, serviceDate, sourceVersion, trains),
		s.state.UpsertTrainInstances(ctx, serviceDate, sourceVersion, trains),
	)
}

func (s *RoutedStore) UpsertTrainStops(ctx context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error {
	if s.schedule == s.state {
		return s.schedule.UpsertTrainStops(ctx, serviceDate, stopsByTrain)
	}
	return joinErrors(
		s.schedule.UpsertTrainStops(ctx, serviceDate, stopsByTrain),
		s.state.UpsertTrainStops(ctx, serviceDate, stopsByTrain),
	)
}

func (s *RoutedStore) ListTrainInstancesByDate(ctx context.Context, serviceDate string) ([]domain.TrainInstance, error) {
	return s.schedule.ListTrainInstancesByDate(ctx, serviceDate)
}

func (s *RoutedStore) ListTrainInstancesByWindow(ctx context.Context, serviceDate string, start, end time.Time) ([]domain.TrainInstance, error) {
	return s.schedule.ListTrainInstancesByWindow(ctx, serviceDate, start, end)
}

func (s *RoutedStore) ListStationWindowTrains(ctx context.Context, serviceDate string, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error) {
	return s.schedule.ListStationWindowTrains(ctx, serviceDate, stationID, start, end)
}

func (s *RoutedStore) ListRouteWindowTrains(ctx context.Context, serviceDate string, fromStationID string, toStationID string, start, end time.Time) ([]domain.RouteWindowTrain, error) {
	return s.schedule.ListRouteWindowTrains(ctx, serviceDate, fromStationID, toStationID, start, end)
}

func (s *RoutedStore) ListStationsByDate(ctx context.Context, serviceDate string) ([]domain.Station, error) {
	return s.schedule.ListStationsByDate(ctx, serviceDate)
}

func (s *RoutedStore) ListReachableDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error) {
	return s.schedule.ListReachableDestinations(ctx, serviceDate, fromStationID)
}

func (s *RoutedStore) ListTerminalDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error) {
	return s.schedule.ListTerminalDestinations(ctx, serviceDate, fromStationID)
}

func (s *RoutedStore) GetStationByID(ctx context.Context, stationID string) (*domain.Station, error) {
	return s.schedule.GetStationByID(ctx, stationID)
}

func (s *RoutedStore) ListTrainStops(ctx context.Context, trainID string) ([]domain.TrainStop, error) {
	return s.schedule.ListTrainStops(ctx, trainID)
}

func (s *RoutedStore) TrainHasStops(ctx context.Context, trainID string) (bool, error) {
	return s.schedule.TrainHasStops(ctx, trainID)
}

func (s *RoutedStore) GetTrainInstanceByID(ctx context.Context, id string) (*domain.TrainInstance, error) {
	return s.schedule.GetTrainInstanceByID(ctx, id)
}

func (s *RoutedStore) EnsureUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	return s.state.EnsureUserSettings(ctx, userID)
}

func (s *RoutedStore) GetUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	return s.state.GetUserSettings(ctx, userID)
}

func (s *RoutedStore) HasUserSettings(ctx context.Context, userID int64) (bool, error) {
	return s.state.HasUserSettings(ctx, userID)
}

func (s *RoutedStore) SetAlertsEnabled(ctx context.Context, userID int64, enabled bool) error {
	return s.state.SetAlertsEnabled(ctx, userID, enabled)
}

func (s *RoutedStore) SetAlertStyle(ctx context.Context, userID int64, style domain.AlertStyle) error {
	return s.state.SetAlertStyle(ctx, userID, style)
}

func (s *RoutedStore) ToggleAlertStyle(ctx context.Context, userID int64) (domain.AlertStyle, error) {
	return s.state.ToggleAlertStyle(ctx, userID)
}

func (s *RoutedStore) SetLanguage(ctx context.Context, userID int64, lang domain.Language) error {
	return s.state.SetLanguage(ctx, userID, lang)
}

func (s *RoutedStore) ResetTestUser(ctx context.Context, userID int64) error {
	return s.state.ResetTestUser(ctx, userID)
}

func (s *RoutedStore) ConsumeTestLoginTicket(ctx context.Context, nonceHash string, userID int64, expiresAt time.Time) (bool, error) {
	return s.state.ConsumeTestLoginTicket(ctx, nonceHash, userID, expiresAt)
}

func (s *RoutedStore) CheckInUser(ctx context.Context, userID int64, trainID string, checkedInAt, autoCheckoutAt time.Time) error {
	return s.state.CheckInUser(ctx, userID, trainID, checkedInAt, autoCheckoutAt)
}

func (s *RoutedStore) CheckInUserAtStation(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error {
	return s.state.CheckInUserAtStation(ctx, userID, trainID, boardingStationID, checkedInAt, autoCheckoutAt)
}

func (s *RoutedStore) GetActiveCheckIn(ctx context.Context, userID int64, now time.Time) (*domain.CheckIn, error) {
	return s.state.GetActiveCheckIn(ctx, userID, now)
}

func (s *RoutedStore) CheckoutUser(ctx context.Context, userID int64) error {
	return s.state.CheckoutUser(ctx, userID)
}

func (s *RoutedStore) UndoCheckoutUser(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error {
	return s.state.UndoCheckoutUser(ctx, userID, trainID, boardingStationID, checkedInAt, autoCheckoutAt)
}

func (s *RoutedStore) SetTrainMute(ctx context.Context, userID int64, trainID string, until time.Time) error {
	return s.state.SetTrainMute(ctx, userID, trainID, until)
}

func (s *RoutedStore) IsTrainMuted(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error) {
	return s.state.IsTrainMuted(ctx, userID, trainID, now)
}

func (s *RoutedStore) CountActiveCheckins(ctx context.Context, trainID string, now time.Time) (int, error) {
	return s.state.CountActiveCheckins(ctx, trainID, now)
}

func (s *RoutedStore) ListActiveCheckinUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	return s.state.ListActiveCheckinUsers(ctx, trainID, now)
}

func (s *RoutedStore) UpsertSubscription(ctx context.Context, userID int64, trainID string, expiresAt time.Time) error {
	return s.state.UpsertSubscription(ctx, userID, trainID, expiresAt)
}

func (s *RoutedStore) DeactivateSubscription(ctx context.Context, userID int64, trainID string) error {
	return s.state.DeactivateSubscription(ctx, userID, trainID)
}

func (s *RoutedStore) HasActiveSubscription(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error) {
	return s.state.HasActiveSubscription(ctx, userID, trainID, now)
}

func (s *RoutedStore) ListActiveSubscriptionUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	return s.state.ListActiveSubscriptionUsers(ctx, trainID, now)
}

func (s *RoutedStore) UpsertFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	return s.state.UpsertFavoriteRoute(ctx, userID, fromStationID, toStationID)
}

func (s *RoutedStore) DeleteFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	return s.state.DeleteFavoriteRoute(ctx, userID, fromStationID, toStationID)
}

func (s *RoutedStore) ListFavoriteRoutes(ctx context.Context, userID int64) ([]domain.FavoriteRoute, error) {
	return s.state.ListFavoriteRoutes(ctx, userID)
}

func (s *RoutedStore) ListAllFavoriteRoutes(ctx context.Context) ([]domain.FavoriteRoute, error) {
	return s.state.ListAllFavoriteRoutes(ctx)
}

func (s *RoutedStore) InsertReportEvent(ctx context.Context, e domain.ReportEvent) error {
	return s.state.InsertReportEvent(ctx, e)
}

func (s *RoutedStore) GetLastReportByUserTrain(ctx context.Context, userID int64, trainID string) (*domain.ReportEvent, error) {
	return s.state.GetLastReportByUserTrain(ctx, userID, trainID)
}

func (s *RoutedStore) ListReportsSince(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.ReportEvent, error) {
	return s.state.ListReportsSince(ctx, trainID, since, limit)
}

func (s *RoutedStore) ListRecentReports(ctx context.Context, trainID string, limit int) ([]domain.ReportEvent, error) {
	return s.state.ListRecentReports(ctx, trainID, limit)
}

func (s *RoutedStore) ListRecentReportEvents(ctx context.Context, since time.Time, limit int) ([]domain.ReportEvent, error) {
	return s.state.ListRecentReportEvents(ctx, since, limit)
}

func (s *RoutedStore) InsertStationSighting(ctx context.Context, e domain.StationSighting) error {
	return s.state.InsertStationSighting(ctx, e)
}

func (s *RoutedStore) GetLastStationSightingByUserScope(ctx context.Context, userID int64, stationID string, destinationStationID *string) (*domain.StationSighting, error) {
	return s.state.GetLastStationSightingByUserScope(ctx, userID, stationID, destinationStationID)
}

func (s *RoutedStore) ListRecentStationSightings(ctx context.Context, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.state.ListRecentStationSightings(ctx, since, limit)
}

func (s *RoutedStore) ListRecentStationSightingsByStation(ctx context.Context, stationID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.state.ListRecentStationSightingsByStation(ctx, stationID, since, limit)
}

func (s *RoutedStore) ListRecentStationSightingsByTrain(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.state.ListRecentStationSightingsByTrain(ctx, trainID, since, limit)
}

func (s *RoutedStore) UpsertIncidentVote(ctx context.Context, vote domain.IncidentVote) error {
	return s.state.UpsertIncidentVote(ctx, vote)
}

func (s *RoutedStore) InsertIncidentVoteEvent(ctx context.Context, vote domain.IncidentVoteEvent) error {
	return s.state.InsertIncidentVoteEvent(ctx, vote)
}

func (s *RoutedStore) ListIncidentVotes(ctx context.Context, incidentID string) ([]domain.IncidentVote, error) {
	return s.state.ListIncidentVotes(ctx, incidentID)
}

func (s *RoutedStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]domain.IncidentVoteEvent, error) {
	return s.state.ListIncidentVoteEvents(ctx, incidentID, since, limit)
}

func (s *RoutedStore) InsertIncidentComment(ctx context.Context, comment domain.IncidentComment) error {
	return s.state.InsertIncidentComment(ctx, comment)
}

func (s *RoutedStore) ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]domain.IncidentComment, error) {
	return s.state.ListIncidentComments(ctx, incidentID, limit)
}

func (s *RoutedStore) CleanupExpired(ctx context.Context, now time.Time, retention time.Duration, loc *time.Location) (CleanupResult, error) {
	if s.schedule == s.state {
		return s.schedule.CleanupExpired(ctx, now, retention, loc)
	}
	scheduleRes, scheduleErr := s.schedule.CleanupExpired(ctx, now, retention, loc)
	stateRes, stateErr := s.state.CleanupExpired(ctx, now, retention, loc)
	if scheduleErr != nil && !errors.Is(scheduleErr, ErrCleanupUnsupported) {
		return CleanupResult{}, scheduleErr
	}
	if stateErr != nil && !errors.Is(stateErr, ErrCleanupUnsupported) {
		return CleanupResult{}, stateErr
	}
	if scheduleErr != nil && stateErr != nil {
		return CleanupResult{}, ErrCleanupUnsupported
	}
	return CleanupResult{
		CheckinsDeleted:         scheduleRes.CheckinsDeleted + stateRes.CheckinsDeleted,
		SubscriptionsDeleted:    scheduleRes.SubscriptionsDeleted + stateRes.SubscriptionsDeleted,
		ReportsDeleted:          scheduleRes.ReportsDeleted + stateRes.ReportsDeleted,
		StationSightingsDeleted: scheduleRes.StationSightingsDeleted + stateRes.StationSightingsDeleted,
		TrainStopsDeleted:       scheduleRes.TrainStopsDeleted + stateRes.TrainStopsDeleted,
		TrainsDeleted:           scheduleRes.TrainsDeleted + stateRes.TrainsDeleted,
		FeedEventsDeleted:       scheduleRes.FeedEventsDeleted + stateRes.FeedEventsDeleted,
		FeedImportsDeleted:      scheduleRes.FeedImportsDeleted + stateRes.FeedImportsDeleted,
		ImportChunksDeleted:     scheduleRes.ImportChunksDeleted + stateRes.ImportChunksDeleted,
	}, nil
}

func (s *RoutedStore) DeleteTrainDataByServiceDate(ctx context.Context, serviceDate string) (CleanupResult, error) {
	if s.schedule == s.state {
		return s.schedule.DeleteTrainDataByServiceDate(ctx, serviceDate)
	}
	scheduleRes, scheduleErr := s.schedule.DeleteTrainDataByServiceDate(ctx, serviceDate)
	stateRes, stateErr := s.state.DeleteTrainDataByServiceDate(ctx, serviceDate)
	if err := joinErrors(scheduleErr, stateErr); err != nil {
		return CleanupResult{}, err
	}
	return CleanupResult{
		CheckinsDeleted:         scheduleRes.CheckinsDeleted + stateRes.CheckinsDeleted,
		SubscriptionsDeleted:    scheduleRes.SubscriptionsDeleted + stateRes.SubscriptionsDeleted,
		ReportsDeleted:          scheduleRes.ReportsDeleted + stateRes.ReportsDeleted,
		StationSightingsDeleted: scheduleRes.StationSightingsDeleted + stateRes.StationSightingsDeleted,
		TrainStopsDeleted:       scheduleRes.TrainStopsDeleted + stateRes.TrainStopsDeleted,
		TrainsDeleted:           scheduleRes.TrainsDeleted + stateRes.TrainsDeleted,
		FeedEventsDeleted:       scheduleRes.FeedEventsDeleted + stateRes.FeedEventsDeleted,
		FeedImportsDeleted:      scheduleRes.FeedImportsDeleted + stateRes.FeedImportsDeleted,
		ImportChunksDeleted:     scheduleRes.ImportChunksDeleted + stateRes.ImportChunksDeleted,
	}, nil
}

func (s *RoutedStore) UpsertDailyMetric(ctx context.Context, metricDate string, key string, value int64) error {
	return s.state.UpsertDailyMetric(ctx, metricDate, key, value)
}

func (s *RoutedStore) PublishActiveBundle(ctx context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error {
	if publisher, ok := s.state.(activeBundlePublisher); ok {
		return publisher.PublishActiveBundle(ctx, version, serviceDate, generatedAt, sourceVersion)
	}
	return nil
}

func joinErrors(errs ...error) error {
	var combined error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if combined == nil {
			combined = err
			continue
		}
		combined = errors.Join(combined, err)
	}
	return combined
}
