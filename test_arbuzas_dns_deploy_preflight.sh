#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_PATH="${REPO_ROOT}/tools/arbuzas/deploy.sh"

if [[ ! -f "${SCRIPT_PATH}" ]]; then
  echo "FAIL: missing Arbuzas deploy script at ${SCRIPT_PATH}" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
fakebin="${tmpdir}/fakebin"
mkdir -p "${fakebin}"

cleanup() {
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

cat > "${tmpdir}/arbuzas.env" <<'EOF_ENV'
ARBUZAS_DNS_ADMIN_LAN_IP=192.168.32.22
EOF_ENV

real_base64="$(command -v base64)"

cat > "${fakebin}/ssh" <<'EOF_SSH'
#!/usr/bin/env bash
set -euo pipefail

log_dir="${FAKE_SSH_LOG_DIR:?}"
scenario="${PRETEND_SCENARIO:?}"
real_base64="${REAL_BASE64:?}"
last_arg="${!#}"
script_content=""

if [[ "${last_arg}" == *"base64 -d | bash -s"* ]]; then
  encoded="$(printf '%s\n' "${last_arg}" | sed -n "s/.*printf '%s' '\\([^']*\\)'.*/\\1/p")"
  script_content="$(printf '%s' "${encoded}" | "${real_base64}" -d)"
elif [[ "${last_arg}" == *"bash -s"* ]]; then
  script_content="$(cat)"
else
  script_content="${last_arg}"
  cat >/dev/null || true
fi

printf '%s\n---\n' "${script_content}" >> "${log_dir}/scripts.log"

if [[ "${script_content}" == *"DNS_SAFE_REPAIR_COMMAND"* ]]; then
  case "${scenario}" in
    broken_443)
      echo "DNS host preflight failed on Arbuzas; fix the listener conflict before retrying." >&2
      echo "- conflicting host listener on 443: LISTEN 0 128 100.64.0.10:443 0.0.0.0:* users:((\"tailscaled\",pid=99,fd=9))" >&2
      echo "Safe repair: ARBUZAS_HOST='arbuzas' ARBUZAS_USER='${USER:-tester}' ARBUZAS_SSH_PORT='22' ARBUZAS_DNS_ADMIN_LAN_IP='192.168.32.22' bash tools/arbuzas/deploy.sh repair-dns-admin" >&2
      exit 1
      ;;
    conflict_853)
      echo "DNS host preflight failed on Arbuzas; fix the listener conflict before retrying." >&2
      echo "- conflicting Docker publisher: rogue-dns|0.0.0.0:853->853/tcp" >&2
      echo "Safe repair only applies to stale private DNS admin forwarding. If this is a different service, free the port manually and retry." >&2
      exit 1
      ;;
    healthy)
      exit 0
      ;;
    *)
      echo "unknown fake ssh scenario: ${scenario}" >&2
      exit 1
      ;;
  esac
fi

if [[ "${script_content}" == *"--- tailscale serve status ---"* ]]; then
  echo "--- tailscale serve status ---" >&2
  case "${scenario}" in
    broken_443)
      echo "https://arbuzas.tailnet.ts.net (TLS over TCP 443) proxying to 127.0.0.1:8097" >&2
      echo "--- dns host listeners ---" >&2
      echo "LISTEN 0 128 100.64.0.10:443 0.0.0.0:* users:((\"tailscaled\",pid=99,fd=9))" >&2
      ;;
    conflict_853)
      echo "No serve config" >&2
      echo "--- dns host listeners ---" >&2
      echo "LISTEN 0 128 0.0.0.0:853 0.0.0.0:* users:((\"rogue\",pid=55,fd=8))" >&2
      ;;
    healthy)
      echo "No serve config" >&2
      echo "--- dns host listeners ---" >&2
      echo "LISTEN 0 128 127.0.0.1:8097 0.0.0.0:* users:((\"docker-proxy\",pid=77,fd=7))" >&2
      ;;
  esac
  exit 0
fi

if [[ "${script_content}" == *"compose stop dns_controlplane"* ]]; then
  echo "compose stop dns_controlplane" >> "${log_dir}/cutover.log"
fi

exit 0
EOF_SSH
chmod +x "${fakebin}/ssh"

run_compact() {
  local scenario="$1"
  local stdout_file="$2"
  local stderr_file="$3"

  PRETEND_SCENARIO="${scenario}" \
  FAKE_SSH_LOG_DIR="${tmpdir}" \
  REAL_BASE64="${real_base64}" \
  PATH="${fakebin}:${PATH}" \
  bash "${SCRIPT_PATH}" compact-dns-db --env-file "${tmpdir}/arbuzas.env" \
    >"${stdout_file}" 2>"${stderr_file}"
}

if run_compact broken_443 "${tmpdir}/broken.out" "${tmpdir}/broken.err"; then
  echo "FAIL: compact-dns-db should fail fast when a stale Tailscale HTTPS forward owns port 443" >&2
  exit 1
fi

if ! cat "${tmpdir}/broken.out" "${tmpdir}/broken.err" | grep -F "repair-dns-admin" >/dev/null; then
  echo "FAIL: broken 443 preflight did not point the operator at repair-dns-admin" >&2
  exit 1
fi

if [[ -f "${tmpdir}/cutover.log" ]]; then
  echo "FAIL: broken 443 preflight still reached the DNS cutover path" >&2
  exit 1
fi

rm -f "${tmpdir}/scripts.log" "${tmpdir}/cutover.log"

if run_compact conflict_853 "${tmpdir}/conflict.out" "${tmpdir}/conflict.err"; then
  echo "FAIL: compact-dns-db should fail fast when another service owns port 853" >&2
  exit 1
fi

if ! cat "${tmpdir}/conflict.out" "${tmpdir}/conflict.err" | grep -F "rogue-dns|0.0.0.0:853->853/tcp" >/dev/null; then
  echo "FAIL: conflicting 853 preflight did not report the owning listener" >&2
  exit 1
fi

if [[ -f "${tmpdir}/cutover.log" ]]; then
  echo "FAIL: conflicting 853 preflight still reached the DNS cutover path" >&2
  exit 1
fi

rm -f "${tmpdir}/scripts.log" "${tmpdir}/cutover.log"

if ! run_compact healthy "${tmpdir}/healthy.out" "${tmpdir}/healthy.err"; then
  echo "FAIL: healthy DNS preflight should allow compact-dns-db to continue" >&2
  cat "${tmpdir}/healthy.err" >&2 || true
  exit 1
fi

if [[ ! -f "${tmpdir}/cutover.log" ]] || ! grep -F "compose stop dns_controlplane" "${tmpdir}/cutover.log" >/dev/null; then
  echo "FAIL: healthy DNS preflight did not reach the compact cutover path" >&2
  exit 1
fi

echo "PASS: Arbuzas DNS deploy preflight fails early on listener conflicts and allows healthy cutovers through"
