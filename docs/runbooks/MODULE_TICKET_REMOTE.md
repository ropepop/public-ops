# ticket_remote Module Runbook

Current Ticket product map: [CURRENT.md](../../workloads/ticket-remote/CURRENT.md).
This runbook is for start, stop, health, and deploy. It is not the first product explanation.

For design and maintenance choices, follow the root `AGENTS.md` section **Fast, Simple, Reliable Changes**: one owner per state/effect, atomic local transitions, durable idempotent handoffs, bounded physical attempts, canonical paths, and evidence from every layer.

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Disaster recovery, rare operator backup: [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md)

## September 6 independent-control cutover

Current command/readiness authority is specified in `workloads/ticket-remote/CURRENT.md`. Publish the additive module with `spacetime publish ticket-remote-prod-v3 --server maincloud --module-path ./spacetimedb --delete-data=never --yes` from the workload directory. Deploy the compatible Pixel v367 before the new browser/server v174. Observe its current phone-control row and the signed-in page before retiring old browser admissions. Preserve retained data and terminal reconciliation until old pending work drains. Earlier media-watermark and automatic `prove_current` descriptions below document the pre-cutover protocol, not current browser command authority.

## V2 coordinated maintenance

Before the V2 cutover, publish the additive maintenance gate against the deployed V1 source with `--delete-data=never`. The owner-controlled `ticketremote_owner_set_maintenance(ticketId, true)` blocks new admissions and defers scheduled work while allowing admitted results and cleanup to settle. Prove all queues and cleanup are drained before publishing V2. Keep the same database; publish V2, deploy the compatible Pixel `ticket_screen`, then deploy the complete `ticket_remote` selector. Verify all release identities before setting the maintenance flag to false. Never publish the local fixture module.

Rollback must preserve recorded outcomes: pause admissions and reconcile/drain work first; do not restore a database snapshot or replay an uncertain physical action. Scheduled work retains its original due time while paused.

## Start / Stop / Restart

Owners can use **Sleep / cold mode** on `/admin?tab=tickets` for a real cold-opening test, including while viewers are connected. It cancels the 30-minute warm timer, clears capture demand and relay pictures, proves Pixel capture/lease release, then reconnects the viewer pages in place. Ordinary page openings retain the normal warm hold afterward. The admin page stays open and shows stopping, cold confirmation, reconnecting, and live/asleep or failure. Existing actions or cleanup reject the request immediately. An unproved stop remains paused; investigate it before explicitly retrying.

Cold restart progress uses the additive `coldRestart*` fields on `ticketremote_stream_desired_state`. The sidecar forwards that existing database subscription over one authenticated local event stream. The relay reconciles a retained operation after restart and never releases the barrier from `desiredActive=false` alone. Keep the same production database and use `--delete-data=never` for this additive update. Install the compatible Pixel and relay/browser consumers before exposing the button.

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

For the non-destructive ViVi account switch, preserve the compatibility order. First deploy a Pixel that continues to accept versions 1, 2, and 3 and also understands version 4, while retaining every earlier version's semantics. Next publish the module that admits version 4 on the existing `ticketremote_owner_request_vivi_reauth_logout_login` reducer. Last, deploy the rebuilt admin page and generated browser bindings that can produce version 4. With the session-only fallback off, version 3 remains exact: a `vivi-logout-login-` request ID and only `logoutInApp: true` in the private command. With it on, version 4 requires the distinct `vivi-logout-redetect-login-` namespace and adds exactly `redetectAfterLogin: true`; it restores the original ticket first and searches for the newest unused ticket only if the original is unavailable. Version 2 full reset stays separate and unchanged with only `resetAppData: true`, and version 1 keeps its prior compatibility semantics. To roll back, remove or disable the version 4 web producer first, wait until no matching queued, pending, or running version 4 attempt remains, then remove module admission if needed and downgrade the version 4-capable Pixel last. Never reinterpret versions 1 through 3, automatically fall back to full reset, or downgrade Pixel while a version 4 command can still run.

