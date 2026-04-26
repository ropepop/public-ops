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

release_id="codex-dns-hardening-$$"
release_dir="${REPO_ROOT}/output/arbuzas/releases/${release_id}"

cleanup() {
  rm -rf "${tmpdir}" "${release_dir}"
}
trap cleanup EXIT

cat > "${tmpdir}/arbuzas.env" <<'EOF_ENV'
ARBUZAS_DNS_ADMIN_LAN_IP=192.168.32.22
EOF_ENV

real_base64="$(command -v base64)"
real_curl="$(command -v curl)"
real_python3="$(command -v python3)"

printf 'broken\n' > "${tmpdir}/dns-admin-state"

cat > "${fakebin}/ssh" <<'EOF_SSH'
#!/usr/bin/env bash
set -euo pipefail

log_dir="${FAKE_SSH_LOG_DIR:?}"
state_file="${FAKE_DNS_ADMIN_STATE_FILE:?}"
real_base64="${REAL_BASE64:?}"
last_arg="${!#}"
script_content=""
state="$(cat "${state_file}")"

if [[ "${last_arg}" == *"base64 -d | bash -s"* ]]; then
  encoded="$(printf '%s\n' "${last_arg}" | sed -n "s/.*printf '%s' '\\([^']*\\)'.*/\\1/p")"
  script_content="$(printf '%s' "${encoded}" | "${real_base64}" -d)"
elif [[ "${last_arg}" == *"bash -s"* ]]; then
  script_content="$(cat)"
else
  script_content="${last_arg}"
  cat >/dev/null || true
fi

printf '%s\n---\n' "${script_content}" >> "${log_dir}/ssh.log"

if [[ "${script_content}" == *"readlink '/etc/arbuzas/current' 2>/dev/null"* ]]; then
  printf 'previous-release\n'
  exit 0
fi

if [[ "${script_content}" == *"tailscale ip -4 | head -n 1"* ]]; then
  printf '100.64.0.10\n'
  exit 0
fi

if [[ "${script_content}" == *"tailscale ip -6 | head -n 1"* ]]; then
  printf 'fd7a:115c:a1e0::563a:5a77\n'
  exit 0
fi

if [[ "${script_content}" == *"tailscale', 'status', '--json'"* ]]; then
  if [[ "${script_content}" == *"DNSName"* ]]; then
    printf 'arbuzas.tail9345a.ts.net\n'
  else
    printf 'arbuzas\n'
  fi
  exit 0
fi

if [[ "${script_content}" == *"DNS_SAFE_REPAIR_COMMAND"* ]]; then
  if [[ "${state}" == "broken" ]]; then
    echo "DNS host preflight failed on Arbuzas; fix the listener conflict before retrying." >&2
    echo "- conflicting host listener on 443: LISTEN 0 128 100.64.0.10:443 0.0.0.0:* users:((\"tailscaled\",pid=99,fd=9))" >&2
    echo "Safe repair: ARBUZAS_HOST='arbuzas' ARBUZAS_USER='${USER:-tester}' ARBUZAS_SSH_PORT='22' ARBUZAS_DNS_ADMIN_LAN_IP='192.168.32.22' bash tools/arbuzas/deploy.sh repair-dns-admin" >&2
    exit 1
  fi
  exit 0
fi

if [[ "${script_content}" == *"tailscale serve --bg --yes --tcp 8097 127.0.0.1:8097"* ]]; then
  printf 'healthy\n' > "${state_file}"
  echo "nginx -t" >> "${log_dir}/repair.log"
  echo "--- tailscale serve status ---"
  echo "tcp://100.64.0.10:8097 -> tcp://127.0.0.1:8097"
  echo "--- dns host listeners ---"
  echo "LISTEN 0 128 100.64.0.10:80 0.0.0.0:* users:((\"nginx\",pid=76,fd=6))"
  echo "LISTEN 0 128 [fd7a:115c:a1e0::563a:5a77]:80 [::]:* users:((\"nginx\",pid=76,fd=7))"
  echo "LISTEN 0 128 127.0.0.1:8097 0.0.0.0:* users:((\"docker-proxy\",pid=77,fd=7))"
  echo "LISTEN 0 128 192.168.32.22:8097 0.0.0.0:* users:((\"docker-proxy\",pid=78,fd=8))"
  echo "--- docker published dns ports ---"
  echo "arbuzas-dns_controlplane-1|0.0.0.0:443->443/tcp, 0.0.0.0:853->853/tcp, 127.0.0.1:8097->8097/tcp, 192.168.32.22:8097->8097/tcp"
  exit 0
fi

if [[ "${script_content}" == *"--- tailscale serve status ---"* ]]; then
  echo "--- tailscale serve status ---"
  echo "tcp://100.64.0.10:8097 -> tcp://127.0.0.1:8097"
  echo "--- dns host listeners ---"
  echo "LISTEN 0 128 100.64.0.10:80 0.0.0.0:* users:((\"nginx\",pid=76,fd=6))"
  echo "LISTEN 0 128 [fd7a:115c:a1e0::563a:5a77]:80 [::]:* users:((\"nginx\",pid=76,fd=7))"
  echo "LISTEN 0 128 127.0.0.1:8097 0.0.0.0:* users:((\"docker-proxy\",pid=77,fd=7))"
  echo "LISTEN 0 128 192.168.32.22:8097 0.0.0.0:* users:((\"docker-proxy\",pid=78,fd=8))"
  echo "--- docker published dns ports ---"
  echo "arbuzas-dns_controlplane-1|0.0.0.0:443->443/tcp, 0.0.0.0:853->853/tcp, 127.0.0.1:8097->8097/tcp, 192.168.32.22:8097->8097/tcp"
  exit 0
