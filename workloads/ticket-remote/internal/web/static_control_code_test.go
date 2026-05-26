package web

import (
	"os"
	"strings"
	"testing"
)

func TestControlCodeResultCaptureWaitsQuietlyBeforeRenderedAztecFrame(t *testing.T) {
	source := ticketAppSource(t)
	waitForScreenshot := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")
	succeededBranch := substringBetween(t, source,
		"    if (current.status === 'succeeded') {",
		"    if (current.status === 'failed') {")
	captureScreenshot := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request",
		"  function failControlCodeResultScreenshotWait() {")

	if strings.Contains(succeededBranch, "showControlCodePhoneImageResult(current)") ||
		strings.Contains(succeededBranch, "hasTrustedControlCodePhoneImage(current)") {
		t.Fatalf("succeeded status must use the browser-local capture path, not a phone image shortcut")
	}
	if strings.Contains(waitForScreenshot, "canvas.toDataURL") || strings.Contains(waitForScreenshot, "captureControlCodeResultScreenshot(request)") {
		t.Fatalf("waiting modal path must not snapshot the canvas directly before the rendered Aztec frame is ready")
	}

	clearImageIndex := strings.Index(waitForScreenshot, "codeResultImage.removeAttribute('src');")
	waitingStatusIndex := strings.Index(waitForScreenshot, "codeResultArea.dataset.status = 'waiting';")
	hiddenIndex := strings.Index(waitForScreenshot, "setControlCodeResultVisible(false);")
	if clearImageIndex < 0 || waitingStatusIndex < 0 || hiddenIndex < 0 {
		t.Fatalf("waiting for the post-confirmation Aztec frame must arm a stale-image-free quiet capture state")
	}
	if clearImageIndex > hiddenIndex || waitingStatusIndex > hiddenIndex {
		t.Fatalf("quiet waiting state must clear stale images and set waiting status before ensuring the overlay is hidden")
	}
	for _, forbidden := range []string{
		"codeResultArea.style.background = 'rgba(0,0,0,.72)';",
		"codeResultStatus.textContent = 'Gaida koda attēlu...';",
		"setControlCodeResultVisible(true);",
	} {
		if strings.Contains(waitForScreenshot, forbidden) {
			t.Fatalf("waiting capture state must not show interim overlay chrome, found %q", forbidden)
		}
	}

	captureVisibleIndex := strings.Index(captureScreenshot, "setControlCodeResultVisible(true);")
	captureIndex := strings.Index(captureScreenshot, "const capturedImage = captureControlCodeResultImage(proof);")
	ackIndex := strings.Index(captureScreenshot, "await confirmControlCodeBrowserCapture(request, proof);")
	if captureVisibleIndex < 0 {
		t.Fatalf("capturing the Aztec frame must reveal the private result modal")
	}
	if captureIndex < 0 {
		t.Fatalf("capture function no longer snapshots the generated-code crop from the stream canvas")
	}
	freezeIndex := strings.Index(captureScreenshot, "controlCodeFrozenCandidateFrameForProof(proof)")
	if freezeIndex < 0 || captureIndex < freezeIndex {
		t.Fatalf("capturing the Aztec frame must use the frozen proven stream frame")
	}
	if ackIndex < 0 || captureVisibleIndex < ackIndex || ackIndex < captureIndex {
		t.Fatalf("captured image must be locally snapped, browser-acked, then revealed")
	}
	if !strings.Contains(waitForScreenshot, "controlCodeResultCaptureRequestID = requestID;") ||
		!strings.Contains(waitForScreenshot, "keepControlCodeVideoAlive('control_code_wait_reconnect');") {
		t.Fatalf("quiet waiting path must still arm the request id and keep the video stream alive")
	}
	if strings.Contains(waitForScreenshot, "const timeoutMs =") ||
		strings.Contains(waitForScreenshot, "failControlCodeResultScreenshotWait();") {
		t.Fatalf("quiet waiting path must not give up locally while the phone is holding the generated code for browser capture")
	}
}

