#!/usr/bin/env python3
"""Keep Netdata's native dashboard responsive without replacing its UI."""

from __future__ import annotations

import argparse
import json
import os
import re
import stat
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable


ENTRYPOINTS = (
    "index.html",
    "v3/agent.html",
    "v3/index.html",
    "v3/local-agent.html",
)
MARKER_START = "<!-- arbuzas-native-mobile:start -->"
MARKER_END = "<!-- arbuzas-native-mobile:end -->"
VIEWPORT_META = (
    '<meta name="viewport" '
    'content="width=device-width, initial-scale=1, viewport-fit=cover">'
)
VIEWPORT_CONTENT = "width=device-width, initial-scale=1, viewport-fit=cover"
DEFAULT_MANIFEST = "kitty-gration/build.json"
NATIVE_SCRIPT_RE = re.compile(r"native-mobile\.[A-Z0-9]{8}\.js")
NATIVE_STYLESHEET_RE = re.compile(r"native-mobile\.[A-Z0-9]{8}\.css")
VIEWPORT_RE = re.compile(
    r"<meta\b[^>]*\bname\s*=\s*(['\"])viewport\1[^>]*>",
    re.IGNORECASE,
)
DEVICE_WIDTH_RE = re.compile(
    r"\bcontent\s*=\s*(['\"])[^'\"]*\bwidth\s*=\s*device-width\b[^'\"]*\1",
    re.IGNORECASE,
)


class DashboardPatchError(RuntimeError):
    """Raised when an entrypoint cannot be changed safely."""


@dataclass(frozen=True)
class NativeMobileAssets:
    script: str
    stylesheet: str
    viewport: str

    @property
    def script_url(self) -> str:
        return f"/kitty-gration/{self.script}"

    @property
    def stylesheet_url(self) -> str:
        return f"/kitty-gration/{self.stylesheet}"


@dataclass(frozen=True)
class PlannedChange:
    path: Path
    original: str
    updated: str
    status: str


def _read_regular_file(path: Path) -> tuple[str, os.stat_result]:
    if path.is_symlink():
        raise DashboardPatchError(f"refusing symlinked Netdata entrypoint: {path}")
    try:
        details = path.stat()
    except FileNotFoundError as exc:
        raise DashboardPatchError(f"missing Netdata entrypoint: {path}") from exc
    if not stat.S_ISREG(details.st_mode):
        raise DashboardPatchError(f"Netdata entrypoint is not a regular file: {path}")
    try:
        return path.read_text(encoding="utf-8"), details
    except UnicodeDecodeError as exc:
        raise DashboardPatchError(f"Netdata entrypoint is not UTF-8: {path}") from exc


def _managed_span(document: str, path: Path) -> tuple[int, int] | None:
    starts = document.count(MARKER_START)
    ends = document.count(MARKER_END)
    if starts == 0 and ends == 0:
        return None
    if starts != 1 or ends != 1:
        raise DashboardPatchError(f"malformed managed viewport markers in {path}")
    start = document.index(MARKER_START)
    end_start = document.find(MARKER_END, start)
    if end_start < 0:
        raise DashboardPatchError(f"reversed managed viewport markers in {path}")
    end = end_start + len(MARKER_END)
    return start, end


def _load_assets(web_root: Path, manifest_path: Path | None) -> NativeMobileAssets:
    path = manifest_path or web_root / DEFAULT_MANIFEST
    if path.is_symlink() or not path.is_file():
        raise DashboardPatchError(f"missing or unsafe native mobile manifest: {path}")
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise DashboardPatchError(f"invalid native mobile manifest: {path}") from exc
    native = manifest.get("nativeMobile")
    if not isinstance(native, dict):
        raise DashboardPatchError(f"manifest has no nativeMobile contract: {path}")
    script = native.get("script")
    stylesheet = native.get("stylesheet")
    viewport = native.get("viewport")
    if not isinstance(script, str) or not NATIVE_SCRIPT_RE.fullmatch(script):
        raise DashboardPatchError(f"manifest has an unsafe native mobile script: {script!r}")
    if not isinstance(stylesheet, str) or not NATIVE_STYLESHEET_RE.fullmatch(stylesheet):
        raise DashboardPatchError(f"manifest has an unsafe native mobile stylesheet: {stylesheet!r}")
    if viewport != VIEWPORT_CONTENT:
        raise DashboardPatchError(f"manifest has an unexpected native mobile viewport: {viewport!r}")
    listed_assets = manifest.get("assets")
    if not isinstance(listed_assets, list) or script not in listed_assets or stylesheet not in listed_assets:
        raise DashboardPatchError("manifest native mobile files are absent from its asset list")
    for asset in (script, stylesheet):
        asset_path = path.parent / asset
        if asset_path.is_symlink() or not asset_path.is_file() or asset_path.stat().st_size == 0:
            raise DashboardPatchError(f"missing or unsafe native mobile asset: {asset_path}")
    return NativeMobileAssets(script, stylesheet, viewport)


def _managed_block(assets: NativeMobileAssets, include_viewport: bool) -> str:
    viewport = VIEWPORT_META if include_viewport else ""
    stylesheet = (
        f'<link rel="stylesheet" href="{assets.stylesheet_url}" '
        'data-kitty-netdata-mobile="stylesheet">'
    )
    script = (
        f'<script defer src="{assets.script_url}" '
        'data-kitty-netdata-mobile="script"></script>'
    )
    return f"{MARKER_START}{viewport}{stylesheet}{script}{MARKER_END}"


def _has_responsive_upstream_viewport(document: str) -> bool:
    matches = VIEWPORT_RE.findall(document)
    if not matches:
        return False
    full_matches = list(VIEWPORT_RE.finditer(document))
    return len(full_matches) == 1 and bool(DEVICE_WIDTH_RE.search(full_matches[0].group(0)))


