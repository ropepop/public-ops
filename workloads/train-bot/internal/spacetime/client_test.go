package spacetime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCallJSONProcedureWithTokenUsesCanonicalProcedureNameOnce(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/database/train-db/call/trainbot_get_public_dashboard":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"trains":[]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}

	payload, err := syncer.callJSONProcedureWithToken(context.Background(), "get_public_dashboard", []any{5}, "service-token")
	if err != nil {
		t.Fatalf("call procedure: %v", err)
	}
	raw, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("expected map payload, got %T", payload)
	}
	if trains, ok := raw["trains"].([]any); !ok || len(trains) != 0 {
		t.Fatalf("expected empty trains payload, got %#v", raw["trains"])
	}
	if got, want := paths, []string{
		"/v1/database/train-db/call/trainbot_get_public_dashboard",
	}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected request paths: got %v want %v", got, want)
	}
}

func TestCleanupExpiredStateUsesThreeArgumentCompatibilityContractWithoutSummarySQL(t *testing.T) {
	var capturedPaths []string
	var capturedArgs []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPaths = append(capturedPaths, r.URL.Path)
		switch r.URL.Path {
		case "/v1/database/train-db/call/trainbot_cleanup_expired_state":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(body, &capturedArgs); err != nil {
				t.Fatalf("decode args: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`null`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}
	now := time.Date(2026, 3, 29, 7, 36, 12, 0, time.UTC)
	retentionCutoff := now.Add(-24 * time.Hour)

	result, err := syncer.CleanupExpiredState(context.Background(), now, retentionCutoff, "2026-03-28")
	if err != nil {
		t.Fatalf("cleanup expired state: %v", err)
	}
	if got, want := capturedPaths, []string{
		"/v1/database/train-db/call/trainbot_cleanup_expired_state",
	}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected cleanup paths: got %v want %v", got, want)
	}
	if len(capturedArgs) != 3 {
		t.Fatalf("expected 3 cleanup args, got %d", len(capturedArgs))
	}
	if got := strings.TrimSpace(capturedArgs[0].(string)); got != now.Format(time.RFC3339) {
		t.Fatalf("unexpected now arg: %s", got)
	}
	if got := strings.TrimSpace(capturedArgs[1].(string)); got != retentionCutoff.Format(time.RFC3339) {
		t.Fatalf("unexpected retention cutoff arg: %s", got)
	}
	if got := strings.TrimSpace(capturedArgs[2].(string)); got != "2026-03-28" {
		t.Fatalf("unexpected oldest kept service date arg: %s", got)
	}
	if result != (CleanupExpiredStateResult{}) {
		t.Fatalf("unexpected cleanup result: %+v", result)
	}
}

func TestCleanupExpiredStateTreatsMissingRequiredReducerAsLiveSchemaOutdated(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"External attempt to call nonexistent reducer \"trainbot_cleanup_expired_state\" failed."}`))
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}
	now := time.Date(2026, 3, 29, 7, 36, 12, 0, time.UTC)
	retentionCutoff := now.Add(-24 * time.Hour)

	_, err := syncer.CleanupExpiredState(context.Background(), now, retentionCutoff, "2026-03-28")
	if !errors.Is(err, ErrLiveSchemaOutdated) {
		t.Fatalf("expected live schema outdated error, got %v", err)
	}
	if got, want := paths, []string{
		"/v1/database/train-db/call/trainbot_cleanup_expired_state",
	}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected cleanup paths: got %v want %v", got, want)
	}
	if !strings.Contains(err.Error(), "required reducer trainbot_cleanup_expired_state") {
		t.Fatalf("expected canonical cleanup reducer name in error, got %v", err)
	}
}

func TestServiceGetSchedulePrefersProcedurePayload(t *testing.T) {
	var paths []string
	var capturedArgs []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/v1/database/train-db/call/trainbot_service_get_schedule" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read procedure body: %v", err)
		}
		if err := json.Unmarshal(body, &capturedArgs); err != nil {
			t.Fatalf("decode procedure args: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"serviceDay":{"serviceDate":"2026-04-10","sourceVersion":"agg-2026-04-10","importedAt":"2026-04-09T21:36:56Z","stations":[{"id":"jelgava","name":"Jelgava","normalizedKey":"jelgava"},{"id":"riga","name":"Riga","normalizedKey":"riga"}]},"trips":[{"id":"train-1","serviceDate":"2026-04-10","fromStationId":"riga","fromStationName":"Riga","toStationId":"jelgava","toStationName":"Jelgava","departureAt":"2026-04-10T06:00:00Z","arrivalAt":"2026-04-10T06:45:00Z","sourceVersion":"agg-2026-04-10","stops":[{"trainInstanceId":"train-1","stationId":"riga","stationName":"Riga","seq":1,"departureAt":"2026-04-10T06:00:00Z","latitude":56.9496,"longitude":24.1052},{"trainInstanceId":"train-1","stationId":"jelgava","stationName":"Jelgava","seq":2,"arrivalAt":"2026-04-10T06:45:00Z","latitude":56.6511,"longitude":23.7128}]}]}`))
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}

	serviceDay, trips, err := syncer.ServiceGetSchedule(context.Background(), "2026-04-10")
	if err != nil {
		t.Fatalf("service get schedule: %v", err)
	}
	if serviceDay == nil || serviceDay.ServiceDate != "2026-04-10" {
		t.Fatalf("unexpected service day: %+v", serviceDay)
	}
	if len(serviceDay.Stations) != 2 || serviceDay.Stations[0].ID != "jelgava" || serviceDay.Stations[1].ID != "riga" {
		t.Fatalf("unexpected service day stations: %+v", serviceDay.Stations)
	}
	if len(trips) != 1 || trips[0].ID != "train-1" {
		t.Fatalf("unexpected trips: %+v", trips)
	}
	if len(trips[0].Stops) != 2 || trips[0].Stops[0].StationID != "riga" || trips[0].Stops[1].StationID != "jelgava" {
		t.Fatalf("unexpected trip stops: %+v", trips[0].Stops)
	}
	if got, want := paths, []string{"/v1/database/train-db/call/trainbot_service_get_schedule"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected service get schedule paths: got %v want %v", got, want)
	}
	if len(capturedArgs) != 1 || strings.TrimSpace(capturedArgs[0].(string)) != "2026-04-10" {
		t.Fatalf("unexpected service get schedule args: %+v", capturedArgs)
	}
}

