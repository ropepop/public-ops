package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/stationsearch"
	"telegramtrainapp/internal/store"
)

type Service struct {
	store                 store.Store
	schedules             schedule.ReadModel
	rides                 *ride.Service
	reports               *reports.Service
	loc                   *time.Location
	stationCheckinEnabled bool
}

const (
	authStationDeparturePastWindow   = 2 * time.Hour
	authStationDepartureFutureWindow = 2 * time.Hour
	stationSightingContextWindow     = 30 * time.Minute
	networkMapRelevantSightingWindow = 30 * time.Minute
	mapCheckInFallbackWindow         = 6 * time.Hour
)

type TrainCard struct {
	Train  domain.TrainInstance `json:"train"`
	Status domain.TrainStatus   `json:"status"`
	Riders int                  `json:"riders"`
}

type StationTrainCard struct {
	TrainCard       TrainCard                `json:"trainCard"`
	StationID       string                   `json:"stationId"`
	StationName     string                   `json:"stationName"`
	PassAt          time.Time                `json:"passAt"`
	SightingCount   int                      `json:"sightingCount"`
	SightingContext []domain.StationSighting `json:"sightingContext"`
}

type RouteTrainCard struct {
	TrainCard       TrainCard `json:"trainCard"`
	FromStationID   string    `json:"fromStationId"`
	FromStationName string    `json:"fromStationName"`
	ToStationID     string    `json:"toStationId"`
	ToStationName   string    `json:"toStationName"`
	FromPassAt      time.Time `json:"fromPassAt"`
	ToPassAt        time.Time `json:"toPassAt"`
}

type TrainStatusView struct {
	TrainCard        TrainCard                `json:"trainCard"`
	Timeline         []reports.TimelineEvent  `json:"timeline"`
	StationSightings []domain.StationSighting `json:"stationSightings"`
}

type CurrentRideView struct {
	CheckIn             *domain.CheckIn  `json:"checkIn,omitempty"`
	Train               *TrainStatusView `json:"train,omitempty"`
	BoardingStationID   string           `json:"boardingStationId,omitempty"`
	BoardingStationName string           `json:"boardingStationName,omitempty"`
}

type FavoriteRouteView struct {
	FromStationID   string `json:"fromStationId"`
	FromStationName string `json:"fromStationName"`
	ToStationID     string `json:"toStationId"`
	ToStationName   string `json:"toStationName"`
}

type PublicTrainView struct {
	Train            domain.TrainInstance     `json:"train"`
	Status           domain.TrainStatus       `json:"status"`
	Riders           int                      `json:"riders"`
	Timeline         []reports.TimelineEvent  `json:"timeline"`
	StationSightings []domain.StationSighting `json:"stationSightings"`
}

type PublicStationDeparturesView struct {
	Station         domain.Station           `json:"station"`
	LastDeparture   *StationTrainCard        `json:"lastDeparture,omitempty"`
	Upcoming        []StationTrainCard       `json:"upcoming"`
	RecentSightings []domain.StationSighting `json:"recentSightings"`
}

type StationDeparturesView struct {
	Station         domain.Station           `json:"station"`
	Trains          []StationTrainCard       `json:"trains"`
	RecentSightings []domain.StationSighting `json:"recentSightings"`
}

type TrainStopsView struct {
	TrainCard        TrainCard                `json:"trainCard"`
	Train            domain.TrainInstance     `json:"train"`
	Stops            []domain.TrainStop       `json:"stops"`
	StationSightings []domain.StationSighting `json:"stationSightings"`
}

type NetworkMapView struct {
	Stations         []domain.Station         `json:"stations"`
	RecentSightings  []domain.StationSighting `json:"recentSightings"`
	SameDaySightings []domain.StationSighting `json:"sameDaySightings"`
}

type StaticBundleBase struct {
	ServiceDate   string                 `json:"serviceDate"`
	SourceVersion string                 `json:"sourceVersion"`
	Stations      []domain.Station       `json:"stations"`
	Trains        []domain.TrainInstance `json:"trains"`
	Stops         []domain.TrainStop     `json:"stops"`
}

func NewService(
	st store.Store,
	schedules schedule.ReadModel,
	rides *ride.Service,
	reportsSvc *reports.Service,
	loc *time.Location,
	stationCheckinEnabled bool,
) *Service {
	return &Service{
		store:                 st,
		schedules:             schedules,
		rides:                 rides,
		reports:               reportsSvc,
		loc:                   loc,
		stationCheckinEnabled: stationCheckinEnabled,
	}
}

