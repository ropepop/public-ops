#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_PATH="${REPO_ROOT}/tools/arbuzas/deploy.sh"
COMPOSE_PATH="${REPO_ROOT}/infra/arbuzas/docker/compose.yml"
TRAIN_DOCKERFILE_PATH="${REPO_ROOT}/infra/arbuzas/docker/images/train-bot.Dockerfile"
SATIKSME_DOCKERFILE_PATH="${REPO_ROOT}/infra/arbuzas/docker/images/satiksme-bot.Dockerfile"
TICKET_PHONE_BRIDGE_DOCKERFILE_PATH="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge.Dockerfile"
TICKET_PHONE_BRIDGE_HEALTH_PATH="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge-health.sh"
ARBUZAS_EXAMPLE_ENV_PATH="${REPO_ROOT}/infra/arbuzas/docker/env/arbuzas.example.env"
HOST_MIRROR_PATH="${REPO_ROOT}/tools/arbuzas/host_mirror.py"
ACTIVE_PHONE_BROKER_WORKLOAD_PATH="${REPO_ROOT}/workloads/phone-broker"
ARCHIVED_PHONE_BROKER_PATH="${REPO_ROOT}/archive/phone-broker"
TRAIN_LDFLAGS_PATH="${REPO_ROOT}/workloads/train-bot/scripts/ldflags.sh"
SATIKSME_LDFLAGS_PATH="${REPO_ROOT}/workloads/satiksme-bot/scripts/ldflags.sh"
MEMORY_REPORT_PATH="${REPO_ROOT}/tools/arbuzas/memory_report.py"
LOCAL_RELEASE_GC_PATH="${REPO_ROOT}/tools/arbuzas/local_release_gc.py"
MEMORY_REPORT_DEFAULT_PATH="${REPO_ROOT}/infra/arbuzas/memory-report/etc/default/arbuzas-memory-report"
MEMORY_REPORT_SERVICE_PATH="${REPO_ROOT}/infra/arbuzas/memory-report/etc/systemd/system/arbuzas-memory-report.service"
MEMORY_REPORT_TIMER_PATH="${REPO_ROOT}/infra/arbuzas/memory-report/etc/systemd/system/arbuzas-memory-report.timer"
NETDATA_CONFIG_PATH="${REPO_ROOT}/infra/arbuzas/netdata/netdata.conf"
NETDATA_DOCKER_CONFIG_PATH="${REPO_ROOT}/infra/arbuzas/netdata/go.d/docker.conf"
NETDATA_DOCKER_SD_CONFIG_PATH="${REPO_ROOT}/infra/arbuzas/netdata/go.d/sd/docker.conf"
THINKPAD_FAN_DEFAULT_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/etc/default/arbuzas-thinkpad-fan"
THINKPAD_FAN_MODPROBE_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/etc/modprobe.d/arbuzas-thinkpad-fan.conf"
THINKPAD_FAN_SERVICE_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/etc/systemd/system/arbuzas-thinkpad-fan.service"
THINKPAD_FAN_SCRIPT_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/usr/local/libexec/arbuzas-thinkpad-fan.py"

if [[ ! -f "${SCRIPT_PATH}" ]]; then
  echo "FAIL: missing Arbuzas deploy script at ${SCRIPT_PATH}" >&2
  exit 1
fi

if [[ ! -f "${COMPOSE_PATH}" ]]; then
  echo "FAIL: missing Arbuzas compose file at ${COMPOSE_PATH}" >&2
  exit 1
fi

for release_identity_file in \
  "${TRAIN_DOCKERFILE_PATH}" \
  "${SATIKSME_DOCKERFILE_PATH}" \
  "${TRAIN_LDFLAGS_PATH}" \
  "${SATIKSME_LDFLAGS_PATH}"; do
  if [[ ! -f "${release_identity_file}" ]]; then
    echo "FAIL: missing release identity build file at ${release_identity_file}" >&2
    exit 1
  fi
done

if [[ ! -f "${TICKET_PHONE_BRIDGE_HEALTH_PATH}" ]]; then
  echo "FAIL: missing ticket phone bridge health script at ${TICKET_PHONE_BRIDGE_HEALTH_PATH}" >&2
  exit 1
fi

if [[ ! -f "${MEMORY_REPORT_PATH}" ]]; then
  echo "FAIL: missing Arbuzas memory reporter at ${MEMORY_REPORT_PATH}" >&2
  exit 1
fi

if [[ ! -f "${LOCAL_RELEASE_GC_PATH}" ]]; then
  echo "FAIL: missing Arbuzas local release cleanup helper at ${LOCAL_RELEASE_GC_PATH}" >&2
  exit 1
fi

for memory_report_file in \
  "${MEMORY_REPORT_DEFAULT_PATH}" \
  "${MEMORY_REPORT_SERVICE_PATH}" \
  "${MEMORY_REPORT_TIMER_PATH}"; do
  if [[ ! -f "${memory_report_file}" ]]; then
    echo "FAIL: missing Arbuzas memory report service file at ${memory_report_file}" >&2
    exit 1
  fi
done

for ticket_phone_bridge_image_snippet in \
  "curl" \
  "ticket-phone-bridge-health.sh" \
  "ticket-phone-bridge-health" \
  "USER 1002:1002" \
  "ENV HOME=/home/ticketbridge"; do
  if ! grep -F "${ticket_phone_bridge_image_snippet}" "${TICKET_PHONE_BRIDGE_DOCKERFILE_PATH}" >/dev/null; then
    echo "FAIL: ticket phone bridge image must include health tooling: ${ticket_phone_bridge_image_snippet}" >&2
    exit 1
  fi
done

for ticket_phone_bridge_compose_snippet in \
  "TICKET_PHONE_HEALTH_INTERVAL" \
  "/usr/local/bin/ticket-phone-bridge-health >/dev/null" \
  'user: "1002:1002"' \
  "read_only: true" \
  "no-new-privileges:true" \
  "/home/ticketbridge/.android/adbkey:ro"; do
  if ! grep -F "${ticket_phone_bridge_compose_snippet}" "${COMPOSE_PATH}" >/dev/null; then
    echo "FAIL: ticket_phone_bridge compose healthcheck is missing or incomplete: ${ticket_phone_bridge_compose_snippet}" >&2
    exit 1
  fi
done

if grep -Eq '^  phone_broker:' "${COMPOSE_PATH}"; then
  echo "FAIL: retired phone_broker service remains in compose.yml" >&2
  exit 1
fi
if [[ -e "${ACTIVE_PHONE_BROKER_WORKLOAD_PATH}" ]]; then
  echo "FAIL: retired phone broker remains under active workloads" >&2
  exit 1
fi
for archived_phone_broker_file in \
  "${ARCHIVED_PHONE_BROKER_PATH}/README.md" \
  "${ARCHIVED_PHONE_BROKER_PATH}/phone-broker.Dockerfile" \
  "${ARCHIVED_PHONE_BROKER_PATH}/workload/go.mod"; do
  if [[ ! -f "${archived_phone_broker_file}" ]]; then
    echo "FAIL: missing preserved phone broker archive file at ${archived_phone_broker_file}" >&2
    exit 1
  fi
done
for retired_phone_broker_config in \
  "${ARBUZAS_EXAMPLE_ENV_PATH}" \
  "${HOST_MIRROR_PATH}"; do
  if grep -E 'phone_broker|ARBUZAS_PHONE_BROKER_PORT' "${retired_phone_broker_config}" >/dev/null; then
    echo "FAIL: retired phone broker remains in active deployment configuration: ${retired_phone_broker_config}" >&2
    exit 1
  fi
