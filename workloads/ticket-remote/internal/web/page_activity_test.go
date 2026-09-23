package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/state"
)

// Every other Store method deliberately remains nil. Activity must be committed
// without loading the viewer snapshot, starting the relay, or waking the phone.
type pageActivityStore struct {
	state.Store
	record func(context.Context, string, string, []int64) error
}

func (s *pageActivityStore) RecordMemberActivitySlots(ctx context.Context, ticketID, email string, slots []int64) error {
	return s.record(ctx, ticketID, email, slots)
}

func pageActivityServer(t *testing.T, record func(context.Context, string, string, []int64) error) (*Server, string, string) {
	t.Helper()
	access := auth.AccessConfig{Mode: "spacetime", AuthCookieName: "ticket_remote_auth", SessionSigningKey: "activity-test-only-key"}
	s := &Server{
		cfg:  config.Config{TicketID: "activity-test", PublicBaseURL: "http://ticket.test", Access: access},
		auth: auth.NewValidator(access), store: &pageActivityStore{record: record},
	}
	issue := func(at time.Time) string {
		token, _, err := s.auth.IssueServerSession(auth.Identity{Email: " Member@Example.Test ", EmailVerified: true}, time.Hour, at)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	return s, issue(time.Now()), issue(time.Now().Add(-2 * time.Hour))
}

func pageActivityRequest(method, body, token, origin, contentType string) *http.Request {
	r := httptest.NewRequest(method, "http://ticket.test/api/v1/activity", strings.NewReader(body))
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	}
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", contentType)
	return r
}

func checkActivityNoStore(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	for _, name := range []string{"Cache-Control", "CDN-Cache-Control", "Cloudflare-CDN-Cache-Control", "Surrogate-Control"} {
		if !strings.Contains(w.Header().Get(name), "no-store") {
			t.Errorf("%s permits caching: %q", name, w.Header().Get(name))
		}
	}
}

func TestPageActivityRejectsUntrustedRequestsWithoutWriting(t *testing.T) {
	s, valid, expired := pageActivityServer(t, func(context.Context, string, string, []int64) error {
		t.Fatal("rejected request reached the activity writer")
		return nil
	})
	scope := ticketAccountScopeID("member@example.test")
	body := fmt.Sprintf(`{"accountScopeId":%q,"slots":[%d]}`, scope, time.Now().Unix()/5)
	tooMany, err := json.Marshal(struct {
		AccountScopeID string  `json:"accountScopeId"`
		Slots          []int64 `json:"slots"`
	}{scope, make([]int64, 721)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token, method, origin, contentType, body string
		status                                         int
	}{
		{"missing session", "", "POST", "http://ticket.test", "application/json", body, 401},
		{"invalid session", "trsess1.invalid.invalid", "POST", "http://ticket.test", "application/json", body, 401},
		{"expired session", expired, "POST", "http://ticket.test", "application/json", body, 401},
		{"another account scope", valid, "POST", "http://ticket.test", "application/json", `{"accountScopeId":"other-account","slots":[]}`, 403},
		{"missing origin", valid, "POST", "", "application/json", body, 403},
		{"foreign origin", valid, "POST", "http://attacker.test", "application/json", body, 403},
		{"different scheme", valid, "POST", "https://ticket.test", "application/json", body, 403},
		{"different port", valid, "POST", "http://ticket.test:8080", "application/json", body, 403},
		{"origin with path", valid, "POST", "http://ticket.test/path", "application/json", body, 403},
		{"get", valid, "GET", "http://ticket.test", "application/json", body, 405},
		{"delete", valid, "DELETE", "http://ticket.test", "application/json", body, 405},
		{"not json", valid, "POST", "http://ticket.test", "text/plain", body, 400},
		{"empty json", valid, "POST", "http://ticket.test", "application/json", "", 400},
		{"unknown identity field", valid, "POST", "http://ticket.test", "application/json", strings.TrimSuffix(body, "}") + `,"email":"someone@example.test"}`, 400},
		{"unknown ticket field", valid, "POST", "http://ticket.test", "application/json", strings.TrimSuffix(body, "}") + `,"ticketId":"another-ticket"}`, 400},
		{"trailing json", valid, "POST", "http://ticket.test", "application/json", body + `{}`, 400},
		{"trailing junk", valid, "POST", "http://ticket.test", "application/json", body + `x`, 400},
		{"fractional slot", valid, "POST", "http://ticket.test", "application/json", fmt.Sprintf(`{"accountScopeId":%q,"slots":[1.5]}`, scope), 400},
		{"too many slots", valid, "POST", "http://ticket.test", "application/json", string(tooMany), 400},
		{"oversized body", valid, "POST", "http://ticket.test", "application/json", body + strings.Repeat(" ", 16*1024), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, pageActivityRequest(tc.method, tc.body, tc.token, tc.origin, tc.contentType))
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			checkActivityNoStore(t, w)
			if strings.Contains(w.Body.String(), "acknowledgedSlots") {
				t.Fatal("rejected request acknowledged activity")
			}
		})
	}
}

