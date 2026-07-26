# Tiny VLESS / 3X-UI

This directory is the reproducible source for the existing 3X-UI/Xray Compose
project on kitty-gration. The project deliberately remains separate from the
main `arbuzas` Compose project so its database, client identities, listeners,
and runtime naming do not change. It is no longer separately operated:
`tools/arbuzas/deploy.sh` owns it through the first-class `tiny_vless` service
selector, the same local-first mirror, and the same validation and rollback
umbrella used by the other services.

## Ownership and paths

- Source: `infra/arbuzas/tiny-vless/`
- Canonical environment: `/etc/arbuzas/env/tiny-vless.env`
- Canonical certificates, keys, and capability:
  `/etc/arbuzas/secrets/tiny-vless/`
- Live SQLite database: `/opt/tiny-vless/db`
- SQLite-safe backups: `/srv/arbuzas/tiny-vless/backups`

The environment and secret paths are covered by the existing local-first host
mirror. The database is persistent application state; never place it in the
plaintext mirror or a release bundle.

Pull, compare, and apply configuration through the existing umbrella:

```bash
./tools/arbuzas/deploy.sh mirror-pull --ssh-host kitty-gration --ssh-user ropepop
./tools/arbuzas/deploy.sh mirror-audit --ssh-host kitty-gration --ssh-user ropepop
./tools/arbuzas/deploy.sh deploy-config --ssh-host kitty-gration --ssh-user ropepop
```

Use `mirror-push` only for an intentional reviewed sync that should not restart
anything. `deploy-config` recognizes changes below the tiny-VLESS environment
and secret paths and selects only `tiny_vless`.

Deploy or validate the external project explicitly:

```bash
./tools/arbuzas/deploy.sh deploy --services tiny_vless --validation-profile standard --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services tiny_vless --validation-profile standard --ssh-host kitty-gration --ssh-user "$USER"
```

An unscoped deploy includes tiny-VLESS health checks, but does not restart the
VPN. A source rollout or recreation requires `--services tiny_vless`, so an
unrelated application release cannot interrupt connected clients.

## Host integrations

A clean target needs Docker with the Compose plugin, Python 3, SQLite tooling,
Nginx, Tailscale, the host firewall tooling, `iproute2` traffic control, SSH,
and passwordless operator `sudo`. The required public TCP and UDP listeners
must be available.

The targeted deployment manages the project and its host dependencies as one
unit:

- the private Tailscale panel route and HTTPS subscription publication;
- the public Nginx subscription gateway on TCP port 18081;
- the boot-persistent VPN abuse firewall policy; and
- the recurring bandwidth limiter, which must reattach to the container's
  current host interface after every recreation.

Tailscale changes are made against the complete Serve/Funnel configuration and
must preserve every unrelated route. The public Nginx listener is plain HTTP
and has no TLS. Its random capability prevents practical blind guessing, but
does not provide confidentiality, integrity, or server authentication against
an on-path observer.

## Mobility profiles

The mobility expansion keeps the original VLESS/REALITY/TCP inbound and client
unchanged. Five independent experimental clients reuse the original
subscription identifier through 3X-UI's inbound-add path:

- Hysteria2 with conservative QUIC timing on dedicated public UDP port
  `8447`;
- VLESS XHTTP over HTTP/3;
- WireGuard with endpoint roaming;
- VLESS XHTTP over HTTP/2/REALITY as a fast-recovery TCP control;
- VMess over mKCP as a packet-loss control.

The HTTP/3 profile keeps Xray's packet-up connection pool bounded at six but
disables request-count rotation. In Xray 26.7.11, the default randomized
request budget can rotate a busy H3 upload connection while packet POSTs are
still completing, which resets long uploads even though the server remains
healthy.

A seventh, distinctly named profile provides Karing/sing-box compatibility.
It is a separate VLESS/TCP/REALITY/Vision inbound on TCP 8446 with an explicit
REALITY minimum-client version of 1.8.1. It reuses the same subscription but
has independent client and REALITY credentials, so the original profile stays
unchanged and retains Xray's newer default compatibility floor.

The Compose resource ceiling is 1.5 CPUs across both host CPUs, which is 75%
of this two-CPU VPS. Memory and PID limits remain unchanged.

## Plain-HTTP clearnet subscription endpoint

`clearnet-sub/` defines a separate, capability-addressed HTTP gateway for
3X-UI subscriptions. It uses the host's otherwise-idle Nginx,
binds only to the VPS public IPv4 address on TCP port 18081, and adds no TLS,
container or daemon. The external namespace contains an independent 256-bit
random value and deliberately does not expose 3X-UI's native route. Unknown
paths, query strings, encoded paths, weak/custom subscription identifiers, and
unsupported methods all receive a generic `404` response. Access logging is
disabled, secret-bearing upstream headers are removed, buffering and caching
are disabled, and small per-address/global request limits protect the listener
from blind enumeration.

The gateway is paired with 3X-UI's three publication URI settings. 3X-UI
appends each generated subscription identifier to the managed raw, JSON, or
Clash base. New normal 16-character IDs and backend-generated UUIDv4 IDs work
without another Nginx edit. Adding, removing, or changing profiles under an
existing ID appears on the next refresh because every request is generated
live by 3X-UI. JSON and Clash routes are pre-wired but stay unavailable until
their existing 3X-UI enable switches are deliberately turned on and the panel
is restarted. The original exact HTTP address remains a permanent alias for
the subscription that existed when pairing was installed.

