package payments

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type QuoteRequest struct {
	InvoiceID        string
	AnchorTotalMinor int64
	PayAsset         string
	Network          string
	WebhookURL       string
	Now              time.Time
	QuoteTTL         time.Duration
}

type Quote struct {
	ProviderInvoiceID  string
	PaymentRef         string
	PayAsset           string
	Network            string
	QuotedAmountAtomic string
	QuoteRateLabel     string
	QuoteExpiresAt     time.Time
}

type ProviderPayment struct {
	ExternalPaymentID string
	AmountAtomic      string
	Asset             string
	Network           string
	TxHash            string
	Confirmations     int
	SettlementStatus  string
	ReceivedAt        time.Time
}

type NormalizedPayment struct {
	ExternalPaymentID string
	AmountAnchorMinor int64
	AmountAtomic      string
	Asset             string
	Network           string
	TxHash            string
	Confirmations     int
	SettlementStatus  string
	ReceivedAt        time.Time
}

type Provider interface {
	CreateInvoiceQuote(ctx context.Context, request QuoteRequest) (Quote, error)
	GetInvoiceStatus(ctx context.Context, providerInvoiceID string, now time.Time) (string, error)
	ListInvoiceTransactions(ctx context.Context, providerInvoiceID string) ([]ProviderPayment, error)
	NormalizeProviderPayment(ctx context.Context, providerInvoiceID string, payment ProviderPayment) (NormalizedPayment, error)
}

type WebhookEvent struct {
	ProviderName      string
	ExternalEventID   string
	ProviderInvoiceID string
	EventType         string
	Payments          []ProviderPayment
	PayloadJSON       string
}

type HostedProvider interface {
	Provider
	ParseWebhookEvent(headers http.Header, body []byte, now time.Time) (WebhookEvent, error)
}

type Simulatable interface {
	SimulatePayment(ctx context.Context, providerInvoiceID string, amountAtomic string, now time.Time) (ProviderPayment, error)
}

type assetMeta struct {
	PriceMinor   int64
	AtomicFactor int64
	DefaultNet   string
}

var sandboxAssets = map[string]assetMeta{
	"USDC": {PriceMinor: 100, AtomicFactor: 1_000_000, DefaultNet: "solana"},
	"USDT": {PriceMinor: 100, AtomicFactor: 1_000_000, DefaultNet: "tron"},
	"SOL":  {PriceMinor: 15_000, AtomicFactor: 1_000_000_000, DefaultNet: "solana"},
	"ETH":  {PriceMinor: 350_000, AtomicFactor: 1_000_000_000_000_000_000, DefaultNet: "base"},
	"BTC":  {PriceMinor: 6_500_000, AtomicFactor: 100_000_000, DefaultNet: "bitcoin"},
}

type SandboxProvider struct {
	db *sql.DB
}

func NewSandboxProvider(db *sql.DB) *SandboxProvider {
	return &SandboxProvider{db: db}
}

func (p *SandboxProvider) CreateInvoiceQuote(ctx context.Context, request QuoteRequest) (Quote, error) {
	asset := strings.ToUpper(strings.TrimSpace(request.PayAsset))
	if asset == "" {
		asset = "USDC"
	}
	meta, ok := sandboxAssets[asset]
	if !ok {
		return Quote{}, fmt.Errorf("unsupported sandbox asset: %s", asset)
	}
	network := strings.TrimSpace(request.Network)
	if network == "" {
		network = meta.DefaultNet
	}
	quoteAmountAtomic := divideAndRoundUp(request.AnchorTotalMinor, meta.PriceMinor, meta.AtomicFactor)
	providerInvoiceID := fmt.Sprintf("sandbox-%s-%d", request.InvoiceID, request.Now.UTC().UnixNano())
	paymentRef := fmt.Sprintf("%s@%s", providerInvoiceID, network)
	expiresAt := request.Now.UTC().Add(request.QuoteTTL)
	if _, err := p.db.ExecContext(ctx, `
		INSERT INTO sandbox_quotes(
			provider_invoice_id, invoice_id, anchor_total_minor, pay_asset, network, quote_amount_atomic,
			asset_price_minor, atomic_factor, expires_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, providerInvoiceID, request.InvoiceID, request.AnchorTotalMinor, asset, network, quoteAmountAtomic, meta.PriceMinor, fmt.Sprintf("%d", meta.AtomicFactor), expiresAt.Format(time.RFC3339), request.Now.UTC().Format(time.RFC3339)); err != nil {
		return Quote{}, fmt.Errorf("insert sandbox quote: %w", err)
	}
	return Quote{
		ProviderInvoiceID:  providerInvoiceID,
		PaymentRef:         paymentRef,
		PayAsset:           asset,
		Network:            network,
		QuotedAmountAtomic: quoteAmountAtomic,
		QuoteRateLabel:     fmt.Sprintf("sandbox %s/%s", asset, network),
		QuoteExpiresAt:     expiresAt,
	}, nil
}

func (p *SandboxProvider) GetInvoiceStatus(ctx context.Context, providerInvoiceID string, now time.Time) (string, error) {
	var expiresAt string
	if err := p.db.QueryRowContext(ctx, `SELECT expires_at FROM sandbox_quotes WHERE provider_invoice_id = ?`, providerInvoiceID).Scan(&expiresAt); err != nil {
		return "", fmt.Errorf("load sandbox quote: %w", err)
	}
	var count int
	if err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sandbox_payments WHERE provider_invoice_id = ?`, providerInvoiceID).Scan(&count); err != nil {
		return "", fmt.Errorf("count sandbox payments: %w", err)
	}
	if count > 0 {
		return "detected", nil
	}
	expiresAtTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return "", fmt.Errorf("parse sandbox expires_at: %w", err)
	}
	if now.UTC().After(expiresAtTime) {
		return "expired", nil
	}
	return "open", nil
}

