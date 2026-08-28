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
		`id="ticketViewSwitch"`,
		`id="ticketViewSwitchDetail"`,
		`id="ticketActivationAt"`,
		`id="ticketRegisterOverlay"`,
		`id="ticketLocalRegisterSlider"`,
	} {
		if !strings.Contains(page, id) {
			t.Fatalf("ticket page missing control %q", id)
		}
	}
	if !strings.Contains(page, `id="ticketLocalRegisterSlider" type="range"`) {
		t.Fatal("the over-stream registration control must remain a native keyboard-focusable range input")
	}
	if !strings.Contains(page, `aria-label="Turi un velc pa labi vismaz ceturtdaļu vai līdz galam, lai reģistrētu atvērto biļeti"`) {
		t.Fatal("the registration slider must explain its held rightward quarter-or-terminal completion threshold")
	}
	stageStart := strings.Index(page, `<div class="stage">`)
	overlayIndex := strings.Index(page, `id="ticketRegisterOverlay"`)
	panelStart := strings.Index(page, `<aside id="panel"`)
	if stageStart < 0 || overlayIndex <= stageStart || panelStart <= overlayIndex {
		t.Fatal("the registration range must be mounted over the streamed phone picture")
	}
	buttonPanelStart := strings.Index(page, `<div class="ticket-reset-row">`)
	if buttonPanelStart < 0 || overlayIndex >= buttonPanelStart || strings.Count(page, `id="ticketLocalRegisterSlider"`) != 1 {
		t.Fatal("the registration range must not be rendered among the action buttons")
	}
	ordered := []string{
		`>Pieprasīt kontroles kodu<`,
		`>Reģistrēt atvērto biļeti<`,
		`>Atvērt jaunāko biļeti un reģistrēt<`,
		`>Atvērt jaunāko nereģistrēto biļeti<`,
		`>Skatīt pēdējo reģistrēto biļeti<`,
		`re-register immediately after inspection, for convenience of other users`,
		`id="ticketLimitPanel"`,
		`id="ticketRegistrationLimitUsage"`,
		`id="ticketControlCodeLimitUsage"`,
	}
	last := -1
	for _, label := range ordered {
		index := strings.Index(page, label)
		if index < 0 || index <= last {
			t.Fatalf("ticket menu label missing or out of order: %q", label)
		}
		last = index
	}
	for _, id := range []string{`id="activateTicket"`, `id="requestTicketResetAndActivate"`, `id="requestTicketReset"`} {
		button := strings.Index(page, id)
		if button < 0 || !strings.Contains(page[button:], `disabled`) {
			t.Fatalf("activation action %q must start disabled", id)
		}
	}
}

func TestTicketPanelControlsAreVisibleAsSoonAsThePanelIsReached(t *testing.T) {
	page := ticketIndexTemplate(t)
	source := ticketAppSource(t)
	for _, required := range []string{
		`<aside id="panel" class="panel" aria-label="Biļetes darbības" aria-hidden="false">`,
		`body:not(.details-visible) .panel > *`,
		`visibility: visible`,
		`pointer-events: auto`,
	} {
		if !strings.Contains(page, required) {
			t.Fatalf("naturally reached Ticket controls must be visible, missing %q", required)
		}
	}
	reveal := substringBetween(t, source,
		"function updateDetailsReveal() {",
		"  function keepFirstScreenPinned(force) {")
	if !strings.Contains(reveal, "panel.setAttribute('aria-hidden', 'false')") ||
		strings.Contains(reveal, "revealed ? 'false' : 'true'") {
		t.Fatal("the 82-percent stream presentation threshold must not hide or disable the lower controls")
	}
}

func TestHealthyVisibilityResumePreservesTheLiveSpacetimeSubscription(t *testing.T) {
	source := ticketAppSource(t)
	resume := substringBetween(t, source,
		"function refreshSpacetimeStateAfterResume(reason) {",
		"  async function runSpacetimeMutation(action, reason) {")
	runTicketJavaScript(t, `
let spacetimeClient = {};
let spacetimeStateFresh = true;
let spacetimeClientStatus = 'live';
let renders = 0;
let reconnects = 0;
function renderState() { renders += 1; }
function clientLog() {}
function refreshSpacetimeState() { reconnects += 1; return Promise.resolve(); }
`+resume+`
(async () => {
  await refreshSpacetimeStateAfterResume('healthy_resume');
  if (renders !== 1 || reconnects !== 0) throw new Error('healthy resume rebuilt the subscription');
  spacetimeClientStatus = 'offline';
  await refreshSpacetimeStateAfterResume('offline_resume');
  if (reconnects !== 1) throw new Error('offline resume did not recover the subscription');
})().catch((error) => { console.error(error); process.exit(1); });
`)
}