func TestPageActivityAcknowledgesOnlyAfterCommit(t *testing.T) {
	current := time.Now().Unix()/5 - 1
	expired, future := int64(1), current+1000
	called, release := make(chan struct{}), make(chan struct{})
	s, token, _ := pageActivityServer(t, func(_ context.Context, ticketID, email string, slots []int64) error {
		if ticketID != "activity-test" || email != "member@example.test" || !slices.Equal(slots, []int64{current}) {
			t.Errorf("incorrect authenticated write: ticket=%q email=%q slots=%v", ticketID, email, slots)
		}
		close(called)
		<-release
		return nil
	})
	body := fmt.Sprintf(`{"accountScopeId":%q,"slots":[%d,%d,%d,%d]}`, ticketAccountScopeID("member@example.test"), current, expired, current, future)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.ServeHTTP(w, pageActivityRequest("POST", body, token, "http://ticket.test:80", "application/json; charset=utf-8"))
		close(done)
	}()
	select {
	case <-called:
	case <-done:
		t.Fatalf("request did not commit: %d %s", w.Code, w.Body.String())
	case <-time.After(time.Second):
		t.Fatal("activity writer was not reached")
	}
	select {
	case <-done:
		t.Error("acknowledged before the database committed")
	default:
	}
	close(release)
	<-done
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	checkActivityNoStore(t, w)
	var response struct {
		AccountScopeID    string  `json:"accountScopeId"`
		ServerTime        string  `json:"serverTime"`
		ServerVersion     string  `json:"serverVersion"`
		AcknowledgedSlots []int64 `json:"acknowledgedSlots"`
		DiscardedSlots    []int64 `json:"discardedSlots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	slices.Sort(response.DiscardedSlots)
	if response.AccountScopeID != ticketAccountScopeID("member@example.test") || response.ServerVersion != serverVersion ||
		!slices.Equal(response.AcknowledgedSlots, []int64{current}) || !slices.Equal(response.DiscardedSlots, []int64{expired, future}) {
		t.Fatalf("incorrect committed response: %+v", response)
	}
	serverTime, err := time.Parse(time.RFC3339Nano, response.ServerTime)
	if err != nil || time.Since(serverTime) < -time.Second || time.Since(serverTime) > time.Minute {
		t.Fatalf("invalid server clock anchor %q: %v", response.ServerTime, err)
	}
}

func TestPageActivityChecksCurrentMembershipEvenForEmptyProbe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"member", nil, 200},
		{"revoked", state.ErrNotMember, 403},
		{"forbidden", state.ErrForbidden, 403},
		{"unavailable", errors.New("fixture database unavailable"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s, token, _ := pageActivityServer(t, func(_ context.Context, ticketID, email string, slots []int64) error {
				calls++
				if ticketID != "activity-test" || email != "member@example.test" || len(slots) != 0 {
					t.Fatalf("incorrect membership probe: %q %q %v", ticketID, email, slots)
				}
				return tc.err
			})
			body := fmt.Sprintf(`{"accountScopeId":%q,"slots":[]}`, ticketAccountScopeID("member@example.test"))
			w := httptest.NewRecorder()
			s.ServeHTTP(w, pageActivityRequest("POST", body, token, "http://ticket.test", "application/json"))
			if w.Code != tc.status || calls != 1 {
				t.Fatalf("status=%d calls=%d, want status=%d calls=1: %s", w.Code, calls, tc.status, w.Body.String())
			}
			checkActivityNoStore(t, w)
			if tc.status != 200 && strings.Contains(w.Body.String(), "acknowledgedSlots") {
				t.Fatal("failed database write acknowledged activity")
			}
			if tc.status == 200 {
				var response map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if string(response["acknowledgedSlots"]) != "[]" || string(response["discardedSlots"]) != "[]" {
					t.Fatalf("empty probe did not return empty arrays: %s", w.Body.String())
				}
			}
		})
	}
}

func TestSplitActivitySlotsUsesRigaCalendarDaysAcrossDST(t *testing.T) {
	for _, tc := range []struct{ now, cutoff string }{
		{"2026-03-30T12:00:00Z", "2026-02-28T22:00:00Z"},
		{"2026-10-26T12:00:00Z", "2026-09-26T21:00:00Z"},
		{"2026-09-20T00:00:00Z", "2026-08-21T21:00:00Z"},
	} {
		t.Run(tc.now, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			cutoff, err := time.Parse(time.RFC3339, tc.cutoff)
			if err != nil {
				t.Fatal(err)
			}
			first, current := cutoff.Unix()/5, now.Unix()/5
			accepted, discarded := splitActivitySlots([]int64{current, first - 1, first, current + 1, first, -1, 1<<63 - 1}, now)
			slices.Sort(accepted)
			slices.Sort(discarded)
			if !slices.Equal(accepted, []int64{first, current}) || !slices.Equal(discarded, []int64{-1, first - 1, current + 1, 1<<63 - 1}) {
				t.Fatalf("accepted=%v discarded=%v at %s (first permitted slot=%d)", accepted, discarded, tc.now, first)
			}
		})
	}
}
