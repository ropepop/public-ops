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
		webAppURL: "https://vilciens.kontrole.info",
	}

	row := service.openAppButtonRow(domain.LanguageEN)
	if len(row) != 1 {
		t.Fatalf("expected one button, got %d", len(row))
	}

	webApp, ok := row[0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected web_app payload, got %#v", row[0]["web_app"])
	}
	if webApp["url"] != "https://vilciens.kontrole.info/app" {
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
		webAppURL: "https://vilciens.kontrole.info",
	}

	row := service.openIncidentsButtonRow(domain.LanguageEN)
	if len(row) != 1 {
		t.Fatalf("expected one button, got %d", len(row))
	}

	webApp, ok := row[0]["web_app"].(map[string]string)
	if !ok {
		t.Fatalf("expected web_app payload, got %#v", row[0]["web_app"])
	}
	if webApp["url"] != "https://vilciens.kontrole.info/incidents" {
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
		"https://vilciens.kontrole.info",
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
	if webApp["url"] != "https://vilciens.kontrole.info/app" {
		t.Fatalf("unexpected menu button url: %#v", webApp["url"])
	}
}

func TestHandleMessageSupportsAddressedCommandVariantsAndIncidentsNotice(t *testing.T) {
	t.Parallel()

	h := newCheckinHarness(t)
	defer h.close()
	h.ensureEnglish(t, 7)
	h.service.webAppURL = "https://vilciens.kontrole.info"

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
	if len(sendMessages) != 4 {
		t.Fatalf("expected one reply per command, got %d", len(sendMessages))
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
	channelURL := i18n.NewCatalog().T(domain.LanguageEN, "link_reports_channel")
	startMarkup := sendMessages[2].payload["reply_markup"].(map[string]any)
	startRows := startMarkup["inline_keyboard"].([]any)
	channelButton := startRows[len(startRows)-1].([]any)[0].(map[string]any)
	if channelButton["url"] != channelURL {
		t.Fatalf("start must retain the reports channel button: %v", channelButton)
	}
	menuPromptText, _ := sendMessages[3].payload["text"].(string)
	if menuPromptText != i18n.NewCatalog().T(domain.LanguageEN, "open_app_prompt") {
		t.Fatalf("menu prompt text = %q", menuPromptText)
	}
	menuMarkup := sendMessages[3].payload["reply_markup"].(map[string]any)
	menuRows, ok := menuMarkup["inline_keyboard"].([]any)
	if !ok || len(menuRows) != 2 {
		t.Fatalf("menu must provide inline app and reports channel buttons: %v", menuMarkup)
	}
	appButton := menuRows[0].([]any)[0].(map[string]any)
	if appButton["web_app"].(map[string]any)["url"] != "https://vilciens.kontrole.info/app" {
		t.Fatalf("menu must use the configured web app directly: %v", appButton)
	}
	if menuRows[1].([]any)[0].(map[string]any)["url"] != channelURL {
		t.Fatal("menu must retain the reports channel button")
	}
}

func TestHandleMessageHelpSendsHelpAndOpenAppPrompt(t *testing.T) {
	t.Parallel()

	h := newCheckinHarness(t)
	defer h.close()
	h.ensureEnglish(t, 7)
	h.service.webAppURL = "https://vilciens.kontrole.info"

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

func TestAppEntryMessagesDoNotAdvertiseUnconfiguredMiniApp(t *testing.T) {
	t.Parallel()
	catalog := i18n.NewCatalog()
	for _, lang := range []domain.Language{domain.LanguageEN, domain.LanguageLV} {
		for _, key := range []string{"start", "help", "open_app_prompt"} {
			text := catalog.T(lang, key)
			if strings.Contains(text, "?startapp") || strings.Contains(text, "vivi_kontrole_bot/app") {
				t.Fatalf("%s/%s advertises an unconfigured mini app: %q", lang, key, text)
			}
		}
	}
}
