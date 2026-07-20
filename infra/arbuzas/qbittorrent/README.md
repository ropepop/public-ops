# qBittorrent host storage

This release directory owns the qBittorrent configuration reconciler and the
25 GiB capped ext4 storage mount used by the kitty-gration Compose project.

The mount unit places the image at `/srv/arbuzas/qbittorrent/storage`. Its
`config` and `payload` directories are the only host paths mounted into the
qBittorrent services. Run `install-storage.sh check` as root to verify the
image, mount source, safety options, marker, installed unit, and service state.

The operator-facing access and retention instructions live in
`docs/runbooks/QBITTORRENT_TAILSCALE.md` in the source repository.
