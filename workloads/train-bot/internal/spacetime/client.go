package spacetime

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	trainActivityActiveWindow   = 15 * time.Minute
	stationActivityActiveWindow = 30 * time.Minute
	trainbotDBPrefix            = "trainbot_"
)

var ErrLiveSchemaOutdated = errors.New("spacetime live schema outdated")

func canonicalProcedureName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	if strings.HasPrefix(clean, trainbotDBPrefix) {
		return clean
	}
	return trainbotDBPrefix + clean
}

func canonicalReducerName(name string) string {
	return canonicalProcedureName(name)
}

func missingProcedureResponse(statusCode int, responseBody []byte) bool {
	if statusCode == http.StatusNotFound {
		return true
	}
	message := strings.ToLower(strings.TrimSpace(string(responseBody)))
	if message == "" {
		return false
	}
	return strings.Contains(message, "nonexistent reducer") ||
		strings.Contains(message, "nonexistent procedure") ||
		strings.Contains(message, "unknown reducer") ||
		strings.Contains(message, "unknown procedure")
}

func isRequiredLiveSchemaProcedure(name string) bool {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return false
	}
	clean = strings.TrimPrefix(clean, trainbotDBPrefix)
	switch clean {
	case "cleanup_expired_state",
		"begin_service_day_import",
		"append_service_day_chunk",
		"commit_service_day_import",
		"abort_service_day_import",
		"upsert_rider_batch",
		"upsert_activity_batch":
		return true
	default:
		return strings.HasPrefix(clean, "service_")
	}
}

func isRequiredLiveSchemaReducer(name string) bool {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return false
	}
	clean = strings.TrimPrefix(clean, trainbotDBPrefix)
	switch clean {
	case "cleanup_expired_state",
		"upsert_rider_batch",
		"upsert_activity_batch":
		return true
	default:
		return strings.HasPrefix(clean, "service_")
	}
}

func liveSchemaOutdatedError(kind string, name string) error {
	item := strings.TrimSpace(name)
	if item == "" {
		item = "unknown"
	}
	label := strings.TrimSpace(kind)
	if label == "" {
		label = "procedure"
	}
	return fmt.Errorf(
		"%w: live SpacetimeDB module is missing required %s %s; publish the train-bot Spacetime schema before running the runtime",
		ErrLiveSchemaOutdated,
		label,
		item,
	)
}

type ScheduleSnapshot struct {
	Stations []ScheduleStation `json:"stations"`
	Trains   []ScheduleTrain   `json:"trains"`
	Stops    []ScheduleStop    `json:"stops"`
}

type ScheduleStation struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	NormalizedKey string   `json:"normalizedKey"`
	Latitude      *float64 `json:"latitude,omitempty"`
	Longitude     *float64 `json:"longitude,omitempty"`
}

type ScheduleTrain struct {
	ID            string `json:"id"`
	ServiceDate   string `json:"serviceDate"`
	FromStation   string `json:"fromStation"`
	ToStation     string `json:"toStation"`
	DepartureAt   string `json:"departureAt"`
	ArrivalAt     string `json:"arrivalAt"`
	SourceVersion string `json:"sourceVersion"`
}

type ScheduleStop struct {
	TrainInstanceID string   `json:"trainInstanceId"`
	StationID       string   `json:"stationId"`
	StationName     string   `json:"stationName"`
	Seq             int      `json:"seq"`
	ArrivalAt       string   `json:"arrivalAt,omitempty"`
	DepartureAt     string   `json:"departureAt,omitempty"`
	Latitude        *float64 `json:"latitude,omitempty"`
	Longitude       *float64 `json:"longitude,omitempty"`
}

type ScheduleTripBatchItem struct {
	ID            string         `json:"id"`
	ServiceDate   string         `json:"serviceDate"`
	FromStation   string         `json:"fromStation"`
	ToStation     string         `json:"toStation"`
	DepartureAt   string         `json:"departureAt"`
	ArrivalAt     string         `json:"arrivalAt"`
	SourceVersion string         `json:"sourceVersion"`
	Stops         []ScheduleStop `json:"stops"`
}

type CleanupExpiredStateResult struct {
	CheckinsDeleted         int64 `json:"checkinsDeleted"`
	SubscriptionsDeleted    int64 `json:"subscriptionsDeleted"`
	ReportsDeleted          int64 `json:"reportsDeleted"`
	StationSightingsDeleted int64 `json:"stationSightingsDeleted"`
	TrainStopsDeleted       int64 `json:"trainStopsDeleted"`
	TrainsDeleted           int64 `json:"trainsDeleted"`
	FeedEventsDeleted       int64 `json:"feedEventsDeleted"`
	FeedImportsDeleted      int64 `json:"feedImportsDeleted"`
	ImportChunksDeleted     int64 `json:"importChunksDeleted"`
}

type UserSnapshot struct {
	Profiles           []ProfileRow           `json:"profiles"`
	Settings           []UserSettingsRow      `json:"settings"`
	Favorites          []FavoriteRouteRow     `json:"favorites"`
	ActiveCheckins     []ActiveCheckinRow     `json:"activeCheckins"`
	UndoCheckouts      []UndoCheckoutRow      `json:"undoCheckouts"`
	TrainMutes         []TrainMuteRow         `json:"trainMutes"`
	Subscriptions      []SubscriptionRow      `json:"subscriptions"`
	Reports            []ReportEventRow       `json:"reports"`
	StationSightings   []StationSightingRow   `json:"stationSightings"`
	IncidentVotes      []IncidentVoteRow      `json:"incidentVotes"`
	IncidentVoteEvents []IncidentVoteEventRow `json:"incidentVoteEvents"`
	IncidentComments   []IncidentCommentRow   `json:"incidentComments"`
	DailyMetrics       []DailyMetricRow       `json:"dailyMetrics"`
}

type ProfileRow struct {
	StableID       string `json:"stableId"`
	TelegramUserID string `json:"telegramUserId"`
	Nickname       string `json:"nickname"`
	GivenName      string `json:"givenName"`
	Language       string `json:"language"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	LastSeenAt     string `json:"lastSeenAt"`
}

type UserSettingsRow struct {
	StableID      string `json:"stableId"`
	AlertsEnabled bool   `json:"alertsEnabled"`
	AlertStyle    string `json:"alertStyle"`
	Language      string `json:"language"`
	UpdatedAt     string `json:"updatedAt"`
}

type FavoriteRouteRow struct {
	StableID        string `json:"stableId"`
	FromStationID   string `json:"fromStationId"`
	FromStationName string `json:"fromStationName"`
	ToStationID     string `json:"toStationId"`
	ToStationName   string `json:"toStationName"`
	CreatedAt       string `json:"createdAt"`
}

type ActiveCheckinRow struct {
	StableID          string `json:"stableId"`
	TrainInstanceID   string `json:"trainInstanceId"`
	BoardingStationID string `json:"boardingStationId,omitempty"`
	CheckedInAt       string `json:"checkedInAt"`
	AutoCheckoutAt    string `json:"autoCheckoutAt"`
}

type UndoCheckoutRow struct {
	StableID          string `json:"stableId"`
	TrainInstanceID   string `json:"trainInstanceId"`
	BoardingStationID string `json:"boardingStationId,omitempty"`
	CheckedInAt       string `json:"checkedInAt"`
	AutoCheckoutAt    string `json:"autoCheckoutAt"`
	ExpiresAt         string `json:"expiresAt"`
}

type TrainMuteRow struct {
	StableID        string `json:"stableId"`
	TrainInstanceID string `json:"trainInstanceId"`
	MutedUntil      string `json:"mutedUntil"`
	CreatedAt       string `json:"createdAt"`
}

type SubscriptionRow struct {
	StableID        string `json:"stableId"`
	TrainInstanceID string `json:"trainInstanceId"`
	ExpiresAt       string `json:"expiresAt"`
	IsActive        bool   `json:"isActive"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type ReportEventRow struct {
	ID              string `json:"id"`
	StableID        string `json:"stableId"`
	TrainInstanceID string `json:"trainInstanceId"`
	Signal          string `json:"signal"`
	CreatedAt       string `json:"createdAt"`
}

type StationSightingRow struct {
	ID                     string `json:"id"`
	StableID               string `json:"stableId"`
	StationID              string `json:"stationId"`
	StationName            string `json:"stationName"`
	DestinationStationID   string `json:"destinationStationId,omitempty"`
	DestinationStationName string `json:"destinationStationName"`
	MatchedTrainInstanceID string `json:"matchedTrainInstanceId,omitempty"`
	CreatedAt              string `json:"createdAt"`
}

type IncidentVoteRow struct {
	ID         string `json:"id"`
	IncidentID string `json:"incidentId"`
	StableID   string `json:"stableId"`
	Nickname   string `json:"nickname"`
	Value      string `json:"value"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type IncidentVoteEventRow struct {
	ID         string `json:"id"`
	IncidentID string `json:"incidentId"`
	StableID   string `json:"stableId"`
	Nickname   string `json:"nickname"`
	Value      string `json:"value"`
	CreatedAt  string `json:"createdAt"`
}

type IncidentCommentRow struct {
	ID         string `json:"id"`
	IncidentID string `json:"incidentId"`
	StableID   string `json:"stableId"`
	Nickname   string `json:"nickname"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
}

type DailyMetricRow struct {
	MetricDate string `json:"metricDate"`
	Key        string `json:"key"`
	Value      int64  `json:"value"`
}

type TrainbotStation struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	NormalizedKey string   `json:"normalizedKey"`
	Latitude      *float64 `json:"latitude,omitempty"`
	Longitude     *float64 `json:"longitude,omitempty"`
}

