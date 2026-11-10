#!/usr/bin/env python3
"""The TrueNAS app (Epic 59 story 59.10): its release copy, and the checks that keep it whole.

The app's source is deploy/nas/truenas/ix-dev/community/maktaba, in `truenas/apps`' layout;
deploy/nas/truenas/custom-app.yaml is the same install as a TrueNAS Custom App (Apps → Discover
→ Custom App → Install via YAML), for TrueNAS boxes while the app is not in the catalog.

The source pins the release it was last changed for (CI's catalog checks need a real tag). A
release does not commit a new pin back to main: such a commit would be the next release
candidate, and every release would make another. Each release instead renders its own copy,
as the published Docker compose file is rendered (release.yml: `maktaba-server:latest` → the
tag), and that copy is what goes out:

    truenas_app.py render --tag vX.Y.Z [--digest sha256:...] --out DIR
        DIR/ix-dev/community/maktaba   the app pinned to the release's image (tag@digest when
                                       given), app_version X.Y.Z, version (X+1).Y.Z
        DIR/custom-app.yaml            the Custom App YAML pinned to the same image
    truenas_app.py check
        the source is whole: ix_values.yaml, app.yaml and custom-app.yaml name the same release,
        and app.yaml's version is the one derived from it

The catalog's own version (`version`) must grow with every change the catalog takes; it is
derived from Maktaba's (vX.Y.Z → (X+1).Y.Z: v0.3.121 → 1.3.121), so every release's copy is
newer than the last. release.yml renders the copy (with the server image's digest) and publishes
it as release assets signed by checksums.txt: maktaba-truenas-custom-app.yaml and
maktaba-truenas-app-<version>.tar.gz. Once the app is in the catalog, packages.yml's
`truenas-catalog` job verifies that archive and opens the bump pull request to truenas/apps with
its files; it checks out and runs no code (it runs on workflow_run, with a token).

Standard library only: the files are edited line by line, keeping their comments and layout.
"""

from __future__ import annotations

import argparse
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
TRUENAS = ROOT / "deploy" / "nas" / "truenas"
APP_REL = Path("ix-dev") / "community" / "maktaba"
APP = TRUENAS / APP_REL
CUSTOM_APP = TRUENAS / "custom-app.yaml"
IMAGE = "ghcr.io/hamza-labs-core/maktaba-server"

TAG_RE = re.compile(r"^v(\d+)\.(\d+)\.(\d+)$")
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
# ix_values.yaml: the `image` entry's tag (the first `tag:` under `  image:`)
IX_IMAGE_TAG = re.compile(r"(?m)^(  image:\n    repository: [^\n]+\n    tag: )([^\n]+)$")
APP_VERSION = re.compile(r"(?m)^app_version: ([^\n]+)$")
VERSION = re.compile(r"(?m)^version: ([^\n]+)$")
CUSTOM_IMAGE = re.compile(r"(?m)^(\s+image: " + re.escape(IMAGE) + r":)(\S+)$")


class Broken(Exception):
    pass


def parse_tag(tag: str) -> tuple[int, int, int]:
    m = TAG_RE.match(tag)
    if not m:
        raise Broken(f"{tag!r} is not a release tag (vX.Y.Z)")
    return int(m[1]), int(m[2]), int(m[3])


def catalog_version(tag: str) -> str:
    """The catalog's app version for a Maktaba release: vX.Y.Z → (X+1).Y.Z."""
    x, y, z = parse_tag(tag)
    return f"{x + 1}.{y}.{z}"


def image_ref(tag: str, digest: str = "") -> str:
    """The pin: the tag, or tag@digest (as the catalog's own apps pin their images)."""
    parse_tag(tag)
    if digest and not DIGEST_RE.match(digest):
        raise Broken(f"{digest!r} is not an image digest (sha256:<64 hex>)")
    return f"{tag}@{digest}" if digest else tag


def unquote(v: str) -> str:
    return v.strip().strip("'\"")


def sub_one(pattern: re.Pattern[str], text: str, repl: str, what: str) -> str:
    out, n = pattern.subn(repl, text)
    if n != 1:
        raise Broken(f"{what}: expected one match, found {n}")
    return out


def pin_ix_values(text: str, ref: str) -> str:
    return sub_one(IX_IMAGE_TAG, text, lambda m: m[1] + ref, "ix_values.yaml's image tag")  # type: ignore[arg-type]


