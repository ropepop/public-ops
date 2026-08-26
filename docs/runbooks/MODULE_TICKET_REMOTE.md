# ticket_remote Module Runbook

Current Ticket product map: [CURRENT.md](../../workloads/ticket-remote/CURRENT.md).
This runbook is for start, stop, health, and deploy. It is not the first product explanation.

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Disaster recovery, rare operator backup: [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md)

## Start / Stop / Restart

```bash
../../tools/arbuzas/deploy.sh deploy \
  --services ticket_phone_bridge,ticket_remote_spacetime_sidecar,ticket_remote,ticket_remote_tunnel \
  --ssh-host kitty-gration \
  --ssh-user ropepop

../../tools/arbuzas/deploy.sh validate \
  --services ticket_phone_bridge,ticket_remote_spacetime_sidecar,ticket_remote,ticket_remote_tunnel \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```

Use an explicit `--release-id` for traceable deploys when cutting a known user-facing change.

## Browser UI Standard

Interactive browser UI must use ArrowJS for changing presence, status, stream, and control areas. Edit browser UI in `workloads/ticket-remote/web-client/`, rebuild with `make web-client-build`, and after deploy verify the authenticated page mounts the Arrow-backed path (`document.documentElement.dataset.ticketUi === "arrow"`) with no new browser console errors.

The authenticated admin page also provides a durable one-time latest-ticket re-detection schedule. Its separate date and time fields use the browser's native selectors, but the submitted wall time is interpreted in `TICKET_REMOTE_PHONE_TIME_ZONE` (`Europe/Riga` in production), not the browser or container time zone. The page calls the authenticated admin Spacetime reducer directly; that transaction creates the durable timer, and the timer later enqueues the same `redetect_latest` action used by the immediate path. It survives browser, `ticket_remote`, sidecar, and phone restarts. A scheduled run is complete only when the action projection is terminal and the Pixel proof reports the matching visual re-detection; command acceptance alone is not completion.

Immediate signed-in Ticket controls use the member `ticket_action_v3` reducer directly. The warm Pixel path is the pending-command subscription; the poller is reconnect/acknowledgement recovery. A healthy visible-page resume preserves the confirmed Spacetime subscription rather than rebuilding it. The browser requests non-mutating `prove_current` on the first fresh frame of each stream epoch, after two agreeing significant picture changes, and on visible resume only when the prior current-view proof is stale. A busy phone defers rather than discards a pending picture-change trigger, and an unknown result is retried only after another meaningful change. An explicit user action may atomically supersede only an active `prove_current`; it never supersedes another mutating action.

Spacetime owns every Ticket business clock and delayed product action. Registration is limited per authenticated account to one admitted action per 30 seconds and ten counted admissions per rolling hour; control codes remain two per rolling minute. Admins/owners obey limits by default and can persist an account-wide unlimited testing preference. The public member projection carries authoritative allow/deny booleans, usage, and next boundary times; browser countdowns are presentation-only. Smart switching is authorized for 15 minutes by a durable Spacetime anchor after activation plus a later unactivated-detail proof. Pixel may reject an already-expired command as a fail-safe, but it must not compute policy windows or schedule a future refresh, re-detection, or switch.

When Pixel proves an unused ticket, it may publish `ticketremote_ticket_slider_region_v3`: a five-minute normalized rectangle bound to the exact proof action ID, stream epoch, and frame sequence. The page maps that rectangle to the displayed canvas after resize and places one native range input over ViVi's visible slider. Pointer-down snapshots that exact proof, stream, geometry, layout, and starting position; pointer-up after at least 25% rightward movement across the overlay submits `register_current` once. Keyboard completion uses the same 25% threshold. Taps and incomplete, duplicate, stale, resized, restarted-stream, lost-capture, blurred, blocked, or busy gestures submit nothing. After reducer acceptance the slider remains latched at completion until that exact action becomes terminal. There is no independent slider claim, lease, progress, or heartbeat command stream. Raw device coordinates, pixels, ticket text, and recognized dates remain phone-private.

V3 registration establishes Accessibility readiness before its final two-frame proof, saves the at-most-once dispatch checkpoint, and sends one uninterrupted 800 ms stroke through the proven geometry. It never hands the gesture between segments. A completed Android gesture that two fresh agreeing frames prove left the exact same unactivated detail may request one deterministic Spacetime `register_current` child; that child shares the root admission/history and cannot create another child. An unknown or conflicting post-gesture picture ends as `ticket_action_post_gesture_visual_unproved` and must never be replayed.

Spacetime retains at most one private waiting intent behind the active phone mutation, shared by V3 Ticket actions and control-code requests. Waiting performs no activation or control-code admission. Promotion re-runs membership, policy, quota, proof, expiry, and lane gates before publishing a pending phone command; rejection becomes a visible terminal result, so a stale second-window request neither hangs nor reaches phone input. Control-code digits remain only in the private short-lived intent until promotion.

