package reports

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

type Service struct {
	store      store.Store
	cooldown   time.Duration
	dedupe     time.Duration
	visibility time.Duration
	vehicleMu  sync.Mutex
	areaMu     sync.Mutex
}

type SubmitOptions struct {
	Hidden           bool
	Source           model.IncidentVoteSource
	IdempotencyID    string
	IdempotencyKey   string
	IdempotencySince time.Time
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

// CommittedActionIDs returns Telegram analyzer action IDs whose report or vote
// transaction is already durable. Report transactions use the report ID for
// their incident-vote event too, so one bounded event scan covers every action
// kind without requiring a new storage procedure.
func (s *Service) CommittedActionIDs(ctx context.Context, actionIDs []string, since time.Time) (map[string]struct{}, error) {
	wanted := make(map[string]struct{}, len(actionIDs))
	for _, id := range actionIDs {
		if clean := strings.TrimSpace(id); clean != "" {
			wanted[clean] = struct{}{}
		}
	}
	committed := make(map[string]struct{}, len(wanted))
	if len(wanted) == 0 {
		return committed, nil
	}
	if since.IsZero() {
		since = time.Unix(0, 0).UTC()
	}
	events, err := s.store.ListIncidentVoteEvents(ctx, "", since.UTC(), 0)
	if err != nil {
		return nil, err
	}
	for _, event := range events {
		id := strings.TrimSpace(event.ID)
		if _, ok := wanted[id]; ok {
			committed[id] = struct{}{}
		}
	}
	return committed, nil
}

func (s *Service) SubmitStopSighting(ctx context.Context, userID int64, stopID string, now time.Time) (model.ReportResult, *model.StopSighting, error) {
	return s.SubmitStopSightingWithOptions(ctx, userID, stopID, now, SubmitOptions{})
}

func (s *Service) SubmitStopSightingWithOptions(ctx context.Context, userID int64, stopID string, now time.Time, options SubmitOptions) (model.ReportResult, *model.StopSighting, error) {
	stopID = strings.TrimSpace(stopID)
	incidentID := StopIncidentID(stopID)
	reportID, idempotent := submissionReportID("stop", userID, options.IdempotencyID, options.IdempotencyKey)
	lookupSince := submissionLookupSince(options.IdempotencySince, now)
	if idempotent {
		existing, err := s.findStopSightingByID(ctx, reportID, lookupSince)
		if err != nil {
			return model.ReportResult{}, nil, fmt.Errorf("look up idempotent stop report: %w", err)
		}
		if existing != nil {
			return reconciledStopSighting(existing), existing, nil
		}
	}
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
		ID:        reportID,
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
			if err := combined.InsertStopSightingWithVote(ctx, *item, vote, event, s.dedupe); err != nil {
				if idempotent {
					if existing, lookupErr := s.findStopSightingByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
						return reconciledStopSighting(existing), existing, nil
					}
				}
				if errors.Is(err, store.ErrDuplicateReport) {
					return model.ReportResult{Deduped: true, IncidentID: incidentID}, nil, nil
				}
				return model.ReportResult{}, nil, err
			}
		} else if err := s.store.InsertStopSighting(ctx, *item); err != nil {
			if idempotent {
				if existing, lookupErr := s.findStopSightingByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
					return reconciledStopSighting(existing), existing, nil
				}
			}
			return model.ReportResult{}, nil, err
		} else if err := s.store.RecordIncidentVote(ctx, vote, event); err != nil {
			return model.ReportResult{}, nil, err
		}
	} else if err := s.store.InsertStopSighting(ctx, *item); err != nil {
		if idempotent {
			if existing, lookupErr := s.findStopSightingByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
				return reconciledStopSighting(existing), existing, nil
			}
		}
		return model.ReportResult{}, nil, err
	}
	return model.ReportResult{Accepted: true, IncidentID: incidentID}, item, nil
}

func (s *Service) SubmitVehicleSighting(ctx context.Context, userID int64, input model.VehicleReportInput, now time.Time) (model.ReportResult, *model.VehicleSighting, error) {
	return s.SubmitVehicleSightingWithOptions(ctx, userID, input, now, SubmitOptions{})
}

