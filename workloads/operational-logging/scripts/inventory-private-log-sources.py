#!/usr/bin/env python3
"""Inventory associated Spacetime schemas without reading table rows."""

from __future__ import annotations

import argparse
import json
import re
import shutil
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Any


DATABASE_NAME = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_-]{0,119}$")
IDENTITY = re.compile(r"^[0-9a-f]{64}$")
TABLE_NAME = re.compile(r"^[A-Za-z0-9_]{1,160}$")
LIST_ROW = re.compile(r"^\s*(.*?)\s*\|\s*([0-9a-fA-F]{64})\s*$")
CANDIDATE_TOKEN = re.compile(
    r"(?:^|_)(?:log(?:ging|s)?|trace(?:s)?|audit(?:s)?|event(?:s)?|"
    r"attempt(?:s)?|histor(?:y|ies)|batch(?:es)?|phase(?:s)?|run(?:s)?|"
    r"telemetr(?:y|ies)|observabilit(?:y|ies)|diagnostic(?:s)?|breadcrumb(?:s)?|"
    r"metric(?:s)?|activit(?:y|ies))(?:_|$)",
    re.IGNORECASE,
)
ALLOWED_KINDS = {"canonical_log", "legacy_log_source", "application_state"}


class InventoryError(Exception):
    """An intentionally bounded inventory failure."""


@dataclass(frozen=True)
class Database:
    name: str
    identity: str


@dataclass(frozen=True)
class Candidate:
    database: str
    table: str
    access: str
    kind: str
    note: str


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(
        description="Schema-only inventory of associated private log-like tables."
    )
    parser.add_argument("--server", default="https://maincloud.spacetimedb.com")
    parser.add_argument("--spacetime", default="spacetime")
    parser.add_argument("--spacetime-root", default="")
    parser.add_argument(
        "--classification-file",
        default=str(script_dir / "private-log-source-classification.json"),
    )
    parser.add_argument("--timeout-seconds", type=int, default=60)
    parser.add_argument(
        "--allow-incomplete",
        action="store_true",
        help="Return success when databases are paused or otherwise uninspectable.",
    )
    return parser.parse_args()


def validate_args(args: argparse.Namespace) -> None:
    if not args.server or len(args.server) > 240 or re.search(r"\s", args.server):
        raise InventoryError("server must be a bounded nickname or URL")
    if not 5 <= args.timeout_seconds <= 300:
        raise InventoryError("timeout must be between 5 and 300 seconds")
    if args.spacetime_root and len(args.spacetime_root) > 1024:
        raise InventoryError("Spacetime root path is too long")
    if "/" in args.spacetime:
        if not Path(args.spacetime).is_file():
            raise InventoryError("Spacetime CLI is unavailable")
        executable = args.spacetime
    else:
        executable = shutil.which(args.spacetime)
        if not executable:
            raise InventoryError("Spacetime CLI is unavailable")
    args.spacetime = executable


def base_command(args: argparse.Namespace) -> list[str]:
    command = [args.spacetime]
    if args.spacetime_root:
        command.extend(["--root-dir", args.spacetime_root])
    return command


def run_cli(args: argparse.Namespace, command: list[str]) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            base_command(args) + command,
            check=False,
            capture_output=True,
            text=True,
            timeout=args.timeout_seconds,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise InventoryError("Spacetime CLI command did not complete") from exc


def parse_list(output: str) -> list[Database]:
    if not re.search(r"Associated databases for user [0-9a-fA-F]{64}", output):
        raise InventoryError("authenticated database list output was not recognized")
    databases: list[Database] = []
    seen: set[str] = set()
    for line in output.splitlines():
        match = LIST_ROW.match(line)
        if not match:
            continue
        raw_names, raw_identity = match.groups()
        identity = raw_identity.lower()
        if not IDENTITY.fullmatch(identity):
            raise InventoryError("database list contained an invalid identity")
        aliases = [part.strip() for part in raw_names.split(",") if part.strip()]
        names = [name for name in aliases if DATABASE_NAME.fullmatch(name)]
        if not names:
            raise InventoryError("an associated database has no safe usable name")
        if identity in seen:
            raise InventoryError("database list contained a duplicate identity")
        seen.add(identity)
        databases.append(Database(name=names[0], identity=identity))
    return sorted(databases, key=lambda item: (item.name, item.identity))


def load_classifications(path: str) -> dict[tuple[str, str], dict[str, str]]:
    try:
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise InventoryError("classification file is unreadable") from exc
    if not isinstance(payload, dict) or payload.get("schemaVersion") != 1:
        raise InventoryError("classification file has an unsupported schema")
    entries = payload.get("classifications")
    if not isinstance(entries, list):
        raise InventoryError("classification file has no classifications")
    result: dict[tuple[str, str], dict[str, str]] = {}
    for entry in entries:
        if not isinstance(entry, dict):
            raise InventoryError("classification entry is invalid")
        database = entry.get("database")
        table = entry.get("table")
        kind = entry.get("kind")
        note = entry.get("note", "")
        expected_access = entry.get("expectedAccess", "")
        if not isinstance(database, str) or not DATABASE_NAME.fullmatch(database):
            raise InventoryError("classification database name is invalid")
        if not isinstance(table, str) or not TABLE_NAME.fullmatch(table):
            raise InventoryError("classification table name is invalid")
        if kind not in ALLOWED_KINDS:
            raise InventoryError("classification kind is invalid")
        if not isinstance(note, str) or not note or len(note) > 200 or "\n" in note:
            raise InventoryError("classification note is invalid")
        if expected_access not in {"", "private", "public"}:
            raise InventoryError("classification expected access is invalid")
        key = (database, table)
        if key in result:
            raise InventoryError("classification contains a duplicate table")
        result[key] = {
            "kind": kind,
            "note": note,
            "expected_access": expected_access,
        }
    return result


