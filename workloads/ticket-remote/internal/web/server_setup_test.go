package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	"ticketremote/internal/auth"
	"ticketremote/internal/config"
	"ticketremote/internal/phone"
	"ticketremote/internal/state"
)

func normalizeStaticJSForContains(value string) string {
	normalized := make([]rune, 0, len(value))
	for _, r := range strings.ToLower(value) {
		switch {
		case unicode.IsSpace(r):
			continue
		case r == '"' || r == '`':
			normalized = append(normalized, '\'')
		default:
			normalized = append(normalized, r)
		}
	}
	return string(normalized)
}

func staticContains(source, snippet string) bool {
	return strings.Contains(normalizeStaticJSForContains(source), normalizeStaticJSForContains(snippet))
}

func staticCSSContains(source, snippet string) bool {
	normalizedSource := strings.ReplaceAll(normalizeStaticJSForContains(source), "'", "")
	normalizedSnippet := strings.ReplaceAll(normalizeStaticJSForContains(snippet), "'", "")
	return strings.Contains(normalizedSource, normalizedSnippet)
}

func TestRelayViewerCountTracksUniqueBrowserSessions(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	server.addRelayViewer("session-a")
	server.addRelayViewer("session-a")
	server.addRelayViewer("session-b")

	if got := server.relay.Snapshot().Viewers; got != 2 {
		t.Fatalf("relay viewers after two unique sessions = %d, want 2", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 2 {
		t.Fatalf("relay viewers after closing one socket from session-a = %d, want 2", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after closing session-a = %d, want 1", got)
	}
	server.removeRelayViewer("missing-session")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after closing an unknown session = %d, want 1", got)
	}
	server.removeRelayViewer("session-b")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after closing all sessions = %d, want 0", got)
	}
}

func TestRelayViewerAddRecordsPrivateRelayConnectStart(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)
	traceID := server.direct.beginStartupTrace("session-a", "index_prewarm")
	server.addRelayViewer("session-a")

	snapshot := server.direct.snapshot(time.Now(), server.relay.Snapshot())
	trace, ok := snapshot["startupTrace"].(map[string]any)
	if !ok {
		t.Fatalf("startup trace missing: %#v", snapshot["startupTrace"])
	}
	if trace["id"] != traceID {
		t.Fatalf("startup trace id = %#v, want %q", trace["id"], traceID)
	}
	phases, ok := trace["phases"].([]streamStartupTracePhase)
	if !ok {
		t.Fatalf("startup trace phases missing: %#v", trace["phases"])
	}
	for _, phase := range phases {
		if phase.Name == "private_relay_connect_started" {
			return
		}
	}
	t.Fatalf("private relay connect phase missing: %#v", phases)
}

func TestSpacetimeClientUsesCurrentProductTablesOnly(t *testing.T) {
	body, err := staticFS.ReadFile("static/spacetime-client.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)
	for _, snippet := range []string{
		"ticketremote_stream_desired_state",
		"ticketremote_phone_current_report",
		"ticketremote_relay_current_report",
		"ticketremote_stream_viewer_focus",
		"ticketremote_control_code_request",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("Spacetime client must use current product table marker %q", snippet)
		}
	}
	for _, forbidden := range []string{
		"ticketremote_ticket_summary",
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"ticketremote_service_safe_operational_log",
		"memberAppendDevPerfMetric",
		"memberAppendSafeOperationalLog",
		"logRowId(\"browser\",event,correlationId)",
	} {
		if staticContains(js, forbidden) {
			t.Fatalf("Spacetime client still contains removed marker %q", forbidden)
		}
	}
}

func TestSpacetimeConnectionHooksDoNotCreateViewerPresence(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	start := strings.Index(module, "pub fn identity_connected")
	if start < 0 {
		t.Fatalf("Spacetime connection hook block not found")
	}
	endOffset := strings.Index(module[start:], "service_reducers! {")
	if endOffset < 0 {
		t.Fatalf("Spacetime connection hook block not found")
	}
	end := start + endOffset
	chunk := module[start:end]
	for _, forbidden := range []string{
		"upsertPresence(",
		"disconnectPresence(",
		"connectionSessionId(ctx)",
	} {
		if strings.Contains(chunk, forbidden) {
			t.Fatalf("raw Spacetime connect/disconnect hooks must not change viewer presence, found %q", forbidden)
		}
	}
	for _, required := range []string{
		"pub fn identity_connected(ctx: &ReducerContext) -> Result<(), String> {",
		"if has_valid_service_identity(ctx)",
		"client_email_from_auth(ctx, DEFAULT_TICKET_ID)?",
		"pub fn identity_disconnected(_ctx: &ReducerContext) {}",
		"ticketremote_member_set_stream_focus(ctx;",
	} {
		source := chunk
		if required == "ticketremote_member_set_stream_focus(ctx;" {
			source = module
		}
		if !strings.Contains(source, required) {
			t.Fatalf("connection hook block missing %q", required)
		}
	}
}

func TestStreamPrewarmUsesBrowserSessionLease(t *testing.T) {
	if got := streamPrewarmRelayLeaseID(" session-a "); got != "session-a" {
		t.Fatalf("stream prewarm lease = %q, want session-a", got)
	}
}

func TestPageOpenWarmLeaseIsIndependentOfGraceAndSocketLifetimes(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)
	openedAt := time.Now()
	server.retainRelayViewerForPageOpen("session-a", openedAt, openedAt)
	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", time.Second, "video_socket_open")
	server.releaseRelayViewerPublicOpenGrace("session-a", "stream_first_rendered_frame")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("warm lease inflated the real session count: %d", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 1 || !server.streamDemandStillPresent() {
		t.Fatalf("socket departure cancelled page warmth: viewers=%d", got)
	}
	if server.releaseStreamDesiredIfNoVideoClients("test") {
		t.Fatal("warm page lease allowed durable idle release")
	}
	wantDeadline := openedAt.Add(30 * time.Minute)
	assertDeadline := func() {
		t.Helper()
		status := server.pageOpenWarmSnapshot(openedAt)
		if status["retainedSessions"] != 1 || status["expiresAt"] != wantDeadline.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("warm deadline = %#v, want one session until %s", status, wantDeadline)
		}
	}
	assertDeadline()
	// Ordinary media reconnect and its first picture do not constitute a page opening.
	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", time.Second, "video_socket_open")
	server.releaseRelayViewerPublicOpenGrace("session-a", "stream_first_rendered_frame")
	server.removeRelayViewer("session-a")
	assertDeadline()
	// A later authenticated opening extends the one timer; a late older lookup cannot shorten it.
	newOpening := openedAt.Add(time.Minute)
	server.retainRelayViewerForPageOpen("session-a", newOpening, newOpening)
	wantDeadline = newOpening.Add(30 * time.Minute)
	server.retainRelayViewerForPageOpen("session-a", openedAt, newOpening)
	assertDeadline()
	server.mu.Lock()
	refs, timers := server.relayViewerRefs["session-a"], len(server.streamPrewarmTimers)
	server.mu.Unlock()
	if refs != 1 || timers != 1 {
		t.Fatalf("reopening accumulated warm owners: refs=%d timers=%d", refs, timers)
	}
}

func TestPageOpenWarmExpiryUsesOpeningDeadlineAndPreservesActiveViewer(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			server := newTicketSetupTestServer(t, "pixel")
			t.Cleanup(server.Close)
			if active {
				server.addRelayViewer("session-a")
			}
			now := time.Now()
			// Fresh membership completed with only 30 ms remaining from the original opening.
			openedAt := now.Add(-streamPageOpenWarmHold + 30*time.Millisecond)
			server.retainRelayViewerForPageOpen("session-a", openedAt, now)
			if server.pageOpenWarmSnapshot(now)["expiresAt"] != openedAt.Add(streamPageOpenWarmHold).UTC().Format(time.RFC3339Nano) {
				t.Fatal("membership delay reset the opening deadline")
			}
			deadline := time.Now().Add(time.Second)
			for {
				server.mu.Lock()
				remaining := len(server.streamPageOpenWarmUntil)
				server.mu.Unlock()
				wantViewers := 0
				if active {
					wantViewers = 1
				}
				if remaining == 0 && server.relay.Snapshot().Viewers == wantViewers {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("warm expiry retained a lease or removed the active socket: leases=%d viewers=%d", remaining, server.relay.Snapshot().Viewers)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)
	now := time.Now()
	for _, openedAt := range []time.Time{{}, now.Add(-streamPageOpenWarmHold)} {
		server.retainRelayViewerForPageOpen("session-a", openedAt, now)
	}
	if server.relay.Snapshot().Viewers != 0 || server.pageOpenWarmSnapshot(now)["retainedSessions"] != 0 {
		t.Fatal("unknown or expired opening created a fresh warm lease")
	}
}

func TestPageOpenWarmRenewalFencesAnAlreadyDueExpiry(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)
	now := time.Now()
	server.retainRelayViewerForPageOpen("session-a", now.Add(-streamPageOpenWarmHold+20*time.Millisecond), now)
	key := "page_open_warm:session-a"
	// Hold the actual renewal boundary while the old timer fires. Its callback
	// must not delete the deadline between updating it and replacing the timer.
	func() {
		server.startupLeaseMu.Lock()
		defer server.startupLeaseMu.Unlock()
		server.mu.Lock()
		oldTimer := server.streamPrewarmTimers[key]
		server.mu.Unlock()
		deadline := time.Now().Add(time.Second)
		for oldTimer.Stop() {
			oldTimer.Reset(time.Millisecond)
			time.Sleep(2 * time.Millisecond)
			if time.Now().After(deadline) {
				t.Fatal("old expiry did not become due")
			}
		}
		server.mu.Lock()
		retained := server.streamPrewarmTimers[key] == oldTimer && !server.streamPageOpenWarmUntil["session-a"].IsZero()
		server.mu.Unlock()
		if !retained {
			t.Fatal("due expiry changed a lease while renewal owned its boundary")
		}
		// Replace the timer inside that same boundary, as the real renewal does.
		server.mu.Lock()
		server.streamPageOpenWarmUntil["session-a"] = now.Add(streamPageOpenWarmHold)
		server.mu.Unlock()
		if server.retainRelayLeaseForDuration(key, "session-a", streamPageOpenWarmHold, true, "page_open_warm", false) {
			t.Fatal("replacing the warm timer requested a duplicate viewer reference")
		}
	}()
	time.Sleep(10 * time.Millisecond)
	status := server.pageOpenWarmSnapshot(time.Now())
	if status["retainedSessions"] != 1 || status["expiresAt"] != now.Add(streamPageOpenWarmHold).UTC().Format(time.RFC3339Nano) ||
		server.relay.Snapshot().Viewers != 1 {
		t.Fatalf("stale expiry removed the replacement lease: %#v", status)
	}
}

