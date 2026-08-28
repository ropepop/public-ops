# Ticket Remote

Public manager for `ticket.jolkins.id.lv`.

Start with [CURRENT.md](./CURRENT.md). That is the live Ticket product map: open the signed-in page, get a live picture, swipe the oval, open a fresh unused ticket, or request a control code.

For agent rules and live-page proof, use [AGENTS.md](./AGENTS.md). For start, stop, and health, use the [Ticket runbook](../../docs/runbooks/MODULE_TICKET_REMOTE.md).

The service checks Ticket membership and relays the phone stream to signed-in browsers. Authenticated browsers write bounded Ticket actions directly to SpacetimeDB; the Pixel visually executes those durable rows. Browsers never receive direct phone control.

## Local Development

```bash
make test
make spacetime-build
make web-client-build
```

## Runtime Model

- General mode: linked users can view the ViVi ticket stream together.
- Control-code requests: a linked user enters 2-8 digits in the webpage, a member-only Spacetime reducer queues the request, the phone automates ViVi, and the requester browser freezes a stable stream-resolution live frame to capture the result locally. The browser acknowledges capture only after that frozen image has decoded, remained visible through two paint frames, and passed an on-screen bounds check; assigning an image URL is not an acknowledgement. Pixel must not send ViVi result screenshot bytes to the browser.
- A requester can submit at most two code requests per rolling minute. A second successful request replaces the visible result on that user's page.
- Registration admission is per authenticated account: one admitted physical registration every 30 seconds and ten counted admissions per rolling hour. Admins and owners obey the same limits by default; their account-persistent SpaceTime preference may bypass quotas for testing, with bypassed actions retained as non-counting audit events.
- Other viewers stay connected to the shared raw ticket stream. They may briefly see the real phone open and close ViVi's control-code UI, but they never receive direct control.
- Browser users connect directly to the Ticket SpacetimeDB database for authenticated product state and code-request actions. Browser diagnostics instead travel over the existing authenticated video WebSocket so `ticket_remote` can validate and sanitize them before the sidecar writes them to the central operational log. `ticket_remote` is not a second control authority.
- Browser users never talk directly to the Pixel. Immediate Ticket actions and admin re-detection schedules use authenticated Spacetime reducers that atomically create a sanitized action projection and a durable command row; `ticket_remote` remains the authentication and video-relay boundary.
- Pixel keeps one subscription for every pending Ticket command and one dispatch ledger shared with reconnect polling. The subscription is the warm path; polling is catch-up and acknowledgement recovery. Registration readiness precedes the final fresh proof, after which V3 checkpoints and dispatches one uninterrupted 800 ms stroke; an accepted or uncertain physical action is visually reconciled and never blindly repeated.
- Ticket action v3 has seven targets: open the latest unused ticket, open and register it, register the currently open ticket, show the recently activated ticket, return to the latest unused ticket, re-detect the latest ticket, and non-mutating `prove_current`. Every mutating target acquires the single phone-mutation lane, observes fresh rooted frames, performs at most one proven transition at a time, and publishes one terminal sanitized result. `prove_current` only observes two agreeing frames and never navigates, recognizes a date, taps, or registers.
- The public action projection contains only opaque action identity, target, progress, current visual view, switch availability/expiry, frame watermark, bounded reason, and timestamps. When an unused ticket is proven, a separate five-minute projection may contain the normalized slider rectangle bound to that exact action and stream watermark so the browser can place a transparent local range input over the live picture. Dates, ticket text, recognized glyph output, raw device coordinates, screenshots, and pixels remain on the phone.
- Spacetime is the sole business-clock authority. It computes quota eligibility, schedules quota projection boundaries, creates the 15-minute smart-switch anchor, rejects expired switch requests, and owns delayed refresh/re-detection rows. Pixel retains only private visual matching evidence, physical-action checkpoints, bounded action timeouts, and transport recovery; none of those phone-local timers may originate a future product action.
- The production backend is the physical Pixel through the private `ticket_phone_bridge`; `ticket_remote` relays media directly from that bridge while SpacetimeDB owns durable stream intent and commands.
- Runtime diagnostics are SpacetimeDB-only in `operational-logging-prod`, under `operationallog_event` rows with `domain = 'ticket'` and six-hour retention. The existing minute sampling for routine informational events remains in the central reducer. `ticket_remote`, the phone bridge, and Pixel ticket automation must not leave local runtime log files behind.
- Browser diagnostics are best-effort: they queue until an authenticated video socket is open and are removed after the WebSocket accepts the send. The server accepts only the reviewed 66-name event vocabulary, caps both each socket and the whole service at 60 messages per minute, removes private keys and values, and keeps the first-rendered-frame lifecycle acknowledgement independent from diagnostic admission. It does not trust browser correlation IDs or acknowledge database persistence; it derives correlation from the authenticated browser session.
- Successful Ticket mutations never wait for audit logging. Audit rows use a separate asynchronous 750 ms deadline, and central cleanup removes up to 1,000 expired rows every five minutes so it safely outpaces the bounded browser path.
- Public video is H.264 over the existing HTTPS WebSocket: the active phone backend emits one private rooted hardware H.264 stream, and `ticket_remote` fans it out to authenticated browsers without public media ports.
- Active authenticated members may optionally layer the bounded true-HDR image socket over that same authorized stream. The choice is a private account setting in Ticket SpacetimeDB, defaults off, and restores across sessions and devices. The SDR stream remains authoritative and continuously live underneath; HDR image replacements decode before swapping, and any unsupported capability, stale source, socket failure, invalid image, or sustained transform failure immediately falls back without changing the phone capture or storing media.
- Pixel capture uses one readable SDR hardware profile: 720 px target width at about 1.2 Mbps, BT.709 limited color, and one H.264 encoder configured for a 10 FPS ceiling. The helper changes cadence in place: 1 FPS for static or constrained demand, 5 FPS for moderate motion, and 10 FPS for active motion, startup, slider, and control-code work. Cadence changes use monotonic deadlines, skip expired ticks instead of catching up, and never restart the encoder or change the stream picture contract.
- Browser delivery is adaptive per viewer. Healthy visible viewers receive the contiguous stream; a viewer with decoder backlog, a sequence gap, or visual age above the safety limits is moved to natural keyframes only until two advancing keyframes and a stable probe prove recovery. The relay keeps bounded per-client queues, so one slow connection cannot block the phone or reduce a healthy viewer's cadence. The browser sends cumulative authenticated feedback; it never sends bitrate, resolution, or color controls.
- Interactive browser UI uses ArrowJS for changing presence, status, stream, and control areas. Keep static or no-JS pages simple until they need browser-side interactivity.
- Edit browser UI in `web-client/`, rebuild with `make web-client-build`, and verify the deployed page mounts the Arrow-backed path.

