package reports

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

type Service struct {
	store      store.Store
	cooldown   time.Duration
	dedupe     time.Duration
	visibility time.Duration
}

type SubmitOptions struct {
	Hidden bool
	Source model.IncidentVoteSource
}

type stopSightingVoteStore interface {
	InsertStopSightingWithVote(context.Context, model.StopSighting, model.IncidentVote, model.IncidentVoteEvent, time.Duration) error
}

type vehicleSightingVoteStore interface {
	InsertVehicleSightingWithVote(context.Context, model.VehicleSighting, model.IncidentVote, model.IncidentVoteEvent, time.Duration) error
}

type areaReportVoteStore interface {
	InsertAreaReportWithVote(context.Context, model.AreaReport, model.IncidentVote, model.IncidentVoteEvent, time.Duration) error
}

type publicSightingsStore interface {
	ListPublicSightings(context.Context, string, int) (model.VisibleSightings, error)
}

func NewService(st store.Store, cooldown, dedupe, visibility time.Duration) *Service {
	return &Service{
		store:      st,
		cooldown:   cooldown,
		dedupe:     dedupe,
		visibility: visibility,
	}
}

func (s *Service) HealthCheck(ctx context.Context) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("reports store unavailable")
	}
	return s.store.HealthCheck(ctx)
}

func (s *Service) SubmitStopSighting(ctx context.Context, userID int64, stopID string, now time.Time) (model.ReportResult, *model.StopSighting, error) {
	return s.SubmitStopSightingWithOptions(ctx, userID, stopID, now, SubmitOptions{})
}

func (s *Service) SubmitStopSightingWithOptions(ctx context.Context, userID int64, stopID string, now time.Time, options SubmitOptions) (model.ReportResult, *model.StopSighting, error) {
	stopID = strings.TrimSpace(stopID)
	incidentID := StopIncidentID(stopID)
	source := options.Source
	if source == "" {
		source = model.IncidentVoteSourceMapReport
	}
	if !options.Hidden {
		if result, blocked, err := s.mapReportLimitResult(ctx, userID, incidentID, now); err != nil {
			return model.ReportResult{}, nil, err
		} else if blocked {
			return result, nil, nil
		}
	}

	item := &model.StopSighting{
		ID:        generateID(),
		StopID:    stopID,
		UserID:    userID,
		Hidden:    options.Hidden,
		CreatedAt: now.UTC(),
	}
	if !item.Hidden {
		vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, model.IncidentVoteOngoing, source, item.ID, now)
		if err != nil {
			return model.ReportResult{}, nil, err
		}
		if combined, ok := s.store.(stopSightingVoteStore); ok {
			if err := combined.InsertStopSightingWithVote(ctx, *item, vote, event, s.dedupe); errors.Is(err, store.ErrDuplicateReport) {
				return model.ReportResult{Deduped: true, IncidentID: incidentID}, nil, nil
			} else if err != nil {
				return model.ReportResult{}, nil, err
			}
		} else if err := s.store.InsertStopSighting(ctx, *item); err != nil {
			return model.ReportResult{}, nil, err
		} else if err := s.store.RecordIncidentVote(ctx, vote, event); err != nil {
			return model.ReportResult{}, nil, err
		}
	} else if err := s.store.InsertStopSighting(ctx, *item); err != nil {
		return model.ReportResult{}, nil, err
	}
	return model.ReportResult{Accepted: true, IncidentID: incidentID}, item, nil
}

func (s *Service) SubmitVehicleSighting(ctx context.Context, userID int64, input model.VehicleReportInput, now time.Time) (model.ReportResult, *model.VehicleSighting, error) {
	return s.SubmitVehicleSightingWithOptions(ctx, userID, input, now, SubmitOptions{})
}

