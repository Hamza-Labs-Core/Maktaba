#!/usr/bin/env python3
"""Checks the feed the Core app's dry run wrote (Epic 51 story 51.17): exactly the Core keys
(darwin-aarch64-core and darwin-aarch64-core-app), each naming the release's
Maktaba-Core_<v>_aarch64.app.tar.gz. A Full key there would update every Full Mac to an app
with no ollama.

    core-feed-check.py LATEST_JSON VERSION
"""

from __future__ import annotations

import json
import sys

CORE_KEYS = ["darwin-aarch64-core", "darwin-aarch64-core-app"]


def problems(feed: dict, version: str) -> list[str]:
    out = []
    keys = sorted(feed.get("platforms", {}))
    if keys != CORE_KEYS:
        out.append(f"the Core app's feed keys are {keys}, not {CORE_KEYS}")
    want = "Maktaba-Core_" + version.removeprefix("v") + "_aarch64.app.tar.gz"
    for k, p in sorted(feed.get("platforms", {}).items()):
        if not str(p.get("url", "")).endswith("/" + want):
            out.append(f"{k} names {p.get('url')}, not {want}")
        if not p.get("signature"):
            out.append(f"{k} has no signature")
    return out


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    with open(argv[0], encoding="utf-8") as f:
        feed = json.load(f)
    found = problems(feed, argv[1])
    for p in found:
        print("FAIL " + p)
    if not found:
        print("ok   the feed has darwin-aarch64-core and -app, naming the Core archive, and no Full key")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
