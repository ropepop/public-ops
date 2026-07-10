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

### Automatic local release cleanup

Every successful Arbuzas deploy, including `fast`, `standard`, and `full`, runs a narrow cleanup of direct child directories under `output/arbuzas/releases/`. The deploy's own release id is always protected. By default, other local release bundles are removed when either condition is true:

- the bundle is older than 72 hours;
- the bundle falls beyond the newest 10 entries in its release family.

The explicit protected release can temporarily make the retained count exceed 10. The helper ignores files and symbolic links in the releases root and cannot remove anything outside that root. It does not inspect or remove `ops/evidence/`, `state/`, browser sessions, host mirrors, secrets, databases, or workload runtime data.

Preview the policy without deleting anything:

```bash
python3 tools/arbuzas/local_release_gc.py \
  --releases-root output/arbuzas/releases \
  --protect-release-id "<current-release-id>" \
  --max-age-hours 72 \
  --keep-per-family 10 \
  --dry-run \
  --verbose
```

Deploy-time settings:

- `ARBUZAS_LOCAL_RELEASE_MAX_AGE_HOURS=72`
- `ARBUZAS_LOCAL_RELEASE_KEEP_PER_FAMILY=10`
- `ARBUZAS_LOCAL_RELEASE_CLEANUP_DRY_RUN=false`

Set the dry-run switch to `true` to exercise the automatic path without removing a bundle. Local cleanup failures are reported as warnings after successful validation and do not turn a working deploy into a failed deploy.

### Manual broad scratch cleanup

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
