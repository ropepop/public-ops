#!/usr/bin/env bash
set -uo pipefail

# Best-effort deployment reporter for the canonical private operational log.
# Only compact identifiers and durations reach SpacetimeDB. Normal callers do
# not wait, and reporting can never change the deployment result.

usage() {
  cat <<'USAGE'
Usage:
  report-deployment.sh run-start --run-id ID --source ops|pixel --action ACTION [options]
  report-deployment.sh run-complete --run-id ID --source ops|pixel --action ACTION \
    --status ok|failed|cancelled --total-duration-ms MS \
    --phase-bundle 'phase=status=duration=total@...' [options]

Options:
  --release-id ID             Defaults to none.
  --profile NAME              Defaults to none.
  --target NAME               Defaults to none.
  --phase-bundle BUNDLE       Up to 64 phase=status=duration=total entries.
  --event-id ID               Optional deterministic event id override.
  --database NAME             Defaults to $OPERATIONAL_LOGGING_DATABASE
                              or operational-logging-prod.
  --server URL                Defaults to $OPERATIONAL_LOGGING_HOST
                              or https://maincloud.spacetimedb.com.
  --spacetime PATH            Defaults to $OPERATIONAL_LOGGING_SPACETIME_BIN or spacetime.
  --spacetime-root PATH       Optional isolated Spacetime CLI auth directory.
  --wait                      Send in the foreground.
  --strict                    Surface a failed call; also implies --wait.
  -h, --help                  Show this help.

Never pass command output, error text, credentials, control codes, customer
data, or another free-form value. Reporting is not deployment-critical.
USAGE
}

warn_or_fail() {
  local message="$1"
  if (( strict_mode == 1 )); then
    printf 'operational deployment reporter: %s\n' "${message}" >&2
    exit 1
  fi
  printf 'operational deployment reporter (best effort): %s\n' "${message}" >&2
  exit 0
}

fail_usage() {
  printf 'operational deployment reporter: %s\n' "$1" >&2
  usage >&2
  exit 2
}

