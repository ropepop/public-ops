#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DOCKER_ROOT="${REPO_ROOT}/infra/arbuzas/docker"
DOCKER_DEFAULT_ENV_FILE="${DOCKER_ROOT}/env/arbuzas.env"
LOCAL_RELEASES_ROOT="${REPO_ROOT}/output/arbuzas/releases"
REMOTE_RELEASES_ROOT="/etc/arbuzas/releases"
REMOTE_CURRENT_LINK="/etc/arbuzas/current"
REMOTE_PORTAINER_DATA_DIR="/srv/arbuzas/portainer"
REMOTE_PORTAINER_BACKUPS_DIR="/srv/arbuzas/portainer-backups"
PORTAINER_AGENT_ENDPOINT="tcp://tasks.agent:9001"
PORTAINER_LOCAL_ENDPOINT="unix:///var/run/docker.sock"
PORTAINER_DB_TOOL_DIR="${SCRIPT_DIR}/portainerdb"
PORTAINER_TOOLBOX_IMAGE="${PORTAINER_TOOLBOX_IMAGE:-busybox:1.36.1}"
DOCKER_GC_SCRIPT="${SCRIPT_DIR}/docker_gc.py"
DOCKER_GC_REMOTE_STATE_DIR="/etc/arbuzas/docker-gc"
DOCKER_GC_REMOTE_STATE_FILE="${DOCKER_GC_REMOTE_STATE_DIR}/state.json"
DOCKER_GC_BUILD_CACHE_UNTIL="${DOCKER_GC_BUILD_CACHE_UNTIL:-168h}"
DOCKER_GC_RELEASE_KEEP_PER_FAMILY="${DOCKER_GC_RELEASE_KEEP_PER_FAMILY:-10}"
ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS="${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS:-7}"
ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE="${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE:-100M}"
NETDATA_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/netdata"
NETDATA_REMOTE_CONFIG_DIR="/etc/netdata"
NETDATA_REMOTE_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/netdata.conf"
NETDATA_REMOTE_DOCKER_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/go.d/docker.conf"
NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE="${NETDATA_REMOTE_CONFIG_DIR}/go.d/sd/docker.conf"
NETDATA_KICKSTART_URL="${NETDATA_KICKSTART_URL:-https://get.netdata.cloud/kickstart.sh}"
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

ARBUZAS_HOST="${ARBUZAS_HOST:-arbuzas}"
ARBUZAS_USER="${ARBUZAS_USER:-${USER}}"
ARBUZAS_SSH_PORT="${ARBUZAS_SSH_PORT:-}"
ARBUZAS_TZ="${ARBUZAS_TZ:-Europe/Riga}"
ARBUZAS_RELEASE_ID="${ARBUZAS_RELEASE_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
ARBUZAS_RELEASE_DIR="${ARBUZAS_RELEASE_DIR:-${LOCAL_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}}"

ARBUZAS_TRAIN_BOT_PORT="${ARBUZAS_TRAIN_BOT_PORT:-9317}"
ARBUZAS_SATIKSME_BOT_PORT="${ARBUZAS_SATIKSME_BOT_PORT:-9318}"
ARBUZAS_SUBSCRIPTION_BOT_PORT="${ARBUZAS_SUBSCRIPTION_BOT_PORT:-9320}"
ARBUZAS_TICKET_REMOTE_PORT="${ARBUZAS_TICKET_REMOTE_PORT:-9338}"
ARBUZAS_TICKET_PHONE_ADB_TARGET="${ARBUZAS_TICKET_PHONE_ADB_TARGET:-100.76.50.43:5555}"
ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK="${ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK:-}"
ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK_DEFAULT="${REPO_ROOT}/../pixel-phone/orchestrator/android-orchestrator/app/build/outputs/apk/debug/app-debug.apk"
ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK_REMOTE="/srv/arbuzas/android-sim/apks/pixel-orchestrator-debug.apk"
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
  ticket_android_sim
  ticket_android_sim_tuner
  ticket_android_sim_bridge
  ticket_phone_bridge
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

remote_run_host_cache_cleanup() {
  if [[ ! "${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS}" =~ ^[0-9]+$ ]]; then
    echo "ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS must be a non-negative integer" >&2
    return 2
  fi
  if [[ ! "${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE}" =~ ^[0-9]+[KMGTP]?$ ]]; then
    echo "ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE must be a systemd size such as 100M" >&2
    return 2
  fi

  remote_root_command "
    tmp_min_age_days='${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS}'
    journal_max_size='${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE}'
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
  deploy            Prepare a release bundle, copy it to Arbuzas, render tunnel configs, and run docker compose up -d --build
  validate          Validate the active or requested release on Arbuzas
  rollback          Point /etc/arbuzas/current at a previous release and redeploy it
  cleanup-docker    Run the Arbuzas Docker image, release, build-cache, and host-cache cleanup policy on the live host
  compact-dns-db    Run the Arbuzas DNS cleanup activation and compact maintenance flow on the live host
  repair-dns-admin  Clear stale private DNS admin forwards, re-assert the Tailscale TCP forward, refresh the bare private web URL, and print host listener diagnostics
  install-netdata   Install Netdata plus hardware monitoring packages on the live host and publish it privately over Tailscale
  validate-netdata  Validate the live Netdata host install, private Tailscale access, and expected Arbuzas hardware charts
  install-thinkpad-fan   Install the Arbuzas ThinkPad fan controller on the live host
  validate-thinkpad-fan  Validate the live Arbuzas ThinkPad fan controller and current control mode
  repair-portainer  Backup and repair Portainer state in place, disable Docker Swarm, and restart Portainer on the standalone Docker socket

Options:
  --release-id VALUE
  --services NAME[,NAME...]
  --ssh-host HOST
  --ssh-user USER
  --ssh-port PORT
  --env-file PATH

Services:
  portainer, train_bot, train_tunnel, satiksme_bot, satiksme_tunnel,
  subscription_bot, subscription_tunnel, ticket_android_sim, ticket_android_sim_bridge,
  ticket_android_sim_tuner, ticket_phone_bridge, ticket_remote, ticket_remote_tunnel,
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
    ticket_remote)
      VALIDATE_TICKET_REMOTE=1
      append_unique DIAGNOSTIC_SERVICES ticket_android_sim
      append_unique DIAGNOSTIC_SERVICES ticket_android_sim_tuner
      append_unique DIAGNOSTIC_SERVICES ticket_android_sim_bridge
      append_unique DIAGNOSTIC_SERVICES ticket_phone_bridge
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
        mark_validation_group ticket_remote
        ;;
      ticket_android_sim)
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_tuner
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_bridge
        mark_validation_group ticket_remote
        ;;
      ticket_android_sim_tuner)
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_tuner
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_bridge
        mark_validation_group ticket_remote
        ;;
      ticket_android_sim_bridge)
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_tuner
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_bridge
        mark_validation_group ticket_remote
        ;;
      ticket_remote)
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_tuner
        append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_bridge
        append_unique COMPOSE_TARGET_SERVICES ticket_phone_bridge
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
    ticket_android_sim
    ticket_android_sim_tuner
    ticket_android_sim_bridge
    ticket_phone_bridge
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

