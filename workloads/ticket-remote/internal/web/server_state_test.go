package web

import (
	"net/http"
	"net/http/httptest"
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
		`data-simulator-setup="true"`,
		`id="adminNotice"`,
		`<details class="admin-section admin-raw">`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("admin page missing %q in %s", expected, body)
		}
	}
}
