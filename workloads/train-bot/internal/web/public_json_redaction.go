package web

import (
	"encoding/json"
	"math"
	"strings"
	"time"
)

func redactPublicJSONPayload(payload any) any {
	body, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return payload
	}
	redactPublicJSONValue(decoded)
	return decoded
}

func redactPublicJSONValue(value any) {
	switch item := value.(type) {
	case map[string]any:
		delete(item, "sourceVersion")
		if schedule, ok := item["schedule"].(map[string]any); ok {
			item["schedule"] = publicSchedulePayload(schedule)
		}
		if generatedAt, ok := item["generatedAt"].(string); ok {
			if rounded := publicTimestampString(generatedAt); rounded != "" {
				item["generatedAt"] = rounded
			}
		}
		for _, key := range []string{"latitude", "longitude"} {
			if coordinate, ok := item[key].(float64); ok {
				item[key] = publicCoordinate(coordinate)
			}
		}
		if rawSignal, ok := item["signal"]; ok {
			if _, hasAt := item["at"]; hasAt {
				if _, hasCount := item["count"]; hasCount {
					if _, exists := item["eventLabel"]; !exists {
						item["eventLabel"] = publicSignalEventLabel(rawSignal)
					}
				}
			}
			delete(item, "signal")
		}
		for _, child := range item {
			redactPublicJSONValue(child)
		}
	case []any:
		for _, child := range item {
			redactPublicJSONValue(child)
		}
	}
}

func publicSchedulePayload(schedule map[string]any) map[string]any {
	out := map[string]any{}
	if available, ok := schedule["available"].(bool); ok {
		out["available"] = available
	}
	if serviceDate, ok := schedule["effectiveServiceDate"].(string); ok && strings.TrimSpace(serviceDate) != "" {
		out["effectiveServiceDate"] = strings.TrimSpace(serviceDate)
	}
	return out
}

func publicTimestampString(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return ""
	}
	return parsed.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func publicCoordinate(value float64) float64 {
	return math.Round(value*100000) / 100000
}

func publicSignalEventLabel(value any) string {
	signal, ok := value.(string)
	if !ok {
		return ""
	}
	switch signal {
	case "INSPECTION_STARTED":
		return "Inspection started"
	case "INSPECTION_IN_MY_CAR":
		return "Inspection in carriage"
	case "INSPECTION_ENDED":
		return "Inspection ended"
	default:
		return signal
	}
}
