package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"subscriptionbot/internal/domain"
	"subscriptionbot/internal/payments"
)

const botConversationTTL = 30 * time.Minute

func (a *App) SaveConversationState(ctx context.Context, actor Actor, flow string, step string, payload any, now time.Time) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	user, err := a.ensureUserTx(ctx, tx, actor)
	if err != nil {
		return err
	}
	raw := "{}"
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = string(data)
	}
	expiresAt := now.UTC().Add(botConversationTTL)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bot_conversation_states(user_id, telegram_id, flow, step, payload_json, expires_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			telegram_id = excluded.telegram_id,
			flow = excluded.flow,
			step = excluded.step,
			payload_json = excluded.payload_json,
			expires_at = excluded.expires_at,
			updated_at = excluded.updated_at
	`, user.ID, user.TelegramID, strings.TrimSpace(flow), strings.TrimSpace(step), raw, expiresAt.Format(time.RFC3339), now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *App) LoadConversationState(ctx context.Context, actor Actor, now time.Time) (*domain.BotConversationState, error) {
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return nil, err
	}
	row := a.db.QueryRowContext(ctx, `
		SELECT user_id, telegram_id, flow, step, payload_json, expires_at, updated_at
		FROM bot_conversation_states
		WHERE user_id = ?
	`, user.ID)
	var state domain.BotConversationState
	var expiresAt, updatedAt string
	if err := row.Scan(&state.UserID, &state.TelegramID, &state.Flow, &state.Step, &state.PayloadJSON, &expiresAt, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	state.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	state.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if !state.ExpiresAt.After(now.UTC()) {
		if err := a.ClearConversationState(ctx, actor); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return &state, nil
}

func (a *App) ClearConversationState(ctx context.Context, actor Actor) error {
	if actor.TelegramID <= 0 {
		return nil
	}
	_, err := a.db.ExecContext(ctx, `DELETE FROM bot_conversation_states WHERE telegram_id = ?`, actor.TelegramID)
	return err
}

func (a *App) ListOpenSupportTickets(ctx context.Context, actor Actor) ([]domain.SupportTicketView, error) {
	if !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT t.id, t.plan_id, t.user_id, t.subject, t.body, t.status, t.created_at, t.updated_at,
		       COALESCE(sc.display_name, p.service_code, ''), u.username, u.telegram_id,
		       COALESCE(i.id, ''), COALESCE(i.total_minor - i.credit_applied_minor, 0), COALESCE(i.paid_minor, 0), COALESCE(i.status, '')
		FROM support_tickets t
		LEFT JOIN plans p ON p.id = t.plan_id
		LEFT JOIN service_catalog sc ON sc.service_code = p.service_code
		INNER JOIN users u ON u.id = t.user_id
		LEFT JOIN memberships m ON m.plan_id = t.plan_id AND m.user_id = t.user_id
		LEFT JOIN invoices i ON i.id = m.latest_invoice_id
		WHERE t.status = 'open'
		ORDER BY t.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.SupportTicketView, 0)
	for rows.Next() {
		var view domain.SupportTicketView
		var createdAt, updatedAt string
		if err := rows.Scan(
			&view.Ticket.ID,
			&view.Ticket.PlanID,
			&view.Ticket.UserID,
			&view.Ticket.Subject,
			&view.Ticket.Body,
			&view.Ticket.Status,
			&createdAt,
			&updatedAt,
			&view.PlanServiceName,
			&view.Username,
			&view.UserTelegramID,
			&view.LatestInvoiceID,
			&view.LatestDueMinor,
			&view.LatestPaidMinor,
			&view.LatestStatus,
		); err != nil {
			return nil, err
		}
		view.Ticket.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		view.Ticket.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		out = append(out, view)
	}
	return out, rows.Err()
}

func (a *App) ResolveSupportTicket(ctx context.Context, actor Actor, ticketID string, note string, now time.Time) (domain.SupportTicket, error) {
	if !a.IsOperator(actor.TelegramID) {
		return domain.SupportTicket{}, ErrUnauthorized
	}
	ticketID = strings.TrimSpace(ticketID)
	if ticketID == "" {
		return domain.SupportTicket{}, fmt.Errorf("support ticket id is required")
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.SupportTicket{}, err
	}
	defer tx.Rollback()

	var ticket domain.SupportTicket
	var createdAt, updatedAt string
	if err := tx.QueryRowContext(ctx, `
		SELECT id, plan_id, user_id, subject, body, status, created_at, updated_at
		FROM support_tickets
		WHERE id = ?
		LIMIT 1
	`, ticketID).Scan(
		&ticket.ID,
		&ticket.PlanID,
		&ticket.UserID,
		&ticket.Subject,
		&ticket.Body,
		&ticket.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return domain.SupportTicket{}, ErrNotFound
		}
		return domain.SupportTicket{}, err
	}
	ticket.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	ticket.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if ticket.Status == domain.TicketResolved {
		return ticket, nil
	}

	ticket.Status = domain.TicketResolved
	ticket.UpdatedAt = now.UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE support_tickets
		SET status = ?, updated_at = ?
		WHERE id = ?
	`, ticket.Status, ticket.UpdatedAt.Format(time.RFC3339), ticket.ID); err != nil {
		return domain.SupportTicket{}, err
	}
	if err := a.appendEventTx(ctx, tx, "support_ticket", ticket.ID, "support_resolved", map[string]any{
		"note": strings.TrimSpace(note),
	}); err != nil {
		return domain.SupportTicket{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.SupportTicket{}, err
	}
	return ticket, nil
}

