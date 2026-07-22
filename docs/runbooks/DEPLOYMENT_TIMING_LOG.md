# Deployment Timing Log

Deployment timing now writes to the canonical private operational logging
module under `workloads/operational-logging`. The production target is
`operational-logging-prod`, and all timing rows live in the single private
`operationallog_event` table alongside other bounded operational domains.

For module publication, reporter enrollment, privacy checks, migration, and
legacy-database retirement, follow
[`OPERATIONAL_LOGGING.md`](./OPERATIONAL_LOGGING.md).

## Reporter

Use:

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

The phase bundle contains at most 64 entries in this format:

```text
phase=status=durationMillis=totalDurationMillis@...
```

Use `--phase-bundle -` when no phase completed. The completion reducer validates
the full bundle, then atomically appends every phase row and the finished run.

The repo deployment script measures phases with a monotonic millisecond clock
when Python is available, then launches the reporter outside the deployment's
critical path. Reporter failures never change deployment success or failure.
The reporter never receives terminal output, command errors, credentials,
control codes, or customer data.

## Configuration

- `OPERATIONAL_LOGGING_DATABASE` defaults to `operational-logging-prod`.
- `OPERATIONAL_LOGGING_HOST` defaults to Maincloud.
- `OPERATIONAL_LOGGING_SPACETIME_BIN` defaults to `spacetime`.
- `OPERATIONAL_LOGGING_SPACETIME_ROOT` selects an optional isolated CLI root.
- `OPERATIONAL_LOGGING_RETRY_ATTEMPTS` defaults to 7 and is bounded to 1–10.
- `OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS` defaults to 1 and is bounded to
  0–30.
- `OPERATIONAL_LOGGING_PYTHON_BIN` optionally selects the Python executable used by
  the repo deploy wrapper.

Use `--wait --strict` only for reporter diagnostics. Normal deployment remains
best-effort and detached.

## Query deployment rows

The database owner can query the unified table:

```bash
spacetime sql --server https://maincloud.spacetimedb.com operational-logging-prod \
  "SELECT * FROM operationallog_event WHERE domain = 'deployment';"
```

Deployment rows use:

- `recordType = 'run'` with `event = 'started'` or `event = 'finished'`.
- `recordType = 'phase'` with the phase name in `event`.
- `correlationId` for the run ID.
- `scopeId` for the release ID.
- `component` for the target.
- `operation` for `deploy`, `validate`, `deploy-config`, or `rollback`.
- `durationMillis` and `totalDurationMillis` for timing measurements.

All deployment rows use retention class `deployment_30d`. Indexed cleanup runs
every five minutes and removes up to 1,000 expired rows per run. With no
backlog, final deletion can lag the 30-day timestamp by up to five minutes;
larger backlogs drain over subsequent runs.
