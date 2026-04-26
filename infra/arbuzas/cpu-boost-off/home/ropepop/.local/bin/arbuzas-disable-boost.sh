#!/bin/sh
set -eu
PATH=/usr/bin:/bin
IMAGE_NAME="arbuzas/cpu-boost-off:latest"
STATE_DIR="$HOME/.local/state/arbuzas"
LOG_FILE="$STATE_DIR/cpu-boost-off.log"
mkdir -p "$STATE_DIR"

{
  printf '[%s] waiting for docker\n' "$(date -Is)"
  i=0
  while ! /usr/bin/docker info >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 60 ]; then
      echo 'docker did not become ready within 5 minutes'
      exit 1
    fi
    sleep 5
  done

  if ! /usr/bin/docker image inspect "$IMAGE_NAME" >/dev/null 2>&1; then
    echo "missing image: $IMAGE_NAME"
    exit 1
  fi

  /usr/bin/docker run --rm --privileged --pid=host -v /sys:/sys "$IMAGE_NAME" sh -lc '
    set -eu
    echo 1 > /sys/devices/system/cpu/intel_pstate/no_turbo
    [ "$(cat /sys/devices/system/cpu/intel_pstate/no_turbo)" = "1" ]
  '

  printf '[%s] boost disabled\n' "$(date -Is)"
} >> "$LOG_FILE" 2>&1