done

python3 - "${HOST_MIRROR_PATH}" <<'PY'
import importlib.util
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
spec = importlib.util.spec_from_file_location("arbuzas_host_mirror", path)
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)

managed = {entry.rel for entry in module.PROFILES["arbuzas"]}
if "etc/arbuzas/current/release.env" in managed:
    raise SystemExit("generated current/release.env must not be managed by the host mirror")
if "ticket_remote_spacetime_sidecar" not in module.SERVICE_ORDER:
    raise SystemExit("host mirror service order omits the Ticket Remote Spacetime sidecar")

expected = {
    "etc/arbuzas/env/ticket-remote.env": {"ticket_remote_spacetime_sidecar", "ticket_remote"},
    "etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret": {
        "ticket_remote_spacetime_sidecar",
        "ticket_remote",
    },
    "etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem": {
        "ticket_remote_spacetime_sidecar",
        "chatgpt_broker",
    },
}
for rel, required in expected.items():
    actual = module.affected_services_for_path(rel)
    missing = required - actual
    if missing:
        raise SystemExit(f"host mirror restart mapping for {rel} omits: {sorted(missing)}")
PY

if ! grep -F 'ARBUZAS_TRAIN_BOT_HOSTNAME="${ARBUZAS_TRAIN_BOT_HOSTNAME:-vilciens.kontrole.info}"' "${SCRIPT_PATH}" >/dev/null; then
  echo "FAIL: Arbuzas train tunnel default must use vilciens.kontrole.info" >&2
  exit 1
fi

for train_public_base_snippet in \
  "TRAIN_WEB_PUBLIC_BASE_URL: https://\${ARBUZAS_TRAIN_BOT_HOSTNAME}" \
  "export TRAIN_WEB_PUBLIC_BASE_URL=\"https://\${ARBUZAS_TRAIN_BOT_HOSTNAME}\"" \
  "TRAIN_WEB_TEST_LOGIN_ENABLED: \"false\"" \
  "export TRAIN_WEB_TEST_LOGIN_ENABLED=\"false\""; do
  if ! grep -F "${train_public_base_snippet}" "${COMPOSE_PATH}" >/dev/null; then
    echo "FAIL: train_bot production web setting is missing or misaligned: ${train_public_base_snippet}" >&2
    exit 1
  fi
done

for satiksme_public_base_snippet in \
  "SATIKSME_WEB_PUBLIC_BASE_URL: https://\${ARBUZAS_SATIKSME_BOT_HOSTNAME}" \
  "export SATIKSME_WEB_PUBLIC_BASE_URL=\"https://\${ARBUZAS_SATIKSME_BOT_HOSTNAME}\""; do
  if ! grep -F "${satiksme_public_base_snippet}" "${COMPOSE_PATH}" >/dev/null; then
    echo "FAIL: satiksme_bot production web setting is missing or misaligned: ${satiksme_public_base_snippet}" >&2
    exit 1
  fi
done

for satiksme_live_viewer_snippet in \
  "SATIKSME_WEB_LIVE_VIEWER_HEARTBEAT_ENABLED: \"true\"" \
  "export SATIKSME_WEB_LIVE_VIEWER_HEARTBEAT_ENABLED=\"true\""; do
  if ! grep -F "${satiksme_live_viewer_snippet}" "${COMPOSE_PATH}" >/dev/null; then
    echo "FAIL: satiksme_bot live map heartbeat setting is missing or misaligned: ${satiksme_live_viewer_snippet}" >&2
    exit 1
  fi
done
if ! grep -F "shell missing public live viewer heartbeat writes" "${SCRIPT_PATH}" >/dev/null; then
  echo "FAIL: satiksme deploy validation must require live viewer heartbeat writes" >&2
  exit 1
fi
if grep -F "shell enables public live viewer heartbeat writes" "${SCRIPT_PATH}" >/dev/null; then
  echo "FAIL: satiksme deploy validation must not reject live viewer heartbeat writes" >&2
  exit 1
fi

for release_build_arg in \
  "ARBUZAS_RELEASE_ID" \
  "ARBUZAS_RELEASE_SOURCE_COMMIT" \
  "ARBUZAS_RELEASE_SOURCE_DIRTY" \
  "ARBUZAS_RELEASE_SOURCE_SHA256"; do
  compose_count="$(grep -F "${release_build_arg}: \${${release_build_arg}}" "${COMPOSE_PATH}" || true)"
  compose_count="$(printf '%s\n' "${compose_count}" | sed '/^$/d' | wc -l | tr -d ' ')"
  if [[ "${compose_count}" -lt 2 ]]; then
    echo "FAIL: train_bot and satiksme_bot builds must receive ${release_build_arg}" >&2
    exit 1
  fi
  for dockerfile_path in "${TRAIN_DOCKERFILE_PATH}" "${SATIKSME_DOCKERFILE_PATH}"; do
    if ! grep -F "ARG ${release_build_arg}" "${dockerfile_path}" >/dev/null; then
      echo "FAIL: $(basename "${dockerfile_path}") must declare ${release_build_arg}" >&2
      exit 1
    fi
    if ! grep -F "export ${release_build_arg}" "${dockerfile_path}" >/dev/null; then
      echo "FAIL: $(basename "${dockerfile_path}") must export ${release_build_arg} for ldflags" >&2
      exit 1
    fi
  done
done

for ldflags_path in "${TRAIN_LDFLAGS_PATH}" "${SATIKSME_LDFLAGS_PATH}"; do
  for ldflags_snippet in \
    "ARBUZAS_RELEASE_ID" \
    "ARBUZAS_RELEASE_SOURCE_COMMIT" \
    "ARBUZAS_RELEASE_SOURCE_DIRTY" \
    "ARBUZAS_RELEASE_SOURCE_SHA256" \
    "version.ReleaseID" \
    "version.SourceSHA256"; do
    if ! grep -F "${ldflags_snippet}" "${ldflags_path}" >/dev/null; then
      echo "FAIL: $(basename "${ldflags_path}") must stamp ${ldflags_snippet}" >&2
      exit 1
    fi
  done
done

for ticket_remote_runtime_snippet in \
  'export TICKET_REMOTE_AUTH_MODE="$${TICKET_REMOTE_AUTH_MODE:-spacetime}"' \
  'export TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN="$${TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN:-}"' \
  'export TICKET_REMOTE_CF_ACCESS_AUDIENCE="$${TICKET_REMOTE_CF_ACCESS_AUDIENCE:-}"'; do
  if ! grep -F "${ticket_remote_runtime_snippet}" "${COMPOSE_PATH}" >/dev/null; then
    echo "FAIL: ticket_remote auth env must be resolved at container runtime, not during Compose rendering: ${ticket_remote_runtime_snippet}" >&2
    exit 1
  fi
done

if [[ ! -f "${NETDATA_CONFIG_PATH}" ]]; then
  echo "FAIL: missing Arbuzas Netdata config at ${NETDATA_CONFIG_PATH}" >&2
  exit 1
fi

for netdata_override in "${NETDATA_DOCKER_CONFIG_PATH}" "${NETDATA_DOCKER_SD_CONFIG_PATH}"; do
  if [[ ! -f "${netdata_override}" ]]; then
    echo "FAIL: missing Arbuzas Netdata Docker override at ${netdata_override}" >&2
    exit 1
  fi
  if ! grep -F "disabled: yes" "${netdata_override}" >/dev/null; then
    echo "FAIL: Arbuzas Netdata Docker overrides must stay disabled" >&2
    exit 1
  fi
done

