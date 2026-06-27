package broker

import (
	"encoding/base64"
	"testing"
)

func TestRigasSatiksmePhoneMessageDecisionAcceptsOnlyRealImageResult(t *testing.T) {
	image := rigasSatiksmeScreenshotFixturePNG(t, 10, 20)
	decision := evaluateRigasSatiksmeQRPhoneMessage(rigasSatiksmeQRPhoneMessage{
		Type:        "rigassatiksme_qr_result",
		RequestID:   "req-1",
		OK:          true,
		ImageMIME:   "image/png",
		ImageBase64: base64.StdEncoding.EncodeToString(image),
		SourceApp:   expectedRigasSatiksmeSourceApp,
		TicketFlow:  expectedRigasSatiksmeTicketFlow,
	})

	if !decision.Final || !decision.OK || decision.Reason != "generated" || len(decision.Image) == 0 {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRigasSatiksmePhoneMessageDecisionIgnoresVisualMarkers(t *testing.T) {
	decision := evaluateRigasSatiksmeQRPhoneMessage(rigasSatiksmeQRPhoneMessage{
		Type:        "control_code_result",
		RequestID:   "req-1",
		OK:          true,
		Reason:      "generated",
		TicketState: "generated_result",
	})

	if decision.Final {
		t.Fatalf("control_code_result marker must not complete RS image delivery: %#v", decision)
	}
}

func TestRigasSatiksmePhoneMessageDecisionPreservesPixelFailureReason(t *testing.T) {
	decision := evaluateRigasSatiksmeQRPhoneMessage(rigasSatiksmeQRPhoneMessage{
		Type:      "rigassatiksme_qr_result",
		RequestID: "req-1",
		OK:        false,
		Reason:    "rs_monthly_ticket_control_missing",
	})

	if !decision.Final || decision.OK || decision.Reason != "rs_monthly_ticket_control_missing" {
		t.Fatalf("decision = %#v", decision)
	}
}
