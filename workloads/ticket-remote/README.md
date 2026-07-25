# Ticket Remote

Public manager for `ticket.jolkins.id.lv`.

For agent-specific context, browser verification, auth-mode selection, and control-code pitfalls, start with [AGENTS.md](./AGENTS.md).

The service validates the configured email identity, checks ticket membership in SpacetimeDB, and relays the active phone backend stream. Authenticated browsers request control codes through member-only Spacetime reducers; they never receive direct phone control.

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
- Other viewers stay connected to the shared raw ticket stream. They may briefly see the real phone open and close ViVi's control-code UI, but they never receive direct control.
- Browser users connect directly to the Ticket SpacetimeDB database for authenticated product state and code-request actions. Browser diagnostics instead travel over the existing authenticated video WebSocket so `ticket_remote` can validate and sanitize them before the sidecar writes them to the central operational log. `ticket_remote` is not a second control authority.
- Browser users never talk directly to the Pixel; the server writes durable Spacetime state/commands and the Pixel executes them.
- While the Ticket stream or a control-code request is active, Pixel reads pending commands once at the start of each 75 ms cycle and skips the idle lane's duplicate pending, signal, and desired-state reads. Idle reconciliation still uses the compact signal and writes the phone report only when stable state changes.
- The production backend is the physical Pixel through the private `ticket_phone_bridge`; `ticket_remote` relays media directly from that bridge while SpacetimeDB owns durable stream intent and commands.
- Runtime diagnostics are SpacetimeDB-only in `operational-logging-prod`, under `operationallog_event` rows with `domain = 'ticket'` and six-hour retention. The existing minute sampling for routine informational events remains in the central reducer. `ticket_remote`, the phone bridge, and Pixel ticket automation must not leave local runtime log files behind.
- Browser diagnostics are best-effort: they queue until an authenticated video socket is open and are removed after the WebSocket accepts the send. The server accepts only the reviewed 64-name event vocabulary, caps both each socket and the whole service at 60 messages per minute, removes private keys and values, and keeps the first-rendered-frame lifecycle acknowledgement independent from diagnostic admission. It does not trust browser correlation IDs or acknowledge database persistence; it derives correlation from the authenticated browser session.
- Successful Ticket mutations never wait for audit logging. Audit rows use a separate asynchronous 750 ms deadline, and central cleanup removes up to 1,000 expired rows every five minutes so it safely outpaces the bounded browser path.
- Public video is H.264 over the existing HTTPS WebSocket: the active phone backend emits one private rooted hardware H.264 stream, and `ticket_remote` fans it out to authenticated browsers without public media ports.
- Pixel capture uses the current readable hardware profile: 720 px target width at about 1.2 Mbps, 1 FPS steady viewing, a three-frame 5 FPS cold-start burst, a bounded 4 FPS control-code request window through requester-browser freeze, and a separate short 5 FPS cleanup-proof window. These modes share one root capture helper and one MediaCodec encoder without restarting it for a request.
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
