#!/usr/bin/env python3
"""Map retained legacy log rows into bounded operational-log import batches.

This helper deliberately has no network access and never prints row content.
Its caller owns private source capture, reducer calls, retry, and cleanup.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import math
import os
import re
import sys
import time
from pathlib import Path
from typing import Any, Iterable


MAX_BATCH_EVENTS = 64
MAX_BATCH_BYTES = 65_536
MAX_DETAIL_BYTES = 1_024
MAX_DURATION_MILLIS = 7 * 24 * 60 * 60 * 1_000
MAX_COUNT = 1_000_000_000
MAX_BYTE_COUNT = 1024 * 1024 * 1024 * 1024

SAFE_TOKEN = re.compile(r"^[A-Za-z0-9._:/@=-]+$")
SAFE_DETAIL_KEY = re.compile(r"^[A-Za-z0-9_-]{1,64}$")
INTEGER = re.compile(r"^-?[0-9]+$")
SUSPICIOUS_TEXT = re.compile(
    r"(?i)(https?://|\b(?:bearer|token|password|secret|cookie|authorization|prompt)\b|"
    r"[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}|\b[0-9]{5,}\b|"
    r"\b[A-Za-z0-9+/=_-]{64,}\b)"
)
PRIVATE_FILESYSTEM_PATH_MARKERS = ("/users/", "/home/", "/root/", "\\users\\")
PRIVATE_TOKEN_PATH_MARKERS = (
    "/users/",
    "/home/",
    "/root/",
    "/private/",
    "/etc/",
    "/var/",
    "/tmp/",
    "/opt/",
    "/srv/",
    "/data/",
    "/data/local/",
    "\\users\\",
)
SENSITIVE_KEY_PARTS = (
    "token",
    "password",
    "secret",
    "authorization",
    "cookie",
    "digits",
    "controlcode",
    "imagebase64",
    "payloadjson",
    "prompt",
    "telegram",
    "userid",
    "chatid",
    "email",
    "session",
    "jwt",
    "credential",
    "privatekey",
    "apikey",
    "resulttext",
    "ocr",
)
TIMESTAMP_KEYS = (
    "__timestamp_micros_since_unix_epoch__",
    "micros_since_unix_epoch",
    "microsSinceUnixEpoch",
    "timestamp",
)
EPOCH = dt.datetime(1970, 1, 1, tzinfo=dt.timezone.utc)

PIXEL_EVENT_TYPES = {
    "app_session",
    "manual_action",
    "component_transition",
    "health_change",
    "setting_change",
    "cleanup_result",
    "scheduling_failure",
    "permission_change",
    "dropped_event_summary",
}
PIXEL_COMPONENTS = {
    "orchestrator",
    "stack",
    "automation",
    "speedtest",
    "cellmapper",
    "ticket_readiness",
    "touch_brightness",
    "cpu",
    "gpu",
    "thermal",
    "permissions",
    "cleanup",
    "scheduler",
    "diagnostics",
    "supervisor",
    "management",
    "ssh",
    "vpn",
    "telemetry",
}
PIXEL_CLEANUP_CATEGORIES = {
    "none",
    "ticket_hierarchy_xml",
    "deployment_action_results",
    "support_bundles",
    "root_command_history",
    "stack_logs",
    "dns_history",
    "retired_artifacts",
    "deployment_archives",
    "app_cache",
}
PIXEL_STATUSES = {
    "unknown",
    "healthy",
    "degraded",
    "failed",
    "stale",
    "enabled",
    "disabled",
    "running",
    "completed",
    "skipped",
    "unavailable",
}
PIXEL_RESULTS = {"none", "ok", "failed", "cancelled", "dropped", "rejected", "retrying"}
PIXEL_PRIORITIES = {"low", "normal", "high", "critical"}
LOG_LEVELS = {"trace", "debug", "info", "warn", "error", "critical", "failed"}


class MigrationError(Exception):
    """An intentionally content-free migration validation error."""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument("--output-dir", required=True)
    parser.add_argument("--now-micros", type=int, default=time.time_ns() // 1_000)
    parser.add_argument("--deployment")
    parser.add_argument("--pixel")
    parser.add_argument("--ticket")
    parser.add_argument("--chatgpt")
    return parser.parse_args()


def load_subscription(path: str, label: str) -> dict[str, list[dict[str, Any]]]:
    tables: dict[str, list[dict[str, Any]]] = {}
    try:
        with open(path, "r", encoding="utf-8") as handle:
            for line_number, line in enumerate(handle, start=1):
                if not line.strip():
                    continue
                try:
                    payload = json.loads(line)
                except json.JSONDecodeError as exc:
                    raise MigrationError(
                        f"{label} source capture has invalid JSON at line {line_number}"
                    ) from exc
                if not isinstance(payload, dict):
                    raise MigrationError(f"{label} source capture has an invalid envelope")
                for table_name, update in payload.items():
                    if not isinstance(table_name, str) or not isinstance(update, dict):
                        continue
                    inserts = update.get("inserts", [])
                    if not isinstance(inserts, list):
                        raise MigrationError(f"{label} source capture has an invalid table update")
                    rows = tables.setdefault(table_name, [])
                    for row in inserts:
                        if not isinstance(row, dict):
                            raise MigrationError(f"{label} source capture has an invalid row")
                        rows.append(row)
    except OSError as exc:
        raise MigrationError(f"{label} source capture is unreadable") from exc
    return tables


def required(row: dict[str, Any], field: str, label: str) -> Any:
    if field not in row:
        raise MigrationError(f"{label} row is missing a required field")
    return row[field]


def timestamp_micros(value: Any, label: str) -> int:
    if isinstance(value, bool):
        raise MigrationError(f"{label} timestamp is invalid")
    if isinstance(value, int):
        if -(2**63) <= value <= (2**63 - 1):
            return value
        raise MigrationError(f"{label} timestamp is outside the signed 64-bit bound")
    if isinstance(value, dict):
        for key in TIMESTAMP_KEYS:
            if key in value:
                return timestamp_micros(value[key], label)
        raise MigrationError(f"{label} timestamp is invalid")
    if isinstance(value, str):
        clean = value.strip()
        if INTEGER.fullmatch(clean):
            return timestamp_micros(int(clean), label)
        iso_value = clean[:-1] + "+00:00" if clean.endswith(("Z", "z")) else clean
        try:
            parsed = dt.datetime.fromisoformat(iso_value)
        except ValueError as exc:
            raise MigrationError(f"{label} timestamp is invalid") from exc
        if parsed.tzinfo is None:
            raise MigrationError(f"{label} timestamp has no timezone")
        delta = parsed.astimezone(dt.timezone.utc) - EPOCH
        return timestamp_micros(
            ((delta.days * 86_400 + delta.seconds) * 1_000_000) + delta.microseconds,
            label,
        )
    raise MigrationError(f"{label} timestamp is invalid")


def uint_value(value: Any, label: str, maximum: int) -> int:
    if isinstance(value, bool):
        raise MigrationError(f"{label} is not a non-negative integer")
    if isinstance(value, int):
        number = value
    elif isinstance(value, str) and re.fullmatch(r"[0-9]+", value.strip()):
        number = int(value.strip())
    else:
        raise MigrationError(f"{label} is not a non-negative integer")
    if number < 0 or number > maximum:
        raise MigrationError(f"{label} is outside the import bound")
    return number


def token(value: Any, label: str, maximum: int, *, optional: bool = False) -> str:
    if not isinstance(value, str):
        raise MigrationError(f"{label} is not text")
    clean = value.strip()
    if optional and (not clean or clean == "-"):
        return "none"
    if contains_sensitive_token_text(clean):
        redacted = f"redacted-{hashlib.sha256(clean.encode('utf-8')).hexdigest()[:20]}"
        if len(redacted) <= maximum:
            return redacted
        raise MigrationError(f"{label} cannot fit a privacy-safe replacement")
    if not clean or len(clean.encode("utf-8")) > maximum or not SAFE_TOKEN.fullmatch(clean):
        raise MigrationError(f"{label} is outside the safe token contract")
    return clean


def legacy_identifier(value: Any, label: str, maximum: int) -> str:
    """Keep a safe legacy identifier or replace its private shape deterministically.

    Older Ticket rows used free-form correlation values. Those values are not
    needed to interpret the event, so an unsupported shape must not block a
    retained row from moving into the privacy-bounded store.
    """
    if not isinstance(value, str):
        raise MigrationError(f"{label} is not text")
    clean = value.strip()
    if not clean or clean == "-":
        return "none"
    try:
        return token(clean, label, maximum)
    except MigrationError:
        replacement = f"redacted-{hashlib.sha256(clean.encode('utf-8')).hexdigest()[:20]}"
        if len(replacement) <= maximum:
            return replacement
        raise MigrationError(f"{label} cannot fit a privacy-safe replacement")


def contains_sensitive_token_text(value: str) -> bool:
    lower = value.lower()
    return (
        "://" in lower
        or any(marker in lower for marker in PRIVATE_TOKEN_PATH_MARKERS)
        or looks_like_email(value)
        or contains_ipv4(value)
        or looks_like_long_opaque_token(value)
    )


def looks_like_email(value: str) -> bool:
    for part in value.split():
        clean = part.strip("!\"#$%&'()*,/:;<=>?[\\]^`{|}~")
        if "@" not in clean:
            continue
        local, domain = clean.split("@", 1)
        if local and "." in domain and not domain.startswith(".") and not domain.endswith("."):
            return True
    return False


def looks_like_long_opaque_token(value: str) -> bool:
    if len(value) < 48 or looks_like_structured_operational_id(value):
        return False
    segments = value.split(".")
    if len(segments) == 3 and all(
        len(segment) >= 8 and all(is_base64_token_character(character) for character in segment)
        for segment in segments
    ):
        return True
    if not all(is_base64_token_character(character) for character in value):
        return False
    if len(value) >= 64:
        return True
    return any(character.islower() for character in value) and any(
        character.isupper() for character in value
    ) and any(character.isdigit() for character in value)


def looks_like_structured_operational_id(value: str) -> bool:
    return (
        ":run:" in value
        or ":phase:" in value
        or value.startswith("sample:")
        or ":sample:" in value
        or re.search(r"[0-9]{8}[Tt][0-9]{6}[Zz]", value) is not None
        or re.search(
            r"[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}"
            r"(?:[Zz]|\.[0-9]+[Zz])",
            value,
        )
        is not None
        or sum(
            1
            for part in re.split(r"[-_]", value)
            if len(part) >= 2 and part.isascii() and part.isalnum()
        )
        >= 5
    )


def is_base64_token_character(character: str) -> bool:
    return character.isascii() and (character.isalnum() or character in "+/=_-")


def fixed_token(value: Any, label: str, allowed: set[str]) -> str:
    clean = token(value, label, max(len(item) for item in allowed))
    if clean not in allowed:
        raise MigrationError(f"{label} is unsupported")
    return clean


def log_level(value: Any, label: str) -> str:
    if not isinstance(value, str):
        raise MigrationError(f"{label} is not text")
    clean = value.strip().lower()
    clean = {"warning": "warn", "fatal": "critical"}.get(clean, clean)
    if clean not in LOG_LEVELS:
        raise MigrationError(f"{label} is unsupported")
    return clean


def safe_text(value: Any, maximum: int = 240) -> str:
    if not isinstance(value, str):
        return ""
    clean = " ".join(value.split())
    clean = "".join(character for character in clean if character.isprintable())[:maximum]
    if not clean:
        return ""
    lower = clean.lower()
    if (
        SUSPICIOUS_TEXT.search(clean)
        or any(marker in lower for marker in PRIVATE_FILESYSTEM_PATH_MARKERS)
        or contains_ipv4(clean)
        or contains_sensitive_character_run(clean)
    ):
        return "[redacted]"
    return clean


def contains_sensitive_character_run(value: str) -> bool:
    digit_run = 0
    opaque_run = 0
    for character in value:
        digit_run = digit_run + 1 if character.isascii() and character.isdigit() else 0
        opaque_run = (
            opaque_run + 1
            if character.isascii() and (character.isalnum() or character in "+/=_-")
            else 0
        )
        if digit_run >= 5 or opaque_run >= 64:
            return True
    return False


def contains_ipv4(value: str) -> bool:
    for candidate in re.split(r"[^0-9.]", value):
        parts = candidate.split(".")
        if len(parts) == 4 and all(
            part and part.isascii() and part.isdigit() and int(part) <= 255 for part in parts
        ):
            return True
    return False


def sensitive_key(key: str) -> bool:
    normalized = "".join(character for character in key if character.isascii() and character.isalnum()).lower()
    return any(part in normalized for part in SENSITIVE_KEY_PARTS)


def sanitize_json(value: Any, *, depth: int = 0, field_counter: list[int] | None = None) -> Any:
    if field_counter is None:
        field_counter = [0]
    if depth > 3:
        return None
    if value is None or isinstance(value, bool):
        return value
    if isinstance(value, int):
        return value if abs(value) <= 9_007_199_254_740_991 else None
    if isinstance(value, float):
        return value if math.isfinite(value) and abs(value) <= 9_007_199_254_740_991 else None
    if isinstance(value, str):
        return safe_text(value)
    if isinstance(value, list):
        return [sanitize_json(item, depth=depth + 1, field_counter=field_counter) for item in value[:16]]
    if isinstance(value, dict):
        result: dict[str, Any] = {}
        for key in sorted(value):
            if not isinstance(key, str) or not SAFE_DETAIL_KEY.fullmatch(key) or sensitive_key(key):
                continue
            if field_counter[0] >= 32:
                break
            field_counter[0] += 1
            result[key] = sanitize_json(value[key], depth=depth + 1, field_counter=field_counter)
        return result
    return None


def encoded_size(value: dict[str, Any]) -> int:
    return len(json.dumps(value, ensure_ascii=False, separators=(",", ":"), sort_keys=True).encode("utf-8"))


def bounded_detail_object(value: Any) -> str:
    sanitized = sanitize_json(value)
    if not isinstance(sanitized, dict):
        sanitized = {}
    bounded: dict[str, Any] = {}
    omitted = False
    for key in sorted(sanitized):
        candidate = dict(bounded)
        candidate[key] = sanitized[key]
        if encoded_size(candidate) <= MAX_DETAIL_BYTES:
            bounded = candidate
        else:
            omitted = True
    if omitted:
        candidate = dict(bounded)
        candidate["detailsTruncated"] = True
        if encoded_size(candidate) <= MAX_DETAIL_BYTES:
            bounded = candidate
    return json.dumps(bounded, ensure_ascii=False, separators=(",", ":"), sort_keys=True)


def parsed_detail(raw: Any) -> dict[str, Any]:
    if not isinstance(raw, str) or not raw.strip():
        return {}
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        return {}
    return value if isinstance(value, dict) else {}


def chatgpt_event_detail(public_text: Any, safe_details: Any) -> str:
    result: dict[str, Any] = {}
    clean_public = safe_text(public_text)
    if clean_public:
        result["publicText"] = clean_public

    sanitized_details = sanitize_json(parsed_detail(safe_details))
    omitted = False
    if isinstance(sanitized_details, dict) and sanitized_details:
        nested: dict[str, Any] = {}
        for key in sorted(sanitized_details):
            candidate_nested = dict(nested)
            candidate_nested[key] = sanitized_details[key]
            candidate = dict(result)
            candidate["safeDetails"] = candidate_nested
            if encoded_size(candidate) <= MAX_DETAIL_BYTES:
                nested = candidate_nested
            else:
                omitted = True
        if nested:
            result["safeDetails"] = nested
    if omitted:
        candidate = dict(result)
        candidate["detailsTruncated"] = True
        if encoded_size(candidate) <= MAX_DETAIL_BYTES:
            result = candidate
    return json.dumps(result, ensure_ascii=False, separators=(",", ":"), sort_keys=True)


def retained_times(
    row: dict[str, Any], occurred_field: str, expiry_field: str, label: str, now_micros: int
) -> tuple[int, int] | None:
    occurred = timestamp_micros(required(row, occurred_field, label), f"{label} occurrence")
    expires = timestamp_micros(required(row, expiry_field, label), f"{label} expiry")
    if expires <= now_micros:
        return None
    if expires < occurred:
        raise MigrationError(f"{label} expiry precedes occurrence")
    return occurred, expires


def event_input(
    *,
    event_id: str,
    domain: str,
    record_type: str,
    source: str,
    operation: str,
    event: str,
    level: str,
    status: str = "none",
    result: str = "none",
    scope_id: str = "none",
    correlation_id: str = "none",
    parent_id: str = "none",
    component: str = "none",
    detail_json: str = "{}",
    duration_millis: int = 0,
    total_duration_millis: int = 0,
    count: int = 0,
    byte_count: int = 0,
    occurred_at_micros: int,
    expires_at_micros: int,
    archive: bool = False,
) -> dict[str, Any]:
    try:
        parsed_detail_json = json.loads(detail_json)
    except json.JSONDecodeError as exc:
        raise MigrationError(f"{domain} detail JSON is invalid") from exc
    if not isinstance(parsed_detail_json, dict) or len(detail_json.encode("utf-8")) > MAX_DETAIL_BYTES:
        raise MigrationError(f"{domain} detail JSON is outside the import bound")
    return {
        "id": token(event_id, f"{domain} event id", 220),
        "domain": domain,
        "recordType": record_type,
        "source": token(source, f"{domain} source", 64),
        "operation": token(operation, f"{domain} operation", 120),
        "event": token(event, f"{domain} event", 120),
        "level": log_level(level, f"{domain} level"),
        "status": token(status, f"{domain} status", 48, optional=True),
        "result": token(result, f"{domain} result", 48, optional=True),
        "scopeId": token(scope_id, f"{domain} scope", 180, optional=True),
        "correlationId": token(correlation_id, f"{domain} correlation", 180, optional=True),
        "parentId": token(parent_id, f"{domain} parent", 180, optional=True),
        "component": token(component, f"{domain} component", 160, optional=True),
        "detailJson": detail_json,
        "durationMillis": uint_value(duration_millis, f"{domain} duration", MAX_DURATION_MILLIS),
        "totalDurationMillis": uint_value(
            total_duration_millis, f"{domain} total duration", MAX_DURATION_MILLIS
        ),
        "count": uint_value(count, f"{domain} count", MAX_COUNT),
        "byteCount": uint_value(byte_count, f"{domain} byte count", MAX_BYTE_COUNT),
        "occurredAtMicros": occurred_at_micros,
        "expiresAtMicros": expires_at_micros,
        "archive": archive,
    }


def deployment_level(status: str) -> str:
    if status == "failed":
        return "error"
    if status in {"cancelled", "skipped"}:
        return "warn"
    return "info"


def map_deployment(
    tables: dict[str, list[dict[str, Any]]], now_micros: int, stats: dict[str, int]
) -> Iterable[dict[str, Any]]:
    for row in tables.get("deploymenttiming_run", []):
        times = retained_times(row, "occurredAt", "expiresAt", "deployment run", now_micros)
        if times is None:
            stats["excluded_expired"] += 1
            continue
        occurred, expires = times
        lifecycle = fixed_token(required(row, "lifecycle", "deployment run"), "deployment lifecycle", {"started", "finished"})
        status = fixed_token(
            required(row, "status", "deployment run"),
            "deployment run status",
            {"running", "ok", "failed", "cancelled"},
        )
        profile = token(required(row, "profile", "deployment run"), "deployment profile", 48, optional=True)
        yield event_input(
            event_id=required(row, "id", "deployment run"),
            domain="deployment",
            record_type="run",
            source=required(row, "source", "deployment run"),
            operation=required(row, "action", "deployment run"),
            event=lifecycle,
            level=deployment_level(status),
            status=status,
            scope_id=required(row, "releaseId", "deployment run"),
            correlation_id=required(row, "runId", "deployment run"),
            component=required(row, "target", "deployment run"),
            detail_json=bounded_detail_object({"profile": profile}),
            total_duration_millis=required(row, "totalDurationMillis", "deployment run"),
            occurred_at_micros=occurred,
            expires_at_micros=expires,
        )
        stats["deployment_runs"] += 1

    for row in tables.get("deploymenttiming_phase", []):
        times = retained_times(row, "occurredAt", "expiresAt", "deployment phase", now_micros)
        if times is None:
            stats["excluded_expired"] += 1
            continue
        occurred, expires = times
        status = fixed_token(
            required(row, "status", "deployment phase"),
            "deployment phase status",
            {"ok", "failed", "skipped"},
        )
        profile = token(required(row, "profile", "deployment phase"), "deployment profile", 48, optional=True)
        yield event_input(
            event_id=required(row, "id", "deployment phase"),
            domain="deployment",
            record_type="phase",
            source=required(row, "source", "deployment phase"),
            operation=required(row, "action", "deployment phase"),
            event=required(row, "phase", "deployment phase"),
            level=deployment_level(status),
            status=status,
            scope_id=required(row, "releaseId", "deployment phase"),
            correlation_id=required(row, "runId", "deployment phase"),
            component=required(row, "target", "deployment phase"),
            detail_json=bounded_detail_object({"profile": profile}),
            duration_millis=required(row, "durationMillis", "deployment phase"),
            total_duration_millis=required(row, "totalDurationMillis", "deployment phase"),
            occurred_at_micros=occurred,
            expires_at_micros=expires,
        )
        stats["deployment_phases"] += 1


def pixel_level(status: str, result: str, priority: str) -> str:
    if status in {"failed", "unavailable"} or result in {"failed", "rejected"}:
        return "error"
    if (
        status in {"degraded", "stale"}
        or result in {"cancelled", "dropped", "retrying"}
        or priority in {"high", "critical"}
    ):
        return "warn"
    return "info"


def map_pixel(
    tables: dict[str, list[dict[str, Any]]], now_micros: int, stats: dict[str, int]
) -> Iterable[dict[str, Any]]:
    for row in tables.get("pixelorchestrator_event", []):
        times = retained_times(row, "occurredAt", "expiresAt", "Pixel event", now_micros)
        if times is None:
            stats["excluded_expired"] += 1
            continue
        occurred, expires = times
        correlation = token(required(row, "correlationId", "Pixel event"), "Pixel correlation", 24)
        if not re.fullmatch(r"[0-9a-f]{24}", correlation):
            raise MigrationError("Pixel correlation is outside the legacy contract")
        event_type = fixed_token(required(row, "eventType", "Pixel event"), "Pixel event type", PIXEL_EVENT_TYPES)
        component = fixed_token(required(row, "component", "Pixel event"), "Pixel component", PIXEL_COMPONENTS)
        cleanup = fixed_token(
            required(row, "cleanupCategory", "Pixel event"), "Pixel cleanup category", PIXEL_CLEANUP_CATEGORIES
        )
        if (event_type == "cleanup_result") != (cleanup != "none"):
            raise MigrationError("Pixel cleanup category does not match its event type")
        status = fixed_token(required(row, "status", "Pixel event"), "Pixel status", PIXEL_STATUSES)
        result = fixed_token(required(row, "result", "Pixel event"), "Pixel result", PIXEL_RESULTS)
        priority = fixed_token(required(row, "priority", "Pixel event"), "Pixel priority", PIXEL_PRIORITIES)
        build_id = token(required(row, "buildId", "Pixel event"), "Pixel build id", 96)
        yield event_input(
            event_id=correlation,
            domain="pixel",
            record_type="event",
            source="pixel",
            operation=event_type,
            event=cleanup,
            level=pixel_level(status, result, priority),
            status=status,
            result=result,
            scope_id=build_id,
            correlation_id=correlation,
            component=component,
            detail_json=bounded_detail_object({"priority": priority}),
            duration_millis=required(row, "durationMillis", "Pixel event"),
            count=required(row, "count", "Pixel event"),
            byte_count=required(row, "byteCount", "Pixel event"),
            occurred_at_micros=occurred,
            expires_at_micros=expires,
        )
        stats["pixel_events"] += 1


def map_ticket(
    tables: dict[str, list[dict[str, Any]]], now_micros: int, stats: dict[str, int]
) -> Iterable[dict[str, Any]]:
    for row in tables.get("ticketremote_safe_operational_log", []):
        times = retained_times(row, "createdAt", "expiresAt", "Ticket event", now_micros)
        if times is None:
            stats["excluded_expired"] += 1
            continue
        occurred, expires = times
        source = token(required(row, "source", "Ticket event"), "Ticket source", 64)
        event = token(required(row, "event", "Ticket event"), "Ticket event", 120)
        yield event_input(
            event_id=required(row, "id", "Ticket event"),
            domain="ticket",
            record_type="event",
            source=source,
            operation=event,
            event=event,
            level=required(row, "level", "Ticket event"),
            scope_id=required(row, "ticketId", "Ticket event"),
            correlation_id=legacy_identifier(
                required(row, "correlationId", "Ticket event"),
                "Ticket correlation",
                180,
            ),
            component=source,
            detail_json=bounded_detail_object(parsed_detail(required(row, "detailJson", "Ticket event"))),
            occurred_at_micros=occurred,
            expires_at_micros=expires,
        )
        stats["ticket_events"] += 1


def chatgpt_level(kind_or_status: str, detail: dict[str, Any] | None = None, failure_code: str = "") -> str:
    if detail is not None:
        raw_level = detail.get("level")
        if isinstance(raw_level, str):
            candidate = {"warning": "warn", "fatal": "critical"}.get(raw_level.strip().lower(), raw_level.strip().lower())
            if candidate in LOG_LEVELS:
                return candidate
    lowered = f"{kind_or_status} {failure_code}".lower()
    if any(marker in lowered for marker in ("error", "fail")):
        return "error"
    if any(marker in lowered for marker in ("cancel", "retry", "wait")):
        return "warn"
    return "info"


def archive_timestamp_pair(row: dict[str, Any], created_field: str, label: str, stats: dict[str, int]) -> tuple[int, int] | None:
    expiry = timestamp_micros(required(row, "retentionDeleteAfter", label), f"{label} expiry")
    if expiry != 0:
        stats["excluded_chatgpt_nonzero"] += 1
        return None
    created = timestamp_micros(required(row, created_field, label), f"{label} occurrence")
    return created, 0


def map_chatgpt(
    tables: dict[str, list[dict[str, Any]]], stats: dict[str, int]
) -> Iterable[dict[str, Any]]:
    for row in tables.get("chatgptbroker_event", []):
        times = archive_timestamp_pair(row, "createdAt", "ChatGPT event", stats)
        if times is None:
            continue
        occurred, expires = times
        kind = token(required(row, "kind", "ChatGPT event"), "ChatGPT event kind", 120)
        details = parsed_detail(required(row, "safeDetailsJson", "ChatGPT event"))
        yield event_input(
            event_id=required(row, "id", "ChatGPT event"),
            domain="chatgpt",
            record_type="event",
            source="chatgpt-broker",
            operation=kind,
            event=kind,
            level=chatgpt_level(kind, details),
            scope_id=required(row, "jobId", "ChatGPT event"),
            correlation_id=required(row, "attemptId", "ChatGPT event"),
            component=required(row, "visibility", "ChatGPT event"),
            detail_json=chatgpt_event_detail(
                required(row, "publicText", "ChatGPT event"),
                required(row, "safeDetailsJson", "ChatGPT event"),
            ),
            occurred_at_micros=occurred,
            expires_at_micros=expires,
            archive=True,
        )
        stats["chatgpt_events"] += 1

    for row in tables.get("chatgptbroker_attempt", []):
        times = archive_timestamp_pair(row, "createdAt", "ChatGPT attempt", stats)
        if times is None:
            continue
        occurred, expires = times
        attempt_id = token(required(row, "id", "ChatGPT attempt"), "ChatGPT attempt id", 220)
        status = token(required(row, "status", "ChatGPT attempt"), "ChatGPT attempt status", 48)
        worker_id = token(required(row, "workerId", "ChatGPT attempt"), "ChatGPT worker id", 120, optional=True)
        backend_id = token(required(row, "backendId", "ChatGPT attempt"), "ChatGPT backend id", 80, optional=True)
        failure_code = token(
            required(row, "failureCode", "ChatGPT attempt"), "ChatGPT failure code", 80, optional=True
        )
        updated = timestamp_micros(required(row, "updatedAt", "ChatGPT attempt"), "ChatGPT attempt update")
        result = "none"
        lowered = status.lower()
        if "success" in lowered or lowered in {"succeeded", "done", "completed"}:
            result = "ok"
        elif "fail" in lowered:
            result = "failed"
        elif "cancel" in lowered:
            result = "cancelled"
        detail = {
            "backendId": backend_id,
            "failureCode": failure_code,
            "updatedAtMicros": updated,
            "workerId": worker_id,
        }
        yield event_input(
            event_id=attempt_id,
            domain="chatgpt",
            record_type="attempt",
            source="chatgpt-broker",
            operation="attempt",
            event=status,
            level=chatgpt_level(status, failure_code=failure_code),
            status=status,
            result=result,
            scope_id=required(row, "jobId", "ChatGPT attempt"),
            correlation_id=attempt_id,
            component=backend_id,
            detail_json=bounded_detail_object(detail),
            occurred_at_micros=occurred,
            expires_at_micros=expires,
            archive=True,
        )
        stats["chatgpt_attempts"] += 1


def legacy_weight(event: dict[str, Any]) -> int:
    string_fields = (
        "id",
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
    )
    return 96 + sum(len(str(event[field]).encode("utf-8")) for field in string_fields)


def write_batches(events: list[dict[str, Any]], output_dir: Path) -> int:
    output_dir.mkdir(parents=True, exist_ok=True, mode=0o700)
    batches: list[list[dict[str, Any]]] = []
    current: list[dict[str, Any]] = []
    current_weight = 0
    for event in events:
        weight = legacy_weight(event)
        if weight > MAX_BATCH_BYTES:
            raise MigrationError("one mapped event exceeds the import payload bound")
        if current and (len(current) >= MAX_BATCH_EVENTS or current_weight + weight > MAX_BATCH_BYTES):
            batches.append(current)
            current = []
            current_weight = 0
        current.append(event)
        current_weight += weight
    if current:
        batches.append(current)

    for index, batch in enumerate(batches, start=1):
        path = output_dir / f"batch-{index:06d}.json"
        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
        descriptor = os.open(path, flags, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(batch, handle, ensure_ascii=False, separators=(",", ":"), sort_keys=True)
            handle.write("\n")
    return len(batches)


def main() -> int:
    args = parse_args()
    stats = {
        "deployment_runs": 0,
        "deployment_phases": 0,
        "pixel_events": 0,
        "ticket_events": 0,
        "chatgpt_events": 0,
        "chatgpt_attempts": 0,
        "excluded_expired": 0,
        "excluded_chatgpt_nonzero": 0,
    }
    events: list[dict[str, Any]] = []
    if args.deployment:
        events.extend(map_deployment(load_subscription(args.deployment, "deployment"), args.now_micros, stats))
    if args.pixel:
        events.extend(map_pixel(load_subscription(args.pixel, "Pixel"), args.now_micros, stats))
    if args.ticket:
        events.extend(map_ticket(load_subscription(args.ticket, "Ticket"), args.now_micros, stats))
    if args.chatgpt:
        events.extend(map_chatgpt(load_subscription(args.chatgpt, "ChatGPT"), stats))

    events.sort(key=lambda item: (item["domain"], item["occurredAtMicros"], item["recordType"], item["id"]))
    seen: set[str] = set()
    for event in events:
        raw_id = event["id"]
        prefixed_id = raw_id if raw_id.startswith(f"{event['domain']}:") else f"{event['domain']}:{raw_id}"
        if prefixed_id in seen:
            raise MigrationError("mapped history contains a duplicate target event id")
        seen.add(prefixed_id)
    batch_count = write_batches(events, Path(args.output_dir))

    for key in (
        "deployment_runs",
        "deployment_phases",
        "pixel_events",
        "ticket_events",
        "chatgpt_events",
        "chatgpt_attempts",
        "excluded_expired",
        "excluded_chatgpt_nonzero",
    ):
        print(f"{key}={stats[key]}")
    print(f"total_events={len(events)}")
    print(f"batches={batch_count}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except MigrationError as error:
        print(f"operational history mapper: {error}", file=sys.stderr)
        raise SystemExit(2)
    except Exception:
        print("operational history mapper: unexpected mapping failure", file=sys.stderr)
        raise SystemExit(3)
