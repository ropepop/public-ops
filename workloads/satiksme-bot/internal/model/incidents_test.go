package model

import "testing"

func TestGenericNicknameUsesTelegramStableID(t *testing.T) {
	userID := int64(777001)
	got := GenericNickname(userID)
	want := GenericNicknameForStableID("telegram:777001")
	if got != want {
		t.Fatalf("GenericNickname(%d) = %q, want %q", userID, got, want)
	}
	if TelegramStableID(userID) != "telegram:777001" {
		t.Fatalf("TelegramStableID(%d) = %q", userID, TelegramStableID(userID))
	}
}
