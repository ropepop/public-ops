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
		"const presenceListAnchorKey = 'viewer-list-anchor'",
		"hasVisibleRows: false",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("presence UI missing keyed-list reconnect guard %q", required)
		}
	}
	for _, required := range []string{
		"const nextViewers = active.map",
		"if (!nextViewers.length && countValue > 0)",
		"key: 'viewer-identifiers-pending'",
		"Identifikatori atjaunojas",
		"mark: 'gaida'",
		"presenceState.hasVisibleRows = nextViewers.length > 0",
		"key: presenceListAnchorKey",
		"hidden: true",
		"<div class=\"presence-list\" hidden=\"${() => !presenceState.hasVisibleRows}\">",
		"${() => presenceState.viewers.map((viewer) => html`",
		"<div class=\"presence-item\" hidden=\"${viewer.hidden === true}\">",
	} {
		if !strings.Contains(presenceBody, required) {
			t.Fatalf("presence UI missing stable identifier-refresh behavior %q", required)
		}
	}
	anchorIndex := strings.Index(presenceBody, "key: presenceListAnchorKey")
	assignmentIndex := strings.Index(presenceBody, "presenceState.viewers = nextViewers")
	if anchorIndex < 0 || assignmentIndex < 0 || anchorIndex > assignmentIndex {
		t.Fatal("presence UI must append its invisible keyed anchor before replacing the reactive list")
	}
	for _, forbidden := range []string{
		"presenceState.viewers.length ?",
		"identifiersPending",
		"presenceState.viewers.length === 0",
		"hidden=${() => presenceState.viewers.length === 0}",
	} {
		if strings.Contains(presenceBody, forbidden) {
			t.Fatalf("presence UI must not unmount the Arrow list while its keyed rows update, found %q", forbidden)
		}
	}
}

func TestAdminTicketSelectionIsServerRendered(t *testing.T) {
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	for _, required := range []string{"Latest ticket", `class="admin-redetect-form"`, `class="admin-schedule-form"`, `id="adminObeyMemberLimits"`, "Obey normal user limits", "{{.RawState}}"} {
		if !strings.Contains(admin, required) {
			t.Fatalf("server-rendered admin missing %q", required)
		}
	}
	if strings.Contains(admin, `action="/api/v1/admin/ticket/reselect-latest"`) {
		t.Fatal("immediate admin redetection must not retain the legacy POST producer")
	}
}

func TestAdminHasOnlyScopedDirectTicketActionClient(t *testing.T) {
	admin := ticketRemoteSourceFile(t, "internal", "web", "static", "admin.html.tmpl")
	for _, required := range []string{`class="admin-redetect-form" data-direct-ticket-action-v3="true"`, `class="admin-schedule-form" data-direct-ticket-action-v3="true"`, `/static/spacetime-client.js`, `/static/admin-schedule.js`} {
		if !strings.Contains(admin, required) {
			t.Fatalf("admin direct Ticket action client missing %q", required)
		}
	}
	if got := strings.Count(admin, `action="/api/v1/admin/ticket/reselect-latest/schedule"`); got != 1 {
		t.Fatalf("schedule compatibility route must remain cancel-only, got %d template producers", got)
	}
	if !strings.Contains(admin, `data-schedule-purpose="{{.Purpose}}"`) {
		t.Fatal("manual redetection status must expose its filtered purpose for browser verification")
	}
	if strings.Contains(admin, `app.js`) || strings.Contains(admin, `healthJson`) {
		t.Fatal("admin must not load the viewer app or add a client health parser")
	}
}
