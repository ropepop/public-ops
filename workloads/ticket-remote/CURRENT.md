# Current Ticket

This is the first file to read for Ticket work.

## September 19 steady HDR and local ViVi slider

v206 keeps the WebGPU canvas configured while completed pictures replace its
contents. Ordinary updates reuse the same visible HDR surface. Background-return
replacement still retains the previous picture until the replacement is ready.
An unsupported or failed HDR renderer now reveals the latest ordinary picture
without an error overlay or retry spinner; the saved HDR preference remains on
and the explanation appears beneath that setting. It stays in SDR until an
explicit preference change or a new page opening, avoiding repeated failed
activation. A frozen control-code result uses its already-prepared exact SDR
image on failure.

The browser renders the ViVi orange slider, label and arrow locally in the
existing phone-proved rectangle. Swipes may start anywhere, follow the pointer,
and finish outside the track. The existing eight-pixel directional threshold,
scroll/cancel protections, keyboard activation and separate action buttons remain.
The three-second wave and reduced-motion preference remain in both display modes.

HDR failures now use the existing authenticated browser event route to the shared
operational log. Fixed failure stage, category, browser family, brightness and
picture-age fields are bounded and sanitized; unsent events wait in a bounded
in-memory queue. Ticket retention remains six hours. No phone or database schema
change is required. Physical iPhone contrast and blinking acceptance require
target-device observation after deployment.

Deployed `ticket-steady-hdr-slider-20260919-v206` from `71351bab` on September 19
at 13:35 UTC. Standard release validation, the full test suite, the final 120-check
browser interruption/gesture journey, and real-GPU presentation checks passed.
The signed-in production viewer kept one HDR canvas across seven fresh pictures
with zero reconfigurations. A one-shot presentation failure in the verification
tab switched quietly to fresh SDR, showed only the settings error, and did not
retry during subsequent frames; toggling HDR off/on restored it. Its sanitized
production log event at 13:36:37 UTC expires at 19:36:37 UTC (`ticket_6h`).

A native browser drag starting in the middle of the live slider registered the
ticket once. The visible success matched the durable `activation_proven` result
(13:39:00.814–13:39:05.128 UTC). A real background-tab return restored the live HDR
view with the saved 4× preference and no settings error. Verification tabs were
closed; the relay returned to zero viewers/idle and there were no pending ticket
actions. These checks prove the desktop browser and phone outcome; physical
iPhone brightness, flicker and finger-gesture acceptance remain unobserved.

## September 19 mobile error pages

v205 gives the shared error page a mobile viewport, system font, matching heading
and message sizes, safe-area padding and long-text wrapping. Zoom remains enabled.
The styles use the existing nonce-based security policy and need no authenticated
assets. The server version advances so active viewers pick up this release.

Deployed as `ticket-mobile-errors-20260919-v205` from `0d9aa86e`. All test
components and both builds passed; the static-access test now compares beyond
the generic HTML opening shared with error pages. A real loopback browser rendered
the actual error response at 320, 390 and 1440 px, with matching 16 px text and no
overflow at 200% text size. Standard deployment validation and the public version
and shared error-response checks passed. Physical-phone and signed-in visual
verification remain unobserved.

## September 19 ticket assignment message

v204 bumps the server version so connected viewers reload through the existing
version check. The access-denied message now explains that no ticket has been
assigned to the account and asks the user to contact the owner or an administrator.
The existing membership checks and HTTP 403 response remain unchanged.

Deployed as `ticket-assignment-20260919-v204` from `2eb7db03`. Full tests,
both required builds, standard deployment validation and the public live-version
check passed. The deployed source contains the new message in all three denial
paths. Signed-in visual and automatic-refresh observation remain unverified:
the browser runtime is not initialized, the installed plugin has no skill file,
and Chrome is not running; extension and native-host diagnostics passed.

## September 18 idle-refresh recovery

Pixel v390 (`e6b8fd1`) gives post-restart ticket restoration the same bounded
visual convergence and capture recovery as ordinary navigation. A failed refresh
using the earlier five-second-only check left ViVi on Home; later
refreshes could no longer identify a ticket. Non-activating re-detection restored
the detail, and subsequent ordinary registrations succeeded. No physical attempt
is replayed by the change. Both 508-test app variants, standard Pixel deployment,
and production service validation passed. The corrected local monitor passes
49 tests and reports healthy warmth. A post-change automatic refresh and independent
signed-in browser paint remain unverified; browser inspection was URL-policy blocked.
See the [incident and prevention report](../../ops/reports/2026-09/2026-09-18-ticket-idle-refresh-recovery.md).

## September 18 localized installation screenshots

