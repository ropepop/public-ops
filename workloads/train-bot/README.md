# Telegram Ride Status Alerts Bot (Latvia MVP)

Legal, crowdsourced train ride-status bot for Telegram.

The train-bot Pixel flow is now:
- build and test on the workstation
- package a component release locally
- deploy and run only in the rooted orchestrator runtime at `/data/local/pixel-stack/apps/train-bot`

There is no Termux build, repo sync, or schedule-prep path in the supported train-bot workflow.

## Supported Commands

### Local Development

```bash
cp .env.example .env
# set BOT_TOKEN

go test ./...
go run ./cmd/bot
```

### Pixel Native Pipeline

```bash
make pixel-native-test
make pixel-native-build
../../tools/pixel/redeploy.sh --scope train_bot
make pixel-release-check
make pixel-e2e
```

`make pixel-native-build` packages a train-bot component release locally and prints the staged release dir.

`../../tools/pixel/redeploy.sh --scope train_bot`:
- syncs `.env` to the root-owned train-bot env files
- runs orchestrator bootstrap/preflight
- builds the Android binary on the workstation
- generates the same-day Riga schedule locally
- stages the component release
- redeploys `train_bot`
- runs the release parity check

### Full Native Pass

```bash
make pixel-native-all
```

This runs:
- host-side tests
- native deploy
- public, miniapp, and bot e2e smoke checks

## Retired Commands

These old train-bot commands now fail intentionally:
- `make pixel-bootstrap`
- `make pixel-sync`
- `make pixel-test`
- `make pixel-build`
- `../../tools/pixel/redeploy.sh --scope train_bot`
- `make pixel-validate`
- `make pixel-all`

Use the `pixel-native-*` targets instead.

## Prerequisites

| Component | Required | Notes |
|---|---|---|
| Go 1.22+ | Yes | Local test, build, and scraper execution. |
| Tailscale CLI + SSH password env | Preferred | Primary rooted Pixel transport for deploy/release checks. |
| `adb` | Fallback | Recovery transport when SSH readiness fails or Tailscale is unavailable. |
| Rooted Pixel + Magisk | Yes | Required for the orchestrator-owned runtime. |
| `sqlite3` | Yes | Used locally to validate the pulled runtime DB. |
| `agent-browser` CLI | Yes | Required for browser smoke scripts. |
| `cloudflared` | Conditional | Required locally when `trainBot.ingressMode=cloudflare_tunnel`. |
| Telegram bot token | Yes | Set in `.env` as `BOT_TOKEN`. |

SSH-first environment:

```bash
export PIXEL_TRANSPORT=ssh
export PIXEL_SSH_HOST="<tailnet-ip>"
export PIXEL_SSH_PORT=2222
export PIXEL_DEVICE_SSH_PASSWORD="<root-ssh-password>"

bash ../../tools/pixel/check_ssh_ready.sh --ssh-host "${PIXEL_SSH_HOST}"
```

Fallback ADB target in scripts:
- `192.168.31.25:5555`

Override at runtime:

```bash
export ADB_SERIAL=<your-device-serial>
```

## Environment and Runtime Paths

Copy `.env.example` to `.env`:

```bash
cp .env.example .env
```

The supported Pixel env sync targets are:
- `/data/local/pixel-stack/conf/apps/train-bot.env`
- `/data/local/pixel-stack/apps/train-bot/env/train-bot.env`

The rooted runtime lives under:
- `/data/local/pixel-stack/apps/train-bot`

Important runtime paths:
- binary symlink: `/data/local/pixel-stack/apps/train-bot/bin/train-bot.current`
- schedules: `/data/local/pixel-stack/apps/train-bot/data/schedules`
- database: `/data/local/pixel-stack/apps/train-bot/train_bot.db`
- logs: `/data/local/pixel-stack/apps/train-bot/logs`

## Daily Operations

### Deploy Current Train-Bot Build

```bash
../../tools/pixel/redeploy.sh --scope train_bot --transport ssh --ssh-host "${PIXEL_SSH_HOST}"
```

Expected result:
- `train-bot.current` points to a fresh release under `releases/`
- the same-day schedule exists in the runtime schedule dir
- `make pixel-release-check` passes

### Validate Production Readiness

```bash
make pixel-native-validate
```

This runs:
- runtime asset freshness checks
- host-side test/build/deploy gates
- release check
- public/miniapp/bot smoke checks
- runtime DB and schedule validation

### Restart Runtime Without Repackaging

```bash
make pixel-restart-train
```

### Refresh Orchestrator Runtime Assets

```bash
make pixel-refresh-runtime
```

Use this when orchestrator runtime templates or entrypoints changed locally.

## Troubleshooting

| Symptom | Likely Cause | Action |
|---|---|---|
| `pixel-native-build` fails | local Go build or local scrape failed | inspect `output/pixel/train-bot-native-build-*.log` |
| `pixel-native-test` fails | app regression | inspect `output/pixel/train-bot-native-test-*.log` |
| `tools/pixel/redeploy.sh --scope train_bot` fails before redeploy | bootstrap/env/tunnel preflight failure | rerun with the logged error, then retry |
| release check fails | public origin is stale or tunnel mismatch | run `make pixel-release-check`, inspect the generated JSON report |
| miniapp smoke fails | Telegram Web session/profile drift | rerun `make pixel-miniapp-smoke` after confirming Telegram Web auth |
| runtime shows no trains | same-day snapshot missing or not loaded | rerun `tools/pixel/redeploy.sh --scope train_bot`, then inspect runtime schedule dir and DB rows |

## Useful Checks

Current runtime process:

```bash
bash scripts/pixel/validate_prod_readiness.sh --transport ssh --ssh-host "${PIXEL_SSH_HOST}"
```

Current runtime logs:

```bash
bash scripts/pixel/release_check.sh --transport ssh --ssh-host "${PIXEL_SSH_HOST}"
```

Runtime DB and schedule validation:

```bash
bash scripts/pixel/redeploy_release.sh --validate-only --transport ssh --ssh-host "${PIXEL_SSH_HOST}"
```

## Notes

- The orchestrator component-release contract is unchanged.
- The train-bot bundle remains a tar artifact with the built binary and schedule data.
- The supported deploy entrypoint is `../../tools/pixel/redeploy.sh --scope train_bot`.
