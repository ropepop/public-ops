package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"ticketremote/internal/config"
	"ticketremote/internal/state"
)

type scheduledTestStore struct {
	state.Store

	mu            sync.Mutex
	schedules     []state.LatestTicketReselectSchedule
	scheduled     []state.LatestTicketReselectScheduleInput
	cancellations []state.LatestTicketReselectScheduleCancelInput
}

func (s *scheduledTestStore) Snapshot(ctx context.Context, ticketID string, now time.Time) (state.Snapshot, error) {
	snapshot, err := s.Store.Snapshot(ctx, ticketID, now)
	if err != nil {
		return snapshot, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot.LatestTicketReselectSchedules = append([]state.LatestTicketReselectSchedule(nil), s.schedules...)
	return snapshot, nil
}

func (s *scheduledTestStore) ScheduleLatestTicketReselect(_ context.Context, input state.LatestTicketReselectScheduleInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scheduled = append(s.scheduled, input)
	for _, existing := range s.schedules {
		if existing.ID == input.ScheduleID {
			return nil
		}
	}
	now := input.Now.UTC().Format(time.RFC3339)
	for index := range s.schedules {
		if s.schedules[index].TicketID == input.TicketID &&
			s.schedules[index].BackendID == input.BackendID &&
			s.schedules[index].Status == "pending" {
			s.schedules[index].Status = "replaced"
			s.schedules[index].ResultReason = "replaced_by_new_schedule"
			s.schedules[index].ResultPhase = "replaced"
			s.schedules[index].ProofSource = "admin"
			s.schedules[index].UpdatedAt = now
			s.schedules[index].CompletedAt = now
		}
	}
	s.schedules = append(s.schedules, state.LatestTicketReselectSchedule{
		ID:             input.ScheduleID,
		TicketID:       input.TicketID,
		BackendID:      input.BackendID,
		ScheduledAt:    input.ScheduledAt.UTC().Format(time.RFC3339),
		PhoneLocalTime: input.PhoneLocalTime,
		PhoneTimeZone:  input.PhoneTimeZone,
		Status:         "pending",
		RequestedBy:    input.RequestedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	return nil
}

func (s *scheduledTestStore) CancelLatestTicketReselect(_ context.Context, input state.LatestTicketReselectScheduleCancelInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancellations = append(s.cancellations, input)
	for index := range s.schedules {
		if s.schedules[index].ID == input.ScheduleID {
			now := input.Now.UTC().Format(time.RFC3339)
			s.schedules[index].Status = "canceled"
			s.schedules[index].ResultReason = "canceled_by_admin"
			s.schedules[index].ResultPhase = "canceled"
			s.schedules[index].ProofSource = "admin"
			s.schedules[index].UpdatedAt = now
			s.schedules[index].CompletedAt = now
		}
	}
	return nil
}

func (s *scheduledTestStore) setSchedules(items ...state.LatestTicketReselectSchedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules = append([]state.LatestTicketReselectSchedule(nil), items...)
}

func (s *scheduledTestStore) scheduleInputs() []state.LatestTicketReselectScheduleInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.LatestTicketReselectScheduleInput(nil), s.scheduled...)
}

func (s *scheduledTestStore) cancelInputs() []state.LatestTicketReselectScheduleCancelInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.LatestTicketReselectScheduleCancelInput(nil), s.cancellations...)
}

func (s *scheduledTestStore) scheduleRows() []state.LatestTicketReselectSchedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]state.LatestTicketReselectSchedule(nil), s.schedules...)
}

func newScheduledAdminServer(t *testing.T) (*Server, *scheduledTestStore) {
	t.Helper()
	store := &scheduledTestStore{Store: NewMemoryStore()}
	handler, _ := newBackendSwitchServer(t, store, "", "http://lab.test", "http://pixel.test")
	server, ok := handler.(*Server)
	if !ok {
		t.Fatalf("handler type = %T", handler)
	}
	server.cfg.Phone.TimeZone = config.DefaultPhoneTimeZone
	t.Cleanup(server.Close)
	return server, store
}

