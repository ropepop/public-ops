package service

import (
	"context"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"subscriptionbot/internal/config"
	"subscriptionbot/internal/domain"
	"subscriptionbot/internal/payments"
	"subscriptionbot/internal/store"
)

type testAppHarness struct {
	app      *App
	store    *store.SQLiteStore
	cfg      config.Config
	loc      *time.Location
	ctx      context.Context
	provider *payments.SandboxProvider
}

func newTestAppHarness(t *testing.T) *testAppHarness {
	t.Helper()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "subscription-bot.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Config{
		PlatformFeeBps:    1000,
		GraceDays:         3,
		RenewalLeadDays:   7,
		ReminderDays:      []int{7, 3, 1},
		DefaultPayAsset:   "USDC",
		DefaultPayNetwork: "solana",
		QuoteTTL:          15 * time.Minute,
		OperatorIDs:       map[int64]struct{}{9000: {}},
	}
	provider := payments.NewSandboxProvider(st.DB())
	app := New(st.DB(), cfg, provider, loc)
	if err := app.SeedCatalog(ctx); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	return &testAppHarness{
		app:      app,
		store:    st,
		cfg:      cfg,
		loc:      loc,
		ctx:      ctx,
		provider: provider,
	}
}

func TestJoinPlanProratesAndSeparatesFee(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	owner := Actor{TelegramID: 1001, Username: "owner"}
	member := Actor{TelegramID: 1002, Username: "member"}
	joinAt := time.Date(2026, time.March, 16, 12, 0, 0, 0, h.loc)

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

	membership, invoice, err := h.app.JoinPlan(h.ctx, member, invite.InviteCode, joinAt)
	if err != nil {
		t.Fatalf("join plan: %v", err)
	}

	cycleStart, cycleEnd := cycleWindow(plan.RenewalDate, joinAt.In(h.loc), h.loc)
	wantBase := proratedBaseMinor(plan.PerSeatBaseMinor, joinAt.In(h.loc), cycleStart, cycleEnd)
	wantFee := feeAmount(wantBase, h.cfg.PlatformFeeBps)

	if membership.SeatStatus != domain.MembershipPendingPayment {
		t.Fatalf("expected pending_payment membership, got %s", membership.SeatStatus)
	}
	if invoice.BaseMinor != wantBase {
		t.Fatalf("unexpected prorated base: got %d want %d", invoice.BaseMinor, wantBase)
	}
	if invoice.FeeMinor != wantFee {
		t.Fatalf("unexpected platform fee: got %d want %d", invoice.FeeMinor, wantFee)
	}
	if invoice.TotalMinor != wantBase+wantFee {
		t.Fatalf("unexpected total: got %d want %d", invoice.TotalMinor, wantBase+wantFee)
	}
	if invoice.Status != domain.InvoiceOpen {
		t.Fatalf("expected open invoice, got %s", invoice.Status)
	}

	memberPlans, err := h.app.ListUserPlans(h.ctx, member)
	if err != nil {
		t.Fatalf("list user plans: %v", err)
	}
	if len(memberPlans) != 1 {
		t.Fatalf("expected one visible plan, got %d", len(memberPlans))
	}
	if memberPlans[0].Membership == nil || memberPlans[0].Membership.ID != membership.ID {
		t.Fatalf("expected membership in plan view, got %+v", memberPlans[0].Membership)
	}
}

