# kitty-gration Docker Operations

This is the detailed operator runbook for the active kitty-gration runtime.

## Files

- Active layout: `infra/arbuzas/docker/`
- Existing external VPN project source: `infra/arbuzas/tiny-vless/`
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
   - `/etc/arbuzas/cloudflared/train-bot.json`
   - `/etc/arbuzas/cloudflared/satiksme-bot.json`
   - `/etc/arbuzas/env/tiny-vless.env`
   - `/etc/arbuzas/secrets/tiny-vless/`
6. Do not set `*_WEB_BIND_ADDR` or `*_WEB_PORT` in the Train or Satiksme host env files. Do not set `TRAIN_WEB_PUBLIC_BASE_URL` in the Train host env file. Docker Compose owns those runtime values on kitty-gration.

### Private configuration rules

- Host environment, secret, tunnel-credential, and release environment files must be mode `0600`. Every mirror action repairs checkout-default local modes; normal deploy and config-only deploy repair the host modes before restarting services; full validation rejects unsafe modes.
- Tiny-VLESS certificates, keys, and the clearnet capability belong under
  `infra/arbuzas/host-mirror/etc/arbuzas/secrets/tiny-vless/`. Its live SQLite
  database does not: it remains application state under `/opt/tiny-vless/db`
  and uses SQLite-safe backups under `/srv/arbuzas/tiny-vless/backups`.
- Do not create `.bak`, `.before-*`, `.retired-*`, or editor backup copies under `/etc/arbuzas/env` or its local mirror. Mirror operations ignore these files, deploy removes old copies from the host, and full validation rejects any that remain.
- Satiksme chat-analyzer Telegram and Google credentials belong under `infra/arbuzas/host-mirror/etc/arbuzas/secrets/satiksme-chat-analyzer/`, not inline in `satiksme-bot.env`. The directory is intentionally ignored by Git. The service environment contains only the matching `*_FILE` paths.

Migrate an existing local Satiksme environment without displaying its values:

```bash
python3 tools/arbuzas/migrate_satiksme_analyzer_secrets.py \
  --env-file infra/arbuzas/host-mirror/etc/arbuzas/env/satiksme-bot.env \
  --secrets-dir infra/arbuzas/host-mirror/etc/arbuzas/secrets/satiksme-chat-analyzer
```

Replace a copied Google key without placing it in shell history or command arguments:

```bash
pbpaste | python3 tools/arbuzas/migrate_satiksme_analyzer_secrets.py \
  --secrets-dir infra/arbuzas/host-mirror/etc/arbuzas/secrets/satiksme-chat-analyzer \
  --set-google-key-stdin
```

The replacement command writes both Google/model key files atomically as `0600` and prints only a generic confirmation. Follow either change with `mirror-audit`, then `deploy-config` or the normal deploy flow. Never copy the secret value into a report, terminal command, or tracked file.

## Normal Release Flow

Deploy the current repo state:

```bash
./tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user "$USER"
```

Deploy only one service or a few services:

```bash
./tools/arbuzas/deploy.sh deploy --services train_bot,satiksme_bot --ssh-host kitty-gration --ssh-user "$USER"
```

Deploy or validate the existing external VPN project through the same
entrypoint:

```bash
./tools/arbuzas/deploy.sh deploy --services tiny_vless --validation-profile standard --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services tiny_vless --validation-profile standard --ssh-host kitty-gration --ssh-user "$USER"
```

Use an explicit validation profile for targeted iteration:

```bash
./tools/arbuzas/deploy.sh deploy --services ticket_remote --validation-profile fast --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services ticket_remote --validation-profile standard --release-id "<release-id>" --ssh-host kitty-gration --ssh-user "$USER"
```

Notes for targeted updates:

- `--services` is available only for `deploy` and `validate`.
- Service names normally use the Compose service names from
  `infra/arbuzas/docker/compose.yml`. `tiny_vless` is the deliberate exception:
  it is a first-class umbrella selector for the existing separate Compose
  project and is never passed to the main Compose project.
