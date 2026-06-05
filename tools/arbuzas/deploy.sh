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
MEMORY_REPORT_SCRIPT="${SCRIPT_DIR}/memory_report.py"
DOCKER_GC_REMOTE_STATE_DIR="/etc/arbuzas/docker-gc"
DOCKER_GC_REMOTE_STATE_FILE="${DOCKER_GC_REMOTE_STATE_DIR}/state.json"
DOCKER_GC_BUILD_CACHE_UNTIL="${DOCKER_GC_BUILD_CACHE_UNTIL:-168h}"
DOCKER_GC_RELEASE_KEEP_PER_FAMILY="${DOCKER_GC_RELEASE_KEEP_PER_FAMILY:-10}"
ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS="${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS:-7}"
ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE="${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE:-100M}"
ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE="${ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE:-true}"
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
DNS_ADMIN_NGINX_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/nginx"
DNS_ADMIN_NGINX_TEMPLATE_FILE="${DNS_ADMIN_NGINX_TEMPLATE_FILE:-${DNS_ADMIN_NGINX_CONFIG_ROOT}/arbuzas-dns-admin.conf.template}"
DNS_ADMIN_NGINX_REMOTE_SITE_FILE="/etc/nginx/sites-available/arbuzas-dns-admin"
DNS_ADMIN_NGINX_REMOTE_SITE_LINK="/etc/nginx/sites-enabled/arbuzas-dns-admin"
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
ARBUZAS_PHONE_BROKER_PORT="${ARBUZAS_PHONE_BROKER_PORT:-9398}"
ARBUZAS_TICKET_PHONE_ADB_TARGET="${ARBUZAS_TICKET_PHONE_ADB_TARGET:-100.76.50.43:5555}"
ARBUZAS_DNS_HTTPS_PORT="${ARBUZAS_DNS_HTTPS_PORT:-443}"
ARBUZAS_DNS_DOT_PORT="${ARBUZAS_DNS_DOT_PORT:-853}"
ARBUZAS_DNS_CONTROLPLANE_PORT="${ARBUZAS_DNS_CONTROLPLANE_PORT:-8097}"
ARBUZAS_DNS_ADMIN_LAN_IP="${ARBUZAS_DNS_ADMIN_LAN_IP:-}"
ARBUZAS_NETDATA_PORT="${ARBUZAS_NETDATA_PORT:-19999}"
ARBUZAS_TAILSCALE_IPV4="${ARBUZAS_TAILSCALE_IPV4:-}"
ARBUZAS_FAN_ENTER_AUTO_C="${ARBUZAS_FAN_ENTER_AUTO_C:-89}"
ARBUZAS_FAN_EXIT_AUTO_C="${ARBUZAS_FAN_EXIT_AUTO_C:-89}"

ARBUZAS_TRAIN_BOT_HOSTNAME="${ARBUZAS_TRAIN_BOT_HOSTNAME:-vilciens.kontrole.info}"
ARBUZAS_SATIKSME_BOT_HOSTNAME="${ARBUZAS_SATIKSME_BOT_HOSTNAME:-kontrole.info}"
ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME="${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME:-farel-subscription-bot.jolkins.id.lv}"
ARBUZAS_TICKET_REMOTE_HOSTNAME="${ARBUZAS_TICKET_REMOTE_HOSTNAME:-ticket.jolkins.id.lv}"
ARBUZAS_DNS_HOSTNAME="${ARBUZAS_DNS_HOSTNAME:-dns.jolkins.id.lv}"
ARBUZAS_PORTAINER_IMAGE="${ARBUZAS_PORTAINER_IMAGE:-portainer/portainer-ce:lts}"
ARBUZAS_CLOUDFLARED_IMAGE="${ARBUZAS_CLOUDFLARED_IMAGE:-cloudflare/cloudflared:latest}"

action=""
requested_release_id=""
TARGETED_MODE=0
VALIDATE_PORTAINER=0
VALIDATE_TRAIN=0
VALIDATE_SATIKSME=0
VALIDATE_SUBSCRIPTION=0
VALIDATE_PHONE_BROKER=0
VALIDATE_RIGASATIKSME_QR=0
VALIDATE_TICKET_REMOTE=0
VALIDATE_DNS=0
REQUESTED_SERVICES=()
COMPOSE_TARGET_SERVICES=()
DIAGNOSTIC_SERVICES=()

ALL_SERVICES=(
  portainer
  train_bot
  satiksme_bot
  subscription_bot
  ticket_phone_bridge
  phone_broker
  rigassatiksme_qr_bot
  ticket_remote
  train_tunnel
  satiksme_tunnel
  subscription_tunnel
  ticket_remote_tunnel
  dns_controlplane
)

DNS_SERVICES=(
  dns_controlplane
)

log() {
  printf '[%s] %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*" >&2
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

remote_run_docker_gc() {
  local gc_script=""

  if [[ ! "${DOCKER_GC_RELEASE_KEEP_PER_FAMILY}" =~ ^[0-9]+$ ]]; then
    echo "DOCKER_GC_RELEASE_KEEP_PER_FAMILY must be a non-negative integer" >&2
    return 2
  fi

  if gc_script="$(resolve_local_docker_gc_script)"; then
    run_ssh "$(remote_target)" \
      "python3 - --current-link '${REMOTE_CURRENT_LINK}' --releases-root '${REMOTE_RELEASES_ROOT}' --state-file '${DOCKER_GC_REMOTE_STATE_FILE}' --build-cache-until '${DOCKER_GC_BUILD_CACHE_UNTIL}' --release-keep-per-family '${DOCKER_GC_RELEASE_KEEP_PER_FAMILY}'" \
      < "${gc_script}"
    return 0
  fi

  remote_shell "
    gc_script='${REMOTE_CURRENT_LINK}/tools/arbuzas/docker_gc.py'
    [[ -f \"\${gc_script}\" ]] || {
      echo 'missing Docker GC helper locally and on the current Arbuzas release bundle' >&2
      exit 1
    }
    python3 \"\${gc_script}\" \
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

compact_remote_dns_db() {
  local remote_release_dir="${REMOTE_CURRENT_LINK}"
  ensure_remote_dns_host_preflight
  remote_compose_shell "${remote_release_dir}" "
    restart_dns_controlplane() {
      compose up -d --build --force-recreate --no-deps dns_controlplane >/dev/null
    }

    trap 'restart_dns_controlplane || true' EXIT
    compose stop dns_controlplane
    compose run -T --rm --no-deps --build dns_controlplane /usr/local/bin/arbuzas-dns compact --json --include-legacy-observability </dev/null
    restart_dns_controlplane
    trap - EXIT
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
  compact-dns-db    Run the Arbuzas DNS cleanup activation and compact maintenance flow on the live host
  repair-dns-admin  Clear stale private DNS admin forwards, re-assert the Tailscale TCP forward, refresh the bare private web URL, and print host listener diagnostics
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
  --ssh-host HOST
  --ssh-user USER
  --ssh-port PORT
  --env-file PATH

Services:
  portainer, train_bot, train_tunnel, satiksme_bot, satiksme_tunnel,
  subscription_bot, subscription_tunnel, ticket_phone_bridge, phone_broker, rigassatiksme_qr_bot,
  ticket_remote, ticket_remote_tunnel,
  dns_controlplane
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
    phone_broker)
      VALIDATE_PHONE_BROKER=1
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
      append_unique DIAGNOSTIC_SERVICES phone_broker
      ;;
    rigassatiksme_qr)
      VALIDATE_RIGASATIKSME_QR=1
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
      append_unique DIAGNOSTIC_SERVICES phone_broker
      append_unique DIAGNOSTIC_SERVICES rigassatiksme_qr_bot
      ;;
    ticket_remote)
      VALIDATE_TICKET_REMOTE=1
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
      append_unique DIAGNOSTIC_SERVICES phone_broker
      append_unique DIAGNOSTIC_SERVICES ticket_remote
      append_unique DIAGNOSTIC_SERVICES ticket_remote_tunnel
      ;;
    dns)
      VALIDATE_DNS=1
      append_unique DIAGNOSTIC_SERVICES dns_controlplane
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
        append_unique COMPOSE_TARGET_SERVICES phone_broker
        mark_validation_group phone_broker
        ;;
      phone_broker)
        append_unique COMPOSE_TARGET_SERVICES ticket_phone_bridge
        append_unique COMPOSE_TARGET_SERVICES phone_broker
        mark_validation_group phone_broker
        ;;
      rigassatiksme_qr_bot)
        append_unique COMPOSE_TARGET_SERVICES ticket_phone_bridge
        append_unique COMPOSE_TARGET_SERVICES phone_broker
        append_unique COMPOSE_TARGET_SERVICES rigassatiksme_qr_bot
        mark_validation_group rigassatiksme_qr
        ;;
      ticket_remote)
        append_unique COMPOSE_TARGET_SERVICES ticket_phone_bridge
        append_unique COMPOSE_TARGET_SERVICES phone_broker
        append_unique COMPOSE_TARGET_SERVICES ticket_remote
        append_unique COMPOSE_TARGET_SERVICES ticket_remote_tunnel
        mark_validation_group ticket_remote
        ;;
      ticket_remote_tunnel)
        append_unique COMPOSE_TARGET_SERVICES ticket_remote_tunnel
        mark_validation_group ticket_remote
        ;;
      dns_controlplane)
        append_unique COMPOSE_TARGET_SERVICES "${service_name}"
        mark_validation_group dns
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

