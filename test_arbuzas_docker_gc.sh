#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT="${REPO_ROOT}/tools/arbuzas/docker_gc.py"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

FAKE_DOCKER_ROOT="${tmpdir}/fake-docker"
FAKE_DOCKER_BIN="${tmpdir}/bin"
FAKE_DOCKER_LOG="${FAKE_DOCKER_ROOT}/commands.log"
IMAGES_FILE="${FAKE_DOCKER_ROOT}/images.tsv"
CONTAINERS_FILE="${FAKE_DOCKER_ROOT}/containers.tsv"
STATE_FILE="${tmpdir}/docker-gc/state.json"
RELEASES_ROOT="${tmpdir}/releases"
CURRENT_LINK="${tmpdir}/current"

mkdir -p "${FAKE_DOCKER_ROOT}" "${FAKE_DOCKER_BIN}" "${RELEASES_ROOT}"
touch "${FAKE_DOCKER_LOG}"

cat > "${FAKE_DOCKER_BIN}/docker" <<'PY'
#!/usr/bin/env python3
from __future__ import annotations

import os
import sys
from pathlib import Path

root = Path(os.environ["FAKE_DOCKER_ROOT"])
images_path = root / "images.tsv"
containers_path = root / "containers.tsv"
log_path = root / "commands.log"
args = sys.argv[1:]


def log(message: str) -> None:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with log_path.open("a", encoding="utf-8") as handle:
        handle.write(message + "\n")


def read_images() -> list[tuple[str, str, str]]:
    if not images_path.exists():
        return []
    rows = []
    for raw_line in images_path.read_text(encoding="utf-8").splitlines():
        if not raw_line.strip():
            continue
        image_id, repository, tag = raw_line.split("\t", 2)
        rows.append((image_id, repository, tag))
    return rows


def write_images(rows: list[tuple[str, str, str]]) -> None:
    images_path.write_text(
        "".join(f"{image_id}\t{repository}\t{tag}\n" for image_id, repository, tag in rows),
        encoding="utf-8",
    )


def read_containers() -> list[str]:
    if not containers_path.exists():
        return []
    return [line.strip() for line in containers_path.read_text(encoding="utf-8").splitlines() if line.strip()]


if args == ["image", "ls", "--all", "--no-trunc", "--format", "{{.ID}}"]:
    for image_id, _, _ in read_images():
        print(image_id)
    sys.exit(0)

if args == ["image", "ls", "--all", "--no-trunc", "--format", "{{.Repository}}|{{.Tag}}|{{.ID}}"]:
    for image_id, repository, tag in read_images():
        print(f"{repository}|{tag}|{image_id}")
    sys.exit(0)

if args == ["ps", "-aq", "--no-trunc"]:
    for container_id in read_containers():
        print(container_id)
    sys.exit(0)

if len(args) >= 3 and args[:3] == ["inspect", "--format", "{{.Image}}"]:
    for container_id in args[3:]:
        print(container_id)
    sys.exit(0)

if len(args) == 3 and args[:2] == ["image", "rm"]:
    target = args[2]
    rows = read_images()
    kept = [row for row in rows if row[0] != target]
    if len(kept) == len(rows):
        print(f"missing image {target}", file=sys.stderr)
        sys.exit(1)
    write_images(kept)
    log(f"IMAGE_RM {target}")
    sys.exit(0)

if args == ["builder", "prune", "-af", "--filter", "until=168h"]:
    log("BUILDER_PRUNE until=168h")
    sys.exit(0)

print("unsupported fake docker invocation: " + " ".join(args), file=sys.stderr)
sys.exit(1)
PY
chmod +x "${FAKE_DOCKER_BIN}/docker"

export FAKE_DOCKER_ROOT
export PATH="${FAKE_DOCKER_BIN}:${PATH}"

set_release_mtime() {
  local epoch="$1"
  shift
  python3 - "${epoch}" "$@" <<'PY'
import os
import sys

timestamp = float(sys.argv[1])
for path in sys.argv[2:]:
    os.utime(path, (timestamp, timestamp), follow_symlinks=False)
PY
}

assert_has_image() {
  local image_id="$1"
  if ! grep -Fq "${image_id}" "${IMAGES_FILE}"; then
    echo "FAIL: expected image ${image_id} to be present" >&2
    exit 1
  fi
}

assert_missing_image() {
  local image_id="$1"
  if grep -Fq "${image_id}" "${IMAGES_FILE}"; then
    echo "FAIL: expected image ${image_id} to be absent" >&2
    exit 1
  fi
}

assert_state_has() {
  local image_id="$1"
  local expected_ts="$2"
  if ! python3 - "${STATE_FILE}" "${image_id}" "${expected_ts}" <<'PY'
import json
import sys
from pathlib import Path

state_path = Path(sys.argv[1])
image_id = sys.argv[2]
expected_ts = int(sys.argv[3])
payload = json.loads(state_path.read_text(encoding="utf-8"))
images = payload.get("images", {})
entry = images.get(image_id)
if not isinstance(entry, dict) or entry.get("first_seen_unused_at") != expected_ts:
    raise SystemExit(1)
PY
  then
    echo "FAIL: expected state entry ${image_id} with timestamp ${expected_ts}" >&2
    exit 1
  fi
}

assert_state_missing() {
  local image_id="$1"
  if python3 - "${STATE_FILE}" "${image_id}" <<'PY'
import json
import sys
from pathlib import Path

payload = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
if sys.argv[2] in payload.get("images", {}):
    raise SystemExit(0)
raise SystemExit(1)
PY
  then
    echo "FAIL: expected state entry ${image_id} to be absent" >&2
    exit 1
  fi
}

