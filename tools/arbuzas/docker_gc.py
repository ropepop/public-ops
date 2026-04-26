#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path
from typing import Any

DEFAULT_GRACE_SECONDS = 7 * 24 * 60 * 60
DEFAULT_BUILD_CACHE_UNTIL = "168h"


def warn(message: str) -> None:
    print(f"docker_gc: {message}", file=sys.stderr)


def run_command(args: list[str]) -> str:
    result = subprocess.run(args, capture_output=True, text=True)
    if result.returncode != 0:
        detail = result.stderr.strip() or result.stdout.strip() or f"exit code {result.returncode}"
        raise RuntimeError(f"{' '.join(args)} failed: {detail}")
    return result.stdout


def read_release_id(release_env_path: Path) -> str:
    if not release_env_path.is_file():
        return ""
    for raw_line in release_env_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        if key.strip() == "ARBUZAS_RELEASE_ID":
            return value.strip()
    return ""


def safe_resolve(path: Path) -> Path | None:
    try:
        return path.resolve(strict=True)
    except OSError:
        return None


def resolve_current_release_id(current_link: Path, current_target: Path | None) -> str:
    release_id = read_release_id(current_link / "release.env")
    if release_id:
        return release_id
    if current_target is not None:
        return current_target.name
    return ""


def newest_non_current_release(releases_root: Path, current_target: Path | None, current_release_id: str) -> Path | None:
    if not releases_root.is_dir():
        return None

    candidates = []
    for child in releases_root.iterdir():
        if not child.is_dir():
            continue
        resolved = safe_resolve(child) or child
        if current_target is not None and resolved == current_target:
            continue
        if current_release_id and child.name == current_release_id:
            continue
        candidates.append(child)

    candidates.sort(key=lambda path: path.stat().st_mtime, reverse=True)
    return candidates[0] if candidates else None


def parse_output_lines(output: str) -> list[str]:
    return [line.strip() for line in output.splitlines() if line.strip()]


def list_all_image_ids() -> set[str]:
    output = run_command(["docker", "image", "ls", "--all", "--no-trunc", "--format", "{{.ID}}"])
    return {line for line in parse_output_lines(output) if line not in {"<none>", "IMAGE ID"}}


def list_used_image_ids() -> set[str]:
    container_output = run_command(["docker", "ps", "-aq", "--no-trunc"])
    container_ids = parse_output_lines(container_output)
    if not container_ids:
        return set()
    inspect_output = run_command(["docker", "inspect", "--format", "{{.Image}}", *container_ids])
    return {line for line in parse_output_lines(inspect_output) if line not in {"<none>", "IMAGE ID"}}


def resolve_protected_release_image_ids(protected_release_ids: set[str]) -> set[str]:
    if not protected_release_ids:
        return set()

    output = run_command(
        [
            "docker",
            "image",
            "ls",
            "--all",
            "--no-trunc",
            "--format",
            "{{.Repository}}|{{.Tag}}|{{.ID}}",
        ]
    )
    protected_image_ids: set[str] = set()
    for line in parse_output_lines(output):
        repository, separator, rest = line.partition("|")
        if not separator:
            continue
        tag, separator, image_id = rest.partition("|")
        if not separator:
            continue
        if not repository.startswith("arbuzas/"):
            continue
        if tag in protected_release_ids and image_id.strip():
            protected_image_ids.add(image_id.strip())
    return protected_image_ids


def load_state(state_path: Path) -> dict[str, int]:
    if not state_path.exists():
        return {}

    try:
        payload = json.loads(state_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        warn(f"state reset because {state_path} is unreadable: {exc}")
        return {}

    images = payload.get("images")
    if not isinstance(images, dict):
        warn(f"state reset because {state_path} does not contain an images object")
        return {}

    state: dict[str, int] = {}
    for image_id, image_state in images.items():
        if not isinstance(image_id, str):
            continue
        if not isinstance(image_state, dict):
            continue
        first_seen = image_state.get("first_seen_unused_at")
        if isinstance(first_seen, (int, float)) and first_seen >= 0:
            state[image_id] = int(first_seen)
    return state


def write_state(state_path: Path, state: dict[str, int], now: int) -> None:
    state_path.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "version": 1,
        "updated_at": now,
        "images": {
            image_id: {"first_seen_unused_at": first_seen}
            for image_id, first_seen in sorted(state.items())
        },
    }
    state_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def delete_image(image_id: str) -> None:
    run_command(["docker", "image", "rm", "--force", image_id])