compose_target_service_args_without_dns() {
  local service_args=""
  local service_name
  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    case "${service_name}" in
      dns_controlplane|train_tunnel|satiksme_tunnel|subscription_tunnel|ticket_remote_tunnel)
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

compose_all_non_dns_service_args() {
  local service_args=""
  local service_name
  local non_dns_services=(
    portainer
    train_bot
    satiksme_bot
    subscription_bot
    ticket_phone_bridge
    phone_broker
    rigassatiksme_qr_bot
    ticket_remote
  )
  for service_name in "${non_dns_services[@]}"; do
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

requires_dns_release_prepare() {
  local service_name

  if (( TARGETED_MODE == 0 )); then
    return 0
  fi

  for service_name in ${COMPOSE_TARGET_SERVICES[@]+"${COMPOSE_TARGET_SERVICES[@]}"}; do
    case "${service_name}" in
      dns_controlplane)
        return 0
        ;;
    esac
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

is_dns_admin_bind_ipv4() {
  is_private_ipv4 "${1:-}" || is_tailscale_ipv4 "${1:-}"
}

dns_validation_requested() {
  if (( TARGETED_MODE == 0 || VALIDATE_DNS == 1 )); then
    return 0
  fi
  return 1
}

require_dns_private_admin_env() {
  if [[ -z "${ARBUZAS_DNS_ADMIN_LAN_IP}" ]]; then
    echo "ARBUZAS_DNS_ADMIN_LAN_IP is required for the private Arbuzas DNS admin surface" >&2
    exit 2
  fi
  if ! is_dns_admin_bind_ipv4 "${ARBUZAS_DNS_ADMIN_LAN_IP}"; then
    echo "ARBUZAS_DNS_ADMIN_LAN_IP must be a private RFC1918 or Tailscale IPv4 address (got: ${ARBUZAS_DNS_ADMIN_LAN_IP})" >&2
    exit 2
  fi
}

dns_https_url() {
  local path="$1"
  local base="https://${ARBUZAS_DNS_HOSTNAME}"
  if [[ "${ARBUZAS_DNS_HTTPS_PORT}" != "443" ]]; then
    base="${base}:${ARBUZAS_DNS_HTTPS_PORT}"
  fi
  printf '%s%s\n' "${base}" "${path}"
}

dns_probe_query_base64url() {
  python3 - <<'PY'
import base64
import struct

labels = "example.com".split(".")
question = b"".join(bytes([len(label)]) + label.encode("ascii") for label in labels) + b"\x00"
question += struct.pack("!HH", 1, 1)
query = struct.pack("!HHHHHH", 0x4242, 0x0100, 1, 0, 0, 0) + question
print(base64.urlsafe_b64encode(query).rstrip(b"=").decode("ascii"))
PY
}

probe_doh_endpoint() {
  local connect_ip="${1:-}"
  local doh_query=""

  doh_query="$(dns_probe_query_base64url)" || return 1
  python3 - "${connect_ip}" "${ARBUZAS_DNS_HOSTNAME}" "${ARBUZAS_DNS_HTTPS_PORT}" "${doh_query}" <<'PY'
import http.client
import ssl
import sys

connect_ip = sys.argv[1]
hostname = sys.argv[2]
port = int(sys.argv[3])
doh_query = sys.argv[4]

connect_host = connect_ip or hostname
context = ssl._create_unverified_context()
connection = http.client.HTTPSConnection(connect_host, port, context=context, timeout=10)
try:
    connection.request(
        "GET",
        f"/dns-query?dns={doh_query}",
        headers={
            "Host": hostname,
            "Accept": "application/dns-message",
        },
    )
    response = connection.getresponse()
    content_type = response.getheader("Content-Type", "")
    body = response.read()
    if response.status != 200:
        raise SystemExit(f"unexpected DoH status: {response.status}")
    if not content_type.lower().startswith("application/dns-message"):
        raise SystemExit(f"unexpected DoH content type: {content_type}")
    if not body:
        raise SystemExit("empty DoH response body")
finally:
    connection.close()
PY
}

probe_public_https_status() {
  local path="$1"
  local expected_status="$2"
  local connect_ip="${3:-}"
  local url=""
  local status=""

  url="$(dns_https_url "${path}")"
  if [[ -n "${connect_ip}" ]]; then
    status="$(
      curl --resolve "${ARBUZAS_DNS_HOSTNAME}:${ARBUZAS_DNS_HTTPS_PORT}:${connect_ip}" \
        -sk \
        -o /dev/null \
        -w '%{http_code}' \
        "${url}"
    )" || return 1
  else
    status="$(curl -sk -o /dev/null -w '%{http_code}' "${url}")" || return 1
  fi

  [[ "${status}" == "${expected_status}" ]]
}

