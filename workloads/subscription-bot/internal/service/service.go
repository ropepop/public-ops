package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"subscriptionbot/internal/config"
	"subscriptionbot/internal/domain"
	"subscriptionbot/internal/payments"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrNotFound     = errors.New("not found")
)

type Actor struct {
	TelegramID int64
	Username   string
}

type CreatePlanInput struct {
	ServiceCode      string
	TotalPriceMinor  int64
	SeatLimit        int
	RenewalDate      time.Time
	SharingPolicyAck bool
	AccessMode       string
}

type App struct {
	db       *sql.DB
	cfg      config.Config
	provider payments.Provider
	loc      *time.Location
}

func New(db *sql.DB, cfg config.Config, provider payments.Provider, loc *time.Location) *App {
	return &App{db: db, cfg: cfg, provider: provider, loc: loc}
}

func (a *App) IsOperator(telegramID int64) bool {
	_, ok := a.cfg.OperatorIDs[telegramID]
	return ok
}

func (a *App) ensureActorAllowed(ctx context.Context, actor Actor) error {
	blocked, reason, err := a.IsTelegramIDDenied(ctx, actor.TelegramID)
	if err != nil {
		return err
	}
	if blocked {
		if strings.TrimSpace(reason) == "" {
			reason = "this account is blocked from billing actions"
		}
		return fmt.Errorf("%w: %s", ErrUnauthorized, reason)
	}
	return nil
}

func (a *App) HostedProvider() (payments.HostedProvider, bool) {
	provider, ok := a.provider.(payments.HostedProvider)
	return provider, ok
}

func (a *App) SeedCatalog(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339)
	entries := []domain.ServiceCatalogEntry{
		{ServiceCode: "spotify_family", DisplayName: "Spotify Family", Category: "music", SharingPolicyNote: "Family members must use invite-based family seats.", AccessMode: "invite_seat", Status: domain.PlanStatusActive},
		{ServiceCode: "youtube_family", DisplayName: "YouTube Premium Family", Category: "video", SharingPolicyNote: "Use official family invite flow only.", AccessMode: "invite_seat", Status: domain.PlanStatusActive},
		{ServiceCode: "office365_family", DisplayName: "Microsoft 365 Family", Category: "productivity", SharingPolicyNote: "Each member must receive their own seat or invitation.", AccessMode: "invite_seat", Status: domain.PlanStatusActive},
		{ServiceCode: "canva_teams", DisplayName: "Canva Teams", Category: "design", SharingPolicyNote: "Team seat assignment must stay inside the official product.", AccessMode: "owner_confirmed", Status: domain.PlanStatusActive},
	}
	for _, entry := range entries {
		if _, err := a.db.ExecContext(ctx, `
			INSERT INTO service_catalog(service_code, display_name, category, sharing_policy_note, access_mode, status, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(service_code) DO UPDATE SET
				display_name = excluded.display_name,
				category = excluded.category,
				sharing_policy_note = excluded.sharing_policy_note,
				access_mode = excluded.access_mode,
				status = excluded.status
		`, entry.ServiceCode, entry.DisplayName, entry.Category, entry.SharingPolicyNote, entry.AccessMode, entry.Status, now); err != nil {
			return fmt.Errorf("seed service catalog: %w", err)
		}
	}
	return nil
}

func (a *App) CreatePlan(ctx context.Context, actor Actor, input CreatePlanInput, now time.Time) (domain.Plan, domain.PlanInvite, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	if !input.SharingPolicyAck {
		return domain.Plan{}, domain.PlanInvite{}, fmt.Errorf("sharing policy acknowledgement is required")
	}
	if input.TotalPriceMinor <= 0 {
		return domain.Plan{}, domain.PlanInvite{}, fmt.Errorf("total monthly price must be positive")
	}
	if input.SeatLimit <= 0 {
		return domain.Plan{}, domain.PlanInvite{}, fmt.Errorf("seat limit must be positive")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	defer tx.Rollback()

	owner, err := a.ensureUserTx(ctx, tx, actor)
	if err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	catalog, err := a.lookupCatalogTx(ctx, tx, input.ServiceCode)
	if err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	perSeat := divideRoundHalfUp(input.TotalPriceMinor, int64(input.SeatLimit))
	plan := domain.Plan{
		ID:                newID("plan"),
		OwnerUserID:       owner.ID,
		OwnerTelegramID:   owner.TelegramID,
		ServiceCode:       catalog.ServiceCode,
		ServiceName:       catalog.DisplayName,
		Category:          catalog.Category,
		TotalPriceMinor:   input.TotalPriceMinor,
		PerSeatBaseMinor:  perSeat,
		PlatformFeeBps:    a.cfg.PlatformFeeBps,
		StableAsset:       "USDC",
		BillingPeriod:     "monthly",
		RenewalDate:       atLocalMidnight(input.RenewalDate, a.loc),
		SeatLimit:         input.SeatLimit,
		AccessMode:        chooseAccessMode(input.AccessMode, catalog.AccessMode),
		SharingPolicyNote: catalog.SharingPolicyNote,
		Status:            domain.PlanStatusActive,
		CreatedAt:         now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO plans(
			id, owner_user_id, service_code, total_price_minor, per_seat_base_minor, platform_fee_bps,
			stable_asset, billing_period, renewal_date, seat_limit, access_mode, sharing_policy_note, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, plan.ID, plan.OwnerUserID, plan.ServiceCode, plan.TotalPriceMinor, plan.PerSeatBaseMinor, plan.PlatformFeeBps, plan.StableAsset, plan.BillingPeriod, plan.RenewalDate.Format(time.RFC3339), plan.SeatLimit, plan.AccessMode, plan.SharingPolicyNote, plan.Status, plan.CreatedAt.Format(time.RFC3339)); err != nil {
		return domain.Plan{}, domain.PlanInvite{}, fmt.Errorf("insert plan: %w", err)
	}
	invite, err := a.createInviteTx(ctx, tx, plan.ID, owner.ID, now)
	if err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	if err := a.appendEventTx(ctx, tx, "plan", plan.ID, "plan_created", map[string]any{
		"owner_telegram_id": actor.TelegramID,
		"service_code":      plan.ServiceCode,
		"seat_limit":        plan.SeatLimit,
	}); err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Plan{}, domain.PlanInvite{}, err
	}
	return plan, invite, nil
}

func (a *App) CreateInvite(ctx context.Context, actor Actor, planID string, now time.Time) (domain.PlanInvite, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.PlanInvite{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.PlanInvite{}, err
	}
	defer tx.Rollback()
	user, err := a.ensureUserTx(ctx, tx, actor)
	if err != nil {
		return domain.PlanInvite{}, err
	}
	plan, err := a.lookupPlanTx(ctx, tx, planID)
	if err != nil {
		return domain.PlanInvite{}, err
	}
	if plan.OwnerUserID != user.ID && !a.IsOperator(actor.TelegramID) {
		return domain.PlanInvite{}, ErrUnauthorized
	}
	invite, err := a.createInviteTx(ctx, tx, plan.ID, user.ID, now)
	if err != nil {
		return domain.PlanInvite{}, err
	}
	if err := a.appendEventTx(ctx, tx, "plan", plan.ID, "invite_created", map[string]any{"invite_code": invite.InviteCode}); err != nil {
		return domain.PlanInvite{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.PlanInvite{}, err
	}
	return invite, nil
}

func (a *App) ActiveInviteForPlan(ctx context.Context, actor Actor, planID string) (domain.PlanInvite, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.PlanInvite{}, err
	}
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return domain.PlanInvite{}, err
	}
	plan, err := a.lookupPlan(ctx, planID)
	if err != nil {
		return domain.PlanInvite{}, err
	}
	if plan.OwnerUserID != user.ID && !a.IsOperator(actor.TelegramID) {
		return domain.PlanInvite{}, ErrUnauthorized
	}
	return a.lookupLatestActiveInviteForPlan(ctx, plan.ID)
}

