# ticket_remote Module Runbook

Current Ticket product map: [CURRENT.md](../../workloads/ticket-remote/CURRENT.md).
This runbook is for start, stop, health, and deploy. It is not the first product explanation.

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Disaster recovery, rare operator backup: [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md)

## Start / Stop / Restart

```bash
../../tools/arbuzas/deploy.sh deploy \
  --services ticket_remote \
  --ssh-host kitty-gration \
  --ssh-user ropepop

../../tools/arbuzas/deploy.sh validate \
  --services ticket_remote \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```

The `ticket_remote` selector expands to the bridge, Spacetime sidecar, web
service, and tunnel. Browser WebGPU is the only HDR renderer; no server HDR
transformer is deployed. Use an explicit `--release-id` for
traceable deploys when cutting a known user-facing change.

## Browser UI Standard

Interactive browser UI must use ArrowJS for changing presence, status, stream, and control areas. Edit browser UI in `workloads/ticket-remote/web-client/`, rebuild with `make web-client-build`, and after deploy verify the authenticated page mounts the Arrow-backed path (`document.documentElement.dataset.ticketUi === "arrow"`) with no new browser console errors.

The authenticated admin page also provides a durable one-time latest-ticket re-detection schedule. Its separate date and time fields use the browser's native selectors, but the submitted wall time is interpreted in `TICKET_REMOTE_PHONE_TIME_ZONE` (`Europe/Riga` in production), not the browser or container time zone. The page calls the authenticated admin Spacetime reducer directly; that transaction creates the durable timer, and the timer later enqueues the same `redetect_latest` action used by the immediate path. It survives browser, `ticket_remote`, sidecar, and phone restarts. A scheduled run is complete only when the action projection is terminal and the Pixel proof reports the matching visual re-detection; command acceptance alone is not completion.

Immediate signed-in Ticket controls use the member `ticket_action_v3` reducer directly. The warm Pixel path is the pending-command subscription; the poller is reconnect/acknowledgement recovery. A healthy visible-page resume preserves the confirmed Spacetime subscription rather than rebuilding it. The browser requests non-mutating `prove_current` on the first fresh frame of each stream epoch, after two agreeing significant picture changes, and on visible resume only when the prior current-view proof is stale. A busy phone defers rather than discards a pending picture-change trigger, and an unknown result is retried only after another meaningful change. An explicit user action may atomically supersede only an active `prove_current`; it never supersedes another mutating action.

Spacetime owns every Ticket business clock and delayed product action. Registration is limited per authenticated account to one admitted action per 30 seconds and ten counted admissions per rolling hour; control codes remain two per rolling minute. Admins/owners obey limits by default and can persist an account-wide unlimited testing preference. The public member projection carries authoritative allow/deny booleans, usage, and next boundary times; browser countdowns are presentation-only. Smart switching is authorized for 15 minutes by a durable Spacetime anchor after activation plus a later unactivated-detail proof. Pixel may reject an already-expired command as a fail-safe, but it must not compute policy windows or schedule a future refresh, re-detection, or switch.

When Pixel proves an unused ticket, it may publish `ticketremote_ticket_slider_region_v3`: a five-minute normalized rectangle bound to the exact proof action ID, stream epoch, and frame sequence. The page maps that rectangle to the displayed canvas after resize and places one native keyboard-focusable range input over ViVi's visible slider. Pointer-down may begin anywhere inside that input and snapshots the exact proof, stream, geometry, layout, and starting position. Only the slider owns custom drag handling: on release, rightward travel of at least 8 px and strictly less than 45 degrees from horizontal submits `register_current` once, while native vertical page scrolling cancels the local pointer session and can never submit. The stream itself has no general tap, swipe, fullscreen, or wake-lock action. Keyboard completion keeps the existing 25% threshold. Taps, reverse drags, second pointers, scrolling, and incomplete, duplicate, stale, resized, restarted-stream, blurred, blocked, or busy gestures submit nothing. After reducer acceptance the slider remains latched at completion until that exact action becomes terminal. There is no independent slider claim, lease, progress, or heartbeat command stream, and a rejected gesture never reaches Spacetime or Pixel. Raw device coordinates, pixels, ticket text, and recognized dates remain phone-private.

The authenticated page also exposes an accessible invisible control-code start button over the left 50% of the top 25% of the viewport. It opens the same numeric dialog as the visible control-code button and performs no phone prewarming or background preparation. It is start-only: it does nothing while the dialog or a result is already visible, while control-code admission is blocked or busy, or while the registration slider occupies that region. Result dismissal remains on the visible close control. This bounded button does not restore a stream-wide gesture, fullscreen, wake-lock, or top-right hotspot.

