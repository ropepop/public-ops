#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HEALTH_SCRIPT="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge-health.sh"
LOOP_SCRIPT="${REPO_ROOT}/infra/arbuzas/docker/images/ticket-phone-bridge-loop.sh"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

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
    if [ "${ADB_GET_STATE_FAIL:-0}" = 1 ]; then
      exit 1
    fi
    printf 'device\n'
    exit 0
    ;;
  forward)
    case "${2:-}" in
      --list)
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

printf 'unexpected adb call: %s %s %s\n' "${target}" "$*" >>"${ADB_LOG:?}"
exit 1
SH
  chmod +x "${fake_bin}/adb"

  cat >"${fake_bin}/curl" <<'SH'
#!/usr/bin/env sh
set -eu

if [ -n "${CURL_COUNT_FILE:-}" ]; then
  count=0
  if [ -f "${CURL_COUNT_FILE}" ]; then
    count="$(cat "${CURL_COUNT_FILE}")"
  fi
  count=$((count + 1))
  printf '%s\n' "${count}" >"${CURL_COUNT_FILE}"
  if [ "${CURL_FAIL_AFTER:-0}" -gt 0 ] && [ "${count}" -gt "${CURL_FAIL_AFTER}" ]; then
    exit 7
  fi
fi

if [ "${CURL_FAIL:-0}" = 1 ]; then
  exit 7
fi

printf 'ok\n'
SH
  chmod +x "${fake_bin}/curl"
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

test_health_script_accepts_valid_forward() {
  local fake_bin="${TMP_DIR}/bin-health-ok"
  make_fake_bin "${fake_bin}"
  run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    "${HEALTH_SCRIPT}" >/dev/null
}

test_health_script_rejects_missing_forward() {
  local fake_bin="${TMP_DIR}/bin-health-missing-forward"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19390 tcp:9388" \
    "${HEALTH_SCRIPT}" >/dev/null 2>&1; then
    fail "bridge health passed even though the expected ADB forward was missing"
  fi
}

test_health_script_rejects_dead_forward() {
  local fake_bin="${TMP_DIR}/bin-health-dead-forward"
  make_fake_bin "${fake_bin}"
  if run_with_fake_phone_tools "${fake_bin}" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_FAIL=1 \
    "${HEALTH_SCRIPT}" >/dev/null 2>&1; then
    fail "bridge health passed even though the forwarded phone health endpoint was dead"
  fi
}

test_loop_stops_stale_listener_when_forward_dies() {
  local fake_bin="${TMP_DIR}/bin-loop"
  local socat_log="${TMP_DIR}/socat.log"
  local curl_count="${TMP_DIR}/curl-count"
  make_fake_bin "${fake_bin}"
  cat >"${fake_bin}/socat" <<'SH'
#!/usr/bin/env sh
set -eu

printf 'started\n' >>"${SOCAT_LOG:?}"
trap 'printf "terminated\n" >>"${SOCAT_LOG:?}"; exit 0' TERM INT
while :; do
  sleep 1
done
SH
  chmod +x "${fake_bin}/socat"

  env \
    PATH="${fake_bin}:${PATH}" \
    ADB_LOG="${TMP_DIR}/adb-loop.log" \
    ADB_FORWARD_LIST="test-device:5555 tcp:19389 tcp:9388" \
    CURL_COUNT_FILE="${curl_count}" \
    CURL_FAIL_AFTER=1 \
    SOCAT_LOG="${socat_log}" \
    TICKET_PHONE_ADB_TARGET="test-device:5555" \
    TICKET_PHONE_DEVICE_PORT="9388" \
    TICKET_PHONE_ADB_FORWARD_PORT="19389" \
    TICKET_PHONE_BRIDGE_PORT="9388" \
    TICKET_PHONE_HEALTH_COMMAND="${HEALTH_SCRIPT}" \
    TICKET_PHONE_HEALTH_INTERVAL=0.1 \
    TICKET_PHONE_RETRY_DELAY=0 \
    TICKET_PHONE_MAX_CYCLES=1 \
    "${LOOP_SCRIPT}" >/dev/null 2>&1 &
  local loop_pid="$!"
  local deadline=$((SECONDS + 5))
  while kill -0 "${loop_pid}" >/dev/null 2>&1; do
    if [ "${SECONDS}" -ge "${deadline}" ]; then
      kill "${loop_pid}" >/dev/null 2>&1 || true
      wait "${loop_pid}" >/dev/null 2>&1 || true
      fail "bridge loop did not exit after the watchdog detected a dead forward"
    fi
    sleep 0.1
  done
  wait "${loop_pid}"

  grep -F "started" "${socat_log}" >/dev/null || fail "bridge loop did not start the listener"
  grep -F "terminated" "${socat_log}" >/dev/null || fail "bridge loop did not stop the stale listener"
  grep -F "remove test-device:5555 tcp:19389" "${TMP_DIR}/adb-loop.log" >/dev/null || fail "bridge loop did not clean up the stale ADB forward"
}

if [ ! -x "${LOOP_SCRIPT}" ]; then
  fail "missing executable bridge loop at ${LOOP_SCRIPT}"
fi

test_health_script_accepts_valid_forward
test_health_script_rejects_missing_forward
test_health_script_rejects_dead_forward
test_loop_stops_stale_listener_when_forward_dies

echo "ticket phone bridge hardening checks passed"
