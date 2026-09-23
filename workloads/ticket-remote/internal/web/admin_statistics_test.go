package web

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/state"
)

func statisticsSnapshot() state.Snapshot {
	return state.Snapshot{
		Members:                   []state.Member{{Email: "member@example.test", AccountScopeID: "private-scope", PublicID: "AB12", Active: true}},
		ActionStatisticsStartedAt: "2026-09-09T12:00:00Z",
		ActionActivityDaily: []state.ActionActivityDaily{{AccountScopeID: "private-scope", Day: "2026-09-09",
			RegistrationAttempts: []uint32{2}, RegistrationSuccesses: []uint32{1},
			ControlCodeAttempts: []uint32{3}, ControlCodeSuccesses: []uint32{3}}},
	}
}

func TestStatisticsPageUsesOnlyStatisticsData(t *testing.T) {
	tmpl, err := template.ParseFS(staticFS, "static/admin.html.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately no relay: statistics rendering must not prepare phone/overview data.
	s := &Server{adminTmpl: tmpl}
	w := httptest.NewRecorder()
	s.handleAdminPage(w, httptest.NewRequest("GET", "/admin?tab=statistics", nil), auth.Identity{Email: "admin@example.test"}, "", statisticsSnapshot())
	for _, text := range []string{"ticketActivityStatisticsData", `"registrationAttempts":[2]`, `"controlCodeSuccesses":[3]`, "2026-09-09T12:00:00Z", "admin-statistics.js"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatalf("missing %q", text)
		}
	}
	for _, text := range []string{"admin-schedule.js", "admin-vivi-auth.js", "ticketAdminConfig"} {
		if strings.Contains(w.Body.String(), text) {
			t.Fatalf("unexpected overview data %q", text)
		}
	}
}

func TestHealthSnapshotDoesNotExposeActionStatistics(t *testing.T) {
	body, err := json.Marshal(redactSnapshotForHealth(statisticsSnapshot()))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"private-scope", "actionActivityDaily", "actionStatisticsStartedAt", "registrationAttempts"} {
		if strings.Contains(string(body), text) {
			t.Fatalf("health exposes %q", text)
		}
	}
}

func TestStatisticsEndpointRequiresCurrentAdminAndDoesNotTouchPhone(t *testing.T) {
	const owner, member = "owner@example.test", "member@example.test"
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{TicketID: "stats-test", AdminEmail: owner}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMember(context.Background(), "stats-test", owner, member, state.RoleMember); err != nil {
		t.Fatal(err)
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "ticket_remote_auth", SessionSigningKey: "statistics-fixture-key"}
	relay := phone.NewRelay(phone.RelayConfig{})
	t.Cleanup(relay.Close)
	s := &Server{relay: relay, cfg: config.Config{TicketID: "stats-test", CookieName: "ticket_remote_session", CookieTTL: time.Hour, Access: access}, auth: auth.NewValidator(access), store: store}
	token := func(email string, at time.Time) string {
		value, _, err := s.auth.IssueServerSession(auth.Identity{Email: email, EmailVerified: true}, time.Hour, at)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	adminToken := token(owner, time.Now())
	for _, tc := range []struct {
		token, method string
		status        int
	}{
		{"", "GET", 401}, {token(member, time.Now()), "GET", 403},
		{token(owner, time.Now().Add(-2*time.Hour)), "GET", 401},
		{adminToken, "GET", 200}, {adminToken, "POST", 405},
	} {
		req := httptest.NewRequest(tc.method, "/api/v1/admin/statistics", nil)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: tc.token})
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s status %d, want %d: %s", tc.method, w.Code, tc.status, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("statistics may be cached")
		}
		if tc.status == 200 {
			var payload map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["secondsPerTick"] != float64(5) || payload["timeZone"] != "Europe/Riga" || payload["members"] == nil {
				t.Fatalf("bad payload: %v", payload)
			}
			if _, found := payload["phone"]; found {
				t.Fatal("unexpected phone data")
			}
		} else if strings.Contains(w.Body.String(), "pageActivityDaily") {
			t.Fatal("denied request exposes statistics")
		}
	}
}
