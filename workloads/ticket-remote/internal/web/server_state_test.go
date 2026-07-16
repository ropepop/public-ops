package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"ticketremote/internal/state"
)

func TestMemberStateRedactionHidesAdminOnlyDetails(t *testing.T) {
	snapshot := state.Snapshot{
		Ticket: state.Ticket{ID: "vivi-default", DisplayName: "ViVi timed ticket", UpdatedAt: "2026-05-08T10:00:00Z"},
		Members: []state.Member{
			{Email: "owner@example.test", Role: state.RoleOwner, Active: true},
			{Email: "viewer@example.test", Role: state.RoleMember, Active: true},
		},
		Viewers: []state.Viewer{
			{SessionID: "secret-session", Email: "viewer@example.test", Connected: true},
			{SessionID: "secret-session-2", Email: "other@example.test", Connected: true},
			{SessionID: "secret-session-3", Email: "gone@example.test", Connected: false},
		},
		Phone: &state.PhoneBackend{
			ID:           "pixel",
			AttachName:   "Pixel",
			BaseURL:      "http://ticket_phone_bridge:9388",
			DesiredState: "streaming",
			HealthJSON:   `{"secret":true}`,
			LastError:    "internal",
			LastSeenAt:   "2026-05-08T10:00:00Z",
		},
		ServerTime:   "2026-05-08T10:00:00Z",
		StateBackend: "spacetime",
	}

	public := snapshot.PublicForMember("viewer@example.test")
	body, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, forbidden := range []string{"secret-session", "secret-session-2", "secret-session-3", "secret-control", "owner@example.test", "viewer@example.test", "other@example.test", "gone@example.test", "ticket_phone_bridge", "healthJson", "lastError", `"members"`, `"viewers"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public member state leaked %q in %s", forbidden, text)
		}
	}
	for _, required := range []string{`"viewerCount":2`, `"viewerPresence"`, `"stateBackend":"spacetime"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("public member state missing %q in %s", required, text)
		}
	}
	publicIDPattern := regexp.MustCompile(`^[0-9A-Z]{4}$`)
	for _, viewer := range public.ViewerPresence {
		if !publicIDPattern.MatchString(viewer.Label) || viewer.PublicID != viewer.Label {
			t.Fatalf("public viewer label/public ID = %#v, want matching 4-character account ID", viewer)
		}
		if strings.HasPrefix(viewer.Label, "Skatītājs") {
			t.Fatalf("public viewer label must not use ordinal fallback: %#v", viewer)
		}
	}
}

func TestAdminPageRendersDashboardShell(t *testing.T) {
	server := newDirectTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`class="admin-status-grid"`,
		`action="/api/v1/admin/ticket/reselect-latest"`,
		`action="/api/v1/admin/phone/backend"`,
		`action="/api/v1/admin/members"`,
		`class="admin-member-public-id"`,
		`<details class="admin-section admin-raw">`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("admin page missing %q in %s", expected, body)
		}
	}
	if strings.Contains(body, `/static/app.js`) {
		t.Fatal("server-rendered admin must not load the public stream application")
	}
}
