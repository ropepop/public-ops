#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT}/tools/ticket/bootstrap.sh"
SERVER_INSTALLER="${ROOT}/tools/ticket/install_server_prerequisites.sh"
PIXEL_ROOT="$(cd "${ROOT}/../pixel-phone" && pwd)"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

[[ -x "${SCRIPT}" ]] || fail "bootstrap entrypoint is not executable"
[[ -x "${SERVER_INSTALLER}" ]] || fail "server prerequisite helper is not executable"
bash -n "${SCRIPT}"
bash -n "${SERVER_INSTALLER}"

plan_output="$(${SCRIPT})"
grep -Fxq 'mode=plan' <<<"${plan_output}" || fail "default mode is not plan"
grep -Fxq 'changes=none' <<<"${plan_output}" || fail "default plan does not state that it is read-only"

help_output="$(${SCRIPT} --help)"
for required in \
  'plan|preflight|server|pixel|all' \
  '--execute' \
  '--server-host-key-sha256' \
  '--server-recovery-mirror-root' \
  '--restore-empty-server' \
  '--pixel-token-file' \
  '--pixel-vivi-login-file' \
  '--pixel-platform-bootstrap' \
  '--authorize-server-adb-key'
do
  grep -Fq -- "${required}" <<<"${help_output}" || fail "help is missing ${required}"
done

fake_bin="$(mktemp -d)"
ssh_marker="${fake_bin}/ssh-called"
trap 'rm -rf "${fake_bin}"' EXIT
printf '#!/usr/bin/env bash\ntouch %q\nexit 99\n' "${ssh_marker}" > "${fake_bin}/ssh"
chmod 755 "${fake_bin}/ssh"
if PATH="${fake_bin}:${PATH}" "${SCRIPT}" server \
  --server-host example.invalid \
  --server-user root \
  --ticket-adb-target 100.64.0.2:5555 >/dev/null 2>&1
then
  fail "server mode ran without --execute"
fi
[[ ! -e "${ssh_marker}" ]] || fail "server mode probed the target before requiring --execute"

if "${SCRIPT}" pixel --execute --pixel-transport adb >/dev/null 2>&1; then
  fail "pixel mode accepted no explicit device"
fi

parse_output="$(${SCRIPT} plan \
  --server-host vps.example \
  --server-user root \
  --server-port 22 \
  --server-host-key-sha256 SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA \
  --server-mirror-root /tmp/server-mirror \
  --ticket-adb-target 100.64.0.2:5555 \
  --allow-nonprivate-adb-target \
  --pixel-repo "${PIXEL_ROOT}" \
  --pixel-mirror-root /tmp/pixel-mirror \
  --pixel-token-file /tmp/pixel-token \
  --pixel-vivi-login-file /tmp/pixel-vivi-login \
  --pixel-transport adb \
  --pixel-device clean-pixel \
  --pixel-ssh-host pixel.example \
  --pixel-ssh-port 2222 \
  --install-server-prerequisites \
  --restore-empty-server \
  --replace-pixel-token \
  --replace-pixel-vivi-login \
  --pixel-platform-bootstrap \
  --pixel-rootfs-tarball /tmp/rootfs.tar \
  --pixel-dropbear-artifact-dir /tmp/dropbear \
  --pixel-tailscale-bundle /tmp/tailscale.tar \
  --authorize-server-adb-key)"
grep -Fxq 'changes=none' <<<"${parse_output}" || fail "documented flags are not accepted by plan mode"

for contract in \
  'tools/arbuzas/deploy.sh" deploy' \
  'train_bot,train_tunnel,ticket_phone_bridge,ticket_remote_spacetime_sidecar,ticket_remote,ticket_remote_tunnel' \
  'deploy_orchestrator_apk.sh"' \
  '--action redeploy_component' \
  '--component ticket_screen' \
  'ticket_first_setup.sh"'
do
  grep -Fq -- "${contract}" "${SCRIPT}" || fail "canonical deploy contract is missing ${contract}"
done

for recovery_contract in \
  'TICKET_MIRROR_PROFILE="ticket-recovery"' \
  'ARBUZAS_HOST_MIRROR_PROFILE="${TICKET_MIRROR_PROFILE}"' \
  '--release-id "${SERVER_RELEASE_ID}"' \
  'expected_target="${arbuzas_root}/releases/$1"' \
  'current_before="$(readlink -f "${current_link}" 2>/dev/null || true)"' \
  'current_after="$(readlink -f "${current_link}" 2>/dev/null || true)"'; do
  grep -Fq -- "${recovery_contract}" "${SCRIPT}" || fail "recovery safety contract is missing ${recovery_contract}"