v203 makes the invitation and guide Latvian by default, with an English/Latviski
button that preserves the selected platform and translates live prompt status.
The schematic controls are replaced by viewport crops of real Apple/WebKit,
Google, Mozilla and MacRumors screenshots, with visible source links and an
example-label explanation. Source URLs are recorded in
`web-client/install-guide-sources.md`. Screenshots stay member-only static assets.
The invitation remains hidden when already running as a home-screen app.

The guide browser test passes 43 checks at each of 320, 390 and 1440 px,
including image decoding, language switching, focus and native-prompt lifecycle.
Deployed as `ticket-home-screen-20260918-v203` from `62c9eee5`. Full tests,
both required builds, standard deployment validation and the public live version
check passed. Physical-device and signed-in visual acceptance remain unverified;
the interactive browser connection had no available Ticket tab.

## September 18 home-screen installation

v202 adds one English "Add Ticket to Home Screen" button beneath the viewers
list. The Arrow guide always asks for iPhone/iPad Safari, Android Chrome, or
Android Firefox first. Chrome uses a single-use browser installation prompt
when available; all three paths include illustrated manual steps. The invitation
is hidden in app display mode. The app name is Ticket, with a dark ticket icon,
root launch URL and fullscreen display with browser-controlled fallback.

Only `/manifest.webmanifest` and four explicitly named `/pwa/` icon files are
public. Existing `/static/` membership gates remain intact. No service worker,
offline ticket cache, database change or phone update is involved. Guide source
is `web-client/install-guide.mjs`; its browser test covers the journeys and
prompt lifecycle. Physical iOS/Android installation and launch require separate
device acceptance; simulated prompts are not installation proof.

Deployed as `ticket-home-screen-20260918-v202b` from `a6583c95`. Ticket tests,
both required builds, 31 guide checks at each of 320/390/1440 px, standard
production validation and public manifest/icon byte comparisons passed. The
initial release rolled back because an old guard rejected any `showModal` use;
the guard now checks only retired claim-specific markers. Signed-in visual and
physical mobile-install acceptance remain unverified after task-created browser
tabs crashed. See the [release report](../../ops/reports/2026-09/2026-09-18-ticket-home-screen.md).

## September 16 HDR reset in the normal viewer

The owner authorized production adoption of the v200 diagnostic candidate.
Deployed v201 (`d5d1e019`) reconfigures the canvas immediately before each prepared HDR
picture is copied to the display. Devices, shaders and buffers are reused;
configuration, copy and submission do not yield between them. There is one
production painting path, and the temporary diagnostic painting selector is
removed. The diagnostic still compares SDR and HDR from the same frozen source.

Existing interruption recovery, holdover HDR and frozen-result protection remain
active. No stream, saved preference, database or operational logging change is
involved. Physical iPhone contrast acceptance remains pending; adoption is the
owner's decision, not a claim that desktop checks prove the iPhone symptom fixed.
Required tests/builds, standard deployment validation and the signed-in viewer
passed. Frame median remains 33 ms. The Mac was locked during the final live
foreground/background check, so that check is unproved; ten automated controller
returns and frozen-result restoration passed. Test tabs are closed, relay viewers
are zero and normal stream warmth is preserved.
See the [production report](../../ops/reports/2026-09/2026-09-16-ticket-hdr-frame-reset.md)
and the historical [v200 comparison](../../ops/reports/2026-09/2026-09-15-ticket-hdr-reset-comparison.md).

## September 12 five-round acceptance — complete

Pixel v381 (`eb067931`) removes the historical-failure capture gate and duplicate
one-frame probe. Three recovered helper interruptions followed by a genuine cold
opening reproduced the old failure on v380 and passed on v381: first fresh
picture in 2.743 seconds, one encoder and no stale capture processes. The existing
admitted capture path owns visibility proof, startup and recovery.

Public `ticket-viewer-cleanup-20260912-v199` (`b6ea3f62`) removes three verified
unused template placeholders. Required tests, builds, production validation and
the signed-in Brave live page passed. No API or database changes were made.

Final Pixel `ticket-stream-2026-09-12-stable-switch-v383` (`cd49aac`) also preserves
the proved card identity across cold capture restarts. It reads only the known
validity strip from the same native capture, using the existing recognizer and
confidence checks. The old reduced detail probe lost date information and left
registration tied to a capture-process identity. A reproduced cold-switch failure
reset the earlier streak; the correction then passed the exact regression.

**Five consecutive complete rounds passed** on unchanged v199/v383, using the
signed-in Brave page and real Pixel. All 45 ticket actions and 10 exact code
results had matching browser, saved outcome, phone result and completed cleanup.
Every registration entry point, both switch directions, both code dialogs, HDR
2–6, interrupted gestures, responsive/scroll/background behavior, repeated input,
and sustained reconnect recovery were covered. Cold first pictures took
2.622–4.562 seconds; warm first pictures took 0.438–0.706 seconds. These are five
observed samples, not a general latency guarantee. Sign-in recovery was
inapplicable; physical iPhone HDR brightness remains unmeasured.

