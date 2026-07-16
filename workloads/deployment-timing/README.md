# Deployment Timing

This is a separate SpacetimeDB module and database for deployment timing only.
It does not run deployments, control the Pixel, read application data, or join
the Ticket database.

## What it stores

- `deploymenttiming_run`: append-only `started` and `finished` lifecycle rows.
- `deploymenttiming_phase`: append-only phase-completion rows.
- `deploymenttiming_retention_schedule`: a private hourly scheduled cleanup.

The preferred writer pattern is one detached `started` row followed by one
detached completion batch. The completion batch atomically appends every phase
row and the matching `finished` row, so a rate-limited or interrupted reporter
cannot leave a run with only a partial tail.

Every event is written with the database clock, expires after 30 days, and is
removed in bounded batches. The run and phase tables are private. The configured
ops owner can write immediately; another deploy identity must first be explicitly
enrolled in `deploymenttiming_reporter` by that owner. Before publishing under a
different owner, replace `OPERATOR_IDENTITY` in the module with that owner's
64-character Spacetime identity and rerun the local checks.

Only compact identifiers and durations are accepted. This module deliberately
does not accept command output, error text, authentication material, control
codes, or customer data.

## Local checks

From this directory:

```bash
make test
make build
```

`make build` runs `spacetime build`; it does not publish anything.

## Reporter interface

Use [`scripts/report.sh`](./scripts/report.sh). It defaults to a detached,
best-effort call so timing collection cannot delay or fail a deployment. Use
`--wait --strict` only in tests or when an operator wants to diagnose the
reporter itself.

```bash
# Ops deploy script shape
scripts/report.sh run-start \
  --run-id "ops-20260711T010203Z" --source ops --action deploy \
  --release-id "20260711T010203Z" --profile fast --target kitty-gration

# Preferred completion shape: all phase rows and the finished run are one
# SpacetimeDB reducer transaction. The caller still invokes this asynchronously
# by default, so it cannot block or change deployment behavior.
scripts/report.sh run-complete \
  --run-id "ops-20260711T010203Z" --source ops --action deploy \
  --status ok --total-duration-ms 3214 \
  --release-id "20260711T010203Z" --profile fast --target kitty-gration \
  --phase-bundle "upload_release=ok=812=1654@health=ok=1560=3214"

# Pixel completion shape
scripts/report.sh run-complete \
  --run-id "pixel-20260711T010203Z" --source pixel --action redeploy_component \
  --status ok --total-duration-ms 901 \
  --target ticket_screen --profile standard \
  --phase-bundle "install_apk=ok=512=901"
```

Each `--phase-bundle` entry is `phase=status=durationMillis=totalDurationMillis`.
Entries are joined by `@`, are limited to 64, and use only compact safe tokens.
Use `--phase-bundle -` when a run ends before a phase completes. The existing
`phase` and `run-finish` commands remain compatible for older callers, but new
callers should use `run-complete` to avoid independently queued tail events.

The reporter reads these optional environment variables:

- `DEPLOY_TIMING_SPACETIME_DATABASE` (default `deployment-timing-prod`)
- `DEPLOY_TIMING_SPACETIME_SERVER` (default `https://maincloud.spacetimedb.com`)
- `DEPLOY_TIMING_SPACETIME_BIN` (default `spacetime`)
- `DEPLOY_TIMING_SPACETIME_ROOT` (an optional isolated local CLI/auth directory)
- `DEPLOY_TIMING_RETRY_ATTEMPTS` (default `7`, bounded to `1..10`)
- `DEPLOY_TIMING_RETRY_BASE_DELAY_SECONDS` (default `1`, bounded to `0..30`)

It uses the preconfigured Spacetime CLI identity. It neither reads nor prints
that identity's token. Detached callers use a bounded idempotent retry window,
so temporary Spacetime throttling cannot drop the atomic completion record and
still never extends or changes the deployment result.

## Current ops wiring

Deploy callers should create a detached run start, collect their existing phase
timings locally, and send one detached `run-complete` call on exit for success,
failure, or cancellation. The reporter does not wait for the timing database;
an absent or failed reporter never changes the deployment's outcome. The
focused local integration test uses fake local SSH, mirror, and Spacetime
commands to verify deployment behavior without contacting a host.

## Intended production database

The intended database name is `deployment-timing-prod`. Publishing is an
explicit operator action and is intentionally not part of this change:

```bash
spacetime publish deployment-timing-prod --yes
```

See [the operator runbook](../../docs/runbooks/DEPLOYMENT_TIMING_LOG.md) for
the publish, reporter-enrollment, query, and retention checks.
