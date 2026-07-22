#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INVENTORY="${SCRIPT_DIR}/inventory-private-log-sources.sh"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/private-log-inventory-test.XXXXXX")"
cleanup() {
  [[ -d "${tmp_dir}" ]] && rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fake_spacetime="${tmp_dir}/spacetime"
calls="${tmp_dir}/calls.log"
classification="${tmp_dir}/classification.json"

cat >"${classification}" <<'JSON'
{
  "schemaVersion": 1,
  "classifications": [
    {
      "database": "canonical-fixture",
      "table": "operationallog_event",
      "kind": "canonical_log",
      "expectedAccess": "private",
      "note": "canonical fixture"
    },
    {
      "database": "canonical-fixture",
      "table": "operationallog_event_archive",
      "kind": "canonical_log",
      "expectedAccess": "private",
      "note": "second canonical fixture used only by the duplicate-table test"
    },
    {
      "database": "legacy-fixture",
      "table": "deploymenttiming_run",
      "kind": "legacy_log_source",
      "expectedAccess": "private",
      "note": "legacy fixture"
    },
    {
      "database": "app-fixture",
      "table": "trainbot_feed_event",
      "kind": "application_state",
      "note": "application fixture"
    }
  ]
}
JSON

cat >"${fake_spacetime}" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail

args=("$@")
mode=""
for arg in "${args[@]}"; do
  case "${arg}" in list|describe|subscribe|sql|call) mode="${arg}"; break ;; esac
done
printf '%s\n' "${mode}" >>"${INVENTORY_TEST_CALLS:?}"

if [[ "${mode}" == "list" ]]; then
  cat <<'LIST'
WARNING: fixture

Associated databases for user aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:

 Database Name(s)  | Identity
-------------------+------------------------------------------------------------------
 canonical-fixture | 1111111111111111111111111111111111111111111111111111111111111111
 legacy-fixture    | 2222222222222222222222222222222222222222222222222222222222222222
 app-fixture       | 3333333333333333333333333333333333333333333333333333333333333333
 paused-fixture    | 4444444444444444444444444444444444444444444444444444444444444444
LIST
  if [[ "${INVENTORY_TEST_MODE:-}" == "unclassified" || \
        "${INVENTORY_TEST_MODE:-}" == "expanded-unclassified" ]]; then
    printf '%s\n' ' mystery-fixture   | 5555555555555555555555555555555555555555555555555555555555555555'
  fi
  exit 0
fi

[[ "${mode}" == "describe" ]] || exit 90
identity="${args[${#args[@]}-1]}"
case "${identity}" in
  1111111111111111111111111111111111111111111111111111111111111111)
    case "${INVENTORY_TEST_MODE:-}" in
      access-mismatch)
        printf '%s\n' '{"tables":[{"name":"operationallog_event","table_access":{"Public":[]},"columns":["TOKEN_SHOULD_NOT_PRINT"]}]}'
        ;;
      canonical-missing)
        printf '%s\n' '{"tables":[{"name":"operationallog_reporter","table_access":{"Private":[]}}]}'
        ;;
      canonical-multiple)
        printf '%s\n' '{"tables":[{"name":"operationallog_event","table_access":{"Private":[]}}, {"name":"operationallog_event_archive","table_access":{"Private":[]}}]}'
        ;;
      *)
        printf '%s\n' '{"tables":[{"name":"operationallog_event","table_access":{"Private":[]},"columns":["TOKEN_SHOULD_NOT_PRINT"]}]}'
        ;;
    esac
    ;;
  2222222222222222222222222222222222222222222222222222222222222222)
    printf '%s\n' '{"tables":[{"name":"deploymenttiming_run","table_access":{"Private":[]}}]}'
    ;;
  3333333333333333333333333333333333333333333333333333333333333333)
    printf '%s\n' '{"tables":[{"name":"trainbot_feed_event","table_access":{"Public":[]}}, {"name":"trainbot_trip","table_access":{"Private":[]}}]}'
    ;;
  4444444444444444444444444444444444444444444444444444444444444444)
    printf '%s\n' 'Error: database is paused' >&2
    exit 1
    ;;
  5555555555555555555555555555555555555555555555555555555555555555)
    if [[ "${INVENTORY_TEST_MODE:-}" == "expanded-unclassified" ]]; then
      printf '%s\n' '{"tables":[{"name":"runtime_telemetry","table_access":{"Private":[]}},{"name":"service_observability","table_access":{"Private":[]}},{"name":"worker_diagnostics","table_access":{"Private":[]}},{"name":"request_breadcrumbs","table_access":{"Private":[]}},{"name":"health_metrics","table_access":{"Private":[]}},{"name":"user_activity","table_access":{"Private":[]}}]}'
    else
      printf '%s\n' '{"tables":[{"name":"mystery_audit_history","table_access":{"Private":[]}}]}'
    fi
    ;;
  *) exit 91 ;;
