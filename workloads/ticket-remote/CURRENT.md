# Current Ticket

## September 23 owner/admin viewer emails

Owners and admins now see members' verified email addresses in the live viewer
list and directly in Statistics activity rows. The Statistics code legend and
People code badges are gone. Historical activity without a matching member is
labelled Unknown account. Ordinary members receive neither viewer identities
nor the real viewer count from the authenticated session response or database
viewer view. Four-character account identifiers remain internal where existing
controls still depend on them; invitation links and ViVi control codes are
unchanged.

Published the additive viewer-email column to `ticket-remote-prod-v3` without
deleting data, then deployed `00969df9` as
`ticket-viewer-emails-20260923-00969df9`. The full Ticket suite passed (89
browser/client tests, module and Go tests, 20 sidecar tests); the disposable
database privacy/migration check and Statistics browser layout journeys passed.
Standard deployment validation and the public release identity check passed.
The signed-in owner page showed a live 994x2046 ticket picture and email-labelled
viewers; Statistics and Members showed emails without code badges. A freshly
loaded page had no browser warnings or errors. Separate signed-in regular-member
production proof was not available; ordinary-member privacy passed the local
HTTP and database checks.

## September 23 two matching notification checks

Monitoring sends a problem notification only when two distinct consecutive
checks report the same issue. A different issue resets an unalerted streak;
two missed scheduled checks count as one unavailable issue. After a warning has
been sent, a fresh ready check sends one recovery. The five-minute phone check
cadence, owner settings, and device subscriptions remain in place. The existing
Pixel publisher already spaces repeated checks and needs no phone redeploy.

Published the compatible module to `ticket-remote-prod-v3` without deleting
data, then deployed `a332192c` as
`ticket-monitor-two-checks-20260923-a332192c`. The sent `busy` warning and
its receipt survived the module update. The full Ticket suite passed (88
browser/client tests, module tests, Go packages, and 20 sidecar tests), the
focused Pixel monitoring test passed, and both required builds succeeded.
Targeted deployment validation and mirror audit passed. The signed-in owner
Settings page showed the new copy and enabled monitoring; the live page showed
an Arrow-backed unused-ticket picture. The next ready report produced one
recorded sent recovery and left no pending push delivery. No stream command or
queued action intent remained. A deliberately triggered warning and physical
iPhone push were not part of production verification.

## September 23 invitation creator choices

The signed-in invitation creator starts with no trial duration or viewing
allowance selected in a browser that has not used it. Choosing either saves it
in that browser, and a custom duration is saved too. The choices survive a
reload and stay selected after creating an invitation. The private label is
never remembered and clears after a successful creation. Invalid or unavailable
browser storage leaves the creator usable with empty required choices.

Deployed `00df0c9e` as `ticket-invite-memory-20260923-00df0c9e` from a clean
checkout. The full Ticket suite passed (88 browser/client tests); the focused
browser journey covered reload, custom duration, creation, and unavailable or
invalid storage. A local visual preview showed restored selections. Deployment
validation passed, and the signed-in admin page showed the new empty choices.
The signed-in viewer returned to a live ticket stream, and the public health
endpoint reported the matching asset version. No production invitation was
created for this check.

## September 23 try-first invitation welcome

The invitation landing now puts the guest trial first, with a prominent
"Try Ticket now" button, secondary setup, and registration still available.
Latvian, English and Russian copy says the trial needs no account and points
to the installation options at the bottom of the registered Ticket page for
later setup. Trial, takeover, registration and install behavior are unchanged.

Deployed `d8077874` as `ticket-invite-try-20260923-d8077874` from a clean
checkout so unrelated invitation-admin edits stayed local. The web and
database builds and full Ticket suite passed (88 browser/client tests); the
clean-release invitation fixture passed all nine journeys. A phone-sized
preview showed the new button order and note. Deployment validation passed;
the signed-in live page showed a connected stream, Arrow UI, matching assets,
and the installation link as the final control. No live invitation was created
or redeemed for this copy change.

## September 23 HDR picture background

The HDR stream now renders its fitted ticket and surrounding fill on one
WebGPU surface. Both regions use the same source pixel, color space, and display
boost; the ordinary page background remains an unboosted SDR fallback and
browser theme tint. The 1px right and 15px bottom picture trims are applied
inside the HDR renderer. A stage resize shows the fitted SDR picture while a
new HDR surface is prepared, including for an exact frozen result; an unchanged
foreground return still retains its completed HDR picture. No phone action or
stored data changes are involved.

The real-GPU fixture checks matching edge, side and cropped-strip pixels at
identity, 2x, 4x and 6x, as well as slider, result and return behavior. The
browser page fixture checks portrait, landscape, wide and safe-area layouts.
Physical iPhone appearance remains a separate verification layer.

This is the first file to read for Ticket work.

## September 22 invitation trials

Web v232 adds one shared trial and one eventual member per invitation.
Members contains People and Invitations, with 1/3/5-day or custom deadlines,
5/15/30 viewing minutes, five successful activations and five successful control
codes. Unused expired links remain registration-only until revoked or redeemed.
The complete 32-character bearer link is shown only at creation; private database
records retain its SHA-256 fingerprint. Custom duration is 1..525600 minutes.

`spacetimedb/src/invitations.rs` owns private invitation, guest identity, action
reservation and member-source state. `spacetime-sidecar/src/invitations.rs`
exposes scoped service operations; `internal/web/invitations.go` owns token
exchange, separate signed guest cookies, install context and verified-email
redemption. Guest JWT subjects include both invitation and session. Takeover,
revocation and redemption fence database views/reducers and close media. Existing
members keep their role/source; removed members cannot regain access this way.
Unknown historical membership remains labelled Existing member.

