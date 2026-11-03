#!/usr/bin/env python3
"""Tests of core-feed-check.py: python3 tools/release/mac-app/test_core_feed_check.py"""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path

_spec = importlib.util.spec_from_file_location("core_feed_check", Path(__file__).with_name("core-feed-check.py"))
assert _spec is not None and _spec.loader is not None
cfc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(cfc)

BASE = "https://github.com/o/r/releases/download/v1.2.3/"


def entry(name: str) -> dict:
    return {"url": BASE + name, "signature": "sig"}


class CoreFeedTest(unittest.TestCase):
    def test_the_core_keys_pass(self) -> None:
        core = entry("Maktaba-Core_1.2.3_aarch64.app.tar.gz")
        feed = {"platforms": {"darwin-aarch64-core": core, "darwin-aarch64-core-app": core}}
        self.assertEqual(cfc.problems(feed, "v1.2.3"), [])

    def test_a_full_key_fails(self) -> None:
        core = entry("Maktaba-Core_1.2.3_aarch64.app.tar.gz")
        feed = {"platforms": {"darwin-aarch64-core": core, "darwin-aarch64-core-app": core, "darwin-aarch64": core}}
        self.assertTrue(cfc.problems(feed, "1.2.3"))

    def test_the_full_archive_under_a_core_key_fails(self) -> None:
        full = entry("Maktaba_1.2.3_aarch64.app.tar.gz")
        feed = {"platforms": {"darwin-aarch64-core": full, "darwin-aarch64-core-app": full}}
        self.assertEqual(len(cfc.problems(feed, "1.2.3")), 2)

    def test_no_signature_fails(self) -> None:
        core = {"url": BASE + "Maktaba-Core_1.2.3_aarch64.app.tar.gz", "signature": ""}
        feed = {"platforms": {"darwin-aarch64-core": core, "darwin-aarch64-core-app": core}}
        self.assertEqual(len(cfc.problems(feed, "1.2.3")), 2)


if __name__ == "__main__":
    unittest.main()
