#!/usr/bin/env python3
"""Adopt, deploy, and validate the existing Tiny VLESS component safely.

All output is deliberately fixed and secret-free. The manager never prints an
environment value, credential, certificate fingerprint, subscription identity,
profile link, Tailscale hostname, or rendered subscription path.
"""

from __future__ import annotations

import argparse
import base64
from collections import Counter
from dataclasses import dataclass
from datetime import datetime, timezone
import fcntl
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import uuid


PROJECT = "tiny-vless"
SERVICE = "3xui"
CONTAINER = "tiny-vless-3xui"
EXPECTED_IMAGE = (
    "ghcr.io/mhsanaei/3x-ui@sha256:"
    "344f7a68a91e59d592fc355d67e32d8c2041b1c2082a7eaa3c413dc3a5cab7db"
)
EXPECTED_CONTAINER_IP = "172.30.77.2"
EXPECTED_PROFILE_ENDPOINTS = (
    ("hysteria", 8447),
    ("vless", 8443),
    ("vless", 8444),
    ("vless", 8446),
    ("vless", 18448),
    ("vmess", 8445),
    ("wireguard", 51820),
)
EXPECTED_ENABLED_INBOUNDS = len(EXPECTED_PROFILE_ENDPOINTS)
EXPECTED_PROTOCOLS = dict(Counter(protocol for protocol, _ in EXPECTED_PROFILE_ENDPOINTS))
EXPECTED_CLEARNET_PORT = 18081
EXPECTED_NON_TARGET_TAILSCALE_PORTS = (19999, 24680, 29096)
EXPECTED_RESERVED_DESTINATIONS = (
    "0.0.0.0/8",
    "10.0.0.0/8",
    "100.64.0.0/10",
    "127.0.0.0/8",
    "169.254.0.0/16",
    "172.16.0.0/12",
    "192.0.0.0/24",
    "192.0.2.0/24",
    "192.168.0.0/16",
    "198.18.0.0/15",
    "198.51.100.0/24",
    "203.0.113.0/24",
    "224.0.0.0/4",
    "240.0.0.0/4",
)

STACK_DIR = Path("/opt/tiny-vless")
LIVE_COMPOSE = STACK_DIR / "docker-compose.yml"
DB_PATH = STACK_DIR / "db/x-ui.db"
BACKUP_DIR = Path("/srv/arbuzas/tiny-vless/backups")
PENDING_ROLLBACK = Path("/srv/arbuzas/tiny-vless/pending-rollback.json")
CANONICAL_ENV = Path("/etc/arbuzas/env/tiny-vless.env")
CANONICAL_SECRET_DIR = Path("/etc/arbuzas/secrets/tiny-vless")
CANONICAL_CERT_DIR = CANONICAL_SECRET_DIR / "cert"
CANONICAL_CAPABILITY = CANONICAL_SECRET_DIR / "capability.secret"
CANONICAL_LEGACY_ID = CANONICAL_SECRET_DIR / "legacy-subscription-id.secret"
LEGACY_ENV = STACK_DIR / ".env"
LEGACY_CERT_DIR = STACK_DIR / "cert"
LEGACY_STATE_DIR = Path("/etc/tiny-vless-clearnet-sub")
LEGACY_CAPABILITY = LEGACY_STATE_DIR / "capability.secret"
LEGACY_ID = LEGACY_STATE_DIR / "legacy-subscription-id.secret"
LIVE_NGINX_TEMPLATE = STACK_DIR / "clearnet-sub/nginx.conf.template"
LIVE_NGINX_LIMITS = Path(
    "/etc/systemd/system/nginx.service.d/tiny-vless-clearnet-sub-limits.conf"
)
NGINX_SITE = Path("/etc/nginx/sites-available/tiny-vless-clearnet-sub.conf")
NGINX_SITE_ENABLED = Path("/etc/nginx/sites-enabled/tiny-vless-clearnet-sub.conf")
ABUSE_SCRIPT = Path("/usr/local/sbin/tiny-vless-abuse-blocks")
RATE_SCRIPT = Path("/usr/local/sbin/tiny-vless-rate-limit")
ABUSE_UNIT = Path("/etc/systemd/system/tiny-vless-abuse-blocks.service")
RATE_UNIT = Path("/etc/systemd/system/tiny-vless-rate-limit.service")
RATE_TIMER = Path("/etc/systemd/system/tiny-vless-rate-limit.timer")
LOCK_PATH = Path("/run/lock/arbuzas-tiny-vless.lock")
IFB_INTERFACE = "ifb-tinyvless"

SECRET_FILE_LINKS = (
    (LEGACY_ENV, CANONICAL_ENV),
    (LEGACY_CAPABILITY, CANONICAL_CAPABILITY),
    (LEGACY_ID, CANONICAL_LEGACY_ID),
)


class ComponentError(RuntimeError):
    """A failure with a fixed, safe-to-print error code."""

    def __init__(self, code: str):
        if not re.fullmatch(r"[a-z0-9_]+", code):
            code = "unsafe_error"
        super().__init__(code)
        self.code = code


@dataclass(frozen=True)
class FileSnapshot:
    kind: str
    payload: bytes | str | None
    mode: int | None
    uid: int | None
    gid: int | None


@dataclass
class CompatibilitySwap:
    path: Path
    target: Path
    backup: Path | None
    changed: bool

    def rollback(self) -> None:
        if not self.changed:
            return
        if self.path.is_symlink() or self.path.exists():
            if self.path.is_dir() and not self.path.is_symlink():
                shutil.rmtree(self.path)
            else:
                self.path.unlink()
        if self.backup is not None:
            os.replace(self.backup, self.path)

    def commit(self) -> None:
        if self.backup is None:
            return
        if self.backup.is_dir() and not self.backup.is_symlink():
            shutil.rmtree(self.backup)
        elif self.backup.is_symlink() or self.backup.exists():
            self.backup.unlink()
        self.backup = None


@dataclass(frozen=True)
class UnitState:
    enabled: bool
    active: bool


def require_root() -> None:
    if os.geteuid() != 0:
        raise ComponentError("root_required")