## Privacy and Trust Boundary

- Requester-private data is the exact request digits, exact returned value, and frozen result image. Digits and requester email stay in private records, the exact result is routed only to the requester, and the browser creates the frozen image locally instead of uploading a phone screenshot or storing image bytes in SpacetimeDB.
- The live H.264 stream is shared among all authorized linked members. Every connected member receives the same phone frames and may see ViVi's control-code screen while a request runs, so linked membership is the media privacy boundary; requester-private result delivery does not make the live stream requester-private.
- SpacetimeDB's public tables are sanitized operational and activity projections. They may expose backend and stream state, viewer counts or public IDs, request status, timing, and non-secret proof markers, but they must not contain exact digits, email addresses, exact result values, or result images. Those values remain in private records or requester-only delivery paths.

## Availability Model

Production currently depends on one kitty-gration host and one physical Pixel. State and configuration are recoverable, but there is no automatic host or phone failover; see [Ticket Remote disaster recovery](../../docs/runbooks/TICKET_REMOTE_DISASTER_RECOVERY.md) for the recovery and standby requirements.

## Runtime Containment

- The public tunnel, phone bridge, and Spacetime sidecar are separated onto three dedicated Docker networks. `ticket_remote` is the only service present on all three, so the tunnel cannot directly reach the phone bridge or signing sidecar and unrelated workloads cannot directly reach any Ticket service.
- Docker stdout/stderr retention for the four Ticket runtime containers is capped at three 10 MB local-driver files per container. Durable Ticket diagnostics remain in the bounded central SpacetimeDB operational table.
- The Ticket Cloudflare tunnel runs as the owner of its read-only credential file, with a read-only root filesystem, all Linux capabilities dropped, no privilege escalation, and a 64-process ceiling. The production cloudflared image is pinned by digest. If credential ownership changes, update `ARBUZAS_TICKET_TUNNEL_UID` and `ARBUZAS_TICKET_TUNNEL_GID` together after a mirror pull and ownership check.
- Authenticated browser sockets are capped at four per browser session, eight per member identity, and 64 across the service. Closed sockets immediately release their capacity.