def prune_builder_cache(until_filter: str) -> None:
    run_command(["docker", "builder", "prune", "-af", "--filter", f"until={until_filter}"])


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Apply the Arbuzas Docker image retention policy.")
    parser.add_argument("--current-link", default="/etc/arbuzas/current")
    parser.add_argument("--releases-root", default="/etc/arbuzas/releases")
    parser.add_argument("--state-file", default="/etc/arbuzas/docker-gc/state.json")
    parser.add_argument("--grace-seconds", type=int, default=DEFAULT_GRACE_SECONDS)
    parser.add_argument("--build-cache-until", default=DEFAULT_BUILD_CACHE_UNTIL)
    parser.add_argument("--now", type=int)
    return parser


def main() -> int:
    args = build_parser().parse_args()

    now = args.now if args.now is not None else int(time.time())
    current_link = Path(args.current_link)
    releases_root = Path(args.releases_root)
    state_path = Path(args.state_file)
    grace_seconds = max(0, int(args.grace_seconds))

    current_target = safe_resolve(current_link)
    current_release_id = resolve_current_release_id(current_link, current_target)
    rollback_release_path = newest_non_current_release(releases_root, current_target, current_release_id)
    rollback_release_id = rollback_release_path.name if rollback_release_path is not None else ""

    protected_release_ids = {release_id for release_id in {current_release_id, rollback_release_id} if release_id}
    all_image_ids = list_all_image_ids()
    used_image_ids = list_used_image_ids()
    protected_image_ids = resolve_protected_release_image_ids(protected_release_ids)
    eligible_image_ids = sorted(all_image_ids - used_image_ids - protected_image_ids)

    existing_state = load_state(state_path)
    next_state: dict[str, int] = {}
    deleted_image_ids: list[str] = []
    newly_tracked_image_ids: list[str] = []
    errors: list[str] = []

    for image_id in eligible_image_ids:
        first_seen_unused_at = existing_state.get(image_id, now)
        age_seconds = max(0, now - first_seen_unused_at)
        if image_id not in existing_state:
            newly_tracked_image_ids.append(image_id)
        if image_id in existing_state and age_seconds >= grace_seconds:
            try:
                delete_image(image_id)
            except RuntimeError as exc:
                errors.append(str(exc))
                next_state[image_id] = first_seen_unused_at
            else:
                deleted_image_ids.append(image_id)
            continue
        next_state[image_id] = first_seen_unused_at

    try:
        write_state(state_path, next_state, now)
    except OSError as exc:
        errors.append(f"failed to write state file {state_path}: {exc}")

    try:
        prune_builder_cache(args.build_cache_until)
    except RuntimeError as exc:
        errors.append(str(exc))

    print(
        "docker_gc: "
        f"current_release={current_release_id or '-'} "
        f"rollback_release={rollback_release_id or '-'} "
        f"used_images={len(used_image_ids)} "
        f"protected_images={len(protected_image_ids)} "
        f"eligible_images={len(eligible_image_ids)} "
        f"tracked_images={len(next_state)} "
        f"deleted_images={len(deleted_image_ids)}"
    )
    if newly_tracked_image_ids:
        print(f"docker_gc: newly_tracked={','.join(sorted(newly_tracked_image_ids))}")
    if deleted_image_ids:
        print(f"docker_gc: deleted={','.join(sorted(deleted_image_ids))}")
    print(f"docker_gc: build_cache_pruned=until={args.build_cache_until}")

    for error in errors:
        warn(error)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
