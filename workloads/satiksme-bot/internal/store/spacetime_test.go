package store

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"satiksmebot/internal/model"
	"satiksmebot/internal/spacetime"
)

func TestNormalizeAreaReportVoteErrorRecognizesPublishedSchemaMismatch(t *testing.T) {
	procedureErr := errors.New("spacetime procedure failed: The module instance encountered a fatal error: report and vote incident mismatch")
	mapped := normalizeAreaReportVoteError(procedureErr)
	if !errors.Is(mapped, ErrReportVoteIncidentMismatch) {
		t.Fatalf("mapped error = %v, want ErrReportVoteIncidentMismatch", mapped)
	}
	other := errors.New("temporary transport failure")
	if got := normalizeAreaReportVoteError(other); got != other {
		t.Fatalf("unrelated error = %v, want original %v", got, other)
	}
}

func TestSpacetimeAreaReportVoteMapsPublishedSchemaMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_record_area_report_with_vote") {
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": "The module instance encountered a fatal error: report and vote incident mismatch",
		})
	}))
	defer server.Close()

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              server.URL,
		Database:          "satiksme-bot-test",
		JWTPrivateKeyFile: writeStoreTestRSAKey(t),
		HTTPTimeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}
	st := NewSpacetimeStore(syncer)
	now := time.Date(2026, 7, 22, 3, 15, 0, 0, time.UTC)
	err = st.InsertAreaReportWithVote(context.Background(), model.AreaReport{
		ID: "area-report", UserID: 901, ScopeKey: "56950:24110:500:tunelis", CreatedAt: now,
	}, model.IncidentVote{
		IncidentID: "area:pub-current", UserID: 901, Value: model.IncidentVoteOngoing, CreatedAt: now, UpdatedAt: now,
	}, model.IncidentVoteEvent{
		ID: "area-event", IncidentID: "area:pub-current", UserID: 901, Value: model.IncidentVoteOngoing, CreatedAt: now,
	}, 90*time.Second)
	if !errors.Is(err, ErrReportVoteIncidentMismatch) {
		t.Fatalf("InsertAreaReportWithVote() error = %v, want ErrReportVoteIncidentMismatch", err)
	}
}

func TestSpacetimePayloadsExposeStableTelegramIdentity(t *testing.T) {
	createdAt := time.Date(2026, 4, 24, 10, 30, 0, 0, time.UTC)
	stop := spacetimeStopSightingPayload(model.StopSighting{
		ID:        "stop-1",
		StopID:    "1033a",
		UserID:    777001,
		CreatedAt: createdAt,
	})
	vote := spacetimeIncidentVotePayload(model.IncidentVote{
		IncidentID: "stop:1033a",
		UserID:     777001,
		Value:      model.IncidentVoteOngoing,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	})

	assertIdentityJSON(t, stop)
	assertIdentityJSON(t, vote)
}

func TestSpacetimeChatAnalyzerUsesProductionBaselineProceduresOnly(t *testing.T) {
	var chatStore ChatAnalyzerStore = (*SpacetimeStore)(nil)
	if _, ok := chatStore.(ChatAnalyzerMessageExpiryStore); ok {
		t.Fatal("SpacetimeStore must not require the unpublished bulk message-expiry procedure")
	}
	if _, ok := chatStore.(ChatAnalyzerBatchRecoveryStore); ok {
		t.Fatal("SpacetimeStore must not require the unpublished stale-batch procedure")
	}
	if _, ok := chatStore.(ChatAnalyzerBatchFinalizer); ok {
		t.Fatal("SpacetimeStore must not require an unpublished atomic batch-finalizer procedure")
	}
}