func (s *Service) StationCheckinEnabled() bool {
	return s.stationCheckinEnabled
}

func (s *Service) ScheduleAvailability() (bool, error) {
	if s == nil || s.schedules == nil {
		return false, schedule.ErrUnavailable
	}
	return s.schedules.Availability()
}

func (s *Service) LoadedServiceDate() string {
	if s == nil || s.schedules == nil {
		return ""
	}
	return s.schedules.LoadedServiceDate()
}

func (s *Service) ScheduleContext(now time.Time) schedule.AccessContext {
	if s == nil || s.schedules == nil {
		return schedule.AccessContext{Available: false}
	}
	if s.loc == nil {
		return s.schedules.AccessContext(now)
	}
	return s.schedules.AccessContext(now.In(s.loc))
}

func (s *Service) LanguageFor(ctx context.Context, userID int64) domain.Language {
	settings, err := s.store.EnsureUserSettings(ctx, userID)
	if err != nil {
		return domain.LanguageEN
	}
	return settings.Language
}

func (s *Service) UserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	return s.store.GetUserSettings(ctx, userID)
}

func (s *Service) ResolveSignedInLanguage(ctx context.Context, userID int64, preferred string) (domain.Language, error) {
	lang := ParseLanguage(preferred)
	if s.store == nil {
		return lang, nil
	}
	hasSettings, err := s.store.HasUserSettings(ctx, userID)
	if err != nil {
		return domain.LanguageEN, err
	}
	if !hasSettings {
		if err := s.store.SetLanguage(ctx, userID, lang); err != nil {
			return domain.LanguageEN, err
		}
		return lang, nil
	}
	settings, err := s.store.GetUserSettings(ctx, userID)
	if err != nil {
		return domain.LanguageEN, err
	}
	return settings.Language, nil
}

func (s *Service) SetAlertsEnabled(ctx context.Context, userID int64, enabled bool) error {
	return s.store.SetAlertsEnabled(ctx, userID, enabled)
}

func (s *Service) SetLanguage(ctx context.Context, userID int64, lang domain.Language) error {
	return s.store.SetLanguage(ctx, userID, lang)
}

func (s *Service) ResetTestUser(ctx context.Context, userID int64) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.ResetTestUser(ctx, userID)
}

func (s *Service) ConsumeTestLoginTicket(ctx context.Context, nonceHash string, userID int64, expiresAt time.Time) (bool, error) {
	if s == nil || s.store == nil {
		return true, nil
	}
	return s.store.ConsumeTestLoginTicket(ctx, nonceHash, userID, expiresAt)
}

func (s *Service) SetAlertStyle(ctx context.Context, userID int64, style domain.AlertStyle) error {
	return s.store.SetAlertStyle(ctx, userID, style)
}

func (s *Service) Nickname(userID int64) string {
	return domain.GenericNickname(userID)
}

func (s *Service) WindowTrains(ctx context.Context, userID int64, now time.Time, windowID string) ([]TrainCard, error) {
	trains, err := s.schedules.ListByWindow(ctx, now.In(s.loc), windowID)
	if err != nil {
		return nil, err
	}
	return s.trainCards(ctx, userID, now, trains)
}

func (s *Service) Stations(ctx context.Context, now time.Time, query string) ([]domain.Station, error) {
	stations, err := s.schedules.ListStations(ctx, now.In(s.loc))
	if err != nil {
		return nil, err
	}
	return filterStations(stations, query), nil
}

func (s *Service) StationTrains(ctx context.Context, userID int64, now time.Time, stationID string, d time.Duration) ([]StationTrainCard, error) {
	trains, err := s.schedules.ListByStationWindow(ctx, now.In(s.loc), stationID, d)
	if err != nil {
		return nil, err
	}
	return s.stationTrainCards(ctx, userID, trains, now, nil)
}

func (s *Service) ReachableDestinations(ctx context.Context, now time.Time, fromStationID string, query string) ([]domain.Station, error) {
	destinations, err := s.schedules.ListReachableDestinations(ctx, now.In(s.loc), fromStationID)
	if err != nil {
		return nil, err
	}
	return filterStations(destinations, query), nil
}

