package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rigassatiksmeqrbot/internal/bot"
	"rigassatiksmeqrbot/internal/telegram"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	tg := telegram.NewClient(cfg.BotToken, cfg.HTTPTimeout)
	if err := tg.SetMyCommands(ctx, []telegram.BotCommand{
		{Command: "start", Description: "How to request a QR image"},
		{Command: "help", Description: "Show QR bot help"},
		{Command: "qr", Description: "Request QR image: /qr 12345"},
		{Command: "status", Description: "Check your latest QR request"},
		{Command: "cancel", Description: "Cancel your latest QR request"},
		{Command: "access", Description: "Check ticket access and quota"},
		{Command: "admin", Description: "Show admin access commands"},
	}); err != nil {
		log.Printf("set bot commands failed: %v", err)
	}

	access, err := buildAccessManager(cfg, telegramUsernameResolver{client: tg})
	if err != nil {
		log.Fatalf("access config: %v", err)
	}
	service := bot.NewService(
		bot.ServiceConfig{PollInterval: cfg.JobPollInterval, PollTimeout: cfg.JobPollTimeout, Access: access},
		tg,
		bot.NewHTTPBroker(cfg.BrokerBaseURL, cfg.HTTPTimeout),
	)
	log.Printf("rigassatiksme QR bot started")
	runLongPoll(ctx, tg, service, cfg.LongPollTimeout)
}

type config struct {
	BotToken              string
	BrokerBaseURL         string
	LongPollTimeout       int
	HTTPTimeout           time.Duration
	JobPollInterval       time.Duration
	JobPollTimeout        time.Duration
	AccessStatePath       string
	AccessDefaultOpen     bool
	DefaultUserDailyLimit int
	AdminUserIDs          []string
	SpacetimeHost         string
	SpacetimeDatabase     string
	SpacetimeBearerToken  string
}

func loadConfig() (config, error) {
	longPoll, err := envInt("LONG_POLL_TIMEOUT", 30)
	if err != nil {
		return config{}, err
	}
	httpTimeout, err := envDuration("HTTP_TIMEOUT", 45*time.Second)
	if err != nil {
		return config{}, err
	}
	jobPollInterval, err := envDuration("RIGASATIKSME_QR_JOB_POLL_INTERVAL", 250*time.Millisecond)
	if err != nil {
		return config{}, err
	}
	jobPollTimeout, err := envDuration("RIGASATIKSME_QR_JOB_POLL_TIMEOUT", 10*time.Minute)
	if err != nil {
		return config{}, err
	}
	botToken := env("RIGASATIKSME_QR_BOT_TOKEN", "")
	if botToken == "" {
		botToken = env("BOT_TOKEN", "")
	}
	if botToken == "" {
		return config{}, fmt.Errorf("RIGASATIKSME_QR_BOT_TOKEN or BOT_TOKEN is required")
	}
	accessDefaultOpen, err := envBool("RIGASATIKSME_QR_ACCESS_DEFAULT_OPEN", false)
	if err != nil {
		return config{}, err
	}
	defaultUserDailyLimit, err := envInt("RIGASATIKSME_QR_DEFAULT_USER_DAILY_LIMIT", 20)
	if err != nil {
		return config{}, err
	}
	return config{
		BotToken:              botToken,
		BrokerBaseURL:         strings.TrimRight(env("PHONE_BROKER_BASE_URL", "http://phone_broker:9398"), "/"),
		LongPollTimeout:       longPoll,
		HTTPTimeout:           httpTimeout,
		JobPollInterval:       jobPollInterval,
		JobPollTimeout:        jobPollTimeout,
		AccessStatePath:       env("RIGASATIKSME_QR_ACCESS_STATE_PATH", "/srv/rigassatiksme-qr-bot/state/access.json"),
		AccessDefaultOpen:     accessDefaultOpen,
		DefaultUserDailyLimit: defaultUserDailyLimit,
		AdminUserIDs:          envList("RIGASATIKSME_QR_ADMIN_USER_IDS"),
		SpacetimeHost:         env("RIGASATIKSME_QR_SPACETIME_HOST", ""),
		SpacetimeDatabase:     env("RIGASATIKSME_QR_SPACETIME_DATABASE", ""),
		SpacetimeBearerToken:  env("RIGASATIKSME_QR_SPACETIME_TOKEN", ""),
	}, nil
}