func TestSpacetimePrivateReportListsPreserveZeroAsUnlimited(t *testing.T) {
	t.Parallel()
	called := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args []any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("decode %s args: %v", r.URL.Path, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(args) == 0 || args[len(args)-1] != float64(0) {
			t.Errorf("%s args = %#v, want trailing numeric zero for unlimited lookup", r.URL.Path, args)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_stop_sightings_since"):
			called["stop"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"sightings": []map[string]any{{
				"id": "stop-idempotent", "stopId": "3012", "userId": 701, "createdAt": "2026-07-22T01:00:00Z",
			}}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_vehicle_sightings_since"):
			called["vehicle"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"sightings": []map[string]any{{
				"id": "vehicle-idempotent", "userId": 702, "mode": "bus", "routeLabel": "22", "direction": "a-b",
				"destination": "Lidosta", "departureSeconds": 300, "liveRowId": "live-22", "scopeKey": "live:bus:22:a-b:live-22",
				"createdAt": "2026-07-22T01:00:00Z",
			}}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_area_reports_since"):
			called["area"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"reports": []map[string]any{{
				"id": "area-idempotent", "userId": 703, "latitude": 56.95, "longitude": 24.11,
				"radiusMeters": 250, "description": "Centraltirgus", "scopeKey": "scope-area",
				"createdAt": "2026-07-22T01:00:00Z",
			}}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_incident_vote_events"):
			called["events"]++
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []map[string]any{{
				"id": "chat-action-committed", "incidentId": "stop:3012", "userId": 701,
				"nickname": "Amber 701", "value": "ONGOING", "source": "telegram_chat",
				"createdAt": "2026-07-22T01:00:00Z",
			}}})
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              server.URL,
		Database:          "satiksme-bot-test",
		JWTPrivateKeyFile: writeStoreTestRSAKey(t),
		HTTPTimeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}
	st := NewSpacetimeStore(syncer)
	since := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	stops, err := st.ListStopSightingsSince(context.Background(), since, "", 0)
	if err != nil || len(stops) != 1 || stops[0].ID != "stop-idempotent" {
		t.Fatalf("stop unlimited lookup = %+v err=%v", stops, err)
	}
	vehicles, err := st.ListVehicleSightingsSince(context.Background(), since, "", 0)
	if err != nil || len(vehicles) != 1 || vehicles[0].ID != "vehicle-idempotent" {
		t.Fatalf("vehicle unlimited lookup = %+v err=%v", vehicles, err)
	}
	areas, err := st.ListAreaReportsSince(context.Background(), since, 0)
	if err != nil || len(areas) != 1 || areas[0].ID != "area-idempotent" {
		t.Fatalf("area unlimited lookup = %+v err=%v", areas, err)
	}
	events, err := st.ListIncidentVoteEvents(context.Background(), "", since, 0)
	if err != nil || len(events) != 1 || events[0].ID != "chat-action-committed" {
		t.Fatalf("vote-event unlimited lookup = %+v err=%v", events, err)
	}
	if called["stop"] != 1 || called["vehicle"] != 1 || called["area"] != 1 || called["events"] != 1 {
		t.Fatalf("procedure calls = %+v, want each private report list once", called)
	}
}

func TestSpacetimePrivateReadWirePreservesSensitiveFields(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 22, 5, 6, 7, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	stopWire := map[string]any{
		"id": "stop-private", "stopId": "3012", "userId": 7101,
		"hidden": true, "createdAt": createdAt.Format(time.RFC3339),
	}
	vehicleWire := map[string]any{
		"id": "vehicle-private", "stopId": "3012", "userId": 7102,
		"mode": "bus", "routeLabel": "22", "direction": "a-b", "destination": "Lidosta",
		"departureSeconds": 180, "liveRowId": "private-live-row", "scopeKey": "private-vehicle-scope",
		"hidden": true, "createdAt": createdAt.Format(time.RFC3339),
	}
	areaWire := map[string]any{
		"id": "area-private", "userId": 7103, "latitude": 56.95, "longitude": 24.11,
		"radiusMeters": 250, "description": "private area", "scopeKey": "private-area-scope",
		"hidden": true, "createdAt": createdAt.Format(time.RFC3339),
	}
	voteWire := map[string]any{
		"incidentId": "vehicle:pub-12345678", "userId": 7104, "nickname": "Private Vote",
		"value": "CLEARED", "createdAt": createdAt.Format(time.RFC3339), "updatedAt": updatedAt.Format(time.RFC3339),
	}
	eventWire := map[string]any{
		"id": "event-private", "incidentId": "vehicle:pub-12345678", "userId": 7105,
		"nickname": "Private Event", "value": "ONGOING", "source": "telegram_chat",
		"createdAt": createdAt.Format(time.RFC3339),
	}
	commentWire := map[string]any{
		"id": "comment-private", "incidentId": "vehicle:pub-12345678", "userId": 7106,
		"nickname": "Private Comment", "body": "private body", "createdAt": createdAt.Format(time.RFC3339),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_get_last_stop_sighting"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sighting": stopWire})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_stop_sightings_since"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sightings": []any{stopWire}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_get_last_vehicle_sighting"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sighting": vehicleWire})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_vehicle_sightings_since"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sightings": []any{vehicleWire}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_get_last_area_report"):
			_ = json.NewEncoder(w).Encode(map[string]any{"report": areaWire})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_area_reports_since"):
			_ = json.NewEncoder(w).Encode(map[string]any{"reports": []any{areaWire}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_incident_votes"):
			_ = json.NewEncoder(w).Encode(map[string]any{"votes": []any{voteWire}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_incident_vote_events"):
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{eventWire}})
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_incident_comments"):
			_ = json.NewEncoder(w).Encode(map[string]any{"comments": []any{commentWire}})
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host: server.URL, Database: "satiksme-bot-test",
		JWTPrivateKeyFile: writeStoreTestRSAKey(t), HTTPTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}
	st := NewSpacetimeStore(syncer)
	ctx := context.Background()
	since := createdAt.Add(-time.Hour)

	wantStop := model.StopSighting{ID: "stop-private", StopID: "3012", UserID: 7101, Hidden: true, CreatedAt: createdAt}
	stop, err := st.GetLastStopSightingByUserScope(ctx, 7101, "3012")
	if err != nil || stop == nil || !reflect.DeepEqual(*stop, wantStop) {
		t.Fatalf("GetLastStopSightingByUserScope() = %+v err=%v, want %+v", stop, err, wantStop)
	}
	stops, err := st.ListStopSightingsSince(ctx, since, "", 0)
	if err != nil || !reflect.DeepEqual(stops, []model.StopSighting{wantStop}) {
		t.Fatalf("ListStopSightingsSince() = %+v err=%v", stops, err)
	}

	wantVehicle := model.VehicleSighting{
		ID: "vehicle-private", StopID: "3012", UserID: 7102, Mode: "bus", RouteLabel: "22",
		Direction: "a-b", Destination: "Lidosta", DepartureSeconds: 180, LiveRowID: "private-live-row",
		ScopeKey: "private-vehicle-scope", Hidden: true, CreatedAt: createdAt,
	}
	vehicle, err := st.GetLastVehicleSightingByUserScope(ctx, 7102, wantVehicle.ScopeKey)
	if err != nil || vehicle == nil || !reflect.DeepEqual(*vehicle, wantVehicle) {
		t.Fatalf("GetLastVehicleSightingByUserScope() = %+v err=%v, want %+v", vehicle, err, wantVehicle)
	}
	vehicles, err := st.ListVehicleSightingsSince(ctx, since, "", 0)
	if err != nil || !reflect.DeepEqual(vehicles, []model.VehicleSighting{wantVehicle}) {
		t.Fatalf("ListVehicleSightingsSince() = %+v err=%v", vehicles, err)
	}

	wantArea := model.AreaReport{
		ID: "area-private", UserID: 7103, Latitude: 56.95, Longitude: 24.11,
		RadiusMeters: 250, Description: "private area", ScopeKey: "private-area-scope",
		Hidden: true, CreatedAt: createdAt,
	}
	area, err := st.GetLastAreaReportByUserScope(ctx, 7103, wantArea.ScopeKey)
	if err != nil || area == nil || !reflect.DeepEqual(*area, wantArea) {
		t.Fatalf("GetLastAreaReportByUserScope() = %+v err=%v, want %+v", area, err, wantArea)
	}
	areas, err := st.ListAreaReportsSince(ctx, since, 0)
	if err != nil || !reflect.DeepEqual(areas, []model.AreaReport{wantArea}) {
		t.Fatalf("ListAreaReportsSince() = %+v err=%v", areas, err)
	}

	wantVote := model.IncidentVote{
		IncidentID: "vehicle:pub-12345678", UserID: 7104, Nickname: "Private Vote",
		Value: model.IncidentVoteCleared, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	votes, err := st.ListIncidentVotes(ctx, wantVote.IncidentID)
	if err != nil || !reflect.DeepEqual(votes, []model.IncidentVote{wantVote}) {
		t.Fatalf("ListIncidentVotes() = %+v err=%v", votes, err)
	}
	wantEvent := model.IncidentVoteEvent{
		ID: "event-private", IncidentID: wantVote.IncidentID, UserID: 7105, Nickname: "Private Event",
		Value: model.IncidentVoteOngoing, Source: model.IncidentVoteSourceTelegramChat, CreatedAt: createdAt,
	}
	events, err := st.ListIncidentVoteEvents(ctx, wantVote.IncidentID, since, 0)
	if err != nil || !reflect.DeepEqual(events, []model.IncidentVoteEvent{wantEvent}) {
		t.Fatalf("ListIncidentVoteEvents() = %+v err=%v", events, err)
	}
	wantComment := model.IncidentComment{
		ID: "comment-private", IncidentID: wantVote.IncidentID, UserID: 7106,
		Nickname: "Private Comment", Body: "private body", CreatedAt: createdAt,
	}
	comments, err := st.ListIncidentComments(ctx, wantVote.IncidentID, 0)
	if err != nil || !reflect.DeepEqual(comments, []model.IncidentComment{wantComment}) {
		t.Fatalf("ListIncidentComments() = %+v err=%v", comments, err)
	}

	publicJSON, err := json.Marshal(struct {
		Vehicle model.VehicleSighting `json:"vehicle"`
		Vote    model.IncidentVote    `json:"vote"`
	}{Vehicle: vehicles[0], Vote: votes[0]})
	if err != nil {
		t.Fatalf("marshal public model shape: %v", err)
	}
	for _, privateField := range []string{`"userId"`, `"hidden"`, `"scopeKey"`} {
		if strings.Contains(string(publicJSON), privateField) {
			t.Fatalf("public model JSON exposed %s: %s", privateField, publicJSON)
		}
	}
}

func TestSpacetimeCommentCountFallsBackWhenProcedureMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_count_incident_comments_by_user_since"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"External attempt to call nonexistent procedure \"satiksmebot_service_count_incident_comments_by_user_since\" failed."}`))
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_count_incident_comments_by_incident_since"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"External attempt to call nonexistent procedure \"satiksmebot_service_count_incident_comments_by_incident_since\" failed."}`))
		case strings.HasSuffix(r.URL.Path, "/call/satiksmebot_service_list_incident_comments"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"comments": []map[string]any{
					{
						"id":         "comment-old",
						"incidentId": "stop:1",
						"userId":     1001,
						"nickname":   "Amber 001",
						"body":       "old",
						"createdAt":  "2026-03-18T10:00:00Z",
					},
					{
						"id":         "comment-new",
						"incidentId": "stop:1",
						"userId":     1002,
						"nickname":   "Amber 002",
						"body":       "new",
						"createdAt":  "2026-03-18T11:45:00Z",
					},
				},
			})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	syncer, err := spacetime.NewSyncer(spacetime.SyncConfig{
		Host:              server.URL,
		Database:          "satiksme-bot-test",
		JWTPrivateKeyFile: writeStoreTestRSAKey(t),
		HTTPTimeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("NewSyncer() error = %v", err)
	}
	st := NewSpacetimeStore(syncer)
	since := time.Date(2026, time.March, 18, 11, 30, 0, 0, time.UTC)

	userCount, err := st.CountIncidentCommentsByUserSince(context.Background(), 1002, since)
	if err != nil {
		t.Fatalf("CountIncidentCommentsByUserSince() error = %v", err)
	}
	if userCount != 0 {
		t.Fatalf("CountIncidentCommentsByUserSince() = %d, want missing-procedure fallback 0", userCount)
	}

	incidentCount, err := st.CountIncidentCommentsByIncidentSince(context.Background(), "stop:1", since)
	if err != nil {
		t.Fatalf("CountIncidentCommentsByIncidentSince() error = %v", err)
	}
	if incidentCount != 1 {
		t.Fatalf("CountIncidentCommentsByIncidentSince() = %d, want existing-list fallback 1", incidentCount)
	}
}

