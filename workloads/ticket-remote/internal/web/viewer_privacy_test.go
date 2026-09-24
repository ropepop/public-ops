package web

import (
	"context"
	"encoding/json"
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
	store.mu.Lock()
	store.tickets["privacy"].presence["owner-session"] = state.Viewer{SessionID: "owner-session", Email: owner, Connected: true, LastSeenAt: time.Now().UTC().Format(time.RFC3339)}
	store.tickets["privacy"].presence["member-session"] = state.Viewer{SessionID: "member-session", Email: member, Connected: true, LastSeenAt: time.Now().UTC().Format(time.RFC3339)}
	store.mu.Unlock()
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
		req = httptest.NewRequest("GET", "/api/v1/auth/session", nil)
		req.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
		response = httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("session for %s = %d: %s", email, response.Code, response.Body.String())
		}
		var session struct {
			State map[string]json.RawMessage `json:"state"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		count, hasCount := session.State["viewerCount"]
		presence, hasPresence := session.State["viewerPresence"]
		if !privileged {
			if hasCount || hasPresence || strings.Contains(response.Body.String(), owner) {
				t.Fatalf("member session exposed viewer details: %s", response.Body.String())
			}
			continue
		}
		if string(count) != "2" || !hasCount || !hasPresence {
			t.Fatalf("privileged session missing viewer details: %s", response.Body.String())
		}
		var viewers []struct {
			Label string `json:"label"`
		}
		if err := json.Unmarshal(presence, &viewers); err != nil {
			t.Fatal(err)
		}
		if len(viewers) != 2 || viewers[0].Label != member || viewers[1].Label != owner || strings.Contains(string(presence), `"publicId"`) {
			t.Fatalf("privileged session has wrong viewer labels: %s", presence)
		}
	}
}
