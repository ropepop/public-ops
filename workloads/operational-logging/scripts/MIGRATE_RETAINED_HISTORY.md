# Retained operational-history migration

`migrate-retained-history.sh` is the one-time, owner-authenticated migration
path into `operational-logging-prod`. It reads only these private source tables:

- `deploymenttiming_run` and `deploymenttiming_phase`
- `pixelorchestrator_event`
- `ticketremote_safe_operational_log`
- `chatgptbroker_event` and `chatgptbroker_attempt`

It never queries ChatGPT job, job-secret, prompt, result, OCR, or phone-status
tables. Normal rows are included only while their source expiry is still in the
future. ChatGPT is stricter: only event and attempt rows with the exact zero
expiry sentinel are eligible for the retained archive.

The production migration completed on 2026-07-22 with 1,060 verified rows.
`deployment-timing-prod` and `pixel-orchestrator-observability-prod` were then
deleted. The migration now requires an explicit `--source`; there is no live
all-source default that can accidentally target the deleted databases. Do not
recreate the old databases merely to rerun it. Use explicit source and database
overrides only for a separately approved recovery or audit.

## Before apply

1. Run the authenticated schema-only inventory before relying on the fixed
   source list:

   ```bash
   ./workloads/operational-logging/scripts/inventory-private-log-sources.sh
   ```

   It runs only `spacetime list` and `spacetime describe`; it never reads table
   rows. An unclassified log-, trace-, audit-, event-, attempt-, history-,
   batch-, phase-, run-, telemetry-, observability-, diagnostic-, breadcrumb-,
   metric-, or activity-like table fails the check. Review it and add an
   explicit `canonical_log`, `legacy_log_source`, or `application_state`
   decision to `private-log-source-classification.json` before continuing.

   Paused or otherwise uninspectable databases make the default check
   incomplete and return a failure. After confirming that every named paused
   database is intentionally unavailable, rerun with `--allow-incomplete` to
   record that explicit exception. This option never waives an unclassified
   candidate, a private-log-table access mismatch, or the requirement that the
   inventory find exactly one private canonical log-data table.
2. Publish and verify the target module with
   `operationallog_import_legacy_events` available.
3. Use the configured database-owner identity in the Spacetime CLI root.
4. Cut live logging writers over to the new database. If any old writer can
   still append during the sequential source snapshots, plan one final catch-up
   run after it is quiet.
5. Before the production source databases are retired, run the all-source dry run
   and record its count-only result:

   ```bash
   ./workloads/operational-logging/scripts/migrate-retained-history.sh --dry-run --source all
   ```

   The 2026-07-22 production migration is complete and two sources no longer
   exist, so this exact command is now historical. For an approved
   recovery or audit, select only sources that still exist and provide explicit
   database/server overrides.
6. Review `--help` for source selection and per-source database/server
   overrides when migrating an approved recovery source, non-production
   fixture, or renamed source.

The dry run writes no target rows. Source content and mapped batches exist only
inside a mode-0700 temporary directory, which is removed on success, failure,
or interruption. Terminal output contains counts only.

## Apply and retry

Before the production source databases are retired, the matching apply command
is:

```bash
./workloads/operational-logging/scripts/migrate-retained-history.sh --apply --source all
```

The historical all-source command is now expected to fail because the retired
deployment and Pixel databases are gone. Do not recreate them. Any approved
later recovery must select only the intended source and use explicit overrides.

Each reducer call contains no more than 64 typed rows and remains within the
module's 64 KiB logical payload limit. A failed call retries the exact same
batch. Rerunning the whole command is also safe: deterministic IDs let the
target accept an identical row and reject a different payload under that ID.

After the final batch, the command makes one private, owner-authenticated target
read limited to rows labeled `legacy-import`. It checks every still-retained
mapped ID, logical field, metric, occurrence time, expiry, and retention class.
Only parity counts are printed. Rows whose source expiry passes before that
check are reported separately instead of causing a false failure. Any missing
or altered retained row fails the apply command without printing row bodies.

The final count-only output includes `parity_expected_active`,
`parity_verified`, `parity_expired_before_check`, the current target legacy-row
count, and verified counts for each domain. A successful migration requires
`parity_expected_active` and `parity_verified` to match.

## Local contract test

```bash
./workloads/operational-logging/scripts/migrate-retained-history_test.sh
```

The test uses a fake `spacetime` executable. It covers every mapping, expired
and non-sentinel exclusions, strict JSON sanitization, a batch larger than 64
rows, unchanged retry payloads, source/target overrides, forbidden-table
queries, automatic post-apply parity, altered-target rejection, count-only
output, and temporary-file cleanup. The normal workload `make test` runs it.

The workload's normal `make test` target includes this migration contract test.