is_safe_token() {
  local value="$1"
  local max_len="$2"
  [[ -n "${value}" && ${#value} -le "${max_len}" && "${value}" =~ ^[A-Za-z0-9][A-Za-z0-9._:/@=-]*$ ]]
}

require_safe_token() {
  local label="$1"
  local value="$2"
  local max_len="$3"
  is_safe_token "${value}" "${max_len}" || fail_usage "${label} must be a 1-${max_len} character safe token"
}

require_millis() {
  local label="$1"
  local value="$2"
  [[ "${value}" =~ ^[0-9]+$ ]] || fail_usage "${label} must be a non-negative integer"
  (( ${#value} <= 9 )) || fail_usage "${label} must not exceed seven days"
  (( 10#${value} <= 604800000 )) || fail_usage "${label} must not exceed seven days"
}

require_phase_bundle() {
  local bundle="$1"
  local entry=""
  local phase_name=""
  local phase_status=""
  local phase_duration=""
  local phase_total=""
  local phase_extra=""
  local -a entries=()

  [[ "${bundle}" == "-" ]] && return 0
  [[ -n "${bundle}" && ${#bundle} -le 8256 ]] || fail_usage "phase bundle must be a compact safe value"
  [[ "${bundle}" != @* && "${bundle}" != *@ && "${bundle}" != *@@* ]] || fail_usage "phase bundle has an empty entry"
  IFS='@' read -r -a entries <<< "${bundle}"
  (( ${#entries[@]} <= 64 )) || fail_usage "phase bundle must not exceed 64 entries"

  for entry in "${entries[@]}"; do
    phase_name=""
    phase_status=""
    phase_duration=""
    phase_total=""
    phase_extra=""
    IFS='=' read -r phase_name phase_status phase_duration phase_total phase_extra <<< "${entry}"
    [[ -n "${phase_name}" && -n "${phase_status}" && -n "${phase_duration}" && -n "${phase_total}" && -z "${phase_extra}" ]] || \
      fail_usage "phase bundle entries must be phase=status=duration=total"
    require_safe_token "phase bundle phase" "${phase_name}" 100
    case "${phase_status}" in ok|failed|skipped) ;; *) fail_usage "phase bundle status must be ok, failed, or skipped" ;; esac
    require_millis "phase bundle duration" "${phase_duration}"
    require_millis "phase bundle total duration" "${phase_total}"
  done
}

json_string() {
  # Every string has already passed the safe ASCII token checks.
  printf '"%s"' "$1"
}

command_name="${1:-}"
[[ -n "${command_name}" ]] || fail_usage "a command is required"
shift

run_id=""
source=""
action=""
status=""
release_id="none"
profile="none"
target="none"
total_duration_ms="0"
phase_bundle=""
phase_bundle_provided=0
event_id=""
database="${OPERATIONAL_LOGGING_DATABASE:-operational-logging-prod}"
server="${OPERATIONAL_LOGGING_HOST:-https://maincloud.spacetimedb.com}"
spacetime_bin="${OPERATIONAL_LOGGING_SPACETIME_BIN:-spacetime}"
spacetime_root="${OPERATIONAL_LOGGING_SPACETIME_ROOT:-}"
async_mode=1
strict_mode=0
retry_attempts="${OPERATIONAL_LOGGING_RETRY_ATTEMPTS:-7}"
retry_base_delay_seconds="${OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS:-1}"

while (( $# > 0 )); do
  case "$1" in
    --run-id) (( $# >= 2 )) || fail_usage "--run-id requires a value"; run_id="$2"; shift 2 ;;
    --source) (( $# >= 2 )) || fail_usage "--source requires a value"; source="$2"; shift 2 ;;
    --action) (( $# >= 2 )) || fail_usage "--action requires a value"; action="$2"; shift 2 ;;
    --status) (( $# >= 2 )) || fail_usage "--status requires a value"; status="$2"; shift 2 ;;
    --release-id) (( $# >= 2 )) || fail_usage "--release-id requires a value"; release_id="$2"; shift 2 ;;
    --profile) (( $# >= 2 )) || fail_usage "--profile requires a value"; profile="$2"; shift 2 ;;
    --target) (( $# >= 2 )) || fail_usage "--target requires a value"; target="$2"; shift 2 ;;
    --total-duration-ms) (( $# >= 2 )) || fail_usage "--total-duration-ms requires a value"; total_duration_ms="$2"; shift 2 ;;
    --phase-bundle) (( $# >= 2 )) || fail_usage "--phase-bundle requires a value"; phase_bundle="$2"; phase_bundle_provided=1; shift 2 ;;
    --event-id) (( $# >= 2 )) || fail_usage "--event-id requires a value"; event_id="$2"; shift 2 ;;
    --database) (( $# >= 2 )) || fail_usage "--database requires a value"; database="$2"; shift 2 ;;
    --server) (( $# >= 2 )) || fail_usage "--server requires a value"; server="$2"; shift 2 ;;
    --spacetime) (( $# >= 2 )) || fail_usage "--spacetime requires a value"; spacetime_bin="$2"; shift 2 ;;
    --spacetime-root) (( $# >= 2 )) || fail_usage "--spacetime-root requires a value"; spacetime_root="$2"; shift 2 ;;
    --wait) async_mode=0; shift ;;
    --strict) strict_mode=1; async_mode=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail_usage "unknown option: $1" ;;
  esac
done

[[ "${retry_attempts}" =~ ^[0-9]+$ ]] && (( retry_attempts >= 1 && retry_attempts <= 10 )) || \
  fail_usage "OPERATIONAL_LOGGING_RETRY_ATTEMPTS must be between 1 and 10"
[[ "${retry_base_delay_seconds}" =~ ^[0-9]+$ ]] && (( retry_base_delay_seconds <= 30 )) || \
  fail_usage "OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS must be between 0 and 30"
if (( strict_mode == 1 )); then
  retry_attempts=1
fi

require_safe_token "run id" "${run_id}" 120
require_safe_token "source" "${source}" 16
case "${source}" in ops|pixel) ;; *) fail_usage "source must be ops or pixel" ;; esac
require_safe_token "action" "${action}" 80
require_safe_token "release id" "${release_id}" 160
require_safe_token "profile" "${profile}" 48
require_safe_token "target" "${target}" 160
require_safe_token "database" "${database}" 120
require_millis "total duration" "${total_duration_ms}"

case "${command_name}" in
  run-start)
    status="${status:-running}"
    [[ "${status}" == "running" ]] || fail_usage "run-start status must be running"
    [[ -n "${event_id}" ]] || event_id="${run_id}:run:started:${total_duration_ms}"
    reducer="operationallog_append_deployment_run"
    reducer_args=(
      "$(json_string "${event_id}")" "$(json_string "${run_id}")" "$(json_string "${source}")"
      "$(json_string "${action}")" '"started"' '"running"'
      "$(json_string "${release_id}")" "$(json_string "${profile}")" "$(json_string "${target}")"
      "${total_duration_ms}"
    )
    ;;
  run-complete)
    (( phase_bundle_provided == 1 )) || fail_usage "run-complete requires --phase-bundle"
    case "${status}" in ok|failed|cancelled) ;; *) fail_usage "run-complete status must be ok, failed, or cancelled" ;; esac
    require_phase_bundle "${phase_bundle}"
    [[ -n "${event_id}" ]] || event_id="${run_id}:run:finished:${total_duration_ms}"
    reducer="operationallog_append_deployment_completed_run"
    reducer_args=(
      "$(json_string "${event_id}")" "$(json_string "${run_id}")" "$(json_string "${source}")"
      "$(json_string "${action}")" "$(json_string "${status}")" "$(json_string "${release_id}")"
      "$(json_string "${profile}")" "$(json_string "${target}")" "${total_duration_ms}"
      "$(json_string "${phase_bundle}")"
    )
    ;;
  *) fail_usage "command must be run-start or run-complete" ;;
esac

require_safe_token "event id" "${event_id}" 180
if ! command -v "${spacetime_bin}" >/dev/null 2>&1; then
  warn_or_fail "spacetime CLI is unavailable: ${spacetime_bin}"
fi

call=("${spacetime_bin}")
if [[ -n "${spacetime_root}" ]]; then
  call+=(--root-dir "${spacetime_root}")
fi
call+=(call --no-config -y --server "${server}" "${database}" "${reducer}")
call+=("${reducer_args[@]}")

if (( async_mode == 1 )); then
  nohup "${call[@]}" </dev/null >/dev/null 2>&1 &
  exit 0
fi

attempt=1
retry_delay_seconds="${retry_base_delay_seconds}"
while (( attempt <= retry_attempts )); do
  if "${call[@]}"; then
    exit 0
  fi
  if (( attempt == retry_attempts )); then
    break
  fi
  if (( retry_delay_seconds > 0 )); then
    sleep "${retry_delay_seconds}"
  fi
  if (( retry_delay_seconds < 30 )); then
    retry_delay_seconds=$((retry_delay_seconds * 2))
    if (( retry_delay_seconds > 30 )); then
      retry_delay_seconds=30
    fi
  fi
  attempt=$((attempt + 1))
done
warn_or_fail "SpacetimeDB call failed"
