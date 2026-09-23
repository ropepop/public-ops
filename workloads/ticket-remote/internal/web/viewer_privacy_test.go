package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestViewerListAndDetailedHealthRequireAdmin(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	const owner, admin, member = "owner@example.test", "admin@example.test", "member@example.test"
	if err := store.Bootstrap(ctx, state.BootstrapInput{TicketID: "privacy", AdminEmail: owner}); err != nil {
		t.Fatal(err)
	}
	for email, role := range map[string]string{admin: state.RoleAdmin, member: state.RoleMember} {
		if _, err := store.UpsertMember(ctx, "privacy", owner, email, role); err != nil {
			t.Fatal(err)
		}
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "test_auth", SessionSigningKey: "privacy-fixture-only"}
	relay := phone.NewRelay(phone.RelayConfig{})
	t.Cleanup(relay.Close)
	server, err := NewServer(config.Config{TicketID: "privacy", Access: access}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Snapshot(ctx, "privacy", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{owner, admin, member} {
		privileged := email != member
		response := httptest.NewRecorder()
		server.handleIndex(response, httptest.NewRequest("GET", "/", nil), auth.Identity{Email: email}, "fixture", snapshot, "")
		if strings.Contains(response.Body.String(), `id="presence"`) != privileged {
			t.Fatalf("incorrect viewer visibility for %s", email)
		}
		if !strings.Contains(response.Body.String(), `id="trainCheckinMount"`) {
			t.Fatal("check-in missing")
		}
		token, _, err := server.auth.IssueServerSession(auth.Identity{Email: email, EmailVerified: true}, time.Hour, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "/api/v1/health", nil)
		req.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
		response = httptest.NewRecorder()
		server.ServeHTTP(response, req)
		want := http.StatusForbidden
		if privileged {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("health for %s = %d, want %d", email, response.Code, want)
		}
	}
}