func TestExplicitTicketActionsCanSupersedeBackgroundVisualProof(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"const backgroundProofBusy = Boolean(!ticketActionV3LocalRequestIsBusy()",
		"const blockingBusy = busy && !backgroundProofBusy;",
		"spacetimeStateFresh && !blockingBusy && !controlBusy",
		"ticketActionV3Busy(currentAction) && !backgroundProofBusy",
		"Pašreizējais skats tiek pārbaudīts fonā; atvēršanas darbības ir pieejamas.",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("explicit Ticket actions must remain available while read-only proof is supersedable, missing %q", required)
		}
	}
}

func TestTicketActionV3ControlsUseDirectReducerAndLocalOnlySlider(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"client.requestTicketActionV3(ticketActionV3RequestArgs({",
		"'open_latest_unactivated', 'browser_button'",
		"'open_latest_and_register', 'browser_button'",
		"registerCurrentTicket('browser_button')",
		"target === 'register_current' ? expectedInteractionRevision : ''",
		"attemptId: activation ? actionId : ''",
		"statusView === 'latest_unactivated'",
		"statusView === 'activated_current'",
		"statusView === 'recent_activated'",
		"Atvērtā biļete ir veiksmīgi reģistrēta un vizuāli apstiprināta.",
		"(panel && panel.contains(target))",
		"renderTicketRegisterOverlay(state, blockingBusy, controlBusy, registerReady && Boolean(region))",
		"ticketSliderRegionV3ForAction(",
		"ticketSliderRegionV3Layout(",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("v3 Ticket control missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"ticketSliderOverlay",
		"claimTicketSliderV2",
		"updateTicketSlider(",
		"queueTicketSliderUpdate(",
		"inputPhase",
		"ticketSliderPointer",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("local-only v3 slider retained backend progress call %q", forbidden)
		}
	}
	core := ticketRemoteSourceFile(t, "web-client", "ticket-action-v3-core.mjs")
	if !strings.Contains(source, "handleTicketLocalRegisterSliderChange({") ||
		!strings.Contains(core, "state.inFlight = true") ||
		!strings.Contains(core, "submitRegisterCurrent('browser_slider', stableActionId, proofSnapshot)") {
		t.Fatal("slider completion must latch and dispatch through the single register_current path")
	}
	client, err := os.ReadFile("static/spacetime-client.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"requestTicketActionV3", "memberRequestTicketActionV3", "ticketremote_ticket_action_v3",
		"ticketremote_ticket_slider_region_v_3",
		"ticketremote_member_limit_state", "memberRefreshLimitState", "memberSetLimitPreference",
		"ticketremote_member_hdr_state", "memberRefreshHdrState", "memberSetHdrPreference",
	} {
		if !strings.Contains(string(client), required) {
			t.Fatalf("built direct client missing %q; run make web-client-build", required)
		}
	}
	clientSource := ticketRemoteSourceFile(t, "web-client", "src", "index.ts")
	for _, retired := range []string{
		"requestTicketReset(",
		"requestTicketResetV2(",
		"activateTicketButton(",
		"activateTicketButtonV2(",
		"claimTicketSlider(",
		"claimTicketSliderV2(",
		"updateTicketSlider(",
	} {
		if strings.Contains(clientSource, retired) {
			t.Fatalf("direct browser client still exposes retired producer %q", retired)
		}
	}
	for _, retired := range []string{
		"memberRequestTicketReset",
		"memberRequestTicketResetV2",
		"memberActivateTicketButton",
		"memberActivateTicketButtonV2",
		"memberClaimTicketSlider",
		"memberClaimTicketSliderV2",
		"memberUpdateTicketSlider",
	} {
		if strings.Contains(string(client), retired) {
			t.Fatalf("built direct browser client retained legacy reducer binding %q", retired)
		}
	}
}

