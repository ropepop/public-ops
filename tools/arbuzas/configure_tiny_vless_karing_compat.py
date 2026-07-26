#!/usr/bin/env python3
"""Stage, enable, and verify an additive Karing REALITY compatibility profile.

The script is intended to run as root on kitty-gration. It never prints share
links, subscription identifiers, client UUIDs, REALITY keys, short IDs, panel
credentials, or public endpoints. The original VLESS inbound/client is guarded
by complete normalized-row fingerprints before and after every mutation.
"""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import secrets
import sqlite3
import subprocess
from urllib.parse import parse_qs, quote, unquote, urlsplit

import requests


STACK_DIR = Path("/opt/tiny-vless")
DB_PATH = STACK_DIR / "db" / "x-ui.db"
ENV_PATH = STACK_DIR / ".env"
CONTAINER = "tiny-vless-3xui"
PANEL_BASE = "http://127.0.0.1:12053"
XRAY = "/app/bin/xray-linux-amd64"

REMARK = "Kitty Main - Karing Compatibility"
EMAIL = "owner-main-karing-compat"
PORT = 8446
MIN_CLIENT_VERSION = "1.8.1"


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


def row_digest(row: sqlite3.Row, *, omit: tuple[str, ...] = ()) -> str:
    value = {key: row[key] for key in row.keys() if key not in omit}
    raw = json.dumps(value, sort_keys=True, separators=(",", ":"), default=str).encode()
    return hashlib.sha256(raw).hexdigest()


def original_state() -> dict[str, object]:
    with db_connect() as con:
        inbound = con.execute(
            "SELECT * FROM inbounds WHERE protocol = ? AND port = ?",
            ("vless", 8443),
        ).fetchone()
        if inbound is None:
            raise ApplyError("the protected original inbound was not found")
        settings = json.loads(inbound["settings"] or "{}")
        clients = settings.get("clients") or []
        if len(clients) != 1 or not clients[0].get("email"):
            raise ApplyError("the protected original client shape is unexpected")
        client = con.execute(
            "SELECT * FROM clients WHERE email = ?", (clients[0]["email"],)
        ).fetchone()
        if client is None:
            raise ApplyError("the protected normalized client was not found")
        attachment = con.execute(
            "SELECT * FROM client_inbounds WHERE client_id = ? AND inbound_id = ?",
            (client["id"], inbound["id"]),
        ).fetchone()
        if attachment is None:
            raise ApplyError("the protected client attachment was not found")
        return {
            "sub_id": client["sub_id"],
            "share_addr": inbound["share_addr"],
            "client_hash": row_digest(client),
            "inbound_hash": row_digest(inbound, omit=("up", "down")),
            "attachment_hash": row_digest(attachment),
            "inbound_id": inbound["id"],
            "stream": json.loads(inbound["stream_settings"] or "{}"),
            "sniffing": inbound["sniffing"],
            "client_uuid": clients[0].get("id", ""),
            "reality_private_key": (
                json.loads(inbound["stream_settings"] or "{}")
                .get("realitySettings", {})
                .get("privateKey", "")
            ),
            "reality_short_ids": tuple(
                json.loads(inbound["stream_settings"] or "{}")
                .get("realitySettings", {})
                .get("shortIds", [])
            ),
        }


def assert_original_unchanged(before: dict[str, object]) -> None:
    after = original_state()
    for key in (
        "sub_id",
        "client_hash",
        "inbound_hash",
        "attachment_hash",
        "inbound_id",
    ):
        if after[key] != before[key]:
            raise ApplyError(f"protected original changed at guard {key}")


def run_capture(args: list[str]) -> str:
    process = subprocess.run(args, capture_output=True, text=True, check=False)
    if process.returncode != 0:
        raise ApplyError(f"helper command failed: {Path(args[0]).name}")
    return process.stdout


def xray_x25519() -> tuple[str, str]:
    output = run_capture(["docker", "exec", CONTAINER, XRAY, "x25519"])
    private = ""
    public = ""
    for line in output.splitlines():
        if line.startswith("PrivateKey:"):
            private = line.split(":", 1)[1].strip()
        elif line.startswith("Password (PublicKey):"):
            public = line.split(":", 1)[1].strip()
    if not private or not public:
        raise ApplyError("Xray did not generate a REALITY keypair")
    return private, public


