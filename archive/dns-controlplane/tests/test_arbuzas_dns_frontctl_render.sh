#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE_MANIFEST="${REPO_ROOT}/tools/arbuzas-rs/Cargo.toml"

tmpdir="$(mktemp -d)"
server_pid=""
cleanup() {
  if [[ -n "${server_pid}" ]] && kill -0 "${server_pid}" >/dev/null 2>&1; then
    kill "${server_pid}" >/dev/null 2>&1 || true
    wait "${server_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

mkdir -p "${tmpdir}/etc" "${tmpdir}/state" "${tmpdir}/runtime" "${tmpdir}/run"

openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
  -subj '/CN=dns.example.test' \
  -keyout "${tmpdir}/etc/privkey.pem" \
  -out "${tmpdir}/etc/fullchain.pem" >/dev/null 2>&1

cat > "${tmpdir}/etc/arbuzas-dns.yaml" <<'EOF'
schema_version: 1
upstreams:
  - https://resolver.example.test/dns-query
filters: []
whitelist_filters: []
user_rules:
  - "||blocked.example^"
EOF

cat > "${tmpdir}/runtime.env" <<EOF
ARBUZAS_DNS_DIR=${tmpdir}/etc
ARBUZAS_DNS_STATE_DIR=${tmpdir}/state
ARBUZAS_DNS_RUNTIME_DIR=${tmpdir}/runtime
ARBUZAS_DNS_RUN_DIR=${tmpdir}/run
ARBUZAS_DNS_CONTROLPLANE_DB_FILE=${tmpdir}/state/controlplane.sqlite
ARBUZAS_DNS_SOURCE_CONFIG_FILE=${tmpdir}/etc/arbuzas-dns.yaml
ARBUZAS_DNS_COMPILED_POLICY_FILE=${tmpdir}/runtime/compiled-policy.json
ARBUZAS_DNS_BIND_HOST=127.0.0.1
ARBUZAS_DNS_PORT=53053
ARBUZAS_DNS_CONTROLPLANE_HOST=127.0.0.1
ARBUZAS_DNS_CONTROLPLANE_PORT=58097
ARBUZAS_DNS_HTTPS_PORT=5443
ARBUZAS_DNS_DOT_PORT=5853
ARBUZAS_DNS_HOSTNAME=dns.example.test
ARBUZAS_DNS_DOT_HOSTNAME=dns.example.test
ARBUZAS_DNS_TLS_CERT_FILE=${tmpdir}/etc/fullchain.pem
ARBUZAS_DNS_TLS_KEY_FILE=${tmpdir}/etc/privkey.pem
ARBUZAS_DNS_SKIP_SESSION_CHECK=1
EOF

ARBUZAS_DNS_RUNTIME_ENV_FILE="${tmpdir}/runtime.env" \
  cargo run --quiet --manifest-path "${WORKSPACE_MANIFEST}" --bin arbuzas-dns -- \
  serve >"${tmpdir}/serve.log" 2>&1 &
server_pid=$!

python3 - <<'PY' "${tmpdir}"
import base64
import socket
import ssl
import struct
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

tmpdir = Path(sys.argv[1])
query_name = "blocked.example"
labels = query_name.split(".")
question = b"".join(bytes([len(label)]) + label.encode("ascii") for label in labels) + b"\x00"
question += struct.pack("!HH", 1, 1)
query = struct.pack("!HHHHHH", 0x1234, 0x0100, 1, 0, 0, 0) + question

def decode_flags(response):
    flags = struct.unpack("!H", response[2:4])[0]
    rcode = flags & 0xF
    answer_count = struct.unpack("!H", response[6:8])[0]
    return rcode, answer_count

deadline = time.time() + 45
while time.time() < deadline:
    try:
        with urllib.request.urlopen(
            "http://127.0.0.1:58097/login",
            timeout=2,
        ) as response:
            if response.status == 200:
                break
    except Exception:
        time.sleep(1)
else:
    raise SystemExit("native controlplane listener did not become ready")

context = ssl._create_unverified_context()
for path in ["/", "/login", "/dns/login", "/v1/health", "/livez", "/healthz"]:
    request = urllib.request.Request(
        f"https://127.0.0.1:5443{path}",
        headers={"Host": "dns.example.test"},
    )
    try:
        with urllib.request.urlopen(request, context=context, timeout=5) as response:
            raise SystemExit(f"expected {path} to stay closed publicly, got {response.status}")
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise SystemExit(f"expected {path} to return 404 publicly, got {error.code}") from error

udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
udp.settimeout(5)
udp.sendto(query, ("127.0.0.1", 53053))
udp_response, _ = udp.recvfrom(2048)
assert decode_flags(udp_response) in {(0, 1), (3, 0)}, decode_flags(udp_response)

with socket.create_connection(("127.0.0.1", 53053), timeout=5) as stream:
    stream.sendall(struct.pack("!H", len(query)) + query)
    response_len = struct.unpack("!H", stream.recv(2))[0]
    tcp_response = stream.recv(response_len)
assert decode_flags(tcp_response) in {(0, 1), (3, 0)}, decode_flags(tcp_response)

with socket.create_connection(("127.0.0.1", 5853), timeout=5) as raw_stream:
    with context.wrap_socket(raw_stream, server_hostname="alpha.dns.example.test") as tls_stream:
        tls_stream.sendall(struct.pack("!H", len(query)) + query)
        response_len = struct.unpack("!H", tls_stream.recv(2))[0]
        dot_response = tls_stream.recv(response_len)
assert decode_flags(dot_response) in {(0, 1), (3, 0)}, decode_flags(dot_response)

doh_query = base64.urlsafe_b64encode(query).rstrip(b"=").decode("ascii")
request = urllib.request.Request(
    f"https://127.0.0.1:5443/dns-query?dns={doh_query}",
    headers={
        "Host": "dns.example.test",
        "Accept": "application/dns-message",
    },
)
with urllib.request.urlopen(request, context=context, timeout=5) as response:
    doh_response = response.read()
assert decode_flags(doh_response) in {(0, 1), (3, 0)}, decode_flags(doh_response)
PY

echo "PASS: Arbuzas native serve runtime answers UDP/TCP DNS, DoH, and DoT directly"
