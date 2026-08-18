# Ticket UX 24h Watch

Objective: keep the ticket app up, smooth, and fast for 24 hours. Gates: phone-to-browser raw ticket and generated Aztec frames under 2 seconds, no user-side interaction errors, Pixel returns to raw ticket unless an active task is in progress, and phone/web/Spacetime state stay aligned.

## 2026-05-13 00:50-01:15 Europe/Riga

Finding:
- Public v63 exposed that the bridge was dropping some fresh frames as `future_clock`; the dropped frames were not old video, they were fresh phone frames that looked slightly ahead of bridge time because clock calibration was using bridge receive time too literally.
- A browser-side bug also allowed a rendered frame to age past the hard 2 second limit without immediately being counted in the local stale decision.
- The older 6 second delay did not reproduce on v64 after the clock handling fix. The remaining cold-start wait is hardware encoder first output, not browser decoding: an isolated cold-ish run decoded in about `1702 ms`, with the Pixel logs showing raw-ticket readiness in about `292 ms` and first encoder sync output about `1.3 s` after start.

Change made:
- Browser freshness now includes the currently rendered frame's visual age, so an old picture cannot stay labeled live just because the decoder has not reported a new problem yet.
- Bridge frame-drop diagnostics were split into explicit reasons: invalid, wrong epoch, missing timestamp, uncalibrated clock, future clock, and forward-age backlog.
- Bounded future phone-clock skew is corrected instead of dropping fresh frames when the skew is within the freshness budget.
- Deployed public `ticket-remote-2026-05-13-pixel-only-hardware-v64`.

Validation:
- `node --check workloads/ticket-remote/internal/web/static/app.js` passed.
- `go test ./...` in `workloads/ticket-remote` passed.
- `bash test_arbuzas_deploy_contract.sh` passed.
- Arbuzas deploy and explicit `ticket_remote` validation passed.
- Public `/api/v1/livez` returned `ticket-remote-2026-05-13-pixel-only-hardware-v64`.
- Authenticated public browser quick check on v64: first decoded frame about `236 ms`, current visual age about `105 ms`, zero dropped frames, and bounded clock adjustments active.
- 10-minute authenticated browser sample on v64:
  - samples: `627` over `628 s`
  - median visual age: `47 ms`
  - p95 visual age: `280 ms`
  - p99 visual age: `438 ms`
  - max visual age: `662 ms`
  - frames over `2000 ms`: `0`
  - live-labeled frames over `2000 ms`: `0`
  - stale samples: `0`
  - bridge forwarded `2560/2560` frames during the sampled window.
- Warm reload after v64: first decoded frame about `485 ms`.
- Isolated close-tab/no-viewer wait/reopen run after v64: first decoded frame about `1702 ms`, current frame age about `60 ms`, and page stayed live.
- Added a focused slow-viewer unit test proving a blocked viewer does not accumulate a delta-frame backlog: one delayed delta can wait briefly, a second delayed delta clears the queue, and the viewer must resume from the next keyframe.
- Tightened server-side stream recovery rate limiting from `12 s` to `1.5 s` so a disconnected relay can be asked to recover inside the 2 second freshness contract. Added a focused budget test for this.
- After the slow-viewer and recovery coverage pass, `go test ./...` in `workloads/ticket-remote`, `node --check workloads/ticket-remote/internal/web/static/app.js`, and `bash test_arbuzas_deploy_contract.sh` passed again.
- Deployed public `ticket-remote-2026-05-13-pixel-only-hardware-v65`; explicit Arbuzas validation passed and `/api/v1/livez` returned v65.
- Authenticated browser proof on v65: first decoded frame about `125 ms`, current visual age about `0-106 ms`, bridge forwarded `162/162` frames, and no dropped frames.

Do not repeat:
- Do not treat `frames received` as enough proof; the browser-rendered visual age is the live contract.
- Do not collapse future-clock, uncalibrated-clock, and true forward-age drops into one stale bucket; they have different fixes.
- Do not loosen the browser stale threshold to hide a late frame. A frame above 2 seconds must be stale even if recovery is still underway.
- Do not diagnose the current cold-start edge as browser decode delay before checking Pixel encoder first-sync timing.

## 2026-05-13 00:08-00:16 Europe/Riga

Finding:
- Public page authentication was restored with the shared browser profile and a server session for `ticket@jolkins.id.lv`.
- Before installing the newer Pixel build, the public bridge received video bytes but forwarded zero frames: `sourceFramesReceived=147`, `framesForwarded=0`, `droppedStaleFrames=147`.
- The bridge was rejecting frames because it no longer had a current Pixel clock calibration. The deployed Pixel was `ticket-stream-2026-05-12-freshness-contract-v193`; local Pixel code was already `ticket-stream-2026-05-12-phone-timestamp-freshness-contract-v194`, which includes `phoneUptimeMillis` in health.

Change made:
- Ran Pixel unit tests and debug build.
- Installed the updated Pixel APK and restarted the ticket component.
- Verified the phone bridge now reports `ticket-stream-2026-05-12-phone-timestamp-freshness-contract-v194` and includes `phoneUptimeMillis`.

Validation:
- `./gradlew :app:testDebugUnitTest` passed.
- `./gradlew :app:assembleDebug` passed.
- Pixel deploy installed successfully and `health_component` reported success.
- Authenticated public browser reload after the Pixel deploy reported:
  - `stream_config_received_ms=2`
  - `stream_first_packet_ms=46`
  - `stream_config_to_first_packet_ms=45`
  - `page_first_decoded_frame_ms=296`
  - `stream_config_to_first_decoded_ms=61`
  - `stream_first_decoded_frame_ms=62`
  - browser rendered frame age about `68 ms`
- Public health showed bridge freshness recovered: `streamVerdict=live`, `phoneClockCalibrated=true`, `lastFrameVisualAgeMillis` about `327 ms`, and active stream mode remained hardware H.264.
- A two-minute authenticated browser sample after the Pixel deploy showed median browser frame age `3 ms`, p95 `329 ms`, p99 `972 ms`, and zero live-labeled frames over 2 seconds. It did catch one stale/recovering spike over 2 seconds, so the browser stale-socket reconnect and server-recovery thresholds were reduced from `6500/3000 ms` to `2000/2000 ms` for the next public bridge deploy.

Do not repeat:
- Do not diagnose this specific “waiting first frame while bytes arrive” case as browser decode or network delay before checking `framesForwarded` versus `sourceFramesReceived`.
- Do not let Pixel deploys use `--skip-build` when the APK version marker must change; that only restarts the already-installed package.
- Do not remove the phone uptime health field or the bridge will lose ongoing frame freshness calibration again.

## 2026-05-11 08:18-08:25 Europe/Riga

Change made:
- Replaced the web relay/browser stream behavior that effectively showed keyframes only.
- Server now accepts and forwards live delta frames after each viewer has received a keyframe.
- New viewers still receive cached config plus only a fresh keyframe; stale queued frames are dropped.
- Browser now decodes live delta frames instead of discarding non-keyframes.
- Expected missing direct Spacetime token on old server sessions is no longer logged as user-side error telemetry.

Reason:
- The observed 6 second stream delay matches keyframe cadence. Network was fast, but the page only rendered keyframes.

Validation:
- `node --check internal/web/static/app.js`
- `go test ./...` in `workloads/ticket-remote`
- Deployed `ticket-remote-2026-05-11-live-latest-v4`.
- Public `/api/v1/livez` returns `ticket-remote-2026-05-11-live-latest-v4`.
- Deploy validation passed: local livez, production state backend, public login shell, HTTP redirect, safety headers, auth config, OIDC issuer, stale viewer code absent.
- Live authenticated browser telemetry before v4 follow-up showed:
  - `stream_config_received_ms=170`
  - `stream_first_decoded_frame_ms=1864`
  - `stream_config_to_first_decoded_ms=1703`

Do not repeat:
- Do not return to keyframe-only browser playback unless there is evidence that delta decoding is broken.
- Do not solve stream latency by adding another media transport before exhausting latest-frame H.264 delivery.
- Do not treat expected absence of direct Spacetime token on existing server-cookie sessions as a user-visible error.

Next checks:
- Confirm v4 browser session reports first decoded frame under 2 seconds.
- Confirm generated Aztec frame-to-browser freeze remains under 2 seconds on a live request.
- Confirm no `loading_over_2s`, stale stream, decoder failure, or control-code cleanup attention logs.
- Confirm Pixel/default state is raw ticket after every request or error.

## 2026-05-11 08:29-08:34 Europe/Riga

Finding:
- Public web v4 was deployed, but the Pixel was still hard to interpret because the phone service version marker had not changed.
- Live health then showed a stale control-code popup/generated state during wake. The phone did close it and prove the raw ticket, but wake still ended as failed because the readiness loop kept polling until the 5 second budget expired.
- That failure stopped the hardware encoder while viewers were still connected, which caused stale video, decoder recovery churn, and multi-second first-frame delay.

Change made:
- Bumped Pixel ticket service marker to `ticket-stream-2026-05-11-raw-wake-accept-v139`.
- If wake finds an old control-code popup or generated result and the root-only return-to-raw routine succeeds, wake now immediately counts as ticket-ready instead of timing out.
- Cleanup now refreshes the remembered ViVi state to raw ticket so phone health does not keep reporting a stale popup after successful cleanup.
- Popup cleanup is reported as popup cleanup, not generated-result cleanup.

