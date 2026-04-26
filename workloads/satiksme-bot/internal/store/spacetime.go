package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/spacetime"
)

type SpacetimeStore struct {
	client *spacetime.Syncer
}

func NewSpacetimeStore(client *spacetime.Syncer) *SpacetimeStore {
	return &SpacetimeStore{client: client}
}

func (s *SpacetimeStore) Migrate(context.Context) error {
	return nil
}

func (s *SpacetimeStore) HealthCheck(ctx context.Context) error {
	_, err := s.client.SQL(ctx, "SELECT feed FROM satiksmebot_public_live_snapshot_state LIMIT 1")
	return err
}

func (s *SpacetimeStore) InsertStopSighting(ctx context.Context, sighting model.StopSighting) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_put_stop_sighting", []any{mustJSONValue(spacetimeStopSightingPayload(sighting))})
	return err
}

func (s *SpacetimeStore) InsertStopSightingWithVote(ctx context.Context, sighting model.StopSighting, vote model.IncidentVote, event model.IncidentVoteEvent, dedupeWindow time.Duration) error {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_record_stop_sighting_with_vote", []any{
		mustJSONValue(spacetimeStopSightingPayload(sighting)),
		mustJSONValue(spacetimeIncidentVotePayload(vote)),
		mustJSONValue(spacetimeIncidentVoteEventPayload(event)),
		uint32(dedupeWindow.Seconds()),
	})
	if err != nil {
		return err
	}
	return decodeDedupeResult(payload)
}

func (s *SpacetimeStore) GetLastStopSightingByUserScope(ctx context.Context, userID int64, stopID string) (*model.StopSighting, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_get_last_stop_sighting", []any{strconv.FormatInt(userID, 10), stopID})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Sighting *model.StopSighting `json:"sighting"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Sighting, nil
}

func (s *SpacetimeStore) ListStopSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.StopSighting, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_stop_sightings_since", []any{since.UTC().Format(time.RFC3339), stopID, limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Sightings []model.StopSighting `json:"sightings"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Sightings, nil
}

func (s *SpacetimeStore) InsertVehicleSighting(ctx context.Context, sighting model.VehicleSighting) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_put_vehicle_sighting", []any{mustJSONValue(spacetimeVehicleSightingPayload(sighting))})
	return err
}

func (s *SpacetimeStore) InsertVehicleSightingWithVote(ctx context.Context, sighting model.VehicleSighting, vote model.IncidentVote, event model.IncidentVoteEvent, dedupeWindow time.Duration) error {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_record_vehicle_sighting_with_vote", []any{
		mustJSONValue(spacetimeVehicleSightingPayload(sighting)),
		mustJSONValue(spacetimeIncidentVotePayload(vote)),
		mustJSONValue(spacetimeIncidentVoteEventPayload(event)),
		uint32(dedupeWindow.Seconds()),
	})
	if err != nil {
		return err
	}
	return decodeDedupeResult(payload)
}

func (s *SpacetimeStore) GetLastVehicleSightingByUserScope(ctx context.Context, userID int64, scopeKey string) (*model.VehicleSighting, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_get_last_vehicle_sighting", []any{strconv.FormatInt(userID, 10), scopeKey})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Sighting *model.VehicleSighting `json:"sighting"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Sighting, nil
}

func (s *SpacetimeStore) ListVehicleSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.VehicleSighting, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_vehicle_sightings_since", []any{since.UTC().Format(time.RFC3339), stopID, limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Sightings []model.VehicleSighting `json:"sightings"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Sightings, nil
}

func (s *SpacetimeStore) UpsertIncidentVote(ctx context.Context, vote model.IncidentVote) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_upsert_incident_vote", []any{mustJSONValue(spacetimeIncidentVotePayload(vote))})
	return err
}

func (s *SpacetimeStore) RecordIncidentVote(ctx context.Context, vote model.IncidentVote, event model.IncidentVoteEvent) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_record_incident_vote", []any{
		mustJSONValue(spacetimeIncidentVotePayload(vote)),
		mustJSONValue(spacetimeIncidentVoteEventPayload(event)),
	})
	return err
}

func (s *SpacetimeStore) ListIncidentVotes(ctx context.Context, incidentID string) ([]model.IncidentVote, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_incident_votes", []any{incidentID})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Votes []model.IncidentVote `json:"votes"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Votes, nil
}

func (s *SpacetimeStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]model.IncidentVoteEvent, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_incident_vote_events", []any{incidentID, since.UTC().Format(time.RFC3339), limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Events []model.IncidentVoteEvent `json:"events"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Events, nil
}

func (s *SpacetimeStore) CountMapReportsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_count_map_reports_by_user_since", []any{strconv.FormatInt(userID, 10), since.UTC().Format(time.RFC3339)})
	if err != nil {
		return 0, err
	}
	var raw struct {
		Count int `json:"count"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return 0, err
	}
	return raw.Count, nil
}

func (s *SpacetimeStore) CountIncidentVoteEventsByUserSince(ctx context.Context, userID int64, source model.IncidentVoteSource, since time.Time) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_count_incident_vote_events_by_user_since", []any{strconv.FormatInt(userID, 10), string(source), since.UTC().Format(time.RFC3339)})
	if err != nil {
		return 0, err
	}
	var raw struct {
		Count int `json:"count"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return 0, err
	}
	return raw.Count, nil
}

