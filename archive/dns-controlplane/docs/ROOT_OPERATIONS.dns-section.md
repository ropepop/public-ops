# ROOT_OPERATIONS DNS Section (archived 2026-06-21)

- DNS public surface: DoH on `https://dns.jolkins.id.lv/dns-query` and DoT on `dns.jolkins.id.lv:853`
- DNS admin surface: private only on `http://<arbuzas-tailnet-dns-name>/`, `http://kitty-gration/` when MagicDNS short names are enabled, and the fallback paths `http://127.0.0.1:8097`, `http://<arbuzas-lan-ip>:8097`, and `http://<arbuzas-tailnet-ip>:8097/login`
- DNS config and TLS: `/etc/arbuzas/dns`
Then edit the plaintext tracked files under `infra/arbuzas/host-mirror/`. The mirror covers `/etc/arbuzas/env/**`, `/etc/arbuzas/secrets/**`, `/etc/arbuzas/cloudflared/**`, DNS config/TLS/secrets under `/etc/arbuzas/dns`, and `/etc/arbuzas/current/release.env`.
For the kitty-gration DNS admin UI, use the bare Tailscale HTTP root URL first: `http://<arbuzas-tailnet-dns-name>/`.
If MagicDNS short names are enabled on the client, `http://kitty-gration/` should also work.
Keep `http://<arbuzas-tailnet-ip>:8097/login` as the fallback/debug path.
- `dns_controlplane`
- DNS public HTTPS exposes only DoH, DNS public DoT responds, and the DNS admin UI stays private on loopback, LAN, and Tailscale.
