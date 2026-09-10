# Current Ticket

This is the first file to read for Ticket work.

Live product: the signed-in page at `ticket.jolkins.id.lv`.
The phone shows ViVi. The page shows that picture and exposes one durable visual action engine for opening, registering, switching between the newest unused and recently activated tickets, re-detecting the newest ticket, and requesting a control code.

## Current jobs, in this order

1. Open the signed-in page and get a live ticket picture quickly.
2. Use **Atvērt jaunāko nereģistrēto biļeti** to visually select and prove the newest current-or-upcoming unused ticket.
3. Use **Atvērt jaunāko biļeti un reģistrēt** for the same selection followed by one bounded activation action, or use **Reģistrēt atvērto biļeti** after a fresh slider proof. One action may make an initial 800 ms phone drag and one final retry only after fresh proof that the completed first drag left the exact same ticket unactivated.
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
- One clearly identified unactivated-detail observation supplies the normalized slider region. The phone still requires two fresh agreeing observations, exact private detail identity, foreground/input readiness, unchanged touch and capture generation, and display protection immediately before registration. Browser slider completion authorizes only that exact context.
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

Browser v187 keeps one reconnect attempt in flight, retries failed attempts after one second without a limit, and abandons an attempt after ten seconds without current database state/clock and a fresh presented picture. The existing spinner covers interruptions; the optional reconnect error appears only after thirty seconds of continuous visible downtime. Hidden time does not count, returning or going online expedites recovery, and visible viewing has no inactivity cutoff. Success clears the outage immediately. Only a changed product version automatically reloads the page. Expired authentication offers sign-in after the grace period while session checks continue. Owner cold completion resumes connections in place after the existing proof barrier; it does not renew the page-opening warm lease.

HDR failures keep the ordinary picture working while HDR retries, preserving the saved preference. Reconnects never resubmit phone commands; pending results retain their identity, and abandoned callbacks cannot affect replacement connections. Browser fault coverage lives in the loopback-only recovery test and uses simulated services and decoder output with the real page and canvas presentation.
