#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HEALTH_SCRIPT="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge-health.sh"
LOOP_SCRIPT="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge-loop.sh"

TMP_DIR="$(mktemp -d)"

cleanup() {
  local pid_file pid
  while IFS= read -r pid_file; do
    while IFS= read -r pid; do
      kill "${pid}" >/dev/null 2>&1 || true
    done <"${pid_file}"
  done < <(find "${TMP_DIR}" -name 'event-pids' -type f 2>/dev/null)
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

make_fake_bin() {
  local fake_bin="$1"
  mkdir -p "${fake_bin}"
  cat >"${fake_bin}/adb" <<'SH'
#!/usr/bin/env sh
set -eu

target=""
if [ "${1:-}" = "-s" ]; then
  target="$2"
  shift 2
fi

case "${1:-}" in
  connect)
    printf 'connect %s\n' "${2:-}" >>"${ADB_LOG:?}"
    exit 0
    ;;
  get-state)
    printf 'get-state %s\n' "${target}" >>"${ADB_LOG:?}"
    if [ "${ADB_GET_STATE_FAIL:-0}" = 1 ]; then
      exit 1
    fi
    if [ "${ADB_GET_STATE_FAIL_COUNT:-0}" -gt 0 ]; then
      count=0
      if [ -f "${ADB_GET_STATE_COUNT_FILE:?}" ]; then
        count="$(cat "${ADB_GET_STATE_COUNT_FILE}")"
      fi
      count=$((count + 1))
      printf '%s\n' "${count}" >"${ADB_GET_STATE_COUNT_FILE}"
      if [ "${count}" -le "${ADB_GET_STATE_FAIL_COUNT}" ]; then
        exit 1
      fi
    fi
    printf 'device\n'
    exit 0
    ;;
  forward)
    case "${2:-}" in
      --list)
        printf 'list %s\n' "${target}" >>"${ADB_LOG:?}"
        printf '%s\n' "${ADB_FORWARD_LIST:-}"
        exit 0
        ;;
      --remove)
        printf 'remove %s %s\n' "${target}" "${3:-}" >>"${ADB_LOG:?}"
        exit 0
        ;;
      *)
        printf 'set %s %s %s\n' "${target}" "${2:-}" "${3:-}" >>"${ADB_LOG:?}"
        exit 0
        ;;
    esac
    ;;
esac

printf 'unexpected adb call: %s %s\n' "${target}" "$*" >>"${ADB_LOG:?}"
exit 1
SH
  chmod +x "${fake_bin}/adb"

  cat >"${fake_bin}/curl" <<'SH'
#!/usr/bin/env sh
set -eu

is_event=0
for arg in "$@"; do
  if [ "${arg}" = "--data" ]; then
    is_event=1
    break
  fi
done

if [ "${is_event}" = 1 ]; then
  if [ -n "${EVENT_LOG:-}" ]; then
    printf '%s\n' "$*" >>"${EVENT_LOG}"
  fi
  if [ -n "${EVENT_PID_FILE:-}" ]; then
    printf '%s\n' "$$" >>"${EVENT_PID_FILE}"
  fi
  if [ -n "${EVENT_CURL_SLEEP:-}" ]; then
    sleep "${EVENT_CURL_SLEEP}"
  fi
  if [ -n "${EVENT_FINISHED_FILE:-}" ]; then
    : >"${EVENT_FINISHED_FILE}"
  fi
  [ "${EVENT_CURL_FAIL:-0}" != 1 ]
  exit
fi

count=0
if [ -n "${CURL_COUNT_FILE:-}" ]; then
  if [ -f "${CURL_COUNT_FILE}" ]; then
    count="$(cat "${CURL_COUNT_FILE}")"
  fi
  count=$((count + 1))
  printf '%s\n' "${count}" >"${CURL_COUNT_FILE}"
  if [ "${CURL_RELEASE_ON_COUNT:-0}" -gt 0 ] && [ "${count}" -ge "${CURL_RELEASE_ON_COUNT}" ]; then
    : >"${SOCAT_RELEASE_FILE:?}"
  fi
fi