func TestTicketPanelSliderAndSmartSwitchUseDirectTestedHandlers(t *testing.T) {
	source := ticketAppSource(t)
	panelSlider := substringBetween(t, source,
		"async function submitCompletedTicketRegisterSlider(proofSnapshot)",
		"  ticketViewSwitchButton.addEventListener('click'")
	for _, required := range []string{
		"handleTicketLocalRegisterSliderChange({",
		"slider: ticketLocalRegisterSlider",
		"state: ticketLocalRegisterSliderState",
		"submitRegisterCurrent: (source, exactActionId, exactProof) => registerCurrentTicket(source, {",
		"ticketLocalRegisterSlider.addEventListener('pointerdown'",
		"ticketLocalRegisterSlider.addEventListener('pointerup'",
		"ticketLocalRegisterSlider.addEventListener('pointercancel'",
		"ticketLocalRegisterSlider.addEventListener('lostpointercapture'",
		"ticketLocalRegisterSlider.addEventListener('keydown'",
		"ticketLocalRegisterSlider.addEventListener('keyup'",
		"ticketLocalRegisterSlider.addEventListener('change'",
		"ticketLocalRegisterSlider.addEventListener('blur'",
		"window.addEventListener('blur'",
		"event.isPrimary === false",
		"cancelTicketRegisterSliderSession('secondary_pointer_down')",
		"event.pointerType === 'mouse' && event.button !== 0",
		"Number(event && event.pointerId) !== session.pointerId",
		"progress: Number(ticketLocalRegisterSlider.value || 0)",
		"Number(ticketLocalRegisterSlider.value || 0) < TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT",
		"Slīdņa apstiprinājums vairs nav svaigs.",
		"clientLog('ticket_slider_cancelled', 'change_session_unavailable')",
	} {
		if !strings.Contains(panelSlider, required) {
			t.Fatalf("panel slider must use the direct single-submit handler, missing %q", required)
		}
	}
	secondaryPointer := substringBetween(t, panelSlider,
		"if (event.isPrimary === false) {",
		"    if (event.pointerType === 'mouse' && event.button !== 0) {")
	if !strings.Contains(secondaryPointer, "cancelTicketRegisterSliderSession('secondary_pointer_down');") ||
		!strings.Contains(secondaryPointer, "return;") {
		t.Fatal("a secondary pointer must cancel and reset the primary slider session before native range progress can be trusted")
	}

	render := substringBetween(t, source,
		"function renderTicketActionV3Controls(state = currentState) {",
		"  async function requestTicketActionV3(")
	for _, required := range []string{
		"const switchAction = ticketActionV3SmartSwitchAction(",
		"Date.now() + serverClockSkewMs",
		"const smartSwitch = ticketActionV3SmartSwitchForView(switchCurrentView)",
		"ticketViewSwitchButton.textContent = smartSwitch.label",
		"ticketViewSwitchButton.dataset.target = smartSwitch.target",
	} {
		if !strings.Contains(render, required) {
			t.Fatalf("smart switch must render the tested label-to-target mapping, missing %q", required)
		}
	}

	core := ticketRemoteSourceFile(t, "web-client", "ticket-action-v3-core.mjs")
	coreTest := ticketRemoteSourceFile(t, "web-client", "ticket-action-v3-core.test.mjs")
	for _, required := range []string{
		"export const TICKET_LOCAL_REGISTER_SLIDER_COMPLETION_PERCENT = 25",
		"export async function handleTicketLocalRegisterSliderChange",
		"submitRegisterCurrent('browser_slider', stableActionId, proofSnapshot)",
		"slider completion rejects 24%, accepts 25%",
		"pointer and keyboard completion share one 25% exactly-once gate",
		"a zero-distance range tap never counts as 25% pointer movement",
		"a middle start requires a full quarter of directed travel",
		"native terminal progress completes inside the outer rect but progress 99 does not",
		"tap, tiny travel, wrong direction, and missing coordinates fail closed",
		"an endpoint start needs an intentional anti-tap overshoot",
		"pointer geometry is finite, start is clamped, and terminal progress is bounded",
		"a second pointer and stale pointer events cannot consume the primary session",
		"slider change stays idle below the shared 25% threshold",
		"a false reducer outcome is not submitted",
		"accepted slider stays at 100 until its exact durable action is terminal",
		"export function ticketActionV3SmartSwitchAction",
		"export function ticketActionV3SmartSwitchForView",
		"smart switch labels map to their exact reducer targets",
		"newest automatic activated proof does not hide the database-authorized Return switch",
	} {
		if !strings.Contains(core+coreTest, required) {
			t.Fatalf("direct browser-control test contract missing %q", required)
		}
	}
}