## Browser UI Standard

Interactive browser UI must use ArrowJS for changing presence, status, stream, and control areas. Edit browser UI in `workloads/ticket-remote/web-client/`, rebuild with `make web-client-build`, and after deploy verify the authenticated page mounts the Arrow-backed path (`document.documentElement.dataset.ticketUi === "arrow"`) with no new browser console errors.

The Tickets section at `/admin?tab=tickets` provides a durable one-time latest-ticket re-detection schedule. Owner ViVi credentials and account switching are under `/admin?tab=account`; membership and device/limit settings have their own Members and Settings sections. Its separate date and time fields use the browser's native selectors, but the submitted wall time is interpreted in `TICKET_REMOTE_PHONE_TIME_ZONE` (`Europe/Riga` in production), not the browser or container time zone. The page calls the authenticated admin Spacetime reducer directly; that transaction creates the durable timer, and the timer later enqueues the same `redetect_latest` action used by the immediate path. It survives browser, `ticket_remote`, sidecar, and phone restarts. A scheduled run is complete only when the action projection is terminal and the Pixel proof reports the matching visual re-detection; command acceptance alone is not completion.

Immediate signed-in controls use the version-2 common member command envelope. The database owns admission, policy clocks, quotas, and one waiting intent; Pixel owns the physical action and retains its terminal result until the atomic finalizer accepts it. The current `phone_control_state` observation independently authorizes controls. The page does not create automatic `prove_current` actions or use old stream watermarks as command authority.

The current browser owns one media socket, one decoder, one newest waiting picture, and one presentation path. Conservative picture freshness expires after 3,000 ms. The spinner stays at half opacity in SDR and HDR. The error panel instead waits for 30 visible seconds without a new valid frame receipt, independent of decoding and connection health; hidden and cold-paused time is excluded. Commands and media reconnect independently. New commands use current phone readiness and database authority without requiring a fresh displayed picture; existing exact-result capture and cleanup remain mandatory. A returning page may reuse an admitted idle encoder, but an expired picture is never replayed as fresh. See **Sleep / cold mode** above for the explicitly acknowledged cold boundary and real warm-timer cancellation.

The registration overlay follows the current phone-control slider region and snapshots its observation, layout and gesture at pointer-down. A rightward completion of at least 8 px with less vertical travel submits one common registration command. Vertical scrolling, taps, reverse or cancelled gestures, changed proof, and a busy phone do not submit. Keyboard activation uses the same bounded command. The stream has no general touch-forwarding or fullscreen/wake-lock gesture. Pixel's existing action owner may perform the separately bounded final stroke only after proving the first completed stroke left the same ticket unactivated; uncertain effects are never replayed.

After a valid local registration trigger, the picture painter covers that slider region with its sampled ticket-card background for 3,000 ms. This cover hides activation motion while the phone and stream continue normally; it does not assert an activated result. The fixed deadline survives incoming state and picture updates, early completion and backgrounding. Expiry reveals the current stream even if the action is still running. The cover has no input or wave, needs no fonts, shares the normal HDR path and never changes source freshness or exact control-code results.

The visible control-code button and the invisible upper-left hotspot open the same local numeric dialog. They do not start extra phone preparation. HDR remains active through normal dialog and execution frames. Only the requester freezes the exact generated frame matching its durable result marker, after the corresponding presentation completes; the local SDR copy remains the fallback. Closing or expiring a result releases that presentation and does not replay the request. Do not capture or retain control-code pixels as test evidence.

HDR uses the normal decoded frames and one main-page WebGPU renderer. Each member's preference and 2x–6x boost remain account-scoped, with 4x as the invalid/missing-value default. The browser slider and wave are composed into the picture before that same HDR treatment. The original slider's rounded corners and one refined probe sample are replaced with a strip of the surrounding card sampled from the same frame already drawn on the canvas. Do not crop directly from a `VideoFrame`: WebKit ignores that source rectangle. There is no separate CSS backing or wave canvas. Only a transparent accessible input button sits above the picture. Font failure retains native pixels and usable input. There is no second HDR stream or engine selector. Browser visibility, GPU loss and recovery remain local presentation concerns; displayed brightness must be checked on the real browser/display, separately from numeric settings or health.