probe_dot_endpoint() {
  local connect_host="${1:-${ARBUZAS_DNS_HOSTNAME}}"

  python3 - "${connect_host}" "${ARBUZAS_DNS_HOSTNAME}" "${ARBUZAS_DNS_DOT_PORT}" <<'PY'
import socket
import ssl
import struct
import sys

connect_host = sys.argv[1]
server_name = sys.argv[2]
port = int(sys.argv[3])

labels = "example.com".split(".")
question = b"".join(bytes([len(label)]) + label.encode("ascii") for label in labels) + b"\x00"
question += struct.pack("!HH", 1, 1)
query = struct.pack("!HHHHHH", 0x4343, 0x0100, 1, 0, 0, 0) + question

context = ssl.create_default_context()
with socket.create_connection((connect_host, port), timeout=10) as raw_stream:
    with context.wrap_socket(raw_stream, server_hostname=server_name) as tls_stream:
        tls_stream.sendall(struct.pack("!H", len(query)) + query)
        prefix = tls_stream.recv(2)
        if len(prefix) != 2:
            raise SystemExit("missing DoT response length prefix")
        response_len = struct.unpack("!H", prefix)[0]
        payload = b""
        while len(payload) < response_len:
            chunk = tls_stream.recv(response_len - len(payload))
            if not chunk:
                raise SystemExit("truncated DoT response")
            payload += chunk
        if len(payload) < 4:
            raise SystemExit("short DoT response")
        flags = struct.unpack("!H", payload[2:4])[0]
        if (flags & 0x8000) == 0:
            raise SystemExit("DoT response did not set the QR bit")
PY
}

