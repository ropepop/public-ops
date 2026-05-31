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

socat_pid=""

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
  adb connect "${TICKET_PHONE_ADB_TARGET}" >/dev/null 2>&1 || true
  if adb -s "${TICKET_PHONE_ADB_TARGET}" get-state >/dev/null 2>&1 \
    && adb -s "${TICKET_PHONE_ADB_TARGET}" forward "tcp:${TICKET_PHONE_ADB_FORWARD_PORT}" "tcp:${TICKET_PHONE_DEVICE_PORT}" >/dev/null 2>&1 \
    && "${TICKET_PHONE_HEALTH_COMMAND}" >/dev/null 2>&1; then
    socat \
      "TCP-LISTEN:${TICKET_PHONE_BRIDGE_PORT},fork,reuseaddr,bind=0.0.0.0" \
      "TCP:127.0.0.1:${TICKET_PHONE_ADB_FORWARD_PORT}" &
    socat_pid="$!"
    while kill -0 "${socat_pid}" >/dev/null 2>&1; do
      sleep "${TICKET_PHONE_HEALTH_INTERVAL}"
      if ! "${TICKET_PHONE_HEALTH_COMMAND}" >/dev/null 2>&1; then
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
  fi
  if [ "${TICKET_PHONE_MAX_CYCLES}" -gt 0 ] && [ "${cycle_count}" -ge "${TICKET_PHONE_MAX_CYCLES}" ]; then
    exit 0
  fi
  sleep "${TICKET_PHONE_RETRY_DELAY}"
done