The media writer reserves at most five seconds before delivering fresh frames.
Hidden, disconnected or stale delivery refunds unelapsed time; interrupted
reservations can retain at most five seconds. Each gateway permits one current
page/socket per invitation, matching the single deployed Ticket gateway. New
commands reserve allowance before dispatch; typed no-effect failures refund it,
success settles once, and uncertain outcomes retain it. Accepted actions receive
bounded result delivery after trial expiry, without admitting new actions.

Invitation welcome, trial summary and installation context support LV/EN/RU.
The uncached personalized manifest keeps the existing app ID and an invitation
launch URL. Native Alpha and browser-switching guides copy the same invitation;
installed apps have manual link entry. Opening a fresh link or instructions
starts no trial/media work. Verified existing SpacetimeAuth sign-in atomically
redeems the invitation and issues the normal remembered session in that browser.
Cross-app email returns never bypass the existing browser/PKCE check.

Run `make invitation-test` for disposable real-database journeys, browser
onboarding/admin checks and focused Go authority tests. Publish the additive
module with `--delete-data=never` before the compatible gateway/sidecar/browser.
Rollback the web release first and retain invitation tables and consumed state.
Candidate validation passed: both required builds, 87 client/browser tests,
39 module tests, all Go packages and 20 sidecar tests; the 30 focused Go guest,
invitation and streaming tests also passed with the race detector. The disposable
invitation database fixture passed seven journey groups, plus the existing
check-in and action-statistics fixtures. Installation checks cover 94 invitation,
117 welcome and 895 ordinary-guide assertions. The deployment contract and
pre-release mirror audit passed. Interactive local welcome, install and admin
previews were inspected.

Published the additive module without deleting data and deployed `b39b21f0`
as `ticket-invitations-20260922-v232`. Standard deployment validation passed;
the local mirror was refreshed and audited clean. The saved owner browser
verified People/Invitations, creation, copy-once display, copy action and
revocation. An existing-member invitation visit went directly to Ticket without
consumption, source/role changes or a guest trial; its unused test invitation was
revoked afterward. The live viewer mounted Arrow, served v232 assets, showed
LIVE_FRESH HDR with unchanged 994x2046 source dimensions and the unused-ticket
slider, with no captured browser warnings/errors. No ticket action was submitted.

The anonymous production HTTP check also verified clean landing, unchanged
allowances before start, personalized uncached manifest, no analytics injection,
guest tokens without verified email, start/resume, explicit takeover and old
HTTP/database-token denial, guest admin/asset denial and fresh binary media.
Closing the stream released its reservation; 280ms was charged and both action
allowances stayed intact. The controlled guest invitation is revoked and further
guest-token issuance is denied. A smoke-script assertion expected zero rather
than the stored release timestamp; a separate read proved cleanup. Live
post-consumption resume and revoked-link HTTP redirect were not reached; their
local coverage is not claimed as live proof. No member was created for testing.

Release acceptance remains incomplete in two areas. Physical iPhone PWA and
Android Native Alpha installation, email return and remembered relaunch checks
require the target devices and an authorized test email. Separately, the public
edge still adds Cloudflare NEL reporting headers; HTTP error reports can carry
query strings even though application analytics and logs omit invitation tokens.
The Ticket-only clearing rule is prepared in
[`infra/arbuzas/cloudflare`](../../infra/arbuzas/cloudflare/README.md), but the
existing API token lacks rule access and the browser requires Cloudflare sign-in.
Do not treat the complete error-report privacy requirement as verified until
that rule is applied and checked. Unrelated local notification edits were
restored and their bundle regenerated after deployment.

## September 22 first-visit welcome and Android installation alternatives

Signed-out mobile visitors see a small welcome before authorization. Either
button saves `ticket.welcomeAcknowledged` in browser storage; subsequent visits
skip it even after logout or session expiry. Installed standalone/fullscreen
PWAs (including iOS standalone), desktop browsers and approved signed-in
members bypass the welcome. Storage failure cannot block sign-in, and a normal
authorization link remains usable without JavaScript. Authentication lifetime
and membership authority are unchanged.

The welcome uses the saved LV/EN/RU language or the first supported browser
language, falling back to English. Its guide opens at the detected iPhone/iPad
or Android page with device switching and a sign-in link. The shared guide
styles and six illustration URLs are explicitly public; other private assets
remain protected. The public shell starts no viewer, database or phone work.

The Native Alpha guide now presents GitHub as the free immersive option and
Google Play as the easier installation, with separate free and paid Plus links.
Latvian, English and Russian explain the edition differences. Expandable GitHub
instructions identify the exact v1.5.2 APK, installation permission and browser
fallback; no GitHub account or app is required. One shared setup follows both
choices, with immersive settings limited to GitHub/Plus and link editing in a
separate disclosure.

Deployed from `e7b056b6` as `ticket-welcome-20260922-v231`. Both required
builds passed, alongside the full suite (86 client/browser
tests, 33 module tests, Go packages and 18 sidecar tests), and the deployment
contract. Welcome tests cover 108 checks across ten journeys; the shared guide
covers 895 checks across 320, 390 and 1440px and standalone/fullscreen modes.
Interactive preview verified the welcome layout, translations, direct Android
instructions, public iPhone illustrations, sign-in and repeat-visit bypass.
Standard deployment validation and mirror audit passed. Anonymous production
serves the welcome with no private viewer configuration; all eight public guide
assets match the release, private assets remain gated, and admin authorization
preserves the requested destination. The saved signed-in browser session opens
the viewer directly with v231 assets, fresh HDR and the unused-ticket control.
The shared Android alternatives passed live Latvian, English and Russian checks
with no captured warnings/errors. Latvian was restored and verification tabs
closed; no physical ticket action was submitted. Physical PWA launch/install
and Native Alpha sign-in/playback remain unverified.

