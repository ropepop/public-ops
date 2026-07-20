# qBittorrent Housekeeper

This small companion keeps a private qBittorrent instance within a storage
budget and applies the requested retention rule. It talks only to the
qBittorrent Web API and reads filesystem usage; it never removes files itself.

## Policy

- New metadata-complete torrents are kept stopped until they fit the budget.
- Admission reserves the torrent's full selected size, including bytes not yet
  downloaded. The current filesystem usage is checked as a separate safety
  input, so concurrent downloads cannot overbook the soft cap.
- A torrent larger than the whole soft cap is stopped and tagged
  `retention-rejected`.
- A torrent whose save or temporary path is outside `DOWNLOAD_PATH` is stopped
  and rejected. Prefix lookalikes such as `/downloads2` are outside the mount.
- A torrent that does not yet fit is stopped and tagged `retention-waiting`.
- A torrent that fits is tagged `retention-admitted` and started once. If it is
  stopped later, the housekeeper treats that as a manual stop and does not
  restart it.
- Normal deletion requires a valid qBittorrent completion time, at least 24
  completed hours, and a local ratio of at least 1.0. The whole torrent and its
  downloaded data are deleted.
- `retention-keep` prevents automatic deletion but does not exempt the torrent
  from the storage budget.
- Eligible deletions are submitted oldest-first. Admission waits for a later
  poll and a fresh filesystem measurement, so an asynchronous file deletion
  cannot create imaginary free space.

The qBittorrent add flow should use its **stop after metadata is received**
condition. The controller also re-stops metadata-complete waiting or rejected
torrents if they are started manually.

## Configuration

| Variable | Default | Meaning |
| --- | --- | --- |
| `QBITTORRENT_URL` | `http://qbittorrent:8080` | Internal qBittorrent Web API URL |
| `QBITTORRENT_USERNAME` | empty | Optional API username |
| `QBITTORRENT_PASSWORD_FILE` | empty | Optional file containing the API password |
| `DOWNLOAD_PATH` | `/downloads` | Dedicated torrent filesystem mount |
| `SOFT_CAP_BYTES` | `25769803776` | Admission cap (24 GiB) |
| `MIN_COMPLETED_AGE` | `24h` | Minimum time since valid completion time |
| `MIN_RATIO` | `1.0` | Minimum local qBittorrent ratio |
| `POLL_INTERVAL` | `30s` | Reconciliation interval |
| `REQUEST_TIMEOUT` | `10s` | Timeout for one API operation |
| `HEALTH_ADDR` | `:9091` | Internal health/status listener |
| `HEALTH_MAX_AGE` | `2m` | Maximum age of a successful poll |

Authentication is deliberately optional. For the private Tailscale deployment,
leave both authentication variables empty. If either one is configured, both
are required; the password is read from the mounted file and is never logged.

`GET /healthz` reports whether the latest reconciliation succeeded and is
recent. `GET /status` returns the last policy snapshot, including counts and
storage figures. These endpoints contain no credentials or torrent names.

The intended deployment mounts the dedicated 25 GiB payload filesystem at
`/downloads`, while using the 24 GiB default above as the working limit. The
image runs as UID/GID `1000:1000`, needs read/traverse access to that mount, and
needs network access only to qBittorrent. Port `9091` is internal health traffic;
it should not be published to the internet or Tailscale.

## Local verification

```sh
make check
```
