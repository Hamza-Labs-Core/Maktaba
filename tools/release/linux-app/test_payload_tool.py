"""Tests for payload_tool.py (Epic 39, story 39.2). Run on Linux:

    python3 tools/release/linux-app/test_payload_tool.py
"""

from __future__ import annotations

import contextlib
import io
import json
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import payload_tool as pt  # noqa: E402


def quiet(fn, *args):
    out = io.StringIO()
    with contextlib.redirect_stdout(out):
        rc = fn(*args)
    return rc, out.getvalue()


class ManifestTest(unittest.TestCase):
    def setUp(self) -> None:
        self.root = Path(tempfile.mkdtemp())
        (self.root / "bin").mkdir()
        (self.root / "bin" / "tool").write_bytes(b"#!/bin/sh\n")
        os.chmod(self.root / "bin" / "tool", 0o755)
        (self.root / "lib").mkdir()
        (self.root / "lib" / "libx.so.1.2").write_bytes(b"x")
        os.symlink("libx.so.1.2", self.root / "lib" / "libx.so.1")
        (self.root / "payload.json").write_text("{}")

    def tearDown(self) -> None:
        shutil.rmtree(self.root)

    def test_lists_files_and_symlinks_sorted(self) -> None:
        rc, out = quiet(pt.cmd_manifest, self.root)
        self.assertEqual(rc, 0)
        lines = (self.root / pt.MANIFEST).read_text().splitlines()
        lib = self.root / "lib" / "libx.so.1.2"
        self.assertEqual(
            lines,
            [
                f"f\t0755\t10\t{pt._sha256(self.root / 'bin' / 'tool')}\tbin/tool",
                "l\tlibx.so.1.2\tlib/libx.so.1",
                f"f\t{lib.stat().st_mode & 0o7777:04o}\t1\t{pt._sha256(lib)}\tlib/libx.so.1.2",
            ],
        )
        summary = json.loads(out)
        self.assertEqual(summary["count"], 3)
        self.assertEqual(summary["bytes"], 11)

    def test_verify_finds_changed_added_and_missing(self) -> None:
        quiet(pt.cmd_manifest, self.root)
        self.assertEqual(quiet(pt.cmd_verify, self.root)[0], 0)
        (self.root / "bin" / "tool").write_bytes(b"#!/bin/sh\necho\n")
        (self.root / "new").write_text("n")
        (self.root / "lib" / "libx.so.1").unlink()
        rc, out = quiet(pt.cmd_verify, self.root)
        self.assertEqual(rc, 1)
        self.assertIn("changed: bin/tool", out)
        self.assertIn("added:   new", out)
        self.assertIn("missing: lib/libx.so.1", out)

    def test_a_mode_change_is_a_change(self) -> None:
        quiet(pt.cmd_manifest, self.root)
        os.chmod(self.root / "bin" / "tool", 0o644)
        rc, out = quiet(pt.cmd_verify, self.root)
        self.assertEqual(rc, 1)
        self.assertIn("changed: bin/tool", out)


@unittest.skipUnless(sys.platform.startswith("linux"), "ELF files of this machine")
class ElfTest(unittest.TestCase):
    def test_reads_needed_and_glibc_versions_of_a_system_binary(self) -> None:
        elf = pt.Elf(Path("/bin/ls").resolve().read_bytes())
        needed, _paths = elf.dynamic()
        self.assertIn("libc.so.6", needed)
        self.assertTrue(any(v.startswith("GLIBC_2.") for v in elf.versions()))

    def test_a_missing_library_and_a_glibc_limit_are_reported(self) -> None:
        root = Path(tempfile.mkdtemp())
        try:
            shutil.copy(Path("/bin/ls").resolve(), root / "ls")
            rc, out = quiet(pt.cmd_elf, root, "2.0")
            self.assertEqual(rc, 1)
            self.assertIn("the payload's limit is GLIBC_2.0", out)
            summary = json.loads(out.splitlines()[-1])
            self.assertEqual(summary["elf_files"], 1)
            self.assertGreaterEqual(pt._ver(summary["min_glibc"]), (2, 3))
        finally:
            shutil.rmtree(root)

    def test_resolve_runpath_and_origin_paths(self) -> None:
        root = Path(tempfile.mkdtemp())
        try:
            (root / "postgres" / "bin").mkdir(parents=True)
            (root / "postgres" / "lib").mkdir()
            (root / "postgres" / "lib" / "libssl.so.3").write_bytes(b"x")
            (root / "python" / "lib").mkdir(parents=True)
            (root / "python" / "lib" / "libpython3.12.so.1.0").write_bytes(b"x")
            psql = root / "postgres" / "bin" / "psql"
            libpy3 = root / "python" / "lib" / "libpython3.so"
            self.assertEqual(
                pt._resolve(root, psql, "libssl.so.3", ["$ORIGIN/../lib"]),
                root / "postgres" / "bin" / ".." / "lib" / "libssl.so.3",
            )
            self.assertIsNone(pt._resolve(root, psql, "libxml2.so.2", ["$ORIGIN/../lib"]))
            self.assertIsNone(pt._resolve(root, psql, "libssl.so.3", []))
            # python-build-standalone: NEEDED is a path
            self.assertIsNotNone(pt._resolve(root, libpy3, "$ORIGIN/../lib/libpython3.12.so.1.0", []))
            # a path outside the payload is not the payload's
            self.assertIsNone(pt._resolve(root, psql, "libc.so.6", ["/lib/x86_64-linux-gnu"]))
        finally:
            shutil.rmtree(root)

    def test_not_an_elf_file(self) -> None:
        with self.assertRaises(ValueError):
            pt.Elf(b"#!/bin/sh\n" + bytes(64))


if __name__ == "__main__":
    unittest.main()