type TrainbotStop struct {
	StationID   string   `json:"stationId"`
	StationName string   `json:"stationName"`
	Seq         int      `json:"seq"`
	ArrivalAt   string   `json:"arrivalAt,omitempty"`
	DepartureAt string   `json:"departureAt,omitempty"`
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
}

type TrainbotTripRow struct {
	ID              string         `json:"id"`
	ServiceDate     string         `json:"serviceDate"`
	FromStationID   string         `json:"fromStationId"`
	FromStationName string         `json:"fromStationName"`
	ToStationID     string         `json:"toStationId"`
	ToStationName   string         `json:"toStationName"`
	DepartureAt     string         `json:"departureAt"`
	ArrivalAt       string         `json:"arrivalAt"`
	SourceVersion   string         `json:"sourceVersion"`
	Stops           []TrainbotStop `json:"stops"`
}

type TrainbotServiceDayRow struct {
	ServiceDate   string            `json:"serviceDate"`
	SourceVersion string            `json:"sourceVersion"`
	ImportedAt    string            `json:"importedAt"`
	Stations      []TrainbotStation `json:"stations"`
}

type trainbotTripStopRow struct {
	TrainID     string   `json:"trainId"`
	StationID   string   `json:"stationId"`
	StationName string   `json:"stationName"`
	Seq         int      `json:"seq"`
	ArrivalAt   string   `json:"arrivalAt,omitempty"`
	DepartureAt string   `json:"departureAt,omitempty"`
	Latitude    *float64 `json:"latitude,omitempty"`
	Longitude   *float64 `json:"longitude,omitempty"`
}

type TrainbotSettings struct {
	AlertsEnabled bool   `json:"alertsEnabled"`
	AlertStyle    string `json:"alertStyle"`
	Language      string `json:"language"`
	UpdatedAt     string `json:"updatedAt"`
}

type TrainbotFavorite struct {
	FromStationID   string `json:"fromStationId"`
	FromStationName string `json:"fromStationName"`
	ToStationID     string `json:"toStationId"`
	ToStationName   string `json:"toStationName"`
	CreatedAt       string `json:"createdAt"`
}

type TrainbotRideState struct {
	TrainInstanceID   string `json:"trainInstanceId"`
	BoardingStationID string `json:"boardingStationId"`
	CheckedInAt       string `json:"checkedInAt"`
	AutoCheckoutAt    string `json:"autoCheckoutAt"`
}

type TrainbotUndoRideState struct {
	TrainInstanceID   string `json:"trainInstanceId"`
	BoardingStationID string `json:"boardingStationId"`
	CheckedInAt       string `json:"checkedInAt"`
	AutoCheckoutAt    string `json:"autoCheckoutAt"`
	ExpiresAt         string `json:"expiresAt"`
}

type TrainbotMute struct {
	TrainInstanceID string `json:"trainInstanceId"`
	MutedUntil      string `json:"mutedUntil"`
	CreatedAt       string `json:"createdAt"`
}

