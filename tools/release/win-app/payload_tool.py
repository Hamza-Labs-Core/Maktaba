"""Helpers for the Windows app's server payload (Epic 38, story 38.11), standard library only.

payload.sh and check.sh run it with the payload's own Python (``python/python.exe -I``), so a
build machine needs no Python and no binutils of its own.

  imports FILE...                  print each PE file's machine and the DLLs it imports
                                   (the import and the delay-load tables), lower case
  closure --dir DIR --into DIR [--runtime DIR] FILE...
                                   print the DLLs FILE... need, transitively, that are neither
                                   Windows' nor already in --into (where they are loaded
                                   from: postgres\bin): each as "<dir>\\<name>" from --dir (the
                                   archive's bin) or from --runtime (the Visual C++ runtime
                                   folder). Exits 1 naming what neither has
  check DIR                        every PE file in DIR is x86_64 and finds each DLL it
                                   imports in the payload (where it is looked for at run
                                   time: SEARCH) or in Windows; exits 1 listing the rest

"Windows' DLLs" are the API sets (api-ms-win-*, ext-ms-*) and what %SystemRoot%\\System32
holds, except the Visual C++ runtime: a build machine with Visual Studio has it in System32,
a user's PC may not, so it must be in the payload.
"""

from __future__ import annotations

import argparse
import os
import re
import struct
import sys
from pathlib import Path

MACHINE_X64 = 0x8664
MACHINES = {0x14C: "x86", 0x8664: "x86_64", 0xAA64: "arm64", 0x1C4: "arm"}

#: the Visual C++ runtime and its relatives: never taken from System32
VC_RUNTIME = re.compile(r"^(msvcp1\d\d|vcruntime1\d\d|concrt1\d\d|vccorlib1\d\d|vcomp1\d\d|mfc1\d\d|msvcr1\d\d|ucrtbased)", re.I)

#: where the DLLs a PE file imports are found when it runs, by the payload's top folder
#: ("." is the file's own folder; the loader searches the program's folder first). Postgres
#: loads its extensions (lib\*.dll) by path, and their imports resolve from bin\, beside
#: postgres.exe; Python loads extension modules (DLLs\*.pyd) with their own folder and
#: python.exe's. Elsewhere (bin\, ffmpeg\) a program's own folder.
SEARCH = {"postgres": ("postgres/bin",), "python": (".", "python")}
DEFAULT_SEARCH = (".",)

#: PE files under these folders are never loaded on this machine: pip's launcher templates
#: for other architectures (distlib's t32.exe, w64-arm.exe, ...)
NOT_LOADED = ("python/Lib/",)

PE_SUFFIXES = (".exe", ".dll", ".pyd")


class NotPE(ValueError):
    pass


def read_pe(path: Path) -> tuple[int, list[str]]:
    """(machine, imported DLL names) of a PE file: the import table and delay-load table."""
    data = path.read_bytes()
    if len(data) < 0x40 or data[:2] != b"MZ":
        raise NotPE(f"{path}: not a PE file")
    pe = struct.unpack_from("<I", data, 0x3C)[0]
    if data[pe : pe + 4] != b"PE\0\0":
        raise NotPE(f"{path}: not a PE file")
    machine, nsections = struct.unpack_from("<HH", data, pe + 4)
    opt_size = struct.unpack_from("<H", data, pe + 20)[0]
    opt = pe + 24
    magic = struct.unpack_from("<H", data, opt)[0]
    if magic == 0x20B:  # PE32+
        ndirs_at, dirs_at = opt + 108, opt + 112
    elif magic == 0x10B:  # PE32
        ndirs_at, dirs_at = opt + 92, opt + 96
    else:
        raise NotPE(f"{path}: unknown optional header {magic:#x}")
    ndirs = struct.unpack_from("<I", data, ndirs_at)[0]
    sections = []
    for i in range(nsections):
        s = opt + opt_size + 40 * i
        vsize, va, raw_size, raw = struct.unpack_from("<IIII", data, s + 8)
        sections.append((va, max(vsize, raw_size), raw))

    def offset(rva: int) -> int:
        for va, size, raw in sections:
            if va <= rva < va + size:
                return rva - va + raw
        raise NotPE(f"{path}: address {rva:#x} is in no section")

    def cstr(rva: int) -> str:
        o = offset(rva)
        return data[o : data.index(b"\0", o)].decode("ascii", "replace")

    names = []
    # data directory 1: imports (20-byte descriptors, the name at +12); 13: delay-load
    # imports (32-byte descriptors, the name at +4)
    for index, size, name_at in ((1, 20, 12), (13, 32, 4)):
        if index >= ndirs:
            continue
        rva = struct.unpack_from("<I", data, dirs_at + 8 * index)[0]
        if not rva:
            continue
        o = offset(rva)
        while o + size <= len(data):
            entry = data[o : o + size]
            if entry == b"\0" * size:
                break
            name_rva = struct.unpack_from("<I", entry, name_at)[0]
            if not name_rva:
                break
            names.append(cstr(name_rva).lower())
            o += size
    return machine, sorted(set(names))


