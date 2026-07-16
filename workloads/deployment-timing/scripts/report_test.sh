#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPORTER="${SCRIPT_DIR}/report.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/deployment-timing-report-test.XXXXXX")"
trap 'rm -rf "${tmp_dir}"' EXIT

capture="${tmp_dir}/capture"
fake_spacetime="${tmp_dir}/spacetime"
printf '%s\n' \
  '#!/usr/bin/env bash' \
  'printf "%s\\0" "$@" > "${DEPLOY_TIMING_TEST_CAPTURE:?}"' \
  'if [[ -n "${DEPLOY_TIMING_TEST_COUNTER:-}" ]]; then' \
  '  count=0; [[ ! -f "${DEPLOY_TIMING_TEST_COUNTER}" ]] || count="$(cat "${DEPLOY_TIMING_TEST_COUNTER}")"' \
  '  count=$((count + 1)); printf "%s\\n" "${count}" > "${DEPLOY_TIMING_TEST_COUNTER}"' \
  '  if (( count <= ${DEPLOY_TIMING_TEST_FAIL_ATTEMPTS:-0} )); then exit 7; fi' \
  'fi' \
  'exit "${DEPLOY_TIMING_TEST_EXIT:-0}"' \
  > "${fake_spacetime}"
chmod 0755 "${fake_spacetime}"

assert_arg() {
  local wanted="$1"
  if ! tr '\0' '\n' < "${capture}" | grep -F -x -- "${wanted}" >/dev/null; then
    printf 'missing reporter argument: %s\n' "${wanted}" >&2
    exit 1
  fi
}

DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
  "${REPORTER}" run-start --wait --strict \
  --spacetime "${fake_spacetime}" \
  --spacetime-root "${tmp_dir}/spacetime-root" \
  --server "https://spacetime.test" \
  --database "deployment-timing-test" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy \
  --release-id "release-1" --profile fast --target kitty-gration

for wanted in \
  --root-dir "${tmp_dir}/spacetime-root" call --no-config -y --server https://spacetime.test \
  deployment-timing-test deploymenttiming_append_run \
  '"ops-20260711T010203Z:run:started:0"' '"ops-20260711T010203Z"' \
  '"ops"' '"deploy"' '"started"' '"running"' '"release-1"' '"fast"' '"kitty-gration"' 0; do
  assert_arg "${wanted}"
done

DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
  "${REPORTER}" phase --wait --strict \
  --spacetime "${fake_spacetime}" --run-id "pixel-20260711T010203Z" \
  --source pixel --action redeploy_component --phase install_apk --status ok \
  --duration-ms 512 --total-duration-ms 901

for wanted in deploymenttiming_append_phase '"pixel-20260711T010203Z:phase:install_apk:901"' \
  '"install_apk"' 512 901; do
  assert_arg "${wanted}"
done

DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
  "${REPORTER}" run-complete --wait --strict \
  --spacetime "${fake_spacetime}" --run-id "pixel-20260711T010203Z" \
  --source pixel --action redeploy_component --status ok --total-duration-ms 28508 \
  --release-id "release-1" --profile fast --target ticket_screen \
  --phase-bundle "connect_device=ok=262=284@runtime_postcheck=ok=411=28508"

for wanted in deploymenttiming_append_completed_run \
  '"pixel-20260711T010203Z:run:finished:28508"' '"pixel-20260711T010203Z"' \
  '"pixel"' '"redeploy_component"' '"ok"' '"release-1"' '"fast"' '"ticket_screen"' \
  28508 '"connect_device=ok=262=284@runtime_postcheck=ok=411=28508"'; do
  assert_arg "${wanted}"
done

rm -f "${capture}"
DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
  "${REPORTER}" phase --spacetime "${fake_spacetime}" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy \
  --phase upload_release --status ok --duration-ms 500 --total-duration-ms 1000
for _ in $(seq 1 40); do
  [[ -s "${capture}" ]] && break
  sleep 0.05
done
[[ -s "${capture}" ]] || {
  printf 'default reporter mode did not hand off an asynchronous call\n' >&2
  exit 1
}
assert_arg deploymenttiming_append_phase

rm -f "${capture}"
DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
  "${REPORTER}" run-complete --spacetime "${fake_spacetime}" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy --status failed \
  --total-duration-ms 1000 --phase-bundle "upload_release=failed=500=1000"
for _ in $(seq 1 40); do
  [[ -s "${capture}" ]] && break
  sleep 0.05
done
[[ -s "${capture}" ]] || {
  printf 'default run-complete mode did not hand off an asynchronous call\n' >&2
  exit 1
}
assert_arg deploymenttiming_append_completed_run

if "${REPORTER}" run-start --wait --strict --spacetime "${fake_spacetime}" \
  --run-id "bad id" --source ops --action deploy >/dev/null 2>&1; then
  printf 'unsafe identifier unexpectedly accepted\n' >&2
  exit 1
fi

if "${REPORTER}" run-complete --wait --strict --spacetime "${fake_spacetime}" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy --status ok \
  --total-duration-ms 1 >/dev/null 2>&1; then
  printf 'run-complete unexpectedly accepted a missing phase bundle\n' >&2
  exit 1
fi

if "${REPORTER}" run-complete --wait --strict --spacetime "${fake_spacetime}" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy --status ok \
  --total-duration-ms 1 --phase-bundle "upload_release=unknown=1=1" >/dev/null 2>&1; then
  printf 'run-complete unexpectedly accepted an unsafe phase bundle\n' >&2
  exit 1
fi

if DEPLOY_TIMING_TEST_CAPTURE="${capture}" DEPLOY_TIMING_TEST_EXIT=7 \
  "${REPORTER}" run-finish --wait --strict --spacetime "${fake_spacetime}" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy --status failed \
  --total-duration-ms 901 >/dev/null 2>&1; then
  printf 'strict reporter unexpectedly hid a failed Spacetime call\n' >&2
  exit 1
fi

retry_counter="${tmp_dir}/retry-counter"
DEPLOY_TIMING_TEST_CAPTURE="${capture}" \
DEPLOY_TIMING_TEST_COUNTER="${retry_counter}" \
DEPLOY_TIMING_TEST_FAIL_ATTEMPTS=2 \
DEPLOY_TIMING_RETRY_ATTEMPTS=3 \
DEPLOY_TIMING_RETRY_BASE_DELAY_SECONDS=0 \
  "${REPORTER}" run-complete --wait --spacetime "${fake_spacetime}" \
  --run-id "retry-run" --source ops --action deploy --status ok \
  --total-duration-ms 12 --phase-bundle "upload_release=ok=10=10" >/dev/null
if [[ "$(cat "${retry_counter}")" != "3" ]]; then
  printf 'best-effort reporter did not retry to success\n' >&2
  exit 1
fi

DEPLOY_TIMING_TEST_CAPTURE="${capture}" DEPLOY_TIMING_TEST_EXIT=7 \
DEPLOY_TIMING_RETRY_ATTEMPTS=1 \
  "${REPORTER}" run-finish --wait --spacetime "${fake_spacetime}" \
  --run-id "ops-20260711T010203Z" --source ops --action deploy --status failed \
  --total-duration-ms 901 >/dev/null

printf 'deployment timing reporter tests passed\n'
