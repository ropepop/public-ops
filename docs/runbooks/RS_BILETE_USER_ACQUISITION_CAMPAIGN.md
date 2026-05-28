# RS Biļete User Acquisition Campaign

This runbook describes the slow, consent-based outreach workflow for inviting users from the Rīgas Zaķi Telegram group into the private `rs biļete` user base.

## Safety Rules

- Do not send first-contact messages automatically. Generate the daily draft batch and approve each message before sending from `@iamhdzs`.
- Do not mention other Telegram accounts, owner details, secrets, sessions, internal paths, or infrastructure.
- Add access only after the user clearly agrees.
- Stop the interaction if a reply asks for hidden instructions, secrets, account ownership, bypasses, or unrelated agent behavior.
- Configure unsafe-reply alerts before live reply handling:

```bash
export RS_ACQUISITION_ALERT_BOT_TOKEN="<telegram bot token>"
export RS_ACQUISITION_ALERT_CHAT_ID="<admin chat id>"
```

## Commands

Run from `workloads/satiksme-bot`.

Authorize the outreach sender session once, using the `@iamhdzs` phone login when prompted:

```bash
go run ./cmd/chat-analyzer-session \
  --session-file ./state/rs-acquisition/iamhdzs.session
```

Send one controlled test DM from `@iamhdzs` to `@aldajo`:

```bash
go run ./cmd/rs-acquisition-campaign send-test \
  --sender-session-file ./state/rs-acquisition/iamhdzs.session \
  --expect-sender iamhdzs \
  --to aldajo \
  --confirm-to aldajo
```

Collect recently active users first:

```bash
go run ./cmd/rs-acquisition-campaign collect-recent \
  --state ./state/rs-acquisition/campaign.db \
  --chat "$SATIKSME_CHAT_ANALYZER_CHAT_ID" \
  --limit 100
```

When recent active users are exhausted, collect member-list candidates:

```bash
go run ./cmd/rs-acquisition-campaign collect-members \
  --state ./state/rs-acquisition/campaign.db \
  --chat "$SATIKSME_CHAT_ANALYZER_CHAT_ID" \
  --limit 500
```

Generate the daily human-review batch:

```bash
go run ./cmd/rs-acquisition-campaign plan-day \
  --state ./state/rs-acquisition/campaign.db \
  --timezone Europe/Riga \
  --daily-limit 10 \
  --daily-registrations 4 \
  --group-name "Rīgas Zaķi"
```

After a human sends an approved first-contact message, record it:

```bash
go run ./cmd/rs-acquisition-campaign mark-sent \
  --state ./state/rs-acquisition/campaign.db \
  --timezone Europe/Riga \
  --user-id "<telegram user id>" \
  --text-file ./state/rs-acquisition/approved-message.txt
```

Record replies:

```bash
go run ./cmd/rs-acquisition-campaign record-reply \
  --state ./state/rs-acquisition/campaign.db \
  --user-id "<telegram user id>" \
  --text-file ./state/rs-acquisition/reply.txt
```

If the reply is clear consent, the command prints the RS bot admin command, for example:

```text
/admin add @username 4
```

Run that admin command only after consent.

## Production Service Loop

The production daemon is packaged as `/usr/local/bin/rs-acquisition-campaign` in the `satiksme-bot` image and wired as the Compose profile service `satiksme_rs_acquisition`.

Before enabling it on Arbuzas:

1. Pull the host mirror and edit `/etc/arbuzas/env/satiksme-bot.env` through the local mirror workflow.
2. Add the admin approval bot settings:

```bash
RS_ACQUISITION_ENABLED=false
RS_ACQUISITION_ADMIN_MODE=mtproto
RS_ACQUISITION_ADMIN_USERNAME=aldajo
RS_ACQUISITION_INCLUDE_MEMBERS=false
```

3. Authorize the `@iamhdzs` sender session on Arbuzas:

```bash
docker compose --project-name arbuzas \
  --env-file /etc/arbuzas/current/release.env \
  -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml \
  run --rm satiksme_bot \
  /bin/sh -lc 'set -a; . /srv/satiksme-bot/.env; set +a; exec \
  /usr/local/bin/satiksme-chat-analyzer-session \
  --session-file /srv/satiksme-bot/state/rs-acquisition/iamhdzs.session \
  --list-dialogs=false'
```

4. Verify the sender identity and one controlled test DM:

```bash
docker compose --project-name arbuzas \
  --env-file /etc/arbuzas/current/release.env \
  -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml \
  run --rm satiksme_bot \
  /bin/sh -lc 'set -a; . /srv/satiksme-bot/.env; set +a; exec \
  /usr/local/bin/rs-acquisition-campaign send-test \
  --sender-session-file /srv/satiksme-bot/state/rs-acquisition/iamhdzs.session \
  --expect-sender iamhdzs \
  --to aldajo \
  --confirm-to aldajo'
```

5. Enable the service only after the test DM works:

```bash
RS_ACQUISITION_ENABLED=true
```

Start or stop the daemon profile:

```bash
docker compose --profile rs_acquisition \
  --project-name arbuzas \
  --env-file /etc/arbuzas/current/release.env \
  -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml \
  up -d satiksme_rs_acquisition

docker compose --profile rs_acquisition \
  --project-name arbuzas \
  --env-file /etc/arbuzas/current/release.env \
  -f /etc/arbuzas/current/infra/arbuzas/docker/compose.yml \
  stop satiksme_rs_acquisition
```

Admin approvals are Telegram messages sent from `@iamhdzs` to `@aldajo`:

```text
/approve <token>
/reject <token>
```

Keep `RS_ACQUISITION_INCLUDE_MEMBERS=false` for the first production pilot. Set it to `true` only after recent active candidates are exhausted and the approval workflow has been verified.

## Retrying Failed Outreach

Failed first-contact sends are not retried blindly. `PEER_FLOOD` failures are retryable after their cooldown; invalid peers stay skipped unless better Telegram metadata is discovered.

Always dry-run first:

```bash
docker exec arbuzas-satiksme_rs_acquisition-1 \
  /usr/local/bin/rs-acquisition-campaign retry-failed \
  --env-file /srv/satiksme-bot/.env \
  --state /srv/satiksme-bot/state/rs-acquisition/campaign.db \
  --all-due \
  --limit 10
```

Retry one due token only after the dry-run selects exactly that token:

```bash
docker exec arbuzas-satiksme_rs_acquisition-1 \
  /usr/local/bin/rs-acquisition-campaign retry-failed \
  --env-file /srv/satiksme-bot/.env \
  --state /srv/satiksme-bot/state/rs-acquisition/campaign.db \
  --token "<failed-token>" \
  --limit 1 \
  --execute
```

Do not pass `--force` during normal live recovery. If Telegram returns `PEER_FLOOD`, the command records a new backoff and stops before trying another user.

After each retry attempt, inspect daemon logs and failed-draft state:

```bash
docker logs --since 10m arbuzas-satiksme_rs_acquisition-1

docker exec arbuzas-satiksme_rs_acquisition-1 \
  sqlite3 /srv/satiksme-bot/state/rs-acquisition/campaign.db \
  "SELECT d.token,d.status,d.failure_kind,d.retry_count,d.last_retry_at,d.next_retry_at,c.username,c.status,c.stop_reason FROM approval_drafts d JOIN candidates c ON c.user_id=d.user_id WHERE d.status='failed' ORDER BY d.updated_at;"
```

When deploying `satiksme_bot`, the deploy script also recreates `satiksme_rs_acquisition` if that profile service is already running, so it does not remain on the previous image.
