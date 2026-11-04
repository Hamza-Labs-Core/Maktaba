#!/usr/bin/env python3
"""Tests of bundle_size.py (story 51.17): python3 tools/release/bundle-size/test_bundle_size.py"""

from __future__ import annotations

import io
import json
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import bundle_size  # noqa: E402

BASE = bundle_size.load_baseline()
FULL = BASE["full_dmg_bytes"]


class FootprintTest(unittest.TestCase):
    """N7 is on footprints after setup: payload + first-run downloads."""

    CAT = {"component": [
        {"id": "model.a", "kind": "asset", "bundles": ["full"], "platforms": ["darwin-arm64", "linux-amd64"],
         "source": "hf:x/a@1", "sources": {"darwin-arm64": "hf:x/a-mlx@1"},
         "files": [{"path": "w.bin", "size": 1000},
                   {"source": "hf:x/a-mlx@1", "path": "w.safetensors", "size": 700}]},
        {"id": "runtime.ollama", "kind": "asset", "bundles": ["full"], "in_payload": True,
         "files": [{"path": "ollama", "size": 5000}]},
        {"id": "model.core", "kind": "asset", "bundles": ["full", "core"], "source": "hf:x/c@1",
         "files": [{"path": "c.bin", "size": 30}]},
        {"id": "transcription", "kind": "stages+process", "bundles": ["full"]},
    ]}

    def test_downloads_follow_the_platform_source_and_skip_the_payload(self) -> None:
        self.assertEqual(bundle_size.first_run_bytes(self.CAT, "full", "darwin-arm64"), 730)
        self.assertEqual(bundle_size.first_run_bytes(self.CAT, "full", "linux-amd64"), 1030)
        self.assertEqual(bundle_size.first_run_bytes(self.CAT, "core", "darwin-arm64"), 30)

    def test_full_downloads_count_toward_the_share(self) -> None:
        # a Core dmg at 84% of the Full dmg fails dmg-to-dmg, and passes once Full's
        # first-run downloads (the models) are counted
        core = int(FULL * 0.84)
        self.assertFalse(bundle_size.checks(BASE, core)[0].ok)
        cs = bundle_size.checks(BASE, core, full_downloads=2_000_000_000)
        self.assertTrue(all(c.ok for c in cs), cs)

    def test_core_downloads_count_toward_its_cap(self) -> None:
        cs = bundle_size.checks(BASE, 300_000_000, full_downloads=10**10, core_downloads=150_000_000)
        self.assertFalse(cs[1].ok)

    def test_the_real_catalog_has_full_downloads_on_the_mac(self) -> None:
        cat = bundle_size.load_catalog()
        self.assertGreater(bundle_size.first_run_bytes(cat, "full", "darwin-arm64"), 1_000_000_000)
        self.assertEqual(bundle_size.first_run_bytes(cat, "core", "darwin-arm64"), 0)


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

    def test_exactly_four_processes(self) -> None:
        # N9: exactly postgres, api, streaming and pipeline; fewer is a part missing
        for n, ok in ((3, False), (4, True), (5, False)):
            cs = bundle_size.checks(BASE, 50_000_000, processes=n)
            self.assertEqual(cs[-1].ok, ok, n)

    def test_home_measures_from_core_home_json(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            home = Path(d) / "core-home.json"
            home.write_text(json.dumps({"idle_rss_mb": 512.5, "processes": 4, "roles": []}), encoding="utf-8")
            with redirect_stdout(io.StringIO()) as out:
                self.assertEqual(bundle_size.main(["--core-bytes", "60000000", "--home", str(home)]), 0)
            self.assertIn("ok   idle memory: 512.5 MB", out.getvalue())
            self.assertIn("ok   processes: 4.0 processes (budget exactly 4.0 processes)", out.getvalue())
            home.write_text(json.dumps({"idle_rss_mb": 900, "processes": 6}), encoding="utf-8")
            with redirect_stdout(io.StringIO()) as out:
                self.assertEqual(bundle_size.main(["--core-bytes", "60000000", "--home", str(home)]), 1)
            self.assertIn("FAIL idle memory", out.getvalue())
            self.assertIn("FAIL processes", out.getvalue())

    def test_zero_is_refused(self) -> None:
        with self.assertRaises(ValueError):
            bundle_size.checks(BASE, 0)

    def test_main_exit_codes(self) -> None:
        with redirect_stdout(io.StringIO()) as out:
            self.assertEqual(bundle_size.main(["--core-bytes", "60000000"]), 0)
            # past the 400 MB cap whatever the share
            self.assertEqual(bundle_size.main(["--core-bytes", "401000000"]), 1)
        self.assertIn("FAIL core footprint", out.getvalue())
        with redirect_stdout(io.StringIO()) as out:
            bundle_size.main(["--core-bytes", "60000000", "--summary"])
        self.assertIn("| core footprint | 60.0 MB | 400.0 MB | ok |", out.getvalue())



class ProcessTreeTest(unittest.TestCase):
    # a Core home on a Mac: the four services, and the mDNS loop's responder (sh, dns-sd)
    PS = """\
  100     1  5800 /Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-launcher
  101   100 44900 /Applications/Maktaba.app/Contents/Resources/server/bin/maktaba-server
  102   101 22100 /Applications/Maktaba.app/Contents/Resources/server/postgres/bin/postgres
  103   102  8500 postgres: maktaba maktaba 127.0.0.1(49356) idle
  104   101 48100 maktaba-api
  105   101 24000 maktaba-streaming
  106   101 74800 python
  107   101  2200 sh
  108   107  1800 /usr/bin/dns-sd
  109   107  1600 sh
"""

    def test_the_responder_is_a_helper_not_a_service(self) -> None:
        t = bundle_size.process_tree(100, self.PS)
        self.assertEqual(t["processes"], ["maktaba-api", "maktaba-streaming", "postgres", "python"])
        self.assertEqual(t["helpers"], ["sh"])
        self.assertEqual(sorted(t["tree"]), list(range(100, 110)), "every process's memory counts")

    def test_a_stray_shell_still_counts(self) -> None:
        ps = self.PS.replace("/usr/bin/dns-sd", "/usr/bin/sleep")
        t = bundle_size.process_tree(100, ps)
        self.assertEqual(len(t["processes"]), 5)
        self.assertEqual(t["helpers"], [])

    def test_imports_without_tomllib(self) -> None:
        # core-home.sh runs the Mac's /usr/bin/python3 (3.9): process_tree needs no tomllib
        code = (
            "import sys; sys.modules['tomllib'] = None; sys.path.insert(0, sys.argv[1]); "
            "import bundle_size; print(bundle_size.process_tree(100, sys.argv[2])['processes'])"
        )
        out = subprocess.run(
            [sys.executable, "-I", "-c", code, str(Path(__file__).parent), self.PS],
            capture_output=True, text=True, check=False,
        )
        self.assertEqual(out.returncode, 0, out.stderr)
        self.assertIn("maktaba-api", out.stdout)

    def test_no_supervisor(self) -> None:
        with self.assertRaises(ValueError):
            bundle_size.process_tree(100, "  100     1  5800 maktaba-launcher\n")

if __name__ == "__main__":
    unittest.main()