Validation:
- `./gradlew :app:testDebugUnitTest :app:assembleDebug` passed.
- Installed the APK on Pixel `100.76.50.43:5555` and restarted the ticket service through the exported orchestrator receiver.
- Phone health now reports the patched ticket service, `sessionState=live`, `streamVerdict=live`, `viviState=TICKET_DETAIL`, `pixelTicketState=raw_ticket`, hardware encoder active, and fresh frames around 16 ms old.
- Clean authenticated browser reload after the fix reported `stream_first_decoded_frame_ms=174`.

Do not repeat:
- Do not mark wake as failed after a root-only cleanup has already confirmed raw ticket and fresh video.
- Do not let remembered ViVi state remain on popup/result after cleanup returns to ticket detail.
- Do not interpret service-restart stale-video logs as steady-state browser latency; separate deploy noise from clean reload measurements.

## 2026-05-11 08:57-09:05 Europe/Riga

Finding:
- After web v7, phone health showed fresh hardware frames and raw-ticket state, but browser telemetry still sometimes took several seconds to show the first frame.
- The first browser fix in v8 proved that decode itself is fast once config and frames are stable: first decoded frame was 434 ms from video socket open and 227 ms after config.
- A later reload still took 6704 ms. Logs showed the cause was not image processing or network delivery: the old video socket closed, the relay sent a stop to the phone, then the new socket had to start the same hardware stream again. The actual decode after the final config was only 121 ms.

Change made:
- Browser first-frame recovery now waits when fresh packets are arriving, instead of resetting/reconnecting the decoder during normal startup.
- Public bridge default no-viewer stop delay changed from 1 second to 8 seconds. This keeps the hardware stream alive across ordinary reload/reconnect gaps, while still stopping the encoder shortly after viewers leave.
- Version markers:
  - v8: `ticket-remote-2026-05-11-live-latest-browser-decode-v8`
  - v9: `ticket-remote-2026-05-11-live-latest-browser-decode-v9`

Validation:
- `node --check workloads/ticket-remote/internal/web/static/app.js`
- `go test ./...` in `workloads/ticket-remote`
- Deployed v8 and then v9 on Arbuzas.
- Public `/api/v1/livez` returns v9.
- Clean authenticated browser reload on v9 reported:
  - `stream_video_socket_open`
  - `stream_config_received_ms=629`
  - `stream_first_decoded_frame_ms=657`
  - `stream_config_to_first_decoded_ms=30`
- During that v9 reload window there were no `session/stop`, `stop phone session`, decoder reset, stream restart, or stale-video recovery logs.
- Phone health stayed raw ticket / live hardware stream with fresh frames around 100-250 ms old.

Do not repeat:
- Do not reduce the no-viewer delay back to 1 second; it reintroduces stop/start churn during normal browser reloads.
- Do not treat a slow first frame as browser decode delay until checking whether the relay stopped the phone between old and new video sockets.
- Do not add a new transport to solve this specific class of delay; the measured steady path is already under 1 second once stop/start churn is removed.

## 2026-05-11 09:06-09:23 Europe/Riga

Finding:
- Warm reloads are now fast, but cold opens from no viewers still measured above 2 seconds.
- v10 showed that duplicate config handling was not the main blocker.
- Browser automation was incorrectly treating Headless Chrome as Safari, forcing the slower AVC adapter path. v11 fixed that; headless browser now uses the normal Annex-B H.264 path.
- Cold open still took several seconds because Pixel wake/foreground was the slowest phase, not server-to-browser delivery. Phone health reported:
  - `lastTotalMillis=3589`
  - slowest phase `vivi_foreground`
  - slowest phase duration `3426`
- Once the Pixel was producing frames, browser/phone health showed fresh frames around 39-250 ms old.

Change made:
- v10: browser ignores duplicate video config for the same stream epoch instead of resetting the decoder repeatedly.
- v11: Headless Chrome is no longer classified as Safari, so automated Chrome checks use the direct H.264 path.
- v12: normal idle shutdown now sends the stop command over the already-open phone WebSocket instead of making a separate HTTP stop request that was timing out.

Validation:
- `node --check workloads/ticket-remote/internal/web/static/app.js`
- `go test ./...` in `workloads/ticket-remote`
- Deployed v10, v11, then v12.
- Public `/api/v1/livez` returns `ticket-remote-2026-05-11-live-latest-browser-decode-v12`.
- Idle shutdown after v12 no longer logged `session/stop` timeout; phone health showed `sessionState=stopped`, `streamVerdict=idle`, zero clients, encoder off, and ViVi still on `TICKET_DETAIL`.
- Reopen after idle on v12 still reported a slow first decoded frame (`stream_first_decoded_frame_ms=4655`), but live frame freshness after startup was healthy (`lastFrameAgoMillis` around 39 ms on phone health).

Do not repeat:
- Do not spend more time on browser image upload/base64 paths; result image upload is already removed from the active path.
- Do not blame browser decode for cold-start delay without checking Pixel wake timings.
- Do not bring back HTTP stop for normal idle shutdown; it caused long timeout noise and stale reconnect behavior.
- Next useful cut is Pixel wake/foreground and hardware encoder first-frame startup, not another browser transport rewrite.

## 2026-05-11 10:28-10:35 Europe/Riga

Finding:
- v17/v144 and v18/v145 improved clean first-frame timing, but not reliably enough for the under-2-second gate.
- Raising hardware stream FPS to 6 and lowering keyframe interval to 160 ms helped some runs but did not solve the unstable runs by itself.
- Suppressing duplicate phone config broadcasts removed one decoder reset source, but the bad window still showed repeated browser recovery actions.
- v147 startup encoder drain tuning did not prove a clear benefit; one cold run measured `stream_first_decoded_frame_ms=3105`, and a later recovery-churn run logged a misleading `11771` ms first frame.
- The most concrete root cause in the bad run was startup recovery churn: browser recovery while the phone relay was still connecting cancelled the in-progress relay and started another one.

Change made:
- Added a failing server test proving startup recovery must not restart an already-connecting phone relay.
- Added a browser source check for an explicit startup recovery block.
- v19 server/browser change: while the first video frame is still in startup, browser/server recovery may request/keyframe/wait, but it must not restart or reconnect the phone relay until the startup block expires.

Validation:
- Targeted test first failed with `control dials = 2, want 1`, confirming the bad restart.
- After the fix, targeted tests passed:
  - `go test ./internal/web -run 'TestStartupRecoveryDoesNotRestartConnectingRelay|TestTicketHTMLIncludesExpectedStreamCode'`
- Full public ticket tests passed:
  - `node --check internal/web/static/app.js`
  - `go test ./...` in `workloads/ticket-remote`

Do not repeat:
- Do not add more browser/server recovery actions during first-frame startup. The recovery loop itself caused a measured delay class.
- Do not keep tuning FPS/keyframe/drain without separating encoder startup time from browser relay restart churn.
- If first-frame is slow after v19 without recovery churn, measure the phone helper first-output time before changing the public bridge again.

## 2026-05-11 10:35-10:58 Europe/Riga

Finding:
- v21/v150 showed the public page could receive config and decode quickly on warm stream, but submit could still fail because an optional secure probe was allowed to block the root-only immediate path.
- v152 allowed hardware startup frames before root readiness, but this alone still left cold first frame above the 2 second target.
- v153 removed the pre-start root process scan from the hardware encoder critical path. This removed a real startup delay, but it also exposed that stale helpers need cleanup outside the critical start path.
- v154 added low-latency hardware encoder options. One live run decoded the first frame in 1.83 seconds, but results still hovered around the 2 second boundary.
- v155 cleans stale hardware helpers on service start and keeps the normal path hardware-only/root-only.
- v22 fixed browser recovery churn where fresh decoded local video was ignored because stale server status metadata said frames were old.

Change made:
- Phone v151: immediate control-code start no longer depends on optional secure-blank probe visibility.
- Phone v152: startup frames may be broadcast before root readiness proof, but the session is only marked live after root readiness is verified.
- Phone v153: hardware H.264 startup no longer blocks on the stale-process scan; post-start sanity check runs after the encoder is already starting.
- Phone v154: hardware encoder requests low latency, realtime priority, operating rate, and CBR when supported.
- Phone v155: service startup performs stale helper cleanup before probe/start.
- Public v22: browser recovery treats a fresh locally decoded frame as authoritative and avoids unnecessary restart/recovery actions.

Validation:
- Pixel: `./gradlew :app:testDebugUnitTest :app:assembleDebug` passed for v155.
- Public bridge: `node --check internal/web/static/app.js` and `go test ./...` passed for v22.
- Public `/api/v1/livez` returns `ticket-remote-2026-05-11-fresh-local-video-v22`.
- Pixel health reports `ticket-stream-2026-05-11-clean-fast-encoder-v155`, raw ticket, active stream mode `root_hardware_h264`, root-only automation, and fail-fast policy.

Do not repeat:
- Do not re-add software, screenshot, accessibility, or browser image upload fallbacks.
- Do not let optional secure capture probe state block the fast control-code path.
- Do not put blocking process cleanup back into encoder startup.
- Do not restart/recover the stream while the browser has a fresh decoded local frame.
- Current remaining bottleneck is hardware helper first-output / browser first decoded frame near the 2 second boundary, not network round-trip or page config delivery.

## 2026-05-11 11:28-11:36 Europe/Riga

