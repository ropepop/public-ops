#!/usr/bin/env python3
"""Secret-safe end-to-end checks for a tiny-vless subscription.

The input file (or stdin when ``--links-file -`` is used) must contain a JSON
array of private share links. Each link is converted into a temporary Xray
client configuration and exercised through a local SOCKS listener. The script
reports only fixed profile labels and pass or fail states; it never prints
links, credentials, endpoints, or response data.
"""

from __future__ import annotations

import argparse
import base64
import binascii
import json
import os
from pathlib import Path
import select
import socket
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any
from urllib.parse import parse_qs, unquote, urlsplit


class TestError(RuntimeError):
    pass


EXPECTED_HYSTERIA_PORTS = (8447,)
EXPECTED_PROFILE_COUNT = 7


def one(values: dict[str, list[str]], key: str, default: str = "") -> str:
    found = values.get(key)
    return found[-1] if found else default


def split_csv(value: str) -> list[str]:
    return [part.strip() for part in value.split(",") if part.strip()]


def decode_json_param(value: str) -> dict[str, Any]:
    if not value:
        return {}
    try:
        parsed = json.loads(value)
        return parsed if isinstance(parsed, dict) else {}
    except json.JSONDecodeError:
        pass
    padded = value + "=" * ((4 - len(value) % 4) % 4)
    for decoder in (base64.b64decode, base64.urlsafe_b64decode):
        try:
            parsed = json.loads(decoder(padded))
            return parsed if isinstance(parsed, dict) else {}
        except (ValueError, binascii.Error, UnicodeDecodeError):
            continue
    raise TestError("a profile contains an invalid encoded JSON parameter")


def pin_to_hex(value: str) -> str:
    normalized: list[str] = []
    for item in value.split(","):
        item = item.strip()
        compact = item.replace(":", "")
        if len(compact) == 64:
            try:
                bytes.fromhex(compact)
                normalized.append(compact.lower())
                continue
            except ValueError:
                pass
        for decoder in (base64.b64decode, base64.urlsafe_b64decode):
            try:
                raw = decoder(item + "=" * ((4 - len(item) % 4) % 4))
            except (ValueError, binascii.Error):
                continue
            if len(raw) == 32:
                normalized.append(raw.hex())
                break
        else:
            raise TestError("a certificate pin has an unsupported encoding")
    if not normalized:
        raise TestError("a certificate pin is missing")
    return ",".join(normalized)


def fixed_label(scheme: str, port: int) -> str:
    if scheme == "hysteria2":
        labels = {
            8447: "mobility-hysteria2",
        }
        try:
            return labels[port]
        except KeyError as exc:
            raise TestError("the subscription contains an unexpected Hysteria2 port") from exc
    labels = {
        ("vless", 8443): "original-vless-reality",
        ("vless", 8444): "mobility-vless-xhttp-h3",
        ("wireguard", 51820): "mobility-wireguard",
        ("vless", 18448): "mobility-vless-xhttp-h2",
        ("vmess", 8445): "mobility-vmess-mkcp",
        ("vless", 8446): "karing-singbox-reality-compat",
    }
    try:
        return labels[(scheme, port)]
    except KeyError as exc:
        raise TestError("the subscription contains an unexpected profile") from exc


def common_stream(network: str, security: str) -> dict[str, Any]:
    stream: dict[str, Any] = {"network": network, "security": security}
    if network == "tcp":
        stream["tcpSettings"] = {"header": {"type": "none"}}
    elif network == "kcp":
        # These are the client defaults emitted by 3X-UI 3.5.0's own link
        # parser. Testing them catches a server profile that only works with
        # hidden, non-portable client-side KCP tuning.
        stream["kcpSettings"] = {
            "mtu": 1350,
            "tti": 20,
            "uplinkCapacity": 5,
            "downlinkCapacity": 20,
            "cwndMultiplier": 1,
            "maxSendingWindow": 2097152,
        }
    elif network == "xhttp":
        stream["xhttpSettings"] = {
            "path": "/",
            "host": "",
            "mode": "auto",
            "headers": {},
            "xPaddingBytes": "100-1000",
        }
    return stream


