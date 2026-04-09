# Filesystem and State Reference

## Runtime Root

- `/data/local/pixel-stack/bin` - orchestrator entrypoint scripts (`pixel-dns-*`, `pixel-ssh-*`, `pixel-ddns-sync.sh`, `pixel-train-*`, `pixel-notifier-*`)
- `/data/local/pixel-stack/conf` - runtime env files, orchestrator config JSON, and secret files
- `/data/local/pixel-stack/conf/runtime/runtime-manifest.json` - local runtime manifest staged by deploy script (`--runtime-bundle-dir`)
- `/data/local/pixel-stack/conf/runtime/artifacts` - local runtime artifact tarballs staged by deploy script
- `/data/local/pixel-stack/run` - PID files and lock directories
- `/data/local/pixel-stack/logs` - DNS/runtime/DDNS logs
- `/data/local/pixel-stack/apps` - app component runtimes (`train-bot`, `site-notifications`)
- `/data/local/pixel-stack/backups` - backup area reserved by runtime layout

## Rooted DNS Runtime

- `/data/local/pixel-stack/chroots/adguardhome` - rooted AdGuard Home chroot rootfs
- `/data/local/pixel-stack/templates/rooted` - rooted runtime templates copied from APK assets
- `/data/local/pixel-stack/bin/pixel-dns-identityctl` - host-side wrapper for encrypted DNS identity control (`chroot -> /usr/local/bin/adguardhome-doh-identityctl`)
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-render-config` - renders AdGuardHome config plus remote nginx/watchdog runtime inputs
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-launch-core` - starts core AdGuardHome listeners (`53`, `127.0.0.1:8080`, and internal DoT `8853` when nginx fronts public DoT)
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-launch-frontend` - starts the remote frontend on public HTTPS/DoT listener ports (production contract now `2789` and `853`)
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-start` - high-level runtime helper (`--remote-healthcheck`, `--remote-restart`, `--remote-reload-frontend`, `runtime-status`)
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-doh-identityctl` - encrypted DNS identity CLI entrypoint in chroot
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-doh-identities.py` - encrypted DNS identity and usage state helper
- `/data/local/pixel-stack/chroots/adguardhome/usr/local/bin/adguardhome-doh-identity-web.py` - encrypted DNS identity web/API sidecar (`/pixel-stack/identity`, `/pixel-stack/identity/api/*`)
- `/data/local/pixel-stack/chroots/adguardhome/etc/pixel-stack/remote-dns/runtime.env` - rendered remote runtime env shared by nginx/watchdog/helpers
- `/data/local/pixel-stack/chroots/adguardhome/etc/pixel-stack/remote-dns/doh-identities.json` - encrypted DNS identity source of truth for tokenized DoH plus managed DoT hostnames
- `/data/local/pixel-stack/chroots/adguardhome/etc/pixel-stack/remote-dns/state/doh-usage-events.jsonl` - tokenized DoH usage ledger
- `/data/local/pixel-stack/chroots/adguardhome/etc/pixel-stack/remote-dns/state/doh-usage-cursor.json` - tokenized DoH access-log cursor
- `/data/local/pixel-stack/chroots/adguardhome/etc/nginx/pixel-stack-adguardhome-remote-nginx.conf` - rendered nginx frontend config for the active public HTTPS/DoT listener ports (`2789` and `853` in current production)
- `/data/local/pixel-stack/chroots/adguardhome/var/log/adguardhome/remote-nginx-access.log` - nginx HTTPS access log
- `/data/local/pixel-stack/chroots/adguardhome/var/log/adguardhome/remote-nginx-error.log` - nginx frontend error log
- `/data/local/pixel-stack/chroots/adguardhome/var/log/adguardhome/remote-nginx-doh-access.log` - dedicated DoH access log used by usage rollup
- `/data/local/pixel-stack/chroots/adguardhome/var/log/adguardhome/doh-identity-web.log` - encrypted DNS identity web/API sidecar log
- `/data/local/pixel-stack/chroots/adguardhome/var/log/adguardhome/remote-watchdog.log` - remote frontend watchdog log
- `/data/local/pixel-stack/chroots/adguardhome/run/pixel-stack-adguardhome-remote-nginx.pid` - nginx frontend PID file
- `/data/local/pixel-stack/chroots/adguardhome/run/pixel-stack-adguardhome-remote-watchdog.pid` - remote frontend watchdog PID file
- `/data/local/pixel-stack/chroots/adguardhome/run/pixel-stack-adguardhome-doh-identity-web.pid` - encrypted DNS identity web/API sidecar PID file
- `/data/local/pixel-stack/chroots/adguardhome/run/pixel-stack-adguardhome-frontend-last-reload-reason` - last frontend reload reason marker
- `/data/local/pixel-stack/chroots/adguardhome/run/pixel-stack-adguardhome-frontend-last-reload-epoch` - last frontend reload timestamp marker

## Rooted SSH Runtime

- `/data/local/pixel-stack/ssh/bin` - Dropbear binaries and wrappers
- `/data/local/pixel-stack/ssh/conf` - Dropbear runtime env
- `/data/local/pixel-stack/ssh/etc` - managed auth files and host keys
- `/data/local/pixel-stack/ssh/logs` - SSH loop and Dropbear logs
- `/data/local/pixel-stack/ssh/run` - SSH PID files and lock state
- `/data/adb/pixel-stack/ssh` - legacy path that must remain absent after stabilization (`scripts/ops/purge-legacy-ssh-runtime.sh`)

## VPN Runtime

- `/data/local/pixel-stack/vpn/bin` - Tailscale binaries and loop wrappers
- `/data/local/pixel-stack/vpn/conf` - VPN runtime env mirrors
- `/data/local/pixel-stack/vpn/logs` - tailscaled and service-loop logs
- `/data/local/pixel-stack/vpn/run` - tailscaled socket and PID files
- `/data/local/pixel-stack/vpn/state` - tailscaled state store
- `/data/local/pixel-stack/conf/vpn/tailscale.env` - vpn env source file written by orchestrator
- `/data/local/pixel-stack/conf/vpn/tailscale-authkey` - Tailscale auth key file provisioned by deploy script

## App Component Runtimes

- `/data/local/pixel-stack/apps/train-bot` - train bot runtime root (`bin`, `env`, `logs`, `run`, `state`, `data/schedules`)
- `/data/local/pixel-stack/apps/train-bot/run/train-bot-cloudflared.pid` - Train Bot-owned Cloudflare tunnel PID
- `/data/local/pixel-stack/apps/train-bot/logs/train-bot-cloudflared.log` - Train Bot-owned Cloudflare tunnel log
- `/data/local/pixel-stack/apps/train-bot/state/train-web-tunnel/train-bot-cloudflared.yml` - rendered Cloudflare tunnel config for `train-bot.jolkins.id.lv`
- `/data/local/pixel-stack/apps/site-notifications` - notifier runtime root (`current`, `env`, `logs`, `run`, `state`, optional `.venv`)
- `/data/local/pixel-stack/conf/apps/train-bot.env` - train bot source env file copied into runtime
- `/data/local/pixel-stack/conf/apps/train-bot-cloudflared.json` - canonical Train Bot tunnel credentials source
- `/data/local/pixel-stack/conf/apps/site-notifications.env` - site notifier source env file copied into runtime

## App-Private State

- `<app-files-dir>/stack-store/orchestrator-config-v1.json` - persisted typed config snapshot
- `<app-files-dir>/stack-store/orchestrator-state-v1.json` - persisted health/supervisor state
