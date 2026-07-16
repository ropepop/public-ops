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
chmod 600 "${remote_root}/etc/arbuzas/secrets/android-adb/adbkey"
cat > "${remote_root}/etc/arbuzas/current/release.env" <<'EOF'
ARBUZAS_RELEASE_ID=initial
EOF

python3 "${SCRIPT}" pull --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}"

test -f "${mirror_root}/etc/arbuzas/env/train-bot.env"
test -f "${mirror_root}/etc/arbuzas/secrets/android-adb/adbkey"
test -f "${mirror_root}/etc/arbuzas/cloudflared/train-bot.json"
test ! -f "${mirror_root}/etc/arbuzas/current/release.env"

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
EOF
affected="$(python3 "${SCRIPT}" affected --profile arbuzas --changed-paths-file "${changed_paths}")"
printf '%s\n' "${affected}" | grep -Fx 'ticket_remote_tunnel' >/dev/null
printf '%s\n' "${affected}" | grep -Fx 'train_bot' >/dev/null
if printf '%s\n' "${affected}" | grep -Fx 'satiksme_bot' >/dev/null; then
  echo "FAIL: unrelated services should not be affected" >&2
  exit 1
fi

known_hosts="${tmpdir}/known_hosts"
printf 'example.invalid ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest\n' > "${known_hosts}"
python3 - "${SCRIPT}" "${known_hosts}" <<'PY'
import importlib.util
import pathlib
import types
import sys

spec = importlib.util.spec_from_file_location("host_mirror", sys.argv[1])
module = importlib.util.module_from_spec(spec)
assert spec.loader is not None
sys.modules[spec.name] = module
spec.loader.exec_module(module)
args = types.SimpleNamespace(
    ssh_port="2222",
    ssh_target="deploy@example.invalid",
    ssh_known_hosts_file=str(pathlib.Path(sys.argv[2]).resolve()),
)
expected = [
    "-o", "StrictHostKeyChecking=yes",
    "-o", f"UserKnownHostsFile={args.ssh_known_hosts_file}",
]
ssh_args = module.ssh_base_args(args)
scp_args = module.scp_base_args(args)
for fragment in expected:
    if fragment not in ssh_args or fragment not in scp_args:
        raise SystemExit(f"known_hosts option missing from ssh/scp args: {fragment}")
PY

if python3 "${SCRIPT}" affected --profile arbuzas --changed-paths-file "${changed_paths}" \
  --ssh-known-hosts-file relative-known-hosts >/dev/null 2>&1; then
  echo "FAIL: relative known_hosts file should be rejected" >&2
  exit 1
fi

narrow_remote="${tmpdir}/narrow-remote"
narrow_mirror="${tmpdir}/narrow-mirror"
mkdir -p "${narrow_remote}/etc/arbuzas/env" "${narrow_remote}/etc/arbuzas/cloudflared"
printf 'ticket=remote\n' > "${narrow_remote}/etc/arbuzas/env/ticket-remote.env"
printf 'train=remote\n' > "${narrow_remote}/etc/arbuzas/env/train-bot.env"
printf 'satiksme=unrelated\n' > "${narrow_remote}/etc/arbuzas/env/satiksme-bot.env"
python3 "${SCRIPT}" pull --profile ticket-recovery --remote-root "${narrow_remote}" --mirror-root "${narrow_mirror}"
test -f "${narrow_mirror}/etc/arbuzas/env/ticket-remote.env"
test -f "${narrow_mirror}/etc/arbuzas/env/train-bot.env"
test ! -e "${narrow_mirror}/etc/arbuzas/env/satiksme-bot.env"

printf 'satiksme=changed-but-unrelated\n' > "${narrow_remote}/etc/arbuzas/env/satiksme-bot.env"
python3 "${SCRIPT}" audit --profile ticket-recovery --remote-root "${narrow_remote}" --mirror-root "${narrow_mirror}" >/dev/null
printf 'ticket=selected-drift\n' > "${narrow_remote}/etc/arbuzas/env/ticket-remote.env"
if python3 "${SCRIPT}" audit --profile ticket-recovery --remote-root "${narrow_remote}" --mirror-root "${narrow_mirror}" >"${tmpdir}/narrow-audit.out" 2>&1; then
  echo "FAIL: Ticket recovery profile ignored selected remote drift" >&2
  exit 1
fi
grep -Fq 'remote changed: etc/arbuzas/env/ticket-remote.env' "${tmpdir}/narrow-audit.out"

echo "PASS: Arbuzas host mirror pulls, audits, pushes, detects conflicts, and maps affected services"
