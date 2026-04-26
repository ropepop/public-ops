package bot

import (
	"context"
	"log"
	"math/rand"
	"net/url"
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
	SetMyName(ctx context.Context, name string) error
	SetMyShortDescription(ctx context.Context, description string) error
	SetMyDescription(ctx context.Context, description string) error
}

type Service struct {
	client       MessageClient
	pollTimeout  int
	appURL       string
	publicURL    string
	incidentsURL string
	reportsURL   string
	runtimeState *runtime.State
	replyMarkup  telegram.ReplyKeyboardMarkup
	inlineMarkup telegram.InlineKeyboardMarkup
}

const (
	mapCommand             = "/karte"
	incidentsCommand       = "/notiek"
	legacyIncidentsCommand = "/incidents"
	legacyPublicSite       = "Publiskā mape"
	mainOpenMap            = "Atvērt Kontroli"
	mainIncidents          = "Kontroles plūsma"
	mainReportsFeed        = "Ziņojumu kanāls"
	botName                = "Kontrole"
	botShortDescription    = "Satiksmes karte un kontroles ziņojumi Rīgā."
	botDescription         = "Kontrole parāda Rīgas satiksmes karti, aktīvo transportu un kontroles ziņojumus. Telegram sesija ļauj anonīmi ziņot, balsot un komentēt."
)

type menuDestination struct {
	replyLabel  string
	inlineLabel string
	lineLabel   string
	url         string
	webApp      bool
}

func NewService(client MessageClient, pollTimeout int, appURL, publicURL, reportsURL string, runtimeState *runtime.State) *Service {
	service := &Service{
		client:       client,
		pollTimeout:  pollTimeout,
		appURL:       strings.TrimSpace(appURL),
		publicURL:    strings.TrimSpace(publicURL),
		reportsURL:   strings.TrimSpace(reportsURL),
		runtimeState: runtimeState,
	}
	service.incidentsURL = resolveIncidentsURL(service.appURL, service.publicURL)
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
	text := normalizeTelegramMessageText(message.Text)
	switch text {
	case "/start", "/menu", "", mapCommand, mainOpenMap, legacyPublicSite, mainReportsFeed:
		return s.sendWelcome(ctx, message.Chat.ID)
	case incidentsCommand, legacyIncidentsCommand, "/-incidents", mainIncidents:
		return s.sendIncidents(ctx, message.Chat.ID)
	default:
		return s.sendWelcome(ctx, message.Chat.ID)
	}
}

func (s *Service) sendWelcome(ctx context.Context, chatID int64) error {
	lines := []string{
		"Kontrole — satiksmes karte un kontroles ziņojumi Rīgā.",
		"Atver Kontroli, lai redzētu pieturas, aktīvo transportu un jaunākos ziņojumus vienuviet.",
	}
	return s.sendMenuMessage(ctx, chatID, lines...)
}

func (s *Service) sendIncidents(ctx context.Context, chatID int64) error {
	incidents, ok := s.menuDestination(mainIncidents)
	if !ok {
		return s.sendMessageLines(ctx, chatID, "Pēdējo 24 stundu incidentu skats nav konfigurēts.")
	}
	lines := []string{
		"Kontroles plūsma rāda pēdējo 24 stundu ziņojumus, anonīmus balsojumus un komentārus.",
		"Komanda: " + incidentsCommand,
		"Atvērt: " + incidents.url,
	}
	return s.sendMessageLines(ctx, chatID, lines...)
}

func (s *Service) configureBot(ctx context.Context) {
	configurator, ok := s.client.(BotConfigurator)
	if !ok {
		return
	}
	commands := []telegram.BotCommand{
		{Command: "start", Description: "Atvērt Kontroli"},
		{Command: strings.TrimPrefix(mapCommand, "/"), Description: "Atvērt satiksmes karti"},
		{Command: strings.TrimPrefix(incidentsCommand, "/"), Description: "Skatīt kontroles plūsmu"},
		{Command: "menu", Description: "Parādīt saites"},
	}
	if err := configurator.SetMyCommands(ctx, commands); err != nil {
		log.Printf("telegram setMyCommands error: %v", err)
	}
	if s.appURL != "" {
		button := telegram.MenuButtonWebApp{
			Type:   "web_app",
			Text:   mainOpenMap,
			WebApp: &telegram.WebAppInfo{URL: s.appURL},
		}
		if err := configurator.SetChatMenuButton(ctx, button); err != nil {
			log.Printf("telegram setChatMenuButton error: %v", err)
		}
	}
	if err := configurator.SetMyName(ctx, botName); err != nil {
		log.Printf("telegram setMyName error: %v", err)
	}
	if err := configurator.SetMyShortDescription(ctx, botShortDescription); err != nil {
		log.Printf("telegram setMyShortDescription error: %v", err)
	}
	if err := configurator.SetMyDescription(ctx, botDescription); err != nil {
		log.Printf("telegram setMyDescription error: %v", err)
	}
}

func (s *Service) welcomeMarkup() any {
	if len(s.inlineMarkup.InlineKeyboard) > 0 && len(s.inlineMarkup.InlineKeyboard[0]) > 0 {
		return s.inlineMarkup
	}
	return s.replyMarkup
}

