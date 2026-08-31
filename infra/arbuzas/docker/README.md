# kitty-gration Docker Layout

This directory is the main application Compose layout for the single-host
kitty-gration runtime. The existing 3X-UI/Xray project remains separate under
`../tiny-vless/`, but the same deployment script owns it as the first-class
`tiny_vless` service selector.

## What Lives Here

- `compose.yml`: the main Docker Compose project for apps, tunnels, the
  physical Pixel ticket bridge, the private qBittorrent slice, and its
  read-only Jellyfin media view.
- `env/arbuzas.example.env`: the operator template for hostnames, ports, and image pins.
- `images/`: Dockerfiles and entrypoints for the kitty-gration workloads and DNS sidecars.

The VPN is deliberately not added to `compose.yml`. Keeping its current
Compose project preserves its database, client identities, listeners, and
runtime naming while still bringing its source, local-first configuration,
backup, validation, firewall, limiter, Nginx, and Tailscale operations under
the same umbrella.

## Host Layout

- Persistent state: `/srv/arbuzas`
- Secrets and runtime env files: `/etc/arbuzas`
- Release bundles: `/etc/arbuzas/releases/<release-id>` with cleanup retaining current, rollback, and the newest 10 per release family
- Active release symlink: `/etc/arbuzas/current`

## Operator Entry Point

- Active deploy flow: `tools/arbuzas/deploy.sh`

Use the deployment script for deploys, validation, rollback, cleanup, and routine service changes. For direct inspection, SSH to kitty-gration and run Docker Compose against `/etc/arbuzas/current/release.env` and `/etc/arbuzas/current/infra/arbuzas/docker/compose.yml`, always keeping the Compose project name `arbuzas`.

`cleanup-docker` is a read-only preview by default. Add `--apply` only after reviewing its bounded release/image candidate summary. Successful standard/full deploys may apply the same policy automatically at most once per 24 hours; fast deploys and rollbacks never do.

Use the external selector for VPN-only work:

```bash
./tools/arbuzas/deploy.sh deploy --services tiny_vless --validation-profile standard --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services tiny_vless --validation-profile standard --ssh-host kitty-gration --ssh-user "$USER"
```

An unscoped deploy validates the VPN but does not restart it. Its canonical
configuration is `/etc/arbuzas/env/tiny-vless.env` plus
`/etc/arbuzas/secrets/tiny-vless/`; its live database remains at
`/opt/tiny-vless/db`, with SQLite-safe backups under
`/srv/arbuzas/tiny-vless/backups`. See `../tiny-vless/README.md` for recovery
and host-integration details.

Portainer was retired on 2026-07-20. There is no active Portainer container or listener on `9443`. Its former state is retained temporarily only as a restricted rollback archive matching `/srv/arbuzas/portainer-backups/portainer-retired-<timestamp>.tar.gz`; normal operations do not use it. Netdata runs separately as a host-native service with private Tailscale access. The live kitty-gration host must stay out of Docker Swarm, and the old Swarm and Pixel/orchestrator deployment paths are rollback-only legacy material.
The subscription bot and Mini App were retired on 2026-07-26. They are absent from the active Compose project and public tunnel set. Their source is recoverable through `archive/subscription-bot/README.md`, and the final private state is retained only in a restricted host retirement archive.
The ticket service talks privately and directly to `ticket_phone_bridge`, which owns the ADB connection to the physical Pixel. Stream desired state and commands are durable in SpacetimeDB; there is no ticket device lab or separate phone broker inside the production Compose project.

qBittorrent uses VueTorrent at `https://arbuzas-vps.tail9345a.ts.net:24680/` over Tailscale with no application login. Its Web listener is loopback-only, its peer traffic uses TCP and UDP `45123`, and its configuration plus payload live inside a capped 25 GiB filesystem. See `docs/runbooks/QBITTORRENT_TAILSCALE.md` for retention and deployment details.

Jellyfin uses `https://arbuzas-vps.tail9345a.ts.net:29096/` over Tailscale. Docker publishes its Web listener only at `127.0.0.1:18096`; Tailscale Serve supplies private HTTPS without using port `443` or Funnel. Jellyfin reads the qBittorrent payload through a read-only `/media` mount, while its configuration, artwork, temporary files, and disabled-by-policy transcode area remain disk-backed under `/srv/arbuzas/jellyfin`. The visible `Media` profile is passwordless; the hidden administrator password remains in the root-only host mirror secret and is not mounted into the container.