Every active authenticated Ticket member may opt into browser-rendered HDR. The private account preference defaults off and is restored through the existing direct Spacetime session on any browser or device. Every member can persist exactly 2x, 3x, 4x, 5x, or 6x; missing, retired, or invalid saved state resolves to 4x. Browser v2 reuses each normal decoded frame on an isolated opaque main-page WebGPU canvas; there is no server HDR socket, JPEG transformer, or second media stream. The renderer decodes sRGB to linear light. Its internal 1x mapping is an exact SDR identity for safe handoff and diagnostics; selected boosts apply a black-anchored expansion to every non-black color, driven by the brightest RGB channel. One equal RGB scale preserves hue and channel ratios; exact black stays zero, near-black remains restrained, and ordinary ticket red, orange, green, blue, gray, and white can all expand. The retired owner engine preference remains only as a private compatibility tombstone; ordinary members use the fixed `client_webgpu_v2` capability without an engine selector. Each genuine start or lifecycle recovery creates a fresh canvas, applies unrestricted dynamic range, and configures extended tone mapping before any real swapchain frame. Its first visible copy is an SDR-identity activation image of the exact current frame with only a tiny edge request patch capped at an intended 1.25x, so a dark frame can still request extended range without exposing the selected boost during settlement. That same canvas remains continuously topmost for exactly two animation-frame compositor opportunities, then the browser rechecks lifecycle, freshness, generation, boost, and control-code authority, prepares the full all-color target, and copies it into the same canvas without a visibility or stacking transition. Success is recorded only after the target copy completes on the GPU and receives its own post-copy compositor opportunity. Any late revocation returns immediately to authoritative SDR; the canvas is never hidden, demoted, replaced, or returned to standard range between activation and target. Unsupported capability, backgrounding, hard staleness, invalid output, superseded settings, renderer failure, and GPU device loss expose SDR immediately. The phone capture, encoder, bridge, relay input, and media retention boundary remain unchanged, and no image bytes are stored in SpacetimeDB. Browser `first_presented`, configuration, GPU completion, observed compositor opportunities, and request-patch telemetry describe the requested browser path, not confirmed EDR headroom or physical panel brightness; controlled observation on the target iPhone remains the release gate.

Foreground HDR reacquisition has one browser owner. Cold launch, visibility, pageshow, focus, blur, pagehide, online, and dynamic-range changes feed an attempt-based coordinator. Related positive return events join one attempt, but every standalone focus return outside their short cluster starts a fresh lifecycle even when it was too brief to produce a suspension gap. Visibility, pageshow, and focus also schedule a trailing 500 ms confirmation; an actual animation-frame callback can prove that a still-visible page is painting even when a late iOS blur leaves `document.hasFocus()` stale. A real or inferred return invalidates older promises, exposes authoritative SDR, disposes the previous canvas and WebGPU device, checks `/api/v1/livez` without caching, reloads an old asset at most once, refreshes server and local capability, and waits for both a newly painted current-epoch SDR frame and one second of stable foreground evidence before creating the replacement HDR surface. The replacement attempt has a fresh 12-second deadline. Initialization, GPU completion, and compositor settlement are separately bounded to 8 seconds, 1.5 seconds, and 2 seconds. Settlement expiry is checked by its own timer, each new SDR frame, and lifecycle pulses. Success is recorded only for the matching attempt's `first_presented` after the activation surface's two opportunities and the full target's post-copy opportunity. One fresh-canvas retry is allowed; any later failure stays safely in SDR until a new lifecycle, online, or capability transition. Telemetry describes observable attempts, versions, capabilities, stream watermarks, canvas and renderer generations, activation, retry, GPU completion, and compositor opportunities, never physical display brightness.

The HDR canvas is owned only while the stream stage is the active presentation region. Revealing the below-stream details area or a control-code result tears down the HDR surface and suspends recovery without spending its retry or deadline budget. Returning to the stream starts a new attempt with a fresh device and canvas after a new SDR frame; an offscreen or previously active surface cannot regain authority.

V3 registration establishes Accessibility readiness before its final two-frame proof, saves the at-most-once dispatch checkpoint, and sends one uninterrupted 800 ms stroke through the proven geometry. It never hands the gesture between segments. A completed Android gesture that two fresh agreeing frames prove left the exact same unactivated detail may request one deterministic Spacetime `register_current` child; that child shares the root admission/history and cannot create another child. An unknown or conflicting post-gesture picture ends as `ticket_action_post_gesture_visual_unproved` and must never be replayed.

Spacetime retains at most one private waiting intent behind the active phone mutation, shared by V3 Ticket actions and control-code requests. Waiting performs no activation or control-code admission. Promotion re-runs membership, policy, quota, proof, expiry, and lane gates before publishing a pending phone command; rejection becomes a visible terminal result, so a stale second-window request neither hangs nor reaches phone input. Control-code digits remain only in the private short-lived intent until promotion.

## Health Checks

