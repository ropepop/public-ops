# ticket_remote Module Runbook

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)

## Start / Stop / Restart

```bash
../../tools/arbuzas/deploy.sh deploy \
  --services ticket_phone_bridge,phone_broker,ticket_remote,ticket_remote_tunnel \
  --ssh-host arbuzas \
  --ssh-user ropepop

../../tools/arbuzas/deploy.sh validate \
  --services ticket_phone_bridge,phone_broker,ticket_remote,ticket_remote_tunnel \
  --ssh-host arbuzas \
  --ssh-user ropepop
```

Use an explicit `--release-id` for traceable deploys when cutting a known user-facing change.

## Health Checks

```bash
curl -fsS http://127.0.0.1:9338/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl https://ticket.jolkins.id.lv/api/v1/health | jq '.serverVersion, .phone, .directStream'
cloudflared access curl -I https://ticket.jolkins.id.lv/ | rg -i 'cache-control|cdn-cache-control|cf-cache-status|clear-site-data'
```

The public endpoint sits behind Cloudflare Access for page and API use. A plain unauthenticated public request may redirect to Access login; use local container health for origin checks and `cloudflared access curl` for authenticated public checks.

To confirm the newest page is live, compare the page's embedded version with `/api/v1/health` `serverVersion`, then check that response headers are no-store/dynamic instead of a stale cached response.

## Cloudflare Access

Configure a self-hosted Access app for `ticket.jolkins.id.lv`.

- Login method: One-Time PIN / email.
- Policy/session duration: `1 month`.
- Bootstrap admin/member email: `ticket@jolkins.id.lv`.
- Service validates `Cf-Access-Jwt-Assertion`; set the app audience tag in `TICKET_REMOTE_CF_ACCESS_AUDIENCE`.
- SpacetimeDB controls linked ticket membership after Cloudflare confirms identity.

## Pixel Backend

The phone backend is private to Ops through `phone_broker`, which is the only Arbuzas service ticket_remote should use for phone sessions. The broker owns priority between ticket viewing and lower-priority phone automation, then talks privately to `ticket_phone_bridge`.
`ticket_phone_bridge` connects to the Pixel over ADB on Tailscale, forwards the Pixel's local ticket stream port inside Docker, and exposes it only inside the private Docker network.
The bridge uses the ADB key files in `/etc/arbuzas/secrets/android-adb/`, mounted read-only into the bridge container. Keep those files scoped to the bridge; they are what let Ops reach the already-authorized Pixel without asking Android to approve a new container identity.

The browser never receives the phone URL and never talks directly to the Pixel. Browser clients talk to `ticket_remote`; `ticket_remote` talks privately through `phone_broker`. The normal public stream path is H.264 over the existing HTTPS `/api/v1/stream` WebSocket, decoded in the browser with WebCodecs. Do not add public media ports, a separate media service, or a second public tunnel unless there is a fresh decision to redesign the deployment.

For ViVi control-code requests, Pixel proves that the generated screen exists, then the requester browser captures a stable stream-resolution result from its rendered live stream. `ticket_remote` must not accept or expose Pixel screenshot payloads (`phone_root_image`, `imageMime`, or `imageBase64`) for ViVi control-code results. The detailed contract lives in the Pixel ticket streaming architecture doc.

Pixel stream compute tuning must preserve the current capture profile: 900 px target width, 10 FPS, 5 Mbps, FFmpeg H.264 baseline/ultrafast settings, and the existing keyframe cadence. The intended optimization boundary is duplicate/orphan process removal only: one root surface capture helper and one FFmpeg encoder while streaming, and no helper, encoder, or wrapper process left after stop.

`/api/v1/health.directStream` is the first place to check stream delivery: it records active browser video clients, phone relay state, last config, last frame, last keyframe, reconnect count, and recent browser decoder telemetry.

If the phone leaves ViVi or Android system controls appear, the Pixel backend stops the ticket session; ticket-remote releases controle-code mode and returns viewers to general state.

## Public Page Expectations

The user-facing page is stream-first. On mobile fresh load, reload, reconnect, resize, and page restore, the first viewport should show only the stream. Status, control, and membership options live below the stream and become visible only after scrolling down.

Controle-code controls belong on the web page. The Pixel still enforces touch safety and ticket-page constraints, but it should not show a separate user-facing start screen for the public stream experience.

## Evidence Paths

- `ops/evidence/ticket-remote/`