func buildAccessManager(cfg config, usernameResolver bot.UsernameResolver) (bot.AccessController, error) {
	var remote bot.AccessRemoteStore
	if cfg.SpacetimeHost != "" || cfg.SpacetimeDatabase != "" || cfg.SpacetimeBearerToken != "" {
		store, err := bot.NewSpacetimeAccessStore(bot.SpacetimeAccessConfig{
			Host:        cfg.SpacetimeHost,
			Database:    cfg.SpacetimeDatabase,
			BearerToken: cfg.SpacetimeBearerToken,
			HTTPTimeout: cfg.HTTPTimeout,
		})
		if err != nil {
			return nil, err
		}
		remote = store
	}
	return bot.NewAccessManager(bot.AccessConfig{
		AdminUserIDs:          cfg.AdminUserIDs,
		DefaultOpen:           cfg.AccessDefaultOpen,
		DefaultUserDailyLimit: cfg.DefaultUserDailyLimit,
		StatePath:             cfg.AccessStatePath,
		Remote:                remote,
		UsernameResolver:      usernameResolver,
	})
}

type chatGetter interface {
	GetChat(ctx context.Context, chatID string) (telegram.Chat, error)
}

type telegramUsernameResolver struct {
	client chatGetter
}

func (r telegramUsernameResolver) ResolveUsername(ctx context.Context, username string) (bot.ResolvedUsername, bool, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" || r.client == nil {
		return bot.ResolvedUsername{}, false, nil
	}
	chat, err := r.client.GetChat(ctx, "@"+username)
	if err != nil {
		if isTelegramUsernameLookupMiss(err) {
			return bot.ResolvedUsername{}, false, nil
		}
		return bot.ResolvedUsername{}, false, err
	}
	if chat.ID == 0 || (chat.Type != "" && chat.Type != "private") {
		return bot.ResolvedUsername{}, false, nil
	}
	resolvedUsername := strings.TrimPrefix(strings.TrimSpace(chat.Username), "@")
	if resolvedUsername == "" {
		resolvedUsername = username
	}
	return bot.ResolvedUsername{UserID: strconv.FormatInt(chat.ID, 10), Username: resolvedUsername}, true, nil
}

func isTelegramUsernameLookupMiss(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "chat not found") || strings.Contains(message, "user not found") || strings.Contains(message, "bad request")
}

func runLongPoll(ctx context.Context, tg *telegram.Client, service *bot.Service, timeout int) {
	var offset int64
	for ctx.Err() == nil {
		updates, err := tg.GetUpdates(ctx, offset, timeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("telegram getUpdates failed: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.Message == nil || update.Message.From == nil {
				continue
			}
			msg := botMessageFromTelegram(update.Message)
			if err := service.HandleMessage(ctx, msg); err != nil {
				log.Printf("handle message failed: %v", err)
			}
		}
	}
}

func botMessageFromTelegram(message *telegram.Message) bot.Message {
	if message == nil {
		return bot.Message{}
	}
	msg := bot.Message{
		ChatID:   message.Chat.ID,
		ChatType: message.Chat.Type,
		Text:     message.Text,
	}
	if message.From != nil {
		msg.UserID = message.From.ID
		msg.Username = message.From.Username
	}
	msg.MentionedUsers = mentionedUsersFromTelegram(message.Entities)
	return msg
}

func mentionedUsersFromTelegram(entities []telegram.MessageEntity) []bot.MentionedUser {
	if len(entities) == 0 {
		return nil
	}
	out := make([]bot.MentionedUser, 0, len(entities))
	for _, entity := range entities {
		if entity.User == nil || entity.User.ID == 0 || strings.TrimSpace(entity.User.Username) == "" {
			continue
		}
		out = append(out, bot.MentionedUser{UserID: entity.User.ID, Username: entity.User.Username})
	}
	return out
}

func env(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return parsed, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}

func envList(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	fields := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' })
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration", key)
	}
	return parsed, nil
}
