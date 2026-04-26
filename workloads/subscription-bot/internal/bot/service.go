package bot

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"subscriptionbot/internal/config"
	"subscriptionbot/internal/domain"
	"subscriptionbot/internal/service"
	"subscriptionbot/internal/telegram"
	appversion "subscriptionbot/internal/version"
)

const (
	plansPageSize = 5

	flowCreatePlan = "create_plan"
	flowJoin       = "join_plan"
	flowSupport    = "support_ticket"
	flowAdminBlock = "admin_block"
)

type messageClient interface {
	GetUpdates(ctx context.Context, offset int64, timeout int) ([]telegram.Update, error)
	SendMessage(ctx context.Context, chatID int64, text string, opts telegram.MessageOptions) error
	EditMessageText(ctx context.Context, chatID int64, messageID int64, text string, opts telegram.MessageOptions) error
	AnswerCallbackQuery(ctx context.Context, callbackQueryID string, text string) error
	SetMyCommands(ctx context.Context, commands []telegram.BotCommand) error
	SetChatMenuButton(ctx context.Context, button telegram.MenuButton) error
}

type createPlanState struct {
	ServiceCode       string `json:"service_code"`
	ServiceName       string `json:"service_name"`
	AccessMode        string `json:"access_mode"`
	SharingPolicyNote string `json:"sharing_policy_note"`
	TotalPriceMinor   int64  `json:"total_price_minor"`
	SeatLimit         int    `json:"seat_limit"`
	RenewalDate       string `json:"renewal_date"`
}

type supportState struct {
	PlanID string `json:"plan_id"`
}

type adminBlockState struct {
	TelegramID string `json:"telegram_id"`
}

type actionLimit struct {
	window time.Duration
	max    int
}

type actionLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

type Service struct {
	client            messageClient
	app               *service.App
	pollTimeout       int
	defaultPayAsset   string
	defaultPayNetwork string
	simulateEnabled   bool
	telegramBotName   string
	webAppURL         string
	adminAppURL       string
	now               func() time.Time
	actionLimiter     actionLimiter
}

func NewService(client messageClient, app *service.App, cfg config.Config) *Service {
	return &Service{
		client:            client,
		app:               app,
		pollTimeout:       cfg.LongPollTimeout,
		defaultPayAsset:   cfg.DefaultPayAsset,
		defaultPayNetwork: cfg.DefaultPayNetwork,
		simulateEnabled:   app != nil && app.CanSimulatePayments(),
		telegramBotName:   strings.TrimPrefix(strings.TrimSpace(cfg.TelegramBotUsername), "@"),
		webAppURL:         appRouteURL(cfg),
		adminAppURL:       adminRouteURL(cfg),
		now:               time.Now,
	}
}

func appRouteURL(cfg config.Config) string {
	if !cfg.WebEnabled || !cfg.WebShellEnabled {
		return ""
	}
	base := appPublicOrigin(cfg)
	if base == "" {
		return ""
	}
	return base + "/app"
}

func adminRouteURL(cfg config.Config) string {
	if !cfg.WebEnabled || !cfg.WebShellEnabled {
		return ""
	}
	base := appPublicOrigin(cfg)
	if base == "" {
		return ""
	}
	return base + "/admin"
}

func appPublicOrigin(cfg config.Config) string {
	raw := strings.TrimSpace(cfg.WebPublicBaseURL)
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

var botActionLimits = map[string]actionLimit{
	"join":    {window: 5 * time.Minute, max: 3},
	"pay":     {window: 5 * time.Minute, max: 8},
	"support": {window: 15 * time.Minute, max: 4},
	"invite":  {window: 5 * time.Minute, max: 6},
}

func (s *Service) ensureActionAllowed(ctx context.Context, actor service.Actor, action string) error {
	if s.app != nil {
		blocked, reason, err := s.app.IsTelegramIDDenied(ctx, actor.TelegramID)
		if err != nil {
			return err
		}
		if blocked {
			if strings.TrimSpace(reason) == "" {
				reason = "this account is blocked from billing actions"
			}
			return fmt.Errorf("account blocked: %s", reason)
		}
	}
	limit, ok := botActionLimits[action]
	if !ok || actor.TelegramID <= 0 {
		return nil
	}
	now := s.now().UTC()
	key := action + ":" + strconv.FormatInt(actor.TelegramID, 10)
	s.actionLimiter.mu.Lock()
	defer s.actionLimiter.mu.Unlock()
	if s.actionLimiter.hits == nil {
		s.actionLimiter.hits = make(map[string][]time.Time)
	}
	cutoff := now.Add(-limit.window)
	hits := s.actionLimiter.hits[key][:0]
	for _, hit := range s.actionLimiter.hits[key] {
		if hit.After(cutoff) {
			hits = append(hits, hit)
		}
	}
	if len(hits) >= limit.max {
		s.actionLimiter.hits[key] = hits
		return fmt.Errorf("too many %s attempts right now; please wait a few minutes", action)
	}
	hits = append(hits, now)
	s.actionLimiter.hits[key] = hits
	return nil
}

func (s *Service) Start(ctx context.Context) error {
	s.configureBot(ctx)

	var offset int64
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := s.client.GetUpdates(ctx, offset, s.pollTimeout)
		if err != nil {
			log.Printf("subscription bot getUpdates error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if err := s.handleUpdate(ctx, update); err != nil {
				log.Printf("subscription bot handle update %d: %v", update.UpdateID, err)
			}
		}
	}
}

func (s *Service) ProcessUpdate(ctx context.Context, update telegram.Update) error {
	return s.handleUpdate(ctx, update)
}

