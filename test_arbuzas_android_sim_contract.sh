#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SIM_COMPOSE="${REPO_ROOT}/infra/arbuzas/android-sim/compose.yml"
SIM_SCRIPT="${REPO_ROOT}/tools/arbuzas/android-sim.sh"
SIM_RUNBOOK="${REPO_ROOT}/docs/runbooks/ANDROID_SIMULATOR_TRIAL.md"
PROD_COMPOSE="${REPO_ROOT}/infra/arbuzas/docker/compose.yml"
TICKET_REMOTE_DOCKERFILE="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-remote.Dockerfile"
TICKET_PHONE_BRIDGE_DOCKERFILE="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge.Dockerfile"
TICKET_ANDROID_SIM_TUNE_LOOP="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-android-sim-tune-loop.sh"
DEPLOY_SCRIPT="${REPO_ROOT}/tools/arbuzas/deploy.sh"

for path in "${SIM_COMPOSE}" "${SIM_SCRIPT}" "${SIM_RUNBOOK}" "${TICKET_REMOTE_DOCKERFILE}" "${TICKET_PHONE_BRIDGE_DOCKERFILE}" "${TICKET_ANDROID_SIM_TUNE_LOOP}"; do
  if [[ ! -f "${path}" ]]; then
    echo "FAIL: missing Android simulator file: ${path}" >&2
    exit 1
  fi
done

bash -n "${SIM_SCRIPT}" "${TICKET_ANDROID_SIM_TUNE_LOOP}"

python3 - "${SIM_COMPOSE}" "${SIM_SCRIPT}" "${SIM_RUNBOOK}" "${PROD_COMPOSE}" "${TICKET_REMOTE_DOCKERFILE}" "${TICKET_PHONE_BRIDGE_DOCKERFILE}" "${TICKET_ANDROID_SIM_TUNE_LOOP}" "${DEPLOY_SCRIPT}" <<'PY'
import re
import sys
from pathlib import Path

compose = Path(sys.argv[1]).read_text(encoding="utf-8")
script = Path(sys.argv[2]).read_text(encoding="utf-8")
runbook = Path(sys.argv[3]).read_text(encoding="utf-8")
prod_compose = Path(sys.argv[4]).read_text(encoding="utf-8")
ticket_remote_dockerfile = Path(sys.argv[5]).read_text(encoding="utf-8")
ticket_phone_bridge_dockerfile = Path(sys.argv[6]).read_text(encoding="utf-8")
tune_loop = Path(sys.argv[7]).read_text(encoding="utf-8")
deploy = Path(sys.argv[8]).read_text(encoding="utf-8")

required_compose = [
    "android_sim:",
    "android_sim_tuner:",
    "android_sim_ticket_bridge:",
    "ticket_remote_sim:",
    'cpus: "${ARBUZAS_ANDROID_SIM_CPUS:-2.0}"',
    "mem_limit: ${ARBUZAS_ANDROID_SIM_MEMORY_LIMIT:-6g}",
    "memswap_limit: ${ARBUZAS_ANDROID_SIM_MEMORY_SWAP_LIMIT:-6g}",
    "mem_swappiness: ${ARBUZAS_ANDROID_SIM_MEMORY_SWAPPINESS:-0}",
    'MEMORY: "${ARBUZAS_ANDROID_SIM_MEMORY:-4096}"',
    'CORES: "${ARBUZAS_ANDROID_SIM_CORES:-2}"',
    "/usr/local/bin/ticket-android-sim-tune-loop",
    "TICKET_ANDROID_SIM_ADB_TARGET: android_sim:5555",
    "TICKET_ANDROID_SIM_STATUS_DIR: /srv/android-sim/status",
    "TICKET_ANDROID_SIM_APK_DIR: /srv/android-sim/apks",
    'SKIP_AUTH: "false"',
    "127.0.0.1:${ARBUZAS_ANDROID_SIM_CONSOLE_PORT:-15554}:5554",
    "127.0.0.1:${ARBUZAS_ANDROID_SIM_ADB_PORT:-15555}:5555",
    "127.0.0.1:${ARBUZAS_ANDROID_SIM_TICKET_REMOTE_PORT:-19338}:9338",
    "TICKET_REMOTE_AUTH_MODE: dev",
    "TICKET_REMOTE_STATE_BACKEND: memory",
    "TICKET_REMOTE_PHONE_BASE_URL: http://android_sim_ticket_bridge:9388",
    "/srv/android-sim/apks:ro",
]
for snippet in required_compose:
    if snippet not in compose:
        raise SystemExit(f"missing simulator compose contract snippet: {snippet}")