func (a *App) JoinPlan(ctx context.Context, actor Actor, inviteCode string, now time.Time) (domain.Membership, domain.Invoice, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	defer tx.Rollback()
	user, err := a.ensureUserTx(ctx, tx, actor)
	if err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	invite, plan, err := a.lookupInvitePlanTx(ctx, tx, inviteCode)
	if err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	membership, err := a.ensureMembershipTx(ctx, tx, plan, user, now)
	if err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	cycleStart, cycleEnd := cycleWindow(plan.RenewalDate, now.In(a.loc), a.loc)
	baseMinor := proratedBaseMinor(plan.PerSeatBaseMinor, now.In(a.loc), cycleStart, cycleEnd)
	invoice, err := a.createInvoiceTx(ctx, tx, membership, plan, cycleStart, cycleEnd, now, baseMinor, now)
	if err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE memberships SET latest_invoice_id = ? WHERE id = ?`, invoice.ID, membership.ID); err != nil {
		return domain.Membership{}, domain.Invoice{}, fmt.Errorf("update membership latest invoice: %w", err)
	}
	membership.LatestInvoiceID = invoice.ID
	if invoice.Status == domain.InvoiceConfirmed {
		if err := a.activateMembershipForInvoiceTx(ctx, tx, invoice, now.UTC()); err != nil {
			return domain.Membership{}, domain.Invoice{}, err
		}
		membership.SeatStatus = domain.MembershipActive
	}
	if err := a.appendEventTx(ctx, tx, "membership", membership.ID, "joined_plan", map[string]any{"plan_id": plan.ID, "invite_id": invite.ID}); err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Membership{}, domain.Invoice{}, err
	}
	return membership, invoice, nil
}

func (a *App) QuoteInvoice(ctx context.Context, actor Actor, invoiceID string, payAsset string, network string, now time.Time) (domain.Invoice, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.Invoice{}, err
	}
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return domain.Invoice{}, err
	}
	invoice, err := a.findInvoiceByID(ctx, invoiceID)
	if err != nil {
		return domain.Invoice{}, err
	}
	if invoice.UserID != user.ID && !a.IsOperator(actor.TelegramID) {
		return domain.Invoice{}, ErrUnauthorized
	}
	remainingDue := invoice.AmountDueMinor() - invoice.PaidMinor
	if remainingDue <= 0 {
		return domain.Invoice{}, fmt.Errorf("invoice is already fully covered")
	}
	if strings.TrimSpace(payAsset) == "" {
		payAsset = a.cfg.DefaultPayAsset
	}
	if strings.TrimSpace(network) == "" {
		network = a.cfg.DefaultPayNetwork
	}
	quote, err := a.provider.CreateInvoiceQuote(ctx, payments.QuoteRequest{
		InvoiceID:        invoice.ID,
		AnchorTotalMinor: remainingDue,
		PayAsset:         payAsset,
		Network:          network,
		WebhookURL:       strings.TrimRight(a.cfg.WebPublicBaseURL, "/") + "/api/v1/payments/webhook/nowpayments",
		Now:              now.UTC(),
		QuoteTTL:         a.cfg.QuoteTTL,
	})
	if err != nil {
		return domain.Invoice{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Invoice{}, err
	}
	defer tx.Rollback()
	invoice.PayAsset = quote.PayAsset
	invoice.Network = quote.Network
	invoice.QuotedPayAmount = quote.QuotedAmountAtomic
	invoice.QuoteRateLabel = quote.QuoteRateLabel
	invoice.QuoteExpiresAt = &quote.QuoteExpiresAt
	invoice.PaymentRef = quote.PaymentRef
	invoice.ProviderInvoiceID = quote.ProviderInvoiceID
	invoice.Status = domain.InvoiceOpen
	invoice.UpdatedAt = now.UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE invoices
		SET pay_asset = ?, network = ?, quoted_pay_amount = ?, quote_rate_label = ?, quote_expires_at = ?, payment_ref = ?, provider_invoice_id = ?, status = ?, updated_at = ?
		WHERE id = ?
	`, invoice.PayAsset, invoice.Network, invoice.QuotedPayAmount, invoice.QuoteRateLabel, invoice.QuoteExpiresAt.Format(time.RFC3339), invoice.PaymentRef, invoice.ProviderInvoiceID, invoice.Status, invoice.UpdatedAt.Format(time.RFC3339), invoice.ID); err != nil {
		return domain.Invoice{}, fmt.Errorf("update invoice quote: %w", err)
	}
	if err := a.appendEventTx(ctx, tx, "invoice", invoice.ID, "invoice_quoted", map[string]any{"pay_asset": invoice.PayAsset, "network": invoice.Network}); err != nil {
		return domain.Invoice{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (a *App) QuoteLatestInvoice(ctx context.Context, actor Actor, payAsset string, network string, now time.Time) (domain.Invoice, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.Invoice{}, err
	}
	invoice, err := a.LatestInvoice(ctx, actor)
	if err != nil {
		return domain.Invoice{}, err
	}
	if invoice == nil {
		return domain.Invoice{}, ErrNotFound
	}
	return a.QuoteInvoice(ctx, actor, invoice.ID, payAsset, network, now)
}

func (a *App) SimulateInvoicePayment(ctx context.Context, actor Actor, invoiceID string, amountAtomic string, now time.Time) error {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return err
	}
	simulatable, ok := a.provider.(payments.Simulatable)
	if !ok {
		return fmt.Errorf("configured provider does not support simulation")
	}
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return err
	}
	invoice, err := a.findInvoiceByID(ctx, invoiceID)
	if err != nil {
		return err
	}
	if invoice.UserID != user.ID && !a.IsOperator(actor.TelegramID) {
		return ErrUnauthorized
	}
	if strings.TrimSpace(invoice.ProviderInvoiceID) == "" {
		return fmt.Errorf("invoice has not been quoted yet")
	}
	if _, err := simulatable.SimulatePayment(ctx, invoice.ProviderInvoiceID, amountAtomic, now.UTC()); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := a.appendEventTx(ctx, tx, "invoice", invoice.ID, "sandbox_payment_submitted", map[string]any{"provider_invoice_id": invoice.ProviderInvoiceID}); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) LatestInvoice(ctx context.Context, actor Actor) (*domain.Invoice, error) {
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return nil, err
	}
	row := a.db.QueryRowContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.user_id = ?
		  AND i.status IN ('draft', 'open', 'detected', 'underpaid')
		ORDER BY i.created_at DESC
		LIMIT 1
	`, user.ID)
	invoice, err := scanInvoice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &invoice, nil
}

func (a *App) ListCatalog(ctx context.Context) ([]domain.ServiceCatalogEntry, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT service_code, display_name, category, sharing_policy_note, access_mode, status
		FROM service_catalog
		WHERE status = 'active'
		ORDER BY display_name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.ServiceCatalogEntry, 0)
	for rows.Next() {
		var entry domain.ServiceCatalogEntry
		if err := rows.Scan(&entry.ServiceCode, &entry.DisplayName, &entry.Category, &entry.SharingPolicyNote, &entry.AccessMode, &entry.Status); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (a *App) ListUserPlans(ctx context.Context, actor Actor) ([]domain.PlanView, error) {
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return nil, err
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.id, p.owner_user_id, owner.telegram_id, p.service_code, sc.display_name, sc.category,
		       p.total_price_minor, p.per_seat_base_minor, p.platform_fee_bps, p.stable_asset, p.billing_period,
		       p.renewal_date, p.seat_limit, p.access_mode, p.sharing_policy_note, p.status, p.created_at,
		       m.id, m.seat_status, m.joined_at, m.grace_until, m.removed_at, m.latest_invoice_id,
		       (SELECT COUNT(*) FROM memberships mx WHERE mx.plan_id = p.id AND mx.removed_at IS NULL) AS member_count
		FROM plans p
		INNER JOIN users owner ON owner.id = p.owner_user_id
		INNER JOIN service_catalog sc ON sc.service_code = p.service_code
		LEFT JOIN memberships m ON m.plan_id = p.id AND m.user_id = ?
		WHERE p.owner_user_id = ?
		   OR m.id IS NOT NULL
		ORDER BY p.created_at DESC
	`, user.ID, user.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	views := make([]domain.PlanView, 0)
	latestInvoiceIDs := make([]string, 0)
	for rows.Next() {
		var view domain.PlanView
		var renewalDate, createdAt string
		var membershipID, membershipStatus, joinedAt, latestInvoiceID sql.NullString
		var graceUntil sql.NullString
		var removedAt sql.NullString
		if err := rows.Scan(
			&view.Plan.ID, &view.Plan.OwnerUserID, &view.Plan.OwnerTelegramID, &view.Plan.ServiceCode, &view.Plan.ServiceName, &view.Plan.Category,
			&view.Plan.TotalPriceMinor, &view.Plan.PerSeatBaseMinor, &view.Plan.PlatformFeeBps, &view.Plan.StableAsset, &view.Plan.BillingPeriod,
			&renewalDate, &view.Plan.SeatLimit, &view.Plan.AccessMode, &view.Plan.SharingPolicyNote, &view.Plan.Status, &createdAt,
			&membershipID, &membershipStatus, &joinedAt, &graceUntil, &removedAt, &latestInvoiceID, &view.MemberCount,
		); err != nil {
			return nil, err
		}
		view.Plan.RenewalDate, _ = time.Parse(time.RFC3339, renewalDate)
		view.Plan.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		view.AvailableSeats = maxInt(0, view.Plan.SeatLimit-view.MemberCount)
		view.IsOwner = view.Plan.OwnerUserID == user.ID
		if membershipID.Valid && membershipID.String != "" {
			member := &domain.Membership{
				ID:              membershipID.String,
				PlanID:          view.Plan.ID,
				UserID:          user.ID,
				UserTelegramID:  user.TelegramID,
				Username:        user.Username,
				SeatStatus:      membershipStatus.String,
				LatestInvoiceID: latestInvoiceID.String,
			}
			if joinedAt.Valid {
				member.JoinedAt, _ = time.Parse(time.RFC3339, joinedAt.String)
			}
			if graceUntil.Valid {
				parsed, _ := time.Parse(time.RFC3339, graceUntil.String)
				member.GraceUntil = &parsed
			}
			if removedAt.Valid {
				parsed, _ := time.Parse(time.RFC3339, removedAt.String)
				member.RemovedAt = &parsed
			}
			view.Membership = member
			if latestInvoiceID.Valid && latestInvoiceID.String != "" {
				latestInvoiceIDs = append(latestInvoiceIDs, latestInvoiceID.String)
			} else {
				latestInvoiceIDs = append(latestInvoiceIDs, "")
			}
		} else {
			latestInvoiceIDs = append(latestInvoiceIDs, "")
		}
		views = append(views, view)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for idx := range views {
		if latestInvoiceIDs[idx] == "" {
			continue
		}
		if invoice, err := a.findInvoiceByID(ctx, latestInvoiceIDs[idx]); err == nil {
			views[idx].OpenInvoice = &invoice
		}
	}
	return views, nil
}

