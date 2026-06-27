#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

commit="${ARBUZAS_RELEASE_SOURCE_COMMIT:-}"
dirty="${ARBUZAS_RELEASE_SOURCE_DIRTY:-}"
release_id="${ARBUZAS_RELEASE_ID:-unknown}"
source_sha256="${ARBUZAS_RELEASE_SOURCE_SHA256:-unknown}"

if [[ -z "${commit}" || -z "${dirty}" ]]; then
  git_commit="nogit"
  git_dirty="unknown"
  if git -C "$ROOT_DIR" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git_commit="$(git -C "$ROOT_DIR" rev-parse --short=12 HEAD 2>/dev/null || echo "nogit")"
    git_dirty="clean"
    if ! git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD --; then
      git_dirty="dirty"
    fi
  fi
  commit="${commit:-${git_commit}}"
  dirty="${dirty:-${git_dirty}}"
fi

commit="${commit:-nogit}"
dirty="${dirty:-unknown}"

case "${dirty}" in
  clean | dirty | unknown) ;;
  false | 0 | no) dirty="clean" ;;
  *) dirty="dirty" ;;
esac

if [[ "${source_sha256}" == "" ]]; then
  source_sha256="unknown"
fi

if [[ "${release_id}" == "" ]]; then
  release_id="unknown"
fi

build_time="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

printf '%s' "-X phonebroker/internal/version.Commit=$commit -X phonebroker/internal/version.BuildTime=$build_time -X phonebroker/internal/version.Dirty=$dirty -X phonebroker/internal/version.ReleaseID=$release_id -X phonebroker/internal/version.SourceSHA256=$source_sha256"
