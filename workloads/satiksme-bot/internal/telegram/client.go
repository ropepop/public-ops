package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL         string
	redactedBaseURL string
	httpClient      *http.Client
}

func NewClient(token string, timeout time.Duration) *Client {
	baseURL := fmt.Sprintf("https://api.telegram.org/bot%s", token)
	return &Client{
		baseURL:         baseURL,
		redactedBaseURL: "https://api.telegram.org/bot<redacted>",
		httpClient:      &http.Client{Timeout: timeout},
	}
}

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text,omitempty"`
}

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	FirstName    string `json:"first_name"`
	LanguageCode string `json:"language_code,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description,omitempty"`
}

type MessageOptions struct {
	ReplyMarkup any
	ParseMode   string
}

type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type ReplyKeyboardMarkup struct {
	Keyboard        [][]KeyboardButton `json:"keyboard"`
	ResizeKeyboard  bool               `json:"resize_keyboard,omitempty"`
	IsPersistent    bool               `json:"is_persistent,omitempty"`
	InputFieldLabel string             `json:"input_field_placeholder,omitempty"`
}

type KeyboardButton struct {
	Text   string      `json:"text"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text   string      `json:"text"`
	URL    string      `json:"url,omitempty"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
}

type WebAppInfo struct {
	URL string `json:"url"`
}

type MenuButtonWebApp struct {
	Type   string      `json:"type"`
	Text   string      `json:"text"`
	WebApp *WebAppInfo `json:"web_app,omitempty"`
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

func (c *Client) SendMessage(ctx context.Context, chatID any, text string, opts MessageOptions) error {
	payload := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if opts.ReplyMarkup != nil {
		payload["reply_markup"] = opts.ReplyMarkup
	}
	if opts.ParseMode != "" {
		payload["parse_mode"] = opts.ParseMode
	}
	return c.post(ctx, "/sendMessage", payload)
}

func (c *Client) SetMyCommands(ctx context.Context, commands []BotCommand) error {
	payload := map[string]any{
		"commands": commands,
	}
	return c.post(ctx, "/setMyCommands", payload)
}

func (c *Client) SetChatMenuButton(ctx context.Context, button MenuButtonWebApp) error {
	payload := map[string]any{
		"menu_button": button,
	}
	return c.post(ctx, "/setChatMenuButton", payload)
}

func (c *Client) SetMyName(ctx context.Context, name string) error {
	payload := map[string]any{
		"name": name,
	}
	return c.post(ctx, "/setMyName", payload)
}

func (c *Client) SetMyShortDescription(ctx context.Context, description string) error {
	payload := map[string]any{
		"short_description": description,
	}
	return c.post(ctx, "/setMyShortDescription", payload)
}

func (c *Client) SetMyDescription(ctx context.Context, description string) error {
	payload := map[string]any{
		"description": description,
	}
	return c.post(ctx, "/setMyDescription", payload)
}

func (c *Client) post(ctx context.Context, path string, payload any) error {
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
