package chatanalyzer

import (
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