Presentation retains the raw newest phone frame separately from its reusable composition canvas. Local drag/wave paints share the serialized graphics queue, never replace a pending source picture, and retain the original source age and feedback. An already activated matching HDR surface can repaint locally without repeating its activation or compositor-proof sequence. Changed control context fences old decoration; hidden, expired or frozen pictures stop animation. Where an older surface is retained during recovery, it remains intact until a valid replacement is ready. Exact control-code results bypass composition and require completed raw-frame proof even if a decorated picture had the same source sequence. A failed canvas-to-video conversion enters the normal silent SDR fallback and settings error without restarting media.

Keep policy values, current actions and ownership rules in [CURRENT.md](../../workloads/ticket-remote/CURRENT.md). The deep Pixel capture note is `pixel-phone/docs/architecture/TICKET_STREAMING_ARCHITECTURE.md`. Retained legacy database shapes and explicit reload rejections exist only for safe settlement/old-client rejection; their removal condition is that all retained producers and pending work have drained. They are not part of the current browser bundle.

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

Browser ingress accepts only `ticket_opening` and `ticket_v2_failure`. Per-socket and global admission are capped at 60 messages per minute, and every informational browser event is sampled into a shared minute bucket. Failures include HDR, unexpected script errors and unhandled promise rejections, with fixed categories instead of raw error text. Browser family and visibility are coarse labels; HDR stage/reason, brightness and picture age are bounded fields. Details remain capped at 1 KiB, private keys and private-looking values are removed, and central Ticket rows expire after six hours. Central cleanup removes up to 1,000 expired rows every five minutes, safely above bounded browser ingestion. Browser delivery is intentionally best-effort: up to 16 distinct unsent failures wait in memory for an open authenticated video socket, then are released after WebSocket send acceptance rather than a database acknowledgement. Repeated failure categories are limited to one every 30 seconds. The server ignores browser-supplied correlation and hashes the authenticated session instead. Audit writes use a separate short asynchronous deadline, so a logging delay cannot hold up a successful Ticket state change.

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

For ViVi control-code requests, Pixel proves that the generated screen exists, then the requester browser accepts one exact stream-resolution result identified by request, stream epoch, and frame sequence. The browser always prepares the ordinary SDR frame as the safe local fallback. With active browser HDR, it may instead hold that same frame on the HDR canvas only after the matching GPU completion and post-copy compositor opportunity; later stream frames cannot replace it. The transparent exact-HDR result card and the opaque SDR result card share the same visible close control and silent 60-second expiry; neither shows a countdown. SDR capture is complete only after the frozen image decodes, is visibly inside the viewport for two paint frames, and emits `control_code_frame_painted`; `control_code_frame_displayed` remains a compatibility event with the same post-paint meaning. Exact HDR has its own matching presentation milestone and falls back to the prepared SDR image on mismatch or timeout. Only after one of those presentations is proved may the browser acknowledge capture and allow Pixel cleanup. `ticket_remote` must not accept or expose Pixel screenshot payloads (`phone_root_image`, `imageMime`, or `imageBase64`) for ViVi control-code results. The detailed contract lives in the Pixel ticket streaming architecture doc.

After the result strip closes and the original Aztec detail is freshly proved, Pixel clears the request cleanup barrier and publishes the `fast_ready` watermark through one atomic service reducer. Browser controls therefore cannot observe cleanup as complete while the phone lane still projects blocked.

The exact result payload and browser-frozen image are requester-private, but the live H.264 stream is shared among authorized linked members. Other linked viewers may therefore see the phone's control-code UI while a request runs. SpacetimeDB public tables must remain sanitized operational/activity projections only; exact digits, email addresses, exact result values, and result images belong only in private records or requester-only delivery paths.