3X-UI supports only one advertised URI per format, so its copy and QR actions
show the clearnet gateway after pairing. The older Tailscale Funnel HTTPS route
continues to work, but 3X-UI cannot advertise both raw addresses at once. The
native `subPath`, `subJsonPath`, and `subClashPath` remain unchanged so the
existing HTTPS route and the gateway share the same live generators.

The rendered Nginx configuration and capability stay root-only on the host.
Repository files contain placeholders only. The installer preserves an existing
capability unless `--rotate` is explicitly supplied, verifies that all enabled
profiles in the legacy subscription are present, compares both public routes
to the loopback feed, and rolls back Nginx plus the three publication settings
if validation fails. Before the first settings update it makes a consistent,
integrity-checked, root-only SQLite backup. Nginx is capped at 10% of one CPU,
64 MiB RAM, and 32 tasks; its observed idle footprint is much lower.

This endpoint intentionally has **no transport encryption or server
authentication**. The random path prevents practical blind guessing, but it
does not protect against a train Wi-Fi provider, ISP, or other on-path observer:
they can read and replay the path, read every profile, and modify the response.
Use the existing Tailscale Funnel HTTPS subscription whenever confidentiality
or integrity matters.

Install by copying the two safe template files to a temporary host location,
then run `tools/arbuzas/configure_tiny_vless_clearnet_sub.py` as root with
`--template` and `--limits`. The helper emits only fixed validation fields; it
never prints either subscription address, either token, or profile content.

The Hysteria2 and HTTP/3 profiles use a locally generated, certificate-pinned
self-signed certificate. A client must honor the pin carried by the share
profile; do not disable certificate verification as a workaround.

Hysteria2 must remain on UDP `8447`. TCP `443` continues to serve the separate
HTTP/2 recovery profile, but UDP `443` is intentionally unpublished and must
have no host listener. Do not add port hopping. A port migration must update
the 3X-UI inbound and Docker publication together, preserve the existing
client authentication, certificate pin, SNI, subscription identity, and
display name, and prove the previous UDP endpoint is closed. Clients must
refresh the existing subscription after such a migration.

Run the guarded server-side mutation from this checkout by streaming
`tools/arbuzas/configure_tiny_vless_mobility.py` to root on kitty-gration. The
script prints only sanitized counts and preservation hashes; it never prints
profile links or credentials.

`tools/arbuzas/configure_tiny_vless_karing_compat.py` stages the Karing profile
disabled, verifies the dedicated Docker port, then enables it only after the
runtime configuration contains the explicit compatibility floor. It applies
the same original-profile fingerprint guards and secret-free output policy.

`tools/arbuzas/test_tiny_vless_mobility.py` performs secret-safe client-side
checks with an Xray binary matching the server. The seven-profile suite checks
the original profile, the five mobility profiles, and the Karing compatibility
profile through real tunnels. Passing `--karing-core` adds the decisive check
through Karing's modified sing-box core, which must pass both configuration
validation and a live tunnel without a REALITY verification failure.

Supply private links only through stdin or a file with mode `0600`; never put a
subscription URL or share link on the command line. A complete acceptance run
requires every Xray configuration and tunnel check, the Karing configuration
and tunnel check, and the final suite result to pass. The script writes only
fixed profile labels and pass/fail results. It can also simulate a changed UDP
source port during a live stream. Treat that simulation as a laboratory check;
the final mobility acceptance test is still the real iPhone on train Wi-Fi
while the train changes providers.

## Recovery and rollback

Identity preservation is the default recovery policy. Before a
state-sensitive targeted rollout, create a consistent SQLite backup and retain
the matched mirrored environment and secret set. If the rollout fails, restore
that matched set together with the previous project and host-integration
state, then validate before returning the VPN to use.

For Hysteria recovery, the database inbound port and Compose UDP publication
are one matched state. Never restore only one side. The current matched state
is UDP `8447`; a rollback to an older recovery copy must restore both the old
database port and its matching Compose mapping before reconnect testing.

For a move to another VPS, restore the same database, environment,
certificate/key material, and capability before the targeted deployment
recreates the external project. The deployment must then restore the Nginx
publication, Tailscale routes, firewall, and limiter without disturbing other
host routes or services. This keeps existing subscriptions, clients, profile
credentials, and certificate pins intact.

A fresh reroll is not a restore. It is appropriate only when deliberately
abandoning the previous VPN identity, and requires explicit approval plus a
client migration plan.

## Verification checklist

Do not report a deployment or restore as complete until all of these checks
pass with secret-free output:

- the separate Compose project is healthy and stable, with its expected
  resource limits;
- the SQLite database passes integrity checks and contains the expected
  enabled protocol classes and counts;
- the original profile remains present and a real tunnel check still passes;
- each intended additional profile appears once and its supported tunnel check
  passes;
- the private subscription generator and public port-18081 alias return matching
  content for the private capability;
- unknown public paths, query strings, and unsupported methods remain generic
  failures;
- the private panel and intended TCP/UDP listeners are reachable only in their
  documented scopes;
- unrelated Tailscale Serve/Funnel routes are byte-for-byte unchanged;
- the firewall rules are active and will return after reboot;
- the traffic limiter is attached to the current container interface, not a
  stale interface from an earlier container; and
- the local mirror audits clean after the host has been reconciled.

Never print subscription addresses, profile links, UUIDs, keys, capabilities,
database rows, or private configuration values while running these checks.
