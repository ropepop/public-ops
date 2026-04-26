# Site Notifications Module

- Canonical operations: [ROOT_OPERATIONS](./ROOT_OPERATIONS.md)
- Active runtime: Docker on Arbuzas
- Persistent state root: `/srv/arbuzas/site-notifications/state`
- Host env file: `/etc/arbuzas/env/site-notifications.env`
- Runtime policy: `RUNTIME_CONTEXT_POLICY=managed_service`

## Local Checks

```bash
cd workloads/site-notifications
PYTHONPATH=. pytest -q
make docker-image-build
```

## Deploy

```bash
./tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"
```

## Validate

```bash
./tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"
```

## Notes

- The daemon is intended to run only as a managed service now.
- Manual launches remain unsupported.
