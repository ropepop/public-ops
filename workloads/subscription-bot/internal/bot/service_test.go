package bot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"subscriptionbot/internal/config"
	"subscriptionbot/internal/payments"
	"subscriptionbot/internal/service"
	"subscriptionbot/internal/store"
	"subscriptionbot/internal/telegram"
)

type sentMessage struct {
	chatID int64
	text   string
	opts   telegram.MessageOptions
}

type editedMessage struct {
	chatID    int64
	messageID int64
	text      string
	opts      telegram.MessageOptions
}

type fakeClient struct {
	sent      []sentMessage
	edited    []editedMessage
	callbacks []string
	commands  []telegram.BotCommand
	menu      *telegram.MenuButton
}

func (f *fakeClient) GetUpdates(context.Context, int64, int) ([]telegram.Update, error) {
	return nil, nil
}
func (f *fakeClient) SetMyCommands(_ context.Context, commands []telegram.BotCommand) error {
	f.commands = append([]telegram.BotCommand(nil), commands...)
	return nil
}
func (f *fakeClient) SetChatMenuButton(_ context.Context, button telegram.MenuButton) error {
	copy := button
	f.menu = &copy
	return nil
}
func (f *fakeClient) SendMessage(_ context.Context, chatID int64, text string, opts telegram.MessageOptions) error {
	f.sent = append(f.sent, sentMessage{chatID: chatID, text: text, opts: opts})
	return nil
}
func (f *fakeClient) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, opts telegram.MessageOptions) error {
	f.edited = append(f.edited, editedMessage{chatID: chatID, messageID: messageID, text: text, opts: opts})
	return nil
}
func (f *fakeClient) AnswerCallbackQuery(_ context.Context, _ string, text string) error {
	f.callbacks = append(f.callbacks, text)
	return nil
}

func TestStartShowsGuidedHomeMenu(t *testing.T) {
	t.Parallel()

	svc, client, _ := newBotTestHarness(t)
	svc.configureBot(context.Background())
	if client.menu == nil || client.menu.Type != "commands" {
		t.Fatalf("expected commands menu button, got %+v", client.menu)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1001},
		From: &telegram.User{ID: 1001, Username: "owner"},
		Text: "/start",
	}); err != nil {
		t.Fatalf("start command: %v", err)
	}

	reply := client.sent[len(client.sent)-1]
	if !strings.Contains(reply.text, "Use approved family or team subscriptions only") {
		t.Fatalf("expected guided home text, got %s", reply.text)
	}
	keyboard := mustInlineKeyboard(t, reply.opts.ReplyMarkup)
	if webAppURL := findWebAppButtonURL(keyboard, "Open app"); webAppURL != "https://example.test/app?section=plans" {
		t.Fatalf("expected home keyboard app launch url, got %q", webAppURL)
	}
	if callback := keyboard.InlineKeyboard[1][0].CallbackData; callback != "create_plan:start" {
		t.Fatalf("expected create plan button, got %s", callback)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1001},
		From: &telegram.User{ID: 1001, Username: "owner"},
		Text: "/help",
	}); err != nil {
		t.Fatalf("help command: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "Subscription sharing bot") {
		t.Fatalf("expected help to reuse home screen, got %s", client.sent[len(client.sent)-1].text)
	}
}

