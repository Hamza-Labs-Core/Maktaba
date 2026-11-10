#!/usr/bin/env python3
"""The Synology Container Manager project's files, made from the published Docker compose
(Epic 59 story 59.09).

    deploy/nas/generate.py            write deploy/nas/synology/compose{,.j4125}.yaml
    deploy/nas/generate.py --check    fail when a committed file differs from what it writes
    deploy/nas/generate.py --tag vX.Y.Z --out DIR
                                      the release's files, the image pinned to the tag:
                                      DIR/maktaba-synology-compose.yaml and
                                      DIR/maktaba-synology-compose.j4125.yaml

The published compose (cloud/installers/docker/docker-compose.yml) is the NAS base (story
59.11): its optional settings are `# [name]` blocks, turned on by removing the "# " in front
of their settings (tests/nas/install_files.py's enable(), the install page's compose.ts).
Synology's files are that file filled in as maktaba.tv/install/synology fills it with its
defaults: the DSM user 1026:100, the [memory] block at the Standard tier's 6g, the data folder
/volume1/docker/maktaba, two shares at their own paths (Movies read only, Recordings read and
write), the [dri] block for the J4125 version; then MAKTABA_PLATFORM=dsm and the Synology
header. web/packages/install/src/synology.test.ts checks the page gives exactly these files;
tests/nas/test_synology.py runs --check, and the synology-compose cell runs the file on
Container Manager's Docker 24.0.2.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "tests/nas"))

import install_files  # noqa: E402

BASE = install_files.COMPOSE
OUT = ROOT / "deploy/nas/synology"

# web/packages/install/src/synology.ts: HEADER and VERSION
HEADER = """\
# Maktaba on a Synology NAS (DSM 7.2 or newer): Container Manager → Project → Create, name
# maktaba, path docker/maktaba, this file pasted as the project's YAML. maktaba.tv/install/synology
# fills in the DSM user, the shares, the memory and the time zone from your answers, and says how
# to make the user and its folders. Made from the published Docker compose
# (cloud/installers/docker/docker-compose.yml), as deploy/nas/generate.py makes it.
"""
VERSION = {
    "compose.yaml": "models without Quick Sync (Ryzen, ARM, other Intel)",
    "compose.j4125.yaml": "J4125 models (Quick Sync through /dev/dri)",
}
SHARES = (("/volume1/Movies", True), ("/volume1/Recordings", False))


def once(text: str, old: str, new: str) -> str:
    if text.count(old) != 1:
        raise SystemExit(f"{BASE}: {old.strip()!r} is not there exactly once")
    return text.replace(old, new)


def synology(base: str, j4125: bool, image: str | None = None) -> str:
    """compose.ts's fillCompose with the page's defaults, then Synology's lines."""
    t = install_files.enable(base, ["memory", "dri"] if j4125 else ["memory"])
    t = once(t, 'user: "1000:1000"', 'user: "1026:100"')
    t = once(t, "- maktaba:/var/lib/maktaba", "- /volume1/docker/maktaba:/var/lib/maktaba")
    t = once(t, "\nvolumes:\n  maktaba:\n", "\n")
    m = re.search(r"^(\s*)- /srv/media:/srv/media$", t, re.M)
    indent = m.group(1) if m else "      "
    shares = "\n".join(f"{indent}- {p}:{p}{':ro' if ro else ''}" for p, ro in SHARES)
    t = once(t, f"{indent}- /srv/media:/srv/media", shares)
    t = once(t, "MAKTABA_PLATFORM: docker", "MAKTABA_PLATFORM: dsm")
    if image:
        t = re.sub(r"^(\s*image:\s*)\S+", lambda x: x.group(1) + image, t, count=1, flags=re.M)
    name = "compose.j4125.yaml" if j4125 else "compose.yaml"
    return f"{HEADER}# This version: {VERSION[name]}.\n\n{t[t.index('services:'):]}"


def files(image: str | None = None) -> dict[str, str]:
    base = BASE.read_text(encoding="utf-8")
    return {"compose.yaml": synology(base, False, image), "compose.j4125.yaml": synology(base, True, image)}


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--check", action="store_true")
    ap.add_argument("--tag")
    ap.add_argument("--out", type=Path)
    a = ap.parse_args(argv)
    if a.tag:
        if not a.out or not re.fullmatch(r"v\d+\.\d+\.\d+", a.tag):
            ap.error("--tag vX.Y.Z needs --out")
        a.out.mkdir(parents=True, exist_ok=True)
        for name, text in files(f"{install_files.IMAGE}:{a.tag}").items():
            (a.out / f"maktaba-synology-{name}").write_text(text, encoding="utf-8")
        return 0
    stale = []
    for name, text in files().items():
        path = OUT / name
        if a.check:
            if not path.exists() or path.read_text(encoding="utf-8") != text:
                stale.append(str(path.relative_to(ROOT)))
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text, encoding="utf-8")
    if stale:
        print("stale (run deploy/nas/generate.py): " + ", ".join(stale), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