func (s *SpacetimeStore) InsertIncidentComment(ctx context.Context, comment model.IncidentComment) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_put_incident_comment", []any{mustJSONValue(spacetimeIncidentCommentPayload(comment))})
	return err
}

func (s *SpacetimeStore) ListIncidentComments(ctx context.Context, incidentID string, limit int) ([]model.IncidentComment, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_incident_comments", []any{incidentID, limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Comments []model.IncidentComment `json:"comments"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Comments, nil
}

func (s *SpacetimeStore) EnqueueReportDump(ctx context.Context, item ReportDumpItem) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_enqueue_report_dump", []any{mustJSONValue(spacetimeReportDumpPayload(item))})
	return err
}

func (s *SpacetimeStore) PeekNextReportDump(ctx context.Context) (*ReportDumpItem, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_peek_report_dump", []any{})
	if err != nil {
		return nil, err
	}
	return decodeSpacetimeReportDumpPayload(payload)
}

func (s *SpacetimeStore) NextReportDump(ctx context.Context, now time.Time) (*ReportDumpItem, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_next_report_dump", []any{now.UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	return decodeSpacetimeReportDumpPayload(payload)
}

func (s *SpacetimeStore) DeleteReportDump(ctx context.Context, id string) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_delete_report_dump", []any{id})
	return err
}

func (s *SpacetimeStore) UpdateReportDumpFailure(ctx context.Context, id string, attempts int, nextAttemptAt, lastAttemptAt time.Time, lastError string) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_update_report_dump_failure", []any{
		id,
		attempts,
		nextAttemptAt.UTC().Format(time.RFC3339),
		lastAttemptAt.UTC().Format(time.RFC3339),
		lastError,
	})
	return err
}

func (s *SpacetimeStore) PendingReportDumpCount(ctx context.Context) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_pending_report_dump_count", []any{})
	if err != nil {
		return 0, err
	}
	var raw struct {
		Pending int `json:"pending"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return 0, err
	}
	return raw.Pending, nil
}

func (s *SpacetimeStore) CleanupExpired(ctx context.Context, cutoff time.Time) (CleanupResult, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_cleanup_expired_state", []any{
		time.Now().UTC().Format(time.RFC3339),
		cutoff.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return CleanupResult{}, err
	}
	var raw CleanupResult
	if err := decodePayload(payload, &raw); err != nil {
		return CleanupResult{}, err
	}
	return raw, nil
}

func decodePayload(payload any, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

func decodeDedupeResult(payload any) error {
	var raw struct {
		Deduped bool `json:"deduped"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return err
	}
	if raw.Deduped {
		return ErrDuplicateReport
	}
	return nil
}

