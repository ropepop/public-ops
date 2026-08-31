# Current Ticket

This is the first file to read for Ticket work.

Live product: the signed-in page at `ticket.jolkins.id.lv`.
The phone shows ViVi. The page shows that picture and exposes one durable visual action engine for opening, registering, switching between the newest unused and recently activated tickets, re-detecting the newest ticket, and requesting a control code.

## Current jobs, in this order

1. Open the signed-in page and get a live ticket picture quickly.
2. Use **Atvērt jaunāko nereģistrēto biļeti** to visually select and prove the newest current-or-upcoming unused ticket.
3. Use **Atvērt jaunāko biļeti un reģistrēt** for the same selection followed by one bounded held phone drag, or use **Reģistrēt atvērto biļeti** after a fresh slider proof.
4. The browser slider is a visible local authorization control aligned directly over ViVi's visible slider. It submits that same register action once at completion and does not maintain its own phone-control protocol.
5. For 15 minutes after a proven registration, and only after a newer unused ticket is visually proven, use the context-aware button to move directly between the two Aztec-detail views. The ticket list is a transitional phone view, never a successful resting state.
6. Request a control code when needed from the visible button or the invisible top-left start corner, which covers the left 50% of the first 25% of the viewport. The corner only opens the existing numeric request dialog; it does not prewarm the phone path, dismiss a result, or add a stream-wide gesture. The requester page freezes its own live picture of the result. The phone must not send a screenshot of that result.

Registration policy is per authenticated account: at most one admitted registration every 30 seconds and ten admitted registrations in a rolling hour. Control codes remain limited to two requests in a rolling minute. Admins and owners obey these limits by default and may persist an unlimited testing preference to their account; bypassed actions remain audited without consuming quota.

Prove the result on the signed-in page. If a phone picture is needed, take it through the root path, then pull it.

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

## Short rules