fi

if [[ "${script_content}" == *"compose stop dns_controlplane"* ]]; then
  echo "compose stop dns_controlplane" >> "${log_dir}/cutover.log"
fi

if [[ "${script_content}" == *"build dns_controlplane"* ]]; then
  echo "build dns_controlplane" >> "${log_dir}/cutover.log"
fi

exit 0
EOF_SSH
chmod +x "${fakebin}/ssh"

cat > "${fakebin}/scp" <<'EOF_SCP'
#!/usr/bin/env bash
set -euo pipefail
exit 0
EOF_SCP
chmod +x "${fakebin}/scp"

cat > "${fakebin}/curl" <<'EOF_CURL'
#!/usr/bin/env bash
set -euo pipefail

real_curl="${REAL_CURL:?}"
args="$*"

if [[ "${args}" == *"%{http_code}"* ]]; then
  printf '404'
  exit 0
fi

if [[ "${args}" == *"/login"* ]]; then
  exit 0
fi

if [[ "${args}" == *"http://arbuzas.tail9345a.ts.net/"* ]]; then
  exit 0
fi

exec "${real_curl}" "$@"
EOF_CURL
chmod +x "${fakebin}/curl"

cat > "${fakebin}/python3" <<'EOF_PY'
#!/usr/bin/env bash
set -euo pipefail

real_python3="${REAL_PYTHON3:?}"

if [[ "${1:-}" != "-" ]]; then
  exec "${real_python3}" "$@"
fi

stdin_file="$(mktemp)"
trap 'rm -f "${stdin_file}"' EXIT
cat > "${stdin_file}"
script_content="$(cat "${stdin_file}")"

if [[ "${script_content}" == *"unexpected DoH status"* ]] || [[ "${script_content}" == *"unexpected DoH content type"* ]] || [[ "${script_content}" == *"missing DoT response length prefix"* ]] || [[ "${script_content}" == *"DoT response did not set the QR bit"* ]]; then
  exit 0
fi

exec "${real_python3}" "$@" < "${stdin_file}"
EOF_PY
chmod +x "${fakebin}/python3"

run_deploy_script() {
  PATH="${fakebin}:${PATH}" \
  FAKE_SSH_LOG_DIR="${tmpdir}" \
  FAKE_DNS_ADMIN_STATE_FILE="${tmpdir}/dns-admin-state" \
  REAL_BASE64="${real_base64}" \
  REAL_CURL="${real_curl}" \
  REAL_PYTHON3="${real_python3}" \
  bash "${SCRIPT_PATH}" "$@" --env-file "${tmpdir}/arbuzas.env"
}

if run_deploy_script deploy --services dns_controlplane --release-id "${release_id}" >"${tmpdir}/deploy.out" 2>"${tmpdir}/deploy.err"; then
  echo "FAIL: targeted DNS deploy should fail early while the stale private 443 forward is still present" >&2
  exit 1
fi

if ! cat "${tmpdir}/deploy.out" "${tmpdir}/deploy.err" | grep -F "repair-dns-admin" >/dev/null; then
  echo "FAIL: targeted DNS deploy failure did not point the operator at repair-dns-admin" >&2
  exit 1
fi

if [[ -f "${tmpdir}/cutover.log" ]]; then
  echo "FAIL: targeted DNS deploy reached build or stop even though the host preflight failed" >&2
  cat "${tmpdir}/cutover.log" >&2 || true
  exit 1
fi

if ! run_deploy_script repair-dns-admin >"${tmpdir}/repair.out" 2>"${tmpdir}/repair.err"; then
  echo "FAIL: repair-dns-admin should succeed after a stale private 443 forward is detected" >&2
  cat "${tmpdir}/repair.err" >&2 || true
  exit 1
fi

if ! grep -F "tailscale serve status" "${tmpdir}/repair.out" >/dev/null; then
  echo "FAIL: repair-dns-admin did not print the current Tailscale serve status" >&2
  exit 1
fi

if [[ "$(cat "${tmpdir}/dns-admin-state")" != "healthy" ]]; then
  echo "FAIL: repair-dns-admin did not move the fake host into a healthy state" >&2
  exit 1
fi

if ! run_deploy_script validate --services dns_controlplane >"${tmpdir}/validate.out" 2>"${tmpdir}/validate.err"; then
  echo "FAIL: targeted DNS validation should pass after repair-dns-admin fixes the private forward" >&2
  cat "${tmpdir}/validate.err" >&2 || true
  exit 1
fi

echo "PASS: Arbuzas DNS deploy fails before downtime on stale private forwards, repair-dns-admin clears the blocker, and targeted validation passes afterward"
