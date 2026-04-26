#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_PATH="${REPO_ROOT}/tools/arbuzas/deploy.sh"
NETDATA_CONFIG_PATH="${REPO_ROOT}/infra/arbuzas/netdata/netdata.conf"
NETDATA_DOCKER_CONFIG_PATH="${REPO_ROOT}/infra/arbuzas/netdata/go.d/docker.conf"
NETDATA_DOCKER_SD_CONFIG_PATH="${REPO_ROOT}/infra/arbuzas/netdata/go.d/sd/docker.conf"
THINKPAD_FAN_DEFAULT_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/etc/default/arbuzas-thinkpad-fan"
THINKPAD_FAN_MODPROBE_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/etc/modprobe.d/arbuzas-thinkpad-fan.conf"
THINKPAD_FAN_SERVICE_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/etc/systemd/system/arbuzas-thinkpad-fan.service"
THINKPAD_FAN_SCRIPT_PATH="${REPO_ROOT}/infra/arbuzas/thinkpad-fan/usr/local/libexec/arbuzas-thinkpad-fan.py"
DNS_ADMIN_NGINX_TEMPLATE_PATH="${REPO_ROOT}/infra/arbuzas/nginx/arbuzas-dns-admin.conf.template"

if [[ ! -f "${SCRIPT_PATH}" ]]; then
  echo "FAIL: missing Arbuzas deploy script at ${SCRIPT_PATH}" >&2
  exit 1
fi

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

if [[ ! -f "${DNS_ADMIN_NGINX_TEMPLATE_PATH}" ]]; then
  echo "FAIL: missing Arbuzas DNS admin nginx template at ${DNS_ADMIN_NGINX_TEMPLATE_PATH}" >&2
  exit 1
fi

for dns_admin_snippet in \
  "listen 127.0.0.1:80;" \
  "listen [::1]:80;" \
  "listen __DNS_ADMIN_LISTEN_IPV4__:80;" \
  "listen [__DNS_ADMIN_LISTEN_IPV6__]:80;" \
  "server_name __DNS_ADMIN_SERVER_NAMES__;" \
  "allow 100.64.0.0/10;" \
  "allow fd7a:115c:a1e0::/48;" \
  "proxy_set_header X-Forwarded-Proto http;" \
  "proxy_pass http://127.0.0.1:__DNS_ADMIN_CONTROLPLANE_PORT__;" \
  "deny all;"; do
  if ! grep -F "${dns_admin_snippet}" "${DNS_ADMIN_NGINX_TEMPLATE_PATH}" >/dev/null; then
    echo "FAIL: Arbuzas DNS admin nginx template must contain: ${dns_admin_snippet}" >&2
    exit 1
  fi
done

if ! python3 - "${SCRIPT_PATH}" <<'PY'
import sys
from pathlib import Path

script = Path(sys.argv[1]).read_text(encoding="utf-8")