- The browser writes immediate Ticket actions directly to authenticated Spacetime reducers; the Pixel subscribes to the durable command rows. The browser never talks to the phone directly.
- Spacetime decides whether and when: it owns account quotas, the admin preference, switch availability/expiry, delayed refresh/re-detection schedules, and the sanitized usage projection. Pixel executes one admitted command and proves the visual result. Phone-local clocks and restart jobs must not create product actions.
- `ticketremote_ticket_action_v3` is the browser authority for visual proof and terminal state. The older interaction row is compatibility state only: a newer V3 visual proof fences stale `needs_attention`, and uncertain V3 registration failures keep the exact command revision without creating replay authority.
- A completed registration stroke that two fresh frames prove left the exact same unactivated detail may create one Spacetime-owned `register_current` child. The child reuses the root admission/history and is never allowed to create another child; unknown or conflicting outcomes still stop without replay.
- Two simultaneous browser mutations use one running plus one private durable waiting slot shared by Ticket actions and control-code requests. Admission and quota are deferred until promotion, when membership, the original proof, and policy are revalidated; a stale waiting intent ends visibly without phone input.
- The browser requests non-mutating `prove_current` after the first fresh frame of a stream epoch, after two agreeing significant picture changes, and when a visible page resumes with a stale proof. Requests are coalesced per backend and epoch, and an unknown result waits for another meaningful picture change. A five-minute normalized slider region is usable only when its action ID, epoch, and frame sequence exactly match the current proof.
- The active Pixel stream is fixed at one complete, independently decodable picture per second. Startup, slider, Ticket-action, and control-code refresh requests are coalesced into one immediate next picture instead of changing frame rate or starting a burst. The source must advertise `all_intra` with `fps`, `sourceFps`, and `keyframeIntervalFrames` all set to 1. The relay fails closed on a missing or contradictory contract, rejects and counts every delta picture, accepts source sequence gaps between independent pictures, and keeps at most one in-flight plus one newest waiting picture per viewer. Browser feedback is diagnostic only and the relay sends no adaptive cadence commands. Rollback order is relay compatibility release first, then Pixel encoder; the strict relay must never receive a legacy source.
- Every active authenticated member may opt into browser-only HDR; that account setting defaults off and follows the account across browsers and devices. The selectable boosts are exactly 2x, 3x, 4x, 5x, and 6x, with 4x used when saved state is missing, retired, or invalid. `client_webgpu_v2` reuses the decoded SDR frame on an isolated main-page WebGPU canvas. Its internal 1x identity path remains available for safe SDR handoff and diagnostics; selectable boosts use one black-anchored gain to brighten every non-black color while preserving hue and channel ratios. Exact black stays zero, near-black remains restrained, and ordinary ticket red, orange, green, blue, gray, and white all expand. The Pixel source remains SDR, so this is browser presentation remapping rather than native HDR capture.
- One foreground coordinator owns cold launch, visibility, pageshow, focus, blur, pagehide, online, and dynamic-range changes. Related return signals join one attempt, but every standalone focus return outside the short event cluster starts a fresh lifecycle even when the page was away too briefly to produce a suspension gap. Visibility, pageshow, and focus also schedule a trailing 500 ms foreground confirmation; an actual animation-frame callback can prove that a still-visible page is painting even when a late iOS blur leaves `document.hasFocus()` stale. Each return reveals authoritative SDR, disposes the old renderer and canvas, checks the asset version and capabilities, and waits for both a newly painted current-epoch SDR frame and at least one second of stable foreground evidence before creating a fresh canvas and WebGPU device. The complete attempt is bounded to 12 seconds; initialization, GPU completion, and compositor settlement retain their 8-second, 1.5-second, and 2-second limits. The wall-clock settlement watchdog, one fresh-canvas retry, and stale-callback fencing remain unchanged.
- HDR presentation ownership follows the visible stream region. Scrolling down far enough to reveal the details area, or showing a control-code result, immediately fences the active attempt, disposes its renderer, device, and canvas, and leaves authoritative SDR visible. No HDR frame offers, retries, or recovery deadline run while that region is blocked. Returning to the stream starts one fresh coordinator attempt from a current-epoch SDR frame; the previous HDR surface is never resumed or reused.
- The fresh canvas is configured for extended tone mapping and unrestricted dynamic range before its first real swapchain copy. Its first visible copy is the exact current SDR image with only one tiny edge request patch capped at an intended 1.25x, so the selected boost cannot appear during uncertain activation. That same canvas stays continuously topmost for two observed animation-frame opportunities. The browser then rechecks freshness, generation, boost, lifecycle, and control-code authority, prepares the full all-color target, and copies it into the same canvas without a visibility or stacking change. Only the matching attempt's `first_presented` after target GPU completion and a post-copy compositor opportunity counts as success. Any revoked authority, timeout, stale frame, backgrounding, GPU or device failure, superseded boost, or control-code priority exposes SDR immediately. The owner-only diagnostic still compares a known HDR image with fixed WebGPU 1x, 2x, and 4x patches. Telemetry records observable version, capability, stream, canvas, renderer, activation, retry, GPU, and compositor facts; it never claims physical panel HDR. `first_presented` proves only that the matching browser presentation sequence reached its observable milestone; it does not prove that iOS granted EDR headroom or that the panel became brighter. Controlled observation on the target iPhone remains the release gate for emitted brightness.
- The local registration drag may start anywhere inside the visible slider. Only that slider listens for a custom drag gesture: on release, rightward travel of at least 8 px and strictly less than 45 degrees from horizontal completes, while movement at or beyond 45 degrees is left to native page scrolling and submits nothing. The stream has no general tap or swipe action. The separate accessible top-left control-code corner is start-only and is disabled whenever the registration slider occupies that region, while the dialog or a result is already visible, or the request is blocked or busy. Keyboard completion remains at 25%; taps, reverse drags, cancellation, second pointers, scrolling, or stale proof submit nothing.
- A healthy visible-page resume keeps its live Spacetime subscription. `prove_current` is read-only, does not own the phone mutation lane or panel-dark action lease, and may be safely superseded by an explicit user Ticket action; it never supersedes another mutating action.
- The signed-in browser client exposes only the V3 Ticket action producer. Legacy reducers and table shape remain temporarily in the module for old in-flight rows and sidecar compatibility; remove that schema only after its production TTL and migration checks pass.
- Control-code cleanup clears the request barrier and publishes the fresh raw-ticket ready watermark atomically after Pixel proves the original Aztec detail, allowing controls to return without a slow fallback cycle.
- Keep the existing signed-in browser session. Do not clear cookies or log out.
- Updating the phone app does not update the public page, and the reverse is also true.
- Do not treat a healthy phone, a successful deploy, or a passing test as enough. The signed-in page has to show the expected ticket state.
- Chrome is the default browser path. If it is unavailable, say so and use the existing signed-in session.
- Old claim-dialog and private-control wording must not return.
- Leave leftover Ticket logging retired. New Ticket diagnostics go to the shared operational log, not local files.
