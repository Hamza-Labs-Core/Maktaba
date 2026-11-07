#!/usr/bin/env python3
"""The postmortems that are due (Epic 50 story 50.12): every incident on the status page marked
`severity: sev1`, resolved more than 5 days ago, with no docs/postmortems/<incident id>.md.

    python3 tools/runbooks/postmortems.py [--now ISO time] [repo root]

Prints one incident id a line (nothing when none is due). The Postmortem reminder workflow
opens a SEV3 issue for each.
"""
from __future__ import annotations

import argparse
import re
from datetime import datetime, timedelta, timezone
from pathlib import Path

INCIDENTS = "status/data/incidents"
POSTMORTEMS = "docs/postmortems"
DUE_AFTER = timedelta(days=5)


def front_matter(text: str) -> dict[str, str]:
    m = re.match(r"---\n(.*?)\n---\n", text.replace("\r\n", "\n"), re.S)
    if not m:
        return {}
    out = {}
    for line in m.group(1).splitlines():
        if ":" in line and not line.strip().startswith("#"):
            k, v = line.split(":", 1)
            out[k.strip()] = v.strip().strip("\"'")
    return out


def due(root: Path, now: datetime) -> list[str]:
    out = []
    for p in sorted((root / INCIDENTS).glob("*.md")):
        fm = front_matter(p.read_text())
        if fm.get("severity") != "sev1" or fm.get("state") != "resolved" or not fm.get("resolved"):
            continue
        try:
            resolved = datetime.fromisoformat(fm["resolved"].replace("Z", "+00:00"))
        except ValueError:
            continue  # the status page's build refuses it already
        if now - resolved > DUE_AFTER and not (root / POSTMORTEMS / f"{p.stem}.md").exists():
            out.append(p.stem)
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--now")
    ap.add_argument("root", nargs="?", default=str(Path(__file__).resolve().parents[2]))
    a = ap.parse_args()
    now = datetime.fromisoformat(a.now.replace("Z", "+00:00")) if a.now else datetime.now(timezone.utc)
    for i in due(Path(a.root), now):
        print(i)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
