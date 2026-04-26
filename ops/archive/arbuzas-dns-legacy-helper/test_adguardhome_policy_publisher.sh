#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HELPER="${REPO_ROOT}/infra/adguardhome/debian/adguardhome-policy-publisher.py"

tmpdir="$(mktemp -d)"
cleanup() {
  if [[ -n "${fake_api_pid:-}" ]]; then
    kill "${fake_api_pid}" >/dev/null 2>&1 || true
    wait "${fake_api_pid}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${lock_holder_pid:-}" ]]; then
    kill "${lock_holder_pid}" >/dev/null 2>&1 || true
    wait "${lock_holder_pid}" >/dev/null 2>&1 || true
  fi
  rm -rf "${tmpdir}"
}
trap cleanup EXIT

runtime_env="${tmpdir}/runtime.env"
source_config="${tmpdir}/AdGuardHome.source.yaml"
base_config="${tmpdir}/AdGuardHome.base.yaml"
rendered_config="${tmpdir}/AdGuardHome.yaml"
admin_password="${tmpdir}/admin-password"
state_dir="${tmpdir}/state"
repo_dir="${tmpdir}/policy-repo"
bootstrap_policy="${tmpdir}/bootstrap-policy.yaml"
published_policy_snapshot="${state_dir}/policy-publisher-last-published-policy.yaml"
generated_source_snapshot="${state_dir}/policy-publisher-last-generated-source.yaml"
live_policy_snapshot="${state_dir}/policy-publisher-last-live-policy.yaml"
lock_file="${state_dir}/policy-publisher.lock"

mkdir -p "${state_dir}" "${repo_dir}"
printf 'secret-password\n' > "${admin_password}"

cat > "${source_config}" <<EOF
http:
  address: 127.0.0.1:8080
users:
  - name: pihole
    password: "\$2a\$10\$example"
dns:
  bind_hosts:
    - 0.0.0.0
  port: 53
clients:
  persistent:
    - name: Example
      ids:
        - 127.0.0.1
filtering:
  filtering_enabled: true
filters:
  - enabled: true
    url: https://initial.example/filter-1.txt
    name: Initial Filter
    id: 1
whitelist_filters: []
user_rules:
  - "||old.example^"
log:
  file: /var/log/adguardhome/adguardhome.log
schema_version: 33
EOF

cat > "${runtime_env}" <<EOF
ARBUZAS_DNS_POLICY_REPO_FETCH_URL=${repo_dir}/policy.yaml
ARBUZAS_DNS_POLICY_REPO_PUSH_URL=${repo_dir}
ARBUZAS_DNS_POLICY_REPO_BRANCH=main
ADGUARDHOME_ADMIN_USERNAME=pihole
ADGUARDHOME_ADMIN_PASSWORD_FILE=${admin_password}
EOF

git -C "${repo_dir}" init -b main >/dev/null
git -C "${repo_dir}" config user.name "Test"
git -C "${repo_dir}" config user.email "test@example.com"
git -C "${repo_dir}" config receive.denyCurrentBranch updateInstead
cat > "${repo_dir}/policy.yaml" <<'EOF'
schema_version: 1
filters:
  - enabled: true
    url: https://repo.example/filter-a.txt
    name: Repo Filter
    id: 11
whitelist_filters: []
user_rules:
  - "||repo.example^"
EOF
git -C "${repo_dir}" add policy.yaml >/dev/null
git -C "${repo_dir}" commit -m "Initial policy" >/dev/null

