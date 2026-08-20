package web

import (
	"os"
	"strings"
	"testing"
)

func TestTicketPanelHasExplicitResetAndActivationActions(t *testing.T) {
	page := ticketIndexTemplate(t)
	for _, id := range []string{
		`id="requestControlCode"`,
		`id="requestTicketReset"`,
		`id="requestTicketResetAndActivate"`,
		`id="activateTicket"`,
		`id="ticketActivationAt"`,
		`id="ticketSliderOverlay"`,
	} {
		if !strings.Contains(page, id) {
			t.Fatalf("ticket page missing control %q", id)
		}
	}
	if !strings.Contains(page, `tabindex="0"`) || !strings.Contains(page, `role="slider"`) {
		t.Fatal("ticket slider must remain keyboard-focusable and semantically exposed")
	}
}

func TestTicketButtonActivationUsesSingleDurableReducerCalls(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"async function activateTicketSliderFromButton(reason)",
		"client.activateTicketButtonV2({",
		"async function requestTicketResetAndActivate()",
		"client.requestTicketResetV2(attemptId, resetRequestID, reason)",
		"runAdmittedActivation(",
		"requestTicketResetAndActivateButton.addEventListener",
		"activateTicketButton.addEventListener",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("ticket button flow missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketResetThenActivate",
		"for (let attempt = 0; attempt < 20; attempt += 1)",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("ticket button flow retained browser-side race %q", forbidden)
		}
	}
	app, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"requestTicketResetAndActivate", "activateTicketSliderFromButton", "activateTicketButtonV2", "ticket_slider_button_activate"} {
		if !strings.Contains(string(app), required) {
			t.Fatalf("built app.js missing %q; run make web-client-build", required)
		}
	}
}

func TestTicketStalePreparingStateBecomesRecoverableAttentionState(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"const ticketInteractionPreparingStaleAfterMs = 2 * 60 * 1000;",
		"function ticketInteractionPreparingIsStale(interaction, now)",
		"function ticketInteractionForDisplay(interaction)",
		"status: 'needs_attention'",
		"reason: 'ticket_reset_stale'",
		"ticketInteractionForDisplay(interaction)",
		"ticketInteractionResetIsAllowed(displayInteraction)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("stale Ticket interaction recovery missing %q", required)
		}
	}
	renderBody := substringBetween(t, source,
		"function renderTicketInteraction(interaction) {",
		"  function ticketResetRequestId() {")
	if !strings.Contains(renderBody, "status === 'needs_attention'") ||
		!strings.Contains(renderBody, "ticketSliderResetInFlight = false") {
		t.Fatal("needs_attention must release the reset controls and clear stale reset state")
	}
	if strings.Contains(renderBody, "status === 'needs_attention' || status === 'control_active'") {
		t.Fatal("needs_attention must not expose the slider overlay")
	}
}

func TestTicketControlCodeRequestClearsWhenAuthoritativeRowTerminates(t *testing.T) {
	source := ticketAppSource(t)
	renderState := substringBetween(t, source,
		"function renderState() {",
		"  function ticketInteractionOwnsControl(interaction) {")
	clearState := substringBetween(t, source,
		"function clearControlCodeRequestLocalState(reason) {",
		"  function controlCodeFingerprintRegion() {")
	for _, required := range []string{
		"function clearControlCodeRequestLocalState(reason)",
		"clearControlCodeRequestLocalState(terminal ? `missing_terminal_${localStatus || 'unknown'}` : 'missing_stale')",
		"const terminalWithoutFailure = ['succeeded', 'closed', 'expired'].includes(String(codeRequest.status || ''))",
		"const terminal = terminalWithoutFailure || localStatus === 'failed'",
		"codeRequest = null",
		"clearControlCodeResultCapture()",
		"scheduleControlCodeTicker(null)",
	} {
		if !strings.Contains(renderState+clearState, required) {
			t.Fatalf("control-code terminal cleanup missing %q", required)
		}
	}
	if !strings.Contains(source, "controlCodeSubmitInFlight = false") {
		t.Fatal("terminal control-code state must release the submit admission barrier")
	}
	availability := substringBetween(t, source,
		"function updateControlCodeSubmitAvailability() {",
		"  function reconnectVideoForRecovery(reason) {")
	if !strings.Contains(availability, "ticketInteractionIsBusy(currentState && currentState.ticketInteraction)") {
		t.Fatal("control-code admission must honor an active ticket interaction")
	}
}

