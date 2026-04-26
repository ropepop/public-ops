# Kontrole

Web-first Riga Satiksme control map and incident feed workload for the Arbuzas production stack.

## Local Development

```bash
cp .env.example .env
make test
make build
make spacetime-build
make docker-image-build
go run ./cmd/catalogsync
go run ./cmd/bot
```

## Active Deployment

The active production runtime is Docker on Arbuzas. Local workload commands stop at build and image preparation; deployment happens through the shared operator script:

```bash
../../tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"
../../tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"
```

## Important Runtime Paths

- Catalog source mirror: `/srv/arbuzas/satiksme-bot/data/catalog/source`
- Generated catalog: `/srv/arbuzas/satiksme-bot/data/catalog/generated/catalog.json`
- Public bundles: `/srv/arbuzas/satiksme-bot/data/public-bundles`
- State: `/srv/arbuzas/satiksme-bot/state`

## Notes

- Anonymous visitors can browse the map and incidents; Telegram login unlocks reporting, voting, and commenting.
- The website now uses Telegram's official popup login callback flow: the page fetches `/api/v1/auth/telegram/config`, receives an `id_token` from Telegram in place, and finishes the site session through `/api/v1/auth/telegram/complete`.
- Browser pages no longer talk to Spacetime directly; the site uses its own JSON API while the backend keeps Spacetime as the live data store.
- The old Pixel deploy helpers are rollback-only legacy material.
