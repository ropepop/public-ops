package reports

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/store"
)

type Service struct {
	store    store.Store
	cooldown time.Duration
	dedupe   time.Duration
}

const (
	stationSightingVisibilityWindow = 30 * time.Minute
	DefaultAreaReportRadiusMeters   = 100
	MaxLocationReportDescriptionLen = 160
)

type SubmitResult struct {
	Accepted          bool                `json:"accepted"`
	Deduped           bool                `json:"deduped"`
	CooldownRemaining time.Duration       `json:"cooldownRemaining"`
	IncidentID        string              `json:"incidentId"`
	Event             *domain.ReportEvent `json:"event,omitempty"`
}

type StationSightingSubmitResult struct {
	Accepted          bool                    `json:"accepted"`
	Deduped           bool                    `json:"deduped"`
	CooldownRemaining time.Duration           `json:"cooldownRemaining"`
	IncidentID        string                  `json:"incidentId"`
	Event             *domain.StationSighting `json:"event,omitempty"`
}

type LocationReportSubmitResult struct {
	Accepted          bool                   `json:"accepted"`
	Deduped           bool                   `json:"deduped"`
	CooldownRemaining time.Duration          `json:"cooldownRemaining"`
	IncidentID        string                 `json:"incidentId"`
	Event             *domain.LocationReport `json:"event,omitempty"`
}

type TimelineEvent struct {
	At     time.Time         `json:"at"`
	Signal domain.SignalType `json:"signal"`
	Count  int               `json:"count"`
}

func NewService(st store.Store, cooldown, dedupe time.Duration) *Service {
	return &Service{store: st, cooldown: cooldown, dedupe: dedupe}
}

func (s *Service) SubmitReport(ctx context.Context, userID int64, trainID string, signal domain.SignalType, now time.Time) (SubmitResult, error) {
	last, err := s.store.GetLastReportByUserTrain(ctx, userID, trainID)
	if err != nil {
		return SubmitResult{}, err
	}
	if last != nil {
		delta := now.Sub(last.CreatedAt)
		if last.Signal == signal && delta < s.dedupe {
			return SubmitResult{Accepted: false, Deduped: true}, nil
		}
		if delta < s.cooldown {
			return SubmitResult{Accepted: false, CooldownRemaining: s.cooldown - delta}, nil
		}
	}

	event := domain.ReportEvent{
		ID:              generateID(),
		TrainInstanceID: trainID,
		UserID:          userID,
		Signal:          signal,
		CreatedAt:       now.UTC(),
	}
	if err := s.store.InsertReportEvent(ctx, event); err != nil {
		return SubmitResult{}, err
	}
	train, err := s.store.GetTrainInstanceByID(ctx, trainID)
	if err != nil {
		return SubmitResult{}, err
	}
	stops, err := s.store.ListTrainStops(ctx, trainID)
	if err != nil {
		return SubmitResult{}, err
	}
	contextKey, _ := resolveTrainIncidentContextForTrainID(trainID, train, stops, event.CreatedAt)
	return SubmitResult{Accepted: true, IncidentID: TrainIncidentID(trainID, incidentDayKey(now), contextKey), Event: &event}, nil
}

func (s *Service) SubmitStationSighting(ctx context.Context, userID int64, stationID string, destinationStationID *string, matchedTrainID *string, now time.Time) (StationSightingSubmitResult, error) {
	last, err := s.store.GetLastStationSightingByUserScope(ctx, userID, stationID, destinationStationID)
	if err != nil {
		return StationSightingSubmitResult{}, err
	}
	if last != nil {
		delta := now.Sub(last.CreatedAt)
		if delta < s.dedupe {
			return StationSightingSubmitResult{Accepted: false, Deduped: true}, nil
		}
		if delta < s.cooldown {
			return StationSightingSubmitResult{Accepted: false, CooldownRemaining: s.cooldown - delta}, nil
		}
	}

	event := domain.StationSighting{
		ID:                     generateID(),
		StationID:              stationID,
		DestinationStationID:   trimStringPtr(destinationStationID),
		MatchedTrainInstanceID: trimStringPtr(matchedTrainID),
		UserID:                 userID,
		CreatedAt:              now.UTC(),
	}
	if err := s.store.InsertStationSighting(ctx, event); err != nil {
		return StationSightingSubmitResult{}, err
	}
	contextKey, _ := stationIncidentContext(event)
	return StationSightingSubmitResult{Accepted: true, IncidentID: StationIncidentID(stationID, incidentDayKey(now), contextKey), Event: &event}, nil
}