func TestControlCodeBrowserGateIncludesEveryActiveV3Action(t *testing.T) {
	source := ticketAppSource(t)
	laneBusy := substringBetween(t, source,
		"function controlCodeMutationLaneBusy() {",
		"  function updateControlCodeSubmitAvailability() {")
	for _, required := range []string{
		"controlCodeRequestOccupiesQueue()",
		"ticketInteractionIsBusy(currentState && currentState.ticketInteraction)",
		"ticketActionV3LocalRequestIsBusy()",
		"ticketActionV3Busy(currentState && currentState.ticketAction)",
	} {
		if !strings.Contains(laneBusy, required) {
			t.Fatalf("control-code phone-lane gate missing %q", required)
		}
	}
	if got := strings.Count(source, "if (controlCodeMutationLaneBusy())"); got != 3 {
		t.Fatalf("dialog open, submit, and hotspot must all enforce the V3 lane gate; got %d guards", got)
	}
	availability := substringBetween(t, source,
		"function updateControlCodeSubmitAvailability() {",
		"  function reconnectVideoForRecovery(reason) {")
	if !strings.Contains(availability, "const busy = controlCodeMutationLaneBusy()") ||
		!strings.Contains(availability, "requestCodeButton.disabled = busy") ||
		!strings.Contains(availability, "controlCodeHotspot.disabled = hotspotUnavailable") {
		t.Fatal("control-code button and hotspot must disable from the shared V3 phone-lane gate")
	}
}

func TestSuccessfulRedetectReplacesTheStaleBusyMessage(t *testing.T) {
	source := ticketAppSource(t)
	render := substringBetween(t, source,
		"function renderTicketActionV3Controls(state = currentState) {",
		"  async function requestTicketActionV3(")
	for _, required := range []string{
		"statusTarget === 'redetect_latest'",
		"ticketActionV3ExplicitResultForDisplay(",
		"const statusAction = ticketActionV3LastUserAction || action",
		"Jaunākā biļete ir veiksmīgi atkārtoti noteikta.",
		"Biļetes darbība ir veiksmīgi pabeigta.",
	} {
		if !strings.Contains(render, required) {
			t.Fatalf("successful V3 result rendering missing %q", required)
		}
	}
	resultRendering := substringBetween(t, render,
		"if (ticketActionV3LastUserMessage)",
		"    updateControlCodeSubmitAvailability()")
	if strings.Index(resultRendering, "Jaunākā biļete ir veiksmīgi atkārtoti noteikta.") >
		strings.Index(resultRendering, "} else if (statusBusy) {") {
		t.Fatal("redetect success must be rendered before the generic busy fallback")
	}
}

func TestTicketActionV3KeepsTheExactLocalLatchUntilItsAuthoritativeRowArrives(t *testing.T) {
	source := ticketAppSource(t)
	request := substringBetween(t, source,
		"async function requestTicketActionV3(target, source, reason, expectedInteractionRevision = '', options = {}) {",
		"  async function registerCurrentTicket(source, options = {}) {")
	renderState := substringBetween(t, source,
		"function renderState() {",
		"  function ticketInteractionPreparingIsStale(interaction, now) {")
	reconcile := substringBetween(t, source,
		"function scheduleTicketActionV3Reconcile(reason) {",
		"  function renderTicketActionV3Controls(state = currentState) {")
	for _, required := range []string{
		"beginTicketActionV3LocalRequest(ticketActionV3LocalRequestState, actionId)",
		"settleTicketActionV3LocalRequest(ticketActionV3LocalRequestState, true)",
		"settleTicketActionV3LocalRequest(ticketActionV3LocalRequestState, false)",
		"scheduleTicketActionV3Reconcile(`ticket_action_v3_${target}_reconcile`)",
	} {
		if !strings.Contains(request, required) {
			t.Fatalf("v3 request latch contract missing %q", required)
		}
	}
	if strings.Contains(request, "ticketActionV3InFlight = false") {
		t.Fatal("v3 request must not clear its local latch only because the reducer promise resolved")
	}
	if !strings.Contains(renderState,
		"observeTicketActionV3LocalRequest(ticketActionV3LocalRequestState, state.ticketAction)") {
		t.Fatal("authoritative state must release only the exact local v3 action latch")
	}
	if !strings.Contains(reconcile, "await refreshSpacetimeState(reason || 'ticket_action_v3_reconcile')") ||
		!strings.Contains(reconcile, "scheduleTicketActionV3Reconcile(reason)") {
		t.Fatal("an acknowledged v3 request missing from the subscription must remain latched and reconcile")
	}
}

