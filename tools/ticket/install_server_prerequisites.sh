#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-check}"
OS_RELEASE_FILE="${TICKET_BOOTSTRAP_OS_RELEASE_FILE:-/etc/os-release}"

case "${MODE}" in
  check|install) ;;
  *) echo "usage: install_server_prerequisites.sh [check|install]" >&2; exit 2 ;;
esac

[[ -r "${OS_RELEASE_FILE}" ]] || {
  echo "supported Debian/Ubuntu os-release metadata is required" >&2
  exit 1
}

OS_ID=""
OS_ID_LIKE=""
# shellcheck disable=SC1090
source "${OS_RELEASE_FILE}"
OS_ID="${ID:-}"
OS_ID_LIKE="${ID_LIKE:-}"
case " ${OS_ID} ${OS_ID_LIKE} " in
  *" debian "*|*" ubuntu "*) ;;
  *)
    echo "automatic VPS prerequisites support only Debian/Ubuntu hosts" >&2
    exit 1
    ;;
esac

as_root() {
  if [[ "$(id -u)" == "0" ]]; then
    "$@"
  else
    sudo -n "$@"
  fi
}

if [[ "$(id -u)" != "0" ]]; then
  command -v sudo >/dev/null 2>&1 || {
    echo "passwordless sudo is required for non-root VPS setup" >&2
    exit 1
  }
  sudo -n true
fi
command -v apt-get >/dev/null 2>&1 || {
  echo "apt-get is required for automatic VPS prerequisite installation" >&2
  exit 1
}
command -v apt-cache >/dev/null 2>&1 || {
  echo "apt-cache is required for safe package preflight" >&2
  exit 1
}

# Package availability is checked before apt-get update or any other mutation.
# This intentionally supports only images whose configured repositories already
# provide Docker Engine and Compose v2; it does not execute a convenience script
# or silently add a third-party repository.
apt-cache show docker.io >/dev/null 2>&1 || {
  echo "configured apt repositories do not provide docker.io" >&2
  exit 1
}
COMPOSE_PACKAGE=""
if apt-cache show docker-compose-v2 >/dev/null 2>&1; then
  COMPOSE_PACKAGE="docker-compose-v2"
elif apt-cache show docker-compose-plugin >/dev/null 2>&1; then
  COMPOSE_PACKAGE="docker-compose-plugin"
else
  echo "configured apt repositories do not provide Docker Compose v2" >&2
  exit 1
fi

if [[ "${MODE}" == "check" ]]; then
  printf 'server_prerequisites=available\n'
  exit 0
fi

as_root apt-get update
as_root env DEBIAN_FRONTEND=noninteractive apt-get install -y \
  ca-certificates curl docker.io python3 sudo tar "${COMPOSE_PACKAGE}"
if command -v systemctl >/dev/null 2>&1; then
  as_root systemctl enable --now docker
else
  as_root service docker start
fi
if [[ "$(id -u)" != "0" ]]; then
  as_root usermod -aG docker "$(id -un)"
fi
