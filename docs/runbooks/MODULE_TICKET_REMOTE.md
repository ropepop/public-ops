# ticket_remote Module Runbook

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Disaster recovery and standby requirements: [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md)

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

## Health Checks

```bash
curl -fsS http://127.0.0.1:9338/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl https://ticket.jolkins.id.lv/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl -I https://ticket.jolkins.id.lv/ | rg -i 'cache-control|cdn-cache-control|cf-cache-status|clear-site-data'
```

Production normally uses SpacetimeAuth on the page. Cloudflare Access remains a supported fallback mode; when it is selected, a plain request may redirect to Access login. Use the authenticated browser for user-facing checks and local container health for origin checks.

To confirm the newest page is live, compare the page's embedded version with `/api/v1/health` `serverVersion`, then check that response headers are no-store/dynamic instead of a stale cached response.

For phone-stream failures, validate the private phone path before debugging the public page:

```bash
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T ticket_phone_bridge /usr/local/bin/ticket-phone-bridge-health'
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T ticket_remote sh -lc "curl -fsS http://ticket_phone_bridge:9388/api/v1/health"'
ssh kitty-gration 'docker compose -p arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml logs --since 10m ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote'
```

`ticket_phone_bridge` has its own healthcheck and watchdog. It verifies the Pixel is connected over ADB, the exact ADB forward exists, and the forwarded Pixel health endpoint answers. If that check fails while `socat` is still listening, the bridge loop stops the listener, removes the stale ADB forward, reconnects to the Pixel, and starts a fresh listener. Deploy validation checks this bridge directly and also proves that `ticket_remote` can reach the Pixel health endpoint through the private Compose network.

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

The exact result payload and browser-frozen image are requester-private, but the live H.264 stream is shared among authorized linked members. Other linked viewers may therefore see the phone's control-code UI while a request runs. SpacetimeDB public tables must remain sanitized operational/activity projections only; exact digits, email addresses, exact result values, and result images belong only in private records or requester-only delivery paths.

Pixel stream compute tuning must preserve the current rooted hardware H.264 profile: 720 px target width, about 1.2 Mbps, 1 FPS steady viewing, a three-frame 5 FPS cold-start burst, and the bounded 4 FPS ViVi control-code request window from Pixel command receipt through requester-browser freeze acknowledgement. Cleanup has its own short 5 FPS visual-proof window. These modes pace one root surface-capture helper and one MediaCodec encoder; they must not restart the encoder for a request or leave a helper, encoder, wrapper, or request burst active after stop.

Normal ViVi control-code entry is keyboard-free and root-only. Pixel briefly disables the configured Android input method, focuses the known code field, clears any old value, enters the validated digits, moves focus away, restores the input method, and taps the known unshifted Submit target in one bounded transaction. A shell trap, a unique short-lived restore lease with a detached watchdog, request cleanup, startup recovery, and shutdown recovery prevent the phone from being left without its input method. Ticket entry and Submit do not use Android Accessibility. If the popup remains open, Pixel may repeat the full clear, re-entry, and Submit transaction once, but only after two distinct fresh 4 FPS samples prove the same unshifted popup and enabled Submit button; it never sends a blind second tap.

`/api/v1/health.directStream` is the first place to check stream delivery: it records active browser video clients, phone relay state, last config, last frame, last keyframe, reconnect count, and recent browser decoder telemetry.

If the phone leaves ViVi or Android system controls appear, the Pixel backend stops the ticket session; ticket-remote releases controle-code mode and returns viewers to general state.

## Public Page Expectations

The user-facing page is stream-first. On mobile fresh load, reload, reconnect, resize, and page restore, the first viewport should show only the stream. Status, control, and membership options live below the stream and become visible only after scrolling down.

Controle-code controls belong on the web page. The Pixel still enforces touch safety and ticket-page constraints, but it should not show a separate user-facing start screen for the public stream experience.

## Availability Assumption

The production path currently has one kitty-gration host and one physical Pixel. Recovery is procedural rather than automatic; use [TICKET_REMOTE_DISASTER_RECOVERY](./TICKET_REMOTE_DISASTER_RECOVERY.md) for the current limits and standby acceptance checks.

## Evidence Paths

- `ops/evidence/ticket-remote/`
