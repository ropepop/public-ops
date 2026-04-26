# Workspace Garbage Disposal

## Policy

- Generated output and Codex scratch are temporary local scratch.
- Durable evidence belongs under `ops/evidence/`.
- Reusable browser session bundles belong under `state/browser-use/`.
- Managed garbage roots are:
  - `.codex-tmp/`
  - `output/`
  - `.artifacts/`
  - `workloads/*/.artifacts/`
  - `workloads/*/output/`

## Cleanup

List disposable paths before deleting anything:

```bash
find . \
  \( -path './.codex-tmp' -o -path './output' -o -path './.artifacts' -o -path './workloads/*/.artifacts' -o -path './workloads/*/output' \) \
  -prune -print
```

Delete the local scratch directories:

```bash
rm -rf ./.codex-tmp ./output ./.artifacts
find ./workloads -mindepth 2 -maxdepth 2 \( -name '.artifacts' -o -name 'output' \) -type d -prune -exec rm -rf {} +
```

Check whether anything disposable is still hanging around:

```bash
find . \
  \( -path './.codex-tmp' -o -path './output' -o -path './.artifacts' -o -path './workloads/*/.artifacts' -o -path './workloads/*/output' \) \
  -prune -print | grep .
```

## Evidence And Browser Sessions

- If a generated artifact needs to live beyond local troubleshooting, copy or promote it into `ops/evidence/`.
- Do not keep durable files under `.codex-tmp/`; move anything worth keeping into a tracked location before cleanup runs.
- Do not keep reusable Telegram or browser session bundles under `output/browser-use/`. Use `state/browser-use/` instead.
