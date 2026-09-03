#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ACTION="${1:-check}"
BASE_DIR="/srv/arbuzas/qbittorrent"
MOUNT_DIR="${BASE_DIR}/storage"
PAYLOAD_DIR="${MOUNT_DIR}/payload"
CONFIG_DIR="${MOUNT_DIR}/config"
QBITTORRENT_CONFIG_DIR="${CONFIG_DIR}/qBittorrent"
INCOMPLETE_DIR="${PAYLOAD_DIR}/.incomplete"
JELLYFIN_IGNORE_FILE="${INCOMPLETE_DIR}/.ignore"
LEGACY_CONFIG_DIR="${BASE_DIR}/config"
LEGACY_DOWNLOADS_DIR="${BASE_DIR}/downloads"
IMAGE_FILE="${BASE_DIR}/downloads.ext4"
MARKER_FILE="${PAYLOAD_DIR}/.arbuzas-qbittorrent-volume"
MARKER_FORMAT="arbuzas-qbittorrent-volume-v1"
IMAGE_SIZE_BYTES=26843545600
FILESYSTEM_LABEL="ARBUZAS_QBT"
UNIT_NAME="srv-arbuzas-qbittorrent-storage.mount"
UNIT_SOURCE="${SCRIPT_DIR}/etc/systemd/system/${UNIT_NAME}"
UNIT_TARGET="/etc/systemd/system/${UNIT_NAME}"
LOOP_MODULE_SOURCE="${SCRIPT_DIR}/../host-security/modules-load/arbuzas-qbittorrent-loop.conf"
LOOP_MODULE_TARGET="/etc/modules-load.d/arbuzas-qbittorrent-loop.conf"
LOOP_ORDERING_SOURCE="${SCRIPT_DIR}/../host-security/systemd/${UNIT_NAME}.d/10-loop-module-ordering.conf"
LOOP_ORDERING_TARGET="/etc/systemd/system/${UNIT_NAME}.d/10-loop-module-ordering.conf"
LOOP_ORDERING_CHANGED=0
QBITTORRENT_UID=""
QBITTORRENT_GID=""
TEMP_IMAGE=""
TRANSACTION_DIR="${BASE_DIR}/.storage-creation"
TRANSACTION_OWNER="${TRANSACTION_DIR}/owner"
TRANSACTION_STATE="${TRANSACTION_DIR}/state"
TRANSACTION_FORMAT="arbuzas-qbittorrent-storage-creation-v1"
LOCK_FILE="/run/lock/arbuzas-qbittorrent-storage.lock"

fail() {
  printf 'qBittorrent storage: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -d "${TRANSACTION_DIR}" && ! -L "${TRANSACTION_DIR}" && -f "${TRANSACTION_OWNER}" && ! -L "${TRANSACTION_OWNER}" ]] \
    && grep -Fx "${TRANSACTION_FORMAT}" "${TRANSACTION_OWNER}" >/dev/null 2>&1; then
    rm -rf -- "${TRANSACTION_DIR}"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: install-storage.sh install --uid UID --gid GID
       install-storage.sh prepare-media --uid UID --gid GID
       install-storage.sh check

The install action creates or validates the dedicated 25 GiB ext4 image,
installs its mount unit, and starts the mount. Existing unknown storage is
never reformatted or adopted.

The prepare-media action only verifies the existing live storage and installs
the Jellyfin ignore marker. It never reloads systemd or changes the mount unit,
so it is safe while qBittorrent is running.
EOF
}

directory_has_entries() {
  [[ -d "$1" ]] && [[ -n "$(find "$1" -mindepth 1 -maxdepth 1 -print -quit)" ]]
}

require_commands() {
  local command_name
  for command_name in blkid cmp fallocate find findmnt flock losetup mkfs.ext4 modprobe stat systemctl tune2fs; do
    command -v "${command_name}" >/dev/null 2>&1 || fail "missing required command: ${command_name}"
  done
}

