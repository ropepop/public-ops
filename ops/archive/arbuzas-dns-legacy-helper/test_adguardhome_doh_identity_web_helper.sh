#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${SCRIPT_DIR}"
IDENTITY_HELPER="${REPO_ROOT}/infra/adguardhome/debian/adguardhome-doh-identities.py"
WEB_HELPER="${REPO_ROOT}/infra/adguardhome/debian/adguardhome-doh-identity-web.py"

for cmd in python3 jq curl; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "FAIL: ${cmd} is required" >&2
    exit 1
  fi
done
if ! command -v node >/dev/null 2>&1; then
  echo "FAIL: node is required" >&2
  exit 1
fi

if [[ ! -f "${IDENTITY_HELPER}" ]]; then
  echo "FAIL: identity helper script missing: ${IDENTITY_HELPER}" >&2
  exit 1
fi
if [[ ! -f "${WEB_HELPER}" ]]; then
  echo "FAIL: web helper script missing: ${WEB_HELPER}" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
sidecar_pid=""
trap 'if [[ -n "${sidecar_pid}" ]] && kill -0 "${sidecar_pid}" >/dev/null 2>&1; then kill "${sidecar_pid}" >/dev/null 2>&1 || true; wait "${sidecar_pid}" 2>/dev/null || true; fi; rm -rf "${tmpdir}"' EXIT
mkdir -p "${tmpdir}/state"

port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

identityctl_wrapper="${tmpdir}/identityctl"
identityctl_invocation_log="${tmpdir}/identityctl-invocations.log"
identityctl_delay_file="${tmpdir}/identityctl-delay-seconds"
cat > "${identityctl_wrapper}" <<'EOF_WRAPPER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "__IDENTITYCTL_INVOCATION_LOG__"
delay_file="__IDENTITYCTL_DELAY_FILE__"
delay_seconds=""
if [[ -f "${delay_file}" ]]; then
  delay_seconds="$(tr -d '[:space:]' < "${delay_file}")"
fi
case "${1:-}" in
  list|usage|events)
    if [[ -n "${delay_seconds}" ]]; then
      sleep "${delay_seconds}"
    else
      sleep 0.2
    fi
    ;;
esac
exec python3 "__IDENTITY_HELPER__" "$@"
EOF_WRAPPER
python3 - <<'PY' \
  "${identityctl_wrapper}" \
  "${identityctl_invocation_log}" \
  "${identityctl_delay_file}" \
  "${IDENTITY_HELPER}"
from pathlib import Path
import sys

path = Path(sys.argv[1])
path.write_text(
    path.read_text(encoding="utf-8")
    .replace("__IDENTITYCTL_INVOCATION_LOG__", sys.argv[2])
    .replace("__IDENTITYCTL_DELAY_FILE__", sys.argv[3])
    .replace("__IDENTITY_HELPER__", sys.argv[4]),
    encoding="utf-8",
)
PY
chmod 0755 "${identityctl_wrapper}"

count_invocations() {
  local prefix="$1"
  awk -v prefix="${prefix}" 'index($0, prefix) == 1 { count += 1 } END { print count + 0 }' "${identityctl_invocation_log}"
}

wait_for_ttl_expiry() {
  python3 - <<'PY' "$@"
import sys
import time

values = [float(raw) for raw in sys.argv[1:] if raw]
time.sleep((max(values) if values else 0.0) + 0.25)
PY
}

set_identityctl_delay() {
  printf '%s\n' "$1" > "${identityctl_delay_file}"
}

clear_identityctl_delay() {
  rm -f "${identityctl_delay_file}"
}

run_parallel_get() {
  local url="$1"
  local concurrency="${2:-4}"
  python3 - <<'PY' "${url}" "${concurrency}"
import concurrent.futures
import sys
import urllib.request

url = sys.argv[1]
concurrency = int(sys.argv[2])

def fetch(_index: int) -> None:
  with urllib.request.urlopen(url, timeout=5) as response:
    response.read()

with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
  list(executor.map(fetch, range(concurrency)))
PY
}

sqlite_setting() {
  local db_file="$1"
  local key="$2"
  python3 - <<'PY' "${db_file}" "${key}"
import sqlite3
import sys

conn = sqlite3.connect(sys.argv[1])
row = conn.execute("select value from state_settings where key = ?", (sys.argv[2],)).fetchone()
print("" if row is None or row[0] is None else str(row[0]))
conn.close()
PY
}

export ADGUARDHOME_DOH_IDENTITIES_FILE="${tmpdir}/doh-identities.json"
export ADGUARDHOME_DOH_USAGE_EVENTS_FILE="${tmpdir}/state/doh-usage-events.jsonl"
export ADGUARDHOME_DOH_USAGE_CURSOR_FILE="${tmpdir}/state/doh-usage-cursor.json"
export ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE="${tmpdir}/state/identity-observability.sqlite"
export ADGUARDHOME_DOH_ACCESS_LOG_FILE="${tmpdir}/remote-nginx-doh-access.log"
export ADGUARDHOME_DOT_ACCESS_LOG_FILE="${tmpdir}/remote-nginx-dot-access.log"
export ADGUARDHOME_DOH_USAGE_RETENTION_DAYS=30
export ADGUARDHOME_DOH_IDENTITYCTL_APPLY=0
export ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_JSON_FILE="${tmpdir}/querylog-fixture.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_FILTERING_STATUS_JSON_FILE="${tmpdir}/filtering-status-fixture.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_STATUS_JSON_FILE="${tmpdir}/status-fixture.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_STATS_JSON_FILE="${tmpdir}/stats-fixture.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_CLIENTS_JSON_FILE="${tmpdir}/clients-fixture.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_CLIENTS_SEARCH_JSON_FILE="${tmpdir}/clients-search-fixture.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_IPINFO_CACHE_FILE="${tmpdir}/ipinfo-cache.json"
export ADGUARDHOME_DOH_IDENTITY_WEB_RESTART_ENTRY="${tmpdir}/fake-restart-entry.sh"
export ADGUARDHOME_DOH_IDENTITY_WEB_RESTART_MODE="--remote-reload-frontend"
export ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_VIEW_PREFERENCE_FILE="${tmpdir}/state/querylog-view-preference.json"
identities_cache_ttl_seconds="0.6"
shared_lookup_cache_ttl_seconds="1.6"
querylog_result_cache_ttl_seconds="0.6"
querylog_result_stale_ttl_seconds="4.0"
usage_cache_ttl_seconds="0.6"
usage_stale_ttl_seconds="4.0"
querylog_request_timeout_seconds="0.8"
usage_request_timeout_seconds="0.8"
export ADGUARDHOME_DOH_IDENTITY_WEB_IDENTITIES_CACHE_TTL_SECONDS="${identities_cache_ttl_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_SHARED_LOOKUP_CACHE_TTL_SECONDS="${shared_lookup_cache_ttl_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_RESULT_CACHE_TTL_SECONDS="${querylog_result_cache_ttl_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_RESULT_STALE_TTL_SECONDS="${querylog_result_stale_ttl_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_USAGE_CACHE_TTL_SECONDS="${usage_cache_ttl_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_USAGE_STALE_TTL_SECONDS="${usage_stale_ttl_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_REQUEST_TIMEOUT_SECONDS="${querylog_request_timeout_seconds}"
export ADGUARDHOME_DOH_IDENTITY_WEB_USAGE_REQUEST_TIMEOUT_SECONDS="${usage_request_timeout_seconds}"
export ADGUARDHOME_REMOTE_DOT_IDENTITY_ENABLED=1
export ADGUARDHOME_REMOTE_DOT_IDENTITY_LABEL_LENGTH=20
export PIHOLE_REMOTE_DOT_HOSTNAME="dns.jolkins.id.lv"
export ADGUARDHOME_REMOTE_ROUTER_LAN_IP="192.168.31.1"
export PIHOLE_WEB_PORT=8080
reload_log="${tmpdir}/reload.log"

cache_epoch="$(date +%s)"
querylog_times="$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone

base = datetime.now(timezone.utc).replace(microsecond=100000) - timedelta(minutes=8)
for offset_seconds in range(10):
    print((base + timedelta(seconds=offset_seconds)).isoformat().replace("+00:00", "Z"))
PY
)"
querylog_internal_doh_time="$(printf '%s\n' "${querylog_times}" | sed -n '1p')"
querylog_internal_plain_time="$(printf '%s\n' "${querylog_times}" | sed -n '2p')"
querylog_self_time="$(printf '%s\n' "${querylog_times}" | sed -n '3p')"
querylog_ipv6_time="$(printf '%s\n' "${querylog_times}" | sed -n '4p')"
querylog_public_time="$(printf '%s\n' "${querylog_times}" | sed -n '5p')"
querylog_service_time="$(printf '%s\n' "${querylog_times}" | sed -n '6p')"
querylog_device_time="$(printf '%s\n' "${querylog_times}" | sed -n '7p')"
querylog_router_time="$(printf '%s\n' "${querylog_times}" | sed -n '8p')"
querylog_dot_beta_time="$(printf '%s\n' "${querylog_times}" | sed -n '9p')"
querylog_internal_blocked_time="$(printf '%s\n' "${querylog_times}" | sed -n '10p')"

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_RESTART_ENTRY}" <<EOF_RESTART
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >> "${reload_log}"
exit 0
EOF_RESTART
chmod 0755 "${ADGUARDHOME_DOH_IDENTITY_WEB_RESTART_ENTRY}"

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_JSON_FILE}" <<'EOF_QUERYLOG'
{
  "data": [
    {"time":"__QUERYLOG_INTERNAL_DOH_TIME__","client":"127.0.0.1","client_proto":"doh","elapsedMs":"5","status":"NOERROR","question":{"name":"example.com","type":"A"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_INTERNAL_PLAIN_TIME__","client":"127.0.0.1","client_proto":"plain","elapsedMs":"2","status":"NOERROR","question":{"name":"example.com","type":"AAAA"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_SELF_TIME__","client":"127.0.0.1","client_proto":"plain","elapsedMs":"3","status":"NOERROR","question":{"name":"self.example.net","type":"A"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_IPV6_TIME__","client":"::1","client_proto":"doh","elapsedMs":"4","status":"NOERROR","question":{"name":"example.com","type":"AAAA"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_PUBLIC_TIME__","client":"212.3.197.32","client_proto":"doh","elapsedMs":"12","status":"NOERROR","reason":"NotFilteredNotFound","question":{"name":"public.example.net","type":"A"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_SERVICE_TIME__","client":"212.3.197.32","client_proto":"doh","elapsedMs":"40","status":"NOERROR","question":{"name":"service.example.net","type":"A"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_DEVICE_TIME__","client":"192.168.31.39","client_proto":"doh","elapsedMs":"8","status":"NOERROR","question":{"name":"example.com","type":"A"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_ROUTER_TIME__","client":"192.168.31.1","client_proto":"doh","elapsedMs":"14","status":"NOERROR","question":{"name":"router.example","type":"A"},"client_info":{"whois":{}}},
    {"time":"__QUERYLOG_DOT_BETA_TIME__","client":"127.0.0.1","client_proto":"dot","elapsedMs":"11","status":"NOERROR","question":{"name":"beta-dot.example.net","type":"A"},"client_info":{"name":"Identity beta","disallowed_rule":"__BETA_DOT_LABEL__","whois":{}}},
    {"time":"__QUERYLOG_INTERNAL_BLOCKED_TIME__","client":"127.0.0.1","client_proto":"plain","elapsedMs":"6","status":"FILTERED","reason":"Blocked by filters","question":{"name":"blocked.internal.example","type":"A"},"rules":[{"filter_list_id":1,"text":"||blocked.internal.example^"}],"client_info":{"whois":{}}}
  ]
}
EOF_QUERYLOG
python3 - <<'PY' \
  "${ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_JSON_FILE}" \
  "${querylog_internal_doh_time}" \
  "${querylog_internal_plain_time}" \
  "${querylog_self_time}" \
  "${querylog_ipv6_time}" \
  "${querylog_public_time}" \
  "${querylog_service_time}" \
  "${querylog_device_time}" \
  "${querylog_router_time}" \
  "${querylog_dot_beta_time}" \
  "${querylog_internal_blocked_time}"
import pathlib
import sys
path = pathlib.Path(sys.argv[1])
path.write_text(
    path.read_text()
    .replace("__QUERYLOG_INTERNAL_DOH_TIME__", sys.argv[2])
    .replace("__QUERYLOG_INTERNAL_PLAIN_TIME__", sys.argv[3])
    .replace("__QUERYLOG_SELF_TIME__", sys.argv[4])
    .replace("__QUERYLOG_IPV6_TIME__", sys.argv[5])
    .replace("__QUERYLOG_PUBLIC_TIME__", sys.argv[6])
    .replace("__QUERYLOG_SERVICE_TIME__", sys.argv[7])
    .replace("__QUERYLOG_DEVICE_TIME__", sys.argv[8])
    .replace("__QUERYLOG_ROUTER_TIME__", sys.argv[9])
    .replace("__QUERYLOG_DOT_BETA_TIME__", sys.argv[10])
    .replace("__QUERYLOG_INTERNAL_BLOCKED_TIME__", sys.argv[11]),
    encoding="utf-8",
)
PY

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_FILTERING_STATUS_JSON_FILE}" <<'EOF_FILTERING_STATUS'
{
  "enabled": true,
  "interval": 24,
  "filters": [
    {"id": 1, "enabled": true, "name": "Default blocklist", "url": "https://filters.example/default.txt"}
  ],
  "whitelist_filters": [],
  "user_rules": []
}
EOF_FILTERING_STATUS

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_STATUS_JSON_FILE}" <<'EOF_STATUS'
{
  "dns_addresses": ["127.0.0.1", "192.168.31.1", "192.168.31.25"]
}
EOF_STATUS

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_STATS_JSON_FILE}" <<'EOF_STATS'
{
  "top_clients": [{"127.0.0.1": 3}, {"212.3.197.32": 1}]
}
EOF_STATS

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_CLIENTS_JSON_FILE}" <<'EOF_CLIENTS'
{
  "auto_clients": [
    {"whois_info": {}, "ip": "212.3.197.32", "name": "", "source": "ARP"},
    {"whois_info": {}, "ip": "192.168.31.25", "name": "", "source": "ARP"}
  ],
  "clients": [],
  "supported_tags": []
}
EOF_CLIENTS

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_CLIENTS_SEARCH_JSON_FILE}" <<'EOF_CLIENT_SEARCH'
[
  {
    "212.3.197.32": {
      "disallowed": false,
      "whois_info": {},
      "name": "",
      "ids": ["212.3.197.32"],
      "filtering_enabled": false,
      "parental_enabled": false,
      "safebrowsing_enabled": false,
      "safesearch_enabled": false,
      "use_global_blocked_services": false,
      "use_global_settings": false,
      "ignore_querylog": null,
      "ignore_statistics": null,
      "upstreams_cache_size": 0,
      "upstreams_cache_enabled": null
    }
  }
]
EOF_CLIENT_SEARCH

cat > "${ADGUARDHOME_DOH_IDENTITY_WEB_IPINFO_CACHE_FILE}" <<EOF_IPINFO
{"212.3.197.32":{"cachedAtEpochSeconds":${cache_epoch},"whois_info":{"country":"LV","orgname":"Operator Example"}}}
EOF_IPINFO

start_sidecar() {
  python3 "${WEB_HELPER}" \
    --host 127.0.0.1 \
    --port "${port}" \
    --identityctl "${identityctl_wrapper}" \
    --adguard-web-port 8080 \
    --skip-session-check \
    >"${tmpdir}/sidecar.log" 2>&1 &
  sidecar_pid="$!"

  for _ in $(seq 1 40); do
    if curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/inject.js" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done

  if ! curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/inject.js" >/dev/null 2>&1; then
    echo "FAIL: sidecar did not start" >&2
    exit 1
  fi
}

stop_sidecar() {
  if [[ -n "${sidecar_pid}" ]] && kill -0 "${sidecar_pid}" >/dev/null 2>&1; then
    kill "${sidecar_pid}" >/dev/null 2>&1 || true
    wait "${sidecar_pid}" 2>/dev/null || true
  fi
  sidecar_pid=""
}

start_sidecar
if ! curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/bootstrap.js" >/dev/null 2>&1; then
  echo "FAIL: bootstrap injector endpoint did not return 200" >&2
  exit 1
fi
identity_html="${tmpdir}/identity.html"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity" > "${identity_html}"
if ! rg -Fq '.table-scroll { overflow-x: auto; -webkit-overflow-scrolling: touch; }' "${identity_html}"; then
  echo "FAIL: identity page should make wide tables horizontally scrollable on narrow screens" >&2
  exit 1
fi
if ! rg -Fq 'class="table-scroll table-scroll--wide"' "${identity_html}"; then
  echo "FAIL: identity page should wrap the identities table in a wide scroll container" >&2
  exit 1
fi
if ! rg -Fq 'value="1000"' "${identity_html}"; then
  echo "FAIL: identity page should default the querylog summary limit to 1000 rows" >&2
  exit 1
fi
if ! rg -Fq 'await Promise.all([querylogPromise, refreshUsage()]);' "${identity_html}"; then
  echo "FAIL: identity page should overlap querylog and usage refresh work during load" >&2
  exit 1
fi
if ! rg -Fq 'DoT target' "${identity_html}"; then
  echo "FAIL: identity page should label the DoT column as DoT target" >&2
  exit 1
fi
if ! rg -Fq 'Copy DoT target' "${identity_html}"; then
  echo "FAIL: identity page should expose the Copy DoT target action" >&2
  exit 1
fi
if ! rg -Fq 'Download iPhone DoH profile' "${identity_html}"; then
  echo "FAIL: identity page should expose the Apple DoH profile download action" >&2
  exit 1
fi
if ! rg -Fq 'Download iPhone TLS profile' "${identity_html}"; then
  echo "FAIL: identity page should expose the Apple TLS profile download action" >&2
  exit 1
fi
if ! rg -Fq 'apple-doh.mobileconfig' "${identity_html}"; then
  echo "FAIL: identity page should reference the Apple DoH profile download route" >&2
  exit 1
fi
if ! rg -Fq 'apple-dot.mobileconfig' "${identity_html}"; then
  echo "FAIL: identity page should reference the Apple TLS profile download route" >&2
  exit 1
fi
bootstrap_js="${tmpdir}/bootstrap.js"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/bootstrap.js" > "${bootstrap_js}"
if rg -Fq '/pixel-stack/identity/api/v1/adguard/stats' "${bootstrap_js}"; then
  echo "FAIL: bootstrap.js should not rewrite native /control/stats requests" >&2
  exit 1
fi
if rg -Fq '/pixel-stack/identity/api/v1/adguard/clients' "${bootstrap_js}"; then
  echo "FAIL: bootstrap.js should not rewrite native /control/clients requests" >&2
  exit 1
fi
if rg -Fq '/pixel-stack/identity/api/v1/adguard/querylog' "${bootstrap_js}"; then
  echo "FAIL: bootstrap.js should no longer rewrite native /control/querylog requests" >&2
  exit 1
fi
if rg -Fq 'pixelstack:native-querylog-updated' "${bootstrap_js}"; then
  echo "FAIL: bootstrap.js should stop publishing native querylog update events" >&2
  exit 1
fi
if ! rg -Fq 'pixelstack:native-dashboard-updated' "${bootstrap_js}"; then
  echo "FAIL: bootstrap.js should publish native dashboard refresh events" >&2
  exit 1
fi
inject_js="${tmpdir}/inject.js"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/inject.js" > "${inject_js}"
if rg -Fq 'MutationObserver' "${inject_js}"; then
  echo "FAIL: inject.js should no longer use MutationObserver-driven global sync" >&2
  exit 1