Pixel stream compute tuning must preserve the current rooted hardware H.264 profile: 994 px target width, 2046 px visible height, about 8 Mbps, and one complete, independently decodable picture per second. H.264 pads that frame to 1008x2048 so the coded surface stays within the established decoder and allocation ceilings. This single source profile feeds both SDR and browser HDR. Startup, slider, Ticket-action, control-code, and cleanup requests coalesce into one immediate next picture rather than changing cadence or starting a burst. Ordinary viewer demand is likewise coalesced into at most one legal next capture opportunity; it never raises the fixed ceiling or creates a catch-up burst. The source must advertise `frameDependencyMode=all_intra` with `fps=1`, `sourceFps=1`, and `keyframeIntervalFrames=1`. Current Pixel streams also advertise `frameEnvelope=tsf3` and send the 93-byte TSF3 identity and stage-time header; missing envelope remains TSF2 only for rolling compatibility. The relay rejects a missing or contradictory contract, malformed stage order, non-increasing epoch/sequence replay, an encoded H.264 payload above the canonical 2 MiB limit, and every delta picture; the envelope is additional bounded framing. A new config epoch clears cached frame authority, while source sequence gaps between independent pictures remain valid. The relay immediately runs a validated four-timestamp Pixel clock probe, converts phone monotonic stage times through that bounded UTC mapping, and carries mapping generation and uncertainty forward; one-way samples keep TSF2 working but cannot authorize TSF3, and an arriving frame never creates a fresh mapping after a quiet gap. Feedback v2 then permits one receipt-gated picture plus one newest waiting picture per viewer; v1 is diagnostic-only. The three-second source deadline still bounds frame admission and the WebSocket write, while a separate three-second interval after a successful write bounds receipt liveness. A complete picture received after its visual deadline may restore transport credit but cannot become visible authority. These requests use one root surface-capture helper and one MediaCodec encoder; they must not restart the encoder or leave a helper, encoder, or wrapper active after stop. Rollback must downgrade the Pixel to its legacy TSF2 producer while the compatible new relay is still running, then restore the older relay release. Never expose the older relay to the TSF3-only producer.

Normal ViVi control-code entry is keyboard-free and root-only. Pixel keeps the configured Android input method enabled and unchanged, acquires a request-scoped Accessibility soft-keyboard suppression lease, focuses the visually proved code field, and sends one rooted Android InputManager key-event batch that moves to the end, clears any old value, and enters the validated digits. It creates no temporary input device. Two fresh rooted visual samples must then prove the entered value and derive the enabled Submit target before a separate rooted tap is allowed. Focus is cleared and the original software-keyboard show mode is visibly restored before the request can finish cleanly; startup and service-replacement recovery fail closed on an unresolved ownership marker. If the popup remains open, Pixel may repeat the full clear, re-entry, and Submit transaction once only after fresh visual evidence proves that state; it never sends a blind second tap. Accessibility does not classify Ticket content, enter digits, navigate, or submit.