func TestPageOpenWarmDoesNotExtendItsDeadlineWhileWaitingForLeaseOwnership(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	t.Cleanup(server.Close)
	server.startupLeaseMu.Lock()
	now := time.Now()
	openedAt := now.Add(-streamPageOpenWarmHold + 20*time.Millisecond)
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.retainRelayViewerForPageOpen("session-a", openedAt, now)
	}()
	time.Sleep(30 * time.Millisecond)
	server.startupLeaseMu.Unlock()
	<-done
	server.mu.Lock()
	leases := len(server.streamPrewarmTimers)
	server.mu.Unlock()
	if leases != 0 || server.relay.Snapshot().Viewers != 0 {
		t.Fatal("waiting for the owner created a lease after its opening deadline")
	}
}

func TestRelayPrewarmDoesNotDoubleCountActiveBrowserSession(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPrewarm(streamPrewarmRelayLeaseID("session-a"), time.Hour)

	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after active session prewarm = %d, want 1", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after active socket closes while prewarm retained = %d, want 1", got)
	}
	server.releaseRetainedRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after prewarm release = %d, want 0", got)
	}
}

func TestPublicOpenGraceKeepsViewerAfterInitialSocketCloses(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", time.Hour, "video_socket_open")

	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after grace starts = %d, want 1", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers after initial socket closes during grace = %d, want 1", got)
	}
	server.releaseRelayViewerPublicOpenGrace("session-a", "browser_first_rendered_frame")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after grace release = %d, want 0", got)
	}
}

func TestPublicOpenGraceExpiresIfNoFrameRenders(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", 20*time.Millisecond, "video_socket_open")
	server.removeRelayViewer("session-a")

	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("relay viewers after grace expiry = %d, want 0", server.relay.Snapshot().Viewers)
		case <-ticker.C:
			if got := server.relay.Snapshot().Viewers; got == 0 {
				return
			}
		}
	}
}

func TestExplicitUncorrelatedGraceDoesNotMarkUnrelatedStartupTrace(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	traceID := server.direct.startStartupTrace("session-b", "authenticated_index_accepted")
	beforeSnapshot := server.direct.snapshot(time.Now(), server.relay.Snapshot())
	before, _ := beforeSnapshot["startupTrace"].(map[string]any)
	beforePhases, _ := before["phases"].([]streamStartupTracePhase)

	server.retainRelayViewerForPublicOpenGrace("session-a", time.Hour, "legacy_socket_open", "")
	afterRetainSnapshot := server.direct.snapshot(time.Now(), server.relay.Snapshot())
	afterRetain, _ := afterRetainSnapshot["startupTrace"].(map[string]any)
	afterRetainPhases, _ := afterRetain["phases"].([]streamStartupTracePhase)
	if len(afterRetainPhases) != len(beforePhases) {
		t.Fatalf("explicit uncorrelated grace marked unrelated trace %q: %#v", traceID, afterRetainPhases)
	}
	server.releaseRelayViewerPublicOpenGrace("session-a", "legacy_socket_cleanup", "")
	afterReleaseSnapshot := server.direct.snapshot(time.Now(), server.relay.Snapshot())
	afterRelease, _ := afterReleaseSnapshot["startupTrace"].(map[string]any)
	afterReleasePhases, _ := afterRelease["phases"].([]streamStartupTracePhase)
	if len(afterReleasePhases) != len(beforePhases) {
		t.Fatalf("explicit uncorrelated grace release marked unrelated trace %q: %#v", traceID, afterReleasePhases)
	}
}

func TestPublicOpenGraceOwnerCASRejectsLateOldRunRelease(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	const sessionID = "shared-session"
	traceA := server.direct.startStartupTrace(sessionID, "run_a")
	server.addRelayViewer(sessionID)
	server.retainRelayViewerForPublicOpenGrace(sessionID, time.Hour, "run_a_open", traceA)
	traceB := server.direct.startStartupTrace(sessionID, "run_b")
	server.retainRelayViewerForPublicOpenGrace(sessionID, time.Hour, "run_b_open", traceB)

	server.mu.Lock()
	ownerBefore := server.streamPrewarmOwners[sessionID]
	timerBefore := server.streamPrewarmTimers[sessionID]
	server.mu.Unlock()
	if ownerBefore != traceB || timerBefore == nil {
		t.Fatalf("replacement grace owner=%q timer=%v, want trace B", ownerBefore, timerBefore != nil)
	}
	server.retainRelayViewerForPublicOpenGrace(sessionID, time.Hour, "late_run_a_close", traceA)
	server.mu.Lock()
	ownerAfterLateRetain := server.streamPrewarmOwners[sessionID]
	timerAfterLateRetain := server.streamPrewarmTimers[sessionID]
	server.mu.Unlock()
	if ownerAfterLateRetain != traceB || timerAfterLateRetain != timerBefore {
		t.Fatalf("late retain changed grace owner=%q timer_same=%t", ownerAfterLateRetain, timerAfterLateRetain == timerBefore)
	}
	if server.retainRelaysForDuration(sessionID, 0, false, "release", traceA) {
		t.Fatal("late old run released replacement grace")
	}
	if server.retainRelaysForDuration(sessionID, 0, false, "release", "") {
		t.Fatal("uncorrelated socket released trace-bound grace")
	}
	server.mu.Lock()
	ownerAfter := server.streamPrewarmOwners[sessionID]
	timerAfter := server.streamPrewarmTimers[sessionID]
	server.mu.Unlock()
	if ownerAfter != traceB || timerAfter != timerBefore {
		t.Fatalf("late release changed grace owner=%q timer_same=%t", ownerAfter, timerAfter == timerBefore)
	}
	server.releaseRelayViewerPublicOpenGrace(sessionID, "test_cleanup", traceB)
	server.removeRelayViewer(sessionID)
}

func TestReplacedRunCanReleaseItsOwnUnreplacedGrace(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	const sessionID = "shared-session"
	traceA := server.direct.startStartupTrace(sessionID, "run_a")
	server.addRelayViewer(sessionID)
	server.retainRelayViewerForPublicOpenGrace(sessionID, time.Hour, "run_a_open", traceA)
	server.direct.startStartupTrace(sessionID, "run_b")

	server.releaseRelayViewerPublicOpenGrace(sessionID, "run_a_painted", traceA)
	server.mu.Lock()
	_, timerPresent := server.streamPrewarmTimers[sessionID]
	_, ownerPresent := server.streamPrewarmOwners[sessionID]
	server.mu.Unlock()
	if timerPresent || ownerPresent {
		t.Fatalf("replaced run's own grace remained: timer=%t owner=%t", timerPresent, ownerPresent)
	}
	server.removeRelayViewer(sessionID)
}

func TestTracedPrewarmLeaseKeepsLatestTraceOwner(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	const sessionID = "shared-session"
	traceA := server.direct.startStartupTrace(sessionID, "run_a")
	server.retainRelayViewerForPrewarm(sessionID, time.Hour, startupTraceCorrelationID(traceA), traceA)
	traceB := server.direct.startStartupTrace(sessionID, "run_b")
	server.retainRelayViewerForPrewarm(sessionID, time.Hour, startupTraceCorrelationID(traceB), traceB)

	server.mu.Lock()
	ownerBefore := server.streamPrewarmOwners[sessionID]
	timerBefore := server.streamPrewarmTimers[sessionID]
	server.mu.Unlock()
	if ownerBefore != traceB || timerBefore == nil {
		t.Fatalf("replacement prewarm owner=%q timer=%v, want trace B", ownerBefore, timerBefore != nil)
	}
	if server.retainRelaysForDuration(sessionID, time.Hour, true, "prewarm", traceA) {
		t.Fatal("late old run installed a replacement prewarm lease")
	}
	server.mu.Lock()
	ownerAfter := server.streamPrewarmOwners[sessionID]
	timerAfter := server.streamPrewarmTimers[sessionID]
	server.mu.Unlock()
	if ownerAfter != traceB || timerAfter != timerBefore {
		t.Fatalf("late prewarm changed owner=%q timer_same=%t", ownerAfter, timerAfter == timerBefore)
	}
	if !server.retainRelaysForDuration(sessionID, 0, false, "release", traceB) {
		t.Fatal("latest trace could not release its own prewarm lease")
	}
	server.removeRelayViewer(sessionID)
}

func TestPublicOpenGraceInstallCannotOutrunSiblingCompletion(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	const sessionID = "shared-session"
	traceID := server.direct.startStartupTrace(sessionID, "shared_run")
	server.addRelayViewer(sessionID)

	// Hold the timer-state lock so retain reaches the trace CAS and pauses
	// immediately before installation. Completion must wait for that CAS, and
	// its subsequent release must wait until the matching relay ref is added.
	server.mu.Lock()
	retainDone := make(chan struct{})
	go func() {
		server.retainRelayViewerForPublicOpenGrace(sessionID, time.Hour, "sibling_open", traceID)
		close(retainDone)
	}()
	deadline := time.Now().Add(time.Second)
	traceLocked := false
	for time.Now().Before(deadline) {
		if server.direct.mu.TryLock() {
			server.direct.mu.Unlock()
			time.Sleep(time.Millisecond)
			continue
		}
		traceLocked = true
		break
	}
	if !traceLocked {
		server.mu.Unlock()
		t.Fatal("grace retain did not reach the trace CAS")
	}
	completionDone := make(chan struct{})
	go func() {
		server.direct.completeStartupTraceForTrace(traceID, "browser_first_rendered_frame", "sibling=true")
		server.releaseRelayViewerPublicOpenGrace(sessionID, "sibling_painted", traceID)
		close(completionDone)
	}()
	server.mu.Unlock()

	select {
	case <-retainDone:
	case <-time.After(2 * time.Second):
		t.Fatal("grace retain did not finish")
	}
	select {
	case <-completionDone:
	case <-time.After(2 * time.Second):
		t.Fatal("sibling completion did not release the installed grace")
	}
	server.mu.Lock()
	_, timerPresent := server.streamPrewarmTimers[sessionID]
	_, ownerPresent := server.streamPrewarmOwners[sessionID]
	viewerRefs := server.relayViewerRefs[sessionID]
	server.mu.Unlock()
	if timerPresent || ownerPresent || viewerRefs != 1 {
		t.Fatalf("completed sibling left grace state: timer=%t owner=%t viewer_refs=%d", timerPresent, ownerPresent, viewerRefs)
	}
	server.removeRelayViewer(sessionID)
}

