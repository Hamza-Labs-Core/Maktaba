#!/usr/bin/env python3
"""The review home's content set (Epic 58 story 58.04): check the manifest, and fetch it.

The manifest (deploy/review-home/content/manifest.json) lists every file the store reviewers'
home carries: public-domain and openly licensed only (CC BY, never NC or ND), each with its
English and Arabic title, its source URL, licence and attribution, and the source's published
size and SHA-1 (the Internet Archive's own checksums, so a source is verified before anything
reads it). A file the home needs in a shape no source has (4K HEVC HDR10, an MKV with EAC3 and
WebVTT subtitles, a file every TV must have transcoded) is derived from its source with ffmpeg,
by the arguments the manifest gives.

    content.py validate [--manifest FILE]
    content.py fetch --dest /srv/review-media [--manifest FILE] [--only ID ...] [--keep-sources]

`fetch` is idempotent: a source already downloaded with the right checksum and a file already
made are kept. It writes the manifest and LICENCES.md (every item's licence and attribution)
beside the libraries. Standard library only; ffmpeg on PATH for the derived files.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.request
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[1]
CONTENT = ROOT / "deploy" / "review-home" / "content"
MANIFEST = CONTENT / "manifest.json"

# The licences the review home may carry, and the licence URL each must name.
LICENCES = {
    "public-domain": re.compile(r"^https://creativecommons\.org/publicdomain/(mark|zero)/1\.0/$"),
    "CC0-1.0": re.compile(r"^https://creativecommons\.org/publicdomain/zero/1\.0/$"),
    "CC-BY-3.0": re.compile(r"^https://creativecommons\.org/licenses/by/3\.0/$"),
    "CC-BY-4.0": re.compile(r"^https://creativecommons\.org/licenses/by/4\.0/$"),
}
TIERS = {0, 9, 13, 16, 18}
# What the story's test files must cover, between them.
COVERS = {"4k-hevc-hdr10", "1080p-h264", "mkv-eac3-webvtt", "transcode"}
# The extensions the home's scanner takes (pipeline scanner/walker.py DEFAULT_VIDEO_EXTENSIONS).
SCANNED = {".mp4", ".mkv", ".mov", ".webm", ".avi", ".ts", ".m4v"}
SAFE = re.compile(r"^[a-z0-9][a-z0-9._-]*$")
SHA1 = re.compile(r"^[0-9a-f]{40}$")
PLACEHOLDER = re.compile(r"\{([a-z]+)(\d*)\}")
USER_AGENT = "maktaba-review-home (+https://maktaba.dev)"


def load(path: Path = MANIFEST) -> dict[str, Any]:
    with path.open(encoding="utf-8") as f:
        data: dict[str, Any] = json.load(f)
    return data


def has_arabic(s: str) -> bool:
    return any("؀" <= c <= "ۿ" for c in s)


def has_latin(s: str) -> bool:
    return any("a" <= c.lower() <= "z" for c in s)


def validate(m: dict[str, Any], content: Path = CONTENT) -> list[str]:
    """Every problem with the manifest, as one line each (none: it is good)."""
    errs: list[str] = []
    if m.get("schema") != 1:
        errs.append(f"schema {m.get('schema')!r} (want 1)")
    if not str(m.get("media_root", "")).startswith("/"):
        errs.append("media_root must be an absolute path")
    libs: dict[str, dict[str, Any]] = {}
    for lib in m.get("libraries", []):
        lid = lib.get("id", "")
        if not SAFE.match(lid) or lid in libs:
            errs.append(f"library {lid!r}: missing, not a plain name, or named twice")
            continue
        libs[lid] = lib
        if not SAFE.match(lib.get("dir", "")):
            errs.append(f"library {lid}: dir {lib.get('dir')!r} is not one plain folder name")
        if not has_latin(lib.get("name_en", "")) or not has_arabic(lib.get("name_ar", "")):
            errs.append(f"library {lid}: needs an English and an Arabic name")
        if lib.get("age_tier") not in TIERS:
            errs.append(f"library {lid}: age_tier {lib.get('age_tier')!r} (one of {sorted(TIERS)})")
    ch = m.get("channel", {})
    if ch.get("library") not in libs:
        errs.append(f"channel: library {ch.get('library')!r} is not in the set")
    if not has_latin(ch.get("name_en", "")) or not has_arabic(ch.get("name_ar", "")):
        errs.append("channel: needs an English and an Arabic name")
    if not isinstance(ch.get("number"), int) or ch.get("number", 0) <= 0:
        errs.append("channel: needs a positive number")

    ids: set[str] = set()
    files: set[tuple[str, str]] = set()
    covered: set[str] = set()
    used: set[str] = set()
    for it in m.get("items", []):
        iid = it.get("id", "")
        where = f"item {iid or '?'}"
        if not SAFE.match(iid) or iid in ids:
            errs.append(f"{where}: id missing, not a plain name, or named twice")
        ids.add(iid)
        lib = it.get("library")
        if lib not in libs:
            errs.append(f"{where}: library {lib!r} is not in the set")
        else:
            used.add(lib)
        name = it.get("file", "")
        if not SAFE.match(name) or (lib, name) in files:
            errs.append(f"{where}: file {name!r} is not one plain file name, or is named twice")
        elif Path(name).suffix not in SCANNED:
            errs.append(f"{where}: {name} is not a type the home scans ({' '.join(sorted(SCANNED))})")
        files.add((lib, name))
        if not has_latin(it.get("title_en", "")):
            errs.append(f"{where}: needs an English title")
        if not has_arabic(it.get("title_ar", "")):
            errs.append(f"{where}: needs an Arabic title")
        lic = it.get("licence")
        if lic not in LICENCES:
            errs.append(f"{where}: licence {lic!r} is not public domain or CC BY ({', '.join(LICENCES)})")
        elif not LICENCES[lic].match(it.get("licence_url", "")):
            errs.append(f"{where}: licence_url {it.get('licence_url')!r} is not {lic}'s")
        if not it.get("attribution", "").strip():
            errs.append(f"{where}: needs an attribution")
        cov = it.get("covers", [])
        bad = set(cov) - COVERS
        if bad:
            errs.append(f"{where}: covers {sorted(bad)} (known: {sorted(COVERS)})")
        covered |= set(cov)
        sources = it.get("sources", [])
        if not sources:
            errs.append(f"{where}: no source")
        for i, s in enumerate(sources):
            if not str(s.get("url", "")).startswith("https://") or not str(s.get("page", "")).startswith("https://"):
                errs.append(f"{where}: source {i}: url and page must be https")
            if not SHA1.match(str(s.get("sha1", ""))):
                errs.append(f"{where}: source {i}: sha1 must be 40 hex characters")
            if not isinstance(s.get("size"), int) or s.get("size", 0) <= 0:
                errs.append(f"{where}: source {i}: size must be a positive number of bytes")
        subs = it.get("subtitles", [])
        for i, sub in enumerate(subs):
            p = content / str(sub.get("path", ""))
            if not str(sub.get("path", "")).endswith(".vtt") or ".." in str(sub.get("path", "")):
                errs.append(f"{where}: subtitle {i}: a .vtt file under the content folder")
            elif not p.is_file():
                errs.append(f"{where}: subtitle {i}: {sub.get('path')} does not exist")
            else:
                text = p.read_text(encoding="utf-8")
                if not text.startswith("WEBVTT"):
                    errs.append(f"{where}: subtitle {i}: {sub.get('path')} is not WebVTT")
                if sub.get("lang") == "ar" and not has_arabic(text):
                    errs.append(f"{where}: subtitle {i}: the Arabic file has no Arabic")
        derive = it.get("derive")
        if derive is not None:
            errs += _check_derive(where, derive, len(sources), len(subs))
        elif len(sources) != 1 or subs:
            errs.append(f"{where}: more than one input needs a derive step")
        elif Path(name).suffix != Path(str(sources[0].get("url", ""))).suffix:
            errs.append(f"{where}: a file copied as it is keeps its source's type")
    if not m.get("items"):
        errs.append("no items")
    missing = COVERS - covered
    if missing:
        errs.append(f"no item covers {sorted(missing)}")
    for lid in libs:
        if lid not in used:
            errs.append(f"library {lid}: no item")
    return errs


def _check_derive(where: str, derive: Any, n_src: int, n_sub: int) -> list[str]:
    if not isinstance(derive, list) or not all(isinstance(a, str) for a in derive) or not derive:
        return [f"{where}: derive must be a list of ffmpeg arguments"]
    errs = []
    if derive.count("{out}") != 1 or derive[-1] != "{out}":
        errs.append(f"{where}: derive ends with {{out}}, once")
    for a in derive:
        for kind, n in PLACEHOLDER.findall(a):
            if kind == "out" and not n:
                continue
            if kind == "src" and n and int(n) < n_src:
                continue
            if kind == "sub" and n and int(n) < n_sub:
                continue
            errs.append(f"{where}: derive names {{{kind}{n}}}, which it does not have")
    if any(a in ("-y", "-n") for a in derive):
        errs.append(f"{where}: derive leaves -y/-n to the fetcher")
    return errs


def _sha1(path: Path) -> str:
    h = hashlib.sha1()  # noqa: S324 - the Internet Archive publishes SHA-1; an integrity check
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def _download(url: str, to: Path, size: int, sha1: str) -> None:
    if to.is_file() and to.stat().st_size == size and _sha1(to) == sha1:
        print(f"content: have {to.name}")
        return
    to.parent.mkdir(parents=True, exist_ok=True)
    tmp = to.with_name(to.name + ".part")
    print(f"content: fetching {url}")
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=60) as r, tmp.open("wb") as f:  # noqa: S310 - https only (validate)
        shutil.copyfileobj(r, f, 1 << 20)
    got_size, got = tmp.stat().st_size, _sha1(tmp)
    if got_size != size or got != sha1:
        tmp.unlink()
        raise RuntimeError(f"{url}: got {got_size} bytes, sha1 {got}; the manifest says {size}, {sha1}")
    tmp.replace(to)


def derive_args(derive: list[str], sources: list[Path], subs: list[Path], out: Path) -> list[str]:
    """The ffmpeg command line for a derived file, its placeholders filled in."""

    def fill(a: str) -> str:
        def one(mt: re.Match[str]) -> str:
            kind, n = mt.group(1), mt.group(2)
            if kind == "out":
                return str(out)
            return str((sources if kind == "src" else subs)[int(n)])

        return PLACEHOLDER.sub(one, a)

    return ["ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y", *[fill(a) for a in derive]]


def fetch(
    m: dict[str, Any],
    dest: Path,
    content: Path = CONTENT,
    only: set[str] | None = None,
    keep_sources: bool = True,
    run: Any = subprocess.run,
) -> list[Path]:
    """Fetch, verify and make every item (or those in only) under dest; answers the files."""
    libs = {lib["id"]: lib for lib in m["libraries"]}
    made = []
    cache = dest / ".sources"
    for it in m["items"]:
        if only and it["id"] not in only:
            continue
        out = dest / libs[it["library"]]["dir"] / it["file"]
        out.parent.mkdir(parents=True, exist_ok=True)
        srcs = []
        for s in it["sources"]:
            p = cache / it["id"] / Path(urllib.request.url2pathname(s["url"].rsplit("/", 1)[-1])).name
            _download(s["url"], p, s["size"], s["sha1"])
            srcs.append(p)
        if out.is_file() and out.stat().st_size > 0:
            print(f"content: have {out.relative_to(dest)}")
        elif it.get("derive"):
            subs = [content / s["path"] for s in it.get("subtitles", [])]
            # ffmpeg picks the container from the name, so the partial file keeps the suffix
            fd, tmp_name = tempfile.mkstemp(dir=out.parent, prefix=".part-", suffix=out.suffix)
            os.close(fd)
            tmp = Path(tmp_name)
            print(f"content: making {out.relative_to(dest)}")
            try:
                run(derive_args(it["derive"], srcs, subs, tmp), check=True)
                tmp.replace(out)
            finally:
                tmp.unlink(missing_ok=True)
        else:
            shutil.copyfile(srcs[0], out)
        made.append(out)
        if not keep_sources:
            shutil.rmtree(cache / it["id"], ignore_errors=True)
    # the seeding tool reads the libraries, the channel and the titles from this copy
    (dest / "manifest.json").write_text(json.dumps(m, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (dest / "LICENCES.md").write_text(licences_md(m), encoding="utf-8")
    return made


def licences_md(m: dict[str, Any]) -> str:
    """Every item's titles, licence, attribution and source: the content set's credits."""
    lines = [
        "# The review home's content",
        "",
        "Public-domain and openly licensed only. Each item: its titles, licence, attribution and source.",
        "",
    ]
    for it in m["items"]:
        src = ", ".join(s["page"] for s in it["sources"])
        lines.append(
            f"- **{it['title_en']}** · {it['title_ar']}: {it['licence']} ({it['licence_url']}). "
            f"{it['attribution']}. Source: {src}"
        )
    return "\n".join(lines) + "\n"


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    v = sub.add_parser("validate")
    v.add_argument("--manifest", type=Path, default=MANIFEST)
    f = sub.add_parser("fetch")
    f.add_argument("--manifest", type=Path, default=MANIFEST)
    f.add_argument("--dest", type=Path, required=True)
    f.add_argument("--only", nargs="*")
    f.add_argument("--keep-sources", action="store_true", help="keep the downloads (default: delete each once made)")
    a = ap.parse_args(argv)
    m = load(a.manifest)
    errs = validate(m, a.manifest.parent)
    for e in errs:
        print(f"content: {e}", file=sys.stderr)
    if errs:
        return 1
    if a.cmd == "validate":
        print(f"content: {len(m['items'])} items, ok")
        return 0
    made = fetch(m, a.dest, a.manifest.parent, set(a.only) if a.only else None, a.keep_sources)
    print(f"content: {len(made)} files under {a.dest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