## Required Production Configuration

- `TICKET_REMOTE_AUTH_MODE=spacetime`
- `TICKET_REMOTE_SPACETIME_AUTH_ISSUER=https://auth.spacetimedb.com/oidc`
- `TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID`
- `TICKET_REMOTE_SESSION_SIGNING_KEY`
- `TICKET_REMOTE_STATE_BACKEND=spacetime`
- `TICKET_REMOTE_SPACETIME_DATABASE`
- `TICKET_REMOTE_SPACETIME_OIDC_ISSUER=https://vilciens.kontrole.info/oidc` on kitty-gration, matching the public train OIDC issuer that publishes the runtime signing key.
- `TICKET_REMOTE_SPACETIME_OIDC_AUDIENCE=train-bot-web`
- `TICKET_REMOTE_SPACETIME_SERVICE_SUBJECT=service:ticket-remote`
- `TICKET_REMOTE_SPACETIME_SERVICE_ROLES=ticketremote_service`
- the Spacetime module pins all four service-token claims above; change the module constants and deployment environment together so direct reducer calls remain fail-closed
- the stable database-owner identity may connect for operational SQL, but it does not bypass reducer authorization; anonymous and non-member clients remain rejected
- `TICKET_REMOTE_SPACETIME_CLIENT_URL=http://ticket_remote_spacetime_sidecar:9346`
- `TICKET_REMOTE_SPACETIME_SIDECAR_WRITE_TOKEN_FILE=/run/secrets/ticket-remote/sidecar-write-token.secret`
- `TICKET_REMOTE_OPERATIONAL_LOGGING_HOST=https://maincloud.spacetimedb.com`
- `TICKET_REMOTE_OPERATIONAL_LOGGING_DATABASE=operational-logging-prod`
- the sidecar reuses its short-lived signed service JWT for the central `operationallog_append_ticket_event` reducer; the logging database must enroll that identity for the `ticket` domain before cutover
- the Spacetime private signing key is mounted only into `ticket_remote_spacetime_sidecar`; the public `ticket_remote` container must not receive it
- after login and a current membership check, the sidecar issues a five-minute member-proxy token using that key's real issuer and audience; the module separately pins its proxy role, `member:<email>` subject, verified email, and live membership, without granting service reducers
- `TICKET_REMOTE_PHONE_BACKENDS`
- `TICKET_REMOTE_PHONE_BASE_URL=http://ticket_phone_bridge:9388`
- `TICKET_REMOTE_PHONE_TIME_ZONE=Europe/Riga` for the Pixel-local wall clock used by admin-scheduled latest-ticket re-detection
- `TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID`
- `TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE`

Cloudflare remains only the HTTPS tunnel. SpacetimeAuth is the public email-login front door, and SpacetimeDB remains the ticket membership and session state source of truth. There is still no public ADB, browser-direct phone access, Docker control, public media port, separate media service, broad `/etc/arbuzas/secrets` mount, Pixel ADB key mount, or extra ticket Docker unit.