run_helper() {
  env \
    ARBUZAS_DNS_RUNTIME_ENV_FILE="${runtime_env}" \
    ARBUZAS_DNS_BASE_CONFIG_FILE="${base_config}" \
    ARBUZAS_DNS_SOURCE_CONFIG_FILE="${source_config}" \
    ARBUZAS_DNS_RENDERED_CONFIG_FILE="${rendered_config}" \
    ARBUZAS_DNS_POLICY_STATE_DIR="${state_dir}" \
    ARBUZAS_DNS_POLICY_LOCK_WAIT_SECONDS="0.5" \
    ARBUZAS_DNS_POLICY_STABLE_READ_ATTEMPTS="2" \
    ARBUZAS_DNS_POLICY_STABLE_READ_DELAY_SECONDS="0.05" \
    python3 "${HELPER}" "$@"
}

assert_python() {
  python3 - "$@"
}

hold_lock() {
  python3 - "${lock_file}" <<'PY' &
import fcntl
import pathlib
import sys
import time

lock_path = pathlib.Path(sys.argv[1])
lock_path.parent.mkdir(parents=True, exist_ok=True)
with lock_path.open("w", encoding="utf-8") as handle:
    fcntl.flock(handle.fileno(), fcntl.LOCK_EX)
    time.sleep(2)
PY
  lock_holder_pid=$!
  sleep 0.2
}

cat > "${rendered_config}" <<'EOF'
http:
  address: 127.0.0.1:8080
dns:
  bind_hosts:
    - 0.0.0.0
filters:
  - enabled: true
    url: https://live.example/filter-1.txt
    name: Live Filter One
    id: 101
  - enabled: false
    url: https://live.example/filter-2.txt
    name: Live Filter Two
    id: 102
whitelist_filters:
  - enabled: true
    url: https://live.example/allow-1.txt
    name: Live Allow One
    id: 201
user_rules:
  - "||ads.example^"
  - "@@||allowed.example^"
schema_version: 33
EOF

run_helper bootstrap --policy-out "${bootstrap_policy}"

assert_python "${base_config}" "${bootstrap_policy}" <<'PY'
import sys
from pathlib import Path
import yaml

base = yaml.safe_load(Path(sys.argv[1]).read_text())
policy = yaml.safe_load(Path(sys.argv[2]).read_text())

if "filters" in base or "whitelist_filters" in base or "user_rules" in base:
    raise SystemExit("bootstrap base still contains managed policy keys")

if set(policy.keys()) != {"schema_version", "filters", "whitelist_filters", "user_rules"}:
    raise SystemExit(f"unexpected bootstrap policy keys: {sorted(policy.keys())}")
PY

run_helper watch --max-iterations 1

assert_python \
  "${repo_dir}/policy.yaml" \
  "${source_config}" \
  "${state_dir}/policy-publisher-state.json" \
  "${published_policy_snapshot}" \
  "${generated_source_snapshot}" \
  "${live_policy_snapshot}" \
  "${state_dir}/policy-publisher-health.json" <<'PY'
import json
import sys
from pathlib import Path
import yaml

repo_policy = yaml.safe_load(Path(sys.argv[1]).read_text())
source_config = yaml.safe_load(Path(sys.argv[2]).read_text())
state = json.loads(Path(sys.argv[3]).read_text())
published_snapshot = yaml.safe_load(Path(sys.argv[4]).read_text())
generated_snapshot = yaml.safe_load(Path(sys.argv[5]).read_text())
live_snapshot = yaml.safe_load(Path(sys.argv[6]).read_text())
health = json.loads(Path(sys.argv[7]).read_text())

if repo_policy["filters"][0]["url"] != "https://live.example/filter-1.txt":
    raise SystemExit("watch did not publish live policy into repo")
if source_config["filters"][1]["url"] != "https://live.example/filter-2.txt":
    raise SystemExit("watch did not rewrite generated source config")
if not state.get("last_published_hash"):
    raise SystemExit("publisher state missing last_published_hash")
if state.get("last_source") != "watch":
    raise SystemExit("publisher state missing watch source marker")
if published_snapshot["filters"][0]["url"] != "https://live.example/filter-1.txt":
    raise SystemExit("published policy snapshot was not refreshed")
