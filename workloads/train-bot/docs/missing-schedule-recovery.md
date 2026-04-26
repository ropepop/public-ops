# Missing Schedule Recovery

1. Check whether the GTFS fetch failed or the merged snapshot failed validation.
2. Re-run `make scrape` and inspect the importer output.
3. If GTFS is still bad, use the PDF fallback to validate whether the day is recoverable.
4. Keep serving the last known good snapshot while recovery is in progress.
5. Confirm the stale-data banner is visible in the public app.
6. Pause or avoid freshness-sensitive alerts until a valid same-day snapshot is live.
7. After recovery, rerun the public and bot smoke checks.