- `train_bot` and `satiksme_bot` automatically bring along their matching tunnel service so the public route stays aligned.
- `site-notifications` is kept in the repo for reference and testing, but it is not part of the active kitty-gration deploy set.
- Targeted validation checks the slice you touched instead of forcing a full-stack validation pass.
- `fast` is the inner iteration lane. It requires `--services`, reuses the unchanged release content, restarts only the selected slice, runs bounded readiness probes concurrently, and defers remote Docker/release cleanup. It still prunes expired local release artifacts after successful validation.
- `standard` is the targeted confidence lane. It validates the selected workload more deeply while still avoiding unrelated full-stack checks.
- `full` is the release lane and remains the default for unscoped deploys. It validates the complete host and performs release and image cleanup.
- An unscoped deploy validates tiny-VLESS health but does not restart its
  separate project. Recreating the VPN always requires an explicit
  `--services tiny_vless` selection.
- Full validation refuses releases marked dirty or unknown. A temporary dirty fast release cannot be used as final production proof.
- Direct Spacetime privacy probes retry for up to four minutes so brief upstream interruptions do not incorrectly fail an otherwise healthy release.
- Finish a sequence of fast iterations with an unscoped full deploy and validation. A fast release deliberately preserves unchanged service images and is not a replacement for the canonical full release.

Validate an existing release:

```bash
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services train_bot,satiksme_bot --ssh-host kitty-gration --ssh-user "$USER"
```

Inspect the active Compose project directly over SSH:

```bash
ssh kitty-gration 'cd /etc/arbuzas/current && docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml ps'
ssh kitty-gration 'cd /etc/arbuzas/current && docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml logs --tail 200 ticket_remote'
```

Replace `ticket_remote` with the service you are investigating. Use `deploy.sh` for routine changes. For an emergency manual restart, preserve the `arbuzas` project name and the active release inputs, then run validation:

```bash
ssh kitty-gration 'cd /etc/arbuzas/current && docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml restart ticket_remote'
./tools/arbuzas/deploy.sh validate --services ticket_remote --ssh-host kitty-gration --ssh-user "$USER"
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

## External tiny-VLESS project

3X-UI/Xray remains in the separate Compose project that already owns the live
VPN identities. It is not being copied into `infra/arbuzas/docker/compose.yml`.
The deployment script treats it as a first-class external component so the
same source, mirror, validation, and rollback controls cover it without
renaming containers or regenerating client material.

### Source, configuration, and state

- Reproducible source: `infra/arbuzas/tiny-vless/`
- Canonical environment: `/etc/arbuzas/env/tiny-vless.env`
- Canonical private material: `/etc/arbuzas/secrets/tiny-vless/`
- Live SQLite state: `/opt/tiny-vless/db`
- Consistent recovery copies: `/srv/arbuzas/tiny-vless/backups`

The environment and secret paths use the existing local-first mirror. The
database is live application state and must never be copied into that plaintext
mirror. Pull and audit before editing:

```bash
./tools/arbuzas/deploy.sh mirror-pull --ssh-host kitty-gration --ssh-user ropepop
./tools/arbuzas/deploy.sh mirror-audit --ssh-host kitty-gration --ssh-user ropepop
```

Apply a mirrored VPN-only configuration change with `deploy-config`, or push a
reviewed mirror without restarting anything with `mirror-push`:

```bash
./tools/arbuzas/deploy.sh deploy-config --ssh-host kitty-gration --ssh-user ropepop
./tools/arbuzas/deploy.sh mirror-push --ssh-host kitty-gration --ssh-user ropepop
```

`deploy-config` maps changes below the two tiny-VLESS mirror paths to the
`tiny_vless` selector. It does not rebuild or restart the main application
project.

### Host prerequisites and publications

A clean target needs Docker and the Compose plugin, Python 3, SQLite tooling,
Nginx, Tailscale, host firewall tools, `iproute2` traffic control, SSH, and the
same required public TCP/UDP listener availability. The operator needs
passwordless `sudo` for the guarded host actions.

The private panel and HTTPS subscription publication use Tailscale. The
separate capability-addressed clearnet subscription publication uses host
Nginx on public TCP port 18081. That listener is plain HTTP and has no TLS; the
unguessable capability does not protect the response from an on-path observer.
Deploy and restore operations compare the complete Tailscale Serve/Funnel
configuration so unrelated private routes are preserved.

The same deployment slice also owns the VPN abuse firewall rules and the
recurring bandwidth limiter. The limiter must resolve and attach to the
container's current host interface after every recreation; a stale rule on an
old interface is a failed validation, not a healthy limit. Its sustained cap
remains 100 Mbps in both directions, with a 2 MiB token bucket and the existing
50 ms queue ceiling to absorb short QUIC/GSO bursts. The two-minute repair run
must leave matching live qdiscs and their counters untouched; it changes them
only when an attachment or setting has actually drifted.

The seven-profile subscription publishes Hysteria2 only on dedicated UDP
`8447`. The separate HTTP/2 recovery profile uses dedicated TCP `18448`. The
VPN project publishes neither TCP nor UDP `443` on the VPS public IPv4 address.
Tailscale HTTPS on the private overlay remains unchanged. Restore each database
inbound and its matching Docker publication together, then refresh clients
through the existing subscription.

### iPhone mobility checks

The wired Mac acceptance does not prove cellular or train-Wi-Fi reachability.
Select the Hysteria UDP `8447` profile manually, then use the HTTP/2 TCP `18448`
profile as the recovery control. Never use an automatic selector. Test these
underlays in order:

1. known-good Wi-Fi;
2. direct cellular with Wi-Fi disabled; and
3. train Wi-Fi after completing its captive portal, then enabling Airplane
   Mode and re-enabling Wi-Fi so iOS cannot silently fall back to cellular.

For each network/profile cell, connect at T+0, open an IP-literal HTTPS target
at T+20, open the matching hostname HTTPS target at T+30, transfer an exact
1 MiB from the same test destination at T+40, refresh the affected app at T+50,
and disconnect at T+70. Interpret each cell consistently:

- Hysteria fails while the TCP recovery profile works: the underlay likely
  filters or degrades UDP `8447`;
- both profiles fail: check the underlay, client VPN engine, and server
  reachability separately;
- handshake advances but IP-literal and hostname HTTPS differ: client DNS or
  routing;
- small HTTPS succeeds but the 1 MiB transfer stalls: loss, MTU, or shaping;
- the tunnel tests pass but the app fails: iOS TUN, Network Extension, or
  app-specific routing.

If Hysteria fails, immediately test the TCP `18448` recovery profile on the same
underlay, retry Hysteria once, and repeat in a second client where supported.
The iPhone result remains pending until these cells are run on the actual
networks.

### Manual read-only inspection

Use the umbrella for routine work. For emergency read-only inspection of the
separate project, keep its existing project name and canonical environment:

```bash
ssh kitty-gration 'cd /opt/tiny-vless && docker compose --project-name tiny-vless --env-file /etc/arbuzas/env/tiny-vless.env -f docker-compose.yml ps'
```

Do not run a manual `up`, create a second project name, or substitute a new
database. Use the targeted deploy command for a managed recreation.

### Identity-preserving restore

The normal recovery path restores a matching set: the SQLite backup, mirrored
environment, certificate/key material, and capability. The umbrella then
reinstalls the same source and host integrations. This keeps the existing
subscription, client identities, profile credentials, and certificate pins.

A fresh reroll is intentionally different. Use it only when explicitly
abandoning the prior VPN identity and after planning the client migration; it
is never the default response to a host move or failed deploy.

### VPN verification

The targeted validation is complete only when all of the following pass
without printing private profile material:

- container health and restart stability;
- SQLite integrity plus the expected enabled profile classes and counts;
- required TCP and UDP listeners and private panel publication;
- agreement between the private subscription generator and the public port-18081
  alias for the private capability;
- generic rejection of unknown public paths, queries, and unsupported methods;
- preservation of every unrelated Tailscale route;
- active, boot-persistent firewall policy;
- an active limiter on the current container interface; and
- real tunnel checks for the original profile and supported added profiles.

Netdata is installed on the live host. The following actions refresh or validate its host-native setup.

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
- when `--services tiny_vless` is set, updates only the existing external VPN
  project after taking its guarded recovery snapshot; an unscoped deploy only
  validates that project and leaves it running
- validates the apps, tunnels, and standalone Docker baseline
- after every successful validation profile, prunes expired local bundles under `output/arbuzas/releases` while protecting the deployed release
- prunes unused Docker images after they have stayed unprotected for 7 days
- prunes old release bundles beyond the newest 10 per release family
- prunes Docker build cache older than 7 days
- runs gentle host cache cleanup for package caches, narrow old `/tmp` scratch, and journals
- flushes reclaimable Linux memory cache after cleanup so provider memory graphs fall back quickly

The normal Docker release flow does not install or update Netdata. Netdata is a separate host-maintenance action.
The corrected memory report service is also a separate host-maintenance action.
The ThinkPad fan controller is also a separate host-maintenance action.
The external tiny-VLESS project participates in this release and validation
umbrella, but remains outside the main `arbuzas` Compose project.

## Rollback

```bash
./tools/arbuzas/deploy.sh rollback --release-id "<previous-release-id>" --ssh-host kitty-gration --ssh-user "$USER"
```

Rollback re-runs the same post-validation cleanup policy after the host is healthy again.

Before a targeted VPN rollout, the deploy path creates a consistent SQLite
backup and snapshots the host integration state it may change. If that rollout
fails, it restores the previous inputs and validates the restored external
project. For disaster recovery, restore a matching database/config/secret set
and then run the targeted `tiny_vless` deploy and validation. Do not generate a
new database or client identities as an implicit rollback.

## Cleanup

The deployment workflow applies cleanup in three ways:

- local release cleanup after every successful `deploy` profile and successful `rollback`
- full remote cleanup after a successful `full` deploy or rollback
- manual remote cleanup through `./tools/arbuzas/deploy.sh cleanup-docker`

Local release cleanup is separate from remote Docker cleanup. It considers only direct child directories of `output/arbuzas/releases`, protects the deployed or rolled-back release id, and defaults to a 72-hour window with at most 10 releases per family. A release is selected when it is expired or exceeds the family limit. Files and symbolic links in that root are ignored, and evidence, state, secrets, databases, browser sessions, and workload paths are outside the managed root.

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
- the restricted Portainer retirement archives under `/srv/arbuzas/portainer-backups/`
- application state under `/srv/arbuzas/*`

Implementation notes for operators:

- Local release retention defaults to `ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS=72` and `ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY=10`.
- Set `ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN=true` to preview deploy-time local cleanup without deleting bundles.
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

## Retired Portainer

Portainer was retired from kitty-gration on 2026-07-20. It is not part of the active Compose project, there is no Portainer container, and nothing should listen on host port `9443`.

The former Portainer state was preserved temporarily as a restricted rollback archive at `/srv/arbuzas/portainer-backups/portainer-retired-<timestamp>.tar.gz`. The active `/srv/arbuzas/portainer` directory was removed. Treat the archive as sensitive because it contains the former management database; routine deployment, validation, and Docker recovery do not require it.

Use the deployment script for normal operations and the documented SSH commands for direct Docker inspection. Restoring Portainer is a separate, explicit recovery decision, not part of a routine deploy or rollback.

## Retired Subscription Bot

The Telegram subscription bot and hosted Mini App were retired on 2026-07-26.
The active Compose project contains neither `subscription_bot` nor
`subscription_tunnel`, and routine rollback paths keep both services disabled
even when an older release still defines them.

The final database, runtime state, environment, tunnel credentials, and exact
live-image metadata are retained only in a root-owned archive under
`/srv/arbuzas/subscription-bot-backups/`. The former active state and
configuration paths are absent. Treat the archive as sensitive because it can
contain user, billing, session, and provider credential material. Source and
recovery cautions are documented in `archive/subscription-bot/README.md`.

## Netdata Host Observability

Netdata is installed host-native on kitty-gration, not as a Compose service.

What `install-netdata` does:

- installs `lm-sensors` and `smartmontools`
- snapshots the previous Netdata configuration, shared dashboard, native console entry pages, mobile-layout hook, service state, and private route, and restores them if the refresh or validation fails
- runs the official Netdata installer in stable, native-package, non-interactive mode only when the Agent is absent; an existing Agent keeps its installed package version during dashboard/config refreshes
- keeps Netdata auto-updates and anonymous telemetry disabled
- syncs the repo-managed config, server-owned operations dashboard, and native mobile-layout shim from `infra/arbuzas/netdata/`
- reapplies the native mobile-layout shim before Netdata starts, so a future package refresh cannot silently return the phone view to the squeezed desktop layout
- keeps Netdata's Docker collector and Docker-backed service discovery disabled on kitty-gration so Docker itself is not polled during normal host monitoring
- restarts Netdata so it binds only to `localhost:19999`
- publishes a private HTTPS proxy on the host through `tailscale serve`
- validates the local API, shared dashboard and assets, native console mobile markers, Tailscale access, host and disk charts, named container charts, and the focused core-service view; hardware sensors are checked when the VPS exposes them

Access pattern:

- local host listener: `http://127.0.0.1:19999`
- shared operations dashboard: `https://arbuzas-vps.tail9345a.ts.net:19999/kitty-gration/`
- full Netdata console: `https://arbuzas-vps.tail9345a.ts.net:19999/`
- there is no Cloudflare route for Netdata
- there is no Netdata Cloud claim in the kitty-gration baseline

The shared dashboard uses ArrowJS and is served by Netdata itself, so it does not depend on one browser's saved custom-dashboard state. The same build also produces a small native-console mobile shim: it keeps every native metric and control, collapses the right navigator to a 48-pixel rail, opens it as an overlay, and turns multi-chart rows into full-width swipeable lanes below 700 pixels. Desktop Netdata is unchanged. Source lives under `infra/arbuzas/netdata/web-client/`; built files live under `infra/arbuzas/netdata/web/kitty-gration/`. Rebuild with `npm ci && npm test && npm run build` from the web-client directory before running `install-netdata`. Dashboard refreshes retain recently served hashed assets so an older cached page cannot go blank during the following 24 hours.

## ThinkPad Fan Control

The ThinkPad fan controller script and the `install-thinkpad-fan` deploy action are preserved in the repo at `infra/arbuzas/thinkpad-fan/`.

This is **not active on the live kitty-gration host** and is not applicable to it. The live host is an Intel Xeon Gold 6142 server, not a ThinkPad. The `thinkpad_acpi` kernel module is not loaded, the `arbuzas-thinkpad-fan.service` systemd unit is not installed, and the controller will not run on this hardware. Running `install-thinkpad-fan` on the live host will fail.

The `install-thinkpad-fan` and `validate-thinkpad-fan` deploy actions are kept for a future ThinkPad-based host only.

## Boost-Off Hook

The boost-off reboot hook script and the `install-netdata`-style helper are preserved in the repo at `infra/arbuzas/cpu-boost-off/`.

This is **not active on the live kitty-gration host** either. The `crontab` binary is not installed on the host, no `@reboot` crontab entry exists for the `ropepop` user, no script is installed at `/home/ropepop/.local/bin/arbuzas-disable-boost.sh`, and no log file is written. The kernel does not expose `/sys/devices/system/cpu/intel_pstate` on this Xeon server, so even installing the script would not have an observable effect.

Observed CPU clock stays at the 2.6 GHz base clock with no turbo visible, regardless of the script. The corrected memory report output confirms this.

## Netdata Status

Netdata Agent 2.10.4 is installed and active on the live kitty-gration host. The complete native console at `/` keeps all Netdata metrics while using a compact navigator and one readable chart per swipe on phones. The server-owned `Kitty-gration Operations` page at `/kitty-gration/` remains the focused status view for desktop and phone. The browser-importable JSON at `infra/arbuzas/netdata/dashboards/kitty-gration-operations.json` remains only as a legacy fallback. The `install-netdata` action refreshes both browser views, while `validate-netdata` checks their health and private access.

## Notes

- Netdata lives on the host outside the `arbuzas` Compose project and is reachable only through its private Tailscale route.
- The main application runtime is the Compose project named `arbuzas`. The
  existing tiny-VLESS project remains separate, under the same deployment
  umbrella.
- kitty-gration does not run a public DNS service. Public-interface ports `443`
  and `853` are free; private Tailscale HTTPS remains separate. The
  `dns_controlplane` service was retired on 2026-06-21 (see
  `archive/dns-controlplane/`).
- The live kitty-gration host must stay out of Docker Swarm. Validation fails if Swarm is enabled.
- Swarm and rooted Pixel deployment paths are rollback-only legacy material.
- If the live host still carries old localhost-only web bind or port values from the Pixel era, remove those keys from `/etc/arbuzas/env/*.env` before the next deploy.
