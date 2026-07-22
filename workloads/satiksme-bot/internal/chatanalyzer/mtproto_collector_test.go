package chatanalyzer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"satiksmebot/internal/model"
)

func TestTelegramMessageToAnalyzerMessageUsesTelegramUserIdentity(t *testing.T) {
	receivedAt := time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC)
	item := telegramMessageToAnalyzerMessage("channel:42", &tg.Message{
		ID:      10,
		Date:    int(receivedAt.Unix()),
		FromID:  &tg.PeerUser{UserID: 777001},
		PeerID:  &tg.PeerChannel{ChannelID: 42},
		Message: "kontrole",
	}, receivedAt)

	if item.SenderID != 777001 {
		t.Fatalf("sender id = %d, want Telegram user id 777001", item.SenderID)
	}
	if got, want := item.SenderStableID, model.TelegramStableID(777001); got != want {
		t.Fatalf("sender stable id = %q, want %q", got, want)
	}
	if got, want := item.SenderNickname, model.GenericNickname(777001); got != want {
		t.Fatalf("sender nickname = %q, want %q", got, want)
	}
}

func TestTelegramMessageToAnalyzerMessageDoesNotUseGroupAsReporter(t *testing.T) {
	receivedAt := time.Date(2026, 4, 27, 8, 0, 0, 0, time.UTC)
	item := telegramMessageToAnalyzerMessage("channel:42", &tg.Message{
		ID:      11,
		Date:    int(receivedAt.Unix()),
		FromID:  &tg.PeerChannel{ChannelID: 42},
		PeerID:  &tg.PeerChannel{ChannelID: 42},
		Message: "kontrole",
	}, receivedAt)

	if item.SenderID != 0 {
		t.Fatalf("sender id = %d, want 0 for non-user sender", item.SenderID)
	}
	if _, ok := model.ChatAnalyzerReporterUserID(item.SenderID); ok {
		t.Fatalf("non-user sender unexpectedly resolved to a reporter user id")
	}
}

func TestFilterCollectedMessagesSkipsStaleBeforeEnqueueAndAdvancesCursor(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	result := filterCollectedMessages("channel:42", []*tg.Message{
		{ID: 104, Date: int(now.Add(-25 * time.Hour).Unix()), Message: "old report"},
		{ID: 103, Date: int(now.Add(-time.Hour).Unix()), Message: "fresh report"},
		{ID: 105, Date: int(now.Add(-time.Minute).Unix()), Message: "   "},
		{ID: 100, Date: int(now.Add(-time.Minute).Unix()), Message: "already seen"},
	}, 100, now, 24*time.Hour)

	if len(result.Messages) != 1 || result.Messages[0].MessageID != 103 {
		t.Fatalf("fresh messages = %+v, want only message 103", result.Messages)
	}
	if result.SkippedStale != 1 {
		t.Fatalf("skipped stale = %d, want 1", result.SkippedStale)
	}
	if got := result.CheckpointMessageIDs["channel:42"]; got != 105 {
		t.Fatalf("checkpoint = %d, want highest observed message 105", got)
	}
}

func TestTelegramHistoryRequestFetchesOldestForwardPageAfterCheckpoint(t *testing.T) {
	peer := &tg.InputPeerChannel{ChannelID: 42, AccessHash: 99}
	request := telegramHistoryRequest(peer, 9000, 250)
	if request.Peer != peer {
		t.Fatal("history request changed peer")
	}
	if request.OffsetID != 9000 || request.MinID != 9000 {
		t.Fatalf("history checkpoint fields = offset %d min %d, want 9000", request.OffsetID, request.MinID)
	}
	if request.Limit != 100 || request.AddOffset != -100 {
		t.Fatalf("history page = limit %d addOffset %d, want bounded forward page 100/-100", request.Limit, request.AddOffset)
	}

	latest := telegramHistoryRequest(peer, 0, 1)
	if latest.OffsetID != 0 || latest.MinID != 0 || latest.AddOffset != 0 || latest.Limit != 1 {
		t.Fatalf("latest-message request = %+v", latest)
	}
}

func TestMTProtoCollectorDefaultsToTwentyFiveMessageCollectionPages(t *testing.T) {
	collector := NewMTProtoCollector(MTProtoCollectorConfig{})
	if got, want := collector.pageSize, 25; got != want {
		t.Fatalf("collection page size = %d, want %d", got, want)
	}
}

