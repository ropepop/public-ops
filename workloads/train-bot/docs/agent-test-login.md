# Agent Test Login

This is the canonical TrainBot path for operator-driven browser testing without Telegram.

It exists so an agent can open TrainBot the same way a signed-in user sees it, but through a short-lived one-time link instead of a Telegram-authenticated mini-app session.

## What It Does

- Uses one fixed test user only.
- Resets that user to a clean baseline before the session is created.
- Creates the normal TrainBot signed-in web session.
- Redirects the browser into `/app` without leaving a reusable auth token in the URL.

The only browser entry point an agent needs is a minted URL shaped like:

```text
https://train-bot.jolkins.id.lv/app?test_ticket=...
```

## Required Config

Set these on the TrainBot runtime:

```bash
TRAIN_WEB_TEST_LOGIN_ENABLED=true
TRAIN_WEB_TEST_USER_ID=7001
TRAIN_WEB_TEST_TICKET_SECRET_FILE=/etc/arbuzas/secrets/train-bot-test-ticket.secret
TRAIN_WEB_TEST_TICKET_TTL_SEC=60
```

Notes:

- `TRAIN_WEB_TEST_USER_ID` is the fixed user the agent will act as.
- `TRAIN_WEB_TEST_TICKET_SECRET_FILE` must be different from the normal session secret.
- Keep `TRAIN_WEB_TEST_TICKET_TTL_SEC` low. `60` seconds is the intended default.

## Exact Operator Flow

From the workload root, mint a one-time link:

```bash
cd workloads/train-bot
make test-login-link
```

That command prints the full `/app?test_ticket=...` URL.

Hand that exact URL to the agent. The agent should open it directly. It does not need to start in Telegram first.

## What Happens In The App

When the app sees `test_ticket` in the URL, it:

1. Calls `POST /api/v1/auth/test` with the ticket.
2. The server validates the ticket, rejects reused or expired tickets, and resets the fixed test user.
3. The server creates the same normal signed-in session cookie used by the Telegram auth path.
4. The app loads the signed-in user state.
5. The app removes `test_ticket` from the browser URL.
6. Reloads continue through the normal signed-in session, not through the one-time link.

Expected browser result:

- The agent lands on `/app` already signed in.
- The address bar no longer shows `test_ticket` after success.
- Refreshing the page keeps the session.

## Reset Semantics

Each successful test login resets the fixed test user to a deterministic baseline.

Cleared before session creation:

- active ride
- undo state
- favorites
- mutes
- subscriptions
- recent action state
- reports created by the fixed test user
- station sightings created by the fixed test user
- votes created by the fixed test user
- comments created by the fixed test user

Restored defaults:

- alerts enabled
- detailed alert style
- English language

Not reset:

- shared schedule data
- public train data
- public map data
- activity from other users

## Failure Modes

Common failures and what they mean:

- `not found`: test login is disabled on the running TrainBot server.
- `missing test login ticket`: the link was incomplete or the agent did not open the minted URL.
- `invalid test login ticket signature`: the ticket was altered or signed with the wrong secret.
- `test login ticket expired`: the link sat too long before being opened.
- `test login ticket already used`: the link was already consumed once.

In all of those cases, mint a fresh link with `make test-login-link` and retry.

## Secret Rotation

To rotate the one-time ticket secret:

1. Write a new value into the file referenced by `TRAIN_WEB_TEST_TICKET_SECRET_FILE`.
2. Restart or redeploy TrainBot.
3. Stop using any previously minted links.

## Discovery

This doc is linked from:

- [TrainBot README](../README.md)
- [TrainBot runbook](../../../docs/runbooks/MODULE_TRAIN_BOT.md)