The owner explicitly authorized clearing the single old uncertain checkpoint.
It was cleared without replaying that old action; the historical uncertain server
record remains. Normal quota enforcement passed separate tests and a bounded
live rejection check. Original unlimited/audited and enabled HDR 4× preferences
were restored and independently verified. Only test-owned tabs were closed.
Final state had no pending actions or code cleanup, zero relay viewers, gated
capture demand and no physical action lease; intentional stream warmth remains.
See the [complete acceptance ledger](../../ops/reports/2026-09/2026-09-12-ticket-five-rounds.md).

## September 12 ViVi authentication attention

Deployed as `ticket-vivi-attention-20260912-v198` from `1c0d5628`. Required tests,
builds and production validation passed. The initial ViVi CPU spike was reduced;
sign-in then reached a device-link rejection. The owner subsequently restored
ViVi; a 15:08 phone inspection confirmed an activated ticket. A targeted
`ticket_screen` restart cleared a stuck hardware-reliability probe and restored
frames to the relay at 15:11. At that checkpoint v198 on-page viewing was unproved
because browser navigation timed out; the later v199 acceptance above completes
the live user-page coverage. See the
[incident report](../../ops/reports/2026-09/2026-09-12-vivi-login-recovery.md).

The viewer immediately displays the existing phone observation's `login_required`
or `blocked` state and disables ticket navigation until that state changes.
These states do not offer media reconnect as an account repair or show a waiting
spinner. The existing connections remain available so a recovered ticket clears
the message in place. Unknown/busy observations retain the warning until the
phone identifies a ticket list or detail. The existing owner account flow remains
one bounded attempt; device-link rejection requires attention and never falls
back to clearing app data.

## September 13 idle ticket refresh

September 14 correction: Pixel v389 (`2314e767`) removes a false Home-screen
rejection. A service notice can move the route planner's Search button into the
coarse registration-slider detection band. The four proved Home navigation
glyphs now retain authority despite that body-content match; popup and login
proofs still block navigation. No restart retry or substitute-ticket fallback
was added. The exact regression and all 508 app tests in both build variants
passed. Signed-in recovery restored the original unused ticket; the following
natural refresh restarted ViVi and restored that same ticket in 17.362 seconds.
The returning page showed its first fresh picture in 1.250 seconds, without
reconnects. The temporary
24-hour retention and diagnostics have been removed and six-hour retention
restored; the investigation heartbeat is deleted. See the
[investigation and verification ledger](../../ops/reports/2026-09/2026-09-13-idle-refresh-investigation.md).

Pixel v386 (`f47ca19`) fully restarts ViVi during the existing idle refresh.
The standard deployment and all 507 app tests in both build variants passed.
A real scheduled cycle on the deployed build stopped the old ViVi process,
started a new one and restored the same ticket in 17.424 seconds. The returning
signed-in page showed its first live picture in 1.359 seconds with no reconnects;
settings, action cleanup and normal idle shutdown were checked separately.
The server, browser and database schedule were unchanged by this update. See the
[verification report](../../ops/reports/2026-09/2026-09-13-ticket-idle-app-restart.md).

The idle refresh uses one private durable schedule per phone. Actual
visible `stream_viewer_focus` presence cancels it; retained stream warmth is not
a viewer. After the last viewer leaves (or its presence expires), the first
refresh is due at 40 minutes and subsequent refreshes every 30 minutes. Missing
schedules start a fresh idle period; outages do not accumulate catch-up work.

Pixel v386 changes the internal `refresh_current_ticket` action to a full ViVi
restart through the existing command lane. It proves the current detail's private
identity and validity range, journals the attempt, claims admission once, force-stops
ViVi, proves its process has stopped, and launches it once without clearing data or
login. It then verifies the same restored detail or follows the existing Tickets/Time
tickets route to the exact matching card, with at most three taps. It never selects
a newer substitute. Missing or ambiguous identity skips the cycle. ViVi must already be
foreground; the action never registers a ticket or requests a code.
It preserves physical-touch, maintenance and deliberate cold
mode protections, then releases its temporary observation through the existing
capture owner. A returning viewer cancels an unstarted attempt; after admission,
safe completion may restore the same ticket without admitting another
refresh. A one-time database admission and the existing phone journal prevent
duplicate delivery from repeating input.

A viewer returning while the temporary observation session is still warm starts
the existing viewer-preparation job, which waits for the admitted phone action
and enables picture delivery. Observation-only starts cannot run that preparation.

Late phone replies after command expiry or six-hour history removal drain the
transport receipt without resurrecting an old outcome or changing newer ticket
state. This prevents a long outage from stranding the phone's result journal.

