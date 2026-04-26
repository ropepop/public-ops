package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"subscriptionbot/internal/config"
	"subscriptionbot/internal/domain"
	"subscriptionbot/internal/service"
)

//go:embed static/*
var staticFS embed.FS

type notificationDispatcher interface {
	DispatchNotifications(ctx context.Context, notifications []domain.Notification) error
}

type Server struct {
	cfg           config.Config
	app           *service.App
	loc           *time.Location
	now           func() time.Time
	pathPrefix    string
	sessionSecret []byte
	notifier      notificationDispatcher
	static        fs.FS
	release       releaseInfo
	pageTemplate  *template.Template
}

type pageData struct {
	Title           string
	ModeName        string
	APIBasePathJS   string
	RouteBasePathJS string
	PublicBaseURLJS string
	ModeJS          string
	AppCSSURL       string
	AppJSURL        string
}

type bootstrapSession struct {
	UserID            int64  `json:"userId"`
	Username          string `json:"username"`
	Language          string `json:"language"`
	IsOperator        bool   `json:"isOperator"`
	CanManageRenewals bool   `json:"canManageRenewals"`
}

type bootstrapPayments struct {
	DefaultAsset    string              `json:"defaultAsset"`
	DefaultNetwork  string              `json:"defaultNetwork"`
	AllowedAssets   []string            `json:"allowedAssets"`
	NetworksByAsset map[string][]string `json:"networksByAsset"`
	Provider        string              `json:"provider"`
	SimulateEnabled bool                `json:"simulateEnabled"`
}

type bootstrapLaunch struct {
	BotUsername            string `json:"botUsername"`
	TelegramBotURL         string `json:"telegramBotUrl,omitempty"`
	AppURL                 string `json:"appUrl"`
	AdminURL               string `json:"adminUrl"`
	DirectBrowserSupported bool   `json:"directBrowserSupported"`
	StartApp               string `json:"startApp,omitempty"`
	Section                string `json:"section"`
	AdminView              string `json:"adminView,omitempty"`
	PlanID                 string `json:"planId,omitempty"`
	InvoiceID              string `json:"invoiceId,omitempty"`
	InviteCode             string `json:"inviteCode,omitempty"`
}

type invoicePaymentActions struct {
	CopyReference      string `json:"copyReference,omitempty"`
	CopyQuotedAmount   string `json:"copyQuotedAmount,omitempty"`
	CopyPaymentDetails string `json:"copyPaymentDetails,omitempty"`
	ShareText          string `json:"shareText,omitempty"`
	TelegramDeepLink   string `json:"telegramDeepLink,omitempty"`
}

type launchLinkInfo struct {
	StartApp         string `json:"startApp"`
	AppURL           string `json:"appUrl"`
	TelegramDeepLink string `json:"telegramDeepLink,omitempty"`
}

type bootstrapPayload struct {
	Session        bootstrapSession                 `json:"session"`
	Launch         bootstrapLaunch                  `json:"launch"`
	Payments       bootstrapPayments                `json:"payments"`
	Catalog        []domain.ServiceCatalogEntry     `json:"catalog"`
	Plans          []domain.PlanView                `json:"plans"`
	LatestInvoice  *domain.Invoice                  `json:"latestInvoice,omitempty"`
	InvoiceActions map[string]invoicePaymentActions `json:"invoiceActions,omitempty"`
	Admin          *domain.AdminOverview            `json:"admin,omitempty"`
}

