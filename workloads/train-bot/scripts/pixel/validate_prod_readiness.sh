#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"

DEFAULT_ORCHESTRATOR_REPO="$(cd "$REPO_ROOT/../../orchestrator" 2>/dev/null && pwd || true)"
ORCHESTRATOR_REPO="${ORCHESTRATOR_REPO:-$DEFAULT_ORCHESTRATOR_REPO}"
DEFAULT_ORCHESTRATOR_CONFIG_FILE="${ORCHESTRATOR_REPO}/configs/orchestrator-config-v1.production.json"
ORCHESTRATOR_CONFIG_FILE="${ORCHESTRATOR_CONFIG_FILE:-$DEFAULT_ORCHESTRATOR_CONFIG_FILE}"

usage() {
  cat <<'USAGE'
Usage: validate_prod_readiness.sh [options]

Options:
  --device SERIAL      adb serial to target
  --transport MODE     transport to use (adb|ssh|auto)
  --ssh-host IP        Tailscale or SSH host/IP
  --ssh-port PORT      SSH port (default: 2222)
  -h, --help           show help
USAGE
}

while (( $# > 0 )); do
  if pixel_transport_parse_arg "$1" "${2:-}"; then
    shift "${PIXEL_TRANSPORT_PARSE_CONSUMED}"
    continue
  fi

  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

ensure_device
ensure_root
ensure_output_dirs
ensure_local_env

for cmd in npx make rg git sqlite3 curl; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "Missing required command: $cmd"
    exit 1
  fi
done

timestamp_utc="$(date -u +%Y%m%dT%H%M%SZ)"
run_marker="$REPO_ROOT/output/pixel/prod-readiness-run-$timestamp_utc.marker"
report_file="$REPO_ROOT/output/pixel/prod-readiness-$timestamp_utc.md"
security_hits_file="$REPO_ROOT/output/pixel/prod-readiness-security-hits-$timestamp_utc.log"
runtime_ps_file="$REPO_ROOT/output/pixel/runtime-processes-$timestamp_utc.log"
runtime_train_file="$REPO_ROOT/output/pixel/runtime-train-bot-$timestamp_utc.log"
runtime_loop_file="$REPO_ROOT/output/pixel/runtime-service-loop-$timestamp_utc.log"
runtime_tunnel_file="$REPO_ROOT/output/pixel/runtime-train-bot-cloudflared-$timestamp_utc.log"
stability_file="$REPO_ROOT/output/pixel/runtime-stability-$timestamp_utc.log"
runtime_asset_freshness_file="$REPO_ROOT/output/pixel/runtime-asset-freshness-$timestamp_utc.log"
baseline_health_file="$REPO_ROOT/output/pixel/runtime-baseline-health-$timestamp_utc.log"
touch "$run_marker" "$security_hits_file" "$stability_file" "$runtime_tunnel_file"

cd "$REPO_ROOT"
git_sha="$(git rev-parse HEAD)"
dirty_count="$(git status --porcelain | wc -l | tr -d '[:space:]')"

set -a
# shellcheck source=/dev/null
. "$REPO_ROOT/.env"
set +a

declare -a p1_findings
declare -a p2_findings
add_p1() { p1_findings+=("$1"); }
add_p2() { p2_findings+=("$1|$2|$3|$4"); }

status_test="SKIP"
status_build="SKIP"
status_deploy="SKIP"
status_runtime_assets="FAIL"
status_baseline_health="FAIL"
status_isolation="SKIP"
status_release_check="SKIP"
status_public_smoke="SKIP"
status_miniapp_smoke="SKIP"
status_bot_smoke="SKIP"
status_process="FAIL"
status_polling="FAIL"
status_crash_loop="FAIL"
status_stability="FAIL"
status_schedule_file="FAIL"
status_schedule_db="FAIL"
status_schedule_load="FAIL"
status_train_web_root="FAIL"
status_train_web_departures="FAIL"
status_train_web_app="FAIL"
status_train_web_legacy="FAIL"
status_remote_public_root="FAIL"
status_remote_public_doh="FAIL"
status_remote_public_identity="FAIL"
status_token="FAIL"
status_otp="FAIL"

run_gate() {
  local name="$1"
  shift
  log "Running gate: $name"
  if "$@"; then
    log "Gate passed: $name"
    return 0
  else
    local rc=$?
    log "Gate failed: $name (exit $rc)"
    return "$rc"
  fi
}

transport_exec() {
  local command_path="$1"
  shift
  local -a cmd=("${command_path}")
  local line=""
  while IFS= read -r line; do
    [[ -n "${line}" ]] && cmd+=("${line}")
  done < <(transport_args)
  cmd+=("$@")
  "${cmd[@]}"
}

long_poll="${LONG_POLL_TIMEOUT:-30}"
http_timeout="${HTTP_TIMEOUT_SEC:-15}"
if ! [[ "$long_poll" =~ ^[0-9]+$ ]] || ! [[ "$http_timeout" =~ ^[0-9]+$ ]]; then
  add_p1 "Invalid timeout values in .env: LONG_POLL_TIMEOUT=$long_poll HTTP_TIMEOUT_SEC=$http_timeout"
elif (( http_timeout <= long_poll )); then
  add_p1 "HTTP_TIMEOUT_SEC must be greater than LONG_POLL_TIMEOUT (got $http_timeout <= $long_poll)"
fi

profile_dir="${AGENT_BROWSER_PROFILE_DIR:-$HOME/.cache/agent-browser/telegram-web}"
if [[ ! -d "$profile_dir" || -z "$(find "$profile_dir" -mindepth 1 -print -quit 2>/dev/null)" ]]; then
  add_p1 "agent-browser profile missing or empty: $profile_dir"
fi

if (( ${#p1_findings[@]} == 0 )); then
  if [[ ! -x "${ORCHESTRATOR_REPO}/scripts/android/runtime_asset_freshness.sh" ]]; then
    add_p1 "Missing runtime asset freshness helper: ${ORCHESTRATOR_REPO}/scripts/android/runtime_asset_freshness.sh"
  elif transport_exec "${ORCHESTRATOR_REPO}/scripts/android/runtime_asset_freshness.sh" --scope readiness >"${runtime_asset_freshness_file}" 2>&1; then
    status_runtime_assets="PASS"
  else
    rc=$?
    status_runtime_assets="FAIL"
    if (( rc == 3 )); then
      add_p1 "Bundled rooted/train runtime assets on the Pixel are stale. Run \`make pixel-refresh-runtime\` on already-provisioned devices, or \`$REPO_ROOT/../../tools/pixel/redeploy.sh --scope train_bot --mode force-bootstrap\` when env/config inputs changed, then rerun readiness. Details: ${runtime_asset_freshness_file}"
    else
      add_p1 "Unable to verify runtime asset freshness (exit ${rc}). Details: ${runtime_asset_freshness_file}"
    fi
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "baseline-health" transport_exec "${ORCHESTRATOR_REPO}/scripts/android/deploy_orchestrator_apk.sh" --action health --skip-build >"${baseline_health_file}" 2>&1; then
    status_baseline_health="PASS"
  else
    status_baseline_health="FAIL"
    add_p1 "Baseline orchestrator health failed before release gates. Refresh runtime assets first; if env/config drift is suspected, rerun bootstrap-only before retrying readiness. Details: ${baseline_health_file}"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-native-test" make pixel-native-test; then
    status_test="PASS"
  else
    status_test="FAIL"
    add_p1 "Release gate failed: make pixel-native-test"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-native-build" make pixel-native-build; then
    status_build="PASS"
  else
    status_build="FAIL"
    add_p1 "Release gate failed: make pixel-native-build"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-native-deploy" transport_exec "$REPO_ROOT/../../tools/pixel/redeploy.sh" --scope train_bot; then
    status_deploy="PASS"
  else
    status_deploy="FAIL"
    add_p1 "Release gate failed: tools/pixel/redeploy.sh --scope train_bot"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-release-check" make pixel-release-check; then
    status_release_check="PASS"
  else
    status_release_check="FAIL"
    add_p1 "Release gate failed: make pixel-release-check"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-public-smoke" make pixel-public-smoke; then
    status_public_smoke="PASS"
  else
    status_public_smoke="FAIL"
    add_p1 "Release gate failed: make pixel-public-smoke"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-miniapp-smoke" make pixel-miniapp-smoke; then
    status_miniapp_smoke="PASS"
  else
    status_miniapp_smoke="FAIL"
    add_p1 "Release gate failed: make pixel-miniapp-smoke"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-bot-smoke" make pixel-bot-smoke; then
    status_bot_smoke="PASS"
  else
    status_bot_smoke="FAIL"
    add_p1 "Release gate failed: make pixel-bot-smoke"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  if run_gate "pixel-isolation-check" make pixel-isolation-check; then
    status_isolation="PASS"
  else
    status_isolation="FAIL"
    add_p1 "Release gate failed: make pixel-isolation-check"
  fi
fi

if (( ${#p1_findings[@]} == 0 )); then
  process_out="$(adb_cmd shell su -c 'ps -A | grep -E "train-bot|train-bot-service-loop" || true' | tr -d '\r')"
  printf '%s\n' "$process_out" >"$runtime_ps_file"
  train_count="$(adb_cmd shell su -c 'ps -A | awk "(\$NF==\"train-bot\" || index(\$NF,\"train-bot.\")==1) {c++} END{print c+0}"' | tr -d '\r' | tr -d '[:space:]')"
  if [[ "$train_count" == "1" ]]; then
    status_process="PASS"
  else
    add_p1 "Runtime process check failed: expected exactly 1 train-bot process (found ${train_count:-unknown})"
  fi

  adb_cmd shell su -c '[ -f /data/local/pixel-stack/apps/train-bot/logs/train-bot.log ] && tail -n 200 /data/local/pixel-stack/apps/train-bot/logs/train-bot.log || tail -n 200 /data/local/pixel-stack/telegram-train-bot/logs/train-bot.log' >"$runtime_train_file" || true
  adb_cmd shell su -c '[ -f /data/local/pixel-stack/apps/train-bot/logs/service-loop.log ] && tail -n 200 /data/local/pixel-stack/apps/train-bot/logs/service-loop.log || tail -n 200 /data/local/pixel-stack/telegram-train-bot/logs/service-loop.log' >"$runtime_loop_file" || true
  train_tunnel_enabled="$(adb_cmd shell su -c "value=''; if [ -f /data/local/pixel-stack/apps/train-bot/env/train-bot.env ]; then value=\$(grep -E '^TRAIN_WEB_TUNNEL_ENABLED=' /data/local/pixel-stack/apps/train-bot/env/train-bot.env | tail -n 1 | cut -d= -f2-); fi; case \"\$value\" in 1|true|TRUE|yes|YES|on|ON) printf '1';; *) printf '0';; esac" | tr -d '\r' | tr -d '[:space:]')"
  train_tunnel_public_base_url="$(adb_cmd shell su -c "value=''; if [ -f /data/local/pixel-stack/apps/train-bot/env/train-bot.env ]; then value=\$(grep -E '^TRAIN_WEB_PUBLIC_BASE_URL=' /data/local/pixel-stack/apps/train-bot/env/train-bot.env | tail -n 1 | cut -d= -f2-); fi; printf '%s' \"\$value\"" | tr -d '\r' | sed -e "s/^['\"]//" -e "s/['\"]$//")"
  tunnel_pid_file="/data/local/pixel-stack/apps/train-bot/run/train-bot-cloudflared.pid"
  tunnel_log_file="/data/local/pixel-stack/apps/train-bot/logs/train-bot-cloudflared.log"
  tunnel_config_file="/data/local/pixel-stack/apps/train-bot/state/train-web-tunnel/train-bot-cloudflared.yml"
  train_tunnel_credentials_file="$(adb_cmd shell su -c "value=''; if [ -f /data/local/pixel-stack/apps/train-bot/env/train-bot.env ]; then value=\$(grep -E '^TRAIN_WEB_TUNNEL_CREDENTIALS_FILE=' /data/local/pixel-stack/apps/train-bot/env/train-bot.env | tail -n 1 | cut -d= -f2-); fi; if [ -z \"\$value\" ]; then value=/data/local/pixel-stack/conf/apps/train-bot-cloudflared.json; fi; printf '%s' \"\$value\"" | tr -d '\r' | sed -e "s/^['\"]//" -e "s/['\"]$//")"
  adb_cmd shell su -c "[ -f '${tunnel_log_file}' ] && tail -n 200 '${tunnel_log_file}' || true" >"$runtime_tunnel_file" || true
  if [[ "${train_tunnel_enabled}" == "1" ]]; then
    if ! adb_cmd shell su -c "test -s '${train_tunnel_credentials_file}'"; then
      add_p1 "Train bot tunnel credentials missing or empty: ${train_tunnel_credentials_file}"
    fi
    if ! adb_cmd shell su -c "test -s '${tunnel_config_file}'"; then
      add_p1 "Train bot cloudflared config missing or empty: ${tunnel_config_file}"
    fi
    if ! adb_cmd shell su -c "pid=''; if [ -r '${tunnel_pid_file}' ]; then pid=\$(sed -n '1p' '${tunnel_pid_file}' 2>/dev/null | tr -d '\r'); fi; [ -n \"\$pid\" ] && kill -0 \"\$pid\" >/dev/null 2>&1"; then
      add_p1 "Train bot cloudflared tunnel is not running (pid file: ${tunnel_pid_file}, log: ${runtime_tunnel_file})"
    fi
  fi
  service_date_riga="$(adb_cmd shell su -c 'TZ=Europe/Riga date +%F' | tr -d '\r' | tr -d '[:space:]')"
  schedule_path="/data/local/pixel-stack/apps/train-bot/data/schedules/${service_date_riga}.json"
  if adb_cmd shell su -c "test -s '${schedule_path}'"; then
    status_schedule_file="PASS"
  else
    add_p1 "Missing fresh same-day runtime schedule snapshot for ${service_date_riga}: ${schedule_path}"
  fi

  db_path_raw="$(adb_cmd shell su -c '[ -f /data/local/pixel-stack/apps/train-bot/env/train-bot.env ] && grep -E "^DB_PATH=" /data/local/pixel-stack/apps/train-bot/env/train-bot.env | tail -n 1 | cut -d= -f2- || true' | tr -d '\r' | tr -d '"' | tail -n 1)"
  if [[ -z "${db_path_raw}" ]]; then
    runtime_db_path="/data/local/pixel-stack/apps/train-bot/train_bot.db"
  elif [[ "${db_path_raw}" == /* ]]; then
    runtime_db_path="${db_path_raw}"
  else
    runtime_db_path="/data/local/pixel-stack/apps/train-bot/${db_path_raw#./}"
  fi
  runtime_db_remote_copy="/sdcard/Download/train-bot-runtime-db-${timestamp_utc}.db"
  runtime_db_local_copy="$REPO_ROOT/output/pixel/runtime-train-bot-db-${timestamp_utc}.db"
  if adb_cmd shell su -c "test -f '${runtime_db_path}'"; then
    if adb_cmd shell su -c "cp '${runtime_db_path}' '${runtime_db_remote_copy}' && chmod 0644 '${runtime_db_remote_copy}'"; then
      if adb_cmd pull "${runtime_db_remote_copy}" "${runtime_db_local_copy}" >/dev/null 2>&1; then
        today_train_rows="$(sqlite3 "${runtime_db_local_copy}" "select count(*) from train_instances where service_date='${service_date_riga}';" 2>/dev/null | tr -d '[:space:]')"
        if [[ -z "${today_train_rows}" ]]; then
          today_train_rows="0"
        fi
      else
        today_train_rows="missing_db_pull"
      fi
      adb_cmd shell su -c "rm -f '${runtime_db_remote_copy}'" >/dev/null 2>&1 || true
    else
      today_train_rows="missing_db_copy"
    fi
  else
    today_train_rows="0"
  fi
  if [[ "${today_train_rows}" == "missing_db_copy" ]]; then
    add_p1 "Unable to copy runtime DB from device for schedule validation (db=${runtime_db_path})"
  elif [[ "${today_train_rows}" == "missing_db_pull" ]]; then
    add_p1 "Unable to pull runtime DB copy from device for schedule validation (remote=${runtime_db_remote_copy})"
  elif [[ "${today_train_rows}" =~ ^[0-9]+$ ]] && (( today_train_rows > 0 )); then
    status_schedule_db="PASS"
  else
    add_p1 "Runtime DB has no train_instances for ${service_date_riga} (db=${runtime_db_path}, rows=${today_train_rows:-unknown})"
  fi

  recent_load_failures="$(tail -n 120 "$runtime_train_file" | grep -c 'load today schedule failed' || true)"
  if (( recent_load_failures > 0 )); then
    add_p1 "Schedule load failures detected in runtime log tail (${recent_load_failures} hits of 'load today schedule failed')"
  else
    status_schedule_load="PASS"
  fi

  recent_deadlines="$(tail -n 60 "$runtime_train_file" | grep -c 'context deadline exceeded' || true)"
  if (( recent_deadlines >= 3 )); then
    add_p1 "Sustained polling timeouts detected in runtime log tail ($recent_deadlines hits)"
  else
    status_polling="PASS"
  fi

  recent_starts="$(tail -n 80 "$runtime_train_file" | grep -c 'bot started' || true)"
  if (( recent_starts > 2 )); then
    add_p1 "Possible crash loop: bot started appears $recent_starts times in runtime tail"
  else
    status_crash_loop="PASS"
  fi

  bad_samples=0
  for i in $(seq 1 10); do
    sample="$(adb_cmd shell su -c 'ps -A | grep -E "train-bot|train-bot-service-loop" || true' | tr -d '\r')"
    sample_count="$(adb_cmd shell su -c 'ps -A | awk "(\$NF==\"train-bot\" || index(\$NF,\"train-bot.\")==1) {c++} END{print c+0}"' | tr -d '\r' | tr -d '[:space:]')"
    printf '[sample %02d] %s\n' "$i" "$sample" >>"$stability_file"
    printf '[sample %02d] train-bot-count=%s\n' "$i" "${sample_count:-unknown}" >>"$stability_file"
    if [[ "${sample_count:-0}" != "1" ]]; then
      bad_samples=$((bad_samples + 1))
    fi
    if (( i < 10 )); then
      sleep 30
    fi
  done
  if (( bad_samples == 0 )); then
    status_stability="PASS"
  else
    add_p1 "5-minute stability check failed; expected exactly 1 train-bot process in all samples (bad samples: $bad_samples)"
  fi

  train_web_root_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 https://train-bot.jolkins.id.lv/ || true)"
  if [[ "${train_web_root_code}" == "200" ]]; then
    status_train_web_root="PASS"
  else
    add_p1 "Public station-search root check failed: https://train-bot.jolkins.id.lv/ returned ${train_web_root_code:-unknown}"
  fi

  train_web_departures_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 https://train-bot.jolkins.id.lv/departures || true)"
  if [[ "${train_web_departures_code}" == "200" ]]; then
    status_train_web_departures="PASS"
  else
    add_p1 "Public departures page check failed: https://train-bot.jolkins.id.lv/departures returned ${train_web_departures_code:-unknown}"
  fi

  train_web_app_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 https://train-bot.jolkins.id.lv/app || true)"
  if [[ "${train_web_app_code}" == "200" ]]; then
    status_train_web_app="PASS"
  else
    add_p1 "Mini App shell check failed: https://train-bot.jolkins.id.lv/app returned ${train_web_app_code:-unknown}"
  fi
  if [[ "${train_tunnel_enabled}" == "1" ]]; then
    if [[ "${train_web_root_code}" != "200" || "${train_web_departures_code}" != "200" || "${train_web_app_code}" != "200" ]]; then
      add_p1 "Train bot Cloudflare tunnel gate failed: root=${train_web_root_code:-unknown} departures=${train_web_departures_code:-unknown} app=${train_web_app_code:-unknown} public_base_url=${train_tunnel_public_base_url:-https://train-bot.jolkins.id.lv}"
    fi
  fi

  train_web_legacy_code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 https://dns.jolkins.id.lv/pixel-stack/train/app || true)"
  case "${train_web_legacy_code}" in
    404|410|500|502|503|504|000)
      status_train_web_legacy="PASS"
      ;;
    *)
      add_p1 "Legacy path still exposed: https://dns.jolkins.id.lv/pixel-stack/train/app returned ${train_web_legacy_code:-unknown}"
      ;;
  esac

  remote_hostname="$(python3 - "${ORCHESTRATOR_CONFIG_FILE}" <<'PY'
import json
import sys
from pathlib import Path
cfg = json.loads(Path(sys.argv[1]).read_text())
remote = cfg.get("remote", {})
hostname = (remote.get("hostname") or "").strip()
port = int(remote.get("httpsPort") or 443)
if hostname:
    print(f"https://{hostname}" if port == 443 else f"https://{hostname}:{port}")
PY
)"
  remote_doh_mode="$(python3 - "${ORCHESTRATOR_CONFIG_FILE}" <<'PY'
import json
import sys
from pathlib import Path
cfg = json.loads(Path(sys.argv[1]).read_text())
print((cfg.get("remote", {}).get("dohEndpointMode") or "native").strip())
PY
)"
  remote_doh_token="$(python3 - "${ORCHESTRATOR_CONFIG_FILE}" <<'PY'
import json
import sys
from pathlib import Path
cfg = json.loads(Path(sys.argv[1]).read_text())
print((cfg.get("remote", {}).get("dohPathToken") or "").strip())
PY
)"
  remote_public_root_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/" || true)"
  case "${remote_public_root_code}" in
    200|301|302|303|307|308|401)
    status_remote_public_root="PASS"
      ;;
    *)
      add_p1 "Remote public root check failed: ${remote_hostname}/ returned ${remote_public_root_code:-unknown}"
      ;;
  esac

  remote_public_identity_code="000"
  case "${remote_doh_mode}" in
    tokenized|dual)
      remote_public_identity_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/pixel-stack/identity/inject.js" || true)"
      if [[ "${remote_public_identity_code}" == "200" ]]; then
        status_remote_public_identity="PASS"
      else
        add_p1 "Remote identity frontend check failed: ${remote_hostname}/pixel-stack/identity/inject.js returned ${remote_public_identity_code:-unknown}"
      fi
      ;;
    *)
      status_remote_public_identity="PASS"
      ;;
  esac

  remote_public_bare_code="000"
  remote_public_tokenized_code="000"
  case "${remote_doh_mode}" in
    tokenized)
      if [[ -n "${remote_doh_token}" ]]; then
        remote_public_tokenized_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/${remote_doh_token}/dns-query" || true)"
      fi
      remote_public_bare_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/dns-query" || true)"
      if [[ "${remote_public_tokenized_code}" =~ ^2[0-9][0-9]$ && "${remote_public_bare_code}" == "404" ]]; then
        status_remote_public_doh="PASS"
      else
        add_p1 "Remote DoH contract failed: mode=${remote_doh_mode} tokenized=${remote_public_tokenized_code:-unknown} bare=${remote_public_bare_code:-unknown} base=${remote_hostname}"
      fi
      ;;
    dual)
      if [[ -n "${remote_doh_token}" ]]; then
        remote_public_tokenized_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/${remote_doh_token}/dns-query" || true)"
      fi
      remote_public_bare_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/dns-query" || true)"
      if [[ "${remote_public_tokenized_code}" =~ ^2[0-9][0-9]$ && "${remote_public_bare_code}" =~ ^2[0-9][0-9]$ ]]; then
        status_remote_public_doh="PASS"
      else
        add_p1 "Remote DoH contract failed: mode=${remote_doh_mode} tokenized=${remote_public_tokenized_code:-unknown} bare=${remote_public_bare_code:-unknown} base=${remote_hostname}"
      fi
      ;;
    native)
      remote_public_bare_code="$(curl -ksS -o /dev/null -w '%{http_code}' --max-time 15 "${remote_hostname}/dns-query" || true)"
      if [[ "${remote_public_bare_code}" =~ ^2[0-9][0-9]$ ]]; then
        status_remote_public_doh="PASS"
      else
        add_p1 "Remote DoH contract failed: mode=${remote_doh_mode} bare=${remote_public_bare_code:-unknown} base=${remote_hostname}"
      fi
      ;;
  esac
fi

declare -a scan_files
while IFS= read -r f; do
  scan_files+=("$f")
done < <(find "$REPO_ROOT/output/pixel" "$REPO_ROOT/output/agent-browser" -type f -newer "$run_marker" 2>/dev/null | sort)

declare -a security_scan_files
for f in "${scan_files[@]}"; do
  # agent-browser auth-state dumps can contain unrelated Telegram login metadata and create OTP false positives.
  if [[ "$f" == */output/agent-browser/*/*auth-state*.json ]]; then
    continue
  fi
  security_scan_files+=("$f")
done

if (( ${#security_scan_files[@]} == 0 )); then
  add_p1 "No current-run artifacts found for security scan"
else
  token_hits="$(rg -n --no-heading -P 'bot[0-9]{6,}:[A-Za-z0-9_-]{20,}' "${security_scan_files[@]}" || true)"
  if [[ -n "$token_hits" ]]; then
    status_token="FAIL"
    printf '%s\n' "$token_hits" >>"$security_hits_file"
    add_p1 "Token redaction scan failed on current-run artifacts"
  else
    status_token="PASS"
  fi

  otp_hits="$(rg -n --no-heading -i '(login code|verification code|do not give this code|one-time code|2fa code)' "${security_scan_files[@]}" || true)"
  if [[ -n "$otp_hits" ]]; then
    status_otp="FAIL"
    printf '%s\n' "$otp_hits" >>"$security_hits_file"
    add_p1 "OTP/auth-code leak indicators detected in current-run artifacts"
  else
    status_otp="PASS"
  fi
fi

if [[ "${MANUAL_HEALTH_OK:-0}" != "1" ]]; then
  add_p2 "Manual /health confirmation pending" "QA" "Before release approval" "Run /health in Telegram and confirm exact reply 'ok'."
fi

verdict="READY"
if (( ${#p1_findings[@]} > 0 )); then
  verdict="NOT READY"
elif (( ${#p2_findings[@]} > 0 )); then
  verdict="READY WITH CONDITIONS"
fi

{
  echo "# Production Readiness Report"
  echo
  echo "- Generated (UTC): $(date -u '+%Y-%m-%d %H:%M:%S')"
  echo "- Git SHA: $git_sha"
  echo "- Dirty files at start: $dirty_count"
  echo "- Device: $ADB_SERIAL"
  echo
  echo "## Gate Results"
  echo "- Runtime assets fresh before release gates: $status_runtime_assets"
  echo "- Baseline orchestrator health before release gates: $status_baseline_health"
  echo "- make pixel-native-test: $status_test"
  echo "- make pixel-native-build: $status_build"
  echo "- tools/pixel/redeploy.sh --scope train_bot: $status_deploy"
  echo "- make pixel-isolation-check: $status_isolation"
  echo "- make pixel-release-check: $status_release_check"
  echo "- make pixel-public-smoke: $status_public_smoke"
  echo "- make pixel-miniapp-smoke: $status_miniapp_smoke"
  echo "- make pixel-bot-smoke: $status_bot_smoke"
  echo
  echo "## Runtime Checklist"
  echo "- Exactly one train-bot process present: $status_process"
  echo "- Same-day runtime schedule file fresh: $status_schedule_file"
  echo "- Same-day runtime DB rows present: $status_schedule_db"
  echo "- No recent schedule load failures: $status_schedule_load"
  echo "- No sustained polling timeout loop: $status_polling"
  echo "- No crash-loop behavior in tail: $status_crash_loop"
  echo "- 5-minute stability sampling: $status_stability"
  echo "- Public station-search root returns 200: $status_train_web_root"
  echo "- Public departures page returns 200: $status_train_web_departures"
  echo "- Mini App shell returns 200: $status_train_web_app"
  echo "- Legacy dns.jolkins.id.lv path no longer exposed: $status_train_web_legacy"
  echo "- Remote dns.jolkins.id.lv root returns 200: $status_remote_public_root"
  echo "- Remote public DoH contract is healthy: $status_remote_public_doh"
  echo "- Remote identity frontend is healthy: $status_remote_public_identity"
  echo
  echo "## Security Checklist"
  echo "- No token-like strings in current-run artifacts: $status_token"
  echo "- No OTP/auth-code text leaks in current-run artifacts: $status_otp"
  echo "- Security hits log: $security_hits_file"
  echo
  echo "## Findings"
  if (( ${#p1_findings[@]} == 0 )); then
    echo "- No P1 findings."
  else
    for finding in "${p1_findings[@]}"; do
      echo "- P1: $finding"
    done
  fi
  if (( ${#p2_findings[@]} == 0 )); then
    echo "- No P2 findings."
  else
    for finding in "${p2_findings[@]}"; do
      IFS='|' read -r title owner eta detail <<<"$finding"
      echo "- P2: $title (owner: $owner, eta: $eta). $detail"
    done
  fi
  echo
  echo "## Verdict"
  echo "**$verdict**"
} >"$report_file"

echo "E2E Assessment Checklist"
echo "- Functional E2E gates: runtime_assets=$status_runtime_assets baseline_health=$status_baseline_health native_test=$status_test native_build=$status_build native_deploy=$status_deploy isolation=$status_isolation release_check=$status_release_check public_smoke=$status_public_smoke miniapp_smoke=$status_miniapp_smoke bot_smoke=$status_bot_smoke"
echo "- Runtime checks: process=$status_process schedule_file=$status_schedule_file schedule_db=$status_schedule_db schedule_load=$status_schedule_load polling=$status_polling crash_loop=$status_crash_loop stability=$status_stability web_root=$status_train_web_root web_departures=$status_train_web_departures web_app=$status_train_web_app legacy_path=$status_train_web_legacy"
echo "- Remote checks: root=$status_remote_public_root doh=$status_remote_public_doh identity=$status_remote_public_identity"
echo "- Security checks: token=$status_token otp=$status_otp"
echo "- Report: $report_file"
echo "- Verdict: $verdict"

if [[ "$verdict" == "NOT READY" ]]; then
  exit 1
fi