func (a *App) ListPlanMembers(ctx context.Context, actor Actor, planID string) ([]domain.Membership, error) {
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return nil, err
	}
	plan, err := a.lookupPlan(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan.OwnerUserID != user.ID && !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.id, m.plan_id, m.user_id, u.telegram_id, u.username, m.seat_status, m.joined_at, m.grace_until, m.removed_at, m.latest_invoice_id
		FROM memberships m
		INNER JOIN users u ON u.id = m.user_id
		WHERE m.plan_id = ?
		ORDER BY m.joined_at ASC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Membership, 0)
	for rows.Next() {
		member, err := scanMembershipRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func (a *App) Ledger(ctx context.Context, actor Actor, planID string) (domain.Ledger, error) {
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return domain.Ledger{}, err
	}
	plan, err := a.lookupPlan(ctx, planID)
	if err != nil {
		return domain.Ledger{}, err
	}
	allowed := plan.OwnerUserID == user.ID || a.IsOperator(actor.TelegramID)
	if !allowed {
		var exists int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE plan_id = ? AND user_id = ?`, planID, user.ID).Scan(&exists); err != nil {
			return domain.Ledger{}, err
		}
		allowed = exists > 0
	}
	if !allowed {
		return domain.Ledger{}, ErrUnauthorized
	}
	ledger := domain.Ledger{Plan: plan}
	if ledger.Invoices, err = a.listInvoicesForPlan(ctx, planID); err != nil {
		return domain.Ledger{}, err
	}
	if ledger.Payments, err = a.listPaymentsForPlan(ctx, planID); err != nil {
		return domain.Ledger{}, err
	}
	if ledger.Credits, err = a.listCreditsForPlan(ctx, planID); err != nil {
		return domain.Ledger{}, err
	}
	if ledger.Events, err = a.listEventsForPlan(ctx, planID); err != nil {
		return domain.Ledger{}, err
	}
	return ledger, nil
}

func (a *App) OpenSupportTicket(ctx context.Context, actor Actor, planID string, body string, now time.Time) (domain.SupportTicket, error) {
	if err := a.ensureActorAllowed(ctx, actor); err != nil {
		return domain.SupportTicket{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SupportTicket{}, err
	}
	defer tx.Rollback()
	user, err := a.ensureUserTx(ctx, tx, actor)
	if err != nil {
		return domain.SupportTicket{}, err
	}
	ticket := domain.SupportTicket{
		ID:        newID("ticket"),
		PlanID:    strings.TrimSpace(planID),
		UserID:    user.ID,
		Subject:   "Telegram support request",
		Body:      strings.TrimSpace(body),
		Status:    domain.TicketOpen,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
	if ticket.Body == "" {
		ticket.Body = "Support requested from Telegram"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO support_tickets(id, plan_id, user_id, subject, body, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, ticket.ID, ticket.PlanID, ticket.UserID, ticket.Subject, ticket.Body, ticket.Status, ticket.CreatedAt.Format(time.RFC3339), ticket.UpdatedAt.Format(time.RFC3339)); err != nil {
		return domain.SupportTicket{}, err
	}
	if err := a.appendEventTx(ctx, tx, "support_ticket", ticket.ID, "support_opened", map[string]any{"plan_id": planID}); err != nil {
		return domain.SupportTicket{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.SupportTicket{}, err
	}
	return ticket, nil
}

func (a *App) AdminOverview(ctx context.Context, actor Actor) (domain.AdminOverview, error) {
	if !a.IsOperator(actor.TelegramID) {
		return domain.AdminOverview{}, ErrUnauthorized
	}
	overview := domain.AdminOverview{}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&overview.UsersTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans`).Scan(&overview.PlansTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoices WHERE status IN ('open', 'underpaid', 'detected')`).Scan(&overview.OpenInvoicesTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE seat_status = 'suspended'`).Scan(&overview.FailedRenewalsTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM support_tickets WHERE status = 'open'`).Scan(&overview.SupportOpenTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM denylist_entries WHERE entry_type = 'telegram_id'`).Scan(&overview.BlockedActorsTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE event_name IN ('provider_event_unmatched', 'provider_payment_denied', 'provider_event_failed')`).Scan(&overview.PaymentAlertsTotal); err != nil {
		return domain.AdminOverview{}, err
	}
	if err := a.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(base_minor), 0) FROM invoices WHERE status = 'confirmed'`).Scan(&overview.PayoutDueMinor); err != nil {
		return domain.AdminOverview{}, err
	}
	return overview, nil
}

func (a *App) ProcessProviderWebhookEvent(ctx context.Context, event payments.WebhookEvent, now time.Time) ([]domain.Notification, bool, bool, error) {
	payload := any(nil)
	if strings.TrimSpace(event.PayloadJSON) != "" {
		payload = json.RawMessage(event.PayloadJSON)
	}
	inserted, err := a.RecordProviderEvent(ctx, event.ProviderName, event.ExternalEventID, event.EventType, event.ProviderInvoiceID, payload, now)
	if err != nil {
		return nil, false, false, err
	}
	if !inserted {
		return nil, true, false, nil
	}

	invoice, err := a.findInvoiceByProviderInvoiceID(ctx, event.ProviderInvoiceID)
	if errors.Is(err, sql.ErrNoRows) {
		_ = a.appendEvent(ctx, "payment_provider", event.ExternalEventID, "provider_event_unmatched", map[string]any{
			"provider_name":       event.ProviderName,
			"provider_invoice_id": event.ProviderInvoiceID,
			"detail":              "provider event did not match a known invoice",
		})
		return nil, false, false, nil
	}
	if err != nil {
		_ = a.appendEvent(ctx, "payment_provider", event.ExternalEventID, "provider_event_failed", map[string]any{
			"provider_name":       event.ProviderName,
			"provider_invoice_id": event.ProviderInvoiceID,
			"detail":              err.Error(),
		})
		return nil, false, false, err
	}

	normalized := make([]payments.NormalizedPayment, 0, len(event.Payments))
	for _, providerPayment := range event.Payments {
		if blocked, reason, denyErr := a.IsPaymentReferenceDenied(ctx, providerPayment.ExternalPaymentID); denyErr != nil {
			return nil, false, true, denyErr
		} else if blocked {
			_ = a.appendEvent(ctx, "payment_provider", invoice.ID, "provider_payment_denied", map[string]any{
				"provider_name":       event.ProviderName,
				"provider_invoice_id": event.ProviderInvoiceID,
				"detail":              firstNonEmpty(reason, "blocked payment reference"),
			})
			continue
		}
		if blocked, reason, denyErr := a.IsTxHashDenied(ctx, providerPayment.TxHash); denyErr != nil {
			return nil, false, true, denyErr
		} else if blocked {
			_ = a.appendEvent(ctx, "payment_provider", invoice.ID, "provider_payment_denied", map[string]any{
				"provider_name":       event.ProviderName,
				"provider_invoice_id": event.ProviderInvoiceID,
				"detail":              firstNonEmpty(reason, "blocked payment transaction"),
			})
			continue
		}
		item, err := a.provider.NormalizeProviderPayment(ctx, event.ProviderInvoiceID, providerPayment)
		if err != nil {
			_ = a.appendEvent(ctx, "payment_provider", invoice.ID, "provider_event_failed", map[string]any{
				"provider_name":       event.ProviderName,
				"provider_invoice_id": event.ProviderInvoiceID,
				"detail":              err.Error(),
			})
			return nil, false, true, err
		}
		normalized = append(normalized, item)
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, true, err
	}
	defer tx.Rollback()

	notifications, err := a.applyProviderInvoiceSnapshotTx(ctx, tx, invoice, mapProviderEventStatus(event.ProviderName, event.EventType), normalized, now.UTC())
	if err != nil {
		return nil, false, true, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, true, err
	}
	return notifications, false, true, nil
}

