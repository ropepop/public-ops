# Ticket Remote

Public manager for `ticket.jolkins.id.lv`.

The service validates SpacetimeAuth email identity, checks ticket membership in SpacetimeDB, relays the active phone backend stream to viewers, and gates phone inputs through short private controle-code sessions.

## Local Development

```bash
make test
make spacetime-build
TICKET_REMOTE_AUTH_MODE=dev make run
```

## Runtime Model

- General mode: linked users can view the ViVi ticket stream together.
- Controle-code mode: one linked user claims private phone control for 45 seconds.
- One extension is allowed, for 90 seconds total.
- Non-controllers stay connected and see claimant, timer, and presence.
- Browser users connect directly to SpacetimeDB for authenticated ticket state and control-session changes.
- Browser users never talk directly to the Android simulator or Pixel; only this service talks to the selected phone backend.
- Admins can switch the active backend between the persistent Android simulator and the Pixel fallback from `/admin`.
- The simulator backend runs with 4 GB Android guest RAM inside a 6 GB no-swap Docker envelope, with 2 cores max, and depends on the Pixel orchestrator ticket phone service being installed inside the emulator; the Arbuzas ticket deploy starts it when the cached APK is present.
- Owners have a simulator-only control surface in `/admin` for using the emulator before ViVi is installed. This uses private Docker-network ADB from `ticket_remote`; it does not expose ADB or Docker controls to browsers.
- Public video is H.264 over the existing HTTPS WebSocket: the active phone backend emits one private root FFmpeg H.264 stream, and `ticket_remote` fans it out to authenticated browsers without public media ports.

## Required Production Configuration

- `TICKET_REMOTE_AUTH_MODE=spacetime`
- `TICKET_REMOTE_SPACETIME_AUTH_ISSUER=https://auth.spacetimedb.com/oidc`
- `TICKET_REMOTE_SPACETIME_AUTH_CLIENT_ID`
- `TICKET_REMOTE_SESSION_SIGNING_KEY`
- `TICKET_REMOTE_STATE_BACKEND=spacetime`
- `TICKET_REMOTE_SPACETIME_DATABASE`
- `TICKET_REMOTE_SPACETIME_OIDC_ISSUER=https://vilciens.kontrole.info/oidc` on Arbuzas, matching the public train OIDC issuer that publishes the runtime signing key.
- either `TICKET_REMOTE_SPACETIME_BEARER_TOKEN` or `TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE`
- `TICKET_REMOTE_PHONE_BACKENDS`
- `TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID`
- `TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE`

Cloudflare remains only the HTTPS tunnel. SpacetimeAuth is the public email-login front door, and SpacetimeDB remains the ticket membership and session state source of truth. There is still no public ADB, browser-direct phone access, Docker control, public media port, separate media service, or extra ticket Docker unit.