Finding:
- Public v22 plus Pixel v155 was stable but a clean public open still measured slightly above budget: `stream_first_decoded_frame_ms=2175`, `stream_config_to_first_decoded_ms=1978`.
- Pixel wake was already fast in the clean run, about 336 ms or less. The remaining delay was first hardware H.264 output and first browser decode.
- Pixel v156 startup priming captured once and submitted the same startup frame multiple times to the hardware encoder. This preserved the same visible image and resolution while avoiding several slow screen captures before first encoder output.
- v156 improved one clean socket to `stream_first_decoded_frame_ms=1493`, but an immediately-after-install probe race produced an initial unavailable/stale socket that later recovered.
- Pixel v157 removed repeated keyframe requests inside the startup burst. The clean v157 run showed a very fast fresh frame after socket replacement (`stream_first_decoded_frame_ms=25`, `stream_config_to_first_decoded_ms=23`), but the page opened two video sockets during that run, so the next work is to separate true user-visible first frame from post-reconnect first frame.

Change made:
- Phone v156: hardware helper startup now captures once and primes the encoder with repeated posts of that same frame.
- Phone v157: startup burst requests a sync frame once per captured frame instead of once per repeated post.

Validation:
- Pixel: `./gradlew :app:testDebugUnitTest :app:assembleDebug` passed for v156 and v157.
- Pixel v157 installed successfully and health reports hardware H.264 available, root-only automation, and raw-ticket state.
- Clean v156 run: public config arrived in 2 ms and first decoded frame arrived in 2014 ms, with Pixel first visible frame wait 1454 ms.
- Clean v157 run: public logs showed config in 2-3 ms and first decoded frame in 25 ms after the second video socket; live frame freshness was healthy after that.

Do not repeat:
- Do not count a post-reconnect `stream_first_decoded_frame_ms` as final proof until page-level first-visible timing is also checked.
- Do not keep adding network changes for this bottleneck; config delivery is consistently low single-digit milliseconds.
- Do not prime startup by taking multiple screen captures; the useful direction is fewer captures before first hardware output.

## 2026-05-11 11:45-12:26 Europe/Riga

Finding:
- v25 early video socket still showed page-level first decoded frame above target: `page_first_decoded_frame_ms=2660` and one clean run at `3605`.
- The phone reaction path was not the bottleneck. Pixel wake stayed fast, about 214-336 ms. The remaining delay was browser page startup plus the first hardware frame.
- v26 deferred the heavy Spacetime client until after first frame / short delay. This removed the main script-blocking issue and dropped DOM complete from about 1.85 s to about 0.63 s in one clean comparison.
- v27 started Pixel stream prewarm immediately after auth proof and before the full state lookup. This cut one clean page-level run to `2166` ms and stream socket-to-decoded-frame to `1270` ms.
- v28 tried inlining the full ticket app script, but this was worse: the HTML response became too large and first frame rose to `2814` ms. Do not repeat full app inlining.
- v29 made versioned static assets cacheable and stopped clearing browser cache, but asset URLs still changed every request because `assetVersion()` used `time.Now()` each render.
- v30 made the asset version stable for the running process. With cached versioned assets and the encoder starting from idle, the public page measured `page_first_decoded_frame_ms=1907`, `stream_first_decoded_frame_ms=917`, and `stream_config_to_first_decoded_ms=913`.

Change made:
- Public v26: load the ticket app before Spacetime, and dynamically load Spacetime after first decoded frame or a short delay.

## 2026-05-11 15:31-15:43 Europe/Riga

Finding:
- A real request failed after about 36 seconds even though the phone had moved through the control-code flow.
- Pixel events showed popup open, digits typed, and submit tapped, then the phone waited for a generated-result state until timeout.
- The live root hierarchy after the failure showed the ViVi ticket-detail screen with the large ticket/Aztec graphic and ticket id. This was still classified as raw ticket, so the phone never sent `control_code_frame_ready`.
- The public bridge also had a bad race: if Pixel reported raw-ticket cleanup while the browser was still confirming the frozen frame, the server could turn the request into a user-visible failure with reason `return_to_raw_complete`.

Change made:
- Pixel v173: post-submit result wait now accepts a ticket-detail screen with the large ticket/Aztec graphic as the browser-freeze target, but only in the post-submit wait path.
- Pixel v173: reduced `CONTROL_CODE_FAST_RESULT_TIMEOUT_MILLIS` from 25 seconds to 2.4 seconds.
- Public v40: raw-ticket cleanup arriving before browser metadata confirmation no longer changes a capturing request into a failure. The browser can still confirm the already-frozen frame; cleanup success is kept as cleanup metadata, not a result error.

Validation:
- Pixel targeted tests passed for `TicketViviPageEnforcerTest` and `TicketStreamServiceSourceTest`.
- Pixel full `./gradlew :app:testDebugUnitTest` and `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed phone health reports `ticket-stream-2026-05-11-post-submit-ticket-frame-v173`.
- Public bridge regression first failed with `not_capturing` after early raw return, then passed after the fix.
- Public `node --check internal/web/static/app.js` and `go test ./...` passed.
- Deployed `ticket-remote-2026-05-11-native-annexb-capture-before-raw-v40`; deploy validation passed.
- Public `/api/v1/livez` returns v40.
- Clean authenticated public browser sample after deploy:
  - `stream_config_received_ms=9`
  - `stream_first_packet_ms=1083`
  - `stream_first_decoded_frame_ms=1097`
  - `page_first_decoded_frame_ms=1895`
  - browser debug first decoded around 1895 ms, live frame age around 37 ms
  - canvas stayed 900x1852
- Pixel health after closing the browser tab showed encoder stopped, zero clients, and idle hardware capture.

Do not repeat:
- Do not let `return_to_raw_complete` or any raw-ticket cleanup reason become the failure reason for a request that is still waiting on browser-frame confirmation.
- Do not restore the 25 second generated-result wait; it creates the visible long hang after submit.
- Do not globally classify raw ticket detail as a generated result. The ticket-detail/Aztec acceptance belongs only to the post-submit wait path.
- Do not add phone screenshots, browser image upload, software stream fallback, or accessibility fallback to solve this class of issue.

## 2026-05-11 15:45-15:53 Europe/Riga

Finding:
- A real iPhone request on v173 still failed: `control_code_result_timeout`, total about 13 seconds.
- Pixel events showed the fast path opened the popup, typed digits, and tapped OK, then spent the delay waiting for root result detection.
- The reason the 2.4 second timeout did not hold was that each root hierarchy read after submit can overrun while ViVi is changing state. The loop was still blocked by root inspection, so the phone did not send `control_code_frame_ready` even though cleanup later classified the screen as `CONTROL_CODE_RESULT`.

Change made:
- Pixel v174 removes root hierarchy polling from the post-submit result gate.
- After the OK tap, the phone waits 900 ms, requests the fresh hardware frame watermark, and sends `control_code_frame_ready`.
- Browser freeze remains the result source. Root is still used for cleanup after browser confirmation, not for deciding whether to let the browser capture.

Validation:
- Added/updated source tests proving post-submit browser-frame readiness no longer calls root hierarchy polling and uses `CONTROL_CODE_POST_SUBMIT_FRAME_SETTLE_MILLIS = 900L`.
- Pixel `TicketStreamServiceSourceTest` passed.
- Full Pixel `./gradlew :app:testDebugUnitTest` and `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed phone health reports `ticket-stream-2026-05-11-browser-frame-after-submit-v174`.
- Post-install public browser sample still decoded under 2 seconds:
  - first packet about 1889 ms
  - first decoded about 1901 ms
  - live frame age about 10 ms
  - canvas 900x1852
- No control-code result test has run yet after v174; watch for the next real request or run a known-good-code live proof.

Do not repeat:
- Do not put root hierarchy reads back into the post-submit result gate; they are the cause of the multi-second “doing nothing” period.
- Do not treat a Kotlin timeout constant as an actual wall-clock cap if the awaited root command can overrun it.
- Keep root-only cleanup after browser confirmation, but keep result capture driven by the hardware video frame.
- Public v27: run authenticated index prewarm before the state snapshot lookup finishes.
- Public v29: cache versioned static assets and remove `Clear-Site-Data: "cache"` from normal no-store responses.
- Public v30: make `assetVersion()` stable for the process so browser cache can actually be reused.

Validation:
- `node --check internal/web/static/app.js` passed.
- `go test ./...` in `workloads/ticket-remote` passed for v26, v27, v28, v29, and v30 iterations.
- Deployed public bridge v30: `ticket-remote-2026-05-11-stable-asset-version-v30`.
- Public `/api/v1/livez` returned v30.
- Static assets now return `Cache-Control: public, max-age=31536000, immutable`.
- Browser proof with authenticated session and idle encoder: `page_first_decoded_frame_ms=1907`, `stream_first_decoded_frame_ms=917`.
- After closing the page, Pixel returned to no clients and encoder stopped.

Do not repeat:
- Do not inline the full app script into the authenticated page; it increases HTML transfer time enough to lose the first-frame gain.
- Do not use a per-request timestamp as the static asset version. It defeats cache and makes every page open pay for app.js and CSS.
- Do not re-add `Clear-Site-Data: "cache"` to normal ticket page responses; it destroys the cache needed for sub-2-second repeat opens.
- Keep Spacetime deferred behind video startup; server control socket and Pixel events remain the fast alignment path for first paint.