func (s *Service) SubmitVehicleSightingWithOptions(ctx context.Context, userID int64, input model.VehicleReportInput, now time.Time, options SubmitOptions) (model.ReportResult, *model.VehicleSighting, error) {
	scopeKey := VehicleScopeKey(input)
	incidentID := VehicleIncidentID(scopeKey)
	source := options.Source
	if source == "" {
		source = model.IncidentVoteSourceMapReport
	}
	if !options.Hidden {
		if result, blocked, err := s.mapReportLimitResult(ctx, userID, incidentID, now); err != nil {
			return model.ReportResult{}, nil, err
		} else if blocked {
			return result, nil, nil
		}
	}

	item := &model.VehicleSighting{
		ID:               generateID(),
		StopID:           "",
		UserID:           userID,
		Mode:             strings.TrimSpace(input.Mode),
		RouteLabel:       strings.TrimSpace(input.RouteLabel),
		Direction:        strings.TrimSpace(input.Direction),
		Destination:      strings.TrimSpace(input.Destination),
		DepartureSeconds: input.DepartureSeconds,
		LiveRowID:        strings.TrimSpace(input.LiveRowID),
		ScopeKey:         scopeKey,
		Hidden:           options.Hidden,
		CreatedAt:        now.UTC(),
	}
	if !item.Hidden {
		vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, model.IncidentVoteOngoing, source, item.ID, now)
		if err != nil {
			return model.ReportResult{}, nil, err
		}
		if combined, ok := s.store.(vehicleSightingVoteStore); ok {
			if err := combined.InsertVehicleSightingWithVote(ctx, *item, vote, event, s.dedupe); errors.Is(err, store.ErrDuplicateReport) {
				return model.ReportResult{Deduped: true, IncidentID: incidentID}, nil, nil
			} else if err != nil {
				return model.ReportResult{}, nil, err
			}
		} else if err := s.store.InsertVehicleSighting(ctx, *item); err != nil {
			return model.ReportResult{}, nil, err
		} else if err := s.store.RecordIncidentVote(ctx, vote, event); err != nil {
			return model.ReportResult{}, nil, err
		}
	} else if err := s.store.InsertVehicleSighting(ctx, *item); err != nil {
		return model.ReportResult{}, nil, err
	}
	return model.ReportResult{Accepted: true, IncidentID: incidentID}, item, nil
}

func (s *Service) SubmitAreaReport(ctx context.Context, userID int64, input model.AreaReportInput, now time.Time) (model.ReportResult, *model.AreaReport, error) {
	return s.SubmitAreaReportWithOptions(ctx, userID, input, now, SubmitOptions{})
}

func (s *Service) SubmitAreaReportWithOptions(ctx context.Context, userID int64, input model.AreaReportInput, now time.Time, options SubmitOptions) (model.ReportResult, *model.AreaReport, error) {
	normalized, err := NormalizeAreaReportInput(input)
	if err != nil {
		return model.ReportResult{}, nil, err
	}
	scopeKey := AreaScopeKey(normalized)
	incidentID := AreaIncidentID(scopeKey)
	source := options.Source
	if source == "" {
		source = model.IncidentVoteSourceMapReport
	}
	if !options.Hidden {
		if result, blocked, err := s.mapReportLimitResult(ctx, userID, incidentID, now); err != nil {
			return model.ReportResult{}, nil, err
		} else if blocked {
			return result, nil, nil
		}
	}

	item := &model.AreaReport{
		ID:           generateID(),
		UserID:       userID,
		Latitude:     normalized.Latitude,
		Longitude:    normalized.Longitude,
		RadiusMeters: normalized.RadiusMeters,
		Description:  normalized.Description,
		ScopeKey:     scopeKey,
		Hidden:       options.Hidden,
		CreatedAt:    now.UTC(),
	}
	if !item.Hidden {
		vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, model.IncidentVoteOngoing, source, item.ID, now)
		if err != nil {
			return model.ReportResult{}, nil, err
		}
		if combined, ok := s.store.(areaReportVoteStore); ok {
			if err := combined.InsertAreaReportWithVote(ctx, *item, vote, event, s.dedupe); errors.Is(err, store.ErrDuplicateReport) {
				return model.ReportResult{Deduped: true, IncidentID: incidentID}, nil, nil
			} else if err != nil {
				return model.ReportResult{}, nil, err
			}
		} else if err := s.store.InsertAreaReport(ctx, *item); err != nil {
			return model.ReportResult{}, nil, err
		} else if err := s.store.RecordIncidentVote(ctx, vote, event); err != nil {
			return model.ReportResult{}, nil, err
		}
	} else if err := s.store.InsertAreaReport(ctx, *item); err != nil {
		return model.ReportResult{}, nil, err
	}
	return model.ReportResult{Accepted: true, IncidentID: incidentID}, item, nil
}