func TestTicketResetRequestAllowsAttentionAndClearsInFlightState(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"async function requestTicketReset(reason = 'browser_ticket_reset') {",
		"  function focusTicketSlider(reason) {")
	for _, required := range []string{
		"ticketInteractionResetIsAllowed(currentState && currentState.ticketInteraction)",
		"client.requestTicketResetV2(attemptId, resetRequestID, reason)",
		"client.requestTicketReset(resetRequestID, reason)",
		"finally",
		"ticketSliderResetInFlight = false",
		"renderTicketInteraction(currentState && currentState.ticketInteraction)",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("reset request cleanup missing %q", required)
		}
	}
	if !strings.Contains(source, "status === 'needs_attention'") {
		t.Fatal("reset UI must explicitly support the recoverable needs_attention state")
	}
}

func TestTicketActivationPolicyReenablesAtExactServerDeadline(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"function activationPolicyBlocked(state = currentState) {",
		"  function activationPolicyReason(state = currentState) {")
	for _, required := range []string{
		"if (!eligibility) return true;",
		"if (eligibility.allowed === true) return false;",
		"const retryAt = activationPolicyRetryAt(state);",
		"return !retryAt || retryAt > Date.now() + serverClockSkewMs;",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("activation policy deadline handling missing %q", required)
		}
	}
	if !strings.Contains(source, "waitForActivationDecision(attemptId)") {
		t.Fatal("browser activation flow must wait for its correlated server decision")
	}
}

func TestTicketServerClockPrefersLiveSampleAndUsesEligibilityOnlyAtStartup(t *testing.T) {
	source := ticketAppSource(t)
	sample := substringBetween(t, source,
		"function selectServerClockSample(state) {",
		"  function rememberServerClock(state) {")
	remember := substringBetween(t, source,
		"function rememberServerClock(state) {",
		"  function activeViewers(viewers) {")
	for _, required := range []string{
		"const liveServerTime = Date.parse(String(state && state.serverTime || ''));",
		"return { timestamp: liveServerTime, source: 'live' };",
		"if (serverClockHasLiveSample) return null;",
		"activationEligibility && state.activationEligibility.serverAt",
		"return { timestamp: eligibilityServerAt, source: 'eligibility' };",
	} {
		if !strings.Contains(sample, required) {
			t.Fatalf("server clock source selection missing %q", required)
		}
	}
	for _, required := range []string{
		"const sample = selectServerClockSample(state);",
		"if (!sample) return;",
		"serverClockSkewMs = sample.timestamp - Date.now();",
		"if (sample.source === 'live')",
		"serverClockHasLiveSample = true;",
	} {
		if !strings.Contains(remember, required) {
			t.Fatalf("server clock update safety missing %q", required)
		}
	}
}

func TestTicketActivationPolicyRetryTimerIsBoundedAndUsesLatestState(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"function scheduleActivationPolicyRetry(state = currentState) {",
		"  function renderTicketInteraction(interaction) {")
	for _, required := range []string{
		"clearTimeout(activationPolicyRetryTimer)",
		"activationPolicyRetryTimer = null;",
		"if (!activationPolicyBlocked(state)) return;",
		"const retryAt = activationPolicyRetryAt(state);",
		"if (!Number.isFinite(delayMs) || delayMs <= 0) return;",
		"Math.min(delayMs, activationPolicyRetryTimerMaxDelayMs)",
		"renderTicketInteraction(currentState && currentState.ticketInteraction);",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("cooldown wake-up timer safety missing %q", required)
		}
	}
	if strings.Contains(body, "setTimeout(() => {}, 0)") || strings.Contains(body, "Math.max(0, delayMs)") {
		t.Fatal("cooldown wake-up must not create zero-length timer loops")
	}
}