func (a *App) ProcessCycle(ctx context.Context, now time.Time) ([]domain.Notification, error) {
	invoices, err := a.listPendingInvoices(ctx)
	if err != nil {
		return nil, err
	}
	type providerSnapshot struct {
		invoice  domain.Invoice
		status   string
		payments []payments.NormalizedPayment
	}
	snapshots := make([]providerSnapshot, 0, len(invoices))
	for _, invoice := range invoices {
		status, err := a.provider.GetInvoiceStatus(ctx, invoice.ProviderInvoiceID, now.UTC())
		if err != nil {
			return nil, err
		}
		paymentsList, err := a.provider.ListInvoiceTransactions(ctx, invoice.ProviderInvoiceID)
		if err != nil {
			return nil, err
		}
		normalized := make([]payments.NormalizedPayment, 0, len(paymentsList))
		for _, providerPayment := range paymentsList {
			item, err := a.provider.NormalizeProviderPayment(ctx, invoice.ProviderInvoiceID, providerPayment)
			if err != nil {
				return nil, err
			}
			normalized = append(normalized, item)
		}
		snapshots = append(snapshots, providerSnapshot{
			invoice:  invoice,
			status:   status,
			payments: normalized,
		})
	}

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	notifications := make([]domain.Notification, 0)

	for _, snapshot := range snapshots {
		currentNotifications, err := a.applyProviderInvoiceSnapshotTx(ctx, tx, snapshot.invoice, snapshot.status, snapshot.payments, now.UTC())
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, currentNotifications...)
	}

	if err := a.generateRenewalInvoicesTx(ctx, tx, now.In(a.loc), &notifications); err != nil {
		return nil, err
	}
	if err := a.sendDueRemindersTx(ctx, tx, now.In(a.loc), &notifications); err != nil {
		return nil, err
	}
	if err := a.updateGraceAndSuspensionTx(ctx, tx, now.In(a.loc), &notifications); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return notifications, nil
}

func (a *App) ensureUser(ctx context.Context, actor Actor) (domain.User, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.User{}, err
	}
	defer tx.Rollback()
	user, err := a.ensureUserTx(ctx, tx, actor)
	if err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (a *App) ensureUserTx(ctx context.Context, tx *sql.Tx, actor Actor) (domain.User, error) {
	role := domain.RoleMember
	if a.IsOperator(actor.TelegramID) {
		role = domain.RoleOperator
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(telegram_id, username, role, status, created_at)
		VALUES (?, ?, ?, 'active', ?)
		ON CONFLICT(telegram_id) DO UPDATE SET
			username = excluded.username,
			role = excluded.role
	`, actor.TelegramID, strings.TrimSpace(actor.Username), role, now); err != nil {
		return domain.User{}, fmt.Errorf("upsert user: %w", err)
	}
	row := tx.QueryRowContext(ctx, `SELECT id, telegram_id, username, role, status, created_at FROM users WHERE telegram_id = ?`, actor.TelegramID)
	return scanUser(row)
}

func (a *App) lookupCatalogTx(ctx context.Context, tx *sql.Tx, serviceCode string) (domain.ServiceCatalogEntry, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT service_code, display_name, category, sharing_policy_note, access_mode, status
		FROM service_catalog
		WHERE service_code = ? AND status = 'active'
	`, strings.TrimSpace(serviceCode))
	var entry domain.ServiceCatalogEntry
	if err := row.Scan(&entry.ServiceCode, &entry.DisplayName, &entry.Category, &entry.SharingPolicyNote, &entry.AccessMode, &entry.Status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ServiceCatalogEntry{}, fmt.Errorf("service %q is not in the approved catalog", serviceCode)
		}
		return domain.ServiceCatalogEntry{}, err
	}
	return entry, nil
}

func (a *App) lookupPlan(ctx context.Context, planID string) (domain.Plan, error) {
	row := a.db.QueryRowContext(ctx, `
		SELECT p.id, p.owner_user_id, owner.telegram_id, p.service_code, sc.display_name, sc.category,
		       p.total_price_minor, p.per_seat_base_minor, p.platform_fee_bps, p.stable_asset, p.billing_period,
		       p.renewal_date, p.seat_limit, p.access_mode, p.sharing_policy_note, p.status, p.created_at
		FROM plans p
		INNER JOIN users owner ON owner.id = p.owner_user_id
		INNER JOIN service_catalog sc ON sc.service_code = p.service_code
		WHERE p.id = ?
	`, planID)
	return scanPlan(row)
}

func (a *App) lookupPlanTx(ctx context.Context, tx *sql.Tx, planID string) (domain.Plan, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT p.id, p.owner_user_id, owner.telegram_id, p.service_code, sc.display_name, sc.category,
		       p.total_price_minor, p.per_seat_base_minor, p.platform_fee_bps, p.stable_asset, p.billing_period,
		       p.renewal_date, p.seat_limit, p.access_mode, p.sharing_policy_note, p.status, p.created_at
		FROM plans p
		INNER JOIN users owner ON owner.id = p.owner_user_id
		INNER JOIN service_catalog sc ON sc.service_code = p.service_code
		WHERE p.id = ?
	`, planID)
	return scanPlan(row)
}

func (a *App) lookupInvitePlanTx(ctx context.Context, tx *sql.Tx, inviteCode string) (domain.PlanInvite, domain.Plan, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT i.id, i.plan_id, i.invite_code, i.created_by_user_id, i.status, i.created_at,
		       p.id, p.owner_user_id, owner.telegram_id, p.service_code, sc.display_name, sc.category,
		       p.total_price_minor, p.per_seat_base_minor, p.platform_fee_bps, p.stable_asset, p.billing_period,
		       p.renewal_date, p.seat_limit, p.access_mode, p.sharing_policy_note, p.status, p.created_at
		FROM plan_invites i
		INNER JOIN plans p ON p.id = i.plan_id
		INNER JOIN users owner ON owner.id = p.owner_user_id
		INNER JOIN service_catalog sc ON sc.service_code = p.service_code
		WHERE i.invite_code = ? AND i.status = 'active' AND p.status = 'active'
	`, strings.TrimSpace(inviteCode))
	var invite domain.PlanInvite
	var inviteCreatedAt string
	var plan domain.Plan
	var planRenewal, planCreated string
	if err := row.Scan(
		&invite.ID, &invite.PlanID, &invite.InviteCode, &invite.CreatedByUserID, &invite.Status, &inviteCreatedAt,
		&plan.ID, &plan.OwnerUserID, &plan.OwnerTelegramID, &plan.ServiceCode, &plan.ServiceName, &plan.Category,
		&plan.TotalPriceMinor, &plan.PerSeatBaseMinor, &plan.PlatformFeeBps, &plan.StableAsset, &plan.BillingPeriod,
		&planRenewal, &plan.SeatLimit, &plan.AccessMode, &plan.SharingPolicyNote, &plan.Status, &planCreated,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PlanInvite{}, domain.Plan{}, ErrNotFound
		}
		return domain.PlanInvite{}, domain.Plan{}, err
	}
	invite.CreatedAt, _ = time.Parse(time.RFC3339, inviteCreatedAt)
	plan.RenewalDate, _ = time.Parse(time.RFC3339, planRenewal)
	plan.CreatedAt, _ = time.Parse(time.RFC3339, planCreated)
	return invite, plan, nil
}

func (a *App) lookupLatestActiveInviteForPlan(ctx context.Context, planID string) (domain.PlanInvite, error) {
	row := a.db.QueryRowContext(ctx, `
		SELECT id, plan_id, invite_code, created_by_user_id, status, created_at
		FROM plan_invites
		WHERE plan_id = ? AND status = 'active'
		ORDER BY created_at DESC
		LIMIT 1
	`, strings.TrimSpace(planID))
	var invite domain.PlanInvite
	var createdAt string
	if err := row.Scan(
		&invite.ID,
		&invite.PlanID,
		&invite.InviteCode,
		&invite.CreatedByUserID,
		&invite.Status,
		&createdAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.PlanInvite{}, ErrNotFound
		}
		return domain.PlanInvite{}, err
	}
	invite.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return invite, nil
}

