package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"subscriptionbot/internal/config"
	"subscriptionbot/internal/domain"
	"subscriptionbot/internal/payments"
	"subscriptionbot/internal/service"
	"subscriptionbot/internal/store"
)

type webHarness struct {
	server *Server
	app    *service.App
	loc    *time.Location
	cfg    config.Config
	ctx    context.Context
}

func newWebHarness(t *testing.T) *webHarness {
	return newWebHarnessWithConfig(t, func(cfg *config.Config) {})
}

func newWebHarnessWithConfig(t *testing.T, mutate func(*config.Config)) *webHarness {
	return newWebHarnessWithProvider(t, mutate, nil)
}

func newWebHarnessWithProvider(t *testing.T, mutate func(*config.Config), provider payments.Provider) *webHarness {
	return newWebHarnessWithProviderFactory(t, mutate, func(*sql.DB, config.Config) payments.Provider {
		return provider
	})
}

func newWebHarnessWithProviderFactory(t *testing.T, mutate func(*config.Config), providerFactory func(*sql.DB, config.Config) payments.Provider) *webHarness {
	t.Helper()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "subscription.secret")
	if err := os.WriteFile(secretPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	st, err := store.NewSQLiteStore(filepath.Join(dir, "subscription.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Config{
		BotToken:              "bot-token",
		TelegramBotUsername:   "farel_subscription_bot",
		WebEnabled:            true,
		WebShellEnabled:       false,
		WebBindAddr:           "127.0.0.1",
		WebPort:               9320,
		WebPublicBaseURL:      "https://example.test/pixel-stack/subscription",
		SessionSecretFile:     secretPath,
		TelegramAuthMaxAgeSec: 300,
		PlatformFeeBps:        1000,
		GraceDays:             3,
		RenewalLeadDays:       7,
		ReminderDays:          []int{7, 3, 1},
		DefaultPayAsset:       "USDC",
		DefaultPayNetwork:     "solana",
		QuoteTTL:              15 * time.Minute,
		OperatorIDs:           map[int64]struct{}{9000: {}},
	}
	mutate(&cfg)
	provider := payments.Provider(nil)
	if providerFactory != nil {
		provider = providerFactory(st.DB(), cfg)
	}
	if provider == nil {
		provider = payments.NewSandboxProvider(st.DB())
	}
	app := service.New(st.DB(), cfg, provider, loc)
	if err := app.SeedCatalog(ctx); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	server, err := NewServer(cfg, app, loc)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	server.now = func() time.Time { return time.Date(2026, time.March, 12, 10, 0, 0, 0, time.UTC) }
	return &webHarness{
		server: server,
		app:    app,
		loc:    loc,
		cfg:    cfg,
		ctx:    ctx,
	}
}

type fakeHostedProvider struct {
	nextQuoteID string
}

func (f *fakeHostedProvider) CreateInvoiceQuote(_ context.Context, request payments.QuoteRequest) (payments.Quote, error) {
	providerInvoiceID := strings.TrimSpace(f.nextQuoteID)
	if providerInvoiceID == "" {
		providerInvoiceID = "np-webhook-1"
	}
	return payments.Quote{
		ProviderInvoiceID:  providerInvoiceID,
		PaymentRef:         "wallet-address",
		PayAsset:           "USDC",
		Network:            "solana",
		QuotedAmountAtomic: fmt.Sprintf("%d0000", request.AnchorTotalMinor),
		QuoteRateLabel:     "fake nowpayments",
		QuoteExpiresAt:     request.Now.Add(request.QuoteTTL),
	}, nil
}

func (f *fakeHostedProvider) GetInvoiceStatus(context.Context, string, time.Time) (string, error) {
	return "open", nil
}

func (f *fakeHostedProvider) ListInvoiceTransactions(context.Context, string) ([]payments.ProviderPayment, error) {
	return nil, nil
}

func (f *fakeHostedProvider) NormalizeProviderPayment(_ context.Context, _ string, payment payments.ProviderPayment) (payments.NormalizedPayment, error) {
	amountAtomic := strings.TrimSpace(payment.AmountAtomic)
	if !strings.HasSuffix(amountAtomic, "0000") {
		return payments.NormalizedPayment{}, errors.New("unexpected fake atomic amount")
	}
	minor, err := strconv.ParseInt(strings.TrimSuffix(amountAtomic, "0000"), 10, 64)
	if err != nil {
		return payments.NormalizedPayment{}, err
	}
	return payments.NormalizedPayment{
		ExternalPaymentID: payment.ExternalPaymentID,
		AmountAnchorMinor: minor,
		AmountAtomic:      payment.AmountAtomic,
		Asset:             payment.Asset,
		Network:           payment.Network,
		TxHash:            payment.TxHash,
		Confirmations:     payment.Confirmations,
		SettlementStatus:  payment.SettlementStatus,
		ReceivedAt:        payment.ReceivedAt,
	}, nil
}

func (f *fakeHostedProvider) ParseWebhookEvent(_ http.Header, body []byte, now time.Time) (payments.WebhookEvent, error) {
	var payload struct {
		ProviderInvoiceID string `json:"provider_invoice_id"`
		ExternalEventID   string `json:"external_event_id"`
		AmountAtomic      string `json:"amount_atomic"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return payments.WebhookEvent{}, err
	}
	return payments.WebhookEvent{
		ProviderName:      "nowpayments",
		ExternalEventID:   payload.ExternalEventID,
		ProviderInvoiceID: payload.ProviderInvoiceID,
		EventType:         "finished",
		PayloadJSON:       string(body),
		Payments: []payments.ProviderPayment{{
			ExternalPaymentID: payload.ExternalEventID,
			AmountAtomic:      payload.AmountAtomic,
			Asset:             "USDC",
			Network:           "solana",
			TxHash:            "tx-webhook-1",
			Confirmations:     2,
			SettlementStatus:  "finished",
			ReceivedAt:        now.UTC(),
		}},
	}, nil
}

type notificationRecorder struct {
	items []string
}

func (r *notificationRecorder) DispatchNotifications(_ context.Context, notifications []domain.Notification) error {
	for _, item := range notifications {
		r.items = append(r.items, item.Message)
	}
	return nil
}

func TestAuthTelegramSetsSessionAndSessionEndpointReturnsUser(t *testing.T) {
	t.Parallel()

	h := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.WebShellEnabled = true
	})
	now := h.server.now()
	auth := telegramAuth{
		QueryID:  "AAEAAAE",
		AuthDate: now.Add(-30 * time.Second),
		User: telegramUser{
			ID:           7001,
			FirstName:    "Alex",
			Username:     "alex",
			LanguageCode: "en",
		},
	}
	body := map[string]string{
		"initData": signedInitData(t, h.cfg.BotToken, auth),
	}
	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/auth/telegram", jsonBody(t, body))
	res := httptest.NewRecorder()
	h.server.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("unexpected auth status: got %d body=%s", res.Code, res.Body.String())
	}
	cookies := res.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cookies))
	}
	if cookies[0].Path != "/pixel-stack/subscription" {
		t.Fatalf("unexpected cookie path: %q", cookies[0].Path)
	}

	sessionReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/session", nil)
	sessionReq.AddCookie(cookies[0])
	sessionRes := httptest.NewRecorder()
	h.server.ServeHTTP(sessionRes, sessionReq)
	if sessionRes.Code != http.StatusOK {
		t.Fatalf("unexpected session status: got %d body=%s", sessionRes.Code, sessionRes.Body.String())
	}
	if !strings.Contains(sessionRes.Body.String(), "\"user_id\":7001") {
		t.Fatalf("expected session payload with user id, got %s", sessionRes.Body.String())
	}
}

func TestPlanCreationRespectsApprovedCatalogAndMembersEndpointRequiresOwner(t *testing.T) {
	t.Parallel()

	h := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.WebShellEnabled = true
	})
	ownerCookie := testSessionCookie(t, h.server, 7101, "owner", "en", h.server.now())
	memberCookie := testSessionCookie(t, h.server, 7102, "member", "en", h.server.now())

	badReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/plans", jsonBody(t, map[string]any{
		"service_code":       "netflix_password_sharing",
		"total_price_minor":  1500,
		"seat_limit":         2,
		"renewal_date":       "2026-04-01",
		"sharing_policy_ack": true,
		"access_mode":        "invite_seat",
	}))
	badReq.AddCookie(ownerCookie)
	badRes := httptest.NewRecorder()
	h.server.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusBadRequest {
		t.Fatalf("expected rejected unapproved service, got %d body=%s", badRes.Code, badRes.Body.String())
	}

	createReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/plans", jsonBody(t, map[string]any{
		"service_code":       "spotify_family",
		"total_price_minor":  1800,
		"seat_limit":         2,
		"renewal_date":       "2026-04-01",
		"sharing_policy_ack": true,
		"access_mode":        "invite_seat",
	}))
	createReq.AddCookie(ownerCookie)
	createRes := httptest.NewRecorder()
	h.server.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected created plan, got %d body=%s", createRes.Code, createRes.Body.String())
	}
	var created struct {
		Plan struct {
			ID string `json:"id"`
		} `json:"plan"`
		Invite struct {
			InviteCode string `json:"InviteCode"`
		} `json:"invite"`
		Launch struct {
			StartApp         string `json:"startApp"`
			AppURL           string `json:"appUrl"`
			TelegramDeepLink string `json:"telegramDeepLink"`
		} `json:"launch"`
	}
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Invite.InviteCode == "" {
		t.Fatalf("expected create response invite code, got %+v", created)
	}
	expectedStartApp := "join-" + created.Invite.InviteCode
	if created.Launch.StartApp != expectedStartApp {
		t.Fatalf("expected create response startapp %q, got %+v", expectedStartApp, created.Launch)
	}
	if created.Launch.AppURL != "https://example.test/app?invite_code="+created.Invite.InviteCode+"&plan_id="+created.Plan.ID+"&section=join&startapp="+expectedStartApp {
		t.Fatalf("unexpected create response app launch url: %+v", created.Launch)
	}
	if created.Launch.TelegramDeepLink != "https://t.me/farel_subscription_bot?startapp="+expectedStartApp {
		t.Fatalf("unexpected create response telegram deep link: %+v", created.Launch)
	}

	memberReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/plans/"+created.Plan.ID+"/members", nil)
	memberReq.AddCookie(memberCookie)
	memberRes := httptest.NewRecorder()
	h.server.ServeHTTP(memberRes, memberReq)
	if memberRes.Code != http.StatusForbidden {
		t.Fatalf("expected member access denied, got %d body=%s", memberRes.Code, memberRes.Body.String())
	}

	ownerReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/plans/"+created.Plan.ID+"/members", nil)
	ownerReq.AddCookie(ownerCookie)
	ownerRes := httptest.NewRecorder()
	h.server.ServeHTTP(ownerRes, ownerReq)
	if ownerRes.Code != http.StatusOK {
		t.Fatalf("expected owner access ok, got %d body=%s", ownerRes.Code, ownerRes.Body.String())
	}
}

func TestAdminOverviewRequiresOperator(t *testing.T) {
	t.Parallel()

	h := newWebHarness(t)
	memberCookie := testSessionCookie(t, h.server, 7201, "member", "en", h.server.now())
	operatorCookie := testSessionCookie(t, h.server, 9000, "operator", "en", h.server.now())

	memberReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/admin/overview", nil)
	memberReq.AddCookie(memberCookie)
	memberRes := httptest.NewRecorder()
	h.server.ServeHTTP(memberRes, memberReq)
	if memberRes.Code != http.StatusForbidden {
		t.Fatalf("expected member forbidden, got %d body=%s", memberRes.Code, memberRes.Body.String())
	}

	operatorReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/admin/overview", nil)
	operatorReq.AddCookie(operatorCookie)
	operatorRes := httptest.NewRecorder()
	h.server.ServeHTTP(operatorRes, operatorReq)
	if operatorRes.Code != http.StatusOK {
		t.Fatalf("expected operator overview, got %d body=%s", operatorRes.Code, operatorRes.Body.String())
	}
}

func TestBrowserShellRoutesStayDarkWhenShellDisabled(t *testing.T) {
	t.Parallel()

	h := newWebHarness(t)
	for _, path := range []string{
		"/",
		"/app",
		"/admin",
		"/pixel-stack/subscription",
		"/pixel-stack/subscription/app",
		"/pixel-stack/subscription/admin",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		h.server.ServeHTTP(res, req)
		if res.Code != http.StatusNotFound {
			t.Fatalf("expected shell route %s to return 404, got %d", path, res.Code)
		}
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/health", nil)
	healthRes := httptest.NewRecorder()
	h.server.ServeHTTP(healthRes, healthReq)
	if healthRes.Code != http.StatusOK {
		t.Fatalf("expected health route to stay available, got %d body=%s", healthRes.Code, healthRes.Body.String())
	}
}

func TestShellRoutesServeMiniAppAssetsWhenEnabled(t *testing.T) {
	t.Parallel()

	h := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.WebShellEnabled = true
	})
	for path, mode := range map[string]string{
		"/":                               "launcher",
		"/app":                            "app",
		"/admin":                          "admin",
		"/pixel-stack/subscription":       "launcher",
		"/pixel-stack/subscription/app":   "app",
		"/pixel-stack/subscription/admin": "admin",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		h.server.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("expected shell route %s to return 200, got %d body=%s", path, res.Code, res.Body.String())
		}
		body := res.Body.String()
		if !strings.Contains(body, "/pixel-stack/subscription/assets/app.css?v=") {
			t.Fatalf("expected versioned app.css asset in %s body=%s", path, body)
		}
		if !strings.Contains(body, "/pixel-stack/subscription/assets/app.js?v=") {
			t.Fatalf("expected versioned app.js asset in %s body=%s", path, body)
		}
		if !strings.Contains(body, `data-mode="`+mode+`"`) {
			t.Fatalf("expected shell route %s to render mode %s, got %s", path, mode, body)
		}
		if res.Result().Header.Get("X-Subscription-Bot-App-Js") == "" {
			t.Fatalf("expected release header on shell response for %s", path)
		}
	}

	assetReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/assets/app.css", nil)
	assetRes := httptest.NewRecorder()
	h.server.ServeHTTP(assetRes, assetReq)
	if assetRes.Code != http.StatusOK {
		t.Fatalf("expected asset route ok, got %d body=%s", assetRes.Code, assetRes.Body.String())
	}
	if !strings.Contains(assetRes.Body.String(), "--tg-bg-color:") {
		t.Fatalf("expected app.css body, got %s", assetRes.Body.String())
	}
}

func TestNowPaymentsWebhookRouteSettlesInvoiceAndDispatchesNotification(t *testing.T) {
	t.Parallel()

	provider := &fakeHostedProvider{nextQuoteID: "np-webhook-1"}
	h := newWebHarnessWithProvider(t, func(cfg *config.Config) {}, provider)
	recorder := &notificationRecorder{}
	h.server.SetNotifier(recorder)

	owner := service.Actor{TelegramID: 8101, Username: "owner"}
	member := service.Actor{TelegramID: 8102, Username: "member"}
	now := h.server.now()
	_, invite, err := h.app.CreatePlan(h.ctx, owner, service.CreatePlanInput{
		ServiceCode:      "spotify_family",
		TotalPriceMinor:  1800,
		SeatLimit:        2,
		RenewalDate:      time.Date(2026, time.April, 1, 0, 0, 0, 0, h.loc),
		SharingPolicyAck: true,
		AccessMode:       "invite_seat",
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

	req := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/payments/webhook/nowpayments", jsonBody(t, map[string]any{
		"provider_invoice_id": invoice.ProviderInvoiceID,
		"external_event_id":   "tx-webhook-1",
		"amount_atomic":       invoice.QuotedPayAmount,
	}))
	res := httptest.NewRecorder()
	h.server.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected webhook accepted, got %d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "\"invoice_found\":true") {
		t.Fatalf("expected invoice_found in webhook response, got %s", res.Body.String())
	}

	confirmed, err := h.app.LoadInvoiceByID(h.ctx, member, invoice.ID)
	if err != nil {
		t.Fatalf("load invoice after webhook: %v", err)
	}
	if confirmed.Status != "confirmed" {
		t.Fatalf("expected confirmed invoice after webhook, got %s", confirmed.Status)
	}
	if len(recorder.items) != 1 || !strings.Contains(recorder.items[0], "Payment confirmed") {
		t.Fatalf("expected payment confirmation notification, got %+v", recorder.items)
	}
}

func TestBootstrapSupportResolveAndBlockUserEndpoints(t *testing.T) {
	t.Parallel()

	h := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.WebShellEnabled = true
	})
	operatorCookie := testSessionCookie(t, h.server, 9000, "operator", "en", h.server.now())
	memberCookie := testSessionCookie(t, h.server, 7302, "member", "en", h.server.now())

	owner := service.Actor{TelegramID: 7301, Username: "owner"}
	member := service.Actor{TelegramID: 7302, Username: "member"}
	now := h.server.now()
	plan, invite, err := h.app.CreatePlan(h.ctx, owner, service.CreatePlanInput{
		ServiceCode:      "spotify_family",
		TotalPriceMinor:  1800,
		SeatLimit:        2,
		RenewalDate:      time.Date(2026, time.April, 1, 0, 0, 0, 0, h.loc),
		SharingPolicyAck: true,
		AccessMode:       "invite_seat",
	}, now)
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	_, invoice, err := h.app.JoinPlan(h.ctx, member, invite.InviteCode, now)
	if err != nil {
		t.Fatalf("join plan: %v", err)
	}
	invoice, err = h.app.QuoteInvoice(h.ctx, member, invoice.ID, "USDC", "solana", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("quote invoice: %v", err)
	}
	ticket, err := h.app.OpenSupportTicket(h.ctx, member, plan.ID, "Need help with billing", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("open support: %v", err)
	}

	bootstrapReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/bootstrap?startapp=invoice-"+invoice.ID, nil)
	bootstrapReq.AddCookie(memberCookie)
	bootstrapRes := httptest.NewRecorder()
	h.server.ServeHTTP(bootstrapRes, bootstrapReq)
	if bootstrapRes.Code != http.StatusOK {
		t.Fatalf("expected bootstrap ok, got %d body=%s", bootstrapRes.Code, bootstrapRes.Body.String())
	}
	var bootstrap struct {
		Payments struct {
			DefaultAsset string `json:"defaultAsset"`
		} `json:"payments"`
		Plans  []domain.PlanView `json:"plans"`
		Launch struct {
			BotUsername    string `json:"botUsername"`
			TelegramBotURL string `json:"telegramBotUrl"`
			AppURL         string `json:"appUrl"`
			AdminURL       string `json:"adminUrl"`
			StartApp       string `json:"startApp"`
			Section        string `json:"section"`
			PlanID         string `json:"planId"`
			InvoiceID      string `json:"invoiceId"`
		} `json:"launch"`
		LatestInvoice  *domain.Invoice `json:"latestInvoice"`
		InvoiceActions map[string]struct {
			CopyReference    string `json:"copyReference"`
			TelegramDeepLink string `json:"telegramDeepLink"`
		} `json:"invoiceActions"`
	}
	if err := json.Unmarshal(bootstrapRes.Body.Bytes(), &bootstrap); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if bootstrap.Payments.DefaultAsset != "USDC" {
		t.Fatalf("expected default asset in bootstrap, got %+v", bootstrap.Payments)
	}
	if len(bootstrap.Plans) != 1 || bootstrap.LatestInvoice == nil || bootstrap.LatestInvoice.ID != invoice.ID {
		t.Fatalf("unexpected bootstrap payload: %+v", bootstrap)
	}
	if bootstrap.Launch.BotUsername != "farel_subscription_bot" || bootstrap.Launch.Section != "invoice" {
		t.Fatalf("expected native launch metadata in bootstrap, got %+v", bootstrap.Launch)
	}
	if bootstrap.Launch.StartApp != "invoice-"+invoice.ID || bootstrap.Launch.InvoiceID != invoice.ID || bootstrap.Launch.PlanID != plan.ID {
		t.Fatalf("expected bootstrap launch context to hydrate invoice and plan ids, got %+v", bootstrap.Launch)
	}
	if bootstrap.Launch.TelegramBotURL != "https://t.me/farel_subscription_bot" || bootstrap.Launch.AppURL != "https://example.test/app" || bootstrap.Launch.AdminURL != "https://example.test/admin" {
		t.Fatalf("unexpected bootstrap launch urls: %+v", bootstrap.Launch)
	}
	if actions := bootstrap.InvoiceActions[invoice.ID]; actions.CopyReference == "" || actions.TelegramDeepLink != "https://t.me/farel_subscription_bot?startapp=invoice-"+invoice.ID {
		t.Fatalf("expected invoice payment actions in bootstrap, got %+v", bootstrap.InvoiceActions)
	}

	forbiddenReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/admin/support", nil)
	forbiddenReq.AddCookie(memberCookie)
	forbiddenRes := httptest.NewRecorder()
	h.server.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("expected member forbidden on admin support, got %d body=%s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	supportReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/admin/support", nil)
	supportReq.AddCookie(operatorCookie)
	supportRes := httptest.NewRecorder()
	h.server.ServeHTTP(supportRes, supportReq)
	if supportRes.Code != http.StatusOK {
		t.Fatalf("expected operator admin support ok, got %d body=%s", supportRes.Code, supportRes.Body.String())
	}
	var supportPayload struct {
		Tickets []domain.SupportTicketView `json:"tickets"`
	}
	if err := json.Unmarshal(supportRes.Body.Bytes(), &supportPayload); err != nil {
		t.Fatalf("decode admin support payload: %v", err)
	}
	if len(supportPayload.Tickets) != 1 || supportPayload.Tickets[0].Ticket.ID != ticket.ID {
		t.Fatalf("unexpected admin support payload: %+v", supportPayload)
	}

	resolveReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/admin/support/"+ticket.ID+"/resolve", jsonBody(t, map[string]any{
		"note": "handled in Mini App",
	}))
	resolveReq.AddCookie(operatorCookie)
	resolveRes := httptest.NewRecorder()
	h.server.ServeHTTP(resolveRes, resolveReq)
	if resolveRes.Code != http.StatusOK {
		t.Fatalf("expected support resolve ok, got %d body=%s", resolveRes.Code, resolveRes.Body.String())
	}
	var resolved struct {
		Ticket domain.SupportTicket `json:"ticket"`
	}
	if err := json.Unmarshal(resolveRes.Body.Bytes(), &resolved); err != nil {
		t.Fatalf("decode resolved ticket payload: %v", err)
	}
	if resolved.Ticket.Status != domain.TicketResolved {
		t.Fatalf("expected resolved ticket status, got %+v", resolved)
	}

	supportAfterReq := httptest.NewRequest(http.MethodGet, "/pixel-stack/subscription/api/v1/admin/support", nil)
	supportAfterReq.AddCookie(operatorCookie)
	supportAfterRes := httptest.NewRecorder()
	h.server.ServeHTTP(supportAfterRes, supportAfterReq)
	if supportAfterRes.Code != http.StatusOK {
		t.Fatalf("expected operator admin support after resolve ok, got %d body=%s", supportAfterRes.Code, supportAfterRes.Body.String())
	}
	if strings.Contains(supportAfterRes.Body.String(), ticket.ID) {
		t.Fatalf("expected resolved ticket to leave the open queue, got %s", supportAfterRes.Body.String())
	}

	blockReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/admin/denylist/block-user", jsonBody(t, map[string]any{
		"telegram_id": 7444,
		"reason":      "fraud review",
	}))
	blockReq.AddCookie(operatorCookie)
	blockRes := httptest.NewRecorder()
	h.server.ServeHTTP(blockRes, blockReq)
	if blockRes.Code != http.StatusCreated {
		t.Fatalf("expected block-user endpoint created, got %d body=%s", blockRes.Code, blockRes.Body.String())
	}
	blocked, reason, err := h.app.IsTelegramIDDenied(h.ctx, 7444)
	if err != nil {
		t.Fatalf("lookup blocked actor: %v", err)
	}
	if !blocked || !strings.Contains(reason, "fraud") {
		t.Fatalf("expected blocked actor after web API action, got blocked=%v reason=%q", blocked, reason)
	}
}

func TestProcessCycleTestEndpointRequiresE2EModeAndOperator(t *testing.T) {
	t.Parallel()

	disabled := newWebHarness(t)
	operatorCookie := testSessionCookie(t, disabled.server, 9000, "operator", "en", disabled.server.now())
	disabledReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/process-cycle", jsonBody(t, map[string]any{
		"at": "2026-03-26T08:00:00+02:00",
	}))
	disabledReq.AddCookie(operatorCookie)
	disabledRes := httptest.NewRecorder()
	disabled.server.ServeHTTP(disabledRes, disabledReq)
	if disabledRes.Code != http.StatusNotFound {
		t.Fatalf("expected endpoint hidden when e2e mode is off, got %d body=%s", disabledRes.Code, disabledRes.Body.String())
	}

	enabled := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.E2EMode = true
		cfg.WebShellEnabled = true
	})
	memberCookie := testSessionCookie(t, enabled.server, 7301, "member", "en", enabled.server.now())
	operatorCookie = testSessionCookie(t, enabled.server, 9000, "operator", "en", enabled.server.now())

	ownerCookie := testSessionCookie(t, enabled.server, 7300, "owner", "en", enabled.server.now())
	createReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/plans", jsonBody(t, map[string]any{
		"service_code":       "spotify_family",
		"total_price_minor":  1800,
		"seat_limit":         2,
		"renewal_date":       "2026-04-01",
		"sharing_policy_ack": true,
		"access_mode":        "invite_seat",
	}))
	createReq.AddCookie(ownerCookie)
	createRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected plan create ok, got %d body=%s", createRes.Code, createRes.Body.String())
	}
	var created struct {
		Invite struct {
			InviteCode string `json:"InviteCode"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	joinReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/plans/join", jsonBody(t, map[string]any{
		"invite_code": created.Invite.InviteCode,
	}))
	joinReq.AddCookie(memberCookie)
	joinRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(joinRes, joinReq)
	if joinRes.Code != http.StatusOK {
		t.Fatalf("expected join ok, got %d body=%s", joinRes.Code, joinRes.Body.String())
	}
	var joined struct {
		Invoice struct {
			ID string `json:"id"`
		} `json:"invoice"`
	}
	if err := json.Unmarshal(joinRes.Body.Bytes(), &joined); err != nil {
		t.Fatalf("decode join response: %v", err)
	}

	quoteReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/invoices/"+joined.Invoice.ID+"/quote", jsonBody(t, map[string]any{
		"pay_asset": "USDC",
		"network":   "solana",
	}))
	quoteReq.AddCookie(memberCookie)
	quoteRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(quoteRes, quoteReq)
	if quoteRes.Code != http.StatusOK {
		t.Fatalf("expected quote ok, got %d body=%s", quoteRes.Code, quoteRes.Body.String())
	}
	var quoted struct {
		Invoice struct {
			QuotedPayAmount string `json:"QuotedPayAmount"`
		} `json:"invoice"`
		PaymentActions struct {
			CopyReference      string `json:"copyReference"`
			CopyPaymentDetails string `json:"copyPaymentDetails"`
			TelegramDeepLink   string `json:"telegramDeepLink"`
		} `json:"payment_actions"`
	}
	if err := json.Unmarshal(quoteRes.Body.Bytes(), &quoted); err != nil {
		t.Fatalf("decode quote response: %v", err)
	}
	if quoted.PaymentActions.CopyReference == "" || quoted.PaymentActions.CopyPaymentDetails == "" || !strings.Contains(quoted.PaymentActions.TelegramDeepLink, "startapp=invoice-"+joined.Invoice.ID) {
		t.Fatalf("expected quote response payment actions, got %+v", quoted)
	}

	simulateReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/invoices/"+joined.Invoice.ID+"/simulate", jsonBody(t, map[string]any{
		"amount_atomic": quoted.Invoice.QuotedPayAmount,
	}))
	simulateReq.AddCookie(memberCookie)
	simulateRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(simulateRes, simulateReq)
	if simulateRes.Code != http.StatusOK {
		t.Fatalf("expected simulate ok, got %d body=%s", simulateRes.Code, simulateRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/process-cycle", jsonBody(t, map[string]any{
		"at": "2026-03-10T09:02:00+02:00",
	}))
	forbiddenReq.AddCookie(memberCookie)
	forbiddenRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("expected member forbidden, got %d body=%s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	operatorReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/process-cycle", jsonBody(t, map[string]any{
		"at": "2026-03-10T09:02:00+02:00",
	}))
	operatorReq.AddCookie(operatorCookie)
	operatorRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(operatorRes, operatorReq)
	if operatorRes.Code != http.StatusOK {
		t.Fatalf("expected operator cycle processing ok, got %d body=%s", operatorRes.Code, operatorRes.Body.String())
	}
	if !strings.Contains(operatorRes.Body.String(), "\"ok\":true") {
		t.Fatalf("expected ok response, got %s", operatorRes.Body.String())
	}
	if !strings.Contains(operatorRes.Body.String(), "Payment confirmed") {
		t.Fatalf("expected payment confirmation notification, got %s", operatorRes.Body.String())
	}
}