func (s *Service) StationSightingDestinations(ctx context.Context, now time.Time, fromStationID string) ([]domain.Station, error) {
	localNow := now.In(s.loc)
	station, err := s.schedules.GetStation(ctx, localNow, fromStationID)
	if err != nil {
		return nil, err
	}
	if station == nil {
		return nil, ErrNotFound
	}
	return s.schedules.ListTerminalDestinations(ctx, localNow, fromStationID)
}

func (s *Service) RouteTrains(ctx context.Context, userID int64, now time.Time, fromStationID string, toStationID string, d time.Duration) ([]RouteTrainCard, error) {
	trains, err := s.schedules.ListRouteWindowTrains(ctx, now.In(s.loc), fromStationID, toStationID, d)
	if err != nil {
		return nil, err
	}
	out := make([]RouteTrainCard, 0, len(trains))
	for _, item := range trains {
		card, err := s.trainCard(ctx, userID, item.Train, now)
		if err != nil {
			return nil, err
		}
		out = append(out, RouteTrainCard{
			TrainCard:       card,
			FromStationID:   item.FromStationID,
			FromStationName: item.FromStationName,
			ToStationID:     item.ToStationID,
			ToStationName:   item.ToStationName,
			FromPassAt:      item.FromPassAt.In(s.loc),
			ToPassAt:        item.ToPassAt.In(s.loc),
		})
	}
	return out, nil
}

func (s *Service) FavoriteRoutes(ctx context.Context, userID int64) ([]FavoriteRouteView, error) {
	items, err := s.store.ListFavoriteRoutes(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]FavoriteRouteView, 0, len(items))
	for _, item := range items {
		out = append(out, FavoriteRouteView{
			FromStationID:   item.FromStationID,
			FromStationName: item.FromStationName,
			ToStationID:     item.ToStationID,
			ToStationName:   item.ToStationName,
		})
	}
	return out, nil
}

func (s *Service) SaveFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	return s.store.UpsertFavoriteRoute(ctx, userID, fromStationID, toStationID)
}

func (s *Service) DeleteFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	return s.store.DeleteFavoriteRoute(ctx, userID, fromStationID, toStationID)
}

func (s *Service) CurrentRide(ctx context.Context, userID int64, now time.Time) (*CurrentRideView, error) {
	active, err := s.rides.ActiveCheckIn(ctx, userID, now.In(s.loc))
	if err != nil {
		return nil, err
	}
	if active == nil {
		return nil, nil
	}
	statusView, err := s.TrainStatus(ctx, userID, active.TrainInstanceID, now)
	if err != nil {
		return nil, err
	}
	view := &CurrentRideView{
		CheckIn: active,
		Train:   statusView,
	}
	if active.BoardingStationID != nil {
		view.BoardingStationID = strings.TrimSpace(*active.BoardingStationID)
		if view.BoardingStationID != "" {
			station, err := s.store.GetStationByID(ctx, view.BoardingStationID)
			if err != nil {
				return nil, err
			}
			if station != nil {
				view.BoardingStationName = station.Name
			}
		}
	}
	return view, nil
}

func (s *Service) CheckIn(ctx context.Context, userID int64, trainID string, boardingStationID *string, now time.Time) error {
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return err
	}
	if train == nil {
		return ErrNotFound
	}
	localNow := now.In(s.loc)
	if boardingStationID != nil {
		selected, err := s.selectedStationDeparture(ctx, localNow, strings.TrimSpace(*boardingStationID), trainID)
		if err != nil {
			return err
		}
		if !localNow.Before(selected.PassAt.In(s.loc).Add(10 * time.Minute)) {
			return ErrCheckInUnavailable
		}
		if !localNow.Before(train.ArrivalAt.In(s.loc).Add(10 * time.Minute)) {
			return ErrCheckInUnavailable
		}
		return s.rides.CheckInAtStation(ctx, userID, trainID, boardingStationID, localNow, train.ArrivalAt.In(s.loc))
	}
	if !localNow.Before(train.ArrivalAt.In(s.loc).Add(10 * time.Minute)) {
		return ErrCheckInUnavailable
	}
	return s.rides.CheckIn(ctx, userID, trainID, localNow, train.ArrivalAt.In(s.loc))
}