func (a *App) AddDenylistEntry(ctx context.Context, actor Actor, entryType string, entryValue string, reason string, now time.Time) (domain.DenylistEntry, error) {
	if !a.IsOperator(actor.TelegramID) {
		return domain.DenylistEntry{}, ErrUnauthorized
	}
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return domain.DenylistEntry{}, err
	}
	entryType = strings.ToLower(strings.TrimSpace(entryType))
	entryValue = strings.TrimSpace(entryValue)
	reason = strings.TrimSpace(reason)
	if entryType == "" || entryValue == "" {
		return domain.DenylistEntry{}, fmt.Errorf("denylist type and value are required")
	}
	entry := domain.DenylistEntry{
		EntryType:       entryType,
		EntryValue:      entryValue,
		Reason:          reason,
		CreatedByUserID: user.ID,
		CreatedAt:       now.UTC(),
	}
	result, err := a.db.ExecContext(ctx, `
		INSERT INTO denylist_entries(entry_type, entry_value, reason, created_by_user_id, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(entry_type, entry_value) DO UPDATE SET
			reason = excluded.reason,
			created_by_user_id = excluded.created_by_user_id
	`, entry.EntryType, entry.EntryValue, entry.Reason, entry.CreatedByUserID, entry.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return domain.DenylistEntry{}, err
	}
	entry.ID, _ = result.LastInsertId()
	if err := a.appendEvent(ctx, "denylist_entry", fmt.Sprintf("%s:%s", entry.EntryType, entry.EntryValue), "denylist_upserted", map[string]any{
		"reason": reason,
	}); err != nil {
		return domain.DenylistEntry{}, err
	}
	return entry, nil
}