func TestSpacetimeReportDumpPayloadUsesLowerCamelFields(t *testing.T) {
	createdAt := time.Date(2026, 4, 24, 11, 0, 0, 0, time.UTC)
	nextAttemptAt := createdAt.Add(30 * time.Second)
	payload := spacetimeReportDumpPayload(ReportDumpItem{
		ID:            "dump-1",
		Payload:       "Kontrole pie pieturas",
		Attempts:      2,
		CreatedAt:     createdAt,
		NextAttemptAt: nextAttemptAt,
	})

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	raw := string(body)
	for _, want := range []string{`"id"`, `"payload"`, `"attempts"`, `"createdAt"`, `"nextAttemptAt"`, `"lastAttemptAt"`, `"lastError"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("report dump payload JSON = %s, want key %s", raw, want)
		}
	}
	for _, unwanted := range []string{`"ID"`, `"Payload"`, `"CreatedAt"`, `"NextAttemptAt"`} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("report dump payload JSON = %s, did not want exported Go key %s", raw, unwanted)
		}
	}
	if !strings.Contains(raw, `"payload":"Kontrole pie pieturas"`) {
		t.Fatalf("report dump payload JSON = %s, want non-empty payload", raw)
	}
}

func TestSpacetimeChatAnalyzerPayloadUsesPrivateLowerCamelFields(t *testing.T) {
	now := time.Date(2026, 4, 27, 9, 30, 0, 0, time.UTC)
	payload := spacetimeChatAnalyzerMessagePayload(model.ChatAnalyzerMessage{
		ID:               "chat:1:2",
		ChatID:           "chat:1",
		MessageID:        2,
		SenderID:         -10042,
		SenderStableID:   "telegram:-10042",
		SenderNickname:   "Amber Scout 123",
		Text:             "raw private text",
		MessageDate:      now,
		ReceivedAt:       now,
		ReplyToMessageID: 1,
		Status:           model.ChatAnalyzerMessagePending,
	})
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	raw := string(body)
	for _, want := range []string{`"chatId"`, `"messageId":"2"`, `"senderId":"-10042"`, `"text":"raw private text"`, `"replyToMessageId":"1"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("chat analyzer payload JSON = %s, want %s", raw, want)
		}
	}
	for _, unwanted := range []string{`"ChatID"`, `"MessageID"`, `"SenderID"`} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("chat analyzer payload JSON = %s, did not want Go key %s", raw, unwanted)
		}
	}
}