## September 22 compact check-in and viewer languages

The check-in list item opens one sheet directly. Each direction appears once,
with occupied carriages, active counts and the latest check-in time (including
renewals). Only the existing 40-minute active window appears; expired and
checked-out history is hidden. Existing private retention, notice cooldown,
account revision fences and phone isolation remain unchanged. Confirmation
stays in the sheet, with change/renew and checkout available from the summary.

The viewer language control sits beside Admin, or alone for regular members.
Latvian, English and Russian cover ordinary viewer menus, statuses, accessible
labels, check-in and installation instructions. One browser-local preference
updates these in place; the installation selector shares it. Admin management
and the streamed ViVi picture retain their own language. No schema or phone
deployment is needed.

Deployed from `a3cdd388` as `ticket-checkin-languages-20260922-v230`.
Both required builds and the full suite passed (85 client/browser tests,
33 module tests, Go packages and 18 sidecar tests), plus the real-database
check-in fixture and deployment contract. The signed-in page confirmed a live
check-in, persistence through reload, checkout returning the active count to
zero, shared Russian installation copy, all three viewer languages, matching
v230 assets and fresh HDR. Browser language was restored to Latvian and the
verification tab closed. No physical ticket action was submitted.

The first release rolled back because the old deployment probe rejected all
localStorage use. The probe now rejects the retired authentication-storage keys
while allowing the non-secret language preference; standard deployment
validation passed. Admin management copy remains outside viewer localization.

## September 22 right-edge trim

The shared picture clip removes one displayed pixel from the right edge, as
well as the existing 15px bottom strip. Ordinary, HDR, retained HDR and frozen
pictures expose the matching background at the edge without resizing the
canvas, changing picture proportions or moving the registration control.

Deployed from `7878f747` as `ticket-right-edge-20260922-v228`. Required builds
and the full suite passed (85 client/browser tests including responsive crop
checks, 33 module tests, Go packages and 18 sidecar tests). Signed-in production
showed fresh HDR with a clean right edge, the new shared clip and unchanged
994×2046 source / 415×856 fitted dimensions. Assets, standard deployment
validation and mirror audit passed. Unrelated local edits were restored unchanged.

## September 22 responsive picture background

The space around the fitted ticket picture now uses its sampled lower-left
background color, including the 15px cropped strip. A one-pixel sampling canvas
reads only accepted source updates, not slider animation. The fill follows the
actually displayed SDR/HDR gain and retains the frozen/held picture's color.
Sampling failure cannot interrupt the stream. Cold clear releases the sampler
and removes the color. The fill is for the dark ticket background; CSS colors
clamp highlights above SDR white.

The page and supported browser chrome share that color. Installed iOS pages
request a translucent status area; picture fitting reserves reported safe-area
insets. Portrait, landscape and wide screens retain the source aspect ratio,
canvas dimensions, shared picture bounds and registration geometry.

Deployed from `d0424dd2` as `ticket-background-fill-20260922-v227`. Required
builds, the full suite (85 client/browser tests, 33 module tests, Go packages,
18 sidecar tests) and real-GPU HDR verification passed. The 57 new browser
checks cover portrait, landscape, wide screens and simulated safe-area insets;
presentation tests cover HDR gain, frozen/held color, fallback and sampler
cleanup. Signed-in production verified fresh HDR and filled side/bottom space
at 390×844, 844×390 and 1440×900, with source dimensions still 994×2046 and v227
assets. The current live ticket was already registered, so no live registration
gesture was submitted; unused-ticket hit geometry passed in the local fixture.
Standard deploy validation and mirror audit passed. Physical iPhone status-area
appearance remains unverified. Unrelated local notification edits were restored
and their generated bundle rebuilt after this isolated release.

## September 22 picture bottom crop

The viewer clips 15 displayed CSS pixels from the bottom of the ordinary, HDR,
retained HDR and frozen control-code pictures. Frozen images share the existing
stream bounds so the crop stays picture-relative with letterboxing. Canvas and
source dimensions, picture scaling, registration coordinates and phone capture
remain unchanged. The amount is display-relative, not 15 Android source pixels.

Deployed from `abf51898` as `ticket-bottom-crop-20260922-v226`. Required builds
and the full suite passed (84 client/browser, 33 module and 18 sidecar tests,
plus Go packages). Signed-in previews checked ordinary/HDR pictures and phone
width; the deployed page showed fresh HDR, its registration control, the 15px
crop and no white Android bar or captured browser errors. Source dimensions
remain 994×2046. Public and signed-in assets match v226, standard deployment
validation and mirror audit passed, and unrelated local notification edits were
restored unchanged. No real ticket action was submitted.

## September 22 administration navigation

The admin interface now has task-focused pages at `/admin?tab=…`: Overview
for health and notifications, Tickets for the latest ticket and schedules,
Members for access, Statistics for activity, and Settings for testing limits,
device selection and collapsed diagnostics. Owners also have a separate ViVi
account page; credentials are no longer loaded on Overview. Stream sleep stays
on Tickets, and its state connection works independently of Settings. Successful
forms return to their own section. Emergency account reset and detailed schedule
records use native disclosures. All destinations remain visible on small screens.