func TestAdminRedetectionUsesAuthenticatedDirectV3Reducers(t *testing.T) {
	source := ticketRemoteSourceFile(t, "web-client", "admin-schedule-source.js")
	for _, required := range []string{
		"fetch('/api/v1/auth/session'",
		"window.TicketSpacetime.create({",
		"sessionState.phone && sessionState.phone.id",
		"client.requestTicketActionV3(",
		"adminRedetectTicketActionV3Args(",
		"client.scheduleTicketActionV3(",
		"adminScheduleTicketActionV3Args({",
		"preferenceClient.setLimitPreference(requested)",
		"state && state.memberLimits",
		"client.disconnect(false)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("direct admin v3 flow missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"fetch('/api/v1/admin/ticket/reselect-latest'",
		"fetch('/api/v1/admin/ticket/reselect-latest/schedule'",
		"client.close()",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("admin direct v3 flow retained legacy producer or viewer-presence side effect %q", forbidden)
		}
	}
	built := ticketRemoteSourceFile(t, "internal", "web", "static", "admin-schedule.js")
	for _, required := range []string{"requestTicketActionV3", "scheduleTicketActionV3", "setLimitPreference", "redetect_latest", "ticket_remote_admin", "ticket_action_v3_"} {
		if !strings.Contains(built, required) {
			t.Fatalf("built admin v3 client missing %q; run make web-client-build", required)
		}
	}
}

func TestTicketButtonActivationUsesSingleDurableReducerCalls(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"async function requestTicketActionV3(target, source, reason, expectedInteractionRevision = '', options = {})",
		"client.requestTicketActionV3(ticketActionV3RequestArgs({",
		"requestTicketResetAndActivateButton.addEventListener('click', () => requestTicketActionV3(",
		"'open_latest_and_register', 'browser_button'",
		"activateTicketButton.addEventListener('click', () => registerCurrentTicket('browser_button'))",
		"return requestTicketActionV3(",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("v3 ticket button flow missing %q", required)
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
	for _, required := range []string{"requestTicketActionV3", "open_latest_and_register", "registerCurrentTicket", "browser_slider"} {
		if !strings.Contains(string(app), required) {
			t.Fatalf("built v3 app.js missing %q; run make web-client-build", required)
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
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("stale Ticket interaction recovery missing %q", required)
		}
	}
	if !strings.Contains(source, "ticketInteractionIsBusy(currentState && currentState.ticketInteraction)") {
		t.Fatal("legacy in-flight phone work must still fence the shared mutation lane during rollout")
	}
}

func TestTicketControlCodeRequestClearsWhenAuthoritativeRowTerminates(t *testing.T) {
	source := ticketAppSource(t)
	renderState := substringBetween(t, source,
		"function renderState() {",
		"  function ticketInteractionPreparingIsStale(interaction, now) {")
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
	mutationLaneBusy := substringBetween(t, source,
		"function controlCodeMutationLaneBusy() {",
		"  function updateControlCodeSubmitAvailability() {")
	if !strings.Contains(availability, "const busy = controlCodeMutationLaneBusy()") ||
		!strings.Contains(mutationLaneBusy, "ticketInteractionIsBusy(currentState && currentState.ticketInteraction)") {
		t.Fatal("control-code admission must honor an active ticket interaction")
	}
}

func TestRetiredBrowserTicketResetAndSliderProducersAreAbsent(t *testing.T) {
	source := ticketAppSource(t)
	for _, retired := range []string{
		"async function requestTicketReset(",
		"client.requestTicketResetV2(",
		"client.requestTicketReset(",
		"client.claimTicketSlider",
		"client.updateTicketSlider(",
		"ticketSliderOverlay",
	} {
		if strings.Contains(source, retired) {
			t.Fatalf("browser retained retired Ticket producer %q", retired)
		}
	}
}