func TestGuidedCreateJoinAndPayFlow(t *testing.T) {
	t.Parallel()

	svc, client, _ := newBotTestHarness(t)
	now := time.Date(2026, time.March, 12, 10, 0, 0, 0, time.FixedZone("EET", 2*60*60))
	svc.now = func() time.Time { return now }

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1001},
		From: &telegram.User{ID: 1001, Username: "owner"},
		Text: "/create_plan",
	}); err != nil {
		t.Fatalf("start create plan flow: %v", err)
	}

	if err := svc.handleCallbackQuery(context.Background(), &telegram.CallbackQuery{
		ID:   "cb1",
		From: &telegram.User{ID: 1001, Username: "owner"},
		Message: &telegram.Message{
			MessageID: 2001,
			Chat:      telegram.Chat{ID: 1001},
		},
		Data: "create_plan:service:spotify_family",
	}); err != nil {
		t.Fatalf("choose service: %v", err)
	}
	if len(client.edited) == 0 || !strings.Contains(client.edited[len(client.edited)-1].text, "Send the total monthly price") {
		t.Fatalf("expected edited price prompt, got %+v", client.edited)
	}

	for _, text := range []string{"18.00", "2", "2026-04-01"} {
		if err := svc.handleMessage(context.Background(), &telegram.Message{
			Chat: telegram.Chat{ID: 1001},
			From: &telegram.User{ID: 1001, Username: "owner"},
			Text: text,
		}); err != nil {
			t.Fatalf("flow input %q: %v", text, err)
		}
	}

	confirmMessage := client.sent[len(client.sent)-1].text
	if !strings.Contains(confirmMessage, "Review this plan") {
		t.Fatalf("expected confirmation step, got %s", confirmMessage)
	}

	if err := svc.handleCallbackQuery(context.Background(), &telegram.CallbackQuery{
		ID:   "cb2",
		From: &telegram.User{ID: 1001, Username: "owner"},
		Message: &telegram.Message{
			MessageID: 2002,
			Chat:      telegram.Chat{ID: 1001},
		},
		Data: "create_plan:confirm",
	}); err != nil {
		t.Fatalf("confirm create plan: %v", err)
	}

	createReply := client.sent[len(client.sent)-1].text
	if !strings.Contains(createReply, "Invite code:") {
		t.Fatalf("expected invite code message, got %s", createReply)
	}
	inviteCode := lineValue(createReply, "Invite code: ")
	planID := lineValue(createReply, "Plan ID: ")
	if !strings.Contains(createReply, "Join in app: https://t.me/farel_subscription_bot?startapp=join-"+inviteCode) {
		t.Fatalf("expected plan created message to include a Telegram startapp link, got %s", createReply)
	}
	planKeyboard := mustInlineKeyboard(t, client.sent[len(client.sent)-1].opts.ReplyMarkup)
	if manageURL := findWebAppButtonURL(planKeyboard, "Manage in app"); manageURL != "https://example.test/app?plan_id="+planID+"&section=plans&startapp=plan-"+planID {
		t.Fatalf("expected plan action keyboard app launch url, got %q", manageURL)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "/join",
	}); err != nil {
		t.Fatalf("start join flow: %v", err)
	}
	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: inviteCode,
	}); err != nil {
		t.Fatalf("finish join flow: %v", err)
	}
	joinReply := client.sent[len(client.sent)-1].text
	if !strings.Contains(joinReply, "First invoice:") {
		t.Fatalf("expected first invoice reply, got %s", joinReply)
	}
	invoiceID := strings.Fields(lineValue(joinReply, "First invoice: "))[0]

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "/pay " + invoiceID + " USDC solana",
	}); err != nil {
		t.Fatalf("pay command with invoice id: %v", err)
	}
	reply := client.sent[len(client.sent)-1]
	if !strings.Contains(reply.text, "Quoted amount:") || !strings.Contains(reply.text, "Pay asset: USDC") {
		t.Fatalf("expected quoted invoice reply, got %s", reply.text)
	}
	keyboard := mustInlineKeyboard(t, reply.opts.ReplyMarkup)
	if len(keyboard.InlineKeyboard) == 0 || keyboard.InlineKeyboard[0][0].CallbackData != "invoice:"+invoiceID+":pay" {
		t.Fatalf("expected in-chat invoice actions, got %+v", keyboard)
	}
	if webAppURL := findWebAppButtonURL(keyboard, "Pay in app"); webAppURL == "" || !strings.Contains(webAppURL, "invoice_id=") || !strings.Contains(webAppURL, "plan_id=") {
		t.Fatalf("expected invoice keyboard app launch url with invoice and plan ids, got %q", webAppURL)
	}
}

func TestCancelClearsActiveConversation(t *testing.T) {
	t.Parallel()

	svc, client, _ := newBotTestHarness(t)

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "/join",
	}); err != nil {
		t.Fatalf("join flow start: %v", err)
	}
	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "/cancel",
	}); err != nil {
		t.Fatalf("cancel flow: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "Cancelled the active flow") {
		t.Fatalf("expected cancel confirmation, got %s", client.sent[len(client.sent)-1].text)
	}
	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "SOMECODE",
	}); err != nil {
		t.Fatalf("unexpected plain text after cancel: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "Subscription sharing bot") {
		t.Fatalf("expected home screen after cancelled flow, got %s", client.sent[len(client.sent)-1].text)
	}
}

func TestAdminAccessIsOperatorOnly(t *testing.T) {
	t.Parallel()

	svc, client, _ := newBotTestHarness(t)

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "/admin",
	}); err != nil {
		t.Fatalf("member admin command: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "unauthorized") {
		t.Fatalf("expected member admin rejection, got %s", client.sent[len(client.sent)-1].text)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 9000},
		From: &telegram.User{ID: 9000, Username: "operator"},
		Text: "/admin",
	}); err != nil {
		t.Fatalf("operator admin command: %v", err)
	}
	reply := client.sent[len(client.sent)-1]
	if !strings.Contains(reply.text, "Operator dashboard") {
		t.Fatalf("expected operator dashboard, got %s", reply.text)
	}
	keyboard := mustInlineKeyboard(t, reply.opts.ReplyMarkup)
	if keyboard.InlineKeyboard[0][0].CallbackData != "admin:support" {
		t.Fatalf("expected operator dashboard buttons, got %+v", keyboard)
	}
	if keyboard.InlineKeyboard[1][0].CallbackData != "admin:payouts" {
		t.Fatalf("expected payout summary button, got %+v", keyboard)
	}
	if webAppURL := findWebAppButtonURL(keyboard, "Open operator app"); webAppURL != "https://example.test/admin?admin_view=overview&section=admin&startapp=admin-overview" {
		t.Fatalf("expected operator dashboard app launch url, got %q", webAppURL)
	}

	if err := svc.handleCallbackQuery(context.Background(), &telegram.CallbackQuery{
		ID:   "member-admin",
		From: &telegram.User{ID: 1002, Username: "member"},
		Message: &telegram.Message{
			MessageID: 2003,
			Chat:      telegram.Chat{ID: 1002},
		},
		Data: "admin:home",
	}); err != nil {
		t.Fatalf("member admin callback: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "unauthorized") {
		t.Fatalf("expected member admin callback rejection, got %s", client.sent[len(client.sent)-1].text)
	}
}