required_snippets = [
    "cleanup-docker    Run the Arbuzas Docker image and build-cache cleanup policy on the live host",
    "compact-dns-db    Run the Arbuzas DNS cleanup activation and compact maintenance flow on the live host",
    "repair-dns-admin  Clear stale private DNS admin forwards, re-assert the Tailscale TCP forward, refresh the bare private web URL, and print host listener diagnostics",
    "install-netdata   Install Netdata plus hardware monitoring packages on the live host and publish it privately over Tailscale",
    "validate-netdata  Validate the live Netdata host install, private Tailscale access, and expected Arbuzas hardware charts",
    "install-thinkpad-fan   Install the Arbuzas ThinkPad fan controller on the live host",
    "validate-thinkpad-fan  Validate the live Arbuzas ThinkPad fan controller and current control mode",
    "deploy|validate|rollback|cleanup-docker|compact-dns-db|repair-dns-admin|install-netdata|validate-netdata|install-thinkpad-fan|validate-thinkpad-fan|repair-portainer)",
    "--release-id is not supported for cleanup-docker",
    "--release-id is not supported for compact-dns-db",
    "--release-id is not supported for repair-dns-admin",
    "--release-id is not supported for install-netdata",
    "--release-id is not supported for validate-netdata",
    "--release-id is not supported for install-thinkpad-fan",
    "--release-id is not supported for validate-thinkpad-fan",
    "--services NAME[,NAME...]",
    "--services is only supported for deploy and validate",
    "--services requires at least one service name",
    "remote_run_docker_gc()",
    "resolve_local_docker_gc_script()",
    "compact_remote_dns_db()",
    "run_automatic_remote_docker_gc()",
    "compose_target_service_args_without_dns()",
    "compose_all_non_dns_service_args()",
    "collect_remote_dns_host_diagnostics()",
    "ensure_remote_dns_host_preflight()",
    "repair_remote_dns_admin()",
    "stage_netdata_config_to_remote()",
    "install_remote_netdata()",
    "validate_remote_netdata()",
    "stage_thinkpad_fan_config_to_remote()",
    "install_remote_thinkpad_fan()",
    "validate_remote_thinkpad_fan()",
    'copy_tree_into_release "tools/arbuzas-rs"',
    'cp "${REPO_ROOT}/tools/arbuzas/render_cloudflared_config.py" "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/render_cloudflared_config.py"',
    'cp "${REPO_ROOT}/tools/arbuzas/docker_gc.py" "${ARBUZAS_RELEASE_DIR}/tools/arbuzas/docker_gc.py"',
    'gc_script="$(resolve_local_docker_gc_script)"',
    "missing Docker GC helper locally and on the current Arbuzas release bundle",
    "gc_script='${REMOTE_CURRENT_LINK}/tools/arbuzas/docker_gc.py'",
    "run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns migrate --json </dev/null",
    "run -T --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json </dev/null",
    "up -d --build --force-recreate --no-deps dns_controlplane >/dev/null",
    "up -d --force-recreate --no-deps dns_controlplane",
    "up -d --remove-orphans${all_non_dns_service_args}",
    "up -d --no-deps${non_dns_service_args}",
    "append_unique COMPOSE_TARGET_SERVICES train_tunnel",
    "validate_remote_selected_workload_health",
    "validate_remote_current_release_link",
    "validate_remote_satiksme_dependency_dns",
    "getent hosts saraksti.rigassatiksme.lv",
    "Validation failed: satiksme dependency DNS",
    'DNS_ADMIN_NGINX_CONFIG_ROOT="${REPO_ROOT}/infra/arbuzas/nginx"',
    'DNS_ADMIN_NGINX_TEMPLATE_FILE="${DNS_ADMIN_NGINX_TEMPLATE_FILE:-${DNS_ADMIN_NGINX_CONFIG_ROOT}/arbuzas-dns-admin.conf.template}"',
    'DNS_ADMIN_NGINX_REMOTE_SITE_FILE="/etc/nginx/sites-available/arbuzas-dns-admin"',
    'DNS_ADMIN_NGINX_REMOTE_SITE_LINK="/etc/nginx/sites-enabled/arbuzas-dns-admin"',
    'ARBUZAS_DNS_CONTROLPLANE_PORT="${ARBUZAS_DNS_CONTROLPLANE_PORT:-8097}"',
    'ARBUZAS_SSH_PORT="${ARBUZAS_SSH_PORT:-}"',
    'ARBUZAS_DNS_ADMIN_LAN_IP="${ARBUZAS_DNS_ADMIN_LAN_IP:-}"',
    "run_ssh() {",
    "run_scp() {",
    "dns_validation_requested()",
    "require_dns_private_admin_env()",
    "resolve_remote_tailscale_dns_name()",
    "resolve_remote_tailscale_hostname()",
    "resolve_remote_tailscale_ipv6()",
    "render_dns_admin_nginx_config()",
    "publish_remote_dns_admin_tailscale()",
    "validate_private_dns_admin_access()",
    "is_valid_ipv6()",
    "tailscale serve --bg --https=443 off >/dev/null 2>&1 || true",
    "tailscale serve --bg --yes --tcp ${ARBUZAS_DNS_CONTROLPLANE_PORT} 127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}",
    "command -v nginx >/dev/null 2>&1 || {",
    "printf '%s' '${nginx_config_base64}' | base64 -d > '${DNS_ADMIN_NGINX_REMOTE_SITE_FILE}'",
    "ln -sfn '${DNS_ADMIN_NGINX_REMOTE_SITE_FILE}' '${DNS_ADMIN_NGINX_REMOTE_SITE_LINK}'",
    'rendered = rendered.replace("__DNS_ADMIN_LISTEN_IPV4__", tailnet_ipv4)',
    'rendered = rendered.replace("__DNS_ADMIN_LISTEN_IPV6__", tailnet_ipv6)',
    "curl -fsS -H 'Host: ${tailnet_dns_name}' 'http://127.0.0.1/' >/dev/null 2>/dev/null",
    "private DNS admin root is available at http://${tailnet_dns_name}/",
    "ARBUZAS_DNS_CONTROLPLANE_PORT=${ARBUZAS_DNS_CONTROLPLANE_PORT}",
    "ARBUZAS_DNS_ADMIN_LAN_IP=${ARBUZAS_DNS_ADMIN_LAN_IP}",
    "DNS host preflight failed on Arbuzas; fix the listener conflict before retrying.",
    "Safe repair: {repair_cmd}",
    "for path in / /login /dns/login /v1/health /livez /healthz; do",
    "dns private admin login on Arbuzas loopback",
    "dns private admin login on Arbuzas LAN address",
    "dns private admin root on Arbuzas nginx",
    "dns private admin bare URL over Tailscale",
    "dns private admin login over Tailscale",
    "curl -fsS 'http://${ARBUZAS_DNS_ADMIN_LAN_IP}:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login' >/dev/null 2>/dev/null",
    "curl -fsS -H 'Host: ${tailnet_dns_name}' 'http://127.0.0.1/' >/dev/null 2>/dev/null",
    "curl -fsS \"http://${tailnet_dns_name}/\" >/dev/null 2>&1",
    "curl -fsS \"http://${tailnet_ipv4}:${ARBUZAS_DNS_CONTROLPLANE_PORT}/login\"",
    "ss -H -ltnp | awk '\\$4 ~ /:80$|:443$|:853$|:8097$/ { print }' >&2 || true",
    'validate_private_dns_admin_access "${REMOTE_CURRENT_LINK}"',
    'NETDATA_REMOTE_CONFIG_DIR="/etc/netdata"',
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
    "compose stop dns_controlplane",
    "compose run -T --rm --no-deps --build dns_controlplane /usr/local/bin/arbuzas-dns compact --json --include-legacy-observability </dev/null",
    "validate_remote_dns_workload_health \"${REMOTE_CURRENT_LINK}\"",
    "validate_remote_dns_querylog_flow()",
    "validate_remote_dns_native_api_probe()",
    "sqlite3.connect(f'file:{db_path}?mode=ro', uri=True)",
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
]:
    if retired_snippet in script:
        raise SystemExit(f"retired deploy contract snippet still present: {retired_snippet}")


