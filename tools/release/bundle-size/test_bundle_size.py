#!/usr/bin/env python3
"""Tests of bundle_size.py (story 51.17): python3 tools/release/bundle-size/test_bundle_size.py"""

from __future__ import annotations

import io
import sys
import unittest
from contextlib import redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import bundle_size  # noqa: E402

BASE = bundle_size.load_baseline()
FULL = BASE["full_dmg_bytes"]


class BudgetTest(unittest.TestCase):
    def test_baseline_is_v0362(self) -> None:
        self.assertEqual(BASE["version"], "v0.3.62")
        self.assertEqual(FULL, 188_301_220)

    def test_core_under_its_share_passes(self) -> None:
        cs = bundle_size.checks(BASE, int(FULL * 0.40))
        self.assertTrue(all(c.ok for c in cs), cs)

    def test_core_past_its_share_fails(self) -> None:
        cs = bundle_size.checks(BASE, int(FULL * 0.46))
        self.assertFalse(cs[0].ok)

    def test_a_full_size_given_is_the_reference(self) -> None:
        # a Full dmg measured in the same run, twice the baseline: 80 MB is then 21%
        cs = bundle_size.checks(BASE, 80_000_000, full_bytes=2 * FULL)
        self.assertTrue(cs[0].ok)

    def test_the_cap_holds_whatever_the_share(self) -> None:
        cs = bundle_size.checks(BASE, 401_000_000, full_bytes=10 * FULL)
        self.assertTrue(cs[0].ok)
        self.assertFalse(cs[1].ok)

    def test_memory_and_processes(self) -> None:
        cs = bundle_size.checks(BASE, 50_000_000, idle_rss_mb=701, processes=5)
        self.assertEqual([c.ok for c in cs], [True, True, False, False])
        self.assertEqual(len(bundle_size.checks(BASE, 50_000_000)), 2)

    def test_zero_is_refused(self) -> None:
        with self.assertRaises(ValueError):
            bundle_size.checks(BASE, 0)

    def test_main_exit_codes(self) -> None:
        with redirect_stdout(io.StringIO()) as out:
            self.assertEqual(bundle_size.main(["--core-bytes", "60000000"]), 0)
            self.assertEqual(bundle_size.main(["--core-bytes", str(FULL)]), 1)
        self.assertIn("FAIL core / full", out.getvalue())
        with redirect_stdout(io.StringIO()) as out:
            bundle_size.main(["--core-bytes", "60000000", "--summary"])
        self.assertIn("| core size | 60.0 MB | 400.0 MB | ok |", out.getvalue())


if __name__ == "__main__":
    unittest.main()
