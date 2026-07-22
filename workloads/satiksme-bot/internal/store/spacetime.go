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
		Sighting *spacetimePrivateStopSightingJSON `json:"sighting"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Sighting.modelValue(), nil
}

func (s *SpacetimeStore) ListStopSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.StopSighting, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_stop_sightings_since", []any{since.UTC().Format(time.RFC3339), stopID, limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Sightings []spacetimePrivateStopSightingJSON `json:"sightings"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return privateStopSightingValues(raw.Sightings), nil
}

func (s *SpacetimeStore) ListPublicSightings(ctx context.Context, stopID string, limit int) (model.VisibleSightings, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_list_public_sightings", []any{strings.TrimSpace(stopID), normalizedProcedureLimit(limit)})
	if err != nil {
		return model.VisibleSightings{}, err
	}
	var raw model.VisibleSightings
	if err := decodePayload(payload, &raw); err != nil {
		return model.VisibleSightings{}, err
	}
	return raw, nil
}

func (s *SpacetimeStore) SetLiveViewerState(ctx context.Context, sessionID string, page string, visible bool) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_set_live_viewer_state", []any{
		strings.TrimSpace(sessionID),
		strings.TrimSpace(page),
		visible,
	})
	return err
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
		Sighting *spacetimePrivateVehicleSightingJSON `json:"sighting"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Sighting.modelValue(), nil
}

func (s *SpacetimeStore) ListVehicleSightingsSince(ctx context.Context, since time.Time, stopID string, limit int) ([]model.VehicleSighting, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_vehicle_sightings_since", []any{since.UTC().Format(time.RFC3339), stopID, limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Sightings []spacetimePrivateVehicleSightingJSON `json:"sightings"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return privateVehicleSightingValues(raw.Sightings), nil
}

func (s *SpacetimeStore) InsertAreaReport(ctx context.Context, report model.AreaReport) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_put_area_report", []any{mustJSONValue(spacetimeAreaReportPayload(report))})
	return err
}

func (s *SpacetimeStore) InsertAreaReportWithVote(ctx context.Context, report model.AreaReport, vote model.IncidentVote, event model.IncidentVoteEvent, dedupeWindow time.Duration) error {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_record_area_report_with_vote", []any{
		mustJSONValue(spacetimeAreaReportPayload(report)),
		mustJSONValue(spacetimeIncidentVotePayload(vote)),
		mustJSONValue(spacetimeIncidentVoteEventPayload(event)),
		uint32(dedupeWindow.Seconds()),
	})
	if err != nil {
		return normalizeAreaReportVoteError(err)
	}
	return decodeDedupeResult(payload)
}

func normalizeAreaReportVoteError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), ErrReportVoteIncidentMismatch.Error()) {
		return fmt.Errorf("%w: %v", ErrReportVoteIncidentMismatch, err)
	}
	return err
}

func (s *SpacetimeStore) GetLastAreaReportByUserScope(ctx context.Context, userID int64, scopeKey string) (*model.AreaReport, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_get_last_area_report", []any{strconv.FormatInt(userID, 10), scopeKey})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Report *spacetimePrivateAreaReportJSON `json:"report"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Report.modelValue(), nil
}

func (s *SpacetimeStore) ListAreaReportsSince(ctx context.Context, since time.Time, limit int) ([]model.AreaReport, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_area_reports_since", []any{since.UTC().Format(time.RFC3339), limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Reports []spacetimePrivateAreaReportJSON `json:"reports"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return privateAreaReportValues(raw.Reports), nil
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
		Votes []spacetimePrivateIncidentVoteJSON `json:"votes"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return privateIncidentVoteValues(raw.Votes), nil
}

func (s *SpacetimeStore) ListPublicIncidents(ctx context.Context, viewerID int64, limit int) ([]model.IncidentSummary, error) {
	payload, err := s.callPublicProcedureForViewer(ctx, viewerID, "satiksmebot_list_public_incidents", []any{normalizedProcedureLimit(limit)})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Incidents []model.IncidentSummary `json:"incidents"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return raw.Incidents, nil
}

