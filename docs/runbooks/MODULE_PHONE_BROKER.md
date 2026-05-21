# phone_broker Module Runbook

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)

## Purpose

`phone_broker` is the private owner of the shared Pixel phone. Public ticket viewing goes through the broker and always has priority over Rigas Satiksme QR automation.

When ticket viewers are present, the broker holds lower-priority QR work in the queue. If a ticket viewer appears while a QR job is running, the broker asks the phone to cancel that job, marks it waiting again, and retries after the ticket page is free.

## Start / Validate

```bash
../../tools/arbuzas/deploy.sh deploy \
  --services ticket_phone_bridge,phone_broker,rigassatiksme_qr_bot,ticket_remote,ticket_remote_tunnel \
  --ssh-host arbuzas \
  --ssh-user ropepop

../../tools/arbuzas/deploy.sh validate \
  --services phone_broker,rigassatiksme_qr_bot,ticket_remote \
  --ssh-host arbuzas \
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
- Active ticket viewers still preempt QR work. A running QR job is canceled back to `waiting` with `ticket_active` and retried after the ticket page is free.
- RS jobs must complete with a `rigassatiksme_qr_result` image from the real Rīgas Satiksme monthly-ticket flow. `ticket_state_event` / `control_code_result` markers are not enough, and the broker must not synthesize a QR from the five submitted digits.
- Transient RS app/navigation and image-transport failures such as `qr_image_missing`, `rs_tickets_menu_missing`, `rs_manual_code_button_missing`, `rs_monthly_ticket_missing`, `rs_monthly_ticket_control_missing`, `rs_monthly_ticket_image_capture_failed`, `rs_monthly_ticket_flow_failed`, and `rs_monthly_ticket_flow_timeout` are retried up to 3 phone-result attempts before the bot reports failure. `ticket_active` preemptions do not consume that recovery budget; they only pause the RS job until the ticket priority window is clear.
