#!/usr/bin/env bash
set -euo pipefail

# Compatibility entry point for callers that still use the historical path.
# It can only forward complete run records to the canonical one-table logger;
# it never calls a reducer in the retired deployment-timing database.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CANONICAL_REPORTER="${SCRIPT_DIR}/../../operational-logging/scripts/report-deployment.sh"
CANONICAL_DATABASE="operational-logging-prod"

fail_migration() {
  printf 'deployment timing reporter: %s\n' "$1" >&2
  printf 'Use %s with run-start or run-complete.\n' "${CANONICAL_REPORTER}" >&2
  exit 2
}

[[ -x "${CANONICAL_REPORTER}" ]] || fail_migration "canonical operational logging reporter is unavailable"

command_name="${1:-}"
case "${command_name}" in
  run-start|run-complete) ;;
  -h|--help)
    exec "${CANONICAL_REPORTER}" run-start --help
    ;;
  phase|run-finish)
    fail_migration "${command_name} is retired; send one atomic run-complete record"
    ;;
  "")
    fail_migration "a command is required"
    ;;
  *)
    fail_migration "unsupported command: ${command_name}"
    ;;
esac

legacy_database="${DEPLOY_TIMING_SPACETIME_DATABASE:-}"
[[ -z "${legacy_database}" || "${legacy_database}" == "${CANONICAL_DATABASE}" ]] || \
  fail_migration "the legacy database override is retired"

configured_database="${OPERATIONAL_LOGGING_DATABASE:-${CANONICAL_DATABASE}}"
[[ "${configured_database}" == "${CANONICAL_DATABASE}" ]] || \
  fail_migration "the compatibility entry point only writes ${CANONICAL_DATABASE}"

args=("$@")
for ((index = 0; index < ${#args[@]}; index++)); do
  if [[ "${args[index]}" == "--database" ]]; then
    (( index + 1 < ${#args[@]} )) || fail_migration "--database requires a value"
    [[ "${args[index + 1]}" == "${CANONICAL_DATABASE}" ]] || \
      fail_migration "the compatibility entry point only writes ${CANONICAL_DATABASE}"
  fi
done

# Preserve harmless historical environment names while routing them to the
# canonical reporter. The database itself is deliberately not configurable.
if [[ -z "${OPERATIONAL_LOGGING_HOST:-}" && -n "${DEPLOY_TIMING_SPACETIME_SERVER:-}" ]]; then
  export OPERATIONAL_LOGGING_HOST="${DEPLOY_TIMING_SPACETIME_SERVER}"
fi
if [[ -z "${OPERATIONAL_LOGGING_SPACETIME_BIN:-}" && -n "${DEPLOY_TIMING_SPACETIME_BIN:-}" ]]; then
  export OPERATIONAL_LOGGING_SPACETIME_BIN="${DEPLOY_TIMING_SPACETIME_BIN}"
fi
if [[ -z "${OPERATIONAL_LOGGING_SPACETIME_ROOT:-}" && -n "${DEPLOY_TIMING_SPACETIME_ROOT:-}" ]]; then
  export OPERATIONAL_LOGGING_SPACETIME_ROOT="${DEPLOY_TIMING_SPACETIME_ROOT}"
fi
if [[ -z "${OPERATIONAL_LOGGING_RETRY_ATTEMPTS:-}" && -n "${DEPLOY_TIMING_RETRY_ATTEMPTS:-}" ]]; then
  export OPERATIONAL_LOGGING_RETRY_ATTEMPTS="${DEPLOY_TIMING_RETRY_ATTEMPTS}"
fi
if [[ -z "${OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS:-}" && -n "${DEPLOY_TIMING_RETRY_BASE_DELAY_SECONDS:-}" ]]; then
  export OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS="${DEPLOY_TIMING_RETRY_BASE_DELAY_SECONDS}"
fi
export OPERATIONAL_LOGGING_DATABASE="${CANONICAL_DATABASE}"

exec "${CANONICAL_REPORTER}" "$@"