if generated_snapshot["filters"][1]["url"] != "https://live.example/filter-2.txt":
    raise SystemExit("generated source snapshot was not refreshed")
if live_snapshot["filters"][0]["url"] != "https://live.example/filter-1.txt":
    raise SystemExit("live policy snapshot was not refreshed")
if health.get("mode") != "clean":
    raise SystemExit(f"unexpected watch health mode: {health}")
if health.get("last_published_hash") != state.get("last_published_hash"):
    raise SystemExit("health file missing last_published_hash")
if health.get("last_stable_live_policy_hash") != state.get("last_published_hash"):
    raise SystemExit("health file missing last stable live hash")
PY

run_helper healthcheck --max-age-seconds 20

commit_count_before="$(git -C "${repo_dir}" rev-list --count HEAD)"
run_helper watch --max-iterations 1
commit_count_after="$(git -C "${repo_dir}" rev-list --count HEAD)"
if [[ "${commit_count_before}" != "${commit_count_after}" ]]; then
  echo "FAIL: debounce check created a redundant commit" >&2
  exit 1
fi

retry_source_before="$(cat "${source_config}")"
retry_repo_before="$(cat "${repo_dir}/policy.yaml")"
cat > "${rendered_config}" <<'EOF'
http:
  address: 127.0.0.1:8080
filters:
  - enabled: true
EOF

run_helper watch --max-iterations 1

assert_python "${state_dir}/policy-publisher-health.json" "${state_dir}/policy-publisher-drift.json" <<'PY'
import json
import sys
from pathlib import Path

health = json.loads(Path(sys.argv[1]).read_text())
drift = json.loads(Path(sys.argv[2]).read_text())

if health.get("mode") != "retrying":
    raise SystemExit(f"partial live config should set retrying mode: {health}")
if health.get("failure_category") != "transient-read":
    raise SystemExit(f"partial live config should be tagged transient-read: {health}")
if drift.get("active"):
    raise SystemExit("partial live config should not activate drift")
PY

if [[ "$(cat "${source_config}")" != "${retry_source_before}" ]]; then
  echo "FAIL: partial live config should not rewrite generated source config" >&2
  exit 1
fi
if [[ "$(cat "${repo_dir}/policy.yaml")" != "${retry_repo_before}" ]]; then
  echo "FAIL: partial live config should not publish a new repo policy" >&2
  exit 1
fi

cat > "${rendered_config}" <<'EOF'
http:
  address: 127.0.0.1:8080
dns:
  bind_hosts:
    - 0.0.0.0
filters:
  - enabled: true
    url: https://stable.example/filter-1.txt
    name: Stable Filter One
    id: 401
whitelist_filters: []
user_rules:
  - "||stable.example^"
schema_version: 33
EOF

run_helper watch --max-iterations 1

assert_python "${repo_dir}/policy.yaml" "${source_config}" "${state_dir}/policy-publisher-health.json" <<'PY'
import json
import sys
from pathlib import Path
import yaml

repo_policy = yaml.safe_load(Path(sys.argv[1]).read_text())
source_config = yaml.safe_load(Path(sys.argv[2]).read_text())
health = json.loads(Path(sys.argv[3]).read_text())

if repo_policy["filters"][0]["url"] != "https://stable.example/filter-1.txt":
    raise SystemExit("stable live config did not publish after transient retry")
if source_config["filters"][0]["url"] != "https://stable.example/filter-1.txt":
    raise SystemExit("stable live config did not rewrite generated source config")
if health.get("mode") != "clean":
    raise SystemExit(f"stable live config should return to clean mode: {health}")
PY

lock_source_before="$(cat "${source_config}")"
hold_lock
if run_helper deploy-sync; then
  echo "FAIL: deploy-sync should fail when the mutation lock is held" >&2
  exit 1