func (s *Service) DispatchNotifications(ctx context.Context, notifications []domain.Notification) error {
	for _, item := range notifications {
		if item.TelegramID <= 0 || strings.TrimSpace(item.Message) == "" {
			continue
		}
		if err := s.client.SendMessage(ctx, item.TelegramID, item.Message, telegram.MessageOptions{
			ReplyMarkup: s.homeKeyboard(service.Actor{TelegramID: item.TelegramID}),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleUpdate(ctx context.Context, update telegram.Update) error {
	switch {
	case update.Message != nil && update.Message.From != nil:
		return s.handleMessage(ctx, update.Message)
	case update.CallbackQuery != nil && update.CallbackQuery.From != nil:
		return s.handleCallbackQuery(ctx, update.CallbackQuery)
	default:
		return nil
	}
}

func (s *Service) handleMessage(ctx context.Context, message *telegram.Message) error {
	actor := service.Actor{
		TelegramID: message.From.ID,
		Username:   bestUsername(message.From),
	}
	command, args := parseCommand(message.Text)
	if strings.HasPrefix(strings.TrimSpace(message.Text), "/") {
		switch command {
		case "", "/start", "/help":
			return s.showHome(ctx, message.Chat.ID, 0, actor, false)
		case "/cancel":
			return s.handleCancel(ctx, message.Chat.ID, actor)
		case "/create_plan":
			return s.handleCreatePlanCommand(ctx, message.Chat.ID, actor, args)
		case "/my_plans":
			return s.showMyPlans(ctx, message.Chat.ID, 0, actor, 0, false)
		case "/join":
			return s.handleJoinCommand(ctx, message.Chat.ID, actor, args)
		case "/pay":
			return s.handlePayCommand(ctx, message.Chat.ID, actor, args)
		case "/invoice":
			return s.handleInvoiceCommand(ctx, message.Chat.ID, actor)
		case "/renew":
			return s.handleRenew(ctx, message.Chat.ID, actor)
		case "/members":
			return s.handleMembers(ctx, message.Chat.ID, actor, args)
		case "/ledger":
			return s.handleLedger(ctx, message.Chat.ID, actor, args)
		case "/support":
			return s.handleSupportCommand(ctx, message.Chat.ID, actor, args)
		case "/settings":
			return s.send(ctx, message.Chat.ID, s.settingsMessage(actor), s.settingsKeyboard(actor))
		case "/admin":
			return s.showAdmin(ctx, message.Chat.ID, 0, actor, false)
		default:
			return s.send(ctx, message.Chat.ID, unknownCommandMessage(), s.homeKeyboard(actor))
		}
	}

	state, err := s.app.LoadConversationState(ctx, actor, s.now())
	if err != nil {
		return s.send(ctx, message.Chat.ID, fmt.Sprintf("Could not restore your active step: %v", err), s.homeKeyboard(actor))
	}
	if state == nil {
		return s.showHome(ctx, message.Chat.ID, 0, actor, false)
	}
	return s.handleConversationInput(ctx, message.Chat.ID, actor, strings.TrimSpace(message.Text), state)
}

func (s *Service) handleCallbackQuery(ctx context.Context, query *telegram.CallbackQuery) error {
	actor := service.Actor{
		TelegramID: query.From.ID,
		Username:   bestUsername(query.From),
	}
	chatID := int64(0)
	messageID := int64(0)
	if query.Message != nil {
		chatID = query.Message.Chat.ID
		messageID = query.Message.MessageID
	}
	if chatID == 0 {
		_ = s.client.AnswerCallbackQuery(ctx, query.ID, "This action is missing chat context.")
		return nil
	}
	answerText := ""
	err := s.routeCallback(ctx, query, actor, chatID, messageID, &answerText)
	if cbErr := s.client.AnswerCallbackQuery(ctx, query.ID, answerText); cbErr != nil {
		log.Printf("subscription bot answerCallbackQuery error: %v", cbErr)
	}
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("That action could not be completed: %v", err), s.homeKeyboard(actor))
	}
	return nil
}

func (s *Service) routeCallback(ctx context.Context, query *telegram.CallbackQuery, actor service.Actor, chatID int64, messageID int64, answerText *string) error {
	data := strings.TrimSpace(query.Data)
	switch {
	case data == "home":
		return s.showHome(ctx, chatID, messageID, actor, true)
	case data == "cancel":
		if err := s.app.ClearConversationState(ctx, actor); err != nil {
			return err
		}
		return s.showHome(ctx, chatID, messageID, actor, true)
	case data == "help":
		return s.showHome(ctx, chatID, messageID, actor, true)
	case data == "settings":
		return s.edit(ctx, chatID, messageID, s.settingsMessage(actor), s.settingsKeyboard(actor))
	case data == "plans:0":
		return s.showMyPlans(ctx, chatID, messageID, actor, 0, true)
	case strings.HasPrefix(data, "plans:page:"):
		page, _ := strconv.Atoi(strings.TrimPrefix(data, "plans:page:"))
		return s.showMyPlans(ctx, chatID, messageID, actor, page, true)
	case data == "create_plan:start":
		return s.startCreatePlanFlow(ctx, chatID, messageID, actor, true)
	case strings.HasPrefix(data, "create_plan:service:"):
		return s.selectCreatePlanService(ctx, chatID, messageID, actor, strings.TrimPrefix(data, "create_plan:service:"))
	case data == "create_plan:confirm":
		return s.confirmCreatePlan(ctx, chatID, messageID, actor)
	case data == "join:start":
		return s.startJoinFlow(ctx, chatID, actor)
	case strings.HasPrefix(data, "plan:") && !strings.Contains(strings.TrimPrefix(data, "plan:"), ":"):
		return s.showPlan(ctx, chatID, messageID, actor, strings.TrimPrefix(data, "plan:"), true)
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":invoice"):
		planID := strings.TrimSuffix(strings.TrimPrefix(data, "plan:"), ":invoice")
		return s.showPlanInvoice(ctx, chatID, messageID, actor, planID, true)
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":pay"):
		planID := strings.TrimSuffix(strings.TrimPrefix(data, "plan:"), ":pay")
		return s.startPayForPlan(ctx, chatID, messageID, actor, planID, true)
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":invite"):
		planID := strings.TrimSuffix(strings.TrimPrefix(data, "plan:"), ":invite")
		return s.handleInvite(ctx, chatID, actor, planID)
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":members"):
		planID := strings.TrimSuffix(strings.TrimPrefix(data, "plan:"), ":members")
		return s.handleMembers(ctx, chatID, actor, []string{planID})
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":ledger"):
		planID := strings.TrimSuffix(strings.TrimPrefix(data, "plan:"), ":ledger")
		return s.handleLedger(ctx, chatID, actor, []string{planID})
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":support"):
		planID := strings.TrimSuffix(strings.TrimPrefix(data, "plan:"), ":support")
		return s.startSupportFlow(ctx, chatID, actor, planID)
	case strings.HasPrefix(data, "plan:") && strings.HasSuffix(data, ":renew"):
		return s.handleRenew(ctx, chatID, actor)
	case strings.HasPrefix(data, "invoice:") && strings.HasSuffix(data, ":pay"):
		invoiceID := strings.TrimSuffix(strings.TrimPrefix(data, "invoice:"), ":pay")
		return s.showInvoicePaymentAssets(ctx, chatID, messageID, actor, invoiceID, true)
	case strings.HasPrefix(data, "invoice:") && strings.HasSuffix(data, ":refresh"):
		invoiceID := strings.TrimSuffix(strings.TrimPrefix(data, "invoice:"), ":refresh")
		return s.showInvoiceByID(ctx, chatID, messageID, actor, invoiceID, true)
	case strings.HasPrefix(data, "pay:asset:"):
		parts := strings.Split(strings.TrimPrefix(data, "pay:asset:"), ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid payment asset action")
		}
		return s.showInvoicePaymentNetworks(ctx, chatID, messageID, actor, parts[0], parts[1], true)
	case strings.HasPrefix(data, "pay:quote:"):
		parts := strings.Split(strings.TrimPrefix(data, "pay:quote:"), ":")
		if len(parts) != 3 {
			return fmt.Errorf("invalid payment quote action")
		}
		return s.quoteInvoice(ctx, chatID, actor, parts[0], parts[1], parts[2])
	case strings.HasPrefix(data, "pay:simulate:"):
		invoiceID := strings.TrimPrefix(data, "pay:simulate:")
		return s.simulatePayment(ctx, chatID, actor, invoiceID)
	case data == "invoice:latest":
		return s.handleInvoiceCommand(ctx, chatID, actor)
	case data == "admin:home":
		return s.showAdmin(ctx, chatID, messageID, actor, true)
	case data == "admin:support":
		return s.showAdminSupport(ctx, chatID, messageID, actor)
	case data == "admin:issues":
		return s.showAdminRenewalIssues(ctx, chatID, messageID, actor)
	case data == "admin:payouts":
		return s.showAdminPayouts(ctx, chatID, messageID, actor)
	case data == "admin:alerts":
		return s.showAdminPaymentAlerts(ctx, chatID, messageID, actor)
	case data == "admin:blocklist":
		return s.showAdminBlocklist(ctx, chatID, messageID, actor)
	case data == "admin:block:add:user":
		return s.startAdminBlockUserFlow(ctx, chatID, actor)
	case data == "admin:plans":
		return s.showAdminRecentPlans(ctx, chatID, messageID, actor)
	default:
		*answerText = "This button is no longer valid."
		return nil
	}
}

func (s *Service) handleConversationInput(ctx context.Context, chatID int64, actor service.Actor, text string, state *domain.BotConversationState) error {
	switch state.Flow {
	case flowCreatePlan:
		return s.handleCreatePlanFlowInput(ctx, chatID, actor, text, state)
	case flowJoin:
		return s.finishJoinFlow(ctx, chatID, actor, text)
	case flowSupport:
		return s.finishSupportFlow(ctx, chatID, actor, text, state)
	case flowAdminBlock:
		return s.handleAdminBlockFlowInput(ctx, chatID, actor, text, state)
	default:
		_ = s.app.ClearConversationState(ctx, actor)
		return s.showHome(ctx, chatID, 0, actor, false)
	}
}

func (s *Service) handleCancel(ctx context.Context, chatID int64, actor service.Actor) error {
	if err := s.app.ClearConversationState(ctx, actor); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not clear your active step: %v", err), s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, "Cancelled the active flow. Use the buttons below to start a new action.", s.homeKeyboard(actor))
}

func (s *Service) handleCreatePlanCommand(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	if len(args) >= 4 {
		return s.createPlanRaw(ctx, chatID, actor, args)
	}
	return s.startCreatePlanFlow(ctx, chatID, 0, actor, false)
}

func (s *Service) createPlanRaw(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	totalMinor, err := parseMoneyMinor(args[1])
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not read the monthly price: %v", err), s.homeKeyboard(actor))
	}
	seatLimit, err := strconv.Atoi(args[2])
	if err != nil || seatLimit <= 0 {
		return s.send(ctx, chatID, "Seat limit must be a whole number greater than zero.", s.homeKeyboard(actor))
	}
	renewalDate, err := time.Parse("2006-01-02", args[3])
	if err != nil {
		return s.send(ctx, chatID, "Renewal date must look like YYYY-MM-DD.", s.homeKeyboard(actor))
	}
	accessMode := ""
	if len(args) >= 5 {
		accessMode = args[4]
	}
	plan, invite, err := s.app.CreatePlan(ctx, actor, service.CreatePlanInput{
		ServiceCode:      args[0],
		TotalPriceMinor:  totalMinor,
		SeatLimit:        seatLimit,
		RenewalDate:      renewalDate,
		SharingPolicyAck: true,
		AccessMode:       accessMode,
	}, s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not create plan: %v", err), s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, s.planCreatedMessage(plan, invite), s.planActionKeyboard(&domain.PlanView{Plan: plan, IsOwner: true}, actor))
}

