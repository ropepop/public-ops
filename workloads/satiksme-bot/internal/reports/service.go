package reports

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
}

type stopSightingVoteStore interface {
	InsertStopSightingWithVote(context.Context, model.StopSighting, model.IncidentVote, model.IncidentVoteEvent, time.Duration) error
}

type vehicleSightingVoteStore interface {
	InsertVehicleSightingWithVote(context.Context, model.VehicleSighting, model.IncidentVote, model.IncidentVoteEvent, time.Duration) error
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
		vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, model.IncidentVoteOngoing, model.IncidentVoteSourceMapReport, item.ID, now)
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
		vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, model.IncidentVoteOngoing, model.IncidentVoteSourceMapReport, item.ID, now)
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

func (s *Service) VisibleSightings(ctx context.Context, catalog *model.Catalog, stopID string, now time.Time, limit int) (model.VisibleSightings, error) {
	since := now.Add(-s.visibility)
	stops, err := s.store.ListStopSightingsSince(ctx, since, stopID, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	vehicles, err := s.store.ListVehicleSightingsSince(ctx, since, stopID, 0)
	if err != nil {
		return model.VisibleSightings{}, err
	}
	visible := buildVisibleSightings(catalog, stops, vehicles, func(item model.StopSighting) bool {
		return !item.Hidden
	}, func(item model.VehicleSighting) bool {
		return !item.Hidden
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
	visible := buildVisibleSightings(catalog, stops, vehicles, func(item model.StopSighting) bool {
		return item.UserID == userID
	}, func(item model.VehicleSighting) bool {
		return item.UserID == userID
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
	includeStop func(model.StopSighting) bool,
	includeVehicle func(model.VehicleSighting) bool,
) model.VisibleSightings {
	stopNames := model.StopNameLookup(catalog)
	out := model.VisibleSightings{
		StopSightings:    make([]model.PublicStopSighting, 0, len(stops)),
		VehicleSightings: make([]model.PublicVehicleSighting, 0, len(vehicles)),
	}
	for _, item := range stops {
		if includeStop != nil && !includeStop(item) {
			continue
		}
		out.StopSightings = append(out.StopSightings, model.PublicStopSighting{
			ID:        item.ID,
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
			ID:               item.ID,
			StopID:           item.StopID,
			StopName:         stopNames[item.StopID],
			Mode:             item.Mode,
			RouteLabel:       item.RouteLabel,
			Direction:        item.Direction,
			Destination:      item.Destination,
			DepartureSeconds: item.DepartureSeconds,
			LiveRowID:        item.LiveRowID,
			CreatedAt:        item.CreatedAt,
		})
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
	return visible
}