validate_remote_probe() {
  local probe_release_dir="$1"
  local label="$2"
  local script="$3"
  shift 3
  local services=("$@")

  log "Validate: ${label}"
  if ! remote_compose_shell "${probe_release_dir}" "${script}"; then
    log "Validation failed: ${label}"
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
  if ! is_private_ipv4 "${ARBUZAS_DNS_ADMIN_LAN_IP}"; then
    echo "ARBUZAS_DNS_ADMIN_LAN_IP must be a private RFC1918 IPv4 address (got: ${ARBUZAS_DNS_ADMIN_LAN_IP})" >&2
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
    print('DNS host preflight failed on Arbuzas; fix the listener conflict before retrying.', file=sys.stderr)
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

  log "Validate: dns private admin login on Arbuzas loopback"
  if ! remote_shell "curl -fsS 'http://127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login' >/dev/null 2>/dev/null"; then
    log "Validation failed: dns private admin login on Arbuzas loopback"
    collect_remote_dns_host_diagnostics
    collect_remote_validation_diagnostics "${diagnostics_release_dir}" dns_controlplane
    return 1
  fi

  log "Validate: dns private admin login on Arbuzas LAN address"
  if ! remote_shell "curl -fsS 'http://${ARBUZAS_DNS_ADMIN_LAN_IP}:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login' >/dev/null 2>/dev/null"; then
    log "Validation failed: dns private admin login on Arbuzas LAN address"
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

  log "Validate: dns private admin root on Arbuzas nginx"
  if ! remote_shell "curl -fsS -H 'Host: ${tailnet_dns_name}' 'http://127.0.0.1/' >/dev/null 2>/dev/null"; then
    log "Validation failed: dns private admin root on Arbuzas nginx"
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

  log "Validate: Netdata stays unclaimed on Arbuzas"
  remote_root_command "
    [[ ! -f /var/lib/netdata/cloud.d/claim.conf ]]
  "

  log "Validate: Netdata keeps Docker polling disabled on Arbuzas"
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
      --exclude="${path}/target" \
      --exclude="${path}/tmp" \
      -cf - "${path}"
  ) | (
    cd "${ARBUZAS_RELEASE_DIR}"
    tar -xf -
  )
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
  copy_tree_into_release "workloads/ticket-remote"

  mkdir -p "${ARBUZAS_RELEASE_DIR}/tools/arbuzas"
  cp "${REPO_ROOT}/tools/arbuzas/render_cloudflared_config.py" "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/render_cloudflared_config.py"
  if [[ -f "${REPO_ROOT}/tools/arbuzas/docker_gc.py" ]]; then
    cp "${REPO_ROOT}/tools/arbuzas/docker_gc.py" "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/docker_gc.py"
  fi

  cat > "${ARBUZAS_RELEASE_DIR}/release.env" <<EOF