func (s *Service) startCreatePlanFlow(ctx context.Context, chatID int64, messageID int64, actor service.Actor, editable bool) error {
	if err := s.app.SaveConversationState(ctx, actor, flowCreatePlan, "service_code", createPlanState{}, s.now()); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not start plan creation: %v", err), s.homeKeyboard(actor))
	}
	catalog, err := s.app.ListCatalog(ctx)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load approved services: %v", err), s.homeKeyboard(actor))
	}
	text := "Choose an approved subscription to split. The bot only supports family or team plans that are meant to be shared through official seats or invites."
	keyboard := serviceCatalogKeyboard(catalog)
	if editable && messageID > 0 {
		return s.edit(ctx, chatID, messageID, text, keyboard)
	}
	return s.send(ctx, chatID, text, keyboard)
}

func (s *Service) selectCreatePlanService(ctx context.Context, chatID int64, messageID int64, actor service.Actor, serviceCode string) error {
	catalog, err := s.app.ListCatalog(ctx)
	if err != nil {
		return err
	}
	var selected *domain.ServiceCatalogEntry
	for idx := range catalog {
		if catalog[idx].ServiceCode == serviceCode {
			selected = &catalog[idx]
			break
		}
	}
	if selected == nil {
		return fmt.Errorf("that service is not in the approved catalog")
	}
	state := createPlanState{
		ServiceCode:       selected.ServiceCode,
		ServiceName:       selected.DisplayName,
		AccessMode:        selected.AccessMode,
		SharingPolicyNote: selected.SharingPolicyNote,
	}
	if err := s.app.SaveConversationState(ctx, actor, flowCreatePlan, "monthly_total", state, s.now()); err != nil {
		return err
	}
	text := fmt.Sprintf(
		"%s selected.\n\nPolicy: %s\nAccess mode: %s\n\nSend the total monthly price in USDC, for example `18.00`.",
		selected.DisplayName,
		selected.SharingPolicyNote,
		selected.AccessMode,
	)
	return s.edit(ctx, chatID, messageID, text, cancelKeyboard())
}

func (s *Service) handleCreatePlanFlowInput(ctx context.Context, chatID int64, actor service.Actor, text string, state *domain.BotConversationState) error {
	payload := createPlanState{}
	if err := s.app.ParseConversationPayload(state, &payload); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not restore the create-plan wizard: %v", err), s.homeKeyboard(actor))
	}
	switch state.Step {
	case "monthly_total":
		totalMinor, err := parseMoneyMinor(text)
		if err != nil || totalMinor <= 0 {
			return s.send(ctx, chatID, "Send the monthly total as a price like `18.00`.", cancelKeyboard())
		}
		payload.TotalPriceMinor = totalMinor
		if err := s.app.SaveConversationState(ctx, actor, flowCreatePlan, "seat_limit", payload, s.now()); err != nil {
			return err
		}
		return s.send(ctx, chatID, "How many seats are in the shareable plan? Send a whole number like `2` or `6`.", cancelKeyboard())
	case "seat_limit":
		seatLimit, err := strconv.Atoi(text)
		if err != nil || seatLimit <= 0 {
			return s.send(ctx, chatID, "Seat count must be a whole number greater than zero.", cancelKeyboard())
		}
		payload.SeatLimit = seatLimit
		if err := s.app.SaveConversationState(ctx, actor, flowCreatePlan, "renewal_date", payload, s.now()); err != nil {
			return err
		}
		return s.send(ctx, chatID, "Send the next renewal date as `YYYY-MM-DD`, for example `2026-04-01`.", cancelKeyboard())
	case "renewal_date":
		renewalDate, err := time.Parse("2006-01-02", text)
		if err != nil {
			return s.send(ctx, chatID, "Renewal date must look like `YYYY-MM-DD`.", cancelKeyboard())
		}
		payload.RenewalDate = renewalDate.Format("2006-01-02")
		if err := s.app.SaveConversationState(ctx, actor, flowCreatePlan, "confirm", payload, s.now()); err != nil {
			return err
		}
		return s.send(ctx, chatID, s.createPlanConfirmMessage(payload), createPlanConfirmKeyboard())
	default:
		_ = s.app.ClearConversationState(ctx, actor)
		return s.showHome(ctx, chatID, 0, actor, false)
	}
}

func (s *Service) confirmCreatePlan(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	state, err := s.app.LoadConversationState(ctx, actor, s.now())
	if err != nil {
		return err
	}
	if state == nil || state.Flow != flowCreatePlan {
		return s.edit(ctx, chatID, messageID, "That plan draft expired. Start again from the home screen.", s.homeKeyboard(actor))
	}
	payload := createPlanState{}
	if err := s.app.ParseConversationPayload(state, &payload); err != nil {
		return err
	}
	renewalDate, err := time.Parse("2006-01-02", payload.RenewalDate)
	if err != nil {
		return err
	}
	plan, invite, err := s.app.CreatePlan(ctx, actor, service.CreatePlanInput{
		ServiceCode:      payload.ServiceCode,
		TotalPriceMinor:  payload.TotalPriceMinor,
		SeatLimit:        payload.SeatLimit,
		RenewalDate:      renewalDate,
		SharingPolicyAck: true,
		AccessMode:       payload.AccessMode,
	}, s.now())
	if err != nil {
		return s.edit(ctx, chatID, messageID, fmt.Sprintf("Could not create the plan: %v", err), cancelKeyboard())
	}
	if err := s.app.ClearConversationState(ctx, actor); err != nil {
		return err
	}
	if err := s.edit(ctx, chatID, messageID, "Plan created. I’ve sent the invite and next actions below.", s.homeKeyboard(actor)); err != nil {
		return err
	}
	return s.send(ctx, chatID, s.planCreatedMessage(plan, invite), s.planActionKeyboard(&domain.PlanView{Plan: plan, IsOwner: true}, actor))
}

func (s *Service) handleJoinCommand(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	if len(args) >= 1 {
		return s.finishJoinFlow(ctx, chatID, actor, args[0])
	}
	return s.startJoinFlow(ctx, chatID, actor)
}