func (s *Service) SubmitVehicleSightingWithOptions(ctx context.Context, userID int64, input model.VehicleReportInput, now time.Time, options SubmitOptions) (model.ReportResult, *model.VehicleSighting, error) {
	s.vehicleMu.Lock()
	defer s.vehicleMu.Unlock()

	scopeKey := VehicleScopeKey(input)
	incidentID := VehicleIncidentID(scopeKey)
	reportID, idempotent := submissionReportID("vehicle", userID, options.IdempotencyID, options.IdempotencyKey)
	lookupSince := submissionLookupSince(options.IdempotencySince, now)
	if idempotent {
		existing, err := s.findVehicleSightingByID(ctx, reportID, lookupSince)
		if err != nil {
			return model.ReportResult{}, nil, fmt.Errorf("look up idempotent vehicle report: %w", err)
		}
		if existing != nil {
			return reconciledVehicleSighting(existing), existing, nil
		}
	}
	if err := s.ensureVehiclePublicIDAvailable(ctx, scopeKey, incidentID); err != nil {
		return model.ReportResult{}, nil, err
	}
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
		ID:               reportID,
		StopID:           "",
		UserID:           userID,
		Mode:             strings.TrimSpace(input.Mode),
		RouteLabel:       strings.TrimSpace(input.RouteLabel),
		Direction:        strings.TrimSpace(input.Direction),
		Destination:      strings.TrimSpace(input.Destination),
		DepartureSeconds: input.DepartureSeconds,
		LiveRowID:        strings.TrimSpace(input.LiveRowID),
		// v1 sanitizes this supplied scope directly when deriving its public
		// incident ID. Store the opaque suffix while retaining the logical
		// private identity in the fields above.
		ScopeKey:  opaqueVehicleScopeKey(scopeKey),
		Hidden:    options.Hidden,
		CreatedAt: now.UTC(),
	}
	if !item.Hidden {
		vote, event, err := s.incidentVoteAction(ctx, incidentID, userID, model.IncidentVoteOngoing, source, item.ID, now)
		if err != nil {
			return model.ReportResult{}, nil, err
		}
		if combined, ok := s.store.(vehicleSightingVoteStore); ok {
			writeErr := combined.InsertVehicleSightingWithVote(ctx, *item, vote, event, s.dedupe)
			if writeErr != nil {
				if idempotent {
					if existing, lookupErr := s.findVehicleSightingByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
						reconciled := reconciledVehicleSighting(existing)
						reconciled.IncidentID = incidentID
						return reconciled, existing, nil
					}
				}
				if errors.Is(writeErr, store.ErrDuplicateReport) {
					return model.ReportResult{Deduped: true, IncidentID: incidentID}, nil, nil
				}
				return model.ReportResult{}, nil, writeErr
			}
		} else if err := s.store.InsertVehicleSighting(ctx, *item); err != nil {
			if idempotent {
				if existing, lookupErr := s.findVehicleSightingByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
					return reconciledVehicleSighting(existing), existing, nil
				}
			}
			return model.ReportResult{}, nil, err
		} else if err := s.store.RecordIncidentVote(ctx, vote, event); err != nil {
			return model.ReportResult{}, nil, err
		}
	} else if err := s.store.InsertVehicleSighting(ctx, *item); err != nil {
		if idempotent {
			if existing, lookupErr := s.findVehicleSightingByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
				return reconciledVehicleSighting(existing), existing, nil
			}
		}
		return model.ReportResult{}, nil, err
	}
	return model.ReportResult{Accepted: true, IncidentID: incidentID}, item, nil
}

func (s *Service) ensureVehiclePublicIDAvailable(ctx context.Context, privateScopeKey, publicIncidentID string) error {
	vehicleSightings, err := s.store.ListVehicleSightingsSince(ctx, time.Unix(0, 0).UTC(), "", 0)
	if err != nil {
		return fmt.Errorf("check vehicle incident privacy: %w", err)
	}
	privateScopeKey = strings.TrimSpace(privateScopeKey)
	for index := range vehicleSightings {
		existingScopeKey := strings.TrimSpace(vehicleSightingScopeKey(&vehicleSightings[index]))
		if VehicleIncidentID(existingScopeKey) == publicIncidentID && existingScopeKey != privateScopeKey {
			return fmt.Errorf("opaque vehicle incident collision")
		}
	}
	return nil
}