The regular ticket viewer keeps its existing structure and controls. This is a
navigation change; account authority, ticket actions and physical phone behavior
retain their existing owners and confirmations.

Deployed from `ea63cec3` as `ticket-admin-navigation-20260922-v225`. Both
required builds, the full suite (84 client/browser tests, 33 module tests, Go
packages and 18 sidecar tests), and the separate responsive Statistics checks
passed. Focused checks cover role-specific navigation, page-data isolation,
section-preserving form results and 35 independent sleep/limits state checks.
The local browser verified all six pages at 320px, keyboard navigation,
disclosures, member add/remove and contained diagnostic scrolling. Signed-in
production verified all six destinations, live owner/settings controls and
monitoring, compact Statistics, matching v225 assets and a fresh ticket picture,
with no captured browser warnings/errors. Overview fell from 4,046px to 856px
at the same saved browser size. No real ticket action or credential change was
submitted. Standard deployment validation and mirror audit passed. Existing
local notification edits were restored exactly and excluded from this release.

## September 22 Russian installation instructions

The complete installation guide now includes Russian alongside Latvian and
English. A native language selector retains the current guide and covers all
menus, Safari/Chrome/Firefox/Native Alpha steps, prompt and clipboard messages,
screenshot descriptions and accessible labels. Original third-party screenshots
remain unchanged, with a translated explanation that they show English examples.

Deployed from `965236d6` as `ticket-install-russian-20260922-v224`. Required
builds and the full suite passed: 83 client/browser tests, including 815 guide
checks across 320, 390 and 1440px plus standalone/fullscreen launch; module, Go
and 18 sidecar tests also passed. Standard production validation, public and
signed-in v224 asset versions, all four Russian guides and language switching
passed. The live page showed the unused ticket and its registration oval with
Arrow mounted and no captured browser warnings/errors. No ticket action was
submitted. Mirror audit was clean; local notification edits were preserved and
excluded. Native Alpha acceptance on a separate Android device remains open.

## September 22 installation options

Web v223 groups Chrome and Firefox under Android → Browser installation and adds
Android → Full-screen viewer with Native Alpha setup and editable-link
instructions. Installed apps retain access to these options. Both Latvian and
English preserve the current menu when switching language; Back moves one level.
Ticket link copying excludes query parameters and fragments.

Deployed from `9af82aa0` as `ticket-install-options-20260922-v223`. Required
builds and the full suite passed, including 83 client/browser tests (428 focused
guide checks), database and Go checks, and 18 sidecar tests. Production standard
validation, public and signed-in asset versions, live Android menu navigation,
both languages, link copying, the live ticket picture and final mirror audit
passed. The page mounted Arrow and produced no captured browser warnings/errors.

The user explicitly authorized deployment before separate Android acceptance.
Native Alpha sign-in and live playback remain unverified on Android. The source
Pixel was not repurposed for viewer testing, and no APK was downloaded or
installed. This release retains v222 HDR recovery; unrelated local notification
removal was excluded and its original source edits restored after deployment.
See the [release and acceptance report](../../ops/reports/2026-09/2026-09-22-ticket-installation-options.md)
for the remaining device checks.

## September 22 two-stage HDR recovery

Web v222 starts HDR recovery immediately on opening or returning, then performs
one follow-up after one second. A usable renderer keeps its canvas, device and
brightness and reconfigures only while submitting a prepared boosted picture,
without repeating the ordinary-brightness activation frame. Slow initial work
finishes before the follow-up; a failed initial attempt receives one full retry.
Departure, HDR-off and disposal cancel pending work. The existing page check and
focus handler detect wall-clock gaps over one second when departure events are
missed. Frozen results retain their exact picture and freshness rules.

Deployed from `16198e71` as `ticket-hdr-two-stage-20260922-v222`. Required builds,
83 browser/client tests, 33 database tests, Go tests and 18 sidecar tests passed
before release. The isolated HDR-only release passed 37 focused/page tests and Go
checks. Real-GPU opening and frozen-result follow-ups each configured once,
emitted no identity frame, preserved sampled pixels and reused their resources.
Production validation, the signed-in live page, authenticated bundle hash and
final mirror audit passed. The page loaded v222 with Arrow mounted, live video,
HDR ready, no HDR error, a usable slider and no stream spinner or console errors.

Physical iPhone brightness, flicker and native sleep/return remain unverified.
The browser-tab check did not produce a genuine hidden transition, so it is not
claimed as native-return proof. Separate installation-guide and notification
changes were excluded from that release. See the
[release report](../../ops/reports/2026-09/2026-09-22-ticket-hdr-two-stage.md).

## September 22 live viewer-role revocation

Web v221 hides the viewer section, Admin link and notification controls when the
existing live account state no longer grants owner/admin privileges. It clears
the displayed viewer identities and count at the same time, without reloading
the page or submitting any ticket action. Admins who choose ordinary action
quotas retain access. The database privacy restrictions are unchanged.

The real-page browser regression covers demotion, restored privileges and unknown
role state; the HTTP test retains owner/admin access and excludes ordinary users
from both viewer markup and detailed health. Deployed from `58e5f146` as
`ticket-viewer-role-20260922-v221`. Required builds, the full test suite, production
validation and the deployed bundle check passed. A signed-in administrator who
obeyed ordinary quotas retained viewer access; demotion in that same open page
then immediately removed all privileged controls while viewing and check-in
history stayed available. The three independent live test accounts also completed
eight phone actions with matching saved results and returned browser pictures.
See the [verification report](../../ops/reports/2026-09/2026-09-22-ticket-three-user-test.md)
for coverage, isolated timing/queue checks, exclusions and cleanup.

