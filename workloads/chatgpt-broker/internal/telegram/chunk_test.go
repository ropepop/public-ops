package telegram

import "testing"

func TestChunkText(t *testing.T) {
	chunks := ChunkText("hello wonderful world", 12)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2: %#v", len(chunks), chunks)
	}
	if chunks[0] != "hello wonder" {
		t.Fatalf("first chunk = %q", chunks[0])
	}
}

func TestAllowedUserDefaultsClosed(t *testing.T) {
	if AllowedUser(nil, 123) {
		t.Fatal("empty allowlist should deny")
	}
	if !AllowedUser(map[int64]struct{}{123: {}}, 123) {
		t.Fatal("explicit allowlist should allow")
	}
}
