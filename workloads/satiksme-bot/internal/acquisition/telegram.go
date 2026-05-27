package acquisition

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type TelegramConfig struct {
	APIID       int
	APIHash     string
	SessionFile string
	ChatID      string
	Limit       int
	PageSize    int
	PageDelay   time.Duration
}

type TelegramCandidateCollector struct {
	RecentConfig   TelegramConfig
	MemberConfig   TelegramConfig
	IncludeMembers bool
}

func (c TelegramCandidateCollector) CollectCandidates(ctx context.Context) ([]Candidate, error) {
	recent, err := CollectRecentActive(ctx, c.RecentConfig)
	if err != nil {
		return nil, err
	}
	if !c.IncludeMembers {
		return recent, nil
	}
	members, err := CollectMembers(ctx, c.MemberConfig)
	if err != nil {
		return nil, err
	}
	merged := make([]Candidate, 0, len(recent)+len(members))
	seen := map[int64]struct{}{}
	for _, candidate := range recent {
		seen[candidate.UserID] = struct{}{}
		merged = append(merged, candidate)
	}
	for _, candidate := range members {
		if _, ok := seen[candidate.UserID]; ok {
			continue
		}
		merged = append(merged, candidate)
	}
	sortCandidates(merged)
	return merged, nil
}

func CollectRecentActive(ctx context.Context, cfg TelegramConfig) ([]Candidate, error) {
	if err := validateTelegramConfig(cfg); err != nil {
		return nil, err
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 100
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	out := []Candidate{}
	seen := map[int64]Candidate{}
	client := telegram.NewClient(cfg.APIID, strings.TrimSpace(cfg.APIHash), telegram.Options{
		SessionStorage: &session.FileStorage{Path: filepath.Clean(cfg.SessionFile)},
		NoUpdates:      true,
	})
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status == nil || !status.Authorized {
			return fmt.Errorf("telegram account session is not authorized")
		}
		peer, _, err := resolveCampaignPeer(ctx, client.API(), cfg.ChatID)
		if err != nil {
			return err
		}
		offsetID := 0
		for len(seen) < limit {
			result, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
				Peer:     peer,
				OffsetID: offsetID,
				Limit:    pageSize,
			})
			if err != nil {
				return err
			}
			candidates := CandidatesFromHistory(result, SourceRecentActive)
			minID := 0
			for _, message := range messagesFromHistory(result) {
				if message != nil && (minID == 0 || message.ID < minID) {
					minID = message.ID
				}
			}
			for _, candidate := range candidates {
				if existing, ok := seen[candidate.UserID]; !ok || candidate.LastActiveAt.After(existing.LastActiveAt) {
					seen[candidate.UserID] = candidate
				}
				if len(seen) >= limit {
					break
				}
			}
			if minID <= 1 {
				break
			}
			offsetID = minID
			if cfg.PageDelay > 0 && len(seen) < limit {
				timer := time.NewTimer(cfg.PageDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, candidate := range seen {
		out = append(out, candidate)
	}
	sortCandidates(out)
	return out, nil
}

func CollectMembers(ctx context.Context, cfg TelegramConfig) ([]Candidate, error) {
	if err := validateTelegramConfig(cfg); err != nil {
		return nil, err
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 100
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 100
	}
	now := time.Now().UTC()
	out := []Candidate{}
	client := telegram.NewClient(cfg.APIID, strings.TrimSpace(cfg.APIHash), telegram.Options{
		SessionStorage: &session.FileStorage{Path: filepath.Clean(cfg.SessionFile)},
		NoUpdates:      true,
	})
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status == nil || !status.Authorized {
			return fmt.Errorf("telegram account session is not authorized")
		}
		_, channel, err := resolveCampaignPeer(ctx, client.API(), cfg.ChatID)
		if err != nil {
			return err
		}
		if channel == nil {
			return fmt.Errorf("member-list collection requires a Telegram channel or supergroup descriptor")
		}
		offset := 0
		seen := map[int64]struct{}{}
		for len(out) < limit {
			result, err := client.API().ChannelsGetParticipants(ctx, &tg.ChannelsGetParticipantsRequest{
				Channel: channel,
				Filter:  &tg.ChannelParticipantsRecent{},
				Offset:  offset,
				Limit:   pageSize,
			})
			if err != nil {
				return err
			}
			page, ok := result.(*tg.ChannelsChannelParticipants)
			if !ok || len(page.Users) == 0 {
				break
			}
			for _, candidate := range CandidatesFromUsers(page.Users, SourceMemberList, now) {
				if _, ok := seen[candidate.UserID]; ok {
					continue
				}
				seen[candidate.UserID] = struct{}{}
				out = append(out, candidate)
				if len(out) >= limit {
					break
				}
			}
			offset += len(page.Participants)
			if len(page.Participants) == 0 || offset >= page.Count {
				break
			}
			if cfg.PageDelay > 0 && len(out) < limit {
				timer := time.NewTimer(cfg.PageDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortCandidates(out)
	return out, nil
}

func CandidatesFromHistory(result tg.MessagesMessagesClass, source CandidateSource) []Candidate {
	users := usersFromHistory(result)
	outByID := map[int64]Candidate{}
	for _, message := range messagesFromHistory(result) {
		if message == nil || strings.TrimSpace(message.Message) == "" {
			continue
		}
		userID := peerUserID(message.FromID)
		if userID <= 0 {
			continue
		}
		user, ok := users[userID]
		if !ok || skipTelegramUser(user) {
			continue
		}
		messageAt := time.Unix(int64(message.Date), 0).UTC()
		candidate := candidateFromUser(user, source, messageAt)
		candidate.LastMessageID = int64(message.ID)
		candidate.Language = InferLanguage(message.Message)
		if existing, ok := outByID[candidate.UserID]; ok && !candidate.LastActiveAt.After(existing.LastActiveAt) {
			continue
		}
		outByID[candidate.UserID] = candidate
	}
	out := make([]Candidate, 0, len(outByID))
	for _, candidate := range outByID {
		out = append(out, candidate)
	}
	sortCandidates(out)
	return out
}

func CandidatesFromUsers(users []tg.UserClass, source CandidateSource, seenAt time.Time) []Candidate {
	out := make([]Candidate, 0, len(users))
	seen := map[int64]struct{}{}
	for _, raw := range users {
		user, ok := raw.(*tg.User)
		if !ok || skipTelegramUser(user) {
			continue
		}
		if _, ok := seen[user.ID]; ok {
			continue
		}
		seen[user.ID] = struct{}{}
		out = append(out, candidateFromUser(user, source, seenAt))
	}
	sortCandidates(out)
	return out
}

func candidateFromUser(user *tg.User, source CandidateSource, seenAt time.Time) Candidate {
	displayName := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(user.FirstName), strings.TrimSpace(user.LastName)}, " "))
	return normalizeCandidate(Candidate{
		UserID:       user.ID,
		AccessHash:   user.AccessHash,
		Username:     cleanUsername(user.Username),
		DisplayName:  displayName,
		Language:     "lv",
		Source:       source,
		LastActiveAt: seenAt.UTC(),
	})
}

func skipTelegramUser(user *tg.User) bool {
	return user == nil || user.ID <= 0 || user.Bot || user.Deleted || user.Self || user.Fake || user.Scam
}

func usersFromHistory(result tg.MessagesMessagesClass) map[int64]*tg.User {
	rawUsers := []tg.UserClass{}
	switch value := result.(type) {
	case *tg.MessagesMessages:
		rawUsers = value.Users
	case *tg.MessagesMessagesSlice:
		rawUsers = value.Users
	case *tg.MessagesChannelMessages:
		rawUsers = value.Users
	}
	users := make(map[int64]*tg.User, len(rawUsers))
	for _, raw := range rawUsers {
		if user, ok := raw.(*tg.User); ok {
			users[user.ID] = user
		}
	}
	return users
}

func messagesFromHistory(result tg.MessagesMessagesClass) []*tg.Message {
	rawMessages := []tg.MessageClass{}
	switch value := result.(type) {
	case *tg.MessagesMessages:
		rawMessages = value.Messages
	case *tg.MessagesMessagesSlice:
		rawMessages = value.Messages
	case *tg.MessagesChannelMessages:
		rawMessages = value.Messages
	}
	out := make([]*tg.Message, 0, len(rawMessages))
	for _, raw := range rawMessages {
		if message, ok := raw.(*tg.Message); ok {
			out = append(out, message)
		}
	}
	return out
}

func peerUserID(peer tg.PeerClass) int64 {
	switch value := peer.(type) {
	case *tg.PeerUser:
		return value.UserID
	default:
		return 0
	}
}

func sortCandidates(candidates []Candidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if sourceRank(left.Source) != sourceRank(right.Source) {
			return sourceRank(left.Source) < sourceRank(right.Source)
		}
		if !left.LastActiveAt.Equal(right.LastActiveAt) {
			return left.LastActiveAt.After(right.LastActiveAt)
		}
		if left.LastMessageID != right.LastMessageID {
			return left.LastMessageID > right.LastMessageID
		}
		return candidateKey(left) < candidateKey(right)
	})
}

