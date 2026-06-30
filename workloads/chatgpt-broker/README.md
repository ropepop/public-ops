# Personal Pixel ChatGPT Broker

Personal-only Telegram-to-Pixel broker for driving the ChatGPT Android app through the existing Pixel automation stack.

## Current V1 Boundary

- Telegram bot accepts allowlisted text prompts and queues jobs.
- Broker exposes health, job creation, status, cancellation, OCR, and Telegram notification handling.
- The Pixel worker reads jobs directly from SpacetimeDB; there is no phone-broker ChatGPT route or phone socket bridge.
- Ticket Remote priority remains absolute: the Pixel worker waits or preempts itself when Ticket Remote is active.
- Pixel-side ChatGPT automation is root-grounded and fail-closed against the installed ChatGPT app.
- File upload is deferred until text jobs are reliable.

## Manual Pixel Setup

1. Install ChatGPT without Google Play:
   - prefer Accrescent if ChatGPT/OpenAI is available;
   - otherwise use Aurora Store;
   - use Obtainium or direct APK only after verifying package identity and trust.
2. Confirm setup facts only:
   - package name, expected `com.openai.chatgpt`;
   - installer/source;
   - app version;
   - launch activity.
3. Sign in manually on the Pixel.
4. Open the intended ChatGPT Project and manually select the intended model.
5. Enable the Pixel worker only after root UI discovery verifies the current ChatGPT composer and send controls.

Do not store account passwords, session tokens, or raw files in SpacetimeDB. Text prompts and OCR results live only in the private service table with short retention; public tables keep hashed Telegram ids and status only.

## VPS Env

The bot defaults closed unless the allowlist is set.

```sh
BOT_TOKEN=replace-with-telegram-bot-token
CHATGPT_ALLOWED_TELEGRAM_IDS=123456789
CHATGPT_PROJECT_NAME=Personal Pixel Project
CHATGPT_RUNNER_ENABLED=false
```

Set `CHATGPT_RUNNER_ENABLED=true` and `CHATGPT_PIXEL_WORKER_ENABLED=true` only after the ChatGPT app is installed, manual auth is complete, and Pixel selector discovery has been verified.
