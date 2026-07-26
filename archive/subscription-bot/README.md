# Subscription bot retirement and recovery

The Telegram subscription bot and hosted Mini App were retired from
kitty-gration on 2026-07-26. They are not part of the active Compose project,
deployment selectors, validation set, Cloudflare tunnel set, or public app
inventory.

The last checked-in source before retirement is recoverable from commit
`8b58f7f9ffd583cd6b8e6f9edb19d1a9b165d8c5`. The exact binary that was still
running at retirement was built cleanly from commit
`7f61c6c3afff0ff37a5204a3772511cc0f56ece5` as release
`20260711T004253Z`; its image metadata is kept in the restricted host archive.

The historical source commit also contains obsolete tracked runtime state.
Exclude it explicitly when restoring source:

```bash
git restore --source=8b58f7f9ffd583cd6b8e6f9edb19d1a9b165d8c5 -- \
  workloads/subscription-bot \
  ':(exclude)workloads/subscription-bot/state/**' \
  infra/arbuzas/docker/images/subscription-bot.Dockerfile \
  docs/runbooks/MODULE_SUBSCRIPTION_BOT.md
```

The final SQLite state, runtime state, environment, and Cloudflare tunnel
credentials are retained only in a restricted archive on kitty-gration under
`/srv/arbuzas/subscription-bot-backups/`. The final archive is
`subscription-bot-retired-20260726T113301Z.tar.gz`; it is owned by root with
mode `0600`, and its SHA-256 is
`3490573a96910b48848c8c308f1bd18325e7006359f5c9b5304fe9c85b461245`.
Its checksums, SQLite integrity, image archive, and runtime identity were
verified after creation. The archive contains private user, billing, session,
and credential material and must remain root-only.
Do not restore the historical session secret or database from Git. A future
review should create a new session secret and import private state, if needed,
only from the restricted host archive.

The production Telegram token was invalidated during retirement. The Telegram
bot account remains only as a dormant recovery handle; it has no commands,
webhook, deployment, or public application. Any future return must use newly
issued Telegram and payment-provider credentials.

Restoring source or state does not authorize re-enabling the service. A future
return requires an explicit decision, fresh review of the account-sharing and
payment model, current credentials, full automated tests, and a new end-to-end
production acceptance run. Routine deploy and rollback paths intentionally
keep the retired containers scaled to zero.