func TestOperatorCanBlockUserFromBillingActions(t *testing.T) {
	t.Parallel()

	svc, client, app := newBotTestHarness(t)

	if err := svc.handleCallbackQuery(context.Background(), &telegram.CallbackQuery{
		ID:   "blocklist",
		From: &telegram.User{ID: 9000, Username: "operator"},
		Message: &telegram.Message{
			MessageID: 3001,
			Chat:      telegram.Chat{ID: 9000},
		},
		Data: "admin:block:add:user",
	}); err != nil {
		t.Fatalf("start admin block flow: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "Telegram ID") {
		t.Fatalf("expected Telegram ID prompt, got %s", client.sent[len(client.sent)-1].text)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 9000},
		From: &telegram.User{ID: 9000, Username: "operator"},
		Text: "1002",
	}); err != nil {
		t.Fatalf("submit blocked user id: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "reason for blocking") {
		t.Fatalf("expected reason prompt, got %s", client.sent[len(client.sent)-1].text)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 9000},
		From: &telegram.User{ID: 9000, Username: "operator"},
		Text: "fraud review",
	}); err != nil {
		t.Fatalf("submit block reason: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "Blocked Telegram ID 1002") {
		t.Fatalf("expected block confirmation, got %s", client.sent[len(client.sent)-1].text)
	}

	blocked, reason, err := app.IsTelegramIDDenied(context.Background(), 1002)
	if err != nil {
		t.Fatalf("lookup blocked actor: %v", err)
	}
	if !blocked || !strings.Contains(reason, "fraud") {
		t.Fatalf("expected actor 1002 to be blocked, got blocked=%v reason=%q", blocked, reason)
	}

	if err := svc.handleMessage(context.Background(), &telegram.Message{
		Chat: telegram.Chat{ID: 1002},
		From: &telegram.User{ID: 1002, Username: "member"},
		Text: "/join SOMEINVITE",
	}); err != nil {
		t.Fatalf("blocked join command: %v", err)
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "blocked") {
		t.Fatalf("expected blocked actor rejection, got %s", client.sent[len(client.sent)-1].text)
	}
}

func TestJoinRateLimitRejectsSpam(t *testing.T) {
	t.Parallel()

	svc, client, _ := newBotTestHarness(t)
	ctx := context.Background()
	for idx := 0; idx < 4; idx++ {
		if err := svc.handleMessage(ctx, &telegram.Message{
			Chat: telegram.Chat{ID: 1002},
			From: &telegram.User{ID: 1002, Username: "member"},
			Text: "/join",
		}); err != nil {
			t.Fatalf("join attempt %d: %v", idx+1, err)
		}
	}
	if !strings.Contains(client.sent[len(client.sent)-1].text, "too many join attempts") {
		t.Fatalf("expected join rate limit warning, got %s", client.sent[len(client.sent)-1].text)
	}
}

func newBotTestHarness(t *testing.T) (*Service, *fakeClient, *service.App) {
	t.Helper()

	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	st, err := store.NewSQLiteStore(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := config.Config{
		PlatformFeeBps:      1000,
		GraceDays:           3,
		RenewalLeadDays:     7,
		ReminderDays:        []int{7, 3, 1},
		WebEnabled:          true,
		WebShellEnabled:     true,
		WebPublicBaseURL:    "https://example.test/pixel-stack/subscription",
		TelegramBotUsername: "farel_subscription_bot",
		DefaultPayAsset:     "USDC",
		DefaultPayNetwork:   "solana",
		AllowedPayAssets:    []string{"USDC", "USDT", "SOL"},
		AllowedPayNetworks: []string{
			"solana",
			"tron",
			"base",
			"bitcoin",
		},
		PaymentProvider:       "sandbox",
		RequiredConfirmations: 3,
		QuoteTTL:              15 * time.Minute,
		OperatorIDs:           map[int64]struct{}{9000: {}},
	}
	app := service.New(st.DB(), cfg, payments.NewSandboxProvider(st.DB()), loc)
	if err := app.SeedCatalog(context.Background()); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	client := &fakeClient{}
	svc := NewService(client, app, cfg)
	return svc, client, app
}

func mustInlineKeyboard(t *testing.T, markup any) telegram.InlineKeyboardMarkup {
	t.Helper()
	keyboard, ok := markup.(telegram.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("expected inline keyboard, got %#v", markup)
	}
	return keyboard
}

func trailingValue(text string, prefix string) string {
	index := strings.LastIndex(text, prefix)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(text[index+len(prefix):])
}

func lineValue(text string, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func findWebAppButtonURL(keyboard telegram.InlineKeyboardMarkup, text string) string {
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if button.Text == text && button.WebApp != nil {
				return button.WebApp.URL
			}
		}
	}
	return ""
}
