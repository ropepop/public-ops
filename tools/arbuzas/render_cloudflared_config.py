#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from pathlib import Path


def load_tunnel_id(path: Path) -> str:
    payload = json.loads(path.read_text(encoding="utf-8"))
    tunnel_id = str(payload.get("TunnelID") or payload.get("tunnelID") or "").strip()
    if not tunnel_id:
        raise SystemExit(f"missing TunnelID in {path}")
    return tunnel_id


def render_config(tunnel_id: str, hostname: str, upstream: str) -> str:
    return (
        f"tunnel: {tunnel_id}\n"
        "credentials-file: /run/arbuzas/cloudflared/credentials.json\n"
        "ingress:\n"
        f"  - hostname: {hostname}\n"
        f"    service: {upstream}\n"
        "  - service: http_status:404\n"
    )


def main() -> None:
    parser = argparse.ArgumentParser(description="Render a Cloudflare tunnel config for the Arbuzas Docker layout.")
    parser.add_argument("--credentials-file", required=True)
    parser.add_argument("--hostname", required=True)
    parser.add_argument("--upstream", required=True)
    parser.add_argument("--out", required=True)
    args = parser.parse_args()

    credentials_path = Path(args.credentials_file).expanduser().resolve()
    output_path = Path(args.out).expanduser().resolve()
    tunnel_id = load_tunnel_id(credentials_path)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(render_config(tunnel_id, args.hostname.strip(), args.upstream.strip()), encoding="utf-8")


if __name__ == "__main__":
    main()
