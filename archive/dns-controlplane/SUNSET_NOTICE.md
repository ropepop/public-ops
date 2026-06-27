# Public Sunset Notice — `dns.jolkins.id.lv`

`dns.jolkins.id.lv` was retired on 2026-06-21. Encrypted DNS over HTTPS (`/dns-query`) and DNS over TLS (`:853`) on that hostname no longer answer.

If your device or browser is configured to use it, point it at a public resolver:

- **Cloudflare `1.1.1.1`** — DoH `https://cloudflare-dns.com/dns-query`, DoT `tls://1.1.1.1`, plain `1.1.1.1` / `1.0.0.1`
- **Quad9 `9.9.9.9`** — DoT `tls://9.9.9.9`, plain `9.9.9.9` / `149.112.112.112`
- **Your operator's resolver** — typically distributed via DHCP or your mobile carrier's settings profile

The Kitty-gration production stack and the other Jolkins ID public services are unaffected.
