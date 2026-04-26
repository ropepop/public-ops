package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/spacetime"
)

type SpacetimeStore struct {
	client *spacetime.Syncer

	mu      sync.Mutex
	pending map[string]pendingScheduleSnapshot
}

type pendingScheduleSnapshot struct {
	importID      string
	sourceVersion string
	trains        []domain.TrainInstance
}

func NewSpacetimeStore(client *spacetime.Syncer) *SpacetimeStore {
	return &SpacetimeStore{
		client:  client,
		pending: map[string]pendingScheduleSnapshot{},
	}
}

func (s *SpacetimeStore) Close() error {
	return nil
}

func (s *SpacetimeStore) Migrate(context.Context) error {
	return nil
}

func (s *SpacetimeStore) UpsertTrainInstances(_ context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make([]domain.TrainInstance, len(trains))
	copy(copied, trains)
	s.pending[strings.TrimSpace(serviceDate)] = pendingScheduleSnapshot{
		importID:      fmt.Sprintf("schedule-%s-%d", strings.TrimSpace(serviceDate), time.Now().UTC().UnixNano()),
		sourceVersion: strings.TrimSpace(sourceVersion),
		trains:        copied,
	}
	return nil
}

func (s *SpacetimeStore) UpsertTrainStops(ctx context.Context, serviceDate string, stopsByTrain map[string][]domain.TrainStop) error {
	serviceDate = strings.TrimSpace(serviceDate)
	s.mu.Lock()
	pending := s.pending[serviceDate]
	delete(s.pending, serviceDate)
	s.mu.Unlock()

	trains := pending.trains
	sourceVersion := pending.sourceVersion
	importID := strings.TrimSpace(pending.importID)
	if len(trains) == 0 {
		existing, err := s.ListTrainInstancesByDate(ctx, serviceDate)
		if err != nil {
			return err
		}
		trains = existing
	}
	if sourceVersion == "" {
		sourceVersion = "snapshot-unknown"
	}
	if importID == "" {
		importID = fmt.Sprintf("schedule-%s-%d", serviceDate, time.Now().UTC().UnixNano())
	}

	return s.importTrainData(ctx, serviceDate, sourceVersion, importID, trains, stopsByTrain)
}

func (s *SpacetimeStore) ImportTrainData(ctx context.Context, serviceDate string, sourceVersion string, trains []domain.TrainInstance, stopsByTrain map[string][]domain.TrainStop) error {
	serviceDate = strings.TrimSpace(serviceDate)
	importID := fmt.Sprintf("feed-%s-%d", serviceDate, time.Now().UTC().UnixNano())
	return s.importTrainData(ctx, serviceDate, sourceVersion, importID, trains, stopsByTrain)
}

func (s *SpacetimeStore) importTrainData(ctx context.Context, serviceDate string, sourceVersion string, importID string, trains []domain.TrainInstance, stopsByTrain map[string][]domain.TrainStop) error {
	stationList, tripBatches := buildScheduleBatchSnapshot(nil, trains, stopsByTrain, sourceVersion)
	payloadBytes, _ := json.Marshal(struct {
		Stations []spacetime.ScheduleStation       `json:"stations"`
		Trips    []spacetime.ScheduleTripBatchItem `json:"trips"`
	}{
		Stations: stationList,
		Trips:    tripBatches,
	})
	log.Printf(
		"spacetime schedule import starting importId=%s serviceDate=%s stations=%d trains=%d stops=%d bytes=%d",
		importID,
		serviceDate,
		len(stationList),
		len(tripBatches),
		countScheduleBatchStops(tripBatches),
		len(payloadBytes),
	)
	if err := s.client.ServiceReplaceScheduleBatch(ctx, serviceDate, sourceVersion, stationList, tripBatches, true, true); err != nil {
		log.Printf("spacetime schedule import failed importId=%s serviceDate=%s stage=replace-batch: %v", importID, serviceDate, err)
		return err
	}
	log.Printf("spacetime schedule import committed importId=%s serviceDate=%s trips=%d", importID, serviceDate, len(tripBatches))
	return nil
}

func countScheduleBatchStops(items []spacetime.ScheduleTripBatchItem) int {
	total := 0
	for _, item := range items {
		total += len(item.Stops)
	}
	return total
}

func buildScheduleBatchSnapshot(stations []domain.Station, trains []domain.TrainInstance, stopsByTrain map[string][]domain.TrainStop, fallbackSourceVersion string) ([]spacetime.ScheduleStation, []spacetime.ScheduleTripBatchItem) {
	stationsByID := map[string]spacetime.ScheduleStation{}

	for _, station := range stations {
		stationName := strings.TrimSpace(station.Name)
		if stationName == "" {
			continue
		}
		stationID := strings.TrimSpace(station.ID)
		if stationID == "" {
			stationID = spacetimeNormalizeStationID(stationName)
		}
		existing := stationsByID[stationID]
		existing.ID = stationID
		existing.Name = chooseNonEmpty(stationName, existing.Name)
		existing.NormalizedKey = chooseNonEmpty(strings.TrimSpace(station.NormalizedKey), existing.NormalizedKey, spacetimeNormalizeStationKey(stationName))
		if station.Latitude != nil {
			existing.Latitude = station.Latitude
		}
		if station.Longitude != nil {
			existing.Longitude = station.Longitude
		}
		stationsByID[stationID] = existing
	}

	tripBatches := make([]spacetime.ScheduleTripBatchItem, 0, len(trains))
	for _, train := range trains {
		stops := append([]domain.TrainStop(nil), stopsByTrain[train.ID]...)
		sort.Slice(stops, func(i, j int) bool {
			if stops[i].Seq == stops[j].Seq {
				return stops[i].StationName < stops[j].StationName
			}
			return stops[i].Seq < stops[j].Seq
		})

		batchTrip := spacetime.ScheduleTripBatchItem{
			ID:            train.ID,
			ServiceDate:   train.ServiceDate,
			FromStation:   train.FromStation,
			ToStation:     train.ToStation,
			DepartureAt:   train.DepartureAt.UTC().Format(time.RFC3339),
			ArrivalAt:     train.ArrivalAt.UTC().Format(time.RFC3339),
			SourceVersion: chooseNonEmpty(train.SourceVersion, fallbackSourceVersion),
			Stops:         make([]spacetime.ScheduleStop, 0, len(stops)),
		}
		for _, stop := range stops {
			stationName := strings.TrimSpace(stop.StationName)
			if stationName == "" {
				continue
			}
			stationID := strings.TrimSpace(stop.StationID)
			if stationID == "" {
				stationID = spacetimeNormalizeStationID(stationName)
			}
			existing := stationsByID[stationID]
			existing.ID = stationID
			existing.Name = chooseNonEmpty(stationName, existing.Name)
			existing.NormalizedKey = chooseNonEmpty(existing.NormalizedKey, spacetimeNormalizeStationKey(stationName))
			if stop.Latitude != nil {
				existing.Latitude = stop.Latitude
			}
			if stop.Longitude != nil {
				existing.Longitude = stop.Longitude
			}
			stationsByID[stationID] = existing

			batchStop := spacetime.ScheduleStop{
				TrainInstanceID: train.ID,
				StationID:       stationID,
				StationName:     stationName,
				Seq:             stop.Seq,
				Latitude:        stop.Latitude,
				Longitude:       stop.Longitude,
			}
			if stop.ArrivalAt != nil {
				batchStop.ArrivalAt = stop.ArrivalAt.UTC().Format(time.RFC3339)
			}
			if stop.DepartureAt != nil {
				batchStop.DepartureAt = stop.DepartureAt.UTC().Format(time.RFC3339)
			}
			batchTrip.Stops = append(batchTrip.Stops, batchStop)
		}
		tripBatches = append(tripBatches, batchTrip)
	}

	stationList := make([]spacetime.ScheduleStation, 0, len(stationsByID))
	for _, station := range stationsByID {
		stationList = append(stationList, station)
	}
	sort.Slice(stationList, func(i, j int) bool {
		if stationList[i].Name == stationList[j].Name {
			return stationList[i].ID < stationList[j].ID
		}
		return stationList[i].Name < stationList[j].Name
	})
	sort.Slice(tripBatches, func(i, j int) bool {
		if tripBatches[i].DepartureAt == tripBatches[j].DepartureAt {
			return tripBatches[i].ID < tripBatches[j].ID
		}
		return tripBatches[i].DepartureAt < tripBatches[j].DepartureAt
	})
	return stationList, tripBatches
}

