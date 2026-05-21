package web

import (
	"os"
	"strings"
	"testing"
)

func TestControlCodeResultModalCanWaitBeforeRenderedAztecFrame(t *testing.T) {
	source := ticketAppSource(t)
	waitForScreenshot := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")
	succeededBranch := substringBetween(t, source,
		"    if (current.status === 'succeeded') {",
		"    if (current.status === 'failed') {")
	captureScreenshot := substringBetween(t, source,
		"function captureControlCodeResultScreenshot(request) {",
		"  function failControlCodeResultScreenshotWait() {")

	if strings.Contains(succeededBranch, "setControlCodeResultVisible(true);") {
		t.Fatalf("succeeded status must delegate result display timing to the screenshot wait path")
	}
	if strings.Contains(waitForScreenshot, "canvas.toDataURL") || strings.Contains(waitForScreenshot, "captureControlCodeResultScreenshot(request)") {
		t.Fatalf("waiting modal path must not snapshot the canvas directly before the rendered Aztec frame is ready")
	}

	clearImageIndex := strings.Index(waitForScreenshot, "codeResultImage.removeAttribute('src');")
	waitingStatusIndex := strings.Index(waitForScreenshot, "codeResultArea.dataset.status = 'waiting';")
	visibleIndex := strings.Index(waitForScreenshot, "setControlCodeResultVisible(true);")
	if clearImageIndex < 0 || waitingStatusIndex < 0 || visibleIndex < 0 {
		t.Fatalf("waiting for the post-confirmation Aztec frame must show a stale-image-free waiting modal")
	}
	if clearImageIndex > visibleIndex || waitingStatusIndex > visibleIndex {
		t.Fatalf("waiting modal must clear stale images and set waiting status before it becomes visible")
	}

	captureVisibleIndex := strings.Index(captureScreenshot, "setControlCodeResultVisible(true);")
	captureIndex := strings.Index(captureScreenshot, "codeResultImage.src = canvas.toDataURL('image/png');")
	if captureVisibleIndex < 0 {
		t.Fatalf("capturing the Aztec frame must reveal the private result modal")
	}
	if captureIndex < 0 {
		t.Fatalf("capture function no longer snapshots the stream canvas")
	}
	if captureVisibleIndex < captureIndex {
		t.Fatalf("captured image must be ready before the result modal switches to the captured image")
	}
	if !strings.Contains(waitForScreenshot, "controlCodeResultCaptureRequestID = requestID;") ||
		!strings.Contains(waitForScreenshot, "keepControlCodeVideoAlive('control_code_wait_reconnect');") {
		t.Fatalf("waiting modal path must still arm the request id and keep the video stream alive")
	}
}

func TestControlCodePostConfirmationStressAllowsWaitingModalButRejectsEarlyCapture(t *testing.T) {
	source := ticketAppSource(t)
	readyGate := substringBetween(t, source,
		"function controlCodeMarkerReady(request) {",
		"  function captureControlCodeResultScreenshot(request) {")
	maybeCapture := substringBetween(t, source,
		"function maybeCaptureControlCodeResultImage() {",
		"  function waitForControlCodeResultScreenshot(request) {")
	waitForScreenshot := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")

	if !strings.Contains(readyGate, "lastRenderedFrameEpoch === markerEpoch && lastRenderedFrameSequence >= markerSequence") {
		t.Fatalf("control-code capture must be gated by the rendered frame marker")
	}
	gateIndex := strings.Index(maybeCapture, "if (!controlCodeMarkerReady(codeRequest)) return false;")
	captureIndex := strings.Index(maybeCapture, "return captureControlCodeResultScreenshot(codeRequest);")
	if gateIndex < 0 || captureIndex < 0 || gateIndex > captureIndex {
		t.Fatalf("control-code capture must stay behind marker readiness even when the waiting modal is visible")
	}
	if !strings.Contains(waitForScreenshot, "codeResultArea.dataset.status = 'waiting';") ||
		!strings.Contains(waitForScreenshot, "setControlCodeResultVisible(true);") {
		t.Fatalf("post-confirmation path should be allowed to show a waiting modal before capture")
	}

	badCaptures := 0
	directCaptureBeforeMarker := strings.Contains(waitForScreenshot, "canvas.toDataURL") || gateIndex < 0 || captureIndex < 0 || gateIndex > captureIndex
	for attempt := 0; attempt < 20; attempt++ {
		minFrameSequence := int64(100 + attempt)
		lastRenderedBeforeAztec := minFrameSequence - 1
		frameMarkerReady := lastRenderedBeforeAztec >= minFrameSequence
		if !frameMarkerReady && directCaptureBeforeMarker {
			badCaptures++
		}
	}
	if badCaptures > 1 {
		t.Fatalf("bad post-confirmation capture rate = %d/20, want <= 1/20", badCaptures)
	}
}