func (s *Service) VisibleSightings(ctx context.Context, catalog *model.Catalog, stopID string, now time.Time, limit int) (model.VisibleSightings, error) {
	stopID = strings.TrimSpace(stopID)
	if publicStore, ok := s.store.(publicSightingsStore); ok {
		return publicStore.ListPublicSightings(ctx, stopID, limit)
	}
	since := now.Add(-s.visibility)
	stops, err := s.store.ListStopSightingsSince(ctx, since, stopID, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	vehicles, err := s.store.ListVehicleSightingsSince(ctx, since, stopID, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	areaReports, err := s.store.ListAreaReportsSince(ctx, since, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	visible := buildVisibleSightings(catalog, stops, vehicles, areaReports, func(item model.StopSighting) bool {
		return !item.Hidden
	}, func(item model.VehicleSighting) bool {
		return !item.Hidden
	}, func(item model.AreaReport) bool {
		return !item.Hidden && strings.TrimSpace(stopID) == ""
	})
	return trimVisibleSightings(visible, limit), nil
}

func (s *Service) UserSightings(ctx context.Context, catalog *model.Catalog, userID int64, stopID string, now time.Time, limit int) (model.VisibleSightings, error) {
	since := now.Add(-s.visibility)
	stops, err := s.store.ListStopSightingsSince(ctx, since, stopID, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	vehicles, err := s.store.ListVehicleSightingsSince(ctx, since, stopID, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	areaReports, err := s.store.ListAreaReportsSince(ctx, since, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	visible := buildVisibleSightings(catalog, stops, vehicles, areaReports, func(item model.StopSighting) bool {
		return item.UserID == userID
	}, func(item model.VehicleSighting) bool {
		return item.UserID == userID
	}, func(item model.AreaReport) bool {
		return item.UserID == userID && strings.TrimSpace(stopID) == ""
	})
	return trimVisibleSightings(visible, limit), nil
}

func VehicleScopeKey(input model.VehicleReportInput) string {
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	routeLabel := strings.TrimSpace(input.RouteLabel)
	direction := strings.TrimSpace(input.Direction)
	destination := strings.ToLower(strings.TrimSpace(input.Destination))
	if liveRowID := strings.TrimSpace(input.LiveRowID); liveRowID != "" {
		return fmt.Sprintf("live:%s:%s:%s:%s", mode, routeLabel, direction, liveRowID)
	}
	return fmt.Sprintf("fallback:%s:%s:%s:%s", mode, routeLabel, direction, destination)
}

const (
	defaultAreaRadiusMeters = 100
	maxAreaRadiusMeters     = 500
	publicAreaRadiusMeters  = 250
)

func NormalizeAreaReportInput(input model.AreaReportInput) (model.AreaReportInput, error) {
	description := strings.Join(strings.Fields(strings.TrimSpace(input.Description)), " ")
	if description == "" {
		return model.AreaReportInput{}, fmt.Errorf("description is required")
	}
	if len([]rune(description)) > 160 {
		return model.AreaReportInput{}, fmt.Errorf("description is too long")
	}
	if !validCoordinate(input.Latitude, -90, 90) || !validCoordinate(input.Longitude, -180, 180) {
		return model.AreaReportInput{}, fmt.Errorf("invalid coordinates")
	}
	radius := input.RadiusMeters
	if radius <= 0 {
		radius = defaultAreaRadiusMeters
	}
	if radius > maxAreaRadiusMeters {
		radius = maxAreaRadiusMeters
	}
	return model.AreaReportInput{
		Latitude:     roundCoordinate(input.Latitude),
		Longitude:    roundCoordinate(input.Longitude),
		RadiusMeters: radius,
		Description:  description,
	}, nil
}

func AreaScopeKey(input model.AreaReportInput) string {
	normalized, err := NormalizeAreaReportInput(input)
	if err != nil {
		normalized = input
	}
	latCell := int(math.Round(normalized.Latitude * 1000))
	lonCell := int(math.Round(normalized.Longitude * 1000))
	radius := normalized.RadiusMeters
	if radius <= 0 {
		radius = defaultAreaRadiusMeters
	}
	if radius > maxAreaRadiusMeters {
		radius = maxAreaRadiusMeters
	}
	slug := trimIncidentKey(sanitizeIncidentKey(normalized.Description), 48)
	return fmt.Sprintf("%d:%d:%d:%s", latCell, lonCell, radius, slug)
}

func AreaIncidentID(scopeKey string) string {
	return publicStableID("area:pub", scopeKey)
}

func publicAreaReportID(reportID string) string {
	return publicStableID("area-report:pub", reportID)
}

func publicStopSightingID(reportID string) string {
	return publicStableID("stop-report:pub", reportID)
}

func publicVehicleSightingID(reportID string) string {
	return publicStableID("vehicle-report:pub", reportID)
}

func publicStableID(prefix string, value string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		clean = "unknown"
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(clean))
	return fmt.Sprintf("%s-%08x", strings.TrimSpace(prefix), hash.Sum32())
}

func validCoordinate(value, minValue, maxValue float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= minValue && value <= maxValue
}

func roundCoordinate(value float64) float64 {
	return math.Round(value*100000) / 100000
}

func publicAreaCoordinate(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func publicAreaRadius(radius int) int {
	if radius <= 0 {
		radius = defaultAreaRadiusMeters
	}
	if radius > maxAreaRadiusMeters {
		radius = maxAreaRadiusMeters
	}
	if radius < publicAreaRadiusMeters {
		return publicAreaRadiusMeters
	}
	return radius
}

func publicAreaContext(area *model.IncidentAreaContext) *model.IncidentAreaContext {
	if area == nil {
		return nil
	}
	out := *area
	out.ScopeKey = ""
	out.Latitude = publicAreaCoordinate(area.Latitude)
	out.Longitude = publicAreaCoordinate(area.Longitude)
	out.RadiusMeters = publicAreaRadius(area.RadiusMeters)
	return &out
}

func publicAreaReport(item model.AreaReport) model.PublicAreaReport {
	return model.PublicAreaReport{
		ID:           publicAreaReportID(item.ID),
		IncidentID:   AreaIncidentID(item.ScopeKey),
		Latitude:     publicAreaCoordinate(item.Latitude),
		Longitude:    publicAreaCoordinate(item.Longitude),
		RadiusMeters: publicAreaRadius(item.RadiusMeters),
		Description:  item.Description,
		CreatedAt:    item.CreatedAt,
	}
}

func trimIncidentKey(value string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return "area"
	}
	if maxRunes > 0 && len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	out := strings.Trim(string(runes), "-")
	if out == "" {
		return "area"
	}
	return out
}

func generateID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("evt-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (s *Service) mapReportLimitResult(ctx context.Context, userID int64, incidentID string, now time.Time) (model.ReportResult, bool, error) {
	current, err := s.currentIncidentVote(ctx, incidentID, userID)
	if err != nil {
		return model.ReportResult{}, false, err
	}
	if current != nil && current.Value == model.IncidentVoteOngoing {
		delta := now.Sub(current.UpdatedAt)
		if delta < s.dedupe {
			return model.ReportResult{}, false, nil
		}
		if delta < sameVoteWindow {
			rateErr := &RateLimitError{Reason: "same_vote", Remaining: sameVoteWindow - delta}
			return reportRateLimitResult(rateErr, incidentID), true, nil
		}
	}
	count, err := s.store.CountMapReportsByUserSince(ctx, userID, now.Add(-mapReportWindow))
	if err != nil {
		return model.ReportResult{}, false, err
	}
	if count >= mapReportLimit {
		rateErr := &RateLimitError{Reason: "map_report_limit", Remaining: mapReportWindow}
		return reportRateLimitResult(rateErr, incidentID), true, nil
	}
	return model.ReportResult{}, false, nil
}

func reportRateLimitResult(err *RateLimitError, incidentID string) model.ReportResult {
	seconds := int(err.Remaining.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return model.ReportResult{
		RateLimited:       true,
		Reason:            err.Reason,
		CooldownRemaining: err.Remaining,
		CooldownSeconds:   seconds,
		IncidentID:        incidentID,
	}
}

func buildVisibleSightings(
	catalog *model.Catalog,
	stops []model.StopSighting,
	vehicles []model.VehicleSighting,
	areaReports []model.AreaReport,
	includeStop func(model.StopSighting) bool,
	includeVehicle func(model.VehicleSighting) bool,
	includeArea func(model.AreaReport) bool,
) model.VisibleSightings {
	stopNames := model.StopNameLookup(catalog)
	out := model.VisibleSightings{
		StopSightings:    make([]model.PublicStopSighting, 0, len(stops)),
		VehicleSightings: make([]model.PublicVehicleSighting, 0, len(vehicles)),
		AreaReports:      make([]model.PublicAreaReport, 0, len(areaReports)),
	}
	for _, item := range stops {
		if includeStop != nil && !includeStop(item) {
			continue
		}
		out.StopSightings = append(out.StopSightings, model.PublicStopSighting{
			ID:        publicStopSightingID(item.ID),
			StopID:    item.StopID,
			StopName:  stopNames[item.StopID],
			CreatedAt: item.CreatedAt,
		})
	}
	for _, item := range vehicles {
		if includeVehicle != nil && !includeVehicle(item) {
			continue
		}
		out.VehicleSightings = append(out.VehicleSightings, model.PublicVehicleSighting{
			ID:               publicVehicleSightingID(item.ID),
			StopID:           item.StopID,
			StopName:         stopNames[item.StopID],
			Mode:             item.Mode,
			RouteLabel:       item.RouteLabel,
			Direction:        item.Direction,
			Destination:      item.Destination,
			DepartureSeconds: item.DepartureSeconds,
			CreatedAt:        item.CreatedAt,
		})
	}
	for _, item := range areaReports {
		if includeArea != nil && !includeArea(item) {
			continue
		}
		out.AreaReports = append(out.AreaReports, publicAreaReport(item))
	}
	return out
}

func trimVisibleSightings(visible model.VisibleSightings, limit int) model.VisibleSightings {
	if limit > 0 && len(visible.StopSightings) > limit {
		visible.StopSightings = visible.StopSightings[:limit]
	}
	if limit > 0 && len(visible.VehicleSightings) > limit {
		visible.VehicleSightings = visible.VehicleSightings[:limit]
	}
	if limit > 0 && len(visible.AreaReports) > limit {
		visible.AreaReports = visible.AreaReports[:limit]
	}
	return visible
}
