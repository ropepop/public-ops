#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OPS_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

MODE="plan"
EXECUTE=0
INSTALL_SERVER_PREREQUISITES=0
RESTORE_EMPTY_SERVER=0
REPLACE_PIXEL_TOKEN=0
REPLACE_PIXEL_VIVI_LOGIN=0
AUTHORIZE_SERVER_ADB_KEY=0
PIXEL_PLATFORM_BOOTSTRAP=0

SERVER_HOST=""
SERVER_USER=""
SERVER_PORT=""
SERVER_HOST_KEY_SHA256=""
SERVER_KNOWN_HOSTS_FILE=""
SERVER_KNOWN_HOSTS_DIR=""
SERVER_MIRROR_ROOT=""
SERVER_RECOVERY_MIRROR_ROOT=""
ACTIVE_SERVER_MIRROR_ROOT=""
SERVER_WORKING_MIRROR=""
SERVER_REMOTE_IS_ROOT=0
SERVER_TARGET_CONFIG_EMPTY=0
SERVER_RELEASE_ID=""
TICKET_ADB_TARGET=""

PIXEL_REPO=""
PIXEL_MIRROR_ROOT=""
PIXEL_TOKEN_FILE=""
PIXEL_VIVI_LOGIN_FILE=""
PIXEL_TRANSPORT=""
PIXEL_DEVICE=""
PIXEL_SSH_HOST=""
PIXEL_SSH_PORT="2222"
PIXEL_ROOTFS_TARBALL="${PIXEL_RUNTIME_ROOTFS_TARBALL:-}"
PIXEL_DROPBEAR_ARTIFACT_DIR="${PIXEL_RUNTIME_DROPBEAR_ARTIFACT_DIR:-}"
PIXEL_TAILSCALE_BUNDLE="${PIXEL_RUNTIME_TAILSCALE_BUNDLE:-}"
ALLOW_NONPRIVATE_ADB_TARGET=0
PIXEL_JAVA_HOME=""
PIXEL_ANDROID_SDK_ROOT=""

TICKET_SERVICES="train_bot,train_tunnel,ticket_phone_bridge,ticket_remote_spacetime_sidecar,ticket_hdr_transformer,ticket_remote,ticket_remote_tunnel"
TICKET_MIRROR_PROFILE="ticket-recovery"
TICKET_MIRROR_PATHS=(
  etc/arbuzas/env/ticket-remote.env
  etc/arbuzas/env/train-bot.env
  etc/arbuzas/secrets/android-adb/adbkey
  etc/arbuzas/secrets/android-adb/adbkey.pub
  etc/arbuzas/secrets/android-adb/adb_known_hosts.pb
  etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem
  etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret
  etc/arbuzas/secrets/ticket-remote/turn.secret
  etc/arbuzas/secrets/train-bot-spacetime.key
  etc/arbuzas/secrets/train-bot-web-session-secret
  etc/arbuzas/secrets/train-bot-test-ticket.secret
  etc/arbuzas/cloudflared/ticket-remote.json
  etc/arbuzas/cloudflared/train-bot.json
)

usage() {
  cat <<'USAGE'
Usage: bootstrap.sh [plan|preflight|server|pixel|all] [options]

One entrypoint for restoring Ticket Remote onto a prepared clean VPS, a
prepared clean rooted Pixel, or both. The default mode is plan and never
changes either target.

Modes:
  plan       Print the selected recovery plan without probing or changing targets.
  preflight  Run read-only local, VPS, and Pixel readiness checks.
  server     Restore and deploy six Ticket and Train/OIDC server services.
  pixel      Install the Pixel orchestrator Ticket runtime and start it.
  all        Install the Pixel first, then deploy and validate the server.

Safety:
  --execute                       Required by server, pixel, and all.
  --install-server-prerequisites  On Debian/Ubuntu whose configured apt repos
                                  already provide Docker and Compose v2, install
                                  those packages plus Python, curl, and tar.
  --restore-empty-server          Seed the local host mirror only when the
                                  target /etc/arbuzas config trees contain no files.
  --replace-pixel-token           Permit replacing a different existing Pixel
                                  Ticket service token. Missing or identical is safe.
  --replace-pixel-vivi-login      Permit replacing different existing ViVi login
                                  credentials. Missing or identical is safe.
  --authorize-server-adb-key      Add the mirrored server ADB public key to the
                                  rooted Pixel. Does not expose ADB or alter networking.

Server target:
  --server-host HOST
  --server-user USER
  --server-port PORT
  --server-host-key-sha256 SHA256:...
                                  Verify a new host key into a temporary file.
                                  Without this, the existing known_hosts entry is required.
  --server-mirror-root DIR        Default: infra/arbuzas/host-mirror
  --server-recovery-mirror-root DIR
                                  Persistent narrow mirror state. Default:
                                  state/ticket-bootstrap/server-mirrors/<target>.
  --ticket-adb-target HOST:PORT   Private/Tailscale Pixel ADB target seen by VPS.
  --allow-nonprivate-adb-target   Explicitly allow a non-private ADB address.
                                  Unsafe outside a separately protected network.

Pixel target:
  --pixel-repo DIR                Default: sibling ../pixel-phone checkout
  --pixel-mirror-root DIR         Default: <pixel-repo>/host-mirror
  --pixel-token-file FILE         Default: ticket token in the Pixel mirror
  --pixel-vivi-login-file FILE    Default: ViVi recovery login in the Pixel mirror
  --pixel-transport adb|ssh       Required for pixel/all; use adb for a clean device.
  --pixel-device SERIAL           Required with adb.
  --pixel-ssh-host HOST           Required with ssh.
  --pixel-ssh-port PORT           Default: 2222.
  --pixel-platform-bootstrap      Install the private SSH/VPN platform first.
                                  Required when those assets are absent on a clean Pixel.
  --pixel-rootfs-tarball FILE     Reviewed rooted runtime rootfs artifact.
  --pixel-dropbear-artifact-dir DIR
                                  Reviewed arm64 Dropbear artifact directory.
  --pixel-tailscale-bundle FILE   Reviewed arm64 Tailscale runtime bundle.

This script never creates credentials, embeds secret values, roots Android,
or installs ViVi from an unofficial source. It joins/starts the Pixel private
VPN only when --pixel-platform-bootstrap and reviewed mirrored credentials are
explicitly supplied; otherwise private networking remains a prerequisite.
USAGE
}

die() {
  echo "ERROR: $*" >&2
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing local command: $1"
}

require_file() {
  [[ -f "$1" ]] || die "required file is missing: $1"
}

