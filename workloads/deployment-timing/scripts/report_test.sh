#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPORTER="${SCRIPT_DIR}/report.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/deployment-timing-compat-test.XXXXXX")"
trap 'rm -rf -- "${tmp_dir}"' EXIT

capture="${tmp_dir}/capture"
fake_spacetime="${tmp_dir}/spacetime"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf "%s\\0" "$@" > "${DEPLOY_TIMING_TEST_CAPTURE:?}"' \
  'exit "${DEPLOY_TIMING_TEST_EXIT:-0}"' \
  >"${fake_spacetime}"
chmod 0755 "${fake_spacetime}"

assert_arg() {
  local wanted="$1"
  if ! tr '\0' '\n' <"${capture}" | grep -F -x -- "${wanted}" >/dev/null; then
    printf 'missing compatibility reporter argument: %s\n' "${wanted}" >&2
    exit 1
  fi
}

DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
DEPLOY_TIMING_SPACETIME_BIN="${fake_spacetime}" \
DEPLOY_TIMING_SPACETIME_SERVER="https://spacetime.test" \
DEPLOY_TIMING_SPACETIME_ROOT="${tmp_dir}/spacetime-root" \
  "${REPORTER}" run-start --wait --strict \
  --run-id "ops-20260722T010203Z" --source ops --action deploy \
  --release-id "release-1" --profile fast --target kitty-gration

for wanted in \
  --root-dir "${tmp_dir}/spacetime-root" call --no-config -y --server https://spacetime.test \
  operational-logging-prod operationallog_append_deployment_run \
  '"ops-20260722T010203Z:run:started:0"' '"ops-20260722T010203Z"' \
  '"ops"' '"deploy"' '"started"' '"running"' '"release-1"' '"fast"' '"kitty-gration"' 0; do
  assert_arg "${wanted}"
done

DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
  "${REPORTER}" run-complete --wait --strict --spacetime "${fake_spacetime}" \
  --database operational-logging-prod \
  --run-id "pixel-20260722T010203Z" --source pixel --action redeploy_component \
  --status ok --total-duration-ms 901 --target ticket_screen --profile standard \
  --phase-bundle "install_apk=ok=512=901"

for wanted in \
  operational-logging-prod operationallog_append_deployment_completed_run \
  '"pixel-20260722T010203Z:run:finished:901"' \
  '"install_apk=ok=512=901"'; do
  assert_arg "${wanted}"
done

if tr '\0' '\n' <"${capture}" | grep -E 'deployment-timing-prod|deploymenttiming_' >/dev/null; then
  printf 'compatibility reporter still referenced the retired logging store\n' >&2
  exit 1
fi

for retired_command in phase run-finish; do
  if "${REPORTER}" "${retired_command}" >/dev/null 2>&1; then
    printf 'retired command unexpectedly succeeded: %s\n' "${retired_command}" >&2
    exit 1
  fi
done

if DEPLOY_TIMING_SPACETIME_DATABASE=deployment-timing-prod \
  "${REPORTER}" run-start --run-id test --source ops --action deploy >/dev/null 2>&1; then
  printf 'legacy database environment override unexpectedly succeeded\n' >&2
  exit 1
fi

if "${REPORTER}" run-start --database deployment-timing-prod \
  --run-id test --source ops --action deploy >/dev/null 2>&1; then
  printf 'legacy database command-line override unexpectedly succeeded\n' >&2
  exit 1
fi

if OPERATIONAL_LOGGING_DATABASE=another-log-database \
  "${REPORTER}" run-start --run-id test --source ops --action deploy >/dev/null 2>&1; then
  printf 'non-canonical operational database override unexpectedly succeeded\n' >&2
  exit 1
fi

printf 'deployment timing compatibility reporter tests passed\n'