func (s *Service) SubmitAreaReport(ctx context.Context, userID int64, input model.AreaReportInput, now time.Time) (model.ReportResult, *model.AreaReport, error) {
	return s.SubmitAreaReportWithOptions(ctx, userID, input, now, SubmitOptions{})
}

func (s *Service) SubmitAreaReportWithOptions(ctx context.Context, userID int64, input model.AreaReportInput, now time.Time, options SubmitOptions) (model.ReportResult, *model.AreaReport, error) {
	s.areaMu.Lock()
	defer s.areaMu.Unlock()

	normalized, err := NormalizeAreaReportInput(input)
	if err != nil {
		return model.ReportResult{}, nil, err
	}
	scopeKey := AreaScopeKey(normalized)
	incidentID := AreaIncidentID(scopeKey)
	reportID, idempotent := submissionReportID("area", userID, options.IdempotencyID, options.IdempotencyKey)
	lookupSince := submissionLookupSince(options.IdempotencySince, now)
	if idempotent {
		existing, err := s.findAreaReportByID(ctx, reportID, lookupSince)
		if err != nil {
			return model.ReportResult{}, nil, fmt.Errorf("look up idempotent area report: %w", err)
		}
		if existing != nil {
			return reconciledAreaReport(existing), existing, nil
		}
	}
	if err := s.ensureAreaPublicIDAvailable(ctx, scopeKey, incidentID); err != nil {
		return model.ReportResult{}, nil, err
	}
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
		ID:           reportID,
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
			writeErr := combined.InsertAreaReportWithVote(ctx, *item, vote, event, s.dedupe)
			if errors.Is(writeErr, store.ErrReportVoteIncidentMismatch) {
				// The currently published 2026-04-30 Spacetime module derives
				// area incident IDs from the sanitized scope key. Store only the
				// opaque public suffix as that compatibility scope, so the legacy
				// reducer derives the same private-safe ID as newer source. The
				// first transaction validates the pair before any mutation.
				compatibilityScopeKey := opaqueAreaScopeKey(scopeKey)
				if compatibilityScopeKey != scopeKey {
					compatibilityItem := *item
					compatibilityItem.ScopeKey = compatibilityScopeKey
					writeErr = combined.InsertAreaReportWithVote(ctx, compatibilityItem, vote, event, s.dedupe)
					if writeErr == nil {
						item.ScopeKey = compatibilityScopeKey
					}
				}
			}
			if writeErr != nil {
				if idempotent {
					if existing, lookupErr := s.findAreaReportByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
						reconciled := reconciledAreaReport(existing)
						reconciled.IncidentID = incidentID
						return reconciled, existing, nil
					}
				}
				if errors.Is(writeErr, store.ErrDuplicateReport) {
					return model.ReportResult{Deduped: true, IncidentID: incidentID}, nil, nil
				}
				return model.ReportResult{}, nil, writeErr
			}
		} else if err := s.store.InsertAreaReport(ctx, *item); err != nil {
			if idempotent {
				if existing, lookupErr := s.findAreaReportByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
					return reconciledAreaReport(existing), existing, nil
				}
			}
			return model.ReportResult{}, nil, err
		} else if err := s.store.RecordIncidentVote(ctx, vote, event); err != nil {
			return model.ReportResult{}, nil, err
		}
	} else if err := s.store.InsertAreaReport(ctx, *item); err != nil {
		if idempotent {
			if existing, lookupErr := s.findAreaReportByID(ctx, reportID, lookupSince); lookupErr == nil && existing != nil {
				return reconciledAreaReport(existing), existing, nil
			}
		}
		return model.ReportResult{}, nil, err
	}
	return model.ReportResult{Accepted: true, IncidentID: incidentID}, item, nil
}

func (s *Service) ensureAreaPublicIDAvailable(ctx context.Context, logicalScopeKey, publicIncidentID string) error {
	areaReports, err := s.store.ListAreaReportsSince(ctx, time.Unix(0, 0).UTC(), 0)
	if err != nil {
		return fmt.Errorf("check area incident privacy: %w", err)
	}
	for _, item := range areaReports {
		existingScopeKey := AreaScopeKey(model.AreaReportInput{
			Latitude:     item.Latitude,
			Longitude:    item.Longitude,
			RadiusMeters: item.RadiusMeters,
			Description:  item.Description,
		})
		if AreaIncidentID(existingScopeKey) == publicIncidentID && existingScopeKey != logicalScopeKey {
			return fmt.Errorf("opaque area incident collision")
		}
	}
	return nil
}

