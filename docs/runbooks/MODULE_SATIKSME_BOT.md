# Kontrole Module

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Active runtime: Docker on kitty-gration
- Public host: `https://kontrole.info`
- Persistent state root: `/srv/arbuzas/satiksme-bot`
- Host env file: `/etc/arbuzas/env/satiksme-bot.env`

## Local Checks

```bash
cd workloads/satiksme-bot
make test
(cd web-client && npm run build:arrow)
make build
make spacetime-build
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

- Catalog and public bundle state now live under `/srv/arbuzas/satiksme-bot`.
- `/etc/arbuzas/env/satiksme-bot.env` should not define `SATIKSME_WEB_BIND_ADDR` or `SATIKSME_WEB_PORT`; kitty-gration Docker sets those at runtime.
- `/etc/arbuzas/env/satiksme-bot.env` must define `SATIKSME_WEB_PUBLIC_BASE_URL=https://kontrole.info`, `SATIKSME_WEB_TELEGRAM_BOT_USERNAME=<bot username>`, `SATIKSME_WEB_TELEGRAM_CLIENT_ID=<BotFather Web Login client id>`, and the normal `BOT_TOKEN` for Telegram Mini App signature checks.
- `satiksme_kontrole_bot` is a website wrapper. Its commands and menu button should send users to the Telegram Mini App URL `https://kontrole.info/app`; the public website remains `https://kontrole.info/`, and the public incident feed remains `https://kontrole.info/incidents`.
- `/app` loads Telegram's Mini App bridge and exchanges `Telegram.WebApp.initData` for the normal website session through `/api/v1/auth/telegram/complete`; `/` and `/incidents` do not preload the Mini App bridge.
- BotFather Web Login must allow `https://kontrole.info`; the browser sign-in path uses Telegram's current Login library and verifies the returned `id_token` on the server.
- Browser pages no longer use direct Spacetime auth. Keep `SATIKSME_RUNTIME_SPACETIME_ENABLED=true` for the backend data plane, and leave `SATIKSME_WEB_SPACETIME_ENABLED=false`.
- Interactive browser UI must use ArrowJS for changing map, incident list, detail, and status areas. After deploy, verify `document.documentElement.dataset.satiksmeUi === "arrow"` on the served page and check for new browser console errors.
- The active path is the repo-level Docker deploy flow, not workload-local Pixel helpers.
