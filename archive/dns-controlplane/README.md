# DNS Controlplane — Archived 2026-06-21

This directory holds the materials removed when the `dns_controlplane` service on kitty-gration was sunset. Re-enable is a documented revert; everything needed to bring the service back is here or on the live host under a `.retired-<timestamp>` name.

## Original runtime

- **Service name:** `dns_controlplane` (Compose) → image `arbuzas/dns-controlplane:<release-id>`
- **Source:** `tools/arbuzas-rs/` — native Rust workspace, two crates
- **Image:** `infra/arbuzas/docker/images/dns-controlplane.Dockerfile` (two-stage `rust:1.94-bookworm` + `debian:bookworm-slim`)
- **Public surface:** DoH on `https://dns.jolkins.id.lv/dns-query` and DoT on `dns.jolkins.id.lv:853` (host ports `443`, `853`)
- **Private admin:** `http://<arbuzas-tailnet-dns-name>/` (and the bare `/login` on `8097`)
- **Legacy identity surface:** `/pixel-stack/identity*` — also retired with the controlplane
- **State:** `/srv/arbuzas/dns/` (sqlite state, runtime, logs, run), config under `/etc/arbuzas/dns/`
- **TLS:** `/etc/arbuzas/dns/tls/{fullchain,privkey}.pem`
- **Host `nginx` site:** `/etc/nginx/sites-{available,enabled}/arbuzas-dns-admin`
- **Tailscale forward:** TCP `8097` → `127.0.0.1:8097`

## What is here

```
archive/dns-controlplane/
  README.md                                  # this file
  SUNSET_NOTICE.md                           # public-facing notice
  tools/
    arbuzas-rs/                              # full Rust workspace (Cargo.toml, Cargo.lock, crates/*, identity_assets/)
  docker/
    dns-controlplane.Dockerfile              # archived image recipe
  compose-snippet/
    dns_controlplane.compose.fragment.yml    # the deleted service block, for re-add reference
  nginx/
    arbuzas-dns-admin.conf.template          # archived nginx admin site
  env/
    arbuzas.example.env.dns-section.txt      # the five removed env vars + identity-compaction trio
  infra/
    adguardhome/                             # archived legacy AdGuard Home module template
  docs/
    MODULE_ADGUARDHOME.md                    # archived active runbook
    APPLE_ENCRYPTED_DNS_PROFILES.md          # archived subscriber reference
    KITTY_GRATION_DOCKER_PORTAINER.dns-section.md  # the DNS paragraphs of the live runbook, for revert
    ROOT_OPERATIONS.dns-section.md           # the DNS paragraphs of root operations, for revert
  tests/
    test_arbuzas_dns_*.sh                    # all nine DNS-specific tests
    test_prepare_arbuzas_adguardhome_config.sh  # legacy module prep test
  deploy/
    deploy.sh.dns-stripped.patch             # git diff of the deploy.sh strip; re-enable applies the reverse
    host_mirror.py.dns-stripped.patch        # git diff of the host_mirror.py strip
```

The `deploy.sh.dns-stripped.patch` and `host_mirror.py.dns-stripped.patch` are produced during re-enable by running `git diff` against the current working tree; if those files are empty after sunset, the in-place edits were not committed and re-enable is just a copy from this archive plus a re-apply of the same edits.

Secrets are **not** archived here. The `doh-identities.json`, `secrets/admin-password`, `secrets/cloudflare-token`, `secrets/github-policy-repo*`, and `secrets/ipinfo-lite-token` files stay in the local host mirror (`infra/arbuzas/host-mirror/etc/arbuzas/dns/`) and on the live host under `/etc/arbuzas/dns.retired-<timestamp>/`. Re-enable pulls them from one of those locations, not from this archive.

## Re-enable recipe

Apply in order from this archive:

1. Restore `tools/arbuzas-rs/` from `archive/dns-controlplane/tools/`.
2. Reverse `deploy.sh.dns-stripped.patch` (`git apply -R archive/dns-controlplane/deploy/deploy.sh.dns-stripped.patch`) and `host_mirror.py.dns-stripped.patch` to bring back the DNS deploy hooks.
3. Copy `archive/dns-controlplane/docker/dns-controlplane.Dockerfile` back to `infra/arbuzas/docker/images/dns-controlplane.Dockerfile`.
4. Copy `archive/dns-controlplane/nginx/arbuzas-dns-admin.conf.template` back to `infra/arbuzas/nginx/arbuzas-dns-admin.conf.template`.
5. Copy `archive/dns-controlplane/compose-snippet/dns_controlplane.compose.fragment.yml` back into `infra/arbuzas/docker/compose.yml` (insert before the `dns_controlplane` comment if a marker was left; otherwise append after the last surviving service block).
6. Re-apply the env vars from `archive/dns-controlplane/env/arbuzas.example.env.dns-section.txt` to `infra/arbuzas/docker/env/arbuzas.example.env`.
7. Restore DNS files under `infra/arbuzas/host-mirror/etc/arbuzas/dns/` from either:
   - the host backup at `/etc/arbuzas/dns.retired-<UTC-timestamp>/` (preferred — preserves live identities and admin password), or
   - the local mirror backup if it was kept around after sunset.
8. Run `./tools/arbuzas/deploy.sh mirror-pull --ssh-host kitty-gration --ssh-user ropepop` to refresh the host mirror manifest, then `mirror-audit` to confirm the DNS files are tracked again.
9. Restore `/srv/arbuzas/dns/` from `/srv/arbuzas/dns.retired-<UTC-timestamp>/` on the live host.
10. Re-create the public Cloudflare `dns.jolkins.id.lv` A/AAAA record (user action in Cloudflare — this archive does not have Cloudflare credentials).
11. Redeploy with `./tools/arbuzas/deploy.sh deploy --ssh-host kitty-gration --ssh-user ropepop`. The DNS release-prepare path will rebuild the Rust controlplane, run migrations, sync the policy, recreate the `nginx` site, and re-publish the Tailscale `serve` forward.
12. Validate the public surface with `./tools/arbuzas/deploy.sh validate --services dns_controlplane --ssh-host kitty-gration --ssh-user ropepop`. This re-enables the public DoH/DoT checks that were removed at sunset.
13. Re-run the archived DNS tests under `archive/dns-controlplane/tests/` to confirm the re-enabled service passes the same contract the original service did.

## Notes

- `archive/dns-controlplane/infra/adguardhome/` is the original external AdGuard Home module template (`module_id: adguardhome`, `runtime: external`). The active cutover never used this module; the Rust controlplane replaced it before sunset. Bringing AdGuard Home back specifically (instead of the Rust controlplane) means installing AdGuard Home on the host manually and pointing the module at it, which is **out of scope** for the active re-enable. The module template is archived here only for archaeology.
- `archive/dns-controlplane/docs/APPLE_ENCRYPTED_DNS_PROFILES.md` is kept as historical context. The encrypted-DNS profiles it describes stopped working when `dns.jolkins.id.lv` stopped answering. Do not redistribute those profiles after re-enable without updating the public hostname and the cert.
- Re-enable does not require restoring the `frontend` and `adguardhome` defensive `compose stop` names. The deploy script keeps the `stop frontend` defensive stop; the `dns_controlplane` and `adguardhome` parts were the only things removed from that line.
- The post-sunset cleanup reuses the seven-day Docker GC retention policy, so re-enabling within seven days of the original teardown does not require a fresh `cargo` dependency cache build — the image build cache is still on the host.