func TestFirstRenderedFrameReleasesPublicOpenGrace(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	startupTraceID := server.direct.beginStartupTrace("session-a", "test_video_socket_open")
	client := &client{sessionID: "session-a", startupTraceID: startupTraceID}
	noteTestKeyframeWritten(client, 7, 1, time.Now())

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", time.Hour, "video_socket_open", startupTraceID)
	server.handleVideoStreamMessage(context.Background(), client, []byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":7,\"frameSequence\":1}"}`))

	if !client.firstVideoFrameRendered {
		t.Fatal("client should be marked as having rendered its first video frame")
	}
	if got := server.relay.Snapshot().Viewers; got != 1 {
		t.Fatalf("relay viewers while active socket remains = %d, want 1", got)
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after rendered socket closes = %d, want 0", got)
	}
}

func TestFirstRenderedFrameReleasesPublicOpenGraceWhenDiagnosticLimitIsFull(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	startupTraceID := server.direct.beginStartupTrace("session-a", "test_video_socket_open")
	client := &client{sessionID: "session-a", startupTraceID: startupTraceID}
	noteTestKeyframeWritten(client, 7, 1, time.Now())
	now := time.Now()
	for index := 0; index < maxBrowserClientLogsPerMinute; index++ {
		if !client.allowClientLog(now) || !server.allowBrowserClientLog(now) {
			t.Fatalf("could not fill diagnostic limit at event %d", index+1)
		}
	}

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", time.Hour, "video_socket_open", startupTraceID)
	server.handleVideoStreamMessage(context.Background(), client, []byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":7,\"frameSequence\":1}"}`))

	if !client.firstVideoFrameRendered {
		t.Fatal("first rendered frame lifecycle acknowledgement was blocked by diagnostic rate limiting")
	}
	server.removeRelayViewer("session-a")
	if got := server.relay.Snapshot().Viewers; got != 0 {
		t.Fatalf("relay viewers after rate-limited rendered socket closes = %d, want 0", got)
	}
}

func TestBrowserFrameMarkersRequireMatchingSuccessfulWriterEvidence(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	startupTraceID := server.direct.beginStartupTrace("session-a", "test_video_socket_open")
	client := &client{sessionID: "session-a", startupTraceID: startupTraceID}
	noteTestKeyframeWritten(client, 9, 10, time.Now())

	server.addRelayViewer("session-a")
	server.retainRelayViewerForPublicOpenGrace("session-a", time.Hour, "video_socket_open", startupTraceID)
	for _, marker := range [][]byte{
		[]byte(`{"type":"client_log","event":"browser_first_frame_decoded","detail":"{\"frameEpoch\":8,\"frameSequence\":10}"}`),
		[]byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":9,\"frameSequence\":9}"}`),
	} {
		server.handleVideoStreamMessage(context.Background(), client, marker)
	}

	if client.firstVideoFrameRendered {
		t.Fatal("mismatched browser marker was accepted without matching writer evidence")
	}
	trace := server.direct.startupTraceSnapshot(time.Now())
	if trace["complete"] == true {
		t.Fatalf("mismatched browser marker completed startup trace: %#v", trace)
	}
	server.mu.Lock()
	graceRetained := server.streamPrewarmTimers["session-a"] != nil
	server.mu.Unlock()
	if !graceRetained {
		t.Fatal("mismatched browser marker released public-open grace")
	}

	for _, marker := range [][]byte{
		[]byte(`{"type":"client_log","event":"browser_first_frame_decoded","detail":"{\"frameEpoch\":9,\"frameSequence\":10}"}`),
		[]byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":9,\"frameSequence\":10}"}`),
	} {
		server.handleVideoStreamMessage(context.Background(), client, marker)
	}
	if !client.firstVideoFrameRendered {
		t.Fatal("matching browser marker was not accepted after successful writer evidence")
	}
	trace = server.direct.startupTraceSnapshot(time.Now())
	if trace["complete"] != true {
		t.Fatalf("matching browser marker did not complete startup trace: %#v", trace)
	}
	server.removeRelayViewer("session-a")
}

func TestDelayedBrowserMarkerUsesBoundedSuccessfulWriterHistory(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	startupTraceID := server.direct.beginStartupTrace("session-delayed", "test_video_socket_open")
	client := &client{sessionID: "session-delayed", startupTraceID: startupTraceID}
	now := time.Now()
	noteTestKeyframeWritten(client, 9, 10, now)
	noteTestKeyframeWritten(client, 9, 20, now.Add(time.Millisecond))

	server.addRelayViewer("session-delayed")
	server.retainRelayViewerForPublicOpenGrace("session-delayed", time.Hour, "video_socket_open", startupTraceID)
	for _, marker := range [][]byte{
		[]byte(`{"type":"client_log","event":"browser_first_frame_decoded","detail":"{\"frameEpoch\":9,\"frameSequence\":10}"}`),
		[]byte(`{"type":"client_log","event":"stream_first_rendered_frame","detail":"{\"frameEpoch\":9,\"frameSequence\":10}"}`),
	} {
		server.handleVideoStreamMessage(context.Background(), client, marker)
	}

	if !client.firstVideoFrameRendered {
		t.Fatal("one-shot delayed marker for an earlier successful frame was rejected")
	}
	trace := server.direct.startupTraceSnapshot(time.Now())
	if trace["complete"] != true {
		t.Fatalf("delayed successful marker did not complete startup trace: %#v", trace)
	}
	server.mu.Lock()
	graceRetained := server.streamPrewarmTimers["session-delayed"] != nil
	server.mu.Unlock()
	if graceRetained {
		t.Fatal("delayed successful paint marker did not release public-open grace")
	}
	server.removeRelayViewer("session-delayed")
}

func TestPublicHTTPRedirectsToHTTPS(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "pixel",
			AttachName:       "Pixel",
			BaseURL:          "http://pixel.test",
			DefaultBackendID: "pixel",
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "http://ticket.jolkins.id.lv/", nil)
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "https://ticket.jolkins.id.lv/" {
		t.Fatalf("Location = %q", got)
	}
}

func TestHTTPSResponsesIncludeSafetyHeaders(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:        "pixel",
			AttachName:       "Pixel",
			BaseURL:          "http://pixel.test",
			DefaultBackendID: "pixel",
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	required := map[string]string{
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains; preload",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "same-origin",
		"Permissions-Policy":        "camera=(), microphone=(), geolocation=(), payment=(), usb=(), serial=()",
	}
	for header, want := range required {
		if got := rec.Header().Get(header); got != want {
			t.Fatalf("%s = %q want %q", header, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, snippet := range []string{"default-src 'self'", "script-src 'self' 'nonce-", "worker-src 'none'", "style-src 'self' 'nonce-", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, snippet) {
			t.Fatalf("CSP missing %q: %s", snippet, csp)
		}
	}
	for _, forbidden := range []string{"'unsafe-eval'", "connect-src 'self' https:", " wss:", " ws:"} {
		if strings.Contains(csp, forbidden) {
			t.Fatalf("CSP retained broad source %q: %s", forbidden, csp)
		}
	}
	if !strings.Contains(rec.Body.String(), `nonce="`) {
		t.Fatalf("expected rendered scripts to carry CSP nonce")
	}
}

func TestCSPAllowsOnlyConfiguredAuthAndSpacetimeOrigins(t *testing.T) {
	server := &Server{cfg: config.Config{
		Access: auth.AccessConfig{OIDCIssuer: "https://auth.spacetimedb.com/oidc"},
		State:  state.StoreConfig{SpacetimeHost: "https://maincloud.spacetimedb.com/database/path"},
	}}
	rec := httptest.NewRecorder()
	server.writeHTMLHeaders(rec, "test-nonce")
	csp := rec.Header().Get("Content-Security-Policy")
	for _, allowed := range []string{
		"connect-src 'self' https://maincloud.spacetimedb.com wss://maincloud.spacetimedb.com https://auth.spacetimedb.com",
		"script-src 'self' 'nonce-test-nonce'",
	} {
		if !strings.Contains(csp, allowed) {
			t.Fatalf("CSP missing exact origin %q: %s", allowed, csp)
		}
	}
	for _, broad := range []string{"'unsafe-eval'", " https: ", " wss: ", " ws: "} {
		if strings.Contains(csp, broad) {
			t.Fatalf("CSP contains broad source %q: %s", broad, csp)
		}
	}
}

func TestVersionedStaticAssetsAreCacheable(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
	}, store, phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/static/app.js?v=test-release", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("static app status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") || !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("static app cache-control = %q", got)
	}
	if got := rec.Header().Get("Clear-Site-Data"); got != "" {
		t.Fatalf("static app must not clear browser cache, got %q", got)
	}
}

