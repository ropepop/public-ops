package web

import (
	"encoding/json"
	"strings"
)

func controlCodeInt64FromMessage(raw any) int64 {
	switch typed := raw.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func controlCodePhasesFromMessage(raw any) map[string]int64 {
	values, ok := raw.(map[string]any)
	if !ok || len(values) == 0 {
		return nil
	}
	phases := make(map[string]int64, len(values))
	for name, value := range values {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch typed := value.(type) {
		case float64:
			phases[name] = int64(typed)
		case int64:
			phases[name] = typed
		case int:
			phases[name] = int64(typed)
		case json.Number:
			if parsed, err := typed.Int64(); err == nil {
				phases[name] = parsed
			}
		}
	}
	if len(phases) == 0 {
		return nil
	}
	return phases
}

func cleanControlCodeResultProof(value string) string {
	switch strings.TrimSpace(value) {
	case "phone_root", "phone_visual", "phone_visual_root_confirmed",
		"phone_visual_raw_ticket_after_submit", "phone_root_image", "browser_frame":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}