ARBUZAS_RELEASE_ID=${ARBUZAS_RELEASE_ID}
ARBUZAS_TZ=${ARBUZAS_TZ}
ARBUZAS_TRAIN_BOT_PORT=${ARBUZAS_TRAIN_BOT_PORT}
ARBUZAS_SATIKSME_BOT_PORT=${ARBUZAS_SATIKSME_BOT_PORT}
ARBUZAS_SUBSCRIPTION_BOT_PORT=${ARBUZAS_SUBSCRIPTION_BOT_PORT}
ARBUZAS_TICKET_REMOTE_PORT=${ARBUZAS_TICKET_REMOTE_PORT}
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
      '/srv/arbuzas/android-sim/google-apis/avd' \
      '/srv/arbuzas/android-sim/apks' \
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
        docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --no-deps${non_dns_service_args}
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
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --remove-orphans${all_non_dns_service_args}
    if [[ -n '${tunnel_service_args}' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps${tunnel_service_args}
    fi
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps dns_controlplane
  "
}

prepare_remote_ticket_android_sim_active_backend() {
  remote_root_command "
    mkdir -p /srv/arbuzas/ticket-remote/state
    active_backend_file=/srv/arbuzas/ticket-remote/state/active-phone-backend.json
    if [[ -s \"\${active_backend_file}\" ]] &&
      grep -Eq '\"backendId\"[[:space:]]*:[[:space:]]*\"(android-sim|pixel)\"' \"\${active_backend_file}\"; then
      echo \"ticket_android_sim_active_backend result=preserved path=\${active_backend_file}\"
    else
      printf '{\n  \"backendId\": \"android-sim\",\n  \"updatedAt\": \"%s\"\n}\n' \"\$(date -u +%Y-%m-%dT%H:%M:%SZ)\" >\"\${active_backend_file}\"
      echo \"ticket_android_sim_active_backend result=defaulted backend=android-sim path=\${active_backend_file}\"
    fi
  "
}

resolve_ticket_android_sim_phone_apk() {
  if [[ -n "${ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK}" ]]; then
    printf '%s\n' "${ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK}"
    return 0
  fi
  if [[ -f "${ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK_DEFAULT}" ]]; then
    printf '%s\n' "${ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK_DEFAULT}"
    return 0
  fi
  return 1
}

upload_remote_ticket_android_sim_phone_apk() {
  local local_apk=""
  local remote_tmp="/tmp/ticket-android-sim-phone-service-${ARBUZAS_RELEASE_ID}.apk"
  local remote_tmp_q=""
  local remote_apk_q=""

  if ! local_apk="$(resolve_ticket_android_sim_phone_apk)"; then
    log "Deploy: no local ticket phone service APK found for simulator; using remote cache if present"
    return 0
  fi
  if [[ ! -s "${local_apk}" ]]; then
    echo "Ticket Android simulator phone APK is empty: ${local_apk}" >&2
    return 1
  fi

  log "Deploy: uploading ticket phone service APK for simulator"
  upload_remote_file "${local_apk}" "${remote_tmp}"
  remote_tmp_q="$(shell_quote "${remote_tmp}")"
  remote_apk_q="$(shell_quote "${ARBUZAS_TICKET_ANDROID_SIM_PHONE_APK_REMOTE}")"
  remote_root_command "
    mkdir -p \"\$(dirname -- ${remote_apk_q})\"
    mv -f -- ${remote_tmp_q} ${remote_apk_q}
    chmod 0644 ${remote_apk_q}
  "
}

wait_for_remote_ticket_android_sim_tuning() {
  local remote_release_dir="$1"

  log "Deploy: waiting for Android simulator tuning loop"
  remote_compose_shell "${remote_release_dir}" "
    wait_until_ok() {
      local deadline
      deadline=\$((\$(date +%s) + 420))
      while :; do
        if \"\$@\"; then
          return 0
        fi
        if [[ \$(date +%s) -ge \${deadline} ]]; then
          return 1
        fi
        sleep 5
      done
    }
    sim_tuned_current_boot_ok() {
      status=/srv/arbuzas/android-sim/status/tuning-status.env
      [[ -s \"\${status}\" ]] || return 1
      grep -F 'result=ok' \"\${status}\" >/dev/null || return 1
      grep -F 'swap_total_kb=0' \"\${status}\" >/dev/null || return 1
      boot_id=\$(compose exec -T ticket_android_sim_bridge sh -lc 'adb connect ticket_android_sim:5555 >/dev/null 2>&1 || true; adb -s ticket_android_sim:5555 shell cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d \"\\r\"' 2>/dev/null || true)
      [[ -n \"\${boot_id}\" ]] || return 1
      grep -F \"boot_id=\${boot_id}\" \"\${status}\" >/dev/null
    }
    wait_until_ok sim_tuned_current_boot_ok
  "
}

setup_remote_ticket_android_sim() {
  local remote_release_dir="$1"
  local script

  log "Deploy: preparing persistent Android simulator device"
  read -r -d '' script <<'REMOTE' || true
    mkdir -p /srv/arbuzas/android-sim/apks
    download_if_missing_or_stale() {
      label="$1"
      url="$2"
      apk="$3"
      if [[ -s "${apk}" ]] && find "${apk}" -mtime -7 -print -quit 2>/dev/null | grep -q .; then
        echo "store_apk_cache label=${label} result=hit path=${apk}"
        return 0
      fi
      echo "store_apk_cache label=${label} result=refresh path=${apk}"
      tmp="${apk}.tmp"
      curl -fL --retry 3 -o "${tmp}" "${url}"
      mv "${tmp}" "${apk}"
    }
    download_if_missing_or_stale Accrescent 'https://accrescent.app/accrescent.apk' '/srv/arbuzas/android-sim/apks/accrescent.apk'
    download_if_missing_or_stale Aurora 'https://f-droid.org/repo/com.aurora.store_71.apk' '/srv/arbuzas/android-sim/apks/aurora-store.apk'
    cat > /srv/arbuzas/android-sim/restore-aggressive-packages.sh <<'RESTORE'
#!/usr/bin/env bash
set -euo pipefail
container="${1:-arbuzas-ticket_android_sim_bridge-1}"
adb_target="${2:-ticket_android_sim:5555}"
docker exec "${container}" sh -c 'cat > /tmp/restore-aggressive-packages-inner.sh && sh /tmp/restore-aggressive-packages-inner.sh "$@"' sh "${adb_target}" <<'BRIDGE'
set -eu
adb_target="$1"
adb connect "${adb_target}" >/dev/null 2>&1 || true
packages='
com.google.android.gm
com.google.android.apps.maps
com.google.android.apps.photos
com.google.android.apps.youtube.music
com.google.android.apps.docs
com.google.android.googlequicksearchbox
com.google.android.apps.wellbeing
com.google.android.apps.wallpaper
com.google.android.apps.wallpaper.nexus
com.google.android.apps.customization.pixel
com.google.android.feedback
com.google.android.apps.restore
com.google.android.onetimeinitializer
com.google.android.partnersetup
com.google.android.projection.gearhead
com.google.android.tts
com.google.android.dialer
com.google.android.contacts
com.google.android.calendar
com.google.android.apps.messaging
com.google.android.deskclock
com.google.android.soundpicker
com.google.android.cellbroadcastreceiver
com.google.android.cellbroadcastservice
com.google.android.tag
com.google.android.printservice.recommendation
'
for package in ${packages}; do
  if adb -s "${adb_target}" shell pm path "${package}" >/dev/null 2>&1; then
    adb -s "${adb_target}" shell su 0 cmd package enable --user 0 "${package}" >/dev/null 2>&1 || true
    echo "package_restore package=${package}"
  fi
done
BRIDGE
RESTORE
    chmod 0755 /srv/arbuzas/android-sim/restore-aggressive-packages.sh

    compose exec -T ticket_android_sim_bridge sh -c 'cat > /tmp/ticket-android-sim-setup.sh && sh /tmp/ticket-android-sim-setup.sh' <<'BRIDGE'
set -eu
adb_target='ticket_android_sim:5555'
adb connect "${adb_target}" >/dev/null 2>&1 || true
adb -s "${adb_target}" wait-for-device

deadline=$(( $(date +%s) + 420 ))
while :; do
  booted="$(adb -s "${adb_target}" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r' || true)"
  if [ "${booted}" = "1" ]; then
    break
  fi
  if [ "$(date +%s)" -ge "${deadline}" ]; then
    echo 'Android simulator boot did not complete' >&2
    exit 1
  fi
  sleep 5
done

deadline=$(( $(date +%s) + 180 ))
while :; do
  if adb -s "${adb_target}" shell cmd package list packages android >/dev/null 2>&1; then
    out="$(adb -s "${adb_target}" shell cmd package install-create -r -S 1 2>&1 | tr -d '\r' || true)"
    session="$(printf '%s\n' "${out}" | sed -n 's/.*\[\([0-9][0-9]*\)\].*/\1/p')"
    if printf '%s\n' "${out}" | grep -F 'Success:' >/dev/null 2>&1; then
      [ -n "${session}" ] && adb -s "${adb_target}" shell cmd package install-abandon "${session}" >/dev/null 2>&1 || true
      break
    fi
  fi
  if [ "$(date +%s)" -ge "${deadline}" ]; then
    echo 'Android simulator installer never became ready' >&2
    exit 1
  fi
  sleep 5
done

disable_android_swap() {
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    adb -s "${adb_target}" shell su 0 'swapoff /dev/block/zram0 2>/dev/null || swapoff -a 2>/dev/null || true; [ -e /sys/block/zram0/reset ] && echo 1 > /sys/block/zram0/reset 2>/dev/null || true; [ -e /sys/block/zram0/disksize ] && echo 0 > /sys/block/zram0/disksize 2>/dev/null || true' >/dev/null 2>&1 || true
    swap_total="$(adb -s "${adb_target}" shell su 0 cat /proc/meminfo 2>/dev/null | tr -d '\r' | awk '/^SwapTotal:/ {print $2; exit}')"
    if [ "${swap_total:-}" = "0" ]; then
      echo "android_swap result=disabled swap_total_kb=${swap_total}"
      return 0
    fi
    sleep 3
  done
  echo "Android simulator swap disable failed: SwapTotal=${swap_total:-unknown} kB" >&2
  exit 1
}

tune_android_display_and_background() {
  for attempt in 1 2 3 4 5 6 7 8; do
    adb -s "${adb_target}" shell wm size 540x960 >/dev/null 2>&1 || true
    adb -s "${adb_target}" shell wm density 220 >/dev/null 2>&1 || true
    size="$(adb -s "${adb_target}" shell wm size 2>/dev/null | tr -d '\r' || true)"
    density="$(adb -s "${adb_target}" shell wm density 2>/dev/null | tr -d '\r' || true)"
    if printf '%s\n' "${size}" | grep -F '540x960' >/dev/null 2>&1 &&
      printf '%s\n' "${density}" | grep -F '220' >/dev/null 2>&1; then
      break
    fi
    sleep 5
  done
  adb -s "${adb_target}" shell settings put global window_animation_scale 0 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global transition_animation_scale 0 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global animator_duration_scale 0 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global background_process_limit 2 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global app_process_limit 2 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global cached_apps_freezer enabled >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global wifi_scan_always_enabled 0 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell settings put global ble_scan_always_enabled 0 >/dev/null 2>&1 || true
  echo 'avd_optimization display=540x960 density=220 background_process_limit=2 cached_apps_freezer=enabled scans=disabled'
}

disable_nonessential_package() {
  package="$1"
  if ! adb -s "${adb_target}" shell pm path "${package}" >/dev/null 2>&1; then
    return 0
  fi
  if adb -s "${adb_target}" shell su 0 cmd package disable-user --user 0 "${package}" >/dev/null 2>&1; then
    echo "package_tune package=${package} result=disabled-user"
  else
    adb -s "${adb_target}" shell am force-stop "${package}" >/dev/null 2>&1 || true
    echo "package_tune package=${package} result=force-stopped"
  fi
}

disable_nonessential_packages() {
  packages='
com.google.android.gm
com.google.android.apps.maps
com.google.android.apps.photos
com.google.android.apps.youtube.music
com.google.android.apps.docs
com.google.android.googlequicksearchbox
com.google.android.apps.wellbeing
com.google.android.apps.wallpaper
com.google.android.apps.wallpaper.nexus
com.google.android.apps.customization.pixel
com.google.android.feedback
com.google.android.apps.restore
com.google.android.onetimeinitializer
com.google.android.partnersetup
com.google.android.projection.gearhead
com.google.android.tts
com.google.android.dialer
com.google.android.contacts
com.google.android.calendar
com.google.android.apps.messaging
com.google.android.deskclock
com.google.android.soundpicker
com.google.android.cellbroadcastreceiver
com.google.android.cellbroadcastservice
com.google.android.tag
com.google.android.printservice.recommendation
'
  for package in ${packages}; do
    disable_nonessential_package "${package}"
  done
}

disable_android_swap
tune_android_display_and_background
disable_nonessential_packages
disable_android_swap

for attempt in 1 2 3 4 5 6 7 8; do
  adb -s "${adb_target}" shell wm size 540x960 >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell wm density 220 >/dev/null 2>&1 || true
  size="$(adb -s "${adb_target}" shell wm size 2>/dev/null | tr -d '\r' || true)"
  density="$(adb -s "${adb_target}" shell wm density 2>/dev/null | tr -d '\r' || true)"
  if printf '%s\n' "${size}" | grep -F '540x960' >/dev/null 2>&1 &&
    printf '%s\n' "${density}" | grep -F '220' >/dev/null 2>&1; then
    break
  fi
  sleep 5
done
adb -s "${adb_target}" shell settings put global window_animation_scale 0 >/dev/null 2>&1 || true
adb -s "${adb_target}" shell settings put global transition_animation_scale 0 >/dev/null 2>&1 || true
adb -s "${adb_target}" shell settings put global animator_duration_scale 0 >/dev/null 2>&1 || true
adb -s "${adb_target}" shell wm size 2>/dev/null | tr -d '\r' || true
adb -s "${adb_target}" shell wm density 2>/dev/null | tr -d '\r' || true

install_if_missing() {
  label="$1"
  package="$2"
  apk="$3"
  if adb -s "${adb_target}" shell pm path "${package}" >/dev/null 2>&1; then
    echo "store_client label=${label} package=${package} result=already-installed"
    return 0
  fi
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if adb -s "${adb_target}" install -r "${apk}"; then
      echo "store_client label=${label} package=${package} result=installed"
      return 0
    fi
    if [ "${attempt}" = "12" ]; then
      return 1
    fi
    sleep 10
  done
}
install_or_update() {
  label="$1"
  package="$2"
  apk="$3"
  install_log=/tmp/ticket-android-sim-install.log
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if adb -s "${adb_target}" install -r "${apk}" >"${install_log}" 2>&1; then
      echo "store_client label=${label} package=${package} result=updated"
      return 0
    fi
    cat "${install_log}" >&2 || true
    if grep -F 'INSTALL_FAILED_UPDATE_INCOMPATIBLE' "${install_log}" >/dev/null 2>&1; then
      echo "store_client label=${label} package=${package} result=signature-mismatch-uninstall"
      adb -s "${adb_target}" uninstall "${package}" >/dev/null 2>&1 || true
      if adb -s "${adb_target}" install -r "${apk}" >"${install_log}" 2>&1; then
        echo "store_client label=${label} package=${package} result=reinstalled"
        return 0
      fi
      cat "${install_log}" >&2 || true
      return 1
    fi
    if [ "${attempt}" = "12" ]; then
      return 1
    fi
    sleep 10
  done
}

install_if_missing Accrescent app.accrescent.client /srv/android-sim/apks/accrescent.apk
install_if_missing Aurora com.aurora.store /srv/android-sim/apks/aurora-store.apk

phone_service_apk=/srv/android-sim/apks/pixel-orchestrator-debug.apk
if [ -s "${phone_service_apk}" ]; then
  if ! install_or_update TicketPhoneService lv.jolkins.pixelorchestrator "${phone_service_apk}"; then
    echo "ticket_phone_service package=lv.jolkins.pixelorchestrator result=install-failed"
  fi
  adb -s "${adb_target}" shell pm grant lv.jolkins.pixelorchestrator android.permission.POST_NOTIFICATIONS >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell pm grant lv.jolkins.pixelorchestrator android.permission.WRITE_SECURE_SETTINGS >/dev/null 2>&1 || true
  adb -s "${adb_target}" shell am start -n lv.jolkins.pixelorchestrator/.app.MainActivity >/dev/null 2>&1 || true
  sleep 4
  adb -s "${adb_target}" shell am broadcast \
    -n lv.jolkins.pixelorchestrator/.app.OrchestratorActionReceiver \
    --es orchestrator_action ticket_start_server >/dev/null 2>&1 || true
  echo "ticket_phone_service package=lv.jolkins.pixelorchestrator result=start-requested"
else
  echo "ticket_phone_service package=lv.jolkins.pixelorchestrator result=missing-apk"
fi
disable_android_swap
BRIDGE
REMOTE

  remote_compose_shell "${remote_release_dir}" "${script}"
  wait_for_remote_ticket_android_sim_tuning "${remote_release_dir}"
}

validate_remote_dns_querylog_flow() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "dns local encrypted queries and query logging on Arbuzas" \
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

  validate_remote_probe "${remote_release_dir}" "dns native stats and clients APIs on Arbuzas" \
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
import urllib.error
import urllib.request

root = 'https://${ARBUZAS_TRAIN_BOT_HOSTNAME}'

def request(path, method='GET', body=None):
    data = None if body is None else body.encode('utf-8')
    req = urllib.request.Request(root + path, method=method, data=data, headers={'User-Agent': 'curl/8.0'})
    if body is not None:
        req.add_header('Content-Type', 'application/json')
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.status, {k.lower(): v for k, v in response.headers.items()}, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        return error.code, {k.lower(): v for k, v in error.headers.items()}, error.read().decode('utf-8', 'replace')

status, headers, body = request('/')
if status != 200:
    raise SystemExit(f'root status {status}')
for header in [
    'strict-transport-security',
    'content-security-policy',
    'x-frame-options',
    'x-content-type-options',
    'referrer-policy',
    'permissions-policy',
]:
    if not headers.get(header):
        raise SystemExit(f'missing security header {header}')
for header in headers:
    if header.startswith('x-train-bot-'):
        raise SystemExit(f'public debug header leaked: {header}')

status, _, health_body = request('/api/v1/health')
if status != 200:
    raise SystemExit(f'health status {status}')
health = json.loads(health_body)
if set(health) != {'ok'} or health.get('ok') is not True:
    raise SystemExit(f'health payload is not minimal: {health}')

for path in ['/assets/app.test.js', '/assets/app.js.map', '/assets/live-client.test.js', '/assets/live-client.js']:
    status, _, _ = request(path)
    if status == 200:
        raise SystemExit(f'test-only or unused asset is public: {path}')

status, _, app_js = request('/assets/app.js')
if status != 200:
    raise SystemExit(f'app.js status {status}')
for needle in ['test_ticket', '/auth/test', 'stripTestTicketFromLocation']:
    if needle in app_js:
        raise SystemExit(f'production bundle exposes test login string: {needle}')

for path in ['/assets/%2e%2e/app.js', '/assets//app.js']:
    status, _, _ = request(path)
    if status != 400:
        raise SystemExit(f'unsafe path {path} returned {status}, want 400')

for path in ['/', '/assets/app.js']:
    status, _, _ = request(path, method='POST', body='')
    if status != 405:
        raise SystemExit(f'POST {path} returned {status}, want 405')

for method in ['GET', 'HEAD', 'OPTIONS', 'POST']:
    status, headers, _ = request('/api/v1/auth/test', method=method, body='' if method == 'POST' else None)
    if status != 404:
        raise SystemExit(f'{method} /api/v1/auth/test returned {status}, want 404')
    if headers.get('set-cookie'):
        raise SystemExit(f'{method} /api/v1/auth/test set a cookie')
PY
wait_until_ok python3 \"\${tmp}\"" \
    train_bot train_tunnel
}

validate_remote_train_anonymous_data_denial() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "train anonymous direct data access is denied" \
    "html_tmp=\$(mktemp)
tmp=\$(mktemp)
trap 'rm -f \"\${html_tmp}\" \"\${tmp}\"' EXIT
wait_until_ok compose exec -T train_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TRAIN_BOT_PORT}/' > \"\${html_tmp}\"
cat > \"\${tmp}\" <<'PY'
import json
import os
import re
import urllib.error
import urllib.parse
import urllib.request

with open(os.environ['TRAIN_PAGE_HTML_FILE'], 'r', encoding='utf-8') as handle:
    html = handle.read()

host_match = re.search(r'spacetimeHost:\\s*\"([^\"]+)\"', html)
db_match = re.search(r'spacetimeDatabase:\\s*\"([^\"]+)\"', html)
if not host_match or not db_match:
    raise SystemExit('public page did not expose spacetime host/database config')
spacetime_host = host_match.group(1).rstrip('/')
database = urllib.parse.quote(db_match.group(1), safe='')

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

for name, args in [
    ('trainbot_bootstrap_me', []),
    ('trainbot_get_current_ride', []),
    ('trainbot_service_get_schedule', ['1970-01-01']),
    ('trainbot_service_list_activities', ['', '', '', '']),
]:
    status, body = call(name, args)
    if 200 <= status < 300:
        raise SystemExit(f'anonymous call unexpectedly succeeded: {name} {status} {body[:200]}')
PY
wait_until_ok env TRAIN_PAGE_HTML_FILE=\"\${html_tmp}\" python3 \"\${tmp}\"" \
    train_bot train_tunnel
}

validate_remote_satiksme_public_hardening() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "satiksme public web hardening" \
    "tmp=\$(mktemp)
trap 'rm -f \"\${tmp}\"' EXIT
cat > \"\${tmp}\" <<'PY'
import json
import urllib.error
import urllib.request

root = 'https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}'

def request(path, method='GET', body=None):
    data = None if body is None else body.encode('utf-8')
    req = urllib.request.Request(root + path, method=method, data=data, headers={'User-Agent': 'curl/8.0'})
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.status, {k.lower(): v for k, v in response.headers.items()}, response.read().decode('utf-8', 'replace')
    except urllib.error.HTTPError as error:
        return error.code, {k.lower(): v for k, v in error.headers.items()}, error.read().decode('utf-8', 'replace')

status, headers, _ = request('/')
if status != 200:
    raise SystemExit(f'root status {status}')
for header in [
    'strict-transport-security',
    'content-security-policy',
    'x-frame-options',
    'x-content-type-options',
    'referrer-policy',
    'permissions-policy',
]:
    if not headers.get(header):
        raise SystemExit(f'missing security header {header}')
for header in headers:
    if header.startswith('x-satiksme-bot-'):
        raise SystemExit(f'public debug header leaked: {header}')

status, _, health_body = request('/api/v1/health')
if status != 200:
    raise SystemExit(f'health status {status}')
health = json.loads(health_body)
for forbidden in ['runtime', 'assets', 'catalog', 'telegram', 'reportDump', 'db', 'web', 'bundle', 'liveSnapshot', 'version']:
    if forbidden in health:
        raise SystemExit(f'health payload leaks diagnostics: {forbidden}')
if 'ok' not in health:
    raise SystemExit(f'health payload missing ok: {health}')

for path in ['/assets/app.test.js', '/assets/app.js.map']:
    status, _, _ = request(path)
    if status == 200:
        raise SystemExit(f'test-only asset is public: {path}')

for path in ['/assets/%2e%2e/app.js', '/assets//app.js']:
    status, _, _ = request(path)
    if status != 400:
        raise SystemExit(f'unsafe path {path} returned {status}, want 400')

for path in ['/', '/assets/app.js']:
    status, _, _ = request(path, method='POST', body='')
    if status != 405:
        raise SystemExit(f'POST {path} returned {status}, want 405')
PY
wait_until_ok python3 \"\${tmp}\"" \
    satiksme_bot satiksme_tunnel
}

validate_remote_public_tls_dns_hardening() {
  local remote_release_dir="$1"

  validate_remote_probe "${remote_release_dir}" "public TLS and DNS hardening" \
    "wait_until_ok sh -lc '
      set -e
      for host in \"${ARBUZAS_TRAIN_BOT_HOSTNAME}\" \"${ARBUZAS_SATIKSME_BOT_HOSTNAME}\"; do
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

validate_remote_satiksme_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme local health" \
    "wait_until_ok compose exec -T satiksme_bot sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_SATIKSME_BOT_PORT}/api/v1/health >/dev/null 2>/dev/null'" \
    satiksme_bot satiksme_tunnel
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
    "wait_until_ok sh -lc 'root=https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}; tmp=\$(mktemp -d); trap \"rm -rf \\\"\${tmp}\\\"\" EXIT; curl -fsS -D \"\${tmp}/root.headers\" -o \"\${tmp}/root.html\" \"\${root}/\" && grep -Fi \"strict-transport-security: max-age=300\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"x-frame-options: DENY\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"x-content-type-options: nosniff\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"referrer-policy: strict-origin-when-cross-origin\" \"\${tmp}/root.headers\" >/dev/null && grep -Fi \"content-security-policy:\" \"\${tmp}/root.headers\" >/dev/null && ! grep -Fi \"x-satiksme-bot-\" \"\${tmp}/root.headers\" >/dev/null && grep -F \"/assets/leaflet/leaflet.js\" \"\${tmp}/root.html\" >/dev/null && ! grep -F \"unpkg.com/leaflet\" \"\${tmp}/root.html\" >/dev/null && incidents=\$(curl -fsS \"\${root}/incidents\") && printf %s \"\${incidents}\" | grep -F \"\\\"mode\\\":\\\"public-incidents\\\"\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"unpkg.com/leaflet\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"/assets/leaflet/leaflet.js\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"telegram-login\" >/dev/null && ! printf %s \"\${incidents}\" | grep -F \"telegram-web-app\" >/dev/null'" \
    satiksme_bot satiksme_tunnel
  validate_remote_probe "${remote_release_dir}" "satiksme live snapshots are uncacheable and query-safe" \
    "wait_until_ok sh -lc 'root=https://${ARBUZAS_SATIKSME_BOT_HOSTNAME}; tmp=\$(mktemp -d); trap \"rm -rf \\\"\${tmp}\\\"\" EXIT; curl -fsS -D \"\${tmp}/active.headers\" -o \"\${tmp}/active.json\" \"\${root}/transport/live/active.json\" && grep -Fi \"cache-control: no-store\" \"\${tmp}/active.headers\" >/dev/null && grep -Fi \"x-robots-tag: noindex\" \"\${tmp}/active.headers\" >/dev/null && path=\$(sed -n \"s/.*\\\"path\\\"[[:space:]]*:[[:space:]]*\\\"\\([^\\\"]*\\)\\\".*/\\1/p\" \"\${tmp}/active.json\" | head -1) && test -n \"\${path}\" && case \"\${path}\" in transport/live/*) ;; *) exit 1 ;; esac && curl -fsS -D \"\${tmp}/snapshot.headers\" -o /dev/null \"\${root}/\${path}\" && grep -Fi \"cache-control: no-store\" \"\${tmp}/snapshot.headers\" >/dev/null && grep -Fi \"x-robots-tag: noindex\" \"\${tmp}/snapshot.headers\" >/dev/null && code=\$(curl -sS -o /dev/null -w \"%{http_code}\" \"\${root}/\${path}?cache=split\") && test \"\${code}\" = 404'" \
    satiksme_bot satiksme_tunnel
  validate_remote_satiksme_public_hardening "${remote_release_dir}"
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

validate_remote_ticket_remote_workload_health() {
  local remote_release_dir="$1"

  validate_remote_running_services "${remote_release_dir}" "expected services running" ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge ticket_phone_bridge ticket_remote ticket_remote_tunnel
  validate_remote_probe "${remote_release_dir}" "ticket-remote local health" \
    "wait_until_ok compose exec -T ticket_remote sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null 2>/dev/null'" \
    ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge ticket_phone_bridge ticket_remote ticket_remote_tunnel
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
  validate_remote_probe "${remote_release_dir}" "ticket Android simulator ADB ready" \
    "wait_until_ok compose exec -T ticket_android_sim_bridge sh -lc 'adb connect ticket_android_sim:5555 >/dev/null 2>&1 || true; adb -s ticket_android_sim:5555 get-state >/dev/null 2>/dev/null'" \
    ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge
  validate_remote_probe "${remote_release_dir}" "ticket Android simulator no swap" \
    "wait_until_ok compose exec -T ticket_android_sim_bridge sh -lc 'adb connect ticket_android_sim:5555 >/dev/null 2>&1 || true; swap_total=\$(adb -s ticket_android_sim:5555 shell su 0 cat /proc/meminfo 2>/dev/null | tr -d \"\\r\" | awk \"/^SwapTotal:/ {print \\\$2; exit}\"); test \"\${swap_total}\" = 0'" \
    ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge
  validate_remote_probe "${remote_release_dir}" "ticket Android simulator current boot tuned" \
    "current_boot_tuned_ok() {
      status=/srv/arbuzas/android-sim/status/tuning-status.env
      [[ -s \"\${status}\" ]] || return 1
      grep -F 'result=ok' \"\${status}\" >/dev/null || return 1
      grep -F 'swap_total_kb=0' \"\${status}\" >/dev/null || return 1
      boot_id=\$(compose exec -T ticket_android_sim_bridge sh -lc 'adb connect ticket_android_sim:5555 >/dev/null 2>&1 || true; adb -s ticket_android_sim:5555 shell cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d \"\\r\"' 2>/dev/null || true)
      [[ -n \"\${boot_id}\" ]] || return 1
      grep -F \"boot_id=\${boot_id}\" \"\${status}\" >/dev/null
    }
    wait_until_ok current_boot_tuned_ok" \
    ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge
  validate_remote_probe "${remote_release_dir}" "ticket Android simulator resources" \
    "wait_until_ok sh -lc 'inspect=\$(docker inspect arbuzas-ticket_android_sim-1 --format \"NanoCpus={{.HostConfig.NanoCpus}} Memory={{.HostConfig.Memory}} MemorySwap={{.HostConfig.MemorySwap}}\"); printf %s \"\${inspect}\" | grep -F \"NanoCpus=2000000000\" >/dev/null && printf %s \"\${inspect}\" | grep -F \"Memory=6442450944\" >/dev/null && printf %s \"\${inspect}\" | grep -F \"MemorySwap=6442450944\" >/dev/null'" \
    ticket_android_sim
  validate_remote_probe "${remote_release_dir}" "ticket-remote active configured backend" \
    "active_configured_backend_ok() {
      active=\$(sed -n 's/.*\"backendId\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p' /srv/arbuzas/ticket-remote/state/active-phone-backend.json 2>/dev/null | head -1)
      if [[ -z \"\${active}\" ]]; then
        active=android-sim
      fi
      case \"\${active}\" in
        android-sim|pixel) ;;
        *) return 1 ;;
      esac
      compose exec -T ticket_remote sh -lc 'curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez >/dev/null'
    }
    wait_until_ok active_configured_backend_ok" \
    ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge ticket_remote
  validate_remote_probe "${remote_release_dir}" "ticket-remote public login shell" \
    "wait_until_ok sh -lc 'code=\$(curl -sS -o /dev/null -w \"%{http_code}\" https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/ 2>/dev/null || true); case \"\${code}\" in 200|302) exit 0 ;; *) exit 1 ;; esac'" \
    ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge ticket_phone_bridge ticket_remote ticket_remote_tunnel
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
  validate_remote_probe "${remote_release_dir}" "dns private admin login on Arbuzas" \
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
    return
  fi

  validate_remote_workload_health "${remote_release_dir}"
  validate_public_dns_access "${remote_release_dir}"
  validate_private_dns_admin_access "${remote_release_dir}"
  validate_remote_host_baseline "${remote_release_dir}"
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
    portainer train_bot satiksme_bot subscription_bot ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge ticket_phone_bridge ticket_remote train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel dns_controlplane
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
    portainer train_bot satiksme_bot subscription_bot ticket_android_sim ticket_android_sim_tuner ticket_android_sim_bridge ticket_phone_bridge ticket_remote train_tunnel satiksme_tunnel subscription_tunnel ticket_remote_tunnel dns_controlplane

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
  local all_non_dns_service_args=""
  all_non_dns_service_args="$(compose_all_non_dns_service_args)"
  ensure_remote_dns_host_preflight
  remote_shell "
    [[ -f '${remote_release_dir}/release.env' ]] || { echo 'missing release bundle: ${remote_release_dir}' >&2; exit 1; }
    cd '${remote_release_dir}'
    docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' build dns_controlplane
    docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns migrate --json </dev/null
    docker compose --project-name arbuzas --env-file '${remote_release_dir}/release.env' -f '${remote_release_dir}/infra/arbuzas/docker/compose.yml' run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json </dev/null
    if [[ -f '${REMOTE_CURRENT_LINK}/release.env' ]]; then
      docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' stop dns_controlplane frontend adguardhome >/dev/null 2>&1 || true
    fi
    ln -sfn '${remote_release_dir}' '${REMOTE_CURRENT_LINK}'
    cd '${REMOTE_CURRENT_LINK}'
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --remove-orphans${all_non_dns_service_args}
    docker compose --project-name arbuzas --env-file '${REMOTE_CURRENT_LINK}/release.env' -f '${REMOTE_CURRENT_LINK}/infra/arbuzas/docker/compose.yml' up -d --force-recreate --no-deps dns_controlplane
  "
}

while (( $# > 0 )); do
  case "$1" in
    deploy|validate|rollback|cleanup-docker|compact-dns-db|repair-dns-admin|install-netdata|validate-netdata|install-thinkpad-fan|validate-thinkpad-fan|repair-portainer)
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
    deploy|validate)
      ;;
    *)
      echo "--services is only supported for deploy and validate" >&2
      exit 2
      ;;
  esac
fi

resolve_requested_services

require_cmd ssh
require_cmd scp
require_cmd python3
require_cmd go
require_cmd curl

case "${action}" in
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
    prepare_local_release_bundle
    prepare_remote_host_layout
    copy_release_to_remote
    render_remote_cloudflared_configs
    if targeted_service_selected ticket_android_sim; then
      prepare_remote_ticket_android_sim_active_backend
      upload_remote_ticket_android_sim_phone_apk
    fi
    remote_compose_up
    if targeted_service_selected ticket_android_sim; then
      setup_remote_ticket_android_sim "${REMOTE_CURRENT_LINK}"
    fi
    if requires_dns_release_prepare; then
      publish_remote_dns_admin_tailscale
    fi
    if validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}" && validate_remote_release "${ARBUZAS_RELEASE_ID}"; then
      run_automatic_remote_docker_gc
      exit 0
    fi
    if [[ -n "${previous_release_id}" && "${previous_release_id}" != "${ARBUZAS_RELEASE_ID}" ]]; then
      log "Deploy validation failed; rolling back to ${previous_release_id}"
      requested_release_id="${previous_release_id}"
      rollback_remote_release
      publish_remote_dns_admin_tailscale
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
    require_dns_private_admin_env
    rollback_remote_release
    publish_remote_dns_admin_tailscale
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
  compact-dns-db)
    if [[ -n "${requested_release_id}" ]]; then
      echo "--release-id is not supported for compact-dns-db" >&2
      exit 2
    fi
    require_dns_private_admin_env
    log "Maintenance: activating cleanup and compacting the live Arbuzas DNS control-plane database"
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