if [ -n "${CURL_WAIT_FOR_FILE:-}" ] && [ "${count}" -gt "${CURL_WAIT_FOR_FILE_AFTER_COUNT:-1}" ]; then
  attempts=0
  while [ ! -e "${CURL_WAIT_FOR_FILE}" ] && [ "${attempts}" -lt 100 ]; do
    attempts=$((attempts + 1))
    command -p sleep 0.005
  done
fi

if [ "${CURL_FAIL:-0}" = 1 ]; then
  exit 7
fi
if [ "${CURL_FAIL_FIRST:-0}" -gt 0 ] && [ "${count}" -le "${CURL_FAIL_FIRST}" ]; then
  exit 7
fi
if [ "${CURL_FAIL_AFTER:-0}" -gt 0 ] && [ "${count}" -gt "${CURL_FAIL_AFTER}" ]; then
  exit 7
fi
if [ "${CURL_FAIL_ON_COUNT:-0}" -gt 0 ] && [ "${count}" -eq "${CURL_FAIL_ON_COUNT}" ]; then
  exit 7
fi

printf '%s\n__TICKET_HTTP_STATUS__:%s' \
  "${CURL_BODY:-{\"ok\":true,\"serverRunning\":true}}" \
  "${CURL_STATUS:-200}"
SH
  chmod +x "${fake_bin}/curl"
}

make_immediate_socat() {
  local fake_bin="$1"
  cat >"${fake_bin}/socat" <<'SH'
#!/usr/bin/env sh
set -eu
printf 'started\n' >>"${SOCAT_LOG:?}"
printf 'exited\n' >>"${SOCAT_LOG:?}"
SH
  chmod +x "${fake_bin}/socat"
}

make_persistent_socat() {
  local fake_bin="$1"
  cat >"${fake_bin}/socat" <<'SH'
#!/usr/bin/env sh
set -eu
printf 'started\n' >>"${SOCAT_LOG:?}"
if [ -n "${SOCAT_READY_FILE:-}" ]; then
  : >"${SOCAT_READY_FILE}"
fi
trap 'printf "terminated\n" >>"${SOCAT_LOG:?}"; exit 0' TERM INT
while :; do
  command -p sleep 0.01
done
SH
  chmod +x "${fake_bin}/socat"
}

make_self_stopping_socat() {
  local fake_bin="$1"
  cat >"${fake_bin}/socat" <<'SH'
#!/usr/bin/env sh
set -eu
printf 'started\n' >>"${SOCAT_LOG:?}"
trap 'printf "terminated\n" >>"${SOCAT_LOG:?}"; exit 0' TERM INT
while [ ! -e "${SOCAT_RELEASE_FILE:?}" ]; do
  command -p sleep 0.001
done
printf 'exited\n' >>"${SOCAT_LOG:?}"
SH
  chmod +x "${fake_bin}/socat"
}

run_with_fake_phone_tools() {
  local fake_bin="$1"
  shift
  env \
    PATH="${fake_bin}:${PATH}" \
    ADB_LOG="${TMP_DIR}/adb.log" \
    TICKET_PHONE_ADB_TARGET="test-device:5555" \
    TICKET_PHONE_DEVICE_PORT="9388" \
    TICKET_PHONE_ADB_FORWARD_PORT="19389" \
    "$@"
}

wait_for_nonempty_file() {
  local path="$1"
  local failure_message="$2"
  local attempts=0
  while [ ! -s "${path}" ] && [ "${attempts}" -lt 50 ]; do
    attempts=$((attempts + 1))
    sleep 0.005
  done
  [ -s "${path}" ] || fail "${failure_message}"
}

test_health_fast_path_is_endpoint_only() {
  local fake_bin="${TMP_DIR}/bin-health-fast"
  local adb_log="${TMP_DIR}/adb-health-fast.log"
  make_fake_bin "${fake_bin}"
  run_with_fake_phone_tools "${fake_bin}" \
    ADB_LOG="${adb_log}" \
    ADB_GET_STATE_FAIL=1 \
    CURL_BODY=' { "ok": true, "serverRunning": true } ' \
    "${HEALTH_SCRIPT}" >/dev/null
  [ ! -s "${adb_log}" ] || fail "fast health performed ADB work even though the endpoint was ready"
}