func (p *SandboxProvider) ListInvoiceTransactions(ctx context.Context, providerInvoiceID string) ([]ProviderPayment, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT external_payment_id, amount_atomic, asset, network, tx_hash, confirmations, settlement_status, received_at
		FROM sandbox_payments
		WHERE provider_invoice_id = ?
		ORDER BY received_at ASC
	`, providerInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("list sandbox payments: %w", err)
	}
	defer rows.Close()
	out := make([]ProviderPayment, 0)
	for rows.Next() {
		var payment ProviderPayment
		var receivedAt string
		if err := rows.Scan(&payment.ExternalPaymentID, &payment.AmountAtomic, &payment.Asset, &payment.Network, &payment.TxHash, &payment.Confirmations, &payment.SettlementStatus, &receivedAt); err != nil {
			return nil, fmt.Errorf("scan sandbox payment: %w", err)
		}
		payment.ReceivedAt, err = time.Parse(time.RFC3339, receivedAt)
		if err != nil {
			return nil, fmt.Errorf("parse sandbox payment time: %w", err)
		}
		out = append(out, payment)
	}
	return out, rows.Err()
}

func (p *SandboxProvider) NormalizeProviderPayment(ctx context.Context, providerInvoiceID string, payment ProviderPayment) (NormalizedPayment, error) {
	var assetPriceMinor int64
	var atomicFactorText string
	if err := p.db.QueryRowContext(ctx, `
		SELECT asset_price_minor, atomic_factor
		FROM sandbox_quotes
		WHERE provider_invoice_id = ?
	`, providerInvoiceID).Scan(&assetPriceMinor, &atomicFactorText); err != nil {
		return NormalizedPayment{}, fmt.Errorf("load sandbox quote metadata: %w", err)
	}
	amountMinor, err := atomicToAnchorMinor(payment.AmountAtomic, assetPriceMinor, atomicFactorText)
	if err != nil {
		return NormalizedPayment{}, err
	}
	return NormalizedPayment{
		ExternalPaymentID: payment.ExternalPaymentID,
		AmountAnchorMinor: amountMinor,
		AmountAtomic:      payment.AmountAtomic,
		Asset:             payment.Asset,
		Network:           payment.Network,
		TxHash:            payment.TxHash,
		Confirmations:     payment.Confirmations,
		SettlementStatus:  payment.SettlementStatus,
		ReceivedAt:        payment.ReceivedAt,
	}, nil
}

func (p *SandboxProvider) SimulatePayment(ctx context.Context, providerInvoiceID string, amountAtomic string, now time.Time) (ProviderPayment, error) {
	var payAsset, network, quotedAmount string
	if err := p.db.QueryRowContext(ctx, `
		SELECT pay_asset, network, quote_amount_atomic
		FROM sandbox_quotes
		WHERE provider_invoice_id = ?
	`, providerInvoiceID).Scan(&payAsset, &network, &quotedAmount); err != nil {
		return ProviderPayment{}, fmt.Errorf("load sandbox quote for payment: %w", err)
	}
	if strings.TrimSpace(amountAtomic) == "" {
		amountAtomic = quotedAmount
	}
	externalID := fmt.Sprintf("sandbox-payment-%d", now.UTC().UnixNano())
	txHash := hex.EncodeToString([]byte(externalID))
	if _, err := p.db.ExecContext(ctx, `
		INSERT INTO sandbox_payments(
			id, provider_invoice_id, external_payment_id, amount_atomic, asset, network, tx_hash,
			confirmations, settlement_status, received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, externalID, providerInvoiceID, externalID, amountAtomic, payAsset, network, txHash, 1, "confirmed", now.UTC().Format(time.RFC3339)); err != nil {
		return ProviderPayment{}, fmt.Errorf("insert sandbox payment: %w", err)
	}
	return ProviderPayment{
		ExternalPaymentID: externalID,
		AmountAtomic:      amountAtomic,
		Asset:             payAsset,
		Network:           network,
		TxHash:            txHash,
		Confirmations:     1,
		SettlementStatus:  "confirmed",
		ReceivedAt:        now.UTC(),
	}, nil
}

func divideAndRoundUp(totalMinor int64, assetPriceMinor int64, atomicFactor int64) string {
	numerator := big.NewInt(totalMinor)
	numerator.Mul(numerator, big.NewInt(atomicFactor))
	denominator := big.NewInt(assetPriceMinor)
	quotient, remainder := new(big.Int).QuoRem(numerator, denominator, new(big.Int))
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.String()
}

func atomicToAnchorMinor(amountAtomic string, assetPriceMinor int64, atomicFactorText string) (int64, error) {
	amount := new(big.Int)
	if _, ok := amount.SetString(strings.TrimSpace(amountAtomic), 10); !ok {
		return 0, fmt.Errorf("invalid amount_atomic: %s", amountAtomic)
	}
	atomicFactor := new(big.Int)
	if _, ok := atomicFactor.SetString(strings.TrimSpace(atomicFactorText), 10); !ok {
		return 0, fmt.Errorf("invalid atomic_factor: %s", atomicFactorText)
	}
	numerator := new(big.Int).Mul(amount, big.NewInt(assetPriceMinor))
	half := new(big.Int).Div(atomicFactor, big.NewInt(2))
	numerator.Add(numerator, half)
	value := new(big.Int).Div(numerator, atomicFactor)
	if !value.IsInt64() {
		return 0, fmt.Errorf("normalized payment exceeds int64")
	}
	return value.Int64(), nil
}
