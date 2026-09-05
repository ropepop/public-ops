#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RECONCILER="${REPO_ROOT}/infra/arbuzas/qbittorrent/reconcile-config.py"
COMPOSE_PATH="${REPO_ROOT}/infra/arbuzas/docker/compose.yml"
MEMORY_HEALTH="${REPO_ROOT}/infra/arbuzas/docker/images/qbittorrent-memory-health.sh"
QBITTORRENT_DOCKERFILE="${REPO_ROOT}/infra/arbuzas/docker/images/qbittorrent.Dockerfile"
tmpdir="$(mktemp -d "${REPO_ROOT}/.arbuzas-qbittorrent-test.XXXXXX")"
trap 'rm -rf "${tmpdir}"' EXIT

uid="$(id -u)"
gid="$(id -g)"
if [[ "${uid}" == "0" || "${gid}" == "0" ]]; then
  uid=1000
  gid=1000
fi

safe_root="${tmpdir}/safe/config/qBittorrent"
mkdir -p "${safe_root}"
config_path="${safe_root}/qBittorrent.conf"
python3 "${RECONCILER}" --path "${config_path}" --uid "${uid}" --gid "${gid}"
cp "${config_path}" "${safe_root}/qBittorrent_new.conf"
python3 "${RECONCILER}" --path "${config_path}" --uid "${uid}" --gid "${gid}"
python3 "${RECONCILER}" --path "${config_path}" --uid "${uid}" --gid "${gid}" --check

cgroup_root="${tmpdir}/cgroup"
mkdir -p "${cgroup_root}"
printf '%s\n' '805306368' > "${cgroup_root}/memory.max"
printf '%s\n' '0' > "${cgroup_root}/memory.swap.max"
printf '%s\n' 'low 0' 'high 0' 'max 0' 'oom 0' 'oom_kill 0' 'oom_group_kill 0' > "${cgroup_root}/memory.events"
sh "${MEMORY_HEALTH}" "${cgroup_root}" 805306368
printf '%s\n' 'low 0' 'high 0' 'max 0' 'oom 1' 'oom_kill 0' 'oom_group_kill 0' > "${cgroup_root}/memory.events"
if sh "${MEMORY_HEALTH}" "${cgroup_root}" 805306368 >/dev/null 2>&1; then
  echo "FAIL: qBittorrent memory health accepted an OOM event" >&2
  exit 1
fi

python3 - "${config_path}" "${safe_root}/qBittorrent_new.conf" <<'PY'
import configparser
from pathlib import Path
import stat
import sys