func (a *App) createInviteTx(ctx context.Context, tx *sql.Tx, planID string, createdByUserID int64, now time.Time) (domain.PlanInvite, error) {
	invite := domain.PlanInvite{
		ID:              newID("invite"),
		PlanID:          planID,
		InviteCode:      strings.ToUpper(randomCode(4)),
		CreatedByUserID: createdByUserID,
		Status:          "active",
		CreatedAt:       now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO plan_invites(id, plan_id, invite_code, created_by_user_id, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, invite.ID, invite.PlanID, invite.InviteCode, invite.CreatedByUserID, invite.Status, invite.CreatedAt.Format(time.RFC3339)); err != nil {
		return domain.PlanInvite{}, fmt.Errorf("insert invite: %w", err)
	}
	return invite, nil
}

func (a *App) ensureMembershipTx(ctx context.Context, tx *sql.Tx, plan domain.Plan, user domain.User, now time.Time) (domain.Membership, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, plan_id, user_id, seat_status, joined_at, grace_until, removed_at, latest_invoice_id
		FROM memberships
		WHERE plan_id = ? AND user_id = ?
	`, plan.ID, user.ID)
	var membership domain.Membership
	var joinedAt string
	var graceUntil, removedAt sql.NullString
	err := row.Scan(&membership.ID, &membership.PlanID, &membership.UserID, &membership.SeatStatus, &joinedAt, &graceUntil, &removedAt, &membership.LatestInvoiceID)
	switch {
	case err == nil:
		membership.UserTelegramID = user.TelegramID
		membership.Username = user.Username
		membership.JoinedAt, _ = time.Parse(time.RFC3339, joinedAt)
		if graceUntil.Valid {
			parsed, _ := time.Parse(time.RFC3339, graceUntil.String)
			membership.GraceUntil = &parsed
		}
		if removedAt.Valid {
			parsed, _ := time.Parse(time.RFC3339, removedAt.String)
			membership.RemovedAt = &parsed
		}
		if membership.SeatStatus == domain.MembershipRemoved {
			return domain.Membership{}, fmt.Errorf("membership was removed from this plan")
		}
		return membership, nil
	case !errors.Is(err, sql.ErrNoRows):
		return domain.Membership{}, err
	}
	var memberCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memberships WHERE plan_id = ? AND removed_at IS NULL`, plan.ID).Scan(&memberCount); err != nil {
		return domain.Membership{}, err
	}
	if memberCount >= plan.SeatLimit {
		return domain.Membership{}, fmt.Errorf("this shared plan is full")
	}
	membership = domain.Membership{
		ID:              newID("member"),
		PlanID:          plan.ID,
		UserID:          user.ID,
		UserTelegramID:  user.TelegramID,
		Username:        user.Username,
		SeatStatus:      domain.MembershipPendingPayment,
		JoinedAt:        now.UTC(),
		LatestInvoiceID: "",
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO memberships(id, plan_id, user_id, seat_status, joined_at, grace_until, removed_at, latest_invoice_id)
		VALUES (?, ?, ?, ?, ?, NULL, NULL, '')
	`, membership.ID, membership.PlanID, membership.UserID, membership.SeatStatus, membership.JoinedAt.Format(time.RFC3339)); err != nil {
		return domain.Membership{}, err
	}
	return membership, nil
}

func (a *App) createInvoiceTx(ctx context.Context, tx *sql.Tx, membership domain.Membership, plan domain.Plan, cycleStart time.Time, cycleEnd time.Time, dueAt time.Time, baseMinor int64, now time.Time) (domain.Invoice, error) {
	feeMinor := feeAmount(baseMinor, a.cfg.PlatformFeeBps)
	invoice := domain.Invoice{
		ID:             newID("invoice"),
		MembershipID:   membership.ID,
		PlanID:         plan.ID,
		UserID:         membership.UserID,
		UserTelegramID: membership.UserTelegramID,
		CycleStart:     cycleStart.UTC(),
		CycleEnd:       cycleEnd.UTC(),
		DueAt:          dueAt.UTC(),
		BaseMinor:      baseMinor,
		FeeMinor:       feeMinor,
		TotalMinor:     baseMinor + feeMinor,
		AnchorAsset:    "USDC",
		Status:         domain.InvoiceDraft,
		CreatedAt:      now.UTC(),
		UpdatedAt:      now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO invoices(
			id, membership_id, plan_id, user_id, cycle_start, cycle_end, due_at, base_minor, fee_minor,
			total_minor, credit_applied_minor, paid_minor, anchor_asset, pay_asset, network, quoted_pay_amount,
			quote_rate_label, quote_expires_at, payment_ref, provider_invoice_id, status, tx_hash, reminder_mask,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, '', '', '', '', NULL, '', '', ?, '', 0, ?, ?)
	`, invoice.ID, invoice.MembershipID, invoice.PlanID, invoice.UserID, invoice.CycleStart.Format(time.RFC3339), invoice.CycleEnd.Format(time.RFC3339), invoice.DueAt.Format(time.RFC3339), invoice.BaseMinor, invoice.FeeMinor, invoice.TotalMinor, invoice.AnchorAsset, invoice.Status, invoice.CreatedAt.Format(time.RFC3339), invoice.UpdatedAt.Format(time.RFC3339)); err != nil {
		return domain.Invoice{}, fmt.Errorf("insert invoice: %w", err)
	}
	applied, err := a.applyAvailableCreditsTx(ctx, tx, invoice.UserID, invoice.PlanID, invoice.ID, invoice.TotalMinor)
	if err != nil {
		return domain.Invoice{}, err
	}
	invoice.CreditAppliedMinor = applied
	invoice.Status = domain.InvoiceOpen
	if invoice.AmountDueMinor() == 0 {
		invoice.Status = domain.InvoiceConfirmed
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invoices SET credit_applied_minor = ?, status = ?, updated_at = ? WHERE id = ?`, applied, invoice.Status, now.UTC().Format(time.RFC3339), invoice.ID); err != nil {
		return domain.Invoice{}, err
	}
	if err := a.appendEventTx(ctx, tx, "invoice", invoice.ID, "invoice_created", map[string]any{
		"plan_id":      invoice.PlanID,
		"cycle_start":  invoice.CycleStart.Format("2006-01-02"),
		"cycle_end":    invoice.CycleEnd.Format("2006-01-02"),
		"base_minor":   invoice.BaseMinor,
		"fee_minor":    invoice.FeeMinor,
		"credit_minor": applied,
	}); err != nil {
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (a *App) applyAvailableCreditsTx(ctx context.Context, tx *sql.Tx, userID int64, planID string, invoiceID string, totalMinor int64) (int64, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, remaining_minor
		FROM invoice_credits
		WHERE user_id = ? AND plan_id = ? AND remaining_minor > 0
		ORDER BY created_at ASC
	`, userID, planID)
	if err != nil {
		return 0, err
	}
	type creditRow struct {
		id        string
		remaining int64
	}
	credits := make([]creditRow, 0)
	var applied int64
	for rows.Next() {
		var item creditRow
		if err := rows.Scan(&item.id, &item.remaining); err != nil {
			return 0, err
		}
		credits = append(credits, item)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, credit := range credits {
		if applied >= totalMinor {
			break
		}
		use := minInt64(credit.remaining, totalMinor-applied)
		status := domain.CreditAvailable
		if credit.remaining-use == 0 {
			status = domain.CreditApplied
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE invoice_credits
			SET remaining_minor = ?, status = ?, applied_invoice_id = CASE WHEN ? > 0 THEN ? ELSE applied_invoice_id END
			WHERE id = ?
		`, credit.remaining-use, status, use, invoiceID, credit.id); err != nil {
			return 0, err
		}
		applied += use
	}
	return applied, nil
}

func (a *App) createCreditTx(ctx context.Context, tx *sql.Tx, userID int64, planID string, invoiceID string, amountMinor int64, note string, now time.Time) error {
	if amountMinor <= 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO invoice_credits(id, user_id, plan_id, invoice_id, amount_minor, remaining_minor, status, note, created_at, applied_invoice_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '')
	`, newID("credit"), userID, planID, invoiceID, amountMinor, amountMinor, domain.CreditAvailable, note, now.Format(time.RFC3339)); err != nil {
		return err
	}
	return nil
}

func (a *App) listPendingInvoices(ctx context.Context) ([]domain.Invoice, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.provider_invoice_id != ''
		  AND i.status IN ('open', 'detected', 'underpaid')
		ORDER BY i.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Invoice, 0)
	for rows.Next() {
		invoice, err := scanInvoiceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, invoice)
	}
	return out, rows.Err()
}

