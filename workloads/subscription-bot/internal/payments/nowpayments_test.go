package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"subscriptionbot/internal/store"
)

func TestNowPaymentsQuoteStatusTransactionsAndNormalization(t *testing.T) {
	t.Parallel()

	db := newPaymentsTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("x-api-key"); key != "test-key" {
			t.Fatalf("unexpected x-api-key: %s", key)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/payment":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode create payment payload: %v", err)
			}
			if payload["price_amount"] != "0.50" {
				t.Fatalf("unexpected price amount: %+v", payload)
			}
			if payload["ipn_callback_url"] != "https://farel-subscription-bot.jolkins.id.lv/pixel-stack/subscription/api/v1/payments/webhook/nowpayments" {
				t.Fatalf("unexpected callback url: %+v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payment_id":     "np-1",
				"pay_address":    "wallet-address",
				"pay_amount":     0.500000,
				"price_amount":   0.50,
				"pay_currency":   "usdcsol",
				"order_id":       "invoice-1",
				"payment_status": "waiting",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/payment/np-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payment_id":     "np-1",
				"payment_status": "partially_paid",
				"actually_paid":  0.250000,
				"pay_amount":     "0.500000",
				"pay_currency":   "usdcsol",
				"payin_hash":     "tx-123",
				"confirmations":  2,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := NewNowPaymentsProvider(db, server.URL, "test-key", "secret", 1, 5*time.Second)
	now := time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC)

	quote, err := provider.CreateInvoiceQuote(context.Background(), QuoteRequest{
		InvoiceID:        "invoice-1",
		AnchorTotalMinor: 50,
		PayAsset:         "USDC",
		Network:          "solana",
		WebhookURL:       "https://farel-subscription-bot.jolkins.id.lv/pixel-stack/subscription/api/v1/payments/webhook/nowpayments",
		Now:              now,
		QuoteTTL:         15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("create quote: %v", err)
	}
	if quote.ProviderInvoiceID != "np-1" || quote.QuotedAmountAtomic != "500000" {
		t.Fatalf("unexpected quote: %+v", quote)
	}

	status, err := provider.GetInvoiceStatus(context.Background(), quote.ProviderInvoiceID, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("get invoice status: %v", err)
	}
	if status != "detected" {
		t.Fatalf("expected detected status, got %s", status)
	}

	paymentsList, err := provider.ListInvoiceTransactions(context.Background(), quote.ProviderInvoiceID)
	if err != nil {
		t.Fatalf("list invoice transactions: %v", err)
	}
	if len(paymentsList) != 1 || paymentsList[0].AmountAtomic != "250000" {
		t.Fatalf("unexpected nowpayments transactions: %+v", paymentsList)
	}

	normalized, err := provider.NormalizeProviderPayment(context.Background(), quote.ProviderInvoiceID, paymentsList[0])
	if err != nil {
		t.Fatalf("normalize provider payment: %v", err)
	}
	if normalized.AmountAnchorMinor != 25 {
		t.Fatalf("expected half-payment to normalize to 25 minor units, got %+v", normalized)
	}
}

func TestNowPaymentsWebhookSignatureParsing(t *testing.T) {
	t.Parallel()

	db := newPaymentsTestDB(t)
	provider := NewNowPaymentsProvider(db, "https://example.invalid", "test-key", "secret", 1, 5*time.Second)
	body := []byte(`{"payment_id":"np-2","payment_status":"finished","pay_amount":1.250000,"price_amount":"1.25","pay_currency":"usdcsol","payin_hash":"tx-456"}`)
	mac := hmac.New(sha512.New, []byte("secret"))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	headers := http.Header{}
	headers.Set("x-nowpayments-sig", signature)

	event, err := provider.ParseWebhookEvent(http.Header(headers), body, time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parse webhook event: %v", err)
	}
	if event.ProviderName != "nowpayments" || event.ExternalEventID != "tx-456" {
		t.Fatalf("unexpected webhook event: %+v", event)
	}
	if len(event.Payments) != 1 || event.Payments[0].AmountAtomic != "1250000" {
		t.Fatalf("unexpected webhook payments: %+v", event.Payments)
	}

	headers.Set("x-nowpayments-sig", "bad-signature")
	if _, err := provider.ParseWebhookEvent(http.Header(headers), body, time.Now()); err == nil {
		t.Fatalf("expected invalid signature to fail")
	}
}

func TestNowPaymentsWebhookUsesBodyPaymentWhenLookupLags(t *testing.T) {
	t.Parallel()

	db := newPaymentsTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("x-api-key"); key != "test-key" {
			t.Fatalf("unexpected x-api-key: %s", key)
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/payment/np-2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payment_id":     "np-2",
				"payment_status": "waiting",
				"actually_paid":  "0.000000",
				"pay_amount":     "0.000000",
				"pay_currency":   "usdcsol",
				"payin_hash":     "stale-provider-view",
				"confirmations":  0,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	provider := NewNowPaymentsProvider(db, server.URL, "test-key", "secret", 3, 5*time.Second)
	body := []byte(`{"payment_id":"np-2","payment_status":"finished","pay_amount":1.250000,"price_amount":"1.25","pay_currency":"usdcsol","payin_hash":"tx-456","confirmations":3}`)
	mac := hmac.New(sha512.New, []byte("secret"))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	headers := http.Header{}
	headers.Set("x-nowpayments-sig", signature)

	event, err := provider.ParseWebhookEvent(http.Header(headers), body, time.Date(2026, time.March, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("parse webhook event: %v", err)
	}
	if len(event.Payments) != 1 {
		t.Fatalf("expected one webhook payment, got %+v", event.Payments)
	}
	if event.Payments[0].AmountAtomic != "1250000" {
		t.Fatalf("expected webhook payment amount from body, got %+v", event.Payments[0])
	}
	if event.Payments[0].Confirmations != 3 {
		t.Fatalf("expected webhook confirmations from body, got %+v", event.Payments[0])
	}
	if event.Payments[0].TxHash != "tx-456" {
		t.Fatalf("expected webhook tx hash from body, got %+v", event.Payments[0])
	}
}

func newPaymentsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "payments.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate sqlite store: %v", err)
	}
	return st.DB()
}
