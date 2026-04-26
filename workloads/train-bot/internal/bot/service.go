package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"telegramtrainapp/internal/domain"
	"telegramtrainapp/internal/i18n"
	"telegramtrainapp/internal/reports"
	"telegramtrainapp/internal/ride"
	"telegramtrainapp/internal/schedule"
	"telegramtrainapp/internal/stationsearch"
	"telegramtrainapp/internal/store"
	appversion "telegramtrainapp/internal/version"
)

type Service struct {
	client                *Client
	notifier              *Notifier
	store                 store.Store
	schedules             schedule.ReadModel
	rides                 *ride.Service
	reports               *reports.Service
	catalog               *i18n.Catalog
	loc                   *time.Location
	pollTimeout           int
	stationCheckinEnabled bool
	webAppURL             string
	versionLine           string

	checkinMu       sync.Mutex
	checkinSessions map[int64]checkinSession
}

const stationPageSize = 20
const trainListPageSize = 8
const checkinSearchPageSize = 8
const checkinSessionTTL = 30 * time.Minute

const (
	checkinFlowStationText = "station_text"
	checkinFlowRouteOrigin = "route_origin_text"
	checkinFlowRouteDest   = "route_dest_text"
)

const (
	mainMenuActionCheckin   = "checkin"
	mainMenuActionMyRide    = "my_ride"
	mainMenuActionReport    = "report"
	mainMenuActionSettings  = "settings"
	mainMenuActionHelp      = "help"
	mainMenuActionIncidents = "incidents"
	mainMenuActionOpenApp   = "open_app"
)

type checkinSession struct {
	Flow            string
	OriginStationID string
	LastQuery       string
	ResultOffset    int
	UpdatedAt       time.Time
}

func NewService(
	client *Client,
	notifier *Notifier,
	st store.Store,
	schedules schedule.ReadModel,
	rides *ride.Service,
	reportsSvc *reports.Service,
	catalog *i18n.Catalog,
	loc *time.Location,
	pollTimeout int,
	stationCheckinEnabled bool,
	webAppURL string,
) *Service {
	return &Service{
		client:                client,
		notifier:              notifier,
		store:                 st,
		schedules:             schedules,
		rides:                 rides,
		reports:               reportsSvc,
		catalog:               catalog,
		loc:                   loc,
		pollTimeout:           pollTimeout,
		stationCheckinEnabled: stationCheckinEnabled,
		webAppURL:             strings.TrimRight(strings.TrimSpace(webAppURL), "/"),
		versionLine:           "Bot " + appversion.Display(),
		checkinSessions:       map[int64]checkinSession{},
	}
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
			log.Printf("get updates error: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if err := s.handleUpdate(ctx, u); err != nil {
				log.Printf("handle update %d: %v", u.UpdateID, err)
			}
		}
		now := time.Now()
		s.rides.CleanupUndo(now)
		s.cleanupCheckinSessions(now)
	}
}

func (s *Service) handleUpdate(ctx context.Context, update Update) error {
	if update.Message != nil {
		return s.handleMessage(ctx, update.Message)
	}
	if update.CallbackQuery != nil {
		return s.handleCallback(ctx, update.CallbackQuery)
	}
	return nil
}

func (s *Service) handleMessage(ctx context.Context, m *Message) error {
	if m.From == nil {
		return nil
	}
	lang := s.languageFor(ctx, m.From.ID)
	text := strings.TrimSpace(m.Text)
	normalizedText := normalizeTelegramMessageText(text)

	switch normalizedText {
	case "/start":
		if err := s.sendStart(ctx, m.Chat.ID, lang); err != nil {
			return err
		}
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case "/incidents":
		return s.send(ctx, m.Chat.ID, s.catalog.T(lang, "incidents_unavailable"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
	case "/menu":
		if err := s.send(ctx, m.Chat.ID, s.catalog.T(lang, "main_prompt"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)}); err != nil {
			return err
		}
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case "/help":
		if err := s.sendHelp(ctx, m.Chat.ID, lang); err != nil {
			return err
		}
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case "":
		return s.send(ctx, m.Chat.ID, s.catalog.T(lang, "main_prompt"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
	case "/info", "ℹ️ Info", "Info":
		return s.sendInfo(ctx, m.Chat.ID, lang)
	case "/health":
		return s.send(ctx, m.Chat.ID, "ok", MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
	}

	if handled, err := s.handleActiveCheckInMessage(ctx, m, lang, text); handled || err != nil {
		return err
	}

	switch s.mainMenuAction(text) {
	case mainMenuActionOpenApp:
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case mainMenuActionCheckin:
		s.clearCheckinSession(m.From.ID)
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case mainMenuActionMyRide:
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case mainMenuActionReport:
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	case mainMenuActionSettings:
		return s.sendSettings(ctx, m.Chat.ID, m.From.ID, lang)
	case mainMenuActionHelp:
		if err := s.sendHelp(ctx, m.Chat.ID, lang); err != nil {
			return err
		}
		return s.sendOpenAppPrompt(ctx, m.Chat.ID, lang)
	default:
		return s.send(ctx, m.Chat.ID, s.catalog.T(lang, "main_prompt"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
	}
}

func (s *Service) mainMenuAction(text string) string {
	if s.catalog == nil {
		return ""
	}
	switch strings.TrimSpace(text) {
	case s.catalog.T(domain.LanguageEN, "btn_open_app"), s.catalog.T(domain.LanguageLV, "btn_open_app"):
		return mainMenuActionOpenApp
	case s.catalog.T(domain.LanguageEN, "btn_main_checkin"), s.catalog.T(domain.LanguageLV, "btn_main_checkin"):
		return mainMenuActionCheckin
	case s.catalog.T(domain.LanguageEN, "btn_main_my_ride"), s.catalog.T(domain.LanguageLV, "btn_main_my_ride"):
		return mainMenuActionMyRide
	case s.catalog.T(domain.LanguageEN, "btn_main_report"), s.catalog.T(domain.LanguageLV, "btn_main_report"):
		return mainMenuActionReport
	case s.catalog.T(domain.LanguageEN, "btn_main_settings"), s.catalog.T(domain.LanguageLV, "btn_main_settings"):
		return mainMenuActionSettings
	case s.catalog.T(domain.LanguageEN, "btn_main_help"), s.catalog.T(domain.LanguageLV, "btn_main_help"):
		return mainMenuActionHelp
	default:
		return ""
	}
}

func (s *Service) configureBot(ctx context.Context) {
	if s == nil || s.client == nil {
		return
	}

	commands := []BotCommand{
		{Command: "start", Description: "Open vivi kontrole"},
		{Command: "menu", Description: "Show vivi kontrole menu"},
		{Command: "help", Description: "How to use the train app"},
	}
	if err := s.client.SetMyCommands(ctx, commands); err != nil {
		log.Printf("telegram setMyCommands error: %v", err)
	}

	appLaunchURL := s.appLaunchURL()
	if appLaunchURL == "" {
		return
	}
	if err := s.client.SetChatMenuButton(ctx, MenuButtonWebApp{
		Type:   "web_app",
		Text:   "Open app",
		WebApp: &WebAppInfo{URL: appLaunchURL},
	}); err != nil {
		log.Printf("telegram setChatMenuButton error: %v", err)
	}
}

func (s *Service) handleCallback(ctx context.Context, cb *CallbackQuery) error {
	if cb.Message == nil {
		return nil
	}
	defer func() {
		_ = s.client.AnswerCallbackQuery(ctx, cb.ID, "", false)
	}()

	lang := s.languageFor(ctx, cb.From.ID)
	parsed, err := ParseCallbackData(cb.Data)
	if err != nil {
		return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "error_generic"), MessageOptions{})
	}

	switch parsed.Scope {
	case "onboarding":
		if parsed.Action == "agree" {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "main_prompt"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
		}
		if parsed.Action == "how" {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "how_it_works"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
		}
	case "checkin":
		return s.handleCheckInCallback(ctx, cb, parsed, lang)
	case "status":
		if parsed.Action == "view" {
			return s.sendStatus(ctx, cb.Message.Chat.ID, cb.From.ID, parsed.Arg1, lang)
		}
	case "report":
		return s.handleReportCallback(ctx, cb, parsed, lang)
	case "ride":
		return s.handleRideCallback(ctx, cb, parsed, lang)
	case "settings":
		return s.handleSettingsCallback(ctx, cb, parsed, lang)
	}
	return nil
}

func (s *Service) handleCheckInCallback(ctx context.Context, cb *CallbackQuery, parsed CallbackData, lang domain.Language) error {
	switch parsed.Action {
	case "start":
		return s.editCheckInEntry(ctx, cb, lang)
	case "menu":
		return s.editCheckInEntry(ctx, cb, lang)
	case "cancel":
		s.clearCheckinSession(cb.From.ID)
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "main_prompt"), MessageOptions{})
	case "prompt_station":
		return s.editStationTextPrompt(ctx, cb, lang)
	case "prompt_route_origin":
		return s.editRouteOriginTextPrompt(ctx, cb, lang)
	case "prompt_route_dest":
		return s.editRouteDestinationTextPrompt(ctx, cb, lang)
	case "retry":
		switch strings.TrimSpace(parsed.Arg1) {
		case checkinFlowStationText:
			return s.editStationTextPrompt(ctx, cb, lang)
		case checkinFlowRouteDest:
			return s.editRouteDestinationTextPrompt(ctx, cb, lang)
		default:
			return s.editRouteOriginTextPrompt(ctx, cb, lang)
		}
	case "results_page":
		offset, _ := strconv.Atoi(parsed.Arg1)
		if offset < 0 {
			offset = 0
		}
		return s.editCheckInSearchResultsPage(ctx, cb, lang, offset)
	case "find":
		switch parsed.Arg1 {
		case "time":
			return s.editCheckInWindowPicker(ctx, cb, lang)
		case "station":
			if !s.stationCheckinEnabled {
				return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "station_checkin_disabled"), MessageOptions{})
			}
			return s.editStationTextPrompt(ctx, cb, lang)
		case "search":
			return s.editRouteOriginTextPrompt(ctx, cb, lang)
		case "favorites":
			return s.editFavoriteRoutes(ctx, cb, lang)
		default:
			return s.editCheckInEntry(ctx, cb, lang)
		}
	case "window":
		return s.editWindowTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Arg1, 0)
	case "window_page":
		page, _ := strconv.Atoi(parsed.Arg2)
		if page < 0 {
			page = 0
		}
		return s.editWindowTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Arg1, page)
	case "station_page":
		return s.editStationTextPrompt(ctx, cb, lang)
	case "station_search":
		return s.editStationTextPrompt(ctx, cb, lang)
	case "station":
		return s.editStationTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Arg1, 0)
	case "station_train_page":
		page := 0
		if len(parsed.Args) >= 2 {
			if p, err := strconv.Atoi(parsed.Args[1]); err == nil && p >= 0 {
				page = p
			}
		}
		return s.editStationTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Arg1, page)
	case "station_train":
		train, err := s.schedules.GetTrain(ctx, parsed.Arg1)
		if err != nil {
			return err
		}
		if train == nil {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "not_found"), MessageOptions{})
		}
		hasStops, err := s.store.TrainHasStops(ctx, train.ID)
		if err != nil {
			return err
		}
		if !hasStops {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "station_flow_unavailable"), MessageOptions{})
		}
		station, err := s.store.GetStationByID(ctx, parsed.Arg2)
		if err != nil {
			return err
		}
		if station == nil {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "not_found"), MessageOptions{})
		}
		now := time.Now().In(s.loc)
		if err := s.rides.CheckInAtStation(ctx, cb.From.ID, train.ID, &station.ID, now, train.ArrivalAt.In(s.loc)); err != nil {
			return err
		}
		s.clearCheckinSession(cb.From.ID)
		text := s.catalog.T(
			lang,
			"checked_in_station",
			station.Name,
			train.FromStation,
			train.ToStation,
			train.DepartureAt.In(s.loc).Format("15:04"),
		)
		kb := InlineKeyboard(
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_open_my_ride"), BuildCallback("ride", "refresh"))},
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_report_inspection"), BuildCallback("ride", "report"))},
		)
		return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
	case "train":
		train, err := s.schedules.GetTrain(ctx, parsed.Arg1)
		if err != nil {
			return err
		}
		if train == nil {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "not_found"), MessageOptions{})
		}
		now := time.Now().In(s.loc)
		if err := s.rides.CheckIn(ctx, cb.From.ID, train.ID, now, train.ArrivalAt.In(s.loc)); err != nil {
			return err
		}
		s.clearCheckinSession(cb.From.ID)
		text := s.catalog.T(lang, "checked_in", train.FromStation, train.ToStation, train.DepartureAt.In(s.loc).Format("15:04"))
		kb := InlineKeyboard(
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_open_my_ride"), BuildCallback("ride", "refresh"))},
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_report_inspection"), BuildCallback("ride", "report"))},
		)
		return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
	case "route_origin_page":
		return s.editRouteOriginTextPrompt(ctx, cb, lang)
	case "route_origin_search":
		return s.editRouteOriginTextPrompt(ctx, cb, lang)
	case "route_origin":
		s.setCheckinOriginSession(cb.From.ID, parsed.Arg1, time.Now())
		return s.editRouteDestinationTextPrompt(ctx, cb, lang)
	case "route_dest_page":
		return s.editRouteDestinationTextPrompt(ctx, cb, lang)
	case "route_dest_search":
		return s.editRouteDestinationTextPrompt(ctx, cb, lang)
	case "route_dest":
		if _, ok := s.getCheckinOriginSession(cb.From.ID, time.Now()); !ok {
			return s.editRouteOriginTextPrompt(ctx, cb, lang)
		}
		return s.editRouteTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Arg1, 0)
	case "route_page":
		page := 0
		if len(parsed.Args) >= 2 {
			if p, err := strconv.Atoi(parsed.Args[1]); err == nil && p >= 0 {
				page = p
			}
		}
		if _, ok := s.getCheckinOriginSession(cb.From.ID, time.Now()); !ok {
			return s.editRouteOriginTextPrompt(ctx, cb, lang)
		}
		return s.editRouteTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Arg1, page)
	case "route_train":
		trainID := parsed.Arg1
		originID, ok := s.getCheckinOriginSession(cb.From.ID, time.Now())
		if !ok {
			return s.editRouteOriginTextPrompt(ctx, cb, lang)
		}
		train, err := s.schedules.GetTrain(ctx, trainID)
		if err != nil {
			return err
		}
		if train == nil {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "not_found"), MessageOptions{})
		}
		now := time.Now().In(s.loc)
		if err := s.rides.CheckInAtStation(ctx, cb.From.ID, train.ID, &originID, now, train.ArrivalAt.In(s.loc)); err != nil {
			return err
		}
		s.clearCheckinSession(cb.From.ID)
		text := s.catalog.T(lang, "checked_in", train.FromStation, train.ToStation, train.DepartureAt.In(s.loc).Format("15:04"))
		kb := InlineKeyboard(
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_open_my_ride"), BuildCallback("ride", "refresh"))},
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_report_inspection"), BuildCallback("ride", "report"))},
		)
		return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
	case "favorite_open":
		if len(parsed.Args) < 2 {
			return s.editFavoriteRoutes(ctx, cb, lang)
		}
		s.setCheckinOriginSession(cb.From.ID, parsed.Arg1, time.Now())
		return s.editRouteTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Args[1], 0)
	case "favorite_toggle":
		if len(parsed.Args) < 2 {
			return s.editFavoriteRoutes(ctx, cb, lang)
		}
		if err := s.toggleFavoriteRoute(ctx, cb.From.ID, parsed.Arg1, parsed.Args[1]); err != nil {
			return err
		}
		s.setCheckinOriginSession(cb.From.ID, parsed.Arg1, time.Now())
		return s.editRouteTrainsPage(ctx, cb, cb.From.ID, lang, parsed.Args[1], 0)
	case "favorites_page":
		return s.editFavoriteRoutes(ctx, cb, lang)
	}
	return nil
}

