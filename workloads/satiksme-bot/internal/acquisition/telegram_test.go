package acquisition

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"
)

func TestCandidatesFromHistoryKeepsRecentUserMetadata(t *testing.T) {
	messageTime := time.Date(2026, 5, 26, 7, 30, 0, 0, time.UTC)
	result := &tg.MessagesMessages{
		Messages: []tg.MessageClass{
			&tg.Message{
				ID:      10,
				Date:    int(messageTime.Unix()),
				FromID:  &tg.PeerUser{UserID: 42},
				Message: "да, где контроль?",
			},
		},
		Users: []tg.UserClass{
			&tg.User{ID: 42, AccessHash: 777, Username: "target", FirstName: "Anna"},
		},
	}

	candidates := CandidatesFromHistory(result, SourceRecentActive)

	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	candidate := candidates[0]
	if candidate.UserID != 42 || candidate.AccessHash != 777 || candidate.Username != "target" {
		t.Fatalf("candidate = %+v, want Telegram metadata", candidate)
	}
	if candidate.LastMessageID != 10 || !candidate.LastActiveAt.Equal(messageTime) {
		t.Fatalf("candidate activity = id %d at %s, want message 10 at %s", candidate.LastMessageID, candidate.LastActiveAt, messageTime)
	}
	if candidate.Language != "ru" {
		t.Fatalf("candidate language = %q, want ru inferred from message", candidate.Language)
	}
}

func TestCandidatesFromUsersSkipsBotsDeletedAndSelf(t *testing.T) {
	now := time.Date(2026, 5, 26, 7, 30, 0, 0, time.UTC)
	candidates := CandidatesFromUsers([]tg.UserClass{
		&tg.User{ID: 1, AccessHash: 11, Username: "real", FirstName: "Real"},
		&tg.User{ID: 2, Bot: true, Username: "bot"},
		&tg.User{ID: 3, Deleted: true, Username: "deleted"},
		&tg.User{ID: 4, Self: true, Username: "self"},
	}, SourceMemberList, now)

	if len(candidates) != 1 {
		t.Fatalf("len(candidates) = %d, want 1", len(candidates))
	}
	if candidates[0].UserID != 1 || candidates[0].Source != SourceMemberList {
		t.Fatalf("candidate = %+v, want real member-list user", candidates[0])
	}
}
