package broker

import (
	"encoding/base64"
	"strings"
)

type rigasSatiksmeQRPhoneMessage struct {
	Type                string           `json:"type"`
	RequestID           string           `json:"requestId"`
	OK                  bool             `json:"ok"`
	Accepted            *bool            `json:"accepted"`
	Reason              string           `json:"reason"`
	TicketState         string           `json:"ticketState"`
	Value               string           `json:"value"`
	ImageMIME           string           `json:"imageMime"`
	ImageBase64         string           `json:"imageBase64"`
	SourceApp           string           `json:"sourceApp"`
	TicketFlow          string           `json:"ticketFlow"`
	TotalDurationMillis int64            `json:"totalDurationMillis"`
	Phases              map[string]int64 `json:"phases"`
}

type rigasSatiksmeQRPhoneDecision struct {
	Final  bool
	OK     bool
	Reason string
	MIME   string
	Image  []byte
	Phone  RSQRPhoneSummary
}

func evaluateRigasSatiksmeQRPhoneMessage(payload rigasSatiksmeQRPhoneMessage) rigasSatiksmeQRPhoneDecision {
	phone := RSQRPhoneSummary{
		SourceApp:           strings.TrimSpace(payload.SourceApp),
		TicketFlow:          strings.TrimSpace(payload.TicketFlow),
		TotalDurationMillis: payload.TotalDurationMillis,
		Phases:              sanitizeRSQRPhonePhases(payload.Phases),
	}
	switch strings.TrimSpace(payload.Type) {
	case "rigassatiksme_qr_result":
		if !payload.OK {
			return rigasSatiksmeQRPhoneDecision{
				Final:  true,
				OK:     false,
				Reason: normalizeRigasSatiksmeQRFailureReason(payload.Reason),
				Phone:  phone,
			}
		}
		if strings.TrimSpace(payload.SourceApp) != expectedRigasSatiksmeSourceApp ||
			strings.TrimSpace(payload.TicketFlow) != expectedRigasSatiksmeTicketFlow {
			return rigasSatiksmeQRPhoneDecision{Final: true, OK: false, Reason: "wrong_qr_source", Phone: phone}
		}
		image, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload.ImageBase64))
		if err != nil || len(image) == 0 {
			return rigasSatiksmeQRPhoneDecision{Final: true, OK: false, Reason: "qr_image_missing", Phone: phone}
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
			Phone:  phone,
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
			Phone:  phone,
		}
	default:
		return rigasSatiksmeQRPhoneDecision{}
	}
}

func sanitizeRSQRPhonePhases(phases map[string]int64) map[string]int64 {
	if len(phases) == 0 {
		return nil
	}
	out := make(map[string]int64, len(phases))
	for key, value := range phases {
		cleanKey := strings.TrimSpace(key)
		if cleanKey == "" {
			continue
		}
		if value < 0 {
			value = 0
		}
		out[cleanKey] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