def block_between(start: str, end: str) -> str:
    start_index = script.index(start)
    end_index = script.index(end, start_index)
    return script[start_index:end_index]


deploy_block = block_between('  deploy)\n', '  validate)\n')
validate_block = block_between('  validate)\n', '  rollback)\n')
rollback_block = block_between('  rollback)\n', '  cleanup-docker)\n')
cleanup_block = block_between('  cleanup-docker)\n', '  compact-dns-db)\n')
compact_block = block_between('  compact-dns-db)\n', '  repair-dns-admin)\n')
repair_dns_admin_block = block_between('  repair-dns-admin)\n', '  install-netdata)\n')
install_block = block_between('  install-netdata)\n', '  validate-netdata)\n')
validate_netdata_block = block_between('  validate-netdata)\n', '  install-thinkpad-fan)\n')
install_thinkpad_fan_block = block_between('  install-thinkpad-fan)\n', '  validate-thinkpad-fan)\n')
validate_thinkpad_fan_block = block_between('  validate-thinkpad-fan)\n', '  repair-portainer)\n')
repair_block = block_between('  repair-portainer)\n', 'esac\n')
remote_compose_up_block = block_between('remote_compose_up() {\n', 'validate_remote_dns_querylog_flow() {\n')
compact_function_block = block_between('compact_remote_dns_db() {\n', 'stage_netdata_config_to_remote() {\n')
rollback_function_block = block_between('rollback_remote_release() {\n', 'while (( $# > 0 )); do\n')