resolve_remote_public_ipv4() {
  local ip=""
  ip="$(
    remote_shell "
      if [[ -r '/srv/arbuzas/dns/state/ddns-last-ipv4' ]]; then
        ip=\$(tr -d '\r\n[:space:]' < '/srv/arbuzas/dns/state/ddns-last-ipv4')
        if [[ \"\${ip}\" =~ ^([0-9]{1,3}[.]){3}[0-9]{1,3}$ ]]; then
          printf '%s\n' \"\${ip}\"
          exit 0
        fi
      fi
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

resolve_remote_tailscale_dns_name() {
  local dns_name=""

  dns_name="$(
    remote_inline_shell "
      python3 - <<'PY'
import json
import subprocess

payload = json.loads(subprocess.check_output(['tailscale', 'status', '--json'], text=True))
dns_name = payload.get('Self', {}).get('DNSName', '').rstrip('.')
if not dns_name:
    raise SystemExit('missing Arbuzas Tailscale DNS name')
print(dns_name)
PY
    " 2>/dev/null | tail -n 1 | tr -d '\r\n[:space:]'
  )" || return 1

  [[ -n "${dns_name}" ]] || return 1
  printf '%s\n' "${dns_name}"
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

render_dns_admin_nginx_config() {
  local tailnet_dns_name="$1"
  local tailnet_hostname="$2"
  local tailnet_ipv4="$3"
  local tailnet_ipv6="$4"

  [[ -f "${DNS_ADMIN_NGINX_TEMPLATE_FILE}" ]] || {
    echo "missing DNS admin nginx template: ${DNS_ADMIN_NGINX_TEMPLATE_FILE}" >&2
    return 1
  }

  python3 - "${DNS_ADMIN_NGINX_TEMPLATE_FILE}" "${tailnet_dns_name}" "${tailnet_hostname}" "${tailnet_ipv4}" "${tailnet_ipv6}" "${ARBUZAS_DNS_CONTROLPLANE_PORT}" <<'PY'
from pathlib import Path
import sys

template_path = Path(sys.argv[1])
tailnet_dns_name = sys.argv[2]
tailnet_hostname = sys.argv[3]
tailnet_ipv4 = sys.argv[4]
tailnet_ipv6 = sys.argv[5]
controlplane_port = sys.argv[6]
server_names = []
for candidate in (tailnet_hostname, tailnet_dns_name):
    candidate = candidate.strip()
    if candidate and candidate not in server_names:
        server_names.append(candidate)

rendered = template_path.read_text(encoding="utf-8")
rendered = rendered.replace("__DNS_ADMIN_SERVER_NAMES__", " ".join(server_names))
rendered = rendered.replace("__DNS_ADMIN_LISTEN_IPV4__", tailnet_ipv4)
rendered = rendered.replace("__DNS_ADMIN_LISTEN_IPV6__", tailnet_ipv6)
rendered = rendered.replace("__DNS_ADMIN_CONTROLPLANE_PORT__", controlplane_port)
print(rendered, end="")
PY
}

publish_remote_dns_admin_tailscale() {
  local tailnet_dns_name=""
  local tailnet_hostname=""
  local tailnet_ipv4=""
  local tailnet_ipv6=""
  local nginx_config_base64=""

  tailnet_dns_name="$(resolve_remote_tailscale_dns_name)" || {
    echo "Could not determine the Arbuzas Tailscale DNS name for the bare DNS admin URL." >&2
    exit 1
  }
  tailnet_hostname="$(resolve_remote_tailscale_hostname)" || {
    echo "Could not determine the Arbuzas Tailscale short hostname for the bare DNS admin URL." >&2
    exit 1
  }
  tailnet_ipv4="$(resolve_remote_tailscale_ipv4)" || {
    echo "Could not determine the Arbuzas Tailscale IPv4 address for the bare DNS admin URL." >&2
    exit 1
  }
  tailnet_ipv6="$(resolve_remote_tailscale_ipv6)" || {
    echo "Could not determine the Arbuzas Tailscale IPv6 address for the bare DNS admin URL." >&2
    exit 1
  }
  nginx_config_base64="$(
    render_dns_admin_nginx_config "${tailnet_dns_name}" "${tailnet_hostname}" "${tailnet_ipv4}" "${tailnet_ipv6}" | base64 | tr -d '\n'
  )" || exit 1

  log "Maintenance: publishing the Arbuzas DNS admin surface privately over Tailscale"
  remote_root_command "
    command -v tailscale >/dev/null 2>&1 || {
      echo 'tailscale is required for the Arbuzas DNS private admin forward' >&2
      exit 1
    }
    command -v nginx >/dev/null 2>&1 || {
      echo 'nginx is required for the bare Arbuzas DNS admin URL' >&2
      exit 1
    }
    # DNS owns host port 443 directly; clear any stale Tailscale HTTPS proxy first.
    tailscale serve --bg --https=443 off >/dev/null 2>&1 || true
    tailscale serve --bg --yes --tcp ${ARBUZAS_DNS_CONTROLPLANE_PORT} 127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}
    install -d '$(dirname "${DNS_ADMIN_NGINX_REMOTE_SITE_FILE}")' '$(dirname "${DNS_ADMIN_NGINX_REMOTE_SITE_LINK}")'
    printf '%s' '${nginx_config_base64}' | base64 -d > '${DNS_ADMIN_NGINX_REMOTE_SITE_FILE}'
    ln -sfn '${DNS_ADMIN_NGINX_REMOTE_SITE_FILE}' '${DNS_ADMIN_NGINX_REMOTE_SITE_LINK}'
    nginx -t
    if command -v systemctl >/dev/null 2>&1; then
      if systemctl is-active --quiet nginx; then
        systemctl reload nginx
      else
        systemctl start nginx
      fi
    else
      nginx -s reload
    fi
    curl -fsS -H 'Host: ${tailnet_dns_name}' 'http://127.0.0.1/' >/dev/null 2>/dev/null
  "
  log "Maintenance: private DNS admin root is available at http://${tailnet_dns_name}/"
}

collect_remote_dns_host_diagnostics() {
  remote_root_command "
    echo '--- tailscale serve status ---' >&2
    if command -v tailscale >/dev/null 2>&1; then
      tailscale serve status >&2 || true
    else
      echo 'tailscale not installed' >&2
    fi
    echo '--- dns host listeners ---' >&2
    ss -H -ltnp | awk '\$4 ~ /:80$|:443$|:853$|:8097$/ { print }' >&2 || true
    echo '--- docker published dns ports ---' >&2
    docker ps --format '{{.Names}}|{{.Ports}}' | grep -E '(:443->|:853->|:8097->)' >&2 || true
  " || true
}

ensure_remote_dns_host_preflight() {
  local repair_cmd=""

  repair_cmd="ARBUZAS_HOST='${ARBUZAS_HOST}' ARBUZAS_USER='${ARBUZAS_USER}' ARBUZAS_SSH_PORT='${ARBUZAS_SSH_PORT}' ARBUZAS_DNS_ADMIN_LAN_IP='${ARBUZAS_DNS_ADMIN_LAN_IP}' bash tools/arbuzas/deploy.sh repair-dns-admin"
  log "Preflight: checking Arbuzas DNS host listeners before cutover"
  if remote_root_command "
    DNS_ADMIN_LAN_IP='${ARBUZAS_DNS_ADMIN_LAN_IP}' \
    DNS_SAFE_REPAIR_COMMAND='${repair_cmd}' \
    python3 - <<'PY'
import os
import subprocess
import sys

dns_container = 'arbuzas-dns_controlplane-1'
lan_ip = os.environ['DNS_ADMIN_LAN_IP']
repair_cmd = os.environ['DNS_SAFE_REPAIR_COMMAND']
interesting_ports = {'443', '853', '8097'}


def load_output(command):
    result = subprocess.run(command, capture_output=True, text=True, check=True)
    return result.stdout.splitlines()


listeners = []
for line in load_output(['ss', '-H', '-ltnp']):
    parts = line.split()
    if len(parts) < 5:
        continue
    local_field = parts[3]
    if ':' not in local_field:
        continue
    port = local_field.rsplit(':', 1)[-1]
    if port in interesting_ports:
        listeners.append((port, local_field, line))

docker_rows = load_output(['docker', 'ps', '--format', '{{.Names}}|{{.Ports}}'])
offenders = []
repairable = False

for row in docker_rows:
    name, _, ports = row.partition('|')
    if not ports.strip():
        continue
    if any(marker in ports for marker in (':443->', ':853->', ':8097->')) and name.strip() != dns_container:
        offenders.append(('conflicting Docker publisher', row))

for port, local_field, line in listeners:
    is_docker_proxy = 'docker-proxy' in line
    is_tailscaled = 'tailscaled' in line
    if port in {'443', '853'}:
        if not is_docker_proxy:
            offenders.append((f'conflicting host listener on {port}', line))
            if is_tailscaled and port == '443':
                repairable = True
        continue

    if port != '8097':
        continue
    if is_tailscaled:
        continue
    if is_docker_proxy and local_field in {f'127.0.0.1:{port}', f'{lan_ip}:{port}'}:
        continue
    offenders.append(('unexpected DNS admin listener on 8097', line))

if offenders:
    print('DNS host preflight failed on the live host; fix the listener conflict before retrying.', file=sys.stderr)
    for label, line in offenders:
        print(f'- {label}: {line}', file=sys.stderr)
    if repairable:
        print(f'Safe repair: {repair_cmd}', file=sys.stderr)
    else:
        print('Safe repair only applies to stale private DNS admin forwarding. If this is a different service, free the port manually and retry.', file=sys.stderr)
    raise SystemExit(1)
PY
  "; then
    return 0
  fi

  collect_remote_dns_host_diagnostics
  return 1
}

repair_remote_dns_admin() {
  log "Maintenance: repairing the Arbuzas DNS private admin forwarding"
  publish_remote_dns_admin_tailscale
  collect_remote_dns_host_diagnostics
  validate_private_dns_admin_access "${REMOTE_CURRENT_LINK}"
}

validate_public_dns_access() {
  local diagnostics_release_dir="$1"
  local public_ip=""
  local path=""

  for path in / /login /dns/login /v1/health /livez /healthz; do
    log "Validate: dns public HTTPS keeps ${path} closed"
    if ! wait_until_local_ok probe_public_https_status "${path}" 404 >/dev/null 2>&1; then
      if ! is_valid_ipv4 "${public_ip}"; then
        public_ip="$(resolve_remote_public_ipv4 || true)"
      fi
      if is_valid_ipv4 "${public_ip}" && wait_until_local_ok probe_public_https_status "${path}" 404 "${public_ip}" >/dev/null 2>&1; then
        log "Validate: dns public HTTPS keeps ${path} closed via fallback IP ${public_ip}"
      else
        log "Validation failed: dns public HTTPS keeps ${path} closed"
        echo "Public DNS web access looks too open: ${path} on ${ARBUZAS_DNS_HOSTNAME}:${ARBUZAS_DNS_HTTPS_PORT} is not returning 404." >&2
        collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
        return 1
      fi
    fi
  done

  log "Validate: dns public DoH query"
  if ! wait_until_local_ok probe_doh_endpoint >/dev/null 2>&1; then
    if ! is_valid_ipv4 "${public_ip}"; then
      public_ip="$(resolve_remote_public_ipv4 || true)"
    fi
    if is_valid_ipv4 "${public_ip}" && wait_until_local_ok probe_doh_endpoint "${public_ip}" >/dev/null 2>&1; then
      log "Validate: dns public DoH query via fallback IP ${public_ip}"
    else
      log "Validation failed: dns public DoH query"
      echo "Public DNS-over-HTTPS looks broken: ${ARBUZAS_DNS_HOSTNAME}:${ARBUZAS_DNS_HTTPS_PORT} did not return a DNS message even though the Arbuzas-local HTTPS listener already passed." >&2
      collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
      return 1
    fi
  fi

  log "Validate: dns public DoT query"
  if ! wait_until_local_ok probe_dot_endpoint >/dev/null 2>&1; then
    if ! is_valid_ipv4 "${public_ip}"; then
      public_ip="$(resolve_remote_public_ipv4 || true)"
    fi
    if is_valid_ipv4 "${public_ip}" && wait_until_local_ok probe_dot_endpoint "${public_ip}" >/dev/null 2>&1; then
      log "Validate: dns public DoT query via fallback IP ${public_ip}"
    else
      log "Validation failed: dns public DoT query"
      echo "Public DNS-over-TLS looks broken: ${ARBUZAS_DNS_HOSTNAME}:${ARBUZAS_DNS_DOT_PORT} did not return a DNS answer even though the Arbuzas-local DoT listener already passed." >&2
      collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
      return 1
    fi
  fi
}

validate_private_dns_admin_access() {
  local diagnostics_release_dir="$1"
  local tailnet_ipv4=""
  local tailnet_dns_name=""

  log "Validate: dns private admin login on live host loopback"
  if ! remote_shell "curl -fsS 'http://127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login' >/dev/null 2>/dev/null"; then
    log "Validation failed: dns private admin login on live host loopback"
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  log "Validate: dns private admin login on live host LAN address"
  if ! remote_shell "curl -fsS 'http://${ARBUZAS_DNS_ADMIN_LAN_IP}:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login' >/dev/null 2>/dev/null"; then
    log "Validation failed: dns private admin login on live host LAN address"
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  tailnet_ipv4="$(resolve_remote_tailscale_ipv4 || true)"
  if ! is_valid_ipv4 "${tailnet_ipv4}"; then
    log "Validation failed: dns private admin Tailscale address"
    echo "Could not determine the Arbuzas Tailscale IPv4 address for the private DNS admin check." >&2
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  tailnet_dns_name="$(resolve_remote_tailscale_dns_name || true)"
  if [[ -z "${tailnet_dns_name}" ]]; then
    log "Validation failed: dns private admin Tailscale DNS name"
    echo "Could not determine the Arbuzas Tailscale DNS name for the bare DNS admin URL check." >&2
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  log "Validate: dns private admin root on live host nginx"
  if ! remote_shell "curl -fsS -H 'Host: ${tailnet_dns_name}' 'http://127.0.0.1/' >/dev/null 2>/dev/null"; then
    log "Validation failed: dns private admin root on live host nginx"
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  log "Validate: dns private admin bare URL over Tailscale"
  if ! wait_until_local_ok curl -fsS "http://${tailnet_dns_name}/" >/dev/null 2>&1; then
    log "Validation failed: dns private admin bare URL over Tailscale"
    echo "Private DNS admin bare URL over Tailscale looks broken: http://${tailnet_dns_name}/ did not answer." >&2
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  log "Validate: dns private admin login over Tailscale"
  if ! wait_until_local_ok curl -fsS "http://${tailnet_ipv4}:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login" >/dev/null 2>&1; then
    log "Validation failed: dns private admin login over Tailscale"
    echo "Private DNS admin access over Tailscale looks broken: http://${tailnet_ipv4}:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login did not answer." >&2
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi
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
        or 'app.adguardhome' in descriptor
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
  if [[ -n "$(git -C "${REPO_ROOT}" status --porcelain --untracked-files=all -- infra/arbuzas/docker workloads/shared-go workloads/train-bot workloads/satiksme-bot workloads/phone-broker workloads/rigassatiksme-qr-bot)" ]]; then
    printf 'dirty\n'
  else
    printf 'clean\n'
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
    pathlib.Path("workloads/shared-go"),
    pathlib.Path("workloads/train-bot"),
    pathlib.Path("workloads/satiksme-bot"),
    pathlib.Path("workloads/phone-broker"),
    pathlib.Path("workloads/rigassatiksme-qr-bot"),
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

prepare_local_release_bundle() {
  log "Preparing local release bundle ${ARBUZAS_RELEASE_ID}"
  rm -rf "${ARBUZAS_RELEASE_DIR}"
  mkdir -p "${ARBUZAS_RELEASE_DIR}/generated/cloudflared"

  copy_tree_into_release "infra/arbuzas/docker"
  copy_tree_into_release "tools/arbuzas-rs"
  copy_tree_into_release "workloads/shared-go"
  copy_tree_into_release "workloads/train-bot"
  copy_tree_into_release "workloads/satiksme-bot"
  copy_tree_into_release "workloads/subscription-bot"
  copy_tree_into_release "workloads/phone-broker"
  copy_tree_into_release "workloads/rigassatiksme-qr-bot"
  copy_tree_into_release "workloads/ticket-remote"

  mkdir -p "${ARBUZAS_RELEASE_DIR}/tools/arbuzas"
  cp "${REPO_ROOT}/tools/arbuzas/render_cloudflared_config.py" "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/render_cloudflared_config.py"
  if [[ -f "${REPO_ROOT}/tools/arbuzas/docker_gc.py" ]]; then
    cp "${REPO_ROOT}/tools/arbuzas/docker_gc.py" "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/docker_gc.py"
  fi

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
ARBUZAS_PHONE_BROKER_PORT=${ARBUZAS_PHONE_BROKER_PORT}
ARBUZAS_TICKET_PHONE_ADB_TARGET=${ARBUZAS_TICKET_PHONE_ADB_TARGET}
ARBUZAS_DNS_HTTPS_PORT=${ARBUZAS_DNS_HTTPS_PORT}
ARBUZAS_DNS_DOT_PORT=${ARBUZAS_DNS_DOT_PORT}
ARBUZAS_DNS_CONTROLPLANE_PORT=${ARBUZAS_DNS_CONTROLPLANE_PORT}
ARBUZAS_DNS_ADMIN_LAN_IP=${ARBUZAS_DNS_ADMIN_LAN_IP}
ARBUZAS_TRAIN_BOT_HOSTNAME=${ARBUZAS_TRAIN_BOT_HOSTNAME}
ARBUZAS_SATIKSME_BOT_HOSTNAME=${ARBUZAS_SATIKSME_BOT_HOSTNAME}
ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME=${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME}
ARBUZAS_TICKET_REMOTE_HOSTNAME=${ARBUZAS_TICKET_REMOTE_HOSTNAME}
ARBUZAS_TICKET_REMOTE_AUTH_MODE=${ARBUZAS_TICKET_REMOTE_AUTH_MODE:-spacetime}
ARBUZAS_TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN=${ARBUZAS_TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN:-}
ARBUZAS_TICKET_REMOTE_CF_ACCESS_AUDIENCE=${ARBUZAS_TICKET_REMOTE_CF_ACCESS_AUDIENCE:-}
ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_ISSUER=${ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_ISSUER:-https://auth.spacetimedb.com/oidc}
ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID=${ARBUZAS_TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID:-}
ARBUZAS_DNS_HOSTNAME=${ARBUZAS_DNS_HOSTNAME}
ARBUZAS_PORTAINER_IMAGE=${ARBUZAS_PORTAINER_IMAGE}
ARBUZAS_CLOUDFLARED_IMAGE=${ARBUZAS_CLOUDFLARED_IMAGE}
EOF
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
      '/srv/arbuzas/dns/state' \
      '/srv/arbuzas/dns/runtime' \
      '/srv/arbuzas/dns/run' \
      '/srv/arbuzas/dns/logs' \
      '/etc/arbuzas/env' \
      '/etc/arbuzas/releases' \
      '/etc/arbuzas/docker-gc' \
      '/etc/arbuzas/dns/tls' \
      '/etc/arbuzas/dns/secrets' \
      '/etc/arbuzas/cloudflared' \
      '/etc/arbuzas/secrets'
    if [[ ! -f '${DOCKER_GC_REMOTE_STATE_FILE}' && -r '/srv/arbuzas/docker-gc/state.json' ]]; then
      cp '/srv/arbuzas/docker-gc/state.json' '${DOCKER_GC_REMOTE_STATE_FILE}'
    fi
    touch \
      '/etc/arbuzas/env/train-bot.env' \
      '/etc/arbuzas/env/satiksme-bot.env' \
      '/etc/arbuzas/env/subscription-bot.env' \
      '/etc/arbuzas/env/ticket-remote.env'
  "
}

copy_release_to_remote() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  local remote_tmp_dir="${remote_release_dir}.uploading.$$"
  local remote_tarball="/tmp/arbuzas-${ARBUZAS_RELEASE_ID}.$$.tar"
  local local_tarball=""

  local_tarball="$(mktemp "${TMPDIR:-/tmp}/arbuzas-${ARBUZAS_RELEASE_ID}.XXXXXX.tar")"
  trap 'rm -f "${local_tarball}"' RETURN

  log "Packing release bundle ${ARBUZAS_RELEASE_ID}"
  (
    cd "${ARBUZAS_RELEASE_DIR}"
    COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -cf "${local_tarball}" .
  )

  log "Uploading release bundle to ${ARBUZAS_HOST}:${remote_tarball}"
  upload_remote_file "${local_tarball}" "${remote_tarball}"

  remote_shell "
    rm -rf '${remote_tmp_dir}'
    mkdir -p '${remote_tmp_dir}'
    tar -C '${remote_tmp_dir}' -xf '${remote_tarball}'
    rm -f '${remote_tarball}'
  "

  remote_shell "
    [[ -f '${remote_tmp_dir}/release.env' ]] || { echo 'incomplete upload: missing release.env in ${remote_tmp_dir}' >&2; exit 1; }
    rm -rf '${remote_release_dir}'
    mv '${remote_tmp_dir}' '${remote_release_dir}'
  "
}

render_remote_cloudflared_configs() {
  local remote_release_dir="${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
  remote_shell "
    mkdir -p '${remote_release_dir}/generated/cloudflared'
    python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/train-bot.json' \
      --hostname '${ARBUZAS_TRAIN_BOT_HOSTNAME}' \
      --upstream 'http://train_bot:${ARBUZAS_TRAIN_BOT_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/train-bot.yml'
    python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/satiksme-bot.json' \
      --hostname '${ARBUZAS_SATIKSME_BOT_HOSTNAME}' \
      --upstream 'http://satiksme_bot:${ARBUZAS_SATIKSME_BOT_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/satiksme-bot.yml'
    python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
      --credentials-file '/etc/arbuzas/cloudflared/subscription-bot.json' \
      --hostname '${ARBUZAS_SUBSCRIPTION_BOT_HOSTNAME}' \
      --upstream 'http://subscription_bot:${ARBUZAS_SUBSCRIPTION_BOT_PORT}' \
      --out '${remote_release_dir}/generated/cloudflared/subscription-bot.yml'
    python3 '${remote_release_dir}/tools/arbuzas/render_cloudflared_config.py' \
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
  local non_dns_service_args=""
  local all_non_dns_service_args=""
  local tunnel_service_args=""
  local dns_release_prepare_needed="false"
  non_dns_service_args="$(compose_target_service_args_without_dns)"
  all_non_dns_service_args="$(compose_all_non_dns_service_args)"
  if (( TARGETED_MODE == 1 )); then
    tunnel_service_args="$(compose_target_tunnel_service_args)"
  else
    tunnel_service_args="$(compose_all_tunnel_service_args)"
  fi

  if requires_dns_release_prepare; then
    dns_release_prepare_needed="true"
    ensure_remote_dns_host_preflight
  fi

  if (( TARGETED_MODE == 1 )); then
    remote_shell "
      cd '${remote_release_dir}'
      if ${dns_release_prepare_needed}; then
        docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' build dns_controlplane
        docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns migrate --json </dev/null
        docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json </dev/null
        if [[ -f '${REMOTE_CURRENT_LINK}/release.env' ]]; then
          docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' stop dns_controlplane frontend adguardhome >/dev/null 2>&1 || true
        fi
      fi
      ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
      cd '${REMOTE_CURRENT_LINK}'
      if ${dns_release_prepare_needed}; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps dns_controlplane
      fi
      if [[ -n '${non_dns_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --no-deps${non_dns_service_args}
      fi
      if [[ -n '${tunnel_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${tunnel_service_args}
      fi
    "
    return
  fi

  remote_shell "
    cd '${remote_release_dir}'
    docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' build dns_controlplane
    docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns migrate --json </dev/null
    docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json </dev/null
    if [[ -f '${REMOTE_CURRENT_LINK}/release.env' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' stop dns_controlplane frontend adguardhome >/dev/null 2>&1 || true
    fi
    ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --remove-orphans${all_non_dns_service_args}
    if [[ -n '${tunnel_service_args}' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${tunnel_service_args}
    fi
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps dns_controlplane
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

validate_remote_dns_querylog_flow() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "dns local encrypted queries and query logging on the live host" \
    "wait_until_ok python3 - <<'PY'
import base64
import json
import socket
import sqlite3
import ssl
import struct
import time
import urllib.request

db_path = '/srv/arbuzas/dns/state/controlplane.sqlite'
hostname = '${ARBUZAS_DNS_HOSTNAME}'
https_port = int('${ARBUZAS_DNS_HTTPS_PORT}')
dot_port = int('${ARBUZAS_DNS_DOT_PORT}')

def query_count():
    conn = sqlite3.connect(f'file:{db_path}?mode=ro', uri=True)
    try:
        return conn.execute('SELECT COUNT(*) FROM querylog_mirror_rows').fetchone()[0]
    finally:
        conn.close()

labels = 'example.com'.split('.')
question = b''.join(bytes([len(label)]) + label.encode('ascii') for label in labels) + b'\\x00'
question += struct.pack('!HH', 1, 1)
query = struct.pack('!HHHHHH', 0x5151, 0x0100, 1, 0, 0, 0) + question
query_b64 = base64.urlsafe_b64encode(query).rstrip(b'=').decode('ascii')
before = query_count()

context = ssl._create_unverified_context()
request = urllib.request.Request(
    f'https://127.0.0.1:{https_port}/dns-query?dns={query_b64}',
    headers={
        'Host': hostname,
        'Accept': 'application/dns-message',
    },
)
with urllib.request.urlopen(request, context=context, timeout=5) as response:
    if response.headers.get_content_type() != 'application/dns-message':
        raise SystemExit('DoH probe returned the wrong content type')
    if not response.read():
        raise SystemExit('DoH probe returned an empty body')

with socket.create_connection(('127.0.0.1', dot_port), timeout=5) as raw_stream:
    with context.wrap_socket(raw_stream, server_hostname=hostname) as tls_stream:
        tls_stream.sendall(struct.pack('!H', len(query)) + query)
        prefix = tls_stream.recv(2)
        if len(prefix) != 2:
            raise SystemExit('DoT probe did not return a response prefix')
        response_len = struct.unpack('!H', prefix)[0]
        payload = b''
        while len(payload) < response_len:
            chunk = tls_stream.recv(response_len - len(payload))
            if not chunk:
                raise SystemExit('DoT probe returned a truncated response')
            payload += chunk
        if len(payload) < 4 or (struct.unpack('!H', payload[2:4])[0] & 0x8000) == 0:
            raise SystemExit('DoT probe did not return a DNS answer')

deadline = time.time() + 10
while time.time() < deadline:
    if query_count() > before:
        break
    time.sleep(0.5)
else:
    raise SystemExit('querylog row count did not increase after encrypted DNS traffic')
PY" \
    dns_controlplane
}

validate_remote_dns_native_api_probe() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "dns native stats and clients APIs on the live host" \
    "wait_until_ok python3 - <<'PY'
import json
import urllib.error
import urllib.request

base = 'http://127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}'

for path in [
    '/dns/api/stats?interval=24_hours',
    '/dns/api/clients',
]:
    request = urllib.request.Request(f'{base}{path}')
    try:
        with urllib.request.urlopen(request, timeout=5) as response:
            json.loads(response.read())
    except urllib.error.HTTPError as error:
        if error.code != 401:
            raise
PY" \
    dns_controlplane
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

validate_remote_phone_broker_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" ticket_phone_bridge phone_broker
  validate_remote_probe "${remote_release_dir}" "ticket-phone-bridge local health" \
    "wait_until_ok compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker
  validate_remote_probe "${remote_release_dir}" "phone-broker local health" \
    "wait_until_ok compose exec -T phone_broker sh -lc 'curl -fsS \"http://127.0.0.1:${ARBUZAS_PHONE_BROKER_PORT}/api/v1/health?strict=1\" >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker
}

validate_remote_rigassatiksme_qr_bot_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" ticket_phone_bridge phone_broker rigassatiksme_qr_bot
  validate_remote_probe "${remote_release_dir}" "ticket-phone-bridge local health" \
    "wait_until_ok compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker rigassatiksme_qr_bot
  validate_remote_probe "${remote_release_dir}" "rigassatiksme QR bot broker health" \
    "wait_until_ok compose exec -T phone_broker sh -lc 'curl -fsS \"http://127.0.0.1:${ARBUZAS_PHONE_BROKER_PORT}/api/v1/health?strict=1\" >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker rigassatiksme_qr_bot
}

validate_remote_ticket_remote_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" ticket_phone_bridge phone_broker ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-phone-bridge local health" \
    "wait_until_ok compose exec -T ticket_phone_bridge sh -lc '/usr/local/bin/ticket-phone-bridge-health >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "phone-broker local health" \
    "wait_until_ok compose exec -T phone_broker sh -lc 'curl -fsS \"http://127.0.0.1:${ARBUZAS_PHONE_BROKER_PORT}/api/v1/health?strict=1\" >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote local health" \
    "wait_until_ok compose exec -T ticket_remote sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null 2>/dev/null'" \
    ticket_phone_bridge phone_broker ticket_remote ticket_remote_tunnel
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
      compose exec -T ticket_remote sh -lc 'test \"\${TICKET_REMOTE_PHONE_BACKEND_ID}\" = pixel && test \"\${TICKET_REMOTE_PHONE_BROKER_URL}\" = \"http://phone_broker:${ARBUZAS_PHONE_BROKER_PORT}\" && curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null'
    }
    wait_until_ok active_configured_backend_ok" \
    ticket_phone_bridge phone_broker ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote public login shell" \
    "wait_until_ok sh -lc 'code=\$(curl -sS -o /dev/null -w \"%{http_code}\" https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/ 2>/dev/null || true); case \"\${code}\" in 200|302) exit 0 ;; *) exit 1 ;; esac'" \
    ticket_phone_bridge phone_broker ticket_remote ticket_remote_tunnel
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
      grep -aF \"RTCPeerConnection\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"webrtc_ice_config\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"webrtcVideo\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"iceTransportPolicy\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"Savieno WebRTC video\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"TURN\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"legacy_frame_in_tsf2_stream\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"version: '\\''legacy'\\''\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"configuredFrameEnvelope\" \"\${binary}\" >/dev/null && exit 1
      grep -aF \"|| '\\''legacy'\\''\" \"\${binary}\" >/dev/null && exit 1
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

validate_remote_dns_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" dns_controlplane
  validate_remote_probe "${remote_release_dir}" "dns private admin login on the live host" \
    "wait_until_ok sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login >/dev/null 2>/dev/null'" \
    dns_controlplane
  validate_remote_dns_querylog_flow "${remote_release_dir}"
  validate_remote_dns_native_api_probe "${remote_release_dir}"
  validate_remote_probe "${remote_release_dir}" "dns controlplane healthcheck" \
    "wait_until_ok compose exec -T dns_controlplane /usr/local/bin/arbuzas-dns health --json --strict >/dev/null 2>/dev/null" \
    dns_controlplane
  validate_remote_probe "${remote_release_dir}" "dns controlplane release validation" \
    "wait_until_ok compose exec -T dns_controlplane /usr/local/bin/arbuzas-dns release validate --json >/dev/null 2>/dev/null" \
    dns_controlplane
}

validate_remote_workload_health() {
  local remote_release_dir="$1"

  validate_remote_portainer_health "${remote_release_dir}"
  validate_remote_train_workload_health "${remote_release_dir}"
  validate_remote_satiksme_workload_health "${remote_release_dir}"
  validate_remote_subscription_workload_health "${remote_release_dir}"
  validate_remote_phone_broker_workload_health "${remote_release_dir}"
  validate_remote_rigassatiksme_qr_bot_workload_health "${remote_release_dir}"
  validate_remote_ticket_remote_workload_health "${remote_release_dir}"
  validate_remote_dns_workload_health "${remote_release_dir}"
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
  if (( VALIDATE_PHONE_BROKER == 1 )); then
    validate_remote_phone_broker_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_RIGASATIKSME_QR == 1 )); then
    validate_remote_rigassatiksme_qr_bot_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_TICKET_REMOTE == 1 )); then
    validate_remote_ticket_remote_workload_health "${remote_release_dir}"
  fi
  if (( VALIDATE_DNS == 1 )); then
    validate_remote_dns_workload_health "${remote_release_dir}"
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

  validate_remote_probe "${remote_release_dir}" \
    "release bundle exists" \
    "[[ -f '${remote_release_dir}/release.env' ]]" \
    "${diagnostics_services[@]}"

  if (( TARGETED_MODE == 1 )); then
    validate_remote_selected_workload_health "${remote_release_dir}"
    if (( VALIDATE_DNS == 1 )); then
      validate_public_dns_access "${remote_release_dir}"
      validate_private_dns_admin_access "${remote_release_dir}"
    fi
    validate_remote_swarm_baseline "${remote_release_dir}"
    if (( VALIDATE_PORTAINER == 1 )); then
      validate_remote_portainer_state "${remote_release_dir}"
    fi
    return_remote_validation_status
    return
  fi

  validate_remote_workload_health "${remote_release_dir}"
  validate_public_dns_access "${remote_release_dir}"
  validate_private_dns_admin_access "${remote_release_dir}"
  validate_remote_host_baseline "${remote_release_dir}"
  return_remote_validation_status
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
    portainer train_bot satiksme_bot subscription_bot ticket_phone_bridge phone_broker rigassatiksme_qr_bot ticket_remote train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel dns_controlplane
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
    portainer train_bot satiksme_bot subscription_bot ticket_phone_bridge phone_broker rigassatiksme_qr_bot ticket_remote train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel dns_controlplane

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
  publish_remote_dns_admin_tailscale
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
  local rollback_non_dns_service_args=""
  local rollback_tunnel_service_args=""
  local rollback_dns_in_scope=1
  if (( TARGETED_MODE == 1 )); then
    rollback_non_dns_service_args="$(compose_target_service_args_without_dns)"
    rollback_tunnel_service_args="$(compose_target_tunnel_service_args)"
    rollback_dns_in_scope=0
    if targeted_service_selected dns_controlplane; then
      rollback_dns_in_scope=1
    fi
  else
    rollback_non_dns_service_args="$(compose_all_non_dns_service_args)"
  fi
  if (( rollback_dns_in_scope == 1 )); then
    ensure_remote_dns_host_preflight
  fi
  remote_shell "
    ROLLBACK_DNS_IN_SCOPE='${rollback_dns_in_scope}'
    [[ -f '${remote_release_dir}/release.env' ]] || { echo 'missing release bundle: ${remote_release_dir}' >&2; exit 1; }
    cd '${remote_release_dir}'
    if [[ \"\${ROLLBACK_DNS_IN_SCOPE}\" == '1' ]]; then
      docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' build dns_controlplane
      docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns migrate --json </dev/null
      docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json </dev/null
    fi
    if [[ \"\${ROLLBACK_DNS_IN_SCOPE}\" == '1' && -f '${REMOTE_CURRENT_LINK}/release.env' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' stop dns_controlplane frontend adguardhome >/dev/null 2>&1 || true
    fi
    ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    if [[ '${TARGETED_MODE}' == '1' ]]; then
      if [[ -n '${rollback_non_dns_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --build --force-recreate --no-deps${rollback_non_dns_service_args}
      fi
      if [[ -n '${rollback_tunnel_service_args}' ]]; then
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${rollback_tunnel_service_args}
      fi
    else
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --remove-orphans${rollback_non_dns_service_args}
    fi
    if [[ \"\${ROLLBACK_DNS_IN_SCOPE}\" == '1' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps dns_controlplane
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
  python3 "${args[@]}" "$@"
}

run_host_mirror_push() {
  local changed_paths_file="$1"
  local args=()
  args=("${HOST_MIRROR_SCRIPT}" push --profile arbuzas --mirror-root "${HOST_MIRROR_ROOT}" --ssh-target "$(remote_target)" --changed-paths-file "${changed_paths_file}")
  if [[ -n "${ARBUZAS_SSH_PORT}" ]]; then
    args+=(--ssh-port "${ARBUZAS_SSH_PORT}")
  fi
  python3 "${args[@]}"
}

host_mirror_affected_services() {
  local changed_paths_file="$1"
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
  local -a non_dns_services=()
  local service_name=""
  local non_dns_args=""
  local has_dns=0
  changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
  trap 'rm -f "${changed_paths_file}"' RETURN

  run_host_mirror_push "${changed_paths_file}"
  affected_output="$(host_mirror_affected_services "${changed_paths_file}")"
  if [[ -z "${affected_output}" ]]; then
    log "Deploy config: mirror is already in sync; no services need restart"
    return 0
  fi

  while IFS= read -r service_name; do
    [[ -n "${service_name}" ]] || continue
    affected_services+=("${service_name}")
    if [[ "${service_name}" == "dns_controlplane" ]]; then
      has_dns=1
    else
      non_dns_services+=("${service_name}")
    fi
  done <<< "${affected_output}"

  log "Deploy config: affected services $(csv_join_services "${affected_services[@]}")"
  remote_shell "
    [[ -f '${REMOTE_CURRENT_LINK}/release.env' ]] || { echo 'missing active release: ${REMOTE_CURRENT_LINK}/release.env' >&2; exit 1; }
    [[ -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' ]] || { echo 'missing active compose file under ${REMOTE_CURRENT_LINK}' >&2; exit 1; }
  "

  if (( ${#non_dns_services[@]} > 0 )); then
    non_dns_args=""
    for service_name in "${non_dns_services[@]}"; do
      non_dns_args+=" ${service_name}"
    done
    remote_shell "
      cd '${REMOTE_CURRENT_LINK}'
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${non_dns_args}
    "
  fi

  if (( has_dns == 1 )); then
    remote_shell "
      cd '${REMOTE_CURRENT_LINK}'
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json </dev/null
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps dns_controlplane
    "
  fi
}

while (( $# > 0 )); do
  case "$1" in
    deploy|validate|rollback|cleanup-docker|memory-report|install-memory-report|validate-memory-report|compact-dns-db|repair-dns-admin|install-netdata|validate-netdata|install-thinkpad-fan|validate-thinkpad-fan|repair-portainer|mirror-pull|mirror-audit|mirror-push|deploy-config)
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
    if dns_validation_requested || requires_dns_release_prepare; then
      require_dns_private_admin_env
    fi
    ARBUZAS_RELEASE_ID="${requested_release_id:-${ARBUZAS_RELEASE_ID}}"
    ARBUZAS_RELEASE_DIR="${LOCAL_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"
    previous_release_id="$(resolve_remote_current_release_id || true)"
    if (( TARGETED_MODE == 1 )); then
      log "Deploy: targeted services ${COMPOSE_TARGET_SERVICES[*]}"
    fi
    mirror_changed_paths_file="$(mktemp "${TMPDIR:-/tmp}/arbuzas-host-mirror-changed.XXXXXX")"
    trap 'rm -f "${mirror_changed_paths_file}"' EXIT
    run_host_mirror_push "${mirror_changed_paths_file}"
    prepare_local_release_bundle
    prepare_remote_host_layout
    copy_release_to_remote
    render_remote_cloudflared_configs
    remote_compose_up
    if requires_dns_release_prepare; then
      publish_remote_dns_admin_tailscale
    fi
    if validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}" && validate_remote_release "${ARBUZAS_RELEASE_ID}"; then
      cleanup_remote_public_bundle_versions
      run_automatic_remote_docker_gc
      exit 0
    fi
    if [[ -n "${previous_release_id}" && "${previous_release_id}" != "${ARBUZAS_RELEASE_ID}" ]]; then
      log "Deploy validation failed; rolling back to ${previous_release_id}"
      requested_release_id="${previous_release_id}"
      rollback_remote_release
      if requires_dns_release_prepare; then
        publish_remote_dns_admin_tailscale
      fi
      validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${previous_release_id}"
      validate_remote_release "${previous_release_id}"
    fi
    exit 1
    ;;
  validate)
    if dns_validation_requested; then
      require_dns_private_admin_env
    fi
    if (( TARGETED_MODE == 1 )); then
      log "Validate: targeted services ${COMPOSE_TARGET_SERVICES[*]}"
    fi
    validate_remote_release "${requested_release_id}"
    ;;
  rollback)
    if dns_validation_requested || requires_dns_release_prepare; then
      require_dns_private_admin_env
    fi
    rollback_remote_release
    if requires_dns_release_prepare; then
      publish_remote_dns_admin_tailscale
    fi
    validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${requested_release_id}"
    validate_remote_release "${requested_release_id}"
    run_automatic_remote_docker_gc
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
  compact-dns-db)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for compact-dns-db" >&2
      exit 2
    fi
    require_dns_private_admin_env
    log "Maintenance: activating cleanup and compacting the live DNS control-plane database"
    compact_remote_dns_db
    validate_remote_dns_workload_health "${REMOTE_CURRENT_LINK}"
    ;;
  repair-dns-admin)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for repair-dns-admin" >&2
      exit 2
    fi
    require_dns_private_admin_env
    repair_remote_dns_admin
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
    require_dns_private_admin_env
    repair_remote_portainer
    ;;
esac