func (s *Service) CheckInMap(ctx context.Context, userID int64, trainID string, boardingStationID *string, now time.Time) error {
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return err
	}
	if train == nil {
		return ErrNotFound
	}
	localNow := now.In(s.loc)
	autoCheckoutAt := train.ArrivalAt.In(s.loc).Add(10 * time.Minute)
	fallbackCheckoutAt := localNow.Add(mapCheckInFallbackWindow)
	if autoCheckoutAt.Before(fallbackCheckoutAt) {
		autoCheckoutAt = fallbackCheckoutAt
	}
	if boardingStationID != nil {
		return s.rides.CheckInUntilAtStation(ctx, userID, trainID, boardingStationID, localNow, autoCheckoutAt)
	}
	return s.rides.CheckInUntil(ctx, userID, trainID, localNow, autoCheckoutAt)
}

func (s *Service) Checkout(ctx context.Context, userID int64, now time.Time) error {
	return s.rides.Checkout(ctx, userID, now.In(s.loc))
}

func (s *Service) UndoCheckout(ctx context.Context, userID int64, now time.Time) (bool, error) {
	return s.rides.UndoCheckout(ctx, userID, now.In(s.loc))
}

func (s *Service) MuteTrain(ctx context.Context, userID int64, trainID string, now time.Time, d time.Duration) error {
	return s.rides.MuteForTrain(ctx, userID, trainID, now.In(s.loc), d)
}

func (s *Service) SubmitReport(ctx context.Context, userID int64, trainID string, signal domain.SignalType, now time.Time) (reports.SubmitResult, error) {
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return reports.SubmitResult{}, err
	}
	if train == nil {
		return reports.SubmitResult{}, ErrNotFound
	}
	return s.reports.SubmitReport(ctx, userID, trainID, signal, now.In(s.loc))
}

func (s *Service) SubmitStationSighting(ctx context.Context, userID int64, stationID string, destinationStationID *string, trainID *string, now time.Time) (reports.StationSightingSubmitResult, error) {
	localNow := now.In(s.loc)
	station, err := s.schedules.GetStation(ctx, localNow, stationID)
	if err != nil {
		return reports.StationSightingSubmitResult{}, err
	}
	if station == nil {
		return reports.StationSightingSubmitResult{}, ErrNotFound
	}

	cleanDestination := trimStringPtr(destinationStationID)
	cleanTrainID := trimStringPtr(trainID)
	var matchedTrainID *string
	var destinationName string
	if cleanTrainID != nil {
		selected, err := s.selectedStationDeparture(ctx, localNow, stationID, *cleanTrainID)
		if err != nil {
			return reports.StationSightingSubmitResult{}, err
		}
		matchedTrainID = cleanTrainID
		terminalStop, err := s.trainTerminalStop(ctx, selected.Train.ID)
		if err != nil {
			return reports.StationSightingSubmitResult{}, err
		}
		if terminalStop != nil {
			cleanDestination = &terminalStop.StationID
			destinationName = terminalStop.StationName
		} else {
			destinationName = selected.Train.ToStation
		}
	} else if cleanDestination != nil {
		destinations, err := s.schedules.ListTerminalDestinations(ctx, localNow, stationID)
		if err != nil {
			return reports.StationSightingSubmitResult{}, err
		}
		found := false
		for _, destination := range destinations {
			if destination.ID != *cleanDestination {
				continue
			}
			destinationName = destination.Name
			found = true
			break
		}
		if !found {
			return reports.StationSightingSubmitResult{}, ErrNotFound
		}
	}

	if matchedTrainID == nil {
		matchedTrainID, err = s.matchStationSightingTrainID(ctx, localNow, stationID, cleanDestination)
		if err != nil {
			return reports.StationSightingSubmitResult{}, err
		}
	}
	result, err := s.reports.SubmitStationSighting(ctx, userID, stationID, cleanDestination, matchedTrainID, localNow)
	if err != nil {
		return reports.StationSightingSubmitResult{}, err
	}
	if result.Event != nil {
		result.Event.StationName = station.Name
		result.Event.DestinationStationName = destinationName
	}
	return result, nil
}