func TestProcessCycleHandlesPartialPaymentGraceSuspensionAndRecovery(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	owner := Actor{TelegramID: 2001, Username: "owner"}
	member := Actor{TelegramID: 2002, Username: "member"}
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
		t.Fatalf("process initial payment: %v", err)
	}

	members, err := h.app.ListPlanMembers(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatalf("list members after first payment: %v", err)
	}
	if members[0].SeatStatus != domain.MembershipActive {
		t.Fatalf("expected active after first payment, got %s", members[0].SeatStatus)
	}

	renewalLead := time.Date(2026, time.March, 26, 8, 0, 0, 0, h.loc)
	if _, err := h.app.ProcessCycle(h.ctx, renewalLead); err != nil {
		t.Fatalf("generate renewal invoice: %v", err)
	}

	renewalInvoice, err := latestInvoiceForPlan(h, plan.ID)
	if err != nil {
		t.Fatalf("latest renewal invoice: %v", err)
	}
	if renewalInvoice.ID == firstInvoice.ID {
		t.Fatalf("expected a new renewal invoice")
	}
	renewalInvoice, err = h.app.QuoteInvoice(h.ctx, member, renewalInvoice.ID, "USDC", "solana", renewalLead)
	if err != nil {
		t.Fatalf("quote renewal invoice: %v", err)
	}
	underpay := subtractAtomic(t, renewalInvoice.QuotedPayAmount, "1000000")
	if err := h.app.SimulateInvoicePayment(h.ctx, member, renewalInvoice.ID, underpay, renewalLead); err != nil {
		t.Fatalf("simulate underpayment: %v", err)
	}
	if _, err := h.app.ProcessCycle(h.ctx, renewalLead.Add(5*time.Minute)); err != nil {
		t.Fatalf("process underpayment: %v", err)
	}

	renewalInvoice, err = h.app.findInvoiceByID(h.ctx, renewalInvoice.ID)
	if err != nil {
		t.Fatalf("load renewal invoice after underpayment: %v", err)
	}
	if renewalInvoice.Status != domain.InvoiceUnderpaid {
		t.Fatalf("expected underpaid invoice, got %s", renewalInvoice.Status)
	}

	if _, err := h.app.ProcessCycle(h.ctx, time.Date(2026, time.April, 1, 12, 0, 0, 0, h.loc)); err != nil {
		t.Fatalf("start grace: %v", err)
	}
	members, err = h.app.ListPlanMembers(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatalf("list members in grace: %v", err)
	}
	if members[0].SeatStatus != domain.MembershipGrace {
		t.Fatalf("expected grace membership, got %s", members[0].SeatStatus)
	}

	if _, err := h.app.ProcessCycle(h.ctx, time.Date(2026, time.April, 5, 12, 0, 0, 0, h.loc)); err != nil {
		t.Fatalf("suspend member: %v", err)
	}
	members, err = h.app.ListPlanMembers(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatalf("list members suspended: %v", err)
	}
	if members[0].SeatStatus != domain.MembershipSuspended {
		t.Fatalf("expected suspended membership, got %s", members[0].SeatStatus)
	}

	if err := h.app.SimulateInvoicePayment(h.ctx, member, renewalInvoice.ID, "1000000", time.Date(2026, time.April, 5, 12, 5, 0, 0, h.loc)); err != nil {
		t.Fatalf("simulate top-up: %v", err)
	}
	if _, err := h.app.ProcessCycle(h.ctx, time.Date(2026, time.April, 5, 12, 10, 0, 0, h.loc)); err != nil {
		t.Fatalf("recover member: %v", err)
	}
	members, err = h.app.ListPlanMembers(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatalf("list members recovered: %v", err)
	}
	if members[0].SeatStatus != domain.MembershipActive {
		t.Fatalf("expected active after recovery, got %s", members[0].SeatStatus)
	}

	ledger, err := h.app.Ledger(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	events := map[string]bool{}
	for _, event := range ledger.Events {
		events[event.EventName] = true
	}
	for _, required := range []string{"payment_confirmed", "grace_started", "seat_suspended"} {
		if !events[required] {
			t.Fatalf("expected ledger event %s, got %+v", required, events)
		}
	}
}

func TestCreditCarryForwardAutoSettlesNextInvoice(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	owner := Actor{TelegramID: 3001, Username: "owner"}
	member := Actor{TelegramID: 3002, Username: "member"}
	joinAt := time.Date(2026, time.March, 20, 10, 0, 0, 0, h.loc)

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
	_, invoice, err := h.app.JoinPlan(h.ctx, member, invite.InviteCode, joinAt)
	if err != nil {
		t.Fatalf("join plan: %v", err)
	}
	invoice, err = h.app.QuoteInvoice(h.ctx, member, invoice.ID, "USDC", "solana", joinAt)
	if err != nil {
		t.Fatalf("quote first invoice: %v", err)
	}
	overpay := addAtomic(t, invoice.QuotedPayAmount, "9900000")
	if err := h.app.SimulateInvoicePayment(h.ctx, member, invoice.ID, overpay, joinAt); err != nil {
		t.Fatalf("simulate overpayment: %v", err)
	}
	if _, err := h.app.ProcessCycle(h.ctx, joinAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("process overpayment: %v", err)
	}

	if _, err := h.app.ProcessCycle(h.ctx, time.Date(2026, time.March, 26, 10, 0, 0, 0, h.loc)); err != nil {
		t.Fatalf("generate next invoice: %v", err)
	}

	ledger, err := h.app.Ledger(h.ctx, owner, plan.ID)
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if len(ledger.Credits) == 0 {
		t.Fatalf("expected overpayment credit entry")
	}
	var settled *domain.Invoice
	for i := range ledger.Invoices {
		if ledger.Invoices[i].ID == invoice.ID {
			continue
		}
		if ledger.Invoices[i].Status == domain.InvoiceConfirmed && ledger.Invoices[i].CreditAppliedMinor > 0 {
			settled = &ledger.Invoices[i]
			break
		}
	}
	if settled == nil {
		t.Fatalf("expected a confirmed renewal invoice covered by credit, got %+v", ledger.Invoices)
	}
	if settled.AmountDueMinor() != 0 {
		t.Fatalf("expected zero due after credit, got %d", settled.AmountDueMinor())
	}
}

