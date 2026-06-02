# RS Biļete User Acquisition Campaign

This outreach workflow is disabled.

As of June 2, 2026, Arbuzas must not run the RS Biļete acquisition daemon, send first-contact messages, retry failed outreach, or grant access through this campaign path.

## Disabled State

- The production Compose service `satiksme_rs_acquisition` has been removed from the active repo layout.
- The Satiksme host environment keeps `RS_ACQUISITION_ENABLED=false`.
- Pending approval drafts were invalidated with reason `campaign_disabled`.
- The `iamhdzs` sender session was moved away from the active session filename on Arbuzas.
- Future Satiksme production images should not ship `/usr/local/bin/rs-acquisition-campaign`.

## Operator Rule

Do not reauthorize a sender session, start an acquisition profile, approve old tokens, retry failed drafts, or run test DMs from this campaign.

If historical campaign state needs inspection, use read-only database checks and avoid exposing recipient details in notes or tickets.

## Re-enablement

Re-enabling this campaign is out of scope for normal operations. Treat it as a new launch requiring a fresh design review, safety review, consent policy review, and new operator runbook before any Telegram account is authorized or any message is sent.
