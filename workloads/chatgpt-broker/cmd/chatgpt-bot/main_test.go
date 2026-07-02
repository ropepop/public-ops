package main

import (
	"strings"
	"testing"
)

func TestBrokerPromptAlwaysStartsFreshChatWithoutThreadRouting(t *testing.T) {
	prompt := brokerPrompt("  hello\nthere  ")

	if !strings.HasPrefix(prompt, "CHATGPT_BROKER_CONTROL new=1;files=0\n") {
		t.Fatalf("prompt control header = %q", prompt)
	}
	if strings.Contains(prompt, "thread=") {
		t.Fatalf("prompt must not carry Telegram thread routing: %q", prompt)
	}
	if !strings.HasSuffix(prompt, "\nhello\nthere") {
		t.Fatalf("prompt body was not preserved: %q", prompt)
	}
}

func TestNotificationTextSendsFinalAnswerOnly(t *testing.T) {
	got := notificationText(brokerNotification{
		ID:         "cg-test",
		Status:     "succeeded",
		ResultText: "  model answer  ",
	})

	if got != "model answer" {
		t.Fatalf("notification text = %q", got)
	}
}

func TestNotificationTextHidesFailureCodeInNormalChat(t *testing.T) {
	got := notificationText(brokerNotification{
		ID:           "cg-test",
		Status:       "failed_final",
		PublicStatus: "Phone automation failed",
		FailureCode:  "RESULT_EXTRACTION_UNVERIFIED",
	})

	if got != "Phone automation failed" {
		t.Fatalf("notification text = %q", got)
	}
	if strings.Contains(got, "RESULT_EXTRACTION_UNVERIFIED") {
		t.Fatalf("normal chat notification leaked raw failure code: %q", got)
	}
}