func (s *Service) sendTrainCardsForWindow(ctx context.Context, chatID int64, userID int64, lang domain.Language, windowID string) error {
	now := time.Now().In(s.loc)
	trains, err := s.schedules.ListByWindow(ctx, now, windowID)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(trains) == 0 {
		return s.send(ctx, chatID, s.catalog.T(lang, "no_trains"), MessageOptions{})
	}
	if len(trains) > 15 {
		trains = trains[:15]
	}
	for _, t := range trains {
		if err := s.sendTrainCard(ctx, chatID, userID, lang, t); err != nil {
			log.Printf("send train card %s failed: %v", t.ID, err)
		}
	}
	return nil
}

func (s *Service) sendTrainCard(ctx context.Context, chatID int64, userID int64, lang domain.Language, train domain.TrainInstance) error {
	now := time.Now().In(s.loc)
	status, err := s.reports.BuildStatus(ctx, train.ID, now)
	if err != nil {
		return err
	}
	riders, err := s.store.CountActiveCheckins(ctx, train.ID, now)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("%s → %s", train.FromStation, train.ToStation)
	times := s.catalog.T(lang, "train_times_line", train.DepartureAt.In(s.loc).Format("15:04"), train.ArrivalAt.In(s.loc).Format("15:04"))
	statusLine := s.statusLine(lang, status, now)
	text := strings.Join([]string{
		title,
		times,
		statusLine,
		s.catalog.T(lang, "ride_riders", riders),
	}, "\n")
	text = s.withScheduleNotice(now, lang, text)
	rows := [][]map[string]any{
		{
			InlineButtonAny(s.catalog.T(lang, "btn_checkin_confirm"), BuildCallback("checkin", "train", train.ID)),
			InlineButtonAny(s.catalog.T(lang, "btn_view_status"), BuildCallback("status", "view", train.ID)),
		},
	}
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendStart(ctx context.Context, chatID int64, lang domain.Language) error {
	rows := [][]map[string]any{
		{InlineButtonAny(s.catalog.T(lang, "onboarding_agree"), BuildCallback("onboarding", "agree"))},
		{InlineButtonAny(s.catalog.T(lang, "onboarding_how"), BuildCallback("onboarding", "how"))},
	}
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	if row := s.openIncidentsButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, s.catalog.T(lang, "start"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendCheckInEntry(ctx context.Context, chatID int64, lang domain.Language) error {
	return s.send(ctx, chatID, s.catalog.T(lang, "checkin_entry_prompt"), MessageOptions{ReplyMarkup: s.checkInEntryKeyboard(lang)})
}

func (s *Service) checkInEntryKeyboard(lang domain.Language) map[string]any {
	rows := make([][]map[string]any, 0, 6)
	if s.stationCheckinEnabled {
		rows = append(rows, []map[string]any{InlineButtonAny(s.catalog.T(lang, "btn_checkin_by_station"), BuildCallback("checkin", "prompt_station"))})
	}
	rows = append(rows,
		[]map[string]any{InlineButtonAny(s.catalog.T(lang, "btn_checkin_by_time_today"), BuildCallback("checkin", "find", "time"))},
		[]map[string]any{InlineButtonAny(s.catalog.T(lang, "btn_checkin_favorites"), BuildCallback("checkin", "find", "favorites"))},
		[]map[string]any{InlineButtonAny(s.catalog.T(lang, "btn_checkin_search_route"), BuildCallback("checkin", "prompt_route_origin"))},
		[]map[string]any{InlineButtonAny(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	return InlineKeyboardAny(rows...)
}

func (s *Service) appLaunchURL() string {
	if strings.TrimSpace(s.webAppURL) == "" {
		return ""
	}
	return s.webAppURL + "/app"
}

func (s *Service) incidentsLaunchURL() string {
	if strings.TrimSpace(s.webAppURL) == "" {
		return ""
	}
	return s.webAppURL + "/incidents"
}

func (s *Service) mainReplyKeyboard(lang domain.Language) map[string]any {
	return MainReplyKeyboardWithWebApp(lang, s.catalog, s.appLaunchURL())
}

func (s *Service) openAppButtonRow(lang domain.Language) []map[string]any {
	url := s.appLaunchURL()
	if url == "" {
		return nil
	}
	return []map[string]any{WebAppInlineButton(s.catalog.T(lang, "btn_open_app"), url)}
}

func (s *Service) openIncidentsButtonRow(lang domain.Language) []map[string]any {
	url := s.incidentsLaunchURL()
	if url == "" {
		return nil
	}
	return []map[string]any{WebAppInlineButton(s.catalog.T(lang, "btn_open_incidents"), url)}
}

func (s *Service) reportsChannelButtonRow(lang domain.Language) []map[string]any {
	if s.catalog == nil {
		return nil
	}
	url := strings.TrimSpace(s.catalog.T(lang, "link_reports_channel"))
	if url == "" {
		return nil
	}
	return []map[string]any{URLInlineButton(s.catalog.T(lang, "btn_open_reports_channel"), url)}
}

func (s *Service) sendOpenAppPrompt(ctx context.Context, chatID int64, lang domain.Language) error {
	rows := make([][]map[string]any, 0, 2)
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	if row := s.reportsChannelButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return s.send(ctx, chatID, s.catalog.T(lang, "main_prompt"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
	}
	return s.send(ctx, chatID, s.catalog.T(lang, "open_app_prompt"), MessageOptions{ReplyMarkup: InlineKeyboardAny(rows...)})
}

func (s *Service) sendIncidents(ctx context.Context, chatID int64, lang domain.Language) error {
	return s.send(ctx, chatID, s.catalog.T(lang, "incidents_unavailable"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
}

func (s *Service) editCheckInEntry(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	s.clearCheckinSession(cb.From.ID)
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "checkin_entry_prompt"), MessageOptions{ReplyMarkup: s.checkInEntryKeyboard(lang)})
}

func (s *Service) editCheckInWindowPicker(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	kb := InlineKeyboard(
		[]map[string]string{InlineButton(s.catalog.T(lang, "window_now"), BuildCallback("checkin", "window", "now"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "window_next_hour"), BuildCallback("checkin", "window", "next_hour"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "window_today"), BuildCallback("checkin", "window", "today"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_back"), BuildCallback("checkin", "start"))},
	)
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "select_window"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) handleActiveCheckInMessage(ctx context.Context, m *Message, lang domain.Language, text string) (bool, error) {
	now := time.Now()
	sess, ok := s.getCheckinSession(m.From.ID, now)
	if !ok || strings.TrimSpace(sess.Flow) == "" {
		return false, nil
	}

	switch sess.Flow {
	case checkinFlowStationText:
		return true, s.handleStationTextInput(ctx, m.Chat.ID, m.From.ID, lang, text)
	case checkinFlowRouteOrigin:
		return true, s.handleRouteOriginTextInput(ctx, m.Chat.ID, m.From.ID, lang, text)
	case checkinFlowRouteDest:
		return true, s.handleRouteDestinationTextInput(ctx, m.Chat.ID, m.From.ID, lang, text)
	default:
		return false, nil
	}
}

func (s *Service) sendStationTextPrompt(ctx context.Context, chatID int64, userID int64, lang domain.Language) error {
	if !s.stationCheckinEnabled {
		s.clearCheckinSession(userID)
		return s.send(ctx, chatID, s.catalog.T(lang, "station_checkin_disabled"), MessageOptions{})
	}
	s.setCheckinTextFlow(userID, checkinFlowStationText, time.Now())
	kb := InlineKeyboard(
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_by_time_today"), BuildCallback("checkin", "find", "time"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	return s.send(ctx, chatID, s.catalog.T(lang, "checkin_station_prompt"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) editStationTextPrompt(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	if !s.stationCheckinEnabled {
		s.clearCheckinSession(cb.From.ID)
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "station_checkin_disabled"), MessageOptions{})
	}
	s.setCheckinTextFlow(cb.From.ID, checkinFlowStationText, time.Now())
	kb := InlineKeyboard(
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_by_time_today"), BuildCallback("checkin", "find", "time"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "checkin_station_prompt"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendRouteOriginTextPrompt(ctx context.Context, chatID int64, userID int64, lang domain.Language) error {
	s.clearCheckinSession(userID)
	s.setCheckinTextFlow(userID, checkinFlowRouteOrigin, time.Now())
	kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))})
	return s.send(ctx, chatID, s.catalog.T(lang, "checkin_route_origin_prompt"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) editRouteOriginTextPrompt(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	s.clearCheckinSession(cb.From.ID)
	s.setCheckinTextFlow(cb.From.ID, checkinFlowRouteOrigin, time.Now())
	kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))})
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "checkin_route_origin_prompt"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendRouteDestinationTextPrompt(ctx context.Context, chatID int64, userID int64, lang domain.Language) error {
	text, ok, err := s.routeDestinationPromptText(ctx, userID, lang)
	if err != nil {
		return err
	}
	if !ok {
		return s.sendRouteOriginTextPrompt(ctx, chatID, userID, lang)
	}
	s.setCheckinTextFlow(userID, checkinFlowRouteDest, time.Now())
	kb := InlineKeyboard(
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "prompt_route_origin"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) editRouteDestinationTextPrompt(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	text, ok, err := s.routeDestinationPromptText(ctx, cb.From.ID, lang)
	if err != nil {
		return err
	}
	if !ok {
		return s.editRouteOriginTextPrompt(ctx, cb, lang)
	}
	s.setCheckinTextFlow(cb.From.ID, checkinFlowRouteDest, time.Now())
	kb := InlineKeyboard(
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "prompt_route_origin"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) routeDestinationPromptText(ctx context.Context, userID int64, lang domain.Language) (string, bool, error) {
	originID, ok := s.getCheckinOriginSession(userID, time.Now())
	if !ok {
		return "", false, nil
	}
	station, err := s.store.GetStationByID(ctx, originID)
	if err != nil {
		return "", false, err
	}
	name := originID
	if station != nil && strings.TrimSpace(station.Name) != "" {
		name = station.Name
	}
	return s.catalog.T(lang, "checkin_route_dest_prompt", name), true, nil
}

func (s *Service) handleStationTextInput(ctx context.Context, chatID int64, userID int64, lang domain.Language, text string) error {
	if !s.stationCheckinEnabled {
		s.clearCheckinSession(userID)
		return s.send(ctx, chatID, s.catalog.T(lang, "station_checkin_disabled"), MessageOptions{})
	}
	query := strings.TrimSpace(text)
	if query == "" {
		return s.sendStationTextPrompt(ctx, chatID, userID, lang)
	}
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	matches, exactMatches := rankStations(stations, query)
	if station, ok := autoSelectStation(matches, exactMatches); ok {
		s.setCheckinSearchSession(userID, checkinFlowStationText, query, 0, time.Now())
		return s.sendStationTrainsPage(ctx, chatID, userID, lang, station.ID, 0)
	}
	return s.sendCheckInSearchMatches(ctx, chatID, userID, lang, checkinFlowStationText, query, matches, 0)
}

func (s *Service) handleRouteOriginTextInput(ctx context.Context, chatID int64, userID int64, lang domain.Language, text string) error {
	query := strings.TrimSpace(text)
	if query == "" {
		return s.sendRouteOriginTextPrompt(ctx, chatID, userID, lang)
	}
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	matches, exactMatches := rankStations(stations, query)
	if station, ok := autoSelectStation(matches, exactMatches); ok {
		s.setCheckinOriginSession(userID, station.ID, time.Now())
		return s.sendRouteDestinationTextPrompt(ctx, chatID, userID, lang)
	}
	return s.sendCheckInSearchMatches(ctx, chatID, userID, lang, checkinFlowRouteOrigin, query, matches, 0)
}

func (s *Service) handleRouteDestinationTextInput(ctx context.Context, chatID int64, userID int64, lang domain.Language, text string) error {
	query := strings.TrimSpace(text)
	if query == "" {
		return s.sendRouteDestinationTextPrompt(ctx, chatID, userID, lang)
	}
	now := time.Now().In(s.loc)
	originID, ok := s.getCheckinOriginSession(userID, now)
	if !ok {
		return s.sendRouteOriginTextPrompt(ctx, chatID, userID, lang)
	}
	destinations, err := s.schedules.ListReachableDestinations(ctx, now, originID)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	matches, exactMatches := rankStations(destinations, query)
	if station, ok := autoSelectStation(matches, exactMatches); ok {
		s.setCheckinSearchSession(userID, checkinFlowRouteDest, query, 0, time.Now())
		return s.sendRouteTrainsPage(ctx, chatID, userID, lang, station.ID, 0)
	}
	return s.sendCheckInSearchMatches(ctx, chatID, userID, lang, checkinFlowRouteDest, query, matches, 0)
}

func (s *Service) sendCheckInSearchMatches(ctx context.Context, chatID int64, userID int64, lang domain.Language, flow string, query string, matches []domain.Station, offset int) error {
	text, kb, normalizedOffset := s.buildCheckInSearchMatchesView(lang, flow, query, matches, offset)
	s.setCheckinSearchSession(userID, flow, query, normalizedOffset, time.Now())
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) editCheckInSearchResultsPage(ctx context.Context, cb *CallbackQuery, lang domain.Language, offset int) error {
	sess, ok := s.getCheckinSession(cb.From.ID, time.Now())
	if !ok || strings.TrimSpace(sess.Flow) == "" || strings.TrimSpace(sess.LastQuery) == "" {
		return s.editCheckInEntry(ctx, cb, lang)
	}

	now := time.Now().In(s.loc)
	var (
		stations []domain.Station
		err      error
	)
	switch sess.Flow {
	case checkinFlowStationText:
		stations, err = s.schedules.ListStations(ctx, now)
	case checkinFlowRouteOrigin:
		stations, err = s.schedules.ListStations(ctx, now)
	case checkinFlowRouteDest:
		originID, hasOrigin := s.getCheckinOriginSession(cb.From.ID, now)
		if !hasOrigin {
			return s.editRouteOriginTextPrompt(ctx, cb, lang)
		}
		stations, err = s.schedules.ListReachableDestinations(ctx, now, originID)
	default:
		return s.editCheckInEntry(ctx, cb, lang)
	}
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}

	matches, _ := rankStations(stations, sess.LastQuery)
	text, kb, normalizedOffset := s.buildCheckInSearchMatchesView(lang, sess.Flow, sess.LastQuery, matches, offset)
	s.setCheckinSearchSession(cb.From.ID, sess.Flow, sess.LastQuery, normalizedOffset, time.Now())
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) buildCheckInSearchMatchesView(lang domain.Language, flow string, query string, matches []domain.Station, offset int) (string, map[string]any, int) {
	if len(matches) == 0 {
		return s.buildCheckInNoMatchesView(lang, flow, query)
	}

	page := 0
	if offset > 0 {
		page = offset / checkinSearchPageSize
	}
	start, end, normalizedPage, totalPages := paginate(len(matches), page, checkinSearchPageSize)
	normalizedOffset := start
	pageItems := matches[start:end]

	rows := make([][]map[string]string, 0, len(pageItems)+4)
	action := "station"
	title := s.catalog.T(lang, "checkin_station_matches", query)
	switch flow {
	case checkinFlowRouteOrigin:
		action = "route_origin"
		title = s.catalog.T(lang, "checkin_route_origin_matches", query)
	case checkinFlowRouteDest:
		action = "route_dest"
		title = s.catalog.T(lang, "checkin_route_dest_matches", query)
	}
	for _, st := range pageItems {
		rows = append(rows, []map[string]string{InlineButton(st.Name, BuildCallback("checkin", action, st.ID))})
	}

	nav := make([]map[string]string, 0, 2)
	if normalizedOffset > 0 {
		prevOffset := normalizedOffset - checkinSearchPageSize
		if prevOffset < 0 {
			prevOffset = 0
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), BuildCallback("checkin", "results_page", strconv.Itoa(prevOffset))))
	}
	if end < len(matches) {
		nav = append(nav, InlineButton(s.catalog.T(lang, "btn_checkin_more_results"), BuildCallback("checkin", "results_page", strconv.Itoa(end))))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows,
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "retry", flow))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	if totalPages > 1 {
		title += "\n" + s.catalog.T(lang, "station_page_hint", normalizedPage+1, totalPages)
	}
	return title, InlineKeyboard(rows...), normalizedOffset
}

func (s *Service) buildCheckInNoMatchesView(lang domain.Language, flow string, query string) (string, map[string]any, int) {
	rows := make([][]map[string]string, 0, 3)
	text := s.catalog.T(lang, "checkin_station_no_match", query)
	switch flow {
	case checkinFlowRouteOrigin:
		text = s.catalog.T(lang, "checkin_route_origin_no_match", query)
	case checkinFlowRouteDest:
		text = s.catalog.T(lang, "checkin_route_dest_no_match", query)
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_retry_search"), BuildCallback("checkin", "retry", flow))})
	if flow == checkinFlowStationText {
		rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_by_time_today"), BuildCallback("checkin", "find", "time"))})
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))})
	return text, InlineKeyboard(rows...), 0
}

