package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
)

func TestOpenAppButtonRowUsesConfiguredMiniAppURL(t *testing.T) {
	t.Parallel()

	service := &Service{
		catalog:   i18n.NewCatalog(),
		webAppURL: "https://example.test/pixel-stack/train",
	}

	row := service.openAppButtonRow(domain.LanguageEN)
	if len(row) != 1 {
		t.Fatalf("expected one button, got %d", len(row))
	}

	webApp, ok := row[0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected web_app payload, got %#v", row[0]["web_app"])
	}
	if webApp["url"] != "https://example.test/pixel-stack/train/app" {
		t.Fatalf("unexpected web app url: %q", webApp["url"])
	}
}

func TestOpenAppButtonRowUsesRootHostedMiniAppURL(t *testing.T) {
	t.Parallel()

	service := &Service{
		catalog:   i18n.NewCatalog(),
		webAppURL: "https://train-bot.jolkins.id.lv",
	}

	row := service.openAppButtonRow(domain.LanguageEN)
	if len(row) != 1 {
		t.Fatalf("expected one button, got %d", len(row))
	}

	webApp, ok := row[0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected web_app payload, got %#v", row[0]["web_app"])
	}
	if webApp["url"] != "https://train-bot.jolkins.id.lv/app" {
		t.Fatalf("unexpected web app url: %q", webApp["url"])
	}
}

func TestOpenAppButtonRowReturnsNilWithoutBaseURL(t *testing.T) {
	t.Parallel()

	service := &Service{catalog: i18n.NewCatalog()}
	if row := service.openAppButtonRow(domain.LanguageEN); row != nil {
		t.Fatalf("expected nil row when web app URL is unset, got %#v", row)
	}
}

func TestOpenIncidentsButtonRowUsesConfiguredPublicURL(t *testing.T) {
	t.Parallel()

	service := &Service{
		catalog:   i18n.NewCatalog(),
		webAppURL: "https://train-bot.jolkins.id.lv",
	}

	row := service.openIncidentsButtonRow(domain.LanguageEN)
	if len(row) != 1 {
		t.Fatalf("expected one button, got %d", len(row))
	}

	webApp, ok := row[0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected web_app payload, got %#v", row[0]["web_app"])
	}
	if webApp["url"] != "https://train-bot.jolkins.id.lv/incidents" {
		t.Fatalf("unexpected incidents url: %q", webApp["url"])
	}
}

func TestConfigureBotSetsCommandsAndMenuButton(t *testing.T) {
	t.Parallel()

	recorder, client, closeFn := newTelegramRecorder(t)
	defer closeFn()

	service := NewService(
		client,
		nil,
		nil,
		nil,
		nil,
		nil,
		i18n.NewCatalog(),
		time.UTC,
		1,
		true,
		"https://train-bot.jolkins.id.lv",
	)

	service.configureBot(context.Background())

	commandsReq := recorder.lastRequest(t, "/setMyCommands")
	rawCommands, ok := commandsReq.payload["commands"].([]any)
	if !ok {
		t.Fatalf("commands payload missing or wrong type: %T", commandsReq.payload["commands"])
	}
	if len(rawCommands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(rawCommands))
	}
	secondCommand, ok := rawCommands[1].(map[string]any)
	if !ok {
		t.Fatalf("second command wrong type: %T", rawCommands[1])
	}
	if secondCommand["command"] != "menu" {
		t.Fatalf("unexpected second command: %#v", secondCommand)
	}

	menuReq := recorder.lastRequest(t, "/setChatMenuButton")
	menuButton, ok := menuReq.payload["menu_button"].(map[string]any)
	if !ok {
		t.Fatalf("menu_button missing or wrong type: %T", menuReq.payload["menu_button"])
	}
	webApp, ok := menuButton["web_app"].(map[string]any)
	if !ok {
		t.Fatalf("menu_button.web_app missing or wrong type: %T", menuButton["web_app"])
	}
	if webApp["url"] != "https://train-bot.jolkins.id.lv/app" {
		t.Fatalf("unexpected menu button url: %#v", webApp["url"])
	}
}

