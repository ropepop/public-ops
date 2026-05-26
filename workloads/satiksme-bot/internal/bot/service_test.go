package bot

import (
	"context"
	"strings"
	"testing"

	"satiksmebot/internal/telegram"
)

type fakeMessageClient struct {
	messages                   []sentMessage
	configuredCmds             []telegram.BotCommand
	configuredMenu             *telegram.MenuButtonWebApp
	configuredName             string
	configuredShortDescription string
	configuredDescription      string
}

type sentMessage struct {
	chatID any
	text   string
	opts   telegram.MessageOptions
}

func (f *fakeMessageClient) GetUpdates(context.Context, int64, int) ([]telegram.Update, error) {
	return nil, nil
}

func (f *fakeMessageClient) SendMessage(_ context.Context, chatID any, text string, opts telegram.MessageOptions) error {
	f.messages = append(f.messages, sentMessage{chatID: chatID, text: text, opts: opts})
	return nil
}

func (f *fakeMessageClient) SetMyCommands(_ context.Context, commands []telegram.BotCommand) error {
	f.configuredCmds = append([]telegram.BotCommand{}, commands...)
	return nil
}

func (f *fakeMessageClient) SetChatMenuButton(_ context.Context, button telegram.MenuButtonWebApp) error {
	f.configuredMenu = &button
	return nil
}

func (f *fakeMessageClient) SetMyName(_ context.Context, name string) error {
	f.configuredName = name
	return nil
}

func (f *fakeMessageClient) SetMyShortDescription(_ context.Context, description string) error {
	f.configuredShortDescription = description
	return nil
}

func (f *fakeMessageClient) SetMyDescription(_ context.Context, description string) error {
	f.configuredDescription = description
	return nil
}

func TestSendWelcomePrefersInlineWebAppMarkup(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://kontrole.info/app",
		"https://kontrole.info",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	if err := service.sendWelcome(context.Background(), 42); err != nil {
		t.Fatalf("sendWelcome() error = %v", err)
	}
	if len(client.messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(client.messages))
	}
	if !strings.Contains(client.messages[0].text, "Kontrole — satiksmes karte") {
		t.Fatalf("welcome text = %q", client.messages[0].text)
	}
	if !strings.Contains(client.messages[0].text, "https://kontrole.info/app") {
		t.Fatalf("welcome text missing mini app url: %q", client.messages[0].text)
	}
	for _, forbidden := range []string{"/notiek", "/karte", "https://kontrole.info/incidents", "https://t.me/satiksme_bot_reports"} {
		if strings.Contains(client.messages[0].text, forbidden) {
			t.Fatalf("welcome text should be a minimal wrapper, found %q in %q", forbidden, client.messages[0].text)
		}
	}
	markup, ok := client.messages[0].opts.ReplyMarkup.(telegram.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("reply markup type = %T, want telegram.InlineKeyboardMarkup", client.messages[0].opts.ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 {
		t.Fatalf("inline keyboard = %#v", markup.InlineKeyboard)
	}
	if markup.InlineKeyboard[0][0].Text != mainOpenMap {
		t.Fatalf("website button text = %q", markup.InlineKeyboard[0][0].Text)
	}
	if markup.InlineKeyboard[0][0].WebApp == nil || markup.InlineKeyboard[0][0].WebApp.URL != "https://kontrole.info/app" {
		t.Fatalf("website button = %#v", markup.InlineKeyboard[0][0])
	}
}

func TestConfigureBotSetsCommandsMenuButtonAndMetadata(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://kontrole.info/app",
		"https://kontrole.info",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	service.configureBot(context.Background())

	if len(client.configuredCmds) != 2 {
		t.Fatalf("len(configuredCmds) = %d, want 2", len(client.configuredCmds))
	}
	if client.configuredCmds[0].Description != "Atvērt Kontroli" {
		t.Fatalf("configured command description = %q", client.configuredCmds[0].Description)
	}
	if client.configuredCmds[1].Command != "menu" {
		t.Fatalf("configured menu command = %#v", client.configuredCmds[1])
	}
	if client.configuredMenu == nil {
		t.Fatalf("configuredMenu = nil, want menu button")
	}
	if client.configuredMenu.Type != "web_app" || client.configuredMenu.Text != mainOpenMap || client.configuredMenu.WebApp == nil || client.configuredMenu.WebApp.URL != "https://kontrole.info/app" {
		t.Fatalf("configuredMenu = %#v", client.configuredMenu)
	}
	if client.configuredName != botName {
		t.Fatalf("configuredName = %q", client.configuredName)
	}
	if client.configuredShortDescription != botShortDescription {
		t.Fatalf("configuredShortDescription = %q", client.configuredShortDescription)
	}
	if client.configuredDescription != botDescription {
		t.Fatalf("configuredDescription = %q", client.configuredDescription)
	}
}

func TestHandleMessageRoutesLegacyCommandsToWebsiteWrapper(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://kontrole.info/app",
		"https://kontrole.info",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	for _, text := range []string{"/notiek", "/notiek@satiksmeb_bot", "/incidents", "/-incidents"} {
		err := service.handleMessage(context.Background(), telegram.Message{
			Text: text,
			Chat: telegram.Chat{ID: 42},
		})
		if err != nil {
			t.Fatalf("handleMessage(%q) error = %v", text, err)
		}
	}

	if len(client.messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(client.messages))
	}
	for i, message := range client.messages {
		if !strings.Contains(message.text, "https://kontrole.info/app") {
			t.Fatalf("message[%d] text = %q", i, message.text)
		}
		if strings.Contains(message.text, "/notiek") || strings.Contains(message.text, "https://kontrole.info/incidents") {
			t.Fatalf("message[%d] should not expose the old incidents shortcut: %q", i, message.text)
		}
	}
}

