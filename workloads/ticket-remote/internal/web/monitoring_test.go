package web

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

type monitoringFixture struct {
	*MemoryStore
	health     state.MonitoringState
	sub        state.PushSubscriptionInput
	deliveries []state.PushDelivery
	claimed    *state.PushDelivery
	finished   []string
	claimCalls int
}

func (m *monitoringFixture) Monitoring(context.Context, string, string) (state.MonitoringState, error) {
	return m.health, nil
}
func (m *monitoringFixture) SetMonitoring(_ context.Context, _, _ string, enabled bool) error {
	m.health.Enabled = enabled
	return nil
}
func (m *monitoringFixture) PushSubscribed(context.Context, string, string, string) (bool, error) {
	return m.sub.Enabled, nil
}
func (m *monitoringFixture) SetPushSubscription(_ context.Context, sub state.PushSubscriptionInput) error {
	m.sub = sub
	return nil
}
func (m *monitoringFixture) WatchMonitoring(ctx context.Context, _ func()) error {
	<-ctx.Done()
	return ctx.Err()
}
func (m *monitoringFixture) PushDeliveries(context.Context, string) ([]state.PushDelivery, error) {
	return m.deliveries, nil
}
func (m *monitoringFixture) ClaimPushDelivery(context.Context, string, string, string) (*state.PushDelivery, error) {
	m.claimCalls++
	return m.claimed, nil
}
func (m *monitoringFixture) FinishPushDelivery(_ context.Context, _, _, _, outcome string) error {
	m.finished = append(m.finished, outcome)
	return nil
}

