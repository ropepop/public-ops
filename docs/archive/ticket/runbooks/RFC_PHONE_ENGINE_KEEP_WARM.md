# RFC: Keep-Warm Mode for the Phone-Side Ticket Engine

Status: Draft, awaiting review
Author: ticket-remote team, after cold-start latency report on 2026-06-25
Affects: `pixel-phone/orchestrator/android-orchestrator/.../TicketStreamService.kt`,
         `pixel-phone/orchestrator/android-orchestrator/.../TicketScreenConfig.kt`,
         ticket-remote `control_code.go` (already partially done; the
         `controlCodePrepareRelayHold` constant is the handshake on this side)

## Problem

The public ViVi ticket viewer (`ticket.jolkins.id.lv`) takes 5–10 seconds
to deliver the first live H.264 frame to a fresh browser visit. The
network and the web client are fast; the bottleneck is the phone-side
ticket engine, specifically the cold-start chain in
`TicketStreamService.startTicketSessionLocked`.

The cold-start chain (measured from `pixel-phone/observations/` and the
recon in commit history) is:

| Phase | Wall-clock cost |
|---|---|
| 2s relay delay before `startTicketSession` | 2000ms |
| `input keyevent KEYCODE_WAKEUP` (timeout) | 0–3000ms |
| `waitForTicketScreenInteractiveForWake` | 0–900ms |
| `am start -n com.pv.vivi/.MainActivity` (timeout) | 0–3000ms |
| Post-launch poll for `TICKET_DETAIL` | 0–1400ms |
| `uiautomator dump` for root proof | 0–2500ms |
| Encoder process spawn + first keyframe | 500–1500ms |

`TICKET_WAKE_BUDGET_MILLIS = 5_000L` (line 11106) is the budget; cold
starts frequently overrun it, in which case the session enters
`TICKET_SESSION_NEEDS_ATTENTION` and the user sees a hung state.

The warm path (`session_start_already_active`, line 1037) returns
immediately in <10ms when the phone is already running. The 500–1000x
gap between cold and warm is the opportunity.

## Current Keep-Warm Gap

`TicketInactivityPolicy.TIMEOUT_MILLIS = 10 * 60 * 1000L` (10 minutes)
tears the session down after 10 minutes of viewer inactivity. After
teardown, `browserAutoStartAllowedAfterStop("viewer_inactivity_timeout")`
returns **false** (TicketScreenConfig.kt:629-630), meaning the next
browser visit cannot auto-revive the session — it must wait for an
explicit user action that triggers a full `startTicketSession`. In
practice this means every visitor who arrives more than 10 minutes after
the previous one pays the full 5–10s cold-start cost.

ticket-remote has a partial keep-warm: the `prepare` endpoint at
`/api/v1/control-code/prepare` (control_code.go:1313) calls
`preparePhoneRelayForControlCode` which retains a relay viewer for
12 seconds and forces a phone session start. This is wired in
the browser as of 2026-06-25 so the dialog-open triggers a
phone-warm. **It works, but only when the user opens the dialog.**
A fresh page visit that just wants to *look* at the ticket still
pays the full cold-start cost.

## Proposed Solution

Add an explicit keep-warm mode on the phone side that:

1. **Keeps the encoder running at steady FPS (4 FPS, 720p, 1.2Mbps)**
   when ANY of the following are true:
   - The phone has at least one authenticated viewer in the last
     10 minutes (relax the current 10-minute hard timeout to a
     10-minute *soft* timeout that switches to keep-warm mode).
   - ticket-remote reports a recent `prewarm` request
     (already partially implemented; extend the hold to 30s+).
   - The phone orchestrator detects the user is on a known network
     (e.g. wifi connected to home SSID; out of scope for this RFC).

2. **Marks the session as "warm" so the next visitor hits the
   `session_start_already_active` fast path** (line 1037). The fast
   path requires:
   - `streamActive = true`
   - `activeCaptureMode == CAPTURE_MODE_ROOT_HARDWARE_H264`
   - `hardwareCaptureVerified = true`
   - `frameAgeMillis <= 2_000L`
   - `viviStateMemory.current().state == TICKET_DETAIL` and age
     `<= 30_000L`

   Keep-warm mode needs to keep all five true.

3. **Releases keep-warm after a longer hard timeout** (30 minutes of
   zero authenticated activity) and switches to cold-mode. This
   bounds the phone resource cost.

4. **Allows the browser to auto-revive after keep-warm releases** by
   flipping `browserAutoStartAllowedAfterStop("keep_warm_release")`
   to `true`. Today only `viewer_inactivity_timeout` returns false;
   `keep_warm_release` should return true because the visitor is
   actively trying to view the ticket.

## Concrete File Changes (phone-side)

### `TicketStreamService.kt`

#### New state field (around line 280)

```kotlin
// True when the session is alive in keep-warm mode. Different from
// streamActive: a keep-warm session has the encoder running at
// steady FPS, ViVi in TICKET_DETAIL, and hardwareCaptureVerified
// all true, but is not actively streaming to a viewer.
@Volatile private var keepWarmMode: Boolean = false
```

#### Modify `startTicketSessionLocked` (line 1014)

Add the keep-warm fast path at the top, before the existing
`canReuseActiveHardwareStreamWithoutRootRevalidation` check:

```kotlin
private suspend fun startTicketSessionLocked(reason: String): TicketSessionResponse {
    if (keepWarmMode && canReuseActiveHardwareStreamWithoutRootRevalidation(reason)) {
        // keepWarmMode flag is cleared here because we now have an
        // active viewer.
        keepWarmMode = false
        return TicketSessionResponse(ok = true, state = "active", reason = "keep_warm_reuse:$reason")
    }
    // ... existing code ...
}
```

#### Modify `stopTicketSession` (line 1283)

Replace the immediate full-stop with a keep-warm transition when the
stop reason is a soft stop (not browser_explicit_stop):

```kotlin
private fun stopTicketSession(reason: String, ...): Boolean {
    val isSoftStop = reason != "browser_explicit_stop" && reason != "viewer_inactivity_timeout"
    if (isSoftStop && viewerCount() > 0) {
        // Active viewers present but the caller wants to stop; this
        // is unusual. Fall through to full stop.
    } else if (isSoftStop) {
        // No active viewers. Switch to keep-warm instead of full stop.
        transitionToKeepWarmMode(reason)
        return true
    }
    // ... existing full-stop code ...
}

private fun transitionToKeepWarmMode(reason: String) {
    keepWarmMode = true
    val keepWarmTimeoutAt = SystemClock.elapsedRealtime() + KEEP_WARM_TIMEOUT_MILLIS
    // Schedule a hard-stop after the keep-warm window.
    handler.postDelayed({ stopTicketSession("keep_warm_release") }, KEEP_WARM_TIMEOUT_MILLIS)
    // Notify ticket-remote that the phone is now in keep-warm mode
    // (so the next page visit can land on the fast path).
    s.notifyKeepWarmEntered(reason, KEEP_WARM_TIMEOUT_MILLIS)
}
```

#### Add constants in the companion object (around line 11150)

```kotlin
internal const val KEEP_WARM_TIMEOUT_MILLIS = 30L * 60L * 1000L  // 30 minutes
internal const val KEEP_WARM_MIN_VIEWER_INTERVAL_MILLIS = 5L * 60L * 1000L  // refresh every 5 min
```

### `TicketScreenConfig.kt`

#### Line 629-630: allow browser to auto-revive after keep-warm release

```kotlin
// In browserAutoStartAllowedAfterStop():
"keep_warm_release" -> true,  // was: default false
"viewer_inactivity_timeout" -> false,  // unchanged
```

#### Line 599-600: separate hard-stop from soft-stop

```kotlin
internal object TicketInactivityPolicy {
    // The hard-stop timeout (no viewers, no keep-warm, phone can sleep).
    const val TIMEOUT_MILLIS = 30L * 60L * 1000L  // was 10 min; bump to 30 min
}
```

## Concrete File Changes (ticket-remote side)

Already done in this commit (2026-06-25):

- `controlCodePrepareRelayHold`: 8s → 12s
- `openControlCodeDialog()` in `ticket-app-source.js` now calls
  `/api/v1/control-code/prepare` on dialog open.
- The browser pre-warms the phone via the prepare endpoint.

Recommended next step (not in this commit, awaiting phone-side readiness):

- Add `/api/v1/stream/prewarm?hold=30000` endpoint that the browser
  calls on every authenticated page load when the user is about to
  scroll the page or interact (rather than only on dialog open). The
  endpoint calls `retainRelayViewerForPrewarm` with the requested hold
  duration. This requires the phone-side keep-warm to be in place
  first.

## Acceptance Criteria

After both sides are deployed:

1. **Cold start (browser, phone session is in keep-warm mode)**:
   first H.264 frame lands in `< 1s` (no wake, no ViVi launch, no root
   proof).
2. **Cold start (browser, phone session has fully released)**:
   first H.264 frame lands in `< 5s` 50% of the time, `< 8s` 95%
   of the time.
3. **Phone resource cost in keep-warm mode**: < 5% battery/hour,
   < 200MB RAM, CPU < 5% on the orchestrator process.

## Risk

- Keep-warm mode keeps the screen on (the SCREEN_BRIGHT_WAKE_LOCK has
  a 30s hold, line 1475). For long keep-warm windows the wake lock
  will need to be re-acquired periodically. A 30-minute wake lock is
  within Android's tolerance but should be refreshed.
- ViVi in foreground for 30+ minutes may attract Android system
  attention (battery saver, doze mode). The orchestrator's
  notification-lockdown + secure-window-bypass paths are
  already designed to suppress this; verify they still work in
  keep-warm mode.
- The keep-warm mode doubles the steady-state phone resource cost.
  For 5+ concurrent keep-warm windows (multiple test tickets) the
  cost is 5x. We do not have this scenario in production today
  (single ticket `vivi-default`), but the architecture should
  support it without surprise.

## Rollout

Phase 1 (this commit): ticket-remote side changes (already done).
Verify the prepare flow works in production. Observe the cold-start
latency for users who have the dialog open.

Phase 2: phone-side keep-warm mode. Implement, deploy to test
device, observe latency, then promote.

Phase 3: increase `controlCodePrepareRelayHold` to 60s and
`streamPrewarmHold` to 30s, wire the browser to call prewarm on
every authenticated page load.

Phase 4: bump `TicketInactivityPolicy.TIMEOUT_MILLIS` to 30 minutes.
