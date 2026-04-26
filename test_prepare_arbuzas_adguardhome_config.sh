#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE_MANIFEST="${REPO_ROOT}/tools/arbuzas-rs/Cargo.toml"

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT
mkdir -p "${tmpdir}/etc" "${tmpdir}/state" "${tmpdir}/runtime" "${tmpdir}/run" "${tmpdir}/filters"

cat > "${tmpdir}/filters/block.txt" <<'EOF'
||ads.example^
0.0.0.0 tracker.example
EOF

cat > "${tmpdir}/filters/allow.txt" <<'EOF'
@@||allowed.example^
EOF

cat > "${tmpdir}/etc/arbuzas-dns.yaml" <<EOF
schema_version: 1
upstreams:
  - https://resolver.example.test/dns-query
filters:
  - id: 2001
    name: blocklist
    url: ${tmpdir}/filters/block.txt
    enabled: true
whitelist_filters:
  - id: 2002
    name: allowlist
    url: ${tmpdir}/filters/allow.txt
    enabled: true
user_rules:
  - "||userblocked.example^"
EOF

cat > "${tmpdir}/runtime.env" <<EOF
ARBUZAS_DNS_DIR=${tmpdir}/etc
ARBUZAS_DNS_STATE_DIR=${tmpdir}/state
ARBUZAS_DNS_RUNTIME_DIR=${tmpdir}/runtime
ARBUZAS_DNS_RUN_DIR=${tmpdir}/run
ARBUZAS_DNS_CONTROLPLANE_DB_FILE=${tmpdir}/state/controlplane.sqlite
ARBUZAS_DNS_SOURCE_CONFIG_FILE=${tmpdir}/etc/arbuzas-dns.yaml
ARBUZAS_DNS_COMPILED_POLICY_FILE=${tmpdir}/runtime/compiled-policy.json
EOF

sync_json="$(
  ARBUZAS_DNS_RUNTIME_ENV_FILE="${tmpdir}/runtime.env" \
  cargo run --quiet --manifest-path "${WORKSPACE_MANIFEST}" --bin arbuzas-dns -- \
    release sync-policy --json
)"

python3 - <<'PY' "${tmpdir}/runtime/compiled-policy.json" "${sync_json}"
import json
import sys
from pathlib import Path

compiled = json.loads(Path(sys.argv[1]).read_text())
sync_payload = json.loads(sys.argv[2])

assert compiled["upstreams"] == ["https://resolver.example.test/dns-query"], compiled["upstreams"]
assert "ads.example" in compiled["block_suffix"], compiled["block_suffix"]
assert "tracker.example" in compiled["block_exact"], compiled["block_exact"]
assert "allowed.example" in compiled["allow_suffix"], compiled["allow_suffix"]
assert "userblocked.example" in compiled["block_suffix"], compiled["block_suffix"]
assert compiled["filter_lookup"]["2001"] == "blocklist", compiled["filter_lookup"]
assert compiled["filter_lookup"]["2002"] == "allowlist", compiled["filter_lookup"]
assert sync_payload["compiledPolicyFile"].endswith("compiled-policy.json"), sync_payload
assert sync_payload["ok"] is True, sync_payload
assert sync_payload["publisher"] == "native", sync_payload
PY

echo "PASS: Arbuzas native policy compiler writes the expected compiled policy snapshot"