done
if grep -Fq 'rm -rf /etc/arbuzas/releases' "${SCRIPT}"; then
  fail "first-install cleanup deletes retained release artifacts"
fi

if grep -Fq 'StrictHostKeyChecking=accept-new' "${SCRIPT}"; then
  fail "bootstrap silently accepts an unknown VPS host key"
fi
grep -Fq 'StrictHostKeyChecking=yes' "${SCRIPT}" || fail "strict VPS host-key checking is missing"

if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  TICKET_ADB_TARGET=8.8.8.8:5555
  validate_ticket_adb_target_privacy
) >/dev/null 2>&1; then
  fail "public ADB target was accepted without an explicit override"
fi
(
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  TICKET_ADB_TARGET=100.76.50.43:5555
  validate_ticket_adb_target_privacy
) || fail "CGNAT/Tailscale ADB target was rejected"

missing_hash="$( (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  pixel_transport_remote_sha256_file() { printf 'MISSING\n'; }
  pixel_remote_secret_hash_or_absent /missing token
) 2>/dev/null)"
[[ -z "${missing_hash}" ]] || fail "a missing clean-device secret was not treated as safe absence"
if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  pixel_transport_remote_sha256_file() { printf 'UNKNOWN\n'; }
  pixel_remote_secret_hash_or_absent /unknown token
) >/dev/null 2>&1; then
  fail "an unknown clean-device secret state did not fail closed"
fi

host_key_dir="$(mktemp -d)"
ssh-keygen -q -t ed25519 -N '' -f "${host_key_dir}/key"
ssh-keygen -q -t ed25519 -N '' -f "${host_key_dir}/other-key"
awk -v host='vps.example' '{print host, $1, $2}' "${host_key_dir}/key.pub" > "${host_key_dir}/scan"
awk -v host='vps.example' '{print host, $1, $2}' "${host_key_dir}/other-key.pub" >> "${host_key_dir}/scan"
expected_fingerprint="$(ssh-keygen -lf "${host_key_dir}/scan" -E sha256 | awk 'NR==1 {print $2}')"
cat > "${host_key_dir}/ssh-keyscan" <<'SH'
#!/usr/bin/env bash
cat "${FAKE_HOST_KEY_SCAN}"
SH
chmod 755 "${host_key_dir}/ssh-keyscan"
(
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  SERVER_HOST=vps.example
  SERVER_HOST_KEY_SHA256="${expected_fingerprint}"
  PATH="${host_key_dir}:${PATH}"
  export PATH FAKE_HOST_KEY_SCAN="${host_key_dir}/scan"
  prepare_server_known_hosts
  [[ -s "${SERVER_KNOWN_HOSTS_FILE}" ]]
  [[ "$(wc -l < "${SERVER_KNOWN_HOSTS_FILE}" | tr -d '[:space:]')" == "1" ]]
  ! grep -Fq "$(awk '{print $2}' "${host_key_dir}/other-key.pub")" "${SERVER_KNOWN_HOSTS_FILE}"
  cleanup_server_working_mirror
) || fail "an exact VPS host-key fingerprint was not accepted"
if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  SERVER_HOST=vps.example
  SERVER_HOST_KEY_SHA256='SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA'
  PATH="${host_key_dir}:${PATH}"
  export PATH FAKE_HOST_KEY_SCAN="${host_key_dir}/scan"
  prepare_server_known_hosts
) >/dev/null 2>&1; then
  fail "a mismatched VPS host-key fingerprint was accepted"
fi
rm -rf "${host_key_dir}"

adb_pair_dir="$(mktemp -d)"
mkdir -p "${adb_pair_dir}/mirror/etc/arbuzas/secrets/android-adb" "${adb_pair_dir}/bin"
printf 'private-placeholder\n' > "${adb_pair_dir}/mirror/etc/arbuzas/secrets/android-adb/adbkey"
printf 'matching-public comment\n' > "${adb_pair_dir}/mirror/etc/arbuzas/secrets/android-adb/adbkey.pub"
chmod 600 "${adb_pair_dir}/mirror/etc/arbuzas/secrets/android-adb/adbkey"
cat > "${adb_pair_dir}/bin/adb" <<'SH'
#!/usr/bin/env bash
printf 'matching-public generated\n'
SH
chmod 755 "${adb_pair_dir}/bin/adb"
(
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  SERVER_MIRROR_ROOT="${adb_pair_dir}/mirror"
  PATH="${adb_pair_dir}/bin:${PATH}"
  validate_server_adb_key_pair
) || fail "matching server ADB key pair was rejected"
printf 'different-public comment\n' > "${adb_pair_dir}/mirror/etc/arbuzas/secrets/android-adb/adbkey.pub"
if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  SERVER_MIRROR_ROOT="${adb_pair_dir}/mirror"
  PATH="${adb_pair_dir}/bin:${PATH}"
  validate_server_adb_key_pair
) >/dev/null 2>&1; then
  fail "mismatched server ADB key pair was accepted"
