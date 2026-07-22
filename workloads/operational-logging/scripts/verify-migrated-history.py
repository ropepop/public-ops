#!/usr/bin/env python3
"""Verify imported operational history without printing private row content."""

from __future__ import annotations

import argparse
import json
import re
import sys
import time
from pathlib import Path
from typing import Any


ARCHIVE_EXPIRES_AT_MICROS = 253_402_300_799_999_999
SCHEMA_VERSION = 1
TIMESTAMP_KEYS = (
    "__timestamp_micros_since_unix_epoch__",
    "micros_since_unix_epoch",
    "microsSinceUnixEpoch",
    "timestamp",
)
INTEGER = re.compile(r"^-?[0-9]+$")
RETENTION_CLASS = {
    "deployment": "deployment_30d",
    "pixel": "pixel_24h",
    "ticket": "ticket_6h",
    "chatgpt": "archive",
}
COPIED_FIELDS = (
    "domain",
    "recordType",
    "source",
    "operation",
    "event",
    "level",
    "status",
    "result",
    "scopeId",
    "correlationId",
    "parentId",
    "component",
    "detailJson",
    "durationMillis",
    "totalDurationMillis",
    "count",
    "byteCount",
)


class ParityError(Exception):
    """An intentionally content-free parity failure."""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--batches-dir", required=True)
    parser.add_argument("--target", required=True)
    parser.add_argument("--now-micros", type=int, default=time.time_ns() // 1_000)
    return parser.parse_args()


def integer(value: Any, label: str) -> int:
    if isinstance(value, bool):
        raise ParityError(f"{label} is invalid")
    if isinstance(value, int):
        return value
    if isinstance(value, str) and INTEGER.fullmatch(value.strip()):
        return int(value.strip())
    raise ParityError(f"{label} is invalid")


def timestamp_micros(value: Any, label: str) -> int:
    if isinstance(value, dict):
        for key in TIMESTAMP_KEYS:
            if key in value:
                return timestamp_micros(value[key], label)
        raise ParityError(f"{label} is invalid")
    return integer(value, label)


def load_batches(path: str) -> list[dict[str, Any]]:
    root = Path(path)
    if not root.is_dir():
        raise ParityError("mapped batch directory is unavailable")
    events: list[dict[str, Any]] = []
    for batch_path in sorted(root.glob("batch-*.json")):
        try:
            batch = json.loads(batch_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise ParityError("mapped batch is unreadable") from exc
        if not isinstance(batch, list):
            raise ParityError("mapped batch has an invalid shape")
        for event in batch:
            if not isinstance(event, dict):
                raise ParityError("mapped batch contains an invalid event")
            events.append(event)
    return events


def load_target(path: str) -> dict[str, dict[str, Any]]:
    rows: dict[str, dict[str, Any]] = {}
    try:
        with open(path, "r", encoding="utf-8") as handle:
            for line in handle:
                if not line.strip():
                    continue
                try:
                    envelope = json.loads(line)
                except json.JSONDecodeError as exc:
                    raise ParityError("target capture contains invalid JSON") from exc
                if not isinstance(envelope, dict):
                    raise ParityError("target capture has an invalid envelope")
                update = envelope.get("operationallog_event")
                if update is None:
                    continue
                if not isinstance(update, dict) or not isinstance(update.get("inserts", []), list):
                    raise ParityError("target capture has an invalid event update")
                for row in update.get("inserts", []):
                    if not isinstance(row, dict) or not isinstance(row.get("id"), str):
                        raise ParityError("target capture contains an invalid event")
                    existing = rows.get(row["id"])
                    if existing is not None and existing != row:
                        raise ParityError("target capture contains conflicting duplicate events")
                    rows[row["id"]] = row
    except OSError as exc:
        raise ParityError("target capture is unreadable") from exc
    return rows


def prefixed_id(event: dict[str, Any]) -> str:
    domain = event.get("domain")
    raw_id = event.get("id")
    if not isinstance(domain, str) or domain not in RETENTION_CLASS:
        raise ParityError("mapped event has an invalid domain")
    if not isinstance(raw_id, str) or not raw_id:
        raise ParityError("mapped event has an invalid id")
    return raw_id if raw_id.startswith(f"{domain}:") else f"{domain}:{raw_id}"


def compare_event(expected: dict[str, Any], actual: dict[str, Any]) -> None:
    if actual.get("id") != prefixed_id(expected):
        raise ParityError("target imported event id differs from mapped history")
    if integer(actual.get("schemaVersion"), "target schema version") != SCHEMA_VERSION:
        raise ParityError("target imported event schema version differs")
    if actual.get("writerLabel") != "legacy-import":
        raise ParityError("target imported event writer label differs")
    for field in COPIED_FIELDS:
        expected_value = expected.get(field)
        actual_value = actual.get(field)
        if field in {"durationMillis", "totalDurationMillis", "count", "byteCount"}:
            if integer(actual_value, f"target {field}") != integer(expected_value, f"mapped {field}"):
                raise ParityError("target imported event metric differs from mapped history")
        elif actual_value != expected_value:
            raise ParityError("target imported event content differs from mapped history")
    domain = expected["domain"]
    if actual.get("retentionClass") != RETENTION_CLASS[domain]:
        raise ParityError("target imported event retention class differs")
    if timestamp_micros(actual.get("occurredAt"), "target occurrence timestamp") != integer(
        expected.get("occurredAtMicros"), "mapped occurrence timestamp"
    ):
        raise ParityError("target imported event occurrence timestamp differs")
    expected_expiry = (
        ARCHIVE_EXPIRES_AT_MICROS
        if expected.get("archive") is True
        else integer(expected.get("expiresAtMicros"), "mapped expiry timestamp")
    )
    if timestamp_micros(actual.get("expiresAt"), "target expiry timestamp") != expected_expiry:
        raise ParityError("target imported event expiry timestamp differs")
    timestamp_micros(actual.get("recordedAt"), "target recorded timestamp")


def main() -> int:
    args = parse_args()
    expected = load_batches(args.batches_dir)
    target = load_target(args.target)
    active: list[dict[str, Any]] = []
    expired = 0
    seen: set[str] = set()
    for event in expected:
        event_id = prefixed_id(event)
        if event_id in seen:
            raise ParityError("mapped history contains duplicate target event ids")
        seen.add(event_id)
        archive = event.get("archive") is True
        expiry = integer(event.get("expiresAtMicros"), "mapped expiry timestamp")
        if not archive and expiry <= args.now_micros:
            expired += 1
            continue
        active.append(event)

    verified_by_domain = {domain: 0 for domain in RETENTION_CLASS}
    for event in active:
        actual = target.get(prefixed_id(event))
        if actual is None:
            raise ParityError("target parity is missing retained imported events")
        compare_event(event, actual)
        verified_by_domain[event["domain"]] += 1

    print(f"parity_expected_active={len(active)}")
    print(f"parity_verified={len(active)}")
    print(f"parity_expired_before_check={expired}")
    print(f"parity_target_legacy_rows={len(target)}")
    for domain in ("deployment", "pixel", "ticket", "chatgpt"):
        print(f"parity_{domain}_verified={verified_by_domain[domain]}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ParityError as error:
        print(f"operational history parity: {error}", file=sys.stderr)
        raise SystemExit(2)
    except Exception:
        print("operational history parity: unexpected verification failure", file=sys.stderr)
        raise SystemExit(3)
