#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE_MANIFEST="${REPO_ROOT}/tools/arbuzas-rs/Cargo.toml"

for cmd in cargo curl jq python3 openssl; do
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
  if [[ "${KEEP_TMP:-0}" == "1" ]]; then
    echo "KEEP_TMP=1 retaining ${tmpdir}" >&2
    return
  fi
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
ipinfo_cache="${tmpdir}/ipinfo-cache.json"
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
ARBUZAS_DNS_HTTPS_PORT=${https_port}
ARBUZAS_DNS_DOT_PORT=${dot_port}
ARBUZAS_DNS_TLS_CERT_FILE=${etc_dir}/fullchain.pem
ARBUZAS_DNS_TLS_KEY_FILE=${etc_dir}/privkey.pem
EOF_ENV

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

cat > "${ipinfo_cache}" <<'EOF_IPINFO'
{"212.3.197.32":{"cachedAtEpochSeconds":1712755200,"whois_info":{"country":"LV","orgname":"Operator Example"}}}
EOF_IPINFO

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
  "${ARB_BINARY}" migrate --json >/dev/null

python3 - <<'PY' "${controlplane_db}"
import json
import sqlite3
import sys
from datetime import datetime, timedelta, timezone

controlplane_db = sys.argv[1]
now = datetime.now(timezone.utc).replace(microsecond=0)
alpha_dt = now - timedelta(minutes=3)
beta_dt = now - timedelta(minutes=4)
alpha_time = alpha_dt.isoformat().replace("+00:00", "Z")
beta_time = beta_dt.isoformat().replace("+00:00", "Z")
alpha_ms = int(alpha_dt.timestamp() * 1000)
beta_ms = int(beta_dt.timestamp() * 1000)

rows = [
    {
        "fingerprint": "alpha-fingerprint",
        "time": alpha_time,
        "time_ms": alpha_ms,
        "identity_id": "alpha",
        "client": "212.3.197.32",
        "protocol": "doh",
        "status_raw": "NOERROR",
        "question_name": "public.example.net",
        "payload": {
            "time": alpha_time,
            "client": "212.3.197.32",
            "client_proto": "doh",
            "elapsedMs": "12",
            "status": "NOERROR",
            "question": {"name": "public.example.net", "type": "A"},
            "client_info": {"whois": {"country": "LV", "orgname": "Operator Example"}},
            "identityId": "alpha",
            "identity": {"id": "alpha", "label": "alpha"},
            "display": {"identityLabel": "alpha"},
        },
    },
    {
        "fingerprint": "beta-fingerprint",
        "time": beta_time,
        "time_ms": beta_ms,
        "identity_id": "beta",
        "client": "192.168.31.5",
        "protocol": "dot",
        "status_raw": "NOERROR",
        "question_name": "beta-dot.example.net",
        "payload": {
            "time": beta_time,
            "client": "192.168.31.5",
            "client_proto": "dot",
            "elapsedMs": "9",
            "status": "NOERROR",
            "question": {"name": "beta-dot.example.net", "type": "A"},
            "client_info": {"whois": {}},
            "identityId": "beta",
            "identity": {"id": "beta", "label": "beta"},
            "display": {"identityLabel": "beta"},
        },
    },
]

