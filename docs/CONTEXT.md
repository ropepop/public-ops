# Ops Context Guide

Use this file to pick the smallest useful Markdown context for a kitty-gration ops task. Avoid broad recursive doc reads unless the task is specifically historical or investigative.

## Always-read spine

- `../AGENTS.md`: repo-wide agent rules, browser/session policy, and shared deploy cautions.
- `../README.md`: active runtime map and shared operator/developer commands.
- This file: where to go next.

## Workload context

- `../workloads/satiksme-bot/README.md`: Kontrole public map and incident app overview.
- `../workloads/satiksme-bot/AGENTS.md`: service-specific tests, deploy checks, and live-map pitfalls.
- `../workloads/train-bot/README.md`: Vilciens/train app overview and test-login docs.
- `../workloads/train-bot/AGENTS.md`: train app verification and public-cache cautions.
- `../workloads/ticket-remote/README.md`: public ViVi ticket viewer runtime model.
- `../workloads/ticket-remote/AGENTS.md`: ticket viewer auth, stream, control-code, and browser verification rules.
- `../workloads/subscription-bot/README.md` and `../workloads/subscription-bot/AGENTS.md`: subscription bot and mini app overview plus web UI checks.
- `../workloads/site-notifications/README.md` and `../workloads/rigassatiksme-qr-bot/README.md`: smaller workload entrypoints.
- For browser UI tasks, also read `architecture/WEB_UI_GUIDANCE.md`.

## Durable docs

- `architecture/`: stable system design, boundaries, endpoints, state ownership, and runtime paths.
- `architecture/WEB_UI_GUIDANCE.md`: required ArrowJS policy for active interactive browser UI.
- `runbooks/`: operator procedures that should remain valid across sessions.
- `reference/` if present: exhaustive command/config/API detail.
- `../infra/arbuzas/docker/README.md`: active Docker layout and Compose details.

## Historical / heavy docs

Do not load these by default:

- `../ops/evidence/`: screenshots, logs, browser snapshots, and generated proof.
- `../ops/reports/`: local dated measurements and analyses; usually ignored/generated.
- `archive/`: tracked historical docs kept for narrow archaeology, not normal implementation context.
- `../workloads/*/ops/evidence/`: workload-local historical proof.
- `../workloads/*/ops/monitoring/`: watch logs and dated monitoring writeups.
- `superpowers/plans/archive/` or old dated plan files: useful for archaeology, not normal implementation context.

When a task asks for evidence or historical debugging, read the narrow timestamp/topic folder only and summarize findings back into the active runbook or architecture doc if the lesson is durable.

## Placement rules for new Markdown

- Stable architecture change: `architecture/`.
- Repeatable operator procedure: `runbooks/`.
- Exhaustive config/API detail: `reference/`.
- Current implementation plan: `plans/active/`.
- Completed/old plan: `plans/archive/YYYY/`.
- Dated measurements or analysis: `../ops/reports/YYYY-MM/` when local/generated, or `archive/<service>/...` when the Markdown should remain tracked.
- Browser snapshots, logs, screenshots, raw API captures: `../ops/evidence/<service>/<timestamp-or-topic>/` with a short `README.md` index.

Keep root Markdown limited to `README.md`, `AGENTS.md`, license/changelog/contributing files, and maybe one deliberately current top-level plan.