func (s *Service) VisibleSightings(ctx context.Context, catalog *model.Catalog, stopID string, now time.Time, limit int) (model.VisibleSightings, error) {
	stopID = strings.TrimSpace(stopID)
	if publicStore, ok := s.store.(publicSightingsStore); ok {
		visible, err := publicStore.ListPublicSightings(ctx, stopID, limit)
		if err != nil {
			return model.VisibleSightings{}, err
		}
		return sanitizePublicVisibleSightings(visible), nil
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
	if clean := strings.ToLower(strings.TrimSpace(scopeKey)); isOpaqueAreaScopeKey(clean) {
		return "area:" + clean
	}
	return publicStableID("area:pub", scopeKey)
}

func opaqueVehicleScopeKey(scopeKey string) string {
	return strings.TrimPrefix(VehicleIncidentID(scopeKey), "vehicle:")
}

func isOpaqueVehicleScopeKey(scopeKey string) bool {
	clean := strings.ToLower(strings.TrimSpace(scopeKey))
	return isHashedPublicID("vehicle:"+clean, "vehicle:pub")
}

func legacyAreaIncidentID(scopeKey string) string {
	return "area:" + sanitizeIncidentKey(scopeKey)
}

func opaqueAreaScopeKey(scopeKey string) string {
	return strings.TrimPrefix(AreaIncidentID(scopeKey), "area:")
}

func isOpaqueAreaScopeKey(scopeKey string) bool {
	clean := strings.ToLower(strings.TrimSpace(scopeKey))
	if len(clean) != len("pub-")+8 || !strings.HasPrefix(clean, "pub-") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(clean, "pub-"))
	return err == nil
}

func publicAreaReportID(reportID string) string {
	if clean := strings.ToLower(strings.TrimSpace(reportID)); isHashedPublicID(clean, "area-report:pub") {
		return clean
	}
	return publicStableID("area-report:pub", reportID)
}

func publicStopSightingID(reportID string) string {
	if clean := strings.ToLower(strings.TrimSpace(reportID)); isHashedPublicID(clean, "stop-report:pub") {
		return clean
	}
	return publicStableID("stop-report:pub", reportID)
}

func publicVehicleSightingID(reportID string) string {
	if clean := strings.ToLower(strings.TrimSpace(reportID)); isHashedPublicID(clean, "vehicle-report:pub") {
		return clean
	}
	return publicStableID("vehicle-report:pub", reportID)
}

func publicStableID(prefix string, value string) string {
	clean := strings.TrimSpace(value)
	if clean == "" {
		clean = "unknown"
	}
	// Spacetime's TypeScript module applies FNV-1a to JavaScript charCodeAt
	// values. Those are UTF-16 code units, not UTF-8 bytes. Keep the service
	// hash byte-for-byte compatible so a Unicode area scope produces the same
	// incident ID in the report, vote, and vote event payloads.
	hash := uint32(2166136261)
	for _, unit := range utf16.Encode([]rune(clean)) {
		hash ^= uint32(unit)
		hash *= 16777619
	}
	return fmt.Sprintf("%s-%08x", strings.TrimSpace(prefix), hash)
}

func isHashedPublicID(value, prefix string) bool {
	clean := strings.ToLower(strings.TrimSpace(value))
	publicPrefix := strings.ToLower(strings.TrimSpace(prefix)) + "-"
	if len(clean) != len(publicPrefix)+8 || !strings.HasPrefix(clean, publicPrefix) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(clean, publicPrefix))
	return err == nil
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

func publicAreaIncidentIDFromPayload(currentID, scopeKey string, latitude, longitude float64, radiusMeters int, description string) string {
	cleanID := strings.ToLower(strings.TrimSpace(currentID))
	if strings.HasPrefix(cleanID, "area:") && isOpaqueAreaScopeKey(strings.TrimPrefix(cleanID, "area:")) {
		return cleanID
	}
	cleanScopeKey := strings.ToLower(strings.TrimSpace(scopeKey))
	if isOpaqueAreaScopeKey(cleanScopeKey) {
		return AreaIncidentID(cleanScopeKey)
	}
	if validCoordinate(latitude, -90, 90) && validCoordinate(longitude, -180, 180) {
		return AreaIncidentID(AreaScopeKey(model.AreaReportInput{
			Latitude:     latitude,
			Longitude:    longitude,
			RadiusMeters: radiusMeters,
			Description:  description,
		}))
	}
	if cleanScopeKey != "" {
		return AreaIncidentID(cleanScopeKey)
	}
	if cleanID != "" {
		return publicStableID("area:pub", cleanID)
	}
	return publicStableID("area:pub", "unknown")
}

