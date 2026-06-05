#!/usr/bin/env python3
"""Contract tests for the Arbuzas host memory reporter."""

from __future__ import annotations

import importlib.util
import json
import subprocess
import sys
import tempfile
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent
REPORTER_PATH = REPO_ROOT / "tools" / "arbuzas" / "memory_report.py"

KITTY_GRATION_MEMINFO_SAMPLE = """\
MemTotal:        4015944 kB
MemFree:          598348 kB
MemAvailable:    2913588 kB
Buffers:          178464 kB
Cached:          2133912 kB
SwapCached:            0 kB
Active:           986724 kB
Inactive:        1960024 kB
Active(anon):     271496 kB
Inactive(anon):   312440 kB
Active(file):     715228 kB
Inactive(file):  1647584 kB
Unevictable:       27668 kB
Mlocked:           27668 kB
SwapTotal:             0 kB
SwapFree:              0 kB
Dirty:                68 kB
Writeback:             0 kB
AnonPages:        627172 kB
Mapped:           298364 kB
Shmem:             37980 kB
KReclaimable:     314036 kB
Slab:             393216 kB
SReclaimable:     314036 kB
SUnreclaim:        79180 kB
KernelStack:        9744 kB
PageTables:        10792 kB
SecPageTables:         0 kB
NFS_Unstable:          0 kB
Bounce:                0 kB
WritebackTmp:          0 kB
CommitLimit:     2007972 kB
Committed_AS:    3495544 kB
VmallocTotal:   34359738367 kB
VmallocUsed:       37964 kB
VmallocChunk:          0 kB
Percpu:             2880 kB
HardwareCorrupted:     0 kB
AnonHugePages:         0 kB
ShmemHugePages:        0 kB
ShmemPmdMapped:        0 kB
FileHugePages:         0 kB
FilePmdMapped:         0 kB
Unaccepted:            0 kB
HugePages_Total:       0
HugePages_Free:        0
HugePages_Rsvd:        0
HugePages_Surp:        0
Hugepagesize:       2048 kB
Hugetlb:               0 kB
DirectMap4k:      229248 kB
DirectMap2M:     3964928 kB
"""


def load_reporter():
    spec = importlib.util.spec_from_file_location("arbuzas_memory_report", REPORTER_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"failed to load {REPORTER_PATH}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def assert_rounded(actual: float, expected: float) -> None:
    if round(actual, 1) != expected:
        raise AssertionError(f"{actual:.3f} rounded to {round(actual, 1)}, expected {expected}")


def test_sample_calculates_real_pressure_and_provider_like_line() -> None:
    reporter = load_reporter()
    values = reporter.parse_meminfo(KITTY_GRATION_MEMINFO_SAMPLE)
    report = reporter.build_report(values, "kitty-gration sample")

    assert_rounded(report["provider_like_pct"], 80.7)
    assert_rounded(report["real_pressure_pct"], 27.4)
    assert_rounded(report["literal_nonfree_pct"], 85.1)
    assert_rounded(report["reclaimable_cache_pct"], 65.4)

    if report["provider_like_pct"] - report["real_pressure_pct"] < 50.0:
        raise AssertionError("sample should preserve the observed cached-inclusive gap")
    if report["formulas"]["real_pressure"] != "(MemTotal - MemAvailable) / MemTotal":
        raise AssertionError("real pressure formula must use MemAvailable")
    if report["formulas"]["provider_like"] != "(MemTotal - MemFree - Buffers) / MemTotal":
        raise AssertionError("provider-like comparison formula changed")


def test_cli_outputs_text_and_json_for_sample() -> None:
    with tempfile.NamedTemporaryFile("w", encoding="utf-8") as sample, tempfile.TemporaryDirectory() as output_dir:
        sample.write(KITTY_GRATION_MEMINFO_SAMPLE)
        sample.flush()

        text_result = subprocess.run(
            [
                sys.executable,
                str(REPORTER_PATH),
                "--meminfo",
                sample.name,
                "--source-label",
                "kitty-gration sample",
            ],
            check=True,
            text=True,
            capture_output=True,
        )
        text_output = text_result.stdout
        if "27.4% real pressure" not in text_output:
            raise AssertionError(text_output)
        if "Provider-style cached-inclusive line: 80.7%" not in text_output:
            raise AssertionError(text_output)
        if "Cached/reclaimable shown separately: 65.4%" not in text_output:
            raise AssertionError(text_output)

        json_result = subprocess.run(
            [
                sys.executable,
                str(REPORTER_PATH),
                "--meminfo",
                sample.name,
                "--json",
            ],
            check=True,
            text=True,
            capture_output=True,
        )
        payload = json.loads(json_result.stdout)
        assert_rounded(payload["real_pressure_pct"], 27.4)
        assert_rounded(payload["provider_like_pct"], 80.7)

        prometheus_result = subprocess.run(
            [
                sys.executable,
                str(REPORTER_PATH),
                "--meminfo",
                sample.name,
                "--format",
                "prometheus",
            ],
            check=True,
            text=True,
            capture_output=True,
        )
        if "arbuzas_memory_real_pressure_percent" not in prometheus_result.stdout:
            raise AssertionError(prometheus_result.stdout)

        subprocess.run(
            [
                sys.executable,
                str(REPORTER_PATH),
                "--meminfo",
                sample.name,
                "--write-dir",
                output_dir,
            ],
            check=True,
            text=True,
            capture_output=True,
        )
        written_dir = Path(output_dir)
        written_json = json.loads((written_dir / "latest.json").read_text(encoding="utf-8"))
        written_text = (written_dir / "latest.txt").read_text(encoding="utf-8")
        written_prom = (written_dir / "latest.prom").read_text(encoding="utf-8")
        assert_rounded(written_json["real_pressure_pct"], 27.4)
        if "27.4% real pressure" not in written_text:
            raise AssertionError(written_text)
        if "arbuzas_memory_reclaimable_cache_percent" not in written_prom:
            raise AssertionError(written_prom)


def main() -> int:
    test_sample_calculates_real_pressure_and_provider_like_line()
    test_cli_outputs_text_and_json_for_sample()
    print("arbuzas memory report contract: ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