Full action observations recognise only the known validity-date band, excluding
the changing code, price and registration time. Ordinary detail-only readiness
keeps its existing fast path. Fresh ephemeral identity and screen/input
generations still govern every tap; matching needs no extra remembered identity
or bootstrap navigation after an upgrade. This is navigation maintenance only;
improved swipe reliability or Aztec-code freshness requires separate evidence.

## September 12 HDR contrast candidate

Browser/server `ticket-hdr-contrast-20260912-v195`, implementation `9644b970`,
uses uniform linear-light brightness multiplication instead of the nonlinear
color gain. The existing brightness choices, output formats, activation and
recovery remain intact. No device or database update was needed.

The owner-only `/owner/hdr-diagnostic` now alternates the same frozen SDR source
and production HDR rendering, with pale-gray 12px lettering and an optional
local image held only in memory. It neither opens the stream nor changes saved
preferences. Run `npm run test:hdr` in `web-client` for GPU contrast, comparison
controls and the existing real-GPU presentation checks.

Required builds/tests, standard deployment validation and the signed-in Brave
scroll/toggle/background-return journey passed. Physical iPhone Brave contrast
acceptance is pending: display screenshots still wash out high-brightness grays,
and GPU readback does not prove the emitted display contrast. No speculative
contrast compensation or brightness cap was added. See the
[candidate verification](../../ops/reports/2026-09/2026-09-12-ticket-hdr-contrast.md).

## September 11 independent recovery

Deployed as `ticket-recovery-20260911-v194` from `6ce8a592`; local checks,
deployment validation and the signed-in Brave journey passed. See the
[verification report](../../ops/reports/2026-09/2026-09-11-ticket-independent-recovery.md)
for coverage and the physical iPhone verification gap.

Browser/server v194 separates command and picture recovery. A media or decoder
failure restarts only media; a database interruption restarts only the command
connection. Confirmed expired authentication stops both. Existing admitted
commands retain their identity and result subscription without physical replay.
Registration and control-code eligibility use the current subscribed phone
readiness, context, database clock, busy state and limits; displayed-picture
freshness does not gate commands.

The picture error panel appears after 30 visible seconds without a new valid
frame arriving locally. Receipt resets this timer before decoding or freshness
checks, including delayed pictures, and clears an existing panel immediately.
Hidden time and deliberate cold pauses do not count. Reconnects, control traffic,
duplicate pictures and obsolete callbacks cannot renew the allowance. The early
startup buffer preserves the frame's actual arrival time. The existing
`recoveryDowntimeMillis` page diagnostic now reports visible frame silence;
`recoveryPhase` describes media and `commandRecoveryPhase` describes commands.

The spinner and three-second display freshness still describe picture recovery.
Phone readiness retains its separate three-second expiry. Exact code-picture
presentation and cleanup acknowledgement are unchanged. The error panel covers
only the picture, leaving the command controls below it accessible.

## September 11 scrolling freshness fix

Browser/server v193 removes the scroll-position pause from both ordinary and HDR
presentation. Scrolling below the picture keeps fresh frames and the control-code
button available while the page remains visible. Document backgrounding, real
connection loss, source freshness, phone readiness and action limits retain their
existing checks. The scroll handler still cancels an unfinished slider gesture.
Deployed as `ticket-scroll-live-20260911-v193` from `56f08b74`. The regression,
required local suites and live HDR/ordinary scrolling checks passed; both code
dialogs opened without submitting a request. See the
[scrolling verification](../../ops/reports/2026-09/2026-09-11-ticket-scroll-live.md).

## September 11 viewing reliability and cleanup release

Browser/server v192 and Pixel v379 are deployed. Visible-page activity now uses
its own five-second cadence, with one pending submission and no catch-up for
hidden or missed time. Distinct bounded failure reports preserve their stage and
safe error category. Existing database, admin and sidecar statistics code is
separated into focused files; the Pixel request-scoped keyboard lease is moved
without changing its behavior or service ownership.

Local suites and live fresh/existing-account viewing checks passed. Statistics
matched database totals, hidden time added nothing, and revoked temporary access
returned 403 while retaining inactive-member history. Desktop/mobile Statistics,
HDR and reload were checked. The original missing-user case remains unexplained;
no new physical registration or control-code test was performed. See the
[viewing reliability report](../../ops/reports/2026-09/2026-09-11-ticket-polish.md)
for measurements, deployment details and remaining evidence limits.

## September 11 registration latency release

Pixel v378, implementation commit `ed4d7c7`, introduced the following retained
registration behavior. Registration prepares
from two fresh matching pictures while display protection starts, skips an
unnecessary ViVi resume, and sends one uninterrupted 400 ms phone swipe.
Cold startup reuses the same guarded picture pair instead of holding the action
lane for two additional captures. Protection still independently confirms the
helper identity and dark panel twice; ticket identity, focus, touch, freshness,
durable admission and final native-input checks remain required.

