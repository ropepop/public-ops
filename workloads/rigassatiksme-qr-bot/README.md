# Riga Satiksme QR Telegram bot

Telegram bot that accepts one 5-digit Riga Satiksme ticket code, queues QR generation through `phone-broker`, and sends back only the QR photo when ready.

## User commands

- `/start` or `/help` — usage text.
- `/status` — latest QR request state.
- `/cancel` — cancel the latest QR request.
- `/access` — show current user's access/quota state.
- `/qr <five digits>` — request QR generation in groups where Telegram bot privacy mode only delivers commands.
- `<five digits>` — request QR generation in private chats, or in groups only if BotFather privacy is disabled / the bot receives the message. The bot never echoes the submitted code and sends QR photos with an empty caption.

## Admin commands

Admin users are bootstrapped with `RIGASATIKSME_QR_ADMIN_USER_IDS`. The default regular-user quota is `RIGASATIKSME_QR_DEFAULT_USER_DAILY_LIMIT` (defaults to `20`) and can be changed at runtime with `/admin set_default_limit`.

Daily quota counts only successful QR jobs. Queued/running jobs reserve capacity while pending, but failed, canceled, rejected, or not-registered outcomes release the reservation and do not reduce the user's daily quota.

- `/admin` — list admin commands.
- `/admin add_user <telegram_user_id|@username> [daily_limit=<default>] [group]` (alias: `/admin add`) — allow a regular user by Telegram numeric user ID, or by `@username`. Username adds first try to resolve the numeric ID immediately via Telegram `getChat(@username)` and any native `text_mention` IDs in the admin message; if Telegram does not expose the ID, the grant is queued and later activates when that user sends `/start` or any message such as `/access` to the bot, or when a native text mention provides the ID. `0` means inherited/unlimited, negative means unlimited.
- `/admin add @user1 @user2 ...` — resolve or queue multiple username grants with the current default tickets/day limit.
- `/admin set_default_limit <daily_limit>` — change the default daily limit used by future `/admin add <telegram_user_id|@username>` calls when no explicit limit is provided.
- `/admin remove_user <telegram_user_id>` (alias: `/admin remove`) — remove a user from access state.
- `/admin deny_user <telegram_user_id>` — disable a user while keeping the access-state entry.
- `/admin set_user_limit <telegram_user_id> <daily_limit>` (alias: `/admin set_limit`) — set per-user daily quota.
- `/admin set_group <name> <daily_limit>` — create/update a group daily quota.
- `/admin add_to_group <telegram_user_id> <group>` — assign an allowed user to a quota group.
- `/admin allow_chat <telegram_chat_id> [daily_limit]` — allow a Telegram group/supergroup chat and optionally cap that chat's daily usage.
- `/admin deny_chat <telegram_chat_id>` — disable a group/supergroup chat.
- `/admin list_access` (alias: `/admin list`) — summarize configured admins, users, groups, and chats.
- `/admin announce` — start a text-only announcement draft in a private admin chat. Reply to the command with the announcement text, review the preview and recipient count, then press `Send` or `Cancel`. Announcements go only to active allowed users; disabled users, pending grants, groups, and group chats are skipped.

Seasonal announcement copy:

```text
rs biļete bots ir atjaunināts un tagad darbojas labāk - lūdzu, pamēģini vēlreiz.
Бот rs biļete обновлён и теперь работает лучше - попробуй ещё раз, пожалуйста.
```

## State and SpacetimeDB

The bot always keeps a local JSON state file (`RIGASATIKSME_QR_ACCESS_STATE_PATH`, default `/srv/rigassatiksme-qr-bot/state/access.json`). If all SpacetimeDB variables are provided, it also mirrors access state to the `rigassatiksme-qr-bot/spacetimedb` module:

- `RIGASATIKSME_QR_SPACETIME_HOST`
- `RIGASATIKSME_QR_SPACETIME_DATABASE`
- `RIGASATIKSME_QR_SPACETIME_TOKEN`

The SpacetimeDB token must have the `rigassatiksmeqrbot_service` role. Keep tokens in environment/secret stores only; do not commit them.

## Phone priority

QR jobs use `phone-broker`. `phone-broker` exposes current and desired owner priority in `/api/v1/state`:

- ticket page active: `desiredPriority=["ticket","rigassatiksme"]`, `desiredOwner="ticket"`
- queued/running QR job only: `desiredPriority=["rigassatiksme"]`, `desiredOwner="rigassatiksme"`
- idle: `desiredPriority=[]`, `desiredOwner="none"`

Ticket usage still preempts QR generation; QR jobs wait/retry and report `ticket_active` while the ticket page is using the phone.

## Agent verification safety

Agents must not type RS QR test digits into Telegram unless the selected chat is proven to be the `rs biļete` bot before every send. Use `tools/rigassatiksme/telegram_target_locked_stress.swift --select ...` for Telegram Desktop stress checks; it screenshots the selected-chat header and refuses to type if target proof fails.

If the Telegram window cannot be target-verified, use direct `phone-broker` stress jobs or the fake-Telegram live-smoke harness for phone/broker validation, then state that the real Telegram UI path was not exercised.
