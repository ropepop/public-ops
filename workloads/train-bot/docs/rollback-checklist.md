# Rollback Checklist

1. Identify the last known good release package.
2. Run `../../tools/arbuzas/deploy.sh rollback --release-id "<previous-release-id>" --ssh-host arbuzas --ssh-user "$USER"`.
3. Re-run `../../tools/arbuzas/deploy.sh validate --release-id "<previous-release-id>" --ssh-host arbuzas --ssh-user "$USER"`.
4. Confirm the public app still serves a valid snapshot and the expected version.
5. Confirm Telegram can still open the app and deliver a smoke alert.
6. Leave the failed release archived with notes about what broke.
