# phone_broker Module Runbook

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Ticket-only incident model: [TICKET_PHONE_BROKER_INCIDENT_MODEL](../architecture/TICKET_PHONE_BROKER_INCIDENT_MODEL.md)

## Purpose

`phone_broker` is the private owner of the shared Pixel phone. It serves two callers today: the public `ticket_remote` viewer (`ticket.jolkins.id.lv`) and the upstream `ticket_phone_bridge` Android service. The broker is ticket-only: there is no longer an RS QR owner or RS re-login channel after the 2026-06-18 RS wind-down.

When ticket viewers are present, the broker holds other work in the queue. If a ticket viewer appears while work is running, the broker asks the phone to cancel and retry after the ticket page is free.

## Start / Validate

```bash
../../tools/arbuzas/deploy.sh deploy \
  --services ticket_phone_bridge,phone_broker,ticket_remote,ticket_remote_tunnel \
  --ssh-host kitty-gration \
  --ssh-user ropepop

../../tools/arbuzas/deploy.sh validate \
  --services phone_broker,ticket_remote \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```

## Local Checks

```bash
docker compose --project-name arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T phone_broker curl -fsS http://127.0.0.1:9398/api/v1/health
docker compose --project-name arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T phone_broker curl -fsS http://127.0.0.1:9398/api/v1/state
```

`/api/v1/state` shows the current phone owner, ticket viewer count, and ticket-lease state. It is internal only. After the RS wind-down the priority list is ticket-only: `desiredOwner` is `ticket` while a viewer is present, `none` otherwise. `desiredPriority` is `["ticket"]` or empty.

## Ticket Priority Contract

- `phone_broker` serializes ticket work against the shared Pixel; jobs are FIFO when no ticket viewer is actively using the phone.
- Active ticket viewers and ticket leases preempt other work for their full active duration. A running job is canceled back to `waiting` with `ticket_active` or `ticket_lease_active` and retried after the ticket page or control-code lease is free.
- Ticket jobs must complete with a real Pixel result (live ViVi ticket stream, control-code PNG, or a named final failure). The broker must not synthesize ticket artifacts.
- The public ticket relay may retain its startup prewarm viewer for up to the same 5-second load budget. This prevents the phone stream from being started by the authenticated index shell and then dropped before the browser's real video/control sockets attach.
- Operational target is authenticated load to live ViVi ticket in 5 seconds or less, with current stream delay measured from relay/Pixel frame age and kept below the existing freshness threshold.

## Re-enabling the historical RS path

The previous `rs biļete` Telegram bot, the `rs-acquisition-campaign` daemon, and the broker-side RS re-login channel are archived under `archive/rs-bot/` for archaeology. Re-enabling them is out of scope for normal operations; treat it as a new launch requiring fresh design, safety, and consent review. See `archive/rs-bot/README.md` for the file inventory and re-enable recipe.