## 2026-05-11 14:20-14:45 Europe/Riga

Finding:
- After v30, the remaining bad startup cases were no longer page config or browser decode alone. The page could decode under 2 seconds, but stale stop/reconnect races still caused occasional long waits.
- Pixel v166 queued early hardware keyframe requests and deferred startup maintenance until the first frame or a short timeout. This removed skipped keyframe requests while the hardware helper was still starting.
- Pixel v167 protected new sessions from stale browser HTTP stops during navigation.
- Pixel v168 avoided a full root hardware probe during normal session start when a lightweight hardware probe was enough.
- Pixel v169 fixed the zero-client stop guard, but a real live check found one remaining idle bug: a `relay_no_viewers` stop was treated as stale because the bridge control socket is older than the bridge video socket.
- Public v37 fixed the bridge relay case where the server had `desired=true` but the phone relay was disconnected, so a new viewer could wait until browser recovery kicked in.

Change made:
- Phone v166: early keyframe requests are queued until the hardware encoder process is ready; startup maintenance is deferred after first frame.
- Phone v167: HTTP browser stop is ignored while fresh clients are connected.
- Phone v168: session start uses a lightweight hardware probe to avoid startup probe delay.
- Phone v169: stale explicit stops no longer block a stop when there are zero clients.
- Public v37: `AddViewer` restarts the relay connect loop when desired but disconnected.
- Phone v170: `relay_no_viewers` is now trusted as the idle stop signal and no longer goes through the stale-generation check.

Validation:
- Pixel tests/build passed for v166-v170:
  - `./gradlew :app:testDebugUnitTest`
  - `./gradlew :app:assembleDebug`
- Public bridge tests passed for v37:
  - `node --check internal/web/static/app.js`
  - `go test ./...` in `workloads/ticket-remote`
- Public `/api/v1/livez` returns `ticket-remote-2026-05-11-native-annexb-relay-disconnected-viewer-restart-v37`.
- Pixel health returns `ticket-stream-2026-05-11-relay-no-viewers-stop-v170`.
- Live authenticated page open from idle:
  - v37/v169: `page_first_decoded_frame_ms=1804`.
  - v37/v170: `page_first_decoded_frame_ms=1753`.
  - v37/v170 second open after stopped state: `page_first_decoded_frame_ms=1495`.
- Canvas stayed at `900x1852`; visible aspect ratio remained healthy for the ticket/Aztec area.
- Prepare-only control-code dialog check returned ready from recent raw-ticket state and did not open/generated-code state.
- After closing the page with v170, Pixel returned to stopped/idle with no hardware encoder active.

Do not repeat:
- Do not treat bridge `desired=true` as proof the phone relay is connected; `desired=true` plus disconnected needs a new connect loop.
- Do not use the client generation stale-stop guard for `relay_no_viewers`; the bridge control and video sockets naturally have different generations.
- Do not count a retained prepare/prewarm lease as a reason to keep the Pixel encoder alive after all browser viewers are gone.
- Do not re-add software stream, phone screenshots, browser image/base64 uploads, or accessibility fallback paths.

## 2026-05-11 14:45-14:55 Europe/Riga

Finding:
- Two real control-code attempts failed after the phone successfully tapped the ViVi control-code button. The phone then waited on a second root hierarchy read, failed with `control_code_root_retry_unavailable`, and only then closed the popup.
- Those failures took about 8.7 seconds and 13.0 seconds end-to-end. Cleanup did return to raw ticket, but this still violated the target and created a user-visible error.
- The generated/browser capture timeout was still 25 seconds, which can explain the old 10-20 second generated-Aztec hang when the browser capture confirmation path misses.
- A diagnostic hierarchy dump of the actual ViVi popup showed stable geometry on the Pixel: input center around `x=540 y=1238` on `1080x2424`, and the shifted OK geometry was already present in the app for keyboard-open submit.

Change made:
- Public v38: browser-frame capture confirmation timeout reduced from 25 seconds to 3 seconds; browser capture polling reduced to 50 ms and retry delay to 250 ms.
- Phone v171: after a successful immediate tap on the ViVi control-code button, the phone waits 180 ms and uses measured popup geometry directly instead of waiting on another root hierarchy read.
- Added tests preventing the long browser capture timeout from returning and preventing the immediate popup-open path from blocking behind the slow second root wait.

Validation:
- Public bridge v38:
  - `node --check internal/web/static/app.js`
  - `go test ./...` in `workloads/ticket-remote`
  - `/api/v1/livez` returns `ticket-remote-2026-05-11-native-annexb-fast-capture-v38`
- Pixel v171:
  - `./gradlew :app:testDebugUnitTest`
  - `./gradlew :app:assembleDebug`
  - Installed on the Pixel and health reports `ticket-stream-2026-05-11-popup-geometry-fast-path-v171`.
- Live v38/v171 public page open from stopped state decoded the first frame in `page_first_decoded_frame_ms=1619`.
- Prepare-only browser dialog check returned ready, and closing the page returned the Pixel to stopped/idle with no hardware encoder active.
- Follow-up watch open from idle decoded the first frame in `1684` ms, kept canvas at `900x1852`, and returned the Pixel to stopped/idle after close.
- Later watch open from idle decoded the first frame in `1899` ms, kept canvas at `900x1852`, and returned the Pixel to stopped/idle after close.

Do not repeat:
- Do not let the successful immediate popup tap depend on a second root hierarchy read before typing. That was the direct source of the latest `control_code_root_retry_unavailable` user failures.
- Do not keep a 25 second browser capture timeout; missed capture must return the phone toward raw quickly.
- Keep the direct popup geometry tied to the measured Pixel display size and do not change stream resolution/aspect ratio without rechecking those coordinates.

## 2026-05-11 15:55-16:01 Europe/Riga

Finding:
- The v174 real request succeeded and the browser capture acknowledgement reached the phone, but cleanup still waited about 3 seconds before closing the generated Aztec screen.
- The delay was caused by `returnControlCodeSurfaceToRawTicket` doing a fresh root hierarchy read before the first close tap. For browser-frame results, the generated screen has already been proven enough to capture and acknowledge, so this pre-close read is avoidable.

Change made:
- Pixel v175 uses a browser-frame result sentinel after submit.
- Browser-frame cleanup now immediately enters the generated-result fast-close path, uses the known small generated-code close geometry, and only does root confirmation after the close tap.
- This keeps the old safe behavior for hierarchy-backed cleanup, but removes the slow pre-close root dump from the normal browser-frame result path.

Validation:
- Targeted Pixel source test passed: `TicketStreamServiceSourceTest.successCleanupStartsCloseBeforeSendingResult`.
- Full Pixel `./gradlew :app:testDebugUnitTest` passed.
- Pixel `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed bridge health reports `ticket-stream-2026-05-11-browser-frame-fast-close-v175`.
- Public bridge remained on `ticket-remote-2026-05-11-native-annexb-capture-before-raw-v40`.
- Public authenticated browser samples after v175:
  - first sample: `page_first_decoded_frame_ms=2050`, `stream_first_decoded_frame_ms=885`, live frame age about 484 ms, canvas `900x1852`.
  - second sample from stopped state: `page_first_decoded_frame_ms=1621`, `stream_first_decoded_frame_ms=831`, live frame age about 323 ms, canvas `900x1852`.
- Safe browser dialog-open test after v175:
  - page first decoded in `1770` ms, stream first decoded in `1088` ms.
  - opening the browser code dialog sent prepare and returned `control_code_prepare=ready`.
  - phone health stayed `controlCodeRequest.status=idle`; no generated-code task was started.
- Pixel idle behavior still works: after closing the browser page, health returned to no clients and hardware capture idle.

Watch next:
- Need a real known-good control-code run on v175 to confirm the browser-capture-ack-to-close gap is now under 2 seconds.
- Continue watching for `return_to_raw_complete` surfacing as a user-visible result; public v40 should prevent cleanup completion from failing an already-capturing request.
- Active guard root hierarchy reads still sometimes time out while the stream is live; currently observed as phone events, not user-visible failures. Do not change this unless it correlates with user-side errors or request blocking.

Do not repeat:
- Do not reintroduce a root hierarchy dump before closing a browser-frame result.
- Do not use the top-right ticket exit close for generated-code cleanup; keep the small generated-code close geometry.
- Do not call v175 solved for control-code cleanup until a real request confirms the generated screen closes quickly after browser capture acknowledgement.

## 2026-05-11 16:06-16:11 Europe/Riga

Finding:
- While v175 was live, the active guard still did root hierarchy reads shortly after a confirmed raw-ticket event.
- Those reads timed out as `root:UNKNOWN_VIVI` even though the stream was live and raw-ticket had just been confirmed.
- The timeouts were not user-visible in the sampled page, but they use the same root path that prepare/submit needs, so they are a credible cause of intermittent "phone is refreshing ticket view" behavior when a user acts at the wrong moment.

Change made:
- Pixel v176 skips the active-guard root probe while the stream is live, hardware is verified, session state is live, and the phone has a recent root-observed raw-ticket proof.
- This keeps root healing for control-code states, but removes redundant root reads during the hot user path.

Validation:
- Added a source test proving active guard checks recent raw-ticket proof before calling `observeRootViviState`.
- Targeted test passed: `TicketStreamServiceSourceTest.foregroundGuardSkipsRedundantRootProbeAfterRecentRawTicketProof`.
- Full Pixel `./gradlew :app:testDebugUnitTest` passed.
- Pixel `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed bridge health reports `ticket-stream-2026-05-11-active-guard-recent-raw-skip-v176`.
- Fresh public page open after v176:
  - stream first decoded in `876` ms.
  - page first decoded in `2109` ms on first post-install wake.
  - live frame age about 223 ms.
  - canvas stayed `900x1852`.
  - recent phone events showed `active_guard_recent_ticket_detail` instead of the previous `active_guard_failed root:UNKNOWN_VIVI` timeout.