def command(
    argv: list[str],
    *,
    capture: bool = False,
    check: bool = True,
    timeout: int | None = 120,
) -> str:
    try:
        result = subprocess.run(
            argv,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
            timeout=timeout,
            text=True,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ComponentError("command_unavailable") from error
    if check and result.returncode != 0:
        raise ComponentError("command_failed")
    return result.stdout if capture else ""


def command_succeeds(argv: list[str], *, timeout: int = 30) -> bool:
    try:
        result = subprocess.run(
            argv,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
            timeout=timeout,
        )
    except (OSError, subprocess.TimeoutExpired):
        return False
    return result.returncode == 0


def sha256_bytes(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def sha256_file(path: Path) -> str:
    return sha256_bytes(path.read_bytes())


def atomic_write(
    path: Path,
    content: bytes,
    mode: int,
    *,
    uid: int = 0,
    gid: int = 0,
) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, "wb") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        os.chown(temporary, uid, gid)
        os.replace(temporary, path)
        os.chmod(path, mode)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def snapshot_file(path: Path) -> FileSnapshot:
    if path.is_symlink():
        status = path.lstat()
        return FileSnapshot("symlink", os.readlink(path), None, status.st_uid, status.st_gid)
    if not path.exists():
        return FileSnapshot("missing", None, None, None, None)
    if not path.is_file():
        raise ComponentError("managed_path_not_file")
    status = path.stat()
    return FileSnapshot(
        "file",
        path.read_bytes(),
        stat.S_IMODE(status.st_mode),
        status.st_uid,
        status.st_gid,
    )


def restore_file(path: Path, snapshot: FileSnapshot) -> None:
    if path.is_symlink() or path.exists():
        if path.is_dir() and not path.is_symlink():
            raise ComponentError("rollback_path_not_file")
        path.unlink()
    if snapshot.kind == "missing":
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    if snapshot.kind == "symlink":
        assert isinstance(snapshot.payload, str)
        path.symlink_to(snapshot.payload)
        if snapshot.uid is not None and snapshot.gid is not None:
            os.lchown(path, snapshot.uid, snapshot.gid)
        return
    if snapshot.kind == "file":
        assert isinstance(snapshot.payload, bytes)
        assert snapshot.mode is not None
        atomic_write(
            path,
            snapshot.payload,
            snapshot.mode,
            uid=snapshot.uid if snapshot.uid is not None else 0,
            gid=snapshot.gid if snapshot.gid is not None else 0,
        )
        return
    raise ComponentError("rollback_snapshot_invalid")


def managed_files(source_dir: Path) -> tuple[tuple[Path, Path, int], ...]:
    return (
        (source_dir / "docker-compose.yml", LIVE_COMPOSE, 0o644),
        (source_dir / "clearnet-sub/nginx.conf.template", LIVE_NGINX_TEMPLATE, 0o644),
        (
            source_dir / "clearnet-sub/nginx.service-limits.conf",
            LIVE_NGINX_LIMITS,
            0o644,
        ),
        (source_dir / "host/tiny-vless-abuse-blocks", ABUSE_SCRIPT, 0o755),
        (source_dir / "host/tiny-vless-rate-limit", RATE_SCRIPT, 0o755),
        (
            source_dir / "host/tiny-vless-abuse-blocks.service",
            ABUSE_UNIT,
            0o644,
        ),
        (source_dir / "host/tiny-vless-rate-limit.service", RATE_UNIT, 0o644),
        (source_dir / "host/tiny-vless-rate-limit.timer", RATE_TIMER, 0o644),
    )


def validate_source_contract(source_dir: Path) -> None:
    required = [source for source, _, _ in managed_files(source_dir)]
    required.append(source_dir / "env.example")
    for source in required:
        if not source.is_file() or source.is_symlink():
            raise ComponentError("source_bundle_incomplete")

    compose = (source_dir / "docker-compose.yml").read_text(encoding="utf-8")
    compose_needles = (
        f"image: {EXPECTED_IMAGE}",
        "container_name: tiny-vless-3xui",
        "- /opt/tiny-vless/db:/etc/x-ui",
        "- /etc/arbuzas/secrets/tiny-vless/cert:/root/cert",
        "ipv4_address: 172.30.77.2",
        'cpuset: "0-1"',
        'cpus: "1.50"',
        "mem_limit: 1g",
        "memswap_limit: 1g",
        "pids_limit: 256",
        '0.0.0.0:8447:8447/udp',
        '${PUBLIC_VLESS_ADDRESS:-38.45.80.240}:18448:18448/tcp',
    )
    if not all(needle in compose for needle in compose_needles):
        raise ComponentError("source_compose_contract_mismatch")
    if ":443:443/tcp" in compose or ":443:443/udp" in compose:
        raise ComponentError("source_standard_vpn_port_present")
    if "./db:/etc/x-ui" in compose or "./cert:/root/cert" in compose:
        raise ComponentError("source_compose_uses_relative_state")

    rate_unit = (source_dir / "host/tiny-vless-rate-limit.service").read_text(
        encoding="utf-8"
    )
    if "RemainAfterExit" in rate_unit or "Type=oneshot" not in rate_unit:
        raise ComponentError("source_rate_unit_not_recurring")
    rate_script = (source_dir / "host/tiny-vless-rate-limit").read_text(
        encoding="utf-8"
    )
    rate_script_needles = (
        'RATE="${RATE:-100mbit}"',
        'BURST="${BURST:-2mb}"',
        'LATENCY="${LATENCY:-50ms}"',
        'SYS_CLASS_NET_ROOT="${SYS_CLASS_NET_ROOT:-/sys/class/net}"',
        "healthy_configuration()",
        'tc -j filter show dev "${host_veth}" parent ffff:',
        'tc qdisc change dev "${device}" root tbf',
        "if healthy_configuration; then",
    )
    if not all(needle in rate_script for needle in rate_script_needles):
        raise ComponentError("source_rate_script_contract_mismatch")
    timer = (source_dir / "host/tiny-vless-rate-limit.timer").read_text(encoding="utf-8")
    if "OnBootSec=45s" not in timer or "OnUnitActiveSec=2min" not in timer:
        raise ComponentError("source_rate_timer_mismatch")

    env_names = set()
    for raw_line in (source_dir / "env.example").read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        name, separator, value = line.partition("=")
        if not separator or value:
            raise ComponentError("source_env_example_contains_value")
        env_names.add(name)
    expected_names = {
        "XUI_INIT_WEB_BASE_PATH",
        "XUI_WEB_BASE_PATH",
        "XUI_USERNAME",
        "XUI_PASSWORD",
        "XUI_API_TOKEN",
        "LOCAL_PANEL_PORT",
        "PUBLIC_VLESS_PORT",
        "PUBLIC_VLESS_ADDRESS",
    }
    if env_names != expected_names:
        raise ComponentError("source_env_example_mismatch")


def stage_managed_files(
    source_dir: Path,
) -> tuple[dict[Path, FileSnapshot], set[Path]]:
    snapshots: dict[Path, FileSnapshot] = {}
    changed: set[Path] = set()
    try:
        for source, destination, mode in managed_files(source_dir):
            snapshot = snapshot_file(destination)
            snapshots[destination] = snapshot
            desired = source.read_bytes()
            if (
                snapshot.kind != "file"
                or snapshot.payload != desired
                or snapshot.mode != mode
                or snapshot.uid != 0
                or snapshot.gid != 0
            ):
                changed.add(destination)
                atomic_write(destination, desired, mode)
    except Exception:
        restore_managed_files(snapshots)
        raise
    return snapshots, changed


def restore_managed_files(snapshots: dict[Path, FileSnapshot]) -> None:
    for path in reversed(tuple(snapshots)):
        restore_file(path, snapshots[path])


def snapshot_to_json(snapshot: FileSnapshot) -> dict[str, object]:
    payload: str | None
    if isinstance(snapshot.payload, bytes):
        payload = base64.b64encode(snapshot.payload).decode("ascii")
    elif isinstance(snapshot.payload, str):
        payload = snapshot.payload
    else:
        payload = None
    return {
        "kind": snapshot.kind,
        "payload": payload,
        "payload_encoding": "base64" if isinstance(snapshot.payload, bytes) else "text",
        "mode": snapshot.mode,
        "uid": snapshot.uid,
        "gid": snapshot.gid,
    }


def snapshot_from_json(value: object) -> FileSnapshot:
    if not isinstance(value, dict):
        raise ComponentError("pending_rollback_invalid")
    kind = value.get("kind")
    payload_value = value.get("payload")
    encoding = value.get("payload_encoding")
    if kind not in {"missing", "file", "symlink"}:
        raise ComponentError("pending_rollback_invalid")
    payload: bytes | str | None
    if kind == "file":
        if not isinstance(payload_value, str) or encoding != "base64":
            raise ComponentError("pending_rollback_invalid")
        try:
            payload = base64.b64decode(payload_value, validate=True)
        except ValueError as error:
            raise ComponentError("pending_rollback_invalid") from error
    elif kind == "symlink":
        if not isinstance(payload_value, str) or encoding != "text":
            raise ComponentError("pending_rollback_invalid")
        payload = payload_value
    else:
        payload = None
    integer_fields: list[int | None] = []
    for name in ("mode", "uid", "gid"):
        field = value.get(name)
        if field is not None and (not isinstance(field, int) or field < 0):
            raise ComponentError("pending_rollback_invalid")
        integer_fields.append(field)
    return FileSnapshot(kind, payload, *integer_fields)


def write_pending_rollback(
    previous_image_id: str,
    previous_image_reference: str,
    snapshots: dict[Path, FileSnapshot],
    unit_states: dict[str, UnitState],
) -> None:
    if PENDING_ROLLBACK.exists() or PENDING_ROLLBACK.is_symlink():
        raise ComponentError("pending_rollback_exists")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", previous_image_id):
        raise ComponentError("rollback_image_invalid")
    if (
        not previous_image_reference
        or len(previous_image_reference) > 512
        or re.search(r"[\r\n\x00]", previous_image_reference)
    ):
        raise ComponentError("rollback_image_reference_invalid")
    allowed_paths = {str(destination) for _, destination, _ in managed_files(Path("/"))}
    if set(map(str, snapshots)) != allowed_paths:
        raise ComponentError("pending_rollback_snapshot_incomplete")
    allowed_units = {
        "tiny-vless-abuse-blocks.service",
        "tiny-vless-rate-limit.service",
        "tiny-vless-rate-limit.timer",
    }
    if set(unit_states) != allowed_units:
        raise ComponentError("pending_rollback_unit_state_incomplete")
    payload = {
        "version": 1,
        "previous_image_id": previous_image_id,
        "previous_image_reference": previous_image_reference,
        "files": {str(path): snapshot_to_json(snapshot) for path, snapshot in snapshots.items()},
        "units": {
            unit: {"enabled": state.enabled, "active": state.active}
            for unit, state in unit_states.items()
        },
    }
    PENDING_ROLLBACK.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chown(PENDING_ROLLBACK.parent, 0, 0)
    os.chmod(PENDING_ROLLBACK.parent, 0o700)
    atomic_write(
        PENDING_ROLLBACK,
        (json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8"),
        0o600,
    )


def load_pending_rollback() -> tuple[str, str, dict[Path, FileSnapshot], dict[str, UnitState]]:
    if not PENDING_ROLLBACK.is_file() or PENDING_ROLLBACK.is_symlink():
        raise ComponentError("pending_rollback_missing")
    if stat.S_IMODE(PENDING_ROLLBACK.stat().st_mode) != 0o600:
        raise ComponentError("pending_rollback_mode_invalid")
    try:
        payload = json.loads(PENDING_ROLLBACK.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ComponentError("pending_rollback_invalid") from error
    if not isinstance(payload, dict) or payload.get("version") != 1:
        raise ComponentError("pending_rollback_invalid")
    image_id = payload.get("previous_image_id")
    image_reference = payload.get("previous_image_reference")
    if not isinstance(image_id, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", image_id):
        raise ComponentError("pending_rollback_invalid")
    if not isinstance(image_reference, str) or not image_reference:
        raise ComponentError("pending_rollback_invalid")
    raw_files = payload.get("files")
    if not isinstance(raw_files, dict):
        raise ComponentError("pending_rollback_invalid")
    allowed_paths = {str(destination) for _, destination, _ in managed_files(Path("/"))}
    if set(raw_files) != allowed_paths:
        raise ComponentError("pending_rollback_invalid")
    snapshots = {Path(path): snapshot_from_json(value) for path, value in raw_files.items()}
    raw_units = payload.get("units")
    if not isinstance(raw_units, dict):
        raise ComponentError("pending_rollback_invalid")
    allowed_units = {
        "tiny-vless-abuse-blocks.service",
        "tiny-vless-rate-limit.service",
        "tiny-vless-rate-limit.timer",
    }
    if set(raw_units) != allowed_units:
        raise ComponentError("pending_rollback_invalid")
    unit_states: dict[str, UnitState] = {}
    for unit, raw_state in raw_units.items():
        if not isinstance(raw_state, dict):
            raise ComponentError("pending_rollback_invalid")
        enabled = raw_state.get("enabled")
        active = raw_state.get("active")
        if not isinstance(enabled, bool) or not isinstance(active, bool):
            raise ComponentError("pending_rollback_invalid")
        unit_states[unit] = UnitState(enabled=enabled, active=active)
    return image_id, image_reference, snapshots, unit_states


def clear_pending_rollback() -> None:
    if PENDING_ROLLBACK.is_symlink():
        raise ComponentError("pending_rollback_invalid")
    if PENDING_ROLLBACK.exists():
        PENDING_ROLLBACK.unlink()


def ensure_secret_parent() -> None:
    CANONICAL_SECRET_DIR.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chown(CANONICAL_SECRET_DIR, 0, 0)
    os.chmod(CANONICAL_SECRET_DIR, 0o700)
    CANONICAL_ENV.parent.mkdir(parents=True, exist_ok=True)


def copy_secret_file_once(source: Path, destination: Path) -> bool:
    if not source.is_file():
        raise ComponentError("legacy_secret_missing")
    content = source.read_bytes()
    if not content:
        raise ComponentError("legacy_secret_empty")
    if destination.is_symlink():
        raise ComponentError("canonical_secret_is_symlink")
    if destination.exists():
        if not destination.is_file() or destination.read_bytes() != content:
            raise ComponentError("canonical_secret_conflict")
        os.chown(destination, 0, 0)
        os.chmod(destination, 0o600)
        return False
    atomic_write(destination, content, 0o600)
    return True


def secret_tree_manifest(path: Path) -> dict[str, bytes]:
    if path.is_symlink():
        path = path.resolve(strict=True)
    if not path.is_dir():
        raise ComponentError("legacy_certificate_directory_missing")
    manifest: dict[str, bytes] = {}
    for item in sorted(path.rglob("*")):
        relative = item.relative_to(path).as_posix()
        if item.is_symlink():
            raise ComponentError("certificate_tree_contains_symlink")
        if item.is_dir():
            continue
        if not item.is_file():
            raise ComponentError("certificate_tree_contains_special_file")
        manifest[relative] = item.read_bytes()
    if not manifest or any(not value for value in manifest.values()):
        raise ComponentError("certificate_tree_empty")
    return manifest


def harden_secret_tree(path: Path) -> None:
    os.chown(path, 0, 0)
    os.chmod(path, 0o700)
    for item in path.rglob("*"):
        if item.is_dir():
            os.chown(item, 0, 0)
            os.chmod(item, 0o700)
        elif item.is_file() and not item.is_symlink():
            os.chown(item, 0, 0)
            os.chmod(item, 0o600)


def copy_certificate_tree_once(source: Path, destination: Path) -> bool:
    source_manifest = secret_tree_manifest(source)
    if destination.is_symlink():
        raise ComponentError("canonical_certificate_is_symlink")
    if destination.exists():
        if secret_tree_manifest(destination) != source_manifest:
            raise ComponentError("canonical_certificate_conflict")
        harden_secret_tree(destination)
        return False

    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = Path(tempfile.mkdtemp(prefix=f".{destination.name}.", dir=destination.parent))
    try:
        for relative, content in source_manifest.items():
            target = temporary / relative
            atomic_write(target, content, 0o600)
        harden_secret_tree(temporary)
        os.replace(temporary, destination)
    finally:
        if temporary.exists():
            shutil.rmtree(temporary)
    return True


def begin_compatibility_swap(path: Path, target: Path) -> CompatibilitySwap:
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.is_symlink():
        try:
            if path.resolve(strict=True) == target.resolve(strict=True):
                return CompatibilitySwap(path, target, None, False)
        except OSError:
            pass

    backup: Path | None = None
    if path.is_symlink() or path.exists():
        backup = path.parent / f".{path.name}.arbuzas-pre-adopt-{uuid.uuid4().hex}"
        os.replace(path, backup)
    try:
        path.symlink_to(target, target_is_directory=target.is_dir())
        os.lchown(path, 0, 0)
    except Exception:
        if path.is_symlink() or path.exists():
            path.unlink()
        if backup is not None:
            os.replace(backup, path)
        raise
    return CompatibilitySwap(path, target, backup, True)


def remove_created_secret(path: Path) -> None:
    if path.is_dir() and not path.is_symlink():
        shutil.rmtree(path)
    elif path.is_symlink() or path.exists():
        path.unlink()


def create_database_backup() -> None:
    if not DB_PATH.is_file() or DB_PATH.is_symlink():
        raise ComponentError("database_missing")
    BACKUP_DIR.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chown(BACKUP_DIR, 0, 0)
    os.chmod(BACKUP_DIR, 0o700)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    destination = BACKUP_DIR / f"x-ui-umbrella-{timestamp}.db"
    descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    os.close(descriptor)
    source = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True, timeout=30)
    target = sqlite3.connect(destination, timeout=30)
    try:
        source.backup(target)
        target.commit()
        row = target.execute("PRAGMA integrity_check").fetchone()
        if row is None or row[0] != "ok":
            raise ComponentError("database_backup_integrity_failed")
    except Exception:
        target.close()
        source.close()
        destination.unlink(missing_ok=True)
        raise
    else:
        target.close()
        source.close()
    os.chown(destination, 0, 0)
    os.chmod(destination, 0o600)


def unit_state(unit: str) -> UnitState:
    return UnitState(
        enabled=command_succeeds(["systemctl", "is-enabled", "--quiet", unit]),
        active=command_succeeds(["systemctl", "is-active", "--quiet", unit]),
    )


def capture_unit_states() -> dict[str, UnitState]:
    return {
        unit: unit_state(unit)
        for unit in (
            "tiny-vless-abuse-blocks.service",
            "tiny-vless-rate-limit.service",
            "tiny-vless-rate-limit.timer",
        )
    }


def restore_unit_states(states: dict[str, UnitState]) -> None:
    command(["systemctl", "daemon-reload"])
    for unit, previous in states.items():
        if previous.enabled:
            command(["systemctl", "enable", unit])
        else:
            command(["systemctl", "disable", unit], check=False)
        if previous.active:
            command(["systemctl", "restart", unit], check=False, timeout=180)
        else:
            command(["systemctl", "stop", unit], check=False)


def activate_host_policies() -> None:
    command(["systemctl", "daemon-reload"])
    command(["systemctl", "enable", "tiny-vless-abuse-blocks.service"])
    command(["systemctl", "enable", "tiny-vless-rate-limit.timer"])
    command(["systemctl", "restart", "tiny-vless-abuse-blocks.service"])
    command(["systemctl", "restart", "tiny-vless-rate-limit.timer"])
    command(["systemctl", "restart", "tiny-vless-rate-limit.service"], timeout=180)


def refresh_nginx_if_needed(limits_changed: bool) -> None:
    command(["systemctl", "daemon-reload"])
    if limits_changed:
        command(["nginx", "-t"])
        command(["systemctl", "restart", "nginx.service"])
    if not command_succeeds(["systemctl", "is-active", "--quiet", "nginx.service"]):
        raise ComponentError("nginx_inactive")


def restore_nginx_after_rollback() -> None:
    command(["systemctl", "daemon-reload"])
    command(["nginx", "-t"])
    command(["systemctl", "restart", "nginx.service"])


def load_env() -> dict[str, str]:
    if not CANONICAL_ENV.is_file() or CANONICAL_ENV.is_symlink():
        raise ComponentError("canonical_environment_missing")
    values: dict[str, str] = {}
    for raw_line in CANONICAL_ENV.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[7:].lstrip()
        name, separator, value = line.partition("=")
        name = name.strip()
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", name):
            raise ComponentError("canonical_environment_invalid")
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
            value = value[1:-1]
        values[name] = value
    required = (
        "XUI_INIT_WEB_BASE_PATH",
        "XUI_WEB_BASE_PATH",
        "XUI_USERNAME",
        "XUI_PASSWORD",
        "XUI_API_TOKEN",
        "LOCAL_PANEL_PORT",
        "PUBLIC_VLESS_PORT",
    )
    if any(not values.get(name) for name in required):
        raise ComponentError("canonical_environment_incomplete")
    return values


def ensure_canonical_ready() -> None:
    load_env()
    if stat.S_IMODE(CANONICAL_ENV.stat().st_mode) != 0o600:
        raise ComponentError("canonical_environment_mode_invalid")
    for secret in (CANONICAL_CAPABILITY, CANONICAL_LEGACY_ID):
        if not secret.is_file() or secret.is_symlink() or not secret.read_bytes():
            raise ComponentError("canonical_secret_missing")
        if stat.S_IMODE(secret.stat().st_mode) != 0o600:
            raise ComponentError("canonical_secret_mode_invalid")
    secret_tree_manifest(CANONICAL_CERT_DIR)
    for item in CANONICAL_CERT_DIR.rglob("*"):
        if item.is_file() and stat.S_IMODE(item.stat().st_mode) != 0o600:
            raise ComponentError("canonical_certificate_mode_invalid")


def compatibility_links_valid() -> bool:
    pairs = (*SECRET_FILE_LINKS, (LEGACY_CERT_DIR, CANONICAL_CERT_DIR))
    for path, target in pairs:
        if not path.is_symlink():
            return False
        try:
            if path.resolve(strict=True) != target.resolve(strict=True):
                return False
        except OSError:
            return False
    return True


def inspect_container() -> dict[str, object]:
    raw = command(["docker", "inspect", CONTAINER], capture=True)
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as error:
        raise ComponentError("container_inspect_invalid") from error
    if not isinstance(payload, list) or len(payload) != 1 or not isinstance(payload[0], dict):
        raise ComponentError("container_inspect_invalid")
    return payload[0]


def compose_command(source: Path, *arguments: str, override: Path | None = None) -> None:
    argv = [
        "docker",
        "compose",
        "--project-name",
        PROJECT,
        "--env-file",
        str(CANONICAL_ENV),
        "-f",
        str(source),
    ]
    if override is not None:
        argv.extend(("-f", str(override)))
    argv.extend(arguments)
    command(argv, timeout=300)


def recreate_container(*, override: Path | None = None, pull_never: bool = False) -> None:
    arguments = ["up", "-d", "--force-recreate", "--no-deps"]
    if pull_never:
        arguments.extend(("--pull", "never"))
    arguments.append(SERVICE)
    compose_command(LIVE_COMPOSE, *arguments, override=override)


def wait_for_container_running(expected_image_id: str | None = None) -> None:
    for _ in range(60):
        try:
            inspect = inspect_container()
        except ComponentError:
            time.sleep(1)
            continue
        state = inspect.get("State", {})
        image_id = inspect.get("Image")
        if isinstance(state, dict) and state.get("Running") is True:
            if expected_image_id is None or image_id == expected_image_id:
                return
        time.sleep(1)
    raise ComponentError("container_not_running")


def rollback_container(previous_image_id: str, previous_image_reference: str) -> None:
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", previous_image_id):
        raise ComponentError("rollback_image_invalid")
    if (
        not previous_image_reference
        or len(previous_image_reference) > 512
        or re.search(r"[\r\n\x00]", previous_image_reference)
    ):
        raise ComponentError("rollback_image_reference_invalid")
    fd, override_name = tempfile.mkstemp(
        prefix="tiny-vless-rollback-", suffix=".yml", dir="/run"
    )
    override = Path(override_name)
    try:
        os.fchmod(fd, 0o600)
        content = (
            "services:\n"
            f"  {SERVICE}:\n"
            f"    image: {json.dumps(previous_image_reference)}\n"
        ).encode("utf-8")
        with os.fdopen(fd, "wb") as handle:
            handle.write(content)
            handle.flush()
            os.fsync(handle.fileno())
        recreate_container(override=override, pull_never=True)
        wait_for_container_running(previous_image_id)
    finally:
        if override.exists():
            override.unlink()


def rollback_pending_transaction() -> None:
    image_id, image_reference, snapshots, unit_states = load_pending_rollback()
    restore_managed_files(snapshots)
    restore_nginx_after_rollback()
    rollback_container(image_id, image_reference)
    previous_timer = unit_states["tiny-vless-rate-limit.timer"]
    if previous_timer.enabled or previous_timer.active:
        command([str(RATE_SCRIPT)], timeout=180)
    restore_unit_states(unit_states)
    clear_pending_rollback()


def commit_pending_transaction() -> None:
    load_pending_rollback()
    clear_pending_rollback()


def validate_database() -> dict[str, int]:
    if not DB_PATH.is_file() or DB_PATH.is_symlink():
        raise ComponentError("database_missing")
    connection = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True, timeout=30)
    try:
        integrity = connection.execute("PRAGMA integrity_check").fetchone()
        if integrity is None or integrity[0] != "ok":
            raise ComponentError("database_integrity_failed")
        inbound_rows = connection.execute(
            "SELECT protocol, port, remark FROM inbounds WHERE enable = 1"
        ).fetchall()
        protocol_counts: dict[str, int] = {}
        profile_endpoints: list[tuple[str, int]] = []
        remarks: set[str] = set()
        for protocol, port, remark in inbound_rows:
            normalized = str(protocol).lower()
            protocol_counts[normalized] = protocol_counts.get(normalized, 0) + 1
            profile_endpoints.append((normalized, int(port)))
            remarks.add(str(remark))
        if len(inbound_rows) != EXPECTED_ENABLED_INBOUNDS:
            raise ComponentError("enabled_inbound_count_mismatch")
        if len(remarks) != EXPECTED_ENABLED_INBOUNDS:
            raise ComponentError("inbound_names_not_unique")
        if protocol_counts != EXPECTED_PROTOCOLS:
            raise ComponentError("protocol_class_count_mismatch")
        if tuple(sorted(profile_endpoints)) != EXPECTED_PROFILE_ENDPOINTS:
            raise ComponentError("profile_endpoint_mismatch")
        enabled_clients, identities = connection.execute(
            """
            SELECT COUNT(*), COUNT(DISTINCT sub_id)
            FROM clients
            WHERE enable = 1 AND sub_id IS NOT NULL AND sub_id != ''
            """
        ).fetchone()
        all_enabled_clients = connection.execute(
            "SELECT COUNT(*) FROM clients WHERE enable = 1"
        ).fetchone()[0]
        if int(enabled_clients) != EXPECTED_ENABLED_INBOUNDS:
            raise ComponentError("enabled_client_count_mismatch")
        if int(all_enabled_clients) != int(enabled_clients):
            raise ComponentError("enabled_client_without_subscription")
        if int(identities) != 1:
            raise ComponentError("subscription_identity_count_mismatch")
        attachments, attached_clients, attached_inbounds = connection.execute(
            """
            SELECT COUNT(*), COUNT(DISTINCT ci.client_id), COUNT(DISTINCT ci.inbound_id)
            FROM client_inbounds AS ci
            JOIN clients AS c ON c.id = ci.client_id
            JOIN inbounds AS i ON i.id = ci.inbound_id
            WHERE c.enable = 1 AND i.enable = 1
            """
        ).fetchone()
        if (
            int(attachments) != EXPECTED_ENABLED_INBOUNDS
            or int(attached_clients) != EXPECTED_ENABLED_INBOUNDS
            or int(attached_inbounds) != EXPECTED_ENABLED_INBOUNDS
        ):
            raise ComponentError("enabled_client_attachment_mismatch")
    finally:
        connection.close()
    return {
        "enabled_inbounds": EXPECTED_ENABLED_INBOUNDS,
        "enabled_clients": EXPECTED_ENABLED_INBOUNDS,
        "subscription_identities": 1,
        **{f"protocol_{name}": count for name, count in EXPECTED_PROTOCOLS.items()},
    }


def database_semantic_fingerprint() -> str:
    """Fingerprint profile definitions while ignoring live traffic counters."""
    if not DB_PATH.is_file() or DB_PATH.is_symlink():
        raise ComponentError("database_missing")
    excluded = {"up", "down", "last_traffic_reset_time", "last_online"}
    connection = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True, timeout=30)
    semantic_tables: list[tuple[str, tuple[str, ...], tuple[tuple[object, ...], ...]]] = []
    try:
        for table in ("inbounds", "clients"):
            schema = connection.execute(f"PRAGMA table_info({table})").fetchall()
            columns = tuple(
                str(row[1])
                for row in schema
                if str(row[1]).lower() not in excluded
                and re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", str(row[1]))
            )
            if not columns or "id" not in columns:
                raise ComponentError("database_semantic_schema_invalid")
            selection = ", ".join(f'"{column}"' for column in columns)
            rows = tuple(
                tuple(row)
                for row in connection.execute(
                    f'SELECT {selection} FROM "{table}" ORDER BY "id"'
                ).fetchall()
            )
            semantic_tables.append((table, columns, rows))
    finally:
        connection.close()
    return hashlib.sha256(repr(tuple(semantic_tables)).encode("utf-8")).hexdigest()


def environment_map(inspect: dict[str, object]) -> dict[str, str]:
    config = inspect.get("Config", {})
    items = config.get("Env", []) if isinstance(config, dict) else []
    result: dict[str, str] = {}
    if not isinstance(items, list):
        return result
    for item in items:
        if isinstance(item, str) and "=" in item:
            name, value = item.split("=", 1)
            result[name] = value
    return result


def validate_container() -> None:
    env = load_env()
    if env["LOCAL_PANEL_PORT"] != "12053" or env["PUBLIC_VLESS_PORT"] != "8443":
        raise ComponentError("canonical_port_value_mismatch")
    public_address = env.get("PUBLIC_VLESS_ADDRESS", "38.45.80.240")
    if not re.fullmatch(r"(?:[0-9]{1,3}\.){3}[0-9]{1,3}", public_address):
        raise ComponentError("canonical_public_address_invalid")

    inspect = inspect_container()
    state = inspect.get("State", {})
    config = inspect.get("Config", {})
    host = inspect.get("HostConfig", {})
    network = inspect.get("NetworkSettings", {})
    if not all(isinstance(item, dict) for item in (state, config, host, network)):
        raise ComponentError("container_inspect_incomplete")
    if state.get("Running") is not True:
        raise ComponentError("container_not_running")
    labels = config.get("Labels", {})
    if not isinstance(labels, dict) or labels.get("com.docker.compose.project") != PROJECT:
        raise ComponentError("container_project_mismatch")
    if labels.get("com.docker.compose.service") != SERVICE:
        raise ComponentError("container_service_mismatch")
    if config.get("Image") != EXPECTED_IMAGE:
        raise ComponentError("container_image_mismatch")

    if host.get("NanoCpus") != 1_500_000_000:
        raise ComponentError("container_cpu_limit_mismatch")
    if host.get("Memory") != 1_073_741_824 or host.get("MemorySwap") != 1_073_741_824:
        raise ComponentError("container_memory_limit_mismatch")
    if host.get("PidsLimit") != 256 or host.get("CpusetCpus") != "0-1":
        raise ComponentError("container_process_limit_mismatch")
    cap_add = set(host.get("CapAdd", [])) if isinstance(host.get("CapAdd", []), list) else set()
    if not {"NET_ADMIN", "NET_RAW"}.issubset(cap_add):
        raise ComponentError("container_capability_mismatch")
    security = host.get("SecurityOpt", [])
    if not isinstance(security, list) or "no-new-privileges:true" not in security:
        raise ComponentError("container_security_option_mismatch")

    networks = network.get("Networks", {})
    if not isinstance(networks, dict) or len(networks) != 1:
        raise ComponentError("container_network_mismatch")
    network_details = next(iter(networks.values()))
    if not isinstance(network_details, dict) or network_details.get("IPAddress") != EXPECTED_CONTAINER_IP:
        raise ComponentError("container_fixed_ip_mismatch")

    mounts = inspect.get("Mounts", [])
    mount_map = {
        item.get("Destination"): item
        for item in mounts
        if isinstance(item, dict) and isinstance(item.get("Destination"), str)
    } if isinstance(mounts, list) else {}
    database_mount = mount_map.get("/etc/x-ui")
    certificate_mount = mount_map.get("/root/cert")
    if (
        not isinstance(database_mount, dict)
        or Path(str(database_mount.get("Source", ""))).resolve() != (STACK_DIR / "db").resolve()
        or database_mount.get("RW") is not True
    ):
        raise ComponentError("container_database_mount_mismatch")
    if (
        not isinstance(certificate_mount, dict)
        or Path(str(certificate_mount.get("Source", ""))).resolve() != CANONICAL_CERT_DIR.resolve()
        or certificate_mount.get("RW") is not True
    ):
        raise ComponentError("container_certificate_mount_mismatch")

    expected_bindings = {
        "2053/tcp": [("127.0.0.1", "12053")],
        "2096/tcp": [("127.0.0.1", "12096")],
        "8443/tcp": [("0.0.0.0", "8443")],
        "8446/tcp": [("0.0.0.0", "8446")],
        "18448/tcp": [(public_address, "18448")],
        "8447/udp": [("0.0.0.0", "8447")],
        "8444/udp": [("0.0.0.0", "8444")],
        "8445/udp": [("0.0.0.0", "8445")],
        "51820/udp": [("0.0.0.0", "51820")],
    }
    bindings = host.get("PortBindings", {})
    if not isinstance(bindings, dict) or set(bindings) != set(expected_bindings):
        raise ComponentError("container_port_binding_mismatch")
    for container_port, expected in expected_bindings.items():
        raw = bindings.get(container_port)
        if not isinstance(raw, list):
            raise ComponentError("container_port_binding_mismatch")
        normalized = sorted(
            (
                str(entry.get("HostIp", "")) or "0.0.0.0",
                str(entry.get("HostPort", "")),
            )
            for entry in raw
            if isinstance(entry, dict)
        )
        if normalized != sorted(expected):
            raise ComponentError("container_port_binding_mismatch")

    live_environment = environment_map(inspect)
    for name in (
        "XUI_INIT_WEB_BASE_PATH",
        "XUI_WEB_BASE_PATH",
        "XUI_USERNAME",
        "XUI_PASSWORD",
        "XUI_API_TOKEN",
    ):
        if live_environment.get(name) != env[name]:
            raise ComponentError("container_environment_mismatch")


def validate_managed_files(source_dir: Path) -> dict[str, str]:
    hashes: dict[str, str] = {}
    for index, (source, destination, mode) in enumerate(managed_files(source_dir), start=1):
        if not destination.is_file() or destination.is_symlink():
            raise ComponentError("managed_file_missing")
        if source.read_bytes() != destination.read_bytes():
            raise ComponentError("managed_file_drift")
        status = destination.stat()
        if stat.S_IMODE(status.st_mode) != mode or status.st_uid != 0 or status.st_gid != 0:
            raise ComponentError("managed_file_mode_mismatch")
        hashes[f"managed_{index}_sha256"] = sha256_file(source)
    return hashes


def validate_tcp_listeners(clearnet_port: int) -> None:
    env = load_env()
    public_address = env.get("PUBLIC_VLESS_ADDRESS", "38.45.80.240")
    output = command(["ss", "-H", "-lnt"], capture=True)
    required = (
        "127.0.0.1:12053",
        "127.0.0.1:12096",
        "0.0.0.0:8443",
        "0.0.0.0:8446",
        f"{public_address}:18448",
        f"{public_address}:{clearnet_port}",
    )
    if any(item not in output for item in required):
        raise ComponentError("tcp_listener_mismatch")


def validate_udp_listeners() -> None:
    output = command(["ss", "-H", "-lun"], capture=True)
    required = {8447, 8444, 8445, 51820}
    observed: set[int] = set()
    for line in output.splitlines():
        columns = line.split()
        if len(columns) < 5:
            continue
        # With numeric `ss -H -lun` output, the local address is the
        # penultimate column and the peer address is last.
        match = re.search(r":(\d+)$", columns[-2])
        if match:
            observed.add(int(match.group(1)))
    if not required.issubset(observed):
        raise ComponentError("udp_listener_mismatch")
    if 443 in observed:
        raise ComponentError("standard_hysteria_listener_present")


def detect_clearnet_port(public_address: str) -> int:
    if not NGINX_SITE.is_file() or NGINX_SITE.is_symlink():
        raise ComponentError("nginx_site_missing")
    rendered = NGINX_SITE.read_text(encoding="utf-8")
    match = re.search(
        rf"^\s*listen\s+{re.escape(public_address)}:(\d+)\s+default_server;",
        rendered,
        re.MULTILINE,
    )
    if match is None:
        raise ComponentError("nginx_listener_not_detected")
    port = int(match.group(1))
    if port < 1 or port > 65535:
        raise ComponentError("nginx_listener_not_detected")
    return port


def subscription_route_state() -> tuple[str, str]:
    connection = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True, timeout=30)
    try:
        settings = dict(connection.execute("SELECT key, value FROM settings"))
    finally:
        connection.close()
    raw_route = "/" + str(settings.get("subPath", "")).strip("/") + "/"
    if raw_route == "//" or not re.fullmatch(r"/[0-9A-Za-z._~/-]+/", raw_route):
        raise ComponentError("subscription_route_invalid")
    legacy_id = CANONICAL_LEGACY_ID.read_text(encoding="ascii").strip()
    if not re.fullmatch(
        r"(?:[0-9a-z]{16}|[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12})",
        legacy_id,
    ):
        raise ComponentError("subscription_identity_invalid")
    return raw_route, legacy_id


