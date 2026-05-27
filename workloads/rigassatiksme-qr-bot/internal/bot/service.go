package bot

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	JobWaiting   = "waiting"
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCanceled  = "canceled"
)

var fiveDigitCodePattern = regexp.MustCompile(`^[0-9]{5}$`)

type MentionedUser struct {
	UserID   int64
	Username string
}

type Message struct {
	ChatID         int64
	ChatType       string
	UserID         int64
	Username       string
	Text           string
	MentionedUsers []MentionedUser
}

type Callback struct {
	ChatID   int64
	ChatType string
	UserID   int64
	Username string
	Data     string
}

type InlineButton struct {
	Text string
	Data string
}

type QRJob struct {
	ID          string `json:"id"`
	ChatID      string `json:"chatId"`
	UserID      string `json:"userId"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	Attempts    int    `json:"attempts"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

type ServiceConfig struct {
	PollInterval time.Duration
	PollTimeout  time.Duration
	Access       AccessController
}

type Telegram interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
	SendPhoto(ctx context.Context, chatID int64, image []byte, mime string, caption string) error
}

type buttonTelegram interface {
	SendMessageWithButtons(ctx context.Context, chatID int64, text string, buttons [][]InlineButton) error
}

type LanguageStore interface {
	UserLanguage(ctx context.Context, req AccessRequest) (string, error)
	SetUserLanguage(ctx context.Context, req AccessRequest, language string) error
}

type Broker interface {
	CreateQRJob(ctx context.Context, chatID string, userID string, code string) (QRJob, error)
	Job(ctx context.Context, id string) (QRJob, error)
	LatestJob(ctx context.Context, userID string) (QRJob, bool, error)
	CancelLatestJob(ctx context.Context, userID string) (QRJob, bool, error)
	JobImage(ctx context.Context, id string) ([]byte, string, error)
}

type Service struct {
	cfg      ServiceConfig
	telegram Telegram
	broker   Broker
	access   AccessController
}

func NewService(cfg ServiceConfig, telegram Telegram, broker Broker) *Service {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 10 * time.Minute
	}
	return &Service{cfg: cfg, telegram: telegram, broker: broker, access: cfg.Access}
}

func (s *Service) HandleMessage(ctx context.Context, msg Message) error {
	text := strings.TrimSpace(msg.Text)
	userID := strconv.FormatInt(msg.UserID, 10)
	accessReq := accessRequestFromMessage(msg)
	language := s.preferredLanguage(ctx, accessReq)
	accessReq.Language = language
	if s.access != nil {
		notice, err := s.access.RecordUser(ctx, accessReq)
		if err != nil {
			return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "record_user_error"))
		}
		if notice != "" {
			if err := s.telegram.SendMessage(ctx, msg.ChatID, notice); err != nil {
				return err
			}
		}
	}
	if command, args, ok := parseCommand(text); ok {
		if s.access != nil {
			if response, handled, err := s.access.HandleAdminCommand(ctx, accessReq, command, args); handled {
				if err != nil {
					return err
				}
				return s.telegram.SendMessage(ctx, msg.ChatID, response)
			}
		}
		switch command {
		case "start", "help":
			return s.sendHelp(ctx, msg.ChatID, language)
		case "status":
			return s.handleStatus(ctx, msg.ChatID, userID, language)
		case "cancel":
			return s.handleCancel(ctx, msg.ChatID, userID, language)
		case "access":
			return s.handleAccessStatus(ctx, msg, accessReq, language)
		case "qr", "ticket", "code":
			if len(args) != 1 || !fiveDigitCodePattern.MatchString(args[0]) {
				return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "invalid_qr"))
			}
			return s.handleCode(ctx, msg, accessReq, userID, args[0], language)
		default:
			return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "unknown_command"))
		}
	}
	if fiveDigitCodePattern.MatchString(text) {
		return s.handleCode(ctx, msg, accessReq, userID, text, language)
	}
	return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "invalid_text"))
}

