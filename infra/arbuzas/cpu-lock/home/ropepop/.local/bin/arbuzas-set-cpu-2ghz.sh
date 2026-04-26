#!/bin/sh
set -eu
PATH=/usr/bin:/bin
IMAGE_NAME="arbuzas/cpu-lock:latest"
STATE_DIR="$HOME/.local/state/arbuzas"
LOG_FILE="$STATE_DIR/cpu-lock.log"
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

  /usr/bin/docker run --rm --privileged --pid=host -v /sys:/sys -v /proc:/proc "$IMAGE_NAME" sh -lc '
    set -eu
    echo 1 > /sys/devices/system/cpu/intel_pstate/no_turbo
    cpupower frequency-set -g performance -d 2GHz -u 2GHz >/dev/null
    for c in 0 1 2 3; do
      min=$(cat /sys/devices/system/cpu/cpu${c}/cpufreq/scaling_min_freq)
      max=$(cat /sys/devices/system/cpu/cpu${c}/cpufreq/scaling_max_freq)
      gov=$(cat /sys/devices/system/cpu/cpu${c}/cpufreq/scaling_governor)
      [ "$min" = 2000000 ]
      [ "$max" = 2000000 ]
      [ "$gov" = performance ]
    done
  '

  printf '[%s] cpu lock applied\n' "$(date -Is)"
} >> "$LOG_FILE" 2>&1