func (s *Service) SubmitStationReport(ctx context.Context, userID int64, station domain.Station, now time.Time) (LocationReportSubmitResult, error) {
	stationID := strings.TrimSpace(station.ID)
	if stationID == "" {
		return LocationReportSubmitResult{}, fmt.Errorf("station id is required")
	}
	subjectName := strings.TrimSpace(station.Name)
	if subjectName == "" {
		subjectName = stationID
	}
	event := domain.LocationReport{
		ID:           generateID(),
		Scope:        "station",
		SubjectID:    stationID,
		SubjectName:  subjectName,
		Latitude:     copyFloatPtr(station.Latitude),
		Longitude:    copyFloatPtr(station.Longitude),
		Description:  "Inspection here",
		UserID:       userID,
		CreatedAt:    now.UTC(),
		RadiusMeters: 0,
	}
	return s.submitLocationReport(ctx, event, now)
}

func (s *Service) SubmitAreaReport(ctx context.Context, userID int64, latitude float64, longitude float64, radiusMeters int, description string, now time.Time) (LocationReportSubmitResult, error) {
	if !validLatitude(latitude) || !validLongitude(longitude) {
		return LocationReportSubmitResult{}, fmt.Errorf("invalid coordinates")
	}
	description = normalizeLocationReportDescription(description)
	if description == "" {
		return LocationReportSubmitResult{}, fmt.Errorf("description is required")
	}
	if len([]rune(description)) > MaxLocationReportDescriptionLen {
		return LocationReportSubmitResult{}, fmt.Errorf("description is too long")
	}
	if radiusMeters <= 0 {
		radiusMeters = DefaultAreaReportRadiusMeters
	}
	if radiusMeters < 25 {
		radiusMeters = 25
	}
	if radiusMeters > 1000 {
		radiusMeters = 1000
	}
	lat := roundCoordinate(latitude)
	lng := roundCoordinate(longitude)
	subjectID := areaLocationSubjectID(lat, lng, description)
	event := domain.LocationReport{
		ID:           generateID(),
		Scope:        "area",
		SubjectID:    subjectID,
		SubjectName:  description,
		Latitude:     &lat,
		Longitude:    &lng,
		RadiusMeters: radiusMeters,
		Description:  description,
		UserID:       userID,
		CreatedAt:    now.UTC(),
	}
	return s.submitLocationReport(ctx, event, now)
}

func (s *Service) submitLocationReport(ctx context.Context, event domain.LocationReport, now time.Time) (LocationReportSubmitResult, error) {
	last, err := s.store.GetLastLocationReportByUserScope(ctx, event.UserID, event.Scope, event.SubjectID)
	if err != nil {
		return LocationReportSubmitResult{}, err
	}
	if last != nil {
		delta := now.Sub(last.CreatedAt)
		if delta < s.dedupe {
			return LocationReportSubmitResult{Accepted: false, Deduped: true}, nil
		}
		if delta < s.cooldown {
			return LocationReportSubmitResult{Accepted: false, CooldownRemaining: s.cooldown - delta}, nil
		}
	}
	if err := s.store.InsertLocationReport(ctx, event); err != nil {
		return LocationReportSubmitResult{}, err
	}
	return LocationReportSubmitResult{Accepted: true, IncidentID: LocationIncidentID(event, incidentDayKey(now)), Event: &event}, nil
}

func (s *Service) BuildStatus(ctx context.Context, trainID string, now time.Time) (domain.TrainStatus, error) {
	reports, err := s.store.ListReportsSince(ctx, trainID, now.Add(-24*time.Hour), 500)
	if err != nil {
		return domain.TrainStatus{}, err
	}
	if len(reports) == 0 {
		return domain.TrainStatus{
			State:           domain.StatusNoReports,
			Confidence:      domain.ConfidenceLow,
			UniqueReporters: 0,
		}, nil
	}

	latest := reports[0].CreatedAt
	for _, r := range reports[1:] {
		if r.CreatedAt.After(latest) {
			latest = r.CreatedAt
		}
	}

	mixed := false
	windowStart := now.Add(-10 * time.Minute)
	hasIssue := false
	hasResolved := false
	for _, r := range reports {
		if r.CreatedAt.Before(windowStart) {
			continue
		}
		if isIssueSignal(r.Signal) {
			hasIssue = true
		}
		if r.Signal == domain.SignalInspectionEnded {
			hasResolved = true
		}
	}
	mixed = hasIssue && hasResolved

	unique := make(map[int64]struct{})
	confidenceWindow := now.Add(-15 * time.Minute)
	for _, r := range reports {
		if r.CreatedAt.Before(confidenceWindow) {
			continue
		}
		unique[r.UserID] = struct{}{}
	}
	uniqueCount := len(unique)
	confidence := confidenceFrom(uniqueCount, now.Sub(latest) > 15*time.Minute)

	state := domain.StatusLastSighting
	if mixed {
		state = domain.StatusMixedReports
	}

	return domain.TrainStatus{
		State:           state,
		LastReportAt:    &latest,
		Confidence:      confidence,
		UniqueReporters: uniqueCount,
	}, nil
}