def parse_vless(link: str) -> tuple[str, str, dict[str, Any]]:
    parsed = urlsplit(link)
    port = parsed.port or 443
    query = parse_qs(parsed.query, keep_blank_values=True)
    network = one(query, "type", "tcp")
    security = one(query, "security", "none")
    stream = common_stream(network, security)

    if network == "xhttp":
        xhttp = stream["xhttpSettings"]
        xhttp.update(decode_json_param(one(query, "extra")))
        xhttp["path"] = one(query, "path", "/")
        xhttp["host"] = one(query, "host")
        xhttp["mode"] = one(query, "mode", "auto")
        if one(query, "x_padding_bytes"):
            xhttp["xPaddingBytes"] = one(query, "x_padding_bytes")

    if security == "reality":
        stream["realitySettings"] = {
            "serverName": one(query, "sni"),
            "fingerprint": one(query, "fp", "chrome"),
            "publicKey": one(query, "pbk"),
            "shortId": one(query, "sid"),
            "spiderX": one(query, "spx", "/"),
        }
    elif security == "tls":
        tls: dict[str, Any] = {
            "serverName": one(query, "sni"),
            "fingerprint": one(query, "fp", "chrome"),
            "alpn": split_csv(one(query, "alpn")),
        }
        if one(query, "pcs"):
            tls["pinnedPeerCertSha256"] = pin_to_hex(one(query, "pcs"))
        if one(query, "vcn"):
            tls["verifyPeerCertByName"] = one(query, "vcn")
        stream["tlsSettings"] = tls

    if one(query, "fm"):
        stream["finalmask"] = decode_json_param(one(query, "fm"))

    outbound = {
        "protocol": "vless",
        "tag": "proxy",
        "settings": {
            "address": parsed.hostname,
            "port": port,
            "id": unquote(parsed.username or ""),
            "flow": one(query, "flow"),
            "encryption": one(query, "encryption", "none"),
        },
        "streamSettings": stream,
    }
    return fixed_label("vless", port), parsed.hostname or "", outbound


def parse_hysteria2(link: str) -> tuple[str, str, dict[str, Any]]:
    parsed = urlsplit(link)
    port = parsed.port or 443
    query = parse_qs(parsed.query, keep_blank_values=True)
    stream: dict[str, Any] = {
        "network": "hysteria",
        "security": "tls",
        "hysteriaSettings": {
            "version": 2,
            "auth": unquote(parsed.username or ""),
            "udpIdleTimeout": 60,
        },
        "tlsSettings": {
            "serverName": one(query, "sni"),
            "alpn": split_csv(one(query, "alpn", "h3")),
            "fingerprint": one(query, "fp", "chrome"),
            "pinnedPeerCertSha256": pin_to_hex(one(query, "pinSHA256")),
            "verifyPeerCertByName": one(query, "vcn"),
        },
    }
    if one(query, "fm"):
        stream["finalmask"] = decode_json_param(one(query, "fm"))
    outbound = {
        "protocol": "hysteria",
        "tag": "proxy",
        "settings": {"address": parsed.hostname, "port": port, "version": 2},
        "streamSettings": stream,
    }
    return fixed_label("hysteria2", port), parsed.hostname or "", outbound


def parse_wireguard(link: str) -> tuple[str, str, dict[str, Any]]:
    parsed = urlsplit(link)
    port = parsed.port or 51820
    query = parse_qs(parsed.query, keep_blank_values=True)
    addresses = split_csv(one(query, "address")) or ["10.0.0.2/32"]
    peer: dict[str, Any] = {
        "publicKey": one(query, "publickey"),
        "endpoint": f"{parsed.hostname}:{port}",
        "allowedIPs": ["0.0.0.0/0", "::/0"],
    }
    if one(query, "presharedkey"):
        peer["preSharedKey"] = one(query, "presharedkey")
    if one(query, "keepalive"):
        peer["keepAlive"] = int(one(query, "keepalive"))
    outbound = {
        "protocol": "wireguard",
        "tag": "proxy",
        "settings": {
            "secretKey": unquote(parsed.username or ""),
            "address": addresses,
            "peers": [peer],
            "mtu": int(one(query, "mtu", "1420")),
            "domainStrategy": "ForceIPv4v6",
            "noKernelTun": True,
        },
    }
    return fixed_label("wireguard", port), parsed.hostname or "", outbound