fi
wait "${lock_holder_pid}" >/dev/null 2>&1 || true
unset lock_holder_pid
if [[ "$(cat "${source_config}")" != "${lock_source_before}" ]]; then
  echo "FAIL: lock contention should not rewrite generated source config" >&2
  exit 1
fi

publish_commit_before="$(git -C "${repo_dir}" rev-list --count HEAD)"
hold_lock
if run_helper publish-current; then
  echo "FAIL: publish-current should fail when the mutation lock is held" >&2
  exit 1
fi
wait "${lock_holder_pid}" >/dev/null 2>&1 || true
unset lock_holder_pid
publish_commit_after="$(git -C "${repo_dir}" rev-list --count HEAD)"
if [[ "${publish_commit_before}" != "${publish_commit_after}" ]]; then
  echo "FAIL: lock contention should not publish a new commit" >&2
  exit 1
fi

{
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${admin_password}"
} > "${runtime_env}"
if run_helper deploy-sync; then
  echo "FAIL: deploy-sync should fail fast when the fetch URL is missing" >&2
  exit 1
fi
assert_python "${state_dir}/policy-publisher-health.json" <<'PY'
import json
import sys
from pathlib import Path

health = json.loads(Path(sys.argv[1]).read_text())
if health.get("mode") != "error" or health.get("failure_category") != "config":
    raise SystemExit(f"missing fetch URL should produce a config error: {health}")
PY

{
  printf 'ARBUZAS_DNS_POLICY_REPO_FETCH_URL=%s/policy.yaml\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${admin_password}"
} > "${runtime_env}"
if run_helper publish-current; then
  echo "FAIL: publish-current should fail fast when the push URL is missing" >&2
  exit 1
fi
assert_python "${state_dir}/policy-publisher-health.json" <<'PY'
import json
import sys
from pathlib import Path

health = json.loads(Path(sys.argv[1]).read_text())
if health.get("mode") != "error" or health.get("failure_category") != "config":
    raise SystemExit(f"missing push URL should produce a config error: {health}")
PY

{
  printf 'ARBUZAS_DNS_POLICY_REPO_FETCH_URL=%s/policy.yaml\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_PUSH_URL=%s\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${admin_password}"
} > "${runtime_env}"

cat > "${repo_dir}/policy.yaml" <<'EOF'
schema_version: 1
filters:
  - enabled: true
    url: https://deploy.example/filter-1.txt
    name: Deploy Filter
    id: 451
whitelist_filters: []
user_rules:
  - "||deploy.example^"
EOF
git -C "${repo_dir}" add policy.yaml >/dev/null
git -C "${repo_dir}" commit -m "Deploy sync policy" >/dev/null

run_helper deploy-sync

assert_python "${source_config}" "${published_policy_snapshot}" "${generated_source_snapshot}" "${state_dir}/policy-publisher-state.json" <<'PY'
import json
import sys
from pathlib import Path
import yaml

source_config = yaml.safe_load(Path(sys.argv[1]).read_text())
published_snapshot = yaml.safe_load(Path(sys.argv[2]).read_text())
generated_snapshot = yaml.safe_load(Path(sys.argv[3]).read_text())
state = json.loads(Path(sys.argv[4]).read_text())

if source_config["filters"][0]["url"] != "https://deploy.example/filter-1.txt":
    raise SystemExit("deploy-sync did not rewrite generated source config")
if published_snapshot["filters"][0]["url"] != "https://deploy.example/filter-1.txt":
    raise SystemExit("deploy-sync did not refresh published policy snapshot")
if generated_snapshot["filters"][0]["url"] != "https://deploy.example/filter-1.txt":
    raise SystemExit("deploy-sync did not refresh generated source snapshot")
if state.get("last_source") != "deploy-sync":
    raise SystemExit("deploy-sync did not refresh publisher state")
PY

