#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DOCKER_ROOT="${REPO_ROOT}/infra/arbuzas/docker"
DOCKER_DEFAULT_ENV_FILE="${DOCKER_ROOT}/env/arbuzas.env"
LOCAL_RELEASES_ROOT="${REPO_ROOT}/output/arbuzas/releases"
HOST_MIRROR_SCRIPT="${REPO_ROOT}/tools/arbuzas/host_mirror.py"
HOST_MIRROR_ROOT="${ARBUZAS_HOST_MIRROR_ROOT:-${REPO_ROOT}/infra/arbuzas/host-mirror}"
REMOTE_RELEASES_ROOT="/etc/arbuzas/releases"
REMOTE_CURRENT_LINK="/etc/arbuzas/current"
REMOTE_PORTAINER_DATA_DIR="/srv/arbuzas/portainer"
REMOTE_PORTAINER_BACKUPS_DIR="/srv/arbuzas/portainer-backups"
PORTAINER_AGENT_ENDPOINT="tcp://tasks.agent:9001"
PORTAINER_LOCAL_ENDPOINT="unix:///var/run/docker.sock"
PORTAINER_DB_TOOL_DIR="${SCRIPT_DIR}/portainerdb"
PORTAINER_TOOLBOX_IMAGE="${PORTAINER_TOOLBOX_IMAGE:-busybox:1.36.1}"
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
ARBUZAS_TZ="${ARBUZAS_TZ:-Europe/Riga}"
ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
ARBUZAS_RELEASE_DIR="${ARBUZAS_RELEASE_DIR:-${LOCAL_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}}"

ARBUZAS_TRAIN_BOT_PORT="${ARBUZAS_TRAIN_BOT_PORT:-9317}"
ARBUZAS_SATIKSME_BOT_PORT="${ARBUZAS_SATIKSME_BOT_PORT:-9318}"
ARBUZAS_SUBSCRIPTION_BOT_PORT="${ARBUZAS_SUBSCRIPTION_BOT_PORT:-9320}"
ARBUZAS_TICKET_REMOTE_PORT="${ARBUZAS_TICKET_REMOTE_PORT:-9338}"
ARBUZAS_CHATGPT_BROKER_PORT="${ARBUZAS_CHATGPT_BROKER_PORT:-9348}"
ARBUZAS_TICKET_PHONE_ADB_TARGET="${ARBUZAS_TICKET_PHONE_ADB_TARGET:-100.76.50.43:5555}"
ARBUZAS_TICKET_TUNNEL_UID="${ARBUZAS_TICKET_TUNNEL_UID:-501}"
ARBUZAS_TICKET_TUNNEL_GID="${ARBUZAS_TICKET_TUNNEL_GID:-50}"
ARBUZAS_NETDATA_PORT="${ARBUZAS_NETDATA_PORT:-19999}"
ARBUZAS_TAILSCALE_IPV4="${ARBUZAS_TAILSCALE_IPV4:-}"
ARBUZAS_FAN_ENTER_AUTO_C="${ARBUZAS_FAN_ENTER_AUTO_C:-89}"
ARBUZAS_FAN_EXIT_AUTO_C="${ARBUZAS_FAN_EXIT_AUTO_C:-89}"

ARBUZAS_TRAIN_BOT_HOSTNAME="${ARBUZAS_TRAIN_BOT_HOSTNAME:-vilciens.kontrole.info}"
ARBUZAS_SATIKSME_BOT_HOSTNAME="${ARBUZAS_SATIKSME_BOT_HOSTNAME:-kontrole.info}"
ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME="${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME:-farel-subscription-bot.jolkins.id.lv}"
ARBUZAS_TICKET_REMOTE_HOSTNAME="${ARBUZAS_TICKET_REMOTE_HOSTNAME:-ticket.jolkins.id.lv}"
ARBUZAS_PORTAINER_IMAGE="${ARBUZAS_PORTAINER_IMAGE:-portainer/portainer-ce:lts}"
ARBUZAS_CLOUDFLARED_IMAGE="${ARBUZAS_CLOUDFLARED_IMAGE:-cloudflare/cloudflared:latest}"
ARBUZAS_TICKET_CLOUDFLARED_IMAGE="${ARBUZAS_TICKET_CLOUDFLARED_IMAGE:-cloudflare/cloudflared@sha256:12ff5c6992a9863db4da270746af7c244bcaee49353039af8104268a18d6c4f0}"

action=""
requested_release_id=""
VALIDATION_PROFILE="${ARBUZAS_VALIDATION_PROFILE:-full}"
VALIDATION_PROFILE_OPTION_SET=0
TARGETED_MODE=0
VALIDATE_PORTAINER=0
VALIDATE_TRAIN=0
VALIDATE_SATIKSME=0
VALIDATE_SUBSCRIPTION=0
VALIDATE_TICKET_PHONE_BRIDGE=0
VALIDATE_CHATGPT=0
VALIDATE_TICKET_REMOTE=0
REQUESTED_SERVICES=()
COMPOSE_TARGET_SERVICES=()
DIAGNOSTIC_SERVICES=()
FAST_RELEASE_OVERLAY_PATHS=()
RUN_STARTED_SECONDS="${SECONDS}"

ALL_SERVICES=(
  portainer
  train_bot
  satiksme_bot
  subscription_bot
  ticket_phone_bridge
  chatgpt_broker
  chatgpt_bot
  ticket_remote_spacetime_sidecar
  ticket_remote
  train_tunnel
  satiksme_tunnel
  subscription_tunnel
  ticket_remote_tunnel
)

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*" >&2
}