test_health_strict_accepts_valid_forward() {
  local fake_bin="${TMP_DIR}/bin-health-strict-ok"
  make_fake_bin "${fake_bin}"
  run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    "${HEALTH_SCRIPT}" --strict >/dev/null
}

test_health_strict_requires_adb_state() {
  local fake_bin="${TMP_DIR}/bin-health-strict-adb-state"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_GET_STATE_FAIL=1 \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    "${HEALTH_SCRIPT}" --strict >/dev/null 2>&1; then
    fail "strict bridge health passed even though ADB was unavailable"
  fi
}

test_health_strict_rejects_missing_forward() {
  local fake_bin="${TMP_DIR}/bin-health-missing-forward"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19390 tcp:9388" \
    "${HEALTH_SCRIPT}" --strict >/dev/null 2>&1; then
    fail "strict bridge health passed even though the expected ADB forward was missing"
  fi
}

test_health_rejects_unavailable_endpoint() {
  local fake_bin="${TMP_DIR}/bin-health-dead-endpoint"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_FAIL=1 \
    "${HEALTH_SCRIPT}" >/dev/null 2>&1; then
    fail "bridge health passed even though the forwarded phone endpoint was unavailable"
  fi
}

test_health_requires_exact_http_200() {
  local fake_bin="${TMP_DIR}/bin-health-http-status"
  local error_file="${TMP_DIR}/health-http-status.err"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_STATUS=204 \
    "${HEALTH_SCRIPT}" >/dev/null 2>"${error_file}"; then
    fail "bridge health accepted a non-200 endpoint response"
  fi
  grep -F "HTTP 204" "${error_file}" >/dev/null || fail "bridge health did not explain the non-200 response"
}

test_health_requires_top_level_ok_true() {
  local fake_bin="${TMP_DIR}/bin-health-not-ready"
  local error_file="${TMP_DIR}/health-not-ready.err"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_BODY='{"ok":false,"nested":{"ok":true}}' \
    "${HEALTH_SCRIPT}" >/dev/null 2>"${error_file}"; then
    fail "bridge health accepted an endpoint whose top-level readiness was false"
  fi
  grep -F "did not report ok=true" "${error_file}" >/dev/null || fail "bridge health did not explain the readiness failure"
}

test_loop_reuses_existing_forward_without_adb_setup() {
  local fake_bin="${TMP_DIR}/bin-loop-reuse"
  local adb_log="${TMP_DIR}/adb-loop-reuse.log"
  local socat_log="${TMP_DIR}/socat-loop-reuse.log"
  local socat_ready_file="${TMP_DIR}/socat-loop-reuse.ready"
  local curl_count="${TMP_DIR}/curl-loop-reuse-count"
  make_fake_bin "${fake_bin}"
  make_persistent_socat "${fake_bin}"

  run_with_fake_phone_tools "${fake_bin}" \
    ADB_LOG="${adb_log}" \
    CURL_COUNT_FILE="${curl_count}" \
    CURL_FAIL_AFTER=1 \
    CURL_WAIT_FOR_FILE="${socat_ready_file}" \
    SOCAT_LOG="${socat_log}" \
    SOCAT_READY_FILE="${socat_ready_file}" \
    TICKET_PHONE_BRIDGE_PORT=9388 \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.01 \
    TICKET_PHONE_HEALTH_FAILURE_THRESHOLD=1 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1

  grep -F "started" "${socat_log}" >/dev/null || fail "bridge loop did not reuse the ready forward"
  if grep -E '^(connect|set|get-state|list) ' "${adb_log}" >/dev/null 2>&1; then
    fail "healthy bridge reuse performed unnecessary ADB setup"
  fi
}

