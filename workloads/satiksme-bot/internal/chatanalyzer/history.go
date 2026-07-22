package chatanalyzer

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"satiksmebot/internal/model"
)

type HistoryFetchConfig struct {
	APIID       int
	APIHash     string
	SessionFile string
	ChatID      string
	Limit       int
	PageSize    int
	PageDelay   time.Duration
	Since       time.Time
	Until       time.Time
	Now         func() time.Time
}

type HistoryFetchStats struct {
	CheckpointKey string        `json:"checkpointKey"`
	Requested     int           `json:"requested"`
	Collected     int           `json:"collected"`
	Pages         int           `json:"pages"`
	RawMessages   int           `json:"rawMessages"`
	EarliestID    int64         `json:"earliestId"`
	LatestID      int64         `json:"latestId"`
	EarliestAt    time.Time     `json:"earliestAt,omitempty"`
	LatestAt      time.Time     `json:"latestAt,omitempty"`
	Duration      time.Duration `json:"duration"`
}

func FetchHistory(ctx context.Context, cfg HistoryFetchConfig) ([]model.ChatAnalyzerMessage, HistoryFetchStats, error) {
	started := time.Now()
	stats := HistoryFetchStats{Requested: cfg.Limit}
	if cfg.APIID <= 0 || cfg.APIHash == "" || cfg.SessionFile == "" || cfg.ChatID == "" {
		return nil, stats, fmt.Errorf("telegram history fetch is not configured")
	}
	limit := cfg.Limit
	if limit <= 0 {
		limit = 100
	}
	pageSize := cfg.PageSize
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{
		SessionStorage: NewAtomicSessionStorage(cfg.SessionFile),
		NoUpdates:      true,
	})
	collected := make([]model.ChatAnalyzerMessage, 0, limit)
	seen := make(map[int64]struct{}, limit)
	err := client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status == nil || !status.Authorized {
			return fmt.Errorf("%w; run chat-analyzer-session first", ErrMTProtoSessionUnauthorized)
		}
		resolver := &MTProtoCollector{chatID: cfg.ChatID}
		peer, checkpointKey, err := resolver.resolvePeer(ctx, client.API())
		if err != nil {
			return err
		}
		stats.CheckpointKey = checkpointKey
		offsetID := 0
		for len(collected) < limit {
			page, err := fetchHistoryPage(ctx, client.API(), peer, offsetID, pageSize)
			if err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			stats.Pages++
			stats.RawMessages += len(page)
			minID := int64(0)
			reachedSince := false
			for _, msg := range page {
				if msg == nil {
					continue
				}
				id := int64(msg.ID)
				if minID == 0 || id < minID {
					minID = id
				}
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				if msg.Message == "" {
					continue
				}
				item := telegramMessageToAnalyzerMessage(checkpointKey, msg, now())
				if !cfg.Until.IsZero() && item.MessageDate.After(cfg.Until) {
					continue
				}
				if !cfg.Since.IsZero() && item.MessageDate.Before(cfg.Since) {
					reachedSince = true
					continue
				}
				collected = append(collected, item)
				updateHistoryBounds(&stats, item)
				if len(collected) >= limit {
					break
				}
			}
			if minID <= 1 || reachedSince {
				break
			}
			offsetID = int(minID)
			if cfg.PageDelay > 0 && len(collected) < limit {
				timer := time.NewTimer(cfg.PageDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-timer.C:
				}
			}
		}
		return nil
	})
	stats.Collected = len(collected)
	stats.Duration = time.Since(started)
	sort.SliceStable(collected, func(i, j int) bool {
		return collected[i].MessageID < collected[j].MessageID
	})
	return collected, stats, err
}

func fetchHistoryPage(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, offsetID, limit int) ([]*tg.Message, error) {
	result, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		OffsetID: offsetID,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	return messagesFromHistory(result), nil
}

func updateHistoryBounds(stats *HistoryFetchStats, item model.ChatAnalyzerMessage) {
	if stats.EarliestID == 0 || item.MessageID < stats.EarliestID {
		stats.EarliestID = item.MessageID
		stats.EarliestAt = item.MessageDate
	}
	if item.MessageID > stats.LatestID {
		stats.LatestID = item.MessageID
		stats.LatestAt = item.MessageDate
	}
}