func TestSpacetimeChatAnalyzerBatchPayloadUsesPrivateLowerCamelFields(t *testing.T) {
	now := time.Date(2026, 4, 28, 8, 0, 0, 0, time.UTC)
	payload := spacetimeChatAnalyzerBatchPayload(model.ChatAnalyzerBatch{
		ID:            "batch-1",
		Status:        model.ChatAnalyzerBatchCompleted,
		DryRun:        true,
		StartedAt:     now,
		FinishedAt:    now.Add(time.Second),
		MessageCount:  5,
		ReportCount:   1,
		WouldApply:    1,
		Model:         "openrouter/free",
		SelectedModel: "qwen/free-picked",
		ResultJSON:    `{"reports":[],"votes":[],"ignored":[]}`,
	})
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	raw := string(body)
	for _, want := range []string{`"id":"batch-1"`, `"dryRun":true`, `"messageCount":5`, `"wouldApply":1`, `"selectedModel":"qwen/free-picked"`, `"resultJson"`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("chat analyzer batch payload JSON = %s, want %s", raw, want)
		}
	}
	for _, unwanted := range []string{`"ID"`, `"DryRun"`, `"SelectedModel"`} {
		if strings.Contains(raw, unwanted) {
			t.Fatalf("chat analyzer batch payload JSON = %s, did not want Go key %s", raw, unwanted)
		}
	}
}