test_loop_recovers_failed_existing_forward() {
  local fake_bin="${TMP_DIR}/bin-loop-recover"
  local adb_log="${TMP_DIR}/adb-loop-recover.log"
  local socat_log="${TMP_DIR}/socat-loop-recover.log"
  local socat_ready_file="${TMP_DIR}/socat-loop-recover.ready"
  local curl_count="${TMP_DIR}/curl-loop-recover-count"
  make_fake_bin "${fake_bin}"
  make_persistent_socat "${fake_bin}"

  run_with_fake_phone_tools "${fake_bin}" \
    ADB_LOG="${adb_log}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    ADB_GET_STATE_FAIL_COUNT=1 \
    ADB_GET_STATE_COUNT_FILE="${TMP_DIR}/adb-get-state-recover-count" \
    CURL_COUNT_FILE="${curl_count}" \
    CURL_FAIL_FIRST=1 \
    CURL_FAIL_AFTER=2 \
    CURL_WAIT_FOR_FILE="${socat_ready_file}" \
    CURL_WAIT_FOR_FILE_AFTER_COUNT=2 \
    SOCAT_LOG="${socat_log}" \
    SOCAT_READY_FILE="${socat_ready_file}" \
    TICKET_PHONE_BRIDGE_PORT=9388 \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.01 \
    TICKET_PHONE_HEALTH_FAILURE_THRESHOLD=1 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1

  grep -F "remove test-device:5555 tcp:19389" "${adb_log}" >/dev/null || fail "bridge recovery did not remove the failed forward"
  grep -F "connect test-device:5555" "${adb_log}" >/dev/null || fail "bridge recovery did not reconnect the unavailable ADB target"
  grep -F "set test-device:5555 tcp:19389 tcp:9388" "${adb_log}" >/dev/null || fail "bridge recovery did not recreate the forward"
  grep -F "started" "${socat_log}" >/dev/null || fail "bridge recovery did not reopen the listener"
}

test_loop_tolerates_one_transient_health_failure() {
  local fake_bin="${TMP_DIR}/bin-loop-transient"
  local socat_log="${TMP_DIR}/socat-loop-transient.log"
  local curl_count="${TMP_DIR}/curl-loop-transient-count"
  make_fake_bin "${fake_bin}"
  make_self_stopping_socat "${fake_bin}"

  run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_COUNT_FILE="${curl_count}" \
    CURL_FAIL_ON_COUNT=2 \
    CURL_RELEASE_ON_COUNT=3 \
    SOCAT_LOG="${socat_log}" \
    SOCAT_RELEASE_FILE="${TMP_DIR}/socat-transient-release" \
    TICKET_PHONE_BRIDGE_PORT=9388 \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.01 \
    TICKET_PHONE_HEALTH_FAILURE_RETRY_DELAY=0.01 \
    TICKET_PHONE_HEALTH_FAILURE_THRESHOLD=2 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1

  grep -F "exited" "${socat_log}" >/dev/null || fail "bridge listener did not finish its simulated healthy lifetime"
  if grep -F "terminated" "${socat_log}" >/dev/null 2>&1; then
    fail "bridge loop tore down the listener after one transient health miss"
  fi
}

test_loop_stops_listener_after_failure_threshold() {
  local fake_bin="${TMP_DIR}/bin-loop-threshold"
  local socat_log="${TMP_DIR}/socat-loop-threshold.log"
  local curl_count="${TMP_DIR}/curl-loop-threshold-count"
  make_fake_bin "${fake_bin}"
  make_persistent_socat "${fake_bin}"

  run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_COUNT_FILE="${curl_count}" \
    CURL_FAIL_AFTER=1 \
    SOCAT_LOG="${socat_log}" \
    TICKET_PHONE_BRIDGE_PORT=9388 \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.01 \
    TICKET_PHONE_HEALTH_FAILURE_RETRY_DELAY=0.01 \
    TICKET_PHONE_HEALTH_FAILURE_THRESHOLD=2 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1

  grep -F "terminated" "${socat_log}" >/dev/null || fail "bridge loop did not stop the stale listener after repeated failures"
}