def xray_uuid() -> str:
    value = run_capture(["docker", "exec", CONTAINER, XRAY, "uuid"]).strip()
    if not value:
        raise ApplyError("Xray did not generate a client UUID")
    return value


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

    @staticmethod
    def _json(response: requests.Response, path: str) -> dict:
        if response.status_code != 200:
            raise ApplyError(f"3X-UI {path} returned HTTP {response.status_code}")
        try:
            return response.json()
        except ValueError as exc:
            raise ApplyError(f"3X-UI {path} returned invalid JSON") from exc

    def _csrf(self) -> str:
        body = self._json(self.session.get(PANEL_BASE + "/csrf-token", timeout=10), "/csrf-token")
        token = body.get("obj")
        if not body.get("success") or not isinstance(token, str) or not token:
            raise ApplyError("3X-UI did not issue a CSRF token")
        return token

    def get(self, path: str) -> object:
        body = self._json(self.session.get(PANEL_BASE + path, timeout=15), path)
        if not body.get("success"):
            raise ApplyError(f"3X-UI GET {path} failed")
        return body.get("obj")

    def post(self, path: str, payload: object | None = None) -> object:
        body = self._json(
            self.session.post(
                PANEL_BASE + path,
                json=payload,
                headers={"X-CSRF-Token": self.csrf},
                timeout=20,
            ),
            path,
        )
        if not body.get("success"):
            raise ApplyError(f"3X-UI POST {path} failed")
        return body.get("obj")


def compatibility_row() -> sqlite3.Row | None:
    with db_connect() as con:
        return con.execute("SELECT * FROM inbounds WHERE remark = ?", (REMARK,)).fetchone()


def build_payload(before: dict[str, object]) -> dict[str, object]:
    source_stream = copy.deepcopy(before["stream"])
    if source_stream.get("network") != "tcp" or source_stream.get("security") != "reality":
        raise ApplyError("the protected original is no longer TCP/REALITY")
    source_reality = source_stream.get("realitySettings") or {}
    target = source_reality.get("target") or source_reality.get("dest")
    server_names = source_reality.get("serverNames") or []
    if not target or not server_names:
        raise ApplyError("the protected REALITY target metadata is incomplete")

    private_key, public_key = xray_x25519()
    short_id = secrets.token_hex(8)
    spider_path = "/" + secrets.token_urlsafe(12)
    original_client_settings = source_reality.get("settings") or {}
    server_name = original_client_settings.get("serverName") or server_names[0]
    fingerprint = original_client_settings.get("fingerprint") or "chrome"

    source_stream["realitySettings"] = {
        "show": False,
        "xver": int(source_reality.get("xver") or 0),
        "target": target,
        "serverNames": server_names,
        "privateKey": private_key,
        "minClientVer": MIN_CLIENT_VERSION,
        "maxClientVer": "",
        "maxTimediff": int(source_reality.get("maxTimediff") or 0),
        "shortIds": [short_id],
        "settings": {
            "publicKey": public_key,
            "fingerprint": fingerprint,
            "serverName": server_name,
            "spiderX": spider_path,
        },
    }

    client = {
        "id": xray_uuid(),
        "flow": "xtls-rprx-vision",
        "email": EMAIL,
        "subId": str(before["sub_id"]),
        "limitIp": 0,
        "totalGB": 0,
        "expiryTime": 0,
        "enable": True,
        "tgId": 0,
        "comment": "Karing/sing-box REALITY compatibility",
        "reset": 0,
    }
    settings = {
        "clients": [client],
        "decryption": "none",
        "encryption": "none",
        "fallbacks": [],
    }
    return {
        "up": 0,
        "down": 0,
        "total": 0,
        "remark": REMARK,
        "enable": False,
        "expiryTime": 0,
        "trafficReset": "never",
        "lastTrafficResetTime": 0,
        "listen": "",
        "port": PORT,
        "protocol": "vless",
        "settings": json.dumps(settings),
        "streamSettings": json.dumps(source_stream),
        "sniffing": str(before["sniffing"] or '{"enabled":false}'),
        "tag": "",
        "subSortIndex": 7,
        "shareAddrStrategy": "custom",
        "shareAddr": str(before["share_addr"]),
    }


def rollback_created(api: PanelAPI, inbound_id: int) -> None:
    try:
        api.post("/panel/api/clients/del/" + quote(EMAIL, safe="") + "?keepTraffic=0")
    except Exception:
        pass
    try:
        api.post(f"/panel/api/inbounds/del/{inbound_id}")
    except Exception:
        pass


