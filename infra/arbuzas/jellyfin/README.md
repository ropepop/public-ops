# Jellyfin release configuration

This directory owns the idempotent setup and drift check for the small
Jellyfin instance on kitty-gration. The container itself is declared in
`infra/arbuzas/docker/compose.yml`.

`bootstrap.py` has two modes:

```bash
python3 bootstrap.py bootstrap \
  --url http://127.0.0.1:18096 \
  --admin-password-file /etc/arbuzas/secrets/jellyfin/admin-password.secret

python3 bootstrap.py check \
  --url http://127.0.0.1:18096 \
  --admin-password-file /etc/arbuzas/secrets/jellyfin/admin-password.secret
```

The password file must be an absolute, root-owned, regular non-symlink with
mode `0600`. Its single-line value must contain 24 to 256 UTF-8 characters.
The helper never prints request bodies, the administrator password, or API
tokens.

Bootstrap owns these settings:

- hidden administrator `JellyfinAdmin`, protected by the root-only secret;
- visible, passwordless, non-administrator user `Media`;
- mixed `Torrent Library` rooted at read-only `/media`;
- direct play, remuxing, and audio conversion for `Media`, with video
  transcoding disabled;
- real-time library monitoring plus one daily fallback scan at 04:00;
- disabled discovery, UPnP, chapter images, trickplay, media-segment analysis,
  keyframe extraction, and subtitle/lyric download tasks;
- disk-backed transcoding work at `/transcodes`.

The operator-facing access, deployment, and recovery instructions are in
`docs/runbooks/JELLYFIN_TAILSCALE.md`.