Five warm registrations on v376 started their phone swipe in approximately
1.03–1.25 seconds. The first final-build cold registration started in about
0.89 seconds and succeeded with one 400 ms stroke. All 1,075 Android test
executions passed. Final acceptance remains incomplete: the Mac locked before
the remaining five warm and two cold trials. See the
[registration latency report](../../ops/reports/2026-09/2026-09-11-registration-latency.md)
for exact evidence and remaining verification.

## September 10 cleanup release

Browser/server v191 fully minifies the existing browser bundles while retaining
function names and release metadata; release Go binaries omit debugger symbols.
The database reuses rows returned by writes and the existing SHA-256 formatter,
without changing schema, command fingerprints, admission, or settlement. Pixel
v374 removes unused imports and shares the existing bounded input wrapper.
The obsolete Go schedule producer is removed; browser scheduling and server
cancellation remain supported. Historical schema, old-client rejections,
authentication compatibility, and physical recovery protections remain intact.
Cold-opening acceptance exposed a pre-existing second epoch creator in early
capture callbacks. Pixel v374 removes it: configuration owns the epoch, and an
early unconfigured picture fails the existing generation-and-dimensions check.
The database, server release `ticket-cleanup-20260910-v191`, and Pixel commit
`4f6387e` are deployed. Five final cold openings reached fresh HDR without a
reconnect, with first pictures in 2.28–2.62 seconds. Actions, scheduling, network
recovery, and cleanup were checked independently. After the Mac was unlocked,
two native background returns recovered fresh HDR and controls on the same page,
with no browser warnings or errors. The longer check included over 37 seconds
in the background. See the
[cleanup report](../../ops/reports/2026-09/2026-09-10-ticket-cleanup.md) for scope,
measurements, and proof boundaries.

Live product: the signed-in page at `ticket.jolkins.id.lv`.
The phone shows ViVi. The page shows that picture and exposes one durable visual action engine for opening, registering, switching between the newest unused and recently activated tickets, re-detecting the newest ticket, and requesting a control code.

## Current jobs, in this order

1. Open the signed-in page and get a live ticket picture quickly.
2. Use **Atvērt jaunāko nereģistrēto biļeti** to visually select and prove the newest current-or-upcoming unused ticket.
3. Use **Atvērt jaunāko biļeti un reģistrēt** for the same selection followed by one bounded activation action, or use **Reģistrēt atvērto biļeti** after a fresh slider proof. One action may make an initial 400 ms phone drag and one final retry only after fresh proof that the completed first drag left the exact same ticket unactivated.
4. The browser slider is a visible local authorization control aligned directly over ViVi's visible slider. It submits that same register action once at completion and does not maintain its own phone-control protocol.
5. For 15 minutes after a proven registration, and only after a newer unused ticket is visually proven, use the context-aware button to move directly between the two Aztec-detail views. The ticket list is a transitional phone view, never a successful resting state.
6. Request a control code when needed from the visible button or the invisible top-left start corner, which covers the left 50% of the first 25% of the viewport. The corner only opens the existing numeric request dialog; it does not prewarm the phone path, dismiss a result, or add a stream-wide gesture. When HDR is enabled, the same HDR view continues through the dialog and phone execution. The requester page freezes the exact generated frame in HDR only after that matching browser presentation completes; otherwise it shows the already-prepared local SDR freeze. The phone must not send a screenshot of that result.
7. An active owner may review and edit the single ViVi email/password pair on `/admin`, save it without touching the phone, clear it with confirmation, or separately confirm the standard non-destructive account-switch action. If ViVi is signed in on an exactly proved ticket detail, that action retains its private detail identity, navigates through ViVi's own account controls, proves an in-app sign-out, keeps all app data and the linked-device identity, then writes each saved field once and submits once from the proven login screen. Authentication surfaces are intermediate: the standard action publishes success only after at most three visually typed, journaled navigation steps restore that same ticket detail. A session-only option, off by default and captured when the confirmation opens, keeps that original-ticket restore as the first choice but permits one non-activating search for the newest unused ticket if the original ticket is unavailable; proving that no unused ticket exists is also a successful outcome for that selected request. If ViVi is already signed out, it starts from that proven login screen without a prior detail identity to restore. A separate, strongly warned **Full reset + re-authenticate** action intentionally deletes all ViVi app data and the linked-device identity first, so an external device-link reset may be required. The optional fallback does not change the full-reset path. Neither account-switch path falls back to a full reset, and all paths stop for manual attention on verification, device-link, profile, CAPTCHA, onboarding, unknown surfaces, or an uncertain sign-out.