func (s *Service) newReplyKeyboard() telegram.ReplyKeyboardMarkup {
	firstRow := s.replyRow(mainOpenMap)
	secondRow := s.replyRow(mainIncidents)
	thirdRow := s.replyRow(mainReportsFeed)
	rows := make([][]telegram.KeyboardButton, 0, 3)
	for _, row := range [][]telegram.KeyboardButton{firstRow, secondRow, thirdRow} {
		if len(row) == 0 {
			continue
		}
		rows = append(rows, row)
	}
	return telegram.ReplyKeyboardMarkup{
		Keyboard:       rows,
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
}

func (s *Service) newInlineKeyboard() telegram.InlineKeyboardMarkup {
	firstRow := s.inlineRow(mainOpenMap, mainIncidents)
	secondRow := s.inlineRow(mainReportsFeed)
	rows := make([][]telegram.InlineKeyboardButton, 0, 2)
	for _, row := range [][]telegram.InlineKeyboardButton{firstRow, secondRow} {
		if len(row) == 0 {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return telegram.InlineKeyboardMarkup{}
	}
	return telegram.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (s *Service) sendMenuMessage(ctx context.Context, chatID int64, prefixLines ...string) error {
	lines := append([]string{}, prefixLines...)
	lines = append(lines, s.menuSummaryLines()...)
	return s.sendMessageLines(ctx, chatID, lines...)
}

func (s *Service) sendMessageLines(ctx context.Context, chatID int64, lines ...string) error {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	return s.client.SendMessage(ctx, chatID, strings.Join(filtered, "\n"), telegram.MessageOptions{
		ReplyMarkup: s.welcomeMarkup(),
	})
}

func (s *Service) menuSummaryLines() []string {
	lines := []string{
		"Komanda kartei: " + mapCommand,
		"Komanda plūsmai: " + incidentsCommand,
	}
	for _, action := range []string{mainOpenMap, mainIncidents, mainReportsFeed} {
		destination, ok := s.menuDestination(action)
		if !ok {
			continue
		}
		lines = append(lines, destination.lineLabel+": "+destination.url)
	}
	return lines
}

func (s *Service) replyRow(actions ...string) []telegram.KeyboardButton {
	row := make([]telegram.KeyboardButton, 0, len(actions))
	for _, action := range actions {
		destination, ok := s.menuDestination(action)
		if !ok {
			continue
		}
		button := telegram.KeyboardButton{Text: destination.replyLabel}
		if destination.webApp {
			button.WebApp = &telegram.WebAppInfo{URL: destination.url}
		}
		row = append(row, button)
	}
	return row
}

func (s *Service) inlineRow(actions ...string) []telegram.InlineKeyboardButton {
	row := make([]telegram.InlineKeyboardButton, 0, len(actions))
	for _, action := range actions {
		destination, ok := s.menuDestination(action)
		if !ok {
			continue
		}
		button := telegram.InlineKeyboardButton{Text: destination.inlineLabel}
		if destination.webApp {
			button.WebApp = &telegram.WebAppInfo{URL: destination.url}
		} else {
			button.URL = destination.url
		}
		row = append(row, button)
	}
	return row
}

func (s *Service) menuDestination(action string) (menuDestination, bool) {
	switch action {
	case mainOpenMap:
		if s.appURL == "" {
			return menuDestination{}, false
		}
		return menuDestination{
			replyLabel:  mainOpenMap,
			inlineLabel: mainOpenMap,
			lineLabel:   "Kontrole",
			url:         s.appURL,
			webApp:      true,
		}, true
	case mainIncidents:
		if s.incidentsURL == "" {
			return menuDestination{}, false
		}
		return menuDestination{
			replyLabel:  mainIncidents,
			inlineLabel: mainIncidents,
			lineLabel:   mainIncidents,
			url:         s.incidentsURL,
			webApp:      true,
		}, true
	case mainReportsFeed:
		if s.reportsURL == "" {
			return menuDestination{}, false
		}
		return menuDestination{
			replyLabel:  mainReportsFeed,
			inlineLabel: "Ziņojumi",
			lineLabel:   mainReportsFeed,
			url:         s.reportsURL,
		}, true
	default:
		return menuDestination{}, false
	}
}

func normalizeTelegramMessageText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	command := fields[0]
	if !strings.HasPrefix(command, "/") {
		return text
	}
	if at := strings.Index(command, "@"); at >= 0 {
		command = command[:at]
	}
	return command
}

func resolveIncidentsURL(appURL, publicURL string) string {
	publicURL = strings.TrimSpace(publicURL)
	if publicURL != "" {
		return strings.TrimRight(publicURL, "/") + "/incidents"
	}
	appURL = strings.TrimSpace(appURL)
	if appURL == "" {
		return ""
	}
	parsed, err := url.Parse(appURL)
	if err != nil {
		return ""
	}
	if !strings.HasSuffix(parsed.Path, "/app") {
		return ""
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/app") + "/incidents"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
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
