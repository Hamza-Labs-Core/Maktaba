#!/usr/bin/env python3
"""A TV build is submitted only with its real-device checklist passed (story 58.16).

Each TV platform has a checklist a person runs on real devices before a release candidate is
promoted or submitted: docs/stores/<platform>.md. Its result is a line in that file's
`## Results` section, newest last, and the same line goes in the release notes:

    - Samsung checklist passed on 2020/2022/2025 TVs, launcher 1.3.0 (2026-12-01)
    - Roku checklist failed on Roku Express 4K, channel 1.0.0 (2026-12-02): DV p8 stutters

The release session runs this before a store submission or a promotion:

    tv_checklist_guard.py --platform samsung --version 1.3.0

and prints (key=value lines, as for $GITHUB_OUTPUT):

    ok=true|false
    line=<the result line>        when it passed
    reason=<why the submission stops>

It exits 0 when the newest result for that version passed, and 1 (the submission step stops,
with the reason) when the checklist has no result for the version or its newest result failed.
A result names the platform and its component (PLATFORMS): a line for another platform's
build, or another version, does not count. Standard library only.
"""

from __future__ import annotations

import argparse
import re
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
STORES = ROOT / "docs" / "stores"


@dataclass(frozen=True)
class Platform:
    name: str  # as the result line writes it
    component: str  # what is versioned and submitted


# docs/stores/<key>.md for each TV platform
PLATFORMS: dict[str, Platform] = {
    "amazon": Platform("Fire TV", "app"),
    "cast": Platform("Cast", "receiver"),
    "samsung": Platform("Samsung", "launcher"),
    "lg": Platform("LG", "launcher"),
    "roku": Platform("Roku", "channel"),
}

RESULT = re.compile(
    r"^- (?P<name>.+?) checklist (?P<verdict>passed|failed) on (?P<devices>.+?), "
    r"(?P<component>[a-z]+) (?P<version>\d+\.\d+\.\d+)\b(?P<rest>.*)$"
)


@dataclass(frozen=True)
class Result:
    passed: bool
    version: str
    line: str


def results(text: str, platform: Platform) -> list[Result]:
    """The platform's result lines in a checklist's `## Results` section, in file order."""
    out: list[Result] = []
    in_results = False
    for raw in text.splitlines():
        line = raw.strip()
        if line.startswith("## "):
            in_results = line == "## Results"
            continue
        if not in_results:
            continue
        m = RESULT.match(line)
        if m and m["name"] == platform.name and m["component"] == platform.component:
            out.append(Result(m["verdict"] == "passed", m["version"], line[2:]))
    return out


def decide(text: str, key: str, version: str) -> tuple[bool, str]:
    """(ok, the passing line or the reason it stops)."""
    platform = PLATFORMS[key]
    mine = [r for r in results(text, platform) if r.version == version]
    if not mine:
        return False, (
            f"docs/stores/{key}.md has no checklist result for {platform.name} {platform.component} {version}: "
            "run its real-device checklist and record the result before submitting"
        )
    newest = mine[-1]
    if not newest.passed:
        return False, f"the newest {platform.name} checklist result for {version} failed: {newest.line}"
    return True, newest.line


def main(argv: Sequence[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--platform", required=True, choices=sorted(PLATFORMS))
    ap.add_argument("--version", required=True, help="the build's version, X.Y.Z")
    ap.add_argument("--stores", type=Path, default=STORES, help="the checklists' folder")
    args = ap.parse_args(argv)
    if not re.fullmatch(r"\d+\.\d+\.\d+", args.version):
        ap.error(f"--version {args.version!r} is not X.Y.Z")
    path = args.stores / f"{args.platform}.md"
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as e:
        print("ok=false")
        print(f"reason=the checklist {path} cannot be read ({e.strerror})")
        return 1
    ok, detail = decide(text, args.platform, args.version)
    print(f"ok={'true' if ok else 'false'}")
    print(f"{'line' if ok else 'reason'}={detail}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