func TestDecodeSpacetimeChatAnalyzerMessageAcceptsBlankProcessedAt(t *testing.T) {
	now := "2026-04-27T09:30:00Z"
	item, err := decodeSpacetimeChatAnalyzerMessage(spacetimeChatAnalyzerMessageJSON{
		ID:               "chat:1:2",
		ChatID:           "chat:1",
		MessageID:        float64(2),
		SenderID:         float64(-10042),
		SenderStableID:   "telegram:-10042",
		SenderNickname:   "Amber Scout 123",
		Text:             "raw private text",
		MessageDate:      now,
		ReceivedAt:       now,
		ReplyToMessageID: float64(1),
		Status:           string(model.ChatAnalyzerMessagePending),
		Attempts:         0,
		ProcessedAt:      "",
	})
	if err != nil {
		t.Fatalf("decodeSpacetimeChatAnalyzerMessage() error = %v", err)
	}
	if !item.ProcessedAt.IsZero() {
		t.Fatalf("ProcessedAt = %v, want zero time", item.ProcessedAt)
	}
	if item.MessageID != 2 || item.SenderID != -10042 || item.ReplyToMessageID != 1 {
		t.Fatalf("decoded item = %+v", item)
	}
}

func TestDecodeSpacetimeReportDumpPayloadAcceptsBlankLastAttemptAt(t *testing.T) {
	createdAt := "2026-04-24T11:00:00Z"
	nextAttemptAt := "2026-04-24T11:01:00Z"
	item, err := decodeSpacetimeReportDumpPayload(map[string]any{
		"item": map[string]any{
			"id":            "dump-1",
			"payload":       "Kontrole pie pieturas",
			"attempts":      0,
			"createdAt":     createdAt,
			"nextAttemptAt": nextAttemptAt,
			"lastAttemptAt": "",
			"lastError":     "",
		},
	})
	if err != nil {
		t.Fatalf("decodeSpacetimeReportDumpPayload() error = %v", err)
	}
	if item == nil {
		t.Fatalf("decodeSpacetimeReportDumpPayload() = nil")
	}
	if item.LastAttemptAt.IsZero() != true {
		t.Fatalf("LastAttemptAt = %v, want zero time", item.LastAttemptAt)
	}
	if item.ID != "dump-1" || item.Payload != "Kontrole pie pieturas" {
		t.Fatalf("decoded item = %+v", item)
	}
}

func assertIdentityJSON(t *testing.T, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload["stableId"] != "telegram:777001" {
		t.Fatalf("stableId = %#v, want telegram:777001 in %s", payload["stableId"], string(body))
	}
	if payload["userId"] != "777001" {
		t.Fatalf("userId = %#v, want 777001 in %s", payload["userId"], string(body))
	}
}

func writeStoreTestRSAKey(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "jwt-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