esac
FAKE
chmod 0755 "${fake_spacetime}"

common_args=(
  --spacetime "${fake_spacetime}"
  --spacetime-root "${tmp_dir}/spacetime-root"
  --server "https://fixture.test"
  --classification-file "${classification}"
  --timeout-seconds 5
)

: >"${calls}"
strict_stdout="${tmp_dir}/strict.stdout"
strict_stderr="${tmp_dir}/strict.stderr"
set +e
INVENTORY_TEST_CALLS="${calls}" \
  bash "${INVENTORY}" "${common_args[@]}" >"${strict_stdout}" 2>"${strict_stderr}"
strict_status=$?
set -e
[[ "${strict_status}" == "3" ]] || {
  printf 'strict inventory did not fail closed for a paused database\n' >&2
  exit 1
}
[[ ! -s "${strict_stderr}" ]] || {
  printf 'strict inventory unexpectedly wrote stderr\n' >&2
  exit 1
}
for expected in \
  status=attention \
  associated_databases=4 \
  described_databases=3 \
  paused_databases=1 \
  uninspectable_databases=0 \
  candidate_surfaces=3 \
  canonical_log_tables=1 \
  private_canonical_log_tables=1 \
  canonical_log_data_contract=ok \
  legacy_log_sources=1 \
  application_state_candidates=1 \
  unclassified_candidates=0; do
  rg -F -x "${expected}" "${strict_stdout}" >/dev/null || {
    printf 'missing strict inventory summary: %s\n' "${expected}" >&2
    exit 1
  }
done
rg -F -x 'attention=paused_database database=paused-fixture' "${strict_stdout}" >/dev/null || {
  printf 'paused database was not reported\n' >&2
  exit 1
}
if rg -F 'TOKEN_SHOULD_NOT_PRINT' "${strict_stdout}" "${strict_stderr}" >/dev/null; then
  printf 'inventory printed schema details beyond table names\n' >&2
  exit 1
fi
if rg -x 'subscribe|sql|call' "${calls}" >/dev/null; then
  printf 'inventory used a row-reading or mutating Spacetime command\n' >&2
  exit 1
fi
[[ "$(rg -c '^list$' "${calls}")" == "1" ]] || {
  printf 'inventory did not list databases exactly once\n' >&2
  exit 1
}
[[ "$(rg -c '^describe$' "${calls}")" == "4" ]] || {
  printf 'inventory did not describe every associated database\n' >&2
  exit 1
}

allowed_stdout="${tmp_dir}/allowed.stdout"
INVENTORY_TEST_CALLS="${calls}" \
  bash "${INVENTORY}" --allow-incomplete "${common_args[@]}" >"${allowed_stdout}"
rg -F -x 'unclassified_candidates=0' "${allowed_stdout}" >/dev/null || {
  printf 'allow-incomplete changed candidate classification\n' >&2
  exit 1
}

unclassified_stdout="${tmp_dir}/unclassified.stdout"
set +e
INVENTORY_TEST_CALLS="${calls}" INVENTORY_TEST_MODE=unclassified \
  bash "${INVENTORY}" --allow-incomplete "${common_args[@]}" >"${unclassified_stdout}"
unclassified_status=$?
set -e
[[ "${unclassified_status}" == "2" ]] || {
  printf 'unclassified candidate did not fail the inventory\n' >&2
  exit 1
}
rg -F -x 'unclassified_candidates=1' "${unclassified_stdout}" >/dev/null || {
  printf 'unclassified candidate count was not reported\n' >&2
  exit 1
}
rg -F -x 'attention=unclassified database=mystery-fixture table=mystery_audit_history access=private note=review required' \
  "${unclassified_stdout}" >/dev/null || {
  printf 'unclassified candidate was not identified\n' >&2
  exit 1
}

