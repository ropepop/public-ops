# Train Module

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Active runtime: Docker on Arbuzas
- Public host: `https://train-bot.jolkins.id.lv`
- Persistent state root: `/srv/arbuzas/train-bot`
- Host env file: `/etc/arbuzas/env/train-bot.env`

## Local Checks

```bash
cd workloads/train-bot
make test
make scrape
make build
make docker-image-build
```

## Deploy

```bash
./tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"
```

## Validate

```bash
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"
```

## Agent Test Login

This module supports an operator-only browser bootstrap for agent testing. It is for one fixed test user only, uses one-time links, and expires quickly.

Reference doc:

- [TrainBot agent test login](../../workloads/train-bot/docs/agent-test-login.md)

Enable it in `/etc/arbuzas/env/train-bot.env`:

```bash
TRAIN_WEB_TEST_LOGIN_ENABLED=true
TRAIN_WEB_TEST_USER_ID=7001
TRAIN_WEB_TEST_TICKET_SECRET_FILE=/etc/arbuzas/secrets/train-bot-test-ticket.secret
TRAIN_WEB_TEST_TICKET_TTL_SEC=60
```

Mint the link from the workload root:

```bash
cd workloads/train-bot
make test-login-link
```

Expected operator flow:

1. Enable the env values and deploy or restart TrainBot.
2. Run `make test-login-link`.
3. Hand the printed `/app?test_ticket=...` URL to the agent.
4. The agent opens that URL directly and lands in `/app` already signed in.

Disable it by setting `TRAIN_WEB_TEST_LOGIN_ENABLED=false` and redeploying or restarting the module.

Guardrails:

- Fixed user only. There is no arbitrary impersonation path in v1.
- One-time use only. Reusing the same link should fail.
- Short TTL only. Keep `TRAIN_WEB_TEST_TICKET_TTL_SEC` low.
- Test-only purpose. Do not hand these links to normal users.

Secret rotation:

1. Write a new value to the file referenced by `TRAIN_WEB_TEST_TICKET_SECRET_FILE`.
2. Restart or redeploy TrainBot so the new secret is loaded.
3. Discard any previously minted links.

Troubleshooting:

- `not found`: test login is not enabled on the running server.
- `missing` or `invalid` ticket: the agent did not open the full minted URL.
- `expired` ticket: mint a fresh link and retry quickly.
- `already used` ticket: the link was consumed once already; mint a new one.
- Unexpected user state after login: confirm the agent used the test-login path, not Telegram auth.

## Notes

- The schedule directory now lives under `/srv/arbuzas/train-bot/data/schedules`.
- `/etc/arbuzas/env/train-bot.env` should not define `TRAIN_WEB_BIND_ADDR` or `TRAIN_WEB_PORT`; Arbuzas Docker sets those at runtime.
- Rollback uses the repo-level deploy script, not workload-local Pixel helpers.