func TestControlCodePhoneImageResultDoesNotBypassBrowserFrameCapture(t *testing.T) {
	source := ticketAppSource(t)
	succeededBranch := substringBetween(t, source,
		"    if (current.status === 'succeeded') {",
		"    if (current.status === 'failed') {")

	if strings.Contains(source, "function showControlCodePhoneImageResult(request)") ||
		strings.Contains(source, "function hasTrustedControlCodePhoneImage(request)") {
		t.Fatalf("public browser must not have a trusted phone-image display shortcut")
	}
	if strings.Contains(succeededBranch, "imageBase64") ||
		strings.Contains(succeededBranch, "phone_root_image") {
		t.Fatalf("succeeded branch must not inspect phone screenshot payloads")
	}
	if strings.Contains(source, "candidate_frame_at_or_after_${proof.resultProof}") ||
		strings.Contains(source, "proof.generatedVisible = true;\n      proof.accepted = true") {
		t.Fatalf("browser capture must not trust Pixel proof alone; it must require generated-screen pixels")
	}
	for _, needle := range []string{
		"waitForControlCodeResultScreenshot(current);",
		"scheduleControlCodeTicker(current);",
		"const capturedImage = captureControlCodeResultImage(proof);",
		"await confirmControlCodeBrowserCapture(request, proof);",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("browser-local capture path missing %q", needle)
		}
	}
}

func TestTicketViewerClaimsEarlyVideoSocketInsteadOfClosingItAtLoad(t *testing.T) {
	source := ticketAppSource(t)
	if strings.Contains(source, "closeEarlyVideo('app_loaded');") {
		t.Fatalf("ticket viewer must claim the head-opened video socket instead of closing it at app load")
	}
	for _, needle := range []string{
		"function claimEarlyVideoSocket() {",
		"const queued = Array.isArray(early.queue) ? early.queue.slice() : [];",
		"function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {",
		"claimEarlyVideoSocket()",
		"queuedMessages.forEach((queued) => {",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("ticket viewer missing early video reuse behavior: %q", needle)
		}
	}
}

func TestControlCodeBrowserCaptureAckIsNonBlockingAndTimerless(t *testing.T) {
	source := controlCodeServerSource(t)
	handler := substringBetween(t, source,
		"func (s *Server) handleControlCodeCaptureHTTP(",
		"func (s *Server) confirmControlCodeBrowserCapture(")

	for _, forbidden := range []string{
		"controlCodeBrowserCaptureWait",
		"timeoutControlCodeBrowserCapture",
		"failControlCodeBrowserCapture",
		"browser_capture_expired",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("control-code browser capture must not have timer-driven cleanup, found %q", forbidden)
		}
	}
	if !strings.Contains(handler, "go s.sendControlCodeBrowserCaptureAckUntilCleanup(") {
		t.Fatalf("capture endpoint must acknowledge the browser before persistently delivering phone ack in the background")
	}
	if strings.Contains(handler, "s.sendControlCodeBrowserCaptureAck(") {
		t.Fatalf("capture endpoint must not block the browser response on a direct phone ack send")
	}
	if strings.Contains(handler, "timeoutControlCodeCleanup(") {
		t.Fatalf("capture endpoint must not synthesize cleanup failure while the phone may still be waiting for browser ack")
	}
	ackLoop := substringBetween(t, source,
		"func (s *Server) sendControlCodeBrowserCaptureAckUntilCleanup(",
		"func (s *Server) closeControlCodeRequest(")
	if !strings.Contains(ackLoop, "controlCodeBrowserCaptureAckStillNeeded(requestID)") ||
		!strings.Contains(ackLoop, "for attempt := 1; ; attempt++") {
		t.Fatalf("phone ack delivery must repeat until Pixel cleanup is observed")
	}
}

