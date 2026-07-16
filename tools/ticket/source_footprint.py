#!/usr/bin/env python3
"""Measure the immutable cross-repository Ticket source-reduction contract."""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any, Iterable


SCRIPT_DIR = Path(__file__).resolve().parent
DEFAULT_MANIFEST = SCRIPT_DIR / "source_manifest.json"
DEFAULT_OPS_REPO = SCRIPT_DIR.parents[1]
DEFAULT_PIXEL_REPO = DEFAULT_OPS_REPO.parent / "pixel-phone"


class FootprintError(RuntimeError):
    pass


@dataclass(frozen=True)
class Metrics:
    files: int
    lines: int
    bytes: int

    def as_dict(self) -> dict[str, int]:
        return {"files": self.files, "lines": self.lines, "bytes": self.bytes}


def git(repo: Path, *args: str, binary: bool = False) -> str | bytes:
    try:
        return subprocess.check_output(
            ["git", "-C", str(repo), *args],
            stderr=subprocess.PIPE,
            text=not binary,
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        detail = ""
        if isinstance(exc, subprocess.CalledProcessError) and exc.stderr:
            detail = exc.stderr.decode(errors="replace") if binary else str(exc.stderr)
        raise FootprintError(f"git {' '.join(args)} failed in {repo}: {detail.strip()}") from exc


def load_manifest(path: Path) -> dict[str, Any]:
    try:
        manifest = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise FootprintError(f"cannot read manifest {path}: {exc}") from exc
    if manifest.get("schema_version") != 1:
        raise FootprintError(f"unsupported manifest schema in {path}")
    return manifest


def baseline_paths(repo: Path, commit: str) -> list[str]:
    output = git(repo, "ls-tree", "-r", "--name-only", commit)
    assert isinstance(output, str)
    return [line for line in output.splitlines() if line]


def current_paths(repo: Path) -> list[str]:
    output = git(repo, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
    assert isinstance(output, str)
    paths = []
    for relative in output.split("\0"):
        if relative and (repo / relative).is_file():
            paths.append(relative)
    return paths


def excluded(path: str, spec: dict[str, Any]) -> bool:
    if path in spec.get("exclude_files", []):
        return True
    if any(path.startswith(prefix) for prefix in spec.get("exclude_prefixes", [])):
        return True
    if any(path.endswith(suffix) for suffix in spec.get("exclude_suffixes", [])):
        return True
    parts = set(PurePosixPath(path).parts)
    return any(part in parts for part in spec.get("exclude_path_parts", []))


def rule_matches(path: str, rule: dict[str, Any]) -> bool:
    files = rule.get("files")
    if files is not None:
        return path in files
    prefix = rule.get("prefix", "")
    if prefix and not path.startswith(prefix):
        return False
    contains = rule.get("name_contains")
    if contains and contains.lower() not in PurePosixPath(path).name.lower():
        return False
    extensions = rule.get("extensions", [])
    return not extensions or any(path.endswith(extension) for extension in extensions)


def select_paths(paths: Iterable[str], spec: dict[str, Any]) -> dict[str, str]:
    selected: dict[str, str] = {}
    for path in sorted(set(paths)):
        if excluded(path, spec):
            continue
        for rule in spec["rules"]:
            if rule_matches(path, rule):
                selected[path] = rule["label"]
                break
    return selected


def blob_at(repo: Path, commit: str, path: str) -> bytes:
    output = git(repo, "show", f"{commit}:{path}", binary=True)
    assert isinstance(output, bytes)
    return output


def metrics_for_blobs(blobs: Iterable[bytes]) -> Metrics:
    file_count = line_count = byte_count = 0
    for data in blobs:
        file_count += 1
        line_count += len(data.splitlines())
        byte_count += len(data)
    return Metrics(file_count, line_count, byte_count)


def baseline_metrics(repo: Path, spec: dict[str, Any]) -> tuple[Metrics, dict[str, str]]:
    commit = spec["baseline_commit"]
    selected = select_paths(baseline_paths(repo, commit), spec)
    return metrics_for_blobs(blob_at(repo, commit, path) for path in selected), selected


def worktree_metrics(repo: Path, spec: dict[str, Any]) -> tuple[Metrics, dict[str, str]]:
    selected = select_paths(current_paths(repo), spec)
    return metrics_for_blobs((repo / path).read_bytes() for path in selected), selected


def protected_file_status(repo: Path, spec: dict[str, Any]) -> dict[str, Any]:
    commit = spec["baseline_commit"]
    protected = spec.get("protected_files", [])
    changed = []
    for path in protected:
        current_path = repo / path
        if not current_path.is_file() or current_path.read_bytes() != blob_at(repo, commit, path):
            changed.append(path)
    return {
        "total": len(protected),
        "unchanged": len(protected) - len(changed),
        "changed": changed,
    }


def percent_reduction(baseline: int, current: int) -> float:
    if baseline <= 0:
        return 0.0
    return (baseline - current) * 100.0 / baseline


def evaluate(
    manifest: dict[str, Any],
    measurements: dict[str, dict[str, Metrics]],
    protected: dict[str, dict[str, Any]],
) -> list[dict[str, Any]]:
    contract = manifest["contract"]
    ops = measurements["ops"]
    pixel = measurements["pixel"]
    baseline_lines = ops["baseline"].lines + pixel["baseline"].lines
    current_lines = ops["current"].lines + pixel["current"].lines
    baseline_bytes = ops["baseline"].bytes + pixel["baseline"].bytes
    current_bytes = ops["current"].bytes + pixel["current"].bytes
    minimum_repo_percent = float(contract["minimum_repo_line_reduction_percent"])
    minimum_bytes_percent = float(contract["minimum_combined_byte_reduction_percent"])
    checks = [
        {
            "name": "audited baseline",
            "passed": baseline_lines == int(contract["baseline_lines"]),
            "actual": baseline_lines,
            "required": int(contract["baseline_lines"]),
        },
        {
            "name": "ops line cap",
            "passed": ops["current"].lines <= int(contract["maximum_lines"]["ops"]),
            "actual": ops["current"].lines,
            "required": int(contract["maximum_lines"]["ops"]),
        },
        {
            "name": "pixel line cap",
            "passed": pixel["current"].lines <= int(contract["maximum_lines"]["pixel"]),
            "actual": pixel["current"].lines,
            "required": int(contract["maximum_lines"]["pixel"]),
        },
        {
            "name": "combined line cap",
            "passed": current_lines <= int(contract["maximum_lines"]["combined"]),
            "actual": current_lines,
            "required": int(contract["maximum_lines"]["combined"]),
        },
        {
            "name": "combined line reduction",
            "passed": baseline_lines - current_lines >= int(contract["minimum_line_reduction"]),
            "actual": baseline_lines - current_lines,
            "required": int(contract["minimum_line_reduction"]),
        },
        {
            "name": "ops line reduction percent",
            "passed": percent_reduction(ops["baseline"].lines, ops["current"].lines) >= minimum_repo_percent,
            "actual": percent_reduction(ops["baseline"].lines, ops["current"].lines),
            "required": minimum_repo_percent,
        },
        {
            "name": "pixel line reduction percent",
            "passed": percent_reduction(pixel["baseline"].lines, pixel["current"].lines) >= minimum_repo_percent,
            "actual": percent_reduction(pixel["baseline"].lines, pixel["current"].lines),
            "required": minimum_repo_percent,
        },
        {
            "name": "combined byte reduction percent",
            "passed": percent_reduction(baseline_bytes, current_bytes) >= minimum_bytes_percent,
            "actual": percent_reduction(baseline_bytes, current_bytes),
            "required": minimum_bytes_percent,
        },
    ]
    for name, status in protected.items():
        if status["total"]:
            checks.append(
                {
                    "name": f"{name} protected files unchanged",
                    "passed": status["unchanged"] == status["total"],
                    "actual": status["unchanged"],
                    "required": status["total"],
                }
            )
    return checks


def verify_expected_baselines(
    manifest: dict[str, Any], measurements: dict[str, dict[str, Metrics]]
) -> None:
    for name, spec in manifest["repositories"].items():
        expected = spec["expected_baseline"]
        actual = measurements[name]["baseline"].as_dict()
        if actual != expected:
            raise FootprintError(
                f"{name} baseline manifest drift: expected {expected}, measured {actual}"
            )


def result_payload(
    manifest_path: Path,
    repo_paths: dict[str, Path],
    manifest: dict[str, Any],
    measurements: dict[str, dict[str, Metrics]],
    selected: dict[str, dict[str, dict[str, str]]],
    protected: dict[str, dict[str, Any]],
    checks: list[dict[str, Any]],
    include_files: bool,
) -> dict[str, Any]:
    repositories: dict[str, Any] = {}
    for name in ("ops", "pixel"):
        baseline = measurements[name]["baseline"]
        current = measurements[name]["current"]
        entry: dict[str, Any] = {
            "path": str(repo_paths[name]),
            "baseline_commit": manifest["repositories"][name]["baseline_commit"],
            "baseline": baseline.as_dict(),
            "current": current.as_dict(),
            "line_reduction": baseline.lines - current.lines,
            "line_reduction_percent": percent_reduction(baseline.lines, current.lines),
            "byte_reduction": baseline.bytes - current.bytes,
            "byte_reduction_percent": percent_reduction(baseline.bytes, current.bytes),
        }
        if include_files:
            entry["baseline_files"] = selected[name]["baseline"]
            entry["current_files"] = selected[name]["current"]
        repositories[name] = entry
    baseline_lines = sum(measurements[name]["baseline"].lines for name in repositories)
    current_lines = sum(measurements[name]["current"].lines for name in repositories)
    baseline_bytes = sum(measurements[name]["baseline"].bytes for name in repositories)
    current_bytes = sum(measurements[name]["current"].bytes for name in repositories)
    return {
        "manifest": str(manifest_path),
        "repositories": repositories,
        "combined": {
            "baseline": {"lines": baseline_lines, "bytes": baseline_bytes},
            "current": {"lines": current_lines, "bytes": current_bytes},
            "line_reduction": baseline_lines - current_lines,
            "line_reduction_percent": percent_reduction(baseline_lines, current_lines),
            "byte_reduction": baseline_bytes - current_bytes,
            "byte_reduction_percent": percent_reduction(baseline_bytes, current_bytes),
        },
        "protected_files": protected,
        "checks": checks,
        "passed": all(check["passed"] for check in checks),
    }


def print_human(payload: dict[str, Any]) -> None:
    print("Ticket production source footprint")
    print(f"Manifest: {payload['manifest']}")
    print()
    print(f"{'Repository':<12} {'Baseline lines':>14} {'Current lines':>13} {'Line cut':>10} {'Baseline bytes':>15} {'Current bytes':>14} {'Byte cut':>9}")
    for name in ("ops", "pixel"):
        repo = payload["repositories"][name]
        print(
            f"{name:<12} {repo['baseline']['lines']:>14,} {repo['current']['lines']:>13,} "
            f"{repo['line_reduction_percent']:>9.2f}% {repo['baseline']['bytes']:>15,} "
            f"{repo['current']['bytes']:>14,} {repo['byte_reduction_percent']:>8.2f}%"
        )
    combined = payload["combined"]
    print(
        f"{'combined':<12} {combined['baseline']['lines']:>14,} {combined['current']['lines']:>13,} "
        f"{combined['line_reduction_percent']:>9.2f}% {combined['baseline']['bytes']:>15,} "
        f"{combined['current']['bytes']:>14,} {combined['byte_reduction_percent']:>8.2f}%"
    )
    print()
    print("Acceptance")
    for check in payload["checks"]:
        marker = "PASS" if check["passed"] else "FAIL"
        actual = check["actual"]
        required = check["required"]
        if isinstance(actual, float):
            actual = f"{actual:.2f}%"
            required = f"{float(required):.2f}%"
        print(f"[{marker}] {check['name']}: {actual} (threshold {required})")
    print()
    print("RESULT: " + ("PASS" if payload["passed"] else "NOT YET AT TARGET"))


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Measure Ticket first-party production source against immutable baselines."
    )
    parser.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    parser.add_argument("--ops-repo", type=Path, default=DEFAULT_OPS_REPO)
    parser.add_argument("--pixel-repo", type=Path, default=DEFAULT_PIXEL_REPO)
    parser.add_argument("--json", action="store_true", help="emit machine-readable JSON")
    parser.add_argument("--list-files", action="store_true", help="include selected file/category maps in JSON")
    parser.add_argument(
        "--check",
        action="store_true",
        help="exit non-zero until every reduction threshold passes",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    manifest_path = args.manifest.resolve()
    repo_paths = {"ops": args.ops_repo.resolve(), "pixel": args.pixel_repo.resolve()}
    try:
        manifest = load_manifest(manifest_path)
        measurements: dict[str, dict[str, Metrics]] = {}
        selected: dict[str, dict[str, dict[str, str]]] = {}
        protected: dict[str, dict[str, Any]] = {}
        for name, repo in repo_paths.items():
            if not (repo / ".git").exists():
                raise FootprintError(f"{name} repository not found at {repo}")
            spec = manifest["repositories"][name]
            baseline, baseline_selected = baseline_metrics(repo, spec)
            current, current_selected = worktree_metrics(repo, spec)
            measurements[name] = {"baseline": baseline, "current": current}
            selected[name] = {"baseline": baseline_selected, "current": current_selected}
            protected[name] = protected_file_status(repo, spec)
        verify_expected_baselines(manifest, measurements)
        checks = evaluate(manifest, measurements, protected)
        payload = result_payload(
            manifest_path,
            repo_paths,
            manifest,
            measurements,
            selected,
            protected,
            checks,
            args.list_files,
        )
    except FootprintError as exc:
        print(f"source-footprint error: {exc}", file=sys.stderr)
        return 2
    if args.json:
        print(json.dumps(payload, indent=2, sort_keys=True))
    else:
        print_human(payload)
    return 1 if args.check and not payload["passed"] else 0


if __name__ == "__main__":
    raise SystemExit(main())