func (s *Service) HandleCallback(ctx context.Context, callback Callback) error {
	language, ok := parseLanguageCallback(callback.Data)
	if !ok {
		return nil
	}
	req := accessRequestFromCallback(callback)
	if store, ok := s.access.(LanguageStore); ok {
		if err := store.SetUserLanguage(ctx, req, language); err != nil {
			return s.telegram.SendMessage(ctx, callback.ChatID, botText(language, "language_update_error"))
		}
	}
	return s.sendHelp(ctx, callback.ChatID, language)
}

func (s *Service) handleCode(ctx context.Context, msg Message, accessReq AccessRequest, userID string, code string, language string) error {
	reservationID := ""
	if s.access != nil {
		reservationID = quotaReservationID(msg, userID)
		decision, err := s.access.AuthorizeAndReserve(ctx, accessReq, reservationID)
		if err != nil {
			return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "access_check_error"))
		}
		if !decision.Allowed {
			return s.telegram.SendMessage(ctx, msg.ChatID, accessDenialTextForLanguage(decision, language))
		}
	}
	job, err := s.broker.CreateQRJob(ctx, strconv.FormatInt(msg.ChatID, 10), userID, code)
	if err != nil {
		if s.access != nil {
			_ = s.access.ReleaseReservation(context.Background(), reservationID)
		}
		return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "queue_error"))
	}
	if err := s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "queued")); err != nil {
		return err
	}
	go s.waitAndDeliver(context.Background(), msg.ChatID, job.ID, reservationID, language)
	return nil
}

func (s *Service) handleStatus(ctx context.Context, chatID int64, userID string, language string) error {
	job, ok, err := s.broker.LatestJob(ctx, userID)
	if err != nil {
		return s.telegram.SendMessage(ctx, chatID, botText(language, "latest_check_error"))
	}
	if !ok {
		return s.telegram.SendMessage(ctx, chatID, botText(language, "no_request"))
	}
	return s.telegram.SendMessage(ctx, chatID, statusTextForLanguage(job, language))
}

func (s *Service) handleCancel(ctx context.Context, chatID int64, userID string, language string) error {
	_, ok, err := s.broker.CancelLatestJob(ctx, userID)
	if err != nil {
		return s.telegram.SendMessage(ctx, chatID, botText(language, "cancel_error"))
	}
	if !ok {
		return s.telegram.SendMessage(ctx, chatID, botText(language, "no_request"))
	}
	return s.telegram.SendMessage(ctx, chatID, botText(language, "cancelled"))
}

func (s *Service) handleAccessStatus(ctx context.Context, msg Message, accessReq AccessRequest, language string) error {
	if s.access == nil {
		return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "access_not_configured"))
	}
	text, err := s.access.AccessStatus(ctx, accessReq)
	if err != nil {
		return s.telegram.SendMessage(ctx, msg.ChatID, botText(language, "access_status_error"))
	}
	return s.telegram.SendMessage(ctx, msg.ChatID, text)
}

func (s *Service) waitAndDeliver(ctx context.Context, chatID int64, jobID string, reservationID string, language string) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PollTimeout)
	defer cancel()
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		job, err := s.broker.Job(ctx, jobID)
		if err == nil {
			switch job.Status {
			case JobSucceeded:
				if s.access != nil {
					_ = s.access.CommitReservation(context.Background(), reservationID)
				}
				image, mime, imageErr := s.broker.JobImage(ctx, jobID)
				if imageErr != nil {
					_ = s.telegram.SendMessage(ctx, chatID, botText(language, "qr_image_expired"))
					return
				}
				_ = s.telegram.SendPhoto(ctx, chatID, image, mime, "")
				return
			case JobFailed:
				if s.access != nil {
					_ = s.access.ReleaseReservation(context.Background(), reservationID)
				}
				_ = s.telegram.SendMessage(ctx, chatID, botText(language, "qr_failed", cleanReasonForLanguage(job.Reason, language)))
				return
			case JobCanceled:
				if s.access != nil {
					_ = s.access.ReleaseReservation(context.Background(), reservationID)
				}
				_ = s.telegram.SendMessage(ctx, chatID, botText(language, "job_cancelled"))
				return
			}
		}
		select {
		case <-ctx.Done():
			_ = s.telegram.SendMessage(context.Background(), chatID, botText(language, "still_waiting"))
			return
		case <-ticker.C:
		}
	}
}

