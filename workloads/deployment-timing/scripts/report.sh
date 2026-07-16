#!/usr/bin/env bash
set -uo pipefail

# Best-effort deployment timing reporter. It only accepts compact safe tokens,
# never receives deployment output, and defaults to an asynchronous call so it
# cannot extend a deployment's critical path.

usage() {
  cat <<'USAGE'
Usage:
  report.sh run-start  --run-id ID --source ops|pixel --action ACTION [options]
  report.sh phase      --run-id ID --source ops|pixel --action ACTION --phase NAME \
                       --status ok|failed|skipped --duration-ms MS [options]
  report.sh run-finish --run-id ID --source ops|pixel --action ACTION \
                       --status ok|failed|cancelled --total-duration-ms MS [options]
  report.sh run-complete --run-id ID --source ops|pixel --action ACTION \
                         --status ok|failed|cancelled --total-duration-ms MS \
                         --phase-bundle 'phase=status=duration=total@...' [options]

Options:
  --release-id ID             Defaults to none.
  --profile NAME              Defaults to none.
  --target NAME               Defaults to none.
  --total-duration-ms MS      Defaults to 0 for run-start and phase.
  --phase-bundle BUNDLE       Up to 64 safe phase entries for run-complete.
                             Encode phase=status=durationMillis=totalDurationMillis
                             entries with @, or use - when no phase completed.
  --event-id ID               Optional deterministic event id override.
  --database NAME             Defaults to $DEPLOY_TIMING_SPACETIME_DATABASE or deployment-timing-prod.
  --server URL                Defaults to $DEPLOY_TIMING_SPACETIME_SERVER or https://maincloud.spacetimedb.com.
  --spacetime PATH            Defaults to $DEPLOY_TIMING_SPACETIME_BIN or spacetime.
  --spacetime-root PATH       Optional isolated Spacetime CLI root/auth directory.
  --wait                      Send in the foreground instead of asynchronously.
  --strict                    Return the Spacetime call failure (also implies --wait).
  -h, --help                  Show this help.

All persisted values are compact safe identifiers. Do not pass a token, error
output, password, code, or customer data. The default asynchronous mode exits
after handing off the report and never changes a deploy command's outcome.
USAGE
}

warn_or_fail() {
  local message="$1"
  if (( strict_mode == 1 )); then
    printf 'deployment timing reporter: %s\n' "$message" >&2
    exit 1
  fi
  printf 'deployment timing reporter (best effort): %s\n' "$message" >&2
  exit 0
}

fail_usage() {
  printf 'deployment timing reporter: %s\n' "$1" >&2
  usage >&2
  exit 2
}

is_safe_token() {
  local value="$1"
  local max_len="$2"
  [[ -n "$value" && ${#value} -le "$max_len" && "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._:/@=-]*$ ]]
}

require_safe_token() {
  local label="$1"
  local value="$2"
  local max_len="$3"
  is_safe_token "$value" "$max_len" || fail_usage "$label must be a 1-${max_len} character safe token"
}

require_millis() {
  local label="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || fail_usage "$label must be a non-negative integer"
  (( ${#value} <= 9 )) || fail_usage "$label must not exceed seven days"
  (( 10#$value <= 604800000 )) || fail_usage "$label must not exceed seven days"
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

  [[ "$bundle" == "-" ]] && return 0
  [[ -n "$bundle" && ${#bundle} -le 8256 ]] || fail_usage "phase bundle must be a compact safe value"
  [[ "$bundle" != @* && "$bundle" != *@ && "$bundle" != *@@* ]] || fail_usage "phase bundle has an empty entry"
  IFS='@' read -r -a entries <<< "$bundle"
  (( ${#entries[@]} <= 64 )) || fail_usage "phase bundle must not exceed 64 entries"

  for entry in "${entries[@]}"; do
    phase_name=""
    phase_status=""
    phase_duration=""
    phase_total=""
    phase_extra=""
    IFS='=' read -r phase_name phase_status phase_duration phase_total phase_extra <<< "$entry"
    [[ -n "$phase_name" && -n "$phase_status" && -n "$phase_duration" && -n "$phase_total" && -z "$phase_extra" ]] || \
      fail_usage "phase bundle entries must be phase=status=duration=total"
    require_safe_token "phase bundle phase" "$phase_name" 100
    case "$phase_status" in ok|failed|skipped) ;; *) fail_usage "phase bundle status must be ok, failed, or skipped" ;; esac
    require_millis "phase bundle duration" "$phase_duration"
    require_millis "phase bundle total duration" "$phase_total"
  done
}

json_string() {
  # All strings are validated as safe ASCII tokens before this point.
  printf '"%s"' "$1"
}

command_name="${1:-}"
[[ -n "$command_name" ]] || fail_usage "a command is required"
shift

run_id=""
source=""
action=""
phase=""
status=""
release_id="none"
profile="none"
target="none"
duration_ms="0"
total_duration_ms="0"
phase_bundle=""
phase_bundle_provided=0
event_id=""
database="${DEPLOY_TIMING_SPACETIME_DATABASE:-deployment-timing-prod}"
server="${DEPLOY_TIMING_SPACETIME_SERVER:-https://maincloud.spacetimedb.com}"
spacetime_bin="${DEPLOY_TIMING_SPACETIME_BIN:-spacetime}"
spacetime_root="${DEPLOY_TIMING_SPACETIME_ROOT:-}"
async_mode=1
strict_mode=0
retry_attempts="${DEPLOY_TIMING_RETRY_ATTEMPTS:-7}"
retry_base_delay_seconds="${DEPLOY_TIMING_RETRY_BASE_DELAY_SECONDS:-1}"

while (( $# > 0 )); do
  case "$1" in
    --run-id) run_id="${2:-}"; shift 2 ;;
    --source) source="${2:-}"; shift 2 ;;
    --action) action="${2:-}"; shift 2 ;;
    --phase) phase="${2:-}"; shift 2 ;;
    --status) status="${2:-}"; shift 2 ;;
    --release-id) release_id="${2:-}"; shift 2 ;;
    --profile) profile="${2:-}"; shift 2 ;;
    --target) target="${2:-}"; shift 2 ;;
    --duration-ms) duration_ms="${2:-}"; shift 2 ;;
    --total-duration-ms) total_duration_ms="${2:-}"; shift 2 ;;
    --phase-bundle) phase_bundle="${2:-}"; phase_bundle_provided=1; shift 2 ;;
    --event-id) event_id="${2:-}"; shift 2 ;;
    --database) database="${2:-}"; shift 2 ;;
    --server) server="${2:-}"; shift 2 ;;
    --spacetime) spacetime_bin="${2:-}"; shift 2 ;;
    --spacetime-root) spacetime_root="${2:-}"; shift 2 ;;
    --wait) async_mode=0; shift ;;
    --strict) strict_mode=1; async_mode=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail_usage "unknown option: $1" ;;
  esac
