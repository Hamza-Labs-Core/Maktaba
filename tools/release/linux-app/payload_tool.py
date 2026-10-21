"""Helpers for the Linux server payload (Epic 39, story 39.2), standard library only.

payload.sh and check.sh run it with the payload's own Python (``python/bin/python3 -I``), so
it works inside a minimal container that has no Python of its own.

  manifest DIR              write DIR/files.sha256 (every file and symlink but itself,
                            payload.json and a runtime copy's .installed) and print
                            {"count", "bytes", "sha256"} as JSON
  verify DIR                DIR matches its files.sha256: nothing missing, changed or added
  elf DIR [--max-glibc V]   every ELF file: the newest GLIBC_ symbol version it needs (none
                            above V), and each library it needs found in the payload (through
                            its RUNPATH / RPATH, $ORIGIN expanded) or in the baseline every
                            supported system has (glibc, libgcc_s, libstdc++). Prints
                            {"min_glibc", "elf_files"} as JSON
  closure DIR LIB...        print the libraries (one "soname path" per line, resolved on this
                            machine by the dynamic loader's cache) that LIB... need and that
                            neither the baseline nor DIR provides

files.sha256 (the runtime copy of story 39.3 reads the same format): one entry per line,
tab-separated, sorted by path (bytes):

  f <mode, 4 octal digits> <size> <sha256> <path>     a regular file
  l <target> <path>                                   a symlink (target as stored, relative)

Paths are relative, with "/", and hold no tab, newline or carriage return. Directories are
implied by the paths.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import stat
import struct
import subprocess
import sys
from pathlib import Path

MANIFEST = "files.sha256"
PAYLOAD_JSON = "payload.json"
#: what the runtime copy (story 39.3) adds to an installed payload
INSTALLED = ".installed"

#: what every supported Linux has: glibc's own libraries and GCC's runtime (the ffmpeg
#: builds need libstdc++ of GCC 12, so it is part of the baseline, not of the payload)
BASELINE = frozenset(
    {
        "ld-linux-x86-64.so.2",
        "ld-linux-aarch64.so.1",
        "linux-vdso.so.1",
        "linux-vdso.so.2",
        "libc.so.6",
        "libm.so.6",
        "libmvec.so.1",
        "libdl.so.2",
        "libpthread.so.0",
        "librt.so.1",
        "libresolv.so.2",
        "libutil.so.1",
        "libanl.so.1",
        "libcrypt.so.1",
        "libgcc_s.so.1",
        "libstdc++.so.6",
    }
)


def _die(msg: str) -> None:
    print(f"payload_tool: {msg}", file=sys.stderr)
    sys.exit(1)


# --------------------------------------------------------------------------- manifest


def _entries(root: Path) -> list[str]:
    out: list[str] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        base = Path(dirpath)
        names = sorted(filenames) + sorted(d for d in dirnames if (base / d).is_symlink())
        for name in names:
            p = base / name
            rel = p.relative_to(root).as_posix()
            if rel in (MANIFEST, PAYLOAD_JSON, INSTALLED):
                continue
            if any(c in rel for c in "\t\n\r"):
                _die(f"{rel!r}: a path with a tab or newline cannot be listed")
            st = p.lstat()
            if stat.S_ISLNK(st.st_mode):
                # tauri's bundlers copy resources with fs::copy: a link must end at
                # a file of the payload (the runtime copy makes the link again)
                target = os.readlink(p)
                end = p.resolve()
                if os.path.isabs(target) or not _inside(root, end) or not end.is_file():
                    _die(f"{rel} -> {target}: a link must end at a file of the payload")
                out.append(f"l\t{target}\t{rel}")
            elif stat.S_ISREG(st.st_mode):
                out.append(
                    f"f\t{stat.S_IMODE(st.st_mode):04o}\t{st.st_size}\t{_sha256(p)}\t{rel}"
                )
            else:
                _die(f"{rel}: not a file, symlink or directory")
    out.sort(key=lambda line: line.rsplit("\t", 1)[1].encode())
    return out


def _sha256(p: Path) -> str:
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def cmd_manifest(root: Path) -> int:
    lines = _entries(root)
    text = "".join(line + "\n" for line in lines)
    (root / MANIFEST).write_text(text, encoding="utf-8")
    size = sum(int(line.split("\t")[2]) for line in lines if line.startswith("f\t"))
    print(
        json.dumps(
            {"count": len(lines), "bytes": size, "sha256": hashlib.sha256(text.encode()).hexdigest()}
        )
    )
    return 0


def cmd_verify(root: Path) -> int:
    try:
        want = (root / MANIFEST).read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        _die(f"{MANIFEST}: {exc}")
    got = _entries(root)
    if got == want:
        print(f"{len(got)} entries match {MANIFEST}")
        return 0
    path = lambda line: line.rsplit("\t", 1)[1]  # noqa: E731
    w, g = {path(x): x for x in want}, {path(x): x for x in got}
    for p in sorted(w.keys() - g.keys())[:20]:
        print(f"missing: {p}")
    for p in sorted(g.keys() - w.keys())[:20]:
        print(f"added:   {p}")
    for p in sorted(p for p in w.keys() & g.keys() if w[p] != g[p])[:20]:
        print(f"changed: {p}")
    return 1


# --------------------------------------------------------------------------- ELF

SHT_DYNAMIC = 6
SHT_GNU_VERNEED = 0x6FFFFFFE
DT_NEEDED, DT_RPATH, DT_RUNPATH = 1, 15, 29


class Elf:
    """The few parts of a 64-bit little-endian ELF file this needs: its NEEDED libraries,
    RUNPATH / RPATH and the symbol versions it requires (.gnu.version_r)."""

    def __init__(self, data: bytes) -> None:
        if data[:4] != b"\x7fELF" or data[4] != 2 or data[5] != 1:
            raise ValueError("not a 64-bit little-endian ELF file")
        self.data = data
        (shoff,) = struct.unpack_from("<Q", data, 0x28)
        shentsize, shnum = struct.unpack_from("<HH", data, 0x3A)
        self.sections = []
        for i in range(shnum):
            off = shoff + i * shentsize
            _name, typ, _flags, _addr, offset, size, link, _info, _align, entsize = struct.unpack_from(
                "<IIQQQQIIQQ", data, off
            )
            self.sections.append((typ, offset, size, link, entsize))

    def _str(self, section: int, off: int) -> str:
        _typ, base, size, _link, _es = self.sections[section]
        end = self.data.index(b"\0", base + off, base + size)
        return self.data[base + off : end].decode("utf-8", "replace")

    def dynamic(self) -> tuple[list[str], list[str]]:
        """(NEEDED sonames, RUNPATH or else RPATH entries)."""
        needed: list[str] = []
        runpath: list[str] = []
        rpath: list[str] = []
        for typ, offset, size, link, _es in self.sections:
            if typ != SHT_DYNAMIC:
                continue
            for off in range(offset, offset + size, 16):
                tag, val = struct.unpack_from("<qQ", self.data, off)
                if tag == 0:
                    break
                if tag == DT_NEEDED:
                    needed.append(self._str(link, val))
                elif tag == DT_RUNPATH:
                    runpath += self._str(link, val).split(":")
                elif tag == DT_RPATH:
                    rpath += self._str(link, val).split(":")
        return needed, [p for p in (runpath or rpath) if p]

    def versions(self) -> set[str]:
        """Every version name the file requires (GLIBC_2.34, GLIBCXX_3.4.30, …)."""
        out: set[str] = set()
        for typ, offset, size, link, _es in self.sections:
            if typ != SHT_GNU_VERNEED:
                continue
            off = offset
            while True:
                _ver, cnt, _file, aux, nxt = struct.unpack_from("<HHIII", self.data, off)
                a = off + aux
                for _ in range(cnt):
                    _hash, _flags, _other, name, anext = struct.unpack_from("<IHHII", self.data, a)
                    out.add(self._str(link, name))
                    if not anext:
                        break
                    a += anext
                if not nxt:
                    break
                off += nxt
        return out


def _is_elf(p: Path) -> bool:
    try:
        with open(p, "rb") as f:
            return f.read(4) == b"\x7fELF"
    except OSError:
        return False


def _ver(v: str) -> tuple[int, ...]:
    return tuple(int(x) for x in v.split(".") if x.isdigit())


def _elf_files(root: Path):
    for dirpath, _dirs, files in os.walk(root):
        for name in sorted(files):
            p = Path(dirpath) / name
            if not p.is_symlink() and _is_elf(p):
                yield p


def cmd_elf(root: Path, max_glibc: str | None) -> int:
    problems: list[str] = []
    newest = (0,)
    count = 0
    for p in _elf_files(root):
        count += 1
        rel = p.relative_to(root).as_posix()
        try:
            elf = Elf(p.read_bytes())
        except (ValueError, struct.error) as exc:
            problems.append(f"{rel}: {exc}")
            continue
        for v in elf.versions():
            if v.startswith("GLIBC_") and v[6:7].isdigit():
                got = _ver(v[6:])
                newest = max(newest, got)
                if max_glibc and got > _ver(max_glibc):
                    problems.append(f"{rel}: needs {v}, the payload's limit is GLIBC_{max_glibc}")
        needed, paths = elf.dynamic()
        for lib in needed:
            if lib not in BASELINE and _resolve(root, p, lib, paths) is None:
                problems.append(f"{rel}: needs {lib}, not found in the payload through {paths or 'no RUNPATH'}")
    for line in problems:
        print(line)
    print(json.dumps({"min_glibc": ".".join(map(str, newest)), "elf_files": count}))
    return 1 if problems else 0


def _origin(path: str, elf: Path) -> str:
    return path.replace("${ORIGIN}", str(elf.parent)).replace("$ORIGIN", str(elf.parent))


def _resolve(root: Path, elf: Path, lib: str, paths: list[str]) -> Path | None:
    """Where the dynamic loader finds ``lib`` for ``elf`` inside ``root``: a NEEDED
    with a "/" is a path itself ($ORIGIN expanded; python-build-standalone's
    libpython3.so names libpython3.12 that way), else it is looked for in each
    RUNPATH / RPATH directory."""
    if "/" in lib:
        candidates = [Path(_origin(lib, elf))]
    else:
        candidates = [Path(_origin(d, elf)) / lib for d in paths]
    for c in candidates:
        if c.exists() and _inside(root, c):
            return c
    return None


def _inside(root: Path, p: Path) -> bool:
    try:
        p.resolve().relative_to(root.resolve())
        return True
    except ValueError:
        return False


def cmd_closure(root: Path, libs: list[Path]) -> int:
    """The libraries libs need (transitively) that are neither baseline nor in root."""
    cache = _ldconfig_cache()
    have = {p.name for p in root.rglob("*")}
    seen: dict[str, Path] = {}
    queue = list(libs)
    while queue:
        p = queue.pop()
        try:
            needed, _paths = Elf(p.read_bytes()).dynamic()
        except (ValueError, struct.error, OSError):
            continue
        for lib in needed:
            if lib in BASELINE or lib in have or lib in seen or "/" in lib:
                continue
            found = cache.get(lib)
            if found is None:
                _die(f"{p.name} needs {lib}, which this machine does not have (install it to build the payload)")
            seen[lib] = found
            queue.append(found)
    for lib in sorted(seen):
        print(f"{lib} {seen[lib]}")
    return 0


def _ldconfig_cache() -> dict[str, Path]:
    """soname → path for this machine's architecture, from ``ldconfig -p``."""
    machine = os.uname().machine
    want = {"x86_64": "x86-64", "aarch64": "AArch64"}.get(machine, machine)
    out = subprocess.run(
        [p for p in ("/sbin/ldconfig", "/usr/sbin/ldconfig") if os.path.exists(p)][:1] + ["-p"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    cache: dict[str, Path] = {}
    for line in out.splitlines()[1:]:
        name, _, rest = line.strip().partition(" (")
        flags, _, path = rest.partition(") => ")
        if want in flags and name not in cache:
            cache[name] = Path(path)
    return cache


def main() -> int:
    ap = argparse.ArgumentParser(prog="payload_tool.py", description=__doc__.split("\n\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("manifest", "verify"):
        sub.add_parser(name).add_argument("dir", type=Path)
    e = sub.add_parser("elf")
    e.add_argument("dir", type=Path)
    e.add_argument("--max-glibc")
    c = sub.add_parser("closure")
    c.add_argument("dir", type=Path)
    c.add_argument("libs", type=Path, nargs="+")
    a = ap.parse_args()
    if a.cmd == "manifest":
        return cmd_manifest(a.dir)
    if a.cmd == "verify":
        return cmd_verify(a.dir)
    if a.cmd == "elf":
        return cmd_elf(a.dir, a.max_glibc)
    return cmd_closure(a.dir, a.libs)


if __name__ == "__main__":
    sys.exit(main())
