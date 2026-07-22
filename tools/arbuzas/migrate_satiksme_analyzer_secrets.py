#!/usr/bin/env python3
"""Move Satiksme chat-analyzer credentials out of the service env file."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import stat
import sys
import tempfile


DIRECT_KEYS = {
    "SATIKSME_CHAT_ANALYZER_API_ID": "telegram-api-id.secret",
    "SATIKSME_CHAT_ANALYZER_API_HASH": "telegram-api-hash.secret",
    "SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY": "google-api-key.secret",
    "SATIKSME_CHAT_ANALYZER_MODEL_API_KEY": "model-api-key.secret",
}

FILE_KEYS = {
    "SATIKSME_CHAT_ANALYZER_API_ID": "SATIKSME_CHAT_ANALYZER_API_ID_FILE",
    "SATIKSME_CHAT_ANALYZER_API_HASH": "SATIKSME_CHAT_ANALYZER_API_HASH_FILE",
    "SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY": "SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY_FILE",
    "SATIKSME_CHAT_ANALYZER_MODEL_API_KEY": "SATIKSME_CHAT_ANALYZER_MODEL_API_KEY_FILE",
}


def parse_assignments(lines: list[str]) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in lines:
        stripped = raw.strip()
        if not stripped or stripped.startswith("#") or "=" not in raw:
            continue
        key, value = raw.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def atomic_write(path: Path, value: str, *, mode: int, existing_owner: Path | None = None) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.parent.chmod(0o700)
    fd, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent, text=True)
    temporary = Path(temporary_name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(value)
            handle.flush()
            os.fsync(handle.fileno())
        temporary.chmod(mode)
        if existing_owner is not None and existing_owner.exists():
            details = existing_owner.stat()
            try:
                os.chown(temporary, details.st_uid, details.st_gid)
            except PermissionError:
                if (details.st_uid, details.st_gid) != (os.getuid(), os.getgid()):
                    raise
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def resolved_values(assignments: dict[str, str]) -> dict[str, str]:
    values = {key: assignments.get(key, "").strip() for key in DIRECT_KEYS}
    if not values["SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY"]:
        values["SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY"] = values[
            "SATIKSME_CHAT_ANALYZER_MODEL_API_KEY"
        ]
    if not values["SATIKSME_CHAT_ANALYZER_MODEL_API_KEY"]:
        values["SATIKSME_CHAT_ANALYZER_MODEL_API_KEY"] = values[
            "SATIKSME_CHAT_ANALYZER_GOOGLE_API_KEY"
        ]
    missing = [key for key, value in values.items() if not value]
    if missing:
        raise SystemExit("missing analyzer credential values: " + ", ".join(sorted(missing)))
    if not values["SATIKSME_CHAT_ANALYZER_API_ID"].isdigit():
        raise SystemExit("SATIKSME_CHAT_ANALYZER_API_ID must be numeric")
    if len(values["SATIKSME_CHAT_ANALYZER_API_HASH"]) < 16:
        raise SystemExit("SATIKSME_CHAT_ANALYZER_API_HASH is unexpectedly short")
    return values


def validate(
    env_file: Path,
    secrets_dir: Path,
    runtime_secret_root: str,
) -> None:
    if stat.S_IMODE(env_file.stat().st_mode) != 0o600:
        raise SystemExit(f"service env file must have mode 0600: {env_file}")
    assignments = parse_assignments(env_file.read_text(encoding="utf-8").splitlines())
    still_inline = [key for key in DIRECT_KEYS if assignments.get(key, "").strip()]
    if still_inline:
        raise SystemExit("analyzer credentials remain inline: " + ", ".join(sorted(still_inline)))
    for direct_key, filename in DIRECT_KEYS.items():
        file_key = FILE_KEYS[direct_key]
        expected = f"{runtime_secret_root.rstrip('/')}/{filename}"
        if assignments.get(file_key) != expected:
            raise SystemExit(f"{file_key} does not name the managed secret file")
        path = secrets_dir / filename
        if not path.is_file() or path.is_symlink() or not path.read_text(encoding="utf-8").strip():
            raise SystemExit(f"missing or invalid analyzer secret file: {path}")
        if stat.S_IMODE(path.stat().st_mode) != 0o600:
            raise SystemExit(f"analyzer secret file must have mode 0600: {path}")


def migrate(env_file: Path, secrets_dir: Path, runtime_secret_root: str) -> None:
    if not env_file.is_file() or env_file.is_symlink():
        raise SystemExit(f"refusing invalid service env file: {env_file}")
    original_lines = env_file.read_text(encoding="utf-8").splitlines()
    assignments = parse_assignments(original_lines)
    if not any(assignments.get(key, "").strip() for key in DIRECT_KEYS):
        env_file.chmod(0o600)
        validate(env_file, secrets_dir, runtime_secret_root)
        return
    values = resolved_values(assignments)

    for direct_key, filename in DIRECT_KEYS.items():
        target = secrets_dir / filename
        if target.exists():
            if not target.is_file() or target.is_symlink():
                raise SystemExit(f"refusing invalid analyzer secret target: {target}")
            existing = target.read_text(encoding="utf-8").strip()
            if existing and existing != values[direct_key]:
                raise SystemExit(f"existing analyzer secret differs from env value: {target}")
        atomic_write(target, values[direct_key] + "\n", mode=0o600, existing_owner=target)

    managed_keys = set(DIRECT_KEYS) | set(FILE_KEYS.values())
    retained = []
    for raw in original_lines:
        key = raw.split("=", 1)[0].strip() if "=" in raw else ""
        if key not in managed_keys:
            retained.append(raw)
    while retained and not retained[-1].strip():
        retained.pop()
    retained.extend(
        [
            "",
            "# Analyzer credentials are stored in root-only host secret files.",
            *[
                f"{FILE_KEYS[key]}={runtime_secret_root.rstrip('/')}/{filename}"
                for key, filename in DIRECT_KEYS.items()
            ],
        ]
    )
    atomic_write(env_file, "\n".join(retained) + "\n", mode=0o600, existing_owner=env_file)
    validate(env_file, secrets_dir, runtime_secret_root)


def set_google_key_from_stdin(secrets_dir: Path) -> None:
    value = sys.stdin.read().strip()
    if not value or any(character.isspace() for character in value):
        raise SystemExit("Google API key from stdin is empty or contains whitespace")
    for filename in ("google-api-key.secret", "model-api-key.secret"):
        target = secrets_dir / filename
        if target.exists() and (not target.is_file() or target.is_symlink()):
            raise SystemExit(f"refusing invalid analyzer secret target: {target}")
        atomic_write(target, value + "\n", mode=0o600, existing_owner=target)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--env-file", default="")
    parser.add_argument("--secrets-dir", required=True)
    parser.add_argument(
        "--runtime-secret-root",
        default="/etc/arbuzas/secrets/satiksme-chat-analyzer",
    )
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--set-google-key-stdin", action="store_true")
    args = parser.parse_args()

    secrets_dir = Path(args.secrets_dir).resolve()
    if args.set_google_key_stdin:
        if args.check:
            parser.error("--set-google-key-stdin and --check cannot be combined")
        set_google_key_from_stdin(secrets_dir)
        print("Satiksme analyzer Google credentials replaced in private files")
        return 0
    if not args.env_file:
        parser.error("--env-file is required unless --set-google-key-stdin is used")

    env_file = Path(args.env_file).resolve()
    if args.check:
        validate(env_file, secrets_dir, args.runtime_secret_root)
        print("Satiksme analyzer secret migration is valid")
    else:
        migrate(env_file, secrets_dir, args.runtime_secret_root)
        print("Satiksme analyzer credentials migrated to private files")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