func scheduledAdminFormRequest(values url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ticket/reselect-latest/schedule", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://ticket.test")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	return req
}

func TestResolvePhoneLocalScheduleUsesPhoneZoneAndEarliestOverlap(t *testing.T) {
	t.Run("ordinary Riga time", func(t *testing.T) {
		now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
		got, local, err := resolvePhoneLocalSchedule("2026-07-10", "15:30", "Europe/Riga", now)
		if err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, 7, 10, 12, 30, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("scheduled UTC = %s, want %s", got, want)
		}
		if local != "2026-07-10T15:30" {
			t.Fatalf("phone local time = %q", local)
		}
	})

	t.Run("nonexistent spring time", func(t *testing.T) {
		now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
		_, _, err := resolvePhoneLocalSchedule("2026-03-29", "03:30", "Europe/Riga", now)
		validation, ok := err.(latestTicketScheduleValidationError)
		if !ok || validation.code != "nonexistent_local_time" {
			t.Fatalf("error = %#v, want nonexistent_local_time", err)
		}
	})

	t.Run("earliest autumn overlap", func(t *testing.T) {
		now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		got, _, err := resolvePhoneLocalSchedule("2026-10-25", "03:30", "Europe/Riga", now)
		if err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC); !got.Equal(want) {
			t.Fatalf("overlap UTC = %s, want earliest %s", got, want)
		}
	})
}

func TestResolvePhoneLocalScheduleRejectsPastAndBeyondNinetyDays(t *testing.T) {
	tests := []struct {
		name  string
		date  string
		clock string
		now   time.Time
		code  string
	}{
		{
			name:  "past",
			date:  "2026-07-09",
			clock: "15:30",
			now:   time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
			code:  "schedule_in_past",
		},
		{
			name:  "too far",
			date:  "2026-04-10",
			clock: "15:30",
			now:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			code:  "schedule_too_far",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := resolvePhoneLocalSchedule(test.date, test.clock, "Europe/Riga", test.now)
			validation, ok := err.(latestTicketScheduleValidationError)
			if !ok || validation.code != test.code {
				t.Fatalf("error = %#v, want %s", err, test.code)
			}
		})
	}
}

func TestLatestTicketReselectScheduleIDIsDeterministic(t *testing.T) {
	at := time.Date(2026, 7, 10, 12, 30, 0, 0, time.UTC)
	first := latestTicketReselectScheduleID("vivi-default", "pixel", at, "A1B2")
	second := latestTicketReselectScheduleID("vivi-default", "pixel", at.In(time.FixedZone("other", 3*60*60)), "A1B2")
	if first == "" || first != second {
		t.Fatalf("schedule IDs = %q and %q", first, second)
	}
	if first == latestTicketReselectScheduleID("vivi-default", "other", at, "A1B2") {
		t.Fatal("different backends must not share a schedule ID")
	}
	if first == latestTicketReselectScheduleID("vivi-default", "pixel", at, "C3D4") {
		t.Fatal("different requesters must not share a schedule ID")
	}
}

func TestAdminSchedulesAndCancelsLatestTicketRedetection(t *testing.T) {
	server, store := newScheduledAdminServer(t)
	location, err := time.LoadLocation(config.DefaultPhoneTimeZone)
	if err != nil {
		t.Fatal(err)
	}
	local := time.Now().In(location).Add(24 * time.Hour).Truncate(time.Minute)
	values := url.Values{
		"date": {local.Format("2006-01-02")},
		"time": {local.Format("15:04")},
	}

	for attempt := 0; attempt < 2; attempt++ {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, scheduledAdminFormRequest(values))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin" {
			t.Fatalf("schedule attempt %d status = %d location = %q body = %s", attempt, rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	}
	inputs := store.scheduleInputs()
	if len(inputs) != 2 || inputs[0].ScheduleID == "" || inputs[0].ScheduleID != inputs[1].ScheduleID {
		t.Fatalf("schedule inputs = %#v, want deterministic double submit", inputs)
	}
	if inputs[0].PhoneTimeZone != "Europe/Riga" || inputs[0].PhoneLocalTime != local.Format(phoneLocalMinuteLayout) {
		t.Fatalf("phone-local input = %#v", inputs[0])
	}
	if inputs[0].RequestedBy == "" || strings.Contains(inputs[0].RequestedBy, "@") {
		t.Fatalf("requestedBy = %q, want member public ID", inputs[0].RequestedBy)
	}

	cancelValues := url.Values{
		"action":     {"cancel"},
		"scheduleId": {inputs[0].ScheduleID},
		"backendId":  {inputs[0].BackendID},
	}
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, scheduledAdminFormRequest(cancelValues))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin" {
		t.Fatalf("cancel status = %d location = %q body = %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	cancellations := store.cancelInputs()
	if len(cancellations) != 1 || cancellations[0].ScheduleID != inputs[0].ScheduleID || cancellations[0].BackendID != "lab-pixel" {
		t.Fatalf("cancel inputs = %#v", cancellations)
	}
}