verify_loop_module_policy() {
  [[ -f "${LOOP_MODULE_SOURCE}" && ! -L "${LOOP_MODULE_SOURCE}" ]] \
    || fail "missing loop module policy source: ${LOOP_MODULE_SOURCE}"
  [[ -f "${LOOP_MODULE_TARGET}" && ! -L "${LOOP_MODULE_TARGET}" ]] \
    || fail "missing installed loop module policy: ${LOOP_MODULE_TARGET}"
  cmp -s "${LOOP_MODULE_SOURCE}" "${LOOP_MODULE_TARGET}" \
    || fail "installed loop module policy differs from the release copy"
  [[ -f "${LOOP_ORDERING_SOURCE}" && ! -L "${LOOP_ORDERING_SOURCE}" ]] \
    || fail "missing loop ordering policy source: ${LOOP_ORDERING_SOURCE}"
  [[ -f "${LOOP_ORDERING_TARGET}" && ! -L "${LOOP_ORDERING_TARGET}" ]] \
    || fail "missing installed loop ordering policy: ${LOOP_ORDERING_TARGET}"
  cmp -s "${LOOP_ORDERING_SOURCE}" "${LOOP_ORDERING_TARGET}" \
    || fail "installed loop ordering policy differs from the release copy"
  [[ -c /dev/loop-control ]] || fail "loop driver is not active: /dev/loop-control is absent"
}

install_loop_module_policy() {
  [[ -f "${LOOP_MODULE_SOURCE}" && ! -L "${LOOP_MODULE_SOURCE}" ]] \
    || fail "missing loop module policy source: ${LOOP_MODULE_SOURCE}"
  if [[ -e "${LOOP_MODULE_TARGET}" || -L "${LOOP_MODULE_TARGET}" ]]; then
    [[ -f "${LOOP_MODULE_TARGET}" && ! -L "${LOOP_MODULE_TARGET}" ]] \
      || fail "refusing unsafe loop module policy path: ${LOOP_MODULE_TARGET}"
  fi
  install -d -m 0755 /etc/modules-load.d
  if [[ ! -f "${LOOP_MODULE_TARGET}" ]] || ! cmp -s "${LOOP_MODULE_SOURCE}" "${LOOP_MODULE_TARGET}"; then
    install -m 0644 "${LOOP_MODULE_SOURCE}" "${LOOP_MODULE_TARGET}"
  fi
  [[ -f "${LOOP_ORDERING_SOURCE}" && ! -L "${LOOP_ORDERING_SOURCE}" ]] \
    || fail "missing loop ordering policy source: ${LOOP_ORDERING_SOURCE}"
  if [[ -e "${LOOP_ORDERING_TARGET}" || -L "${LOOP_ORDERING_TARGET}" ]]; then
    [[ -f "${LOOP_ORDERING_TARGET}" && ! -L "${LOOP_ORDERING_TARGET}" ]] \
      || fail "refusing unsafe loop ordering policy path: ${LOOP_ORDERING_TARGET}"
  fi
  install -d -m 0755 "$(dirname "${LOOP_ORDERING_TARGET}")"
  if [[ ! -f "${LOOP_ORDERING_TARGET}" ]] || ! cmp -s "${LOOP_ORDERING_SOURCE}" "${LOOP_ORDERING_TARGET}"; then
    install -m 0644 "${LOOP_ORDERING_SOURCE}" "${LOOP_ORDERING_TARGET}"
    LOOP_ORDERING_CHANGED=1
  fi
  modprobe loop
  verify_loop_module_policy
}

acquire_lock() {
  install -d -m 0755 /run/lock
  exec 9> "${LOCK_FILE}"
  flock -w 120 9 || fail "timed out waiting for storage lock: ${LOCK_FILE}"
}

parse_args() {
  shift || true
  while (( $# > 0 )); do
    case "$1" in
      --uid)
        shift
        QBITTORRENT_UID="${1:-}"
        ;;
      --gid)
        shift
        QBITTORRENT_GID="${1:-}"
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        fail "unknown argument: $1"
        ;;
    esac
    shift
  done

  case "${ACTION}" in
    install|prepare-media)
      [[ "${QBITTORRENT_UID}" =~ ^[1-9][0-9]*$ ]] || fail "${ACTION} requires a positive --uid"
      [[ "${QBITTORRENT_GID}" =~ ^[1-9][0-9]*$ ]] || fail "${ACTION} requires a positive --gid"
      ;;
    check)
      [[ -z "${QBITTORRENT_UID}${QBITTORRENT_GID}" ]] || fail "check does not accept uid/gid"
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
}