func TestTicketActivationPolicyIsEnabledOnlyBySpacetimeProjection(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"function activationPolicyBlocked(state = currentState) {",
		"  function activationPolicyReason(state = currentState) {")
	if !strings.Contains(body, "return memberLimitBlocked('registration', state);") {
		t.Fatal("registration gate must use the authenticated Spacetime member projection")
	}
	for _, forbidden := range []string{"Date.now", "retryAt", "cooldownUntil", "activationEligibility"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("browser registration gate must not infer authority from its own clock, found %q", forbidden)
		}
	}
	registration := substringBetween(t, source,
		"async function registerCurrentTicket(source, options = {}) {",
		"  function selectServerClockSample(state) {")
	if !strings.Contains(registration, "return requestTicketActionV3(") ||
		!strings.Contains(registration, "'register_current'") {
		t.Fatal("browser activation must use the single durable V3 action reducer")
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

func TestTicketLimitCountdownTimerIsPresentationOnly(t *testing.T) {
	source := ticketAppSource(t)
	body := substringBetween(t, source,
		"function renderMemberLimits(state = currentState) {",
		"  function renderTicketInteraction(_interaction) {")
	for _, required := range []string{
		"clearTimeout(ticketLimitPresentationTimer)",
		"ticketLimitPresentationTimer = null;",
		"updateTicketMemberLimitClock(",
		"ticketMemberLimitClockNow(ticketMemberLimitClock, performance.now())",
		"ticketMemberLimitCountdown(",
		"}, 1000);",
		"renderMemberLimits(currentState);",
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("member limit presentation timer missing %q", required)
		}
	}
	for _, forbidden := range []string{"renderTicketActionV3Controls", ".disabled =", "registrationAllowed = true", "registrationAllowed: true"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("countdown presentation must never grant eligibility, found %q", forbidden)
		}
	}
}

func TestTicketLimitProjectionRefreshesFromSpacetimeOnVisibleResume(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"function refreshMemberLimitProjection(reason)",
		"client.refreshLimitState()",
		"refreshMemberLimitProjection('visibility_resume_limit_refresh')",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("member limit resume refresh missing %q", required)
		}
	}
}

func TestTicketSliderRequiresExactFreshGeometryAndAutoProof(t *testing.T) {
	source := ticketAppSource(t)
	for _, required := range []string{
		"function currentTicketSliderRegion(state = currentState)",
		"ticketSliderRegionV3ForAction(",
		"ticketSliderRegionV3Layout(",
		"ticketRegisterOverlay.hidden = true",
		"ticketRegisterOverlay.hidden = false",
		"controlCodeHotspot.style.pointerEvents = ''",
		"controlCodeHotspot.style.pointerEvents = 'none'",
		"function observeTicketCurrentProofFrame()",
		"ticketCurrentProofVisualState.stableChangeCount >= 2",
		"ticketCurrentProofVisualState.changePending = true",
		"'prove_current'",
		"'browser_auto_proof'",
		"ticketCurrentProofVisualState.resumePending = true",
		"const proofScope = `${String(cfg.backendId || 'pixel')}:${Number(stream.epoch || 0)}`",
		"requestedEpoch: ticketCurrentProofRequestedScope === proofScope ? Number(stream.epoch || 0) : 0",
		"ticketCurrentProofRequestedScope = proofScope",
		"renewBeforeMs: ticketCurrentProofRenewBeforeMs",
		"slider_region_renewal",
		"currentTicketRegisterSliderProof(state = currentState)",
		"ticketRegisterSliderProofStillMatches(snapshot, state = currentState)",
		"cancelTicketRegisterSliderSession('viewport_changed')",
		"cancelTicketRegisterSliderSession('window_scrolled')",
		"cancelTicketRegisterSliderSession('stream_reset')",
		"ticketLocalRegisterSlider.addEventListener('lostpointercapture'",
		"ticketLocalRegisterSlider.addEventListener('blur'",
		"window.addEventListener('blur'",
		"ticketRegisterOverlay.dataset.registrationState = 'registering'",
		"releaseTicketLocalRegisterSliderOnTerminal(",
		"row >= 1 && row <= 5 && column >= 2 && column <= 5",
		"backendId: cfg.backendId || 'pixel'",
		"rebaseTicketCurrentProofDetectorFromAction(",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("visual proof/over-stream slider contract missing %q", required)
		}
	}
	core := ticketRemoteSourceFile(t, "web-client", "ticket-action-v3-core.mjs")
	coreTest := ticketRemoteSourceFile(t, "web-client", "ticket-action-v3-core.test.mjs")
	for _, required := range []string{
		"String(region.proofActionId || '') !== String(action.actionId || '')",
		"String(region.streamEpoch || '') !== String(action.streamEpoch || '')",
		"String(region.frameSequence || '') !== String(action.frameSequence || '')",
		"expiresAt <= Number(now)",
		"two agreeing significant frame changes override a still-fresh proof",
		"the same retained frame-change trigger is admitted once the phone is idle",
		"slider session binds exact proof, stream epoch, frame, region, viewport, and visual revisions",
		"pointer capture loss, blur, and incomplete release cancel without submission",
	} {
		if !strings.Contains(core+coreTest, required) {
			t.Fatalf("exact auto-proof test contract missing %q", required)
		}
	}
}

