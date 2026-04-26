package service

import (
	"context"
	"testing"
	"time"

	"subscriptionbot/internal/domain"
)

func TestConversationStateExpiresAfterThirtyMinutes(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	actor := Actor{TelegramID: 7001, Username: "flow-user"}
	now := time.Date(2026, time.March, 22, 10, 0, 0, 0, h.loc)

	if err := h.app.SaveConversationState(h.ctx, actor, "create_plan", "seat_limit", map[string]any{"service_code": "spotify_family"}, now); err != nil {
		t.Fatalf("save conversation state: %v", err)
	}

	state, err := h.app.LoadConversationState(h.ctx, actor, now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("load live conversation state: %v", err)
	}
	if state == nil || state.Flow != "create_plan" || state.Step != "seat_limit" {
		t.Fatalf("unexpected live conversation state: %+v", state)
	}

	expired, err := h.app.LoadConversationState(h.ctx, actor, now.Add(31*time.Minute))
	if err != nil {
		t.Fatalf("load expired conversation state: %v", err)
	}
	if expired != nil {
		t.Fatalf("expected expired conversation state to be cleared, got %+v", expired)
	}
}

func TestOperatorViewsSupportRenewalIssuesAndRecentPlans(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	operator := Actor{TelegramID: 9000, Username: "operator"}
	owner := Actor{TelegramID: 7101, Username: "owner"}
	member := Actor{TelegramID: 7102, Username: "member"}
	joinAt := time.Date(2026, time.March, 10, 9, 0, 0, 0, h.loc)

	plan, invite, err := h.app.CreatePlan(h.ctx, owner, CreatePlanInput{
		ServiceCode:      "spotify_family",
		TotalPriceMinor:  1800,
		SeatLimit:        2,
		RenewalDate:      time.Date(2026, time.April, 1, 0, 0, 0, 0, h.loc),
		SharingPolicyAck: true,
	}, joinAt)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	_, firstInvoice, err := h.app.JoinPlan(h.ctx, member, invite.InviteCode, joinAt)
	if err != nil {
		t.Fatalf("join plan: %v", err)
	}
	firstInvoice, err = h.app.QuoteInvoice(h.ctx, member, firstInvoice.ID, "USDC", "solana", joinAt)
	if err != nil {
		t.Fatalf("quote first invoice: %v", err)
	}
	if err := h.app.SimulateInvoicePayment(h.ctx, member, firstInvoice.ID, firstInvoice.QuotedPayAmount, joinAt); err != nil {
		t.Fatalf("simulate first payment: %v", err)
	}
	if _, err := h.app.ProcessCycle(h.ctx, joinAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("activate membership: %v", err)
	}

	if _, err := h.app.OpenSupportTicket(h.ctx, member, plan.ID, "Need help with the invite", joinAt.Add(3*time.Minute)); err != nil {
		t.Fatalf("open support ticket: %v", err)
	}

	renewalLead := time.Date(2026, time.March, 26, 8, 0, 0, 0, h.loc)
	if _, err := h.app.ProcessCycle(h.ctx, renewalLead); err != nil {
		t.Fatalf("generate renewal invoice: %v", err)
	}
	renewalInvoice, err := latestInvoiceForPlan(h, plan.ID)
	if err != nil {
		t.Fatalf("latest renewal invoice: %v", err)
	}
	renewalInvoice, err = h.app.QuoteInvoice(h.ctx, member, renewalInvoice.ID, "USDC", "solana", renewalLead)
	if err != nil {
		t.Fatalf("quote renewal invoice: %v", err)
	}
	if err := h.app.SimulateInvoicePayment(h.ctx, member, renewalInvoice.ID, subtractAtomic(t, renewalInvoice.QuotedPayAmount, "1000000"), renewalLead); err != nil {
		t.Fatalf("simulate underpayment: %v", err)
	}
	if _, err := h.app.ProcessCycle(h.ctx, renewalLead.Add(5*time.Minute)); err != nil {
		t.Fatalf("process underpayment: %v", err)
	}

	support, err := h.app.ListOpenSupportTickets(h.ctx, operator)
	if err != nil {
		t.Fatalf("list support tickets: %v", err)
	}
	if len(support) != 1 || support[0].PlanServiceName != "Spotify Family" {
		t.Fatalf("unexpected support view: %+v", support)
	}

	issues, err := h.app.ListRenewalIssues(h.ctx, operator)
	if err != nil {
		t.Fatalf("list renewal issues: %v", err)
	}
	if !containsIssueKind(issues, "underpaid") {
		t.Fatalf("expected underpaid issue, got %+v", issues)
	}

	if _, err := h.app.ProcessCycle(h.ctx, time.Date(2026, time.April, 1, 12, 0, 0, 0, h.loc)); err != nil {
		t.Fatalf("start grace: %v", err)
	}
	issues, err = h.app.ListRenewalIssues(h.ctx, operator)
	if err != nil {
		t.Fatalf("list grace issues: %v", err)
	}
	if !containsIssueKind(issues, "grace") {
		t.Fatalf("expected grace issue, got %+v", issues)
	}

	recentPlans, err := h.app.ListRecentPlans(h.ctx, operator, 5)
	if err != nil {
		t.Fatalf("list recent plans: %v", err)
	}
	if len(recentPlans) == 0 || recentPlans[0].ID != plan.ID {
		t.Fatalf("expected recent plans to include created plan, got %+v", recentPlans)
	}

	reimbursements, err := h.app.ListOwnerReimbursementsDue(h.ctx, operator, 5)
	if err != nil {
		t.Fatalf("list owner reimbursements: %v", err)
	}
	if len(reimbursements) == 0 || reimbursements[0].AmountMinor != firstInvoice.BaseMinor {
		t.Fatalf("expected owner reimbursement summary to include %.2f USDC, got %+v", float64(firstInvoice.BaseMinor)/100, reimbursements)
	}

	entry, err := h.app.AddDenylistEntry(h.ctx, operator, "telegram_id", "7109", "fraud check", renewalLead.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("add denylist entry: %v", err)
	}
	if entry.EntryValue != "7109" {
		t.Fatalf("unexpected denylist entry: %+v", entry)
	}
	entries, err := h.app.ListDenylistEntries(h.ctx, operator, 5)
	if err != nil {
		t.Fatalf("list denylist entries: %v", err)
	}
	if len(entries) == 0 || entries[0].EntryValue != "7109" {
		t.Fatalf("expected denylist to include telegram id 7109, got %+v", entries)
	}

	if err := h.app.appendEvent(h.ctx, "payment_provider", "event-1", "provider_event_unmatched", map[string]any{
		"provider_name":       "nowpayments",
		"provider_invoice_id": "missing-1",
		"detail":              "provider event did not match a known invoice",
	}); err != nil {
		t.Fatalf("append payment alert: %v", err)
	}
	alerts, err := h.app.ListRecentPaymentAlerts(h.ctx, operator, 5)
	if err != nil {
		t.Fatalf("list payment alerts: %v", err)
	}
	if len(alerts) == 0 || alerts[0].EventName != "provider_event_unmatched" {
		t.Fatalf("expected provider alert view, got %+v", alerts)
	}

	overview, err := h.app.AdminOverview(h.ctx, operator)
	if err != nil {
		t.Fatalf("admin overview: %v", err)
	}
	if overview.PaymentAlertsTotal == 0 || overview.BlockedActorsTotal == 0 || overview.PayoutDueMinor == 0 {
		t.Fatalf("expected overview to include alerts, blocked actors, and payout due, got %+v", overview)
	}
}

func TestRecordProviderEventIsIdempotent(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, h.loc)

	inserted, err := h.app.RecordProviderEvent(h.ctx, "nowpayments", "payment-123", "finished", "payment-123", map[string]any{"status": "finished"}, now)
	if err != nil {
		t.Fatalf("first record provider event: %v", err)
	}
	if !inserted {
		t.Fatalf("expected first provider event insert to be accepted")
	}

	inserted, err = h.app.RecordProviderEvent(h.ctx, "nowpayments", "payment-123", "finished", "payment-123", map[string]any{"status": "finished"}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("duplicate record provider event: %v", err)
	}
	if inserted {
		t.Fatalf("expected duplicate provider event to be ignored")
	}

	var count int
	if err := h.store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM provider_events WHERE provider_name = 'nowpayments' AND external_event_id = 'payment-123'`).Scan(&count); err != nil {
		t.Fatalf("count provider events: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one stored provider event, got %d", count)
	}
}

func containsIssueKind(items []domain.RenewalIssue, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