def _viewport_state(document: str, path: Path) -> str:
    viewport_matches = list(VIEWPORT_RE.finditer(document))
    if not viewport_matches:
        return "missing"
    if len(viewport_matches) == 1 and _has_responsive_upstream_viewport(document):
        return "responsive"
    raise DashboardPatchError(f"unsupported existing viewport declaration in {path}")


def _plan_apply(path: Path, assets: NativeMobileAssets) -> PlannedChange:
    document, _ = _read_regular_file(path)
    span = _managed_span(document, path)
    if span is not None:
        start, end = span
        outside = document[:start] + document[end:]
        viewport_state = _viewport_state(outside, path)
        updated = document[:start] + _managed_block(assets, viewport_state == "missing") + document[end:]
        if len(list(VIEWPORT_RE.finditer(updated))) != 1:
            raise DashboardPatchError(f"managed entrypoint has duplicate viewport declarations: {path}")
        status = "unchanged" if updated == document else "refreshed"
        return PlannedChange(path, document, updated, status)

    viewport_state = _viewport_state(document, path)
    head_matches = list(re.finditer(r"<head(?:\s[^>]*)?>", document, re.IGNORECASE))
    if len(head_matches) != 1:
        raise DashboardPatchError(f"expected exactly one HTML head in {path}")
    insertion = head_matches[0].end()
    updated = document[:insertion] + _managed_block(assets, viewport_state == "missing") + document[insertion:]
    return PlannedChange(path, document, updated, "injected")


def _plan_remove(path: Path) -> PlannedChange:
    document, _ = _read_regular_file(path)
    span = _managed_span(document, path)
    if span is None:
        return PlannedChange(path, document, document, "unchanged")
    start, end = span
    return PlannedChange(path, document, document[:start] + document[end:], "removed")


def _plan_check(path: Path, assets: NativeMobileAssets) -> PlannedChange:
    document, _ = _read_regular_file(path)
    span = _managed_span(document, path)
    if span is not None:
        start, end = span
        outside = document[:start] + document[end:]
        viewport_state = _viewport_state(outside, path)
        expected_block = _managed_block(assets, viewport_state == "missing")
        if document[start:end] != expected_block:
            raise DashboardPatchError(f"stale managed viewport block in {path}")
        if len(list(VIEWPORT_RE.finditer(document))) != 1:
            raise DashboardPatchError(f"managed entrypoint has duplicate viewport declarations: {path}")
        if document.count('data-kitty-netdata-mobile="stylesheet"') != 1:
            raise DashboardPatchError(f"managed entrypoint has no unique native mobile stylesheet: {path}")
        if document.count('data-kitty-netdata-mobile="script"') != 1:
            raise DashboardPatchError(f"managed entrypoint has no unique native mobile script: {path}")
        return PlannedChange(path, document, document, "managed-responsive")
    raise DashboardPatchError(f"Netdata entrypoint has no managed native mobile assets: {path}")


def _atomic_write(change: PlannedChange) -> None:
    if change.updated == change.original:
        return
    _, details = _read_regular_file(change.path)
    temporary_path: Path | None = None
    try:
        descriptor, temporary_name = tempfile.mkstemp(
            dir=change.path.parent,
            prefix=f".{change.path.name}.arbuzas.",
        )
        temporary_path = Path(temporary_name)
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="") as handle:
            handle.write(change.updated)
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary_path, stat.S_IMODE(details.st_mode))
        os.chown(temporary_path, details.st_uid, details.st_gid)
        os.replace(temporary_path, change.path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            try:
                temporary_path.unlink()
            except FileNotFoundError:
                pass


def run(
    action: str,
    web_root: Path,
    best_effort: bool = False,
    manifest_path: Path | None = None,
) -> list[PlannedChange]:
    assets = None
    if action in {"apply", "check"}:
        try:
            assets = _load_assets(web_root, manifest_path)
        except DashboardPatchError as exc:
            if not best_effort:
                raise
            print(f"warning: {exc}", file=sys.stderr)
            return []
    changes: list[PlannedChange] = []
    errors: list[str] = []
    for relative_path in ENTRYPOINTS:
        path = web_root / relative_path
        try:
            if action == "apply":
                assert assets is not None
                changes.append(_plan_apply(path, assets))
            elif action == "check":
                assert assets is not None
                changes.append(_plan_check(path, assets))
            else:
                changes.append(_plan_remove(path))
        except DashboardPatchError as exc:
            errors.append(str(exc))

    if errors and not best_effort:
        raise DashboardPatchError("; ".join(errors))

    if action != "check":
        for change in changes:
            _atomic_write(change)

    for change in changes:
        print(f"{change.status}: {change.path}")
    for error in errors:
        print(f"warning: {error}", file=sys.stderr)
    return changes


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Apply or validate kitty-gration's native Netdata mobile viewport.",
    )
    parser.add_argument("action", choices=("apply", "remove", "check"))
    parser.add_argument(
        "--web-root",
        type=Path,
        default=Path("/usr/share/netdata/web"),
        help="Netdata static web root",
    )
    parser.add_argument(
        "--manifest",
        type=Path,
        help="built Kitty-gration dashboard manifest (defaults below the web root)",
    )
    parser.add_argument(
        "--best-effort",
        action="store_true",
        help="warn and continue when a future Netdata package changes an entrypoint",
    )
    return parser


def main(argv: Iterable[str] | None = None) -> int:
    arguments = _parser().parse_args(argv)
    try:
        run(
            arguments.action,
            arguments.web_root,
            arguments.best_effort,
            arguments.manifest,
        )
    except DashboardPatchError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