```bash
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T ticket_remote curl -fsS http://127.0.0.1:9338/api/v1/livez' | jq '.serverVersion, .assetVersion, .ok'
cloudflared access curl https://ticket.jolkins.id.lv/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl -I https://ticket.jolkins.id.lv/ | rg -i 'cache-control|cdn-cache-control|cf-cache-status|clear-site-data'
```

Production normally uses SpacetimeAuth on the page. `/api/v1/livez` is the unauthenticated origin identity check; detailed `/api/v1/health` data requires an authenticated active Ticket member. Cloudflare Access remains a supported fallback mode; when it is selected, a plain request may redirect to Access login. Use the authenticated browser for user-facing and detailed-health checks and local container health for origin checks.

To confirm the newest page is live, compare the page's embedded version with `/api/v1/livez` `assetVersion` and `serverVersion`, then check that response headers are no-store/dynamic instead of a stale cached response.

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

Browser event names come from a fixed 79-name list, per-socket and global admission are capped at 60 messages per minute, and every informational browser event is sampled into a shared minute bucket. Details remain capped at 1 KiB, private keys and private-looking values are removed, and central Ticket rows expire after six hours. Central cleanup removes up to 1,000 expired rows every five minutes, safely above bounded browser ingestion. Browser delivery is intentionally best-effort: the queue waits for an open authenticated video socket, then releases an event after WebSocket send acceptance rather than a database acknowledgement. The server ignores browser-supplied correlation and hashes the authenticated session instead. Audit writes use a separate short asynchronous deadline, so a logging delay cannot hold up a successful Ticket state change.

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

Pixel stream compute tuning must preserve the current rooted hardware H.264 profile: 994 px target width, 2046 px visible height, about 8 Mbps, and one complete, independently decodable picture per second. H.264 pads that frame to 1008x2048 so the coded surface stays within the established decoder and allocation ceilings. This single source profile feeds both SDR and browser HDR. Startup, slider, Ticket-action, control-code, and cleanup requests coalesce into one immediate next picture rather than changing cadence or starting a burst. The source must advertise `frameDependencyMode=all_intra` with `fps=1`, `sourceFps=1`, and `keyframeIntervalFrames=1`. The relay rejects a missing or contradictory contract and every delta picture, accepts source sequence gaps between independent pictures, and retains at most one in-flight plus one newest waiting picture for each viewer. These requests use one root surface-capture helper and one MediaCodec encoder; they must not restart the encoder or leave a helper, encoder, or wrapper active after stop. Rollback must restore the relay compatibility release before rolling the Pixel encoder back; never expose the strict relay to a legacy source.

Normal ViVi control-code entry is keyboard-free and root-only. Pixel keeps the configured Android input method enabled and unchanged, acquires a request-scoped Accessibility soft-keyboard suppression lease, focuses the visually proved code field, and sends one rooted Android InputManager key-event batch that moves to the end, clears any old value, and enters the validated digits. It creates no temporary input device. Two fresh rooted visual samples must then prove the entered value and derive the enabled Submit target before a separate rooted tap is allowed. Focus is cleared and the original software-keyboard show mode is visibly restored before the request can finish cleanly; startup and service-replacement recovery fail closed on an unresolved ownership marker. If the popup remains open, Pixel may repeat the full clear, re-entry, and Submit transaction once only after fresh visual evidence proves that state; it never sends a blind second tap. Accessibility does not classify Ticket content, enter digits, navigate, or submit.

`/api/v1/health.directStream` is the first place to check stream delivery: it records active browser video clients, phone relay state, the raw advertised frame-dependency and 1 FPS tuple, whether that all-intra contract is valid, its mismatch count, last config, last frame, last keyframe, rejected all-intra deltas, reconnect count, and recent browser decoder telemetry. An advertised all-intra tuple other than 1/1/1 is rejected before it reaches viewers and reports `invalid_source_config` until the source sends a valid config.

If the phone leaves ViVi or Android system controls appear, the Pixel backend stops the ticket session; ticket-remote releases controle-code mode and returns viewers to general state.

## Public Page Expectations

The user-facing page is stream-first. On mobile fresh load, reload, reconnect, resize, and page restore, the first viewport should show only the stream. Status, control, and membership options live below the stream and become visible only after scrolling down.

Controle-code controls belong on the web page. The Pixel still enforces touch safety and ticket-page constraints, but it should not show a separate user-facing start screen for the public stream experience.

## Availability Assumption

The production path currently has one kitty-gration host and one physical Pixel. Recovery is procedural rather than automatic; use [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md) for the current limits and standby acceptance checks.

## Evidence Paths

New Ticket proof, if a task explicitly needs local artifacts, goes under `ops/evidence/ticket-remote/<timestamp-or-topic>/` with a short index.
Leftover Ticket pictures and old proof packs now live in `archive/ticket/`. Do not start Ticket work there.