type TrainbotSubscription struct {
	TrainInstanceID string `json:"trainInstanceId"`
	ExpiresAt       string `json:"expiresAt"`
	IsActive        bool   `json:"isActive"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type TrainbotRecentActionState struct {
	UpdatedAt string `json:"updatedAt"`
}

type TrainbotRiderRow struct {
	StableID          string                     `json:"stableId"`
	TelegramUserID    string                     `json:"telegramUserId"`
	Nickname          string                     `json:"nickname"`
	CreatedAt         string                     `json:"createdAt"`
	UpdatedAt         string                     `json:"updatedAt"`
	LastSeenAt        string                     `json:"lastSeenAt"`
	Settings          TrainbotSettings           `json:"settings"`
	Favorites         []TrainbotFavorite         `json:"favorites"`
	CurrentRide       *TrainbotRideState         `json:"currentRide,omitempty"`
	UndoRide          *TrainbotUndoRideState     `json:"undoRide,omitempty"`
	Mutes             []TrainbotMute             `json:"mutes"`
	Subscriptions     []TrainbotSubscription     `json:"subscriptions"`
	RecentActionState *TrainbotRecentActionState `json:"recentActionState,omitempty"`
}

type TrainbotActivitySummary struct {
	LastReportName    string `json:"lastReportName"`
	LastReportAt      string `json:"lastReportAt"`
	LastActivityName  string `json:"lastActivityName"`
	LastActivityAt    string `json:"lastActivityAt"`
	LastActivityActor string `json:"lastActivityActor"`
	LastReporter      string `json:"lastReporter"`
}

type TrainbotActivityEvent struct {
	ID                     string `json:"id"`
	Kind                   string `json:"kind"`
	StableID               string `json:"stableId"`
	Nickname               string `json:"nickname"`
	Name                   string `json:"name"`
	Detail                 string `json:"detail"`
	CreatedAt              string `json:"createdAt"`
	Signal                 string `json:"signal"`
	TrainInstanceID        string `json:"trainInstanceId"`
	StationID              string `json:"stationId"`
	StationName            string `json:"stationName"`
	DestinationStationID   string `json:"destinationStationId"`
	DestinationStationName string `json:"destinationStationName"`
	MatchedTrainInstanceID string `json:"matchedTrainInstanceId"`
}

type TrainbotActivityComment struct {
	ID        string `json:"id"`
	StableID  string `json:"stableId"`
	Nickname  string `json:"nickname"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

type TrainbotActivityVote struct {
	StableID  string `json:"stableId"`
	Nickname  string `json:"nickname"`
	Value     string `json:"value"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type TrainbotActivityRow struct {
	ID             string                    `json:"id"`
	ScopeType      string                    `json:"scopeType"`
	SubjectID      string                    `json:"subjectId"`
	SubjectName    string                    `json:"subjectName"`
	ServiceDate    string                    `json:"serviceDate"`
	Active         bool                      `json:"active"`
	LastActivityAt string                    `json:"lastActivityAt"`
	Summary        TrainbotActivitySummary   `json:"summary"`
	Timeline       []TrainbotActivityEvent   `json:"timeline"`
	Comments       []TrainbotActivityComment `json:"comments"`
	Votes          []TrainbotActivityVote    `json:"votes"`
}

type SQLStatementStats struct {
	RowsInserted int64 `json:"rows_inserted"`
	RowsDeleted  int64 `json:"rows_deleted"`
	RowsUpdated  int64 `json:"rows_updated"`
}

type SQLStatementResult struct {
	Schema              map[string]any    `json:"schema"`
	Rows                [][]any           `json:"rows"`
	TotalDurationMicros int64             `json:"total_duration_micros"`
	Stats               SQLStatementStats `json:"stats"`
}

type SyncConfig struct {
	Host              string
	Database          string
	Issuer            string
	Audience          string
	JWTPrivateKeyFile string
	ServiceSubject    string
	ServiceRoles      []string
	TokenTTL          time.Duration
	HTTPTimeout       time.Duration
}

type Syncer struct {
	baseURL  string
	database string
	client   *http.Client
	issuer   *serviceTokenIssuer

	missingRequiredProcedures sync.Map
}

type ListActivitiesFilter struct {
	Since       *time.Time
	ScopeType   string
	SubjectID   string
	ServiceDate string
}

type ServiceDeleteServiceDayResult struct {
	ServiceDate       string `json:"serviceDate"`
	TripsDeleted      int64  `json:"tripsDeleted"`
	StopsDeleted      int64  `json:"stopsDeleted"`
	ServiceDayDeleted int64  `json:"serviceDayDeleted"`
}

type ActiveBundleMetadata struct {
	Version       string `json:"version"`
	ServiceDate   string `json:"serviceDate"`
	GeneratedAt   string `json:"generatedAt"`
	SourceVersion string `json:"sourceVersion"`
}

type ServiceStationWindowTrainRow struct {
	Train       TrainbotTripRow `json:"train"`
	StationID   string          `json:"stationId"`
	StationName string          `json:"stationName"`
	PassAt      string          `json:"passAt"`
}

type ServiceRouteWindowTrainRow struct {
	Train           TrainbotTripRow `json:"train"`
	FromStationID   string          `json:"fromStationId"`
	FromStationName string          `json:"fromStationName"`
	ToStationID     string          `json:"toStationId"`
	ToStationName   string          `json:"toStationName"`
	FromPassAt      string          `json:"fromPassAt"`
	ToPassAt        string          `json:"toPassAt"`
}

type serviceTokenIssuer struct {
	issuer   string
	audience string
	subject  string
	roles    []string
	tokenTTL time.Duration
	keyID    string

	privateKey *rsa.PrivateKey
}

type TokenOptions struct {
	Subject string
	Roles   []string
	Claims  map[string]any
}

func NewSyncer(cfg SyncConfig) (*Syncer, error) {
	host := strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	if host == "" {
		return nil, fmt.Errorf("spacetime host is required")
	}
	database := strings.TrimSpace(cfg.Database)
	if database == "" {
		return nil, fmt.Errorf("spacetime database is required")
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if cfg.TokenTTL <= 0 {
		cfg.TokenTTL = 5 * time.Minute
	}
	privateKey, err := loadRSAPrivateKey(cfg.JWTPrivateKeyFile)
	if err != nil {
		return nil, err
	}
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		issuer = "train-bot-runtime"
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		audience = "spacetimedb"
	}
	subject := strings.TrimSpace(cfg.ServiceSubject)
	if subject == "" {
		subject = "service:train-bot"
	}
	roles := cfg.ServiceRoles
	if len(roles) == 0 {
		roles = []string{"train_service"}
	}
	return &Syncer{
		baseURL:  host,
		database: database,
		client:   &http.Client{Timeout: cfg.HTTPTimeout},
		issuer: &serviceTokenIssuer{
			issuer:     issuer,
			audience:   audience,
			subject:    subject,
			roles:      append([]string(nil), roles...),
			tokenTTL:   cfg.TokenTTL,
			keyID:      keyIDForPublicKey(&privateKey.PublicKey),
			privateKey: privateKey,
		},
	}, nil
}

func (s *Syncer) SyncScheduleSnapshot(ctx context.Context, snapshot ScheduleSnapshot) error {
	return s.SyncScheduleSnapshotWithImportID(ctx, "schedule-"+randomTokenID(), snapshot)
}

func (s *Syncer) SyncScheduleSnapshotWithImportID(ctx context.Context, importID string, snapshot ScheduleSnapshot) error {
	importID = strings.TrimSpace(importID)
	if importID == "" {
		importID = "schedule-" + randomTokenID()
	}

	byServiceDate := map[string]ScheduleSnapshot{}
	stationByID := map[string]ScheduleStation{}
	trainServiceDates := map[string]string{}
	for _, station := range snapshot.Stations {
		if id := strings.TrimSpace(station.ID); id != "" {
			stationByID[id] = station
		}
	}
	for _, train := range snapshot.Trains {
		serviceDate := strings.TrimSpace(train.ServiceDate)
		trainID := strings.TrimSpace(train.ID)
		if serviceDate == "" || trainID == "" {
			continue
		}
		group := byServiceDate[serviceDate]
		group.Trains = append(group.Trains, train)
		byServiceDate[serviceDate] = group
		trainServiceDates[trainID] = serviceDate
	}
	for _, stop := range snapshot.Stops {
		trainID := strings.TrimSpace(stop.TrainInstanceID)
		serviceDate := trainServiceDates[trainID]
		if serviceDate == "" || trainID == "" {
			continue
		}
		group := byServiceDate[serviceDate]
		group.Stops = append(group.Stops, stop)
		byServiceDate[serviceDate] = group
		stationID := strings.TrimSpace(stop.StationID)
		if stationID == "" {
			continue
		}
		if _, ok := stationByID[stationID]; ok {
			continue
		}
		stationByID[stationID] = ScheduleStation{
			ID:            stationID,
			Name:          strings.TrimSpace(stop.StationName),
			NormalizedKey: normalizeStationKey(stop.StationName),
			Latitude:      stop.Latitude,
			Longitude:     stop.Longitude,
		}
	}

	for serviceDate, group := range byServiceDate {
		stationSet := map[string]struct{}{}
		for _, stop := range group.Stops {
			if stationID := strings.TrimSpace(stop.StationID); stationID != "" {
				stationSet[stationID] = struct{}{}
			}
		}
		group.Stations = make([]ScheduleStation, 0, len(stationSet))
		for stationID := range stationSet {
			group.Stations = append(group.Stations, stationByID[stationID])
		}
		if err := s.syncSingleServiceDate(ctx, importID+"-"+serviceDate, serviceDate, group); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) SyncUserSnapshot(ctx context.Context, snapshot UserSnapshot, schedule ScheduleSnapshot) error {
	riders, activities := transformUserSnapshot(snapshot, schedule)
	for _, batch := range batchTrainbotRiders(riders, 100) {
		if _, err := s.CallReducer(ctx, "upsert_rider_batch", []any{mustJSON(batch)}); err != nil {
			return err
		}
	}
	for _, batch := range batchTrainbotActivities(activities, 100) {
		if _, err := s.CallReducer(ctx, "upsert_activity_batch", []any{mustJSON(batch)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) CleanupExpiredState(ctx context.Context, now time.Time, retentionCutoff time.Time, oldestKeptServiceDate string) (CleanupExpiredStateResult, error) {
	args := []any{
		now.UTC().Format(time.RFC3339),
		retentionCutoff.UTC().Format(time.RFC3339),
		strings.TrimSpace(oldestKeptServiceDate),
	}
	if _, err := s.CallReducer(ctx, "cleanup_expired_state", args); err != nil {
		return CleanupExpiredStateResult{}, err
	}
	return CleanupExpiredStateResult{}, nil
}

func (s *Syncer) ServiceSchedulePresent(ctx context.Context, serviceDate string) (bool, error) {
	cleanDate := strings.TrimSpace(serviceDate)
	results, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT serviceDate FROM trainbot_service_day WHERE serviceDate = %s LIMIT 1",
		sqlQuote(cleanDate),
	))
	if err != nil {
		return false, err
	}
	rows, err := sqlRows(results)
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

func (s *Syncer) ServiceGetSchedule(ctx context.Context, serviceDate string) (*TrainbotServiceDayRow, []TrainbotTripRow, error) {
	cleanDate := strings.TrimSpace(serviceDate)
	payload, err := s.CallProcedure(ctx, "service_get_schedule", []any{cleanDate})
	if err == nil {
		var raw struct {
			ServiceDay *TrainbotServiceDayRow `json:"serviceDay"`
			Trips      []TrainbotTripRow      `json:"trips"`
		}
		if err := decodeInto(payload, &raw); err != nil {
			return nil, nil, err
		}
		return raw.ServiceDay, raw.Trips, nil
	}
	if !errors.Is(err, ErrLiveSchemaOutdated) {
		return nil, nil, err
	}
	serviceDayResults, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT serviceDate, sourceVersion, importedAt FROM trainbot_service_day WHERE serviceDate = %s LIMIT 1",
		sqlQuote(cleanDate),
	))
	if err != nil {
		return nil, nil, err
	}
	serviceDayRows := make([]TrainbotServiceDayRow, 0, 1)
	serviceDayRowsRaw, err := sqlRows(serviceDayResults)
	if err != nil {
		return nil, nil, err
	}
	if err := decodeSQLRowsInto(serviceDayRowsRaw, &serviceDayRows); err != nil {
		return nil, nil, err
	}
	tripResults, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT id, serviceDate, fromStationId, fromStationName, toStationId, toStationName, departureAt, arrivalAt, sourceVersion FROM trainbot_trip_public WHERE serviceDate = %s ORDER BY departureAt ASC, id ASC",
		sqlQuote(cleanDate),
	))
	if err != nil {
		return nil, nil, err
	}
	tripRows := make([]TrainbotTripRow, 0)
	tripRowsRaw, err := sqlRows(tripResults)
	if err != nil {
		return nil, nil, err
	}
	if err := decodeSQLRowsInto(tripRowsRaw, &tripRows); err != nil {
		return nil, nil, err
	}
	stopResults, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT trainId, stationId, stationName, seq, arrivalAt, departureAt, latitude, longitude FROM trainbot_trip_stop WHERE serviceDate = %s ORDER BY trainId ASC, seq ASC, stationId ASC",
		sqlQuote(cleanDate),
	))
	if err != nil {
		return nil, nil, err
	}
	stopRows := make([]trainbotTripStopRow, 0)
	stopRowsRaw, err := sqlRows(stopResults)
	if err != nil {
		return nil, nil, err
	}
	if err := decodeSQLRowsInto(stopRowsRaw, &stopRows); err != nil {
		return nil, nil, err
	}
	attachStopsToTrips(tripRows, stopRows)
	stations := stationsFromTripRows(tripRows)
	if len(serviceDayRows) == 0 {
		if len(tripRows) == 0 {
			return nil, nil, nil
		}
		return &TrainbotServiceDayRow{
			ServiceDate:   cleanDate,
			SourceVersion: firstTripSourceVersion(tripRows),
			Stations:      stations,
		}, tripRows, nil
	}
	serviceDayRows[0].Stations = stations
	return &serviceDayRows[0], tripRows, nil
}

func attachStopsToTrips(trips []TrainbotTripRow, stopRows []trainbotTripStopRow) {
	if len(trips) == 0 {
		return
	}
	byID := make(map[string]*TrainbotTripRow, len(trips))
	for index := range trips {
		trips[index].Stops = nil
		byID[strings.TrimSpace(trips[index].ID)] = &trips[index]
	}
	for _, stop := range stopRows {
		trip := byID[strings.TrimSpace(stop.TrainID)]
		if trip == nil {
			continue
		}
		trip.Stops = append(trip.Stops, TrainbotStop{
			StationID:   strings.TrimSpace(stop.StationID),
			StationName: strings.TrimSpace(stop.StationName),
			Seq:         stop.Seq,
			ArrivalAt:   strings.TrimSpace(stop.ArrivalAt),
			DepartureAt: strings.TrimSpace(stop.DepartureAt),
			Latitude:    stop.Latitude,
			Longitude:   stop.Longitude,
		})
	}
}

func stationsFromTripRows(trips []TrainbotTripRow) []TrainbotStation {
	byID := make(map[string]TrainbotStation)
	record := func(id string, name string, latitude *float64, longitude *float64) {
		cleanID := strings.TrimSpace(id)
		cleanName := strings.TrimSpace(name)
		if cleanID == "" || cleanName == "" {
			return
		}
		next := byID[cleanID]
		next.ID = cleanID
		if strings.TrimSpace(next.Name) == "" {
			next.Name = cleanName
		}
		if strings.TrimSpace(next.NormalizedKey) == "" {
			next.NormalizedKey = normalizeStationKey(cleanName)
		}
		if latitude != nil {
			next.Latitude = latitude
		}
		if longitude != nil {
			next.Longitude = longitude
		}
		byID[cleanID] = next
	}

	for _, trip := range trips {
		record(trip.FromStationID, trip.FromStationName, nil, nil)
		record(trip.ToStationID, trip.ToStationName, nil, nil)
		for _, stop := range trip.Stops {
			record(stop.StationID, stop.StationName, stop.Latitude, stop.Longitude)
		}
	}

	out := make([]TrainbotStation, 0, len(byID))
	for _, station := range byID {
		out = append(out, station)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func firstTripSourceVersion(trips []TrainbotTripRow) string {
	for _, trip := range trips {
		if clean := strings.TrimSpace(trip.SourceVersion); clean != "" {
			return clean
		}
	}
	return ""
}

func (s *Syncer) ServiceGetTrip(ctx context.Context, trainID string) (*TrainbotTripRow, error) {
	results, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT id, serviceDate, fromStationId, fromStationName, toStationId, toStationName, departureAt, arrivalAt, sourceVersion FROM trainbot_trip_public WHERE id = %s LIMIT 1",
		sqlQuote(trainID),
	))
	if err != nil {
		return nil, err
	}
	rows, err := sqlRows(results)
	if err != nil {
		return nil, err
	}
	items := make([]TrainbotTripRow, 0, 1)
	if err := decodeSQLRowsInto(rows, &items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	stopResults, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT trainId, stationId, stationName, seq, arrivalAt, departureAt, latitude, longitude FROM trainbot_trip_stop WHERE trainId = %s ORDER BY seq ASC, stationId ASC",
		sqlQuote(trainID),
	))
	if err != nil {
		return nil, err
	}
	stopRows := make([]trainbotTripStopRow, 0)
	stopRowsRaw, err := sqlRows(stopResults)
	if err != nil {
		return nil, err
	}
	if err := decodeSQLRowsInto(stopRowsRaw, &stopRows); err != nil {
		return nil, err
	}
	attachStopsToTrips(items, stopRows)
	return &items[0], nil
}

func (s *Syncer) ServiceGetRider(ctx context.Context, stableID string) (*TrainbotRiderRow, error) {
	results, err := s.SQL(ctx, fmt.Sprintf(
		"SELECT * FROM trainbot_rider WHERE stableId = %s LIMIT 1",
		sqlQuote(stableID),
	))
	if err != nil {
		return nil, err
	}
	rows, err := sqlRows(results)
	if err != nil {
		return nil, err
	}
	items := make([]TrainbotRiderRow, 0, 1)
	if err := decodeSQLRowsInto(rows, &items); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	return &items[0], nil
}

func (s *Syncer) ServiceListRiders(ctx context.Context) ([]TrainbotRiderRow, error) {
	results, err := s.SQL(ctx, "SELECT * FROM trainbot_rider ORDER BY stableId ASC")
	if err != nil {
		return nil, err
	}
	rows, err := sqlRows(results)
	if err != nil {
		return nil, err
	}
	items := make([]TrainbotRiderRow, 0, len(rows))
	if err := decodeSQLRowsInto(rows, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Syncer) ServiceListActivities(ctx context.Context, filter ListActivitiesFilter) ([]TrainbotActivityRow, error) {
	sinceISO := ""
	if filter.Since != nil {
		sinceISO = filter.Since.UTC().Format(time.RFC3339)
	}
	payload, err := s.CallProcedure(ctx, "service_list_activities", []any{
		sinceISO,
		strings.TrimSpace(filter.ScopeType),
		strings.TrimSpace(filter.SubjectID),
		strings.TrimSpace(filter.ServiceDate),
	})
	if err == nil {
		var raw struct {
			Activities []TrainbotActivityRow `json:"activities"`
		}
		if err := decodeInto(payload, &raw); err != nil {
			return nil, err
		}
		for index := range raw.Activities {
			normalizeActivity(&raw.Activities[index])
		}
		return raw.Activities, nil
	}
	if !errors.Is(err, ErrLiveSchemaOutdated) {
		return nil, err
	}
	clauses := make([]string, 0, 4)
	if filter.Since != nil {
		clauses = append(clauses, fmt.Sprintf("lastActivityAt >= %s", sqlQuote(filter.Since.UTC().Format(time.RFC3339))))
	}
	if scopeType := strings.TrimSpace(filter.ScopeType); scopeType != "" {
		clauses = append(clauses, fmt.Sprintf("scopeType = %s", sqlQuote(scopeType)))
	}
	if subjectID := strings.TrimSpace(filter.SubjectID); subjectID != "" {
		clauses = append(clauses, fmt.Sprintf("subjectId = %s", sqlQuote(subjectID)))
	}
	if serviceDate := strings.TrimSpace(filter.ServiceDate); serviceDate != "" {
		clauses = append(clauses, fmt.Sprintf("serviceDate = %s", sqlQuote(serviceDate)))
	}
	query := "SELECT * FROM trainbot_activity"
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY lastActivityAt DESC, id ASC"
	results, err := s.SQL(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := sqlRows(results)
	if err != nil {
		return nil, err
	}
	items := make([]TrainbotActivityRow, 0, len(rows))
	if err := decodeSQLRowsInto(rows, &items); err != nil {
		return nil, err
	}
	for index := range items {
		normalizeActivity(&items[index])
	}
	return items, nil
}

func (s *Syncer) ServiceListWindowTrains(ctx context.Context, windowID string) ([]TrainbotTripRow, error) {
	payload, err := s.CallProcedure(ctx, "service_list_window_trains", []any{strings.TrimSpace(windowID)})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Trips []TrainbotTripRow `json:"trips"`
	}
	if err := decodeInto(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Trips, nil
}

func (s *Syncer) ServiceListStationWindowTrains(ctx context.Context, stationID string, start, end time.Time) ([]ServiceStationWindowTrainRow, error) {
	payload, err := s.CallProcedure(ctx, "service_list_station_window_trains", []any{
		strings.TrimSpace(stationID),
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Trains []ServiceStationWindowTrainRow `json:"trains"`
	}
	if err := decodeInto(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Trains, nil
}

func (s *Syncer) ServiceListRouteWindowTrains(ctx context.Context, originStationID string, destinationStationID string, start, end time.Time) ([]ServiceRouteWindowTrainRow, error) {
	payload, err := s.CallProcedure(ctx, "service_list_route_window_trains", []any{
		strings.TrimSpace(originStationID),
		strings.TrimSpace(destinationStationID),
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Trains []ServiceRouteWindowTrainRow `json:"trains"`
	}
	if err := decodeInto(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Trains, nil
}

func (s *Syncer) ServiceListActiveCheckinUsers(ctx context.Context, trainID string, now time.Time) ([]string, error) {
	riders, err := s.ServiceListRiders(ctx)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	trainID = strings.TrimSpace(trainID)
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rider := range riders {
		if rider.CurrentRide == nil || strings.TrimSpace(rider.CurrentRide.TrainInstanceID) != trainID {
			continue
		}
		autoCheckoutAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(rider.CurrentRide.AutoCheckoutAt))
		if parseErr != nil || autoCheckoutAt.Before(now) {
			continue
		}
		userID := strings.TrimSpace(rider.TelegramUserID)
		if userID == "" && strings.HasPrefix(strings.TrimSpace(rider.StableID), "telegram:") {
			userID = strings.TrimSpace(strings.TrimPrefix(rider.StableID, "telegram:"))
		}
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Syncer) ServiceListActiveSubscriptionUsers(ctx context.Context, trainID string, now time.Time) ([]string, error) {
	riders, err := s.ServiceListRiders(ctx)
	if err != nil {
		return nil, err
	}
	now = now.UTC()
	trainID = strings.TrimSpace(trainID)
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rider := range riders {
		hasActive := false
		for _, subscription := range rider.Subscriptions {
			if strings.TrimSpace(subscription.TrainInstanceID) != trainID || subscription.IsActive == false {
				continue
			}
			expiresAt, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(subscription.ExpiresAt))
			if parseErr != nil || expiresAt.Before(now) {
				continue
			}
			hasActive = true
			break
		}
		if !hasActive {
			continue
		}
		userID := strings.TrimSpace(rider.TelegramUserID)
		if userID == "" && strings.HasPrefix(strings.TrimSpace(rider.StableID), "telegram:") {
			userID = strings.TrimSpace(strings.TrimPrefix(rider.StableID, "telegram:"))
		}
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Syncer) ServicePutRider(ctx context.Context, rider TrainbotRiderRow) error {
	_, err := s.CallReducer(ctx, "service_put_rider", []any{mustJSON(rider)})
	return err
}

func (s *Syncer) ServicePutActivity(ctx context.Context, activity TrainbotActivityRow) error {
	_, err := s.CallReducer(ctx, "service_put_activity", []any{mustJSON(activity)})
	return err
}

func (s *Syncer) ServiceResetTestRider(ctx context.Context, stableID string) error {
	_, err := s.CallReducer(ctx, "service_reset_test_rider", []any{strings.TrimSpace(stableID)})
	return err
}

func (s *Syncer) ServiceConsumeTestLoginTicket(ctx context.Context, nonceHash string, stableID string, expiresAt time.Time) error {
	_, err := s.CallReducer(ctx, "service_consume_test_login_ticket", []any{
		strings.TrimSpace(nonceHash),
		strings.TrimSpace(stableID),
		expiresAt.UTC().Format(time.RFC3339),
	})
	return err
}

func (s *Syncer) ServiceDeleteServiceDay(ctx context.Context, serviceDate string) (ServiceDeleteServiceDayResult, error) {
	serviceDay, trips, err := s.ServiceGetSchedule(ctx, strings.TrimSpace(serviceDate))
	if err != nil {
		return ServiceDeleteServiceDayResult{}, err
	}
	stopsDeleted := int64(0)
	for _, trip := range trips {
		stopsDeleted += int64(len(trip.Stops))
	}
	if _, err := s.CallReducer(ctx, "service_delete_service_day", []any{strings.TrimSpace(serviceDate)}); err != nil {
		return ServiceDeleteServiceDayResult{}, err
	}
	return ServiceDeleteServiceDayResult{
		ServiceDate:       strings.TrimSpace(serviceDate),
		TripsDeleted:      int64(len(trips)),
		StopsDeleted:      stopsDeleted,
		ServiceDayDeleted: boolToInt64(serviceDay != nil),
	}, nil
}

func (s *Syncer) ServiceReplaceScheduleBatch(ctx context.Context, serviceDate string, sourceVersion string, stations []ScheduleStation, trips []ScheduleTripBatchItem, reset bool, finalize bool) error {
	_, err := s.CallReducer(ctx, "service_replace_schedule_batch", []any{
		strings.TrimSpace(serviceDate),
		strings.TrimSpace(sourceVersion),
		mustJSON(stations),
		mustJSON(trips),
		reset,
		finalize,
	})
	return err
}

func (s *Syncer) PublishActiveBundle(ctx context.Context, version string, serviceDate string, generatedAt time.Time, sourceVersion string) error {
	_, err := s.CallReducer(ctx, "service_set_active_bundle", []any{
		strings.TrimSpace(version),
		strings.TrimSpace(serviceDate),
		generatedAt.UTC().Format(time.RFC3339),
		strings.TrimSpace(sourceVersion),
	})
	return err
}

func (s *Syncer) PublishRuntimeConfig(ctx context.Context, scheduleCutoffHour int) error {
	if scheduleCutoffHour < 0 || scheduleCutoffHour > 23 {
		scheduleCutoffHour = 3
	}
	_, err := s.CallReducer(ctx, "service_set_runtime_config", []any{scheduleCutoffHour})
	return err
}

func (s *Syncer) CallReducer(ctx context.Context, name string, args []any) (any, error) {
	token, err := s.IssueToken(time.Now().UTC(), TokenOptions{})
	if err != nil {
		return nil, err
	}
	return s.callJSONReducerWithToken(ctx, name, args, token)
}

func (s *Syncer) CallReducerWithToken(ctx context.Context, name string, args []any, token string) (any, error) {
	return s.callJSONReducerWithToken(ctx, name, args, token)
}

func (s *Syncer) CallProcedure(ctx context.Context, name string, args []any) (any, error) {
	token, err := s.IssueToken(time.Now().UTC(), TokenOptions{})
	if err != nil {
		return nil, err
	}
	return s.callJSONProcedureWithToken(ctx, name, args, token)
}

func (s *Syncer) CallProcedureWithToken(ctx context.Context, name string, args []any, token string) (any, error) {
	return s.callJSONProcedureWithToken(ctx, name, args, token)
}

func (s *Syncer) SQL(ctx context.Context, query string) ([]SQLStatementResult, error) {
	token, err := s.IssueToken(time.Now().UTC(), TokenOptions{})
	if err != nil {
		return nil, err
	}
	return s.SQLWithToken(ctx, query, token)
}

func (s *Syncer) SQLWithToken(ctx context.Context, query string, token string) ([]SQLStatementResult, error) {
	requestURL := fmt.Sprintf("%s/v1/database/%s/sql", s.baseURL, url.PathEscape(s.database))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(strings.TrimSpace(query)))
	if err != nil {
		return nil, fmt.Errorf("build spacetime sql request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call spacetime sql: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spacetime sql response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spacetime sql failed: %s", strings.TrimSpace(string(body)))
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload []SQLStatementResult
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode spacetime sql response: %w", err)
	}
	return payload, nil
}

func (s *Syncer) IssueToken(now time.Time, opts TokenOptions) (string, error) {
	return s.issuer.issueWith(now, opts)
}

func (s *Syncer) callJSONReducerWithToken(ctx context.Context, name string, args []any, token string) (any, error) {
	canonicalName := canonicalReducerName(name)
	if canonicalName == "" {
		return nil, fmt.Errorf("spacetime reducer name is required")
	}
	required := isRequiredLiveSchemaReducer(canonicalName)
	if required {
		if _, cached := s.missingRequiredProcedures.Load("reducer:" + canonicalName); cached {
			return nil, liveSchemaOutdatedError("reducer", canonicalName)
		}
	}
	payload, err, missing := s.callJSONModuleActionExactWithToken(ctx, "reducer", canonicalName, args, token)
	if err == nil {
		return payload, nil
	}
	if required && missing {
		s.missingRequiredProcedures.Store("reducer:"+canonicalName, struct{}{})
		return nil, liveSchemaOutdatedError("reducer", canonicalName)
	}
	return nil, err
}

func (s *Syncer) callJSONProcedureWithToken(ctx context.Context, name string, args []any, token string) (any, error) {
	canonicalName := canonicalProcedureName(name)
	if canonicalName == "" {
		return nil, fmt.Errorf("spacetime procedure name is required")
	}
	required := isRequiredLiveSchemaProcedure(canonicalName)
	if required {
		if _, cached := s.missingRequiredProcedures.Load("procedure:" + canonicalName); cached {
			return nil, liveSchemaOutdatedError("procedure", canonicalName)
		}
	}
	payload, err, missing := s.callJSONModuleActionExactWithToken(ctx, "procedure", canonicalName, args, token)
	if err == nil {
		return payload, nil
	}
	if required && missing {
		s.missingRequiredProcedures.Store("procedure:"+canonicalName, struct{}{})
		return nil, liveSchemaOutdatedError("procedure", canonicalName)
	}
	return nil, err
}

func (s *Syncer) callJSONModuleActionExactWithToken(ctx context.Context, kind string, name string, args []any, token string) (any, error, bool) {
	name = strings.TrimSpace(name)
	body, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal spacetime args: %w", err), false
	}
	requestURL := fmt.Sprintf("%s/v1/database/%s/call/%s", s.baseURL, url.PathEscape(s.database), url.PathEscape(strings.TrimSpace(name)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build spacetime request: %w", err), false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call spacetime %s %s: %w", kind, name, err), false
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read spacetime %s response %s: %w", kind, name, err), false
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("spacetime %s %s failed: %s", kind, name, strings.TrimSpace(string(responseBody))), missingProcedureResponse(resp.StatusCode, responseBody)
	}
	if len(bytes.TrimSpace(responseBody)) == 0 {
		return nil, nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, nil, false
	}
	if text, ok := payload.(string); ok {
		if err := validateProcedureJSON(text); err != nil {
			return nil, err, false
		}
		var nested any
		nestedDecoder := json.NewDecoder(strings.NewReader(text))
		nestedDecoder.UseNumber()
		if err := nestedDecoder.Decode(&nested); err == nil {
			return nested, nil, false
		}
	}
	return payload, nil, false
}

func (s *Syncer) syncSingleServiceDate(ctx context.Context, importID string, serviceDate string, snapshot ScheduleSnapshot) error {
	sourceVersion := ""
	for _, train := range snapshot.Trains {
		if trimmed := strings.TrimSpace(train.SourceVersion); trimmed != "" {
			sourceVersion = trimmed
			break
		}
	}
	if _, err := s.CallProcedure(ctx, "begin_service_day_import", []any{importID, strings.TrimSpace(serviceDate), sourceVersion}); err != nil {
		return err
	}
	aborted := false
	abortImport := func(cause error) error {
		if aborted {
			return cause
		}
		aborted = true
		if _, abortErr := s.CallProcedure(ctx, "abort_service_day_import", []any{importID}); abortErr != nil {
			return fmt.Errorf("%w (abort failed: %v)", cause, abortErr)
		}
		return cause
	}
	for _, batch := range batchScheduleStations(snapshot.Stations, 250) {
		if _, err := s.CallProcedure(ctx, "append_service_day_chunk", []any{importID, "stations", mustJSON(batch)}); err != nil {
			return abortImport(err)
		}
	}
	for _, batch := range batchScheduleTrains(snapshot.Trains, 250) {
		if _, err := s.CallProcedure(ctx, "append_service_day_chunk", []any{importID, "trips", mustJSON(batch)}); err != nil {
			return abortImport(err)
		}
	}
	for _, batch := range batchScheduleStops(snapshot.Stops, 1000) {
		if _, err := s.CallProcedure(ctx, "append_service_day_chunk", []any{importID, "stops", mustJSON(batch)}); err != nil {
			return abortImport(err)
		}
	}
	if _, err := s.CallProcedure(ctx, "commit_service_day_import", []any{importID}); err != nil {
		return abortImport(err)
	}
	return nil
}

func batchScheduleStations(items []ScheduleStation, batchSize int) [][]ScheduleStation {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]ScheduleStation, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func batchScheduleTrains(items []ScheduleTrain, batchSize int) [][]ScheduleTrain {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]ScheduleTrain, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func batchScheduleStops(items []ScheduleStop, batchSize int) [][]ScheduleStop {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]ScheduleStop, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func batchTrainbotRiders(items []TrainbotRiderRow, batchSize int) [][]TrainbotRiderRow {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]TrainbotRiderRow, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func batchTrainbotActivities(items []TrainbotActivityRow, batchSize int) [][]TrainbotActivityRow {
	if batchSize <= 0 || len(items) == 0 {
		return nil
	}
	out := make([][]TrainbotActivityRow, 0, (len(items)+batchSize-1)/batchSize)
	for start := 0; start < len(items); start += batchSize {
		end := start + batchSize
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[start:end])
	}
	return out
}

func transformUserSnapshot(snapshot UserSnapshot, schedule ScheduleSnapshot) ([]TrainbotRiderRow, []TrainbotActivityRow) {
	trainServiceDates := map[string]string{}
	trainNames := map[string]string{}
	stationNames := map[string]string{}
	for _, station := range schedule.Stations {
		if id := strings.TrimSpace(station.ID); id != "" {
			stationNames[id] = strings.TrimSpace(station.Name)
		}
	}
	for _, train := range schedule.Trains {
		trainID := strings.TrimSpace(train.ID)
		if trainID == "" {
			continue
		}
		trainServiceDates[trainID] = strings.TrimSpace(train.ServiceDate)
		trainNames[trainID] = strings.TrimSpace(train.FromStation) + " -> " + strings.TrimSpace(train.ToStation)
	}

	riders := map[string]*TrainbotRiderRow{}
	activities := map[string]*TrainbotActivityRow{}

	ensureRider := func(stableID string, nickname string) *TrainbotRiderRow {
		stableID = strings.TrimSpace(stableID)
		if stableID == "" {
			return nil
		}
		if existing := riders[stableID]; existing != nil {
			if strings.TrimSpace(existing.Nickname) == "" && strings.TrimSpace(nickname) != "" {
				existing.Nickname = strings.TrimSpace(nickname)
			}
			return existing
		}
		row := &TrainbotRiderRow{
			StableID:       stableID,
			TelegramUserID: telegramUserIDForStableID(stableID),
			Nickname:       strings.TrimSpace(nickname),
			Settings: TrainbotSettings{
				AlertsEnabled: true,
				AlertStyle:    "DETAILED",
				Language:      "EN",
			},
			Favorites:     []TrainbotFavorite{},
			Mutes:         []TrainbotMute{},
			Subscriptions: []TrainbotSubscription{},
		}
		if row.Nickname == "" {
			row.Nickname = genericNickname(stableID)
		}
		riders[stableID] = row
		return row
	}

	ensureTrainActivity := func(trainID string, at string) *TrainbotActivityRow {
		trainID = strings.TrimSpace(trainID)
		if trainID == "" {
			return nil
		}
		serviceDate := strings.TrimSpace(trainServiceDates[trainID])
		if serviceDate == "" {
			serviceDate = datePart(at)
		}
		id := fmt.Sprintf("train:%s:%s", trainID, serviceDate)
		if existing := activities[id]; existing != nil {
			return existing
		}
		row := &TrainbotActivityRow{
			ID:          id,
			ScopeType:   "train",
			SubjectID:   trainID,
			SubjectName: strings.TrimSpace(trainNames[trainID]),
			ServiceDate: serviceDate,
			Summary:     TrainbotActivitySummary{},
			Timeline:    []TrainbotActivityEvent{},
			Comments:    []TrainbotActivityComment{},
			Votes:       []TrainbotActivityVote{},
		}
		if row.SubjectName == "" {
			row.SubjectName = trainID
		}
		activities[id] = row
		return row
	}

	ensureStationActivity := func(stationID string, stationName string, at string) *TrainbotActivityRow {
		stationID = strings.TrimSpace(stationID)
		if stationID == "" {
			return nil
		}
		serviceDate := datePart(at)
		id := fmt.Sprintf("station:%s:%s", stationID, serviceDate)
		if existing := activities[id]; existing != nil {
			if strings.TrimSpace(existing.SubjectName) == "" && strings.TrimSpace(stationName) != "" {
				existing.SubjectName = strings.TrimSpace(stationName)
			}
			return existing
		}
		row := &TrainbotActivityRow{
			ID:          id,
			ScopeType:   "station",
			SubjectID:   stationID,
			SubjectName: firstNonEmpty(stationName, stationNames[stationID], stationID),
			ServiceDate: serviceDate,
			Summary:     TrainbotActivitySummary{},
			Timeline:    []TrainbotActivityEvent{},
			Comments:    []TrainbotActivityComment{},
			Votes:       []TrainbotActivityVote{},
		}
		activities[id] = row
		return row
	}

	for _, profile := range snapshot.Profiles {
		rider := ensureRider(profile.StableID, profile.Nickname)
		if rider == nil {
			continue
		}
		rider.TelegramUserID = firstNonEmpty(strings.TrimSpace(profile.TelegramUserID), rider.TelegramUserID)
		rider.CreatedAt = firstNonEmpty(strings.TrimSpace(profile.CreatedAt), rider.CreatedAt)
		rider.UpdatedAt = firstNonEmpty(strings.TrimSpace(profile.UpdatedAt), rider.UpdatedAt)
		rider.LastSeenAt = firstNonEmpty(strings.TrimSpace(profile.LastSeenAt), rider.LastSeenAt)
		if lang := normalizeLanguage(profile.Language); lang != "" {
			rider.Settings.Language = lang
		}
	}

	for _, settings := range snapshot.Settings {
		rider := ensureRider(settings.StableID, "")
		if rider == nil {
			continue
		}
		rider.Settings.AlertsEnabled = settings.AlertsEnabled
		rider.Settings.AlertStyle = normalizeAlertStyle(settings.AlertStyle)
		rider.Settings.Language = normalizeLanguage(settings.Language)
		rider.Settings.UpdatedAt = firstNonEmpty(strings.TrimSpace(settings.UpdatedAt), rider.Settings.UpdatedAt)
		rider.UpdatedAt = firstNonEmpty(rider.UpdatedAt, rider.Settings.UpdatedAt)
	}

	for _, favorite := range snapshot.Favorites {
		rider := ensureRider(favorite.StableID, "")
		if rider == nil {
			continue
		}
		rider.Favorites = append(rider.Favorites, TrainbotFavorite{
			FromStationID:   strings.TrimSpace(favorite.FromStationID),
			FromStationName: strings.TrimSpace(favorite.FromStationName),
			ToStationID:     strings.TrimSpace(favorite.ToStationID),
			ToStationName:   strings.TrimSpace(favorite.ToStationName),
			CreatedAt:       strings.TrimSpace(favorite.CreatedAt),
		})
	}

	for _, item := range snapshot.ActiveCheckins {
		rider := ensureRider(item.StableID, "")
		if rider == nil {
			continue
		}
		rider.CurrentRide = &TrainbotRideState{
			TrainInstanceID:   strings.TrimSpace(item.TrainInstanceID),
			BoardingStationID: strings.TrimSpace(item.BoardingStationID),
			CheckedInAt:       strings.TrimSpace(item.CheckedInAt),
			AutoCheckoutAt:    strings.TrimSpace(item.AutoCheckoutAt),
		}
	}

	for _, item := range snapshot.UndoCheckouts {
		rider := ensureRider(item.StableID, "")
		if rider == nil {
			continue
		}
		rider.UndoRide = &TrainbotUndoRideState{
			TrainInstanceID:   strings.TrimSpace(item.TrainInstanceID),
			BoardingStationID: strings.TrimSpace(item.BoardingStationID),
			CheckedInAt:       strings.TrimSpace(item.CheckedInAt),
			AutoCheckoutAt:    strings.TrimSpace(item.AutoCheckoutAt),
			ExpiresAt:         strings.TrimSpace(item.ExpiresAt),
		}
	}

	for _, item := range snapshot.TrainMutes {
		rider := ensureRider(item.StableID, "")
		if rider == nil {
			continue
		}
		rider.Mutes = append(rider.Mutes, TrainbotMute{
			TrainInstanceID: strings.TrimSpace(item.TrainInstanceID),
			MutedUntil:      strings.TrimSpace(item.MutedUntil),
			CreatedAt:       strings.TrimSpace(item.CreatedAt),
		})
	}

	for _, item := range snapshot.Subscriptions {
		rider := ensureRider(item.StableID, "")
		if rider == nil {
			continue
		}
		rider.Subscriptions = append(rider.Subscriptions, TrainbotSubscription{
			TrainInstanceID: strings.TrimSpace(item.TrainInstanceID),
			ExpiresAt:       strings.TrimSpace(item.ExpiresAt),
			IsActive:        item.IsActive,
			CreatedAt:       strings.TrimSpace(item.CreatedAt),
			UpdatedAt:       strings.TrimSpace(item.UpdatedAt),
		})
	}

	for _, item := range snapshot.Reports {
		activity := ensureTrainActivity(item.TrainInstanceID, item.CreatedAt)
		if activity == nil {
			continue
		}
		rider := ensureRider(item.StableID, "")
		activity.Timeline = append(activity.Timeline, TrainbotActivityEvent{
			ID:              strings.TrimSpace(item.ID),
			Kind:            "report",
			StableID:        strings.TrimSpace(item.StableID),
			Nickname:        rider.Nickname,
			Name:            trainSignalIncidentLabel(item.Signal),
			CreatedAt:       strings.TrimSpace(item.CreatedAt),
			Signal:          strings.ToUpper(strings.TrimSpace(item.Signal)),
			TrainInstanceID: strings.TrimSpace(item.TrainInstanceID),
		})
	}

	for _, item := range snapshot.StationSightings {
		activity := ensureStationActivity(item.StationID, item.StationName, item.CreatedAt)
		if activity == nil {
			continue
		}
		rider := ensureRider(item.StableID, "")
		activity.Timeline = append(activity.Timeline, TrainbotActivityEvent{
			ID:                     strings.TrimSpace(item.ID),
			Kind:                   "station_sighting",
			StableID:               strings.TrimSpace(item.StableID),
			Nickname:               rider.Nickname,
			Name:                   stationSightingName(item.DestinationStationName),
			CreatedAt:              strings.TrimSpace(item.CreatedAt),
			StationID:              strings.TrimSpace(item.StationID),
			StationName:            firstNonEmpty(strings.TrimSpace(item.StationName), stationNames[item.StationID]),
			DestinationStationID:   strings.TrimSpace(item.DestinationStationID),
			DestinationStationName: strings.TrimSpace(item.DestinationStationName),
			MatchedTrainInstanceID: strings.TrimSpace(item.MatchedTrainInstanceID),
		})
	}

	for _, item := range snapshot.IncidentVotes {
		activity := ensureLegacyActivity(item.IncidentID, firstNonEmpty(item.UpdatedAt, item.CreatedAt), trainServiceDates, trainNames, stationNames, activities)
		if activity == nil {
			continue
		}
		rider := ensureRider(item.StableID, item.Nickname)
		activity.Votes = append(activity.Votes, TrainbotActivityVote{
			StableID:  strings.TrimSpace(item.StableID),
			Nickname:  rider.Nickname,
			Value:     strings.ToUpper(strings.TrimSpace(item.Value)),
			CreatedAt: strings.TrimSpace(item.CreatedAt),
			UpdatedAt: strings.TrimSpace(item.UpdatedAt),
		})
	}

	for _, item := range snapshot.IncidentComments {
		activity := ensureLegacyActivity(item.IncidentID, item.CreatedAt, trainServiceDates, trainNames, stationNames, activities)
		if activity == nil {
			continue
		}
		rider := ensureRider(item.StableID, item.Nickname)
		activity.Comments = append(activity.Comments, TrainbotActivityComment{
			ID:        strings.TrimSpace(item.ID),
			StableID:  strings.TrimSpace(item.StableID),
			Nickname:  rider.Nickname,
			Body:      strings.TrimSpace(item.Body),
			CreatedAt: strings.TrimSpace(item.CreatedAt),
		})
	}

	riderList := make([]TrainbotRiderRow, 0, len(riders))
	for _, rider := range riders {
		if rider.CreatedAt == "" {
			rider.CreatedAt = firstNonEmpty(rider.UpdatedAt, rider.LastSeenAt, time.Now().UTC().Format(time.RFC3339))
		}
		if rider.UpdatedAt == "" {
			rider.UpdatedAt = firstNonEmpty(rider.Settings.UpdatedAt, rider.LastSeenAt, rider.CreatedAt)
		}
		if rider.LastSeenAt == "" {
			rider.LastSeenAt = rider.UpdatedAt
		}
		if rider.Settings.UpdatedAt == "" {
			rider.Settings.UpdatedAt = rider.UpdatedAt
		}
		if rider.Settings.Language == "" {
			rider.Settings.Language = "EN"
		}
		if rider.Settings.AlertStyle == "" {
			rider.Settings.AlertStyle = "DETAILED"
		}
		if rider.Nickname == "" {
			rider.Nickname = genericNickname(rider.StableID)
		}
		riderList = append(riderList, *rider)
	}

	activityList := make([]TrainbotActivityRow, 0, len(activities))
	for _, activity := range activities {
		normalizeActivity(activity)
		activityList = append(activityList, *activity)
	}
	return riderList, activityList
}

func ensureLegacyActivity(
	incidentID string,
	at string,
	trainServiceDates map[string]string,
	trainNames map[string]string,
	stationNames map[string]string,
	activities map[string]*TrainbotActivityRow,
) *TrainbotActivityRow {
	incidentID = strings.TrimSpace(incidentID)
	if incidentID == "" {
		return nil
	}
	parts := strings.Split(incidentID, ":")
	if len(parts) < 2 {
		return nil
	}
	switch parts[0] {
	case "train":
		trainID := strings.TrimSpace(parts[1])
		serviceDate := firstNonEmpty(strings.TrimSpace(trainServiceDates[trainID]), datePart(at))
		id := fmt.Sprintf("train:%s:%s", trainID, serviceDate)
		if existing := activities[id]; existing != nil {
			return existing
		}
		row := &TrainbotActivityRow{
			ID:          id,
			ScopeType:   "train",
			SubjectID:   trainID,
			SubjectName: firstNonEmpty(strings.TrimSpace(trainNames[trainID]), trainID),
			ServiceDate: serviceDate,
			Summary:     TrainbotActivitySummary{},
			Timeline:    []TrainbotActivityEvent{},
			Comments:    []TrainbotActivityComment{},
			Votes:       []TrainbotActivityVote{},
		}
		activities[id] = row
		return row
	case "station":
		stationID := strings.TrimSpace(parts[1])
		serviceDate := datePart(at)
		id := fmt.Sprintf("station:%s:%s", stationID, serviceDate)
		if existing := activities[id]; existing != nil {
			return existing
		}
		row := &TrainbotActivityRow{
			ID:          id,
			ScopeType:   "station",
			SubjectID:   stationID,
			SubjectName: firstNonEmpty(strings.TrimSpace(stationNames[stationID]), stationID),
			ServiceDate: serviceDate,
			Summary:     TrainbotActivitySummary{},
			Timeline:    []TrainbotActivityEvent{},
			Comments:    []TrainbotActivityComment{},
			Votes:       []TrainbotActivityVote{},
		}
		activities[id] = row
		return row
	default:
		return nil
	}
}

func normalizeActivity(activity *TrainbotActivityRow) {
	if activity == nil {
		return
	}
	sortByTimeDesc(activity.Timeline, func(item TrainbotActivityEvent) string { return item.CreatedAt })
	sortByTimeDesc(activity.Comments, func(item TrainbotActivityComment) string { return item.CreatedAt })
	dedupedVotes := make(map[string]TrainbotActivityVote, len(activity.Votes))
	for _, vote := range activity.Votes {
		if vote.StableID == "" {
			continue
		}
		existing, ok := dedupedVotes[vote.StableID]
		if !ok || compareTime(vote.UpdatedAt, existing.UpdatedAt) > 0 {
			dedupedVotes[vote.StableID] = vote
		}
	}
	activity.Votes = activity.Votes[:0]
	for _, vote := range dedupedVotes {
		activity.Votes = append(activity.Votes, vote)
	}
	sortByTimeDesc(activity.Votes, func(item TrainbotActivityVote) string { return item.UpdatedAt })

	var latestReport *TrainbotActivityEvent
	for i := range activity.Timeline {
		if activity.Timeline[i].Kind == "report" || activity.Timeline[i].Kind == "station_sighting" {
			latestReport = &activity.Timeline[i]
			break
		}
	}
	lastActivityName := ""
	lastActivityAt := ""
	lastActivityActor := ""
	if latestReport != nil {
		activity.Summary.LastReportName = latestReport.Name
		activity.Summary.LastReportAt = latestReport.CreatedAt
		activity.Summary.LastReporter = latestReport.Nickname
		lastActivityName = latestReport.Name
		lastActivityAt = latestReport.CreatedAt
		lastActivityActor = latestReport.Nickname
	}

	if len(activity.Comments) > 0 && compareTime(activity.Comments[0].CreatedAt, lastActivityAt) > 0 {
		lastActivityName = "Comment"
		lastActivityAt = activity.Comments[0].CreatedAt
		lastActivityActor = activity.Comments[0].Nickname
	}

	for _, vote := range activity.Votes {
		if strings.ToUpper(strings.TrimSpace(vote.Value)) != "ONGOING" {
			continue
		}
		if compareTime(vote.UpdatedAt, lastActivityAt) > 0 {
			lastActivityName = "Still there"
			lastActivityAt = vote.UpdatedAt
			lastActivityActor = vote.Nickname
		}
		break
	}

	activity.Summary.LastActivityName = lastActivityName
	activity.Summary.LastActivityAt = lastActivityAt
	activity.Summary.LastActivityActor = lastActivityActor
	activity.LastActivityAt = lastActivityAt
	activity.Active = isActivityActive(*activity)
}

func isActivityActive(activity TrainbotActivityRow) bool {
	if activity.Summary.LastReportAt == "" {
		return false
	}
	reportAt, err := time.Parse(time.RFC3339, activity.Summary.LastReportAt)
	if err != nil {
		return false
	}
	cutoff := trainActivityActiveWindow
	if activity.ScopeType == "station" {
		cutoff = stationActivityActiveWindow
	}
	return time.Since(reportAt) <= cutoff
}

func datePart(value string) string {
	if len(strings.TrimSpace(value)) >= 10 {
		return strings.TrimSpace(value)[:10]
	}
	return ""
}

func trainSignalIncidentLabel(signal string) string {
	switch strings.ToUpper(strings.TrimSpace(signal)) {
	case "INSPECTION_STARTED":
		return "Inspection started"
	case "INSPECTION_IN_MY_CAR":
		return "Inspection in carriage"
	case "INSPECTION_ENDED":
		return "Inspection ended"
	default:
		return strings.TrimSpace(signal)
	}
}

func stationSightingName(destination string) string {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "Platform sighting"
	}
	return "Platform sighting to " + destination
}

func decodeCleanupExpiredStateResult(payload any) CleanupExpiredStateResult {
	raw, ok := payload.(map[string]any)
	if !ok {
		return CleanupExpiredStateResult{}
	}
	return CleanupExpiredStateResult{
		CheckinsDeleted:         decodeInt64(raw["checkinsDeleted"]),
		SubscriptionsDeleted:    decodeInt64(raw["subscriptionsDeleted"]),
		ReportsDeleted:          decodeInt64(raw["reportsDeleted"]),
		StationSightingsDeleted: decodeInt64(raw["stationSightingsDeleted"]),
		TrainStopsDeleted:       decodeInt64(raw["trainStopsDeleted"]),
		TrainsDeleted:           decodeInt64(raw["trainsDeleted"]),
		FeedEventsDeleted:       decodeInt64(raw["feedEventsDeleted"]),
		FeedImportsDeleted:      decodeInt64(raw["feedImportsDeleted"]),
		ImportChunksDeleted:     decodeInt64(raw["importChunksDeleted"]),
	}
}

func decodeInt64(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func sqlQuote(value string) string {
	return "'" + strings.ReplaceAll(strings.TrimSpace(value), "'", "''") + "'"
}

func sqlRows(results []SQLStatementResult) ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	for _, result := range results {
		rows, err := sqlStatementRows(result)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func sqlStatementRows(result SQLStatementResult) ([]map[string]any, error) {
	names, err := sqlColumnNames(result.Schema)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		item := make(map[string]any, len(names))
		for index, name := range names {
			if index < len(row) {
				item[name] = row[index]
				continue
			}
			item[name] = nil
		}
		rows = append(rows, item)
	}
	return rows, nil
}

func sqlColumnNames(schema map[string]any) ([]string, error) {
	if len(schema) == 0 {
		return nil, nil
	}
	rawElements, ok := schema["elements"]
	if !ok {
		return nil, nil
	}
	elements, ok := rawElements.([]any)
	if !ok {
		return nil, fmt.Errorf("decode spacetime sql schema: unexpected elements payload %T", rawElements)
	}
	names := make([]string, 0, len(elements))
	for index, rawElement := range elements {
		element, ok := rawElement.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("decode spacetime sql schema element: unexpected element payload %T", rawElement)
		}
		name := fmt.Sprintf("col_%d", index)
		switch rawName := element["name"].(type) {
		case string:
			if strings.TrimSpace(rawName) != "" {
				name = strings.TrimSpace(rawName)
			}
		case map[string]any:
			if rawSome, ok := rawName["some"].(string); ok && strings.TrimSpace(rawSome) != "" {
				name = strings.TrimSpace(rawSome)
			}
		}
		names = append(names, name)
	}
	return names, nil
}

func decodeSQLRowsInto(rows []map[string]any, out any) error {
	body, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("marshal spacetime sql rows: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 || string(bytes.TrimSpace(body)) == "null" {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode spacetime sql rows: %w", err)
	}
	return nil
}

func decodeInto(payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal procedure payload: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 || string(bytes.TrimSpace(body)) == "null" {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode procedure payload: %w", err)
	}
	return nil
}

func validateProcedureJSON(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var payload any
	return json.Unmarshal([]byte(raw), &payload)
}

func mustJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(body)
}

func loadRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("spacetime jwt private key file is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read spacetime private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("decode spacetime private key: invalid PEM")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#1 spacetime private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PKCS#8 spacetime private key: %w", err)
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("spacetime private key must be RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported spacetime private key type %q", block.Type)
	}
}

func keyIDForPublicKey(publicKey *rsa.PublicKey) string {
	sum := sha256.Sum256(x509.MarshalPKCS1PublicKey(publicKey))
	return hex.EncodeToString(sum[:8])
}

func (i *serviceTokenIssuer) issue(now time.Time) (string, error) {
	return i.issueWith(now, TokenOptions{})
}

func (i *serviceTokenIssuer) issueWith(now time.Time, opts TokenOptions) (string, error) {
	expiresAt := now.Add(i.tokenTTL)
	subject := strings.TrimSpace(opts.Subject)
	if subject == "" {
		subject = i.subject
	}
	roles := append([]string(nil), i.roles...)
	if len(opts.Roles) > 0 {
		roles = append([]string(nil), opts.Roles...)
	}
	claims := map[string]any{
		"iss":   i.issuer,
		"sub":   subject,
		"aud":   []string{i.audience},
		"iat":   now.Unix(),
		"nbf":   now.Unix(),
		"exp":   expiresAt.Unix(),
		"jti":   randomTokenID(),
		"roles": roles,
	}
	for key, value := range opts.Claims {
		claims[key] = value
	}
	return signJWT(i.privateKey, i.keyID, claims)
}

func signJWT(privateKey *rsa.PrivateKey, keyID string, claims map[string]any) (string, error) {
	headerJSON, err := json.Marshal(map[string]any{
		"typ": "JWT",
		"alg": "RS256",
		"kid": keyID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal spacetime token header: %w", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal spacetime token claims: %w", err)
	}
	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign spacetime token: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func randomTokenID() string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("train-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func JWKSForPrivateKey(path string) (map[string]any, error) {
	privateKey, err := loadRSAPrivateKey(path)
	if err != nil {
		return nil, err
	}
	publicKey := &privateKey.PublicKey
	return map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": keyIDForPublicKey(publicKey),
				"n":   base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(publicKey.E)).Bytes()),
			},
		},
	}, nil
}

func StableIDForTelegramUser(userID int64) string {
	return "telegram:" + strconv.FormatInt(userID, 10)
}

func TelegramUserIDFromStableID(stableID string) (int64, bool) {
	if !strings.HasPrefix(strings.TrimSpace(stableID), "telegram:") {
		return 0, false
	}
	parsed, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(stableID), "telegram:"), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func telegramUserIDForStableID(stableID string) string {
	if userID, ok := TelegramUserIDFromStableID(stableID); ok {
		return strconv.FormatInt(userID, 10)
	}
	return strings.TrimSpace(stableID)
}

func normalizeLanguage(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "LV":
		return "LV"
	default:
		return "EN"
	}
}

func normalizeAlertStyle(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "DISCREET":
		return "DISCREET"
	default:
		return "DETAILED"
	}
}

func normalizeStationKey(value string) string {
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

func genericNickname(stableID string) string {
	adjectives := []string{
		"Amber", "Cedar", "Silver", "North", "Swift", "Mellow", "Harbor", "Forest",
		"Granite", "Quiet", "Bright", "Saffron", "Willow", "Copper", "River", "Cloud",
	}
	nouns := []string{
		"Scout", "Rider", "Signal", "Beacon", "Traveler", "Watcher", "Harbor", "Comet",
		"Falcon", "Lantern", "Pioneer", "Courier", "Voyager", "Pilot", "Atlas", "Drifter",
	}
	sum := sha256.Sum256([]byte("train:" + strings.TrimSpace(stableID)))
	adjective := adjectives[int(sum[0])%len(adjectives)]
	noun := nouns[int(sum[1])%len(nouns)]
	suffix := int(sum[2])%900 + 100
	return fmt.Sprintf("%s %s %03d", adjective, noun, suffix)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func compareTime(left string, right string) int {
	leftTime, _ := time.Parse(time.RFC3339, strings.TrimSpace(left))
	rightTime, _ := time.Parse(time.RFC3339, strings.TrimSpace(right))
	switch {
	case leftTime.Before(rightTime):
		return -1
	case leftTime.After(rightTime):
		return 1
	default:
		return 0
	}
}

func sortByTimeDesc[T any](items []T, ts func(T) string) {
	sort.Slice(items, func(i, j int) bool {
		return compareTime(ts(items[i]), ts(items[j])) > 0
	})
}