func stopsByTrainFromList(items []domain.TrainStop) map[string][]domain.TrainStop {
	out := make(map[string][]domain.TrainStop)
	for _, stop := range items {
		trainID := strings.TrimSpace(stop.TrainInstanceID)
		if trainID == "" {
			continue
		}
		out[trainID] = append(out[trainID], stop)
	}
	return out
}

func scheduleSourceVersion(fallback string, trains []domain.TrainInstance) string {
	if trimmed := strings.TrimSpace(fallback); trimmed != "" {
		return trimmed
	}
	for _, train := range trains {
		if trimmed := strings.TrimSpace(train.SourceVersion); trimmed != "" {
			return trimmed
		}
	}
	return "snapshot-unknown"
}

func (s *SpacetimeStore) ScheduleCounts(ctx context.Context, serviceDate string) (int, int, int, error) {
	if s == nil || s.client == nil {
		return 0, 0, 0, nil
	}
	present, err := s.client.ServiceSchedulePresent(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return 0, 0, 0, err
	}
	if !present {
		return 0, 0, 0, nil
	}
	// Recovery only needs to know whether the day already exists in Spacetime.
	// Avoid the heavier schedule reconstruction path here because some live SQL
	// backends do not support the richer trip tables consistently.
	return 0, 1, 0, nil
}

func (s *SpacetimeStore) ReplaceScheduleSnapshot(ctx context.Context, serviceDate string, sourceVersion string, stations []domain.Station, trains []domain.TrainInstance, stops []domain.TrainStop) (int, int, int, error) {
	if s == nil || s.client == nil {
		return 0, 0, 0, nil
	}
	serviceDate = strings.TrimSpace(serviceDate)
	sourceVersion = scheduleSourceVersion(sourceVersion, trains)
	stationList, tripBatches := buildScheduleBatchSnapshot(stations, trains, stopsByTrainFromList(stops), sourceVersion)
	payloadBytes, _ := json.Marshal(struct {
		Stations []spacetime.ScheduleStation       `json:"stations"`
		Trips    []spacetime.ScheduleTripBatchItem `json:"trips"`
	}{
		Stations: stationList,
		Trips:    tripBatches,
	})
	log.Printf(
		"spacetime schedule replace starting serviceDate=%s stations=%d trains=%d stops=%d bytes=%d",
		serviceDate,
		len(stationList),
		len(tripBatches),
		countScheduleBatchStops(tripBatches),
		len(payloadBytes),
	)
	if err := s.client.ServiceReplaceScheduleBatch(ctx, serviceDate, sourceVersion, stationList, tripBatches, true, true); err != nil {
		log.Printf("spacetime schedule replace failed serviceDate=%s: %v", serviceDate, err)
		return 0, 0, 0, err
	}
	log.Printf("spacetime schedule replace committed serviceDate=%s trains=%d", serviceDate, len(tripBatches))
	return len(stationList), len(tripBatches), countScheduleBatchStops(tripBatches), nil
}

func (s *SpacetimeStore) PublishActiveBundle(ctx context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.PublishActiveBundle(ctx, strings.TrimSpace(version), strings.TrimSpace(serviceDate), generatedAt.UTC(), strings.TrimSpace(sourceVersion))
}

func (s *SpacetimeStore) PublishRuntimeConfig(ctx context.Context, scheduleCutoffHour int) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.PublishRuntimeConfig(ctx, scheduleCutoffHour)
}

func (s *SpacetimeStore) ListTrainInstancesByDate(ctx context.Context, serviceDate string) ([]domain.TrainInstance, error) {
	_, trips, err := s.client.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return nil, err
	}
	out := make([]domain.TrainInstance, 0, len(trips))
	for _, trip := range trips {
		item, err := tripToDomainTrain(trip)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SpacetimeStore) ListTrainInstancesByWindow(ctx context.Context, serviceDate string, start, end time.Time) ([]domain.TrainInstance, error) {
	items, err := s.ListTrainInstancesByDate(ctx, serviceDate)
	if err != nil {
		return nil, err
	}
	out := make([]domain.TrainInstance, 0, len(items))
	start = start.UTC()
	end = end.UTC()
	for _, item := range items {
		if !item.DepartureAt.Before(start) && !item.DepartureAt.After(end) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DepartureAt.Before(out[j].DepartureAt)
	})
	return out, nil
}

func (s *SpacetimeStore) ListStationWindowTrains(ctx context.Context, serviceDate string, stationID string, start, end time.Time) ([]domain.StationWindowTrain, error) {
	_, trips, err := s.client.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return nil, err
	}
	out := make([]domain.StationWindowTrain, 0)
	start = start.UTC()
	end = end.UTC()
	for _, trip := range trips {
		train, err := tripToDomainTrain(trip)
		if err != nil {
			return nil, err
		}
		for _, stop := range trip.Stops {
			if strings.TrimSpace(stop.StationID) != strings.TrimSpace(stationID) {
				continue
			}
			passAt, err := stopPassTime(stop)
			if err != nil {
				continue
			}
			if passAt.Before(start) || passAt.After(end) {
				continue
			}
			out = append(out, domain.StationWindowTrain{
				Train:       train,
				StationID:   strings.TrimSpace(stop.StationID),
				StationName: strings.TrimSpace(stop.StationName),
				PassAt:      passAt,
			})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PassAt.Equal(out[j].PassAt) {
			return out[i].Train.DepartureAt.Before(out[j].Train.DepartureAt)
		}
		return out[i].PassAt.Before(out[j].PassAt)
	})
	return out, nil
}

func (s *SpacetimeStore) ListRouteWindowTrains(ctx context.Context, serviceDate string, fromStationID string, toStationID string, start, end time.Time) ([]domain.RouteWindowTrain, error) {
	_, trips, err := s.client.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return nil, err
	}
	out := make([]domain.RouteWindowTrain, 0)
	start = start.UTC()
	end = end.UTC()
	for _, trip := range trips {
		train, err := tripToDomainTrain(trip)
		if err != nil {
			return nil, err
		}
		fromStop, toStop := routeStopsForTrip(trip, strings.TrimSpace(fromStationID), strings.TrimSpace(toStationID))
		if fromStop == nil || toStop == nil {
			continue
		}
		fromPassAt, err := stopPassTime(*fromStop)
		if err != nil {
			continue
		}
		toPassAt, err := stopArrivalOrDepartureTime(*toStop)
		if err != nil {
			continue
		}
		if fromPassAt.Before(start) || fromPassAt.After(end) {
			continue
		}
		out = append(out, domain.RouteWindowTrain{
			Train:           train,
			FromStationID:   strings.TrimSpace(fromStop.StationID),
			FromStationName: strings.TrimSpace(fromStop.StationName),
			ToStationID:     strings.TrimSpace(toStop.StationID),
			ToStationName:   strings.TrimSpace(toStop.StationName),
			FromPassAt:      fromPassAt,
			ToPassAt:        toPassAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromPassAt.Equal(out[j].FromPassAt) {
			return out[i].Train.DepartureAt.Before(out[j].Train.DepartureAt)
		}
		return out[i].FromPassAt.Before(out[j].FromPassAt)
	})
	return out, nil
}