## September 22 quiet HDR foreground recovery

Web v220 remembers window focus loss, hidden visibility and page exit immediately.
The first visible return consumes that marker and rebuilds HDR once, including
after a previous renderer failure. Overlapping focus, visibility and cached-page
return events do not repeat the attempt. Focus changes between page controls do
not count; focus-only returns leave the media connections alone. The marker is
page-local, with no background timer or persistent storage.

Recovery retains the completed HDR picture until a fresh replacement is ready,
or leaves the ordinary picture visible if HDR had already failed. HDR preparation
alone no longer shows the stream spinner. A failed attempt falls back quietly and
waits for the next foreground return or an explicit preference change. Saved HDR
off and brightness choices, frame freshness and exact control-code results remain
unchanged. Browser events cannot guarantee a one-second notification if iOS has
already suspended execution; physical iPhone brightness still needs observation.

Deployed from `f55f782f` as `ticket-hdr-foreground-20260922-v220` on September 22.
Both required builds, the full test-suite rerun, focused recovery checks and real-GPU
checks passed. Standard production validation, public version, deployed bundle
hash and the final mirror audit passed. The first full suite had a notification
fixture browser-launch timeout; that test passed alone and in the full rerun.
Signed-in native return and physical iPhone brightness remain unverified. See the
[release report](../../ops/reports/2026-09/2026-09-22-ticket-hdr-foreground.md).

## September 22 check-in diagram refresh

Web v219 fixes history/notice miniatures retaining an earlier carriage or count
after the report text updated. Nested miniature rendering is now reactive when
Arrow reuses a group row. The selection form's top-to-bottom order was already
correct. Browser coverage now checks actual visual positions for all four
carriages in both directions, replacement from fourth to first, count changes,
and history/form dismissal and focus return. Check-in data and timing are unchanged.

Deployed from `1155aa55` as `ticket-checkin-view-20260922-v219`. The regression
failed before the fix and now passes 45 checks at each of 320, 390 and 1440px;
history and form previews were visually inspected. Required builds and the full
test suite passed. One unrelated slider pixel check failed in the first combined
run, then passed both alone and in the full rerun without source changes.
Production validation, live version, deployed bundle hash and connected sidecar
passed. Signed-in browser verification remains blocked by the existing URL policy.

## September 22 compact check-in entry

Web v218 puts the check-in section inside a native disclosure, collapsed by
default. Its blue entry is 88px tall (twice the 44px action buttons), shows a
brief current status, and is the only highlighted main control. The control-code
button and expanded check-in actions use the ordinary dark style. Check-in
dialogs, notice timing, database behavior and ticket operations are unchanged.

Deployed from `0e8671c5` as `ticket-checkin-button-20260922-v218`. Required
builds/tests and standard production validation passed; deployed script, CSS
and template hashes match. The local mobile preview and 33 check-in checks at
each of 320, 390 and 1440px passed. Signed-in live verification remains blocked
by the browser URL policy already encountered in this thread.

## September 22 voluntary train check-in and private viewers

Web v217 adds a Latvian Arrow check-in island above the ticket actions: two
directions, four carriages counted from the front, explicit confirmation,
replacement and early check-out. It never creates a phone command or registers
a ViVi ticket. Spacetime owns one latest report per account, 40-minute activity,
120-minute history, anonymous grouped views, and one opening notice per account
per 120 minutes only when another active member has a fresh report. Existing
policy-boundary timers expire and remove reports without browser polling.
An account revision remains after report removal to fence delayed retries.

Raw viewer, desired-stream, phone-report and relay-report tables are private.
Current owners/admins retain viewer access; ordinary members receive only the
cold-restart fields they need. Detailed HTTP health is admin-only. Pixel v394
and the sidecar subscribe to the restricted service views. The release order is
additive views, compatible Pixel and sidecar/web, then private-table cutover;
all publications use `--delete-data=never`. Do not downgrade those consumers
to raw-table subscribers after the cutover.

Checks: `make test`, `make spacetime-build`, `make web-client-build`, and
`make checkin-test`. The last command uses only a disposable loopback database
and synthetic browser pages, including the data-preserving public-to-private
migration. Never publish its fixture module to production.

Deployed from `51895693` as `ticket-checkin-20260922-v217`, with Pixel
`f3550a3` / v394. Compatible publication, scoped client deployments, final
private-table publication, and post-cutover production validation all passed.
Live schema confirms the six source/account tables are private; sidecar and
phone remain healthy. Signed-in browser acceptance is still blocked by Browser
Use URL policy. See the [release report](../../ops/reports/2026-09/2026-09-22-ticket-checkin.md)
for the tested behavior, production evidence, and remaining acceptance gap.

## September 22 false monitoring alerts

Pixel v393 keeps a successfully requested asynchronous monitoring capture pending
until the classifier reports its real result. Previously, a warm stream with no
viewer demand immediately reported capture unavailable; that artificial timestamp
could also reject the real result as older, causing repeated problem/recovery
notification pairs. Failed dispatch and overdue observations retain their existing
alerts. No phone action, additional capture loop, or server change was added.
See the [incident report](../../ops/reports/2026-09/2026-09-22-ticket-monitoring-false-alerts.md)
for deployment and live verification. The owner's enabled Monitoring setting and
private device subscription are preserved.

## September 22 slider input-service repair

