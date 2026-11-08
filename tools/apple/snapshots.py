#!/usr/bin/env python3
"""Makes or updates the Mac Player's page references (issue #876) from a CI run.

PageSnapshots (apps/apple/PlayerMacTests/PageSnapshotTests.swift) attaches every page it
draws to its test, the image and its words. This copies those attachments into
apps/apple/PlayerMacTests/Snapshots/ as <name>.png and <name>.txt, the references the next run
compares with. Read them before committing: a reference is what the page is meant to look like
and say.

    tools/apple/snapshots.py <run id>           # downloads player-ui-tests-mac with gh
    tools/apple/snapshots.py <path/to/mac.xcresult>
    tools/apple/snapshots.py --self-test

Only names PageSnapshots makes are copied (<page>-<panels>-<lang>[-ax3].png or .txt, and the
Apple TV form's tv-panel-<lang>.png or .txt).
"""

from __future__ import annotations

import json
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
DEST = ROOT / "apps/apple/PlayerMacTests/Snapshots"
NAME = re.compile(
    r"^((?:library|series|video)-(?:none|collapsed|open)-(?:en|ar)(?:-ax3)?|tv-panel-(?:en|ar))(?:[._-].*)?\.(png|txt)$"
)


def reference_name(suggested: str) -> str | None:
    """The reference a suggested attachment name is for, or None for any other attachment."""
    m = NAME.match(suggested)
    return f"{m.group(1)}.{m.group(2)}" if m else None


def pick(manifest: list[dict]) -> dict[str, str]:
    """Exported file -> reference name, for PageSnapshots' attachments only."""
    out: dict[str, str] = {}
    for test in manifest:
        if "PageSnapshots" not in str(test.get("testIdentifier", "")) and "pageMatchesItsReference" not in str(
            test.get("testIdentifier", "")
        ):
            continue
        for a in test.get("attachments", []):
            ref = reference_name(str(a.get("suggestedHumanReadableName", "")))
            if ref:
                out[str(a["exportedFileName"])] = ref
    return out


def export(xcresult: Path) -> int:
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(
            ["xcrun", "xcresulttool", "export", "attachments", "--path", str(xcresult), "--output-path", tmp],
            check=True,
        )
        manifest = json.loads((Path(tmp) / "manifest.json").read_text())
        chosen = pick(manifest)
        if not chosen:
            print("no PageSnapshots attachments in", xcresult, file=sys.stderr)
            return 1
        DEST.mkdir(parents=True, exist_ok=True)
        for exported, ref in sorted(chosen.items(), key=lambda x: x[1]):
            shutil.copyfile(Path(tmp) / exported, DEST / ref)
            print("wrote", (DEST / ref).relative_to(ROOT))
    return 0


def from_run(run: str) -> int:
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(["gh", "run", "download", run, "--name", "player-ui-tests-mac", "--dir", tmp], check=True)
        found = sorted(Path(tmp).rglob("mac.xcresult"))
        if not found:
            print("no mac.xcresult in run", run, file=sys.stderr)
            return 1
        return export(found[0])


def self_test() -> int:
    assert reference_name("library-none-en.png") == "library-none-en.png"
    assert reference_name("series-open-ar-ax3.png") == "series-open-ar-ax3.png"
    assert reference_name("video-collapsed-en_0_2F1C.png") == "video-collapsed-en.png"
    assert reference_name("series-none-ar.txt") == "series-none-ar.txt"
    assert reference_name("series-none-ar_1_AB.txt") == "series-none-ar.txt"
    assert reference_name("series-none-ar.json") is None
    assert reference_name("tv-panel-ar.png") == "tv-panel-ar.png"
    assert reference_name("tv-panel-en_0_1A2B.txt") == "tv-panel-en.txt"
    assert reference_name("tv-panel-fr.png") is None
    assert reference_name("library-open-fr.png") is None
    assert reference_name("Screenshot.png") is None
    manifest = [
        {"testIdentifier": "PageSnapshots/pageMatchesItsReference(_:)", "attachments": [
            {"exportedFileName": "a.png", "suggestedHumanReadableName": "library-none-en.png"},
            {"exportedFileName": "b.png", "suggestedHumanReadableName": "other.png"},
            {"exportedFileName": "d.txt", "suggestedHumanReadableName": "library-none-en.txt"},
        ]},
        {"testIdentifier": "PageSnapshots/tvPanelMatchesItsReference(_:)", "attachments": [
            {"exportedFileName": "e.png", "suggestedHumanReadableName": "tv-panel-ar.png"},
        ]},
        {"testIdentifier": "PlayerMenus/x()", "attachments": [
            {"exportedFileName": "c.png", "suggestedHumanReadableName": "library-none-ar.png"},
        ]},
    ]
    assert pick(manifest) == {
        "a.png": "library-none-en.png",
        "d.txt": "library-none-en.txt",
        "e.png": "tv-panel-ar.png",
    }
    print("snapshots.py: self-test passed")
    return 0


def main(argv: list[str]) -> int:
    if argv == ["--self-test"]:
        return self_test()
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    arg = argv[0]
    return export(Path(arg)) if arg.endswith(".xcresult") else from_run(arg)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