func (a *App) recordProviderPaymentsTx(ctx context.Context, tx *sql.Tx, invoice domain.Invoice, providerPayments []payments.NormalizedPayment) (int64, string, error) {
	lastTx := invoice.TxHash
	for _, normalized := range providerPayments {
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM payments WHERE external_payment_id = ?`, normalized.ExternalPaymentID).Scan(&exists); err != nil {
			return 0, "", err
		}
		if exists == 0 {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO payments(id, invoice_id, external_payment_id, amount_received, asset, network, tx_hash, confirmations, received_at, settlement_status)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, newID("payment"), invoice.ID, normalized.ExternalPaymentID, normalized.AmountAnchorMinor, normalized.Asset, normalized.Network, normalized.TxHash, normalized.Confirmations, normalized.ReceivedAt.Format(time.RFC3339), normalized.SettlementStatus); err != nil {
				return 0, "", err
			}
		}
		lastTx = normalized.TxHash
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT amount_received, confirmations, settlement_status
		FROM payments
		WHERE invoice_id = ?
	`, invoice.ID)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	var totalPaid int64
	for rows.Next() {
		var amount int64
		var confirmations int
		var settlementStatus string
		if err := rows.Scan(&amount, &confirmations, &settlementStatus); err != nil {
			return 0, "", err
		}
		if paymentCountsTowardSettlement(confirmations, settlementStatus, a.cfg.RequiredConfirmations) {
			totalPaid += amount
		}
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return totalPaid, lastTx, nil
}

func (a *App) applyProviderInvoiceSnapshotTx(ctx context.Context, tx *sql.Tx, invoice domain.Invoice, providerStatus string, providerPayments []payments.NormalizedPayment, now time.Time) ([]domain.Notification, error) {
	totalPaid, lastTxHash, err := a.recordProviderPaymentsTx(ctx, tx, invoice, providerPayments)
	if err != nil {
		return nil, err
	}

	notifications := make([]domain.Notification, 0, 1)
	dueMinor := invoice.AmountDueMinor()
	normalizedStatus := normalizeProviderInvoiceStatus(providerStatus)

	switch {
	case totalPaid == 0 && normalizedStatus == domain.InvoiceExpired && invoice.Status != domain.InvoiceExpired:
		if err := a.setInvoiceStatusTx(ctx, tx, invoice.ID, domain.InvoiceExpired, 0, invoice.TxHash, now); err != nil {
			return nil, err
		}
		if err := a.appendEventTx(ctx, tx, "invoice", invoice.ID, "invoice_expired", map[string]any{"provider_status": normalizedStatus}); err != nil {
			return nil, err
		}
	case totalPaid > 0 && totalPaid < dueMinor && (invoice.Status != domain.InvoiceUnderpaid || invoice.PaidMinor != totalPaid || invoice.TxHash != lastTxHash):
		if err := a.setInvoiceStatusTx(ctx, tx, invoice.ID, domain.InvoiceUnderpaid, totalPaid, lastTxHash, now); err != nil {
			return nil, err
		}
		if err := a.appendEventTx(ctx, tx, "invoice", invoice.ID, "invoice_underpaid", map[string]any{"paid_minor": totalPaid, "due_minor": dueMinor}); err != nil {
			return nil, err
		}
		if invoice.UserTelegramID > 0 {
			notifications = append(notifications, domain.Notification{
				TelegramID: invoice.UserTelegramID,
				Message:    fmt.Sprintf("Invoice %s is still short. Paid so far: %.2f USDC, due: %.2f USDC.", invoice.ID, centsToUnits(totalPaid), centsToUnits(dueMinor)),
			})
		}
	case totalPaid >= dueMinor && dueMinor >= 0 && (invoice.Status != domain.InvoiceConfirmed || invoice.PaidMinor != totalPaid || invoice.TxHash != lastTxHash):
		if err := a.setInvoiceStatusTx(ctx, tx, invoice.ID, domain.InvoiceConfirmed, totalPaid, lastTxHash, now); err != nil {
			return nil, err
		}
		if err := a.activateMembershipForInvoiceTx(ctx, tx, invoice, now); err != nil {
			return nil, err
		}
		if totalPaid > dueMinor {
			if err := a.createCreditTx(ctx, tx, invoice.UserID, invoice.PlanID, invoice.ID, totalPaid-dueMinor, "overpayment", now); err != nil {
				return nil, err
			}
		}
		if err := a.appendEventTx(ctx, tx, "invoice", invoice.ID, "payment_confirmed", map[string]any{"paid_minor": totalPaid}); err != nil {
			return nil, err
		}
		if invoice.UserTelegramID > 0 {
			notifications = append(notifications, domain.Notification{
				TelegramID: invoice.UserTelegramID,
				Message:    fmt.Sprintf("Payment confirmed for invoice %s. Your seat is active.", invoice.ID),
			})
		}
	}

	return notifications, nil
}

func (a *App) setInvoiceStatusTx(ctx context.Context, tx *sql.Tx, invoiceID string, status string, paidMinor int64, txHash string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE invoices SET status = ?, paid_minor = ?, tx_hash = ?, updated_at = ?
		WHERE id = ?
	`, status, paidMinor, txHash, now.Format(time.RFC3339), invoiceID)
	return err
}

func (a *App) activateMembershipForInvoiceTx(ctx context.Context, tx *sql.Tx, invoice domain.Invoice, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE memberships
		SET seat_status = ?, grace_until = NULL, latest_invoice_id = ?
		WHERE id = ?
	`, domain.MembershipActive, invoice.ID, invoice.MembershipID)
	if err != nil {
		return err
	}
	return a.appendEventTx(ctx, tx, "membership", invoice.MembershipID, "seat_activated", map[string]any{"invoice_id": invoice.ID, "at": now.Format(time.RFC3339)})
}

func normalizeProviderInvoiceStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case domain.InvoiceConfirmed, "finished", "sending":
		return domain.InvoiceConfirmed
	case domain.InvoiceDetected, "confirming", "partially_paid":
		return domain.InvoiceDetected
	case domain.InvoiceExpired, domain.InvoiceCancelled, "failed", "refunded":
		return domain.InvoiceExpired
	default:
		return domain.InvoiceOpen
	}
}

