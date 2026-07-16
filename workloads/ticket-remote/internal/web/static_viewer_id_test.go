package web

import (
	"strings"
	"testing"
)

func TestStaticClientShowsAccountPublicIDsInViewerAndAdminLists(t *testing.T) {
	source := ticketAppSource(t)
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	presenceBody := substringBetween(t, source,
		"function activeViewerPresence(state) {",
		"  function renderViewerSummary(viewers, visibleViewerCount) {")

	if !strings.Contains(presenceBody, "viewer.publicId") {
		t.Fatalf("public viewer presence must prefer account public IDs before falling back to ordinal labels")
	}
	if !strings.Contains(admin, "{{.PublicID}}") || !strings.Contains(admin, "admin-member-public-id") {
		t.Fatalf("admin member public IDs should have a dedicated class for stable UI styling")
	}
}

func TestStaticClientKeepsArrowPresenceListMountedAcrossIdentifierRefresh(t *testing.T) {
	source := ticketAppSource(t)
	presenceBody := substringBetween(t, source,
		"function renderPresence(viewers, visibleViewerCount) {",
		"  codeDigits.addEventListener('focus', updateViewportVars);")

	for _, required := range []string{
		"const nextViewers = active.map",
		"if (!nextViewers.length && countValue > 0)",
		"key: 'viewer-identifiers-pending'",
		"Identifikatori atjaunojas",
		"mark: 'gaida'",
		"<div class=\"presence-list\" hidden=\"${() => presenceState.viewers.length === 0}\">",
		"${() => presenceState.viewers.map((viewer) => html`",
	} {
		if !strings.Contains(presenceBody, required) {
			t.Fatalf("presence UI missing stable identifier-refresh behavior %q", required)
		}
	}
	for _, forbidden := range []string{
		"presenceState.viewers.length ?",
		"identifiersPending",
		"hidden=${() => presenceState.viewers.length === 0}",
	} {
		if strings.Contains(presenceBody, forbidden) {
			t.Fatalf("presence UI must not unmount the Arrow list while its keyed rows update, found %q", forbidden)
		}
	}
}

func TestAdminTicketSelectionIsServerRendered(t *testing.T) {
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	for _, required := range []string{"Latest ticket", `/api/v1/admin/ticket/reselect-latest`, "{{.RawState}}"} {
		if !strings.Contains(admin, required) {
			t.Fatalf("server-rendered admin missing %q", required)
		}
	}
}

func TestAdminHasNoClientHealthParser(t *testing.T) {
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	if strings.Contains(admin, `<script`) || strings.Contains(admin, `app.js`) {
		t.Fatal("admin must remain a server-rendered page without a client health parser")
	}
}
