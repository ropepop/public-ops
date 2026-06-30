package telegram

import "strings"

const MessageLimit = 4096

func ChunkText(text string, limit int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if limit <= 0 || limit > MessageLimit {
		limit = MessageLimit
	}
	runes := []rune(text)
	out := make([]string, 0, (len(runes)/limit)+1)
	for len(runes) > limit {
		split := limit
		for i := limit; i > limit/2; i-- {
			if runes[i-1] == '\n' || runes[i-1] == ' ' {
				split = i
				break
			}
		}
		chunk := strings.TrimSpace(string(runes[:split]))
		if chunk != "" {
			out = append(out, chunk)
		}
		runes = []rune(strings.TrimSpace(string(runes[split:])))
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func AllowedUser(allowed map[int64]struct{}, userID int64) bool {
	if len(allowed) == 0 {
		return false
	}
	_, ok := allowed[userID]
	return ok
}
