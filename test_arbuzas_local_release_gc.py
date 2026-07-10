#!/usr/bin/env python3
from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent
SCRIPT = REPO_ROOT / "tools" / "arbuzas" / "local_release_gc.py"
NOW = 1_800_000_000.0


class LocalReleaseGCTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tempdir = tempfile.TemporaryDirectory()
        self.base = Path(self.tempdir.name)
        self.releases = self.base / "output" / "arbuzas" / "releases"
        self.releases.mkdir(parents=True)

    def tearDown(self) -> None:
        self.tempdir.cleanup()

    def make_release(self, name: str, age_hours: int, body: bytes = b"release") -> Path:
        release = self.releases / name
        release.mkdir()
        (release / "release.bin").write_bytes(body)
        mtime = NOW - age_hours * 60 * 60
        os.utime(release / "release.bin", (mtime, mtime))
        os.utime(release, (mtime, mtime))
        return release

    def run_gc(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                "python3",
                str(SCRIPT),
                "--releases-root",
                str(self.releases),
                "--now",
                str(NOW),
                *args,
            ],
            check=False,
            capture_output=True,
            text=True,
        )

    def test_dry_run_protects_release_and_never_leaves_managed_root(self) -> None:
        expired = self.make_release("20260101T000000Z", 100)
        recent = self.make_release("20260102T000000Z", 1)
        protected = self.make_release("20260103T000000Z", 100)
        unmanaged_file = self.releases / "keep-me.txt"
        unmanaged_file.write_text("not a release directory", encoding="utf-8")

        evidence = self.base / "ops" / "evidence" / "proof.txt"
        state = self.base / "state" / "runtime.json"
        secret = self.base / "secrets" / "token.secret"
        for marker in (evidence, state, secret):
            marker.parent.mkdir(parents=True, exist_ok=True)
            marker.write_text("keep", encoding="utf-8")

        outside = self.base / "outside-release"
        outside.mkdir()
        symlink = self.releases / "linked-release"
        symlink.symlink_to(outside, target_is_directory=True)

        result = self.run_gc(
            "--protect-release-id",
            protected.name,
            "--max-age-hours",
            "72",
            "--keep-per-family",
            "10",
            "--dry-run",
            "--verbose",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"action=would-remove release={expired.name}", result.stdout)
        self.assertIn("selected=1", result.stdout)
        self.assertIn("removed=0", result.stdout)
        for path in (expired, recent, protected, unmanaged_file, evidence, state, secret, symlink, outside):
            self.assertTrue(path.exists(), path)

    def test_actual_cleanup_enforces_age_and_per_family_limit(self) -> None:
        timestamped = []
        for index in range(12):
            timestamped.append(self.make_release(f"20260101T{index:02d}0000Z", index + 1))
        protected = timestamped[-1]

        fresh_other_family = self.make_release("ticket-debug-20260102T000000Z", 2)
        expired_other_family = self.make_release("ticket-debug-20260101T000000Z", 100)
        expired_unstructured = self.make_release("manual-debug-bundle", 100)

        evidence = self.base / "ops" / "evidence" / "proof.txt"
        evidence.parent.mkdir(parents=True)
        evidence.write_text("keep", encoding="utf-8")

        result = self.run_gc(
            "--protect-release-id",
            protected.name,
            "--max-age-hours",
            "72",
            "--keep-per-family",
            "10",
            "--verbose",
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("selected=3", result.stdout)
        self.assertIn("removed=3", result.stdout)
        self.assertFalse(timestamped[-2].exists())
        self.assertTrue(protected.exists())
        for release in timestamped[:10]:
            self.assertTrue(release.exists(), release)
        self.assertTrue(fresh_other_family.exists())
        self.assertFalse(expired_other_family.exists())
        self.assertFalse(expired_unstructured.exists())
        self.assertTrue(evidence.exists())

    def test_symlink_release_root_is_refused(self) -> None:
        alias = self.base / "release-alias"
        alias.symlink_to(self.releases, target_is_directory=True)
        result = subprocess.run(
            ["python3", str(SCRIPT), "--releases-root", str(alias), "--dry-run"],
            check=False,
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("refusing non-directory or symlink release root", result.stderr)


if __name__ == "__main__":
    unittest.main()
