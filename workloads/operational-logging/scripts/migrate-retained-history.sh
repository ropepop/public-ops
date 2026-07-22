#!/usr/bin/env bash
set -euo pipefail

# One-time owner-only migration of retained private operational history.
# Source rows and reducer payloads live only in a mode-0700 temporary directory
# and are removed on every exit path. User-visible output contains counts only.

export LC_ALL=C
umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MAPPER="${SCRIPT_DIR}/migrate-retained-history.py"
PARITY_VERIFIER="${SCRIPT_DIR}/verify-migrated-history.py"

usage() {
  cat <<'USAGE'
Usage: migrate-retained-history.sh [--dry-run|--apply] --source NAME [options]

Modes:
  --dry-run                       Read, validate, and print counts only (default).
  --apply                         Import mapped rows in retry-safe batches, then
                                  verify retained source/target parity.

Sources:
  --source NAME                   Repeat for deployment, pixel, ticket, or chatgpt.
                                  Required. Use --source all only for an explicitly
                                  approved historical fixture or recovery.
  --deployment-database NAME      Defaults to deployment-timing-prod.
  --pixel-database NAME           Defaults to pixel-orchestrator-observability-prod.
  --ticket-database NAME          Defaults to ticket-remote-prod-v3.
  --chatgpt-database NAME         Defaults to chatgpt-broker-prod.

Servers and target:
  --server URL                    Default for every source and the target.
  --source-server URL             Override the common source server.
  --target-server URL             Override the target server.
  --deployment-server URL         Override only the deployment source server.
  --pixel-server URL              Override only the Pixel source server.
  --ticket-server URL             Override only the Ticket source server.
  --chatgpt-server URL            Override only the ChatGPT source server.
  --target-database NAME          Defaults to operational-logging-prod.

Runtime:
  --spacetime PATH                Defaults to $OPERATIONAL_LOG_MIGRATION_SPACETIME
                                  or spacetime.
  --spacetime-root PATH           Optional isolated owner-authenticated CLI root.
  --python PATH                   Defaults to $OPERATIONAL_LOG_MIGRATION_PYTHON
                                  or python3.
  --retry-attempts N              Apply attempts per unchanged batch, 1-10 (default 3).
  --retry-delay-seconds N         Base retry delay, 0-30 (default 1).
  -h, --help                      Show this help.

The source reads are private `spacetime subscribe --print-initial-update`
subscriptions. This tool never queries ChatGPT job, job-secret, OCR, prompt,
result, or phone-status tables. It imports only zero-expiry-sentinel ChatGPT
event and attempt rows. Apply verification reads only target rows labeled
`legacy-import`. It never prints row content or reducer payloads.
USAGE
}

fail() {
  printf 'operational history migration: %s\n' "$1" >&2
  exit 1
}

fail_usage() {
  printf 'operational history migration: %s\n' "$1" >&2
  usage >&2
  exit 2
}

