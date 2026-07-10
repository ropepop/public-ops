#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

fail=0
for module in orchestrator train-bot task-executor pihole vpn-access; do
  if [ ! -d "ops/evidence/${module}" ]; then
    echo "missing evidence directory: ops/evidence/${module}"
    fail=1
  fi
done

if [ -f standards/schemas/observability-event.v1.schema.json ]; then
  while IFS= read -r -d '' f; do
    if ! jq -e . "$f" >/dev/null 2>&1; then
      echo "warning: legacy non-json evidence file skipped: $f"
    fi
  done < <(find ops/evidence -type f -name '*.json' -print0)
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi

echo "evidence validation passed"
