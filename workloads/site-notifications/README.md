# Notifications

Telegram-controlled notifier for new gribu.lv messages.

## Features

- Telegram control commands and navigation buttons
- Adaptive polling cadence with watchdogs and restart backoff
- Session refresh and automatic cookie rotation
- Healthcheck and diagnostics commands
- Managed-service runtime gate for production

## Reliability Model

- daemon startup is allowed only when `RUNTIME_CONTEXT_POLICY=managed_service`
- non-managed daemon launch exits with code `11`
- worker exits `20/21` are restarted by the daemon supervisor with exponential backoff

## Local Development

```bash
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
pip install -r requirements-dev.txt
cp .env.example .env
PYTHONPATH=. pytest -q
make docker-image-build
```

## Run

Start the daemon locally only through a managed wrapper:

```bash
RUNTIME_CONTEXT_POLICY=managed_service python app.py daemon
```

Run a single check:

```bash
python app.py check-once
```

Run the health check:

```bash
python app.py healthcheck
```

## Active Deployment

```bash
../../tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"
../../tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"
```

The production service stores state under `/srv/arbuzas/site-notifications/state` and uses `/etc/arbuzas/env/site-notifications.env` as its managed env file.

## Notes

- Only the configured Telegram chat can control the bot.
- Manual daemon launches outside a managed service remain unsupported.
- The old Pixel diagnostics flow is rollback-only legacy material.
