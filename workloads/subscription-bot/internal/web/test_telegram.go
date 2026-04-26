package web

import (
	"context"
	"net/http"
	"time"

	"subscriptionbot/internal/bot"
	"subscriptionbot/internal/telegram"
)

type recordedSentMessage struct {
	ChatID      int64  `json:"chat_id"`
	Text        string `json:"text"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type recordedEditedMessage struct {
	ChatID      int64  `json:"chat_id"`
	MessageID   int64  `json:"message_id"`
	Text        string `json:"text"`
	ReplyMarkup any    `json:"reply_markup,omitempty"`
}

type telegramUpdateRecorder struct {
	sentMessages   []recordedSentMessage
	editedMessages []recordedEditedMessage
	callbacks      []string
}

func (r *telegramUpdateRecorder) GetUpdates(context.Context, int64, int) ([]telegram.Update, error) {
	return nil, nil
}

func (r *telegramUpdateRecorder) SendMessage(_ context.Context, chatID int64, text string, opts telegram.MessageOptions) error {
	r.sentMessages = append(r.sentMessages, recordedSentMessage{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: opts.ReplyMarkup,
	})
	return nil
}

func (r *telegramUpdateRecorder) EditMessageText(_ context.Context, chatID int64, messageID int64, text string, opts telegram.MessageOptions) error {
	r.editedMessages = append(r.editedMessages, recordedEditedMessage{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: opts.ReplyMarkup,
	})
	return nil
}

func (r *telegramUpdateRecorder) AnswerCallbackQuery(_ context.Context, _ string, text string) error {
	r.callbacks = append(r.callbacks, text)
	return nil
}

func (r *telegramUpdateRecorder) SetMyCommands(context.Context, []telegram.BotCommand) error {
	return nil
}

func (r *telegramUpdateRecorder) SetChatMenuButton(context.Context, telegram.MenuButton) error {
	return nil
}

func (s *Server) handleTestTelegramUpdate(w http.ResponseWriter, r *http.Request, now time.Time) {
	if !s.cfg.E2EMode {
		http.NotFound(w, r)
		return
	}
	var update telegram.Update
	if err := decodeJSON(r, &update); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	recorder := &telegramUpdateRecorder{}
	botService := bot.NewService(recorder, s.app, s.cfg)
	err := botService.ProcessUpdate(r.Context(), update)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               err == nil,
		"handled":          err == nil,
		"error":            errorString(err),
		"sent_messages":    recorder.sentMessages,
		"edited_messages":  recorder.editedMessages,
		"callback_answers": recorder.callbacks,
		"update_id":        update.UpdateID,
		"processed_at":     now.UTC().Format(time.RFC3339),
	})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
