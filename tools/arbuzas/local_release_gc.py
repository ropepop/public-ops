#!/usr/bin/env python3
"""Prune expired local Arbuzas release bundles within one managed root."""

from __future__ import annotations

import argparse
import os
import re
import shutil
import sys
import time
from dataclasses import dataclass
from pathlib import Path


DEFAULT_MAX_AGE_HOURS = 72
DEFAULT_KEEP_PER_FAMILY = 10


@dataclass(frozen=True)
class Release:
    path: Path
    family: str
    mtime: float


def release_family(release_name: str) -> str:
    if re.match(r"^\d{8}T\d{6}Z(?:-.+)?$", release_name):
        return "timestamped"

    prefixed_compact_date = re.match(r"^(.+?)-\d{8}(?:T\d{4,6}Z?)?(?:-.+)?$", release_name)
    if prefixed_compact_date:
        return prefixed_compact_date.group(1).strip("-") or release_name

    prefixed_hyphen_date = re.match(r"^(.+?)-\d{4}-\d{2}-\d{2}(?:-.+)?$", release_name)
    if prefixed_hyphen_date:
        return prefixed_hyphen_date.group(1).strip("-") or release_name

    return release_name


def validate_release_id(release_id: str) -> str:
    candidate = release_id.strip()
    if not candidate or candidate in {".", ".."} or Path(candidate).name != candidate:
        raise argparse.ArgumentTypeError("protected release ids must be direct child names")
    return candidate


def directory_size(path: Path) -> int:
    total = 0
    for root, directories, files in os.walk(path, followlinks=False):
        root_path = Path(root)
        for name in files:
            candidate = root_path / name
            try:
                total += candidate.lstat().st_size
            except FileNotFoundError:
                continue
        for name in directories:
            candidate = root_path / name
            try:
                if candidate.is_symlink():
                    total += candidate.lstat().st_size
            except FileNotFoundError:
                continue
    return total


def list_releases(releases_root: Path) -> list[Release]:
    releases: list[Release] = []
    for child in releases_root.iterdir():
        try:
            if child.is_symlink() or not child.is_dir():
                continue
            stat = child.stat()
        except FileNotFoundError:
            continue
        releases.append(Release(child, release_family(child.name), stat.st_mtime))
    return releases


def select_removals(
    releases: list[Release],
    protected_release_ids: set[str],
    now: float,
    max_age_hours: int,
    keep_per_family: int,
) -> list[tuple[Release, tuple[str, ...]]]:
    by_family: dict[str, list[Release]] = {}
    for release in releases:
        by_family.setdefault(release.family, []).append(release)

    max_age_seconds = max_age_hours * 60 * 60
    removals: list[tuple[Release, tuple[str, ...]]] = []
    for family_releases in by_family.values():
        family_releases.sort(key=lambda release: (release.mtime, release.path.name), reverse=True)
        for position, release in enumerate(family_releases):
            if release.path.name in protected_release_ids:
                continue
            age_seconds = max(0.0, now - release.mtime)
            reasons: list[str] = []
            if age_seconds > max_age_seconds:
                reasons.append("expired")
            if position >= keep_per_family:
                reasons.append("over-family-limit")
            if reasons:
                removals.append((release, tuple(reasons)))

    removals.sort(key=lambda item: (item[0].mtime, item[0].path.name))
    return removals


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Prune direct child release directories under a managed local release root."
    )
    parser.add_argument("--releases-root", required=True, type=Path)
    parser.add_argument(
        "--protect-release-id",
        action="append",
        default=[],
        type=validate_release_id,
        help="Direct child release id that must never be removed; may be repeated.",
    )
    parser.add_argument("--max-age-hours", type=int, default=DEFAULT_MAX_AGE_HOURS)
    parser.add_argument("--keep-per-family", type=int, default=DEFAULT_KEEP_PER_FAMILY)
    parser.add_argument("--now", type=float, default=None, help=argparse.SUPPRESS)
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--verbose", action="store_true")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    if args.max_age_hours < 0:
        raise SystemExit("--max-age-hours must be a non-negative integer")
    if args.keep_per_family < 0:
        raise SystemExit("--keep-per-family must be a non-negative integer")

    releases_root = args.releases_root.expanduser()
    if not releases_root.exists():
        print(
            "local_release_gc: "
            f"root={releases_root} dry_run={str(args.dry_run).lower()} "
            "scanned=0 protected=0 selected=0 selected_bytes=0 "
            "removed=0 reclaimed_bytes=0 result=missing-root"
        )
        return 0
    if releases_root.is_symlink() or not releases_root.is_dir():
        raise SystemExit(f"refusing non-directory or symlink release root: {releases_root}")

    protected_release_ids = set(args.protect_release_id)
    releases = list_releases(releases_root)
    now = args.now if args.now is not None else time.time()
    removals = select_removals(
        releases,
        protected_release_ids,
        now,
        args.max_age_hours,
        args.keep_per_family,
    )

    removed = 0
    selected_bytes = 0
    reclaimed_bytes = 0
    for release, reasons in removals:
        size = directory_size(release.path)
        if args.verbose:
            action = "would-remove" if args.dry_run else "remove"
            age_hours = max(0, int((now - release.mtime) // 3600))
            print(
                "local_release_gc: "
                f"action={action} release={release.path.name} family={release.family} "
                f"age_hours={age_hours} bytes={size} reasons={','.join(reasons)}"
            )
        selected_bytes += size
        if args.dry_run:
            continue
        try:
            shutil.rmtree(release.path)
        except OSError as exc:
            print(f"local_release_gc: failed to remove {release.path}: {exc}", file=sys.stderr)
            return 1
        removed += 1
        reclaimed_bytes += size

    protected_present = sum(1 for release in releases if release.path.name in protected_release_ids)
    print(
        "local_release_gc: "
        f"root={releases_root} dry_run={str(args.dry_run).lower()} "
        f"scanned={len(releases)} protected={protected_present} "
        f"selected={len(removals)} selected_bytes={selected_bytes} "
        f"removed={removed} reclaimed_bytes={reclaimed_bytes} "
        f"max_age_hours={args.max_age_hours} keep_per_family={args.keep_per_family}"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
