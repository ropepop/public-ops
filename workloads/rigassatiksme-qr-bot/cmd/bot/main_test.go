package main

import (
	"context"
	"testing"
	"time"

	"rigassatiksmeqrbot/internal/telegram"
)

func TestLoadConfigDefaultsUseLowLatencyJobPolling(t *testing.T) {
	t.Setenv("RIGASATIKSME_QR_BOT_TOKEN", "test-token")
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("RIGASATIKSME_QR_JOB_POLL_INTERVAL", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.JobPollInterval != 250*time.Millisecond {
		t.Fatalf("JobPollInterval = %s, want 250ms", cfg.JobPollInterval)
	}
}

func TestLoadConfigDefaultUserDailyLimitFromEnv(t *testing.T) {
	t.Setenv("RIGASATIKSME_QR_BOT_TOKEN", "test-token")
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("RIGASATIKSME_QR_DEFAULT_USER_DAILY_LIMIT", "8")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.DefaultUserDailyLimit != 8 {
		t.Fatalf("DefaultUserDailyLimit = %d, want 8", cfg.DefaultUserDailyLimit)
	}
}

func TestTelegramUsernameResolverLooksUpAtUsernameWithGetChat(t *testing.T) {
	client := &fakeChatGetter{chat: telegram.Chat{ID: 42, Type: "private", Username: "darja_smm_prod"}}
	resolver := telegramUsernameResolver{client: client}

	resolved, ok, err := resolver.ResolveUsername(t.Context(), "@darja_smm_prod")
	if err != nil {
		t.Fatalf("ResolveUsername: %v", err)
	}
	if !ok {
		t.Fatal("ResolveUsername ok=false, want true")
	}
	if client.chatID != "@darja_smm_prod" {
		t.Fatalf("GetChat chatID = %q, want @darja_smm_prod", client.chatID)
	}
	if resolved.UserID != "42" || resolved.Username != "darja_smm_prod" {
		t.Fatalf("resolved = %#v, want user 42 @darja_smm_prod", resolved)
	}
}

func TestBotMessageFromTelegramCapturesTextMentionUserIDs(t *testing.T) {
	msg := botMessageFromTelegram(&telegram.Message{
		Chat: telegram.Chat{ID: 7, Type: "private"},
		From: &telegram.User{ID: 7, Username: "admin"},
		Text: "/admin add @darja_smm_prod",
		Entities: []telegram.MessageEntity{{
			Type: "text_mention",
			User: &telegram.User{ID: 42, Username: "darja_smm_prod"},
		}},
	})

	if len(msg.MentionedUsers) != 1 {
		t.Fatalf("MentionedUsers len = %d, want 1: %#v", len(msg.MentionedUsers), msg.MentionedUsers)
	}
	if msg.MentionedUsers[0].UserID != 42 || msg.MentionedUsers[0].Username != "darja_smm_prod" {
		t.Fatalf("MentionedUsers[0] = %#v, want user 42 @darja_smm_prod", msg.MentionedUsers[0])
	}
}

type fakeChatGetter struct {
	chatID string
	chat   telegram.Chat
	err    error
}

func (g *fakeChatGetter) GetChat(ctx context.Context, chatID string) (telegram.Chat, error) {
	g.chatID = chatID
	if g.err != nil {
		return telegram.Chat{}, g.err
	}
	return g.chat, nil
}