def http_probe(
    address: str,
    port: int,
    path: str,
    *,
    method: str = "GET",
) -> tuple[int, dict[str, str], bytes]:
    connection = http.client.HTTPConnection(address, port, timeout=10)
    try:
        connection.request(
            method,
            path,
            headers={
                "Host": address,
                "Accept": "text/plain, application/octet-stream",
                "Accept-Encoding": "",
                "Connection": "close",
                "User-Agent": "arbuzas-tiny-vless-validation/1",
            },
        )
        response = connection.getresponse()
        body = response.read()
        headers = {key.lower(): value for key, value in response.getheaders()}
        return response.status, headers, body
    except (OSError, http.client.HTTPException) as error:
        raise ComponentError("subscription_http_probe_failed") from error
    finally:
        connection.close()


def validate_clearnet_response(public_address: str, port: int) -> None:
    raw_route, legacy_id = subscription_route_state()
    capability = CANONICAL_CAPABILITY.read_text(encoding="ascii").strip()
    if not re.fullmatch(r"[0-9a-f]{64}", capability):
        raise ComponentError("capability_shape_invalid")
    upstream_status, _, upstream_body = http_probe(
        "127.0.0.1",
        12096,
        raw_route + legacy_id,
    )
    legacy_path = f"/{capability}.dat"
    canonical_path = f"/{capability}/raw/{legacy_id}"
    legacy_status, _, legacy_body = http_probe(public_address, port, legacy_path)
    canonical_status, canonical_headers, canonical_body = http_probe(
        public_address,
        port,
        canonical_path,
    )
    wrong_status, _, _ = http_probe(
        public_address,
        port,
        f"/{secrets.token_hex(32)}.dat",
    )
    query_status, _, _ = http_probe(
        public_address,
        port,
        canonical_path + "?view=html",
    )
    post_status, _, _ = http_probe(
        public_address,
        port,
        canonical_path,
        method="POST",
    )
    if upstream_status != 200 or not upstream_body:
        raise ComponentError("subscription_upstream_invalid")
    if legacy_status != 200 or canonical_status != 200:
        raise ComponentError("subscription_public_status_invalid")
    if legacy_body != upstream_body or canonical_body != upstream_body:
        raise ComponentError("subscription_public_body_mismatch")
    if wrong_status != 404 or query_status != 404 or post_status != 404:
        raise ComponentError("subscription_concealment_invalid")
    cache_control = canonical_headers.get("cache-control", "").lower()
    if (
        "no-store" not in cache_control
        or canonical_headers.get("referrer-policy", "").lower() != "no-referrer"
        or "noindex" not in canonical_headers.get("x-robots-tag", "").lower()
        or "set-cookie" in canonical_headers
        or "profile-web-page-url" in canonical_headers
    ):
        raise ComponentError("subscription_headers_invalid")


