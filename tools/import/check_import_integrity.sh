#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

required=(
  workloads/train-bot
  workloads/site-notifications
  automation/task-executor
  infra/pihole/secrets
)

for path in "${required[@]}"; do
  if [ ! -d "${path}" ]; then
    echo "missing required directory: ${path}" >&2
    exit 1
  fi
done

if find . -type d -name .git -not -path './.git' | grep -q .; then
  echo "nested .git directories detected" >&2
  find . -type d -name .git -not -path './.git' >&2
  exit 1
fi

count_check() {
  local path="$1"
  local min="$2"
  local count
  count="$(find "$path" -type f | wc -l | tr -d '[:space:]')"
  if [ "$count" -lt "$min" ]; then
    echo "insufficient file count in $path: got $count expected >= $min" >&2
    exit 1
  fi
}

count_check workloads/train-bot 40
count_check workloads/site-notifications 30
count_check automation/task-executor 10

echo "import integrity checks passed"