func TestControlCodePostConfirmationStressAllowsWaitingModalButRejectsEarlyCapture(t *testing.T) {
	source := ticketAppSource(t)
	maybeCapture := substringBetween(t, source,
		"function maybeCaptureControlCodeResultImage() {",
		"  function waitForControlCodeResultScreenshot(request) {")
	waitForScreenshot := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")

	for _, needle := range []string{
		"function controlCodeRenderedFrameEpoch()",
		"function controlCodeRenderedFrameSequence()",
		"if (hasRenderedFrame && currentStreamEpoch) return currentStreamEpoch;",
		"if (hasRenderedFrame && lastAcceptedFrameSequence) return lastAcceptedFrameSequence;",
		"const renderedEpoch = controlCodeRenderedFrameEpoch();",
		"const renderedSequence = controlCodeRenderedFrameSequence();",
		"return renderedEpoch === markerEpoch && renderedSequence >= markerSequence;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code capture marker gate must use rendered-frame metadata with accepted-frame fallback, missing %q", needle)
		}
	}
	gateIndex := strings.Index(maybeCapture, "if (!controlCodeMarkerReady(codeRequest)) {")
	waitingIndex := strings.Index(maybeCapture, "noteControlCodeMarkerWaiting(codeRequest);")
	proofIndex := strings.Index(maybeCapture, "const proof = controlCodeCandidateFrameProof(codeRequest);")
	captureIndex := strings.Index(maybeCapture, "captureControlCodeResultScreenshot(codeRequest, proof);")
	if gateIndex < 0 || waitingIndex < 0 || proofIndex < 0 || captureIndex < 0 || gateIndex > waitingIndex || waitingIndex > proofIndex || proofIndex > captureIndex {
		t.Fatalf("control-code capture must stay behind marker readiness and browser frame proof even when the waiting modal is visible")
	}
	if !strings.Contains(waitForScreenshot, "codeResultArea.dataset.status = 'waiting';") ||
		!strings.Contains(waitForScreenshot, "setControlCodeResultVisible(false);") {
		t.Fatalf("post-confirmation path should arm capture while keeping the waiting state visually quiet")
	}
	if strings.Contains(waitForScreenshot, "setControlCodeResultVisible(true);") ||
		strings.Contains(waitForScreenshot, "Gaida koda attēlu") {
		t.Fatalf("post-confirmation waiting path must not show interim waiting text or overlay")
	}

	badCaptures := 0
	directCaptureBeforeMarker := strings.Contains(waitForScreenshot, "captureControlCodeResultImage(") || gateIndex < 0 || captureIndex < 0 || gateIndex > captureIndex
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

