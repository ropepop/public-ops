package auth

import (
	"strings"
	"testing"
	"time"
)

func TestGuestSessionCannotBecomeMember(t *testing.T) {
	v := NewValidator(AccessConfig{SessionSigningKey: "guest-test-key"})
	now := time.Now()
	token, err := v.IssueGuestSession("invitation-1", "session-1", now)
	if err != nil {
		t.Fatal(err)
	}
	guest, err := v.ValidateGuestSession(token, now)
	if err != nil || guest.ActorID() != "guest:invitation-1" {
		t.Fatalf("guest session: %v, %v", guest, err)
	}
	if _, err := v.ValidateServerSession(token, now); err == nil {
		t.Fatal("guest became a member")
	}
	for _, bad := range []string{token + "a", strings.Replace(token, "trguest1.", "trsess1.", 1), ""} {
		if _, err := v.ValidateGuestSession(bad, now); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	if _, err := v.ValidateGuestSession(token, now.Add(DefaultServerSessionTTL)); err == nil {
		t.Fatal("expired guest accepted")
	}
}