for forbidden in [
    "cloudflared",
    "tunnel run",
    "ticket.jolkins.id.lv",
    "0.0.0.0:${ARBUZAS_ANDROID_SIM",
]:
    if forbidden in compose:
        raise SystemExit(f"simulator compose must stay private and tunnel-free; found {forbidden}")

required_prod_compose = [
    "ticket_android_sim:",
    "ticket_android_sim_tuner:",
    "ticket_android_sim_bridge:",
    "image: halimqarroum/docker-android:api-33",
    'cpus: "2.0"',
    "mem_limit: 6g",
    "memswap_limit: 6g",
    "mem_swappiness: 0",
    "rm -rf /root/.android/avd/running",
    "find /data -maxdepth 3",
    'MEMORY: "4096"',
    'CORES: "2"',
    "/srv/arbuzas/android-sim/google-apis/avd:/data",
    "/usr/local/bin/ticket-android-sim-tune-loop",
    "TICKET_ANDROID_SIM_ADB_TARGET: ticket_android_sim:5555",
    "TICKET_ANDROID_SIM_STATUS_DIR: /srv/android-sim/status",
    "/srv/arbuzas/android-sim:/srv/android-sim",
    "TICKET_PHONE_ADB_TARGET: ticket_android_sim:5555",
    "TICKET_REMOTE_PHONE_BACKENDS: android-sim|Android simulator|http://ticket_android_sim_bridge:9388;pixel|Pixel|http://ticket_phone_bridge:9388",
    "TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID: android-sim",
    "TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE: /srv/ticket-remote/state/active-phone-backend.json",
    "TICKET_REMOTE_SIMULATOR_SETUP_BACKEND_ID: android-sim",
    "TICKET_REMOTE_SIMULATOR_SETUP_ADB_TARGET: ticket_android_sim:5555",
    "/etc/arbuzas/secrets/android-adb/adbkey:/root/.android/adbkey:ro",
]
for snippet in required_prod_compose:
    if snippet not in prod_compose:
        raise SystemExit(f"missing production simulator contract snippet: {snippet}")

sim_section = re.search(r"  ticket_android_sim:\n(?P<body>.*?)(?=\n  [A-Za-z0-9_-]+:|\Z)", prod_compose, re.S)
if not sim_section:
    raise SystemExit("production compose must include ticket_android_sim")
if re.search(r"\n\s*ports:", sim_section.group("body")):
    raise SystemExit("ticket_android_sim must not publish emulator or ADB ports")
bridge_section = re.search(r"  ticket_android_sim_bridge:\n(?P<body>.*?)(?=\n  [A-Za-z0-9_-]+:|\Z)", prod_compose, re.S)
if not bridge_section:
    raise SystemExit("production compose must include ticket_android_sim_bridge")
if re.search(r"\n\s*ports:", bridge_section.group("body")):
    raise SystemExit("ticket_android_sim_bridge must stay Docker-private")
tuner_section = re.search(r"  ticket_android_sim_tuner:\n(?P<body>.*?)(?=\n  [A-Za-z0-9_-]+:|\Z)", prod_compose, re.S)
if not tuner_section:
    raise SystemExit("production compose must include ticket_android_sim_tuner")