def pin_app_yaml(text: str, tag: str) -> str:
    x, y, z = parse_tag(tag)
    text = sub_one(APP_VERSION, text, f"app_version: {x}.{y}.{z}", "app.yaml's app_version")
    return sub_one(VERSION, text, f"version: {catalog_version(tag)}", "app.yaml's version")


def pin_custom_app(text: str, ref: str) -> str:
    return sub_one(CUSTOM_IMAGE, text, lambda m: m[1] + ref, "custom-app.yaml's image")  # type: ignore[arg-type]


def pinned_tag(ix_values: str) -> str:
    m = IX_IMAGE_TAG.search(ix_values)
    if not m:
        raise Broken("ix_values.yaml has no image tag")
    return unquote(m[2]).split("@", 1)[0]


def check(app: Path = APP, custom_app: Path = CUSTOM_APP) -> list[str]:
    """What is wrong with the source ([] when it is whole)."""
    errs: list[str] = []
    try:
        tag = pinned_tag((app / "ix_values.yaml").read_text(encoding="utf-8"))
        parse_tag(tag)
    except (Broken, OSError) as e:
        return [str(e)]
    app_yaml = (app / "app.yaml").read_text(encoding="utf-8")
    av, v = APP_VERSION.search(app_yaml), VERSION.search(app_yaml)
    if not av or unquote(av[1]) != tag[1:]:
        errs.append(f"app.yaml's app_version is {av and unquote(av[1])!r}, ix_values.yaml pins {tag}")
    if not v or unquote(v[1]) != catalog_version(tag):
        errs.append(f"app.yaml's version is {v and unquote(v[1])!r}, expected {catalog_version(tag)} for {tag}")
    m = CUSTOM_IMAGE.search(custom_app.read_text(encoding="utf-8"))
    if not m or m[2].split("@", 1)[0] != tag:
        errs.append(f"custom-app.yaml's image is {m and m[2]!r}, ix_values.yaml pins {tag}")
    for f in ("app.yaml", "ix_values.yaml", "questions.yaml", "item.yaml", "README.md",
              "templates/docker-compose.yaml", "templates/test_values/basic-values.yaml"):
        if not (app / f).is_file():
            errs.append(f"the app has no {f} (truenas/apps' layout)")
    if "MAKTABA_UPDATE_DISABLE" in (app / "templates" / "docker-compose.yaml").read_text(encoding="utf-8"):
        errs.append("the app's compose sets MAKTABA_UPDATE_DISABLE: the self-update stays on (Epic 59 §11 decision 15)")
    if "MAKTABA_UPDATE_DISABLE" in re.sub(r"(?m)#.*$", "", custom_app.read_text(encoding="utf-8")):
        errs.append("custom-app.yaml sets MAKTABA_UPDATE_DISABLE: the self-update stays on (Epic 59 §11 decision 15)")
    return errs


def render(tag: str, digest: str, out: Path, app: Path = APP, custom_app: Path = CUSTOM_APP) -> Path:
    """The release's copy under out; returns the app's folder there."""
    ref = image_ref(tag, digest)
    dst = out / APP_REL
    if dst.exists():
        shutil.rmtree(dst)
    shutil.copytree(app, dst, ignore=shutil.ignore_patterns("library", "rendered"))
    for name, pin in (("ix_values.yaml", lambda t: pin_ix_values(t, ref)), ("app.yaml", lambda t: pin_app_yaml(t, tag))):
        p = dst / name
        p.write_text(pin(p.read_text(encoding="utf-8")), encoding="utf-8")
    (out / "custom-app.yaml").write_text(pin_custom_app(custom_app.read_text(encoding="utf-8"), ref), encoding="utf-8")
    return dst


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("render", help="the release's copy, pinned to its image")
    r.add_argument("--tag", required=True)
    r.add_argument("--digest", default="")
    r.add_argument("--out", required=True, type=Path)
    sub.add_parser("check", help="the source is whole")
    a = ap.parse_args(argv)
    try:
        if a.cmd == "render":
            dst = render(a.tag, a.digest, a.out)
            print(f"{dst} and {a.out / 'custom-app.yaml'}: {image_ref(a.tag, a.digest)}, version {catalog_version(a.tag)}")
            return 0
        errs = check()
    except Broken as e:
        print(f"truenas_app: {e}", file=sys.stderr)
        return 1
    for e in errs:
        print(f"truenas_app: {e}", file=sys.stderr)
    if not errs:
        print("truenas_app: the app and the Custom App pin the same release")
    return 1 if errs else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
