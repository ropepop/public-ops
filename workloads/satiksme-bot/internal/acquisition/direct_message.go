package acquisition

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type DirectTestMessageOptions struct {
	APIID                   int
	APIHash                 string
	SessionFile             string
	TargetUsername          string
	ConfirmTargetUsername   string
	ExpectSenderUsername    string
	Message                 string
	AllowUnconfirmedTesting bool
}

type DirectTestMessageResult struct {
	SenderUsername string `json:"senderUsername"`
	TargetUsername string `json:"targetUsername"`
	Message        string `json:"message"`
	Sent           bool   `json:"sent"`
}

type MTProtoOutreachConfig struct {
	APIID       int
	APIHash     string
	SessionFile string
}

type MTProtoOutreach struct {
	cfg MTProtoOutreachConfig
}

func NewMTProtoOutreach(cfg MTProtoOutreachConfig) *MTProtoOutreach {
	return &MTProtoOutreach{cfg: cfg}
}

func (m *MTProtoOutreach) SenderUsername(ctx context.Context) (string, error) {
	var username string
	err := m.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		user, err := selfUser(ctx, client.API())
		if err != nil {
			return err
		}
		username = cleanUsername(user.Username)
		return nil
	})
	return username, err
}

func (m *MTProtoOutreach) SendDirect(ctx context.Context, candidate Candidate, text string) error {
	return m.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		peer, err := inputPeerForCandidate(ctx, client.API(), candidate)
		if err != nil {
			return err
		}
		return client.SendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:      peer,
			Message:   strings.TrimSpace(text),
			NoWebpage: true,
		})
	})
}

func (m *MTProtoOutreach) PollReplies(ctx context.Context, candidates []Candidate) ([]ContactReply, error) {
	replies := []ContactReply{}
	err := m.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		for _, candidate := range candidates {
			peer, err := inputPeerForCandidate(ctx, client.API(), candidate)
			if err != nil {
				continue
			}
			result, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer:  peer,
				Limit: 10,
			})
			if err != nil {
				return err
			}
			for _, message := range messagesFromHistory(result) {
				if message == nil || int64(message.ID) <= candidate.LastReplyID || strings.TrimSpace(message.Message) == "" {
					continue
				}
				if peerUserID(message.FromID) != candidate.UserID {
					continue
				}
				replies = append(replies, ContactReply{
					UserID:    candidate.UserID,
					Username:  candidate.Username,
					MessageID: int64(message.ID),
					Text:      message.Message,
					SentAt:    timeFromTelegramDate(message.Date),
				})
			}
		}
		return nil
	})
	sort.SliceStable(replies, func(i, j int) bool {
		if replies[i].UserID != replies[j].UserID {
			return replies[i].UserID < replies[j].UserID
		}
		return replies[i].MessageID < replies[j].MessageID
	})
	return replies, err
}

func ValidateDirectTestMessage(opts DirectTestMessageOptions) error {
	target := cleanUsername(opts.TargetUsername)
	confirmed := cleanUsername(opts.ConfirmTargetUsername)
	expectedSender := cleanUsername(opts.ExpectSenderUsername)
	if target == "" {
		return fmt.Errorf("target username is required")
	}
	if !opts.AllowUnconfirmedTesting && confirmed != target {
		return fmt.Errorf("confirmed recipient must match target username")
	}
	if expectedSender == "" {
		return fmt.Errorf("expected sender username is required")
	}
	if strings.TrimSpace(opts.Message) == "" {
		return fmt.Errorf("message is required")
	}
	return nil
}

func DefaultDirectTestMessage(senderUsername string, targetUsername string) string {
	sender := "@" + cleanUsername(senderUsername)
	target := "@" + cleanUsername(targetUsername)
	return fmt.Sprintf("Test message from %s to %s for the rs biļete outreach setup. No action needed.", sender, target)
}

