#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="${REPO_ROOT}/infra/arbuzas/docker/compose.yml"
MODULE_FILE="${REPO_ROOT}/infra/adguardhome/module.yaml"
DEPLOY_FILE="${REPO_ROOT}/tools/arbuzas/deploy.sh"
DOCKERFILE="${REPO_ROOT}/infra/arbuzas/docker/images/dns-controlplane.Dockerfile"
CONFIG_FILE="${REPO_ROOT}/tools/arbuzas-rs/crates/arbuzas-dns-lib/src/config.rs"
ARCHIVE_DIR="${REPO_ROOT}/ops/archive/arbuzas-dns-legacy-helper"

python3 - "${COMPOSE_FILE}" "${MODULE_FILE}" "${DEPLOY_FILE}" "${DOCKERFILE}" "${CONFIG_FILE}" "${ARCHIVE_DIR}" <<'PY'
import sys
from pathlib import Path

compose = Path(sys.argv[1]).read_text()
module = Path(sys.argv[2]).read_text()
deploy = Path(sys.argv[3]).read_text()
dockerfile = Path(sys.argv[4]).read_text()
config = Path(sys.argv[5]).read_text()
archive_dir = Path(sys.argv[6])

required_compose_snippets = [
    "dns_controlplane:",
    "dockerfile: infra/arbuzas/docker/images/dns-controlplane.Dockerfile",
    "ARBUZAS_DNS_SOURCE_CONFIG_FILE: /etc/arbuzas/dns/arbuzas-dns.yaml",
    "ARBUZAS_DNS_IDENTITIES_FILE: /etc/arbuzas/dns/doh-identities.json",
    "ARBUZAS_DNS_QUERYLOG_VIEW_PREFERENCE_FILE: /srv/arbuzas/dns/state/querylog-view-preference.json",
    'ARBUZAS_DNS_HOSTNAME: "${ARBUZAS_DNS_HOSTNAME}"',
    'ARBUZAS_DNS_HTTPS_PORT: "${ARBUZAS_DNS_HTTPS_PORT}"',
    'ARBUZAS_DNS_DOT_PORT: "${ARBUZAS_DNS_DOT_PORT}"',
    'ARBUZAS_DNS_CONTROLPLANE_PORT: "${ARBUZAS_DNS_CONTROLPLANE_PORT}"',
    'ARBUZAS_DNS_PORT: "53"',
    "ARBUZAS_DNS_CONTROLPLANE_DB_FILE: /srv/arbuzas/dns/state/controlplane.sqlite",
    "/srv/arbuzas/dns/state:/srv/arbuzas/dns/state",
    '127.0.0.1:${ARBUZAS_DNS_CONTROLPLANE_PORT}:${ARBUZAS_DNS_CONTROLPLANE_PORT}',
    '${ARBUZAS_DNS_ADMIN_LAN_IP}:${ARBUZAS_DNS_CONTROLPLANE_PORT}:${ARBUZAS_DNS_CONTROLPLANE_PORT}',
    'http://127.0.0.1:$${ARBUZAS_DNS_CONTROLPLANE_PORT:-8097}/dns/login',
    'openssl s_client -brief -connect 127.0.0.1:$${ARBUZAS_DNS_DOT_PORT:-853} -servername $${ARBUZAS_DNS_HOSTNAME:-dns.jolkins.id.lv}',
]
for snippet in required_compose_snippets:
    if snippet not in compose:
        raise SystemExit(f"missing compose contract snippet: {snippet}")

for retired_snippet in [
    "ARBUZAS_DNS_LEGACY_BRIDGE_ENABLED",
    "ARBUZAS_DNS_LEGACY_POLICY_WATCH_ENABLED",
    "published: 53",
    "PIHOLE_REMOTE_HTTPS_PORT",
    "PIHOLE_REMOTE_DOT_PORT",
    "PIHOLE_REMOTE_TLS_CERT_FILE",
    "PIHOLE_REMOTE_TLS_KEY_FILE",
    "/pixel-stack/identity/inject.js",
]:
    if retired_snippet in compose:
        raise SystemExit(f"retired runtime dependency still present: {retired_snippet}")

for retired_snippet in ["policy_publisher:", "identity_web:", "adguardhome:", "frontend:"]:
    if retired_snippet in compose:
        raise SystemExit(f"retired compose service still present: {retired_snippet}")

required_module_snippets = [
    "build dns_controlplane",
    "run --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns migrate --json",
    "run --rm --no-deps dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json",
    "up -d dns_controlplane",
    "stop dns_controlplane",
    "event_source: arbuzas-dns",
    "/dns/login",
]
for snippet in required_module_snippets:
    if snippet not in module:
        raise SystemExit(f"missing module contract snippet: {snippet}")

required_deploy_snippets = [
    "dns_controlplane /usr/local/bin/arbuzas-dns migrate --json",
    "dns_controlplane /usr/local/bin/arbuzas-dns release sync-policy --json",
    "validate_remote_current_release_link",
    "ensure_remote_dns_host_preflight",
    "repair_remote_dns_admin",
    "portainer train_bot satiksme_bot subscription_bot train_tunnel satiksme_tunnel subscription_tunnel dns_controlplane",
    "dns controlplane healthcheck",
    "dns controlplane release validation",
    "collect_remote_validation_diagnostics \"${diagnostics_release_dir}\" dns_controlplane",
]
for snippet in required_deploy_snippets:
    if snippet not in deploy:
        raise SystemExit(f"missing deploy contract snippet: {snippet}")

for retired_snippet in [
    "adguardhome-doh-identity-web.py",
    "adguardhome-policy-publisher.py",
    "adguardhome-doh-identityctl",
    "python:3.12-slim",
    "netcat-openbsd",
]:
    if retired_snippet in dockerfile:
        raise SystemExit(f"retired controlplane image dependency still present: {retired_snippet}")

if "ca-certificates curl openssl tzdata" not in dockerfile:
    raise SystemExit("missing TLS-aware DNS controlplane runtime dependencies in Dockerfile")

for retired_snippet in [
    "ARBUZAS_DNS_LEGACY_BRIDGE_ENABLED",
    "ARBUZAS_DNS_LEGACY_BRIDGE_HOST",
    "ARBUZAS_DNS_LEGACY_BRIDGE_PORT",
    "ARBUZAS_DNS_LEGACY_IDENTITY_WEB_SCRIPT",
    "ARBUZAS_DNS_LEGACY_POLICY_PUBLISHER_SCRIPT",
    "ARBUZAS_DNS_LEGACY_POLICY_WATCH_ENABLED",
    "legacy_bridge_base_url",
]:
    if retired_snippet in config:
        raise SystemExit(f"retired config fallback still present: {retired_snippet}")

expected_archive_files = [
    "README.md",
    "adguardhome-doh-identities.py",
    "adguardhome-doh-identity-web.py",
    "adguardhome-doh-identityctl",
    "adguardhome-policy-publisher.py",
    "arbuzas-dns-nginx.conf.template",
    "test_adguardhome_doh_identity_web_helper.sh",
    "test_adguardhome_policy_publisher.sh",
]
for name in expected_archive_files:
    if not (archive_dir / name).is_file():
        raise SystemExit(f"missing archived legacy helper file: {name}")

active_helper_dir = Path("infra/adguardhome/debian")
for name in expected_archive_files[1:6]:
    if (active_helper_dir / name).exists():
        raise SystemExit(f"legacy helper still present in active runtime tree: {name}")
PY

echo "PASS: Arbuzas DNS controlplane is wired into compose, module start, deploy validation, and no longer depends on the legacy bridge runtime"