Pixel v392 requires a connected input service before publishing action readiness
or reusing registration evidence. Connection changes invalidate the old context
and fence earlier captures. Recognized-screen monitoring remains independent.
Ticket-scoped SSH deployment restores the existing accessibility component and
checks that Android actually bound it after app replacement. Notification
listeners and other services are preserved. Web v216 reports missing phone
control accurately without changing slider gestures, retries or ticket identity
checks. See the [incident report](../../ops/reports/2026-09/2026-09-22-slider-input-service-repair.md)
for the three pre-dispatch failures and remaining signed-in acceptance.

## September 22 optional readiness monitoring

v215 and Pixel v391 add an owner-controlled monitoring switch, off by default,
and per-device Web Push subscriptions for active owners and administrators.
The existing Pixel classifier supplies active observations; an idle check every
five minutes takes one bounded capture through the same helper before any encoder
is constructed. Cold shutdown is allowed to settle first; asleep mode still
permits monitoring. Checks never navigate, tap, or renew three-second action
readiness. Observation timestamps use the existing database clock anchor.

SpacetimeDB owns current health, the five-minute incident threshold, subscriptions,
and bounded delivery receipts. Missing checks are reported as unable to verify,
not as a known blocked ticket. Idle detection normally takes five to ten minutes.
The existing server sends one problem notification and one verified recovery;
roles, monitoring epoch, phone session, observation age, and delivery claims are
checked before accepting work. Disable cancels pending delivery. No new automatic
recovery actions are added. The worker caches no private page or ticket content.

Routine healthy checks update current state only. Transitions and delivery
outcomes use the shared operational log with unchanged six-hour Ticket retention.
The logging/resource audit is in
`ops/reports/2026-09/2026-09-22-ticket-monitoring-audit.md` at repo root.
The compatible releases are deployed. Initial idle checks and restart persistence
passed; the owner subsequently enabled Monitoring and subscribed a device. The
September 22 iPhone screenshot confirms problem and recovery delivery, although
it does not establish whether the app was closed on arrival. The signed-in journey
remains unverified by browser automation. See the
[rollout report](../../ops/reports/2026-09/2026-09-22-ticket-monitoring-rollout.md)
for evidence and the remaining device/browser acceptance steps; source and
fixture tests alone do not establish notification delivery.

## September 21 continuous slider drag

v214 keeps an established horizontal drag active when the finger returns to or
past its starting position, including slight vertical drift. The thumb clamps
at the start and follows the same finger forward again until release. Initial
vertical scrolling still cancels; release still requires the existing rightward
travel and angle, and all phone-context, layout, capture and lifecycle checks
remain active. The shrinking picture, HDR treatment and three-second cover are
unchanged.

Deployed from `8113dd6c` as `ticket-slider-drag-20260921-v214`. All local checks
and standard production validation passed; runtime version and the release
bundle hash match. The real-page browser fixture passed 174 checks, including
the 12-check reversible-drag regression. Signed-in live touch verification
remains blocked by browser URL policy. See the
[release report](../../ops/reports/2026-09/2026-09-21-ticket-slider-drag.md).

## September 20 independent statistics and shrinking slider

v213 loads `page-activity.js` separately before the viewer. It immediately samples
visible viewing time and repeats every five seconds, independently of media,
phone readiness and the browser database subscription. Samples are saved to an
account/ticket-scoped IndexedDB outbox and delivered asynchronously through the
signed-in `POST /api/v1/activity` route. Hidden or suspended intervals are never
backfilled. Offline visible samples remain queued for the current and previous
29 Riga calendar days; unavailable browser storage uses a diagnosed in-memory
fallback that cannot survive closing the page.

The HTTP handler derives account and ticket from the signed-in session/config.
Its narrow sidecar reducer rechecks membership atomically, writes private daily
aggregates, and deduplicates UTC five-second slots across retries, tabs and
out-of-order deliveries. The existing hourly display and registration accepted /
confirmed-successful counts remain unchanged. Defaulted coverage fields preserve
historical totals; missing historical samples cannot be reconstructed. Existing
37-day database row retention remains unchanged.

One tracker owns version refresh from both activity responses and media notices.
It persists the current sample before reloading and never resubmits an action.
The old member tick reducer remains only for already-open pre-v213 pages; remove
it after those deployed callers have drained. Publish the additive database
module with `--delete-data=never`, then deploy sidecar and web together.

Deployed from `503a8df6` as `ticket-async-statistics-20260920-v213` on
20 September. The data-preserving module publication, scoped web/sidecar deploy
and standard Ticket production validation passed. Runtime version and release
asset hashes match; no phone command remains pending. The combined browser
fixture proved an actual reload preserves queued activity without replaying a
running registration. Signed-in production visual/counting verification is
still blocked by browser URL policy. See the
[verification report](../../ops/reports/2026-09/2026-09-20-ticket-async-statistics.md).

The orange slider now shortens behind its moving black thumb, keeping its right
edge fixed and reaching a circle at full travel. Cancellation regrows the track.
The full input region, swipe threshold, text fade, HDR path and three-second
registration cover are unchanged. Edit sources and regenerate all browser assets.

## September 20 registration transition cover

v211 retains the validated slider region for 3,000 ms after a local
`register_current` trigger. The existing painter draws only the sampled card
background during that interval, hiding both slider versions and the native
activation motion while new phone pictures continue through the same SDR/HDR
path. Swipe, keyboard, assistive activation and the registration button use the
same submission path. Invalid gestures never start a cover. This local cover
does not report success or delay, retry or change the phone action.

