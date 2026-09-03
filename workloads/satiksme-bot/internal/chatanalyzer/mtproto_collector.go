package chatanalyzer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"satiksmebot/internal/model"
	"satiksmebot/internal/store"
)

var ErrMTProtoSessionUnauthorized = errors.New("Telegram account session is not authorized")

const (
	maxStaleCatchUpPagesPerCollect = 4
	staleCatchUpPageDelay          = 250 * time.Millisecond
)

type MTProtoCollectorConfig struct {
	APIID         int
	APIHash       string
	SessionFile   string
	ChatID        string
	Store         store.ChatAnalyzerStore
	PageSize      int
	MaxMessageAge time.Duration
	Now           func() time.Time
}

type MTProtoCollector struct {
	apiID         int
	apiHash       string
	sessionFile   string
	chatID        string
	store         store.ChatAnalyzerStore
	pageSize      int
	maxMessageAge time.Duration
	now           func() time.Time
}

func NewMTProtoCollector(cfg MTProtoCollectorConfig) *MTProtoCollector {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 {
		pageSize = 25
	}
	maxMessageAge := cfg.MaxMessageAge
	if maxMessageAge <= 0 {
		maxMessageAge = 24 * time.Hour
	}
	return &MTProtoCollector{
		apiID:         cfg.APIID,
		apiHash:       strings.TrimSpace(cfg.APIHash),
		sessionFile:   strings.TrimSpace(cfg.SessionFile),
		chatID:        strings.TrimSpace(cfg.ChatID),
		store:         cfg.Store,
		pageSize:      pageSize,
		maxMessageAge: maxMessageAge,
		now:           now,
	}
}

func (c *MTProtoCollector) Collect(ctx context.Context) (CollectionResult, error) {
	if c == nil || c.apiID <= 0 || c.apiHash == "" || c.sessionFile == "" || c.chatID == "" || c.store == nil {
		return CollectionResult{}, fmt.Errorf("telegram chat collector is not configured")
	}
	result := CollectionResult{}
	client := telegram.NewClient(c.apiID, c.apiHash, telegram.Options{
		SessionStorage: NewAtomicSessionStorage(c.sessionFile),
		NoUpdates:      true,
	})
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status == nil || !status.Authorized {
			return fmt.Errorf("%w; run chat-analyzer-session first", ErrMTProtoSessionUnauthorized)
		}
		peer, checkpointKey, err := c.resolvePeer(ctx, client.API())
		if err != nil {
			return err
		}
		lastID, found, err := c.store.GetChatAnalyzerCheckpoint(ctx, checkpointKey)
		if err != nil {
			return err
		}
		if !found {
			latest, err := c.fetchMessages(ctx, client.API(), peer, 0, 1)
			if err != nil {
				return err
			}
			if maxID := maxTelegramMessageID(latest); maxID > 0 {
				result.CheckpointMessageIDs = map[string]int64{checkpointKey: maxID}
			}
			return nil
		}
		result, err = collectForwardMessages(
			ctx,
			checkpointKey,
			lastID,
			c.now().UTC(),
			c.maxMessageAge,
			c.pageSize,
			maxStaleCatchUpPagesPerCollect,
			staleCatchUpPageDelay,
			func(ctx context.Context, minID, limit int) ([]*tg.Message, error) {
				return c.fetchMessages(ctx, client.API(), peer, minID, limit)
			},
		)
		return err
	})
	if err != nil {
		// Discard partial catch-up progress on any request failure. The service
		// persists checkpoints only after Collect returns successfully, so the
		// next poll will safely retry the first uncommitted page.
		return CollectionResult{}, err
	}
	return result, nil
}

type telegramHistoryFetcher func(context.Context, int, int) ([]*tg.Message, error)

