# Subscription Module

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Active runtime: Docker on Arbuzas
- Public host: `https://farel-subscription-bot.jolkins.id.lv`
- Persistent state root: `/srv/arbuzas/subscription-bot`
- Host env file: `/etc/arbuzas/env/subscription-bot.env`

## Local Checks

```bash
cd workloads/subscription-bot
go test ./...
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

## Notes

- `/etc/arbuzas/env/subscription-bot.env` should not define `SUBSCRIPTION_BOT_WEB_BIND_ADDR` or `SUBSCRIPTION_BOT_WEB_PORT`; Arbuzas Docker sets those at runtime.
