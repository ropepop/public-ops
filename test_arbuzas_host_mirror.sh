#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${REPO_ROOT}/tools/arbuzas/host_mirror.py"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

remote_root="${tmpdir}/remote"
mirror_root="${tmpdir}/mirror"
changed_paths="${tmpdir}/changed.txt"

mkdir -p \
  "${remote_root}/etc/arbuzas/env" \
  "${remote_root}/etc/arbuzas/secrets/android-adb" \
  "${remote_root}/etc/arbuzas/cloudflared" \
  "${remote_root}/etc/arbuzas/dns/tls" \
  "${remote_root}/etc/arbuzas/current"

cat > "${remote_root}/etc/arbuzas/env/train-bot.env" <<'EOF'
BOT_TOKEN=initial-train
EOF
cat > "${remote_root}/etc/arbuzas/secrets/android-adb/adbkey" <<'EOF'
adb-private-key
EOF
cat > "${remote_root}/etc/arbuzas/cloudflared/train-bot.json" <<'EOF'
{"AccountTag":"initial"}
EOF
cat > "${remote_root}/etc/arbuzas/dns/runtime.env" <<'EOF'
ARBUZAS_DNS_HOSTNAME=dns.example.test
EOF
cat > "${remote_root}/etc/arbuzas/dns/arbuzas-dns.yaml" <<'EOF'
hostname: dns.example.test
EOF
cat > "${remote_root}/etc/arbuzas/dns/doh-identities.json" <<'EOF'
{"identities":[]}
EOF
cat > "${remote_root}/etc/arbuzas/dns/tls/fullchain.pem" <<'EOF'
cert
EOF
cat > "${remote_root}/etc/arbuzas/dns/tls/privkey.pem" <<'EOF'
key
EOF
chmod 600 "${remote_root}/etc/arbuzas/dns/tls/privkey.pem"
cat > "${remote_root}/etc/arbuzas/current/release.env" <<'EOF'
ARBUZAS_RELEASE_ID=initial
EOF

python3 "${SCRIPT}" pull --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}"

test -f "${mirror_root}/etc/arbuzas/env/train-bot.env"
test -f "${mirror_root}/etc/arbuzas/secrets/android-adb/adbkey"
test -f "${mirror_root}/etc/arbuzas/cloudflared/train-bot.json"
test -f "${mirror_root}/etc/arbuzas/dns/runtime.env"
test -f "${mirror_root}/etc/arbuzas/dns/arbuzas-dns.yaml"
test -f "${mirror_root}/etc/arbuzas/dns/doh-identities.json"
test -f "${mirror_root}/etc/arbuzas/dns/tls/fullchain.pem"
test -f "${mirror_root}/etc/arbuzas/dns/tls/privkey.pem"
test -f "${mirror_root}/etc/arbuzas/current/release.env"

if [[ "$(stat -f '%Lp' "${mirror_root}/etc/arbuzas/dns/tls/privkey.pem" 2>/dev/null || stat -c '%a' "${mirror_root}/etc/arbuzas/dns/tls/privkey.pem")" != "600" ]]; then
  echo "FAIL: pulled private key mode was not preserved" >&2
  exit 1
fi

cat > "${mirror_root}/etc/arbuzas/env/train-bot.env" <<'EOF'
BOT_TOKEN=local-change
EOF
python3 "${SCRIPT}" push --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}" --changed-paths-file "${changed_paths}"

grep -F 'BOT_TOKEN=local-change' "${remote_root}/etc/arbuzas/env/train-bot.env" >/dev/null
grep -Fx 'etc/arbuzas/env/train-bot.env' "${changed_paths}" >/dev/null

python3 "${SCRIPT}" pull --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}"
cat > "${remote_root}/etc/arbuzas/env/satiksme-bot.env" <<'EOF'
BOT_TOKEN=host-only
EOF
if python3 "${SCRIPT}" audit --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}" >/tmp/arbuzas-host-mirror-audit.out 2>&1; then
  echo "FAIL: host-only drift should make audit fail" >&2
  exit 1
fi
grep -F 'remote changed: etc/arbuzas/env/satiksme-bot.env' /tmp/arbuzas-host-mirror-audit.out >/dev/null

python3 "${SCRIPT}" pull --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}"
cat > "${mirror_root}/etc/arbuzas/env/train-bot.env" <<'EOF'
BOT_TOKEN=local-conflict
EOF
cat > "${remote_root}/etc/arbuzas/env/train-bot.env" <<'EOF'
BOT_TOKEN=remote-conflict
EOF
if python3 "${SCRIPT}" push --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}" >/tmp/arbuzas-host-mirror-push.out 2>&1; then
  echo "FAIL: both-sides conflict should block push" >&2
  exit 1
fi
grep -F 'conflict: etc/arbuzas/env/train-bot.env' /tmp/arbuzas-host-mirror-push.out >/dev/null

cat > "${changed_paths}" <<'EOF'
etc/arbuzas/env/train-bot.env
etc/arbuzas/cloudflared/ticket-remote.json
etc/arbuzas/dns/runtime.env
EOF
affected="$(python3 "${SCRIPT}" affected --profile arbuzas --changed-paths-file "${changed_paths}")"
printf '%s\n' "${affected}" | grep -Fx 'dns_controlplane' >/dev/null
printf '%s\n' "${affected}" | grep -Fx 'ticket_remote_tunnel' >/dev/null
printf '%s\n' "${affected}" | grep -Fx 'train_bot' >/dev/null
if printf '%s\n' "${affected}" | grep -Fx 'satiksme_bot' >/dev/null; then
  echo "FAIL: unrelated services should not be affected" >&2
  exit 1
fi

echo "PASS: Arbuzas host mirror pulls, audits, pushes, detects conflicts, and maps affected services"