if ! grep -F "[web]" "${NETDATA_CONFIG_PATH}" >/dev/null || ! grep -F "bind to = localhost:19999" "${NETDATA_CONFIG_PATH}" >/dev/null; then
  echo "FAIL: Arbuzas Netdata config must keep the dashboard bound to localhost:19999" >&2
  exit 1
fi

for thinkpad_fan_file in \
  "${THINKPAD_FAN_DEFAULT_PATH}" \
  "${THINKPAD_FAN_MODPROBE_PATH}" \
  "${THINKPAD_FAN_SERVICE_PATH}" \
  "${THINKPAD_FAN_SCRIPT_PATH}"; do
  if [[ ! -f "${thinkpad_fan_file}" ]]; then
    echo "FAIL: missing Arbuzas ThinkPad fan control file at ${thinkpad_fan_file}" >&2
    exit 1
  fi
done

if ! grep -F "options thinkpad_acpi fan_control=1" "${THINKPAD_FAN_MODPROBE_PATH}" >/dev/null; then
  echo "FAIL: Arbuzas ThinkPad fan modprobe config must enable manual fan control" >&2
  exit 1
fi

# DNS controlplane retired 2026-06-21 — confirm no DNS service or admin surface remains.
if grep -F "dns_controlplane" "${COMPOSE_PATH}" >/dev/null; then
  echo "FAIL: dns_controlplane is retired; compose.yml must not include it" >&2
  exit 1
fi
if grep -F "arbuzas-dns-admin" "${SCRIPT_PATH}" >/dev/null; then
  echo "FAIL: arbuzas-dns-admin nginx site is retired; deploy.sh must not reference it" >&2
  exit 1
fi
if grep -F "DNS_ADMIN_NGINX" "${SCRIPT_PATH}" >/dev/null; then
  echo "FAIL: DNS_ADMIN_NGINX_* constants are retired; deploy.sh must not define them" >&2
  exit 1
fi

help_output="$(${SCRIPT_PATH} --help)"
if ! grep -F -- "--validation-profile fast|standard|full" <<< "${help_output}" >/dev/null; then
  echo "FAIL: deploy help does not document validation profiles" >&2
  exit 1
fi

assert_cli_rejected() {
  local expected_message="$1"
  shift
  local output=""
  local status=0

  set +e
  output="$(${SCRIPT_PATH} "$@" 2>&1)"
  status=$?
  set -e
  if [[ "${status}" -ne 2 ]]; then
    echo "FAIL: expected CLI rejection status 2 for: $*" >&2
    exit 1
  fi
  if ! grep -F -- "${expected_message}" <<< "${output}" >/dev/null; then
    echo "FAIL: CLI rejection did not explain '$expected_message' for: $*" >&2
    exit 1
  fi
}

assert_cli_rejected "Unknown validation profile: invalid" deploy --services ticket_remote --validation-profile invalid
assert_cli_rejected "Validation profile fast requires --services" deploy --validation-profile fast
assert_cli_rejected "--validation-profile is only supported for deploy, validate, and rollback" cleanup-docker --validation-profile standard
assert_cli_rejected "Unknown service: phone_broker" validate --services phone_broker

if ! python3 - "${SCRIPT_PATH}" <<'PY'
import sys
from pathlib import Path

script = Path(sys.argv[1]).read_text(encoding="utf-8")

