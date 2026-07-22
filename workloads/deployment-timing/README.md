# Deployment Timing (retired source)

This directory preserves the former deployment-timing module for schema
history, migration verification, and local regression tests. It is not an
active production logging destination. All current deployment timing belongs
in the single private `operational-logging-prod.operationallog_event` table.
The standalone production database was deleted on 2026-07-22 after retained
history passed parity; do not recreate it.

Use the canonical implementation and operator guide:

- [`../operational-logging`](../operational-logging/)
- [`../../docs/runbooks/OPERATIONAL_LOGGING.md`](../../docs/runbooks/OPERATIONAL_LOGGING.md)
- [`../../docs/runbooks/DEPLOYMENT_TIMING_LOG.md`](../../docs/runbooks/DEPLOYMENT_TIMING_LOG.md)

## Historical schema

The retired database held:

- `deploymenttiming_run`: append-only start and finish rows.
- `deploymenttiming_phase`: append-only phase-completion rows.
- `deploymenttiming_reporter`: writer authorization metadata.
- `deploymenttiming_retention_schedule`: cleanup metadata.

Its rows were private, used the database clock, and expired after 30 days. The
retained module deliberately accepts only bounded identifiers and durations;
it never accepts terminal output, errors, credentials, control codes, or
customer data.

## Compatibility reporter

[`scripts/report.sh`](./scripts/report.sh) remains only so an older caller
cannot silently target the retired database. `run-start` and `run-complete`
delegate to the canonical operational-logging reporter and are locked to
`operational-logging-prod`. The old `phase` and `run-finish` commands, the old
database environment override, and any non-canonical `--database` value are
rejected.

New callers should use the canonical path directly:

```bash
workloads/operational-logging/scripts/report-deployment.sh run-start \
  --run-id "ops-20260722T010203Z" --source ops --action deploy \
  --release-id "20260722T010203Z" --profile fast --target kitty-gration

workloads/operational-logging/scripts/report-deployment.sh run-complete \
  --run-id "ops-20260722T010203Z" --source ops --action deploy \
  --status ok --total-duration-ms 3214 \
  --release-id "20260722T010203Z" --profile fast --target kitty-gration \
  --phase-bundle "upload_release=ok=812=1654@health=ok=1560=3214"
```

## Local historical checks

From this directory:

```bash
make test
make build
```

`make build` compiles the historical module locally. Do not publish this module
as a new production database or restore the retired database as an active
writer. Production publication, writer enrollment, retained-history imports,
queries, and retention checks belong to the operational-logging runbook.
