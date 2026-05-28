package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rigassatiksmeqrbot/internal/bot"
)

type Client struct {
	baseURL         string
	redactedBaseURL string
	httpClient      *http.Client
}

func NewClient(token string, timeout time.Duration) *Client {
	baseURL := fmt.Sprintf("https://api.telegram.org/bot%s", strings.TrimSpace(token))
	return &Client{
		baseURL:         baseURL,
		redactedBaseURL: "https://api.telegram.org/bot<redacted>",
		httpClient:      &http.Client{Timeout: timeout},
	}
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

type Message struct {
	MessageID      int64           `json:"message_id"`
	From           *User           `json:"from,omitempty"`
	Chat           Chat            `json:"chat"`
	Date           int64           `json:"date"`
	Text           string          `json:"text,omitempty"`
	Entities       []MessageEntity `json:"entities,omitempty"`
	ReplyToMessage *Message        `json:"reply_to_message,omitempty"`
}

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LanguageCode string `json:"language_code,omitempty"`
	Username     string `json:"username,omitempty"`
}

type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
	User   *User  `json:"user,omitempty"`
}

type Chat struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Username string `json:"username,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from,omitempty"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description,omitempty"`
}

func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(timeout))
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/getUpdates?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, c.sanitizeError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("telegram getUpdates status %d: %s", resp.StatusCode, string(body))
	}
	var payload apiResponse[[]Update]
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, fmt.Errorf("telegram getUpdates failed: %s", payload.Description)
	}
	return payload.Result, nil
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	return c.postJSON(ctx, "/sendMessage", map[string]any{"chat_id": chatID, "text": text})
}

func (c *Client) SendMessageWithButtons(ctx context.Context, chatID int64, text string, buttons [][]bot.InlineButton) error {
	keyboard := make([][]map[string]string, 0, len(buttons))
	for _, row := range buttons {
		outRow := make([]map[string]string, 0, len(row))
		for _, button := range row {
			if strings.TrimSpace(button.Text) == "" || strings.TrimSpace(button.Data) == "" {
				continue
			}
			outRow = append(outRow, map[string]string{
				"text":          button.Text,
				"callback_data": button.Data,
			})
		}
		if len(outRow) > 0 {
			keyboard = append(keyboard, outRow)
		}
	}
	payload := map[string]any{"chat_id": chatID, "text": text}
	if len(keyboard) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	return c.postJSON(ctx, "/sendMessage", payload)
}

func (c *Client) SendPhoto(ctx context.Context, chatID int64, image []byte, mime string, caption string) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	if strings.TrimSpace(caption) != "" {
		if err := writer.WriteField("caption", caption); err != nil {
			return err
		}
	}
	if strings.TrimSpace(mime) == "" {
		mime = "image/png"
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="photo"; filename="qr.png"`)
	header.Set("Content-Type", mime)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(image); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sendPhoto", &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.sanitizeError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("telegram sendPhoto status %d: %s", resp.StatusCode, string(data))
	}
	var out apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("telegram sendPhoto failed: %s", out.Description)
	}
	return nil
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	return c.SetMyCommandsForLanguage(ctx, commands, "")
}

func (c *Client) SetMyCommandsForLanguage(ctx context.Context, commands []BotCommand, languageCode string) error {
	payload := map[string]any{"commands": commands}
	if strings.TrimSpace(languageCode) != "" {
		payload["language_code"] = strings.TrimSpace(languageCode)
	}
	return c.postJSON(ctx, "/setMyCommands", payload)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	return c.postJSON(ctx, "/answerCallbackQuery", map[string]any{"callback_query_id": id})
}

func (c *Client) GetChat(ctx context.Context, chatID string) (Chat, error) {
	q := url.Values{}
	q.Set("chat_id", strings.TrimSpace(chatID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/getChat?"+q.Encode(), nil)
	if err != nil {
		return Chat{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Chat{}, c.sanitizeError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Chat{}, fmt.Errorf("telegram getChat status %d: %s", resp.StatusCode, string(data))
	}
	var out apiResponse[Chat]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Chat{}, err
	}
	if !out.OK {
		return Chat{}, fmt.Errorf("telegram getChat failed: %s", out.Description)
	}
	return out.Result, nil
}

func (c *Client) postJSON(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.sanitizeError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("telegram %s status %d: %s", path, resp.StatusCode, string(data))
	}
	var out apiResponse[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.OK {
		return fmt.Errorf("telegram %s failed: %s", path, out.Description)
	}
	return nil
}

func (c *Client) sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), c.baseURL, c.redactedBaseURL))
}
