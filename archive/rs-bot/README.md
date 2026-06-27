# archive/rs-bot

Archived source, configuration, and runbook material for the Rīgas Satiksme monthly-ticket Telegram bot (`rigassatiksme_qr_bot`), the Rīgas Satiksme user acquisition campaign (`rs-acquisition-campaign`), the broker-side RS re-login channel, the target-locked Telegram stress helper, and the pre-strip `phone-broker` source.

Everything here is **out of scope for normal operations**. Do not restart, re-enable, or restore any of these artifacts without a fresh design, safety, and consent review.

## Wind-down history

- 2026-06-02: The `satiksme_rs_acquisition` Compose service was removed and `RS_ACQUISITION_ENABLED=false` in production (`docs/runbooks/RS_BILETE_USER_ACQUISITION_CAMPAIGN.md`).
- 2026-06-18: The full wind-down landed. The `rigassatiksme_qr_bot` container was stopped and removed from Compose; the bot workload, the acquisition campaign, the broker-side RS re-login channel, and the Swift helper were deleted from active code; the phone-broker was stripped to ticket-only.

## Inventory

| Path | What it is |
| ---- | ---------- |
| `workloads/rigassatiksme-qr-bot/` | Full source of the RS Telegram bot workload (cmd, internal, scripts, go.mod, spacetimedb, README, module.yaml). |
| `workloads/satiksme-bot-cmd-rs-acquisition/` | The `rs-acquisition-campaign` command and the `internal/acquisition` package from `satiksme-bot`. |
| `phone-broker-rs-snapshot/` | Pre-strip copy of `workloads/phone-broker/`. Use this to compare against the current ticket-only source. |
| `phone-broker-rs-stripped.patch` | `git diff` of the phone-broker strip commit. Reverse-apply to bring RS code back into a fresh `phone-broker`. |
| `tools/telegram_target_locked_stress.swift` | macOS helper that targets Telegram Desktop and refuses to type if the chat header is not the `rs biļete` bot. |
| `spacetimedb/` | The SpacetimeDB module for the bot. |
| `docker/rigassatiksme-qr-bot.Dockerfile` | The bot's Dockerfile. |
| `docs/RS_BILETE_USER_ACQUISITION_CAMPAIGN.md` | Archived acquisition-campaign runbook. |
| `docs/RS_VIVI_INCIDENT_ANALYTICS_ARCHITECTURE.md` | Archived RS/ViVi incident architecture doc. |
| `schemas/rs-vivi-incident-trace.v1.schema.json` | Archived RS/ViVi user-impact incident trace schema. |
| `schemas/rs-vivi-qr-analytics.v1.schema.json` | Archived RS broker analytics rollup schema. |
| `env/arbuzas.example.env.rs-snippet.txt` | The four `ARBUZAS_RIGASATIKSME_QR_*` env vars as they appeared in `arbuzas.example.env`. |
| `env/rigassatiksme-qr-bot.env.redacted-keys.txt` | Redacted keys from the live `rigassatiksme-qr-bot.env` (Telegram bot token and SpacetimeDB token are masked). |
| `env/satiksme-bot.env.rs-snippet.txt` | The `RS_ACQUISITION_*` env vars that were dropped from `satiksme-bot.env`. |
| `compose-snippet/rigassatiksme_qr_bot.compose.fragment.yml` | The deleted Compose service block, for re-add reference. |

## Re-enable recipe (out of scope)

To re-enable the historical RS path you would need to do all of the following, in addition to a fresh design, safety, and consent review:

1. Restore the bot workload:

   ```bash
   cp -a archive/rs-bot/workloads/rigassatiksme-qr-bot workloads/
   ```

2. Restore the acquisition campaign code (cmd and package):

   ```bash
   cp -a archive/rs-bot/workloads/satiksme-bot-cmd-rs-acquisition/cmd/rs-acquisition-campaign workloads/satiksme-bot/cmd/
   cp -a archive/rs-bot/workloads/satiksme-bot-cmd-rs-acquisition/internal/acquisition workloads/satiksme-bot/internal/
   ```

3. Restore the Swift helper:

   ```bash
   mkdir -p tools/rigassatiksme
   cp -a archive/rs-bot/tools/telegram_target_locked_stress.swift tools/rigassatiksme/
   ```

4. Restore the bot Dockerfile and add the service back to `infra/arbuzas/docker/compose.yml` using the fragment under `archive/rs-bot/compose-snippet/`.

5. Reintroduce the RS env vars in `infra/arbuzas/docker/env/arbuzas.example.env` and on the host at `/etc/arbuzas/env/rigassatiksme-qr-bot.env`. Use the redacted-keys file as a key list; do not reuse the retired env file from the host without re-validating the tokens.

6. Restore the broker-side RS code: copy `archive/rs-bot/phone-broker-rs-snapshot/` over `workloads/phone-broker/`, then reverse-apply `archive/rs-bot/phone-broker-rs-stripped.patch`. If the strip has further evolved past the patch, re-do the surgery by hand using the snapshot as ground truth.

7. Restore the RS re-login channel and the `rsQr` / `rsLogin` analytics rollup in the broker's `Snapshot` and `Analytics` if needed. Both are part of the strip diff.

8. Restore the RS runbook (`docs/runbooks/RS_BILETE_USER_ACQUISITION_CAMPAIGN.md`) and the RS architecture doc (`docs/architecture/RS_VIVI_INCIDENT_ANALYTICS_ARCHITECTURE.md`) under their original paths.

9. Update `docs/architecture/INDEX.md`, `AGENTS.md`, `docs/CONTEXT.md`, `docs/runbooks/ROOT_OPERATIONS.md`, `docs/runbooks/MODULE_PHONE_BROKER.md`, and `docs/plans/active/2026-06-04-arbuzas-ram-maxout-investigation.md` to add the bot back to the active surface.

10. Add the four `ARBUZAS_RIGASATIKSME_QR_*` env vars to `tools/arbuzas/deploy.sh` and `tools/arbuzas/host_mirror.py` if you need the deploy script to track them. The current `arbuzas.example.env.rs-snippet.txt` shows what was there.

11. Re-enable the SpacetimeDB service token for the `rigassatiksmeqrbot_service` role in the SpacetimeDB console. The retired host file had the previous token; the user revoked it during the wind-down.

12. Re-add the bot deployment to `tools/arbuzas/deploy.sh`: `ALL_SERVICES`, `mark_validation_group rigassatiksme_qr`, the case in `resolve_requested_services`, the inclusion in `compute_release_source_sha256`, the `copy_tree_into_release` line, the `validate_remote_rigassatiksme_qr_bot_workload_health` function, the help text, and the `repair-portainer` service list.

13. Before deploying, run `go test ./...` in `workloads/phone-broker/`, `workloads/satiksme-bot/`, and `workloads/ticket-remote/`. Validate ticket + bot end-to-end on a non-production host first.

## Pixel side

The Pixel side is in the external `pixel-phone` repo (out of this tree). The `RigasSatiksmeLoginOperation` and the `rigassatiksme_login_*` WebSocket commands still exist there. The broker no longer sends those commands, so the Pixel side is dormant but intact. Re-enabling the RS path on the broker side will start sending those commands again without any Pixel-side change.

## @rs_bilete_bot BotFather account

The `@rs_bilete_bot` BotFather account was not deleted during the wind-down. The account is live but unreached. If a re-enable is approved, no new BotFather work is needed; the existing token in the archived env file (if still valid) can be used, or a new token can be requested.