def parse_vmess(link: str) -> tuple[str, str, dict[str, Any]]:
    encoded = link.split("://", 1)[1]
    padded = encoded + "=" * ((4 - len(encoded) % 4) % 4)
    try:
        obj = json.loads(base64.urlsafe_b64decode(padded))
    except (ValueError, binascii.Error, UnicodeDecodeError) as exc:
        raise TestError("the VMess profile is malformed") from exc
    port = int(obj["port"])
    network = str(obj.get("net") or "tcp")
    security = "tls" if obj.get("tls") == "tls" else "none"
    stream = common_stream(network, security)
    if network == "kcp":
        kcp = stream["kcpSettings"]
        kcp["mtu"] = int(obj.get("mtu") or 1350)
        kcp["tti"] = int(obj.get("tti") or 20)
        if str(obj.get("type") or "none") != "none" or obj.get("path"):
            raise TestError("the mKCP profile uses removed header or seed settings")
    if obj.get("fm"):
        stream["finalmask"] = decode_json_param(str(obj["fm"]))
    outbound = {
        "protocol": "vmess",
        "tag": "proxy",
        "settings": {
            "vnext": [{
                "address": obj["add"],
                "port": port,
                "users": [{
                    "id": obj["id"],
                    "security": obj.get("scy") or "auto",
                }],
            }],
        },
        "streamSettings": stream,
    }
    return fixed_label("vmess", port), str(obj["add"]), outbound


def parse_link(link: str) -> tuple[str, str, dict[str, Any]]:
    scheme = link.split(":", 1)[0].lower()
    if scheme == "vless":
        return parse_vless(link)
    if scheme == "hysteria2":
        return parse_hysteria2(link)
    if scheme == "wireguard":
        return parse_wireguard(link)
    if scheme == "vmess":
        return parse_vmess(link)
    raise TestError("the subscription contains an unsupported profile scheme")


def unused_local_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def wait_for_listener(port: int, process: subprocess.Popen[bytes]) -> bool:
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        if process.poll() is not None:
            return False
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return True
        except OSError:
            time.sleep(0.1)
    return False


def client_config(port: int, outbound: dict[str, Any]) -> dict[str, Any]:
    return {
        "log": {"loglevel": "warning"},
        "inbounds": [{
            "listen": "127.0.0.1",
            "port": port,
            "protocol": "socks",
            "settings": {"udp": True},
            "tag": "probe-in",
        }],
        "outbounds": [outbound],
    }


def test_outbound(
    xray: Path,
    label: str,
    endpoint: str,
    outbound: dict[str, Any],
) -> tuple[bool, bool, float]:
    port = unused_local_port()
    config = client_config(port, outbound)
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="tiny-vless-probe-") as tmp:
        tmpdir = Path(tmp)
        os.chmod(tmpdir, 0o700)
        config_path = tmpdir / "config.json"
        log_path = tmpdir / "xray.log"
        response_path = tmpdir / "egress.txt"
        config_path.write_text(json.dumps(config, separators=(",", ":")))
        os.chmod(config_path, 0o600)
        with log_path.open("wb") as log:
            checked = subprocess.run(
                [str(xray), "run", "-test", "-c", str(config_path)],
                stdout=log,
                stderr=subprocess.STDOUT,
                check=False,
                timeout=15,
            )
        if checked.returncode != 0:
            return False, False, time.monotonic() - started
        with log_path.open("ab") as log:
            process = subprocess.Popen(
                [str(xray), "run", "-c", str(config_path)],
                stdout=log,
                stderr=subprocess.STDOUT,
            )
            try:
                if not wait_for_listener(port, process):
                    return True, False, time.monotonic() - started
                with response_path.open("wb") as response:
                    curl = subprocess.run(
                        [
                            "/usr/bin/curl", "-fsS", "--max-time", "20",
                            "--socks5-hostname", f"127.0.0.1:{port}",
                            "https://api.ipify.org",
                        ],
                        stdout=response,
                        stderr=log,
                        check=False,
                    )
                if curl.returncode != 0:
                    return True, False, time.monotonic() - started
                observed = response_path.read_text().strip()
                return True, observed == endpoint, time.monotonic() - started
            finally:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)


