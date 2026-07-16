#!/usr/bin/env python3

import importlib.util
import sys
import unittest
from pathlib import Path


HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("source_footprint", HERE / "source_footprint.py")
assert SPEC and SPEC.loader
SOURCE_FOOTPRINT = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = SOURCE_FOOTPRINT
SPEC.loader.exec_module(SOURCE_FOOTPRINT)


class SourceFootprintTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = SOURCE_FOOTPRINT.load_manifest(HERE / "source_manifest.json")

    def test_contract_arithmetic(self):
        contract = self.manifest["contract"]
        baselines = {
            name: spec["expected_baseline"]
            for name, spec in self.manifest["repositories"].items()
        }
        self.assertEqual(
            contract["baseline_lines"],
            baselines["ops"]["lines"] + baselines["pixel"]["lines"],
        )
        self.assertEqual(
            contract["minimum_line_reduction"],
            contract["baseline_lines"] - contract["maximum_lines"]["combined"],
        )

    def test_immutable_baseline_matches_manifest(self):
        repos = {
            "ops": SOURCE_FOOTPRINT.DEFAULT_OPS_REPO,
            "pixel": SOURCE_FOOTPRINT.DEFAULT_PIXEL_REPO,
        }
        if not all((repo / ".git").exists() for repo in repos.values()):
            self.skipTest("ops and pixel-phone sibling repositories are required")
        for name, repo in repos.items():
            spec = self.manifest["repositories"][name]
            actual, _ = SOURCE_FOOTPRINT.baseline_metrics(repo, spec)
            self.assertEqual(spec["expected_baseline"], actual.as_dict())

    def test_generated_tests_and_docs_are_excluded(self):
        cases = {
            "ops": [
                "workloads/ticket-remote/internal/web/server_test.go",
                "workloads/ticket-remote/web-client/src/generated/index.ts",
                "workloads/ticket-remote/internal/web/static/app.js",
                "workloads/ticket-remote/docs/design.md",
            ],
            "pixel": [
                "orchestrator/android-orchestrator/app/src/test/FakeTicketTest.kt",
                "tools/observability/tests/test_ticket_health_monitor.py",
                "docs/architecture/TICKET_STREAMING_ARCHITECTURE.md",
                "ops/evidence/ticket/frame.png",
            ],
        }
        for name, paths in cases.items():
            selected = SOURCE_FOOTPRINT.select_paths(
                paths, self.manifest["repositories"][name]
            )
            self.assertEqual({}, selected)


if __name__ == "__main__":
    unittest.main()