func (s *Service) TrainStatus(ctx context.Context, userID int64, trainID string, now time.Time) (*TrainStatusView, error) {
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return nil, err
	}
	if train == nil {
		return nil, ErrNotFound
	}
	card, err := s.trainCard(ctx, userID, *train, now)
	if err != nil {
		return nil, err
	}
	timeline, err := s.reports.RecentTimeline(ctx, trainID, 5)
	if err != nil {
		return nil, err
	}
	stationSightings, err := s.reports.RecentStationSightingsByTrain(ctx, trainID, now.In(s.loc), 5)
	if err != nil {
		return nil, err
	}
	return &TrainStatusView{TrainCard: card, Timeline: timeline, StationSightings: stationSightings}, nil
}

func (s *Service) OngoingIncidents(ctx context.Context, now time.Time, viewerID int64, limit int) ([]domain.IncidentSummary, error) {
	return s.reports.ListActiveIncidents(ctx, now.In(s.loc), viewerID, limit)
}

func (s *Service) IncidentDetail(ctx context.Context, incidentID string, now time.Time, viewerID int64) (*domain.IncidentDetail, error) {
	item, err := s.reports.IncidentDetail(ctx, incidentID, now.In(s.loc), viewerID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return item, nil
}

func (s *Service) VoteIncident(ctx context.Context, incidentID string, userID int64, value domain.IncidentVoteValue, now time.Time) (domain.IncidentVoteSummary, error) {
	item, err := s.reports.VoteIncident(ctx, incidentID, userID, value, now.In(s.loc))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return domain.IncidentVoteSummary{}, ErrNotFound
	}
	return item, err
}

func (s *Service) AddIncidentComment(ctx context.Context, incidentID string, userID int64, body string, now time.Time) (*domain.IncidentComment, error) {
	item, err := s.reports.AddIncidentComment(ctx, incidentID, userID, body, now.In(s.loc))
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil, ErrNotFound
	}
	return item, err
}

func (s *Service) PublicDashboard(ctx context.Context, now time.Time, limit int) ([]PublicTrainView, error) {
	trains, err := s.schedules.ListByWindow(ctx, now.In(s.loc), "today")
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(trains) > limit {
		trains = trains[:limit]
	}
	return s.buildPublicTrainViews(ctx, trains, now)
}

func (s *Service) PublicServiceDay(ctx context.Context, now time.Time) ([]PublicTrainView, error) {
	trains, err := s.schedules.ListAllTrains(ctx, now.In(s.loc))
	if err != nil {
		return nil, err
	}
	return s.buildPublicTrainViews(ctx, trains, now)
}