if re.search(r"\n\s*ports:", tuner_section.group("body")):
    raise SystemExit("ticket_android_sim_tuner must stay Docker-private")
remote_section = re.search(r"  ticket_remote:\n(?P<body>.*?)(?=\n  [A-Za-z0-9_-]+:|\Z)", prod_compose, re.S)
if not remote_section:
    raise SystemExit("production compose must include ticket_remote")
remote_body = remote_section.group("body")
if re.search(r"\n\s*ports:", remote_body):
    raise SystemExit("ticket_remote must not publish public media ports; stream stays on Cloudflare HTTPS")
for forbidden in ["TICKET_REMOTE_WEBRTC", "ARBUZAS_TICKET_WEBRTC", "TURN", "49388", "49399", "49400", "49440"]:
    if forbidden in remote_body:
        raise SystemExit(f"ticket_remote must not contain retired public media config: {forbidden}")
if "android-tools-adb" not in ticket_remote_dockerfile:
    raise SystemExit("ticket_remote image must include private ADB tooling for owner simulator control")
if "ticket-android-sim-tune-loop.sh" not in ticket_phone_bridge_dockerfile:
    raise SystemExit("ticket phone bridge image must include the private simulator tuning loop")
for forbidden in ["ticket_android_sim_tunnel", "android_sim_ticket_bridge:", "ticket_remote_sim:"]:
    if forbidden in prod_compose:
        raise SystemExit(f"production compose must not add public/private trial simulator service {forbidden}")
if "ticket_remote_sim" in deploy or "android_sim_ticket_bridge" in deploy:
    raise SystemExit("normal Arbuzas deploy script must not manage private trial simulator services")
required_deploy = [
    "append_unique COMPOSE_TARGET_SERVICES ticket_android_sim",
    "append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_tuner",
    "append_unique COMPOSE_TARGET_SERVICES ticket_android_sim_bridge",
    "prepare_remote_ticket_android_sim_active_backend",
    "ticket_android_sim_active_backend result=preserved",
    "upload_remote_ticket_android_sim_phone_apk",
    "setup_remote_ticket_android_sim",
    "cat > /tmp/ticket-android-sim-setup.sh",
    "ticket_phone_service package=lv.jolkins.pixelorchestrator",
    "INSTALL_FAILED_UPDATE_INCOMPATIBLE",
    "result=signature-mismatch-uninstall",
    "result=reinstalled",
    "ticket Android simulator ADB ready",
    "ticket Android simulator no swap",
    "ticket Android simulator current boot tuned",
    "ticket Android simulator resources",
    "Memory=6442450944",
    "MemorySwap=6442450944",
    "wait_for_remote_ticket_android_sim_tuning",
    "restore-aggressive-packages.sh",
    "swapoff /dev/block/zram0",
    "zram0/reset",
    "zram0/disksize",
    "SwapTotal",
    "540x960",
    "220",
    "background_process_limit 2",
    "cached_apps_freezer enabled",
    "package_tune",
]
for snippet in required_deploy:
    if snippet not in deploy:
        raise SystemExit(f"missing deploy simulator contract snippet: {snippet}")

