# Jellyfin over Tailscale

## Purpose

Jellyfin provides a small mobile-friendly view of the torrent library on
kitty-gration. It is private to the tailnet and is designed for direct
playback, not CPU-heavy video conversion.

Normal access is:

```text
https://arbuzas-vps.tail9345a.ts.net:29096
```

Port `443` is not used. Tailscale terminates HTTPS on `29096` and forwards to
the host-only listener at `127.0.0.1:18096`. The container listens internally
on `8096`. There is no public-IP, Cloudflare, LAN, DLNA, or discovery route.

## Sign-in model

The visible `Media` profile has no password. On a new browser or Jellyfin app,
select `Media` once; the client normally remembers that session afterward.
Jellyfin does not offer a supported fully anonymous mode, so the initial
profile selection cannot be removed without modifying Jellyfin clients.

The separate `JellyfinAdmin` profile is hidden and has a strong password. Its
password lives only at:

```text
/etc/arbuzas/secrets/jellyfin/admin-password.secret
```

The file is root-owned with mode `0600`, is never mounted into the container,
and is never placed in the release environment, container, logs, or operator
notes. The normal Arbuzas host-mirror pull copies it into the local plaintext
ops mirror with the other managed secrets. Use the hidden administrator only
for server configuration.

## Runtime boundaries

The service uses the official Jellyfin `10.11.11` image pinned by manifest
digest. Its resource envelope is:

- `0.75` CPU;
- `512 MiB` hard memory limit;
- `128 MiB` protected memory reservation;
- no swap beyond the hard memory limit;
- `256` process limit.

The container runs as the resolved Jellyfin user and group, with a read-only
root filesystem, all Linux capabilities removed, and privilege escalation
disabled. Writable state is limited to these disk-backed host directories:

```text
/srv/arbuzas/jellyfin/config
/srv/arbuzas/jellyfin/cache
/srv/arbuzas/jellyfin/tmp
/srv/arbuzas/jellyfin/transcodes
```

The torrent payload is mounted read-only at `/media`. Jellyfin cannot rename,
alter, or delete a torrent. qBittorrent and its housekeeper remain the only
owners of torrent lifecycle changes.

The private Docker network is `172.29.247.0/28`; Jellyfin uses
`172.29.247.2`. Only TCP `8096` is published, and only to host loopback. No
UDP discovery ports are published.

## Playback and library behavior

The single mixed library is named `Torrent Library` and is rooted at `/media`.
Real-time monitoring handles normal additions and removals, with one daily
fallback scan at 04:00.

The `Media` profile permits:

- direct playback;
- container remuxing;
- audio-only conversion.

Video transcoding is disabled. If a phone cannot decode a video's codec,
resolution, or subtitle combination, playback fails instead of consuming the
server with software video conversion. Prefer a client and playback mode that
reports `Direct Play`.

Chapter-image extraction, trickplay previews, keyframe extraction, media
segment analysis, automatic subtitle/lyric downloads, photo handling, and
audio loudness scans are disabled.

`/media/.incomplete/.ignore` prevents unfinished qBittorrent downloads from
appearing in Jellyfin. The qBittorrent storage installer creates and validates
that marker. When the housekeeper expires a completed torrent, Jellyfin's
filesystem monitor removes it from the library. Apply qBittorrent's
`retention-keep` tag before the retention deadline when a title must remain.

## Deploy and validate

Deploy only Jellyfin and its required configuration:

```bash
./tools/arbuzas/deploy.sh deploy \
  --services jellyfin \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```

The deploy prepares the four state directories, resolves their ownership,
creates the root-only administrator secret when absent, starts the container,
runs the idempotent bootstrap, verifies the resulting configuration, and then
publishes the Tailscale route. An existing administrator secret is preserved.

Re-run the full Jellyfin checks with the deployed release identifier:

```bash
./tools/arbuzas/deploy.sh validate \
  --services jellyfin \
  --release-id "<release-id>" \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```

The checks cover the loopback-only port, private network, resource and
hardening settings, read-only media mount, root-only secret, passwordless
`Media` profile, hidden administrator, library policy, health endpoint, and
exact Tailscale Serve route.

## Manual health checks

On kitty-gration, the private host listener must return `Healthy`:

```bash
curl -fsS http://127.0.0.1:18096/health
```

From a device connected to the tailnet, the HTTPS endpoint must also return
`Healthy`:

```bash
curl -fsS https://arbuzas-vps.tail9345a.ts.net:29096/health
```

Inspect the managed route without changing it:

```bash
tailscale serve status
```

The expected route is HTTPS `:29096` to
`http://127.0.0.1:18096`. Existing qBittorrent `:24680` and other Tailscale
routes must remain unchanged.

Run the configuration drift check without showing the administrator secret:

```bash
sudo -n python3 /etc/arbuzas/current/infra/arbuzas/jellyfin/bootstrap.py check \
  --url http://127.0.0.1:18096 \
  --admin-password-file /etc/arbuzas/secrets/jellyfin/admin-password.secret
```

## Recovery

If health fails, check in this order:

1. Confirm the torrent storage mount is active and its `.incomplete/.ignore`
   marker is valid.
2. Confirm all four Jellyfin state directories are owned by the resolved
   Jellyfin user and group and have mode `0750`.
3. Confirm the administrator secret is a regular root-owned file with mode
   `0600`; do not print its contents.
4. Inspect `docker logs arbuzas-jellyfin-1` for startup or permission errors.
5. Re-run targeted deployment and then targeted validation.

Do not delete `/srv/arbuzas/jellyfin/config` as routine recovery: it contains
the database, watched progress, users, and client sessions. Do not recreate or
rotate the administrator secret while the existing Jellyfin configuration
still uses it.

If publication fails because `:29096` already has an unrelated Tailscale Serve
handler, deployment deliberately stops rather than replacing it. Inspect the
route and resolve the ownership conflict before retrying.

Use the normal release rollback when the new release itself is faulty:

```bash
./tools/arbuzas/deploy.sh rollback \
  --release-id "<previous-release-id>" \
  --services jellyfin \
  --ssh-host kitty-gration \
  --ssh-user ropepop
```
