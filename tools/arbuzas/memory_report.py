#!/usr/bin/env python3
"""Report Linux memory pressure without counting reclaimable cache as pressure."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from pathlib import Path
from typing import Any


MEMINFO_VALUE_RE = re.compile(r"^\s*(\d+)(?:\s+kB)?\s*$")


def parse_meminfo(text: str) -> dict[str, int]:
    values: dict[str, int] = {}
    for line in text.splitlines():
        if ":" not in line:
            continue
        key, raw_value = line.split(":", 1)
        match = MEMINFO_VALUE_RE.match(raw_value)
        if match:
            values[key] = int(match.group(1))
    return values


def require_meminfo(values: dict[str, int], key: str) -> int:
    if key not in values:
        raise ValueError(f"/proc/meminfo is missing required key: {key}")
    return values[key]


def percent(value_kb: int, total_kb: int) -> float:
    if total_kb <= 0:
        raise ValueError("MemTotal must be greater than zero")
    return value_kb * 100.0 / total_kb


def mib(value_kb: int) -> float:
    return value_kb / 1024.0


def build_report(values: dict[str, int], source_label: str) -> dict[str, Any]:
    total_kb = require_meminfo(values, "MemTotal")
    free_kb = require_meminfo(values, "MemFree")
    buffers_kb = values.get("Buffers", 0)
    cached_kb = values.get("Cached", 0)
    sreclaimable_kb = values.get("SReclaimable", 0)
    slab_kb = values.get("Slab", 0)
    anon_kb = values.get("AnonPages", values.get("Active(anon)", 0) + values.get("Inactive(anon)", 0))

    if "MemAvailable" in values:
        available_kb = values["MemAvailable"]
        available_source = "MemAvailable"
    else:
        available_kb = free_kb + buffers_kb + cached_kb + sreclaimable_kb
        available_source = "MemFree + Buffers + Cached + SReclaimable fallback"

    swap_total_kb = values.get("SwapTotal", 0)
    swap_free_kb = values.get("SwapFree", 0)
    swap_used_kb = max(swap_total_kb - swap_free_kb, 0)

    real_pressure_kb = max(total_kb - available_kb, 0)
    provider_like_kb = max(total_kb - free_kb - buffers_kb, 0)
    literal_nonfree_kb = max(total_kb - free_kb, 0)
    reclaimable_cache_kb = buffers_kb + cached_kb + sreclaimable_kb
    cached_and_slab_reclaimable_kb = cached_kb + sreclaimable_kb
    used_excluding_reclaimable_kb = max(total_kb - free_kb - reclaimable_cache_kb, 0)

    return {
        "source": source_label,
        "formulas": {
            "real_pressure": "(MemTotal - MemAvailable) / MemTotal",
            "provider_like": "(MemTotal - MemFree - Buffers) / MemTotal",
            "literal_nonfree": "(MemTotal - MemFree) / MemTotal",
            "reclaimable_cache": "(Buffers + Cached + SReclaimable) / MemTotal",
        },
        "available_source": available_source,
        "total_kb": total_kb,
        "free_kb": free_kb,
        "available_kb": available_kb,
        "buffers_kb": buffers_kb,
        "cached_kb": cached_kb,
        "sreclaimable_kb": sreclaimable_kb,
        "slab_kb": slab_kb,
        "anon_kb": anon_kb,
        "real_pressure_kb": real_pressure_kb,
        "real_pressure_pct": percent(real_pressure_kb, total_kb),
        "provider_like_kb": provider_like_kb,
        "provider_like_pct": percent(provider_like_kb, total_kb),
        "literal_nonfree_kb": literal_nonfree_kb,
        "literal_nonfree_pct": percent(literal_nonfree_kb, total_kb),
        "reclaimable_cache_kb": reclaimable_cache_kb,
        "reclaimable_cache_pct": percent(reclaimable_cache_kb, total_kb),
        "cached_and_slab_reclaimable_kb": cached_and_slab_reclaimable_kb,
        "cached_and_slab_reclaimable_pct": percent(cached_and_slab_reclaimable_kb, total_kb),
        "used_excluding_reclaimable_kb": used_excluding_reclaimable_kb,
        "used_excluding_reclaimable_pct": percent(used_excluding_reclaimable_kb, total_kb),
        "available_pct": percent(available_kb, total_kb),
        "swap_total_kb": swap_total_kb,
        "swap_free_kb": swap_free_kb,
        "swap_used_kb": swap_used_kb,
        "swap_used_pct": percent(swap_used_kb, swap_total_kb) if swap_total_kb > 0 else 0.0,
    }


def format_mib(value_kb: int) -> str:
    return f"{mib(value_kb):,.0f} MiB"


def format_text(report: dict[str, Any]) -> str:
    lines = [
        f"Memory source: {report['source']}",
        "Source of truth: "
        f"{report['real_pressure_pct']:.1f}% real pressure "
        f"({format_mib(report['real_pressure_kb'])} of {format_mib(report['total_kb'])}) "
        f"using {report['available_source']}",
        "Provider-style cached-inclusive line: "
        f"{report['provider_like_pct']:.1f}% "
        f"({format_mib(report['provider_like_kb'])})",
        "Cached/reclaimable shown separately: "
        f"{report['reclaimable_cache_pct']:.1f}% "
        f"({format_mib(report['reclaimable_cache_kb'])})",
        "",
        "Breakdown:",
        f"  Total: {format_mib(report['total_kb'])}",
        f"  Available: {format_mib(report['available_kb'])}",
        f"  Free: {format_mib(report['free_kb'])}",
        f"  Buffers: {format_mib(report['buffers_kb'])}",
        f"  Cached: {format_mib(report['cached_kb'])}",
        f"  Reclaimable slab: {format_mib(report['sreclaimable_kb'])}",
        f"  Anonymous/application pages: {format_mib(report['anon_kb'])}",
    ]
    if report["swap_total_kb"] > 0:
        lines.append(
            "  Swap used: "
            f"{format_mib(report['swap_used_kb'])} "
            f"({report['swap_used_pct']:.1f}%)"
        )
    else:
        lines.append("  Swap used: 0 MiB")
    lines.extend(
        [
            "",
            "Formulas:",
            f"  Real pressure: {report['formulas']['real_pressure']}",
            f"  Provider-style comparison: {report['formulas']['provider_like']}",
        ]
    )
    return "\n".join(lines) + "\n"


def format_prometheus(report: dict[str, Any]) -> str:
    metrics = {
        "arbuzas_memory_real_pressure_percent": report["real_pressure_pct"],
        "arbuzas_memory_provider_like_percent": report["provider_like_pct"],
        "arbuzas_memory_reclaimable_cache_percent": report["reclaimable_cache_pct"],
        "arbuzas_memory_available_percent": report["available_pct"],
        "arbuzas_memory_real_pressure_bytes": report["real_pressure_kb"] * 1024,
        "arbuzas_memory_available_bytes": report["available_kb"] * 1024,
        "arbuzas_memory_reclaimable_cache_bytes": report["reclaimable_cache_kb"] * 1024,
        "arbuzas_memory_total_bytes": report["total_kb"] * 1024,
        "arbuzas_memory_provider_like_bytes": report["provider_like_kb"] * 1024,
    }
    lines = [
        "# HELP arbuzas_memory_real_pressure_percent Corrected memory pressure using MemAvailable.",
        "# TYPE arbuzas_memory_real_pressure_percent gauge",
    ]
    for name, value in metrics.items():
        if name != "arbuzas_memory_real_pressure_percent":
            lines.append(f"# TYPE {name} gauge")
        if isinstance(value, float):
            lines.append(f"{name} {value:.6f}")
        else:
            lines.append(f"{name} {value}")
    return "\n".join(lines) + "\n"


def write_atomic(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp_path = path.with_name(f".{path.name}.{os.getpid()}.tmp")
    tmp_path.write_text(content, encoding="utf-8")
    tmp_path.chmod(0o644)
    os.replace(tmp_path, path)


def write_report_dir(report: dict[str, Any], output_dir: Path) -> None:
    write_atomic(output_dir / "latest.json", json.dumps(report, indent=2, sort_keys=True) + "\n")
    write_atomic(output_dir / "latest.txt", format_text(report))
    write_atomic(output_dir / "latest.prom", format_prometheus(report))


def print_text(report: dict[str, Any]) -> None:
    sys.stdout.write(format_text(report))


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Report Linux host memory pressure from /proc/meminfo, separating reclaimable cache.",
    )
    parser.add_argument(
        "--meminfo",
        default="/proc/meminfo",
        help="Path to a procfs meminfo file. Defaults to /proc/meminfo.",
    )
    parser.add_argument(
        "--source-label",
        default="",
        help="Label printed as the source. Defaults to the --meminfo path.",
    )
    parser.add_argument(
        "--format",
        choices=("text", "json", "prometheus"),
        default="text",
        help="Output format. Defaults to text.",
    )
    parser.add_argument(
        "--json",
        action="store_true",
        help="Shortcut for --format json.",
    )
    parser.add_argument(
        "--write-dir",
        default="",
        help="Write latest.json, latest.txt, and latest.prom to this directory.",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    meminfo_path = Path(args.meminfo)
    source_label = args.source_label or str(meminfo_path)

    try:
        values = parse_meminfo(meminfo_path.read_text(encoding="utf-8"))
        report = build_report(values, source_label)
    except (OSError, ValueError) as exc:
        print(f"memory-report failed: {exc}", file=sys.stderr)
        return 1

    if args.write_dir:
        write_report_dir(report, Path(args.write_dir))

    if args.json or args.format == "json":
        json.dump(report, sys.stdout, indent=2, sort_keys=True)
        print()
    elif args.format == "prometheus":
        sys.stdout.write(format_prometheus(report))
    else:
        print_text(report)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
