package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"chatgptbroker/internal/config"
	tgutil "chatgptbroker/internal/telegram"
)

type update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"from"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

type telegramResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if strings.TrimSpace(cfg.BotToken) == "" {
		log.Fatal("BOT_TOKEN is required for chatgpt-bot")
	}
	if len(cfg.AllowedTelegramIDs) == 0 {
		log.Fatal("CHATGPT_ALLOWED_TELEGRAM_IDS must be set; bot defaults closed")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	client := &http.Client{Timeout: cfg.HTTPTimeout}
	baseURL := "https://api.telegram.org/bot" + cfg.BotToken
	var offset int64
	log.Print("chatgpt bot polling Telegram")
	for ctx.Err() == nil {
		if err := deliverNotifications(ctx, client, baseURL, cfg.BrokerBaseURL, cfg.AllowedTelegramIDs); err != nil {
			log.Printf("deliver notifications: %v", err)
		}
		updates, err := getUpdates(ctx, client, baseURL, offset, cfg.LongPollTimeout)
		if err != nil {
			log.Printf("telegram getUpdates: %v", sanitizeTelegramError(err, cfg.BotToken))
			time.Sleep(2 * time.Second)
			continue
		}
		for _, item := range updates {
			offset = item.UpdateID + 1
			if item.Message == nil || item.Message.From == nil {
				continue
			}
			if !tgutil.AllowedUser(cfg.AllowedTelegramIDs, item.Message.From.ID) {
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "This personal broker is allowlist-only.")
				continue
			}
			text := strings.TrimSpace(item.Message.Text)
			switch {
			case text == "/start" || text == "/help":
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Send me a prompt and I will queue it for the Pixel ChatGPT broker. Commands: /status, /privacy.")
			case text == "/privacy":
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Prompts are sent to the owner-controlled ChatGPT Android app on the Pixel. Do not send secrets or regulated data.")
			case text == "/status":
				status, err := fetchStatus(ctx, client, cfg.BrokerBaseURL, item.Message.From.ID)
				if err != nil {
					log.Printf("fetch status: %v", err)
					_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Could not read broker status.")
					continue
				}
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, status)
			case text == "/health":
				health, err := fetchHealth(ctx, client, cfg.BrokerBaseURL)
				if err != nil {
					log.Printf("fetch health: %v", err)
					_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Broker health is not available.")
					continue
				}
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, health)
			case strings.HasPrefix(text, "/cancel"):
				jobID := strings.TrimSpace(strings.TrimPrefix(text, "/cancel"))
				if jobID == "" {
					_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Send /cancel followed by the job id.")
					continue
				}
				if err := cancelJob(ctx, client, cfg.BrokerBaseURL, jobID); err != nil {
					log.Printf("cancel job: %v", err)
					_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Could not cancel that job.")
					continue
				}
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Cancellation requested for "+jobID)
			case strings.HasPrefix(text, "/"):
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Unknown command.")
			case text != "":
				jobID, err := submitJob(ctx, client, cfg.BrokerBaseURL, item.Message.Chat.ID, item.Message.From.ID, text, cfg.DefaultProjectName)
				if err != nil {
					log.Printf("submit job: %v", err)
					_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Could not queue that request.")
					continue
				}
				_ = sendMessage(ctx, client, baseURL, item.Message.Chat.ID, "Queued "+jobID)
			}
		}
	}
}

func getUpdates(ctx context.Context, client *http.Client, baseURL string, offset int64, timeout int) ([]update, error) {
	url := baseURL + "/getUpdates?timeout=" + strconv.Itoa(timeout)
	if offset > 0 {
		url += "&offset=" + strconv.FormatInt(offset, 10)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload telegramResponse[[]update]
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if !payload.OK {
		return nil, errors.New(payload.Description)
	}
	return payload.Result, nil
}

func sendMessage(ctx context.Context, client *http.Client, baseURL string, chatID int64, text string) error {
	for _, chunk := range tgutil.ChunkText(text, tgutil.MessageLimit) {
		payload, _ := json.Marshal(map[string]any{"chat_id": chatID, "text": chunk})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/sendMessage", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("telegram sendMessage status %d", resp.StatusCode)
		}
	}
	return nil
}