def verify_database(before: dict[str, object], *, enabled: bool) -> sqlite3.Row:
    assert_original_unchanged(before)
    with db_connect() as con:
        if con.execute("PRAGMA quick_check").fetchone()[0] != "ok":
            raise ApplyError("SQLite quick_check failed")
        counts = {
            "clients": con.execute("SELECT count(*) FROM clients").fetchone()[0],
            "inbounds": con.execute("SELECT count(*) FROM inbounds").fetchone()[0],
            "attachments": con.execute("SELECT count(*) FROM client_inbounds").fetchone()[0],
        }
        if counts != {"clients": 7, "inbounds": 7, "attachments": 7}:
            raise ApplyError("the normalized row counts are unexpected")
        row = con.execute("SELECT * FROM inbounds WHERE remark = ?", (REMARK,)).fetchone()
        if row is None or bool(row["enable"]) != enabled or row["port"] != PORT:
            raise ApplyError("the compatibility inbound state is unexpected")
        stream = json.loads(row["stream_settings"] or "{}")
        reality = stream.get("realitySettings") or {}
        if reality.get("minClientVer") != MIN_CLIENT_VERSION:
            raise ApplyError("the Xray compatibility floor is missing")
        settings = json.loads(row["settings"] or "{}")
        clients = settings.get("clients") or []
        if len(clients) != 1 or clients[0].get("subId") != before["sub_id"]:
            raise ApplyError("the compatibility client is not on the protected subscription")
        normalized = con.execute("SELECT * FROM clients WHERE email = ?", (EMAIL,)).fetchone()
        if normalized is None or normalized["sub_id"] != before["sub_id"]:
            raise ApplyError("the normalized compatibility client is inconsistent")
        if (
            normalized["limit_ip"] != 0
            or normalized["total_gb"] != 0
            or normalized["expiry_time"] != 0
            or not bool(normalized["enable"])
        ):
            raise ApplyError("the compatibility client is not enabled and unlimited")
        if normalized["uuid"] == before["client_uuid"]:
            raise ApplyError("the compatibility client reused the protected UUID")
        attachments = con.execute(
            "SELECT count(*) FROM client_inbounds WHERE client_id = ? AND inbound_id = ?",
            (normalized["id"], row["id"]),
        ).fetchone()[0]
        if attachments != 1:
            raise ApplyError("the compatibility client attachment is inconsistent")
        if reality.get("privateKey") == before["reality_private_key"]:
            raise ApplyError("the compatibility inbound reused the protected REALITY key")
        if set(reality.get("shortIds") or []).intersection(before["reality_short_ids"]):
            raise ApplyError("the compatibility inbound reused a protected short ID")
        return row


def docker_port_is_published() -> bool:
    raw = run_capture(["docker", "inspect", CONTAINER])
    inspected = json.loads(raw)
    bindings = inspected[0].get("HostConfig", {}).get("PortBindings", {})
    values = bindings.get(f"{PORT}/tcp") or []
    return any(
        item.get("HostIp") == "0.0.0.0" and item.get("HostPort") == str(PORT)
        for item in values
    )


def assembled_config(api: PanelAPI) -> dict[str, object]:
    config = api.get("/panel/api/server/getConfigJson")
    if not isinstance(config, dict):
        raise ApplyError("3X-UI did not return its assembled Xray config")
    return config


def runtime_floor_is_active(config: dict[str, object]) -> bool:
    for inbound in config.get("inbounds", []):
        if inbound.get("protocol") == "vless" and inbound.get("port") == PORT:
            reality = (inbound.get("streamSettings") or {}).get("realitySettings") or {}
            return reality.get("minClientVer") == MIN_CLIENT_VERSION
    return False


def verify_subscription(api: PanelAPI, before: dict[str, object]) -> None:
    sub_id = quote(str(before["sub_id"]), safe="")
    obj = api.get("/panel/api/clients/subLinks/" + sub_id)
    if not isinstance(obj, list):
        raise ApplyError("subscription link API did not return a list")
    links = [item for item in obj if isinstance(item, str) and item]
    if len(links) != 7:
        raise ApplyError("the shared subscription does not contain seven profiles")
    matches = []
    for link in links:
        if not link.startswith("vless://"):
            continue
        parsed = urlsplit(link)
        if parsed.port == PORT:
            matches.append((parsed, parse_qs(parsed.query)))
    if len(matches) != 1:
        raise ApplyError("the compatibility share profile is missing or duplicated")
    parsed, query = matches[0]
    required = ("pbk", "sid", "sni", "fp")
    rendered_name = unquote(parsed.fragment)
    expected_name = f"{REMARK}-{EMAIL}"
    if (
        parsed.hostname != before["share_addr"]
        or rendered_name != expected_name
        or query.get("security", [""])[0] != "reality"
        or query.get("flow", [""])[0] != "xtls-rprx-vision"
        or query.get("type", [""])[0] != "tcp"
        or any(not query.get(key, [""])[0] for key in required)
    ):
        raise ApplyError("the compatibility share profile is incomplete")