func quotaReservationID(msg Message, userID string) string {
	return fmt.Sprintf("qr:%s:%d:%d", strings.TrimSpace(userID), msg.ChatID, time.Now().UnixNano())
}

func statusText(job QRJob) string {
	return statusTextForLanguage(job, "en")
}

func statusTextForLanguage(job QRJob, language string) string {
	switch job.Status {
	case JobWaiting:
		if job.Reason == "ticket_active" {
			return botText(language, "status_ticket_active")
		}
		return botText(language, "status_waiting")
	case JobRunning:
		return botText(language, "status_running")
	case JobSucceeded:
		return botText(language, "status_ready")
	case JobCanceled:
		return botText(language, "status_cancelled")
	case JobFailed:
		return botText(language, "qr_failed", cleanReasonForLanguage(job.Reason, language))
	default:
		return botText(language, "status_unknown", job.Status)
	}
}

func cleanReason(reason string) string {
	return cleanReasonForLanguage(reason, "en")
}

func cleanReasonForLanguage(reason string, language string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return botText(language, "reason_unknown")
	}
	switch reason {
	case "rs_app_attention_required", "rs_monthly_ticket_unknown_state":
		return botText(language, "reason_rs_attention")
	case "rs_monthly_ticket_stale_code":
		return botText(language, "reason_stale_code")
	}
	return strings.ReplaceAll(reason, "_", " ")
}

func parseCommand(text string) (string, []string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", nil, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", nil, false
	}
	command := cleanCommand(fields[0])
	if command == "" {
		return "", nil, false
	}
	return command, fields[1:], true
}

func (s *Service) preferredLanguage(ctx context.Context, req AccessRequest) string {
	if store, ok := s.access.(LanguageStore); ok {
		language, err := store.UserLanguage(ctx, req)
		if err == nil && strings.TrimSpace(language) != "" {
			return normalizePublicBotLanguage(language)
		}
	}
	return "lv"
}

func (s *Service) sendHelp(ctx context.Context, chatID int64, language string) error {
	language = normalizePublicBotLanguage(language)
	text := startText(language)
	buttons := languageButtons(language)
	if sender, ok := s.telegram.(buttonTelegram); ok {
		return sender.SendMessageWithButtons(ctx, chatID, text, buttons)
	}
	return s.telegram.SendMessage(ctx, chatID, text)
}

func parseLanguageCallback(data string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(data)) {
	case "lang:ru":
		return "ru", true
	case "lang:lv":
		return "lv", true
	default:
		return "", false
	}
}

func normalizeBotLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "ru", "rus", "russian":
		return "ru"
	case "en", "eng", "english":
		return "en"
	default:
		return "lv"
	}
}

func normalizePublicBotLanguage(language string) string {
	if normalizeBotLanguage(language) == "ru" {
		return "ru"
	}
	return "lv"
}

func languageButtons(language string) [][]InlineButton {
	if normalizePublicBotLanguage(language) == "ru" {
		return [][]InlineButton{{{Text: "Latviski", Data: "lang:lv"}}}
	}
	return [][]InlineButton{{{Text: "Русский", Data: "lang:ru"}}}
}

func startText(language string) string {
	if normalizePublicBotLanguage(language) == "ru" {
		return strings.Join([]string{
			"rs biļete бот помогает получить QR для транспорта.",
			"Отправь 5-значный код из приложения Rīgas satiksme. Я подожду, пока телефон освободится, и пришлю QR изображение.",
			"В группах используй /qr 12345, чтобы Telegram privacy mode доставил запрос.",
			"Команды: /qr 12345, /status, /cancel, /access, /help.",
		}, "\n")
	}
	return strings.Join([]string{
		"rs biļete bots palīdz saņemt transporta QR.",
		"Nosūti 5 ciparu kodu no Rīgas satiksme lietotnes. Es pagaidīšu, līdz telefons būs brīvs, un atsūtīšu QR attēlu.",
		"Grupās izmanto /qr 12345, lai Telegram privacy mode piegādātu pieprasījumu.",
		"Komandas: /qr 12345, /status, /cancel, /access, /help.",
	}, "\n")
}

