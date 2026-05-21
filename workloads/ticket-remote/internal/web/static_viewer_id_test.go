package web

import (
	"strings"
	"testing"
)

func TestStaticClientShowsAccountPublicIDsInViewerAndAdminLists(t *testing.T) {
	source := ticketAppSource(t)
	presenceBody := substringBetween(t, source,
		"function activeViewerPresence(state) {",
		"  function renderPanelSummary(viewers, visibleViewerCount) {")
	adminBody := substringBetween(t, source,
		"function renderAdmin(state, phone, backendsPayload) {",
		"    memberForm.addEventListener('submit'")

	if !strings.Contains(presenceBody, "viewer.publicId") {
		t.Fatalf("public viewer presence must prefer account public IDs before falling back to ordinal labels")
	}
	if !strings.Contains(adminBody, "member.publicId") {
		t.Fatalf("admin member list must show the same 4-character account public ID beside each account")
	}
	if !strings.Contains(adminBody, "admin-member-public-id") {
		t.Fatalf("admin member public IDs should have a dedicated class for stable UI styling")
	}
}
