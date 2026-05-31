#!/bin/sh
set -eu

: "${TICKET_PHONE_ADB_TARGET:=100.76.50.43:5555}"
: "${TICKET_PHONE_DEVICE_PORT:=9388}"
: "${TICKET_PHONE_ADB_FORWARD_PORT:=19389}"
: "${TICKET_PHONE_HEALTH_TIMEOUT:=2}"

expected_forward="${TICKET_PHONE_ADB_TARGET} tcp:${TICKET_PHONE_ADB_FORWARD_PORT} tcp:${TICKET_PHONE_DEVICE_PORT}"

if ! adb -s "${TICKET_PHONE_ADB_TARGET}" get-state >/dev/null 2>&1; then
  echo "ticket phone bridge health failed: ADB target is not connected: ${TICKET_PHONE_ADB_TARGET}" >&2
  exit 1
fi

if ! adb -s "${TICKET_PHONE_ADB_TARGET}" forward --list | grep -Fx "${expected_forward}" >/dev/null 2>&1; then
  echo "ticket phone bridge health failed: missing ADB forward: ${expected_forward}" >&2
  exit 1
fi

if ! curl -fsS --max-time "${TICKET_PHONE_HEALTH_TIMEOUT}" "http://127.0.0.1:${TICKET_PHONE_ADB_FORWARD_PORT}/api/v1/health" >/dev/null; then
  echo "ticket phone bridge health failed: forwarded phone health endpoint is unavailable" >&2
  exit 1
fi
