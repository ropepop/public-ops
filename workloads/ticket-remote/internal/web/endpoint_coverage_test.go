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

func TestSafeEndpointBoundaries(t *testing.T) {
	const owner, admin, member = "owner@example.test", "admin@example.test", "member@example.test"
	ctx := context.Background()
	store := NewMemoryStore()
	if err := store.Bootstrap(ctx, state.BootstrapInput{TicketID: "endpoints", AdminEmail: owner}); err != nil {
		t.Fatal(err)
	}
	for email, role := range map[string]string{admin: state.RoleAdmin, member: state.RoleMember} {
		if _, err := store.UpsertMember(ctx, "endpoints", owner, email, role); err != nil {
			t.Fatal(err)
		}
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "test_auth", SessionSigningKey: "endpoint-fixture-only"}
	relay := phone.NewRelay(phone.RelayConfig{})
	s, err := NewServer(config.Config{TicketID: "endpoints", PublicBaseURL: "https://ticket.example.test", Access: access}, store, relay)
	if err != nil {
		relay.Close()
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	tokens := map[string]string{}
	for _, email := range []string{owner, admin, member} {
		token, _, err := s.auth.IssueServerSession(auth.Identity{Email: email, EmailVerified: true}, time.Hour, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		tokens[email] = token
	}
	request := func(email, method, path, body, bearer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://ticket.example.test"+path, strings.NewReader(body))
		r.Header.Set("Origin", "https://ticket.example.test")
		r.Header.Set("Content-Type", "application/json")
		if token := tokens[email]; token != "" {
			r.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}

	t.Run("retired routes never admit work", func(t *testing.T) {
		paths := []string{
			"/api/v1/session", "/api/v1/me", "/api/v1/state", "/api/v1/client-log",
			"/api/v1/control-code/request", "/api/v1/control-code/prepare", "/api/v1/control-code/capture", "/api/v1/control-code/close",
			"/api/v1/control/claim", "/api/v1/control/extend", "/api/v1/control/release",
			"/api/v1/admin/control/revoke", "/api/v1/admin/ticket/reselect-latest",
		}
		for _, path := range paths {
			for _, email := range []string{"", owner} {
				for _, method := range []string{http.MethodGet, http.MethodPost} {
					w := request(email, method, path, `{}`, "")
					if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), `"error":"route_retired"`) {
						t.Fatalf("%s %s as %q: %d %s", method, path, email, w.Code, w.Body.String())
					}
				}
			}
		}
	})

	t.Run("HDR diagnostics require current owner", func(t *testing.T) {
		for _, path := range []string{"/owner/hdr-diagnostic", "/owner/hdr-diagnostic/app.js"} {
			for email, want := range map[string]int{"": 401, member: 404, admin: 404, owner: 200} {
				if w := request(email, "GET", path, "", ""); w.Code != want {
					t.Fatalf("%s as %q: %d, want %d", path, email, w.Code, want)
				}
			}
			if w := request(owner, "POST", path, `{}`, ""); w.Code != http.StatusMethodNotAllowed {
				t.Fatalf("POST %s = %d", path, w.Code)
			}
		}
		for _, role := range []string{state.RoleOwner, state.RoleAdmin} {
			if _, err := store.UpsertMember(ctx, "endpoints", owner, admin, role); err != nil {
				t.Fatal(err)
			}
			want := http.StatusNotFound
			if role == state.RoleOwner {
				want = http.StatusOK
			}
			for _, path := range []string{"/owner/hdr-diagnostic", "/owner/hdr-diagnostic/app.js"} {
				if w := request(admin, "GET", path, "", ""); w.Code != want {
					t.Fatalf("unchanged session with current role %s: %s = %d, want %d", role, path, w.Code, want)
				}
			}
		}
	})

	t.Run("service events reject before writing", func(t *testing.T) {
		const path = "/api/v1/internal/service-events"
		const body = `{"source":"test","category":"test","action":"unauthorized"}`
		for _, tc := range []struct {
			token, email, method, bearer string
			want                         int
		}{
			{"", "", "POST", "", 404},
			{"fixture-token", "", "GET", "fixture-token", 405},
			{"fixture-token", "", "POST", "", 403},
			{"fixture-token", owner, "POST", "", 403},
			{"fixture-token", "", "POST", "wrongxx-token", 403},
		} {
			s.cfg.ServiceEvents.Token = tc.token
			if w := request(tc.email, tc.method, path, body, tc.bearer); w.Code != tc.want {
				t.Fatalf("%s as %q: %d, want %d", tc.method, tc.email, w.Code, tc.want)
			}
		}
		store.mu.Lock()
		defer store.mu.Unlock()
		for _, event := range store.tickets["endpoints"].safeLogs {
			if event.Source == "test" {
				t.Fatal("unauthorized service event was saved")
			}
		}
	})

	t.Run("retired schedule cannot create work", func(t *testing.T) {
		const path = "/api/v1/admin/ticket/reselect-latest/schedule"
		for _, email := range []string{owner, admin} {
			for _, body := range []string{`{}`, `{"action":"create","date":"2099-01-01","time":"12:00"}`} {
				w := request(email, "POST", path, body, "")
				if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), `"error":"route_retired"`) {
					t.Fatalf("schedule as %s: %d %s", email, w.Code, w.Body.String())
				}
			}
		}
		for email, want := range map[string]int{"": 401, member: 403} {
			if w := request(email, "POST", path, `{"action":"create"}`, ""); w.Code != want {
				t.Fatalf("schedule as %q = %d, want %d", email, w.Code, want)
			}
		}
		if w := request(owner, "GET", path, "", ""); w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("GET schedule = %d", w.Code)
		}
	})
}