func collectForwardMessages(
	ctx context.Context,
	checkpointKey string,
	lastID int64,
	now time.Time,
	maxMessageAge time.Duration,
	limit int,
	maxPages int,
	pageDelay time.Duration,
	fetch telegramHistoryFetcher,
) (CollectionResult, error) {
	result := CollectionResult{
		Messages:             make([]model.ChatAnalyzerMessage, 0),
		CheckpointMessageIDs: make(map[string]int64, 1),
	}
	if fetch == nil {
		return CollectionResult{}, fmt.Errorf("telegram history fetcher is not configured")
	}
	if maxPages <= 0 {
		maxPages = 1
	}
	pageLimit := boundedTelegramHistoryLimit(limit)
	cursor := lastID
	for page := 0; page < maxPages; page++ {
		messages, err := fetch(ctx, int(cursor), pageLimit)
		if err != nil {
			return CollectionResult{}, err
		}
		pageResult := filterCollectedMessages(checkpointKey, messages, cursor, now, maxMessageAge)
		result.Messages = append(result.Messages, pageResult.Messages...)
		result.SkippedStale += pageResult.SkippedStale

		nextID := pageResult.CheckpointMessageIDs[checkpointKey]
		if nextID > cursor {
			result.CheckpointMessageIDs[checkpointKey] = nextID
		}

		// Catch up immediately only through pages made entirely of stale
		// history. Once current content appears, let the normal service path
		// enqueue it and commit the checkpoint before another request.
		if len(pageResult.Messages) > 0 || containsFreshTelegramMessage(messages, cursor, now, maxMessageAge) {
			break
		}
		// A short page has reached the available edge. A non-advancing page is
		// also terminal so an unexpected Telegram response cannot spin.
		if len(messages) < pageLimit || nextID <= cursor {
			break
		}
		cursor = nextID
		if page+1 < maxPages && pageDelay > 0 {
			timer := time.NewTimer(pageDelay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return CollectionResult{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return result, nil
}

func containsFreshTelegramMessage(messages []*tg.Message, lastID int64, now time.Time, maxMessageAge time.Duration) bool {
	cutoff := now.Add(-maxMessageAge)
	for _, msg := range messages {
		if msg == nil || int64(msg.ID) <= lastID || strings.TrimSpace(msg.Message) == "" {
			continue
		}
		messageDate := time.Unix(int64(msg.Date), 0).UTC()
		if msg.Date <= 0 {
			messageDate = now
		}
		if !messageDate.Before(cutoff) {
			return true
		}
	}
	return false
}

func filterCollectedMessages(checkpointKey string, messages []*tg.Message, lastID int64, now time.Time, maxMessageAge time.Duration) CollectionResult {
	result := CollectionResult{
		Messages:             make([]model.ChatAnalyzerMessage, 0, len(messages)),
		CheckpointMessageIDs: make(map[string]int64, 1),
	}
	cutoff := now.Add(-maxMessageAge)
	maxSeenID := lastID
	for _, msg := range messages {
		if msg == nil || int64(msg.ID) <= lastID {
			continue
		}
		if int64(msg.ID) > maxSeenID {
			maxSeenID = int64(msg.ID)
		}
		if strings.TrimSpace(msg.Message) == "" {
			continue
		}
		item := telegramMessageToAnalyzerMessage(checkpointKey, msg, now)
		if item.MessageDate.Before(cutoff) {
			result.SkippedStale++
			continue
		}
		result.Messages = append(result.Messages, item)
	}
	if maxSeenID > lastID {
		result.CheckpointMessageIDs[checkpointKey] = maxSeenID
	}
	sort.SliceStable(result.Messages, func(i, j int) bool {
		return result.Messages[i].MessageID < result.Messages[j].MessageID
	})
	return result
}

func (c *MTProtoCollector) fetchMessages(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, minID, limit int) ([]*tg.Message, error) {
	result, err := api.MessagesGetHistory(ctx, telegramHistoryRequest(peer, minID, limit))
	if err != nil {
		return nil, err
	}
	return messagesFromHistory(result), nil
}

func telegramHistoryRequest(peer tg.InputPeerClass, minID, limit int) *tg.MessagesGetHistoryRequest {
	// Telegram history limits are normally capped at 100. When a checkpoint is
	// present, a negative add_offset asks for the first page newer than that ID,
	// rather than the newest page in the chat. Advancing the checkpoint from
	// this bounded forward page prevents bursts larger than one page from being
	// silently skipped.
	limit = boundedTelegramHistoryLimit(limit)
	request := &tg.MessagesGetHistoryRequest{Peer: peer, Limit: limit}
	if minID > 0 {
		request.OffsetID = minID
		request.AddOffset = -limit
		request.MinID = minID
	}
	return request
}

func boundedTelegramHistoryLimit(limit int) int {
	if limit <= 0 {
		return 25
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func messagesFromHistory(result tg.MessagesMessagesClass) []*tg.Message {
	var raw []tg.MessageClass
	switch value := result.(type) {
	case *tg.MessagesMessages:
		raw = value.Messages
	case *tg.MessagesMessagesSlice:
		raw = value.Messages
	case *tg.MessagesChannelMessages:
		raw = value.Messages
	default:
		return nil
	}
	out := make([]*tg.Message, 0, len(raw))
	for _, item := range raw {
		if msg, ok := item.(*tg.Message); ok {
			out = append(out, msg)
			continue
		}
		// Service and empty constructors still consume a Telegram message ID.
		// Preserve that ID with a textless placeholder so the forward checkpoint
		// cannot become stuck behind a page containing only non-text events.
		if withID, ok := item.(interface{ GetID() int }); ok && withID.GetID() > 0 {
			out = append(out, &tg.Message{ID: withID.GetID()})
		}
	}
	return out
}

func maxTelegramMessageID(messages []*tg.Message) int64 {
	var maxID int64
	for _, msg := range messages {
		if msg != nil && int64(msg.ID) > maxID {
			maxID = int64(msg.ID)
		}
	}
	return maxID
}

func telegramMessageToAnalyzerMessage(chatID string, msg *tg.Message, receivedAt time.Time) model.ChatAnalyzerMessage {
	senderID := peerTelegramUserID(msg.FromID)
	replyToID := int64(0)
	if reply, ok := msg.ReplyTo.(*tg.MessageReplyHeader); ok {
		replyToID = int64(reply.ReplyToMsgID)
	}
	messageDate := time.Unix(int64(msg.Date), 0).UTC()
	if msg.Date <= 0 {
		messageDate = receivedAt
	}
	return model.ChatAnalyzerMessage{
		ID:               fmt.Sprintf("%s:%d", chatID, msg.ID),
		ChatID:           chatID,
		MessageID:        int64(msg.ID),
		SenderID:         senderID,
		SenderStableID:   model.ChatAnalyzerStableID(senderID),
		SenderNickname:   model.ChatAnalyzerReporterNickname(senderID),
		Text:             msg.Message,
		MessageDate:      messageDate,
		ReceivedAt:       receivedAt,
		ReplyToMessageID: replyToID,
		Status:           model.ChatAnalyzerMessagePending,
	}
}

func peerTelegramUserID(peer tg.PeerClass) int64 {
	switch value := peer.(type) {
	case *tg.PeerUser:
		return value.UserID
	default:
		return 0
	}
}

func (c *MTProtoCollector) resolvePeer(ctx context.Context, api *tg.Client) (tg.InputPeerClass, string, error) {
	raw := strings.TrimSpace(c.chatID)
	lower := strings.ToLower(raw)
	switch {
	case strings.HasPrefix(lower, "chat:"):
		id, err := strconv.ParseInt(strings.TrimSpace(raw[len("chat:"):]), 10, 64)
		if err != nil || id == 0 {
			return nil, "", fmt.Errorf("invalid Telegram chat descriptor")
		}
		return &tg.InputPeerChat{ChatID: absInt64(id)}, "chat:" + strconv.FormatInt(absInt64(id), 10), nil
	case strings.HasPrefix(lower, "channel:"):
		parts := strings.Split(raw, ":")
		if len(parts) != 3 {
			return nil, "", fmt.Errorf("channel descriptor must be channel:<id>:<accessHash>")
		}
		id, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || id == 0 {
			return nil, "", fmt.Errorf("invalid Telegram channel id")
		}
		hash, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil || hash == 0 {
			return nil, "", fmt.Errorf("invalid Telegram channel access hash")
		}
		id = absInt64(id)
		return &tg.InputPeerChannel{ChannelID: id, AccessHash: hash}, "channel:" + strconv.FormatInt(id, 10), nil
	case strings.HasPrefix(raw, "@") || strings.Contains(lower, "t.me/"):
		return resolveUsernamePeer(ctx, api, raw)
	default:
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id == 0 {
			return nil, "", fmt.Errorf("unsupported Telegram chat descriptor")
		}
		if id < -1000000000000 {
			return nil, "", fmt.Errorf("telegram supergroup/channel numeric ids need channel:<id>:<accessHash> from the session setup command")
		}
		id = absInt64(id)
		return &tg.InputPeerChat{ChatID: id}, "chat:" + strconv.FormatInt(id, 10), nil
	}
}

func resolveUsernamePeer(ctx context.Context, api *tg.Client, raw string) (tg.InputPeerClass, string, error) {
	username := strings.TrimSpace(raw)
	username = strings.TrimPrefix(username, "https://t.me/")
	username = strings.TrimPrefix(username, "http://t.me/")
	username = strings.TrimPrefix(username, "t.me/")
	username = strings.TrimPrefix(username, "@")
	username = strings.Trim(username, "/")
	if username == "" {
		return nil, "", fmt.Errorf("telegram username is empty")
	}
	resolved, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{
		Username: username,
	})
	if err != nil {
		return nil, "", err
	}
	switch peer := resolved.Peer.(type) {
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: peer.ChatID}, "chat:" + strconv.FormatInt(peer.ChatID, 10), nil
	case *tg.PeerChannel:
		for _, chat := range resolved.Chats {
			channel, ok := chat.(*tg.Channel)
			if !ok || channel.ID != peer.ChannelID {
				continue
			}
			return &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}, "channel:" + strconv.FormatInt(channel.ID, 10), nil
		}
		return nil, "", fmt.Errorf("resolved Telegram channel without an access hash")
	default:
		return nil, "", fmt.Errorf("Telegram descriptor did not resolve to a group or channel")
	}
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