Registration policy is per authenticated account: at most one admitted registration every 30 seconds and ten admitted registrations in a rolling hour. Control codes remain limited to two requests in a rolling minute. Admins and owners obey these limits by default and may persist an unlimited testing preference to their account; bypassed actions remain audited without consuming quota.

Each freshly authorized page opening retains the shared stream for 30 minutes from that opening, even after its browser disconnects. A later page opening renews that session's warm hold; media reconnects and first-frame presentation do not. Active viewers retain their own stream demand beyond that deadline. The warm hold does not add a visible viewer and requires no continuous browser frame delivery while nobody is watching.

The owner-only **Sleep / cold mode** control on `/admin` is the deliberate exception: it cancels the actual page-warm and startup timers, blocks new phone admissions and all rewarming, clears relay pictures, and asks the existing Pixel lifecycle owner to stop capture and release its secure-capture lease. One operation ID and its progress live in the existing stream desired-state row. The matching phone acknowledgement and the relay's empty/disabled state must both be proved before existing viewer pages reconnect in place; hidden pages reconnect on return. No-viewer completion leaves the phone asleep. A 15-second unproved shutdown remains paused and reports failure; it never silently retries. New ordinary openings then create the normal 30-minute hold again. Existing phone work, including queued work and cleanup, causes immediate rejection instead of queuing a stop.

An admitted demand-idle encoder is reusable even when its last picture expired. Reuse never permits stale picture presentation. The phone watchdog measures a missing requested picture from its first successful demand dispatch; repeated permits cannot extend that deadline. Replacement helpers within an admitted session do not wait for an already-consumed activation signal. The browser records one bounded navigation-to-first-presentation/ten-distinct-pictures summary in shared private operational logging.

Prove the result on the signed-in page. If a phone picture is needed, take it through the root path, then pull it.

The HDR settings show one fixed explanation alongside the existing switch and brightness selector. The redundant control-code and viewer summary cards are removed; the limits table and live viewer list with its count remain. Freshness checks still revoke action proof without hiding an established HDR picture between updates.

## Where to work

- Page: `web-client/ticket-app-source.js` and `internal/web/static/index.html.tmpl`. Rebuild the page bundle after page edits.
- Phone Ticket: `pixel-phone` Ticket files under the Android orchestrator. Do not split that service unless asked.
- Durable state: `spacetimedb/src/lib.rs`.
- Operator start/stop/health: `../../docs/runbooks/MODULE_TICKET_REMOTE.md`.
- Deep stream/capture note: `pixel-phone/docs/architecture/TICKET_STREAMING_ARCHITECTURE.md`. Open it only when the stream path itself is the task.

Generated copies such as `internal/web/static/app.js` and `internal/web/static/spacetime-client.js` are build output. Edit the source, then rebuild.

## Administrator statistics

`/admin?tab=statistics` shows the last 30 Europe/Riga calendar days of viewing
time and accepted slider-registration/control-code requests. Each action count
is successful/accepted. Only `register_current` from `browser_slider` contributes
registration counts; menu actions do not. Code success means confirmed phone
generation, not browser presentation or inspector use. Queued requests count at
acceptance, including later failures; blocked submissions do not count.

The existing command receipt owns the counted kind, original requester/time, and
success deduplication. Private `ticketremote_member_daily_actions` rows contain
four hourly counter arrays and expire after 30 calendar days. The private
tracking watermark starts at service bootstrap or the first counted admission
and is retained across deployments. Receipts created before tracking have no
counted kind, so delayed old results cannot create unpaired successes. Existing
viewing history keeps its prior retention. The administrator snapshot receives
these aggregates through service-only views; member and health responses exclude
them. No code digits, tickets, new event log, browser request, or poller is added.

Edit `web-client/admin-statistics-source.js` and rebuild. The same entry renderer
serves compact and detailed views, with counts only where attempts exist. Run
`make test`, `make spacetime-build`, `make web-client-build`, and
`make statistics-test`. The latter uses synthetic loopback browser pages and a
temporary database/module copy, including an old-schema migration check; never
publish the fixture module. Publish the additive production module with
`--delete-data=never` before deploying the sidecar/web service. Keep receipt
defaults for old retained rows; they carry no legacy execution path.

## Do not start here

Leftover Ticket history now lives in:

- `../../archive/ticket/`
- `../../docs/archive/ticket/`
- `pixel-phone/archive/ticket/`

Those folders are backups. Open them only when a task explicitly asks for old Ticket history.

## V2 release on 7 September 2026

Production runs `ticket-v2-v181-20260907-r5` with Pixel commit `4b81977` and the
existing data-preserving database identity. The owner-controlled maintenance
pause is off. The five main action routes each produced five successful results;
brightness and physical touch-interruption verification were explicitly excluded
by the user. Detailed acceptance and timing limits are recorded in
[the release result](../../.ai/result.md). This dated record is not a substitute
for checking the current live release before another deployment.