type stationRankedMatch struct {
	station domain.Station
	rank    int
	exact   bool
}

func rankStations(stations []domain.Station, query string) ([]domain.Station, []domain.Station) {
	normalizedQuery := normalizeStationText(query)
	if normalizedQuery == "" {
		out := make([]domain.Station, 0, len(stations))
		seen := make(map[string]struct{}, len(stations))
		for _, st := range stations {
			if _, ok := seen[st.ID]; ok {
				continue
			}
			seen[st.ID] = struct{}{}
			out = append(out, st)
		}
		sort.Slice(out, func(i, j int) bool {
			leftName := normalizeStationText(out[i].Name)
			rightName := normalizeStationText(out[j].Name)
			if leftName == rightName {
				return out[i].ID < out[j].ID
			}
			return leftName < rightName
		})
		return out, nil
	}

	candidates := make([]stationRankedMatch, 0, len(stations))
	seen := make(map[string]struct{}, len(stations))
	exactMatches := make([]domain.Station, 0, 1)
	for _, st := range stations {
		if _, ok := seen[st.ID]; ok {
			continue
		}
		rank, exact, ok := stationMatchRank(st, normalizedQuery)
		if !ok {
			continue
		}
		seen[st.ID] = struct{}{}
		candidates = append(candidates, stationRankedMatch{
			station: st,
			rank:    rank,
			exact:   exact,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		leftName := normalizeStationText(candidates[i].station.Name)
		rightName := normalizeStationText(candidates[j].station.Name)
		if leftName == rightName {
			return candidates[i].station.ID < candidates[j].station.ID
		}
		return leftName < rightName
	})

	out := make([]domain.Station, 0, len(candidates))
	for _, item := range candidates {
		out = append(out, item.station)
		if item.exact {
			exactMatches = append(exactMatches, item.station)
		}
	}
	return out, exactMatches
}

func autoSelectStation(matches []domain.Station, exactMatches []domain.Station) (domain.Station, bool) {
	if len(exactMatches) == 1 {
		return exactMatches[0], true
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return domain.Station{}, false
}

func stationMatchRank(st domain.Station, normalizedQuery string) (int, bool, bool) {
	normalizedKey := normalizeStationText(st.NormalizedKey)
	if normalizedKey == "" {
		normalizedKey = normalizeStationText(st.Name)
	}
	normalizedName := normalizeStationText(st.Name)

	switch {
	case normalizedKey == normalizedQuery:
		return 0, true, true
	case normalizedName == normalizedQuery:
		return 0, true, true
	case strings.HasPrefix(normalizedKey, normalizedQuery):
		return 1, false, true
	case strings.HasPrefix(normalizedName, normalizedQuery):
		return 2, false, true
	case strings.Contains(normalizedKey, normalizedQuery):
		return 3, false, true
	case strings.Contains(normalizedName, normalizedQuery):
		return 4, false, true
	default:
		return 0, false, false
	}
}

func normalizeStationText(value string) string {
	return stationsearch.Normalize(value)
}

func paginate(total int, page int, pageSize int) (start int, end int, normalizedPage int, totalPages int) {
	if pageSize <= 0 {
		pageSize = trainListPageSize
	}
	if total < 0 {
		total = 0
	}
	if total == 0 {
		return 0, 0, 0, 1
	}
	totalPages = (total + pageSize - 1) / pageSize
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start = page * pageSize
	end = start + pageSize
	if end > total {
		end = total
	}
	return start, end, page, totalPages
}

func (s *Service) editWindowTrainsPage(ctx context.Context, cb *CallbackQuery, userID int64, lang domain.Language, windowID string, page int) error {
	text, kb, err := s.buildWindowTrainsPageView(ctx, lang, windowID, page)
	if err != nil {
		return err
	}
	_ = userID
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) buildWindowTrainsPageView(ctx context.Context, lang domain.Language, windowID string, page int) (string, map[string]any, error) {
	now := time.Now().In(s.loc)
	trains, err := s.schedules.ListByWindow(ctx, now, windowID)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.catalog.T(lang, "schedule_unavailable"), nil, nil
		}
		return "", nil, err
	}
	if len(trains) == 0 {
		kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_back"), BuildCallback("checkin", "find", "time"))})
		return s.catalog.T(lang, "no_trains"), kb, nil
	}
	start, end, page, totalPages := paginate(len(trains), page, trainListPageSize)
	pageItems := trains[start:end]

	rows := make([][]map[string]string, 0, len(pageItems)+3)
	for _, t := range pageItems {
		label := fmt.Sprintf("%s %s → %s", t.DepartureAt.In(s.loc).Format("15:04"), t.FromStation, t.ToStation)
		rows = append(rows, []map[string]string{InlineButton(label, BuildCallback("checkin", "train", t.ID))})
	}
	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), BuildCallback("checkin", "window_page", windowID, strconv.Itoa(page-1))))
	}
	if page < totalPages-1 {
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), BuildCallback("checkin", "window_page", windowID, strconv.Itoa(page+1))))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_windows"), BuildCallback("checkin", "find", "time"))})
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_main"), BuildCallback("checkin", "menu"))})
	text := s.catalog.T(lang, "checkin_window_departures", s.windowLabel(lang, windowID))
	if totalPages > 1 {
		text += "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	}
	text = s.withScheduleNotice(now, lang, text)
	return text, InlineKeyboard(rows...), nil
}