fi
rm -rf "${adb_pair_dir}"

python3 - "${SCRIPT}" <<'PY'
import pathlib
import sys

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
mkdir_pos = source.index("pixel_transport_root_exec mkdir -p /data/local/pixel-stack/conf/apps")
push_pos = source.index('pixel_transport_push "${local_path}" "${remote_path}"')
if mkdir_pos > push_pos:
    raise SystemExit("clean Pixel token parent is not created before token staging")
if "as_root()" not in source or "[ \"$(id -u)\" -eq 0 ]" not in source:
    raise SystemExit("clean root VPS fallback is missing")
access_block = source.split("preflight_server_access() {", 1)[1].split("preflight_server_runtime() {", 1)[0]
runtime_block = source.split("preflight_server_runtime() {", 1)[1].split("server_mirror_is_empty() {", 1)[0]
if "command -v tar" in access_block or "command -v tar" not in runtime_block:
    raise SystemExit("tar must be validated after the optional prerequisite install, not before it")
mirror_block = source.split("prepare_server_recovery_mirror() {", 1)[1].split("ticket_oidc_issuer() {", 1)[0]
full_audit = mirror_block.index('ARBUZAS_HOST_MIRROR_PROFILE="arbuzas"')
narrow_pull = mirror_block.index('ARBUZAS_HOST_MIRROR_PROFILE="${TICKET_MIRROR_PROFILE}"')
if full_audit > narrow_pull or 'SERVER_TARGET_CONFIG_EMPTY == 0' not in mirror_block:
    raise SystemExit("new nonempty recovery mirrors do not audit the authoritative full mirror first")
deploy_block = source.split("deploy_server() {", 1)[1].split("print_plan() {", 1)[0]
if deploy_block.index("prepare_server_recovery_mirror") > deploy_block.index("audit_server_mirror"):
    raise SystemExit("server deploy does not consistently select the persistent recovery mirror before audit")
PY

cleanup_marker="$(mktemp)"
if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  server_current_link_exists() { return 1; }
  run_canonical_server_deploy() { [[ "${SERVER_RELEASE_ID}" == ticket-recovery-* ]]; return 27; }
  cleanup_failed_first_server_deploy() {
    [[ "$1" == "${SERVER_RELEASE_ID}" ]]
    printf '%s\n' "$1" > "${cleanup_marker}"
  }
  run_server_deploy_with_first_install_cleanup
) >/dev/null 2>&1; then
  fail "failed first install unexpectedly returned success"
else
  cleanup_rc=$?
fi
[[ "${cleanup_rc}" == "27" ]] || fail "first-install cleanup did not preserve the deploy failure status"
grep -Eq '^ticket-recovery-[A-Za-z0-9._-]+$' "${cleanup_marker}" || fail "cleanup did not receive the explicit failed release id"

rm -f "${cleanup_marker}"
if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  server_current_link_exists() { return 0; }
  run_canonical_server_deploy() { return 28; }
  cleanup_failed_first_server_deploy() { touch "${cleanup_marker}"; }
  run_server_deploy_with_first_install_cleanup
) >/dev/null 2>&1; then
  fail "failed deploy over an existing release unexpectedly returned success"
else
  existing_rc=$?
fi
[[ "${existing_rc}" == "28" ]] || fail "existing-release failure status was not preserved"
[[ ! -e "${cleanup_marker}" ]] || fail "existing release triggered first-install cleanup"