required_snippets = [
    "cleanup-docker    Run the Arbuzas Docker image, release, build-cache, and host-cache cleanup policy on the live host",
    "memory-report     Report corrected host memory pressure and provider-like cached-inclusive memory from /proc/meminfo",
    "install-memory-report   Install the corrected host memory report service and timer on the live host",
    "validate-memory-report  Validate the corrected host memory report service, timer, and latest snapshot",
    "install-netdata   Install Netdata plus hardware monitoring packages on the live host and publish it privately over Tailscale",
    "validate-netdata  Validate the live Netdata host install, private Tailscale access, and expected Arbuzas hardware charts",
    "install-thinkpad-fan   Install the Arbuzas ThinkPad fan controller on the live host",
    "validate-thinkpad-fan  Validate the live ThinkPad fan controller and current control mode",
    "mirror-pull       Pull deployment variables and secrets from the host into the local plaintext mirror",
    "mirror-audit      Compare the local host mirror with the host and report drift before deploy",
    "mirror-push       Push local host mirror changes to the host when the host has not drifted",
    "deploy-config     Push local mirror changes and restart/reload only affected services; no build or release upload",
    "deploy|validate|rollback|cleanup-docker|memory-report|install-memory-report|validate-memory-report|install-netdata|validate-netdata|install-thinkpad-fan|validate-thinkpad-fan|repair-portainer|mirror-pull|mirror-audit|mirror-push|deploy-config)",
    "--release-id is not supported for cleanup-docker",
    "--release-id is not supported for memory-report",
    "--release-id is not supported for install-memory-report",
    "--release-id is not supported for validate-memory-report",
    "--release-id is not supported for install-netdata",
    "--release-id is not supported for validate-netdata",
    "--release-id is not supported for install-thinkpad-fan",
    "--release-id is not supported for validate-thinkpad-fan",
    "--services NAME[,NAME...]",
    "--validation-profile fast|standard|full",
    "--validation-profile|--validation-level",
    'VALIDATION_PROFILE="${ARBUZAS_VALIDATION_PROFILE:-full}"',
    "Unknown validation profile: ${VALIDATION_PROFILE}; expected fast, standard, or full",
    "Validation profile ${VALIDATION_PROFILE} requires --services",
    "--services is only supported for deploy, validate, and rollback",
    "--services requires at least one service name",
    "remote_run_docker_gc()",
    "run_local_release_cleanup()",
    "remote_run_memory_report()",
    "remote_run_host_cache_cleanup()",
    "resolve_local_docker_gc_script()",
    "run_automatic_remote_docker_gc()",
    "compose_target_service_args_without_tunnels()",
    "compose_target_tunnel_service_args()",
    "compose_all_service_args()",
    "compose_all_tunnel_service_args()",
    "--no-deps --remove-orphans${non_tunnel_service_args}",
    "--no-deps --remove-orphans${tunnel_service_args}",
    "--no-deps --remove-orphans${service_args}",
    "render_remote_cloudflared_configs()",
    "cleanup_remote_public_bundle_versions()",
    "run_timed_phase()",
    "Phase timing: phase=${phase_name}",
    "prepare_local_fast_release_overlay()",
    "copy_fast_release_overlay_to_remote()",
    '${FAST_RELEASE_OVERLAY_PATHS[@]+"${FAST_RELEASE_OVERLAY_PATHS[@]}"}',
    "trap - RETURN",
    "validate_remote_selected_smoke_health()",
    "run_post_deploy_maintenance()",
    'LOCAL_RELEASE_GC_SCRIPT="${SCRIPT_DIR}/local_release_gc.py"',
    'ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS="${ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS:-72}"',
    'ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY="${ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY:-10}"',
    'ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN="${ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN:-false}"',
    '--releases-root "${LOCAL_RELEASES_ROOT}"',
    '--protect-release-id "${protect_release_id}"',
    'Remote cleanup deferred by ${VALIDATION_PROFILE} profile; local expired release cleanup still completed',
    'run_post_deploy_maintenance "${requested_release_id}"',
    "compute_release_source_sha256()",
    "validate_remote_release_identity()",
    "ARBUZAS_RELEASE_SOURCE_COMMIT",
    "ARBUZAS_RELEASE_SOURCE_DIRTY",
    "ARBUZAS_RELEASE_SOURCE_SHA256",
    "releaseId",
    "sourceSha256",
    "public bundle cleanup target=train_bot",
    "public bundle cleanup target=satiksme_bot",
    "version_dirs(versions_root)",
    "reason=missing-active-no-versions",
    "missing active while version dirs exist",
    "empty active while version dirs exist",
    "active version directory is missing",
    "stage_netdata_config_to_remote()",
    "install_remote_netdata()",
    "validate_remote_netdata()",
    "stage_memory_report_config_to_remote()",
    "install_remote_memory_report()",
    "validate_remote_memory_report()",
    "stage_thinkpad_fan_config_to_remote()",
    "install_remote_thinkpad_fan()",
    "validate_remote_thinkpad_fan()",
    '--exclude="${path}/.env"',
    '--exclude="${path}/.env.*"',
    '--exclude="${path}/.DS_Store"',
    '--exclude="${path}/state"',
    '--exclude="${path}/*.env"',
    '--exclude="${path}/*.secret"',
    '--exclude="${path}/*.db"',
    '--exclude="${path}/*.db.lock"',
    '--exclude="${path}/*.instance.lock"',
    '--exclude="${path}/data/*.db"',
    '--exclude="${path}/data/*.db.lock"',
    '--exclude="${path}/data/catalog"',
    '--exclude="${path}/data/public-bundles"',
    '--exclude="${path}/data/schedules/*.json"',
    '--exclude="${path}/spacetimedb/dist"',
    '--exclude="${path}/web-client/src/generated"',
    'copy_tree_into_release "tools/arbuzas"',
    'pathlib.Path("tools/arbuzas")',
    'tools/arbuzas test_arbuzas_deploy_contract.sh test_ticket_phone_bridge_hardening.sh',
    'MEMORY_REPORT_SCRIPT="${SCRIPT_DIR}/memory_report.py"',
    "python3 - --source-label '/proc/meminfo on ${ARBUZAS_HOST}'",
    'MEMORY_REPORT_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/memory-report"',
    'MEMORY_REPORT_REMOTE_SERVICE_FILE="/etc/systemd/system/arbuzas-memory-report.service"',
    'MEMORY_REPORT_REMOTE_TIMER_FILE="/etc/systemd/system/arbuzas-memory-report.timer"',
    'MEMORY_REPORT_REMOTE_DEFAULT_FILE="/etc/default/arbuzas-memory-report"',
    'MEMORY_REPORT_REMOTE_SCRIPT_FILE="/usr/local/libexec/arbuzas-memory-report.py"',
    'MEMORY_REPORT_REMOTE_JSON_FILE="${MEMORY_REPORT_REMOTE_OUTPUT_DIR}/latest.json"',
    'MEMORY_REPORT_REMOTE_TEXT_FILE="${MEMORY_REPORT_REMOTE_OUTPUT_DIR}/latest.txt"',
    'MEMORY_REPORT_REMOTE_PROM_FILE="${MEMORY_REPORT_REMOTE_OUTPUT_DIR}/latest.prom"',
    "COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C \"${MEMORY_REPORT_CONFIG_ROOT}\" -cf - . | base64 | tr -d '\\n'",
    "printf '%s' '${memory_report_tree_base64}' | base64 -d | tar -xf - -C '${remote_tmp_dir}'",
    "printf '%s' '${memory_report_script_base64}' | base64 -d > '${remote_tmp_dir}/usr/local/libexec/arbuzas-memory-report.py'",
    "systemctl enable arbuzas-memory-report.timer >/dev/null",
    "systemctl restart arbuzas-memory-report.timer",
    "systemctl start arbuzas-memory-report.service",
    "Validate: corrected memory report publishes real pressure and cache separately",
    "arbuzas_memory_real_pressure_percent",
    'gc_script="$(resolve_local_docker_gc_script)"',
    'DOCKER_GC_RELEASE_KEEP_PER_FAMILY="${DOCKER_GC_RELEASE_KEEP_PER_FAMILY:-10}"',
    "DOCKER_GC_RELEASE_KEEP_PER_FAMILY must be a non-negative integer",
    'ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS="${ARBUZAS_HOST_CLEANUP_TMP_MIN_AGE_DAYS:-7}"',
    'ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE="${ARBUZAS_HOST_CLEANUP_JOURNAL_MAX_SIZE:-100M}"',
    'ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE="${ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE:-true}"',
    "ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE must be true or false",
    "--release-keep-per-family '${DOCKER_GC_RELEASE_KEEP_PER_FAMILY}'",
    "apt-get clean",
    "-name 'arbuzas-*'",
    "-name 'satiksme-*'",
    "-name 'chat-analyzer-*'",
    "-name 'ticket-*'",
    "-name 'speedtest-install.*'",
    "journalctl --vacuum-size=\\\"\\${journal_max_size}\\\"",
    "drop_reclaimable_cache='${ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE}'",
    "report_memory 'before reclaimable cache flush'",
    "printf '3\\n' > /proc/sys/vm/drop_caches",
    "report_memory 'after reclaimable cache flush'",
    "reclaimable cache flush skipped because ARBUZAS_HOST_DROP_RECLAIMABLE_CACHE=false",
    "missing Docker GC helper locally and on the current Arbuzas release bundle",
    "gc_script='${REMOTE_CURRENT_LINK}/tools/arbuzas/docker_gc.py'",
    "release_static = pathlib.Path('${remote_release_dir}') / 'workloads/train-bot/internal/web/static'",
    "release_static = pathlib.Path('${remote_release_dir}') / 'workloads/satiksme-bot/internal/web/static'",
    "def expected_asset_body(path):",
    "def strip_named_js_function(source, name):",
    "root shell does not reference release asset hash",
    "public asset {asset} hash {actual} does not match release hash {expected}",
    "public asset {path} exposes private hostname",
    "private_hostname_patterns = [",
    "cfargotunnel",
    "--out '${remote_release_dir}/generated/cloudflared/train-bot.yml'",
    "--out '${remote_release_dir}/generated/cloudflared/satiksme-bot.yml'",
    "--out '${remote_release_dir}/generated/cloudflared/subscription-bot.yml'",
    "--out '${remote_release_dir}/generated/cloudflared/ticket-remote.yml'",
    "append_unique COMPOSE_TARGET_SERVICES train_tunnel",
    "ticket_phone_bridge ticket_remote_spacetime_sidecar ticket_remote ticket_remote_tunnel",
    "ticket-remote Spacetime sidecar health",
    "ticket_remote_spacetime_sidecar",
    "ticket-phone-bridge local health",
    "ticket-remote direct bridge health",
    "/usr/local/bin/ticket-phone-bridge-health",
    "VALIDATE_TICKET_PHONE_BRIDGE",
    "validate_remote_ticket_phone_bridge_workload_health",
    "TICKET_REMOTE_PHONE_BASE_URL",
    "http://ticket_phone_bridge:9388",
    "ticket-remote stale viewer code absent",
    "ARBUZAS_TICKET_REMOTE_AUTH_MODE=${ARBUZAS_TICKET_REMOTE_AUTH_MODE:-spacetime}",
    "ARBUZAS_TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN=${ARBUZAS_TICKET_REMOTE_CF_ACCESS_TEAM_DOMAIN:-}",
    "ARBUZAS_TICKET_REMOTE_CF_ACCESS_AUDIENCE=${ARBUZAS_TICKET_REMOTE_CF_ACCESS_AUDIENCE:-}",
    "TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID",
    "TICKET_REMOTE_SESSION_SIGNING_KEY",
    "ticket-remote runtime OIDC issuer",
    "TICKET_REMOTE_SPACETIME_OIDC_ISSUER",
    "https://${ARBUZAS_TRAIN_BOT_HOSTNAME}/oidc",
    "jwks.json",
    "/api/v1/livez",
    "claim-dialog|showModal|confirmClaim",
    "options.tap.x",
    "control_code_button",
    "quick_claim_tap",
    "runControlMutation",
    "claimControl()",
    "releaseControl(",
    "revokeControl(",
    "inputQueueLimit = 30",
    "inputDrainDelayMs = 35",
    "/api/v1/control-code/request",
    "/api/v1/control-code/close",
    "control_code_request",
    "generate_control_code",
    "requestControlCode",
    "sanitizeControlDigits",
    "navigator.wakeLock.request",
    "requestFullscreen",
    "toolbarCollapseAnchorPx",
    "--ticket-viewport-height",
    "mozBrightness|AmbientLightSensor",
    "gesturechange",
    "dblclick",
    "touch-action: pan-y",
    "ctx.drawImage",
    "ticket-remote active configured backend",
    "validate_remote_selected_workload_health",
    "validate_remote_current_release_link",
    "mark_remote_validation_failed()",
    "return_remote_validation_status()",
    "validate_remote_satiksme_dependency_dns",
    "getent hosts saraksti.rigassatiksme.lv",
    "Validation failed: satiksme dependency DNS",
    "validate_remote_train_public_hardening",
    "validate_remote_train_anonymous_data_denial",
    "validate_remote_satiksme_public_hardening",
    "validate_remote_public_tls_dns_hardening",
    "train public web hardening",
    "train anonymous direct data access is denied",
    "satiksme public web hardening",
    "public TLS and DNS hardening",
    "/assets/app.test.js",
    "/assets/app.js.map",
    "/assets/live-client.js",
    "/api/v1/auth/test",
    "/pixel-stack/train/api/v1/health",
    "/service-worker.js",
    "/spacetimedb/dist/bundle.js",
    "app.js?v={app_hash}&debug=1",
    "assert_no_train_bot_headers",
    "assert_no_satiksme_headers",
    "assert_cloudflare_script_order_guard",
    "assert_vary_accept_encoding",
    "assert_unversioned_asset_range_not_partial",
    "assert_immutable_public_asset_cache",
    "assert_public_json_cache_not_long_immutable",
    "non_current_asset_hash",
    "missing immutable public asset cache",
    "public JSON cache is immutable",
    "'Range': 'bytes=0-63'",
    "range request returned Content-Range",
    "HEAD /robots.txt returned",
    "Cloudflare-managed robots.txt is missing expected content signals",
    "/api/v1/public/service-day-trains?debug=1",
    "/api/v1/messages?lang=lv&lang=en",
    "/__outside-audit-404",
    "/.well-known/security.txt",
    "/favicon.ico",
    "/site.webmanifest",
    "/apple-touch-icon.png",
    "HEAD {path} returned {status}, want 200",
    "exposes repeated per-train sourceVersion",
    "shell exposes public sourceVersion",
    "train active bundle pointer exposes sourceVersion",
    "train active bundle manifest /assets/{manifest_path} exposes sourceVersion",
    "public dashboard HEAD cache redirect returned",
    "sourceMappingURL=",
    "'\\\"__\\\" + \\\"test__\\\"'",
    "resetStateForTest",
    "malformed Telegram login leaks validation detail",
    "legacy malformed Telegram login leaks validation detail",
    "unauthenticated {path} returned {status}, want 401",
    "public live viewer heartbeat route is enabled for {method}",
    "oidc discovery exposes internal smoke claim",
    "/api/v1/public/dashboard?limit=2001",
    "unknown public train shell {path} returned {status}, want 404",
    "/bundles/no-such-version/manifest.json",
    "live snapshot active path is not under transport/live",
    "invalid public incident limit",
    "invalid public sightings limit",
    "unexpected public query",
    "public incident detail event id is not opaque",
    "/api/v1/public/live-vehicles",
    "test_ticket",
    "stripTestTicketFromLocation",
    "trainbot_service_get_schedule",
    "trainbot_service_list_activities",
    "trainbot_submit_report",
    "trainbot_vote_incident",
    "trainbot_comment_incident",
    "trainbot_begin_service_day_import",
    "trainbot_run_trainbot_job",
    "trainbot_my_profile",
    "satiksmebot_bootstrap_me",
    "satiksmebot_list_recent_reports",
    "satiksmebot_submit_stop_report",
    "satiksmebot_submit_vehicle_report",
    "satiksmebot_submit_area_report",
    "satiksmebot_vote_incident",
    "satiksmebot_comment_incident",
    "satiksmebot_heartbeat_live_viewer",
    "satiksmebot_set_live_viewer_state",
    "satiksmebot_service_pending_report_dump_count",
    "satiksmebot_stop_sighting",
    "satiksmebot_incident_comment",
    "stale query-versioned asset remained public",
    "/robots.txt",
    "public shell exposes preview metadata",
    "TLS 1.0 unexpectedly accepted",
    "TLS 1.1 unexpectedly accepted",
    "dig +short CAA kontrole.info",
    "run_ssh() {",
    "run_scp() {",
    "is_valid_ipv6()",
    "NETDATA_REMOTE_CONFIG_DIR=\"/etc/netdata\"",
    "NETDATA_REMOTE_DOCKER_CONFIG_FILE=\"${NETDATA_REMOTE_CONFIG_DIR}/go.d/docker.conf\"",
    "NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE=\"${NETDATA_REMOTE_CONFIG_DIR}/go.d/sd/docker.conf\"",
    "COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C \"${NETDATA_CONFIG_ROOT}\" -cf - . | base64 | tr -d '\\n'",
    "printf '%s' '${netdata_config_tree_base64}' | base64 -d | tar -xf - -C '${remote_tmp_dir}'",
    "tar -C '${remote_stage_root}' -cf - . | tar -C '${NETDATA_REMOTE_CONFIG_DIR}' -xf -",
    'remote_tarball="/tmp/arbuzas-${ARBUZAS_RELEASE_ID}.$$.tar"',
    'local_tarball="$(mktemp "${TMPDIR:-/tmp}/arbuzas-${ARBUZAS_RELEASE_ID}.XXXXXX.tar")"',
    'log "Packing release bundle ${ARBUZAS_RELEASE_ID}"',
    'log "Uploading release bundle to ${ARBUZAS_HOST}:${remote_tarball}"',
    'upload_remote_file "${local_tarball}" "${remote_tarball}"',
    "grep -F 'disabled: yes' '${NETDATA_REMOTE_DOCKER_CONFIG_FILE}' >/dev/null",
    "grep -F 'disabled: yes' '${NETDATA_REMOTE_DOCKER_SD_CONFIG_FILE}' >/dev/null",
    "unexpected Docker charts still enabled:",
    "collector=docker|/images/json|/containers/json",
    "tailscale serve --bg --yes --tcp ${ARBUZAS_NETDATA_PORT} 127.0.0.1:${ARBUZAS_NETDATA_PORT}",
    "curl -fsS 'http://127.0.0.1:${ARBUZAS_NETDATA_PORT}/api/v1/info'",
    "curl -fsS \"http://${tailnet_ipv4}:${ARBUZAS_NETDATA_PORT}/api/v1/info\"",
    "rm -f /var/lib/netdata/cloud.d/claim.conf",
    "[[ ! -f /var/lib/netdata/cloud.d/claim.conf ]]",
    'THINKPAD_FAN_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/thinkpad-fan"',
    "COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata -C \"${THINKPAD_FAN_CONFIG_ROOT}\" -cf - . | base64 | tr -d '\\n'",
    "printf '%s' '${thinkpad_fan_tree_base64}' | base64 -d | tar -xf - -C '${remote_tmp_dir}'",
    "chmod 0755 '${THINKPAD_FAN_REMOTE_SCRIPT_FILE}'",
    "modprobe thinkpad_acpi fan_control=1",
    "systemctl enable arbuzas-thinkpad-fan.service >/dev/null",
    "systemctl restart arbuzas-thinkpad-fan.service",
    "grep -Fx 'options thinkpad_acpi fan_control=1' '${THINKPAD_FAN_REMOTE_MODPROBE_FILE}' >/dev/null",
]