verify_image_path() {
  local image_path="$1"
  [[ -e "${image_path}" ]] || fail "missing image: ${image_path}"
  [[ -f "${image_path}" && ! -L "${image_path}" ]] || fail "image is not a regular non-symlink file: ${image_path}"

  local size_bytes filesystem_type filesystem_label allocated_bytes reserved_blocks
  size_bytes="$(stat -c '%s' "${image_path}")"
  [[ "${size_bytes}" == "${IMAGE_SIZE_BYTES}" ]] || fail "existing image is ${size_bytes} bytes; expected ${IMAGE_SIZE_BYTES}; refusing to resize or reformat it"
  filesystem_type="$(blkid -p -s TYPE -o value "${image_path}" 2>/dev/null || true)"
  filesystem_label="$(blkid -p -s LABEL -o value "${image_path}" 2>/dev/null || true)"
  [[ "${filesystem_type}" == "ext4" ]] || fail "existing image is not ext4; refusing to reformat it"
  [[ "${filesystem_label}" == "${FILESYSTEM_LABEL}" ]] || fail "existing image label is not ${FILESYSTEM_LABEL}; refusing to adopt it"

  allocated_bytes="$(( $(stat -c '%b' "${image_path}") * 512 ))"
  [[ "${allocated_bytes}" -ge "${IMAGE_SIZE_BYTES}" ]] || fail "image is sparse (${allocated_bytes}/${IMAGE_SIZE_BYTES} bytes allocated); refusing an uncapped backing file"
  reserved_blocks="$(tune2fs -l "${image_path}" 2>/dev/null | awk -F: '/^Reserved block count:/ {gsub(/[[:space:]]/, "", $2); print $2}')"
  [[ "${reserved_blocks}" == "0" ]] || fail "ext4 reserved block count is ${reserved_blocks:-unknown}, expected 0"
}

verify_image() {
  verify_image_path "${IMAGE_FILE}"
}

verify_active_mount() {
  findmnt -rn -M "${MOUNT_DIR}" >/dev/null 2>&1 || fail "${MOUNT_DIR} is not mounted"

  local source fstype loop_match mount_options required_option
  source="$(findmnt -rn -M "${MOUNT_DIR}" -o SOURCE)"
  fstype="$(findmnt -rn -M "${MOUNT_DIR}" -o FSTYPE)"
  [[ "${fstype}" == "ext4" ]] || fail "${MOUNT_DIR} has filesystem ${fstype}, expected ext4"
  mount_options=",$(findmnt -rn -M "${MOUNT_DIR}" -o OPTIONS),"
  for required_option in rw nosuid nodev noexec noatime; do
    [[ "${mount_options}" == *",${required_option},"* ]] \
      || fail "${MOUNT_DIR} is missing required mount option: ${required_option}"
  done
  loop_match="$(losetup -j "${IMAGE_FILE}" --noheadings -O NAME 2>/dev/null | awk -v source="${source}" '$1 == source {print $1; exit}')"
  [[ -n "${loop_match}" ]] || fail "${MOUNT_DIR} is not backed by ${IMAGE_FILE}"
  [[ -f "${MARKER_FILE}" && ! -L "${MARKER_FILE}" ]] || fail "mounted filesystem has no valid ${MARKER_FILE}; refusing to adopt it"
  grep -Fx "format=${MARKER_FORMAT}" "${MARKER_FILE}" >/dev/null || fail "mounted filesystem marker format is unknown"
  grep -Fx "size_bytes=${IMAGE_SIZE_BYTES}" "${MARKER_FILE}" >/dev/null || fail "mounted filesystem marker has the wrong size"
}

verify_jellyfin_ignore() {
  [[ -f "${JELLYFIN_IGNORE_FILE}" && ! -L "${JELLYFIN_IGNORE_FILE}" ]] \
    || fail "missing safe Jellyfin ignore marker: ${JELLYFIN_IGNORE_FILE}"
  [[ ! -s "${JELLYFIN_IGNORE_FILE}" ]] \
    || fail "Jellyfin ignore marker must be empty: ${JELLYFIN_IGNORE_FILE}"
}