Watch next:
- Repeat page open after normal idle; expected page-first decode should return under 2 seconds now that recent raw-ticket memory is warm.
- Run/observe a real control-code request on v176; the important target is browser capture acknowledgement to small-close/raw return under 2 seconds.
- Watch whether active-guard skip event spam hides useful recent events; reduce logging if it becomes noisy.

Do not repeat:
- Do not let active guard contend with prepare/submit immediately after a trusted raw-ticket proof.
- Do not disable root return-to-raw for known popup/result states; this change only skips redundant raw-ticket re-proof.

## 2026-05-11 16:11-16:16 Europe/Riga

Finding:
- v176 fixed the active-guard root contention, but the phone health recent-events list became noisy: `active_guard_recent_ticket_detail` appeared every few seconds and pushed useful events out of the short health history.

Change made:
- Pixel v177 rate-limits the active-guard recent-raw skip event to once every 30 seconds.
- The guard still skips redundant root reads immediately after trusted raw-ticket proof; it just does not flood health events.

Validation:
- Updated the source test to require rate-limited skip logging.
- Targeted test passed: `TicketStreamServiceSourceTest.foregroundGuardSkipsRedundantRootProbeAfterRecentRawTicketProof`.
- Full Pixel `./gradlew :app:testDebugUnitTest` passed.
- Pixel `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed bridge health reports `ticket-stream-2026-05-11-active-guard-quiet-skip-v177`.
- First post-install page sample:
  - stream first decoded in `1194` ms.
  - page first decoded in `2144` ms.
  - live frame age about 57 ms.
  - active-guard skip logs appeared at 30-second spacing.
- Normal idle reopen after page close:
  - page first decoded in `1825` ms.
  - stream first decoded in `1342` ms.
  - live frame age about 50 ms.
  - wake-to-ticket-ready was `238` ms using recent raw-ticket proof.
  - no `active_guard_failed root:UNKNOWN_VIVI` event appeared.

Watch next:
- Continue minute checks for public health, Pixel idle/raw state, recent user-visible errors, and stream freshness.
- Still need a real known-good v177 control-code request to prove generated-Aztec browser freeze plus small-close/raw-return is under 2 seconds.

Do not repeat:
- Do not spam health recent-events from a successful skip path; those events are needed for diagnosing real user failures.

## 2026-05-11 16:16-16:20 Europe/Riga

Finding:
- v177 still allowed active-guard root timeout checks after the raw-ticket proof aged past 60 seconds.
- This is still bad for normal use because a viewer can keep the page open longer than a minute, and the guard can then contend with prepare/submit again.

Change made:
- Pixel v178 bases the live active-guard skip on the current trusted Pixel state, not a 60-second age window.
- If current Pixel memory says `TICKET_DETAIL`, stream is live, and hardware capture is verified, active guard does not run a redundant root hierarchy dump.
- If current Pixel memory is popup/result/other known state, the skip does not apply and the existing root-only return-to-raw logic can run.

Validation:
- Targeted source test passed.
- Full Pixel `./gradlew :app:testDebugUnitTest` passed.
- Pixel `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed bridge health reports `ticket-stream-2026-05-11-active-guard-live-memory-skip-v178`.
- Kept the authenticated public page open for 75 seconds, past the old 60-second failure point:
  - page first decoded in `1890` ms.
  - stream first decoded in `1397` ms.
  - live frame age about 306 ms.
  - canvas stayed `900x1852`.
  - no `active_guard_failed` or root hierarchy timeout appeared after 60 seconds.
  - guard skip logs remained rate-limited at about 30-second spacing.

Watch next:
- Encoder stop after the long-view page was confirmed: Pixel health returned to `streamActive=false`, `clients=0`, `captureMode=idle`.
- Still need a real known-good control-code request on v178 to prove generated-Aztec browser freeze plus return-to-raw is under 2 seconds.

Do not repeat:
- Do not use a short wall-clock freshness cutoff for the raw-ticket active-guard skip; it reintroduces root contention during ordinary long viewing.

## 2026-05-11 16:21-16:27 Europe/Riga

Finding:
- Safe browser dialog-open on v178 still took the phone prepare path through a full root readiness check when the current raw-ticket proof was older than 15 seconds.
- The observed prepare took about 3.3 seconds, even though the stream was live and Pixel memory already said `TICKET_DETAIL`.

Change made:
- Pixel v179 makes dialog prepare trust the current Pixel `TICKET_DETAIL` state while the stream is live and hardware capture is verified.
- Prepare also refreshes the ticket-detail memory timestamp so a submit immediately after dialog-open can use the fast geometry path.