func TestServiceListActivitiesPrefersProcedurePayload(t *testing.T) {
	var paths []string
	var capturedArgs []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/v1/database/train-db/call/trainbot_service_list_activities" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read procedure body: %v", err)
		}
		if err := json.Unmarshal(body, &capturedArgs); err != nil {
			t.Fatalf("decode procedure args: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"activities":[{"id":"train:train-1:2026-04-10","scopeType":"train","subjectId":"train-1","subjectName":"Riga -> Jelgava","serviceDate":"2026-04-10","summary":{"lastReportName":"Inspection in my car","lastReportAt":"2026-04-10T06:10:00Z","lastActivityName":"Inspection in my car","lastActivityAt":"2026-04-10T06:10:00Z","lastActivityActor":"Amber Scout 123","lastReporter":"Amber Scout 123"},"timeline":[],"comments":[],"votes":[]}]}`))
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}
	since := time.Date(2026, 4, 10, 6, 0, 0, 0, time.UTC)

	activities, err := syncer.ServiceListActivities(context.Background(), ListActivitiesFilter{
		Since:       &since,
		ScopeType:   "train",
		SubjectID:   "train-1",
		ServiceDate: "2026-04-10",
	})
	if err != nil {
		t.Fatalf("service list activities: %v", err)
	}
	if len(activities) != 1 || activities[0].ID != "train:train-1:2026-04-10" {
		t.Fatalf("unexpected activities: %+v", activities)
	}
	if got, want := paths, []string{"/v1/database/train-db/call/trainbot_service_list_activities"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected activity paths: got %v want %v", got, want)
	}
	if len(capturedArgs) != 4 {
		t.Fatalf("unexpected activity args: %+v", capturedArgs)
	}
	if strings.TrimSpace(capturedArgs[0].(string)) != since.Format(time.RFC3339) ||
		strings.TrimSpace(capturedArgs[1].(string)) != "train" ||
		strings.TrimSpace(capturedArgs[2].(string)) != "train-1" ||
		strings.TrimSpace(capturedArgs[3].(string)) != "2026-04-10" {
		t.Fatalf("unexpected activity args: %+v", capturedArgs)
	}
}

func TestServiceSchedulePresentUsesScalarServiceDayQuery(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/database/train-db/sql" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read sql body: %v", err)
		}
		queries = append(queries, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"schema":{"elements":[{"name":"serviceDate"}]},"rows":[["2026-04-10"]],"total_duration_micros":0,"stats":{"rows_inserted":0,"rows_deleted":0,"rows_updated":0}}]`))
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}

	present, err := syncer.ServiceSchedulePresent(context.Background(), "2026-04-10")
	if err != nil {
		t.Fatalf("service schedule present: %v", err)
	}
	if !present {
		t.Fatalf("expected service date to be present")
	}
	if len(queries) != 1 {
		t.Fatalf("expected 1 sql query, got %d", len(queries))
	}
	if got, want := strings.TrimSpace(queries[0]), "SELECT serviceDate FROM trainbot_service_day WHERE serviceDate = '2026-04-10' LIMIT 1"; got != want {
		t.Fatalf("unexpected service day presence query: got %q want %q", got, want)
	}
}

func TestCallJSONProcedureWithTokenCachesMissingRequiredProcedureAsLiveSchemaOutdated(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"External attempt to call nonexistent procedure \"service_get_schedule\" failed."}`))
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}

	_, err := syncer.callJSONProcedureWithToken(context.Background(), "service_get_schedule", []any{"2026-03-30"}, "service-token")
	if !errors.Is(err, ErrLiveSchemaOutdated) {
		t.Fatalf("expected live schema outdated error, got %v", err)
	}
	if !strings.Contains(err.Error(), "trainbot_service_get_schedule") {
		t.Fatalf("expected canonical procedure name in error, got %v", err)
	}
	if got, want := paths, []string{
		"/v1/database/train-db/call/trainbot_service_get_schedule",
	}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected request paths: got %v want %v", got, want)
	}

	_, err = syncer.callJSONProcedureWithToken(context.Background(), "service_get_schedule", []any{"2026-03-30"}, "service-token")
	if !errors.Is(err, ErrLiveSchemaOutdated) {
		t.Fatalf("expected cached live schema outdated error, got %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected cached required procedure miss to avoid extra calls, got %v", paths)
	}
}

func testServiceTokenIssuer(t *testing.T) *serviceTokenIssuer {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return &serviceTokenIssuer{
		issuer:     "test-issuer",
		audience:   "spacetimedb",
		subject:    "service:test",
		roles:      []string{"train_service"},
		tokenTTL:   time.Minute,
		keyID:      keyIDForPublicKey(&privateKey.PublicKey),
		privateKey: privateKey,
	}
}