func NewServer(cfg config.Config, app *service.App, loc *time.Location) (*Server, error) {
	pathPrefix := "/pixel-stack/subscription"
	if strings.TrimSpace(cfg.WebPublicBaseURL) != "" {
		parsed, err := url.Parse(cfg.WebPublicBaseURL)
		if err != nil {
			return nil, fmt.Errorf("parse SUBSCRIPTION_BOT_WEB_PUBLIC_BASE_URL: %w", err)
		}
		if strings.TrimSpace(parsed.Path) == "" || parsed.Path == "/" {
			pathPrefix = ""
		} else {
			pathPrefix = strings.TrimRight(parsed.Path, "/")
		}
	}

	staticFiles := mustStaticSubFS()
	release, err := newReleaseInfo(staticFiles)
	if err != nil {
		return nil, err
	}

	server := &Server{
		cfg:        cfg,
		app:        app,
		loc:        loc,
		now:        time.Now,
		pathPrefix: pathPrefix,
		static:     staticFiles,
		release:    release,
		pageTemplate: template.Must(template.New("shell").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="{{.AppCSSURL}}">
  <script>
    window.SUBSCRIPTION_APP_CONFIG = {
      apiBasePath: {{.APIBasePathJS}},
      routeBasePath: {{.RouteBasePathJS}},
      publicBaseURL: {{.PublicBaseURLJS}},
      mode: {{.ModeJS}}
    };
  </script>
  <script src="https://telegram.org/js/telegram-web-app.js"></script>
  <script defer src="{{.AppJSURL}}"></script>
</head>
<body data-mode="{{.ModeName}}">
  <div id="app"></div>
  <noscript>This Mini App needs JavaScript enabled.</noscript>
</body>
</html>`)),
	}
	if cfg.WebEnabled {
		secret, err := loadSessionSecret(cfg.SessionSecretFile)
		if err != nil {
			return nil, err
		}
		server.sessionSecret = secret
	}
	return server, nil
}

func mustStaticSubFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}

func (s *Server) AppURL() string {
	if !s.cfg.WebEnabled || !s.cfg.WebShellEnabled {
		return ""
	}
	base := s.publicOriginURL()
	if base == "" {
		base = strings.TrimRight(s.cfg.WebPublicBaseURL, "/")
	}
	return strings.TrimRight(base, "/") + "/app"
}

func (s *Server) AdminURL() string {
	if !s.cfg.WebEnabled || !s.cfg.WebShellEnabled {
		return ""
	}
	base := s.publicOriginURL()
	if base == "" {
		base = strings.TrimRight(s.cfg.WebPublicBaseURL, "/")
	}
	return strings.TrimRight(base, "/") + "/admin"
}

func (s *Server) publicOriginURL() string {
	raw := strings.TrimSpace(s.cfg.WebPublicBaseURL)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(parsed.Host) == "" {
		return strings.TrimRight(raw, "/")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/")
}

func (s *Server) telegramBotUsername() string {
	return strings.TrimPrefix(strings.TrimSpace(s.cfg.TelegramBotUsername), "@")
}

func (s *Server) telegramBotURL() string {
	if username := s.telegramBotUsername(); username != "" {
		return "https://t.me/" + username
	}
	return ""
}

func (s *Server) telegramStartAppURL(startApp string) string {
	botURL := s.telegramBotURL()
	if botURL == "" {
		return ""
	}
	if strings.TrimSpace(startApp) == "" {
		return botURL
	}
	return botURL + "?startapp=" + url.QueryEscape(strings.TrimSpace(startApp))
}

func (s *Server) directAppLaunchURL(section string, planID string, invoiceID string, inviteCode string, adminView string, startApp string) string {
	base := s.AppURL()
	if base == "" {
		return ""
	}
	values := url.Values{}
	if section = strings.TrimSpace(section); section != "" {
		values.Set("section", section)
	}
	if planID = strings.TrimSpace(planID); planID != "" {
		values.Set("plan_id", planID)
	}
	if invoiceID = strings.TrimSpace(invoiceID); invoiceID != "" {
		values.Set("invoice_id", invoiceID)
	}
	if inviteCode = strings.TrimSpace(inviteCode); inviteCode != "" {
		values.Set("invite_code", inviteCode)
	}
	if adminView = strings.TrimSpace(adminView); adminView != "" {
		values.Set("admin_view", adminView)
	}
	if startApp = strings.TrimSpace(startApp); startApp != "" {
		values.Set("startapp", startApp)
	}
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

func (s *Server) SetNotifier(notifier notificationDispatcher) {
	s.notifier = notifier
}

func (s *Server) Enabled() bool {
	return s.cfg.WebEnabled
}

func (s *Server) Run(ctx context.Context) error {
	if !s.cfg.WebEnabled {
		return nil
	}
	httpServer := &http.Server{
		Addr:              net.JoinHostPort(s.cfg.WebBindAddr, strconv.Itoa(s.cfg.WebPort)),
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen subscription web server: %w", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) || err == nil {
			return nil
		}
		return err
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	basePath := strings.TrimRight(s.pathPrefix, "/")
	path := strings.TrimRight(strings.TrimSpace(r.URL.Path), "/")
	if path == "" {
		path = "/"
	}
	s.setReleaseHeaders(w)
	switch {
	case path == basePath || path == "/":
		if !s.cfg.WebShellEnabled {
			http.NotFound(w, r)
			return
		}
		routeBasePath := basePath
		if path == "/" {
			routeBasePath = ""
		}
		s.serveShell(w, "Subscription Mini App", "launcher", routeBasePath)
	case path == "/app":
		if !s.cfg.WebShellEnabled {
			http.NotFound(w, r)
			return
		}
		s.serveShell(w, "Subscription Mini App", "app", "")
	case path == "/admin":
		if !s.cfg.WebShellEnabled {
			http.NotFound(w, r)
			return
		}
		s.serveShell(w, "Subscription Admin", "admin", "")
	case path == basePath+"/app":
		if !s.cfg.WebShellEnabled {
			http.NotFound(w, r)
			return
		}
		s.serveShell(w, "Subscription Mini App", "app", basePath)
	case path == basePath+"/admin":
		if !s.cfg.WebShellEnabled {
			http.NotFound(w, r)
			return
		}
		s.serveShell(w, "Subscription Admin", "admin", basePath)
	case strings.HasPrefix(path, basePath+"/assets/"):
		if !s.cfg.WebShellEnabled {
			http.NotFound(w, r)
			return
		}
		s.serveAsset(w, r, basePath)
	case strings.HasPrefix(path, basePath+"/api/v1/"):
		s.handleAPI(w, r, strings.TrimPrefix(path, basePath+"/api/v1"))
	default:
		http.NotFound(w, r)
	}
}

func normalizeSection(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "plans", "join", "invoice", "ledger", "support", "admin":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func normalizeAdminView(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "overview", "support", "issues", "alerts", "plans", "denylist":
		return strings.TrimSpace(strings.ToLower(value))
	default:
		return ""
	}
}

func parseStartApp(raw string) bootstrapLaunch {
	startApp := strings.TrimSpace(raw)
	launch := bootstrapLaunch{StartApp: startApp}
	switch {
	case strings.HasPrefix(startApp, "join-"):
		launch.Section = "join"
		launch.InviteCode = strings.TrimPrefix(startApp, "join-")
	case strings.HasPrefix(startApp, "plan-"):
		launch.Section = "plans"
		launch.PlanID = strings.TrimPrefix(startApp, "plan-")
	case strings.HasPrefix(startApp, "invoice-"):
		launch.Section = "invoice"
		launch.InvoiceID = strings.TrimPrefix(startApp, "invoice-")
	case startApp == "admin-support":
		launch.Section = "admin"
		launch.AdminView = "support"
	case startApp == "admin-overview":
		launch.Section = "admin"
		launch.AdminView = "overview"
	case startApp == "admin-issues":
		launch.Section = "admin"
		launch.AdminView = "issues"
	case startApp == "admin-alerts":
		launch.Section = "admin"
		launch.AdminView = "alerts"
	case startApp == "admin-plans":
		launch.Section = "admin"
		launch.AdminView = "plans"
	case startApp == "admin-denylist":
		launch.Section = "admin"
		launch.AdminView = "denylist"
	}
	return launch
}

func (s *Server) launchFromRequest(r *http.Request) bootstrapLaunch {
	mode := normalizeSection(r.URL.Query().Get("mode"))
	if mode == "" {
		mode = strings.TrimSpace(strings.ToLower(r.URL.Query().Get("mode")))
	}
	launch := parseStartApp(r.URL.Query().Get("startapp"))
	if section := normalizeSection(r.URL.Query().Get("section")); section != "" {
		launch.Section = section
	}
	if adminView := normalizeAdminView(r.URL.Query().Get("admin_view")); adminView != "" {
		launch.AdminView = adminView
	}
	if planID := strings.TrimSpace(r.URL.Query().Get("plan_id")); planID != "" {
		launch.PlanID = planID
	}
	if invoiceID := strings.TrimSpace(r.URL.Query().Get("invoice_id")); invoiceID != "" {
		launch.InvoiceID = invoiceID
	}
	if inviteCode := strings.TrimSpace(r.URL.Query().Get("invite_code")); inviteCode != "" {
		launch.InviteCode = inviteCode
	}
	if launch.Section == "" {
		switch {
		case launch.AdminView != "":
			launch.Section = "admin"
		case launch.InviteCode != "":
			launch.Section = "join"
		case launch.InvoiceID != "":
			launch.Section = "invoice"
		case mode == "admin":
			launch.Section = "admin"
		default:
			launch.Section = "plans"
		}
	}
	if launch.Section == "admin" && launch.AdminView == "" {
		launch.AdminView = "overview"
	}
	return launch
}

func (s *Server) normalizeLaunchContext(ctx context.Context, actor service.Actor, requestLaunch bootstrapLaunch, latestInvoice *domain.Invoice) bootstrapLaunch {
	launch := requestLaunch
	launch.BotUsername = s.telegramBotUsername()
	launch.TelegramBotURL = s.telegramBotURL()
	launch.AppURL = s.AppURL()
	launch.AdminURL = s.AdminURL()
	launch.DirectBrowserSupported = true

	if launch.InvoiceID != "" && launch.PlanID == "" {
		if invoice, err := s.app.LoadInvoiceByID(ctx, actor, launch.InvoiceID); err == nil {
			launch.PlanID = invoice.PlanID
		}
	}
	if launch.InvoiceID == "" && latestInvoice != nil && launch.Section == "invoice" {
		launch.InvoiceID = latestInvoice.ID
		if launch.PlanID == "" {
			launch.PlanID = latestInvoice.PlanID
		}
	}
	return launch
}

func (s *Server) invoiceActionsMap(latestInvoice *domain.Invoice, plans []domain.PlanView) map[string]invoicePaymentActions {
	out := make(map[string]invoicePaymentActions)
	appendInvoice := func(invoice *domain.Invoice) {
		if invoice == nil || strings.TrimSpace(invoice.ID) == "" {
			return
		}
		actions, ok := s.paymentActionsForInvoice(*invoice)
		if !ok {
			return
		}
		out[invoice.ID] = actions
	}
	appendInvoice(latestInvoice)
	for _, view := range plans {
		appendInvoice(view.OpenInvoice)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Server) paymentActionsForInvoice(invoice domain.Invoice) (invoicePaymentActions, bool) {
	actions := invoicePaymentActions{}
	if ref := strings.TrimSpace(invoice.PaymentRef); ref != "" {
		actions.CopyReference = ref
	}
	if amount := strings.TrimSpace(invoice.QuotedPayAmount); amount != "" {
		actions.CopyQuotedAmount = amount
	}
	if invoice.ID != "" && actions.CopyReference != "" {
		lines := []string{
			fmt.Sprintf("Invoice %s", invoice.ID),
			fmt.Sprintf("Quoted amount: %s %s", strings.TrimSpace(invoice.QuotedPayAmount), strings.TrimSpace(invoice.PayAsset)),
			fmt.Sprintf("Network: %s", strings.TrimSpace(invoice.Network)),
			fmt.Sprintf("Reference: %s", actions.CopyReference),
		}
		if deepLink := s.telegramStartAppURL("invoice-" + invoice.ID); deepLink != "" {
			lines = append(lines, "Open in app: "+deepLink)
			actions.TelegramDeepLink = deepLink
		}
		actions.CopyPaymentDetails = strings.Join(lines, "\n")
		actions.ShareText = actions.CopyPaymentDetails
	}
	if actions.CopyReference == "" && actions.CopyQuotedAmount == "" && actions.CopyPaymentDetails == "" {
		return invoicePaymentActions{}, false
	}
	return actions, true
}

func (s *Server) inviteLaunch(planID string, inviteCode string) launchLinkInfo {
	startApp := "join-" + strings.TrimSpace(inviteCode)
	return launchLinkInfo{
		StartApp:         startApp,
		AppURL:           s.directAppLaunchURL("join", planID, "", inviteCode, "", startApp),
		TelegramDeepLink: s.telegramStartAppURL(startApp),
	}
}

func (s *Server) serveShell(w http.ResponseWriter, title string, mode string, routeBasePath string) {
	basePath := strings.TrimRight(s.pathPrefix, "/")
	data := pageData{
		Title:           title,
		ModeName:        mode,
		APIBasePathJS:   basePath,
		RouteBasePathJS: strings.TrimRight(routeBasePath, "/"),
		PublicBaseURLJS: strings.TrimRight(s.cfg.WebPublicBaseURL, "/"),
		ModeJS:          mode,
		AppCSSURL:       s.release.AssetURL(basePath, "app.css"),
		AppJSURL:        s.release.AssetURL(basePath, "app.js"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.setNoStoreHeaders(w)
	_ = s.pageTemplate.Execute(w, data)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request, route string) {
	now := s.now()
	switch {
	case route == "/health":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "subscription_bot"})
	case route == "/payments/webhook/nowpayments" && r.Method == http.MethodPost:
		s.handleNowPaymentsWebhook(w, r, now)
	case route == "/auth/telegram" && r.Method == http.MethodPost:
		s.handleAuthTelegram(w, r, now)
	case route == "/session":
		s.handleSession(w, r, now)
	case route == "/bootstrap" && r.Method == http.MethodGet:
		s.handleBootstrap(w, r, now)
	case route == "/catalog":
		s.handleCatalog(w, r, now)
	case route == "/plans" && r.Method == http.MethodGet:
		s.handlePlansList(w, r, now)
	case route == "/plans" && r.Method == http.MethodPost:
		s.handlePlanCreate(w, r, now)
	case route == "/plans/join" && r.Method == http.MethodPost:
		s.handlePlanJoin(w, r, now)
	case strings.HasPrefix(route, "/plans/") && strings.HasSuffix(route, "/invite") && r.Method == http.MethodPost:
		planID := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(route, "/plans/"), "/invite"), "/")
		s.handlePlanInvite(w, r, now, planID)
	case strings.HasPrefix(route, "/plans/") && strings.HasSuffix(route, "/members") && r.Method == http.MethodGet:
		planID := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(route, "/plans/"), "/members"), "/")
		s.handlePlanMembers(w, r, now, planID)
	case route == "/ledger" && r.Method == http.MethodGet:
		s.handleLedger(w, r, now)
	case route == "/invoices/latest" && r.Method == http.MethodGet:
		s.handleLatestInvoice(w, r, now)
	case route == "/invoices/latest/quote" && r.Method == http.MethodPost:
		s.handleLatestInvoiceQuote(w, r, now)
	case strings.HasPrefix(route, "/invoices/") && strings.HasSuffix(route, "/quote") && r.Method == http.MethodPost:
		invoiceID := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(route, "/invoices/"), "/quote"), "/")
		s.handleInvoiceQuote(w, r, now, invoiceID)
	case strings.HasPrefix(route, "/invoices/") && strings.HasSuffix(route, "/simulate") && r.Method == http.MethodPost:
		invoiceID := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(route, "/invoices/"), "/simulate"), "/")
		s.handleInvoiceSimulate(w, r, now, invoiceID)
	case route == "/support" && r.Method == http.MethodPost:
		s.handleSupport(w, r, now)
	case route == "/admin/overview" && r.Method == http.MethodGet:
		s.handleAdminOverview(w, r, now)
	case route == "/admin/support" && r.Method == http.MethodGet:
		s.handleAdminSupport(w, r, now)
	case strings.HasPrefix(route, "/admin/support/") && strings.HasSuffix(route, "/resolve") && r.Method == http.MethodPost:
		ticketID := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(route, "/admin/support/"), "/resolve"), "/")
		s.handleAdminSupportResolve(w, r, now, ticketID)
	case route == "/admin/issues" && r.Method == http.MethodGet:
		s.handleAdminIssues(w, r, now)
	case route == "/admin/recent-plans" && r.Method == http.MethodGet:
		s.handleAdminRecentPlans(w, r, now)
	case route == "/admin/reimbursements" && r.Method == http.MethodGet:
		s.handleAdminReimbursements(w, r, now)
	case route == "/admin/payment-alerts" && r.Method == http.MethodGet:
		s.handleAdminPaymentAlerts(w, r, now)
	case route == "/admin/denylist" && r.Method == http.MethodGet:
		s.handleAdminDenylist(w, r, now)
	case route == "/admin/denylist/block-user" && r.Method == http.MethodPost:
		s.handleAdminBlockUser(w, r, now)
	case route == "/test/telegram-update" && r.Method == http.MethodPost:
		s.handleTestTelegramUpdate(w, r, now)
	case route == "/test/plan-lookup" && r.Method == http.MethodPost:
		s.handleTestPlanLookup(w, r, now)
	case route == "/test/process-cycle" && r.Method == http.MethodPost:
		s.handleTestProcessCycle(w, r, now)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleNowPaymentsWebhook(w http.ResponseWriter, r *http.Request, now time.Time) {
	hostedProvider, ok := s.app.HostedProvider()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, fmt.Errorf("hosted payment callbacks are not enabled"))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("read request body: %w", err))
		return
	}
	event, err := hostedProvider.ParseWebhookEvent(r.Header, body, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(event.ProviderName) == "" {
		event.ProviderName = "nowpayments"
	}
	if !strings.EqualFold(event.ProviderName, "nowpayments") {
		writeError(w, http.StatusBadRequest, fmt.Errorf("unexpected payment provider in webhook payload"))
		return
	}
	notifications, duplicate, invoiceFound, err := s.app.ProcessProviderWebhookEvent(r.Context(), event, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.emitNotifications(r.Context(), notifications)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"provider":          "nowpayments",
		"external_event_id": event.ExternalEventID,
		"provider_invoice":  event.ProviderInvoiceID,
		"duplicate":         duplicate,
		"invoice_found":     invoiceFound,
		"notifications":     notifications,
	})
}

func (s *Server) handleAuthTelegram(w http.ResponseWriter, r *http.Request, now time.Time) {
	var body struct {
		InitData string `json:"initData"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	auth, err := validateTelegramInitData(body.InitData, s.cfg.BotToken, time.Duration(s.cfg.TelegramAuthMaxAgeSec)*time.Second, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	cookie, err := issueSessionCookie(s.sessionSecret, auth, cookiePath(s.pathPrefix), now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"telegram": auth.User.ID,
		"username": auth.User.Username,
	})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	actor := actorFromClaims(claims)
	_, err = s.app.ListUserPlans(r.Context(), actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	canManageRenewals, err := s.app.CanManageRenewals(r.Context(), actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defaultAsset, defaultNetwork := s.app.DefaultPayOptions()
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":           claims.UserID,
		"username":          claims.Username,
		"language":          claims.Language,
		"isOperator":        s.app.IsOperator(claims.UserID),
		"canManageRenewals": canManageRenewals,
		"defaultPayAsset":   defaultAsset,
		"defaultPayNetwork": defaultNetwork,
		"simulateEnabled":   s.app.CanSimulatePayments(),
	})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	actor := actorFromClaims(claims)
	catalog, err := s.app.ListCatalog(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	plans, err := s.app.ListUserPlans(r.Context(), actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	latestInvoice, err := s.app.LatestInvoice(r.Context(), actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	canManageRenewals, err := s.app.CanManageRenewals(r.Context(), actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defaultAsset, defaultNetwork := s.app.DefaultPayOptions()
	networksByAsset := make(map[string][]string)
	for _, asset := range s.app.AllowedPayAssets() {
		networksByAsset[asset] = s.app.AllowedNetworksForAsset(asset)
	}
	launch := s.normalizeLaunchContext(r.Context(), actor, s.launchFromRequest(r), latestInvoice)
	payload := bootstrapPayload{
		Launch: launch,
		Session: bootstrapSession{
			UserID:            claims.UserID,
			Username:          claims.Username,
			Language:          claims.Language,
			IsOperator:        s.app.IsOperator(claims.UserID),
			CanManageRenewals: canManageRenewals,
		},
		Payments: bootstrapPayments{
			DefaultAsset:    defaultAsset,
			DefaultNetwork:  defaultNetwork,
			AllowedAssets:   s.app.AllowedPayAssets(),
			NetworksByAsset: networksByAsset,
			Provider:        s.app.PaymentProviderName(),
			SimulateEnabled: s.app.CanSimulatePayments(),
		},
		Catalog:        catalog,
		Plans:          plans,
		LatestInvoice:  latestInvoice,
		InvoiceActions: s.invoiceActionsMap(latestInvoice, plans),
	}
	if payload.Session.IsOperator {
		overview, err := s.app.AdminOverview(r.Context(), actor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		payload.Admin = &overview
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request, now time.Time) {
	if _, err := s.requireSession(r, now); err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	catalog, err := s.app.ListCatalog(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalog": catalog})
}

func (s *Server) handlePlansList(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	plans, err := s.app.ListUserPlans(r.Context(), actorFromClaims(claims))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (s *Server) handlePlanCreate(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		ServiceCode      string `json:"service_code"`
		TotalPriceMinor  int64  `json:"total_price_minor"`
		SeatLimit        int    `json:"seat_limit"`
		RenewalDate      string `json:"renewal_date"`
		AccessMode       string `json:"access_mode"`
		SharingPolicyAck bool   `json:"sharing_policy_ack"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	renewalDate, err := time.Parse("2006-01-02", strings.TrimSpace(body.RenewalDate))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("renewal_date must be YYYY-MM-DD"))
		return
	}
	plan, invite, err := s.app.CreatePlan(r.Context(), actorFromClaims(claims), service.CreatePlanInput{
		ServiceCode:      body.ServiceCode,
		TotalPriceMinor:  body.TotalPriceMinor,
		SeatLimit:        body.SeatLimit,
		RenewalDate:      renewalDate,
		SharingPolicyAck: body.SharingPolicyAck,
		AccessMode:       body.AccessMode,
	}, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"plan":   plan,
		"invite": invite,
		"launch": s.inviteLaunch(plan.ID, invite.InviteCode),
	})
}

func (s *Server) handlePlanJoin(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		InviteCode string `json:"invite_code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	membership, invoice, err := s.app.JoinPlan(r.Context(), actorFromClaims(claims), body.InviteCode, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"membership": membership, "invoice": invoice})
}

func (s *Server) handlePlanInvite(w http.ResponseWriter, r *http.Request, now time.Time, planID string) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	invite, err := s.app.CreateInvite(r.Context(), actorFromClaims(claims), planID, now)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"invite": invite,
		"launch": s.inviteLaunch(planID, invite.InviteCode),
	})
}

func (s *Server) handlePlanMembers(w http.ResponseWriter, r *http.Request, now time.Time, planID string) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	members, err := s.app.ListPlanMembers(r.Context(), actorFromClaims(claims), planID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (s *Server) handleLedger(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	planID := strings.TrimSpace(r.URL.Query().Get("plan_id"))
	if planID == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("plan_id is required"))
		return
	}
	ledger, err := s.app.Ledger(r.Context(), actorFromClaims(claims), planID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, ledger)
}

func (s *Server) handleLatestInvoice(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	invoice, err := s.app.LatestInvoice(r.Context(), actorFromClaims(claims))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoice": invoice})
}

func (s *Server) handleLatestInvoiceQuote(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		PayAsset string `json:"pay_asset"`
		Network  string `json:"network"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	invoice, err := s.app.QuoteLatestInvoice(r.Context(), actorFromClaims(claims), body.PayAsset, body.Network, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	response := map[string]any{"invoice": invoice}
	if actions, ok := s.paymentActionsForInvoice(invoice); ok {
		response["payment_actions"] = actions
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleInvoiceQuote(w http.ResponseWriter, r *http.Request, now time.Time, invoiceID string) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		PayAsset string `json:"pay_asset"`
		Network  string `json:"network"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	invoice, err := s.app.QuoteInvoice(r.Context(), actorFromClaims(claims), invoiceID, body.PayAsset, body.Network, now)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	response := map[string]any{"invoice": invoice}
	if actions, ok := s.paymentActionsForInvoice(invoice); ok {
		response["payment_actions"] = actions
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleInvoiceSimulate(w http.ResponseWriter, r *http.Request, now time.Time, invoiceID string) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		AmountAtomic string `json:"amount_atomic"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.app.SimulateInvoicePayment(r.Context(), actorFromClaims(claims), invoiceID, body.AmountAtomic, now); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSupport(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		PlanID  string `json:"plan_id"`
		Message string `json:"message"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ticket, err := s.app.OpenSupportTicket(r.Context(), actorFromClaims(claims), body.PlanID, body.Message, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ticket": ticket})
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	overview, err := s.app.AdminOverview(r.Context(), actorFromClaims(claims))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleAdminSupport(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	tickets, err := s.app.ListOpenSupportTickets(r.Context(), actorFromClaims(claims))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (s *Server) handleAdminSupportResolve(w http.ResponseWriter, r *http.Request, now time.Time, ticketID string) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	ticket, err := s.app.ResolveSupportTicket(r.Context(), actorFromClaims(claims), ticketID, body.Note, now)
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, service.ErrUnauthorized):
			status = http.StatusForbidden
		case errors.Is(err, service.ErrNotFound):
			status = http.StatusNotFound
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (s *Server) handleAdminIssues(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	issues, err := s.app.ListRenewalIssues(r.Context(), actorFromClaims(claims))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": issues})
}

func (s *Server) handleAdminRecentPlans(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	plans, err := s.app.ListRecentPlans(r.Context(), actorFromClaims(claims), 12)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (s *Server) handleAdminReimbursements(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	items, err := s.app.ListOwnerReimbursementsDue(r.Context(), actorFromClaims(claims), 12)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reimbursements": items})
}

func (s *Server) handleAdminPaymentAlerts(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	items, err := s.app.ListRecentPaymentAlerts(r.Context(), actorFromClaims(claims), 12)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": items})
}

func (s *Server) handleAdminDenylist(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	items, err := s.app.ListDenylistEntries(r.Context(), actorFromClaims(claims), 24)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": items})
}

func (s *Server) handleAdminBlockUser(w http.ResponseWriter, r *http.Request, now time.Time) {
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	var body struct {
		TelegramID int64  `json:"telegram_id"`
		Reason     string `json:"reason"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.TelegramID <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("telegram_id must be a positive integer"))
		return
	}
	entry, err := s.app.AddDenylistEntry(r.Context(), actorFromClaims(claims), "telegram_id", strconv.FormatInt(body.TelegramID, 10), body.Reason, now)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"entry": entry})
}

func (s *Server) handleTestProcessCycle(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.cfg.E2EMode {
		http.NotFound(w, r)
		return
	}
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	if !s.app.IsOperator(claims.UserID) {
		writeError(w, http.StatusForbidden, service.ErrUnauthorized)
		return
	}
	var body struct {
		At string `json:"at"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	runAt, err := time.Parse(time.RFC3339, strings.TrimSpace(body.At))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("at must be RFC3339"))
		return
	}
	notifications, err := s.app.ProcessCycle(r.Context(), runAt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.emitNotifications(r.Context(), notifications)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"processed_at":  runAt.Format(time.RFC3339),
		"notifications": notifications,
	})
}

func (s *Server) handleTestPlanLookup(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.cfg.E2EMode {
		http.NotFound(w, r)
		return
	}
	claims, err := s.requireSession(r, now)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err)
		return
	}
	actor := actorFromClaims(claims)
	if !s.app.IsOperator(claims.UserID) {
		writeError(w, http.StatusForbidden, service.ErrUnauthorized)
		return
	}
	var body struct {
		CreatedAfter    string `json:"created_after"`
		ServiceCode     string `json:"service_code"`
		TotalPriceMinor int64  `json:"total_price_minor"`
		SeatLimit       int    `json:"seat_limit"`
		RenewalDate     string `json:"renewal_date"`
		AccessMode      string `json:"access_mode"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	createdAfter, err := time.Parse(time.RFC3339, strings.TrimSpace(body.CreatedAfter))
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("created_after must be RFC3339"))
		return
	}
	renewalDate := ""
	if strings.TrimSpace(body.RenewalDate) != "" {
		parsedRenewalDate, err := time.Parse("2006-01-02", strings.TrimSpace(body.RenewalDate))
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("renewal_date must be YYYY-MM-DD"))
			return
		}
		renewalDate = parsedRenewalDate.Format("2006-01-02")
	}
	plans, err := s.app.ListRecentPlans(r.Context(), actor, 50)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeError(w, status, err)
		return
	}
	for _, plan := range plans {
		if plan.Status != domain.PlanStatusActive {
			continue
		}
		if plan.CreatedAt.Before(createdAfter) {
			continue
		}
		if serviceCode := strings.TrimSpace(body.ServiceCode); serviceCode != "" && plan.ServiceCode != serviceCode {
			continue
		}
		if body.TotalPriceMinor > 0 && plan.TotalPriceMinor != body.TotalPriceMinor {
			continue
		}
		if body.SeatLimit > 0 && plan.SeatLimit != body.SeatLimit {
			continue
		}
		if renewalDate != "" && plan.RenewalDate.Format("2006-01-02") != renewalDate {
			continue
		}
		if accessMode := strings.TrimSpace(body.AccessMode); accessMode != "" && plan.AccessMode != accessMode {
			continue
		}
		invite, err := s.app.ActiveInviteForPlan(r.Context(), actor, plan.ID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, service.ErrUnauthorized) {
				status = http.StatusForbidden
			} else if errors.Is(err, service.ErrNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"plan":   plan,
			"invite": invite,
		})
		return
	}
	writeError(w, http.StatusNotFound, service.ErrNotFound)
}

func (s *Server) requireSession(r *http.Request, now time.Time) (sessionClaims, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return sessionClaims{}, fmt.Errorf("missing session cookie")
	}
	return parseSession(s.sessionSecret, cookie.Value, now)
}

func actorFromClaims(claims sessionClaims) service.Actor {
	return service.Actor{
		TelegramID: claims.UserID,
		Username:   claims.Username,
	}
}

func decodeJSON(r *http.Request, target any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(bytesTrimSpace(body)) == 0 {
		return fmt.Errorf("request body is empty")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoStoreHeaders(w)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{"error": err.Error()})
}

func cookiePath(prefix string) string {
	value := strings.TrimSpace(strings.TrimRight(prefix, "/"))
	if value == "" {
		return "/"
	}
	return value
}

func (s *Server) emitNotifications(ctx context.Context, notifications []domain.Notification) {
	if s.notifier == nil || len(notifications) == 0 {
		return
	}
	_ = s.notifier.DispatchNotifications(ctx, notifications)
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, basePath string) {
	assetPath := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, basePath), "/assets/")
	version := strings.TrimSpace(r.URL.Query().Get("v"))
	if version != "" && version == s.release.AssetHash(assetPath) {
		s.setImmutableHeaders(w)
	} else {
		s.setNoStoreHeaders(w)
	}
	http.StripPrefix(basePath+"/assets/", http.FileServer(http.FS(s.static))).ServeHTTP(w, r)
}

func (s *Server) setReleaseHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Subscription-Bot-Commit", s.release.Commit)
	w.Header().Set("X-Subscription-Bot-Build-Time", s.release.BuildTime)
	w.Header().Set("X-Subscription-Bot-Dirty", s.release.Dirty)
	w.Header().Set("X-Subscription-Bot-Instance", s.release.Instance)
	w.Header().Set("X-Subscription-Bot-App-Js", s.release.AppJSHash)
}

func (s *Server) setNoStoreHeaders(w http.ResponseWriter) {
	setNoStoreHeaders(w)
}

func setNoStoreHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
}

func (s *Server) setImmutableHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("CDN-Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=31536000, immutable")
}
