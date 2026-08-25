# Current Ticket

This is the first file to read for Ticket work.

Live product: the signed-in page at `ticket.jolkins.id.lv`.
The phone shows ViVi. The page shows that picture and exposes one durable visual action engine for opening, registering, switching between the newest unused and recently activated tickets, re-detecting the newest ticket, and requesting a control code.

## Current jobs, in this order

1. Open the signed-in page and get a live ticket picture quickly.
2. Use **Atvērt jaunāko nereģistrēto biļeti** to visually select and prove the newest current-or-upcoming unused ticket.
3. Use **Atvērt jaunāko biļeti un reģistrēt** for the same selection followed by one bounded held phone drag, or use **Reģistrēt atvērto biļeti** after a fresh slider proof.
4. The browser slider is a transparent local authorization control placed directly over ViVi's visible slider. It submits that same register action once at completion and does not maintain its own phone-control protocol.
5. For 15 minutes after a proven registration, and only after a newer unused ticket is visually proven, use the context-aware button to move directly between the two Aztec-detail views. The ticket list is a transitional phone view, never a successful resting state.
6. Request a control code when needed. The requester page freezes its own live picture of the result. The phone must not send a screenshot of that result.

Registration policy is per authenticated account: at most one admitted registration every 30 seconds and ten admitted registrations in a rolling hour. Control codes remain limited to two requests in a rolling minute. Admins and owners obey these limits by default and may persist an unlimited testing preference to their account; bypassed actions remain audited without consuming quota.

Prove the result on the signed-in page. If a phone picture is needed, take it through the root path, then pull it.

## Where to work

- Page: `web-client/ticket-app-source.js` and `internal/web/static/index.html.tmpl`. Rebuild the page bundle after page edits.
- Phone Ticket: `pixel-phone` Ticket files under the Android orchestrator. Do not split that service unless asked.
- Durable state: `spacetimedb/src/lib.rs`.
- Operator start/stop/health: `../../docs/runbooks/MODULE_TICKET_REMOTE.md`.
- Deep stream/capture note: `pixel-phone/docs/architecture/TICKET_STREAMING_ARCHITECTURE.md`. Open it only when the stream path itself is the task.

Generated copies such as `internal/web/static/app.js` and `internal/web/static/spacetime-client.js` are build output. Edit the source, then rebuild.

## Do not start here

Leftover Ticket history now lives in:

- `../../archive/ticket/`
- `../../docs/archive/ticket/`
- `pixel-phone/archive/ticket/`

Those folders are backups. Open them only when a task explicitly asks for old Ticket history.

## Short rules

- The browser writes immediate Ticket actions directly to authenticated Spacetime reducers; the Pixel subscribes to the durable command rows. The browser never talks to the phone directly.
- Spacetime decides whether and when: it owns account quotas, the admin preference, switch availability/expiry, delayed refresh/re-detection schedules, and the sanitized usage projection. Pixel executes one admitted command and proves the visual result. Phone-local clocks and restart jobs must not create product actions.
- `ticketremote_ticket_action_v3` is the browser authority for visual proof and terminal state. The older interaction row is compatibility state only: a newer V3 visual proof fences stale `needs_attention`, and a V3 registration failure keeps the exact command revision instead of creating a replayable retry revision.
- The browser requests non-mutating `prove_current` after the first fresh frame of a stream epoch, after two agreeing significant picture changes, and when a visible page resumes with a stale proof. Requests are coalesced per backend and epoch, and an unknown result waits for another meaningful picture change. A five-minute normalized slider region is usable only when its action ID, epoch, and frame sequence exactly match the current proof.
- The signed-in browser client exposes only the V3 Ticket action producer. Legacy reducers and table shape remain temporarily in the module for old in-flight rows and sidecar compatibility; remove that schema only after its production TTL and migration checks pass.
- Keep the existing signed-in browser session. Do not clear cookies or log out.
- Updating the phone app does not update the public page, and the reverse is also true.
- Do not treat a healthy phone, a successful deploy, or a passing test as enough. The signed-in page has to show the expected ticket state.
- Chrome is the default browser path. If it is unavailable, say so and use the existing signed-in session.
- Old claim-dialog and private-control wording must not return.
- Leave leftover Ticket logging retired. New Ticket diagnostics go to the shared operational log, not local files.
