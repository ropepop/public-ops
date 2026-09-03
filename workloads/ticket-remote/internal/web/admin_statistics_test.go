package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/state"
)

type adminActivitySnapshotStore struct {
	state.Store
}

func (s *adminActivitySnapshotStore) Snapshot(ctx context.Context, ticketID string, now time.Time) (state.Snapshot, error) {
	snapshot, err := s.Store.Snapshot(ctx, ticketID, now)
	if err != nil {
		return snapshot, err
	}
	for index := range snapshot.Members {
		snapshot.Members[index].AccountScopeID = ticketAccountScopeID(snapshot.Members[index].Email)
	}
	inactiveScope := ticketAccountScopeID("inactive@example.test")
	snapshot.Members = append(snapshot.Members, state.Member{
		Email:          "inactive@example.test",
		AccountScopeID: inactiveScope,
		PublicID:       "Z9Y8",
		Role:           state.RoleMember,
		Active:         false,
		UpdatedAt:      "2026-09-01T12:00:00Z",
	})
	memberScope := ticketAccountScopeID("member@example.com")
	snapshot.PageActivityDaily = []state.PageActivityDaily{
		{
			AccountScopeID: memberScope,
			Day:            "2026-09-02",
			HourlyTicks:    []uint32{1, 2, 3},
			FirstTickAt:    "2026-09-02T00:00:05Z",
			LastTickAt:     "2026-09-02T02:00:15Z",
			UpdatedAt:      "2026-09-02T02:00:15Z",
			ExpiresAt:      "2026-10-09T00:00:00Z",
		},
		{
			AccountScopeID: inactiveScope,
			Day:            "2026-09-01",
			HourlyTicks:    []uint32{0, 6},
			FirstTickAt:    "2026-09-01T01:00:05Z",
			LastTickAt:     "2026-09-01T01:00:30Z",
			UpdatedAt:      "2026-09-01T01:00:30Z",
			ExpiresAt:      "2026-10-08T00:00:00Z",
		},
	}
	snapshot.MemberHDREngines = []state.MemberHDREngine{{
		AccountScopeID: memberScope,
		Engine:         "client_webgpu_v2",
	}}
	snapshot.MemberHDRBoosts = []state.MemberHDRBoost{{
		AccountScopeID:       memberScope,
		SelectedDisplayBoost: 4,
	}}
	return snapshot, nil
}

func newAdminActivityTestServer(t *testing.T) *Server {
	t.Helper()
	server := newTicketSetupTestServer(t, "pixel")
	server.store = &adminActivitySnapshotStore{Store: server.store}
	return server
}

func TestAdminStatisticsTabRendersPrivateArrowIslandOnly(t *testing.T) {
	server := newAdminActivityTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin?tab=statistics", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("statistics status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, required := range []string{
		`class="admin-tab is-active" href="/admin?tab=statistics" aria-current="page"`,
		`id="ticketActivityStatistics"`,
		`id="ticketActivityStatisticsData" type="application/json" nonce="`,
		`/static/admin-statistics.css?v=`,
		`/static/admin-statistics.js?v=`,
		`"pageActivityDaily"`,
		`"accountScopeId"`,
		`"day":"2026-09-02"`,
		`"hourlyTicks":[1,2,3]`,
		`inactive@example.test`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("statistics page missing %q in %s", required, body)
		}
	}
	for _, forbidden := range []string{
		`class="admin-status-grid"`,
		`id="ticketAdminConfig"`,
		`/static/spacetime-client.js`,
		`/static/admin-schedule.js`,
		`/static/app.js`,
		`class="admin-state"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("statistics page loaded overview or viewer surface %q in %s", forbidden, body)
		}
	}

	nonceMatch := regexp.MustCompile(`id="ticketActivityStatisticsData" type="application/json" nonce="([^"]+)"`).FindStringSubmatch(body)
	if len(nonceMatch) != 2 {
		t.Fatalf("statistics data nonce missing in %s", body)
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "'nonce-"+nonceMatch[1]+"'") {
		t.Fatalf("statistics data nonce is not authorized by CSP: %s", rec.Header().Get("Content-Security-Policy"))
	}
}

func TestAdminUnknownTabDefaultsToOverviewWithoutActivityRows(t *testing.T) {
	server := newAdminActivityTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin?tab=unknown", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("overview status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, required := range []string{
		`class="admin-tab is-active" href="/admin" aria-current="page"`,
		`class="admin-status-grid"`,
		`/static/spacetime-client.js`,
		`/static/admin-schedule.js`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("overview fallback missing %q in %s", required, body)
		}
	}
	for _, forbidden := range []string{
		`id="ticketActivityStatistics"`,
		`id="ticketActivityStatisticsData"`,
		`/static/admin-statistics.js`,
		`"pageActivityDaily"`,
		`"accountScopeId"`,
		`"day":"2026-09-02"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("overview fallback exposed statistics data or UI %q in %s", forbidden, body)
		}
	}
}

func TestAdminStatisticsTabRejectsOrdinaryMemberWithoutRenderingData(t *testing.T) {
	server := newAdminActivityTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin?tab=statistics", nil)
	req.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("ordinary member statistics status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"pageActivityDaily", "ticketActivityStatisticsData", "inactive@example.test"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden statistics response exposed %q in %s", forbidden, body)
		}
	}
}

func TestMemberHealthRedactsActivityAndAllAccountScopeData(t *testing.T) {
	server := newAdminActivityTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("member health status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	memberScope := ticketAccountScopeID("member@example.com")
	inactiveScope := ticketAccountScopeID("inactive@example.test")
	for _, forbidden := range []string{
		`"pageActivityDaily"`,
		`"accountScopeId"`,
		`"memberHDREngines"`,
		`"memberHDRBoosts"`,
		`"hourlyTicks"`,
		`"day":"2026-09-02"`,
		memberScope,
		inactiveScope,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("member health exposed private activity identity %q in %s", forbidden, body)
		}
	}
}