for snippet in required_snippets:
    if snippet not in script:
        raise SystemExit(f"missing required deploy contract snippet: {snippet}")

for retired_snippet in [
    "require_cmd nc",
    "ticket_android_" + "sim",
    "android-" + "sim",
    "Android " + "sim" + "ulator",
    "android_" + "sim",
    "/srv/arbuzas/android-" + "sim",
    "ticket-android-" + "sim",
    "emu" + "lator",
    "sim" + "ulator",
    "av" + "d",
    "qe" + "mu",
    # DNS controlplane retired 2026-06-21
    "dns_controlplane",
    "arbuzas-dns",
    "compact-dns-db",
    "repair-dns-admin",
    "DNS_ADMIN_NGINX",
    "ARBUZAS_DNS_HTTPS_PORT",
    "ARBUZAS_DNS_DOT_PORT",
    "ARBUZAS_DNS_CONTROLPLANE_PORT",
    "ARBUZAS_DNS_ADMIN_LAN_IP",
    "ARBUZAS_DNS_HOSTNAME",
    "publish_remote_dns_admin_tailscale",
    "render_dns_admin_nginx_config",
    "validate_public_dns_access",
    "validate_private_dns_admin_access",
    "validate_remote_dns_querylog_flow",
    "validate_remote_dns_native_api_probe",
    "validate_remote_dns_workload_health",
    "compact_remote_dns_db",
    "requires_dns_release_prepare",
    "dns_validation_requested",
    "require_dns_private_admin_env",
    "resolve_remote_tailscale_dns_name",
    "ensure_remote_dns_host_preflight",
    "repair_remote_dns_admin",
    "collect_remote_dns_host_diagnostics",
    "probe_doh_endpoint",
    "probe_dot_endpoint",
    "probe_public_https_status",
    "ddns-last-ipv4",
    # Ticket phone broker retired 2026-07-10; see archive/phone-broker/.
    "phone_" + "broker",
    "phone-" + "broker",
    "PHONE_" + "BROKER",
    "ARBUZAS_PHONE_" + "BROKER_PORT",
    "TICKET_REMOTE_PHONE_" + "BROKER_URL",
]:
    if retired_snippet in script:
        raise SystemExit(f"retired deploy contract snippet still present: {retired_snippet}")