Validation:
- Added/updated the source test to prevent prepare from expiring current raw-ticket state through the old 15-second window.
- Targeted test passed: `TicketStreamServiceSourceTest.dialogPrepareDoesNotBlockFastSubmitBehindFullWakeRecovery`.
- Full Pixel `./gradlew :app:testDebugUnitTest` passed.
- Pixel `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed bridge health reports `ticket-stream-2026-05-11-current-state-prepare-v179`.
- Safe prepare-only public browser test:
  - waited past the old 15-second freshness window before opening the dialog.
  - browser logged `control_code_prepare=ready`.
  - phone event was `control_code_prepare_recent_ticket_detail ... age_ms=34831`, followed by `TICKET_DETAIL:recent_ticket_detail:success=true`.
  - `wake.lastReason` remained the page stream start, proving prepare did not start the full wake/root-read path.
  - `controlCodeRequest.status` stayed `idle`; no generated-code task was started.
- Real iPhone Safari viewer shortly after v179:
  - page first decoded in `1736` ms.
  - stream first decoded in `1189` ms.
  - stream first packet in `1176` ms.
  - no user-side error logged during startup.
- Later authenticated browser startup sample:
  - page first decoded in `1466` ms.
  - stream first decoded in `1259` ms.
  - live frame age about 24 ms.
  - Pixel returned to `streamActive=false`, `clients=0`, `captureMode=idle` after closing the page.

Watch next:
- Encoder stop after the prepare-only page was confirmed: Pixel health returned to `streamActive=false`, `clients=0`, `captureMode=idle`.
- Still need a real known-good control-code request on v179 to prove submit-to-popup and generated-Aztec return-to-raw timings.

Do not repeat:
- Do not make dialog prepare expire trusted current raw-ticket state through the short submit-immediate freshness window.

## 2026-05-11 16:37-16:42 Europe/Riga

Live user proof:
- A real iPhone Safari session opened the public page on v40 while the Pixel was running v179.
- Startup timing from client logs:
  - stream first packet in `997` ms.
  - stream first decoded frame in `1036` ms.
  - page first decoded frame in `1696` ms.
- Two real control-code requests succeeded:
  - `cd1f86806860e2e4c0e49430937e5568`
  - `7682649b6655fb9471e5c264db16994b`
- Browser logs showed `control_code_browser_capture_canvas_frozen` and `control_code_browser_capture_confirmed` for both requests.
- Phone logs showed both requests returned to raw ticket after browser capture:
  - first request cleanup completed with fresh frame in about `591` ms after cleanup started.
  - second request cleanup completed with fresh frame in about `833` ms after cleanup started.
- Pixel health after the browser closed returned to:
  - `streamActive=false`
  - `clients=0`
  - `captureMode=idle`

Current remaining measured gap:
- Submit-to-generated-frame-ready is still about `2594-2712` ms.
- The largest visible contributor is digit entry at about `827-828` ms plus the fixed post-submit generated-frame wait.
- This is much better than the earlier stuck/failure state, but still above the strict under-2-second internal target for moving past the popup.

Do not repeat:
- Do not reintroduce phone PNG capture or browser image upload; the browser-local freeze path worked live.
- Do not root-poll for generated result after submit; the current frame-watermark path worked and avoided the old long hangs.
- Do not treat `return_to_raw_complete` as a failed user result; it is a successful raw-ticket state event after capture.

## 2026-05-11 16:42-16:45 Europe/Riga

Finding:
- The stream and return-to-raw path are currently healthy, but submit-to-generated-frame-ready is still above the internal 2-second target.
- The measured request path is dominated by small fixed waits plus root text entry:
  - popup geometry settle was `180` ms.
  - input focus settle was `120` ms.
  - typed-to-submit settle reused the full `120` ms poll interval.
  - post-submit frame wait was `900` ms.
  - text entry deleted 9 characters even though the control-code field is bounded to 6 digits.

Change made:
- Pixel v180 keeps the same root-only and browser-frame result design, but trims only the fixed waits around the proven sequence:
  - popup geometry settle `180` -> `120` ms.
  - input focus settle `120` -> `80` ms.
  - typed-to-submit settle now has its own `40` ms wait instead of a full poll interval.
  - post-submit generated-frame wait `900` -> `650` ms.
  - stale-field cleanup deletes 6 characters instead of 9.

Validation:
- Added source tests so this speed pass is explicit and does not reintroduce root polling, accessibility, phone screenshots, or browser image upload.
- Focused Android source tests passed.
- Full Pixel `./gradlew :app:testDebugUnitTest` passed.
- Pixel `./gradlew :app:assembleDebug` passed.
- Installed Pixel APK and confirmed bridge health reports `ticket-stream-2026-05-11-fast-popup-submit-v180`.
- Authenticated browser startup after install:
  - stream first packet in `1366` ms from logs.
  - stream first decoded frame in `1380` ms from logs.
  - page first decoded frame in `1971` ms from logs.
  - live frame age at browser sample was `18` ms.
  - canvas stayed `900x1852`.
  - Pixel idled after closing: `streamActive=false`, `clients=0`, `captureMode=idle`.

Watch next:
- Need a real control-code request on v180 to verify whether submit-to-generated-frame-ready moved below 2 seconds without freezing the popup too early.
- If v180 ever freezes before the Aztec is visible, revert only the post-submit wait increase; do not change the browser-local capture or return-to-raw design.
- Safe prepare-only check on v180:
  - authenticated page first decoded in `1812` ms.
  - opening the control-code dialog logged `control_code_prepare=ready`.
  - Pixel used current raw-ticket state: `control_code_prepare_recent_ticket_detail ... success=true`.
  - no control-code request was created.
  - closing the dialog and page returned Pixel to idle with `streamActive=false`, `clients=0`, `captureMode=idle`.
- Later idle-to-page sample:
  - authenticated page first decoded in `1763` ms.
  - live frame age was about `189` ms.
  - canvas stayed `900x1852`.
  - Pixel wake-to-ticket-ready was `306` ms using recent raw-ticket proof.
  - closing the page again returned Pixel to idle with encoder stopped.

Do not repeat:
- Do not optimize by root-polling after submit; that path already caused long hangs.
- Do not optimize by keeping the encoder always hot; startup is already under 2 seconds while still idling after viewers leave.

## 2026-05-11 16:45-17:00 Europe/Riga

Watch status:
- Repeated minute checks stayed clean:
  - public `/api/v1/livez` returned OK.
  - Pixel remained on v180.
  - idle state returned to `streamActive=false`, `clients=0`, `captureMode=idle` after synthetic viewer tabs closed.
  - no `phone is refreshing`, `return_to_raw_complete` user-error, media error, decoder error, panic, or runtime error appeared in the public logs.
- Additional signed-in startup sample:
  - stream first packet in `1293` ms.
  - stream first decoded frame in `1303` ms.
  - page first decoded frame in `1528` ms.

Still unproven:
- No real submitted-code request has run yet on v180.
- v180 should remain under close watch until a real request proves the shorter post-submit wait still freezes the generated Aztec, not the popup/raw ticket.

## 2026-05-11 17:00-17:10 Europe/Riga

Watch status:
- Minute health checks continued clean:
  - public `/api/v1/livez` OK.
  - Pixel v180 idle when no viewer is connected.
  - no active control-code request stuck.
  - no public log errors for refreshing-ticket, media/decoder/runtime failures, or `return_to_raw_complete` as a user error.
- Signed-in startup sample:
  - first packet in `1353` ms.
  - first decoded frame in `1360` ms.
  - live frame age about `82` ms.
  - canvas stayed `900x1852`.
  - after closing, Pixel returned to `streamActive=false`, `clients=0`, `captureMode=idle`.

Still unproven:
- Need a real v180 control-code submission to verify the shorter submit path freezes generated Aztec reliably.

## 2026-05-11 17:10-17:22 Europe/Riga

Change deployed during watch:
- Public bridge v41 is live: `ticket-remote-2026-05-11-native-annexb-pixel-event-capture-v41`.
- The browser now starts generated-code freezing from the Pixel `generated_result` event instead of waiting for the later request-status update.
- Safari/iPhone now chooses the AVC decoder adapter immediately.

Post-deploy checks:
- Public `/api/v1/livez` OK with v41.
- Pixel stayed on v180 and returned to idle after viewer tabs closed.
- Signed-in desktop browser sample:
  - first packet in `1486` ms.
  - first decoded stream frame in `1506` ms.
  - page first decoded frame in `1787` ms.
  - live frame age about `21` ms.
  - canvas stayed `900x1852`.
- Real iPhone page load on v41:
  - first packet in `260` ms.
  - first decoded stream frame in `288` ms.
  - page first decoded frame in `786` ms.
- No decoder recovery/error, media error, refreshing-ticket failure, panic, or runtime error appeared in the checked log window.

Still unproven:
- Need a fresh real submitted-code request on v41 to confirm the Pixel-event capture shortcut reduces generated Aztec hold time and still freezes the generated Aztec, not raw ticket.

## 2026-05-11 17:22-17:27 Europe/Riga

Safe prepare check:
- Opened the authenticated public page and exercised dialog-open only; no code submitted.
- Browser stream stayed live and fresh while the dialog was open.
- Public log reported `control_code_prepare` as `ready`.
- Pixel used recent root-confirmed raw ticket state: `control_code_prepare_recent_ticket_detail`.
- After closing the dialog and tab, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.

Validation:
- `node --check internal/web/static/app.js` passed.
- Focused ticket remote tests passed:
  - `TestControlCode`
  - `TestPixelTicket`
  - `TestTicketViewerKeepsSafariOnCodeRequestPath`

Still unproven:
- Need a fresh real submitted-code request on v41 for generated Aztec capture timing.

## 2026-05-11 17:27-17:31 Europe/Riga

Small hardening change:
- Added a public translation for the internal `return_to_raw_complete` cleanup reason so it cannot leak as raw internal text if an unexpected UI path displays it.
- Test first failed as expected, then passed after the change.
- Full `go test ./...` in `workloads/ticket-remote` passed.

Deploy:
- Public bridge v42 is live: `ticket-remote-2026-05-11-native-annexb-pixel-event-capture-v42`.
- Versioned public asset includes:
  - Pixel-event capture shortcut.
  - Safari/iPhone AVC decoder preference.
  - `return_to_raw_complete` public translation.

Post-deploy proof:
- Public `/api/v1/livez` OK with v42.
- Signed-in desktop browser sample:
  - first packet in `1316` ms.
  - first decoded stream frame in `1328` ms.
  - page first decoded frame in `1574` ms.
  - live frame age about `6` ms.
  - canvas stayed `900x1852`.
- After closing the tab, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- No decoder/runtime/refreshing-ticket errors appeared in the checked logs.

Still unproven:
- Need a fresh real submitted-code request on v42 for generated Aztec capture timing and Safari capture behavior.

## 2026-05-11 17:31-17:40 Europe/Riga

Live anomaly:
- A real iPhone page boot reached v42, but only `spacetime_client_loaded` appeared; no first-frame client logs arrived before the session left.
- Phone health showed hardware stream active with fresh frames during that session.
- Server logs showed SpacetimeDB state/presence calls timing out around that page load.

Root cause found:
- Video sockets were accepted and frames could be written, but the server waited for the Spacetime presence heartbeat before entering the socket read loop.
- If Spacetime stalled, browser telemetry and some browser coordination messages could be delayed or lost even while video frames were flowing.

Change made:
- Presence heartbeat and disconnect are now bounded background updates.
- Browser video/control sockets send initial state and enter their read loops immediately.
- Slow Spacetime presence updates still refresh shared state when they succeed, but no longer block stream telemetry or socket interaction.

Validation:
- Added a regression test proving video client logs are handled while the presence heartbeat is blocked.
- The new test failed before the fix and passed after the fix.
- Focused stream tests passed.
- Full `go test ./...` in `workloads/ticket-remote` passed.

Deploy:
- Public bridge v43 is live: `ticket-remote-2026-05-11-native-annexb-pixel-event-capture-v43`.

Post-deploy proof:
- Public `/api/v1/livez` OK with v43.
- Signed-in desktop browser sample:
  - config received in `79` ms.
  - first packet in `1409` ms.
  - first decoded stream frame in `1423` ms.
  - page first decoded frame in `1558` ms.
  - canvas stayed `900x1852`.
- First-frame telemetry arrived normally after the socket-read fix.
- After closing the tab, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.

Still unproven:
- Need a fresh real submitted-code request on v43 for generated Aztec capture timing and Safari capture behavior.

## 2026-05-11 18:22 Europe/Riga

Retention check:
- Focused server tests passed:
  - `go test ./internal/web -run 'TestControlCodeSucceeded(ViewExpiresAfterSixtySeconds|RequestExpiresStoredImageWithoutBrowserView)'`
- The tested behavior keeps a successful control-code browser view available for 60 seconds, then expires it and clears private result data.
- This supports the active goal requirement that the requesting browser can keep the captured generated-code view briefly without storing or uploading image bytes.

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- Idle-cool state confirmed:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=1927`, `keyFrames=1927`.
- Last control-code request remains `succeeded`.
- No recent stream, decoder, stale-video, refreshing-ticket, presence-failure, or user-facing error logs in the checked 3-minute window.

Authenticated startup sample:
- Browser reported:
  - first packet `1710.5` ms.
  - first decoded frame `1780.5` ms.
  - current live frame age about `101` ms.
  - canvas `900x1852`.
  - `keyFrameOnlyLatestVideo=true`.