func (s *Service) sendStationPicker(ctx context.Context, chatID int64, lang domain.Language, page int, prefix string) error {
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(stations) == 0 {
		return s.send(ctx, chatID, s.catalog.T(lang, "no_stations"), MessageOptions{})
	}
	filtered := filterStationsByPrefix(stations, prefix)
	if len(filtered) == 0 {
		return s.send(ctx, chatID, s.catalog.T(lang, "no_station_match"), MessageOptions{})
	}
	if page < 0 {
		page = 0
	}
	totalPages := (len(filtered) + stationPageSize - 1) / stationPageSize
	if totalPages <= 0 {
		totalPages = 1
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * stationPageSize
	end := start + stationPageSize
	if end > len(filtered) {
		end = len(filtered)
	}

	pageItems := filtered[start:end]
	rows := make([][]map[string]string, 0, len(pageItems)+3)
	for _, st := range pageItems {
		rows = append(rows, []map[string]string{
			InlineButton(st.Name, BuildCallback("checkin", "station", st.ID)),
		})
	}

	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		callback := BuildCallback("checkin", "station_page", strconv.Itoa(page-1))
		if prefix != "" {
			callback = BuildCallback("checkin", "station_page", strconv.Itoa(page-1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), callback))
	}
	if page < totalPages-1 {
		callback := BuildCallback("checkin", "station_page", strconv.Itoa(page+1))
		if prefix != "" {
			callback = BuildCallback("checkin", "station_page", strconv.Itoa(page+1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), callback))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_search_button"), BuildCallback("checkin", "station_search"))})
	if prefix != "" {
		rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "station_search", "all"))})
	}

	header := s.catalog.T(lang, "find_station")
	if prefix != "" {
		header = s.catalog.T(lang, "find_station_filtered", strings.ToUpper(prefix))
	}
	text := header + "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) sendStationSearch(ctx context.Context, chatID int64, lang domain.Language) error {
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(stations) == 0 {
		return s.send(ctx, chatID, s.catalog.T(lang, "no_stations"), MessageOptions{})
	}

	prefixes := stationPrefixKeys(stations)
	rows := make([][]map[string]string, 0, len(prefixes)/4+3)
	for i := 0; i < len(prefixes); i += 4 {
		end := i + 4
		if end > len(prefixes) {
			end = len(prefixes)
		}
		row := make([]map[string]string, 0, end-i)
		for _, p := range prefixes[i:end] {
			row = append(row, InlineButton(strings.ToUpper(p), BuildCallback("checkin", "station_search", p)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, []map[string]string{
		InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "station_search", "all")),
		InlineButton(s.catalog.T(lang, "station_back_list"), BuildCallback("checkin", "station_page", "0")),
	})
	return s.send(ctx, chatID, s.catalog.T(lang, "station_search_prompt"), MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func filterStationsByPrefix(stations []domain.Station, prefix string) []domain.Station {
	prefix = normalizeStationText(prefix)
	if prefix == "" {
		out := make([]domain.Station, len(stations))
		copy(out, stations)
		return out
	}
	out := make([]domain.Station, 0, len(stations))
	for _, st := range stations {
		key := normalizeStationText(st.NormalizedKey)
		if key == "" {
			key = normalizeStationText(st.Name)
		}
		if strings.HasPrefix(key, prefix) || strings.HasPrefix(normalizeStationText(st.Name), prefix) {
			out = append(out, st)
		}
	}
	return out
}

func stationPrefixKeys(stations []domain.Station) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(stations))
	for _, st := range stations {
		key := normalizeStationText(st.NormalizedKey)
		if key == "" {
			key = normalizeStationText(st.Name)
		}
		if key == "" {
			continue
		}
		prefix := string([]rune(key)[0])
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		out = append(out, prefix)
	}
	sort.Strings(out)
	return out
}