class UdpRebinder:
    """Forward UDP while allowing the Internet-facing source socket to change."""

    def __init__(self, host: str, port: int):
        resolved = socket.getaddrinfo(host, port, socket.AF_INET, socket.SOCK_DGRAM)[0][4]
        self.remote = (str(resolved[0]), int(resolved[1]))
        self.listener = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.listener.bind(("127.0.0.1", 0))
        self.listener.setblocking(False)
        self.local_port = int(self.listener.getsockname()[1])
        self._lock = threading.Lock()
        self._upstream = self._new_upstream()
        self._client: tuple[str, int] | None = None
        self._stop = threading.Event()
        self.to_server = 0
        self.to_client = 0
        self._thread = threading.Thread(target=self._run, daemon=True)

    def _new_upstream(self) -> socket.socket:
        upstream = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        upstream.connect(self.remote)
        upstream.setblocking(False)
        return upstream

    def start(self) -> None:
        self._thread.start()

    def snapshot(self) -> tuple[int, int]:
        with self._lock:
            return self.to_server, self.to_client

    def rebind(self) -> None:
        replacement = self._new_upstream()
        with self._lock:
            old = self._upstream
            self._upstream = replacement
        old.close()

    def close(self) -> None:
        self._stop.set()
        self._thread.join(timeout=2)
        with self._lock:
            self._upstream.close()
        self.listener.close()

    def _run(self) -> None:
        while not self._stop.is_set():
            with self._lock:
                upstream = self._upstream
            try:
                readable, _, _ = select.select([self.listener, upstream], [], [], 0.1)
            except (OSError, ValueError):
                continue
            if self.listener in readable:
                try:
                    data, client = self.listener.recvfrom(65535)
                    with self._lock:
                        self._client = (str(client[0]), int(client[1]))
                        current = self._upstream
                    current.send(data)
                    with self._lock:
                        self.to_server += 1
                except (BlockingIOError, OSError):
                    pass
            if upstream in readable:
                try:
                    data = upstream.recv(65535)
                    with self._lock:
                        client = self._client
                    if client is not None:
                        self.listener.sendto(data, client)
                        with self._lock:
                            self.to_client += 1
                except (BlockingIOError, OSError):
                    pass


def redirect_udp_outbound(outbound: dict[str, Any], local_port: int) -> dict[str, Any]:
    # JSON is sufficient as a safe deep-copy because Xray configs contain only
    # JSON-native data. The original endpoint stays only in memory.
    redirected = json.loads(json.dumps(outbound))
    protocol = redirected["protocol"]
    if protocol in {"vless", "hysteria"}:
        redirected["settings"]["address"] = "127.0.0.1"
        redirected["settings"]["port"] = local_port
    elif protocol == "vmess":
        redirected["settings"]["vnext"][0]["address"] = "127.0.0.1"
        redirected["settings"]["vnext"][0]["port"] = local_port
    elif protocol == "wireguard":
        redirected["settings"]["peers"][0]["endpoint"] = f"127.0.0.1:{local_port}"
    else:
        raise TestError("the selected profile is not UDP-based")
    return redirected


