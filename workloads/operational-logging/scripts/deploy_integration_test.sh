#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
DEPLOY_SCRIPT="${REPO_ROOT}/tools/arbuzas/deploy.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/operational-log-deploy-test.XXXXXX")"
fake_bin="${tmp_dir}/bin"
capture="${tmp_dir}/spacetime-calls"
mkdir -p "${fake_bin}"
trap 'rm -rf "${tmp_dir}"' EXIT

if [[ ! -x "${DEPLOY_SCRIPT}" ]]; then
  printf 'missing executable deploy script: %s\n' "${DEPLOY_SCRIPT}" >&2
  exit 1
fi

if ! grep -F -- 'workloads/operational-logging/scripts/report-deployment.sh' "${DEPLOY_SCRIPT}" >/dev/null; then
  printf 'deploy script does not use the canonical operational reporter\n' >&2
  exit 1
fi
if ! grep -F -- '"$@" --wait' "${DEPLOY_SCRIPT}" >/dev/null; then
  printf 'deployment reporter worker must be detached once and wait for its Spacetime write\n' >&2
  exit 1
fi
if ! grep -F -- 'start_new_session=True' "${DEPLOY_SCRIPT}" >/dev/null; then
  printf 'deployment reporter worker must start in an independent OS session\n' >&2
  exit 1
fi
if ! grep -F -- 'deployment_timing_report run-complete' "${DEPLOY_SCRIPT}" >/dev/null || \
   ! grep -F -- '--phase-bundle "${DEPLOYMENT_TIMING_PHASE_BUNDLE}"' "${DEPLOY_SCRIPT}" >/dev/null; then
  printf 'deployment reporter must submit one completed run with its phase bundle\n' >&2
  exit 1
fi
if grep -F -- 'deployment_timing_report phase' "${DEPLOY_SCRIPT}" >/dev/null; then
  printf 'deployment reporter must not dispatch one remote call per phase\n' >&2
  exit 1
fi

write_fake() {
  local name="$1"
  shift
  printf '%s\n' "$@" > "${fake_bin}/${name}"
  chmod 0755 "${fake_bin}/${name}"
}

write_fake ssh \
  '#!/usr/bin/env bash' \
  'exit 0'
write_fake scp \
  '#!/usr/bin/env bash' \
  'exit 0'
write_fake python3 \
  '#!/usr/bin/env bash' \
  'exit "${OPERATIONAL_LOGGING_TEST_PYTHON_EXIT:-0}"'
write_fake spacetime \
  '#!/usr/bin/env bash' \
  'printf "%s\\n" "$*" >> "${OPERATIONAL_LOGGING_TEST_CAPTURE:?}"' \
  'exit "${OPERATIONAL_LOGGING_TEST_SPACETIME_EXIT:-0}"'

wait_for_calls() {
  local minimum_calls="$1"
  local count="0"
  local _
  for _ in $(seq 1 80); do
    if [[ -f "${capture}" ]]; then
      count="$(wc -l < "${capture}" | tr -d ' ')"
      if (( count >= minimum_calls )); then
        return 0
      fi
    fi
    sleep 0.05
  done
  printf 'expected at least %s detached operational log calls; got %s\n' "${minimum_calls}" "${count}" >&2
  [[ -f "${capture}" ]] && sed -n '1,20p' "${capture}" >&2
  return 1
}

assert_contains() {
  local wanted="$1"
  if ! grep -F -- "${wanted}" "${capture}" >/dev/null; then
    printf 'missing operational log call content: %s\n' "${wanted}" >&2
    sed -n '1,20p' "${capture}" >&2
    exit 1
  fi
}

assert_exact_call_count() {
  local wanted="$1"
  local actual
  actual="$(wc -l < "${capture}" | tr -d ' ')"
  if [[ "${actual}" != "${wanted}" ]]; then
    printf 'expected exactly %s logging calls; got %s\n' "${wanted}" "${actual}" >&2
    sed -n '1,20p' "${capture}" >&2
    exit 1
  fi
}

assert_phase_bundle() {
  local status="$1"
  if ! grep -E -- "\\\"deploy_config=${status}=[0-9]+=[0-9]+\\\"" "${capture}" >/dev/null; then
    printf 'missing completed-run phase bundle with status %s\n' "${status}" >&2
    sed -n '1,20p' "${capture}" >&2
    exit 1
  fi
}

# A failed logging write must not make a healthy config-only deploy fail.
OPERATIONAL_LOGGING_TEST_CAPTURE="${capture}" \
OPERATIONAL_LOGGING_TEST_SPACETIME_EXIT=7 \
OPERATIONAL_LOGGING_SPACETIME_BIN="${fake_bin}/spacetime" \
OPERATIONAL_LOGGING_RETRY_ATTEMPTS=1 \
OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS=0 \
PATH="${fake_bin}:${PATH}" \
  bash "${DEPLOY_SCRIPT}" deploy-config --ssh-host timing-test-host --ssh-user timing-test-user >/dev/null

wait_for_calls 2
assert_exact_call_count 2
assert_contains 'operational-logging-prod'
assert_contains 'operationallog_append_deployment_run'
assert_contains 'operationallog_append_deployment_completed_run'
assert_contains '"deploy-config"'
assert_contains '"started"'
assert_contains '"ok"'
assert_phase_bundle ok

# A real deploy-config failure must keep its exit code and still report failure.
: > "${capture}"
set +e
OPERATIONAL_LOGGING_TEST_CAPTURE="${capture}" \
OPERATIONAL_LOGGING_TEST_PYTHON_EXIT=7 \
OPERATIONAL_LOGGING_SPACETIME_BIN="${fake_bin}/spacetime" \
OPERATIONAL_LOGGING_RETRY_ATTEMPTS=1 \
OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS=0 \
PATH="${fake_bin}:${PATH}" \
  bash "${DEPLOY_SCRIPT}" deploy-config --ssh-host timing-test-host --ssh-user timing-test-user >/dev/null 2>&1
deploy_status=$?
set -e
if [[ "${deploy_status}" != "7" ]]; then
  printf 'deploy-config did not preserve the failed phase exit code: %s\n' "${deploy_status}" >&2
  exit 1
fi

wait_for_calls 2
assert_exact_call_count 2
assert_contains 'operationallog_append_deployment_run'
assert_contains 'operationallog_append_deployment_completed_run'
assert_contains '"failed"'
assert_phase_bundle failed

printf 'operational logging deploy integration tests passed\n'
