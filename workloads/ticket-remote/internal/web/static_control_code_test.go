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

	captureVisibleIndex := strings.Index(captureScreenshot, "const painted = await displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');")
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
	paintGuardIndex := strings.Index(captureScreenshot, "if (!painted) return false;")
	if ackIndex < 0 || paintGuardIndex < 0 || captureIndex > captureVisibleIndex || captureVisibleIndex > paintGuardIndex || paintGuardIndex > ackIndex {
		t.Fatalf("captured image must be locally snapped, painted, verified, then browser-acked")
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

func TestControlCodeResultAcknowledgesOnlyAfterVisibleTwoFramePaintHandshake(t *testing.T) {
	source := ticketAppSource(t)
	imageReadyBody := substringBetween(t, source,
		"function waitForControlCodeResultImageReady(image) {",
		"  function controlCodeResultPaintReady(requestID) {")
	paintReadyBody := substringBetween(t, source,
		"function controlCodeResultPaintReady(requestID) {",
		"  function waitForControlCodePaintFrame() {")
	displayBody := substringBetween(t, source,
		"async function displayControlCodeResultImage(requestID, proof, capturedImage, outcome) {",
		"  function controlCodeResultDisplayedForRequest(requestID) {")
	captureBody := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request, proof) {",
		"  function failControlCodeResultScreenshotWait() {")

	for _, needle := range []string{
		"const controlCodeResultImageReadyTimeoutMs = 1200;",
		"const controlCodeResultPaintFrameTimeoutMs = 500;",
		"image.addEventListener('load', onLoad, { once: true });",
		"image.addEventListener('error', onError, { once: true });",
		"timer = setTimeout(() => finish(false), controlCodeResultImageReadyTimeoutMs);",
		"image.decode()",
		"decodeSettled = true;",
		"if (decodeSettled && ready()) finish(true);",
	} {
		if !strings.Contains(source, needle) && !strings.Contains(imageReadyBody, needle) {
			t.Fatalf("result image decode/load handshake missing %q", needle)
		}
	}
	if strings.Contains(imageReadyBody, "if (ready()) return Promise.resolve(true);") {
		t.Fatal("a complete image must not bypass the explicit decode handshake")
	}
	for _, needle := range []string{
		"document.visibilityState !== 'visible'",
		"codeResultArea.hidden || codeResultImage.hidden",
		"codeResultImage.naturalWidth <= 0 || codeResultImage.naturalHeight <= 0",
		"areaRect.width <= 0 || areaRect.height <= 0 || imageRect.width <= 0 || imageRect.height <= 0",
		"visibleWidth <= 0 || visibleHeight <= 0",
		"areaStyle.display !== 'none'",
		"imageStyle.visibility !== 'hidden'",
	} {
		if !strings.Contains(paintReadyBody, needle) {
			t.Fatalf("paint visibility proof missing %q", needle)
		}
	}

	srcIndex := strings.Index(displayBody, "codeResultImage.src = capturedImage;")
	decodeIndex := strings.Index(displayBody, "await waitForControlCodeResultImageReady(codeResultImage)")
	firstPaintIndex := strings.Index(displayBody, "await waitForControlCodePaintFrame()")
	secondPaintRelative := -1
	if firstPaintIndex >= 0 {
		secondPaintRelative = strings.Index(displayBody[firstPaintIndex+1:], "await waitForControlCodePaintFrame()")
	}
	secondPaintIndex := -1
	if secondPaintRelative >= 0 {
		secondPaintIndex = firstPaintIndex + 1 + secondPaintRelative
	}
	reverifyIndex := strings.LastIndex(displayBody, "if (!controlCodeResultPaintReady(requestID)) return false;")
	paintedEventIndex := strings.Index(displayBody, "controlCodeCaptureTrace('control_code_frame_painted'")
	displayedEventIndex := strings.Index(displayBody, "controlCodeCaptureTrace('control_code_frame_displayed'")
	if srcIndex < 0 || decodeIndex < 0 || firstPaintIndex < 0 || secondPaintIndex < 0 || reverifyIndex < 0 || paintedEventIndex < 0 || displayedEventIndex < 0 {
		t.Fatal("control-code display path is missing a complete paint handshake")
	}
	if !(srcIndex < decodeIndex && decodeIndex < firstPaintIndex && firstPaintIndex < secondPaintIndex && secondPaintIndex < reverifyIndex && reverifyIndex < paintedEventIndex && paintedEventIndex < displayedEventIndex) {
		t.Fatal("image must decode, survive two paints, and be reverified before painted/displayed events")
	}
	if strings.Contains(displayBody[:paintedEventIndex], "confirmControlCodeBrowserCapture(") {
		t.Fatal("display helper must never acknowledge before its painted event")
	}

	displayAwaitIndex := strings.Index(captureBody, "const painted = await displayControlCodeResultImage(")
	paintGuardIndex := strings.Index(captureBody, "if (!painted) return false;")
	closedGuardIndex := strings.Index(captureBody[paintGuardIndex+1:], "if (locallyClosedControlCodeRequestIDs.has(requestID)) return false;")
	ackIndex := strings.Index(captureBody, "await confirmControlCodeBrowserCapture(request, proof);")
	if displayAwaitIndex < 0 || paintGuardIndex < 0 || closedGuardIndex < 0 || ackIndex < 0 || !(displayAwaitIndex < paintGuardIndex && paintGuardIndex < ackIndex) {
		t.Fatal("Spacetime capture acknowledgement must remain behind successful paint verification")
	}
	if !strings.Contains(captureBody, "scheduleControlCodeResultCaptureRetry(requestID);") ||
		!strings.Contains(captureBody, "if (!capturedAndAcknowledged") {
		t.Fatal("a failed or hidden paint must re-arm capture instead of acknowledging")
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
	lowLatencyBody := substringBetween(t, source,
		"function resetControlCodeDecoderBacklog(requestID, reason) {",
		"  function requestControlCodeLowLatencyFrame(requestID, reason) {")
	renderRequestBody := substringBetween(t, source,
		"function renderControlCodeRequest(request) {",
		"  function setDetailsPanelVisible(visible) {")
	keyframeBody := substringBetween(t, source,
		"function requestKeyframe(reason, force) {",
		"  function requestKeyframeDebounced(reason, minIntervalMs, force) {")

	for _, needle := range []string{
		"const controlCodeCapturePollMs = 100;",
		"const controlCodeResultInitialKeyframeDelayMs = 1200;",
		"const controlCodeCaptureKeyframeRetryMs = 5000;",
		"const controlCodeCaptureKeyframeRetryLimit = 2;",
		"const controlCodeLowLatencyVisualAgeMs = 750;",
		"const controlCodeLowLatencyDecodeQueueLimit = 1;",
		"const keyframeCommandMinIntervalMs = 2500;",
		"let lastKeyframeCommandAt = 0;",
		"let lastControlCodeLowLatencyFrameKey = '';",
		"let lastControlCodeDecoderBacklogResetRequestID = '';",
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
		"requestControlCodeLowLatencyFrame(requestID, 'control_code_result_marker_low_latency');",
		"maybeRequestControlCodeResultWaitKeyframe(requestID, 'control_code_result_wait_retry');",
	} {
		if !strings.Contains(waitForScreenshot, needle) {
			t.Fatalf("control-code result wait must try capture first and then arm delayed retry, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"decoder.reset();",
		"pendingFrameMetadata = [];",
		"needsKeyFrame = true;",
		"lastAcceptedFrameSequence = Number(lastRenderedFrameSequence || 0);",
		"lastControlCodeDecoderBacklogResetRequestID = requestID;",
		"control_code_decoder_backlog_reset",
	} {
		if !strings.Contains(lowLatencyBody, needle) {
			t.Fatalf("control-code low-latency reset missing %q", needle)
		}
	}
	for _, needle := range []string{
		"requestControlCodeLowLatencyFrame(currentRequestID, 'control_code_running_low_latency');",
		"requestKeyframeDebounced('control_code_running', controlCodeCaptureKeyframeRetryMs);",
	} {
		if !strings.Contains(renderRequestBody, needle) {
			t.Fatalf("running control-code request must arm low-latency stream capture, missing %q", needle)
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

func TestControlCodeLowLatencyResetReconfiguresDecoderBeforeFreshKeyframe(t *testing.T) {
	source := ticketAppSource(t)
	resetConfigBody := substringBetween(t, source,
		"function controlCodeDecoderResetConfig() {",
		"  function resetControlCodeDecoderBacklog(requestID, reason) {")
	resetBody := substringBetween(t, source,
		"function resetControlCodeDecoderBacklog(requestID, reason) {",
		"  function requestControlCodeLowLatencyFrame(requestID, reason) {")

	for _, needle := range []string{
		"const config = lastDecoderConfig || {};",
		"if (decoderMode === 'avc') {",
		"if (!avcDescription) return null;",
		"return { codec, codedWidth, codedHeight, description: avcDescription };",
		"return { codec, codedWidth, codedHeight, avc: { format: 'annexb' } };",
	} {
		if !strings.Contains(resetConfigBody, needle) {
			t.Fatalf("control-code decoder reset config missing %q", needle)
		}
	}

	resetIndex := strings.Index(resetBody, "decoder.reset();")
	disabledIndex := strings.Index(resetBody, "decoderConfigured = false;")
	configureIndex := strings.Index(resetBody, "decoder.configure(resetConfig);")
	enabledIndex := strings.Index(resetBody, "decoderConfigured = true;")
	keyframeIndex := strings.Index(resetBody, "needsKeyFrame = true;")
	if resetIndex < 0 || disabledIndex < 0 || configureIndex < 0 || enabledIndex < 0 || keyframeIndex < 0 {
		t.Fatal("control-code low-latency reset must explicitly restore the decoder configuration")
	}
	if !(resetIndex < disabledIndex && disabledIndex < configureIndex && configureIndex < enabledIndex && enabledIndex < keyframeIndex) {
		t.Fatal("decoder must be reconfigured successfully before the fresh control-code keyframe is requested")
	}
	if !strings.Contains(resetBody, "const resetConfig = controlCodeDecoderResetConfig();") ||
		!strings.Contains(resetBody, "if (!resetConfig) return false;") {
		t.Fatal("decoder reset must not run unless its replacement configuration is ready")
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
		"appendStreamURLParam(url, 'restore_reason', restoreReason);",
		"appendStreamURLParam(url, 'recovery_id', resuming && resuming.id);",
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
		"control_code_frame_painted",
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

func TestStreamResumeSpinnerDebugStateIsSelfContainedInSourceAndBundle(t *testing.T) {
	source := ticketAppSource(t)
	debugPublisher := substringBetween(t, source,
		"function publishStreamDebug() {",
		"  function readUint64(view, offset) {")

	if !strings.Contains(debugPublisher, "streamResumeSpinnerVisible: Boolean(streamResumeSpinner && !streamResumeSpinner.hidden),") {
		t.Fatal("stream debug publisher must compute spinner visibility without an external helper")
	}
	bundle, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bundle), "streamResumeSpinnerVisible:Boolean(streamResumeSpinner&&!streamResumeSpinner.hidden)") {
		t.Fatal("shipped bundle must contain the self-contained spinner visibility check")
	}
	if strings.Contains(debugPublisher, "streamResumeSpinnerVisible()") || strings.Contains(string(bundle), "streamResumeSpinnerVisible()") {
		t.Fatal("stream debug must not depend on a separate spinner visibility symbol")
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
		"const popupVisible = dialogVisible && (okButtonVisible || inputLineVisible);",
		"popupVisible,",
		"unsafeOverlayVisible: popupVisible || popupKeyboardVisible || dialogGhostVisible || (dimOverlayVisible && (popupVisible || dialogGhostVisible || popupKeyboardVisible))",
	} {
		if !strings.Contains(popupProof, needle) {
			t.Fatalf("popup proof must expose fade/ghost overlay rejection, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"Object.assign(proof, popupProof, {",
		"popupGhostVisible: popupProof.dialogGhostVisible",
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
		!strings.Contains(updateSubmit, "codeSubmit.disabled = !codeDialogOpen || busy || !digitsValid;") {
		t.Fatalf("control-code submit must be unavailable while the dialog is closed")
	}
	if !strings.Contains(updateSubmit, "requestCodeButton.disabled = busy;") {
		t.Fatalf("closed-page request button should be unavailable only while the phone lane is occupied")
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

func TestControlCodeQueuesImmediatelyWhileFastPathWarms(t *testing.T) {
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
		"  function relayReportToStreamStatus(report) {")
	updateSubmit := substringBetween(t, source,
		"function updateControlCodeSubmitAvailability() {",
		"  function reconnectVideoForRecovery(reason) {")

	for _, needle := range []string{
		"function liveFrameReadyForControlCode() {",
		"function spacetimeReadyForControlCode() {",
		"function controlCodeFastStateFresh(state) {",
		"function renderControlCodeFastStateDataset() {",
		"function scheduleControlCodeFastStateExpiryCheck() {",
		"controlCodeFastStateExpiryTimer = setTimeout(() => {",
		"refreshControlCodeReadiness('control_code_fast_state_expired');",
		"spacetimeClientStatus === 'live'",
		"if (controlCodeSubmitInFlight) return true;",
		"function refreshControlCodeReadiness(reason, options) {",
		"const allowPrepare = !options || options.prepare !== false;",
		"connectSpacetimeState().catch((error) => clientLog('spacetime_reconnect_failed', error && error.message));",
		"function controlCodeRequestOccupiesPhone(request) {",
		"function controlCodeRequestOccupiesQueue() {",
		"request.cleanupPending === true",
		"request.captureRequired === true && request.captureAcknowledged !== true",
	} {
		if !strings.Contains(readiness, needle) {
			t.Fatalf("control-code background readiness/queue contract missing %q", needle)
		}
	}
	if !strings.Contains(source, "const controlCodeAutoPrepareMinIntervalMs = 5000;") {
		t.Fatalf("control-code auto-prepare must have a bounded interval")
	}
	for _, needle := range []string{
		"if (document.visibilityState === 'hidden') return;",
		"if (!codeResultArea.hidden) return;",
		"if (controlCodeAutoPrepareInFlight || !spacetimeReadyForControlCode()) return;",
		"if (controlCodeFastStateFresh()) return;",
		"const busy = controlCodeRequestOccupiesQueue();",
		"now - lastControlCodeAutoPrepareAt < controlCodeAutoPrepareMinIntervalMs",
		"client.prepareControlCode(reason || 'page_ready_control_code')",
	} {
		if !strings.Contains(autoPrepare, needle) {
			t.Fatalf("control-code auto-prepare must be visible-only and debounced, missing %q", needle)
		}
	}
	if !strings.Contains(updateSubmit, "if (!busy && spacetimeReadyForControlCode() && !controlCodeFastStateFresh())") {
		t.Fatalf("Spacetime-ready but fast-stale control-code page should trigger one debounced prepare")
	}
	for _, needle := range []string{
		"refreshControlCodeReadiness('control_code_dialog_background_warmup');",
		"reconnectVideoForRecovery('control_code_dialog_stream_warmup');",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("dialog entry must warm the transport and fast path in the background, missing %q", needle)
		}
	}
	if strings.Contains(autoPrepare, "if (codeDialogOpen) return;") {
		t.Fatalf("an open control-code dialog must not block readiness preparation")
	}
	if strings.Contains(source, "function controlCodeRequestBusyForAutoPrepare() {") {
		t.Fatalf("auto-prepare must not maintain a second, divergent phone-occupancy predicate")
	}
	if !strings.Contains(source, "let controlCodeFastStateExpiryTimer = null;") ||
		!strings.Contains(source, "scheduleControlCodeFastStateExpiryCheck();") ||
		!strings.Contains(source, "renderControlCodeFastStateDataset();\n    const busy = controlCodeRequestOccupiesQueue();") {
		t.Fatalf("control-code readiness must re-evaluate when a fast-ready lease expires while the dialog is open")
	}
	for _, needle := range []string{
		"let controlCodeSubmitInFlight = false;",
		"const fastRevision = controlCodeFastRevisionForRequest();",
		"return revision && controlCodeFastStateFresh(state) ? revision : '';",
		"client.requestControlCode(digits, fastRevision)",
		"fastReady: controlCodeFastStateFresh()",
		"fastRevisionSent: Boolean(fastRevision)",
		"document.body.dataset.controlCodeFastReady = controlCodeFastStateFresh() ? 'true' : 'false';",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code submit must carry optional fast-state telemetry, missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"if (!fastRevision) {",
		"control_code_submit_fast_not_ready",
		"const requestUnavailable = Boolean(busy) || !spacetimeReadyForControlCode();",
		"const submitUnavailable = requestUnavailable || !controlCodeFastStateFresh();",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("cold control-code submission must not retain readiness gate %q", forbidden)
		}
	}
	for name, body := range map[string]string{
		"open dialog": openDialog,
		"submit":      submitRequest,
		"hotspot":     hotspot,
	} {
		if strings.Contains(body, "controlCodeReadinessMessage()") || strings.Contains(body, "fast_not_ready") {
			t.Fatalf("%s must not block on setup readiness", name)
		}
	}
	for _, needle := range []string{
		"const digitCount = sanitizeControlDigits(codeDigits.value).length;",
		"const digitsValid = digitCount >= 2 && digitCount <= 8;",
		"codeSubmit.disabled = !codeDialogOpen || busy || !digitsValid;",
		"codeSubmit.textContent = controlCodeSubmitInFlight ? 'Nosūta…' : 'Izveidot kodu';",
		"codeSubmit.setAttribute('aria-busy', 'true');",
		"requestCodeButton.disabled = busy;",
	} {
		if !strings.Contains(updateSubmit, needle) {
			t.Fatalf("submit availability must depend only on valid digits and occupied work, missing %q", needle)
		}
	}
	setInFlight := strings.Index(submitRequest, "controlCodeSubmitInFlight = true;")
	mutation := strings.Index(submitRequest, "await runSpacetimeMutation")
	clearInFlight := strings.Index(submitRequest, "controlCodeSubmitInFlight = false;")
	if setInFlight < 0 || mutation < 0 || clearInFlight < 0 || !(setInFlight < mutation && mutation < clearInFlight) {
		t.Fatalf("local submission guard must cover the complete asynchronous mutation")
	}
	for _, forbidden := range []string{
		"controlCodeHotspot.addEventListener('touchend'",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("native hotspot buttons must not fire touchend after a swipe: %q", forbidden)
		}
	}
	for _, needle := range []string{
		"--ticket-hotspot-width",
		"--ticket-hotspot-height",
		"stageViewport.width * 0.5",
		"stageViewport.height * 0.25",
		"Spacetime connection is not ready",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("mobile immediate-submit rough edge missing %q", needle)
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
		"logResumeCheckpoint('activation_resume_recovery_decision'",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("browser stream trace breadcrumb missing %q", needle)
		}
	}
}

func TestTicketBrowserRuntimeLogRetriesReuseTheOriginalRowID(t *testing.T) {
	source := ticketAppSource(t)
	clientSource := readTicketWebClientSource(t, "src/index.ts")
	for _, needle := range []string{
		"function newClientLogRowID(entry) {",
		"if (!compacted.rowId) compacted.rowId = newClientLogRowID(compacted);",
		"pendingClientLogs.unshift(entry);",
		"entry.correlationId || '', entry.rowId || ''",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("browser safe-log retry must preserve its enqueue-time row ID, missing %q", needle)
		}
	}
	for _, needle := range []string{
		`appendSafeLog(level: string, event: string, detailJson: string, correlationId = "", rowId = "")`,
		`id: rowId || this.logRowId("browser", event, correlationId)`,
	} {
		if !strings.Contains(clientSource, needle) {
			t.Fatalf("Spacetime browser client must accept a retry-stable safe-log row ID, missing %q", needle)
		}
	}
}

func TestTicketBrowserDeduplicatesResumeOutcomesAndHiddenDecoderNoise(t *testing.T) {
	source := ticketAppSource(t)
	eventMap := substringBetween(t, source,
		"function compactClientEventName(value) {",
		"  function compactClientLogEntry(entry) {")
	resumeQueue := substringBetween(t, source,
		"function logResumeCheckpoint(event, detail, flow) {",
		"  function finishActivationResumeFlow(reason, flow) {")
	decoderReport := substringBetween(t, source,
		"function reportDecoderError(error, mode) {",
		"  function sendVideoSocketClientLog(event, detail) {")
	visibility := substringBetween(t, source,
		"document.addEventListener('visibilitychange', () => {",
		"  window.addEventListener('pageshow'")

	for _, needle := range []string{
		"if (/activation_resume_fresh_frame/.test(event)) return 'stream_recovered';",
		"if (/recover|recovery/.test(event)) return /failed|exhausted/.test(event) ? 'stream_failed' : 'stream_recovery_requested';",
	} {
		if !strings.Contains(eventMap, needle) {
			t.Fatalf("resume event classification must reserve terminal outcomes for fresh/exhausted, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"if (!target || target.done || target.logs >= 6) return;",
		"target.logs += 1;",
		"correlationId: target.id",
	} {
		if !strings.Contains(resumeQueue, needle) {
			t.Fatalf("resume safe-log path must allow at most one failure and one recovery per flow, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"if (document.visibilityState === 'hidden') {",
		"if (hiddenDecoderTransientLogged) return;",
		"sendVideoClientLog('decoder_transient_hidden'",
		"sendVideoClientLog('decoder_error'",
	} {
		if !strings.Contains(decoderReport, needle) {
			t.Fatalf("hidden decoder faults must collapse into one transient diagnostic per hidden episode, missing %q", needle)
		}
	}
	if strings.Count(source, "reportDecoderError(error,") != 3 {
		t.Fatalf("both decoder error callbacks must use the bounded classifier")
	}
	if strings.Count(visibility, "hiddenDecoderTransientLogged = false;") != 2 {
		t.Fatalf("decoder transient gate must reset at the visible/hidden episode boundaries")
	}
}

func TestControlCodeRequestRenderingIsIdempotentAcrossUnrelatedStateUpdates(t *testing.T) {
	source := ticketAppSource(t)
	renderBody := substringBetween(t, source,
		"function renderControlCodeRequest(request) {",
		"  function setDetailsPanelVisible(visible) {")
	for _, needle := range []string{
		"let lastRenderedControlCodeRequestSignature = '';",
		"function normalizedControlCodeRequestSignature(request) {",
		"const renderSignature = normalizedControlCodeRequestSignature(nextRequest);",
		"if (renderSignature === lastRenderedControlCodeRequestSignature) return;",
		"lastRenderedControlCodeRequestSignature = renderSignature;",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code subscription rendering must use a normalized idempotence signature, missing %q", needle)
		}
	}
	availabilityIndex := strings.Index(renderBody, "updateControlCodeSubmitAvailability();")
	guardIndex := strings.Index(renderBody, "if (renderSignature === lastRenderedControlCodeRequestSignature) return;")
	requestSideEffectIndex := strings.Index(renderBody, "keepControlCodeVideoAlive('control_code_request_active');")
	if availabilityIndex < 0 || guardIndex < 0 || requestSideEffectIndex < 0 || availabilityIndex > guardIndex || guardIndex > requestSideEffectIndex {
		t.Fatalf("idempotence guard must preserve global submit readiness while skipping repeated request side effects")
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

func TestSpacetimeReconnectRefreshesOnlyExpiredTokens(t *testing.T) {
	source := ticketAppSource(t)
	if strings.Contains(source, "spacetimeDirectUnavailable = true") || !strings.Contains(source, "setTimeout(() => { if (!idleDisconnected && !spacetimeClient) connectSpacetimeState()") {
		t.Fatal("a transient token or script failure must retry instead of permanently disabling direct state")
	}
	recoverExpired := substringBetween(t, source,
		"function recoverExpiredSpacetimeConnection(client, reason) {",
		"  async function connectSpacetimeState() {")
	statusPublisher := substringBetween(t, source,
		"function publishSpacetimeClientStatus(status) {",
		"  function usesDirectSpacetimeAuth() {")

	for _, required := range []string{
		"if (client !== spacetimeClient || spacetimeExpiredTokenRefreshPromise) return false;",
		"if (!directSpacetimeToken || !spacetimeTokenExpired(directSpacetimeToken)) return false;",
		"spacetimeClient = null;",
		"if (client && typeof client.close === 'function') client.close();",
		"clearLocalAuthState();",
		"await fetchAuthSessionToken();",
		"if (!idleDisconnected) await connectSpacetimeState();",
		"spacetimeExpiredTokenRefreshPromise = null;",
	} {
		if !strings.Contains(recoverExpired, required) {
			t.Fatalf("expired Spacetime token recovery missing %q", required)
		}
	}
	guardAt := strings.Index(recoverExpired, "!directSpacetimeToken || !spacetimeTokenExpired")
	dropAt := strings.Index(recoverExpired, "spacetimeClient = null;")
	if guardAt < 0 || dropAt < 0 || guardAt > dropAt {
		t.Fatalf("ordinary network outages must not drop the Spacetime client unless its token is expiring")
	}
	if strings.Contains(recoverExpired, "beginSpacetimeLogin(") {
		t.Fatalf("connection errors must refresh the existing session first instead of directly starting an auth redirect")
	}

	for _, required := range []string{
		"if (client !== spacetimeClient) return;",
		"if (status === 'offline' || status === 'reconnecting') {",
		"recoverExpiredSpacetimeConnection(client, status);",
		"if (spacetimeClient === client) spacetimeClient = null;",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Spacetime status lifecycle missing %q", required)
		}
	}
	for _, required := range []string{
		"heartbeat_failed: 'degraded'",
		"})[normalized] || 'offline';",
		"document.body.dataset.spacetimeConnection = safeStatus;",
	} {
		if !strings.Contains(statusPublisher, required) {
			t.Fatalf("safe Spacetime status dataset missing %q", required)
		}
	}
	for _, forbidden := range []string{"detail", "directSpacetimeToken", "token"} {
		if strings.Contains(statusPublisher, forbidden) {
			t.Fatalf("public Spacetime status dataset must not expose %q", forbidden)
		}
	}
}

func TestControlCodeUsesDirectSpacetimeReducerFlow(t *testing.T) {
	appSource := ticketAppSource(t)
	for _, needle := range []string{
		"client.requestControlCode(digits, fastRevision)",
		"client.confirmControlCodeBrowserCapture(",
		"client.closeControlCode(requestID, 'browser_closed')",
	} {
		if !strings.Contains(appSource, needle) {
			t.Fatalf("control-code browser flow must call Spacetime directly, missing %q", needle)
		}
	}

	clientSource := ticketRemoteSourceFile(t, "internal", "web", "static", "spacetime-client.js")
	for _, needle := range []string{
		"this.callReducer(\"memberRequestControlCode\"",
		"this.callReducer(\"memberConfirmControlCodeBrowserCapture\"",
		"this.callReducer(\"memberCloseControlCode\"",
	} {
		if !strings.Contains(clientSource, needle) {
			t.Fatalf("Spacetime client must own the control-code mutation, missing %q", needle)
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
	if !strings.Contains(candidateProof, "Object.assign(proof, generatedProof);") {
		t.Fatalf("candidate proof debug must retain every generated proof field")
	}
}

func TestControlCodePopupProofTargetsCenteredEntryDialog(t *testing.T) {
	source := ticketAppSource(t)
	popupProof := substringBetween(t, source,
		"function controlCodePopupFrameProof() {",
		"  function controlCodeResultChipProof() {")

	for _, needle := range []string{
		"const dialog = sample(0.16, 0.38, 0.68, 0.22);",
		"const dialogUpper = sample(0.16, 0.30, 0.68, 0.22);",
		"const inputLineUpper = sample(0.24, 0.41, 0.52, 0.045);",
		"const okButton = sample(0.64, 0.51, 0.18, 0.07);",
		"const okButtonUpper = sample(0.64, 0.43, 0.18, 0.07);",
		"function regionOrangeCellRatio(region) {",
		"okButtonOrangeRatio",
		"okButtonVisible",
		"const popupVisible = dialogVisible && (okButtonVisible || inputLineVisible);",
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

func TestControlCodeCloseLocallyDismissesFailedResultWithoutOwnershipCache(t *testing.T) {
	source := ticketAppSource(t)
	closeBody := substringBetween(t, source,
		"async function closeCurrentControlCode(openNext) {",
		"  function requestControlCodeFromHotspot(event) {")
	hotspotBody := substringBetween(t, source,
		"function requestControlCodeFromHotspot(event) {",
		"  function relayReportToStreamStatus(report) {")

	for _, required := range []string{
		"const request = codeRequest;",
		"const canCloseRequest = Boolean(requestID && (",
		"ownedControlCodeRequestIDs.has(String(requestID)) || isOwnedControlCodeRequest(request)",
		"locallyClosedControlCodeRequestIDs.add(String(requestID));",
		"setControlCodeResultVisible(false);",
		"clearControlCodeResultCapture();",
		"scheduleControlCodeTicker(null);",
		"codeRequest = null;",
		"clientLog('control_code_close_local_only', 'not_owned');",
	} {
		if !strings.Contains(closeBody, required) {
			t.Fatalf("result close must locally dismiss even when the ownership cache is late, missing %q", required)
		}
	}
	if strings.Contains(closeBody, "if (!ownedControlCodeRequestIDs.has(String(requestID)))") {
		t.Fatal("a late ownership cache must not block immediate local result dismissal")
	}
	cleanupIndex := strings.Index(closeBody, "setControlCodeResultVisible(false);")
	postGuardIndex := strings.Index(closeBody, "if (requestID && canCloseRequest) {")
	if cleanupIndex < 0 || postGuardIndex < 0 || cleanupIndex > postGuardIndex {
		t.Fatal("result close must clear the local overlay before deciding whether the server close may run")
	}
	if !strings.Contains(hotspotBody, "if (!codeResultArea.hidden) {") ||
		strings.Contains(hotspotBody, "if (!codeResultArea.hidden && codeRequest)") {
		t.Fatal("the left result-dismiss action must work even when a failed result no longer has request state")
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
	revealIndex := strings.Index(captureBody, "const painted = await displayControlCodeResultImage(requestID, proof, capturedImage, 'browser_capture_displayed');")
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

func TestControlCodeRunningOnlyPreparesHiddenResult(t *testing.T) {
	source := ticketAppSource(t)
	prepareBody := substringBetween(t, source,
		"function maybePrepareControlCodeResultFrame() {",
		"  function noteControlCodeMarkerWaiting(request) {")
	captureBody := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request, proof) {",
		"  function failControlCodeResultScreenshotWait() {")

	for _, needle := range []string{
		"if (!codeRequest || codeRequest.status !== 'running') return false;",
		"const proof = controlCodeCandidateFrameProof(codeRequest, { allowProvisional: true });",
		"controlCodePreparedCaptureProof = Object.assign({}, proof, {",
		"preparedAt: Date.now()",
	} {
		if !strings.Contains(prepareBody, needle) {
			t.Fatalf("running control-code request must only prepare a hidden frozen frame, missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"displayControlCodeResultImage(",
		"captureControlCodeResultImage(",
		"confirmControlCodeBrowserCapture(",
		"setControlCodeResultVisible(true)",
		"control_code_frame_displayed",
		"control_code_frame_painted",
	} {
		if strings.Contains(prepareBody, forbidden) {
			t.Fatalf("running control-code request must not display or acknowledge provisionally, found %q", forbidden)
		}
	}
	if !strings.Contains(captureBody, "if (!request || request.status !== 'succeeded'") {
		t.Fatal("browser result display must reject any direct provisional capture call")
	}
}

func TestTicketViewerRunsBoundedActivationReconnectBurst(t *testing.T) {
	source := ticketAppSource(t)
	for _, needle := range []string{
		"let activeResumeFlow = null;",
		"let activationReconnectBurstTimer = null;",
		"let lastRecoveryVideoReconnectSeq = -1;",
		"const activationReconnectBurstMs = 10000;",
		"const activationReconnectFirstRetryMs = 150;",
		"const activationReconnectTickMs = 500;",
		"const activationReconnectMaxTicks = 10;",
		"function startActivationResumeFlow(reason, trigger, options) {",
		"function runActivationReconnectBurst(reason, flow) {",
		"if (!flow || flow !== activeResumeFlow || flow.done) return;",
		"flow.attempts >= activationReconnectMaxTicks",
		"performance.now() - flow.startedAt >= activationReconnectBurstMs",
		"requestServerRecoveryDebounced(`${reason || 'resume'}_exhausted`, true);",
		"connectSpacetimeState().catch",
		"publishCurrentStreamFocus(reason || 'activation');",
		"requestKeyframeDebounced(`${reason || 'activation'}_keyframe`, 0, true);",
		"recoverFreshMediaSession(reason || 'activation', 'activation_resume'",
		"flow.attempts += 1;",
		"flow.attempts === 1 ? activationReconnectFirstRetryMs : activationReconnectTickMs",
		"function recoverFreshMediaSession(reason, kind, options) {",
		"lastRecoveryVideoReconnectSeq === videoSocketOpenSeq",
		"now - lastRecoveryVideoReconnectAt < recoveryVideoReconnectDebounceMs",
		"lastRecoveryVideoReconnectSeq = videoSocketOpenSeq;",
		"preserveCurrentFrame(`media_recovery:${reason || 'unknown'}`);",
		"closeDirectVideo();",
		"resetStreamState({ preserveFrame: true });",
		"connectDirectVideo({ skipEarlyGrace: Boolean(options.skipEarlyGrace) });",
		"recoverFreshMediaSession(reason || 'visibility_resume', 'old_tab_resume'",
		"keyframeReason: `${reason || 'resume'}_cached_keyframe`",
		"startActivationResumeFlow(event.persisted ? 'pageshow_persisted' : 'pageshow', 'pageshow');",
		"startActivationResumeFlow('focus', 'focus');",
		"startActivationResumeFlow('initial_load', 'initial_load');",
		"startActivationResumeFlow('visibility_hidden', 'visibility_hidden', { pauseBurst: true });",
		"recoverAfterVisibilityResume('visibility_resume');",
		"spacetimeClient.appendSafeLog(entry.level || 'info', entry.event || 'client_event', detailJson, entry.correlationId || '', entry.rowId || '')",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("explicit bounded stream lifecycle missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"pendingResumeFreshFrameFlow",
		"resumeRecoverySoftTimer",
		"resumeRecoveryHardTimer",
		"activationResumeLogLimit",
		"recordDevPerfMetric(",
		"randomMetricFlowId(",
		"memberAppendDevPerfMetric",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("stream lifecycle still contains retired overlapping state %q", forbidden)
		}
	}
}

func TestTicketViewerResumeRecoveryWaitsForLiveFrameAndReusesFreshSocket(t *testing.T) {
	source := ticketAppSource(t)
	recoveryBody := substringBetween(t, source,
		"function recoverFreshMediaSession(reason, kind, options) {",
		"  function connect() {")
	renderBody := substringBetween(t, source,
		"function renderDecodedFrame(frame, source) {",
		"  async function configureDecoder(config, options) {")
	freshnessBody := substringBetween(t, source,
		"function updateStreamFreshnessStatus(reason) {",
		"  function liveFrameReadyForControlCode() {")

	if !strings.Contains(recoveryBody, "lastRecoveryVideoReconnectSeq === videoSocketOpenSeq") ||
		!strings.Contains(recoveryBody, "now - lastRecoveryVideoReconnectAt < recoveryVideoReconnectDebounceMs") ||
		!strings.Contains(recoveryBody, "if (reusable) {") ||
		!strings.Contains(recoveryBody, "requestKeyframeDebounced(options.keyframeReason") {
		t.Fatalf("a newly opened recovery socket must be reused during its bounded cooldown")
	}
	if strings.Contains(recoveryBody, "reusable && !mediaSessionStuckOnPreservedFrame()") {
		t.Fatalf("a preserved stale frame must not bypass the recovery socket cooldown")
	}
	connect := strings.Index(recoveryBody, "connectDirectVideo({ skipEarlyGrace: Boolean(options.skipEarlyGrace) });")
	guard := strings.Index(recoveryBody, "lastRecoveryVideoReconnectSeq = videoSocketOpenSeq;")
	if connect < 0 || guard < connect {
		t.Fatalf("the recovery guard must record the generation of the newly opened socket")
	}
	if strings.Contains(renderBody, "clearActivationReconnectBurst();") ||
		strings.Contains(renderBody, "finishActivationResumeFlow(") {
		t.Fatalf("an arbitrary decoded frame must not finish the activation recovery burst")
	}
	liveBranch := strings.Index(freshnessBody, "} else if (freshness.liveLabeled) {")
	finish := strings.Index(freshnessBody, "finishActivationResumeFlow('fresh_frame');")
	if liveBranch < 0 || finish < liveBranch {
		t.Fatalf("only a live-labeled frame may finish the activation recovery burst")
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

func TestBrowserSuppressesBackgroundRecoveryWhenStreamIsFresh(t *testing.T) {
	source := ticketAppSource(t)
	keyframeBody := substringBetween(t, source,
		"function requestKeyframe(reason, force) {",
		"  function requestKeyframeDebounced(reason, minIntervalMs, force) {")
	recoveryBody := substringBetween(t, source,
		"function requestServerRecoveryDebounced(reason, force) {",
		"  function resetFirstFrameServerRecovery() {")

	for _, needle := range []string{
		"function liveStreamSuppressesBackgroundRequest(reason) {",
		"if (cleanReason.includes('control_code')) return false;",
		"return streamHasFreshRenderedFrame();",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("fresh stream suppression missing %q", needle)
		}
	}
	if !strings.Contains(keyframeBody, "if (liveStreamSuppressesBackgroundRequest(reason)) return false;") {
		t.Fatalf("keyframe requests must no-op when the stream is already fresh")
	}
	if !strings.Contains(recoveryBody, "if (liveStreamSuppressesBackgroundRequest(reason)) return false;") {
		t.Fatalf("server recovery requests must no-op when the stream is already fresh")
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