def block_between(start: str, end: str) -> str:
    start_index = script.index(start)
    end_index = script.index(end, start_index)
    return script[start_index:end_index]


deploy_block = block_between('  deploy)\n', '  validate)\n')
release_source_policy_block = block_between('enforce_release_source_policy() {\n', 'compute_release_source_sha256() {\n')
validate_block = block_between('  validate)\n', '  rollback)\n')
rollback_block = block_between('  rollback)\n', '  cleanup-docker)\n')
cleanup_block = block_between('  cleanup-docker)\n', '  memory-report)\n')
memory_report_block = block_between('  memory-report)\n    if [[ -n "${requested_release_id}" ]]; then\n', '  install-memory-report)\n')
install_memory_report_block = block_between('  install-memory-report)\n    if [[ -n "${requested_release_id}" ]]; then\n', '  validate-memory-report)\n')
validate_memory_report_block = block_between('  validate-memory-report)\n    if [[ -n "${requested_release_id}" ]]; then\n', '  install-netdata)\n')
install_block = block_between('  install-netdata)\n', '  validate-netdata)\n')
validate_netdata_block = block_between('  validate-netdata)\n', '  install-thinkpad-fan)\n')
install_thinkpad_fan_block = block_between('  install-thinkpad-fan)\n', '  validate-thinkpad-fan)\n')
validate_thinkpad_fan_block = block_between('  validate-thinkpad-fan)\n', '  repair-portainer)\n')
repair_block = block_between('  repair-portainer)\n', 'esac\n')
render_cloudflared_block = block_between('render_remote_cloudflared_configs() {\n', 'resolve_remote_current_release_id() {\n')
target_non_dns_block = block_between('compose_target_service_args_without_tunnels() {\n', 'compose_target_tunnel_service_args() {\n')
all_non_dns_block = block_between('compose_all_service_args() {\n', 'compose_all_tunnel_service_args() {\n')
rollback_function_block = block_between('rollback_remote_release() {\n', 'while (( $# > 0 )); do\n')
deploy_config_function_block = block_between('deploy_config_from_mirror() {\n', 'while (( $# > 0 )); do\n')
validate_probe_block = block_between('validate_remote_probe() {\n', 'validate_remote_host_probe() {\n')
validate_host_probe_block = block_between('validate_remote_host_probe() {\n', 'wait_until_local_ok() {\n')
validate_release_block = block_between('validate_remote_release() {\n', 'repair_remote_portainer() {\n')
fast_smoke_block = block_between('validate_remote_selected_smoke_health() {\n', 'validate_remote_workload_health() {\n')
deploy_validation_block = block_between('validate_deployed_release() {\n', 'repair_remote_portainer() {\n')
post_deploy_maintenance_block = block_between('run_post_deploy_maintenance() {\n', 'repair_remote_portainer() {\n')
service_resolution_block = block_between('resolve_requested_services() {\n', 'populate_current_diagnostic_services() {\n')
prepare_ticket_permissions_block = block_between('prepare_remote_ticket_runtime_permissions() {\n', 'prepare_remote_host_layout() {\n')
prepare_host_block = block_between('prepare_remote_host_layout() {\n', 'copy_release_to_remote() {\n')

for timed_phase in (
    "mirror_push",
    "prepare_ticket_permissions",
    "package_release",
    "upload_release",
    "render_tunnels",
    "restart_services",
    "validate_release",
    "post_deploy_maintenance",
):
    if f"run_timed_phase {timed_phase}" not in deploy_block:
        raise SystemExit(f"deploy block does not time the {timed_phase} phase")
if 'enforce_release_source_policy' not in deploy_block:
    raise SystemExit("deploy does not enforce clean source for production profiles")
for policy_snippet in (
    'if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then',
    'ARBUZAS_ALLOW_DIRTY_FAST_RELEASE',
    'replace it with a clean standard or full release before close-out',
    'Refusing ${VALIDATION_PROFILE} deployment from ${source_dirty} source',
):
    if policy_snippet not in release_source_policy_block:
        raise SystemExit(f"release source policy is missing: {policy_snippet}")
if 'run_timed_phase validate_release validate_deployed_release' not in deploy_block:
    raise SystemExit("deploy does not validate the deployed release through the profile-aware path")
if 'log "Deploy: targeted services ${COMPOSE_TARGET_SERVICES[*]} profile=${VALIDATION_PROFILE}"' not in deploy_block:
    raise SystemExit("deploy block does not announce targeted service deployments")

if 'log "Validate: targeted services ${COMPOSE_TARGET_SERVICES[*]} profile=${VALIDATION_PROFILE}"' not in validate_block:
    raise SystemExit("validate block does not announce targeted service validation")
if 'run_timed_phase validate_release validate_remote_release' not in validate_block:
    raise SystemExit("validate action does not emit phase timing")