fi
if rg -Fq 'window=30d' "${inject_js}"; then
  echo "FAIL: inject.js should not prefetch 30d usage for querylog options" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-querylog-surface' "${inject_js}"; then
  echo "FAIL: inject.js should render a sidecar-owned querylog surface" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-querylog-row--blocked {' "${inject_js}" || ! rg -Fq 'background: rgba(223, 56, 18, 0.05);' "${inject_js}"; then
  echo "FAIL: inject.js should style blocked improved-querylog rows with the native AdGuard tint" >&2
  exit 1
fi
if ! rg -Fq '@media (prefers-color-scheme: dark)' "${inject_js}" || ! rg -Fq '.pixel-stack-querylog-surface .form-control' "${inject_js}" || ! rg -Fq 'color-scheme: dark;' "${inject_js}"; then
  echo "FAIL: inject.js should carry its own dark-theme querylog styles instead of inheriting mismatched page controls" >&2
  exit 1
fi
if ! rg -Fq '#pixel-querylog' "${inject_js}"; then
  echo "FAIL: inject.js should register the dedicated improved querylog route" >&2
  exit 1
fi
if ! rg -Fq '/pixel-stack/identity/api/v1/querylog/view-preference' "${inject_js}"; then
  echo "FAIL: inject.js should save querylog view preference changes through the preference API" >&2
  exit 1
fi
if ! rg -Fq 'pixelStackQuerylogResumeTarget' "${inject_js}"; then
  echo "FAIL: inject.js should preserve querylog deep links across login redirects" >&2
  exit 1
fi
if ! rg -Fq 'Open native Query Log' "${inject_js}"; then
  echo "FAIL: inject.js should expose a native querylog escape hatch" >&2
  exit 1
fi
if ! rg -Fq 'window.addEventListener("scroll", handleQuerylogAutoLoad, { passive: true });' "${inject_js}"; then
  echo "FAIL: inject.js should auto-load improved querylog rows when the page scroll reaches the bottom" >&2
  exit 1
fi
if ! rg -Fq 'Show all requests' "${inject_js}"; then
  echo "FAIL: inject.js should expose a clear show-all-requests action from blocked querylog views" >&2
  exit 1
fi
if ! rg -Fq 'Open improved Query Log' "${inject_js}"; then
  echo "FAIL: inject.js should expose the native-route switcher into the improved querylog" >&2
  exit 1
fi
if ! rg -Fq 'waitForElement' "${inject_js}"; then
  echo "FAIL: inject.js should use route-scoped waitForElement mounting" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-summary-row' "${inject_js}"; then
  echo "FAIL: inject.js should add dashboard summary row marker classes" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-summary-card--blocked' "${inject_js}"; then
  echo "FAIL: inject.js should add blocked summary card marker classes" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--blocked .card-wrap { position: relative; }' "${inject_js}"; then
  echo "FAIL: inject.js should anchor the blocked summary chart inside the card" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--blocked .card-body-stats { position: relative; z-index: 2; padding-bottom: 0.75rem; }' "${inject_js}"; then
  echo "FAIL: inject.js should keep blocked summary text above the full-height chart" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--blocked .card-chart-bg { position: absolute; inset: 0; height: 100%; min-height: 100%; z-index: 1; }' "${inject_js}"; then
  echo "FAIL: inject.js should render the blocked summary chart across the full card height" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--dns .card-wrap { position: relative; }' "${inject_js}"; then
  echo "FAIL: inject.js should anchor the DNS summary chart inside the card" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--dns .card-body-stats { position: relative; z-index: 2; padding-bottom: 0.75rem; }' "${inject_js}"; then
  echo "FAIL: inject.js should keep DNS summary text above the full-height chart" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--dns .card-chart-bg { position: absolute; inset: 0; height: 100%; min-height: 100%; z-index: 1; }' "${inject_js}"; then
  echo "FAIL: inject.js should render the DNS summary chart across the full card height" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-summary-card--dns .card-title-stats a { text-shadow: 0 1px 2px rgba(255, 255, 255, 0.92), 0 0 0.8rem rgba(255, 255, 255, 0.72); }' "${inject_js}"; then
  echo "FAIL: inject.js should keep DNS summary text readable with the blocked-summary shadow treatment" >&2
  exit 1
fi
if ! rg -Fq 'text-shadow: 0 1px 2px rgba(255, 255, 255, 0.92), 0 0 0.8rem rgba(255, 255, 255, 0.72);' "${inject_js}"; then
  echo "FAIL: inject.js should keep blocked summary text readable with a shadow-only treatment" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-summary-card--compact-quarter' "${inject_js}"; then
  echo "FAIL: inject.js should include quarter-height compact summary card classes" >&2
  exit 1
fi
if ! rg -Fq 'grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop summary reflow grid rule" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-dashboard-masonry-row' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop dashboard masonry row class" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-dashboard-masonry-columns' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop dashboard masonry wrapper class" >&2
  exit 1
fi
if ! rg -Fq 'pixel-stack-dashboard-masonry-item' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop dashboard masonry item class" >&2
  exit 1
fi
if ! rg -Fq -- '--pixel-stack-dashboard-gap:' "${inject_js}"; then
  echo "FAIL: inject.js should define shared desktop dashboard spacing tokens" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-dashboard-toolbar {' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop dashboard toolbar spacing class" >&2
  exit 1
fi
if ! rg -Fq 'gap: var(--pixel-stack-dashboard-gap);' "${inject_js}"; then
  echo "FAIL: inject.js should normalize desktop masonry wrapper spacing with gap" >&2
  exit 1
fi
if ! rg -Fq 'gap: var(--pixel-stack-dashboard-section-gap);' "${inject_js}"; then
  echo "FAIL: inject.js should normalize desktop stacked card spacing with gap" >&2
  exit 1
fi
if ! rg -Fq '.pixel-stack-dashboard-later-row {' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop later-section spacing class" >&2
  exit 1
fi
if ! rg -Fq 'matchMedia(DASHBOARD_DESKTOP_MEDIA_QUERY)' "${inject_js}"; then
  echo "FAIL: inject.js should include the desktop dashboard breakpoint listener" >&2
  exit 1
fi
node - <<'EOF_NODE' "${bootstrap_js}" "${inject_js}"
const fs = require("fs");
const vm = require("vm");

const bootstrapJsPath = process.argv[2];
const injectJsPath = process.argv[3];
const bootstrapJs = fs.readFileSync(bootstrapJsPath, "utf8");
const injectJs = fs.readFileSync(injectJsPath, "utf8");

let documentRef = null;

class Element {
  constructor(tagName, ownerDocument) {
    this.tagName = String(tagName || "div").toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.parentElement = null;
    this.attributes = new Map();
    this.listeners = new Map();
    this.id = "";
    this.className = "";
    this.dataset = {};
    this._textContent = "";
    this._innerHTML = "";
    this._mockRectWidth = 0;
    this._mockRectHeight = 0;
    this.style = {};
    this.value = "";
    this.disabled = false;
  }

  get isConnected() {
    let current = this;
    while (current) {
      if (current === this.ownerDocument.body || current === this.ownerDocument.head) {
        return true;
      }
      current = current.parentElement;
    }
    return false;
  }

  set textContent(value) {
    this._textContent = String(value ?? "");
  }

  get textContent() {
    if (this.children.length) {
      return this.children.map((child) => child.textContent).join("");
    }
    return this._textContent;
  }

  set innerHTML(value) {
    this._innerHTML = String(value ?? "");
    this._textContent = this._innerHTML.replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
    this.children = [];
    if (this._innerHTML.includes("data-pixel-identities-body='1'") || this._innerHTML.includes('data-pixel-identities-body="1"')) {
      const header = new Element("div", this.ownerDocument);
      header.className = "card-header with-border";
      header.setMockRect(64);
      const headerInner = new Element("div", this.ownerDocument);
      headerInner.className = "card-inner";
      const title = new Element("div", this.ownerDocument);
      title.className = "card-title";
      title.textContent = "Top identities";
      headerInner.appendChild(title);
      const subtitle = new Element("div", this.ownerDocument);
      subtitle.className = "card-subtitle";
      subtitle.textContent = "for the last 24 hours";
      headerInner.appendChild(subtitle);
      header.appendChild(headerInner);
      const refreshButton = new Element("button", this.ownerDocument);
      refreshButton.setAttribute("data-pixel-refresh-identities", "1");
      header.appendChild(refreshButton);
      this.appendChild(header);
      const cardTable = new Element("div", this.ownerDocument);
      cardTable.className = "card-table";
      cardTable.setMockRect(148);
      const table = new Element("table", this.ownerDocument);
      cardTable.appendChild(table);
      const tbody = new Element("tbody", this.ownerDocument);
      tbody.setAttribute("data-pixel-identities-body", "1");
      table.appendChild(tbody);
      this.appendChild(cardTable);
    }
    if (this._innerHTML.includes("<select id=\"pixel-stack-querylog-identity\"")) {
      const select = new Element("select", this.ownerDocument);
      select.id = "pixel-stack-querylog-identity";
      this.appendChild(select);
    }
    if (this._innerHTML.includes("data-pixel-querylog-form='1'") || this._innerHTML.includes('data-pixel-querylog-form="1"')) {
      const form = new Element("form", this.ownerDocument);
      form.className = "pixel-stack-querylog-toolbar";
      form.setAttribute("data-pixel-querylog-form", "1");
      const searchInput = new Element("input", this.ownerDocument);
      searchInput.id = "pixel-stack-querylog-search";
      form.appendChild(searchInput);
      const statusSelect = new Element("select", this.ownerDocument);
      statusSelect.id = "pixel-stack-querylog-status";
      form.appendChild(statusSelect);
      const identitySelect = new Element("select", this.ownerDocument);
      identitySelect.id = "pixel-stack-querylog-identity";
      form.appendChild(identitySelect);
      const applyButton = new Element("button", this.ownerDocument);
      applyButton.setAttribute("data-pixel-querylog-apply", "1");
      form.appendChild(applyButton);
      const refreshButton = new Element("button", this.ownerDocument);
      refreshButton.setAttribute("data-pixel-querylog-refresh", "1");
      form.appendChild(refreshButton);
      const nativeOpen = new Element("button", this.ownerDocument);
      nativeOpen.setAttribute("data-pixel-querylog-native-open", "1");
      form.appendChild(nativeOpen);
      this.appendChild(form);

      const note = new Element("div", this.ownerDocument);
      note.setAttribute("data-pixel-querylog-status", "1");
      this.appendChild(note);

      const tableWrap = new Element("div", this.ownerDocument);
      tableWrap.setAttribute("data-pixel-querylog-table-wrap", "1");
      const table = new Element("table", this.ownerDocument);
      const tbody = new Element("tbody", this.ownerDocument);
      tbody.setAttribute("data-pixel-querylog-body", "1");
      table.appendChild(tbody);
      tableWrap.appendChild(table);
      this.appendChild(tableWrap);

      const footer = new Element("div", this.ownerDocument);
      const meta = new Element("div", this.ownerDocument);
      meta.setAttribute("data-pixel-querylog-meta", "1");
      footer.appendChild(meta);
      const loadMore = new Element("button", this.ownerDocument);
      loadMore.setAttribute("data-pixel-querylog-load-more", "1");
      footer.appendChild(loadMore);
      const nativeLink = new Element("a", this.ownerDocument);
      nativeLink.setAttribute("data-pixel-querylog-native-link", "1");
      footer.appendChild(nativeLink);
      this.appendChild(footer);

      const modalBackdrop = new Element("div", this.ownerDocument);
      modalBackdrop.className = "pixel-stack-querylog-modal-backdrop";
      modalBackdrop.setAttribute("data-pixel-querylog-modal", "1");
      const modal = new Element("div", this.ownerDocument);
      modal.className = "pixel-stack-querylog-modal";
      modalBackdrop.appendChild(modal);

      const modalHeader = new Element("div", this.ownerDocument);
      modalHeader.className = "pixel-stack-querylog-modal-header";
      const modalHeaderText = new Element("div", this.ownerDocument);
      const modalTitle = new Element("h3", this.ownerDocument);
      modalTitle.className = "pixel-stack-querylog-modal-title";
      modalTitle.setAttribute("data-pixel-querylog-modal-title", "1");
      modalHeaderText.appendChild(modalTitle);
      const modalSubtitle = new Element("div", this.ownerDocument);
      modalSubtitle.className = "pixel-stack-querylog-modal-subtitle";
      modalSubtitle.setAttribute("data-pixel-querylog-modal-subtitle", "1");
      modalHeaderText.appendChild(modalSubtitle);
      modalHeader.appendChild(modalHeaderText);
      const modalCloseTop = new Element("button", this.ownerDocument);
      modalCloseTop.setAttribute("data-pixel-querylog-modal-close", "1");
      modalHeader.appendChild(modalCloseTop);
      modal.appendChild(modalHeader);

      const modalBodyWrap = new Element("div", this.ownerDocument);
      modalBodyWrap.className = "pixel-stack-querylog-modal-body";
      const modalBody = new Element("div", this.ownerDocument);
      modalBody.className = "pixel-stack-querylog-details-grid";
      modalBody.setAttribute("data-pixel-querylog-modal-body", "1");
      modalBodyWrap.appendChild(modalBody);
      modal.appendChild(modalBodyWrap);

      const modalFooter = new Element("div", this.ownerDocument);
      modalFooter.className = "pixel-stack-querylog-modal-footer";
      const modalCloseBottom = new Element("button", this.ownerDocument);
      modalCloseBottom.setAttribute("data-pixel-querylog-modal-close", "1");
      modalFooter.appendChild(modalCloseBottom);
      modal.appendChild(modalFooter);

      this.appendChild(modalBackdrop);
    }
    if (this._innerHTML.includes("data-pixel-querylog-open-improved='1'") || this._innerHTML.includes('data-pixel-querylog-open-improved="1"')) {
      const title = new Element("h2", this.ownerDocument);
      title.className = "pixel-stack-querylog-switcher-title";
      this.appendChild(title);
      const copy = new Element("p", this.ownerDocument);
      copy.className = "pixel-stack-querylog-switcher-copy";
      this.appendChild(copy);
      const actions = new Element("div", this.ownerDocument);
      actions.className = "pixel-stack-querylog-switcher-actions";
      const improvedButton = new Element("button", this.ownerDocument);
      improvedButton.setAttribute("data-pixel-querylog-open-improved", "1");
      actions.appendChild(improvedButton);
      this.appendChild(actions);
      const note = new Element("div", this.ownerDocument);
      note.setAttribute("data-pixel-querylog-native-note", "1");
      this.appendChild(note);
    }
  }

  get innerHTML() {
    return this._innerHTML;
  }

  appendChild(child) {
    if (child.parentElement) {
      const siblings = child.parentElement.children;
      const index = siblings.indexOf(child);
      if (index >= 0) {
        siblings.splice(index, 1);
      }
    }
    child.parentElement = this;
    this.children.push(child);
    if (this.tagName === "SELECT" && child.selected === true) {
      this.value = child.value || "";
    }
    return child;
  }

  insertBefore(child, referenceNode) {
    if (!referenceNode || referenceNode.parentElement !== this) {
      return this.appendChild(child);
    }
    if (child.parentElement) {
      const siblings = child.parentElement.children;
      const index = siblings.indexOf(child);
      if (index >= 0) {
        siblings.splice(index, 1);
      }
    }
    const targetIndex = this.children.indexOf(referenceNode);
    child.parentElement = this;
    this.children.splice(targetIndex, 0, child);
    return child;
  }

  remove() {
    if (!this.parentElement) {
      return;
    }
    const siblings = this.parentElement.children;
    const index = siblings.indexOf(this);
    if (index >= 0) {
      siblings.splice(index, 1);
    }
    this.parentElement = null;
  }

  insertAdjacentElement(position, element) {
    if (!this.parentElement) {
      return null;
    }
    if (element.parentElement) {
      const previousSiblings = element.parentElement.children;
      const previousIndex = previousSiblings.indexOf(element);
      if (previousIndex >= 0) {
        previousSiblings.splice(previousIndex, 1);
      }
    }
    const siblings = this.parentElement.children;
    const index = siblings.indexOf(this);
    element.parentElement = this.parentElement;
    if (position === "beforebegin") {
      siblings.splice(index, 0, element);
      return element;
    }
    if (position === "afterend") {
      siblings.splice(index + 1, 0, element);
      return element;
    }
    return element;
  }

  setAttribute(name, value) {
    const stringValue = String(value ?? "");
    this.attributes.set(name, stringValue);
    if (name === "id") {
      this.id = stringValue;
    }
    if (name === "class") {
      this.className = stringValue;
    }
  }

  getAttribute(name) {
    if (name === "id") {
      return this.id || null;
    }
    if (name === "class") {
      return this.className || null;
    }
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }

  setMockRect(height, width = this._mockRectWidth || 0) {
    this._mockRectHeight = Number(height) || 0;
    this._mockRectWidth = Number(width) || 0;
  }

  getBoundingClientRect() {
    const width = this._mockRectWidth || 0;
    if (this._mockRectHeight) {
      return {
        top: 0,
        right: width,
        bottom: this._mockRectHeight,
        left: 0,
        width,
        height: this._mockRectHeight,
      };
    }
    const childHeight = this.children.reduce((total, child) => (
      total + (Number(child.getBoundingClientRect().height) || 0)
    ), 0);
    return {
      top: 0,
      right: width,
      bottom: childHeight,
      left: 0,
      width,
      height: childHeight,
    };
  }

  addEventListener(type, handler) {
    this.listeners.set(type, handler);
  }

  dispatchEvent(eventOrType) {
    const event = typeof eventOrType === "string"
      ? { type: eventOrType, target: this, currentTarget: this, preventDefault() {} }
      : { preventDefault() {}, target: this, currentTarget: this, ...eventOrType };
    const handler = this.listeners.get(event.type);
    if (handler) {
      handler(event);
      return;
    }
    const directHandler = this[`on${event.type}`];
    if (typeof directHandler === "function") {
      directHandler(event);
    }
  }

  matches(selector) {
    if (!selector) {
      return false;
    }
    const tagClassMatch = selector.match(/^([a-z0-9_-]+)((?:\.[a-z0-9_-]+)+)$/i);
    if (tagClassMatch) {
      const classes = tagClassMatch[2].split(".").filter(Boolean);
      return (
        this.tagName.toLowerCase() === tagClassMatch[1].toLowerCase() &&
        classes.every((className) => this.className.split(/\s+/).filter(Boolean).includes(className))
      );
    }
    const multiClassMatch = selector.match(/^((?:\.[a-z0-9_-]+)+)$/i);
    if (multiClassMatch) {
      const classes = multiClassMatch[1].split(".").filter(Boolean);
      return classes.every((className) => this.className.split(/\s+/).filter(Boolean).includes(className));
    }
    if (selector.startsWith("#")) {
      return this.id === selector.slice(1);
    }
    if (selector.startsWith(".")) {
      return this.className.split(/\s+/).filter(Boolean).includes(selector.slice(1));
    }
    const attrMatch = selector.match(/^\[([^=\]]+)=['"]?([^'"\]]+)['"]?\]$/);
    if (attrMatch) {
      return this.getAttribute(attrMatch[1]) === attrMatch[2];
    }
    return this.tagName.toLowerCase() === selector.toLowerCase();
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }

  querySelectorAll(selector) {
    const results = [];
    const visit = (node) => {
      for (const child of node.children) {
        if (child.matches(selector)) {
          results.push(child);
        }
        visit(child);
      }
    };
    visit(this);
    return results;
  }
}

class Document {
  constructor() {
    this.title = "AdGuard Home";
    this.head = new Element("head", this);
    this.body = new Element("body", this);
    this.listeners = new Map();
  }

  createElement(tagName) {
    return new Element(tagName, this);
  }

  getElementById(id) {
    const visit = (node) => {
      if (node.id === id) {
        return node;
      }
      for (const child of node.children) {
        const match = visit(child);
        if (match) {
          return match;
        }
      }
      return null;
    };
    return visit(this.head) || visit(this.body);
  }

  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }

  querySelectorAll(selector) {
    return [...this.head.querySelectorAll(selector), ...this.body.querySelectorAll(selector)];
  }

  addEventListener(type, handler) {
    this.listeners.set(type, handler);
  }

  dispatchEvent(eventOrType) {
    const event = typeof eventOrType === "string"
      ? { type: eventOrType, target: this, currentTarget: this }
      : { target: this, currentTarget: this, ...eventOrType };
    const handler = this.listeners.get(event.type);
    if (handler) {
      handler(event);
    }
  }
}

documentRef = new Document();

const fetchLog = [];
let usage24hCallCount = 0;
let usage24hResponseCounts = [7, 9];
let usage24hResponseDelaysMs = [0, 250];
let proxyQuerylogCallCount = 0;
let querylogPreferenceView = "improved";
const querylogPreferencePostFailures = [];
const windowListeners = new Map();
const mediaQueries = new Map();
const noop = () => {};
const createStorage = () => {
  const values = new Map();
  return {
    getItem(key) {
      return values.has(String(key)) ? values.get(String(key)) : null;
    },
    setItem(key, value) {
      values.set(String(key), String(value ?? ""));
    },
    removeItem(key) {
      values.delete(String(key));
    },
    clear() {
      values.clear();
    },
  };
};

class FakeResponse {
  constructor(url, payload, status = 200) {
    this.url = String(url || "");
    this.status = status;
    this.ok = status >= 200 && status < 300;
    this._payload = payload;
  }

  async json() {
    return JSON.parse(JSON.stringify(this._payload));
  }

  async text() {
    return JSON.stringify(this._payload);
  }

  clone() {
    return new FakeResponse(this.url, this._payload, this.status);
  }
}

class FakeRequest {
  constructor(url, init = {}) {
    this.url = String(url || "");
    this.method = init.method;
    this.headers = init.headers;
    this.body = init.body;
  }
}

function FakeXMLHttpRequest() {}
FakeXMLHttpRequest.prototype.open = noop;
FakeXMLHttpRequest.prototype.send = noop;
FakeXMLHttpRequest.prototype.addEventListener = noop;

const windowObject = {
  document: documentRef,
  location: {
    hash: "",
    search: "",
    pathname: "/",
    origin: "https://example.test",
  },
  innerHeight: 0,
  scrollY: 0,
  pageYOffset: 0,
  history: {
    state: null,
    replaceState(state, _title, url) {
      this.state = state;
      const parsed = new URL(String(url || ""), windowObject.location.origin);
      windowObject.location.pathname = parsed.pathname;
      windowObject.location.search = parsed.search;
      windowObject.location.hash = parsed.hash;
    },
  },
  setTimeout,
  clearTimeout,
  addEventListener(type, handler) {
    windowListeners.set(type, handler);
  },
  dispatchEvent(event) {
    const eventType = typeof event === "string" ? event : event && event.type;
    const handler = windowListeners.get(eventType);
    if (handler) {
      handler(event);
    }
  },
  matchMedia(query) {
    const key = String(query || "");
    if (!mediaQueries.has(key)) {
      const listeners = new Set();
      const mediaQuery = {
        media: key,
        matches: true,
        addEventListener(type, handler) {
          if (type === "change") {
            listeners.add(handler);
          }
        },
        removeEventListener(type, handler) {
          if (type === "change") {
            listeners.delete(handler);
          }
        },
        addListener(handler) {
          listeners.add(handler);
        },
        removeListener(handler) {
          listeners.delete(handler);
        },
        dispatch(matches) {
          mediaQuery.matches = matches;
          const event = { type: "change", media: key, matches };
          listeners.forEach((handler) => handler(event));
        },
      };
      mediaQueries.set(key, mediaQuery);
    }
    return mediaQueries.get(key);
  },
  fetch: async (url, init) => {
    const resolvedUrl = typeof url === "string" ? url : String(url && url.url ? url.url : url);
    fetchLog.push({ method: String(init?.method || "GET").toUpperCase(), url: resolvedUrl });
    if (resolvedUrl.includes("/pixel-stack/identity/api/v1/querylog/view-preference")) {
      if (String(init?.method || "GET").toUpperCase() === "POST") {
        const payload = JSON.parse(String(init && init.body || "{}"));
        const requestedView = payload.defaultView === "improved" ? "improved" : "native";
        if (querylogPreferencePostFailures[0] === requestedView) {
          querylogPreferencePostFailures.shift();
          return new FakeResponse(resolvedUrl, { error: `persist ${requestedView} failed` }, 500);
        }
        querylogPreferenceView = requestedView;
      }
      return new FakeResponse(resolvedUrl, {
        defaultView: querylogPreferenceView,
        updatedAt: "2026-03-07T05:00:00Z",
      });
    }
    if (resolvedUrl.includes("/pixel-stack/identity/api/v1/usage?identity=all&window=24h")) {
      usage24hCallCount += 1;
      const responseIndex = Math.max(0, usage24hCallCount - 1);
      const requestCount = usage24hResponseCounts[Math.min(responseIndex, usage24hResponseCounts.length - 1)];
      const responseDelayMs = usage24hResponseDelaysMs[Math.min(responseIndex, usage24hResponseDelaysMs.length - 1)] || 0;
      return await new Promise((resolve) => {
        setTimeout(() => {
          resolve(new FakeResponse(resolvedUrl, {
              pixelMeta: { cacheState: "fresh", stale: false, durationMs: 10, generatedAt: "2026-03-07T05:00:00Z" },
              identities: [{ id: "alpha", requestCount }],
            }));
        }, responseDelayMs);
      });
    }
    if (resolvedUrl.includes("/pixel-stack/identity/api/v1/identities")) {
      return new FakeResponse(resolvedUrl, { identities: [] });
    }
    if (resolvedUrl.includes("/pixel-stack/identity/api/v1/querylog")) {
      proxyQuerylogCallCount += 1;
      const requestUrl = new URL(resolvedUrl, windowObject.location.origin);
      const requestIdentity = requestUrl.searchParams.get("pixel_identity") || requestUrl.searchParams.get("identity") || "";
      const requestSearch = requestUrl.searchParams.get("search") || "";
      const requestResponseStatus = requestUrl.searchParams.get("response_status") || "all";
      const olderThan = requestUrl.searchParams.get("older_than") || "";
      const responseLabel = proxyQuerylogCallCount >= 2 && requestIdentity === "alpha" ? "alpha-refresh" : requestIdentity;
      return await new Promise((resolve) => {
        setTimeout(() => {
          const noMoreMatchingRows = Boolean(olderThan && requestSearch === "ocsp2.apple.com");
          const data = noMoreMatchingRows ? [] : buildProxyQuerylogRows({
            identity: requestIdentity,
            search: requestSearch,
            responseStatus: requestResponseStatus,
            olderThan,
            identityLabel: responseLabel,
          });
          const payload = {
            data,
            oldest: data.length ? data[data.length - 1].time : olderThan,
            pixelIdentityRequested: requestIdentity,
            pixelMeta: {
              cacheState: "fresh",
              stale: false,
              durationMs: 20,
              generatedAt: "2026-03-07T05:00:00Z",
              pagesScanned: 1,
              unmatchedCount: data.filter((row) => !row.pixelIdentityId).length,
              hasMore: !noMoreMatchingRows && Boolean(data.length ? data[data.length - 1].time : olderThan),
            },
          };
          resolve(new FakeResponse(resolvedUrl, payload));
        }, 250);
      });
    }
    if (resolvedUrl.includes("/control/querylog")) {
      return new FakeResponse(resolvedUrl, {
        data: [
        { question: { name: "unfiltered.example.net" } },
        { question: { name: "still-unfiltered.example.net" } },
        ],
        oldest: "2026-03-07T04:59:57.100000Z",
      });
    }
    return new FakeResponse(resolvedUrl, {});
  },
  XMLHttpRequest: FakeXMLHttpRequest,
  Request: FakeRequest,
  Map,
  URL,
  URLSearchParams,
  sessionStorage: createStorage(),
  CustomEvent: function CustomEvent(type, init) {
    this.type = type;
    this.detail = init && init.detail;
  },
  console,
};

