package bot

import (
	"context"
	"log"
	"math/rand"
	"strings"
	"time"

	"satiksmebot/internal/runtime"
	"satiksmebot/internal/telegram"
)

type MessageClient interface {
	GetUpdates(ctx context.Context, offset int64, timeout int) ([]telegram.Update, error)
	SendMessage(ctx context.Context, chatID any, text string, opts telegram.MessageOptions) error
}

type BotConfigurator interface {
	SetMyCommands(ctx context.Context, commands []telegram.BotCommand) error
	SetChatMenuButton(ctx context.Context, button telegram.MenuButtonWebApp) error
}

type Service struct {
	client       MessageClient
	pollTimeout  int
	miniAppURL   string
	publicURL    string
	reportsURL   string
	runtimeState *runtime.State
	replyMarkup  telegram.ReplyKeyboardMarkup
	inlineMarkup telegram.InlineKeyboardMarkup
}

const (
	mainOpenMap     = "Atvērt satiksmes mapi"
	mainPublicSite  = "Publiskā mape"
	mainReportsFeed = "Ziņojumu kanāls"
)

func NewService(client MessageClient, pollTimeout int, miniAppURL, publicURL, reportsURL string, runtimeState *runtime.State) *Service {
	service := &Service{
		client:       client,
		pollTimeout:  pollTimeout,
		miniAppURL:   strings.TrimSpace(miniAppURL),
		publicURL:    strings.TrimSpace(publicURL),
		reportsURL:   strings.TrimSpace(reportsURL),
		runtimeState: runtimeState,
	}
	service.replyMarkup = service.newReplyKeyboard()
	service.inlineMarkup = service.newInlineKeyboard()
	return service
}

func (s *Service) Start(ctx context.Context) error {
	s.configureBot(ctx)

	var offset int64
	backoff := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		updates, err := s.client.GetUpdates(ctx, offset, s.pollTimeout)
		if err != nil {
			log.Printf("telegram getUpdates error: %v", err)
			if s.runtimeState != nil {
				s.runtimeState.RecordTelegramError(time.Now().UTC(), err.Error())
			}
			sleepWithContext(ctx, backoff+jitterDuration(250*time.Millisecond))
			backoff = nextTelegramBackoff(backoff)
			continue
		}
		backoff = 2 * time.Second
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.Message == nil || update.Message.From == nil {
				continue
			}
			if err := s.handleMessage(ctx, *update.Message); err != nil {
				log.Printf("handle message %d error: %v", update.Message.MessageID, err)
			}
		}
		if s.runtimeState != nil {
			s.runtimeState.RecordTelegramSuccess(time.Now().UTC(), offset-1)
		}
	}
}

func (s *Service) handleMessage(ctx context.Context, message telegram.Message) error {
	text := strings.TrimSpace(message.Text)
	switch text {
	case "/start", "/menu", "":
		return s.sendWelcome(ctx, message.Chat.ID)
	case mainPublicSite:
		return s.client.SendMessage(ctx, message.Chat.ID, s.publicURL, telegram.MessageOptions{ReplyMarkup: s.replyMarkup})
	case mainReportsFeed:
		if s.reportsURL == "" {
			return s.client.SendMessage(ctx, message.Chat.ID, "Ziņojumu kanāls nav konfigurēts.", telegram.MessageOptions{ReplyMarkup: s.replyMarkup})
		}
		return s.client.SendMessage(ctx, message.Chat.ID, s.reportsURL, telegram.MessageOptions{ReplyMarkup: s.replyMarkup})
	case mainOpenMap:
		return s.sendWelcome(ctx, message.Chat.ID)
	default:
		return s.sendWelcome(ctx, message.Chat.ID)
	}
}

func (s *Service) sendWelcome(ctx context.Context, chatID int64) error {
	lines := []string{
		"Rīgas Satiksmes kontroles mape.",
		"Mini lietotnē vari redzēt tuvākās pieturas, tiešraides atiešanas laikus un iesniegt kontroles ziņojumus.",
	}
	if s.publicURL != "" {
		lines = append(lines, "Publiskā mape: "+s.publicURL)
	}
	if s.reportsURL != "" {
		lines = append(lines, "Ziņojumu kanāls: "+s.reportsURL)
	}
	return s.client.SendMessage(ctx, chatID, strings.Join(lines, "\n"), telegram.MessageOptions{
		ReplyMarkup: s.welcomeMarkup(),
	})
}

func (s *Service) configureBot(ctx context.Context) {
	configurator, ok := s.client.(BotConfigurator)
	if !ok {
		return
	}
	commands := []telegram.BotCommand{
		{Command: "start", Description: "Atvērt satiksmes mini lietotni"},
		{Command: "menu", Description: "Parādīt satiksmes saites"},
	}
	if err := configurator.SetMyCommands(ctx, commands); err != nil {
		log.Printf("telegram setMyCommands error: %v", err)
	}
	if s.miniAppURL == "" {
		return
	}
	if err := configurator.SetChatMenuButton(ctx, telegram.MenuButtonWebApp{
		Type:   "web_app",
		Text:   "Atvērt mini lietotni",
		WebApp: &telegram.WebAppInfo{URL: s.miniAppURL},
	}); err != nil {
		log.Printf("telegram setChatMenuButton error: %v", err)
	}
}

func (s *Service) welcomeMarkup() any {
	if len(s.inlineMarkup.InlineKeyboard) > 0 && len(s.inlineMarkup.InlineKeyboard[0]) > 0 {
		return s.inlineMarkup
	}
	return s.replyMarkup
}

func (s *Service) newReplyKeyboard() telegram.ReplyKeyboardMarkup {
	firstRow := []telegram.KeyboardButton{}
	if s.miniAppURL != "" {
		firstRow = append(firstRow, telegram.KeyboardButton{
			Text:   mainOpenMap,
			WebApp: &telegram.WebAppInfo{URL: s.miniAppURL},
		})
	} else {
		firstRow = append(firstRow, telegram.KeyboardButton{Text: mainOpenMap})
	}
	return telegram.ReplyKeyboardMarkup{
		Keyboard: [][]telegram.KeyboardButton{
			firstRow,
			{{Text: mainPublicSite}, {Text: mainReportsFeed}},
		},
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
}

func (s *Service) newInlineKeyboard() telegram.InlineKeyboardMarkup {
	row := []telegram.InlineKeyboardButton{}
	if s.miniAppURL != "" {
		row = append(row, telegram.InlineKeyboardButton{
			Text:   "Mini lietotne",
			WebApp: &telegram.WebAppInfo{URL: s.miniAppURL},
		})
	}
	if s.publicURL != "" {
		row = append(row, telegram.InlineKeyboardButton{Text: "Publiskā mape", URL: s.publicURL})
	}
	if s.reportsURL != "" {
		row = append(row, telegram.InlineKeyboardButton{Text: "Ziņojumi", URL: s.reportsURL})
	}
	if len(row) == 0 {
		return telegram.InlineKeyboardMarkup{}
	}
	return telegram.InlineKeyboardMarkup{InlineKeyboard: [][]telegram.InlineKeyboardButton{row}}
}

func nextTelegramBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return 2 * time.Second
	}
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func jitterDuration(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(limit)))
}

func sleepWithContext(ctx context.Context, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
