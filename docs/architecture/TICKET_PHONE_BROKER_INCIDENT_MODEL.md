# Ticket Phone-Broker Incident Model

This document is the stable architecture contract for the ticket-only phone-broker surface on kitty-gration. It replaces the previous RS/ViVi incident model now that the Rīgas Satiksme monthly-ticket Telegram bot and acquisition campaign have been wound down.

The shared Pixel phone media path is still proxied by `phone_broker`. Stream-control truth is no longer owned by the broker session socket; it is represented by Spacetime stream desired-state and command rows.

## Surfaces in scope

- `phone_broker` (Go): ticket-only proxy on kitty-gration. Reachable at `http://phone_broker:9398` on the Compose network.
- `ticket_phone_bridge` (Go + Android ADB): Pixel-side bridge that owns the `TicketStreamService` WebSocket and the phone ADB forward.
- `ticket_remote` (Go): public authenticated ticket viewer. It relays browser video through `/api/v1/stream` and publishes stream state/control intent to Spacetime.
- `ticket_remote_tunnel` (cloudflared): the public Cloudflare tunnel for `ticket.jolkins.id.lv`.
- Public user surface: `https://ticket.jolkins.id.lv/` (authenticated) and the unauthenticated `/api/v1/livez` and `/api/v1/health` endpoints.

## Phone-broker HTTP surface (ticket-only)

| Method | Path | Purpose |
| ------ | ---- | ------- |
| GET | `/api/v1/health` | Liveness. With `?strict=1`, fails if the upstream Pixel is unreachable. |
| GET | `/api/v1/state` | Internal-only phone state snapshot. |
| POST | `/api/v1/ticket/presence` | `{ "viewers": N }` — updates ticket presence from the page. |
| POST | `/api/v1/phone/leases/ticket` | Acquire a ticket lease; `{ leaseId, requestId, reason, ttlMillis }`. |
| POST | `/api/v1/phone/leases/ticket/release` | Release a ticket lease by `leaseId` or `requestId`. |
| POST | `/api/v1/session/start` | Legacy proxy to upstream `/api/v1/session/start`; not used by `ticket_remote` as stream-control authority. |
| POST | `/api/v1/session/stop` | Legacy proxy to upstream `/api/v1/session/stop`; not used by `ticket_remote` as stream-control authority. |
| WS | `/api/v1/session` | Legacy proxy to upstream `/api/v1/session` (ticket control); not used by `ticket_remote` as stream-control authority. |
| WS | `/api/v1/stream` | Proxy to upstream `/api/v1/stream` (H.264 video frames). |

All other paths (`/api/v1/qr/jobs*`, `/api/v1/rs/login*`, `/api/v1/analytics`) are gone. The broker is no longer a job queue; it is a stateful proxy with ticket-priority awareness.

## State model

The phone has exactly two possible owners:

- `ticket` — at least one ticket viewer, ticket socket, or active ticket lease is present, or the ticket grace window has not yet expired.
- `none` — no ticket activity; the phone is idle and the upstream may be in a non-ticket screen.

`desiredPriority` is the same list: `["ticket"]` when active, empty otherwise. There is no longer a `rigassatiksme` owner, no QR job queue, and no preempt-via-cancel logic. The broker still tracks `lastPreemptionReason` / `lastPreemptionAt` for historical / observability continuity but those are no longer written to.

The ticket grace window (`PHONE_BROKER_TICKET_GRACE`, default 10s) gives the phone a few seconds to settle in ticket mode after the last viewer/socket goes away. The grace prevents thrashing when ticket pages reconnect.

Lease TTL is clamped to `[3s, 3m]`, default `45s`. Leases auto-expire and are pruned on snapshot.

## Spacetime stream-control contract

Browser video stays on the existing WebSocket stream path. Spacetime is the durable state and control plane for everything around the stream: desired phone state, one-shot commands, phone reports, and safe operational logs.

Current-state tables keep only the latest truth and do not expire: `ticketremote_ticket_summary`, `ticketremote_stream_desired_state`, and `ticketremote_phone_current_report`.

History and log tables expire after 24 hours and are pruned by the scheduled cleanup job every 30 minutes: `ticketremote_stream_command`, `ticketremote_safe_operational_log`, `ticketremote_phone_status_history`, and `ticketremote_audit_event`. Expiring tables must keep an `expiresAt` index so cleanup is bounded.