for raw_path in sys.argv[1:]:
    path = Path(raw_path)
    parser = configparser.RawConfigParser(interpolation=None, strict=False, delimiters=("=",))
    parser.optionxform = str
    with path.open(encoding="utf-8") as handle:
        parser.read_file(handle)
    expected = {
        ("BitTorrent", "Session\\AsyncIOThreadsCount"): "2",
        ("BitTorrent", "Session\\CheckingMemUsageSize"): "8",
        ("BitTorrent", "Session\\ConnectionSpeed"): "10",
        ("BitTorrent", "Session\\DiskIOReadMode"): "DisableOSCache",
        ("BitTorrent", "Session\\DiskIOType"): "SimplePreadPwrite",
        ("BitTorrent", "Session\\DiskIOWriteMode"): "DisableOSCache",
        ("BitTorrent", "Session\\DiskQueueSize"): "1048576",
        ("BitTorrent", "Session\\FilePoolSize"): "32",
        ("BitTorrent", "Session\\HashingThreadsCount"): "1",
        ("BitTorrent", "Session\\MaxActiveCheckingTorrents"): "1",
        ("BitTorrent", "Session\\MaxConcurrentHTTPAnnounces"): "10",
        ("BitTorrent", "Session\\MaxConnections"): "80",
        ("BitTorrent", "Session\\MaxConnectionsPerTorrent"): "20",
        ("BitTorrent", "Session\\MaxUploads"): "12",
        ("BitTorrent", "Session\\MaxUploadsPerTorrent"): "4",
        ("BitTorrent", "Session\\QueueingSystemEnabled"): "false",
        ("BitTorrent", "Session\\RequestQueueSize"): "50",
        ("BitTorrent", "Session\\SendBufferLowWatermark"): "16",
        ("BitTorrent", "Session\\SendBufferWatermark"): "128",
        ("BitTorrent", "Session\\SendBufferWatermarkFactor"): "25",
        ("BitTorrent", "Session\\SocketReceiveBufferSize"): "65536",
        ("BitTorrent", "Session\\SocketSendBufferSize"): "65536",
        ("BitTorrent", "Session\\TorrentStopCondition"): "MetadataReceived",
        ("BitTorrent", "Session\\GlobalMaxRatio"): "-1",
        ("BitTorrent", "Session\\Port"): "45123",
        ("BitTorrent", "Session\\SaveResumeDataInterval"): "1",
        ("Preferences", "WebUI\\Port"): "24680",
        ("Preferences", "WebUI\\AlternativeUIEnabled"): "true",
        ("Preferences", "WebUI\\RootFolder"): "/vuetorrent",
        ("Preferences", "WebUI\\ServerDomains"): (
            '"arbuzas-vps.tail9345a.ts.net;localhost;127.0.0.1;qbittorrent"'
        ),
        ("Preferences", "WebUI\\AuthSubnetWhitelist"): (
            "127.0.0.1/32, ::1/128, 172.29.246.0/28, "
            "100.64.0.0/10, fd7a:115c:a1e0::/48"
        ),
    }
    for (section, key), wanted in expected.items():
        actual = parser.get(section, key, raw=True)
        if actual != wanted:
            raise SystemExit(f"{path}: {section}/{key}={actual!r}, expected {wanted!r}")
    if stat.S_IMODE(path.stat().st_mode) != 0o640:
        raise SystemExit(f"{path}: expected mode 0640")
PY

python3 - "${COMPOSE_PATH}" <<'PY'
from pathlib import Path
import re
import sys

compose = Path(sys.argv[1]).read_text(encoding="utf-8")
marker = "\n  qbittorrent:\n"
next_marker = "\n  qbittorrent_housekeeper:\n"
if marker not in compose or next_marker not in compose:
    raise SystemExit("qBittorrent Compose service boundaries are missing")
service = compose.split(marker, 1)[1].split(next_marker, 1)[0]
housekeeper = compose.split(next_marker, 1)[1].split("\n  train_tunnel:\n", 1)[0]
for expected in (
    "mem_limit: 768m",
    "mem_reservation: 384m",
    "memswap_limit: 768m",
    "/usr/local/bin/qbittorrent-memory-health",
):
    if expected not in service:
        raise SystemExit(f"qBittorrent Compose service is missing {expected!r}")
if "mem_limit: 1g" in service or "mem_limit: 2g" in service:
    raise SystemExit("qBittorrent does not have the managed sub-1-GiB memory limit")
def mib_value(block: str, key: str) -> int:
    match = re.search(rf"^\s+{re.escape(key)}:\s+(\d+)m\s*$", block, re.MULTILINE)
    if not match:
        raise SystemExit(f"missing MiB value for {key!r}")
    return int(match.group(1))

main_limit = mib_value(service, "mem_limit")
main_swap_limit = mib_value(service, "memswap_limit")
housekeeper_limit = mib_value(housekeeper, "mem_limit")
housekeeper_swap_limit = mib_value(housekeeper, "memswap_limit")
if main_limit != 768 or housekeeper_limit != 128:
    raise SystemExit(f"unexpected qBittorrent limits: {main_limit=} {housekeeper_limit=}")
if main_swap_limit != main_limit or housekeeper_swap_limit != housekeeper_limit:
    raise SystemExit("qBittorrent services have an additional swap allowance")
if (main_limit + housekeeper_limit) >= 1024:
    raise SystemExit("combined qBittorrent hard limits are not below 1 GiB")
for expected in (
    "grep -Fx '134217728' /sys/fs/cgroup/memory.max",
    "grep -Fx '0' /sys/fs/cgroup/memory.swap.max",
    "grep -Fx 'oom 0'",
    "grep -Fx 'oom_kill 0'",
):
    if expected not in housekeeper:
        raise SystemExit(f"qBittorrent housekeeper health is missing {expected!r}")
