#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE_MANIFEST="${REPO_ROOT}/tools/arbuzas-rs/Cargo.toml"

for cmd in cargo curl jq python3 openssl rg; do
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "FAIL: ${cmd} is required" >&2
    exit 1
  fi
done

cargo build --manifest-path "${WORKSPACE_MANIFEST}" -p arbuzas-dns >/dev/null
ARB_BINARY="${REPO_ROOT}/tools/arbuzas-rs/target/debug/arbuzas-dns"
if [[ ! -x "${ARB_BINARY}" ]]; then
  echo "FAIL: missing arbuzas-dns binary at ${ARB_BINARY}" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
controlplane_pid=""
cleanup() {
  if [[ -n "${controlplane_pid}" ]] && kill -0 "${controlplane_pid}" >/dev/null 2>&1; then
    kill "${controlplane_pid}" >/dev/null 2>&1 || true
    wait "${controlplane_pid}" 2>/dev/null || true
  fi
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

controlplane_port="$(free_port)"
dns_port="$(free_port)"
https_port="$(free_port)"
dot_port="$(free_port)"

etc_dir="${tmpdir}/etc"
state_dir="${tmpdir}/state"
runtime_dir="${tmpdir}/runtime"
run_dir="${tmpdir}/run"
mkdir -p "${etc_dir}" "${state_dir}" "${runtime_dir}" "${run_dir}"

runtime_env="${etc_dir}/runtime.env"
source_config="${etc_dir}/arbuzas-dns.yaml"
controlplane_db="${state_dir}/controlplane.sqlite"
legacy_observability_db="${state_dir}/identity-observability.sqlite"
admin_password_file="${etc_dir}/admin-password"
default_filter_file="${tmpdir}/default-filter.txt"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=dns.example.test' \
  -keyout "${etc_dir}/privkey.pem" \
  -out "${etc_dir}/fullchain.pem" >/dev/null 2>&1

cat > "${runtime_env}" <<EOF_ENV
ARBUZAS_DNS_HOSTNAME=dns.example.test
ARBUZAS_DNS_DOT_HOSTNAME=dns.example.test
ARBUZAS_DNS_BIND_HOST=127.0.0.1
ARBUZAS_DNS_PORT=${dns_port}
ARBUZAS_DNS_CONTROLPLANE_HOST=127.0.0.1
ARBUZAS_DNS_CONTROLPLANE_PORT=${controlplane_port}
ARBUZAS_DNS_HTTPS_PORT=${https_port}
ARBUZAS_DNS_DOT_PORT=${dot_port}
ARBUZAS_DNS_TLS_CERT_FILE=${etc_dir}/fullchain.pem
ARBUZAS_DNS_TLS_KEY_FILE=${etc_dir}/privkey.pem
EOF_ENV

printf 'super-secret-password\n' > "${admin_password_file}"
cat > "${default_filter_file}" <<'EOF_FILTER'
||blocked.example^
EOF_FILTER

cat > "${source_config}" <<'EOF_SOURCE'
schema_version: 1
upstreams:
  - https://resolver.example.test/dns-query
filters:
  - enabled: true
    name: Default blocklist
    url: file://__DEFAULT_FILTER_FILE__
whitelist_filters: []
user_rules:
  - '@@||allowed.example^'
EOF_SOURCE
python3 - <<'PY' "${source_config}" "${default_filter_file}"
from pathlib import Path
import sys
config_path = Path(sys.argv[1])
config_path.write_text(
    config_path.read_text(encoding="utf-8").replace(
        "__DEFAULT_FILTER_FILE__", Path(sys.argv[2]).as_posix()
    ),
    encoding="utf-8",
)
PY

env \
  ARBUZAS_DNS_RUNTIME_ENV_FILE="${runtime_env}" \
  ARBUZAS_DNS_DIR="${etc_dir}" \
  ARBUZAS_DNS_STATE_DIR="${state_dir}" \
  ARBUZAS_DNS_RUNTIME_DIR="${runtime_dir}" \
  ARBUZAS_DNS_RUN_DIR="${run_dir}" \
  ARBUZAS_DNS_CONTROLPLANE_DB_FILE="${controlplane_db}" \
  ARBUZAS_DNS_IDENTITIES_FILE="${etc_dir}/doh-identities.json" \
  ARBUZAS_DNS_QUERYLOG_VIEW_PREFERENCE_FILE="${state_dir}/querylog-view-preference.json" \
  ARBUZAS_DNS_LEGACY_OBSERVABILITY_DB_FILE="${legacy_observability_db}" \
  ARBUZAS_DNS_SOURCE_CONFIG_FILE="${source_config}" \
  ARBUZAS_DNS_ADMIN_USERNAME="admin" \
  ARBUZAS_DNS_ADMIN_PASSWORD_FILE="${admin_password_file}" \
  "${ARB_BINARY}" migrate --json >/dev/null

env \
  ARBUZAS_DNS_RUNTIME_ENV_FILE="${runtime_env}" \
  ARBUZAS_DNS_DIR="${etc_dir}" \
  ARBUZAS_DNS_STATE_DIR="${state_dir}" \
  ARBUZAS_DNS_RUNTIME_DIR="${runtime_dir}" \
  ARBUZAS_DNS_RUN_DIR="${run_dir}" \
  ARBUZAS_DNS_CONTROLPLANE_DB_FILE="${controlplane_db}" \
  ARBUZAS_DNS_IDENTITIES_FILE="${etc_dir}/doh-identities.json" \
  ARBUZAS_DNS_QUERYLOG_VIEW_PREFERENCE_FILE="${state_dir}/querylog-view-preference.json" \
  ARBUZAS_DNS_LEGACY_OBSERVABILITY_DB_FILE="${legacy_observability_db}" \
  ARBUZAS_DNS_SOURCE_CONFIG_FILE="${source_config}" \
  ARBUZAS_DNS_ADMIN_USERNAME="admin" \
  ARBUZAS_DNS_ADMIN_PASSWORD_FILE="${admin_password_file}" \
  "${ARB_BINARY}" serve >"${tmpdir}/serve.log" 2>&1 &
controlplane_pid=$!

base_url="http://127.0.0.1:${controlplane_port}"
host_header="Host: dns.example.test"
origin_header="Origin: https://dns.example.test"
cookie_jar="${tmpdir}/cookies.txt"

for _ in $(seq 1 50); do
  if curl -fsS -H "${host_header}" "${base_url}/dns/login" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

login_page="$(curl -fsS -H "${host_header}" "${base_url}/dns/login")"
if [[ "${login_page}" != *"DNS admin"* ]]; then
  echo "FAIL: login page did not load" >&2
  exit 1
fi

root_headers="${tmpdir}/root.headers"
curl -fsS -D "${root_headers}" -o /dev/null -H "${host_header}" "${base_url}/"
if ! rg -Fqi 'location: /dns/login' "${root_headers}"; then
  echo "FAIL: unauthenticated root did not redirect to /dns/login" >&2
  exit 1
fi

auth_state="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/session")"
echo "${auth_state}" | jq -e '.authenticated == false and .username == ""' >/dev/null