func (s *SpacetimeStore) ListStationsByDate(ctx context.Context, serviceDate string) ([]domain.Station, error) {
	serviceDay, _, err := s.client.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return nil, err
	}
	if serviceDay == nil {
		return nil, nil
	}
	out := make([]domain.Station, 0, len(serviceDay.Stations))
	for _, station := range serviceDay.Stations {
		out = append(out, stationToDomain(station))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (s *SpacetimeStore) ListReachableDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error) {
	_, trips, err := s.client.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return nil, err
	}
	destinations := map[string]domain.Station{}
	for _, trip := range trips {
		seenOrigin := false
		for _, stop := range trip.Stops {
			if !seenOrigin {
				if strings.TrimSpace(stop.StationID) == strings.TrimSpace(fromStationID) {
					seenOrigin = true
				}
				continue
			}
			destinations[stop.StationID] = stationFromStop(stop)
		}
	}
	return sortStationsMap(destinations), nil
}

func (s *SpacetimeStore) ListTerminalDestinations(ctx context.Context, serviceDate string, fromStationID string) ([]domain.Station, error) {
	_, trips, err := s.client.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return nil, err
	}
	destinations := map[string]domain.Station{}
	for _, trip := range trips {
		stops := trip.Stops
		if len(stops) < 2 {
			continue
		}
		originIndex := -1
		for index, stop := range stops {
			if strings.TrimSpace(stop.StationID) == strings.TrimSpace(fromStationID) {
				originIndex = index
				break
			}
		}
		if originIndex < 0 || originIndex >= len(stops)-1 {
			continue
		}
		terminal := stops[len(stops)-1]
		destinations[terminal.StationID] = stationFromStop(terminal)
	}
	return sortStationsMap(destinations), nil
}

func (s *SpacetimeStore) GetStationByID(ctx context.Context, stationID string) (*domain.Station, error) {
	for _, serviceDate := range candidateServiceDates() {
		stations, err := s.ListStationsByDate(ctx, serviceDate)
		if err != nil {
			return nil, err
		}
		for _, station := range stations {
			if station.ID == strings.TrimSpace(stationID) {
				item := station
				return &item, nil
			}
		}
	}
	return nil, nil
}

func (s *SpacetimeStore) ListTrainStops(ctx context.Context, trainID string) ([]domain.TrainStop, error) {
	trip, err := s.client.ServiceGetTrip(ctx, strings.TrimSpace(trainID))
	if err != nil {
		return nil, err
	}
	if trip == nil {
		return nil, nil
	}
	out := make([]domain.TrainStop, 0, len(trip.Stops))
	for _, stop := range trip.Stops {
		item, err := stopToDomain(trip.ID, stop)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Seq < out[j].Seq
	})
	return out, nil
}

func (s *SpacetimeStore) TrainHasStops(ctx context.Context, trainID string) (bool, error) {
	trip, err := s.client.ServiceGetTrip(ctx, strings.TrimSpace(trainID))
	if err != nil {
		return false, err
	}
	return trip != nil && len(trip.Stops) > 0, nil
}

func (s *SpacetimeStore) GetTrainInstanceByID(ctx context.Context, id string) (*domain.TrainInstance, error) {
	trip, err := s.client.ServiceGetTrip(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if trip == nil {
		return nil, nil
	}
	item, err := tripToDomainTrain(*trip)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *SpacetimeStore) EnsureUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return domain.UserSettings{}, err
	}
	return riderSettingsToDomain(userID, rider), nil
}

func (s *SpacetimeStore) GetUserSettings(ctx context.Context, userID int64) (domain.UserSettings, error) {
	return s.EnsureUserSettings(ctx, userID)
}

func (s *SpacetimeStore) HasUserSettings(ctx context.Context, userID int64) (bool, error) {
	rider, err := s.loadRider(ctx, userID)
	if err != nil {
		return false, err
	}
	return rider != nil, nil
}

func (s *SpacetimeStore) SetAlertsEnabled(ctx context.Context, userID int64, enabled bool) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.Settings.AlertsEnabled = enabled
	rider.Settings.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.UpdatedAt = rider.Settings.UpdatedAt
	rider.LastSeenAt = rider.Settings.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) SetAlertStyle(ctx context.Context, userID int64, style domain.AlertStyle) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.Settings.AlertStyle = string(style)
	rider.Settings.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.UpdatedAt = rider.Settings.UpdatedAt
	rider.LastSeenAt = rider.Settings.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) ToggleAlertStyle(ctx context.Context, userID int64) (domain.AlertStyle, error) {
	settings, err := s.EnsureUserSettings(ctx, userID)
	if err != nil {
		return "", err
	}
	next := domain.AlertStyleDetailed
	if settings.AlertStyle == domain.AlertStyleDetailed {
		next = domain.AlertStyleDiscreet
	}
	return next, s.SetAlertStyle(ctx, userID, next)
}

func (s *SpacetimeStore) SetLanguage(ctx context.Context, userID int64, lang domain.Language) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.Settings.Language = string(lang)
	rider.Settings.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.UpdatedAt = rider.Settings.UpdatedAt
	rider.LastSeenAt = rider.Settings.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) ResetTestUser(ctx context.Context, userID int64) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.ServiceResetTestRider(ctx, spacetime.StableIDForTelegramUser(userID))
}

func (s *SpacetimeStore) ConsumeTestLoginTicket(ctx context.Context, nonceHash string, userID int64, expiresAt time.Time) (bool, error) {
	if s == nil || s.client == nil {
		return false, nil
	}
	err := s.client.ServiceConsumeTestLoginTicket(ctx, strings.TrimSpace(nonceHash), spacetime.StableIDForTelegramUser(userID), expiresAt)
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "test login ticket already used") {
		return false, nil
	}
	return false, err
}

func (s *SpacetimeStore) CheckInUser(ctx context.Context, userID int64, trainID string, checkedInAt, autoCheckoutAt time.Time) error {
	return s.CheckInUserAtStation(ctx, userID, trainID, nil, checkedInAt, autoCheckoutAt)
}

func (s *SpacetimeStore) CheckInUserAtStation(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	boarding := ""
	if boardingStationID != nil {
		boarding = strings.TrimSpace(*boardingStationID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rider.CurrentRide = &spacetime.TrainbotRideState{
		TrainInstanceID:   strings.TrimSpace(trainID),
		BoardingStationID: boarding,
		CheckedInAt:       checkedInAt.UTC().Format(time.RFC3339),
		AutoCheckoutAt:    autoCheckoutAt.UTC().Format(time.RFC3339),
	}
	rider.UndoRide = nil
	rider.UpdatedAt = now
	rider.LastSeenAt = now
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) GetActiveCheckIn(ctx context.Context, userID int64, now time.Time) (*domain.CheckIn, error) {
	rider, err := s.loadRider(ctx, userID)
	if err != nil || rider == nil || rider.CurrentRide == nil {
		return nil, err
	}
	autoCheckoutAt, err := time.Parse(time.RFC3339, rider.CurrentRide.AutoCheckoutAt)
	if err != nil {
		return nil, err
	}
	if autoCheckoutAt.Before(now.UTC()) {
		return nil, nil
	}
	checkedInAt, err := time.Parse(time.RFC3339, rider.CurrentRide.CheckedInAt)
	if err != nil {
		return nil, err
	}
	item := &domain.CheckIn{
		UserID:          userID,
		TrainInstanceID: rider.CurrentRide.TrainInstanceID,
		CheckedInAt:     checkedInAt,
		AutoCheckoutAt:  autoCheckoutAt,
		IsActive:        true,
	}
	if boarding := strings.TrimSpace(rider.CurrentRide.BoardingStationID); boarding != "" {
		item.BoardingStationID = &boarding
	}
	return item, nil
}

func (s *SpacetimeStore) CheckoutUser(ctx context.Context, userID int64) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.CurrentRide = nil
	rider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.LastSeenAt = rider.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) UndoCheckoutUser(ctx context.Context, userID int64, trainID string, boardingStationID *string, checkedInAt, autoCheckoutAt time.Time) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	boarding := ""
	if boardingStationID != nil {
		boarding = strings.TrimSpace(*boardingStationID)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rider.CurrentRide = &spacetime.TrainbotRideState{
		TrainInstanceID:   strings.TrimSpace(trainID),
		BoardingStationID: boarding,
		CheckedInAt:       checkedInAt.UTC().Format(time.RFC3339),
		AutoCheckoutAt:    autoCheckoutAt.UTC().Format(time.RFC3339),
	}
	rider.UndoRide = nil
	rider.UpdatedAt = now
	rider.LastSeenAt = now
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) SetTrainMute(ctx context.Context, userID int64, trainID string, until time.Time) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.Mutes = filterMutes(rider.Mutes, strings.TrimSpace(trainID))
	rider.Mutes = append([]spacetime.TrainbotMute{{
		TrainInstanceID: strings.TrimSpace(trainID),
		MutedUntil:      until.UTC().Format(time.RFC3339),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}}, rider.Mutes...)
	rider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.LastSeenAt = rider.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) IsTrainMuted(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error) {
	rider, err := s.loadRider(ctx, userID)
	if err != nil || rider == nil {
		return false, err
	}
	for _, mute := range rider.Mutes {
		if strings.TrimSpace(mute.TrainInstanceID) != strings.TrimSpace(trainID) {
			continue
		}
		mutedUntil, err := time.Parse(time.RFC3339, mute.MutedUntil)
		if err == nil && !mutedUntil.Before(now.UTC()) {
			return true, nil
		}
	}
	return false, nil
}

