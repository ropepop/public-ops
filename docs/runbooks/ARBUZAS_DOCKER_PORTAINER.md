# Arbuzas Docker + Portainer

This is the detailed operator runbook for the active Arbuzas runtime.

## Files

- Active layout: `infra/arbuzas/docker/`
- Host Netdata config: `infra/arbuzas/netdata/`
- Active deploy entrypoint: `tools/arbuzas/deploy.sh`
- Active DNS Rust workspace: `tools/arbuzas-rs/`
- Tunnel config renderer: `tools/arbuzas/render_cloudflared_config.py`

## Initial Setup

1. Copy `infra/arbuzas/docker/env/arbuzas.example.env` to a private local env file if you need overrides.
2. Make sure Arbuzas has Docker with the Compose plugin, Python 3, and SSH access.
3. Make sure Arbuzas has `nginx` installed and running for the bare private DNS admin URL.
4. Make sure these host files exist:
   - `/etc/arbuzas/env/train-bot.env`
   - `/etc/arbuzas/env/satiksme-bot.env`
   - `/etc/arbuzas/env/subscription-bot.env`
   - `/etc/arbuzas/dns/runtime.env`
   - `/etc/arbuzas/dns/arbuzas-dns.yaml`
   - `/etc/arbuzas/dns/tls/fullchain.pem`
   - `/etc/arbuzas/dns/tls/privkey.pem`
   - `/etc/arbuzas/cloudflared/train-bot.json`
   - `/etc/arbuzas/cloudflared/satiksme-bot.json`
   - `/etc/arbuzas/cloudflared/subscription-bot.json`
5. Do not set `*_WEB_BIND_ADDR` or `*_WEB_PORT` in the Train, Satiksme, or Subscription host env files. Docker Compose owns those runtime values on Arbuzas.
6. DNS on Arbuzas binds directly to host ports `443` and `853`.

## Normal Release Flow

Deploy the current repo state:

```bash
./tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"
```

Deploy only one service or a few services:

```bash
./tools/arbuzas/deploy.sh deploy --services dns_controlplane --ssh-host arbuzas --ssh-user "$USER"
./tools/arbuzas/deploy.sh deploy --services train_bot,subscription_bot --ssh-host arbuzas --ssh-user "$USER"
```

Notes for targeted updates:

- `--services` is available only for `deploy` and `validate`.
- Service names use the Compose service names from `infra/arbuzas/docker/compose.yml`.
- `train_bot`, `satiksme_bot`, and `subscription_bot` automatically bring along their matching tunnel service so the public route stays aligned.
- `site-notifications` is kept in the repo for reference and testing, but it is not part of the active Arbuzas deploy set.
- Targeted validation checks the slice you touched instead of forcing a full-stack validation pass.

Validate an existing release:

```bash
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services dns_controlplane --ssh-host arbuzas --ssh-user "$USER"
```

Run the Docker cleanup policy without deploying:

```bash
./tools/arbuzas/deploy.sh cleanup-docker --ssh-host arbuzas --ssh-user "$USER"
```

Run one live DNS observability database compaction pass without deploying:

```bash
./tools/arbuzas/deploy.sh compact-dns-db --ssh-host arbuzas --ssh-user "$USER"
```

Repair a stale or broken Portainer install on the active host:

```bash
./tools/arbuzas/deploy.sh repair-portainer --ssh-host arbuzas --ssh-user "$USER"
```

Install or refresh the host-native Netdata setup:

```bash
./tools/arbuzas/deploy.sh install-netdata --ssh-host arbuzas --ssh-user "$USER"
```

Re-run the Netdata checks without reinstalling it:

```bash
./tools/arbuzas/deploy.sh validate-netdata --ssh-host arbuzas --ssh-user "$USER"
```

Install or refresh the host-native ThinkPad fan controller:

```bash
./tools/arbuzas/deploy.sh install-thinkpad-fan --ssh-host arbuzas --ssh-user "$USER"
```

Re-run the fan-controller checks without reinstalling it:

```bash
./tools/arbuzas/deploy.sh validate-thinkpad-fan --ssh-host arbuzas --ssh-user "$USER"
```

## What Deploy Does

- prepares a minimal release bundle under `/etc/arbuzas/releases/<release-id>`
- renders Cloudflare tunnel configs inside that release bundle
- updates `/etc/arbuzas/current`
- runs `docker compose -p arbuzas up -d --build`
- when `--services` is set, rebuilds and restarts only the requested services instead of the full stack
- validates Portainer, apps, tunnels, and DNS
- prunes unused Docker images after they have stayed unprotected for 7 days
- prunes Docker build cache older than 7 days
- confirms native Arbuzas DNS on `443/853` both on the host itself and from the public endpoint

The normal Docker release flow does not install or update Netdata. Netdata is a separate host-maintenance action.
The ThinkPad fan controller is also a separate host-maintenance action.

## Rollback

```bash
./tools/arbuzas/deploy.sh rollback --release-id "<previous-release-id>" --ssh-host arbuzas --ssh-user "$USER"
```

Rollback re-runs the same post-validation Docker cleanup policy after the host is healthy again.

## Docker Cleanup

The active Arbuzas runtime now applies Docker cleanup in two ways:

- automatically after a successful `deploy`
- automatically after a successful `rollback`
- manually through `./tools/arbuzas/deploy.sh cleanup-docker`

What the cleanup protects:

- any image still referenced by a container, even if that container is stopped
- all `arbuzas/*:<release-id>` images for the current release
- all `arbuzas/*:<release-id>` images for one rollback slot: the newest non-current release directory under `/etc/arbuzas/releases`

