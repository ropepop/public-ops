# kitty-gration Docker Layout

This directory is the active production deployment layout for the single-host kitty-gration runtime.

## What Lives Here

- `compose.yml`: the one active Docker Compose project for Portainer, apps, tunnels, the physical Pixel ticket bridge, the private qBittorrent slice, and its read-only Jellyfin media view.
- `env/arbuzas.example.env`: the operator template for hostnames, ports, and image pins.
- `images/`: Dockerfiles and entrypoints for the kitty-gration workloads and DNS sidecars.

## Host Layout

- Persistent state: `/srv/arbuzas`
- Secrets and runtime env files: `/etc/arbuzas`
- Release bundles: `/etc/arbuzas/releases/<release-id>` with cleanup retaining current, rollback, and the newest 10 per release family
- Active release symlink: `/etc/arbuzas/current`

## Operator Entry Point

- Active deploy flow: `tools/arbuzas/deploy.sh`

Portainer runs directly against the local Docker socket on port `9443`. The live kitty-gration host must stay out of Docker Swarm, and the active repair flow now rewrites stale `tasks.agent` state in place before falling back to a clean first-run setup. The old Swarm and Pixel/orchestrator deployment paths are rollback-only legacy material.
The ticket service talks privately and directly to `ticket_phone_bridge`, which owns the ADB connection to the physical Pixel. Stream desired state and commands are durable in SpacetimeDB; there is no ticket device lab or separate phone broker inside the production Compose project.

qBittorrent uses VueTorrent at `https://arbuzas-vps.tail9345a.ts.net:24680/` over Tailscale with no application login. Its Web listener is loopback-only, its peer traffic uses TCP and UDP `45123`, and its configuration plus payload live inside a capped 25 GiB filesystem. See `docs/runbooks/QBITTORRENT_TAILSCALE.md` for retention and deployment details.

Jellyfin uses `https://arbuzas-vps.tail9345a.ts.net:29096/` over Tailscale. Docker publishes its Web listener only at `127.0.0.1:18096`; Tailscale Serve supplies private HTTPS without using port `443` or Funnel. Jellyfin reads the qBittorrent payload through a read-only `/media` mount, while its configuration, artwork, temporary files, and disabled-by-policy transcode area remain disk-backed under `/srv/arbuzas/jellyfin`. The visible `Media` profile is passwordless; the hidden administrator password remains in the root-only host mirror secret and is not mounted into the container.