func (s *SpacetimeStore) CountActiveCheckins(ctx context.Context, trainID string, now time.Time) (int, error) {
	users, err := s.ListActiveCheckinUsers(ctx, trainID, now)
	if err != nil {
		return 0, err
	}
	return len(users), nil
}

func (s *SpacetimeStore) ListActiveCheckinUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	userIDs, err := s.client.ServiceListActiveCheckinUsers(ctx, trainID, now)
	if err == nil {
		return parseServiceUserIDs(userIDs), nil
	}
	riders, err := s.client.ServiceListRiders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0)
	for _, rider := range riders {
		if rider.CurrentRide == nil || strings.TrimSpace(rider.CurrentRide.TrainInstanceID) != strings.TrimSpace(trainID) {
			continue
		}
		autoCheckoutAt, err := time.Parse(time.RFC3339, rider.CurrentRide.AutoCheckoutAt)
		if err != nil || autoCheckoutAt.Before(now.UTC()) {
			continue
		}
		if userID, ok := spacetime.TelegramUserIDFromStableID(rider.StableID); ok {
			out = append(out, userID)
		}
	}
	return out, nil
}

func (s *SpacetimeStore) UpsertSubscription(ctx context.Context, userID int64, trainID string, expiresAt time.Time) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.Subscriptions = filterSubscriptions(rider.Subscriptions, strings.TrimSpace(trainID))
	now := time.Now().UTC().Format(time.RFC3339)
	rider.Subscriptions = append([]spacetime.TrainbotSubscription{{
		TrainInstanceID: strings.TrimSpace(trainID),
		ExpiresAt:       expiresAt.UTC().Format(time.RFC3339),
		IsActive:        true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}}, rider.Subscriptions...)
	rider.UpdatedAt = now
	rider.LastSeenAt = now
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) DeactivateSubscription(ctx context.Context, userID int64, trainID string) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	for index, subscription := range rider.Subscriptions {
		if strings.TrimSpace(subscription.TrainInstanceID) != strings.TrimSpace(trainID) {
			continue
		}
		rider.Subscriptions[index].IsActive = false
		rider.Subscriptions[index].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	rider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.LastSeenAt = rider.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) HasActiveSubscription(ctx context.Context, userID int64, trainID string, now time.Time) (bool, error) {
	rider, err := s.loadRider(ctx, userID)
	if err != nil || rider == nil {
		return false, err
	}
	for _, subscription := range rider.Subscriptions {
		if strings.TrimSpace(subscription.TrainInstanceID) != strings.TrimSpace(trainID) || !subscription.IsActive {
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339, subscription.ExpiresAt)
		if err == nil && !expiresAt.Before(now.UTC()) {
			return true, nil
		}
	}
	return false, nil
}

func (s *SpacetimeStore) ListActiveSubscriptionUsers(ctx context.Context, trainID string, now time.Time) ([]int64, error) {
	userIDs, err := s.client.ServiceListActiveSubscriptionUsers(ctx, trainID, now)
	if err == nil {
		return parseServiceUserIDs(userIDs), nil
	}
	riders, err := s.client.ServiceListRiders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0)
	for _, rider := range riders {
		for _, subscription := range rider.Subscriptions {
			if strings.TrimSpace(subscription.TrainInstanceID) != strings.TrimSpace(trainID) || !subscription.IsActive {
				continue
			}
			expiresAt, err := time.Parse(time.RFC3339, subscription.ExpiresAt)
			if err != nil || expiresAt.Before(now.UTC()) {
				continue
			}
			if userID, ok := spacetime.TelegramUserIDFromStableID(rider.StableID); ok {
				out = append(out, userID)
			}
			break
		}
	}
	return out, nil
}

func (s *SpacetimeStore) UpsertFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	fromStationID = strings.TrimSpace(fromStationID)
	toStationID = strings.TrimSpace(toStationID)
	rider.Favorites = filterFavorites(rider.Favorites, fromStationID, toStationID)
	rider.Favorites = append([]spacetime.TrainbotFavorite{{
		FromStationID:   fromStationID,
		FromStationName: s.stationName(ctx, fromStationID),
		ToStationID:     toStationID,
		ToStationName:   s.stationName(ctx, toStationID),
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}}, rider.Favorites...)
	rider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.LastSeenAt = rider.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) DeleteFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	rider, err := s.ensureRider(ctx, userID)
	if err != nil {
		return err
	}
	rider.Favorites = filterFavorites(rider.Favorites, strings.TrimSpace(fromStationID), strings.TrimSpace(toStationID))
	rider.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	rider.LastSeenAt = rider.UpdatedAt
	return s.client.ServicePutRider(ctx, rider)
}