func TestHandleMessageAcceptsAddressedStartAndMenuCommands(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://kontrole.info/app",
		"https://kontrole.info",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	for _, text := range []string{"/start@satiksmeb_bot", "/menu@satiksmeb_bot"} {
		err := service.handleMessage(context.Background(), telegram.Message{
			Text: text,
			Chat: telegram.Chat{ID: 42},
		})
		if err != nil {
			t.Fatalf("handleMessage(%q) error = %v", text, err)
		}
	}

	if len(client.messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(client.messages))
	}
	for i, message := range client.messages {
		if !strings.Contains(message.text, "Kontrole — satiksmes karte") {
			t.Fatalf("message[%d] text = %q", i, message.text)
		}
		if strings.Contains(message.text, "/-incidents") {
			t.Fatalf("message[%d] unexpectedly mentioned legacy incidents alias: %q", i, message.text)
		}
	}
}

func TestHandleMessageShortcutButtonsReuseUnifiedMenu(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://kontrole.info/app",
		"https://kontrole.info",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	for _, text := range []string{mainOpenMap, legacyPublicSite, mainReportsFeed, mainIncidents} {
		err := service.handleMessage(context.Background(), telegram.Message{
			Text: text,
			Chat: telegram.Chat{ID: 42},
		})
		if err != nil {
			t.Fatalf("handleMessage(%q) error = %v", text, err)
		}
	}

	if len(client.messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(client.messages))
	}
	for i, message := range client.messages {
		if !strings.Contains(message.text, "https://kontrole.info/app") {
			t.Fatalf("message[%d] missing mini app url: %q", i, message.text)
		}
		for _, forbidden := range []string{incidentsCommand, mapCommand, "https://kontrole.info/incidents", "https://t.me/satiksme_bot_reports"} {
			if strings.Contains(message.text, forbidden) {
				t.Fatalf("message[%d] should be a minimal wrapper, found %q in %q", i, forbidden, message.text)
			}
		}
	}
}

func TestReplyKeyboardOnlyOpensMiniApp(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://kontrole.info/app",
		"https://kontrole.info",
		"",
		nil,
	)

	if len(service.replyMarkup.Keyboard) != 1 || len(service.replyMarkup.Keyboard[0]) != 1 {
		t.Fatalf("reply keyboard = %#v", service.replyMarkup.Keyboard)
	}
	button := service.replyMarkup.Keyboard[0][0]
	if button.Text != mainOpenMap || button.WebApp == nil || button.WebApp.URL != "https://kontrole.info/app" {
		t.Fatalf("reply keyboard button = %#v", button)
	}
}

func TestReplyKeyboardIsNotPersistent(t *testing.T) {
	service := NewService(
		&fakeMessageClient{},
		30,
		"https://kontrole.info",
		"https://kontrole.info",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	if service.replyMarkup.IsPersistent {
		t.Fatal("reply keyboard is persistent, want normal non-pinned keyboard")
	}
}

func TestResolveIncidentsURLFallsBackToLegacyAppBase(t *testing.T) {
	got := resolveIncidentsURL("https://kontrole.info/prefix/app", "")
	if got != "https://kontrole.info/prefix/incidents" {
		t.Fatalf("resolveIncidentsURL() = %q", got)
	}
}