func (s *Service) startJoinFlow(ctx context.Context, chatID int64, actor service.Actor) error {
	if err := s.ensureActionAllowed(ctx, actor, "join"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	if err := s.app.SaveConversationState(ctx, actor, flowJoin, "invite_code", map[string]string{}, s.now()); err != nil {
		return err
	}
	return s.send(ctx, chatID, "Paste the invite code from the owner to join a plan.", cancelKeyboard())
}

func (s *Service) finishJoinFlow(ctx context.Context, chatID int64, actor service.Actor, inviteCode string) error {
	if err := s.ensureActionAllowed(ctx, actor, "join"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	member, invoice, err := s.app.JoinPlan(ctx, actor, strings.TrimSpace(inviteCode), s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not join plan: %v", err), s.homeKeyboard(actor))
	}
	_ = s.app.ClearConversationState(ctx, actor)
	return s.send(ctx, chatID, fmt.Sprintf(
		"Joined the plan.\nMembership: %s\nSeat status: %s\nFirst invoice: %s\nBase: %.2f USDC\nFee: %.2f USDC\nDue now: %.2f USDC",
		member.ID,
		member.SeatStatus,
		invoice.ID,
		units(invoice.BaseMinor),
		units(invoice.FeeMinor),
		units(invoice.AmountDueMinor()),
	), s.invoiceKeyboard(invoice))
}

func (s *Service) handlePayCommand(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	invoiceID := ""
	payAsset := ""
	network := ""
	if len(args) > 0 {
		first := strings.TrimSpace(args[0])
		if looksLikeInvoiceID(first) {
			invoiceID = first
			args = args[1:]
		}
	}
	if len(args) > 0 {
		payAsset = strings.ToUpper(strings.TrimSpace(args[0]))
	}
	if len(args) > 1 {
		network = strings.ToLower(strings.TrimSpace(args[1]))
	}
	if payAsset == "" && network == "" && invoiceID == "" {
		invoice, err := s.app.LatestInvoice(ctx, actor)
		if err != nil {
			return s.send(ctx, chatID, fmt.Sprintf("Could not load your latest invoice: %v", err), s.homeKeyboard(actor))
		}
		if invoice == nil {
			return s.send(ctx, chatID, "You do not have an open invoice right now.", s.homeKeyboard(actor))
		}
		return s.showInvoicePaymentAssets(ctx, chatID, 0, actor, invoice.ID, false)
	}
	if invoiceID == "" {
		invoice, err := s.app.LatestInvoice(ctx, actor)
		if err != nil {
			return s.send(ctx, chatID, fmt.Sprintf("Could not load your latest invoice: %v", err), s.homeKeyboard(actor))
		}
		if invoice == nil {
			return s.send(ctx, chatID, "You do not have an open invoice right now.", s.homeKeyboard(actor))
		}
		invoiceID = invoice.ID
	}
	if payAsset == "" {
		return s.showInvoicePaymentAssets(ctx, chatID, 0, actor, invoiceID, false)
	}
	if network == "" {
		return s.showInvoicePaymentNetworks(ctx, chatID, 0, actor, invoiceID, payAsset, false)
	}
	return s.quoteInvoice(ctx, chatID, actor, invoiceID, payAsset, network)
}

func (s *Service) handleInvoiceCommand(ctx context.Context, chatID int64, actor service.Actor) error {
	invoice, err := s.app.LatestInvoice(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load invoice: %v", err), s.homeKeyboard(actor))
	}
	if invoice == nil {
		return s.send(ctx, chatID, "No open invoice found.", s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, s.invoiceMessage(*invoice), s.invoiceKeyboard(*invoice))
}

func (s *Service) handleRenew(ctx context.Context, chatID int64, actor service.Actor) error {
	allowed, err := s.app.CanManageRenewals(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not verify renewal permissions: %v", err), s.homeKeyboard(actor))
	}
	if !allowed {
		return s.send(ctx, chatID, "Only plan owners and operators can run billing checks.", s.homeKeyboard(actor))
	}
	notifications, err := s.app.ProcessCycle(ctx, s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not run billing checks: %v", err), s.homeKeyboard(actor))
	}
	if err := s.DispatchNotifications(ctx, notifications); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Billing checks ran, but follow-up notices failed: %v", err), s.homeKeyboard(actor))
	}
	invoice, err := s.app.LatestInvoice(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Billing checks ran, but loading your latest invoice failed: %v", err), s.homeKeyboard(actor))
	}
	if invoice == nil {
		return s.send(ctx, chatID, "Billing checks finished. You do not have an open invoice right now.", s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, fmt.Sprintf("Billing checks finished. Latest invoice: %s (%s).", invoice.ID, invoice.Status), s.invoiceKeyboard(*invoice))
}

func (s *Service) handleMembers(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	if len(args) < 1 {
		return s.send(ctx, chatID, "Usage: /members <plan_id>", s.homeKeyboard(actor))
	}
	members, err := s.app.ListPlanMembers(ctx, actor, args[0])
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load members: %v", err), s.homeKeyboard(actor))
	}
	lines := []string{fmt.Sprintf("Members for %s:", args[0])}
	for _, member := range members {
		label := member.Username
		if label == "" {
			label = strconv.FormatInt(member.UserTelegramID, 10)
		}
		lines = append(lines, fmt.Sprintf("- %s seat=%s latest_invoice=%s", label, member.SeatStatus, member.LatestInvoiceID))
	}
	return s.send(ctx, chatID, strings.Join(lines, "\n"), s.homeKeyboard(actor))
}

func (s *Service) handleLedger(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	if len(args) < 1 {
		return s.send(ctx, chatID, "Usage: /ledger <plan_id>", s.homeKeyboard(actor))
	}
	ledger, err := s.app.Ledger(ctx, actor, args[0])
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load ledger: %v", err), s.homeKeyboard(actor))
	}
	lines := []string{
		fmt.Sprintf("Ledger for %s:", ledger.Plan.ServiceName),
		fmt.Sprintf("Invoices: %d", len(ledger.Invoices)),
		fmt.Sprintf("Payments: %d", len(ledger.Payments)),
		fmt.Sprintf("Credits: %d", len(ledger.Credits)),
		fmt.Sprintf("Events: %d", len(ledger.Events)),
	}
	if len(ledger.Invoices) > 0 {
		lines = append(lines, fmt.Sprintf("Latest invoice: %s (%s)", ledger.Invoices[0].ID, ledger.Invoices[0].Status))
	}
	return s.send(ctx, chatID, strings.Join(lines, "\n"), s.homeKeyboard(actor))
}

func (s *Service) handleSupportCommand(ctx context.Context, chatID int64, actor service.Actor, args []string) error {
	if len(args) >= 2 {
		if err := s.ensureActionAllowed(ctx, actor, "support"); err != nil {
			return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
		}
		ticket, err := s.app.OpenSupportTicket(ctx, actor, args[0], strings.Join(args[1:], " "), s.now())
		if err != nil {
			return s.send(ctx, chatID, fmt.Sprintf("Could not open support request: %v", err), s.homeKeyboard(actor))
		}
		return s.send(ctx, chatID, fmt.Sprintf("Support request opened: %s", ticket.ID), s.homeKeyboard(actor))
	}
	if len(args) == 1 {
		return s.startSupportFlow(ctx, chatID, actor, args[0])
	}
	return s.send(ctx, chatID, "Usage: /support <plan_id> <message>", s.homeKeyboard(actor))
}

func (s *Service) startSupportFlow(ctx context.Context, chatID int64, actor service.Actor, planID string) error {
	if err := s.ensureActionAllowed(ctx, actor, "support"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	if err := s.app.SaveConversationState(ctx, actor, flowSupport, "message", supportState{PlanID: planID}, s.now()); err != nil {
		return err
	}
	return s.send(ctx, chatID, fmt.Sprintf("Send the support message for plan %s.", planID), cancelKeyboard())
}

func (s *Service) finishSupportFlow(ctx context.Context, chatID int64, actor service.Actor, text string, state *domain.BotConversationState) error {
	if err := s.ensureActionAllowed(ctx, actor, "support"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	payload := supportState{}
	if err := s.app.ParseConversationPayload(state, &payload); err != nil {
		return err
	}
	ticket, err := s.app.OpenSupportTicket(ctx, actor, payload.PlanID, text, s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not open support request: %v", err), s.homeKeyboard(actor))
	}
	_ = s.app.ClearConversationState(ctx, actor)
	return s.send(ctx, chatID, fmt.Sprintf("Support request opened: %s", ticket.ID), s.homeKeyboard(actor))
}

func (s *Service) handleInvite(ctx context.Context, chatID int64, actor service.Actor, planID string) error {
	if err := s.ensureActionAllowed(ctx, actor, "invite"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	invite, err := s.app.CreateInvite(ctx, actor, planID, s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not create invite: %v", err), s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, fmt.Sprintf("Fresh invite code for %s: %s", planID, invite.InviteCode), s.homeKeyboard(actor))
}

func (s *Service) showHome(ctx context.Context, chatID int64, messageID int64, actor service.Actor, editable bool) error {
	text := s.startMessage(actor)
	keyboard := s.homeKeyboard(actor)
	if editable && messageID > 0 {
		return s.edit(ctx, chatID, messageID, text, keyboard)
	}
	return s.send(ctx, chatID, text, keyboard)
}

func (s *Service) showMyPlans(ctx context.Context, chatID int64, messageID int64, actor service.Actor, page int, editable bool) error {
	views, err := s.app.ListUserPlans(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load your plans: %v", err), s.homeKeyboard(actor))
	}
	if len(views) == 0 {
		return s.respond(ctx, chatID, messageID, editable, "You are not in any shared plans yet.", s.homeKeyboard(actor))
	}
	totalPages := (len(views) + plansPageSize - 1) / plansPageSize
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * plansPageSize
	end := start + plansPageSize
	if end > len(views) {
		end = len(views)
	}
	lines := []string{fmt.Sprintf("Your plans (%d/%d):", page+1, totalPages)}
	for _, view := range views[start:end] {
		role := "member"
		if view.IsOwner {
			role = "owner"
		}
		line := fmt.Sprintf("- %s [%s] %s", view.Plan.ServiceName, role, view.Plan.ID)
		if view.Membership != nil {
			line += fmt.Sprintf(" seat=%s", view.Membership.SeatStatus)
		}
		if view.OpenInvoice != nil {
			line += fmt.Sprintf(" invoice=%s", view.OpenInvoice.Status)
		}
		lines = append(lines, line)
	}
	return s.respond(ctx, chatID, messageID, editable, strings.Join(lines, "\n"), plansKeyboard(views, page, totalPages))
}

func (s *Service) showPlan(ctx context.Context, chatID int64, messageID int64, actor service.Actor, planID string, editable bool) error {
	view, err := s.app.FindPlanView(ctx, actor, planID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that plan: %v", err), s.homeKeyboard(actor))
	}
	text := s.planSummaryMessage(view)
	keyboard := s.planActionKeyboard(view, actor)
	return s.respond(ctx, chatID, messageID, editable, text, keyboard)
}

func (s *Service) showPlanInvoice(ctx context.Context, chatID int64, messageID int64, actor service.Actor, planID string, editable bool) error {
	invoice, err := s.app.LatestInvoiceForPlan(ctx, actor, planID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that invoice: %v", err), s.homeKeyboard(actor))
	}
	if invoice == nil {
		return s.respond(ctx, chatID, messageID, editable, "There is no invoice for you on this plan right now.", s.homeKeyboard(actor))
	}
	return s.respond(ctx, chatID, messageID, editable, s.invoiceMessage(*invoice), s.invoiceKeyboard(*invoice))
}

func (s *Service) showInvoiceByID(ctx context.Context, chatID int64, messageID int64, actor service.Actor, invoiceID string, editable bool) error {
	invoice, err := s.loadAuthorizedInvoice(ctx, actor, invoiceID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that invoice: %v", err), s.homeKeyboard(actor))
	}
	return s.respond(ctx, chatID, messageID, editable, s.invoiceMessage(invoice), s.invoiceKeyboard(invoice))
}

func (s *Service) startPayForPlan(ctx context.Context, chatID int64, messageID int64, actor service.Actor, planID string, editable bool) error {
	if err := s.ensureActionAllowed(ctx, actor, "pay"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	invoice, err := s.app.LatestInvoiceForPlan(ctx, actor, planID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that invoice: %v", err), s.homeKeyboard(actor))
	}
	if invoice == nil {
		return s.respond(ctx, chatID, messageID, editable, "You do not have an invoice for that plan right now.", s.homeKeyboard(actor))
	}
	return s.showInvoicePaymentAssets(ctx, chatID, messageID, actor, invoice.ID, editable)
}

func (s *Service) showInvoicePaymentAssets(ctx context.Context, chatID int64, messageID int64, actor service.Actor, invoiceID string, editable bool) error {
	invoice, err := s.loadAuthorizedInvoice(ctx, actor, invoiceID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that invoice: %v", err), s.homeKeyboard(actor))
	}
	text := fmt.Sprintf("Choose what the member will pay with for invoice %s.\nAmount due: %.2f USDC.", invoice.ID, units(invoice.AmountDueMinor()-invoice.PaidMinor))
	keyboard := payAssetKeyboard(invoice.ID, s.app.AllowedPayAssets())
	return s.respond(ctx, chatID, messageID, editable, text, keyboard)
}

func (s *Service) showInvoicePaymentNetworks(ctx context.Context, chatID int64, messageID int64, actor service.Actor, invoiceID string, asset string, editable bool) error {
	_, err := s.loadAuthorizedInvoice(ctx, actor, invoiceID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that invoice: %v", err), s.homeKeyboard(actor))
	}
	text := fmt.Sprintf("Choose the network for %s.", strings.ToUpper(asset))
	keyboard := payNetworkKeyboard(invoiceID, asset, s.app.AllowedNetworksForAsset(asset))
	return s.respond(ctx, chatID, messageID, editable, text, keyboard)
}

func (s *Service) quoteInvoice(ctx context.Context, chatID int64, actor service.Actor, invoiceID string, asset string, network string) error {
	if err := s.ensureActionAllowed(ctx, actor, "pay"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	invoice, err := s.app.QuoteInvoice(ctx, actor, invoiceID, asset, network, s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not create payment quote: %v", err), s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, s.invoiceMessage(invoice), s.invoiceKeyboard(invoice))
}

func (s *Service) simulatePayment(ctx context.Context, chatID int64, actor service.Actor, invoiceID string) error {
	if err := s.ensureActionAllowed(ctx, actor, "pay"); err != nil {
		return s.send(ctx, chatID, err.Error(), s.homeKeyboard(actor))
	}
	if !s.simulateEnabled {
		return s.send(ctx, chatID, "Sandbox payment simulation is not enabled here.", s.homeKeyboard(actor))
	}
	invoice, err := s.loadAuthorizedInvoice(ctx, actor, invoiceID)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load that invoice: %v", err), s.homeKeyboard(actor))
	}
	amount := invoice.QuotedPayAmount
	if strings.TrimSpace(amount) == "" {
		return s.send(ctx, chatID, "Quote the invoice first so there is a payment amount to simulate.", s.invoiceKeyboard(invoice))
	}
	if err := s.app.SimulateInvoicePayment(ctx, actor, invoice.ID, amount, s.now()); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not simulate the payment: %v", err), s.invoiceKeyboard(invoice))
	}
	notifications, err := s.app.ProcessCycle(ctx, s.now())
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Payment was submitted, but the follow-up check failed: %v", err), s.invoiceKeyboard(invoice))
	}
	if err := s.DispatchNotifications(ctx, notifications); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Payment was submitted, but delivery of notices failed: %v", err), s.homeKeyboard(actor))
	}
	refreshed, err := s.loadAuthorizedInvoice(ctx, actor, invoice.ID)
	if err != nil {
		return s.send(ctx, chatID, "Sandbox payment submitted. I could not reload the invoice afterward.", s.homeKeyboard(actor))
	}
	return s.send(ctx, chatID, "Sandbox payment submitted and processed.", s.invoiceKeyboard(refreshed))
}

func (s *Service) showAdmin(ctx context.Context, chatID int64, messageID int64, actor service.Actor, editable bool) error {
	overview, err := s.app.AdminOverview(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load the operator dashboard: %v", err), s.homeKeyboard(actor))
	}
	text := fmt.Sprintf(
		"Operator dashboard\n\nUsers: %d\nPlans: %d\nOpen invoices: %d\nRenewal issues: %d\nOpen support: %d\nPayment alerts: %d\nBlocked actors: %d\nOwner reimbursement due: %.2f USDC",
		overview.UsersTotal,
		overview.PlansTotal,
		overview.OpenInvoicesTotal,
		overview.FailedRenewalsTotal,
		overview.SupportOpenTotal,
		overview.PaymentAlertsTotal,
		overview.BlockedActorsTotal,
		units(overview.PayoutDueMinor),
	)
	return s.respond(ctx, chatID, messageID, editable, text, s.adminKeyboard())
}

func (s *Service) showAdminSupport(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	items, err := s.app.ListOpenSupportTickets(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load open support tickets: %v", err), s.homeKeyboard(actor))
	}
	if len(items) == 0 {
		return s.edit(ctx, chatID, messageID, "Open support queue\n\nNo support tickets are waiting right now.", s.adminKeyboard())
	}
	lines := []string{"Open support queue:"}
	for _, item := range items {
		label := item.Username
		if label == "" {
			label = strconv.FormatInt(item.UserTelegramID, 10)
		}
		line := fmt.Sprintf("- %s on %s: %s", label, item.PlanServiceName, item.Ticket.Body)
		if item.LatestInvoiceID != "" {
			line += fmt.Sprintf(" invoice=%s status=%s due=%.2f paid=%.2f", item.LatestInvoiceID, item.LatestStatus, units(item.LatestDueMinor), units(item.LatestPaidMinor))
		}
		lines = append(lines, line)
	}
	return s.edit(ctx, chatID, messageID, strings.Join(lines, "\n"), s.adminKeyboard())
}

func (s *Service) showAdminRenewalIssues(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	items, err := s.app.ListRenewalIssues(ctx, actor)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load renewal issues: %v", err), s.homeKeyboard(actor))
	}
	if len(items) == 0 {
		return s.edit(ctx, chatID, messageID, "Renewal issues\n\nNo underpaid, grace, or suspended seats right now.", s.adminKeyboard())
	}
	lines := []string{"Renewal issues:"}
	for _, item := range items {
		label := item.Username
		if label == "" {
			label = strconv.FormatInt(item.UserTelegramID, 10)
		}
		line := fmt.Sprintf("- %s on %s [%s]", label, item.PlanServiceName, item.Kind)
		if item.InvoiceID != "" {
			line += fmt.Sprintf(" invoice=%s", item.InvoiceID)
		}
		if item.DueAt != nil {
			line += fmt.Sprintf(" due=%s", item.DueAt.In(s.now().Location()).Format("2006-01-02"))
		}
		lines = append(lines, line)
	}
	return s.edit(ctx, chatID, messageID, strings.Join(lines, "\n"), s.adminKeyboard())
}

func (s *Service) showAdminRecentPlans(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	plans, err := s.app.ListRecentPlans(ctx, actor, 5)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load recent plans: %v", err), s.homeKeyboard(actor))
	}
	if len(plans) == 0 {
		return s.edit(ctx, chatID, messageID, "Recent plans\n\nNo plans have been created yet.", s.adminKeyboard())
	}
	lines := []string{"Recent plans:"}
	for _, plan := range plans {
		lines = append(lines, fmt.Sprintf("- %s (%s) seats=%d total=%.2f USDC", plan.ServiceName, plan.ID, plan.SeatLimit, units(plan.TotalPriceMinor)))
	}
	return s.edit(ctx, chatID, messageID, strings.Join(lines, "\n"), s.adminKeyboard())
}

func (s *Service) showAdminPayouts(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	items, err := s.app.ListOwnerReimbursementsDue(ctx, actor, 5)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load owner reimbursement totals: %v", err), s.homeKeyboard(actor))
	}
	if len(items) == 0 {
		return s.edit(ctx, chatID, messageID, "Owner reimbursement due\n\nNo confirmed owner reimbursements are waiting right now.", s.adminKeyboard())
	}
	lines := []string{"Owner reimbursement due (before manual payout reconciliation):"}
	for _, item := range items {
		label := item.Username
		if label == "" {
			label = strconv.FormatInt(item.OwnerTelegramID, 10)
		}
		lines = append(lines, fmt.Sprintf("- %s: %.2f USDC", label, units(item.AmountMinor)))
	}
	return s.edit(ctx, chatID, messageID, strings.Join(lines, "\n"), s.adminKeyboard())
}

func (s *Service) showAdminPaymentAlerts(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	items, err := s.app.ListRecentPaymentAlerts(ctx, actor, 8)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load payment alerts: %v", err), s.homeKeyboard(actor))
	}
	if len(items) == 0 {
		return s.edit(ctx, chatID, messageID, "Payment alerts\n\nNo payment callback or settlement alerts are waiting right now.", s.adminKeyboard())
	}
	lines := []string{"Payment alerts:"}
	for _, item := range items {
		line := fmt.Sprintf("- %s", item.EventName)
		if item.ProviderInvoice != "" {
			line += fmt.Sprintf(" invoice=%s", item.ProviderInvoice)
		}
		if item.Detail != "" {
			line += fmt.Sprintf(" detail=%s", item.Detail)
		}
		lines = append(lines, line)
	}
	return s.edit(ctx, chatID, messageID, strings.Join(lines, "\n"), s.adminKeyboard())
}

