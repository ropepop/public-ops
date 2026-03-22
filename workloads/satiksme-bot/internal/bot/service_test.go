package bot

import (
	"context"
	"strings"
	"testing"

	"satiksmebot/internal/telegram"
)

type fakeMessageClient struct {
	messages       []sentMessage
	configuredCmds []telegram.BotCommand
	configuredMenu *telegram.MenuButtonWebApp
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

func TestSendWelcomePrefersInlineWebAppMarkup(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://satiksme-bot.jolkins.id.lv/app",
		"https://satiksme-bot.jolkins.id.lv",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	if err := service.sendWelcome(context.Background(), 42); err != nil {
		t.Fatalf("sendWelcome() error = %v", err)
	}
	if len(client.messages) != 1 {
		t.Fatalf("len(messages) = %d, want 1", len(client.messages))
	}
	if !strings.Contains(client.messages[0].text, "Rīgas Satiksmes kontroles mape.") {
		t.Fatalf("welcome text = %q", client.messages[0].text)
	}
	markup, ok := client.messages[0].opts.ReplyMarkup.(telegram.InlineKeyboardMarkup)
	if !ok {
		t.Fatalf("reply markup type = %T, want telegram.InlineKeyboardMarkup", client.messages[0].opts.ReplyMarkup)
	}
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 3 {
		t.Fatalf("inline keyboard = %#v", markup.InlineKeyboard)
	}
	if markup.InlineKeyboard[0][0].WebApp == nil || markup.InlineKeyboard[0][0].WebApp.URL != "https://satiksme-bot.jolkins.id.lv/app" {
		t.Fatalf("mini app button = %#v", markup.InlineKeyboard[0][0])
	}
	if markup.InlineKeyboard[0][0].Text != "Mini lietotne" {
		t.Fatalf("mini app button text = %q", markup.InlineKeyboard[0][0].Text)
	}
}

func TestConfigureBotSetsCommandsAndMenuButton(t *testing.T) {
	client := &fakeMessageClient{}
	service := NewService(
		client,
		30,
		"https://satiksme-bot.jolkins.id.lv/app",
		"https://satiksme-bot.jolkins.id.lv",
		"https://t.me/satiksme_bot_reports",
		nil,
	)

	service.configureBot(context.Background())

	if len(client.configuredCmds) != 2 {
		t.Fatalf("len(configuredCmds) = %d, want 2", len(client.configuredCmds))
	}
	if client.configuredCmds[0].Description != "Atvērt satiksmes mini lietotni" {
		t.Fatalf("configured command description = %q", client.configuredCmds[0].Description)
	}
	if client.configuredMenu == nil {
		t.Fatalf("configuredMenu = nil")
	}
	if client.configuredMenu.WebApp == nil || client.configuredMenu.WebApp.URL != "https://satiksme-bot.jolkins.id.lv/app" {
		t.Fatalf("configuredMenu = %#v", client.configuredMenu)
	}
	if client.configuredMenu.Text != "Atvērt mini lietotni" {
		t.Fatalf("configuredMenu.Text = %q", client.configuredMenu.Text)
	}
}
