#!/usr/bin/env python3
"""The NAS compose files, derived from the published Docker compose (Epic 59).

    deploy/nas/generate.py            write deploy/nas/synology/compose{,.j4125}.yaml
    deploy/nas/generate.py --check    fail when a committed file differs from what it writes
    deploy/nas/generate.py --tag vX.Y.Z --out DIR
                                      the release's files, the image pinned to the tag:
                                      DIR/maktaba-synology-compose.yaml and
                                      DIR/maktaba-synology-compose.j4125.yaml

The plain Docker compose (cloud/installers/docker/docker-compose.yml) is the one source
(story 59.11): the image, the restart policy, the stop grace period and the network mode are
read from its service, so a change there reaches the Synology files the next time this runs
(CI runs --check: tests/nas/test_synology.py). Synology's own lines (story 59.09) are written
here: the DSM user (1026:100), the container's name, the memory limit of the Standard tier,
MAKTABA_PLATFORM=dsm, the data folder on volume1 and two shares at their own paths; the J4125
file adds the render device and its group.

maktaba.tv's install page (web/cloud/src/install/synology.ts) writes the same file from a
household's answers; its test checks that the page's defaults give exactly these files.

Only keys Container Manager's Docker 24.0.2 (and its compose) reads are used.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASE = ROOT / "cloud/installers/docker/docker-compose.yml"
OUT = ROOT / "deploy/nas/synology"

# the base's service keys this derives (the first uncommented occurrence, four spaces in)
KEYS = ("image", "restart", "stop_grace_period", "network_mode")

HEADER = """\
# Maktaba on a Synology NAS: Container Manager → Project → Create, name maktaba, path
# docker/maktaba, this file pasted as the project's YAML. maktaba.tv/install/synology fills
# in the user, the shares, the memory limit and (optionally) this NAS's address and time
# zone from your answers; the guide there says how to make the DSM user and its folders.
#
# Generated from cloud/installers/docker/docker-compose.yml by deploy/nas/generate.py
# (Epic 59 story 59.09): edit those, not this file.
"""

GPU = """\
    # J4125 (Quick Sync): the render device and its group, so the DSM user may open it.
    # The group's id differs from NAS to NAS: over SSH, stat -c %g /dev/dri/renderD128
    devices:
      - /dev/dri:/dev/dri
    group_add:
      - "937"
"""


def base_keys(text: str) -> dict[str, str]:
    out: dict[str, str] = {}
    for line in text.splitlines():
        m = re.match(r"^    ([a-z_]+): (.+?)\s*$", line)
        if m and m.group(1) in KEYS and m.group(1) not in out:
            out[m.group(1)] = m.group(2)
    lacking = [k for k in KEYS if k not in out]
    if lacking:
        raise SystemExit(f"{BASE}: no {', '.join(lacking)} in the service")
    return out


def synology(base: dict[str, str], j4125: bool, image: str | None = None) -> str:
    model = "J4125 models (Quick Sync)" if j4125 else "models without a GPU for video (Ryzen, ARM, other Intel)"
    lines = [
        HEADER,
        f"# This version: {model}.",
        "",
        "services:",
        "  maktaba:",
        f"    image: {image or base['image']}",
        "    container_name: maktaba",
        "    # the DSM user made for Maktaba (its uid, and the users group, 100)",
        '    user: "1026:100"',
        f"    network_mode: {base['network_mode']}",
        f"    restart: {base['restart']}",
        f"    stop_grace_period: {base['stop_grace_period']}",
        "    # the memory tier's limit: Light 4g, Standard 6g, Strong 16g",
        "    mem_limit: 6g",
        "    environment:",
        "      MAKTABA_PLATFORM: dsm",
        "      # MAKTABA_HOST_IP: 192.168.1.20   # this NAS's address on your network",
        "      # TZ: Asia/Riyadh                 # your time zone",
        "    volumes:",
        "      # everything Maktaba keeps (an SSD volume's folder when there is one)",
        "      - /volume1/docker/maktaba:/var/lib/maktaba",
        "      # each share at its own path; :ro where Maktaba may only read",
        "      - /volume1/Movies:/volume1/Movies:ro",
        "      - /volume1/Recordings:/volume1/Recordings",
    ]
    text = "\n".join(lines) + "\n"
    return text + (GPU if j4125 else "")


def files(image: str | None = None) -> dict[str, str]:
    base = base_keys(BASE.read_text(encoding="utf-8"))
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
        base = base_keys(BASE.read_text(encoding="utf-8"))
        repo = base["image"].rsplit(":", 1)[0]
        a.out.mkdir(parents=True, exist_ok=True)
        for name, text in files(f"{repo}:{a.tag}").items():
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
