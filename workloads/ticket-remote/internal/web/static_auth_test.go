package web

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
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

func TestStaticFilesRequireCurrentMembership(t *testing.T) {
	const owner, member = "owner@example.test", "member@example.test"
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{TicketID: "static-test", AdminEmail: owner}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMember(context.Background(), "static-test", owner, member, state.RoleMember); err != nil {
		t.Fatal(err)
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "ticket_remote_auth", SessionSigningKey: "static-test-only-key"}
	assets, err := fs.Sub(staticFS, "static")
	if err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{})
	t.Cleanup(relay.Close)
	server := &Server{
		cfg:  config.Config{TicketID: "static-test", CookieName: "ticket_remote_session", CookieTTL: time.Hour, Access: access},
		auth: auth.NewValidator(access), store: store, relay: relay, static: assets,
	}
	issue := func(email string, issuedAt time.Time) string {
		t.Helper()
		token, _, err := server.auth.IssueServerSession(auth.Identity{Email: email, EmailVerified: true}, time.Hour, issuedAt)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	valid := issue(member, time.Now())
	expired := issue(member, time.Now().Add(-2*time.Hour))
	unapproved := issue("outsider@example.test", time.Now())
	paths, err := fs.Glob(assets, "*")
	if err != nil || len(paths) == 0 {
		t.Fatalf("static inventory: %v", err)
	}
	request := func(t *testing.T, name, query, token, method string, conditional bool, want int) {
		t.Helper()
		req := httptest.NewRequest(method, "/static/"+name+query, nil)
		if token != "" {
			req.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
		}
		if conditional {
			req.Header.Set("If-None-Match", "*")
			req.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
			req.Header.Set("Range", "bytes=0-15")
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("status = %d, want %d", response.Code, want)
		}
		for _, header := range []string{"Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control", "Surrogate-Control"} {
			value := response.Header().Get(header)
			if !strings.Contains(value, "no-store") || strings.Contains(value, "public") || strings.Contains(value, "immutable") {
				t.Errorf("%s allows caching: %q", header, value)
			}
		}
		body, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		if want == http.StatusOK && method == http.MethodGet {
			if !bytes.Equal(response.Body.Bytes(), body) {
				t.Fatal("approved member did not receive the exact asset")
			}
		} else if bytes.Contains(response.Body.Bytes(), body[:min(32, len(body))]) {
			t.Fatal("denied request disclosed asset bytes")
		}
	}
	queries := []string{"", "?v=old-release", "?v=" + serverVersion}
	for _, name := range paths {
		for _, query := range queries {
			for _, tc := range []struct {
				name, token string
				status      int
			}{
				{"missing", "", http.StatusUnauthorized},
				{"invalid", "trsess1.invalid.invalid", http.StatusUnauthorized},
				{"expired", expired, http.StatusUnauthorized},
				{"unapproved", unapproved, http.StatusForbidden},
				{"approved", valid, http.StatusOK},
			} {
				t.Run(name+query+"/"+tc.name, func(t *testing.T) {
					request(t, name, query, tc.token, http.MethodGet, false, tc.status)
					if tc.status != http.StatusOK {
						request(t, name, query, tc.token, http.MethodGet, true, tc.status)
						request(t, name, query, tc.token, http.MethodHead, true, tc.status)
					}
				})
			}
		}
	}
	// A previously admitted session and a warm server cache cannot retain access.
	if _, err := store.RemoveMember(context.Background(), "static-test", owner, member); err != nil {
		t.Fatal(err)
	}
	for _, name := range paths {
		for _, query := range queries {
			t.Run(name+query+"/removed", func(t *testing.T) {
				request(t, name, query, valid, http.MethodGet, true, http.StatusForbidden)
			})
		}
	}
	// Failed fresh lookup must not authorize from the last successful snapshot.
	if _, err := store.UpsertMember(context.Background(), "static-test", owner, member, state.RoleMember); err != nil {
		t.Fatal(err)
	}
	request(t, "app.js", "", valid, http.MethodGet, false, http.StatusOK)
	server.store = &staticUnavailableStore{Store: store}
	request(t, "app.js", "?v=old-release", valid, http.MethodGet, true, http.StatusServiceUnavailable)
}

type staticUnavailableStore struct{ state.Store }

func (*staticUnavailableStore) Snapshot(context.Context, string, time.Time) (state.Snapshot, error) {
	return state.Snapshot{}, errors.New("test membership lookup unavailable")
}