fake_cleanup_root="$(mktemp -d)"
mkdir -p "${fake_cleanup_root}/releases/expected-release" "${fake_cleanup_root}/releases/other-release" "${fake_cleanup_root}/bin"
ln -s "${fake_cleanup_root}/releases/expected-release" "${fake_cleanup_root}/current"
cat > "${fake_cleanup_root}/bin/docker" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${FAKE_DOCKER_LOG}"
if [[ "${1:-}" == "ps" ]]; then printf 'fake-container-id\n'; fi
SH
chmod 755 "${fake_cleanup_root}/bin/docker"
fake_docker_log="${fake_cleanup_root}/docker.log"
(
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  SERVER_RELEASE_ID=expected-release
  TICKET_BOOTSTRAP_TEST_REMOTE_ROOT="${fake_cleanup_root}"
  server_ssh() {
    PATH="${fake_cleanup_root}/bin:${PATH}" FAKE_DOCKER_LOG="${fake_docker_log}" \
      bash -s -- "${SERVER_RELEASE_ID}" "${TICKET_BOOTSTRAP_TEST_REMOTE_ROOT}"
  }
  cleanup_failed_first_server_deploy "${SERVER_RELEASE_ID}"
) >/dev/null || fail "fake-host cleanup rejected its exact expected failed release"
[[ ! -e "${fake_cleanup_root}/current" ]] || fail "fake-host cleanup left the exact failed current link"
[[ -d "${fake_cleanup_root}/releases/expected-release" ]] || fail "fake-host cleanup deleted failed release artifacts"
for service in train_bot train_tunnel ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel; do
  grep -Fq "label=com.docker.compose.service=${service}" "${fake_docker_log}" ||
    fail "fake-host cleanup missed selected service ${service}"
done

: > "${fake_docker_log}"
ln -s "${fake_cleanup_root}/releases/other-release" "${fake_cleanup_root}/current"
if (
  set --
  TICKET_BOOTSTRAP_LIBRARY_ONLY=1 source "${SCRIPT}"
  SERVER_RELEASE_ID=expected-release
  TICKET_BOOTSTRAP_TEST_REMOTE_ROOT="${fake_cleanup_root}"
  server_ssh() {
    PATH="${fake_cleanup_root}/bin:${PATH}" FAKE_DOCKER_LOG="${fake_docker_log}" \
      bash -s -- "${SERVER_RELEASE_ID}" "${TICKET_BOOTSTRAP_TEST_REMOTE_ROOT}"
  }
  cleanup_failed_first_server_deploy "${SERVER_RELEASE_ID}"
) >/dev/null 2>&1; then
  fail "fake-host cleanup accepted a current link pointing to another release"
fi
[[ -L "${fake_cleanup_root}/current" ]] || fail "fake-host cleanup removed another release link"
[[ ! -s "${fake_docker_log}" ]] || fail "fake-host cleanup touched containers for another current release"
rm -rf "${fake_cleanup_root}"

PIXEL_DEPLOY="${PIXEL_ROOT}/orchestrator/scripts/android/deploy_orchestrator_apk.sh"
PIXEL_FACADE="${PIXEL_ROOT}/orchestrator/android-orchestrator/app/src/main/java/lv/jolkins/pixelorchestrator/app/OrchestratorFacade.kt"
grep -Fq 'install_reason="package_missing"' "${PIXEL_DEPLOY}" || fail "canonical Pixel deployer does not install a missing APK"
grep -Fq 'ddns|ticket_screen) return 1' "${PIXEL_DEPLOY}" || fail "Ticket clean deploy unexpectedly requires an external release manifest"
grep -Fq 'runtimeInstaller.syncBundledRuntimeAssets(assetProvider, component = spec.runtimeAssetComponent)' "${PIXEL_FACADE}" ||
  fail "Ticket redeploy does not sync bundled runtime assets"
grep -Fq '"ticket_screen" -> RedeploySpec(' "${PIXEL_FACADE}" || fail "Ticket redeploy spec is missing"
grep -Fq -- '--scope platform' "${SCRIPT}" || fail "clean Pixel platform bootstrap is missing"
grep -Fq -- '--mode force-bootstrap' "${SCRIPT}" || fail "clean Pixel force-bootstrap mode is missing"
grep -Fq 'ticket-screen-vivi-login.env' "${SCRIPT}" || fail "ViVi recovery credentials are not staged"
grep -Fq -- '--enable-ticket-service' "${SCRIPT}" || fail "Ticket reboot persistence is not enabled"
grep -Fq 'ticket_service_enabled' "${SCRIPT}" || fail "Ticket reboot-persistence proxy is not verified"
grep -Fq 'adb pubkey' "${SCRIPT}" || fail "server ADB key pair is not validated"
grep -Fq 'platforms/android-35/android.jar' "${SCRIPT}" || fail "Android SDK 35 preflight is missing"
grep -Fq 'aarch64-linux-android29-clang' "${SCRIPT}" || fail "Android NDK preflight is missing"
grep -Fq -- '--ssh-known-hosts-file "${SERVER_KNOWN_HOSTS_FILE}"' "${SCRIPT}" ||
  fail "verified VPS host key is not forwarded to the canonical deployer"