def validate_nginx(requested_port: int | None) -> int:
    env = load_env()
    public_address = env.get("PUBLIC_VLESS_ADDRESS", "38.45.80.240")
    port = detect_clearnet_port(public_address)
    if requested_port is not None and requested_port != port:
        raise ComponentError("nginx_listener_port_mismatch")
    rendered = NGINX_SITE.read_text(encoding="utf-8")
    capability = CANONICAL_CAPABILITY.read_text(encoding="ascii").strip()
    legacy_id = CANONICAL_LEGACY_ID.read_text(encoding="ascii").strip()
    if not capability or not legacy_id:
        raise ComponentError("canonical_secret_empty")
    if capability not in rendered or legacy_id not in rendered or "__" in rendered:
        raise ComponentError("nginx_rendered_config_mismatch")
    if stat.S_IMODE(NGINX_SITE.stat().st_mode) != 0o600:
        raise ComponentError("nginx_site_mode_mismatch")
    if not NGINX_SITE_ENABLED.is_symlink():
        raise ComponentError("nginx_site_not_enabled")
    try:
        if NGINX_SITE_ENABLED.resolve(strict=True) != NGINX_SITE.resolve(strict=True):
            raise ComponentError("nginx_site_not_enabled")
    except OSError as error:
        raise ComponentError("nginx_site_not_enabled") from error
    if not command_succeeds(["nginx", "-t"]):
        raise ComponentError("nginx_config_invalid")
    if not command_succeeds(["systemctl", "is-active", "--quiet", "nginx.service"]):
        raise ComponentError("nginx_inactive")
    properties = command(
        [
            "systemctl",
            "show",
            "nginx.service",
            "--property=CPUQuotaPerSecUSec",
            "--property=MemoryHigh",
            "--property=MemoryMax",
            "--property=TasksMax",
        ],
        capture=True,
    )
    parsed = dict(line.split("=", 1) for line in properties.splitlines() if "=" in line)
    if parsed.get("CPUQuotaPerSecUSec") not in {"100ms", "100000us"}:
        raise ComponentError("nginx_cpu_limit_mismatch")
    if parsed.get("MemoryHigh") not in {"33554432", "32M"}:
        raise ComponentError("nginx_memory_limit_mismatch")
    if parsed.get("MemoryMax") not in {"67108864", "64M"}:
        raise ComponentError("nginx_memory_limit_mismatch")
    if parsed.get("TasksMax") != "32":
        raise ComponentError("nginx_task_limit_mismatch")
    validate_clearnet_response(public_address, port)
    return port


