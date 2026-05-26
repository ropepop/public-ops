package acquisition

import (
	"strings"
	"testing"
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
