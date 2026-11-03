"""Download a wheelhouse for one platform (story 51.15), standard library and pip only.

    wheelhouse.py --dest DIR --platform linux-amd64 --python 3.12 --version 0.4.2 CONSTRAINTS...

For each constraints file: the pins whose markers hold on the platform (with their hashes)
are downloaded with ``pip download --only-binary=:all: --require-hashes --no-deps`` for
that platform's wheel tags. A pin with no wheel there is left out and listed as missing
(a machine installs it from PyPI), as the home's mirror does
(api/internal/updates/wheelhouse.go). index.json is written last, sorted, with built_at
the commit's time (SOURCE_DATE_EPOCH): the same pins give the same files.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import subprocess
import sys
from pathlib import Path

try:
    from packaging.requirements import Requirement
except ImportError:  # pip's own copy
    from pip._vendor.packaging.requirements import Requirement


def _manylinux(arch: str) -> list[str]:
    tags = [
        f"manylinux_{g}_{arch}"
        for g in ("2_17", "2_24", "2_26", "2_27", "2_28", "2_31", "2_34", "2_35")
    ]
    return tags + [f"manylinux2014_{arch}"]


def _macos(arch: str, start: int) -> list[str]:
    return [
        t
        for major in range(start, 16)
        for t in (f"macosx_{major}_0_{arch}", f"macosx_{major}_0_universal2")
    ]


#: the catalog's platforms as pip downloads for them: wheel tags and marker values
TARGETS: dict[str, tuple[str, list[str], dict[str, str]]] = {
    "linux-amd64": (
        "linux/amd64",
        _manylinux("x86_64"),
        {
            "sys_platform": "linux",
            "platform_system": "Linux",
            "platform_machine": "x86_64",
            "os_name": "posix",
        },
    ),
    "linux-arm64": (
        "linux/arm64",
        _manylinux("aarch64"),
        {
            "sys_platform": "linux",
            "platform_system": "Linux",
            "platform_machine": "aarch64",
            "os_name": "posix",
        },
    ),
    "darwin-arm64": (
        "darwin/arm64",
        _macos("arm64", 11),
        {
            "sys_platform": "darwin",
            "platform_system": "Darwin",
            "platform_machine": "arm64",
            "os_name": "posix",
        },
    ),
    "windows-amd64": (
        "windows/amd64",
        ["win_amd64"],
        {
            "sys_platform": "win32",
            "platform_system": "Windows",
            "platform_machine": "AMD64",
            "os_name": "nt",
        },
    ),
}

_MISSING = re.compile(
    r"(?:no matching distribution found for"
    r"|could not find a version that satisfies the requirement)"
    r"\s+([A-Za-z0-9][A-Za-z0-9._-]*)",
    re.I,
)


def _norm(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


def pins_for(constraints: Path, env: dict[str, str]) -> tuple[list[str], list[str]]:
    """The pins (``name==ver --hash=...``) whose markers hold in env, and the index lines."""
    lines: list[str] = []
    cur = ""
    for raw in constraints.read_text(encoding="utf-8").splitlines():
        line = "" if raw.lstrip().startswith("#") else raw.split(" #", 1)[0].rstrip()
        if line.endswith("\\"):
            cur += line[:-1] + " "
            continue
        cur += line
        if cur.strip():
            lines.append(cur.strip())
        cur = ""
    if cur.strip():
        lines.append(cur.strip())
    pins, options = [], []
    for line in lines:
        if line.startswith("-"):
            if line.startswith(("--index-url", "--extra-index-url")):
                options.append(line)
            continue
        toks = line.split()
        hashes = [t for t in toks if t.startswith("--hash=")]
        req = Requirement(" ".join(t for t in toks if not t.startswith("--hash=")))
        if req.marker is not None and not req.marker.evaluate(env):
            continue
        pins.append(" ".join([req.name + str(req.specifier), *hashes]))
    return pins, options


def download(
    pins: list[str], options: list[str], dest: Path, tags: list[str], py: str, work: Path
) -> list[str]:
    missing: list[str] = []
    for _ in range(60):
        req = work / "pins.txt"
        req.write_text("\n".join(options + pins) + "\n", encoding="utf-8")
        cmd = [
            sys.executable,
            "-m",
            "pip",
            "download",
            "--disable-pip-version-check",
            "--no-input",
            "--no-deps",
            "--only-binary=:all:",
            "--require-hashes",
            "--dest",
            str(dest),
            "--python-version",
            py,
            "--implementation",
            "cp",
            "--abi",
            "cp" + py.replace(".", ""),
            "--abi",
            "abi3",
            "--abi",
            "none",
        ]
        for t in tags:
            cmd += ["--platform", t]
        cmd += ["-r", str(req)]
        res = subprocess.run(cmd, capture_output=True, text=True, check=False)
        if res.returncode == 0:
            return missing
        m = _MISSING.search(res.stdout + res.stderr)
        name = _norm(m.group(1)) if m else ""
        drop = [p for p in pins if _norm(re.split(r"[=<>!~ ]", p, maxsplit=1)[0]) == name]
        if not drop:
            sys.stderr.write(res.stdout[-2000:] + res.stderr[-4000:])
            raise SystemExit(f"wheelhouse.py: pip download failed ({res.returncode})")
        pins.remove(drop[0])
        missing.append(drop[0].split()[0])
        print(f"no wheel of {drop[0].split()[0]} for this platform: left out", file=sys.stderr)
    raise SystemExit("wheelhouse.py: too many pins without a wheel")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--dest", required=True, type=Path)
    ap.add_argument("--platform", required=True, choices=sorted(TARGETS))
    ap.add_argument("--python", required=True)
    ap.add_argument("--version", required=True)
    ap.add_argument("constraints", nargs="+", type=Path)
    a = ap.parse_args()
    os_arch, tags, marker = TARGETS[a.platform]
    py = ".".join(a.python.split(".")[:2])
    env = {
        "python_version": py,
        "python_full_version": py + ".0",
        "implementation_name": "cpython",
        "platform_python_implementation": "CPython",
        "implementation_version": py + ".0",
        **marker,
    }
    a.dest.mkdir(parents=True, exist_ok=True)
    work = a.dest.parent / ".wheelhouse-work"
    work.mkdir(exist_ok=True)
    missing: list[str] = []
    for c in a.constraints:
        pins, options = pins_for(c, env)
        missing += download(pins, options, a.dest, tags, py, work)
    for p in work.iterdir():
        p.unlink()
    work.rmdir()
    files = []
    for w in sorted(a.dest.glob("*.whl")):
        h = hashlib.sha256()
        with open(w, "rb") as f:
            for chunk in iter(lambda: f.read(1 << 20), b""):
                h.update(chunk)
        files.append({"name": w.name, "sha256": h.hexdigest(), "size": w.stat().st_size})
    built = dt.datetime.fromtimestamp(int(os.environ["SOURCE_DATE_EPOCH"]), dt.UTC)
    index = {
        "version": a.version,
        "platform": os_arch,
        "python": py,
        "files": files,
        "built_at": built.strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    if missing:
        index["missing"] = sorted(set(missing))
    (a.dest / "index.json").write_text(json.dumps(index, indent=2) + "\n", encoding="utf-8")
    print(
        f"{len(files)} wheels for {a.platform}, {len(set(missing))} pins without one",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
