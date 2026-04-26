# Kontrole Module

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Active runtime: Docker on Arbuzas
- Public host: `https://kontrole.info`
- Persistent state root: `/srv/arbuzas/satiksme-bot`
- Host env file: `/etc/arbuzas/env/satiksme-bot.env`

## Local Checks

```bash
cd workloads/satiksme-bot
make test
make build
make spacetime-build
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

## Notes

- Catalog and public bundle state now live under `/srv/arbuzas/satiksme-bot`.
- `/etc/arbuzas/env/satiksme-bot.env` should not define `SATIKSME_WEB_BIND_ADDR` or `SATIKSME_WEB_PORT`; Arbuzas Docker sets those at runtime.
- `/etc/arbuzas/env/satiksme-bot.env` must define `SATIKSME_WEB_PUBLIC_BASE_URL=https://kontrole.info`, `SATIKSME_WEB_TELEGRAM_BOT_USERNAME=<bot username>`, and the normal `BOT_TOKEN` for Telegram signature checks.
- BotFather Web Login must link `https://kontrole.info` to the bot so the Telegram web login button is allowed to complete there.
- Browser pages no longer use direct Spacetime auth. Keep `SATIKSME_RUNTIME_SPACETIME_ENABLED=true` for the backend data plane, and leave `SATIKSME_WEB_SPACETIME_ENABLED=false`.
- The active path is the repo-level Docker deploy flow, not workload-local Pixel helpers.
