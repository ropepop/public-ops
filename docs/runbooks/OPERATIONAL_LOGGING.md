# Operational Logging

`workloads/operational-logging` is the canonical private cloud logging module
for deployment timing, Pixel orchestrator events, and Ticket Remote safe
operational events. It holds one log-data table, `operationallog_event`.

Production consolidation completed on 2026-07-22. Retained history passed
count-and-content parity, and the standalone `deployment-timing-prod` and
`pixel-orchestrator-observability-prod` databases were deleted. Ticket Remote
and the retired ChatGPT broker databases remain because they contain
application state; their old log tables are migration-only and have no active
writer. All new operational rows go to `operational-logging-prod`.

This runbook does not authorize publication, data import, database pause, or
database deletion as part of an ordinary source change.

## Build before publication

```bash
cd workloads/operational-logging
make test
make build
```

Review the configured `OPERATOR_IDENTITY` before publishing. Then, as a
separate approved production action:

```bash
spacetime publish operational-logging-prod --yes
```

When updating an existing database, `init` does not run again. Immediately
reconcile the existing schedule with the current module settings:

```bash
spacetime call --server https://maincloud.spacetimedb.com operational-logging-prod \
  operationallog_refresh_retention_schedule
```

The scheduled cleanup also reconciles itself on its next run, but the explicit
owner call avoids waiting for the previous interval.

The fresh database was created only for the completed consolidation. Do not
create a second logging database for ordinary changes. SpacetimeDB cannot
automatically remove existing tables, and clearing the production database
would destroy all of its data.

## Enroll domain writers

Use the database-owner identity to manage reporters. The production deployment
reporter currently writes as that owner, so it needs no reporter row. Pixel and
Ticket use dedicated identities, each authorized for one domain:

```bash
spacetime call --server https://maincloud.spacetimedb.com operational-logging-prod \
  operationallog_set_reporter '"<pixel-identity>"' '"pixel"' '"pixel-orchestrator"' true

spacetime call --server https://maincloud.spacetimedb.com operational-logging-prod \
  operationallog_set_reporter '"<ticket-service-identity>"' '"ticket"' '"ticket-service"' true
```

If deployment reporting later moves to a non-owner identity, enroll that
identity for `deployment` with the same reducer before cutting it over.

Keep authentication tokens in the Spacetime CLI root or the existing secret
store. Never place a token in a reducer argument, event row, tracked file, or
support bundle.

Disable a reporter by repeating its enrollment call with `false`. Enrollment
changes do not alter old rows. Enabling a second domain for an identity is
rejected. To move an identity, disable its current domain first, then enable
the replacement domain.

## Verify privacy and authorization

Describe the schema as the owner and confirm that all three tables are private:

```bash
spacetime describe --server https://maincloud.spacetimedb.com \
  --json operational-logging-prod
```

Expected tables:

- `operationallog_event`
- `operationallog_reporter`
- `operationallog_retention_schedule`

Test each enrolled identity only in its assigned domain. Also verify that an
anonymous caller and a disabled reporter cannot append an event. Do not print
or capture bearer tokens while testing.

Confirm there is exactly one cleanup schedule row and that it reports a batch
size of `1000`:

```bash
spacetime sql --server https://maincloud.spacetimedb.com operational-logging-prod \
  "SELECT scheduled_id, scheduled_at, batchSize, updatedAt FROM operationallog_retention_schedule;"
```

## Cut over live writers safely

Use a recorded UTC watermark and keep every old database intact during the
transition.

1. Run the schema-only private database inventory. Resolve every unclassified
   candidate before continuing; explicitly review any paused database reported
   as incomplete:

   ```bash
   ./workloads/operational-logging/scripts/inventory-private-log-sources.sh
   ```

2. Publish and verify the new module.
3. Enroll all live reporter identities.
4. Redirect the deployment reporter, Pixel client, and trusted Ticket service
   writer to `operational-logging-prod`.
5. Confirm one new live row from each domain.
6. Export eligible legacy rows through owner-authenticated reads.
7. Sanitize and map them locally, then import bounded batches through
   `operationallog_import_legacy_events`.
8. Require the importer's count-only post-apply parity result to show identical
   expected and verified retained counts for every selected domain.
9. Confirm that old tables receive no rows after the watermark and after the
   longest reporter retry or in-memory queue window.

Ticket browser identities must not be enrolled directly. Browser diagnostics
must pass through the authenticated Ticket service, which checks membership,
sanitizes the event, and writes with the enrolled Ticket service identity.
SpacetimeDB reducers cannot write across databases.

Ticket action latency producers use
`operationallog_append_ticket_latency_phase` through that same trusted service
identity. The producer creates an opaque 24-character lowercase hexadecimal
trace ID and reuses it across these fixed checkpoints, at most once each:

1. `browser_intent`
2. `reducer_committed`
3. `phone_observed`
4. `executor_started`
5. `action_completed`
6. `frame_captured`
7. `relay_sent`
8. `browser_rendered`

