# KITTY_GRATION_DOCKER_PORTAINER DNS Section (archived 2026-06-21)

- Active DNS Rust workspace: `tools/arbuzas-rs/`
5. Make sure kitty-gration has `nginx` installed and running for the bare private DNS admin URL.
   - `/etc/arbuzas/dns/runtime.env`
   - `/etc/arbuzas/dns/arbuzas-dns.yaml`
   - `/etc/arbuzas/dns/tls/fullchain.pem`
   - `/etc/arbuzas/dns/tls/privkey.pem`
8. DNS on kitty-gration binds directly to host ports `443` and `853`.
./tools/arbuzas/deploy.sh deploy --services dns_controlplane --ssh-host kitty-gration --ssh-user "$USER"
./tools/arbuzas/deploy.sh validate --services dns_controlplane --ssh-host kitty-gration --ssh-user "$USER"
Run one live DNS observability database compaction pass without deploying:
- validates Portainer, apps, tunnels, and DNS
- confirms native kitty-gration DNS on `443/853` both on the host itself and from the public endpoint
- DNS state

## DNS DB Compaction (full section)

## DNS DB Compaction

The active kitty-gration `dns_controlplane` service owns DNS state compaction.

- Primary state store: `/srv/arbuzas/dns/state/controlplane.sqlite`
- Compatibility observability store: `/srv/arbuzas/dns/state/identity-observability.sqlite`
- Public identity surface and policy sync now run inside the same native `dns_controlplane` service.

Manual operator command:

```bash
./tools/arbuzas/deploy.sh compact-dns-db --ssh-host kitty-gration --ssh-user "$USER"
```

Expected result:

- The command prints a JSON result from the live `dns_controlplane` container.
- `controlplane.status` is normally `compacted`.
- `legacyObservability.status` is normally `compacted` when the compatibility observability file exists.
- The `beforeBytes`, `afterBytes`, and `reclaimedBytes` values show how much space was recovered.

Post-run checks:

- `./tools/arbuzas/deploy.sh validate --services dns_controlplane --ssh-host kitty-gration --ssh-user "$USER"`
- confirm `dns_controlplane` stays healthy
- confirm public `https://dns.jolkins.id.lv/login` returns `404`
- confirm the private admin UI still loads on `http://<arbuzas-tailnet-dns-name>/`
- confirm the short MagicDNS host works too at `http://kitty-gration/` when the operator machine has MagicDNS enabled
- confirm the raw private admin port still loads on `http://<arbuzas-tailnet-ip>:8097/login`
- confirm the improved query log and usage pages still load normally through the private admin port

- kitty-gration keeps the native DNS controlplane directly on `443/853`.