func (s *Service) buildPublicTrainViews(ctx context.Context, trains []domain.TrainInstance, now time.Time) ([]PublicTrainView, error) {
	out := make([]PublicTrainView, 0, len(trains))
	for _, train := range trains {
		status, err := s.reports.BuildStatus(ctx, train.ID, now.In(s.loc))
		if err != nil {
			return nil, err
		}
		riders, err := s.store.CountActiveCheckins(ctx, train.ID, now.In(s.loc))
		if err != nil {
			return nil, err
		}
		timeline, err := s.reports.RecentTimeline(ctx, train.ID, 5)
		if err != nil {
			return nil, err
		}
		out = append(out, PublicTrainView{
			Train:            train,
			Status:           status,
			Riders:           riders,
			Timeline:         timeline,
			StationSightings: nil,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Train.DepartureAt.Before(out[j].Train.DepartureAt)
	})
	return out, nil
}

func (s *Service) PublicDashboardPayload(ctx context.Context, now time.Time, limit int) (map[string]any, error) {
	items, err := s.PublicDashboard(ctx, now, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"generatedAt": now.UTC(),
		"trains":      items,
		"schedule":    s.ScheduleContext(now),
	}, nil
}

func (s *Service) PublicServiceDayPayload(ctx context.Context, now time.Time) (map[string]any, error) {
	items, err := s.PublicServiceDay(ctx, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"generatedAt": now.UTC(),
		"trains":      items,
		"schedule":    s.ScheduleContext(now),
	}, nil
}

func (s *Service) StaticBundleBase(ctx context.Context, now time.Time) (*StaticBundleBase, error) {
	localNow := now.In(s.loc)
	access := s.ScheduleContext(localNow)
	if !access.Available || strings.TrimSpace(access.EffectiveServiceDate) == "" {
		return nil, schedule.ErrUnavailable
	}
	stations, err := s.schedules.ListStations(ctx, localNow)
	if err != nil {
		return nil, err
	}
	trains, err := s.schedules.ListAllTrains(ctx, localNow)
	if err != nil {
		return nil, err
	}
	stops, err := s.schedules.ListAllStops(ctx, localNow)
	if err != nil {
		return nil, err
	}
	sourceVersions := make([]string, 0, len(trains))
	seenSourceVersions := make(map[string]struct{}, len(trains))
	for _, train := range trains {
		version := strings.TrimSpace(train.SourceVersion)
		if version == "" {
			continue
		}
		if _, ok := seenSourceVersions[version]; ok {
			continue
		}
		seenSourceVersions[version] = struct{}{}
		sourceVersions = append(sourceVersions, version)
	}
	sort.Strings(sourceVersions)
	sourceVersion := "snapshot-unknown"
	if len(sourceVersions) == 1 {
		sourceVersion = sourceVersions[0]
	} else if len(sourceVersions) > 1 {
		sourceVersion = strings.Join(sourceVersions, ",")
	}
	return &StaticBundleBase{
		ServiceDate:   access.EffectiveServiceDate,
		SourceVersion: sourceVersion,
		Stations:      stations,
		Trains:        trains,
		Stops:         stops,
	}, nil
}

func (s *Service) PublicTrain(ctx context.Context, trainID string, now time.Time) (*PublicTrainView, error) {
	view, err := s.TrainStatus(ctx, 0, trainID, now)
	if err != nil {
		return nil, err
	}
	return &PublicTrainView{
		Train:            view.TrainCard.Train,
		Status:           view.TrainCard.Status,
		Riders:           view.TrainCard.Riders,
		Timeline:         view.Timeline,
		StationSightings: view.StationSightings,
	}, nil
}

func (s *Service) PublicStationDepartures(ctx context.Context, now time.Time, stationID string, limit int) (*PublicStationDeparturesView, error) {
	station, err := s.schedules.GetStation(ctx, now.In(s.loc), stationID)
	if err != nil {
		return nil, err
	}
	if station == nil {
		return nil, ErrNotFound
	}

	localNow := now.In(s.loc)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, s.loc)
	dayEnd := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 23, 59, 59, int(time.Second-time.Nanosecond), s.loc)

	var lastDeparture *StationTrainCard
	if localNow.After(dayStart) {
		recent, err := s.schedules.ListByStationRange(ctx, localNow, stationID, dayStart, localNow.Add(-time.Nanosecond))
		if err != nil {
			return nil, err
		}
		if len(recent) > 0 {
			cards, err := s.stationTrainCards(ctx, 0, recent[len(recent)-1:], now, nil)
			if err != nil {
				return nil, err
			}
			lastDeparture = &cards[0]
		}
	}

	upcoming, err := s.schedules.ListByStationRange(ctx, localNow, stationID, localNow, dayEnd)
	if err != nil {
		return nil, err
	}
	cards, err := s.stationTrainCards(ctx, 0, upcoming, now, nil)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(cards) > limit {
		cards = cards[:limit]
	}
	recentSightings, err := s.reports.RecentStationSightingsByStation(ctx, stationID, localNow, 10)
	if err != nil {
		return nil, err
	}

	return &PublicStationDeparturesView{
		Station:         *station,
		LastDeparture:   lastDeparture,
		Upcoming:        cards,
		RecentSightings: recentSightings,
	}, nil
}

func (s *Service) StationDepartures(ctx context.Context, userID int64, now time.Time, stationID string, pastWindow, futureWindow time.Duration) (*StationDeparturesView, error) {
	localNow := now.In(s.loc)
	station, err := s.schedules.GetStation(ctx, localNow, stationID)
	if err != nil {
		return nil, err
	}
	if station == nil {
		return nil, ErrNotFound
	}
	if pastWindow <= 0 {
		pastWindow = authStationDeparturePastWindow
	}
	if futureWindow <= 0 {
		futureWindow = authStationDepartureFutureWindow
	}
	rangeStart := localNow.Add(-pastWindow)
	rangeEnd := localNow.Add(futureWindow)
	trains, err := s.schedules.ListByStationRange(ctx, localNow, stationID, rangeStart, rangeEnd)
	if err != nil {
		return nil, err
	}
	stationSightings, err := s.reports.StationSightingsByStationSince(ctx, stationID, rangeStart.Add(-stationSightingContextWindow), 250)
	if err != nil {
		return nil, err
	}
	trainCards, err := s.stationTrainCards(ctx, userID, trains, localNow, stationSightings)
	if err != nil {
		return nil, err
	}
	recentSightings, err := s.reports.RecentStationSightingsByStation(ctx, stationID, localNow, 10)
	if err != nil {
		return nil, err
	}
	return &StationDeparturesView{
		Station:         *station,
		Trains:          trainCards,
		RecentSightings: recentSightings,
	}, nil
}