`ticketremote_stream_desired_state` is idempotent. It records whether the stream should be active, how many viewers are present, the current mode, and the safe reason for the latest state change.

`ticketremote_stream_command` is for one-shot work such as keyframe requests or controlled phone actions. Commands carry a revision, bounded safe JSON payload, expiry, and acknowledgement status. They must not contain screenshots, frame bytes, auth tokens, raw health dumps, or other secret material.

`ticketremote_phone_current_report` is the Pixel-side latest report. The future direct Pixel client should subscribe to desired state and commands, then write this report back directly. Until that cutover is complete, `phone_broker` remains the stream proxy.

Implemented ticket_remote cutover rule:

- Browser video remains WebSocket-based through `/api/v1/stream`.
- Opening and closing browser video viewers writes Spacetime desired stream state.
- Prewarm, keyframe, recovery, control-code prepare, control-code generate, capture acknowledgement, result acknowledgement, activity, and control-exit are Spacetime command rows.
- `ticket_remote` must not use broker `/api/v1/session/start`, `/api/v1/session/stop`, or broker `/api/v1/session` as control authority.
- `phone_broker` can remain the media proxy until the Pixel sidecar is deployed, but it is not the source of control truth.

## Phone-broker incident trace schema

`standards/schemas/ticket-phone-incident-trace.v1.schema.json` is the canonical schema for any production incident on this surface. Required fields:

- `traceId` — safe, non-secret identifier. Format suggestion: `ticket:<sequence>`.
- `actor` — safe actor label. SpacetimeAuth email login when available, `user:<hex>` hash otherwise. Never a raw numeric Telegram/Cloudflare user ID.
- `userImpact` — what the affected user actually saw: page load success, stream reach-live, control-code accept.
- `phases` — `auth`, `sessionStart`, `sessionConnect`, `streamFirstFrame`, `controlCodeSubmit`, `controlCodeResult`, `viviCleanup`, `sessionStop`. Each with `at`, `durationMillis`, `ok`, `reason`.
- `broker` — broker state at incident time: `desiredOwner`, `desiredPriority`, `ticketViewers`, `ticketSockets`, `activeLeaseId`, `leaseReason`, `leaseRequestId`. No raw job IDs.
- `upstream` — Pixel/TicketStreamService phase timings: `ticketState`, `viviState`, `streamVerdict`, `streamActive`, `controlCodeRequestId`, `phases`. No image bytes, no raw codes.
- `outcome` — `open` / `mitigated` / `fixed` / `recovered` / `failed` / `monitoring` with `rootCause`, `tests`, `deployEvidence`, `liveVerificationEvidence`.
- `safety` — declaration that the trace contains `noSecrets`, `noRawUserIDs`, and `noCodes`. All three must be `true`.

## Operational targets

- Public `ticket.jolkins.id.lv` load to live ViVi ticket in 5 seconds or less on average.
- Public stream frame delay (visibleFrame / directStream last-frame age) at or below 1500 ms during close-out.
- Upstream Pixel health (ticket_phone_bridge) green; broker `/api/v1/health?strict=1` returns 200.
- Ticket lease hold time bounded by configured TTL; expired leases pruned on next snapshot.

## Watchdog / steward expectations

When a ticket-phone incident happens, the watchdog and steward must produce a trace that conforms to the schema above. Synthetic smokes are not enough; the trace must show the affected user, the broker state at the time, the upstream phase timings, and a root cause tied to a deploy or a known regression. Public safety headers (HSTS, CSP, X-Frame-Options, X-Content-Type-Options) must be verified on every deploy that touches `ticket_remote` or the tunnel.

## Re-enable of the historical RS path

The previous `rs biļete` Telegram bot, the `rs-acquisition-campaign` daemon, the broker-side RS re-login channel, and the old `rs-vivi-incident-trace` schema are archived under `archive/rs-bot/` for archaeology. The Pixel side still carries the `RigasSatiksmeLoginOperation` and the `rigassatiksme_login_*` WebSocket commands in the external `pixel-phone` repo; the broker no longer sends those commands. Re-enabling any of this is out of scope for normal operations and requires fresh design, safety, and consent review.
