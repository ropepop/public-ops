# Current Ticket

This is the first file to read for Ticket work.

Live product: the signed-in page at `ticket.jolkins.id.lv`.
The phone shows ViVi. The page shows that picture, lets a linked person swipe to register, open a fresh unused ticket, or request a control code.

## Current jobs, in this order

1. Open the signed-in page and get a live ticket picture quickly.
2. Show the small orange oval only after that live picture is there. No picture, no oval.
3. Swipe the oval to register the ticket. While the swipe is happening, briefly speed up the pictures, then return to the quiet view.
4. Use **Atvērt jaunu nereģistrētu biļeti** to stay inside ViVi and open the latest unused ticket. Do not force-close ViVi for the ordinary button. Hide the oval until the new unused ticket picture is visible.
5. Request a control code when needed. The requester page freezes its own live picture of the result. The phone must not send a screenshot of that result.

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

- The browser never talks to the phone directly.
- Keep the existing signed-in browser session. Do not clear cookies or log out.
- Updating the phone app does not update the public page, and the reverse is also true.
- Do not treat a healthy phone, a successful deploy, or a passing test as enough. The signed-in page has to show the expected ticket state.
- Chrome is the default browser path. If it is unavailable, say so and use the existing signed-in session.
- Old claim-dialog and private-control wording must not return.
- Leave leftover Ticket logging retired. New Ticket diagnostics go to the shared operational log, not local files.