- Server logs for the same sample:
  - stream first packet `1507` ms.
  - stream first decoded frame `1577` ms.
  - page first decoded frame `1781` ms.
- After the tab closed, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2030`, `keyFrames=2030`.
- Wake for this sample was fast:
  - ticket-ready `270` ms.
- No recent stream or user-facing error logs around the sample.

## 2026-05-11 18:25-18:28 Europe/Riga

Cleanup-reason hardening:
- Added a regression test for the edge case where a raw-ticket cleanup marker arrives while the server still considers a control-code request running.
- Confirmed the new test failed before the patch because the browser could receive `return_to_raw_complete` as the failed request reason.
- Patched the server to convert that internal cleanup marker into `control_code_not_generated` for that abnormal failed path.
- Verified:
  - targeted cleanup/capture tests passed.
  - full `go test ./...` in `workloads/ticket-remote` passed.
- Deployed public bridge release `ticket-remote-2026-05-11-latest-keyframe-stream-cleanup-reason-v47`.
- Live checks after deploy:
  - public `/api/v1/livez` OK with v47.
  - Pixel still v182.
  - Pixel idle-cool state confirmed: `streamActive=false`, `clients=0`, `captureMode=idle`.
  - all-key stream counts stayed matched: `encodedFrames=2121`, `keyFrames=2121`.
  - latest wake sample stayed fast: ticket-ready `320` ms.
  - no recent stream/control-code/error logs in the checked deploy window.

## 2026-05-11 18:28-18:34 Europe/Riga

Stale-video recovery hardening:
- v47 cold sample had a fast first frame, but then the browser logged stale frames and restarted the video socket:
  - first packet `1505` ms.
  - first decoded stream frame `1520` ms.
  - page first decoded frame `1653` ms.
  - later stale-video restart caused a new first-packet wait of about `5080` ms.
- Root cause: browser recovery was reconnecting the video socket after a short ~2.2s stale period even while the phone/server stream was active. That made a small stall much worse.
- Changed the browser stale recovery to keep using keyframe/server recovery quickly, but defer video-socket reconnect until `6500` ms.
- Verified before deploy:
  - `node --check internal/web/static/app.js` passed.
  - focused viewer/control-code tests passed.
  - full `go test ./...` in `workloads/ticket-remote` passed.
- Deployed public bridge release `ticket-remote-2026-05-11-latest-keyframe-stream-stale-reconnect-v48`.
- v48 authenticated browser sample:
  - first packet `1393` ms in server log / `1613.7` ms in page debug.
  - first decoded stream frame `1409` ms in server log / `1629.2` ms in page debug.
  - page first decoded frame `1629` ms.
  - live decoded frame age about `434` ms.
  - canvas remained `900x1852`.
  - no stale-video, video-restart, decoder, runtime, or media errors in the checked window.
- After closing the tab, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2757`, `keyFrames=2757`.
- Attempted to create a one-minute thread heartbeat again for the 24-hour watch; the scheduler returned `dynamic tool request failed`, so continue explicit foreground checks and do not assume a background automation exists.

## 2026-05-11 18:35-18:36 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v48.
- A browser-use viewer was still connected at the first health read:
  - Pixel stream was live.
  - first packet `1305` ms.
  - first decoded stream frame `1320` ms.
  - page first decoded frame `1547` ms.
  - no stale-video restart or error logs in the checked window.
- After closing/waiting, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2845`, `keyFrames=2845`.
- Last wake was fast:
  - ticket-ready `219` ms.
- No recent control-code, decoder, runtime, media, stale-video, or user-facing error logs in the final 30-second check.

## 2026-05-11 18:37 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v48.
- Pixel health OK with v182.
- Idle-cool state confirmed:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2845`, `keyFrames=2845`.
- Last control-code request remains `succeeded`.
- Last wake remains fast:
  - ticket-ready `219` ms.
- No recent stream, decoder, runtime, media, stale-video, refreshing-ticket, `return_to_raw_complete`, or user-facing error logs in the checked 90-second window.

Repeat browser startup sample:
- Authenticated page stayed on v48.
- Browser reported:
  - first packet `1702.9` ms.
  - first decoded frame `1717.9` ms.
  - live decoded frame age about `157` ms.
  - canvas `900x1852`.
- Server logs for the same sample:
  - stream first packet `1460` ms.
  - stream first decoded frame `1475` ms.
  - page first decoded frame `1718` ms.
- Pixel wake for this sample:
  - ticket-ready `247` ms.
- No stale-video, video-restart, decoder, runtime, or media errors in the checked window.
- After closing, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2981`, `keyFrames=2981`.

## 2026-05-11 18:40 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v48.
- Pixel health OK with v182.
- Idle-cool state confirmed:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2981`, `keyFrames=2981`.
- Last control-code request remains `succeeded`.
- Last wake remains fast:
  - ticket-ready `247` ms.
- No recent stream, decoder, runtime, media, stale-video, refreshing-ticket, `return_to_raw_complete`, or user-facing error logs in the checked 90-second window.

## 2026-05-11 18:41 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v48.
- Pixel health OK with v182.
- Idle-cool state confirmed:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2981`, `keyFrames=2981`.
- Last control-code request remains `succeeded`.
- No recent stream, decoder, runtime, media, stale-video, refreshing-ticket, `return_to_raw_complete`, or user-facing error logs in the checked 90-second window.

## 2026-05-11 18:42 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v48.
- Pixel health OK with v182.
- Idle-cool state confirmed:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2981`, `keyFrames=2981`.
- Last control-code request remains `succeeded`.
- No recent stream, decoder, runtime, media, stale-video, refreshing-ticket, `return_to_raw_complete`, or user-facing error logs in the checked 90-second window.

## 2026-05-11 18:44 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v48.
- Pixel health OK with v182.
- Idle-cool state confirmed:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=2981`, `keyFrames=2981`.
- Last control-code request remains `succeeded`.
- No recent stream, decoder, runtime, media, stale-video, refreshing-ticket, `return_to_raw_complete`, or user-facing error logs in the checked 90-second window.

Authenticated startup sample:
- Browser reported:
  - first packet `1604.6` ms.
  - first decoded frame `1616` ms.
  - live decoded frame age about `157` ms.
  - canvas `900x1852`.
- Server logs for the same sample:
  - stream first packet `1379` ms.
  - stream first decoded frame `1390` ms.
  - page first decoded frame `1616` ms.
- Pixel wake for this sample:
  - ticket-ready `3330` ms; still under the 5s wake target, but slower than prior samples.
- No stale-video, video-restart, decoder, runtime, media, or direct-video errors in the checked window.
- After closing, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=3130`, `keyFrames=3130`.

## 2026-05-11 18:45-18:52 Europe/Riga

Slow startup root cause and fix:
- A headless browser startup sample violated the under-2s target:
  - stream first packet `3630` ms.
  - stream first decoded frame `3647` ms.
  - page first decoded frame `4037` ms.
  - log also showed `backend_inactive`.
- Root cause from logs:
  - two immediate HTTP phone prewarm starts fired for the same page start.
  - both timed out at the 1.5s prewarm timeout.
  - this delayed the first fresh frame until the relay recovered.
- Added regression tests for:
  - no duplicate HTTP phone start while an immediate prewarm start is already in flight.
  - prewarm HTTP start not being cancelled before a realistic slow Pixel wake can finish.
- Patched public bridge prewarm:
  - single in-flight HTTP start per short window.
  - async start timeout raised to `5s` so a valid wake is not cancelled early.
- Verified before deploy:
  - targeted prewarm tests passed.
  - full `go test ./...` in `workloads/ticket-remote` passed.
- Deployed public bridge release `ticket-remote-2026-05-11-latest-keyframe-stream-prewarm-dedupe-v49`.
- v49 live verification:
  - public `/api/v1/livez` OK with v49.
  - browser first packet `1559.5` ms.
  - browser first decoded frame `1574.1` ms.
  - server stream first packet `1330` ms.
  - server stream first decoded frame `1344` ms.
  - page first decoded frame `1574` ms.
  - live frame age about `22` ms.
  - Pixel wake ticket-ready `296` ms.
  - no immediate prewarm timeout, stale-video, video-restart, decoder, runtime, media, or direct-video errors in the checked window.
