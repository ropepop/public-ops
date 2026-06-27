# kitty-gration Docker + Portainer

This is the detailed operator runbook for the active kitty-gration runtime.

## Files

- Active layout: `infra/arbuzas/docker/`
- Host Netdata config: `infra/arbuzas/netdata/`
- Active deploy entrypoint: `tools/arbuzas/deploy.sh`
- Tunnel config renderer: `tools/arbuzas/render_cloudflared_config.py`

## Initial Setup

1. Pull the current host variables/secrets into the local plaintext mirror:
   ```bash
   ./tools/arbuzas/deploy.sh mirror-pull --ssh-host kitty-gration --ssh-user ropepop
   ```
2. Edit deployment variables and secrets under `infra/arbuzas/host-mirror/` first. Use `mirror-audit` before overwriting host drift, and use `deploy-config` for config-only updates that should avoid rebuilds and release uploads.
3. Copy `infra/arbuzas/docker/env/arbuzas.example.env` to a private local env file if you need operator-only CLI overrides.
4. Make sure kitty-gration has Docker with the Compose plugin, Python 3, and SSH access.
5. Make sure these host files exist, preferably via the local mirror:
   - `/etc/arbuzas/env/train-bot.env`
   - `/etc/arbuzas/env/satiksme-bot.env`
   - `/etc/arbuzas/env/subscription-bot.env`
   - `/etc/arbuzas/cloudflared/train-bot.json`
   - `/etc/arbuzas/cloudflared/satiksme-bot.json`
   - `/etc/arbuzas/cloudflared/subscription-bot.json`
6. Do not set `*_WEB_BIND_ADDR` or `*_WEB_PORT` in the Train, Satiksme, or Subscription host env files. Do not set `TRAIN_WEB_PUBLIC_BASE_URL` in the Train host env file. Docker Compose owns those runtime values on kitty-gration.

## Normal Release Flow

Deploy the current repo state:

```bash
./tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user "$USER"
```

Deploy only one service or a few services:

```bash
./tools/arbuzas/deploy.sh deploy --services train_bot,subscription_bot --ssh-host kitty-gration --ssh-user "$USER"
```

Notes for targeted updates:

- `--services` is available only for `deploy` and `validate`.
- Service names use the Compose service names from `infra/arbuzas/docker/compose.yml`.
- `train_bot`, `satiksme_bot`, and `subscription_bot` automatically bring along their matching tunnel service so the public route stays aligned.
- `site-notifications` is kept in the repo for reference and testing, but it is not part of the active kitty-gration deploy set.
- Targeted validation checks the slice you touched instead of forcing a full-stack validation pass.

Validate an existing release:

```bash
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services train_bot,subscription_bot --ssh-host kitty-gration --ssh-user "$USER"
```

Run the cleanup policy without deploying:

```bash
./tools/arbuzas/deploy.sh cleanup-docker --ssh-host kitty-gration --ssh-user "$USER"
```

Report the corrected host memory pressure without deploying or flushing cache:

```bash
./tools/arbuzas/deploy.sh memory-report --ssh-host kitty-gration --ssh-user "$USER"
```

Install or refresh the host-native corrected memory report service:

```bash
./tools/arbuzas/deploy.sh install-memory-report --ssh-host kitty-gration --ssh-user "$USER"
```

Re-run the memory report service checks without reinstalling it:

```bash
./tools/arbuzas/deploy.sh validate-memory-report --ssh-host kitty-gration --ssh-user "$USER"
```

Push only local mirror changes and restart/reload affected services without rebuilding or uploading a release bundle:

```bash
./tools/arbuzas/deploy.sh deploy-config --ssh-host kitty-gration --ssh-user "$USER"
```

Repair a stale or broken Portainer install on the active host:

```bash
./tools/arbuzas/deploy.sh repair-portainer --ssh-host kitty-gration --ssh-user "$USER"
```

Install or refresh the host-native Netdata setup:

```bash
./tools/arbuzas/deploy.sh install-netdata --ssh-host kitty-gration --ssh-user "$USER"
```

Re-run the Netdata checks without reinstalling it:

```bash
./tools/arbuzas/deploy.sh validate-netdata --ssh-host kitty-gration --ssh-user "$USER"
```

Install or refresh the host-native ThinkPad fan controller:

```bash
./tools/arbuzas/deploy.sh install-thinkpad-fan --ssh-host kitty-gration --ssh-user "$USER"
```

Re-run the fan-controller checks without reinstalling it:

```bash
./tools/arbuzas/deploy.sh validate-thinkpad-fan --ssh-host kitty-gration --ssh-user "$USER"
```

## What Deploy Does