func TestTicketSliderRequiresFreshUnactivatedProofAndCancelsOnFailure(t *testing.T) {
	source := ticketAppSource(t)
	proof := substringBetween(t, source,
		"function ticketSliderProofIsFresh(interaction) {",
		"  function ticketInteractionResetIsAllowed(interaction) {")
	render := substringBetween(t, source,
		"function renderTicketInteraction(interaction) {",
		"  function ticketResetRequestId() {")
	flush := substringBetween(t, source,
		"async function flushTicketSliderUpdate() {",
		"  function cancelTicketSliderFromLifecycle(reason) {")
	for _, required := range []string{
		"streamHasFreshRenderedFrame()",
		"proofEpoch > 0 && liveEpoch > 0 && proofEpoch === liveEpoch",
		"proofSequence > 0 && liveSequence > 0 && proofSequence <= liveSequence",
		"const sliderReady = status === 'unactivated_ready' && ticketSliderProofIsFresh(displayInteraction)",
		"const activePointerClaim = status === 'control_active' && Boolean(ticketSliderPointer)",
		"ticketSliderOverlay.setAttribute('aria-hidden', 'true')",
		"ticketSliderOverlay.setAttribute('aria-hidden', 'false')",
		"controlCodeHotspot.style.pointerEvents = ''",
		"controlCodeHotspot.style.pointerEvents = 'none'",
		"queueTicketSliderUpdate('cancel', pending.progress, pending.controlId, pending.interactionRevision)",
		"ticketSliderPointer = null",
	} {
		if !strings.Contains(proof+render+flush, required) && !strings.Contains(source, required) {
			t.Fatalf("slider safety/cleanup missing %q", required)
		}
	}
	if strings.Contains(render, "const sliderReady = status === 'needs_attention'") {
		t.Fatal("needs_attention must never be treated as a fresh slider proof")
	}
}

func TestTicketStateFailsClosedUntilFreshSnapshotAndNeverShowsOldActivation(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"let spacetimeStateFresh = false;",
		"function markSpacetimeStateUnconfirmed(reason) {",
		"renderTicketStateAsUnconfirmed();",
		"function markSpacetimeStateFresh() {",
		"onSnapshotApplied: () => {",
		"markSpacetimeStateFresh();",
		"refreshSpacetimeState(reason || 'visibility_resume')",
		"if (spacetimeClient && typeof spacetimeClient.refresh === 'function')",
		"const displayInteraction = stateConfirmed ? ticketInteractionForDisplay(interaction) : null;",
		"const sliderReadyForButton = status === 'unactivated_ready' && ticketSliderProofIsFresh(displayInteraction);",
		"const activationControlsVisible = stateConfirmed && sliderReadyForButton;",
		"activateTicketButton.hidden = !activationControlsVisible;",
		"requestTicketResetAndActivateButton.hidden = !activationControlsVisible;",
		"const activationFieldsComplete = status === 'activated' &&",
		"Number.isFinite(activationAt) && activationAt > 0",
		"ticketActivationAt.textContent = activationFieldsComplete",
		"Atjaunošana pēc ${minutes} min",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Ticket state freshness guard missing %q", required)
		}
	}
	renderBody := substringBetween(t, source,
		"function renderTicketInteraction(interaction) {",
		"  function ticketResetRequestId() {")
	if strings.Contains(renderBody, "else if (Number.isFinite(activationAt) && activationAt > 0)") {
		t.Fatal("non-activated interactions must not reuse the old activation timestamp")
	}
	if strings.Contains(renderBody, "if (activationBlocked && activationPolicyRetryAt(authoritativeState))") {
		t.Fatal("activation cooldown must not hide the activated ticket refresh countdown")
	}
	if !strings.Contains(renderBody, "else if (!stateConfirmed)") {
		t.Fatal("unconfirmed Ticket state must render a fail-closed message before policy or controls")
	}
}