PY

python3 - "${QBITTORRENT_DOCKERFILE}" <<'PY'
from pathlib import Path
import sys

dockerfile = Path(sys.argv[1]).read_text(encoding="utf-8")
node_builder = (
    "FROM node:22.22.1-bookworm-slim@sha256:"
    "4f77a690f2f8946ab16fe1e791a3ac0667ae1c3575c3e4d0d4589e9ed5bfaf3d "
    "AS vuetorrent-builder"
)
runtime = (
    "FROM lscr.io/linuxserver/qbittorrent:5.2.3@sha256:"
    "1a4641fa759dee784708ed277ece10adbbc5810ebb8bb9fdfe1cf00031f5ab2b"
)
source_digest = "af29d17312bcf0c1d8b496f96ae74e511cbf3d31a25071d38e7eb5b61c7dcfb4"
original_dnd_digest = "1bd06f97b868cbea3d2d2bd7277e05163efd7ff326a357ca39e075e94ed4ee61"
source_url = "https://github.com/VueTorrent/VueTorrent/archive/refs/tags/v2.34.1.tar.gz"
overlay_copy = "COPY infra/arbuzas/docker/images/vuetorrent-2.34.1-overlay/"
artifact_copy = "COPY --from=vuetorrent-builder /work/vuetorrent/vuetorrent /vuetorrent"

for required in (
    node_builder,
    runtime,
    f"ADD --checksum=sha256:{source_digest}",
    source_url,
    f"'{source_digest}'",
    f"'{original_dnd_digest}'",
    overlay_copy,
    "npm ci;",
    "npm test;",
    "npm run build;",
    "PWA worker is missing current asset",
    artifact_copy,
    'io.arbuzas.vuetorrent.patch="ios-drag-materialize-v1"',
):
    if required not in dockerfile:
        raise SystemExit(f"qBittorrent image packaging is missing: {required}")

if dockerfile.count("\nFROM ") != 2:
    raise SystemExit("qBittorrent image must have exactly one builder and one runtime stage")
if dockerfile.index(node_builder) > dockerfile.index(runtime):
    raise SystemExit("VueTorrent builder must precede the qBittorrent runtime stage")
if dockerfile.index(original_dnd_digest) > dockerfile.index(overlay_copy):
    raise SystemExit("upstream DnDZone checksum must be verified before applying the overlay")
if not (
    dockerfile.index("npm ci;")
    < dockerfile.index("npm test;")
    < dockerfile.index("npm run build;")
    < dockerfile.index(runtime)
):
    raise SystemExit("VueTorrent install, test, and build must run in order in the builder stage")
if dockerfile.count("COPY --from=") != 1:
    raise SystemExit("qBittorrent runtime must copy only one builder artifact")
for retired in ("vuetorrent.zip", "releases/download/v2.34.1"):
    if retired in dockerfile:
        raise SystemExit(f"qBittorrent image still uses the retired prebuilt bundle: {retired}")
PY

mkdir -p "${tmpdir}/unsafe/config" "${tmpdir}/outside"
ln -s "${tmpdir}/outside" "${tmpdir}/unsafe/config/qBittorrent"
set +e
unsafe_output="$(python3 "${RECONCILER}" \
  --path "${tmpdir}/unsafe/config/qBittorrent/qBittorrent.conf" \
  --uid "${uid}" \
  --gid "${gid}" 2>&1)"
unsafe_status=$?
set -e
if [[ "${unsafe_status}" -eq 0 ]]; then
  echo "FAIL: reconciler followed a symlinked qBittorrent config parent" >&2
  exit 1
fi
if ! grep -F "refusing unsafe qBittorrent config parent" <<< "${unsafe_output}" >/dev/null; then
  echo "FAIL: reconciler did not explain the unsafe parent rejection" >&2
  exit 1
fi
if [[ -e "${tmpdir}/outside/qBittorrent.conf" ]]; then
  echo "FAIL: reconciler wrote outside the managed config tree" >&2
  exit 1
fi

echo "PASS: qBittorrent configuration and symlink-safety contracts hold"
