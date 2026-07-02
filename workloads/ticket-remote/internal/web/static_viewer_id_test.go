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

func TestAdminTicketSelectionReadsPhoneStatusJson(t *testing.T) {
	source := ticketAppSource(t)
	adminBody := substringBetween(t, source,
		"function renderAdmin(state, phone, backendsPayload) {",
		"  function renderStatus(state, phone, phoneHealth) {")

	if !strings.Contains(adminBody, "phoneRecord.statusJson || phoneRecord.healthJson") {
		t.Fatalf("admin ticket selection must read current phone statusJson before legacy healthJson")
	}
	if !strings.Contains(adminBody, "renderTicketSelection(phoneHealth);") {
		t.Fatalf("admin render must pass parsed phone health into the ticket selection panel")
	}
}

func TestAdminPhoneHealthParserRecoversClippedTicketState(t *testing.T) {
	source := ticketAppSource(t)
	parserBody := substringBetween(t, source,
		"function extractJsonObjectField(raw, field) {",
		"    function relativeTime(value) {")

	for _, required := range []string{
		"function parsePartialPhoneHealth(raw) {",
		"'latestTicketReselect'",
		"'viviState'",
		"'controlCodeRequest'",
		"extractJsonBooleanField(raw, 'streamActive')",
		"return parsePartialPhoneHealth(raw);",
	} {
		if !strings.Contains(parserBody, required) {
			t.Fatalf("admin parser must recover safe fields from clipped phone health, missing %q", required)
		}
	}
}
