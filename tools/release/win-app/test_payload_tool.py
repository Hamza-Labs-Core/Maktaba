"""Tests for payload_tool.py (Epic 38, story 38.11). Standard library only; they build small
PE files of their own, so they run anywhere:

    python3 tools/release/win-app/test_payload_tool.py
"""

from __future__ import annotations

import contextlib
import io
import os
import struct
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import payload_tool as pt  # noqa: E402


def make_pe(imports=(), delay=(), machine=pt.MACHINE_X64) -> bytes:
    """A PE32+ file with one section that holds an import table, a delay-load table and
    the DLL names (enough for read_pe: nothing here would run)."""
    va, raw = 0x1000, 0x200
    imp_size = 20 * (len(imports) + 1)
    delay_size = 32 * (len(delay) + 1)
    names_at = imp_size + delay_size
    names, rvas = bytearray(), []
    for n in (*imports, *delay):
        rvas.append(va + names_at + len(names))
        names += n.encode() + b"\0"
    body = bytearray(names_at) + names
    for i in range(len(imports)):
        struct.pack_into("<IIIII", body, 20 * i, 0, 0, 0, rvas[i], 0)
    for j in range(len(delay)):
        struct.pack_into("<8I", body, imp_size + 32 * j, 1, rvas[len(imports) + j], 0, 0, 0, 0, 0, 0)
    hdr = bytearray(raw)
    hdr[0:2] = b"MZ"
    struct.pack_into("<I", hdr, 0x3C, 0x40)
    hdr[0x40:0x44] = b"PE\0\0"
    struct.pack_into("<HHIIIHH", hdr, 0x44, machine, 1, 0, 0, 0, 240, 0x22)
    opt = 0x58
    struct.pack_into("<H", hdr, opt, 0x20B)
    struct.pack_into("<I", hdr, opt + 108, 16)
    if imports:
        struct.pack_into("<II", hdr, opt + 112 + 8 * 1, va, imp_size)
    if delay:
        struct.pack_into("<II", hdr, opt + 112 + 8 * 13, va + imp_size, delay_size)
    sec = opt + 240
    hdr[sec : sec + 8] = b".idata\0\0"
    struct.pack_into("<IIII", hdr, sec + 8, len(body), va, len(body), raw)
    return bytes(hdr) + bytes(body)


def put(path: Path, data: bytes) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return path


def quiet(fn, *args):
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        rc = fn(*args)
    return rc, out.getvalue(), err.getvalue()


