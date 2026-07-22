#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
DEPLOY_SCRIPT="${SCRIPT_DIR}/deploy.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/arbuzas-deployment-reporting-test.XXXXXX")"
fake_bin="${tmp_dir}/bin"
capture="${tmp_dir}/spacetime-calls"
mkdir -p "${fake_bin}"
trap 'rm -rf "${tmp_dir}"' EXIT

write_fake() {
  local name="$1"
  shift
  printf '%s\n' "$@" > "${fake_bin}/${name}"
  chmod 0755 "${fake_bin}/${name}"
}

write_fake ssh \
  '#!/usr/bin/env bash' \
  'sleep "${DEPLOYMENT_REPORTING_TEST_SSH_SLEEP_SECONDS:-0}"' \
  'exit "${DEPLOYMENT_REPORTING_TEST_SSH_EXIT:-0}"'
write_fake scp \
  '#!/usr/bin/env bash' \
  'exit 0'
write_fake python3 \
  '#!/usr/bin/env bash' \
  'exit "${DEPLOYMENT_REPORTING_TEST_PYTHON_EXIT:-0}"'
write_fake go \
  '#!/usr/bin/env bash' \
  'exit 0'
write_fake curl \
  '#!/usr/bin/env bash' \
  'exit 0'
write_fake spacetime \
  '#!/usr/bin/env bash' \
  'printf "%s\n" "$*" >> "${DEPLOYMENT_REPORTING_TEST_CAPTURE:?}"' \
  'exit 0'

wait_for_calls() {
  local wanted="$1"
  local actual=0
  local attempt
  for attempt in $(seq 1 100); do
    if [[ -f "${capture}" ]]; then
      actual="$(wc -l < "${capture}" | tr -d ' ')"
      if (( actual >= wanted )); then
        sleep 0.1
        return 0
      fi
    fi
    sleep 0.05
  done
  printf 'expected at least %s detached reporting calls; got %s\n' "${wanted}" "${actual}" >&2
  [[ -f "${capture}" ]] && sed -n '1,20p' "${capture}" >&2
  return 1
}

assert_exact_calls() {
  local wanted="$1"
  local actual
  actual="$(wc -l < "${capture}" | tr -d ' ')"
  if [[ "${actual}" != "${wanted}" ]]; then
    printf 'expected exactly %s detached reporting calls; got %s\n' "${wanted}" "${actual}" >&2
    sed -n '1,20p' "${capture}" >&2
    exit 1
  fi
}

reported_env=(
  "DEPLOYMENT_REPORTING_TEST_CAPTURE=${capture}"
  "OPERATIONAL_LOGGING_TEST_CAPTURE=${capture}"
  "OPERATIONAL_LOGGING_SPACETIME_BIN=${fake_bin}/spacetime"
  "OPERATIONAL_LOGGING_RETRY_ATTEMPTS=1"
  "OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS=0"
  "PATH=${fake_bin}:${PATH}"
)

# A short config-only run must retain millisecond resolution and must describe
# the implicit active release honestly rather than using a generated release id.
: > "${capture}"
env "${reported_env[@]}" \
  DEPLOYMENT_REPORTING_TEST_SSH_SLEEP_SECONDS=0.08 \
  bash "${DEPLOY_SCRIPT}" deploy-config \
    --ssh-host timing-test-host --ssh-user timing-test-user >/dev/null
wait_for_calls 2
assert_exact_calls 2
config_completed="$(grep -F 'operationallog_append_deployment_completed_run' "${capture}")"
if [[ "${config_completed}" != *'"deploy-config"'* || "${config_completed}" != *'"current"'* ]]; then
  printf 'config-only reporting lost its action or honest current-release label\n' >&2
  sed -n '1,20p' "${capture}" >&2
  exit 1
fi
if [[ "${config_completed}" =~ deploy_config=ok=([0-9]+)=([0-9]+) ]]; then
  phase_duration_millis="${BASH_REMATCH[1]}"
  phase_total_millis="${BASH_REMATCH[2]}"
else
  printf 'config-only completion has no deploy_config millisecond phase\n' >&2
  sed -n '1,20p' "${capture}" >&2
  exit 1
fi
if (( phase_duration_millis <= 0 || phase_duration_millis >= 1000 )); then
  printf 'short config-only phase was not measured below one second: %s ms\n' "${phase_duration_millis}" >&2
  exit 1
fi
if (( phase_total_millis < phase_duration_millis )); then
  printf 'reported total duration is shorter than its config-only phase\n' >&2
  exit 1
fi

# Rollback is a first-class reported action with the requested release id.
: > "${capture}"
env "${reported_env[@]}" \
  bash "${DEPLOY_SCRIPT}" rollback --release-id rollback-test-release \
    --services train_bot --validation-profile fast \
    --ssh-host timing-test-host --ssh-user timing-test-user >/dev/null
wait_for_calls 2
assert_exact_calls 2
rollback_completed="$(grep -F 'operationallog_append_deployment_completed_run' "${capture}")"
if [[ "${rollback_completed}" != *'"rollback"'* || "${rollback_completed}" != *'"rollback-test-release"'* ]]; then
  printf 'rollback completion lost its action or requested release id\n' >&2
  sed -n '1,20p' "${capture}" >&2
  exit 1
fi
if [[ "${rollback_completed}" != *'rollback_release=ok='* ]]; then
  printf 'rollback completion lost its rollback phase\n' >&2
  sed -n '1,20p' "${capture}" >&2
  exit 1
fi

# A terminating signal must preserve the shell's signal exit code while the
# detached completion record identifies the run as cancelled.
: > "${capture}"
status_file="${tmp_dir}/cancel-status"
env "${reported_env[@]}" \
  DEPLOYMENT_REPORTING_TEST_SSH_SLEEP_SECONDS=5 \
  /usr/bin/python3 - "${DEPLOY_SCRIPT}" "${status_file}" <<'PY'
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

process = subprocess.Popen(
    ["bash", sys.argv[1], "deploy-config", "--ssh-host", "timing-test-host", "--ssh-user", "timing-test-user"],
    env=os.environ.copy(),
    stdin=subprocess.DEVNULL,
    stdout=subprocess.DEVNULL,
    stderr=subprocess.DEVNULL,
    start_new_session=True,
)
time.sleep(0.35)
os.killpg(process.pid, signal.SIGTERM)
return_code = process.wait(timeout=10)
Path(sys.argv[2]).write_text(str(return_code), encoding="utf-8")
PY
cancel_status="$(cat "${status_file}")"
if [[ "${cancel_status}" != "143" ]]; then
  printf 'terminated deploy-config did not preserve exit code 143: %s\n' "${cancel_status}" >&2
  exit 1
fi
wait_for_calls 2
assert_exact_calls 2
cancel_completed="$(grep -F 'operationallog_append_deployment_completed_run' "${capture}")"
if [[ "${cancel_completed}" != *'"cancelled"'* ]]; then
  printf 'terminated deploy-config was not reported as cancelled\n' >&2
  sed -n '1,20p' "${capture}" >&2
  exit 1
fi

printf 'deployment reporting behavior tests passed\n'