func (s *SpacetimeStore) ListFavoriteRoutes(ctx context.Context, userID int64) ([]domain.FavoriteRoute, error) {
	rider, err := s.loadRider(ctx, userID)
	if err != nil || rider == nil {
		return nil, err
	}
	out := make([]domain.FavoriteRoute, 0, len(rider.Favorites))
	for _, favorite := range rider.Favorites {
		item, err := favoriteToDomain(userID, favorite)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			if out[i].FromStationID == out[j].FromStationID {
				return out[i].ToStationID < out[j].ToStationID
			}
			return out[i].FromStationID < out[j].FromStationID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *SpacetimeStore) ListAllFavoriteRoutes(ctx context.Context) ([]domain.FavoriteRoute, error) {
	riders, err := s.client.ServiceListRiders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.FavoriteRoute, 0)
	for _, rider := range riders {
		userID, ok := spacetime.TelegramUserIDFromStableID(rider.StableID)
		if !ok {
			continue
		}
		for _, favorite := range rider.Favorites {
			item, err := favoriteToDomain(userID, favorite)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			if out[i].UserID == out[j].UserID {
				if out[i].FromStationID == out[j].FromStationID {
					return out[i].ToStationID < out[j].ToStationID
				}
				return out[i].FromStationID < out[j].FromStationID
			}
			return out[i].UserID < out[j].UserID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *SpacetimeStore) InsertReportEvent(ctx context.Context, e domain.ReportEvent) error {
	activity, err := s.ensureTrainActivity(ctx, e.TrainInstanceID, e.CreatedAt)
	if err != nil {
		return err
	}
	rider, err := s.ensureRider(ctx, e.UserID)
	if err != nil {
		return err
	}
	activity.Timeline = append(activity.Timeline, spacetime.TrainbotActivityEvent{
		ID:              strings.TrimSpace(e.ID),
		Kind:            "report",
		StableID:        spacetime.StableIDForTelegramUser(e.UserID),
		Nickname:        rider.Nickname,
		Name:            reportSignalLabel(e.Signal),
		CreatedAt:       e.CreatedAt.UTC().Format(time.RFC3339),
		Signal:          string(e.Signal),
		TrainInstanceID: strings.TrimSpace(e.TrainInstanceID),
	})
	return s.client.ServicePutActivity(ctx, *activity)
}

func (s *SpacetimeStore) GetLastReportByUserTrain(ctx context.Context, userID int64, trainID string) (*domain.ReportEvent, error) {
	activity, err := s.findTrainActivity(ctx, trainID)
	if err != nil || activity == nil {
		return nil, err
	}
	stableID := spacetime.StableIDForTelegramUser(userID)
	for _, event := range sortActivityTimeline(activity.Timeline) {
		if event.Kind == "report" && event.StableID == stableID {
			item, err := reportEventToDomain(userID, event)
			if err != nil {
				return nil, err
			}
			return &item, nil
		}
	}
	return nil, nil
}

func (s *SpacetimeStore) ListReportsSince(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.ReportEvent, error) {
	activity, err := s.findTrainActivity(ctx, trainID)
	if err != nil || activity == nil {
		return nil, err
	}
	return reportsFromActivity(*activity, since, limit)
}

func (s *SpacetimeStore) ListRecentReports(ctx context.Context, trainID string, limit int) ([]domain.ReportEvent, error) {
	return s.ListReportsSince(ctx, trainID, time.Time{}, limit)
}

func (s *SpacetimeStore) ListRecentReportEvents(ctx context.Context, since time.Time, limit int) ([]domain.ReportEvent, error) {
	activities, err := s.client.ServiceListActivities(ctx, spacetime.ListActivitiesFilter{
		Since:     &since,
		ScopeType: "train",
	})
	if err != nil {
		return nil, err
	}
	out := make([]domain.ReportEvent, 0)
	for _, activity := range activities {
		items, err := reportsFromActivity(activity, since, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return trimReports(out, limit), nil
}

func (s *SpacetimeStore) InsertStationSighting(ctx context.Context, e domain.StationSighting) error {
	activity, err := s.ensureStationActivity(ctx, e.StationID, e.StationName, e.CreatedAt)
	if err != nil {
		return err
	}
	rider, err := s.ensureRider(ctx, e.UserID)
	if err != nil {
		return err
	}
	destinationStationID := ""
	if e.DestinationStationID != nil {
		destinationStationID = strings.TrimSpace(*e.DestinationStationID)
	}
	matchedTrainID := ""
	if e.MatchedTrainInstanceID != nil {
		matchedTrainID = strings.TrimSpace(*e.MatchedTrainInstanceID)
	}
	stationName := firstNonEmpty(strings.TrimSpace(e.StationName), s.stationName(ctx, e.StationID))
	destinationName := firstNonEmpty(strings.TrimSpace(e.DestinationStationName), s.stationName(ctx, destinationStationID))
	activity.Timeline = append(activity.Timeline, spacetime.TrainbotActivityEvent{
		ID:                     strings.TrimSpace(e.ID),
		Kind:                   "station_sighting",
		StableID:               spacetime.StableIDForTelegramUser(e.UserID),
		Nickname:               rider.Nickname,
		Name:                   stationSightingLabel(destinationName),
		CreatedAt:              e.CreatedAt.UTC().Format(time.RFC3339),
		StationID:              strings.TrimSpace(e.StationID),
		StationName:            stationName,
		DestinationStationID:   destinationStationID,
		DestinationStationName: destinationName,
		MatchedTrainInstanceID: matchedTrainID,
	})
	return s.client.ServicePutActivity(ctx, *activity)
}

func (s *SpacetimeStore) GetLastStationSightingByUserScope(ctx context.Context, userID int64, stationID string, destinationStationID *string) (*domain.StationSighting, error) {
	activity, err := s.findStationActivity(ctx, stationID, nil)
	if err != nil || activity == nil {
		return nil, err
	}
	stableID := spacetime.StableIDForTelegramUser(userID)
	targetDestination := ""
	if destinationStationID != nil {
		targetDestination = strings.TrimSpace(*destinationStationID)
	}
	for _, event := range sortActivityTimeline(activity.Timeline) {
		if event.Kind != "station_sighting" || event.StableID != stableID || strings.TrimSpace(event.DestinationStationID) != targetDestination {
			continue
		}
		item, err := stationSightingEventToDomain(event)
		if err != nil {
			return nil, err
		}
		item.UserID = userID
		return &item, nil
	}
	return nil, nil
}

func (s *SpacetimeStore) ListRecentStationSightings(ctx context.Context, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.listStationSightings(ctx, spacetime.ListActivitiesFilter{
		Since:     &since,
		ScopeType: "station",
	}, nil, limit)
}

func (s *SpacetimeStore) ListRecentStationSightingsByStation(ctx context.Context, stationID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.listStationSightings(ctx, spacetime.ListActivitiesFilter{
		Since:     &since,
		ScopeType: "station",
		SubjectID: strings.TrimSpace(stationID),
	}, func(item domain.StationSighting) bool {
		return item.StationID == strings.TrimSpace(stationID)
	}, limit)
}

func (s *SpacetimeStore) ListRecentStationSightingsByTrain(ctx context.Context, trainID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.listStationSightings(ctx, spacetime.ListActivitiesFilter{
		Since:     &since,
		ScopeType: "station",
	}, func(item domain.StationSighting) bool {
		return item.MatchedTrainInstanceID != nil && *item.MatchedTrainInstanceID == strings.TrimSpace(trainID)
	}, limit)
}

func (s *SpacetimeStore) UpsertIncidentVote(ctx context.Context, vote domain.IncidentVote) error {
	activity, err := s.ensureIncidentActivity(ctx, vote.IncidentID, vote.UpdatedAt)
	if err != nil {
		return err
	}
	rider, err := s.ensureRider(ctx, vote.UserID)
	if err != nil {
		return err
	}
	createdAt := vote.CreatedAt
	if createdAt.IsZero() {
		createdAt = vote.UpdatedAt
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := vote.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	activity.Votes = filterVotes(activity.Votes, spacetime.StableIDForTelegramUser(vote.UserID))
	activity.Votes = append(activity.Votes, spacetime.TrainbotActivityVote{
		StableID:  spacetime.StableIDForTelegramUser(vote.UserID),
		Nickname:  firstNonEmpty(strings.TrimSpace(vote.Nickname), rider.Nickname),
		Value:     string(vote.Value),
		CreatedAt: createdAt.UTC().Format(time.RFC3339),
		UpdatedAt: updatedAt.UTC().Format(time.RFC3339),
	})
	return s.client.ServicePutActivity(ctx, *activity)
}

func (s *SpacetimeStore) InsertIncidentVoteEvent(context.Context, domain.IncidentVoteEvent) error {
	return nil
}

func (s *SpacetimeStore) ListIncidentVotes(ctx context.Context, incidentID string) ([]domain.IncidentVote, error) {
	activity, err := s.findIncidentActivity(ctx, incidentID, nil)
	if err != nil || activity == nil {
		return nil, err
	}
	out := make([]domain.IncidentVote, 0, len(activity.Votes))
	for _, vote := range sortActivityVotes(activity.Votes) {
		item, err := voteToDomain(incidentID, vote)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SpacetimeStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]domain.IncidentVoteEvent, error) {
	activity, err := s.findIncidentActivity(ctx, incidentID, nil)
	if err != nil || activity == nil {
		return nil, err
	}
	out := make([]domain.IncidentVoteEvent, 0, len(activity.Votes))
	for _, vote := range sortActivityVotes(activity.Votes) {
		updatedAt, err := time.Parse(time.RFC3339, vote.UpdatedAt)
		if err != nil || updatedAt.Before(since.UTC()) {
			continue
		}
		item, err := voteEventToDomain(incidentID, vote)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return trimVoteEvents(out, limit), nil
}

func (s *SpacetimeStore) InsertIncidentComment(ctx context.Context, comment domain.IncidentComment) error {
	activity, err := s.ensureIncidentActivity(ctx, comment.IncidentID, comment.CreatedAt)
	if err != nil {
		return err
	}
	rider, err := s.ensureRider(ctx, comment.UserID)
	if err != nil {
		return err
	}
	activity.Comments = append(activity.Comments, spacetime.TrainbotActivityComment{
		ID:        strings.TrimSpace(comment.ID),
		StableID:  spacetime.StableIDForTelegramUser(comment.UserID),
		Nickname:  firstNonEmpty(strings.TrimSpace(comment.Nickname), rider.Nickname),
		Body:      strings.TrimSpace(comment.Body),
		CreatedAt: comment.CreatedAt.UTC().Format(time.RFC3339),
	})
	return s.client.ServicePutActivity(ctx, *activity)
}

func (s *SpacetimeStore) ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]domain.IncidentComment, error) {
	activity, err := s.findIncidentActivity(ctx, incidentID, nil)
	if err != nil || activity == nil {
		return nil, err
	}
	out := make([]domain.IncidentComment, 0, len(activity.Comments))
	for _, comment := range sortActivityComments(activity.Comments) {
		item, err := commentToDomain(incidentID, comment)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return trimComments(out, limit), nil
}

func (s *SpacetimeStore) CleanupExpired(ctx context.Context, now time.Time, retention time.Duration, loc *time.Location) (CleanupResult, error) {
	if loc == nil {
		loc = time.UTC
	}
	result, err := s.client.CleanupExpiredState(ctx, now, now.Add(-retention), now.In(loc).AddDate(0, 0, -1).Format("2006-01-02"))
	if err != nil {
		return CleanupResult{}, err
	}
	return CleanupResult{
		CheckinsDeleted:         result.CheckinsDeleted,
		SubscriptionsDeleted:    result.SubscriptionsDeleted,
		ReportsDeleted:          result.ReportsDeleted,
		StationSightingsDeleted: result.StationSightingsDeleted,
		TrainStopsDeleted:       result.TrainStopsDeleted,
		TrainsDeleted:           result.TrainsDeleted,
		FeedEventsDeleted:       result.FeedEventsDeleted,
		FeedImportsDeleted:      result.FeedImportsDeleted,
		ImportChunksDeleted:     result.ImportChunksDeleted,
	}, nil
}

func (s *SpacetimeStore) DeleteTrainDataByServiceDate(ctx context.Context, serviceDate string) (CleanupResult, error) {
	result, err := s.client.ServiceDeleteServiceDay(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return CleanupResult{}, err
	}
	return CleanupResult{
		TrainStopsDeleted: result.StopsDeleted,
		TrainsDeleted:     result.TripsDeleted,
	}, nil
}

func (s *SpacetimeStore) UpsertDailyMetric(context.Context, string, string, int64) error {
	return nil
}

func (s *SpacetimeStore) listStationSightings(ctx context.Context, filter spacetime.ListActivitiesFilter, match func(domain.StationSighting) bool, limit int) ([]domain.StationSighting, error) {
	activities, err := s.client.ServiceListActivities(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]domain.StationSighting, 0)
	for _, activity := range activities {
		for _, event := range sortActivityTimeline(activity.Timeline) {
			if event.Kind != "station_sighting" {
				continue
			}
			item, err := stationSightingEventToDomain(event)
			if err != nil {
				return nil, err
			}
			if match != nil && !match(item) {
				continue
			}
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit <= 0 || limit >= len(out) {
		return out, nil
	}
	return out[:limit], nil
}

func (s *SpacetimeStore) ensureRider(ctx context.Context, userID int64) (spacetime.TrainbotRiderRow, error) {
	if rider, err := s.loadRider(ctx, userID); err != nil {
		return spacetime.TrainbotRiderRow{}, err
	} else if rider != nil {
		return *rider, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rider := spacetime.TrainbotRiderRow{
		StableID:       spacetime.StableIDForTelegramUser(userID),
		TelegramUserID: fmt.Sprintf("%d", userID),
		Nickname:       domain.GenericNickname(userID),
		CreatedAt:      now,
		UpdatedAt:      now,
		LastSeenAt:     now,
		Settings: spacetime.TrainbotSettings{
			AlertsEnabled: true,
			AlertStyle:    string(domain.AlertStyleDetailed),
			Language:      string(domain.LanguageEN),
			UpdatedAt:     now,
		},
		Favorites:     []spacetime.TrainbotFavorite{},
		Mutes:         []spacetime.TrainbotMute{},
		Subscriptions: []spacetime.TrainbotSubscription{},
	}
	if err := s.client.ServicePutRider(ctx, rider); err != nil {
		return spacetime.TrainbotRiderRow{}, err
	}
	return rider, nil
}

func (s *SpacetimeStore) loadRider(ctx context.Context, userID int64) (*spacetime.TrainbotRiderRow, error) {
	return s.client.ServiceGetRider(ctx, spacetime.StableIDForTelegramUser(userID))
}

func (s *SpacetimeStore) ensureTrainActivity(ctx context.Context, trainID string, at time.Time) (*spacetime.TrainbotActivityRow, error) {
	if activity, err := s.findTrainActivity(ctx, trainID); err != nil {
		return nil, err
	} else if activity != nil {
		return activity, nil
	}
	trip, err := s.client.ServiceGetTrip(ctx, strings.TrimSpace(trainID))
	if err != nil {
		return nil, err
	}
	serviceDate := at.UTC().Format("2006-01-02")
	subjectName := strings.TrimSpace(trainID)
	if trip != nil {
		serviceDate = firstNonEmpty(strings.TrimSpace(trip.ServiceDate), serviceDate)
		subjectName = firstNonEmpty(strings.TrimSpace(trip.FromStationName)+" -> "+strings.TrimSpace(trip.ToStationName), subjectName)
	}
	row := &spacetime.TrainbotActivityRow{
		ID:          fmt.Sprintf("train:%s:%s", strings.TrimSpace(trainID), serviceDate),
		ScopeType:   "train",
		SubjectID:   strings.TrimSpace(trainID),
		SubjectName: subjectName,
		ServiceDate: serviceDate,
		Summary:     spacetime.TrainbotActivitySummary{},
		Timeline:    []spacetime.TrainbotActivityEvent{},
		Comments:    []spacetime.TrainbotActivityComment{},
		Votes:       []spacetime.TrainbotActivityVote{},
	}
	return row, nil
}

func (s *SpacetimeStore) findTrainActivity(ctx context.Context, trainID string) (*spacetime.TrainbotActivityRow, error) {
	trip, err := s.client.ServiceGetTrip(ctx, strings.TrimSpace(trainID))
	if err != nil || trip == nil {
		return nil, err
	}
	activities, err := s.client.ServiceListActivities(ctx, spacetime.ListActivitiesFilter{
		ScopeType:   "train",
		SubjectID:   strings.TrimSpace(trainID),
		ServiceDate: strings.TrimSpace(trip.ServiceDate),
	})
	if err != nil || len(activities) == 0 {
		return nil, err
	}
	return &activities[0], nil
}

func (s *SpacetimeStore) ensureStationActivity(ctx context.Context, stationID string, stationName string, at time.Time) (*spacetime.TrainbotActivityRow, error) {
	if activity, err := s.findStationActivity(ctx, stationID, &at); err != nil {
		return nil, err
	} else if activity != nil {
		return activity, nil
	}
	serviceDate := at.UTC().Format("2006-01-02")
	row := &spacetime.TrainbotActivityRow{
		ID:          fmt.Sprintf("station:%s:%s", strings.TrimSpace(stationID), serviceDate),
		ScopeType:   "station",
		SubjectID:   strings.TrimSpace(stationID),
		SubjectName: firstNonEmpty(strings.TrimSpace(stationName), s.stationName(ctx, stationID), strings.TrimSpace(stationID)),
		ServiceDate: serviceDate,
		Summary:     spacetime.TrainbotActivitySummary{},
		Timeline:    []spacetime.TrainbotActivityEvent{},
		Comments:    []spacetime.TrainbotActivityComment{},
		Votes:       []spacetime.TrainbotActivityVote{},
	}
	return row, nil
}

func (s *SpacetimeStore) findStationActivity(ctx context.Context, stationID string, at *time.Time) (*spacetime.TrainbotActivityRow, error) {
	filter := spacetime.ListActivitiesFilter{
		ScopeType: "station",
		SubjectID: strings.TrimSpace(stationID),
	}
	if at != nil {
		filter.ServiceDate = at.UTC().Format("2006-01-02")
	}
	activities, err := s.client.ServiceListActivities(ctx, filter)
	if err != nil || len(activities) == 0 {
		return nil, err
	}
	return &activities[0], nil
}

func (s *SpacetimeStore) ensureIncidentActivity(ctx context.Context, incidentID string, at time.Time) (*spacetime.TrainbotActivityRow, error) {
	if activity, err := s.findIncidentActivity(ctx, incidentID, &at); err != nil {
		return nil, err
	} else if activity != nil {
		return activity, nil
	}
	kind, subjectID := parseIncidentID(incidentID)
	switch kind {
	case "train":
		return s.ensureTrainActivity(ctx, subjectID, at)
	case "station":
		return s.ensureStationActivity(ctx, subjectID, s.stationName(ctx, subjectID), at)
	default:
		return nil, fmt.Errorf("unsupported incident id %q", incidentID)
	}
}

func (s *SpacetimeStore) findIncidentActivity(ctx context.Context, incidentID string, at *time.Time) (*spacetime.TrainbotActivityRow, error) {
	kind, subjectID := parseIncidentID(incidentID)
	switch kind {
	case "train":
		if activity, err := s.findTrainActivity(ctx, subjectID); err != nil || activity != nil {
			return activity, err
		}
		if at != nil {
			activities, err := s.client.ServiceListActivities(ctx, spacetime.ListActivitiesFilter{
				ScopeType:   "train",
				SubjectID:   subjectID,
				ServiceDate: at.UTC().Format("2006-01-02"),
			})
			if err != nil || len(activities) == 0 {
				return nil, err
			}
			return &activities[0], nil
		}
	case "station":
		return s.findStationActivity(ctx, subjectID, at)
	}
	return nil, nil
}

func (s *SpacetimeStore) stationName(ctx context.Context, stationID string) string {
	station, err := s.GetStationByID(ctx, stationID)
	if err != nil || station == nil {
		return ""
	}
	return station.Name
}

func tripToDomainTrain(trip spacetime.TrainbotTripRow) (domain.TrainInstance, error) {
	departureAt, err := time.Parse(time.RFC3339, strings.TrimSpace(trip.DepartureAt))
	if err != nil {
		return domain.TrainInstance{}, err
	}
	arrivalAt, err := time.Parse(time.RFC3339, strings.TrimSpace(trip.ArrivalAt))
	if err != nil {
		return domain.TrainInstance{}, err
	}
	return domain.TrainInstance{
		ID:            strings.TrimSpace(trip.ID),
		ServiceDate:   strings.TrimSpace(trip.ServiceDate),
		FromStation:   strings.TrimSpace(trip.FromStationName),
		ToStation:     strings.TrimSpace(trip.ToStationName),
		DepartureAt:   departureAt,
		ArrivalAt:     arrivalAt,
		SourceVersion: strings.TrimSpace(trip.SourceVersion),
	}, nil
}

func stationToDomain(station spacetime.TrainbotStation) domain.Station {
	return domain.Station{
		ID:            strings.TrimSpace(station.ID),
		Name:          strings.TrimSpace(station.Name),
		NormalizedKey: strings.TrimSpace(station.NormalizedKey),
		Latitude:      station.Latitude,
		Longitude:     station.Longitude,
	}
}

func stationFromStop(stop spacetime.TrainbotStop) domain.Station {
	return domain.Station{
		ID:            strings.TrimSpace(stop.StationID),
		Name:          strings.TrimSpace(stop.StationName),
		NormalizedKey: spacetimeNormalizeStationKey(stop.StationName),
		Latitude:      stop.Latitude,
		Longitude:     stop.Longitude,
	}
}

func sortStationsMap(items map[string]domain.Station) []domain.Station {
	out := make([]domain.Station, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func stopToDomain(trainID string, stop spacetime.TrainbotStop) (domain.TrainStop, error) {
	arrivalAt, err := optionalParseTime(stop.ArrivalAt)
	if err != nil {
		return domain.TrainStop{}, err
	}
	departureAt, err := optionalParseTime(stop.DepartureAt)
	if err != nil {
		return domain.TrainStop{}, err
	}
	return domain.TrainStop{
		TrainInstanceID: strings.TrimSpace(trainID),
		StationID:       strings.TrimSpace(stop.StationID),
		StationName:     strings.TrimSpace(stop.StationName),
		Seq:             stop.Seq,
		ArrivalAt:       arrivalAt,
		DepartureAt:     departureAt,
		Latitude:        stop.Latitude,
		Longitude:       stop.Longitude,
	}, nil
}

func routeStopsForTrip(trip spacetime.TrainbotTripRow, fromStationID string, toStationID string) (*spacetime.TrainbotStop, *spacetime.TrainbotStop) {
	var fromStop *spacetime.TrainbotStop
	for index := range trip.Stops {
		stop := trip.Stops[index]
		if fromStop == nil {
			if strings.TrimSpace(stop.StationID) == fromStationID {
				fromStop = &trip.Stops[index]
			}
			continue
		}
		if strings.TrimSpace(stop.StationID) == toStationID && stop.Seq > fromStop.Seq {
			return fromStop, &trip.Stops[index]
		}
	}
	return nil, nil
}

func stopPassTime(stop spacetime.TrainbotStop) (time.Time, error) {
	if trimmed := strings.TrimSpace(stop.DepartureAt); trimmed != "" {
		return time.Parse(time.RFC3339, trimmed)
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(stop.ArrivalAt))
}

func stopArrivalOrDepartureTime(stop spacetime.TrainbotStop) (time.Time, error) {
	if trimmed := strings.TrimSpace(stop.ArrivalAt); trimmed != "" {
		return time.Parse(time.RFC3339, trimmed)
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(stop.DepartureAt))
}

func riderSettingsToDomain(userID int64, rider spacetime.TrainbotRiderRow) domain.UserSettings {
	updatedAt, _ := time.Parse(time.RFC3339, strings.TrimSpace(rider.Settings.UpdatedAt))
	return domain.UserSettings{
		UserID:        userID,
		AlertsEnabled: rider.Settings.AlertsEnabled,
		AlertStyle:    spacetimeParseAlertStyle(rider.Settings.AlertStyle),
		Language:      spacetimeParseLanguage(rider.Settings.Language),
		UpdatedAt:     updatedAt,
	}
}

func favoriteToDomain(userID int64, favorite spacetime.TrainbotFavorite) (domain.FavoriteRoute, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(favorite.CreatedAt))
	if err != nil {
		return domain.FavoriteRoute{}, err
	}
	return domain.FavoriteRoute{
		UserID:          userID,
		FromStationID:   strings.TrimSpace(favorite.FromStationID),
		FromStationName: strings.TrimSpace(favorite.FromStationName),
		ToStationID:     strings.TrimSpace(favorite.ToStationID),
		ToStationName:   strings.TrimSpace(favorite.ToStationName),
		CreatedAt:       createdAt,
	}, nil
}

func reportEventToDomain(userID int64, event spacetime.TrainbotActivityEvent) (domain.ReportEvent, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(event.CreatedAt))
	if err != nil {
		return domain.ReportEvent{}, err
	}
	return domain.ReportEvent{
		ID:              strings.TrimSpace(event.ID),
		TrainInstanceID: strings.TrimSpace(event.TrainInstanceID),
		UserID:          userID,
		Signal:          domain.SignalType(strings.TrimSpace(event.Signal)),
		CreatedAt:       createdAt,
	}, nil
}

func stationSightingEventToDomain(event spacetime.TrainbotActivityEvent) (domain.StationSighting, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(event.CreatedAt))
	if err != nil {
		return domain.StationSighting{}, err
	}
	item := domain.StationSighting{
		ID:                     strings.TrimSpace(event.ID),
		StationID:              strings.TrimSpace(event.StationID),
		StationName:            strings.TrimSpace(event.StationName),
		DestinationStationName: strings.TrimSpace(event.DestinationStationName),
		CreatedAt:              createdAt,
	}
	if destinationID := strings.TrimSpace(event.DestinationStationID); destinationID != "" {
		item.DestinationStationID = &destinationID
	}
	if matchedTrainID := strings.TrimSpace(event.MatchedTrainInstanceID); matchedTrainID != "" {
		item.MatchedTrainInstanceID = &matchedTrainID
	}
	return item, nil
}

func voteToDomain(incidentID string, vote spacetime.TrainbotActivityVote) (domain.IncidentVote, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(vote.CreatedAt))
	if err != nil {
		return domain.IncidentVote{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(vote.UpdatedAt))
	if err != nil {
		return domain.IncidentVote{}, err
	}
	var userID int64
	if parsed, ok := spacetime.TelegramUserIDFromStableID(vote.StableID); ok {
		userID = parsed
	}
	return domain.IncidentVote{
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   strings.TrimSpace(vote.Nickname),
		Value:      domain.IncidentVoteValue(strings.TrimSpace(vote.Value)),
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}, nil
}

func voteEventToDomain(incidentID string, vote spacetime.TrainbotActivityVote) (domain.IncidentVoteEvent, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(vote.UpdatedAt))
	if err != nil {
		return domain.IncidentVoteEvent{}, err
	}
	var userID int64
	if parsed, ok := spacetime.TelegramUserIDFromStableID(vote.StableID); ok {
		userID = parsed
	}
	return domain.IncidentVoteEvent{
		ID:         incidentID + "|" + strings.TrimSpace(vote.StableID),
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   strings.TrimSpace(vote.Nickname),
		Value:      domain.IncidentVoteValue(strings.TrimSpace(vote.Value)),
		CreatedAt:  createdAt,
	}, nil
}

func commentToDomain(incidentID string, comment spacetime.TrainbotActivityComment) (domain.IncidentComment, error) {
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(comment.CreatedAt))
	if err != nil {
		return domain.IncidentComment{}, err
	}
	var userID int64
	if parsed, ok := spacetime.TelegramUserIDFromStableID(comment.StableID); ok {
		userID = parsed
	}
	return domain.IncidentComment{
		ID:         strings.TrimSpace(comment.ID),
		IncidentID: incidentID,
		UserID:     userID,
		Nickname:   strings.TrimSpace(comment.Nickname),
		Body:       strings.TrimSpace(comment.Body),
		CreatedAt:  createdAt,
	}, nil
}

func reportsFromActivity(activity spacetime.TrainbotActivityRow, since time.Time, limit int) ([]domain.ReportEvent, error) {
	since = since.UTC()
	out := make([]domain.ReportEvent, 0)
	for _, event := range sortActivityTimeline(activity.Timeline) {
		if event.Kind != "report" {
			continue
		}
		var userID int64
		if parsed, ok := spacetime.TelegramUserIDFromStableID(event.StableID); ok {
			userID = parsed
		}
		item, err := reportEventToDomain(userID, event)
		if err != nil {
			return nil, err
		}
		if !since.IsZero() && item.CreatedAt.Before(since) {
			continue
		}
		out = append(out, item)
	}
	return trimReports(out, limit), nil
}

func sortActivityTimeline(items []spacetime.TrainbotActivityEvent) []spacetime.TrainbotActivityEvent {
	out := append([]spacetime.TrainbotActivityEvent(nil), items...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func sortActivityVotes(items []spacetime.TrainbotActivityVote) []spacetime.TrainbotActivityVote {
	out := append([]spacetime.TrainbotActivityVote(nil), items...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func sortActivityComments(items []spacetime.TrainbotActivityComment) []spacetime.TrainbotActivityComment {
	out := append([]spacetime.TrainbotActivityComment(nil), items...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out
}

func trimReports(items []domain.ReportEvent, limit int) []domain.ReportEvent {
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func trimVoteEvents(items []domain.IncidentVoteEvent, limit int) []domain.IncidentVoteEvent {
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func trimComments(items []domain.IncidentComment, limit int) []domain.IncidentComment {
	if limit <= 0 || limit >= len(items) {
		return items
	}
	return items[:limit]
}

func filterMutes(items []spacetime.TrainbotMute, trainID string) []spacetime.TrainbotMute {
	out := make([]spacetime.TrainbotMute, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.TrainInstanceID) != trainID {
			out = append(out, item)
		}
	}
	return out
}

func filterSubscriptions(items []spacetime.TrainbotSubscription, trainID string) []spacetime.TrainbotSubscription {
	out := make([]spacetime.TrainbotSubscription, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.TrainInstanceID) != trainID {
			out = append(out, item)
		}
	}
	return out
}

func filterFavorites(items []spacetime.TrainbotFavorite, fromStationID string, toStationID string) []spacetime.TrainbotFavorite {
	out := make([]spacetime.TrainbotFavorite, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.FromStationID) == fromStationID && strings.TrimSpace(item.ToStationID) == toStationID {
			continue
		}
		out = append(out, item)
	}
	return out
}

func filterVotes(items []spacetime.TrainbotActivityVote, stableID string) []spacetime.TrainbotActivityVote {
	out := make([]spacetime.TrainbotActivityVote, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.StableID) != stableID {
			out = append(out, item)
		}
	}
	return out
}

func optionalParseTime(raw string) (*time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseIncidentID(incidentID string) (string, string) {
	parts := strings.Split(strings.TrimSpace(incidentID), ":")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

func reportSignalLabel(signal domain.SignalType) string {
	switch signal {
	case domain.SignalInspectionStarted:
		return "Inspection started"
	case domain.SignalInspectionInCar:
		return "Inspection in carriage"
	case domain.SignalInspectionEnded:
		return "Inspection ended"
	default:
		return string(signal)
	}
}

func stationSightingLabel(destination string) string {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "Platform sighting"
	}
	return "Platform sighting to " + destination
}

func spacetimeNormalizeStationKey(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(
		"ā", "a",
		"č", "c",
		"ē", "e",
		"ģ", "g",
		"ī", "i",
		"ķ", "k",
		"ļ", "l",
		"ņ", "n",
		"š", "s",
		"ū", "u",
		"ž", "z",
		"-", " ",
	)
	normalized = replacer.Replace(normalized)
	return strings.Join(strings.Fields(normalized), " ")
}

func spacetimeNormalizeStationID(value string) string {
	return strings.ReplaceAll(spacetimeNormalizeStationKey(value), " ", "-")
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	return chooseNonEmpty(values...)
}

func spacetimeParseAlertStyle(raw string) domain.AlertStyle {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case string(domain.AlertStyleDiscreet):
		return domain.AlertStyleDiscreet
	default:
		return domain.AlertStyleDetailed
	}
}

func spacetimeParseLanguage(raw string) domain.Language {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case string(domain.LanguageLV):
		return domain.LanguageLV
	default:
		return domain.LanguageEN
	}
}

func candidateServiceDates() []string {
	now := time.Now().UTC()
	return []string{
		now.Format("2006-01-02"),
		now.Add(-24 * time.Hour).Format("2006-01-02"),
	}
}

func isHotServiceDate(serviceDate string) bool {
	cleanDate := strings.TrimSpace(serviceDate)
	if cleanDate == "" {
		return true
	}
	for _, candidate := range candidateServiceDates() {
		if cleanDate == candidate {
			return true
		}
	}
	return false
}

func windowIDForRange(start, end time.Time) string {
	now := time.Now().UTC()
	start = start.UTC()
	end = end.UTC()
	if start.Before(now.Add(2*time.Minute)) && end.After(now.Add(-2*time.Minute)) && end.Sub(start) <= 40*time.Minute {
		return "now"
	}
	if !start.Before(now.Add(-2*time.Minute)) && end.Sub(start) <= 65*time.Minute {
		return "next_hour"
	}
	return "today"
}

func parseServiceUserIDs(userIDs []string) []int64 {
	out := make([]int64, 0, len(userIDs))
	for _, userID := range userIDs {
		parsed, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64)
		if err != nil || parsed <= 0 {
			continue
		}
		out = append(out, parsed)
	}
	return out
}