func (s *Service) showAdminBlocklist(ctx context.Context, chatID int64, messageID int64, actor service.Actor) error {
	items, err := s.app.ListDenylistEntries(ctx, actor, 8)
	if err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not load the deny list: %v", err), s.homeKeyboard(actor))
	}
	respond := func(text string) error {
		if messageID > 0 {
			return s.edit(ctx, chatID, messageID, text, s.adminBlocklistKeyboard())
		}
		return s.send(ctx, chatID, text, s.adminBlocklistKeyboard())
	}
	if len(items) == 0 {
		return respond("Deny list\n\nNo blocked actors or payment references are configured yet.")
	}
	lines := []string{"Deny list:"}
	for _, item := range items {
		line := fmt.Sprintf("- %s %s", item.EntryType, item.EntryValue)
		if item.Reason != "" {
			line += fmt.Sprintf(" (%s)", item.Reason)
		}
		lines = append(lines, line)
	}
	return respond(strings.Join(lines, "\n"))
}

func (s *Service) startAdminBlockUserFlow(ctx context.Context, chatID int64, actor service.Actor) error {
	if !s.app.IsOperator(actor.TelegramID) {
		return s.send(ctx, chatID, "Only operators can change the deny list.", s.homeKeyboard(actor))
	}
	if err := s.app.SaveConversationState(ctx, actor, flowAdminBlock, "telegram_id", adminBlockState{}, s.now()); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not start the block-user flow: %v", err), s.adminBlocklistKeyboard())
	}
	return s.send(ctx, chatID, "Send the Telegram ID you want to block from billing actions.", cancelKeyboard())
}