conn = sqlite3.connect(controlplane_db)
conn.execute("DELETE FROM identities")
conn.execute("DELETE FROM settings")
conn.execute("DELETE FROM querylog_mirror_meta")
conn.execute("DELETE FROM querylog_mirror_rows")
conn.execute("DELETE FROM querylog_mirror_details")
conn.execute("DELETE FROM hourly_identity_usage")
conn.execute("DELETE FROM hourly_client_usage")
conn.execute("DELETE FROM hourly_latency_usage")
conn.executemany(
    "INSERT INTO identities (identity_id, token, dot_label, created_epoch_seconds, expires_epoch_seconds, sort_order) VALUES (?, ?, ?, ?, ?, ?)",
    [
        ("alpha", "AlphaTokenValue1234", "abcdefghijklmnopqrst", 1712700000, None, 0),
        ("beta", "BetaTokenValue56789", "qrstabcdefghijklmnop", 1712700300, None, 1),
    ],
)
conn.executemany(
    "INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
    [
        ("primaryIdentityId", "alpha"),
        ("querylogDefaultView", "none"),
        ("querylogDefaultViewUpdatedAt", "2026-04-10T10:30:00Z"),
    ],
)
conn.executemany(
    "INSERT INTO querylog_mirror_meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
    [
        ("querylogMirrorGeneratedAt", now.isoformat().replace("+00:00", "Z")),
        ("querylogMirrorRowCount", str(len(rows))),
        ("querylogMirrorPageCount", "1"),
        ("querylogMirrorBootstrapState", "ready"),
        ("querylogMirrorOldestTime", beta_time),
    ],
)
for row in rows:
    payload_json = json.dumps(row["payload"], separators=(",", ":"), sort_keys=False)
    conn.execute(
        """
        INSERT INTO querylog_mirror_rows (
          row_fingerprint, row_time, row_time_ms, request_time_ms, response_code, identity_id, unmatched,
          block_category, status_raw, status_family, tokens_lc, search_text,
          client, original_client, protocol, query_name, query_type,
          enrichment_state, enrichment_version, enriched_at, last_enrichment_attempt_at,
          last_seen_generation
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            row["fingerprint"],
            row["time"],
            row["time_ms"],
            12,
            200,
            row["identity_id"],
            0,
            "",
            row["status_raw"],
            "2xx",
            "noerror",
            row["question_name"].lower(),
            row["client"],
            row["client"],
            row["protocol"],
            row["question_name"],
            "A",
            "bare",
            0,
            "",
            "",
            1,
        ),
    )
    conn.execute(
        "INSERT INTO querylog_mirror_details (row_fingerprint, row_json) VALUES (?, ?)",
        (row["fingerprint"], payload_json),
    )
conn.commit()
conn.close()
PY

env \
  ARBUZAS_DNS_RUNTIME_ENV_FILE="${runtime_env}" \
  ARBUZAS_DNS_DIR="${etc_dir}" \
  ARBUZAS_DNS_STATE_DIR="${state_dir}" \
  ARBUZAS_DNS_RUNTIME_DIR="${runtime_dir}" \
  ARBUZAS_DNS_RUN_DIR="${run_dir}" \
  ARBUZAS_DNS_CONTROLPLANE_DB_FILE="${controlplane_db}" \
  ARBUZAS_DNS_CONTROLPLANE_HOST="127.0.0.1" \
  ARBUZAS_DNS_CONTROLPLANE_PORT="${controlplane_port}" \
  ARBUZAS_DNS_IDENTITIES_FILE="${etc_dir}/doh-identities.json" \
  ARBUZAS_DNS_QUERYLOG_VIEW_PREFERENCE_FILE="${state_dir}/querylog-view-preference.json" \
  ARBUZAS_DNS_LEGACY_OBSERVABILITY_DB_FILE="${legacy_observability_db}" \
  ARBUZAS_DNS_IPINFO_CACHE_FILE="${ipinfo_cache}" \
  ARBUZAS_DNS_SOURCE_CONFIG_FILE="${source_config}" \
  ARBUZAS_DNS_SKIP_SESSION_CHECK="1" \
  ARBUZAS_DNS_HOSTNAME="dns.example.test" \
  ARBUZAS_DNS_DOT_HOSTNAME="dns.example.test" \
  "${ARB_BINARY}" serve >"${tmpdir}/controlplane.log" 2>&1 &
controlplane_pid="$!"

base_url="http://127.0.0.1:${controlplane_port}"
host_header="Host: dns.example.test"
origin_header="Origin: https://dns.example.test"

for _ in $(seq 1 50); do
  if curl -fsS -H "${host_header}" "${base_url}/dns/identities" >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

for page in \
  '/dns|DNS overview' \
  '/dns/settings|DNS settings' \
  '/dns/identities|DNS identities' \
  '/dns/queries|DNS queries'
do
  path="${page%%|*}"
  title="${page##*|}"
  html="$(curl -fsS -H "${host_header}" "${base_url}${path}")"
  if [[ "${html}" != *"${title}"* ]]; then
    echo "FAIL: ${path} did not render ${title}" >&2
    exit 1
  fi
done

for retired_path in \
  '/pixel-stack/dns' \
  '/pixel-stack/dns/identities' \
  '/pixel-stack/identity' \
  '/pixel-stack/identity/settings' \
  '/pixel-stack/identity/api/v1/querylog'
do
  status_code="$(curl -sS -o /dev/null -w '%{http_code}' -H "${host_header}" "${base_url}${retired_path}")"
  if [[ "${status_code}" != "410" ]]; then
    echo "FAIL: retired route ${retired_path} returned ${status_code} instead of 410" >&2
    exit 1
  fi
done

identities_json="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/identities")"
usage_json="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/usage?identity=all&window=7d")"
summary_json="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/queries?summary=1&limit=1000")"
querylog_json="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/queries?page_size=10")"
stats_json="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/stats?interval=24_hours")"
clients_json="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/clients")"

echo "${identities_json}" | jq -e '.primaryIdentityId == "alpha" and .dotHostnameBase == "dns.example.test" and (.identities | length) == 2' >/dev/null
echo "${usage_json}" | jq -e '.totalRequestCount >= 0' >/dev/null
echo "${summary_json}" | jq -e '.querylog_status == "ok" and .total_query_count == 2' >/dev/null
echo "${stats_json}" | jq -e '.dns_queries >= 0 and .blocked_filtering >= 0' >/dev/null
echo "${clients_json}" | jq -e 'has("auto_clients")' >/dev/null
echo "${querylog_json}" | jq -e '
  .meta.mirrorSource == "native" and
  (has("pixelMeta") | not) and
  (.data | length) == 2 and
  all(.data[]; has("rowFingerprint") and has("identityId") and has("identity") and has("display")) and
  all(.data[]; has("pixelIdentityId") | not)
' >/dev/null

first_row_fingerprint="$(echo "${querylog_json}" | jq -r '.data[0].rowFingerprint')"
first_row_detail="$(curl -fsS -H "${host_header}" "${base_url}/dns/api/queries/${first_row_fingerprint}")"
echo "${first_row_detail}" | jq -e '.rowFingerprint == "'"${first_row_fingerprint}"'" and .identity.id != null and .detailMode == "full" and (has("pixelIdentity") | not) and (has("pixelIdentityId") | not)' >/dev/null

create_json="$(curl -fsS -H "${host_header}" -H "${origin_header}" -H 'Content-Type: application/json' -d '{"id":"gamma","primary":false}' "${base_url}/dns/api/identities")"
rename_json="$(curl -fsS -H "${host_header}" -H "${origin_header}" -H 'Content-Type: application/json' -d '{"newId":"gamma-renamed"}' "${base_url}/dns/api/identities/gamma/rename")"
revoke_json="$(curl -fsS -X DELETE -H "${host_header}" -H "${origin_header}" "${base_url}/dns/api/identities/gamma-renamed")"
echo "${create_json}" | jq -e '.created == "gamma"' >/dev/null
echo "${rename_json}" | jq -e '.renamed == "gamma-renamed" and .previousId == "gamma"' >/dev/null
echo "${revoke_json}" | jq -e '.revoked == "gamma-renamed"' >/dev/null

apple_doh_profile="${tmpdir}/alpha-apple-doh.mobileconfig"
curl -fsS -D "${tmpdir}/apple-doh.headers" -H "${host_header}" "${base_url}/dns/api/identities/alpha/apple-doh.mobileconfig" > "${apple_doh_profile}"
python3 - <<'PY' "${apple_doh_profile}"
from pathlib import Path
import sys

payload = Path(sys.argv[1]).read_text(encoding="utf-8")
assert "dns.example.test" in payload
assert "AlphaTokenValue1234" in payload
assert "lv.jolkins.arbuzas.dnsidentity.alpha" in payload
assert "lv.jolkins.pixel.dnsidentity.alpha" not in payload
PY

echo "PASS: public DNS identity surface uses /dns routes, normalizes query fields, and keeps identity management working"
