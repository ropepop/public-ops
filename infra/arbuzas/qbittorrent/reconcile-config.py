#!/usr/bin/env python3
"""Reconcile the small security/behavior surface owned by the Arbuzas deploy."""

from __future__ import annotations

import argparse
import configparser
import os
from pathlib import Path
import stat
import tempfile


TAILNET_HOSTNAME = "arbuzas-vps.tail9345a.ts.net"
PRIVATE_SUBNET = "172.29.246.0/28"
DOCKER_GATEWAY = "172.29.246.1"
TAILNET_IPV4_SUBNET = "100.64.0.0/10"
TAILNET_IPV6_SUBNET = "fd7a:115c:a1e0::/48"

MANAGED_VALUES = {
    "BitTorrent": {
        "Session\\AsyncIOThreadsCount": "2",
        "Session\\CheckingMemUsageSize": "8",
        "Session\\ConnectionSpeed": "10",
        "Session\\DefaultSavePath": "/downloads/",
        "Session\\DiskIOReadMode": "DisableOSCache",
        "Session\\DiskIOType": "SimplePreadPwrite",
        "Session\\DiskIOWriteMode": "DisableOSCache",
        "Session\\DiskQueueSize": "1048576",
        "Session\\FilePoolSize": "32",
        "Session\\GlobalMaxInactiveSeedingMinutes": "-1",
        "Session\\GlobalMaxRatio": "-1",
        "Session\\GlobalMaxSeedingMinutes": "-1",
        "Session\\HashingThreadsCount": "1",
        "Session\\MaxActiveCheckingTorrents": "1",
        "Session\\MaxConcurrentHTTPAnnounces": "10",
        "Session\\MaxConnections": "80",
        "Session\\MaxConnectionsPerTorrent": "20",
        "Session\\MaxUploads": "12",
        "Session\\MaxUploadsPerTorrent": "4",
        "Session\\Port": "45123",
        "Session\\QueueingSystemEnabled": "false",
        "Session\\RequestQueueSize": "50",
        "Session\\SaveResumeDataInterval": "1",
        "Session\\SendBufferLowWatermark": "16",
        "Session\\SendBufferWatermark": "128",
        "Session\\SendBufferWatermarkFactor": "25",
        "Session\\ShareLimitAction": "Stop",
        "Session\\SocketReceiveBufferSize": "65536",
        "Session\\SocketSendBufferSize": "65536",
        "Session\\TempPath": "/downloads/.incomplete/",
        "Session\\TempPathEnabled": "true",
        "Session\\TorrentStopCondition": "MetadataReceived",
    },
    "Preferences": {
        "WebUI\\Address": "*",
        "WebUI\\AlternativeUIEnabled": "true",
        "WebUI\\AuthSubnetWhitelist": ", ".join(
            (
                "127.0.0.1/32",
                "::1/128",
                PRIVATE_SUBNET,
                TAILNET_IPV4_SUBNET,
                TAILNET_IPV6_SUBNET,
            )
        ),
        "WebUI\\AuthSubnetWhitelistEnabled": "true",
        "WebUI\\CSRFProtection": "true",
        "WebUI\\ClickjackingProtection": "true",
        "WebUI\\HostHeaderValidation": "true",
        "WebUI\\LocalHostAuth": "false",
        "WebUI\\Port": "24680",
        "WebUI\\ReverseProxySupportEnabled": "true",
        "WebUI\\RootFolder": "/vuetorrent",
        "WebUI\\SecureCookie": "true",
        # QSettings treats unquoted semicolons as comments in an INI file.
        # Keep the raw value quoted so qBittorrent receives four domains.
        "WebUI\\ServerDomains": (
            f'"{TAILNET_HOSTNAME};localhost;127.0.0.1;qbittorrent"'
        ),
        "WebUI\\TrustedReverseProxiesList": DOCKER_GATEWAY,
        "WebUI\\UseUPnP": "false",
        "WebUI\\HTTPS\\Enabled": "false",
    },
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--path",
        type=Path,
        default=Path("/srv/arbuzas/qbittorrent/storage/config/qBittorrent/qBittorrent.conf"),
    )
    parser.add_argument("--uid", type=int, required=True)
    parser.add_argument("--gid", type=int, required=True)
    parser.add_argument("--check", action="store_true")
    return parser.parse_args()


def load(path: Path) -> configparser.RawConfigParser:
    parser = configparser.RawConfigParser(
        interpolation=None,
        strict=False,
        delimiters=("=",),
        comment_prefixes=("#", ";"),
        empty_lines_in_values=False,
    )
    parser.optionxform = str
    if path.exists():
        mode = path.lstat().st_mode
        if not stat.S_ISREG(mode) or stat.S_ISLNK(mode):
            raise SystemExit(f"refusing non-regular qBittorrent config: {path}")
        with path.open("r", encoding="utf-8") as handle:
            parser.read_file(handle)
    return parser


def drift(parser: configparser.RawConfigParser) -> list[str]:
    problems: list[str] = []
    for section, values in MANAGED_VALUES.items():
        if not parser.has_section(section):
            problems.append(f"missing section [{section}]")
            continue
        for key, expected in values.items():
            actual = parser.get(section, key, fallback=None, raw=True)
            if actual != expected:
                problems.append(f"[{section}] {key}={actual!r}, expected {expected!r}")
    return problems


def reconcile(parser: configparser.RawConfigParser) -> None:
    for section, values in MANAGED_VALUES.items():
        if not parser.has_section(section):
            parser.add_section(section)
        for key, value in values.items():
            parser.set(section, key, value)


def ensure_safe_parent(path: Path, *, create: bool) -> Path:
    """Return an absolute path after rejecting symlinks in every parent."""
    absolute = path.absolute()
    parent = absolute.parent
    components = [*reversed(parent.parents), parent]
    for component in components:
        try:
            mode = component.lstat().st_mode
        except FileNotFoundError:
            if not create:
                raise SystemExit(f"missing qBittorrent config parent: {component}")
            try:
                component.mkdir(mode=0o750)
            except FileExistsError:
                pass
            mode = component.lstat().st_mode
        if stat.S_ISLNK(mode) or not stat.S_ISDIR(mode):
            raise SystemExit(f"refusing unsafe qBittorrent config parent: {component}")
    return absolute


def write_atomic(path: Path, parser: configparser.RawConfigParser, uid: int, gid: int) -> None:
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent, text=True)
    tmp_path = Path(tmp_name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            parser.write(handle, space_around_delimiters=False)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(tmp_path, 0o640)
        os.chown(tmp_path, uid, gid)
        os.replace(tmp_path, path)
    finally:
        try:
            tmp_path.unlink()
        except FileNotFoundError:
            pass


def main() -> int:
    args = parse_args()
    if args.uid < 1 or args.gid < 1:
        raise SystemExit("qBittorrent uid and gid must be positive integers")

    managed_path = ensure_safe_parent(args.path, create=not args.check)
    fallback_path = managed_path.with_name(f"{managed_path.stem}_new{managed_path.suffix}")
    paths = [managed_path]
    if fallback_path.exists() or fallback_path.is_symlink():
        paths.append(fallback_path)

    if args.check:
        problems = []
        for path in paths:
            problems.extend(f"{path}: {problem}" for problem in drift(load(path)))
        if problems:
            raise SystemExit("qBittorrent managed config drift:\n  " + "\n  ".join(problems))
        return 0

    for path in paths:
        parser = load(path)
        reconcile(parser)
        write_atomic(path, parser, args.uid, args.gid)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
