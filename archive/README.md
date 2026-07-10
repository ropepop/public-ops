# Retired code recovery index

Retired DNS control-plane, Rigas Satiksme bot, and phone-broker source is no
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