func TestMonitoringRequiresCurrentRoleAndOwnerToChange(t *testing.T) {
	const owner, admin, member = "owner@example.test", "admin@example.test", "member@example.test"
	store := &monitoringFixture{MemoryStore: NewMemoryStore()}
	ctx := context.Background()
	if err := store.Bootstrap(ctx, state.BootstrapInput{TicketID: "monitor-test", AdminEmail: owner}); err != nil {
		t.Fatal(err)
	}
	for email, role := range map[string]string{admin: state.RoleAdmin, member: state.RoleMember} {
		if _, err := store.UpsertMember(ctx, "monitor-test", owner, email, role); err != nil {
			t.Fatal(err)
		}
	}
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "ticket_remote_auth", SessionSigningKey: "monitoring-fixture-key"}
	relay := phone.NewRelay(phone.RelayConfig{})
	t.Cleanup(relay.Close)
	s := &Server{relay: relay, cfg: config.Config{PublicBaseURL: "https://ticket.example.test", TicketID: "monitor-test", CookieName: "ticket_remote_session", CookieTTL: time.Hour, Access: access}, auth: auth.NewValidator(access), store: store}
	request := func(email, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://ticket.example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://ticket.example.test")
		if email != "" {
			token, _, err := s.auth.IssueServerSession(auth.Identity{Email: email, EmailVerified: true}, time.Hour, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(&http.Cookie{Name: access.AuthCookieName, Value: token})
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		email, method, body string
		status              int
	}{
		{"", "GET", "", 401}, {member, "GET", "", 403}, {admin, "GET", "", 200},
		{admin, "POST", `{"enabled":true}`, 403}, {owner, "POST", `{"enabled":true}`, 200},
		{owner, "POST", `{}`, 400}, {owner, "POST", `{"enabled":false,"email":"spoof"}`, 400},
	} {
		w := request(tc.email, tc.method, "/api/v1/admin/monitoring", tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s %s got %d: %s", tc.email, tc.method, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("monitoring must not be cached")
		}
	}
	if !store.health.Enabled {
		t.Fatal("valid owner setting lost")
	}
	w := request(admin, "POST", "/api/v1/admin/notifications", `{"action":"status","endpoint":"https://127.0.0.1/private"}`)
	if w.Code != 400 {
		t.Fatal("arbitrary subscription endpoint accepted")
	}
	if _, err := store.RemoveMember(ctx, "monitor-test", owner, admin); err != nil {
		t.Fatal(err)
	}
	if w = request(admin, "GET", "/api/v1/admin/monitoring", ""); w.Code != 403 {
		t.Fatalf("revoked admin retained access: %d", w.Code)
	}
}

func testPushDelivery(t *testing.T) state.PushDelivery {
	t.Helper()
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return state.PushDelivery{ID: "d", IncidentID: "incident", Kind: "problem", Endpoint: "https://web.push.apple.com/test", P256dh: base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(make([]byte, 16))}
}

type pushRoundTrip func(*http.Request) (*http.Response, error)

func (f pushRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPushValidatesEndpointsEncryptsAndClassifiesProviderResults(t *testing.T) {
	for _, endpoint := range []string{"https://localhost/x", "http://web.push.apple.com/x", "https://web.push.apple.com.evil.test/x", "https://user@web.push.apple.com/x", "https://web.push.apple.com:8443/x", "https://127.0.0.1/x"} {
		if validPushEndpoint(endpoint) {
			t.Fatalf("unsafe endpoint accepted %s", endpoint)
		}
	}
	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	delivery := testPushDelivery(t)
	for status, outcome := range map[int]string{201: "sent", 404: "invalid", 410: "invalid", 429: "retry", 503: "retry", 400: "failed", 302: "failed"} {
		calls := 0
		s := &Server{cfg: config.Config{PublicBaseURL: "https://ticket.example.test"}, push: &ticketPush{PublicKey: public, PrivateKey: private, client: &http.Client{Transport: pushRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			body, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Encoding") != "aes128gcm" || !strings.Contains(r.Header.Get("Authorization"), "vapid") || strings.Contains(string(body), "Ticket has not") {
				t.Fatal("push must encrypt payload and authenticate")
			}
			if r.Header.Get("TTL") != "300" {
				t.Fatal("stale push lifetime too long")
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
		}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}}
		if got := s.sendTicketPush(context.Background(), delivery); got != outcome || calls != 1 {
			t.Fatalf("status %d got %s calls %d", status, got, calls)
		}
		bad := delivery
		bad.Auth = "invalid"
		if got := s.sendTicketPush(context.Background(), bad); got != "invalid" || calls != 1 {
			t.Fatal("invalid keys reached network")
		}
	}
}

func TestPushRequiresDurableClaimAndWaitsForDueRetry(t *testing.T) {
	delivery := testPushDelivery(t)
	store := &monitoringFixture{MemoryStore: NewMemoryStore(), deliveries: []state.PushDelivery{delivery}}
	s := &Server{cfg: config.Config{TicketID: "monitor-test"}, store: store}
	s.deliverTicketPush(context.Background(), store)
	if store.claimCalls != 1 || len(store.finished) != 0 {
		t.Fatal("revoked or obsolete claim must not send")
	}
	store.deliveries[0].NextAttemptAtMS = time.Now().Add(2 * time.Minute).UnixMilli()
	next := s.deliverTicketPush(context.Background(), store)
	if store.claimCalls != 1 || next < time.Minute {
		t.Fatal("retry sent before its durable due time")
	}
}

func TestNotificationBodyDistinguishesMissingProofAndContainsNoPrivateData(t *testing.T) {
	for _, tc := range []struct {
		kind, reason, want string
	}{
		{"problem", "capture_unavailable", "Ticket could not be checked twice in a row. Open Ticket to review it."},
		{"problem", "observation_overdue", "Ticket could not be checked twice in a row. Open Ticket to review it."},
		{"problem", "busy", "Ticket could not be checked twice in a row. Open Ticket to review it."},
		{"problem", "unknown", "Ticket could not be checked twice in a row. Open Ticket to review it."},
		{"problem", "blocked", "Two checks in a row found the same ticket problem. Open Ticket to check it."},
		{"recovery", "ticket_ready", "Ticket is ready again."},
	} {
		delivery := state.PushDelivery{Kind: tc.kind, Reason: tc.reason, IncidentID: "private-incident", Endpoint: "private-endpoint", P256dh: "private-key", Auth: "private-auth"}
		if got := notificationBody(delivery); got != tc.want {
			t.Fatalf("%s/%s body = %q; want %q", tc.kind, tc.reason, got, tc.want)
		}
	}
}

func TestNotificationWorkerIsPublicButContainsNoPrivateCache(t *testing.T) {
	w := httptest.NewRecorder()
	s := &Server{}
	s.ServeHTTP(w, httptest.NewRequest("GET", "/ticket-notifications-sw.js", nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("worker unavailable: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "caches.") {
		t.Fatal("worker must not cache private ticket content")
	}
}
