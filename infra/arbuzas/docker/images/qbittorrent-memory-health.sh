#!/bin/sh
set -eu

cgroup_root="${1:-/sys/fs/cgroup}"
expected_memory_max="${2:-805306368}"

for file in memory.max memory.swap.max memory.events; do
  if [ ! -r "${cgroup_root}/${file}" ]; then
    echo "qBittorrent memory health: unreadable ${cgroup_root}/${file}" >&2
    exit 1
  fi
done

require_line() {
  expected="$1"
  file="$2"
  if ! grep -Fx "${expected}" "${file}" >/dev/null; then
    echo "qBittorrent memory health: expected '${expected}' in ${file}" >&2
    exit 1
  fi
}

require_line "${expected_memory_max}" "${cgroup_root}/memory.max"
require_line '0' "${cgroup_root}/memory.swap.max"
require_line 'oom 0' "${cgroup_root}/memory.events"
require_line 'oom_kill 0' "${cgroup_root}/memory.events"