- prepares a minimal release bundle under `/etc/arbuzas/releases/<release-id>`
- renders Cloudflare tunnel configs inside that release bundle
- updates `/etc/arbuzas/current`
- runs `docker compose -p arbuzas up -d --build`
- when `--services` is set, rebuilds and restarts only the requested services instead of the full stack
- validates Portainer, apps, and tunnels
- prunes unused Docker images after they have stayed unprotected for 7 days
- prunes old release bundles beyond the newest 10 per release family
- prunes Docker build cache older than 7 days
- runs gentle host cache cleanup for package caches, narrow old `/tmp` scratch, and journals
- flushes reclaimable Linux memory cache after cleanup so provider memory graphs fall back quickly

The normal Docker release flow does not install or update Netdata. Netdata is a separate host-maintenance action.
The corrected memory report service is also a separate host-maintenance action.
The ThinkPad fan controller is also a separate host-maintenance action.

## Rollback

```bash
./tools/arbuzas/deploy.sh rollback --release-id "<previous-release-id>" --ssh-host kitty-gration --ssh-user "$USER"
```

Rollback re-runs the same post-validation cleanup policy after the host is healthy again.

## Cleanup

The active kitty-gration runtime now applies cleanup in three ways:

- automatically after a successful `deploy`
- automatically after a successful `rollback`
- manually through `./tools/arbuzas/deploy.sh cleanup-docker`

What the cleanup protects:

- any image still referenced by a container, even if that container is stopped
- all `arbuzas/*:<release-id>` images for the current release
- all `arbuzas/*:<release-id>` images for one rollback slot: the newest non-current release directory under `/etc/arbuzas/releases`
- the current release bundle and newest rollback release bundle
- the newest 10 release bundles per release family under `/etc/arbuzas/releases`

What the cleanup removes:

- any other unused image only after it has stayed unused and unprotected for 7 days
- older release bundles beyond the protected current, rollback, and newest 10 per family set
- Docker build cache older than 7 days
- package-manager cache through `apt-get clean`
- old kitty-gration scratch files in `/tmp` that match narrow known patterns
- systemd journals beyond the configured cap, default `100M`
- reclaimable in-memory Linux page, directory, and inode cache through `drop_caches=3`

Dropping reclaimable memory cache affects provider memory charts and warm file reads, not live application memory.
The host may rebuild cache naturally after Docker builds, validation, or busy app traffic.

What the cleanup does not touch:

- containers
- volumes
- networks
- Portainer data or backups
- application state under `/srv/arbuzas/*`

Implementation notes for operators:

- Cleanup state is tracked under `/etc/arbuzas/docker-gc/state.json`.
- Release bundle retention defaults to `DOCKER_GC_RELEASE_KEEP_PER_FAMILY=10`.
- Host scratch retention defaults to `ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS=7`.
- Journal cleanup defaults to `ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE=100M`.
- Reclaimable memory cache flushing defaults to enabled; set `ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE=false` to skip it for one run.
- If the cleanup state file is missing or corrupted, kitty-gration recreates it and starts a fresh 7-day countdown instead of deleting newly eligible images immediately.
- If automatic cleanup fails after a successful deploy or rollback, the release still stays successful and the cleanup failure is logged as a warning.
- Manual `cleanup-docker` fails loudly if the cleanup itself cannot complete.

## Memory Reporting

The VPS provider's "Memory Utilization" graph is not the source of truth for real pressure on kitty-gration. The observed provider line matches this cached-inclusive calculation:

```text
(MemTotal - MemFree - Buffers) / MemTotal
```

That formula counts much of the Linux file/slab cache as used memory. Linux normally keeps RAM warm for recently read files and releases that cache when applications need memory, so the provider line can stay high even when the machine is not under pressure.

Use this formula for real pressure:

```text
(MemTotal - MemAvailable) / MemTotal
```

Use this command for the canonical live number:

```bash
./tools/arbuzas/deploy.sh memory-report --ssh-host kitty-gration --ssh-user "$USER"
```

The report shows three separate values:

- real pressure using `MemAvailable`
- cached/reclaimable memory shown separately
- the provider-style cached-inclusive percentage for comparison with the VPS panel

The canonical host source is the `arbuzas-memory-report.timer` service. Install it with `install-memory-report`; it publishes the latest corrected snapshot every minute under `/var/lib/arbuzas/memory-report/`:

- `latest.json`
- `latest.txt`
- `latest.prom`

The provider memory panel is useful only as a cached-inclusive comparison line. Cleanup still flushes reclaimable cache as a cosmetic fallback for that provider panel, but the flush is not proof of application memory pressure.

Do not disable `qemu-guest-agent` to change the provider graph. The agent does not expose the provider's memory-utilization formula, and disabling it can break provider-side guest operations such as shutdown/reboot handling or console metadata.

## Portainer Repair

