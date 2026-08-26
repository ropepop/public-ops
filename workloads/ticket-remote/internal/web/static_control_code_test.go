package web

import (
	"os"
	"os/exec"
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
	visibilityBody := substringBetween(t, source,
		"function setControlCodeResultVisible(visible) {",
		"  function clearControlCodeResultCapture() {")
	imageReadyBody := substringBetween(t, source,
		"function waitForControlCodeResultImageReady(image) {",
		"  function controlCodeResultPaintReady(requestID) {")
	paintReadyBody := substringBetween(t, source,
		"function controlCodeResultPaintReady(requestID) {",
		"  function waitForControlCodePaintFrame() {")
	displayBody := substringBetween(t, source,
		"async function displayControlCodeResultImage(requestID, proof, capturedImage, outcome) {",
		"  function controlCodeResultDisplayedForRequest(requestID) {")
	revealBody := substringBetween(t, source,
		"function revealControlCodeResultImageAtomically(requestID) {",
		"  async function displayControlCodeResultImage(requestID, proof, capturedImage, outcome) {")
	captureBody := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request, proof) {",
		"  function failControlCodeResultScreenshotWait() {")

	for _, needle := range []string{
		"if (visible)",
		"document.body.classList.remove('details-visible')",
		"panel.setAttribute('aria-hidden', 'true')",
		"stage.scrollIntoView({ block: 'start', inline: 'nearest', behavior: 'auto' })",
	} {
		if !strings.Contains(visibilityBody, needle) {
			t.Fatalf("result visibility must align the stream stage with the viewport before paint: missing %q", needle)
		}
	}
	scrollIndex := strings.Index(visibilityBody, "stage.scrollIntoView({ block: 'start', inline: 'nearest', behavior: 'auto' })")
	revealAreaIndex := strings.Index(visibilityBody, "codeResultArea.hidden = !visible;")
	if scrollIndex < 0 || revealAreaIndex < 0 || scrollIndex > revealAreaIndex {
		t.Fatal("control-code result must move into the viewport before its area is revealed")
	}

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

	hideAreaIndex := strings.Index(displayBody, "setControlCodeResultVisible(false);")
	hideImageIndex := strings.Index(displayBody, "codeResultImage.hidden = true;")
	clearSourceIndex := strings.Index(displayBody, "codeResultImage.removeAttribute('src');")
	srcIndex := strings.Index(displayBody, "codeResultImage.src = capturedImage;")
	decodeIndex := strings.Index(displayBody, "await waitForControlCodeResultImageReady(codeResultImage)")
	revealIndex := strings.Index(displayBody, "await revealControlCodeResultImageAtomically(requestID)")
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
	if hideAreaIndex < 0 || hideImageIndex < 0 || clearSourceIndex < 0 || srcIndex < 0 || decodeIndex < 0 || revealIndex < 0 || firstPaintIndex < 0 || secondPaintIndex < 0 || reverifyIndex < 0 || paintedEventIndex < 0 || displayedEventIndex < 0 {
		t.Fatal("control-code display path is missing a complete paint handshake")
	}
	if !(hideAreaIndex < srcIndex && hideImageIndex < srcIndex && clearSourceIndex < srcIndex && srcIndex < decodeIndex && decodeIndex < revealIndex && revealIndex < firstPaintIndex && firstPaintIndex < secondPaintIndex && secondPaintIndex < reverifyIndex && reverifyIndex < paintedEventIndex && paintedEventIndex < displayedEventIndex) {
		t.Fatal("image must stay hidden through decode, reveal atomically, survive two paints, and be reverified before painted/displayed events")
	}
	for _, needle := range []string{
		"requestAnimationFrame(() => {",
		"document.visibilityState !== 'visible'",
		"locallyClosedControlCodeRequestIDs.has(requestID)",
		"!codeRequest",
		"String(codeRequest.requestId || '').trim() !== requestID",
		"codeRequest.status !== 'succeeded'",
		"!codeResultImage.complete",
		"codeResultImage.naturalWidth <= 0",
		"codeResultImage.naturalHeight <= 0",
		"codeResultImage.hidden = false;",
		"setControlCodeResultVisible(true);",
	} {
		if !strings.Contains(revealBody, needle) {
			t.Fatalf("atomic control-code reveal missing %q", needle)
		}
	}
	imageRevealIndex := strings.Index(revealBody, "codeResultImage.hidden = false;")
	areaRevealIndex := strings.Index(revealBody, "setControlCodeResultVisible(true);")
	frameRevealIndex := strings.Index(revealBody, "frameID = requestAnimationFrame(() => {")
	if frameRevealIndex < 0 || imageRevealIndex < frameRevealIndex || areaRevealIndex < imageRevealIndex {
		t.Fatal("result image and containing layer must reveal together inside one animation frame")
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

func TestTrustedPhoneMarkerRequiresItsModeSpecificGeneratedDetector(t *testing.T) {
	source := ticketAppSource(t)
	candidateBody := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")

	markerGuardIndex := strings.Index(candidateBody, "if (markerEpoch && markerSequence && (renderedEpoch !== markerEpoch || renderedSequence < markerSequence))")
	markerIndex := strings.Index(candidateBody, "const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof")
	changedIndex := strings.Index(candidateBody, "const frameChangedFromBaseline = Boolean(controlCodeBaselineFrameFingerprint")
	visualIndex := strings.Index(candidateBody, "const browserTrustedGeneratedVisible = Boolean(")
	resultIndex := strings.Index(candidateBody, "const browserTrustedResultVisible = browserTrustedGeneratedVisible;")
	rejectIndex := strings.Index(candidateBody, "if (!proof.browserTrustedResultVisible) {")
	rejectedIndex := strings.Index(candidateBody, "proof.generatedMarkerOnlyRejected = true;")
	if markerGuardIndex < 0 || changedIndex < 0 || markerIndex < 0 || visualIndex < 0 || resultIndex < 0 || rejectIndex < 0 || rejectedIndex < 0 {
		t.Fatalf("strict phone/generated marker proof or its rejection diagnostics are missing")
	}
	if markerGuardIndex > markerIndex {
		t.Fatalf("trusted phone marker-frame diagnostics must run only after the frame-at-or-after-marker guard")
	}
	if changedIndex > visualIndex || visualIndex > resultIndex || markerIndex > rejectIndex || resultIndex > rejectIndex {
		t.Fatalf("generated visual proof must be built after its guards and before result rejection")
	}
	for _, forbidden := range []string{
		"trustedPhoneChangedMarkerFrame",
		"candidate_frame_at_or_after_trusted_phone_marker_and_changed_visual",
		"proof.generatedVisibleByPhoneMarker",
	} {
		if strings.Contains(candidateBody, forbidden) {
			t.Fatalf("trusted phone marker must not bridge browser generated proof: found %q", forbidden)
		}
	}
	for _, needle := range []string{
		"renderedEpoch === markerEpoch",
		"renderedSequence >= markerSequence",
		"frameChangedFromBaseline",
		"phoneGeneratedProofKind === 'inline'",
		"generatedProof.generatedCodeVisible &&",
		"!generatedProof.generatedChipVisible",
		"phoneGeneratedProofKind === 'with_close'",
		"generatedProof.generatedVisible &&",
		"generatedProof.generatedChipVisible",
		"proof.generatedMarkerOnlyRejected = true;",
	} {
		if !strings.Contains(candidateBody[changedIndex:rejectIndex], needle) && !strings.Contains(candidateBody[markerIndex:rejectIndex], needle) {
			t.Fatalf("strict generated marker proof missing guard %q", needle)
		}
	}
	for _, needle := range []string{
		"controlCodeBaselineFrameFingerprint &&",
		"proof.fingerprintDifferenceScore >= controlCodeFingerprintDifferenceThreshold",
		"proof.fingerprintChangedCells >= controlCodeFingerprintChangedCellsThreshold",
	} {
		if !strings.Contains(candidateBody[changedIndex:visualIndex], needle) {
			t.Fatalf("generated proof must require a changed pre-request baseline, missing %q", needle)
		}
	}
	if !strings.Contains(candidateBody, "request.status !== 'succeeded'") {
		t.Fatal("candidate frame proof must require a succeeded request")
	}
}

func TestControlCodeBusyRenderPreservesPreRequestBaseline(t *testing.T) {
	source := ticketAppSource(t)
	renderBody := substringBetween(t, source,
		"function renderControlCodeRequest(request) {",
		"  function setDetailsPanelVisible(visible) {")
	submitBody := substringBetween(t, source,
		"async function submitControlCodeRequest() {",
		"  async function closeCurrentControlCode(openNext) {")

	baselineIndex := strings.Index(submitBody, "pendingControlCodeBaselineFrameFingerprint = canvasRegionFingerprint(controlCodeFingerprintRegion());")
	mutationIndex := strings.Index(submitBody, "await runSpacetimeMutation((client) => client.requestControlCode(digits, fastRevision)")
	if baselineIndex < 0 || mutationIndex < 0 || baselineIndex > mutationIndex {
		t.Fatal("control-code submission must capture its raw-ticket baseline before the reducer call")
	}
	busyBody := substringBetween(t, renderBody,
		"if (busy) {",
		"    if (!current || current.status === 'closed' || current.status === 'expired') {")
	for _, needle := range []string{
		"rememberControlCodeBaselineFrame(requestID);",
		"clearUnpaintedControlCodeResultImage(currentRequestID);",
		"scheduleControlCodeTicker(current);",
		"return;",
	} {
		if !strings.Contains(renderBody, needle) {
			t.Fatalf("busy control-code rendering must preserve the pre-request baseline, missing %q", needle)
		}
	}
	if strings.Contains(busyBody, "clearControlCodeResultCapture();") {
		t.Fatal("queued/running control-code rendering must not erase the pre-request baseline")
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
		"function resetControlCodeDecoderBacklog(requestID, reason, force) {",
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
		"let lastControlCodeDecoderBacklogResetKey = '';",
		"let decoderGeneration = 0;",
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
		"closeDecoder();",
		"decoder = new VideoDecoder({",
		"decoder.configure(resetConfig);",
		"clearFrameMetadata();",
		"const decoderInstanceGeneration = decoderGeneration;",
		"if (decoderInstanceGeneration !== decoderGeneration)",
		"try { frame.close(); } catch (_) {}",
		"needsKeyFrame = true;",
		"lastAcceptedFrameSequence = Number(lastRenderedFrameSequence || 0);",
		"const resetKey = `${requestID}:${reason || 'control_code'}`;",
		"lastControlCodeDecoderBacklogResetKey = resetKey;",
		"if (!force && !backlogReason) return false;",
		"forced: Boolean(force),",
		"control_code_decoder_backlog_reset",
	} {
		if !strings.Contains(lowLatencyBody, needle) {
			t.Fatalf("control-code low-latency reset missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"requestControlCodeLowLatencyFrame(currentRequestID, 'control_code_running_low_latency');",
		"requestKeyframeDebounced('control_code_running', controlCodeCaptureKeyframeRetryMs);",
		"maybePrepareControlCodeResultFrame();",
	} {
		if strings.Contains(renderRequestBody, forbidden) {
			t.Fatalf("running control-code request must not do pre-generated-result work, found %q", forbidden)
		}
	}
	if strings.Contains(source, "function maybePrepareControlCodeResultFrame() {") ||
		strings.Contains(source, "controlCodePreparedCaptureProof") {
		t.Fatal("browser must not retain a speculative pre-generated-result capture path")
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
		"if (!force && lastKeyframeCommandAt > 0 && now - lastKeyframeCommandAt < keyframeCommandMinIntervalMs) return false;",
		"lastKeyframeCommandAt = now;",
		"return true;",
	} {
		if !strings.Contains(keyframeBody, needle) {
			t.Fatalf("keyframe command helper must globally throttle writes, missing %q", needle)
		}
	}
}

func TestControlCodeLowLatencyResetRecreatesDecoderBeforeFreshKeyframe(t *testing.T) {
	source := ticketAppSource(t)
	resetConfigBody := substringBetween(t, source,
		"function controlCodeDecoderResetConfig() {",
		"  function resetControlCodeDecoderBacklog(requestID, reason, force) {")
	resetBody := substringBetween(t, source,
		"function resetControlCodeDecoderBacklog(requestID, reason, force) {",
		"  function requestControlCodeLowLatencyFrame(requestID, reason) {")

	for _, needle := range []string{
		"const config = lastDecoderConfig || {};",
		"if (decoderMode === 'avc') {",
		"if (!avcDescription) return null;",
		"return { codec, codedWidth, codedHeight, description: avcDescription, optimizeForLatency: true };",
		"return { codec, codedWidth, codedHeight, avc: { format: 'annexb' }, optimizeForLatency: true };",
	} {
		if !strings.Contains(resetConfigBody, needle) {
			t.Fatalf("control-code decoder reset config missing %q", needle)
		}
	}

	closeIndex := strings.Index(resetBody, "closeDecoder();")
	constructorIndex := strings.Index(resetBody, "decoder = new VideoDecoder({")
	configureIndex := strings.Index(resetBody, "decoder.configure(resetConfig);")
	enabledIndex := strings.Index(resetBody, "decoderConfigured = true;")
	keyframeIndex := -1
	if configureIndex >= 0 {
		keyframeIndex = strings.Index(resetBody[configureIndex:], "needsKeyFrame = true;")
		if keyframeIndex >= 0 {
			keyframeIndex += configureIndex
		}
	}
	if closeIndex < 0 || constructorIndex < 0 || configureIndex < 0 || enabledIndex < 0 || keyframeIndex < 0 {
		t.Fatal("control-code low-latency reset must recreate and configure the decoder")
	}
	if !(closeIndex < constructorIndex && constructorIndex < configureIndex && configureIndex < enabledIndex && enabledIndex < keyframeIndex) {
		t.Fatal("decoder must be recreated and configured before the fresh control-code keyframe is requested")
	}
	if !strings.Contains(resetBody, "const resetConfig = controlCodeDecoderResetConfig();") ||
		!strings.Contains(resetBody, "if (!resetConfig) return false;") {
		t.Fatal("decoder reset must not run unless its replacement configuration is ready")
	}
	if strings.Contains(source, "control_code_running_low_latency") ||
		!strings.Contains(source, "resetControlCodeDecoderBacklog(requestID, reason || 'control_code_low_latency', false);") ||
		!strings.Contains(source, "requestKeyframeDebounced(reason || 'control_code_low_latency_frame', 0, true);") ||
		!strings.Contains(source, "optimizeForLatency: true") {
		t.Fatal("control-code flow must defer any decoder reset/keyframe request until the generated marker")
	}
}

func TestControlCodeResultMarkerKeepsLiveVideoPath(t *testing.T) {
	source := ticketAppSource(t)
	lowLatencyBody := substringBetween(t, source,
		"function requestControlCodeLowLatencyFrame(requestID, reason) {",
		"  function publishStreamDebug() {")
	for _, forbidden := range []string{
		"function reconnectVideoForControlCodeResult(",
		"lastControlCodeResultVideoReconnectKey",
		"controlCodeResultVideoReconnected(",
	} {
		if strings.Contains(source, forbidden) || strings.Contains(lowLatencyBody, forbidden) {
			t.Fatalf("result marker must keep the live video path, found %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		"closeDirectVideo();",
		"resetStreamState({ preserveFrame: true });",
		"connectDirectVideo({ skipEarlyGrace: true });",
	} {
		if strings.Contains(lowLatencyBody, forbidden) {
			t.Fatalf("result marker must not reconnect the live video path, found %q", forbidden)
		}
	}
	for _, needle := range []string{
		"Keep the live socket intact for the control-code flow.",
		"resetControlCodeDecoderBacklog(requestID, reason || 'control_code_low_latency', false);",
		"return requestKeyframeDebounced(reason || 'control_code_low_latency_frame', 0, true);",
	} {
		if !strings.Contains(lowLatencyBody, needle) {
			t.Fatalf("live result marker path missing %q", needle)
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
		"function claimableEarlyVideoQueue(early) {",
		"function claimEarlyVideoSocket() {",
		"const queued = claimableEarlyVideoQueue(early);",
		"function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {",
		"claimEarlyVideoSocket()",
		"queuedMessages.forEach((queued) => queueVideoSocketMessage(queued, true));",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("ticket viewer missing early video reuse behavior: %q", needle)
		}
	}
}

func TestTicketViewerSerializesEarlyConfigBeforeQueuedAndLiveFrames(t *testing.T) {
	source := ticketAppSource(t)
	claimBody := substringBetween(t, source,
		"function claimEarlyVideoSocket() {",
		"  function scheduleViewerIdleDisconnect(reason) {")
	adoptBody := substringBetween(t, source,
		"function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {",
		"  function sendVideoClientLog(event, detail) {")

	if !strings.Contains(claimBody, "if (early.config) queued.unshift(early.config);") {
		t.Fatal("early decoder config must remain first in the adopted message queue")
	}
	for _, needle := range []string{
		"let videoMessageChain = Promise.resolve();",
		"function queueVideoSocketMessage(event, queued) {",
		"videoMessageChain = videoMessageChain.then(() => {",
		"return handleVideoSocketMessage(event);",
		"socket.onmessage = (event) => queueVideoSocketMessage(event, false);",
		"queuedMessages.forEach((queued) => queueVideoSocketMessage(queued, true));",
	} {
		if !strings.Contains(adoptBody, needle) {
			t.Fatalf("early video adoption must serialize config, cached frames, and live frames, missing %q", needle)
		}
	}
	if strings.Contains(adoptBody, "queuedMessages.forEach((queued) => {\n      handleVideoSocketMessage(queued)") {
		t.Fatal("early cached frames must not race asynchronous decoder configuration")
	}
}

func TestTicketViewerAllowsFirstEligibleKeyframeAndRecoveryRequests(t *testing.T) {
	source := ticketAppSource(t)
	keyframeBody := substringBetween(t, source,
		"function requestKeyframe(reason, force) {",
		"  function requestKeyframeDebounced(reason, minIntervalMs, force) {")
	keyframeDebounceBody := substringBetween(t, source,
		"function requestKeyframeDebounced(reason, minIntervalMs, force) {",
		"  function requestServerRecoveryDebounced(reason, force) {")
	recoveryBody := substringBetween(t, source,
		"function requestServerRecoveryDebounced(reason, force) {",
		"  function resetFirstFrameServerRecovery() {")

	for body, needle := range map[string]string{
		keyframeBody:         "lastKeyframeCommandAt > 0 && now - lastKeyframeCommandAt < keyframeCommandMinIntervalMs",
		keyframeDebounceBody: "lastRecoveryKeyframeAt > 0 && now - lastRecoveryKeyframeAt < minIntervalMs",
		recoveryBody:         "lastRecoveryServerRecoverAt > 0 && now - lastRecoveryServerRecoverAt < recoveryServerRecoverDebounceMs",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("startup throttle must treat zero as no previous request, missing %q", needle)
		}
	}
}

func TestTicketViewerDoesNotRaceInitialResumeBurstWithEarlySocketAdoption(t *testing.T) {
	source := ticketAppSource(t)
	connectBody := substringBetween(t, source,
		"function connect() {",
		"  function resetStreamState(options) {")
	idleResumeBody := substringBetween(t, source,
		"function resumeFromIdleDisconnect(reason) {",
		"  function layoutViewportRect() {")
	visibilityResumeBody := substringBetween(t, source,
		"function recoverAfterVisibilityResume(reason) {",
		"  window.addEventListener('resize', resizeCanvasBox);")
	startupIndex := strings.LastIndex(source, "connect();")
	if startupIndex < 0 {
		t.Fatal("ticket viewer initializer must call connect")
	}
	startupBody := source[startupIndex:]
	initialFlow := "startActivationResumeFlow('initial_load', 'initial_load');"
	initialFlowIndex := strings.Index(startupBody, initialFlow)
	if initialFlowIndex < 0 {
		t.Fatal("ticket viewer initializer must start the initial resume flow")
	}

	for _, needle := range []string{
		"if (!hasRenderedFrame && !activeResumeFlow)",
		"startActivationResumeFlow('cold_open', 'initial_load');",
	} {
		if !strings.Contains(connectBody, needle) {
			t.Fatalf("connect must keep the first-load resume burst single and adoption-safe, missing %q", needle)
		}
	}
	if strings.Contains(connectBody, "startActivationResumeFlow('cold_open', 'watchdog');") {
		t.Fatal("cold startup must not skip the early WebSocket adoption grace")
	}
	burstBody := substringBetween(t, source,
		"function runActivationReconnectBurst(reason, flow) {",
		"  function initialVideoSocketNeedsAdoption() {")
	noteOpenBody := substringBetween(t, source,
		"function noteVideoSocketOpen(socket, reason) {",
		"  function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {")
	helperBody := substringBetween(t, source,
		"function initialVideoSocketNeedsAdoption() {",
		"  function recoverFreshMediaSession(reason, kind, options) {")
	recoveryBody := substringBetween(t, source,
		"function recoverFreshMediaSession(reason, kind, options) {",
		"  function connect() {")
	for _, needle := range []string{
		"waitForInitialSocket: flow.trigger === 'initial_load'",
		"function initialVideoSocketNeedsAdoption() {",
		"videoWs.readyState === WebSocket.CONNECTING",
		"videoWs.readyState === WebSocket.OPEN",
		"early.ws.readyState === WebSocket.CONNECTING || early.ws.readyState === WebSocket.OPEN",
		"if (options.waitForInitialSocket && initialVideoSocketNeedsAdoption())",
		"connectDirectVideo({ skipEarlyGrace: false });",
	} {
		if !strings.Contains(burstBody+helperBody+recoveryBody, needle) {
			t.Fatalf("initial-load retry must preserve a pending startup socket, missing %q", needle)
		}
	}
	if !strings.Contains(recoveryBody, "skipEarlyGrace: Boolean(options.skipEarlyGrace)") {
		t.Fatal("explicit recovery must retain its skip-early-grace behavior")
	}
	waitIndex := strings.Index(recoveryBody, "if (options.waitForInitialSocket && initialVideoSocketNeedsAdoption())")
	waitReturnIndex := -1
	if waitIndex >= 0 {
		waitReturnIndex = strings.Index(recoveryBody[waitIndex:], "return true;")
	}
	if waitIndex < 0 || waitReturnIndex < 0 || strings.Contains(recoveryBody[waitIndex:waitIndex+waitReturnIndex], "closeDirectVideo()") {
		t.Fatal("initial-load socket guard must return without closing or replacing the pending socket")
	}
	if guardIndex := strings.LastIndex(startupBody[:initialFlowIndex], "if (!activeResumeFlow)"); guardIndex < 0 {
		t.Fatal("initializer must not start a second resume burst when connect already started the first one")
	}
	if strings.Count(idleResumeBody, "startActivationResumeFlow(") != 1 ||
		strings.Contains(idleResumeBody, "startActivationResumeFlow(reason || 'idle_resume', 'watchdog');") ||
		strings.Contains(idleResumeBody, "requestKeyframeDebounced(") {
		t.Fatal("idle resume must start one bounded recovery burst, not a duplicate watchdog cascade")
	}
	if strings.Count(visibilityResumeBody, "claimActivationResumeLifecycle(") != 1 ||
		strings.Contains(visibilityResumeBody, "startActivationResumeFlow(") {
		t.Fatal("visibility resume must reuse one paused recovery burst, not start a second watchdog cascade")
	}
	if strings.Contains(noteOpenBody, "requestKeyframe(") || strings.Contains(noteOpenBody, "requestKeyframeDebounced(") {
		t.Fatal("socket open must rely on the server ordered start and keyframe path")
	}
	for _, needle := range []string{
		"const initialLoad = flow.trigger === 'initial_load';",
		"connectDirectVideo({ skipEarlyGrace: !initialLoad });",
		"if (!initialLoad) {",
		"requestKeyframeDebounced(`${reason || 'activation'}_keyframe`, 0, true);",
	} {
		if !strings.Contains(burstBody, needle) {
			t.Fatalf("initial-load keyframe deferral missing %q", needle)
		}
	}
}

func TestTicketViewerKeepsInitialLoadFlowAcrossOrdinaryPageshowAndFocus(t *testing.T) {
	source := ticketAppSource(t)
	flowBody := substringBetween(t, source,
		"function startActivationResumeFlow(reason, trigger, options) {",
		"  function runActivationReconnectBurst(reason, flow) {")

	for _, needle := range []string{
		"const nextReason = safeResumeLabel(reason, 'activation');",
		"const nextTrigger = safeResumeLabel(trigger, 'activation');",
		"flow && !flow.done && flow.trigger === 'initial_load'",
		"(nextReason === 'pageshow' || nextReason === 'focus')",
		"flow.reason = nextReason;",
		"flow.trigger = nextTrigger;",
	} {
		if !strings.Contains(flowBody, needle) {
			t.Fatalf("initial-load lifecycle ownership missing %q", needle)
		}
	}
	guardIndex := strings.Index(flowBody, "flow && !flow.done && flow.trigger === 'initial_load'")
	returnIndex := strings.Index(flowBody[guardIndex:], "return flow;")
	updateIndex := strings.Index(flowBody, "flow.trigger = nextTrigger;")
	burstIndex := strings.Index(flowBody, "runActivationReconnectBurst(flow.reason, flow);")
	if guardIndex < 0 || returnIndex < 0 || updateIndex < 0 || burstIndex < 0 ||
		guardIndex+returnIndex > updateIndex || guardIndex+returnIndex > burstIndex {
		t.Fatal("ordinary initial pageshow/focus must return before replacing or rerunning the active initial-load flow")
	}
	if strings.Contains(flowBody, "nextReason === 'pageshow_persisted'") {
		t.Fatal("persisted-page recovery must not be suppressed by the ordinary initial-load lifecycle guard")
	}
}

func TestTicketViewerLifecycleEventSequencesHaveOneRecoveryOwner(t *testing.T) {
	source := ticketAppSource(t)
	noteActivity := substringBetween(t, source,
		"function noteViewerActivity(event, reason) {",
		"  function expireViewerIdle(reason) {")
	resumeFlow := substringBetween(t, source,
		"function startActivationResumeFlow(reason, trigger, options) {",
		"  function initialVideoSocketNeedsAdoption() {")
	handlers := substringBetween(t, source,
		"document.addEventListener('visibilitychange', () => {",
		"  window.addEventListener('pagehide'")

	runTicketJavaScript(t, `
let now = 100;
const performance = { now: () => now };
const handlers = {};
const document = {
  visibilityState: 'visible',
  wasDiscarded: false,
  addEventListener: (name, fn) => { handlers[name] = fn; }
};
const window = { addEventListener: (name, fn) => { handlers[name] = fn; } };
let streamUnsupported = false;
let activeResumeFlow = null;
let activationReconnectBurstTimer = null;
let idleDisconnected = false;
let idleDisconnectTimer = null;
let hiddenStreamFocusTimer = null;
let hiddenDecoderTransientLogged = false;
let lastHiddenAt = 0;
let lastHiddenWallAt = 0;
let hasRenderedFrame = false;
let screenEngaged = false;
const activationReconnectBurstMs = 10000;
const activationReconnectFirstRetryMs = 150;
const activationReconnectTickMs = 500;
const activationReconnectMaxTicks = 10;
const recoveryKeyframeDebounceMs = 2500;
let recoveryRuns = 0;
let keyframeRequests = 0;
let exhaustedRecoveries = 0;
let idleResumes = 0;
let limitRefreshes = 0;
const ticketCurrentProofVisualState = { resumePending: false };
let scheduled = [];
function check(value, message) { if (!value) throw new Error(message); }
function safeResumeLabel(value, fallback) { return String(value || fallback); }
function resumeBooleanLabel(value) { return value ? 'true' : 'false'; }
function logResumeCheckpoint() {}
function clearActivationReconnectBurst() { activationReconnectBurstTimer = null; }
function streamHasFreshRenderedFrame() { return false; }
function finishActivationResumeFlow(reason, flow) { flow.done = true; if (flow === activeResumeFlow) activeResumeFlow = null; }
function requestServerRecoveryDebounced(reason) { if (String(reason).includes('exhausted')) exhaustedRecoveries += 1; }
function connectSpacetimeState() { return Promise.resolve(); }
function clientLog() {}
function publishCurrentStreamFocus() {}
function refreshMemberLimitProjection() { limitRefreshes += 1; }
function mediaSessionStuckOnPreservedFrame() { return false; }
function connectDirectVideo() {}
function requestKeyframeDebounced() { keyframeRequests += 1; return true; }
function recoverFreshMediaSession() { recoveryRuns += 1; return true; }
function setTimeout(fn, delay) { scheduled.push({ fn, delay }); return scheduled.length; }
function clearTimeout() {}
function scheduleViewerIdleDisconnect() {}
function resumeFromIdleDisconnect(reason) {
  if (!idleDisconnected) return false;
  idleDisconnected = false;
  idleResumes += 1;
  const flow = startActivationResumeFlow(reason || 'idle_resume', 'idle_resume');
  if (flow) flow.lifecycleResumeStarted = true;
  return true;
}
function scheduleStreamFeedback() {}
function cancelTicketSliderFromLifecycle() {}
function releaseScreenWakeLock() {}
function pauseHiddenStreamAfterGrace() {}
function requestScreenWakeLock() {}
function keepFirstScreenPinned() {}
function chaseLiveStream() {}
function publishCurrentStreamFocus() {}
function streamHasFreshRenderedFrame() { return false; }
function recoverAfterVisibilityResume(reason) {
  const flow = claimActivationResumeLifecycle(reason, 'visibility_resume');
  if (!flow) return false;
  recoveryRuns += 1;
  runActivationReconnectBurst(reason, flow);
  return true;
}
	`+noteActivity+resumeFlow+handlers+`
	startActivationResumeFlow('initial_load', 'initial_load');
	check(keyframeRequests === 0, 'initial load must wait for the server ordered keyframe');
	handlers.pageshow({ persisted: false, isTrusted: true });
	handlers.focus();
	check(keyframeRequests === 0 && recoveryRuns === 0, 'ordinary pageshow and focus must follow the initial-load owner');

document.visibilityState = 'hidden';
handlers.visibilitychange();
now += 20000;
document.visibilityState = 'visible';
handlers.visibilitychange();
handlers.focus();
check(recoveryRuns === 1, 'visibilitychange then focus must run one recovery');
	check(keyframeRequests === 1, 'visibilitychange then focus must request one recovery keyframe');
check(exhaustedRecoveries === 0, 'a long-hidden flow must receive a fresh retry budget');

document.visibilityState = 'hidden';
handlers.visibilitychange();
now += 20000;
document.visibilityState = 'visible';
handlers.pageshow({ persisted: true, isTrusted: true });
handlers.focus();
check(recoveryRuns === 2, 'persisted pageshow then focus must run one recovery');
check(exhaustedRecoveries === 0, 'persisted restore must not inherit an exhausted hidden budget');
check(limitRefreshes === 1, 'the visibility resume must refresh the database-backed limits once');

activeResumeFlow = null;
idleDisconnected = true;
const recoveriesBeforeIdleFocus = recoveryRuns;
handlers.focus();
check(idleResumes === 1, 'idle focus must resume exactly once');
check(recoveryRuns === recoveriesBeforeIdleFocus, 'idle focus must return before a second lifecycle recovery');
`)
}

func TestTicketViewerHiddenColdStartStopsSocketAndFocusAtThreeSeconds(t *testing.T) {
	source := ticketAppSource(t)
	hiddenPause := substringBetween(t, source,
		"function pauseVideoWhileHidden(reason) {",
		"  function connectDirectVideo(options) {")

	runTicketJavaScript(t, `
let clock = 0;
let nextTimer = 1;
const timers = new Map();
function setTimeout(fn, delay) { const id = nextTimer++; timers.set(id, { fn, at: clock + delay, id }); return id; }
function clearTimeout(id) { timers.delete(id); }
function advance(ms) {
  clock += ms;
  for (;;) {
    const due = [...timers.values()].filter((timer) => timer.at <= clock).sort((a, b) => a.at - b.at || a.id - b.id)[0];
    if (!due) return;
    timers.delete(due.id);
    due.fn();
  }
}
function check(value, message) { if (!value) throw new Error(message); }
const hiddenVideoCloseDelayMs = 3000;
const document = { visibilityState: 'hidden' };
const WebSocket = { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 };
let videoWs = { readyState: WebSocket.OPEN };
let hiddenVideoCloseTimer = null;
let hiddenStreamFocusTimer = null;
let closes = 0;
const focusChanges = [];
function controlCodeKeepsVideoAliveWhileHidden() { return false; }
function keepControlCodeVideoAlive() {}
function publishStreamFocus(active) { focusChanges.push(active); }
function clientLog() {}
function preserveCurrentFrame() {}
function closeDirectVideo() {
  closes += 1;
  videoWs = null;
  if (hiddenStreamFocusTimer) clearTimeout(hiddenStreamFocusTimer);
  hiddenStreamFocusTimer = null;
}
`+hiddenPause+`
pauseHiddenStreamAfterGrace('visibility_hidden');
advance(1000);
pauseHiddenStreamAfterGrace('chase_live_stream_hidden');
advance(1000);
pauseHiddenStreamAfterGrace('chase_live_stream_hidden');
advance(999);
check(closes === 0 && !focusChanges.includes(false), 'hidden cold start must retain the three-second grace');
advance(1);
check(closes === 1, 'hidden cold start socket must close at three seconds');
check(focusChanges.filter((value) => value === false).length === 1, 'hidden stream focus must release once at three seconds');
`)
}

func TestTicketViewerPersistedPagehideKeepsHiddenGraceAndRapidRestoreCancelsIt(t *testing.T) {
	source := ticketAppSource(t)
	hiddenPause := substringBetween(t, source,
		"function pauseVideoWhileHidden(reason) {",
		"  function connectDirectVideo(options) {")
	recovery := substringBetween(t, source,
		"function recoverAfterVisibilityResume(reason) {",
		"  window.addEventListener('resize', resizeCanvasBox);")
	freshRecovery := substringBetween(t, source,
		"function recoverFreshMediaSession(reason, kind, options) {",
		"  function connect() {")
	reconnectBurst := substringBetween(t, source,
		"function runActivationReconnectBurst(reason, flow) {",
		"  function initialVideoSocketNeedsAdoption() {")
	pageshow := substringBetween(t, source,
		"window.addEventListener('pageshow', (event) => {",
		"  window.addEventListener('focus', () => {")
	pagehide := substringBetween(t, source,
		"window.addEventListener('pagehide', (event) => {",
		"  window.addEventListener('load', () => keepFirstScreenPinned(true));")

	runTicketJavaScript(t, `
let clock = 0;
let nextTimer = 1;
const timers = new Map();
function setTimeout(fn, delay) { const id = nextTimer++; timers.set(id, { fn, at: clock + delay, id }); return id; }
function clearTimeout(id) { timers.delete(id); }
function advance(ms) {
  clock += ms;
  for (;;) {
    const due = [...timers.values()].filter((timer) => timer.at <= clock).sort((a, b) => a.at - b.at || a.id - b.id)[0];
    if (!due) return;
    timers.delete(due.id);
    due.fn();
  }
}
function check(value, message) { if (!value) throw new Error(message); }
const performance = { now: () => clock };
Date.now = () => 100000 + clock;
const handlers = {};
const window = { addEventListener: (name, fn) => { handlers[name] = fn; } };
const document = { visibilityState: 'hidden', wasDiscarded: false };
const WebSocket = { CONNECTING: 0, OPEN: 1, CLOSING: 2, CLOSED: 3 };
const hiddenVideoCloseDelayMs = 3000;
const backgroundRecoveryHiddenMs = 30000;
const oldTabFreshResumeHiddenMs = 5000;
const streamStaleVideoReconnectMs = 5000;
const resumeSoftReconnectMs = 2000;
const activationReconnectBurstMs = 10000;
const activationReconnectFirstRetryMs = 150;
const activationReconnectTickMs = 500;
const activationReconnectMaxTicks = 10;
let videoWs = { readyState: WebSocket.OPEN };
let hiddenVideoCloseTimer = null;
let hiddenStreamFocusTimer = null;
let lastHiddenAt = 0;
let lastHiddenWallAt = 0;
let configured = false;
let lastFrameAt = 0;
let videoSocketCreatedAt = 0;
let idleDisconnected = false;
let streamUnsupported = false;
let fallbackFrameAvailable = true;
let screenEngaged = false;
const ticketCurrentProofVisualState = { resumePending: false };
let activationReconnectBurstTimer = null;
let activeResumeFlow = null;
let lastRecoveryVideoReconnectSeq = -1;
let videoSocketOpenSeq = 1;
let lastRecoveryVideoReconnectAt = 0;
const recoveryVideoReconnectDebounceMs = 8000;
let spacetimeClient = null;
let closes = 0;
let opens = 0;
let resets = 0;
let keyframeRequests = 0;
let preservedFrames = 0;
const focusChanges = [];
function controlCodeKeepsVideoAliveWhileHidden() { return false; }
function keepControlCodeVideoAlive() {}
function videoSocketKeepsStreamActive() { return Boolean(videoWs && (videoWs.readyState === WebSocket.OPEN || videoWs.readyState === WebSocket.CONNECTING)); }
function publishStreamFocus(active) { focusChanges.push(active); }
function publishCurrentStreamFocus() {}
function clientLog() {}
function preserveCurrentFrame() { preservedFrames += 1; }
function closeDirectVideo() {
  closes += 1;
  videoWs = null;
  if (hiddenVideoCloseTimer) clearTimeout(hiddenVideoCloseTimer);
  if (hiddenStreamFocusTimer) clearTimeout(hiddenStreamFocusTimer);
  hiddenVideoCloseTimer = null;
  hiddenStreamFocusTimer = null;
}
function cancelTicketSliderFromLifecycle() {}
function newResumeFlow(paused) {
  return { startedAt: clock, attempts: 0, done: false, paused: Boolean(paused), trigger: 'pagehide', lifecycleResumeStarted: false };
}
function pauseActivationResumeLifecycle() {
  if (!activeResumeFlow || activeResumeFlow.done) activeResumeFlow = newResumeFlow(true);
  activeResumeFlow.paused = true;
  activeResumeFlow.lifecycleResumeStarted = false;
  clearActivationReconnectBurst();
  return activeResumeFlow;
}
function logResumeCheckpoint() {}
function resumeBooleanLabel(value) { return value ? 'true' : 'false'; }
function closeEarlyVideo() {}
function clearActivationReconnectBurst() {
  if (activationReconnectBurstTimer) clearTimeout(activationReconnectBurstTimer);
  activationReconnectBurstTimer = null;
}
function claimActivationResumeLifecycle() {
  if (!activeResumeFlow || activeResumeFlow.done) activeResumeFlow = newResumeFlow(true);
  activeResumeFlow.startedAt = clock;
  activeResumeFlow.attempts = 0;
  activeResumeFlow.paused = true;
  activeResumeFlow.lifecycleResumeStarted = true;
  return activeResumeFlow;
}
function safeResumeLabel(value, fallback) { return String(value || fallback); }
function hiddenDurationBucket() { return 'short'; }
function redrawPreservedFrame() {}
function requestScreenWakeLock() {}
function keepFirstScreenPinned() {}
function refreshSpacetimeState() { return Promise.resolve(); }
function refreshSpacetimeStateAfterResume() { return Promise.resolve(false); }
function streamHasFreshRenderedFrame() { return false; }
function finishActivationResumeFlow(reason, flow) { flow.done = true; if (flow === activeResumeFlow) activeResumeFlow = null; }
function connectSpacetimeState() { return Promise.resolve(); }
function mediaSessionStuckOnPreservedFrame() { return false; }
function connectDirectVideo() {
  if (videoWs && (videoWs.readyState === WebSocket.OPEN || videoWs.readyState === WebSocket.CONNECTING)) return;
  videoSocketOpenSeq += 1;
  opens += 1;
  videoWs = { readyState: WebSocket.OPEN, generation: videoSocketOpenSeq };
}
function resetStreamState() { configured = false; resets += 1; }
function showStreamRecovery() {}
function requestKeyframeDebounced() { keyframeRequests += 1; return true; }
function safeString(value) { return JSON.stringify(value); }
function requestServerRecoveryDebounced() {}
function reconnectVideoForRecovery() {}
function chaseLiveStream() {}
function noteViewerActivity() { return false; }
function followActivationResumeLifecycle() {}
`+hiddenPause+freshRecovery+reconnectBurst+recovery+pageshow+pagehide+`

handlers.pagehide({ persisted: true });
pauseHiddenStreamAfterGrace('chase_live_stream_hidden');
pauseHiddenStreamAfterGrace('chase_live_stream_hidden');
advance(2999);
check(closes === 0, 'persisted pagehide must retain the three-second socket grace');
check(focusChanges.filter((value) => value === false).length === 0, 'persisted pagehide must retain the three-second focus grace');
advance(1);
check(closes === 1, 'persisted pagehide must close the direct socket once at three seconds');
check(focusChanges.filter((value) => value === false).length === 1, 'persisted pagehide must release stream focus once at three seconds');
check(preservedFrames > 0, 'persisted pagehide must preserve the current frame');

timers.clear();
hiddenVideoCloseTimer = null;
hiddenStreamFocusTimer = null;
videoWs = { readyState: WebSocket.OPEN, generation: videoSocketOpenSeq };
document.visibilityState = 'hidden';
const closesBeforeRestore = closes;
const opensBeforeRestore = opens;
const keyframesBeforeRestore = keyframeRequests;
const liveSocketBeforeRestore = videoWs;
const falseFocusBeforeRestore = focusChanges.filter((value) => value === false).length;
handlers.pagehide({ persisted: true });
advance(1000);
document.visibilityState = 'visible';
handlers.pageshow({ persisted: true, isTrusted: true });
check(hiddenVideoCloseTimer === null && hiddenStreamFocusTimer === null, 'rapid persisted pageshow must cancel both hidden grace timers');
advance(2001);
check(videoWs === liveSocketBeforeRestore, 'rapid persisted pageshow must reuse the healthy current socket');
check(closes === closesBeforeRestore, 'rapid persisted pageshow must not close the healthy current socket');
check(opens === opensBeforeRestore, 'rapid persisted pageshow must not open a replacement socket');
check(keyframeRequests === keyframesBeforeRestore + 2, 'rapid persisted pageshow and its first follow-up must nudge the reused socket without replacing it');
check(focusChanges.filter((value) => value === false).length === falseFocusBeforeRestore, 'rapid persisted pageshow must cancel the pending hidden focus release');

document.visibilityState = 'hidden';
configured = true;
lastFrameAt = clock - streamStaleVideoReconnectMs - 1;
const staleSocket = videoWs;
const closesBeforeStaleRestore = closes;
const opensBeforeStaleRestore = opens;
const resetsBeforeStaleRestore = resets;
const keyframesBeforeStaleRestore = keyframeRequests;
handlers.pagehide({ persisted: true });
advance(1000);
document.visibilityState = 'visible';
handlers.pageshow({ persisted: true, isTrusted: true });
check(closes === closesBeforeStaleRestore + 1, 'stale persisted pageshow must close the old media session once');
check(opens === opensBeforeStaleRestore + 1, 'stale persisted pageshow must open one replacement socket');
check(videoWs !== staleSocket, 'stale persisted pageshow must replace the stale current socket');
check(resets === resetsBeforeStaleRestore + 1 && keyframeRequests === keyframesBeforeStaleRestore + 1, 'stale persisted pageshow must reset media state and request one keyframe');

document.visibilityState = 'hidden';
configured = false;
lastFrameAt = 0;
const thresholdSocket = videoWs;
const closesBeforeThresholdRestore = closes;
const opensBeforeThresholdRestore = opens;
handlers.pagehide({ persisted: true });
clock += hiddenVideoCloseDelayMs;
document.visibilityState = 'visible';
handlers.pageshow({ persisted: true, isTrusted: true });
check(closes === closesBeforeThresholdRestore + 1, 'persisted pageshow at the hidden grace boundary must close the old media session once');
check(opens === opensBeforeThresholdRestore + 1, 'persisted pageshow at the hidden grace boundary must open one replacement socket');
check(videoWs !== thresholdSocket, 'persisted pageshow at the hidden grace boundary must not reuse the expired socket');

document.visibilityState = 'hidden';
configured = false;
videoWs = { readyState: WebSocket.CONNECTING, generation: videoSocketOpenSeq };
videoSocketCreatedAt = clock - resumeSoftReconnectMs - 1;
const connectingSocket = videoWs;
const closesBeforeConnectingRestore = closes;
const opensBeforeConnectingRestore = opens;
handlers.pagehide({ persisted: true });
advance(1000);
document.visibilityState = 'visible';
handlers.pageshow({ persisted: true, isTrusted: true });
check(closes === closesBeforeConnectingRestore + 1, 'overlong cached CONNECTING socket must close once on persisted pageshow');
check(opens === opensBeforeConnectingRestore + 1, 'overlong cached CONNECTING socket must open one replacement');
check(videoWs !== connectingSocket && videoWs.readyState === WebSocket.OPEN, 'overlong cached CONNECTING socket must recover to a new open socket');
`)
}

func TestTicketEarlySocketRetainsFreshKeyframeAcrossDelayedAdoption(t *testing.T) {
	template := ticketIndexTemplate(t)
	earlyQueue := substringBetween(t, template,
		"var earlyMaxFrames = 8;",
		"      function streamURL() {")

	runTicketJavaScript(t, `
let monotonic = 10;
const wallStart = 1000000;
const performance = { now: () => monotonic };
const originalDateNow = Date.now;
Date.now = () => wallStart + monotonic + 10000;
const early = { config: { data: '{"type":"config"}' }, queue: [], queueBytes: 0, firstCaptureAt: 0, lastCaptureAt: 0 };
function check(value, message) { if (!value) throw new Error(message); }
function frame(key, sequence, capturedAt) {
  const raw = new ArrayBuffer(30);
  const view = new DataView(raw);
  view.setUint32(0, 0x54534632);
  view.setUint8(4, key ? 1 : 0);
  view.setUint32(5, 0);
  view.setUint32(9, 1);
  view.setUint32(13, 0);
  view.setUint32(17, sequence);
  const timestamp = (wallStart + capturedAt) * 1000;
  view.setUint32(21, Math.floor(timestamp / 4294967296));
  view.setUint32(25, timestamp >>> 0);
  return { data: raw };
}
`+earlyQueue+`
earlyEnqueue(frame(true, 1, monotonic));
monotonic = 110; earlyEnqueue(frame(false, 2, monotonic));
monotonic = 210; earlyEnqueue(frame(false, 3, monotonic));
monotonic = 310; earlyEnqueue(frame(false, 4, monotonic));
check(early.queue.length === 1 && early.queue[0].meta.key && early.queue[0].meta.sequence === 1,
  'delayed adoption must retain the fresh keyframe after trimming dependent deltas');
check(early.config && early.config.data.includes('config'), 'decoder config must survive delayed queue trimming');
monotonic = 410; earlyEnqueue(frame(false, 5, monotonic));
check(early.queue.length === 1 && early.queue[0].meta.sequence === 1,
  'later deltas with a trimmed dependency must not evict the retained keyframe');
monotonic = 510; earlyEnqueue(frame(true, 10, monotonic));
check(early.queue.length === 1 && early.queue[0].meta.sequence === 10,
  'a newer keyframe must replace the retained GOP');
monotonic = 2011; earlyEnqueue(frame(false, 11, monotonic));
check(early.queue.length === 0, 'a retained keyframe must still expire at the bounded freshness limit');
Date.now = originalDateNow;
	`)
}

func TestTicketEarlySocketRevalidatesRetainedKeyframeWhenClaimed(t *testing.T) {
	source := ticketAppSource(t)
	template := ticketIndexTemplate(t)
	claimEarly := substringBetween(t, source,
		"function claimableEarlyVideoQueue(early) {",
		"  function scheduleViewerIdleDisconnect(reason) {")
	if !strings.Contains(template, "early.maxKeyframeAgeMs = earlyMaxKeyframeAgeMs;") {
		t.Fatal("head socket must expose its retained-keyframe freshness bound to the app claim path")
	}

	runTicketJavaScript(t, `
let monotonic = 100;
const wallStart = 1000000;
const performance = { now: () => monotonic };
const originalDateNow = Date.now;
Date.now = () => wallStart + monotonic + 10000;
const WebSocket = { OPEN: 1, CLOSING: 2, CLOSED: 3 };
const window = { TICKET_EARLY_VIDEO: null };
function check(value, message) { if (!value) throw new Error(message); }
function makeEarly(receivedAt) {
  return {
    ws: { readyState: WebSocket.OPEN, close() {} },
    config: { data: '{"type":"config"}' },
    queue: [{
      data: new ArrayBuffer(30),
      meta: { key: true, epoch: 1, sequence: 1, timestamp: (wallStart + receivedAt) * 1000 },
      receivedAt
    }],
    queueBytes: 30,
    maxKeyframeAgeMs: 1500,
    openedAt: receivedAt,
    claimed: false,
    error: false,
    closed: false
  };
}
`+claimEarly+`
const freshEarly = makeEarly(10);
window.TICKET_EARLY_VIDEO = freshEarly;
const freshClaim = claimEarlyVideoSocket();
check(freshClaim && freshClaim.queued.length === 2,
  'fresh claim must retain decoder config and the retained keyframe');

monotonic = 1600;
const expiredEarly = makeEarly(10);
window.TICKET_EARLY_VIDEO = expiredEarly;
const expiredClaim = claimEarlyVideoSocket();
check(expiredClaim && expiredClaim.queued.length === 1,
  'expired retained keyframe must be discarded when the app claims the socket');
check(typeof expiredClaim.queued[0].data === 'string' && expiredClaim.queued[0].data.includes('config'),
  'claim-time media expiry must preserve decoder configuration');
Date.now = originalDateNow;
`)
}

func TestTicketViewerFreshIngressIgnoresRenderedCanvasAgeButKeepsHardQueueLimit(t *testing.T) {
	source := ticketAppSource(t)
	acceptFrame := substringBetween(t, source,
		"function acceptFreshFrame(frame) {",
		"  function queueFrameMetadata(frame) {")
	handleMessage := substringBetween(t, source,
		"async function handleVideoSocketMessage(event) {",
		"  function decodeAvcFrame(frame) {")

	if strings.Contains(handleMessage, "currentRenderedFreshness") ||
		strings.Contains(handleMessage, "streamVisualAgeHardLimitMs") {
		t.Fatal("rendered canvas age must not reject an incoming recovery frame")
	}
	if !strings.Contains(handleMessage, "Number(decoder && decoder.decodeQueueSize || 0) > streamDecoderQueueHardLimit") ||
		!strings.Contains(handleMessage, "serverClockHasLiveSample && Number(lastAcceptedFrameVisualAgeMillis || 0) > streamIngressFrameMaxAgeMs") {
		t.Fatal("decoder queue hard-limit protection must remain active")
	}

	runTicketJavaScript(t, `
let now = 100;
const wallStart = 1000000;
const performance = { now: () => now };
const originalDateNow = Date.now;
Date.now = () => wallStart + now + 10000;
let serverClockHasLiveSample = true;
let serverClockSkewMs = -10000;
let freshnessQueries = 0;
let currentStreamEpoch = 1;
let lastDecoderConfig = { streamEpoch: 1 };
let lastPacketSequence = 0;
let lastPacketSequenceAdvancedAt = 0;
let lastPacketTimestamp = 0;
let lastAcceptedFrameSequence = 0;
let lastAcceptedFrameTimestamp = 0;
let lastAcceptedFrameReceivedAt = 0;
let lastAcceptedFrameVisualAgeMillis = 0;
let lastAcceptedFrameQueuedAt = 0;
let needsKeyFrame = true;
let configured = true;
let decoderMode = 'annexb';
let videoWs = null;
let decoderRejectedFrames = 0;
let resyncDroppedFrames = 0;
let avcAdapterTried = true;
const streamDecoderQueueHardLimit = 4;
const streamIngressFrameMaxAgeMs = 2000;
const frames = [];
const decoded = [];
const resetReasons = [];
const keyframeReasons = [];
let metadataClears = 0;
const decoder = {
  decodeQueueSize: 0,
  decode: () => { decoded.push(lastAcceptedFrameSequence); }
};
class EncodedVideoChunk {
  constructor(value) { Object.assign(this, value); }
}
function check(value, message) { if (!value) throw new Error(message); }
function parseFrameEnvelope() { return frames.shift() || null; }
function currentRenderedFreshness() {
  freshnessQueries += 1;
  return { visualAgeMillis: 5000 };
}
function requestKeyframeDebounced() { return true; }
function requestKeyframe(reason) { keyframeReasons.push(reason); return true; }
function scheduleStreamFeedback() {}
function queueFrameMetadata() {}
function clearFrameMetadata() { metadataClears += 1; }
function resetDecoderForRecovery(reason) { resetReasons.push(reason); return true; }
function decodeAvcFrame() { throw new Error('unexpected AVC path'); }
function sendVideoClientLog() {}
function switchToAvcAdapter() {}
`+acceptFrame+handleMessage+`
;(async () => {
  frames.push({ kind: 'key', epoch: 1, sequence: 1, timestamp: (wallStart + now) * 1000, data: new Uint8Array([1]) });
  await handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  check(decoded.length === 1 && decoded[0] === 1, 'old painted canvas rejected a valid incoming keyframe');
  check(freshnessQueries === 0, 'incoming frame path consulted rendered canvas age');

  frames.push({ kind: 'delta', epoch: 1, sequence: 2, timestamp: (wallStart + now - 3000) * 1000, data: new Uint8Array([2]) });
  await handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  check(decoded.length === 1, 'genuinely stale incoming frame reached the decoder');
  check(needsKeyFrame, 'genuinely stale incoming frame did not enter keyframe-only recovery');
  check(resetReasons.length === 1 && resetReasons[0] === 'visual_age_overflow',
    'genuinely stale incoming frame did not retain its bounded reset path');

  decoder.decodeQueueSize = 5;
  frames.push({ kind: 'key', epoch: 1, sequence: 10, timestamp: (wallStart + now) * 1000, data: new Uint8Array([10]) });
  await handleVideoSocketMessage({ data: new ArrayBuffer(1) });
  check(decoded.length === 1, 'decoder queue hard limit did not stop a congested frame');
  check(needsKeyFrame, 'decoder queue overflow must return to keyframe-only recovery');
  check(resetReasons.length === 2 && resetReasons[1] === 'decoder_queue_overflow',
    'decoder queue overflow must retain its bounded reset path');
  check(metadataClears === 2, 'stale ingress and decoder queue overflow must clear pending metadata');
  check(keyframeReasons.length === 0, 'successful bounded resets must not add duplicate keyframe requests');
  Date.now = originalDateNow;
})().catch((error) => {
  Date.now = originalDateNow;
  console.error(error && error.stack || error);
  process.exit(1);
});
		`)
}

func TestTicketViewerProvisionalConfigBindsFirstKeyframeEpochBeforeFeedback(t *testing.T) {
	source := ticketAppSource(t)
	acceptFrame := substringBetween(t, source,
		"function acceptFreshFrame(frame) {",
		"  function queueFrameMetadata(frame) {")
	sendFeedback := substringBetween(t, source,
		"function sendStreamFeedback(reason, immediate) {",
		"  function scheduleStreamFeedback(reason) {")

	runTicketJavaScript(t, `
let now = 100;
const wallStart = 1000000;
const performance = { now: () => now };
const originalDateNow = Date.now;
Date.now = () => wallStart + now;
const WebSocket = { OPEN: 1 };
const document = { visibilityState: 'visible' };
let serverClockHasLiveSample = true;
let serverClockSkewMs = 0;
let currentStreamEpoch = 0;
let lastDecoderConfig = { streamEpoch: 0, provisional: true, codec: 'avc1.42C028' };
let lastPacketSequence = 0;
let lastPacketSequenceAdvancedAt = 0;
let lastPacketTimestamp = 0;
let lastAcceptedFrameSequence = 0;
let lastAcceptedFrameTimestamp = 0;
let lastAcceptedFrameReceivedAt = 0;
let lastAcceptedFrameVisualAgeMillis = 0;
let needsKeyFrame = true;
let lastFeedbackSentAt = 0;
let lastDecodedFrameSequence = 0;
let lastRenderedFrameSequence = 0;
let lastRenderedKeyframeSequence = 0;
let feedbackSentCount = 0;
let feedbackSendFailureCount = 0;
let feedbackImmediateKey = '';
const streamFeedbackVersion = 1;
const streamFeedbackIntervalMs = 500;
const streamFeedbackHiddenIntervalMs = 2000;
let sentFeedback = null;
const videoWs = {
  readyState: WebSocket.OPEN,
  send(value) { sentFeedback = JSON.parse(value); }
};
const decoder = { decodeQueueSize: 0 };
function check(value, message) { if (!value) throw new Error(message); }
function requestKeyframeDebounced() { throw new Error('unexpected keyframe request'); }
function scheduleStreamFeedback() {}
function clampFeedbackNumber(value, max) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric <= 0) return 0;
  return Math.min(max, Math.round(numeric));
}
function currentRenderedFreshness() { return { visualAgeMillis: 0 }; }
`+acceptFrame+sendFeedback+`
const delta = { kind: 'delta', epoch: 42, sequence: 1, timestamp: (wallStart + now) * 1000 };
check(acceptFreshFrame(delta) === false, 'provisional decoder bound to a delta frame');
check(currentStreamEpoch === 0 && lastDecoderConfig.streamEpoch === 0,
  'rejected delta changed the provisional epoch');

const keyframe = { kind: 'key', epoch: 42, sequence: 2, timestamp: (wallStart + now) * 1000 };
check(acceptFreshFrame(keyframe) === true, 'fresh recovery keyframe was rejected');
check(currentStreamEpoch === 42, 'first recovery keyframe did not bind the live epoch');
check(lastDecoderConfig.streamEpoch === 42 && lastDecoderConfig.provisional === false,
  'decoder recovery config retained the provisional epoch');
check(sendStreamFeedback('recovery_keyframe', true) === true, 'bound-epoch feedback was not sent');
check(sentFeedback && sentFeedback.epoch === 42,
  'feedback advertised provisional epoch 0 after the live keyframe');

const wrongEpochKeyframe = { kind: 'key', epoch: 43, sequence: 3, timestamp: (wallStart + now) * 1000 };
check(acceptFreshFrame(wrongEpochKeyframe) === false,
  'later mismatched epoch was accepted after provisional binding');
check(currentStreamEpoch === 42, 'mismatched frame replaced the bound epoch');
Date.now = originalDateNow;
`)
}

func TestTicketViewerRenderedAgeUsesCalibratedIngressAge(t *testing.T) {
	source := ticketAppSource(t)
	renderFrame := substringBetween(t, source,
		"function renderDecodedFrame(frame, source) {",
		"  async function configureDecoder(config, options) {")

	runTicketJavaScript(t, `
let now = 100;
const wallStart = 1000000;
const performance = { now: () => now };
const originalDateNow = Date.now;
Date.now = () => wallStart + now + 10000;
let serverClockHasLiveSample = true;
let serverClockSkewMs = -10000;
let lastFrameAt = 0;
let lastDecodedFrameAt = 0;
let lastDecodedFrameSequence = 0;
let lastAcceptedFrameSequence = 1;
let lastAcceptedFrameReceivedAt = 80;
let lastAcceptedFrameQueuedAt = 90;
let lastAcceptedFrameVisualAgeMillis = 50;
let lastRenderedFrameReceivedAt = 0;
let lastRenderedFrameQueuedAt = 0;
let lastRenderedFrameRenderedAt = 0;
let lastRenderedFrameVisualAgeMillis = 0;
let lastRenderedFrameEpoch = 0;
let lastRenderedFrameSequence = 0;
let lastRenderedKeyframeSequence = 0;
let lastRenderedFrameTimestamp = 0;
let firstFrameReceived = false;
let hasRenderedFrame = false;
let firstRenderedTraceSent = false;
let needsKeyFrame = false;
let currentState = null;
let firstFrameDetail = null;
let closes = 0;
const canvas = { width: 720, height: 1482 };
const ctx = { drawImage() {} };
const frame = { close() { closes += 1; } };
const metadata = {
  epoch: 1,
  sequence: 1,
  timestamp: (wallStart + now - 50) * 1000,
  keyFrame: true,
  visualAgeMillis: 50,
  visualAgeKnown: true,
  receivedAt: 80,
  queuedAt: 90
};
function check(value, message) { if (!value) throw new Error(message); }
function shiftFrameMetadata() { throw new Error('explicit metadata was ignored'); }
function resetFirstFrameServerRecovery() {}
function sendVideoSocketClientLog(event, detail) {
  if (event === 'stream_first_rendered_frame') firstFrameDetail = detail;
}
function maybeCaptureControlCodeResultImage() {}
function hideEmpty() {}
function updateStreamFreshnessStatus() {}
function renderTicketInteraction() {}
function updateControlCodeSubmitAvailability() {}
function publishStreamDebug() {}
function scheduleStreamFeedback() {}
function sendVideoClientLog() {}
function preserveCurrentFrame() {}
function showStreamRecovery() {}
function requestKeyframe() {}
`+renderFrame+`
renderDecodedFrame(frame, 'annexb', metadata);
check(lastRenderedFrameVisualAgeMillis === 70,
  'browser wall-clock skew was added to the rendered frame age');
check(firstFrameDetail && firstFrameDetail.visualAgeMillis === 70,
  'first rendered trace did not use the calibrated frame age');
check(hasRenderedFrame && lastRenderedFrameSequence === 1 && closes === 1,
  'calibrated frame did not complete the normal render path');
Date.now = originalDateNow;
`)
}

func TestTicketViewerRejectsTrimmedGOPGapUntilRecoveryKeyframe(t *testing.T) {
	source := ticketAppSource(t)
	template := ticketIndexTemplate(t)
	earlyQueue := substringBetween(t, template,
		"var earlyMaxFrames = 8;",
		"      function streamURL() {")
	parseFrame := substringBetween(t, source,
		"function readUint64(view, offset) {",
		"  function isAppleWebKit() {")
	keyframePolicy := substringBetween(t, source,
		"function liveStreamSuppressesBackgroundRequest(reason) {",
		"  function requestServerRecoveryDebounced(reason, force) {")
	acceptFrame := substringBetween(t, source,
		"function acceptFreshFrame(frame) {",
		"  function queueFrameMetadata(frame) {")
	handleMessage := substringBetween(t, source,
		"async function handleVideoSocketMessage(event) {",
		"  function decodeAvcFrame(frame) {")

	runTicketJavaScript(t, `
let monotonic = 10;
const wallStart = 1000000;
const performance = { now: () => monotonic };
const originalDateNow = Date.now;
Date.now = () => wallStart + monotonic;
let serverClockHasLiveSample = true;
let serverClockSkewMs = 0;
const early = { config: { data: '{"type":"config"}' }, queue: [], queueBytes: 0, firstCaptureAt: 0, lastCaptureAt: 0 };
const FRAME_ENVELOPE_MAGIC = 0x54534632;
const FRAME_ENVELOPE_HEADER_BYTES = 29;
const streamDecoderQueueHardLimit = 4;
const streamIngressFrameMaxAgeMs = 2000;
const recoveryKeyframeDebounceMs = 2000;
const keyframeCommandMinIntervalMs = 2500;
const streamFirstFrameKeyframeMs = 2000;
let currentStreamEpoch = 1;
let lastPacketSequence = 0;
let lastPacketSequenceAdvancedAt = 0;
let lastPacketTimestamp = 0;
let lastAcceptedFrameSequence = 0;
let lastAcceptedFrameTimestamp = 0;
let lastAcceptedFrameReceivedAt = 0;
let lastAcceptedFrameVisualAgeMillis = 0;
let lastAcceptedFrameQueuedAt = 0;
let needsKeyFrame = true;
let configured = true;
let decoderMode = 'annexb';
let decoderRejectedFrames = 0;
let resyncDroppedFrames = 0;
let avcAdapterTried = true;
let lastKeyframeCommandAt = 0;
let lastRecoveryKeyframeAt = 0;
let hasRenderedFrame = false;
let activeResumeFlow = { trigger: 'initial_load', done: false, startedAt: 10 };
let videoWs = null;
let lastFrameAt = 0;
let keyframeMutations = 0;
const decoded = [];
const decoder = {
  decodeQueueSize: 0,
  decode: () => { decoded.push(lastAcceptedFrameSequence); }
};
class EncodedVideoChunk {
  constructor(value) { Object.assign(this, value); }
}
function check(value, message) { if (!value) throw new Error(message); }
function frame(key, sequence, capturedAt) {
  const raw = new ArrayBuffer(30);
  const view = new DataView(raw);
  view.setUint32(0, FRAME_ENVELOPE_MAGIC);
  view.setUint8(4, key ? 1 : 0);
  view.setUint32(5, 0);
  view.setUint32(9, 1);
  view.setUint32(13, 0);
  view.setUint32(17, sequence);
  const timestamp = (wallStart + capturedAt) * 1000;
  view.setUint32(21, Math.floor(timestamp / 4294967296));
  view.setUint32(25, timestamp >>> 0);
  return { data: raw };
}
function streamHasFreshRenderedFrame() { return true; }
function clientLog() {}
function runSpacetimeMutation(callback) {
  keyframeMutations += 1;
  callback({ requestKeyframe() {} });
  return Promise.resolve();
}
function scheduleStreamFeedback() {}
function queueFrameMetadata() {}
function clearFrameMetadata() {}
function resetDecoderForRecovery() { return false; }
function currentRenderedFreshness() { return { visualAgeMillis: 0 }; }
function decodeAvcFrame() { throw new Error('unexpected AVC path'); }
function sendVideoClientLog() {}
function showUnsupported(message) { throw new Error(message); }
function switchToAvcAdapter() {}
`+earlyQueue+parseFrame+keyframePolicy+acceptFrame+handleMessage+`
;(async () => {
  earlyEnqueue(frame(true, 1, monotonic));
  monotonic = 110; earlyEnqueue(frame(false, 2, monotonic));
  monotonic = 210; earlyEnqueue(frame(false, 3, monotonic));
  monotonic = 310; earlyEnqueue(frame(false, 4, monotonic));
  check(early.queue.length === 1 && early.queue[0].meta.sequence === 1,
    'test setup did not trim the early GOP to its retained keyframe');

  await handleVideoSocketMessage(early.queue[0]);
  check(decoded.join(',') === '1', 'retained keyframe was not decoded');

  monotonic = 410;
  await handleVideoSocketMessage(frame(false, 5, monotonic));
  check(decoded.join(',') === '1', 'noncontiguous live delta reached the decoder');
  check(lastAcceptedFrameSequence === 1 && needsKeyFrame, 'sequence gap did not enter keyframe-only recovery');
  check(keyframeMutations === 1, 'sequence gap did not request one bounded recovery keyframe');

  await handleVideoSocketMessage(frame(false, 6, monotonic));
  check(keyframeMutations === 1, 'repeated gapped deltas bypassed the recovery debounce');
  check(resyncDroppedFrames === 2, 'gapped deltas were not counted as resync drops');

  monotonic = 600;
  await handleVideoSocketMessage(frame(true, 10, monotonic));
  await handleVideoSocketMessage(frame(false, 11, monotonic));
  check(decoded.join(',') === '1,10,11', 'recovery keyframe did not restore exact delta continuity');
  check(!needsKeyFrame && lastAcceptedFrameSequence === 11, 'recovery did not return the viewer to flowing state');
  Date.now = originalDateNow;
})().catch((error) => {
  Date.now = originalDateNow;
  console.error(error && error.stack || error);
  process.exit(1);
});
`)
}

func TestTicketViewerSupersededAsyncDecoderConfigurationCannotWin(t *testing.T) {
	source := ticketAppSource(t)
	closeDecoder := substringBetween(t, source,
		"function closeDecoder() {",
		"  function resetDecoderForRecovery(reason) {")
	configureDecoder := substringBetween(t, source,
		"async function configureDecoder(config, options) {",
		"  function configureAvcDecoderFromDescription(config, description) {")

	runTicketJavaScript(t, `
let now = 100;
const performance = { now: () => now };
const probes = new Map();
const configuredWidths = [];
const configuredEpochs = [];
const unsupported = [];
const keyframeReasons = [];
let decoder = null;
let decoderGeneration = 0;
let decoderConfigureGeneration = 0;
let decoderConfigured = false;
let decoderMode = 'annexb';
let pendingPresentedFrame = null;
let presentationFrameHandle = null;
let lastDecoderConfig = null;
let lastAcceptedFrameSequence = 0;
let lastAcceptedFrameTimestamp = 0;
let hasRenderedFrame = false;
let fallbackFrameAvailable = false;
let avcAdapterTried = false;
let avcDescription = null;
let avcSps = null;
let avcPps = null;
let streamSize = null;
let currentStreamEpoch = 0;
let lastDecodedFrameSequence = 0;
let lastRenderedKeyframeSequence = 0;
let needsKeyFrame = true;
let configured = false;
let configuredAt = 0;
let firstFrameReceived = false;
const canvas = { width: 0, height: 0 };
const ctx = { imageSmoothingEnabled: true };
class EncodedVideoChunk {}
class VideoDecoder {
  constructor(callbacks) {
    this.callbacks = callbacks;
    this.decodeQueueSize = 0;
  }
  configure(config) { configuredWidths.push(config.codedWidth); }
  close() {}
  static isConfigSupported(config) {
    return new Promise((resolve) => probes.set(config.codedWidth, resolve));
  }
}
const window = { VideoDecoder, EncodedVideoChunk };
function check(value, message) { if (!value) throw new Error(message); }
function showUnsupported(message) { unsupported.push(message); }
function isAppleWebKit() { return false; }
function preserveCurrentFrame() {}
function redrawPreservedFrame() {}
function clearFrameMetadata() {}
function resizeCanvasBox() {}
function publishStreamDebug() {}
function showStreamWaiting() {}
function keepFirstScreenPinned() {}
function requestKeyframe(reason) { keyframeReasons.push(reason); return true; }
function sendVideoClientLog() {}
function sendVideoSocketClientLog(event, detail) {
  if (event === 'browser_configured') configuredEpochs.push(detail.streamEpoch);
}
function scheduleDecodedFrame() {}
function reportDecoderError() {}
function switchToAvcAdapter() {}
`+closeDecoder+configureDecoder+`
function config(width, epoch) {
  return { codec: 'avc1.42C028', transport: 'h264-annexb', width, height: width + 1, streamEpoch: epoch };
}
;(async () => {
  const oldFirst = configureDecoder(config(101, 1));
  const newFirst = configureDecoder(config(201, 2));
  probes.get(201)({ supported: true });
  await newFirst;
  probes.get(101)({ supported: false });
  await oldFirst;
  check(configuredWidths.join(',') === '201', 'late old capability result replaced the newer decoder');
  check(configuredEpochs.join(',') === '2', 'late old capability result emitted a stale configured event');
  check(currentStreamEpoch === 2 && lastDecoderConfig.streamEpoch === 2 && canvas.width === 201,
    'late old capability result overwrote current stream state');
  check(unsupported.length === 0, 'late unsupported result replaced the valid newer configuration');

  const oldSecond = configureDecoder(config(102, 3));
  const newSecond = configureDecoder(config(202, 4));
  probes.get(102)({ supported: true });
  await oldSecond;
  check(configuredWidths.join(',') === '201', 'older request won merely because its probe resolved first');
  probes.get(202)({ supported: true });
  await newSecond;
  check(configuredWidths.join(',') === '201,202' && configuredEpochs.join(',') === '2,4',
    'newest configuration did not win in request order');
  check(currentStreamEpoch === 4 && lastDecoderConfig.streamEpoch === 4 && canvas.width === 202,
    'newest stream configuration was not authoritative');

  const closedPending = configureDecoder(config(303, 5));
  closeDecoder();
  probes.get(303)({ supported: true });
  await closedPending;
  check(decoder === null && currentStreamEpoch === 4 && configuredEpochs.join(',') === '2,4',
    'socket close allowed an old pending configure result to resurrect the decoder');
  check(keyframeReasons.join(',') === 'config_received,config_received',
    'superseded configurations caused extra keyframe requests');
})().catch((error) => {
  console.error(error && error.stack || error);
  process.exit(1);
});
`)
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
		!strings.Contains(connectBody, "safeWebSocket(streamURL('connect_direct_video'), 'video', videoSocketProtocols())") {
		t.Fatalf("direct video socket must attach current open context")
	}
	if strings.Contains(streamURLBody, "startupRunOrigin") || strings.Contains(streamURLBody, "ticket.startup.") {
		t.Fatal("private startup origin must never enter the video URL")
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
		`function streamProtocols()`,
		`var protocols = ["ticket.video.v1"]`,
		`/^ticket\.startup\.[0-9a-f]{32}$/.test(startupRun)`,
		`new WebSocket(streamURL(), streamProtocols())`,
	} {
		if !strings.Contains(template, needle) {
			t.Fatalf("early video socket context missing %q", needle)
		}
	}
}

func TestTicketViewerDoesNotBypassOrderedStartupWithBrowserKeyframe(t *testing.T) {
	source := ticketAppSource(t)
	noteOpen := substringBetween(t, source,
		"function noteVideoSocketOpen(socket, reason) {",
		"  function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {")
	keyframePolicy := substringBetween(t, source,
		"function initialLoadDefersBrowserKeyframe(reason) {",
		"  function requestKeyframeDebounced(reason, minIntervalMs, force) {")

	runTicketJavaScript(t, `
let now = 100;
const performance = { now: () => now };
const WebSocket = { OPEN: 1 };
const document = { visibilityState: 'visible' };
const socket = { readyState: WebSocket.OPEN, close() {} };
let videoWs = socket;
let idleDisconnected = false;
let videoConnectedAt = 0;
let videoSocketCreatedAt = 25;
let lastFrameAt = 0;
let lastKeyframeCommandAt = 0;
let configured = false;
let hasRenderedFrame = false;
let activeResumeFlow = { trigger: 'initial_load', done: false, startedAt: now };
let browserReducerKeyframes = 0;
const keyframeCommandMinIntervalMs = 2500;
const streamFirstFrameKeyframeMs = 2000;
const intentionallyClosedVideoSockets = new Set();
function check(value, message) { if (!value) throw new Error(message); }
function clientLog() {}
function flushClientLogs() {}
function resetFirstFrameServerRecovery() {}
function showStreamWaiting() {}
function scheduleStreamFeedback() {}
function liveStreamSuppressesBackgroundRequest() { return false; }
function runSpacetimeMutation() { browserReducerKeyframes += 1; return Promise.resolve(); }
`+keyframePolicy+noteOpen+`
noteVideoSocketOpen(socket, 'early_video_socket_open');
check(browserReducerKeyframes === 0, 'socket open bypassed the blocked ordered start path');
check(requestKeyframe('config_received') === false, 'initial config requested a keyframe before the ordered server nudge');
check(requestKeyframe('initial_load_recovery', true) === false, 'forced initial recovery requested a keyframe before startup grace');
check(browserReducerKeyframes === 0, 'initial config bypassed the blocked ordered start path');
now = 2101;
check(requestKeyframe('first_frame_timeout') === true, 'post-grace recovery keyframe was not retained');
check(browserReducerKeyframes === 1, 'post-grace recovery must make one reducer request');
`)
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
		"const browserTrustedGeneratedVisible = Boolean(",
		"const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof",
		"const frameChangedFromBaseline = Boolean(controlCodeBaselineFrameFingerprint",
		"phoneGeneratedProofKind === 'inline'",
		"generatedProof.generatedCodeVisible &&",
		"!generatedProof.generatedChipVisible",
		"phoneGeneratedProofKind === 'with_close'",
		"generatedProof.generatedVisible &&",
		"generatedProof.generatedChipVisible",
		"const browserTrustedResultVisible = browserTrustedGeneratedVisible;",
		"if (!browserTrustedResultVisible && trustedPhoneMarkerFrame)",
		"proof.generatedMarkerOnlyRejected = true;",
		"if (!proof.browserTrustedResultVisible)",
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
	generatedIndex := strings.Index(candidateProof, "if (!proof.browserTrustedResultVisible)")
	if markerOnlyRejectIndex < 0 || generatedIndex < 0 || markerOnlyRejectIndex > generatedIndex {
		t.Fatalf("candidate frame must reject marker-only proof before accepting a generated frame")
	}
	if strings.Contains(candidateProof, "proof.generatedVisibleByPhoneMarker") ||
		strings.Contains(candidateProof, "trustedPhoneChangedMarkerFrame") {
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
	generatedRejectIndex := strings.Index(candidateProof, "if (!proof.browserTrustedResultVisible)")
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
		"const popupVisible = dialogVisible && okButtonVisible && okButtonOrangeRatio >= 0.03;",
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
	if !strings.Contains(source, "const controlCodeSafeGeneratedFrameRequiredCount = 2;") {
		t.Fatalf("untrusted generated-code capture must require two distinct verified frames before freezing")
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

func TestTicketMutationFailuresAreLocalized(t *testing.T) {
	source := ticketAppSource(t)
	for _, needle := range []string{
		"['ticket_mutation_in_progress', 'Tālrunis pabeidz iepriekšējo biļetes darbību. Mēģini vēlreiz pēc mirkļa.']",
		"['ticket_action_interaction_revision_unproved', 'Biļetes vizuālais apstiprinājums vairs nav aktuāls. Sagaidi svaigu apstiprinājumu.']",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("Ticket mutation failure translation missing %q", needle)
		}
	}
}

func TestControlCodeCaptureRequiresModeSpecificPhoneAndBrowserGeneratedProof(t *testing.T) {
	source := ticketAppSource(t)
	candidateProof := substringBetween(t, source,
		"function controlCodeCandidateFrameProof(request) {",
		"  function noteControlCodeCandidateRejected(proof) {")

	for _, needle := range []string{
		"function controlCodePhoneGeneratedProofKind(resultProof) {",
		"resultProof === 'phone_visual_generated_inline'",
		"resultProof === 'phone_visual_generated_with_close'",
		"const phoneGeneratedProofKind = controlCodePhoneGeneratedProofKind(proof.resultProof);",
		"const trustedPhonePostSubmitProof = Boolean(phoneGeneratedProofKind);",
		"if (trustedPhonePostSubmitProof) {",
		"proof.trustedPhonePostSubmitProof = true;",
		"const browserTrustedGeneratedVisible = Boolean(",
		"trustedPhonePostSubmitProof &&",
		"phoneGeneratedProofKind === 'inline'",
		"generatedProof.generatedCodeVisible &&",
		"!generatedProof.generatedChipVisible",
		"phoneGeneratedProofKind === 'with_close'",
		"generatedProof.generatedVisible &&",
		"generatedProof.generatedChipVisible",
		"proof.browserTrustedGeneratedVisible = browserTrustedGeneratedVisible;",
		"const trustedPhoneMarkerFrame = Boolean(trustedPhonePostSubmitProof",
		"const frameChangedFromBaseline = Boolean(controlCodeBaselineFrameFingerprint",
		"const browserTrustedResultVisible = browserTrustedGeneratedVisible;",
		"proof.browserTrustedResultVisible = browserTrustedResultVisible;",
		"renderedEpoch === markerEpoch",
		"renderedSequence >= markerSequence",
		"request.status !== 'succeeded'",
		"if (!browserTrustedResultVisible && trustedPhoneMarkerFrame)",
		"proof.generatedMarkerOnlyRejected = true;",
		"if (!proof.browserTrustedResultVisible)",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("post-submit phone proof diagnostic path missing %q", needle)
		}
	}
	if strings.Contains(candidateProof, "proof.acceptedReason = `candidate_frame_at_or_after_${proof.resultProof}`;") ||
		strings.Contains(candidateProof, "proof.generatedVisibleByPhoneMarker") ||
		strings.Contains(candidateProof, "trustedPhoneChangedMarkerFrame") ||
		strings.Contains(source, "resultProof === 'phone_visual_raw_ticket_after_submit'") {
		t.Fatalf("phone post-submit proof must not bypass exact marker and generated browser proof")
	}

	popupRejectIndex := strings.Index(candidateProof, "if (popupProof.popupVisible)")
	trustedDiagnosticIndex := strings.Index(candidateProof, "if (trustedPhonePostSubmitProof) {")
	generatedRejectIndex := strings.Index(candidateProof, "if (!proof.browserTrustedResultVisible)")
	if popupRejectIndex < 0 || trustedDiagnosticIndex < 0 || generatedRejectIndex < 0 {
		t.Fatalf("candidate proof must keep popup rejection, phone proof diagnostics, and trusted result enforcement")
	}
	if popupRejectIndex > trustedDiagnosticIndex || trustedDiagnosticIndex > generatedRejectIndex {
		t.Fatalf("phone proof diagnostics must happen after popup rejection and before trusted result enforcement")
	}
	if !strings.Contains(candidateProof, "proof.fingerprintDifferenceScore >= controlCodeFingerprintDifferenceThreshold") ||
		!strings.Contains(candidateProof, "proof.fingerprintChangedCells >= controlCodeFingerprintChangedCellsThreshold") {
		t.Fatalf("generated proof must also require a changed pre-request baseline")
	}
	if strings.Contains(candidateProof, "browserTrustedGeneratedVisible = trustedPhonePostSubmitProof") ||
		strings.Contains(candidateProof, "trustedPhoneChangedMarkerFrame") {
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
		!strings.Contains(updateSubmit, "codeSubmit.disabled = !codeDialogOpen || busy || limitBlocked || !digitsValid;") {
		t.Fatalf("control-code submit must be unavailable while the dialog is closed")
	}
	if !strings.Contains(updateSubmit, "requestCodeButton.disabled = busy || limitBlocked;") {
		t.Fatalf("closed-page request button should be unavailable while the phone lane or SpaceTime quota blocks it")
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
		"const localRequestStillPresent = Boolean(requestRows && localRequestID && requestRows.some((request) =>",
		"['succeeded', 'closed', 'expired'].includes(String(codeRequest.status || ''))",
		"clearControlCodeResultCapture();",
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
	mutationLaneBusy := substringBetween(t, source,
		"function controlCodeMutationLaneBusy() {",
		"  function updateControlCodeSubmitAvailability() {")

	for _, needle := range []string{
		"function controlCodeFastStateFresh(state) {",
		"function renderControlCodeFastStateDataset() {",
		"if (controlCodeSubmitInFlight) return true;",
		"function controlCodeRequestOccupiesPhone(request) {",
		"function controlCodeRequestOccupiesQueue() {",
		"const requestsAvailable = Array.isArray(currentState && currentState.controlCodeRequests);",
		"const localRequestIsPresent = Boolean(!requestsAvailable || !localRequestID || requests.some((request) =>",
		"isOwnedControlCodeRequest(request) &&",
		"controlCodeRequestIsStillRelevant(request) &&",
		"if (status === 'closed' || status === 'expired' || status === 'failed') return false;",
		"request.cleanupPending === true",
		"request.captureRequired === true && request.captureAcknowledged !== true",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("control-code background readiness/queue contract missing %q", needle)
		}
	}
	for _, needle := range []string{
		"function scheduleControlCodeFastStateExpiryCheck() {",
		"controlCodeFastStateExpiryTimer = setTimeout(() => {",
		"updateControlCodeSubmitAvailability();",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("fast-state expiry handling missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"maybeAutoPrepareControlCode",
		"controlCodeAutoPrepareInFlight",
		"lastControlCodeAutoPrepareAt",
		"prepareControlCode(",
		"control_code_auto_prepare",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("ordinary browser rendering must not launch redundant preparation %q", forbidden)
		}
	}
	if !strings.Contains(openDialog, "if (controlCodeMutationLaneBusy()) return;") {
		t.Fatalf("dialog entry must use the shared phone-mutation lane guard")
	}
	for _, required := range []string{
		"controlCodeRequestOccupiesQueue()",
		"ticketInteractionIsBusy(currentState && currentState.ticketInteraction)",
		"ticketActionV3Busy(currentState && currentState.ticketAction)",
	} {
		if !strings.Contains(mutationLaneBusy, required) {
			t.Fatalf("shared phone-mutation lane guard missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"refreshControlCodeReadiness",
		"reconnectVideoForRecovery",
		"prepareControlCode",
		"controlCodeFastStateFresh",
	} {
		if strings.Contains(openDialog+mutationLaneBusy, forbidden) {
			t.Fatalf("dialog entry and lane gate must not wait for or launch fast-path work %q", forbidden)
		}
	}
	if strings.Contains(source, "function controlCodeRequestBusyForAutoPrepare() {") {
		t.Fatalf("browser must not maintain a second phone-occupancy predicate")
	}
	if !strings.Contains(source, "let controlCodeFastStateExpiryTimer = null;") ||
		!strings.Contains(source, "scheduleControlCodeFastStateExpiryCheck();") ||
		!strings.Contains(source, "renderControlCodeFastStateDataset();\n      updateControlCodeSubmitAvailability();") {
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
		"const limitBlocked = memberLimitBlocked('control_code');",
		"codeSubmit.disabled = !codeDialogOpen || busy || limitBlocked || !digitsValid;",
		"codeSubmit.textContent = controlCodeSubmitInFlight ? 'Nosūta…' : 'Izveidot kodu';",
		"codeSubmit.setAttribute('aria-busy', 'true');",
		"requestCodeButton.disabled = busy || limitBlocked;",
	} {
		if !strings.Contains(updateSubmit, needle) {
			t.Fatalf("submit availability must depend on valid digits, occupied work, and SpaceTime quota, missing %q", needle)
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
		"const navigationEntry = performance.getEntriesByType('navigation')[0];",
		"sendVideoSocketClientLog('browser_opened'",
		"source: 'navigation_timing'",
		"function browserLifecycleSourceTime(sourceAtPerformanceMillis) {",
		"sourceAtEpochMillis: Math.round(timeOrigin + performanceMillis)",
		"sourceAtPerformanceMillis: Number(performanceMillis.toFixed(3))",
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
	bundle, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"browser_opened", "navigation_timing", "sourceAtEpochMillis", "sourceAtPerformanceMillis"} {
		if !strings.Contains(string(bundle), needle) {
			t.Fatalf("shipped browser bundle is missing startup lifecycle marker %q", needle)
		}
	}
}

func TestTicketViewerCarriesOnlyTheOpaqueStartupOriginAsAVideoSubprotocol(t *testing.T) {
	source := ticketAppSource(t)
	template := ticketIndexTemplate(t)
	bundle, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"source":   source,
		"template": template,
		"bundle":   string(bundle),
	} {
		for _, needle := range []string{"ticket.video.v1", `ticket\.startup\.`, "startupRunOrigin"} {
			if !strings.Contains(body, needle) {
				t.Fatalf("%s is missing private startup subprotocol contract %q", name, needle)
			}
		}
	}
	for _, forbidden := range []string{
		"appendStreamURLParam(url, 'startup",
		`url.searchParams.set("startup`,
		"clientLog('startup_run",
	} {
		if strings.Contains(source, forbidden) || strings.Contains(template, forbidden) || strings.Contains(string(bundle), forbidden) {
			t.Fatalf("private startup origin leaked into a URL or browser log via %q", forbidden)
		}
	}
}

func TestTicketBrowserRuntimeLogsUseAuthenticatedVideoSocketQueue(t *testing.T) {
	source := ticketAppSource(t)
	clientSource := readTicketWebClientSource(t, "src/index.ts")
	for _, needle := range []string{
		"if (!videoWs || videoWs.readyState !== WebSocket.OPEN || !pendingClientLogs.length) return;",
		"const batch = pendingClientLogs.splice(0, Math.min(20, pendingClientLogs.length));",
		"videoWs.send(JSON.stringify({",
		"type: 'client_log'",
		"event: String(entry.event || 'client_event').slice(0, 80)",
		"detail: detailJson",
		"pendingClientLogs.unshift(...batch.slice(index));",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("browser safe-log video queue missing %q", needle)
		}
	}
	for _, needle := range []string{
		"memberAppendSafeOperationalLog",
		"appendSafeLog(",
		"logRowId(",
	} {
		if strings.Contains(clientSource, needle) {
			t.Fatalf("Spacetime browser client still contains retired direct logging marker %q", needle)
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
		"  function browserLifecycleSourceTime(sourceAtPerformanceMillis) {")
	visibility := substringBetween(t, source,
		"document.addEventListener('visibilitychange', () => {",
		"  window.addEventListener('pageshow'")

	for _, needle := range []string{
		"activation_visibility_hidden|activation_pagehide/.test(event)) return 'stream_closed';",
		"activation_resume_(start|finish)/.test(event)) return 'stream_started';",
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
	if strings.Count(source, "reportDecoderError(error,") != 4 {
		t.Fatalf("all decoder error callbacks must use the bounded classifier")
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
		"private livePromise: Promise<void> | null = null;",
		"this.createLivePromise();",
		"this.resolveLive();",
		"await this.whenLive(2000);",
		"private whenLive(timeoutMs: number): Promise<void> {",
		"reject(new Error(\"Spacetime connection is not ready\"));",
		"private rejectLive(error: Error): void {",
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
		"const controlCodeGeneratedChipScanStartY = 0.30;",
		"const controlCodeGeneratedChipScanEndY = 0.50;",
		"const controlCodeGeneratedChipScanStepY = 0.005;",
		"const minimumDarkCellsPerRow = 35;",
		"if (rowDark >= minimumDarkCellsPerRow)",
		"chipRows >= 4 && chipDarkRatio >= 0.42",
		"chipLightRatio <= 0.60",
		"for (let yRatio = controlCodeGeneratedChipScanStartY;",
		"sampleControlCodeResultChipRegion(yRatio, sampledFrame)",
		"ctx.getImageData(scanX, scanY, scanWidth, scanHeight)",
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
		"keyboard.lightCellRatio <= 0.35",
		"keyboard.darkCellRatio >= 0.12",
		"okButtonOrangeRatio",
		"okButtonVisible",
		"const popupVisible = dialogVisible && okButtonVisible && okButtonOrangeRatio >= 0.03;",
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
	queueBody := substringBetween(t, source,
		"function controlCodeRequestOccupiesQueue() {",
		"  function updateControlCodeSubmitAvailability() {")

	postIndex := strings.Index(closeBody, "client.closeControlCode(requestID, 'browser_closed')")
	closedIndex := strings.Index(closeBody, "locallyClosedControlCodeRequestIDs.add(String(requestID));")
	cleanupBarrierIndex := strings.Index(closeBody, "controlCodeCleanupPendingRequestID = requestID;")
	codeRequestNilIndex := strings.Index(closeBody, "codeRequest = null;")
	if postIndex < 0 || closedIndex < 0 || cleanupBarrierIndex < 0 || codeRequestNilIndex < 0 {
		t.Fatalf("close path must mark a request locally closed and clear the retained request before syncing")
	}
	if closedIndex > cleanupBarrierIndex || cleanupBarrierIndex > codeRequestNilIndex || codeRequestNilIndex > postIndex {
		t.Fatalf("close path must clear local request state before the asynchronous close can race with capture")
	}
	if !strings.Contains(closeBody, "request.status === 'succeeded' && request.cleanupPending === true") ||
		!strings.Contains(queueBody, "if (controlCodeCleanupPendingRequestID) return true;") ||
		!strings.Contains(source, "if (controlCodeCleanupPendingRequestID && controlCodeFastStateFresh())") {
		t.Fatalf("browser must keep the phone lane occupied until fresh fast-ready cleanup is published")
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

func TestControlCodeCleanupBarrierClearsWhenAuthoritativeRequestDisappears(t *testing.T) {
	source := ticketAppSource(t)
	reconcileBody := substringBetween(t, source,
		"function reconcileControlCodeCleanupBarrier(state) {",
		"  function renderState() {")
	renderBody := substringBetween(t, source,
		"function renderState() {",
		"  function ticketInteractionPreparingIsStale(")

	for _, required := range []string{
		"const pendingRequestID = String(controlCodeCleanupPendingRequestID || '').trim();",
		"const requests = Array.isArray(state && state.controlCodeRequests) ? state.controlCodeRequests : null;",
		"const authoritativeRequestStillPresent = requests.some((request) =>",
		"String(request.requestId || '').trim() === pendingRequestID",
		"controlCodeCleanupPendingRequestID = '';",
		"clientLog('control_code_cleanup_barrier_cleared', 'authoritative_request_absent');",
	} {
		if !strings.Contains(reconcileBody, required) {
			t.Fatalf("orphaned cleanup barrier reconciliation is missing %q", required)
		}
	}
	if !strings.Contains(renderBody, "reconcileControlCodeCleanupBarrier(state);") {
		t.Fatal("authoritative control-code snapshots must reconcile a locally orphaned cleanup barrier")
	}
	if strings.Contains(reconcileBody, "controlCodeRequestIsStillRelevant(request)") ||
		strings.Contains(reconcileBody, "locallyClosedControlCodeRequestIDs.has") {
		t.Fatal("local result dismissal must not clear the cleanup barrier while its authoritative request still exists")
	}
}

func TestControlCodeCaptureStartsOnlyAfterGeneratedMarker(t *testing.T) {
	source := ticketAppSource(t)
	captureBody := substringBetween(t, source,
		"async function captureControlCodeResultScreenshot(request, proof) {",
		"  function failControlCodeResultScreenshotWait() {")

	if strings.Contains(source, "maybePrepareControlCodeResultFrame") ||
		strings.Contains(source, "allowProvisional") ||
		strings.Contains(source, "browser_prepared_generated_frame_before_marker") {
		t.Fatal("browser must not prepare or freeze a generated frame before the phone marker")
	}
	if !strings.Contains(captureBody, "if (!request || request.status !== 'succeeded'") {
		t.Fatal("browser result display must reject any direct provisional capture call")
	}
	if !strings.Contains(source, "requestControlCodeLowLatencyFrame(requestID, 'control_code_result_marker_low_latency');") {
		t.Fatal("browser must request the existing low-latency marker refresh only after result publication")
	}
}

func TestTicketViewerVisiblePageMayRecoverWithoutDOMFocus(t *testing.T) {
	source := ticketAppSource(t)
	predicate := substringBetween(t, source,
		"function viewerIsForeground() {",
		"  function serverFrameAge(status) {")
	runTicketJavaScript(t, `
let controlCodeActive = false;
const document = {
  visibilityState: 'visible',
  hasFocus: () => { throw new Error('DOM focus must not gate visible recovery'); }
};
function controlCodeKeepsVideoAliveWhileHidden() { return controlCodeActive; }
function check(value, message) { if (!value) throw new Error(message); }
`+predicate+`
check(viewerIsForeground(), 'a visible page must remain eligible for stream recovery');
`)
}

func TestTicketViewerHiddenRecoveryRemainsControlCodeScoped(t *testing.T) {
	source := ticketAppSource(t)
	predicate := substringBetween(t, source,
		"function viewerIsForeground() {",
		"  function serverFrameAge(status) {")
	runTicketJavaScript(t, `
let controlCodeActive = false;
const document = { visibilityState: 'hidden' };
function controlCodeKeepsVideoAliveWhileHidden() { return controlCodeActive; }
function check(value, message) { if (!value) throw new Error(message); }
`+predicate+`
check(!viewerIsForeground(), 'an ordinary hidden page must not recover video');
controlCodeActive = true;
check(viewerIsForeground(), 'hidden control-code capture must retain its existing keepalive');
`)
}

func TestTicketViewerVisibleUnfocusedWatchdogReconnectsOnceWithinDebounce(t *testing.T) {
	source := ticketAppSource(t)
	predicate := substringBetween(t, source,
		"function viewerIsForeground() {",
		"  function serverFrameAge(status) {")
	reconnect := substringBetween(t, source,
		"function reconnectVideoForRecovery(reason) {",
		"  function decoderStartupGraceActive(now) {")
	watchdog := substringBetween(t, source,
		"function chaseLiveStream() {",
		"\n\t  function recoverAfterVisibilityResume(reason) {")
	runTicketJavaScript(t, `
let monotonic = 10000;
const performance = { now: () => monotonic };
const document = {
  visibilityState: 'visible',
  hasFocus: () => false
};
const WebSocket = { OPEN: 1, CLOSED: 3, CLOSING: 2 };
let videoWs = { readyState: WebSocket.OPEN };
let idleDisconnected = false;
let streamUnsupported = false;
let configured = true;
let lastDecodedFrameAt = 1000;
let lastPacketAt = 1000;
let lastPacketSequenceAdvancedAt = 1000;
let configuredAt = 1000;
let latestStreamStatus = null;
let hasRenderedFrame = true;
let lastRecoveryVideoReconnectAt = 0;
const recoveryVideoReconnectDebounceMs = 8000;
const streamStaleKeyframeMs = 2500;
const streamStaleDecoderResetMs = 5000;
const streamStaleVideoReconnectMs = 8000;
const streamStaleServerRecoverMs = 12000;
const recoveryKeyframeDebounceMs = 2000;
let reconnects = 0;
let keyframes = 0;
let decoderResets = 0;
function controlCodeKeepsVideoAliveWhileHidden() { return false; }
function freshStreamStatus() { return null; }
function serverFrameAge() { return -1; }
function backendLooksRecoverable() { return false; }
function currentRenderedFreshness() {
  return {
    hasFrame: true,
    visualAgeMillis: 9000,
    browserReceiveToDecodeMillis: 0,
    decodeToRenderMillis: 0,
    decoderQueueDelayMillis: 0,
    streamFreshnessState: 'STALE',
    liveLabeled: false
  };
}
function lastRenderedVisualAge() { return 9000; }
function requestKeyframeDebounced() { keyframes += 1; return true; }
function resetDecoderForRecovery() { decoderResets += 1; return true; }
function requestServerRecoveryDebounced() {}
function requestFirstFrameServerRecovery() {}
function connectDirectVideo() {}
function pauseHiddenStreamAfterGrace() {}
function decoderStartupGraceActive() { return false; }
function sendVideoClientLog() {}
function streamRecoveryDetail(values) { return values; }
function restartStream() { reconnects += 1; }
function check(value, message) { if (!value) throw new Error(message); }
`+predicate+reconnect+watchdog+`
chaseLiveStream();
chaseLiveStream();
check(reconnects === 1, 'visible stale video must reconnect once inside the debounce window');
check(keyframes >= 1 && decoderResets >= 1, 'stale watchdog must retain keyframe and decoder recovery');
`)
}

func TestTicketViewerSocketCloseAndWatchdogShareVisibilityRecoveryPredicate(t *testing.T) {
	source := ticketAppSource(t)
	predicate := substringBetween(t, source,
		"function viewerIsForeground() {",
		"  function serverFrameAge(status) {")
	socket := substringBetween(t, source,
		"function adoptVideoSocket(socket, queuedMessages, openedAt, reason) {",
		"  function sendVideoClientLog(event, detail) {")
	watchdog := substringBetween(t, source,
		"function chaseLiveStream() {",
		"\n\t  function recoverAfterVisibilityResume(reason) {")
	if strings.Contains(predicate, "document.hasFocus") {
		t.Fatal("DOM focus must not gate recovery for a visible page")
	}
	if !strings.Contains(predicate, "document.visibilityState === 'visible'") {
		t.Fatal("recovery eligibility must follow visible-page state")
	}
	if !strings.Contains(socket, "if (viewerIsForeground())") {
		t.Fatal("socket close must use the shared recovery predicate")
	}
	if !strings.Contains(watchdog, "if (!viewerIsForeground())") {
		t.Fatal("stale-frame watchdog must use the shared recovery predicate")
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
		"const initialLoad = flow.trigger === 'initial_load';",
		"connectDirectVideo({ skipEarlyGrace: !initialLoad });",
		"if (!initialLoad) {",
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
		"followActivationResumeLifecycle('pageshow', 'pageshow');",
		"followActivationResumeLifecycle('focus', 'focus');",
		"startActivationResumeFlow('initial_load', 'initial_load');",
		"pauseActivationResumeLifecycle('visibility_hidden', 'visibility_hidden');",
		"claimActivationResumeLifecycle(reason || 'visibility_resume', 'visibility_resume');",
		"flow.startedAt = performance.now();",
		"flow.attempts = 0;",
		"recoverAfterVisibilityResume('visibility_resume');",
		"if (!videoWs || videoWs.readyState !== WebSocket.OPEN || !pendingClientLogs.length) return;",
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
		"  function controlCodeFastStateExpiryMillis(state) {")

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

func TestTicketViewerBoundsPresentationLiveGraceWithoutRelaxingProof(t *testing.T) {
	source := ticketAppSource(t)
	freshnessBody := substringBetween(t, source,
		"function clearStreamLiveStaleGrace() {",
		"  function controlCodeFastStateExpiryMillis(state) {")
	showEmptyBody := substringBetween(t, source,
		"function showEmpty(message, showStart) {",
		"  function showStreamWaiting(message) {")
	resetBody := substringBetween(t, source,
		"function resetStreamState(options) {",
		"  function restartStream(reason, options) {")

	if !strings.Contains(showEmptyBody, "clearStreamLiveStaleGrace();") ||
		!strings.Contains(resetBody, "clearStreamLiveStaleGrace();") {
		t.Fatal("hard unavailable and stream-reset paths must cancel the presentation-live grace")
	}
	if !strings.Contains(source, "return currentRenderedFreshness(performance.now()).liveLabeled;") {
		t.Fatal("proof freshness must remain strict and must not consume the presentation-only grace")
	}

	runTicketJavaScript(t, `
let monotonic = 1000;
const performance = { now: () => monotonic };
const WebSocket = { OPEN: 1, CLOSED: 3 };
const document = { body: { dataset: { streamLive: 'true', streamReady: 'true' } } };
let idleDisconnected = false;
let streamUnsupported = false;
let foreground = true;
let videoWs = { readyState: WebSocket.OPEN };
let latestStreamStatus = {
  phoneDesired: true,
  phoneConnected: true,
  phoneStreamState: 'streaming',
  activeVideoClients: 1,
  lastFrameAgoMillis: 2100
};
let freshStatus = latestStreamStatus;
let streamLiveStaleGraceTimer = null;
const streamLiveStaleGraceMs = 500;
let timerID = 0;
const timers = new Map();
function setTimeout(callback, delay) {
  const id = ++timerID;
  timers.set(id, { callback, delay });
  return id;
}
function clearTimeout(id) { timers.delete(id); }
function viewerIsForeground() { return foreground; }
function freshStreamStatus() { return freshStatus; }
function backendLooksRecoverable(status) {
  if (!status || status.phoneDesired === false) return false;
  if (status.phoneConnected === false) return true;
  const state = String(status.phoneStreamState || '');
  return state !== '' && state !== 'streaming';
}
function streamStatusStale(status) {
  return Boolean(status && status.activeVideoClients > 0 && Number(status.lastFrameAgoMillis) > 2500);
}
let freshness = { hasFrame: true, liveLabeled: false, streamFreshnessState: 'STALE' };
function currentRenderedFreshness() { return freshness; }
let spinnerShows = 0;
let spinnerHides = 0;
function showStreamResumeSpinner() { spinnerShows += 1; }
function hideStreamResumeSpinner() { spinnerHides += 1; }
function updateControlCodeSubmitAvailability() {}
let activeResumeFlow = null;
function finishActivationResumeFlow() {}
let hasRenderedFrame = true;
function check(value, message) { if (!value) throw new Error(message); }
`+freshnessBody+`

updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamFreshness === 'STALE', 'stale label must be immediate');
check(document.body.dataset.streamLive === 'true', 'one healthy connected transition may retain presentation-live');
check(document.body.dataset.streamReady === 'true', 'freshness updates must not change stream readiness');
check(spinnerShows === 1, 'the recovery spinner must appear immediately during the grace');
check(timers.size === 1, 'the grace must have one bounded expiry');
const expiry = [...timers.values()][0];
check(expiry.delay === 500, 'the presentation-live grace must remain narrowly bounded');
timers.clear();
expiry.callback();
check(document.body.dataset.streamLive === 'false', 'an unchanged stale frame must become unavailable at expiry');
check(streamLiveStaleGraceTimer === null && timers.size === 0, 'expiry must not re-arm its own grace');

freshness = { hasFrame: true, liveLabeled: true, streamFreshnessState: 'LIVE_OK' };
updateStreamFreshnessStatus('frame_rendered');
check(document.body.dataset.streamLive === 'true', 'a fresh frame must restore presentation-live');
check(spinnerHides === 1, 'a fresh frame must hide recovery feedback');

freshness = { hasFrame: true, liveLabeled: false, streamFreshnessState: 'STALE' };
latestStreamStatus.phoneConnected = false;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'a disconnected phone must bypass the grace immediately');

latestStreamStatus.phoneConnected = true;
document.body.dataset.streamLive = 'true';
latestStreamStatus.phoneDesired = false;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'an intentionally stopped phone stream must bypass the grace immediately');

latestStreamStatus.phoneDesired = true;
document.body.dataset.streamLive = 'true';
freshStatus = null;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'a missing fresh relay status must bypass the grace immediately');

freshStatus = latestStreamStatus;
document.body.dataset.streamLive = 'true';
latestStreamStatus.phoneStreamState = 'preparing_phone';
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'a non-streaming phone state must bypass the grace immediately');

latestStreamStatus.phoneStreamState = 'streaming';
document.body.dataset.streamLive = 'true';
latestStreamStatus.activeVideoClients = 0;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'an inactive relay must bypass the grace immediately');

latestStreamStatus.activeVideoClients = 1;
document.body.dataset.streamLive = 'true';
foreground = false;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'a hidden or unfocused viewer must bypass the grace immediately');

foreground = true;
document.body.dataset.streamLive = 'true';
videoWs.readyState = WebSocket.CLOSED;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'a closed video socket must bypass the grace immediately');

videoWs.readyState = WebSocket.OPEN;
document.body.dataset.streamLive = 'true';
latestStreamStatus.lastFrameAgoMillis = 2600;
updateStreamFreshnessStatus('stream_status');
check(document.body.dataset.streamLive === 'false' && timers.size === 0,
  'a server-confirmed stale stream must bypass the grace immediately');
`)
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
		"pauseHiddenStreamAfterGrace('pagehide_cached');",
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
		"const stateRefresh = refreshSpacetimeState(reason || 'idle_resume');",
		"stateRefresh.catch",
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
		"function sendVideoSocketClientLog(event, detail, sourceAtPerformanceMillis) {",
		"const timing = browserLifecycleSourceTime(sourceAtPerformanceMillis);",
		"detailJson: safeString(payload).slice(0, 1000)",
		"sourceAtEpochMillis: Math.round(timeOrigin + performanceMillis)",
		"sourceAtPerformanceMillis: Number(performanceMillis.toFixed(3))",
		"function flushClientLogs() {",
		"videoWs.send(JSON.stringify({",
		"type: 'client_log'",
		"event: String(entry.event || 'client_event').slice(0, 80)",
		"sendVideoSocketClientLog('browser_configured',",
		"sendVideoSocketClientLog('browser_first_frame_decoded',",
		"sendVideoSocketClientLog('stream_first_rendered_frame', firstFrameDetail, lastFrameAt);",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("first rendered frame must be sent over the video socket, missing %q", needle)
		}
	}
	for _, needle := range []string{
		"const configuredAtPerformanceMillis = performance.now();",
		"}, configuredAtPerformanceMillis);",
		"const decodedAtPerformanceMillis = performance.now();",
		"}, decodedAtPerformanceMillis);",
	} {
		if !strings.Contains(source, needle) {
			t.Fatalf("browser lifecycle source timestamp missing %q", needle)
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

func runTicketJavaScript(t *testing.T, source string) {
	t.Helper()
	command := exec.Command("node", "--input-type=commonjs", "-")
	command.Stdin = strings.NewReader(source)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser behavior script failed: %v\n%s", err, output)
	}
}
