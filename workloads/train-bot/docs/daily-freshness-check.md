# Daily Freshness Check

1. Confirm the latest schedule snapshot exists in `data/schedules/`.
2. Hit the runtime readiness endpoint and verify it returns HTTP 200.
3. Confirm the diagnostic health payload reports `ready=true` and `scheduleAvailable=true`.
4. Check whether the app is serving the expected `loadedServiceDate`.
5. If fallback mode is active, confirm the public banner is visible and alerts that require fresh same-day data stay paused.
6. Run the public smoke test before making any manual intervention.

If the newest schedule is missing or invalid, switch to the missing-schedule recovery runbook.
