package web

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func TestAdminNavigationScopesDataAndOwnerAccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.Bootstrap(ctx, state.BootstrapInput{TicketID: "navigation", AdminEmail: "owner@example.test"}); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{state.RoleAdmin, state.RoleMember} {
		if _, err := store.UpsertMember(ctx, "navigation", "owner@example.test", role+"@example.test", role); err != nil {
			t.Fatal(err)
		}
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "navigation_auth", SessionSigningKey: "navigation-fixture-only"}
	relay := phone.NewRelay(phone.RelayConfig{})
	t.Cleanup(relay.Close)
	// Inspect exactly the data each page may render, independent of presentation.
	tmpl := template.Must(template.New("navigation").Parse(`{{.Tab}}|{{.PageTitle}}|{{.IsOwner}}|
{{if .AdminConfigJSON}}[config]{{end}}{{if .StatisticsJSON}}[statistics]{{end}}{{if .Phone}}[phone]{{end}}{{if .RawState}}[raw]{{end}}{{if .PhoneTimeZone}}[schedule]{{end}}
{{range .Members}}{{.Role}}={{.CanRemove}};{{end}}`))
	s := &Server{adminTmpl: tmpl, relay: relay, store: store, auth: auth.NewValidator(access), cfg: config.Config{TicketID: "navigation", Access: access, CookieName: "navigation_session", CookieTTL: time.Hour}}
	for _, role := range []string{"", state.RoleMember, state.RoleAdmin, state.RoleOwner} {
		for _, page := range []struct{ query, tab, title string }{
			{"", "overview", "Overview"}, {"overview", "overview", "Overview"},
			{"tickets", "tickets", "Tickets"}, {"members", "members", "Members"},
			{"statistics", "statistics", "Statistics"}, {"settings", "settings", "Settings"},
			{"account", "account", "ViVi account"}, {"unknown", "overview", "Overview"},
			{" StAtIsTiCs ", "statistics", "Statistics"},
		} {
			t.Run(role+"/"+page.query, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/admin?tab="+url.QueryEscape(page.query), nil)
				if role != "" {
					token, _, err := s.auth.IssueServerSession(auth.Identity{Email: role + "@example.test", EmailVerified: true}, time.Hour, time.Now())
					if err != nil {
						t.Fatal(err)
					}
					req.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
				}
				w := httptest.NewRecorder()
				s.ServeHTTP(w, req)
				if role == "" || role == state.RoleMember {
					want := http.StatusForbidden
					if role == "" {
						want = http.StatusFound
					}
					if w.Code != want || strings.Contains(w.Body.String(), "[config]") {
						t.Fatalf("unprivileged navigation returned %d: %s", w.Code, w.Body.String())
					}
					return
				}
				tab, title := page.tab, page.title
				if tab == "account" && role != state.RoleOwner {
					tab, title = "overview", "Overview"
				}
				owner := "false"
				if role == state.RoleOwner {
					owner = "true"
				}
				body := w.Body.String()
				if w.Code != http.StatusOK || !strings.HasPrefix(body, tab+"|"+title+"|"+owner+"|") {
					t.Fatalf("wrong page response: %d %s", w.Code, body)
				}
				for marker, want := range map[string]bool{
					"[config]":     tab == "tickets" || tab == "settings" || tab == "account",
					"[statistics]": tab == "statistics", "[phone]": tab == "overview" || tab == "settings",
					"[raw]": tab == "settings", "[schedule]": tab == "tickets",
				} {
					if strings.Contains(body, marker) != want {
						t.Fatalf("%s presence should be %t: %s", marker, want, body)
					}
				}
				if tab == "members" && (!strings.Contains(body, "member=true;") || !strings.Contains(body, "owner=false;") || strings.Contains(body, "admin=true;") != (role == state.RoleOwner)) {
					t.Fatalf("member permissions changed: %s", body)
				}
				if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("admin navigation must not be cached")
				}
			})
		}
	}
}

func TestAdminSectionsRenderOnlyTheirControlsAndScripts(t *testing.T) {
	tmpl, err := template.ParseFS(staticFS, "static/admin.html.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{})
	t.Cleanup(relay.Close)
	for _, role := range []string{state.RoleAdmin, state.RoleOwner} {
		snapshot := state.Snapshot{Members: []state.Member{{Email: "viewer@example.test", Role: role, Active: true}}}
		for _, tab := range []string{"overview", "statistics", "members", "tickets", "settings", "account"} {
			selected := tab
			if tab == "account" && role != state.RoleOwner {
				selected = "overview"
			}
			s := &Server{adminTmpl: tmpl}
			// Unrelated pages must render without reading phone health.
			if selected == "overview" || selected == "settings" {
				s.relay = relay
			}
			w := httptest.NewRecorder()
			s.handleAdminPage(w, httptest.NewRequest(http.MethodGet, "/admin?tab="+tab, nil), auth.Identity{Email: "viewer@example.test"}, "", snapshot)
			body := w.Body.String()
			if w.Code != http.StatusOK || !strings.Contains(body, "</html>") || strings.Count(body, `aria-current="page"`) != 1 {
				t.Fatalf("%s/%s did not finish rendering with one selected section", role, tab)
			}
			for marker, want := range map[string]bool{
				`href="/admin?tab=account"`: role == state.RoleOwner,
				`id="ticketAdminViviAuth"`:  selected == "account", "admin-vivi-auth.js?": selected == "account",
				`id="ticketAdminColdRestart"`: selected == "tickets" && role == state.RoleOwner,
				`class="admin-redetect-form"`: selected == "tickets", `class="member-form"`: selected == "members",
				`id="adminObeyMemberLimits"`: selected == "settings", `class="admin-state"`: selected == "settings",
				`id="ticketNotifications"`:      selected == "settings" && role == state.RoleOwner,
				"notifications.css?":            selected == "settings" && role == state.RoleOwner,
				"notifications.js?":             selected == "settings" && role == state.RoleOwner,
				`id="ticketActivityStatistics"`: selected == "statistics", "admin-statistics.js?": selected == "statistics",
				"admin-schedule.js?":   selected == "tickets" || selected == "settings",
				"spacetime-client.js?": selected == "tickets" || selected == "settings" || selected == "account",
			} {
				if strings.Contains(body, marker) != want {
					t.Fatalf("%s/%s: %s presence should be %t", role, tab, marker, want)
				}
			}
		}
	}
}

func TestAdminFormsReturnToTheirSection(t *testing.T) {
	for _, page := range []struct{ path, target string }{
		{"/api/v1/admin/members", "/admin?tab=members"},
		{"/api/v1/admin/phone/backend", "/admin?tab=settings"},
		{"/api/v1/admin/ticket/reselect-latest/schedule", "/admin?tab=tickets"},
		{"/api/v1/admin/unknown", "/admin"},
	} {
		for _, form := range []bool{true, false} {
			req := httptest.NewRequest(http.MethodPost, page.path+"?returnTo=https://outside.example", strings.NewReader("returnTo=https://outside.example"))
			if form {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
			} else {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			if redirectAdminForm(w, req) != form {
				t.Fatal("only browser form requests should redirect")
			}
			if form && (w.Code != http.StatusSeeOther || w.Header().Get("Location") != page.target) {
				t.Fatalf("%s returned %d %s", page.path, w.Code, w.Header().Get("Location"))
			}
			if !form && w.Header().Get("Location") != "" {
				t.Fatal("JSON action unexpectedly redirects")
			}
		}
	}
}