func TestControlCodeResultCaptureRequiresBrowserFrameProof(t *testing.T) {
	source := ticketAppSource(t)
	maybeCapture := substringBetween(t, source,
		"function maybeCaptureControlCodeResultImage() {",
		"  function waitForControlCodeResultScreenshot(request) {")
	captureScreenshot := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request",
		"  function failControlCodeResultScreenshotWait() {")
	debugPublisher := substringBetween(t, source,
		"function publishStreamDebug() {",
		"  function readUint64(view, offset) {")

	for _, needle := range []string{
		"let lastControlCodeCaptureDebug = null;",
		"function controlCodeCandidateFrameProof(request)",
		"result_window_closed_before_capture",
		"frame_before_marker",
		"candidate_matches_pre_request_frame",
		"control_popup_keyboard_frame",
		"generated_frame_not_visible",
		"candidate_frame_at_or_after_phone_marker_and_generated_visual",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code freeze proof missing %q", needle)
		}
	}
	candidateProof := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")
	for _, needle := range []string{
		"const candidateFingerprint = canvasRegionFingerprint(controlCodeFingerprintRegion());",
		"const difference = fingerprintDifferenceScore(controlCodeBaselineFrameFingerprint, candidateFingerprint);",
		"const popupProof = controlCodePopupFrameProof();",
		"if (popupProof.popupVisible)",
		"const generatedProof = controlCodeGeneratedFrameProof();",
		"if (!generatedProof.generatedVisible)",
		"candidateFrameEpoch: controlCodeRenderedFrameEpoch()",
		"candidateFrameSequence: controlCodeRenderedFrameSequence()",
		"const renderedEpoch = controlCodeRenderedFrameEpoch();",
		"const renderedSequence = controlCodeRenderedFrameSequence();",
	} {
		if !strings.Contains(candidateProof, needle) {
			t.Fatalf("candidate frame proof must reject stale/popup frames before capture, missing %q", needle)
		}
	}
	generatedProof := substringBetween(t, source,
		"function controlCodeGeneratedFrameProof() {",
		"  function rememberControlCodeBaselineFrame(requestID) {")
	for _, needle := range []string{
		"const chip = controlCodeResultChipProof();",
		"generatedVisible: chip.chipVisible && generatedCodeVisible",
		"generatedChipRows: chip.chipRows",
	} {
		if !strings.Contains(generatedProof, needle) {
			t.Fatalf("generated frame proof must require the generated control-code chip, missing %q", needle)
		}
	}
	acceptIndex := strings.Index(candidateProof, "proof.acceptedReason = 'candidate_frame_at_or_after_phone_marker_and_generated_visual';")
	generatedIndex := strings.Index(candidateProof, "if (!generatedProof.generatedVisible)")
	if acceptIndex < 0 || generatedIndex < 0 || generatedIndex > acceptIndex {
		t.Fatalf("browser-generated candidate frame must prove generated visuals before fallback acceptance")
	}
	for _, needle := range []string{
		"const proof = controlCodeCandidateFrameProof(codeRequest);",
		"if (!proof.accepted) {",
		"noteControlCodeCandidateRejected(proof);",
		"return false;",
		"captureControlCodeResultScreenshot(codeRequest, proof);",
		"return true;",
	} {
		if !strings.Contains(maybeCapture, needle) {
			t.Fatalf("capture path must require accepted browser frame proof, missing %q", needle)
		}
	}
	if strings.Contains(maybeCapture, "return captureControlCodeResultScreenshot(codeRequest);") {
		t.Fatalf("marker-only capture must not be able to snapshot the canvas")
	}
	for _, needle := range []string{
		"proof.accepted",
		"lastControlCodeCaptureDebug",
		"candidateAccepted",
		"capturedAt",
	} {
		if !strings.Contains(captureScreenshot, needle) {
			t.Fatalf("successful capture must publish browser proof debug, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"lastRenderedFrameEpoch",
		"lastRenderedFrameSequence",
		"lastRenderedFrameTimestamp",
		"controlCodeCapture: lastControlCodeCaptureDebug",
	} {
		if !strings.Contains(debugPublisher, needle) {
			t.Fatalf("stream debug must expose browser frame/capture proof state, missing %q", needle)
		}
	}
}

func TestControlCodeResultCaptureUsesStreamSizeFrozenFrame(t *testing.T) {
	source := ticketAppSource(t)
	captureImage := substringBetween(t, source,
		"function captureControlCodeResultImage(proof) {",
		"  async function captureControlCodeResultScreenshot(request, proof) {")
	captureScreenshot := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request",
		"  function failControlCodeResultScreenshotWait() {")

	if strings.Contains(source, "function controlCodeResultCaptureRegion(") {
		t.Fatalf("browser result must not compute a crop region for the normal requester image")
	}
	for _, needle := range []string{
		"const captureCanvas = document.createElement('canvas');",
		"const sourceCanvas = controlCodeFrozenCandidateFrameForProof(proof);",
		"captureCanvas.width = sourceCanvas.width;",
		"captureCanvas.height = sourceCanvas.height;",
		"captureContext.imageSmoothingEnabled = false;",
		"captureContext.drawImage(sourceCanvas, 0, 0, captureCanvas.width, captureCanvas.height);",
		"return captureCanvas.toDataURL('image/png');",
	} {
		if !strings.Contains(captureImage, needle) {
			t.Fatalf("browser result full-frame image missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"region.sx",
		"region.sy",
		"region.sw",
		"region.sh",
		"const scale =",
		"const dx =",
		"const dy =",
	} {
		if strings.Contains(captureImage, forbidden) {
			t.Fatalf("browser result must not crop or scale the frozen stream frame, found %q", forbidden)
		}
	}
	if strings.Contains(captureScreenshot, "canvas.toDataURL('image/png')") {
		t.Fatalf("requester result must use the frozen proven stream frame, not the live canvas directly")
	}
	if !strings.Contains(captureScreenshot, "const capturedImage = captureControlCodeResultImage(proof);") {
		t.Fatalf("requester result must use the browser-local full-frame image")
	}
}

func TestControlCodeCaptureRejectsPopupFadeAndRequiresStableFrames(t *testing.T) {
	source := ticketAppSource(t)
	candidateProof := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")
	popupProof := substringBetween(t, source,
		"function controlCodePopupFrameProof() {",
		"  function controlCodeResultChipProof() {")
	debugPublisher := substringBetween(t, source,
		"function publishStreamDebug() {",
		"  function readUint64(view, offset) {")

	for _, needle := range []string{
		"dialogGhostVisible",
		"dimOverlayVisible",
		"unsafeOverlayVisible",
		"const popupKeyboardVisible = dialogVisible && keyboardVisible;",
		"popupVisible: dialogVisible && (okButtonVisible || inputLineVisible)",
		"unsafeOverlayVisible: popupVisible || popupKeyboardVisible || dialogGhostVisible || (dimOverlayVisible && (popupVisible || dialogGhostVisible || popupKeyboardVisible))",
	} {
		if !strings.Contains(popupProof, needle) {
			t.Fatalf("popup proof must expose fade/ghost overlay rejection, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"proof.popupGhostVisible = popupProof.dialogGhostVisible;",
		"proof.dimOverlayVisible = popupProof.dimOverlayVisible;",
		"proof.unsafeOverlayVisible = popupProof.unsafeOverlayVisible;",
		"if (popupProof.unsafeOverlayVisible)",
		"control_popup_fade_frame",
		"const safeFrameCount = noteControlCodeSafeGeneratedFrame(proof);",
		"if (safeFrameCount < controlCodeSafeGeneratedFrameRequiredCount)",
		"generated_frame_not_stable",
		"freezeControlCodeCandidateFrame(proof)",
	} {
		if !strings.Contains(candidateProof, needle) {
			t.Fatalf("candidate frame proof must reject popup fade and require stable frames, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"controlCodeSafeGeneratedFrameCount",
		"controlCodeFrozenFrameKey",
		"popupGhostVisible",
		"unsafeOverlayVisible",
		"capturedNaturalWidth",
		"capturedNaturalHeight",
	} {
		if !strings.Contains(source, needle) && !strings.Contains(debugPublisher, needle) {
			t.Fatalf("capture debug must expose stable/frozen-frame state, missing %q", needle)
		}
	}
}

func TestControlCodeCaptureDoesNotTrustPhonePostSubmitProofAlone(t *testing.T) {
	source := ticketAppSource(t)
	candidateProof := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")

	for _, needle := range []string{
		"function controlCodeTrustedPhonePostSubmitProof(resultProof) {",
		"resultProof === 'phone_visual_raw_ticket_after_submit'",
		"resultProof === 'phone_visual_root_confirmed'",
		"const trustedPhonePostSubmitProof = controlCodeTrustedPhonePostSubmitProof(proof.resultProof);",
		"if (trustedPhonePostSubmitProof) {",
		"proof.trustedPhonePostSubmitProof = true;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("post-submit phone proof diagnostic path missing %q", needle)
		}
	}
	if strings.Contains(candidateProof, "proof.acceptedReason = `candidate_frame_at_or_after_${proof.resultProof}`;") ||
		strings.Contains(candidateProof, "proof.accepted = true;\n      proof.candidateAccepted = true") {
		t.Fatalf("phone post-submit proof must not bypass browser generated-screen proof")
	}

	popupRejectIndex := strings.Index(candidateProof, "if (popupProof.popupVisible)")
	trustedDiagnosticIndex := strings.Index(candidateProof, "if (trustedPhonePostSubmitProof) {")
	generatedRejectIndex := strings.Index(candidateProof, "if (!generatedProof.generatedVisible)")
	if popupRejectIndex < 0 || trustedDiagnosticIndex < 0 || generatedRejectIndex < 0 {
		t.Fatalf("candidate proof must keep popup rejection, phone proof diagnostics, and generated visual enforcement")
	}
	if popupRejectIndex > trustedDiagnosticIndex || trustedDiagnosticIndex > generatedRejectIndex {
		t.Fatalf("phone proof diagnostics must happen after popup rejection and before generated visual enforcement")
	}
}

func TestControlCodeGeneratedProofScansLowerResultStrip(t *testing.T) {
	source := ticketAppSource(t)
	generatedProof := substringBetween(t, source,
		"function controlCodeGeneratedFrameProof() {",
		"  function rememberControlCodeBaselineFrame(requestID) {")
	chipProof := substringBetween(t, source,
		"function controlCodeResultChipProof() {",
		"  function controlCodeGeneratedFrameProof() {")
	candidateProof := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")

	for _, needle := range []string{
		"const controlCodeGeneratedChipScanStartY = 0.50;",
		"const controlCodeGeneratedChipScanEndY = 0.61;",
		"const controlCodeGeneratedChipScanStepY = 0.01;",
		"for (let yRatio = controlCodeGeneratedChipScanStartY;",
		"sampleControlCodeResultChipRegion(yRatio)",
		"candidate.chipScore > bestChip.chipScore",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("generated chip proof must scan the real lower result strip, missing %q", needle)
		}
	}
	if strings.Contains(chipProof, "Math.round(canvas.height * 0.47)") {
		t.Fatalf("generated chip proof must not depend on the old fixed upper strip y=0.47")
	}
	if !strings.Contains(generatedProof, "y: Math.max(0.12, chip.chipY - 0.34)") {
		t.Fatalf("generated code-area proof must be anchored to the detected result strip")
	}
	for _, needle := range []string{
		"proof.generatedChipY = generatedProof.generatedChipY;",
		"proof.generatedChipScore = generatedProof.generatedChipScore;",
		"proof.generatedCodeScore = generatedProof.generatedCodeScore;",
	} {
		if !strings.Contains(candidateProof, needle) {
			t.Fatalf("candidate proof debug must expose generated proof details, missing %q", needle)
		}
	}
}

func TestControlCodePopupProofTargetsCenteredEntryDialog(t *testing.T) {
	source := ticketAppSource(t)
	popupProof := substringBetween(t, source,
		"function controlCodePopupFrameProof() {",
		"  function controlCodeResultChipProof() {")

	for _, needle := range []string{
		"const dialog = canvasRegionFingerprint({",
		"y: 0.38",
		"height: 0.22",
		"const okButton = canvasRegionFingerprint({",
		"x: 0.64",
		"y: 0.51",
		"function regionOrangeCellRatio(region) {",
		"okButtonOrangeRatio",
		"okButtonVisible",
		"popupVisible: dialogVisible && (okButtonVisible || inputLineVisible)",
		"dialogGhostVisible",
		"dialog.darkCellRatio <= 0.30",
		"dialog.contrastScore <= 106",
		"dimOverlayVisible",
		"unsafeOverlayVisible",
	} {
		if !strings.Contains(popupProof, needle) {
			t.Fatalf("popup proof must target the centered ViVi entry dialog and OK button, missing %q", needle)
		}
	}
	if strings.Contains(popupProof, "y: 0.22") {
		t.Fatalf("popup proof must not only sample the old upper-ticket band; it missed the actual centered entry popup")
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
		"} else if (ws.readyState === WebSocket.OPEN) {\n      send(heartbeatMessage(reason));",
		"if (!videoWs || videoWs.readyState === WebSocket.CLOSED || videoWs.readyState === WebSocket.CLOSING) {\n      connectDirectVideo();",
		"if (videoStale) {\n      setTimeout(() => {",
	}
	for _, needle := range requiredRecoverySnippets {
		if !strings.Contains(recoveryBody, needle) {
			t.Fatalf("resume recovery missing bounded behavior %q", needle)
		}
	}
}

func TestTicketViewerDisconnectsAfterIdleTimeoutUntilReload(t *testing.T) {
	source := ticketAppSource(t)
	expireBody := substringBetween(t, source,
		"function expireViewerIdle(reason) {",
		"  function layoutViewportRect() {")
	connectBody := substringBetween(t, source,
		"function connect() {",
		"  function resetStreamState(options) {")
	videoBody := substringBetween(t, source,
		"function connectDirectVideo() {",
		"  function sendVideoSignal(value) {")
	recoveryBody := substringBetween(t, source,
		"function recoverAfterVisibilityResume(reason) {",
		"  window.addEventListener('resize', resizeCanvasBox);")

	for _, needle := range []string{
		"const idleDisconnectMs = 15 * 60 * 1000;",
		"let idleDisconnected = false;",
		"let idleDisconnectTimer = null;",
		"const activeVideoSockets = new Set();",
		"function noteViewerActivity(event, reason) {",
		"function scheduleViewerIdleDisconnect(reason) {",
		"function closeEarlyVideo(reason) {",
		"function claimEarlyVideoSocket() {",
		"claimEarlyVideoSocket()",
		"closeEarlyVideo('pagehide');",
		"for (const eventName of ['pointerdown', 'touchend', 'click', 'keydown', 'scroll', 'focus'])",
		"document.addEventListener('visibilitychange'",
		"noteViewerActivity(null, 'visibility_visible');",
		"startStreamButton.addEventListener('click'",
		"location.reload();",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("idle cutoff source missing %q", needle)
		}
	}
	for _, needle := range []string{
		"idleDisconnected = true;",
		"clearTimeout(reconnectTimer);",
		"closeEarlyVideo('idle_disconnect');",
		"closeDirectVideo();",
		"resetStreamState({ preserveFrame: true });",
		"ws.close();",
		"spacetimeClient.close();",
		"releaseScreenWakeLock('idle_disconnect');",
		"showEmpty('Straume ir apturēta pēc 15 minūtēm bez darbības. Pārlādē lapu, lai turpinātu.', true);",
		"document.body.dataset.streamFreshness = 'IDLE_DISCONNECTED';",
		"setConnected('Apturēts');",
	} {
		if !strings.Contains(expireBody, needle) {
			t.Fatalf("idle expiry must close connections and show reload state, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"activeVideoSockets.add(socket);",
		"const sockets = new Set(activeVideoSockets);",
		"activeVideoSockets.delete(socket);",
		"if (idleDisconnected || videoWs !== socket) return;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("video sockets must be tracked so idle can close all streams, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"if (idleDisconnected) return;",
	} {
		if !strings.Contains(connectBody, needle) ||
			!strings.Contains(videoBody, needle) ||
			!strings.Contains(recoveryBody, needle) {
			t.Fatalf("idle cutoff must block reconnect paths, missing %q", needle)
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

func controlCodeServerSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("control_code.go")
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