validate_safe_target_value() {
  local label="$1"
  local value="$2"
  [[ -n "${value}" ]] || die "${label} is required"
  [[ "${value}" =~ ^[A-Za-z0-9._:@%+-]+$ ]] || die "${label} contains unsupported characters"
}

validate_port() {
  local label="$1"
  local value="$2"
  [[ "${value}" =~ ^[0-9]+$ ]] || die "${label} must be a port number"
  (( value >= 1 && value <= 65535 )) || die "${label} must be between 1 and 65535"
}

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

require_private_file() {
  local path="$1"
  require_file "${path}"
  [[ -s "${path}" ]] || die "required secret file is empty: ${path}"
  python3 - "${path}" <<'PY'
import os
import stat
import sys

path = sys.argv[1]
mode = stat.S_IMODE(os.stat(path).st_mode)
if mode & 0o077:
    raise SystemExit(f"secret file must not be group/world accessible: {path} mode={mode:04o}")
PY
}

server_ssh_args() {
  SERVER_SSH_ARGS=(-o BatchMode=yes -o ConnectTimeout=12 -o StrictHostKeyChecking=yes)
  if [[ -n "${SERVER_KNOWN_HOSTS_FILE}" ]]; then
    SERVER_SSH_ARGS+=(-o "UserKnownHostsFile=${SERVER_KNOWN_HOSTS_FILE}")
  fi
  if [[ -n "${SERVER_PORT}" ]]; then
    SERVER_SSH_ARGS+=(-p "${SERVER_PORT}")
  fi
}