func validateTelegramConfig(cfg TelegramConfig) error {
	if cfg.APIID <= 0 {
		return fmt.Errorf("telegram API ID is required")
	}
	if strings.TrimSpace(cfg.APIHash) == "" {
		return fmt.Errorf("telegram API hash is required")
	}
	if strings.TrimSpace(cfg.SessionFile) == "" {
		return fmt.Errorf("telegram session file is required")
	}
	if strings.TrimSpace(cfg.ChatID) == "" {
		return fmt.Errorf("telegram chat descriptor is required")
	}
	return nil
}

func resolveCampaignPeer(ctx context.Context, api *tg.Client, raw string) (tg.InputPeerClass, tg.InputChannelClass, error) {
	descriptor := strings.TrimSpace(raw)
	lower := strings.ToLower(descriptor)
	switch {
	case strings.HasPrefix(lower, "chat:"):
		id, err := strconv.ParseInt(strings.TrimSpace(descriptor[len("chat:"):]), 10, 64)
		if err != nil || id == 0 {
			return nil, nil, fmt.Errorf("invalid telegram chat descriptor %q", raw)
		}
		return &tg.InputPeerChat{ChatID: absInt64(id)}, nil, nil
	case strings.HasPrefix(lower, "channel:"):
		parts := strings.Split(descriptor, ":")
		if len(parts) != 3 {
			return nil, nil, fmt.Errorf("channel descriptor must be channel:<id>:<accessHash>")
		}
		id, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || id == 0 {
			return nil, nil, fmt.Errorf("invalid channel id in %q", raw)
		}
		hash, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil || hash == 0 {
			return nil, nil, fmt.Errorf("invalid channel access hash in %q", raw)
		}
		channel := &tg.InputChannel{ChannelID: absInt64(id), AccessHash: hash}
		return &tg.InputPeerChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash}, channel, nil
	case strings.HasPrefix(descriptor, "@") || strings.Contains(lower, "t.me/"):
		username := telegramUsername(descriptor)
		resolved, err := api.ContactsResolveUsername(ctx, username)
		if err != nil {
			return nil, nil, err
		}
		switch peer := resolved.Peer.(type) {
		case *tg.PeerChat:
			return &tg.InputPeerChat{ChatID: peer.ChatID}, nil, nil
		case *tg.PeerChannel:
			for _, chat := range resolved.Chats {
				channel, ok := chat.(*tg.Channel)
				if !ok || channel.ID != peer.ChannelID {
					continue
				}
				inputChannel := &tg.InputChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
				return &tg.InputPeerChannel{ChannelID: inputChannel.ChannelID, AccessHash: inputChannel.AccessHash}, inputChannel, nil
			}
			return nil, nil, fmt.Errorf("resolved channel %q without access hash", username)
		default:
			return nil, nil, fmt.Errorf("telegram descriptor %q did not resolve to a group or channel", raw)
		}
	default:
		id, err := strconv.ParseInt(descriptor, 10, 64)
		if err != nil || id == 0 {
			return nil, nil, fmt.Errorf("unsupported telegram chat descriptor %q", raw)
		}
		if id < -1000000000000 {
			return nil, nil, fmt.Errorf("telegram supergroup/channel numeric ids need channel:<id>:<accessHash>")
		}
		return &tg.InputPeerChat{ChatID: absInt64(id)}, nil, nil
	}
}

func telegramUsername(raw string) string {
	username := strings.TrimSpace(raw)
	username = strings.TrimPrefix(username, "https://t.me/")
	username = strings.TrimPrefix(username, "http://t.me/")
	username = strings.TrimPrefix(username, "t.me/")
	username = strings.TrimPrefix(username, "@")
	return strings.Trim(username, "/")
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
