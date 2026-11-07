#!/usr/bin/env python3
"""maktaba.dev follows what ships: a warning when a pull request adds user-facing work and leaves the portal alone.

CLAUDE.md asks a pull request that ships or changes a user-facing feature, an app screen, a
developer-facing API or the plugin system to update maktaba.dev (`web/packages/devportal`,
and the guides it publishes from `docs/developers/`) in the same pull request, in English and
Arabic. This check reads the pull request's changes and warns when it finds such work and no
change to the portal:

- a new screen or page in an app: a file added under `web/src/pages/` or `web/src/bigscreen/`,
  a `*Screen.kt` or `*Page.kt` added to an Android, Android TV or desktop app, a `*View.swift`
  or `*Screen.swift` added to an Apple app;
- a new path in the API's OpenAPI description (`shared/api/openapi.json`);
- a change to the plugin system's contract: the manifest schema (`shared/plugins/`) or the
  plugin API (`api/internal/plugins/`).

It only warns: a new file is not always a new feature (a split, a test helper), so a hard
gate would be noisy. A commit in the pull request with a `No-Portal-Change: <reason>` trailer
says the portal needs nothing, and turns the warning into a notice.

    devportal_follows.py --range BASE...HEAD
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections.abc import Iterable, Sequence
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# where the portal's own content lives
PORTAL = ("web/packages/devportal/", "docs/developers/")

TRAILER = re.compile(r"^No-Portal-Change:\s*(\S.*)$", re.MULTILINE)

_TEST = re.compile(r"(\.test\.|\.cases\.|Test\.kt$|Tests?\.swift$|/test/|/tests/|/androidTest/|UITests/|Previews?\.swift$)")


def new_screen(path: str) -> bool:
    """A file that adds a screen or page to an app (tests and previews are not screens)."""
    if _TEST.search(path):
        return False
    if path.startswith(("web/src/pages/", "web/src/bigscreen/")):
        return path.endswith(".tsx")
    if path.startswith(("apps/android/", "apps/tv/android/", "apps/desktop-player/")):
        return path.endswith(("Screen.kt", "Page.kt")) and "/src/main/" in path
    if path.startswith(("apps/apple/", "apps/tv/tvos/")):
        return path.endswith(("View.swift", "Screen.swift"))
    return False


def plugin_contract(path: str) -> bool:
    """A change to what plugin authors build against."""
    if _TEST.search(path) or path.endswith("_test.go"):
        return False
    return path.startswith(("shared/plugins/", "api/internal/plugins/"))


def new_api_paths(before: dict | None, after: dict | None) -> list[str]:
    """The OpenAPI paths `after` has and `before` did not."""
    old = set((before or {}).get("paths", {}))
    return sorted(p for p in (after or {}).get("paths", {}) if p not in old)


def findings(changes: Iterable[tuple[str, str]], api_added: Sequence[str] = ()) -> list[str]:
    """What in (status, path) changes asks for a portal update; [] when the portal changed too."""
    changes = list(changes)
    if any(p.startswith(PORTAL) for _, p in changes):
        return []
    out = [f"new screen: {p}" for s, p in changes if s.startswith("A") and new_screen(p)]
    out += [f"plugin system: {p}" for _, p in changes if plugin_contract(p)]
    out += [f"new API path: {p}" for p in api_added]
    return out


def _git(*args: str) -> str:
    return subprocess.run(["git", "-C", str(ROOT), *args], check=True, capture_output=True, text=True).stdout


def _openapi_at(rev: str) -> dict | None:
    try:
        return json.loads(_git("show", f"{rev}:shared/api/openapi.json"))
    except (subprocess.CalledProcessError, json.JSONDecodeError):
        return None


def main(argv: Sequence[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--range", required=True, help="BASE...HEAD")
    a = ap.parse_args(argv)
    base, _, head = a.range.partition("...")
    if not head:
        ap.error("--range takes BASE...HEAD")
    fork = _git("merge-base", base, head).strip()
    changes = []
    for line in _git("diff", "--name-status", "--no-renames", f"{fork}..{head}").splitlines():
        status, _, path = line.partition("\t")
        changes.append((status, path))
    api_added: list[str] = []
    if any(p == "shared/api/openapi.json" for _, p in changes):
        api_added = new_api_paths(_openapi_at(fork), _openapi_at(head))
    found = findings(changes, api_added)
    if not found:
        print("maktaba.dev: nothing here asks for a portal change, or the portal changed too.")
        return 0
    reasons = TRAILER.findall(_git("log", "--format=%B", f"{fork}..{head}"))
    listing = "\n".join(f"  - {f}" for f in found[:30]) + ("\n  - …" if len(found) > 30 else "")
    if reasons:
        print(f"::notice title=maktaba.dev::No-Portal-Change: {reasons[-1].strip()}")
        print(listing)
        return 0
    print(
        "::warning title=maktaba.dev::This pull request adds user-facing work and does not touch "
        "maktaba.dev (web/packages/devportal, docs/developers). Update the portal in English and "
        "Arabic (CLAUDE.md), or add a `No-Portal-Change: <reason>` trailer to a commit."
    )
    print(listing)
    return 0


if __name__ == "__main__":
    sys.exit(main())
