#!/bin/sh
set -eu

: "${TICKET_PHONE_ADB_TARGET:=100.76.50.43:5555}"
: "${TICKET_PHONE_DEVICE_PORT:=9388}"
: "${TICKET_PHONE_ADB_FORWARD_PORT:=19389}"
: "${TICKET_PHONE_BRIDGE_PORT:=9388}"
: "${TICKET_PHONE_RETRY_DELAY:=2}"
: "${TICKET_PHONE_HEALTH_INTERVAL:=5}"
: "${TICKET_PHONE_HEALTH_STABLE_INTERVAL:=15}"
: "${TICKET_PHONE_HEALTH_STABLE_AFTER:=3}"
: "${TICKET_PHONE_HEALTH_FAILURE_RETRY_DELAY:=0.5}"
: "${TICKET_PHONE_HEALTH_FAILURE_THRESHOLD:=2}"
: "${TICKET_PHONE_HEALTH_COMMAND:=/usr/local/bin/ticket-phone-bridge-health}"
: "${TICKET_PHONE_MAX_CYCLES:=0}"
: "${TICKET_PHONE_BRIDGE_EVENT_SINK_URL:=}"
: "${TICKET_PHONE_BRIDGE_EVENT_SINK_TOKEN:=}"
: "${TICKET_PHONE_BRIDGE_EVENT_CONNECT_TIMEOUT:=0.25}"
: "${TICKET_PHONE_BRIDGE_EVENT_TIMEOUT:=1}"

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
    --connect-timeout "${TICKET_PHONE_BRIDGE_EVENT_CONNECT_TIMEOUT}" \
    --max-time "${TICKET_PHONE_BRIDGE_EVENT_TIMEOUT}" \
    --data "${body}" \
    "${TICKET_PHONE_BRIDGE_EVENT_SINK_URL}" </dev/null >/dev/null 2>&1 &
}

stop_proxy() {
  if [ -n "${socat_pid}" ]; then
    kill "${socat_pid}" >/dev/null 2>&1 || true
    wait "${socat_pid}" >/dev/null 2>&1 || true
    socat_pid=""
  fi
}

remove_forward() {
  adb -s "${TICKET_PHONE_ADB_TARGET}" forward --remove "tcp:${TICKET_PHONE_ADB_FORWARD_PORT}" >/dev/null 2>&1 || true
}

cleanup() {
  stop_proxy
  remove_forward
}

terminate() {
  cleanup
  exit 0
}

run_health() {
  TICKET_PHONE_HEALTH_DIAGNOSTICS=0 "${TICKET_PHONE_HEALTH_COMMAND}" >/dev/null 2>&1
}

recover_forward() {
  remove_forward
  if ! adb -s "${TICKET_PHONE_ADB_TARGET}" get-state >/dev/null 2>&1; then
    adb connect "${TICKET_PHONE_ADB_TARGET}" >/dev/null 2>&1 || true
  fi
  if ! adb -s "${TICKET_PHONE_ADB_TARGET}" get-state >/dev/null 2>&1; then
    emit_event "adb_connect_failed" "failed" "adb_state_unavailable" "${cycle_count}"
    return 1
  fi
  if ! adb -s "${TICKET_PHONE_ADB_TARGET}" forward "tcp:${TICKET_PHONE_ADB_FORWARD_PORT}" "tcp:${TICKET_PHONE_DEVICE_PORT}" >/dev/null 2>&1; then
    emit_event "adb_forward_failed" "failed" "adb_forward_unavailable" "${cycle_count}"
    return 1
  fi
  if ! run_health; then
    emit_event "forwarded_phone_health_failed" "failed" "phone_health_unavailable" "${cycle_count}"
    return 1
  fi
  emit_event "adb_forward_ready" "ok" "phone_health_ok" "${cycle_count}"
}

monitor_proxy() {
  health_interval="${TICKET_PHONE_HEALTH_INTERVAL}"
  health_successes=0
  health_failures=0

  while kill -0 "${socat_pid}" >/dev/null 2>&1; do
    sleep "${health_interval}"
    if ! kill -0 "${socat_pid}" >/dev/null 2>&1; then
      break
    fi
    if run_health; then
      if [ "${health_failures}" -gt 0 ]; then
        emit_event "forwarded_phone_health_recovered" "ok" "health_check_recovered" "${health_failures}"
      fi
      health_failures=0
      health_successes=$((health_successes + 1))
      if [ "${health_successes}" -ge "${TICKET_PHONE_HEALTH_STABLE_AFTER}" ]; then
        health_interval="${TICKET_PHONE_HEALTH_STABLE_INTERVAL}"
      else
        health_interval="${TICKET_PHONE_HEALTH_INTERVAL}"
      fi
      continue
    fi

    health_successes=0
    health_failures=$((health_failures + 1))
    emit_event "forwarded_phone_health_failed" "failed" "health_check_failed" "${health_failures}"
    if [ "${health_failures}" -ge "${TICKET_PHONE_HEALTH_FAILURE_THRESHOLD}" ]; then
      stop_proxy
      return 1
    fi
    health_interval="${TICKET_PHONE_HEALTH_FAILURE_RETRY_DELAY}"
  done

  if [ -n "${socat_pid}" ]; then
    wait "${socat_pid}" >/dev/null 2>&1 || true
    socat_pid=""
  fi
  return 0
}

trap cleanup EXIT
trap terminate INT TERM

cycle_count=0
while :; do
  cycle_count=$((cycle_count + 1))
  emit_event "loop_cycle_started" "ok" "bridge_loop" "${cycle_count}"

  if run_health; then
    emit_event "adb_forward_ready" "ok" "existing_phone_health_ok" "${cycle_count}"
    bridge_ready=1
  elif recover_forward; then
    bridge_ready=1
  else
    bridge_ready=0
  fi

  if [ "${bridge_ready}" = "1" ]; then
    socat \
      "TCP-LISTEN:${TICKET_PHONE_BRIDGE_PORT},fork,reuseaddr,bind=0.0.0.0" \
      "TCP:127.0.0.1:${TICKET_PHONE_ADB_FORWARD_PORT}" &
    socat_pid="$!"
    emit_event "proxy_opened" "ok" "socat_started" "${cycle_count}"
    if monitor_proxy; then
      emit_event "proxy_closed" "ok" "socat_stopped" "${cycle_count}"
    else
      emit_event "proxy_closed" "failed" "health_failure_threshold_reached" "${cycle_count}"
    fi
  fi

  if [ "${TICKET_PHONE_MAX_CYCLES}" -gt 0 ] && [ "${cycle_count}" -ge "${TICKET_PHONE_MAX_CYCLES}" ]; then
    exit 0
  fi
  sleep "${TICKET_PHONE_RETRY_DELAY}"
done
