# Operational Logging

This is the canonical private SpacetimeDB module for short-lived operational
logging across deployments, the Pixel orchestrator, and Ticket Remote. It
replaces separate active logging destinations without joining or copying their
application state.

## Data layout

There is one log-data table:

- `operationallog_event`: private, append-only operational events.

Two private metadata tables support it:

- `operationallog_reporter`: domain-scoped writer authorization.
- `operationallog_retention_schedule`: one internal five-minute cleanup schedule.

The event table has fixed columns for domain, record type, source, operation,
event, level, status, result, scope, correlation, component, bounded metrics,
and timestamps. `detailJson` is limited to a 1 KiB JSON object with bounded
nesting, field count, arrays, and strings. Sensitive field names are rejected.
Email, user/session/chat identifiers, credentials, OCR/result text, URLs,
addresses, long opaque values, private paths, and other private-looking string
values are rejected by the central reducer as a final safety boundary.
The table does not store the authenticated writer identity; it stores only the
generic label assigned during reporter enrollment.

The column order is deliberate: row identity and schema first; domain and
classification next; safe scope/correlation/component references after that;
bounded detail and numeric measurements next; and retention plus occurrence,
recording, and expiry timestamps last. This keeps routine scans predictable
without introducing a second domain-specific log table.

The table and metadata are private. There are no public subscriptions and no
generic live append reducer.

## Retention

- Ticket operational events: 6 hours.
- Pixel operational events: 24 hours.
- Deployment run and phase events: 30 days.
- Explicitly selected, sanitized legacy archive rows: no automatic expiry.

Cleanup runs every five minutes and deletes at most 1,000 expired rows in one
transaction by the `expiresAt` index. With no backlog, normal deletion can lag
expiry by up to five minutes; a backlog drains at up to 12,000 rows per hour.
Ticket browser admission is globally capped at 60 messages per minute, has a
fixed vocabulary of at most 64 names, and every informational browser name is
sampled into one shared minute bucket. Cleanup capacity is therefore more than
three times that browser bound. Typed Ticket latency proof is separately capped
at 30 new phase rows per minute and eight rows per trace. Together, the maximum
sampled browser and latency-proof rates use less than half of cleanup capacity,
leaving headroom for warnings and other domains. Archive rows use a far-future
expiry and stay outside the normal cleanup range.

## Writer contracts

The owner enrolls each dedicated non-owner identity for exactly one live
domain: `deployment`, `pixel`, or `ticket`. Trying to enable a second domain for
the same identity is rejected; disable the old assignment before enabling the
new one. The database owner may write all domains; production deployment
reporting currently uses that owner path.

Live reducers are domain-specific:

- `operationallog_append_deployment_run`
- `operationallog_append_deployment_completed_run`
- `operationallog_append_pixel_event`
- `operationallog_append_ticket_event`
- `operationallog_append_ticket_latency_phase`

Deployment completion accepts the existing bounded phase-bundle format and
inserts all phases plus the finished run in one transaction. Pixel uses the
same fixed 11-value vocabulary as the previous observability module. Ticket
accepts only its safe event shape and preserves minute-bucket sampling for
known high-volume informational events.

Ticket action latency proof uses the separate typed reducer but still writes
to the same `operationallog_event` table with six-hour retention. One opaque
24-hex trace ID joins at most eight fixed checkpoints: browser intent, reducer
commit, phone observation, executor start, action completion, captured frame,
relay send, and browser render. Action, phase, component, status, and proof are
fixed vocabularies. Phase and end-to-end latency use `durationMillis` and
`totalDurationMillis`; post-action frame phases carry a bounded frame sequence
in `count`.

The latency reducer accepts no free-form detail, screen content, account or
session identity, command payload, coordinates, device identifier, URL, or raw
revision. Exact phase retries are no-ops and changed retries collide. Producers
must write this diagnostic evidence asynchronously: logging failure must never
make a Ticket action or stream fail.

Every live ID is deterministically prefixed by its domain. An exact retry is a
no-op. Reusing an ID for a different payload fails the whole transaction,
except for a sampled Ticket minute bucket, where later details are deliberately
dropped.

`operationallog_import_legacy_events` is owner-only, typed, and limited to 64
events and 64 KiB per call. Non-archive rows preserve supplied occurrence and
expiry timestamps. Only imported ChatGPT rows may select archive retention;
there is no active ChatGPT writer.

## Local checks

From this directory:

```bash
make test
make build
```

`make build` compiles the module. It does not publish it.

## Private database inventory

Before a migration or whenever a new Spacetime database is introduced, run:

```bash
./scripts/inventory-private-log-sources.sh
```

The inventory uses the authenticated CLI database list and schema descriptions
only. It never reads rows. Every table whose name looks like a log, trace,
audit, event, attempt, history, batch, phase, run, telemetry, observability,
diagnostic, breadcrumb, metric, or activity surface must have an explicit
decision in `scripts/private-log-source-classification.json`: canonical log,
legacy logging source, or application state. New unclassified candidates and
unexpectedly public log tables fail the check so they cannot be silently left
outside the one-table design.

Paused and otherwise uninspectable databases are named and make the default
result incomplete. `--allow-incomplete` is available only after those databases
have been deliberately reviewed; it does not waive unknown candidate tables or
the core contract that exactly one private canonical log-data table must exist.
The fixture-backed inventory contract is included in `make test`.

## Deployment timing reporter

Use `scripts/report-deployment.sh`. It supports `run-start` and the preferred
atomic `run-complete` command. The reporter defaults to a detached,
best-effort call and never receives deployment output.

The repo deployment script launches it in a detached process with `--wait`, so
bounded reporter retries remain outside the deployment's critical path.

Configuration:

- `OPERATIONAL_LOGGING_DATABASE` (default `operational-logging-prod`)
- `OPERATIONAL_LOGGING_HOST` (default `https://maincloud.spacetimedb.com`)
- `OPERATIONAL_LOGGING_SPACETIME_BIN` (default `spacetime`)
- `OPERATIONAL_LOGGING_SPACETIME_ROOT` (optional isolated CLI/auth directory)
- `OPERATIONAL_LOGGING_RETRY_ATTEMPTS` (default `7`, range `1..10`)
- `OPERATIONAL_LOGGING_RETRY_BASE_DELAY_SECONDS` (default `1`, range `0..30`)
- `OPERATIONAL_LOGGING_PYTHON_BIN` (optional Python path used by the repo deploy wrapper)

## Production target

The active database is `operational-logging-prod`. On 2026-07-22, all eligible
history passed parity and the two standalone log-only databases were deleted.
Ticket and ChatGPT application databases remain, but their old log tables have
no active writer. Publishing, reporter enrollment, any later legacy import, and
database retirement remain explicit operator actions. Follow
[`docs/runbooks/OPERATIONAL_LOGGING.md`](../../docs/runbooks/OPERATIONAL_LOGGING.md).