base_backup="${tmpdir}/AdGuardHome.base.valid.yaml"
cp "${base_config}" "${base_backup}"
printf 'invalid: [\n' > "${base_config}"
before_invalid_base_sync="$(cat "${source_config}")"
if run_helper deploy-sync; then
  echo "FAIL: deploy-sync should fail fast when the base config is invalid" >&2
  exit 1
fi
if [[ "$(cat "${source_config}")" != "${before_invalid_base_sync}" ]]; then
  echo "FAIL: invalid base config should not rewrite generated source config" >&2
  exit 1
fi
assert_python "${state_dir}/policy-publisher-health.json" <<'PY'
import json
import sys
from pathlib import Path

health = json.loads(Path(sys.argv[1]).read_text())
if health.get("mode") != "error" or health.get("failure_category") != "config":
    raise SystemExit(f"invalid base config should produce a config error: {health}")
PY
cp "${base_backup}" "${base_config}"

published_source_copy="${tmpdir}/published-source.yaml"
cp "${source_config}" "${published_source_copy}"

cat > "${rendered_config}" <<'EOF'
http:
  address: 127.0.0.1:8080
dns:
  bind_hosts:
    - 0.0.0.0
filters:
  - enabled: true
    url: https://drift.example/filter-1.txt
    name: Drift Filter
    id: 301
whitelist_filters: []
user_rules:
  - "||drift.example^"
schema_version: 33
EOF

invalid_push_repo="${tmpdir}/invalid-policy-repo"
mkdir -p "${invalid_push_repo}"
{
  printf 'ARBUZAS_DNS_POLICY_REPO_FETCH_URL=%s/policy.yaml\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_PUSH_URL=%s\n' "${invalid_push_repo}"
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${admin_password}"
} > "${runtime_env}"

run_helper watch --max-iterations 1

assert_python "${state_dir}/policy-publisher-drift.json" "${source_config}" "${published_source_copy}" <<'PY'
import json
import sys
from pathlib import Path

drift = json.loads(Path(sys.argv[1]).read_text())
source_now = Path(sys.argv[2]).read_text()
source_before = Path(sys.argv[3]).read_text()

if not drift.get("active"):
    raise SystemExit("push failure did not activate drift freeze")
if source_now != source_before:
    raise SystemExit("push failure should not rewrite generated source config")
PY

if run_helper healthcheck --max-age-seconds 20; then
  echo "FAIL: healthcheck should fail while drift freeze is active" >&2
  exit 1
fi

if run_helper deploy-sync; then
  echo "FAIL: deploy-sync should fail while drift freeze is active" >&2
  exit 1
fi

{
  printf 'ARBUZAS_DNS_POLICY_REPO_FETCH_URL=%s/policy.yaml\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_PUSH_URL=%s\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${admin_password}"
} > "${runtime_env}"

run_helper publish-current

assert_python "${state_dir}/policy-publisher-drift.json" "${repo_dir}/policy.yaml" "${source_config}" <<'PY'
import json
import sys
from pathlib import Path
import yaml

drift = json.loads(Path(sys.argv[1]).read_text())
repo_policy = yaml.safe_load(Path(sys.argv[2]).read_text())
source_config = yaml.safe_load(Path(sys.argv[3]).read_text())

if drift.get("active"):
    raise SystemExit("publish-current did not clear drift")
if repo_policy["filters"][0]["url"] != "https://drift.example/filter-1.txt":
    raise SystemExit("publish-current did not keep the drifted live policy")
if source_config["filters"][0]["url"] != "https://drift.example/filter-1.txt":
    raise SystemExit("publish-current did not rewrite generated source config")
PY

cat > "${repo_dir}/policy.yaml" <<'EOF'
schema_version: 1
filters:
  - enabled: true
    url: https://restore.example/filter-1.txt
    name: Restore Filter
    id: 501
whitelist_filters:
  - enabled: true
    url: https://restore.example/allow-1.txt
    name: Restore Allow
    id: 601
user_rules:
  - "||restore.example^"
  - "@@||keep.example^"
