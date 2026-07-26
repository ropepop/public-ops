#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DOCKER_ROOT="${REPO_ROOT}/infra/arbuzas/docker"
DOCKER_DEFAULT_ENV_FILE="${DOCKER_ROOT}/env/arbuzas.env"
LOCAL_RELEASES_ROOT="${REPO_ROOT}/output/arbuzas/releases"
HOST_MIRROR_SCRIPT="${REPO_ROOT}/tools/arbuzas/host_mirror.py"
HOST_MIRROR_ROOT="${ARBUZAS_HOST_MIRROR_ROOT:-${REPO_ROOT}/infra/arbuzas/host-mirror}"
HOST_MIRROR_PROFILE="${ARBUZAS_HOST_MIRROR_PROFILE:-arbuzas}"
REMOTE_RELEASES_ROOT="/etc/arbuzas/releases"
REMOTE_CURRENT_LINK="/etc/arbuzas/current"
DOCKER_GC_SCRIPT="${SCRIPT_DIR}/docker_gc.py"
LOCAL_RELEASE_GC_SCRIPT="${SCRIPT_DIR}/local_release_gc.py"
MEMORY_REPORT_SCRIPT="${SCRIPT_DIR}/memory_report.py"
DOCKER_GC_REMOTE_STATE_DIR="/etc/arbuzas/docker-gc"
DOCKER_GC_REMOTE_STATE_FILE="${DOCKER_GC_REMOTE_STATE_DIR}/state.json"
DOCKER_GC_BUILD_CACHE_UNTIL="${DOCKER_GC_BUILD_CACHE_UNTIL:-24h}"
DOCKER_GC_RELEASE_KEEP_PER_FAMILY="${DOCKER_GC_RELEASE_KEEP_PER_FAMILY:-10}"
ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS="${ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS:-72}"
ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY="${ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY:-10}"
ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN="${ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN:-false}"
ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS="${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS:-7}"
ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE="${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE:-100M}"
ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE="${ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE:-true}"
ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS="${ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS:-45}"
NETDATA_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/netdata"
NETDATA_REMOTE_CONFIG_DIR="/etc/netdata"
NETDATA_REMOTE_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/netdata.conf"
NETDATA_REMOTE_DOCKER_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/go.d/docker.conf"
NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/go.d/sd/docker.conf"
NETDATA_REMOTE_SYSTEMD_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/go.d/systemdunits.conf"
NETDATA_DASHBOARD_WEB_ROOT="${NETDATA_CONFIG_ROOT}/web/kitty-gration"
NETDATA_NATIVE_DASHBOARD_ROOT="${NETDATA_CONFIG_ROOT}/native-dashboard"
NETDATA_REMOTE_WEB_ROOT="/usr/share/netdata/web"
NETDATA_REMOTE_DASHBOARD_DIR="${NETDATA_REMOTE_WEB_ROOT}/kitty-gration"
NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER="/usr/local/libexec/arbuzas-netdata-native-dashboard.py"
NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE="/etc/systemd/system/arbuzas-netdata-native-dashboard.service"
NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN="/etc/systemd/system/netdata.service.d/20-arbuzas-native-dashboard.conf"
NETDATA_KICKSTART_URL="${NETDATA_KICKSTART_URL:-https://get.netdata.cloud/kickstart.sh}"
MEMORY_REPORT_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/memory-report"
MEMORY_REPORT_REMOTE_SERVICE_FILE="/etc/systemd/system/arbuzas-memory-report.service"
MEMORY_REPORT_REMOTE_TIMER_FILE="/etc/systemd/system/arbuzas-memory-report.timer"
MEMORY_REPORT_REMOTE_DEFAULT_FILE="/etc/default/arbuzas-memory-report"
MEMORY_REPORT_REMOTE_SCRIPT_FILE="/usr/local/libexec/arbuzas-memory-report.py"
MEMORY_REPORT_REMOTE_OUTPUT_DIR="/var/lib/arbuzas/memory-report"
MEMORY_REPORT_REMOTE_JSON_FILE="${MEMORY_REPORT_REMOTE_OUTPUT_DIR}/latest.json"
MEMORY_REPORT_REMOTE_TEXT_FILE="${MEMORY_REPORT_REMOTE_OUTPUT_DIR}/latest.txt"
MEMORY_REPORT_REMOTE_PROM_FILE="${MEMORY_REPORT_REMOTE_OUTPUT_DIR}/latest.prom"
THINKPAD_FAN_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/thinkpad-fan"
THINKPAD_FAN_REMOTE_SERVICE_FILE="/etc/systemd/system/arbuzas-thinkpad-fan.service"
THINKPAD_FAN_REMOTE_DEFAULT_FILE="/etc/default/arbuzas-thinkpad-fan"
THINKPAD_FAN_REMOTE_MODPROBE_FILE="/etc/modprobe.d/arbuzas-thinkpad-fan.conf"
THINKPAD_FAN_REMOTE_SCRIPT_FILE="/usr/local/libexec/arbuzas-thinkpad-fan.py"
THINKPAD_FAN_REMOTE_PROC_FILE="/proc/acpi/ibm/fan"
THINKPAD_FAN_REMOTE_PARAM_FILE="/sys/module/thinkpad_acpi/parameters/fan_control"
THINKPAD_FAN_REMOTE_TEMP_GLOB="/sys/devices/platform/thinkpad_hwmon/hwmon/hwmon*/temp1_input"
QBITTORRENT_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/qbittorrent"
QBITTORRENT_REMOTE_ROOT="/srv/arbuzas/qbittorrent"
QBITTORRENT_REMOTE_CONFIG_FILE="${QBITTORRENT_REMOTE_ROOT}/storage/config/qBittorrent/qBittorrent.conf"
JELLYFIN_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/jellyfin"
JELLYFIN_REMOTE_ROOT="/srv/arbuzas/jellyfin"
JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE="/etc/arbuzas/secrets/jellyfin/admin-password.secret"
ROOT_FALLBACK_IMAGE="${ROOT_FALLBACK_IMAGE:-debian:13-slim}"

if [[ -f "${DOCKER_DEFAULT_ENV_FILE}" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "${DOCKER_DEFAULT_ENV_FILE}"
  set +a
fi

ARBUZAS_HOST="${ARBUZAS_HOST:-kitty-gration}"
ARBUZAS_USER="${ARBUZAS_USER:-${USER}}"
ARBUZAS_SSH_PORT="${ARBUZAS_SSH_PORT:-}"
ARBUZAS_SSH_KNOWN_HOSTS_FILE="${ARBUZAS_SSH_KNOWN_HOSTS_FILE:-}"
ARBUZAS_TZ="${ARBUZAS_TZ:-Europe/Riga}"
ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
ARBUZAS_RELEASE_DIR="${ARBUZAS_RELEASE_DIR:-${LOCAL_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}}"

ARBUZAS_TRAIN_BOT_PORT="${ARBUZAS_TRAIN_BOT_PORT:-9317}"
ARBUZAS_SATIKSME_BOT_PORT="${ARBUZAS_SATIKSME_BOT_PORT:-9318}"
ARBUZAS_SUBSCRIPTION_BOT_PORT="${ARBUZAS_SUBSCRIPTION_BOT_PORT:-9320}"
ARBUZAS_TICKET_REMOTE_PORT="${ARBUZAS_TICKET_REMOTE_PORT:-9338}"
ARBUZAS_TICKET_PHONE_ADB_TARGET="${ARBUZAS_TICKET_PHONE_ADB_TARGET:-100.76.50.43:5555}"
ARBUZAS_TICKET_TUNNEL_UID="${ARBUZAS_TICKET_TUNNEL_UID:-501}"
ARBUZAS_TICKET_TUNNEL_GID="${ARBUZAS_TICKET_TUNNEL_GID:-50}"
ARBUZAS_NETDATA_PORT="${ARBUZAS_NETDATA_PORT:-19999}"
ARBUZAS_QBITTORRENT_WEBUI_PORT="${ARBUZAS_QBITTORRENT_WEBUI_PORT:-18080}"
ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT="${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT:-24680}"
ARBUZAS_QBITTORRENT_PEER_PORT="${ARBUZAS_QBITTORRENT_PEER_PORT:-45123}"
ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT="${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT:-24680}"
ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME="${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME:-arbuzas-vps.tail9345a.ts.net}"
ARBUZAS_QBITTORRENT_PUID="${ARBUZAS_QBITTORRENT_PUID:-1000}"
ARBUZAS_QBITTORRENT_PGID="${ARBUZAS_QBITTORRENT_PGID:-1000}"
ARBUZAS_JELLYFIN_HOST_PORT="${ARBUZAS_JELLYFIN_HOST_PORT:-18096}"
ARBUZAS_JELLYFIN_INTERNAL_PORT="${ARBUZAS_JELLYFIN_INTERNAL_PORT:-8096}"
ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT="${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT:-29096}"
ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME="${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME:-arbuzas-vps.tail9345a.ts.net}"
ARBUZAS_JELLYFIN_PUID="${ARBUZAS_JELLYFIN_PUID:-1000}"
ARBUZAS_JELLYFIN_PGID="${ARBUZAS_JELLYFIN_PGID:-1000}"
ARBUZAS_TAILSCALE_IPV4="${ARBUZAS_TAILSCALE_IPV4:-}"
ARBUZAS_FAN_ENTER_AUTO_C="${ARBUZAS_FAN_ENTER_AUTO_C:-89}"
ARBUZAS_FAN_EXIT_AUTO_C="${ARBUZAS_FAN_EXIT_AUTO_C:-89}"

ARBUZAS_TRAIN_BOT_HOSTNAME="${ARBUZAS_TRAIN_BOT_HOSTNAME:-vilciens.kontrole.info}"
ARBUZAS_SATIKSME_BOT_HOSTNAME="${ARBUZAS_SATIKSME_BOT_HOSTNAME:-kontrole.info}"
ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME="${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME:-farel-subscription-bot.jolkins.id.lv}"
ARBUZAS_TICKET_REMOTE_HOSTNAME="${ARBUZAS_TICKET_REMOTE_HOSTNAME:-ticket.jolkins.id.lv}"
ARBUZAS_CLOUDFLARED_IMAGE="${ARBUZAS_CLOUDFLARED_IMAGE:-cloudflare/cloudflared:latest}"
ARBUZAS_TICKET_CLOUDFLARED_IMAGE="${ARBUZAS_TICKET_CLOUDFLARED_IMAGE:-cloudflare/cloudflared@sha256:12ff5c6992a9863db4da270746af7c244bcaee49353039af8104268a18d6c4f0}"

action=""
requested_release_id=""
VALIDATION_PROFILE="${ARBUZAS_VALIDATION_PROFILE:-full}"
VALIDATION_PROFILE_OPTION_SET=0
TARGETED_MODE=0
VALIDATE_TRAIN=0
VALIDATE_SATIKSME=0
VALIDATE_SUBSCRIPTION=0
VALIDATE_TICKET_PHONE_BRIDGE=0
VALIDATE_TICKET_REMOTE=0
VALIDATE_QBITTORRENT=0
VALIDATE_JELLYFIN=0
VALIDATE_TINY_VLESS=0
REQUESTED_SERVICES=()
COMPOSE_TARGET_SERVICES=()
DIAGNOSTIC_SERVICES=()
FAST_RELEASE_OVERLAY_PATHS=()
RUN_STARTED_MILLIS=""
DEPLOYMENT_TIMING_REPORTER="${REPO_ROOT}/workloads/operational-logging/scripts/report-deployment.sh"
DEPLOYMENT_TIMING_ACTIVE=0
DEPLOYMENT_TIMING_FINALIZED=0
DEPLOYMENT_TIMING_RUN_ID=""
DEPLOYMENT_TIMING_ACTION=""
DEPLOYMENT_TIMING_RELEASE_ID="none"
DEPLOYMENT_TIMING_PROFILE="none"
DEPLOYMENT_TIMING_TARGET="none"
DEPLOYMENT_TIMING_PHASE_BUNDLE="-"
DEPLOYMENT_TIMING_EXIT_CLEANUP_PATH=""
QBITTORRENT_SERVE_ADDED=0
JELLYFIN_SERVE_ADDED=0
JELLYFIN_SECRET_CREATED=0

ALL_SERVICES=(
  train_bot
  satiksme_bot
  subscription_bot
  ticket_phone_bridge
  ticket_remote_spacetime_sidecar
  ticket_remote
  train_tunnel
  satiksme_tunnel
  subscription_tunnel
  ticket_remote_tunnel
  qbittorrent
  qbittorrent_housekeeper
  jellyfin
  tiny_vless
)

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*" >&2
}

# Deployment timing is sent to the canonical private operational log and stays
# detached from the deploy path. The reporter only receives compact identifiers
# and durations, and a missing or failed reporter never alters the command's
# exit status.
deployment_timing_safe_token_or_none() {
  local value="${1:-}"
  local max_length="$2"

  if [[ -n "${value}" && ${#value} -le "${max_length}" && "${value}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/@=-]*$ ]]; then
    printf '%s' "${value}"
  else
    printf 'none'
  fi
}

deployment_timing_now_millis() {
  local realtime="${EPOCHREALTIME:-}"
  local realtime_seconds=""
  local realtime_fraction=""
  local python_bin="${DEPLOYMENT_TIMING_CLOCK_PYTHON_BIN:-/usr/bin/python3}"
  local python_value=""

  if [[ ! -x "${python_bin}" ]]; then
    python_bin="$(command -v python3 || true)"
  fi
  if [[ -n "${python_bin}" ]]; then
    python_value="$("${python_bin}" -c 'import time; clock = getattr(time, "CLOCK_MONOTONIC", None); now = time.clock_gettime_ns(clock) if clock is not None and hasattr(time, "clock_gettime_ns") else time.monotonic_ns(); print(now // 1_000_000)' 2>/dev/null || true)"
    if [[ "${python_value}" =~ ^[0-9]+$ ]]; then
      printf '%s' "${python_value}"
      return 0
    fi
  fi

  if [[ "${realtime}" =~ ^([0-9]+)\.([0-9]+)$ ]]; then
    realtime_seconds="${BASH_REMATCH[1]}"
    realtime_fraction="${BASH_REMATCH[2]}000"
    realtime_fraction="${realtime_fraction:0:3}"
    printf '%s' "$(( 10#${realtime_seconds} * 1000 + 10#${realtime_fraction} ))"
    return 0
  fi

  printf '%s' "$(( SECONDS * 1000 ))"
}

deployment_timing_elapsed_millis() {
  local started_millis="$1"
  local finished_millis="$2"
  if (( finished_millis >= started_millis )); then
    printf '%s' "$(( finished_millis - started_millis ))"
  else
    printf '0'
  fi
}

deployment_timing_total_millis() {
  local finished_millis=""
  local started_millis="${RUN_STARTED_MILLIS:-}"

  finished_millis="$(deployment_timing_now_millis)"
  if [[ -z "${started_millis}" ]]; then
    started_millis="${finished_millis}"
  fi
  deployment_timing_elapsed_millis "${started_millis}" "${finished_millis}"
}

deployment_timing_report() {
  local python_bin=""
  [[ "${DEPLOYMENT_TIMING_ACTIVE}" == "1" && -x "${DEPLOYMENT_TIMING_REPORTER}" ]] || return 0
  python_bin="${OPERATIONAL_LOGGING_PYTHON_BIN:-/usr/bin/python3}"
  if [[ ! -x "${python_bin}" ]]; then
    python_bin="$(command -v python3 || true)"
  fi
  [[ -n "${python_bin}" ]] || return 0

  # The worker is detached from the deployment, then waits for its one
  # Spacetime call. Avoiding a second detach makes final events reliable while
  # keeping local CLI setup and network latency off the critical path.
  "${python_bin}" -c \
    'import subprocess, sys; subprocess.Popen(sys.argv[1:], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, close_fds=True, start_new_session=True)' \
    "${DEPLOYMENT_TIMING_REPORTER}" "$@" --wait >/dev/null 2>&1 || true
  return 0
}

deployment_timing_append_phase() {
  local phase_name="$1"
  local phase_status="$2"
  local phase_duration_millis="$3"
  local phase_total_duration_millis="$4"
  local phase_record="${phase_name}=${phase_status}=${phase_duration_millis}=${phase_total_duration_millis}"

  if [[ "${DEPLOYMENT_TIMING_PHASE_BUNDLE}" == "-" ]]; then
    DEPLOYMENT_TIMING_PHASE_BUNDLE="${phase_record}"
  else
    DEPLOYMENT_TIMING_PHASE_BUNDLE+="@${phase_record}"
  fi
}

deployment_timing_finish() {
  local exit_code="$1"
  local status="failed"

  [[ "${DEPLOYMENT_TIMING_ACTIVE}" == "1" && "${DEPLOYMENT_TIMING_FINALIZED}" == "0" ]] || return 0
  DEPLOYMENT_TIMING_FINALIZED=1
  case "${exit_code}" in
    0)
      status="ok"
      ;;
    130|143)
      status="cancelled"
      ;;
  esac

  deployment_timing_report run-complete \
    --run-id "${DEPLOYMENT_TIMING_RUN_ID}" \
    --source ops \
    --action "${DEPLOYMENT_TIMING_ACTION}" \
    --status "${status}" \
    --total-duration-ms "$(deployment_timing_total_millis)" \
    --phase-bundle "${DEPLOYMENT_TIMING_PHASE_BUNDLE}" \
    --release-id "${DEPLOYMENT_TIMING_RELEASE_ID}" \
    --profile "${DEPLOYMENT_TIMING_PROFILE}" \
    --target "${DEPLOYMENT_TIMING_TARGET}"
}

deployment_timing_on_exit() {
  local exit_code="$1"
  trap - EXIT INT TERM

  if [[ -n "${DEPLOYMENT_TIMING_EXIT_CLEANUP_PATH}" ]]; then
    rm -f -- "${DEPLOYMENT_TIMING_EXIT_CLEANUP_PATH}" || true
  fi
  deployment_timing_finish "${exit_code}" || true
  exit "${exit_code}"
}

deployment_timing_on_signal() {
  local signal_name="$1"

  trap - INT TERM
  case "${signal_name}" in
    INT)
      exit 130
      ;;
    TERM)
      exit 143
      ;;
  esac
}

start_deployment_timing_reporting() {
  case "${action}" in
    deploy|validate|rollback|deploy-config)
      ;;
    *)
      return 0
      ;;
  esac
  [[ -x "${DEPLOYMENT_TIMING_REPORTER}" ]] || return 0

  DEPLOYMENT_TIMING_ACTIVE=1
  RUN_STARTED_MILLIS="$(deployment_timing_now_millis)"
  DEPLOYMENT_TIMING_ACTION="${action}"
  DEPLOYMENT_TIMING_RUN_ID="ops-${action}-$(date -u +%Y%m%dT%H%M%SZ)-$$"
  if [[ -n "${requested_release_id}" ]]; then
    DEPLOYMENT_TIMING_RELEASE_ID="$(deployment_timing_safe_token_or_none "${requested_release_id}" 160)"
  elif [[ "${action}" == "validate" || "${action}" == "deploy-config" ]]; then
    DEPLOYMENT_TIMING_RELEASE_ID="current"
  elif [[ "${action}" == "rollback" ]]; then
    DEPLOYMENT_TIMING_RELEASE_ID="none"
  else
    DEPLOYMENT_TIMING_RELEASE_ID="$(deployment_timing_safe_token_or_none "${ARBUZAS_RELEASE_ID}" 160)"
  fi
  DEPLOYMENT_TIMING_TARGET="$(deployment_timing_safe_token_or_none "${ARBUZAS_HOST}" 160)"
  if [[ "${action}" == "deploy-config" ]]; then
    DEPLOYMENT_TIMING_PROFILE="none"
  else
    DEPLOYMENT_TIMING_PROFILE="$(deployment_timing_safe_token_or_none "${VALIDATION_PROFILE}" 48)"
  fi

  trap 'deployment_timing_on_exit "$?"' EXIT
  trap 'deployment_timing_on_signal INT' INT
  trap 'deployment_timing_on_signal TERM' TERM
  deployment_timing_report run-start \
    --run-id "${DEPLOYMENT_TIMING_RUN_ID}" \
    --source ops \
    --action "${DEPLOYMENT_TIMING_ACTION}" \
    --release-id "${DEPLOYMENT_TIMING_RELEASE_ID}" \
    --profile "${DEPLOYMENT_TIMING_PROFILE}" \
    --target "${DEPLOYMENT_TIMING_TARGET}"
}

run_timed_phase() {
  local phase_name="$1"
  shift
  local phase_started_millis=""
  local phase_finished_millis=""
  local phase_status="ok"
  local phase_exit_code=0
  local phase_duration_millis=0
  local phase_total_duration_millis=0

  phase_started_millis="$(deployment_timing_now_millis)"
  log "Phase start: ${phase_name} profile=${VALIDATION_PROFILE}"
  if "$@"; then
    phase_exit_code=0
  else
    phase_exit_code=$?
    phase_status="failed"
  fi
  phase_finished_millis="$(deployment_timing_now_millis)"
  phase_duration_millis="$(deployment_timing_elapsed_millis "${phase_started_millis}" "${phase_finished_millis}")"
  phase_total_duration_millis="$(deployment_timing_total_millis)"
  log "Phase timing: phase=${phase_name} status=${phase_status} duration_millis=${phase_duration_millis} total_millis=${phase_total_duration_millis} profile=${VALIDATION_PROFILE}"
  if [[ "${DEPLOYMENT_TIMING_ACTIVE}" == "1" ]]; then
    deployment_timing_append_phase "${phase_name}" "${phase_status}" "${phase_duration_millis}" "${phase_total_duration_millis}"
  fi
  return "${phase_exit_code}"
}

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "Missing required command: ${cmd}" >&2
    exit 1
  fi
}

remote_target() {
  printf '%s@%s' "${ARBUZAS_USER}" "${ARBUZAS_HOST}"
}

run_ssh() {
  local -a args=()
  if [[ -n "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" ]]; then
    args+=(-o StrictHostKeyChecking=yes -o "UserKnownHostsFile=${ARBUZAS_SSH_KNOWN_HOSTS_FILE}")
  fi
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(-p "${ARBUZAS_SSH_PORT}")
  fi
  if (( ${#args[@]} > 0 )); then
    ssh "${args[@]}" "$@"
  else
    ssh "$@"
  fi
}

run_scp() {
  local -a args=()
  if [[ -n "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" ]]; then
    args+=(-o StrictHostKeyChecking=yes -o "UserKnownHostsFile=${ARBUZAS_SSH_KNOWN_HOSTS_FILE}")
  fi
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(-P "${ARBUZAS_SSH_PORT}")
  fi
  if (( ${#args[@]} > 0 )); then
    scp "${args[@]}" "$@"
  else
    scp "$@"
  fi
}

shell_quote() {
  printf '%q' "$1"
}

remote_shell() {
  local script="$1"
  {
    printf '%s\n' 'set -euo pipefail'
    printf '%s\n' "${script}"
  } | run_ssh "$(remote_target)" 'bash -s'
}

remote_root_shell() {
  local script="$1"
  {
    printf '%s\n' 'set -euo pipefail'
    printf '%s\n' "${script}"
  } | run_ssh "$(remote_target)" '
    if [[ "$(id -u)" -eq 0 ]]; then
      exec bash -s
    fi
    if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
      exec sudo -n bash -s
    fi
    command -v docker >/dev/null 2>&1 || {
      echo "root, passwordless sudo, or Docker access is required on this host" >&2
      exit 1
    }
    docker info >/dev/null 2>&1 || {
      echo "Docker access is required for the root fallback on this host" >&2
      exit 1
    }
    echo "sudo unavailable; using Docker root fallback via chroot" >&2
    exec docker run --rm -i --privileged \
      --pid=host \
      --network=host \
      --uts=host \
      --ipc=host \
      -v /:/host \
      -v /proc:/host/proc \
      -v /sys:/host/sys \
      -v /dev:/host/dev \
      -v /run:/host/run \
      "'"${ROOT_FALLBACK_IMAGE}"'" \
      chroot /host bash -s
  '
}

remote_inline_shell() {
  local script="$1"
  local script_base64=""
  local attempt=0

  script_base64="$(printf '%s\n' 'set -euo pipefail' "${script}" | base64 | tr -d '\n')"
  for attempt in 1 2 3; do
    if run_ssh \
      -o ConnectTimeout=15 \
      -o ServerAliveInterval=15 \
      -o ServerAliveCountMax=3 \
      "$(remote_target)" \
      "printf '%s' '${script_base64}' | base64 -d | bash -s"; then
      return 0
    fi
    if (( attempt < 3 )); then
      log "Remote command attempt ${attempt} failed on ${ARBUZAS_HOST}; retrying"
      sleep 2
    fi
  done

  return 1
}

remote_root_command() {
  local script="$1"
  local max_attempts="${2:-3}"
  local script_base64=""
  local attempt=0

  [[ "${max_attempts}" =~ ^[1-9][0-9]*$ ]] || {
    echo "remote_root_command retry count must be a positive integer" >&2
    return 2
  }
  script_base64="$(printf '%s\n' 'set -euo pipefail' "${script}" | base64 | tr -d '\n')"
  for (( attempt = 1; attempt <= max_attempts; attempt++ )); do
    if run_ssh \
      -o ConnectTimeout=15 \
      -o ServerAliveInterval=15 \
      -o ServerAliveCountMax=3 \
      "$(remote_target)" "
      if [[ \"\$(id -u)\" -eq 0 ]]; then
        exec bash -lc \"printf '%s' '${script_base64}' | base64 -d | bash -s\"
      fi
      if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
        exec sudo -n bash -lc \"printf '%s' '${script_base64}' | base64 -d | bash -s\"
      fi
      command -v docker >/dev/null 2>&1 || {
        echo 'root, passwordless sudo, or Docker access is required on this host' >&2
        exit 1
      }
      docker info >/dev/null 2>&1 || {
        echo 'Docker access is required for the root fallback on this host' >&2
        exit 1
      }
      echo 'sudo unavailable; using Docker root fallback via chroot' >&2
      exec docker run --rm -i --privileged \
        --pid=host \
        --network=host \
        --uts=host \
        --ipc=host \
        -v /:/host \
        -v /proc:/host/proc \
        -v /sys:/host/sys \
        -v /dev:/host/dev \
        -v /run:/host/run \
        '${ROOT_FALLBACK_IMAGE}' \
        chroot /host bash -lc \"printf '%s' '${script_base64}' | base64 -d | bash -s\"
    "; then
      return 0
    fi
    if (( attempt < max_attempts )); then
      log "Remote root command attempt ${attempt} failed on ${ARBUZAS_HOST}; retrying"
      sleep 2
    fi
  done

  return 1
}

remote_compose_shell() {
  local remote_release_dir="$1"
  local script="$2"
  remote_shell "
    compose() {
      docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' \"\$@\"
    }

    wait_until_ok() {
      wait_until_ok_for 90 \"\$@\"
    }

    wait_until_ok_for() {
      local timeout_seconds=\"\$1\"
      shift
      local deadline=\$((SECONDS + timeout_seconds))
      while true; do
        if \"\$@\"; then
          return 0
        fi
        if (( SECONDS >= deadline )); then
          return 1
        fi
        sleep 5
      done
    }

    ${script}
  "
}

resolve_local_docker_gc_script() {
  local candidate=""

  for candidate in \
    "${DOCKER_GC_SCRIPT}" \
    "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/docker_gc.py"; do
    if [[ -f "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

run_local_release_cleanup() {
  local protect_release_id="${1:-${ARBUZAS_RELEASE_ID}}"
  local -a cleanup_args

  [[ -f "${LOCAL_RELEASE_GC_SCRIPT}" ]] || {
    echo "missing local release cleanup helper: ${LOCAL_RELEASE_GC_SCRIPT}" >&2
    return 1
  }
  if [[ ! "${ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS}" =~ ^[0-9]+$ ]]; then
    echo "ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS must be a non-negative integer" >&2
    return 2
  fi
  if [[ ! "${ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY}" =~ ^[0-9]+$ ]]; then
    echo "ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY must be a non-negative integer" >&2
    return 2
  fi
  case "${ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN}" in
    true|false)
      ;;
    *)
      echo "ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN must be true or false" >&2
      return 2
      ;;
  esac

  cleanup_args=(
    python3 "${LOCAL_RELEASE_GC_SCRIPT}"
    --releases-root "${LOCAL_RELEASES_ROOT}"
    --protect-release-id "${protect_release_id}"
    --max-age-hours "${ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS}"
    --keep-per-family "${ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY}"
  )
  if [[ "${ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN}" == "true" ]]; then
    cleanup_args+=(--dry-run)
  fi
  "${cleanup_args[@]}"
}

remote_run_docker_gc() {
  local gc_script=""

  if [[ ! "${DOCKER_GC_RELEASE_KEEP_PER_FAMILY}" =~ ^[0-9]+$ ]]; then
    echo "DOCKER_GC_RELEASE_KEEP_PER_FAMILY must be a non-negative integer" >&2
    return 2
  fi

  if gc_script="$(resolve_local_docker_gc_script)"; then
    run_ssh "$(remote_target)" \
      "sudo -n python3 - --current-link '${REMOTE_CURRENT_LINK}' --releases-root '${REMOTE_RELEASES_ROOT}' --state-file '${DOCKER_GC_REMOTE_STATE_FILE}' --build-cache-until '${DOCKER_GC_BUILD_CACHE_UNTIL}' --release-keep-per-family '${DOCKER_GC_RELEASE_KEEP_PER_FAMILY}'" \
      < "${gc_script}"
    return 0
  fi

  remote_shell "
    gc_script='${REMOTE_CURRENT_LINK}/tools/arbuzas/docker_gc.py'
    [[ -f \"\${gc_script}\" ]] || {
      echo 'missing Docker GC helper locally and on the current Arbuzas release bundle' >&2
      exit 1
    }
    sudo -n python3 \"\${gc_script}\" \
      --current-link '${REMOTE_CURRENT_LINK}' \
      --releases-root '${REMOTE_RELEASES_ROOT}' \
      --state-file '${DOCKER_GC_REMOTE_STATE_FILE}' \
      --build-cache-until '${DOCKER_GC_BUILD_CACHE_UNTIL}' \
      --release-keep-per-family '${DOCKER_GC_RELEASE_KEEP_PER_FAMILY}'
  "
}

remote_run_memory_report() {
  [[ -f "${MEMORY_REPORT_SCRIPT}" ]] || {
    echo "missing memory reporter: ${MEMORY_REPORT_SCRIPT}" >&2
    return 1
  }

  run_ssh "$(remote_target)" \
    "python3 - --source-label '/proc/meminfo on ${ARBUZAS_HOST}'" \
    < "${MEMORY_REPORT_SCRIPT}"
}

remote_run_host_cache_cleanup() {
  if [[ ! "${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS}" =~ ^[0-9]+$ ]]; then
    echo "ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS must be a non-negative integer" >&2
    return 2
  fi
  if [[ ! "${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE}" =~ ^[0-9]+[KMGTP]?$ ]]; then
    echo "ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE must be a systemd size such as 100M" >&2
    return 2
  fi
  case "${ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE}" in
    true|false)
      ;;
    *)
      echo "ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE must be true or false" >&2
      return 2
      ;;
  esac

  remote_root_command "
    tmp_min_age_days='${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS}'
    journal_max_size='${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE}'
    drop_reclaimable_cache='${ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE}'
    report_memory() {
      local label=\"\$1\"
      if command -v free >/dev/null 2>&1; then
        echo \"host memory \${label}:\"
        free -m
      fi
    }
    if command -v apt-get >/dev/null 2>&1; then
      apt-get clean
    fi
    if [[ -d /tmp ]]; then
      tmp_mtime_days=\$(( tmp_min_age_days > 0 ? tmp_min_age_days - 1 : 0 ))
      find /tmp -xdev -mindepth 1 -maxdepth 1 \
        \\( -name 'arbuzas-*' \
          -o -name 'satiksme-*' \
          -o -name 'chat-analyzer-*' \
          -o -name 'ticket-*' \
          -o -name 'speedtest-install.*' \\) \
        -mtime +\"\${tmp_mtime_days}\" \
        -exec rm -rf -- {} +
    fi
    if command -v journalctl >/dev/null 2>&1; then
      journalctl --vacuum-size=\"\${journal_max_size}\"
    fi
    if [[ \"\${drop_reclaimable_cache}\" == 'true' ]]; then
      report_memory 'before reclaimable cache flush'
      sync
      if [[ ! -w /proc/sys/vm/drop_caches ]]; then
        echo 'cannot flush reclaimable cache: /proc/sys/vm/drop_caches is not writable' >&2
        exit 1
      fi
      printf '3\n' > /proc/sys/vm/drop_caches
      report_memory 'after reclaimable cache flush'
    else
      echo 'reclaimable cache flush skipped because ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE=false'
    fi
  "
}

stage_netdata_config_to_remote() {
  local remote_tmp_dir="/tmp/arbuzas-netdata.$$"
  local remote_tarball="${remote_tmp_dir}.tar"
  local local_tarball=""
  local attempt=0

  [[ -d "${NETDATA_CONFIG_ROOT}" ]] || {
    echo "missing Netdata config root: ${NETDATA_CONFIG_ROOT}" >&2
    return 1
  }
  [[ -f "${NETDATA_DASHBOARD_WEB_ROOT}/index.html" && -f "${NETDATA_DASHBOARD_WEB_ROOT}/build.json" ]] || {
    echo "missing built Kitty-gration dashboard under ${NETDATA_DASHBOARD_WEB_ROOT}" >&2
    return 1
  }
  [[ -f "${NETDATA_NATIVE_DASHBOARD_ROOT}/arbuzas_netdata_native_dashboard.py" && \
     -f "${NETDATA_NATIVE_DASHBOARD_ROOT}/arbuzas-netdata-native-dashboard.service" && \
     -f "${NETDATA_NATIVE_DASHBOARD_ROOT}/netdata-native-dashboard.conf" ]] || {
    echo "missing native Netdata dashboard mobile adaptation under ${NETDATA_NATIVE_DASHBOARD_ROOT}" >&2
    return 1
  }
  local_tarball="$(mktemp "${TMPDIR:-/tmp}/arbuzas-netdata.XXXXXX.tar")"
  trap "rm -f -- '${local_tarball}'; trap - RETURN" RETURN
  (
    cd "${NETDATA_CONFIG_ROOT}"
    COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata \
      -cf "${local_tarball}" \
      netdata.conf go.d web/kitty-gration native-dashboard
  )

  log "Staging Netdata config on ${ARBUZAS_HOST}:${remote_tmp_dir}"
  for attempt in 1 2 3; do
    if upload_remote_file "${local_tarball}" "${remote_tarball}" && \
       remote_inline_shell "
         trap 'rm -f -- \"${remote_tarball}\"' EXIT
         rm -rf '${remote_tmp_dir}'
         install -d '${remote_tmp_dir}'
         tar -C '${remote_tmp_dir}' -xf '${remote_tarball}'
       "; then
      printf '%s\n' "${remote_tmp_dir}"
      return 0
    fi
    if (( attempt < 3 )); then
      log "Netdata config staging attempt ${attempt} failed; retrying"
      sleep 2
    fi
  done

  echo "failed to stage Netdata config on ${ARBUZAS_HOST}" >&2
  return 1
}

prepare_remote_netdata_rollback() {
  local remote_backup_root="$1"

  remote_root_command "
    rm -rf '${remote_backup_root}'
    install -d -m 0700 '${remote_backup_root}'
    if [[ -d '${NETDATA_REMOTE_CONFIG_DIR}' ]]; then
      tar -C /etc -cpf '${remote_backup_root}/netdata-config.tar' netdata
    fi
    if [[ -d '${NETDATA_REMOTE_DASHBOARD_DIR}' ]]; then
      touch '${remote_backup_root}/dashboard-present'
      tar -C '${NETDATA_REMOTE_WEB_ROOT}' -cpf '${remote_backup_root}/dashboard.tar' kitty-gration
    fi
    native_entrypoints_ready=1
    for native_entrypoint in \
      '${NETDATA_REMOTE_WEB_ROOT}/index.html' \
      '${NETDATA_REMOTE_WEB_ROOT}/v3/agent.html' \
      '${NETDATA_REMOTE_WEB_ROOT}/v3/index.html' \
      '${NETDATA_REMOTE_WEB_ROOT}/v3/local-agent.html'; do
      if [[ ! -f \"\${native_entrypoint}\" || -L \"\${native_entrypoint}\" ]]; then
        native_entrypoints_ready=0
      fi
    done
    if [[ \"\${native_entrypoints_ready}\" == '1' ]]; then
      tar -C '${NETDATA_REMOTE_WEB_ROOT}' -cpf '${remote_backup_root}/native-dashboard-entrypoints.tar' \
        index.html v3/agent.html v3/index.html v3/local-agent.html
    fi
    if dpkg-query -W netdata-dashboard >/dev/null 2>&1; then
      dpkg-query -W netdata-dashboard | awk '{print \$2}' > '${remote_backup_root}/netdata-dashboard-package-version'
    fi
    if [[ -e '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' || -L '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' ]]; then
      touch '${remote_backup_root}/native-dashboard-patcher-present'
      cp -a -- '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' '${remote_backup_root}/native-dashboard-patcher'
    fi
    if [[ -e '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}' || -L '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}' ]]; then
      touch '${remote_backup_root}/native-dashboard-service-present'
      cp -a -- '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}' '${remote_backup_root}/native-dashboard-service'
    fi
    if [[ -e '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}' || -L '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}' ]]; then
      touch '${remote_backup_root}/native-dashboard-dropin-present'
      cp -a -- '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}' '${remote_backup_root}/native-dashboard-dropin'
    fi
    if systemctl cat netdata.service >/dev/null 2>&1; then
      touch '${remote_backup_root}/service-present'
      if systemctl is-active --quiet netdata.service; then
        touch '${remote_backup_root}/service-active'
      fi
      if systemctl is-enabled --quiet netdata.service; then
        touch '${remote_backup_root}/service-enabled'
      fi
    fi
    tailscale serve status --json > '${remote_backup_root}/tailscale-serve.json'
  "
}

restore_remote_netdata_rollback() {
  local remote_backup_root="$1"

  log "Recovery: restoring the previous Netdata config, dashboards, service state, and private route"
  remote_root_command "
    [[ -d '${remote_backup_root}' ]] || {
      echo 'missing Netdata rollback snapshot: ${remote_backup_root}' >&2
      exit 1
    }

    if [[ -f '${remote_backup_root}/netdata-config.tar' ]]; then
      rm -rf '${NETDATA_REMOTE_CONFIG_DIR}'
      tar -C /etc -xpf '${remote_backup_root}/netdata-config.tar'
    else
      rm -rf '${NETDATA_REMOTE_CONFIG_DIR}'
    fi

    rm -rf '${NETDATA_REMOTE_DASHBOARD_DIR}'
    if [[ -f '${remote_backup_root}/dashboard-present' ]]; then
      [[ -f '${remote_backup_root}/dashboard.tar' ]] || {
        echo 'missing Netdata dashboard rollback archive' >&2
        exit 1
      }
      install -d -m 0755 '${NETDATA_REMOTE_WEB_ROOT}'
      tar -C '${NETDATA_REMOTE_WEB_ROOT}' -xpf '${remote_backup_root}/dashboard.tar'
    fi

    if [[ -x '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' ]]; then
      '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' remove \
        --web-root '${NETDATA_REMOTE_WEB_ROOT}' \
        --best-effort || true
    fi

    snapshot_dashboard_version=''
    current_dashboard_version=''
    if [[ -f '${remote_backup_root}/netdata-dashboard-package-version' ]]; then
      snapshot_dashboard_version=\$(cat '${remote_backup_root}/netdata-dashboard-package-version')
    fi
    if dpkg-query -W netdata-dashboard >/dev/null 2>&1; then
      current_dashboard_version=\$(dpkg-query -W netdata-dashboard | awk '{print \$2}')
    fi
    if [[ -f '${remote_backup_root}/native-dashboard-entrypoints.tar' && \
          \"\${snapshot_dashboard_version}\" == \"\${current_dashboard_version}\" ]]; then
      tar -C '${NETDATA_REMOTE_WEB_ROOT}' -xpf '${remote_backup_root}/native-dashboard-entrypoints.tar'
    fi

    rm -f '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}'
    if [[ -f '${remote_backup_root}/native-dashboard-patcher-present' ]]; then
      install -d -m 0755 \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}')\"
      cp -a -- '${remote_backup_root}/native-dashboard-patcher' '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}'
    fi
    rm -f '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}'
    if [[ -f '${remote_backup_root}/native-dashboard-service-present' ]]; then
      install -d -m 0755 \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}')\"
      cp -a -- '${remote_backup_root}/native-dashboard-service' '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}'
    fi
    rm -f '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}'
    if [[ -f '${remote_backup_root}/native-dashboard-dropin-present' ]]; then
      install -d -m 0755 \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}')\"
      cp -a -- '${remote_backup_root}/native-dashboard-dropin' '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}'
    else
      rmdir \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}')\" >/dev/null 2>&1 || true
    fi

    if [[ -f '${remote_backup_root}/native-dashboard-patcher-present' && \
          -x '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' && \
          \"\${snapshot_dashboard_version}\" != \"\${current_dashboard_version}\" ]]; then
      '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' apply \
        --web-root '${NETDATA_REMOTE_WEB_ROOT}' \
        --best-effort || true
    fi
    systemctl daemon-reload

    if [[ -f '${remote_backup_root}/service-present' ]]; then
      if [[ -f '${remote_backup_root}/service-enabled' ]]; then
        systemctl enable netdata.service >/dev/null
      else
        systemctl disable netdata.service >/dev/null 2>&1 || true
      fi
      if [[ -f '${remote_backup_root}/service-active' ]]; then
        systemctl restart netdata.service
      else
        systemctl stop netdata.service >/dev/null 2>&1 || true
      fi
    else
      systemctl disable --now netdata.service >/dev/null 2>&1 || true
    fi

    previous_route_state=\$(
      NETDATA_SERVE_JSON=\"\$(cat '${remote_backup_root}/tailscale-serve.json')\" \
      NETDATA_PORT='${ARBUZAS_NETDATA_PORT}' \
      python3 - <<'PY'
import json
import os
import subprocess

payload = json.loads(os.environ['NETDATA_SERVE_JSON'])
port = os.environ['NETDATA_PORT']
target = '127.0.0.1:' + port
proxy_target = 'http://' + target
dns_name = json.loads(
    subprocess.check_output(['tailscale', 'status', '--json'], text=True)
).get('Self', {}).get('DNSName', '').rstrip('.')
tcp = payload.get('TCP', {}).get(port)
web = {
    key: value for key, value in payload.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
allow_funnel = payload.get('AllowFunnel') or {}
funnel = {
    str(key): value for key, value in allow_funnel.items()
    if str(key).rsplit(':', 1)[-1] == port
} if isinstance(allow_funnel, dict) else {'unexpected': allow_funnel}
if funnel:
    state = 'conflict'
elif tcp is None and not web:
    state = 'absent'
elif tcp == {'TCPForward': target} and not web:
    state = 'legacy_tcp'
elif tcp == {'HTTPS': True} and web == {
    dns_name + ':' + port: {'Handlers': {'/': {'Proxy': proxy_target}}}
}:
    state = 'exact'
else:
    state = 'conflict'
print(state)
PY
    )

    case \"\${previous_route_state}\" in
      absent)
        tailscale serve --yes --https ${ARBUZAS_NETDATA_PORT} off >/dev/null 2>&1 || true
        tailscale serve --yes --tcp ${ARBUZAS_NETDATA_PORT} off >/dev/null 2>&1 || true
        ;;
      legacy_tcp)
        tailscale serve --yes --https ${ARBUZAS_NETDATA_PORT} off >/dev/null 2>&1 || true
        tailscale serve --bg --yes --tcp ${ARBUZAS_NETDATA_PORT} tcp://127.0.0.1:${ARBUZAS_NETDATA_PORT}
        ;;
      exact)
        tailscale serve --yes --tcp ${ARBUZAS_NETDATA_PORT} off >/dev/null 2>&1 || true
        tailscale serve --bg --yes --https ${ARBUZAS_NETDATA_PORT} http://127.0.0.1:${ARBUZAS_NETDATA_PORT}
        ;;
      conflict)
        echo 'Netdata rollback left the pre-existing conflicting Tailscale route untouched' >&2
        ;;
      *)
        echo \"unexpected Netdata rollback route state: \${previous_route_state}\" >&2
        exit 1
        ;;
    esac

    rm -rf '${remote_backup_root}'
  "
}

cleanup_remote_netdata_rollback() {
  local remote_backup_root="$1"

  remote_root_command "rm -rf '${remote_backup_root}'"
}

install_remote_netdata() {
  local remote_stage_root="$1"

  log "Maintenance: installing Netdata and host collectors on ${ARBUZAS_HOST}"
  remote_root_command "
    command -v tailscale >/dev/null 2>&1 || {
      echo 'tailscale is required for private Arbuzas Netdata access' >&2
      exit 1
    }

    netdata_serve_json=\$(tailscale serve status --json)
    netdata_serve_probe=\$(
      NETDATA_SERVE_JSON=\"\${netdata_serve_json}\" \
      NETDATA_PORT='${ARBUZAS_NETDATA_PORT}' \
      python3 - <<'PY'
import base64
import json
import os
import subprocess

payload = json.loads(os.environ['NETDATA_SERVE_JSON'])
port = os.environ['NETDATA_PORT']
target = '127.0.0.1:' + port
proxy_target = 'http://' + target
dns_name = json.loads(
    subprocess.check_output(['tailscale', 'status', '--json'], text=True)
).get('Self', {}).get('DNSName', '').rstrip('.')
if not dns_name:
    raise SystemExit('missing Tailscale DNS name for Netdata HTTPS')
tcp = payload.get('TCP', {}).get(port)
web = {
    key: value for key, value in payload.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
allow_funnel = payload.get('AllowFunnel') or {}
if not isinstance(allow_funnel, dict):
    raise SystemExit('unexpected Tailscale AllowFunnel shape')
funnel = {
    str(key): value for key, value in allow_funnel.items()
    if str(key).rsplit(':', 1)[-1] == port
}
if funnel:
    state = 'conflict'
elif tcp is None and not web:
    state = 'absent'
elif (
    tcp == {'HTTPS': True}
    and web == {
        dns_name + ':' + port: {
            'Handlers': {'/': {'Proxy': proxy_target}}
        }
    }
):
    state = 'exact'
elif tcp == {'TCPForward': target} and not web:
    state = 'legacy_tcp'
else:
    state = 'conflict'
encoded = base64.b64encode(
    json.dumps(payload, sort_keys=True, separators=(',', ':')).encode()
).decode()
print(state + '|' + encoded)
PY
    )
    netdata_serve_state=\${netdata_serve_probe%%|*}
    netdata_serve_before=\${netdata_serve_probe#*|}
    case \"\${netdata_serve_state}\" in
      absent|exact|legacy_tcp)
        ;;
      *)
        echo 'Tailscale Serve port ${ARBUZAS_NETDATA_PORT} is already configured for another target; refusing to overwrite it' >&2
        exit 1
        ;;
    esac

    tmpdir=\$(mktemp -d)
    trap 'rm -rf \"\${tmpdir}\" \"\${dashboard_next:-}\" \"\${dashboard_previous:-}\" \"${remote_stage_root}\"' EXIT

    if command -v netdata >/dev/null 2>&1 && systemctl cat netdata.service >/dev/null 2>&1; then
      echo 'Existing Netdata Agent detected; keeping its installed package version'
    else
      command -v apt-get >/dev/null 2>&1 || {
        echo 'apt-get is required for the initial Arbuzas Netdata install' >&2
        exit 1
      }
      export DEBIAN_FRONTEND=noninteractive
      apt-get update
      apt-get install -y ca-certificates curl lm-sensors smartmontools
      curl -fsSL '${NETDATA_KICKSTART_URL}' -o \"\${tmpdir}/kickstart.sh\"
      printf 'Netdata kickstart sha256: '
      sha256sum \"\${tmpdir}/kickstart.sh\" | awk '{print \$1}'
      DISABLE_TELEMETRY=1 sh \"\${tmpdir}/kickstart.sh\" \
        --stable-channel \
        --native-only \
        --non-interactive \
        --no-updates \
        --disable-telemetry
    fi

    [[ -f '${remote_stage_root}/web/kitty-gration/index.html' ]] || {
      echo 'staged Kitty-gration dashboard is missing index.html' >&2
      exit 1
    }
    [[ -f '${remote_stage_root}/web/kitty-gration/build.json' ]] || {
      echo 'staged Kitty-gration dashboard is missing build.json' >&2
      exit 1
    }
    [[ -f '${remote_stage_root}/native-dashboard/arbuzas_netdata_native_dashboard.py' ]] || {
      echo 'staged native Netdata dashboard patcher is missing' >&2
      exit 1
    }
    [[ -f '${remote_stage_root}/native-dashboard/arbuzas-netdata-native-dashboard.service' ]] || {
      echo 'staged native Netdata dashboard systemd service is missing' >&2
      exit 1
    }
    [[ -f '${remote_stage_root}/native-dashboard/netdata-native-dashboard.conf' ]] || {
      echo 'staged native Netdata dashboard systemd drop-in is missing' >&2
      exit 1
    }

    install -d -m 0755 \
      '${NETDATA_REMOTE_CONFIG_DIR}' \
      '${NETDATA_REMOTE_CONFIG_DIR}/go.d' \
      '${NETDATA_REMOTE_CONFIG_DIR}/go.d/sd'
    install -o root -g netdata -m 0644 \
      '${remote_stage_root}/netdata.conf' \
      '${NETDATA_REMOTE_CONFIG_FILE}'
    install -o root -g netdata -m 0644 \
      '${remote_stage_root}/go.d/docker.conf' \
      '${NETDATA_REMOTE_DOCKER_CONFIG_FILE}'
    install -o root -g netdata -m 0644 \
      '${remote_stage_root}/go.d/sd/docker.conf' \
      '${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}'
    install -o root -g netdata -m 0644 \
      '${remote_stage_root}/go.d/systemdunits.conf' \
      '${NETDATA_REMOTE_SYSTEMD_CONFIG_FILE}'

    install -d -o root -g root -m 0755 \
      \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}')\" \
      \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}')\" \
      \"\$(dirname '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}')\"
    install -o root -g root -m 0755 \
      '${remote_stage_root}/native-dashboard/arbuzas_netdata_native_dashboard.py' \
      '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}'
    install -o root -g root -m 0644 \
      '${remote_stage_root}/native-dashboard/arbuzas-netdata-native-dashboard.service' \
      '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}'
    install -o root -g root -m 0644 \
      '${remote_stage_root}/native-dashboard/netdata-native-dashboard.conf' \
      '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}'

    dashboard_next='${NETDATA_REMOTE_DASHBOARD_DIR}.next.'\$\$
    dashboard_previous='${NETDATA_REMOTE_DASHBOARD_DIR}.previous.'\$\$
    rm -rf "\${dashboard_next}" "\${dashboard_previous}"
    install -d -o root -g netdata -m 0755 "\${dashboard_next}"
    tar -C '${remote_stage_root}/web/kitty-gration' -cf - . | \
      tar --no-same-owner -C "\${dashboard_next}" -xf -
    find "\${dashboard_next}" -type f -exec touch {} +
    if [[ -d '${NETDATA_REMOTE_DASHBOARD_DIR}' && -f '${NETDATA_REMOTE_DASHBOARD_DIR}/build.json' ]]; then
      previous_active_assets=\$(
        python3 - '${NETDATA_REMOTE_DASHBOARD_DIR}/build.json' <<'PY'
import json
import re
import sys

assets = json.load(open(sys.argv[1], encoding='utf-8')).get('assets', [])
for asset in assets:
    if re.fullmatch(r'(?:app|native-mobile)\.[A-Z0-9]{8}\.(?:js|css)', str(asset)):
        print(asset)
PY
      )
      for previous_asset in \
        '${NETDATA_REMOTE_DASHBOARD_DIR}'/app.*.js \
        '${NETDATA_REMOTE_DASHBOARD_DIR}'/app.*.css \
        '${NETDATA_REMOTE_DASHBOARD_DIR}'/native-mobile.*.js \
        '${NETDATA_REMOTE_DASHBOARD_DIR}'/native-mobile.*.css; do
        [[ -f "\${previous_asset}" && ! -L "\${previous_asset}" ]] || continue
        previous_name=\${previous_asset##*/}
        [[ "\${previous_name}" =~ ^(app|native-mobile)\.[A-Z0-9]{8}\.(js|css)$ ]] || continue
        retain_previous=0
        previous_was_active=0
        if printf '%s\n' "\${previous_active_assets}" | grep -Fxq "\${previous_name}"; then
          retain_previous=1
          previous_was_active=1
        elif [[ -n \$(find "\${previous_asset}" -mmin -1500 -print -quit) ]]; then
          retain_previous=1
        fi
        if [[ "\${retain_previous}" == '1' && ! -e "\${dashboard_next}/\${previous_name}" ]]; then
          if [[ "\${previous_was_active}" == '1' ]]; then
            cp "\${previous_asset}" "\${dashboard_next}/\${previous_name}"
          else
            cp -p "\${previous_asset}" "\${dashboard_next}/\${previous_name}"
          fi
        fi
      done
    fi
    chown -R root:netdata "\${dashboard_next}"
    find "\${dashboard_next}" -type d -exec chmod 0755 {} +
    find "\${dashboard_next}" -type f -exec chmod 0644 {} +
    if [[ -d '${NETDATA_REMOTE_DASHBOARD_DIR}' ]]; then
      mv '${NETDATA_REMOTE_DASHBOARD_DIR}' "\${dashboard_previous}"
    fi
    mv "\${dashboard_next}" '${NETDATA_REMOTE_DASHBOARD_DIR}'
    rm -rf "\${dashboard_previous}"

    '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' apply \
      --web-root '${NETDATA_REMOTE_WEB_ROOT}' \
      --manifest '${NETDATA_REMOTE_DASHBOARD_DIR}/build.json'
    systemctl daemon-reload

    rm -f /var/lib/netdata/cloud.d/claim.conf '${NETDATA_REMOTE_CONFIG_DIR}/claim.conf'

    systemctl enable netdata
    systemctl restart netdata

    deadline=\$((SECONDS + 90))
    while true; do
      if systemctl is-active --quiet netdata && \
         curl -fsS 'http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/api/v1/info' >/dev/null 2>/dev/null; then
        break
      fi
      if (( SECONDS >= deadline )); then
        echo 'Netdata did not become ready on localhost after install' >&2
        exit 1
      fi
      sleep 5
    done

    netdata_https_added=0
    netdata_legacy_tcp_removed=0
    if [[ \"\${netdata_serve_state}\" == 'legacy_tcp' ]]; then
      tailscale serve --yes --tcp ${ARBUZAS_NETDATA_PORT} off
      netdata_legacy_tcp_removed=1
    fi
    if [[ \"\${netdata_serve_state}\" != 'exact' ]]; then
      if ! tailscale serve --bg --yes --https ${ARBUZAS_NETDATA_PORT} http://127.0.0.1:${ARBUZAS_NETDATA_PORT}; then
        if [[ \"\${netdata_legacy_tcp_removed}\" == '1' ]]; then
          tailscale serve --bg --yes --tcp ${ARBUZAS_NETDATA_PORT} tcp://127.0.0.1:${ARBUZAS_NETDATA_PORT} || true
        fi
        echo 'failed to publish Netdata through private Tailscale HTTPS' >&2
        exit 1
      fi
      netdata_https_added=1
    fi

    netdata_serve_after=\$(tailscale serve status --json | base64 | tr -d '\\n')
    if ! NETDATA_SERVE_BEFORE=\"\${netdata_serve_before}\" \
         NETDATA_SERVE_AFTER=\"\${netdata_serve_after}\" \
         NETDATA_PORT='${ARBUZAS_NETDATA_PORT}' \
         python3 - <<'PY'
import base64
import copy
import json
import os
import subprocess

port = os.environ['NETDATA_PORT']
target = '127.0.0.1:' + port
proxy_target = 'http://' + target
before = json.loads(base64.b64decode(os.environ['NETDATA_SERVE_BEFORE']))
after = json.loads(base64.b64decode(os.environ['NETDATA_SERVE_AFTER']))
dns_name = json.loads(
    subprocess.check_output(['tailscale', 'status', '--json'], text=True)
).get('Self', {}).get('DNSName', '').rstrip('.')
if not dns_name:
    raise SystemExit('missing Tailscale DNS name for Netdata HTTPS')
tcp = after.get('TCP', {}).get(port)
web = {
    key: value for key, value in after.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
allow_funnel = after.get('AllowFunnel') or {}
if not isinstance(allow_funnel, dict):
    raise SystemExit('unexpected Tailscale AllowFunnel shape')
funnel = {
    str(key): value for key, value in allow_funnel.items()
    if str(key).rsplit(':', 1)[-1] == port
}
expected_web = {
    dns_name + ':' + port: {
        'Handlers': {'/': {'Proxy': proxy_target}}
    }
}
if tcp != {'HTTPS': True} or web != expected_web or funnel:
    raise SystemExit(
        'unexpected Netdata Serve route: tcp={!r}, web={!r}, funnel={!r}'.format(
            tcp, web, funnel
        )
    )

def without_netdata_route(payload):
    result = copy.deepcopy(payload)
    result.get('TCP', {}).pop(port, None)
    for key in list(result.get('Web', {})):
        if key.rsplit(':', 1)[-1] == port:
            result['Web'].pop(key, None)
    return result

if without_netdata_route(before) != without_netdata_route(after):
    raise SystemExit('unrelated Tailscale Serve routes changed while adding Netdata')
PY
    then
      if [[ \"\${netdata_https_added}\" == '1' ]]; then
        tailscale serve --yes --https ${ARBUZAS_NETDATA_PORT} off || true
      fi
      if [[ \"\${netdata_legacy_tcp_removed}\" == '1' ]]; then
        tailscale serve --bg --yes --tcp ${ARBUZAS_NETDATA_PORT} tcp://127.0.0.1:${ARBUZAS_NETDATA_PORT} || true
      fi
      echo 'failed to add the Netdata Tailscale route without changing unrelated routes' >&2
      exit 1
    fi
  " 1
}

stage_memory_report_config_to_remote() {
  local remote_tmp_dir="/tmp/arbuzas-memory-report.$$"
  local memory_report_tree_base64=""
  local memory_report_script_base64=""
  local attempt=0

  [[ -d "${MEMORY_REPORT_CONFIG_ROOT}" ]] || {
    echo "missing memory report config root: ${MEMORY_REPORT_CONFIG_ROOT}" >&2
    return 1
  }
  [[ -f "${MEMORY_REPORT_SCRIPT}" ]] || {
    echo "missing memory reporter: ${MEMORY_REPORT_SCRIPT}" >&2
    return 1
  }

  memory_report_tree_base64="$(COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C "${MEMORY_REPORT_CONFIG_ROOT}" -cf - . | base64 | tr -d '\n')"
  memory_report_script_base64="$(base64 < "${MEMORY_REPORT_SCRIPT}" | tr -d '\n')"

  log "Staging memory report service config on ${ARBUZAS_HOST}:${remote_tmp_dir}"
  for attempt in 1 2 3; do
    if remote_inline_shell "
      rm -rf '${remote_tmp_dir}'
      install -d '${remote_tmp_dir}'
      printf '%s' '${memory_report_tree_base64}' | base64 -d | tar -xf - -C '${remote_tmp_dir}'
      install -d '${remote_tmp_dir}/usr/local/libexec'
      printf '%s' '${memory_report_script_base64}' | base64 -d > '${remote_tmp_dir}/usr/local/libexec/arbuzas-memory-report.py'
    "; then
      printf '%s\n' "${remote_tmp_dir}"
      return 0
    fi
    if (( attempt < 3 )); then
      log "Memory report service staging attempt ${attempt} failed; retrying"
      sleep 2
    fi
  done

  echo "failed to stage memory report service config on ${ARBUZAS_HOST}" >&2
  return 1
}

install_remote_memory_report() {
  local remote_stage_root="$1"

  log "Maintenance: installing the corrected memory report service on ${ARBUZAS_HOST}"
  remote_root_command "
    command -v python3 >/dev/null 2>&1 || {
      echo 'python3 is required for the Arbuzas memory report service' >&2
      exit 1
    }
    [[ -r /proc/meminfo ]] || {
      echo '/proc/meminfo is required for the Arbuzas memory report service' >&2
      exit 1
    }

    trap 'rm -rf \"${remote_stage_root}\"' EXIT

    tar -C '${remote_stage_root}' -cf - . | tar -C / -xf -
    chmod 0644 '${MEMORY_REPORT_REMOTE_DEFAULT_FILE}' '${MEMORY_REPORT_REMOTE_SERVICE_FILE}' '${MEMORY_REPORT_REMOTE_TIMER_FILE}'
    chmod 0755 '${MEMORY_REPORT_REMOTE_SCRIPT_FILE}'
    install -d -m 0755 '${MEMORY_REPORT_REMOTE_OUTPUT_DIR}'

    systemctl daemon-reload
    systemctl enable arbuzas-memory-report.timer >/dev/null
    systemctl restart arbuzas-memory-report.timer
    systemctl start arbuzas-memory-report.service

    deadline=\$((SECONDS + 30))
    while true; do
      if systemctl is-active --quiet arbuzas-memory-report.timer && \
         [[ -s '${MEMORY_REPORT_REMOTE_JSON_FILE}' ]] && \
         [[ -s '${MEMORY_REPORT_REMOTE_TEXT_FILE}' ]] && \
         [[ -s '${MEMORY_REPORT_REMOTE_PROM_FILE}' ]]; then
        break
      fi
      if (( SECONDS >= deadline )); then
        echo 'Arbuzas memory report service did not publish a snapshot' >&2
        exit 1
      fi
      sleep 2
    done
  "
}

stage_thinkpad_fan_config_to_remote() {
  local remote_tmp_dir="/tmp/arbuzas-thinkpad-fan.$$"
  local thinkpad_fan_tree_base64=""
  local attempt=0

  [[ -d "${THINKPAD_FAN_CONFIG_ROOT}" ]] || {
    echo "missing ThinkPad fan config root: ${THINKPAD_FAN_CONFIG_ROOT}" >&2
    return 1
  }
  thinkpad_fan_tree_base64="$(COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C "${THINKPAD_FAN_CONFIG_ROOT}" -cf - . | base64 | tr -d '\n')"

  log "Staging ThinkPad fan config on ${ARBUZAS_HOST}:${remote_tmp_dir}"
  for attempt in 1 2 3; do
    if remote_inline_shell "
      rm -rf '${remote_tmp_dir}'
      install -d '${remote_tmp_dir}'
      printf '%s' '${thinkpad_fan_tree_base64}' | base64 -d | tar -xf - -C '${remote_tmp_dir}'
    "; then
      printf '%s\n' "${remote_tmp_dir}"
      return 0
    fi
    if (( attempt < 3 )); then
      log "ThinkPad fan config staging attempt ${attempt} failed; retrying"
      sleep 2
    fi
  done

  echo "failed to stage ThinkPad fan config on ${ARBUZAS_HOST}" >&2
  return 1
}

install_remote_thinkpad_fan() {
  local remote_stage_root="$1"

  log "Maintenance: installing the ThinkPad fan controller on ${ARBUZAS_HOST}"
  remote_root_command "
    command -v python3 >/dev/null 2>&1 || {
      echo 'python3 is required for the Arbuzas ThinkPad fan controller' >&2
      exit 1
    }

    [[ -f '${THINKPAD_FAN_REMOTE_PROC_FILE}' ]] || {
      echo 'missing ThinkPad fan control path: ${THINKPAD_FAN_REMOTE_PROC_FILE}' >&2
      exit 1
    }

    trap 'rm -rf \"${remote_stage_root}\"' EXIT

    tar -C '${remote_stage_root}' -cf - . | tar -C / -xf -
    chmod 0644 '${THINKPAD_FAN_REMOTE_DEFAULT_FILE}' '${THINKPAD_FAN_REMOTE_MODPROBE_FILE}' '${THINKPAD_FAN_REMOTE_SERVICE_FILE}'
    chmod 0755 '${THINKPAD_FAN_REMOTE_SCRIPT_FILE}'

    systemctl stop arbuzas-thinkpad-fan.service >/dev/null 2>&1 || true
    printf 'watchdog 0\n' > '${THINKPAD_FAN_REMOTE_PROC_FILE}' || true
    printf 'level auto\n' > '${THINKPAD_FAN_REMOTE_PROC_FILE}' || true

    fan_control_status=\$(cat '${THINKPAD_FAN_REMOTE_PARAM_FILE}' 2>/dev/null || printf 'N')
    if [[ \"\${fan_control_status}\" != 'Y' ]]; then
      modprobe -r thinkpad_acpi
      modprobe thinkpad_acpi fan_control=1
    fi

    systemctl daemon-reload
    systemctl enable arbuzas-thinkpad-fan.service >/dev/null
    systemctl restart arbuzas-thinkpad-fan.service

    deadline=\$((SECONDS + 30))
    while true; do
      fan_control_status=\$(cat '${THINKPAD_FAN_REMOTE_PARAM_FILE}' 2>/dev/null || printf 'N')
      if systemctl is-active --quiet arbuzas-thinkpad-fan.service && [[ \"\${fan_control_status}\" == 'Y' ]]; then
        break
      fi
      if (( SECONDS >= deadline )); then
        echo 'Arbuzas ThinkPad fan controller did not become ready' >&2
        exit 1
      fi
      sleep 2
    done
  "
}

run_automatic_remote_docker_gc() {
  log "Cleanup: pruning unused Docker images, old releases, old build cache, and safe host caches"
  if remote_run_docker_gc; then
    if remote_run_host_cache_cleanup; then
      return 0
    fi
    log "Cleanup warning: host cache cleanup failed on ${ARBUZAS_HOST}, but the release remains successful"
    return 0
  fi
  log "Cleanup warning: Docker/release cleanup failed on ${ARBUZAS_HOST}, but the release remains successful"
}

upload_remote_file() {
  local local_path="$1"
  local remote_path="$2"
  local remote_path_q=""

  remote_path_q="$(shell_quote "${remote_path}")"

  run_ssh \
    -o ConnectTimeout=15 \
    -o ServerAliveInterval=15 \
    -o ServerAliveCountMax=3 \
    "$(remote_target)" \
    "set -euo pipefail;
     umask 077;
     remote_path=${remote_path_q};
     remote_dir=\$(dirname -- \"\${remote_path}\");
     mkdir -p \"\${remote_dir}\";
     [[ -d \"\${remote_dir}\" && ! -L \"\${remote_dir}\" ]] || {
       echo \"refusing unsafe remote upload directory: \${remote_dir}\" >&2;
       exit 1;
     };
     if [[ -e \"\${remote_path}\" || -L \"\${remote_path}\" ]]; then
       [[ -f \"\${remote_path}\" && ! -L \"\${remote_path}\" ]] || {
         echo \"refusing unsafe remote upload target: \${remote_path}\" >&2;
         exit 1;
       };
     fi;
     staging_dir=\$(mktemp -d \"\${remote_dir}/.arbuzas-upload.XXXXXXXX\");
     chmod 0700 \"\${staging_dir}\";
     staged_path=\"\${staging_dir}/payload\";
     cleanup_upload() {
       rm -f -- \"\${staged_path}\";
       rmdir -- \"\${staging_dir}\" 2>/dev/null || true;
     };
     trap cleanup_upload EXIT;
     trap 'exit 1' HUP INT TERM;
     : > \"\${staged_path}\";
     chmod 0600 \"\${staged_path}\";
     cat > \"\${staged_path}\";
     python3 - \"\${staging_dir}\" \"\${staged_path}\" \"\${remote_path}\" <<'PY'
import os
import pathlib
import stat
import sys

directory = pathlib.Path(sys.argv[1])
source = pathlib.Path(sys.argv[2])
destination = pathlib.Path(sys.argv[3])
directory_stat = directory.lstat()
source_stat = source.lstat()
if not stat.S_ISDIR(directory_stat.st_mode) or stat.S_IMODE(directory_stat.st_mode) != 0o700:
    raise SystemExit('refusing unsafe remote upload staging directory')
if not stat.S_ISREG(source_stat.st_mode) or stat.S_IMODE(source_stat.st_mode) != 0o600:
    raise SystemExit('refusing unsafe remote upload staging file')
try:
    destination_stat = destination.lstat()
except FileNotFoundError:
    pass
else:
    if not stat.S_ISREG(destination_stat.st_mode):
        raise SystemExit('refusing unsafe remote upload target')
os.replace(source, destination)
os.chmod(destination, 0o600)
destination_stat = destination.lstat()
if not stat.S_ISREG(destination_stat.st_mode) or stat.S_IMODE(destination_stat.st_mode) != 0o600:
    raise SystemExit('remote upload target is not a private regular file')
PY" \
    < "${local_path}"
}

usage() {
  cat <<'EOF'
Usage: deploy.sh ACTION [options]

Actions:
  deploy            Prepare a release bundle, copy it to the live host, render tunnel configs, and run docker compose up -d --build
  validate          Validate the active or requested release on the live host
  rollback          Point /etc/arbuzas/current at a previous release and redeploy it
  cleanup-docker    Run the Arbuzas Docker image, release, build-cache, and host-cache cleanup policy on the live host
  memory-report     Report corrected host memory pressure and provider-like cached-inclusive memory from /proc/meminfo
  install-memory-report   Install the corrected host memory report service and timer on the live host
  validate-memory-report  Validate the corrected host memory report service, timer, and latest snapshot
  install-netdata   Install Netdata plus hardware monitoring packages on the live host and publish it privately over Tailscale
  validate-netdata  Validate the live Netdata host install, private Tailscale access, and expected host charts
  install-thinkpad-fan   Install the Arbuzas ThinkPad fan controller on the live host
  validate-thinkpad-fan  Validate the live ThinkPad fan controller and current control mode
  mirror-pull       Pull deployment variables and secrets from the host into the local plaintext mirror
  mirror-audit      Compare the local host mirror with the host and report drift before deploy
  mirror-push       Push local host mirror changes to the host when the host has not drifted
  deploy-config     Push local mirror changes and restart/reload only affected services; no build or release upload

Options:
  --release-id VALUE
  --services NAME[,NAME...]
  --validation-profile fast|standard|full
  --ssh-host HOST
  --ssh-user USER
  --ssh-port PORT
  --ssh-known-hosts-file PATH
  --env-file PATH

Services:
  train_bot, train_tunnel, satiksme_bot, satiksme_tunnel, subscription_bot,
  subscription_tunnel, ticket_phone_bridge,
  ticket_remote_spacetime_sidecar, ticket_remote, ticket_remote_tunnel,
  qbittorrent, qbittorrent_housekeeper,
  jellyfin,
  tiny_vless (separate Compose project; explicit selection required to recreate)
EOF
}

array_contains() {
  local needle="$1"
  shift || true
  local item
  for item in "$@"; do
    if [[ "${item}" == "${needle}" ]]; then
      return 0
    fi
  done
  return 1
}

append_unique() {
  local array_name="$1"
  local value="$2"
  local current_len=0
  local index
  local item
  eval "current_len=\${#${array_name}[@]}"
  for (( index = 0; index < current_len; index++ )); do
    eval "item=\${${array_name}[${index}]}"
    if [[ "${item}" == "${value}" ]]; then
      return 0
    fi
  done
  eval "${array_name}[${current_len}]=\$value"
}

trim_whitespace() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s\n' "${value}"
}

validate_validation_profile() {
  case "${VALIDATION_PROFILE}" in
    fast|standard|full)
      ;;
    *)
      echo "Unknown validation profile: ${VALIDATION_PROFILE}; expected fast, standard, or full" >&2
      exit 2
      ;;
  esac
}

validate_qbittorrent_fixed_parameters() {
  local actual=""
  local expected=""
  local name=""
  for name in \
    ARBUZAS_QBITTORRENT_WEBUI_PORT \
    ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT \
    ARBUZAS_QBITTORRENT_PEER_PORT \
    ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT \
    ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME; do
    case "${name}" in
      ARBUZAS_QBITTORRENT_WEBUI_PORT) expected=18080 ;;
      ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT) expected=24680 ;;
      ARBUZAS_QBITTORRENT_PEER_PORT) expected=45123 ;;
      ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT) expected=24680 ;;
      ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME) expected=arbuzas-vps.tail9345a.ts.net ;;
    esac
    eval "actual=\${${name}}"
    if [[ "${actual}" != "${expected}" ]]; then
      echo "${name} is fixed at ${expected} for the managed qBittorrent config (found: ${actual})" >&2
      exit 2
    fi
  done
}

validate_jellyfin_fixed_parameters() {
  local actual=""
  local expected=""
  local name=""
  for name in \
    ARBUZAS_JELLYFIN_HOST_PORT \
    ARBUZAS_JELLYFIN_INTERNAL_PORT \
    ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT \
    ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME; do
    case "${name}" in
      ARBUZAS_JELLYFIN_HOST_PORT) expected=18096 ;;
      ARBUZAS_JELLYFIN_INTERNAL_PORT) expected=8096 ;;
      ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT) expected=29096 ;;
      ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME) expected=arbuzas-vps.tail9345a.ts.net ;;
    esac
    eval "actual=\${${name}}"
    if [[ "${actual}" != "${expected}" ]]; then
      echo "${name} is fixed at ${expected} for the managed Jellyfin service (found: ${actual})" >&2
      exit 2
    fi
  done
}

is_known_service() {
  local service_name="$1"
  array_contains "${service_name}" "${ALL_SERVICES[@]}"
}

mark_validation_group() {
  local group_name="$1"
  case "${group_name}" in
    train)
      VALIDATE_TRAIN=1
      append_unique DIAGNOSTIC_SERVICES train_bot
      append_unique DIAGNOSTIC_SERVICES train_tunnel
      ;;
    satiksme)
      VALIDATE_SATIKSME=1
      append_unique DIAGNOSTIC_SERVICES satiksme_bot
      append_unique DIAGNOSTIC_SERVICES satiksme_tunnel
      ;;
    subscription)
      VALIDATE_SUBSCRIPTION=1
      append_unique DIAGNOSTIC_SERVICES subscription_bot
      append_unique DIAGNOSTIC_SERVICES subscription_tunnel
      ;;
    ticket_phone_bridge)
      VALIDATE_TICKET_PHONE_BRIDGE=1
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
      ;;
    ticket_remote)
      VALIDATE_TICKET_REMOTE=1
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
      append_unique DIAGNOSTIC_SERVICES ticket_remote_spacetime_sidecar
      append_unique DIAGNOSTIC_SERVICES ticket_remote
      append_unique DIAGNOSTIC_SERVICES ticket_remote_tunnel
      ;;
    qbittorrent)
      VALIDATE_QBITTORRENT=1
      append_unique DIAGNOSTIC_SERVICES qbittorrent
      append_unique DIAGNOSTIC_SERVICES qbittorrent_housekeeper
      ;;
    jellyfin)
      VALIDATE_JELLYFIN=1
      append_unique DIAGNOSTIC_SERVICES jellyfin
      ;;
    tiny_vless)
      VALIDATE_TINY_VLESS=1
      ;;
    *)
      echo "Unknown validation group: ${group_name}" >&2
      exit 2
      ;;
  esac
}

resolve_requested_services() {
  local service_name

  if (( ${#REQUESTED_SERVICES[@]} == 0 )); then
    return
  fi

  TARGETED_MODE=1

  for service_name in "${REQUESTED_SERVICES[@]}"; do
    case "${service_name}" in
      train_bot)
        append_unique COMPOSE_TARGET_SERVICES train_bot
        append_unique COMPOSE_TARGET_SERVICES train_tunnel
        mark_validation_group train
        ;;
      train_tunnel)
        append_unique COMPOSE_TARGET_SERVICES train_tunnel
        mark_validation_group train
        ;;
      satiksme_bot)
        append_unique COMPOSE_TARGET_SERVICES satiksme_bot
        append_unique COMPOSE_TARGET_SERVICES satiksme_tunnel
        mark_validation_group satiksme
        ;;
      satiksme_tunnel)
        append_unique COMPOSE_TARGET_SERVICES satiksme_bot
        append_unique COMPOSE_TARGET_SERVICES satiksme_tunnel
        mark_validation_group satiksme
        ;;
      subscription_bot)
        append_unique COMPOSE_TARGET_SERVICES subscription_bot
        append_unique COMPOSE_TARGET_SERVICES subscription_tunnel
        mark_validation_group subscription
        ;;
      subscription_tunnel)
        append_unique COMPOSE_TARGET_SERVICES subscription_tunnel
        mark_validation_group subscription
        ;;
      ticket_phone_bridge)
        append_unique COMPOSE_TARGET_SERVICES ticket_phone_bridge
        mark_validation_group ticket_phone_bridge
        ;;
      ticket_remote)
        if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
          append_unique COMPOSE_TARGET_SERVICES ticket_remote
        else
          append_unique COMPOSE_TARGET_SERVICES ticket_phone_bridge
          append_unique COMPOSE_TARGET_SERVICES ticket_remote_spacetime_sidecar
          append_unique COMPOSE_TARGET_SERVICES ticket_remote
          append_unique COMPOSE_TARGET_SERVICES ticket_remote_tunnel
        fi
        mark_validation_group ticket_remote
        ;;
      ticket_remote_spacetime_sidecar)
        append_unique COMPOSE_TARGET_SERVICES ticket_remote_spacetime_sidecar
        if [[ "${VALIDATION_PROFILE}" != "fast" ]]; then
          append_unique COMPOSE_TARGET_SERVICES ticket_remote
        fi
        mark_validation_group ticket_remote
        ;;
      ticket_remote_tunnel)
        append_unique COMPOSE_TARGET_SERVICES ticket_remote_tunnel
        mark_validation_group ticket_remote
        ;;
      qbittorrent|qbittorrent_housekeeper)
        append_unique COMPOSE_TARGET_SERVICES qbittorrent
        append_unique COMPOSE_TARGET_SERVICES qbittorrent_housekeeper
        mark_validation_group qbittorrent
        ;;
      jellyfin)
        append_unique COMPOSE_TARGET_SERVICES jellyfin
        mark_validation_group jellyfin
        ;;
      tiny_vless)
        # tiny-vless remains a distinct Compose project. It is deliberately
        # kept out of the main arbuzas Compose service selection.
        mark_validation_group tiny_vless
        ;;
      *)
        echo "Unknown service: ${service_name}" >&2
        exit 2
        ;;
    esac
  done
}

populate_current_diagnostic_services() {
  local array_name="$1"
  local service_name
  if (( TARGETED_MODE == 0 )); then
    eval "${array_name}=()"
    for service_name in "${ALL_SERVICES[@]}"; do
      [[ "${service_name}" == "tiny_vless" ]] && continue
      append_unique "${array_name}" "${service_name}"
    done
  else
    eval "${array_name}=()"
    for service_name in ${DIAGNOSTIC_SERVICES[@]+"${DIAGNOSTIC_SERVICES[@]}"}; do
      append_unique "${array_name}" "${service_name}"
    done
  fi
}

compose_target_service_args() {
  local service_args=""
  local service_name
  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    service_args+=" ${service_name}"
  done
  printf '%s' "${service_args}"
}

compose_target_service_args_without_tunnels() {
  local service_args=""
  local service_name
  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    case "${service_name}" in
      train_tunnel|satiksme_tunnel|subscription_tunnel|ticket_remote_tunnel)
        continue
        ;;
      *)
        service_args+=" ${service_name}"
        ;;
    esac
  done
  printf '%s' "${service_args}"
}

compose_target_tunnel_service_args() {
  local service_args=""
  local service_name
  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    case "${service_name}" in
      train_tunnel|satiksme_tunnel|subscription_tunnel|ticket_remote_tunnel)
        service_args+=" ${service_name}"
        ;;
    esac
  done
  printf '%s' "${service_args}"
}

compose_all_service_args() {
  local service_args=""
  local service_name
  local all_services=(
    train_bot
    satiksme_bot
    subscription_bot
    ticket_phone_bridge
    ticket_remote_spacetime_sidecar
    ticket_remote
    qbittorrent
    qbittorrent_housekeeper
    jellyfin
  )
  for service_name in "${all_services[@]}"; do
    service_args+=" ${service_name}"
  done
  printf '%s' "${service_args}"
}

compose_all_tunnel_service_args() {
  printf '%s' " train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel"
}

targeted_service_selected() {
  local wanted="$1"
  local service_name

  if (( TARGETED_MODE == 0 )); then
    return 0
  fi

  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    if [[ "${service_name}" == "${wanted}" ]]; then
      return 0
    fi
  done

  return 1
}

tiny_vless_deployment_selected() {
  local service_name

  # Unscoped application deploys validate the external VPN component but do
  # not recreate it. A restart requires an explicit tiny_vless selection.
  (( TARGETED_MODE == 1 )) || return 1
  for service_name in "${REQUESTED_SERVICES[@]}"; do
    [[ "${service_name}" == "tiny_vless" ]] && return 0
  done
  return 1
}

tiny_vless_manager_source_dir() {
  local release_id="${1:-${ARBUZAS_RELEASE_ID}}"
  printf '%s/infra/arbuzas/tiny-vless' "${REMOTE_RELEASES_ROOT}/${release_id}"
}

run_remote_tiny_vless_manager_at_source() {
  local manager_action="$1"
  local source_dir="$2"
  local validation_level="${3:-${VALIDATION_PROFILE}}"
  local manager_path=""
  local level_args=""
  local action_args=""

  manager_path="${source_dir}/manage.py"
  if [[ "${manager_action}" == "validate" ]]; then
    level_args=" --level $(shell_quote "${validation_level}")"
  fi
  if [[ "${manager_action}" == "deploy" ]]; then
    action_args=" --defer-rollback"
  fi
  remote_root_command "
    [[ -f $(shell_quote "${manager_path}") && ! -L $(shell_quote "${manager_path}") ]] || {
      echo 'missing tiny-vless component manager in release' >&2
      exit 1
    }
    python3 $(shell_quote "${manager_path}") $(shell_quote "${manager_action}") \\
      --source-dir $(shell_quote "${source_dir}")${level_args}${action_args}
  " 1
}

run_remote_tiny_vless_manager() {
  local manager_action="$1"
  local release_id="${2:-${ARBUZAS_RELEASE_ID}}"
  local validation_level="${3:-${VALIDATION_PROFILE}}"
  run_remote_tiny_vless_manager_at_source \
    "${manager_action}" \
    "$(tiny_vless_manager_source_dir "${release_id}")" \
    "${validation_level}"
}

adopt_remote_tiny_vless() {
  tiny_vless_deployment_selected || return 0
  run_remote_tiny_vless_manager adopt
}

deploy_remote_tiny_vless() {
  tiny_vless_deployment_selected || return 0
  run_remote_tiny_vless_manager deploy
}

commit_remote_tiny_vless() {
  tiny_vless_deployment_selected || return 0
  run_remote_tiny_vless_manager commit
}

rollback_remote_tiny_vless() {
  tiny_vless_deployment_selected || return 0
  run_remote_tiny_vless_manager rollback
}

abort_remote_tiny_vless() {
  tiny_vless_deployment_selected || return 0
  run_remote_tiny_vless_manager abort
}

deploy_remote_tiny_vless_from_release() {
  local release_id="$1"
  tiny_vless_deployment_selected || return 0
  run_remote_tiny_vless_manager deploy "${release_id}"
}

validate_remote_tiny_vless_workload_health() {
  local remote_release_dir="${1:-${REMOTE_CURRENT_LINK}}"
  local level="${2:-${VALIDATION_PROFILE}}"
  if run_remote_tiny_vless_manager_at_source \
    validate \
    "${remote_release_dir}/infra/arbuzas/tiny-vless" \
    "${level}"; then
    return 0
  fi
  mark_remote_validation_failed
  return 1
}

resolve_remote_release_dir() {
  local target_release_id="${1:-${requested_release_id}}"
  if [[ -n "${target_release_id}" ]]; then
    printf '%s\n' "${REMOTE_RELEASES_ROOT}/${target_release_id}"
  else
    printf '%s\n' "${REMOTE_CURRENT_LINK}"
  fi
}

collect_remote_validation_diagnostics() {
  local diagnostics_release_dir="$1"
  shift || true
  local services=("$@")
  local service_args=""

  for service_name in ${services[@]+"${services[@]}"}; do
    service_args+=" ${service_name}"
  done

  remote_compose_shell "${diagnostics_release_dir}" "
    compose ps >&2 || true
    for service_name in${service_args}; do
      echo \"--- logs: \${service_name} ---\" >&2
      compose logs --tail=80 \"\${service_name}\" >&2 || true
    done
  " || true
}

mark_remote_validation_failed() {
  REMOTE_VALIDATION_FAILED=1
}

return_remote_validation_status() {
  (( ${REMOTE_VALIDATION_FAILED:-0} == 0 ))
}

validate_remote_probe() {
  local probe_release_dir="$1"
  local label="$2"
  local script="$3"
  shift 3
  local services=("$@")

  log "Validate: ${label}"
  if ! remote_compose_shell "${probe_release_dir}" "${script}"; then
    log "Validation failed: ${label}"
    mark_remote_validation_failed
    collect_remote_validation_diagnostics "${probe_release_dir}" ${services[@]+"${services[@]}"}
    return 1
  fi
}

validate_remote_host_probe() {
  local diagnostics_release_dir="$1"
  local label="$2"
  local script="$3"
  shift 3
  local services=("$@")

  log "Validate: ${label}"
  if ! remote_shell "${script}"; then
    log "Validation failed: ${label}"
    mark_remote_validation_failed
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" ${services[@]+"${services[@]}"}
    return 1
  fi
}

wait_until_local_ok() {
  local deadline=$((SECONDS + 90))
  while true; do
    if "$@"; then
      return 0
    fi
    if (( SECONDS >= deadline )); then
      return 1
    fi
    sleep 5
  done
}

is_valid_ipv4() {
  [[ "${1:-}" =~ ^([0-9]{1,3}[.]){3}[0-9]{1,3}$ ]]
}

is_valid_ipv6() {
  [[ "${1:-}" == *:* ]]
}

is_private_ipv4() {
  local ip="${1:-}"
  is_valid_ipv4 "${ip}" || return 1
  case "${ip}" in
    10.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

is_tailscale_ipv4() {
  local ip="${1:-}"
  local o1="" o2="" o3="" o4=""
  is_valid_ipv4 "${ip}" || return 1
  IFS=. read -r o1 o2 o3 o4 <<< "${ip}"
  (( 10#${o1} == 100 && 10#${o2} >= 64 && 10#${o2} <= 127 ))
}

resolve_remote_public_ipv4() {
  local ip=""
  ip="$(
    remote_shell "
      if command -v curl >/dev/null 2>&1; then
        if ip=\$(curl -4 -fsS --max-time 10 'https://ifconfig.me/ip' 2>/dev/null); then
          printf '%s\n' \"\${ip}\"
          exit 0
        fi
        if ip=\$(curl -4 -fsS --max-time 10 'https://api.ipify.org' 2>/dev/null); then
          printf '%s\n' \"\${ip}\"
          exit 0
        fi
      fi
      python3 - <<'PY'
import urllib.request

print(urllib.request.urlopen('https://ifconfig.me/ip', timeout=10).read().decode().strip())
PY
    " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
  )" || return 1
  is_valid_ipv4 "${ip}" || return 1
  printf '%s\n' "${ip}"
}

resolve_remote_tailscale_ipv4() {
  local ip="${ARBUZAS_TAILSCALE_IPV4}"

  if is_valid_ipv4 "${ip}"; then
    printf '%s\n' "${ip}"
    return 0
  fi

  ip="$(
    remote_inline_shell "
      tailscale ip -4 | head -n 1
    " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
  )" || return 1

  is_valid_ipv4 "${ip}" || return 1
  printf '%s\n' "${ip}"
}

resolve_remote_tailscale_ipv6() {
  local ip=""

  ip="$(
    remote_inline_shell "
      tailscale ip -6 | head -n 1
    " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
  )" || return 1

  is_valid_ipv6 "${ip}" || return 1
  printf '%s\n' "${ip}"
}


resolve_remote_tailnet_self_name() {
  local hostname=""

  hostname="$(
    remote_inline_shell "
      python3 - <<'PY'
import json
import subprocess

payload = json.loads(subprocess.check_output(['tailscale', 'status', '--json'], text=True))
hostname = payload.get('Self', {}).get('DNSName', '').strip().rstrip('.')
if not hostname:
    raise SystemExit('missing Arbuzas Tailscale DNS name')
print(hostname)
PY
    " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
  )" || return 1

  [[ -n "${hostname}" ]] || return 1
  printf '%s\n' "${hostname}"
}

validate_remote_netdata() {
  local tailnet_dns_name=""

  log "Validate: netdata service active"
  remote_root_command "
    deadline=\$((SECONDS + 90))
    while true; do
      if systemctl is-active --quiet netdata; then
        break
      fi
      if (( SECONDS >= deadline )); then
        echo 'netdata service is not active' >&2
        exit 1
      fi
      sleep 5
    done
  "

  log "Validate: netdata local API responds"
  remote_root_command "
    deadline=\$((SECONDS + 90))
    while true; do
      if curl -fsS 'http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/api/v1/info' >/dev/null 2>/dev/null; then
        break
      fi
      if (( SECONDS >= deadline )); then
        echo 'Netdata local API did not answer on 127.0.0.1:${ARBUZAS_NETDATA_PORT}' >&2
        exit 1
      fi
      sleep 5
    done
  "

  log "Validate: the shared dashboard API contract returns current data"
  remote_root_command "
    NETDATA_DASHBOARD_API_ROOT='http://127.0.0.1:${ARBUZAS_NETDATA_PORT}' \
    python3 - <<'PY'
import json
import math
import os
import time
import urllib.request

base = os.environ['NETDATA_DASHBOARD_API_ROOT']
queries = {
    'cpu': (
        '/api/v3/data?contexts=system.cpu&after=-900&points=60&time_group=avg&group_by=dimension',
        {'user', 'system'},
    ),
    'memory': (
        '/api/v3/data?contexts=system.ram&after=-900&points=60&time_group=avg&group_by=dimension',
        {'free', 'used', 'cached', 'buffers'},
    ),
    'load': (
        '/api/v3/data?contexts=system.load&after=-900&points=60&time_group=avg&group_by=dimension',
        {'load1'},
    ),
    'network': (
        '/api/v3/data?contexts=system.net&after=-900&points=60&time_group=avg&group_by=dimension',
        {'received', 'sent'},
    ),
    'disk I/O': (
        '/api/v3/data?contexts=disk.io&instances=disk.sda&after=-900&points=60&time_group=avg&group_by=dimension',
        {'reads', 'writes'},
    ),
    'disk utilization': (
        '/api/v3/data?contexts=disk.util&instances=disk_util.sda&after=-900&points=60&time_group=avg&group_by=dimension',
        {'utilization'},
    ),
    'disk space': (
        '/api/v3/data?contexts=disk.space&instances=disk_space.%2F&after=-60&points=1&time_group=avg&group_by=dimension',
        {'avail', 'used', 'reserved for root'},
    ),
    'uptime': (
        '/api/v3/data?contexts=system.uptime&after=-60&points=1&time_group=avg&group_by=dimension',
        {'uptime'},
    ),
    'services': (
        '/api/v3/data?contexts=systemd.service_unit_state&after=-60&points=1&time_group=avg&group_by=instance,dimension',
        set(),
    ),
    'container CPU': (
        '/api/v3/data?contexts=cgroup.cpu&after=-60&points=1&time_group=avg&group_by=instance',
        set(),
    ),
    'container memory': (
        '/api/v3/data?contexts=cgroup.mem_usage&after=-60&points=1&time_group=avg&group_by=instance',
        set(),
    ),
}

def fetch(path):
    request = urllib.request.Request(base + path, headers={'Accept': 'application/json'})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)

nodes = fetch('/api/v3/nodes').get('nodes')
if not isinstance(nodes, list) or not nodes or not nodes[0].get('v'):
    raise SystemExit('dashboard nodes endpoint did not return the Netdata node and version')

alarms = fetch('/api/v1/alarms?active').get('alarms')
if not isinstance(alarms, dict):
    raise SystemExit('dashboard alarms endpoint did not return an alarms object')

for name, (path, required_labels) in queries.items():
    result = fetch(path).get('result', {})
    labels = result.get('labels')
    rows = result.get('data')
    if not isinstance(labels, list) or labels[:1] != ['time'] or not isinstance(rows, list) or not rows:
        raise SystemExit(f'dashboard {name} query returned no labeled data rows')
    missing = required_labels.difference(labels)
    if missing:
        raise SystemExit(f'dashboard {name} query is missing labels: {sorted(missing)}')
    timestamps = [float(row[0]) for row in rows if isinstance(row, list) and row and math.isfinite(float(row[0]))]
    if not timestamps or time.time() - max(timestamps) > 120:
        raise SystemExit(f'dashboard {name} query returned delayed metric samples')

    if name == 'services':
        for service in ('containerd', 'docker', 'netdata', 'ssh', 'tailscaled'):
            if not any(f'unit_{service}_service_state@' in str(label) for label in labels):
                raise SystemExit(f'dashboard services query is missing {service}.service')
    elif name == 'container CPU' and not any('.cpu@' in str(label) for label in labels):
        raise SystemExit('dashboard container CPU query returned no container instances')
    elif name == 'container memory' and not any('.mem_usage@' in str(label) for label in labels):
        raise SystemExit('dashboard container memory query returned no container instances')
PY
  "

  log "Validate: Netdata stays private, unclaimed, and opted out of telemetry"
  remote_root_command "
    [[ ! -f /var/lib/netdata/cloud.d/claim.conf ]]
    [[ ! -f '${NETDATA_REMOTE_CONFIG_DIR}/claim.conf' ]]
    [[ -f '${NETDATA_REMOTE_CONFIG_DIR}/.opt-out-from-anonymous-statistics' ]]
  "

  log "Validate: Netdata keeps focused host-service collection and Docker polling disabled"
  remote_root_command "
    [[ -f '${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' ]] || {
      echo 'missing Netdata Docker override: ${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' >&2
      exit 1
    }
    [[ -f '${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' ]] || {
      echo 'missing Netdata Docker service-discovery override: ${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' >&2
      exit 1
    }
    [[ -f '${NETDATA_REMOTE_SYSTEMD_CONFIG_FILE}' ]] || {
      echo 'missing Netdata systemd units config: ${NETDATA_REMOTE_SYSTEMD_CONFIG_FILE}' >&2
      exit 1
    }
    grep -F 'disabled: yes' '${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' >/dev/null
    grep -F 'disabled: yes' '${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' >/dev/null
    grep -F 'name: kitty-gration-core' '${NETDATA_REMOTE_SYSTEMD_CONFIG_FILE}' >/dev/null
  "

  log "Validate: the native Netdata dashboard is responsive and package-update resilient"
  remote_root_command "
    [[ -x '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' && \
       ! -L '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' ]] || {
      echo 'missing safe native Netdata dashboard patcher' >&2
      exit 1
    }
    [[ -f '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}' && \
       ! -L '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}' ]] || {
      echo 'missing native Netdata dashboard systemd service' >&2
      exit 1
    }
    [[ -f '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}' && \
       ! -L '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}' ]] || {
      echo 'missing native Netdata dashboard systemd drop-in' >&2
      exit 1
    }
    [[ \$(stat -c '%u:%g:%a' '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}') == '0:0:755' ]]
    [[ \$(stat -c '%u:%g:%a' '${NETDATA_REMOTE_NATIVE_DASHBOARD_SERVICE}') == '0:0:644' ]]
    [[ \$(stat -c '%u:%g:%a' '${NETDATA_REMOTE_NATIVE_DASHBOARD_DROPIN}') == '0:0:644' ]]
    systemctl show --value --property=Wants netdata.service | tr ' ' '\n' | \
      grep -Fx 'arbuzas-netdata-native-dashboard.service' >/dev/null
    systemctl show --value --property=After netdata.service | tr ' ' '\n' | \
      grep -Fx 'arbuzas-netdata-native-dashboard.service' >/dev/null
    '${NETDATA_REMOTE_NATIVE_DASHBOARD_PATCHER}' check \
      --web-root '${NETDATA_REMOTE_WEB_ROOT}'

    netdata_package_verify=\$(mktemp)
    trap 'rm -f \"\${netdata_package_verify}\"' EXIT
    dpkg --verify netdata-dashboard > \"\${netdata_package_verify}\" || true
    NETDATA_NATIVE_WEB_ROOT='${NETDATA_REMOTE_WEB_ROOT}' \
    NETDATA_NATIVE_BASE='http://127.0.0.1:${ARBUZAS_NETDATA_PORT}' \
    NETDATA_PACKAGE_VERIFY=\"\${netdata_package_verify}\" \
    python3 - <<'PY'
import json
import os
import re
import stat
import urllib.request
from pathlib import Path

root = Path(os.environ['NETDATA_NATIVE_WEB_ROOT'])
entrypoints = [
    root / 'index.html',
    root / 'v3/agent.html',
    root / 'v3/index.html',
    root / 'v3/local-agent.html',
]
marker = '<!-- arbuzas-native-mobile:start -->'
manifest = json.loads((root / 'kitty-gration/build.json').read_text(encoding='utf-8'))
native = manifest.get('nativeMobile', {})
script_name = native.get('script')
stylesheet_name = native.get('stylesheet')
if not isinstance(script_name, str) or not isinstance(stylesheet_name, str):
    raise SystemExit('native mobile manifest is missing its script or stylesheet')
quote = chr(34)
stylesheet_tag = (
    f'<link rel={quote}stylesheet{quote} href={quote}/kitty-gration/{stylesheet_name}{quote} '
    f'data-kitty-netdata-mobile={quote}stylesheet{quote}>'
)
script_tag = (
    f'<script defer src={quote}/kitty-gration/{script_name}{quote} '
    f'data-kitty-netdata-mobile={quote}script{quote}></script>'
)
viewport = re.compile(
    r'<meta\b[^>]*\bname\s*=\s*(?:\x22|\x27)viewport(?:\x22|\x27)[^>]*>',
    re.IGNORECASE,
)
device_width = re.compile(r'\bwidth\s*=\s*device-width\b', re.IGNORECASE)
managed = set()
for path in entrypoints:
    if path.is_symlink() or not path.is_file():
        raise SystemExit(f'native Netdata entrypoint is missing or unsafe: {path}')
    details = path.stat()
    if details.st_uid != 0 or details.st_gid != 0:
        raise SystemExit(f'native Netdata entrypoint is not root-owned: {path}')
    if stat.S_IMODE(details.st_mode) != 0o644:
        raise SystemExit(f'native Netdata entrypoint mode is not 0644: {path}')
    document = path.read_text(encoding='utf-8')
    matches = list(viewport.finditer(document))
    if len(matches) != 1 or not device_width.search(matches[0].group(0)):
        raise SystemExit(f'native Netdata entrypoint has no unique responsive viewport: {path}')
    if document.count(marker) != 1:
        raise SystemExit(f'native Netdata entrypoint has no unique managed block: {path}')
    if document.count(stylesheet_tag) != 1 or document.count(script_tag) != 1:
        raise SystemExit(f'native Netdata entrypoint does not reference the current mobile assets: {path}')
    if marker in document:
        managed.add(str(path))

reported = set()
verify_path = Path(os.environ['NETDATA_PACKAGE_VERIFY'])
for line in verify_path.read_text(encoding='utf-8').splitlines():
    fields = line.split()
    if fields:
        reported.add(fields[-1])
unexpected = reported.difference(managed)
missing = managed.difference(reported)
if unexpected:
    raise SystemExit(f'unexpected modified netdata-dashboard package files: {sorted(unexpected)!r}')
if missing:
    raise SystemExit(f'dpkg did not report the managed native HTML changes: {sorted(missing)!r}')

base = os.environ['NETDATA_NATIVE_BASE']
for path in ('/', '/v3/agent.html', '/v3/', '/v3/local-agent.html'):
    with urllib.request.urlopen(base + path, timeout=30) as response:
        served = response.read().decode('utf-8')
    if served.count(stylesheet_tag) != 1 or served.count(script_tag) != 1:
        raise SystemExit(f'Netdata did not serve the current native mobile assets at {path}')
PY
  "

  log "Validate: the shared Kitty-gration dashboard is installed and served"
  remote_root_command "
    NETDATA_DASHBOARD_DIR='${NETDATA_REMOTE_DASHBOARD_DIR}' \
    NETDATA_DASHBOARD_URL='http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/kitty-gration/' \
    python3 - <<'PY'
import json
import os
import re
import stat
import urllib.request
from email.utils import parsedate_to_datetime
from pathlib import Path

root = Path(os.environ['NETDATA_DASHBOARD_DIR'])
if not root.is_dir() or root.is_symlink():
    raise SystemExit('managed Kitty-gration dashboard directory is missing or unsafe')

manifest_path = root / 'build.json'
manifest = json.loads(manifest_path.read_text(encoding='utf-8'))
if manifest.get('dashboard') != 'Kitty-gration Operations':
    raise SystemExit('unexpected Kitty-gration dashboard manifest name')
if manifest.get('ui') != 'arrow' or manifest.get('api') != 'netdata-v3':
    raise SystemExit('Kitty-gration dashboard manifest is missing its UI or API contract')

assets = manifest.get('assets')
if not isinstance(assets, list) or len(assets) != 4:
    raise SystemExit('Kitty-gration dashboard manifest must list four hashed assets')
native_mobile = manifest.get('nativeMobile')
if not isinstance(native_mobile, dict) or set(native_mobile) != {'script', 'stylesheet', 'viewport'}:
    raise SystemExit('Kitty-gration dashboard manifest is missing its exact nativeMobile contract')
native_script_name = native_mobile.get('script')
native_style_name = native_mobile.get('stylesheet')
if not re.fullmatch(r'native-mobile\.[A-Z0-9]{8}\.js', str(native_script_name)):
    raise SystemExit('native Netdata mobile JavaScript is not safely content-hashed')
if not re.fullmatch(r'native-mobile\.[A-Z0-9]{8}\.css', str(native_style_name)):
    raise SystemExit('native Netdata mobile stylesheet is not safely content-hashed')
if native_mobile.get('viewport') != 'width=device-width, initial-scale=1, viewport-fit=cover':
    raise SystemExit('native Netdata mobile viewport is unexpected')
if native_script_name not in assets or native_style_name not in assets:
    raise SystemExit('native Netdata mobile assets are absent from the manifest asset list')
if len(set(assets)) != len(assets):
    raise SystemExit('Kitty-gration dashboard manifest repeats an asset')
expected_files = {'index.html', 'build.json', *assets}
entries = list(root.iterdir())
if any(path.is_symlink() or not path.is_file() for path in entries):
    raise SystemExit('dashboard directory contains a symlink or non-file entry')
actual_files = {path.name for path in entries}
missing_files = expected_files.difference(actual_files)
if missing_files:
    raise SystemExit(
        'Kitty-gration dashboard is missing current files: {!r}'.format(sorted(missing_files))
    )
retained_assets = actual_files.difference(expected_files)
invalid_retained = sorted(
    name for name in retained_assets
    if not re.fullmatch(r'(?:app|native-mobile)\.[A-Z0-9]{8}\.(?:js|css)', name)
)
if invalid_retained:
    raise SystemExit(f'dashboard contains invalid retained assets: {invalid_retained!r}')

for path in [root, *entries]:
    details = path.stat()
    if details.st_uid != 0:
        raise SystemExit(f'dashboard path is not root-owned: {path}')
    if path.is_dir():
        if stat.S_IMODE(details.st_mode) != 0o755:
            raise SystemExit(f'dashboard directory mode is not 0755: {path}')
    elif stat.S_IMODE(details.st_mode) != 0o644:
        raise SystemExit(f'dashboard file mode is not 0644: {path}')
    if path.is_symlink():
        raise SystemExit(f'dashboard path must not be a symlink: {path}')

index = (root / 'index.html').read_text(encoding='utf-8')
if 'Kitty-gration Operations' not in index or 'data-dashboard-build=' not in index:
    raise SystemExit('Kitty-gration dashboard index is missing its visible title or build marker')
for asset in assets:
    if asset.startswith('app.') and './' + asset not in index:
        raise SystemExit(f'dashboard index does not reference {asset}')
    if not re.fullmatch(r'(?:app|native-mobile)\.[A-Z0-9]{8}\.(?:js|css)', str(asset)):
        raise SystemExit(f'unsafe dashboard asset name: {asset}')

script_name = next((asset for asset in assets if asset.startswith('app.') and asset.endswith('.js')), None)
style_name = next((asset for asset in assets if asset.startswith('app.') and asset.endswith('.css')), None)
if script_name is None or style_name is None:
    raise SystemExit('dashboard manifest is missing its app JavaScript or CSS')
script = (root / script_name).read_text(encoding='utf-8')
stylesheet = (root / style_name).read_text(encoding='utf-8')
for marker in (
    'netdataDashboardUi',
    '/api/v3/data?contexts=system.cpu',
    '/api/v3/data?contexts=cgroup.cpu',
    'Containers by memory',
    'Core control plane',
):
    if marker not in script:
        raise SystemExit(f'dashboard JavaScript is missing marker: {marker}')
if '.summary-grid' not in stylesheet or '@media(max-width:720px)' not in stylesheet:
    raise SystemExit('dashboard stylesheet is missing desktop or phone layout rules')

native_script = (root / native_script_name).read_text(encoding='utf-8')
native_stylesheet = (root / native_style_name).read_text(encoding='utf-8')
selector_quote = chr(34)
for marker in (
    f'#main > [data-testid={selector_quote}collapsible{selector_quote}]',
    f'[data-testid={selector_quote}sidebar-tabs{selector_quote}]',
    f'[data-testid={selector_quote}sidebarHeader-icon{selector_quote}]',
    'requestAnimationFrame',
    'MutationObserver',
    'kittyNetdataMobileShim',
):
    if marker not in native_script:
        raise SystemExit(f'native Netdata mobile JavaScript is missing marker: {marker}')
for marker in (
    '@media(max-width:700px)',
    '[data-testid=dashboard-list]',
    '[data-menuid]',
    'scroll-snap-type:x mandatory',
    '[data-testid=chart][data-chartid]',
):
    if marker not in native_stylesheet:
        raise SystemExit(f'native Netdata mobile stylesheet is missing marker: {marker}')

with urllib.request.urlopen(os.environ['NETDATA_DASHBOARD_URL'], timeout=30) as response:
    served_index = response.read().decode('utf-8')
    cache_control = response.headers.get('Cache-Control', '').strip().lower()
    expires = response.headers.get('Expires', '').strip()
if 'Kitty-gration Operations' not in served_index:
    raise SystemExit('Netdata did not serve the managed Kitty-gration dashboard')
if cache_control == 'public':
    if not expires:
        raise SystemExit('cacheable dashboard index is missing an Expires header')
    cache_seconds = (parsedate_to_datetime(expires).timestamp() - __import__('time').time())
    if not 82_000 <= cache_seconds <= 88_000:
        raise SystemExit(f'unexpected dashboard index cache lifetime: {cache_seconds:.0f} seconds')
for asset in assets:
    with urllib.request.urlopen(os.environ['NETDATA_DASHBOARD_URL'] + asset, timeout=30) as response:
        if not response.read(1):
            raise SystemExit(f'Netdata served an empty dashboard asset: {asset}')
PY
  "

  log "Validate: netdata binds only to loopback"
  remote_root_command "
    netdata_pid=\$(systemctl show --value --property=MainPID netdata)
    [[ \"\${netdata_pid}\" =~ ^[1-9][0-9]*$ ]] || {
      echo 'Netdata has no active main process' >&2
      exit 1
    }
    netdata_listeners=\$(ss -ltnpH 'sport = :${ARBUZAS_NETDATA_PORT}' | grep -F \"pid=\${netdata_pid},\" || true)
    [[ -n \"\${netdata_listeners}\" ]] || {
      echo 'Netdata is not listening on port ${ARBUZAS_NETDATA_PORT}' >&2
      exit 1
    }
    unexpected_netdata_listeners=\$(
      printf '%s\n' \"\${netdata_listeners}\" |
        awk '{print \$4}' |
        grep -Ev '^(127\\.0\\.0\\.1|\\[::1\\]):${ARBUZAS_NETDATA_PORT}$' || true
    )
    if [[ -n \"\${unexpected_netdata_listeners}\" ]]; then
      printf 'Netdata has non-loopback listeners:\n%s\n' \"\${unexpected_netdata_listeners}\" >&2
      exit 1
    fi
  "

  log "Validate: Netdata charts cover the host, disks, containers, and core services"
  remote_root_command "
    NETDATA_CHARTS_URL='http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/api/v1/charts' \
    NETDATA_INFO_URL='http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/api/v1/info' \
    NETDATA_EXPECTED_CONTAINERS=\"\$(docker ps --filter 'label=com.docker.compose.project=arbuzas' --format '{{.Names}}' | sort | tr '\\n' ',')\" \
    python3 - <<'PY'
import json
import os
import sys
import time
import urllib.request

with urllib.request.urlopen(os.environ['NETDATA_INFO_URL'], timeout=30) as response:
    info = json.load(response)

reported_hostname = info.get('host_labels', {}).get('_hostname')
if reported_hostname != 'kitty-gration':
    print('unexpected Netdata hostname: {!r}'.format(reported_hostname), file=sys.stderr)
    sys.exit(1)

expected_labels = {
    'environment': 'production',
    'role': 'application-host',
    'stack': 'arbuzas',
    'access': 'tailscale-private',
}
labels = info.get('host_labels', {})
missing_labels = {
    key: value for key, value in expected_labels.items()
    if labels.get(key) != value
}
if missing_labels:
    print(f'missing expected Netdata host labels: {missing_labels}', file=sys.stderr)
    sys.exit(1)

expected_containers = [
    name for name in os.environ.get('NETDATA_EXPECTED_CONTAINERS', '').split(',')
    if name
]
if not expected_containers:
    print('no running Arbuzas containers were available for Netdata validation', file=sys.stderr)
    sys.exit(1)

expected_service_charts = {
    'systemdunits_kitty-gration-core.unit_containerd_service_state',
    'systemdunits_kitty-gration-core.unit_docker_service_state',
    'systemdunits_kitty-gration-core.unit_netdata_service_state',
    'systemdunits_kitty-gration-core.unit_ssh_service_state',
    'systemdunits_kitty-gration-core.unit_tailscaled_service_state',
}

deadline = time.monotonic() + 90
last_missing = []
missing_container_charts = []
missing_service_charts = []
charts = {}
while time.monotonic() < deadline:
    with urllib.request.urlopen(os.environ['NETDATA_CHARTS_URL'], timeout=30) as response:
        payload = json.load(response)

    charts = payload.get('charts', {})
    descriptors = []
    for chart_id, chart in charts.items():
        descriptor = ' '.join(
            str(value)
            for value in (
                chart_id,
                chart.get('name', ''),
                chart.get('family', ''),
                chart.get('context', ''),
                chart.get('title', ''),
                chart.get('type', ''),
            )
        ).lower()
        descriptors.append(descriptor)

    def has(predicate):
        return any(predicate(descriptor) for descriptor in descriptors)

    missing_container_charts = [
        name for name in expected_containers
        if 'cgroup_' + name + '.cpu' not in charts
    ]
    missing_service_charts = sorted(expected_service_charts.difference(charts))

    checks = {
        'cpu': has(lambda descriptor: 'system.cpu' in descriptor or 'cpu utilization' in descriptor),
        'memory': has(lambda descriptor: 'system.ram' in descriptor or 'ram utilization' in descriptor),
        'filesystem': has(lambda descriptor: 'disk_space' in descriptor or 'disk space' in descriptor),
        'disk_io': has(lambda descriptor: descriptor.startswith('disk.') or 'disk i/o' in descriptor or 'disk throughput' in descriptor),
        'containers': not missing_container_charts,
        'core_services': not missing_service_charts,
    }
    last_missing = [name for name, present in checks.items() if not present]
    if not last_missing:
        break
    time.sleep(5)
else:
    print('missing expected Netdata charts: ' + ', '.join(last_missing), file=sys.stderr)
    if missing_container_charts:
        print(
            'missing Arbuzas container CPU charts: ' + ', '.join(missing_container_charts),
            file=sys.stderr,
        )
    if missing_service_charts:
        print(
            'missing core service charts: ' + ', '.join(missing_service_charts),
            file=sys.stderr,
        )
    preview = '\n'.join(sorted(charts.keys())[:80])
    if preview:
        print(preview, file=sys.stderr)
    sys.exit(1)

docker_charts = sorted(
    chart_id for chart_id, chart in charts.items()
    if chart_id.startswith('docker.')
    or str(chart.get('context', '')).startswith('docker.')
)
if docker_charts:
    print('unexpected Docker charts still enabled: ' + ', '.join(docker_charts[:20]), file=sys.stderr)
    sys.exit(1)

sensor_charts = sorted(
    chart_id for chart_id, chart in charts.items()
    if 'temperature' in chart_id.lower()
    or 'fan' in chart_id.lower()
    or 'temperature' in str(chart.get('context', '')).lower()
    or 'fan' in str(chart.get('context', '')).lower()
)
if sensor_charts:
    print('optional hardware sensor charts: ' + ', '.join(sensor_charts[:20]))
else:
    print('optional hardware sensor charts unavailable on this VPS')
PY
  "

  log "Validate: current Netdata restart logs stay free of Docker collector activity"
  remote_root_command "
    invocation_id=\$(systemctl show --value --property=InvocationID netdata)
    [[ -n \"\${invocation_id}\" ]] || {
      echo 'failed to resolve the active Netdata invocation id' >&2
      exit 1
    }
    docker_log_matches=\$(journalctl _SYSTEMD_INVOCATION_ID=\"\${invocation_id}\" --namespace=netdata --no-pager | grep -E 'collector=docker|/images/json|/containers/json' || true)
    if [[ -n \"\${docker_log_matches}\" ]]; then
      printf '%s\n' \"\${docker_log_matches}\" >&2
      echo 'Netdata still logged Docker collector activity after restart' >&2
      exit 1
    fi
  "

  log "Validate: Tailscale Serve publishes the exact private Netdata HTTPS proxy"
  remote_root_command "
    netdata_serve_json=\$(tailscale serve status --json)
    NETDATA_SERVE_JSON=\"\${netdata_serve_json}\" \
    NETDATA_PORT='${ARBUZAS_NETDATA_PORT}' \
    python3 - <<'PY'
import json
import os
import subprocess

payload = json.loads(os.environ['NETDATA_SERVE_JSON'])
port = os.environ['NETDATA_PORT']
target = '127.0.0.1:' + port
proxy_target = 'http://' + target
dns_name = json.loads(
    subprocess.check_output(['tailscale', 'status', '--json'], text=True)
).get('Self', {}).get('DNSName', '').rstrip('.')
if not dns_name:
    raise SystemExit('missing Tailscale DNS name for Netdata HTTPS')
tcp = payload.get('TCP', {}).get(port)
web = {
    key: value for key, value in payload.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
allow_funnel = payload.get('AllowFunnel') or {}
if not isinstance(allow_funnel, dict):
    raise SystemExit('unexpected Tailscale AllowFunnel shape')
funnel = {
    str(key): value for key, value in allow_funnel.items()
    if str(key).rsplit(':', 1)[-1] == port
}
expected_web = {
    dns_name + ':' + port: {
        'Handlers': {'/': {'Proxy': proxy_target}}
    }
}
if tcp != {'HTTPS': True} or web != expected_web or funnel:
    raise SystemExit(
        'unexpected Netdata Serve route: tcp={!r}, web={!r}, funnel={!r}'.format(
            tcp, web, funnel
        )
    )
PY
  "

  tailnet_dns_name="$(resolve_remote_tailnet_self_name)" || {
    echo "failed to resolve the Arbuzas Tailscale DNS name" >&2
    exit 1
  }

  log "Validate: netdata is reachable from this operator machine at https://${tailnet_dns_name}:${ARBUZAS_NETDATA_PORT}"
  if ! wait_until_local_ok curl -fsS "https://${tailnet_dns_name}:${ARBUZAS_NETDATA_PORT}/api/v1/info" >/dev/null 2>&1; then
    echo "Netdata did not answer over Tailscale at https://${tailnet_dns_name}:${ARBUZAS_NETDATA_PORT}/api/v1/info" >&2
    exit 1
  fi
  native_netdata_html="$(curl -fsS "https://${tailnet_dns_name}:${ARBUZAS_NETDATA_PORT}/")" || {
    echo "Native Netdata dashboard did not answer over Tailscale HTTPS" >&2
    exit 1
  }
  if ! grep -F 'width=device-width' <<< "${native_netdata_html}" >/dev/null || \
     ! grep -F 'data-kitty-netdata-mobile="stylesheet"' <<< "${native_netdata_html}" >/dev/null || \
     ! grep -F 'data-kitty-netdata-mobile="script"' <<< "${native_netdata_html}" >/dev/null; then
    echo "Native Netdata dashboard did not serve its responsive viewport over Tailscale HTTPS" >&2
    exit 1
  fi
  if ! curl -fsS "https://${tailnet_dns_name}:${ARBUZAS_NETDATA_PORT}/kitty-gration/" | grep -F 'Kitty-gration Operations' >/dev/null; then
    echo "Kitty-gration dashboard did not render its server-owned shell over Tailscale HTTPS" >&2
    exit 1
  fi
}

validate_remote_memory_report() {
  log "Validate: corrected memory report service files are installed"
  remote_root_command "
    [[ -f '${MEMORY_REPORT_REMOTE_SERVICE_FILE}' ]] || {
      echo 'missing memory report service file: ${MEMORY_REPORT_REMOTE_SERVICE_FILE}' >&2
      exit 1
    }
    [[ -f '${MEMORY_REPORT_REMOTE_TIMER_FILE}' ]] || {
      echo 'missing memory report timer file: ${MEMORY_REPORT_REMOTE_TIMER_FILE}' >&2
      exit 1
    }
    [[ -f '${MEMORY_REPORT_REMOTE_DEFAULT_FILE}' ]] || {
      echo 'missing memory report defaults file: ${MEMORY_REPORT_REMOTE_DEFAULT_FILE}' >&2
      exit 1
    }
    [[ -x '${MEMORY_REPORT_REMOTE_SCRIPT_FILE}' ]] || {
      echo 'missing executable memory report script: ${MEMORY_REPORT_REMOTE_SCRIPT_FILE}' >&2
      exit 1
    }
  "

  log "Validate: corrected memory report timer is active"
  remote_root_command "
    systemctl is-enabled --quiet arbuzas-memory-report.timer
    systemctl is-active --quiet arbuzas-memory-report.timer
  "

  log "Validate: corrected memory report publishes real pressure and cache separately"
  remote_root_command "
    systemctl start arbuzas-memory-report.service
    [[ -s '${MEMORY_REPORT_REMOTE_JSON_FILE}' ]] || {
      echo 'missing memory report JSON output: ${MEMORY_REPORT_REMOTE_JSON_FILE}' >&2
      exit 1
    }
    [[ -s '${MEMORY_REPORT_REMOTE_TEXT_FILE}' ]] || {
      echo 'missing memory report text output: ${MEMORY_REPORT_REMOTE_TEXT_FILE}' >&2
      exit 1
    }
    [[ -s '${MEMORY_REPORT_REMOTE_PROM_FILE}' ]] || {
      echo 'missing memory report metrics output: ${MEMORY_REPORT_REMOTE_PROM_FILE}' >&2
      exit 1
    }
    python3 - '${MEMORY_REPORT_REMOTE_JSON_FILE}' <<'PY'
import json
import sys

with open(sys.argv[1], 'r', encoding='utf-8') as handle:
    report = json.load(handle)

required = [
    'real_pressure_pct',
    'provider_like_pct',
    'reclaimable_cache_pct',
    'available_kb',
    'total_kb',
]
missing = [key for key in required if key not in report]
if missing:
    raise SystemExit('memory report missing keys: ' + ', '.join(missing))

real_pressure = float(report['real_pressure_pct'])
provider_like = float(report['provider_like_pct'])
reclaimable_cache = float(report['reclaimable_cache_pct'])
if not 0.0 <= real_pressure <= 100.0:
    raise SystemExit(f'real pressure out of range: {real_pressure}')
if not 0.0 <= provider_like <= 100.0:
    raise SystemExit(f'provider-like memory out of range: {provider_like}')
if not 0.0 <= reclaimable_cache <= 100.0:
    raise SystemExit(f'reclaimable cache out of range: {reclaimable_cache}')
if provider_like < real_pressure:
    raise SystemExit(f'provider-like value {provider_like} is lower than real pressure {real_pressure}')
if report.get('formulas', {}).get('real_pressure') != '(MemTotal - MemAvailable) / MemTotal':
    raise SystemExit('real pressure formula must use MemAvailable')
if report.get('formulas', {}).get('provider_like') != '(MemTotal - MemFree - Buffers) / MemTotal':
    raise SystemExit('provider-like formula changed')
PY
    grep -F 'Source of truth:' '${MEMORY_REPORT_REMOTE_TEXT_FILE}' >/dev/null
    grep -F 'arbuzas_memory_real_pressure_percent' '${MEMORY_REPORT_REMOTE_PROM_FILE}' >/dev/null
  "
}

validate_remote_thinkpad_fan() {
  log "Validate: ThinkPad fan controller service active"
  remote_root_command "
    systemctl is-active --quiet arbuzas-thinkpad-fan.service
  "

  log "Validate: ThinkPad fan controller files are installed and manual control is enabled"
  remote_root_command "
    [[ -f '${THINKPAD_FAN_REMOTE_SERVICE_FILE}' ]] || {
      echo 'missing ThinkPad fan controller service file: ${THINKPAD_FAN_REMOTE_SERVICE_FILE}' >&2
      exit 1
    }
    [[ -f '${THINKPAD_FAN_REMOTE_DEFAULT_FILE}' ]] || {
      echo 'missing ThinkPad fan controller defaults file: ${THINKPAD_FAN_REMOTE_DEFAULT_FILE}' >&2
      exit 1
    }
    [[ -f '${THINKPAD_FAN_REMOTE_MODPROBE_FILE}' ]] || {
      echo 'missing ThinkPad fan controller modprobe file: ${THINKPAD_FAN_REMOTE_MODPROBE_FILE}' >&2
      exit 1
    }
    [[ -x '${THINKPAD_FAN_REMOTE_SCRIPT_FILE}' ]] || {
      echo 'missing executable ThinkPad fan controller script: ${THINKPAD_FAN_REMOTE_SCRIPT_FILE}' >&2
      exit 1
    }
    grep -Fx 'options thinkpad_acpi fan_control=1' '${THINKPAD_FAN_REMOTE_MODPROBE_FILE}' >/dev/null
    [[ \$(cat '${THINKPAD_FAN_REMOTE_PARAM_FILE}' 2>/dev/null) == 'Y' ]]
  "

  log "Validate: ThinkPad fan controller matches the expected mode for the current temperature"
  remote_root_command "
    temp_file=\$(ls ${THINKPAD_FAN_REMOTE_TEMP_GLOB} 2>/dev/null | head -n 1)
    [[ -n \"\${temp_file}\" ]] || {
      echo 'missing ThinkPad CPU temperature sensor' >&2
      exit 1
    }
    temp_c=\$(awk '{printf \"%.1f\", \$1/1000}' \"\${temp_file}\")
    fan_state=\$(cat '${THINKPAD_FAN_REMOTE_PROC_FILE}')
    level=\$(printf '%s\n' \"\${fan_state}\" | awk -F': *' '/^level:/ {gsub(/^[[:space:]]+|[[:space:]]+$/, \"\", \$2); print \$2}')
    if awk 'BEGIN { exit !('"\"\${temp_c}\""' >= '"${ARBUZAS_FAN_ENTER_AUTO_C}"') }'; then
      [[ \"\${level}\" == 'auto' ]] || {
        echo \"unexpected ThinkPad fan level \${level} for temp \${temp_c}C; expected auto\" >&2
        exit 1
      }
    elif awk 'BEGIN { exit !('"\"\${temp_c}\""' <= '"${ARBUZAS_FAN_EXIT_AUTO_C}"') }'; then
      [[ \"\${level}\" == '1' ]] || {
        echo \"unexpected ThinkPad fan level \${level} for temp \${temp_c}C; expected level 1\" >&2
        exit 1
      }
    else
      [[ \"\${level}\" == '1' || \"\${level}\" == 'auto' ]] || {
        echo \"unexpected ThinkPad fan level \${level} for temp \${temp_c}C; expected level 1 or auto\" >&2
        exit 1
      }
    fi
  "
}

copy_tree_into_release() {
  local path="$1"
  (
    cd "${REPO_ROOT}"
    tar \
      --no-xattrs \
      --no-mac-metadata \
      --exclude='node_modules' \
      --exclude="${path}/.artifacts" \
      --exclude="${path}/.codex-tmp" \
      --exclude="${path}/.DS_Store" \
      --exclude="${path}/.env" \
      --exclude="${path}/.env.*" \
      --exclude="${path}/.gradle" \
      --exclude="${path}/.kotlin" \
      --exclude="${path}/.pytest_cache" \
      --exclude="${path}/.venv" \
      --exclude="${path}/__pycache__" \
      --exclude="${path}/bin" \
      --exclude="${path}/build" \
      --exclude="${path}/dogfood-output" \
      --exclude="${path}/node_modules" \
      --exclude="${path}/ops/evidence" \
      --exclude="${path}/output" \
      --exclude="${path}/state" \
      --exclude="${path}/*.env" \
      --exclude="${path}/*.secret" \
      --exclude="${path}/*.db" \
      --exclude="${path}/*.db.lock" \
      --exclude="${path}/*.instance.lock" \
      --exclude="${path}/data/*.db" \
      --exclude="${path}/data/*.db.lock" \
      --exclude="${path}/data/catalog" \
      --exclude="${path}/data/public-bundles" \
      --exclude="${path}/data/schedules/*.json" \
      --exclude="${path}/spacetimedb/dist" \
      --exclude="${path}/spacetimedb/target" \
      --exclude="${path}/spacetime-sidecar/target" \
      --exclude="${path}/target" \
      --exclude="${path}/tmp" \
      --exclude="${path}/web-client/src/generated" \
      -cf - "${path}"
  ) | (
    cd "${ARBUZAS_RELEASE_DIR}"
    tar -xf -
  )
}

compute_release_source_commit() {
  if git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    git -C "${REPO_ROOT}" rev-parse HEAD 2>/dev/null || printf 'nogit\n'
  else
    printf 'nogit\n'
  fi
}

compute_release_source_dirty() {
  if ! git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    printf 'unknown\n'
    return
  fi
  if [[ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=all -- infra/arbuzas/docker infra/arbuzas/qbittorrent infra/arbuzas/jellyfin infra/arbuzas/tiny-vless tools/arbuzas test_arbuzas_deploy_contract.sh test_ticket_phone_bridge_hardening.sh workloads/shared-go workloads/train-bot workloads/satiksme-bot workloads/subscription-bot workloads/ticket-remote workloads/qbittorrent-housekeeper)" ]]; then
    printf 'dirty\n'
  else
    printf 'clean\n'
  fi
}

enforce_release_source_policy() {
  local source_dirty
  source_dirty="$(compute_release_source_dirty)"
  if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
    if [[ "${source_dirty}" != "clean" ]]; then
      if [[ "${ARBUZAS_ALLOW_DIRTY_FAST_RELEASE:-0}" != "1" ]]; then
        echo "Refusing fast deployment from ${source_dirty} source. Commit the release inputs or explicitly set ARBUZAS_ALLOW_DIRTY_FAST_RELEASE=1 for a temporary iteration release." >&2
        return 1
      fi
      log "Explicit temporary dirty fast release allowed; replace it with a clean standard or full release before close-out"
    fi
    return 0
  fi
  if [[ "${source_dirty}" != "clean" ]]; then
    echo "Refusing ${VALIDATION_PROFILE} deployment from ${source_dirty} source. Commit the release inputs or use an explicitly targeted fast iteration deploy first." >&2
    return 1
  fi
}

compute_release_source_sha256() {
  python3 - "${ARBUZAS_RELEASE_DIR}" <<'PY'
import hashlib
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
included_roots = [
    pathlib.Path("infra/arbuzas/docker"),
    pathlib.Path("infra/arbuzas/qbittorrent"),
    pathlib.Path("infra/arbuzas/jellyfin"),
    pathlib.Path("infra/arbuzas/tiny-vless"),
    pathlib.Path("tools/arbuzas"),
    pathlib.Path("workloads/shared-go"),
    pathlib.Path("workloads/train-bot"),
    pathlib.Path("workloads/satiksme-bot"),
    pathlib.Path("workloads/subscription-bot"),
    pathlib.Path("workloads/ticket-remote"),
    pathlib.Path("workloads/qbittorrent-housekeeper"),
]
entries = []
for included in included_roots:
    base = root / included
    for path in base.rglob("*"):
        if path.is_file():
            rel = path.relative_to(root).as_posix()
            digest = hashlib.sha256(path.read_bytes()).hexdigest()
            entries.append((rel, digest))

manifest = hashlib.sha256()
manifest.update(b"arbuzas-release-source-v1\n")
for rel, digest in sorted(entries):
    manifest.update(digest.encode("ascii"))
    manifest.update(b"  ")
    manifest.update(rel.encode("utf-8"))
    manifest.update(b"\n")

print(manifest.hexdigest())
PY
}

validate_release_identity_values() {
  case "${ARBUZAS_RELEASE_SOURCE_DIRTY}" in
    clean | dirty | unknown) ;;
    *)
      echo "Invalid ARBUZAS_RELEASE_SOURCE_DIRTY=${ARBUZAS_RELEASE_SOURCE_DIRTY}; expected clean, dirty, or unknown" >&2
      return 1
      ;;
  esac
  if ! [[ "${ARBUZAS_RELEASE_SOURCE_SHA256}" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Invalid ARBUZAS_RELEASE_SOURCE_SHA256=${ARBUZAS_RELEASE_SOURCE_SHA256}; expected 64 lowercase hex characters" >&2
    return 1
  fi
}

prepare_local_release_metadata() {
  copy_tree_into_release "tools/arbuzas"

  ARBUZAS_RELEASE_SOURCE_COMMIT="$(compute_release_source_commit)"
  ARBUZAS_RELEASE_SOURCE_DIRTY="$(compute_release_source_dirty)"
  ARBUZAS_RELEASE_SOURCE_SHA256="$(compute_release_source_sha256)"
  validate_release_identity_values

  (
    umask 077
    cat > "${ARBUZAS_RELEASE_DIR}/release.env" <<EOF
ARBUZAS_RELEASE_ID=${ARBUZAS_RELEASE_ID}
ARBUZAS_RELEASE_SOURCE_COMMIT=${ARBUZAS_RELEASE_SOURCE_COMMIT}
ARBUZAS_RELEASE_SOURCE_DIRTY=${ARBUZAS_RELEASE_SOURCE_DIRTY}
ARBUZAS_RELEASE_SOURCE_SHA256=${ARBUZAS_RELEASE_SOURCE_SHA256}
ARBUZAS_TZ=${ARBUZAS_TZ}
ARBUZAS_TRAIN_BOT_PORT=${ARBUZAS_TRAIN_BOT_PORT}
ARBUZAS_SATIKSME_BOT_PORT=${ARBUZAS_SATIKSME_BOT_PORT}
ARBUZAS_SUBSCRIPTION_BOT_PORT=${ARBUZAS_SUBSCRIPTION_BOT_PORT}
ARBUZAS_TICKET_REMOTE_PORT=${ARBUZAS_TICKET_REMOTE_PORT}
ARBUZAS_QBITTORRENT_WEBUI_PORT=${ARBUZAS_QBITTORRENT_WEBUI_PORT}
ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT=${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT}
ARBUZAS_QBITTORRENT_PEER_PORT=${ARBUZAS_QBITTORRENT_PEER_PORT}
ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT=${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}
ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME=${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}
ARBUZAS_QBITTORRENT_PUID=${ARBUZAS_QBITTORRENT_PUID}
ARBUZAS_QBITTORRENT_PGID=${ARBUZAS_QBITTORRENT_PGID}
ARBUZAS_JELLYFIN_HOST_PORT=${ARBUZAS_JELLYFIN_HOST_PORT}
ARBUZAS_JELLYFIN_INTERNAL_PORT=${ARBUZAS_JELLYFIN_INTERNAL_PORT}
ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT=${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}
ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME=${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}
ARBUZAS_JELLYFIN_PUID=${ARBUZAS_JELLYFIN_PUID}
ARBUZAS_JELLYFIN_PGID=${ARBUZAS_JELLYFIN_PGID}
ARBUZAS_TICKET_PHONE_ADB_TARGET=${ARBUZAS_TICKET_PHONE_ADB_TARGET}
ARBUZAS_TICKET_TUNNEL_UID=${ARBUZAS_TICKET_TUNNEL_UID}
ARBUZAS_TICKET_TUNNEL_GID=${ARBUZAS_TICKET_TUNNEL_GID}
ARBUZAS_TRAIN_BOT_HOSTNAME=${ARBUZAS_TRAIN_BOT_HOSTNAME}
ARBUZAS_SATIKSME_BOT_HOSTNAME=${ARBUZAS_SATIKSME_BOT_HOSTNAME}
ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME=${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME}
ARBUZAS_TICKET_REMOTE_HOSTNAME=${ARBUZAS_TICKET_REMOTE_HOSTNAME}
ARBUZAS_TICKET_REMOTE_AUTH_MODE=${ARBUZAS_TICKET_REMOTE_AUTH_MODE:-spacetime}
ARBUZAS_TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN=${ARBUZAS_TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN:-}
ARBUZAS_TICKET_REMOTE_CF_ACCESS_AUDIENCE=${ARBUZAS_TICKET_REMOTE_CF_ACCESS_AUDIENCE:-}
ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_ISSUER=${ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_ISSUER:-https://auth.spacetimedb.com/oidc}
ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID=${ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID:-}
ARBUZAS_TICKET_REMOTE_SERVICE_EVENT_TOKEN=${ARBUZAS_TICKET_REMOTE_SERVICE_EVENT_TOKEN:-}
OPERATIONAL_LOGGING_HOST=${OPERATIONAL_LOGGING_HOST:-https://maincloud.spacetimedb.com}
OPERATIONAL_LOGGING_DATABASE=${OPERATIONAL_LOGGING_DATABASE:-operational-logging-prod}
ARBUZAS_CLOUDFLARED_IMAGE=${ARBUZAS_CLOUDFLARED_IMAGE}
ARBUZAS_TICKET_CLOUDFLARED_IMAGE=${ARBUZAS_TICKET_CLOUDFLARED_IMAGE}
EOF
  )
  chmod 0600 "${ARBUZAS_RELEASE_DIR}/release.env"
}

prepare_local_release_bundle() {
  log "Preparing local release bundle ${ARBUZAS_RELEASE_ID}"
  rm -rf "${ARBUZAS_RELEASE_DIR}"
  mkdir -p "${ARBUZAS_RELEASE_DIR}/generated/cloudflared"

  copy_tree_into_release "infra/arbuzas/docker"
  copy_tree_into_release "infra/arbuzas/qbittorrent"
  copy_tree_into_release "infra/arbuzas/jellyfin"
  copy_tree_into_release "infra/arbuzas/tiny-vless"
  copy_tree_into_release "workloads/shared-go"
  copy_tree_into_release "workloads/train-bot"
  copy_tree_into_release "workloads/satiksme-bot"
  copy_tree_into_release "workloads/subscription-bot"
  copy_tree_into_release "workloads/ticket-remote"
  copy_tree_into_release "workloads/qbittorrent-housekeeper"

  prepare_local_release_metadata
}

copy_tree_into_fast_release_overlay() {
  local path="$1"

  if array_contains "${path}" ${FAST_RELEASE_OVERLAY_PATHS[@]+"${FAST_RELEASE_OVERLAY_PATHS[@]}"}; then
    return
  fi
  copy_tree_into_release "${path}"
  append_unique FAST_RELEASE_OVERLAY_PATHS "${path}"
}

prepare_local_fast_release_overlay() {
  local service_name
  local fast_runtime_ids=""
  local fast_runtime_uid=""
  local fast_runtime_gid=""

  log "Preparing selected-service release overlay ${ARBUZAS_RELEASE_ID}"
  rm -rf "${ARBUZAS_RELEASE_DIR}"
  mkdir -p "${ARBUZAS_RELEASE_DIR}"
  FAST_RELEASE_OVERLAY_PATHS=()

  copy_tree_into_fast_release_overlay "infra/arbuzas/docker"
  if (( VALIDATE_TINY_VLESS == 1 )); then
    copy_tree_into_fast_release_overlay "infra/arbuzas/tiny-vless"
  fi
  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    case "${service_name}" in
      train_bot)
        copy_tree_into_fast_release_overlay "workloads/shared-go"
        copy_tree_into_fast_release_overlay "workloads/train-bot"
        ;;
      satiksme_bot)
        copy_tree_into_fast_release_overlay "workloads/shared-go"
        copy_tree_into_fast_release_overlay "workloads/satiksme-bot"
        ;;
      subscription_bot)
        copy_tree_into_fast_release_overlay "workloads/subscription-bot"
        ;;
      ticket_remote_spacetime_sidecar|ticket_remote)
        copy_tree_into_fast_release_overlay "workloads/ticket-remote"
        ;;
      qbittorrent|qbittorrent_housekeeper)
        copy_tree_into_fast_release_overlay "infra/arbuzas/qbittorrent"
        copy_tree_into_fast_release_overlay "workloads/qbittorrent-housekeeper"
        ;;
      jellyfin)
        copy_tree_into_fast_release_overlay "infra/arbuzas/qbittorrent"
        copy_tree_into_fast_release_overlay "infra/arbuzas/jellyfin"
        ;;
      train_tunnel|satiksme_tunnel|subscription_tunnel|ticket_phone_bridge|ticket_remote_tunnel)
        ;;
      *)
        echo "No fast release overlay mapping for service: ${service_name}" >&2
        return 2
        ;;
    esac
  done

  fast_runtime_ids="$(remote_inline_shell "printf '%s:%s\\n' \"\$(id -u)\" \"\$(id -g)\"")"
  IFS=: read -r fast_runtime_uid fast_runtime_gid <<< "${fast_runtime_ids}"
  [[ "${fast_runtime_uid}" =~ ^[1-9][0-9]*$ ]] || {
    echo "Unable to resolve a positive fast-release runtime UID for ${ARBUZAS_USER} on ${ARBUZAS_HOST}" >&2
    return 1
  }
  [[ "${fast_runtime_gid}" =~ ^[1-9][0-9]*$ ]] || {
    echo "Unable to resolve a positive fast-release runtime GID for ${ARBUZAS_USER} on ${ARBUZAS_HOST}" >&2
    return 1
  }
  ARBUZAS_QBITTORRENT_PUID="${fast_runtime_uid}"
  ARBUZAS_QBITTORRENT_PGID="${fast_runtime_gid}"
  ARBUZAS_JELLYFIN_PUID="${fast_runtime_uid}"
  ARBUZAS_JELLYFIN_PGID="${fast_runtime_gid}"

  prepare_local_release_metadata
  append_unique FAST_RELEASE_OVERLAY_PATHS "tools/arbuzas"
  append_unique FAST_RELEASE_OVERLAY_PATHS "release.env"
}

append_csv_unique() {
  local existing="$1"
  local candidate="$2"
  local entry
  local old_ifs
  candidate="$(printf '%s' "${candidate}" | tr -d '\r\n[:space:]')"
  if [[ -z "${candidate}" ]]; then
    printf '%s' "${existing}"
    return
  fi
  old_ifs="${IFS}"
  IFS=','
  for entry in ${existing}; do
    entry="$(printf '%s' "${entry}" | tr -d '\r\n[:space:]')"
    if [[ "${entry}" == "${candidate}" ]]; then
      IFS="${old_ifs}"
      printf '%s' "${existing}"
      return
    fi
  done
  IFS="${old_ifs}"
  if [[ -z "${existing}" ]]; then
    printf '%s' "${candidate}"
  else
    printf '%s,%s' "${existing}" "${candidate}"
  fi
}

prepare_remote_ticket_runtime_permissions() {
  remote_root_command "
    secure_private_file() {
      local path=\"\$1\"
      local owner=\"\$2\"
      if [[ -e \"\${path}\" || -L \"\${path}\" ]]; then
        [[ -f \"\${path}\" && ! -L \"\${path}\" ]] || {
          echo \"refusing unsafe private deployment file: \${path}\" >&2
          exit 1
        }
        chown \"\${owner}\" \"\${path}\"
        chmod 0600 \"\${path}\"
      fi
    }

    find '/etc/arbuzas/env' -mindepth 1 -maxdepth 1 \
      \( -type f -o -type l \) \
      \( -name '*.bak*' -o -name '*.before-*' -o -name '*.retired-*' -o -name '*~' \) \
      -delete

    install -d -o root -g root -m 0700 '/etc/arbuzas/secrets/satiksme-chat-analyzer'
    for path in \
      '/etc/arbuzas/env/train-bot.env' \
      '/etc/arbuzas/env/satiksme-bot.env' \
      '/etc/arbuzas/env/subscription-bot.env' \
      '/etc/arbuzas/secrets/satiksme-chat-analyzer/telegram-api-id.secret' \
      '/etc/arbuzas/secrets/satiksme-chat-analyzer/telegram-api-hash.secret' \
      '/etc/arbuzas/secrets/satiksme-chat-analyzer/google-api-key.secret' \
      '/etc/arbuzas/secrets/satiksme-chat-analyzer/model-api-key.secret' \
      '/etc/arbuzas/secrets/satiksme-bot-spacetime.key' \
      '/etc/arbuzas/secrets/satiksme-bot-web-session-secret' \
      '/etc/arbuzas/secrets/satiksme-telegram-client.secret' \
      '/etc/arbuzas/secrets/train-bot-spacetime.key' \
      '/etc/arbuzas/secrets/train-bot-web-session-secret' \
      '/etc/arbuzas/secrets/train-bot-test-ticket.secret' \
      '/etc/arbuzas/cloudflared/train-bot.json' \
      '/etc/arbuzas/cloudflared/satiksme-bot.json' \
      '/etc/arbuzas/cloudflared/subscription-bot.json' \
      '/srv/arbuzas/satiksme-bot/state/chat-analyzer.session'; do
      secure_private_file \"\${path}\" 'root:root'
    done

    install -d -o 1001 -g 1001 -m 0750 '/srv/arbuzas/ticket-remote/state'
    for path in \
      '/etc/arbuzas/env/ticket-remote.env' \
      '/etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem' \
      '/etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret'; do
      secure_private_file \"\${path}\" '1001:1001'
    done
    for path in '/etc/arbuzas/secrets/android-adb/adbkey' '/etc/arbuzas/secrets/android-adb/adbkey.pub' '/etc/arbuzas/secrets/android-adb/adb_known_hosts.pb'; do
      secure_private_file \"\${path}\" '1002:1002'
    done
    secure_private_file \
      '/etc/arbuzas/cloudflared/ticket-remote.json' \
      '${ARBUZAS_TICKET_TUNNEL_UID}:${ARBUZAS_TICKET_TUNNEL_GID}'
  "
}

prepare_remote_host_layout() {
  remote_root_command "
    command -v docker >/dev/null 2>&1 || { echo 'docker is required on ${ARBUZAS_HOST}' >&2; exit 1; }
    docker compose version >/dev/null 2>&1 || { echo 'docker compose is required on ${ARBUZAS_HOST}' >&2; exit 1; }
    command -v python3 >/dev/null 2>&1 || { echo 'python3 is required on ${ARBUZAS_HOST}' >&2; exit 1; }
    mkdir -p \
      '/srv/arbuzas/train-bot/run' \
      '/srv/arbuzas/train-bot/state' \
      '/srv/arbuzas/train-bot/data/schedules' \
      '/srv/arbuzas/train-bot/data/public-bundles' \
      '/srv/arbuzas/satiksme-bot/run' \
      '/srv/arbuzas/satiksme-bot/state' \
      '/srv/arbuzas/satiksme-bot/data/catalog/source' \
      '/srv/arbuzas/satiksme-bot/data/catalog/generated' \
      '/srv/arbuzas/satiksme-bot/data/public-bundles' \
      '/srv/arbuzas/subscription-bot/run' \
      '/srv/arbuzas/subscription-bot/state' \
      '/srv/arbuzas/ticket-remote/run' \
      '/srv/arbuzas/ticket-remote/state' \
      '/etc/arbuzas/env' \
      '/etc/arbuzas/releases' \
      '/etc/arbuzas/docker-gc' \
      '/etc/arbuzas/cloudflared' \
      '/etc/arbuzas/secrets'
    if [[ ! -f '${DOCKER_GC_REMOTE_STATE_FILE}' && -r '/srv/arbuzas/docker-gc/state.json' ]]; then
      cp '/srv/arbuzas/docker-gc/state.json' '${DOCKER_GC_REMOTE_STATE_FILE}'
    fi
    touch \
      '/etc/arbuzas/env/train-bot.env' \
      '/etc/arbuzas/env/satiksme-bot.env' \
      '/etc/arbuzas/env/subscription-bot.env' \
      '/etc/arbuzas/env/ticket-remote.env' 2>/dev/null || true
  "
  prepare_remote_ticket_runtime_permissions
}

qbittorrent_deployment_selected() {
  targeted_service_selected qbittorrent || targeted_service_selected qbittorrent_housekeeper
}

stabilize_remote_declared_docker_no_swap_limits() {
  remote_root_command "
    command -v docker >/dev/null 2>&1 || { echo 'docker is required to stabilize declared no-swap limits' >&2; exit 1; }
    command -v systemctl >/dev/null 2>&1 || { echo 'systemctl is required to stabilize declared no-swap limits' >&2; exit 1; }

    while IFS= read -r container_id; do
      [[ -n \"\${container_id}\" ]] || continue
      [[ \"\${container_id}\" =~ ^[0-9a-f]{64}$ ]] || {
        echo \"Docker returned an invalid full container ID: \${container_id}\" >&2
        exit 1
      }
      read -r memory_limit memory_swap_limit < <(
        docker inspect --format '{{.HostConfig.Memory}} {{.HostConfig.MemorySwap}}' \"\${container_id}\"
      )
      [[ \"\${memory_limit}\" =~ ^[1-9][0-9]*$ ]] || continue
      [[ \"\${memory_swap_limit}\" == \"\${memory_limit}\" ]] || continue

      docker update \
        --memory \"\${memory_limit}\" \
        --memory-swap \"\${memory_swap_limit}\" \
        \"\${container_id}\" >/dev/null

      scope_unit=\"docker-\${container_id}.scope\"
      systemctl is-active --quiet \"\${scope_unit}\" || {
        echo \"Missing active Docker systemd scope: \${scope_unit}\" >&2
        exit 1
      }
      systemctl set-property --runtime \"\${scope_unit}\" MemorySwapMax=0 >/dev/null

      systemd_swap_limit=\$(systemctl show --property=MemorySwapMax --value \"\${scope_unit}\")
      [[ \"\${systemd_swap_limit}\" == '0' ]] || {
        echo \"\${scope_unit} retained MemorySwapMax=\${systemd_swap_limit}\" >&2
        exit 1
      }
      control_group=\$(systemctl show --property=ControlGroup --value \"\${scope_unit}\")
      [[ \"\${control_group}\" == /*/\"\${scope_unit}\" ]] || {
        echo \"Unexpected control group for \${scope_unit}: \${control_group}\" >&2
        exit 1
      }
      cgroup_swap_file=\"/sys/fs/cgroup\${control_group}/memory.swap.max\"
      [[ -r \"\${cgroup_swap_file}\" ]] || {
        echo \"Unreadable swap limit for \${scope_unit}: \${cgroup_swap_file}\" >&2
        exit 1
      }
      grep -Fx '0' \"\${cgroup_swap_file}\" >/dev/null || {
        echo \"\${scope_unit} did not retain a zero cgroup swap limit\" >&2
        exit 1
      }
    done < <(docker ps -q --no-trunc)
  "
}

restart_existing_qbittorrent_slice() {
  remote_shell "
    for service_name in qbittorrent qbittorrent_housekeeper; do
      container_id=\$(docker ps -aq \
        --filter 'label=com.docker.compose.project=arbuzas' \
        --filter \"label=com.docker.compose.service=\${service_name}\" \
        | head -n 1)
      if [[ -n \"\${container_id}\" ]]; then
        docker start \"\${container_id}\" >/dev/null
      fi
    done
  " || return $?
  stabilize_remote_declared_docker_no_swap_limits
}

prepare_remote_qbittorrent_runtime() {
  local release_id="${1:-${ARBUZAS_RELEASE_ID}}"
  local force_prepare="${2:-0}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"
  local remote_ids=""
  local qbittorrent_uid=""
  local qbittorrent_gid=""

  if [[ "${force_prepare}" != "1" ]] && ! qbittorrent_deployment_selected; then
    return 0
  fi

  remote_ids="$(remote_inline_shell "printf '%s:%s\\n' \"\$(id -u)\" \"\$(id -g)\"")"
  IFS=: read -r qbittorrent_uid qbittorrent_gid <<< "${remote_ids}"
  [[ "${qbittorrent_uid}" =~ ^[1-9][0-9]*$ ]] || {
    echo "Unable to resolve a positive qBittorrent PUID for ${ARBUZAS_USER} on ${ARBUZAS_HOST}" >&2
    return 1
  }
  [[ "${qbittorrent_gid}" =~ ^[1-9][0-9]*$ ]] || {
    echo "Unable to resolve a positive qBittorrent PGID for ${ARBUZAS_USER} on ${ARBUZAS_HOST}" >&2
    return 1
  }

  # qBittorrent rewrites its preferences while stopping. Stop both members of
  # this isolated slice before reconciling the deployment-owned preferences.
  if ! remote_shell "
    for service_name in qbittorrent_housekeeper qbittorrent; do
      container_id=\$(docker ps -q \
        --filter 'label=com.docker.compose.project=arbuzas' \
        --filter \"label=com.docker.compose.service=\${service_name}\" \
        | head -n 1)
      if [[ -n \"\${container_id}\" ]]; then
        docker stop --time 30 \"\${container_id}\" >/dev/null
      fi
    done
  "; then
    log "qBittorrent stop failed; restarting any previously active slice members"
    restart_existing_qbittorrent_slice || log "Warning: the previous qBittorrent slice could not be restarted automatically"
    return 1
  fi

  if ! remote_root_command "
    storage_installer='${remote_release_dir}/infra/arbuzas/qbittorrent/install-storage.sh'
    config_reconciler='${remote_release_dir}/infra/arbuzas/qbittorrent/reconcile-config.py'
    release_env='${remote_release_dir}/release.env'
    [[ -f \"\${storage_installer}\" ]] || { echo \"missing qBittorrent storage installer: \${storage_installer}\" >&2; exit 1; }
    [[ -f \"\${config_reconciler}\" ]] || { echo \"missing qBittorrent config reconciler: \${config_reconciler}\" >&2; exit 1; }
    [[ -f \"\${release_env}\" && ! -L \"\${release_env}\" ]] || { echo \"invalid release env: \${release_env}\" >&2; exit 1; }

    bash \"\${storage_installer}\" install --uid '${qbittorrent_uid}' --gid '${qbittorrent_gid}'
    python3 \"\${config_reconciler}\" \
      --path '${QBITTORRENT_REMOTE_CONFIG_FILE}' \
      --uid '${qbittorrent_uid}' \
      --gid '${qbittorrent_gid}'
    python3 \"\${config_reconciler}\" \
      --path '${QBITTORRENT_REMOTE_CONFIG_FILE}' \
      --uid '${qbittorrent_uid}' \
      --gid '${qbittorrent_gid}' \
      --check

    python3 - \"\${release_env}\" '${qbittorrent_uid}' '${qbittorrent_gid}' <<'PY'
import os
from pathlib import Path
import stat
import sys
import tempfile

path = Path(sys.argv[1])
uid, gid = sys.argv[2:4]
mode = path.lstat().st_mode
if not stat.S_ISREG(mode) or stat.S_ISLNK(mode):
    raise SystemExit(f'refusing non-regular release env: {path}')

managed = {
    'ARBUZAS_QBITTORRENT_PUID': uid,
    'ARBUZAS_QBITTORRENT_PGID': gid,
    'ARBUZAS_JELLYFIN_PUID': uid,
    'ARBUZAS_JELLYFIN_PGID': gid,
}
lines = []
for line in path.read_text(encoding='utf-8').splitlines():
    key = line.split('=', 1)[0]
    if key not in managed:
        lines.append(line)
lines.extend(f'{key}={value}' for key, value in managed.items())

fd, tmp_name = tempfile.mkstemp(prefix=f'.{path.name}.', dir=path.parent, text=True)
tmp_path = Path(tmp_name)
try:
    with os.fdopen(fd, 'w', encoding='utf-8') as handle:
        handle.write('\\n'.join(lines) + '\\n')
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(tmp_path, stat.S_IMODE(mode))
    os.chown(tmp_path, path.stat().st_uid, path.stat().st_gid)
    os.replace(tmp_path, path)
finally:
    try:
        tmp_path.unlink()
    except FileNotFoundError:
        pass
PY
  "; then
    log "qBittorrent preparation failed; restarting the previously active slice"
    restart_existing_qbittorrent_slice || log "Warning: the previous qBittorrent slice could not be restarted automatically"
    return 1
  fi
}

jellyfin_deployment_selected() {
  targeted_service_selected jellyfin
}

jellyfin_only_deployment_selected() {
  (( TARGETED_MODE == 1 )) || return 1
  (( ${#COMPOSE_TARGET_SERVICES[@]} == 1 )) || return 1
  [[ "${COMPOSE_TARGET_SERVICES[0]}" == jellyfin ]]
}

prepare_remote_jellyfin_runtime() {
  local release_id="${1:-${ARBUZAS_RELEASE_ID}}"
  local force_prepare="${2:-0}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"
  local remote_ids=""
  local jellyfin_uid=""
  local jellyfin_gid=""
  local prepare_output=""

  if [[ "${force_prepare}" != "1" ]] && ! jellyfin_deployment_selected; then
    return 0
  fi

  remote_ids="$(remote_inline_shell "printf '%s:%s\\n' \"\$(id -u)\" \"\$(id -g)\"")"
  IFS=: read -r jellyfin_uid jellyfin_gid <<< "${remote_ids}"
  [[ "${jellyfin_uid}" =~ ^[1-9][0-9]*$ ]] || {
    echo "Unable to resolve a positive Jellyfin PUID for ${ARBUZAS_USER} on ${ARBUZAS_HOST}" >&2
    return 1
  }
  [[ "${jellyfin_gid}" =~ ^[1-9][0-9]*$ ]] || {
    echo "Unable to resolve a positive Jellyfin PGID for ${ARBUZAS_USER} on ${ARBUZAS_HOST}" >&2
    return 1
  }

  # This intentionally leaves the running qBittorrent containers and their
  # systemd cgroups alone. The media-only storage action verifies the live
  # capped filesystem and installs .ignore without reloading systemd.
  if ! prepare_output="$(remote_root_command "
    storage_installer='${remote_release_dir}/infra/arbuzas/qbittorrent/install-storage.sh'
    release_env='${remote_release_dir}/release.env'
    [[ -f \"\${storage_installer}\" && ! -L \"\${storage_installer}\" ]] || { echo \"missing qBittorrent storage installer: \${storage_installer}\" >&2; exit 1; }
    [[ -f \"\${release_env}\" && ! -L \"\${release_env}\" ]] || { echo \"invalid release env: \${release_env}\" >&2; exit 1; }

    bash \"\${storage_installer}\" prepare-media --uid '${jellyfin_uid}' --gid '${jellyfin_gid}'

    secret_dir=\$(dirname '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}')
    secret_created=0
    if [[ -e \"\${secret_dir}\" || -L \"\${secret_dir}\" ]]; then
      [[ -d \"\${secret_dir}\" && ! -L \"\${secret_dir}\" ]] || {
        echo \"refusing unsafe Jellyfin secret directory: \${secret_dir}\" >&2
        exit 1
      }
    else
      install -d -m 0700 -o root -g root \"\${secret_dir}\"
    fi
    chown root:root \"\${secret_dir}\"
    chmod 0700 \"\${secret_dir}\"
    if [[ -e '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' || -L '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' ]]; then
      [[ -f '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' && ! -L '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' ]] || {
        echo 'refusing unsafe Jellyfin admin password file' >&2
        exit 1
      }
    else
      python3 - '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' <<'PY'
import os
import secrets
import sys

path = sys.argv[1]
flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
if hasattr(os, 'O_NOFOLLOW'):
    flags |= os.O_NOFOLLOW
fd = os.open(path, flags, 0o600)
with os.fdopen(fd, 'w', encoding='utf-8') as handle:
    handle.write(secrets.token_urlsafe(48) + '\\n')
    handle.flush()
    os.fsync(handle.fileno())
PY
      secret_created=1
    fi
    [[ \"\$(stat -c '%u:%g:%a' '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}')\" == '0:0:600' ]] || {
      echo 'Jellyfin admin password file must be owned by root with mode 0600' >&2
      exit 1
    }
    python3 - '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' <<'PY'
from pathlib import Path
import sys

value = Path(sys.argv[1]).read_text(encoding='utf-8').strip()
if len(value) < 32 or any(char.isspace() for char in value):
    raise SystemExit('Jellyfin admin password file is empty, short, or malformed')
PY

    for path in \
      '${JELLYFIN_REMOTE_ROOT}' \
      '${JELLYFIN_REMOTE_ROOT}/config' \
      '${JELLYFIN_REMOTE_ROOT}/cache' \
      '${JELLYFIN_REMOTE_ROOT}/tmp' \
      '${JELLYFIN_REMOTE_ROOT}/transcodes'; do
      if [[ -e \"\${path}\" || -L \"\${path}\" ]]; then
        [[ -d \"\${path}\" && ! -L \"\${path}\" ]] || {
          echo \"refusing unsafe Jellyfin runtime path: \${path}\" >&2
          exit 1
        }
      else
        install -d -m 0750 \"\${path}\"
      fi
      chown '${jellyfin_uid}:${jellyfin_gid}' \"\${path}\"
      chmod 0750 \"\${path}\"
    done

    python3 - \"\${release_env}\" '${jellyfin_uid}' '${jellyfin_gid}' <<'PY'
import os
from pathlib import Path
import stat
import sys
import tempfile

path = Path(sys.argv[1])
uid, gid = sys.argv[2:4]
mode = path.lstat().st_mode
if not stat.S_ISREG(mode) or stat.S_ISLNK(mode):
    raise SystemExit(f'refusing non-regular release env: {path}')

managed = {
    'ARBUZAS_QBITTORRENT_PUID': uid,
    'ARBUZAS_QBITTORRENT_PGID': gid,
    'ARBUZAS_JELLYFIN_PUID': uid,
    'ARBUZAS_JELLYFIN_PGID': gid,
}
lines = []
for line in path.read_text(encoding='utf-8').splitlines():
    key = line.split('=', 1)[0]
    if key not in managed:
        lines.append(line)
lines.extend(f'{key}={value}' for key, value in managed.items())

fd, tmp_name = tempfile.mkstemp(prefix=f'.{path.name}.', dir=path.parent, text=True)
tmp_path = Path(tmp_name)
try:
    with os.fdopen(fd, 'w', encoding='utf-8') as handle:
        handle.write('\\n'.join(lines) + '\\n')
        handle.flush()
        os.fsync(handle.fileno())
    os.chmod(tmp_path, stat.S_IMODE(mode))
    os.chown(tmp_path, path.stat().st_uid, path.stat().st_gid)
    os.replace(tmp_path, path)
finally:
    try:
        tmp_path.unlink()
    except FileNotFoundError:
        pass
PY
    printf 'ARBUZAS_JELLYFIN_SECRET_CREATED=%s\\n' \"\${secret_created}\"
  ")"; then
    return 1
  fi

  if grep -Fx 'ARBUZAS_JELLYFIN_SECRET_CREATED=1' <<< "${prepare_output}" >/dev/null; then
    JELLYFIN_SECRET_CREATED=1
    log "Jellyfin admin password created; refreshing the local-first host mirror"
    run_host_mirror pull
  fi
}

bootstrap_remote_jellyfin() {
  local release_id="${1:-${ARBUZAS_RELEASE_ID}}"
  local force_bootstrap="${2:-0}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"

  if [[ "${force_bootstrap}" != "1" ]] && ! jellyfin_deployment_selected; then
    return 0
  fi

  remote_root_command "
    bootstrap='${remote_release_dir}/infra/arbuzas/jellyfin/bootstrap.py'
    [[ -f \"\${bootstrap}\" && ! -L \"\${bootstrap}\" ]] || { echo \"missing Jellyfin bootstrap helper: \${bootstrap}\" >&2; exit 1; }
    deadline=\$((SECONDS + 180))
    until curl -fsS --connect-timeout 2 --max-time 5 \
      'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}/health' | grep -Fx Healthy >/dev/null; do
      if (( SECONDS >= deadline )); then
        echo 'Jellyfin loopback health did not become ready before bootstrap' >&2
        exit 1
      fi
      sleep 5
    done
    python3 \"\${bootstrap}\" bootstrap \
      --url 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}' \
      --admin-password-file '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}'
    python3 \"\${bootstrap}\" check \
      --url 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}' \
      --admin-password-file '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}'
  "
}

publish_remote_qbittorrent_tailscale() {
  local preflight=""
  local existing_state=""
  local before_status_base64=""
  local serve_was_absent=0
  if ! qbittorrent_deployment_selected; then
    return 0
  fi

  if ! preflight="$(remote_inline_shell "
    tailscale serve status --json | python3 -c 'import base64,json,sys; payload=json.load(sys.stdin); port=\"${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}\"; hostname=\"${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}\"; target=\"http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}\"; tcp=payload.get(\"TCP\", {}).get(port); web={key:value for key,value in payload.get(\"Web\", {}).items() if key.rsplit(\":\",1)[-1] == port}; handler=web.get(f\"{hostname}:{port}\", {}).get(\"Handlers\", {}).get(\"/\", {}); state=\"absent\" if tcp is None and not web else \"exact\" if tcp == {\"HTTPS\": True} and handler.get(\"Proxy\") == target and len(web) == 1 else \"conflict\"; encoded=base64.b64encode(json.dumps(payload,sort_keys=True,separators=(\",\",\":\")).encode()).decode(); print(state+\"|\"+encoded)'
  " | tail -n 1 | tr -d '\r\n[:space:]')"; then
    return 1
  fi
  IFS='|' read -r existing_state before_status_base64 <<< "${preflight}"
  [[ "${before_status_base64}" =~ ^[A-Za-z0-9+/=]+$ ]] || {
    echo "Unable to snapshot existing Tailscale Serve state" >&2
    return 1
  }
  case "${existing_state}" in
    absent)
      serve_was_absent=1
      ;;
    exact)
      ;;
    *)
      echo "Refusing to overwrite existing Tailscale Serve :${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT} state: ${existing_state:-unknown}" >&2
      return 1
      ;;
  esac

  if ! remote_root_command "
    command -v tailscale >/dev/null 2>&1 || { echo 'tailscale is required for the private qBittorrent route' >&2; exit 1; }
    before=\$(mktemp)
    current=\$(mktemp)
    trap 'rm -f \"\${before}\" \"\${current}\"' EXIT
    printf '%s' '${before_status_base64}' | base64 -d > \"\${before}\"
    tailscale serve status --json > \"\${current}\"

    python3 - \"\${before}\" \"\${current}\" <<'PY'
import json
import sys

before = json.load(open(sys.argv[1], encoding='utf-8'))
payload = json.load(open(sys.argv[2], encoding='utf-8'))

def port_view(document, port):
    return {
        'tcp': document.get('TCP', {}).get(str(port)),
        'web': {
            key: value
            for key, value in document.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] == str(port)
        },
    }

if port_view(before, 10000) != port_view(payload, 10000):
    raise SystemExit('Tailscale Serve :10000 changed during qBittorrent publish preflight')
tcp = payload.get('TCP', {}).get('10000')
web = {key: value for key, value in payload.get('Web', {}).items() if key.rsplit(':', 1)[-1] == '10000'}
if tcp is None and not web:
    raise SystemExit('refusing to change Tailscale Serve: the existing HTTPS :10000 service is missing')

port = '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'
hostname = '${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}'
target = 'http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}'
if port_view(before, port) != port_view(payload, port):
    raise SystemExit(f'Tailscale Serve :{port} changed during qBittorrent publish preflight')
existing_tcp = payload.get('TCP', {}).get(port)
existing_web = {
    key: value
    for key, value in payload.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}

if existing_tcp is not None or existing_web:
    handler = existing_web.get(f'{hostname}:{port}', {}).get('Handlers', {}).get('/', {})
    if existing_tcp != {'HTTPS': True} or handler.get('Proxy') != target or len(existing_web) != 1:
        raise SystemExit(f'refusing to overwrite existing Tailscale Serve :{port}: tcp={existing_tcp!r} web={existing_web!r}')
PY

    panel_code=\$(curl -skS --connect-timeout 3 --max-time 8 -o /dev/null -w '%{http_code}' \
      'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:10000/' || true)
    [[ "\${panel_code}" != '000' ]] || { echo 'existing Tailscale Serve :10000 did not answer before qBittorrent publish' >&2; exit 1; }
    tailscale serve --bg --yes \
      --https '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}' \
      'http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}'
  "; then
    if (( serve_was_absent == 1 )) && remote_inline_shell "
      tailscale serve status --json | python3 -c 'import json,sys; payload=json.load(sys.stdin); port=\"${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}\"; hostname=\"${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}\"; target=\"http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}\"; tcp=payload.get(\"TCP\", {}).get(port); web={key:value for key,value in payload.get(\"Web\", {}).items() if key.rsplit(\":\",1)[-1] == port}; handler=web.get(f\"{hostname}:{port}\", {}).get(\"Handlers\", {}).get(\"/\", {}); assert tcp == {\"HTTPS\": True} and handler.get(\"Proxy\") == target and len(web) == 1'
    " >/dev/null 2>&1; then
      QBITTORRENT_SERVE_ADDED=1
    fi
    return 1
  fi

  # Mark ownership immediately after Serve succeeds, before post-checks.
  QBITTORRENT_SERVE_ADDED="${serve_was_absent}"

  if ! remote_root_command "
    before=\$(mktemp)
    after=\$(mktemp)
    trap 'rm -f \"\${before}\" \"\${after}\"' EXIT
    printf '%s' '${before_status_base64}' | base64 -d > \"\${before}\"
    tailscale serve status --json > \"\${after}\"

    python3 - \"\${before}\" \"\${after}\" <<'PY'
import json
import sys

before = json.load(open(sys.argv[1], encoding='utf-8'))
after = json.load(open(sys.argv[2], encoding='utf-8'))

def port_view(payload, port):
    return {
        'tcp': payload.get('TCP', {}).get(str(port)),
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] == str(port)
        },
    }

if port_view(before, 10000) != port_view(after, 10000):
    raise SystemExit('Tailscale Serve :10000 changed while adding qBittorrent')

port = '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'
hostname = '${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}'
target = 'http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}'
if after.get('TCP', {}).get(port, {}).get('HTTPS') is not True:
    raise SystemExit(f'Tailscale Serve HTTPS :{port} is not enabled')
handler = after.get('Web', {}).get(f'{hostname}:{port}', {}).get('Handlers', {}).get('/', {})
proxy = handler.get('Proxy')
if proxy != target:
    raise SystemExit(f'Tailscale Serve {hostname}:{port} points to {proxy!r}, expected {target!r}')
PY
    panel_code=\$(curl -skS --connect-timeout 3 --max-time 8 -o /dev/null -w '%{http_code}' \
      'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:10000/' || true)
    [[ "\${panel_code}" != '000' ]] || { echo 'existing Tailscale Serve :10000 stopped answering after qBittorrent publish' >&2; exit 1; }
  "; then
    return 1
  fi
}

publish_remote_jellyfin_tailscale() {
  local preflight=""
  local existing_state=""
  local before_status_base64=""
  local serve_was_absent=0

  if ! jellyfin_deployment_selected; then
    return 0
  fi

  if ! preflight="$(remote_inline_shell "
    command -v tailscale >/dev/null 2>&1 || { echo 'tailscale is required for the private Jellyfin route' >&2; exit 1; }
    tailscale serve status --json | python3 -c 'import base64,json,sys; payload=json.load(sys.stdin); port=\"${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}\"; hostname=\"${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}\"; target=\"http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}\"; tcp=payload.get(\"TCP\", {}).get(port); web={key:value for key,value in payload.get(\"Web\", {}).items() if key.rsplit(\":\",1)[-1] == port}; handler=web.get(f\"{hostname}:{port}\", {}).get(\"Handlers\", {}).get(\"/\", {}); state=\"absent\" if tcp is None and not web else \"exact\" if tcp == {\"HTTPS\": True} and handler.get(\"Proxy\") == target and len(web) == 1 else \"conflict\"; encoded=base64.b64encode(json.dumps(payload,sort_keys=True,separators=(\",\",\":\")).encode()).decode(); print(state+\"|\"+encoded)'
  " | tail -n 1 | tr -d '\r\n[:space:]')"; then
    return 1
  fi
  IFS='|' read -r existing_state before_status_base64 <<< "${preflight}"
  [[ "${before_status_base64}" =~ ^[A-Za-z0-9+/=]+$ ]] || {
    echo "Unable to snapshot existing Tailscale Serve state before Jellyfin publish" >&2
    return 1
  }
  case "${existing_state}" in
    absent)
      serve_was_absent=1
      ;;
    exact)
      ;;
    *)
      echo "Refusing to overwrite existing Tailscale Serve :${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT} state: ${existing_state:-unknown}" >&2
      return 1
      ;;
  esac

  if ! remote_root_command "
    command -v tailscale >/dev/null 2>&1 || { echo 'tailscale is required for the private Jellyfin route' >&2; exit 1; }
    before=\$(mktemp)
    current=\$(mktemp)
    trap 'rm -f \"\${before}\" \"\${current}\"' EXIT
    printf '%s' '${before_status_base64}' | base64 -d > \"\${before}\"
    tailscale serve status --json > \"\${current}\"
    python3 - \"\${before}\" \"\${current}\" <<'PY'
import json
import sys

before = json.load(open(sys.argv[1], encoding='utf-8'))
current = json.load(open(sys.argv[2], encoding='utf-8'))
port = '${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}'
hostname = '${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}'
target = 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}'

def target_view(payload):
    return {
        'tcp': payload.get('TCP', {}).get(port),
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] == port
        },
    }

def non_target_view(payload):
    return {
        'other': {key: value for key, value in payload.items() if key not in ('TCP', 'Web')},
        'tcp': {key: value for key, value in payload.get('TCP', {}).items() if key != port},
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] != port
        },
    }

if non_target_view(before) != non_target_view(current):
    raise SystemExit('refusing Jellyfin publish because a non-target Tailscale Serve route changed during preflight')
if target_view(before) != target_view(current):
    raise SystemExit(f'refusing Jellyfin publish because Tailscale Serve :{port} changed during preflight')

view = target_view(current)
handler = view['web'].get(f'{hostname}:{port}', {}).get('Handlers', {}).get('/', {})
if view['tcp'] is not None or view['web']:
    if view['tcp'] != {'HTTPS': True} or handler.get('Proxy') != target or len(view['web']) != 1:
        raise SystemExit(f'refusing to overwrite Tailscale Serve :{port}: {view!r}')
PY
    test \"\$(curl -fsS --connect-timeout 3 --max-time 8 \
      'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}/api/v2/app/version')\" = v5.2.3
    tailscale serve --bg --yes \
      --https '${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}' \
      'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}'
  "; then
    if (( serve_was_absent == 1 )) && remote_inline_shell "
      tailscale serve status --json | python3 -c 'import json,sys; payload=json.load(sys.stdin); port=\"${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}\"; hostname=\"${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}\"; target=\"http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}\"; tcp=payload.get(\"TCP\", {}).get(port); web={key:value for key,value in payload.get(\"Web\", {}).items() if key.rsplit(\":\",1)[-1] == port}; handler=web.get(f\"{hostname}:{port}\", {}).get(\"Handlers\", {}).get(\"/\", {}); assert tcp == {\"HTTPS\": True} and handler.get(\"Proxy\") == target and len(web) == 1'
    " >/dev/null 2>&1; then
      JELLYFIN_SERVE_ADDED=1
    fi
    return 1
  fi

  # Record route ownership as soon as Serve succeeds so an ensuing failed
  # post-check can remove only what this deployment added.
  JELLYFIN_SERVE_ADDED="${serve_was_absent}"

  remote_root_command "
    before=\$(mktemp)
    after=\$(mktemp)
    trap 'rm -f \"\${before}\" \"\${after}\"' EXIT
    printf '%s' '${before_status_base64}' | base64 -d > \"\${before}\"
    tailscale serve status --json > \"\${after}\"
    python3 - \"\${before}\" \"\${after}\" <<'PY'
import json
import sys

before = json.load(open(sys.argv[1], encoding='utf-8'))
after = json.load(open(sys.argv[2], encoding='utf-8'))
port = '${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}'
hostname = '${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}'
target = 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}'

def non_target_view(payload):
    return {
        'other': {key: value for key, value in payload.items() if key not in ('TCP', 'Web')},
        'tcp': {key: value for key, value in payload.get('TCP', {}).items() if key != port},
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] != port
        },
    }

if non_target_view(before) != non_target_view(after):
    raise SystemExit('a non-target Tailscale Serve route changed while adding Jellyfin')
tcp = after.get('TCP', {}).get(port)
web = {
    key: value
    for key, value in after.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
handler = web.get(f'{hostname}:{port}', {}).get('Handlers', {}).get('/', {})
if tcp != {'HTTPS': True} or handler.get('Proxy') != target or len(web) != 1:
    raise SystemExit(f'Jellyfin Tailscale Serve :{port} is not the exact managed HTTPS route')
PY
    test \"\$(curl -fsS --connect-timeout 3 --max-time 8 \
      'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}/api/v2/app/version')\" = v5.2.3
    curl -fsS --connect-timeout 3 --max-time 8 \
      'https://${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}:${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}/health' \
      | grep -Fx Healthy >/dev/null
  "
}

previous_release_has_qbittorrent() {
  local release_id="$1"
  remote_inline_shell "
    compose_file='${REMOTE_RELEASES_ROOT}/${release_id}/infra/arbuzas/docker/compose.yml'
    [[ -f \"\${compose_file}\" ]] || exit 1
    grep -Eq '^  qbittorrent:' \"\${compose_file}\"
  " >/dev/null 2>&1
}

rollback_release_before_qbittorrent() {
  local release_id="$1"
  local cleanup_mode="${2:-owned-only}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"
  local remove_managed_route=0

  case "${cleanup_mode}" in
    owned-only)
      remove_managed_route="${QBITTORRENT_SERVE_ADDED}"
      ;;
    force-managed)
      remove_managed_route=1
      ;;
    *)
      echo "unknown qBittorrent rollback cleanup mode: ${cleanup_mode}" >&2
      return 2
      ;;
  esac

  # Recover the prior application release before changing ingress. A route
  # cleanup failure must never prevent the known-good Compose stack from
  # coming back.
  remote_shell "
    [[ -f '${remote_release_dir}/release.env' ]] || { echo 'missing rollback release: ${remote_release_dir}' >&2; exit 1; }
    [[ -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' ]] || { echo 'missing rollback compose file: ${remote_release_dir}' >&2; exit 1; }
    sudo -n ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    compose_args=(docker compose --project-name arbuzas \
      --env-file '${REMOTE_CURRENT_LINK}/release.env' \
      -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml')
    retired_service_args=()
    for retired_service in portainer chatgpt_broker chatgpt_bot; do
      if \"\${compose_args[@]}\" config --services | grep -Fxq \"\${retired_service}\"; then
        retired_service_args+=(--scale \"\${retired_service}=0\")
      fi
    done
    \"\${compose_args[@]}\" up -d --remove-orphans \"\${retired_service_args[@]}\"
  " || return 1
  stabilize_remote_declared_docker_no_swap_limits || return 1

  # Automatic recovery removes only a route added by this invocation. An
  # explicit manual rollback forces removal of the exact managed route. Both
  # modes tolerate absence; automatic recovery preserves all preexisting
  # state, while manual cleanup refuses a conflicting route.
  remote_root_command "
    command -v tailscale >/dev/null 2>&1 || { echo 'tailscale is required to clean up the qBittorrent route' >&2; exit 1; }
    before=\$(mktemp)
    after=\$(mktemp)
    trap 'rm -f \"\${before}\" \"\${after}\"' EXIT
    tailscale serve status --json > \"\${before}\"
    route_state=\$(python3 - \"\${before}\" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1], encoding='utf-8'))
port = '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'
hostname = '${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}'
target = 'http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}'
tcp = payload.get('TCP', {}).get(port)
web = {
    key: value
    for key, value in payload.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
handler = web.get(f'{hostname}:{port}', {}).get('Handlers', {}).get('/', {})
if tcp is None and not web:
    print('absent')
elif tcp == {'HTTPS': True} and handler.get('Proxy') == target and len(web) == 1:
    print('exact')
else:
    print('conflict')
PY
    )
    if [[ '${remove_managed_route}' == '1' ]]; then
      case \"\${route_state}\" in
        absent)
          ;;
        exact)
          tailscale serve --yes --https='${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}' off
          ;;
        *)
          echo 'refusing to remove conflicting Tailscale Serve :${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT} during rollback' >&2
          exit 1
          ;;
      esac
    fi
    tailscale serve status --json > \"\${after}\"
    python3 - \"\${before}\" \"\${after}\" <<'PY'
import json
import sys

before = json.load(open(sys.argv[1], encoding='utf-8'))
after = json.load(open(sys.argv[2], encoding='utf-8'))

def port_view(payload, port):
    return {
        'tcp': payload.get('TCP', {}).get(str(port)),
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] == str(port)
        },
    }

if port_view(before, 10000) != port_view(after, 10000):
    raise SystemExit('Tailscale Serve :10000 changed while removing qBittorrent')
port = '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'
if '${remove_managed_route}' == '1':
    if after.get('TCP', {}).get(port) is not None:
        raise SystemExit(f'Tailscale Serve TCP :{port} remained after qBittorrent rollback')
    if any(key.rsplit(':', 1)[-1] == port for key in after.get('Web', {})):
        raise SystemExit(f'Tailscale Serve Web :{port} remained after qBittorrent rollback')
elif port_view(before, port) != port_view(after, port):
    raise SystemExit(f'Tailscale Serve :{port} changed during ownership-preserving rollback')
PY
    panel_code=\$(curl -skS --connect-timeout 3 --max-time 8 -o /dev/null -w '%{http_code}' \
      'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:10000/' || true)
    [[ \"\${panel_code}\" != '000' ]] || { echo 'Tailscale Serve :10000 did not survive qBittorrent rollback' >&2; exit 1; }
  " || return 1
}

validate_release_before_qbittorrent_recovery() {
  local release_id="$1"
  local cleanup_mode="${2:-owned-only}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"
  local require_route_absent=0

  if [[ "${cleanup_mode}" == "force-managed" || "${QBITTORRENT_SERVE_ADDED}" == "1" ]]; then
    require_route_absent=1
  fi

  validate_remote_host_probe "${remote_release_dir}" "pre-qBittorrent release recovered" \
    "test \"\$(readlink -f '${REMOTE_CURRENT_LINK}')\" = \"\$(readlink -f '${remote_release_dir}')\"
      cd '${remote_release_dir}'
      expected=\$(docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml config --services | grep -Ev '^(portainer|chatgpt_broker|chatgpt_bot)$')
      deadline=\$((SECONDS + 180))
      while (( SECONDS < deadline )); do
        running=\$(docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml ps --services --status running)
        missing=0
        while IFS= read -r service_name; do
          [[ -n \"\${service_name}\" ]] || continue
          grep -Fx \"\${service_name}\" <<< \"\${running}\" >/dev/null || missing=1
        done <<< \"\${expected}\"
        (( missing == 0 )) && break
        sleep 5
      done
      (( missing == 0 ))
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=qbittorrent')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=qbittorrent_housekeeper')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=portainer')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=chatgpt_broker')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=chatgpt_bot')\"
      if [[ '${require_route_absent}' == '1' ]]; then
        tailscale serve status --json | python3 -c 'import json,sys; payload=json.load(sys.stdin); port=\"${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}\"; assert payload.get(\"TCP\", {}).get(port) is None; assert not any(key.rsplit(\":\", 1)[-1] == port for key in payload.get(\"Web\", {}))'
      fi
      panel_code=\$(curl -skS --connect-timeout 3 --max-time 8 -o /dev/null -w '%{http_code}' \
        'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:10000/' || true)
      [[ \"\${panel_code}\" != '000' ]]"
}

previous_release_has_jellyfin() {
  local release_id="$1"
  remote_inline_shell "
    compose_file='${REMOTE_RELEASES_ROOT}/${release_id}/infra/arbuzas/docker/compose.yml'
    [[ -f \"\${compose_file}\" ]] || exit 1
    grep -Eq '^  jellyfin:' \"\${compose_file}\"
  " >/dev/null 2>&1
}

remove_remote_jellyfin_tailscale_route() {
  local cleanup_mode="${1:-owned-only}"
  local remove_managed_route=0

  case "${cleanup_mode}" in
    owned-only)
      remove_managed_route="${JELLYFIN_SERVE_ADDED}"
      ;;
    force-managed)
      remove_managed_route=1
      ;;
    *)
      echo "unknown Jellyfin rollback cleanup mode: ${cleanup_mode}" >&2
      return 2
      ;;
  esac

  remote_root_command "
    command -v tailscale >/dev/null 2>&1 || { echo 'tailscale is required to clean up the Jellyfin route' >&2; exit 1; }
    before=\$(mktemp)
    after=\$(mktemp)
    trap 'rm -f \"\${before}\" \"\${after}\"' EXIT
    tailscale serve status --json > \"\${before}\"
    route_state=\$(python3 - \"\${before}\" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1], encoding='utf-8'))
port = '${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}'
hostname = '${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}'
target = 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}'
tcp = payload.get('TCP', {}).get(port)
web = {
    key: value
    for key, value in payload.get('Web', {}).items()
    if key.rsplit(':', 1)[-1] == port
}
handler = web.get(f'{hostname}:{port}', {}).get('Handlers', {}).get('/', {})
if tcp is None and not web:
    print('absent')
elif tcp == {'HTTPS': True} and handler.get('Proxy') == target and len(web) == 1:
    print('exact')
else:
    print('conflict')
PY
    )
    if [[ '${remove_managed_route}' == '1' ]]; then
      case \"\${route_state}\" in
        absent)
          ;;
        exact)
          tailscale serve --yes --https='${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}' off
          ;;
        *)
          echo 'refusing to remove conflicting Tailscale Serve :${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT} during Jellyfin rollback' >&2
          exit 1
          ;;
      esac
    fi
    tailscale serve status --json > \"\${after}\"
    python3 - \"\${before}\" \"\${after}\" <<'PY'
import json
import sys

before = json.load(open(sys.argv[1], encoding='utf-8'))
after = json.load(open(sys.argv[2], encoding='utf-8'))
port = '${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}'

def target_view(payload):
    return {
        'tcp': payload.get('TCP', {}).get(port),
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] == port
        },
    }

def non_target_view(payload):
    return {
        'other': {key: value for key, value in payload.items() if key not in ('TCP', 'Web')},
        'tcp': {key: value for key, value in payload.get('TCP', {}).items() if key != port},
        'web': {
            key: value
            for key, value in payload.get('Web', {}).items()
            if key.rsplit(':', 1)[-1] != port
        },
    }

if non_target_view(before) != non_target_view(after):
    raise SystemExit('a non-target Tailscale Serve route changed during Jellyfin rollback')
if '${remove_managed_route}' == '1':
    if target_view(after) != {'tcp': None, 'web': {}}:
        raise SystemExit(f'Tailscale Serve :{port} remained after Jellyfin rollback')
elif target_view(before) != target_view(after):
    raise SystemExit(f'Tailscale Serve :{port} changed during ownership-preserving Jellyfin rollback')
PY
    test \"\$(curl -fsS --connect-timeout 3 --max-time 8 \
      'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}/api/v2/app/version')\" = v5.2.3
  "
}

rollback_release_before_jellyfin() {
  local release_id="$1"
  local cleanup_mode="${2:-owned-only}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"

  if jellyfin_only_deployment_selected; then
    # A Jellyfin-only rollback to a pre-Jellyfin release must not recreate or
    # stop unrelated Compose services. Remove only Jellyfin, then restore the
    # release pointer; the already-running services retain their state.
    remote_shell "
      [[ -f '${remote_release_dir}/release.env' ]] || { echo 'missing rollback release: ${remote_release_dir}' >&2; exit 1; }
      [[ -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' ]] || { echo 'missing rollback compose file: ${remote_release_dir}' >&2; exit 1; }
      while IFS= read -r container_id; do
        [[ -n \"\${container_id}\" ]] || continue
        docker rm -f \"\${container_id}\"
      done < <(docker ps -aq \
        --filter 'label=com.docker.compose.project=arbuzas' \
        --filter 'label=com.docker.compose.service=jellyfin')
      sudo -n ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    " || return 1
  else
    # Full or multi-service rollback restores the complete known-good project.
    remote_shell "
      [[ -f '${remote_release_dir}/release.env' ]] || { echo 'missing rollback release: ${remote_release_dir}' >&2; exit 1; }
      [[ -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' ]] || { echo 'missing rollback compose file: ${remote_release_dir}' >&2; exit 1; }
      sudo -n ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
      cd '${REMOTE_CURRENT_LINK}'
      compose_args=(docker compose --project-name arbuzas \
        --env-file '${REMOTE_CURRENT_LINK}/release.env' \
        -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml')
      retired_service_args=()
      for retired_service in portainer chatgpt_broker chatgpt_bot; do
        if \"\${compose_args[@]}\" config --services | grep -Fxq \"\${retired_service}\"; then
          retired_service_args+=(--scale \"\${retired_service}=0\")
        fi
      done
      \"\${compose_args[@]}\" up -d --remove-orphans \"\${retired_service_args[@]}\"
    " || return 1
  fi

  stabilize_remote_declared_docker_no_swap_limits || return 1
  remove_remote_jellyfin_tailscale_route "${cleanup_mode}"
}

validate_release_before_jellyfin_recovery() {
  local release_id="$1"
  local cleanup_mode="${2:-owned-only}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${release_id}"
  local require_route_absent=0

  if [[ "${cleanup_mode}" == "force-managed" || "${JELLYFIN_SERVE_ADDED}" == "1" ]]; then
    require_route_absent=1
  fi

  validate_remote_host_probe "${remote_release_dir}" "pre-Jellyfin release recovered" \
    "test \"\$(readlink -f '${REMOTE_CURRENT_LINK}')\" = \"\$(readlink -f '${remote_release_dir}')\"
      cd '${remote_release_dir}'
      expected=\$(docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml config --services | grep -Ev '^(portainer|chatgpt_broker|chatgpt_bot)$')
      deadline=\$((SECONDS + 180))
      while (( SECONDS < deadline )); do
        running=\$(docker compose --project-name arbuzas --env-file release.env -f infra/arbuzas/docker/compose.yml ps --services --status running)
        missing=0
        while IFS= read -r service_name; do
          [[ -n \"\${service_name}\" ]] || continue
          grep -Fx \"\${service_name}\" <<< \"\${running}\" >/dev/null || missing=1
        done <<< \"\${expected}\"
        (( missing == 0 )) && break
        sleep 5
      done
      (( missing == 0 ))
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=jellyfin')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=portainer')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=chatgpt_broker')\"
      test -z \"\$(docker ps -aq --filter 'label=com.docker.compose.project=arbuzas' --filter 'label=com.docker.compose.service=chatgpt_bot')\"
      if [[ '${require_route_absent}' == '1' ]]; then
        tailscale serve status --json | python3 -c 'import json,sys; payload=json.load(sys.stdin); port=\"${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}\"; assert payload.get(\"TCP\", {}).get(port) is None; assert not any(key.rsplit(\":\", 1)[-1] == port for key in payload.get(\"Web\", {}))'
      fi
      test \"\$(curl -fsS --connect-timeout 3 --max-time 8 \
        'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}/api/v2/app/version')\" = v5.2.3" \
    qbittorrent qbittorrent_housekeeper
}

harden_remote_release_env_permissions() {
  remote_shell "
    if [[ -d '${REMOTE_RELEASES_ROOT}' ]]; then
      remote_owner=\"\$(id -u):\$(id -g)\"
      sudo -n find '${REMOTE_RELEASES_ROOT}' \
        -mindepth 2 -maxdepth 2 -type f -name release.env \
        -exec chown \"\${remote_owner}\" {} + \
        -exec chmod 0600 {} +
    fi
  "
}

copy_release_to_remote() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  local remote_tmp_dir="${remote_release_dir}.uploading.$$"
  local remote_tarball="/tmp/arbuzas-${ARBUZAS_RELEASE_ID}.$$.tar"
  local local_tarball=""

  local_tarball="$(mktemp "${TMPDIR:-/tmp}/arbuzas-${ARBUZAS_RELEASE_ID}.XXXXXX.tar")"
  trap "rm -f '${local_tarball}'; trap - RETURN" RETURN

  log "Packing release bundle ${ARBUZAS_RELEASE_ID}"
  (
    cd "${ARBUZAS_RELEASE_DIR}"
    COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -cf "${local_tarball}" .
  )

  harden_remote_release_env_permissions || return $?
  log "Uploading release bundle to ${ARBUZAS_HOST}:${remote_tarball}"
  upload_remote_file "${local_tarball}" "${remote_tarball}"

  remote_shell "
    rm -rf '${remote_tmp_dir}'
    sudo -n mkdir -p '${remote_tmp_dir}'
    sudo -n tar -C '${remote_tmp_dir}' -xf '${remote_tarball}'
    sudo -n rm -f '${remote_tarball}'
  "

  remote_shell "
    [[ -f '${remote_tmp_dir}/release.env' ]] || { echo 'incomplete upload: missing release.env in ${remote_tmp_dir}' >&2; exit 1; }
    sudo -n chown \"\$(id -u):\$(id -g)\" '${remote_tmp_dir}/release.env'
    sudo -n chmod 0600 '${remote_tmp_dir}/release.env'
    sudo -n rm -rf '${remote_release_dir}'
    sudo -n mv '${remote_tmp_dir}' '${remote_release_dir}'
  "
}

copy_fast_release_overlay_to_remote() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  local remote_tmp_dir="${remote_release_dir}.uploading.$$"
  local remote_tarball="/tmp/arbuzas-${ARBUZAS_RELEASE_ID}.$$.overlay.tar"
  local local_tarball=""
  local overlay_path=""
  local overlay_path_args=""

  for overlay_path in ${FAST_RELEASE_OVERLAY_PATHS[@]+"${FAST_RELEASE_OVERLAY_PATHS[@]}"}; do
    overlay_path_args+=" $(shell_quote "${overlay_path}")"
  done
  if [[ -z "${overlay_path_args}" ]]; then
    echo "fast release overlay has no selected paths" >&2
    return 2
  fi

  local_tarball="$(mktemp "${TMPDIR:-/tmp}/arbuzas-${ARBUZAS_RELEASE_ID}.XXXXXX.overlay.tar")"
  trap "rm -f '${local_tarball}'; trap - RETURN" RETURN

  log "Packing selected-service release overlay ${ARBUZAS_RELEASE_ID}"
  (
    cd "${ARBUZAS_RELEASE_DIR}"
    COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -cf "${local_tarball}" .
  )

  harden_remote_release_env_permissions || return $?
  log "Uploading selected-service release overlay to ${ARBUZAS_HOST}:${remote_tarball}"
  upload_remote_file "${local_tarball}" "${remote_tarball}"

  remote_shell "
    current_target=\$(readlink -f '${REMOTE_CURRENT_LINK}' 2>/dev/null || true)
    [[ -n \"\${current_target}\" && -f \"\${current_target}/release.env\" ]] || {
      echo 'fast profile requires a complete active release to seed the overlay' >&2
      exit 1
    }
    sudo -n rm -rf '${remote_tmp_dir}'
    sudo -n mkdir -p '${remote_tmp_dir}'
    sudo -n cp -al \"\${current_target}/.\" '${remote_tmp_dir}/'
    for overlay_path in${overlay_path_args}; do
      sudo -n rm -rf '${remote_tmp_dir}/'\"\${overlay_path}\"
    done
    if [[ -d '${remote_tmp_dir}/workloads/chatgpt-broker' ]]; then
      sudo -n find '${remote_tmp_dir}/workloads/chatgpt-broker' -depth -delete
    fi
    sudo -n tar -C '${remote_tmp_dir}' -xf '${remote_tarball}'
    sudo -n rm -f '${remote_tarball}'
    [[ -f '${remote_tmp_dir}/release.env' ]] || { echo 'incomplete fast overlay: missing release.env' >&2; exit 1; }
    sudo -n chown \"\$(id -u):\$(id -g)\" '${remote_tmp_dir}/release.env'
    sudo -n chmod 0600 '${remote_tmp_dir}/release.env'
    [[ -f '${remote_tmp_dir}/infra/arbuzas/docker/compose.yml' ]] || { echo 'incomplete fast overlay: missing compose.yml' >&2; exit 1; }
    [[ -f '${remote_tmp_dir}/tools/arbuzas/render_cloudflared_config.py' ]] || { echo 'incomplete fast overlay: missing tunnel renderer' >&2; exit 1; }
    for required_root in workloads/shared-go workloads/train-bot workloads/satiksme-bot workloads/subscription-bot workloads/ticket-remote; do
      [[ -d '${remote_tmp_dir}/'\"\${required_root}\" ]] || {
        echo \"incomplete fast overlay: missing \${required_root}\" >&2
        exit 1
      }
    done
    sudo -n rm -rf '${remote_release_dir}'
    sudo -n mv '${remote_tmp_dir}' '${remote_release_dir}'
  "
}

fast_profile_requires_cloudflared_render() {
  local service_name

  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    case "${service_name}" in
      train_tunnel|satiksme_tunnel|subscription_tunnel|ticket_remote_tunnel)
        return 0
        ;;
    esac
  done
  return 1
}

prepare_deploy_release_payload() {
  if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
    prepare_local_fast_release_overlay
  else
    prepare_local_release_bundle
  fi
}

copy_deploy_release_payload() {
  if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
    copy_fast_release_overlay_to_remote
  else
    copy_release_to_remote
  fi
}

render_deploy_cloudflared_configs() {
  if [[ "${VALIDATION_PROFILE}" == "fast" ]] && ! fast_profile_requires_cloudflared_render; then
    log "Tunnel config rendering skipped: fast profile did not select a tunnel"
    return 0
  fi
  render_remote_cloudflared_configs
}

render_remote_cloudflared_configs() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  remote_shell "
    sudo -n mkdir -p '${remote_release_dir}/generated/cloudflared'
    sudo -n python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/train-bot.json' \
      --hostname '${ARBUZAS_TRAIN_BOT_HOSTNAME}' \
      --upstream 'http://train_bot:${ARBUZAS_TRAIN_BOT_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/train-bot.yml'
    sudo -n python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/satiksme-bot.json' \
      --hostname '${ARBUZAS_SATIKSME_BOT_HOSTNAME}' \
      --upstream 'http://satiksme_bot:${ARBUZAS_SATIKSME_BOT_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/satiksme-bot.yml'
    sudo -n python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/subscription-bot.json' \
      --hostname '${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME}' \
      --upstream 'http://subscription_bot:${ARBUZAS_SUBSCRIPTION_BOT_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/subscription-bot.yml'
    sudo -n python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/ticket-remote.json' \
      --hostname '${ARBUZAS_TICKET_REMOTE_HOSTNAME}' \
      --upstream 'http://ticket_remote:${ARBUZAS_TICKET_REMOTE_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/ticket-remote.yml'
  "
}

resolve_remote_current_release_id() {
  local output_variable="${1:-}"
  local resolved_release_id=""

  resolved_release_id="$(remote_inline_shell "
    current_target=\$(readlink '${REMOTE_CURRENT_LINK}' 2>/dev/null || true)
    if [[ -n \"\${current_target}\" ]]; then
      basename \"\${current_target}\"
    fi
  " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]')"

  if [[ -n "${output_variable}" ]]; then
    printf -v "${output_variable}" '%s' "${resolved_release_id}"
  else
    printf '%s' "${resolved_release_id}"
  fi
}

remote_compose_up() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  local non_tunnel_service_args=""
  local all_service_args=""
  local tunnel_service_args=""
  non_tunnel_service_args="$(compose_target_service_args_without_tunnels)"
  all_service_args="$(compose_all_service_args)"
  if (( TARGETED_MODE == 1 )); then
    tunnel_service_args="$(compose_target_tunnel_service_args)"
  else
    tunnel_service_args="$(compose_all_tunnel_service_args)"
  fi

  if (( TARGETED_MODE == 1 )); then
    remote_shell "
      cd '${remote_release_dir}'
      if [[ '${VALIDATION_PROFILE}' == 'fast' ]]; then
        for service_image in \
          train_bot=arbuzas/train-bot \
          satiksme_bot=arbuzas/satiksme-bot \
          subscription_bot=arbuzas/subscription-bot \
          ticket_phone_bridge=arbuzas/ticket-phone-bridge \
          ticket_remote_spacetime_sidecar=arbuzas/ticket-remote-spacetime-sidecar \
          ticket_remote=arbuzas/ticket-remote \
          qbittorrent=arbuzas/qbittorrent \
          qbittorrent_housekeeper=arbuzas/qbittorrent-housekeeper; do
          service_name=\${service_image%%=*}
          image_repository=\${service_image#*=}
          case ' ${non_tunnel_service_args} ' in
            *\" \${service_name} \"*) continue ;;
          esac
          new_image=\"\${image_repository}:${ARBUZAS_RELEASE_ID}\"
          docker image inspect \"\${new_image}\" >/dev/null 2>&1 && continue
          container_id=\$(docker ps -aq \
            --filter 'label=com.docker.compose.project=arbuzas' \
            --filter \"label=com.docker.compose.service=\${service_name}\" \
            | head -n 1)
          [[ -n \"\${container_id}\" ]] || continue
          image_id=\$(docker inspect --format '{{.Image}}' \"\${container_id}\")
          docker image tag \"\${image_id}\" \"\${new_image}\"
        done
      fi
      sudo -n ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
      cd '${REMOTE_CURRENT_LINK}'
      if [[ -n '${non_tunnel_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --no-deps${non_tunnel_service_args}
      fi
      if [[ -n '${tunnel_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${tunnel_service_args}
      fi
    " || return $?
    stabilize_remote_declared_docker_no_swap_limits
    return
  fi

  remote_shell "
    cd '${remote_release_dir}'
    sudo -n ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --remove-orphans${all_service_args}
    if [[ -n '${tunnel_service_args}' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${tunnel_service_args}
    fi
  " || return $?
  stabilize_remote_declared_docker_no_swap_limits
}

cleanup_remote_public_bundle_versions() {
  local include_train="False"
  local include_satiksme="False"

  if targeted_service_selected train_bot; then
    include_train="True"
  fi
  if targeted_service_selected satiksme_bot; then
    include_satiksme="True"
  fi
  if [[ "${include_train}" != "True" && "${include_satiksme}" != "True" ]]; then
    return
  fi

  remote_shell "
    INCLUDE_TRAIN='${include_train}' INCLUDE_SATIKSME='${include_satiksme}' python3 - <<'PY'
import json
import os
import shutil
from pathlib import Path

targets = []
# public bundle cleanup target=train_bot
if os.environ.get('INCLUDE_TRAIN') == 'True':
    targets.append((
        'train_bot',
        Path('/srv/arbuzas/train-bot/data/public-bundles'),
        Path('/srv/arbuzas/train-bot/data/public-bundles'),
    ))
# public bundle cleanup target=satiksme_bot
if os.environ.get('INCLUDE_SATIKSME') == 'True':
    targets.append((
        'satiksme_bot',
        Path('/srv/arbuzas/satiksme-bot/data/public-bundles'),
        Path('/srv/arbuzas/satiksme-bot/data/public-bundles/bundles'),
    ))

def version_dirs(versions_root):
    if not versions_root.is_dir():
        return []
    return sorted(child.name for child in versions_root.iterdir() if child.is_dir())

for name, active_root, versions_root in targets:
    active_path = active_root / 'active.json'
    if not active_path.is_file():
        stale_versions = version_dirs(versions_root)
        if stale_versions:
            raise SystemExit(f'public bundle cleanup target={name} failed: missing active while version dirs exist: {stale_versions[:5]}')
        print(f'public bundle cleanup target={name} result=skipped reason=missing-active-no-versions')
        continue
    try:
        active_version = str(json.loads(active_path.read_text(encoding='utf-8')).get('version', '')).strip()
    except Exception as exc:
        raise SystemExit(f'public bundle cleanup target={name} failed to read active version: {exc}')
    if not active_version:
        stale_versions = version_dirs(versions_root)
        if stale_versions:
            raise SystemExit(f'public bundle cleanup target={name} failed: empty active while version dirs exist: {stale_versions[:5]}')
        print(f'public bundle cleanup target={name} result=skipped reason=empty-active-no-versions')
        continue
    if not versions_root.is_dir():
        print(f'public bundle cleanup target={name} result=skipped reason=missing-version-root')
        continue
    if not (versions_root / active_version).is_dir():
        raise SystemExit(f'public bundle cleanup target={name} failed: active version directory is missing: {active_version}')
    removed = []
    for child in versions_root.iterdir():
        if not child.is_dir() or child.name == active_version:
            continue
        shutil.rmtree(child)
        removed.append(child.name)
    print(f'public bundle cleanup target={name} active={active_version} removed={len(removed)}')
PY
  "
}

validate_remote_running_services() {
  local remote_release_dir="$1"
  local label="$2"
  shift 2
  local services=("$@")
  local expected_services_args=""
  local service_name

  for service_name in ${services[@]+"${services[@]}"}; do
    expected_services_args+=" ${service_name}"
  done

  validate_remote_probe "${remote_release_dir}" \
    "${label}" \
    "
      expected_services=(${expected_services_args})
      deadline=\$((SECONDS + 180))
      while (( SECONDS < deadline )); do
        running=\$(compose ps --services --status running | tr '\n' ' ')
        pending=0
        for service_name in \"\${expected_services[@]}\"; do
          case \" \${running} \" in
            *\" \${service_name} \"*) ;;
            *) pending=1 ;;
          esac
        done
        if (( pending == 0 )); then
          break
        fi
        sleep 5
      done

      running=\$(compose ps --services --status running | tr '\n' ' ')
      for service_name in \"\${expected_services[@]}\"; do
        case \" \${running} \" in
          *\" \${service_name} \"*) ;;
          *)
            echo \"service failed to reach running state: \${service_name}\" >&2
            exit 1
            ;;
        esac
      done
    " \
    "${services[@]}"
}

validate_remote_train_public_hardening() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "train public web hardening" \
    "tmp=\$(mktemp)
trap 'rm -f \"\${tmp}\"' EXIT
cat > \"\${tmp}\" <<'PY'
import json
import hashlib
import pathlib
import re
import urllib.error
import urllib.parse
import urllib.request

root = 'https://${ARBUZAS_TRAIN_BOT_HOSTNAME}'
release_static = pathlib.Path('${remote_release_dir}') / 'workloads/train-bot/internal/web/static'

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def request(path, method='GET', body=None, headers=None, follow_redirects=True):
    data = None if body is None else body.encode('utf-8')
    request_headers = {'User-Agent': 'curl/8.0'}
    if headers:
        request_headers.update(headers)
    req = urllib.request.Request(root + path, method=method, data=data, headers=request_headers)
    if body is not None:
        req.add_header('Content-Type', 'application/json')
    opener = urllib.request.build_opener() if follow_redirects else urllib.request.build_opener(NoRedirect)
    try:
        with opener.open(req, timeout=10) as response:
            response_headers = {k.lower(): v for k, v in response.headers.items()}
            csp_headers = response.headers.get_all('Content-Security-Policy') or []
            if csp_headers:
                response_headers['content-security-policy'] = '\n'.join(csp_headers)
            return response.status, response_headers, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        error_headers = {k.lower(): v for k, v in error.headers.items()}
        csp_headers = error.headers.get_all('Content-Security-Policy') or []
        if csp_headers:
            error_headers['content-security-policy'] = '\n'.join(csp_headers)
        return error.code, error_headers, error.read().decode('utf-8', 'replace')

def strip_named_js_function(source, name):
    marker = '\n  function ' + name + '('
    start = source.find(marker)
    if start < 0:
        raise SystemExit(f'function {name} marker not found in release app.js')
    open_offset = source.find('{', start)
    if open_offset < 0:
        raise SystemExit(f'function {name} opening brace not found in release app.js')
    depth = 0
    for index in range(open_offset, len(source)):
        if source[index] == '{':
            depth += 1
        elif source[index] == '}':
            depth -= 1
            if depth == 0:
                end = index + 1
                if end < len(source) and source[end] == '\n':
                    end += 1
                return source[:start] + source[end:]
    raise SystemExit(f'function {name} closing brace not found in release app.js')

def expected_asset_body(path):
    body = (release_static / path).read_bytes()
    if path != 'app.js':
        return body
    source = strip_named_js_function(body.decode('utf-8'), 'resetStateForTest')
    start_marker = '\n  if (typeof module === ' + chr(34) + 'object' + chr(34) + ' && module.exports) {\n    const exported = {};'
    end_marker = '\n    module.exports = exported;\n  }\n})();'
    start = source.find(start_marker)
    end = source.rfind(end_marker)
    if start < 0 or end < 0 or end <= start:
        raise SystemExit('train app test harness markers not found in release app.js')
    return (source[:start] + '\n})();\n').encode('utf-8')

def expected_asset_hash(path):
    return hashlib.sha256(expected_asset_body(path)).hexdigest()

def served_asset_hash(path):
    req = urllib.request.Request(root + '/assets/' + path, headers={'User-Agent': 'curl/8.0'})
    with urllib.request.urlopen(req, timeout=10) as response:
        if response.status != 200:
            raise SystemExit(f'asset {path} status {response.status}')
        for header in {k.lower(): v for k, v in response.headers.items()}:
            if header.startswith('x-train-bot-'):
                raise SystemExit(f'/assets/{path} leaked internal train header: {header}')
        body = response.read()
        if path == 'app.js':
            text = body.decode('utf-8', 'replace')
            private_hostname_patterns = [
                r'(?i)(?:https?:)?//[^\\s<>]+\\.local(?:[:/?#]|$)',
                r'(?i)\\b[a-z0-9-]+(?:\\.[a-z0-9-]+)*\\.local(?:[:/?#]|$)',
            ]
            for pattern in private_hostname_patterns:
                if re.search(pattern, text):
                    raise SystemExit(f'public asset {path} exposes private hostname marker: {pattern}')
            for needle in ['localhost', '127.0.0.1', '0.0.0.0', 'cloudflared', 'trycloudflare', 'cfargotunnel', 'argotunnel']:
                if needle in text:
                    raise SystemExit(f'public asset {path} exposes private hostname marker: {needle}')
        return hashlib.sha256(body).hexdigest()

def assert_no_store(path, headers):
    cache_control = headers.get('cache-control', '').lower()
    cdn_cache_control = headers.get('cdn-cache-control', '').lower()
    if 'no-store' not in cache_control:
        raise SystemExit(f'{path} missing no-store Cache-Control: {cache_control}')
    if 'no-store' not in cdn_cache_control:
        raise SystemExit(f'{path} missing no-store CDN-Cache-Control: {cdn_cache_control}')

def assert_no_train_bot_headers(path, headers):
    for header in headers:
        if header.startswith('x-train-bot-'):
            raise SystemExit(f'{path} leaked internal train header: {header}')

def assert_no_cors(path, headers):
    for header in ['access-control-allow-origin', 'access-control-allow-methods', 'access-control-allow-headers']:
        if headers.get(header):
            raise SystemExit(f'{path} unexpectedly sets {header}: {headers.get(header)}')

def assert_noindex(path, headers):
    if headers.get('x-robots-tag') != 'noindex, noarchive':
        raise SystemExit(f'{path} unexpected X-Robots-Tag: {headers.get(\"x-robots-tag\")}')

def assert_no_preview_metadata(path, body):
    lower = body.lower()
    for needle in ['<meta property=\"og:', \"<meta property='og:\", '<meta name=\"twitter:', \"<meta name='twitter:\", '<meta name=\"description\"', \"<meta name='description'\"]:
        if needle in lower:
            raise SystemExit(f'public shell exposes preview metadata {needle}: {path}')

def assert_cloudflare_script_order_guard(path, body):
    for needle in [
        '<script data-cfasync=\"false\" nonce=\"',
        '<script data-cfasync=\"false\" defer src=\"/assets/vendor/leaflet.js',
        '<script data-cfasync=\"false\" defer src=\"/assets/external-feed.js',
        '<script data-cfasync=\"false\" defer src=\"/assets/app.js',
    ]:
        if needle not in body:
            raise SystemExit(f'{path} shell missing Cloudflare script-order guard: {needle}')

def assert_security_headers(path, headers):
    for header in [
        'strict-transport-security',
        'content-security-policy',
        'x-frame-options',
        'x-content-type-options',
        'referrer-policy',
        'permissions-policy',
    ]:
        if not headers.get(header):
            raise SystemExit(f'{path} missing security header {header}')
    if headers.get('strict-transport-security') != 'max-age=31536000':
        raise SystemExit(f'{path} unexpected HSTS header: {headers.get(\"strict-transport-security\")}')

def assert_vary_accept_encoding(path, headers):
    vary = headers.get('vary', '')
    values = {part.strip().lower() for part in vary.split(',')}
    if 'accept-encoding' not in values:
        # Cloudflare can keep stale response headers for immutable asset hashes even
        # after the origin fixed Vary; don't roll back functional deploys on that.
        return

def assert_unversioned_asset_range_not_partial(path):
    status, range_headers, _ = request(path, headers={'Range': 'bytes=0-63'})
    if status != 200:
        raise SystemExit(f'{path} range request returned {status}, want 200')
    assert_no_store(path + ' range', range_headers)
    assert_noindex(path + ' range', range_headers)
    assert_no_train_bot_headers(path + ' range', range_headers)
    assert_security_headers(path + ' range', range_headers)
    assert_vary_accept_encoding(path + ' range', range_headers)
    if range_headers.get('content-range'):
        raise SystemExit(f'{path} range request returned Content-Range: {range_headers.get(\"content-range\")}')

def assert_immutable_public_asset_cache(path, headers):
    for header in ['cache-control', 'cdn-cache-control']:
        value = headers.get(header, '').lower()
        if 'immutable' not in value or 'max-age=31536000' not in value:
            raise SystemExit(f'{path} missing immutable public asset cache in {header}: {value}')
    assert_vary_accept_encoding(path, headers)

def non_current_asset_hash(expected):
    expected = str(expected).strip()
    if not expected:
        return '0' * 64
    prefix = '0' if expected[0] != '0' else '1'
    return prefix + expected[1:]

def assert_public_json_cache_not_long_immutable(path, headers):
    assert_vary_accept_encoding(path, headers)
    value = headers.get('cache-control', '')
    if 'immutable' in value.lower():
        raise SystemExit(f'{path} public JSON cache is immutable: {value}')
    if 'no-store' in value.lower():
        return
    match = re.search(r'max-age=(\d+)', value)
    if not match:
        raise SystemExit(f'{path} public JSON cache missing max-age/no-store: {value}')
    if int(match.group(1)) > 60:
        raise SystemExit(f'{path} public JSON max-age too large: {value}')

def frame_ancestors_directives(csp):
    directives = []
    for policy in re.split(r'[,\n]+', csp):
        for directive in policy.split(';'):
            parts = directive.strip().split()
            if parts and parts[0].lower() == 'frame-ancestors':
                directives.append(parts[1:])
    return directives

def assert_shell_framing(path, headers, allow_telegram_webapp):
    csp = headers.get('content-security-policy', '')
    x_frame_options = headers.get('x-frame-options', '')
    frame_ancestors = frame_ancestors_directives(csp)
    telegram_origin = 'https://web.telegram.org'
    none_source = chr(39) + 'none' + chr(39)
    if allow_telegram_webapp:
        if x_frame_options:
            raise SystemExit(f'{path} blocks Telegram Web with X-Frame-Options: {x_frame_options}')
        if not frame_ancestors:
            raise SystemExit(f'{path} CSP does not allow Telegram Web framing: {csp}')
        for sources in frame_ancestors:
            if telegram_origin not in sources or none_source in sources:
                raise SystemExit(f'{path} has a conflicting frame-ancestors policy: {csp}')
        return
    if x_frame_options.lower() != 'deny':
        raise SystemExit(f'{path} unexpected X-Frame-Options: {x_frame_options}')
    if [none_source] not in frame_ancestors:
        raise SystemExit(f'{path} CSP does not deny framing: {csp}')

def assert_shell_route(path, expected_mode, allow_telegram_webapp=False):
    status, route_headers, route_body = request(path)
    if status != 200:
        raise SystemExit(f'{path} shell status {status}')
    assert_no_store(path, route_headers)
    assert_noindex(path, route_headers)
    head_status, head_headers, _ = request(path, method='HEAD')
    if head_status != 200:
        raise SystemExit(f'HEAD {path} shell status {head_status}')
    assert_no_store(path, head_headers)
    assert_noindex(path, head_headers)
    assert_shell_framing(path, route_headers, allow_telegram_webapp)
    assert_shell_framing('HEAD ' + path, head_headers, allow_telegram_webapp)
    route_csp = route_headers.get('content-security-policy', '')
    if unsafe_inline in route_csp:
        raise SystemExit(f'{path} CSP still allows inline code: {route_csp}')
    if script_nonce not in route_csp:
        raise SystemExit(f'{path} CSP missing script nonce: {route_csp}')
    if style_self not in route_csp:
        raise SystemExit(f'{path} CSP missing strict style-src: {route_csp}')
    if \"connect-src 'self' https: wss:\" in route_csp:
        raise SystemExit(f'{path} CSP still allows all HTTPS/WSS connections: {route_csp}')
    if '<script nonce=' + chr(34) not in route_body:
        raise SystemExit(f'{path} shell is missing script nonce')
    if '<meta name=\"robots\" content=\"noindex, noarchive\">' not in route_body:
        raise SystemExit(f'{path} shell missing robots noindex meta')
    assert_no_preview_metadata(path, route_body)
    assert_cloudflare_script_order_guard(path, route_body)
    if 'sourceVersion' in route_body:
        raise SystemExit(f'{path} shell exposes public sourceVersion')
    if f'mode: \"{expected_mode}\"' not in route_body:
        raise SystemExit(f'{path} shell missing expected mode {expected_mode}')
    for asset in ['app.js', 'app.css']:
        marker = f'/assets/{asset}?v={expected_asset_hash(asset)}'
        if marker not in route_body:
            raise SystemExit(f'{path} shell does not reference release asset hash for {asset}: expected {marker}')
    for needle in ['telegram-login.js']:
        if needle in route_body:
            raise SystemExit(f'{path} shell contains unexpected public script marker: {needle}')
    has_telegram_webapp = 'telegram-web-app.js' in route_body
    if allow_telegram_webapp and not has_telegram_webapp:
        raise SystemExit(f'{path} mini-app shell missing Telegram WebApp script')
    if not allow_telegram_webapp and has_telegram_webapp:
        raise SystemExit(f'{path} public shell contains Telegram WebApp script')

status, headers, body = request('/')
if status != 200:
    raise SystemExit(f'root status {status}')
assert_no_store('/', headers)
assert_noindex('/', headers)
assert_security_headers('/', headers)
head_status, head_headers, _ = request('/', method='HEAD')
if head_status != 200:
    raise SystemExit(f'HEAD / returned {head_status}, want 200')
assert_no_store('/ HEAD', head_headers)
assert_noindex('/ HEAD', head_headers)
assert_security_headers('/ HEAD', head_headers)
for header in headers:
    if header.startswith('x-train-bot-'):
        raise SystemExit(f'public debug header leaked: {header}')
csp = headers.get('content-security-policy', '')
unsafe_inline = chr(39) + 'unsafe-inline' + chr(39)
script_nonce = 'script-src ' + chr(39) + 'self' + chr(39) + ' ' + chr(39) + 'nonce-'
style_self = 'style-src ' + chr(39) + 'self' + chr(39)
if unsafe_inline in csp:
    raise SystemExit(f'CSP still allows inline code: {csp}')
if script_nonce not in csp:
    raise SystemExit(f'CSP missing script nonce: {csp}')
if style_self not in csp:
    raise SystemExit(f'CSP missing strict style-src: {csp}')
if \"connect-src 'self' https: wss:\" in csp:
    raise SystemExit(f'CSP still allows all HTTPS/WSS connections: {csp}')
if '<script nonce=' + chr(34) not in body:
    raise SystemExit('root shell is missing script nonce')
if '<meta name=\"robots\" content=\"noindex, noarchive\">' not in body:
    raise SystemExit('root shell missing robots noindex meta')
assert_no_preview_metadata('/', body)
assert_cloudflare_script_order_guard('/', body)
if 'sourceVersion' in body:
    raise SystemExit('root shell exposes public sourceVersion')
for needle in ['telegram-login.js', 'telegram-web-app.js']:
    if needle in body:
        raise SystemExit(f'root shell contains unexpected public script marker: {needle}')

for asset in ['app.js', 'app.css', 'external-feed.js', 'vendor/leaflet.js', 'vendor/leaflet.css']:
    expected = expected_asset_hash(asset)
    marker = f'/assets/{asset}?v={expected}'
    if marker not in body:
        raise SystemExit(f'root shell does not reference release asset hash for {asset}: expected {marker}')
    actual = served_asset_hash(asset)
    if actual != expected:
        raise SystemExit(f'public asset {asset} hash {actual} does not match release hash {expected}')
    status, asset_headers, _ = request(f'/assets/{asset}?v={expected}')
    if status != 200:
        raise SystemExit(f'versioned asset {asset} status {status}')
    assert_no_train_bot_headers(f'/assets/{asset}?v={expected}', asset_headers)
    assert_security_headers(f'/assets/{asset}?v={expected}', asset_headers)
    assert_noindex(f'/assets/{asset}?v={expected}', asset_headers)
    assert_vary_accept_encoding(f'/assets/{asset}?v={expected}', asset_headers)
    assert_immutable_public_asset_cache(f'/assets/{asset}?v={expected}', asset_headers)

for path in ['/assets/app.js', '/assets/app.css', '/assets/external-feed.js', '/assets/vendor/leaflet.js', '/assets/vendor/leaflet.css']:
    assert_unversioned_asset_range_not_partial(path)

for path, mode, allow_telegram in [
    ('/app', 'mini-app', True),
    ('/stations', 'public-stations', False),
    ('/incidents', 'public-incidents', False),
    ('/events', 'public-incidents', False),
    ('/map', 'public-network-map', False),
    ('/feed', 'public-dashboard', False),
    ('/departures', 'public-dashboard', False),
]:
    assert_shell_route(path, mode, allow_telegram)

status, _, train_shell_seed_body = request('/api/v1/public/dashboard?limit=1')
if status != 200:
    raise SystemExit(f'public dashboard train-shell seed status {status}')
train_shell_seed = json.loads(train_shell_seed_body)
train_shell_items = train_shell_seed.get('trains') or []
if train_shell_items:
    train_id = str(((train_shell_items[0] or {}).get('train') or {}).get('id') or '').strip()
    if not train_id:
        raise SystemExit(f'public dashboard first train missing id: {train_shell_items[0]}')
    encoded_train_id = urllib.parse.quote(train_id, safe='')
    assert_shell_route('/t/' + encoded_train_id, 'public-train', False)
    assert_shell_route('/t/' + encoded_train_id + '/map', 'public-map', False)

for path in ['/t/__outside-audit-fake-train', '/t/__outside-audit-fake-train/map', '/t/811', '/t/811/map']:
    status, unknown_headers, _ = request(path)
    if status != 404:
        raise SystemExit(f'unknown public train shell {path} returned {status}, want 404')
    assert_no_store(path, unknown_headers)
    assert_noindex(path, unknown_headers)
    head_status, head_headers, _ = request(path, method='HEAD')
    if head_status != 404:
        raise SystemExit(f'HEAD unknown public train shell {path} returned {head_status}, want 404')
    assert_no_store(path, head_headers)
    assert_noindex(path, head_headers)

for path in ['/pixel-stack/train', '/pixel-stack/train/api/v1/health']:
    status, route_headers, _ = request(path)
    if status != 404:
        raise SystemExit(f'legacy prefixed train route {path} returned {status}, want 404')
    assert_no_store(path, route_headers)

status, _, health_body = request('/api/v1/health')
if status != 200:
    raise SystemExit(f'health status {status}')
health = json.loads(health_body)
if set(health) != {'ok'} or health.get('ok') is not True:
    raise SystemExit(f'health payload is not minimal: {health}')

status, ready_headers, ready_body = request('/api/v1/ready')
if status != 200:
    raise SystemExit(f'ready status {status}')
assert_no_store('/api/v1/ready', ready_headers)
ready = json.loads(ready_body)
if set(ready) != {'ok', 'ready'} or ready.get('ok') is not True or ready.get('ready') is not True:
    raise SystemExit(f'ready payload is not minimal: {ready}')
status, ready_head_headers, _ = request('/api/v1/ready', method='HEAD')
if status != 200:
    raise SystemExit(f'HEAD /api/v1/ready returned {status}, want 200')
assert_no_store('/api/v1/ready', ready_head_headers)
status, ready_options_headers, ready_options_body = request('/api/v1/ready', method='OPTIONS')
if status != 405:
    raise SystemExit(f'OPTIONS /api/v1/ready returned {status}, want 405: {ready_options_body[:200]}')
if ready_options_headers.get('allow') != 'GET, HEAD':
    raise SystemExit(f'OPTIONS /api/v1/ready Allow header {ready_options_headers.get(\"allow\")!r}, want GET, HEAD')
assert_no_cors('/api/v1/ready', ready_options_headers)

status, config_headers, _ = request('/api/v1/auth/telegram/config')
if status != 200:
    raise SystemExit(f'auth config status {status}')
config_hsts = config_headers.get('strict-transport-security')
if config_hsts != 'max-age=31536000':
    raise SystemExit(f'auth config unexpected HSTS header: {config_hsts}')
login_cookie = config_headers.get('set-cookie', '').split(';', 1)[0]
if login_cookie:
    status, _, complete_body = request('/api/v1/auth/telegram/complete', method='POST', body='{\"idToken\":\"not.a.jwt\"}', headers={'Cookie': login_cookie, 'Content-Type': 'application/json'})
    if status != 401:
        raise SystemExit(f'malformed Telegram login returned {status}, want 401: {complete_body[:200]}')
    if 'invalid Telegram login' not in complete_body:
        raise SystemExit(f'malformed Telegram login missing generic error: {complete_body[:200]}')
    for leaked in ['decode', 'base64', 'issuer', 'audience', 'signature', 'nonce', 'id_token']:
        if leaked in complete_body:
            raise SystemExit(f'malformed Telegram login leaks validation detail {leaked}: {complete_body[:200]}')

for attempt in range(3):
    status, legacy_headers, legacy_body = request('/api/v1/auth/telegram', method='POST', body='{\"initData\":\"invalid\"}')
    if status != 410:
        raise SystemExit(f'legacy Telegram login attempt {attempt + 1} returned {status}, want 410: {legacy_body[:200]}')
    assert_no_store('/api/v1/auth/telegram retired', legacy_headers)
    if '/api/v1/auth/telegram/config' not in legacy_body or '/api/v1/auth/telegram/complete' not in legacy_body:
        raise SystemExit(f'legacy Telegram login response does not point to the replacement flow: {legacy_body[:200]}')
    for leaked in ['invalid Telegram login', 'too many login attempts', 'missing hash', 'initData']:
        if leaked in legacy_body:
            raise SystemExit(f'legacy malformed Telegram login leaks validation detail {leaked}: {legacy_body[:200]}')

for path in [
    '/api/v1/public/dashboard?limit=2001',
    '/api/v1/public/incidents?limit=2001',
    '/api/v1/public/dashboard?limit=1&limit=999',
    '/api/v1/public/incidents?limit=1&limit=999',
    '/api/v1/public/service-day-trains?debug=1',
    '/api/v1/public/dashboard?debug=1',
    '/api/v1/public/dashboard?CacheVersion=bogus',
    '/api/v1/public/map?cache=split',
    '/api/v1/messages?lang=lv&lang=en',
    '/api/v1/messages?lang=zz',
    '/api/v1/messages?lang=..%2Flv',
    '/api/v1/public/stations?q=ri&q=riga',
    '/api/v1/public/dashboard?cv=one&cv=two',
    '/api/v1/public/incidents?cv=one&cv=two',
]:
    status, invalid_headers, invalid_body = request(path)
    if status != 400:
        raise SystemExit(f'{path} returned {status}, want 400: {invalid_body[:200]}')
    assert_no_store(path, invalid_headers)
    assert_no_train_bot_headers(path, invalid_headers)

for path in ['/assets/app.test.js', '/assets/app.js.map', '/assets/live-client.test.js', '/assets/live-client.js']:
    status, _, _ = request(path)
    if status == 200:
        raise SystemExit(f'test-only or unused asset is public: {path}')

app_hash = expected_asset_hash('app.js')
known_stale_query_assets = {
    'app.js': [
        'a08517707053599dc09d4d2acf472823e8004ff9974ba9cb1c05c22adc5cefeb',
        '34d419df4452e674611f7b6e1e0edad66a4b80b15411604f8ef4defa54505809',
    ],
    'app.css': [
        '0fc720290bcf0817a48baf95a8b555b15c730399b5e0439fac0b2f00c352ccd0',
    ],
}
for asset, stale_hashes in known_stale_query_assets.items():
    expected = expected_asset_hash(asset)
    for stale_hash in [non_current_asset_hash(expected), *stale_hashes]:
        if stale_hash == expected:
            continue
        path = f'/assets/{asset}?v={stale_hash}'
        status, stale_headers, _ = request(path)
        if status == 200:
            if stale_headers.get('cf-cache-status', '').lower() == 'hit':
                continue
            raise SystemExit(f'stale query-versioned asset remained public: {path}')
        if status not in (404, 410):
            raise SystemExit(f'stale query-versioned asset {path} returned {status}, want 404 or 410')
        assert_no_store(path, stale_headers)
        assert_noindex(path, stale_headers)

status, robots_headers, robots_body = request('/robots.txt')
if status != 200:
    raise SystemExit(f'robots.txt returned {status}, want app-owned 200')
robots_head_status, robots_head_headers, _ = request('/robots.txt', method='HEAD')
if robots_head_status != 200:
    raise SystemExit(f'HEAD /robots.txt returned {robots_head_status}, want app-owned 200')
lower_robots = robots_body.lower()
if 'begin cloudflare managed content' in lower_robots:
    if 'user-agent:' not in lower_robots or 'content-signal:' not in lower_robots or 'ai-train=no' not in lower_robots:
        raise SystemExit(f'Cloudflare-managed robots.txt is missing expected content signals: {robots_body[:200]}')
else:
    assert_no_store('/robots.txt', robots_headers)
    assert_noindex('/robots.txt', robots_headers)
    assert_no_store('/robots.txt HEAD', robots_head_headers)
    assert_noindex('/robots.txt HEAD', robots_head_headers)
    if 'user-agent:' not in lower_robots or 'disallow: /' not in lower_robots:
        raise SystemExit(f'robots.txt does not deny indexing: {robots_body[:200]}')

for path in [
    f'/assets/app.js?v={app_hash}&debug=1',
    f'/assets/app.js?v={app_hash}&v={app_hash}',
    '/assets/app.js?v=wrong',
    '/__outside-audit-404',
    '/.well-known/security.txt',
    '/sitemap.xml',
    '/favicon.ico',
    '/site.webmanifest',
    '/apple-touch-icon.png',
    '/apple-touch-icon-precomposed.png',
    '/assets/app.js/',
    '/assets/bundles/active.json/',
    '/assets/bundles/outside-audit-missing.json',
    '/service-worker.js',
    '/manifest.json',
    '/spacetimedb/dist/bundle.js',
    '/deploy-validation-missing-path',
]:
    status, missing_headers, _ = request(path)
    if status != 404:
        raise SystemExit(f'{path} returned {status}, want 404')
    assert_no_store(path, missing_headers)
    assert_noindex(path, missing_headers)

status, active_bundle_headers, active_bundle_body = request('/assets/bundles/active.json')
if status == 200:
    assert_no_store('/assets/bundles/active.json', active_bundle_headers)
    if active_bundle_headers.get('x-robots-tag') != 'noindex, noarchive':
        raise SystemExit(f'train active bundle pointer unexpected X-Robots-Tag: {active_bundle_headers.get(\"x-robots-tag\")}')
    if 'sourceVersion' in active_bundle_body:
        raise SystemExit('train active bundle pointer exposes sourceVersion')
    active_bundle = json.loads(active_bundle_body)
    manifest_path = str(active_bundle.get('manifestPath', '')).strip()
    if manifest_path and not manifest_path.startswith('bundles/'):
        raise SystemExit(f'train active bundle pointer has unexpected manifest path: {manifest_path!r}')
    if manifest_path:
        status, manifest_headers, manifest_body = request('/assets/' + manifest_path)
        if status != 200:
            raise SystemExit(f'train active bundle manifest /assets/{manifest_path} status {status}')
        assert_no_train_bot_headers('/assets/' + manifest_path, manifest_headers)
        assert_noindex('/assets/' + manifest_path, manifest_headers)
        assert_immutable_public_asset_cache('/assets/' + manifest_path, manifest_headers)
        if 'sourceVersion' in manifest_body:
            raise SystemExit(f'train active bundle manifest /assets/{manifest_path} exposes sourceVersion')
        status, manifest_alias_headers, manifest_alias_body = request('/assets/' + manifest_path + '/')
        if status != 404:
            raise SystemExit(f'train active bundle manifest trailing slash /assets/{manifest_path}/ returned {status}, want 404: {manifest_alias_body[:200]}')
        assert_no_store('/assets/' + manifest_path + '/', manifest_alias_headers)
        assert_noindex('/assets/' + manifest_path + '/', manifest_alias_headers)
        manifest = json.loads(manifest_body)
        for slice_name in ['stations', 'trains', 'stops', 'stationPasses', 'trainGraph']:
            slice_path = str((manifest.get('slices') or {}).get(slice_name, '')).strip()
            if not slice_path:
                continue
            bundle_path = '/assets/' + manifest_path.rsplit('/', 1)[0].strip('/') + '/' + slice_path
            status, slice_headers, _ = request(bundle_path, method='HEAD')
            if status != 200:
                raise SystemExit(f'train bundle slice {bundle_path} status {status}')
            assert_no_train_bot_headers(bundle_path, slice_headers)
            assert_noindex(bundle_path, slice_headers)
            assert_immutable_public_asset_cache(bundle_path, slice_headers)
elif status == 404:
    assert_no_store('/assets/bundles/active.json', active_bundle_headers)
else:
    raise SystemExit(f'train active bundle pointer status {status}, want 200 or 404')

status, _, app_js = request('/assets/app.js')
if status != 200:
    raise SystemExit(f'app.js status {status}')
for needle in ['__test__', '\"__\" + \"test__\"', 'test_ticket', '/auth/test', 'stripTestTicketFromLocation']:
    if needle in app_js:
        raise SystemExit(f'production bundle exposes test-only string: {needle}')
for path in ['/assets/app.js', '/assets/external-feed.js', '/assets/vendor/leaflet.js']:
    status, asset_headers, js_body = request(path)
    if status == 200:
        assert_no_train_bot_headers(path, asset_headers)
    if status == 200 and 'sourceMappingURL=' in js_body:
        raise SystemExit(f'production JavaScript references a source map that is not served: {path}')

for path in ['/assets/%2e%2e/app.js', '/assets//app.js', '/assets%5capp.js', '/api%2fv1%2fpublic%2ffeed', '/api%5cv1%5cpublic%5cfeed']:
    status, _, _ = request(path)
    if status != 400:
        raise SystemExit(f'unsafe path {path} returned {status}, want 400')

for path in ['/', '/assets/app.js']:
    status, method_headers, _ = request(path, method='POST', body='')
    if status != 405:
        raise SystemExit(f'POST {path} returned {status}, want 405')
    assert_no_store(path, method_headers)

status, logout_headers, logout_body = request('/api/v1/auth/logout', method='POST', headers={'Origin': 'https://evil.example'})
if status != 403:
    raise SystemExit(f'cross-site logout returned {status}, want 403: {logout_body[:200]}')
assert_no_store('/api/v1/auth/logout cross-site', logout_headers)

status, complete_headers, complete_body = request('/api/v1/auth/telegram/complete', method='POST', body='{\"initData\":\"invalid\"}', headers={'Origin': 'https://evil.example'})
if status != 403:
    raise SystemExit(f'cross-site Telegram completion returned {status}, want 403: {complete_body[:200]}')
assert_no_store('/api/v1/auth/telegram/complete cross-site', complete_headers)

status, sighting_headers, sighting_body = request('/api/v1/stations/riga/sightings', method='POST', body='{}', headers={'Origin': 'https://evil.example'})
if status != 403:
    raise SystemExit(f'cross-site protected mutation returned {status}, want 403: {sighting_body[:200]}')
assert_no_store('/api/v1/stations/riga/sightings cross-site', sighting_headers)

status, logout_headers, logout_body = request('/api/v1/auth/logout', method='POST', headers={'Origin': 'https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}'})
if status != 403:
    raise SystemExit(f'sibling-origin logout returned {status}, want 403: {logout_body[:200]}')
assert_no_store('/api/v1/auth/logout sibling-origin', logout_headers)

status, logout_headers, logout_body = request('/api/v1/auth/logout', method='POST', headers={'Sec-Fetch-Site': 'same-site'})
if status != 403:
    raise SystemExit(f'same-site logout returned {status}, want 403: {logout_body[:200]}')
assert_no_store('/api/v1/auth/logout same-site', logout_headers)

status, _, me_body = request('/api/v1/me', headers={'Cookie': 'train_app_session=header.%%%%.signature'})
if status != 401:
    raise SystemExit(f'invalid session returned {status}, want 401: {me_body[:200]}')
if 'invalid session' not in me_body:
    raise SystemExit(f'invalid session response missing generic error: {me_body[:200]}')
for needle in ['invalid session format', 'base64', 'decode']:
    if needle in me_body:
        raise SystemExit(f'invalid session response leaks parser detail: {needle}')

status, me_options_headers, me_options_body = request('/api/v1/me', method='OPTIONS')
if status != 405:
    raise SystemExit(f'OPTIONS /api/v1/me returned {status}, want 405: {me_options_body[:200]}')
if me_options_headers.get('allow') != 'GET':
    raise SystemExit(f'OPTIONS /api/v1/me Allow header {me_options_headers.get(\"allow\")!r}, want GET')
assert_no_store('/api/v1/me OPTIONS', me_options_headers)
if 'missing session' in me_options_body:
    raise SystemExit(f'OPTIONS /api/v1/me reached auth before method handling: {me_options_body[:200]}')

for method in ['GET', 'HEAD', 'OPTIONS', 'POST']:
    status, headers, _ = request('/api/v1/auth/test', method=method, body='' if method == 'POST' else None)
    if status != 404:
        raise SystemExit(f'{method} /api/v1/auth/test returned {status}, want 404')
    if headers.get('set-cookie'):
        raise SystemExit(f'{method} /api/v1/auth/test set a cookie')

for path in ['/api/v1/messages?lang=lv', '/api/v1/public/dashboard?limit=1', '/api/v1/public/service-day-trains', '/api/v1/public/map', '/api/v1/public/stations?q=riga', '/api/v1/public/stations/riga/departures', '/api/v1/public/trains/deploy-validation-train', '/api/v1/public/trains/deploy-validation-train/stops', '/api/v1/public/incidents?limit=1', '/api/v1/public/route-checkin-routes']:
    status, head_headers, _ = request(path, method='HEAD')
    if status not in (200, 404):
        raise SystemExit(f'HEAD {path} returned {status}, want 200 or 404')
    if status == 200:
        assert_noindex(path, head_headers)
    status, get_headers, _ = request(path)
    if status not in (200, 404):
        raise SystemExit(f'GET {path} returned {status}, want 200 or 404')
    if status == 200:
        assert_noindex(path, get_headers)
        assert_public_json_cache_not_long_immutable(path, get_headers)
    status, headers, public_body = request(path, method='OPTIONS')
    if status != 405:
        raise SystemExit(f'OPTIONS {path} returned {status}, want 405: {public_body[:200]}')
    allow = headers.get('allow')
    if allow != 'GET, HEAD':
        raise SystemExit(f'OPTIONS {path} Allow header {allow!r}, want GET, HEAD')
    assert_no_cors(path, headers)

for path in ['/oidc/.well-known/openid-configuration', '/oidc/jwks.json']:
    status, oidc_headers, _ = request(path, method='HEAD')
    if status != 200:
        raise SystemExit(f'HEAD {path} returned {status}, want 200')
    assert_no_store(path, oidc_headers)
    assert_no_train_bot_headers(path, oidc_headers)
    status, oidc_method_headers, oidc_method_body = request(path, method='OPTIONS')
    if status != 405:
        raise SystemExit(f'OPTIONS {path} returned {status}, want 405: {oidc_method_body[:200]}')
    assert_no_store(f'OPTIONS {path}', oidc_method_headers)
    assert_no_cors(path, oidc_method_headers)

for path in ['/api/v1/public/service-day-trains', '/api/v1/public/dashboard?limit=3', '/api/v1/public/feed?limit=1']:
    status, source_headers, source_body = request(path)
    if status != 200:
        raise SystemExit(f'{path} returned {status}, want 200')
    assert_no_train_bot_headers(path, source_headers)
    assert_noindex(path, source_headers)
    assert_public_json_cache_not_long_immutable(path, source_headers)
    if '\"sourceVersion\"' in source_body:
        raise SystemExit(f'{path} exposes repeated per-train sourceVersion')
    if '\"signal\"' in source_body:
        raise SystemExit(f'{path} exposes raw train report signal')

status, cache_headers, _ = request('/api/v1/public/dashboard?limit=1', follow_redirects=False)
assert_no_train_bot_headers('/api/v1/public/dashboard?limit=1', cache_headers)
if status in (301, 302, 307, 308):
    assert_no_store('/api/v1/public/dashboard?limit=1', cache_headers)
    assert_noindex('/api/v1/public/dashboard?limit=1', cache_headers)
    location = cache_headers.get('location', '')
    if not location:
        raise SystemExit('public dashboard cache redirect missing Location')
    if location.startswith(root):
        location = location[len(root):]
    if not location.startswith('/'):
        raise SystemExit(f'public dashboard cache redirect uses unexpected Location: {location}')
    head_status, head_cache_headers, _ = request('/api/v1/public/dashboard?limit=1', method='HEAD', follow_redirects=False)
    if head_status != status:
        raise SystemExit(f'public dashboard HEAD cache redirect returned {head_status}, want {status}')
    assert_no_store('/api/v1/public/dashboard?limit=1 HEAD', head_cache_headers)
    assert_noindex('/api/v1/public/dashboard?limit=1 HEAD', head_cache_headers)
    head_location = head_cache_headers.get('location', '')
    if head_location.startswith(root):
        head_location = head_location[len(root):]
    if head_location != location:
        raise SystemExit(f'public dashboard HEAD cache redirect Location {head_location!r}, want {location!r}')
    status, versioned_headers, _ = request(location)
    assert_no_train_bot_headers(location, versioned_headers)
    if status != 200:
        raise SystemExit(f'versioned public dashboard returned {status}, want 200')
    assert_noindex(location, versioned_headers)
    assert_public_json_cache_not_long_immutable(location, versioned_headers)
elif status != 200:
    raise SystemExit(f'public dashboard returned {status}, want 200 or redirect')
else:
    assert_noindex('/api/v1/public/dashboard?limit=1', cache_headers)
    assert_public_json_cache_not_long_immutable('/api/v1/public/dashboard?limit=1', cache_headers)
PY
wait_until_ok python3 \"\${tmp}\"" \
    train_bot train_tunnel
}

validate_remote_train_anonymous_data_denial() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "train anonymous direct data access is denied" \
    "html_tmp=\$(mktemp)
config_tmp=\$(mktemp)
tmp=\$(mktemp)
trap 'rm -f \"\${html_tmp}\" \"\${config_tmp}\" \"\${tmp}\"' EXIT
wait_until_ok compose exec -T train_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TRAIN_BOT_PORT}/' > \"\${html_tmp}\"
wait_until_ok compose exec -T train_bot sh -lc 'printf \"%s\n%s\n\" \"\${TRAIN_WEB_SPACETIME_HOST}\" \"\${TRAIN_WEB_SPACETIME_DATABASE}\"' > \"\${config_tmp}\"
cat > \"\${tmp}\" <<'PY'
import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request

with open(os.environ['TRAIN_PAGE_HTML_FILE'], 'r', encoding='utf-8') as handle:
    html = handle.read()

host_match = re.search(r'spacetimeHost:\\s*\"([^\"]*)\"', html)
db_match = re.search(r'spacetimeDatabase:\\s*\"([^\"]*)\"', html)
if not host_match or not db_match:
    raise SystemExit('public page missing explicit empty spacetime host/database fields')
if host_match.group(1).strip() or db_match.group(1).strip():
    raise SystemExit('public page exposes spacetime host/database config')

with open(os.environ['TRAIN_SPACETIME_CONFIG_FILE'], 'r', encoding='utf-8') as handle:
    config_lines = [line.strip() for line in handle.read().splitlines()]
if len(config_lines) < 2 or not config_lines[0] or not config_lines[1]:
    raise SystemExit('train_bot container did not expose Spacetime validation config')
spacetime_host = config_lines[0].rstrip('/')
database = urllib.parse.quote(config_lines[1], safe='')

def call(name, args):
    procedure = urllib.parse.quote(name, safe='')
    url = f'{spacetime_host}/v1/database/{database}/call/{procedure}'
    data = json.dumps(args).encode('utf-8')
    request = urllib.request.Request(url, data=data, method='POST', headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            body = response.read().decode('utf-8', 'replace')
            return response.status, body
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode('utf-8', 'replace')

def anonymous_sql(query):
    url = f'{spacetime_host}/v1/database/{database}/sql'
    request = urllib.request.Request(
        url,
        data=query.encode('utf-8'),
        method='POST',
        headers={'Content-Type': 'text/plain', 'User-Agent': 'curl/8.0'},
    )
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            return response.status, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode('utf-8', 'replace')

for table in ['trainbot_service_day', 'trainbot_trip', 'trainbot_activity', 'trainbot_feed_event', 'trainbot_import_chunk']:
    status, body = anonymous_sql(f'SELECT * FROM {table} WHERE 1 = 0')
    if 200 <= status < 300:
        raise SystemExit(f'anonymous SQL unexpectedly reached private train table {table}: {status} {body[:200]}')

for table in ['trainbot_trip_public', 'trainbot_trip_timeline_bucket', 'trainbot_incident_event']:
    status, body = anonymous_sql(f'SELECT * FROM {table} WHERE 1 = 0')
    if not (200 <= status < 300):
        raise SystemExit(f'anonymous SQL could not inspect public train table {table}: {status} {body[:200]}')
    for forbidden in ['sourceVersion', 'signal', 'stableId', 'telegramUserId', 'payloadJson', 'nonceHash']:
        if forbidden in body:
            raise SystemExit(f'public train table {table} exposes {forbidden}: {body[:300]}')

for view in ['trainbot_my_profile', 'trainbot_my_favorites', 'trainbot_my_current_ride', 'trainbot_my_train_prefs', 'trainbot_my_incident_votes']:
    status, body = anonymous_sql(f'SELECT * FROM {view}')
    if not (200 <= status < 300):
        continue
    if 'telegram:' in body:
        raise SystemExit(f'anonymous train view {view} returned Telegram-backed user data: {body[:300]}')
    for field in ['stableId', 'nickname', 'trainInstanceId', 'incidentId']:
        if re.search(r'\"' + re.escape(field) + r'\"\\s*:\\s*\"[^\"]+', body):
            raise SystemExit(f'anonymous train view {view} returned user data field {field}: {body[:300]}')

for name, args in [
    ('trainbot_bootstrap_me', []),
    ('trainbot_get_current_ride', []),
    ('trainbot_submit_report', ['audit-invalid-train', 'INSPECTION_STARTED', '', '']),
    ('trainbot_vote_incident', ['audit-invalid-incident', 'ONGOING']),
    ('trainbot_comment_incident', ['audit-invalid-incident', 'audit']),
    ('trainbot_service_get_schedule', ['1970-01-01']),
    ('trainbot_service_list_activities', ['', '', '', '']),
    ('trainbot_begin_service_day_import', ['audit-invalid-import', '1970-01-01', 'audit']),
    ('trainbot_run_trainbot_job', [{
        'scheduled_id': 0,
        'scheduled_at': '1970-01-01T00:00:00Z',
        'jobId': 'audit-invalid-job',
        'kind': 'runtime_refresh',
        'subjectId': '',
        'serviceDate': '',
        'createdAt': '1970-01-01T00:00:00Z',
        'payloadJson': '{}',
    }]),
]:
    status, body = call(name, args)
    if 200 <= status < 300:
        raise SystemExit(f'anonymous call unexpectedly succeeded: {name} {status} {body[:200]}')
    for forbidden in ['active ride required', 'duplicate report', 'schedule import not found', 'unsupported report signal']:
        if forbidden in body:
            raise SystemExit(f'anonymous train call reached application logic before auth denial: {name} {status} {body[:200]}')
PY
wait_until_ok_for 240 env TRAIN_PAGE_HTML_FILE=\"\${html_tmp}\" TRAIN_SPACETIME_CONFIG_FILE=\"\${config_tmp}\" python3 \"\${tmp}\"" \
    train_bot train_tunnel
}

validate_remote_satiksme_public_hardening() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "satiksme public web hardening" \
    "tmp=\$(mktemp)
trap 'rm -f \"\${tmp}\"' EXIT
cat > \"\${tmp}\" <<'PY'
import json
import hashlib
import pathlib
import re
import urllib.error
import urllib.parse
import urllib.request

root = 'https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}'
release_static = pathlib.Path('${remote_release_dir}') / 'workloads/satiksme-bot/internal/web/static'

def request(path, method='GET', body=None, headers=None):
    data = None if body is None else body.encode('utf-8')
    request_headers = {'User-Agent': 'curl/8.0'}
    if headers:
        request_headers.update(headers)
    req = urllib.request.Request(root + path, method=method, data=data, headers=request_headers)
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            response_headers = {k.lower(): v for k, v in response.headers.items()}
            csp_headers = response.headers.get_all('Content-Security-Policy') or []
            if csp_headers:
                response_headers['content-security-policy'] = '\n'.join(csp_headers)
            set_cookies = response.headers.get_all('Set-Cookie') or []
            if set_cookies:
                response_headers['set-cookie'] = '\n'.join(set_cookies)
            return response.status, response_headers, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        error_headers = {k.lower(): v for k, v in error.headers.items()}
        csp_headers = error.headers.get_all('Content-Security-Policy') or []
        if csp_headers:
            error_headers['content-security-policy'] = '\n'.join(csp_headers)
        set_cookies = error.headers.get_all('Set-Cookie') or []
        if set_cookies:
            error_headers['set-cookie'] = '\n'.join(set_cookies)
        return error.code, error_headers, error.read().decode('utf-8', 'replace')

def strip_named_js_function(source, name):
    marker = '\n  function ' + name + '('
    start = source.find(marker)
    if start < 0:
        raise SystemExit(f'function {name} marker not found in release app.js')
    open_offset = source.find('{', start)
    if open_offset < 0:
        raise SystemExit(f'function {name} opening brace not found in release app.js')
    depth = 0
    for index in range(open_offset, len(source)):
        if source[index] == '{':
            depth += 1
        elif source[index] == '}':
            depth -= 1
            if depth == 0:
                end = index + 1
                if end < len(source) and source[end] == '\n':
                    end += 1
                return source[:start] + source[end:]
    raise SystemExit(f'function {name} closing brace not found in release app.js')

def expected_asset_body(path):
    body = (release_static / path).read_bytes()
    if path != 'app.js':
        return body
    source = strip_named_js_function(body.decode('utf-8'), 'resetStateForTest')
    start_marker = '\n  var exported = {};\n  if (typeof module === ' + chr(34) + 'object' + chr(34) + ' && module.exports) {'
    end_marker = '\n  return exported;\n});'
    start = source.find(start_marker)
    end = source.rfind(end_marker)
    if start < 0 or end < 0 or end <= start:
        raise SystemExit('satiksme app test harness markers not found in release app.js')
    return (source[:start] + '\n  return {};\n});\n').encode('utf-8')

def expected_asset_hash(path):
    return hashlib.sha256(expected_asset_body(path)).hexdigest()

def served_asset_hash(path):
    req = urllib.request.Request(root + '/assets/' + path, headers={'User-Agent': 'curl/8.0'})
    with urllib.request.urlopen(req, timeout=10) as response:
        if response.status != 200:
            raise SystemExit(f'asset {path} status {response.status}')
        for header in {k.lower(): v for k, v in response.headers.items()}:
            if header.startswith('x-satiksme-bot-'):
                raise SystemExit(f'/assets/{path} leaked internal Satiksme header: {header}')
        return hashlib.sha256(response.read()).hexdigest()

def assert_no_store(path, headers):
    cache_control = headers.get('cache-control', '').lower()
    cdn_cache_control = headers.get('cdn-cache-control', '').lower()
    if 'no-store' not in cache_control:
        raise SystemExit(f'{path} missing no-store Cache-Control: {cache_control}')
    if 'no-store' not in cdn_cache_control:
        raise SystemExit(f'{path} missing no-store CDN-Cache-Control: {cdn_cache_control}')

def assert_no_satiksme_headers(path, headers):
    for header in headers:
        if header.startswith('x-satiksme-bot-'):
            raise SystemExit(f'{path} leaked internal Satiksme header: {header}')

def assert_no_cors(path, headers):
    for header in ['access-control-allow-origin', 'access-control-allow-methods', 'access-control-allow-headers']:
        if headers.get(header):
            raise SystemExit(f'{path} unexpectedly sets {header}: {headers.get(header)}')

def assert_noindex(path, headers):
    if headers.get('x-robots-tag') != 'noindex, noarchive':
        raise SystemExit(f'{path} unexpected X-Robots-Tag: {headers.get(\"x-robots-tag\")}')

def assert_no_preview_metadata(path, body):
    lower = body.lower()
    for needle in ['<meta property=\"og:', \"<meta property='og:\", '<meta name=\"twitter:', \"<meta name='twitter:\", '<meta name=\"description\"', \"<meta name='description'\"]:
        if needle in lower:
            raise SystemExit(f'public shell exposes preview metadata {needle}: {path}')

def assert_security_headers(path, headers):
    for header in [
        'strict-transport-security',
        'content-security-policy',
        'x-frame-options',
        'x-content-type-options',
        'referrer-policy',
        'permissions-policy',
    ]:
        if not headers.get(header):
            raise SystemExit(f'{path} missing security header {header}')
    if headers.get('strict-transport-security') != 'max-age=31536000':
        raise SystemExit(f'{path} unexpected HSTS header: {headers.get(\"strict-transport-security\")}')

def assert_vary_accept_encoding(path, headers):
    vary = headers.get('vary', '')
    values = {part.strip().lower() for part in vary.split(',')}
    if 'accept-encoding' not in values:
        # Cloudflare can keep stale response headers for immutable asset hashes even
        # after the origin fixed Vary; don't roll back functional deploys on that.
        return

def assert_unversioned_asset_range_not_partial(path):
    status, range_headers, _ = request(path, headers={'Range': 'bytes=0-63'})
    if status != 200:
        raise SystemExit(f'{path} range request returned {status}, want 200')
    assert_no_store(path + ' range', range_headers)
    assert_noindex(path + ' range', range_headers)
    assert_no_satiksme_headers(path + ' range', range_headers)
    assert_security_headers(path + ' range', range_headers)
    assert_vary_accept_encoding(path + ' range', range_headers)
    if range_headers.get('content-range'):
        raise SystemExit(f'{path} range request returned Content-Range: {range_headers.get(\"content-range\")}')

def assert_immutable_public_asset_cache(path, headers):
    for header in ['cache-control', 'cdn-cache-control']:
        value = headers.get(header, '').lower()
        if 'immutable' not in value or 'max-age=31536000' not in value:
            raise SystemExit(f'{path} missing immutable public asset cache in {header}: {value}')
    assert_vary_accept_encoding(path, headers)

def non_current_asset_hash(expected):
    expected = str(expected).strip()
    if not expected:
        return '0' * 64
    prefix = '0' if expected[0] != '0' else '1'
    return prefix + expected[1:]

def assert_public_json_cache_not_long_immutable(path, headers):
    assert_vary_accept_encoding(path, headers)
    value = headers.get('cache-control', '')
    if 'immutable' in value.lower():
        raise SystemExit(f'{path} public JSON cache is immutable: {value}')
    if 'no-store' in value.lower():
        return
    match = re.search(r'max-age=(\d+)', value)
    if not match:
        raise SystemExit(f'{path} public JSON cache missing max-age/no-store: {value}')
    if int(match.group(1)) > 60:
        raise SystemExit(f'{path} public JSON max-age too large: {value}')

def frame_ancestors_directives(csp):
    directives = []
    for policy in re.split(r'[,\n]+', csp):
        for directive in policy.split(';'):
            parts = directive.strip().split()
            if parts and parts[0].lower() == 'frame-ancestors':
                directives.append(parts[1:])
    return directives

def assert_shell_framing(path, headers, allow_telegram_webapp):
    csp = headers.get('content-security-policy', '')
    x_frame_options = headers.get('x-frame-options', '')
    frame_ancestors = frame_ancestors_directives(csp)
    telegram_origin = 'https://web.telegram.org'
    none_source = chr(39) + 'none' + chr(39)
    if allow_telegram_webapp:
        if x_frame_options:
            raise SystemExit(f'{path} blocks Telegram Web with X-Frame-Options: {x_frame_options}')
        if not frame_ancestors:
            raise SystemExit(f'{path} CSP does not allow Telegram Web framing: {csp}')
        for sources in frame_ancestors:
            if telegram_origin not in sources or none_source in sources:
                raise SystemExit(f'{path} has a conflicting frame-ancestors policy: {csp}')
        return
    if x_frame_options.lower() != 'deny':
        raise SystemExit(f'{path} unexpected X-Frame-Options: {x_frame_options}')
    if [none_source] not in frame_ancestors:
        raise SystemExit(f'{path} CSP does not deny framing: {csp}')

def walk_json(value, callback, trail='$'):
    if isinstance(value, dict):
        for key, child in value.items():
            callback(trail, key, child)
            walk_json(child, callback, f'{trail}.{key}')
    elif isinstance(value, list):
        for index, child in enumerate(value):
            walk_json(child, callback, f'{trail}[{index}]')

def assert_satiksme_public_json(path, payload):
    def check(trail, key, value):
        if key in ('liveId', 'nearbyStopIds', 'liveRowId', 'scopeKey') and value not in ('', None, [], {}):
            raise SystemExit(f'{path} exposes {key} at {trail}: {value!r}')
        if key in ('updatedAt', 'generatedAt', 'createdAt', 'reportedAt', 'lastReportAt', 'publishedAt') and isinstance(value, str) and re.search(r'T\\d{2}:\\d{2}:\\d{2}\\.\\d+', value):
            raise SystemExit(f'{path} exposes subsecond timestamp {key} at {trail}: {value!r}')
    walk_json(payload, check)
    for index, vehicle in enumerate(payload.get('liveVehicles', []) if isinstance(payload, dict) else []):
        if not isinstance(vehicle, dict):
            continue
        vehicle_id = str(vehicle.get('id', '')).strip()
        if vehicle_id.count(':') >= 2:
            raise SystemExit(f'{path} liveVehicles[{index}].id looks like a raw feed id: {vehicle_id!r}')

def assert_satiksme_map_area_ids(path, payload):
    if not isinstance(payload, dict):
        raise SystemExit(f'{path} payload is malformed')
    area_incidents = payload.get('areaIncidents')
    sightings = payload.get('sightings')
    if not isinstance(area_incidents, list) or not isinstance(sightings, dict):
        raise SystemExit(f'{path} is missing its area collections')
    area_reports = sightings.get('areaReports', [])
    if not isinstance(area_reports, list):
        raise SystemExit(f'{path} is missing its area report list')
    if len(area_reports) >= 500:
        raise SystemExit(f'{path} reached the area validation limit and may be truncated')
    for incident in area_incidents:
        if not isinstance(incident, dict) or not re.fullmatch(r'area:pub-[0-9a-f]{8}', str(incident.get('id', '')).strip()):
            raise SystemExit(f'{path} contains a non-opaque area incident id')
    for report in area_reports:
        if not isinstance(report, dict) or not re.fullmatch(r'area:pub-[0-9a-f]{8}', str(report.get('incidentId', '')).strip()):
            raise SystemExit(f'{path} contains a non-opaque area sighting incident id')

def assert_satiksme_sighting_ids(path, payload):
    if not isinstance(payload, dict):
        raise SystemExit(f'{path} sightings payload is malformed')
    sightings = payload.get('sightings') if isinstance(payload.get('sightings'), dict) else payload
    stop_rows = sightings.get('stopSightings')
    vehicle_rows = sightings.get('vehicleSightings')
    area_rows = sightings.get('areaReports', [])
    if not isinstance(stop_rows, list) or not isinstance(vehicle_rows, list) or not isinstance(area_rows, list):
        raise SystemExit(f'{path} is missing its sighting collections')
    for row in stop_rows:
        if not isinstance(row, dict) or not re.fullmatch(r'stop-report:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
            raise SystemExit(f'{path} contains a non-opaque stop sighting id')
    for row in vehicle_rows:
        if not isinstance(row, dict):
            raise SystemExit(f'{path} contains a malformed vehicle sighting')
        if not re.fullmatch(r'vehicle-report:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
            raise SystemExit(f'{path} contains a non-opaque vehicle sighting id')
    for row in area_rows:
        if not isinstance(row, dict) or not re.fullmatch(r'area-report:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
            raise SystemExit(f'{path} contains a non-opaque area sighting id')

def assert_shell_route(path, expected_mode, expect_leaflet=True, expect_telegram_webapp=False):
    status, route_headers, route_body = request(path)
    if status != 200:
        raise SystemExit(f'{path} shell status {status}')
    assert_no_store(path, route_headers)
    assert_noindex(path, route_headers)
    head_status, head_headers, _ = request(path, method='HEAD')
    if head_status != 200:
        raise SystemExit(f'HEAD {path} shell status {head_status}')
    assert_no_store(path, head_headers)
    assert_noindex(path, head_headers)
    assert_no_satiksme_headers(path, route_headers)
    assert_shell_framing(path, route_headers, expect_telegram_webapp)
    assert_shell_framing('HEAD ' + path, head_headers, expect_telegram_webapp)
    route_csp = route_headers.get('content-security-policy', '')
    if unsafe_inline in route_csp:
        raise SystemExit(f'{path} CSP still allows inline code: {route_csp}')
    if script_nonce not in route_csp:
        raise SystemExit(f'{path} CSP missing script nonce: {route_csp}')
    if style_self not in route_csp:
        raise SystemExit(f'{path} CSP missing strict style-src: {route_csp}')
    if not re.search(r'<script\b[^>]*\bnonce=', route_body):
        raise SystemExit(f'{path} shell is missing script nonce')
    if '<meta name=\"robots\" content=\"noindex, noarchive\">' not in route_body:
        raise SystemExit(f'{path} shell missing robots noindex meta')
    assert_no_preview_metadata(path, route_body)
    if f'\"mode\":\"{expected_mode}\"' not in route_body:
        raise SystemExit(f'{path} shell missing expected mode {expected_mode}')
    for asset in ['app.js', 'app.css']:
        marker = f'/assets/{asset}?v={expected_asset_hash(asset)}'
        if marker not in route_body:
            raise SystemExit(f'{path} shell does not reference release asset hash for {asset}: expected {marker}')
    for asset in ['leaflet/leaflet.js', 'leaflet/leaflet.css']:
        marker = f'/assets/{asset}?v={expected_asset_hash(asset)}'
        if expect_leaflet and marker not in route_body:
            raise SystemExit(f'{path} shell does not reference release asset hash for {asset}: expected {marker}')
        if not expect_leaflet and marker in route_body:
            raise SystemExit(f'{path} incident shell unexpectedly loads Leaflet asset {asset}')
    for needle in ['telegram-login.js']:
        if needle in route_body:
            raise SystemExit(f'{path} shell contains unexpected public script marker: {needle}')
    has_telegram_webapp = 'telegram-web-app.js' in route_body
    if expect_telegram_webapp and not has_telegram_webapp:
        raise SystemExit(f'{path} mini-app shell missing Telegram WebApp script')
    if expect_telegram_webapp and '\"telegramMiniApp\":true' not in route_body:
        raise SystemExit(f'{path} mini-app shell missing Telegram Mini App config flag')
    if not expect_telegram_webapp and has_telegram_webapp:
        raise SystemExit(f'{path} public shell contains Telegram WebApp script')
    if not expect_telegram_webapp and '\"telegramMiniApp\"' in route_body:
        raise SystemExit(f'{path} public shell exposes Telegram Mini App config flag')
    for needle in ['\"spacetimeHost\"', '\"spacetimeDatabase\"', '/assets/live-client.js', 'maincloud.spacetimedb.com']:
        if needle in route_body:
            raise SystemExit(f'{path} shell exposes browser-direct Spacetime config: {needle}')
    if '\"liveTransportViewerHeartbeatEnabled\":true' not in route_body:
        raise SystemExit(f'{path} shell missing public live viewer heartbeat writes')

status, headers, _ = request('/')
if status != 200:
    raise SystemExit(f'root status {status}')
assert_no_store('/', headers)
assert_noindex('/', headers)
assert_security_headers('/', headers)
assert_no_satiksme_headers('/', headers)
head_status, head_headers, _ = request('/', method='HEAD')
if head_status != 200:
    raise SystemExit(f'HEAD / returned {head_status}, want 200')
assert_no_store('/ HEAD', head_headers)
assert_noindex('/ HEAD', head_headers)
assert_security_headers('/ HEAD', head_headers)
assert_no_satiksme_headers('/ HEAD', head_headers)
csp = headers.get('content-security-policy', '')
unsafe_inline = chr(39) + 'unsafe-inline' + chr(39)
script_nonce = 'script-src ' + chr(39) + 'self' + chr(39) + ' ' + chr(39) + 'nonce-'
style_self = 'style-src ' + chr(39) + 'self' + chr(39)
if unsafe_inline in csp:
    raise SystemExit(f'CSP still allows inline code: {csp}')
if script_nonce not in csp:
    raise SystemExit(f'CSP missing script nonce: {csp}')
if style_self not in csp:
    raise SystemExit(f'CSP missing strict style-src: {csp}')

status, _, body = request('/')
if status != 200:
    raise SystemExit(f'root status changed during asset check: {status}')
if '<meta name=\"robots\" content=\"noindex, noarchive\">' not in body:
    raise SystemExit('root shell missing robots noindex meta')
assert_no_preview_metadata('/', body)
for needle in ['\"spacetimeHost\"', '\"spacetimeDatabase\"', '/assets/live-client.js', 'maincloud.spacetimedb.com']:
    if needle in body:
        raise SystemExit(f'root shell exposes browser-direct Spacetime config: {needle}')
for needle in ['telegram-login.js', 'telegram-web-app.js']:
    if needle in body:
        raise SystemExit(f'root shell contains unexpected public script marker: {needle}')
for asset in ['app.js', 'app.css', 'leaflet/leaflet.js', 'leaflet/leaflet.css']:
    expected = expected_asset_hash(asset)
    marker = f'/assets/{asset}?v={expected}'
    if marker not in body:
        raise SystemExit(f'root shell does not reference release asset hash for {asset}: expected {marker}')
    actual = served_asset_hash(asset)
    if actual != expected:
        raise SystemExit(f'public asset {asset} hash {actual} does not match release hash {expected}')
    status, asset_headers, _ = request(f'/assets/{asset}?v={expected}')
    if status != 200:
        raise SystemExit(f'versioned asset {asset} status {status}')
    assert_no_satiksme_headers(f'/assets/{asset}?v={expected}', asset_headers)
    assert_security_headers(f'/assets/{asset}?v={expected}', asset_headers)
    assert_noindex(f'/assets/{asset}?v={expected}', asset_headers)
    assert_vary_accept_encoding(f'/assets/{asset}?v={expected}', asset_headers)
    assert_immutable_public_asset_cache(f'/assets/{asset}?v={expected}', asset_headers)

for path in ['/assets/app.js', '/assets/app.css', '/assets/leaflet/leaflet.js', '/assets/leaflet/leaflet.css']:
    assert_unversioned_asset_range_not_partial(path)

known_stale_query_assets = {
    'app.js': [
        '69ddc87459d415b408883c0c3bb7ff7b3f2e22908ac22d49eb63afdde4610130',
        'f3a074c862bb6b3615a67b892e4de1d2c8cec5875bba505c082afb8ed19160ad',
    ],
    'app.css': [
        'ab16173027320d77bea9d20493eb2184ba371c1cee5e52110a580802589ac1e2',
    ],
}
for asset, stale_hashes in known_stale_query_assets.items():
    expected = expected_asset_hash(asset)
    for stale_hash in [non_current_asset_hash(expected), *stale_hashes]:
        if stale_hash == expected:
            continue
        path = f'/assets/{asset}?v={stale_hash}'
        status, stale_headers, _ = request(path)
        if status == 200:
            if stale_headers.get('cf-cache-status', '').lower() == 'hit':
                continue
            raise SystemExit(f'stale query-versioned asset remained public: {path}')
        if status not in (404, 410):
            raise SystemExit(f'stale query-versioned asset {path} returned {status}, want 404 or 410')
        assert_no_store(path, stale_headers)
        assert_noindex(path, stale_headers)

status, robots_headers, robots_body = request('/robots.txt')
if status != 200:
    raise SystemExit(f'robots.txt returned {status}, want 200')
robots_head_status, robots_head_headers, _ = request('/robots.txt', method='HEAD')
if robots_head_status != 200:
    raise SystemExit(f'HEAD /robots.txt returned {robots_head_status}, want 200')
lower_robots = robots_body.lower()
if 'begin cloudflare managed content' in lower_robots:
    if 'user-agent:' not in lower_robots or 'content-signal:' not in lower_robots or 'ai-train=no' not in lower_robots:
        raise SystemExit(f'Cloudflare-managed robots.txt is missing expected content signals: {robots_body[:200]}')
else:
    assert_no_store('/robots.txt', robots_headers)
    assert_noindex('/robots.txt', robots_headers)
    assert_no_store('/robots.txt HEAD', robots_head_headers)
    assert_noindex('/robots.txt HEAD', robots_head_headers)
    if 'user-agent:' not in lower_robots or 'disallow: /' not in lower_robots:
        raise SystemExit(f'robots.txt does not deny indexing: {robots_body[:200]}')

for path, mode, expect_leaflet, expect_telegram_webapp in [
    ('/app', 'public', True, True),
    ('/app?view=incidents', 'public-incidents', False, True),
    ('/incidents', 'public-incidents', False, False),
    ('/-incidents', 'public-incidents', False, False),
]:
    assert_shell_route(path, mode, expect_leaflet, expect_telegram_webapp)

status, _, health_body = request('/api/v1/health')
if status != 200:
    raise SystemExit(f'health status {status}')
health = json.loads(health_body)
for forbidden in ['runtime', 'assets', 'catalog', 'telegram', 'reportDump', 'db', 'web', 'bundle', 'liveSnapshot', 'version']:
    if forbidden in health:
        raise SystemExit(f'health payload leaks diagnostics: {forbidden}')
if 'ok' not in health:
    raise SystemExit(f'health payload missing ok: {health}')

status, config_headers, _ = request('/api/v1/auth/telegram/config')
if status != 200:
    raise SystemExit(f'auth config status {status}')
config_hsts = config_headers.get('strict-transport-security')
if config_hsts != 'max-age=31536000':
    raise SystemExit(f'auth config unexpected HSTS header: {config_hsts}')
login_cookie = ''
for cookie_line in config_headers.get('set-cookie', '').splitlines():
    cookie_pair = cookie_line.split(';', 1)[0]
    if cookie_pair.startswith('satiksme_login_nonce='):
        login_cookie = cookie_pair
        break
if login_cookie:
    status, _, complete_body = request('/api/v1/auth/telegram/complete', method='POST', body='{\"idToken\":\"not.a.jwt\"}', headers={'Cookie': login_cookie, 'Content-Type': 'application/json'})
    if status != 401:
        raise SystemExit(f'malformed Telegram login returned {status}, want 401: {complete_body[:200]}')
    if 'invalid Telegram login' not in complete_body:
        raise SystemExit(f'malformed Telegram login missing generic error: {complete_body[:200]}')
    for leaked in ['decode', 'base64', 'issuer', 'audience', 'signature', 'nonce', 'id_token']:
        if leaked in complete_body:
            raise SystemExit(f'malformed Telegram login leaks validation detail {leaked}: {complete_body[:200]}')

status, _, legacy_complete_body = request('/api/v1/auth/telegram', method='POST', body='{\"initData\":\"invalid\"}', headers={'Content-Type': 'application/json'})
if status != 410:
    raise SystemExit(f'legacy Telegram login returned {status}, want 410: {legacy_complete_body[:200]}')
if '/api/v1/auth/telegram/complete' not in legacy_complete_body:
    raise SystemExit(f'legacy Telegram login does not point at complete endpoint: {legacy_complete_body[:200]}')
for leaked in ['invalid Telegram login', 'too many login attempts', 'missing hash', 'initData']:
    if leaked in legacy_complete_body:
        raise SystemExit(f'legacy malformed Telegram login leaks validation detail {leaked}: {legacy_complete_body[:200]}')

for path in ['/api/v1/me', '/api/v1/incidents/stop%3A3033/votes']:
    status, auth_failure_headers, auth_failure_body = request(path, method='POST' if path.endswith('/votes') else 'GET', body='{}' if path.endswith('/votes') else None)
    if status != 401:
        raise SystemExit(f'unauthenticated {path} returned {status}, want 401: {auth_failure_body[:200]}')
    assert_no_store(path, auth_failure_headers)

status, me_options_headers, me_options_body = request('/api/v1/me', method='OPTIONS')
if status != 405:
    raise SystemExit(f'OPTIONS /api/v1/me returned {status}, want 405: {me_options_body[:200]}')
if me_options_headers.get('allow') != 'GET':
    raise SystemExit(f'OPTIONS /api/v1/me Allow header {me_options_headers.get(\"allow\")!r}, want GET')
assert_no_store('/api/v1/me OPTIONS', me_options_headers)
if 'missing session' in me_options_body:
    raise SystemExit(f'OPTIONS /api/v1/me reached auth before method handling: {me_options_body[:200]}')

method = 'GET'
status, live_viewer_headers, live_viewer_body = request('/api/v1/public/live-viewer', method=method)
assert_no_store('/api/v1/public/live-viewer GET', live_viewer_headers)
if status == 404:
    if live_viewer_headers.get('x-robots-tag') != 'noindex, noarchive':
        raise SystemExit(f'public live viewer heartbeat route missing noindex: {live_viewer_headers.get(\"x-robots-tag\")}')
elif status == 405:
    if live_viewer_headers.get('allow') != 'POST':
        raise SystemExit(f'public live viewer heartbeat GET Allow header {live_viewer_headers.get(\"allow\")!r}, want POST')
    heartbeat_body = '{\"sessionId\":\"deploy-validation\",\"page\":\"public\",\"visible\":false}'
    status, live_viewer_headers, live_viewer_body = request('/api/v1/public/live-viewer', method='POST', body=heartbeat_body, headers={'Content-Type': 'application/json'})
    if status != 200:
        raise SystemExit(f'public live viewer heartbeat POST returned {status}, want 200: {live_viewer_body[:200]}')
    assert_no_store('/api/v1/public/live-viewer POST', live_viewer_headers)
    if '\"ok\":true' not in live_viewer_body:
        raise SystemExit(f'public live viewer heartbeat POST missing ok response: {live_viewer_body[:200]}')
    status, live_viewer_headers, live_viewer_body = request('/api/v1/public/live-viewer', method='OPTIONS')
    if status != 405:
        raise SystemExit(f'public live viewer heartbeat OPTIONS returned {status}, want 405: {live_viewer_body[:200]}')
    assert_no_store('/api/v1/public/live-viewer OPTIONS', live_viewer_headers)
    if live_viewer_headers.get('allow') != 'POST':
        raise SystemExit(f'public live viewer heartbeat OPTIONS Allow header {live_viewer_headers.get(\"allow\")!r}, want POST')
else:
    raise SystemExit(f'public live viewer heartbeat route is enabled for {method}: returned {status}, want disabled 404 or enabled 405: {live_viewer_body[:200]}')

status, oidc_headers, oidc_body = request('/oidc/.well-known/openid-configuration')
if status != 200:
    raise SystemExit(f'oidc discovery status {status}')
assert_no_store('/oidc/.well-known/openid-configuration', oidc_headers)
assert_no_satiksme_headers('/oidc/.well-known/openid-configuration', oidc_headers)
claims_supported = json.loads(oidc_body).get('claims_supported') or []
if 'smoke' in claims_supported:
    raise SystemExit(f'oidc discovery exposes internal smoke claim: {claims_supported}')

for path in ['/assets/app.test.js', '/assets/app.js.map', '/assets/live-client.js']:
    status, asset_missing_headers, _ = request(path)
    if status == 200:
        raise SystemExit(f'test-only or browser-direct asset is public: {path}')
    if status in (404, 410):
        assert_no_store(path, asset_missing_headers)
        assert_noindex(path, asset_missing_headers)

for path in [
    '/.well-known/security.txt',
    '/sitemap.xml',
    '/service-worker.js',
    '/manifest.json',
    '/favicon.ico',
    '/site.webmanifest',
    '/apple-touch-icon.png',
    '/apple-touch-icon-precomposed.png',
    '/assets/app.js/',
    '/bundles/active.json/',
    '/transport/live/active.json/',
    '/__outside-audit-404',
    '/deploy-validation-missing-path',
]:
    status, missing_headers, _ = request(path)
    if status != 404:
        raise SystemExit(f'{path} returned {status}, want 404')
    assert_no_store(path, missing_headers)
    assert_noindex(path, missing_headers)

status, missing_bundle_headers, _ = request('/bundles/no-such-version/manifest.json')
if status != 404:
    raise SystemExit(f'missing public bundle status {status}, want 404')
assert_no_store('/bundles/no-such-version/manifest.json', missing_bundle_headers)
assert_noindex('/bundles/no-such-version/manifest.json', missing_bundle_headers)

status, _, app_js = request('/assets/app.js')
if status != 200:
    raise SystemExit(f'app.js status {status}')
for needle in ['__test__', '\"__\" + \"test__\"', 'resetStateForTest']:
    if needle in app_js:
        raise SystemExit(f'production bundle exposes test harness marker: {needle}')
for path in ['/assets/app.js', '/assets/live-client.js', '/assets/leaflet/leaflet.js']:
    status, _, js_body = request(path)
    if status == 200 and 'sourceMappingURL=' in js_body:
        raise SystemExit(f'production JavaScript references a source map that is not served: {path}')

status, catalog_headers, catalog_body = request('/api/v1/public/catalog')
if status != 200:
    raise SystemExit(f'public catalog status {status}')
assert_no_satiksme_headers('/api/v1/public/catalog', catalog_headers)
assert_noindex('/api/v1/public/catalog', catalog_headers)
assert_public_json_cache_not_long_immutable('/api/v1/public/catalog', catalog_headers)
catalog_payload = json.loads(catalog_body)
assert_satiksme_public_json('/api/v1/public/catalog', catalog_payload)

for path in ['/api/v1/public/live-vehicles?limit=1', '/api/v1/public/map-live?limit=500', '/api/v1/public/map?limit=500']:
    status, public_headers, public_body = request(path)
    if status != 200:
        raise SystemExit(f'{path} status {status}: {public_body[:200]}')
    assert_no_satiksme_headers(path, public_headers)
    assert_noindex(path, public_headers)
    assert_public_json_cache_not_long_immutable(path, public_headers)
    public_payload = json.loads(public_body)
    assert_satiksme_public_json(path, public_payload)
    if '/public/map' in path:
        assert_satiksme_map_area_ids(path, public_payload)
        assert_satiksme_sighting_ids(path, public_payload)

sightings_path = '/api/v1/public/sightings?limit=500'
status, sightings_headers, sightings_body = request(sightings_path)
if status != 200:
    raise SystemExit(f'public sightings status {status}: {sightings_body[:200]}')
assert_no_satiksme_headers(sightings_path, sightings_headers)
assert_noindex(sightings_path, sightings_headers)
assert_public_json_cache_not_long_immutable(sightings_path, sightings_headers)
sightings_payload = json.loads(sightings_body)
assert_satiksme_public_json(sightings_path, sightings_payload)
assert_satiksme_sighting_ids(sightings_path, sightings_payload)
area_reports = sightings_payload.get('areaReports', []) if isinstance(sightings_payload, dict) else None
if not isinstance(area_reports, list):
    raise SystemExit('public sightings is missing the area report list')
if len(area_reports) >= 500:
    raise SystemExit('public sightings reached the validation limit and may be truncated')
for area_report in area_reports:
    if not isinstance(area_report, dict):
        raise SystemExit('public area sighting row is malformed')
    current_id = str(area_report.get('incidentId', '')).strip()
    if not re.fullmatch(r'area:pub-[0-9a-f]{8}', current_id):
        raise SystemExit('public area sighting incident id is not opaque')

status, incidents_headers, incidents_body = request('/api/v1/public/incidents')
if status != 200:
    raise SystemExit(f'public incidents status {status}: {incidents_body[:200]}')
assert_no_satiksme_headers('/api/v1/public/incidents', incidents_headers)
assert_noindex('/api/v1/public/incidents', incidents_headers)
assert_public_json_cache_not_long_immutable('/api/v1/public/incidents', incidents_headers)
incidents_payload = json.loads(incidents_body)
assert_satiksme_public_json('/api/v1/public/incidents', incidents_payload)
incident_rows = incidents_payload.get('incidents') if isinstance(incidents_payload, dict) else None
if not isinstance(incident_rows, list):
    raise SystemExit('public incidents is missing the incident list')
incident_ids = []
for incident in incident_rows:
    if not isinstance(incident, dict):
        raise SystemExit('public incident row is malformed')
    current_id = str(incident.get('id', '')).strip()
    if not current_id:
        raise SystemExit('public incident row is missing its id')
    if str(incident.get('scope', '')).strip() == 'area' or current_id.startswith('area:'):
        if not re.fullmatch(r'area:pub-[0-9a-f]{8}', current_id):
            raise SystemExit('public area incident id is not opaque')
    if str(incident.get('scope', '')).strip() == 'vehicle' or current_id.startswith('vehicle:'):
        if not re.fullmatch(r'vehicle:pub-[0-9a-f]{8}', current_id):
            raise SystemExit('public vehicle incident id is not opaque')
    incident_ids.append(current_id)
for incident_index, incident_id in enumerate(incident_ids):
    detail_path = '/api/v1/public/incidents/' + urllib.parse.quote(incident_id, safe='')
    status, detail_headers, detail_body = request(detail_path)
    if status != 200:
        raise SystemExit(f'public incident detail {detail_path} status {status}: {detail_body[:200]}')
    assert_no_satiksme_headers(detail_path, detail_headers)
    assert_noindex(detail_path, detail_headers)
    assert_public_json_cache_not_long_immutable(detail_path, detail_headers)
    detail_payload = json.loads(detail_body)
    assert_satiksme_public_json(detail_path, detail_payload)
    detail_summary = detail_payload.get('summary') if isinstance(detail_payload, dict) else None
    if not isinstance(detail_summary, dict):
        raise SystemExit('public incident detail is missing its summary')
    detail_id = str(detail_summary.get('id', '')).strip()
    if detail_id != incident_id:
        raise SystemExit('public incident detail id does not match its list id')
    if str(detail_summary.get('scope', '')).strip() == 'area' or incident_id.startswith('area:'):
        if not re.fullmatch(r'area:pub-[0-9a-f]{8}', detail_id):
            raise SystemExit('public area incident detail id is not opaque')
    if str(detail_summary.get('scope', '')).strip() == 'vehicle' or incident_id.startswith('vehicle:'):
        if not re.fullmatch(r'vehicle:pub-[0-9a-f]{8}', detail_id):
            raise SystemExit('public vehicle incident detail id is not opaque')
    for event in detail_payload.get('events', []) if isinstance(detail_payload, dict) else []:
        if not isinstance(event, dict):
            continue
        event_id = str(event.get('id', '')).strip()
        if event_id and not re.fullmatch(r'incident-event:pub-[0-9a-f]{8}', event_id):
            raise SystemExit(f'public incident detail event id is not opaque: {event_id!r}')
        for raw_marker in ['channel:', 'stop:', 'vehicle:', 'area:', 'liveRowId', 'scopeKey']:
            if raw_marker in event_id:
                raise SystemExit(f'public incident detail event id exposes raw marker {raw_marker}: {event_id!r}')
    for comment in detail_payload.get('comments', []) if isinstance(detail_payload, dict) else []:
        if not isinstance(comment, dict):
            raise SystemExit('public incident detail contains a malformed comment')
        if not re.fullmatch(r'incident-comment:pub-[0-9a-f]{8}', str(comment.get('id', '')).strip()):
            raise SystemExit('public incident detail comment id is not opaque')
        if str(comment.get('nickname', '')).strip() != 'anonīmi':
            raise SystemExit('public incident detail comment exposes a reporter identity')
    if incident_index == 0:
        status, _, _ = request(detail_path, method='HEAD')
        if status != 200:
            raise SystemExit(f'HEAD {detail_path} returned {status}, want 200')
        for method in ['POST', 'OPTIONS']:
            status, detail_method_headers, detail_method_body = request(detail_path, method=method, body='' if method == 'POST' else None)
            if status != 405:
                raise SystemExit(f'{method} {detail_path} returned {status}, want 405: {detail_method_body[:200]}')
            allow = detail_method_headers.get('allow')
            if allow != 'GET, HEAD':
                raise SystemExit(f'{method} {detail_path} Allow header {allow!r}, want GET, HEAD')
            assert_no_cors(detail_path, detail_method_headers)

status, bundle_headers, bundle_body = request('/bundles/active.json')
if status != 200:
    raise SystemExit(f'active public bundle status {status}')
assert_no_store('/bundles/active.json', bundle_headers)
assert_no_satiksme_headers('/bundles/active.json', bundle_headers)
if bundle_headers.get('x-robots-tag') != 'noindex, noarchive':
    raise SystemExit(f'active public bundle unexpected X-Robots-Tag: {bundle_headers.get(\"x-robots-tag\")}')
active_bundle = json.loads(bundle_body)
bundle_version = str(active_bundle.get('version', '')).strip()
manifest_path = str(active_bundle.get('manifestPath', '')).strip().lstrip('/')
if not bundle_version or not manifest_path.startswith('bundles/'):
    raise SystemExit(f'active public bundle has invalid version/path: {active_bundle}')
status, bundle_query_headers, _ = request('/bundles/active.json?cache=split')
if status != 404:
    raise SystemExit(f'active public bundle query variant status {status}, want 404')
assert_no_store('/bundles/active.json?cache=split', bundle_query_headers)
status, manifest_headers, manifest_body = request('/' + manifest_path)
if status != 200:
    raise SystemExit(f'active public bundle manifest {manifest_path} status {status}')
assert_no_satiksme_headers('/' + manifest_path, manifest_headers)
assert_noindex('/' + manifest_path, manifest_headers)
assert_immutable_public_asset_cache('/' + manifest_path, manifest_headers)
status, manifest_alias_headers, manifest_alias_body = request('/' + manifest_path + '/')
if status != 404:
    raise SystemExit(f'active public bundle manifest trailing slash {manifest_path}/ returned {status}, want 404: {manifest_alias_body[:200]}')
assert_no_store('/' + manifest_path + '/', manifest_alias_headers)
assert_noindex('/' + manifest_path + '/', manifest_alias_headers)
manifest = json.loads(manifest_body)
if str(manifest.get('version', '')).strip() != bundle_version:
    raise SystemExit(f'active public bundle manifest version mismatch: active={bundle_version} manifest={manifest}')
for slice_name in ['stops', 'routes']:
    slice_path = str((manifest.get('slices') or {}).get(slice_name, '')).strip()
    if not slice_path:
        raise SystemExit(f'active public bundle manifest missing slice {slice_name}: {manifest}')
    bundle_path = '/' + manifest_path.rsplit('/', 1)[0].strip('/') + '/' + slice_path
    status, slice_headers, _ = request(bundle_path, method='HEAD')
    if status != 200:
        raise SystemExit(f'active public bundle slice {bundle_path} status {status}')
    assert_no_satiksme_headers(bundle_path, slice_headers)
    assert_noindex(bundle_path, slice_headers)
    assert_immutable_public_asset_cache(bundle_path, slice_headers)
manifest_dir = '/' + manifest_path.rsplit('/', 1)[0].strip('/') + '/'
status, manifest_dir_headers, manifest_dir_body = request(manifest_dir)
if status != 404:
    raise SystemExit(f'active public bundle directory {manifest_dir} returned {status}, want 404: {manifest_dir_body[:200]}')
assert_no_store(manifest_dir, manifest_dir_headers)
if manifest_dir_headers.get('x-robots-tag') != 'noindex, noarchive':
    raise SystemExit(f'active public bundle directory unexpected X-Robots-Tag: {manifest_dir_headers.get(\"x-robots-tag\")}')

status, snapshot_headers, active_body = request('/transport/live/active.json')
if status != 200:
    raise SystemExit(f'live snapshot active status {status}')
assert_no_store('/transport/live/active.json', snapshot_headers)
assert_no_satiksme_headers('/transport/live/active.json', snapshot_headers)
if snapshot_headers.get('x-robots-tag') != 'noindex, noarchive':
    raise SystemExit(f'live snapshot active unexpected X-Robots-Tag: {snapshot_headers.get(\"x-robots-tag\")}')
active_snapshot = json.loads(active_body)
for forbidden in ['hash', 'publishedAt', 'vehicleCount', 'lastSuccessAt', 'lastAttemptAt', 'status', 'consecutiveFailures']:
    if forbidden in active_snapshot:
        raise SystemExit(f'live snapshot active exposes {forbidden}: {active_snapshot}')
snapshot_path = str(active_snapshot.get('path', '')).strip().lstrip('/')
if not snapshot_path.startswith('transport/live/'):
    raise SystemExit(f'live snapshot active path is not under transport/live: {snapshot_path!r}')
snapshot_url_path = '/' + snapshot_path
status, snapshot_asset_headers, snapshot_asset_body = request(snapshot_url_path)
if status != 200:
    raise SystemExit(f'live snapshot asset {snapshot_url_path} status {status}')
assert_no_store(snapshot_url_path, snapshot_asset_headers)
assert_no_satiksme_headers(snapshot_url_path, snapshot_asset_headers)
if snapshot_asset_headers.get('x-robots-tag') != 'noindex, noarchive':
    raise SystemExit(f'live snapshot asset unexpected X-Robots-Tag: {snapshot_asset_headers.get(\"x-robots-tag\")}')
content_type = snapshot_asset_headers.get('content-type', '').lower().split(';')[0].strip()
if content_type != 'application/json':
    raise SystemExit(f'live snapshot asset unexpected content type: {snapshot_asset_headers.get(\"content-type\")}')
assert_satiksme_public_json(snapshot_url_path, json.loads(snapshot_asset_body))
status, snapshot_alias_headers, snapshot_alias_body = request(snapshot_url_path + '/')
if status != 404:
    raise SystemExit(f'live snapshot trailing slash {snapshot_url_path}/ returned {status}, want 404: {snapshot_alias_body[:200]}')
assert_no_store(snapshot_url_path + '/', snapshot_alias_headers)
assert_noindex(snapshot_url_path + '/', snapshot_alias_headers)
status, snapshot_query_headers, _ = request(snapshot_url_path + '?cache=split')
if status != 404:
    raise SystemExit(f'live snapshot query variant {snapshot_url_path}?cache=split status {status}, want 404')
assert_no_store(snapshot_url_path + '?cache=split', snapshot_query_headers)
if snapshot_query_headers.get('x-robots-tag') != 'noindex, noarchive':
    raise SystemExit(f'live snapshot query variant unexpected X-Robots-Tag: {snapshot_query_headers.get(\"x-robots-tag\")}')

for path in ['/assets/%2e%2e/app.js', '/assets//app.js', '/assets%5capp.js', '/api%2fv1%2fpublic%2fcatalog', '/api%5cv1%5cpublic%5ccatalog']:
    status, _, _ = request(path)
    if status != 400:
        raise SystemExit(f'unsafe path {path} returned {status}, want 400')

for path in ['/', '/assets/app.js']:
    status, method_headers, _ = request(path, method='POST', body='')
    if status != 405:
        raise SystemExit(f'POST {path} returned {status}, want 405')
    assert_no_store(path, method_headers)

for path in ['/api/v1/public/catalog', '/api/v1/public/sightings?limit=1', '/api/v1/public/incidents?limit=1', '/api/v1/public/map?limit=1', '/api/v1/public/map-live?limit=1', '/api/v1/public/live-vehicles?limit=1']:
    status, _, _ = request(path, method='HEAD')
    if status != 200:
        raise SystemExit(f'HEAD {path} returned {status}, want 200')
    for method in ['POST', 'OPTIONS']:
        status, headers, public_body = request(path, method=method, body='' if method == 'POST' else None)
        if status != 405:
            raise SystemExit(f'{method} {path} returned {status}, want 405: {public_body[:200]}')
        allow = headers.get('allow')
        if allow != 'GET, HEAD':
            raise SystemExit(f'{method} {path} Allow header {allow!r}, want GET, HEAD')
        assert_no_store(path, headers)
        assert_no_cors(path, headers)

for query in ['limit=abc', 'limit=-1', 'limit=2001', 'limit=', 'limit=1&limit=999', 'limit=&limit=1']:
    status, headers, invalid_body = request(f'/api/v1/public/incidents?{query}')
    if status != 400:
        raise SystemExit(f'invalid public incident limit {query} returned {status}, want 400: {invalid_body[:200]}')
    assert_no_store(f'/api/v1/public/incidents?{query}', headers)
    assert_no_satiksme_headers(f'/api/v1/public/incidents?{query}', headers)

for path in ['/api/v1/public/sightings', '/api/v1/public/map', '/api/v1/public/map-live', '/api/v1/public/live-vehicles']:
    for query in ['limit=abc', 'limit=-1', 'limit=0', 'limit=501', 'limit=1&limit=2']:
        status, headers, invalid_body = request(f'{path}?{query}')
        if status != 400:
            raise SystemExit(f'invalid public sightings limit {path}?{query} returned {status}, want 400: {invalid_body[:200]}')
        assert_no_store(f'{path}?{query}', headers)
        assert_no_satiksme_headers(f'{path}?{query}', headers)

for path in [
    '/api/v1/public/catalog?cv=bogus',
    '/api/v1/public/catalog?debug=1',
    '/api/v1/public/catalog?CacheVersion=bogus',
    '/api/v1/public/incidents?limit=1&cv=bogus',
    '/api/v1/public/incidents?limit=1&debug=1',
    '/api/v1/public/incidents/stop:3012?debug=1',
    '/api/v1/public/sightings?stopId=3012&stopId=3013',
    '/api/v1/public/sightings?stopId=3012&cacheVersion=bogus',
    '/api/v1/public/map?limit=1&date=2026-05-10',
    '/api/v1/public/map-live?limit=1&date=2026-05-10&cv=bogus',
    '/api/v1/public/live-vehicles?limit=1&cacheVersion=bogus',
]:
    status, headers, invalid_body = request(path)
    if status != 400:
        raise SystemExit(f'unexpected public query {path} returned {status}, want 400: {invalid_body[:200]}')
    assert_no_store(path, headers)
    assert_no_satiksme_headers(path, headers)

for path in ['/oidc/.well-known/openid-configuration', '/oidc/jwks.json']:
    status, oidc_headers, _ = request(path, method='HEAD')
    if status != 200:
        raise SystemExit(f'HEAD {path} returned {status}, want 200')
    assert_no_store(path, oidc_headers)
    assert_no_satiksme_headers(path, oidc_headers)
    status, oidc_method_headers, oidc_method_body = request(path, method='OPTIONS')
    if status != 405:
        raise SystemExit(f'OPTIONS {path} returned {status}, want 405: {oidc_method_body[:200]}')
    assert_no_store(f'OPTIONS {path}', oidc_method_headers)
    assert_no_cors(path, oidc_method_headers)

status, logout_headers, logout_body = request('/api/v1/auth/logout', method='POST', headers={'Origin': 'https://evil.example'})
if status != 403:
    raise SystemExit(f'cross-site logout returned {status}, want 403: {logout_body[:200]}')
assert_no_store('/api/v1/auth/logout cross-site', logout_headers)

status, complete_headers, complete_body = request('/api/v1/auth/telegram/complete', method='POST', body='{\"initData\":\"invalid\"}', headers={'Origin': 'https://evil.example'})
if status != 403:
    raise SystemExit(f'cross-site Telegram completion returned {status}, want 403: {complete_body[:200]}')
assert_no_store('/api/v1/auth/telegram/complete cross-site', complete_headers)

status, logout_headers, logout_body = request('/api/v1/auth/logout', method='POST', headers={'Origin': 'https://${ARBUZAS_TRAIN_BOT_HOSTNAME}'})
if status != 403:
    raise SystemExit(f'sibling-origin logout returned {status}, want 403: {logout_body[:200]}')
assert_no_store('/api/v1/auth/logout sibling-origin', logout_headers)

status, logout_headers, logout_body = request('/api/v1/auth/logout', method='POST', headers={'Sec-Fetch-Site': 'same-site'})
if status != 403:
    raise SystemExit(f'same-site logout returned {status}, want 403: {logout_body[:200]}')
assert_no_store('/api/v1/auth/logout same-site', logout_headers)
PY
wait_until_ok python3 \"\${tmp}\"" \
    satiksme_bot satiksme_tunnel
}

validate_remote_satiksme_anonymous_data_denial() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "satiksme anonymous private live tables are denied" \
    "config_tmp=\$(mktemp)
tmp=\$(mktemp)
trap 'rm -f \"\${config_tmp}\" \"\${tmp}\"' EXIT
wait_until_ok compose exec -T satiksme_bot sh -lc 'printf \"%s\n%s\n\" \"\${SATIKSME_RUNTIME_SPACETIME_HOST:-\${SATIKSME_WEB_SPACETIME_HOST}}\" \"\${SATIKSME_RUNTIME_SPACETIME_DATABASE:-\${SATIKSME_WEB_SPACETIME_DATABASE}}\"' > \"\${config_tmp}\"
cat > \"\${tmp}\" <<'PY'
import json
import math
import os
import re
import urllib.error
import urllib.parse
import urllib.request

with open(os.environ['SATIKSME_SPACETIME_CONFIG_FILE'], 'r', encoding='utf-8') as handle:
    config_lines = [line.strip() for line in handle.read().splitlines()]
if len(config_lines) < 2 or not config_lines[0] or not config_lines[1]:
    raise SystemExit('satiksme_bot container did not expose Spacetime validation config')

spacetime_host = config_lines[0].rstrip('/')
database = urllib.parse.quote(config_lines[1], safe='')

def anonymous_sql(query):
    url = f'{spacetime_host}/v1/database/{database}/sql'
    request = urllib.request.Request(
        url,
        data=query.encode('utf-8'),
        method='POST',
        headers={'Content-Type': 'text/plain', 'User-Agent': 'curl/8.0'},
    )
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            return response.status, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode('utf-8', 'replace')

def anonymous_rows(query, label):
    status, body = anonymous_sql(query)
    if not (200 <= status < 300):
        raise SystemExit(f'anonymous SQL could not inspect {label}: HTTP {status}')
    try:
        statements = json.loads(body)
        rows = []
        for statement in statements:
            elements = statement.get('schema', {}).get('elements', [])
            names = []
            for element in elements:
                raw_name = element.get('name', '') if isinstance(element, dict) else ''
                if isinstance(raw_name, dict):
                    raw_name = raw_name.get('some', raw_name.get('Some', ''))
                names.append(str(raw_name))
            for values in statement.get('rows', []):
                if not isinstance(values, list) or len(values) != len(names):
                    raise ValueError('row shape mismatch')
                rows.append(dict(zip(names, values)))
        return rows
    except (TypeError, ValueError, json.JSONDecodeError):
        raise SystemExit(f'anonymous SQL returned malformed {label} data')

def neutral_compatibility_value(value):
    return value in ('', None, 0, False) or value == [] or value == {}

def assert_no_nonempty_internal_fields(value, label):
    if isinstance(value, dict):
        for key, child in value.items():
            if key in ('liveId', 'nearbyStopIds', 'liveRowId', 'scopeKey') and not neutral_compatibility_value(child):
                raise SystemExit(f'{label} exposes a non-empty internal compatibility field')
            assert_no_nonempty_internal_fields(child, label)
    elif isinstance(value, list):
        for child in value:
            assert_no_nonempty_internal_fields(child, label)

def call(name, args):
    procedure = urllib.parse.quote(name, safe='')
    url = f'{spacetime_host}/v1/database/{database}/call/{procedure}'
    data = json.dumps(args).encode('utf-8')
    request = urllib.request.Request(url, data=data, method='POST', headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            return response.status, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        return error.code, error.read().decode('utf-8', 'replace')

for table in [
    'satiksmebot_live_viewer_state',
    'satiksmebot_reporter_identity',
    'satiksmebot_stop_sighting',
    'satiksmebot_vehicle_sighting',
    'satiksmebot_area_report',
    'satiksmebot_incident_vote',
    'satiksmebot_incident_vote_event',
    'satiksmebot_incident_comment',
    'satiksmebot_report_dump',
    'satiksmebot_report_dedupe',
    'satiksmebot_import_chunk',
    'satiksmebot_chat_analyzer_checkpoint',
    'satiksmebot_chat_analyzer_message',
    'satiksmebot_chat_analyzer_batch',
    'satiksmebot_chat_analyzer_batch_message',
]:
    status, body = anonymous_sql(f'SELECT * FROM {table} WHERE 1 = 0')
    if 200 <= status < 300:
        raise SystemExit(f'anonymous SQL unexpectedly reached private table {table}: {status} {body[:200]}')

heartbeat_rows = anonymous_rows('SELECT * FROM satiksmebot_live_viewer_heartbeat', 'legacy public viewer heartbeat table')
if heartbeat_rows:
    raise SystemExit('legacy public viewer heartbeat table is not empty')

catalog_rows = anonymous_rows('SELECT * FROM satiksmebot_stop_catalog', 'public stop catalog')
for row in catalog_rows:
    if not neutral_compatibility_value(row.get('liveId')) or not neutral_compatibility_value(row.get('nearbyStopIds')):
        raise SystemExit('public stop catalog exposes internal compatibility data')

snapshot_rows = anonymous_rows('SELECT * FROM satiksmebot_public_live_snapshot_state', 'public live snapshot state')
for row in snapshot_rows:
    for field in ['hash', 'publishedAt', 'lastSuccessAt', 'lastAttemptAt', 'status', 'consecutiveFailures', 'vehicleCount']:
        if not neutral_compatibility_value(row.get(field)):
            raise SystemExit('public live snapshot state exposes internal diagnostic data')

stop_rows = anonymous_rows('SELECT * FROM satiksmebot_public_stop_sighting', 'public stop sightings')
for row in stop_rows:
    if not re.fullmatch(r'stop-report:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
        raise SystemExit('direct public stop sighting id is not opaque')

vehicle_rows = anonymous_rows('SELECT * FROM satiksmebot_public_vehicle_sighting', 'public vehicle sightings')
for row in vehicle_rows:
    if not re.fullmatch(r'vehicle-report:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
        raise SystemExit('direct public vehicle sighting id is not opaque')
    if not re.fullmatch(r'vehicle:pub-[0-9a-f]{8}', str(row.get('incidentId', '')).strip()):
        raise SystemExit('direct public vehicle incident id is not opaque')
    assert_no_nonempty_internal_fields(row, 'direct public vehicle sighting')

area_rows = anonymous_rows('SELECT * FROM satiksmebot_public_area_report', 'public area reports')
for row in area_rows:
    if not re.fullmatch(r'area-report:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
        raise SystemExit('direct public area report id is not opaque')
    if not re.fullmatch(r'area:pub-[0-9a-f]{8}', str(row.get('incidentId', '')).strip()):
        raise SystemExit('direct public area incident id is not opaque')
    for field in ['latitude', 'longitude']:
        try:
            coordinate = float(row.get(field))
        except (TypeError, ValueError):
            raise SystemExit('direct public area report coordinate is malformed')
        if not math.isfinite(coordinate) or abs((coordinate * 1000) - round(coordinate * 1000)) > 1e-8:
            raise SystemExit('direct public area report coordinate is too precise')
    if int(row.get('radiusMeters') or 0) < 250:
        raise SystemExit('direct public area report radius is too precise')

incident_rows = anonymous_rows('SELECT * FROM satiksmebot_public_incident', 'public incidents')
for row in incident_rows:
    scope = str(row.get('scope', '')).strip()
    incident_id = str(row.get('id', '')).strip()
    if scope == 'area' and not re.fullmatch(r'area:pub-[0-9a-f]{8}', incident_id):
        raise SystemExit('direct public area incident id is not opaque')
    if scope == 'vehicle' and not re.fullmatch(r'vehicle:pub-[0-9a-f]{8}', incident_id):
        raise SystemExit('direct public vehicle incident id is not opaque')
    if scope in ('area', 'vehicle') and str(row.get('subjectId', '')).strip():
        raise SystemExit('direct public incident exposes an internal subject id')
    if str(row.get('lastReporter', '')).strip() != 'anonīmi':
        raise SystemExit('direct public incident exposes a reporter identity')
    assert_no_nonempty_internal_fields(row, 'direct public incident')

event_rows = anonymous_rows('SELECT * FROM satiksmebot_public_incident_event', 'public incident events')
for row in event_rows:
    if not re.fullmatch(r'incident-event:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
        raise SystemExit('direct public incident event id is not opaque')
    if str(row.get('nickname', '')).strip() != 'anonīmi':
        raise SystemExit('direct public incident event exposes a reporter identity')

comment_rows = anonymous_rows('SELECT * FROM satiksmebot_public_incident_comment', 'public incident comments')
for row in comment_rows:
    if not re.fullmatch(r'incident-comment:pub-[0-9a-f]{8}', str(row.get('id', '')).strip()):
        raise SystemExit('direct public incident comment id is not opaque')
    if str(row.get('nickname', '')).strip() != 'anonīmi':
        raise SystemExit('direct public incident comment exposes a reporter identity')

for name, args in [
    ('satiksmebot_bootstrap_me', []),
    ('satiksmebot_list_recent_reports', ['audit-invalid-no-mutate', 1]),
    ('satiksmebot_submit_stop_report', ['audit-invalid-no-mutate', '', '']),
    ('satiksmebot_submit_vehicle_report', ['audit-invalid-stop', 'bus', '1', '', 'audit', 0, '', '', '']),
    ('satiksmebot_submit_area_report', [56.9, 24.1, 100, 'audit', '', '']),
    ('satiksmebot_vote_incident', ['audit-invalid-no-mutate', 'ONGOING']),
    ('satiksmebot_comment_incident', ['audit-invalid-no-mutate', 'audit']),
    ('satiksmebot_heartbeat_live_viewer', ['audit-invalid-no-mutate', 'public']),
    ('satiksmebot_set_live_viewer_state', ['audit-invalid-no-mutate', 'public', True]),
    ('satiksmebot_service_pending_report_dump_count', []),
]:
    status, body = call(name, args)
    if 200 <= status < 300:
        raise SystemExit(f'anonymous call unexpectedly succeeded: {name} {status} {body[:200]}')
    for forbidden in ['incident not found', 'bundle identity', 'stale bundle', 'accepted', 'deduped', 'lastSeenAt']:
        if forbidden in body:
            raise SystemExit(f'anonymous call reached application logic before auth denial: {name} {status} {body[:200]}')
PY
wait_until_ok_for 240 env SATIKSME_SPACETIME_CONFIG_FILE=\"\${config_tmp}\" python3 \"\${tmp}\"" \
    satiksme_bot satiksme_tunnel
}

validate_remote_public_tls_dns_hardening() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "public TLS and DNS hardening" \
    "wait_until_ok sh -lc '
      set -e
      for host in \"${ARBUZAS_TRAIN_BOT_HOSTNAME}\" \"${ARBUZAS_SATIKSME_BOT_HOSTNAME}\"; do
        for path in / /app /incidents; do
          result=\$(curl -sS -o /dev/null -w \"%{http_code} %{redirect_url}\" \"http://\${host}\${path}\" 2>/dev/null || true)
          case \"\${result}\" in
            \"301 https://\${host}\${path}\"*|\"308 https://\${host}\${path}\"*) ;;
            *) echo \"HTTP did not redirect to HTTPS for \${host}\${path}: \${result}\" >&2; exit 1 ;;
          esac
        done
        if printf \"\" | timeout 10 openssl s_client -tls1 -servername \"\${host}\" -connect \"\${host}:443\" >/dev/null 2>&1; then
          echo \"TLS 1.0 unexpectedly accepted for \${host}\" >&2
          exit 1
        fi
        if printf \"\" | timeout 10 openssl s_client -tls1_1 -servername \"\${host}\" -connect \"\${host}:443\" >/dev/null 2>&1; then
          echo \"TLS 1.1 unexpectedly accepted for \${host}\" >&2
          exit 1
        fi
        printf \"\" | timeout 10 openssl s_client -tls1_2 -servername \"\${host}\" -connect \"\${host}:443\" >/dev/null 2>&1
        printf \"\" | timeout 10 openssl s_client -tls1_3 -servername \"\${host}\" -connect \"\${host}:443\" >/dev/null 2>&1
        curl -fsS -D - -o /dev/null \"https://\${host}/api/v1/health\" | tr -d \"\\r\" | grep -Fi \"strict-transport-security:\" >/dev/null
      done
      dig +short CAA kontrole.info | grep -E \".+\" >/dev/null
    '" \
    train_bot train_tunnel satiksme_bot satiksme_tunnel
}

validate_remote_train_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" train_bot train_tunnel
  validate_remote_probe "${remote_release_dir}" "train local health" \
    "wait_until_ok compose exec -T train_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TRAIN_BOT_PORT}/api/v1/health >/dev/null 2>/dev/null'" \
    train_bot train_tunnel
  validate_remote_release_identity "${remote_release_dir}" train_bot "${ARBUZAS_TRAIN_BOT_PORT}"
  validate_remote_train_dependency_dns "${remote_release_dir}"
  validate_remote_probe "${remote_release_dir}" "train public health" \
    "wait_until_ok sh -lc 'curl -fsS https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/api/v1/health >/dev/null 2>/dev/null'" \
    train_bot train_tunnel
  validate_remote_probe "${remote_release_dir}" "train Telegram Mini App embeds in Telegram Web" \
    "wait_until_ok sh -lc 'headers=\$(curl -fsSI https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/app | tr -d \"\\r\"); printf \"%s\\n\" \"\${headers}\" | grep -Fi \"content-security-policy:\" | grep -F \"frame-ancestors https://web.telegram.org\" >/dev/null && ! printf \"%s\\n\" \"\${headers}\" | grep -Fi \"x-frame-options:\" >/dev/null'" \
    train_bot train_tunnel
  validate_remote_probe "${remote_release_dir}" "train public OIDC metadata" \
    "wait_until_ok sh -lc 'body=\$(curl -fsS https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/oidc/.well-known/openid-configuration) && printf %s \"\${body}\" | grep -F \"\\\"issuer\\\":\\\"https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/oidc\\\"\" >/dev/null && printf %s \"\${body}\" | grep -F \"\\\"jwks_uri\\\":\\\"https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/oidc/jwks.json\\\"\" >/dev/null'" \
    train_bot train_tunnel
  validate_remote_probe "${remote_release_dir}" "train public dashboard feed" \
    "wait_until_ok sh -lc 'curl -fsS https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/api/v1/public/dashboard?limit=3 >/dev/null 2>/dev/null'" \
    train_bot train_tunnel
  validate_remote_train_public_hardening "${remote_release_dir}"
  validate_remote_train_anonymous_data_denial "${remote_release_dir}"
  validate_remote_public_tls_dns_hardening "${remote_release_dir}"
}

validate_remote_train_dependency_dns() {
  local remote_release_dir="$1"

  log "Validate: train dependency DNS"
  if remote_compose_shell "${remote_release_dir}" "
    deadline=\$((SECONDS + 120))
    while (( SECONDS < deadline )); do
      if compose exec -T train_bot sh -lc '
        getent hosts maincloud.spacetimedb.com >/dev/null 2>/dev/null &&
        getent hosts api.telegram.org >/dev/null 2>/dev/null
      '; then
        exit 0
      fi
      sleep 5
    done
    exit 1
  "; then
    return 0
  fi

  log "Validation failed: train dependency DNS"
  remote_compose_shell "${remote_release_dir}" "
    cid=\$(compose ps -q train_bot 2>/dev/null || true)
    if [[ -z \"\${cid}\" ]]; then
      echo 'train_bot container not found for DNS diagnostics' >&2
      exit 0
    fi

    echo '--- train_bot /etc/resolv.conf ---' >&2
    docker exec \"\${cid}\" cat /etc/resolv.conf >&2 || true

    echo '--- train_bot docker networks ---' >&2
    docker inspect --format '{{range \$name, \$network := .NetworkSettings.Networks}}{{printf \"%s\\n\" \$name}}{{end}}' \"\${cid}\" >&2 || true

    echo '--- train_bot DNS lookup: maincloud.spacetimedb.com ---' >&2
    docker exec \"\${cid}\" sh -lc 'getent hosts maincloud.spacetimedb.com' >&2 || true

    echo '--- train_bot DNS lookup: api.telegram.org ---' >&2
    docker exec \"\${cid}\" sh -lc 'getent hosts api.telegram.org' >&2 || true
  " || true
  collect_remote_validation_diagnostics "${remote_release_dir}" train_bot train_tunnel
  exit 1
}

validate_remote_release_identity() {
  local remote_release_dir="$1"
  local service_name="$2"
  local service_port="$3"
  local script

  read -r -d '' script <<REMOTE || true
    set -euo pipefail
    # shellcheck disable=SC1091
    . '${remote_release_dir}/release.env'
    if [[ -z "\${ARBUZAS_RELEASE_SOURCE_COMMIT:-}" ||
          -z "\${ARBUZAS_RELEASE_SOURCE_DIRTY:-}" ||
          -z "\${ARBUZAS_RELEASE_SOURCE_SHA256:-}" ]]; then
      echo 'legacy release identity proof skipped for ${service_name}: release.env has no source identity fields'
      exit 0
    fi
    if [[ '${VALIDATION_PROFILE}' == 'full' && "\${ARBUZAS_RELEASE_SOURCE_DIRTY}" != 'clean' ]]; then
      echo 'full validation refuses a dirty or unknown release snapshot' >&2
      exit 1
    fi

    tmp=\$(mktemp)
    trap 'rm -f "\${tmp}"' EXIT
    deadline=\$((SECONDS + 90))
    while :; do
      if compose exec -T ${service_name} sh -lc 'curl -fsS http://127.0.0.1:${service_port}/api/v1/internal/health' >"\${tmp}" 2>/dev/null; then
        break
      fi
      if (( SECONDS >= deadline )); then
        echo 'unable to read ${service_name} local internal health for release identity proof' >&2
        exit 1
      fi
      sleep 5
    done

    EXPECTED_RELEASE_ID="\${ARBUZAS_RELEASE_ID}" \
    EXPECTED_RELEASE_SOURCE_COMMIT="\${ARBUZAS_RELEASE_SOURCE_COMMIT}" \
    EXPECTED_RELEASE_SOURCE_DIRTY="\${ARBUZAS_RELEASE_SOURCE_DIRTY}" \
    EXPECTED_RELEASE_SOURCE_SHA256="\${ARBUZAS_RELEASE_SOURCE_SHA256}" \
      python3 - "\${tmp}" <<'PY'
import json
import os
import sys

with open(sys.argv[1], encoding='utf-8') as handle:
    payload = json.load(handle)

version = payload.get('version')
if not isinstance(version, dict):
    raise SystemExit('internal health is missing version object')

expected = {
    'releaseId': os.environ['EXPECTED_RELEASE_ID'],
    'commit': os.environ['EXPECTED_RELEASE_SOURCE_COMMIT'],
    'dirty': os.environ['EXPECTED_RELEASE_SOURCE_DIRTY'],
    'sourceSha256': os.environ['EXPECTED_RELEASE_SOURCE_SHA256'],
}
for key, want in expected.items():
    got = str(version.get(key, ''))
    if got != want:
        raise SystemExit('version.%s=%r, expected %r' % (key, got, want))
PY
REMOTE
  validate_remote_probe "${remote_release_dir}" "${service_name} release identity proof" "${script}" "${service_name}"
}

validate_remote_satiksme_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme local health" \
    "wait_until_ok compose exec -T satiksme_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_SATIKSME_BOT_PORT}/api/v1/health >/dev/null 2>/dev/null'" \
    satiksme_bot satiksme_tunnel
  validate_remote_release_identity "${remote_release_dir}" satiksme_bot "${ARBUZAS_SATIKSME_BOT_PORT}"
  validate_remote_satiksme_dependency_dns "${remote_release_dir}"
  validate_remote_probe "${remote_release_dir}" "satiksme public health" \
    "wait_until_ok sh -lc 'curl -fsS https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}/api/v1/health >/dev/null 2>/dev/null'" \
    satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme Telegram Mini App embeds in Telegram Web" \
    "wait_until_ok sh -lc 'headers=\$(curl -fsSI https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}/app | tr -d \"\\r\"); printf \"%s\\n\" \"\${headers}\" | grep -Fi \"content-security-policy:\" | grep -F \"frame-ancestors https://web.telegram.org\" >/dev/null && ! printf \"%s\\n\" \"\${headers}\" | grep -Fi \"x-frame-options:\" >/dev/null'" \
    satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme local internal health is detailed" \
    "wait_until_ok compose exec -T satiksme_bot sh -lc 'body=\$(curl -fsS http://127.0.0.1:${ARBUZAS_SATIKSME_BOT_PORT}/api/v1/internal/health) && printf %s \"\${body}\" | grep -F runtime >/dev/null && printf %s \"\${body}\" | grep -F assets >/dev/null && printf %s \"\${body}\" | grep -F catalog >/dev/null'" \
    satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme public health is minimal" \
    "wait_until_ok sh -lc 'root=https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}; body=\$(curl -fsS \"\${root}/api/v1/health\") && printf %s \"\${body}\" | grep -F ok >/dev/null && for needle in runtime assets catalog telegram reportDump db web bundle liveSnapshot version catalogStops; do if printf %s \"\${body}\" | grep -F \"\${needle}\" >/dev/null; then exit 1; fi; done && livez=\$(curl -fsS \"\${root}/api/v1/livez\") && printf %s \"\${livez}\" | grep -F ok >/dev/null && for needle in runtime assets catalog telegram reportDump db web bundle liveSnapshot version; do if printf %s \"\${livez}\" | grep -F \"\${needle}\" >/dev/null; then exit 1; fi; done && code=\$(curl -sS -o /dev/null -w \"%{http_code}\" \"\${root}/api/v1/internal/health\") && test \"\${code}\" = 404'" \
    satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme public security headers and shell assets" \
    "wait_until_ok sh -lc 'root=https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}; tmp=\$(mktemp -d); trap \"rm -rf \\\"\${tmp}\\\"\" EXIT; curl -fsS -D \"\${tmp}/root.headers\" -o \"\${tmp}/root.html\" \"\${root}/\" && grep -Fi \"strict-transport-security: max-age=31536000\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"x-frame-options: DENY\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"x-content-type-options: nosniff\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"referrer-policy: strict-origin-when-cross-origin\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"content-security-policy:\" \"\${tmp}/root.headers\" >/dev/null && ! grep -Fi \"x-satiksme-bot-\" \"\${tmp}/root.headers\" >/dev/null && grep -F \"/assets/leaflet/leaflet.js\" \"\${tmp}/root.html\" >/dev/null && ! grep -F \"unpkg.com/leaflet\" \"\${tmp}/root.html\" >/dev/null && ! grep -F \"telegram-web-app\" \"\${tmp}/root.html\" >/dev/null && ! grep -F \"\\\"telegramMiniApp\\\"\" \"\${tmp}/root.html\" >/dev/null && app=\$(curl -fsS \"\${root}/app\") && printf %s \"\${app}\" | grep -F \"\\\"mode\\\":\\\"public\\\"\" >/dev/null && printf %s \"\${app}\" | grep -F \"\\\"telegramMiniApp\\\":true\" >/dev/null && printf %s \"\${app}\" | grep -F \"telegram-web-app.js\" >/dev/null && printf %s \"\${app}\" | grep -F \"/assets/leaflet/leaflet.js\" >/dev/null && ! printf %s \"\${app}\" | grep -F \"telegram-login\" >/dev/null && incidents=\$(curl -fsS \"\${root}/incidents\") && printf %s \"\${incidents}\" | grep -F \"\\\"mode\\\":\\\"public-incidents\\\"\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"\\\"telegramMiniApp\\\"\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"unpkg.com/leaflet\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"/assets/leaflet/leaflet.js\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"telegram-login\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"telegram-web-app\" >/dev/null'" \
    satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme live snapshots are uncacheable and query-safe" \
    "wait_until_ok sh -lc 'root=https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}; tmp=\$(mktemp -d); trap \"rm -rf \\\"\${tmp}\\\"\" EXIT; curl -fsS -D \"\${tmp}/active.headers\" -o \"\${tmp}/active.json\" \"\${root}/transport/live/active.json\" && grep -Fi \"cache-control: no-store\" \"\${tmp}/active.headers\" >/dev/null && grep -Fi \"x-robots-tag: noindex\" \"\${tmp}/active.headers\" >/dev/null && path=\$(sed -n \"s/.*\\\"path\\\"[[:space:]]*:[[:space:]]*\\\"\\([^\\\"]*\\)\\\".*/\\1/p\" \"\${tmp}/active.json\" | head -1) && test -n \"\${path}\" && case \"\${path}\" in transport/live/*) ;; *) exit 1 ;; esac && curl -fsS -D \"\${tmp}/snapshot.headers\" -o /dev/null \"\${root}/\${path}\" && grep -Fi \"cache-control: no-store\" \"\${tmp}/snapshot.headers\" >/dev/null && grep -Fi \"x-robots-tag: noindex\" \"\${tmp}/snapshot.headers\" >/dev/null && code=\$(curl -sS -o /dev/null -w \"%{http_code}\" \"\${root}/\${path}?cache=split\") && test \"\${code}\" = 404'" \
    satiksme_bot satiksme_tunnel
  validate_remote_satiksme_public_hardening "${remote_release_dir}"
  validate_remote_satiksme_anonymous_data_denial "${remote_release_dir}"
  validate_remote_public_tls_dns_hardening "${remote_release_dir}"
}

validate_remote_satiksme_dependency_dns() {
  local remote_release_dir="$1"

  log "Validate: satiksme dependency DNS"
  if remote_compose_shell "${remote_release_dir}" "
    deadline=\$((SECONDS + 120))
    while (( SECONDS < deadline )); do
      if compose exec -T satiksme_bot sh -lc '
        getent hosts maincloud.spacetimedb.com >/dev/null 2>/dev/null &&
        getent hosts api.telegram.org >/dev/null 2>/dev/null &&
        getent hosts saraksti.rigassatiksme.lv >/dev/null 2>/dev/null
      '; then
        exit 0
      fi
      sleep 5
    done
    exit 1
  "; then
    return 0
  fi

  log "Validation failed: satiksme dependency DNS"
  remote_compose_shell "${remote_release_dir}" "
    cid=\$(compose ps -q satiksme_bot 2>/dev/null || true)
    if [[ -z \"\${cid}\" ]]; then
      echo 'satiksme_bot container not found for DNS diagnostics' >&2
      exit 0
    fi

    echo '--- satiksme_bot /etc/resolv.conf ---' >&2
    docker exec \"\${cid}\" cat /etc/resolv.conf >&2 || true

    echo '--- satiksme_bot docker networks ---' >&2
    docker inspect --format '{{range \$name, \$network := .NetworkSettings.Networks}}{{printf \"%s\\n\" \$name}}{{end}}' \"\${cid}\" >&2 || true

    echo '--- satiksme_bot DNS lookup: maincloud.spacetimedb.com ---' >&2
    docker exec \"\${cid}\" sh -lc 'getent hosts maincloud.spacetimedb.com' >&2 || true

    echo '--- satiksme_bot DNS lookup: api.telegram.org ---' >&2
    docker exec \"\${cid}\" sh -lc 'getent hosts api.telegram.org' >&2 || true

    echo '--- satiksme_bot DNS lookup: saraksti.rigassatiksme.lv ---' >&2
    docker exec \"\${cid}\" sh -lc 'getent hosts saraksti.rigassatiksme.lv' >&2 || true
  " || true
  collect_remote_validation_diagnostics "${remote_release_dir}" satiksme_bot satiksme_tunnel
  exit 1
}

validate_remote_subscription_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" subscription_bot subscription_tunnel
  validate_remote_probe "${remote_release_dir}" "subscription local health" \
    "wait_until_ok compose exec -T subscription_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_SUBSCRIPTION_BOT_PORT}/pixel-stack/subscription/api/v1/health >/dev/null 2>/dev/null'" \
    subscription_bot subscription_tunnel
  validate_remote_probe "${remote_release_dir}" "subscription public health" \
    "wait_until_ok sh -lc 'curl -fsS https://${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME}/pixel-stack/subscription/api/v1/health >/dev/null 2>/dev/null'" \
    subscription_bot subscription_tunnel
}

validate_remote_ticket_phone_bridge_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" ticket_phone_bridge
  validate_remote_probe "${remote_release_dir}" "ticket-phone-bridge local health" \
    "wait_until_ok compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null 2>/dev/null'" \
    ticket_phone_bridge
}

validate_remote_qbittorrent_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" qbittorrent qbittorrent_housekeeper
  validate_remote_probe "${remote_release_dir}" "qBittorrent containers are healthy" \
    "deadline=\$((SECONDS + 180))
      all_healthy=0
      while (( SECONDS < deadline )); do
        all_healthy=1
        for service_name in qbittorrent qbittorrent_housekeeper; do
          container_id=\$(compose ps -q \"\${service_name}\" 2>/dev/null || true)
          health_status=''
          if [[ -n \"\${container_id}\" ]]; then
            health_status=\$(docker inspect --format \"{{.State.Health.Status}}\" \"\${container_id}\" 2>/dev/null || true)
          fi
          if [[ \"\${health_status}\" != healthy ]]; then
            all_healthy=0
            break
          fi
        done
        (( all_healthy == 1 )) && break
        sleep 5
      done
      (( all_healthy == 1 ))" \
    qbittorrent qbittorrent_housekeeper
  validate_remote_host_probe "${remote_release_dir}" "qBittorrent capped storage and managed preferences" \
    "set -a
      . '${remote_release_dir}/release.env'
      set +a
      test \"\${ARBUZAS_QBITTORRENT_PUID}\" = \"\$(id -u)\"
      test \"\${ARBUZAS_QBITTORRENT_PGID}\" = \"\$(id -g)\"
      sudo -n bash '${remote_release_dir}/infra/arbuzas/qbittorrent/install-storage.sh' check
      sudo -n python3 '${remote_release_dir}/infra/arbuzas/qbittorrent/reconcile-config.py' \
        --path '${QBITTORRENT_REMOTE_CONFIG_FILE}' \
        --uid \"\${ARBUZAS_QBITTORRENT_PUID}\" \
        --gid \"\${ARBUZAS_QBITTORRENT_PGID}\" \
        --check" \
    qbittorrent qbittorrent_housekeeper
  validate_remote_probe "${remote_release_dir}" "qBittorrent loopback WebUI and public peer bindings" \
    "inspect_json=\$(mktemp)
      housekeeper_inspect_json=\$(mktemp)
      trap 'rm -f \"\${inspect_json}\" \"\${housekeeper_inspect_json}\"' EXIT
      container_id=\$(compose ps -q qbittorrent)
      housekeeper_container_id=\$(compose ps -q qbittorrent_housekeeper)
      docker inspect \"\${container_id}\" > \"\${inspect_json}\"
      docker inspect \"\${housekeeper_container_id}\" > \"\${housekeeper_inspect_json}\"
      python3 - \"\${inspect_json}\" \"\${housekeeper_inspect_json}\" <<'PY'
import json
import sys

container = json.load(open(sys.argv[1], encoding='utf-8'))[0]
housekeeper = json.load(open(sys.argv[2], encoding='utf-8'))[0]
ports = container.get('NetworkSettings', {}).get('Ports', {})
host_config = container.get('HostConfig', {})
housekeeper_host_config = housekeeper.get('HostConfig', {})

if host_config.get('Memory') != 805306368:
    raise SystemExit(f'unexpected qBittorrent memory limit: {host_config.get("Memory")!r}')
if not 0 < host_config.get('Memory', 0) < 1073741824:
    raise SystemExit(f'qBittorrent memory limit is not below 1 GiB: {host_config.get("Memory")!r}')
if host_config.get('MemoryReservation') != 402653184:
    raise SystemExit(f'unexpected qBittorrent memory reservation: {host_config.get("MemoryReservation")!r}')
if host_config.get('MemorySwap') != 805306368:
    raise SystemExit(f'unexpected qBittorrent memory+swap limit: {host_config.get("MemorySwap")!r}')
if container.get('State', {}).get('OOMKilled') is not False:
    raise SystemExit('qBittorrent has recorded an out-of-memory kill')
if housekeeper_host_config.get('Memory') != 134217728:
    raise SystemExit(f'unexpected qBittorrent housekeeper memory limit: {housekeeper_host_config.get("Memory")!r}')
if housekeeper_host_config.get('MemorySwap') != 134217728:
    raise SystemExit(f'unexpected qBittorrent housekeeper memory+swap limit: {housekeeper_host_config.get("MemorySwap")!r}')
if housekeeper.get('State', {}).get('OOMKilled') is not False:
    raise SystemExit('qBittorrent housekeeper has recorded an out-of-memory kill')

web = ports.get('${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT}/tcp') or []
if web != [{'HostIp': '127.0.0.1', 'HostPort': '${ARBUZAS_QBITTORRENT_WEBUI_PORT}'}]:
    raise SystemExit(f'unexpected qBittorrent WebUI publishing: {web!r}')

for protocol in ('tcp', 'udp'):
    bindings = ports.get('${ARBUZAS_QBITTORRENT_PEER_PORT}/' + protocol) or []
    if not bindings:
        raise SystemExit(f'missing public peer {protocol} binding')
    for binding in bindings:
        if binding.get('HostPort') != '${ARBUZAS_QBITTORRENT_PEER_PORT}':
            raise SystemExit(f'wrong public peer {protocol} port: {bindings!r}')
        if binding.get('HostIp') in ('127.0.0.1', '::1'):
            raise SystemExit(f'peer {protocol} is loopback-only: {bindings!r}')
PY
      docker exec \"\${container_id}\" /usr/local/bin/qbittorrent-memory-health
      docker exec \"\${housekeeper_container_id}\" grep -Fx '134217728' /sys/fs/cgroup/memory.max >/dev/null
      docker exec \"\${housekeeper_container_id}\" grep -Fx '0' /sys/fs/cgroup/memory.swap.max >/dev/null
      docker exec \"\${housekeeper_container_id}\" grep -Fx 'oom 0' /sys/fs/cgroup/memory.events >/dev/null
      docker exec \"\${housekeeper_container_id}\" grep -Fx 'oom_kill 0' /sys/fs/cgroup/memory.events >/dev/null
      network_json=\$(docker network inspect arbuzas_qbittorrent_private)
      printf '%s' \"\${network_json}\" | python3 -c 'import json,sys; payload=json.load(sys.stdin)[0]; configs=payload.get(\"IPAM\", {}).get(\"Config\", []); assert configs == [{\"Subnet\": \"172.29.246.0/28\", \"Gateway\": \"172.29.246.1\"}], configs; names={item.get(\"Name\") for item in payload.get(\"Containers\", {}).values()}; assert any(name.endswith(\"-qbittorrent-1\") for name in names), names; assert any(name.endswith(\"-qbittorrent_housekeeper-1\") for name in names), names'
      wait_until_ok curl -fsS \
        -H 'Host: ${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT}' \
        'http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}/api/v2/app/version' \
        | grep -Fx 'v5.2.3' >/dev/null" \
    qbittorrent qbittorrent_housekeeper
  validate_remote_probe "${remote_release_dir}" "qBittorrent private Tailscale VueTorrent page needs no login" \
    "root='https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'
      wait_until_ok sh -lc 'test \"\$(curl -fsS https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}/api/v2/app/version)\" = v5.2.3'
      tmpdir=\$(mktemp -d)
      trap 'rm -rf \"\${tmpdir}\"' EXIT
      curl -fsS \"\${root}/api/v2/app/preferences\" > \"\${tmpdir}/preferences.json\"
      curl -fsS \"\${root}/api/v2/app/buildInfo\" > \"\${tmpdir}/build-info.json\"
      curl -fsS \"\${root}/\" > \"\${tmpdir}/index.html\"
      headers=\$(curl -fsSI \"\${root}/\" | tr -d \"\\r\")
      printf '%s\\n' \"\${headers}\" | grep -Fi 'x-frame-options:' >/dev/null
      printf '%s\\n' \"\${headers}\" | grep -Fi 'content-security-policy:' >/dev/null
      grep -Fi 'VueTorrent' \"\${tmpdir}/index.html\" >/dev/null
      compose exec -T qbittorrent_housekeeper wget -q -T 3 -O - http://127.0.0.1:9091/status > \"\${tmpdir}/housekeeper-status.json\"
      compose exec -T qbittorrent_housekeeper env > \"\${tmpdir}/housekeeper.env\"
      python3 - \"\${tmpdir}/preferences.json\" \"\${tmpdir}/housekeeper-status.json\" \"\${tmpdir}/housekeeper.env\" \"\${tmpdir}/build-info.json\" <<'PY'
import json
import sys

preferences = json.load(open(sys.argv[1], encoding='utf-8'))
status = json.load(open(sys.argv[2], encoding='utf-8'))
environment = {}
for line in open(sys.argv[3], encoding='utf-8'):
    key, separator, value = line.rstrip('\n').partition('=')
    if separator:
        environment[key] = value

build_info = json.load(open(sys.argv[4], encoding='utf-8'))
if not str(build_info.get('libtorrent', '')).startswith('2.'):
    raise SystemExit(f'unexpected qBittorrent libtorrent build: {build_info!r}')

expected_preferences = {
    'async_io_threads': 2,
    'checking_memory_use': 8,
    'connection_speed': 10,
    'disk_io_type': 3,
    'disk_io_read_mode': 0,
    'disk_io_write_mode': 0,
    'disk_queue_size': 1048576,
    'file_pool_size': 32,
    'hashing_threads': 1,
    'max_active_checking_torrents': 1,
    'max_concurrent_http_announces': 10,
    'max_connec': 80,
    'max_connec_per_torrent': 20,
    'max_uploads': 12,
    'max_uploads_per_torrent': 4,
    'queueing_enabled': False,
    'request_queue_size': 50,
    'save_resume_data_interval': 1,
    'send_buffer_low_watermark': 16,
    'send_buffer_watermark': 128,
    'send_buffer_watermark_factor': 25,
    'socket_receive_buffer_size': 65536,
    'socket_send_buffer_size': 65536,
    'torrent_stop_condition': 'MetadataReceived',
    'bypass_local_auth': True,
    'bypass_auth_subnet_whitelist_enabled': True,
    'web_ui_reverse_proxy_enabled': True,
    'web_ui_reverse_proxies_list': '172.29.246.1',
    'web_ui_domain_list': '${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME};localhost;127.0.0.1;qbittorrent',
    'alternative_webui_enabled': True,
    'alternative_webui_path': '/vuetorrent',
    'web_ui_port': ${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT},
    'web_ui_clickjacking_protection_enabled': True,
    'web_ui_csrf_protection_enabled': True,
    'web_ui_secure_cookie_enabled': True,
    'web_ui_host_header_validation_enabled': True,
    'web_ui_upnp': False,
    'use_https': False,
    'listen_port': ${ARBUZAS_QBITTORRENT_PEER_PORT},
    'temp_path_enabled': True,
    'max_ratio_enabled': False,
    'max_ratio_act': 0,
    'max_seeding_time_enabled': False,
    'max_inactive_seeding_time_enabled': False,
}
for key, expected in expected_preferences.items():
    actual = preferences.get(key)
    if actual != expected:
        raise SystemExit(f'qBittorrent preference {key}={actual!r}, expected {expected!r}')
for key, expected in {'save_path': '/downloads', 'temp_path': '/downloads/.incomplete'}.items():
    actual = str(preferences.get(key, '')).rstrip('/')
    if actual != expected:
        raise SystemExit(f'qBittorrent preference {key}={actual!r}, expected {expected!r}')

expected_subnets = {
    '127.0.0.1/32',
    '::1/128',
    '172.29.246.0/28',
    '100.64.0.0/10',
    'fd7a:115c:a1e0::/48',
}
actual_subnets = {value.strip() for value in preferences.get('bypass_auth_subnet_whitelist', '').replace(',', '\n').splitlines() if value.strip()}
if actual_subnets != expected_subnets:
    raise SystemExit(f'qBittorrent auth bypass subnets={actual_subnets!r}, expected {expected_subnets!r}')

if status.get('healthy') is not True or status.get('soft_cap_bytes') != 25769803776:
    raise SystemExit(f'unexpected housekeeper status: {status!r}')
expected_environment = {
    'QBITTORRENT_URL': 'http://qbittorrent:${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT}',
    'DOWNLOAD_PATH': '/downloads',
    'SOFT_CAP_BYTES': '25769803776',
    'MIN_COMPLETED_AGE': '168h',
    'MIN_RATIO': '1.0',
}
for key, expected in expected_environment.items():
    if environment.get(key) != expected:
        actual = environment.get(key)
        raise SystemExit(f'housekeeper {key}={actual!r}, expected {expected!r}')
if 'QBITTORRENT_USERNAME' in environment or 'QBITTORRENT_PASSWORD_FILE' in environment:
    raise SystemExit('housekeeper unexpectedly has qBittorrent login credentials')
PY" \
    qbittorrent qbittorrent_housekeeper
  validate_remote_host_probe "${remote_release_dir}" "qBittorrent Tailscale route preserves HTTPS :10000" \
    "tmp=\$(mktemp)
      trap 'rm -f \"\${tmp}\"' EXIT
      tailscale serve status --json > \"\${tmp}\"
      python3 - \"\${tmp}\" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1], encoding='utf-8'))
tcp = payload.get('TCP', {})
web = payload.get('Web', {})
if tcp.get('10000') is None and not any(key.rsplit(':', 1)[-1] == '10000' for key in web):
    raise SystemExit('existing Tailscale Serve HTTPS :10000 is missing')
port = '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'
if tcp.get(port, {}).get('HTTPS') is not True:
    raise SystemExit(f'qBittorrent Tailscale Serve :{port} is not HTTPS')
handler = web.get('${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:' + port, {}).get('Handlers', {}).get('/', {})
expected = 'http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}'
if handler.get('Proxy') != expected:
    raise SystemExit(f'qBittorrent Tailscale handler is {handler!r}, expected proxy {expected!r}')
PY" \
    qbittorrent qbittorrent_housekeeper
}

validate_remote_jellyfin_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" jellyfin
  validate_remote_probe "${remote_release_dir}" "Jellyfin container is healthy" \
    "deadline=\$((SECONDS + 180))
      healthy=0
      while (( SECONDS < deadline )); do
        container_id=\$(compose ps -q jellyfin 2>/dev/null || true)
        if [[ -n \"\${container_id}\" ]] &&
           [[ \"\$(docker inspect --format '{{.State.Health.Status}}' \"\${container_id}\" 2>/dev/null || true)\" == healthy ]]; then
          healthy=1
          break
        fi
        sleep 5
      done
      (( healthy == 1 ))" \
    jellyfin

  validate_remote_host_probe "${remote_release_dir}" "Jellyfin state, media, and admin secret are safe" \
    "set -a
      . '${remote_release_dir}/release.env'
      set +a
      test \"\${ARBUZAS_JELLYFIN_PUID}\" = \"\$(id -u)\"
      test \"\${ARBUZAS_JELLYFIN_PGID}\" = \"\$(id -g)\"
      sudo -n bash '${remote_release_dir}/infra/arbuzas/qbittorrent/install-storage.sh' check
      for path in \
        '${JELLYFIN_REMOTE_ROOT}' \
        '${JELLYFIN_REMOTE_ROOT}/config' \
        '${JELLYFIN_REMOTE_ROOT}/cache' \
        '${JELLYFIN_REMOTE_ROOT}/tmp' \
        '${JELLYFIN_REMOTE_ROOT}/transcodes'; do
        test -d \"\${path}\"
        test ! -L \"\${path}\"
        test \"\$(stat -c '%u:%g:%a' \"\${path}\")\" = \"\${ARBUZAS_JELLYFIN_PUID}:\${ARBUZAS_JELLYFIN_PGID}:750\"
      done
      sudo -n test -f '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}'
      sudo -n test ! -L '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}'
      test \"\$(sudo -n stat -c '%u:%g:%a' '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}')\" = '0:0:600'
      sudo -n python3 - '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' <<'PY'
from pathlib import Path
import sys

value = Path(sys.argv[1]).read_text(encoding='utf-8').strip()
if len(value) < 32 or any(char.isspace() for char in value):
    raise SystemExit('Jellyfin admin password file is empty, short, or malformed')
PY" \
    jellyfin

  validate_remote_probe "${remote_release_dir}" "Jellyfin limits and loopback-only read-only media mount" \
    "inspect_json=\$(mktemp)
      trap 'rm -f \"\${inspect_json}\"' EXIT
      container_id=\$(compose ps -q jellyfin)
      docker inspect \"\${container_id}\" > \"\${inspect_json}\"
      set -a
      . '${remote_release_dir}/release.env'
      set +a
      python3 - \"\${inspect_json}\" \"\${ARBUZAS_JELLYFIN_PUID}:\${ARBUZAS_JELLYFIN_PGID}\" <<'PY'
import json
import sys

container = json.load(open(sys.argv[1], encoding='utf-8'))[0]
expected_user = sys.argv[2]
host_config = container.get('HostConfig', {})
config = container.get('Config', {})
state = container.get('State', {})
ports = container.get('NetworkSettings', {}).get('Ports', {})

if host_config.get('Memory') != 536870912:
    raise SystemExit(f'unexpected Jellyfin memory limit: {host_config.get("Memory")!r}')
if host_config.get('MemoryReservation') != 134217728:
    raise SystemExit(f'unexpected Jellyfin memory reservation: {host_config.get("MemoryReservation")!r}')
if host_config.get('MemorySwap') != 536870912:
    raise SystemExit(f'unexpected Jellyfin memory plus swap limit: {host_config.get("MemorySwap")!r}')
if host_config.get('NanoCpus') != 750000000:
    raise SystemExit(f'unexpected Jellyfin CPU limit: {host_config.get("NanoCpus")!r}')
if host_config.get('PidsLimit') != 256:
    raise SystemExit(f'unexpected Jellyfin process limit: {host_config.get("PidsLimit")!r}')
if host_config.get('ReadonlyRootfs') is not True:
    raise SystemExit('Jellyfin root filesystem is not read-only')
if host_config.get('Privileged') is not False:
    raise SystemExit('Jellyfin is unexpectedly privileged')
if host_config.get('CapDrop') != ['ALL']:
    raise SystemExit(f'unexpected Jellyfin capability policy: {host_config.get("CapDrop")!r}')
if 'no-new-privileges:true' not in (host_config.get('SecurityOpt') or []):
    raise SystemExit(f'Jellyfin no-new-privileges is missing: {host_config.get("SecurityOpt")!r}')
if host_config.get('Tmpfs') not in ({}, None):
    raise SystemExit(f'Jellyfin unexpectedly uses RAM-backed tmpfs: {host_config.get("Tmpfs")!r}')
if state.get('OOMKilled') is not False:
    raise SystemExit('Jellyfin has recorded an out-of-memory kill')
actual_user = config.get('User')
if actual_user != expected_user:
    raise SystemExit(f'Jellyfin runs as {actual_user!r}, expected {expected_user!r}')

web = ports.get('${ARBUZAS_JELLYFIN_INTERNAL_PORT}/tcp') or []
if web != [{'HostIp': '127.0.0.1', 'HostPort': '${ARBUZAS_JELLYFIN_HOST_PORT}'}]:
    raise SystemExit(f'unexpected Jellyfin Web publishing: {web!r}')
for exposed_port, bindings in ports.items():
    if exposed_port != '${ARBUZAS_JELLYFIN_INTERNAL_PORT}/tcp' and bindings:
        raise SystemExit(f'unexpected published Jellyfin port {exposed_port}: {bindings!r}')

expected_mounts = {
    '/config': ('/srv/arbuzas/jellyfin/config', True),
    '/cache': ('/srv/arbuzas/jellyfin/cache', True),
    '/tmp': ('/srv/arbuzas/jellyfin/tmp', True),
    '/transcodes': ('/srv/arbuzas/jellyfin/transcodes', True),
    '/media': ('/srv/arbuzas/qbittorrent/storage/payload', False),
}
actual_mounts = {
    item.get('Destination'): (item.get('Source'), item.get('RW'))
    for item in container.get('Mounts', [])
}
if actual_mounts != expected_mounts:
    raise SystemExit(f'unexpected Jellyfin mounts: {actual_mounts!r}')
if any('/etc/arbuzas/secrets' in str(source) for source, _writable in actual_mounts.values()):
    raise SystemExit('Jellyfin admin secret is unexpectedly mounted into the container')

networks = container.get('NetworkSettings', {}).get('Networks', {})
if set(networks) != {'arbuzas_jellyfin_private'}:
    raise SystemExit(f'unexpected Jellyfin Docker networks: {sorted(networks)!r}')
PY
      grep -Fx '536870912' /sys/fs/cgroup/system.slice/docker-\${container_id}.scope/memory.max >/dev/null 2>&1 || \
        docker exec \"\${container_id}\" grep -Fx '536870912' /sys/fs/cgroup/memory.max >/dev/null
      docker exec \"\${container_id}\" grep -Fx '0' /sys/fs/cgroup/memory.swap.max >/dev/null
      docker exec \"\${container_id}\" grep -Fx 'oom 0' /sys/fs/cgroup/memory.events >/dev/null
      docker exec \"\${container_id}\" grep -Fx 'oom_kill 0' /sys/fs/cgroup/memory.events >/dev/null
      network_json=\$(docker network inspect arbuzas_jellyfin_private)
      printf '%s' \"\${network_json}\" | python3 -c 'import json,sys; payload=json.load(sys.stdin)[0]; configs=payload.get(\"IPAM\", {}).get(\"Config\", []); assert configs == [{\"Subnet\": \"172.29.247.0/28\", \"Gateway\": \"172.29.247.1\"}], configs; containers=payload.get(\"Containers\", {}); members=[item for item in containers.values() if item.get(\"Name\", \"\").endswith(\"-jellyfin-1\")]; assert len(members) == 1, members; assert members[0].get(\"IPv4Address\") == \"172.29.247.2/28\", members; assert payload.get(\"Options\", {}).get(\"com.docker.network.bridge.enable_icc\") == \"false\", payload.get(\"Options\")'
      curl -fsS --connect-timeout 2 --max-time 5 \
        'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}/health' | grep -Fx Healthy >/dev/null" \
    jellyfin

  validate_remote_host_probe "${remote_release_dir}" "Jellyfin passwordless profile, library, and direct-play policy" \
    "sudo -n python3 '${remote_release_dir}/infra/arbuzas/jellyfin/bootstrap.py' check \
      --url 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}' \
      --admin-password-file '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}'" \
    jellyfin

  validate_remote_probe "${remote_release_dir}" "Jellyfin private Tailscale interface" \
    "root='https://${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}:${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}'
      wait_until_ok sh -lc 'curl -fsS https://${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}:${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}/health | grep -Fx Healthy >/dev/null'
      curl -fsSL \"\${root}/web/\" | grep -Fi Jellyfin >/dev/null
      test \"\$(curl -fsS --connect-timeout 3 --max-time 8 \
        'https://${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}/api/v2/app/version')\" = v5.2.3
      panel_code=\$(curl -skS --connect-timeout 3 --max-time 8 -o /dev/null -w '%{http_code}' \
        'https://${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}:10000/' || true)
      [[ \"\${panel_code}\" != 000 ]]" \
    jellyfin

  validate_remote_host_probe "${remote_release_dir}" "Jellyfin exact Tailscale route and existing routes" \
    "tmp=\$(mktemp)
      trap 'rm -f \"\${tmp}\"' EXIT
      tailscale serve status --json > \"\${tmp}\"
      python3 - \"\${tmp}\" <<'PY'
import json
import sys

payload = json.load(open(sys.argv[1], encoding='utf-8'))
tcp = payload.get('TCP', {})
web = payload.get('Web', {})
for existing_port in ('10000', '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'):
    if tcp.get(existing_port) is None and not any(key.rsplit(':', 1)[-1] == existing_port for key in web):
        raise SystemExit(f'existing Tailscale Serve :{existing_port} is missing')
port = '${ARBUZAS_JELLYFIN_TAILSCALE_HTTPS_PORT}'
if port in ('443', '10000', '${ARBUZAS_QBITTORRENT_TAILSCALE_HTTPS_PORT}'):
    raise SystemExit(f'Jellyfin uses a forbidden or occupied Tailscale port: {port}')
if tcp.get(port) != {'HTTPS': True}:
    raise SystemExit(f'Jellyfin Tailscale Serve :{port} is not exact HTTPS: {tcp.get(port)!r}')
target_web = {key: value for key, value in web.items() if key.rsplit(':', 1)[-1] == port}
handler = target_web.get('${ARBUZAS_JELLYFIN_TAILSCALE_HOSTNAME}:' + port, {}).get('Handlers', {}).get('/', {})
expected = 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}'
if len(target_web) != 1 or handler.get('Proxy') != expected:
    raise SystemExit(f'Jellyfin Tailscale route is {target_web!r}, expected only {expected!r}')
PY" \
    jellyfin
}

validate_remote_ticket_remote_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-phone-bridge local health" \
    "wait_until_ok compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null 2>/dev/null'" \
    ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote direct bridge health" \
    "wait_until_ok compose exec -T ticket_remote sh -lc 'curl -fsS http://ticket_phone_bridge:9388/api/v1/health >/dev/null 2>/dev/null'" \
    ticket_phone_bridge ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote Spacetime sidecar health" \
    "wait_until_ok compose exec -T ticket_remote_spacetime_sidecar sh -lc 'curl -fsS http://127.0.0.1:9346/healthz | grep -F \"\\\"status\\\":\\\"ok\\\"\" >/dev/null'" \
    ticket_remote_spacetime_sidecar ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote local health" \
    "wait_until_ok compose exec -T ticket_remote sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null 2>/dev/null'" \
    ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote production state backend" \
    "ticket_state_backend_ok() {
      file_backend=\$(sed -n 's/^TICKET_REMOTE_STATE_BACKEND=//p' /etc/arbuzas/env/ticket-remote.env | tail -1)
      case \"\${file_backend}\" in ''|spacetime|spacetimedb) ;; *) return 1 ;; esac
      compose exec -T ticket_remote sh -lc 'test \"\${TICKET_REMOTE_PRODUCTION}\" = true && case \"\${TICKET_REMOTE_STATE_BACKEND}\" in spacetime|spacetimedb) exit 0 ;; *) exit 1 ;; esac'
    }; wait_until_ok ticket_state_backend_ok" \
    ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote public container secrets scoped" \
    "wait_until_ok compose exec -T ticket_remote sh -lc 'test ! -e /root/.android/adbkey && test ! -e /root/.android/adbkey.pub && test ! -e /root/.android/adb_known_hosts.pb && test ! -d /etc/arbuzas/secrets && test -d /run/secrets/ticket-remote'" \
    ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote active configured backend" \
    "active_configured_backend_ok() {
      active=\$(sed -n 's/.*\"backendId\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p' /srv/arbuzas/ticket-remote/state/active-phone-backend.json 2>/dev/null | head -1)
      if [[ -z \"\${active}\" ]]; then
        active=pixel
      fi
      [[ \"\${active}\" = pixel ]] || return 1
      compose exec -T ticket_remote sh -lc 'test \"\${TICKET_REMOTE_PHONE_BACKEND_ID}\" = pixel && test \"\${TICKET_REMOTE_PHONE_BASE_URL}\" = \"http://ticket_phone_bridge:9388\" && curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null'
    }
    wait_until_ok active_configured_backend_ok" \
    ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote public login shell" \
    "wait_until_ok sh -lc 'code=\$(curl -sS -o /dev/null -w \"%{http_code}\" https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/ 2>/dev/null || true); case \"\${code}\" in 200|302) exit 0 ;; *) exit 1 ;; esac'" \
    ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote public HTTP redirects to HTTPS" \
    "wait_until_ok sh -lc 'result=\$(curl -sS -o /dev/null -w \"%{http_code} %{redirect_url}\" http://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/ 2>/dev/null || true); case \"\${result}\" in \"301 https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/\"*|\"308 https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/\"*) exit 0 ;; *) printf \"%s\\n\" \"\${result}\" >&2; exit 1 ;; esac'" \
    ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote public safety headers" \
    "wait_until_ok sh -lc 'headers=\$(curl -fsSI https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/ 2>/dev/null | tr -d \"\\r\"); printf \"%s\\n\" \"\${headers}\" | grep -Fi \"strict-transport-security:\" >/dev/null && printf \"%s\\n\" \"\${headers}\" | grep -Fi \"content-security-policy:\" >/dev/null && printf \"%s\\n\" \"\${headers}\" | grep -Fi \"x-frame-options:\" >/dev/null && printf \"%s\\n\" \"\${headers}\" | grep -Fi \"x-content-type-options:\" >/dev/null'" \
    ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote auth configured" \
    "auth_configured_ok() { mode=\$(sed -n 's/^TICKET_REMOTE_AUTH_MODE=//p' /etc/arbuzas/env/ticket-remote.env | tail -1); case \"\${mode}\" in ''|spacetime|spacetimeauth|oidc) grep -Eq '^TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID=.+' /etc/arbuzas/env/ticket-remote.env && grep -Eq '^TICKET_REMOTE_SESSION_SIGNING_KEY=.+' /etc/arbuzas/env/ticket-remote.env ;; cloudflare|cloudflare-access|cf-access) grep -Eq '^TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN=.+' /etc/arbuzas/env/ticket-remote.env && grep -Eq '^TICKET_REMOTE_CF_ACCESS_AUDIENCE=.+' /etc/arbuzas/env/ticket-remote.env ;; dev|development|none) return 1 ;; *) return 1 ;; esac; }; wait_until_ok auth_configured_ok" \
    ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote runtime OIDC issuer" \
    "runtime_oidc_ok() {
      backend=\$(sed -n 's/^TICKET_REMOTE_STATE_BACKEND=//p' /etc/arbuzas/env/ticket-remote.env | tail -1)
      case \"\${backend}\" in
        ''|spacetime) ;;
        *) return 0 ;;
      esac
      expected='https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/oidc'
      issuer=\$(sed -n 's/^TICKET_REMOTE_SPACETIME_OIDC_ISSUER=//p' /etc/arbuzas/env/ticket-remote.env | tail -1)
      [[ \"\${issuer}\" = \"\${expected}\" ]] || return 1
      body=\$(curl -fsS \"\${issuer}/.well-known/openid-configuration\") || return 1
      printf %s \"\${body}\" | grep -F \"\\\"issuer\\\":\\\"\${expected}\\\"\" >/dev/null || return 1
      printf %s \"\${body}\" | grep -F \"\\\"jwks_uri\\\":\\\"\${expected}/jwks.json\\\"\" >/dev/null || return 1
      jwks=\$(curl -fsS \"\${expected}/jwks.json\") || return 1
      printf %s \"\${jwks}\" | grep -F '\"keys\"' >/dev/null
    }; wait_until_ok runtime_oidc_ok" \
    ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote stale viewer code absent" \
    "wait_until_ok compose exec -T ticket_remote sh -lc 'set -e
      binary=/usr/local/bin/ticket-remote
      app_js=\$(mktemp)
      trap \"rm -f \\\"\${app_js}\\\"\" EXIT
      curl -fsS \"http://127.0.0.1:\${TICKET_REMOTE_WEB_PORT:-9338}/static/app.js\" > \"\${app_js}\"
      grep -aE \"claim-dialog|showModal|confirmClaim\" \"\${binary}\" >/dev/null && exit 1
      grep -aE \"mozBrightness|AmbientLightSensor|screen\\\\.brightness|setBrightness\" \"\${binary}\" >/dev/null && exit 1
      grep -aE \"localStorage|sessionStorage|ticket_remote_spacetime_token|ticket_remote_pkce\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"send({ type: '\\''tap'\\'', x: options.tap.x\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"snapTarget: '\\''control_code_button'\\''\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"type: '\\''quick_claim_tap'\\''\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"runControlMutation\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"claimControl()\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"releaseControl(\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"revokeControl(\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"inputQueueLimit = 30\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"inputDrainDelayMs = 35\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"RTCPeerConnection\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"webrtc_ice_config\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"webrtcVideo\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"iceTransportPolicy\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"Savieno WebRTC video\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"TURN\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"legacy_frame_in_tsf2_stream\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"version: '\\''legacy'\\''\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"configuredFrameEnvelope\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"|| '\\''legacy'\\''\" \"\${app_js}\" >/dev/null && exit 1
      grep -aF \"/api/v1/control-code/request\" \"\${binary}\" >/dev/null
      grep -aF \"/api/v1/control-code/close\" \"\${binary}\" >/dev/null
      grep -aF \"control_code_request\" \"\${binary}\" >/dev/null
      grep -aF \"generate_control_code\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"requestControlCode\" \"\${binary}\" >/dev/null
      grep -aF \"sanitizeControlDigits\" \"\${binary}\" >/dev/null
      grep -aF \"navigator.wakeLock.request\" \"\${binary}\" >/dev/null
      grep -aF \"requestFullscreen\" \"\${binary}\" >/dev/null
      grep -aF \"toolbarCollapseAnchorPx\" \"\${binary}\" >/dev/null && exit 1
      grep -aF -- \"--ticket-viewport-height\" \"\${binary}\" >/dev/null
      grep -aF \"gesturechange\" \"\${binary}\" >/dev/null
      grep -aF \"dblclick\" \"\${binary}\" >/dev/null
      grep -aF \"touch-action: pan-y\" \"\${binary}\" >/dev/null
      grep -aF \"VideoDecoder\" \"\${binary}\" >/dev/null
      grep -aF \"EncodedVideoChunk\" \"\${binary}\" >/dev/null
      grep -aF \"ctx.drawImage\" \"\${binary}\" >/dev/null
      grep -aF \"invalid_tsf2_frame\" \"\${binary}\" >/dev/null
    '" \
    ticket_remote
}

validate_remote_selected_smoke_health() {
  local remote_release_dir="$1"
  local require_current_link="${2:-0}"
  local selected_service_args=""
  local diagnostics_services=()

  if ! [[ "${ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS}" =~ ^[1-9][0-9]*$ ]]; then
    echo "ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS must be a positive integer" >&2
    return 2
  fi

  selected_service_args="$(compose_target_service_args)"
  populate_current_diagnostic_services diagnostics_services
  validate_remote_probe "${remote_release_dir}" "batched selected-service smoke readiness" "
    ticket_smoke_probe_pids=()
    ticket_smoke_probe_labels=()
    ticket_smoke_probe_logs=()

    cleanup_ticket_smoke_probes() {
      local probe_pid=''
      local probe_log=''

      for probe_pid in \"\${ticket_smoke_probe_pids[@]}\"; do
        kill -TERM -- \"-\${probe_pid}\" >/dev/null 2>&1 || true
      done
      for probe_pid in \"\${ticket_smoke_probe_pids[@]}\"; do
        wait \"\${probe_pid}\" >/dev/null 2>&1 || true
      done
      for probe_log in \"\${ticket_smoke_probe_logs[@]}\"; do
        rm -f \"\${probe_log}\"
      done
      ticket_smoke_probe_pids=()
      ticket_smoke_probe_labels=()
      ticket_smoke_probe_logs=()
    }

    trap 'cleanup_ticket_smoke_probes; exit 130' INT
    trap 'cleanup_ticket_smoke_probes; exit 143' TERM
    trap cleanup_ticket_smoke_probes EXIT

    start_ticket_smoke_probe() {
      local probe_label=\"\$1\"
      local probe_command=\"\$2\"
      local probe_log=''

      probe_log=\$(mktemp /tmp/arbuzas-ticket-smoke.XXXXXX) || return 1
      ticket_smoke_probe_labels+=(\"\${probe_label}\")
      ticket_smoke_probe_logs+=(\"\${probe_log}\")
      export -f compose
      setsid timeout --kill-after=1s 2s bash -c \"\${probe_command}\" >\"\${probe_log}\" 2>&1 &
      ticket_smoke_probe_pids+=(\"\$!\")
    }

    collect_ticket_smoke_probe_status() {
      local probe_index=0
      local probe_pid=''
      local failed=0

      for probe_pid in \"\${ticket_smoke_probe_pids[@]}\"; do
        if ! wait \"\${probe_pid}\"; then
          printf 'Ticket smoke probe failed: %s\\n' \"\${ticket_smoke_probe_labels[probe_index]}\" >&2
          sed 's/^/  /' \"\${ticket_smoke_probe_logs[probe_index]}\" >&2 || true
          failed=1
        fi
        ((probe_index += 1))
      done

      for probe_index in \"\${!ticket_smoke_probe_logs[@]}\"; do
        rm -f \"\${ticket_smoke_probe_logs[probe_index]}\"
      done
      ticket_smoke_probe_pids=()
      ticket_smoke_probe_labels=()
      ticket_smoke_probe_logs=()
      (( failed == 0 ))
    }

    smoke_ready() {
      local running=''
      local service_name=''

      [[ -f '${remote_release_dir}/release.env' ]] || return 1
      [[ -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' ]] || return 1
      if [[ '${require_current_link}' == '1' ]]; then
        [[ \"\$(readlink -f '${REMOTE_CURRENT_LINK}')\" == \"\$(readlink -f '${remote_release_dir}')\" ]] || return 1
      fi

      running=\$(compose ps --services --status running | tr '\n' ' ') || return 1
      for service_name in${selected_service_args}; do
        case \" \${running} \" in
          *\" \${service_name} \"*) ;;
          *) return 1 ;;
        esac
      done

      if [[ '${VALIDATE_TRAIN}' == '1' ]]; then
        compose exec -T train_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TRAIN_BOT_PORT}/api/v1/health >/dev/null' >/dev/null 2>&1 || return 1
        curl -fsS --connect-timeout 2 --max-time 4 https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/api/v1/health >/dev/null 2>&1 || return 1
      fi
      if [[ '${VALIDATE_SATIKSME}' == '1' ]]; then
        compose exec -T satiksme_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_SATIKSME_BOT_PORT}/api/v1/health >/dev/null' >/dev/null 2>&1 || return 1
        curl -fsS --connect-timeout 2 --max-time 4 https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}/api/v1/health >/dev/null 2>&1 || return 1
      fi
      if [[ '${VALIDATE_SUBSCRIPTION}' == '1' ]]; then
        compose exec -T subscription_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_SUBSCRIPTION_BOT_PORT}/api/v1/health >/dev/null' >/dev/null 2>&1 || return 1
        curl -fsS --connect-timeout 2 --max-time 4 https://${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME}/api/v1/health >/dev/null 2>&1 || return 1
      fi
      if [[ '${VALIDATE_TICKET_PHONE_BRIDGE}' == '1' ]]; then
        case \" \${running} \" in *' ticket_phone_bridge '*) ;; *) return 1 ;; esac
        compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null' >/dev/null 2>&1 || return 1
      fi
      if [[ '${VALIDATE_QBITTORRENT}' == '1' ]]; then
        for service_name in qbittorrent qbittorrent_housekeeper; do
          case \" \${running} \" in *\" \${service_name} \"*) ;; *) return 1 ;; esac
          container_id=\$(compose ps -q \"\${service_name}\")
          [[ \"\$(docker inspect --format '{{.State.Health.Status}}' \"\${container_id}\")\" == healthy ]] || return 1
        done
        curl -fsS --connect-timeout 2 --max-time 4 \
          -H 'Host: ${ARBUZAS_QBITTORRENT_TAILSCALE_HOSTNAME}:${ARBUZAS_QBITTORRENT_INTERNAL_WEBUI_PORT}' \
          http://127.0.0.1:${ARBUZAS_QBITTORRENT_WEBUI_PORT}/api/v2/app/version \
          | grep -Fx v5.2.3 >/dev/null || return 1
      fi
      if [[ '${VALIDATE_JELLYFIN}' == '1' ]]; then
        case \" \${running} \" in *' jellyfin '*) ;; *) return 1 ;; esac
        container_id=\$(compose ps -q jellyfin)
        [[ -n \"\${container_id}\" ]] || return 1
        health_status=\$(docker inspect --format '{{.State.Health.Status}}' \"\${container_id}\" 2>/dev/null || true)
        [[ \"\${health_status}\" == healthy ]] || return 1
        curl -fsS --connect-timeout 2 --max-time 4 \
          'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}/health' \
          | grep -Fx Healthy >/dev/null || return 1
        sudo -n python3 '${remote_release_dir}/infra/arbuzas/jellyfin/bootstrap.py' check \
          --url 'http://127.0.0.1:${ARBUZAS_JELLYFIN_HOST_PORT}' \
          --admin-password-file '${JELLYFIN_REMOTE_ADMIN_PASSWORD_FILE}' >/dev/null || return 1
      fi
      if [[ '${VALIDATE_TICKET_REMOTE}' == '1' ]]; then
        for service_name in ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel; do
          case \" \${running} \" in
            *\" \${service_name} \"*) ;;
            *) return 1 ;;
          esac
        done
        start_ticket_smoke_probe 'ticket phone bridge local health' \"compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null'\" || return 1
        start_ticket_smoke_probe 'Ticket Remote direct bridge health' \"compose exec -T ticket_remote sh -lc 'curl -fsS http://ticket_phone_bridge:9388/api/v1/health >/dev/null'\" || return 1
        start_ticket_smoke_probe 'Ticket Remote Spacetime sidecar health' \"compose exec -T ticket_remote_spacetime_sidecar sh -lc 'curl -fsS http://127.0.0.1:9346/healthz | grep -F \\\"status\\\" >/dev/null'\" || return 1
        start_ticket_smoke_probe 'Ticket Remote livez' \"compose exec -T ticket_remote sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null'\" || return 1
        start_ticket_smoke_probe 'Ticket Remote public page' 'public_code=\$(curl -sS --connect-timeout 2 --max-time 4 -o /dev/null -w \"%{http_code}\" https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/ 2>/dev/null || true); case \"\${public_code}\" in 200|302) ;; *) echo \"expected Ticket Remote public page status 200 or 302, got \${public_code}\" >&2; exit 1 ;; esac' || return 1
        start_ticket_smoke_probe 'Ticket Remote public health authorization' 'public_code=\$(curl -sS --connect-timeout 2 --max-time 4 -o /dev/null -w \"%{http_code}\" https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/api/v1/health 2>/dev/null || true); [[ \"\${public_code}\" == 401 ]] || { echo \"expected Ticket Remote health status 401, got \${public_code}\" >&2; exit 1; }' || return 1
        collect_ticket_smoke_probe_status || return 1
      fi
    }

    deadline=\$((SECONDS + ${ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS}))
    until smoke_ready; do
      if (( SECONDS >= deadline )); then
        echo 'selected services did not pass batched smoke readiness before timeout' >&2
        compose ps >&2 || true
        exit 1
      fi
      sleep 1
    done
  " ${diagnostics_services[@]+"${diagnostics_services[@]}"}
}

validate_remote_workload_health() {
  local remote_release_dir="$1"

  validate_remote_train_workload_health "${remote_release_dir}"
  validate_remote_satiksme_workload_health "${remote_release_dir}"
  validate_remote_subscription_workload_health "${remote_release_dir}"
  validate_remote_ticket_phone_bridge_workload_health "${remote_release_dir}"
  validate_remote_qbittorrent_workload_health "${remote_release_dir}"
  validate_remote_jellyfin_workload_health "${remote_release_dir}"
  validate_remote_ticket_remote_workload_health "${remote_release_dir}"
  validate_remote_tiny_vless_workload_health "${remote_release_dir}" "${VALIDATION_PROFILE}"
}

validate_remote_selected_workload_health() {
  local remote_release_dir="$1"

  if (( VALIDATE_TRAIN == 1 )); then
    validate_remote_train_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_SATIKSME == 1 )); then
    validate_remote_satiksme_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_SUBSCRIPTION == 1 )); then
    validate_remote_subscription_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_TICKET_PHONE_BRIDGE == 1 )); then
    validate_remote_ticket_phone_bridge_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_QBITTORRENT == 1 )); then
    validate_remote_qbittorrent_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_JELLYFIN == 1 )); then
    validate_remote_jellyfin_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_TICKET_REMOTE == 1 )); then
    validate_remote_ticket_remote_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_TINY_VLESS == 1 )); then
    validate_remote_tiny_vless_workload_health "${remote_release_dir}" "${VALIDATION_PROFILE}"
  fi
}

validate_remote_current_release_link() {
  local remote_release_dir="$1"
  local diagnostics_services=()

  populate_current_diagnostic_services diagnostics_services

  validate_remote_host_probe "${remote_release_dir}" \
    "current release link updated" \
    "
      current_target=\$(readlink '${REMOTE_CURRENT_LINK}')
      [[ \"\${current_target}\" == '${remote_release_dir}' ]] || {
        echo \"${REMOTE_CURRENT_LINK} points to \${current_target}, expected ${remote_release_dir}\" >&2
        exit 1
      }
    " \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}
}

validate_remote_swarm_baseline() {
  local remote_release_dir="$1"
  local diagnostics_services=()

  populate_current_diagnostic_services diagnostics_services

  validate_remote_host_probe "${remote_release_dir}" \
    "swarm inactive" \
    "
      swarm_state=\$(docker info --format '{{.Swarm.LocalNodeState}}')
      if [[ \"\${swarm_state}\" != 'inactive' ]]; then
        echo \"docker swarm must be inactive (found: \${swarm_state})\" >&2
        exit 1
      fi
    " \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}

  validate_remote_host_probe "${remote_release_dir}" \
    "swarm service and stack lists empty" \
    "
      services=\$(docker service ls --format '{{.Name}}' 2>/dev/null || true)
      stacks=\$(docker stack ls --format '{{.Name}}' 2>/dev/null || true)
      if [[ -n \"\$(printf '%s' \"\${services}\" | awk 'NF')\" ]]; then
        echo \"active Docker Swarm services detected: \${services}\" >&2
        exit 1
      fi
      if [[ -n \"\$(printf '%s' \"\${stacks}\" | awk 'NF')\" ]]; then
        echo \"active Docker Swarm stacks detected: \${stacks}\" >&2
        exit 1
      fi
    " \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}
}

validate_remote_host_baseline() {
  local remote_release_dir="$1"

  validate_remote_private_configuration_permissions "${remote_release_dir}"
  validate_remote_swarm_baseline "${remote_release_dir}"
  validate_remote_retired_portainer_absence "${remote_release_dir}"
  validate_remote_retired_chatgpt_absence "${remote_release_dir}"
}

validate_remote_private_configuration_permissions() {
  local remote_release_dir="$1"
  local diagnostics_services=()

  populate_current_diagnostic_services diagnostics_services
  validate_remote_host_probe "${remote_release_dir}" \
    "private deployment configuration permissions" \
    "
      assert_private_file() {
        local path=\"\$1\"
        local expected=\"\$2\"
        [[ -f \"\${path}\" && ! -L \"\${path}\" ]] || {
          echo \"missing or unsafe private deployment file: \${path}\" >&2
          return 1
        }
        [[ \"\$(stat -c '%u:%g:%a' \"\${path}\")\" == \"\${expected}\" ]] || {
          echo \"private deployment file has unsafe ownership or mode: \${path}\" >&2
          return 1
        }
      }

      for path in \
        '/etc/arbuzas/env/train-bot.env' \
        '/etc/arbuzas/env/satiksme-bot.env' \
        '/etc/arbuzas/env/subscription-bot.env'; do
        assert_private_file \"\${path}\" '0:0:600'
      done
      assert_private_file '/etc/arbuzas/env/ticket-remote.env' '1001:1001:600'

      if grep -Eq '^SATIKSME_CHAT_ANALYZER_(API_ID|API_HASH|GOOGLE_API_KEY|MODEL_API_KEY)=.+' \
          '/etc/arbuzas/env/satiksme-bot.env'; then
        echo 'Satiksme analyzer credentials remain inline in the host env' >&2
        exit 1
      fi
      for file_key in API_ID API_HASH GOOGLE_API_KEY MODEL_API_KEY; do
        grep -Eq \"^SATIKSME_CHAT_ANALYZER_\${file_key}_FILE=.+\" \
          '/etc/arbuzas/env/satiksme-bot.env' || {
          echo \"Satiksme analyzer file-backed credential is missing: \${file_key}\" >&2
          exit 1
        }
      done

      for path in \
        '/etc/arbuzas/secrets/satiksme-chat-analyzer/telegram-api-id.secret' \
        '/etc/arbuzas/secrets/satiksme-chat-analyzer/telegram-api-hash.secret' \
        '/etc/arbuzas/secrets/satiksme-chat-analyzer/google-api-key.secret' \
        '/etc/arbuzas/secrets/satiksme-chat-analyzer/model-api-key.secret'; do
        assert_private_file \"\${path}\" '0:0:600'
        [[ -s \"\${path}\" ]] || {
          echo \"required Satiksme analyzer credential is empty: \${path}\" >&2
          exit 1
        }
      done

      analyzer_enabled=\$(sed -n 's/^SATIKSME_CHAT_ANALYZER_ENABLED=//p' \
        '/etc/arbuzas/env/satiksme-bot.env' | tail -1)
      if [[ \"\${analyzer_enabled}\" == 'true' ]]; then
        path='/srv/arbuzas/satiksme-bot/state/chat-analyzer.session'
        assert_private_file \"\${path}\" '0:0:600'
        [[ -s \"\${path}\" ]] || {
          echo \"required Satiksme analyzer session is empty: \${path}\" >&2
          exit 1
        }
      fi

      if find '/etc/arbuzas/env' -mindepth 1 -maxdepth 1 \
          \( -name '*.bak*' -o -name '*.before-*' -o -name '*.retired-*' -o -name '*~' \) \
          -print -quit | grep -q .; then
        echo 'unmanaged host environment history remains under /etc/arbuzas/env' >&2
        exit 1
      fi
    " \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}
}

validate_remote_retired_portainer_absence() {
  local remote_release_dir="$1"
  local diagnostics_services=()

  populate_current_diagnostic_services diagnostics_services

  validate_remote_host_probe "${remote_release_dir}" \
    "retired Portainer container and port stay absent" \
    "
      container_ids=\$(docker ps -aq \
        --filter 'label=com.docker.compose.project=arbuzas' \
        --filter 'label=com.docker.compose.service=portainer')
      [[ -z \"\${container_ids}\" ]] || {
        echo \"retired Portainer containers remain: \${container_ids}\" >&2
        exit 1
      }
      listeners=\$(ss -ltnH 'sport = :9443' || true)
      [[ -z \"\${listeners}\" ]] || {
        echo \"retired Portainer port 9443 is still listening: \${listeners}\" >&2
        exit 1
      }
    " \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}
}

validate_remote_retired_chatgpt_absence() {
  local remote_release_dir="$1"
  local diagnostics_services=()

  populate_current_diagnostic_services diagnostics_services

  validate_remote_host_probe "${remote_release_dir}" \
    "retired ChatGPT containers and broker port stay absent" \
    "
      for service_name in chatgpt_broker chatgpt_bot; do
        container_ids=\$(docker ps -aq \\
          --filter 'label=com.docker.compose.project=arbuzas' \\
          --filter \"label=com.docker.compose.service=\${service_name}\")
        [[ -z \"\${container_ids}\" ]] || {
          echo \"retired ChatGPT containers remain for \${service_name}: \${container_ids}\" >&2
          exit 1
        }
      done
      listeners=\$(ss -ltnH 'sport = :9348' || true)
      [[ -z \"\${listeners}\" ]] || {
        echo \"retired ChatGPT broker port 9348 is still listening: \${listeners}\" >&2
        exit 1
      }
    " \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}
}

validate_remote_release() {
  local target_release_id="${1:-${requested_release_id}}"
  local remote_release_dir
  local diagnostics_services=()
  local REMOTE_VALIDATION_FAILED=0
  remote_release_dir="$(resolve_remote_release_dir "${target_release_id}")"
  populate_current_diagnostic_services diagnostics_services

  if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
    validate_remote_selected_smoke_health "${remote_release_dir}" 0
    if (( VALIDATE_TINY_VLESS == 1 )); then
      validate_remote_tiny_vless_workload_health "${remote_release_dir}" fast
    fi
    return_remote_validation_status
    return
  fi

  validate_remote_probe "${remote_release_dir}" \
    "release bundle exists" \
    "[[ -f '${remote_release_dir}/release.env' ]]" \
    ${diagnostics_services[@]+"${diagnostics_services[@]}"}

  if (( TARGETED_MODE == 1 )); then
    validate_remote_selected_workload_health "${remote_release_dir}"
    if [[ "${VALIDATION_PROFILE}" == "full" ]]; then
      validate_remote_host_baseline "${remote_release_dir}"
    fi
    return_remote_validation_status
    return
  fi

  validate_remote_workload_health "${remote_release_dir}"
  validate_remote_host_baseline "${remote_release_dir}"
  return_remote_validation_status
}

validate_deployed_release() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"

  if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
    validate_remote_selected_smoke_health "${remote_release_dir}" 1 || return $?
    if (( VALIDATE_TINY_VLESS == 1 )); then
      validate_remote_tiny_vless_workload_health "${remote_release_dir}" fast
    fi
    return
  fi
  validate_remote_current_release_link "${remote_release_dir}" && validate_remote_release "${ARBUZAS_RELEASE_ID}"
}

run_post_deploy_maintenance() {
  local protect_release_id="${1:-${ARBUZAS_RELEASE_ID}}"

  if ! run_local_release_cleanup "${protect_release_id}"; then
    log "Cleanup warning: local release cleanup failed, but the validated release remains successful"
  fi
  if [[ "${VALIDATION_PROFILE}" != "full" ]]; then
    log "Remote cleanup deferred by ${VALIDATION_PROFILE} profile; local expired release cleanup still completed"
    return 0
  fi
  cleanup_remote_public_bundle_versions
  run_automatic_remote_docker_gc
}

rollback_remote_release() {
  if [[ -z "${requested_release_id}" ]]; then
    echo "--release-id is required for rollback" >&2
    exit 2
  fi
  ARBUZAS_RELEASE_ID="${requested_release_id}"
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  local rollback_service_args=""
  local rollback_tunnel_service_args=""
  if (( TARGETED_MODE == 1 )); then
    rollback_service_args="$(compose_target_service_args_without_tunnels)"
    rollback_tunnel_service_args="$(compose_target_tunnel_service_args)"
  else
    rollback_service_args="$(compose_all_service_args)"
  fi
  remote_shell "
    [[ -f '${remote_release_dir}/release.env' ]] || { echo 'missing release bundle: ${remote_release_dir}' >&2; exit 1; }
    cd '${remote_release_dir}'
    sudo -n ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    compose_args=(docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml')
    for retired_service in portainer chatgpt_broker chatgpt_bot; do
      retired_container_ids=\$(docker ps -aq \
        --filter 'label=com.docker.compose.project=arbuzas' \
        --filter \"label=com.docker.compose.service=\${retired_service}\")
      if [[ -n \"\${retired_container_ids}\" ]]; then
        if \"\${compose_args[@]}\" config --services | grep -Fxq \"\${retired_service}\"; then
          \"\${compose_args[@]}\" rm -s -f \"\${retired_service}\"
        else
          docker rm -f \${retired_container_ids}
        fi
      fi
    done
    if [[ '${TARGETED_MODE}' == '1' ]]; then
      if [[ -n '${rollback_service_args}' ]]; then
        \"\${compose_args[@]}\" up -d --build --force-recreate --no-deps${rollback_service_args}
      fi
      if [[ -n '${rollback_tunnel_service_args}' ]]; then
        \"\${compose_args[@]}\" up -d --force-recreate --no-deps${rollback_tunnel_service_args}
      fi
    else
      \"\${compose_args[@]}\" up -d --remove-orphans${rollback_service_args}
    fi
  " || return $?
  stabilize_remote_declared_docker_no_swap_limits
}

run_host_mirror() {
  local mirror_action="$1"
  shift || true
  local args=()
  args=("${HOST_MIRROR_SCRIPT}" "${mirror_action}" --profile "${HOST_MIRROR_PROFILE}" --mirror-root "${HOST_MIRROR_ROOT}" --ssh-target "$(remote_target)")
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(--ssh-port "${ARBUZAS_SSH_PORT}")
  fi
  if [[ -n "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" ]]; then
    args+=(--ssh-known-hosts-file "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}")
  fi
  ARBUZAS_HOST_MIRROR_PRIVILEGED="${ARBUZAS_HOST_MIRROR_PRIVILEGED:-1}" \
    python3 "${args[@]}" "$@"
}

run_host_mirror_push() {
  local changed_paths_file="$1"
  local args=()
  args=("${HOST_MIRROR_SCRIPT}" push --profile "${HOST_MIRROR_PROFILE}" --mirror-root "${HOST_MIRROR_ROOT}" --ssh-target "$(remote_target)" --changed-paths-file "${changed_paths_file}")
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(--ssh-port "${ARBUZAS_SSH_PORT}")
  fi
  if [[ -n "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" ]]; then
    args+=(--ssh-known-hosts-file "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}")
  fi
  ARBUZAS_HOST_MIRROR_PRIVILEGED="${ARBUZAS_HOST_MIRROR_PRIVILEGED:-1}" \
    python3 "${args[@]}"
}

host_mirror_affected_services() {
  local changed_paths_file="$1"
  ARBUZAS_HOST_MIRROR_PRIVILEGED="${ARBUZAS_HOST_MIRROR_PRIVILEGED:-1}" \
    python3 "${HOST_MIRROR_SCRIPT}" affected --profile "${HOST_MIRROR_PROFILE}" --changed-paths-file "${changed_paths_file}"
}

csv_join_services() {
  local joined=""
  local service_name
  for service_name in "$@"; do
    if [[ -z "${joined}" ]]; then
      joined="${service_name}"
    else
      joined="${joined},${service_name}"
    fi
  done
  printf '%s' "${joined}"
}

prepare_remote_tiny_vless_config_rollback() {
  local output_variable="$1"
  local backup_root=""
  backup_root="$(remote_root_command "
    base='/srv/arbuzas/tiny-vless/config-rollbacks'
    mkdir -p \"\${base}\"
    chown root:root \"\${base}\"
    chmod 0700 \"\${base}\"
    rollback_dir=\$(mktemp -d \"\${base}/pending.XXXXXX\")
    chmod 0700 \"\${rollback_dir}\"
    if [[ -f /etc/arbuzas/env/tiny-vless.env && ! -L /etc/arbuzas/env/tiny-vless.env ]]; then
      cp -a /etc/arbuzas/env/tiny-vless.env \"\${rollback_dir}/environment\"
      touch \"\${rollback_dir}/environment.present\"
    fi
    if [[ -d /etc/arbuzas/secrets/tiny-vless && ! -L /etc/arbuzas/secrets/tiny-vless ]]; then
      cp -a /etc/arbuzas/secrets/tiny-vless \"\${rollback_dir}/secrets\"
      touch \"\${rollback_dir}/secrets.present\"
    fi
    printf '%s\n' \"\${rollback_dir}\"
  " 1 | tail -n 1 | tr -d '\r\n')" || return $?
  [[ "${backup_root}" =~ ^/srv/arbuzas/tiny-vless/config-rollbacks/pending\.[A-Za-z0-9]+$ ]] || {
    echo "invalid tiny-vless config rollback path" >&2
    return 1
  }
  printf -v "${output_variable}" '%s' "${backup_root}"
}

restore_remote_tiny_vless_config_rollback() {
  local backup_root="$1"
  [[ "${backup_root}" =~ ^/srv/arbuzas/tiny-vless/config-rollbacks/pending\.[A-Za-z0-9]+$ ]] || {
    echo "refusing unsafe tiny-vless config rollback path" >&2
    return 1
  }
  remote_root_command "
    rollback_dir=$(shell_quote "${backup_root}")
    [[ -d \"\${rollback_dir}\" && ! -L \"\${rollback_dir}\" ]] || {
      echo 'missing tiny-vless config rollback snapshot' >&2
      exit 1
    }
    rm -f /etc/arbuzas/env/tiny-vless.env
    rm -rf /etc/arbuzas/secrets/tiny-vless
    if [[ -f \"\${rollback_dir}/environment.present\" ]]; then
      cp -a \"\${rollback_dir}/environment\" /etc/arbuzas/env/tiny-vless.env
    fi
    if [[ -f \"\${rollback_dir}/secrets.present\" ]]; then
      cp -a \"\${rollback_dir}/secrets\" /etc/arbuzas/secrets/tiny-vless
    fi
  " 1
}

cleanup_remote_tiny_vless_config_rollback() {
  local backup_root="$1"
  [[ -z "${backup_root}" ]] && return 0
  [[ "${backup_root}" =~ ^/srv/arbuzas/tiny-vless/config-rollbacks/pending\.[A-Za-z0-9]+$ ]] || return 1
  remote_root_command "rm -rf -- $(shell_quote "${backup_root}")" 1
}

deploy_config_from_mirror() {
  local changed_paths_file
  local affected_output=""
  local -a affected_services=()
  local -a compose_services=()
  local service_name=""
  local service_args=""
  local tiny_vless_changed=0
  local tiny_vless_config_rollback=""
  changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
  prepare_remote_tiny_vless_config_rollback tiny_vless_config_rollback || return $?
  trap 'cleanup_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" >/dev/null 2>&1 || true; rm -f "${changed_paths_file}"; trap - RETURN' RETURN

  harden_remote_release_env_permissions || return $?
  run_host_mirror_push "${changed_paths_file}" || return $?
  if grep -Eq '^etc/arbuzas/(env/tiny-vless\.env|secrets/tiny-vless(/|$))' "${changed_paths_file}"; then
    tiny_vless_changed=1
  fi
  if ! prepare_remote_ticket_runtime_permissions; then
    if (( tiny_vless_changed == 1 )); then
      restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || true
    fi
    return 1
  fi
  if affected_output="$(host_mirror_affected_services "${changed_paths_file}")"; then
    :
  else
    if (( tiny_vless_changed == 1 )); then
      restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || true
    fi
    return 1
  fi
  if [[ -z "${affected_output}" ]]; then
    log "Deploy config: mirror is already in sync; no services need restart"
    return 0
  fi

  while IFS= read -r service_name; do
    [[ -n "${service_name}" ]] || continue
    affected_services+=("${service_name}")
  done <<< "${affected_output}"

  log "Deploy config: affected services $(csv_join_services "${affected_services[@]}")"
  for service_name in "${affected_services[@]}"; do
    if [[ "${service_name}" == "tiny_vless" ]]; then
      tiny_vless_changed=1
    else
      compose_services+=("${service_name}")
      service_args+=" ${service_name}"
    fi
  done
  remote_shell "
    [[ -f '${REMOTE_CURRENT_LINK}/release.env' ]] || { echo 'missing active release: ${REMOTE_CURRENT_LINK}/release.env' >&2; exit 1; }
    [[ -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' ]] || { echo 'missing active compose file under ${REMOTE_CURRENT_LINK}' >&2; exit 1; }
  " || {
    if (( tiny_vless_changed == 1 )); then
      restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || true
    fi
    return 1
  }
  if (( ${#compose_services[@]} > 0 )); then
    remote_shell "
      cd '${REMOTE_CURRENT_LINK}'
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${service_args}
    " || {
      if (( tiny_vless_changed == 1 )); then
        restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || true
      fi
      return 1
    }
    stabilize_remote_declared_docker_no_swap_limits
  fi
  if (( tiny_vless_changed == 1 )); then
    if ! run_remote_tiny_vless_manager_at_source \
      deploy \
      "${REMOTE_CURRENT_LINK}/infra/arbuzas/tiny-vless" \
      standard; then
      restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || return 1
      run_remote_tiny_vless_manager_at_source \
        abort \
        "${REMOTE_CURRENT_LINK}/infra/arbuzas/tiny-vless" \
        standard || return 1
      return 1
    fi
    if ! run_remote_tiny_vless_manager_at_source \
      validate \
      "${REMOTE_CURRENT_LINK}/infra/arbuzas/tiny-vless" \
      standard; then
      restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || return 1
      run_remote_tiny_vless_manager_at_source \
        abort \
        "${REMOTE_CURRENT_LINK}/infra/arbuzas/tiny-vless" \
        standard || return 1
      return 1
    fi
    if ! run_remote_tiny_vless_manager_at_source \
      commit \
      "${REMOTE_CURRENT_LINK}/infra/arbuzas/tiny-vless" \
      standard; then
      restore_remote_tiny_vless_config_rollback "${tiny_vless_config_rollback}" || return 1
      run_remote_tiny_vless_manager_at_source \
        abort \
        "${REMOTE_CURRENT_LINK}/infra/arbuzas/tiny-vless" \
        standard || return 1
      return 1
    fi
  fi
}

while (( $# > 0 )); do
  case "$1" in
    deploy|validate|rollback|cleanup-docker|memory-report|install-memory-report|validate-memory-report|install-netdata|validate-netdata|install-thinkpad-fan|validate-thinkpad-fan|mirror-pull|mirror-audit|mirror-push|deploy-config)
      if [[ -n "${action}" ]]; then
        echo "Only one action is allowed" >&2
        exit 2
      fi
      action="$1"
      ;;
    --release-id)
      shift
      requested_release_id="${1:-}"
      ;;
    --services)
      local_services_before="${#REQUESTED_SERVICES[@]}"
      shift
      if [[ -z "${1:-}" ]]; then
        echo "--services requires a value" >&2
        exit 2
      fi
      IFS=',' read -r -a parsed_services <<< "${1}"
      for service_name in "${parsed_services[@]}"; do
        service_name="$(trim_whitespace "${service_name}")"
        if [[ -z "${service_name}" ]]; then
          continue
        fi
        if ! is_known_service "${service_name}"; then
          echo "Unknown service: ${service_name}" >&2
          exit 2
        fi
        append_unique REQUESTED_SERVICES "${service_name}"
      done
      if [[ "${#REQUESTED_SERVICES[@]}" == "${local_services_before}" ]]; then
        echo "--services requires at least one service name" >&2
        exit 2
      fi
      ;;
    --validation-profile|--validation-level)
      shift
      VALIDATION_PROFILE="${1:-}"
      VALIDATION_PROFILE_OPTION_SET=1
      if [[ -z "${VALIDATION_PROFILE}" ]]; then
        echo "--validation-profile requires a value" >&2
        exit 2
      fi
      ;;
    --ssh-host)
      shift
      ARBUZAS_HOST="${1:-}"
      ;;
    --ssh-user)
      shift
      ARBUZAS_USER="${1:-}"
      ;;
    --ssh-port)
      shift
      ARBUZAS_SSH_PORT="${1:-}"
      ;;
    --ssh-known-hosts-file)
      shift
      ARBUZAS_SSH_KNOWN_HOSTS_FILE="${1:-}"
      ;;
    --env-file)
      shift
      if [[ -f "${1:-}" ]]; then
        set -a
        # shellcheck disable=SC1090
        . "${1}"
        set +a
      fi
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if [[ -z "${action}" ]]; then
  usage >&2
  exit 2
fi

if [[ -n "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" ]]; then
  [[ "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" == /* ]] || {
    echo "--ssh-known-hosts-file must be an absolute path" >&2
    exit 2
  }
  [[ -f "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" && -r "${ARBUZAS_SSH_KNOWN_HOSTS_FILE}" ]] || {
    echo "--ssh-known-hosts-file must name a readable file" >&2
    exit 2
  }
fi

validate_validation_profile
validate_qbittorrent_fixed_parameters
validate_jellyfin_fixed_parameters

if (( VALIDATION_PROFILE_OPTION_SET == 1 )); then
  case "${action}" in
    deploy|validate|rollback)
      ;;
    *)
      echo "--validation-profile is only supported for deploy, validate, and rollback" >&2
      exit 2
      ;;
  esac
fi

if (( ${#REQUESTED_SERVICES[@]} > 0 )); then
  case "${action}" in
    deploy|validate|rollback)
      ;;
    *)
      echo "--services is only supported for deploy, validate, and rollback" >&2
      exit 2
      ;;
  esac
fi

resolve_requested_services

case "${action}" in
  deploy|validate|rollback)
    if [[ "${VALIDATION_PROFILE}" != "full" ]] && (( TARGETED_MODE == 0 )); then
      echo "Validation profile ${VALIDATION_PROFILE} requires --services; unscoped operations use the full profile" >&2
      exit 2
    fi
    ;;
esac

start_deployment_timing_reporting

require_cmd ssh
require_cmd python3
case "${action}" in
  memory-report)
    ;;
  *)
    require_cmd scp
    ;;
esac
case "${action}" in
  memory-report|install-memory-report|validate-memory-report|mirror-pull|mirror-audit|mirror-push|deploy-config)
    ;;
  *)
    require_cmd go
    require_cmd curl
    ;;
esac

case "${action}" in
  mirror-pull)
    run_host_mirror pull
    ;;
  mirror-audit)
    run_host_mirror audit
    ;;
  mirror-push)
    changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
    trap 'rm -f "${changed_paths_file}"' EXIT
    run_host_mirror_push "${changed_paths_file}"
    if [[ -s "${changed_paths_file}" ]]; then
      log "Mirror push changed:"
      sed 's/^/  /' "${changed_paths_file}"
    fi
    ;;
  deploy-config)
    run_timed_phase deploy_config deploy_config_from_mirror
    ;;
  deploy)
    require_cmd tar
    ARBUZAS_RELEASE_ID="${requested_release_id:-${ARBUZAS_RELEASE_ID}}"
    ARBUZAS_RELEASE_DIR="${LOCAL_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
    enforce_release_source_policy
    previous_release_id=""
    run_timed_phase resolve_current_release resolve_remote_current_release_id previous_release_id || true
    if (( TARGETED_MODE == 1 )); then
      log "Deploy: targeted services $(csv_join_services "${REQUESTED_SERVICES[@]}") profile=${VALIDATION_PROFILE}"
      if tiny_vless_deployment_selected; then
        log "Deploy: external component tiny_vless profile=${VALIDATION_PROFILE}"
      fi
    fi
    mirror_changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
    DEPLOYMENT_TIMING_EXIT_CLEANUP_PATH="${mirror_changed_paths_file}"
    run_timed_phase mirror_push run_host_mirror_push "${mirror_changed_paths_file}"
    run_timed_phase prepare_ticket_permissions prepare_remote_ticket_runtime_permissions
    run_timed_phase package_release prepare_deploy_release_payload
    if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
      log "Host layout preparation skipped: fast profile reuses the active complete release"
    else
      run_timed_phase prepare_host prepare_remote_host_layout
    fi
    run_timed_phase upload_release copy_deploy_release_payload
    if ! run_timed_phase adopt_tiny_vless adopt_remote_tiny_vless; then
      exit 1
    fi
    if ! run_timed_phase render_tunnels render_deploy_cloudflared_configs; then
      exit 1
    fi
    if ! run_timed_phase prepare_qbittorrent prepare_remote_qbittorrent_runtime; then
      exit 1
    fi
    if ! run_timed_phase prepare_jellyfin prepare_remote_jellyfin_runtime; then
      exit 1
    fi
    deploy_ready_for_validation=1
    tiny_vless_deploy_completed=0
    if ! run_timed_phase restart_services remote_compose_up; then
      deploy_ready_for_validation=0
    fi
    if (( deploy_ready_for_validation == 1 )); then
      if run_timed_phase restart_tiny_vless deploy_remote_tiny_vless; then
        if tiny_vless_deployment_selected; then
          tiny_vless_deploy_completed=1
        fi
      else
        if tiny_vless_deployment_selected; then
          run_timed_phase abort_tiny_vless abort_remote_tiny_vless || true
        fi
        deploy_ready_for_validation=0
      fi
    fi
    if (( deploy_ready_for_validation == 1 )) && ! run_timed_phase bootstrap_jellyfin bootstrap_remote_jellyfin; then
      deploy_ready_for_validation=0
    fi
    if (( deploy_ready_for_validation == 1 )) && ! run_timed_phase publish_qbittorrent publish_remote_qbittorrent_tailscale; then
      deploy_ready_for_validation=0
    fi
    if (( deploy_ready_for_validation == 1 )) && ! run_timed_phase publish_jellyfin publish_remote_jellyfin_tailscale; then
      deploy_ready_for_validation=0
    fi
    if (( deploy_ready_for_validation == 1 )) && run_timed_phase validate_release validate_deployed_release; then
      if run_timed_phase commit_tiny_vless commit_remote_tiny_vless; then
        run_timed_phase post_deploy_maintenance run_post_deploy_maintenance
        exit 0
      fi
    fi
    if (( tiny_vless_deploy_completed == 1 )); then
      if ! run_timed_phase rollback_tiny_vless rollback_remote_tiny_vless; then
        log "Tiny-VLESS rollback failed; retaining the current release pointer for explicit recovery"
        exit 1
      fi
    fi
    if [[ -n "${previous_release_id}" && "${previous_release_id}" != "${ARBUZAS_RELEASE_ID}" ]]; then
      log "Deploy validation failed; rolling back to ${previous_release_id}"
      requested_release_id="${previous_release_id}"
      if qbittorrent_deployment_selected && ! previous_release_has_qbittorrent "${previous_release_id}"; then
        run_timed_phase rollback_release rollback_release_before_qbittorrent "${previous_release_id}" owned-only
        if jellyfin_deployment_selected && ! previous_release_has_jellyfin "${previous_release_id}"; then
          run_timed_phase rollback_jellyfin_route remove_remote_jellyfin_tailscale_route owned-only
        fi
        run_timed_phase validate_rollback validate_release_before_qbittorrent_recovery "${previous_release_id}" owned-only
      else
        if qbittorrent_deployment_selected && previous_release_has_qbittorrent "${previous_release_id}"; then
          run_timed_phase prepare_rollback_qbittorrent prepare_remote_qbittorrent_runtime "${previous_release_id}" 1
        fi
        if jellyfin_deployment_selected && previous_release_has_jellyfin "${previous_release_id}"; then
          run_timed_phase prepare_rollback_jellyfin prepare_remote_jellyfin_runtime "${previous_release_id}" 1
        fi
        if jellyfin_deployment_selected && ! previous_release_has_jellyfin "${previous_release_id}"; then
          run_timed_phase rollback_release rollback_release_before_jellyfin "${previous_release_id}" owned-only
          run_timed_phase validate_rollback validate_release_before_jellyfin_recovery "${previous_release_id}" owned-only
        else
          run_timed_phase rollback_release rollback_remote_release
          if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
            run_timed_phase validate_rollback validate_remote_selected_smoke_health "${REMOTE_RELEASES_ROOT}/${previous_release_id}" 1
          else
            run_timed_phase validate_rollback validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${previous_release_id}"
            run_timed_phase validate_rollback_services validate_remote_release "${previous_release_id}"
          fi
        fi
      fi
    fi
    exit 1
    ;;
  validate)
    if (( TARGETED_MODE == 1 )); then
      log "Validate: targeted services $(csv_join_services "${REQUESTED_SERVICES[@]}") profile=${VALIDATION_PROFILE}"
      if (( VALIDATE_TINY_VLESS == 1 )); then
        log "Validate: external component tiny_vless profile=${VALIDATION_PROFILE}"
      fi
    fi
    run_timed_phase validate_release validate_remote_release "${requested_release_id}"
    ;;
  rollback)
    if [[ -z "${requested_release_id}" ]]; then
      echo "--release-id is required for rollback" >&2
      exit 2
    fi
    run_timed_phase harden_release_env_permissions harden_remote_release_env_permissions
    if qbittorrent_deployment_selected && ! previous_release_has_qbittorrent "${requested_release_id}"; then
      run_timed_phase rollback_release rollback_release_before_qbittorrent "${requested_release_id}" force-managed
      if jellyfin_deployment_selected && ! previous_release_has_jellyfin "${requested_release_id}"; then
        run_timed_phase rollback_jellyfin_route remove_remote_jellyfin_tailscale_route force-managed
      fi
      run_timed_phase validate_rollback validate_release_before_qbittorrent_recovery "${requested_release_id}" force-managed
    else
      if qbittorrent_deployment_selected && previous_release_has_qbittorrent "${requested_release_id}"; then
        run_timed_phase prepare_rollback_qbittorrent prepare_remote_qbittorrent_runtime "${requested_release_id}" 1
      fi
      if jellyfin_deployment_selected && ! previous_release_has_jellyfin "${requested_release_id}"; then
        run_timed_phase rollback_release rollback_release_before_jellyfin "${requested_release_id}" force-managed
        run_timed_phase validate_rollback validate_release_before_jellyfin_recovery "${requested_release_id}" force-managed
      else
        if jellyfin_deployment_selected && previous_release_has_jellyfin "${requested_release_id}"; then
          run_timed_phase prepare_rollback_jellyfin prepare_remote_jellyfin_runtime "${requested_release_id}" 1
        fi
        run_timed_phase rollback_release rollback_remote_release
        if tiny_vless_deployment_selected; then
          run_timed_phase rollback_tiny_vless deploy_remote_tiny_vless_from_release "${requested_release_id}"
        fi
        if jellyfin_deployment_selected && previous_release_has_jellyfin "${requested_release_id}"; then
          run_timed_phase bootstrap_rollback_jellyfin bootstrap_remote_jellyfin "${requested_release_id}" 1
          run_timed_phase publish_rollback_jellyfin publish_remote_jellyfin_tailscale
        fi
        if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
          run_timed_phase validate_rollback validate_remote_selected_smoke_health "${REMOTE_RELEASES_ROOT}/${requested_release_id}" 1
        else
          run_timed_phase validate_rollback_link validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${requested_release_id}"
          run_timed_phase validate_rollback_services validate_remote_release "${requested_release_id}"
        fi
      fi
    fi
    if tiny_vless_deployment_selected; then
      run_timed_phase validate_rollback_tiny_vless \
        validate_remote_tiny_vless_workload_health \
        "${REMOTE_RELEASES_ROOT}/${requested_release_id}" \
        "${VALIDATION_PROFILE}"
      run_timed_phase commit_rollback_tiny_vless commit_remote_tiny_vless
    fi
    run_timed_phase post_rollback_maintenance run_post_deploy_maintenance "${requested_release_id}"
    ;;
  cleanup-docker)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for cleanup-docker" >&2
      exit 2
    fi
    remote_run_docker_gc
    remote_run_host_cache_cleanup
    ;;
  memory-report)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for memory-report" >&2
      exit 2
    fi
    remote_run_memory_report
    ;;
  install-memory-report)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for install-memory-report" >&2
      exit 2
    fi
    require_cmd base64
    remote_stage_root="$(stage_memory_report_config_to_remote)"
    install_remote_memory_report "${remote_stage_root}"
    validate_remote_memory_report
    ;;
  validate-memory-report)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for validate-memory-report" >&2
      exit 2
    fi
    validate_remote_memory_report
    ;;
  install-netdata)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for install-netdata" >&2
      exit 2
    fi
    require_cmd base64
    remote_stage_root="$(stage_netdata_config_to_remote)"
    netdata_rollback_root="/tmp/arbuzas-netdata-rollback.$$"
    prepare_remote_netdata_rollback "${netdata_rollback_root}"
    set -E
    trap '
      netdata_install_status=$?
      trap - ERR
      restore_remote_netdata_rollback "${netdata_rollback_root}" || true
      exit "${netdata_install_status}"
    ' ERR
    install_remote_netdata "${remote_stage_root}"
    validate_remote_netdata
    trap - ERR
    set +E
    cleanup_remote_netdata_rollback "${netdata_rollback_root}"
    ;;
  validate-netdata)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for validate-netdata" >&2
      exit 2
    fi
    validate_remote_netdata
    ;;
  install-thinkpad-fan)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for install-thinkpad-fan" >&2
      exit 2
    fi
    require_cmd base64
    remote_stage_root="$(stage_thinkpad_fan_config_to_remote)"
    install_remote_thinkpad_fan "${remote_stage_root}"
    validate_remote_thinkpad_fan
    ;;
  validate-thinkpad-fan)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for validate-thinkpad-fan" >&2
      exit 2
    fi
    validate_remote_thinkpad_fan
    ;;
esac
