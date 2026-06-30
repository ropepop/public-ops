# Workspace Garbage Disposal

## Policy

- Generated output and Codex scratch are temporary local scratch.
- Durable evidence belongs under `ops/evidence/`.
- Reusable legacy browser session bundles belong under `state/browser-use/`.
- Generated reports under `ops/reports/` are local report output unless promoted into a runbook or durable evidence bundle.
- Release bundles must not include top-level workload runtime state, database, lock, or session-secret files.
- Managed garbage roots are:
  - `.codex-tmp/`
  - `output/`
  - `.artifacts/`
  - `workloads/*/.artifacts/`
  - `workloads/*/output/`
  - `ops/reports/`

## Cleanup

List disposable paths before deleting anything:

```bash
find . \
  \( -path './.codex-tmp' -o -path './output' -o -path './.artifacts' -o -path './workloads/*/.artifacts' -o -path './workloads/*/output' -o -path './ops/reports' \) \
  -prune -print
```

Delete the local scratch directories:

```bash
rm -rf ./.codex-tmp ./output ./.artifacts ./ops/reports
find ./workloads -mindepth 2 -maxdepth 2 \( -name '.artifacts' -o -name 'output' \) -type d -prune -exec rm -rf {} +
```

Check whether anything disposable is still hanging around:

```bash
find . \
  \( -path './.codex-tmp' -o -path './output' -o -path './.artifacts' -o -path './workloads/*/.artifacts' -o -path './workloads/*/output' -o -path './ops/reports' \) \
  -prune -print | grep .
```

## Evidence And Browser Sessions

- If a generated artifact needs to live beyond local troubleshooting, copy or promote it into `ops/evidence/`.
- Do not keep durable files under `.codex-tmp/`; move anything worth keeping into a tracked location before cleanup runs.
- Do not keep reusable Telegram or legacy browser session bundles under `output/browser-use/`. Use `state/browser-use/` instead.
- Archive ad hoc root `evidence/`, `security-audits/`, and `security-audit-evidence/` outside the repo before deleting them locally.