## Current control and presentation contract

The 8 September Pixel resource pass is deployed from implementation commit
`8ff29de`, with the server still on `ticket-v2-v181-20260907-r5`. It removes
redundant orientation and health work and retires unused DNS installations.
The shared-picture-copy experiment was reverted; capture ownership remains as
before. Full service health, live reconnect, browser-slider registration,
exact HDR control-code delivery and cleanup, and a fifteen-minute active memory
observation passed. RAM savings were not demonstrated. The user excluded
waiting for warm-session expiry. See the [measured resource report](../../../pixel-phone/ops/reports/2026-09/2026-09-08-pixel-resource-optimization.md).

- Slider placement uses the actual orange track and attached dark thumb in the existing detailed observation. The compact detector's safety padding and dark page borders do not enlarge the browser overlay, and CSS does not impose a larger minimum rectangle. Input still requires the full phone readiness fence.

- The phone publishes one current control observation directly to Spacetime, bound to its session, context revision, observation sequence, and a three-second expiry. The browser uses that subscribed row and a bounded database clock for readiness. Encoder output, relay reports, initial video, and HDR presentation cannot grant or revoke command authority.
- One clearly identified unactivated-detail observation supplies the normalized slider region. The phone still requires two fresh agreeing observations, exact private detail identity, foreground/input readiness, unchanged touch and capture generation, and display protection immediately before registration. Fresh first-stroke registration may reuse the ordinary capture owner's private matching pair and prepare it concurrently with protection. Both capture timestamps must remain strictly less than three seconds old; changed context, focus, touch, capture or another action invalidates the pair. Browser slider completion authorizes only that exact context.
- All current browser actions use the versioned member command envelope: opening, registration, switching, re-detection, control codes, and owner account switching. Immutable command identity, context, time and payload are deduplicated. Existing operation-specific quotas, owner checks, private credentials, one running plus one waiting slot, and atomic result settlement remain authoritative.
- Command receipt, progress and terminal delivery run independently of media delivery. A retained terminal result can be retried for acknowledgement; physical execution cannot be replayed. One registration may make its second stroke only under the existing conclusive first-stroke and unchanged-ticket proof rules.
- The existing capture owner shares one immutable picture between unencoded classification and the newest-frame encoder handoff. Ordinary and Ticket-action recognition can overlap encoding; control-code generation probes preserve classification-before-media ordering. No private startup capture window, extra capture loop, browser picture-change poller, or automatic `prove_current` discovery supplies readiness.
- Control-code semantic success and visual delivery are separate. Only the requester captures its matching streamed result; the phone never sends a result screenshot. Cleanup settles after the original detail and panel protection are proved, without waiting for a fresh encoded frame. Current readiness is published independently and cannot be renewed by replaying cleanup.
- HDR preferences and capability bootstrap with the page. GPU preparation overlaps database connection and video startup. The exact fresh-frame GPU and compositor checks, bounded activation, established-picture continuity and SDR fallback remain presentation safeguards. They do not gate Ticket commands. Observable presentation does not prove physical iPhone brightness.
- The registration slider keeps its existing native-scroll behavior: at least 8 px rightward travel, strictly less than 45 degrees from horizontal, or the keyboard threshold. Changed context, geometry, lifecycle, connection, expiry, a second pointer, or cancellation submits nothing. The top-left control-code corner remains passive and start-only.
- Registration quotas, the fifteen-minute switch policy, durable scheduled re-detection, owner credential rules, and page-opening thirty-minute warmth are unchanged. Saving credentials does not touch the phone. Account switching preserves the distinct standard, optional fallback, and explicitly destructive full-reset modes.
- Cut over additively: publish the database module, deploy the compatible phone, then deploy the browser/service. Retained legacy table shapes and terminal reconciliation may be removed only after old producers stop and pending work drains; never delete production data during publication. Once a backend has established its phone-control session, old-page mutation reducers reject with `ticket_client_reload_required`; only the common member command can admit new work. Retained legacy bodies and table shapes serve unmigrated backends and settlement only, and can be removed after all backends and retained clients have drained.
- Edit `web-client/` sources and rebuild generated assets. Commit complete release inputs before deployment. Use `kitty-gration` explicitly and preserve local-first mirrors and regular-user access.
- Verify the signed-in page, durable settlement, phone result, and final quiet state separately. V2 performance acceptance uses the actual signed-in Brave page, as requested on 7 September; desktop presentation does not establish physical phone brightness.
- Preserve the signed-in browser session, close only task-owned tabs, and keep private ticket content and credentials out of diagnostics. New bounded runtime events belong only in shared operational logging.

