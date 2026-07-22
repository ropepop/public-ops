#!/usr/bin/env bash
set -euo pipefail

# Authenticated schema-only inventory. This command deliberately uses only
# `spacetime list` and `spacetime describe`; it never reads table rows.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON_BIN="${OPERATIONAL_LOG_INVENTORY_PYTHON:-python3}"

if [[ "${PYTHON_BIN}" == */* ]]; then
  [[ -x "${PYTHON_BIN}" ]] || {
    printf 'private log-source inventory: Python is unavailable\n' >&2
    exit 4
  }
else
  command -v "${PYTHON_BIN}" >/dev/null 2>&1 || {
    printf 'private log-source inventory: Python is unavailable\n' >&2
    exit 4
  }
fi

exec "${PYTHON_BIN}" "${SCRIPT_DIR}/inventory-private-log-sources.py" "$@"