## Health Checks

```bash
curl -fsS http://127.0.0.1:9338/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl https://ticket.jolkins.id.lv/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl -I https://ticket.jolkins.id.lv/ | rg -i 'cache-control|cdn-cache-control|cf-cache-status|clear-site-data'
```

Production normally uses SpacetimeAuth on the page. Cloudflare Access remains a supported fallback mode; when it is selected, a plain request may redirect to Access login. Use the authenticated browser for user-facing checks and local container health for origin checks.

To confirm the newest page is live, compare the page's embedded version with `/api/v1/health` `serverVersion`, then check that response headers are no-store/dynamic instead of a stale cached response.

The sidecar health response also reports the configured central logging host/database and the last central write attempt, success, and error. That section is informational: check it explicitly when validating logging instead of relying only on the top-level Ticket database status.

```bash
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T ticket_remote_spacetime_sidecar curl -fsS http://127.0.0.1:9346/healthz' | jq '.operationalLogging'
```

For phone-stream failures, validate the private phone path before debugging the public page:

```bash
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T ticket_phone_bridge /usr/local/bin/ticket-phone-bridge-health'
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T ticket_remote sh -lc "curl -fsS http://ticket_phone_bridge:9388/api/v1/health"'
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml logs --since 10m ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote'
```

`ticket_phone_bridge` has its own healthcheck and watchdog. It verifies the Pixel is connected over ADB, the exact ADB forward exists, and the forwarded Pixel health endpoint answers. If that check fails while `socat` is still listening, the bridge loop stops the listener, removes the stale ADB forward, reconnects to the Pixel, and starts a fresh listener. Deploy validation checks this bridge directly and also proves that `ticket_remote` can reach the Pixel health endpoint through the private Compose network.

## Operational logging

New Ticket diagnostics share one destination. The browser sends bounded `client_log` messages over its authenticated video WebSocket, `ticket_remote` validates and sanitizes them, `Store.AppendSafeOperationalLog` calls the private sidecar route, and the sidecar invokes `operationallog_append_ticket_event` in `operational-logging-prod`. Server, relay, and audit events use that Store/sidecar path. Pixel ticket diagnostics write the same central reducer directly from the verified Pixel runtime; they do not pass through the Ticket Store or sidecar.

Browser event names come from a fixed 66-name list, per-socket and global admission are capped at 60 messages per minute, and every informational browser event is sampled into a shared minute bucket. Details remain capped at 1 KiB, private keys and private-looking values are removed, and central Ticket rows expire after six hours. Central cleanup removes up to 1,000 expired rows every five minutes, safely above bounded browser ingestion. Browser delivery is intentionally best-effort: the queue waits for an open authenticated video socket, then releases an event after WebSocket send acceptance rather than a database acknowledgement. The server ignores browser-supplied correlation and hashes the authenticated session instead. Audit writes use a separate short asynchronous deadline, so a logging delay cannot hold up a successful Ticket state change.

Run the combined product-state and central-log trace locally with:

```bash
cd workloads/ticket-remote
./scripts/trace-spacetime.sh
```

Maincloud does not support the previous SQL ordering clause. The script keeps
the time-bounded result in memory, sorts timestamp-first rows locally, and
prints only `TRACE_LIMIT` rows without creating a temporary file.

The Ticket database's `ticketremote_safe_operational_log` table remains only so old rows can drain and its six-hour cleanup can remove them. Its append reducers explicitly reject old writers and must not be used as a logging fallback.

For a production cutover, publish and verify the central logging module and enroll the sidecar and Pixel identities first. Deploy and verify the central-writing sidecar and rebuilt browser next. Then deploy the central-writer Pixel APK and prove a fresh Pixel Ticket row reached `operational-logging-prod`. Only after that Pixel proof may the Ticket module be published with rejecting legacy reducers. Wait more than six hours before considering removal of the legacy table or cleanup. Publishing the rejecting Ticket module before the Pixel central-writer APK is live creates a logging gap for the old Pixel worker.

## Cloudflare Access fallback

Configure a self-hosted Access app for `ticket.jolkins.id.lv`.

- Login method: One-Time PIN / email.
- Policy/session duration: `1 month`.
- Bootstrap admin/member email: `ticket@jolkins.id.lv`.
- Service validates `Cf-Access-Jwt-Assertion`; set the app audience tag in `TICKET_REMOTE_CF_ACCESS_AUDIENCE`.
- SpacetimeDB controls linked ticket membership after Cloudflare confirms identity.
- After that membership check, the isolated Rust sidecar signs a five-minute member-proxy token with its real issuer and audience. The Spacetime module requires the dedicated proxy role, exact subject/email binding, verified email, and current membership; it does not grant the service role.