func (s *Service) editStationPicker(ctx context.Context, cb *CallbackQuery, lang domain.Language, page int, prefix string) error {
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(stations) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_stations"), MessageOptions{})
	}

	filtered := filterStationsByPrefix(stations, prefix)
	if len(filtered) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_station_match"), MessageOptions{})
	}
	start, end, page, totalPages := paginate(len(filtered), page, stationPageSize)
	pageItems := filtered[start:end]
	rows := make([][]map[string]string, 0, len(pageItems)+4)
	for _, st := range pageItems {
		rows = append(rows, []map[string]string{
			InlineButton(st.Name, BuildCallback("checkin", "station", st.ID)),
		})
	}
	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		callback := BuildCallback("checkin", "station_page", strconv.Itoa(page-1))
		if prefix != "" {
			callback = BuildCallback("checkin", "station_page", strconv.Itoa(page-1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), callback))
	}
	if page < totalPages-1 {
		callback := BuildCallback("checkin", "station_page", strconv.Itoa(page+1))
		if prefix != "" {
			callback = BuildCallback("checkin", "station_page", strconv.Itoa(page+1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), callback))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_search_button"), BuildCallback("checkin", "station_search"))})
	if prefix != "" {
		rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "station_search", "all"))})
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_main"), BuildCallback("checkin", "menu"))})

	header := s.catalog.T(lang, "find_station")
	if prefix != "" {
		header = s.catalog.T(lang, "find_station_filtered", strings.ToUpper(prefix))
	}
	text := header + "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) editStationSearch(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(stations) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_stations"), MessageOptions{})
	}
	prefixes := stationPrefixKeys(stations)
	rows := make([][]map[string]string, 0, len(prefixes)/4+2)
	for i := 0; i < len(prefixes); i += 4 {
		end := i + 4
		if end > len(prefixes) {
			end = len(prefixes)
		}
		row := make([]map[string]string, 0, end-i)
		for _, p := range prefixes[i:end] {
			row = append(row, InlineButton(strings.ToUpper(p), BuildCallback("checkin", "station_search", p)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, []map[string]string{
		InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "station_search", "all")),
		InlineButton(s.catalog.T(lang, "station_back_list"), BuildCallback("checkin", "station_page", "0")),
	})
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "station_search_prompt"), MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) editStationTrainsPage(ctx context.Context, cb *CallbackQuery, userID int64, lang domain.Language, stationID string, page int) error {
	text, kb, err := s.buildStationTrainsPageView(ctx, lang, stationID, page)
	if err != nil {
		return err
	}
	_ = userID
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendStationTrainsPage(ctx context.Context, chatID int64, userID int64, lang domain.Language, stationID string, page int) error {
	text, kb, err := s.buildStationTrainsPageView(ctx, lang, stationID, page)
	if err != nil {
		return err
	}
	_ = userID
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) buildStationTrainsPageView(ctx context.Context, lang domain.Language, stationID string, page int) (string, map[string]any, error) {
	now := time.Now().In(s.loc)
	trains, err := s.schedules.ListByStationWindow(ctx, now, stationID, 18*time.Hour)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.catalog.T(lang, "schedule_unavailable"), nil, nil
		}
		return "", nil, err
	}
	if len(trains) == 0 {
		kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "prompt_station"))})
		return s.catalog.T(lang, "no_station_trains"), kb, nil
	}
	start, end, page, totalPages := paginate(len(trains), page, trainListPageSize)
	pageItems := trains[start:end]
	rows := make([][]map[string]string, 0, len(pageItems)+4)
	for _, t := range pageItems {
		label := fmt.Sprintf("%s %s → %s", t.PassAt.In(s.loc).Format("15:04"), t.Train.FromStation, t.Train.ToStation)
		rows = append(rows, []map[string]string{
			InlineButton(label, BuildCallback("checkin", "station_train", t.Train.ID, stationID)),
		})
	}
	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), BuildCallback("checkin", "station_train_page", stationID, strconv.Itoa(page-1))))
	}
	if page < totalPages-1 {
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), BuildCallback("checkin", "station_train_page", stationID, strconv.Itoa(page+1))))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows,
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "prompt_station"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	stationName := pageItems[0].StationName
	text := s.catalog.T(lang, "checkin_station_departures", stationName)
	if totalPages > 1 {
		text += "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	}
	text = s.withScheduleNotice(now, lang, text)
	return text, InlineKeyboard(rows...), nil
}

