# phone_broker Module Runbook

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)

## Purpose

`phone_broker` is the private owner of the shared Pixel phone. Public ticket viewing goes through the broker and always has priority over Rigas Satiksme QR automation.

When ticket viewers are present, the broker holds lower-priority QR work in the queue. If a ticket viewer appears while a QR job is running, the broker asks the phone to cancel that job, marks it waiting again, and retries after the ticket page is free.

## Start / Validate

```bash
../../tools/arbuzas/deploy.sh deploy \
  --services ticket_phone_bridge,phone_broker,rigassatiksme_qr_bot,ticket_remote,ticket_remote_tunnel \
  --ssh-host kitty-gration \
  --ssh-user ropepop

../../tools/arbuzas/deploy.sh validate \
  --services phone_broker,rigassatiksme_qr_bot,ticket_remote \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```

## Local Checks

```bash
docker compose --project-name arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T phone_broker curl -fsS http://127.0.0.1:9398/api/v1/health
docker compose --project-name arbuzas --env-file /etc/arbuzas/current/release.env -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml exec -T phone_broker curl -fsS http://127.0.0.1:9398/api/v1/state
```

`/api/v1/state` shows the current phone owner, ticket viewer count, and Rigas Satiksme queue depth. It is internal only.

## Rigas Satiksme QR Bot

The bot runs as `rigassatiksme_qr_bot` and reads its Telegram token from `/etc/arbuzas/env/rigassatiksme-qr-bot.env`.

Required setting:

```bash
RIGASATIKSME_QR_BOT_TOKEN=replace-with-telegram-token
```

The bot accepts `/start`, `/status`, `/cancel`, `/access`, `/qr <five digits>`, and exactly one bare 5-digit code when Telegram delivers non-command messages. Use `/qr <five digits>` in groups so BotFather privacy mode still delivers the request. Completed jobs send only the QR image back to the requester.

## QR Reliability Contract

- `phone_broker` serializes RS jobs against the shared Pixel; jobs are FIFO when no ticket viewer is actively using the phone.
- Active ticket viewers and ticket leases preempt QR work for their full active duration. A running QR job is canceled back to `waiting` with `ticket_active` or `ticket_lease_active` and retried after the ticket page/control-code lease is free.
- RS jobs must complete with a `rigassatiksme_qr_result` image from the real Rīgas Satiksme monthly-ticket flow. `ticket_state_event` / `control_code_result` markers are not enough, and the broker must not synthesize a QR from the five submitted digits.
- RS job analytics may include safe phone phase summaries from the Pixel result message: source app, ticket flow, total phone duration, and named phase timings. These summaries are for incident tracing only and must not include RS codes, raw Telegram IDs, chat IDs, tokens, cookies, or session values.
- Only explicitly recoverable RS transport failures such as `phone_timeout` and `qr_image_missing` get one broker retry at most. Pixel semantic failures such as `code_rejected_by_rs`, `rs_manual_code_button_missing`, `rs_monthly_ticket_stale_code`, `rs_monthly_ticket_unknown_state`, and `rs_monthly_ticket_image_capture_failed` are preserved as named final outcomes unless the Pixel reports a retryable reason. `ticket_active` and `ticket_lease_active` preemptions do not consume recovery budget; they only pause the RS job until ticket priority is gone.
- RS operational target is a final QR image or named final failure in 15 seconds or less on average. `ticket.jolkins.id.lv` operational target is authenticated load to live ViVi ticket in 5 seconds or less, with current stream delay measured from relay/Pixel frame age and kept below the existing freshness threshold.
- The public ticket relay may retain its startup prewarm viewer for up to the same 5-second load budget. This prevents the phone stream from being started by the authenticated index shell and then dropped before the browser's real video/control sockets attach.