EOF
git -C "${repo_dir}" add policy.yaml >/dev/null
git -C "${repo_dir}" commit -m "Restore policy" >/dev/null

server_state="${tmpdir}/fake-adguard-state.json"
cat > "${server_state}" <<'EOF'
{
  "next_id": 700,
  "filters": [
    {"enabled": true, "url": "https://old-live.example/filter-a.txt", "name": "Old Live A", "id": 701}
  ],
  "whitelist_filters": [
    {"enabled": true, "url": "https://old-live.example/allow-a.txt", "name": "Old Allow A", "id": 801}
  ],
  "user_rules": ["||old-live.example^"]
}
EOF

fake_api_script="${tmpdir}/fake_adguard_api.py"
cat > "${fake_api_script}" <<'PY'
#!/usr/bin/env python3
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

state_path = Path(sys.argv[1])
port = int(sys.argv[2])

def load_state():
    return json.loads(state_path.read_text())

def save_state(payload):
    state_path.write_text(json.dumps(payload, indent=2))

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        return

    def _json_response(self, payload):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_json(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length) if length else b"{}"
        return json.loads(raw.decode("utf-8") or "{}")

    def do_POST(self):
        payload = self._read_json()
        state = load_state()

        if self.path == "/control/login":
            self._json_response({})
            return

        if self.path == "/control/filtering/remove_url":
            key = "whitelist_filters" if payload.get("whitelist") else "filters"
            state[key] = [item for item in state[key] if item["url"] != payload.get("url")]
            save_state(state)
            self._json_response({})
            return

        if self.path == "/control/filtering/add_url":
            key = "whitelist_filters" if payload.get("whitelist") else "filters"
            state["next_id"] += 1
            state[key].append(
                {
                    "enabled": True,
                    "url": payload["url"],
                    "name": payload["name"],
                    "id": state["next_id"],
                }
            )
            save_state(state)
            self._json_response({})
            return

        if self.path == "/control/filtering/set_url":
            key = "whitelist_filters" if payload.get("whitelist") else "filters"
            for item in state[key]:
                if item["url"] == payload.get("url"):
                    item.update(payload.get("data", {}))
                    break
            save_state(state)
            self._json_response({})
            return

        if self.path == "/control/filtering/refresh":
            self._json_response({"updated": 1})
            return

        if self.path == "/control/filtering/set_rules":
            state["user_rules"] = payload.get("rules", [])
            save_state(state)
            self._json_response({})
            return

        self.send_error(404)

    def do_GET(self):
        state = load_state()
        if self.path == "/control/filtering/status":
            self._json_response(
                {
                    "enabled": True,
                    "interval": 24,
                    "filters": state["filters"],
                    "whitelist_filters": state["whitelist_filters"],
                    "user_rules": state["user_rules"],
                }
            )
            return
        self.send_error(404)

ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY
chmod 0755 "${fake_api_script}"

fake_api_port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
python3 "${fake_api_script}" "${server_state}" "${fake_api_port}" &
fake_api_pid=$!
sleep 1

env \
  ARBUZAS_DNS_RUNTIME_ENV_FILE="${runtime_env}" \
  ARBUZAS_DNS_BASE_CONFIG_FILE="${base_config}" \
  ARBUZAS_DNS_SOURCE_CONFIG_FILE="${source_config}" \
  ARBUZAS_DNS_RENDERED_CONFIG_FILE="${rendered_config}" \
  ARBUZAS_DNS_POLICY_STATE_DIR="${state_dir}" \
  PIHOLE_WEB_HOST="127.0.0.1" \
  PIHOLE_WEB_PORT="${fake_api_port}" \
  python3 "${HELPER}" restore-published

assert_python "${server_state}" "${source_config}" "${state_dir}/policy-publisher-drift.json" <<'PY'
import json
import sys
from pathlib import Path
import yaml