func (s *Service) RecentTimeline(ctx context.Context, trainID string, limit int) ([]TimelineEvent, error) {
	reports, err := s.store.ListRecentReports(ctx, trainID, 200)
	if err != nil {
		return nil, err
	}
	if len(reports) == 0 {
		return nil, nil
	}
	grouped := map[string]*TimelineEvent{}
	for _, r := range reports {
		bucket := r.CreatedAt.UTC().Truncate(time.Minute)
		key := fmt.Sprintf("%s|%s", bucket.Format(time.RFC3339), r.Signal)
		if existing, ok := grouped[key]; ok {
			existing.Count++
			continue
		}
		copyEvent := TimelineEvent{At: bucket, Signal: r.Signal, Count: 1}
		grouped[key] = &copyEvent
	}
	flat := make([]TimelineEvent, 0, len(grouped))
	for _, v := range grouped {
		flat = append(flat, *v)
	}
	sort.Slice(flat, func(i, j int) bool {
		return flat[i].At.After(flat[j].At)
	})
	if limit <= 0 || limit > len(flat) {
		limit = len(flat)
	}
	return flat[:limit], nil
}

func (s *Service) RecentStationSightingsByStation(ctx context.Context, stationID string, now time.Time, limit int) ([]domain.StationSighting, error) {
	return s.store.ListRecentStationSightingsByStation(ctx, stationID, now.Add(-stationSightingVisibilityWindow), limit)
}

func (s *Service) StationSightingsByStationSince(ctx context.Context, stationID string, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.store.ListRecentStationSightingsByStation(ctx, stationID, since, limit)
}

func (s *Service) RecentStationSightings(ctx context.Context, now time.Time, limit int) ([]domain.StationSighting, error) {
	return s.store.ListRecentStationSightings(ctx, now.Add(-stationSightingVisibilityWindow), limit)
}

func (s *Service) StationSightingsSince(ctx context.Context, since time.Time, limit int) ([]domain.StationSighting, error) {
	return s.store.ListRecentStationSightings(ctx, since, limit)
}

func (s *Service) RecentStationSightingsByTrain(ctx context.Context, trainID string, now time.Time, limit int) ([]domain.StationSighting, error) {
	return s.store.ListRecentStationSightingsByTrain(ctx, trainID, now.Add(-stationSightingVisibilityWindow), limit)
}

func ParseSignal(raw string) (domain.SignalType, bool) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	switch s {
	case string(domain.SignalInspectionStarted):
		return domain.SignalInspectionStarted, true
	case string(domain.SignalInspectionInCar):
		return domain.SignalInspectionInCar, true
	case string(domain.SignalInspectionEnded):
		return domain.SignalInspectionEnded, true
	default:
		return "", false
	}
}

func confidenceFrom(unique int, stale bool) domain.Confidence {
	if stale {
		return domain.ConfidenceLow
	}
	if unique >= 3 {
		return domain.ConfidenceHigh
	}
	if unique == 2 {
		return domain.ConfidenceMedium
	}
	return domain.ConfidenceLow
}

func isIssueSignal(signal domain.SignalType) bool {
	return signal == domain.SignalInspectionStarted || signal == domain.SignalInspectionInCar
}

func generateID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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

func copyFloatPtr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func validLatitude(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= -90 && value <= 90
}

func validLongitude(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= -180 && value <= 180
}

func roundCoordinate(value float64) float64 {
	return math.Round(value*100000) / 100000
}

func normalizeLocationReportDescription(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func areaLocationSubjectID(latitude float64, longitude float64, description string) string {
	latKey := int(math.Round(latitude * 100000))
	lngKey := int(math.Round(longitude * 100000))
	return fmt.Sprintf("%d,%d,%s", latKey, lngKey, sanitizeIncidentKey(description))
}