class Base(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tmp = Path(tmp.name)
        # a Windows of our own: System32 holds kernel32.dll, and a Visual C++ runtime as a
        # build machine with Visual Studio has one
        root = self.tmp / "Windows"
        for n in ("kernel32.dll", "advapi32.dll", "msvcp140.dll", "vcruntime140.dll"):
            put(root / "System32" / n, b"MZ")
        old = os.environ.get("SystemRoot")
        os.environ["SystemRoot"] = str(root)
        self.addCleanup(lambda: os.environ.__setitem__("SystemRoot", old) if old is not None else os.environ.pop("SystemRoot"))
        self.sysdir = pt.system32()


class ReadPETest(Base):
    def test_imports_and_delay_loads(self):
        f = put(self.tmp / "a.exe", make_pe(["KERNEL32.dll", "libpq.dll"], ["icuuc67.dll"]))
        self.assertEqual(pt.read_pe(f), (pt.MACHINE_X64, ["icuuc67.dll", "kernel32.dll", "libpq.dll"]))

    def test_no_imports(self):
        f = put(self.tmp / "a.dll", make_pe(machine=0x14C))
        self.assertEqual(pt.read_pe(f), (0x14C, []))

    def test_not_a_pe(self):
        f = put(self.tmp / "a.dll", b"#!/bin/sh\n")
        with self.assertRaises(pt.NotPE):
            pt.read_pe(f)

    @unittest.skipUnless(sys.platform == "win32", "a real Windows program")
    def test_this_python(self):
        machine, names = pt.read_pe(Path(sys.executable))
        self.assertIn(machine, pt.MACHINES)
        self.assertTrue(any(n.startswith("python3") for n in names), names)


class WindowsDLLTest(Base):
    def test_api_sets_and_system32(self):
        self.assertTrue(pt.is_windows_dll("api-ms-win-crt-runtime-l1-1-0.dll", self.sysdir))
        self.assertTrue(pt.is_windows_dll("ext-ms-win-ntuser-window-l1-1-0.dll", self.sysdir))
        self.assertTrue(pt.is_windows_dll("KERNEL32.DLL", self.sysdir))
        self.assertFalse(pt.is_windows_dll("libpq.dll", self.sysdir))

    def test_the_visual_cpp_runtime_is_never_windows(self):
        # in this System32, as on a machine with Visual Studio, but not on every PC
        for n in ("msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll", "concrt140.dll", "msvcr120.dll"):
            self.assertFalse(pt.is_windows_dll(n, self.sysdir), n)


class ClosureTest(Base):
    def setUp(self):
        super().setUp()
        self.archive = self.tmp / "archive" / "bin"
        self.runtime = self.tmp / "crt"
        self.into = self.tmp / "out" / "postgres" / "bin"
        put(self.archive / "libpq.dll", make_pe(["kernel32.dll", "libssl.dll"]))
        put(self.archive / "libssl.dll", make_pe(["kernel32.dll", "vcruntime140.dll"]))
        put(self.archive / "wxbase.dll", make_pe(["kernel32.dll"]))  # nobody needs it
        put(self.archive / "vcruntime140.dll", make_pe(["kernel32.dll"]))  # not the runtime's
        put(self.runtime / "vcruntime140.dll", make_pe(["kernel32.dll"]))
        put(self.runtime / "msvcp140.dll", make_pe(["kernel32.dll", "vcruntime140.dll", "vcruntime140_1.dll"]))
        put(self.runtime / "vcruntime140_1.dll", make_pe(["kernel32.dll"]))
        self.prog = put(self.into / "psql.exe", make_pe(["kernel32.dll", "libpq.dll", "msvcp140.dll"]))

    def closure(self, *files):
        return quiet(pt.main, ["closure", "--dir", str(self.archive), "--into", str(self.into),
                               "--runtime", str(self.runtime), *map(str, files)])

    def test_transitive_from_the_archive_and_the_runtime(self):
        rc, out, err = self.closure(self.prog)
        self.assertEqual((rc, err), (0, ""))
        got = sorted(Path(p).relative_to(self.tmp).as_posix() for p in out.split())
        self.assertEqual(got, ["archive/bin/libpq.dll", "archive/bin/libssl.dll", "crt/msvcp140.dll",
                               "crt/vcruntime140.dll", "crt/vcruntime140_1.dll"])

    def test_an_extension_elsewhere_finds_what_into_has(self):
        # lib\ext.dll imports postgres.exe (in --into) and libpq.dll (copied there)
        put(self.into / "postgres.exe", make_pe(["kernel32.dll"]))
        ext = put(self.tmp / "out" / "postgres" / "lib" / "ext.dll", make_pe(["postgres.exe", "libpq.dll"]))
        rc, out, _ = self.closure(ext)
        self.assertEqual(rc, 0)
        self.assertIn("libpq.dll", out)
        self.assertNotIn("postgres.exe", out)

    def test_a_dll_nobody_has(self):
        prog = put(self.into / "x.exe", make_pe(["perl538.dll"]))
        rc, _, err = self.closure(prog)
        self.assertEqual(rc, 1)
        self.assertIn("missing perl538.dll (needed by x.exe)", err)


class CheckTest(Base):
    def payload(self):
        root = self.tmp / "server"
        put(root / "bin" / "maktaba-server.exe", make_pe(["kernel32.dll"]))
        put(root / "postgres" / "bin" / "postgres.exe", make_pe(["kernel32.dll", "libpq.dll", "vcruntime140.dll"]))
        put(root / "postgres" / "bin" / "libpq.dll", make_pe(["kernel32.dll"]))
        put(root / "postgres" / "bin" / "vcruntime140.dll", make_pe(["kernel32.dll"]))
        put(root / "postgres" / "lib" / "pgcrypto.dll", make_pe(["postgres.exe", "libpq.dll"]))
        put(root / "python" / "python.exe", make_pe(["kernel32.dll", "python312.dll"]))
        put(root / "python" / "python312.dll", make_pe(["kernel32.dll"]))
        put(root / "python" / "DLLs" / "_ssl.pyd", make_pe(["python312.dll", "libssl-3-x64.dll"]))
        put(root / "python" / "DLLs" / "libssl-3-x64.dll", make_pe(["kernel32.dll"]))
        # pip's launcher templates for other machines are never loaded here
        put(root / "python" / "Lib" / "site-packages" / "pip" / "t32.exe", make_pe(["kernel32.dll"], machine=0x14C))
        put(root / "payload.json", b"{}")
        return root

    def test_a_self_contained_payload(self):
        rc, out, _ = quiet(pt.main, ["check", str(self.payload())])
        self.assertEqual(rc, 0, out)
        self.assertIn("9 PE files", out)  # not the launcher template

    def test_an_extension_import_only_in_lib_is_not_found(self):
        root = self.payload()
        put(root / "postgres" / "lib" / "libxml2.dll", make_pe(["kernel32.dll"]))
        put(root / "postgres" / "lib" / "pgxml.dll", make_pe(["postgres.exe", "libxml2.dll"]))
        rc, out, _ = quiet(pt.main, ["check", str(root)])
        self.assertEqual(rc, 1)
        self.assertIn("postgres/lib/pgxml.dll: imports libxml2.dll", out)

    def test_the_runtime_from_system32_is_not_enough(self):
        root = self.payload()
        (root / "postgres" / "bin" / "vcruntime140.dll").unlink()
        rc, out, _ = quiet(pt.main, ["check", str(root)])
        self.assertEqual(rc, 1)
        self.assertIn("postgres/bin/postgres.exe: imports vcruntime140.dll", out)

    def test_not_x86_64(self):
        root = self.payload()
        put(root / "ffmpeg" / "ffmpeg.exe", make_pe(["kernel32.dll"], machine=0xAA64))
        rc, out, _ = quiet(pt.main, ["check", str(root)])
        self.assertEqual(rc, 1)
        self.assertIn("ffmpeg/ffmpeg.exe: arm64, not x86_64", out)


if __name__ == "__main__":
    unittest.main()
