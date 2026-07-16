# Pixel Orchestrator Observability

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

The accepted event types are app sessions, manual actions, component
transitions, health changes, setting changes, cleanup results, scheduling
failures, permission changes, and dropped-event summaries. Ticket and ChatGPT
domain events remain in their existing databases.

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

## Intended production setup

The intended private database name is
`pixel-orchestrator-observability-prod`. Publishing and identity enrollment are
explicit operator actions:

```bash
spacetime publish pixel-orchestrator-observability-prod --yes
spacetime call pixel-orchestrator-observability-prod \
  pixelorchestrator_set_reporter '"<64-character-phone-identity>"' true
```

Before publishing under a different Spacetime owner, replace
`OPERATOR_IDENTITY` in the module and rerun both local checks. The Android app
must receive the database URL/name and an authenticated phone bearer token
through the local-first Pixel configuration workflow. The token must not be
written into app logs, telemetry, support bundles, or tracked files.

## Android reducer call

`pixelorchestrator_append_event` accepts arguments in this order:

```text
correlationId, eventType, component, cleanupCategory, status, result, priority,
buildId, durationMillis, count, byteCount
```

The matching Android client uses this exact ordering and measures its 4 MiB
in-memory queue from the UTF-8 bytes of the reducer request bodies.