func (a *App) ListDenylistEntries(ctx context.Context, actor Actor, limit int) ([]domain.DenylistEntry, error) {
	if !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT id, entry_type, entry_value, reason, COALESCE(created_by_user_id, 0), created_at
		FROM denylist_entries
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.DenylistEntry, 0, limit)
	for rows.Next() {
		var item domain.DenylistEntry
		var createdAt string
		if err := rows.Scan(&item.ID, &item.EntryType, &item.EntryValue, &item.Reason, &item.CreatedByUserID, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (a *App) ListOwnerReimbursementsDue(ctx context.Context, actor Actor, limit int) ([]domain.OwnerReimbursementSummary, error) {
	if !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.owner_user_id, owner.telegram_id, owner.username, COALESCE(SUM(i.base_minor), 0)
		FROM invoices i
		INNER JOIN plans p ON p.id = i.plan_id
		INNER JOIN users owner ON owner.id = p.owner_user_id
		WHERE i.status = 'confirmed'
		GROUP BY p.owner_user_id, owner.telegram_id, owner.username
		HAVING COALESCE(SUM(i.base_minor), 0) > 0
		ORDER BY COALESCE(SUM(i.base_minor), 0) DESC, owner.telegram_id ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.OwnerReimbursementSummary, 0, limit)
	for rows.Next() {
		var item domain.OwnerReimbursementSummary
		if err := rows.Scan(&item.OwnerUserID, &item.OwnerTelegramID, &item.Username, &item.AmountMinor); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (a *App) ListRecentPaymentAlerts(ctx context.Context, actor Actor, limit int) ([]domain.PaymentAlert, error) {
	if !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT event_name, entity_id, payload_json, created_at
		FROM events
		WHERE event_name IN ('provider_event_unmatched', 'provider_payment_denied', 'provider_event_failed')
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.PaymentAlert, 0, limit)
	for rows.Next() {
		var item domain.PaymentAlert
		var payloadJSON string
		var createdAt string
		if err := rows.Scan(&item.EventName, &item.EntityID, &payloadJSON, &createdAt); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		item.Detail = payloadJSON
		var payload map[string]any
		if json.Unmarshal([]byte(payloadJSON), &payload) == nil {
			if value, ok := payload["provider_name"].(string); ok {
				item.ProviderName = value
			}
			if value, ok := payload["provider_invoice_id"].(string); ok {
				item.ProviderInvoice = value
			}
			if value, ok := payload["detail"].(string); ok && strings.TrimSpace(value) != "" {
				item.Detail = value
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (a *App) ListRenewalIssues(ctx context.Context, actor Actor) ([]domain.RenewalIssue, error) {
	if !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.id,
		       COALESCE(sc.display_name, p.service_code, ''),
		       m.id,
		       m.user_id,
		       u.telegram_id,
		       u.username,
		       m.seat_status,
		       COALESCE(i.id, ''),
		       COALESCE(i.status, ''),
		       i.due_at,
		       COALESCE(i.total_minor - i.credit_applied_minor, 0),
		       COALESCE(i.paid_minor, 0)
		FROM memberships m
		INNER JOIN plans p ON p.id = m.plan_id
		LEFT JOIN service_catalog sc ON sc.service_code = p.service_code
		INNER JOIN users u ON u.id = m.user_id
		LEFT JOIN invoices i ON i.id = m.latest_invoice_id
		WHERE (m.seat_status IN ('grace', 'suspended'))
		   OR (i.status = 'underpaid')
		ORDER BY p.created_at DESC, m.joined_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RenewalIssue, 0)
	for rows.Next() {
		var item domain.RenewalIssue
		var dueAt sql.NullString
		if err := rows.Scan(
			&item.PlanID,
			&item.PlanServiceName,
			&item.MembershipID,
			&item.UserID,
			&item.UserTelegramID,
			&item.Username,
			&item.SeatStatus,
			&item.InvoiceID,
			&item.InvoiceStatus,
			&dueAt,
			&item.AmountDueMinor,
			&item.PaidMinor,
		); err != nil {
			return nil, err
		}
		if dueAt.Valid {
			parsed, _ := time.Parse(time.RFC3339, dueAt.String)
			item.DueAt = &parsed
		}
		switch {
		case item.SeatStatus == domain.MembershipGrace:
			item.Kind = "grace"
		case item.SeatStatus == domain.MembershipSuspended:
			item.Kind = "suspended"
		case item.InvoiceStatus == domain.InvoiceUnderpaid:
			item.Kind = "underpaid"
		default:
			item.Kind = "renewal_issue"
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (a *App) ListRecentPlans(ctx context.Context, actor Actor, limit int) ([]domain.Plan, error) {
	if !a.IsOperator(actor.TelegramID) {
		return nil, ErrUnauthorized
	}
	if limit <= 0 {
		limit = 5
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT p.id, p.owner_user_id, owner.telegram_id, p.service_code, sc.display_name, sc.category,
		       p.total_price_minor, p.per_seat_base_minor, p.platform_fee_bps, p.stable_asset, p.billing_period,
		       p.renewal_date, p.seat_limit, p.access_mode, p.sharing_policy_note, p.status, p.created_at
		FROM plans p
		INNER JOIN users owner ON owner.id = p.owner_user_id
		INNER JOIN service_catalog sc ON sc.service_code = p.service_code
		ORDER BY p.created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.Plan, 0)
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, rows.Err()
}

func (a *App) LatestInvoiceForPlan(ctx context.Context, actor Actor, planID string) (*domain.Invoice, error) {
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
		WHERE i.plan_id = ?
		  AND i.user_id = ?
		ORDER BY i.created_at DESC
		LIMIT 1
	`, strings.TrimSpace(planID), user.ID)
	invoice, err := scanInvoice(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &invoice, nil
}

func (a *App) LoadInvoiceByID(ctx context.Context, actor Actor, invoiceID string) (domain.Invoice, error) {
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
	return invoice, nil
}

func (a *App) CanManageRenewals(ctx context.Context, actor Actor) (bool, error) {
	if a.IsOperator(actor.TelegramID) {
		return true, nil
	}
	user, err := a.ensureUser(ctx, actor)
	if err != nil {
		return false, err
	}
	var count int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans WHERE owner_user_id = ?`, user.ID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (a *App) RecordProviderEvent(ctx context.Context, providerName string, externalEventID string, eventType string, providerInvoiceID string, payload any, now time.Time) (bool, error) {
	raw := "{}"
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return false, err
		}
		raw = string(data)
	}
	result, err := a.db.ExecContext(ctx, `
		INSERT INTO provider_events(provider_name, external_event_id, event_type, provider_invoice_id, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_name, external_event_id) DO NOTHING
	`, strings.ToLower(strings.TrimSpace(providerName)), strings.TrimSpace(externalEventID), strings.TrimSpace(eventType), strings.TrimSpace(providerInvoiceID), raw, now.UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

func (a *App) IsTelegramIDDenied(ctx context.Context, telegramID int64) (bool, string, error) {
	if telegramID <= 0 {
		return false, "", nil
	}
	return a.lookupDenylistEntry(ctx, "telegram_id", strconv.FormatInt(telegramID, 10))
}

func (a *App) IsPaymentReferenceDenied(ctx context.Context, ref string) (bool, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return false, "", nil
	}
	return a.lookupDenylistEntry(ctx, "payment_ref", ref)
}

func (a *App) IsTxHashDenied(ctx context.Context, txHash string) (bool, string, error) {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return false, "", nil
	}
	return a.lookupDenylistEntry(ctx, "tx_hash", txHash)
}

func (a *App) lookupDenylistEntry(ctx context.Context, entryType string, entryValue string) (bool, string, error) {
	var reason string
	err := a.db.QueryRowContext(ctx, `
		SELECT reason
		FROM denylist_entries
		WHERE entry_type = ?
		  AND entry_value = ?
		LIMIT 1
	`, strings.ToLower(strings.TrimSpace(entryType)), strings.TrimSpace(entryValue)).Scan(&reason)
	if err == sql.ErrNoRows {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, reason, nil
}

func (a *App) appendEvent(ctx context.Context, entityType string, entityID string, eventName string, payload any) error {
	raw := "{}"
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = string(data)
	}
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO events(entity_type, entity_id, event_name, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, strings.TrimSpace(entityType), strings.TrimSpace(entityID), strings.TrimSpace(eventName), raw, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (a *App) CanSimulatePayments() bool {
	_, ok := a.provider.(payments.Simulatable)
	return ok
}

func (a *App) AllowedPayAssets() []string {
	out := make([]string, len(a.cfg.AllowedPayAssets))
	copy(out, a.cfg.AllowedPayAssets)
	return out
}

func (a *App) AllowedNetworksForAsset(asset string) []string {
	asset = strings.ToUpper(strings.TrimSpace(asset))
	switch asset {
	case "USDC":
		return filterAllowedNetworks(a.cfg.AllowedPayNetworks, "solana", "base")
	case "USDT":
		return filterAllowedNetworks(a.cfg.AllowedPayNetworks, "tron", "solana")
	case "SOL":
		return filterAllowedNetworks(a.cfg.AllowedPayNetworks, "solana")
	case "ETH":
		return filterAllowedNetworks(a.cfg.AllowedPayNetworks, "base")
	case "BTC":
		return filterAllowedNetworks(a.cfg.AllowedPayNetworks, "bitcoin")
	default:
		out := make([]string, len(a.cfg.AllowedPayNetworks))
		copy(out, a.cfg.AllowedPayNetworks)
		return out
	}
}

func (a *App) DefaultPayOptions() (string, string) {
	return a.cfg.DefaultPayAsset, a.cfg.DefaultPayNetwork
}

func (a *App) PaymentProviderName() string {
	return strings.ToLower(strings.TrimSpace(a.cfg.PaymentProvider))
}

func filterAllowedNetworks(allowed []string, preferred ...string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, network := range allowed {
		allowedSet[strings.ToLower(strings.TrimSpace(network))] = struct{}{}
	}
	out := make([]string, 0, len(preferred))
	for _, network := range preferred {
		network = strings.ToLower(strings.TrimSpace(network))
		if network == "" {
			continue
		}
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[network]; !ok {
				continue
			}
		}
		out = append(out, network)
	}
	if len(out) > 0 {
		return out
	}
	fallback := make([]string, 0, len(allowed))
	for _, network := range allowed {
		network = strings.ToLower(strings.TrimSpace(network))
		if network == "" {
			continue
		}
		fallback = append(fallback, network)
	}
	return fallback
}

func (a *App) FindPlanView(ctx context.Context, actor Actor, planID string) (*domain.PlanView, error) {
	views, err := a.ListUserPlans(ctx, actor)
	if err != nil {
		return nil, err
	}
	for idx := range views {
		if views[idx].Plan.ID == strings.TrimSpace(planID) {
			return &views[idx], nil
		}
	}
	if a.IsOperator(actor.TelegramID) {
		plan, err := a.lookupPlan(ctx, planID)
		if err != nil {
			return nil, err
		}
		view := domain.PlanView{Plan: plan, IsOwner: true}
		return &view, nil
	}
	return nil, ErrNotFound
}

func (a *App) ParseConversationPayload(state *domain.BotConversationState, target any) error {
	if state == nil || strings.TrimSpace(state.PayloadJSON) == "" {
		return nil
	}
	return json.Unmarshal([]byte(state.PayloadJSON), target)
}

func (a *App) EncodeConversationPayload(payload any) (string, error) {
	if payload == nil {
		return "{}", nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode conversation payload: %w", err)
	}
	return string(data), nil
}
