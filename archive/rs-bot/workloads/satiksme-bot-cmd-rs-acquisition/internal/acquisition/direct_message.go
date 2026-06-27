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

type SenderInfo struct {
	UserID   int64  `json:"userId"`
	Username string `json:"username"`
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
	info, err := m.SenderInfo(ctx)
	return info.Username, err
}

func (m *MTProtoOutreach) SenderInfo(ctx context.Context) (SenderInfo, error) {
	var info SenderInfo
	err := m.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		user, err := selfUser(ctx, client.API())
		if err != nil {
			return err
		}
		info = SenderInfo{UserID: user.ID, Username: cleanUsername(user.Username)}
		return nil
	})
	return info, err
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

func (m *MTProtoOutreach) SendUsernameMessage(ctx context.Context, username string, text string) error {
	username = cleanUsername(username)
	if username == "" {
		return fmt.Errorf("target username is required")
	}
	message := strings.TrimSpace(text)
	if message == "" {
		return fmt.Errorf("message is required")
	}
	return m.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		user, err := resolveUserByUsernameWithOptions(ctx, client.API(), username, resolveUserOptions{AllowBot: true})
		if err != nil {
			return err
		}
		return client.SendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:      &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash},
			Message:   message,
			NoWebpage: true,
		})
	})
}

type MTProtoGrantGatewayConfig struct {
	APIID       int
	APIHash     string
	SessionFile string
	BotUsername string
}

type MTProtoGrantGateway struct {
	outreach    *MTProtoOutreach
	botUsername string
}

func NewMTProtoGrantGateway(cfg MTProtoGrantGatewayConfig) (*MTProtoGrantGateway, error) {
	botUsername := cleanUsername(cfg.BotUsername)
	if botUsername == "" {
		return nil, fmt.Errorf("grant bot username is required")
	}
	return &MTProtoGrantGateway{
		outreach:    NewMTProtoOutreach(MTProtoOutreachConfig{APIID: cfg.APIID, APIHash: cfg.APIHash, SessionFile: cfg.SessionFile}),
		botUsername: botUsername,
	}, nil
}

func (g *MTProtoGrantGateway) SendGrantCommand(ctx context.Context, command string) error {
	if g == nil || g.outreach == nil {
		return fmt.Errorf("grant gateway is not configured")
	}
	return g.outreach.SendUsernameMessage(ctx, g.botUsername, command)
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
				if nonFatalReplyPollError(err) {
					continue
				}
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
		target, err := resolveUserByUsernameWithOptions(ctx, client.API(), targetUsername, resolveUserOptions{AllowBot: true})
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
	username := cleanUsername(candidate.Username)
	if username != "" {
		user, err := resolveUserByUsername(ctx, api, username)
		if err == nil {
			return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, nil
		}
		if candidate.UserID <= 0 || candidate.AccessHash == 0 {
			return nil, err
		}
	}
	if candidate.UserID > 0 && candidate.AccessHash != 0 {
		return &tg.InputPeerUser{UserID: candidate.UserID, AccessHash: candidate.AccessHash}, nil
	}
	return nil, fmt.Errorf("candidate %d has no username or access hash", candidate.UserID)
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
	return resolveUserByUsernameWithOptions(ctx, api, username, resolveUserOptions{})
}

type resolveUserOptions struct {
	AllowBot bool
}

func resolveUserByUsernameWithOptions(ctx context.Context, api *tg.Client, username string, opts resolveUserOptions) (*tg.User, error) {
	resolved, err := api.ContactsResolveUsername(ctx, cleanUsername(username))
	if err != nil {
		return nil, err
	}
	return selectResolvedUser(username, resolved, opts)
}

func selectResolvedUser(username string, resolved *tg.ContactsResolvedPeer, opts resolveUserOptions) (*tg.User, error) {
	peer, ok := resolved.Peer.(*tg.PeerUser)
	if !ok {
		return nil, fmt.Errorf("@%s did not resolve to a Telegram user", cleanUsername(username))
	}
	for _, raw := range resolved.Users {
		user, ok := raw.(*tg.User)
		if ok && user.ID == peer.UserID && !skipResolvedUser(user, opts) {
			return user, nil
		}
	}
	return nil, fmt.Errorf("@%s resolved without usable user metadata", cleanUsername(username))
}

func skipResolvedUser(user *tg.User, opts resolveUserOptions) bool {
	if user == nil || user.ID <= 0 || user.Deleted || user.Self || user.Fake || user.Scam {
		return true
	}
	return user.Bot && !opts.AllowBot
}