func TestAdminCanReplacePendingLatestTicketSchedule(t *testing.T) {
	server, store := newScheduledAdminServer(t)
	location, _ := time.LoadLocation(config.DefaultPhoneTimeZone)
	firstLocal := time.Now().In(location).Add(24 * time.Hour).Truncate(time.Minute)
	secondLocal := firstLocal.Add(2 * time.Hour)
	for _, local := range []time.Time{firstLocal, secondLocal} {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, scheduledAdminFormRequest(url.Values{
			"date": {local.Format("2006-01-02")},
			"time": {local.Format("15:04")},
		}))
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("replace schedule status = %d body = %s", rec.Code, rec.Body.String())
		}
	}
	rows := store.scheduleRows()
	if len(rows) != 2 || rows[0].Status != "replaced" || rows[1].Status != "pending" {
		t.Fatalf("replacement rows = %#v", rows)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	body := rec.Body.String()
	if rec.Code != http.StatusOK ||
		!strings.Contains(body, `data-schedule-status="pending"`) ||
		!strings.Contains(body, `class="admin-schedule-form"`) ||
		!strings.Contains(body, `Cancel schedule`) {
		t.Fatalf("replacement admin page status = %d body = %s", rec.Code, body)
	}
}

func TestAdminLatestTicketScheduleRejectsCrossOriginAndNonAdmin(t *testing.T) {
	server, store := newScheduledAdminServer(t)
	location, _ := time.LoadLocation(config.DefaultPhoneTimeZone)
	local := time.Now().In(location).Add(24 * time.Hour).Truncate(time.Minute)
	values := url.Values{"date": {local.Format("2006-01-02")}, "time": {local.Format("15:04")}}

	crossOrigin := scheduledAdminFormRequest(values)
	crossOrigin.Header.Set("Origin", "https://attacker.example")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, crossOrigin)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d body = %s", rec.Code, rec.Body.String())
	}

	snapshot, err := store.Store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "member@example.test", state.RoleMember)
	_, memberOK := snapshot.Member("member@example.test")
	if err != nil || !memberOK {
		t.Fatalf("add non-admin: snapshot=%#v err=%v", snapshot, err)
	}
	nonAdmin := scheduledAdminFormRequest(values)
	nonAdmin.Header.Set("X-Ticket-Remote-Email", "member@example.test")
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, nonAdmin)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin status = %d body = %s", rec.Code, rec.Body.String())
	}
	if inputs := store.scheduleInputs(); len(inputs) != 0 {
		t.Fatalf("unauthorized schedule inputs = %#v", inputs)
	}
}

