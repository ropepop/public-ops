#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
WEB_CLIENT_DIR="${REPO_ROOT}/web-client"
OUT_FILE="${REPO_ROOT}/internal/web/static/live-client.js"

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*"
}

for cmd in node npm spacetime; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    log "Missing required command: ${cmd}"
    exit 1
  fi
done

if [[ ! -d "${WEB_CLIENT_DIR}/node_modules" ]]; then
  log "Installing web-client dependencies"
  (
    cd "${WEB_CLIENT_DIR}"
    npm install
  )
fi

log "Building generated live client from web-client sources"
(
  cd "${WEB_CLIENT_DIR}"
  npm run build
)

if [[ ! -s "${OUT_FILE}" ]]; then
  log "Expected generated asset is missing: ${OUT_FILE}"
  exit 1
fi
