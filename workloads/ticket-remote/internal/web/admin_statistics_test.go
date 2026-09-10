package web

import (
	"encoding/json"
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"

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
