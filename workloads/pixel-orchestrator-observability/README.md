# Pixel Orchestrator Observability

> **Deleted legacy source:** the Pixel orchestrator now writes general
> operational events to the unified `operational-logging-prod` database. The
> standalone production database was deleted on 2026-07-22 after retained-row
> parity. Keep this module only for historical regression tests. Do not publish
> it, enroll writers, or recreate the old destination. New logging work belongs in
> [`../operational-logging`](../operational-logging/).

This standalone private SpacetimeDB module stores only short-lived, general
operational events from the Pixel orchestrator. It does not control the phone,
read Ticket or ChatGPT data, or accept logs, commands, output, paths, network
addresses, UI text, account identifiers, prompts, ticket digits, or JSON fields.

## Data contract

- `pixelorchestrator_event` is a private append-only event table.
- `pixelorchestrator_reporter` is private access-control state containing only
  explicitly enrolled authenticated Spacetime identities.
- `pixelorchestrator_retention_schedule` runs an internal hourly cleanup and
  drains the complete expired backlog in bounded query/delete batches.
- Event time and the exact 24-hour expiry are assigned from the database clock.
- Reusing the same 24-character correlation ID is idempotent.
- Event type, component, cleanup category, status, result, and priority use
  fixed allowlists.
- The remaining event fields are one bounded build token and bounded unsigned
  duration, count, and byte-count values.
- Event rows do not store the authenticated writer identity or another stable
  phone identifier.

The retained schema accepts app sessions, manual actions, component
transitions, health changes, setting changes, cleanup results, scheduling
failures, permission changes, and dropped-event summaries. Ticket domain events
now use the same unified operational event table while Ticket application state
remains in the Ticket database. Selected retained ChatGPT logging history is
also archived in the unified event table; this module does not create new
ChatGPT events.

Cleanup-result events require one of these fixed categories:
`ticket_hierarchy_xml`, `deployment_action_results`, `support_bundles`,
`root_command_history`, `stack_logs`, `dns_history`, `retired_artifacts`,
`deployment_archives`, or `app_cache`. All other event types must use `none`.
This allows per-category counts and reclaimed bytes without adding a free-form
label field.

## Local checks

From this directory:

```bash
make test
make build
```

`make build` compiles the module without publishing it.

## Retired production setup

The deleted legacy private database name was
`pixel-orchestrator-observability-prod`. Historical publish and enrollment
commands are intentionally omitted so this directory cannot be mistaken for a
recovery instruction. Restore retained rows from the canonical table rather
than recreating the old logging database.

The Android app now receives `OPERATIONAL_LOGGING_HOST`,
`OPERATIONAL_LOGGING_DATABASE`, and its authenticated bearer-token path through
the local-first Pixel configuration workflow. The token must not be written
into app logs, telemetry, support bundles, or tracked files.

## Historical Android reducer call

`pixelorchestrator_append_event` accepts arguments in this order:

```text
correlationId, eventType, component, cleanupCategory, status, result, priority,
buildId, durationMillis, count, byteCount
```

This ordering is retained only to interpret or verify legacy rows. The active
Android client uses `operationallog_append_pixel_event` in
`operational-logging-prod` with the same bounded field order.