ensure_jellyfin_ignore() {
  ensure_owned_directory "${PAYLOAD_DIR}" 0750
  ensure_owned_directory "${INCOMPLETE_DIR}" 0750
  if [[ -e "${JELLYFIN_IGNORE_FILE}" || -L "${JELLYFIN_IGNORE_FILE}" ]]; then
    [[ -f "${JELLYFIN_IGNORE_FILE}" && ! -L "${JELLYFIN_IGNORE_FILE}" ]] \
      || fail "refusing unsafe Jellyfin ignore marker: ${JELLYFIN_IGNORE_FILE}"
  else
    install -o "${QBITTORRENT_UID}" -g "${QBITTORRENT_GID}" -m 0640 /dev/null "${JELLYFIN_IGNORE_FILE}"
  fi
  [[ ! -s "${JELLYFIN_IGNORE_FILE}" ]] \
    || fail "Jellyfin ignore marker must be empty: ${JELLYFIN_IGNORE_FILE}"
  chown "${QBITTORRENT_UID}:${QBITTORRENT_GID}" "${JELLYFIN_IGNORE_FILE}"
  chmod 0640 "${JELLYFIN_IGNORE_FILE}"
  verify_jellyfin_ignore
}

create_image() {
  [[ ! -e "${IMAGE_FILE}" ]] || fail "image appeared while preparing storage; refusing to overwrite it"
  if findmnt -rn -M "${MOUNT_DIR}" >/dev/null 2>&1; then
    fail "${MOUNT_DIR} is already a mount while ${IMAGE_FILE} is absent"
  fi
  if directory_has_entries "${MOUNT_DIR}"; then
    fail "${MOUNT_DIR} is nonempty; refusing to hide or adopt existing data"
  fi

  if [[ -e "${TRANSACTION_DIR}" || -L "${TRANSACTION_DIR}" ]]; then
    [[ -d "${TRANSACTION_DIR}" && ! -L "${TRANSACTION_DIR}" ]] || fail "unsafe interrupted storage transaction: ${TRANSACTION_DIR}"
    [[ -f "${TRANSACTION_OWNER}" && ! -L "${TRANSACTION_OWNER}" ]] || fail "unowned interrupted storage transaction: ${TRANSACTION_DIR}"
    grep -Fx "${TRANSACTION_FORMAT}" "${TRANSACTION_OWNER}" >/dev/null || fail "unknown interrupted storage transaction: ${TRANSACTION_DIR}"
    if [[ -f "${TRANSACTION_STATE}" ]] && grep -Fx 'complete' "${TRANSACTION_STATE}" >/dev/null; then
      TEMP_IMAGE="${TRANSACTION_DIR}/downloads.ext4"
      verify_image_path "${TEMP_IMAGE}"
    else
      rm -rf -- "${TRANSACTION_DIR}"
    fi
  fi

  if [[ -z "${TEMP_IMAGE}" ]]; then
    (umask 0077; mkdir "${TRANSACTION_DIR}")
    printf '%s\n' "${TRANSACTION_FORMAT}" > "${TRANSACTION_OWNER}"
    install -d -m 0750 "${TRANSACTION_DIR}/seed/payload/.incomplete"
    {
      printf 'format=%s\n' "${MARKER_FORMAT}"
      printf 'size_bytes=%s\n' "${IMAGE_SIZE_BYTES}"
    } > "${TRANSACTION_DIR}/seed/payload/.arbuzas-qbittorrent-volume"
    chmod 0444 "${TRANSACTION_DIR}/seed/payload/.arbuzas-qbittorrent-volume"
    : > "${TRANSACTION_DIR}/seed/payload/.incomplete/.ignore"
    chmod 0640 "${TRANSACTION_DIR}/seed/payload/.incomplete/.ignore"

    TEMP_IMAGE="${TRANSACTION_DIR}/downloads.ext4"
    (umask 0077; : > "${TEMP_IMAGE}")
    fallocate -l "${IMAGE_SIZE_BYTES}" "${TEMP_IMAGE}"
    mkfs.ext4 -q -F -m 0 -L "${FILESYSTEM_LABEL}" -d "${TRANSACTION_DIR}/seed" "${TEMP_IMAGE}"
    # Re-allocate any ranges the formatter may have discarded from the regular file.
    fallocate -l "${IMAGE_SIZE_BYTES}" "${TEMP_IMAGE}"
    verify_image_path "${TEMP_IMAGE}"
    printf 'complete\n' > "${TRANSACTION_STATE}"
  fi

  if ! ln "${TEMP_IMAGE}" "${IMAGE_FILE}"; then
    fail "could not install the new image without overwriting an existing path"
  fi
  rm -rf -- "${TRANSACTION_DIR}"
  TEMP_IMAGE=""
}