func (s *SpacetimeStore) GetPublicIncidentDetail(ctx context.Context, incidentID string, viewerID int64) (*model.IncidentDetail, error) {
	payload, err := s.callPublicProcedureForViewer(ctx, viewerID, "satiksmebot_get_public_incident_detail", []any{strings.TrimSpace(incidentID)})
	if err != nil {
		return nil, err
	}
	var raw model.IncidentDetail
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func (s *SpacetimeStore) ListIncidentVoteEvents(ctx context.Context, incidentID string, since time.Time, limit int) ([]model.IncidentVoteEvent, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_incident_vote_events", []any{incidentID, since.UTC().Format(time.RFC3339), limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Events []spacetimePrivateIncidentVoteEventJSON `json:"events"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return privateIncidentVoteEventValues(raw.Events), nil
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

func (s *SpacetimeStore) CountIncidentCommentsByUserSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_count_incident_comments_by_user_since", []any{strconv.FormatInt(userID, 10), since.UTC().Format(time.RFC3339)})
	if err != nil {
		if missingSpacetimeProcedure(err) {
			return 0, nil
		}
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

func (s *SpacetimeStore) CountIncidentCommentsByIncidentSince(ctx context.Context, incidentID string, since time.Time) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_count_incident_comments_by_incident_since", []any{strings.TrimSpace(incidentID), since.UTC().Format(time.RFC3339)})
	if err != nil {
		if missingSpacetimeProcedure(err) {
			return s.countIncidentCommentsByIncidentSinceFallback(ctx, incidentID, since)
		}
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

func (s *SpacetimeStore) countIncidentCommentsByIncidentSinceFallback(ctx context.Context, incidentID string, since time.Time) (int, error) {
	comments, err := s.ListIncidentComments(ctx, incidentID, 100)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, comment := range comments {
		if !comment.CreatedAt.Before(since.UTC()) {
			count++
		}
	}
	return count, nil
}

func missingSpacetimeProcedure(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "nonexistent procedure") ||
		strings.Contains(message, "unknown procedure")
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
		Comments []spacetimePrivateIncidentCommentJSON `json:"comments"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	return privateIncidentCommentValues(raw.Comments), nil
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

func (s *SpacetimeStore) GetChatAnalyzerCheckpoint(ctx context.Context, chatID string) (int64, bool, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_get_chat_analyzer_checkpoint", []any{strings.TrimSpace(chatID)})
	if err != nil {
		return 0, false, err
	}
	var raw struct {
		LastMessageID int64 `json:"lastMessageId"`
		Found         bool  `json:"found"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return 0, false, err
	}
	return raw.LastMessageID, raw.Found, nil
}

func (s *SpacetimeStore) SetChatAnalyzerCheckpoint(ctx context.Context, chatID string, lastMessageID int64, updatedAt time.Time) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_set_chat_analyzer_checkpoint", []any{
		strings.TrimSpace(chatID),
		strconv.FormatInt(lastMessageID, 10),
		updatedAt.UTC().Format(time.RFC3339),
	})
	return err
}