def table_access(table: dict[str, Any]) -> str:
    access = table.get("table_access")
    if isinstance(access, dict):
        if "Private" in access:
            return "private"
        if "Public" in access:
            return "public"
    return "unknown"


def parse_schema(raw: str) -> list[tuple[str, str]]:
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise InventoryError("database schema output was not valid JSON") from exc
    if not isinstance(payload, dict) or not isinstance(payload.get("tables"), list):
        raise InventoryError("database schema output had an unsupported shape")
    tables: list[tuple[str, str]] = []
    for table in payload["tables"]:
        if not isinstance(table, dict):
            raise InventoryError("database schema contained an invalid table")
        name = table.get("name")
        if not isinstance(name, str) or not TABLE_NAME.fullmatch(name):
            raise InventoryError("database schema contained an unsafe table name")
        tables.append((name, table_access(table)))
    return tables


def describe_databases(
    args: argparse.Namespace,
    databases: list[Database],
    classifications: dict[tuple[str, str], dict[str, str]],
) -> tuple[list[Candidate], list[Candidate], list[str], list[str], int]:
    candidates: list[Candidate] = []
    unclassified: list[Candidate] = []
    paused: list[str] = []
    uninspectable: list[str] = []
    described = 0
    for database in databases:
        result = run_cli(
            args,
            ["describe", "--server", args.server, "--json", database.identity],
        )
        if result.returncode != 0:
            if re.search(r"database is paused", result.stderr, re.IGNORECASE):
                paused.append(database.name)
            else:
                uninspectable.append(database.name)
            continue
        tables = parse_schema(result.stdout)
        described += 1
        for table, access in tables:
            if not CANDIDATE_TOKEN.search(table):
                continue
            known = classifications.get((database.name, table))
            if known is None:
                unclassified.append(
                    Candidate(database.name, table, access, "unclassified", "review required")
                )
                continue
            candidate = Candidate(
                database.name,
                table,
                access,
                known["kind"],
                known["note"],
            )
            candidates.append(candidate)
            if known["expected_access"] and known["expected_access"] != access:
                unclassified.append(
                    Candidate(
                        database.name,
                        table,
                        access,
                        "access_mismatch",
                        f"expected {known['expected_access']} access",
                    )
                )
    return candidates, unclassified, paused, uninspectable, described


def emit(
    databases: list[Database],
    candidates: list[Candidate],
    unclassified: list[Candidate],
    paused: list[str],
    uninspectable: list[str],
    described: int,
    canonical_contract_ok: bool,
) -> None:
    incomplete = bool(paused or uninspectable)
    attention = bool(unclassified or incomplete or not canonical_contract_ok)
    candidate_keys = {
        (item.database, item.table) for item in [*candidates, *unclassified]
    }
    canonical = [item for item in candidates if item.kind == "canonical_log"]
    private_canonical = [item for item in canonical if item.access == "private"]
    print(f"status={'attention' if attention else 'ok'}")
    print(f"associated_databases={len(databases)}")
    print(f"described_databases={described}")
    print(f"paused_databases={len(paused)}")
    print(f"uninspectable_databases={len(uninspectable)}")
    print(f"candidate_surfaces={len(candidate_keys)}")
    print(f"canonical_log_tables={len(canonical)}")
    print(f"private_canonical_log_tables={len(private_canonical)}")
    print(
        "canonical_log_data_contract="
        f"{'ok' if canonical_contract_ok else 'violation'}"
    )
    print(f"legacy_log_sources={sum(item.kind == 'legacy_log_source' for item in candidates)}")
    print(f"application_state_candidates={sum(item.kind == 'application_state' for item in candidates)}")
    print(f"unclassified_candidates={len(unclassified)}")
    for item in sorted(candidates, key=lambda value: (value.kind, value.database, value.table)):
        print(
            f"classification={item.kind} database={item.database} "
            f"table={item.table} access={item.access} note={item.note}"
        )
    for item in sorted(unclassified, key=lambda value: (value.database, value.table, value.kind)):
        print(
            f"attention={item.kind} database={item.database} "
            f"table={item.table} access={item.access} note={item.note}"
        )
    if not canonical_contract_ok:
        print(
            "attention=canonical_log_data_contract "
            f"observed={len(canonical)} private={len(private_canonical)} "
            "required=exactly_one_private"
        )
    for name in sorted(paused):
        print(f"attention=paused_database database={name}")
    for name in sorted(uninspectable):
        print(f"attention=uninspectable_database database={name}")


def main() -> int:
    args = parse_args()
    validate_args(args)
    classifications = load_classifications(args.classification_file)
    listing = run_cli(args, ["list", "--server", args.server, "--yes"])
    if listing.returncode != 0:
        raise InventoryError("authenticated private database list failed")
    databases = parse_list(listing.stdout)
    candidates, unclassified, paused, uninspectable, described = describe_databases(
        args, databases, classifications
    )
    canonical = [item for item in candidates if item.kind == "canonical_log"]
    canonical_contract_ok = len(canonical) == 1 and canonical[0].access == "private"
    emit(
        databases,
        candidates,
        unclassified,
        paused,
        uninspectable,
        described,
        canonical_contract_ok,
    )
    if unclassified or not canonical_contract_ok:
        return 2
    if (paused or uninspectable) and not args.allow_incomplete:
        return 3
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except InventoryError as error:
        print(f"private log-source inventory: {error}", file=sys.stderr)
        raise SystemExit(4)
