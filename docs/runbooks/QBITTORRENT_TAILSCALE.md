# qBittorrent over Tailscale

This runbook covers the private qBittorrent and VueTorrent setup on
kitty-gration.

## Mobile access

While the phone is connected to the tailnet, open:

`https://arbuzas-vps.tail9345a.ts.net:24680/`

There is no qBittorrent login screen. Access control is provided by Tailscale,
so a device or user that should not see qBittorrent must be denied by the
tailnet policy. The Web interface is not published on the server's public IP.
Port `443` is not used: Tailscale terminates HTTPS on the deliberately uncommon
port `24680` and forwards it to a loopback-only listener on port `18080`.

VueTorrent is the installed interface and is intended to work well on a phone.
The public TCP and UDP peer port is `45123`; that port carries torrent peer
traffic, not the Web interface.

## Retention and space policy

The companion housekeeper applies these rules every 30 seconds:

- A completed torrent is kept for at least 24 hours.
- Normal automatic deletion also requires its local share ratio to be at least
  `1.0`. Both conditions must be true.
- Eligible torrents and their payloads are deleted oldest-completion-first.
- Adding a torrent first reserves its full selected size. If it would exceed
  the 24 GiB working limit, the torrent remains stopped while the oldest
  eligible torrents are removed. Admission waits for a fresh disk measurement.
- If nothing is old enough and sufficiently seeded, the new torrent remains
  waiting. The age and ratio promise is never bypassed merely to make space.
- A torrent larger than the whole working limit is rejected.
- The `retention-keep` tag prevents automatic deletion, but it does not exempt
  the data from the space calculation.

The entire qBittorrent storage area, including its configuration, lives inside
a fully allocated 25 GiB ext4 image. This is the hard boundary. The 24 GiB
housekeeper limit leaves room for filesystem and configuration overhead. The
first deployment therefore needs at least 25 GiB of real free host storage.

New torrents stop after metadata is received. The housekeeper starts them only
after their full selected size fits. A torrent stopped manually after admission
stays stopped.

qBittorrent uses simple `pread`/`pwrite` disk access rather than memory-mapped
files, with operating-system cache use disabled where libtorrent supports it.
This reduces cache pressure; the hard container limit contains any remaining
reclaimable Linux file cache during fast downloads. The main
container has a 768 MiB hard memory ceiling and no additional swap allowance;
with the housekeeper's 128 MiB ceiling, the complete torrent stack is capped at
896 MiB with no extra swap allowance. Peer connections, socket and send
buffers, disk queues, checking memory, and open files use an explicitly managed
low-memory profile. qBittorrent's own torrent queue is disabled so it cannot
override the housekeeper's admission and ratio policy. The tradeoff is lower
peak swarm performance in exchange for a stable footprint.

Resume state is saved every minute, so an unrelated interruption cannot roll
progress back by more than one resume interval. The health check also becomes
unhealthy if the cgroup limit is wrong or after any cgroup OOM event or kill,
instead of allowing the internal process supervisor to hide the failure.

## Deploy and verify

Deploy only this slice with standard validation:

```sh
./tools/arbuzas/deploy.sh deploy \
  --services qbittorrent \
  --validation-profile standard \
  --ssh-host kitty-gration \
  --ssh-user "$USER"
```

Validate an already deployed release:

```sh
./tools/arbuzas/deploy.sh validate \
  --services qbittorrent \
  --validation-profile standard \
  --release-id "<release-id>" \
  --ssh-host kitty-gration \
  --ssh-user "$USER"
```

Validation checks the capped mount, no-login private API, VueTorrent page,
loopback-only Web binding, public peer bindings, housekeeper status, Tailscale
route, and the existing Tailscale HTTPS service on port `10000`.

## Diagnosis

Use the normal Compose logs from the active release:

```sh
ssh kitty-gration 'cd /etc/arbuzas/current && docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml logs --tail 200 qbittorrent qbittorrent_housekeeper'
```

Check the storage boundary without changing it:

```sh
ssh kitty-gration 'sudo /etc/arbuzas/current/infra/arbuzas/qbittorrent/install-storage.sh check'
```

Check the enforced limit, current and peak use, and any internal qBittorrent OOM
restart that Docker's container restart counter can miss:

```sh
ssh kitty-gration 'docker exec arbuzas-qbittorrent-1 sh -c "for file in memory.current memory.peak memory.max memory.swap.max memory.events; do echo \"\$file\"; cat \"/sys/fs/cgroup/\$file\"; done"'
```

`memory.max` must be `805306368`, `memory.swap.max` must be `0`, and both `oom`
and `oom_kill` must remain `0`. The Compose health check enforces these values
for the current container lifetime.

If a torrent is waiting, first check whether enough space can be reclaimed
without violating the 24-hour and ratio requirements. Removing the
`retention-keep` tag only makes the torrent eligible once both normal deletion
conditions are also satisfied.

Only download and share material you are permitted to distribute. Tailscale
protects the management page; it does not make torrent peer traffic private.
