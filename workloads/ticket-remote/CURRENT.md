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

Prove the result on the signed-in page. If a phone picture is needed, take it through the root path, then pull it.

The HDR settings show one fixed explanation alongside the existing switch and brightness selector. The redundant control-code and viewer summary cards are removed; the limits table and live viewer list with its count remain. Freshness checks still revoke action proof without hiding an established HDR picture between updates.

## Where to work

- Page: `web-client/ticket-app-source.js` and `internal/web/static/index.html.tmpl`. Rebuild the page bundle after page edits.
- Phone Ticket: `pixel-phone` Ticket files under the Android orchestrator. Do not split that service unless asked.
- Durable state: `spacetimedb/src/lib.rs`.
- Operator start/stop/health: `../../docs/runbooks/MODULE_TICKET_REMOTE.md`.
- Deep stream/capture note: `pixel-phone/docs/architecture/TICKET_STREAMING_ARCHITECTURE.md`. Open it only when the stream path itself is the task.

Generated copies such as `internal/web/static/app.js` and `internal/web/static/spacetime-client.js` are build output. Edit the source, then rebuild.

## Do not start here

Leftover Ticket history now lives in:

- `../../archive/ticket/`
- `../../docs/archive/ticket/`
- `pixel-phone/archive/ticket/`

Those folders are backups. Open them only when a task explicitly asks for old Ticket history.

## Current control and presentation contract

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
- Verify the signed-in page, durable settlement, phone result, and final quiet state separately. Performance acceptance requires the actual iPhone Home Screen app; desktop checks and earlier releases do not establish the requested new cold/warm timing or physical brightness.
- Preserve the signed-in browser session, close only task-owned tabs, and keep private ticket content and credentials out of diagnostics. New bounded runtime events belong only in shared operational logging.

- Live transition checks found and fixed a leftover two-second continuity/spinner ceiling: browser and relay now share the existing three-second frame boundary (3000 ms accepted, 3001 ms rejected). Phone v369 renews its database clock independently of fresh readiness publication, preserving the three-second control expiry.

- Transition acceptance on 6 September: Pixel v372 overlaps ordinary classification with encoding on the same retained picture; browser v177 dismisses an exact HDR code result by seeding the existing renderer with the fresh live picture. The affected five-pass streaks, measured snags, and desktop-only limitations are recorded in [the transition report](../../ops/reports/2026-09/2026-09-06-ticket-slider-transition-streaks.md).

- Browser v178 abandons any in-flight readiness-clock refresh when its database connection ends. A replacement connection must acquire its own clock; late old responses cannot restore authority or clear the new refresh. This prevents a lost reducer reply from leaving registration and code controls disabled after reconnect. The ready slider uses a 5% base and has a passive white sweep over a three-second cycle (v180), with half the previous white-wave opacity; reduced-motion mode disables the sweep. It does not animate during registration or alter input geometry.
