# Subscription

Telegram-first subscription sharing workload for the current production stack.

This service keeps the first version intentionally self-contained:
- Telegram bot for owner/member/operator flows
- command-first in-chat menus with inline action buttons
- SQLite-backed billing, membership, events, and support state
- sandbox crypto billing adapter with quote polling and simulated settlement
- NOWPayments integration with signed webhook handling, provider-event storage, and polling fallback
- monthly billing only, with USDC as the internal pricing anchor

## Local Development

```bash
cp .env.example .env
go test ./...
go run ./cmd/bot
make docker-image-build
```

By default the workload starts in Telegram-first mode:
- Telegram is the main product surface
- the HTTP listener stays on for health checks and payment callbacks
- the Mini App shell stays on unless `SUBSCRIPTION_BOT_WEB_SHELL_ENABLED=false` is set for debugging
- payment simulation buttons only appear when the sandbox provider is active

For a live deployment, the minimum env you need is:
- `BOT_TOKEN`
- `SUBSCRIPTION_BOT_TELEGRAM_BOT_USERNAME`
- `SUBSCRIPTION_BOT_OPERATOR_IDS`
- `SUBSCRIPTION_BOT_PAYMENT_PROVIDER=nowpayments`
- `SUBSCRIPTION_BOT_NOWPAYMENTS_API_KEY`
- `SUBSCRIPTION_BOT_NOWPAYMENTS_IPN_SECRET`
- a public `SUBSCRIPTION_BOT_WEB_PUBLIC_BASE_URL` that resolves to this workload

## Active Deployment

The active production runtime is Docker on Arbuzas:

```bash
../../tools/arbuzas/deploy.sh deploy --ssh-host arbuzas --ssh-user "$USER"
../../tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host arbuzas --ssh-user "$USER"
```

## Core Commands

- `/start`
- `/help`
- `/create_plan`
- `/create_plan <service-code> <monthly-usdc> <seats> <renewal-date> [access_mode]`
- `/my_plans`
- `/join`
- `/join <invite-code>`
- `/pay`
- `/pay [invoice-id] [asset] [network]`
- `/invoice`
- `/renew`
- `/members [plan-id]`
- `/ledger [plan-id]`
- `/support [plan-id] [message]`
- `/settings`
- `/admin`
- `/cancel`

The guided path is now button-driven:
- `/start` or `/help` opens the home menu
- `Create plan` walks through service, price, seats, and renewal date
- `My plans` shows plan cards with `Pay`, `Invoice`, `Invite`, `Members`, `Ledger`, `Support`, and `Renew`
- operators get an in-chat dashboard for open support, renewal issues, and recent plans

## Notes

- The built-in payment adapter is a sandbox provider so the full billing loop can run inside this repo and on the Arbuzas runtime without external processor credentials.
- The sandbox provider still uses the production payment abstraction (`CreateInvoiceQuote`, `GetInvoiceStatus`, `ListInvoiceTransactions`, `NormalizeProviderPayment`) so a live processor can replace it later.
- The first hosted processor target is NOWPayments. The live callback endpoint is `POST /api/v1/payments/webhook/nowpayments` under the configured public base URL.
- No third-party account passwords are stored or sent through Telegram.