func mapProviderEventStatus(providerName string, eventType string) string {
	switch strings.ToLower(strings.TrimSpace(providerName)) {
	case "nowpayments":
		return normalizeProviderInvoiceStatus(eventType)
	default:
		return strings.ToLower(strings.TrimSpace(eventType))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (a *App) generateRenewalInvoicesTx(ctx context.Context, tx *sql.Tx, now time.Time, notifications *[]domain.Notification) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id, m.plan_id, m.user_id, u.telegram_id, u.username, m.seat_status, m.joined_at, m.grace_until, m.removed_at, m.latest_invoice_id,
		       p.id, p.owner_user_id, owner.telegram_id, p.service_code, sc.display_name, sc.category,
		       p.total_price_minor, p.per_seat_base_minor, p.platform_fee_bps, p.stable_asset, p.billing_period,
		       p.renewal_date, p.seat_limit, p.access_mode, p.sharing_policy_note, p.status, p.created_at
		FROM memberships m
		INNER JOIN users u ON u.id = m.user_id
		INNER JOIN plans p ON p.id = m.plan_id
		INNER JOIN users owner ON owner.id = p.owner_user_id
		INNER JOIN service_catalog sc ON sc.service_code = p.service_code
		WHERE m.seat_status IN ('active', 'grace')
		  AND m.removed_at IS NULL
	`)
	if err != nil {
		return err
	}
	type renewalTarget struct {
		member domain.Membership
		plan   domain.Plan
	}
	targets := make([]renewalTarget, 0)
	for rows.Next() {
		member, plan, err := scanMembershipPlanRows(rows)
		if err != nil {
			return err
		}
		targets = append(targets, renewalTarget{member: member, plan: plan})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, target := range targets {
		member := target.member
		plan := target.plan
		_, currentEnd := cycleWindow(plan.RenewalDate, now, a.loc)
		if now.Before(currentEnd.AddDate(0, 0, -a.cfg.RenewalLeadDays)) {
			continue
		}
		nextStart := currentEnd
		nextEnd := addMonthsClamped(nextStart, 1)
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM invoices
			WHERE membership_id = ? AND cycle_start = ? AND cycle_end = ?
		`, member.ID, nextStart.UTC().Format(time.RFC3339), nextEnd.UTC().Format(time.RFC3339)).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		invoice, err := a.createInvoiceTx(ctx, tx, member, plan, nextStart, nextEnd, nextStart, plan.PerSeatBaseMinor, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE memberships SET latest_invoice_id = ? WHERE id = ?`, invoice.ID, member.ID); err != nil {
			return err
		}
		*notifications = append(*notifications, domain.Notification{
			TelegramID: member.UserTelegramID,
			Message:    fmt.Sprintf("Renewal invoice %s is ready for %s. Amount due: %.2f USDC.", invoice.ID, plan.ServiceName, centsToUnits(invoice.AmountDueMinor())),
		})
	}
	return nil
}

func (a *App) sendDueRemindersTx(ctx context.Context, tx *sql.Tx, now time.Time, notifications *[]domain.Notification) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.status IN ('open', 'underpaid')
		  AND i.due_at = i.cycle_start
	`)
	if err != nil {
		return err
	}
	invoices := make([]domain.Invoice, 0)
	for rows.Next() {
		invoice, err := scanInvoiceRows(rows)
		if err != nil {
			return err
		}
		invoices = append(invoices, invoice)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, invoice := range invoices {
		daysUntil := int(math.Round(invoice.DueAt.Sub(now).Hours() / 24))
		for idx, reminderDay := range a.cfg.ReminderDays {
			mask := 1 << idx
			if reminderDay != daysUntil || invoice.ReminderMask&mask != 0 {
				continue
			}
			invoice.ReminderMask |= mask
			if _, err := tx.ExecContext(ctx, `UPDATE invoices SET reminder_mask = ?, updated_at = ? WHERE id = ?`, invoice.ReminderMask, now.UTC().Format(time.RFC3339), invoice.ID); err != nil {
				return err
			}
			*notifications = append(*notifications, domain.Notification{
				TelegramID: invoice.UserTelegramID,
				Message:    fmt.Sprintf("Renewal for invoice %s is due in %d day(s).", invoice.ID, reminderDay),
			})
			break
		}
	}
	return nil
}

func (a *App) updateGraceAndSuspensionTx(ctx context.Context, tx *sql.Tx, now time.Time, notifications *[]domain.Notification) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT m.id, m.plan_id, m.user_id, u.telegram_id, u.username, m.seat_status, m.joined_at, m.grace_until, m.removed_at, m.latest_invoice_id,
		       i.id, i.due_at, i.status
		FROM memberships m
		INNER JOIN users u ON u.id = m.user_id
		LEFT JOIN invoices i ON i.id = m.latest_invoice_id
		WHERE m.seat_status IN ('active', 'grace')
		  AND m.removed_at IS NULL
	`)
	if err != nil {
		return err
	}
	type graceRow struct {
		membership    domain.Membership
		invoiceID     sql.NullString
		dueAt         sql.NullString
		invoiceStatus sql.NullString
	}
	items := make([]graceRow, 0)
	for rows.Next() {
		var item graceRow
		var joinedAt string
		var graceUntil sql.NullString
		var removedAt sql.NullString
		if err := rows.Scan(&item.membership.ID, &item.membership.PlanID, &item.membership.UserID, &item.membership.UserTelegramID, &item.membership.Username, &item.membership.SeatStatus, &joinedAt, &graceUntil, &removedAt, &item.membership.LatestInvoiceID, &item.invoiceID, &item.dueAt, &item.invoiceStatus); err != nil {
			return err
		}
		item.membership.JoinedAt, _ = time.Parse(time.RFC3339, joinedAt)
		if graceUntil.Valid {
			parsed, _ := time.Parse(time.RFC3339, graceUntil.String)
			item.membership.GraceUntil = &parsed
		}
		if removedAt.Valid {
			parsed, _ := time.Parse(time.RFC3339, removedAt.String)
			item.membership.RemovedAt = &parsed
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		membership := item.membership
		if !item.invoiceID.Valid || !item.dueAt.Valid || !item.invoiceStatus.Valid {
			continue
		}
		if item.invoiceStatus.String == domain.InvoiceConfirmed || item.invoiceStatus.String == domain.InvoiceCancelled {
			continue
		}
		dueTime, _ := time.Parse(time.RFC3339, item.dueAt.String)
		if now.Before(dueTime) {
			continue
		}
		switch membership.SeatStatus {
		case domain.MembershipActive:
			grace := dueTime.AddDate(0, 0, a.cfg.GraceDays)
			if _, err := tx.ExecContext(ctx, `UPDATE memberships SET seat_status = ?, grace_until = ? WHERE id = ?`, domain.MembershipGrace, grace.Format(time.RFC3339), membership.ID); err != nil {
				return err
			}
			if err := a.appendEventTx(ctx, tx, "membership", membership.ID, "grace_started", map[string]any{"invoice_id": item.invoiceID.String, "grace_until": grace.Format(time.RFC3339)}); err != nil {
				return err
			}
			*notifications = append(*notifications, domain.Notification{
				TelegramID: membership.UserTelegramID,
				Message:    fmt.Sprintf("Your seat is in grace period until %s because renewal invoice %s is still unpaid.", grace.In(a.loc).Format("2006-01-02"), item.invoiceID.String),
			})
		case domain.MembershipGrace:
			if membership.GraceUntil != nil {
				graceTime := *membership.GraceUntil
				if now.After(graceTime) {
					if _, err := tx.ExecContext(ctx, `UPDATE memberships SET seat_status = ? WHERE id = ?`, domain.MembershipSuspended, membership.ID); err != nil {
						return err
					}
					if err := a.appendEventTx(ctx, tx, "membership", membership.ID, "seat_suspended", map[string]any{"invoice_id": item.invoiceID.String}); err != nil {
						return err
					}
					*notifications = append(*notifications, domain.Notification{
						TelegramID: membership.UserTelegramID,
						Message:    fmt.Sprintf("Your seat has been suspended because invoice %s is still unpaid.", item.invoiceID.String),
					})
				}
			}
		}
	}
	return nil
}

func (a *App) appendEventTx(ctx context.Context, tx *sql.Tx, entityType string, entityID string, eventName string, payload any) error {
	raw := "{}"
	if payload != nil {
		bytes, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = string(bytes)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO events(entity_type, entity_id, event_name, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, entityType, entityID, eventName, raw, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (a *App) findInvoiceByID(ctx context.Context, invoiceID string) (domain.Invoice, error) {
	row := a.db.QueryRowContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.id = ?
	`, invoiceID)
	return scanInvoice(row)
}

func (a *App) findInvoiceByProviderInvoiceID(ctx context.Context, providerInvoiceID string) (domain.Invoice, error) {
	row := a.db.QueryRowContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.provider_invoice_id = ?
		ORDER BY i.created_at DESC
		LIMIT 1
	`, strings.TrimSpace(providerInvoiceID))
	return scanInvoice(row)
}

func (a *App) lookupInvoiceTx(ctx context.Context, tx *sql.Tx, invoiceID string) (domain.Invoice, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.id = ?
	`, invoiceID)
	return scanInvoice(row)
}

func (a *App) listInvoicesForPlan(ctx context.Context, planID string) ([]domain.Invoice, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT i.id, i.membership_id, i.plan_id, i.user_id, u.telegram_id, i.cycle_start, i.cycle_end, i.due_at,
		       i.base_minor, i.fee_minor, i.total_minor, i.credit_applied_minor, i.paid_minor, i.anchor_asset,
		       i.pay_asset, i.network, i.quoted_pay_amount, i.quote_rate_label, i.quote_expires_at,
		       i.payment_ref, i.provider_invoice_id, i.status, i.tx_hash, i.reminder_mask, i.created_at, i.updated_at
		FROM invoices i
		INNER JOIN users u ON u.id = i.user_id
		WHERE i.plan_id = ?
		ORDER BY i.created_at DESC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Invoice, 0)
	for rows.Next() {
		invoice, err := scanInvoiceRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, invoice)
	}
	return out, rows.Err()
}

func (a *App) listPaymentsForPlan(ctx context.Context, planID string) ([]domain.Payment, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.id, p.invoice_id, p.amount_received, p.asset, p.network, p.tx_hash, p.confirmations, p.received_at, p.settlement_status
		FROM payments p
		INNER JOIN invoices i ON i.id = p.invoice_id
		WHERE i.plan_id = ?
		ORDER BY p.received_at DESC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Payment, 0)
	for rows.Next() {
		var payment domain.Payment
		var receivedAt string
		if err := rows.Scan(&payment.ID, &payment.InvoiceID, &payment.AmountReceived, &payment.Asset, &payment.Network, &payment.TxHash, &payment.Confirmations, &receivedAt, &payment.SettlementStatus); err != nil {
			return nil, err
		}
		payment.ReceivedAt, _ = time.Parse(time.RFC3339, receivedAt)
		out = append(out, payment)
	}
	return out, rows.Err()
}

func (a *App) listCreditsForPlan(ctx context.Context, planID string) ([]domain.Credit, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, user_id, plan_id, invoice_id, amount_minor, remaining_minor, status, note, created_at, applied_invoice_id
		FROM invoice_credits
		WHERE plan_id = ?
		ORDER BY created_at DESC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Credit, 0)
	for rows.Next() {
		var credit domain.Credit
		var createdAt string
		if err := rows.Scan(&credit.ID, &credit.UserID, &credit.PlanID, &credit.InvoiceID, &credit.AmountMinor, &credit.RemainingMinor, &credit.Status, &credit.Note, &createdAt, &credit.AppliedInvoiceID); err != nil {
			return nil, err
		}
		credit.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, credit)
	}
	return out, rows.Err()
}