func TestCollectForwardMessagesCatchesUpOneHundredStaleMessagesWithTwentyFiveMessagePages(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	calls := 0
	result, err := collectForwardMessages(
		context.Background(), "channel:42", 100, now, 24*time.Hour, 25, maxStaleCatchUpPagesPerCollect, 0,
		func(_ context.Context, minID, limit int) ([]*tg.Message, error) {
			if got, want := limit, 25; got != want {
				t.Fatalf("fetch limit = %d, want %d", got, want)
			}
			if got, want := minID, 100+calls*25; got != want {
				t.Fatalf("fetch %d min id = %d, want %d", calls+1, got, want)
			}
			page := staleTelegramPage(now, minID+1, 25)
			calls++
			return page, nil
		},
	)
	if err != nil {
		t.Fatalf("collect forward messages: %v", err)
	}
	if got, want := calls, maxStaleCatchUpPagesPerCollect; got != want {
		t.Fatalf("fetch calls = %d, want %d", got, want)
	}
	if got, want := result.SkippedStale, 100; got != want {
		t.Fatalf("skipped stale = %d, want %d", got, want)
	}
	if got, want := result.CheckpointMessageIDs["channel:42"], int64(200); got != want {
		t.Fatalf("checkpoint = %d, want %d", got, want)
	}
}