func submitJob(ctx context.Context, client *http.Client, brokerURL string, chatID, userID int64, prompt, project string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"telegramChatId": chatID,
		"telegramUserId": userID,
		"prompt":         prompt,
		"projectName":    project,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(brokerURL, "/")+"/api/v1/jobs", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("broker status %d", resp.StatusCode)
	}
	return body.Job.ID, nil
}

type brokerJob struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	PublicStatus string `json:"publicStatus"`
	FailureCode  string `json:"failureCode"`
}

func fetchStatus(ctx context.Context, client *http.Client, brokerURL string, _ int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(brokerURL, "/")+"/api/v1/jobs", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("broker status %d", resp.StatusCode)
	}
	var body struct {
		Jobs []brokerJob `json:"jobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	lines := []string{}
	for _, job := range body.Jobs {
		line := job.ID + " - " + job.Status
		if strings.TrimSpace(job.PublicStatus) != "" {
			line += " - " + job.PublicStatus
		}
		if strings.TrimSpace(job.FailureCode) != "" {
			line += " (" + job.FailureCode + ")"
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "No jobs yet.", nil
	}
	return strings.Join(lines, "\n"), nil
}

func fetchHealth(ctx context.Context, client *http.Client, brokerURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(brokerURL, "/")+"/healthz", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("broker status %d", resp.StatusCode)
	}
	var body struct {
		OK        bool   `json:"ok"`
		Project   string `json:"project"`
		Spacetime bool   `json:"spacetime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.OK && body.Spacetime {
		return "Broker is connected to Spacetime.", nil
	}
	return "Broker is running, but Spacetime is not ready.", nil
}

type brokerNotification struct {
	ID             string `json:"id"`
	TelegramChatID string `json:"telegramChatId"`
	TelegramUserID string `json:"telegramUserId"`
	Status         string `json:"status"`
	PublicStatus   string `json:"publicStatus"`
	ResultText     string `json:"resultText"`
	FailureCode    string `json:"failureCode"`
}

func deliverNotifications(ctx context.Context, client *http.Client, telegramURL, brokerURL string, allowed map[int64]struct{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(brokerURL, "/")+"/api/v1/notifications", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("broker status %d", resp.StatusCode)
	}
	var body struct {
		Notifications []brokerNotification `json:"notifications"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	for _, item := range body.Notifications {
		chatID, chatErr := strconv.ParseInt(strings.TrimSpace(item.TelegramChatID), 10, 64)
		userID, userErr := strconv.ParseInt(strings.TrimSpace(item.TelegramUserID), 10, 64)
		if chatErr != nil || userErr != nil || !tgutil.AllowedUser(allowed, userID) {
			continue
		}
		text := notificationText(item)
		if err := sendMessage(ctx, client, telegramURL, chatID, text); err != nil {
			return err
		}
		if err := markNotified(ctx, client, brokerURL, item.ID); err != nil {
			return err
		}
	}
	return nil
}

func notificationText(item brokerNotification) string {
	if item.Status == "succeeded" && strings.TrimSpace(item.ResultText) != "" {
		return strings.TrimSpace(item.ResultText)
	}
	text := strings.TrimSpace(item.PublicStatus)
	if text == "" {
		text = "Job " + item.ID + " finished with status " + item.Status
	}
	if strings.TrimSpace(item.FailureCode) != "" {
		text += " (" + strings.TrimSpace(item.FailureCode) + ")"
	}
	return text
}

func markNotified(ctx context.Context, client *http.Client, brokerURL, jobID string) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(brokerURL, "/")+"/api/v1/jobs/"+strings.TrimSpace(jobID)+"/notified",
		nil,
	)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("broker status %d", resp.StatusCode)
	}
	return nil
}

func cancelJob(ctx context.Context, client *http.Client, brokerURL, jobID string) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(brokerURL, "/")+"/api/v1/jobs/"+strings.TrimSpace(jobID)+"/cancel",
		nil,
	)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("broker status %d", resp.StatusCode)
	}
	return nil
}

func sanitizeTelegramError(err error, token string) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "<redacted>"))
}