required_script = [
    'ARBUZAS_ANDROID_SIM_MEMORY="${ARBUZAS_ANDROID_SIM_MEMORY:-4096}"',
    'ARBUZAS_ANDROID_SIM_CORES="${ARBUZAS_ANDROID_SIM_CORES:-2}"',
    "ARBUZAS_ANDROID_SIM_MEMORY must stay 4096",
    "ARBUZAS_ANDROID_SIM_CORES must stay 2",
    'ARBUZAS_ANDROID_SIM_RESOURCE_PROFILE="stable-6gb-total-4gb-guest-2core-noswap"',
    "ARBUZAS_ANDROID_SIM_MEMORY_LIMIT=6g",
    "ARBUZAS_ANDROID_SIM_MEMORY_SWAP_LIMIT=6g",
    "ARBUZAS_ANDROID_SIM_MEMORY_SWAPPINESS=0",
    "--comparison-cores",
    "--force-store-install",
    "compare-cores",
    "reset-state",
    "ARBUZAS_ANDROID_SIM_STATE_MODE",
    "ARBUZAS_ANDROID_SIM_DISPLAY_SIZE",
    "ARBUZAS_ANDROID_SIM_DISPLAY_DENSITY",
    "540x960",
    "220",
    "display_profile",
    "wm size",
    "wm density",
    "swapoff /dev/block/zram0",
    "zram0/reset",
    "zram0/disksize",
    "SwapTotal",
    "android_swap result=disabled",
    "background_process_limit 2",
    "cached_apps_freezer enabled",
    "restore-aggressive-packages.sh",
    "package_tune",
    "install-create -r",
    "install-abandon",
    "halimqarroum/docker-android:api-33",
    "halimqarroum/docker-android:api-33-playstore",
    "https://accrescent.app/accrescent.apk",
    "https://f-droid.org/repo/com.aurora.store_71.apk",
    "app.accrescent.client",
    "com.aurora.store",
    "com.pv.vivi",
    "https://accrescent.app/app/com.pv.vivi",
    "market://details?id=com.pv.vivi",
    "vivi-test",
    "source_policy: no login, official store UI only, no APK mirrors",
    "store_apk_cache",
    "already-installed",
    "force_store_install",
    "Google APIs run did not reach a meaningful store-source result",
    "tap_to_screen_change_ms",
    "home_response_ms",
    "screencap -p",
    "uiautomator dump",
    "final-state",
    "vivi_source_blocked",
    "vivi_open_ok",
    "docker stats --no-stream",
]
for snippet in required_script:
    if snippet not in script:
        raise SystemExit(f"missing simulator script contract snippet: {snippet}")

if script.index("https://accrescent.app/app/com.pv.vivi") > script.index("market://details?id=com.pv.vivi"):
    raise SystemExit("script must try Accrescent before Aurora")

required_runbook = [
    "does not change `ticket.jolkins.id.lv`",
    "4096 MB",
    "6 GB",
    "`2` cores",
    "Swap: disabled",
    "private tuning loop",
    "lighter Google APIs image",
    "reset-state",
    "Store APKs are cached",
    "--force-store-install",
    "`compare-cores`",
    "Accrescent",
    "Aurora",
    "does not use random APK mirrors",
    "does not sign in to Google or ViVi",
    "restore-aggressive-packages.sh",
]
for snippet in required_runbook:
    if snippet not in runbook:
        raise SystemExit(f"missing simulator runbook snippet: {snippet}")

required_tune_loop = [
    "TICKET_ANDROID_SIM_ADB_TARGET",
    "TICKET_ANDROID_SIM_STATUS_DIR",
    "tuning-status.env",
    "swapoff /dev/block/zram0",
    "zram0/reset",
    "SwapTotal",
    "wm size",
    "wm density",
    "background_process_limit 2",
    "cached_apps_freezer enabled",
    "install_if_missing Accrescent",
    "install_if_missing Aurora",
    "install_or_update TicketPhoneService",
    "result=updated",
    "ticket_start_server",
    "boot_id",
]
for snippet in required_tune_loop:
    if snippet not in tune_loop:
        raise SystemExit(f"missing tuning loop contract snippet: {snippet}")

for text in (compose, script, runbook, prod_compose):
    if "6144" in text or "8g" in text or "8192" in text or "CORES=4" in text or 'CORES: "4"' in text:
        raise SystemExit("simulator trial must not drift back to larger resources")

print("PASS: Arbuzas Android simulator trial is private, fixed at 4GB guest/6GB total/2 cores with no swap, and store-sources ViVi only")
PY
