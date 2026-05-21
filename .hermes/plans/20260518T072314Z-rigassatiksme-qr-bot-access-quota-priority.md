# Riga Satiksme QR bot access/quota/priority plan

## Done criteria
- Telegram bot accepts exactly one 5-digit code, queues through phone-broker, and returns only the QR photo with no code caption.
- `/start`, `/help`, `/status`, `/cancel`, `/access`, and admin controls are available.
- Access is explicit when configured: admins can allow/deny users, configure per-user quota, create quota groups, assign users to quota groups, and allow/deny Telegram chats with daily chat quotas.
- Daily usage is counted by UTC date and enforced for user, quota group, and Telegram chat scopes before a broker job is created.
- Access/quota state persists via local JSON and has a SpacetimeDB-backed store/schema/reducer wiring for production.
- Phone broker exposes and uses desired owner/priority state: ticket viewers/sockets are highest priority, QR work waits/retries, and QR runs only when ticket priority is idle.
- Ticket remote keeps publishing presence to the broker so desired priority state matches actual ticket viewers.
- Tests cover access denied, user/group/chat quotas, admin commands, broker priority snapshot/routing, and existing QR secrecy behavior.
- Verification runs targeted bot and broker Go tests, plus relevant config/build checks.

## Implementation outline
1. Add bot access state model and manager with file persistence plus optional SpacetimeDB client.
2. Extend bot message metadata for Telegram chat type and usernames.
3. Add user/admin/group/chat command handling and quota checks before job creation.
4. Add phone-broker desired owner/priority fields and selection tests.
5. Add SpacetimeDB module files for QR bot access state reducers.
6. Wire env/config/compose/module docs for access state, SpacetimeDB backend, and admin IDs.
7. Run gofmt and tests, fix regressions.
