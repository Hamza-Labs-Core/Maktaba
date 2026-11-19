"""Build Maktaba's first-party plugin packages (Epic 54, story 54.27 §2).

``build.py --version 0.3.152 --out DIR [plugin ...]`` writes, for every folder of
``plugins/first-party/`` (or the ones named), ``tv.maktaba.<name>-<version>.mkplugin`` and
the package's ``files.json`` beside it as ``tv.maktaba.<name>-<version>.files.json``. The
release lists both in its signed ``checksums.txt``; a listed ``files.json`` digest is what
``firstparty.CheckPackage`` (story 54.22 §2) trusts.

A package holds:

    manifest.toml        the plugin's manifest, ``version`` and ``maktaba`` stamped from the
                         release (``version = "<v>"``, ``maktaba = "=<v>"``, README §15.6)
    i18n/en.json, ar.json its strings
    statements.json      its named statements, compiled as the server's bundle compiles them
                         (``tools/plugins/bundle.py``; story 54.24)
    wheels/<wheel>       the plugin's code (``src/``) as a wheel of its own
    files.json           every file's sha256 (written by the packer)

The packer is the home's own writer (``shared/go/componentpack``, ``cmd/mkplugin``), so a
package is read back by the same code that installs one. Every output is reproducible: the
same commit and version give the same bytes, so a re-run of the release writes the same
signed checksums.

A first-party plugin with no stage is refused (it would be an empty component, 54.27 edge
cases). There is no ``_lib`` folder yet: no plugin shares code with another.
"""

from __future__ import annotations

import argparse
import base64
import csv
import hashlib
import io
import os
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
FIRST_PARTY = ROOT / "plugins" / "first-party"
COMPONENTPACK = ROOT / "shared" / "go" / "componentpack"
sys.path.insert(0, str(Path(__file__).resolve().parent))

import bundle  # noqa: E402

# semver 2.0 without build metadata (54.01 §2), as a release tag gives it
SEMVER = re.compile(
    r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$"
)
PRE = re.compile(r"^(alpha|beta|rc|a|b)\.?(\d+)$")
# 1980-01-01, the earliest time a zip holds: every wheel entry has it
ZIP_TIME = (1980, 1, 1, 0, 0, 0)


class BuildError(Exception):
    """A plugin that cannot be packaged, with the reason."""


def pep440(version: str) -> str:
    """The wheel's version for a release version (1.2.3-rc.1 -> 1.2.3rc1), as hatchling and
    the pipeline wheel normalise it (deploy/packaging/node/build-release.sh)."""
    m = SEMVER.match(version)
    if not m:
        raise BuildError(f"version {version!r} is not semver without build metadata")
    base = ".".join(m.group(i) for i in (1, 2, 3))
    pre = m.group(4)
    if pre is None:
        return base
    p = PRE.match(pre)
    if not p:
        raise BuildError(f"version {version!r}: pre-release {pre!r} has no PEP 440 form")
    tag = {"alpha": "a", "beta": "b"}.get(p.group(1), p.group(1))
    return f"{base}{tag}{int(p.group(2))}"


def stamp(text: str, version: str) -> str:
    """The manifest with its top-level ``version`` and ``maktaba`` set from the release."""
    out = text
    for key, value in (("version", version), ("maktaba", f"={version}")):
        out, n = re.subn(
            rf'^{key}(\s*)=(\s*)"[^"\n]*"', rf'{key}\g<1>=\g<2>"{value}"', out, count=1, flags=re.M
        )
        if n != 1:
            raise BuildError(f"the manifest has no top-level {key} line to stamp")
    m = tomllib.loads(out)
    if m.get("version") != version or m.get("maktaba") != f"={version}":
        raise BuildError("the stamped manifest does not read back with the release's version")
    return out


def _record_hash(b: bytes) -> str:
    return "sha256=" + base64.urlsafe_b64encode(hashlib.sha256(b).digest()).rstrip(b"=").decode()


def wheel(folder: Path, plugin_id: str, version: str) -> tuple[str, bytes]:
    """The plugin's ``src/`` as a reproducible pure-Python wheel: its name and bytes."""
    src = folder / "src"
    files: dict[str, bytes] = {}
    if src.is_dir():
        for p in sorted(src.rglob("*")):
            rel = p.relative_to(src)
            if (
                p.is_dir()
                or "__pycache__" in rel.parts
                or p.suffix in (".pyc", ".pyo")
                or p.name == ".DS_Store"
            ):
                continue
            files[rel.as_posix()] = p.read_bytes()
    if not any(name.endswith("/__init__.py") for name in files):
        raise BuildError(f"{folder.name}: src/ holds no Python package")
    dist = "maktaba_fp_" + folder.name.replace("-", "_")
    pep = pep440(version)
    info = f"{dist}-{pep}.dist-info"
    licence = tomllib.loads((folder / "manifest.toml").read_text(encoding="utf-8")).get(
        "licence", ""
    )
    files[f"{info}/METADATA"] = (
        "Metadata-Version: 2.1\n"
        f"Name: {dist.replace('_', '-')}\n"
        f"Version: {pep}\n"
        f"Summary: Maktaba's first-party plugin {plugin_id}\n"
        f"License: {licence}\n"
        "Requires-Python: >=3.12\n"
        f"Requires-Dist: maktaba-pipeline=={pep}\n"
    ).encode()
    files[f"{info}/WHEEL"] = (
        b"Wheel-Version: 1.0\n"
        b"Generator: maktaba tools/plugins/build.py\n"
        b"Root-Is-Purelib: true\n"
        b"Tag: py3-none-any\n"
    )
    # RECORD is CSV (PEP 376): a name with a comma or a quote is quoted
    rec = io.StringIO()
    w = csv.writer(rec, lineterminator="\n")
    for n, b in sorted(files.items()):
        w.writerow([n, _record_hash(b), len(b)])
    w.writerow([f"{info}/RECORD", "", ""])
    files[f"{info}/RECORD"] = rec.getvalue().encode()
    # dist-info last, as wheel tools write it
    order = sorted(n for n in files if not n.startswith(info)) + [
        f"{info}/METADATA",
        f"{info}/WHEEL",
        f"{info}/RECORD",
    ]
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as z:
        for name in order:
            zi = zipfile.ZipInfo(name, date_time=ZIP_TIME)
            zi.compress_type = zipfile.ZIP_DEFLATED
            zi.external_attr = 0o644 << 16
            z.writestr(zi, files[name], compresslevel=9)
    return f"{dist}-{pep}-py3-none-any.whl", buf.getvalue()