if deploy_block.index('validate_remote_release "${ARBUZAS_RELEASE_ID}"') > deploy_block.index("run_automatic_remote_docker_gc"):
    raise SystemExit("deploy cleanup runs before validation")
if deploy_block.index('validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"') > deploy_block.index('validate_remote_release "${ARBUZAS_RELEASE_ID}"'):
    raise SystemExit("deploy validates the release before confirming the current symlink")
if 'log "Deploy: targeted services ${COMPOSE_TARGET_SERVICES[*]}"' not in deploy_block:
    raise SystemExit("deploy block does not announce targeted service deployments")
if 'if dns_validation_requested || requires_dns_release_prepare; then' not in deploy_block:
    raise SystemExit("deploy block does not gate DNS-only private admin requirements")
if 'require_dns_private_admin_env' not in deploy_block:
    raise SystemExit("deploy block does not require the DNS private admin environment when needed")
if deploy_block.index("publish_remote_dns_admin_tailscale") > deploy_block.index('validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${ARBUZAS_RELEASE_ID}"'):
    raise SystemExit("deploy block publishes the private DNS admin path after validation starts")

if 'log "Validate: targeted services ${COMPOSE_TARGET_SERVICES[*]}"' not in validate_block:
    raise SystemExit("validate block does not announce targeted service validation")

if rollback_block.index('validate_remote_release "${requested_release_id}"') > rollback_block.index("run_automatic_remote_docker_gc"):
    raise SystemExit("rollback cleanup runs before validation")
if rollback_block.index('validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${requested_release_id}"') > rollback_block.index('validate_remote_release "${requested_release_id}"'):
    raise SystemExit("rollback validates the release before confirming the current symlink")
if rollback_block.index("publish_remote_dns_admin_tailscale") > rollback_block.index('validate_remote_current_release_link "${REMOTE_RELEASES_ROOT}/${requested_release_id}"'):
    raise SystemExit("rollback block publishes the private DNS admin path after validation starts")

if "remote_run_docker_gc" not in cleanup_block:
    raise SystemExit("cleanup-docker action does not invoke remote_run_docker_gc")

if "compact_remote_dns_db" not in compact_block:
    raise SystemExit("compact-dns-db action does not invoke compact_remote_dns_db")
if 'log "Maintenance: activating cleanup and compacting the live Arbuzas DNS control-plane database"' not in compact_block:
    raise SystemExit("compact-dns-db block does not announce the activation-and-compact action")
if 'require_dns_private_admin_env' not in compact_block:
    raise SystemExit("compact-dns-db block does not require the DNS private admin environment")
if 'validate_remote_dns_workload_health "${REMOTE_CURRENT_LINK}"' not in compact_block:
    raise SystemExit("compact-dns-db block does not validate the DNS workload after restart")

if "repair_remote_dns_admin" not in repair_dns_admin_block:
    raise SystemExit("repair-dns-admin block does not invoke repair_remote_dns_admin")
if 'require_dns_private_admin_env' not in repair_dns_admin_block:
    raise SystemExit("repair-dns-admin block does not require the DNS private admin environment")
for forbidden in ("prepare_local_release_bundle", "copy_release_to_remote", "remote_compose_up", "run_automatic_remote_docker_gc", "validate_remote_release"):
    if forbidden in repair_dns_admin_block:
        raise SystemExit(f"repair-dns-admin block should stay isolated from the app deploy flow: {forbidden}")

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

if remote_compose_up_block.index("ensure_remote_dns_host_preflight") > remote_compose_up_block.index('remote_shell "'):
    raise SystemExit("remote_compose_up runs the DNS preflight after beginning the remote compose flow")
if compact_function_block.index("ensure_remote_dns_host_preflight") > compact_function_block.index('remote_compose_shell "${remote_release_dir}"'):
    raise SystemExit("compact_remote_dns_db runs the DNS preflight after beginning the compact flow")
if rollback_function_block.index("ensure_remote_dns_host_preflight") > rollback_function_block.index('remote_shell "'):
    raise SystemExit("rollback_remote_release runs the DNS preflight after beginning the rollback flow")
PY
then
  echo "FAIL: Arbuzas deploy script no longer matches the Arbuzas maintenance contract" >&2
  exit 1
fi

echo "PASS: Arbuzas deploy script exposes and wires the Arbuzas maintenance contract"
