# Kontrole

Web-first Riga Satiksme control map and incident feed workload for the kitty-gration production stack.

For agent-specific context, tests, deploy checks, and live-map pitfalls, start with [AGENTS.md](./AGENTS.md).

## Local Development

```bash
cp .env.example .env
make test
make build
make spacetime-build
make docker-image-build
go run ./cmd/catalogsync
go run ./cmd/bot
```

## Active Deployment

The active production runtime is Docker on kitty-gration. Local workload commands stop at build and image preparation; deployment happens through the shared operator script:

```bash
../../tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user "$USER"
../../tools/arbuzas/deploy.sh validate --release-id "<release-id>" --ssh-host kitty-gration --ssh-user "$USER"
```

## Important Runtime Paths

- Catalog source mirror: `/srv/arbuzas/satiksme-bot/data/catalog/source`
- Generated catalog: `/srv/arbuzas/satiksme-bot/data/catalog/generated/catalog.json`
- Public bundles: `/srv/arbuzas/satiksme-bot/data/public-bundles`
- State: `/srv/arbuzas/satiksme-bot/state`

## Private Telegram Analyzer Operations

Authorize or replace the personal-account session with the interactive helper. It writes to a private staging file and only replaces the current session after Telegram confirms authorization:

```bash
go run ./cmd/chat-analyzer-session -list-dialogs=false
```

Validate the current session without starting login or writing anything back to the session file:

```bash
go run ./cmd/chat-analyzer-session -validate-only -list-dialogs=false
```

Analyzer credentials may be loaded from private files with `SATIKSME_CHAT_ANALYZER_API_ID_FILE`, `SATIKSME_CHAT_ANALYZER_API_HASH_FILE`, and `SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY_FILE`. For the managed Google provider, that Google key also supplies the model client. Disabled analyzer deployments do not require or pass these private files. A direct environment value takes precedence when both forms are present.

The loopback-only `/api/v1/internal/health` response includes a sanitized `chatAnalyzer` section with session state, collection and processing timestamps, the selected model, circuit state, and interrupted-batch recovery count. It never includes chat text or credentials.

Google auto-selection defaults to the `gemma_parameter` policy. It selects compatible Gemma text models by capability and size but does not label them free-tier models; `verified_free` policies require explicit eligibility metadata from the model endpoint.

Collection accepts only messages no older than `SATIKSME_CHAT_ANALYZER_MAX_MESSAGE_AGE` (24 hours by default). Older messages are skipped before storage or model processing, while the Telegram checkpoint still advances. Pending messages that have aged beyond the same limit are expired without model processing. The sanitized internal health response reports both skipped collection and expired pending counts. Model calls are paced by `SATIKSME_CHAT_ANALYZER_MODEL_CALL_DELAY` (5 seconds by default).

Telegram-derived report and vote writes are retry-safe across service restarts. Before a public write, every source message receives the same private action claim. After an interrupted finalization, the next pass reconciles that claim against the already committed event before any fresh model analysis, so regrouping or reclassification cannot publish the action again.

Retry delays use the private `processingAttempt` value stored with each analysis outcome. This is the logical model/application attempt count; the storage-level `attempts` column counts durable message-state writes, including the separate safety claim and final outcome writes.

Telegram history is read forward from the saved checkpoint so a burst larger than one API page is not silently skipped. Collection uses `SATIKSME_CHAT_ANALYZER_COLLECTION_PAGE_SIZE` (25 messages by default), independently of `SATIKSME_CHAT_ANALYZER_BATCH_LIMIT` (5 messages by default) used for each model request. Source chat text remains private analyzer input; public area incidents use only a derived location label, never the source message text.

## Notes

- Anonymous visitors can browse the map and incidents; Telegram login unlocks reporting, voting, and commenting.
- The website uses Telegram's current Login library (`telegram-login.js?3`): the page fetches `/api/v1/auth/telegram/config`, receives an `id_token` from Telegram, and finishes the site session through `/api/v1/auth/telegram/complete`.
- Browser pages no longer talk to Spacetime directly; the site uses its own JSON API while the backend keeps Spacetime as the live data store.
- Interactive browser UI uses ArrowJS for changing map, incident list, detail, and status areas. Keep static or no-JS pages simple until they need browser-side interactivity.
- For UI changes that touch the Arrow runtime, run `(cd web-client && npm run build:arrow)` before the normal tests.
- The old Pixel deploy helpers are rollback-only legacy material.