expanded_stdout="${tmp_dir}/expanded-unclassified.stdout"
set +e
INVENTORY_TEST_CALLS="${calls}" INVENTORY_TEST_MODE=expanded-unclassified \
  bash "${INVENTORY}" --allow-incomplete "${common_args[@]}" >"${expanded_stdout}"
expanded_status=$?
set -e
[[ "${expanded_status}" == "2" ]] || {
  printf 'expanded log-like vocabulary did not fail the inventory\n' >&2
  exit 1
}
rg -F -x 'unclassified_candidates=6' "${expanded_stdout}" >/dev/null || {
  printf 'expanded log-like vocabulary was not fully detected\n' >&2
  exit 1
}
for table in runtime_telemetry service_observability worker_diagnostics request_breadcrumbs health_metrics user_activity; do
  rg -F "database=mystery-fixture table=${table} access=private" "${expanded_stdout}" >/dev/null || {
    printf 'expanded candidate was not identified: %s\n' "${table}" >&2
    exit 1
  }
done

access_stdout="${tmp_dir}/access.stdout"
set +e
INVENTORY_TEST_CALLS="${calls}" INVENTORY_TEST_MODE=access-mismatch \
  bash "${INVENTORY}" --allow-incomplete "${common_args[@]}" >"${access_stdout}"
access_status=$?
set -e
[[ "${access_status}" == "2" ]] || {
  printf 'private log-table access mismatch did not fail the inventory\n' >&2
  exit 1
}
rg -F 'attention=access_mismatch database=canonical-fixture table=operationallog_event access=public' \
  "${access_stdout}" >/dev/null || {
  printf 'private log-table access mismatch was not reported\n' >&2
  exit 1
}
rg -F -x 'canonical_log_data_contract=violation' "${access_stdout}" >/dev/null || {
  printf 'public canonical table did not violate the one-private-table contract\n' >&2
  exit 1
}
rg -F -x 'attention=canonical_log_data_contract observed=1 private=0 required=exactly_one_private' \
  "${access_stdout}" >/dev/null || {
  printf 'public canonical table contract details were not reported\n' >&2
  exit 1
}

missing_stdout="${tmp_dir}/canonical-missing.stdout"
set +e
INVENTORY_TEST_CALLS="${calls}" INVENTORY_TEST_MODE=canonical-missing \
  bash "${INVENTORY}" --allow-incomplete "${common_args[@]}" >"${missing_stdout}"
missing_status=$?
set -e
[[ "${missing_status}" == "2" ]] || {
  printf 'missing canonical log-data table did not fail the inventory\n' >&2
  exit 1
}
for expected in \
  canonical_log_tables=0 \
  private_canonical_log_tables=0 \
  canonical_log_data_contract=violation \
  'attention=canonical_log_data_contract observed=0 private=0 required=exactly_one_private'; do
  rg -F -x "${expected}" "${missing_stdout}" >/dev/null || {
    printf 'missing canonical table summary was incomplete: %s\n' "${expected}" >&2
    exit 1
  }
done

multiple_stdout="${tmp_dir}/canonical-multiple.stdout"
set +e
INVENTORY_TEST_CALLS="${calls}" INVENTORY_TEST_MODE=canonical-multiple \
  bash "${INVENTORY}" --allow-incomplete "${common_args[@]}" >"${multiple_stdout}"
multiple_status=$?
set -e
[[ "${multiple_status}" == "2" ]] || {
  printf 'multiple canonical log-data tables did not fail the inventory\n' >&2
  exit 1
}
for expected in \
  canonical_log_tables=2 \
  private_canonical_log_tables=2 \
  canonical_log_data_contract=violation \
  'attention=canonical_log_data_contract observed=2 private=2 required=exactly_one_private'; do
  rg -F -x "${expected}" "${multiple_stdout}" >/dev/null || {
    printf 'multiple canonical table summary was incomplete: %s\n' "${expected}" >&2
    exit 1
  }
done

printf 'private log-source inventory tests passed\n'
