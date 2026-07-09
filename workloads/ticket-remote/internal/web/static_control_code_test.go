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

	captureVisibleIndex := strings.Index(captureScreenshot, "displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');")
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
	if ackIndex < 0 || captureIndex > captureVisibleIndex || captureVisibleIndex > ackIndex {
		t.Fatalf("captured image must be locally snapped, revealed, then browser-acked")
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

func TestTrustedPhoneMarkerFrameCannotBypassBrowserGeneratedDetector(t *testing.T) {
	source := ticketAppSource(t)
	candidateBody := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function controlCodePreparedProofUsable(request, proof) {")

	markerGuardIndex := strings.Index(candidateBody, "if (markerEpoch && markerSequence && (renderedEpoch !== markerEpoch || renderedSequence < markerSequence))")
	fallbackIndex := strings.Index(candidateBody, "const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof")
	rejectIndex := strings.Index(candidateBody, "if (!proof.browserTrustedGeneratedVisible) {")
	rejectedIndex := strings.Index(candidateBody, "proof.generatedMarkerOnlyRejected = true;")
	if markerGuardIndex < 0 || fallbackIndex < 0 || rejectIndex < 0 || rejectedIndex < 0 {
		t.Fatalf("trusted phone marker-frame rejection diagnostics are missing")
	}
	if markerGuardIndex > fallbackIndex {
		t.Fatalf("trusted phone marker-frame diagnostics must run only after the frame-at-or-after-marker guard")
	}
	if fallbackIndex > rejectIndex {
		t.Fatalf("trusted phone marker-frame diagnostics must happen before generated-frame rejection")
	}
	if strings.Contains(candidateBody, "? 'trusted_phone_marker_frame'") ||
		strings.Contains(candidateBody, "proof.generatedVisibleByPhoneMarker = true;") ||
		strings.Contains(candidateBody[fallbackIndex:rejectIndex], "proof.browserTrustedGeneratedVisible = true;") {
		t.Fatalf("trusted phone marker-frame must not bypass browser generated-frame proof")
	}
	for _, needle := range []string{
		"renderedEpoch === markerEpoch",
		"renderedSequence >= markerSequence",
		"(request.status === 'succeeded' || allowProvisional)",
		"proof.generatedMarkerOnlyRejected = true;",
	} {
		if !strings.Contains(candidateBody[fallbackIndex:rejectIndex], needle) {
			t.Fatalf("trusted phone marker-frame rejection missing guard %q", needle)
		}
	}
}

func TestBrowserPublicOwnerIdMatchesRustSpacetimeModule(t *testing.T) {
	source := ticketAppSource(t)
	client := readTicketWebClientSource(t, "src/index.ts")

	for _, fixture := range []struct {
		name string
		body string
		end  string
	}{
		{name: "page", body: source, end: "function clientLog"},
		{name: "client", body: client, end: "function tableRows"},
	} {
		accountPublicID := substringBetween(t, fixture.body,
			"function accountPublicId(email",
			fixture.end)
		if !strings.Contains(accountPublicID, "hash.toString(36).padStart(4") ||
			!strings.Contains(accountPublicID, ".slice(0, 4)") {
			t.Fatalf("%s accountPublicId must match the Rust Spacetime account_public_id shape", fixture.name)
		}
		for _, forbidden := range []string{
			"ABCDEFGHIJKLMNOPQRSTUVWXYZ",
			"36 * 36 * 36 * 36",
			"hash %",
		} {
			if strings.Contains(accountPublicID, forbidden) {
				t.Fatalf("%s accountPublicId must not use the old modulo/uppercase owner id shape, found %q", fixture.name, forbidden)
			}
		}
	}
}

func TestControlCodeCaptureRetriesAreBounded(t *testing.T) {
	source := ticketAppSource(t)
	rejectedBody := substringBetween(t, source,
		"function noteControlCodeCandidateRejected(proof) {",
		"  async function confirmControlCodeBrowserCapture(request, proof) {")
	waitForScreenshot := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")
	resultWaitRetry := substringBetween(t, source,
		"function maybeRequestControlCodeResultWaitKeyframe(requestID, reason) {",
		"  function waitForControlCodeResultScreenshot(request) {")
	keyframeBody := substringBetween(t, source,
		"function requestKeyframe(reason, force) {",
		"  function requestKeyframeDebounced(reason, minIntervalMs, force) {")

	for _, needle := range []string{
		"const controlCodeCapturePollMs = 100;",
		"const controlCodeResultInitialKeyframeDelayMs = 1200;",
		"const controlCodeCaptureKeyframeRetryMs = 5000;",
		"const controlCodeCaptureKeyframeRetryLimit = 2;",
		"const keyframeCommandMinIntervalMs = 2500;",
		"let lastKeyframeCommandAt = 0;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code capture throttling missing %q", needle)
		}
	}
	for _, needle := range []string{
		"lastControlCodeCaptureKeyframeRetryCount < controlCodeCaptureKeyframeRetryLimit",
		"requestKeyframeDebounced(`control_code_candidate_rejected_${reason}`, controlCodeCaptureKeyframeRetryMs)",
		"lastControlCodeCaptureKeyframeRetryCount += 1;",
	} {
		if !strings.Contains(rejectedBody, needle) {
			t.Fatalf("candidate rejection must bound keyframe retry writes, missing %q", needle)
		}
	}
	if !strings.Contains(waitForScreenshot, "setTimeout(tick, controlCodeCapturePollMs)") {
		t.Fatalf("control-code capture polling must use the named bounded interval")
	}
	if strings.Contains(waitForScreenshot, "requestKeyframeDebounced('control_code_result_wait_start', controlCodeCaptureKeyframeRetryMs)") {
		t.Fatalf("control-code result wait must not immediately request a keyframe before trying the rendered marker frame")
	}
	for _, needle := range []string{
		"if (maybeCaptureControlCodeResultImage()) return;",
		"maybeRequestControlCodeResultWaitKeyframe(requestID, 'control_code_result_wait_retry');",
	} {
		if !strings.Contains(waitForScreenshot, needle) {
			t.Fatalf("control-code result wait must try capture first and then arm delayed retry, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"now - controlCodeResultCaptureStartedAt < controlCodeResultInitialKeyframeDelayMs",
		"lastControlCodeCaptureKeyframeRetryCount >= controlCodeCaptureKeyframeRetryLimit",
		"requestKeyframeDebounced(reason || 'control_code_result_wait_retry', controlCodeCaptureKeyframeRetryMs)",
		"lastControlCodeCaptureKeyframeRetryCount += 1;",
	} {
		if !strings.Contains(resultWaitRetry, needle) {
			t.Fatalf("control-code result retry helper must delay and bound extra keyframes, missing %q", needle)
		}
	}
	if strings.Contains(waitForScreenshot, "setTimeout(tick, 20)") {
		t.Fatalf("control-code capture polling must not run every 20ms")
	}
	for _, needle := range []string{
		"if (!force && now - lastKeyframeCommandAt < keyframeCommandMinIntervalMs) return false;",
		"lastKeyframeCommandAt = now;",
		"return true;",
	} {
		if !strings.Contains(keyframeBody, needle) {
			t.Fatalf("keyframe command helper must globally throttle writes, missing %q", needle)
		}
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

func TestTicketViewerSendsVideoSocketOpenContext(t *testing.T) {
	source := ticketAppSource(t)
	template := ticketIndexTemplate(t)
	streamURLBody := substringBetween(t, source,
		"function streamURL(reason) {",
		"  function setConnected(text) {")
	connectBody := substringBetween(t, source,
		"function connectDirectVideo(options) {",
		"  function noteVideoSocketOpen(socket, reason) {")

	for _, needle := range []string{
		"appendStreamURLParam(url, 'page_version', pageVersion);",
		"appendStreamURLParam(url, 'asset_version', assetVersion);",
		"appendStreamURLParam(url, 'visibility', document.visibilityState);",
		"appendStreamURLParam(url, 'restore_reason', videoOpenRestoreReason(reason));",
		"appendStreamURLParam(url, 'recovery_id', activeVideoRecoveryID());",
		"appendStreamURLParam(url, 'frame_age_ms', lastFrameAt > 0 ? Math.round(now - lastFrameAt) : '');",
		"appendStreamURLParam(url, 'has_frame', hasRenderedFrame ? '1' : '0');",
		"appendStreamURLParam(url, 'configured', configured ? '1' : '0');",
		"appendStreamURLParam(url, 'open_seq', videoSocketOpenSeq);",
	} {
		if !strings.Contains(streamURLBody, needle) {
			t.Fatalf("stream URL context missing %q", needle)
		}
	}
	if !strings.Contains(connectBody, "videoSocketOpenSeq += 1;") ||
		!strings.Contains(connectBody, "safeWebSocket(streamURL('connect_direct_video'), 'video')") {
		t.Fatalf("direct video socket must attach current open context")
	}
	for _, needle := range []string{
		"options = options || {};",
		"if (options.skipEarlyGrace)",
		"clientLog('early_video_connecting_grace_skipped', 'fast_resume');",
		"closeEarlyVideo('fast_resume');",
	} {
		if !strings.Contains(connectBody, needle) {
			t.Fatalf("fast restored-page video open missing %q", needle)
		}
	}
	for _, needle := range []string{
		`url.searchParams.set("page_version"`,
		`url.searchParams.set("asset_version"`,
		`url.searchParams.set("visibility"`,
		`url.searchParams.set("restore_reason", "early_video_socket")`,
		`url.searchParams.set("has_frame", "0")`,
		`url.searchParams.set("configured", "0")`,
		`new WebSocket(streamURL())`,
	} {
		if !strings.Contains(template, needle) {
			t.Fatalf("early video socket context missing %q", needle)
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
		"function controlCodeCaptureTrace(event, request, proof, detail)",
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
	for _, needle := range []string{
		"control_code_marker_received",
		"control_code_marker_frame_waiting",
		"control_code_candidate_rejected",
		"control_code_candidate_accepted",
		"control_code_frame_frozen",
		"control_code_frame_displayed",
		"control_code_browser_capture_ack_sent",
		"control_code_frame_retry_requested",
		"requestKey: requestID ? accountPublicId(requestID) : ''",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code capture observability missing %q", needle)
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
		"const browserTrustedGeneratedVisible = generatedProof.generatedVisible ||",
		"const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof",
		"if (!browserTrustedGeneratedVisible && trustedPhoneMarkerFrame)",
		"proof.generatedMarkerOnlyRejected = true;",
		"if (!proof.browserTrustedGeneratedVisible)",
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
	markerOnlyRejectIndex := strings.Index(candidateProof, "proof.generatedMarkerOnlyRejected = true;")
	generatedIndex := strings.Index(candidateProof, "if (!proof.browserTrustedGeneratedVisible)")
	if markerOnlyRejectIndex < 0 || generatedIndex < 0 || markerOnlyRejectIndex > generatedIndex {
		t.Fatalf("candidate frame must reject marker-only proof before accepting a generated frame")
	}
	if strings.Contains(candidateProof, "proof.generatedVisibleByPhoneMarker") ||
		strings.Contains(candidateProof, "'trusted_phone_marker_frame'") {
		t.Fatalf("candidate frame must not accept marker-only phone proof")
	}
	baselineRejectIndex := strings.Index(candidateProof, "proof.candidateRejectedReason = 'candidate_matches_pre_request_frame';")
	if baselineRejectIndex < 0 || baselineRejectIndex < generatedIndex {
		t.Fatalf("baseline sameness rejection must run only after generated-screen proof is evaluated")
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
		if !strings.Contains(captureScreenshot, needle) && !strings.Contains(source, needle) {
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

func TestControlCodeBrowserCaptureCanContinueAfterPhoneRawReturn(t *testing.T) {
	source := ticketAppSource(t)
	candidateProof := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")

	for _, forbidden := range []string{
		"request.resultWindowClosedAt ||",
		"request.cleanupFrameEpoch ||",
		"request.cleanupMinFrameSequence",
	} {
		if strings.Contains(candidateProof, forbidden) {
			t.Fatalf("browser capture must not reject just because phone cleanup has begun, found %q", forbidden)
		}
	}
	if !strings.Contains(candidateProof, "if (request.cleanupCompletedAt) {") ||
		!strings.Contains(candidateProof, "proof.candidateRejectedReason = 'result_window_closed_before_capture';") {
		t.Fatalf("browser capture must still reject after cleanup is fully completed")
	}
	cleanupCompletedIndex := strings.Index(candidateProof, "if (request.cleanupCompletedAt)")
	generatedRejectIndex := strings.Index(candidateProof, "if (!proof.browserTrustedGeneratedVisible)")
	if cleanupCompletedIndex < 0 || generatedRejectIndex < 0 || cleanupCompletedIndex > generatedRejectIndex {
		t.Fatalf("cleanup-completed rejection must happen before any generated-frame capture can be accepted")
	}
}

func TestControlCodeCaptureRejectsPopupFadeAndRequiresVerifiedGeneratedFrame(t *testing.T) {
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
		"dialogUpper",
		"inputLineUpper",
		"okButtonUpper",
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
		"const requiredSafeFrameCount = trustedPhonePostSubmitProof ?",
		"controlCodeTrustedProofSafeGeneratedFrameRequiredCount",
		"controlCodeSafeGeneratedFrameRequiredCount",
		"if (safeFrameCount < requiredSafeFrameCount)",
		"generated_frame_not_stable",
		"freezeControlCodeCandidateFrame(proof)",
	} {
		if !strings.Contains(candidateProof, needle) {
			t.Fatalf("candidate frame proof must reject popup fade and require stable frames, missing %q", needle)
		}
	}
	if !strings.Contains(source, "const controlCodeSafeGeneratedFrameRequiredCount = 1;") {
		t.Fatalf("untrusted generated-code capture must accept the first verified generated frame for the sub-5s path")
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

func TestControlCodeRecoveryQueueReasonsArePublicAndVisible(t *testing.T) {
	source := ticketAppSource(t)
	for _, needle := range []string{
		"['waiting_for_ticket_reselect', 'Tālrunis vēl izvēlas biļeti. Uzgaidi mirkli.']",
		"['waiting_for_stream_recovery', 'Tiešraide atjaunojas pirms koda pieprasījuma.']",
		"['control_code_recovery_queue_timeout', 'Tālrunis nepaguva atjaunot biļeti. Mēģini vēlreiz.']",
		"['control_code_stream_unstable', 'Tiešraide nav pietiekami stabila koda pieprasījumam.']",
		"return localizePublicMessage(reason || 'waiting_for_stream_recovery');",
		"return localizePublicMessage(request.reason || request.message || 'waiting_for_stream_recovery');",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code recovery queue source missing %q", needle)
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
		"resultProof === 'phone_visual_root_confirmed'",
		"resultProof === 'phone_visual'",
		"const trustedPhonePostSubmitProof = controlCodeTrustedPhonePostSubmitProof(proof.resultProof);",
		"if (trustedPhonePostSubmitProof) {",
		"proof.trustedPhonePostSubmitProof = true;",
		"const browserTrustedGeneratedVisible = generatedProof.generatedVisible ||",
		"trustedPhonePostSubmitProof &&",
		"generatedProof.generatedChipVisible &&",
		"proof.browserTrustedGeneratedVisible = browserTrustedGeneratedVisible;",
		"const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof",
		"renderedEpoch === markerEpoch",
		"renderedSequence >= markerSequence",
		"(request.status === 'succeeded' || allowProvisional)",
		"if (!browserTrustedGeneratedVisible && trustedPhoneMarkerFrame)",
		"proof.generatedMarkerOnlyRejected = true;",
		"if (!proof.browserTrustedGeneratedVisible)",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("post-submit phone proof diagnostic path missing %q", needle)
		}
	}
	if strings.Contains(candidateProof, "proof.acceptedReason = `candidate_frame_at_or_after_${proof.resultProof}`;") ||
		strings.Contains(candidateProof, "proof.browserTrustedGeneratedVisible = true;") ||
		strings.Contains(candidateProof, "proof.generatedVisibleByPhoneMarker") ||
		strings.Contains(source, "resultProof === 'phone_visual_raw_ticket_after_submit'") {
		t.Fatalf("phone post-submit proof must not bypass browser generated-screen or marker-frame proof")
	}

	popupRejectIndex := strings.Index(candidateProof, "if (popupProof.popupVisible)")
	trustedDiagnosticIndex := strings.Index(candidateProof, "if (trustedPhonePostSubmitProof) {")
	generatedRejectIndex := strings.Index(candidateProof, "if (!proof.browserTrustedGeneratedVisible)")
	if popupRejectIndex < 0 || trustedDiagnosticIndex < 0 || generatedRejectIndex < 0 {
		t.Fatalf("candidate proof must keep popup rejection, phone proof diagnostics, and generated visual enforcement")
	}
	if popupRejectIndex > trustedDiagnosticIndex || trustedDiagnosticIndex > generatedRejectIndex {
		t.Fatalf("phone proof diagnostics must happen after popup rejection and before generated visual enforcement")
	}
	if !strings.Contains(candidateProof, "proof.fingerprintDifferenceScore >= controlCodeFingerprintDifferenceThreshold") ||
		!strings.Contains(candidateProof, "proof.fingerprintChangedCells >= controlCodeFingerprintChangedCellsThreshold") {
		t.Fatalf("phone post-submit proof may assist only when the browser frame changed from the pre-request baseline")
	}
	if strings.Contains(candidateProof, "browserTrustedGeneratedVisible = trustedPhonePostSubmitProof") {
		t.Fatalf("phone post-submit proof must not be the sole generated-frame trust signal")
	}
}

func TestControlCodeDialogLocksBodyScrollInsteadOfRestoringAfterSubmit(t *testing.T) {
	source := ticketAppSource(t)
	css := ticketAppCSS(t)
	openDialog := substringBetween(t, source,
		"function openControlCodeDialog() {",
		"  function closeControlCodeDialog() {")
	closeDialog := substringBetween(t, source,
		"function closeControlCodeDialog() {",
		"  async function submitControlCodeRequest() {")
	updateSubmit := substringBetween(t, source,
		"function updateControlCodeSubmitAvailability() {",
		"  function reconnectVideoForRecovery(reason) {")
	updateReveal := substringBetween(t, source,
		"function updateDetailsReveal() {",
		"  function keepFirstScreenPinned(force) {")
	submitRequest := substringBetween(t, source,
		"async function submitControlCodeRequest() {",
		"  async function closeCurrentControlCode(openNext) {")

	for _, forbidden := range []string{
		"function pinToFirstScreenAfterKeyboardCollapse(",
		"keyboard_restore_complete",
		"keyboardRestoreActive",
		"KEYBOARD_RESTORE_FALLBACK_MS",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("control-code submit jump fix must not use delayed scroll restore: found %q", forbidden)
		}
	}
	for _, needle := range []string{
		"let controlCodeDialogScrollLock = null;",
		"function lockControlCodeDialogScroll() {",
		"function unlockControlCodeDialogScroll() {",
		"function settleCodeDialogScrollUnlock() {",
		"setDetailsPanelVisible(lock.detailsVisible);",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code dialog scroll lock missing %q", needle)
		}
	}
	if !strings.Contains(openDialog, "lockControlCodeDialogScroll();") {
		t.Fatalf("control-code dialog must remember details visibility before focusing the input")
	}
	if !strings.Contains(closeDialog, "document.activeElement.blur();") ||
		!strings.Contains(closeDialog, "settleCodeDialogScrollUnlock();") {
		t.Fatalf("control-code dialog close must blur focused input and release dialog-owned state")
	}
	if !strings.Contains(closeDialog, "updateControlCodeSubmitAvailability();") ||
		!strings.Contains(updateSubmit, "codeSubmit.disabled = unavailable || !codeDialogOpen;") {
		t.Fatalf("control-code submit must be unavailable while the dialog is closed")
	}
	if !strings.Contains(updateSubmit, "requestCodeButton.disabled = unavailable;") {
		t.Fatalf("closed-page request button should remain governed by stream freshness, not dialog-open state")
	}
	if !strings.Contains(updateReveal, "if (controlCodeDialogScrollLock && controlCodeDialogScrollLock.active) return;") {
		t.Fatalf("details reveal must ignore modal-owned scroll while dialog body lock is active")
	}
	if !strings.Contains(source, "const dialogVisible = codeDialogOpen && !codeDialog.hidden;") ||
		!strings.Contains(source, "(inputFocused || dialogVisible)") {
		t.Fatalf("keyboard detection must keep the stage stable even when iOS changes focus timing")
	}
	if strings.Contains(submitRequest, "pinToFirstScreenAfterKeyboardCollapse(") ||
		strings.Contains(submitRequest, "window.scrollTo(0, 0)") ||
		strings.Contains(source, "control-code-dialog-scroll-locked") ||
		strings.Contains(source, "--ticket-dialog-scroll-y") ||
		strings.Contains(source, "window.scrollTo(") {
		t.Fatalf("submit path must not scroll the page back after closing the dialog")
	}
	if strings.Contains(submitRequest, "digits,\n") ||
		strings.Contains(submitRequest, `"digits"`) {
		t.Fatalf("control-code submit log must not include the submitted digits")
	}
	if !strings.Contains(submitRequest, "digitCount: digits.length") {
		t.Fatalf("control-code submit log should keep only the submitted digit count")
	}
	for _, needle := range []string{
		"body.control-code-dialog-scroll-locked",
		"top:var(--ticket-dialog-scroll-y,0)",
	} {
		if strings.Contains(css, needle) {
			t.Fatalf("control-code dialog must not use body fixed scroll-lock CSS: found %q", needle)
		}
	}
	for _, needle := range []string{
		".shell{width:100%;min-height:100dvh",
		".stage-page{width:100%;min-height:100dvh",
		".stage{position:relative;z-index:1;width:100%;min-height:100dvh",
	} {
		if strings.Contains(css, needle) {
			t.Fatalf("ticket stream shell must not follow keyboard-sensitive 100dvh sizing: found %q", needle)
		}
	}
	for _, needle := range []string{
		".shell{width:100%;min-height:var(--ticket-stage-height)",
		".stage-page{width:100%;min-height:var(--ticket-stage-height)",
		".stage{position:relative;z-index:1;width:100%;min-height:var(--ticket-stage-height)",
	} {
		if !strings.Contains(css, needle) {
			t.Fatalf("ticket stream shell must use stable stage sizing, missing %q", needle)
		}
	}
}

func TestControlCodeStateIgnoresExpiredOwnedRequests(t *testing.T) {
	source := ticketAppSource(t)
	latestRequest := substringBetween(t, source,
		"function latestOwnedControlCodeRequest(state) {",
		"  function renderState() {")
	renderState := substringBetween(t, source,
		"function renderState() {",
		"  function rememberServerClock(state) {")

	for _, needle := range []string{
		"function controlCodeRequestExpiryTime(request) {",
		"function controlCodeRequestSortTime(request) {",
		"function controlCodeRequestIsStillRelevant(request) {",
		"return (Date.now() + serverClockSkewMs) <= expiresAt + 1000;",
		".filter((request) => isOwnedControlCodeRequest(request) && controlCodeRequestIsStillRelevant(request))",
		".sort((a, b) => controlCodeRequestSortTime(b) - controlCodeRequestSortTime(a))[0] || null;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code request selection must ignore expired rows and prefer newest valid requests, missing %q", needle)
		}
	}
	if strings.Contains(latestRequest, "requests.find((request) => isOwnedControlCodeRequest(request))") {
		t.Fatalf("control-code request selection must not take the first owned Spacetime row without expiry sorting")
	}
	if !strings.Contains(renderState, "renderControlCodeRequest(controlCodeRequestIsStillRelevant(codeRequest) ? codeRequest : null);") {
		t.Fatalf("render state must clear a locally retained request once it is no longer fresh")
	}
}

func TestSpacetimeClientIncludesControlCodeRequestExpiry(t *testing.T) {
	data, err := os.ReadFile("../../web-client/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if !strings.Contains(source, `expiresAt: String(request.expiresAt || request.expires_at || ""),`) {
		t.Fatalf("direct Spacetime browser state must expose control-code request expiry to the page")
	}
}

func TestControlCodeRequiresLiveSpacetimeBeforeRequest(t *testing.T) {
	source := ticketAppSource(t)
	readiness := substringBetween(t, source,
		"function liveFrameReadyForControlCode() {",
		"  function updateControlCodeSubmitAvailability() {")
	autoPrepare := substringBetween(t, source,
		"function maybeAutoPrepareControlCode(reason) {",
		"  function updateControlCodeSubmitAvailability() {")
	openDialog := substringBetween(t, source,
		"function openControlCodeDialog() {",
		"  function closeControlCodeDialog() {")
	submitRequest := substringBetween(t, source,
		"async function submitControlCodeRequest() {",
		"  async function closeCurrentControlCode(openNext) {")
	hotspot := substringBetween(t, source,
		"function requestControlCodeFromHotspot(event) {",
		"  function closeControlCodeFromHotspot(event) {")

	for _, needle := range []string{
		"function liveFrameReadyForControlCode() {",
		"function spacetimeReadyForControlCode() {",
		"function controlCodeTransportReadyForControlCode() {",
		"function controlCodeFastStateFresh(state) {",
		"function renderControlCodeFastStateDataset() {",
		"function scheduleControlCodeFastStateExpiryCheck() {",
		"controlCodeFastStateExpiryTimer = setTimeout(() => {",
		"refreshControlCodeReadiness('control_code_fast_state_expired');",
		"spacetimeClientStatus === 'live'",
		"return liveFrameReadyForControlCode() && spacetimeReadyForControlCode();",
		"return controlCodeTransportReadyForControlCode() && controlCodeFastStateFresh();",
		"function controlCodeRequestBusyForAutoPrepare() {",
		"status === 'queued' || status === 'running'",
		"status !== 'succeeded'",
		"controlCodePreparedCaptureDisplayedRequestID !== requestID",
		"codeResultArea.hidden",
		"function refreshControlCodeReadiness(reason) {",
		"connectSpacetimeState().catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));",
	} {
		if !strings.Contains(readiness, needle) {
			t.Fatalf("control-code readiness must include live Spacetime state, missing %q", needle)
		}
	}
	if !strings.Contains(source, "const controlCodeAutoPrepareMinIntervalMs = 5000;") {
		t.Fatalf("control-code auto-prepare must have a bounded interval")
	}
	for _, needle := range []string{
		"if (document.visibilityState === 'hidden') return;",
		"if (!codeResultArea.hidden) return;",
		"if (controlCodeAutoPrepareInFlight || !controlCodeTransportReadyForControlCode()) return;",
		"if (controlCodeFastStateFresh()) return;",
		"const busy = controlCodeRequestBusyForAutoPrepare();",
		"now - lastControlCodeAutoPrepareAt < controlCodeAutoPrepareMinIntervalMs",
		"client.prepareControlCode(reason || 'page_ready_control_code')",
	} {
		if !strings.Contains(autoPrepare, needle) {
			t.Fatalf("control-code auto-prepare must be visible-only and debounced, missing %q", needle)
		}
	}
	if !strings.Contains(source, "if (!busy && controlCodeTransportReadyForControlCode() && !controlCodeFastStateFresh())") {
		t.Fatalf("transport-ready but fast-stale control-code page should trigger one debounced prepare")
	}
	if !strings.Contains(source, "let controlCodeFastStateExpiryTimer = null;") ||
		!strings.Contains(source, "scheduleControlCodeFastStateExpiryCheck();") ||
		!strings.Contains(source, "renderControlCodeFastStateDataset();\n    const busy = codeRequest") {
		t.Fatalf("control-code readiness must re-evaluate when a fast-ready lease expires while the dialog is open")
	}
	for _, needle := range []string{
		"const fastRevision = controlCodeFastRevisionForRequest();",
		"client.requestControlCode(digits, fastRevision)",
		"control_code_submit_fast_not_ready",
		"document.body.dataset.controlCodeFastReady = controlCodeFastStateFresh() ? 'true' : 'false';",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code submit must require fresh fast-state revision, missing %q", needle)
		}
	}
	for _, chunk := range []struct {
		name string
		body string
	}{
		{name: "open dialog", body: openDialog},
		{name: "submit", body: submitRequest},
		{name: "hotspot", body: hotspot},
	} {
		if !strings.Contains(chunk.body, "controlCodeReadinessMessage()") ||
			!strings.Contains(chunk.body, "refreshControlCodeReadiness(") {
			t.Fatalf("%s must report centralized control-code readiness and refresh stale state", chunk.name)
		}
	}
}

func TestTicketBrowserLogsStreamTraceBreadcrumbs(t *testing.T) {
	source := ticketAppSource(t)
	for _, needle := range []string{
		"const browserTraceId = accountPublicId(localSessionID || localPublicID || pageVersion);",
		"correlationId: typeof browserTraceId === 'string' ? browserTraceId : ''",
		"clientLog('page_boot'",
		"clientLog('video_socket_connect_attempt'",
		"clientLog('video_socket_opened'",
		"clientLog('video_socket_closed'",
		"clientLog('keyframe_request'",
		"clientLog('stream_recovery_request'",
		"clientLog('visibility_resume_recovery'",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("browser stream trace breadcrumb missing %q", needle)
		}
	}
}

func TestSpacetimeClientReducersWaitForLiveConnection(t *testing.T) {
	data, err := os.ReadFile("../../web-client/src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, needle := range []string{
		"private readyWaiters: Array<{ resolve: () => void; reject: (error: Error) => void; timer: number }> = [];",
		"this.resolveReadyWaiters();",
		"await this.whenReady(5000);",
		"private whenReady(timeoutMs: number): Promise<void> {",
		"reject(new Error(\"Spacetime connection is not ready\"));",
		"private rejectReadyWaiters(error: Error): void {",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("Spacetime client reducers must wait for a live connection, missing %q", needle)
		}
	}
}

func TestControlCodeUsesPixelOwnedSpacetimeCommandFlow(t *testing.T) {
	source := ticketRemoteSourceFile(t, "internal", "web", "control_code.go")
	for _, needle := range []string{
		"appendStreamCommand(ctx, \"generate_control_code\"",
		"appendStreamCommand(ctx, \"control_code_browser_capture\"",
		"appendStreamCommand(ctx, \"control_code_result_ack\"",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code flow must stay Pixel-owned through Spacetime commands, missing %q", needle)
		}
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
		"const dialogUpper = canvasRegionFingerprint({",
		"y: 0.30",
		"const inputLineUpper = canvasRegionFingerprint({",
		"y: 0.41",
		"const okButton = canvasRegionFingerprint({",
		"x: 0.64",
		"y: 0.51",
		"const okButtonUpper = canvasRegionFingerprint({",
		"y: 0.43",
		"function regionOrangeCellRatio(region) {",
		"okButtonOrangeRatio",
		"okButtonVisible",
		"popupVisible: dialogVisible && (okButtonVisible || inputLineVisible)",
		"dialogGhostVisible",
		"dialogProof.darkCellRatio <= 0.30",
		"dialogProof.contrastScore <= 106",
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

	postIndex := strings.Index(closeBody, "client.closeControlCode(requestID, 'browser_closed')")
	closedIndex := strings.Index(closeBody, "locallyClosedControlCodeRequestIDs.add(String(requestID));")
	if postIndex < 0 || closedIndex < 0 {
		t.Fatalf("close path must mark the request closed locally and then sync through Spacetime")
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

func TestControlCodeClosePreventsLateCaptureRedisplay(t *testing.T) {
	source := ticketAppSource(t)
	closeBody := substringBetween(t, source,
		"async function closeCurrentControlCode(openNext) {",
		"  function requestControlCodeFromHotspot(event) {")
	captureBody := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request",
		"  function failControlCodeResultScreenshotWait() {")
	maybeCaptureBody := substringBetween(t, source,
		"function maybeCaptureControlCodeResultImage() {",
		"  function waitForControlCodeResultScreenshot(request) {")
	waitBody := substringBetween(t, source,
		"function waitForControlCodeResultScreenshot(request) {",
		"  function rememberOwnedControlCodeRequest(request) {")

	postIndex := strings.Index(closeBody, "client.closeControlCode(requestID, 'browser_closed')")
	closedIndex := strings.Index(closeBody, "locallyClosedControlCodeRequestIDs.add(String(requestID));")
	codeRequestNilIndex := strings.Index(closeBody, "codeRequest = null;")
	if postIndex < 0 || closedIndex < 0 || codeRequestNilIndex < 0 {
		t.Fatalf("close path must mark a request locally closed and clear the retained request before syncing")
	}
	if closedIndex > codeRequestNilIndex || codeRequestNilIndex > postIndex {
		t.Fatalf("close path must clear local request state before the asynchronous close can race with capture")
	}
	if strings.Count(captureBody, "locallyClosedControlCodeRequestIDs.has(requestID)") < 4 {
		t.Fatalf("browser capture must check for locally closed requests before and after async capture work")
	}
	ackIndex := strings.Index(captureBody, "await confirmControlCodeBrowserCapture(request, proof);")
	revealIndex := strings.Index(captureBody, "displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');")
	if ackIndex < 0 || revealIndex < 0 || revealIndex > ackIndex {
		t.Fatalf("capture path must reveal the safe browser frame before waiting for the ack")
	}
	if !strings.Contains(captureBody[:revealIndex], "if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;") {
		t.Fatalf("capture path must not reveal a result if the request was locally closed first")
	}
	catchIndex := strings.Index(captureBody, "} catch (error) {")
	failIndex := strings.Index(captureBody, "failControlCodeResultScreenshotWait();")
	if catchIndex < 0 || failIndex < 0 || catchIndex > failIndex {
		t.Fatalf("capture failure path no longer shows the waiting failure message")
	}
	if !strings.Contains(captureBody[catchIndex:failIndex], "if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;") {
		t.Fatalf("capture failure path must not redisplay a failed result after local close")
	}
	maybeGuardIndex := strings.Index(maybeCaptureBody, "if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;")
	maybeCaptureIndex := strings.Index(maybeCaptureBody, "captureControlCodeResultScreenshot(codeRequest, proof);")
	if maybeGuardIndex < 0 || maybeCaptureIndex < 0 || maybeGuardIndex > maybeCaptureIndex {
		t.Fatalf("maybe-capture path must ignore locally closed requests before starting screenshot capture")
	}
	waitGuardIndex := strings.Index(waitBody, "if (locallyClosedControlCodeRequestIDs.has(requestID)) return;")
	waitStatusIndex := strings.Index(waitBody, "codeResultArea.dataset.status = 'waiting';")
	if waitGuardIndex < 0 || waitStatusIndex < 0 || waitGuardIndex > waitStatusIndex {
		t.Fatalf("wait path must not re-arm result UI for a locally closed request")
	}
}

func TestControlCodeProvisionalResultStaysVisibleWhileRunning(t *testing.T) {
	source := ticketAppSource(t)
	renderBody := substringBetween(t, source,
		"function renderControlCodeRequest(request) {",
		"  function setDetailsPanelVisible(visible) {")

	for _, needle := range []string{
		"function controlCodeResultDisplayedForRequest(requestID) {",
		"controlCodePreparedCaptureDisplayedRequestID === requestID",
		"!codeResultArea.hidden",
		"!codeResultImage.hidden",
		"Boolean(codeResultImage.currentSrc || codeResultImage.src)",
		"const currentRequestID = String(current && current.requestId || '').trim();",
		"maybePrepareControlCodeResultFrame();",
		"if (controlCodeResultDisplayedForRequest(currentRequestID)) {",
		"scheduleControlCodeTicker(current);",
		"return;",
	} {
		if !strings.Contains(source, needle) && !strings.Contains(renderBody, needle) {
			t.Fatalf("provisional control-code result visibility guard missing %q", needle)
		}
	}
}

func TestTicketViewerRunsBoundedActivationReconnectBurst(t *testing.T) {
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
	restoreBody := substringBetween(t, source,
		"function restoreCachedVideoForFreshFrame(reason, kind) {",
		"  function connect() {")
	resumeWatchdogsBody := substringBetween(t, source,
		"function scheduleResumeWatchdogs(reason) {",
		"  function recoverFreshMediaSession(reason, kind, options) {")
	recoverMediaBody := substringBetween(t, source,
		"function recoverFreshMediaSession(reason, kind, options) {",
		"  function forceFreshVideoResume(reason, kind) {")
	activationBody := substringBetween(t, source,
		"function startActivationResumeFlow(reason, trigger, options) {",
		"  function activationRetryPhase(attempt) {")
	burstBody := substringBetween(t, source,
		"function runActivationReconnectBurst(reason, flow) {",
		"  function scheduleResumeWatchdogs(reason) {")
	chaseBody := substringBetween(t, source,
		"function chaseLiveStream() {",
		"  function recoverAfterVisibilityResume(reason) {")

	for _, needle := range []string{
		"let lastHiddenAt = 0;",
		"let lastHiddenWallAt = 0;",
		"let activeResumeFlow = null;",
		"let activationReconnectBurstTimer = null;",
		"const backgroundRecoveryHiddenMs = 30000;",
		"const oldTabFreshResumeHiddenMs = 5000;",
		"const resumeVideoReconnectDelayMs = 0;",
		"const resumeSoftReconnectMs = 1800;",
		"const resumeHardRecoverMs = 4500;",
		"const activationReconnectBurstMs = 10000;",
		"const activationReconnectFirstRetryMs = 150;",
		"const activationReconnectTickMs = 500;",
		"const activationReconnectMaxTicks = 10;",
		"const activationResumeLogLimit = 32;",
		"let pendingResumeFreshFrameFlow = null;",
		"function resumeDiagnosticSnapshot(detail) {",
		"decoder: decoderStateLabel(),",
		"function mediaSessionStuckOnPreservedFrame() {",
		"if (socketState !== WebSocket.OPEN && socketState !== WebSocket.CONNECTING) return false;",
		"if (!hasRenderedFrame && !fallbackFrameAvailable) return false;",
		"return !configured || !decoderConfigured;",
		"function recoverFreshMediaSession(reason, kind, options) {",
		"function enqueueCompletedResumeSafeLog(flow, event, detail) {",
		"function logResumeCheckpoint(event, detail, flow) {",
		"function finishActivationResumeFlow(reason, flow) {",
		"pendingResumeFreshFrameFlow = {",
		"result: 'late_fresh'",
		"spacetimeClient.appendSafeLog(entry.level || 'info', entry.event || 'client_event', detailJson, entry.correlationId || '')",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("missing visibility resume state/cadence %q", needle)
		}
	}
	if !strings.Contains(visibilityBody, "lastHiddenAt = performance.now();") ||
		!strings.Contains(visibilityBody, "lastHiddenWallAt = Date.now();") ||
		!strings.Contains(visibilityBody, "recoverAfterVisibilityResume('visibility_resume');") ||
		!strings.Contains(visibilityBody, "startActivationResumeFlow('visibility_hidden', 'visibility_hidden', { pauseBurst: true });") ||
		!strings.Contains(visibilityBody, "logResumeCheckpoint('activation_visibility_hidden'") {
		t.Fatalf("visibility changes must record hidden time and resume through the bounded recovery path")
	}
	if !strings.Contains(pageshowBody, "startActivationResumeFlow(event.persisted ? 'pageshow_persisted' : 'pageshow', 'pageshow');") ||
		!strings.Contains(source, "startActivationResumeFlow('focus', 'focus');") ||
		!strings.Contains(source, "startActivationResumeFlow('initial_load', 'initial_load');") {
		t.Fatalf("initial load, pageshow, and focus must all start the bounded activation watcher")
	}
	requiredRecoverySnippets := []string{
		"const hiddenPerfMs = lastHiddenAt > 0 ? now - lastHiddenAt : 0;",
		"const hiddenWallMs = lastHiddenWallAt > 0 ? Date.now() - lastHiddenWallAt : 0;",
		"const hiddenMs = Math.max(hiddenPerfMs, hiddenWallMs);",
		"const longHidden = hiddenMs >= backgroundRecoveryHiddenMs;",
		"const oldHiddenTab = hiddenMs >= oldTabFreshResumeHiddenMs;",
		"const videoStale = configured && (lastFrameAt === 0 || (frameAgeMs !== null && frameAgeMs > streamStaleVideoReconnectMs));",
		"const cacheRestored = reason === 'pageshow_persisted'",
		"const resumeFlow = startActivationResumeFlow(reason || 'visibility_resume', 'visibility_resume', { pauseBurst: true });",
		"logResumeCheckpoint('activation_resume_recovery_decision'",
		"if (longHidden || oldHiddenTab || videoStale || cacheRestored || connectingTooLong) {\n      clientLog('visibility_resume_recovery'",
		"publishCurrentStreamFocus(reason || 'visibility_visible');",
		"restoreCachedVideoForFreshFrame(reason || 'visibility_resume', 'old_tab_resume');",
		"runActivationReconnectBurst(reason || 'visibility_resume', resumeFlow);",
		"scheduleResumeWatchdogs(reason || 'visibility_visible');",
		"if (videoStale) {\n      reconnectVideoForRecovery('visibility_resume_stale');",
	}
	for _, needle := range requiredRecoverySnippets {
		if !strings.Contains(recoveryBody, needle) {
			t.Fatalf("resume recovery missing bounded behavior %q", needle)
		}
	}
	for _, needle := range []string{
		"recoverFreshMediaSession(reason || 'resume', 'resume_hard_recover'",
	} {
		if !strings.Contains(resumeWatchdogsBody, needle) {
			t.Fatalf("stream recovery watchdog must still perform hard recovery, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"recordDevPerfMetric(",
		"randomMetricFlowId(",
		"memberAppendDevPerfMetric",
	} {
		if strings.Contains(resumeWatchdogsBody, needle) {
			t.Fatalf("stream recovery watchdog must not emit removed dev-performance metrics %q", needle)
		}
	}
	for _, needle := range []string{
		"deadlineAt: performance.now() + activationReconnectBurstMs",
		"logResumeCheckpoint('activation_resume_start'",
		"logResumeCheckpoint('activation_resume_merged'",
		"runActivationReconnectBurst(reason || 'activation', flow);",
	} {
		if !strings.Contains(activationBody, needle) {
			t.Fatalf("activation flow missing bounded start/merge behavior %q", needle)
		}
	}
	for _, needle := range []string{
		"if (streamHasFreshRenderedFrame())",
		"logResumeCheckpoint('activation_resume_fresh_frame'",
		"if (flow.attempts >= activationReconnectMaxTicks || now >= flow.deadlineAt)",
		"logResumeCheckpoint('activation_resume_exhausted'",
		"action: mediaSessionStuckOnPreservedFrame() ? 'media_deep_recover'",
		"requestKeyframeDebounced(`${reason || 'activation'}_activation_keyframe`, 0, true);",
		"recoverFreshMediaSession(reason || 'activation', 'activation_resume'",
		"keyframeMinIntervalMs: 0,",
		"forceServerRecovery: mediaSessionStuckOnPreservedFrame()",
		"attempt === 0 ? activationReconnectFirstRetryMs : activationReconnectTickMs",
	} {
		if !strings.Contains(burstBody, needle) {
			t.Fatalf("activation reconnect burst missing %q", needle)
		}
	}
	for _, needle := range []string{
		"logResumeCheckpoint('activation_resume_media_stuck'",
		"logResumeCheckpoint('activation_resume_deep_recover'",
		"closeEarlyVideo(reason || 'media_session_recovery');",
		"preserveCurrentFrame(`fresh_media_session:${reason || 'unknown'}`);",
		"closeDirectVideo();",
		"resetStreamState({ preserveFrame: true });",
		"showStreamRecovery();",
		"beginStreamOpenMetric(kind || 'media_session_recovery', reason || 'resume', true);",
		"connectDirectVideo({ skipEarlyGrace: Boolean(options.skipEarlyGrace) });",
		"requestKeyframeDebounced(options.keyframeReason || `${reason || 'resume'}_fresh_media`, options.keyframeMinIntervalMs || 0, true);",
		"requestServerRecoveryDebounced(options.serverRecoveryReason || `${reason || 'resume'}_fresh_media_recover`, true);",
	} {
		if !strings.Contains(recoverMediaBody, needle) {
			t.Fatalf("fresh media-session recovery helper missing %q", needle)
		}
	}
	for _, needle := range []string{
		"recoverFreshMediaSession(reason || 'cached_resume', kind || 'old_tab_resume'",
		"keyframeReason: `${reason || 'resume'}_cached_keyframe`,",
		"keyframeMinIntervalMs: 0,",
		"serverRecoveryReason: `${reason || 'resume'}_cached_recover`,",
		"forceServerRecovery: false",
		"skipEarlyGrace: true",
	} {
		if !strings.Contains(restoreBody, needle) {
			t.Fatalf("cached-tab restore must aggressively request a fresh frame, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"if (pendingAge > streamStaleDecoderResetMs && mediaSessionStuckOnPreservedFrame())",
		"recoverFreshMediaSession('h264_first_frame_pending', 'first_frame_pending'",
		"keyframeReason: 'h264_first_frame_pending_keyframe'",
		"serverRecoveryReason: 'h264_first_frame_pending_recover'",
		"forceServerRecovery: true",
	} {
		if !strings.Contains(chaseBody, needle) {
			t.Fatalf("first-frame-pending recovery must escalate before long loading stalls, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"const firstFrameServerRecoveryMaxAttempts = 2;",
		"function requestFirstFrameServerRecovery(reason, phase) {",
		"event: 'h264_first_frame_recovery_exhausted'",
		"requestFirstFrameServerRecovery('h264_start_pending', 'unconfigured');",
		"requestFirstFrameServerRecovery('first_frame_server_recover', 'configured');",
		"resetFirstFrameServerRecovery();",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("first-frame-pending server recovery must stay capped, missing %q", needle)
		}
	}
}

func TestTicketViewerCanRecoverAfterIdleTimeoutWithoutReload(t *testing.T) {
	source := ticketAppSource(t)
	expireBody := substringBetween(t, source,
		"function expireViewerIdle(reason) {",
		"  function resumeFromIdleDisconnect(reason) {")
	resumeBody := substringBetween(t, source,
		"function resumeFromIdleDisconnect(reason) {",
		"  function layoutViewportRect() {")
	connectBody := substringBetween(t, source,
		"function connect() {",
		"  function resetStreamState(options) {")
	videoBody := substringBetween(t, source,
		"function connectDirectVideo(options) {",
		"  function sendVideoClientLog(event, detail) {")
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
		"if (event && event.persisted) {",
		"publishStreamFocus(false, 'pagehide_cached');",
		"for (const eventName of ['pointerdown', 'touchend', 'click', 'keydown', 'scroll', 'focus'])",
		"document.addEventListener('visibilitychange'",
		"noteViewerActivity(null, 'visibility_visible');",
		"startStreamButton.addEventListener('click'",
		"resumeFromIdleDisconnect('manual_start');",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("idle cutoff source missing %q", needle)
		}
	}
	for _, needle := range []string{
		"if (document.visibilityState === 'visible') {",
		"scheduleViewerIdleDisconnect('visible_idle_keepalive');",
		"recoverAfterVisibilityResume('visible_idle_keepalive');",
		"idleDisconnected = true;",
		"clearTimeout(reconnectTimer);",
		"clearTimeout(hiddenStreamFocusTimer);",
		"closeEarlyVideo('idle_disconnect');",
		"closeDirectVideo();",
		"resetStreamState({ preserveFrame: true });",
		"spacetimeClient.close();",
		"releaseScreenWakeLock('idle_disconnect');",
		"showEmpty('Straume ir apturēta pēc 15 minūtēm bez darbības. Pieskaries Sākt, lai turpinātu.', true);",
		"document.body.dataset.streamFreshness = 'IDLE_DISCONNECTED';",
		"setConnected('Apturēts');",
	} {
		if !strings.Contains(expireBody, needle) {
			t.Fatalf("idle expiry must close connections and show reload state, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"idleDisconnected = false;",
		"setStatus('Atjauno tiešraidi...');",
		"beginStreamOpenMetric('old_tab_resume'",
		"connectSpacetimeState().catch",
		"publishCurrentStreamFocus(reason || 'idle_resume');",
		"connectDirectVideo();",
		"requestServerRecoveryDebounced(`${reason || 'idle_resume'}_recover`, true);",
		"clientLog('viewer_idle_resumed', reason || 'idle_resume');",
	} {
		if !strings.Contains(resumeBody, needle) {
			t.Fatalf("idle resume must restore the stream without reload, missing %q", needle)
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
			!strings.Contains(videoBody, needle) {
			t.Fatalf("idle cutoff must block reconnect paths, missing %q", needle)
		}
	}
	if !strings.Contains(recoveryBody, "resumeFromIdleDisconnect(reason || 'visibility_resume');") {
		t.Fatalf("visibility resume must recover an idle-disconnected tab")
	}
}

func TestFirstRenderedFrameIsSentOverVideoSocket(t *testing.T) {
	source := ticketAppSource(t)
	for _, needle := range []string{
		"function sendVideoSocketClientLog(event, detail) {",
		"videoWs.send(JSON.stringify({",
		"type: 'client_log'",
		"event: String(event || 'client_log').slice(0, 96)",
		"sendVideoSocketClientLog('stream_first_rendered_frame', firstFrameDetail);",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("first rendered frame must be sent over the video socket, missing %q", needle)
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

func readTicketWebClientSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile("../../web-client/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ticketAppCSS(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func ticketIndexTemplate(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("static/index.html.tmpl")
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