def test_udp_rebind(
    xray: Path,
    endpoint: str,
    outbound: dict[str, Any],
    stream_url: str,
    expected_bytes: int,
    limit_rate: str,
    trigger_bytes: int,
    min_payload_seconds: float,
) -> tuple[bool | None, bool, float]:
    protocol = str(outbound["protocol"])
    if protocol == "wireguard":
        remote_port = int(str(outbound["settings"]["peers"][0]["endpoint"]).rsplit(":", 1)[1])
    elif protocol == "vmess":
        remote_port = int(outbound["settings"]["vnext"][0]["port"])
    else:
        remote_port = int(outbound["settings"]["port"])

    rebinder = UdpRebinder(endpoint, remote_port)
    rebinder.start()
    socks_port = unused_local_port()
    redirected = redirect_udp_outbound(outbound, rebinder.local_port)
    config = client_config(socks_port, redirected)
    started = time.monotonic()
    try:
        with tempfile.TemporaryDirectory(prefix="tiny-vless-rebind-") as tmp:
            tmpdir = Path(tmp)
            os.chmod(tmpdir, 0o700)
            config_path = tmpdir / "config.json"
            log_path = tmpdir / "xray.log"
            response_path = tmpdir / "stream.bin"
            config_path.write_text(json.dumps(config, separators=(",", ":")))
            os.chmod(config_path, 0o600)
            with log_path.open("wb") as log:
                checked = subprocess.run(
                    [str(xray), "run", "-test", "-c", str(config_path)],
                    stdout=log,
                    stderr=subprocess.STDOUT,
                    check=False,
                    timeout=15,
                )
            if checked.returncode != 0:
                return None, False, time.monotonic() - started
            with log_path.open("ab") as log:
                process = subprocess.Popen(
                    [str(xray), "run", "-c", str(config_path)],
                    stdout=log,
                    stderr=subprocess.STDOUT,
                )
                try:
                    if not wait_for_listener(socks_port, process):
                        return None, False, time.monotonic() - started
                    with response_path.open("wb") as response:
                        curl_timeout = max(50, int(min_payload_seconds) + 60)
                        curl_args = [
                                "/usr/bin/curl", "-fsSL", "--http1.1",
                                "--max-time", str(curl_timeout),
                        ]
                        if limit_rate:
                            curl_args.extend(["--limit-rate", limit_rate])
                        curl_args.extend([
                            "--socks5-hostname", f"127.0.0.1:{socks_port}",
                            stream_url,
                        ])
                        curl = subprocess.Popen(
                            curl_args,
                            stdout=response,
                            stderr=log,
                        )
                        deadline = time.monotonic() + 25
                        first_payload_at: float | None = None
                        rebind_threshold = trigger_bytes or min(
                            32768, max(512, expected_bytes // 8)
                        )
                        while time.monotonic() < deadline:
                            up, down = rebinder.snapshot()
                            delivered = response_path.stat().st_size
                            if delivered > 0 and first_payload_at is None:
                                first_payload_at = time.monotonic()
                            # A slow TCP download carried inside UDP can be very
                            # asymmetric: WireGuard or QUIC may ACK many packets
                            # in one outer datagram. One packet each way plus
                            # delivered payload is enough to prove the path is
                            # live before changing the NAT mapping.
                            if (
                                up >= 1
                                and down >= 1
                                and delivered >= rebind_threshold
                            ):
                                break
                            if curl.poll() is not None:
                                return None, False, time.monotonic() - started
                            time.sleep(0.1)
                        else:
                            curl.terminate()
                            curl.wait(timeout=3)
                            return None, False, time.monotonic() - started
                        before_up, before_down = rebinder.snapshot()
                        rebinder.rebind()
                        try:
                            returncode = curl.wait(timeout=curl_timeout + 5)
                        except subprocess.TimeoutExpired:
                            curl.kill()
                            curl.wait(timeout=3)
                            returncode = -1
                    after_up, after_down = rebinder.snapshot()
                    payload_elapsed = (
                        time.monotonic() - first_payload_at
                        if first_payload_at is not None
                        else 0.0
                    )
                    size_ok = response_path.stat().st_size == expected_bytes
                    path_moved = after_up > before_up and after_down > before_down
                    continuity_ok = (
                        returncode == 0
                        and size_ok
                        and path_moved
                        and payload_elapsed >= min_payload_seconds
                    )

                    # A transport that cannot preserve the existing inner TCP
                    # stream may still heal cleanly for the next connection.
                    # Keep that recovery result separate from continuity so the
                    # mKCP control remains informative instead of failing the
                    # whole mobility experiment by design.
                    recovery_path = tmpdir / "recovery.txt"
                    with recovery_path.open("wb") as response:
                        recovery = subprocess.run(
                            [
                                "/usr/bin/curl", "-fsS", "--max-time", "20",
                                "--socks5-hostname", f"127.0.0.1:{socks_port}",
                                "https://api.ipify.org",
                            ],
                            stdout=response,
                            stderr=log,
                            check=False,
                        )
                    recovery_ok = (
                        recovery.returncode == 0
                        and recovery_path.read_text().strip() == endpoint
                    )
                    return continuity_ok, recovery_ok, time.monotonic() - started
                finally:
                    process.terminate()
                    try:
                        process.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        process.kill()
                        process.wait(timeout=3)
    finally:
        rebinder.close()


def karing_client_config(link: str, port: int) -> dict[str, Any]:
    parsed = urlsplit(link)
    query = parse_qs(parsed.query, keep_blank_values=True)
    if parsed.scheme != "vless" or parsed.port != 8446:
        raise TestError("the Karing compatibility profile is missing")
    return {
        "log": {"level": "warn", "timestamp": True},
        "inbounds": [{
            "type": "mixed",
            "tag": "probe-in",
            "listen": "127.0.0.1",
            "listen_port": port,
        }],
        "outbounds": [{
            "type": "vless",
            "tag": "proxy",
            "server": parsed.hostname,
            "server_port": parsed.port,
            "uuid": unquote(parsed.username or ""),
            "flow": one(query, "flow"),
            "tls": {
                "enabled": True,
                "server_name": one(query, "sni"),
                "utls": {
                    "enabled": True,
                    "fingerprint": one(query, "fp", "chrome"),
                },
                "reality": {
                    "enabled": True,
                    "public_key": one(query, "pbk"),
                    "short_id": one(query, "sid"),
                },
            },
        }],
        "route": {"final": "proxy"},
    }


def test_karing_compatibility(core: Path, link: str) -> tuple[bool, bool, float]:
    parsed = urlsplit(link)
    port = unused_local_port()
    config = karing_client_config(link, port)
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="tiny-vless-karing-") as tmp:
        tmpdir = Path(tmp)
        os.chmod(tmpdir, 0o700)
        config_path = tmpdir / "config.json"
        log_path = tmpdir / "core.log"
        response_path = tmpdir / "egress.txt"
        config_path.write_text(json.dumps(config, separators=(",", ":")))
        os.chmod(config_path, 0o600)
        with log_path.open("wb") as log:
            checked = subprocess.run(
                [str(core), "check", "-c", str(config_path)],
                stdout=log,
                stderr=subprocess.STDOUT,
                check=False,
                timeout=20,
            )
        if checked.returncode != 0:
            return False, False, time.monotonic() - started
        with log_path.open("ab") as log:
            process = subprocess.Popen(
                [str(core), "run", "-c", str(config_path)],
                stdout=log,
                stderr=subprocess.STDOUT,
            )
            try:
                if not wait_for_listener(port, process):
                    return True, False, time.monotonic() - started
                with response_path.open("wb") as response:
                    curl = subprocess.run(
                        [
                            "/usr/bin/curl", "-fsS", "--max-time", "20",
                            "--socks5-hostname", f"127.0.0.1:{port}",
                            "https://api.ipify.org",
                        ],
                        stdout=response,
                        stderr=log,
                        check=False,
                    )
                tunnel_ok = (
                    curl.returncode == 0
                    and response_path.read_text().strip() == (parsed.hostname or "")
                    and "reality verification failed"
                    not in log_path.read_text(errors="replace")
                )
                return True, tunnel_ok, time.monotonic() - started
            finally:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--xray", type=Path, required=True)
    parser.add_argument("--links-file", type=Path, required=True)
    parser.add_argument(
        "--hysteria-port",
        type=int,
        action="append",
        dest="hysteria_ports",
        help=(
            "expected Hysteria2 UDP endpoint port; repeat for multiple ports "
            "(default: 8447)"
        ),
    )
    parser.add_argument(
        "--karing-core",
        type=Path,
        help="optional Karing sing-box core used to test the compatibility profile",
    )
    parser.add_argument(
        "--simulate-udp-rebind",
        action="store_true",
        help="change the outer UDP source socket during a live proxied stream",
    )
    parser.add_argument(
        "--rebind-stream-url",
        default="https://speed.cloudflare.com/__down?bytes=524288",
        help="slow HTTP stream used by the optional UDP-rebinding probe",
    )
    parser.add_argument(
        "--rebind-stream-bytes",
        type=int,
        default=524288,
        help="exact response size expected from the rebinding stream",
    )
    parser.add_argument(
        "--rebind-profile",
        action="append",
        choices=[
            "mobility-hysteria2",
            "mobility-vless-xhttp-h3",
            "mobility-wireguard",
            "mobility-vmess-mkcp",
        ],
        help="limit optional rebinding checks to one or more fixed profile labels",
    )
    parser.add_argument(
        "--rebind-limit-rate",
        default="64k",
        help="curl download cap that keeps the stream active across rebinding; empty disables it",
    )
    parser.add_argument(
        "--rebind-trigger-bytes",
        type=int,
        default=0,
        help="override the delivered-byte threshold used before changing the UDP source mapping",
    )
    parser.add_argument(
        "--rebind-min-payload-seconds",
        type=float,
        default=0.0,
        help="minimum live payload duration required by the optional rebinding probe",
    )
    args = parser.parse_args()
    if not 0.0 <= args.rebind_min_payload_seconds <= 600.0:
        raise TestError("the rebinding payload-duration requirement is invalid")
    if args.rebind_min_payload_seconds and not args.simulate_udp_rebind:
        raise TestError("the rebinding payload-duration requirement needs simulation")
    if not args.xray.is_file() or not os.access(args.xray, os.X_OK):
        raise TestError("the Xray executable is unavailable")
    if str(args.links_file) == "-":
        raw_links = sys.stdin.read()
    else:
        if args.links_file.stat().st_mode & 0o077:
            raise TestError("the private links file must not be readable by group or others")
        raw_links = args.links_file.read_text()
    links = json.loads(raw_links)
    if (
        not isinstance(links, list)
        or len(links) != EXPECTED_PROFILE_COUNT
        or not all(isinstance(x, str) for x in links)
    ):
        raise TestError("the subscription did not contain exactly seven profiles")
    expected_hysteria_ports = tuple(
        sorted(args.hysteria_ports or EXPECTED_HYSTERIA_PORTS)
    )
    if (
        len(expected_hysteria_ports) != len(set(expected_hysteria_ports))
        or any(not 1 <= port <= 65535 for port in expected_hysteria_ports)
    ):
        raise TestError("the expected Hysteria2 port set is invalid")
    hysteria_ports = [
        urlsplit(link).port or 443
        for link in links
        if link.split(":", 1)[0].lower() == "hysteria2"
    ]
    if tuple(sorted(hysteria_ports)) != expected_hysteria_ports:
        raise TestError("the Hysteria2 profiles are missing, duplicated, or on the wrong ports")

    parsed = [parse_link(link) for link in links]
    if len({label for label, _, _ in parsed}) != EXPECTED_PROFILE_COUNT:
        raise TestError("the subscription profile set is incomplete")

    tunnel_passed = True
    for label, endpoint, outbound in parsed:
        config_ok, tunnel_ok, elapsed = test_outbound(args.xray, label, endpoint, outbound)
        print(
            f"profile={label} config={'passed' if config_ok else 'failed'} "
            f"tunnel={'passed' if tunnel_ok else 'failed'} elapsed={elapsed:.1f}s"
        )
        tunnel_passed = tunnel_passed and config_ok and tunnel_ok
    if args.karing_core is not None:
        if not args.karing_core.is_file() or not os.access(args.karing_core, os.X_OK):
            raise TestError("the Karing core executable is unavailable")
        matches = [
            link
            for link in links
            if link.startswith("vless://") and urlsplit(link).port == 8446
        ]
        if len(matches) != 1:
            raise TestError("the Karing compatibility profile is missing or duplicated")
        config_ok, tunnel_ok, elapsed = test_karing_compatibility(
            args.karing_core, matches[0]
        )
        print(
            "profile=karing-singbox-reality-compat "
            f"karing_config={'passed' if config_ok else 'failed'} "
            f"karing_tunnel={'passed' if tunnel_ok else 'failed'} "
            f"elapsed={elapsed:.1f}s"
        )
        tunnel_passed = tunnel_passed and config_ok and tunnel_ok
    rebind_passed = True
    if args.simulate_udp_rebind:
        udp_labels = {
            "mobility-hysteria2",
            "mobility-vless-xhttp-h3",
            "mobility-wireguard",
            "mobility-vmess-mkcp",
        }
        continuity_required = {
            "mobility-hysteria2",
            "mobility-vless-xhttp-h3",
            "mobility-wireguard",
        }
        for label, endpoint, outbound in parsed:
            if label not in udp_labels:
                continue
            if args.rebind_profile and label not in args.rebind_profile:
                continue
            continuity_ok, recovery_ok, elapsed = test_udp_rebind(
                args.xray,
                endpoint,
                outbound,
                args.rebind_stream_url,
                args.rebind_stream_bytes,
                args.rebind_limit_rate,
                args.rebind_trigger_bytes,
                args.rebind_min_payload_seconds,
            )
            print(
                f"profile={label} udp_continuity="
                f"{'not_tested' if continuity_ok is None else ('passed' if continuity_ok else 'interrupted')} "
                f"udp_recovery={'passed' if recovery_ok else 'failed'} "
                f"elapsed={elapsed:.1f}s"
            )
            profile_ok = recovery_ok and (
                continuity_ok is True or label not in continuity_required
            )
            rebind_passed = rebind_passed and profile_ok
        print("mobility_udp_rebind_suite=" + ("passed" if rebind_passed else "failed"))
    print("mobility_tunnel_suite=" + ("passed" if tunnel_passed else "failed"))
    overall_passed = tunnel_passed and rebind_passed
    return 0 if overall_passed else 1


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except TestError as exc:
        print(f"test_error={exc}")
        raise SystemExit(1)
    except (json.JSONDecodeError, OSError, ValueError):
        print("test_error=private profile input could not be processed")
        raise SystemExit(1)
