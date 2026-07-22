#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MIRROR_ROOT="${REPO_ROOT}/infra/arbuzas/host-mirror"
SATIKSME_ENV="${MIRROR_ROOT}/etc/arbuzas/env/satiksme-bot.env"

python3 - "${REPO_ROOT}" "${MIRROR_ROOT}" "${SATIKSME_ENV}" <<'PY'
from pathlib import Path
import stat
import subprocess
import sys

repo_root = Path(sys.argv[1])
mirror_root = Path(sys.argv[2])
satiksme_env = Path(sys.argv[3])


def is_history(path: Path) -> bool:
    name = path.name
    return ".bak" in name or ".before-" in name or ".retired-" in name or name.endswith("~")


tracked = subprocess.run(
    ["git", "-C", str(repo_root), "ls-files", "infra/arbuzas/host-mirror/etc/arbuzas/env"],
    check=True,
    stdout=subprocess.PIPE,
    text=True,
).stdout.splitlines()
tracked_history = [
    rel
    for rel in tracked
    if (repo_root / rel).exists() and is_history(Path(rel))
]
if tracked_history:
    raise SystemExit("tracked host environment history remains: " + ", ".join(tracked_history))

if satiksme_env.exists():
    assignments = {}
    for raw in satiksme_env.read_text(encoding="utf-8").splitlines():
        if raw.lstrip().startswith("#") or "=" not in raw:
            continue
        key, value = raw.split("=", 1)
        assignments[key.strip()] = value.strip()
    direct = {
        "SATIKSME_CHAT_ANALYZER_API_ID",
        "SATIKSME_CHAT_ANALYZER_API_HASH",
        "SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY",
        "SATIKSME_CHAT_ANALYZER_MODEL_API_KEY",
    }
    remaining = sorted(key for key in direct if assignments.get(key))
    if remaining:
        raise SystemExit("inline analyzer credentials remain: " + ", ".join(remaining))
    missing_files = sorted(f"{key}_FILE" for key in direct if not assignments.get(f"{key}_FILE"))
    if missing_files:
        raise SystemExit("file-backed analyzer settings are missing: " + ", ".join(missing_files))

private_roots = [
    mirror_root / "etc/arbuzas/env",
    mirror_root / "etc/arbuzas/secrets",
    mirror_root / "etc/arbuzas/cloudflared",
]
unsafe_modes = []
for root in private_roots:
    if not root.exists():
        continue
    for path in root.rglob("*"):
        if path.is_file() and stat.S_IMODE(path.stat().st_mode) != 0o600:
            unsafe_modes.append(str(path.relative_to(repo_root)))
for rel in (".host-mirror-manifest.json", "etc/arbuzas/current/release.env"):
    path = mirror_root / rel
    if path.exists() and stat.S_IMODE(path.stat().st_mode) != 0o600:
        unsafe_modes.append(str(path.relative_to(repo_root)))
if unsafe_modes:
    raise SystemExit("private host mirror files are not 0600: " + ", ".join(sorted(unsafe_modes)))
PY

if ! git -C "${REPO_ROOT}" check-ignore -q \
  infra/arbuzas/host-mirror/etc/arbuzas/secrets/satiksme-chat-analyzer/google-api-key.secret; then
  printf 'analyzer credential files are not protected by .gitignore\n' >&2
  exit 1
fi

printf 'host mirror secret policy tests passed\n'