run_timed_phase() {
  local phase_name="$1"
  shift
  local phase_started_seconds="${SECONDS}"
  local phase_status="ok"
  local phase_exit_code=0

  log "Phase start: ${phase_name} profile=${VALIDATION_PROFILE}"
  if "$@"; then
    phase_exit_code=0
  else
    phase_exit_code=$?
    phase_status="failed"
  fi
  log "Phase timing: phase=${phase_name} status=${phase_status} duration_seconds=$((SECONDS - phase_started_seconds)) total_seconds=$((SECONDS - RUN_STARTED_SECONDS)) profile=${VALIDATION_PROFILE}"
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
  local script_base64=""
  local attempt=0

  script_base64="$(printf '%s\n' 'set -euo pipefail' "${script}" | base64 | tr -d '\n')"
  for attempt in 1 2 3; do
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
    if (( attempt < 3 )); then
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
      local deadline=\$((SECONDS + 90))
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
  local netdata_config_tree_base64=""
  local attempt=0

  [[ -d "${NETDATA_CONFIG_ROOT}" ]] || {
    echo "missing Netdata config root: ${NETDATA_CONFIG_ROOT}" >&2
    return 1
  }
  netdata_config_tree_base64="$(COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C "${NETDATA_CONFIG_ROOT}" -cf - . | base64 | tr -d '\n')"

  log "Staging Netdata config on ${ARBUZAS_HOST}:${remote_tmp_dir}"
  for attempt in 1 2 3; do
    if remote_inline_shell "
      rm -rf '${remote_tmp_dir}'
      install -d '${remote_tmp_dir}'
      printf '%s' '${netdata_config_tree_base64}' | base64 -d | tar -xf - -C '${remote_tmp_dir}'
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

install_remote_netdata() {
  local remote_stage_root="$1"

  log "Maintenance: installing Netdata and host collectors on ${ARBUZAS_HOST}"
  remote_root_command "
    command -v apt-get >/dev/null 2>&1 || {
      echo 'apt-get is required for Arbuzas Netdata install' >&2
      exit 1
    }

    export DEBIAN_FRONTEND=noninteractive
    apt-get update
    apt-get install -y ca-certificates curl lm-sensors smartmontools

    tmpdir=\$(mktemp -d)
    trap 'rm -rf \"\${tmpdir}\" \"${remote_stage_root}\"' EXIT

    curl -fsSL '${NETDATA_KICKSTART_URL}' -o \"\${tmpdir}/kickstart.sh\"
    DISABLE_TELEMETRY=1 sh \"\${tmpdir}/kickstart.sh\" \
      --stable-channel \
      --native-only \
      --non-interactive \
      --no-updates \
      --disable-telemetry

    install -d '${NETDATA_REMOTE_CONFIG_DIR}'
    tar -C '${remote_stage_root}' -cf - . | tar -C '${NETDATA_REMOTE_CONFIG_DIR}' -xf -

    rm -f /var/lib/netdata/cloud.d/claim.conf

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

    tailscale serve --bg --yes --tcp ${ARBUZAS_NETDATA_PORT} 127.0.0.1:${ARBUZAS_NETDATA_PORT}
  "
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

run_portainer_db_tool() {
  (
    cd "${PORTAINER_DB_TOOL_DIR}"
    go run . "$@"
  )
}

download_remote_portainer_db() {
  local local_db_path="$1"
  remote_shell "
    portainer_container_id=\$(docker ps -a \
      --filter 'label=com.docker.compose.project=arbuzas' \
      --filter 'label=com.docker.compose.service=portainer' \
      --format '{{.ID}}' | head -n 1)
    [[ -n \"\${portainer_container_id}\" ]] || { echo 'Portainer container not found' >&2; exit 1; }
    tmpfile=\$(mktemp /tmp/portainer.db.XXXXXX)
    trap 'rm -f \"\${tmpfile}\"' EXIT
    docker cp \"\${portainer_container_id}:/data/portainer.db\" \"\${tmpfile}\" >/dev/null
    cat \"\${tmpfile}\"
  " > "${local_db_path}"
}

upload_remote_file() {
  local local_path="$1"
  local remote_path="$2"
  local remote_tmp_path="${remote_path}.uploading.$$"
  local remote_path_q=""
  local remote_tmp_path_q=""

  remote_path_q="$(shell_quote "${remote_path}")"
  remote_tmp_path_q="$(shell_quote "${remote_tmp_path}")"

  run_ssh \
    -o ConnectTimeout=15 \
    -o ServerAliveInterval=15 \
    -o ServerAliveCountMax=3 \
    "$(remote_target)" \
    "set -euo pipefail;
     remote_path=${remote_path_q};
     remote_tmp_path=${remote_tmp_path_q};
     mkdir -p \"\$(dirname -- \"\${remote_path}\")\";
     trap 'rm -f -- \"\${remote_tmp_path}\"' EXIT;
     cat > \"\${remote_tmp_path}\";
     mv -f -- \"\${remote_tmp_path}\" \"\${remote_path}\"" \
    < "${local_path}"
}

backup_remote_portainer_data() {
  local backup_path="$1"
  local backup_filename="${backup_path##*/}"
  remote_shell "
    docker run --rm \
      -v '${REMOTE_PORTAINER_DATA_DIR}:/from:ro' \
      -v '${REMOTE_PORTAINER_BACKUPS_DIR}:/backup' \
      '${PORTAINER_TOOLBOX_IMAGE}' \
      sh -lc 'tar -C /from -czf \"/backup/${backup_filename}\" .'
  "
}

install_remote_portainer_db() {
  local local_db_path="$1"
  local remote_tmp_path="$2"

  upload_remote_file "${local_db_path}" "${remote_tmp_path}"
  remote_shell "
    portainer_container_id=\$(docker ps -a \
      --filter 'label=com.docker.compose.project=arbuzas' \
      --filter 'label=com.docker.compose.service=portainer' \
      --format '{{.ID}}' | head -n 1)
    [[ -n \"\${portainer_container_id}\" ]] || { echo 'Portainer container not found' >&2; exit 1; }
    docker cp '${remote_tmp_path}' \"\${portainer_container_id}:/data/portainer.db\" >/dev/null
    rm -f '${remote_tmp_path}'
  "
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
  validate-netdata  Validate the live Netdata host install, private Tailscale access, and expected Arbuzas hardware charts
  install-thinkpad-fan   Install the Arbuzas ThinkPad fan controller on the live host
  validate-thinkpad-fan  Validate the live ThinkPad fan controller and current control mode
  repair-portainer  Backup and repair Portainer state in place, disable Docker Swarm, and restart Portainer on the standalone Docker socket
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
  --env-file PATH

Services:
  portainer, train_bot, train_tunnel, satiksme_bot, satiksme_tunnel,
  subscription_bot, subscription_tunnel, ticket_phone_bridge,
  chatgpt_broker, chatgpt_bot, ticket_remote_spacetime_sidecar,
  ticket_remote, ticket_remote_tunnel
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

is_known_service() {
  local service_name="$1"
  array_contains "${service_name}" "${ALL_SERVICES[@]}"
}

mark_validation_group() {
  local group_name="$1"
  case "${group_name}" in
    portainer)
      VALIDATE_PORTAINER=1
      append_unique DIAGNOSTIC_SERVICES portainer
      ;;
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
    chatgpt)
      VALIDATE_CHATGPT=1
      append_unique DIAGNOSTIC_SERVICES chatgpt_broker
      append_unique DIAGNOSTIC_SERVICES chatgpt_bot
      ;;
    ticket_remote)
      VALIDATE_TICKET_REMOTE=1
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
      append_unique DIAGNOSTIC_SERVICES ticket_remote_spacetime_sidecar
      append_unique DIAGNOSTIC_SERVICES ticket_remote
      append_unique DIAGNOSTIC_SERVICES ticket_remote_tunnel
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
      portainer)
        append_unique COMPOSE_TARGET_SERVICES portainer
        mark_validation_group portainer
        ;;
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
      chatgpt_broker)
        append_unique COMPOSE_TARGET_SERVICES chatgpt_broker
        append_unique COMPOSE_TARGET_SERVICES chatgpt_bot
        mark_validation_group chatgpt
        ;;
      chatgpt_bot)
        append_unique COMPOSE_TARGET_SERVICES chatgpt_broker
        append_unique COMPOSE_TARGET_SERVICES chatgpt_bot
        mark_validation_group chatgpt
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
      *)
        echo "Unknown service: ${service_name}" >&2
        exit 2
        ;;
    esac
  done
}