Use `repair-portainer` when Portainer is carrying stale Swarm-era state, such as a saved `tasks.agent` endpoint from the old deployment path.

What the repair does:

- confirms the current kitty-gration Compose stack is healthy before changing Portainer
- refuses to continue if any Docker Swarm services or stacks are still active
- stops only the Portainer container
- archives `/srv/arbuzas/portainer` into `/srv/arbuzas/portainer-backups/portainer-<timestamp>.tar.gz`
- rewrites the saved Portainer endpoint from `tcp://tasks.agent:9001` to the local Docker socket where possible
- compacts the Portainer database so the stale agent address is removed from saved state
- runs `docker swarm leave --force` so the live host returns to standalone Docker
- starts Portainer again through the active Compose project
- re-runs validation for Portainer, apps, tunnels, and the standalone-host baseline

Important consequences:

- The normal repair path is intended to preserve existing Portainer users, endpoints, and settings.
- If there is no existing Portainer database to repair, Portainer comes back at first-run setup on `https://<host>:9443`.
- The backup under `/srv/arbuzas/portainer-backups/` is the rollback point for Portainer-only recovery.
- Validation now fails if the live host is still in Swarm mode or if Portainer state still contains `tcp://tasks.agent:9001`.

Portainer-only rollback:

1. Stop the Portainer container with the active Compose project.
2. Replace `/srv/arbuzas/portainer` with the desired backup from `/srv/arbuzas/portainer-backups/`.
3. Start the Portainer container again through the active Compose project.

## Netdata Host Observability

The kitty-gration Netdata setup is intentionally host-native, not a Compose service.

What `install-netdata` does:

- installs `lm-sensors` and `smartmontools`
- runs the official Netdata installer in stable, native-package, non-interactive mode
- keeps Netdata auto-updates and anonymous telemetry disabled
- syncs the repo-managed config from `infra/arbuzas/netdata/`
- keeps Netdata's Docker collector and Docker-backed service discovery disabled on kitty-gration so Docker itself is not polled during normal host monitoring
- restarts Netdata so it binds only to `localhost:19999`
- publishes a private TCP forward on the host through `tailscale serve`
- validates the local API, Tailscale access, and expected kitty-gration hardware charts

Access pattern:

- local host listener: `http://127.0.0.1:19999`
- operator access: `http://<arbuzas-tailnet-ip>:19999`
- there is no Cloudflare route for Netdata
- there is no Portainer plugin dependency
- there is no Netdata Cloud claim in the kitty-gration baseline

## ThinkPad Fan Control

The ThinkPad fan controller script and the `install-thinkpad-fan` deploy action are preserved in the repo at `infra/arbuzas/thinkpad-fan/`.

This is **not active on the live kitty-gration host** and is not applicable to it. The live host is an Intel Xeon Gold 6142 server, not a ThinkPad. The `thinkpad_acpi` kernel module is not loaded, the `arbuzas-thinkpad-fan.service` systemd unit is not installed, and the controller will not run on this hardware. Running `install-thinkpad-fan` on the live host will fail.

The `install-thinkpad-fan` and `validate-thinkpad-fan` deploy actions are kept for a future ThinkPad-based host only.

## Boost-Off Hook

The boost-off reboot hook script and the `install-netdata`-style helper are preserved in the repo at `infra/arbuzas/cpu-boost-off/`.

This is **not active on the live kitty-gration host** either. The `crontab` binary is not installed on the host, no `@reboot` crontab entry exists for the `ropepop` user, no script is installed at `/home/ropepop/.local/bin/arbuzas-disable-boost.sh`, and no log file is written. The kernel does not expose `/sys/devices/system/cpu/intel_pstate` on this Xeon server, so even installing the script would not have an observable effect.

Observed CPU clock stays at the 2.6 GHz base clock with no turbo visible, regardless of the script. The corrected memory report output confirms this.

## Netdata Status

Netdata is **not installed** on the live kitty-gration host. The `install-netdata` and `validate-netdata` deploy actions are preserved for when Netdata is wanted; today there is no `netdata` systemd unit on the host.

## Notes

- Portainer connects directly to the local Docker socket.
- Netdata lives on the host outside the `arbuzas` Compose project.
- The active runtime is one Compose project named `arbuzas`.
- kitty-gration does not run a public DNS service. The host ports `443` and `853` are free; the `dns_controlplane` service was retired on 2026-06-21 (see `archive/dns-controlplane/`).
- The live kitty-gration host must stay out of Docker Swarm. Validation now fails if Swarm is still enabled or if Portainer state still references `tasks.agent`.
- Swarm and rooted Pixel deployment paths are rollback-only legacy material.
- If the live host still carries old localhost-only web bind or port values from the Pixel era, remove those keys from `/etc/arbuzas/env/*.env` before the next deploy.
