package web

import (
	"strings"
	"ticketremote/internal/state"
)

func adminStatisticsPayload(snapshot state.Snapshot) map[string]any {
	members := append([]state.Member(nil), snapshot.Members...)
	for index := range members {
		if strings.TrimSpace(members[index].AccountScopeID) == "" && strings.TrimSpace(members[index].Email) != "" {
			members[index].AccountScopeID = ticketAccountScopeID(members[index].Email)
		}
	}
	return map[string]any{
		"members":                   members,
		"pageActivityDaily":         snapshot.PageActivityDaily,
		"actionActivityDaily":       snapshot.ActionActivityDaily,
		"actionStatisticsStartedAt": snapshot.ActionStatisticsStartedAt,
		"serverTime":                snapshot.ServerTime,
		"timeZone":                  "Europe/Riga",
		"days":                      30,
		"secondsPerTick":            5,
	}
}
