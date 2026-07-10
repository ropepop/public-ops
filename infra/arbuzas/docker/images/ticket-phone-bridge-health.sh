#!/bin/sh
set -eu

: "${TICKET_PHONE_ADB_TARGET:=100.76.50.43:5555}"
: "${TICKET_PHONE_DEVICE_PORT:=9388}"
: "${TICKET_PHONE_ADB_FORWARD_PORT:=19389}"
: "${TICKET_PHONE_HEALTH_TIMEOUT:=2}"
: "${TICKET_PHONE_HEALTH_MODE:=fast}"
: "${TICKET_PHONE_HEALTH_DIAGNOSTICS:=1}"

case "${1:-}" in
  --strict)
    TICKET_PHONE_HEALTH_MODE="strict"
    ;;
  --fast)
    TICKET_PHONE_HEALTH_MODE="fast"
    ;;
  "")
    ;;
  *)
    echo "usage: ticket-phone-bridge-health [--fast|--strict]" >&2
    exit 2
    ;;
esac

case "${TICKET_PHONE_HEALTH_MODE}" in
  fast|strict)
    ;;
  *)
    echo "ticket phone bridge health failed: invalid health mode: ${TICKET_PHONE_HEALTH_MODE}" >&2
    exit 2
    ;;
esac

health_url="http://127.0.0.1:${TICKET_PHONE_ADB_FORWARD_PORT}/api/v1/health"
expected_forward="${TICKET_PHONE_ADB_TARGET} tcp:${TICKET_PHONE_ADB_FORWARD_PORT} tcp:${TICKET_PHONE_DEVICE_PORT}"
endpoint_reason=""

probe_endpoint() {
  if ! health_response="$(
    curl -sS \
      --max-time "${TICKET_PHONE_HEALTH_TIMEOUT}" \
      --write-out '\n__TICKET_HTTP_STATUS__:%{http_code}' \
      "${health_url}"
  )"; then
    endpoint_reason="forwarded phone health endpoint is unavailable"
    return 1
  fi

  http_status="${health_response##*__TICKET_HTTP_STATUS__:}"
  if [ "${http_status}" != "200" ]; then
    endpoint_reason="forwarded phone health endpoint returned HTTP ${http_status}"
    return 1
  fi

  health_body="${health_response%__TICKET_HTTP_STATUS__:*}"
  compact_health_body="$(printf '%s' "${health_body}" | tr -d '[:space:]')"
  case "${compact_health_body}" in
    '{"ok":true}'|'{"ok":true,'*)
      return 0
      ;;
    *)
      endpoint_reason="forwarded phone health endpoint did not report ok=true"
      return 1
      ;;
  esac
}

endpoint_ok=0
if probe_endpoint; then
  endpoint_ok=1
  if [ "${TICKET_PHONE_HEALTH_MODE}" = "fast" ]; then
    exit 0
  fi
fi

if [ "${TICKET_PHONE_HEALTH_MODE}" = "strict" ] || [ "${TICKET_PHONE_HEALTH_DIAGNOSTICS}" = "1" ]; then
  if ! adb -s "${TICKET_PHONE_ADB_TARGET}" get-state >/dev/null 2>&1; then
    echo "ticket phone bridge health failed: ADB target is not connected: ${TICKET_PHONE_ADB_TARGET}" >&2
    exit 1
  fi

  if ! adb -s "${TICKET_PHONE_ADB_TARGET}" forward --list | grep -Fx "${expected_forward}" >/dev/null 2>&1; then
    echo "ticket phone bridge health failed: missing ADB forward: ${expected_forward}" >&2
    exit 1
  fi
fi

if [ "${endpoint_ok}" != "1" ]; then
  echo "ticket phone bridge health failed: ${endpoint_reason}" >&2
  exit 1
fi