func publicAreaIncidentIDFromContext(currentID string, area *model.IncidentAreaContext) string {
	if area == nil {
		return publicAreaIncidentIDFromPayload(currentID, "", math.NaN(), math.NaN(), 0, "")
	}
	return publicAreaIncidentIDFromPayload(
		currentID,
		area.ScopeKey,
		area.Latitude,
		area.Longitude,
		area.RadiusMeters,
		area.Description,
	)
}

func publicVehicleIncidentIDFromContext(currentID string, vehicle *model.IncidentVehicleContext) string {
	cleanID := strings.ToLower(strings.TrimSpace(currentID))
	if isHashedPublicID(cleanID, "vehicle:pub") {
		return cleanID
	}
	if vehicle != nil {
		if scopeKey := strings.TrimSpace(vehicle.ScopeKey); scopeKey != "" {
			return VehicleIncidentID(scopeKey)
		}
		return VehicleIncidentID(VehicleScopeKey(model.VehicleReportInput{
			Mode:             vehicle.Mode,
			RouteLabel:       vehicle.RouteLabel,
			Direction:        vehicle.Direction,
			Destination:      vehicle.Destination,
			DepartureSeconds: vehicle.DepartureSeconds,
			LiveRowID:        vehicle.LiveRowID,
		}))
	}
	if cleanID != "" {
		return publicStableID("vehicle:pub", cleanID)
	}
	return publicStableID("vehicle:pub", "unknown")
}

func sanitizePublicAreaReport(item model.PublicAreaReport) model.PublicAreaReport {
	item.ID = publicAreaReportID(item.ID)
	item.IncidentID = publicAreaIncidentIDFromPayload(
		item.IncidentID,
		"",
		item.Latitude,
		item.Longitude,
		item.RadiusMeters,
		item.Description,
	)
	item.Latitude = publicAreaCoordinate(item.Latitude)
	item.Longitude = publicAreaCoordinate(item.Longitude)
	item.RadiusMeters = publicAreaRadius(item.RadiusMeters)
	item.CreatedAt = publicIncidentTime(item.CreatedAt)
	return item
}

func publicAreaReport(item model.AreaReport) model.PublicAreaReport {
	return sanitizePublicAreaReport(model.PublicAreaReport{
		ID:           publicAreaReportID(item.ID),
		IncidentID:   publicAreaIncidentIDFromPayload("", item.ScopeKey, item.Latitude, item.Longitude, item.RadiusMeters, item.Description),
		Latitude:     item.Latitude,
		Longitude:    item.Longitude,
		RadiusMeters: item.RadiusMeters,
		Description:  item.Description,
		CreatedAt:    item.CreatedAt,
	})
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

func submissionReportID(kind string, userID int64, identifiers ...string) (string, bool) {
	var exactID, key string
	if len(identifiers) == 1 {
		key = identifiers[0]
	} else if len(identifiers) > 1 {
		exactID = identifiers[0]
		key = identifiers[1]
	}
	if exactID = strings.TrimSpace(exactID); exactID != "" {
		return exactID, true
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return generateID(), false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("satiksme-report-idempotency-v1\x00%s\x00%d\x00%s", strings.TrimSpace(kind), userID, key)))
	return hex.EncodeToString(sum[:12]), true
}

func submissionLookupSince(since, now time.Time) time.Time {
	since = since.UTC()
	if since.IsZero() || since.After(now.UTC()) {
		return time.Unix(0, 0).UTC()
	}
	return since
}

func reconciledStopSighting(existing *model.StopSighting) model.ReportResult {
	incidentID := ""
	if existing != nil {
		incidentID = StopIncidentID(existing.StopID)
	}
	return model.ReportResult{Accepted: true, Reconciled: true, IncidentID: incidentID}
}