func (s *Service) NetworkMap(ctx context.Context, now time.Time) (*NetworkMapView, error) {
	localNow := now.In(s.loc)
	stations, err := s.schedules.ListStations(ctx, localNow)
	if err != nil {
		return nil, err
	}
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, s.loc)
	sameDaySightings, err := s.reports.StationSightingsSince(ctx, dayStart, 500)
	if err != nil {
		return nil, err
	}
	visibleTrains, err := s.schedules.ListByWindow(ctx, localNow, "today")
	if err != nil {
		return nil, err
	}
	visibleTrainIDs := make(map[string]struct{}, len(visibleTrains))
	for _, train := range visibleTrains {
		visibleTrainIDs[train.ID] = struct{}{}
	}
	recentSightings := filterRelevantNetworkMapSightings(sameDaySightings, localNow, visibleTrainIDs)
	filtered := make([]domain.Station, 0, len(stations))
	for _, station := range stations {
		if station.Latitude == nil || station.Longitude == nil {
			continue
		}
		filtered = append(filtered, station)
	}
	return &NetworkMapView{
		Stations:         filtered,
		RecentSightings:  recentSightings,
		SameDaySightings: sameDaySightings,
	}, nil
}

func (s *Service) TrainStops(ctx context.Context, userID int64, now time.Time, trainID string) (*TrainStopsView, error) {
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return nil, err
	}
	if train == nil {
		return nil, ErrNotFound
	}
	card, err := s.trainCard(ctx, userID, *train, now)
	if err != nil {
		return nil, err
	}
	stops, err := s.store.ListTrainStops(ctx, trainID)
	if err != nil {
		return nil, err
	}
	stationSightings, err := s.reports.RecentStationSightingsByTrain(ctx, trainID, now.In(s.loc), 10)
	if err != nil {
		return nil, err
	}
	return &TrainStopsView{
		TrainCard:        card,
		Train:            *train,
		Stops:            stops,
		StationSightings: stationSightings,
	}, nil
}

func (s *Service) trainCards(ctx context.Context, userID int64, now time.Time, trains []domain.TrainInstance) ([]TrainCard, error) {
	out := make([]TrainCard, 0, len(trains))
	for _, train := range trains {
		card, err := s.trainCard(ctx, userID, train, now)
		if err != nil {
			return nil, err
		}
		out = append(out, card)
	}
	return out, nil
}

func (s *Service) stationTrainCards(ctx context.Context, userID int64, trains []domain.StationWindowTrain, now time.Time, stationSightings []domain.StationSighting) ([]StationTrainCard, error) {
	out := make([]StationTrainCard, 0, len(trains))
	for _, item := range trains {
		card, err := s.trainCard(ctx, userID, item.Train, now)
		if err != nil {
			return nil, err
		}
		context := stationSightingContextForPassAt(stationSightings, item.PassAt)
		out = append(out, StationTrainCard{
			TrainCard:       card,
			StationID:       item.StationID,
			StationName:     item.StationName,
			PassAt:          item.PassAt.In(s.loc),
			SightingCount:   len(context),
			SightingContext: context,
		})
	}
	return out, nil
}

func (s *Service) trainCard(ctx context.Context, userID int64, train domain.TrainInstance, now time.Time) (TrainCard, error) {
	status, err := s.reports.BuildStatus(ctx, train.ID, now.In(s.loc))
	if err != nil {
		return TrainCard{}, err
	}
	riders, err := s.store.CountActiveCheckins(ctx, train.ID, now.In(s.loc))
	if err != nil {
		return TrainCard{}, err
	}
	return TrainCard{
		Train:  train,
		Status: status,
		Riders: riders,
	}, nil
}

func (s *Service) ListActiveCheckinUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	return s.store.ListActiveCheckinUsers(ctx, trainID, now.In(s.loc))
}