server_state = json.loads(Path(sys.argv[1]).read_text())
source_config = yaml.safe_load(Path(sys.argv[2]).read_text())
drift = json.loads(Path(sys.argv[3]).read_text())

if drift.get("active"):
    raise SystemExit("restore-published should clear drift")

filters = server_state["filters"]
whitelist = server_state["whitelist_filters"]
rules = server_state["user_rules"]

if [item["url"] for item in filters] != ["https://restore.example/filter-1.txt"]:
    raise SystemExit(f"restore-published did not replace live filters: {filters}")
if [item["url"] for item in whitelist] != ["https://restore.example/allow-1.txt"]:
    raise SystemExit(f"restore-published did not replace live whitelist filters: {whitelist}")
if rules != ["||restore.example^", "@@||keep.example^"]:
    raise SystemExit(f"restore-published did not replace live user rules: {rules}")
if source_config["filters"][0]["url"] != "https://restore.example/filter-1.txt":
    raise SystemExit("restore-published did not rewrite generated source config")
PY

missing_admin_password="${tmpdir}/missing-admin-password"
{
  printf 'ARBUZAS_DNS_POLICY_REPO_FETCH_URL=%s/policy.yaml\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_PUSH_URL=%s\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${missing_admin_password}"
} > "${runtime_env}"
if env \
  ARBUZAS_DNS_RUNTIME_ENV_FILE="${runtime_env}" \
  ARBUZAS_DNS_BASE_CONFIG_FILE="${base_config}" \
  ARBUZAS_DNS_SOURCE_CONFIG_FILE="${source_config}" \
  ARBUZAS_DNS_RENDERED_CONFIG_FILE="${rendered_config}" \
  ARBUZAS_DNS_POLICY_STATE_DIR="${state_dir}" \
  PIHOLE_WEB_HOST="127.0.0.1" \
  PIHOLE_WEB_PORT="${fake_api_port}" \
  ARBUZAS_DNS_POLICY_LOCK_WAIT_SECONDS="0.5" \
  ARBUZAS_DNS_POLICY_STABLE_READ_ATTEMPTS="2" \
  ARBUZAS_DNS_POLICY_STABLE_READ_DELAY_SECONDS="0.05" \
  python3 "${HELPER}" restore-published; then
  echo "FAIL: restore-published should fail fast when the admin password file is missing" >&2
  exit 1
fi
assert_python "${state_dir}/policy-publisher-health.json" <<'PY'
import json
import sys
from pathlib import Path

health = json.loads(Path(sys.argv[1]).read_text())
if health.get("mode") != "error" or health.get("failure_category") != "config":
    raise SystemExit(f"missing admin password should produce a config error: {health}")
PY

{
  printf 'ARBUZAS_DNS_POLICY_REPO_FETCH_URL=%s/policy.yaml\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_PUSH_URL=%s\n' "${repo_dir}"
  printf 'ARBUZAS_DNS_POLICY_REPO_BRANCH=main\n'
  printf 'ADGUARDHOME_ADMIN_USERNAME=pihole\n'
  printf 'ADGUARDHOME_ADMIN_PASSWORD_FILE=%s\n' "${admin_password}"
} > "${runtime_env}"

cat > "${repo_dir}/policy.yaml" <<'EOF'
schema_version: 1
filters: []
whitelist_filters: []
user_rules: []
unexpected_key: true
EOF

before_invalid_sync="$(cat "${source_config}")"
if run_helper deploy-sync; then
  echo "FAIL: deploy-sync should reject policy files with unknown top-level keys" >&2
  exit 1
fi
after_invalid_sync="$(cat "${source_config}")"
if [[ "${before_invalid_sync}" != "${after_invalid_sync}" ]]; then
  echo "FAIL: invalid policy should not rewrite the generated source config" >&2
  exit 1
fi

echo "PASS: AdGuard policy publisher helper handles bootstrap, publish, drift, recovery, and validation"