`/api/v1/health.directStream` is the first place to check source delivery: it records active browser video clients, phone relay state, the advertised envelope and frame-dependency tuple, whether the exact rooted all-intra contract is valid, bounded clock generation and uncertainty, config regressions, non-monotonic or oversize drops, distinct source-estimate/receipt timestamps, last frame, rejected all-intra deltas, reconnect count, and relay-owned events. Viewer decoder errors remain per-viewer diagnostics and cannot change this shared source verdict. Public `live` authority requires conservative `LIVE_FRESH` age. `LIVE_OK` may suppress only the browser's between-frame recovery cue when every canonical continuity check agrees; it cannot authorize an action. `DEGRADED` cannot authorize actions or suppress recovery. A source contract other than the canonical codec, Annex-B transport, rooted capture source and secure method, fixed GPU color correction, 1080x2424 source/crop geometry, 994x2046 output, 8 Mbps target, BT.709 limited SDR signal, and all-intra 1/1/1 tuple is rejected before it reaches viewers and reports `invalid_source_config` until the source sends a valid config. Browser media sockets must present the exact configured schemeful origin before membership lookup or phone wake. Their bounded clock replies are rate-limited and fenced to the current socket config generation. Feedback v2 grants one complete-message delivery credit per viewer; after a browser config is actually written, the fastest current visible viewer with no queue, write, or receipt debt drives one aggregate 2.5-second ordinary Pixel capture opportunity. A binary result completes it before admission, and no-result expiry retries only while credit remains. Proof, keyframe, startup, prewarm, and durable commands bypass this ordinary gate. Quiet Pixel and public browser media sockets use bounded Ping/Pong liveness; a failed public probe closes only that viewer, while a failed private probe reconnects with equal jitter and healthy activity resets exponential backoff. An already-desired private relay loop owns the in-flight handshake and must not be restarted by a parallel wake. Pixel answers Ping and clock-probe traffic immediately after upgrade, including while serialized phone preparation is running; keyframe and capture-demand handling begins only after the connection is atomically registered as the current generation. Phone health uses one replaceable slot; causal ticket transitions use an ordered 64-event FIFO and retry storage failures with capped jittered backoff. The normal 40-second stall matrix is lossless within 64 accepted events. Overflow is logged and rejected before source acceptance, then forces a reconnect; it is not a replay guarantee. Generated-result priority remains outside TSF. The first-party browser emits the hint only after its local Spacetime view matches an owned real request, but the relay does not receive that row and does not verify requester ownership. It treats the hint as untrusted viewer-local input: the arm lasts at most the durable request's five-minute workflow window, leaves ordinary frame delivery and capture demand running, and can affect only that socket; an exact public epoch/sequence marker starts a separate five-second delivery budget and releases the newest eligible picture without clearing existing receipt debt or changing another viewer. If marked delivery expires, only that viewer socket closes and its normal reconnect path may retry within the durable workflow deadline. Exact presentation, config replacement, close, failure, or expiry clears the reservation; every picture still obeys the same three-second source-freshness deadline.

If the phone leaves ViVi or Android system controls appear, the Pixel backend stops the ticket session; ticket-remote releases controle-code mode and returns viewers to general state.

## Public Page Expectations

The user-facing page is stream-first. On mobile fresh load, reload, reconnect, resize, and page restore, the first viewport should show only the stream. Status, control, and membership options live below the stream and become visible only after scrolling down.

Controle-code controls belong on the web page. The Pixel still enforces touch safety and ticket-page constraints, but it should not show a separate user-facing start screen for the public stream experience.

## Availability Assumption

The production path currently has one kitty-gration host and one physical Pixel. Recovery is procedural rather than automatic; use [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md) for the current limits and standby acceptance checks.

## Evidence Paths

New Ticket proof, if a task explicitly needs local artifacts, goes under `ops/evidence/ticket-remote/<timestamp-or-topic>/` with a short index.
Leftover Ticket pictures and old proof packs now live in `archive/ticket/`. Do not start Ticket work there.

### Page-opening warm hold

A freshly authorized Ticket page opening keeps the shared relay and phone stream available for 30 minutes from that opening. Reopening renews the same session hold; media reconnects, first-frame presentation, and closing the browser do not reset or release it. The timed reference uses the same relay lifecycle owner as existing startup holds but is independent of the short first-frame grace. It does not create a second viewer identity or keep sending browser pictures when no browser has delivery credit. Expiry removes only the warm reference; active viewers continue normally, and an otherwise unused stream follows the existing idle shutdown. This is process-local relay lifecycle state and is cleared during server shutdown.

The existing relay current report includes the same bounded `pageOpenWarm` retained-session count and expiry as authenticated health, without session identities. The local Pixel-repository health monitor uses that projection to distinguish intentional warmth from unexplained no-viewer activity. It calculates frame age from absolute `lastFrameAt`, checks relay `updatedAt` independently, and never treats the legacy zero-valued relative-age column as fresh-frame evidence. No extra reporter, database table, timer, or repair loop is introduced.