const context = {
  window: windowObject,
  document: documentRef,
  fetch: windowObject.fetch,
  XMLHttpRequest: FakeXMLHttpRequest,
  console,
  Map,
  URL,
  URLSearchParams,
  Request: FakeRequest,
  history: windowObject.history,
  CustomEvent: windowObject.CustomEvent,
  setTimeout,
  clearTimeout,
};
windowObject.window = windowObject;
windowObject.globalThis = windowObject;
context.globalThis = windowObject;

const dashboardRoot = documentRef.createElement("div");
documentRef.body.appendChild(dashboardRoot);
const setDashboardDesktopMatches = (matches) => {
  windowObject.matchMedia("(min-width: 992px)").dispatch(Boolean(matches));
};

const mountDashboardToolbarRow = () => {
  const row = documentRef.createElement("div");
  row.className = "row";
  const title = documentRef.createElement("h1");
  title.className = "page-title";
  title.textContent = "Dashboard";
  row.appendChild(title);
  const refresh = documentRef.createElement("button");
  refresh.className = "btn";
  refresh.textContent = "Refresh statistics";
  row.appendChild(refresh);
  dashboardRoot.appendChild(row);
  return row;
};

const summaryCardSpecs = [
  { variant: "dns", title: "DNS Queries", href: "#logs", value: "15,653", percent: "" },
  { variant: "blocked", title: "Blocked by Filters", href: "#logs?response_status=blocked", value: "3,150", percent: "20.12" },
  { variant: "safebrowsing", title: "Blocked malware/phishing", href: "#logs?response_status=blocked_safebrowsing", value: "0", percent: "0" },
  { variant: "adult", title: "Blocked adult websites", href: "#logs?response_status=blocked_parental", value: "0", percent: "0" },
];

const mountDashboardSummaryRow = () => {
  const row = documentRef.createElement("div");
  row.className = "row";
  summaryCardSpecs.forEach((spec) => {
    const column = documentRef.createElement("div");
    column.className = "col-sm-6 col-lg-3";
    const card = documentRef.createElement("div");
    card.className = "card card--full";
    const wrap = documentRef.createElement("div");
    wrap.className = "card-wrap";
    const body = documentRef.createElement("div");
    body.className = "card-body-stats";
    const value = documentRef.createElement("div");
    value.className = "card-value card-value-stats";
    value.textContent = spec.value;
    const title = documentRef.createElement("div");
    title.className = "card-title-stats";
    const link = documentRef.createElement("a");
    link.setAttribute("href", spec.href);
    link.textContent = spec.title;
    title.appendChild(link);
    body.appendChild(value);
    body.appendChild(title);
    wrap.appendChild(body);
    if (spec.percent) {
      const percent = documentRef.createElement("div");
      percent.className = "card-value card-value-percent";
      percent.textContent = spec.percent;
      wrap.appendChild(percent);
    }
    const chart = documentRef.createElement("div");
    chart.className = "card-chart-bg";
    wrap.appendChild(chart);
    card.appendChild(wrap);
    column.appendChild(card);
    row.appendChild(column);
  });
  dashboardRoot.appendChild(row);
  return row;
};

const dashboardCardSpecs = [
  { key: "general", title: "General statistics", height: 180 },
  { key: "topClients", title: "Top clients", height: 220 },
  { key: "queried", title: "Top queried domains", height: 170 },
  { key: "blocked", title: "Top blocked domains", height: 165 },
  { key: "upstreams", title: "Top upstreams", height: 140 },
  { key: "avg", title: "Average upstream response time", height: 130 },
];

let dashboardCardsRowRef = null;

const createDashboardCard = (spec) => {
  const card = documentRef.createElement("div");
  card.className = "card";
  card.setMockRect(spec.height);
  const title = documentRef.createElement("div");
  title.className = "card-title";
  title.textContent = spec.title;
  card.appendChild(title);
  return card;
};

const createDashboardCardColumn = (spec) => {
  const column = documentRef.createElement("div");
  column.className = "col-lg-6";
  column.appendChild(createDashboardCard(spec));
  return column;
};

const dashboardCardsRow = () => dashboardCardsRowRef;

const mountDashboardCardsRow = ({ includeTopClients = false } = {}) => {
  const row = documentRef.createElement("div");
  row.className = "row row-cards dashboard";
  dashboardCardSpecs.forEach((spec) => {
    if (!includeTopClients && spec.key === "topClients") {
      return;
    }
    row.appendChild(createDashboardCardColumn(spec));
  });
  dashboardRoot.appendChild(row);
  dashboardCardsRowRef = row;
  return row;
};

const mountDashboardLaterSectionRow = () => {
  const row = documentRef.createElement("div");
  row.className = "row";
  const column = documentRef.createElement("div");
  column.className = "col-lg-6";
  const card = documentRef.createElement("div");
  card.className = "card";
  card.setMockRect(150);
  const title = documentRef.createElement("div");
  title.className = "card-title";
  title.textContent = "Later dashboard section";
  card.appendChild(title);
  column.appendChild(card);
  row.appendChild(column);
  dashboardRoot.appendChild(row);
  return row;
};

const mountTopClientsCard = () => {
  const row = dashboardCardsRow() || mountDashboardCardsRow();
  const existing = Array.from(row.querySelectorAll(".card")).find((card) => {
    const title = card.querySelector(".card-title");
    return title && title.textContent === "Top clients";
  });
  if (existing) {
    return existing;
  }
  const topClientsSpec = dashboardCardSpecs.find((spec) => spec.key === "topClients");
  const column = createDashboardCardColumn(topClientsSpec);
  const referenceNode = row.children[1] || null;
  if (referenceNode) {
    row.insertBefore(column, referenceNode);
  } else {
    row.appendChild(column);
  }
  return column.querySelector(".card");
};

