#!/bin/sh
set -eu

readonly marker="/downloads/.arbuzas-qbittorrent-volume"

if [ ! -f "$marker" ] || [ -L "$marker" ]; then
  echo "Refusing to start qBittorrent: the capped /downloads volume marker is missing or invalid: $marker" >&2
  exit 78
fi

if [ ! -x /init ]; then
  echo "Refusing to start qBittorrent: the LinuxServer /init program is unavailable." >&2
  exit 70
fi

exec /init "$@"
