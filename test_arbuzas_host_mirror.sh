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
cat > "${remote_root}/etc/arbuzas/env/train-bot.env.bak-google-ai" <<'EOF'
BOT_TOKEN=historical-copy-must-not-be-mirrored
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
test ! -e "${mirror_root}/etc/arbuzas/env/train-bot.env.bak-google-ai"
grep -F 'ARBUZAS_RELEASE_ID=initial' "${mirror_root}/etc/arbuzas/current/release.env" >/dev/null
for private_path in \
  "${mirror_root}/etc/arbuzas/env/train-bot.env" \
  "${mirror_root}/etc/arbuzas/secrets/android-adb/adbkey" \
  "${mirror_root}/etc/arbuzas/cloudflared/train-bot.json" \
  "${mirror_root}/etc/arbuzas/current/release.env"; do
  if [[ "$(stat -f '%Lp' "${private_path}" 2>/dev/null || stat -c '%a' "${private_path}")" != 600 ]]; then
    echo "FAIL: private mirror file was not hardened to 0600: ${private_path}" >&2
    exit 1
  fi
done
chmod 0644 "${mirror_root}/etc/arbuzas/env/train-bot.env"
python3 "${SCRIPT}" audit --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}" >/dev/null
if [[ "$(stat -f '%Lp' "${mirror_root}/etc/arbuzas/env/train-bot.env" 2>/dev/null || stat -c '%a' "${mirror_root}/etc/arbuzas/env/train-bot.env")" != 600 ]]; then
  echo "FAIL: host mirror audit did not repair a checkout-default private mode" >&2
  exit 1
fi

cat > "${mirror_root}/etc/arbuzas/env/train-bot.env" <<'EOF'
BOT_TOKEN=local-change
EOF
cat > "${remote_root}/etc/arbuzas/current/release.env" <<'EOF'
ARBUZAS_RELEASE_ID=remote-advanced
EOF
python3 "${SCRIPT}" push --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}" --changed-paths-file "${changed_paths}"

grep -F 'BOT_TOKEN=local-change' "${remote_root}/etc/arbuzas/env/train-bot.env" >/dev/null
grep -F 'ARBUZAS_RELEASE_ID=remote-advanced' "${remote_root}/etc/arbuzas/current/release.env" >/dev/null
grep -Fx 'etc/arbuzas/env/train-bot.env' "${changed_paths}" >/dev/null
if [[ "$(stat -f '%Lp' "${remote_root}/etc/arbuzas/env/train-bot.env" 2>/dev/null || stat -c '%a' "${remote_root}/etc/arbuzas/env/train-bot.env")" != 600 ]]; then
  echo "FAIL: permission-only mirror push did not harden the remote env" >&2
  exit 1
fi

cp "${mirror_root}/etc/arbuzas/current/release.env" "${tmpdir}/release.env.snapshot"
printf 'ARBUZAS_RELEASE_ID=must-not-push\n' > "${mirror_root}/etc/arbuzas/current/release.env"
if python3 "${SCRIPT}" push --profile arbuzas --remote-root "${remote_root}" --mirror-root "${mirror_root}" >"${tmpdir}/pull-only-push.out" 2>&1; then
  echo "FAIL: pull-only active release snapshot must not be pushable" >&2
  exit 1
fi
grep -F 'pull-only local changed: etc/arbuzas/current/release.env' "${tmpdir}/pull-only-push.out" >/dev/null
grep -F 'ARBUZAS_RELEASE_ID=remote-advanced' "${remote_root}/etc/arbuzas/current/release.env" >/dev/null
cp "${tmpdir}/release.env.snapshot" "${mirror_root}/etc/arbuzas/current/release.env"

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

cat > "${changed_paths}" <<'EOF'
etc/arbuzas/secrets/satiksme-chat-analyzer/google-api-key.secret
EOF
affected="$(python3 "${SCRIPT}" affected --profile arbuzas --changed-paths-file "${changed_paths}")"
if [[ "${affected}" != satiksme_bot ]]; then
  echo "FAIL: analyzer secret change should restart only satiksme_bot" >&2
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

python3 - "${SCRIPT}" <<'PY'
import importlib.util
import pathlib
import sys

spec = importlib.util.spec_from_file_location("host_mirror", sys.argv[1])
host_mirror = importlib.util.module_from_spec(spec)
assert spec.loader is not None
sys.modules[spec.name] = host_mirror
spec.loader.exec_module(host_mirror)

source = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
upload_block = source.split("def push_remote_ssh(", 1)[1].split("def write_changed_paths(", 1)[0]
for required_guard in (
    "secrets.token_hex(16)",
    "mkdir -m 0700",
    "chmod 0600",
    "stat.S_IMODE(staging_stat.st_mode) != 0o700",
    "stat.S_IMODE(tar_stat.st_mode) != 0o600",
    "finally:",
    "rmdir --",
):
    if required_guard not in upload_block:
        raise SystemExit(f"host-mirror secure remote staging is missing: {required_guard}")
if "scp_base_args(args)" in upload_block:
    raise SystemExit("host-mirror secret archive still crosses scp without private remote staging")

path = "etc/arbuzas/env/satiksme-bot.env"
baseline = {path: {"sha256": "old-content", "mode": 0o644}}
local = {path: {"sha256": "new-content", "mode": 0o600}}
remote = {path: {"sha256": "old-content", "mode": 0o600}}

local_changed, remote_changed, conflicts = host_mirror.classify(baseline, local, remote)
if local_changed != [path] or remote_changed or conflicts:
    raise SystemExit(
        "matched remote permission hardening did not allow the local content migration: "
        f"local={local_changed} remote={remote_changed} conflicts={conflicts}"
    )

remote[path] = {"sha256": "different-remote-content", "mode": 0o600}
local_changed, remote_changed, conflicts = host_mirror.classify(baseline, local, remote)
if conflicts != [path] or local_changed or remote_changed:
    raise SystemExit("genuinely different remote content was not rejected as a conflict")

remote[path] = {"sha256": "old-content", "mode": 0o640}
local_changed, remote_changed, conflicts = host_mirror.classify(baseline, local, remote)
if conflicts != [path] or local_changed or remote_changed:
    raise SystemExit("genuinely different remote mode was not rejected as a conflict")

local[path] = {"sha256": "old-content", "mode": 0o600}
remote[path] = {"sha256": "old-content", "mode": 0o600}
local_changed, remote_changed, conflicts = host_mirror.classify(baseline, local, remote)
if local_changed or remote_changed or conflicts:
    raise SystemExit("identical local/remote permission hardening was not treated as converged")
PY

echo "PASS: Arbuzas host mirror preserves pull-only migration state, syncs config, detects conflicts, and maps affected services"
