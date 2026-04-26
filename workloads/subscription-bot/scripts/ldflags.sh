#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

commit="nogit"
dirty="unknown"

if git -C "$ROOT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  commit="$(git -C "$ROOT_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo "nogit")"
  dirty="clean"
  if ! git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD --; then
    dirty="dirty"
  fi
fi

build_time="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

printf '%s' "-X subscriptionbot/internal/version.Commit=$commit -X subscriptionbot/internal/version.BuildTime=$build_time -X subscriptionbot/internal/version.Dirty=$dirty"