func TestUnversionedStaticAssetsAreNotLongLived(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "https://ticket.jolkins.id.lv",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
	}, store, phone.NewRelay(phone.RelayConfig{BackendID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "https://ticket.jolkins.id.lv/static/app.js", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("static app status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") || strings.Contains(got, "immutable") {
		t.Fatalf("unversioned static app cache-control = %q", got)
	}
	if got := rec.Header().Get("CDN-Cache-Control"); !strings.Contains(got, "no-store") || strings.Contains(got, "immutable") {
		t.Fatalf("unversioned static app CDN cache-control = %q", got)
	}
}

func TestAssetVersionIsStableDuringProcess(t *testing.T) {
	first := assetVersion()
	time.Sleep(1100 * time.Millisecond)
	second := assetVersion()
	if first == "" || second == "" {
		t.Fatalf("asset version must not be empty: first=%q second=%q", first, second)
	}
	if first != second {
		t.Fatalf("asset version changed within one process: first=%q second=%q", first, second)
	}
}

func TestAdminPageDoesNotExposeRetiredDeviceSetup(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	ownerReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	ownerReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerRec := httptest.NewRecorder()
	server.ServeHTTP(ownerRec, ownerReq)
	ownerBody := ownerRec.Body.String()
	if ownerRec.Code != http.StatusOK {
		t.Fatalf("owner admin page status=%d body=%s", ownerRec.Code, ownerRec.Body.String())
	}
	for _, forbidden := range []string{`data-` + `sim` + `ulator-setup="true"`, `Owner ` + `sim` + `ulator control`, `data-sim-key=`, `/api/v1/admin/phone/setup`} {
		if strings.Contains(ownerBody, forbidden) {
			t.Fatalf("admin page must not render retired device setup %q: %s", forbidden, ownerBody)
		}
	}
}

func TestTicketViewerAdminLinkOnlyShowsForAdmins(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	memberReq := httptest.NewRequest(http.MethodGet, "/", nil)
	memberReq.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	memberRec := httptest.NewRecorder()
	server.ServeHTTP(memberRec, memberReq)
	if memberRec.Code != http.StatusOK {
		t.Fatalf("member page status = %d body = %s", memberRec.Code, memberRec.Body.String())
	}
	if strings.Contains(memberRec.Body.String(), `class="admin-link"`) {
		t.Fatalf("member viewer should not render an unusable admin link: %s", memberRec.Body.String())
	}

	ownerReq := httptest.NewRequest(http.MethodGet, "/", nil)
	ownerReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerRec := httptest.NewRecorder()
	server.ServeHTTP(ownerRec, ownerReq)
	if ownerRec.Code != http.StatusOK {
		t.Fatalf("owner page status = %d body = %s", ownerRec.Code, ownerRec.Body.String())
	}
	if !strings.Contains(ownerRec.Body.String(), `class="admin-link"`) {
		t.Fatalf("owner viewer should render admin link: %s", ownerRec.Body.String())
	}
}

func TestTicketViewerKeepsSafariOnCodeRequestPath(t *testing.T) {
	jsBody, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	cssBody, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	spinnerBody, err := staticFS.ReadFile("static/quick-claim-spinner.svg")
	if err != nil {
		t.Fatal(err)
	}
	serverGoBody, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBody)
	css := string(cssBody)
	spinner := string(spinnerBody)
	serverGo := string(serverGoBody)
	for _, snippet := range []string{
		"requestCodeButton = requireElement('#requestControlCode', 'requestControlCode')",
		"codeDialog = requireElement('#controlCodeDialog', 'controlCodeDialog')",
		"codeDigits = requireElement('#controlCodeDigits', 'controlCodeDigits')",
		"function sanitizeControlDigits(value)",
		"function renderControlCodeRequest(request)",
		"ownedControlCodeRequestIDs=new Set",
		"ownedControlCodeRequestIDs.has(String(requestID))",
		"control_code_message_ignored",
		"function closeCurrentControlCode(openNext)",
		"client.requestControlCode(digits,fastRevision,()=>",
		"client.closeControlCode(requestID,\"browser_closed\")",
		"ownerPublicId:localPublicID",
		"codeResultArea.addEventListener('click'",
		"window.TicketSpacetime.create",
		"/api/v1/auth/session",
		"/api/v1/auth/start",
		"beginSpacetimeLogin(authReturnTarget())",
		"beginSpacetimeLogin",
		"window.addEventListener('error'",
		"window.addEventListener('unhandledrejection'",
		"function requireElement(selector, label)",
		"function showFatalPage(message)",
		"control_code_request_failed",
		"video_message_failed",
		"decoded_frame_render_failed",
		"location.pathname==='/auth/callback'",
		"location.replace('/')",
		"function openControlCodeDialog()",
		"document.exitFullscreen().catch",
		"function layoutViewportRect()",
		"function activeViewers(viewers)",
		"function preserveCurrentFrame(reason)",
		"function redrawPreservedFrame()",
		"function streamStatusStale(status)",
		"preserveCurrentFrame('stream_status_stale')",
		"preserveCurrentFrame('configure_decoder')",
		"invalid_tsf2_frame",
		"function findStartCode(data,from)",
		"function configureDecoder(config, options)",
		"sendVideoClientLog('h264_decoder_recovery_avc_adapter', reason)",
		"function connectDirectVideo(options)",
		"function switchToAvcAdapter(reason)",
		"intentionallyClosedVideoSockets",
		"activeVideoSockets",
		"function closeEarlyVideo(reason)",
		"function claimEarlyVideoSocket()",
		"claimEarlyVideoSocket()",
		"new VideoDecoder({",
		"avc: { format: 'annexb' }",
		"const preferAvc=Boolean(options.preferAvc)",
		"requestReason:`${reason}_avc_adapter`",
		"configure_avc_decoder",
		"ctx.drawImage(frame, 0, 0, canvas.width, canvas.height)",
		"const capturedImage=captureControlCodeResultImage(proof)",
		"await confirmControlCodeBrowserCapture(request,proof)",
		"codeResultImage.src=capturedImage",
		"const sourceCanvas=controlCodeFrozenCandidateFrameForProof(proof)",
		"captureCanvas.width=sourceCanvas.width",
		"captureCanvas.height=sourceCanvas.height",
		"captureContext.drawImage(sourceCanvas,0,0,captureCanvas.width,captureCanvas.height)",
		"function controlCodeMarkerReady(request)",
		"function waitForControlCodeResultScreenshot(request)",
		"status === 'succeeded'",
		"String(serverVersion).startsWith('ticket-remote-')",
		"lastPacketAt=0",
		"lastDecodedFrameAt=0",
		"latestStreamStatus=null",
		"relayReportToStreamStatus(state.relayCurrentReport)",
		"function resetDecoderForRecovery(reason)",
		"requestReason:reason",
		"resetDecoderForRecovery(\"first_frame_decoder_reset\")",
		"function pauseVideoWhileHidden(reason)",
		"function controlCodeKeepsVideoAliveWhileHidden()",
		"control_code_capture_keepalive",
		"control_code_wait_reconnect",
		"publishCurrentStreamFocus('public_connected')",
		"spacetimeClient.heartbeat(active,active?'browser_stream_heartbeat':'browser_no_stream_heartbeat')",
		"window.visualViewport",
		"idleDisconnected=false",
		"idleDisconnectTimer=null",
		"function expireViewerIdle(reason)",
		"closeEarlyVideo('idle_disconnect')",
		"resetStreamState({preserveFrame:true})",
		"showEmpty('Straume ir apturēta pēc 15 minūtēm bez darbības. Pieskaries Sākt, lai turpinātu.', true)",
		"document.body.dataset.streamFreshness='IDLE_DISCONNECTED'",
		"startStreamButton.addEventListener('click'",
		"function resumeFromIdleDisconnect(reason)",
		"resumeFromIdleDisconnect('manual_start')",
		"normalizeAssetVersionURL()",
		"function normalizeAssetVersionURL()",
		"history.replaceState(history.state, document.title, next.toString())",
		"serverAssetVersion && assetVersion",
		"searchParams.set('v'",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer JS missing %q", snippet)
		}
	}
	for _, forbidden := range []string{
		"function handleScreenEngagementEvent(event)",
		"function engageTicketScreen(reason)",
		"navigator.wakeLock.request('screen')",
		"requestTicketFullscreen",
		"requestFullscreen(",
		"webkitRequestFullscreen",
		"function blockStreamGesture(event)",
		"canvas.addEventListener('pointerdown'",
		"canvas.addEventListener('pointermove'",
		"canvas.addEventListener('pointerup'",
		"canvas.addEventListener('pointercancel'",
		"canvas.addEventListener('touchend'",
		"canvas.addEventListener('dblclick'",
		"gesturestart",
		"gesturechange",
		"gestureend",
	} {
		if staticContains(js, forbidden) {
			t.Fatalf("ticket stream and document must not own custom gestures: found %q", forbidden)
		}
	}
	foundFirstFrameSignals := false
	for _, snippet := range []string{
		"first_frame_timeout",
		"first_frame_decoder_reset",
		"first_frame_video_reconnect",
		"first_frame_server_recover",
	} {
		if strings.Contains(js, snippet) {
			foundFirstFrameSignals = true
			break
		}
	}
	if !foundFirstFrameSignals {
		t.Fatalf("ticket viewer JS missing first-frame recovery signals")
	}
	for _, snippet := range []string{
		"canvas.toBlob",
		"FileReader",
		"uploadControlCodeCapture",
		"controlCodeCapturedImages",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS must not upload browser image bytes: found %q", snippet)
		}
	}
	for _, snippet := range []string{
		"controlCodeResultImage",
		"lastRenderedFrameEpoch",
		"lastRenderedFrameSequence",
		"minFrameSequence",
		`captureCanvas.toDataURL("image/png")`,
		"confirmControlCodeBrowserCapture",
		"client.confirmControlCodeBrowserCapture(",
		"captureControlCodeResultScreenshot",
	} {
		if !strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS missing local marker-frame screenshot behavior: %q", snippet)
		}
	}
	if strings.Contains(js, "Gaida koda attēlu") {
		t.Fatalf("ticket viewer must not show interim waiting text over the stream while capturing the control-code frame")
	}
	for _, snippet := range []string{
		"touch-action:pan-y",
		"scroll-snap-type:y proximity",
		".shell{width:100%;min-height:var(--ticket-stage-height)",
		".stage-page{width:100%;min-height:var(--ticket-stage-height)",
		".stage{position:relative;z-index:1;width:100%;min-height:var(--ticket-stage-height)",
		"--ticket-viewport-width",
		"--ticket-viewport-height",
		"--ticket-viewport-left",
		"--ticket-viewport-top",
		"--ticket-dialog-height",
		"--ticket-toolbar-anchor",
		"overscroll-behavior-y:contain",
		"overscroll-behavior:none",
		"-webkit-touch-callout:none",
		"-webkit-tap-highlight-color:transparent",
		".stream-resume-spinner",
		".control-code-result",
		".code-dialog",
		".code-dialog-field input",
		".presence-header",
		"left:calc(var(--stream-left,0px) + 20px)",
		"top:calc(var(--stream-top,0px) + 20px)",
		"pointer-events:none",
		"font-variant-numeric:tabular-nums",
		"streamResumeSpinnerRotate",
	} {
		if !staticCSSContains(css, snippet) {
			t.Fatalf("ticket viewer CSS missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"left: 50%",
		"top: 50%",
		"margin-left: -27px",
		"margin-top: -27px",
		"background: rgba(2, 3, 4",
	} {
		if strings.Contains(css, snippet) {
			t.Fatalf("ticket viewer stream resume spinner should use top-left quick-spinner styling, found %q", snippet)
		}
	}
	for _, forbidden := range []string{
		`control-code-close-hotspot`,
		`screen-engaged`,
	} {
		if strings.Contains(indexHTML, forbidden) || staticContains(js, forbidden) || strings.Contains(css, forbidden) {
			t.Fatalf("ticket stream must not restore retired close or global engagement surfaces: found %q", forbidden)
		}
	}
	if !strings.Contains(indexHTML, `<button id="controlCodeHotspot" class="control-code-hotspot" type="button" aria-label="Pieprasīt kontroles kodu"></button>`) {
		t.Fatal("ticket viewer must expose an accessible top-left control-code start button")
	}
	for _, required := range []string{
		`.control-code-hotspot { position: fixed`,
		`top: 0`,
		`left: 0`,
		`width: var(--ticket-hotspot-width, 50vw)`,
		`height: var(--ticket-hotspot-height, 25vh)`,
		`border: 0`,
		`color: transparent`,
		`font-size: 0`,
		`.control-code-hotspot:disabled`,
		`pointer-events: none`,
	} {
		if !staticCSSContains(indexHTML, required) && !staticCSSContains(css, required) {
			t.Fatalf("ticket viewer must expose the invisible accessible top-left control-code start surface, missing %q", required)
		}
	}
	for _, visible := range []string{
		`aria-label="Pieprasīt kontroles kodu">Kods</button>`,
		`background: rgba(7, 11, 16, 0.86);`,
		`border: 1px solid rgba(255, 255, 255, 0.32);`,
		`box-shadow: 0 4px 18px`,
	} {
		if strings.Contains(indexHTML, visible) {
			t.Fatalf("control-code start surface must remain visually invisible: %q", visible)
		}
	}
	for _, snippet := range []string{
		`/static/quick-claim-spinner.svg`,
		`id="streamResumeSpinner"`,
		`id="controlCodeResultArea"`,
		`class="control-code-result"`,
		`id="requestControlCode"`,
		`id="controlCodeDialog"`,
		`id="controlCodeForm"`,
		`id="controlCodeDigits"`,
		`inputmode="numeric"`,
		`pattern="[0-9]*"`,
		`minlength="2"`,
		`id="closeControlCodeResult"`,
		`class="control-code-close" type="button"`,
		`name="theme-color" content="#020304"`,
		`aria-hidden="true"`,
		`draggable="false" hidden`,
	} {
		if !strings.Contains(indexHTML, snippet) {
			t.Fatalf("ticket viewer HTML missing %q", snippet)
		}
	}
	if strings.Contains(indexHTML, "controlCodeCloseHotspot") || strings.Contains(js, "controlCodeCloseHotspot") {
		t.Fatal("the result must close through its visible cross, not an overlapping invisible close hotspot")
	}
	if !strings.Contains(indexHTML, `<button id="closeControlCodeResult" class="control-code-close" type="button"`) {
		t.Fatal("the visible control-code result cross must be a standalone button")
	}
	for _, snippet := range []string{
		"<svg",
		"viewBox=\"0 0 64 64\"",
		"fill=\"none\"",
		"feDropShadow",
	} {
		if !strings.Contains(spinner, snippet) {
			t.Fatalf("quick-claim spinner asset missing %q", snippet)
		}
	}
	if !strings.Contains(indexHTML, "maximum-scale=1, user-scalable=no") {
		t.Fatalf("ticket viewer viewport should disable Safari double-tap zoom")
	}
	if !strings.Contains(indexHTML, "interactive-widget=overlays-content") {
		t.Fatalf("ticket viewer viewport should ask mobile browsers to overlay the keyboard instead of resizing the stream")
	}
	for _, snippet := range []string{
		`class="panel-summary-item stream-summary"`,
		`.stream-summary`,
		`id="streamStateLabel"`,
		`id="streamStateDetail"`,
		`Pieejama ikvienam lapā`,
		`Biļetes attēls ir aktīvs`,
		`function renderStreamSummary()`,
	} {
		if strings.Contains(indexHTML, snippet) || strings.Contains(js, snippet) || strings.Contains(css, snippet) {
			t.Fatalf("ticket viewer should not expose stale stream summary or copy %q", snippet)
		}
	}
	if !strings.Contains(authRedirectHTML, `name="theme-color" content="#020304"`) {
		t.Fatalf("ticket auth redirect shell should keep the same dark browser theme color")
	}
	if strings.Contains(js, "['touchstart', 'touchmove']") {
		t.Fatalf("ticket viewer should not block all touch movement; vertical scroll must remain available")
	}
	if serverVersion != "ticket-remote-2026-09-06-independent-phone-control-gentle-wave-v180" {
		t.Fatalf("ticket page version should identify the independent-control rollout, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "root-image") || strings.Contains(serverVersion, "phone-image") {
		t.Fatalf("ticket page version should not name the superseded phone-image path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "emu"+"lator") || strings.Contains(serverVersion, "sim"+"ulator") || strings.Contains(serverVersion, "android-"+"sim") {
		t.Fatalf("ticket page version should not name retired device paths, got %q", serverVersion)
	}
	if !strings.Contains(indexHTML, `<script nonce="{{.Nonce}}" defer src="/static/app.js?v={{.AssetVersion}}"></script>`) {
		t.Fatalf("ticket viewer must keep the app script versioned and cacheable")
	}
	if !strings.Contains(indexHTML, `<style nonce="{{.Nonce}}">`) {
		t.Fatal("ticket viewer HTML must keep its nonce-protected inline slider styles")
	}
	if !strings.Contains(serverGo, "assetVersionValue = serverVersion") {
		t.Fatalf("ticket asset fallback version must follow the page version so public caches cannot keep an old app.js")
	}
	if strings.Contains(indexHTML, `/static/spacetime-client.js`) {
		t.Fatalf("ticket viewer must not block first video frame behind the Spacetime client script")
	}
	for _, snippet := range []string{`/static/spacetime-client.js?v={{.AssetVersion}}`, `/static/admin-schedule.js?v={{.AssetVersion}}`} {
		if !strings.Contains(adminHTML, snippet) {
			t.Fatalf("admin scheduled redetection must use the direct authenticated client, missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"function usesDirectSpacetimeAuth()",
		"function loadSpacetimeClientScript()",
		"/static/spacetime-client.js?v=${",
		"function connectSpacetimeState()",
		"spacetimeToken()",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer should defer Spacetime until video is active, missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		`id="extendControl"`,
		`Pagarināt`,
		`const extendButton`,
		`memberExtendControl`,
		`control.extended ? 'Pagarināts laiks'`,
		`already_extended`,
	} {
		if strings.Contains(indexHTML, snippet) || strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer should not expose extension UI or logic %q", snippet)
		}
	}
	for _, snippet := range []string{
		"spacetimeLogin",
		"Pieraksties ar e-pastu",
		"Pierakstīties ar e-pastu",
		"Ja e-pasta saite atveras",
		"send({ type: 'activity', reason: 'public_connected' })",
		"send({ type: 'activity', reason: 'public_heartbeat' })",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer must auto-start SpacetimeAuth instead of showing the old login panel: %q", snippet)
		}
	}
	for _, forbidden := range []struct {
		label string
		body  string
	}{
		{"indexHTML", indexHTML},
		{"app.js", js},
		{"app.css", css},
	} {
		for _, snippet := range []string{
			"claimDialog",
			"showModal",
			"claim-dialog",
			"confirmClaim",
			"claimControl",
			"releaseControl",
			"quickClaimControl",
			"queueQuickClaimTap",
			"quickClaimQueuedOrInFlight",
			"quick_claim_tap",
			"runControlMutation",
			"/api/v1/control/claim",
			"/api/v1/control/release",
			"controlOwner",
			"controlMode",
			"controlTimeDetail",
			"inputQueue",
			"Priv\u0101ta kontroles koda sesija",
			"privacyOverlay",
			"isPrivacyCovered",
			"send({ type: 'tap', x: options.tap.x",
			"RTCPeerConnection",
			"webrtc_ice_config",
			"webrtcVideo",
			"Savieno WebRTC video",
			"TURN",
			"renderPngFrame",
			"isPngStream",
			"control.sessionId === cfg.sessionId && control.email === cfg.email",
			"createImageBitmap",
			"legacy_frame_in_tsf2_stream",
			"version: 'legacy'",
			"configuredFrameEnvelope",
			"|| 'legacy'",
			"left = '-10000px'",
			"MediaProjection fallback",
			"AV1",
			"showUnsupported('Video straume neatnāca laikā. Tālrunim vajag uzmanību.')",
			"Atbalstīti ir tikai pieskārieni.",
			"localStorage.getItem",
			"localStorage.setItem",
			"localStorage.removeItem",
			"sessionStorage.getItem",
			"sessionStorage.setItem",
			"sessionStorage.removeItem",
			"ticket_remote_spacetime_token",
			"ticket_remote_spacetime_token_expires_at",
			"ticket_remote_pkce_verifier",
			"ticket_remote_pkce_state",
			"mozBrightness",
			"AmbientLightSensor",
			"screen.brightness",
			"setBrightness",
		} {
			if strings.Contains(forbidden.body, snippet) {
				t.Fatalf("%s should not contain stale control dialog snippet %q", forbidden.label, snippet)
			}
		}
	}
	if !strings.Contains(serverVersion, "independent-phone-control") {
		t.Fatalf("ticket page version should name the current phone-control path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "root-image") || strings.Contains(serverVersion, "phone-image") {
		t.Fatalf("ticket page version should not name the superseded phone-image path, got %q", serverVersion)
	}
	if strings.Contains(serverVersion, "emu"+"lator") || strings.Contains(serverVersion, "sim"+"ulator") || strings.Contains(serverVersion, "android-"+"sim") {
		t.Fatalf("ticket page version should not name retired device paths, got %q", serverVersion)
	}
	if strings.Contains(indexHTML, `id="webrtcVideo"`) || !strings.Contains(indexHTML, `id="screen"`) {
		t.Fatalf("ticket viewer must render HTTPS H.264 on the canvas, not WebRTC video")
	}
	if strings.Contains(js, "decoderMode !== 'avc'") {
		t.Fatalf("latest-keyframe decoder reset must apply to every WebCodecs decoder mode")
	}
	for _, snippet := range []string{
		"function lastRenderedVisualAge(now)",
		"const renderedVisualAge = freshness.hasFrame ? Number(freshness.visualAgeMillis || 0) : lastRenderedVisualAge(now)",
		"const localStaleAge = Math.max(decodedAge, renderedVisualAge, sequenceStalled ? sequenceStalledAge : 0)",
		"renderedVisualAge,",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer stale detection must use current rendered-frame age, missing %q", snippet)
		}
	}
	for _, snippet := range []string{
		"const streamLiveFreshMaxAgeMs = 3e3",
		"const streamLiveOkMaxAgeMs = streamLiveFreshMaxAgeMs",
		"const streamDegradedMaxAgeMs = 3e3",
		"function freshnessStateForVisualAge(ageMs)",
		"function currentRenderedFreshness(now)",
		"function updateStreamFreshnessStatus(reason)",
		"document.body.dataset.streamFreshness",
		"document.body.dataset.streamLive",
		"visualAgeMillis:",
		"browserReceiveToDecodeMillis:",
		"decodeToRenderMillis:",
		"decoderQueueDelayMillis:",
		"streamFreshnessState:",
		"continuityPresentable:",
		"liveLabeled:",
		"const liveLabeled = visualAgeConservative && clockBoundCurrent && streamFreshnessState === 'LIVE_FRESH'",
		"streamFreshnessState === 'DEGRADED'",
		"LIVE_FRESH",
		"LIVE_OK",
		"DEGRADED",
		"STALE",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer JS missing freshness contract snippet %q", snippet)
		}
	}
	if !staticCSSContains(css, `.stream-resume-spinner`) {
		t.Fatalf("ticket viewer CSS missing stream recovery spinner")
	}
	for _, snippet := range []string{
		`body[data-stream-freshness="STALE"] #screen`,
		`body[data-stream-freshness="STALE"] .stage::after`,
		`body[data-stream-live="false"] .stage::after`,
		`filter:`,
		`opacity:.62`,
		`Straume atjaunojas`,
	} {
		if staticCSSContains(css, snippet) {
			t.Fatalf("ticket viewer CSS must not alter the stream picture during recovery, found %q", snippet)
		}
	}
	for _, snippet := range []string{
		`Straume atjaunojas`,
		`showStreamWaiting('Atjauno straumi...')`,
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("ticket viewer JS must keep stream recovery quiet over the video, found %q", snippet)
		}
	}
	if !strings.Contains(js, "function showStreamRecovery()") {
		t.Fatalf("ticket viewer JS missing quiet stream recovery helper")
	}
	if !strings.Contains(js, "function showQuietStreamLoading()") {
		t.Fatalf("ticket viewer JS missing quiet stream loading helper")
	}
	if strings.Contains(js, `showEmpty("Savienojas...",false)`) {
		t.Fatalf("ticket viewer JS must keep initial stream loading quiet over the video")
	}
	if strings.Contains(js, "renderControlCodeRequest(codeRequest);\n    setStatus('Tiešraide rāda biļeti.');") {
		t.Fatalf("ticket viewer must not unconditionally label the stream live")
	}
	stageStart := strings.Index(indexHTML, `<section class="stage-page"`)
	panelStart := strings.Index(indexHTML, `<aside id="panel"`)
	resultStart := strings.Index(indexHTML, `id="controlCodeResultArea"`)
	if stageStart < 0 || panelStart < 0 || resultStart < 0 {
		t.Fatalf("ticket viewer missing stage, panel, or control-code result markup")
	}
	if !(stageStart < resultStart && resultStart < panelStart) {
		t.Fatalf("control-code result must render in the stream stage, not the lower panel")
	}
	resultCSSStart := strings.Index(css, ".control-code-result {")
	if resultCSSStart < 0 {
		resultCSSStart = strings.Index(css, ".control-code-result{")
	}
	if resultCSSStart < 0 {
		t.Fatalf("ticket viewer CSS missing control-code result overlay")
	}
	resultCSSEnd := strings.Index(css[resultCSSStart:], ".control-code-result[hidden]")
	if resultCSSEnd < 0 {
		t.Fatalf("ticket viewer CSS missing control-code result hidden rule")
	}
	resultCSS := css[resultCSSStart : resultCSSStart+resultCSSEnd]
	for _, snippet := range []string{
		"position: absolute",
		"inset: 0",
		"width: 100%",
		"height: 100%",
		"z-index: 7",
		"place-items: center",
		"padding: 0",
	} {
		if !staticCSSContains(resultCSS, snippet) {
			t.Fatalf("control-code result overlay CSS missing %q", snippet)
		}
	}
	if !staticCSSContains(css, ".control-code-image") || !strings.Contains(indexHTML, "controlCodeResultImage") {
		t.Fatalf("ticket viewer must include the private local control-code result image surface")
	}
	if !staticCSSContains(css, `.control-code-result[data-status="succeeded"] .control-code-result-status`) ||
		!staticCSSContains(css, `.control-code-result[data-status="succeeded"] .panel-detail`) {
		t.Fatalf("successful control-code overlay must hide non-result chrome around the numeric marker")
	}
}

func TestTicketViewerCodeDialogUsesNumericRequestFlow(t *testing.T) {
	body, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(body)
	for _, snippet := range []string{
		"sanitizeControlDigits(codeDigits.value)",
		"controlCodeStatusRank(request.status)",
		"ownerPublicId:localPublicID",
		"locallyClosedControlCodeRequestIDs.add(String(requestID))",
		"return String(value || '').replace(/\\D/g, '')",
		"digits.length < 2 || digits.length > 8",
		"client.requestControlCode(digits,fastRevision,()=>",
		"renderControlCodeRequest({requestId:`pending:${Date.now()}`",
		"closeCurrentControlCode(false)",
		"scheduleControlCodeExpiry(current)",
		"codeResultValue.hidden=true",
		"client.closeControlCode(requestID,\"browser_closed\")",
		"publicPresence=Array.isArray(state&&state.viewerPresence)",
		"activeViewers(state&&state.viewers||[])",
		"requestCodeButton.addEventListener('click',()=>openControlCodeDialog())",
		"controlCodeHotspot.addEventListener('click',requestControlCodeFromHotspot)",
		"codeResultClose.addEventListener('click'",
		"codeDialog.addEventListener('click'",
		"event.key==='Escape'",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("control-code request flow missing %q", snippet)
		}
	}
	if strings.Contains(js, `refreshControlCodeReadiness("control_code_dialog_background_warmup")`) {
		t.Fatalf("opening the control-code dialog must not launch preparation")
	}
	if strings.Contains(js, `reconnectVideoForRecovery("control_code_dialog_stream_warmup")`) {
		t.Fatalf("opening the control-code dialog must not launch a second local video recovery")
	}
	for _, snippet := range []string{
		"function requestControlCodeFromHotspot(event)",
		"if(codeDialogOpen||!codeDialog.hidden||!codeResultArea.hidden||ticketRegisterOverlayOccupiesHotspot())return",
		"if(controlCodeMutationLaneBusy()||memberLimitBlocked('control_code'))return",
		"const sliderOwnsHotspot=ticketRegisterOverlayOccupiesHotspot()",
		"const hotspotUnavailable=busy||limitBlocked||!dialogEntryReady||sliderOwnsHotspot||codeDialogOpen||!codeResultArea.hidden",
		"controlCodeHotspot.disabled=hotspotUnavailable",
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("control-code hotspot must be start-only and unavailable during busy/quota/HDR-proof/slider/result states, missing %q", snippet)
		}
	}
	resultClickStart := strings.Index(js, `codeResultArea.addEventListener("click"`)
	if resultClickStart < 0 {
		t.Fatalf("control-code result click handler missing")
	}
	resultClickEnd := strings.Index(js[resultClickStart:], `codeResultClose.addEventListener("click"`)
	if resultClickEnd < 0 {
		t.Fatalf("control-code result close handler missing")
	}
	resultClickHandler := js[resultClickStart : resultClickStart+resultClickEnd]
	if strings.Contains(resultClickHandler, "closeCurrentControlCode") {
		t.Fatalf("control-code result body clicks must not close the overlay")
	}
	for _, snippet := range []string{
		"pointerStart.claimZone",
		"showQuickClaimSpinner",
		"quickClaimControl",
		"type: 'quick_claim_tap'",
		"replace(/\\D/g, '').slice(0, 9)",
	} {
		if strings.Contains(js, snippet) {
			t.Fatalf("control-code request flow should not keep old quick-claim code %q", snippet)
		}
	}
}

