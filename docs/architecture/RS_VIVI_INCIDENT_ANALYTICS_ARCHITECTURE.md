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
- `slowSuccess` currently means successful RS QR delivery took at least 15 seconds end-to-end; this is the per-job incident signal that supports the 15-second average target.
- `userImpact` groups all retained broker jobs by safe actor hash so watchdog/steward runs can start from affected users instead of aggregate health.
- `recentIncidents` includes failures, non-user cancellations, running jobs, retried jobs, and slow successes.
- `recentIncidents[].phone` may include safe Pixel phase timings such as source app, ticket flow, total duration, and per-phase milliseconds. It must not include RS codes, raw user IDs, tokens, cookies, or session values.
- RS monthly-ticket reasons must preserve Pixel's named phone result when one exists. `phone_timeout` means no final Pixel outcome arrived before the broker deadline; it must not replace reasons such as `rs_phone_automation_unavailable`, `rs_app_launch_failed`, `rs_app_foreground_failed`, `wrong_code`, `code_rejected_by_rs`, `rs_monthly_ticket_missing`, `rs_manual_code_field_missing`, `rs_manual_code_button_missing`, `rs_monthly_ticket_stale_code`, `rs_monthly_ticket_unknown_state`, `rs_monthly_ticket_state_timeout`, or `rs_monthly_ticket_image_capture_failed`. Non-critical cleanup may be delayed briefly after a final result so rapidly arriving RS work can stay inside the warm RS app.
- Public ticket viewer presence and ticket leases are hard phone-priority signals, not bounded grace windows. While an authenticated viewer is present or a ViVi control-code lease is active, broker desired owner is `ticket`, queued RS jobs stay waiting, and a running RS job is canceled back to waiting without consuming RS retry budget. During that time the phone must remain on ViVi/ticket work rather than RS.
- Current operational timing targets are: public `ticket.jolkins.id.lv` load to live ViVi ticket in 5 seconds or less, RS final image or named final failure in 15 seconds or less on average, and public stream frame delay remaining within the existing live freshness threshold (`visibleFrame`/`directStream` last-frame age at or below 1500 ms during close-out).
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
- slow success: 15 seconds total
- retried success: any success with more than one attempt
- failed/canceled job: always an incident unless cancellation is explicit user cancellation

These thresholds are operational and may be tightened as latency work improves, but changes must be recorded here and covered by tests or watchdog verification.

## Architecture update notes

- 2026-05-26: Automated RS Telegram stress tests must be target-locked. Agents must prove the selected Telegram chat header is the `rs biļete` bot before every typed send; if proof fails, the test must abort and save evidence. Coordinate-only Telegram typing is not allowed for RS QR testing.
- 2026-05-26: RS manual-code entry has one bounded safe re-entry attempt. Pixel may refocus and re-enter while the manual popup remains proven open, but it still must not tap OK until the requested digits are visible. If the popup disappears before digit proof, the failure is `rs_app_attention_required`; if digits remain unproven after the retry, the failure is `rs_manual_code_entry_unverified`.
- 2026-05-26: RS monthly-ticket startup treats an already-open old control-ticket QR as a recoverable app state, not as proof of a stale final result. Pixel must try a visible RS close/back control, then Back, then no-data-loss relaunch, rechecking the UI after every action. It must not enter digits until a safe register/manual-entry path is proven. Only a stale QR observed after a verified new-code submit remains `rs_monthly_ticket_stale_code`; startup screens that cannot be cleared are `rs_app_attention_required`.
- 2026-05-23: RS QR analytics now preserve safe phone-side phase summaries from Pixel result messages. This keeps broker triage aligned with the phone as source of truth while keeping sensitive values out of watchdog and steward output.
- 2026-05-23: Broker ticket priority is hard while public viewers or ticket leases are active. The old bounded viewer-priority window is not the production contract; RS must wait or be preempted for the full ticket presence/lease duration, and `ticket_lease_active` preemptions must not burn RS retry attempts.
- 2026-05-23: RS monthly-ticket work is batch-aware. Broker commands include an `rsQueueHint` with pending RS demand and ticket-priority state; Pixel sends each final RS result before ViVi cleanup, routes non-critical cleanup through an idle delay so a rapidly arriving next RS job can continue in RS, uses semantic RS field focus before non-touch digit entry when Flutter accessibility text replacement is ineffective, activates Flutter controls from matched labels with parent-bound fallback, uses small bounded settle windows after stale-ticket back navigation and Flutter screen changes, retries visible semantic controls after transient failed activation, and public ticket priority still preempts RS. After submit, a stale control ticket that does not prove the requested digits is a final `rs_monthly_ticket_stale_code` result rather than an unknown reset loop; returning to the manual-code choice after submit is `code_rejected_by_rs` rather than a retried/stalled input loop. Broker health polling reconciles named Pixel failures while the control socket is open and reconciles image-missing generated health only after socket disconnect; an open control socket must wait for the explicit `rigassatiksme_qr_result` image so health cannot race a still-arriving screenshot into `qr_image_missing`.
- Added a broker analytics endpoint and schemas so agents no longer need to parse raw job state for first-pass triage.
- Watchdog output now carries broker analytics summaries when available.
- The steward close-out bar is raised from "current health green" to "real affected users explained, fixed/recovered, and product semantics verified." 