def validate_firewall() -> None:
    if not command_succeeds(["iptables", "-w", "-C", "DOCKER-USER", "-j", "TINYVLESS-FILTER"]):
        raise ComponentError("firewall_jump_missing")
    jumps = command(["iptables", "-w", "-S", "DOCKER-USER"], capture=True)
    if jumps.splitlines().count("-A DOCKER-USER -j TINYVLESS-FILTER") != 1:
        raise ComponentError("firewall_jump_count_mismatch")
    mail_rule = [
        "iptables",
        "-w",
        "-C",
        "TINYVLESS-FILTER",
        "-s",
        "172.30.77.2/32",
        "-p",
        "tcp",
        "-m",
        "multiport",
        "--dports",
        "25,465,587",
        "-j",
        "REJECT",
        "--reject-with",
        "tcp-reset",
    ]
    if not command_succeeds(mail_rule):
        raise ComponentError("firewall_mail_block_missing")
    for destination in EXPECTED_RESERVED_DESTINATIONS:
        if not command_succeeds(
            [
                "iptables",
                "-w",
                "-C",
                "TINYVLESS-FILTER",
                "-s",
                "172.30.77.2/32",
                "-d",
                destination,
                "-j",
                "REJECT",
            ]
        ):
            raise ComponentError("firewall_reserved_block_missing")
    rules = command(["iptables", "-w", "-S", "TINYVLESS-FILTER"], capture=True)
    nonempty = [line for line in rules.splitlines() if line.strip()]
    if not nonempty or nonempty[-1] != "-A TINYVLESS-FILTER -j RETURN":
        raise ComponentError("firewall_return_missing")