func TestTicketSpacetimeModuleRemovesOldControlMutations(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, snippet := range []string{
		"ticketremote_member_request_control_code(ctx;",
		"ticketremote_member_close_control_code(ctx;",
	} {
		if !strings.Contains(module, snippet) {
			t.Fatalf("SpacetimeDB module missing %q", snippet)
		}
	}
	if strings.Contains(module, "prepare_control_code") {
		t.Fatalf("SpacetimeDB module must not retain the removed preparation reducer")
	}
	for _, snippet := range []string{
		"pub fn ticketremote_member_claim_control(",
		"pub fn ticketremote_member_release_control(",
		"pub fn ticketremote_member_revoke_control(",
		"ticketremote_control_session",
		"control_claimed",
		"CONTROL_EXTENDED_MS",
		"control_extended",
		"already_extended",
		"extended: true",
		"stateError(",
	} {
		if strings.Contains(module, snippet) {
			t.Fatalf("SpacetimeDB module should not keep extension behavior %q", snippet)
		}
	}
}

func TestTicketSpacetimeMemberReducersUseServerClockAndConnectionID(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, reducer := range []string{
		"ticketremote_member_set_stream_focus",
		"ticketremote_member_request_control_code",
		"ticketremote_member_confirm_control_code_browser_capture",
		"ticketremote_member_close_control_code",
		"ticketremote_member_upsert_member",
		"ticketremote_member_remove_member",
	} {
		declarative := reducer + "(ctx;"
		if !strings.Contains(module, declarative) && !strings.Contains(module, "pub fn "+reducer+"(") {
			t.Fatalf("missing reducer %s", reducer)
		}
	}
	for _, snippet := range []string{
		"let $clock = now($ctx);",
		"ctx.timestamp",
		"ctx.connection_id()",
		"fn now(ctx: &ReducerContext)",
		"fn connection_session_id(ctx: &ReducerContext)",
	} {
		if !strings.Contains(module, snippet) {
			t.Fatalf("module missing %q", snippet)
		}
	}
}