## Pixel Backend

The phone backend is private to Ops through `ticket_phone_bridge`, which is the only private kitty-gration service `ticket_remote` should use for phone media. SpacetimeDB desired-state and command rows own stream and control intent; there is no intermediate session broker.
`ticket_phone_bridge` connects to the Pixel over ADB on Tailscale, forwards the Pixel's local ticket stream port inside Docker, and exposes it only inside the private Docker network.
The bridge uses the ADB key files in `/etc/arbuzas/secrets/android-adb/`, mounted read-only into the bridge container. Keep those files scoped to the bridge; they are what let Ops reach the already-authorized Pixel without asking Android to approve a new container identity.

The browser never receives the phone URL and never talks directly to the Pixel. It uses `ticket_remote` for authenticated H.264 media and uses direct member-only Spacetime state/reducers for control intent; `ticket_remote` talks privately to `ticket_phone_bridge`. Do not add public media ports, a separate public media service, or a second public tunnel unless there is a fresh decision to redesign the deployment.

For ViVi control-code requests, Pixel proves that the generated screen exists, then the requester browser captures a stable stream-resolution result from its rendered live stream. Browser capture is complete only after the frozen image decodes, is visibly inside the viewport for two paint frames, and emits `control_code_frame_painted`; `control_code_frame_displayed` remains a compatibility event with the same post-paint meaning. Only then may the browser acknowledge capture and allow Pixel cleanup. `ticket_remote` must not accept or expose Pixel screenshot payloads (`phone_root_image`, `imageMime`, or `imageBase64`) for ViVi control-code results. The detailed contract lives in the Pixel ticket streaming architecture doc.

After the result strip closes and the original Aztec detail is freshly proved, Pixel clears the request cleanup barrier and publishes the `fast_ready` watermark through one atomic service reducer. Browser controls therefore cannot observe cleanup as complete while the phone lane still projects blocked.

The exact result payload and browser-frozen image are requester-private, but the live H.264 stream is shared among authorized linked members. Other linked viewers may therefore see the phone's control-code UI while a request runs. SpacetimeDB public tables must remain sanitized operational/activity projections only; exact digits, email addresses, exact result values, and result images belong only in private records or requester-only delivery paths.

Pixel stream compute tuning must preserve the current rooted hardware H.264 profile: 720 px target width, about 1.2 Mbps, 1 FPS steady viewing, a three-frame 10 FPS cold-start burst, and the bounded 10 FPS ViVi control-code request window from Pixel command receipt through requester-browser freeze acknowledgement. Cleanup uses that same bounded visual-proof cadence. These modes pace one root surface-capture helper and one MediaCodec encoder; they must not restart the encoder for a request or leave a helper, encoder, wrapper, or request burst active after stop.

Normal ViVi control-code entry is keyboard-free and root-only. Pixel keeps the configured Android input method enabled and unchanged, acquires a request-scoped Accessibility soft-keyboard suppression lease, focuses the visually proved code field, and sends one rooted Android InputManager key-event batch that moves to the end, clears any old value, and enters the validated digits. It creates no temporary input device. Two fresh rooted visual samples must then prove the entered value and derive the enabled Submit target before a separate rooted tap is allowed. Focus is cleared and the original software-keyboard show mode is visibly restored before the request can finish cleanly; startup and service-replacement recovery fail closed on an unresolved ownership marker. If the popup remains open, Pixel may repeat the full clear, re-entry, and Submit transaction once only after fresh visual evidence proves that state; it never sends a blind second tap. Accessibility does not classify Ticket content, enter digits, navigate, or submit.

`/api/v1/health.directStream` is the first place to check stream delivery: it records active browser video clients, phone relay state, last config, last frame, last keyframe, reconnect count, and recent browser decoder telemetry.

If the phone leaves ViVi or Android system controls appear, the Pixel backend stops the ticket session; ticket-remote releases controle-code mode and returns viewers to general state.

## Public Page Expectations

The user-facing page is stream-first. On mobile fresh load, reload, reconnect, resize, and page restore, the first viewport should show only the stream. Status, control, and membership options live below the stream and become visible only after scrolling down.

Controle-code controls belong on the web page. The Pixel still enforces touch safety and ticket-page constraints, but it should not show a separate user-facing start screen for the public stream experience.

## Availability Assumption

The production path currently has one kitty-gration host and one physical Pixel. Recovery is procedural rather than automatic; use [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md) for the current limits and standby acceptance checks.

## Evidence Paths

New Ticket proof, if a task explicitly needs local artifacts, goes under `ops/evidence/ticket-remote/<timestamp-or-topic>/` with a short index.
Leftover Ticket pictures and old proof packs now live in `archive/ticket/`. Do not start Ticket work there.
