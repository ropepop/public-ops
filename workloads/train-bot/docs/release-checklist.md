# Release Checklist

1. Run `make test`.
2. Run `make scrape` and confirm the new snapshot validates.
3. Run `make build` or `make docker-image-build`.
4. Deploy with `../../tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"`.
5. Run `../../tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"`.
6. Confirm the public homepage renders normal incidents content instead of the live-data outage screen.
7. If validation fails on dependency DNS, inspect the train-bot container resolver path before changing host-wide DNS.
8. Record the release id you would roll back to before closing the release.
