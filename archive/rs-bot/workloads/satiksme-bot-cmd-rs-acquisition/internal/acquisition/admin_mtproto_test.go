package acquisition

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestAdminDecisionsFromMessagesParsesPrivateDialogCommandsWithoutSenderID(t *testing.T) {
	decisions, maxID, stats := adminDecisionsFromMessages([]*tg.Message{
		{ID: 187, Out: true, Message: "RS acquisition draft\nApprove: /approve ignored"},
		{ID: 188, Message: "/approve tok-1"},
		{ID: 189, Message: "/reject tok-2"},
	}, 186)

	if len(decisions) != 2 {
		t.Fatalf("decisions = %+v, want two exact commands", decisions)
	}
	if decisions[0].decision.Action != AdminApprove || decisions[0].decision.Token != "tok-1" {
		t.Fatalf("first decision = %+v, want approve tok-1", decisions[0].decision)
	}
	if decisions[1].decision.Action != AdminReject || decisions[1].decision.Token != "tok-2" {
		t.Fatalf("second decision = %+v, want reject tok-2", decisions[1].decision)
	}
	if maxID != 189 || stats.CursorAfter != 189 || stats.MessagesScanned != 3 || stats.DecisionsParsed != 2 {
		t.Fatalf("maxID=%d stats=%+v, want scanned 3 parsed 2 cursor 189", maxID, stats)
	}
}

func TestAdminDecisionsFromMessagesIgnoresOldMessagesAndDraftPromptText(t *testing.T) {
	decisions, maxID, stats := adminDecisionsFromMessages([]*tg.Message{
		{ID: 185, Message: "/approve old"},
		{ID: 187, Out: true, Message: "RS acquisition draft\nTarget: @target\n\nApprove: /approve tok-1\nReject: /reject tok-1"},
	}, 186)

	if len(decisions) != 0 {
		t.Fatalf("decisions = %+v, want none", decisions)
	}
	if maxID != 187 || stats.CursorAfter != 187 || stats.MessagesScanned != 1 || stats.DecisionsParsed != 0 {
		t.Fatalf("maxID=%d stats=%+v, want one scanned prompt and no decision", maxID, stats)
	}
}

func TestAdminDecisionsFromMessagesTreatsIncomingDraftCopyAsApproval(t *testing.T) {
	decisions, maxID, stats := adminDecisionsFromMessages([]*tg.Message{
		{ID: 188, Out: false, Message: "RS acquisition draft\nTarget: @target\nLanguage: lv\n\nhello\n\nApprove: /approve tok-copy\nReject: /reject tok-copy"},
	}, 186)

	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want incoming copied draft approval", decisions)
	}
	if decisions[0].decision.Action != AdminApprove || decisions[0].decision.Token != "tok-copy" {
		t.Fatalf("decision = %+v, want approve tok-copy", decisions[0].decision)
	}
	if maxID != 188 || stats.CursorAfter != 188 || stats.MessagesScanned != 1 || stats.DecisionsParsed != 1 {
		t.Fatalf("maxID=%d stats=%+v, want scanned 1 parsed 1 cursor 188", maxID, stats)
	}
}

func TestAdminDecisionsFromMessagesTreatsForwardedDraftAsApproval(t *testing.T) {
	forwarded := &tg.Message{ID: 188, Message: "RS acquisition draft\nTarget: @target\nLanguage: ru\n\nhello\n\nApprove: /approve tok-forward\nReject: /reject tok-forward"}
	forwarded.SetFwdFrom(tg.MessageFwdHeader{})

	decisions, maxID, stats := adminDecisionsFromMessages([]*tg.Message{forwarded}, 186)

	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want one forwarded approval decision", decisions)
	}
	if decisions[0].decision.Action != AdminApprove || decisions[0].decision.Token != "tok-forward" || decisions[0].decision.MessageID != 188 {
		t.Fatalf("decision = %+v, want approve tok-forward from message 188", decisions[0].decision)
	}
	if maxID != 188 || stats.CursorAfter != 188 || stats.MessagesScanned != 1 || stats.DecisionsParsed != 1 {
		t.Fatalf("maxID=%d stats=%+v, want scanned 1 parsed 1 cursor 188", maxID, stats)
	}
}

func TestAdminDecisionsFromMessagesIgnoresForwardedNonDraftApprovalText(t *testing.T) {
	forwarded := &tg.Message{ID: 188, Message: "Approve: /approve tok-forward"}
	forwarded.SetFwdFrom(tg.MessageFwdHeader{})

	decisions, _, stats := adminDecisionsFromMessages([]*tg.Message{forwarded}, 186)

	if len(decisions) != 0 || stats.DecisionsParsed != 0 {
		t.Fatalf("decisions=%+v stats=%+v, want forwarded non-draft text ignored", decisions, stats)
	}
}