func (s *Service) editRouteOriginPicker(ctx context.Context, cb *CallbackQuery, lang domain.Language, page int, prefix string) error {
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(stations) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_stations"), MessageOptions{})
	}
	filtered := filterStationsByPrefix(stations, prefix)
	if len(filtered) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_station_match"), MessageOptions{})
	}
	start, end, page, totalPages := paginate(len(filtered), page, stationPageSize)
	pageItems := filtered[start:end]
	rows := make([][]map[string]string, 0, len(pageItems)+4)
	for _, st := range pageItems {
		rows = append(rows, []map[string]string{
			InlineButton(st.Name, BuildCallback("checkin", "route_origin", st.ID)),
		})
	}
	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		callback := BuildCallback("checkin", "route_origin_page", strconv.Itoa(page-1))
		if prefix != "" {
			callback = BuildCallback("checkin", "route_origin_page", strconv.Itoa(page-1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), callback))
	}
	if page < totalPages-1 {
		callback := BuildCallback("checkin", "route_origin_page", strconv.Itoa(page+1))
		if prefix != "" {
			callback = BuildCallback("checkin", "route_origin_page", strconv.Itoa(page+1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), callback))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_search_button"), BuildCallback("checkin", "route_origin_search"))})
	if prefix != "" {
		rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "route_origin_search", "all"))})
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_main"), BuildCallback("checkin", "menu"))})
	header := s.catalog.T(lang, "route_origin_picker_title")
	if prefix != "" {
		header = s.catalog.T(lang, "route_origin_picker_filtered", strings.ToUpper(prefix))
	}
	text := header + "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) editRouteOriginSearch(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	now := time.Now().In(s.loc)
	stations, err := s.schedules.ListStations(ctx, now)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(stations) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_stations"), MessageOptions{})
	}
	prefixes := stationPrefixKeys(stations)
	rows := make([][]map[string]string, 0, len(prefixes)/4+2)
	for i := 0; i < len(prefixes); i += 4 {
		end := i + 4
		if end > len(prefixes) {
			end = len(prefixes)
		}
		row := make([]map[string]string, 0, end-i)
		for _, p := range prefixes[i:end] {
			row = append(row, InlineButton(strings.ToUpper(p), BuildCallback("checkin", "route_origin_search", p)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, []map[string]string{
		InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "route_origin_search", "all")),
		InlineButton(s.catalog.T(lang, "btn_back"), BuildCallback("checkin", "route_origin_page", "0")),
	})
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "route_origin_search_prompt"), MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) editRouteDestinationPicker(ctx context.Context, cb *CallbackQuery, lang domain.Language, page int, prefix string) error {
	now := time.Now()
	originID, ok := s.getCheckinOriginSession(cb.From.ID, now)
	if !ok {
		return s.editRouteOriginPicker(ctx, cb, lang, 0, "")
	}
	destinations, err := s.schedules.ListReachableDestinations(ctx, now.In(s.loc), originID)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(destinations) == 0 {
		kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_origin"), BuildCallback("checkin", "route_origin_page", "0"))})
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "route_dest_none"), MessageOptions{ReplyMarkup: kb})
	}
	filtered := filterStationsByPrefix(destinations, prefix)
	if len(filtered) == 0 {
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "no_station_match"), MessageOptions{})
	}
	start, end, page, totalPages := paginate(len(filtered), page, stationPageSize)
	pageItems := filtered[start:end]
	rows := make([][]map[string]string, 0, len(pageItems)+4)
	for _, st := range pageItems {
		rows = append(rows, []map[string]string{
			InlineButton(st.Name, BuildCallback("checkin", "route_dest", st.ID)),
		})
	}
	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		callback := BuildCallback("checkin", "route_dest_page", strconv.Itoa(page-1))
		if prefix != "" {
			callback = BuildCallback("checkin", "route_dest_page", strconv.Itoa(page-1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), callback))
	}
	if page < totalPages-1 {
		callback := BuildCallback("checkin", "route_dest_page", strconv.Itoa(page+1))
		if prefix != "" {
			callback = BuildCallback("checkin", "route_dest_page", strconv.Itoa(page+1), prefix)
		}
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), callback))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_search_button"), BuildCallback("checkin", "route_dest_search"))})
	if prefix != "" {
		rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "route_dest_search", "all"))})
	}
	rows = append(rows, []map[string]string{InlineButton(s.catalog.T(lang, "btn_origin"), BuildCallback("checkin", "route_origin_page", "0"))})
	header := s.catalog.T(lang, "route_dest_picker_title")
	if prefix != "" {
		header = s.catalog.T(lang, "route_dest_picker_filtered", strings.ToUpper(prefix))
	}
	text := header + "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) editRouteDestinationSearch(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	now := time.Now()
	originID, ok := s.getCheckinOriginSession(cb.From.ID, now)
	if !ok {
		return s.editRouteOriginPicker(ctx, cb, lang, 0, "")
	}
	destinations, err := s.schedules.ListReachableDestinations(ctx, now.In(s.loc), originID)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	prefixes := stationPrefixKeys(destinations)
	rows := make([][]map[string]string, 0, len(prefixes)/4+2)
	for i := 0; i < len(prefixes); i += 4 {
		end := i + 4
		if end > len(prefixes) {
			end = len(prefixes)
		}
		row := make([]map[string]string, 0, end-i)
		for _, p := range prefixes[i:end] {
			row = append(row, InlineButton(strings.ToUpper(p), BuildCallback("checkin", "route_dest_search", p)))
		}
		rows = append(rows, row)
	}
	rows = append(rows, []map[string]string{
		InlineButton(s.catalog.T(lang, "station_show_all"), BuildCallback("checkin", "route_dest_search", "all")),
		InlineButton(s.catalog.T(lang, "btn_back"), BuildCallback("checkin", "route_dest_page", "0")),
	})
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "route_dest_search_prompt"), MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) editRouteTrainsPage(ctx context.Context, cb *CallbackQuery, userID int64, lang domain.Language, destinationID string, page int) error {
	if _, ok := s.getCheckinOriginSession(userID, time.Now()); !ok {
		return s.editRouteOriginTextPrompt(ctx, cb, lang)
	}
	text, kb, err := s.buildRouteTrainsPageView(ctx, userID, lang, destinationID, page)
	if err != nil {
		return err
	}
	return s.editOrSendCallback(ctx, cb, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendRouteTrainsPage(ctx context.Context, chatID int64, userID int64, lang domain.Language, destinationID string, page int) error {
	if _, ok := s.getCheckinOriginSession(userID, time.Now()); !ok {
		return s.sendRouteOriginTextPrompt(ctx, chatID, userID, lang)
	}
	text, kb, err := s.buildRouteTrainsPageView(ctx, userID, lang, destinationID, page)
	if err != nil {
		return err
	}
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) buildRouteTrainsPageView(ctx context.Context, userID int64, lang domain.Language, destinationID string, page int) (string, map[string]any, error) {
	now := time.Now().In(s.loc)
	originID, ok := s.getCheckinOriginSession(userID, now)
	if !ok {
		return s.catalog.T(lang, "checkin_route_origin_prompt"), InlineKeyboard(
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
		), nil
	}
	trains, err := s.schedules.ListRouteWindowTrains(ctx, now, originID, destinationID, 18*time.Hour)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.catalog.T(lang, "schedule_unavailable"), nil, nil
		}
		return "", nil, err
	}
	if len(trains) == 0 {
		kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "prompt_route_dest"))})
		return s.catalog.T(lang, "no_trains"), kb, nil
	}
	start, end, page, totalPages := paginate(len(trains), page, trainListPageSize)
	pageItems := trains[start:end]
	rows := make([][]map[string]string, 0, len(pageItems)+5)
	for _, t := range pageItems {
		label := fmt.Sprintf("%s %s → %s", t.FromPassAt.In(s.loc).Format("15:04"), t.FromStationName, t.ToStationName)
		rows = append(rows, []map[string]string{InlineButton(label, BuildCallback("checkin", "route_train", t.Train.ID))})
	}
	nav := make([]map[string]string, 0, 2)
	if page > 0 {
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_prev_page"), BuildCallback("checkin", "route_page", destinationID, strconv.Itoa(page-1))))
	}
	if page < totalPages-1 {
		nav = append(nav, InlineButton(s.catalog.T(lang, "station_next_page"), BuildCallback("checkin", "route_page", destinationID, strconv.Itoa(page+1))))
	}
	if len(nav) > 0 {
		rows = append(rows, nav)
	}
	isFav, err := s.isFavoriteRoute(ctx, userID, originID, destinationID)
	if err != nil {
		return "", nil, err
	}
	favLabel := s.catalog.T(lang, "btn_save_route")
	if isFav {
		favLabel = s.catalog.T(lang, "btn_remove_favorite")
	}
	rows = append(rows,
		[]map[string]string{InlineButton(favLabel, BuildCallback("checkin", "favorite_toggle", originID, destinationID))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_refine_search"), BuildCallback("checkin", "prompt_route_dest"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	text := s.catalog.T(lang, "checkin_route_departures", pageItems[0].FromStationName, pageItems[0].ToStationName)
	if totalPages > 1 {
		text += "\n" + s.catalog.T(lang, "station_page_hint", page+1, totalPages)
	}
	text = s.withScheduleNotice(now, lang, text)
	return text, InlineKeyboard(rows...), nil
}

func (s *Service) isFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) (bool, error) {
	items, err := s.store.ListFavoriteRoutes(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.FromStationID == fromStationID && item.ToStationID == toStationID {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) toggleFavoriteRoute(ctx context.Context, userID int64, fromStationID string, toStationID string) error {
	isFav, err := s.isFavoriteRoute(ctx, userID, fromStationID, toStationID)
	if err != nil {
		return err
	}
	if isFav {
		return s.store.DeleteFavoriteRoute(ctx, userID, fromStationID, toStationID)
	}
	return s.store.UpsertFavoriteRoute(ctx, userID, fromStationID, toStationID)
}

func (s *Service) editFavoriteRoutes(ctx context.Context, cb *CallbackQuery, lang domain.Language) error {
	items, err := s.store.ListFavoriteRoutes(ctx, cb.From.ID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		kb := InlineKeyboard(
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_search_route"), BuildCallback("checkin", "prompt_route_origin"))},
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
		)
		return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "favorite_routes_empty"), MessageOptions{ReplyMarkup: kb})
	}
	rows := make([][]map[string]string, 0, len(items)+2)
	for _, item := range items {
		fromName := item.FromStationName
		if fromName == "" {
			fromName = item.FromStationID
		}
		toName := item.ToStationName
		if toName == "" {
			toName = item.ToStationID
		}
		rows = append(rows, []map[string]string{
			InlineButton(fmt.Sprintf("%s → %s", fromName, toName), BuildCallback("checkin", "favorite_open", item.FromStationID, item.ToStationID)),
		})
	}
	rows = append(rows,
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_search_route"), BuildCallback("checkin", "prompt_route_origin"))},
		[]map[string]string{InlineButton(s.catalog.T(lang, "btn_checkin_cancel"), BuildCallback("checkin", "cancel"))},
	)
	return s.editOrSendCallback(ctx, cb, s.catalog.T(lang, "favorite_routes_title"), MessageOptions{ReplyMarkup: InlineKeyboard(rows...)})
}

func (s *Service) sendStationTrainCards(ctx context.Context, chatID int64, userID int64, lang domain.Language, stationID string) error {
	now := time.Now().In(s.loc)
	trains, err := s.schedules.ListByStationWindow(ctx, now, stationID, 18*time.Hour)
	if err != nil {
		if errors.Is(err, schedule.ErrUnavailable) {
			return s.send(ctx, chatID, s.catalog.T(lang, "schedule_unavailable"), MessageOptions{})
		}
		return err
	}
	if len(trains) == 0 {
		return s.send(ctx, chatID, s.catalog.T(lang, "no_station_trains"), MessageOptions{})
	}
	if len(trains) > 15 {
		trains = trains[:15]
	}
	for _, t := range trains {
		if err := s.sendStationTrainCard(ctx, chatID, userID, lang, t, stationID); err != nil {
			log.Printf("send station train card %s failed: %v", t.Train.ID, err)
		}
	}
	return nil
}

