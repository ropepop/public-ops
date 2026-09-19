package spacetime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestScheduleReplacementChunksLargeSnapshotsAndAbortsFailedUploads(t *testing.T) {
	const serviceDate = "2026-09-17"
	trips := make([]ScheduleTripBatchItem, 400)
	for i := range trips {
		trips[i] = ScheduleTripBatchItem{ID: fmt.Sprintf("train-%d", i), ServiceDate: serviceDate}
		for j := 0; j < 30; j++ {
			trips[i].Stops = append(trips[i].Stops, ScheduleStop{
				TrainInstanceID: trips[i].ID, StationID: fmt.Sprintf("station-%d", j),
				StationName: strings.Repeat("Rīga ", 20), Seq: j + 1,
			})
		}
	}
	if len(mustJSON(trips)) < 2<<20 {
		t.Fatal("fixture must exceed the production request limit")
	}
	for _, failUpload := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail_upload_%t", failUpload), func(t *testing.T) {
			var actions []string
			counts := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
				if err != nil {
					http.Error(w, "length limit exceeded", http.StatusRequestEntityTooLarge)
					return
				}
				action := strings.TrimPrefix(r.URL.Path, "/v1/database/train-db/call/trainbot_")
				actions = append(actions, action)
				var args []json.RawMessage
				if err := json.Unmarshal(body, &args); err != nil {
					t.Error(err)
				}
				if action == "append_service_day_chunk" {
					if failUpload {
						http.Error(w, "upload unavailable", http.StatusServiceUnavailable)
						return
					}
					var kind, payload string
					_ = json.Unmarshal(args[1], &kind)
					_ = json.Unmarshal(args[2], &payload)
					var rows []json.RawMessage
					if err := json.Unmarshal([]byte(payload), &rows); err != nil {
						t.Error(err)
					}
					counts[kind] += len(rows)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			syncer := &Syncer{baseURL: server.URL, database: "train-db", client: server.Client(), issuer: testServiceTokenIssuer(t)}
			err := syncer.ServiceReplaceSchedule(context.Background(), serviceDate, "test", []ScheduleStation{{ID: "riga", Name: "Rīga"}}, trips)
			if failUpload {
				if err == nil || strings.Join(actions, ",") != "begin_service_day_import,append_service_day_chunk,abort_service_day_import" {
					t.Fatalf("failed upload must abort without publishing: actions=%v err=%v", actions, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if actions[0] != "begin_service_day_import" || actions[len(actions)-1] != "commit_service_day_import" || counts["stations"] != 1 || counts["trips"] != 400 || counts["stops"] != 12000 {
				t.Fatalf("incomplete atomic import: actions=%v counts=%v", actions, counts)
			}
		})
	}
}

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

func TestServiceSchedulePresentUsesAuthorizedProcedure(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/database/train-db/call/trainbot_service_get_schedule" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "private table unavailable", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read procedure body: %v", err)
		}
		if string(body) != `["2026-04-10"]` || r.Header.Get("Authorization") == "" {
			t.Fatalf("missing service date or authentication")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"serviceDay":{"serviceDate":"2026-04-10"},"trips":[]}`))
		} else {
			_, _ = w.Write([]byte(`{"serviceDay":null,"trips":[]}`))
		}
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
	if present, err := syncer.ServiceSchedulePresent(context.Background(), "2026-04-10"); err != nil || present {
		t.Fatalf("expected absent service date: present=%v err=%v", present, err)
	}
}

func TestServiceGetTripUsesAuthorizedProcedure(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/database/train-db/call/trainbot_service_get_trip" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "private table unavailable", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read procedure body: %v", err)
		}
		if string(body) != `["train-1"]` || r.Header.Get("Authorization") == "" {
			t.Fatalf("missing train id or service authentication")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"trip":{"id":"train-1","serviceDate":"2026-04-10","fromStationId":"riga","fromStationName":"Riga","toStationId":"jelgava","toStationName":"Jelgava","departureAt":"2026-04-10T06:00:00Z","arrivalAt":"2026-04-10T06:45:00Z","sourceVersion":"agg-2026-04-10","stops":[{"stationId":"jelgava","stationName":"Jelgava","seq":2,"arrivalAt":"2026-04-10T06:45:00Z","longitude":23.7128},{"stationId":"riga","stationName":"Riga","seq":1,"departureAt":"2026-04-10T06:00:00Z","latitude":56.9496}]}}`))
		} else {
			_, _ = w.Write([]byte(`{"trip":null}`))
		}
	}))
	defer server.Close()

	syncer := &Syncer{
		baseURL:  server.URL,
		database: "train-db",
		client:   server.Client(),
		issuer:   testServiceTokenIssuer(t),
	}

	trip, err := syncer.ServiceGetTrip(context.Background(), " train-1 ")
	if err != nil {
		t.Fatalf("service get trip: %v", err)
	}
	if trip == nil || trip.ID != "train-1" {
		t.Fatalf("unexpected trip: %+v", trip)
	}
	if trip.SourceVersion != "agg-2026-04-10" {
		t.Fatalf("unexpected trip source version: %q", trip.SourceVersion)
	}
	if len(trip.Stops) != 2 || trip.Stops[0].StationID != "riga" || trip.Stops[1].StationID != "jelgava" {
		t.Fatalf("expected stops to be sorted in Go, got %+v", trip.Stops)
	}
	if trip.Stops[0].DepartureAt != "2026-04-10T06:00:00Z" || trip.Stops[1].ArrivalAt != "2026-04-10T06:45:00Z" {
		t.Fatalf("expected stop times to be preserved, got %+v", trip.Stops)
	}
	if trip.Stops[0].Latitude == nil || *trip.Stops[0].Latitude != 56.9496 || trip.Stops[1].Longitude == nil || *trip.Stops[1].Longitude != 23.7128 {
		t.Fatalf("expected coordinates to be preserved, got %+v", trip.Stops)
	}
	if calls != 1 {
		t.Fatalf("expected one trip procedure call, got %d", calls)
	}
	if trip, err := syncer.ServiceGetTrip(context.Background(), "train-1"); err != nil || trip != nil {
		t.Fatalf("expected missing trip to return nil, got trip=%+v err=%v", trip, err)
	}
}

func TestServiceReadsRequireProtectedProcedures(t *testing.T) {
	for _, name := range []string{"service_get_schedule", "service_list_activities"} {
		t.Run(name, func(t *testing.T) {
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprintf(w, `{"error":"External attempt to call nonexistent procedure \"trainbot_%s\" failed."}`, name)
			}))
			defer server.Close()
			syncer := &Syncer{baseURL: server.URL, database: "train-db", client: server.Client(), issuer: testServiceTokenIssuer(t)}
			var err error
			if name == "service_get_schedule" {
				_, _, err = syncer.ServiceGetSchedule(context.Background(), "2026-09-17")
			} else {
				_, err = syncer.ServiceListActivities(context.Background(), ListActivitiesFilter{ServiceDate: "2026-09-17"})
			}
			if !errors.Is(err, ErrLiveSchemaOutdated) {
				t.Fatalf("expected missing protected procedure error, got %v", err)
			}
			if len(paths) != 1 || paths[0] != "/v1/database/train-db/call/trainbot_"+name {
				t.Fatalf("read attempted an unsupported fallback: %v", paths)
			}
		})
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