def resolve_current_veth() -> str:
    inspect = inspect_container()
    state = inspect.get("State", {})
    pid = state.get("Pid") if isinstance(state, dict) else None
    if not isinstance(pid, int) or pid < 1:
        raise ComponentError("container_pid_invalid")
    iflink = command(
        ["docker", "exec", CONTAINER, "sh", "-c", "cat /sys/class/net/eth0/iflink"],
        capture=True,
    ).strip()
    if not re.fullmatch(r"[0-9]+", iflink):
        raise ComponentError("container_iflink_invalid")
    matches = []
    for index_file in Path("/sys/class/net").glob("*/ifindex"):
        try:
            if index_file.read_text(encoding="ascii").strip() == iflink:
                matches.append(index_file.parent.name)
        except OSError:
            continue
    if len(matches) != 1:
        raise ComponentError("container_veth_not_found")
    interface = matches[0]
    if interface in {"", "eth0", "tailscale0", "lo", IFB_INTERFACE}:
        raise ComponentError("container_veth_unsafe")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", interface):
        raise ComponentError("container_veth_unsafe")
    return interface


def tbf_is_100mbit(output: str) -> bool:
    for raw_line in output.lower().splitlines():
        line = " ".join(raw_line.split())
        if (
            "qdisc tbf" in line
            and " root " in f" {line} "
            and "rate 100mbit" in line
            and "burst 2mb" in line
            and "lat 50ms" in line
        ):
            return True
    return False