test_loop_uses_adaptive_health_intervals() {
  local fake_bin="${TMP_DIR}/bin-loop-adaptive"
  local socat_log="${TMP_DIR}/socat-loop-adaptive.log"
  local curl_count="${TMP_DIR}/curl-loop-adaptive-count"
  local sleep_log="${TMP_DIR}/sleep-loop-adaptive.log"
  make_fake_bin "${fake_bin}"
  make_persistent_socat "${fake_bin}"
  cat >"${fake_bin}/sleep" <<'SH'
#!/usr/bin/env sh
set -eu
printf '%s\n' "${1:-}" >>"${SLEEP_LOG:?}"
command -p sleep 0.005
SH
  chmod +x "${fake_bin}/sleep"

  run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_COUNT_FILE="${curl_count}" \
    CURL_FAIL_AFTER=3 \
    SLEEP_LOG="${sleep_log}" \
    SOCAT_LOG="${socat_log}" \
    TICKET_PHONE_BRIDGE_PORT=9388 \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.01 \
    TICKET_PHONE_HEALTH_STABLE_INTERVAL=0.03 \
    TICKET_PHONE_HEALTH_STABLE_AFTER=2 \
    TICKET_PHONE_HEALTH_FAILURE_RETRY_DELAY=0.005 \
    TICKET_PHONE_HEALTH_FAILURE_THRESHOLD=2 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1

  grep -Fx "0.03" "${sleep_log}" >/dev/null || fail "stable bridge did not switch to the lower-frequency health interval"
  grep -Fx "0.005" "${sleep_log}" >/dev/null || fail "failed bridge did not use the short confirmation interval"
}

test_loop_event_reporting_is_bounded_and_nonblocking() {
  local fake_bin="${TMP_DIR}/bin-loop-events"
  local event_dir="${TMP_DIR}/events"
  local event_log="${event_dir}/event.log"
  local event_pids="${event_dir}/event-pids"
  local socat_log="${TMP_DIR}/socat-loop-events.log"
  mkdir -p "${event_dir}"
  make_fake_bin "${fake_bin}"
  make_persistent_socat "${fake_bin}"

  run_with_fake_phone_tools "${fake_bin}" \
    EVENT_LOG="${event_log}" \
    EVENT_PID_FILE="${event_pids}" \
    EVENT_CURL_SLEEP=0.5 \
    SOCAT_LOG="${socat_log}" \
    TICKET_PHONE_BRIDGE_PORT=9388 \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.01 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    TICKET_PHONE_BRIDGE_EVENT_SINK_URL="http://event-sink.invalid/events" \
    TICKET_PHONE_BRIDGE_EVENT_SINK_TOKEN="test-token" \
    TICKET_PHONE_BRIDGE_EVENT_CONNECT_TIMEOUT=0.25 \
    TICKET_PHONE_BRIDGE_EVENT_TIMEOUT=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1 &
  local loop_pid="$!"

  wait_for_nonempty_file "${socat_log}" "blocked event reporting prevented bridge startup"
  wait_for_nonempty_file "${event_log}" "asynchronous event reporting did not start"
  kill "${loop_pid}" >/dev/null 2>&1 || true
  wait "${loop_pid}" >/dev/null 2>&1 || true
  grep -F -- "--connect-timeout 0.25" "${event_log}" >/dev/null || fail "event reporting did not carry a bounded connect timeout"
  grep -F -- "--max-time 1" "${event_log}" >/dev/null || fail "event reporting did not carry a bounded total timeout"
}

if [ ! -x "${LOOP_SCRIPT}" ] || [ ! -x "${HEALTH_SCRIPT}" ]; then
  fail "ticket phone bridge scripts must remain executable"
fi

test_health_fast_path_is_endpoint_only
test_health_strict_accepts_valid_forward
test_health_strict_requires_adb_state
test_health_strict_rejects_missing_forward
test_health_rejects_unavailable_endpoint
test_health_requires_exact_http_200
test_health_requires_top_level_ok_true
test_loop_reuses_existing_forward_without_adb_setup
test_loop_recovers_failed_existing_forward
test_loop_tolerates_one_transient_health_failure
test_loop_stops_listener_after_failure_threshold
test_loop_uses_adaptive_health_intervals
test_loop_event_reporting_is_bounded_and_nonblocking

echo "ticket phone bridge hardening checks passed"
