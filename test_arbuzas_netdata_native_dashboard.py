#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import json
import stat
import sys
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent
MODULE_PATH = (
    REPO_ROOT
    / "infra"
    / "arbuzas"
    / "netdata"
    / "native-dashboard"
    / "arbuzas_netdata_native_dashboard.py"
)
SPEC = importlib.util.spec_from_file_location("arbuzas_netdata_native_dashboard", MODULE_PATH)
assert SPEC and SPEC.loader
PATCHER = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = PATCHER
SPEC.loader.exec_module(PATCHER)


class NativeDashboardPatchTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.web_root = Path(self.temporary.name)
        self.original = '<!doctype html><html><head><meta charset="utf-8"></head><body></body></html>'
        for relative_path in PATCHER.ENTRYPOINTS:
            path = self.web_root / relative_path
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(self.original, encoding="utf-8")
            path.chmod(0o640)
        self.dashboard_root = self.web_root / "kitty-gration"
        self.dashboard_root.mkdir()
        self.script = "native-mobile.ABCDEFGH.js"
        self.stylesheet = "native-mobile.HGFEDCBA.css"
        (self.dashboard_root / self.script).write_text("// mobile", encoding="utf-8")
        (self.dashboard_root / self.stylesheet).write_text("/* mobile */", encoding="utf-8")
        self._write_manifest(self.script, self.stylesheet)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def _write_manifest(self, script: str, stylesheet: str) -> None:
        (self.dashboard_root / "build.json").write_text(
            json.dumps(
                {
                    "assets": ["app.AAAAAAAA.js", "app.BBBBBBBB.css", script, stylesheet],
                    "nativeMobile": {
                        "script": script,
                        "stylesheet": stylesheet,
                        "viewport": PATCHER.VIEWPORT_CONTENT,
                    },
                }
            ),
            encoding="utf-8",
        )

    def test_apply_is_idempotent_and_preserves_mode(self) -> None:
        PATCHER.run("apply", self.web_root)
        first = {
            path: (path.read_bytes(), path.stat().st_mtime_ns)
            for path in (self.web_root / item for item in PATCHER.ENTRYPOINTS)
        }
        PATCHER.run("apply", self.web_root)

        for path, (contents, modified_at) in first.items():
            document = path.read_text(encoding="utf-8")
            self.assertEqual(document.count(PATCHER.MARKER_START), 1)
            self.assertEqual(document.count('name="viewport"'), 1)
            self.assertEqual(document.count('data-kitty-netdata-mobile="stylesheet"'), 1)
            self.assertEqual(document.count('data-kitty-netdata-mobile="script"'), 1)
            self.assertEqual(path.read_bytes(), contents)
            self.assertEqual(path.stat().st_mtime_ns, modified_at)
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o640)

    def test_remove_restores_the_original_entrypoints(self) -> None:
        PATCHER.run("apply", self.web_root)
        PATCHER.run("remove", self.web_root)
        for relative_path in PATCHER.ENTRYPOINTS:
            self.assertEqual(
                (self.web_root / relative_path).read_text(encoding="utf-8"),
                self.original,
            )

    def test_upstream_responsive_viewport_is_kept_while_assets_are_injected(self) -> None:
        upstream = self.original.replace(
            "<head>",
            '<head><meta name="viewport" content="width=device-width, initial-scale=1">',
        )
        for relative_path in PATCHER.ENTRYPOINTS:
            (self.web_root / relative_path).write_text(upstream, encoding="utf-8")

        changes = PATCHER.run("apply", self.web_root)

        self.assertTrue(all(change.status == "injected" for change in changes))
        self.assertTrue(
            all(
                (self.web_root / relative_path).read_text(encoding="utf-8").count('name="viewport"') == 1
                for relative_path in PATCHER.ENTRYPOINTS
            )
        )
        self.assertTrue(
            all(
                PATCHER.VIEWPORT_META not in (self.web_root / relative_path).read_text(encoding="utf-8")
                for relative_path in PATCHER.ENTRYPOINTS
            )
        )

    def test_strict_mode_plans_every_file_before_writing(self) -> None:
        malformed_path = self.web_root / PATCHER.ENTRYPOINTS[-1]
        malformed_path.write_text("<html><body>missing head</body></html>", encoding="utf-8")

        with self.assertRaises(PATCHER.DashboardPatchError):
            PATCHER.run("apply", self.web_root)

        for relative_path in PATCHER.ENTRYPOINTS[:-1]:
            self.assertEqual(
                (self.web_root / relative_path).read_text(encoding="utf-8"),
                self.original,
            )

    def test_best_effort_mode_keeps_future_package_changes_non_blocking(self) -> None:
        missing_path = self.web_root / PATCHER.ENTRYPOINTS[-1]
        missing_path.unlink()

        changes = PATCHER.run("apply", self.web_root, best_effort=True)

        self.assertEqual(len(changes), len(PATCHER.ENTRYPOINTS) - 1)
        for change in changes:
            self.assertEqual(change.updated.count(PATCHER.MARKER_START), 1)

    def test_check_rejects_a_duplicate_viewport(self) -> None:
        PATCHER.run("apply", self.web_root)
        path = self.web_root / PATCHER.ENTRYPOINTS[0]
        path.write_text(
            path.read_text(encoding="utf-8")
            .replace("</head>", '<meta name="viewport" content="width=device-width"></head>'),
            encoding="utf-8",
        )

        with self.assertRaises(PATCHER.DashboardPatchError):
            PATCHER.run("check", self.web_root)

    def test_apply_refreshes_manifest_hashed_assets_without_duplicates(self) -> None:
        PATCHER.run("apply", self.web_root)
        new_script = "native-mobile.ZYXWVUTS.js"
        new_stylesheet = "native-mobile.STUVWXYZ.css"
        (self.dashboard_root / new_script).write_text("// refreshed", encoding="utf-8")
        (self.dashboard_root / new_stylesheet).write_text("/* refreshed */", encoding="utf-8")
        self._write_manifest(new_script, new_stylesheet)

        PATCHER.run("apply", self.web_root)
        PATCHER.run("check", self.web_root)

        for relative_path in PATCHER.ENTRYPOINTS:
            document = (self.web_root / relative_path).read_text(encoding="utf-8")
            self.assertIn(f'/kitty-gration/{new_script}', document)
            self.assertIn(f'/kitty-gration/{new_stylesheet}', document)
            self.assertNotIn(f'/kitty-gration/{self.script}', document)
            self.assertNotIn(f'/kitty-gration/{self.stylesheet}', document)
            self.assertEqual(document.count('data-kitty-netdata-mobile="stylesheet"'), 1)
            self.assertEqual(document.count('data-kitty-netdata-mobile="script"'), 1)


if __name__ == "__main__":
    unittest.main()