func TestProcessProviderWebhookEventIsIdempotentAndCreatesCredit(t *testing.T) {
	t.Parallel()

	h := newTestAppHarness(t)
	owner := Actor{TelegramID: 4001, Username: "owner"}
	member := Actor{TelegramID: 4002, Username: "member"}
	now := time.Date(2026, time.March, 22, 11, 0, 0, 0, h.loc)

	_, invite, err := h.app.CreatePlan(h.ctx, owner, CreatePlanInput{
		ServiceCode:      "spotify_family",
		TotalPriceMinor:  1800,
		SeatLimit:        2,
		RenewalDate:      time.Date(2026, time.April, 1, 0, 0, 0, 0, h.loc),
		SharingPolicyAck: true,
	}, now)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	_, invoice, err := h.app.JoinPlan(h.ctx, member, invite.InviteCode, now)
	if err != nil {
		t.Fatalf("join plan: %v", err)
	}
	invoice, err = h.app.QuoteInvoice(h.ctx, member, invoice.ID, "USDC", "solana", now)
	if err != nil {
		t.Fatalf("quote invoice: %v", err)
	}

	overpayAtomic := addAtomic(t, invoice.QuotedPayAmount, "1000000")
	event := payments.WebhookEvent{
		ProviderName:      "nowpayments",
		ExternalEventID:   "evt-1",
		ProviderInvoiceID: invoice.ProviderInvoiceID,
		EventType:         "finished",
		PayloadJSON:       `{"payment_id":"evt-1"}`,
		Payments: []payments.ProviderPayment{{
			ExternalPaymentID: "evt-1",
			AmountAtomic:      overpayAtomic,
			Asset:             "USDC",
			Network:           "solana",
			TxHash:            "tx-evt-1",
			Confirmations:     2,
			SettlementStatus:  "finished",
			ReceivedAt:        now.UTC(),
		}},
	}

	notifications, duplicate, invoiceFound, err := h.app.ProcessProviderWebhookEvent(h.ctx, event, now)
	if err != nil {
		t.Fatalf("process webhook event: %v", err)
	}
	if duplicate || !invoiceFound {
		t.Fatalf("expected first webhook event to settle invoice, got duplicate=%v invoiceFound=%v", duplicate, invoiceFound)
	}
	if len(notifications) != 1 || !strings.Contains(notifications[0].Message, "Payment confirmed") {
		t.Fatalf("expected payment confirmation notification, got %+v", notifications)
	}

	notifications, duplicate, invoiceFound, err = h.app.ProcessProviderWebhookEvent(h.ctx, event, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("reprocess duplicate webhook event: %v", err)
	}
	if !duplicate || invoiceFound {
		t.Fatalf("expected duplicate webhook to be ignored, got duplicate=%v invoiceFound=%v", duplicate, invoiceFound)
	}
	if len(notifications) != 0 {
		t.Fatalf("expected duplicate webhook to skip notifications, got %+v", notifications)
	}

	ledger, err := h.app.Ledger(h.ctx, owner, invoice.PlanID)
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	if len(ledger.Credits) != 1 || ledger.Credits[0].AmountMinor <= 0 {
		t.Fatalf("expected overpayment credit from webhook settlement, got %+v", ledger.Credits)
	}
}

func latestInvoiceForPlan(h *testAppHarness, planID string) (domain.Invoice, error) {
	ledger, err := h.app.Ledger(h.ctx, Actor{TelegramID: 9000, Username: "operator"}, planID)
	if err != nil {
		return domain.Invoice{}, err
	}
	if len(ledger.Invoices) == 0 {
		return domain.Invoice{}, fmt.Errorf("no invoices found")
	}
	return ledger.Invoices[0], nil
}

func addAtomic(t *testing.T, raw string, delta string) string {
	t.Helper()
	value := new(big.Int)
	if _, ok := value.SetString(raw, 10); !ok {
		t.Fatalf("invalid atomic amount %q", raw)
	}
	change := new(big.Int)
	if _, ok := change.SetString(delta, 10); !ok {
		t.Fatalf("invalid delta %q", delta)
	}
	return value.Add(value, change).String()
}

func subtractAtomic(t *testing.T, raw string, delta string) string {
	t.Helper()
	value := new(big.Int)
	if _, ok := value.SetString(raw, 10); !ok {
		t.Fatalf("invalid atomic amount %q", raw)
	}
	change := new(big.Int)
	if _, ok := change.SetString(delta, 10); !ok {
		t.Fatalf("invalid delta %q", delta)
	}
	return value.Sub(value, change).String()
}
