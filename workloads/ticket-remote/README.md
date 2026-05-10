# Ticket Remote

Public manager for `ticket.jolkins.id.lv`.

The service validates SpacetimeAuth email identity, checks ticket membership in SpacetimeDB, relays the active phone backend stream to viewers, and brokers short automated control-code requests without exposing direct phone control to browsers.

## Local Development

```bash
make test
make spacetime-build
TICKET_REMOTE_AUTH_MODE=dev make run
```

## Runtime Model

- General mode: linked users can view the ViVi ticket stream together.
- Control-code requests: a linked user enters 2-9 digits in the webpage, `ticket_remote` queues the request, the phone automates ViVi, and only the requester receives the captured result until they close it, replace it, refresh/sign out, or the service loses in-memory state.
- A requester can submit at most two code requests per rolling minute. A second successful request replaces the visible result on that user's page.
- Other viewers stay connected to the shared raw ticket stream. They may briefly see the real phone open and close ViVi's control-code UI, but they never receive direct control.
- Browser users connect directly to SpacetimeDB for authenticated ticket state, while code-request actions go through `ticket_remote`.
- Browser users never talk directly to the Android simulator or Pixel; only this service talks to the selected phone backend.
- Admins can switch the active backend between the persistent Android simulator and the Pixel fallback from `/admin`.
- The simulator backend runs with 4 GB Android guest RAM inside a 6 GB no-swap Docker envelope, with 2 cores max, and depends on the Pixel orchestrator ticket phone service being installed inside the emulator; the Arbuzas ticket deploy starts it when the cached APK is present.
- Owners have a simulator-only control surface in `/admin` for using the emulator before ViVi is installed. This uses private Docker-network ADB from `ticket_remote`; it does not expose ADB or Docker controls to browsers, and the public web container must not mount Pixel ADB private keys.
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
- on Arbuzas, key-file mode should use `TICKET_REMOTE_SPACETIME_JWT_PRIVATE_KEY_FILE=/run/secrets/ticket-remote/spacetime-jwt-private-key.pem`
- `TICKET_REMOTE_PHONE_BACKENDS`
- `TICKET_REMOTE_DEFAULT_PHONE_BACKEND_ID`
- `TICKET_REMOTE_ACTIVE_PHONE_BACKEND_FILE`

Cloudflare remains only the HTTPS tunnel. SpacetimeAuth is the public email-login front door, and SpacetimeDB remains the ticket membership and session state source of truth. There is still no public ADB, browser-direct phone access, Docker control, public media port, separate media service, broad `/etc/arbuzas/secrets` mount, Pixel ADB key mount, or extra ticket Docker unit.
