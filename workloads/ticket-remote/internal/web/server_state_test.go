package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/state"
)

func TestAdjustSnapshotTimeExpiresControlLocally(t *testing.T) {
	now := time.Date(2026, 4, 30, 15, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		ActiveControl: &state.ControlSession{
			ExpiresAt: now.Add(-time.Second).Format(time.RFC3339),
		},
	}

	adjustSnapshotTime(&snapshot, now)

	if snapshot.ActiveControl != nil {
		t.Fatalf("expected expired control to be hidden, got %#v", snapshot.ActiveControl)
	}
	if snapshot.ServerTime != now.Format(time.RFC3339) {
		t.Fatalf("server time = %q", snapshot.ServerTime)
	}
}

func TestAdjustSnapshotTimeUpdatesRemainingControlTime(t *testing.T) {
	now := time.Date(2026, 4, 30, 15, 0, 0, 0, time.UTC)
	snapshot := state.Snapshot{
		ActiveControl: &state.ControlSession{
			ExpiresAt: now.Add(12*time.Second + 500*time.Millisecond).Format(time.RFC3339),
		},
	}

	adjustSnapshotTime(&snapshot, now)

	if snapshot.ActiveControl == nil {
		t.Fatal("expected active control")
	}
	if snapshot.ActiveControl.RemainingMS != 12000 {
		t.Fatalf("remaining ms = %d", snapshot.ActiveControl.RemainingMS)
	}
}

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
		ActiveControl: &state.ControlSession{
			ID:          "secret-control",
			SessionID:   "secret-session",
			Email:       "controller@example.test",
			ClaimedAt:   "2026-05-08T10:00:00Z",
			ExpiresAt:   "2026-05-08T10:01:30Z",
			RemainingMS: 90000,
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
	for _, required := range []string{`"viewerCount":2`, `"viewerPresence"`, `"activeControl"`, `"ownerEmail":"controller@example.test"`, `"stateBackend":"spacetime"`} {
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

func TestActiveControlGateAllowsSameEmailController(t *testing.T) {
	now := time.Date(2026, 4, 30, 15, 0, 0, 0, time.UTC)
	server := &Server{}
	server.gate = &controlGate{
		sessionID: "controller-session",
		email:     "ticket@jolkins.id.lv",
		expiresAt: now.Add(45 * time.Second),
	}

	active, allowed := server.activeControlGateAllows("controller-session", "ticket@jolkins.id.lv", now)
	if !active || !allowed {
		t.Fatalf("controller active=%v allowed=%v", active, allowed)
	}

	active, allowed = server.activeControlGateAllows("other-session", "ticket@jolkins.id.lv", now)
	if !active || !allowed {
		t.Fatalf("same-email other session active=%v allowed=%v", active, allowed)
	}

	active, allowed = server.activeControlGateAllows("other-session", "other@example.com", now)
	if !active || allowed {
		t.Fatalf("different email active=%v allowed=%v", active, allowed)
	}
}

func TestActiveControlGateRejectsWhenNoActiveControl(t *testing.T) {
	server := &Server{}
	active, allowed := server.activeControlGateAllows("session", "ticket@jolkins.id.lv", time.Now())
	if active || allowed {
		t.Fatalf("expected inactive gate, got active=%v allowed=%v", active, allowed)
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
		`id="adminPhoneState"`,
		`id="adminSafetyState"`,
		`id="adminBackendList"`,
		`id="adminNotice"`,
		`<details class="admin-section admin-raw">`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("admin page missing %q in %s", expected, body)
		}
	}
}