func (s *Service) sendStationTrainCard(ctx context.Context, chatID int64, userID int64, lang domain.Language, stationTrain domain.StationWindowTrain, stationID string) error {
	train := stationTrain.Train
	now := time.Now().In(s.loc)
	status, err := s.reports.BuildStatus(ctx, train.ID, now)
	if err != nil {
		return err
	}
	riders, err := s.store.CountActiveCheckins(ctx, train.ID, now)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("%s → %s", train.FromStation, train.ToStation)
	times := s.catalog.T(lang, "train_times_line", train.DepartureAt.In(s.loc).Format("15:04"), train.ArrivalAt.In(s.loc).Format("15:04"))
	passLine := s.catalog.T(lang, "station_pass_line", stationTrain.StationName, stationTrain.PassAt.In(s.loc).Format("15:04"))
	statusLine := s.statusLine(lang, status, now)
	text := strings.Join([]string{
		title,
		times,
		passLine,
		statusLine,
		s.catalog.T(lang, "ride_riders", riders),
	}, "\n")
	text = s.withScheduleNotice(now, lang, text)
	rows := [][]map[string]any{
		{
			InlineButtonAny(s.catalog.T(lang, "btn_checkin_confirm"), BuildCallback("checkin", "station_train", train.ID, stationID)),
			InlineButtonAny(s.catalog.T(lang, "btn_view_status"), BuildCallback("status", "view", train.ID)),
		},
	}
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendMyRide(ctx context.Context, chatID int64, userID int64, lang domain.Language) error {
	now := time.Now().In(s.loc)
	active, err := s.rides.ActiveCheckIn(ctx, userID, now)
	if err != nil {
		return err
	}
	if active == nil {
		rows := [][]map[string]any{
			{InlineButtonAny(s.catalog.T(lang, "btn_start_checkin"), BuildCallback("checkin", "start"))},
		}
		if row := s.openAppButtonRow(lang); row != nil {
			rows = append(rows, row)
		}
		kb := InlineKeyboardAny(rows...)
		return s.send(ctx, chatID, s.catalog.T(lang, "my_ride_none"), MessageOptions{ReplyMarkup: kb})
	}
	train, err := s.schedules.GetTrain(ctx, active.TrainInstanceID)
	if err != nil {
		return err
	}
	if train == nil {
		return s.send(ctx, chatID, s.catalog.T(lang, "not_found"), MessageOptions{})
	}
	status, err := s.reports.BuildStatus(ctx, train.ID, now)
	if err != nil {
		return err
	}
	riders, err := s.store.CountActiveCheckins(ctx, train.ID, now)
	if err != nil {
		return err
	}

	lines := []string{
		s.catalog.T(lang, "my_ride_title", train.FromStation, train.ToStation, train.DepartureAt.In(s.loc).Format("15:04")),
		s.catalog.T(lang, "ride_riders", riders),
	}
	if status.State == domain.StatusNoReports || status.LastReportAt == nil {
		lines = append(lines, s.catalog.T(lang, "ride_no_reports"))
	} else {
		lines = append(lines, s.catalog.T(lang, "ride_last", s.relativeAgo(lang, now, status.LastReportAt.In(s.loc))))
	}
	lines = append(lines, s.catalog.T(lang, "auto_checkout", active.AutoCheckoutAt.In(s.loc).Format("15:04")))

	rows := [][]map[string]any{
		{InlineButtonAny(s.catalog.T(lang, "btn_report_inspection"), BuildCallback("ride", "report"))},
		{InlineButtonAny(s.catalog.T(lang, "btn_refresh"), BuildCallback("ride", "refresh"))},
		{InlineButtonAny(s.catalog.T(lang, "btn_mute_30m"), BuildCallback("ride", "mute", "30", train.ID))},
		{InlineButtonAny(s.catalog.T(lang, "btn_checkout"), BuildCallback("ride", "checkout"))},
	}
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, s.withScheduleNotice(now, lang, strings.Join(lines, "\n")), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendReportPrompt(ctx context.Context, chatID int64, userID int64, lang domain.Language) error {
	now := time.Now().In(s.loc)
	active, err := s.rides.ActiveCheckIn(ctx, userID, now)
	if err != nil {
		return err
	}
	if active == nil {
		rows := [][]map[string]any{
			{InlineButtonAny(s.catalog.T(lang, "btn_start_checkin"), BuildCallback("checkin", "start"))},
		}
		if row := s.openAppButtonRow(lang); row != nil {
			rows = append(rows, row)
		}
		kb := InlineKeyboardAny(rows...)
		return s.send(ctx, chatID, s.catalog.T(lang, "report_requires_checkin"), MessageOptions{ReplyMarkup: kb})
	}
	rows := [][]map[string]any{
		{InlineButtonAny(s.catalog.T(lang, "btn_report_started"), BuildCallback("report", "type", string(domain.SignalInspectionStarted)))},
		{InlineButtonAny(s.catalog.T(lang, "btn_report_in_car"), BuildCallback("report", "type", string(domain.SignalInspectionInCar)))},
		{InlineButtonAny(s.catalog.T(lang, "btn_report_ended"), BuildCallback("report", "type", string(domain.SignalInspectionEnded)))},
		{InlineButtonAny(s.catalog.T(lang, "btn_cancel"), BuildCallback("ride", "refresh"))},
	}
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, s.catalog.T(lang, "report_prompt"), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) handleReportCallback(ctx context.Context, cb *CallbackQuery, parsed CallbackData, lang domain.Language) error {
	now := time.Now().In(s.loc)
	active, err := s.rides.ActiveCheckIn(ctx, cb.From.ID, now)
	if err != nil {
		return err
	}
	if active == nil {
		rows := [][]map[string]any{
			{InlineButtonAny(s.catalog.T(lang, "btn_start_checkin"), BuildCallback("checkin", "start"))},
		}
		if row := s.openAppButtonRow(lang); row != nil {
			rows = append(rows, row)
		}
		kb := InlineKeyboardAny(rows...)
		return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "report_requires_checkin"), MessageOptions{ReplyMarkup: kb})
	}

	switch parsed.Action {
	case "type":
		signal, ok := reports.ParseSignal(parsed.Arg1)
		if !ok {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "error_generic"), MessageOptions{})
		}
		train, err := s.schedules.GetTrain(ctx, active.TrainInstanceID)
		if err != nil {
			return err
		}
		if train == nil {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "not_found"), MessageOptions{})
		}
		confirmText := s.catalog.T(lang, "report_confirm", train.FromStation, train.ToStation, train.DepartureAt.In(s.loc).Format("15:04"), s.signalLabel(lang, signal))
		kb := InlineKeyboard(
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_confirm"), BuildCallback("report", "confirm", train.ID, string(signal)))},
			[]map[string]string{InlineButton(s.catalog.T(lang, "btn_back"), BuildCallback("ride", "report"))},
		)
		return s.send(ctx, cb.Message.Chat.ID, confirmText, MessageOptions{ReplyMarkup: kb})
	case "confirm":
		signal, ok := reports.ParseSignal(parsed.Arg2)
		if !ok {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "error_generic"), MessageOptions{})
		}
		if parsed.Arg1 == "" || parsed.Arg1 != active.TrainInstanceID {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "report_requires_checkin"), MessageOptions{})
		}
		result, err := s.reports.SubmitReport(ctx, cb.From.ID, active.TrainInstanceID, signal, now)
		if err != nil {
			return err
		}
		if result.Deduped {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "report_deduped"), MessageOptions{})
		}
		if result.CooldownRemaining > 0 {
			mins := int(result.CooldownRemaining.Minutes())
			if mins <= 0 {
				mins = 1
			}
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "report_cooldown", mins), MessageOptions{})
		}
		if result.Accepted {
			if err := s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "report_sent"), MessageOptions{}); err != nil {
				log.Printf("send ack failed: %v", err)
			}
			if err := s.notifyRideUsers(ctx, cb.From.ID, active.TrainInstanceID, signal, now); err != nil {
				log.Printf("notify users failed: %v", err)
			}
		}
		return nil
	default:
		return nil
	}
}

func (s *Service) notifyRideUsers(ctx context.Context, reporterID int64, trainID string, signal domain.SignalType, now time.Time) error {
	if s.notifier == nil {
		return nil
	}
	payload := RideAlertPayload{
		TrainID:    trainID,
		Signal:     signal,
		ReportedAt: now,
		ReporterID: reporterID,
	}
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return err
	}
	if train != nil {
		payload.FromStation = train.FromStation
		payload.ToStation = train.ToStation
		payload.DepartureAt = train.DepartureAt
		payload.ArrivalAt = train.ArrivalAt
	}
	return s.notifier.DispatchRideAlert(ctx, payload, now)
}

func (s *Service) handleRideCallback(ctx context.Context, cb *CallbackQuery, parsed CallbackData, lang domain.Language) error {
	switch parsed.Action {
	case "report":
		return s.sendReportPrompt(ctx, cb.Message.Chat.ID, cb.From.ID, lang)
	case "refresh":
		return s.sendMyRide(ctx, cb.Message.Chat.ID, cb.From.ID, lang)
	case "mute":
		minutes, err := strconv.Atoi(parsed.Arg1)
		if err != nil || minutes <= 0 {
			minutes = 30
		}
		now := time.Now().In(s.loc)
		trainID := strings.TrimSpace(parsed.Arg2)
		if trainID == "" {
			active, err := s.rides.ActiveCheckIn(ctx, cb.From.ID, now)
			if err != nil {
				return err
			}
			if active == nil {
				return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "mute_requires_train"), MessageOptions{})
			}
			trainID = active.TrainInstanceID
		}
		if err := s.rides.MuteForTrain(ctx, cb.From.ID, trainID, now, time.Duration(minutes)*time.Minute); err != nil {
			return err
		}
		return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "muted_30m"), MessageOptions{})
	case "checkout":
		if err := s.rides.Checkout(ctx, cb.From.ID, time.Now().In(s.loc)); err != nil {
			return err
		}
		kb := InlineKeyboard([]map[string]string{InlineButton(s.catalog.T(lang, "btn_undo"), BuildCallback("ride", "undo"))})
		return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "checked_out"), MessageOptions{ReplyMarkup: kb})
	case "undo":
		ok, err := s.rides.UndoCheckout(ctx, cb.From.ID, time.Now().In(s.loc))
		if err != nil {
			return err
		}
		if !ok {
			return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "undo_checkout_expired"), MessageOptions{})
		}
		return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "undo_checkout_restored"), MessageOptions{})
	}
	return nil
}

func (s *Service) handleSettingsCallback(ctx context.Context, cb *CallbackQuery, parsed CallbackData, lang domain.Language) error {
	settings, err := s.store.GetUserSettings(ctx, cb.From.ID)
	if err != nil {
		return err
	}
	switch parsed.Action {
	case "alerts":
		if err := s.store.SetAlertsEnabled(ctx, cb.From.ID, !settings.AlertsEnabled); err != nil {
			return err
		}
	case "style":
		if _, err := s.store.ToggleAlertStyle(ctx, cb.From.ID); err != nil {
			return err
		}
	case "lang":
		var target domain.Language
		switch strings.ToLower(parsed.Arg1) {
		case "lv":
			target = domain.LanguageLV
		default:
			target = domain.LanguageEN
		}
		if err := s.store.SetLanguage(ctx, cb.From.ID, target); err != nil {
			return err
		}
		lang = target
		if err := s.sendSettings(ctx, cb.Message.Chat.ID, cb.From.ID, lang); err != nil {
			return err
		}
		return s.send(ctx, cb.Message.Chat.ID, s.catalog.T(lang, "settings_updated"), MessageOptions{})
	}
	return s.sendSettings(ctx, cb.Message.Chat.ID, cb.From.ID, lang)
}