prepare_server_known_hosts() {
  local scan_port="${SERVER_PORT:-22}"
  local scan_file=""
  local candidate_file=""
  local line=""
  local fingerprint=""
  [[ -n "${SERVER_HOST_KEY_SHA256}" ]] || return 0
  [[ "${SERVER_HOST_KEY_SHA256}" =~ ^SHA256:[A-Za-z0-9+/=]+$ ]] ||
    die "--server-host-key-sha256 must be an exact SHA256 fingerprint"
  require_cmd ssh-keyscan
  require_cmd ssh-keygen
  SERVER_KNOWN_HOSTS_DIR="$(mktemp -d "${TMPDIR:-/tmp}/ticket-server-known-hosts.XXXXXX")"
  chmod 700 "${SERVER_KNOWN_HOSTS_DIR}"
  scan_file="${SERVER_KNOWN_HOSTS_DIR}/scan"
  candidate_file="${SERVER_KNOWN_HOSTS_DIR}/candidate"
  SERVER_KNOWN_HOSTS_FILE="${SERVER_KNOWN_HOSTS_DIR}/verified"
  : > "${SERVER_KNOWN_HOSTS_FILE}"
  chmod 600 "${SERVER_KNOWN_HOSTS_FILE}"
  ssh-keyscan -T 8 -p "${scan_port}" "${SERVER_HOST}" > "${scan_file}" 2>/dev/null ||
    die "could not scan the VPS host key"
  [[ -s "${scan_file}" ]] || die "VPS host-key scan returned no keys"
  while IFS= read -r line; do
    [[ -n "${line}" && "${line}" != \#* ]] || continue
    printf '%s\n' "${line}" > "${candidate_file}"
    fingerprint="$(ssh-keygen -lf "${candidate_file}" -E sha256 2>/dev/null | awk 'NR==1 {print $2}')"
    if [[ "${fingerprint}" == "${SERVER_HOST_KEY_SHA256}" ]]; then
      cat "${candidate_file}" >> "${SERVER_KNOWN_HOSTS_FILE}"
    fi
  done < "${scan_file}"
  rm -f "${scan_file}" "${candidate_file}"
  [[ -s "${SERVER_KNOWN_HOSTS_FILE}" ]] ||
    die "VPS host-key fingerprint did not match the supplied fingerprint"
}

server_ssh() {
  server_ssh_args
  ssh "${SERVER_SSH_ARGS[@]}" "${SERVER_USER}@${SERVER_HOST}" "$@"
}

server_deploy_target_args() {
  SERVER_DEPLOY_TARGET_ARGS=(--ssh-host "${SERVER_HOST}" --ssh-user "${SERVER_USER}")
  if [[ -n "${SERVER_PORT}" ]]; then
    SERVER_DEPLOY_TARGET_ARGS+=(--ssh-port "${SERVER_PORT}")
  fi
  if [[ -n "${SERVER_KNOWN_HOSTS_FILE}" ]]; then
    SERVER_DEPLOY_TARGET_ARGS+=(--ssh-known-hosts-file "${SERVER_KNOWN_HOSTS_FILE}")
  fi
}

validate_server_options() {
  validate_safe_target_value "--server-host" "${SERVER_HOST}"
  validate_safe_target_value "--server-user" "${SERVER_USER}"
  if [[ -n "${SERVER_PORT}" ]]; then
    validate_port "--server-port" "${SERVER_PORT}"
  fi
  [[ "${TICKET_ADB_TARGET}" =~ ^[A-Za-z0-9._-]+:[0-9]+$ ]] ||
    die "--ticket-adb-target must be a private HOST:PORT target"
  validate_port "Ticket ADB target port" "${TICKET_ADB_TARGET##*:}"
  validate_ticket_adb_target_privacy
  if [[ -z "${SERVER_RECOVERY_MIRROR_ROOT}" ]]; then
    local target_slug=""
    target_slug="$(printf '%s' "${SERVER_USER}-${SERVER_HOST}-${SERVER_PORT:-22}" | tr -c 'A-Za-z0-9._-' '_')"
    SERVER_RECOVERY_MIRROR_ROOT="${OPS_ROOT}/state/ticket-bootstrap/server-mirrors/${target_slug}"
  fi
  [[ "${SERVER_RECOVERY_MIRROR_ROOT}" == /* ]] || die "--server-recovery-mirror-root must be absolute"
}

validate_ticket_adb_target_privacy() {
  local target_host="${TICKET_ADB_TARGET%:*}"
  (( ALLOW_NONPRIVATE_ADB_TARGET == 1 )) && return 0
  python3 - "${target_host}" <<'PY'
import ipaddress
import sys

try:
    address = ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit("ADB target must be a private IP address; use --allow-nonprivate-adb-target only for a separately protected hostname")

allowed = (
    ipaddress.ip_network("10.0.0.0/8"),
    ipaddress.ip_network("172.16.0.0/12"),
    ipaddress.ip_network("192.168.0.0/16"),
    ipaddress.ip_network("100.64.0.0/10"),
    ipaddress.ip_network("127.0.0.0/8"),
)
if not any(address in network for network in allowed):
    raise SystemExit("ADB target is not RFC1918, CGNAT, or loopback; refusing public ADB")
PY
}

validate_pixel_options() {
  case "${PIXEL_TRANSPORT}" in
    adb)
      [[ -n "${PIXEL_DEVICE}" ]] || die "--pixel-device is required with --pixel-transport adb"
      ;;
    ssh)
      [[ -n "${PIXEL_SSH_HOST}" ]] || die "--pixel-ssh-host is required with --pixel-transport ssh"
      validate_port "--pixel-ssh-port" "${PIXEL_SSH_PORT}"
      ;;
    *)
      die "--pixel-transport must be explicitly set to adb or ssh"
      ;;
  esac
}

preflight_server_mirror() {
  local env_file="${SERVER_MIRROR_ROOT}/etc/arbuzas/env/ticket-remote.env"
  local manifest_file="${SERVER_MIRROR_ROOT}/.host-mirror-manifest.json"
  local required_private=""

  require_file "${manifest_file}"
  require_private_file "${env_file}"
  require_file "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/android-adb/adbkey.pub"

  for required_private in \
    "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/android-adb/adbkey" \
    "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/android-adb/adb_known_hosts.pb" \
    "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem" \
    "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret" \
    "${SERVER_MIRROR_ROOT}/etc/arbuzas/cloudflared/ticket-remote.json"
  do
    require_private_file "${required_private}"
  done
  require_private_file "${SERVER_MIRROR_ROOT}/etc/arbuzas/env/train-bot.env"
  require_private_file "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/train-bot-spacetime.key"
  require_private_file "${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/train-bot-web-session-secret"
  require_private_file "${SERVER_MIRROR_ROOT}/etc/arbuzas/cloudflared/train-bot.json"
  validate_server_adb_key_pair

  python3 - "${manifest_file}" "${env_file}" <<'PY'
import json
import sys

manifest_path, env_path = sys.argv[1:]
manifest = json.load(open(manifest_path, encoding="utf-8"))
if manifest.get("profile") != "arbuzas":
    raise SystemExit("server mirror manifest is not an arbuzas mirror")

values = {}
for raw in open(env_path, encoding="utf-8"):
    line = raw.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    key, value = line.split("=", 1)
    values[key.strip()] = value.strip()

required = (
    "TICKET_REMOTE_AUTH_MODE",
    "TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID",
    "TICKET_REMOTE_SESSION_SIGNING_KEY",
    "TICKET_REMOTE_STATE_BACKEND",
    "TICKET_REMOTE_SPACETIME_DATABASE",
    "TICKET_REMOTE_SPACETIME_OIDC_ISSUER",
    "TICKET_REMOTE_SPACETIME_OIDC_AUDIENCE",
    "TICKET_REMOTE_SPACETIME_SERVICE_SUBJECT",
    "TICKET_REMOTE_SPACETIME_SERVICE_ROLES",
)
missing = []
for key in required:
    value = values.get(key, "")
    lowered = value.lower()
    if not value or "replace-with" in lowered or "changeme" in lowered:
        missing.append(key)
if missing:
    raise SystemExit("Ticket server environment is incomplete: " + ", ".join(missing))
PY
}

validate_server_adb_key_pair() {
  local private_key="${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/android-adb/adbkey"
  local public_key="${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/android-adb/adbkey.pub"
  local generated=""
  local expected=""
  require_cmd adb
  generated="$(adb pubkey "${private_key}" | awk 'NR==1 {print $1}')"
  expected="$(awk 'NR==1 {print $1}' "${public_key}")"
  [[ -n "${generated}" && "${generated}" == "${expected}" ]] ||
    die "mirrored Android ADB private/public keys do not match"
}

preflight_server_access() {
  local remote_uid=""
  remote_uid="$(server_ssh 'set -eu; command -v bash >/dev/null; id -u' | tr -d '\r[:space:]')"
  if [[ "${remote_uid}" == "0" ]]; then
    SERVER_REMOTE_IS_ROOT=1
    return 0
  fi
  [[ "${remote_uid}" =~ ^[0-9]+$ ]] || die "could not determine VPS user identity"
  server_ssh 'set -eu; command -v sudo >/dev/null; sudo -n true'
}

preflight_server_runtime() {
  server_ssh 'set -eu
command -v python3 >/dev/null
command -v curl >/dev/null
command -v tar >/dev/null
command -v docker >/dev/null
if [ "$(id -u)" -eq 0 ]; then
  docker info >/dev/null
else
  command -v sudo >/dev/null
  sudo -n true
  docker info >/dev/null
fi
docker compose version >/dev/null'
}

server_mirror_is_empty() {
  server_ssh 'set -eu
as_root() {
  if [ "$(id -u)" -eq 0 ]; then "$@"; else sudo -n "$@"; fi
}
if as_root find /etc/arbuzas/env /etc/arbuzas/secrets /etc/arbuzas/cloudflared -type f -print -quit 2>/dev/null | grep -q .; then
  exit 1
fi'
}

preflight_server_prerequisite_install() {
  require_file "${OPS_ROOT}/tools/ticket/install_server_prerequisites.sh"
  server_ssh 'bash -s -- check' < "${OPS_ROOT}/tools/ticket/install_server_prerequisites.sh"
}

install_server_prerequisites() {
  echo "Installing supported Debian/Ubuntu VPS prerequisites from configured apt repositories"
  require_file "${OPS_ROOT}/tools/ticket/install_server_prerequisites.sh"
  server_ssh 'bash -s -- install' < "${OPS_ROOT}/tools/ticket/install_server_prerequisites.sh"
}

cleanup_server_working_mirror() {
  if [[ -n "${SERVER_WORKING_MIRROR}" && -d "${SERVER_WORKING_MIRROR}" ]]; then
    rm -rf "${SERVER_WORKING_MIRROR}"
  fi
  if [[ -n "${SERVER_KNOWN_HOSTS_FILE}" && -f "${SERVER_KNOWN_HOSTS_FILE}" ]]; then
    rm -f "${SERVER_KNOWN_HOSTS_FILE}"
  fi
  if [[ -n "${SERVER_KNOWN_HOSTS_DIR}" && -d "${SERVER_KNOWN_HOSTS_DIR}" ]]; then
    rm -rf "${SERVER_KNOWN_HOSTS_DIR}"
  fi
}

prepare_server_recovery_mirror() {
  local source_path=""
  local relative_path=""
  local manifest_file="${SERVER_RECOVERY_MIRROR_ROOT}/.host-mirror-manifest.json"
  server_deploy_target_args
  [[ ! -L "${SERVER_RECOVERY_MIRROR_ROOT}" ]] || die "recovery mirror root must not be a symlink"
  mkdir -p "${SERVER_RECOVERY_MIRROR_ROOT}"
  chmod 700 "${SERVER_RECOVERY_MIRROR_ROOT}"

  if [[ ! -f "${manifest_file}" ]]; then
    if find "${SERVER_RECOVERY_MIRROR_ROOT}" -type f -print -quit | grep -q .; then
      die "recovery mirror has files but no manifest: ${SERVER_RECOVERY_MIRROR_ROOT}"
    fi
    if (( SERVER_TARGET_CONFIG_EMPTY == 0 )); then
      ARBUZAS_HOST_MIRROR_PROFILE="arbuzas" \
      ARBUZAS_HOST_MIRROR_PRIVILEGED="$((1 - SERVER_REMOTE_IS_ROOT))" \
      ARBUZAS_HOST_MIRROR_ROOT="${SERVER_MIRROR_ROOT}" \
        "${OPS_ROOT}/tools/arbuzas/deploy.sh" mirror-audit "${SERVER_DEPLOY_TARGET_ARGS[@]}"
    fi
    ARBUZAS_HOST_MIRROR_PROFILE="${TICKET_MIRROR_PROFILE}" \
    ARBUZAS_HOST_MIRROR_PRIVILEGED="$((1 - SERVER_REMOTE_IS_ROOT))" \
    ARBUZAS_HOST_MIRROR_ROOT="${SERVER_RECOVERY_MIRROR_ROOT}" \
      "${OPS_ROOT}/tools/arbuzas/deploy.sh" mirror-pull "${SERVER_DEPLOY_TARGET_ARGS[@]}"
  fi
  python3 - "${manifest_file}" "${TICKET_MIRROR_PROFILE}" <<'PY'
import json
import sys

manifest = json.load(open(sys.argv[1], encoding="utf-8"))
if manifest.get("profile") != sys.argv[2]:
    raise SystemExit("persistent recovery mirror has the wrong profile")
PY

  for relative_path in "${TICKET_MIRROR_PATHS[@]}"; do
    source_path="${SERVER_MIRROR_ROOT}/${relative_path}"
    mkdir -p "${SERVER_RECOVERY_MIRROR_ROOT}/$(dirname "${relative_path}")"
    rm -f "${SERVER_RECOVERY_MIRROR_ROOT}/${relative_path}"
    if [[ -f "${source_path}" ]]; then
      cp -p "${source_path}" "${SERVER_RECOVERY_MIRROR_ROOT}/${relative_path}"
    fi
  done
  ACTIVE_SERVER_MIRROR_ROOT="${SERVER_RECOVERY_MIRROR_ROOT}"
}

ticket_oidc_issuer() {
  python3 - "${SERVER_MIRROR_ROOT}/etc/arbuzas/env/ticket-remote.env" <<'PY'
import sys
from urllib.parse import urlparse

value = ""
for raw in open(sys.argv[1], encoding="utf-8"):
    if raw.startswith("TICKET_REMOTE_SPACETIME_OIDC_ISSUER="):
        value = raw.split("=", 1)[1].strip()
parsed = urlparse(value)
if parsed.scheme != "https" or not parsed.netloc or parsed.query or parsed.fragment:
    raise SystemExit("Ticket OIDC issuer must be a plain HTTPS URL")
print(value.rstrip("/"))
PY
}

preflight_server_oidc_issuer() {
  local issuer=""
  issuer="$(ticket_oidc_issuer)"
  [[ "${issuer}" =~ ^https://[A-Za-z0-9._:/-]+$ ]] || die "Ticket OIDC issuer contains unsupported characters"
  server_ssh "OIDC_ISSUER='${issuer}' bash -s" <<'REMOTE'
set -euo pipefail
curl -fsS --max-time 12 "${OIDC_ISSUER}/.well-known/openid-configuration" >/dev/null
REMOTE
}

audit_server_mirror() {
  server_deploy_target_args
  [[ "${ACTIVE_SERVER_MIRROR_ROOT}" == "${SERVER_RECOVERY_MIRROR_ROOT}" ]] ||
    die "Ticket recovery refused a non-recovery mirror root"
  ARBUZAS_HOST_MIRROR_PROFILE="${TICKET_MIRROR_PROFILE}" \
  ARBUZAS_HOST_MIRROR_PRIVILEGED="$((1 - SERVER_REMOTE_IS_ROOT))" \
  ARBUZAS_HOST_MIRROR_ROOT="${ACTIVE_SERVER_MIRROR_ROOT}" \
    "${OPS_ROOT}/tools/arbuzas/deploy.sh" mirror-audit "${SERVER_DEPLOY_TARGET_ARGS[@]}"
}

require_canonical_server_deploy_privilege() {
  if (( SERVER_REMOTE_IS_ROOT == 1 )); then
    if ! server_ssh 'command -v sudo >/dev/null 2>&1'; then
      die "the canonical server deployer requires sudo; add --install-server-prerequisites on this clean root VPS"
    fi
  fi
}

preflight_server_phone_route() {
  local target_host="${TICKET_ADB_TARGET%:*}"
  local target_port="${TICKET_ADB_TARGET##*:}"
  server_ssh "TARGET_HOST='${target_host}' TARGET_PORT='${target_port}' bash -s" <<'REMOTE'
set -euo pipefail
if command -v python3 >/dev/null 2>&1; then
  python3 - "${TARGET_HOST}" "${TARGET_PORT}" <<'PY'
import socket
import sys

sock = socket.create_connection((sys.argv[1], int(sys.argv[2])), timeout=8)
sock.close()
PY
else
  command -v timeout >/dev/null 2>&1
  timeout 8 bash -c 'exec 3<>/dev/tcp/$1/$2' _ "${TARGET_HOST}" "${TARGET_PORT}"
fi
REMOTE
}

configure_pixel_transport() {
  export ADB_SERIAL="${PIXEL_DEVICE}"
  export PIXEL_TRANSPORT PIXEL_SSH_HOST PIXEL_SSH_PORT
  # shellcheck source=/dev/null
  source "${PIXEL_REPO}/tools/pixel/transport.sh"
}

pixel_deploy_target_args() {
  PIXEL_DEPLOY_TARGET_ARGS=(--transport "${PIXEL_TRANSPORT}")
  if [[ "${PIXEL_TRANSPORT}" == "adb" ]]; then
    PIXEL_DEPLOY_TARGET_ARGS+=(--device "${PIXEL_DEVICE}")
  else
    PIXEL_DEPLOY_TARGET_ARGS+=(--ssh-host "${PIXEL_SSH_HOST}" --ssh-port "${PIXEL_SSH_PORT}")
  fi
}

preflight_pixel_local() {
  require_cmd bash
  require_cmd python3
  require_cmd adb
  require_private_file "${PIXEL_TOKEN_FILE}"
  require_private_file "${PIXEL_VIVI_LOGIN_FILE}"
  require_file "${PIXEL_REPO}/tools/pixel/transport.sh"
  require_file "${PIXEL_REPO}/tools/pixel/ticket_first_setup.sh"
  require_file "${PIXEL_REPO}/orchestrator/scripts/android/deploy_orchestrator_apk.sh"
  require_file "${PIXEL_REPO}/orchestrator/android-orchestrator/gradlew"
  [[ -x "${PIXEL_REPO}/orchestrator/android-orchestrator/gradlew" ]] || die "Pixel Gradle wrapper is not executable"
  require_file "${PIXEL_REPO}/orchestrator/android-orchestrator/gradle/wrapper/gradle-wrapper.jar"
  resolve_pixel_java17
  resolve_pixel_android_toolchain
}

pixel_remote_secret_hash_or_absent() {
  local remote_path="$1"
  local label="$2"
  local value=""
  value="$(pixel_transport_remote_sha256_file "${remote_path}" 2>/dev/null || true)"
  case "${value}" in
    MISSING)
      printf '\n'
      ;;
    UNKNOWN|"")
      die "could not verify existing Pixel ${label}; refusing to overwrite an unknown state"
      ;;
    *)
      if [[ "${value}" =~ ^[0-9a-f]{64}$ ]]; then
        printf '%s\n' "${value}"
      else
        die "Pixel ${label} returned an invalid hash state"
      fi
      ;;
  esac
}

resolve_pixel_java17() {
  if [[ -x /usr/libexec/java_home ]]; then
    PIXEL_JAVA_HOME="$(/usr/libexec/java_home -v 17 2>/dev/null || true)"
  elif [[ -n "${JAVA_HOME:-}" ]] && "${JAVA_HOME}/bin/java" -version 2>&1 | head -n1 | grep -Eq 'version "17([.]|\")'; then
    PIXEL_JAVA_HOME="${JAVA_HOME}"
  else
    PIXEL_JAVA_HOME="$(find /usr/lib/jvm -maxdepth 1 -type d -name 'java-17*' 2>/dev/null | head -n1 || true)"
  fi
  [[ -x "${PIXEL_JAVA_HOME}/bin/java" ]] || die "JDK 17 is required for the Pixel APK build"
}

resolve_pixel_android_toolchain() {
  PIXEL_ANDROID_SDK_ROOT="${ANDROID_SDK_ROOT:-${ANDROID_HOME:-}}"
  if [[ -z "${PIXEL_ANDROID_SDK_ROOT}" ]]; then
    PIXEL_ANDROID_SDK_ROOT="$(python3 - "${PIXEL_REPO}/orchestrator/android-orchestrator/local.properties" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
if path.exists():
    for raw in path.read_text(encoding="utf-8").splitlines():
        if raw.startswith("sdk.dir="):
            print(raw.split("=", 1)[1].replace("\\:", ":").replace("\\\\", "\\"))
            break
PY
)"
  fi
  [[ -f "${PIXEL_ANDROID_SDK_ROOT}/platforms/android-35/android.jar" ]] ||
    die "Android SDK platform 35 is required for the Pixel APK build"
  find "${PIXEL_ANDROID_SDK_ROOT}/ndk" \
    -path '*/toolchains/llvm/prebuilt/*/bin/aarch64-linux-android29-clang' \
    -type f -perm -111 -print -quit 2>/dev/null | grep -q . ||
    die "an Android NDK with the arm64 API 29 compiler is required for the root keyboard helper"
}

preflight_pixel_platform_inputs() {
  local mirror_conf="${PIXEL_MIRROR_ROOT}/data/local/pixel-stack/conf"
  (( PIXEL_PLATFORM_BOOTSTRAP == 1 )) || return 0
  require_file "${PIXEL_ROOTFS_TARBALL}"
  require_file "${PIXEL_TAILSCALE_BUNDLE}"
  [[ -x "${PIXEL_DROPBEAR_ARTIFACT_DIR}/dropbearmulti" ]] ||
    die "reviewed Dropbear artifact is missing: ${PIXEL_DROPBEAR_ARTIFACT_DIR}/dropbearmulti"
  require_private_file "${mirror_conf}/orchestrator-config-v1.json"
  require_private_file "${mirror_conf}/ssh/authorized_keys"
  require_private_file "${mirror_conf}/ssh/root_password.hash"
  require_private_file "${mirror_conf}/vpn/tailscale-authkey"
}

preflight_pixel_device() {
  local local_hash=""
  local remote_hash=""
  local local_vivi_hash=""
  local remote_vivi_hash=""
  configure_pixel_transport
  pixel_transport_require_device >/dev/null
  pixel_transport_require_root >/dev/null
  if ! pixel_transport_package_installed com.pv.vivi; then
    die "ViVi is not installed on the Pixel; install it from an approved store and sign in before retrying"
  fi

  if ! pixel_transport_root_exec test -x /data/local/pixel-stack/bin/pixel-ssh-start.sh >/dev/null 2>&1 ||
    ! pixel_transport_root_exec test -x /data/local/pixel-stack/bin/pixel-vpn-start.sh >/dev/null 2>&1
  then
    (( PIXEL_PLATFORM_BOOTSTRAP == 1 )) ||
      die "clean Pixel is missing the private SSH/VPN platform; add --pixel-platform-bootstrap and its reviewed artifacts"
  fi

  local_hash="$(file_sha256 "${PIXEL_TOKEN_FILE}")"
  remote_hash="$(pixel_remote_secret_hash_or_absent /data/local/pixel-stack/conf/apps/ticket-screen-spacetime-token "Ticket token")"
  if [[ -n "${remote_hash}" && "${remote_hash}" != "${local_hash}" && "${REPLACE_PIXEL_TOKEN}" != "1" ]]; then
    die "the Pixel already has a different Ticket token; pass --replace-pixel-token only for an intentional recovery cutover"
  fi

  local_vivi_hash="$(file_sha256 "${PIXEL_VIVI_LOGIN_FILE}")"
  remote_vivi_hash="$(pixel_remote_secret_hash_or_absent /data/local/pixel-stack/conf/apps/ticket-screen-vivi-login.env "ViVi login")"
  if [[ -n "${remote_vivi_hash}" && "${remote_vivi_hash}" != "${local_vivi_hash}" && "${REPLACE_PIXEL_VIVI_LOGIN}" != "1" ]]; then
    die "the Pixel already has different ViVi recovery credentials; pass --replace-pixel-vivi-login only for an intentional cutover"
  fi
}

stage_pixel_secret() {
  local local_path="$1"
  local remote_path="$2"
  local label="$3"
  local local_hash=""
  local remote_hash=""
  local_hash="$(file_sha256 "${local_path}")"
  remote_hash="$(pixel_remote_secret_hash_or_absent "${remote_path}" "${label}")"
  if [[ "${remote_hash}" == "${local_hash}" ]]; then
    echo "Pixel ${label} is already current"
    return 0
  fi
  pixel_transport_root_exec mkdir -p /data/local/pixel-stack/conf/apps >/dev/null
  pixel_transport_push "${local_path}" "${remote_path}" >/dev/null
  pixel_transport_root_exec chmod 600 "${remote_path}" >/dev/null
  pixel_transport_root_shell "chcon u:object_r:shell_data_file:s0 '${remote_path}' 2>/dev/null || true" >/dev/null
  remote_hash="$(pixel_remote_secret_hash_or_absent "${remote_path}" "${label}")"
  [[ "${remote_hash}" == "${local_hash}" ]] || die "Pixel ${label} verification failed after staging"
}

stage_pixel_ticket_secrets() {
  stage_pixel_secret \
    "${PIXEL_TOKEN_FILE}" \
    /data/local/pixel-stack/conf/apps/ticket-screen-spacetime-token \
    "Ticket service token"
  stage_pixel_secret \
    "${PIXEL_VIVI_LOGIN_FILE}" \
    /data/local/pixel-stack/conf/apps/ticket-screen-vivi-login.env \
    "ViVi recovery credentials"
}

authorize_server_adb_key_on_pixel() {
  local public_key="${SERVER_MIRROR_ROOT}/etc/arbuzas/secrets/android-adb/adbkey.pub"
  local staged_key="/data/local/tmp/ticket-bootstrap-server-adbkey-$$-${RANDOM}.pub"
  require_file "${public_key}"
  pixel_transport_push "${public_key}" "${staged_key}" >/dev/null
  pixel_transport_root_shell_stdin <<REMOTE
set -eu
staged_key='${staged_key}'
keys_file=/data/misc/adb/adb_keys
mkdir -p /data/misc/adb
touch "\${keys_file}"
if ! grep -Fqx -f "\${staged_key}" "\${keys_file}"; then
  if [ -s "\${keys_file}" ]; then printf '\n' >> "\${keys_file}"; fi
  cat "\${staged_key}" >> "\${keys_file}"
  printf '\n' >> "\${keys_file}"
fi
chown system:shell "\${keys_file}" 2>/dev/null || true
chmod 0640 "\${keys_file}"
restorecon "\${keys_file}" 2>/dev/null || true
rm -f "\${staged_key}"
REMOTE
}

deploy_pixel() {
  local mirror_conf="${PIXEL_MIRROR_ROOT}/data/local/pixel-stack/conf"
  pixel_deploy_target_args
  if (( PIXEL_PLATFORM_BOOTSTRAP == 1 )); then
    ORCHESTRATOR_CONFIG_FILE="${mirror_conf}/orchestrator-config-v1.json" \
    SSH_PUBLIC_KEY_FILE="${mirror_conf}/ssh/authorized_keys" \
    SSH_PASSWORD_HASH_FILE="${mirror_conf}/ssh/root_password.hash" \
    VPN_AUTH_KEY_FILE="${mirror_conf}/vpn/tailscale-authkey" \
    PIXEL_RUNTIME_ROOTFS_TARBALL="${PIXEL_ROOTFS_TARBALL}" \
    PIXEL_RUNTIME_DROPBEAR_ARTIFACT_DIR="${PIXEL_DROPBEAR_ARTIFACT_DIR}" \
    PIXEL_RUNTIME_TAILSCALE_BUNDLE="${PIXEL_TAILSCALE_BUNDLE}" \
    JAVA_HOME="${PIXEL_JAVA_HOME}" \
    ANDROID_SDK_ROOT="${PIXEL_ANDROID_SDK_ROOT}" \
      bash "${PIXEL_REPO}/tools/pixel/redeploy.sh" \
        "${PIXEL_DEPLOY_TARGET_ARGS[@]}" \
        --scope platform \
        --profile standard \
        --mode force-bootstrap \
        --rootfs-tarball "${PIXEL_ROOTFS_TARBALL}"
  fi

  stage_pixel_ticket_secrets
  if (( AUTHORIZE_SERVER_ADB_KEY == 1 )); then
    authorize_server_adb_key_on_pixel
  fi

  JAVA_HOME="${PIXEL_JAVA_HOME}" ANDROID_SDK_ROOT="${PIXEL_ANDROID_SDK_ROOT}" \
  bash "${PIXEL_REPO}/orchestrator/scripts/android/deploy_orchestrator_apk.sh" \
    "${PIXEL_DEPLOY_TARGET_ARGS[@]}" \
    --profile standard \
    --action redeploy_component \
    --component ticket_screen \
    --enable-ticket-service

  bash "${PIXEL_REPO}/tools/pixel/ticket_first_setup.sh" "${PIXEL_DEPLOY_TARGET_ARGS[@]}"
  bash "${PIXEL_REPO}/orchestrator/scripts/android/deploy_orchestrator_apk.sh" \
    "${PIXEL_DEPLOY_TARGET_ARGS[@]}" \
    --profile fast \
    --skip-build \
    --action health_component \
    --component ticket_screen
  verify_pixel_reboot_survival_proxy
}

verify_pixel_reboot_survival_proxy() {
  pixel_transport_root_shell 'set -eu
prefs=/data/user/0/lv.jolkins.pixelorchestrator/shared_prefs/ticket_service_settings.xml
test -f "${prefs}"
grep -Eq "<boolean[[:space:]]+name=\"ticket_service_enabled\"[[:space:]]+value=\"true\"" "${prefs}"
dumpsys package lv.jolkins.pixelorchestrator | grep -q android.intent.action.BOOT_COMPLETED
sh /data/local/pixel-stack/bin/pixel-ticket-health.sh >/dev/null'
}

server_current_link_exists() {
  server_ssh "test -e /etc/arbuzas/current -o -L /etc/arbuzas/current"
}

cleanup_failed_first_server_deploy() {
  local expected_release_id="$1"
  local remote_arbuzas_root="/etc/arbuzas"
  [[ "${expected_release_id}" =~ ^[A-Za-z0-9._-]+$ ]] || die "unsafe cleanup release id"
  if [[ "${TICKET_BOOTSTRAP_LIBRARY_ONLY:-0}" == "1" && -n "${TICKET_BOOTSTRAP_TEST_REMOTE_ROOT:-}" ]]; then
    remote_arbuzas_root="${TICKET_BOOTSTRAP_TEST_REMOTE_ROOT}"
  fi
  [[ "${remote_arbuzas_root}" == /* && "${remote_arbuzas_root}" =~ ^[A-Za-z0-9._/-]+$ ]] ||
    die "unsafe cleanup root"
  server_ssh "if [ \"\$(id -u)\" -eq 0 ]; then bash -s -- '${expected_release_id}' '${remote_arbuzas_root}'; else sudo -n bash -s -- '${expected_release_id}' '${remote_arbuzas_root}'; fi" <<'REMOTE'
set -u
arbuzas_root="$(cd "$2" && pwd -P)"
current_link="${arbuzas_root}/current"
expected_target="${arbuzas_root}/releases/$1"
services='train_bot train_tunnel ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_hdr_transformer ticket_remote ticket_remote_tunnel'
current_before="$(readlink -f "${current_link}" 2>/dev/null || true)"
if [[ -n "${current_before}" && "${current_before}" != "${expected_target}" ]]; then
  echo "first-install cleanup refused unexpected current target: ${current_before}" >&2
  exit 1
fi
if [[ ! -d "${expected_target}" ]]; then
  echo "first-install cleanup found no uploaded failed release: ${expected_target}"
  exit 0
fi

cleanup_rc=0
for service in ${services}; do
  current_during="$(readlink -f "${current_link}" 2>/dev/null || true)"
  if [[ -n "${current_during}" && "${current_during}" != "${expected_target}" ]]; then
    echo "current release changed during first-install cleanup; refusing container removal" >&2
    exit 1
  fi
  ids="$(docker ps -aq \
    --filter 'label=com.docker.compose.project=arbuzas' \
    --filter "label=com.docker.compose.service=${service}" 2>/dev/null || true)"
  if [[ -n "${ids}" ]]; then
    docker stop -t 10 ${ids} >/dev/null 2>&1 || cleanup_rc=1
    docker rm -f ${ids} >/dev/null 2>&1 || cleanup_rc=1
  fi
done

current_after="$(readlink -f "${current_link}" 2>/dev/null || true)"
if [[ -n "${current_after}" && "${current_after}" != "${expected_target}" ]]; then
  echo "current release changed during first-install cleanup; refusing to remove it" >&2
  exit 1
fi
if [[ "${current_after}" == "${expected_target}" ]]; then
  rm -f "${current_link}"
fi
echo "failed first release preserved for diagnosis: ${expected_target}"
exit "${cleanup_rc}"
REMOTE
}

run_canonical_server_deploy() {
  ARBUZAS_HOST_MIRROR_PROFILE="${TICKET_MIRROR_PROFILE}" \
  ARBUZAS_HOST_MIRROR_ROOT="${ACTIVE_SERVER_MIRROR_ROOT}" \
  ARBUZAS_HOST_MIRROR_PRIVILEGED="$((1 - SERVER_REMOTE_IS_ROOT))" \
  ARBUZAS_TICKET_PHONE_ADB_TARGET="${TICKET_ADB_TARGET}" \
    "${OPS_ROOT}/tools/arbuzas/deploy.sh" deploy \
      --services "${TICKET_SERVICES}" \
      --validation-profile full \
      --release-id "${SERVER_RELEASE_ID}" \
      "${SERVER_DEPLOY_TARGET_ARGS[@]}"
}

run_server_deploy_with_first_install_cleanup() {
  local had_current_link=0
  local deploy_rc=0
  SERVER_RELEASE_ID="ticket-recovery-$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}"
  if server_current_link_exists; then
    had_current_link=1
  fi
  if run_canonical_server_deploy; then
    return 0
  else
    deploy_rc=$?
  fi
  if (( had_current_link == 0 )); then
    echo "First Ticket install failed; removing only its selected containers and current link" >&2
    cleanup_failed_first_server_deploy "${SERVER_RELEASE_ID}" ||
      echo "WARNING: bounded first-install cleanup was incomplete; failed release artifacts were retained" >&2
  fi
  return "${deploy_rc}"
}

deploy_server() {
  server_deploy_target_args
  if (( INSTALL_SERVER_PREREQUISITES == 1 )); then
    install_server_prerequisites
  fi
  preflight_server_runtime
  require_canonical_server_deploy_privilege

  SERVER_TARGET_CONFIG_EMPTY=0
  if server_mirror_is_empty; then
    SERVER_TARGET_CONFIG_EMPTY=1
    (( RESTORE_EMPTY_SERVER == 1 )) ||
      die "server mirror target is empty; pass --restore-empty-server to authorize the initial restore"
  fi
  prepare_server_recovery_mirror
  audit_server_mirror
  preflight_server_phone_route

  run_server_deploy_with_first_install_cleanup
  preflight_server_oidc_issuer
}

print_plan() {
  cat <<EOF
mode=plan
changes=none
server_target=${SERVER_HOST:-missing}
server_user=${SERVER_USER:-missing}
server_host_key=$([[ -n "${SERVER_HOST_KEY_SHA256}" ]] && printf configured || printf existing-known-host-required)
server_mirror=$([[ -f "${SERVER_MIRROR_ROOT}/.host-mirror-manifest.json" ]] && printf ready || printf missing)
server_recovery_mirror=${SERVER_RECOVERY_MIRROR_ROOT:-per-target-default}
ticket_adb_target=${TICKET_ADB_TARGET:-missing}
pixel_transport=${PIXEL_TRANSPORT:-missing}
pixel_target=$([[ "${PIXEL_TRANSPORT}" == "adb" ]] && printf '%s' "${PIXEL_DEVICE:-missing}" || printf '%s' "${PIXEL_SSH_HOST:-missing}")
pixel_token=$([[ -s "${PIXEL_TOKEN_FILE}" ]] && printf configured || printf missing)
pixel_vivi_login=$([[ -s "${PIXEL_VIVI_LOGIN_FILE}" ]] && printf configured || printf missing)
pixel_platform_bootstrap=$([[ "${PIXEL_PLATFORM_BOOTSTRAP}" == "1" ]] && printf planned || printf existing-required)
server_services=${TICKET_SERVICES}
order=pixel_then_server

No target was probed or changed. Run preflight with both explicit targets, then
run all with --execute. Add --install-server-prerequisites and
--restore-empty-server only for a reviewed clean VPS.
EOF
}

mode_needs_server() {
  [[ "${MODE}" == "preflight" || "${MODE}" == "server" || "${MODE}" == "all" ]]
}

mode_needs_pixel() {
  [[ "${MODE}" == "preflight" || "${MODE}" == "pixel" || "${MODE}" == "all" ]]
}

if (( $# > 0 )); then
  case "$1" in
    plan|preflight|server|pixel|all)
      MODE="$1"
      shift
      ;;
  esac
fi

while (( $# > 0 )); do
  case "$1" in
    --execute) EXECUTE=1 ;;
    --install-server-prerequisites) INSTALL_SERVER_PREREQUISITES=1 ;;
    --restore-empty-server) RESTORE_EMPTY_SERVER=1 ;;
    --replace-pixel-token) REPLACE_PIXEL_TOKEN=1 ;;
    --replace-pixel-vivi-login) REPLACE_PIXEL_VIVI_LOGIN=1 ;;
    --authorize-server-adb-key) AUTHORIZE_SERVER_ADB_KEY=1 ;;
    --server-host) shift; SERVER_HOST="${1:-}" ;;
    --server-user) shift; SERVER_USER="${1:-}" ;;
    --server-port) shift; SERVER_PORT="${1:-}" ;;
    --server-host-key-sha256) shift; SERVER_HOST_KEY_SHA256="${1:-}" ;;
    --server-mirror-root) shift; SERVER_MIRROR_ROOT="${1:-}" ;;
    --server-recovery-mirror-root) shift; SERVER_RECOVERY_MIRROR_ROOT="${1:-}" ;;
    --ticket-adb-target) shift; TICKET_ADB_TARGET="${1:-}" ;;
    --allow-nonprivate-adb-target) ALLOW_NONPRIVATE_ADB_TARGET=1 ;;
    --pixel-repo) shift; PIXEL_REPO="${1:-}" ;;
    --pixel-mirror-root) shift; PIXEL_MIRROR_ROOT="${1:-}" ;;
    --pixel-token-file) shift; PIXEL_TOKEN_FILE="${1:-}" ;;
    --pixel-vivi-login-file) shift; PIXEL_VIVI_LOGIN_FILE="${1:-}" ;;
    --pixel-transport) shift; PIXEL_TRANSPORT="${1:-}" ;;
    --pixel-device) shift; PIXEL_DEVICE="${1:-}" ;;
    --pixel-ssh-host) shift; PIXEL_SSH_HOST="${1:-}" ;;
    --pixel-ssh-port) shift; PIXEL_SSH_PORT="${1:-}" ;;
    --pixel-platform-bootstrap) PIXEL_PLATFORM_BOOTSTRAP=1 ;;
    --pixel-rootfs-tarball) shift; PIXEL_ROOTFS_TARBALL="${1:-}" ;;
    --pixel-dropbear-artifact-dir) shift; PIXEL_DROPBEAR_ARTIFACT_DIR="${1:-}" ;;
    --pixel-tailscale-bundle) shift; PIXEL_TAILSCALE_BUNDLE="${1:-}" ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

[[ -n "${SERVER_MIRROR_ROOT}" ]] || SERVER_MIRROR_ROOT="${OPS_ROOT}/infra/arbuzas/host-mirror"
ACTIVE_SERVER_MIRROR_ROOT="${SERVER_MIRROR_ROOT}"
[[ -n "${PIXEL_REPO}" ]] || PIXEL_REPO="$(cd "${OPS_ROOT}/../pixel-phone" 2>/dev/null && pwd || true)"
[[ -n "${PIXEL_MIRROR_ROOT}" ]] || PIXEL_MIRROR_ROOT="${PIXEL_REPO}/host-mirror"
[[ -n "${PIXEL_TOKEN_FILE}" ]] || PIXEL_TOKEN_FILE="${PIXEL_MIRROR_ROOT}/data/local/pixel-stack/conf/apps/ticket-screen-spacetime-token"
[[ -n "${PIXEL_VIVI_LOGIN_FILE}" ]] || PIXEL_VIVI_LOGIN_FILE="${PIXEL_MIRROR_ROOT}/data/local/pixel-stack/conf/apps/ticket-screen-vivi-login.env"

if [[ "${TICKET_BOOTSTRAP_LIBRARY_ONLY:-0}" == "1" ]]; then
  return 0 2>/dev/null || exit 0
fi

if [[ "${MODE}" == "plan" ]]; then
  print_plan
  exit 0
fi

if [[ "${MODE}" == "server" || "${MODE}" == "pixel" || "${MODE}" == "all" ]]; then
  (( EXECUTE == 1 )) || die "${MODE} changes a target; rerun with --execute after reviewing plan and preflight"
fi

trap cleanup_server_working_mirror EXIT

if mode_needs_server; then
  validate_server_options
  prepare_server_known_hosts
  require_cmd ssh
  require_cmd scp
  require_cmd tar
  require_cmd python3
  require_file "${OPS_ROOT}/tools/arbuzas/deploy.sh"
  preflight_server_mirror
  preflight_server_access
  if (( INSTALL_SERVER_PREREQUISITES == 1 )); then
    preflight_server_prerequisite_install
  fi
fi

if mode_needs_pixel; then
  validate_pixel_options
  preflight_pixel_local
  preflight_pixel_platform_inputs
  preflight_pixel_device
fi

if [[ "${MODE}" == "preflight" ]]; then
  if (( INSTALL_SERVER_PREREQUISITES == 0 )); then
    preflight_server_runtime
  fi
  SERVER_TARGET_CONFIG_EMPTY=0
  if server_mirror_is_empty; then
    SERVER_TARGET_CONFIG_EMPTY=1
    (( RESTORE_EMPTY_SERVER == 1 )) ||
      die "server config is empty; preflight requires --restore-empty-server for the planned recovery"
  fi
  if (( INSTALL_SERVER_PREREQUISITES == 1 )); then
    echo "recovery_mirror_check=deferred_until_server_prerequisites_install"
  else
    prepare_server_recovery_mirror
    audit_server_mirror
  fi
  if (( PIXEL_PLATFORM_BOOTSTRAP == 1 )); then
    echo "phone_route_check=deferred_until_platform_bootstrap"
  else
    preflight_server_phone_route
  fi
  if (( INSTALL_SERVER_PREREQUISITES == 1 )) || server_mirror_is_empty; then
    echo "oidc_issuer_check=deferred_until_train_dependency_deploy"
  else
    preflight_server_oidc_issuer
  fi
  echo "preflight=pass"
  exit 0
fi

case "${MODE}" in
  pixel)
    deploy_pixel
    ;;
  server)
    deploy_server
    ;;
  all)
    deploy_pixel
    deploy_server
    ;;
esac

echo "ticket_bootstrap=installation_and_health_complete"
echo "external_acceptance=authenticated_browser_two_cycle_and_reboot_proof_required"