func (s *Service) handleAdminBlockFlowInput(ctx context.Context, chatID int64, actor service.Actor, text string, state *domain.BotConversationState) error {
	if !s.app.IsOperator(actor.TelegramID) {
		return s.send(ctx, chatID, "Only operators can change the deny list.", s.homeKeyboard(actor))
	}
	payload := adminBlockState{}
	if err := s.app.ParseConversationPayload(state, &payload); err != nil {
		return s.send(ctx, chatID, fmt.Sprintf("Could not restore the deny-list flow: %v", err), s.adminBlocklistKeyboard())
	}
	switch state.Step {
	case "telegram_id":
		telegramID, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil || telegramID <= 0 {
			return s.send(ctx, chatID, "Send a whole Telegram ID, for example `123456789`.", cancelKeyboard())
		}
		payload.TelegramID = strconv.FormatInt(telegramID, 10)
		if err := s.app.SaveConversationState(ctx, actor, flowAdminBlock, "reason", payload, s.now()); err != nil {
			return s.send(ctx, chatID, fmt.Sprintf("Could not save that Telegram ID: %v", err), s.adminBlocklistKeyboard())
		}
		return s.send(ctx, chatID, "Send the reason for blocking this actor.", cancelKeyboard())
	case "reason":
		entry, err := s.app.AddDenylistEntry(ctx, actor, "telegram_id", payload.TelegramID, text, s.now())
		if err != nil {
			return s.send(ctx, chatID, fmt.Sprintf("Could not add the deny-list entry: %v", err), s.adminBlocklistKeyboard())
		}
		_ = s.app.ClearConversationState(ctx, actor)
		return s.send(ctx, chatID, fmt.Sprintf("Blocked Telegram ID %s. Reason: %s", entry.EntryValue, entry.Reason), s.adminBlocklistKeyboard())
	default:
		_ = s.app.ClearConversationState(ctx, actor)
		return s.showAdminBlocklist(ctx, chatID, 0, actor)
	}
}

func (s *Service) loadAuthorizedInvoice(ctx context.Context, actor service.Actor, invoiceID string) (domain.Invoice, error) {
	return s.app.LoadInvoiceByID(ctx, actor, invoiceID)
}

func (s *Service) configureBot(ctx context.Context) {
	if s.client == nil {
		return
	}
	commands := []telegram.BotCommand{
		{Command: "start", Description: "Open the subscription bot"},
		{Command: "help", Description: "Show the home screen"},
		{Command: "create_plan", Description: "Create a compliant shared plan"},
		{Command: "my_plans", Description: "List your plans"},
		{Command: "join", Description: "Join with an invite code"},
		{Command: "pay", Description: "Create a crypto payment quote"},
		{Command: "invoice", Description: "Show your latest invoice"},
		{Command: "renew", Description: "Run billing checks now"},
		{Command: "members", Description: "Show plan members"},
		{Command: "ledger", Description: "Show a plan ledger summary"},
		{Command: "support", Description: "Open a support request"},
		{Command: "settings", Description: "Show billing rules and defaults"},
		{Command: "admin", Description: "Open the operator dashboard"},
		{Command: "cancel", Description: "Cancel the active guided flow"},
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Command < commands[j].Command })
	if err := s.client.SetMyCommands(ctx, commands); err != nil {
		log.Printf("subscription bot setMyCommands error: %v", err)
	}
	if err := s.client.SetChatMenuButton(ctx, telegram.MenuButton{Type: "commands"}); err != nil {
		log.Printf("subscription bot setChatMenuButton error: %v", err)
	}
}

func (s *Service) send(ctx context.Context, chatID int64, text string, keyboard telegram.InlineKeyboardMarkup) error {
	return s.client.SendMessage(ctx, chatID, text, telegram.MessageOptions{ReplyMarkup: keyboard})
}

func (s *Service) edit(ctx context.Context, chatID int64, messageID int64, text string, keyboard telegram.InlineKeyboardMarkup) error {
	if messageID <= 0 {
		return s.send(ctx, chatID, text, keyboard)
	}
	err := s.client.EditMessageText(ctx, chatID, messageID, text, telegram.MessageOptions{ReplyMarkup: keyboard})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return err
}