done

[[ "${retry_attempts}" =~ ^[0-9]+$ ]] && (( retry_attempts >= 1 && retry_attempts <= 10 )) || \
  fail_usage "DEPLOY_TIMING_RETRY_ATTEMPTS must be between 1 and 10"
[[ "${retry_base_delay_seconds}" =~ ^[0-9]+$ ]] && (( retry_base_delay_seconds <= 30 )) || \
  fail_usage "DEPLOY_TIMING_RETRY_BASE_DELAY_SECONDS must be between 0 and 30"
if (( strict_mode == 1 )); then
  retry_attempts=1
fi

require_safe_token "run id" "$run_id" 120
require_safe_token "source" "$source" 16
case "$source" in ops|pixel) ;; *) fail_usage "source must be ops or pixel" ;; esac
require_safe_token "action" "$action" 80
require_safe_token "release id" "$release_id" 160
require_safe_token "profile" "$profile" 48
require_safe_token "target" "$target" 160
require_safe_token "database" "$database" 120
require_millis "total duration" "$total_duration_ms"

case "$command_name" in
  run-start)
    lifecycle="started"
    status="${status:-running}"
    [[ "$status" == "running" ]] || fail_usage "run-start status must be running"
    [[ -n "$event_id" ]] || event_id="${run_id}:run:${lifecycle}:${total_duration_ms}"
    reducer="deploymenttiming_append_run"
    reducer_args=(
      "$(json_string "$event_id")" "$(json_string "$run_id")" "$(json_string "$source")"
      "$(json_string "$action")" "$(json_string "$lifecycle")" "$(json_string "$status")"
      "$(json_string "$release_id")" "$(json_string "$profile")" "$(json_string "$target")"
      "$total_duration_ms"
    )
    ;;
  run-finish)
    lifecycle="finished"
    case "$status" in ok|failed|cancelled) ;; *) fail_usage "run-finish status must be ok, failed, or cancelled" ;; esac
    [[ -n "$event_id" ]] || event_id="${run_id}:run:${lifecycle}:${total_duration_ms}"
    reducer="deploymenttiming_append_run"
    reducer_args=(
      "$(json_string "$event_id")" "$(json_string "$run_id")" "$(json_string "$source")"
      "$(json_string "$action")" "$(json_string "$lifecycle")" "$(json_string "$status")"
      "$(json_string "$release_id")" "$(json_string "$profile")" "$(json_string "$target")"
      "$total_duration_ms"
    )
    ;;
  run-complete)
    (( phase_bundle_provided == 1 )) || fail_usage "run-complete requires --phase-bundle"
    case "$status" in ok|failed|cancelled) ;; *) fail_usage "run-complete status must be ok, failed, or cancelled" ;; esac
    require_phase_bundle "$phase_bundle"
    [[ -n "$event_id" ]] || event_id="${run_id}:run:finished:${total_duration_ms}"
    reducer="deploymenttiming_append_completed_run"
    reducer_args=(
      "$(json_string "$event_id")" "$(json_string "$run_id")" "$(json_string "$source")"
      "$(json_string "$action")" "$(json_string "$status")" "$(json_string "$release_id")"
      "$(json_string "$profile")" "$(json_string "$target")" "$total_duration_ms"
      "$(json_string "$phase_bundle")"
    )
    ;;
  phase)
    require_safe_token "phase" "$phase" 100
    case "$status" in ok|failed|skipped) ;; *) fail_usage "phase status must be ok, failed, or skipped" ;; esac
    require_millis "phase duration" "$duration_ms"
    [[ -n "$event_id" ]] || event_id="${run_id}:phase:${phase}:${total_duration_ms}"
    reducer="deploymenttiming_append_phase"
    reducer_args=(
      "$(json_string "$event_id")" "$(json_string "$run_id")" "$(json_string "$source")"
      "$(json_string "$action")" "$(json_string "$phase")" "$(json_string "$status")"
      "$(json_string "$release_id")" "$(json_string "$profile")" "$(json_string "$target")"
      "$duration_ms" "$total_duration_ms"
    )
    ;;
  *) fail_usage "command must be run-start, phase, run-finish, or run-complete" ;;
esac

require_safe_token "event id" "$event_id" 180
if ! command -v "$spacetime_bin" >/dev/null 2>&1; then
  warn_or_fail "spacetime CLI is unavailable: $spacetime_bin"
fi

call=("$spacetime_bin")
if [[ -n "$spacetime_root" ]]; then
  call+=(--root-dir "$spacetime_root")
fi
call+=(call --no-config -y --server "$server" "$database" "$reducer")
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
