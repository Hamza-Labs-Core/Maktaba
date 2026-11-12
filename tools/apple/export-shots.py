#!/usr/bin/env python3
"""Exports the product screenshots (story 60.09) from UI test result bundles as PNG files
(.github/workflows/_apple-apps.yml).

    export-shots.py OUT_DIR RESULT.xcresult...   # each `shot-<app>-<screen>-<lang>` → OUT_DIR/<app>-<screen>-<lang>.png
    export-shots.py --self-test

A UI test attaches each product screenshot named `shot-<app>-<screen>-<language>`
(`PlayerDemoShots`, `RemoteDemoShotsUITests`, `TVDemoShotsUITests`,
`PlayerMacDemoShotsUITests`). `xcrun xcresulttool export attachments` writes every attachment
of a bundle and a `manifest.json` mapping each one's `suggestedHumanReadableName` (the
attachment's name, with a suffix of its own: `shot-iphone-home-en_0_<UUID>.png`) to the file
it wrote; the `shot-` ones are copied out under their plain names, as PNG (`sips` converts one
written in another format). A name seen twice keeps its last one (a test run again). Prints
what it wrote; exits 1 when it wrote nothing, so a job whose shots are missing says so.
"""
from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

# the attachment's own name, before the suffix xcresulttool adds
NAME = re.compile(r"^shot-([a-z]+-[a-z0-9]+-[a-z]{2})(?=[_.]|$)")


def shots(manifest: list[dict]) -> dict[str, str]:
    """`<app>-<screen>-<lang>` → the exported file's name, for each `shot-` attachment."""
    out: dict[str, str] = {}
    for test in manifest:
        for a in sorted(test.get("attachments", []), key=lambda a: a.get("timestamp", 0)):
            m = NAME.match(a.get("suggestedHumanReadableName", ""))
            if m:
                out[m.group(1)] = a["exportedFileName"]
    return out


def export(bundle: str, out_dir: str) -> list[str]:
    written = []
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(["xcrun", "xcresulttool", "export", "attachments", "--path", bundle, "--output-path", tmp],
                       check=True)
        with open(os.path.join(tmp, "manifest.json"), encoding="utf-8") as fh:
            manifest = json.load(fh)
        for name, exported in sorted(shots(manifest).items()):
            src, dst = os.path.join(tmp, exported), os.path.join(out_dir, f"{name}.png")
            with open(src, "rb") as fh:
                png = fh.read(8) == b"\x89PNG\r\n\x1a\n"
            if png:
                shutil.copyfile(src, dst)
            else:
                subprocess.run(["sips", "-s", "format", "png", src, "--out", dst], check=True, capture_output=True)
            written.append(dst)
    return written


def self_test() -> None:
    manifest = [
        {"testIdentifier": "PlayerDemoShots/testHome()", "attachments": [
            {"exportedFileName": "a.png", "suggestedHumanReadableName": "shot-iphone-home-en_0_1F2E.png", "timestamp": 1},
            {"exportedFileName": "b.png", "suggestedHumanReadableName": "shot-iphone-home-ar_0_3C4D.png", "timestamp": 2},
            {"exportedFileName": "c.png", "suggestedHumanReadableName": "player-iphone-en-home_0_99.png", "timestamp": 3},
            {"exportedFileName": "d.txt", "suggestedHumanReadableName": "PlayerUITestCase warm-up_0_1.txt", "timestamp": 4},
        ]},
        {"testIdentifier": "TVDemoShotsUITests/testGuide()", "attachments": [
            {"exportedFileName": "e.jpeg", "suggestedHumanReadableName": "shot-appletv-guide-ar.jpeg", "timestamp": 5},
            {"exportedFileName": "f.png", "suggestedHumanReadableName": "shot-appletv-guide-ar_1_2.png", "timestamp": 6},
        ]},
    ]
    got = shots(manifest)
    want = {"iphone-home-en": "a.png", "iphone-home-ar": "b.png", "appletv-guide-ar": "f.png"}
    assert got == want, got
    assert not NAME.match("shot-iphone-home-english"), "a language is two letters"
    print("export-shots self-test: ok")


def main(argv: list[str]) -> int:
    if argv == ["--self-test"]:
        self_test()
        return 0
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    out_dir, bundles = argv[0], argv[1:]
    os.makedirs(out_dir, exist_ok=True)
    written = []
    for bundle in bundles:
        if not os.path.isdir(bundle):
            print(f"no result bundle at {bundle}", file=sys.stderr)
            continue
        written += export(bundle, out_dir)
    for w in written:
        print(w)
    if not written:
        print("no shot- attachment in " + ", ".join(bundles), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
