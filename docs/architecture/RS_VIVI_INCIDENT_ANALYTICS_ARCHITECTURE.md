# RS/ViVi Incident Analytics Architecture

This document is the stable architecture contract for the shared Pixel phone incidents that cross:

- Telegram Rīgas Satiksme monthly-ticket QR bot
- `phone_broker`
- Android `ticket_screen` / `TicketStreamService`
- `ticket_phone_bridge`
- public `ticket.jolkins.id.lv` ViVi stream
- Hermes watchdog/steward cron jobs

It exists because aggregate health and synthetic smokes repeatedly hid real-user failures. A system can be green while a real Telegram user has a failed RS QR request, or while the phone returned from RS to the wrong ViVi screen and the public ticket page is effectively broken.

## Architecture problems learned from incidents

1. **Health was component-local instead of user-impact centric.** Broker queue depth, Pixel health, or a passing fake smoke did not answer: which user asked, did they get a real RS monthly-ticket PNG, and did ViVi return to `TICKET_DETAIL`?
2. **The causal chain was split across stores.** Telegram user/access state lived in the bot, job attempts lived in `phone_broker`, Android phase failures lived in bridge health/logs, images lived behind broker `/image`, and public verification lived in a browser profile. Agents had to manually stitch this together.
3. **Synthetic success could mask real failures.** A steward could run a fake-Telegram smoke after the app recovered and report success, while earlier real users such as `@iamhdzs` still had failed jobs with no root-cause closure.
4. **Failure names were not enough.** Reasons like `qr_image_missing`, `rs_monthly_ticket_image_capture_failed`, `rs_monthly_ticket_flow_timeout`, `stale_code`, and `UNKNOWN_VIVI` need phase timing and cleanup state to point at the right component.
5. **Monitoring output was ad hoc.** Watchdog messages were evolving by patch rather than by a formal incident schema, so future agents could miss safe actor labels, retry counts, or closure requirements.

## New incident model

Every RS/ViVi production incident must be treated as a **user incident trace**, not as a service-health blip.

Required trace fields:

- **Trace ID:** safe, non-secret identifier (`rsqr:<sequence>` for broker rollups; no raw broker job ID in public/steward reports unless it is strictly needed locally).
- **Safe actor:** Telegram username from RS bot access state when available (`@name`), otherwise a stable hash (`user:<hex>`). Never print raw numeric Telegram IDs.
- **Broker job summary:** sequence, status, reason, attempts, created/started/completed timestamps, queue seconds, final-attempt seconds, total seconds.
- **Lifecycle phases:** Telegram request, broker job creation, broker attempt, phone control connection, RS navigation, code submit, RS control screen verification, PNG capture, Telegram photo send, ViVi cleanup, ViVi ticket-detail restoration, public ticket verification when relevant.
- **Outcome:** open / mitigated / fixed / recovered / failed / monitoring, with root cause, tests, deploy evidence, and live verification evidence.
- **Safety marker:** trace artifacts must declare they contain no secrets, no raw user IDs, and no RS codes.

Canonical schema files:

- `standards/schemas/rs-vivi-incident-trace.v1.schema.json`
- `standards/schemas/rs-vivi-qr-analytics.v1.schema.json`

The same schema files are mirrored in the Pixel repo because Android/ViVi recovery and public ticket verification live there.

## New broker analytics surface

`phone_broker` exposes a safe rollup endpoint:

```text
GET /api/v1/analytics
```

Response shape:

```json
{
  "ok": true,
  "analytics": {
    "schema": "rs-vivi-qr-analytics.v1",
    "generatedAt": "...",
    "rsQr": {
      "totals": {
        "jobs": 0,
        "waiting": 0,
        "running": 0,
        "succeeded": 0,
        "failed": 0,
        "canceled": 0,
        "retried": 0,
        "slowSuccess": 0
      },
      "byReason": {},
      "successLatencySec": { "count": 0 },
      "userImpact": [
        {
          "actorHash": "user:<hash>",
          "jobs": 0,
          "failed": 0,
          "retried": 0,
          "lastStatus": "failed",
          "lastReason": "...",
          "lastAt": "..."
        }
      ],
      "recentIncidents": []
    }
  }
}
```

Safety constraints:

- The endpoint does **not** expose RS five-digit codes.
- It does **not** expose raw Telegram chat IDs or user IDs.
- It does **not** expose raw broker job IDs.
- It uses `actorHash` only when actor correlation is needed.
- The Telegram bot access state remains the only place agents may map known safe usernames.

Semantics:

- `retried` means `attempts > 1`; it includes slow successes that should still be investigated.
- `slowSuccess` currently means successful RS QR delivery took at least 60 seconds end-to-end.
- `userImpact` groups all retained broker jobs by safe actor hash so watchdog/steward runs can start from affected users instead of aggregate health.
- `recentIncidents` includes failures, non-user cancellations, running jobs, retried jobs, and slow successes.
- Broker analytics are a starting point; they do not replace image semantics or live ViVi/public verification.

## Watchdog contract

The 5-minute watchdog is cheap and silent when healthy. When it emits output, it must include:

- safe recovery action attempted, if any;
- real RS QR failures or slow/retried successes with safe actor label/hash;
- compact bridge health;
- broker analytics summary when available.

The watchdog may safely request `POST /api/v1/session/start` only when no RS QR job is queued/running. It must not interrupt active real-user QR generation.

## Steward contract

Hourly steward runs must begin from user-impact data, not from synthetic smokes:

1. Read latest watchdog output and broker `/api/v1/analytics`.
2. Inspect persisted broker jobs only if more detail is required; redact codes, raw user IDs, tokens, cookies, sessions, and connection strings.
3. Map actors to known Telegram usernames via RS bot access state when available.
4. Group incidents by real affected user and failure class.
5. Root-cause unresolved failures before patching.
6. If code changes are needed, add a focused failing regression first, implement a minimal fix, run targeted and relevant full tests, deploy only affected components.
7. Verify product semantics:
   - RS Telegram path must deliver a real RS monthly-ticket/control PNG from the intended RS app flow.
   - ViVi/ticket path must end at `sessionState=live`, `streamActive=true`, `streamVerdict=live`, `ticketState=live`, and `viviState=TICKET_DETAIL`.
   - Public `ticket.jolkins.id.lv` must be verified for ticket viewer changes.
8. Do not close an incident solely because a fake smoke passes after the user failure.

## Analytics thresholds

Current alerting thresholds:

- waiting RS QR job: 90 seconds
- running RS QR job: 85 seconds
- slow success: 60 seconds total
- retried success: any success with more than one attempt
- failed/canceled job: always an incident unless cancellation is explicit user cancellation

These thresholds are operational and may be tightened as latency work improves, but changes must be recorded here and covered by tests or watchdog verification.

## Architecture update notes

- Added a broker analytics endpoint and schemas so agents no longer need to parse raw job state for first-pass triage.
- Watchdog output now carries broker analytics summaries when available.
- The steward close-out bar is raised from "current health green" to "real affected users explained, fixed/recovered, and product semantics verified." 