def rate_filter_is_expected(output: str) -> bool:
    try:
        payload = json.loads(output)
    except json.JSONDecodeError:
        return False
    if not isinstance(payload, list) or len(payload) != 2:
        return False
    detailed = []
    for item in payload:
        if not isinstance(item, dict):
            return False
        if (
            item.get("protocol") != "all"
            or item.get("pref") != 1
            or item.get("kind") != "matchall"
            or item.get("chain") != 0
        ):
            return False
        if isinstance(item.get("options"), dict):
            detailed.append(item)
    if len(detailed) != 1:
        return False
    actions = detailed[0]["options"].get("actions", [])
    if not isinstance(actions, list) or len(actions) != 1:
        return False
    action = actions[0]
    return bool(
        isinstance(action, dict)
        and action.get("order") == 1
        and action.get("kind") == "mirred"
        and action.get("mirred_action") == "redirect"
        and action.get("direction") == "egress"
        and action.get("to_dev") == IFB_INTERFACE
        and isinstance(action.get("control_action"), dict)
        and action["control_action"].get("type") == "stolen"
    )


def validate_rate_limit() -> None:
    interface = resolve_current_veth()
    host_qdisc = command(["tc", "qdisc", "show", "dev", interface], capture=True)
    if not tbf_is_100mbit(host_qdisc) or "qdisc ingress ffff:" not in host_qdisc.lower():
        raise ComponentError("rate_limit_host_qdisc_mismatch")
    host_filter = command(
        ["tc", "-j", "filter", "show", "dev", interface, "parent", "ffff:"],
        capture=True,
    )
    if not rate_filter_is_expected(host_filter):
        raise ComponentError("rate_limit_redirect_missing")
    ifb_qdisc = command(["tc", "qdisc", "show", "dev", IFB_INTERFACE], capture=True)
    if not tbf_is_100mbit(ifb_qdisc):
        raise ComponentError("rate_limit_ifb_qdisc_mismatch")
    if not command_succeeds(["ip", "link", "show", "dev", IFB_INTERFACE]):
        raise ComponentError("rate_limit_ifb_missing")
    if not command_succeeds(
        ["systemctl", "is-enabled", "--quiet", "tiny-vless-rate-limit.timer"]
    ) or not command_succeeds(
        ["systemctl", "is-active", "--quiet", "tiny-vless-rate-limit.timer"]
    ):
        raise ComponentError("rate_limit_timer_inactive")
    timers = command(
        [
            "systemctl",
            "list-timers",
            "--all",
            "--no-legend",
            "--no-pager",
            "tiny-vless-rate-limit.timer",
        ],
        capture=True,
    ).strip()
    if (
        not timers
        or timers.startswith("- -")
        or re.search(r"\bn/a\s+n/a\b", timers, re.IGNORECASE)
    ):
        raise ComponentError("rate_limit_timer_unscheduled")


def flatten_json(value: object, path: tuple[str, ...] = ()) -> list[tuple[tuple[str, ...], object]]:
    rows: list[tuple[tuple[str, ...], object]] = []
    if isinstance(value, dict):
        for key, child in value.items():
            rows.extend(flatten_json(child, path + (str(key),)))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            rows.extend(flatten_json(child, path + (str(index),)))
    else:
        rows.append((path, value))
    return rows


def validate_tailscale() -> None:
    raw = command(["tailscale", "serve", "status", "--json"], capture=True)
    try:
        status = json.loads(raw)
    except json.JSONDecodeError as error:
        raise ComponentError("tailscale_status_invalid") from error
    rows = flatten_json(status)

    def target_exists(exposed_port: int, target_port: int) -> bool:
        exposed = f":{exposed_port}"
        target = f"127.0.0.1:{target_port}"
        return any(
            isinstance(value, str)
            and target in value
            and exposed in "/".join(path)
            for path, value in rows
        )

    if not target_exists(10000, 12053) or not target_exists(443, 12096):
        raise ComponentError("tailscale_target_route_missing")
    serialized = json.dumps(status, sort_keys=True, separators=(",", ":"))
    if any(f":{port}" not in serialized for port in EXPECTED_NON_TARGET_TAILSCALE_PORTS):
        raise ComponentError("tailscale_non_target_route_missing")
    funnel_enabled = any(
        "allowfunnel" in "/".join(path).lower()
        and (":443" in "/".join(path) or (isinstance(value, bool) and value))
        for path, value in rows
    )
    if not funnel_enabled:
        raise ComponentError("tailscale_funnel_not_enabled")


def validate_component(
    source_dir: Path,
    level: str,
    requested_clearnet_port: int | None,
) -> tuple[dict[str, int], dict[str, str], int | None]:
    validate_source_contract(source_dir)
    ensure_canonical_ready()
    if not compatibility_links_valid():
        raise ComponentError("compatibility_links_invalid")
    database_counts = validate_database()
    managed_hashes = validate_managed_files(source_dir)
    validate_container()
    clearnet_port: int | None = None
    if level in {"standard", "full"}:
        clearnet_port = validate_nginx(requested_clearnet_port)
        validate_tcp_listeners(clearnet_port)
        validate_udp_listeners()
        validate_firewall()
        validate_rate_limit()
    if level == "full":
        validate_tailscale()
    return database_counts, managed_hashes, clearnet_port


