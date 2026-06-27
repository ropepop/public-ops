package acquisition

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

type MTProtoAdminConfig struct {
	APIID        int
	APIHash      string
	SessionFile  string
	Username     string
	HistoryLimit int
}

type MTProtoAdminGateway struct {
	cfg           MTProtoAdminConfig
	outreach      *MTProtoOutreach
	store         *Store
	cursorName    string
	bootstrapped  bool
	lastPollStats AdminPollStats
}

func NewMTProtoAdminGateway(cfg MTProtoAdminConfig, store *Store) (*MTProtoAdminGateway, error) {
	cfg.Username = cleanUsername(cfg.Username)
	if cfg.Username == "" {
		return nil, fmt.Errorf("admin username is required")
	}
	if store == nil {
		return nil, fmt.Errorf("campaign store is required")
	}
	if cfg.HistoryLimit <= 0 {
		cfg.HistoryLimit = 50
	}
	return &MTProtoAdminGateway{
		cfg:        cfg,
		outreach:   NewMTProtoOutreach(MTProtoOutreachConfig{APIID: cfg.APIID, APIHash: cfg.APIHash, SessionFile: cfg.SessionFile}),
		store:      store,
		cursorName: "mtproto:" + cfg.Username,
	}, nil
}

func (g *MTProtoAdminGateway) Bootstrap(ctx context.Context) error {
	if g.bootstrapped {
		return nil
	}
	cursor, err := g.store.AdminMessageCursor(ctx, g.cursorName)
	if err != nil {
		return err
	}
	if cursor > 0 {
		g.bootstrapped = true
		return nil
	}
	maxID := int64(0)
	if err := g.outreach.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		peer, _, err := g.adminPeer(ctx, client.API())
		if err != nil {
			return err
		}
		history, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: g.cfg.HistoryLimit,
		})
		if err != nil {
			return err
		}
		for _, message := range messagesFromHistory(history) {
			if message != nil && int64(message.ID) > maxID {
				maxID = int64(message.ID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := g.store.SetAdminMessageCursor(ctx, g.cursorName, maxID); err != nil {
		return err
	}
	g.bootstrapped = true
	return nil
}

func (g *MTProtoAdminGateway) SendDraftApproval(ctx context.Context, draft ApprovalDraft) error {
	return g.sendAdmin(ctx, FormatDraftApprovalMessage(draft))
}

func (g *MTProtoAdminGateway) SendAlert(ctx context.Context, text string) error {
	return g.sendAdmin(ctx, text)
}

func (g *MTProtoAdminGateway) PollDecisions(ctx context.Context) ([]AdminDecision, error) {
	if err := g.Bootstrap(ctx); err != nil {
		return nil, err
	}
	cursor, err := g.store.AdminMessageCursor(ctx, g.cursorName)
	if err != nil {
		return nil, err
	}
	decisions := []adminDecisionWithMessageID{}
	maxAdminMessageID := cursor
	stats := AdminPollStats{CursorBefore: cursor, CursorAfter: cursor}
	err = g.outreach.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		peer, _, err := g.adminPeer(ctx, client.API())
		if err != nil {
			return err
		}
		history, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: g.cfg.HistoryLimit,
		})
		if err != nil {
			return err
		}
		decisions, maxAdminMessageID, stats = adminDecisionsFromMessages(messagesFromHistory(history), cursor)
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = maxAdminMessageID
	g.lastPollStats = stats
	out := make([]AdminDecision, 0, len(decisions))
	for _, decision := range decisions {
		out = append(out, decision.decision)
	}
	return out, nil
}

func (g *MTProtoAdminGateway) AckAdminCursor(ctx context.Context, messageID int64) error {
	return g.store.SetAdminMessageCursor(ctx, g.cursorName, messageID)
}

func (g *MTProtoAdminGateway) LastPollStats() AdminPollStats {
	return g.lastPollStats
}

func adminDecisionsFromMessages(messages []*tg.Message, cursor int64) ([]adminDecisionWithMessageID, int64, AdminPollStats) {
	decisions := []adminDecisionWithMessageID{}
	maxMessageID := cursor
	stats := AdminPollStats{CursorBefore: cursor, CursorAfter: cursor}
	for _, message := range messages {
		if message == nil || int64(message.ID) <= cursor {
			continue
		}
		stats.MessagesScanned++
		messageID := int64(message.ID)
		if messageID > maxMessageID {
			maxMessageID = messageID
		}
		if decision, ok := adminDecisionFromMessage(message); ok {
			decision.MessageID = messageID
			decisions = append(decisions, adminDecisionWithMessageID{messageID: messageID, decision: decision})
			stats.DecisionsParsed++
		}
	}
	stats.CursorAfter = maxMessageID
	sort.SliceStable(decisions, func(i, j int) bool {
		return decisions[i].messageID < decisions[j].messageID
	})
	return decisions, maxMessageID, stats
}

func adminDecisionFromMessage(message *tg.Message) (AdminDecision, bool) {
	if message == nil {
		return AdminDecision{}, false
	}
	if decision, ok := ParseAdminDecision(message.Message); ok {
		return decision, true
	}
	if message.Out {
		return AdminDecision{}, false
	}
	return parseForwardedDraftApproval(message.Message)
}

func parseForwardedDraftApproval(text string) (AdminDecision, bool) {
	if !strings.Contains(text, "RS acquisition draft") {
		return AdminDecision{}, false
	}
	var decision AdminDecision
	found := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "approve:") {
			continue
		}
		parsed, ok := ParseAdminDecision(strings.TrimSpace(line[len("Approve:"):]))
		if !ok || parsed.Action != AdminApprove {
			return AdminDecision{}, false
		}
		decision = parsed
		found++
	}
	if found != 1 {
		return AdminDecision{}, false
	}
	return decision, true
}

func (g *MTProtoAdminGateway) sendAdmin(ctx context.Context, text string) error {
	return g.outreach.run(ctx, func(ctx context.Context, client *telegram.Client) error {
		peer, _, err := g.adminPeer(ctx, client.API())
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

func (g *MTProtoAdminGateway) adminPeer(ctx context.Context, api *tg.Client) (tg.InputPeerClass, int64, error) {
	user, err := resolveUserByUsername(ctx, api, g.cfg.Username)
	if err != nil {
		return nil, 0, err
	}
	return &tg.InputPeerUser{UserID: user.ID, AccessHash: user.AccessHash}, user.ID, nil
}

type adminDecisionWithMessageID struct {
	messageID int64
	decision  AdminDecision
}
