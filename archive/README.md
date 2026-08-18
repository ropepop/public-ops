# Retired code recovery index

Retired DNS control-plane, Rigas Satiksme bot, phone-broker, and subscription-bot source is no
longer checked out with the active production repository. None of these
services runs on kitty-gration.

The complete pre-reduction archive remains recoverable from Git commit
`de42a1c526cc3c13c4a927c1007f2a7ee03bef9f`:

```bash
git restore --source=de42a1c526cc3c13c4a927c1007f2a7ee03bef9f -- archive
```

Restoring files does not authorize re-enabling a service. Each retired service
still requires a current architecture review, fresh credentials, tests, and an
explicit deployment decision.

The subscription bot has its own recovery boundary and final-state notes in
[`subscription-bot/README.md`](./subscription-bot/README.md).

## Ticket leftovers

Leftover Ticket pictures, proof packs, and retired notes now live in [`ticket/`](./ticket/).
That folder is backup only. Start live Ticket work from `workloads/ticket-remote/CURRENT.md`.

