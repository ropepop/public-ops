# Deployment Timing Log

`workloads/deployment-timing` is a standalone SpacetimeDB module for bounded
timing facts from the local ops and Pixel deployment scripts. It is deliberately
separate from Ticket Remote so timing diagnostics cannot slow, expand, or
couple to the live ticket-control path.

## Publish once

From `workloads/deployment-timing`, after normal source review and local tests:

```bash
spacetime publish deployment-timing-prod --yes
```

This runbook does not authorize publishing as part of a code change. The
configured ops owner (`OPERATOR_IDENTITY` in the module) is initially the only
reporter. Before publishing under a different owner, replace that value and
rerun the local checks. A separate CLI identity can be enrolled later with the
configured owner identity:

```bash
spacetime call --server https://maincloud.spacetimedb.com deployment-timing-prod \
  deploymenttiming_set_reporter '"<identity>"' '"<label>"' true
```

`<identity>` must be the 64-character Spacetime identity used by the deploy
script's CLI root. Keep its authentication token in the CLI root or secret
store, never in this module, a shell argument, or a timing row.

## Add reporting without changing deployment behavior

The reporter is intentionally detached by default. It never receives command
output, and callers should not wait for it on a normal deployment path. Use a
detached `run-start` once, retain phase timings locally, then send one detached
`run-complete` batch from the caller's exit path:

```bash
workloads/deployment-timing/scripts/report.sh run-start \
  --run-id "$RUN_ID" --source ops --action deploy \
  --release-id "$ARBUZAS_RELEASE_ID" --profile "$VALIDATION_PROFILE" \
  --target kitty-gration

workloads/deployment-timing/scripts/report.sh run-complete \
  --run-id "$RUN_ID" --source ops --action deploy \
  --status ok --total-duration-ms "$TOTAL_MS" \
  --release-id "$ARBUZAS_RELEASE_ID" --profile "$VALIDATION_PROFILE" \
  --target kitty-gration \
  --phase-bundle "upload_release=ok=${PHASE_MS}=1654@health=ok=1560=${TOTAL_MS}"
```

Each phase entry is `phase=status=durationMillis=totalDurationMillis`; join at
most 64 entries with `@`, or use `--phase-bundle -` if no phase finished. The
reducer validates the entire bundle and appends all missing private phase rows
plus the `finished` run row in one transaction. A retry with the same run and
phase totals is idempotent. `phase` and `run-finish` remain accepted for older
callers, but new callers should not send their tail as separate asynchronous
calls.

Use the same command shape for Pixel, changing `--source pixel`, `--action`,
and `--target`. Do not add reporting to a script until its timing identifiers
are known to be non-secret.

The caller's reporting wrapper must preserve the deployment result: it sends a
detached start and a detached completion batch for success, failure, or
cancellation, while reporter errors never change the deployment command's
result. The detached worker retries a failed idempotent call with bounded
backoff, covering temporary throttling without adding to deployment time. The
phase and total durations use the caller's existing clock, so they remain
directly comparable with deployment log lines.

For a reporter-only diagnostic, make the error visible without changing the
normal default:

```bash
workloads/deployment-timing/scripts/report.sh run-start --wait --strict \
  --run-id test-run-1 --source ops --action deploy --target kitty-gration
```

## Verify retention and rows

The module writes private append-only tables. Query using the database owner or
an authorized operational identity:

```bash
spacetime sql --server https://maincloud.spacetimedb.com deployment-timing-prod \
  "SELECT id, runId, source, action, lifecycle, status, totalDurationMillis, occurredAt, expiresAt FROM deploymenttiming_run LIMIT 20;"

spacetime sql --server https://maincloud.spacetimedb.com deployment-timing-prod \
  "SELECT id, runId, phase, status, durationMillis, totalDurationMillis, occurredAt, expiresAt FROM deploymenttiming_phase LIMIT 50;"
```

`deploymenttiming_retention_schedule` runs once per hour and deletes at most
1,000 expired rows across the two event tables per invocation. Each event has a
30-day expiry calculated by the database clock. A temporary backlog therefore
drains predictably without a large cleanup transaction.