func (s *Service) respond(ctx context.Context, chatID int64, messageID int64, editable bool, text string, keyboard telegram.InlineKeyboardMarkup) error {
	if editable && messageID > 0 {
		return s.edit(ctx, chatID, messageID, text, keyboard)
	}
	return s.send(ctx, chatID, text, keyboard)
}

func (s *Service) appLaunchURL(section string, planID string, invoiceID string, inviteCode string, adminView string) string {
	base := strings.TrimSpace(s.webAppURL)
	if base == "" {
		return ""
	}
	query := url.Values{}
	if strings.TrimSpace(section) != "" {
		query.Set("section", strings.TrimSpace(section))
	}
	if strings.TrimSpace(planID) != "" {
		query.Set("plan_id", strings.TrimSpace(planID))
	}
	if strings.TrimSpace(invoiceID) != "" {
		query.Set("invoice_id", strings.TrimSpace(invoiceID))
	}
	if strings.TrimSpace(inviteCode) != "" {
		query.Set("invite_code", strings.TrimSpace(inviteCode))
	}
	if strings.TrimSpace(adminView) != "" {
		query.Set("admin_view", strings.TrimSpace(adminView))
	}
	if startApp := s.startAppToken(section, planID, invoiceID, inviteCode, adminView); startApp != "" {
		query.Set("startapp", startApp)
	}
	if encoded := query.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

func (s *Service) adminLaunchURL(adminView string) string {
	base := strings.TrimSpace(s.adminAppURL)
	if base == "" {
		return ""
	}
	query := url.Values{}
	query.Set("section", "admin")
	if strings.TrimSpace(adminView) != "" {
		query.Set("admin_view", strings.TrimSpace(adminView))
	}
	if startApp := s.startAppToken("admin", "", "", "", adminView); startApp != "" {
		query.Set("startapp", startApp)
	}
	if encoded := query.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

func (s *Service) startAppToken(section string, planID string, invoiceID string, inviteCode string, adminView string) string {
	switch {
	case strings.TrimSpace(inviteCode) != "":
		return "join-" + strings.TrimSpace(inviteCode)
	case strings.TrimSpace(invoiceID) != "":
		return "invoice-" + strings.TrimSpace(invoiceID)
	case strings.TrimSpace(planID) != "":
		return "plan-" + strings.TrimSpace(planID)
	case strings.EqualFold(strings.TrimSpace(section), "admin") && strings.TrimSpace(adminView) != "":
		return "admin-" + strings.TrimSpace(strings.ToLower(adminView))
	default:
		return ""
	}
}

func (s *Service) telegramStartAppURL(token string) string {
	if s.telegramBotName == "" {
		return ""
	}
	base := "https://t.me/" + s.telegramBotName
	if strings.TrimSpace(token) == "" {
		return base
	}
	return base + "?startapp=" + url.QueryEscape(strings.TrimSpace(token))
}

func webAppButton(text string, targetURL string) telegram.InlineKeyboardButton {
	return telegram.InlineKeyboardButton{
		Text:   text,
		WebApp: &telegram.WebAppInfo{URL: targetURL},
	}
}

func (s *Service) homeKeyboard(actor service.Actor) telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{
		{{Text: "Create plan", CallbackData: "create_plan:start"}, {Text: "My plans", CallbackData: "plans:0"}},
		{{Text: "Join plan", CallbackData: "join:start"}, {Text: "Latest invoice", CallbackData: "invoice:latest"}},
		{{Text: "Settings", CallbackData: "settings"}},
	}
	if launchURL := s.appLaunchURL("plans", "", "", "", ""); launchURL != "" {
		rows = append([][]telegram.InlineKeyboardButton{{webAppButton("Open app", launchURL)}}, rows...)
	}
	if s.app != nil && s.app.IsOperator(actor.TelegramID) {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Operator dashboard", CallbackData: "admin:home"}})
	}
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) settingsKeyboard(actor service.Actor) telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{{{Text: "Home", CallbackData: "home"}}}
	if launchURL := s.appLaunchURL("plans", "", "", "", ""); launchURL != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{webAppButton("Open app", launchURL)})
	}
	if s.app != nil && s.app.IsOperator(actor.TelegramID) {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Operator dashboard", CallbackData: "admin:home"}})
	}
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) planActionKeyboard(view *domain.PlanView, actor service.Actor) telegram.InlineKeyboardMarkup {
	rows := make([][]telegram.InlineKeyboardButton, 0, 4)
	rows = append(rows, []telegram.InlineKeyboardButton{
		{Text: "Pay", CallbackData: "plan:" + view.Plan.ID + ":pay"},
		{Text: "Invoice", CallbackData: "plan:" + view.Plan.ID + ":invoice"},
	})
	if view.IsOwner || (s.app != nil && s.app.IsOperator(actor.TelegramID)) {
		rows = append(rows, []telegram.InlineKeyboardButton{
			{Text: "Invite", CallbackData: "plan:" + view.Plan.ID + ":invite"},
			{Text: "Members", CallbackData: "plan:" + view.Plan.ID + ":members"},
		})
		rows = append(rows, []telegram.InlineKeyboardButton{
			{Text: "Ledger", CallbackData: "plan:" + view.Plan.ID + ":ledger"},
			{Text: "Renew", CallbackData: "plan:" + view.Plan.ID + ":renew"},
		})
	} else {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Ledger", CallbackData: "plan:" + view.Plan.ID + ":ledger"}})
	}
	if launchURL := s.appLaunchURL("plans", view.Plan.ID, "", "", ""); launchURL != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{webAppButton("Manage in app", launchURL)})
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Support", CallbackData: "plan:" + view.Plan.ID + ":support"}, {Text: "Home", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) startMessage(actor service.Actor) string {
	operatorLine := ""
	if s.app != nil && s.app.IsOperator(actor.TelegramID) {
		operatorLine = "\n\nYou also have operator access for support and renewal triage."
	}
	return fmt.Sprintf(
		"Subscription sharing bot %s\n\nOpen the Mini App for the fastest path through plans, invoices, and support. Use approved family or team subscriptions only. Prices stay anchored in USDC, every billed share shows the 10%% platform fee separately, billing runs monthly, late renewals get a 3-day grace period, and the bot never sends raw third-party passwords.%s",
		appversion.Display(),
		operatorLine,
	)
}

func (s *Service) settingsMessage(actor service.Actor) string {
	operator := "no"
	if s.app != nil && s.app.IsOperator(actor.TelegramID) {
		operator = "yes"
	}
	return fmt.Sprintf(
		"Rules and defaults:\n- Pricing anchor: USDC\n- Platform fee: 10%% shown separately on every invoice\n- Billing cadence: monthly only\n- Renewal reminders: 7, 3, and 1 day before due\n- Grace period: 3 days after renewal\n- Access policy: approved services only, no password sharing in chat\n- Default payment option: %s on %s\n- Operator access: %s",
		s.defaultPayAsset,
		s.defaultPayNetwork,
		operator,
	)
}

func (s *Service) createPlanConfirmMessage(state createPlanState) string {
	return fmt.Sprintf(
		"Review this plan:\n\nService: %s\nMonthly total: %.2f USDC\nSeats: %d\nRenewal date: %s\nAccess mode: %s\nPolicy: %s\n\nPress Create plan to confirm that this subscription is allowed to be shared through official invites or seats.",
		state.ServiceName,
		units(state.TotalPriceMinor),
		state.SeatLimit,
		state.RenewalDate,
		state.AccessMode,
		state.SharingPolicyNote,
	)
}

func (s *Service) planCreatedMessage(plan domain.Plan, invite domain.PlanInvite) string {
	lines := []string{
		fmt.Sprintf("%s plan created.", plan.ServiceName),
		fmt.Sprintf("Plan ID: %s", plan.ID),
		fmt.Sprintf("Monthly total: %.2f USDC", units(plan.TotalPriceMinor)),
		fmt.Sprintf("Per seat before fee: %.2f USDC", units(plan.PerSeatBaseMinor)),
		"Platform fee: 10%",
		fmt.Sprintf("Invite code: %s", invite.InviteCode),
	}
	if joinURL := s.telegramStartAppURL("join-" + invite.InviteCode); joinURL != "" {
		lines = append(lines, "Join in app: "+joinURL)
	}
	return strings.Join(lines, "\n")
}