if "if [[ \"${VALIDATION_PROFILE}\" == \"fast\" ]]; then" not in validate_release_block:
    raise SystemExit("fast profile is not routed to selected-service smoke validation")
if "validate_remote_selected_smoke_health \"${remote_release_dir}\" 0" not in validate_release_block:
    raise SystemExit("fast validation does not use selected-service smoke readiness")
if "if [[ \"${VALIDATION_PROFILE}\" == \"full\" ]]; then" not in validate_release_block:
    raise SystemExit("full profile no longer guards strict host validation")
if "validate_remote_swarm_baseline \"${remote_release_dir}\"" not in validate_release_block:
    raise SystemExit("full profile no longer validates the swarm baseline")
if "validate_remote_selected_workload_health \"${remote_release_dir}\"" not in validate_release_block:
    raise SystemExit("standard profile no longer retains targeted workload readiness")

for smoke_snippet in (
    "ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS must be a positive integer",
    'validate_remote_probe "${remote_release_dir}" "batched selected-service smoke readiness"',
    "deadline=\\$((SECONDS + ${ARBUZAS_FAST_SMOKE_TIMEOUT_SECONDS}))",
    "compose ps --services --status running",
    "selected services did not pass batched smoke readiness before timeout",
    "start_ticket_smoke_probe()",
    "collect_ticket_smoke_probe_status()",
    "setsid timeout --kill-after=1s 2s bash -c",
    "trap 'cleanup_ticket_smoke_probes; exit 130' INT",
    "trap 'cleanup_ticket_smoke_probes; exit 143' TERM",
    "trap cleanup_ticket_smoke_probes EXIT",
    "Ticket smoke probe failed: %s",
    "curl -fsS http://127.0.0.1:${ARBUZAS_TICKET_REMOTE_PORT}/api/v1/livez",
    "https://${ARBUZAS_TICKET_REMOTE_HOSTNAME}/api/v1/health",
    r'-w \"%{http_code}\"',
    r'case \"\${public_code}\" in 200|302)',
    r'[[ \"\${public_code}\" == 401 ]]',
):
    if smoke_snippet not in fast_smoke_block:
        raise SystemExit(f"fast smoke validation is missing: {smoke_snippet}")

ticket_probe_starts = fast_smoke_block.count("start_ticket_smoke_probe '")
if ticket_probe_starts != 6:
    raise SystemExit(f"Ticket Remote fast smoke must start six independent probes, found {ticket_probe_starts}")
if "-w '%{http_code}'" in fast_smoke_block:
    raise SystemExit("Ticket Remote public probes contain shell-breaking nested single quotes")
running_gate_index = fast_smoke_block.index("compose ps --services --status running")
first_ticket_probe_index = fast_smoke_block.index("start_ticket_smoke_probe 'ticket phone bridge local health'")
collect_ticket_probe_index = fast_smoke_block.index("collect_ticket_smoke_probe_status || return 1")
if first_ticket_probe_index <= running_gate_index:
    raise SystemExit("Ticket Remote probes must start after the running-service gate")
if collect_ticket_probe_index <= fast_smoke_block.rindex("start_ticket_smoke_probe '"):
    raise SystemExit("Ticket Remote fast smoke must collect every concurrent probe before returning")
if "ticket_smoke_probe_pids=()" not in fast_smoke_block or "kill -TERM -- \\\"-\\${probe_pid}\\\"" not in fast_smoke_block:
    raise SystemExit("Ticket Remote fast smoke does not clean up probe process groups")

if "if [[ \"${VALIDATION_PROFILE}\" == \"fast\" ]]; then" not in deploy_validation_block:
    raise SystemExit("fast deployment does not require the current release link during smoke validation")
if 'validate_remote_selected_smoke_health "${remote_release_dir}" 1' not in deploy_validation_block:
    raise SystemExit("fast deployment smoke validation does not require the new current release")
if 'if [[ "${VALIDATION_PROFILE}" != "full" ]]; then' not in post_deploy_maintenance_block:
    raise SystemExit("non-full profiles do not defer slow post-deploy maintenance")
if "cleanup_remote_public_bundle_versions" not in post_deploy_maintenance_block or "run_automatic_remote_docker_gc" not in post_deploy_maintenance_block:
    raise SystemExit("full profile no longer preserves post-deploy cleanup")

ticket_remote_fast_branch = block_between('      ticket_remote)\n', '      ticket_remote_spacetime_sidecar)\n')
if 'if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then' not in ticket_remote_fast_branch or 'append_unique COMPOSE_TARGET_SERVICES ticket_remote' not in ticket_remote_fast_branch:
    raise SystemExit("fast Ticket Remote iteration does not keep its restart scope to ticket_remote")

if "mark_remote_validation_failed" not in validate_probe_block:
    raise SystemExit("remote compose validation failures must be recorded explicitly")
if "mark_remote_validation_failed" not in validate_host_probe_block:
    raise SystemExit("remote host validation failures must be recorded explicitly")
if "local REMOTE_VALIDATION_FAILED=0" not in validate_release_block:
    raise SystemExit("validate_remote_release must reset the explicit failure flag")
if validate_release_block.count("return_remote_validation_status") < 2:
    raise SystemExit("validate_remote_release must return the explicit failure flag in both full and targeted modes")

for tunnel_service in ("train_tunnel", "satiksme_tunnel", "subscription_tunnel", "ticket_remote_tunnel"):
    if tunnel_service not in target_non_dns_block:
        raise SystemExit(f"targeted non-DNS service args must explicitly skip tunnel service: {tunnel_service}")
    if tunnel_service in all_non_dns_block:
        raise SystemExit(f"all non-DNS service args should leave tunnel recreation to compose_all_tunnel_service_args: {tunnel_service}")

if "targeted_service_selected" in render_cloudflared_block:
    raise SystemExit("cloudflared configs must be rendered for every release, not only selected tunnels")
for cloudflared_config in (
    "train-bot.yml",
    "satiksme-bot.yml",
    "subscription-bot.yml",
    "ticket-remote.yml",
):
    if render_cloudflared_block.count(cloudflared_config) != 1:
        raise SystemExit(f"cloudflared config should be rendered exactly once: {cloudflared_config}")

if 'run_timed_phase rollback_release rollback_remote_release' not in rollback_block:
    raise SystemExit("rollback action does not emit phase timing")
if 'if [[ "${VALIDATION_PROFILE}" == "fast" ]]; then' not in rollback_block:
    raise SystemExit("rollback does not preserve the fast selected-service smoke path")
if 'run_timed_phase validate_rollback validate_remote_selected_smoke_health' not in rollback_block:
    raise SystemExit("fast rollback does not use selected-service smoke readiness")
if 'run_timed_phase validate_rollback_link validate_remote_current_release_link' not in rollback_block:
    raise SystemExit("standard and full rollback do not verify the current release link")
if 'run_timed_phase validate_rollback_services validate_remote_release' not in rollback_block:
    raise SystemExit("standard and full rollback do not retain workload validation")
if 'run_timed_phase post_rollback_maintenance run_post_deploy_maintenance' not in rollback_block:
    raise SystemExit("rollback does not route maintenance through the validation profile")
for rollback_snippet in (
    'rollback_service_args="$(compose_target_service_args_without_tunnels)"',
    'rollback_tunnel_service_args="$(compose_target_tunnel_service_args)"',
    'up -d --build --force-recreate --no-deps${rollback_service_args}',
    'up -d --force-recreate --no-deps${rollback_tunnel_service_args}',
):
    if rollback_snippet not in rollback_function_block:
        raise SystemExit(f"rollback function does not preserve targeted deploy scope: {rollback_snippet}")

