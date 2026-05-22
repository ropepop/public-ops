package broker

import (
	"encoding/base64"
	"strings"
)

type rigasSatiksmeQRPhoneMessage struct {
	Type        string `json:"type"`
	RequestID   string `json:"requestId"`
	OK          bool   `json:"ok"`
	Accepted    *bool  `json:"accepted"`
	Reason      string `json:"reason"`
	TicketState string `json:"ticketState"`
	Value       string `json:"value"`
	ImageMIME   string `json:"imageMime"`
	ImageBase64 string `json:"imageBase64"`
	SourceApp   string `json:"sourceApp"`
	TicketFlow  string `json:"ticketFlow"`
}

type rigasSatiksmeQRPhoneDecision struct {
	Final  bool
	OK     bool
	Reason string
	MIME   string
	Image  []byte
}

func evaluateRigasSatiksmeQRPhoneMessage(payload rigasSatiksmeQRPhoneMessage) rigasSatiksmeQRPhoneDecision {
	switch strings.TrimSpace(payload.Type) {
	case "rigassatiksme_qr_result":
		if !payload.OK {
			return rigasSatiksmeQRPhoneDecision{
				Final:  true,
				OK:     false,
				Reason: normalizeRigasSatiksmeQRFailureReason(payload.Reason),
			}
		}
		if strings.TrimSpace(payload.SourceApp) != expectedRigasSatiksmeSourceApp ||
			strings.TrimSpace(payload.TicketFlow) != expectedRigasSatiksmeTicketFlow {
			return rigasSatiksmeQRPhoneDecision{Final: true, OK: false, Reason: "wrong_qr_source"}
		}
		image, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload.ImageBase64))
		if err != nil || len(image) == 0 {
			return rigasSatiksmeQRPhoneDecision{Final: true, OK: false, Reason: "qr_image_missing"}
		}
		mime := strings.TrimSpace(payload.ImageMIME)
		if mime == "" {
			mime = "image/png"
		}
		image, mime = cropRigasSatiksmeGeneratedScreenshotArtifact(image, mime)
		return rigasSatiksmeQRPhoneDecision{
			Final:  true,
			OK:     true,
			Reason: "generated",
			MIME:   mime,
			Image:  image,
		}
	case "ticket_state_event":
		return rigasSatiksmeQRPhoneDecision{}
	case "control_code_result":
		accepted := payload.OK
		if payload.Accepted != nil {
			accepted = *payload.Accepted
		}
		if accepted {
			return rigasSatiksmeQRPhoneDecision{}
		}
		return rigasSatiksmeQRPhoneDecision{
			Final:  true,
			OK:     false,
			Reason: normalizeRigasSatiksmeQRFailureReason(payload.Reason),
		}
	default:
		return rigasSatiksmeQRPhoneDecision{}
	}
}