func TestTicketSliderCancelsImmediatelyOnConfirmedVisualChange(t *testing.T) {
	source := ticketAppSource(t)
	scroll := substringBetween(t, source,
		"window.addEventListener('scroll'",
		"  if (window.visualViewport) {")
	if !strings.Contains(scroll, "cancelTicketRegisterSliderSession('window_scrolled')") ||
		!strings.Contains(scroll, "updateDetailsReveal();") {
		t.Fatal("ordinary window scroll must cancel an active slider before updating the scrolled layout")
	}
	if strings.Index(scroll, "cancelTicketRegisterSliderSession('window_scrolled')") > strings.Index(scroll, "updateDetailsReveal();") {
		t.Fatal("ordinary window scroll must cancel before the stage can move away from the held pointer")
	}
	observe := substringBetween(t, source,
		"function observeTicketCurrentProofFrame() {",
		"  async function maybeRequestTicketCurrentProof(reason) {")
	for _, required := range []string{
		"ticketCurrentProofVisualState.stableChangeCount >= 2",
		"ticketSliderVisualRevision += 1",
		"cancelTicketRegisterSliderSession('visual_view_changed')",
		"maybeRequestTicketCurrentProof('frame_observed')",
	} {
		if !strings.Contains(observe, required) {
			t.Fatalf("confirmed visual-change slider fence missing %q", required)
		}
	}
	if strings.Index(observe, "cancelTicketRegisterSliderSession('visual_view_changed')") >
		strings.Index(observe, "maybeRequestTicketCurrentProof('frame_observed')") {
		t.Fatal("the active slider must cancel before any cooldown-delayed proof request")
	}
	if strings.Count(source, "ticketSliderVisualRevision,") < 2 {
		t.Fatal("pointer-down snapshot and pointer-up match must bind the same visual revision")
	}

	change := substringBetween(t, source,
		"ticketLocalRegisterSlider.addEventListener('change'",
		"  ticketLocalRegisterSlider.addEventListener('blur'")
	for _, required := range []string{
		"if (!beginTicketLocalRegisterSliderSession(",
		"ticketLocalRegisterSlider.value = '0'",
		"change_session_unavailable",
		"renderTicketActionV3Controls(currentState)",
	} {
		if !strings.Contains(change, required) {
			t.Fatalf("failed change fallback must reset and surface safely, missing %q", required)
		}
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
		"function refreshSpacetimeStateAfterResume(reason)",
		"refreshSpacetimeStateAfterResume(reason || 'visibility_resume')",
		"const liveSubscription = Boolean(spacetimeClient && spacetimeStateFresh",
		"clientLog('spacetime_resume_reused'",
		"if (spacetimeClient && typeof spacetimeClient.refresh === 'function')",
		"renderTicketInteraction(spacetimeStateFresh ? state.ticketInteraction : null);",
		"const proofReady = spacetimeStateFresh && ticketActionV3RegistrationProofIsFresh(action);",
		"const registerReady = proofReady && proveCurrentReady && !activationPolicyBlocked(state);",
		"setTicketButtonGate(activateTicketButton, !blockingBusy && !controlBusy && registerReady, registerReason);",
		"renderTicketRegisterOverlay(state, blockingBusy, controlBusy, registerReady && Boolean(region));",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("Ticket state freshness guard missing %q", required)
		}
	}
	if !strings.Contains(source, "function clearTicketRegisterOverlay()") ||
		!strings.Contains(source, "ticketLocalRegisterSlider.disabled = true") {
		t.Fatal("an unconfirmed or stale Ticket state must hide and disable registration")
	}
}