def adopt(source_dir: Path) -> None:
    require_root()
    validate_source_contract(source_dir)
    ensure_secret_parent()
    created: list[Path] = []
    swaps: list[CompatibilitySwap] = []
    snapshots: dict[Path, FileSnapshot] = {}
    unit_states = capture_unit_states()
    backup_created = False
    try:
        if copy_secret_file_once(LEGACY_ENV, CANONICAL_ENV):
            created.append(CANONICAL_ENV)
        if copy_certificate_tree_once(LEGACY_CERT_DIR, CANONICAL_CERT_DIR):
            created.append(CANONICAL_CERT_DIR)
        if copy_secret_file_once(LEGACY_CAPABILITY, CANONICAL_CAPABILITY):
            created.append(CANONICAL_CAPABILITY)
        if copy_secret_file_once(LEGACY_ID, CANONICAL_LEGACY_ID):
            created.append(CANONICAL_LEGACY_ID)

        for path, target in SECRET_FILE_LINKS:
            swaps.append(begin_compatibility_swap(path, target))
        swaps.append(begin_compatibility_swap(LEGACY_CERT_DIR, CANONICAL_CERT_DIR))

        create_database_backup()
        backup_created = True
        snapshots, changed = stage_managed_files(source_dir)
        refresh_nginx_if_needed(LIVE_NGINX_LIMITS in changed)
        activate_host_policies()
        ensure_canonical_ready()
        if not compatibility_links_valid():
            raise ComponentError("compatibility_links_invalid")
        for swap in swaps:
            swap.commit()
    except Exception as error:
        rollback_ok = True
        try:
            if snapshots:
                restore_managed_files(snapshots)
            for swap in reversed(swaps):
                swap.rollback()
            for path in reversed(created):
                remove_created_secret(path)
            restore_unit_states(unit_states)
            if LIVE_NGINX_LIMITS in snapshots:
                restore_nginx_after_rollback()
        except Exception:
            rollback_ok = False
        if not rollback_ok:
            raise ComponentError("adopt_rollback_failed") from error
        if isinstance(error, ComponentError):
            raise
        raise ComponentError("adopt_failed") from error

    print("tiny_vless_adopted=true")
    print(f"canonical_items_created={len(created)}")
    print("compatibility_links_ready=true")
    print(f"database_backup_created={str(backup_created).lower()}")
    print("container_recreated=false")
    print(f"managed_file_count={len(managed_files(source_dir))}")
    print("firewall_unit_enabled=true")
    print("rate_limit_timer_enabled=true")


def deploy(
    source_dir: Path,
    requested_clearnet_port: int | None,
    *,
    defer_rollback: bool,
) -> None:
    require_root()
    validate_source_contract(source_dir)
    ensure_canonical_ready()
    if not compatibility_links_valid():
        raise ComponentError("compatibility_links_invalid")
    previous = inspect_container()
    previous_state = previous.get("State", {})
    previous_config = previous.get("Config", {})
    if not isinstance(previous_state, dict) or previous_state.get("Running") is not True:
        raise ComponentError("predeploy_container_not_running")
    if not isinstance(previous_config, dict):
        raise ComponentError("predeploy_container_invalid")
    labels = previous_config.get("Labels", {})
    if not isinstance(labels, dict) or labels.get("com.docker.compose.project") != PROJECT:
        raise ComponentError("predeploy_project_mismatch")
    previous_image_id = str(previous.get("Image", ""))
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", previous_image_id):
        raise ComponentError("predeploy_image_invalid")
    previous_image_reference = str(previous_config.get("Image", ""))
    if not previous_image_reference:
        raise ComponentError("predeploy_image_reference_invalid")

    snapshots: dict[Path, FileSnapshot] = {}
    unit_states = capture_unit_states()
    container_recreated = False
    pending_written = False
    semantic_before = database_semantic_fingerprint()
    try:
        create_database_backup()
        snapshots, changed = stage_managed_files(source_dir)
        write_pending_rollback(
            previous_image_id,
            previous_image_reference,
            snapshots,
            unit_states,
        )
        pending_written = True
        refresh_nginx_if_needed(LIVE_NGINX_LIMITS in changed)
        activate_host_policies()
        compose_command(LIVE_COMPOSE, "config", "--quiet")
        container_recreated = True
        recreate_container()
        wait_for_container_running()
        activate_host_policies()
        validate_component(source_dir, "standard", requested_clearnet_port)
        if database_semantic_fingerprint() != semantic_before:
            raise ComponentError("profile_state_changed")
    except Exception as error:
        rollback_ok = True
        try:
            if pending_written and defer_rollback:
                rollback_ok = True
            elif pending_written:
                rollback_pending_transaction()
            elif snapshots:
                restore_managed_files(snapshots)
                restore_nginx_after_rollback()
                restore_unit_states(unit_states)
                if container_recreated:
                    rollback_container(previous_image_id, previous_image_reference)
                    activate_host_policies()
        except Exception:
            rollback_ok = False
        if not rollback_ok:
            raise ComponentError("deploy_rollback_failed") from error
        if isinstance(error, ComponentError):
            raise
        raise ComponentError("deploy_failed") from error

    print("tiny_vless_deployed=true")
    print("database_backup_created=true")
    print("container_recreated=true")
    print("rollback_used=false")
    print("firewall_reapplied=true")
    print("rate_limit_reapplied=true")
    print("profile_state_preserved=true")
    print("pending_rollback_ready=true")
    print("standard_validation_passed=true")


def validate_action(
    source_dir: Path,
    level: str,
    requested_clearnet_port: int | None,
) -> None:
    require_root()
    database_counts, managed_hashes, clearnet_port = validate_component(
        source_dir, level, requested_clearnet_port
    )
    print("tiny_vless_valid=true")
    print(f"validation_level={level}")
    print("database_integrity=true")
    for key in sorted(database_counts):
        print(f"{key}={database_counts[key]}")
    print("container_project_valid=true")
    print("container_identity_valid=true")
    print("container_resources_valid=true")
    print("container_mounts_valid=true")
    print("container_listeners_valid=true")
    print("compatibility_links_valid=true")
    for key in sorted(managed_hashes):
        print(f"{key}={managed_hashes[key]}")
    if level in {"standard", "full"}:
        print("nginx_config_valid=true")
        print("nginx_limits_valid=true")
        print("clearnet_listener_detected=true")
        print("subscription_response_valid=true")
        print("subscription_concealment_valid=true")
        print("firewall_valid=true")
        print("bidirectional_100mbit_limit_valid=true")
        print("rate_limit_timer_scheduled=true")
        print(f"clearnet_port_matches_request={str(requested_clearnet_port is None or clearnet_port == requested_clearnet_port).lower()}")
    if level == "full":
        print("tailscale_target_routes_valid=true")
        print("tailscale_non_target_routes_present=true")


def rollback_action() -> None:
    require_root()
    rollback_pending_transaction()
    print("tiny_vless_rolled_back=true")
    print("pending_rollback_cleared=true")


def abort_action() -> None:
    require_root()
    if PENDING_ROLLBACK.is_file() and not PENDING_ROLLBACK.is_symlink():
        rollback_pending_transaction()
        print("tiny_vless_aborted=true")
        print("rollback_applied=true")
    elif PENDING_ROLLBACK.exists() or PENDING_ROLLBACK.is_symlink():
        raise ComponentError("pending_rollback_invalid")
    else:
        print("tiny_vless_aborted=true")
        print("rollback_applied=false")


def commit_action() -> None:
    require_root()
    commit_pending_transaction()
    print("tiny_vless_committed=true")
    print("pending_rollback_cleared=true")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Manage the Tiny VLESS component without exposing private configuration."
    )
    subparsers = parser.add_subparsers(dest="action", required=True)
    default_source = Path(__file__).resolve().parent
    for action in ("adopt", "deploy", "validate", "rollback", "abort", "commit"):
        subparser = subparsers.add_parser(action)
        subparser.add_argument("--source-dir", type=Path, default=default_source)
        subparser.add_argument(
            "--clearnet-port", type=int, default=EXPECTED_CLEARNET_PORT
        )
        if action == "deploy":
            subparser.add_argument("--defer-rollback", action="store_true")
        if action == "validate":
            subparser.add_argument(
                "--level", choices=("fast", "standard", "full"), default="standard"
            )
    return parser.parse_args()


def validate_cli_port(port: int | None) -> None:
    if port is not None and not 1 <= port <= 65535:
        raise ComponentError("clearnet_port_invalid")


def main() -> int:
    args = parse_args()
    action = str(args.action)
    try:
        validate_cli_port(args.clearnet_port)
        source_dir = args.source_dir.resolve(strict=True)
        LOCK_PATH.parent.mkdir(parents=True, exist_ok=True)
        with LOCK_PATH.open("a+b") as lock_handle:
            os.fchmod(lock_handle.fileno(), 0o600)
            fcntl.flock(lock_handle.fileno(), fcntl.LOCK_EX)
            if action == "adopt":
                adopt(source_dir)
            elif action == "deploy":
                deploy(
                    source_dir,
                    args.clearnet_port,
                    defer_rollback=bool(args.defer_rollback),
                )
            elif action == "validate":
                validate_action(source_dir, args.level, args.clearnet_port)
            elif action == "rollback":
                rollback_action()
            elif action == "abort":
                abort_action()
            elif action == "commit":
                commit_action()
            else:
                raise ComponentError("action_invalid")
    except ComponentError as error:
        print(f"tiny_vless_{action}=false error={error.code}", file=sys.stderr)
        return 1
    except Exception:
        print(f"tiny_vless_{action}=false error=unexpected_failure", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