Each call supplies fixed action, phase, status, component, and proof values,
plus the phase duration and total duration in milliseconds. Frame capture,
relay, and render checkpoints require a positive bounded frame sequence; all
other successful phases require zero. Failed, timed-out, and cancelled phases
use proof `none` and frame sequence zero. A producer that cannot know the
browser's monotonic end-to-end duration uses total duration zero; the browser
render checkpoint supplies the final total. Durations may not exceed two
minutes. The reducer rejects a ninth phase, caps new latency rows at 30 per
minute, and keeps exact retries idempotent. A retry with changed data under the
same trace and phase is an error.

Do not derive a cross-device duration by subtracting unrelated wall clocks.
Each producer measures local adjacent work with a monotonic clock. The browser
measures intent-to-render total duration. Database occurrence time provides
ordering and audit evidence. Keep latency writes asynchronous and best effort;
logging availability is never a prerequisite for a reducer, phone action,
capture, relay, or browser render.

Live and imported detail objects use the same strict privacy boundary. Keys
that describe email, user/chat/session identity, credentials, Telegram, OCR,
result text, prompts, secrets, or raw payloads are not accepted. Values that
look like email addresses, URLs, IP addresses, private paths, long opaque
credentials, or long digit sequences are redacted before the write and
rejected by the central reducer if they still reach it.

## Legacy import policy

The importer accepts at most 64 typed rows and 64 KiB per reducer call. Every
row receives the same deterministic domain-prefixed ID used by live writers.
Retries are safe, while a different payload under the same ID is rejected.

`--apply` automatically verifies every mapped row that is still within its
source retention window against the private target table. The verification
reads only target rows labeled `legacy-import`, prints counts rather than row
bodies, and fails if a retained row is missing or differs. Rows that expire
between mapping and verification are counted separately.

For normal deployment, Pixel, and Ticket rows, preserve the original
`occurredAtMicros` and `expiresAtMicros`. Already expired rows should not be
imported.

The retired ChatGPT broker has no live writer. Only explicitly selected rows
that used its never-delete sentinel may be considered for archive import. Audit
and sanitize `publicText` and safe-detail content first, set `archive: true`,
and use domain `chatgpt` with record type `event` or `attempt`. Archive import
replaces the old zero timestamp with the module's far-future expiry. Expired
broker rows stay out of the canonical log.

## Query the canonical table

Owner-only examples:

```bash
spacetime sql --server https://maincloud.spacetimedb.com operational-logging-prod \
  "SELECT * FROM operationallog_event WHERE domain = 'deployment';"

spacetime sql --server https://maincloud.spacetimedb.com operational-logging-prod \
  "SELECT * FROM operationallog_event WHERE domain = 'pixel';"

spacetime sql --server https://maincloud.spacetimedb.com operational-logging-prod \
  "SELECT * FROM operationallog_event WHERE domain = 'ticket';"

spacetime sql --server https://maincloud.spacetimedb.com operational-logging-prod \
  "SELECT correlationId, operation, event, component, status, result, durationMillis, totalDurationMillis, count, occurredAt FROM operationallog_event WHERE domain = 'ticket' AND recordType = 'latency_phase';"
```

Keep queries domain- and time-bounded when possible. The table has indexes for
domain/time, domain/correlation/time, domain/scope/time, and expiry. Add another
index only after a measured recurring query needs it.

## Retention verification

Live writers use the database clock:

- `ticket_6h`
- `pixel_24h`
- `deployment_30d`

The single internal schedule runs every five minutes and deletes at most 1,000
expired rows per transaction through the expiry index. This keeps cleanup cost
bounded. With no backlog, an expired row can remain for up to five additional
minutes; a backlog drains at up to 12,000 rows per hour. The Ticket service
admits at most 60 browser diagnostics per minute globally, uses no more than 64
fixed browser event names, and the central reducer samples every informational
browser name by minute. Typed Ticket latency proof admits at most 1,800 rows
per hour, while the worst-case sampled browser vocabulary adds at most 3,840.
Their combined 5,640 rows use less than half of the 12,000-row hourly cleanup
capacity, leaving meaningful headroom for warnings and other domains. Archive
rows use retention class `archive` and stay outside the normal cleanup range.

After publication, insert disposable owner-authenticated test rows in each live
domain only when an approved test plan allows it. Confirm their expiry values,
then remove or let them expire according to that plan.

## Retire old logging destinations

Do not clear or rebuild Ticket Remote to remove its old table. Table removal is
not an automatic SpacetimeDB migration, and Ticket Remote contains active
application state. Stop old log writes and let its six-hour table drain to
empty; the unused schema can remain.

The standalone deployment-timing and Pixel-observability databases were
deleted on 2026-07-22 only after all 1,060 eligible historical rows passed the
private post-apply parity check and both old writers stayed quiet. Do not
recreate either destination.

The retired ChatGPT database contains non-log state and must not be deleted as
part of logging consolidation. Its selected zero-expiry archive rows are now
also present in the canonical table; its old event and attempt tables are not
an active logging destination. The Ticket database likewise remains for live
product state. Its old six-hour log rows will drain, while its append reducers
reject legacy writers.