func TestQuoteEndpointAcceptsNumericNowPaymentsAmounts(t *testing.T) {
	t.Parallel()

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("x-api-key"); key != "test-key" {
			t.Fatalf("unexpected x-api-key: %s", key)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/payment":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode create payment payload: %v", err)
			}
			if payload["price_currency"] != "usd" || payload["pay_currency"] != "usdcsol" {
				t.Fatalf("unexpected quote request payload: %+v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payment_id":     "np-web-1",
				"payment_status": "waiting",
				"pay_address":    "wallet-address",
				"pay_amount":     0.900000,
				"price_amount":   9.00,
				"price_currency": "usd",
				"pay_currency":   "usdcsol",
				"order_id":       payload["order_id"],
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()

	enabled := newWebHarnessWithProviderFactory(t, func(cfg *config.Config) {
		cfg.PaymentProvider = "nowpayments"
		cfg.NowPaymentsAPIBaseURL = providerServer.URL
		cfg.NowPaymentsAPIKey = "test-key"
		cfg.NowPaymentsIPNSecret = "secret"
		cfg.RequiredConfirmations = 1
		cfg.HTTPTimeoutSec = 5
	}, func(db *sql.DB, cfg config.Config) payments.Provider {
		return payments.NewNowPaymentsProvider(
			db,
			cfg.NowPaymentsAPIBaseURL,
			cfg.NowPaymentsAPIKey,
			cfg.NowPaymentsIPNSecret,
			cfg.RequiredConfirmations,
			time.Duration(cfg.HTTPTimeoutSec)*time.Second,
		)
	})

	ownerCookie := testSessionCookie(t, enabled.server, 7300, "owner", "en", enabled.server.now())
	memberCookie := testSessionCookie(t, enabled.server, 7301, "member", "en", enabled.server.now())

	createReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/plans", jsonBody(t, map[string]any{
		"service_code":       "spotify_family",
		"total_price_minor":  1800,
		"seat_limit":         2,
		"renewal_date":       "2026-04-01",
		"sharing_policy_ack": true,
		"access_mode":        "invite_seat",
	}))
	createReq.AddCookie(ownerCookie)
	createRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(createRes, createReq)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected plan create ok, got %d body=%s", createRes.Code, createRes.Body.String())
	}
	var created struct {
		Invite struct {
			InviteCode string `json:"InviteCode"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(createRes.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	joinReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/plans/join", jsonBody(t, map[string]any{
		"invite_code": created.Invite.InviteCode,
	}))
	joinReq.AddCookie(memberCookie)
	joinRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(joinRes, joinReq)
	if joinRes.Code != http.StatusOK {
		t.Fatalf("expected join ok, got %d body=%s", joinRes.Code, joinRes.Body.String())
	}
	var joined struct {
		Invoice struct {
			ID string `json:"id"`
		} `json:"invoice"`
	}
	if err := json.Unmarshal(joinRes.Body.Bytes(), &joined); err != nil {
		t.Fatalf("decode join response: %v", err)
	}

	quoteReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/invoices/"+joined.Invoice.ID+"/quote", jsonBody(t, map[string]any{
		"pay_asset": "USDC",
		"network":   "solana",
	}))
	quoteReq.AddCookie(memberCookie)
	quoteRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(quoteRes, quoteReq)
	if quoteRes.Code != http.StatusOK {
		t.Fatalf("expected quote ok, got %d body=%s", quoteRes.Code, quoteRes.Body.String())
	}
	var quoted struct {
		Invoice struct {
			QuotedPayAmount string `json:"QuotedPayAmount"`
			PaymentRef      string `json:"PaymentRef"`
		} `json:"invoice"`
		PaymentActions struct {
			CopyReference    string `json:"copyReference"`
			TelegramDeepLink string `json:"telegramDeepLink"`
		} `json:"payment_actions"`
	}
	if err := json.Unmarshal(quoteRes.Body.Bytes(), &quoted); err != nil {
		t.Fatalf("decode quote response: %v", err)
	}
	if quoted.Invoice.QuotedPayAmount != "900000" {
		t.Fatalf("expected numeric nowpayments quote to normalize to 900000, got %+v", quoted.Invoice)
	}
	if quoted.Invoice.PaymentRef != "wallet-address" {
		t.Fatalf("expected payment ref from provider, got %+v", quoted.Invoice)
	}
	if quoted.PaymentActions.CopyReference != "wallet-address" || !strings.Contains(quoted.PaymentActions.TelegramDeepLink, "startapp=invoice-"+joined.Invoice.ID) {
		t.Fatalf("expected payment_actions to mirror the quoted payment details, got %+v", quoted.PaymentActions)
	}
}

func TestPlanLookupTestEndpointRequiresE2EModeAndReturnsNewestMatch(t *testing.T) {
	t.Parallel()

	disabled := newWebHarness(t)
	operatorCookie := testSessionCookie(t, disabled.server, 9000, "operator", "en", disabled.server.now())
	disabledReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/plan-lookup", jsonBody(t, map[string]any{
		"created_after": "2026-03-12T09:59:00Z",
	}))
	disabledReq.AddCookie(operatorCookie)
	disabledRes := httptest.NewRecorder()
	disabled.server.ServeHTTP(disabledRes, disabledReq)
	if disabledRes.Code != http.StatusNotFound {
		t.Fatalf("expected endpoint hidden when e2e mode is off, got %d body=%s", disabledRes.Code, disabledRes.Body.String())
	}

	enabled := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.E2EMode = true
		cfg.WebShellEnabled = true
	})
	memberCookie := testSessionCookie(t, enabled.server, 7301, "member", "en", enabled.server.now())
	operatorCookie = testSessionCookie(t, enabled.server, 9000, "operator", "en", enabled.server.now())

	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/plan-lookup", jsonBody(t, map[string]any{
		"created_after": "2026-03-12T09:59:00Z",
	}))
	unauthorizedRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(unauthorizedRes, unauthorizedReq)
	if unauthorizedRes.Code != http.StatusUnauthorized {
		t.Fatalf("expected missing session rejected, got %d body=%s", unauthorizedRes.Code, unauthorizedRes.Body.String())
	}

	forbiddenReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/plan-lookup", jsonBody(t, map[string]any{
		"created_after": "2026-03-12T09:59:00Z",
	}))
	forbiddenReq.AddCookie(memberCookie)
	forbiddenRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(forbiddenRes, forbiddenReq)
	if forbiddenRes.Code != http.StatusForbidden {
		t.Fatalf("expected member forbidden, got %d body=%s", forbiddenRes.Code, forbiddenRes.Body.String())
	}

	owner := service.Actor{TelegramID: 7300, Username: "owner"}
	renewalDate := time.Date(2026, time.April, 1, 0, 0, 0, 0, enabled.loc)
	firstAt := time.Date(2026, time.March, 12, 10, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(2 * time.Minute)
	_, _, err := enabled.app.CreatePlan(enabled.ctx, owner, service.CreatePlanInput{
		ServiceCode:      "spotify_family",
		TotalPriceMinor:  1800,
		SeatLimit:        2,
		RenewalDate:      renewalDate,
		SharingPolicyAck: true,
		AccessMode:       "invite_seat",
	}, firstAt)
	if err != nil {
		t.Fatalf("create first plan: %v", err)
	}
	secondPlan, secondInvite, err := enabled.app.CreatePlan(enabled.ctx, owner, service.CreatePlanInput{
		ServiceCode:      "spotify_family",
		TotalPriceMinor:  1800,
		SeatLimit:        2,
		RenewalDate:      renewalDate,
		SharingPolicyAck: true,
		AccessMode:       "invite_seat",
	}, secondAt)
	if err != nil {
		t.Fatalf("create second plan: %v", err)
	}

	notFoundReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/plan-lookup", jsonBody(t, map[string]any{
		"created_after":     secondAt.Add(time.Second).Format(time.RFC3339),
		"service_code":      "spotify_family",
		"total_price_minor": 1800,
		"seat_limit":        2,
		"renewal_date":      "2026-04-01",
		"access_mode":       "invite_seat",
	}))
	notFoundReq.AddCookie(operatorCookie)
	notFoundRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(notFoundRes, notFoundReq)
	if notFoundRes.Code != http.StatusNotFound {
		t.Fatalf("expected missing plan match, got %d body=%s", notFoundRes.Code, notFoundRes.Body.String())
	}

	lookupReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/plan-lookup", jsonBody(t, map[string]any{
		"created_after":     firstAt.Add(-time.Second).Format(time.RFC3339),
		"service_code":      "spotify_family",
		"total_price_minor": 1800,
		"seat_limit":        2,
		"renewal_date":      "2026-04-01",
		"access_mode":       "invite_seat",
	}))
	lookupReq.AddCookie(operatorCookie)
	lookupRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(lookupRes, lookupReq)
	if lookupRes.Code != http.StatusOK {
		t.Fatalf("expected matching plan lookup ok, got %d body=%s", lookupRes.Code, lookupRes.Body.String())
	}
	var lookupPayload struct {
		OK     bool              `json:"ok"`
		Plan   domain.Plan       `json:"plan"`
		Invite domain.PlanInvite `json:"invite"`
	}
	if err := json.Unmarshal(lookupRes.Body.Bytes(), &lookupPayload); err != nil {
		t.Fatalf("decode lookup response: %v", err)
	}
	if !lookupPayload.OK {
		t.Fatalf("expected ok payload, got %+v", lookupPayload)
	}
	if lookupPayload.Plan.ID != secondPlan.ID {
		t.Fatalf("expected newest matching plan %s, got %+v", secondPlan.ID, lookupPayload.Plan)
	}
	if lookupPayload.Invite.InviteCode != secondInvite.InviteCode {
		t.Fatalf("expected active invite %s, got %+v", secondInvite.InviteCode, lookupPayload.Invite)
	}
}

func TestTelegramUpdateTestEndpointReusesBotFlow(t *testing.T) {
	t.Parallel()

	disabled := newWebHarness(t)
	disabledReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/telegram-update", jsonBody(t, map[string]any{
		"update_id": 1,
		"message": map[string]any{
			"message_id": 10,
			"chat": map[string]any{
				"id":   7401,
				"type": "private",
			},
			"from": map[string]any{
				"id":       7401,
				"username": "owner",
			},
			"text": "/start",
		},
	}))
	disabledRes := httptest.NewRecorder()
	disabled.server.ServeHTTP(disabledRes, disabledReq)
	if disabledRes.Code != http.StatusNotFound {
		t.Fatalf("expected endpoint hidden when e2e mode is off, got %d body=%s", disabledRes.Code, disabledRes.Body.String())
	}

	enabled := newWebHarnessWithConfig(t, func(cfg *config.Config) {
		cfg.E2EMode = true
		cfg.WebShellEnabled = true
	})
	startReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/telegram-update", jsonBody(t, map[string]any{
		"update_id": 2,
		"message": map[string]any{
			"message_id": 11,
			"chat": map[string]any{
				"id":   7401,
				"type": "private",
			},
			"from": map[string]any{
				"id":       7401,
				"username": "owner",
			},
			"text": "/start",
		},
	}))
	startRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(startRes, startReq)
	if startRes.Code != http.StatusOK {
		t.Fatalf("expected /start injection ok, got %d body=%s", startRes.Code, startRes.Body.String())
	}
	var startPayload struct {
		OK           bool `json:"ok"`
		Handled      bool `json:"handled"`
		SentMessages []struct {
			Text        string `json:"text"`
			ReplyMarkup struct {
				InlineKeyboard [][]struct {
					Text         string `json:"text"`
					CallbackData string `json:"callback_data"`
					WebApp       *struct {
						URL string `json:"url"`
					} `json:"web_app,omitempty"`
				} `json:"inline_keyboard"`
			} `json:"reply_markup"`
		} `json:"sent_messages"`
	}
	if err := json.Unmarshal(startRes.Body.Bytes(), &startPayload); err != nil {
		t.Fatalf("decode /start payload: %v", err)
	}
	if !startPayload.OK || !startPayload.Handled || len(startPayload.SentMessages) == 0 {
		t.Fatalf("expected handled /start payload, got %+v", startPayload)
	}
	if !strings.Contains(startPayload.SentMessages[0].Text, "Subscription sharing bot") {
		t.Fatalf("expected home screen text, got %s", startPayload.SentMessages[0].Text)
	}
	if webApp := startPayload.SentMessages[0].ReplyMarkup.InlineKeyboard[0][0].WebApp; webApp == nil || webApp.URL != "https://example.test/app?section=plans" {
		t.Fatalf("expected first row to launch the native Mini App, got %+v", startPayload.SentMessages[0].ReplyMarkup.InlineKeyboard)
	}
	if got := startPayload.SentMessages[0].ReplyMarkup.InlineKeyboard[1][0].CallbackData; got != "create_plan:start" {
		t.Fatalf("expected create plan button, got %s", got)
	}

	callbackReq := httptest.NewRequest(http.MethodPost, "/pixel-stack/subscription/api/v1/test/telegram-update", jsonBody(t, map[string]any{
		"update_id": 3,
		"callback_query": map[string]any{
			"id": "cb-start",
			"from": map[string]any{
				"id":       7401,
				"username": "owner",
			},
			"message": map[string]any{
				"message_id": 12,
				"chat": map[string]any{
					"id":   7401,
					"type": "private",
				},
			},
			"data": "create_plan:start",
		},
	}))
	callbackRes := httptest.NewRecorder()
	enabled.server.ServeHTTP(callbackRes, callbackReq)
	if callbackRes.Code != http.StatusOK {
		t.Fatalf("expected callback injection ok, got %d body=%s", callbackRes.Code, callbackRes.Body.String())
	}
	var callbackPayload struct {
		OK             bool `json:"ok"`
		EditedMessages []struct {
			Text string `json:"text"`
		} `json:"edited_messages"`
		CallbackAnswers []string `json:"callback_answers"`
	}
	if err := json.Unmarshal(callbackRes.Body.Bytes(), &callbackPayload); err != nil {
		t.Fatalf("decode callback payload: %v", err)
	}
	if !callbackPayload.OK || len(callbackPayload.EditedMessages) == 0 {
		t.Fatalf("expected edited callback response, got %+v", callbackPayload)
	}
	if !strings.Contains(callbackPayload.EditedMessages[0].Text, "Choose an approved subscription") {
		t.Fatalf("expected create plan prompt, got %s", callbackPayload.EditedMessages[0].Text)
	}
	if len(callbackPayload.CallbackAnswers) != 1 {
		t.Fatalf("expected one callback answer, got %+v", callbackPayload.CallbackAnswers)
	}
}

func testSessionCookie(t *testing.T, server *Server, userID int64, username string, language string, now time.Time) *http.Cookie {
	t.Helper()
	cookie, err := issueSessionCookie(server.sessionSecret, telegramAuth{
		AuthDate: now,
		User: telegramUser{
			ID:           userID,
			Username:     username,
			LanguageCode: language,
		},
	}, cookiePath(server.pathPrefix), now)
	if err != nil {
		t.Fatalf("issue session cookie: %v", err)
	}
	return cookie
}

func signedInitData(t *testing.T, botToken string, auth telegramAuth) string {
	t.Helper()
	userJSON, err := json.Marshal(auth.User)
	if err != nil {
		t.Fatalf("marshal telegram user: %v", err)
	}
	values := url.Values{}
	values.Set("auth_date", strconv.FormatInt(auth.AuthDate.Unix(), 10))
	values.Set("query_id", auth.QueryID)
	values.Set("user", string(userJSON))
	lines := []string{
		"auth_date=" + values.Get("auth_date"),
		"query_id=" + values.Get("query_id"),
		"user=" + values.Get("user"),
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	hash := hmacSHA256(secret, []byte(strings.Join(lines, "\n")))
	values.Set("hash", fmt.Sprintf("%x", hash))
	return values.Encode()
}

func jsonBody(t *testing.T, payload any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal json body: %v", err)
	}
	return bytes.NewReader(raw)
}