func TestTicketSpacetimePublicTablesAreRedacted(t *testing.T) {
	module := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	if strings.Contains(module, "ticketremote_live_state") || strings.Contains(module, "stateJson") {
		t.Fatalf("SpacetimeDB module must not publish the old public JSON snapshot")
	}
	for _, forbidden := range []string{
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"ticketremote_phone_status_history",
		"ticketremote_ticket_summary",
	} {
		if strings.Contains(module, forbidden) {
			t.Fatalf("SpacetimeDB module still publishes removed public table %q", forbidden)
		}
	}
	for _, required := range []string{
		"#[spacetimedb::table(accessor = ticketremote_stream_desired_state, public",
		"#[spacetimedb::table(accessor = ticketremote_phone_current_report, public",
		"#[spacetimedb::table(accessor = ticketremote_relay_current_report, public",
		"#[spacetimedb::table(accessor = ticketremote_control_code_request, public",
	} {
		if !strings.Contains(module, required) {
			t.Fatalf("SpacetimeDB module missing current public table %q", required)
		}
	}
}

func TestSpacetimeControlCodeOwnershipIsRequesterScoped(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, snippet := range []string{
		"ticketremote_control_code_owner",
		"ownerPublicId",
		"client_email_from_auth(ctx, &ticket.id)",
		"if owner.ticketId != ticket_id || clean_email(&owner.email) != email",
	} {
		if !strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB control-code ownership missing snippet %q", snippet)
		}
	}
	for _, snippet := range []string{
		"ticketremote_control_session",
		"member_claim_control",
		"member_release_control",
		"member_revoke_control",
	} {
		if strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB module still contains old control session marker %q", snippet)
		}
	}
}