assert_log_contains() {
  local needle="$1"
  if ! grep -Fq "${needle}" "${FAKE_DOCKER_LOG}"; then
    echo "FAIL: expected fake docker log to contain ${needle}" >&2
    exit 1
  fi
}

assert_log_not_contains() {
  local needle="$1"
  if grep -Fq "${needle}" "${FAKE_DOCKER_LOG}"; then
    echo "FAIL: fake docker log unexpectedly contained ${needle}" >&2
    exit 1
  fi
}

CURRENT_RELEASE_ID="20260409T120000Z"
ROLLBACK_RELEASE_ID="20260408T120000Z"
OLD_DELETE_RELEASE_ID="20260320T120000Z"
OLD_PROTECT_RELEASE_ID="20260401T120000Z"

mkdir -p \
  "${RELEASES_ROOT}/${CURRENT_RELEASE_ID}" \
  "${RELEASES_ROOT}/${ROLLBACK_RELEASE_ID}" \
  "${RELEASES_ROOT}/${OLD_DELETE_RELEASE_ID}" \
  "${RELEASES_ROOT}/${OLD_PROTECT_RELEASE_ID}"
ln -s "${RELEASES_ROOT}/${CURRENT_RELEASE_ID}" "${CURRENT_LINK}"

cat > "${RELEASES_ROOT}/${CURRENT_RELEASE_ID}/release.env" <<EOF
ARBUZAS_RELEASE_ID=${CURRENT_RELEASE_ID}
EOF

set_release_mtime 400 \
  "${RELEASES_ROOT}/${CURRENT_RELEASE_ID}" \
  "${RELEASES_ROOT}/${CURRENT_RELEASE_ID}/release.env"
set_release_mtime 300 "${RELEASES_ROOT}/${ROLLBACK_RELEASE_ID}"
set_release_mtime 200 "${RELEASES_ROOT}/${OLD_PROTECT_RELEASE_ID}"
set_release_mtime 100 "${RELEASES_ROOT}/${OLD_DELETE_RELEASE_ID}"

cat > "${IMAGES_FILE}" <<EOF
sha256:current	arbuzas/train-bot	${CURRENT_RELEASE_ID}
sha256:rollback	arbuzas/train-bot	${ROLLBACK_RELEASE_ID}
sha256:old-delete	arbuzas/train-bot	${OLD_DELETE_RELEASE_ID}
sha256:old-protect	arbuzas/site-notifications	${OLD_PROTECT_RELEASE_ID}
sha256:generic	external/image	latest
sha256:stopped	used/image	latest
EOF

cat > "${CONTAINERS_FILE}" <<'EOF'
sha256:stopped
EOF

python3 "${SCRIPT}" \
  --current-link "${CURRENT_LINK}" \
  --releases-root "${RELEASES_ROOT}" \
  --state-file "${STATE_FILE}" \
  --grace-seconds 604800 \
  --build-cache-until 168h \
  --now 1000 > "${tmpdir}/run1.out"

assert_has_image "sha256:current"
assert_has_image "sha256:rollback"
assert_has_image "sha256:old-delete"
assert_has_image "sha256:old-protect"
assert_has_image "sha256:generic"
assert_has_image "sha256:stopped"
assert_state_has "sha256:old-delete" 1000
assert_state_has "sha256:old-protect" 1000
assert_state_has "sha256:generic" 1000
assert_log_not_contains "IMAGE_RM"
assert_log_contains "BUILDER_PRUNE until=168h"

set_release_mtime 350 "${RELEASES_ROOT}/${OLD_PROTECT_RELEASE_ID}"

python3 "${SCRIPT}" \
  --current-link "${CURRENT_LINK}" \
  --releases-root "${RELEASES_ROOT}" \
  --state-file "${STATE_FILE}" \
  --grace-seconds 604800 \
  --build-cache-until 168h \
  --now 605810 > "${tmpdir}/run2.out"

assert_has_image "sha256:current"
assert_has_image "sha256:rollback"
assert_missing_image "sha256:old-delete"
assert_has_image "sha256:old-protect"
assert_missing_image "sha256:generic"
assert_has_image "sha256:stopped"
assert_state_missing "sha256:old-delete"
assert_state_missing "sha256:old-protect"
assert_state_missing "sha256:generic"
assert_state_has "sha256:rollback" 605810
assert_log_contains "IMAGE_RM sha256:old-delete"
assert_log_contains "IMAGE_RM sha256:generic"
assert_log_not_contains "IMAGE_RM sha256:old-protect"

printf 'not-json\n' > "${STATE_FILE}"
cat >> "${IMAGES_FILE}" <<'EOF'
sha256:corrupt	external/corrupt	latest
EOF

python3 "${SCRIPT}" \
  --current-link "${CURRENT_LINK}" \
  --releases-root "${RELEASES_ROOT}" \
  --state-file "${STATE_FILE}" \
  --grace-seconds 604800 \
  --build-cache-until 168h \
  --now 1209620 > "${tmpdir}/run3.out" 2> "${tmpdir}/run3.err"

assert_has_image "sha256:corrupt"
assert_state_has "sha256:corrupt" 1209620
assert_log_not_contains "IMAGE_RM sha256:corrupt"
assert_log_contains "BUILDER_PRUNE until=168h"

if [[ "$(grep -Fc 'BUILDER_PRUNE until=168h' "${FAKE_DOCKER_LOG}")" -ne 3 ]]; then
  echo "FAIL: expected build cache prune to run on every helper invocation" >&2
  exit 1
fi

if ! grep -Fq "state reset because" "${tmpdir}/run3.err"; then
  echo "FAIL: expected corrupt state warning on stderr" >&2
  exit 1
fi

echo "PASS: Arbuzas Docker GC preserves protected images and applies the 7-day grace policy"
