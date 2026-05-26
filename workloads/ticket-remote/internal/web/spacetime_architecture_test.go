package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ticketRemoteSourceFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func TestSpacetimeModuleUsesFocusedPublicTablesAndHotHistoryTTL(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "index.ts")

	for _, required := range []string{
		"const HISTORY_TTL_MS = 72 * 60 * 60 * 1000",
		"ticketremote_ticket_summary",
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"ticketremote_phone_status_history",
		"ticketremote_cleanup_schedule",
		"expiresAt: t.string().index()",
		"nextAuditOrdinal(",
		"cleanupExpired(",
		"ScheduleAt.interval(",
		"if (!ctx.senderAuth?.hasJWT) return;",
		"export const updatePhoneStatus = spacetimedb.reducer(",
		"export const getState = spacetimedb.procedure(",
		"{ name: named('get_state') }",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("SpacetimeDB module is missing clean-sheet architecture marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketremote_live_state",
		"stateJson",
		"rowsFrom(tx.db.ticketremote_audit_event.ticketId.filter(ticketId)).length",
		"writeLiveState(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("SpacetimeDB module still contains old scan-heavy marker %q", forbidden)
		}
	}
}

func TestSpacetimeBrowserClientSubscribesToTicketScopedFocusedTables(t *testing.T) {
	source := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")

	for _, required := range []string{
		"SELECT * FROM ticketremote_ticket_summary WHERE ticketId =",
		"SELECT * FROM ticketremote_viewer_public WHERE ticketId =",
		"SELECT * FROM ticketremote_phone_status WHERE ticketId =",
		"publishFocusedState(",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime browser client is missing focused subscription marker %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketremoteLiveState",
		"ticketremote_live_state",
		"stateJson",
		"asRowState(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Spacetime browser client still contains old broad live-state marker %q", forbidden)
		}
	}
}