- After closing, Pixel returned to idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=3503`, `keyFrames=3503`.

Real iPhone control-code activity:
- A real iPhone viewer loaded v49:
  - stream first packet `975` ms.
  - stream first decoded frame `1005` ms.
  - page first decoded frame `1640` ms.
- First observed v49 request `a0489b872abc852f8b2f96ff92139b1f`:
  - request succeeded.
  - Pixel total duration reported `7459` ms.
  - browser saw Pixel generated-result event at frame `28`.
  - browser froze frame `46`.
  - browser confirmed capture.
  - phone returned through `control_exit_popup_closed`.
  - This was successful but slower than target; continue watching before changing phone sequencing because the immediate next request was much faster.
- Second observed v49 request `44b3c219eb7fe9e9627c0d65b7d17358`:
  - request succeeded.
  - Pixel total duration reported `2855` ms.
  - browser saw Pixel generated-result event at frame `180`.
  - browser froze the same frame `180`.
  - browser confirmed capture.
  - current Pixel stream fresh: last frame age about `284` ms.
- No user-facing failure, refreshing-ticket error, screenshot error, or `return_to_raw_complete` error appeared for these requests.

## 2026-05-11 18:56 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v49.
- Pixel health OK with v182.
- Pixel returned to idle after the real iPhone activity:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - `encodedFrames=4013`, `keyFrames=4013`.
- Last control-code request remains `succeeded` with total duration `2855` ms.
- Additional headless startup sample after v49:
  - stream first packet `306` ms.
  - stream first decoded frame `314` ms.
  - page first decoded frame `698` ms.
- No immediate prewarm timeout, stale-video, video-restart, decoder, runtime, media, refreshing-ticket, `return_to_raw_complete`, or user-facing error logs in the checked 90-second window.

## 2026-05-11 17:43-18:06 Europe/Riga

Evidence after real iPhone use:
- Public v44 and Pixel v181 handled several real iPhone control-code requests successfully.
- The phone did not stay stuck:
  - generated-result events were followed by browser capture confirmation.
  - cleanup returned to raw ticket.
  - a later request was able to start.
- The fast path was working:
  - latest observed phone wake after clean idle was `266` ms in one sample and `262` ms in a later sample.
  - submit-to-generated-result on the phone was about `2.1` s in the checked health events.
- The remaining 6-second visual delay pattern was isolated:
  - slow captures froze about 48 frames after the Pixel-generated-result watermark.
  - at 8 fps, 48 frames is about 6 seconds.
  - this points to browser decoder/backlog delay, not root actions or phone-side app state detection.

Change made:
- Pixel v182 changes the hardware stream profile to `hardware_h264_all_key_latest_low_latency`.
- Every encoded hardware frame is now a keyframe by setting the Pixel keyframe interval below the frame interval.
- Public v45 changes the browser to latest-keyframe mode.
- Public v46 extends latest-frame decoder reset to all WebCodecs decoder modes, not just Safari/AVC mode.

Validation:
- `go test ./...` in `workloads/ticket-remote` passed after v46.
- `node --check internal/web/static/app.js` passed.
- `./gradlew :app:testDebugUnitTest` passed.
- `./gradlew :app:assembleDebug` passed.
- Public `/api/v1/livez` reports `ticket-remote-2026-05-11-latest-keyframe-stream-v46`.
- Real Pixel health reports `ticket-stream-2026-05-11-all-key-latest-v182`.
- Pixel health confirmed all-key output:
  - `encodedFrames` equaled `keyFrames`.
  - active stream mode stayed `root_hardware_h264`.
  - quality profile was `hardware_h264_all_key_latest_low_latency`.
- Signed-in browser proof on v46:
  - first packet about `1.16` to `1.37` s.
  - first decoded frame about `1.18` to `1.38` s.
  - canvas remained `900x1852`.
  - frame age stayed fresh after startup.
- After closing the proof tab:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.

Do not repeat:
- Do not go back to delta-frame streaming for the control-code result path; the observed bad delay was exactly consistent with a delta/decode backlog.
- Do not limit latest-frame decoder resets to Safari only; Chrome/headless can also build a queue.
- Do not treat the old `return_to_raw_complete` reason as a user-visible error; it is a cleanup success marker.

Still watching:
- Need more real iPhone submitted-code requests on v46/v182 to confirm generated Aztec freeze happens within the 2-second target.
- Watch for iPhone `decoder_error` and `foreground_video_socket_closed` logs after visibility changes; recovery has been fast so far, but user-visible errors should stay at zero.

## 2026-05-11 18:07-18:08 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- No viewers:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- All-key stream remained true for the latest session:
  - `encodedFrames=1702`.
  - `keyFrames=1702`.
- Last wake sample stayed fast:
  - wake-to-ticket-ready `262` ms.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 90-second window.

## 2026-05-11 18:03-18:09 Europe/Riga

Real iPhone v46/v182 control-code proof:
- Public page version was v46.
- Pixel version was v182.
- iPhone first decoded stream frame:
  - stream first decoded `137` ms.
  - page first decoded `432` ms.
- Control-code request `1cccd0a32c6dd210302f82da01737c0d` succeeded.
- Phone-side timing:
  - control popup event at frame `1094`.
  - generated-result event at frame `1110`.
  - phone reported frame ready in `2486` ms total.
- Browser capture timing:
  - Pixel capture watermark was frame `1110`.
  - browser froze frame `1112`.
  - freeze and confirmation landed in the same log second.
  - this is about two frames after the Pixel watermark, not the previous 48-frame / 6-second delay.
- Return-to-raw:
  - browser capture ack was followed by `returning_raw`.
  - close used `geometry_close_control_code_result`.
  - surface was clean in `470` ms.
  - post-cleanup fresh frame verified with `frame_age_ms=53`.
  - raw-ticket event was frame `1121`.
- No phone-not-ready, refreshing-ticket, decoder-error, stale-video, or user-facing failure logs appeared for this request.

Current status:
- The core target is met on this live request:
  - generated Aztec to browser freeze was under 2 seconds.
  - generated screen did not linger for 10-20 seconds.
  - cleanup returned to raw and verified a fresh stream frame.

## 2026-05-11 18:09-18:10 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- Phone idle after real request:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- All-key count still matched:
  - `encodedFrames=1702`.
  - `keyFrames=1702`.
- Last control-code request remained succeeded.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 90-second window.

## 2026-05-11 18:11-18:12 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- Phone remains idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- Last stream session still proves all-key output:
  - `encodedFrames=1702`.
  - `keyFrames=1702`.
- Last control-code request still succeeded.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 90-second window.

## 2026-05-11 18:13 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- Phone remains idle:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- Last stream session still proves all-key output:
  - `encodedFrames=1702`.
  - `keyFrames=1702`.
- Last control-code request still succeeded.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 3-minute window.

## 2026-05-11 18:13-18:14 Europe/Riga

Cold-start browser proof:
- Opened the authenticated public page from idle using the shared Chrome profile.
- Public page version was v46.
- Stream metrics:
  - first packet `1306` ms in server logs / `1624` ms in page debug.
  - first decoded stream frame `1316` ms.
  - page first decoded frame `1634` ms.
  - live frame age was about `30` ms when checked.
- Canvas remained `900x1852`, preserving the ticket aspect ratio.
- Browser was in latest-keyframe mode:
  - `keyFrameOnlyLatestVideo=true`.
- Pixel health during stream:
  - wake-to-ticket-ready `346` ms.
  - stream mode `root_hardware_h264`.
  - quality profile `hardware_h264_all_key_latest_low_latency`.
  - `encodedFrames=1785`, `keyFrames=1785`.
- After closing the proof tab:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - all-key count remained matched at `encodedFrames=1839`, `keyFrames=1839`.
- No stale-stream, decoder-error, or video restart logs appeared for this proof.

## 2026-05-11 18:15 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- A HeadlessChrome viewer was active, likely from the shared browser-use proof surface.
- Stream timing for that viewer stayed within target:
  - first packet `1303` ms.
  - first decoded stream frame `1316` ms.
  - page first decoded frame `1543` ms.
- Pixel stream was live and fresh:
  - `lastFrameSentAgoMillis=45`.
  - `encodedFrames=1904`, `keyFrames=1904`.
- Wake for this sample was slower at `3246` ms, but still under 5 seconds and the browser frame target stayed under 2 seconds.
- Closed the browser-use tab/session surface and rechecked Pixel:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
  - all-key count stayed matched at `encodedFrames=1927`, `keyFrames=1927`.
- No stale-stream, decoder-error, refreshing-ticket, or user-facing failure logs in the checked window.

## 2026-05-11 18:17 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- No viewers:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- All-key output remained matched from the last stream:
  - `encodedFrames=1927`.
  - `keyFrames=1927`.
- Last control-code request still succeeded.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 90-second window.

## 2026-05-11 18:19 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- No viewers:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- All-key output still matched:
  - `encodedFrames=1927`.
  - `keyFrames=1927`.
- Last control-code request still succeeded.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 90-second window.
- Thread heartbeat automation creation failed twice with `dynamic tool request failed`; continue explicit foreground checks instead of assuming a scheduler is active.

## 2026-05-11 18:20 Europe/Riga

Minute watch:
- Public `/api/v1/livez` OK with v46.
- Pixel health OK with v182.
- No viewers:
  - `streamActive=false`.
  - `clients=0`.
  - `captureMode=idle`.
- All-key output still matched:
  - `encodedFrames=1927`.
  - `keyFrames=1927`.
- Last control-code request still succeeded.
- No recent browser error, stale-stream, decoder-error, refreshing-ticket, or presence-failure logs in the checked 90-second window.

Additional startup sample:
- Authenticated desktop page after idle:
  - config received in `4` ms.
  - first packet in `1580` ms.
  - first decoded stream frame in `1593` ms.
  - page first decoded frame in `1874` ms.
  - live frame age about `26` ms.
  - canvas stayed `900x1852`.
- After closing, Pixel returned to idle with encoder stopped.

## 2026-05-11 17:40-17:43 Europe/Riga

Watch status:
- Repeated minute checks stayed clean:
  - public `/api/v1/livez` OK with v43.
  - Pixel returned to idle and stayed idle with no viewers.
  - `streamActive=false`, `clients=0`, `captureMode=idle`.
  - no presence timeout logs after the v43 browser proof.
  - no decoder/runtime/refreshing-ticket/user-error logs in the checked windows.

Still unproven:
- Need a fresh real submitted-code request on v43 for generated Aztec capture timing and Safari capture behavior.