def stage(api: PanelAPI, before: dict[str, object]) -> None:
    if compatibility_row() is not None:
        raise ApplyError("the compatibility profile already exists")
    with db_connect() as con:
        if con.execute("SELECT count(*) FROM clients WHERE email = ?", (EMAIL,)).fetchone()[0]:
            raise ApplyError("the compatibility client name already exists")
        if con.execute("SELECT count(*) FROM inbounds WHERE port = ?", (PORT,)).fetchone()[0]:
            raise ApplyError("the compatibility port is already used by 3X-UI")
    obj = api.post("/panel/api/inbounds/add", build_payload(before))
    if not isinstance(obj, dict):
        raise ApplyError("3X-UI did not return the staged inbound")
    inbound_id = obj.get("id") or obj.get("Id")
    if not isinstance(inbound_id, int) or inbound_id <= 0:
        raise ApplyError("3X-UI did not return a valid staged inbound ID")
    try:
        verify_database(before, enabled=False)
    except Exception:
        rollback_created(api, inbound_id)
        raise


def discard_staged(api: PanelAPI, before: dict[str, object]) -> None:
    row = verify_database(before, enabled=False)
    inbound_id = int(row["id"])
    api.post("/panel/api/clients/del/" + quote(EMAIL, safe="") + "?keepTraffic=0")
    api.post(f"/panel/api/inbounds/del/{inbound_id}")
    assert_original_unchanged(before)
    with db_connect() as con:
        if con.execute("PRAGMA quick_check").fetchone()[0] != "ok":
            raise ApplyError("SQLite quick_check failed after discarding the stage")
        counts = (
            con.execute("SELECT count(*) FROM clients").fetchone()[0],
            con.execute("SELECT count(*) FROM inbounds").fetchone()[0],
            con.execute("SELECT count(*) FROM client_inbounds").fetchone()[0],
        )
        if counts != (6, 6, 6):
            raise ApplyError("discarding the stage did not restore baseline counts")
        if con.execute("SELECT count(*) FROM clients WHERE email = ?", (EMAIL,)).fetchone()[0]:
            raise ApplyError("the staged compatibility client still exists")
        if con.execute("SELECT count(*) FROM inbounds WHERE remark = ?", (REMARK,)).fetchone()[0]:
            raise ApplyError("the staged compatibility inbound still exists")


def enable(api: PanelAPI, before: dict[str, object]) -> None:
    row = verify_database(before, enabled=False)
    if not docker_port_is_published():
        raise ApplyError("the dedicated Docker port is not published")
    inbound_id = int(row["id"])
    api.post(f"/panel/api/inbounds/setEnable/{inbound_id}", {"enable": True})
    try:
        verify_database(before, enabled=True)
        config = assembled_config(api)
        if not runtime_floor_is_active(config):
            raise ApplyError("the active Xray config does not contain the compatibility floor")
        subprocess.run(
            ["docker", "exec", "-i", CONTAINER, XRAY, "run", "-test", "-c", "stdin:"],
            input=json.dumps(config),
            text=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
            timeout=20,
        )
        verify_subscription(api, before)
    except Exception:
        try:
            current = compatibility_row()
            if current is not None and bool(current["enable"]):
                api.post(f"/panel/api/inbounds/setEnable/{inbound_id}", {"enable": False})
        except Exception:
            pass
        raise


def main() -> int:
    parser = argparse.ArgumentParser()
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--stage", action="store_true")
    group.add_argument("--enable", action="store_true")
    group.add_argument("--discard-staged", action="store_true")
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ApplyError("run as root on kitty-gration")
    if not DB_PATH.exists() or not ENV_PATH.exists():
        raise ApplyError("the tiny-vless runtime files were not found")

    before = original_state()
    if not args.stage and not args.enable and not args.discard_staged:
        row = compatibility_row()
        print("preflight_ok=true")
        print("protected_client_hash=" + str(before["client_hash"]))
        print("protected_inbound_hash=" + str(before["inbound_hash"]))
        print("protected_attachment_hash=" + str(before["attachment_hash"]))
        print("compatibility_profile=" + ("absent" if row is None else ("enabled" if row["enable"] else "staged")))
        return 0

    api = PanelAPI(load_env(ENV_PATH))
    if args.stage:
        stage(api, before)
        print("stage_ok=true")
        print("compatibility_profile=staged_disabled")
        print("same_subscription=true")
        print("protected_original_unchanged=true")
    elif args.enable:
        enable(api, before)
        print("enable_ok=true")
        print("compatibility_profile=enabled")
        print("subscription_profiles=7")
        print("runtime_compatibility_floor=active")
        print("protected_original_unchanged=true")
    else:
        discard_staged(api, before)
        print("discard_ok=true")
        print("compatibility_profile=absent")
        print("protected_original_unchanged=true")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (ApplyError, requests.RequestException, subprocess.SubprocessError, json.JSONDecodeError) as exc:
        # Exception text is deliberately bounded to script-authored messages;
        # HTTP bodies and private profile values are never included.
        print(f"error={exc}", file=os.sys.stderr)
        raise SystemExit(1)