func botText(language string, key string, args ...any) string {
	language = normalizeBotLanguage(language)
	var text string
	switch language {
	case "ru":
		text = botTextRU(key)
	case "en":
		text = botTextEN(key)
	default:
		text = botTextLV(key)
	}
	if text == "" {
		text = botTextLV(key)
	}
	if len(args) > 0 {
		return fmt.Sprintf(text, args...)
	}
	return text
}

func botTextLV(key string) string {
	switch key {
	case "record_user_error":
		return "Neizdevās atjaunināt piekļuvi. Lūdzu, mēģini vēlreiz."
	case "invalid_qr":
		return "Lietojums: /qr 12345"
	case "unknown_command":
		return "Nezināma komanda. Izmanto /help, /qr 12345, /status, /cancel vai nosūti tieši 5 ciparus."
	case "invalid_text":
		return "Nosūti tieši 5 ciparus vai izmanto /status un /cancel."
	case "language_update_error":
		return "Neizdevās nomainīt valodu. Lūdzu, mēģini vēlreiz."
	case "access_check_error":
		return "Neizdevās pārbaudīt piekļuvi. Lūdzu, mēģini vēlreiz."
	case "queue_error":
		return "Neizdevās pievienot kodu rindai. Lūdzu, mēģini vēlreiz."
	case "queued":
		return "Pieprasījums gaida. Atsūtīšu QR attēlu šeit, kad tas būs gatavs."
	case "latest_check_error":
		return "Neizdevās pārbaudīt pēdējo pieprasījumu."
	case "no_request":
		return "Nav rindā esoša QR pieprasījuma."
	case "cancel_error":
		return "Neizdevās atcelt pēdējo pieprasījumu."
	case "cancelled":
		return "Pēdējais QR pieprasījums atcelts."
	case "access_not_configured":
		return "Šim botam piekļuves kontrole nav ieslēgta."
	case "access_status_error":
		return "Neizdevās pārbaudīt piekļuves statusu."
	case "qr_image_expired":
		return "QR tika izveidots, bet attēls paspēja izbeigties, pirms varēju to atsūtīt."
	case "qr_failed":
		return "QR pieprasījums neizdevās: %s"
	case "job_cancelled":
		return "QR pieprasījums tika atcelts."
	case "still_waiting":
		return "QR pieprasījums joprojām gaida. Izmanto /status, lai pārbaudītu vēlāk."
	case "status_ticket_active":
		return "QR pieprasījums gaida, jo biļetes lapa pašlaik tiek izmantota."
	case "status_waiting":
		return "QR pieprasījums gaida."
	case "status_running":
		return "QR pieprasījums pašlaik tiek apstrādāts."
	case "status_ready":
		return "QR pieprasījums ir gatavs."
	case "status_cancelled":
		return "QR pieprasījums tika atcelts."
	case "status_unknown":
		return "QR pieprasījuma statuss: %s."
	case "reason_unknown":
		return "nezināms"
	case "reason_rs_attention":
		return "Rīgas satiksme lietotnei vajag uzmanību. Atver to vienreiz un mēģini vēlreiz."
	case "reason_stale_code":
		return "Rīgas satiksme joprojām rādīja iepriekšējo QR pēc jaunā koda ievades. Novecojušu attēlu nesūtīju."
	case "admin_only":
		return "Tikai administratoram."
	case "unknown_admin":
		return "Nezināma administratora komanda. Izmanto /admin palīdzībai."
	}
	return ""
}

