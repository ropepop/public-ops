# Subscription Module

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Active runtime: Docker on kitty-gration
- Public host: `https://farel-subscription-bot.jolkins.id.lv`
- Persistent state root: `/srv/arbuzas/subscription-bot`
- Host env file: `/etc/arbuzas/env/subscription-bot.env`

## Local Checks

```bash
cd workloads/subscription-bot
go test ./...
(cd web-client && npm run build)
make build
make docker-image-build
```

## Deploy

```bash
./tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user "$USER"
```

## Validate

```bash
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host kitty-gration --ssh-user "$USER"
```

## Notes

- `/etc/arbuzas/env/subscription-bot.env` should not define `SUBSCRIPTION_BOT_WEB_BIND_ADDR` or `SUBSCRIPTION_BOT_WEB_PORT`; kitty-gration Docker sets those at runtime.
- Interactive mini app UI must use ArrowJS for changing launcher, billing, and operator/admin surfaces. After deploy, verify `document.documentElement.dataset.subscriptionUi === "arrow"` on the served page and check for new browser console errors.
