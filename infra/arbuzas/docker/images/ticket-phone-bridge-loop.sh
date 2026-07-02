#!/bin/sh
set -eu

: "${TICKET_PHONE_ADB_TARGET:=100.76.50.43:5555}"
: "${TICKET_PHONE_DEVICE_PORT:=9388}"
: "${TICKET_PHONE_ADB_FORWARD_PORT:=19389}"
: "${TICKET_PHONE_BRIDGE_PORT:=9388}"
: "${TICKET_PHONE_RETRY_DELAY:=2}"
: "${TICKET_PHONE_HEALTH_INTERVAL:=5}"
: "${TICKET_PHONE_HEALTH_COMMAND:=/usr/local/bin/ticket-phone-bridge-health}"
: "${TICKET_PHONE_MAX_CYCLES:=0}"
: "${TICKET_PHONE_BRIDGE_EVENT_SINK_URL:=}"
: "${TICKET_PHONE_BRIDGE_EVENT_SINK_TOKEN:=}"

socat_pid=""

emit_event() {
  if [ -z "${TICKET_PHONE_BRIDGE_EVENT_SINK_URL}" ] || [ -z "${TICKET_PHONE_BRIDGE_EVENT_SINK_TOKEN}" ]; then
    return 0
  fi
  action="${1:-event}"
  status="${2:-ok}"
  reason="${3:-}"
  count="${4:-0}"
  body='{"source":"ticket_phone_bridge","category":"bridge","action":"'"${action}"'","status":"'"${status}"'","reason":"'"${reason}"'","backendId":"pixel","safeState":{"cycle":'"${cycle_count:-0}"'},"count":'"${count}"'}'
  curl -fsS \
    -H "Authorization: Bearer ${TICKET_PHONE_BRIDGE_EVENT_SINK_TOKEN}" \
    -H "Content-Type: application/json" \
    --max-time 2 \
    --data "${body}" \
    "${TICKET_PHONE_BRIDGE_EVENT_SINK_URL}" >/dev/null 2>&1 || true
}

cleanup() {
  if [ -n "${socat_pid}" ]; then
    kill "${socat_pid}" >/dev/null 2>&1 || true
    wait "${socat_pid}" >/dev/null 2>&1 || true
    socat_pid=""
  fi
  adb -s "${TICKET_PHONE_ADB_TARGET}" forward --remove "tcp:${TICKET_PHONE_ADB_FORWARD_PORT}" >/dev/null 2>&1 || true
}

terminate() {
  cleanup
  exit 0
}

trap cleanup EXIT
trap terminate INT TERM

cycle_count=0
while :; do
  cycle_count=$((cycle_count + 1))
  cleanup
  emit_event "loop_cycle_started" "ok" "bridge_loop" "${cycle_count}"
  adb connect "${TICKET_PHONE_ADB_TARGET}" >/dev/null 2>&1 || true
  if ! adb -s "${TICKET_PHONE_ADB_TARGET}" get-state >/dev/null 2>&1; then
    emit_event "adb_connect_failed" "failed" "adb_state_unavailable" "${cycle_count}"
  elif ! adb -s "${TICKET_PHONE_ADB_TARGET}" forward "tcp:${TICKET_PHONE_ADB_FORWARD_PORT}" "tcp:${TICKET_PHONE_DEVICE_PORT}" >/dev/null 2>&1; then
    emit_event "adb_forward_failed" "failed" "adb_forward_unavailable" "${cycle_count}"
  elif ! "${TICKET_PHONE_HEALTH_COMMAND}" >/dev/null 2>&1; then
    emit_event "forwarded_phone_health_failed" "failed" "phone_health_unavailable" "${cycle_count}"
  else
    emit_event "adb_forward_ready" "ok" "phone_health_ok" "${cycle_count}"
    socat \
      "TCP-LISTEN:${TICKET_PHONE_BRIDGE_PORT},fork,reuseaddr,bind=0.0.0.0" \
      "TCP:127.0.0.1:${TICKET_PHONE_ADB_FORWARD_PORT}" &
    socat_pid="$!"
    emit_event "proxy_opened" "ok" "socat_started" "${cycle_count}"
    while kill -0 "${socat_pid}" >/dev/null 2>&1; do
      sleep "${TICKET_PHONE_HEALTH_INTERVAL}"
      if ! "${TICKET_PHONE_HEALTH_COMMAND}" >/dev/null 2>&1; then
        emit_event "forwarded_phone_health_failed" "failed" "health_check_failed" "${cycle_count}"
        kill "${socat_pid}" >/dev/null 2>&1 || true
        wait "${socat_pid}" >/dev/null 2>&1 || true
        socat_pid=""
        break
      fi
    done
    if [ -n "${socat_pid}" ]; then
      wait "${socat_pid}" >/dev/null 2>&1 || true
      socat_pid=""
    fi
    emit_event "proxy_closed" "ok" "socat_stopped" "${cycle_count}"
  fi
  if [ "${TICKET_PHONE_MAX_CYCLES}" -gt 0 ] && [ "${cycle_count}" -ge "${TICKET_PHONE_MAX_CYCLES}" ]; then
    exit 0
  fi
  sleep "${TICKET_PHONE_RETRY_DELAY}"
done