func spacetimeUserID(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

func spacetimeStableID(userID int64) string {
	return model.TelegramStableID(userID)
}

func spacetimeNickname(userID int64, nickname string) string {
	clean := strings.TrimSpace(nickname)
	if clean != "" {
		return clean
	}
	return model.GenericNickname(userID)
}

type spacetimeStopSightingJSON struct {
	ID        string    `json:"id"`
	StopID    string    `json:"stopId"`
	StableID  string    `json:"stableId"`
	UserID    string    `json:"userId"`
	Hidden    bool      `json:"hidden"`
	CreatedAt time.Time `json:"createdAt"`
}

func spacetimeStopSightingPayload(item model.StopSighting) spacetimeStopSightingJSON {
	return spacetimeStopSightingJSON{
		ID:        item.ID,
		StopID:    item.StopID,
		StableID:  spacetimeStableID(item.UserID),
		UserID:    spacetimeUserID(item.UserID),
		Hidden:    item.Hidden,
		CreatedAt: item.CreatedAt,
	}
}

type spacetimeVehicleSightingJSON struct {
	ID               string    `json:"id"`
	StopID           string    `json:"stopId,omitempty"`
	StableID         string    `json:"stableId"`
	UserID           string    `json:"userId"`
	Mode             string    `json:"mode"`
	RouteLabel       string    `json:"routeLabel"`
	Direction        string    `json:"direction"`
	Destination      string    `json:"destination"`
	DepartureSeconds int       `json:"departureSeconds"`
	LiveRowID        string    `json:"liveRowId,omitempty"`
	ScopeKey         string    `json:"scopeKey"`
	Hidden           bool      `json:"hidden"`
	CreatedAt        time.Time `json:"createdAt"`
}

func spacetimeVehicleSightingPayload(item model.VehicleSighting) spacetimeVehicleSightingJSON {
	return spacetimeVehicleSightingJSON{
		ID:               item.ID,
		StopID:           item.StopID,
		StableID:         spacetimeStableID(item.UserID),
		UserID:           spacetimeUserID(item.UserID),
		Mode:             item.Mode,
		RouteLabel:       item.RouteLabel,
		Direction:        item.Direction,
		Destination:      item.Destination,
		DepartureSeconds: item.DepartureSeconds,
		LiveRowID:        item.LiveRowID,
		ScopeKey:         item.ScopeKey,
		Hidden:           item.Hidden,
		CreatedAt:        item.CreatedAt,
	}
}

type spacetimeIncidentVoteJSON struct {
	IncidentID string                  `json:"incidentId"`
	StableID   string                  `json:"stableId"`
	UserID     string                  `json:"userId"`
	Nickname   string                  `json:"nickname"`
	Value      model.IncidentVoteValue `json:"value"`
	CreatedAt  time.Time               `json:"createdAt"`
	UpdatedAt  time.Time               `json:"updatedAt"`
}

func spacetimeIncidentVotePayload(item model.IncidentVote) spacetimeIncidentVoteJSON {
	return spacetimeIncidentVoteJSON{
		IncidentID: item.IncidentID,
		StableID:   spacetimeStableID(item.UserID),
		UserID:     spacetimeUserID(item.UserID),
		Nickname:   spacetimeNickname(item.UserID, item.Nickname),
		Value:      item.Value,
		CreatedAt:  item.CreatedAt,
		UpdatedAt:  item.UpdatedAt,
	}
}

type spacetimeIncidentVoteEventJSON struct {
	ID         string                   `json:"id"`
	IncidentID string                   `json:"incidentId"`
	StableID   string                   `json:"stableId"`
	UserID     string                   `json:"userId"`
	Nickname   string                   `json:"nickname"`
	Value      model.IncidentVoteValue  `json:"value"`
	Source     model.IncidentVoteSource `json:"source"`
	CreatedAt  time.Time                `json:"createdAt"`
}

func spacetimeIncidentVoteEventPayload(item model.IncidentVoteEvent) spacetimeIncidentVoteEventJSON {
	return spacetimeIncidentVoteEventJSON{
		ID:         item.ID,
		IncidentID: item.IncidentID,
		StableID:   spacetimeStableID(item.UserID),
		UserID:     spacetimeUserID(item.UserID),
		Nickname:   spacetimeNickname(item.UserID, item.Nickname),
		Value:      item.Value,
		Source:     item.Source,
		CreatedAt:  item.CreatedAt,
	}
}

type spacetimeIncidentCommentJSON struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incidentId"`
	StableID   string    `json:"stableId"`
	UserID     string    `json:"userId"`
	Nickname   string    `json:"nickname"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

func spacetimeIncidentCommentPayload(item model.IncidentComment) spacetimeIncidentCommentJSON {
	return spacetimeIncidentCommentJSON{
		ID:         item.ID,
		IncidentID: item.IncidentID,
		StableID:   spacetimeStableID(item.UserID),
		UserID:     spacetimeUserID(item.UserID),
		Nickname:   spacetimeNickname(item.UserID, item.Nickname),
		Body:       item.Body,
		CreatedAt:  item.CreatedAt,
	}
}

type spacetimeReportDumpJSON struct {
	ID            string `json:"id"`
	Payload       string `json:"payload"`
	Attempts      int    `json:"attempts"`
	CreatedAt     string `json:"createdAt"`
	NextAttemptAt string `json:"nextAttemptAt"`
	LastAttemptAt string `json:"lastAttemptAt"`
	LastError     string `json:"lastError"`
}

func spacetimeReportDumpPayload(item ReportDumpItem) spacetimeReportDumpJSON {
	return spacetimeReportDumpJSON{
		ID:            item.ID,
		Payload:       item.Payload,
		Attempts:      item.Attempts,
		CreatedAt:     item.CreatedAt.UTC().Format(time.RFC3339),
		NextAttemptAt: item.NextAttemptAt.UTC().Format(time.RFC3339),
		LastAttemptAt: formatOptionalTime(item.LastAttemptAt),
		LastError:     item.LastError,
	}
}

func decodeSpacetimeReportDumpPayload(payload any) (*ReportDumpItem, error) {
	var raw struct {
		Item *spacetimeReportDumpJSON `json:"item"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	if raw.Item == nil {
		return nil, nil
	}
	item := ReportDumpItem{
		ID:        raw.Item.ID,
		Payload:   raw.Item.Payload,
		Attempts:  raw.Item.Attempts,
		LastError: raw.Item.LastError,
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339, strings.TrimSpace(raw.Item.CreatedAt))
	if err != nil {
		return nil, fmt.Errorf("parse report dump createdAt: %w", err)
	}
	item.NextAttemptAt, err = time.Parse(time.RFC3339, strings.TrimSpace(raw.Item.NextAttemptAt))
	if err != nil {
		return nil, fmt.Errorf("parse report dump nextAttemptAt: %w", err)
	}
	item.LastAttemptAt, err = parseOptionalTime(raw.Item.LastAttemptAt)
	if err != nil {
		return nil, fmt.Errorf("parse report dump lastAttemptAt: %w", err)
	}
	return &item, nil
}

func mustJSONValue(value any) string {
	body, _ := json.Marshal(value)
	return string(body)
}