The deadline starts before sending the request and is never extended by phone
updates or an early result. Input stays hidden while covered. Expiry reveals
the actual current picture, including an unfinished action if the phone needs
longer. Hidden time counts and resume checks expiry before restoring the
picture. A known rejection clears the cover with the rejected request; uncertain
delivery retains existing request ownership. Frozen control-code results bypass
composition, and the existing source-freshness rules remain intact.

Validation passed all 56 web tests, including 149 normal browser journey checks
and eight font-failure checks, all 44 slider pixel/animation checks, Go tests,
24 module tests, 17 sidecar tests, bindings validation and both builds. The
journey verifies cover at 2,999 ms and removal at 3,000 ms, early activation,
incoming registered pictures, stale-frame restoration and expired background
return. The real-GPU probe matches covered card pixels, accepts new source
updates and restores their raw pixels while preserving freshness, exact-result
safeguards and the same HDR resources with zero reconfigurations.

Deployed `ticket-slider-transition-20260920-v211` from `6bcd96df` at 00:58
Europe/Riga on September 20. Standard release validation and public v211
identity checks passed. No Ticket action remained pending. The host mirror
was pulled after deployment and its audit is clean; local outputs remain
accessible to the workspace user. The Chrome control tool is not available in
this session and its documented diagnostic reports Chrome is not running;
the existing signed-in fallback is URL-policy blocked. No physical iPhone
acceptance, Pixel action or browser recording is claimed for this release.

## September 19 slider border polish