package_test_dir="$(mktemp -d)"
mkdir -p "${package_test_dir}/bin"
cat > "${package_test_dir}/ubuntu-os-release" <<'EOF'
ID=ubuntu
ID_LIKE=debian
EOF
cat > "${package_test_dir}/unsupported-os-release" <<'EOF'
ID=fedora
EOF
cat > "${package_test_dir}/bin/id" <<'SH'
#!/usr/bin/env bash
if [[ "${1:-}" == "-u" ]]; then printf '1000\n'; else printf 'deploy\n'; fi
SH
cat > "${package_test_dir}/bin/sudo" <<'SH'
#!/usr/bin/env bash
[[ "${1:-}" == "-n" ]] && shift
[[ "${1:-}" == "true" ]] && exit 0
exec "$@"
SH
cat > "${package_test_dir}/bin/apt-cache" <<'SH'
#!/usr/bin/env bash
[[ "${1:-}" == "show" ]] || exit 2
case "${2:-}" in
  docker.io) exit 0 ;;
  docker-compose-v2) [[ "${FAKE_COMPOSE_PACKAGE:-}" == "v2" ]] ;;
  docker-compose-plugin) [[ "${FAKE_COMPOSE_PACKAGE:-}" == "plugin" ]] ;;
  *) exit 1 ;;
esac
SH
cat > "${package_test_dir}/bin/apt-get" <<'SH'
#!/usr/bin/env bash
printf 'apt-get %s\n' "$*" >> "${FAKE_MUTATION_LOG}"
SH
cat > "${package_test_dir}/bin/systemctl" <<'SH'
#!/usr/bin/env bash
printf 'systemctl %s\n' "$*" >> "${FAKE_MUTATION_LOG}"
SH
cat > "${package_test_dir}/bin/usermod" <<'SH'
#!/usr/bin/env bash
printf 'usermod %s\n' "$*" >> "${FAKE_MUTATION_LOG}"
SH
chmod 755 "${package_test_dir}/bin/"*

mutation_log="${package_test_dir}/mutations.log"
if PATH="${package_test_dir}/bin:${PATH}" \
  TICKET_BOOTSTRAP_OS_RELEASE_FILE="${package_test_dir}/unsupported-os-release" \
  FAKE_COMPOSE_PACKAGE=v2 FAKE_MUTATION_LOG="${mutation_log}" \
  "${SERVER_INSTALLER}" install >/dev/null 2>&1
then
  fail "unsupported VPS OS was accepted"
fi
[[ ! -e "${mutation_log}" ]] || fail "unsupported VPS OS mutated package state"

if PATH="${package_test_dir}/bin:${PATH}" \
  TICKET_BOOTSTRAP_OS_RELEASE_FILE="${package_test_dir}/ubuntu-os-release" \
  FAKE_COMPOSE_PACKAGE=none FAKE_MUTATION_LOG="${mutation_log}" \
  "${SERVER_INSTALLER}" install >/dev/null 2>&1
then
  fail "VPS without a configured Compose v2 package was accepted"
fi
[[ ! -e "${mutation_log}" ]] || fail "missing Compose v2 mutated apt state before failing"

PATH="${package_test_dir}/bin:${PATH}" \
TICKET_BOOTSTRAP_OS_RELEASE_FILE="${package_test_dir}/ubuntu-os-release" \
FAKE_COMPOSE_PACKAGE=plugin FAKE_MUTATION_LOG="${mutation_log}" \
  "${SERVER_INSTALLER}" check >/dev/null
[[ ! -e "${mutation_log}" ]] || fail "read-only package preflight mutated apt state"

PATH="${package_test_dir}/bin:${PATH}" \
TICKET_BOOTSTRAP_OS_RELEASE_FILE="${package_test_dir}/ubuntu-os-release" \
FAKE_COMPOSE_PACKAGE=v2 FAKE_MUTATION_LOG="${mutation_log}" \
  "${SERVER_INSTALLER}" install >/dev/null
grep -Fq 'apt-get update' "${mutation_log}" || fail "supported VPS did not update apt metadata"
grep -Fq 'docker-compose-v2' "${mutation_log}" || fail "supported Compose v2 package was not installed"
rm -rf "${package_test_dir}"

if grep -Eq '(TICKET_REMOTE_SESSION_SIGNING_KEY|SPACETIME_SERVICE_TOKEN)=.[^$]' "${SCRIPT}"; then
  fail "bootstrap script appears to embed a secret value"
fi

echo "PASS: Ticket bootstrap wrapper is default-read-only, explicit, and delegates to canonical deployers"
