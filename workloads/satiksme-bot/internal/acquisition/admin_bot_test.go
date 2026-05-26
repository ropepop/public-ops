package acquisition

import "testing"

func TestParseAdminDecisionRecognizesApproveAndRejectCommands(t *testing.T) {
	for _, tc := range []struct {
		text   string
		action AdminAction
		token  string
	}{
		{text: "/approve abc123", action: AdminApprove, token: "abc123"},
		{text: "approve abc123", action: AdminApprove, token: "abc123"},
		{text: "/reject def456", action: AdminReject, token: "def456"},
		{text: "reject def456", action: AdminReject, token: "def456"},
	} {
		decision, ok := ParseAdminDecision(tc.text)
		if !ok || decision.Action != tc.action || decision.Token != tc.token {
			t.Fatalf("ParseAdminDecision(%q) = %+v %v, want %s %s", tc.text, decision, ok, tc.action, tc.token)
		}
	}
}

func TestParseAdminDecisionIgnoresOtherMessages(t *testing.T) {
	for _, text := range []string{"", "hello", "/approve", "/unknown token"} {
		if decision, ok := ParseAdminDecision(text); ok {
			t.Fatalf("ParseAdminDecision(%q) = %+v true, want ignored", text, decision)
		}
	}
}

func TestFormatDraftApprovalMessageIncludesApproveAndRejectCommands(t *testing.T) {
	message := FormatDraftApprovalMessage(ApprovalDraft{
		Token:    "tok-1",
		UserID:   42,
		Username: "target",
		Language: "lv",
		Text:     "hello",
	})

	for _, want := range []string{"/approve tok-1", "/reject tok-1", "@target", "hello"} {
		if !contains(message, want) {
			t.Fatalf("message %q missing %q", message, want)
		}
	}
}

func contains(text string, want string) bool {
	return len(want) == 0 || (len(text) >= len(want) && stringContains(text, want))
}

func stringContains(text string, want string) bool {
	for i := 0; i+len(want) <= len(text); i++ {
		if text[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