def contents(folder: Path, version: str) -> tuple[str, dict[str, bytes]]:
    """A plugin's id and the files of its package (files.json aside)."""
    text = (folder / "manifest.toml").read_text(encoding="utf-8")
    m = tomllib.loads(text)
    plugin_id = m.get("id", "")
    if not plugin_id.startswith("tv.maktaba."):
        raise BuildError(f"{folder.name}: {plugin_id!r} is not a Maktaba id")
    if not m.get("stage"):
        raise BuildError(f"{folder.name}: a first-party plugin with no stage is an empty component")
    compiled = bundle.plugin(folder)  # the same strings and statements the server embeds
    name, whl = wheel(folder, plugin_id, version)
    files = {
        "manifest.toml": stamp(text, version).encode(),
        "statements.json": (bundle.canonical(compiled["statements"]) + "\n").encode(),
        f"wheels/{name}": whl,
    }
    for lang in ("en", "ar"):
        files[f"i18n/{lang}.json"] = (folder / "i18n" / f"{lang}.json").read_bytes()
    return plugin_id, files


def mkplugin_binary(work: Path) -> Path:
    """Build the home's packer once."""
    exe = work / ("mkplugin.exe" if os.name == "nt" else "mkplugin")
    subprocess.run(  # noqa: S603
        ["go", "build", "-o", str(exe), "./cmd/mkplugin"],  # noqa: S607
        cwd=COMPONENTPACK,
        check=True,
    )
    return exe


# A pre-release (1.2.3-rc.1) builds no package: a manifest's maktaba range takes only
# MAJOR.MINOR.PATCH (54.01 §2), so "=<pre-release>" would fail the trusted manifest check and
# the release with it. Nothing installs a first-party package yet (stories 54.45, 54.46), so a
# pre-release ships without them; the release check (fp-packages) requires that none is there.
# The note is the job's one output, so its artifact is never empty.
PRERELEASE_NOTE = "first-party-packages.txt"


def is_prerelease(version: str) -> bool:
    m = SEMVER.match(version)
    return bool(m and m.group(4))


def build(folders: list[Path], version: str, out: Path, mkplugin: Path | None = None) -> list[Path]:
    """Every package, written to out; answers the .mkplugin paths (none for a pre-release)."""
    pep440(version)  # a bad version fails before anything is written
    out.mkdir(parents=True, exist_ok=True)
    if is_prerelease(version):
        (out / PRERELEASE_NOTE).write_text(
            f"{version} is a pre-release: it carries no first-party plugin packages.\n",
            encoding="utf-8",
        )
        return []
    built: list[Path] = []
    with tempfile.TemporaryDirectory(prefix="fp-build-") as tmp:
        work = Path(tmp)
        exe = mkplugin or mkplugin_binary(work)
        for folder in folders:
            plugin_id, files = contents(folder, version)
            stage = work / folder.name
            for rel, b in files.items():
                p = stage / rel
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_bytes(b)
            pkg = out / f"{plugin_id}-{version}.mkplugin"
            fj = out / f"{plugin_id}-{version}.files.json"
            subprocess.run(  # noqa: S603
                [str(exe), "-files-json", str(fj), str(stage), str(pkg)],
                check=True,
                stdout=subprocess.DEVNULL,
            )
            shutil.rmtree(stage)
            built.append(pkg)
    return built


def folders_of(names: list[str]) -> list[Path]:
    every = sorted(p for p in FIRST_PARTY.iterdir() if (p / "manifest.toml").is_file())
    if not names:
        return every
    by = {p.name: p for p in every}
    missing = [n for n in names if n not in by]
    if missing:
        raise BuildError(f"no first-party plugin {', '.join(missing)}")
    return [by[n] for n in names]


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument(
        "plugins", nargs="*", help="folder names under plugins/first-party (all when none)"
    )
    ap.add_argument("--version", required=True, help="the release version (a tag without its v)")
    ap.add_argument("--out", required=True, type=Path)
    ap.add_argument(
        "--mkplugin",
        type=Path,
        help="a built shared/go/componentpack/cmd/mkplugin (default: go build it here)",
    )
    args = ap.parse_args(argv)
    try:
        built = build(
            folders_of(args.plugins), args.version.removeprefix("v"), args.out, args.mkplugin
        )
    except BuildError as e:
        print(f"build.py: {e}", file=sys.stderr)  # noqa: T201
        return 1
    for p in built:
        print(p.name)  # noqa: T201
    return 0


if __name__ == "__main__":
    sys.exit(main())