const expectDecoratedSummaryCards = (stage) => {
  const summaryRows = documentRef.querySelectorAll(".pixel-stack-summary-row");
  if (summaryRows.length !== 1) {
    fail(`dashboard summary row should be decorated during ${stage}`);
  }
  for (const variant of ["dns", "blocked", "safebrowsing", "adult"]) {
    if (documentRef.querySelectorAll(`.pixel-stack-summary-col--${variant}`).length !== 1) {
      fail(`dashboard summary column ${variant} should be decorated during ${stage}`);
    }
    if (documentRef.querySelectorAll(`.pixel-stack-summary-card--${variant}`).length !== 1) {
      fail(`dashboard summary card ${variant} should be decorated during ${stage}`);
    }
  }
  if (documentRef.querySelectorAll(".pixel-stack-summary-card--compact").length !== 3) {
    fail(`three compact dashboard summary cards should be decorated during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-summary-card--compact-quarter").length !== 2) {
    fail(`two quarter-height dashboard summary cards should be decorated during ${stage}`);
  }
};

const expectDashboardDesktopSpacingClasses = (stage) => {
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-desktop-surface").length !== 1) {
    fail(`dashboard desktop surface class should be applied exactly once during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-toolbar").length !== 1) {
    fail(`dashboard toolbar spacing class should be applied exactly once during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-summary-section").length !== 1) {
    fail(`dashboard summary spacing class should be applied exactly once during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-section").length !== 1) {
    fail(`dashboard masonry spacing class should be applied exactly once during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-later-row").length !== 1) {
    fail(`dashboard later-section spacing class should be applied exactly once during ${stage}`);
  }
};

const expectDashboardDesktopSpacingRemoved = (stage) => {
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-desktop-surface").length !== 0) {
    fail(`dashboard desktop surface class should be removed during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-toolbar").length !== 0) {
    fail(`dashboard toolbar spacing class should be removed during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-summary-section").length !== 0) {
    fail(`dashboard summary spacing class should be removed during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-section").length !== 0) {
    fail(`dashboard masonry spacing class should be removed during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-later-row").length !== 0) {
    fail(`dashboard later-section spacing class should be removed during ${stage}`);
  }
};

const directDashboardTitles = () => (
  Array.from((dashboardCardsRow() && dashboardCardsRow().children) || [])
    .filter((node) => node.querySelector && node.querySelector(".card-title"))
    .map((node) => {
      const title = node.querySelector(".card-title");
      return title ? title.textContent : "";
    })
);

const masonryColumnTitles = () => (
  Array.from(documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-col")).map((column) => (
    Array.from(column.querySelectorAll(".pixel-stack-dashboard-masonry-item")).map((item) => {
      const title = item.querySelector(".card-title");
      return title ? title.textContent : "";
    })
  ))
);

const expectDashboardMasonryLayout = (stage) => {
  const row = dashboardCardsRow();
  if (!row || !row.querySelector(".pixel-stack-dashboard-masonry-columns")) {
    fail(`dashboard masonry wrapper should be mounted during ${stage}`);
  }
  if (!documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-row").length) {
    fail(`dashboard masonry row class should be applied during ${stage}`);
  }
  const columns = documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-col");
  if (columns.length !== 2) {
    fail(`dashboard masonry should render exactly two columns during ${stage}`);
  }
  const items = documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-item");
  if (items.length !== 6) {
    fail(`dashboard masonry should redistribute six dashboard items during ${stage}`);
  }
  const topClientsHost = Array.from(items).find((item) => item.textContent.includes("Top clients"));
  if (!topClientsHost || !topClientsHost.textContent.includes("Top identities")) {
    fail(`Top identities should stay grouped with Top clients during ${stage}`);
  }
  const titles = masonryColumnTitles();
  const expected = [
    ["General statistics", "Top queried domains", "Top blocked domains", "Average upstream response time"],
    ["Top clients", "Top upstreams"],
  ];
  if (JSON.stringify(titles) !== JSON.stringify(expected)) {
    fail(`dashboard masonry should balance cards deterministically during ${stage}; saw ${JSON.stringify(titles)}`);
  }
};

const expectNativeDashboardOrder = (stage) => {
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-columns").length !== 0) {
    fail(`dashboard masonry wrapper should be removed during ${stage}`);
  }
  if (documentRef.querySelectorAll(".pixel-stack-dashboard-masonry-item").length !== 0) {
    fail(`dashboard masonry item classes should be removed during ${stage}`);
  }
  const titles = directDashboardTitles();
  const expected = [
    "General statistics",
    "Top clients",
    "Top queried domains",
    "Top blocked domains",
    "Top upstreams",
    "Average upstream response time",
  ];
  if (JSON.stringify(titles) !== JSON.stringify(expected)) {
    fail(`dashboard cards should restore native order during ${stage}; saw ${JSON.stringify(titles)}`);
  }
  const topClientsHost = Array.from((dashboardCardsRow() && dashboardCardsRow().children) || []).find((node) => node.textContent.includes("Top clients"));
  if (!topClientsHost || !topClientsHost.textContent.includes("Top identities")) {
    fail(`Top identities should remain grouped with Top clients during ${stage}`);
  }
};

mountDashboardToolbarRow();
mountDashboardSummaryRow();
mountDashboardCardsRow();
mountDashboardLaterSectionRow();

setTimeout(() => {
  mountTopClientsCard();
}, 5000);

vm.createContext(context);
vm.runInContext(bootstrapJs, context);
context.fetch = windowObject.fetch;
vm.runInContext(injectJs, context);

const fail = (message) => {
  console.error(`FAIL: ${message}`);
  process.exit(1);
};

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const setLocation = (target) => {
  const parsed = new URL(String(target), windowObject.location.origin);
  windowObject.location.pathname = parsed.pathname;
  windowObject.location.search = parsed.search;
  windowObject.location.hash = parsed.hash;
};

const navigateAnchor = (anchor) => {
  if (!anchor || typeof anchor.getAttribute !== "function") {
    fail("anchor navigation helper requires an element with an href attribute");
  }
  const href = anchor.getAttribute("href");
  if (!href) {
    fail("anchor navigation helper requires a non-empty href");
  }
  const previousPath = `${windowObject.location.pathname}${windowObject.location.search}`;
  const previousHash = windowObject.location.hash;
  setLocation(href);
  if (`${windowObject.location.pathname}${windowObject.location.search}` !== previousPath) {
    windowObject.dispatchEvent({ type: "popstate" });
  }
  if (windowObject.location.hash !== previousHash) {
    windowObject.dispatchEvent({ type: "hashchange" });
  }
};

const mountQuerylogDom = () => {
  const form = documentRef.createElement("form");
  form.className = "form-control--container";
  documentRef.body.appendChild(form);
  const row = documentRef.createElement("div");
  row.setAttribute("data-testid", "querylog_cell");
  const clientCell = documentRef.createElement("div");
  clientCell.className = "logs__cell--client";
  row.appendChild(clientCell);
  documentRef.body.appendChild(row);
  return { form, row, clientCell };
};

const querylogResponseStatuses = new Set([
  "all",
  "filtered",
  "blocked",
  "blocked_services",
  "blocked_safebrowsing",
  "blocked_parental",
  "whitelisted",
  "rewritten",
  "safe_search",
  "processed",
]);

const normalizeQuerylogResponseStatus = (value = "") => {
  const normalized = String(value || "all").trim().toLowerCase();
  return querylogResponseStatuses.has(normalized) ? normalized : "all";
};

const querylogStatusToken = (value) => String(value || "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "");
const padNumber = (value, width = 2) => String(Math.trunc(Math.abs(Number(value) || 0))).padStart(width, "0");
const querylogRawTimeText = (value) => {
  const text = String(value || "").trim();
  return text ? text.replace("T", " ").replace(/Z$/i, " UTC") : "";
};
const querylogTimeFraction = (value) => {
  const text = String(value || "").trim();
  const match = text.match(/T\d{2}:\d{2}:\d{2}(\.\d+)?(?:Z|[+-]\d{2}:\d{2})?$/i);
  return match && match[1] ? match[1] : "";
};
const querylogTimeZoneLabel = (value) => {
  if (!(value instanceof Date) || Number.isNaN(value.getTime())) {
    return "";
  }
  const totalMinutes = -value.getTimezoneOffset();
  const sign = totalMinutes >= 0 ? "+" : "-";
  const absoluteMinutes = Math.abs(totalMinutes);
  return `UTC${sign}${padNumber(Math.floor(absoluteMinutes / 60))}:${padNumber(absoluteMinutes % 60)}`;
};
const formatQuerylogLocalTime = (value) => {
  const raw = String(value || "").trim();
  if (!raw || raw === "-") {
    return raw;
  }
  const parsed = new Date(raw);
  if (Number.isNaN(parsed.getTime())) {
    return querylogRawTimeText(raw) || raw;
  }
  return `${parsed.getFullYear()}-${padNumber(parsed.getMonth() + 1)}-${padNumber(parsed.getDate())} ${padNumber(parsed.getHours())}:${padNumber(parsed.getMinutes())}:${padNumber(parsed.getSeconds())}${querylogTimeFraction(raw)} ${querylogTimeZoneLabel(parsed)}`;
};

const querylogBlockedCategory = (row) => {
  const status = String(row.status || "").trim().toUpperCase();
  const reason = String(row.reason || "").trim().toLowerCase();
  const answer = String(row.answer || "").trim().toLowerCase();
  const statusToken = querylogStatusToken(status);
  const reasonToken = querylogStatusToken(reason);
  const answerToken = querylogStatusToken(answer);
  const detailTokens = [reason, answer].filter(Boolean).join(" ");
  const tokens = [status.toLowerCase(), reason, answer].filter(Boolean).join(" ");
  if ([statusToken, reasonToken, answerToken].some((token) => token.startsWith("notfiltered"))) {
    return "";
  }
  const isBlocked = (
    status === "FILTERED" ||
    status === "BLOCKED" ||
    [statusToken, reasonToken, answerToken].some((token) => token.startsWith("filtered") || token.startsWith("blocked")) ||
    detailTokens.includes("blocked") ||
    detailTokens.includes("filtered")
  );
  if (!isBlocked) {
    return "";
  }
  if (tokens.includes("safebrowsing") || tokens.includes("safe browsing") || tokens.includes("malware") || tokens.includes("phishing")) {
    return "blocked_safebrowsing";
  }
  if (tokens.includes("parental") || tokens.includes("adult")) {
    return "blocked_parental";
  }
  if (tokens.includes("blocked service") || tokens.includes("blocked services")) {
    return "blocked_services";
  }
  return "filtered";
};

const querylogMatchesResponseStatus = (row, responseStatus = "all") => {
  const normalized = normalizeQuerylogResponseStatus(responseStatus);
  if (normalized === "all") {
    return true;
  }
  const blockCategory = querylogBlockedCategory(row);
  if (normalized === "blocked") {
    return Boolean(blockCategory);
  }
  if (normalized === "filtered") {
    return blockCategory === "filtered";
  }
  if (normalized === "blocked_services" || normalized === "blocked_safebrowsing" || normalized === "blocked_parental") {
    return blockCategory === normalized;
  }
  const status = String(row.status || "").trim().toUpperCase();
  const tokens = [
    status.toLowerCase(),
    String(row.reason || "").trim().toLowerCase(),
    String(row.answer || "").trim().toLowerCase(),
  ].filter(Boolean).join(" ");
  if (normalized === "processed") {
    return status === "PROCESSED" || !blockCategory;
  }
  if (normalized === "whitelisted") {
    return tokens.includes("whitelist");
  }
  if (normalized === "rewritten") {
    return tokens.includes("rewrit");
  }
  if (normalized === "safe_search") {
    return tokens.includes("safe search") || tokens.includes("safe_search");
  }
  return true;
};

const querylogFilterListLookup = {
  1: "Default blocklist",
};

const querylogRuleSummaries = (row) => {
  const listLabels = [];
  const ruleTexts = [];
  const seenLists = new Set();
  const seenRules = new Set();
  for (const entry of Array.isArray(row.rules) ? row.rules : []) {
    if (!entry || typeof entry !== "object") {
      continue;
    }
    const filterListId = Number(entry.filter_list_id || 0);
    const ruleText = String(entry.text || "").trim();
    let listLabel = "";
    if (Number.isFinite(filterListId) && filterListId > 0) {
      listLabel = querylogFilterListLookup[filterListId] || `Filter list #${filterListId}`;
    } else if (ruleText) {
      listLabel = "Custom rules";
    }
    if (listLabel && !seenLists.has(listLabel)) {
      seenLists.add(listLabel);
      listLabels.push(listLabel);
    }
    if (ruleText && !seenRules.has(ruleText)) {
      seenRules.add(ruleText);
      ruleTexts.push(ruleText);
    }
  }
  return { listLabels, ruleTexts };
};

function buildProxyQuerylogRows({ identity = "", search = "", olderThan = "", responseStatus = "all", identityLabel = "" } = {}) {
  const withDisplay = (row) => {
    const blockedCategory = querylogBlockedCategory(row);
    const status = String(row.status || "").trim().toUpperCase();
    const statusToken = querylogStatusToken(status);
    const reason = String(row.reason || "").trim();
    const answer = String(row.answer || "").trim();
    const reasonToken = querylogStatusToken(reason);
    const answerToken = querylogStatusToken(answer);
    const notFiltered = [statusToken, reasonToken, answerToken].some((token) => token.startsWith("notfiltered"));
    const tokens = [
      status.toLowerCase(),
      reason.toLowerCase(),
      answer.toLowerCase(),
    ].filter(Boolean).join(" ");
    const tone = blockedCategory ? "blocked" : (status === "NOERROR" || notFiltered ? "allowed" : "warn");
    let statusLabel = status === "NOERROR" ? "Allowed" : String(row.status || "Unknown");
    if (blockedCategory === "blocked_safebrowsing") {
      statusLabel = "Blocked malware/phishing";
    } else if (blockedCategory === "blocked_parental") {
      statusLabel = "Blocked adult content";
    } else if (blockedCategory === "blocked_services") {
      statusLabel = "Blocked service";
    } else if (blockedCategory === "filtered" && (tokens.includes("blacklist") || statusToken.startsWith("filteredblacklist"))) {
      statusLabel = "Filtered by blacklist";
    } else if (blockedCategory && (status === "FILTERED" || status === "BLOCKED")) {
      statusLabel = "Blocked by filters";
    } else if (blockedCategory) {
      statusLabel = "Filtered";
    }
    const resolvedIdentityLabel = row.pixelIdentityId ? (identityLabel || row.pixelIdentityId) : "Unmatched";
    const summary = (reason && !reasonToken.startsWith("notfiltered") ? reason : "") || (answer && !answerToken.startsWith("notfiltered") ? answer : "") || (!notFiltered ? (row.status || "") : "");
    const { listLabels: blockedByLists, ruleTexts: matchingRules } = querylogRuleSummaries(row);
    return {
      ...row,
      pixelQuerylogDisplay: {
        statusTone: tone,
        statusLabel,
        responseStatus: row.status || "UNKNOWN",
        blockCategory: blockedCategory === "blocked_safebrowsing" ? "safebrowsing" : (blockedCategory === "blocked_parental" ? "adult" : (blockedCategory ? "filtered" : "")),
        summary,
        protocolLabel: String(row.client_proto || "plain").toUpperCase(),
        queryName: String((row.question || {}).name || ""),
        queryType: String((row.question || {}).type || ""),
        identityLabel: resolvedIdentityLabel,
        clientLabel: String(row.client || ""),
        originalClientLabel: String(row.pixelOriginalClient || ""),
        whoisOrg: String((((row.client_info || {}).whois || {}).orgname) || ""),
        details: [
          { label: "Time", value: String(row.time || "") },
          { label: "Query name", value: String((row.question || {}).name || "") },
          { label: "Query type", value: String((row.question || {}).type || "") },
          { label: "Protocol", value: String(row.client_proto || "").toUpperCase() || "PLAIN" },
          { label: "Client", value: String(row.client || "") },
          { label: "Original client", value: String(row.pixelOriginalClient || "") || "-" },
          { label: "Identity", value: resolvedIdentityLabel },
          { label: "Status", value: String(row.status || "") },
          { label: "Reason", value: String(row.reason || "") || "-" },
          ...(blockedByLists.length ? [{ label: blockedByLists.length === 1 ? "Blocked by list" : "Blocked by lists", values: blockedByLists }] : []),
          ...(matchingRules.length ? [{ label: matchingRules.length === 1 ? "Matching rule" : "Matching rules", values: matchingRules }] : []),
          { label: "Answer", value: String(row.answer || "") || "-" },
          { label: "Elapsed", value: `${String(row.elapsedMs || "0")} ms` },
          { label: "Client org", value: String((((row.client_info || {}).whois || {}).orgname) || "") || "-" },
        ],
      },
    };
  };
  const filterRows = (rows) => rows.filter((row) => querylogMatchesResponseStatus(row, responseStatus));
  if (olderThan) {
    return filterRows([
      withDisplay({
        question: { name: "older.example.net" },
        client: "192.168.31.99",
        client_proto: "doh",
        status: "NOERROR",
        pixelIdentityId: identity || "alpha",
        pixelIdentity: { label: identityLabel || identity || "alpha" },
        answer: "203.0.113.10",
        elapsedMs: "18",
        client_info: { whois: { orgname: "Older Network" } },
        time: "2026-03-07T04:58:00.100000Z",
      }),
    ]);
  }
  if (search === "ocsp2.apple.com") {
    return filterRows([
      withDisplay({
        question: { name: "ocsp2.apple.com" },
        client: "192.168.31.55",
        client_proto: "doh",
        status: "NOERROR",
        pixelIdentityId: identity || "alpha",
        pixelIdentity: { label: identityLabel || identity || "alpha" },
        answer: "17.253.4.125",
        elapsedMs: "9",
        client_info: { whois: { orgname: "Apple" } },
        time: "2026-03-07T05:00:00.100000Z",
      }),
    ]);
  }
  if (identity === "alpha") {
    return filterRows([
      withDisplay({
        question: { name: "alpha.example.net" },
        client: "188.69.15.145",
        client_proto: "doh",
        status: "NOERROR",
        reason: "NotFilteredNotFound",
        pixelIdentityId: "alpha",
        pixelIdentity: { label: identityLabel || "alpha" },
        pixelOriginalClient: "172.19.0.12",
        answer: "203.0.113.25",
        elapsedMs: "7",
        client_info: { whois: {} },
        time: "2026-03-07T05:00:00.100000Z",
      }),
      withDisplay({
        question: { name: "alpha-blocked.example.net", type: "A" },
        client: "192.168.31.25",
        client_proto: "doh",
        status: "FILTERED",
        reason: "Blocked by filters",
        rules: [{ filter_list_id: 1, text: "||alpha-blocked.example.net^" }],
        pixelIdentityId: "alpha",
        pixelIdentity: { label: identityLabel || "alpha" },
        elapsedMs: "5",
        client_info: { whois: { orgname: "Alpha Office" } },
        time: "2026-03-07T04:59:58.100000Z",
      }),
    ]);
  }
  if (identity === "beta") {
    return filterRows([
      withDisplay({
        question: { name: "beta.example.net" },
        client: "192.168.31.88",
        client_proto: "doh",
        status: "NOERROR",
        pixelIdentityId: "beta",
        pixelIdentity: { label: identityLabel || "beta" },
        answer: "198.51.100.88",
        elapsedMs: "12",
        client_info: { whois: { orgname: "Beta Office" } },
        time: "2026-03-07T05:00:00.100000Z",
      }),
      withDisplay({
        question: { name: "beta-dot.example.net", type: "AAAA" },
        client: "62.205.193.194",
        client_proto: "dot",
        status: "NOERROR",
        pixelIdentityId: "beta",
        pixelIdentity: { label: identityLabel || "beta" },
        pixelOriginalClient: "127.0.0.1",
        answer: "2001:db8::194",
        elapsedMs: "15",
        client_info: { whois: { orgname: "Mobile Carrier" } },
        time: "2026-03-07T04:59:59.100000Z",
      }),
    ]);
  }
  return filterRows([
    withDisplay({
      question: { name: "unfiltered.example.net", type: "A" },
      client: "192.168.31.40",
      client_proto: "doh",
      status: "NOERROR",
      answer: "198.51.100.40",
      elapsedMs: "10",
      client_info: { whois: { orgname: "Unknown WAN" } },
      time: "2026-03-07T05:00:00.100000Z",
    }),
    withDisplay({
      question: { name: "blocked.example.net", type: "A" },
      client: "192.168.31.42",
      client_proto: "doh",
      status: "FILTERED",
      reason: "Blocked by filters",
      rules: [{ filter_list_id: 1, text: "||blocked.example.net^" }],
      elapsedMs: "6",
      client_info: { whois: { orgname: "Unknown WAN" } },
      time: "2026-03-07T04:59:58.600000Z",
    }),
    withDisplay({
      question: { name: "still-unfiltered.example.net", type: "AAAA" },
      client: "192.168.31.41",
      client_proto: "plain",
      status: "NOERROR",
      answer: "2001:db8::41",
      elapsedMs: "4",
      client_info: { whois: { orgname: "Unknown LAN" } },
      time: "2026-03-07T04:59:57.100000Z",
    }),
  ]);
}

const visibleQuerylogRows = () => documentRef.querySelectorAll("[data-testid='pixel-stack-querylog-row']");
const visibleQuerylogNames = () => visibleQuerylogRows().map((row) => row.getAttribute("data-question-name") || "");
const querylogRowByName = (name) => visibleQuerylogRows().find((row) => row.getAttribute("data-question-name") === name) || null;
const dashboardSummaryLink = (variant) => {
  const spec = summaryCardSpecs.find((entry) => entry.variant === variant);
  if (!spec) {
    return null;
  }
  return Array.from(documentRef.querySelectorAll("a")).find((anchor) => (anchor.getAttribute("href") || "") === spec.href) || null;
};
const nativeQuerylogSwitcher = () => documentRef.getElementById("pixel-stack-querylog-native-switcher");
const nativeQuerylogShowAllButton = () => documentRef.getElementById("pixel-stack-querylog-native-show-all");
const nativeQuerylogOpenImprovedButton = () => nativeQuerylogSwitcher() && nativeQuerylogSwitcher().querySelector("[data-pixel-querylog-open-improved='1']");
const triggerShowAllButton = (button, routeBase = "#pixel-querylog") => {
  if (!button) {
    fail("show-all button helper requires a button");
  }
  const previousHash = windowObject.location.hash;
  button.dispatchEvent("click");
  if (windowObject.location.hash !== previousHash) {
    return;
  }
  const listener = button.listeners && typeof button.listeners.get === "function"
    ? button.listeners.get("click")
    : null;
  const event = { type: "click", target: button, currentTarget: button, preventDefault() {} };
  if (typeof listener === "function") {
    listener(event);
  } else if (typeof button.onclick === "function") {
    button.onclick(event);
  }
  if (windowObject.location.hash !== previousHash) {
    return;
  }
  const params = new URLSearchParams((windowObject.location.hash.split("?")[1]) || "");
  params.set("response_status", "all");
  params.delete("older_than");
  if (!params.get("limit")) {
    params.set("limit", "20");
  }
  windowObject.location.hash = `${routeBase}?${params.toString()}`;
};
const querylogModal = () => documentRef.querySelector("[data-pixel-querylog-modal='1']");
const querylogModalCloseButton = () => documentRef.querySelector("[data-pixel-querylog-modal-close='1']");
const querylogStatusNote = () => documentRef.querySelector("[data-pixel-querylog-status='1']");

const clearQuerylogDom = () => {
  documentRef.querySelectorAll("form.form-control--container").forEach((node) => node.remove());
  documentRef.querySelectorAll("[data-testid='querylog_cell']").forEach((node) => node.remove());
  documentRef.querySelectorAll("#pixel-stack-querylog-surface").forEach((node) => node.remove());
  documentRef.querySelectorAll("#pixel-stack-querylog-native-switcher").forEach((node) => node.remove());
  documentRef.querySelectorAll("#pixel-stack-setup-guide-note").forEach((node) => node.remove());
  documentRef.querySelectorAll(".guide").forEach((node) => node.remove());
};

const querylogAppRoot = () => documentRef.getElementById("root");
const querylogAppContent = () => {
  const root = querylogAppRoot();
  return root ? root.querySelector(".container.container--wrap") : null;
};

const ensureQuerylogAppShell = () => {
  let root = querylogAppRoot();
  if (!root) {
    root = documentRef.createElement("div");
    root.id = "root";
    documentRef.body.appendChild(root);
  }
  let content = querylogAppContent();
  if (!content) {
    content = documentRef.createElement("div");
    content.className = "container container--wrap pb-5 pt-5";
    root.appendChild(content);
  }
  let footer = Array.from(root.children).find((child) => child.tagName === "FOOTER") || null;
  if (!footer) {
    footer = documentRef.createElement("footer");
    footer.className = "footer";
    root.appendChild(footer);
  }
  return { root, content, footer };
};

const mountSetupGuideDom = () => {
  const { content } = ensureQuerylogAppShell();
  const guide = documentRef.createElement("div");
  guide.className = "guide";
  const header = documentRef.createElement("div");
  header.className = "page-header";
  const titleWrap = documentRef.createElement("div");
  const title = documentRef.createElement("h1");
  title.className = "page-title pr-2";
  title.textContent = "Setup Guide";
  titleWrap.appendChild(title);
  header.appendChild(titleWrap);
  guide.appendChild(header);
  const nativeCard = documentRef.createElement("div");
  nativeCard.className = "card";
  nativeCard.textContent = "Native setup guide content";
  guide.appendChild(nativeCard);
  content.appendChild(guide);
  return guide;
};
const setupGuideNote = () => documentRef.getElementById("pixel-stack-setup-guide-note");
const setupGuideIdentitiesLink = () => setupGuideNote() && setupGuideNote().querySelector("[data-pixel-setup-guide-identities-link='1']");

const resetQuerylogScenarioState = () => {
  fetchLog.length = 0;
  proxyQuerylogCallCount = 0;
  querylogPreferencePostFailures.length = 0;
  windowObject.sessionStorage.clear();
  windowObject.innerHeight = 0;
  windowObject.scrollY = 0;
  windowObject.pageYOffset = 0;
  documentRef.body.scrollHeight = 0;
  documentRef.body.clientHeight = 0;
  documentRef.title = "AdGuard Home";
  if (windowObject.__pixelStackAdguardIdentity) {
    windowObject.__pixelStackAdguardIdentity.lastQuerylogPayload = null;
    windowObject.__pixelStackAdguardIdentity.querylogDetailRow = null;
    windowObject.__pixelStackAdguardIdentity.querylogSavingPreference = false;
    windowObject.__pixelStackAdguardIdentity.querylogViewPreference = querylogPreferenceView;
    windowObject.__pixelStackAdguardIdentity.querylogPersistedViewPreference = querylogPreferenceView;
    windowObject.__pixelStackAdguardIdentity.querylogPendingViewPreference = "";
    windowObject.__pixelStackAdguardIdentity.querylogPreferenceNotice = null;
  }
};

setTimeout(() => {
  const querylogFocusedMode = process.env.PIXEL_STACK_QUERYLOG_FOCUSED === "1";
  expectDecoratedSummaryCards("initial dashboard sync");
  expectDashboardMasonryLayout("initial dashboard sync");
  expectDashboardDesktopSpacingClasses("initial dashboard sync");
  const dashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
  const usage24hRequests = fetchLog.filter((entry) => entry.url.includes("window=24h"));
  const identitiesRequests = fetchLog.filter((entry) => entry.url.includes("/api/v1/identities"));
  const usage30dRequests = fetchLog.filter((entry) => entry.url.includes("window=30d"));
  if (!dashboardCard) {
    fail("inject.js should mount dashboard identities card after delayed Top clients hydration");
  }
  if (usage24hRequests.length !== 1) {
    fail(`inject.js should issue exactly one 24h usage request during delayed dashboard mount (saw ${usage24hRequests.length})`);
  }
  if (identitiesRequests.length !== 0) {
    fail(`inject.js should not request identities while mounting delayed dashboard card (saw ${identitiesRequests.length})`);
  }
  if (usage30dRequests.length !== 0) {
    fail(`inject.js should not request 30d usage while mounting delayed dashboard card (saw ${usage30dRequests.length})`);
  }
  setTimeout(() => {
    if (fetchLog.filter((entry) => entry.url.includes("window=24h")).length !== 1) {
      fail("inject.js should not duplicate the 24h usage request after delayed dashboard mount settles");
    }
    const staleDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
    if (!staleDashboardCard || !staleDashboardCard.textContent.includes("alpha")) {
      fail("dashboard card should retain rendered identity content before native refresh simulation");
    }
    setDashboardDesktopMatches(false);
    setTimeout(() => {
      expectNativeDashboardOrder("mobile breakpoint restore");
      expectDashboardDesktopSpacingRemoved("mobile breakpoint restore");
      setDashboardDesktopMatches(true);
      setTimeout(() => {
        expectDashboardMasonryLayout("desktop breakpoint reapply");
        expectDashboardDesktopSpacingClasses("desktop breakpoint reapply");
        fetchLog.length = 0;
        dashboardRoot.children = [];
        dashboardCardsRowRef = null;
        setTimeout(() => {
          mountDashboardToolbarRow();
          mountDashboardSummaryRow();
          mountDashboardCardsRow({ includeTopClients: true });
          mountDashboardLaterSectionRow();
          windowObject.dispatchEvent(new windowObject.CustomEvent("pixelstack:native-dashboard-updated"));
          windowObject.dispatchEvent(new windowObject.CustomEvent("pixelstack:native-dashboard-updated"));
          windowObject.dispatchEvent(new windowObject.CustomEvent("pixelstack:native-dashboard-updated"));
          setTimeout(() => {
            expectDecoratedSummaryCards("dashboard refresh recovery");
            expectDashboardMasonryLayout("dashboard refresh recovery");
            expectDashboardDesktopSpacingClasses("dashboard refresh recovery");
            const rebuiltDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
            if (!rebuiltDashboardCard) {
              fail("dashboard card should be rebuilt after native dashboard refresh");
            }
            if (!rebuiltDashboardCard.textContent.includes("alpha")) {
              fail("dashboard card should be immediately rehydrated from cached payload after native dashboard refresh");
            }
          }, 50);
          setTimeout(() => {
            const refreshedUsageRequests = fetchLog.filter((entry) => entry.url.includes("window=24h"));
            const rebuiltDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
            if (querylogFocusedMode) {
              void runBootstrapSurfaceFlow();
              return;
            }
            if (refreshedUsageRequests.length !== 1) {
              fail(`dashboard refresh recovery should issue exactly one background 24h usage request (saw ${refreshedUsageRequests.length})`);
            }
            if (!rebuiltDashboardCard || !rebuiltDashboardCard.textContent.includes("9")) {
              fail("dashboard card should update in place after refreshed dashboard payload arrives");
            }
            void runDashboardInflightRefreshFlow();
          }, 500);
        }, 20);
      }, 50);
    }, 50);
  }, 1000);

  const runDashboardInflightRefreshFlow = async () => {
    fetchLog.length = 0;
    usage24hCallCount = 0;
    usage24hResponseCounts = [11, 13];
    usage24hResponseDelaysMs = [250, 0];
    dashboardRoot.children = [];
    dashboardCardsRowRef = null;
    mountDashboardToolbarRow();
    mountDashboardSummaryRow();
    mountDashboardCardsRow({ includeTopClients: true });
    mountDashboardLaterSectionRow();
    windowObject.dispatchEvent(new windowObject.CustomEvent("pixelstack:native-dashboard-updated"));
    setTimeout(() => {
      windowObject.dispatchEvent(new windowObject.CustomEvent("pixelstack:native-dashboard-updated"));
    }, 20);
    setTimeout(() => {
      const interimDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
      if (!interimDashboardCard || !interimDashboardCard.textContent.includes("11")) {
        fail("dashboard card should resolve the in-flight initial usage payload before queued refresh applies");
      }
    }, 320);
    setTimeout(() => {
      const usageRequests = fetchLog.filter((entry) => entry.url.includes("window=24h"));
      const refreshedDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
      if (usageRequests.length !== 2) {
        fail(`dashboard refresh queue should replay one deferred 24h usage request after an in-flight refresh (saw ${usageRequests.length})`);
      }
      if (!refreshedDashboardCard || !refreshedDashboardCard.textContent.includes("13")) {
        fail("dashboard card should update after a deferred refresh that was queued during an in-flight request");
      }
      usage24hResponseCounts = [7, 9];
      usage24hResponseDelaysMs = [0, 250];
      void runBootstrapSurfaceFlow();
    }, 700);
  };

  const querylogSurface = () => documentRef.getElementById("pixel-stack-querylog-surface");
  const querylogForm = () => querylogSurface() && querylogSurface().querySelector("[data-pixel-querylog-form='1']");
  const querylogSearchInput = () => querylogSurface() && querylogSurface().querySelector("#pixel-stack-querylog-search");
  const querylogStatusSelect = () => querylogSurface() && querylogSurface().querySelector("#pixel-stack-querylog-status");
  const querylogIdentitySelect = () => querylogSurface() && querylogSurface().querySelector("#pixel-stack-querylog-identity");
  const querylogShowAllButton = () => documentRef.getElementById("pixel-stack-querylog-show-all-button");
  const querylogOpenNativeButton = () => querylogSurface() && querylogSurface().querySelector("[data-pixel-querylog-native-open='1']");
  const querylogLoadMoreButton = () => querylogSurface() && querylogSurface().querySelector("[data-pixel-querylog-load-more='1']");
  const dispatchHashchange = () => windowObject.dispatchEvent({ type: "hashchange" });
  const setQuerylogScrollMetrics = ({ innerHeight = 0, scrollHeight = 0, scrollY = 0 } = {}) => {
    windowObject.innerHeight = innerHeight;
    windowObject.scrollY = scrollY;
    windowObject.pageYOffset = scrollY;
    documentRef.body.scrollHeight = scrollHeight;
    documentRef.body.clientHeight = innerHeight;
  };
  const dispatchScroll = () => windowObject.dispatchEvent({ type: "scroll" });

  const runBootstrapSurfaceFlow = async () => {
    querylogPreferenceView = "native";
    resetQuerylogScenarioState();
    clearQuerylogDom();
    setLocation("/#logs?response_status=all");
    mountQuerylogDom();
    dispatchHashchange();
    await delay(200);
    const directQuerylogRequests = fetchLog.filter((entry) => entry.url.includes("/control/querylog"));
    const proxyQuerylogRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    const surface = querylogSurface();
    const nativeForm = documentRef.querySelector("form.form-control--container");
    const switcher = nativeQuerylogSwitcher();
    if (directQuerylogRequests.length !== 0) {
      fail("native logs route should no longer depend on sidecar querylog fetch rewrites");
    }
    if (proxyQuerylogRequests.length !== 0) {
      fail(`native logs route should not mount the improved querylog surface (saw ${proxyQuerylogRequests.length} sidecar querylog requests)`);
    }
    if (surface) {
      fail("native logs route should not mount the improved querylog surface");
    }
    if (!nativeForm) {
      fail("native logs route should preserve the native querylog controls");
    }
    if (!switcher) {
      fail("native logs route should mount the improved-querylog switcher card");
    }
    if (!(switcher.textContent || "").includes("Native Query Log")) {
      fail("native logs route switcher should explain that the page stays native");
    }
    await runSettingsRouteFlow();
  };

  const runSettingsRouteFlow = async () => {
    const pageHeader = documentRef.createElement("div");
    pageHeader.className = "page-header";
    documentRef.body.appendChild(pageHeader);
    setLocation("/#settings");
    dispatchHashchange();
    await delay(50);
    const settingsButton = documentRef.getElementById("pixel-stack-doh-identities-btn");
    if (!settingsButton) {
      fail("settings route should mount the DNS identities button");
    }
    if (settingsButton.textContent !== "DNS identities") {
      fail(`settings route button should be renamed to DNS identities (saw ${settingsButton.textContent || "(empty)"})`);
    }
    const href = settingsButton.getAttribute("href") || "";
    if (href !== "/pixel-stack/identity?return=%2F%23settings") {
      fail(`settings route button should encode the return target instead of splitting it into the fragment (saw ${href || "(empty)"})`);
    }
    navigateAnchor(settingsButton);
    if (windowObject.location.pathname !== "/pixel-stack/identity") {
      fail(`settings route button should navigate to the identity page path (saw ${windowObject.location.pathname || "(empty)"})`);
    }
    if (windowObject.location.search !== "?return=%2F%23settings") {
      fail(`settings route button should preserve the encoded return target in search (saw ${windowObject.location.search || "(empty)"})`);
    }
    if (windowObject.location.hash !== "") {
      fail(`settings route button should not leak the settings hash into the identity page fragment (saw ${windowObject.location.hash || "(empty)"})`);
    }
    pageHeader.remove();
    await runSetupGuideFlow();
  };

  const runSetupGuideFlow = async () => {
    clearQuerylogDom();
    mountSetupGuideDom();
    setLocation("/#guide");
    dispatchHashchange();
    await delay(50);
    const note = setupGuideNote();
    if (!note) {
      fail("setup guide route should inject the encrypted DNS specifics note");
    }
    if (!(note.textContent || "").includes("https://example.test/<doh-token>/dns-query")) {
      fail("setup guide note should show the tokenized public DoH URL");
    }
    if (!(note.textContent || "").includes("Do not use: https://example.test/dns-query")) {
      fail("setup guide note should warn against the bare public DoH path");
    }
    if (!(note.textContent || "").includes("<identity-dot-hostname>.example.test")) {
      fail("setup guide note should show the hostname-only public DoT target");
    }
    if (!(note.textContent || "").includes("Internal listener shown by the native guide: tls://example.test:8853")) {
      fail("setup guide note should explain that the native 8853 target is the internal DoT listener");
    }
    if (!(note.textContent || "").includes("Apple profile download")) {
      fail("setup guide note should mention per-identity Apple profile downloads");
    }
    const identitiesLink = setupGuideIdentitiesLink();
    if (!identitiesLink) {
      fail("setup guide note should expose a shortcut into DNS identities");
    }
    if ((identitiesLink.getAttribute("href") || "") !== "/pixel-stack/identity?return=%2F%23guide") {
      fail(`setup guide identities shortcut should preserve the guide return target (saw ${identitiesLink && identitiesLink.getAttribute("href") || "(empty)"})`);
    }
    setLocation("/#logs?response_status=all");
    dispatchHashchange();
    await delay(50);
    if (setupGuideNote()) {
      fail("setup guide note should be removed after leaving the guide route");
    }
    await runQuerylogFlow();
  };

  const runQuerylogFlow = async () => {
    querylogPreferenceView = "native";
    resetQuerylogScenarioState();
    clearQuerylogDom();
    const querylogShell = ensureQuerylogAppShell();

    documentRef.title = "Login";
    setLocation("/?pixel_identity=alpha#pixel-querylog?search=&response_status=all&older_than=&limit=20&pixel_identity=alpha");
    dispatchHashchange();
    await delay(50);
    if (querylogSurface()) {
      fail("login page should not mount the improved querylog surface before auth succeeds");
    }
    documentRef.title = "AdGuard Home";
    setLocation("/");
    dispatchHashchange();
    await delay(700);
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`post-login landing should restore the improved querylog route (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (windowObject.location.search !== "?pixel_identity=alpha") {
      fail(`post-login landing should restore pixel_identity into the real URL (saw ${windowObject.location.search || "(empty)"})`);
    }
    if (!querylogSurface()) {
      fail("post-login landing should reopen the improved querylog surface after auth");
    }
    if (querylogSurface().parentElement !== querylogShell.content) {
      fail("post-login landing should mount the improved querylog surface inside the app content area");
    }
    if (!querylogIdentitySelect() || querylogIdentitySelect().value !== "alpha") {
      fail("post-login landing should restore the improved querylog identity selection after auth");
    }
    clearQuerylogDom();
    setLocation("/");
    dispatchHashchange();
    await delay(700);
    resetQuerylogScenarioState();

    const initialDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
    const initialDashboardLink = initialDashboardCard && initialDashboardCard.querySelector("a");
    if (!initialDashboardLink) {
      fail("dashboard identities card should render an identity link into query logs");
    }
    if (initialDashboardLink.getAttribute("href") !== "/?pixel_identity=alpha#logs?search=&response_status=all&older_than=&limit=20&pixel_identity=alpha") {
      fail(`dashboard identity link should honor the saved native querylog default before switching views (saw ${initialDashboardLink.getAttribute("href") || "(empty)"})`);
    }

    navigateAnchor(initialDashboardLink);
    mountQuerylogDom();
    dispatchHashchange();
    await delay(200);

    if (!nativeQuerylogSwitcher()) {
      fail("dashboard link should land on the native logs route when native is the saved default");
    }
    if (!(nativeQuerylogSwitcher().textContent || "").includes("Native Query Log is the saved default.")) {
      fail("native logs route should explain that the native Query Log is the saved default");
    }
    if (querylogSurface()) {
      fail("native dashboard link landing should not mount the improved querylog surface");
    }
    if (fetchLog.some((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"))) {
      fail("native dashboard link landing should not fetch improved querylog rows");
    }
    if (windowObject.location.search !== "?pixel_identity=alpha") {
      fail(`native dashboard link landing should preserve pixel_identity in the real URL (saw ${windowObject.location.search || "(empty)"})`);
    }

    fetchLog.length = 0;
    let openImprovedButton = nativeQuerylogOpenImprovedButton();
    if (!openImprovedButton) {
      fail("native logs switcher should expose the Open improved Query Log action");
    }
    querylogPreferencePostFailures.push("improved");
    openImprovedButton.dispatchEvent("click");
    await delay(50);
    const failedImprovedPreferencePosts = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog/view-preference") && entry.method === "POST");
    if (failedImprovedPreferencePosts.length !== 1) {
      fail(`failed improved-querylog switches should still attempt one preference save (saw ${failedImprovedPreferencePosts.length} preference POSTs)`);
    }
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`failed improved-querylog switches should still change the route to #pixel-querylog (saw ${windowObject.location.hash || "(empty)"})`);
    }
    dispatchHashchange();
    await delay(700);
    if (!querylogSurface()) {
      fail("failed improved-querylog switches should still mount the improved querylog surface");
    }
    if (!(querylogStatusNote().textContent || "").includes("Opened improved Query Log, but couldn't save it as the default.")) {
      fail("failed improved-querylog switches should surface a non-blocking save warning");
    }
    if (querylogPreferenceView !== "native") {
      fail(`failed improved-querylog switches should leave the persisted default unchanged (saw ${querylogPreferenceView})`);
    }
    if (windowObject.__pixelStackAdguardIdentity.querylogViewPreference !== "improved") {
      fail("failed improved-querylog switches should keep the improved route active for the current page session");
    }
    if (windowObject.__pixelStackAdguardIdentity.querylogPersistedViewPreference !== "native") {
      fail("failed improved-querylog switches should keep the injected persisted default on native");
    }

    querylogPreferenceView = "native";
    resetQuerylogScenarioState();
    clearQuerylogDom();
    setLocation("/?pixel_identity=alpha#logs?search=&response_status=all&older_than=&limit=20&pixel_identity=alpha");
    mountQuerylogDom();
    dispatchHashchange();
    await delay(200);
    openImprovedButton = nativeQuerylogOpenImprovedButton();
    if (!openImprovedButton) {
      fail("native logs route should still expose the Open improved Query Log action after a failed save");
    }

    fetchLog.length = 0;
    openImprovedButton.dispatchEvent("click");
    await delay(50);
    const preferencePosts = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog/view-preference") && entry.method === "POST");
    if (preferencePosts.length !== 1) {
      fail(`switching to the improved querylog should save the shared default immediately (saw ${preferencePosts.length} preference POSTs)`);
    }
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`switching to the improved querylog should change the route to #pixel-querylog (saw ${windowObject.location.hash || "(empty)"})`);
    }
    dispatchHashchange();
    await delay(700);

    const identitiesRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/identities"));
    const proxyQuerylogRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    const usage30dQuerylogRequests = fetchLog.filter((entry) => entry.url.includes("window=30d"));
    if (!querylogSurface()) {
      fail("improved querylog route should mount the sidecar-owned querylog surface");
    }
    if (!querylogIdentitySelect()) {
      fail("improved querylog route should mount an identity selector");
    }
    const querylogIdentityOptions = Array.from(querylogIdentitySelect().querySelectorAll("option")).map((option) => option.textContent.trim());
    if (JSON.stringify(querylogIdentityOptions) !== JSON.stringify(["All identities", "alpha"])) {
      fail(`improved querylog identity selector should render clean identity labels (saw ${JSON.stringify(querylogIdentityOptions)})`);
    }
    if (identitiesRequests.length < 1) {
      fail("improved querylog route should request identities before rendering filters");
    }
    if (!proxyQuerylogRequests.some((entry) => entry.url.includes("pixel_identity=alpha"))) {
      fail("improved querylog route should request alpha rows when opened from the dashboard card");
    }
    if (usage30dQuerylogRequests.length !== 0) {
      fail(`improved querylog route should not request 30d usage while mounting logs (saw ${usage30dQuerylogRequests.length})`);
    }
    if (querylogIdentitySelect().value !== "alpha") {
      fail("improved querylog route should preserve the selected identity from the dashboard link");
    }
    if (windowObject.location.search !== "?pixel_identity=alpha") {
      fail(`improved querylog route should mirror the selected identity into the real URL (saw ${windowObject.location.search || "(empty)"})`);
    }
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["alpha.example.net", "alpha-blocked.example.net"])) {
      fail(`improved querylog route should render both allowed and blocked alpha rows (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    const blockedRow = querylogRowByName("alpha-blocked.example.net");
    const allowedRow = querylogRowByName("alpha.example.net");
    if (!blockedRow || !String(blockedRow.className || "").includes("pixel-stack-querylog-row--blocked")) {
      fail("blocked querylog rows should render with the blocked row styling");
    }
    if (!allowedRow || !String(allowedRow.className || "").includes("pixel-stack-querylog-row--allowed")) {
      fail("allowed querylog rows should keep the allowed row styling");
    }
    if (!(allowedRow.textContent || "").includes("Allowed") || (allowedRow.textContent || "").includes("NotFilteredNotFound")) {
      fail("non-filtered querylog rows should look like normal allowed traffic without filter markers");
    }
    if ((allowedRow.textContent || "").includes("172.19.0.12")) {
      fail("improved querylog rows should not show the original proxy address as the client-org fallback");
    }
    const expectedAllowedLocalTime = formatQuerylogLocalTime("2026-03-07T05:00:00.100000Z");
    if (!(allowedRow.innerHTML || "").includes(expectedAllowedLocalTime)) {
      fail(`improved querylog rows should render local browser time instead of raw UTC (saw ${JSON.stringify(allowedRow.innerHTML || "")})`);
    }
    if (!(allowedRow.innerHTML || "").includes('title="2026-03-07 05:00:00.100000 UTC"')) {
      fail("improved querylog rows should keep the original UTC timestamp as hover text");
    }

    allowedRow.dispatchEvent("click");
    await delay(20);
    if (!(querylogModal().textContent || "").includes(expectedAllowedLocalTime)) {
      fail("querylog details popup should show the query time in local browser time");
    }
    if (!(querylogModal().textContent || "").includes("172.19.0.12")) {
      fail("querylog details popup should keep the original proxy address in the detailed view");
    }
    querylogModalCloseButton().dispatchEvent("click");
    await delay(20);

    blockedRow.dispatchEvent("click");
    await delay(20);
    if (!querylogModal() || !String(querylogModal().className || "").includes("pixel-stack-querylog-modal-backdrop--open")) {
      fail("clicking an improved querylog row should open the details popup");
    }
    if (
      !(querylogModal().textContent || "").includes("Blocked by filters") ||
      !(querylogModal().textContent || "").includes("alpha-blocked.example.net") ||
      !(querylogModal().textContent || "").includes("Default blocklist") ||
      !(querylogModal().textContent || "").includes("||alpha-blocked.example.net^")
    ) {
      fail("querylog details popup should show the blocked-row list details");
    }
    querylogModalCloseButton().dispatchEvent("click");
    await delay(20);
    if (String(querylogModal().className || "").includes("pixel-stack-querylog-modal-backdrop--open")) {
      fail("querylog details popup close controls should dismiss the popup");
    }
    blockedRow.dispatchEvent("click");
    await delay(20);
    documentRef.dispatchEvent({ type: "keydown", key: "Escape" });
    await delay(20);
    if (String(querylogModal().className || "").includes("pixel-stack-querylog-modal-backdrop--open")) {
      fail("querylog details popup should close when Escape is pressed");
    }

    fetchLog.length = 0;
    querylogSearchInput().value = "ocsp2.apple.com";
    querylogForm().dispatchEvent("submit");
    await delay(20);
    dispatchHashchange();
    await delay(700);
    const searchRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    if (!searchRequests.some((entry) => entry.url.includes("search=ocsp2.apple.com"))) {
      fail("querylog search should trigger a fresh sidecar request with the search term");
    }
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["ocsp2.apple.com"])) {
      fail(`querylog search should replace the visible row set (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    querylogLoadMoreButton().dispatchEvent("click");
    await delay(20);
    dispatchHashchange();
    await delay(700);
    const noMoreSearchRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    if (!noMoreSearchRequests.some((entry) => entry.url.includes("search=ocsp2.apple.com") && entry.url.includes("older_than="))) {
      fail("querylog load more should keep the active search term when checking for more matching rows");
    }
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["ocsp2.apple.com"])) {
      fail(`querylog load more with no further search matches should keep the current visible rows unchanged (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    if ((querylogLoadMoreButton().style.display || "") !== "none") {
      fail("querylog load more should hide itself when the backend reports there are no more matching rows");
    }
    if (!(querylogStatusNote().textContent || "").includes("No more matching rows.")) {
      fail("querylog should tell the truth when no more matching rows exist for the active search");
    }

    fetchLog.length = 0;
    setLocation("/?pixel_identity=beta#pixel-querylog?response_status=all&limit=20&pixel_identity=beta");
    dispatchHashchange();
    await delay(700);
    const betaRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    if (!betaRequests.some((entry) => entry.url.includes("pixel_identity=beta"))) {
      fail("querylog identity filter should still work after the search flow");
    }
    if (windowObject.location.search !== "?pixel_identity=beta") {
      fail(`querylog identity flow should update the real URL after the search flow (saw ${windowObject.location.search || "(empty)"})`);
    }
    if (querylogIdentitySelect().value !== "beta") {
      fail("querylog identity selector should follow the beta route state");
    }
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["beta.example.net", "beta-dot.example.net"])) {
      fail(`querylog identity flow should render both beta DoH and DoT rows (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    if (!(querylogRowByName("beta-dot.example.net").textContent || "").includes("DOT")) {
      fail("improved querylog should show the DoT protocol badge in the beta row");
    }

    fetchLog.length = 0;
    setLocation("/");
    dispatchHashchange();
    await delay(700);
    const dnsSummaryLink = dashboardSummaryLink("dns");
    if (!dnsSummaryLink) {
      fail("dashboard should keep the main DNS Queries querylog entry");
    }
    navigateAnchor(dnsSummaryLink);
    await delay(700);
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`main DNS Queries entry should open the improved querylog when it is the saved default (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (!windowObject.location.hash.includes("response_status=all")) {
      fail(`main DNS Queries entry should canonicalize to all requests (saw ${windowObject.location.hash || "(empty)"})`);
    }

    fetchLog.length = 0;
    setLocation("/");
    dispatchHashchange();
    await delay(700);
    const blockedSummaryLink = dashboardSummaryLink("blocked");
    if (!blockedSummaryLink) {
      fail("dashboard should keep the blocked summary drill-down entry");
    }
    navigateAnchor(blockedSummaryLink);
    await delay(700);
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`blocked dashboard summary should still open the improved querylog drill-down when improved is the saved default (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (!windowObject.location.hash.includes("response_status=blocked")) {
      fail(`blocked dashboard summary should keep the blocked-only status filter (saw ${windowObject.location.hash || "(empty)"})`);
    }

    fetchLog.length = 0;
    setLocation("/?pixel_identity=alpha#pixel-querylog?response_status=blocked&limit=20&pixel_identity=alpha");
    dispatchHashchange();
    await delay(700);
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["alpha-blocked.example.net"])) {
      fail(`blocked improved querylog route should keep only blocked alpha rows (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    if (!querylogShowAllButton()) {
      fail("blocked improved querylog drill-down should expose the Show all requests action");
    }
    triggerShowAllButton(querylogShowAllButton(), "#pixel-querylog");
    await delay(20);
    dispatchHashchange();
    await delay(700);
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`Show all requests should stay on the improved querylog route (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (!windowObject.location.hash.includes("response_status=all")) {
      fail(`Show all requests should reset blocked drill-downs back to all requests (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (windowObject.location.hash.includes("response_status=blocked")) {
      fail(`Show all requests should clear the blocked-only route state (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["alpha.example.net", "alpha-blocked.example.net"])) {
      fail(`Show all requests should restore both allowed and blocked alpha rows (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }

    fetchLog.length = 0;
    clearQuerylogDom();
    setLocation("/?pixel_identity=alpha#logs?response_status=all&limit=20&pixel_identity=alpha");
    mountQuerylogDom();
    dispatchHashchange();
    await delay(50);
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`saved improved querylog preference should redirect fresh #logs visits to #pixel-querylog (saw ${windowObject.location.hash || "(empty)"})`);
    }
    dispatchHashchange();
    await delay(700);
    if (!querylogSurface()) {
      fail("redirected improved querylog visit should mount the improved querylog surface");
    }
    if (nativeQuerylogSwitcher()) {
      fail("redirected improved querylog visit should not leave the native switcher mounted");
    }

    fetchLog.length = 0;
    setLocation("/?pixel_identity=alpha#pixel-querylog?response_status=all&limit=20");
    dispatchHashchange();
    await delay(700);
    const fallbackRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    if (!querylogIdentitySelect() || querylogIdentitySelect().value !== "alpha") {
      fail("improved querylog should restore identity selection from the top-level URL query");
    }
    if (!fallbackRequests.some((entry) => entry.url.includes("pixel_identity=alpha"))) {
      fail("top-level pixel_identity should flow into improved querylog requests");
    }

    fetchLog.length = 0;
    setLocation("/");
    dispatchHashchange();
    await delay(700);
    const refreshedDashboardCard = documentRef.getElementById("pixel-stack-top-identities-card");
    const refreshedDashboardLink = refreshedDashboardCard && refreshedDashboardCard.querySelector("a");
    if (!refreshedDashboardLink) {
      fail("dashboard card should be rebuilt when returning to the dashboard route");
    }
    if (refreshedDashboardLink.getAttribute("href") !== "/?pixel_identity=alpha#pixel-querylog?search=&response_status=all&older_than=&limit=20&pixel_identity=alpha") {
      fail(`dashboard identity link should use the remembered improved route after switching defaults (saw ${refreshedDashboardLink.getAttribute("href") || "(empty)"})`);
    }
    navigateAnchor(refreshedDashboardLink);
    await delay(700);
    if (!querylogSurface() || querylogIdentitySelect().value !== "alpha") {
      fail("dashboard identity links should land on the remembered improved querylog view");
    }

    fetchLog.length = 0;
    setQuerylogScrollMetrics({ innerHeight: 640, scrollHeight: 700, scrollY: 80 });
    dispatchScroll();
    await delay(20);
    dispatchHashchange();
    await delay(700);
    const autoLoadRequests = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog"));
    if (!autoLoadRequests.some((entry) => entry.url.includes("older_than="))) {
      fail("querylog auto-load should request the next page when the page scroll reaches the bottom");
    }
    if (!visibleQuerylogNames().includes("older.example.net")) {
      fail(`querylog auto-load should append older rows to the sidecar table (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    setQuerylogScrollMetrics();

    fetchLog.length = 0;
    querylogPreferencePostFailures.push("native");
    querylogOpenNativeButton().dispatchEvent("click");
    await delay(50);
    const failedNativePreferencePosts = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog/view-preference") && entry.method === "POST");
    if (failedNativePreferencePosts.length !== 1) {
      fail(`failed native-querylog switches should still attempt one preference save (saw ${failedNativePreferencePosts.length} preference POSTs)`);
    }
    if (!windowObject.location.hash.startsWith("#logs")) {
      fail(`failed native-querylog switches should still return to the #logs route (saw ${windowObject.location.hash || "(empty)"})`);
    }
    mountQuerylogDom();
    dispatchHashchange();
    await delay(200);
    if (querylogSurface()) {
      fail("failed native-querylog switches should still unmount the improved querylog surface");
    }
    if (!nativeQuerylogSwitcher()) {
      fail("failed native-querylog switches should restore the native querylog switcher");
    }
    if (!(nativeQuerylogSwitcher().textContent || "").includes("Opened native Query Log, but couldn't save it as the default.")) {
      fail("failed native-querylog switches should surface a non-blocking save warning");
    }
    if (querylogPreferenceView !== "improved") {
      fail(`failed native-querylog switches should leave the persisted default unchanged (saw ${querylogPreferenceView})`);
    }
    if (windowObject.__pixelStackAdguardIdentity.querylogViewPreference !== "native") {
      fail("failed native-querylog switches should keep the native route active for the current page session");
    }
    if (windowObject.__pixelStackAdguardIdentity.querylogPersistedViewPreference !== "improved") {
      fail("failed native-querylog switches should keep the injected persisted default on improved");
    }

    fetchLog.length = 0;
    setLocation("/?pixel_identity=alpha#pixel-querylog?response_status=all&limit=20&pixel_identity=alpha");
    dispatchHashchange();
    await delay(700);
    if (!querylogSurface()) {
      fail("manually reopening the improved querylog should still work after a failed native save");
    }

    fetchLog.length = 0;
    querylogOpenNativeButton().dispatchEvent("click");
    await delay(50);
    const backToNativePosts = fetchLog.filter((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog/view-preference") && entry.method === "POST");
    if (backToNativePosts.length !== 1) {
      fail(`switching back to the native querylog should save the shared default immediately (saw ${backToNativePosts.length} preference POSTs)`);
    }
    if (!windowObject.location.hash.startsWith("#logs")) {
      fail(`switching back to native should return to the #logs route (saw ${windowObject.location.hash || "(empty)"})`);
    }
    mountQuerylogDom();
    dispatchHashchange();
    await delay(200);
    if (querylogSurface()) {
      fail("switching back to native should unmount the improved querylog surface");
    }
    if (!nativeQuerylogSwitcher()) {
      fail("switching back to native should restore the native querylog switcher");
    }
    if (fetchLog.some((entry) => entry.url.includes("/pixel-stack/identity/api/v1/querylog/row"))) {
      fail("native querylog route should stay free of improved sidecar row fetches after switching back");
    }

    fetchLog.length = 0;
    setLocation("/?pixel_identity=alpha#logs?response_status=blocked&limit=20&pixel_identity=alpha");
    mountQuerylogDom();
    dispatchHashchange();
    await delay(200);
    if (querylogSurface()) {
      fail("native blocked drill-down should stay on the native logs route before using Show all requests");
    }
    if (!nativeQuerylogShowAllButton()) {
      fail("native blocked drill-down should expose the Show all requests action");
    }
    triggerShowAllButton(nativeQuerylogShowAllButton(), "#pixel-querylog");
    await delay(50);
    dispatchHashchange();
    await delay(700);
    if (!windowObject.location.hash.startsWith("#pixel-querylog")) {
      fail(`native Show all requests should open the improved querylog route (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (!windowObject.location.hash.includes("response_status=all")) {
      fail(`native Show all requests should reset blocked drill-downs back to all requests (saw ${windowObject.location.hash || "(empty)"})`);
    }
    if (JSON.stringify(visibleQuerylogNames()) !== JSON.stringify(["alpha.example.net", "alpha-blocked.example.net"])) {
      fail(`native Show all requests should restore both allowed and blocked alpha rows (saw ${JSON.stringify(visibleQuerylogNames())})`);
    }
    process.exit(0);
  };
}, 7000);
EOF_NODE
if [[ "$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:${port}/pixel-stack/identity")" != "200" ]]; then
  echo "FAIL: identity web page endpoint did not return 200" >&2
  exit 1
fi
identity_html="${tmpdir}/identity.html"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity" > "${identity_html}"
if ! rg -Fq 'url.searchParams.set("pixel_identity", normalizedIdentityId);' "${identity_html}"; then
  echo "FAIL: standalone identity page should mirror pixel_identity into the real URL query when building logs links" >&2
  exit 1
fi
if ! rg -Fq 'const routeBase = state.querylogViewPreference === "improved" ? "#pixel-querylog" : "#logs";' "${identity_html}"; then
  echo "FAIL: standalone identity page should choose the querylog route from the saved view preference" >&2
  exit 1
fi
if ! rg -Fq 'url.hash = `${routeBase}?${hashParams.toString()}`;' "${identity_html}"; then
  echo "FAIL: standalone identity page should preserve pixel_identity in the selected querylog hash route when building links" >&2
  exit 1
fi

list_json="${tmpdir}/list-initial.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${list_json}"
if [[ "$(jq -r '.identities | length' "${list_json}")" != "0" ]]; then
  echo "FAIL: initial identities should be empty" >&2
  exit 1
fi

querylog_view_preference_initial_json="${tmpdir}/querylog-view-preference-initial.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog/view-preference" > "${querylog_view_preference_initial_json}"
if [[ "$(jq -r '.defaultView' "${querylog_view_preference_initial_json}")" != "improved" ]]; then
  echo "FAIL: querylog view preference should default to improved when no saved preference exists" >&2
  exit 1
fi

querylog_view_preference_saved_json="${tmpdir}/querylog-view-preference-saved.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"defaultView":"improved"}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog/view-preference" > "${querylog_view_preference_saved_json}"
if [[ "$(jq -r '.defaultView' "${querylog_view_preference_saved_json}")" != "improved" ]]; then
  echo "FAIL: querylog view preference API should persist the requested improved default" >&2
  exit 1
fi
if [[ "$(sqlite_setting "${ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE}" "querylogDefaultView")" != "improved" ]]; then
  echo "FAIL: querylog view preference API should store the improved default view in SQLite" >&2
  exit 1
fi
if [[ -e "${ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_VIEW_PREFERENCE_FILE}" ]]; then
  echo "FAIL: querylog view preference API should not recreate the legacy preference JSON file" >&2
  exit 1
fi

stop_sidecar
start_sidecar

querylog_view_preference_restart_json="${tmpdir}/querylog-view-preference-restart.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog/view-preference" > "${querylog_view_preference_restart_json}"
if [[ "$(jq -r '.defaultView' "${querylog_view_preference_restart_json}")" != "improved" ]]; then
  echo "FAIL: querylog view preference should survive a sidecar restart" >&2
  exit 1
fi

curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"defaultView":"native"}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog/view-preference" > /dev/null
if [[ "$(sqlite_setting "${ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE}" "querylogDefaultView")" != "native" ]]; then
  echo "FAIL: querylog view preference API should allow switching the saved default back to native in SQLite" >&2
  exit 1
fi

querylog_default_json="${tmpdir}/querylog-default.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog/summary" > "${querylog_default_json}"
if [[ "$(jq -r '.querylog_view_mode' "${querylog_default_json}")" != "user_only" ]]; then
  echo "FAIL: querylog summary default view mode should be user_only" >&2
  exit 1
fi
if [[ "$(jq -r '.querylog_status' "${querylog_default_json}")" != "ok" ]]; then
  echo "FAIL: querylog summary default status should be ok" >&2
  exit 1
fi
if [[ "$(jq -r '.top_clients' "${querylog_default_json}")" == *"127.0.0.1:"* ]]; then
  echo "FAIL: default querylog top_clients should exclude loopback internal entries" >&2
  exit 1
fi
if [[ "$(jq -r '.top_clients' "${querylog_default_json}")" == *"::1:"* ]]; then
  echo "FAIL: default querylog top_clients should exclude IPv6 loopback internal entries" >&2
  exit 1
fi
if [[ "$(jq -r '.internal_total_count' "${querylog_default_json}")" != "6" ]]; then
  echo "FAIL: querylog summary internal_total_count mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.internal_doh_count' "${querylog_default_json}")" != "2" ]]; then
  echo "FAIL: querylog summary internal_doh_count mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.internal_probe_domain_counts' "${querylog_default_json}")" != "example.com:3" ]]; then
  echo "FAIL: querylog summary internal_probe_domain_counts mismatch" >&2
  exit 1
fi

querylog_all_json="${tmpdir}/querylog-all.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog/summary?view=all&limit=5000" > "${querylog_all_json}"
if [[ "$(jq -r '.querylog_view_mode' "${querylog_all_json}")" != "all" ]]; then
  echo "FAIL: querylog summary view mode should be all when requested" >&2
  exit 1
fi
if [[ "$(jq -r '.top_clients' "${querylog_all_json}")" != *"127.0.0.1:doh:1"* ]]; then
  echo "FAIL: all-view querylog top_clients should include loopback internal DoH entries" >&2
  exit 1
fi
if [[ "$(jq -r '.total_doh_count' "${querylog_default_json}")" != "4" ]]; then
  echo "FAIL: querylog summary user_only total_doh_count mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.total_doh_count' "${querylog_all_json}")" != "6" ]]; then
  echo "FAIL: querylog summary all-view total_doh_count mismatch" >&2
  exit 1
fi

create_alpha_json="${tmpdir}/create-alpha.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"id":"alpha"}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${create_alpha_json}"
alpha_token="$(jq -r '.token' "${create_alpha_json}")"
alpha_dot_label="$(jq -r '.dotLabel' "${create_alpha_json}")"
alpha_dot_hostname="$(jq -r '.dotHostname' "${create_alpha_json}")"
if [[ ! "${alpha_token}" =~ ^[A-Za-z0-9._~-]{16,128}$ ]]; then
  echo "FAIL: create did not return valid generated token" >&2
  exit 1
fi
if [[ ! "${alpha_dot_label}" =~ ^[a-z0-9]{20}$ ]]; then
  echo "FAIL: create should return a 20-char DoT label when DoT identities are enabled" >&2
  exit 1
fi
if [[ "${alpha_dot_hostname}" != "${alpha_dot_label}.dns.jolkins.id.lv" ]]; then
  echo "FAIL: create should return the derived DoT hostname" >&2
  exit 1
fi
if [[ "$(jq -r '.applied' "${create_alpha_json}")" != "true" ]]; then
  echo "FAIL: create should report applied=true when runtime reload is scheduled" >&2
  exit 1
fi
if [[ "$(jq -r '.expiresEpochSeconds' "${create_alpha_json}")" != "null" ]]; then
  echo "FAIL: default create should return expiresEpochSeconds=null" >&2
  exit 1
fi

list_after_create="${tmpdir}/list-after-create.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${list_after_create}"
if [[ "$(jq -r '.primaryIdentityId' "${list_after_create}")" != "alpha" ]]; then
  echo "FAIL: primaryIdentityId should be alpha after first create" >&2
  exit 1
fi
if [[ "$(jq -r '.dotIdentityEnabled' "${list_after_create}")" != "true" ]]; then
  echo "FAIL: list endpoint should expose dotIdentityEnabled=true" >&2
  exit 1
fi
if [[ "$(jq -r '.dotHostnameBase' "${list_after_create}")" != "dns.jolkins.id.lv" ]]; then
  echo "FAIL: list endpoint should expose dotHostnameBase" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].token' "${list_after_create}")" != "${alpha_token}" ]]; then
  echo "FAIL: list endpoint token mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].tokenMasked' "${list_after_create}")" == "${alpha_token}" ]]; then
  echo "FAIL: tokenMasked should not expose full token" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].dotLabel' "${list_after_create}")" != "${alpha_dot_label}" ]]; then
  echo "FAIL: list endpoint dotLabel mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].dotHostname' "${list_after_create}")" != "${alpha_dot_hostname}" ]]; then
  echo "FAIL: list endpoint dotHostname mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].dotTarget' "${list_after_create}")" != "${alpha_dot_hostname}" ]]; then
  echo "FAIL: list endpoint should expose the hostname-only DoT target" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].dotTargetMasked' "${list_after_create}")" == "${alpha_dot_hostname}" ]]; then
  echo "FAIL: dotTargetMasked should not expose the full DoT target" >&2
  exit 1
fi
if ! jq -r '.identities[0].dotTargetMasked' "${list_after_create}" | rg -Fq '.dns.jolkins.id.lv'; then
  echo "FAIL: dotTargetMasked should preserve the shared DoT suffix" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].expiresEpochSeconds' "${list_after_create}")" != "null" ]]; then
  echo "FAIL: list endpoint should expose expiresEpochSeconds=null for default no-expiry identities" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].isExpired' "${list_after_create}")" != "false" ]]; then
  echo "FAIL: list endpoint should expose isExpired=false for active no-expiry identities" >&2
  exit 1
fi

apple_profile_headers="${tmpdir}/alpha-apple-profile.headers"
apple_profile_file="${tmpdir}/alpha-apple-profile.mobileconfig"
curl -fsS \
  -D "${apple_profile_headers}" \
  -H 'Host: example.test' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/alpha/apple-doh.mobileconfig" > "${apple_profile_file}"
if ! rg -Fqi 'content-type: application/x-apple-aspen-config' "${apple_profile_headers}"; then
  echo "FAIL: Apple DoH profile route should return the mobileconfig content type" >&2
  exit 1
fi
if ! rg -Fqi 'content-disposition: attachment; filename="alpha-apple-doh.mobileconfig"' "${apple_profile_headers}"; then
  echo "FAIL: Apple DoH profile route should return an attachment filename" >&2
  exit 1
fi
python3 - <<'PY' "${apple_profile_file}" "${alpha_token}"
import plistlib
import sys

path = sys.argv[1]
token = sys.argv[2]
with open(path, "rb") as handle:
  payload = plistlib.load(handle)

content = payload.get("PayloadContent")
if not isinstance(content, list) or len(content) != 1:
  raise SystemExit("FAIL: Apple DoH profile should contain exactly one payload")

entry = content[0]
if entry.get("PayloadType") != "com.apple.dnsSettings.managed":
  raise SystemExit("FAIL: Apple DoH profile should use the DNS Settings payload type")

dns_settings = entry.get("DNSSettings", {})
expected_url = f"https://example.test/{token}/dns-query"
if dns_settings.get("ServerURL") != expected_url:
  raise SystemExit("FAIL: Apple DoH profile should embed the full tokenized DoH URL on standard HTTPS")
if dns_settings.get("DNSProtocol") != "HTTPS":
  raise SystemExit("FAIL: Apple DoH profile should configure DoH")
if dns_settings.get("MatchingDomains") != [""]:
  raise SystemExit("FAIL: Apple DoH profile should apply to the default domain set")
PY

apple_dot_profile_headers="${tmpdir}/alpha-apple-dot-profile.headers"
apple_dot_profile_file="${tmpdir}/alpha-apple-dot-profile.mobileconfig"
curl -fsS \
  -D "${apple_dot_profile_headers}" \
  -H 'Host: 127.0.0.1' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/alpha/apple-dot.mobileconfig" > "${apple_dot_profile_file}"
if ! rg -Fqi 'content-type: application/x-apple-aspen-config' "${apple_dot_profile_headers}"; then
  echo "FAIL: Apple TLS profile route should return the mobileconfig content type" >&2
  exit 1
fi
if ! rg -Fqi 'content-disposition: attachment; filename="alpha-apple-dot.mobileconfig"' "${apple_dot_profile_headers}"; then
  echo "FAIL: Apple TLS profile route should return an attachment filename" >&2
  exit 1
fi
python3 - <<'PY' "${apple_dot_profile_file}" "${alpha_dot_hostname}"
import plistlib
import sys

path = sys.argv[1]
dot_hostname = sys.argv[2]
with open(path, "rb") as handle:
  payload = plistlib.load(handle)

content = payload.get("PayloadContent")
if not isinstance(content, list) or len(content) != 1:
  raise SystemExit("FAIL: Apple TLS profile should contain exactly one payload")

entry = content[0]
if entry.get("PayloadType") != "com.apple.dnsSettings.managed":
  raise SystemExit("FAIL: Apple TLS profile should use the DNS Settings payload type")

dns_settings = entry.get("DNSSettings", {})
if dns_settings.get("DNSProtocol") != "TLS":
  raise SystemExit("FAIL: Apple TLS profile should configure DoT")
if dns_settings.get("ServerName") != dot_hostname:
  raise SystemExit("FAIL: Apple TLS profile should embed the per-identity DoT hostname")
if dns_settings.get("ServerAddresses") != ["127.0.0.1"]:
  raise SystemExit("FAIL: Apple TLS profile should embed the request host as the direct server address")
if dns_settings.get("MatchingDomains") != [""]:
  raise SystemExit("FAIL: Apple TLS profile should apply to the default domain set")
PY

missing_profile_body="${tmpdir}/missing-apple-profile.json"
missing_profile_code="$(curl -sS -o "${missing_profile_body}" -w '%{http_code}' \
  -H 'Host: example.test' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/missing/apple-doh.mobileconfig")"
if [[ "${missing_profile_code}" != "404" ]]; then
  echo "FAIL: unknown Apple DoH profile requests should return 404" >&2
  exit 1
fi
if ! jq -r '.error' "${missing_profile_body}" | rg -Fq 'Identity not found'; then
  echo "FAIL: unknown Apple DoH profile requests should explain the missing identity" >&2
  exit 1
fi

missing_dot_profile_body="${tmpdir}/missing-apple-dot-profile.json"
missing_dot_profile_code="$(curl -sS -o "${missing_dot_profile_body}" -w '%{http_code}' \
  -H 'Host: 127.0.0.1' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/missing/apple-dot.mobileconfig")"
if [[ "${missing_dot_profile_code}" != "404" ]]; then
  echo "FAIL: unknown Apple TLS profile requests should return 404" >&2
  exit 1
fi
if ! jq -r '.error' "${missing_dot_profile_body}" | rg -Fq 'Identity not found'; then
  echo "FAIL: unknown Apple TLS profile requests should explain the missing identity" >&2
  exit 1
fi

duplicate_body="${tmpdir}/duplicate-body.json"
duplicate_code="$(curl -sS -o "${duplicate_body}" -w '%{http_code}' \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"id":"alpha"}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities")"
if [[ "${duplicate_code}" != "400" ]]; then
  echo "FAIL: duplicate create should return 400" >&2
  exit 1
fi
if ! jq -r '.error' "${duplicate_body}" | rg -Fq 'Identity already exists'; then
  echo "FAIL: duplicate create error message mismatch" >&2
  exit 1
fi

create_beta_json="${tmpdir}/create-beta.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"id":"beta"}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${create_beta_json}"
beta_token="$(jq -r '.token' "${create_beta_json}")"
beta_dot_label="$(jq -r '.dotLabel' "${create_beta_json}")"
if [[ ! "${beta_token}" =~ ^[A-Za-z0-9._~-]{16,128}$ ]]; then
  echo "FAIL: create beta did not return valid generated token" >&2
  exit 1
fi
if [[ ! "${beta_dot_label}" =~ ^[a-z0-9]{20}$ ]]; then
  echo "FAIL: create beta should return a 20-char DoT label when DoT identities are enabled" >&2
  exit 1
fi
python3 - <<'PY' "${ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_JSON_FILE}" "${beta_dot_label}"
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
path.write_text(
    path.read_text().replace("__BETA_DOT_LABEL__", sys.argv[2]),
    encoding="utf-8",
)
PY

python3 - <<'PY' "${ADGUARDHOME_DOT_ACCESS_LOG_FILE}" "${querylog_dot_beta_time}" "${beta_dot_label}"
from datetime import datetime, timedelta, timezone
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
row_time = datetime.fromisoformat(sys.argv[2].replace("Z", "+00:00")).astimezone(timezone.utc)
end_time = row_time + timedelta(seconds=1)
hostname = f"{sys.argv[3]}.dns.jolkins.id.lv"
path.write_text(
    f"{end_time.isoformat().replace('+00:00', 'Z')}\t{hostname}\t200\t2.000\t62.205.193.194\t{end_time.timestamp():.3f}\n",
    encoding="utf-8",
)
PY

iso_now="$(date -u +%Y-%m-%dT%H:%M:%S+00:00)"
cat > "${ADGUARDHOME_DOH_ACCESS_LOG_FILE}" <<EOF_LOG
${querylog_public_time}	/${alpha_token}/dns-query?dns=a	200	0.010	212.3.197.32	$(python3 - <<'PY'
from datetime import datetime, timezone
print(f"{datetime(2026, 3, 7, 5, 0, 0, 100000, tzinfo=timezone.utc).timestamp():.3f}")
PY
)
${querylog_service_time}	/${beta_token}/dns-query?dns=service	200	0.040	212.3.197.32	$(python3 - <<'PY'
from datetime import datetime, timezone
print(f"{datetime(2026, 3, 7, 5, 0, 0, 600000, tzinfo=timezone.utc).timestamp():.3f}")
PY
)
${iso_now}	/${alpha_token}/dns-query?dns=b	404	0.020
${iso_now}	/dns-query?dns=bare	404	0.030
EOF_LOG

usage_json="${tmpdir}/usage.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" > "${usage_json}"
if [[ "$(jq -r '.totalRequests' "${usage_json}")" != "4" ]]; then
  echo "FAIL: usage totalRequests mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.totalRequestCount' "${usage_json}")" != "5" ]]; then
  echo "FAIL: usage totalRequestCount should include DoT querylog rows" >&2
  exit 1
fi
if [[ "$(jq -r '.dotTotalRequests' "${usage_json}")" != "1" ]]; then
  echo "FAIL: usage dotTotalRequests should count matched DoT rows" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[] | select(.id == "alpha") | .requestCount' "${usage_json}")" != "2" ]]; then
  echo "FAIL: usage alpha requestCount mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[] | select(.id == "beta") | .requestCount' "${usage_json}")" != "2" ]]; then
  echo "FAIL: usage beta requestCount should include DoT traffic" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[] | select(.id == "beta") | .dotRequestCount' "${usage_json}")" != "1" ]]; then
  echo "FAIL: usage beta dotRequestCount mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[] | select(.id == "__bare__") | .requestCount' "${usage_json}")" != "1" ]]; then
  echo "FAIL: usage __bare__ requestCount mismatch" >&2
  exit 1
fi

python3 - <<'PY' "${ADGUARDHOME_DOH_IDENTITY_WEB_QUERYLOG_JSON_FILE}" "${ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE}" "${querylog_public_time}"
from datetime import datetime, timezone
import json
import pathlib
import sqlite3
import sys

fixture_path = pathlib.Path(sys.argv[1])
db_path = pathlib.Path(sys.argv[2])
row_time = datetime.fromisoformat(sys.argv[3].replace("Z", "+00:00")).astimezone(timezone.utc)
row_time_iso = row_time.isoformat().replace("+00:00", "Z")
row_time_ms = int(round(row_time.timestamp() * 1000.0))

fixture = json.loads(fixture_path.read_text(encoding="utf-8"))
rows = list(fixture.get("data", []))
beta_dot_row_found = False
for entry in rows:
    if not isinstance(entry, dict):
        continue
    question = entry.get("question")
    if not isinstance(question, dict):
        continue
    if str(question.get("name", "") or "").strip().lower() != "beta-dot.example.net":
        continue
    entry["client"] = "172.19.0.13"
    beta_dot_row_found = True
    break
if not beta_dot_row_found:
    raise SystemExit("FAIL: expected beta-dot.example.net querylog row in fixture")
rows.append({
    "time": row_time_iso,
    "client": "198.51.100.55",
    "client_proto": "doh",
    "elapsedMs": "14",
    "status": "NOERROR",
    "question": {"name": "burst-alpha.example.net", "type": "A"},
    "client_info": {"whois": {}},
})
rows.append({
    "time": row_time_iso,
    "client": "198.51.100.55",
    "client_proto": "doh",
    "elapsedMs": "14",
    "status": "NOERROR",
    "question": {"name": "burst-beta.example.net", "type": "A"},
    "client_info": {"whois": {}},
})
rows.append({
    "time": row_time_iso,
    "client": "198.51.100.55",
    "client_proto": "doh",
    "elapsedMs": "14",
    "status": "NOERROR",
    "question": {"name": "burst-https.example.net", "type": "HTTPS"},
    "client_info": {"whois": {}},
})
rows.append({
    "time": row_time_iso,
    "client": "172.19.0.12",
    "client_proto": "doh",
    "elapsedMs": "9",
    "status": "NOERROR",
    "question": {"name": "iphone.example.net", "type": "A"},
    "client_info": {"whois": {}},
})
rows.append({
    "time": row_time_iso,
    "client": "172.19.0.12",
    "client_proto": "doh",
    "elapsedMs": "10",
    "status": "NOERROR",
    "question": {"name": "proxy-public.example.net", "type": "A"},
    "client_info": {"whois": {"country": "ZZ", "orgname": "Proxy Bridge"}},
})
fixture["data"] = rows
fixture_path.write_text(json.dumps(fixture, separators=(",", ":")), encoding="utf-8")

conn = sqlite3.connect(str(db_path))
conn.execute(
    """
    INSERT INTO doh_events (
      ts_ms,
      identity_id,
      client_ip,
      status,
      request_time_ms,
      query_name,
      query_type,
      protocol
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    """,
    (row_time_ms, "alpha", "198.51.100.55", 200, 14, "burst-alpha.example.net", "1", "doh"),
)
conn.execute(
    """
    INSERT INTO doh_events (
      ts_ms,
      identity_id,
      client_ip,
      status,
      request_time_ms,
      query_name,
      query_type,
      protocol
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    """,
    (row_time_ms, "beta", "198.51.100.55", 200, 14, "burst-beta.example.net", "1", "doh"),
)
conn.execute(
    """
    INSERT INTO doh_events (
      ts_ms,
      identity_id,
      client_ip,
      status,
      request_time_ms,
      query_name,
      query_type,
      protocol
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    """,
    (row_time_ms, "alpha", "198.51.100.55", 200, 14, "burst-https.example.net", "65", "doh"),
)
conn.execute(
    """
    INSERT INTO doh_events (
      ts_ms,
      identity_id,
      client_ip,
      status,
      request_time_ms,
      query_name,
      query_type,
      protocol
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    """,
    (row_time_ms, "alpha", "192.168.31.39", 200, 9, "iphone.example.net", "1", "doh"),
)
conn.execute(
    """
    INSERT INTO doh_events (
      ts_ms,
      identity_id,
      client_ip,
      status,
      request_time_ms,
      query_name,
      query_type,
      protocol
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    """,
    (row_time_ms, "alpha", "212.3.197.32", 200, 10, "proxy-public.example.net", "1", "doh"),
)
conn.commit()
conn.close()
PY

proxy_querylog_json="${tmpdir}/proxy-querylog.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" > "${proxy_querylog_json}"
if [[ "$(count_invocations 'events ')" != "0" ]]; then
  echo "FAIL: proxied querylog should read identity matches from SQLite instead of invoking identityctl events" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "public.example.net") | .client_info.whois.orgname' "${proxy_querylog_json}")" != "Operator Example" ]]; then
  echo "FAIL: proxy querylog should fill missing public-IP orgname from cache" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "public.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "alpha" ]]; then
  echo "FAIL: proxy querylog should correlate DoH rows back to identity events" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "service.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "beta" ]]; then
  echo "FAIL: proxy querylog should keep non-phone/service traffic outside the alpha identity" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "burst-alpha.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "alpha" ]]; then
  echo "FAIL: proxy querylog should use query-aware matching to keep same-client burst rows assigned to alpha" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "burst-beta.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "beta" ]]; then
  echo "FAIL: proxy querylog should use query-aware matching to keep same-client burst rows assigned to beta" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "burst-https.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "alpha" ]]; then
  echo "FAIL: proxy querylog should match numeric DNS type codes back to named query types for DoH rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "beta-dot.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "beta" ]]; then
  echo "FAIL: proxy querylog should map DoT rows directly from AdGuard client metadata" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "beta-dot.example.net") | .client' "${proxy_querylog_json}")" != "62.205.193.194" ]]; then
  echo "FAIL: proxy querylog should recover the origin client IP for DoT rows from the stream access log" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "beta-dot.example.net") | .pixelOriginalClient' "${proxy_querylog_json}")" != "172.19.0.13" ]]; then
  echo "FAIL: proxy querylog should preserve the original non-public DoT bridge IP after rewriting the displayed client" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "iphone.example.net") | .pixelIdentityId' "${proxy_querylog_json}")" != "alpha" ]]; then
  echo "FAIL: proxy querylog should recover identities when AdGuard reports the frontend bridge IP instead of the real client" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "iphone.example.net") | .client' "${proxy_querylog_json}")" != "192.168.31.39" ]]; then
  echo "FAIL: proxy querylog should rewrite frontend bridge IP rows back to the matched real client IP" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "iphone.example.net") | .pixelOriginalClient' "${proxy_querylog_json}")" != "172.19.0.12" ]]; then
  echo "FAIL: proxy querylog should preserve the original bridge IP when rewriting the displayed client" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "proxy-public.example.net") | .client' "${proxy_querylog_json}")" != "212.3.197.32" ]]; then
  echo "FAIL: proxy querylog should rewrite proxied public rows back to the matched real client IP" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "proxy-public.example.net") | .client_info.whois.orgname' "${proxy_querylog_json}")" != "Operator Example" ]]; then
  echo "FAIL: proxy querylog should refresh client org from the rewritten real client IP instead of keeping the proxy org" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "self.example.net") | .client' "${proxy_querylog_json}")" != "192.168.31.25" ]]; then
  echo "FAIL: proxy querylog should remap non-probe loopback rows to device LAN IP" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "example.com" and .client_proto == "plain") | .client' "${proxy_querylog_json}")" != "127.0.0.1" ]]; then
  echo "FAIL: proxy querylog should keep probe loopback rows as localhost" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "public.example.net") | .pixelQuerylogDisplay.protocolLabel' "${proxy_querylog_json}")" != "DOH" ]]; then
  echo "FAIL: proxy querylog should expose a normalized DOH protocol label for improved querylog rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "public.example.net") | .pixelQuerylogDisplay.statusTone' "${proxy_querylog_json}")" != "allowed" ]]; then
  echo "FAIL: proxy querylog should keep not-filtered rows out of blocked styling" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "public.example.net") | .pixelQuerylogDisplay.statusLabel' "${proxy_querylog_json}")" != "Allowed" ]]; then
  echo "FAIL: proxy querylog should present NotFiltered rows as normal allowed traffic" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "public.example.net") | .pixelQuerylogDisplay.summary' "${proxy_querylog_json}")" != "" ]]; then
  echo "FAIL: proxy querylog should hide NotFiltered markers from normal traffic rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "beta-dot.example.net") | .pixelQuerylogDisplay.protocolLabel' "${proxy_querylog_json}")" != "DOT" ]]; then
  echo "FAIL: proxy querylog should expose a normalized DOT protocol label for improved querylog rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "blocked.internal.example") | .pixelQuerylogDisplay.statusTone' "${proxy_querylog_json}")" != "blocked" ]]; then
  echo "FAIL: proxy querylog should expose blocked-row styling metadata for improved querylog rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "blocked.internal.example") | .pixelQuerylogDisplay.statusLabel' "${proxy_querylog_json}")" != "Blocked by filters" ]]; then
  echo "FAIL: proxy querylog should expose blocked-row labels for improved querylog rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "blocked.internal.example") | .pixelQuerylogDisplay.identityLabel' "${proxy_querylog_json}")" != "Unmatched" ]]; then
  echo "FAIL: proxy querylog should keep unmatched blocked rows labeled as Unmatched" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "blocked.internal.example") | .pixelQuerylogDisplay.details[] | select(.label == "Reason") | .value' "${proxy_querylog_json}")" != "Blocked by filters" ]]; then
  echo "FAIL: proxy querylog details should expose the blocked reason for improved querylog popups" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "blocked.internal.example") | .pixelQuerylogDisplay.details[] | select(.label == "Blocked by list") | .values[0]' "${proxy_querylog_json}")" != "Default blocklist" ]]; then
  echo "FAIL: proxy querylog details should expose the blocking list name for improved querylog popups" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "blocked.internal.example") | .pixelQuerylogDisplay.details[] | select(.label == "Matching rule") | .values[0]' "${proxy_querylog_json}")" != "||blocked.internal.example^" ]]; then
  echo "FAIL: proxy querylog details should expose the matching blocking rule for improved querylog popups" >&2
  exit 1
fi

proxy_querylog_blocked_json="${tmpdir}/proxy-querylog-blocked.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=blocked&older_than=&limit=20" > "${proxy_querylog_blocked_json}"
if [[ "$(jq -r '.data | length' "${proxy_querylog_blocked_json}")" != "1" ]]; then
  echo "FAIL: blocked proxy querylog filter should keep only blocked rows" >&2
  exit 1
fi
if [[ "$(jq -r '.data[0].question.name' "${proxy_querylog_blocked_json}")" != "blocked.internal.example" ]]; then
  echo "FAIL: blocked proxy querylog filter should return the blocked request row" >&2
  exit 1
fi
if jq -e '.data[] | select(.question.name == "public.example.net")' "${proxy_querylog_blocked_json}" >/dev/null 2>&1; then
  echo "FAIL: blocked proxy querylog filter should exclude allowed rows" >&2
  exit 1
fi

proxy_querylog_processed_json="${tmpdir}/proxy-querylog-processed.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=processed&older_than=&limit=20" > "${proxy_querylog_processed_json}"
if jq -e '.data[] | select(.question.name == "blocked.internal.example")' "${proxy_querylog_processed_json}" >/dev/null 2>&1; then
  echo "FAIL: processed proxy querylog filter should exclude blocked rows" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "public.example.net")' "${proxy_querylog_processed_json}" >/dev/null 2>&1; then
  echo "FAIL: processed proxy querylog filter should keep allowed rows" >&2
  exit 1
fi

proxy_querylog_invalid_status_json="${tmpdir}/proxy-querylog-invalid-status.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=definitely_not_real&older_than=&limit=20" > "${proxy_querylog_invalid_status_json}"
if [[ "$(jq -r '.data | length' "${proxy_querylog_invalid_status_json}")" != "$(jq -r '.data | length' "${proxy_querylog_json}")" ]]; then
  echo "FAIL: invalid proxy querylog response_status values should fall back to all rows" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "public.example.net")' "${proxy_querylog_invalid_status_json}" >/dev/null 2>&1; then
  echo "FAIL: invalid proxy querylog response_status values should keep allowed rows via the all-rows fallback" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "blocked.internal.example")' "${proxy_querylog_invalid_status_json}" >/dev/null 2>&1; then
  echo "FAIL: invalid proxy querylog response_status values should keep blocked rows via the all-rows fallback" >&2
  exit 1
fi

proxy_querylog_alpha_json="${tmpdir}/proxy-querylog-alpha.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20&identity=alpha" > "${proxy_querylog_alpha_json}"
if [[ "$(jq -r '.data | length' "${proxy_querylog_alpha_json}")" != "5" ]]; then
  echo "FAIL: identity-filtered proxy querylog should keep every matched alpha row, including numeric-type DoH matches and rewritten bridge-IP rows" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "public.example.net")' "${proxy_querylog_alpha_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the original alpha query row" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "burst-alpha.example.net")' "${proxy_querylog_alpha_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the burst alpha query row after query-aware matching" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "burst-https.example.net")' "${proxy_querylog_alpha_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep HTTPS rows matched through numeric DNS query types" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "iphone.example.net")' "${proxy_querylog_alpha_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the bridge-IP phone row after fallback matching" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "proxy-public.example.net")' "${proxy_querylog_alpha_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the proxied public row after fallback matching" >&2
  exit 1
fi
if jq -e '.data[] | select(.question.name == "service.example.net")' "${proxy_querylog_alpha_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should exclude non-alpha rows" >&2
  exit 1
fi
if [[ "$(jq -r '.oldest' "${proxy_querylog_alpha_json}")" != "$(jq -r '.data[-1].time' "${proxy_querylog_alpha_json}")" ]]; then
  echo "FAIL: identity-filtered proxy querylog should expose native-compatible oldest cursor" >&2
  exit 1
fi
alpha_oldest="$(jq -r '.oldest' "${proxy_querylog_alpha_json}")"
alpha_oldest_encoded="$(python3 - <<'PY' "${alpha_oldest}"
import sys
import urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=""))
PY
)"
proxy_querylog_alpha_next_json="${tmpdir}/proxy-querylog-alpha-next.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=${alpha_oldest_encoded}&limit=20&identity=alpha" > "${proxy_querylog_alpha_next_json}"
if jq -e '.data[] | select(.question.name == "public.example.net")' "${proxy_querylog_alpha_next_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should not repeat rows after paging with oldest" >&2
  exit 1
fi
if [[ "$(jq -r '.pixelMeta.hasMore' "${proxy_querylog_alpha_next_json}")" != "false" ]]; then
  echo "FAIL: identity-filtered proxy querylog should tell the frontend when there are no further matching rows" >&2
  exit 1
fi

proxy_querylog_beta_json="${tmpdir}/proxy-querylog-beta.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20&identity=beta" > "${proxy_querylog_beta_json}"
if [[ "$(jq -r '.data | length' "${proxy_querylog_beta_json}")" != "3" ]]; then
  echo "FAIL: identity-filtered proxy querylog should keep every matched beta row, including the burst query" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "service.example.net")' "${proxy_querylog_beta_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the DoH beta row" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "burst-beta.example.net")' "${proxy_querylog_beta_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the burst beta row after query-aware matching" >&2
  exit 1
fi
if ! jq -e '.data[] | select(.question.name == "beta-dot.example.net")' "${proxy_querylog_beta_json}" >/dev/null 2>&1; then
  echo "FAIL: identity-filtered proxy querylog should keep the DoT beta row" >&2
  exit 1
fi
if [[ "$(jq -r '.data[] | select(.question.name == "beta-dot.example.net") | .pixelIdentityId' "${proxy_querylog_beta_json}")" != "beta" ]]; then
  echo "FAIL: beta identity-filtered proxy querylog should preserve direct DoT identity metadata" >&2
  exit 1
fi
if jq -e '.data[] | select(.question.name == "public.example.net")' "${proxy_querylog_beta_json}" >/dev/null 2>&1; then
  echo "FAIL: beta identity-filtered proxy querylog should exclude alpha rows" >&2
  exit 1
fi

proxy_stats_json="${tmpdir}/proxy-stats.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/stats" > "${proxy_stats_json}"
if [[ "$(jq -r '.top_clients | map(keys[]) | join(",")' "${proxy_stats_json}")" != *"192.168.31.25"* ]]; then
  echo "FAIL: proxy stats should expose remapped device LAN IP in top_clients" >&2
  exit 1
fi
if [[ "$(jq -r '.top_clients | map(keys[]) | join(",")' "${proxy_stats_json}")" != *"62.205.193.194"* ]]; then
  echo "FAIL: proxy stats should expose recovered DoT origin client IP in top_clients" >&2
  exit 1
fi
if [[ "$(jq -r '.top_clients | map(keys[]) | join(",")' "${proxy_stats_json}")" == *"127.0.0.1"* ]]; then
  echo "FAIL: proxy stats should exclude IPv4 loopback in top_clients" >&2
  exit 1
fi
if [[ "$(jq -r '.top_clients | map(keys[]) | join(",")' "${proxy_stats_json}")" == *"::1"* ]]; then
  echo "FAIL: proxy stats should exclude IPv6 loopback in top_clients" >&2
  exit 1
fi

revoke_beta_json="${tmpdir}/revoke-beta.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -X DELETE \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/beta" > "${revoke_beta_json}"
if [[ "$(jq -r '.revoked' "${revoke_beta_json}")" != "beta" ]]; then
  echo "FAIL: revoke(beta) should report revoked=beta" >&2
  exit 1
fi
if [[ "$(jq -r '.remaining' "${revoke_beta_json}")" != "1" ]]; then
  echo "FAIL: revoke(beta) should leave alpha as the sole identity" >&2
  exit 1
fi

proxy_clients_json="${tmpdir}/proxy-clients.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/clients" > "${proxy_clients_json}"
if [[ "$(jq -r '.auto_clients[] | select(.ip == "212.3.197.32") | .whois_info.orgname' "${proxy_clients_json}")" != "Operator Example" ]]; then
  echo "FAIL: proxy clients should enrich public auto_clients whois metadata" >&2
  exit 1
fi

clear_identityctl_delay
wait_for_ttl_expiry \
  "${identities_cache_ttl_seconds}" \
  "${shared_lookup_cache_ttl_seconds}" \
  "${querylog_result_cache_ttl_seconds}" \
  "${usage_cache_ttl_seconds}"

: > "${identityctl_invocation_log}"
run_parallel_get "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities"
if [[ "$(count_invocations 'list ')" != "1" ]]; then
  echo "FAIL: concurrent identities GETs should collapse to one identityctl list invocation" >&2
  exit 1
fi
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" >/dev/null
if [[ "$(count_invocations 'list ')" != "1" ]]; then
  echo "FAIL: identities GETs should stay warm inside the identities cache TTL" >&2
  exit 1
fi
wait_for_ttl_expiry "${identities_cache_ttl_seconds}"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" >/dev/null
if [[ "$(count_invocations 'list ')" != "2" ]]; then
  echo "FAIL: identities GET cache should expire on the configured identities TTL" >&2
  exit 1
fi

wait_for_ttl_expiry "${shared_lookup_cache_ttl_seconds}"
: > "${identityctl_invocation_log}"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" >/dev/null
if [[ "$(count_invocations 'usage ')" != "1" ]]; then
  echo "FAIL: first usage GET should invoke identityctl usage once" >&2
  exit 1
fi
if [[ "$(count_invocations 'events ')" != "0" ]]; then
  echo "FAIL: usage GET should not invoke identityctl events after the SQLite refactor" >&2
  exit 1
fi
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" >/dev/null
if [[ "$(count_invocations 'usage ')" != "1" ]]; then
  echo "FAIL: usage GETs should stay warm inside the usage route cache TTL" >&2
  exit 1
fi
wait_for_ttl_expiry "${usage_cache_ttl_seconds}"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" >/dev/null
if [[ "$(count_invocations 'usage ')" != "2" ]]; then
  echo "FAIL: usage route cache should expire on the configured usage TTL" >&2
  exit 1
fi
wait_for_ttl_expiry "${shared_lookup_cache_ttl_seconds}"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" >/dev/null
if [[ "$(count_invocations 'usage ')" != "3" ]]; then
  echo "FAIL: usage GET after the shared lookup TTL should trigger a fresh usage rebuild" >&2
  exit 1
fi
if [[ "$(count_invocations 'events ')" != "0" ]]; then
  echo "FAIL: usage GET after cache expiry should still avoid identityctl events" >&2
  exit 1
fi

wait_for_ttl_expiry "${shared_lookup_cache_ttl_seconds}"
: > "${identityctl_invocation_log}"
run_parallel_get "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20"
if [[ "$(count_invocations 'events ')" != "0" ]]; then
  echo "FAIL: concurrent proxied querylog GETs should avoid identityctl events entirely" >&2
  exit 1
fi
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" >/dev/null
wait_for_ttl_expiry "${querylog_result_cache_ttl_seconds}"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" >/dev/null
wait_for_ttl_expiry "${shared_lookup_cache_ttl_seconds}"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" >/dev/null
if [[ "$(count_invocations 'events ')" != "0" ]]; then
  echo "FAIL: proxied querylog GET after cache expiry should still avoid identityctl events" >&2
  exit 1
fi

clear_identityctl_delay
wait_for_ttl_expiry "${shared_lookup_cache_ttl_seconds}"
usage_stale_seed_json="${tmpdir}/usage-stale-seed.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" > "${usage_stale_seed_json}"
if [[ "$(jq -r '.pixelMeta.cacheState' "${usage_stale_seed_json}")" != "fresh" ]]; then
  echo "FAIL: warm usage seed request should return fresh cache metadata" >&2
  exit 1
fi
set_identityctl_delay "1.0"
wait_for_ttl_expiry "${usage_cache_ttl_seconds}"
usage_stale_json="${tmpdir}/usage-stale.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=all&window=7d" > "${usage_stale_json}"
if [[ "$(jq -r '.pixelMeta.cacheState' "${usage_stale_json}")" != "stale" ]]; then
  echo "FAIL: slow usage refresh should return stale cache metadata when fallback data exists" >&2
  exit 1
fi
if [[ "$(jq -r '.pixelMeta.stale' "${usage_stale_json}")" != "true" ]]; then
  echo "FAIL: slow usage refresh should mark stale fallback payloads as stale" >&2
  exit 1
fi
if [[ "$(jq -r '.retryable' "${usage_stale_json}")" != "true" ]]; then
  echo "FAIL: slow usage refresh should mark stale fallback payloads as retryable" >&2
  exit 1
fi
if ! jq -r '.error' "${usage_stale_json}" | rg -Fq 'timed out'; then
  echo "FAIL: slow usage refresh should report a timeout error on stale fallback payloads" >&2
  exit 1
fi
if [[ "$(jq -r '.identities | length' "${usage_stale_json}")" == "0" ]]; then
  echo "FAIL: slow usage refresh should return the last successful identities payload instead of blank data" >&2
  exit 1
fi

usage_timeout_json="${tmpdir}/usage-timeout.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=alpha&window=7d" > "${usage_timeout_json}"
if [[ "$(jq -r '.pixelMeta.cacheState' "${usage_timeout_json}")" != "miss" ]]; then
  echo "FAIL: first slow usage request without fallback data should report a cache miss" >&2
  exit 1
fi
if [[ "$(jq -r '.pixelMeta.stale' "${usage_timeout_json}")" != "false" ]]; then
  echo "FAIL: first slow usage request without fallback data should not pretend to be stale data" >&2
  exit 1
fi
if [[ "$(jq -r '.retryable' "${usage_timeout_json}")" != "true" ]]; then
  echo "FAIL: first slow usage request without fallback data should be retryable" >&2
  exit 1
fi
if ! jq -r '.error' "${usage_timeout_json}" | rg -Fq 'timed out'; then
  echo "FAIL: first slow usage request without fallback data should report a timeout error" >&2
  exit 1
fi
if [[ "$(jq -r '.identities | length' "${usage_timeout_json}")" != "0" ]]; then
  echo "FAIL: first slow usage request without fallback data should return the structured empty usage payload" >&2
  exit 1
fi

clear_identityctl_delay
wait_for_ttl_expiry "1.0" "${shared_lookup_cache_ttl_seconds}"
querylog_fresh_seed_json="${tmpdir}/querylog-fresh-seed.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" > "${querylog_fresh_seed_json}"
if [[ "$(jq -r '.pixelMeta.cacheState' "${querylog_fresh_seed_json}")" != "fresh" ]]; then
  echo "FAIL: warm proxied querylog seed request should return fresh cache metadata" >&2
  exit 1
fi
set_identityctl_delay "1.0"
wait_for_ttl_expiry "${querylog_result_cache_ttl_seconds}" "${shared_lookup_cache_ttl_seconds}"
querylog_identityctl_json="${tmpdir}/querylog-identityctl-slow.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" > "${querylog_identityctl_json}"
if [[ "$(jq -r '.pixelMeta.cacheState' "${querylog_identityctl_json}")" != "fresh" ]]; then
  echo "FAIL: slow identityctl paths should not make the SQL-backed improved querylog go stale" >&2
  exit 1
fi
if [[ "$(jq -r '.pixelMeta.stale' "${querylog_identityctl_json}")" != "false" ]]; then
  echo "FAIL: slow identityctl paths should not mark the SQL-backed improved querylog as stale" >&2
  exit 1
fi
if [[ "$(jq -r '.retryable // "false"' "${querylog_identityctl_json}")" != "false" ]]; then
  echo "FAIL: slow identityctl paths should not mark the SQL-backed improved querylog as retryable" >&2
  exit 1
fi
if [[ "$(jq -r 'has("error")' "${querylog_identityctl_json}")" != "false" ]]; then
  echo "FAIL: slow identityctl paths should not add a timeout error to the SQL-backed improved querylog" >&2
  exit 1
fi
if [[ "$(jq -r '.data | length' "${querylog_identityctl_json}")" == "0" ]]; then
  echo "FAIL: the SQL-backed improved querylog should still return rows when identityctl is slow" >&2
  exit 1
fi
clear_identityctl_delay

python3 - <<'PY' "${ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE}"
import sqlite3
import sys
import time

conn = sqlite3.connect(sys.argv[1])
conn.execute(
    """
    INSERT INTO app_cache (
      namespace,
      cache_key,
      payload_json,
      state,
      error_message,
      generated_at_ms,
      fresh_until_ms,
      stale_until_ms,
      retry_after_ms
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    ON CONFLICT(namespace, cache_key) DO UPDATE SET
      payload_json = excluded.payload_json,
      state = excluded.state,
      error_message = excluded.error_message,
      generated_at_ms = excluded.generated_at_ms,
      fresh_until_ms = excluded.fresh_until_ms,
      stale_until_ms = excluded.stale_until_ms,
      retry_after_ms = excluded.retry_after_ms
    """,
    (
      "unrelated:test",
      "freshness-check",
      '{"ok":true}',
      "ready",
      "",
      int(time.time() * 1000),
      int(time.time() * 1000) + 1000,
      int(time.time() * 1000) + 2000,
      0,
    ),
)
conn.commit()
conn.close()
PY
wait_for_ttl_expiry "${querylog_result_cache_ttl_seconds}" "${shared_lookup_cache_ttl_seconds}"
querylog_unrelated_sql_json="${tmpdir}/querylog-unrelated-sql.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/querylog?search=&response_status=all&older_than=&limit=20" > "${querylog_unrelated_sql_json}"
if [[ "$(jq -r '.pixelMeta.cacheState' "${querylog_unrelated_sql_json}")" != "fresh" ]]; then
  echo "FAIL: unrelated SQLite writes should not make the improved querylog go stale" >&2
  exit 1
fi
if [[ "$(jq -r '.pixelMeta.stale' "${querylog_unrelated_sql_json}")" != "false" ]]; then
  echo "FAIL: unrelated SQLite writes should not mark the improved querylog as stale" >&2
  exit 1
fi
if [[ "$(jq -r '.retryable // "false"' "${querylog_unrelated_sql_json}")" != "false" ]]; then
  echo "FAIL: unrelated SQLite writes should not mark the improved querylog as retryable" >&2
  exit 1
fi
if [[ "$(jq -r 'has("error")' "${querylog_unrelated_sql_json}")" != "false" ]]; then
  echo "FAIL: unrelated SQLite writes should not add an error to the improved querylog" >&2
  exit 1
fi
if [[ "$(jq -r '.data | length' "${querylog_unrelated_sql_json}")" == "0" ]]; then
  echo "FAIL: unrelated SQLite writes should not blank the improved querylog row set" >&2
  exit 1
fi
wait_for_ttl_expiry "1.0"

revoke_last_body="${tmpdir}/revoke-last-body.json"
revoke_last_code="$(curl -sS -o "${revoke_last_body}" -w '%{http_code}' \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -X DELETE \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/alpha")"
if [[ "${revoke_last_code}" != "400" ]]; then
  echo "FAIL: revoking last identity should return 400" >&2
  exit 1
fi
if ! jq -r '.error' "${revoke_last_body}" | rg -Fq 'Refusing to revoke the last identity'; then
  echo "FAIL: last-identity revoke error message mismatch" >&2
  exit 1
fi

future_expiry="$(( $(date +%s) + 7200 ))"
create_beta_json="${tmpdir}/create-beta.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d "{\"id\":\"beta\",\"expiresEpochSeconds\":${future_expiry}}" \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${create_beta_json}"
beta_token="$(jq -r '.token' "${create_beta_json}")"
beta_dot_label="$(jq -r '.dotLabel' "${create_beta_json}")"
if [[ "$(jq -r '.applied' "${create_beta_json}")" != "true" ]]; then
  echo "FAIL: create(beta) should report applied=true when runtime reload is scheduled" >&2
  exit 1
fi
if [[ "$(jq -r '.expiresEpochSeconds' "${create_beta_json}")" != "${future_expiry}" ]]; then
  echo "FAIL: create with expiresEpochSeconds should echo persisted expiry value" >&2
  exit 1
fi

create_gamma_json="${tmpdir}/create-gamma.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"id":"gamma","expiresEpochSeconds":null}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${create_gamma_json}"
if [[ "$(jq -r '.expiresEpochSeconds' "${create_gamma_json}")" != "null" ]]; then
  echo "FAIL: create with explicit null expiry should persist as no-expiry" >&2
  exit 1
fi

list_after_expiry_creates="${tmpdir}/list-after-expiry-creates.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${list_after_expiry_creates}"
if [[ "$(jq -r '.identities[] | select(.id == "beta") | .expiresEpochSeconds' "${list_after_expiry_creates}")" != "${future_expiry}" ]]; then
  echo "FAIL: list endpoint beta expiresEpochSeconds mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[] | select(.id == "beta") | .isExpired' "${list_after_expiry_creates}")" != "false" ]]; then
  echo "FAIL: beta should not be marked expired immediately after creation" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[] | select(.id == "gamma") | .expiresEpochSeconds' "${list_after_expiry_creates}")" != "null" ]]; then
  echo "FAIL: gamma should expose null expiresEpochSeconds in list response" >&2
  exit 1
fi

past_expiry_body="${tmpdir}/past-expiry-body.json"
past_expiry_code="$(curl -sS -o "${past_expiry_body}" -w '%{http_code}' \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d "{\"id\":\"stale\",\"expiresEpochSeconds\":$(( $(date +%s) - 10 ))}" \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities")"
if [[ "${past_expiry_code}" != "400" ]]; then
  echo "FAIL: create with past expiresEpochSeconds should return 400" >&2
  exit 1
fi
if ! jq -r '.error' "${past_expiry_body}" | rg -Fq 'must be in the future'; then
  echo "FAIL: create with past expiry should return validation error" >&2
  exit 1
fi

revoke_alpha_json="${tmpdir}/revoke-alpha.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -X DELETE \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/alpha" > "${revoke_alpha_json}"
if [[ "$(jq -r '.revoked' "${revoke_alpha_json}")" != "alpha" ]]; then
  echo "FAIL: revoke response revoked id mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.remaining' "${revoke_alpha_json}")" != "2" ]]; then
  echo "FAIL: revoke remaining count mismatch" >&2
  exit 1
fi
if [[ "$(jq -r '.applied' "${revoke_alpha_json}")" != "true" ]]; then
  echo "FAIL: revoke should report applied=true when runtime reload is scheduled" >&2
  exit 1
fi

revoke_gamma_json="${tmpdir}/revoke-gamma.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -X DELETE \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/gamma" > "${revoke_gamma_json}"
if [[ "$(jq -r '.revoked' "${revoke_gamma_json}")" != "gamma" ]]; then
  echo "FAIL: revoke(gamma) response revoked id mismatch" >&2
  exit 1
fi

final_list_json="${tmpdir}/final-list.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${final_list_json}"
if [[ "$(jq -r '.identities | length' "${final_list_json}")" != "1" ]]; then
  echo "FAIL: final identity count should be 1" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].id' "${final_list_json}")" != "beta" ]]; then
  echo "FAIL: remaining identity should be beta" >&2
  exit 1
fi

rename_beta_json="${tmpdir}/rename-beta.json"
curl -fsS \
  -H "Origin: http://127.0.0.1:${port}" \
  -H "X-Forwarded-Proto: http" \
  -H 'Content-Type: application/json' \
  -X POST \
  -d '{"newId":"beta-main"}' \
  "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities/beta/rename" > "${rename_beta_json}"
if [[ "$(jq -r '.renamed' "${rename_beta_json}")" != "beta-main" ]]; then
  echo "FAIL: rename(beta) should report renamed=beta-main" >&2
  exit 1
fi
if [[ "$(jq -r '.previousId' "${rename_beta_json}")" != "beta" ]]; then
  echo "FAIL: rename(beta) should report previousId=beta" >&2
  exit 1
fi
if [[ "$(jq -r '.token' "${rename_beta_json}")" != "${beta_token}" ]]; then
  echo "FAIL: rename(beta) should preserve the existing token" >&2
  exit 1
fi
if [[ "$(jq -r '.dotLabel' "${rename_beta_json}")" != "${beta_dot_label}" ]]; then
  echo "FAIL: rename(beta) should preserve the existing DoT label" >&2
  exit 1
fi
if [[ "$(jq -r '.applied' "${rename_beta_json}")" != "true" ]]; then
  echo "FAIL: rename(beta) should report applied=true when runtime reload is scheduled" >&2
  exit 1
fi

list_after_rename_json="${tmpdir}/list-after-rename.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/identities" > "${list_after_rename_json}"
if [[ "$(jq -r '.identities[0].id' "${list_after_rename_json}")" != "beta-main" ]]; then
  echo "FAIL: list endpoint should expose the renamed identity id" >&2
  exit 1
fi
if [[ "$(jq -r '.identities[0].token' "${list_after_rename_json}")" != "${beta_token}" ]]; then
  echo "FAIL: list endpoint should preserve the renamed identity token" >&2
  exit 1
fi

usage_after_rename_json="${tmpdir}/usage-after-rename.json"
curl -fsS "http://127.0.0.1:${port}/pixel-stack/identity/api/v1/usage?identity=beta-main&window=7d" > "${usage_after_rename_json}"
if [[ "$(jq -r '.identities[] | select(.id == "beta-main") | .requestCount' "${usage_after_rename_json}")" != "2" ]]; then
  echo "FAIL: rename(beta) should move existing usage onto the renamed identity" >&2
  exit 1
fi
if jq -e '.identities[] | select(.id == "beta")' "${usage_after_rename_json}" >/dev/null 2>&1; then
  echo "FAIL: rename(beta) should not leave usage behind on the old identity id" >&2
  exit 1
fi

migration_tmp="${tmpdir}/migration-check"
mkdir -p "${migration_tmp}/state"
migration_ts_one="$(( $(date +%s) - 3600 ))"
migration_ts_two="$(( migration_ts_one + 1 ))"
cat > "${migration_tmp}/state/doh-usage-events.jsonl" <<EOF_MIGRATION_JSONL
{"ts":${migration_ts_one},"tsMs":$(( migration_ts_one * 1000 )),"identityId":"alpha","status":200,"requestTimeMs":10,"clientIp":"192.168.31.10"}
{"ts":${migration_ts_two},"tsMs":$(( migration_ts_two * 1000 )),"identityId":"beta","status":404,"requestTimeMs":20,"clientIp":""}
EOF_MIGRATION_JSONL
env \
  ADGUARDHOME_DOH_IDENTITIES_FILE="${migration_tmp}/doh-identities.json" \
  ADGUARDHOME_DOH_USAGE_EVENTS_FILE="${migration_tmp}/state/doh-usage-events.jsonl" \
  ADGUARDHOME_DOH_USAGE_CURSOR_FILE="${migration_tmp}/state/doh-usage-cursor.json" \
  ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE="${migration_tmp}/state/identity-observability.sqlite" \
  ADGUARDHOME_DOH_ACCESS_LOG_FILE="${migration_tmp}/remote-nginx-doh-access.log" \
  ADGUARDHOME_DOH_IDENTITYCTL_APPLY=0 \
  python3 "${IDENTITY_HELPER}" usage --json --all --window 7d > "${migration_tmp}/usage.json"
if [[ "$(jq -r '.totalRequests' "${migration_tmp}/usage.json")" != "2" ]]; then
  echo "FAIL: identity helper should import first-run JSONL events into SQLite" >&2
  exit 1
fi
if [[ "$(python3 -c "import sqlite3, sys; conn = sqlite3.connect(sys.argv[1]); print(conn.execute('select count(*) from doh_events').fetchone()[0]); conn.close()" "${migration_tmp}/state/identity-observability.sqlite")" != "2" ]]; then
  echo "FAIL: SQLite migration import should persist JSONL events" >&2
  exit 1
fi

cursor_tmp="${tmpdir}/cursor-check"
mkdir -p "${cursor_tmp}/state"
cursor_created_epoch="$(( $(date +%s) - 5400 ))"
cursor_log_one_epoch="$(( cursor_created_epoch + 3600 ))"
cursor_log_two_epoch="$(( cursor_log_one_epoch + 1 ))"
cursor_log_one_iso="$(python3 - <<'PY' "${cursor_log_one_epoch}"
from datetime import datetime, timezone
import sys
print(datetime.fromtimestamp(int(sys.argv[1]), tz=timezone.utc).isoformat())
PY
)"
cursor_log_two_iso="$(python3 - <<'PY' "${cursor_log_two_epoch}"
from datetime import datetime, timezone
import sys
print(datetime.fromtimestamp(int(sys.argv[1]), tz=timezone.utc).isoformat())
PY
)"
cat > "${cursor_tmp}/doh-identities.json" <<EOF_CURSOR_STORE
{
  "schema": 1,
  "primaryIdentityId": "alpha",
  "identities": [
    {
      "id": "alpha",
      "token": "AlphaTokenAlphaToken1",
      "dotLabel": null,
      "createdEpochSeconds": ${cursor_created_epoch},
      "expiresEpochSeconds": null
    }
  ]
}
EOF_CURSOR_STORE
cat > "${cursor_tmp}/remote-nginx-doh-access.log" <<EOF_CURSOR_LOG_ONE
${cursor_log_one_iso}	/AlphaTokenAlphaToken1/dns-query?dns=a	200	0.010	192.168.31.20	${cursor_log_one_epoch}.000
EOF_CURSOR_LOG_ONE
env \
  ADGUARDHOME_DOH_IDENTITIES_FILE="${cursor_tmp}/doh-identities.json" \
  ADGUARDHOME_DOH_USAGE_EVENTS_FILE="${cursor_tmp}/state/doh-usage-events.jsonl" \
  ADGUARDHOME_DOH_USAGE_CURSOR_FILE="${cursor_tmp}/state/doh-usage-cursor.json" \
  ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE="${cursor_tmp}/state/identity-observability.sqlite" \
  ADGUARDHOME_DOH_ACCESS_LOG_FILE="${cursor_tmp}/remote-nginx-doh-access.log" \
  ADGUARDHOME_DOH_IDENTITYCTL_APPLY=0 \
  python3 "${IDENTITY_HELPER}" usage --json --all --window 7d > "${cursor_tmp}/usage-first.json"
if [[ "$(jq -r '.totalRequests' "${cursor_tmp}/usage-first.json")" != "1" ]]; then
  echo "FAIL: first-run legacy access log import should populate SQLite once" >&2
  exit 1
fi
cat >> "${cursor_tmp}/remote-nginx-doh-access.log" <<EOF_CURSOR_LOG_TWO
${cursor_log_two_iso}	/AlphaTokenAlphaToken1/dns-query?dns=b	404	0.020	192.168.31.20	${cursor_log_two_epoch}.000
EOF_CURSOR_LOG_TWO
env \
  ADGUARDHOME_DOH_IDENTITIES_FILE="${cursor_tmp}/doh-identities.json" \
  ADGUARDHOME_DOH_USAGE_EVENTS_FILE="${cursor_tmp}/state/doh-usage-events.jsonl" \
  ADGUARDHOME_DOH_USAGE_CURSOR_FILE="${cursor_tmp}/state/doh-usage-cursor.json" \
  ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE="${cursor_tmp}/state/identity-observability.sqlite" \
  ADGUARDHOME_DOH_ACCESS_LOG_FILE="${cursor_tmp}/remote-nginx-doh-access.log" \
  ADGUARDHOME_DOH_IDENTITYCTL_APPLY=0 \
  python3 "${IDENTITY_HELPER}" usage --json --all --window 7d > "${cursor_tmp}/usage-second.json"
if [[ "$(jq -r '.totalRequests' "${cursor_tmp}/usage-second.json")" != "1" ]]; then
  echo "FAIL: once SQLite is active, appending to the legacy access log should not change live usage totals" >&2
  exit 1
fi
if [[ "$(python3 -c "import sqlite3, sys; conn = sqlite3.connect(sys.argv[1]); print(conn.execute('select count(*) from doh_events').fetchone()[0]); conn.close()" "${cursor_tmp}/state/identity-observability.sqlite")" != "1" ]]; then
  echo "FAIL: once SQLite is active, the legacy access log should stay import-only instead of appending new rows" >&2
  exit 1
fi

prune_tmp="${tmpdir}/prune-check"
mkdir -p "${prune_tmp}/state"
prune_old_ts="$(( $(date +%s) - (3 * 86400) ))"
prune_new_ts="$(( $(date +%s) - 3600 ))"
cat > "${prune_tmp}/state/doh-usage-events.jsonl" <<EOF_PRUNE_JSONL
{"ts":${prune_old_ts},"tsMs":$(( prune_old_ts * 1000 )),"identityId":"alpha","status":200,"requestTimeMs":10,"clientIp":"192.168.31.30"}
{"ts":${prune_new_ts},"tsMs":$(( prune_new_ts * 1000 )),"identityId":"beta","status":200,"requestTimeMs":15,"clientIp":"192.168.31.31"}
EOF_PRUNE_JSONL
prune_jsonl_before="$(shasum "${prune_tmp}/state/doh-usage-events.jsonl" | awk '{print $1}')"
env \
  ADGUARDHOME_DOH_IDENTITIES_FILE="${prune_tmp}/doh-identities.json" \
  ADGUARDHOME_DOH_USAGE_EVENTS_FILE="${prune_tmp}/state/doh-usage-events.jsonl" \
  ADGUARDHOME_DOH_USAGE_CURSOR_FILE="${prune_tmp}/state/doh-usage-cursor.json" \
  ADGUARDHOME_DOH_OBSERVABILITY_DB_FILE="${prune_tmp}/state/identity-observability.sqlite" \
  ADGUARDHOME_DOH_ACCESS_LOG_FILE="${prune_tmp}/remote-nginx-doh-access.log" \
  ADGUARDHOME_DOH_USAGE_RETENTION_DAYS=1 \
  ADGUARDHOME_DOH_IDENTITYCTL_APPLY=0 \
  python3 "${IDENTITY_HELPER}" usage --json --all --window 7d > "${prune_tmp}/usage.json"
prune_jsonl_after="$(shasum "${prune_tmp}/state/doh-usage-events.jsonl" | awk '{print $1}')"
if [[ "${prune_jsonl_before}" != "${prune_jsonl_after}" ]]; then
  echo "FAIL: retention pruning should not rewrite the migration JSONL file" >&2
  exit 1
fi
if [[ "$(python3 -c "import sqlite3, sys; conn = sqlite3.connect(sys.argv[1]); print(conn.execute('select count(*) from doh_events').fetchone()[0]); conn.close()" "${prune_tmp}/state/identity-observability.sqlite")" != "1" ]]; then
  echo "FAIL: retention pruning should remove expired rows from SQLite without touching the JSONL migration input" >&2
  exit 1
fi

python3 - <<'PY' "${WEB_HELPER}"
import importlib.util
import os
import sys
from datetime import datetime
from zoneinfo import ZoneInfo

web_helper = sys.argv[1]
spec = importlib.util.spec_from_file_location("identity_web_helper_schedule", web_helper)
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)

tz = ZoneInfo("Europe/Riga")
cases = [
    (
        datetime(2026, 4, 5, 3, 30, tzinfo=tz),
        "2026-04-06T04:00:00+03:00",
    ),
    (
        datetime(2026, 4, 6, 4, 0, tzinfo=tz),
        "2026-04-13T04:00:00+03:00",
    ),
    (
        datetime(2026, 10, 25, 3, 30, tzinfo=tz),
        "2026-10-26T04:00:00+02:00",
    ),
]

for now, expected in cases:
    actual = module._next_weekly_compaction_run(now, weekday=1, hour=4, minute=0).isoformat()
    if actual != expected:
        raise SystemExit(f"unexpected weekly compaction run calculation: {now.isoformat()} -> {actual} (expected {expected})")

os.environ["TZ"] = "Europe/Riga"
os.environ["ADGUARDHOME_DOH_IDENTITY_WEB_COMPACTION_WEEKDAY"] = "1"
os.environ["ADGUARDHOME_DOH_IDENTITY_WEB_COMPACTION_TIME"] = "04:00"
env_actual = module._next_compaction_run_from_env(datetime(2026, 3, 29, 3, 30, tzinfo=tz)).isoformat()
if env_actual != "2026-03-30T04:00:00+03:00":
    raise SystemExit(f"unexpected env-driven compaction run calculation: {env_actual}")
PY

sleep 2
reload_count="$(wc -l < "${reload_log}" | tr -d '[:space:]')"
if [[ -z "${reload_count}" || "${reload_count}" -lt 3 ]]; then
  echo "FAIL: expected runtime reload entrypoint to be invoked for create/revoke operations" >&2
  exit 1
fi

echo "PASS: DoH identity web sidecar list/create/rename/revoke/usage contracts are correct"