What the cleanup removes:

- any other unused image only after it has stayed unused and unprotected for 7 days
- Docker build cache older than 7 days

What the cleanup does not touch:

- release bundles under `/etc/arbuzas/releases`
- containers
- volumes
- networks

Implementation notes for operators:

- Cleanup state is tracked under `/etc/arbuzas/docker-gc/state.json`.
- If the cleanup state file is missing or corrupted, Arbuzas recreates it and starts a fresh 7-day countdown instead of deleting newly eligible images immediately.
- If automatic cleanup fails after a successful deploy or rollback, the release still stays successful and the cleanup failure is logged as a warning.
- Manual `cleanup-docker` fails loudly if the cleanup itself cannot complete.

## DNS DB Compaction

The active Arbuzas `dns_controlplane` service owns DNS state compaction.

- Primary state store: `/srv/arbuzas/dns/state/controlplane.sqlite`
- Compatibility observability store: `/srv/arbuzas/dns/state/identity-observability.sqlite`
- Public identity surface and policy sync now run inside the same native `dns_controlplane` service.

Manual operator command:

```bash
./tools/arbuzas/deploy.sh compact-dns-db --ssh-host arbuzas --ssh-user "$USER"
```

Expected result:

- The command prints a JSON result from the live `dns_controlplane` container.
- `controlplane.status` is normally `compacted`.
- `legacyObservability.status` is normally `compacted` when the compatibility observability file exists.
- The `beforeBytes`, `afterBytes`, and `reclaimedBytes` values show how much space was recovered.

Post-run checks:

- `./tools/arbuzas/deploy.sh validate --services dns_controlplane --ssh-host arbuzas --ssh-user "$USER"`
- confirm `dns_controlplane` stays healthy
- confirm public `https://dns.jolkins.id.lv/login` returns `404`
- confirm the private admin UI still loads on `http://<arbuzas-tailnet-dns-name>/`
- confirm the short MagicDNS host works too at `http://arbuzas/` when the operator machine has MagicDNS enabled
- confirm the raw private admin port still loads on `http://<arbuzas-tailnet-ip>:8097/login`
- confirm the improved query log and usage pages still load normally through the private admin port

## Portainer Repair

Use `repair-portainer` when Portainer is carrying stale Swarm-era state, such as a saved `tasks.agent` endpoint from the old deployment path.

What the repair does:

- confirms the current Arbuzas Compose stack is healthy before changing Portainer
- refuses to continue if any Docker Swarm services or stacks are still active
- stops only the Portainer container
- archives `/srv/arbuzas/portainer` into `/srv/arbuzas/portainer-backups/portainer-<timestamp>.tar.gz`
- rewrites the saved Portainer endpoint from `tcp://tasks.agent:9001` to the local Docker socket where possible
- compacts the Portainer database so the stale agent address is removed from saved state
- runs `docker swarm leave --force` so the live host returns to standalone Docker
- starts Portainer again through the active Compose project
- re-runs validation for Portainer, apps, tunnels, DNS, and the standalone-host baseline

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

The Arbuzas Netdata setup is intentionally host-native, not a Compose service.

What `install-netdata` does:

- installs `lm-sensors` and `smartmontools`
- runs the official Netdata installer in stable, native-package, non-interactive mode
- keeps Netdata auto-updates and anonymous telemetry disabled
- syncs the repo-managed config from `infra/arbuzas/netdata/`
- keeps Netdata's Docker collector and Docker-backed service discovery disabled on Arbuzas so Docker itself is not polled during normal host monitoring
- restarts Netdata so it binds only to `localhost:19999`
- publishes a private TCP forward on the host through `tailscale serve`
- validates the local API, Tailscale access, and expected Arbuzas hardware charts

Access pattern:

- local host listener: `http://127.0.0.1:19999`
- operator access: `http://<arbuzas-tailnet-ip>:19999`
- there is no Cloudflare route for Netdata
- there is no Portainer plugin dependency
- there is no Netdata Cloud claim in the Arbuzas baseline

## ThinkPad Fan Control

Arbuzas now keeps a small host-native ThinkPad fan policy outside the Docker Compose project.

What `install-thinkpad-fan` does:

- installs a repo-managed `thinkpad_acpi` modprobe override that enables manual fan control after boot
- installs a systemd service and Python controller under `/etc/systemd/system/` and `/usr/local/libexec/`
- reloads the ThinkPad ACPI driver so the manual fan interface is available immediately
- keeps the fan at manual level `1` under normal temperatures so it stays spinning at the lowest confirmed running speed
- hands control back to the ThinkPad embedded controller at `89°C` and above, and does not force manual mode again until the CPU sensor cools back to `89°C`
- re-arms the ThinkPad fan watchdog continuously so a controller crash falls back to a safe automatic mode

Validation checks:

- the `arbuzas-thinkpad-fan.service` unit stays active
- `thinkpad_acpi` reports manual fan control enabled
- the current live fan mode matches the expected policy for the current CPU temperature

## Notes

- Portainer connects directly to the local Docker socket.
- Netdata lives on the host outside the `arbuzas` Compose project.
- The active runtime is one Compose project named `arbuzas`.
- Arbuzas keeps the native DNS controlplane directly on `443/853`.
- The live Arbuzas host must stay out of Docker Swarm. Validation now fails if Swarm is still enabled or if Portainer state still references `tasks.agent`.
- Swarm and rooted Pixel deployment paths are rollback-only legacy material.
- If the live host still carries old localhost-only web bind or port values from the Pixel era, remove those keys from `/etc/arbuzas/env/*.env` before the next deploy.