func (s *Service) planSummaryMessage(view *domain.PlanView) string {
	lines := []string{
		fmt.Sprintf("%s", view.Plan.ServiceName),
		fmt.Sprintf("Plan ID: %s", view.Plan.ID),
		fmt.Sprintf("Monthly total: %.2f USDC", units(view.Plan.TotalPriceMinor)),
		fmt.Sprintf("Seats filled: %d/%d", view.MemberCount, view.Plan.SeatLimit),
		fmt.Sprintf("Access mode: %s", view.Plan.AccessMode),
	}
	if view.Membership != nil {
		lines = append(lines, fmt.Sprintf("Your seat: %s", view.Membership.SeatStatus))
	}
	if view.OpenInvoice != nil {
		lines = append(lines, fmt.Sprintf("Latest invoice: %s (%s)", view.OpenInvoice.ID, view.OpenInvoice.Status))
	}
	if view.IsOwner {
		lines = append(lines, "You are the owner of this plan.")
	}
	return strings.Join(lines, "\n")
}

func (s *Service) invoiceMessage(invoice domain.Invoice) string {
	quoteExpiry := "not set"
	if invoice.QuoteExpiresAt != nil {
		quoteExpiry = invoice.QuoteExpiresAt.UTC().Format(time.RFC3339)
	}
	lines := []string{
		fmt.Sprintf("Invoice %s", invoice.ID),
		fmt.Sprintf("Status: %s", invoice.Status),
		fmt.Sprintf("Base: %.2f USDC", units(invoice.BaseMinor)),
		fmt.Sprintf("Platform fee: %.2f USDC", units(invoice.FeeMinor)),
		fmt.Sprintf("Credit applied: %.2f USDC", units(invoice.CreditAppliedMinor)),
		fmt.Sprintf("Paid: %.2f USDC", units(invoice.PaidMinor)),
		fmt.Sprintf("Still due: %.2f USDC", units(invoice.AmountDueMinor()-invoice.PaidMinor)),
	}
	if strings.TrimSpace(invoice.PayAsset) != "" {
		lines = append(lines,
			fmt.Sprintf("Pay asset: %s", invoice.PayAsset),
			fmt.Sprintf("Network: %s", invoice.Network),
			fmt.Sprintf("Quoted amount: %s", invoice.QuotedPayAmount),
			fmt.Sprintf("Reference: %s", invoice.PaymentRef),
			fmt.Sprintf("Quote expires: %s", quoteExpiry),
		)
	}
	return strings.Join(lines, "\n")
}

func (s *Service) invoiceKeyboard(invoice domain.Invoice) telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{
		{{Text: "Pay", CallbackData: "invoice:" + invoice.ID + ":pay"}, {Text: "Refresh", CallbackData: "invoice:" + invoice.ID + ":refresh"}},
		{{Text: "Plan", CallbackData: "plan:" + invoice.PlanID}, {Text: "Home", CallbackData: "home"}},
	}
	if launchURL := s.appLaunchURL("invoice", invoice.PlanID, invoice.ID, "", ""); launchURL != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{webAppButton("Pay in app", launchURL)})
	}
	if s.simulateEnabled && strings.TrimSpace(invoice.QuotedPayAmount) != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Simulate payment", CallbackData: "pay:simulate:" + invoice.ID}})
	}
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func plansKeyboard(views []domain.PlanView, page int, totalPages int) telegram.InlineKeyboardMarkup {
	rows := make([][]telegram.InlineKeyboardButton, 0, plansPageSize+2)
	start := page * plansPageSize
	end := start + plansPageSize
	if end > len(views) {
		end = len(views)
	}
	for _, view := range views[start:end] {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: view.Plan.ServiceName, CallbackData: "plan:" + view.Plan.ID}})
	}
	nav := make([]telegram.InlineKeyboardButton, 0, 3)
	if page > 0 {
		nav = append(nav, telegram.InlineKeyboardButton{Text: "Previous", CallbackData: fmt.Sprintf("plans:page:%d", page-1)})
	}
	if page+1 < totalPages {
		nav = append(nav, telegram.InlineKeyboardButton{Text: "Next", CallbackData: fmt.Sprintf("plans:page:%d", page+1)})
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Home", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func serviceCatalogKeyboard(entries []domain.ServiceCatalogEntry) telegram.InlineKeyboardMarkup {
	rows := make([][]telegram.InlineKeyboardButton, 0, len(entries)+1)
	for _, entry := range entries {
		rows = append(rows, []telegram.InlineKeyboardButton{{
			Text:         entry.DisplayName,
			CallbackData: "create_plan:service:" + entry.ServiceCode,
		}})
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Cancel", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func createPlanConfirmKeyboard() telegram.InlineKeyboardMarkup {
	return telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{
		{{Text: "Create plan", CallbackData: "create_plan:confirm"}},
		{{Text: "Cancel", CallbackData: "cancel"}},
	}}
}

func cancelKeyboard() telegram.InlineKeyboardMarkup {
	return telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{
		{{Text: "Cancel", CallbackData: "cancel"}},
	}}
}

func (s *Service) adminKeyboard() telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{
		{{Text: "Open support", CallbackData: "admin:support"}, {Text: "Renewal issues", CallbackData: "admin:issues"}},
		{{Text: "Payout due", CallbackData: "admin:payouts"}, {Text: "Payment alerts", CallbackData: "admin:alerts"}},
		{{Text: "Deny list", CallbackData: "admin:blocklist"}, {Text: "Recent plans", CallbackData: "admin:plans"}},
		{{Text: "Refresh counts", CallbackData: "admin:home"}},
	}
	if launchURL := s.adminLaunchURL("overview"); launchURL != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{webAppButton("Open operator app", launchURL)})
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Home", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) adminBlocklistKeyboard() telegram.InlineKeyboardMarkup {
	rows := [][]telegram.InlineKeyboardButton{
		{{Text: "Block user", CallbackData: "admin:block:add:user"}, {Text: "Refresh list", CallbackData: "admin:blocklist"}},
		{{Text: "Dashboard", CallbackData: "admin:home"}},
	}
	if launchURL := s.adminLaunchURL("denylist"); launchURL != "" {
		rows = append(rows, []telegram.InlineKeyboardButton{webAppButton("Open operator app", launchURL)})
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Home", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func payAssetKeyboard(invoiceID string, assets []string) telegram.InlineKeyboardMarkup {
	rows := make([][]telegram.InlineKeyboardButton, 0, len(assets)+1)
	for _, asset := range assets {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: asset, CallbackData: "pay:asset:" + invoiceID + ":" + asset}})
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Home", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func payNetworkKeyboard(invoiceID string, asset string, networks []string) telegram.InlineKeyboardMarkup {
	rows := make([][]telegram.InlineKeyboardButton, 0, len(networks)+1)
	for _, network := range networks {
		rows = append(rows, []telegram.InlineKeyboardButton{{Text: network, CallbackData: "pay:quote:" + invoiceID + ":" + strings.ToUpper(asset) + ":" + strings.ToLower(network)}})
	}
	rows = append(rows, []telegram.InlineKeyboardButton{{Text: "Back", CallbackData: "invoice:" + invoiceID + ":pay"}, {Text: "Home", CallbackData: "home"}})
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func parseCommand(text string) (string, []string) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 {
		return "", nil
	}
	command := strings.ToLower(parts[0])
	if idx := strings.Index(command, "@"); idx >= 0 {
		command = command[:idx]
	}
	return command, parts[1:]
}

func bestUsername(user *telegram.User) string {
	if user == nil {
		return ""
	}
	if strings.TrimSpace(user.Username) != "" {
		return strings.TrimSpace(user.Username)
	}
	return strings.TrimSpace(user.FirstName)
}

func parseMoneyMinor(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("empty amount")
	}
	negative := false
	if strings.HasPrefix(value, "-") {
		negative = true
		value = strings.TrimPrefix(value, "-")
	}
	parts := strings.SplitN(value, ".", 3)
	if len(parts) > 2 {
		return 0, fmt.Errorf("too many decimal points")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid whole number")
	}
	var fraction int64
	if len(parts) == 2 {
		cents := parts[1]
		if len(cents) > 2 {
			cents = cents[:2]
		}
		for len(cents) < 2 {
			cents += "0"
		}
		fraction, err = strconv.ParseInt(cents, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid decimal amount")
		}
	}
	total := whole*100 + fraction
	if negative {
		total *= -1
	}
	return total, nil
}

func units(minor int64) float64 {
	return float64(minor) / 100
}

func looksLikeInvoiceID(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return strings.HasPrefix(value, "invoice-") || strings.HasPrefix(value, "invoice_")
}

func unknownCommandMessage() string {
	return "I didn't recognize that command. Use /start for the guided menu, or /help for the main buttons."
}