func (s *Service) sendSettings(ctx context.Context, chatID int64, userID int64, lang domain.Language) error {
	settings, err := s.store.GetUserSettings(ctx, userID)
	if err != nil {
		return err
	}
	alerts := s.catalog.T(lang, "alerts_off")
	if settings.AlertsEnabled {
		alerts = s.catalog.T(lang, "alerts_on")
	}
	style := s.catalog.T(lang, "style_detailed")
	if settings.AlertStyle == domain.AlertStyleDiscreet {
		style = s.catalog.T(lang, "style_discreet")
	}
	langLine := s.catalog.T(lang, "lang_en")
	if settings.Language == domain.LanguageLV {
		langLine = s.catalog.T(lang, "lang_lv")
	}
	text := strings.Join([]string{
		s.catalog.T(lang, "settings_title"),
		alerts,
		style,
		langLine,
	}, "\n")
	rows := [][]map[string]any{
		{InlineButtonAny(s.settingsAlertsButtonLabel(lang, settings.AlertsEnabled), BuildCallback("settings", "alerts"))},
		{InlineButtonAny(s.catalog.T(lang, "btn_settings_toggle_style"), BuildCallback("settings", "style"))},
		{InlineButtonAny("LV", BuildCallback("settings", "lang", "lv")), InlineButtonAny("EN", BuildCallback("settings", "lang", "en"))},
	}
	if row := s.reportsChannelButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: kb})
}

func (s *Service) sendHelp(ctx context.Context, chatID int64, lang domain.Language) error {
	return s.send(ctx, chatID, s.catalog.T(lang, "help"), MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
}

func (s *Service) sendInfo(ctx context.Context, chatID int64, lang domain.Language) error {
	info := strings.TrimSpace(s.versionLine)
	if info == "" {
		info = "Bot " + appversion.Display()
	}
	text := strings.Join([]string{s.catalog.T(lang, "info"), info}, "\n")
	return s.send(ctx, chatID, text, MessageOptions{ReplyMarkup: s.mainReplyKeyboard(lang)})
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

func (s *Service) sendStatus(ctx context.Context, chatID int64, userID int64, trainID string, lang domain.Language) error {
	now := time.Now().In(s.loc)
	train, err := s.schedules.GetTrain(ctx, trainID)
	if err != nil {
		return err
	}
	if train == nil {
		return s.send(ctx, chatID, s.catalog.T(lang, "not_found"), MessageOptions{})
	}
	status, err := s.reports.BuildStatus(ctx, trainID, now)
	if err != nil {
		return err
	}
	riders, err := s.store.CountActiveCheckins(ctx, trainID, now)
	if err != nil {
		return err
	}
	timeline, err := s.reports.RecentTimeline(ctx, trainID, 5)
	if err != nil {
		return err
	}
	lines := []string{
		s.catalog.T(lang, "status_view_title", train.FromStation, train.ToStation, train.DepartureAt.In(s.loc).Format("15:04")),
		s.catalog.T(lang, "ride_riders", riders),
		s.statusLine(lang, status, now),
		s.catalog.T(lang, "status_recent_events"),
	}
	if len(timeline) == 0 {
		lines = append(lines, s.catalog.T(lang, "status_no_recent_events"))
	} else {
		for _, item := range timeline {
			lines = append(lines, s.catalog.T(lang, "status_event_line", item.At.In(s.loc).Format("15:04"), s.signalLabel(lang, item.Signal), item.Count))
		}
	}
	rows := [][]map[string]any{
		{InlineButtonAny(s.catalog.T(lang, "btn_checkin_confirm"), BuildCallback("checkin", "train", trainID))},
		{InlineButtonAny(s.catalog.T(lang, "btn_report_inspection"), BuildCallback("ride", "report"))},
		{InlineButtonAny(s.catalog.T(lang, "btn_refresh"), BuildCallback("status", "view", trainID))},
	}
	if row := s.openAppButtonRow(lang); row != nil {
		rows = append(rows, row)
	}
	kb := InlineKeyboardAny(rows...)
	return s.send(ctx, chatID, s.withScheduleNotice(now, lang, strings.Join(lines, "\n")), MessageOptions{ReplyMarkup: kb})
}

func (s *Service) send(ctx context.Context, chatID int64, text string, opts MessageOptions) error {
	if opts.ReplyMarkup == nil {
		lang := s.languageFor(ctx, chatID)
		opts.ReplyMarkup = s.mainReplyKeyboard(lang)
	}
	return s.client.SendMessage(ctx, chatID, strings.TrimSpace(text), opts)
}

func (s *Service) editOrSendCallback(ctx context.Context, cb *CallbackQuery, text string, opts MessageOptions) error {
	if cb == nil || cb.Message == nil {
		return nil
	}
	err := s.client.EditMessageText(ctx, cb.Message.Chat.ID, cb.Message.MessageID, strings.TrimSpace(text), MessageEditOptions{
		ParseMode:   opts.ParseMode,
		ReplyMarkup: opts.ReplyMarkup,
	})
	if err == nil {
		return nil
	}
	// Telegram returns 400 when edited content is identical. Treat as success to avoid noisy retries.
	if strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
		return nil
	}
	return s.send(ctx, cb.Message.Chat.ID, text, opts)
}

func (s *Service) cleanupCheckinSessions(now time.Time) {
	s.checkinMu.Lock()
	defer s.checkinMu.Unlock()
	for userID, sess := range s.checkinSessions {
		if now.Sub(sess.UpdatedAt) > checkinSessionTTL {
			delete(s.checkinSessions, userID)
		}
	}
}

func (s *Service) getCheckinSession(userID int64, now time.Time) (checkinSession, bool) {
	s.checkinMu.Lock()
	defer s.checkinMu.Unlock()
	sess, ok := s.checkinSessions[userID]
	if !ok {
		return checkinSession{}, false
	}
	if now.Sub(sess.UpdatedAt) > checkinSessionTTL {
		delete(s.checkinSessions, userID)
		return checkinSession{}, false
	}
	return sess, true
}

func (s *Service) setCheckinTextFlow(userID int64, flow string, now time.Time) {
	s.checkinMu.Lock()
	sess := s.checkinSessions[userID]
	sess.Flow = flow
	sess.LastQuery = ""
	sess.ResultOffset = 0
	sess.UpdatedAt = now
	s.checkinSessions[userID] = sess
	s.checkinMu.Unlock()
}

func (s *Service) setCheckinSearchSession(userID int64, flow string, query string, offset int, now time.Time) {
	s.checkinMu.Lock()
	sess := s.checkinSessions[userID]
	sess.Flow = flow
	sess.LastQuery = strings.TrimSpace(query)
	sess.ResultOffset = offset
	sess.UpdatedAt = now
	s.checkinSessions[userID] = sess
	s.checkinMu.Unlock()
}

func (s *Service) setCheckinOriginSession(userID int64, originStationID string, now time.Time) {
	s.checkinMu.Lock()
	sess := s.checkinSessions[userID]
	sess.OriginStationID = originStationID
	sess.LastQuery = ""
	sess.ResultOffset = 0
	sess.UpdatedAt = now
	s.checkinSessions[userID] = sess
	s.checkinMu.Unlock()
}

func (s *Service) getCheckinOriginSession(userID int64, now time.Time) (string, bool) {
	sess, ok := s.getCheckinSession(userID, now)
	if !ok || strings.TrimSpace(sess.OriginStationID) == "" {
		return "", false
	}
	return sess.OriginStationID, true
}

func (s *Service) clearCheckinSession(userID int64) {
	s.checkinMu.Lock()
	delete(s.checkinSessions, userID)
	s.checkinMu.Unlock()
}

func (s *Service) windowLabel(lang domain.Language, windowID string) string {
	switch windowID {
	case "now":
		return s.catalog.T(lang, "window_now")
	case "next_hour":
		return s.catalog.T(lang, "window_next_hour")
	case "today":
		return s.catalog.T(lang, "window_today")
	default:
		return windowID
	}
}

func (s *Service) languageFor(ctx context.Context, userID int64) domain.Language {
	settings, err := s.store.EnsureUserSettings(ctx, userID)
	if err != nil {
		return domain.LanguageEN
	}
	return settings.Language
}

func (s *Service) withScheduleNotice(now time.Time, lang domain.Language, text string) string {
	access := s.schedules.AccessContext(now)
	if !access.FallbackActive || strings.TrimSpace(access.EffectiveServiceDate) == "" {
		return text
	}
	notice := s.catalog.T(lang, "schedule_fallback_notice", access.EffectiveServiceDate)
	if strings.TrimSpace(text) == "" {
		return notice
	}
	return notice + "\n\n" + text
}

func (s *Service) statusLine(lang domain.Language, status domain.TrainStatus, now time.Time) string {
	switch status.State {
	case domain.StatusNoReports:
		return s.catalog.T(lang, "status_no_reports")
	case domain.StatusMixedReports:
		return s.catalog.T(lang, "status_mixed")
	default:
		if status.LastReportAt == nil {
			return s.catalog.T(lang, "status_no_reports")
		}
		return s.catalog.T(lang, "status_last", s.relativeAgo(lang, now, status.LastReportAt.In(s.loc)))
	}
}

func (s *Service) confidenceLabel(lang domain.Language, confidence domain.Confidence) string {
	switch confidence {
	case domain.ConfidenceHigh:
		return s.catalog.T(lang, "confidence_high")
	case domain.ConfidenceMedium:
		return s.catalog.T(lang, "confidence_medium")
	default:
		return s.catalog.T(lang, "confidence_low")
	}
}

func (s *Service) signalLabel(lang domain.Language, signal domain.SignalType) string {
	switch signal {
	case domain.SignalInspectionStarted:
		return s.catalog.T(lang, "event_inspection_started")
	case domain.SignalInspectionInCar:
		return s.catalog.T(lang, "event_inspection_in_car")
	case domain.SignalInspectionEnded:
		return s.catalog.T(lang, "event_inspection_ended")
	default:
		return s.catalog.T(lang, "event_unknown")
	}
}

func (s *Service) settingsAlertsButtonLabel(lang domain.Language, enabled bool) string {
	if enabled {
		return s.catalog.T(lang, "btn_settings_disable_alerts")
	}
	return s.catalog.T(lang, "btn_settings_enable_alerts")
}

func (s *Service) relativeAgo(lang domain.Language, now time.Time, t time.Time) string {
	mins := int(now.Sub(t).Minutes())
	if mins <= 0 {
		return s.catalog.T(lang, "relative_now")
	}
	if mins == 1 {
		return s.catalog.T(lang, "relative_one_min")
	}
	return s.catalog.T(lang, "relative_many_mins", mins)
}