func TestSpacetimeAuthDirectClientContract(t *testing.T) {
	source := ticketRemoteSourceFile(t, "spacetimedb", "src", "lib.rs")
	for _, snippet := range []string{
		"#[spacetimedb::table(accessor = ticketremote_stream_desired_state, public",
		"#[spacetimedb::table(accessor = ticketremote_phone_current_report, public",
		"#[spacetimedb::table(accessor = ticketremote_relay_current_report, public",
		"#[spacetimedb::table(accessor = ticketremote_control_code_request, public",
		"client_email_from_auth(ctx, &ticket.id)",
		"payload.get(\"email_verified\").and_then(|v| v.as_bool()) != Some(true)",
		"ticketremote_member_set_stream_focus(ctx;",
		"let session_id = non_empty(&sessionId, &connection_session_id(ctx));",
		"upsert_stream_desired_state(",
	} {
		if !strings.Contains(source, snippet) {
			t.Fatalf("SpacetimeDB auth/direct-client contract missing %q", snippet)
		}
	}
	jsBody, err := staticFS.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	clientBody, err := staticFS.ReadFile("static/spacetime-client.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(jsBody)
	for _, snippet := range []string{
		"beginSpacetimeLogin(authReturnTarget())",
		"beginSpacetimeLogin",
		"/api/v1/auth/start",
		"clearLocalAuthState()",
		"/api/v1/auth/session",
		"client.requestControlCode(digits,fastRevision,()=>",
		"client.closeControlCode(requestID,\"browser_closed\")",
		"usesDirectSpacetimeAuth()",
		"publishCurrentStreamFocus('public_connected')",
		"spacetimeClient.heartbeat(active,active?'browser_stream_heartbeat':'browser_no_stream_heartbeat')",
		"let spacetimeClientConnectPromise=null",
		"if(spacetimeClientConnectPromise)return spacetimeClientConnectPromise",
		"spacetimeClientConnectPromise=(async()=>",
		"loadSpacetimeClientScript()",
		"document.head.appendChild(script)",
		`document.documentElement.dataset.ticketUi="arrow"`,
	} {
		if !staticContains(js, snippet) {
			t.Fatalf("ticket viewer SpacetimeAuth JS missing %q", snippet)
		}
	}
	authRedirectIndex := strings.Index(normalizeStaticJSForContains(js), "if(!cfg.authenticated)")
	spacetimeUnavailableIndex := strings.Index(normalizeStaticJSForContains(js), "spacetimedirectunavailable")
	if authRedirectIndex < 0 || spacetimeUnavailableIndex < 0 {
		t.Fatalf("ticket viewer SpacetimeAuth JS missing auth redirect or direct state initialization")
	}
	if spacetimeUnavailableIndex > authRedirectIndex {
		t.Fatalf("SpacetimeAuth callback state must be initialized before unauthenticated redirect starts")
	}
	for _, forbidden := range []string{
		"runSpacetimeMutation((client) => client.upsertMember",
		"runSpacetimeMutation((client) => client.removeMember",
		"runSpacetimeMutation((client) => client.revokeControl",
	} {
		if strings.Contains(string(jsBody), forbidden) {
			t.Fatalf("admin mutations must go through ticket_remote so server state stays synchronized: %q", forbidden)
		}
	}
	for _, snippet := range []string{
		"DbConnection.builder()",
		"memberSetStreamFocus",
		"ticketremote_stream_desired_state",
		"ticketremote_phone_current_report",
		"ticketremote_relay_current_report",
		"ticketremote_control_code_request",
		"onApplied(()=>",
		"this.handlers.onSnapshotApplied?.()",
		"this.publishFocusedState()",
	} {
		if !staticContains(string(clientBody), snippet) {
			t.Fatalf("ticket Spacetime browser bundle missing %q", snippet)
		}
	}
	for _, forbidden := range []string{
		"claimControl()",
		"releaseControl(",
		"revokeControl(",
		"memberClaimControl",
		"ticketremote_ticket_summary",
		"ticketremote_viewer_public",
		"ticketremote_phone_status",
		"memberAppendDevPerfMetric",
	} {
		if strings.Contains(string(clientBody), forbidden) {
			t.Fatalf("ticket Spacetime browser bundle should not expose removed wrapper/table %q", forbidden)
		}
	}
	if strings.Contains(indexHTML, "Cloudflare Access") || strings.Contains(string(jsBody), "Cloudflare Access") {
		t.Fatalf("ticket viewer must not mention Cloudflare Access login")
	}
	if strings.Contains(string(jsBody), "spacetimeLogin") || strings.Contains(string(jsBody), "Pierakstīties ar e-pastu") {
		t.Fatalf("ticket viewer must auto-start SpacetimeAuth instead of showing a local login panel")
	}
}

func TestAdminMembersRouteAddsAndRemovesMember(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"new.member@example.com","role":"member"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	addRec := httptest.NewRecorder()
	server.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", addRec.Code, addRec.Body.String())
	}
	var added apiResponse
	if err := json.NewDecoder(addRec.Body).Decode(&added); err != nil {
		t.Fatal(err)
	}
	if _, ok := added.State.Member("new.member@example.com"); !ok {
		t.Fatalf("added member missing from state: %#v", added.State.Members)
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/members?email=new.member%40example.com", nil)
	removeReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	removeRec := httptest.NewRecorder()
	server.ServeHTTP(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove status = %d body = %s", removeRec.Code, removeRec.Body.String())
	}
	var removed apiResponse
	if err := json.NewDecoder(removeRec.Body).Decode(&removed); err != nil {
		t.Fatal(err)
	}
	if _, ok := removed.State.Member("new.member@example.com"); ok {
		t.Fatalf("removed member still active in state: %#v", removed.State.Members)
	}
}

func TestAdminMemberFormAllowsConfiguredSameOrigin(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader("email=form.member%40example.com&role=member"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://ticket.test")
	req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("same-origin form status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/admin" {
		t.Fatalf("same-origin form redirect = %q, want /admin", got)
	}
	snapshot, err := server.store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.Member("form.member@example.com"); !ok {
		t.Fatalf("same-origin form member missing from state: %#v", snapshot.Members)
	}

	defaultPortReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader("email=default.port%40example.com&role=member"))
	defaultPortReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	defaultPortReq.Header.Set("Origin", "http://ticket.test:80")
	defaultPortReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	defaultPortRec := httptest.NewRecorder()
	server.ServeHTTP(defaultPortRec, defaultPortReq)
	if defaultPortRec.Code != http.StatusSeeOther {
		t.Fatalf("same-origin default-port form status = %d body = %s", defaultPortRec.Code, defaultPortRec.Body.String())
	}
}

func TestAdminMemberFormRejectsOpaqueAndCrossOriginRequests(t *testing.T) {
	tests := []struct {
		name          string
		origin        string
		host          string
		forwardedHost string
	}{
		{name: "opaque", origin: "null"},
		{name: "cross origin", origin: "https://attacker.example"},
		{name: "same host wrong scheme", origin: "https://ticket.test"},
		{name: "same origin wrong port", origin: "http://ticket.test:81"},
		{name: "non http scheme", origin: "ftp://ticket.test"},
		{name: "origin with path", origin: "http://ticket.test/path"},
		{name: "request host alias", origin: "https://alias.example", host: "alias.example", forwardedHost: "alias.example"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newTicketSetupTestServer(t, "pixel")
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader("email=blocked%40example.com&role=member"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", tc.origin)
			if tc.host != "" {
				req.Host = tc.host
			}
			if tc.forwardedHost != "" {
				req.Header.Set("X-Forwarded-Host", tc.forwardedHost)
			}
			req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("origin %q status = %d body = %s", tc.origin, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"error":"bad_origin"`) {
				t.Fatalf("origin %q body = %s", tc.origin, rec.Body.String())
			}
			snapshot, err := server.store.Snapshot(context.Background(), "vivi-default", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := snapshot.Member("blocked@example.com"); ok {
				t.Fatalf("origin %q unexpectedly changed member state", tc.origin)
			}
		})
	}
}

func TestAdminMemberMutationsProtectOwnerAndRejectInvalidRoles(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	tests := []struct {
		name        string
		method      string
		target      string
		contentType string
		body        string
		wantStatus  int
		wantError   string
	}{
		{name: "form remove owner", method: http.MethodPost, target: "/api/v1/admin/members", contentType: "application/x-www-form-urlencoded", body: "action=remove&email=ticket%40jolkins.id.lv", wantStatus: http.StatusConflict, wantError: "owner_protected"},
		{name: "delete owner", method: http.MethodDelete, target: "/api/v1/admin/members?email=ticket%40jolkins.id.lv", wantStatus: http.StatusConflict, wantError: "owner_protected"},
		{name: "json demote owner", method: http.MethodPost, target: "/api/v1/admin/members", contentType: "application/json", body: `{"email":"ticket@jolkins.id.lv","role":"member"}`, wantStatus: http.StatusConflict, wantError: "owner_protected"},
		{name: "form demote owner", method: http.MethodPost, target: "/api/v1/admin/members", contentType: "application/x-www-form-urlencoded", body: "email=ticket%40jolkins.id.lv&role=admin", wantStatus: http.StatusConflict, wantError: "owner_protected"},
		{name: "invalid role", method: http.MethodPost, target: "/api/v1/admin/members", contentType: "application/json", body: `{"email":"invalid.role@example.com","role":"superuser"}`, wantStatus: http.StatusBadRequest, wantError: "bad_role"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			req.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
			rec := httptest.NewRecorder()
			server.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"error":"`+tc.wantError+`"`) {
				t.Fatalf("body = %s", rec.Body.String())
			}
		})
	}

	idempotentReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"ticket@jolkins.id.lv","role":"owner"}`))
	idempotentReq.Header.Set("Content-Type", "application/json")
	idempotentReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	idempotentRec := httptest.NewRecorder()
	server.ServeHTTP(idempotentRec, idempotentReq)
	if idempotentRec.Code != http.StatusOK {
		t.Fatalf("idempotent owner update status = %d body = %s", idempotentRec.Code, idempotentRec.Body.String())
	}

	snapshot, err := server.store.Snapshot(context.Background(), "vivi-default", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := snapshot.Member("ticket@jolkins.id.lv")
	if !ok || owner.Role != state.RoleOwner || !owner.Active {
		t.Fatalf("owner changed after rejected mutations: %#v", owner)
	}
	if _, ok := snapshot.Member("invalid.role@example.com"); ok {
		t.Fatalf("invalid role request created a member: %#v", snapshot.Members)
	}
}

func TestAdminCanManageOnlyOrdinaryMembers(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	request := func(method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ticket-Remote-Email", "admin@example.com")
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}

	if rec := request(http.MethodPost, "/api/v1/admin/members", `{"email":"ordinary@example.com","role":"member"}`); rec.Code != http.StatusOK {
		t.Fatalf("admin add member status = %d body = %s", rec.Code, rec.Body.String())
	}
	for _, body := range []string{
		`{"email":"promoted@example.com","role":"admin"}`,
		`{"email":"promoted@example.com","role":"owner"}`,
		`{"email":"admin@example.com","role":"member"}`,
		`{"email":"ticket@jolkins.id.lv","role":"member"}`,
	} {
		rec := request(http.MethodPost, "/api/v1/admin/members", body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"forbidden"`) {
			t.Fatalf("admin privileged mutation status = %d body = %s", rec.Code, rec.Body.String())
		}
	}
	for _, email := range []string{"admin@example.com", "ticket@jolkins.id.lv"} {
		rec := request(http.MethodDelete, "/api/v1/admin/members?email="+url.QueryEscape(email), "")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"error":"forbidden"`) {
			t.Fatalf("admin privileged removal status = %d body = %s", rec.Code, rec.Body.String())
		}
	}
	if rec := request(http.MethodDelete, "/api/v1/admin/members?email=ordinary%40example.com", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin remove member status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestOwnerCanTransferOwnershipWithoutRemovingFinalOwner(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	request := func(actor, method, target, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ticket-Remote-Email", actor)
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		return rec
	}

	if rec := request("ticket@jolkins.id.lv", http.MethodPost, "/api/v1/admin/members", `{"email":"second.owner@example.com","role":"owner"}`); rec.Code != http.StatusOK {
		t.Fatalf("create second owner status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := request("second.owner@example.com", http.MethodDelete, "/api/v1/admin/members?email=ticket%40jolkins.id.lv", ""); rec.Code != http.StatusOK {
		t.Fatalf("remove first of two owners status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := request("second.owner@example.com", http.MethodPost, "/api/v1/admin/members", `{"email":"second.owner@example.com","role":"member"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error":"owner_protected"`) {
		t.Fatalf("demote final owner status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := request("second.owner@example.com", http.MethodDelete, "/api/v1/admin/members?email=second.owner%40example.com", ""); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error":"owner_protected"`) {
		t.Fatalf("remove final owner status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAdminMemberEditorRendersRoleAppropriateControls(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")
	ownerReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	ownerReq.Header.Set("X-Ticket-Remote-Email", "ticket@jolkins.id.lv")
	ownerRec := httptest.NewRecorder()
	server.ServeHTTP(ownerRec, ownerReq)
	if ownerRec.Code != http.StatusOK {
		t.Fatalf("owner page status = %d body = %s", ownerRec.Code, ownerRec.Body.String())
	}
	ownerBody := ownerRec.Body.String()
	for _, option := range []string{`<option value="member">`, `<option value="admin">`, `<option value="owner">`} {
		if !strings.Contains(ownerBody, option) {
			t.Fatalf("owner page missing role option %s", option)
		}
	}
	if !strings.Contains(ownerBody, `data-member-role="owner" disabled`) {
		t.Fatal("owner page must disable removal of the final owner")
	}
	if strings.Contains(ownerBody, `data-member-role="admin" disabled`) || strings.Contains(ownerBody, `data-member-role="member" disabled`) {
		t.Fatal("owner page must permit removal of administrator and member accounts")
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/admin", nil)
	adminReq.Header.Set("X-Ticket-Remote-Email", "admin@example.com")
	adminRec := httptest.NewRecorder()
	server.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin page status = %d body = %s", adminRec.Code, adminRec.Body.String())
	}
	adminBody := adminRec.Body.String()
	if !strings.Contains(adminBody, `<option value="member">`) || strings.Contains(adminBody, `<option value="admin">`) || strings.Contains(adminBody, `<option value="owner">`) {
		t.Fatal("administrator role selector must offer only ordinary member access")
	}
	for _, role := range []string{"owner", "admin"} {
		if !strings.Contains(adminBody, `data-member-role="`+role+`" disabled`) {
			t.Fatalf("administrator page must disable %s removal", role)
		}
	}
	if strings.Contains(adminBody, `data-member-role="member" disabled`) {
		t.Fatal("administrator page must permit ordinary member removal")
	}
}

func TestAdminMembersRouteRequiresAdmin(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"blocked@example.com","role":"member"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ticket-Remote-Email", "member@example.com")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin add status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestDevAdminMemberDeleteUsesConfiguredIdentityNotTargetEmailQuery(t *testing.T) {
	server := newTicketSetupTestServer(t, "pixel")

	addReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/members", strings.NewReader(`{"email":"delete.target@example.com","role":"member"}`))
	addReq.Header.Set("Content-Type", "application/json")
	addRec := httptest.NewRecorder()
	server.ServeHTTP(addRec, addReq)
	if addRec.Code != http.StatusOK {
		t.Fatalf("add status = %d body = %s", addRec.Code, addRec.Body.String())
	}

	removeReq := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/members?email=delete.target%40example.com", nil)
	removeRec := httptest.NewRecorder()
	server.ServeHTTP(removeRec, removeReq)
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove status = %d body = %s", removeRec.Code, removeRec.Body.String())
	}
	var removed apiResponse
	if err := json.NewDecoder(removeRec.Body).Decode(&removed); err != nil {
		t.Fatal(err)
	}
	if _, ok := removed.State.Member("delete.target@example.com"); ok {
		t.Fatalf("removed member still active in state: %#v", removed.State.Members)
	}
}

func TestSpacetimeAuthUnauthenticatedAdminServesRedirectShell(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:           "spacetime",
			OIDCIssuer:     "https://auth.spacetimedb.com/oidc",
			OIDCClientID:   "client_test",
			OIDCScope:      "openid profile email",
			OIDCRedirect:   "http://ticket.test/auth/callback",
			AuthCookieName: "ticket_remote_auth",
		},
		Phone: config.PhoneConfig{
			BackendID:         "pixel",
			AttachName:        "Pixel",
			BaseURL:           "http://pixel.test",
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("admin unauth status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`window.TICKET_REMOTE_CONFIG`,
		`"authenticated":false`,
		`/static/app.js`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("admin unauth shell missing %q in %s", expected, body)
		}
	}
	if strings.Contains(body, "Admin access is required") || strings.Contains(body, `class="admin-shell"`) {
		t.Fatalf("unauthenticated admin should receive auth redirect shell, got %s", body)
	}
}

func TestSpacetimeAuthServerSessionKeepsAuthenticatedHTTPWorking(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	directStore := &spacetimeBackendCountingStore{Store: store}
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:              "spacetime",
			OIDCIssuer:        "https://auth.spacetimedb.com/oidc",
			OIDCClientID:      "client_test",
			OIDCScope:         "openid profile email",
			OIDCRedirect:      "http://ticket.test/auth/callback",
			AuthCookieName:    "ticket_remote_auth",
			SessionSigningKey: "test-signing-key",
		},
		State: state.StoreConfig{
			Backend:           "spacetime",
			SpacetimeHost:     "https://maincloud.spacetimedb.com",
			SpacetimeDatabase: "ticket-remote-prod-v3",
		},
		Phone: config.PhoneConfig{
			BackendID:         "pixel",
			AttachName:        "Pixel",
			BaseURL:           "http://pixel.test",
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, directStore, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !server.usesDirectSpacetimePresence() {
		t.Fatal("test server should use direct Spacetime presence")
	}
	token, _, err := server.auth.IssueServerSession(auth.Identity{
		Email:         "ticket@jolkins.id.lv",
		Subject:       "user_123",
		EmailVerified: true,
	}, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("session status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["accountScopeId"] != ticketAccountScopeID("ticket@jolkins.id.lv") {
		t.Fatalf("authenticated session account scope = %#v, want signed-in account scope", payload["accountScopeId"])
	}
	spacetimePayload, _ := payload["spacetime"].(map[string]any)
	if spacetimePayload["token"] != "sidecar-member-token" {
		t.Fatalf("authenticated member should receive a sidecar-issued direct token: %#v", spacetimePayload)
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	healthReq.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	healthRec := httptest.NewRecorder()
	server.ServeHTTP(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status = %d body = %s", healthRec.Code, healthRec.Body.String())
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexReq.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	indexRec := httptest.NewRecorder()
	server.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK {
		t.Fatalf("index status = %d body = %s", indexRec.Code, indexRec.Body.String())
	}
	if !strings.Contains(indexRec.Body.String(), `"authenticated":true`) {
		t.Fatalf("authenticated index should render ticket shell, got %s", indexRec.Body.String())
	}
	if len(indexRec.Result().Cookies()) == 0 {
		t.Fatalf("authenticated index should establish a browser session cookie")
	}
}

func TestNeverTTLIsConvertedToFiniteBrowserSession(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  "pixel",
		PhoneBaseURL:    "http://pixel.test",
		PhoneAttachName: "Pixel",
	}); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     config.DurationNever,
		Access: auth.AccessConfig{
			Mode:              "spacetime",
			OIDCIssuer:        "https://auth.spacetimedb.com/oidc",
			OIDCClientID:      "client_test",
			OIDCScope:         "openid profile email",
			OIDCRedirect:      "http://ticket.test/auth/callback",
			AuthCookieName:    "ticket_remote_auth",
			SessionSigningKey: "test-signing-key",
		},
		Phone: config.PhoneConfig{
			BackendID:         "pixel",
			AttachName:        "Pixel",
			BaseURL:           "http://pixel.test",
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, phone.NewRelay(phone.RelayConfig{
		BackendID:  "pixel",
		AttachName: "Pixel",
		BaseURL:    "http://pixel.test",
	}))
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Now()
	token, expiresAt, err := server.auth.IssueServerSession(auth.Identity{
		Email:         "ticket@jolkins.id.lv",
		Subject:       "user_123",
		EmailVerified: true,
	}, config.DurationNever, issuedAt)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := expiresAt.Sub(issuedAt), auth.DefaultServerSessionTTL; got != want {
		t.Fatalf("session TTL = %s, want %s", got, want)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "ticket_remote_auth", Value: token})
	first := httptest.NewRecorder()
	server.ServeHTTP(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first index status = %d body = %s", first.Code, first.Body.String())
	}

	var sessionCookie *http.Cookie
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == "ticket_remote_session" {
			sessionCookie = cookie
			break
		}
	}
	if sessionCookie == nil || sessionCookie.MaxAge != int(auth.DefaultServerSessionTTL.Seconds()) {
		t.Fatalf("browser session cookie is not finite: %#v", first.Result().Cookies())
	}
}

func newTicketSetupTestServer(t *testing.T, activeBackendID string) *Server {
	t.Helper()
	store := NewMemoryStore()
	activeURL := "http://pixel.test"
	activeName := "Pixel"
	if err := store.Bootstrap(context.Background(), state.BootstrapInput{
		TicketID:        "vivi-default",
		DisplayName:     "ViVi timed ticket",
		AdminEmail:      "ticket@jolkins.id.lv",
		PhoneBackendID:  activeBackendID,
		PhoneBaseURL:    activeURL,
		PhoneAttachName: activeName,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "admin@example.com", state.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertMember(context.Background(), "vivi-default", "ticket@jolkins.id.lv", "member@example.com", state.RoleMember); err != nil {
		t.Fatal(err)
	}
	relay := phone.NewRelay(phone.RelayConfig{
		BackendID:  activeBackendID,
		AttachName: activeName,
		BaseURL:    activeURL,
	})
	server, err := NewServer(config.Config{
		PublicBaseURL: "http://ticket.test",
		TicketID:      "vivi-default",
		CookieName:    "ticket_remote_session",
		CookieTTL:     time.Hour,
		Access: auth.AccessConfig{
			Mode:     "dev",
			DevEmail: "ticket@jolkins.id.lv",
		},
		Phone: config.PhoneConfig{
			BackendID:  activeBackendID,
			AttachName: activeName,
			BaseURL:    activeURL,
			Backends: []config.PhoneBackend{
				{ID: "pixel", AttachName: "Pixel", BaseURL: "http://pixel.test"},
			},
			DefaultBackendID:  "pixel",
			ActiveBackendFile: filepath.Join(t.TempDir(), "active-phone-backend.json"),
		},
	}, store, relay)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