func TestAdminLatestTicketScheduleJSONResponse(t *testing.T) {
	server, store := newScheduledAdminServer(t)
	location, _ := time.LoadLocation(config.DefaultPhoneTimeZone)
	local := time.Now().In(location).Add(24 * time.Hour).Truncate(time.Minute)
	body := `{"date":"` + local.Format("2006-01-02") + `","time":"` + local.Format("15:04") + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/ticket/reselect-latest/schedule", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://ticket.test")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("JSON schedule status = %d body = %s", rec.Code, rec.Body.String())
	}
	var response struct {
		OK             bool   `json:"ok"`
		ScheduleID     string `json:"scheduleId"`
		PhoneTimeZone  string `json:"phoneTimeZone"`
		PhoneLocalTime string `json:"phoneLocalTime"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	inputs := store.scheduleInputs()
	if !response.OK || len(inputs) != 1 || response.ScheduleID != inputs[0].ScheduleID || response.PhoneTimeZone != "Europe/Riga" {
		t.Fatalf("response = %#v inputs = %#v", response, inputs)
	}
}

func TestAdminPageRendersNativeScheduleControlsAndPendingCancel(t *testing.T) {
	server, store := newScheduledAdminServer(t)
	future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	store.setSchedules(state.LatestTicketReselectSchedule{
		ID:             "latest_reselect_pending",
		TicketID:       "vivi-default",
		BackendID:      "lab-pixel",
		ScheduledAt:    future.Format(time.RFC3339),
		PhoneLocalTime: "2026-07-24T12:30",
		PhoneTimeZone:  "Europe/Riga",
		Status:         "pending",
		RequestedBy:    "A1B2",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
		UpdatedAt:      time.Now().UTC().Format(time.RFC3339),
	})
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()

	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`data-schedule-status="pending"`,
		`action="/api/v1/admin/ticket/reselect-latest/schedule"`,
		`name="action" value="cancel"`,
		`name="scheduleId" value="latest_reselect_pending"`,
		`Cancel schedule`,
		`Europe/Riga`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("pending admin page missing %q in %s", expected, body)
		}
	}
	if !strings.Contains(body, `class="admin-schedule-form"`) {
		t.Fatal("new schedule form must remain available so a pending schedule can be replaced")
	}
}

func TestAdminPageKeepsLatestScheduledResultVisible(t *testing.T) {
	statuses := []string{"queued", "running", "succeeded", "failed", "expired", "replaced", "canceled"}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			server, store := newScheduledAdminServer(t)
			now := time.Now().UTC().Truncate(time.Second)
			store.setSchedules(state.LatestTicketReselectSchedule{
				ID:             "latest_reselect_" + status,
				TicketID:       "vivi-default",
				BackendID:      "lab-pixel",
				ScheduledAt:    now.Add(-time.Minute).Format(time.RFC3339),
				PhoneLocalTime: "2026-07-23T12:30",
				PhoneTimeZone:  "Europe/Riga",
				Status:         status,
				CommandID:      "command-" + status,
				ResultReason:   "reason-" + status,
				ResultPhase:    "phase-" + status,
				ProofSource:    "proof-" + status,
				RequestedBy:    "A1B2",
				CreatedAt:      now.Add(-2 * time.Minute).Format(time.RFC3339),
				UpdatedAt:      now.Format(time.RFC3339),
				TriggeredAt:    now.Add(-time.Minute).Format(time.RFC3339),
				CompletedAt:    now.Format(time.RFC3339),
			})
			req := httptest.NewRequest(http.MethodGet, "/admin", nil)
			req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
			rec := httptest.NewRecorder()

			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("admin status = %d body = %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, expected := range []string{
				`data-schedule-status="` + status + `"`,
				`Reason: reason-` + status,
				`Phase: phase-` + status,
				`Proof: proof-` + status,
				`Command: command-` + status,
				`Requested by: A1B2`,
				`Created: `,
				`Triggered: `,
				`Completed: `,
			} {
				if !strings.Contains(body, expected) {
					t.Fatalf("%s page missing %q in %s", status, expected, body)
				}
			}
			if strings.Contains(body, `Cancel schedule`) {
				t.Fatalf("%s result must not offer pending-only cancellation", status)
			}
			active := status == "queued" || status == "running"
			if active && strings.Contains(body, `class="admin-schedule-form"`) {
				t.Fatalf("%s result must block another schedule while work is active", status)
			}
			if !active && !strings.Contains(body, `class="admin-schedule-form"`) {
				t.Fatalf("%s result must allow a new schedule", status)
			}
		})
	}
}