def system32() -> Path:
    return Path(os.environ.get("SystemRoot", r"C:\Windows")) / "System32"


def is_windows_dll(name: str, sysdir: Path) -> bool:
    name = name.lower()
    if name.startswith(("api-ms-win-", "ext-ms-")):
        return True
    if VC_RUNTIME.match(name):
        return False
    return (sysdir / name).is_file()


def listing(folder: Path) -> dict[str, Path]:
    """lower-case file name -> path, for the files directly in folder"""
    if not folder.is_dir():
        return {}
    return {p.name.lower(): p for p in folder.iterdir() if p.is_file()}


def cmd_imports(args: argparse.Namespace) -> int:
    for f in args.files:
        machine, names = read_pe(Path(f))
        print(f"{Path(f).name} [{MACHINES.get(machine, hex(machine))}]: {' '.join(names)}")
    return 0


def cmd_closure(args: argparse.Namespace) -> int:
    sysdir = system32()
    archive = listing(Path(args.dir))
    # the runtime's DLLs come from one place, so their versions match each other
    runtime = listing(Path(args.runtime)) if args.runtime else archive
    # what --into holds already; a FILE elsewhere (an extension in lib\) is not found by name
    have = set(listing(Path(args.into)))
    queue = [Path(f) for f in args.files]
    needed: dict[str, Path] = {}
    missing: dict[str, str] = {}
    while queue:
        f = queue.pop()
        for name in read_pe(f)[1]:
            if name in have or name in needed or is_windows_dll(name, sysdir):
                continue
            src = runtime if VC_RUNTIME.match(name) else archive
            if name in src:
                needed[name] = src[name]
                queue.append(src[name])
            else:
                missing.setdefault(name, f.name)
    for name, needer in sorted(missing.items()):
        print(f"missing {name} (needed by {needer})", file=sys.stderr)
    for name in sorted(needed):
        print(needed[name])
    return 1 if missing else 0


def cmd_check(args: argparse.Namespace) -> int:
    root = Path(args.dir)
    sysdir = system32()
    folders: dict[Path, dict[str, Path]] = {}
    bad, count = [], 0
    for f in sorted(root.rglob("*")):
        rel = f.relative_to(root).as_posix()
        if not f.is_file() or not f.name.lower().endswith(PE_SUFFIXES) or rel.startswith(NOT_LOADED):
            continue
        count += 1
        try:
            machine, names = read_pe(f)
        except NotPE as e:
            bad.append(f"{rel}: {e}")
            continue
        if machine != MACHINE_X64:
            bad.append(f"{rel}: {MACHINES.get(machine, hex(machine))}, not x86_64")
        search = SEARCH.get(rel.split("/", 1)[0], DEFAULT_SEARCH)
        look = [f.parent if d == "." else root / d for d in search]
        found = set()
        for d in look:
            found |= set(folders.setdefault(d, listing(d)))
        for name in names:
            if name not in found and not is_windows_dll(name, sysdir):
                bad.append(f"{rel}: imports {name}, which is neither in the payload nor in Windows")
    for line in bad:
        print(line)
    print(f"{count} PE files")
    return 1 if bad else 0


def main(argv: list[str]) -> int:
    p = argparse.ArgumentParser(prog="payload_tool.py", description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("imports")
    s.add_argument("files", nargs="+")
    s.set_defaults(fn=cmd_imports)
    s = sub.add_parser("closure")
    s.add_argument("--dir", required=True)
    s.add_argument("--into", required=True)
    s.add_argument("--runtime")
    s.add_argument("files", nargs="+")
    s.set_defaults(fn=cmd_closure)
    s = sub.add_parser("check")
    s.add_argument("dir")
    s.set_defaults(fn=cmd_check)
    args = p.parse_args(argv)
    return args.fn(args)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