func botTextRU(key string) string {
	switch key {
	case "record_user_error":
		return "Не удалось обновить доступ. Попробуй ещё раз."
	case "invalid_qr":
		return "Используй: /qr 12345"
	case "unknown_command":
		return "Неизвестная команда. Используй /help, /qr 12345, /status, /cancel или отправь ровно 5 цифр."
	case "invalid_text":
		return "Отправь ровно 5 цифр или используй /status и /cancel."
	case "language_update_error":
		return "Не удалось изменить язык. Попробуй ещё раз."
	case "access_check_error":
		return "Не удалось проверить доступ. Попробуй ещё раз."
	case "queue_error":
		return "Не удалось поставить код в очередь. Попробуй ещё раз."
	case "queued":
		return "Запрос ожидает. Я пришлю QR изображение сюда, когда оно будет готово."
	case "latest_check_error":
		return "Не удалось проверить последний запрос."
	case "no_request":
		return "Нет QR-запроса в очереди."
	case "cancel_error":
		return "Не удалось отменить последний запрос."
	case "cancelled":
		return "Последний QR-запрос отменён."
	case "access_not_configured":
		return "Контроль доступа для этого бота не настроен."
	case "access_status_error":
		return "Не удалось проверить статус доступа."
	case "qr_image_expired":
		return "QR был создан, но изображение истекло до отправки."
	case "qr_failed":
		return "QR-запрос не удался: %s"
	case "job_cancelled":
		return "QR-запрос был отменён."
	case "still_waiting":
		return "QR-запрос всё ещё ожидает. Используй /status, чтобы проверить позже."
	case "status_ticket_active":
		return "QR-запрос ожидает, потому что страница билета сейчас занята."
	case "status_waiting":
		return "QR-запрос ожидает."
	case "status_running":
		return "QR-запрос сейчас выполняется."
	case "status_ready":
		return "QR-запрос готов."
	case "status_cancelled":
		return "QR-запрос был отменён."
	case "status_unknown":
		return "Статус QR-запроса: %s."
	case "reason_unknown":
		return "неизвестно"
	case "reason_rs_attention":
		return "Приложению RS нужно внимание. Открой его один раз и попробуй снова."
	case "reason_stale_code":
		return "Rīgas satiksme всё ещё показывала предыдущий QR после ввода нового кода. Устаревшее изображение не отправлялось."
	case "admin_only":
		return "Только для администратора."
	case "unknown_admin":
		return "Неизвестная команда администратора. Используй /admin для помощи."
	}
	return ""
}

func botTextEN(key string) string {
	switch key {
	case "record_user_error":
		return "I could not update ticket access. Please try again."
	case "invalid_qr":
		return "Usage: /qr 12345"
	case "unknown_command":
		return "Unknown command. Use /help, /qr 12345, /status, /cancel, or send exactly 5 digits."
	case "invalid_text":
		return "Send exactly 5 digits, or use /status and /cancel."
	case "language_update_error":
		return "I could not update language preference. Please try again."
	case "access_check_error":
		return "I could not check ticket access. Please try again."
	case "queue_error":
		return "I could not queue that code. Please try again."
	case "queued":
		return "Your request is waiting. I will send the QR image here when it is ready."
	case "latest_check_error":
		return "I could not check the latest request."
	case "no_request":
		return "No QR request is queued."
	case "cancel_error":
		return "I could not cancel the latest request."
	case "cancelled":
		return "Latest QR request cancelled."
	case "access_not_configured":
		return "Access control is not configured for this bot."
	case "access_status_error":
		return "I could not check access status."
	case "qr_image_expired":
		return "The QR was created, but the image expired before I could send it."
	case "qr_failed":
		return "The QR request failed: %s"
	case "job_cancelled":
		return "The QR request was cancelled."
	case "still_waiting":
		return "The QR request is still waiting. Use /status to check it later."
	case "status_ticket_active":
		return "Your QR request is waiting because the ticket page is in use."
	case "status_waiting":
		return "Your QR request is waiting."
	case "status_running":
		return "Your QR request is running now."
	case "status_ready":
		return "Your QR request is ready."
	case "status_cancelled":
		return "Your QR request was cancelled."
	case "status_unknown":
		return "Your QR request status is %s."
	case "reason_unknown":
		return "unknown"
	case "reason_rs_attention":
		return "RS app needs attention. Open it once and retry."
	case "reason_stale_code":
		return "RS kept showing the previous QR after the new code was submitted. I did not send a stale image."
	case "admin_only":
		return "Admin only."
	case "unknown_admin":
		return "Unknown admin command. Use /admin for help."
	}
	return ""
}