require_value() {
  (( $# >= 2 )) && [[ -n "$2" ]] || fail_usage "$1 requires a value"
}

require_database() {
  local label="$1"
  local value="$2"
  [[ ${#value} -le 120 && "${value}" =~ ^[A-Za-z0-9][A-Za-z0-9_-]*$ ]] || \
    fail_usage "${label} must be a bounded database name"
}

require_server() {
  local label="$1"
  local value="$2"
  [[ -n "${value}" && ${#value} -le 240 && "${value}" != *[[:space:]]* ]] || \
    fail_usage "${label} must be a bounded server nickname or URL"
  [[ "${value}" =~ ^[A-Za-z0-9._:/-]+$ ]] || \
    fail_usage "${label} contains unsupported characters"
}

resolve_command() {
  local label="$1"
  local value="$2"
  if [[ "${value}" == */* ]]; then
    [[ -x "${value}" ]] || fail "${label} is unavailable"
  else
    command -v "${value}" >/dev/null 2>&1 || fail "${label} is unavailable"
  fi
}

common_server="${OPERATIONAL_LOG_MIGRATION_SERVER:-https://maincloud.spacetimedb.com}"
source_server="${OPERATIONAL_LOG_MIGRATION_SOURCE_SERVER:-}"
target_server="${OPERATIONAL_LOG_MIGRATION_TARGET_SERVER:-}"
deployment_server="${OPERATIONAL_LOG_MIGRATION_DEPLOYMENT_SERVER:-}"
pixel_server="${OPERATIONAL_LOG_MIGRATION_PIXEL_SERVER:-}"
ticket_server="${OPERATIONAL_LOG_MIGRATION_TICKET_SERVER:-}"
chatgpt_server="${OPERATIONAL_LOG_MIGRATION_CHATGPT_SERVER:-}"

deployment_database="${OPERATIONAL_LOG_MIGRATION_DEPLOYMENT_DATABASE:-deployment-timing-prod}"
pixel_database="${OPERATIONAL_LOG_MIGRATION_PIXEL_DATABASE:-pixel-orchestrator-observability-prod}"
ticket_database="${OPERATIONAL_LOG_MIGRATION_TICKET_DATABASE:-ticket-remote-prod-v3}"
chatgpt_database="${OPERATIONAL_LOG_MIGRATION_CHATGPT_DATABASE:-chatgpt-broker-prod}"
target_database="${OPERATIONAL_LOG_MIGRATION_TARGET_DATABASE:-operational-logging-prod}"

spacetime_bin="${OPERATIONAL_LOG_MIGRATION_SPACETIME:-spacetime}"
spacetime_root="${OPERATIONAL_LOG_MIGRATION_SPACETIME_ROOT:-}"
python_bin="${OPERATIONAL_LOG_MIGRATION_PYTHON:-python3}"
retry_attempts="${OPERATIONAL_LOG_MIGRATION_RETRY_ATTEMPTS:-3}"
retry_delay_seconds="${OPERATIONAL_LOG_MIGRATION_RETRY_DELAY_SECONDS:-1}"
apply_mode=0
source_selection_seen=0
use_deployment=0
use_pixel=0
use_ticket=0
use_chatgpt=0

select_source() {
  source_selection_seen=1
  case "$1" in
    deployment) use_deployment=1 ;;
    pixel) use_pixel=1 ;;
    ticket) use_ticket=1 ;;
    chatgpt) use_chatgpt=1 ;;
    all) use_deployment=1; use_pixel=1; use_ticket=1; use_chatgpt=1 ;;
    *) fail_usage "source must be deployment, pixel, ticket, chatgpt, or all" ;;
  esac
}

while (( $# > 0 )); do
  case "$1" in
    --dry-run) apply_mode=0; shift ;;
    --apply) apply_mode=1; shift ;;
    --source) require_value "$1" "${2:-}"; select_source "$2"; shift 2 ;;
    --server) require_value "$1" "${2:-}"; common_server="$2"; shift 2 ;;
    --source-server) require_value "$1" "${2:-}"; source_server="$2"; shift 2 ;;
    --target-server) require_value "$1" "${2:-}"; target_server="$2"; shift 2 ;;
    --deployment-server) require_value "$1" "${2:-}"; deployment_server="$2"; shift 2 ;;
    --pixel-server) require_value "$1" "${2:-}"; pixel_server="$2"; shift 2 ;;
    --ticket-server) require_value "$1" "${2:-}"; ticket_server="$2"; shift 2 ;;
    --chatgpt-server) require_value "$1" "${2:-}"; chatgpt_server="$2"; shift 2 ;;
    --deployment-database) require_value "$1" "${2:-}"; deployment_database="$2"; shift 2 ;;
    --pixel-database) require_value "$1" "${2:-}"; pixel_database="$2"; shift 2 ;;
    --ticket-database) require_value "$1" "${2:-}"; ticket_database="$2"; shift 2 ;;
    --chatgpt-database) require_value "$1" "${2:-}"; chatgpt_database="$2"; shift 2 ;;
    --target-database) require_value "$1" "${2:-}"; target_database="$2"; shift 2 ;;
    --spacetime) require_value "$1" "${2:-}"; spacetime_bin="$2"; shift 2 ;;
    --spacetime-root) require_value "$1" "${2:-}"; spacetime_root="$2"; shift 2 ;;
    --python) require_value "$1" "${2:-}"; python_bin="$2"; shift 2 ;;
    --retry-attempts) require_value "$1" "${2:-}"; retry_attempts="$2"; shift 2 ;;
    --retry-delay-seconds) require_value "$1" "${2:-}"; retry_delay_seconds="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) fail_usage "unknown option: $1" ;;
  esac
done

(( source_selection_seen == 1 )) || \
  fail_usage "at least one explicit --source is required after production consolidation"

source_server="${source_server:-${common_server}}"
target_server="${target_server:-${common_server}}"
deployment_server="${deployment_server:-${source_server}}"
pixel_server="${pixel_server:-${source_server}}"
ticket_server="${ticket_server:-${source_server}}"
chatgpt_server="${chatgpt_server:-${source_server}}"

require_database "deployment database" "${deployment_database}"
require_database "Pixel database" "${pixel_database}"
require_database "Ticket database" "${ticket_database}"
require_database "ChatGPT database" "${chatgpt_database}"
require_database "target database" "${target_database}"
require_server "deployment server" "${deployment_server}"
require_server "Pixel server" "${pixel_server}"
require_server "Ticket server" "${ticket_server}"
require_server "ChatGPT server" "${chatgpt_server}"
require_server "target server" "${target_server}"
[[ "${retry_attempts}" =~ ^[0-9]+$ ]] && (( retry_attempts >= 1 && retry_attempts <= 10 )) || \
  fail_usage "retry attempts must be between 1 and 10"
[[ "${retry_delay_seconds}" =~ ^[0-9]+$ ]] && (( retry_delay_seconds <= 30 )) || \
  fail_usage "retry delay must be between 0 and 30 seconds"
[[ -f "${MAPPER}" ]] || fail "migration mapper is unavailable"
[[ -f "${PARITY_VERIFIER}" ]] || fail "migration parity verifier is unavailable"
resolve_command "Spacetime CLI" "${spacetime_bin}"
resolve_command "Python" "${python_bin}"

temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/operational-log-migration.XXXXXX")" || \
  fail "could not create a private temporary directory"
cleanup() {
  if [[ -n "${temp_dir:-}" && -d "${temp_dir}" && "${temp_dir}" == *operational-log-migration.* ]]; then
    rm -rf -- "${temp_dir}"
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -m 0700 "${temp_dir}/batches"

spacetime_base=("${spacetime_bin}")
if [[ -n "${spacetime_root}" ]]; then
  spacetime_base+=(--root-dir "${spacetime_root}")
fi

capture_source() {
  local label="$1"
  local server="$2"
  local database="$3"
  local output="$4"
  shift 4
  local stderr_file="${temp_dir}/${label}.stderr"
  local command=("${spacetime_base[@]}" subscribe --no-config -y --server "${server}" \
    --print-initial-update --num-updates 0 "${database}")
  local query
  for query in "$@"; do
    command+=("${query}")
  done
  if ! "${command[@]}" >"${output}" 2>"${stderr_file}"; then
    fail "${label} private source read failed"
  fi
  chmod 0600 "${output}" "${stderr_file}"
}

mapper_args=("${python_bin}" "${MAPPER}" --output-dir "${temp_dir}/batches")
if (( use_deployment == 1 )); then
  deployment_capture="${temp_dir}/deployment.jsonl"
  capture_source deployment "${deployment_server}" "${deployment_database}" "${deployment_capture}" \
    "SELECT * FROM deploymenttiming_run" \
    "SELECT * FROM deploymenttiming_phase"
  mapper_args+=(--deployment "${deployment_capture}")
fi
if (( use_pixel == 1 )); then
  pixel_capture="${temp_dir}/pixel.jsonl"
  capture_source pixel "${pixel_server}" "${pixel_database}" "${pixel_capture}" \
    "SELECT * FROM pixelorchestrator_event"
  mapper_args+=(--pixel "${pixel_capture}")
fi
if (( use_ticket == 1 )); then
  ticket_capture="${temp_dir}/ticket.jsonl"
  capture_source ticket "${ticket_server}" "${ticket_database}" "${ticket_capture}" \
    "SELECT * FROM ticketremote_safe_operational_log"
  mapper_args+=(--ticket "${ticket_capture}")
fi
if (( use_chatgpt == 1 )); then
  chatgpt_capture="${temp_dir}/chatgpt.jsonl"
  capture_source chatgpt "${chatgpt_server}" "${chatgpt_database}" "${chatgpt_capture}" \
    "SELECT * FROM chatgptbroker_event" \
    "SELECT * FROM chatgptbroker_attempt"
  mapper_args+=(--chatgpt "${chatgpt_capture}")
fi

summary_file="${temp_dir}/summary"
mapper_stderr="${temp_dir}/mapper.stderr"
if ! "${mapper_args[@]}" >"${summary_file}" 2>"${mapper_stderr}"; then
  fail "retained source validation or mapping failed"
fi
chmod 0600 "${summary_file}" "${mapper_stderr}"

if (( apply_mode == 1 )); then
  batch_number=0
  shopt -s nullglob
  batch_files=("${temp_dir}"/batches/batch-*.json)
  shopt -u nullglob
  for batch_file in "${batch_files[@]}"; do
    batch_number=$((batch_number + 1))
    batch_json="$(<"${batch_file}")"
    attempt=1
    delay="${retry_delay_seconds}"
    while true; do
      call_stdout="${temp_dir}/call-${batch_number}.stdout"
      call_stderr="${temp_dir}/call-${batch_number}.stderr"
      call=("${spacetime_base[@]}" call --no-config -y --server "${target_server}" \
        "${target_database}" operationallog_import_legacy_events "${batch_json}")
      if "${call[@]}" >"${call_stdout}" 2>"${call_stderr}"; then
        break
      fi
      if (( attempt >= retry_attempts )); then
        fail "target import batch ${batch_number} failed after ${retry_attempts} attempts"
      fi
      if (( delay > 0 )); then
        sleep "${delay}"
      fi
      if (( delay < 30 )); then
        delay=$((delay * 2))
        (( delay <= 30 )) || delay=30
      fi
      attempt=$((attempt + 1))
    done
  done

  target_capture="${temp_dir}/target.jsonl"
  capture_source target "${target_server}" "${target_database}" "${target_capture}" \
    "SELECT * FROM operationallog_event WHERE writerLabel = 'legacy-import'"
  parity_summary_file="${temp_dir}/parity-summary"
  parity_stderr="${temp_dir}/parity.stderr"
  if ! "${python_bin}" "${PARITY_VERIFIER}" \
    --batches-dir "${temp_dir}/batches" \
    --target "${target_capture}" >"${parity_summary_file}" 2>"${parity_stderr}"; then
    fail "post-apply source/target parity verification failed"
  fi
  chmod 0600 "${parity_summary_file}" "${parity_stderr}"
fi

if (( apply_mode == 1 )); then
  printf 'mode=apply\n'
else
  printf 'mode=dry-run\n'
fi
while IFS= read -r summary_line; do
  [[ "${summary_line}" =~ ^[a-z_]+=[0-9]+$ ]] || fail "mapper returned an invalid count summary"
  printf '%s\n' "${summary_line}"
done < "${summary_file}"
if (( apply_mode == 1 )); then
  while IFS= read -r parity_line; do
    [[ "${parity_line}" =~ ^parity_[a-z_]+=[0-9]+$ ]] || \
      fail "parity verifier returned an invalid count summary"
    printf '%s\n' "${parity_line}"
  done < "${parity_summary_file}"
fi