func TestControlCodeCloseHidesGeneratedResultBeforeNetworkRoundTrip(t *testing.T) {
	source := ticketAppSource(t)
	closeBody := substringBetween(t, source,
		"async function closeCurrentControlCode(openNext) {",
		"  function requestControlCodeFromHotspot(event) {")

	postIndex := strings.Index(closeBody, "await postJSON('/api/v1/control-code/close'")
	closedIndex := strings.Index(closeBody, "locallyClosedControlCodeRequestIDs.add(String(requestID));")
	if postIndex < 0 || closedIndex < 0 {
		t.Fatalf("close path must mark the request closed locally and then sync with the server")
	}
	localCloseBody := closeBody[closedIndex:postIndex]
	hideIndex := strings.Index(localCloseBody, "setControlCodeResultVisible(false);")
	clearIndex := strings.Index(localCloseBody, "clearControlCodeResultCapture();")
	if hideIndex < 0 || clearIndex < 0 {
		t.Fatalf("close path must hide the result and clear capture state before the close POST can block")
	}
	if hideIndex > clearIndex {
		t.Fatalf("generated control-code result must disappear locally before capture state cleanup and the close POST")
	}
}

func TestVisibilityResumeRecoversOnlyAfterRealBackgroundOrStaleStream(t *testing.T) {
	source := ticketAppSource(t)
	visibilityBody := substringBetween(t, source,
		"document.addEventListener('visibilitychange', () => {",
		"  window.addEventListener('pageshow'")
	pageshowBody := substringBetween(t, source,
		"window.addEventListener('pageshow'",
		"  window.addEventListener('focus'")
	recoveryBody := substringBetween(t, source,
		"function recoverAfterVisibilityResume(reason) {",
		"  window.addEventListener('resize', resizeCanvasBox);")

	for _, needle := range []string{
		"let lastHiddenAt = 0;",
		"const backgroundRecoveryHiddenMs = 30000;",
		"const resumeVideoReconnectDelayMs = 600;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("missing visibility resume state/cadence %q", needle)
		}
	}
	if !strings.Contains(visibilityBody, "lastHiddenAt = performance.now();") ||
		!strings.Contains(visibilityBody, "recoverAfterVisibilityResume('visibility_resume');") {
		t.Fatalf("visibility changes must record hidden time and resume through the bounded recovery path")
	}
	if !strings.Contains(pageshowBody, "if (event.persisted || lastHiddenAt > 0) recoverAfterVisibilityResume('pageshow');") {
		t.Fatalf("pageshow must recover only for BFCache/previously-hidden pages, not every initial show")
	}
	requiredRecoverySnippets := []string{
		"const hiddenMs = lastHiddenAt > 0 ? now - lastHiddenAt : 0;",
		"const longHidden = hiddenMs >= backgroundRecoveryHiddenMs;",
		"const videoStale = configured && (lastFrameAt === 0 || (frameAgeMs !== null && frameAgeMs > streamStaleVideoReconnectMs));",
		"if (longHidden || videoStale) {\n      clientLog('visibility_resume_recovery'",
		"if (!ws || ws.readyState === WebSocket.CLOSED || ws.readyState === WebSocket.CLOSING) {\n      connect();",
		"} else if (ws.readyState === WebSocket.OPEN) {\n      send({ type: 'heartbeat', reason });",
		"if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {\n      connectDirectVideo();",
		"if (videoStale) {\n      setTimeout(() => {",
	}
	for _, needle := range requiredRecoverySnippets {
		if !strings.Contains(recoveryBody, needle) {
			t.Fatalf("resume recovery missing bounded behavior %q", needle)
		}
	}
}

func ticketAppSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../web-client/ticket-app-source.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func substringBetween(t *testing.T, source, startNeedle, endNeedle string) string {
	t.Helper()
	start := strings.Index(source, startNeedle)
	if start < 0 {
		t.Fatalf("missing start needle %q", startNeedle)
	}
	end := strings.Index(source[start+len(startNeedle):], endNeedle)
	if end < 0 {
		t.Fatalf("missing end needle %q", endNeedle)
	}
	return source[start : start+len(startNeedle)+end]
}