func SendDirectTestMessage(ctx context.Context, opts DirectTestMessageOptions) (DirectTestMessageResult, error) {
	if err := ValidateDirectTestMessage(opts); err != nil {
		return DirectTestMessageResult{}, err
	}
	if opts.APIID <= 0 {
		return DirectTestMessageResult{}, fmt.Errorf("telegram API ID is required")
	}
	if strings.TrimSpace(opts.APIHash) == "" {
		return DirectTestMessageResult{}, fmt.Errorf("telegram API hash is required")
	}
	if strings.TrimSpace(opts.SessionFile) == "" {
		return DirectTestMessageResult{}, fmt.Errorf("sender session file is required")
	}
	targetUsername := cleanUsername(opts.TargetUsername)
	expectedSender := cleanUsername(opts.ExpectSenderUsername)
	message := strings.TrimSpace(opts.Message)

	client := telegram.NewClient(opts.APIID, strings.TrimSpace(opts.APIHash), telegram.Options{
		SessionStorage: &session.FileStorage{Path: filepath.Clean(opts.SessionFile)},
		NoUpdates:      true,
	})
	result := DirectTestMessageResult{TargetUsername: targetUsername, Message: message}
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status == nil || !status.Authorized {
			return fmt.Errorf("telegram sender session is not authorized")
		}
		self, err := selfUser(ctx, client.API())
		if err != nil {
			return err
		}
		senderUsername := cleanUsername(self.Username)
		result.SenderUsername = senderUsername
		if senderUsername != expectedSender {
			return fmt.Errorf("sender session is @%s, want @%s", senderUsername, expectedSender)
		}
		target, err := resolveUserByUsername(ctx, client.API(), targetUsername)
		if err != nil {
			return err
		}
		peer := &tg.InputPeerUser{UserID: target.ID, AccessHash: target.AccessHash}
		if err := client.SendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:      peer,
			Message:   message,
			NoWebpage: true,
		}); err != nil {
			return err
		}
		result.Sent = true
		return nil
	})
	return result, err
}

func (m *MTProtoOutreach) run(ctx context.Context, fn func(context.Context, *telegram.Client) error) error {
	if m.cfg.APIID <= 0 {
		return fmt.Errorf("telegram API ID is required")
	}
	if strings.TrimSpace(m.cfg.APIHash) == "" {
		return fmt.Errorf("telegram API hash is required")
	}
	if strings.TrimSpace(m.cfg.SessionFile) == "" {
		return fmt.Errorf("sender session file is required")
	}
	client := telegram.NewClient(m.cfg.APIID, strings.TrimSpace(m.cfg.APIHash), telegram.Options{
		SessionStorage: &session.FileStorage{Path: filepath.Clean(m.cfg.SessionFile)},
		NoUpdates:      true,
	})
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status == nil || !status.Authorized {
			return fmt.Errorf("telegram sender session is not authorized")
		}
		return fn(ctx, client)
	})
}

func inputPeerForCandidate(ctx context.Context, api *tg.Client, candidate Candidate) (tg.InputPeerClass, error) {
	if candidate.UserID > 0 && candidate.AccessHash != 0 {
		return &tg.InputPeerUser{UserID: candidate.UserID, AccessHash: candidate.AccessHash}, nil
	}
	username := cleanUsername(candidate.Username)
	if username == "" {
		return nil, fmt.Errorf("candidate %d has no username or access hash", candidate.UserID)
	}
	user, err := resolveUserByUsername(ctx, api, username)
	if err != nil {
		return nil, err
	}
	return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
}

func timeFromTelegramDate(date int) time.Time {
	if date <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(date), 0).UTC()
}

func selfUser(ctx context.Context, api *tg.Client) (*tg.User, error) {
	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, err
	}
	for _, raw := range users {
		if user, ok := raw.(*tg.User); ok {
			return user, nil
		}
	}
	return nil, fmt.Errorf("telegram self user was not returned")
}

func resolveUserByUsername(ctx context.Context, api *tg.Client, username string) (*tg.User, error) {
	resolved, err := api.ContactsResolveUsername(ctx, cleanUsername(username))
	if err != nil {
		return nil, err
	}
	peer, ok := resolved.Peer.(*tg.PeerUser)
	if !ok {
		return nil, fmt.Errorf("@%s did not resolve to a Telegram user", cleanUsername(username))
	}
	for _, raw := range resolved.Users {
		user, ok := raw.(*tg.User)
		if ok && user.ID == peer.UserID && !skipTelegramUser(user) {
			return user, nil
		}
	}
	return nil, fmt.Errorf("@%s resolved without usable user metadata", cleanUsername(username))
}
