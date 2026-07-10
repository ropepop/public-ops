# Notifications (retired)

The standalone gribu.lv notifier is not part of the kitty-gration runtime and
is not running on the Pixel. Its checked-out implementation was removed during
the 2026-07 code-footprint reduction so inactive code does not remain mixed
with production workloads.

The last complete implementation is recoverable from Git commit
`de42a1c526cc3c13c4a927c1007f2a7ee03bef9f` if a future product decision brings
the notifier back:

```bash
git restore --source=de42a1c526cc3c13c4a927c1007f2a7ee03bef9f -- workloads/site-notifications
```

Restoration is source recovery only. A new deployment design, current tests,
fresh credentials, and explicit production approval are still required.
