#!/usr/bin/env python3
"""The Samsung TV package of the launcher (Epic 58 story 58.10).

    package_wgt.py --dist web/dist-launcher --version 0.3.141 --out build/tizen

Builds two things from the launcher's build (`pnpm build:launcher`, web/dist-launcher):

- OUT/stage/: the package's files, ready for the Tizen CLI to sign (`tizen package -t wgt
  -s <profile> -- OUT/stage`, tv-web-release.yml, with the Samsung author and distributor
  certificates from the release secrets: never in the repository);
- OUT/maktaba-tizen-<version>-unsigned.wgt: the same files zipped, unsigned. CI builds and
  lints this one on every pull request; a TV in Developer Mode installs it only once signed.

The stage holds config.xml (this folder's, its version filled), icon.png and the build.
index.html gains Samsung's webapis.js (`$WEBAPIS/webapis/webapis.js`, which gives the page
`webapis`: AVPlay, productinfo, network) ahead of the launcher's own script; LG's package
has no such script, so the shared index.html does not carry it. Source maps stay out.

A Tizen version is three numbers, 0-255.0-255.0-65535 (a tag's vX.Y.Z without the v).
Standard library only.
"""

from __future__ import annotations

import argparse
import re
import shutil
import sys
import zipfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
WEBAPIS = '<script src="$WEBAPIS/webapis/webapis.js"></script>'
VERSION_RE = re.compile(r"^v?(\d+)\.(\d+)\.(\d+)$")


def tizen_version(raw: str) -> str:
    """A tag or version as Tizen takes it ("v0.3.141" -> "0.3.141"); ValueError otherwise."""
    m = VERSION_RE.match(raw.strip())
    if not m:
        raise ValueError(f"not a version: {raw!r} (want X.Y.Z)")
    major, minor, patch = (int(g) for g in m.groups())
    if major > 255 or minor > 255 or patch > 65535:
        raise ValueError(f"out of Tizen's range (255.255.65535): {raw!r}")
    return f"{major}.{minor}.{patch}"


def with_webapis(html: str) -> str:
    """index.html with webapis.js loaded before the first script."""
    if WEBAPIS in html:
        return html
    i = html.find("<script")
    if i < 0:
        i = html.find("</head>")
    if i < 0:
        raise ValueError("index.html has neither a <script> nor </head>")
    return html[:i] + WEBAPIS + "\n    " + html[i:]


def stage(dist: Path, version: str, out: Path) -> Path:
    """Writes OUT/stage (see the module doc) and returns it."""
    if not (dist / "index.html").is_file():
        raise FileNotFoundError(f"{dist}/index.html: build the launcher first (pnpm build:launcher)")
    st = out / "stage"
    if st.exists():
        shutil.rmtree(st)
    st.mkdir(parents=True)
    for src in sorted(dist.rglob("*")):
        if src.is_dir() or src.suffix == ".map":
            continue
        rel = src.relative_to(dist)
        dst = st / rel
        dst.parent.mkdir(parents=True, exist_ok=True)
        if rel.as_posix() == "index.html":
            dst.write_text(with_webapis(src.read_text(encoding="utf-8")), encoding="utf-8")
        else:
            shutil.copy2(src, dst)
    config = (HERE / "config.xml").read_text(encoding="utf-8")
    if "@VERSION@" not in config:
        raise ValueError("config.xml has no @VERSION@ to fill")
    (st / "config.xml").write_text(config.replace("@VERSION@", version), encoding="utf-8")
    shutil.copy2(HERE / "icon.png", st / "icon.png")
    return st


def zip_stage(st: Path, wgt: Path) -> Path:
    """The stage zipped as an unsigned .wgt (config.xml first, fixed times: reproducible)."""
    files = sorted(p for p in st.rglob("*") if p.is_file())
    files.sort(key=lambda p: (p.name != "config.xml", p.relative_to(st).as_posix()))
    wgt.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(wgt, "w", zipfile.ZIP_DEFLATED) as z:
        for p in files:
            info = zipfile.ZipInfo(p.relative_to(st).as_posix(), date_time=(2020, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.external_attr = 0o644 << 16
            z.writestr(info, p.read_bytes())
    return wgt


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dist", type=Path, required=True, help="the launcher's build (web/dist-launcher)")
    ap.add_argument("--version", required=True, help="X.Y.Z or a vX.Y.Z tag")
    ap.add_argument("--out", type=Path, required=True)
    a = ap.parse_args(argv)
    try:
        version = tizen_version(a.version)
        st = stage(a.dist, version, a.out)
        wgt = zip_stage(st, a.out / f"maktaba-tizen-{version}-unsigned.wgt")
    except (ValueError, FileNotFoundError) as e:
        print(f"package_wgt: {e}", file=sys.stderr)
        return 1
    print(f"package_wgt: {st} and {wgt}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