func (s *SpacetimeStore) EnqueueChatAnalyzerMessage(ctx context.Context, item model.ChatAnalyzerMessage) (bool, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_enqueue_chat_analyzer_message", []any{mustJSONValue(spacetimeChatAnalyzerMessagePayload(item))})
	if err != nil {
		return false, err
	}
	var raw struct {
		Inserted bool `json:"inserted"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return false, err
	}
	return raw.Inserted, nil
}

func (s *SpacetimeStore) ListPendingChatAnalyzerMessages(ctx context.Context, limit int) ([]model.ChatAnalyzerMessage, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_list_pending_chat_analyzer_messages", []any{limit})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Messages []spacetimeChatAnalyzerMessageJSON `json:"messages"`
	}
	if err := decodePayload(payload, &raw); err != nil {
		return nil, err
	}
	out := make([]model.ChatAnalyzerMessage, 0, len(raw.Messages))
	for _, item := range raw.Messages {
		decoded, err := decodeSpacetimeChatAnalyzerMessage(item)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func (s *SpacetimeStore) MarkChatAnalyzerMessageProcessed(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, lastError string, processedAt time.Time) error {
	return s.MarkChatAnalyzerMessageProcessedInBatch(ctx, id, status, analysisJSON, appliedActionID, appliedTargetKey, "", lastError, processedAt)
}

func (s *SpacetimeStore) MarkChatAnalyzerMessageProcessedInBatch(ctx context.Context, id string, status model.ChatAnalyzerMessageStatus, analysisJSON, appliedActionID, appliedTargetKey, batchID, lastError string, processedAt time.Time) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_mark_chat_analyzer_message_processed", []any{
		strings.TrimSpace(id),
		string(status),
		strings.TrimSpace(analysisJSON),
		strings.TrimSpace(appliedActionID),
		strings.TrimSpace(appliedTargetKey),
		strings.TrimSpace(batchID),
		strings.TrimSpace(lastError),
		processedAt.UTC().Format(time.RFC3339),
	})
	return err
}

func (s *SpacetimeStore) SaveChatAnalyzerBatch(ctx context.Context, batch model.ChatAnalyzerBatch) error {
	_, err := s.client.CallProcedure(ctx, "satiksmebot_service_save_chat_analyzer_batch", []any{mustJSONValue(spacetimeChatAnalyzerBatchPayload(batch))})
	return err
}

func (s *SpacetimeStore) CountChatAnalyzerMessagesBySenderSince(ctx context.Context, chatID string, senderID int64, since time.Time) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_count_chat_analyzer_messages_by_sender_since", []any{
		strings.TrimSpace(chatID),
		strconv.FormatInt(senderID, 10),
		since.UTC().Format(time.RFC3339),
	})
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

func (s *SpacetimeStore) CountChatAnalyzerAppliedByTargetSince(ctx context.Context, targetKey string, since time.Time) (int, error) {
	payload, err := s.client.CallProcedure(ctx, "satiksmebot_service_count_chat_analyzer_applied_by_target_since", []any{
		strings.TrimSpace(targetKey),
		since.UTC().Format(time.RFC3339),
	})
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

func normalizedProcedureLimit(limit int) int {
	if limit < 0 {
		return 0
	}
	return limit
}

func (s *SpacetimeStore) callPublicProcedureForViewer(ctx context.Context, viewerID int64, name string, args []any) (any, error) {
	subject := "anonymous:satiksme-public"
	if viewerID > 0 {
		subject = fmt.Sprintf("telegram:%d", viewerID)
	}
	token, err := s.client.IssueToken(time.Now().UTC(), spacetime.TokenOptions{
		Subject: subject,
		Roles:   []string{"satiksme_viewer"},
	})
	if err != nil {
		return nil, err
	}
	return s.client.CallProcedureWithToken(ctx, name, args, token)
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

// Private service procedures include fields deliberately omitted from the
// public model JSON tags. Keep an explicit wire boundary here so private reads
// retain those fields without making normal model serialization expose them.
type spacetimePrivateStopSightingJSON struct {
	ID        string    `json:"id"`
	StopID    string    `json:"stopId"`
	UserID    int64     `json:"userId"`
	Hidden    bool      `json:"hidden"`
	CreatedAt time.Time `json:"createdAt"`
}

func (item *spacetimePrivateStopSightingJSON) modelValue() *model.StopSighting {
	if item == nil {
		return nil
	}
	return &model.StopSighting{
		ID: item.ID, StopID: item.StopID, UserID: item.UserID,
		Hidden: item.Hidden, CreatedAt: item.CreatedAt,
	}
}

func privateStopSightingValues(items []spacetimePrivateStopSightingJSON) []model.StopSighting {
	if items == nil {
		return nil
	}
	out := make([]model.StopSighting, len(items))
	for index := range items {
		out[index] = *items[index].modelValue()
	}
	return out
}

type spacetimePrivateVehicleSightingJSON struct {
	ID               string    `json:"id"`
	StopID           string    `json:"stopId"`
	UserID           int64     `json:"userId"`
	Mode             string    `json:"mode"`
	RouteLabel       string    `json:"routeLabel"`
	Direction        string    `json:"direction"`
	Destination      string    `json:"destination"`
	DepartureSeconds int       `json:"departureSeconds"`
	LiveRowID        string    `json:"liveRowId"`
	ScopeKey         string    `json:"scopeKey"`
	Hidden           bool      `json:"hidden"`
	CreatedAt        time.Time `json:"createdAt"`
}

func (item *spacetimePrivateVehicleSightingJSON) modelValue() *model.VehicleSighting {
	if item == nil {
		return nil
	}
	return &model.VehicleSighting{
		ID: item.ID, StopID: item.StopID, UserID: item.UserID,
		Mode: item.Mode, RouteLabel: item.RouteLabel, Direction: item.Direction,
		Destination: item.Destination, DepartureSeconds: item.DepartureSeconds,
		LiveRowID: item.LiveRowID, ScopeKey: item.ScopeKey, Hidden: item.Hidden,
		CreatedAt: item.CreatedAt,
	}
}

func privateVehicleSightingValues(items []spacetimePrivateVehicleSightingJSON) []model.VehicleSighting {
	if items == nil {
		return nil
	}
	out := make([]model.VehicleSighting, len(items))
	for index := range items {
		out[index] = *items[index].modelValue()
	}
	return out
}

type spacetimePrivateAreaReportJSON struct {
	ID           string    `json:"id"`
	UserID       int64     `json:"userId"`
	Latitude     float64   `json:"latitude"`
	Longitude    float64   `json:"longitude"`
	RadiusMeters int       `json:"radiusMeters"`
	Description  string    `json:"description"`
	ScopeKey     string    `json:"scopeKey"`
	Hidden       bool      `json:"hidden"`
	CreatedAt    time.Time `json:"createdAt"`
}

func (item *spacetimePrivateAreaReportJSON) modelValue() *model.AreaReport {
	if item == nil {
		return nil
	}
	return &model.AreaReport{
		ID: item.ID, UserID: item.UserID, Latitude: item.Latitude,
		Longitude: item.Longitude, RadiusMeters: item.RadiusMeters,
		Description: item.Description, ScopeKey: item.ScopeKey,
		Hidden: item.Hidden, CreatedAt: item.CreatedAt,
	}
}

func privateAreaReportValues(items []spacetimePrivateAreaReportJSON) []model.AreaReport {
	if items == nil {
		return nil
	}
	out := make([]model.AreaReport, len(items))
	for index := range items {
		out[index] = *items[index].modelValue()
	}
	return out
}

type spacetimePrivateIncidentVoteJSON struct {
	IncidentID string                  `json:"incidentId"`
	UserID     int64                   `json:"userId"`
	Nickname   string                  `json:"nickname"`
	Value      model.IncidentVoteValue `json:"value"`
	CreatedAt  time.Time               `json:"createdAt"`
	UpdatedAt  time.Time               `json:"updatedAt"`
}

func privateIncidentVoteValues(items []spacetimePrivateIncidentVoteJSON) []model.IncidentVote {
	if items == nil {
		return nil
	}
	out := make([]model.IncidentVote, len(items))
	for index, item := range items {
		out[index] = model.IncidentVote{
			IncidentID: item.IncidentID, UserID: item.UserID,
			Nickname: item.Nickname, Value: item.Value,
			CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		}
	}
	return out
}

type spacetimePrivateIncidentVoteEventJSON struct {
	ID         string                   `json:"id"`
	IncidentID string                   `json:"incidentId"`
	UserID     int64                    `json:"userId"`
	Nickname   string                   `json:"nickname"`
	Value      model.IncidentVoteValue  `json:"value"`
	Source     model.IncidentVoteSource `json:"source"`
	CreatedAt  time.Time                `json:"createdAt"`
}

func privateIncidentVoteEventValues(items []spacetimePrivateIncidentVoteEventJSON) []model.IncidentVoteEvent {
	if items == nil {
		return nil
	}
	out := make([]model.IncidentVoteEvent, len(items))
	for index, item := range items {
		out[index] = model.IncidentVoteEvent{
			ID: item.ID, IncidentID: item.IncidentID, UserID: item.UserID,
			Nickname: item.Nickname, Value: item.Value, Source: item.Source,
			CreatedAt: item.CreatedAt,
		}
	}
	return out
}

type spacetimePrivateIncidentCommentJSON struct {
	ID         string    `json:"id"`
	IncidentID string    `json:"incidentId"`
	UserID     int64     `json:"userId"`
	Nickname   string    `json:"nickname"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

func privateIncidentCommentValues(items []spacetimePrivateIncidentCommentJSON) []model.IncidentComment {
	if items == nil {
		return nil
	}
	out := make([]model.IncidentComment, len(items))
	for index, item := range items {
		out[index] = model.IncidentComment{
			ID: item.ID, IncidentID: item.IncidentID, UserID: item.UserID,
			Nickname: item.Nickname, Body: item.Body, CreatedAt: item.CreatedAt,
		}
	}
	return out
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

type spacetimeAreaReportJSON struct {
	ID           string    `json:"id"`
	StableID     string    `json:"stableId"`
	UserID       string    `json:"userId"`
	Latitude     float64   `json:"latitude"`
	Longitude    float64   `json:"longitude"`
	RadiusMeters int       `json:"radiusMeters"`
	Description  string    `json:"description"`
	ScopeKey     string    `json:"scopeKey"`
	Hidden       bool      `json:"hidden"`
	CreatedAt    time.Time `json:"createdAt"`
}

func spacetimeAreaReportPayload(item model.AreaReport) spacetimeAreaReportJSON {
	return spacetimeAreaReportJSON{
		ID:           item.ID,
		StableID:     spacetimeStableID(item.UserID),
		UserID:       spacetimeUserID(item.UserID),
		Latitude:     item.Latitude,
		Longitude:    item.Longitude,
		RadiusMeters: item.RadiusMeters,
		Description:  item.Description,
		ScopeKey:     item.ScopeKey,
		Hidden:       item.Hidden,
		CreatedAt:    item.CreatedAt,
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

type spacetimeChatAnalyzerMessageJSON struct {
	ID               string `json:"id"`
	ChatID           string `json:"chatId"`
	MessageID        any    `json:"messageId"`
	SenderID         any    `json:"senderId"`
	SenderStableID   string `json:"senderStableId"`
	SenderNickname   string `json:"senderNickname"`
	Text             string `json:"text"`
	MessageDate      string `json:"messageDate"`
	ReceivedAt       string `json:"receivedAt"`
	ReplyToMessageID any    `json:"replyToMessageId"`
	Status           string `json:"status"`
	Attempts         int    `json:"attempts"`
	AnalysisJSON     string `json:"analysisJson"`
	AppliedActionID  string `json:"appliedActionId"`
	AppliedTargetKey string `json:"appliedTargetKey"`
	BatchID          string `json:"batchId,omitempty"`
	LastError        string `json:"lastError"`
	ProcessedAt      string `json:"processedAt"`
}

type spacetimeChatAnalyzerBatchJSON struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	DryRun        bool   `json:"dryRun"`
	StartedAt     string `json:"startedAt"`
	FinishedAt    string `json:"finishedAt"`
	MessageCount  int    `json:"messageCount"`
	ReportCount   int    `json:"reportCount"`
	VoteCount     int    `json:"voteCount"`
	IgnoredCount  int    `json:"ignoredCount"`
	WouldApply    int    `json:"wouldApply"`
	AppliedCount  int    `json:"appliedCount"`
	ErrorCount    int    `json:"errorCount"`
	Model         string `json:"model"`
	SelectedModel string `json:"selectedModel"`
	ResultJSON    string `json:"resultJson"`
	Error         string `json:"error"`
}

func spacetimeChatAnalyzerMessagePayload(item model.ChatAnalyzerMessage) spacetimeChatAnalyzerMessageJSON {
	status := item.Status
	if status == "" {
		status = model.ChatAnalyzerMessagePending
	}
	return spacetimeChatAnalyzerMessageJSON{
		ID:               item.ID,
		ChatID:           item.ChatID,
		MessageID:        strconv.FormatInt(item.MessageID, 10),
		SenderID:         strconv.FormatInt(item.SenderID, 10),
		SenderStableID:   item.SenderStableID,
		SenderNickname:   item.SenderNickname,
		Text:             item.Text,
		MessageDate:      item.MessageDate.UTC().Format(time.RFC3339),
		ReceivedAt:       item.ReceivedAt.UTC().Format(time.RFC3339),
		ReplyToMessageID: strconv.FormatInt(item.ReplyToMessageID, 10),
		Status:           string(status),
		Attempts:         item.Attempts,
		AnalysisJSON:     item.AnalysisJSON,
		AppliedActionID:  item.AppliedActionID,
		AppliedTargetKey: item.AppliedTargetKey,
		BatchID:          item.BatchID,
		LastError:        item.LastError,
		ProcessedAt:      formatOptionalTime(item.ProcessedAt),
	}
}

func spacetimeChatAnalyzerBatchPayload(batch model.ChatAnalyzerBatch) spacetimeChatAnalyzerBatchJSON {
	status := batch.Status
	if status == "" {
		status = model.ChatAnalyzerBatchRunning
	}
	return spacetimeChatAnalyzerBatchJSON{
		ID:            strings.TrimSpace(batch.ID),
		Status:        string(status),
		DryRun:        batch.DryRun,
		StartedAt:     batch.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:    formatOptionalTime(batch.FinishedAt),
		MessageCount:  batch.MessageCount,
		ReportCount:   batch.ReportCount,
		VoteCount:     batch.VoteCount,
		IgnoredCount:  batch.IgnoredCount,
		WouldApply:    batch.WouldApply,
		AppliedCount:  batch.AppliedCount,
		ErrorCount:    batch.ErrorCount,
		Model:         strings.TrimSpace(batch.Model),
		SelectedModel: strings.TrimSpace(batch.SelectedModel),
		ResultJSON:    batch.ResultJSON,
		Error:         batch.Error,
	}
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

func decodeSpacetimeChatAnalyzerMessage(raw spacetimeChatAnalyzerMessageJSON) (model.ChatAnalyzerMessage, error) {
	messageID, err := parseSpacetimeInt64(raw.MessageID, "messageId")
	if err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	senderID, err := parseSpacetimeInt64(raw.SenderID, "senderId")
	if err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	replyToMessageID, err := parseSpacetimeInt64(raw.ReplyToMessageID, "replyToMessageId")
	if err != nil {
		return model.ChatAnalyzerMessage{}, err
	}
	messageDate, err := time.Parse(time.RFC3339, strings.TrimSpace(raw.MessageDate))
	if err != nil {
		return model.ChatAnalyzerMessage{}, fmt.Errorf("parse chat analyzer messageDate: %w", err)
	}
	receivedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(raw.ReceivedAt))
	if err != nil {
		return model.ChatAnalyzerMessage{}, fmt.Errorf("parse chat analyzer receivedAt: %w", err)
	}
	processedAt, err := parseOptionalTime(raw.ProcessedAt)
	if err != nil {
		return model.ChatAnalyzerMessage{}, fmt.Errorf("parse chat analyzer processedAt: %w", err)
	}
	return model.ChatAnalyzerMessage{
		ID:               strings.TrimSpace(raw.ID),
		ChatID:           strings.TrimSpace(raw.ChatID),
		MessageID:        messageID,
		SenderID:         senderID,
		SenderStableID:   strings.TrimSpace(raw.SenderStableID),
		SenderNickname:   strings.TrimSpace(raw.SenderNickname),
		Text:             raw.Text,
		MessageDate:      messageDate,
		ReceivedAt:       receivedAt,
		ReplyToMessageID: replyToMessageID,
		Status:           model.ChatAnalyzerMessageStatus(strings.TrimSpace(raw.Status)),
		Attempts:         raw.Attempts,
		AnalysisJSON:     raw.AnalysisJSON,
		AppliedActionID:  strings.TrimSpace(raw.AppliedActionID),
		AppliedTargetKey: strings.TrimSpace(raw.AppliedTargetKey),
		BatchID:          strings.TrimSpace(raw.BatchID),
		LastError:        raw.LastError,
		ProcessedAt:      processedAt,
	}, nil
}

func parseSpacetimeInt64(value any, field string) (int64, error) {
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse chat analyzer %s: %w", field, err)
		}
		return parsed, nil
	case float64:
		parsed := int64(typed)
		if typed != float64(parsed) {
			return 0, fmt.Errorf("parse chat analyzer %s: non-integer number %v", field, typed)
		}
		return parsed, nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0, fmt.Errorf("parse chat analyzer %s: %w", field, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("parse chat analyzer %s: unsupported value %T", field, value)
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