bad_status="$(curl -sS -o /dev/null -w '%{http_code}' -H "${host_header}" -H "${origin_header}" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"wrong"}' \
  "${base_url}/dns/api/session")"
if [[ "${bad_status}" != "401" ]]; then
  echo "FAIL: wrong admin password was not rejected" >&2
  exit 1
fi

login_json="$(curl -fsS -c "${cookie_jar}" -b "${cookie_jar}" -H "${host_header}" -H "${origin_header}" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"super-secret-password","returnTo":"/dns"}' \
  "${base_url}/dns/api/session")"
echo "${login_json}" | jq -e '.authenticated == true and .username == "admin" and .returnTo == "/dns"' >/dev/null

curl -fsS -D "${root_headers}" -o /dev/null -b "${cookie_jar}" -H "${host_header}" "${base_url}/"
if ! rg -Fqi 'location: /dns' "${root_headers}"; then
  echo "FAIL: authenticated root did not redirect to /dns" >&2
  exit 1
fi

assert_page() {
  local path="$1"
  local title="$2"
  local active_href="$3"
  local html
  html="$(curl -fsS -b "${cookie_jar}" -H "${host_header}" "${base_url}${path}")"
  if [[ "${html}" != *"${title}"* ]]; then
    echo "FAIL: ${path} did not render ${title}" >&2
    exit 1
  fi
  for item in \
    '/dns|Overview' \
    '/dns/settings|Settings' \
    '/dns/clients|Clients' \
    '/dns/identities|Identities' \
    '/dns/queries|Queries'
  do
    local href="${item%%|*}"
    local label="${item##*|}"
    if [[ "${html}" != *"href=\"${href}\">${label}</a>"* ]]; then
      echo "FAIL: ${path} is missing nav item ${label}" >&2
      exit 1
    fi
  done
  if [[ "${html}" != *"<a class=\"active\" href=\"${active_href}\">"* ]]; then
    echo "FAIL: ${path} did not mark ${active_href} as active" >&2
    exit 1
  fi
  if [[ "${html}" != *"<button id=\"logout\" type=\"button\">Log out</button>"* ]]; then
    echo "FAIL: ${path} is missing the shared logout button" >&2
    exit 1
  fi
}

