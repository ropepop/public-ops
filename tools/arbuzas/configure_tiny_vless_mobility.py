#!/usr/bin/env python3
"""Add the kitty-gration mobility profiles without modifying the original client.

This script is intended to run as root on kitty-gration. It uses the live
3X-UI HTTP API for mutations, but reads the SQLite database in read-only mode
to enforce preservation guards before and after the change. It never prints
client credentials, subscription IDs, keys, certificates, or share links.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import sqlite3
import subprocess
import sys
import uuid

import requests


STACK_DIR = Path("/opt/tiny-vless")
DB_PATH = STACK_DIR / "db" / "x-ui.db"
ENV_PATH = STACK_DIR / ".env"
CERT_PATH = STACK_DIR / "cert" / "mobility-fullchain.pem"
KEY_PATH = STACK_DIR / "cert" / "mobility-privkey.pem"
CONTAINER = "tiny-vless-3xui"
PANEL_BASE = "http://127.0.0.1:12053"
XRAY = "/app/bin/xray-linux-amd64"
TLS_NAME = "mobility.kitty-gration"
HYSTERIA_PORT = 8447

TARGETS = (
    ("mobility-hy2", "Kitty Mobility - Hysteria2", "hysteria", HYSTERIA_PORT),
    ("mobility-h3", "Kitty Mobility - VLESS XHTTP H3", "vless", 8444),
    ("mobility-wg", "Kitty Mobility - WireGuard", "wireguard", 51820),
    ("mobility-h2", "Kitty Mobility - XHTTP H2 Recovery", "vless", 443),
    ("mobility-mkcp", "Kitty Mobility - VMess mKCP", "vmess", 8445),
)


class ApplyError(RuntimeError):
    pass


def load_env(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip().strip('"').strip("'")
    return values


def db_connect() -> sqlite3.Connection:
    con = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    con.row_factory = sqlite3.Row
    return con


def row_digest(row: sqlite3.Row | dict, *, omit: tuple[str, ...] = ()) -> str:
    obj = {k: row[k] for k in row.keys() if k not in omit}
    raw = json.dumps(obj, sort_keys=True, separators=(",", ":"), default=str).encode()
    return hashlib.sha256(raw).hexdigest()


def original_state() -> dict[str, object]:
    with db_connect() as con:
        inbound = con.execute(
            "SELECT * FROM inbounds WHERE protocol = ? AND port = ?",
            ("vless", 8443),
        ).fetchone()
        if inbound is None:
            raise ApplyError("the protected VLESS/TCP inbound was not found")
        settings = json.loads(inbound["settings"] or "{}")
        clients = settings.get("clients") or []
        if len(clients) != 1:
            raise ApplyError("the protected inbound no longer has exactly one client")
        email = clients[0].get("email", "")
        if not email:
            raise ApplyError("the protected client email is missing")
        client = con.execute("SELECT * FROM clients WHERE email = ?", (email,)).fetchone()
        if client is None:
            raise ApplyError("the protected normalized client row was not found")
        attachment = con.execute(
            "SELECT * FROM client_inbounds WHERE client_id = ? AND inbound_id = ?",
            (client["id"], inbound["id"]),
        ).fetchone()
        if attachment is None:
            raise ApplyError("the protected client attachment was not found")
        return {
            "email": email,
            "sub_id": client["sub_id"],
            "share_addr": inbound["share_addr"],
            "client_hash": row_digest(client),
            "inbound_hash": row_digest(inbound, omit=("up", "down")),
            "attachment_hash": row_digest(attachment),
            "inbound_id": inbound["id"],
        }


def assert_original_unchanged(before: dict[str, object]) -> None:
    after = original_state()
    for key in ("email", "sub_id", "client_hash", "inbound_hash", "attachment_hash", "inbound_id"):
        if after[key] != before[key]:
            raise ApplyError(f"protected original changed at guard {key}")


def run_capture(args: list[str], *, input_data: bytes | None = None) -> bytes:
    proc = subprocess.run(args, input=input_data, capture_output=True, check=False)
    if proc.returncode != 0:
        raise ApplyError(f"helper command failed: {Path(args[0]).name}")
    return proc.stdout


def xray_keypair(command: str) -> tuple[str, str]:
    output = run_capture(["docker", "exec", CONTAINER, XRAY, command]).decode()
    private = ""
    public = ""
    for line in output.splitlines():
        if line.startswith("PrivateKey:"):
            private = line.split(":", 1)[1].strip()
        elif line.startswith("Password (PublicKey):"):
            public = line.split(":", 1)[1].strip()
    if not private or not public:
        raise ApplyError(f"could not generate the {command} keypair")
    return private, public


def ensure_certificate(public_ip: str) -> str:
    CERT_PATH.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if not CERT_PATH.exists() or not KEY_PATH.exists():
        run_capture(
            [
                "openssl", "req", "-x509", "-newkey", "ec",
                "-pkeyopt", "ec_paramgen_curve:prime256v1",
                "-sha256", "-nodes", "-days", "825",
                "-subj", f"/CN={TLS_NAME}",
                "-addext", f"subjectAltName=DNS:{TLS_NAME},IP:{public_ip}",
                "-keyout", str(KEY_PATH), "-out", str(CERT_PATH),
            ]
        )
        os.chmod(KEY_PATH, 0o600)
        os.chmod(CERT_PATH, 0o644)
    run_capture(["openssl", "x509", "-in", str(CERT_PATH), "-noout", "-checkend", "31536000"])
    cert_der = run_capture(["openssl", "x509", "-in", str(CERT_PATH), "-outform", "DER"])
    return base64.b64encode(hashlib.sha256(cert_der).digest()).decode()


class PanelAPI:
    def __init__(self, env: dict[str, str]):
        self.session = requests.Session()
        self.csrf = self._csrf()
        response = self.session.post(
            PANEL_BASE + "/login",
            json={
                "username": env.get("XUI_USERNAME", ""),
                "password": env.get("XUI_PASSWORD", ""),
                "twoFactorCode": "",
            },
            headers={"X-CSRF-Token": self.csrf},
            timeout=10,
        )
        body = self._json(response, "/login")
        if not body.get("success"):
            raise ApplyError("3X-UI login failed")
        self.csrf = self._csrf()

    def _json(self, response: requests.Response, path: str) -> dict:
        if response.status_code != 200:
            raise ApplyError(f"3X-UI {path} returned HTTP {response.status_code}")
        try:
            return response.json()
        except ValueError as exc:
            raise ApplyError(f"3X-UI {path} returned invalid JSON") from exc

    def _csrf(self) -> str:
        response = self.session.get(PANEL_BASE + "/csrf-token", timeout=10)
        body = self._json(response, "/csrf-token")
        token = body.get("obj")
        if not body.get("success") or not isinstance(token, str) or not token:
            raise ApplyError("3X-UI did not issue a CSRF token")
        return token

    def get(self, path: str) -> object:
        response = self.session.get(PANEL_BASE + path, timeout=15)
        body = self._json(response, path)
        if not body.get("success"):
            raise ApplyError(f"3X-UI GET {path} failed")
        return body.get("obj")

    def post(self, path: str, payload: object | None = None) -> object:
        response = self.session.post(
            PANEL_BASE + path,
            json=payload,
            headers={"X-CSRF-Token": self.csrf},
            timeout=20,
        )
        body = self._json(response, path)
        if not body.get("success"):
            raise ApplyError(f"3X-UI POST {path} failed")
        return body.get("obj")


def common_client(email: str, sub_id: str, comment: str) -> dict[str, object]:
    return {
        "email": email,
        "subId": sub_id,
        "limitIp": 0,
        "totalGB": 0,
        "expiryTime": 0,
        "enable": True,
        "tgId": 0,
        "comment": comment,
        "reset": 0,
    }


def tls_settings(pin: str) -> dict[str, object]:
    return {
        "serverName": TLS_NAME,
        "minVersion": "1.3",
        "maxVersion": "1.3",
        "cipherSuites": "",
        "rejectUnknownSni": False,
        "disableSystemRoot": False,
        "enableSessionResumption": True,
        "certificates": [
            {
                "certificateFile": "/root/cert/mobility-fullchain.pem",
                "keyFile": "/root/cert/mobility-privkey.pem",
                "ocspStapling": 0,
                "oneTimeLoading": False,
                "usage": "encipherment",
                "buildChain": False,
            }
        ],
        "alpn": ["h3"],
        "echServerKeys": "",
        "settings": {
            "fingerprint": "chrome",
            "echConfigList": "",
            "pinnedPeerCertSha256": [pin],
            "verifyPeerCertByName": TLS_NAME,
        },
    }


def quic_tuning() -> dict[str, object]:
    return {
        "congestion": "bbr",
        "bbrProfile": "conservative",
        "maxIdleTimeout": 45,
        "keepAlivePeriod": 5,
        "disablePathMTUDiscovery": False,
    }


def xhttp_settings(
    path: str,
    *,
    xmux: bool = False,
    stable_packet_up: bool = False,
) -> dict[str, object]:
    out: dict[str, object] = {
        "path": path,
        "host": "",
        "mode": "auto",
        "xPaddingBytes": "100-1000",
        "xPaddingObfsMode": False,
        "scMaxBufferedPosts": 30,
        "scStreamUpServerSecs": "20-80",
        "serverMaxHeaderBytes": 0,
        "headers": {},
    }
    if stable_packet_up:
        # Xray 26.7.11's randomized 600-900 request lifetime can rotate an
        # H3 packet-up client while POST responses are still in flight. Keep
        # the connection pool bounded, but do not rotate it by request count.
        out["xmux"] = {
            "maxConcurrency": 0,
            "maxConnections": 6,
            "cMaxReuseTimes": 0,
            "hMaxRequestTimes": 0,
            "hMaxReusableSecs": 0,
            "hKeepAlivePeriod": 0,
        }
    elif xmux:
        out["xmux"] = {
            "maxConcurrency": "16-32",
            "maxConnections": 0,
            "cMaxReuseTimes": 0,
            "hMaxRequestTimes": "600-900",
            "hMaxReusableSecs": "90-180",
            "hKeepAlivePeriod": 10,
        }
    return out


def build_payloads(state: dict[str, object], pin: str) -> list[dict[str, object]]:
    sub_id = str(state["sub_id"])
    share_addr = str(state["share_addr"])
    if not share_addr:
        raise ApplyError("the protected inbound has no public share address")

    wg_server_private, _ = xray_keypair("wg")
    wg_client_private, wg_client_public = xray_keypair("wg")
    reality_private, reality_public = xray_keypair("x25519")
    wg_psk = base64.b64encode(secrets.token_bytes(32)).decode()

    common_top = {
        "up": 0,
        "down": 0,
        "total": 0,
        "enable": True,
        "expiryTime": 0,
        "trafficReset": "never",
        "lastTrafficResetTime": 0,
        "listen": "",
        "tag": "",
        "shareAddrStrategy": "custom",
        "shareAddr": share_addr,
    }
    disabled_sniffing = {"enabled": False}
    enabled_sniffing = {
        "enabled": True,
        "destOverride": ["http", "tls", "quic", "fakedns"],
        "metadataOnly": False,
        "routeOnly": False,
        "ipsExcluded": [],
        "domainsExcluded": [],
    }

    hy_client = common_client("mobility-hy2", sub_id, "QUIC roaming experiment")
    hy_client["auth"] = secrets.token_urlsafe(24)
    h3_client = common_client("mobility-h3", sub_id, "VLESS HTTP/3 roaming experiment")
    h3_client.update({"id": str(uuid.uuid4()), "flow": ""})
    wg_client = common_client("mobility-wg", sub_id, "WireGuard endpoint roaming experiment")
    wg_client.update({
        "id": str(uuid.uuid4()),
        "privateKey": wg_client_private,
        "publicKey": wg_client_public,
        "preSharedKey": wg_psk,
        "allowedIPs": ["10.77.0.2/32"],
        "keepAlive": 15,
    })
    h2_client = common_client("mobility-h2", sub_id, "TCP fast-recovery control")
    h2_client.update({"id": str(uuid.uuid4()), "flow": ""})
    mkcp_client = common_client("mobility-mkcp", sub_id, "Loss-tolerance control")
    mkcp_client.update({"id": str(uuid.uuid4()), "security": "auto"})

    tls = tls_settings(pin)
    h3_path = "/" + secrets.token_urlsafe(12)
    h2_path = "/" + secrets.token_urlsafe(12)
    short_id = secrets.token_hex(8)

    payloads: list[dict[str, object]] = []

    payloads.append({
        **common_top,
        "remark": "Kitty Mobility - Hysteria2",
        "subSortIndex": 2,
        "port": HYSTERIA_PORT,
        "protocol": "hysteria",
        "settings": json.dumps({"version": 2, "clients": [hy_client]}),
        "streamSettings": json.dumps({
            "network": "hysteria",
            "security": "tls",
            "hysteriaSettings": {"version": 2, "udpIdleTimeout": 60},
            "tlsSettings": tls,
            "finalmask": {"quicParams": quic_tuning()},
        }),
        "sniffing": json.dumps(disabled_sniffing),
    })

    payloads.append({
        **common_top,
        "remark": "Kitty Mobility - VLESS XHTTP H3",
        "subSortIndex": 3,
        "port": 8444,
        "protocol": "vless",
        "settings": json.dumps({
            "clients": [h3_client], "decryption": "none", "encryption": "none", "fallbacks": [],
        }),
        "streamSettings": json.dumps({
            "network": "xhttp",
            "security": "tls",
            "xhttpSettings": xhttp_settings(h3_path, stable_packet_up=True),
            "tlsSettings": tls,
            "finalmask": {"quicParams": quic_tuning()},
        }),
        "sniffing": json.dumps(enabled_sniffing),
    })

    payloads.append({
        **common_top,
        "remark": "Kitty Mobility - WireGuard",
        "subSortIndex": 4,
        "port": 51820,
        "protocol": "wireguard",
        "settings": json.dumps({
            "mtu": 1280,
            "secretKey": wg_server_private,
            "dns": "1.1.1.1, 1.0.0.1",
            "peers": [],
            "clients": [wg_client],
            "noKernelTun": False,
            "domainStrategy": "ForceIPv4v6",
        }),
        "streamSettings": json.dumps({"security": "none"}),
        "sniffing": json.dumps(disabled_sniffing),
    })

    payloads.append({
        **common_top,
        "remark": "Kitty Mobility - XHTTP H2 Recovery",
        "subSortIndex": 5,
        "port": 443,
        "protocol": "vless",
        "settings": json.dumps({
            "clients": [h2_client], "decryption": "none", "encryption": "none", "fallbacks": [],
        }),
        "streamSettings": json.dumps({
            "network": "xhttp",
            "security": "reality",
            "xhttpSettings": xhttp_settings(h2_path, xmux=True),
            "realitySettings": {
                "show": False,
                "xver": 0,
                "target": "www.cloudflare.com:443",
                "serverNames": ["www.cloudflare.com"],
                "privateKey": reality_private,
                "minClientVer": "",
                "maxClientVer": "",
                "maxTimediff": 0,
                "shortIds": [short_id],
                "mldsa65Seed": "",
                "settings": {
                    "publicKey": reality_public,
                    "fingerprint": "chrome",
                    "serverName": "www.cloudflare.com",
                    "spiderX": "/",
                    "mldsa65Verify": "",
                },
            },
            "sockopt": {
                "tcpKeepAliveIdle": 30,
                "tcpKeepAliveInterval": 10,
                "tcpUserTimeout": 30000,
            },
        }),
        "sniffing": json.dumps(enabled_sniffing),
    })

    payloads.append({
        **common_top,
        "remark": "Kitty Mobility - VMess mKCP",
        "subSortIndex": 6,
        "port": 8445,
        "protocol": "vmess",
        "settings": json.dumps({"clients": [mkcp_client]}),
        "streamSettings": json.dumps({
            "network": "kcp",
            "security": "none",
            "kcpSettings": {
                "mtu": 1200,
                "tti": 20,
                "uplinkCapacity": 20,
                "downlinkCapacity": 100,
                "cwndMultiplier": 1,
                "maxSendingWindow": 2097152,
            },
        }),
        "sniffing": json.dumps(enabled_sniffing),
    })

    return payloads


def existing_target_rows() -> list[sqlite3.Row]:
    remarks = tuple(item[1] for item in TARGETS)
    placeholders = ",".join("?" for _ in remarks)
    with db_connect() as con:
        return list(con.execute(f"SELECT id, remark FROM inbounds WHERE remark IN ({placeholders})", remarks))


def add_profiles(api: PanelAPI, state: dict[str, object], pin: str) -> list[int]:
    if existing_target_rows():
        raise ApplyError("one or more mobility profile names already exist; refusing a partial overwrite")
    created: list[tuple[int, str]] = []
    try:
        for payload in build_payloads(state, pin):
            obj = api.post("/panel/api/inbounds/add", payload)
            if not isinstance(obj, dict):
                raise ApplyError("3X-UI did not return the created inbound")
            inbound_id = obj.get("id") or obj.get("Id")
            if not isinstance(inbound_id, int) or inbound_id <= 0:
                raise ApplyError("3X-UI did not return a valid inbound ID")
            settings = json.loads(str(payload["settings"]))
            email = str(settings["clients"][0]["email"])
            created.append((inbound_id, email))
        return [item[0] for item in created]
    except Exception:
        for inbound_id, email in reversed(created):
            try:
                api.post("/panel/api/clients/del/" + requests.utils.quote(email, safe=""))
            except Exception:
                pass
            try:
                api.post(f"/panel/api/inbounds/del/{inbound_id}")
            except Exception:
                pass
        raise


def verify_database(before: dict[str, object]) -> dict[str, int]:
    assert_original_unchanged(before)
    with db_connect() as con:
        quick = con.execute("PRAGMA quick_check").fetchone()[0]
        if quick != "ok":
            raise ApplyError("SQLite quick_check failed")
        counts = {
            "clients": con.execute("SELECT count(*) FROM clients").fetchone()[0],
            "inbounds": con.execute("SELECT count(*) FROM inbounds").fetchone()[0],
            "attachments": con.execute("SELECT count(*) FROM client_inbounds").fetchone()[0],
        }
        if counts != {"clients": 6, "inbounds": 6, "attachments": 6}:
            raise ApplyError(f"unexpected normalized-row counts: {counts}")
        rows = con.execute(
            "SELECT protocol, port, enable, settings FROM inbounds WHERE remark LIKE 'Kitty Mobility - %' ORDER BY sub_sort_index"
        ).fetchall()
        if len(rows) != 5 or not all(bool(row["enable"]) for row in rows):
            raise ApplyError("not all five mobility inbounds are enabled")
        observed = sorted((str(row["protocol"]), int(row["port"])) for row in rows)
        expected = sorted((protocol, port) for _, _, protocol, port in TARGETS)
        if observed != expected:
            raise ApplyError("mobility protocol and port set is incomplete")
        for row in rows:
            clients = (json.loads(row["settings"] or "{}").get("clients") or [])
            if len(clients) != 1 or clients[0].get("subId") != before["sub_id"]:
                raise ApplyError("a mobility client is not attached to the protected subscription")
        return counts


def verify_subscription(api: PanelAPI, state: dict[str, object]) -> list[str]:
    sub_id = requests.utils.quote(str(state["sub_id"]), safe="")
    obj = api.get("/panel/api/clients/subLinks/" + sub_id)
    if not isinstance(obj, list):
        raise ApplyError("subscription link API did not return a list")
    links = [item for item in obj if isinstance(item, str) and item]
    schemes = sorted(link.split(":", 1)[0].lower() for link in links)
    expected = sorted(["vless", "hysteria2", "vless", "wireguard", "vless", "vmess"])
    if schemes != expected:
        raise ApplyError(f"subscription scheme set is incomplete: {schemes}")
    return schemes


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--apply", action="store_true", help="perform the additive change")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ApplyError("run as root on kitty-gration")
    if not DB_PATH.exists() or not ENV_PATH.exists():
        raise ApplyError("tiny-vless runtime files were not found")

    before = original_state()
    if not args.apply:
        print("preflight_ok=true")
        print("protected_client_hash=" + str(before["client_hash"]))
        print("protected_inbound_hash=" + str(before["inbound_hash"]))
        print("protected_attachment_hash=" + str(before["attachment_hash"]))
        print("existing_mobility_profiles=" + str(len(existing_target_rows())))
        return 0

    if existing_target_rows():
        raise ApplyError("mobility profiles already exist; use verification instead of reapplying")
    env = load_env(ENV_PATH)
    public_ip = str(before["share_addr"])
    if not re.fullmatch(r"(?:\d{1,3}\.){3}\d{1,3}", public_ip):
        raise ApplyError("the protected share address is not an IPv4 address")
    pin = ensure_certificate(public_ip)
    api = PanelAPI(env)
    created_ids = add_profiles(api, before, pin)
    counts = verify_database(before)
    schemes = verify_subscription(api, before)

    print("apply_ok=true")
    print("created_inbounds=" + str(len(created_ids)))
    print("subscription_profiles=" + str(len(schemes)))
    print("subscription_schemes=" + ",".join(schemes))
    print("normalized_counts=" + json.dumps(counts, sort_keys=True))
    print("protected_client_unchanged=true")
    print("protected_inbound_unchanged=true")
    print("protected_attachment_unchanged=true")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ApplyError as exc:
        print(f"error={exc}", file=sys.stderr)
        raise SystemExit(1)
