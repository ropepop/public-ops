#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WORKSPACE_ROOT="$(cd "${REPO_ROOT}/../.." && pwd)"
# shellcheck source=../../../../tools/pixel/transport.sh
source "${WORKSPACE_ROOT}/tools/pixel/transport.sh"

PIXEL_RUN_ID="${PIXEL_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM}"
export PIXEL_TRANSPORT ADB_SERIAL PIXEL_SSH_HOST PIXEL_SSH_PORT PIXEL_RUN_ID

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*"
}

adb_cmd() {
  local subcommand="${1:-}"
  shift || true

	case "${subcommand}" in
	  get-state)
	    pixel_transport_require_device >/dev/null
	    printf 'device\n'
	    ;;
	  install)
	    if [[ "${1:-}" == "-r" ]]; then
	      shift
	    fi
	    pixel_transport_install_apk "$1"
	    ;;
	  push)
	    pixel_transport_push "$1" "$2"
	    ;;
	  pull)
	    pixel_transport_pull "$1" "$2"
	    ;;
	  forward)
	    case "${1:-}" in
        --remove)
          pixel_transport_forward_stop "${2#tcp:}"
          ;;
        tcp:*)
          pixel_transport_forward_start "${1#tcp:}" "${2#tcp:}"
          ;;
        *)
          echo "Unsupported adb_cmd forward invocation: ${subcommand} $*" >&2
          return 1
          ;;
      esac
      ;;
    shell)
      if [[ "${1:-}" == "su" && "${2:-}" == "-c" ]]; then
        shift 2
      fi
      pixel_transport_root_shell "$(printf '%s' "$*")"
      ;;
    *)
      local -a cmd=("${ADB_BIN}")
      if [[ -n "${ADB_SERIAL:-}" ]]; then
        cmd+=(-s "${ADB_SERIAL}")
      fi
      cmd+=("${subcommand}" "$@")
      "${cmd[@]}"
      ;;
  esac
}

adb_shell_root() {
  pixel_transport_root_shell "$1"
}

adb_shell_root_stdin() {
  pixel_transport_root_shell_stdin
}

transport_args() {
  local args=()
  pixel_transport_append_cli_args args
  printf '%s\n' "${args[@]}"
}

ensure_output_dirs() {
  mkdir -p "$REPO_ROOT/output/pixel" "$REPO_ROOT/output/agent-browser"
}

ensure_root() {
  ensure_device
  if ! pixel_transport_require_root >/dev/null 2>&1; then
    log "Root shell not available on target"
    exit 1
  fi
}

ensure_local_env() {
  if [[ ! -f "$REPO_ROOT/.env" ]]; then
    if [[ -f "$REPO_ROOT/.env.example" ]]; then
      cp "$REPO_ROOT/.env.example" "$REPO_ROOT/.env"
      log "Created .env from .env.example"
    else
      log "Missing .env and .env.example"
      exit 1
    fi
  fi

	if ! grep -q '^BOT_TOKEN=' "$REPO_ROOT/.env"; then
	  log "BOT_TOKEN is missing in .env"
	  exit 1
	fi

	if grep -q '^BOT_TOKEN=your_telegram_bot_token$' "$REPO_ROOT/.env"; then
	  log "BOT_TOKEN placeholder still set in .env"
	  exit 1
	fi
}

reserve_local_port() {
  python3 - <<'PY'
import socket

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
    sock.bind(("127.0.0.1", 0))
    print(sock.getsockname()[1])
PY
}

ensure_device() {
  if ! pixel_transport_require_device >/dev/null 2>&1; then
    log "Pixel transport is not ready (transport=${PIXEL_TRANSPORT})"
    exit 1
  fi
}