func TestMessagesFromHistoryPreservesNonTextEventIDsForCheckpointing(t *testing.T) {
	items := messagesFromHistory(&tg.MessagesMessages{Messages: []tg.MessageClass{
		&tg.MessageService{ID: 106},
		&tg.MessageEmpty{ID: 107},
		&tg.Message{ID: 108, Message: "fresh report"},
	}})
	if len(items) != 3 || items[0].ID != 106 || items[1].ID != 107 || items[2].ID != 108 {
		t.Fatalf("history ids = %+v, want service/empty/text ids 106,107,108", items)
	}
	result := filterCollectedMessages("channel:42", items, 105, time.Now().UTC(), 24*time.Hour)
	if got := result.CheckpointMessageIDs["channel:42"]; got != 108 {
		t.Fatalf("checkpoint = %d, want 108", got)
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != 108 {
		t.Fatalf("queued messages = %+v, want only text message 108", result.Messages)
	}
}

func TestTelegramMessageWithoutDateUsesCollectionTime(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	item := telegramMessageToAnalyzerMessage("channel:42", &tg.Message{ID: 12, Message: "fresh report"}, now)
	if !item.MessageDate.Equal(now) {
		t.Fatalf("message date = %s, want collection time %s", item.MessageDate, now)
	}
}

func TestCollectForwardMessagesAdvancesAcrossBoundedStalePages(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	pages := [][]*tg.Message{
		staleTelegramPage(now, 101, 3),
		staleTelegramPage(now, 104, 3),
		staleTelegramPage(now, 107, 3),
		staleTelegramPage(now, 110, 2),
	}
	calls := 0
	result, err := collectForwardMessages(
		context.Background(), "channel:42", 100, now, 24*time.Hour, 3, maxStaleCatchUpPagesPerCollect, 0,
		func(_ context.Context, minID, limit int) ([]*tg.Message, error) {
			if limit != 3 {
				t.Fatalf("fetch limit = %d, want 3", limit)
			}
			wantMinID := 100 + calls*3
			if minID != wantMinID {
				t.Fatalf("fetch %d min id = %d, want %d", calls+1, minID, wantMinID)
			}
			page := pages[calls]
			calls++
			return page, nil
		},
	)
	if err != nil {
		t.Fatalf("collect forward messages: %v", err)
	}
	if calls != 4 {
		t.Fatalf("fetch calls = %d, want bounded catch-up of 4", calls)
	}
	if len(result.Messages) != 0 {
		t.Fatalf("fresh messages = %+v, want none", result.Messages)
	}
	if result.SkippedStale != 11 {
		t.Fatalf("skipped stale = %d, want 11", result.SkippedStale)
	}
	if got := result.CheckpointMessageIDs["channel:42"]; got != 111 {
		t.Fatalf("checkpoint = %d, want 111", got)
	}
}

func TestCollectForwardMessagesStopsOnFreshPage(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	calls := 0
	result, err := collectForwardMessages(
		context.Background(), "channel:42", 100, now, 24*time.Hour, 2, maxStaleCatchUpPagesPerCollect, 0,
		func(_ context.Context, minID, _ int) ([]*tg.Message, error) {
			calls++
			switch calls {
			case 1:
				if minID != 100 {
					t.Fatalf("first min id = %d, want 100", minID)
				}
				return staleTelegramPage(now, 101, 2), nil
			case 2:
				if minID != 102 {
					t.Fatalf("second min id = %d, want 102", minID)
				}
				return []*tg.Message{
					{ID: 103, Date: int(now.Add(-23 * time.Hour).Unix()), Message: "fresh report"},
					{ID: 104, Date: int(now.Add(-25 * time.Hour).Unix()), Message: "old report"},
				}, nil
			default:
				t.Fatalf("unexpected fetch call %d", calls)
				return nil, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("collect forward messages: %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetch calls = %d, want stop after fresh page", calls)
	}
	if len(result.Messages) != 1 || result.Messages[0].MessageID != 103 {
		t.Fatalf("fresh messages = %+v, want only message 103", result.Messages)
	}
	if result.SkippedStale != 3 {
		t.Fatalf("skipped stale = %d, want 3", result.SkippedStale)
	}
	if got := result.CheckpointMessageIDs["channel:42"]; got != 104 {
		t.Fatalf("checkpoint = %d, want 104", got)
	}
}

func TestCollectForwardMessagesIgnoresTextlessServicePlaceholdersForFreshness(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	calls := 0
	result, err := collectForwardMessages(
		context.Background(), "channel:42", 100, now, 24*time.Hour, 2, maxStaleCatchUpPagesPerCollect, 0,
		func(_ context.Context, minID, _ int) ([]*tg.Message, error) {
			calls++
			switch calls {
			case 1:
				if minID != 100 {
					t.Fatalf("first min id = %d, want 100", minID)
				}
				return []*tg.Message{
					{ID: 101, Date: int(now.Add(-25 * time.Hour).Unix()), Message: "old report"},
					{ID: 102},
				}, nil
			case 2:
				if minID != 102 {
					t.Fatalf("second min id = %d, want 102", minID)
				}
				return staleTelegramPage(now, 103, 1), nil
			default:
				t.Fatalf("unexpected fetch call %d", calls)
				return nil, nil
			}
		},
	)
	if err != nil {
		t.Fatalf("collect forward messages: %v", err)
	}
	if calls != 2 {
		t.Fatalf("fetch calls = %d, want catch-up to continue past textless service event", calls)
	}
	if len(result.Messages) != 0 || result.SkippedStale != 2 {
		t.Fatalf("catch-up result = %+v, want two stale text messages and no fresh messages", result)
	}
	if got := result.CheckpointMessageIDs["channel:42"]; got != 103 {
		t.Fatalf("checkpoint = %d, want 103", got)
	}
}

func TestCollectForwardMessagesStopsWhenPageDoesNotAdvance(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	calls := 0
	result, err := collectForwardMessages(
		context.Background(), "channel:42", 100, now, 24*time.Hour, 2, maxStaleCatchUpPagesPerCollect, 0,
		func(_ context.Context, _ int, _ int) ([]*tg.Message, error) {
			calls++
			return []*tg.Message{
				{ID: 100, Date: int(now.Add(-25 * time.Hour).Unix()), Message: "already seen"},
				{ID: 99, Date: int(now.Add(-25 * time.Hour).Unix()), Message: "already seen"},
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("collect forward messages: %v", err)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1 for non-advancing page", calls)
	}
	if len(result.Messages) != 0 || result.SkippedStale != 0 || len(result.CheckpointMessageIDs) != 0 {
		t.Fatalf("non-progress result = %+v, want empty", result)
	}
}

func TestCollectForwardMessagesDiscardsPartialProgressOnFailure(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	wantErr := errors.New("temporary Telegram failure")
	calls := 0
	result, err := collectForwardMessages(
		context.Background(), "channel:42", 100, now, 24*time.Hour, 2, maxStaleCatchUpPagesPerCollect, 0,
		func(_ context.Context, _ int, _ int) ([]*tg.Message, error) {
			calls++
			if calls == 1 {
				return staleTelegramPage(now, 101, 2), nil
			}
			return nil, wantErr
		},
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if calls != 2 {
		t.Fatalf("fetch calls = %d, want 2", calls)
	}
	if len(result.Messages) != 0 || result.SkippedStale != 0 || len(result.CheckpointMessageIDs) != 0 {
		t.Fatalf("partial failure result = %+v, want empty for at-least-once retry", result)
	}
}

func TestCollectForwardMessagesCancelsDuringInterPageDelay(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	result, err := collectForwardMessages(
		ctx, "channel:42", 100, now, 24*time.Hour, 2, maxStaleCatchUpPagesPerCollect, time.Hour,
		func(_ context.Context, _ int, _ int) ([]*tg.Message, error) {
			calls++
			cancel()
			return staleTelegramPage(now, 101, 2), nil
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1 before canceled delay", calls)
	}
	if len(result.Messages) != 0 || result.SkippedStale != 0 || len(result.CheckpointMessageIDs) != 0 {
		t.Fatalf("canceled result = %+v, want empty for at-least-once retry", result)
	}
}

func staleTelegramPage(now time.Time, firstID, count int) []*tg.Message {
	out := make([]*tg.Message, 0, count)
	for offset := 0; offset < count; offset++ {
		out = append(out, &tg.Message{
			ID:      firstID + offset,
			Date:    int(now.Add(-25 * time.Hour).Unix()),
			Message: "old report",
		})
	}
	return out
}