assert_page "/dns" "DNS overview" "/dns"
assert_page "/dns/settings" "DNS settings" "/dns/settings"
assert_page "/dns/clients" "DNS clients" "/dns/clients"
assert_page "/dns/identities" "DNS identities" "/dns/identities"

query_page="$(curl -fsS -b "${cookie_jar}" -H "${host_header}" "${base_url}/dns/queries")"
if [[ "${query_page}" != *"DNS queries"* ]]; then
  echo "FAIL: /dns/queries did not render the new query page" >&2
  exit 1
fi
if [[ "${query_page}" == *"PREFETCH_PAGE_TARGET"* ]]; then
  echo "FAIL: query page still includes the removed prefetch buffer" >&2
  exit 1
fi
if [[ "${query_page}" != *"/dns/api/queries"* ]]; then
  echo "FAIL: query page is not using the canonical /dns/api/queries endpoint" >&2
  exit 1
fi

stats_headers="${tmpdir}/stats.headers"
curl -fsS -D "${stats_headers}" -o /dev/null -b "${cookie_jar}" -H "${host_header}" "${base_url}/dns/stats"
if ! rg -Fqi 'location: /dns' "${stats_headers}"; then
  echo "FAIL: /dns/stats did not fold back into /dns" >&2
  exit 1
fi

settings_anchor_headers="${tmpdir}/settings-anchor.headers"
curl -fsS -D "${settings_anchor_headers}" -o /dev/null -b "${cookie_jar}" -H "${host_header}" "${base_url}/dns/filters"
if ! rg -Fqi 'location: /dns/settings#filters' "${settings_anchor_headers}"; then
  echo "FAIL: settings anchor redirect did not point at /dns/settings#filters" >&2
  exit 1
fi

for retired_path in \
  '/pixel-stack/dns' \
  '/pixel-stack/dns/overview' \
  '/pixel-stack/dns/api/v1/settings' \
  '/pixel-stack/identity' \
  '/pixel-stack/identity/settings' \
  '/pixel-stack/identity/api/v1/identities'
do
  status_code="$(curl -sS -o /dev/null -w '%{http_code}' -b "${cookie_jar}" -H "${host_header}" "${base_url}${retired_path}")"
  if [[ "${status_code}" != "410" ]]; then
    echo "FAIL: retired route ${retired_path} returned ${status_code} instead of 410" >&2
    exit 1
  fi
done

settings_json="$(curl -fsS -b "${cookie_jar}" -H "${host_header}" "${base_url}/dns/api/settings")"
echo "${settings_json}" | jq -e '.runtime.querylogDefaultView != null and .transport.hostname == "dns.example.test"' >/dev/null

query_page_json="$(curl -fsS -b "${cookie_jar}" -H "${host_header}" "${base_url}/dns/api/queries?page_size=1")"
echo "${query_page_json}" | jq -e 'has("meta") and (has("pixelMeta") | not) and .meta.hasMore == false and .data == []' >/dev/null

create_identity_json="$(curl -fsS -b "${cookie_jar}" -H "${host_header}" -H "${origin_header}" -H 'Content-Type: application/json' \
  -d '{"id":"alpha","primary":true}' \
  "${base_url}/dns/api/identities")"
echo "${create_identity_json}" | jq -e '.created == "alpha" and .primaryIdentityId == "alpha"' >/dev/null

logout_json="$(curl -fsS -X DELETE -b "${cookie_jar}" -H "${host_header}" -H "${origin_header}" "${base_url}/dns/api/session")"
echo "${logout_json}" | jq -e '.authenticated == false' >/dev/null

post_logout_status="$(curl -sS -o /dev/null -w '%{http_code}' -b "${cookie_jar}" -H "${host_header}" "${base_url}/dns/api/settings")"
if [[ "${post_logout_status}" != "401" ]]; then
  echo "FAIL: settings API remained accessible after logout" >&2
  exit 1
fi

echo "PASS: admin routes, session flow, and query page all use the simplified /dns surface"