v210 fixes the dark rectangle shown in the user's v209 screenshots. WebKit's
`VideoFrame` drawing path ignores its source crop, so the backing strip contained
the whole phone picture squeezed behind the pill. The painter now samples the
card from the frame already drawn on its reusable canvas. The approved slider
appearance, wave, gestures and HDR presentation are unchanged. The relevant
[WebKit implementation](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/html/canvas/CanvasRenderingContext2DBase.cpp#L1597-L1617)
was verified on September 19.

The regression reproduces the old border by emulating that WebKit behavior and
passes with the fix for native-size and cropped/scaled video frames. All 38
slider pixel/animation checks, all 56 web tests, Go tests, 24 module tests,
17 sidecar tests, bindings validation and both builds passed. Generic white,
gray and dark card previews have clean borders. Real-GPU checks preserve the
same resources, raw exact results and numeric colors, with zero canvas
reconfigurations. These checks do not establish physical iPhone acceptance.

Deployed `ticket-slider-border-20260919-v210` from `63bf3edc` at 20:22 UTC.
Standard release validation passed and public liveness reports v210. No Ticket
action remained pending. The host mirror was pulled after deployment and its
final audit is clean; modified files remain accessible to the workspace user.
The signed-in visual check remains blocked by the browser URL policy. No phone
action or browser recording was performed for this border-only correction.

## September 19 picture-composed registration slider

v209 replaces v208's separate white backing after the user's screenshot showed
its visible rectangular seam. The local slider, sampled card backing and wave
are drawn into one reusable picture canvas and receive the same HDR treatment
as the rest of the frame. The transparent browser button keeps the existing
eight-pixel swipe-anywhere trigger, pointer capture, keyboard and accessibility
activation. A font download failure keeps those inputs available over native
pixels. Native busy/result pictures still take over after command admission;
the browser does not invent a successful phone outcome.

Local animation uses the existing serialized graphics owner. It preserves the
source timestamp and feedback, gives new source pictures priority, and stops
when hidden, frozen or expired. Raw frames remain separate for exact control-code
results. An HDR snapshot failure falls back silently to SDR with the settings
error. There is no extra visible CSS backing or separate HDR wave canvas.

Local pixel checks cover white, gray and dark cards, removal of native corners,
typography, drag, return and reduced motion. The real-GPU 994 × 2046 probe kept
the same canvas/resources with zero reconfigurations; eight BT.709 samples
outside the slider were numerically identical before and after composition.
Thirteen drag samples had a 16.6 ms median and 18.3 ms p95. Ten background-return
simulations and raw exact-result checks passed. These are desktop software
checks, not physical iPhone HDR acceptance or a high-frame-rate native gesture
recording. Native release timing and text-fade matching remain unverified; the
browser currently uses a 200 ms eased return and progress-based text fade.

Deployed `ticket-picture-slider-20260919-v209` from `acdd5a52` at 19:27 UTC.
Standard release validation passed and public liveness reports v209. All 56 web
test cases passed, with the SDK case rerun after concurrent regeneration briefly
removed its generated import. The font-failure browser mode passed five checks;
the normal journey retained eight actions, one code request and one capture
acknowledgement with no browser errors. Go, 24 module tests, 17 sidecar tests,
bindings validation and both builds passed. No Ticket action remained pending.
The host mirror was pulled after the release change and its final audit is clean.

Signed-in visual acceptance remains blocked by the browser URL policy. Static
assets also require authentication, so an unauthenticated asset request returned
401; it was not retried through another access route. No Pixel input or recording
was started for this correction. Exact native motion still needs a physical-user
gesture recording: the installed custom ViVi widget does not expose its timing,
and the phone has no external partial-drag command through its action owner.

## September 19 single visible registration slider

v208 covers the native streamed slider whenever the local browser slider is
visible. A noninteractive white card backing sits above SDR, current HDR and
retained HDR pictures, underneath the local rounded slider. It covers the rounded
corners plus one sample from the phone's refined 192 × 288 edge probe, clamped
inside the displayed picture. The backing shares the slider's proof, hidden,
details-view and frozen-result visibility. Activation bounds, trigger and wave
are unchanged; the media canvases and HDR renderer are not modified.

Validation passed all 50 web tests, including 142 actual-browser journey checks
with zero browser errors and unchanged action counts, plus Go, 24 module and 17
sidecar tests and both builds. A loopback compositor probe with an intentionally
visible stream marker behind all three picture layers showed zero marker pixels
inside the cover. Signed-in live appearance and target-device HDR white matching
remain unverified because the existing browser session was policy-blocked.

Deployed `ticket-slider-cover-20260919-v208` from `79639f0f` at 18:13 UTC.
Standard release validation passed, the public liveness response reports v208,
and no Ticket action remained pending. No phone action or browser session change
was performed for this release.

## September 19 native ViVi slider appearance

v207 replaces the v206 approximation using a direct Pixel reference: golden
`#f7b500` track, `#262b2f` handle, narrow yellow rim, Material-style arrow and
centred Roboto lettering. Geometry and spacing scale from the observed 932 × 147
slider. Two preloaded, self-hosted Roboto subsets contain only these labels;
their OFL notice is shipped beside them. The type weights are calibrated for
the browser rendering. No proprietary ViVi font or ticket image is served.

The eight-pixel rightward trigger, starting anywhere on the slider, pointer
capture, scroll cancellation, keyboard/assistive activation and three-second
HDR/SDR wave are unchanged. Handle travel now follows the measured rim geometry.
HDR presentation, fallback and operational logging are unchanged from v206.

Local validation passed the full test suite and both builds. A real loopback
browser loaded both fonts and rendered the slider at 280, 332, 440 and 932 px;
the existing 120-check browser journey retained its eight admitted commands and
zero browser errors. Direct Pixel secure recording produced clear native pixels,
but the quiet capture supplied only seven changed frames over 24.14 seconds:
high-frame-rate drag/return motion and physical finger feel remain unverified.
The signed-in browser harness blocked the task tab under its URL policy, so
these local checks do not establish production visual acceptance.

Deployed `ticket-native-vivi-slider-20260919-v207` from `1bf22b9a` at 16:16 UTC.
Standard release validation and public `/api/v1/livez` identity checks passed.
No Ticket action remained pending. No phone input was issued during the native
reference capture; temporary recordings, full ticket pictures and the copied APK
were removed. Only generic slider crops were retained for visual comparison.
The task-created browser tab/group was closed without changing saved sessions.

## September 19 steady HDR and local ViVi slider

v206 keeps the WebGPU canvas configured while completed pictures replace its
contents. Ordinary updates reuse the same visible HDR surface. Background-return
replacement still retains the previous picture until the replacement is ready.
An unsupported or failed HDR renderer now reveals the latest ordinary picture
without an error overlay or retry spinner; the saved HDR preference remains on
and the explanation appears beneath that setting. In v206 it stayed in SDR until
an explicit preference change or a new page opening; v220 also permits one retry
per foreground return, without a continuous retry loop. A frozen control-code
result uses its already-prepared exact SDR
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
3. Use **Atvērt jaunāko biļeti un reģistrēt** for the same selection followed by one bounded activation action, or use **Reģistrēt atvērto biļeti** after a fresh slider proof. One action may make an initial 400 ms phone drag and one final retry only after fresh proof that the completed first drag left the exact same ticket unactivated. The phone waits one second after that proof before preparing the second drag.
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

Initial action-count fixes deployed as `ticket-statistics-20260920-v212` on
20 September 2026; v213 above replaces its viewer-driven sampling. Local tests,
module publication, and standard production validation passed. Signed-in live
browser acceptance remains blocked by browser URL policy. See the
[release verification](../../ops/reports/2026-09/2026-09-20-ticket-statistics.md).

`/admin?tab=statistics` shows the last 30 Europe/Riga calendar days of viewing
time and accepted slider-registration/control-code requests. Each action count
is successful/accepted. Both `register_current` and `open_latest_and_register` contribute
registration counts, through the slider or menu. Older totals counted sliders only. Code success means confirmed phone
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
them. No code digits, tickets, or new event log are stored.

The Statistics page refreshes through the administrator-only
`GET /api/v1/admin/statistics` every 15 seconds while visible, with one request
in flight and a ten-second timeout. It preserves the chosen view and expanded
day, marks retained figures stale after a failure, and stops on revoked access.
Viewing samples now use the independent v213 outbox described above. A stalled
delivery times out after ten seconds without reconnecting the viewer; only saved
samples are retried. Physical actions are never replayed.

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

HDR failures keep receiving ordinary pictures, preserving the saved preference. A foreground return or explicit preference change permits another attempt; ordinary frames and transport reconnects do not retry a failed renderer. Recovery retains the last completed HDR surface until its replacement finishes; retained pixels never renew live action authority. Hidden-page, window-focus and cached-page returns share one HDR recovery path. Displayed control-code results retain their original frame only until dismissal or expiry so they can be reprocessed without a new phone request. Reconnects never resubmit phone commands; pending results retain their identity, and abandoned callbacks cannot affect replacement connections. Browser fault coverage lives in the loopback-only recovery test and uses simulated services and decoder output with the real page and canvas presentation.

HDR recovery tests: `web-client/presentation.test.mjs` exercises the real controller with controlled GPU completion; `web-client/presentation-real-gpu.html` exercises ten replacements and exact-result restoration on the real GPU with synthetic pixels. The page exposes only the latest HDR recovery duration and selected color space for inspection. Physical iPhone contrast acceptance remains distinct from these checks.
