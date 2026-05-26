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
	if s.access != nil {
		notice, err := s.access.RecordUser(ctx, accessRequestFromMessage(msg))
		if err != nil {
			return s.telegram.SendMessage(ctx, msg.ChatID, "I could not update ticket access. Please try again.")
		}
		if notice != "" {
			if err := s.telegram.SendMessage(ctx, msg.ChatID, notice); err != nil {
				return err
			}
		}
	}
	if command, args, ok := parseCommand(text); ok {
		if s.access != nil {
			if response, handled, err := s.access.HandleAdminCommand(ctx, accessRequestFromMessage(msg), command, args); handled {
				if err != nil {
					return err
				}
				return s.telegram.SendMessage(ctx, msg.ChatID, response)
			}
		}
		switch command {
		case "start", "help":
			return s.telegram.SendMessage(ctx, msg.ChatID, startText())
		case "status":
			return s.handleStatus(ctx, msg.ChatID, userID)
		case "cancel":
			return s.handleCancel(ctx, msg.ChatID, userID)
		case "access":
			return s.handleAccessStatus(ctx, msg)
		case "qr", "ticket", "code":
			if len(args) != 1 || !fiveDigitCodePattern.MatchString(args[0]) {
				return s.telegram.SendMessage(ctx, msg.ChatID, "Usage: /qr 12345")
			}
			return s.handleCode(ctx, msg, userID, args[0])
		default:
			return s.telegram.SendMessage(ctx, msg.ChatID, "Unknown command. Use /help, /qr 12345, /status, /cancel, or send exactly 5 digits.")
		}
	}
	if fiveDigitCodePattern.MatchString(text) {
		return s.handleCode(ctx, msg, userID, text)
	}
	return s.telegram.SendMessage(ctx, msg.ChatID, "Send exactly 5 digits, or use /status and /cancel.")
}

func (s *Service) handleCode(ctx context.Context, msg Message, userID string, code string) error {
	accessReq := accessRequestFromMessage(msg)
	reservationID := ""
	if s.access != nil {
		reservationID = quotaReservationID(msg, userID)
		decision, err := s.access.AuthorizeAndReserve(ctx, accessReq, reservationID)
		if err != nil {
			return s.telegram.SendMessage(ctx, msg.ChatID, "I could not check ticket access. Please try again.")
		}
		if !decision.Allowed {
			return s.telegram.SendMessage(ctx, msg.ChatID, accessDenialText(decision))
		}
	}
	job, err := s.broker.CreateQRJob(ctx, strconv.FormatInt(msg.ChatID, 10), userID, code)
	if err != nil {
		if s.access != nil {
			_ = s.access.ReleaseReservation(context.Background(), reservationID)
		}
		return s.telegram.SendMessage(ctx, msg.ChatID, "I could not queue that code. Please try again.")
	}
	if err := s.telegram.SendMessage(ctx, msg.ChatID, "Your request is waiting. I will send the QR image here when it is ready."); err != nil {
		return err
	}
	go s.waitAndDeliver(context.Background(), msg.ChatID, job.ID, reservationID)
	return nil
}

func (s *Service) handleStatus(ctx context.Context, chatID int64, userID string) error {
	job, ok, err := s.broker.LatestJob(ctx, userID)
	if err != nil {
		return s.telegram.SendMessage(ctx, chatID, "I could not check the latest request.")
	}
	if !ok {
		return s.telegram.SendMessage(ctx, chatID, "No QR request is queued.")
	}
	return s.telegram.SendMessage(ctx, chatID, statusText(job))
}

func (s *Service) handleCancel(ctx context.Context, chatID int64, userID string) error {
	_, ok, err := s.broker.CancelLatestJob(ctx, userID)
	if err != nil {
		return s.telegram.SendMessage(ctx, chatID, "I could not cancel the latest request.")
	}
	if !ok {
		return s.telegram.SendMessage(ctx, chatID, "No QR request is queued.")
	}
	return s.telegram.SendMessage(ctx, chatID, "Latest QR request cancelled.")
}

func (s *Service) handleAccessStatus(ctx context.Context, msg Message) error {
	if s.access == nil {
		return s.telegram.SendMessage(ctx, msg.ChatID, "Access control is not configured for this bot.")
	}
	text, err := s.access.AccessStatus(ctx, accessRequestFromMessage(msg))
	if err != nil {
		return s.telegram.SendMessage(ctx, msg.ChatID, "I could not check access status.")
	}
	return s.telegram.SendMessage(ctx, msg.ChatID, text)
}

func (s *Service) waitAndDeliver(ctx context.Context, chatID int64, jobID string, reservationID string) {
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
					_ = s.telegram.SendMessage(ctx, chatID, "The QR was created, but the image expired before I could send it.")
					return
				}
				_ = s.telegram.SendPhoto(ctx, chatID, image, mime, "")
				return
			case JobFailed:
				if s.access != nil {
					_ = s.access.ReleaseReservation(context.Background(), reservationID)
				}
				_ = s.telegram.SendMessage(ctx, chatID, fmt.Sprintf("The QR request failed: %s", cleanReason(job.Reason)))
				return
			case JobCanceled:
				if s.access != nil {
					_ = s.access.ReleaseReservation(context.Background(), reservationID)
				}
				_ = s.telegram.SendMessage(ctx, chatID, "The QR request was cancelled.")
				return
			}
		}
		select {
		case <-ctx.Done():
			_ = s.telegram.SendMessage(context.Background(), chatID, "The QR request is still waiting. Use /status to check it later.")
			return
		case <-ticker.C:
		}
	}
}

func quotaReservationID(msg Message, userID string) string {
	return fmt.Sprintf("qr:%s:%d:%d", strings.TrimSpace(userID), msg.ChatID, time.Now().UnixNano())
}

func statusText(job QRJob) string {
	switch job.Status {
	case JobWaiting:
		if job.Reason == "ticket_active" {
			return "Your QR request is waiting because the ticket page is in use."
		}
		return "Your QR request is waiting."
	case JobRunning:
		return "Your QR request is running now."
	case JobSucceeded:
		return "Your QR request is ready."
	case JobCanceled:
		return "Your QR request was cancelled."
	case JobFailed:
		return "Your QR request failed: " + cleanReason(job.Reason)
	default:
		return "Your QR request status is " + job.Status + "."
	}
}

func cleanReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "unknown"
	}
	switch reason {
	case "rs_app_attention_required", "rs_monthly_ticket_unknown_state":
		return "RS app needs attention. Open it once and retry."
	case "rs_monthly_ticket_stale_code":
		return "RS kept showing the previous QR after the new code was submitted. I did not send a stale image."
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

func startText() string {
	return strings.Join([]string{
		"Send one 5 digit code. I will wait for the phone to be free and send back the QR image.",
		"In groups, use /qr 12345 so Telegram privacy mode still delivers the request.",
		"Commands: /qr 12345, /status, /cancel, /access, /help.",
	}, "\n")
}