if "remote_run_docker_gc" not in cleanup_block:
    raise SystemExit("cleanup-docker action does not invoke remote_run_docker_gc")
if "remote_run_host_cache_cleanup" not in cleanup_block:
    raise SystemExit("cleanup-docker action does not invoke host cache cleanup")
if cleanup_block.index("remote_run_docker_gc") > cleanup_block.index("remote_run_host_cache_cleanup"):
    raise SystemExit("cleanup-docker runs host cache cleanup before Docker/release cleanup")

automatic_cleanup_block = block_between('run_automatic_remote_docker_gc() {\n', 'run_portainer_db_tool() {\n')
if automatic_cleanup_block.index("remote_run_docker_gc") > automatic_cleanup_block.index("remote_run_host_cache_cleanup"):
    raise SystemExit("automatic cleanup runs host cache cleanup before Docker/release cleanup")

memory_report_function_block = block_between('remote_run_memory_report() {\n', 'remote_run_host_cache_cleanup() {\n')
if "MEMORY_REPORT_SCRIPT" not in memory_report_function_block:
    raise SystemExit("memory-report function does not use the repo memory reporter")
if "python3 - --source-label" not in memory_report_function_block:
    raise SystemExit("memory-report function does not stream the reporter to the host")

host_cache_cleanup_block = block_between('remote_run_host_cache_cleanup() {\n', 'compose_all_service_args() {\n')
if host_cache_cleanup_block.index("journalctl --vacuum-size=\\\"\\${journal_max_size}\\\"") > host_cache_cleanup_block.index("report_memory 'before reclaimable cache flush'"):
    raise SystemExit("host cache cleanup flushes reclaimable memory before journal cleanup")
if host_cache_cleanup_block.index("report_memory 'before reclaimable cache flush'") > host_cache_cleanup_block.index("printf '3\\n' > /proc/sys/vm/drop_caches"):
    raise SystemExit("host cache cleanup drops reclaimable memory before logging the pre-flush state")
if host_cache_cleanup_block.index("printf '3\\n' > /proc/sys/vm/drop_caches") > host_cache_cleanup_block.index("report_memory 'after reclaimable cache flush'"):
    raise SystemExit("host cache cleanup logs the post-flush state before dropping reclaimable memory")

for ownership_snippet in (
    "install -d -o 1001 -g 1001 -m 0750 '/srv/arbuzas/ticket-remote/state'",
    "'/etc/arbuzas/env/ticket-remote.env'",
    "'/etc/arbuzas/secrets/ticket-remote/spacetime-jwt-private-key.pem'",
    "'/etc/arbuzas/secrets/ticket-remote/sidecar-write-token.secret'",
    "chown 1001:1001",
    "chmod 0600",
):
    if ownership_snippet not in prepare_ticket_permissions_block:
        raise SystemExit(f"Ticket Remote non-root host preparation is missing: {ownership_snippet}")
for bridge_ownership_snippet in (
    "'/etc/arbuzas/secrets/android-adb/adbkey'",
    "'/etc/arbuzas/secrets/android-adb/adbkey.pub'",
    "'/etc/arbuzas/secrets/android-adb/adb_known_hosts.pb'",
    "chown 1002:1002",
):
    if bridge_ownership_snippet not in prepare_ticket_permissions_block:
        raise SystemExit(f"Ticket phone bridge non-root host preparation is missing: {bridge_ownership_snippet}")
if "prepare_remote_ticket_runtime_permissions" not in prepare_host_block:
    raise SystemExit("full host preparation does not retain Ticket Remote runtime permissions")
if "run_timed_phase prepare_ticket_permissions prepare_remote_ticket_runtime_permissions" not in deploy_block:
    raise SystemExit("deploy does not prepare Ticket Remote permissions after mirror sync")
if "prepare_remote_ticket_runtime_permissions" not in deploy_config_function_block:
    raise SystemExit("deploy-config does not restore non-root Ticket and ADB secret ownership after mirror sync")
if not (
    deploy_config_function_block.index('run_host_mirror_push "${changed_paths_file}"')
    < deploy_config_function_block.index("prepare_remote_ticket_runtime_permissions")
    < deploy_config_function_block.index('host_mirror_affected_services "${changed_paths_file}"')
):
    raise SystemExit("deploy-config must restore secret ownership after mirror push and before service restart selection")

if "remote_run_memory_report" not in memory_report_block:
    raise SystemExit("memory-report action does not invoke remote_run_memory_report")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc", "remote_run_docker_gc", "remote_run_host_cache_cleanup", "validate_remote_release"):
    if forbidden in memory_report_block:
        raise SystemExit(f"memory-report block should stay read-only and isolated from deploy/cleanup flow: {forbidden}")

if "stage_memory_report_config_to_remote" not in install_memory_report_block or "install_remote_memory_report" not in install_memory_report_block or "validate_remote_memory_report" not in install_memory_report_block:
    raise SystemExit("install-memory-report block does not stage config, install the service, and validate it")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc", "remote_run_docker_gc", "remote_run_host_cache_cleanup", "validate_remote_release"):
    if forbidden in install_memory_report_block:
        raise SystemExit(f"install-memory-report block should stay isolated from app deploy/cleanup flow: {forbidden}")

if "validate_remote_memory_report" not in validate_memory_report_block:
    raise SystemExit("validate-memory-report block does not invoke validate_remote_memory_report")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc", "remote_run_docker_gc", "remote_run_host_cache_cleanup", "validate_remote_release"):
    if forbidden in validate_memory_report_block:
        raise SystemExit(f"validate-memory-report block should stay isolated from app deploy/cleanup flow: {forbidden}")

if "stage_netdata_config_to_remote" not in install_block or "install_remote_netdata" not in install_block or "validate_remote_netdata" not in install_block:
    raise SystemExit("install-netdata block does not stage config, install Netdata, and validate it")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc"):
    if forbidden in install_block:
        raise SystemExit(f"install-netdata block should stay isolated from the app deploy flow: {forbidden}")

if "validate_remote_netdata" not in validate_netdata_block:
    raise SystemExit("validate-netdata block does not invoke validate_remote_netdata")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc"):
    if forbidden in validate_netdata_block:
        raise SystemExit(f"validate-netdata block should stay isolated from the app deploy flow: {forbidden}")

if "stage_thinkpad_fan_config_to_remote" not in install_thinkpad_fan_block or "install_remote_thinkpad_fan" not in install_thinkpad_fan_block or "validate_remote_thinkpad_fan" not in install_thinkpad_fan_block:
    raise SystemExit("install-thinkpad-fan block does not stage config, install the controller, and validate it")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc"):
    if forbidden in install_thinkpad_fan_block:
        raise SystemExit(f"install-thinkpad-fan block should stay isolated from the app deploy flow: {forbidden}")

if "validate_remote_thinkpad_fan" not in validate_thinkpad_fan_block:
    raise SystemExit("validate-thinkpad-fan block does not invoke validate_remote_thinkpad_fan")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc"):
    if forbidden in validate_thinkpad_fan_block:
        raise SystemExit(f"validate-thinkpad-fan block should stay isolated from the app deploy flow: {forbidden}")

if "run_automatic_remote_docker_gc" in repair_block or "remote_run_docker_gc" in repair_block:
    raise SystemExit("repair-portainer block should not trigger Docker GC")
PY
then
  echo "FAIL: Arbuzas deploy script no longer matches the Arbuzas maintenance contract" >&2
  exit 1
fi

echo "PASS: Arbuzas deploy script exposes and wires the Arbuzas maintenance contract"