func reconciledVehicleSighting(existing *model.VehicleSighting) model.ReportResult {
	incidentID := ""
	if existing != nil {
		incidentID = VehicleIncidentID(vehicleSightingScopeKey(existing))
	}
	return model.ReportResult{Accepted: true, Reconciled: true, IncidentID: incidentID}
}

func reconciledAreaReport(existing *model.AreaReport) model.ReportResult {
	incidentID := ""
	if existing != nil {
		incidentID = AreaIncidentID(areaReportScopeKey(existing))
	}
	return model.ReportResult{Accepted: true, Reconciled: true, IncidentID: incidentID}
}

func vehicleSightingScopeKey(existing *model.VehicleSighting) string {
	if existing == nil {
		return ""
	}
	// Existing v1 rows retain their exact private scope identity. New rows use
	// an opaque compatibility alias, so reconstruct only those from the private
	// fields retained alongside it.
	if scopeKey := strings.TrimSpace(existing.ScopeKey); scopeKey != "" && !isOpaqueVehicleScopeKey(scopeKey) {
		return scopeKey
	}
	return VehicleScopeKey(model.VehicleReportInput{
		Mode:             existing.Mode,
		RouteLabel:       existing.RouteLabel,
		Direction:        existing.Direction,
		Destination:      existing.Destination,
		DepartureSeconds: existing.DepartureSeconds,
		LiveRowID:        existing.LiveRowID,
	})
}

func areaReportScopeKey(existing *model.AreaReport) string {
	if existing == nil {
		return ""
	}
	if scopeKey := strings.TrimSpace(existing.ScopeKey); scopeKey != "" {
		return scopeKey
	}
	return AreaScopeKey(model.AreaReportInput{
		Latitude:     existing.Latitude,
		Longitude:    existing.Longitude,
		RadiusMeters: existing.RadiusMeters,
		Description:  existing.Description,
	})
}

func (s *Service) findStopSightingByID(ctx context.Context, id string, since time.Time) (*model.StopSighting, error) {
	items, err := s.store.ListStopSightingsSince(ctx, since, "", 0)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].ID == id {
			return &items[index], nil
		}
	}
	return nil, nil
}

func (s *Service) findVehicleSightingByID(ctx context.Context, id string, since time.Time) (*model.VehicleSighting, error) {
	items, err := s.store.ListVehicleSightingsSince(ctx, since, "", 0)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].ID == id {
			return &items[index], nil
		}
	}
	return nil, nil
}

func (s *Service) findAreaReportByID(ctx context.Context, id string, since time.Time) (*model.AreaReport, error) {
	items, err := s.store.ListAreaReportsSince(ctx, since, 0)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if items[index].ID == id {
			return &items[index], nil
		}
	}
	return nil, nil
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
			CreatedAt: publicIncidentTime(item.CreatedAt),
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
			CreatedAt:        publicIncidentTime(item.CreatedAt),
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

func sanitizePublicVisibleSightings(visible model.VisibleSightings) model.VisibleSightings {
	if visible.StopSightings != nil {
		cloned := make([]model.PublicStopSighting, len(visible.StopSightings))
		copy(cloned, visible.StopSightings)
		visible.StopSightings = cloned
	}
	if visible.VehicleSightings != nil {
		cloned := make([]model.PublicVehicleSighting, len(visible.VehicleSightings))
		copy(cloned, visible.VehicleSightings)
		visible.VehicleSightings = cloned
	}
	if visible.AreaReports != nil {
		cloned := make([]model.PublicAreaReport, len(visible.AreaReports))
		copy(cloned, visible.AreaReports)
		visible.AreaReports = cloned
	}
	for index := range visible.StopSightings {
		visible.StopSightings[index].ID = publicStopSightingID(visible.StopSightings[index].ID)
		visible.StopSightings[index].CreatedAt = publicIncidentTime(visible.StopSightings[index].CreatedAt)
	}
	for index := range visible.VehicleSightings {
		visible.VehicleSightings[index].ID = publicVehicleSightingID(visible.VehicleSightings[index].ID)
		visible.VehicleSightings[index].LiveRowID = ""
		visible.VehicleSightings[index].CreatedAt = publicIncidentTime(visible.VehicleSightings[index].CreatedAt)
	}
	for index := range visible.AreaReports {
		visible.AreaReports[index] = sanitizePublicAreaReport(visible.AreaReports[index])
	}
	return visible
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