func TestHandleMessageSupportsAddressedCommandVariantsAndIncidentsNotice(t *testing.T) {
	t.Parallel()

	h := newCheckinHarness(t)
	defer h.close()
	h.ensureEnglish(t, 7)
	h.service.webAppURL = "https://train-bot.jolkins.id.lv"

	for _, text := range []string{"/incidents", "/incidents@vivi_kontrole_bot"} {
		if err := h.service.handleMessage(context.Background(), &Message{
			Text: text,
			Chat: Chat{ID: 42},
			From: &User{ID: 7},
		}); err != nil {
			t.Fatalf("handleMessage(%q) error = %v", text, err)
		}
	}
	if err := h.service.handleMessage(context.Background(), &Message{
		Text: "/start@vivi_kontrole_bot",
		Chat: Chat{ID: 42},
		From: &User{ID: 7},
	}); err != nil {
		t.Fatalf("handleMessage(start) error = %v", err)
	}
	if err := h.service.handleMessage(context.Background(), &Message{
		Text: "/menu@vivi_kontrole_bot",
		Chat: Chat{ID: 42},
		From: &User{ID: 7},
	}); err != nil {
		t.Fatalf("handleMessage(menu) error = %v", err)
	}

	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	sendMessages := make([]recordedRequest, 0)
	for _, request := range h.recorder.requests {
		if request.path == "/sendMessage" {
			sendMessages = append(sendMessages, request)
		}
	}
	if len(sendMessages) != 6 {
		t.Fatalf("expected 6 sendMessage requests, got %d", len(sendMessages))
	}

	for i := 0; i < 2; i++ {
		text, _ := sendMessages[i].payload["text"].(string)
		if text != i18n.NewCatalog().T(domain.LanguageEN, "incidents_unavailable") {
			t.Fatalf("incidents message[%d] text = %q", i, text)
		}
		replyMarkup, ok := sendMessages[i].payload["reply_markup"].(map[string]any)
		if !ok {
			t.Fatalf("incidents message[%d] missing reply markup: %#v", i, sendMessages[i].payload["reply_markup"])
		}
		rows, ok := replyMarkup["keyboard"].([]any)
		if !ok || len(rows) == 0 {
			t.Fatalf("incidents keyboard[%d] malformed: %#v", i, replyMarkup)
		}
		firstRow, ok := rows[0].([]any)
		if !ok || len(firstRow) == 0 {
			t.Fatalf("incidents keyboard[%d] first row malformed: %#v", i, replyMarkup)
		}
		firstButton, ok := firstRow[0].(map[string]any)
		if !ok {
			t.Fatalf("incidents keyboard[%d] first button malformed: %#v", i, firstRow[0])
		}
		if firstButton["text"] != i18n.NewCatalog().T(domain.LanguageEN, "btn_open_app") {
			t.Fatalf("incidents keyboard[%d] first button = %#v", i, firstButton)
		}
	}

	startText, _ := sendMessages[2].payload["text"].(string)
	if startText != i18n.NewCatalog().T(domain.LanguageEN, "start") {
		t.Fatalf("start text = %q", startText)
	}
	startPromptText, _ := sendMessages[3].payload["text"].(string)
	if startPromptText != i18n.NewCatalog().T(domain.LanguageEN, "open_app_prompt") {
		t.Fatalf("start prompt text = %q", startPromptText)
	}

	menuText, _ := sendMessages[4].payload["text"].(string)
	if menuText != i18n.NewCatalog().T(domain.LanguageEN, "main_prompt") {
		t.Fatalf("menu text = %q", menuText)
	}
	menuPromptText, _ := sendMessages[5].payload["text"].(string)
	if menuPromptText != i18n.NewCatalog().T(domain.LanguageEN, "open_app_prompt") {
		t.Fatalf("menu prompt text = %q", menuPromptText)
	}
}

func TestHandleMessageHelpSendsHelpAndOpenAppPrompt(t *testing.T) {
	t.Parallel()

	h := newCheckinHarness(t)
	defer h.close()
	h.ensureEnglish(t, 7)
	h.service.webAppURL = "https://train-bot.jolkins.id.lv"

	if err := h.service.handleMessage(context.Background(), &Message{
		Text: "❓ Help",
		Chat: Chat{ID: 42},
		From: &User{ID: 7},
	}); err != nil {
		t.Fatalf("handleMessage(help) error = %v", err)
	}

	h.recorder.mu.Lock()
	defer h.recorder.mu.Unlock()
	sendMessages := make([]recordedRequest, 0)
	for _, request := range h.recorder.requests {
		if request.path == "/sendMessage" {
			sendMessages = append(sendMessages, request)
		}
	}
	if len(sendMessages) != 2 {
		t.Fatalf("expected 2 sendMessage requests, got %d", len(sendMessages))
	}
	helpText, _ := sendMessages[0].payload["text"].(string)
	if helpText != i18n.NewCatalog().T(domain.LanguageEN, "help") {
		t.Fatalf("help text = %q", helpText)
	}
	openAppText, _ := sendMessages[1].payload["text"].(string)
	if openAppText != i18n.NewCatalog().T(domain.LanguageEN, "open_app_prompt") {
		t.Fatalf("open app prompt text = %q", openAppText)
	}
}

func TestOpenAppPromptAndHelpMentionTelegramDeepLink(t *testing.T) {
	t.Parallel()

	recorder, client, closeFn := newTelegramRecorder(t)
	defer closeFn()

	service := NewService(
		client,
		nil,
		nil,
		nil,
		nil,
		nil,
		i18n.NewCatalog(),
		time.UTC,
		1,
		true,
		"https://train-bot.jolkins.id.lv",
	)

	if err := service.sendOpenAppPrompt(context.Background(), 42, domain.LanguageEN); err != nil {
		t.Fatalf("sendOpenAppPrompt error = %v", err)
	}
	if err := service.sendHelp(context.Background(), 42, domain.LanguageEN); err != nil {
		t.Fatalf("sendHelp error = %v", err)
	}

	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	sendMessages := make([]recordedRequest, 0)
	for _, request := range recorder.requests {
		if request.path == "/sendMessage" {
			sendMessages = append(sendMessages, request)
		}
	}
	if len(sendMessages) != 2 {
		t.Fatalf("expected 2 sendMessage requests, got %d", len(sendMessages))
	}

	for i, request := range sendMessages {
		text, _ := request.payload["text"].(string)
		if !strings.Contains(text, "https://t.me/vivi_kontrole_bot/app") {
			t.Fatalf("message[%d] missing Telegram deep link: %q", i, text)
		}
	}
}
