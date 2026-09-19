package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	trainapp "telegramtrainapp/internal/app"
	"telegramtrainapp/internal/spacetime"
	"telegramtrainapp/internal/store"
)

func TestLiveViewsUseOneAggregateRequest(t *testing.T) {
	server := newTestServerWithBaseURL(t, "https://example.test")
	now := time.Now().UTC()
	server.now = func() time.Time { return now }
	cookie, err := issueSessionCookie(server.sessionSecret, telegramAuth{User: telegramUser{ID: 7001}, AuthDate: now}, now)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(keyFile, pemEncodePKCS1PrivateKey(t), 0o600); err != nil {
		t.Fatal(err)
	}
	trains := make([]any, 334)
	for i := range trains {
		trains[i] = map[string]any{
			"train":    map[string]any{"id": "train", "sourceVersion": "private-source"},
			"status":   map[string]any{"state": "MIXED_REPORTS", "lastReportAt": ""},
			"timeline": []any{map[string]any{"eventLabel": "inspection", "count": 3}},
		}
	}
	calls := 0
	wantProcedure := ""
	wantArgs := []any{}
	wantUser := false
	fail := false
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/database/train-db/call/trainbot_"+wantProcedure {
			t.Errorf("unexpected remote request: %s", r.URL.Path)
		}
		var args []any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil || !reflect.DeepEqual(args, wantArgs) {
			t.Errorf("args = %#v, want %#v (%v)", args, wantArgs, err)
		}
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		if len(parts) != 3 {
			t.Fatal("missing signed authorization")
		}
		body, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			t.Error(err)
		}
		var claims struct {
			Subject string   `json:"sub"`
			Roles   []string `json:"roles"`
		}
		if err := json.Unmarshal(body, &claims); err != nil {
			t.Error(err)
		}
		if wantUser {
			if claims.Subject != "telegram:7001" || !reflect.DeepEqual(claims.Roles, []string{"train_user"}) {
				t.Errorf("wrong viewer authorization: %+v", claims)
			}
		} else if !reflect.DeepEqual(claims.Roles, []string{"train_service"}) {
			t.Errorf("public read unexpectedly impersonates a user: %+v", claims)
		}
		if fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		// Procedure strings must survive decoding without Go struct conversion:
		// eventLabel and the absent report timestamp are part of the browser API.
		payload, _ := json.Marshal(map[string]any{
			"trains": trains, "schedule": map[string]any{"available": true},
			"stops":            []any{map[string]any{"stationId": "riga", "latitude": 56.95, "longitude": 24.1}},
			"stationSightings": []any{map[string]any{"id": "sighting:public", "stationId": "riga", "matchedTrainInstanceId": "train-1"}},
		})
		_ = json.NewEncoder(w).Encode(string(payload))
	}))
	defer remote.Close()
	client, err := spacetime.NewSyncer(spacetime.SyncConfig{Host: remote.URL, Database: "train-db", JWTPrivateKeyFile: keyFile})
	if err != nil {
		t.Fatal(err)
	}
	state := store.NewSpacetimeStore(client, time.UTC)
	server.app = trainapp.NewService(store.NewRoutedStore(nil, state), nil, nil, nil, time.UTC, false)
	for _, tc := range []struct {
		path, procedure string
		args            []any
		user            bool
	}{
		{"/public/dashboard?limit=334", "get_public_dashboard", []any{float64(334)}, false},
		{"/public/service-day-trains", "get_public_service_day_trains", []any{}, false},
		{"/public/map", "get_public_network_map", []any{}, false},
		{"/public/trains/train-1", "get_public_train", []any{"train-1"}, false},
		{"/public/trains/train-1/stops", "get_public_train_stops", []any{"train-1"}, false},
		{"/public/stations/riga/departures", "get_public_station_departures", []any{"riga"}, false},
		{"/windows/today", "list_window_trains", []any{"today"}, true},
		{"/stations/riga/departures", "get_station_departures", []any{"riga"}, true},
		{"/trains/train-1/stops", "get_train_stops", []any{"train-1"}, true},
	} {
		t.Run(tc.procedure, func(t *testing.T) {
			wantProcedure, wantArgs, wantUser = tc.procedure, tc.args, tc.user
			if tc.user {
				before := calls
				res := httptest.NewRecorder()
				server.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/v1"+tc.path, nil))
				if res.Code != http.StatusUnauthorized || calls != before {
					t.Fatalf("anonymous request reached backend: status %d calls %d", res.Code, calls-before)
				}
			}
			for _, failed := range []bool{false, true} {
				fail = failed
				before := calls
				req := httptest.NewRequest(http.MethodGet, "/api/v1"+tc.path, nil)
				if tc.user {
					req.AddCookie(cookie)
				}
				res := httptest.NewRecorder()
				server.ServeHTTP(res, req)
				if calls != before+1 {
					t.Fatalf("got %d remote calls for 334 trains; want one", calls-before)
				}
				if failed {
					if res.Code != http.StatusInternalServerError {
						t.Fatalf("backend error was hidden: %d", res.Code)
					}
					continue
				}
				var payload struct {
					Trains []map[string]any `json:"trains"`
				}
				if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil || res.Code != http.StatusOK || len(payload.Trains) != 334 {
					t.Fatalf("aggregate response lost: status %d count %d error %v", res.Code, len(payload.Trains), err)
				}
				if !strings.Contains(res.Body.String(), `"eventLabel":"inspection"`) || !strings.Contains(res.Body.String(), `"state":"MIXED_REPORTS"`) {
					t.Fatal("live state/timeline lost in response conversion")
				}
				if !strings.Contains(res.Body.String(), `"id":"sighting:public"`) || !strings.Contains(res.Body.String(), `"latitude":56.95`) {
					t.Fatal("live sightings or stop coordinates lost in response conversion")
				}
				if !tc.user && strings.Contains(res.Body.String(), "private-source") {
					t.Fatal("public response bypassed redaction")
				}
				if tc.user && !strings.Contains(res.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("authenticated response became publicly cacheable")
				}
			}
		})
	}
}