- Live transition checks found and fixed a leftover two-second continuity/spinner ceiling: browser and relay now share the existing three-second frame boundary (3000 ms accepted, 3001 ms rejected). Phone v369 renews its database clock independently of fresh readiness publication, preserving the three-second control expiry.

- Transition acceptance on 6 September: Pixel v372 overlaps ordinary classification with encoding on the same retained picture; browser v177 dismisses an exact HDR code result by seeding the existing renderer with the fresh live picture. The affected five-pass streaks, measured snags, and desktop-only limitations are recorded in [the transition report](../../ops/reports/2026-09/2026-09-06-ticket-slider-transition-streaks.md).

- Browser v178 abandons any in-flight readiness-clock refresh when its database connection ends. A replacement connection must acquire its own clock; late old responses cannot restore authority or clear the new refresh. This prevents a lost reducer reply from leaving registration and code controls disabled after reconnect. The ready slider uses a 5% base and has a passive white sweep over a three-second cycle (v180), with half the previous white-wave opacity; reduced-motion mode disables the sweep. It does not animate during registration or alter input geometry.

- The slider white sweep uses the active stream HDR brightness boost, preserving its 19% alpha and three-second motion, with the existing SDR and reduced-motion fallback. Its small transparent canvas shares the stream device and paints only when shown, resized, or its boost changes. The upper-left loading spinner uses 50% opacity in all modes.

- Browser v183 sizes the stream from the full layout viewport so the on-screen keyboard overlays the picture without shrinking either SDR or HDR. Only the control-code entry dialog follows the smaller visual viewport; normal window and orientation resizing still update the picture.

- Browser v184 opens that dialog without a dimming veil or broad shadow. It holds the existing page position through focus and keyboard viewport panning, then restores normal scrolling when the dialog closes.

- Server v186 redirects signed-out viewer and admin requests directly to the existing sign-in route, retaining their path and query. Login no longer loads the viewer bundle against an empty page. Verify fresh and stale-cookie redirects with `TestSignedOutPagesReachLoginWithoutJavaScript` and complete a private-browser sign-in.

  Deployed as `ticket-login-20260909-v186` on 9 September. Required builds/tests and standard production validation passed. A fresh email-link login in private Brave and a subsequent reload both reached the live ticket with advancing `LIVE_FRESH` pictures; served asset hashes matched. The task's private and email windows were closed, preserving the original browser session. No Ticket action was submitted during this login verification.

## Continuous browser recovery

Server v188 requires a current approved membership for every `/static/*` file, including old versioned URLs, and sends no-store headers for browsers and shared caches. Login remains independent of these files. `TestStaticFilesRequireCurrentMembership` covers the embedded file inventory, expired and removed sessions, conditional requests, and unavailable membership state. Purge the `ticket.jolkins.id.lv/static/` CDN prefix when deploying this change to remove previously public copies.

Deployed as `ticket-private-static-20260910-v188` on 10 September. Required builds/tests and standard production validation passed; the stale-code deployment check now reads the release bundle because anonymous downloads are forbidden. The Cloudflare dashboard purged the static prefix. All 11 authenticated asset hashes matched the build with no-store headers, 46 public old/new-version and conditional probes returned 401, and fresh private-Brave email login showed the live ticket. A separate signed-in reload reached `LIVE_FRESH` without console errors. Test windows were closed without submitting Ticket actions or changing normal sessions.

Browser v194 retains v187's unlimited recovery with one attempt in flight per
connection, a ten-second attempt limit and one-second retry delay. Commands and
media now recover independently, and the error panel follows local frame
silence as described above. Returning or going online expedites recovery; visible
viewing has no inactivity cutoff. Only a changed product version automatically
reloads the page. Expired authentication offers sign-in after the frame-silence
grace period while session checks continue. Owner cold completion resumes media
in place after the existing proof barrier without replacing the command
connection or renewing the page-opening warm lease.

HDR failures keep receiving ordinary pictures while HDR retries, preserving the saved preference. Browser v190 retains the last completed HDR surface until its replacement finishes; retained pixels never renew live action authority. A lost display device shows recovery while a new surface is prepared. Hidden-page and cached-page returns share HDR recovery, and displayed control-code results retain their original frame only until dismissal or expiry so they can be reprocessed without a new phone request. Reconnects never resubmit phone commands; pending results retain their identity, and abandoned callbacks cannot affect replacement connections. Browser fault coverage lives in the loopback-only recovery test and uses simulated services and decoder output with the real page and canvas presentation.

HDR recovery tests: `web-client/presentation.test.mjs` exercises the real controller with controlled GPU completion; `web-client/presentation-real-gpu.html` exercises ten replacements and exact-result restoration on the real GPU with synthetic pixels. The page exposes only the latest HDR recovery duration and selected color space for inspection. Physical iPhone contrast acceptance remains distinct from these checks.
