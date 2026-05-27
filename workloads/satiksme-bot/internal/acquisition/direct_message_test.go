package acquisition

import (
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestValidateDirectTestMessageRequiresConfirmedRecipient(t *testing.T) {
	err := ValidateDirectTestMessage(DirectTestMessageOptions{
		TargetUsername:          "@aldajo",
		ConfirmTargetUsername:   "@someone_else",
		ExpectSenderUsername:    "@iamhdzs",
		Message:                 "test",
		AllowUnconfirmedTesting: false,
	})
	if err == nil || !strings.Contains(err.Error(), "confirmed recipient") {
		t.Fatalf("ValidateDirectTestMessage() error = %v, want confirmed recipient error", err)
	}
}

func TestValidateDirectTestMessageRequiresExpectedSender(t *testing.T) {
	err := ValidateDirectTestMessage(DirectTestMessageOptions{
		TargetUsername:        "@aldajo",
		ConfirmTargetUsername: "@aldajo",
		Message:               "test",
	})
	if err == nil || !strings.Contains(err.Error(), "expected sender") {
		t.Fatalf("ValidateDirectTestMessage() error = %v, want expected sender error", err)
	}
}

func TestDefaultDirectTestMessageNamesSenderAndPurpose(t *testing.T) {
	message := DefaultDirectTestMessage("@iamhdzs", "@aldajo")
	for _, want := range []string{"@iamhdzs", "@aldajo", "test"} {
		if !strings.Contains(strings.ToLower(message), strings.ToLower(want)) {
			t.Fatalf("message %q missing %q", message, want)
		}
	}
}

func TestSelectResolvedUserAllowsBotOnlyWhenRequested(t *testing.T) {
	resolved := &tg.ContactsResolvedPeer{
		Peer: &tg.PeerUser{UserID: 42},
		Users: []tg.UserClass{
			&tg.User{ID: 42, Username: "rs_bilete_bot", Bot: true},
		},
	}

	if _, err := selectResolvedUser("rs_bilete_bot", resolved, resolveUserOptions{}); err == nil {
		t.Fatal("selectResolvedUser() error = nil, want bot rejected by default")
	}

	user, err := selectResolvedUser("rs_bilete_bot", resolved, resolveUserOptions{AllowBot: true})
	if err != nil {
		t.Fatalf("selectResolvedUser() with AllowBot: %v", err)
	}
	if user.ID != 42 || user.Username != "rs_bilete_bot" {
		t.Fatalf("user = %+v, want bot user 42", user)
	}
}