populate_current_diagnostic_services() {
  local array_name="$1"
  if (( TARGETED_MODE == 0 )); then
    eval "${array_name}=(\"\${ALL_SERVICES[@]}\")"
  else
    eval "${array_name}=(\"\${DIAGNOSTIC_SERVICES[@]}\")"
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
    portainer
    train_bot
    satiksme_bot
    subscription_bot
    ticket_phone_bridge
    chatgpt_broker
    chatgpt_bot
    ticket_remote_spacetime_sidecar
    ticket_remote
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

  for service_name in "${services[@]}"; do
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
    collect_remote_validation_diagnostics "${probe_release_dir}" "${services[@]}"
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
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" "${services[@]}"
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


resolve_remote_tailscale_hostname() {
  local hostname=""

  hostname="$(
    remote_inline_shell "
      python3 - <<'PY'
import json
import subprocess

payload = json.loads(subprocess.check_output(['tailscale', 'status', '--json'], text=True))
hostname = payload.get('Self', {}).get('HostName', '').strip()
if not hostname:
    raise SystemExit('missing Arbuzas Tailscale hostname')
print(hostname)
PY
    " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
  )" || return 1

  [[ -n "${hostname}" ]] || return 1
  printf '%s\n' "${hostname}"
}

validate_remote_netdata() {
  local tailnet_ipv4=""

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

  log "Validate: Netdata stays unclaimed on the live host"
  remote_root_command "
    [[ ! -f /var/lib/netdata/cloud.d/claim.conf ]]
  "

  log "Validate: Netdata keeps Docker polling disabled on the live host"
  remote_root_command "
    [[ -f '${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' ]] || {
      echo 'missing Netdata Docker override: ${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' >&2
      exit 1
    }
    [[ -f '${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' ]] || {
      echo 'missing Netdata Docker service-discovery override: ${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' >&2
      exit 1
    }
    grep -F 'disabled: yes' '${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' >/dev/null
    grep -F 'disabled: yes' '${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' >/dev/null
  "

  log "Validate: netdata binds only to loopback"
  remote_root_command "
    listeners=\$(ss -ltn sport = :${ARBUZAS_NETDATA_PORT} | tail -n +2 || true)
    [[ -n \"\${listeners}\" ]] || {
      echo 'Netdata is not listening on port ${ARBUZAS_NETDATA_PORT}' >&2
      exit 1
    }
    if printf '%s\n' \"\${listeners}\" | grep -E '(^|[[:space:]])0\\.0\\.0\\.0:${ARBUZAS_NETDATA_PORT}([[:space:]]|$)|\\[::\\]:${ARBUZAS_NETDATA_PORT}([[:space:]]|$)' >/dev/null; then
      echo 'Netdata is listening publicly on port ${ARBUZAS_NETDATA_PORT}' >&2
      exit 1
    fi
  "

  log "Validate: Netdata charts cover host, disk, container, and ThinkPad hardware metrics"
  remote_root_command "
    NETDATA_CHARTS_URL='http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/api/v1/charts' \
    python3 - <<'PY'
import json
import os
import sys
import urllib.request

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

checks = {
    'cpu': has(lambda descriptor: 'system.cpu' in descriptor or 'cpu utilization' in descriptor),
    'memory': has(lambda descriptor: 'system.ram' in descriptor or 'ram utilization' in descriptor),
    'filesystem': has(lambda descriptor: 'disk_space' in descriptor or 'disk space' in descriptor),
    'disk_io': has(lambda descriptor: descriptor.startswith('disk.') or 'disk i/o' in descriptor or 'disk throughput' in descriptor),
    'containers': has(
        lambda descriptor: 'cgroup' in descriptor
        or 'app.arbuzas-' in descriptor
        or 'app.cloudflared' in descriptor
    ),
    'temperature': has(lambda descriptor: 'temperature' in descriptor and ('thinkpad' in descriptor or 'coretemp' in descriptor or 'cpu' in descriptor)),
    'fan': has(lambda descriptor: 'fan' in descriptor and 'thinkpad' in descriptor),
}

missing = [name for name, present in checks.items() if not present]
if missing:
    print('missing expected Netdata charts: ' + ', '.join(missing), file=sys.stderr)
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

  log "Validate: Tailscale serve publishes the Netdata TCP forwarder"
  remote_root_command "
    serve_status=\$(tailscale serve status 2>&1)
    printf '%s\n' \"\${serve_status}\" >&2
    printf '%s' \"\${serve_status}\" | grep -F '${ARBUZAS_NETDATA_PORT}' >/dev/null
  "

  tailnet_ipv4="$(resolve_remote_tailscale_ipv4)" || {
    echo "failed to resolve the Arbuzas Tailscale IPv4 address" >&2
    exit 1
  }

  log "Validate: netdata is reachable from this operator machine at http://${tailnet_ipv4}:${ARBUZAS_NETDATA_PORT}"
  if ! wait_until_local_ok curl -fsS "http://${tailnet_ipv4}:${ARBUZAS_NETDATA_PORT}/api/v1/info" >/dev/null 2>&1; then
    echo "Netdata did not answer over Tailscale at http://${tailnet_ipv4}:${ARBUZAS_NETDATA_PORT}/api/v1/info" >&2
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
  if [[ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=all -- infra/arbuzas/docker tools/arbuzas test_arbuzas_deploy_contract.sh test_ticket_phone_bridge_hardening.sh workloads/shared-go workloads/train-bot workloads/satiksme-bot workloads/subscription-bot workloads/chatgpt-broker workloads/ticket-remote)" ]]; then
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
    pathlib.Path("tools/arbuzas"),
    pathlib.Path("workloads/shared-go"),
    pathlib.Path("workloads/train-bot"),
    pathlib.Path("workloads/satiksme-bot"),
    pathlib.Path("workloads/subscription-bot"),
    pathlib.Path("workloads/chatgpt-broker"),
    pathlib.Path("workloads/ticket-remote"),
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
ARBUZAS_CHATGPT_BROKER_PORT=${ARBUZAS_CHATGPT_BROKER_PORT}
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
ARBUZAS_PORTAINER_IMAGE=${ARBUZAS_PORTAINER_IMAGE}
ARBUZAS_CLOUDFLARED_IMAGE=${ARBUZAS_CLOUDFLARED_IMAGE}
ARBUZAS_TICKET_CLOUDFLARED_IMAGE=${ARBUZAS_TICKET_CLOUDFLARED_IMAGE}
EOF
}

prepare_local_release_bundle() {
  log "Preparing local release bundle ${ARBUZAS_RELEASE_ID}"
  rm -rf "${ARBUZAS_RELEASE_DIR}"
  mkdir -p "${ARBUZAS_RELEASE_DIR}/generated/cloudflared"

  copy_tree_into_release "infra/arbuzas/docker"
  copy_tree_into_release "workloads/shared-go"
  copy_tree_into_release "workloads/train-bot"
  copy_tree_into_release "workloads/satiksme-bot"
  copy_tree_into_release "workloads/subscription-bot"
  copy_tree_into_release "workloads/chatgpt-broker"
  copy_tree_into_release "workloads/ticket-remote"

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

  log "Preparing selected-service release overlay ${ARBUZAS_RELEASE_ID}"
  rm -rf "${ARBUZAS_RELEASE_DIR}"
  mkdir -p "${ARBUZAS_RELEASE_DIR}"
  FAST_RELEASE_OVERLAY_PATHS=()

  copy_tree_into_fast_release_overlay "infra/arbuzas/docker"
  for service_name in "${COMPOSE_TARGET_SERVICES[@]}"; do
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
      chatgpt_broker|chatgpt_bot)
        copy_tree_into_fast_release_overlay "workloads/chatgpt-broker"
        ;;
      ticket_remote_spacetime_sidecar|ticket_remote)
        copy_tree_into_fast_release_overlay "workloads/ticket-remote"
        ;;
      portainer|train_tunnel|satiksme_tunnel|subscription_tunnel|ticket_phone_bridge|ticket_remote_tunnel)
        ;;
      *)
        echo "No fast release overlay mapping for service: ${service_name}" >&2
        return 2
        ;;
    esac
  done

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
    install -d -o 1001 -g 1001 -m 0750 '/srv/arbuzas/ticket-remote/state'
    for path in \
      '/etc/arbuzas/env/ticket-remote.env' \
      '/etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem' \
      '/etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret'; do
      if [[ -f \"\${path}\" ]]; then
        chown 1001:1001 \"\${path}\"
        chmod 0600 \"\${path}\"
      fi
    done
    for path in '/etc/arbuzas/secrets/android-adb/adbkey' '/etc/arbuzas/secrets/android-adb/adbkey.pub' '/etc/arbuzas/secrets/android-adb/adb_known_hosts.pb'; do
      if [[ -f \"\${path}\" ]]; then
        chown 1002:1002 \"\${path}\"
        chmod 0600 \"\${path}\"
      fi
    done
  "
}

prepare_remote_host_layout() {
  remote_shell "
    command -v docker >/dev/null 2>&1 || { echo 'docker is required on ${ARBUZAS_HOST}' >&2; exit 1; }
    docker compose version >/dev/null 2>&1 || { echo 'docker compose is required on ${ARBUZAS_HOST}' >&2; exit 1; }
    command -v python3 >/dev/null 2>&1 || { echo 'python3 is required on ${ARBUZAS_HOST}' >&2; exit 1; }
    mkdir -p \
      '/srv/arbuzas/portainer' \
      '/srv/arbuzas/portainer-backups' \
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
    sudo -n touch \
      '/etc/arbuzas/env/train-bot.env' \
      '/etc/arbuzas/env/satiksme-bot.env' \
      '/etc/arbuzas/env/subscription-bot.env' \
      '/etc/arbuzas/env/ticket-remote.env' 2>/dev/null || true
  "
  prepare_remote_ticket_runtime_permissions
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
    sudo -n tar -C '${remote_tmp_dir}' -xf '${remote_tarball}'
    sudo -n rm -f '${remote_tarball}'
    [[ -f '${remote_tmp_dir}/release.env' ]] || { echo 'incomplete fast overlay: missing release.env' >&2; exit 1; }
    [[ -f '${remote_tmp_dir}/infra/arbuzas/docker/compose.yml' ]] || { echo 'incomplete fast overlay: missing compose.yml' >&2; exit 1; }
    [[ -f '${remote_tmp_dir}/tools/arbuzas/render_cloudflared_config.py' ]] || { echo 'incomplete fast overlay: missing tunnel renderer' >&2; exit 1; }
    for required_root in workloads/shared-go workloads/train-bot workloads/satiksme-bot workloads/subscription-bot workloads/chatgpt-broker workloads/ticket-remote; do
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

  for service_name in "${COMPOSE_TARGET_SERVICES[@]}"; do
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
  remote_inline_shell "
    current_target=\$(readlink '${REMOTE_CURRENT_LINK}' 2>/dev/null || true)
    if [[ -n \"\${current_target}\" ]]; then
      basename \"\${current_target}\"
    fi
  " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
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
          chatgpt_broker=arbuzas/chatgpt-broker \
          chatgpt_bot=arbuzas/chatgpt-bot \
          ticket_remote_spacetime_sidecar=arbuzas/ticket-remote-spacetime-sidecar \
          ticket_remote=arbuzas/ticket-remote; do
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
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --no-deps --remove-orphans${non_tunnel_service_args}
      fi
      if [[ -n '${tunnel_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps --remove-orphans${tunnel_service_args}
      fi
    "
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
  "
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

  for service_name in "${services[@]}"; do
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

validate_remote_portainer_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" portainer
  validate_remote_probe "${remote_release_dir}" "portainer responds" \
    "wait_until_ok sh -lc 'curl -skf https://127.0.0.1:9443 >/dev/null 2>/dev/null'" \
    portainer
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
            return response.status, {k.lower(): v for k, v in response.headers.items()}, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        return error.code, {k.lower(): v for k, v in error.headers.items()}, error.read().decode('utf-8', 'replace')

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
wait_until_ok env TRAIN_PAGE_HTML_FILE=\"\${html_tmp}\" TRAIN_SPACETIME_CONFIG_FILE=\"\${config_tmp}\" python3 \"\${tmp}\"" \
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
            set_cookies = response.headers.get_all('Set-Cookie') or []
            if set_cookies:
                response_headers['set-cookie'] = '\n'.join(set_cookies)
            return response.status, response_headers, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        error_headers = {k.lower(): v for k, v in error.headers.items()}
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

for path in ['/api/v1/public/live-vehicles?limit=1', '/api/v1/public/map-live?limit=1', '/api/v1/public/map?limit=1']:
    status, public_headers, public_body = request(path)
    if status != 200:
        raise SystemExit(f'{path} status {status}: {public_body[:200]}')
    assert_no_satiksme_headers(path, public_headers)
    assert_noindex(path, public_headers)
    assert_public_json_cache_not_long_immutable(path, public_headers)
    assert_satiksme_public_json(path, json.loads(public_body))

status, incidents_headers, incidents_body = request('/api/v1/public/incidents?limit=1')
if status != 200:
    raise SystemExit(f'public incidents status {status}: {incidents_body[:200]}')
assert_no_satiksme_headers('/api/v1/public/incidents?limit=1', incidents_headers)
assert_noindex('/api/v1/public/incidents?limit=1', incidents_headers)
assert_public_json_cache_not_long_immutable('/api/v1/public/incidents?limit=1', incidents_headers)
incidents_payload = json.loads(incidents_body)
assert_satiksme_public_json('/api/v1/public/incidents?limit=1', incidents_payload)
incident_id = ''
for incident in incidents_payload.get('incidents', []) if isinstance(incidents_payload, dict) else []:
    if isinstance(incident, dict) and str(incident.get('id', '')).strip():
        current_id = str(incident.get('id')).strip()
        if current_id.startswith('area:') and not re.fullmatch(r'area:pub-[0-9a-f]{8}', current_id):
            raise SystemExit(f'public area incident id is not opaque: {current_id!r}')
        incident_id = current_id
        break
if incident_id:
    detail_path = '/api/v1/public/incidents/' + urllib.parse.quote(incident_id, safe='')
    status, detail_headers, detail_body = request(detail_path)
    if status != 200:
        raise SystemExit(f'public incident detail {detail_path} status {status}: {detail_body[:200]}')
    assert_no_satiksme_headers(detail_path, detail_headers)
    assert_noindex(detail_path, detail_headers)
    assert_public_json_cache_not_long_immutable(detail_path, detail_headers)
    detail_payload = json.loads(detail_body)
    assert_satiksme_public_json(detail_path, detail_payload)
    for event in detail_payload.get('events', []) if isinstance(detail_payload, dict) else []:
        if not isinstance(event, dict):
            continue
        event_id = str(event.get('id', '')).strip()
        if event_id and not re.fullmatch(r'incident-event:pub-[0-9a-f]{8}', event_id):
            raise SystemExit(f'public incident detail event id is not opaque: {event_id!r}')
        for raw_marker in ['channel:', 'stop:', 'vehicle:', 'area:', 'liveRowId', 'scopeKey']:
            if raw_marker in event_id:
                raise SystemExit(f'public incident detail event id exposes raw marker {raw_marker}: {event_id!r}')
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
import os
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
    'satiksmebot_live_viewer_heartbeat',
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

status, body = anonymous_sql('SELECT * FROM satiksmebot_public_live_snapshot_state WHERE 1 = 0')
if not (200 <= status < 300):
    raise SystemExit(f'anonymous SQL could not inspect public live snapshot table layout: {status} {body[:200]}')
for forbidden in ['hash', 'publishedAt', 'lastSuccessAt', 'lastAttemptAt', 'status', 'consecutiveFailures', 'vehicleCount']:
    if forbidden in body:
        raise SystemExit(f'public live snapshot table exposes {forbidden}: {body[:300]}')

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
wait_until_ok env SATIKSME_SPACETIME_CONFIG_FILE=\"\${config_tmp}\" python3 \"\${tmp}\"" \
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

validate_remote_chatgpt_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" chatgpt_broker chatgpt_bot
  validate_remote_probe "${remote_release_dir}" "chatgpt broker local health" \
    "wait_until_ok compose exec -T chatgpt_broker sh -lc 'curl -fsS \"http://127.0.0.1:${ARBUZAS_CHATGPT_BROKER_PORT}/healthz\" >/dev/null 2>/dev/null'" \
    chatgpt_broker chatgpt_bot
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
      grep -aF \"generate_control_code\" \"\${binary}\" >/dev/null
      grep -aF \"requestControlCode\" \"\${binary}\" >/dev/null
      grep -aF \"sanitizeControlDigits\" \"\${binary}\" >/dev/null
      grep -aF \"navigator.wakeLock.request\" \"\${binary}\" >/dev/null
      grep -aF \"requestFullscreen\" \"\${binary}\" >/dev/null
      grep -aF \"toolbarCollapseAnchorPx\" \"\${binary}\" >/dev/null
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

      if [[ '${VALIDATE_PORTAINER}' == '1' ]]; then
        case \" \${running} \" in *' portainer '*) ;; *) return 1 ;; esac
        curl -skf --connect-timeout 2 --max-time 4 https://127.0.0.1:9443/ >/dev/null 2>&1 || return 1
      fi
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
      if [[ '${VALIDATE_CHATGPT}' == '1' ]]; then
        case \" \${running} \" in *' chatgpt_broker '*) ;; *) return 1 ;; esac
        case \" \${running} \" in *' chatgpt_bot '*) ;; *) return 1 ;; esac
        compose exec -T chatgpt_broker sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_CHATGPT_BROKER_PORT}/healthz >/dev/null' >/dev/null 2>&1 || return 1
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
  " "${diagnostics_services[@]}"
}

validate_remote_workload_health() {
  local remote_release_dir="$1"

  validate_remote_portainer_health "${remote_release_dir}"
  validate_remote_train_workload_health "${remote_release_dir}"
  validate_remote_satiksme_workload_health "${remote_release_dir}"
  validate_remote_subscription_workload_health "${remote_release_dir}"
  validate_remote_ticket_phone_bridge_workload_health "${remote_release_dir}"
  validate_remote_chatgpt_workload_health "${remote_release_dir}"
  validate_remote_ticket_remote_workload_health "${remote_release_dir}"
}

validate_remote_selected_workload_health() {
  local remote_release_dir="$1"

  if (( VALIDATE_PORTAINER == 1 )); then
    validate_remote_portainer_health "${remote_release_dir}"
  fi
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
  if (( VALIDATE_CHATGPT == 1 )); then
    validate_remote_chatgpt_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_TICKET_REMOTE == 1 )); then
    validate_remote_ticket_remote_workload_health "${remote_release_dir}"
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
    "${diagnostics_services[@]}"
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
    "${diagnostics_services[@]}"

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
    "${diagnostics_services[@]}"
}

validate_remote_host_baseline() {
  local remote_release_dir="$1"

  validate_remote_swarm_baseline "${remote_release_dir}"
  validate_remote_portainer_state "${remote_release_dir}"
}

validate_remote_portainer_state() {
  local remote_release_dir="$1"
  local tmpdir
  local local_db_path
  local diagnostics_services=()

  populate_current_diagnostic_services diagnostics_services

  log "Validate: Portainer state uses ${PORTAINER_LOCAL_ENDPOINT} and no longer stores ${PORTAINER_AGENT_ENDPOINT}"
  tmpdir="$(mktemp -d)"
  local_db_path="${tmpdir}/portainer.db"

  if ! download_remote_portainer_db "${local_db_path}"; then
    rm -rf "${tmpdir}"
    log "Validation failed: unable to download Portainer state from ${ARBUZAS_HOST}"
    collect_remote_validation_diagnostics "${remote_release_dir}" "${diagnostics_services[@]}"
    exit 1
  fi

  if ! run_portainer_db_tool check "${local_db_path}" >&2; then
    rm -rf "${tmpdir}"
    log "Validation failed: Portainer still carries stale endpoint state"
    collect_remote_validation_diagnostics "${remote_release_dir}" "${diagnostics_services[@]}"
    exit 1
  fi

  rm -rf "${tmpdir}"
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
    return_remote_validation_status
    return
  fi

  validate_remote_probe "${remote_release_dir}" \
    "release bundle exists" \
    "[[ -f '${remote_release_dir}/release.env' ]]" \
    "${diagnostics_services[@]}"

  if (( TARGETED_MODE == 1 )); then
    validate_remote_selected_workload_health "${remote_release_dir}"
    if [[ "${VALIDATION_PROFILE}" == "full" ]]; then
      validate_remote_swarm_baseline "${remote_release_dir}"
      if (( VALIDATE_PORTAINER == 1 )); then
        validate_remote_portainer_state "${remote_release_dir}"
      fi
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
    validate_remote_selected_smoke_health "${remote_release_dir}" 1
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

repair_remote_portainer() {
  local remote_release_dir="${REMOTE_CURRENT_LINK}"
  local backup_timestamp
  local backup_path
  local tmpdir
  local local_db_path
  local repaired_db_path
  local remote_upload_path
  local has_portainer_db=0

  validate_remote_probe "${remote_release_dir}" \
    "release bundle exists" \
    "[[ -f '${remote_release_dir}/release.env' ]]" \
    portainer train_bot satiksme_bot subscription_bot ticket_phone_bridge chatgpt_broker chatgpt_bot ticket_remote_spacetime_sidecar ticket_remote train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel
  validate_remote_workload_health "${remote_release_dir}"

  validate_remote_host_probe "${remote_release_dir}" \
    "no active Swarm workloads remain" \
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
    portainer train_bot satiksme_bot subscription_bot ticket_phone_bridge chatgpt_broker chatgpt_bot ticket_remote_spacetime_sidecar ticket_remote train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel

  backup_timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
  backup_path="${REMOTE_PORTAINER_BACKUPS_DIR}/portainer-${backup_timestamp}.tar.gz"
  remote_upload_path="/tmp/portainer.db.${backup_timestamp}"
  tmpdir="$(mktemp -d)"
  local_db_path="${tmpdir}/portainer.db"
  repaired_db_path="${tmpdir}/portainer.repaired.db"

  log "Repair: stopping Portainer and backing up ${REMOTE_PORTAINER_DATA_DIR} to ${backup_path}"
  remote_compose_shell "${remote_release_dir}" "
    compose stop portainer
  "
  if ! backup_remote_portainer_data "${backup_path}"; then
    rm -rf "${tmpdir}"
    remote_compose_shell "${remote_release_dir}" "compose up -d portainer"
    echo "failed to back up Portainer data to ${backup_path}" >&2
    exit 1
  fi

  if download_remote_portainer_db "${local_db_path}"; then
    has_portainer_db=1
    log "Repair: downloaded Portainer state for in-place repair"

    log "Repair: rewriting stale Portainer endpoint state while preserving existing users and settings"
    if ! run_portainer_db_tool repair "${local_db_path}" "${repaired_db_path}" >&2; then
      rm -rf "${tmpdir}"
      remote_compose_shell "${remote_release_dir}" "compose up -d portainer"
      echo "failed to repair Portainer state in place" >&2
      exit 1
    fi

    log "Repair: uploading repaired Portainer database"
    if ! install_remote_portainer_db "${repaired_db_path}" "${remote_upload_path}"; then
      rm -rf "${tmpdir}"
      remote_compose_shell "${remote_release_dir}" "compose up -d portainer"
      echo "failed to install repaired Portainer database" >&2
      exit 1
    fi
  else
    log "Repair: no existing Portainer database found, keeping the backup and continuing with a clean standalone restart"
  fi

  log "Repair: disabling Docker Swarm on ${ARBUZAS_HOST}"
  if ! remote_shell "
    swarm_state=\$(docker info --format '{{.Swarm.LocalNodeState}}')
    case \"\${swarm_state}\" in
      active)
        docker swarm leave --force
        ;;
      inactive)
        ;;
      *)
        echo \"unexpected Docker Swarm state: \${swarm_state}\" >&2
        exit 1
        ;;
    esac
  "; then
    rm -rf "${tmpdir}"
    remote_compose_shell "${remote_release_dir}" "compose up -d portainer"
    echo "failed to disable Docker Swarm during Portainer repair" >&2
    exit 1
  fi

  log "Repair: restarting Portainer on the standalone Docker socket"
  remote_compose_shell "${remote_release_dir}" "
    mkdir -p '${REMOTE_PORTAINER_DATA_DIR}'
    compose up -d portainer
  "

  rm -rf "${tmpdir}"
  validate_remote_release
  log "Repair complete. Portainer backup saved at ${backup_path}"
  if (( has_portainer_db == 1 )); then
    log "Existing Portainer users and settings were preserved in place."
  else
    log "Manual action required: open https://${ARBUZAS_HOST}:9443 and complete the first-run Portainer setup to recreate the admin user."
  fi
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
    ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    if [[ '${TARGETED_MODE}' == '1' ]]; then
      if [[ -n '${rollback_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --no-deps${rollback_service_args}
      fi
      if [[ -n '${rollback_tunnel_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${rollback_tunnel_service_args}
      fi
    else
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --remove-orphans${rollback_service_args}
    fi
  "
}

run_host_mirror() {
  local mirror_action="$1"
  shift || true
  local args=()
  args=("${HOST_MIRROR_SCRIPT}" "${mirror_action}" --profile arbuzas --mirror-root "${HOST_MIRROR_ROOT}" --ssh-target "$(remote_target)")
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(--ssh-port "${ARBUZAS_SSH_PORT}")
  fi
  ARBUZAS_HOST_MIRROR_PRIVILEGED="${ARBUZAS_HOST_MIRROR_PRIVILEGED:-1}" \
    python3 "${args[@]}" "$@"
}

run_host_mirror_push() {
  local changed_paths_file="$1"
  local args=()
  args=("${HOST_MIRROR_SCRIPT}" push --profile arbuzas --mirror-root "${HOST_MIRROR_ROOT}" --ssh-target "$(remote_target)" --changed-paths-file "${changed_paths_file}")
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(--ssh-port "${ARBUZAS_SSH_PORT}")
  fi
  ARBUZAS_HOST_MIRROR_PRIVILEGED="${ARBUZAS_HOST_MIRROR_PRIVILEGED:-1}" \
    python3 "${args[@]}"
}

host_mirror_affected_services() {
  local changed_paths_file="$1"
  ARBUZAS_HOST_MIRROR_PRIVILEGED="${ARBUZAS_HOST_MIRROR_PRIVILEGED:-1}" \
    python3 "${HOST_MIRROR_SCRIPT}" affected --profile arbuzas --changed-paths-file "${changed_paths_file}"
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

deploy_config_from_mirror() {
  local changed_paths_file
  local affected_output=""
  local -a affected_services=()
  local service_name=""
  local service_args=""
  changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
  trap "rm -f '${changed_paths_file}'; trap - RETURN" RETURN

  run_host_mirror_push "${changed_paths_file}"
  prepare_remote_ticket_runtime_permissions
  affected_output="$(host_mirror_affected_services "${changed_paths_file}")"
  if [[ -z "${affected_output}" ]]; then
    log "Deploy config: mirror is already in sync; no services need restart"
    return 0
  fi

  while IFS= read -r service_name; do
    [[ -n "${service_name}" ]] || continue
    affected_services+=("${service_name}")
  done <<< "${affected_output}"

  log "Deploy config: affected services $(csv_join_services "${affected_services[@]}")"
  remote_shell "
    [[ -f '${REMOTE_CURRENT_LINK}/release.env' ]] || { echo 'missing active release: ${REMOTE_CURRENT_LINK}/release.env' >&2; exit 1; }
    [[ -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' ]] || { echo 'missing active compose file under ${REMOTE_CURRENT_LINK}' >&2; exit 1; }
  "

  for service_name in "${affected_services[@]}"; do
    service_args+=" ${service_name}"
  done
  remote_shell "
    cd '${REMOTE_CURRENT_LINK}'
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps --remove-orphans${service_args}
  "
}

while (( $# > 0 )); do
  case "$1" in
    deploy|validate|rollback|cleanup-docker|memory-report|install-memory-report|validate-memory-report|install-netdata|validate-netdata|install-thinkpad-fan|validate-thinkpad-fan|repair-portainer|mirror-pull|mirror-audit|mirror-push|deploy-config)
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

validate_validation_profile

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
    deploy_config_from_mirror
    ;;
  deploy)
    require_cmd tar
    ARBUZAS_RELEASE_ID="${requested_release_id:-${ARBUZAS_RELEASE_ID}}"
    ARBUZAS_RELEASE_DIR="${LOCAL_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
    enforce_release_source_policy
    previous_release_id="$(run_timed_phase resolve_current_release resolve_remote_current_release_id || true)"
    if (( TARGETED_MODE == 1 )); then
      log "Deploy: targeted services ${COMPOSE_TARGET_SERVICES[*]} profile=${VALIDATION_PROFILE}"
    fi
    mirror_changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
    trap 'rm -f "${mirror_changed_paths_file}"' EXIT
    run_timed_phase mirror_push run_host_mirror_push "${mirror_changed_paths_file}"
    run_timed_phase prepare_ticket_permissions prepare_remote_ticket_runtime_permissions
    run_timed_phase package_release prepare_deploy_release_payload
    if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
      log "Host layout preparation skipped: fast profile reuses the active complete release"
    else
      run_timed_phase prepare_host prepare_remote_host_layout
    fi
    run_timed_phase upload_release copy_deploy_release_payload
    run_timed_phase render_tunnels render_deploy_cloudflared_configs
    deploy_ready_for_validation=1
    if ! run_timed_phase restart_services remote_compose_up; then
      deploy_ready_for_validation=0
    fi
    if (( deploy_ready_for_validation == 1 )) && run_timed_phase validate_release validate_deployed_release; then
      run_timed_phase post_deploy_maintenance run_post_deploy_maintenance
      exit 0
    fi
    if [[ -n "${previous_release_id}" && "${previous_release_id}" != "${ARBUZAS_RELEASE_ID}" ]]; then
      log "Deploy validation failed; rolling back to ${previous_release_id}"
      requested_release_id="${previous_release_id}"
      run_timed_phase rollback_release rollback_remote_release
      if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
        run_timed_phase validate_rollback validate_remote_selected_smoke_health "${REMOTE_RELEASES_ROOT}/${previous_release_id}" 1
      else
        run_timed_phase validate_rollback validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${previous_release_id}"
        run_timed_phase validate_rollback_services validate_remote_release "${previous_release_id}"
      fi
    fi
    exit 1
    ;;
  validate)
    if (( TARGETED_MODE == 1 )); then
      log "Validate: targeted services ${COMPOSE_TARGET_SERVICES[*]} profile=${VALIDATION_PROFILE}"
    fi
    run_timed_phase validate_release validate_remote_release "${requested_release_id}"
    ;;
  rollback)
    run_timed_phase rollback_release rollback_remote_release
    if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then
      run_timed_phase validate_rollback validate_remote_selected_smoke_health "${REMOTE_RELEASES_ROOT}/${requested_release_id}" 1
    else
      run_timed_phase validate_rollback_link validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${requested_release_id}"
      run_timed_phase validate_rollback_services validate_remote_release "${requested_release_id}"
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
    install_remote_netdata "${remote_stage_root}"
    validate_remote_netdata
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
  repair-portainer)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for repair-portainer" >&2
      exit 2
    fi
    repair_remote_portainer
    ;;
esac