func (a *App) listEvents(ctx context.Context, entityType string, entityID string) ([]domain.Event, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, entity_type, entity_id, event_name, payload_json, created_at
		FROM events
		WHERE entity_type = ? AND entity_id = ?
		ORDER BY id DESC
	`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Event, 0)
	for rows.Next() {
		var event domain.Event
		var createdAt string
		if err := rows.Scan(&event.ID, &event.EntityType, &event.EntityID, &event.EventName, &event.PayloadJSON, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (a *App) listEventsForPlan(ctx context.Context, planID string) ([]domain.Event, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT e.id, e.entity_type, e.entity_id, e.event_name, e.payload_json, e.created_at
		FROM events e
		WHERE (e.entity_type = 'plan' AND e.entity_id = ?)
		   OR (e.entity_type = 'membership' AND e.entity_id IN (SELECT id FROM memberships WHERE plan_id = ?))
		   OR (e.entity_type = 'invoice' AND e.entity_id IN (SELECT id FROM invoices WHERE plan_id = ?))
		   OR (e.entity_type = 'support_ticket' AND e.entity_id IN (SELECT id FROM support_tickets WHERE plan_id = ?))
		ORDER BY e.id DESC
	`, planID, planID, planID, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Event, 0)
	for rows.Next() {
		var event domain.Event
		var createdAt string
		if err := rows.Scan(&event.ID, &event.EntityType, &event.EntityID, &event.EventName, &event.PayloadJSON, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, event)
	}
	return out, rows.Err()
}

func scanUser(row interface{ Scan(dest ...any) error }) (domain.User, error) {
	var user domain.User
	var createdAt string
	if err := row.Scan(&user.ID, &user.TelegramID, &user.Username, &user.Role, &user.Status, &createdAt); err != nil {
		return domain.User{}, err
	}
	user.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return user, nil
}

func scanPlan(row interface{ Scan(dest ...any) error }) (domain.Plan, error) {
	var plan domain.Plan
	var renewalDate, createdAt string
	if err := row.Scan(&plan.ID, &plan.OwnerUserID, &plan.OwnerTelegramID, &plan.ServiceCode, &plan.ServiceName, &plan.Category, &plan.TotalPriceMinor, &plan.PerSeatBaseMinor, &plan.PlatformFeeBps, &plan.StableAsset, &plan.BillingPeriod, &renewalDate, &plan.SeatLimit, &plan.AccessMode, &plan.SharingPolicyNote, &plan.Status, &createdAt); err != nil {
		return domain.Plan{}, err
	}
	plan.RenewalDate, _ = time.Parse(time.RFC3339, renewalDate)
	plan.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return plan, nil
}

func scanMembershipRows(scanner interface{ Scan(dest ...any) error }) (domain.Membership, error) {
	var membership domain.Membership
	var joinedAt string
	var graceUntil, removedAt sql.NullString
	if err := scanner.Scan(&membership.ID, &membership.PlanID, &membership.UserID, &membership.UserTelegramID, &membership.Username, &membership.SeatStatus, &joinedAt, &graceUntil, &removedAt, &membership.LatestInvoiceID); err != nil {
		return domain.Membership{}, err
	}
	membership.JoinedAt, _ = time.Parse(time.RFC3339, joinedAt)
	if graceUntil.Valid {
		parsed, _ := time.Parse(time.RFC3339, graceUntil.String)
		membership.GraceUntil = &parsed
	}
	if removedAt.Valid {
		parsed, _ := time.Parse(time.RFC3339, removedAt.String)
		membership.RemovedAt = &parsed
	}
	return membership, nil
}

func scanMembershipPlanRows(scanner interface{ Scan(dest ...any) error }) (domain.Membership, domain.Plan, error) {
	var membership domain.Membership
	var plan domain.Plan
	var joinedAt, renewalDate, createdAt string
	var graceUntil, removedAt sql.NullString
	if err := scanner.Scan(
		&membership.ID, &membership.PlanID, &membership.UserID, &membership.UserTelegramID, &membership.Username, &membership.SeatStatus, &joinedAt, &graceUntil, &removedAt, &membership.LatestInvoiceID,
		&plan.ID, &plan.OwnerUserID, &plan.OwnerTelegramID, &plan.ServiceCode, &plan.ServiceName, &plan.Category,
		&plan.TotalPriceMinor, &plan.PerSeatBaseMinor, &plan.PlatformFeeBps, &plan.StableAsset, &plan.BillingPeriod,
		&renewalDate, &plan.SeatLimit, &plan.AccessMode, &plan.SharingPolicyNote, &plan.Status, &createdAt,
	); err != nil {
		return domain.Membership{}, domain.Plan{}, err
	}
	membership.JoinedAt, _ = time.Parse(time.RFC3339, joinedAt)
	if graceUntil.Valid {
		parsed, _ := time.Parse(time.RFC3339, graceUntil.String)
		membership.GraceUntil = &parsed
	}
	if removedAt.Valid {
		parsed, _ := time.Parse(time.RFC3339, removedAt.String)
		membership.RemovedAt = &parsed
	}
	plan.RenewalDate, _ = time.Parse(time.RFC3339, renewalDate)
	plan.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return membership, plan, nil
}

func scanInvoice(row interface{ Scan(dest ...any) error }) (domain.Invoice, error) {
	var invoice domain.Invoice
	var cycleStart, cycleEnd, dueAt, createdAt, updatedAt string
	var quoteExpiresAt sql.NullString
	if err := row.Scan(&invoice.ID, &invoice.MembershipID, &invoice.PlanID, &invoice.UserID, &invoice.UserTelegramID, &cycleStart, &cycleEnd, &dueAt, &invoice.BaseMinor, &invoice.FeeMinor, &invoice.TotalMinor, &invoice.CreditAppliedMinor, &invoice.PaidMinor, &invoice.AnchorAsset, &invoice.PayAsset, &invoice.Network, &invoice.QuotedPayAmount, &invoice.QuoteRateLabel, &quoteExpiresAt, &invoice.PaymentRef, &invoice.ProviderInvoiceID, &invoice.Status, &invoice.TxHash, &invoice.ReminderMask, &createdAt, &updatedAt); err != nil {
		return domain.Invoice{}, err
	}
	invoice.CycleStart, _ = time.Parse(time.RFC3339, cycleStart)
	invoice.CycleEnd, _ = time.Parse(time.RFC3339, cycleEnd)
	invoice.DueAt, _ = time.Parse(time.RFC3339, dueAt)
	invoice.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	invoice.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if quoteExpiresAt.Valid {
		parsed, _ := time.Parse(time.RFC3339, quoteExpiresAt.String)
		invoice.QuoteExpiresAt = &parsed
	}
	return invoice, nil
}

func scanInvoiceRows(scanner interface{ Scan(dest ...any) error }) (domain.Invoice, error) {
	return scanInvoice(scanner)
}

func chooseAccessMode(requested string, fallback string) string {
	value := strings.TrimSpace(requested)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func atLocalMidnight(value time.Time, loc *time.Location) time.Time {
	local := value.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

func cycleWindow(anchor time.Time, now time.Time, loc *time.Location) (time.Time, time.Time) {
	end := atLocalMidnight(anchor, loc)
	for !end.After(now) {
		end = addMonthsClamped(end, 1)
	}
	start := addMonthsClamped(end, -1)
	return start, end
}

func addMonthsClamped(base time.Time, months int) time.Time {
	return base.AddDate(0, months, 0)
}

func proratedBaseMinor(baseMinor int64, now time.Time, cycleStart time.Time, cycleEnd time.Time) int64 {
	totalDays := maxInt64(1, int64(math.Ceil(cycleEnd.Sub(cycleStart).Hours()/24)))
	remainingDays := maxInt64(1, int64(math.Ceil(cycleEnd.Sub(now).Hours()/24)))
	if remainingDays > totalDays {
		remainingDays = totalDays
	}
	return divideRoundHalfUp(baseMinor*remainingDays, totalDays)
}

func divideRoundHalfUp(numerator int64, denominator int64) int64 {
	if denominator <= 0 {
		return numerator
	}
	quotient := numerator / denominator
	remainder := numerator % denominator
	if remainder*2 >= denominator {
		return quotient + 1
	}
	return quotient
}

func feeAmount(baseMinor int64, feeBps int) int64 {
	return divideRoundHalfUp(baseMinor*int64(feeBps), 10_000)
}

func centsToUnits(cents int64) float64 {
	return float64(cents) / 100
}

func paymentCountsTowardSettlement(confirmations int, settlementStatus string, requiredConfirmations int) bool {
	status := strings.ToLower(strings.TrimSpace(settlementStatus))
	switch status {
	case "failed", "expired", "refunded", "cancelled":
		return false
	}
	if requiredConfirmations > 0 && confirmations < requiredConfirmations {
		return false
	}
	if status == "" && confirmations >= requiredConfirmations {
		return true
	}
	switch status {
	case "confirmed", "finished", "completed", "paid", "sending":
		return true
	default:
		return confirmations >= requiredConfirmations && requiredConfirmations > 0
	}
}

func newID(prefix string) string {
	return prefix + "-" + randomCode(6)
}

func randomCode(byteCount int) string {
	buf := make([]byte, byteCount)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func minInt64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(v int, other int) int {
	if v > other {
		return v
	}
	return other
}

func maxInt64(v int64, other int64) int64 {
	if v > other {
		return v
	}
	return other
}
