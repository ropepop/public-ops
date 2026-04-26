# Train App

Train bot, public web app, and Telegram entrypoints for the Arbuzas production stack.

## Runtime Shape

The active production runtime is Docker on Arbuzas:

1. A daily importer writes validated schedule snapshots.
2. The Go runtime serves the web app and Telegram bot.
3. Persistent state and snapshots live under `/srv/arbuzas/train-bot`.

## Local Development

```bash
make test
make scrape
make run
make build
make docker-image-build
```

## Important Runtime Config

```bash
SCHEDULE_DIR=/srv/arbuzas/train-bot/data/schedules
SCRAPER_OUTPUT_DIR=/srv/arbuzas/train-bot/data/schedules
TRAIN_WEB_ENABLED=true
TRAIN_WEB_PUBLIC_BASE_URL=https://train-bot.jolkins.id.lv
TRAIN_WEB_BUNDLE_DIR=/srv/arbuzas/train-bot/data/public-bundles
TRAIN_WEB_PUBLIC_EDGE_CACHE_STATE_FILE=/srv/arbuzas/train-bot/state/public-edge-cache.json
SINGLE_INSTANCE_LOCK_PATH=/srv/arbuzas/train-bot/run/train-bot.lock
```

## Agent Testing Login

TrainBot now has a fixed-user test login path for browser agents. It is opt-in, one-time, short-lived, and resets that fixed test user back to a clean baseline before the session is created.

Enable it with these runtime settings:

```bash
TRAIN_WEB_TEST_LOGIN_ENABLED=true
TRAIN_WEB_TEST_USER_ID=7001
TRAIN_WEB_TEST_TICKET_SECRET_FILE=/etc/arbuzas/secrets/train-bot-test-ticket.secret
TRAIN_WEB_TEST_TICKET_TTL_SEC=60
```

Mint the one-time link from the workload root:

```bash
make test-login-link
```

The command prints a `/app?test_ticket=...` URL. Agents should open that minted URL directly. They do not need Telegram for this path.

Full setup and troubleshooting steps live in [Agent test login](./docs/agent-test-login.md).

## Canonical Release Flow

1. Run `make test`.
2. Run `make scrape`.
3. Run `make build` or `make docker-image-build`.
4. Deploy with `../../tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"`.
5. Validate with `../../tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"`.
6. Confirm the public incidents homepage renders real content instead of the live-data outage screen.

## Runbooks

- [Release checklist](./docs/release-checklist.md)
- [Rollback checklist](./docs/rollback-checklist.md)
- [Daily freshness check](./docs/daily-freshness-check.md)
- [Missing schedule recovery](./docs/missing-schedule-recovery.md)
- [Agent test login](./docs/agent-test-login.md)

## Notes

- Public URLs stay unchanged.
- The old rooted Pixel deploy flow is rollback-only legacy material now.
- If train-bot loses outbound name resolution in Docker, fix the train-bot stack first before changing host-wide DNS.