reapply_declared_docker_no_swap_limits() {
  command -v docker >/dev/null 2>&1 || return 0
  command -v systemctl >/dev/null 2>&1 || fail "missing required command: systemctl"

  local container_id memory_limit memory_swap_limit scope_unit control_group
  local systemd_swap_limit cgroup_swap_file
  while IFS= read -r container_id; do
    [[ -n "${container_id}" ]] || continue
    [[ "${container_id}" =~ ^[0-9a-f]{64}$ ]] || fail "Docker returned an invalid full container ID: ${container_id}"
    read -r memory_limit memory_swap_limit < <(
      docker inspect --format '{{.HostConfig.Memory}} {{.HostConfig.MemorySwap}}' "${container_id}"
    )
    [[ "${memory_limit}" =~ ^[1-9][0-9]*$ ]] || continue
    [[ "${memory_swap_limit}" == "${memory_limit}" ]] || continue
    docker update \
      --memory "${memory_limit}" \
      --memory-swap "${memory_swap_limit}" \
      "${container_id}" >/dev/null

    # runc writes a zero cgroup-v2 swap limit directly but omits the matching
    # zero-valued systemd property. Persist it on the transient Docker scope so
    # a later systemd daemon-reload cannot restore the default infinity value.
    scope_unit="docker-${container_id}.scope"
    systemctl is-active --quiet "${scope_unit}" || fail "missing active Docker systemd scope: ${scope_unit}"
    systemctl set-property --runtime "${scope_unit}" MemorySwapMax=0 >/dev/null

    systemd_swap_limit="$(systemctl show --property=MemorySwapMax --value "${scope_unit}")"
    [[ "${systemd_swap_limit}" == "0" ]] || fail "${scope_unit} retained MemorySwapMax=${systemd_swap_limit}"
    control_group="$(systemctl show --property=ControlGroup --value "${scope_unit}")"
    [[ "${control_group}" == /*/"${scope_unit}" ]] || fail "unexpected control group for ${scope_unit}: ${control_group}"
    cgroup_swap_file="/sys/fs/cgroup${control_group}/memory.swap.max"
    [[ -r "${cgroup_swap_file}" ]] || fail "unreadable swap limit for ${scope_unit}: ${cgroup_swap_file}"
    grep -Fx '0' "${cgroup_swap_file}" >/dev/null || fail "${scope_unit} did not retain a zero cgroup swap limit"
  done < <(docker ps -q --no-trunc)
}

install_mount() {
  local unit_changed=0
  local enablement_changed=0
  [[ -f "${UNIT_SOURCE}" && ! -L "${UNIT_SOURCE}" ]] || fail "missing mount unit source: ${UNIT_SOURCE}"
  if [[ -e "${UNIT_TARGET}" || -L "${UNIT_TARGET}" ]]; then
    [[ -f "${UNIT_TARGET}" && ! -L "${UNIT_TARGET}" ]] || fail "refusing unsafe installed mount unit path: ${UNIT_TARGET}"
  fi
  if [[ ! -f "${UNIT_TARGET}" ]] || ! cmp -s "${UNIT_SOURCE}" "${UNIT_TARGET}"; then
    install -m 0644 "${UNIT_SOURCE}" "${UNIT_TARGET}"
    unit_changed=1
  fi
  if (( unit_changed == 1 || LOOP_ORDERING_CHANGED == 1 )); then
    systemctl daemon-reload
  fi
  if ! systemctl is-enabled --quiet "${UNIT_NAME}"; then
    systemctl enable "${UNIT_NAME}" >/dev/null
    enablement_changed=1
  fi
  if (( unit_changed == 1 || enablement_changed == 1 || LOOP_ORDERING_CHANGED == 1 )); then
    # systemd on this host reapplies infinity when runc has not persisted a
    # zero-valued transient scope property. Stabilize every running container
    # that already declares memory-swap equal to memory without restarting it.
    reapply_declared_docker_no_swap_limits
  fi

  if findmnt -rn -M "${MOUNT_DIR}" >/dev/null 2>&1; then
    verify_active_mount
    return
  fi
  if directory_has_entries "${MOUNT_DIR}"; then
    fail "${MOUNT_DIR} became nonempty before mount; refusing to hide its contents"
  fi

  systemctl start "${UNIT_NAME}"
  verify_active_mount
}

verify_mount_unit() {
  [[ -f "${UNIT_SOURCE}" && ! -L "${UNIT_SOURCE}" ]] || fail "missing mount unit source: ${UNIT_SOURCE}"
  [[ -f "${UNIT_TARGET}" && ! -L "${UNIT_TARGET}" ]] || fail "missing installed mount unit: ${UNIT_TARGET}"
  cmp -s "${UNIT_SOURCE}" "${UNIT_TARGET}" || fail "installed mount unit differs from the release copy"
}

ensure_owned_directory() {
  local path="$1"
  local mode="$2"
  if [[ -e "${path}" || -L "${path}" ]]; then
    [[ -d "${path}" && ! -L "${path}" ]] || fail "refusing unsafe managed directory: ${path}"
  else
    install -d -m "${mode}" "${path}"
  fi
  chown "${QBITTORRENT_UID}:${QBITTORRENT_GID}" "${path}"
  chmod "${mode}" "${path}"
}

prepare_owned_directories() {
  local path
  for path in "${BASE_DIR}" "${MOUNT_DIR}"; do
    if [[ -e "${path}" || -L "${path}" ]]; then
      [[ -d "${path}" && ! -L "${path}" ]] || fail "refusing unsafe directory path: ${path}"
    fi
  done
  for path in "${LEGACY_CONFIG_DIR}" "${LEGACY_DOWNLOADS_DIR}"; do
    if [[ -e "${path}" || -L "${path}" ]]; then
      fail "refusing unmanaged legacy path outside the capped filesystem: ${path}"
    fi
  done
  install -d -m 0750 "${BASE_DIR}" "${MOUNT_DIR}"
}

apply_runtime_permissions() {
  ensure_owned_directory "${CONFIG_DIR}" 0750
  ensure_owned_directory "${QBITTORRENT_CONFIG_DIR}" 0750
  ensure_jellyfin_ignore
  # find does not follow symlinks by default, and chown -h keeps ownership
  # reconciliation from dereferencing qBittorrent-controlled links as root.
  find "${CONFIG_DIR}" -xdev -mindepth 1 -exec chown -h "${QBITTORRENT_UID}:${QBITTORRENT_GID}" {} +
}

prepare_media_consumer() {
  verify_loop_module_policy
  verify_image
  verify_active_mount
  verify_mount_unit
  systemctl is-enabled --quiet "${UNIT_NAME}" || fail "${UNIT_NAME} is not enabled"
  systemctl is-active --quiet "${UNIT_NAME}" || fail "${UNIT_NAME} is not active"
  ensure_jellyfin_ignore
}

main() {
  parse_args "$@"
  [[ "$(id -u)" == "0" ]] || fail "must run as root"
  require_commands
  acquire_lock

  if [[ "${ACTION}" == "check" ]]; then
    verify_loop_module_policy
    verify_image
    verify_active_mount
    verify_mount_unit
    systemctl is-enabled --quiet "${UNIT_NAME}" || fail "${UNIT_NAME} is not enabled"
    systemctl is-active --quiet "${UNIT_NAME}" || fail "${UNIT_NAME} is not active"
    verify_jellyfin_ignore
    return
  fi

  if [[ "${ACTION}" == "prepare-media" ]]; then
    prepare_media_consumer
    return
  fi

  install_loop_module_policy
  prepare_owned_directories
  if [[ ! -e "${IMAGE_FILE}" ]]; then
    create_image
  fi
  verify_image
  install_mount
  apply_runtime_permissions
}

main "$@"