func (s *Service) matchStationSightingTrainID(ctx context.Context, now time.Time, stationID string, destinationStationID *string) (*string, error) {
	if destinationStationID == nil {
		return nil, nil
	}
	items, err := s.schedules.ListRouteWindowTrains(ctx, now, stationID, *destinationStationID, 90*time.Minute)
	if err != nil {
		return nil, err
	}
	rangeStart := now.Add(-5 * time.Minute)
	rangeEnd := now.Add(90 * time.Minute)
	candidates := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		if item.FromPassAt.Before(rangeStart) || item.FromPassAt.After(rangeEnd) {
			continue
		}
		if _, ok := seen[item.Train.ID]; ok {
			continue
		}
		seen[item.Train.ID] = struct{}{}
		candidates = append(candidates, item.Train.ID)
	}
	if len(candidates) != 1 {
		return nil, nil
	}
	return &candidates[0], nil
}

func (s *Service) selectedStationDeparture(ctx context.Context, now time.Time, stationID string, trainID string) (*domain.StationWindowTrain, error) {
	items, err := s.schedules.ListByStationRange(ctx, now, stationID, now.Add(-authStationDeparturePastWindow), now.Add(authStationDepartureFutureWindow))
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.Train.ID == trainID {
			copyItem := item
			return &copyItem, nil
		}
	}
	return nil, ErrNotFound
}

func filterRelevantNetworkMapSightings(items []domain.StationSighting, now time.Time, visibleTrainIDs map[string]struct{}) []domain.StationSighting {
	if len(items) == 0 || len(visibleTrainIDs) == 0 {
		return nil
	}
	cutoff := now.Add(-networkMapRelevantSightingWindow)
	out := make([]domain.StationSighting, 0, len(items))
	for _, item := range items {
		if item.CreatedAt.Before(cutoff) {
			continue
		}
		if item.MatchedTrainInstanceID == nil {
			continue
		}
		if _, ok := visibleTrainIDs[strings.TrimSpace(*item.MatchedTrainInstanceID)]; !ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (s *Service) trainTerminalStop(ctx context.Context, trainID string) (*domain.TrainStop, error) {
	stops, err := s.store.ListTrainStops(ctx, trainID)
	if err != nil {
		return nil, err
	}
	if len(stops) == 0 {
		return nil, nil
	}
	last := stops[0]
	for _, stop := range stops[1:] {
		if stop.Seq > last.Seq {
			last = stop
		}
	}
	return &last, nil
}

func stationSightingContextForPassAt(items []domain.StationSighting, passAt time.Time) []domain.StationSighting {
	if len(items) == 0 {
		return nil
	}
	out := make([]domain.StationSighting, 0, len(items))
	for _, item := range items {
		diff := item.CreatedAt.Sub(passAt)
		if diff < 0 {
			diff = -diff
		}
		if diff > stationSightingContextWindow {
			continue
		}
		out = append(out, item)
	}
	return out
}

var (
	ErrNotFound           = errors.New("not found")
	ErrCheckInUnavailable = errors.New("departure is no longer available for check-in")
)

func filterStations(stations []domain.Station, query string) []domain.Station {
	normalizedQuery := normalizeStationQuery(query)
	if normalizedQuery == "" {
		return stations
	}
	out := make([]domain.Station, 0, len(stations))
	for _, station := range stations {
		normalizedKey := normalizeStationQuery(station.NormalizedKey)
		normalizedName := normalizeStationQuery(station.Name)
		if strings.HasPrefix(normalizedKey, normalizedQuery) || strings.HasPrefix(normalizedName, normalizedQuery) {
			out = append(out, station)
		}
	}
	return out
}

func normalizeStationQuery(v string) string {
	return stationsearch.Normalize(v)
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func ParseLanguage(v string) domain.Language {
	if strings.EqualFold(strings.TrimSpace(v), string(domain.LanguageLV)) {
		return domain.LanguageLV
	}
	return domain.LanguageEN
}

func ParseAlertStyle(v string) (domain.AlertStyle, error) {
	switch strings.ToUpper(strings.TrimSpace(v)) {
	case string(domain.AlertStyleDetailed):
		return domain.AlertStyleDetailed, nil
	case string(domain.AlertStyleDiscreet):
		return domain.AlertStyleDiscreet, nil
	default:
		return "", fmt.Errorf("unsupported alert style %q", v)
	}
}
