#!/usr/bin/env python3
"""Lint the NAS device checklists (story 59.15, T04): docs/nas/checklists/*.md.

    check_checklists.py [DIR]

Each checklist has front matter (platform, devices, status: draft or ready) and steps: a
`### N. Title` heading, numbered from 1 with no gap, followed by `- **Do:**`, `- **See:**`
(the expected result) and `- **Screenshots:**`, which lists an English `.en.png` and an
Arabic `.ar.png` of the same name (relative to DIR/screenshots) or says `none`. A `ready`
checklist must have every screenshot it names; a `draft` one has not been run on its device
yet. README.md is the folder's guide, not a checklist. Exit 1 with every problem listed.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

STEP = re.compile(r"^### (\d+)\. \S")
FIELD = re.compile(r"^- \*\*(Do|See|Screenshots):\*\*\s*(.*)$")
SHOT = re.compile(r"`([^`]+\.png)`")


def front_matter(text: str) -> dict[str, str]:
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---\n", 4)
    if end < 0:
        return {}
    out = {}
    for line in text[4:end].splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            out[k.strip()] = v.strip()
    return out


def check_file(path: Path, shots_dir: Path) -> list[str]:
    text = path.read_text(encoding="utf-8")
    errs: list[str] = []
    fm = front_matter(text)
    for key in ("platform", "devices", "status"):
        if not fm.get(key):
            errs.append(f"{path.name}: front matter has no {key}")
    status = fm.get("status", "")
    if status and status not in ("draft", "ready"):
        errs.append(f"{path.name}: status is {status!r}, not draft or ready")

    steps: list[tuple[int, int, dict[str, str]]] = []  # number, line, fields
    for i, line in enumerate(text.splitlines(), 1):
        m = STEP.match(line)
        if m:
            steps.append((int(m.group(1)), i, {}))
            continue
        f = FIELD.match(line)
        if f and steps:
            steps[-1][2][f.group(1)] = f.group(2).strip()
    if not steps:
        errs.append(f"{path.name}: no steps (### N. Title)")
    for want, (n, line, fields) in enumerate(steps, 1):
        where = f"{path.name}:{line} step {n}"
        if n != want:
            errs.append(f"{where}: numbered {n}, expected {want}")
        for key in ("Do", "See", "Screenshots"):
            if not fields.get(key):
                errs.append(f"{where}: no {key}" + (" (the expected result)" if key == "See" else ""))
        shots = fields.get("Screenshots", "")
        if not shots or shots == "none":
            continue
        names = SHOT.findall(shots)
        en = {s[: -len(".en.png")] for s in names if s.endswith(".en.png")}
        ar = {s[: -len(".ar.png")] for s in names if s.endswith(".ar.png")}
        if not en or en != ar or len(names) != len(en) + len(ar):
            errs.append(f"{where}: screenshots must come in pairs, name.en.png and name.ar.png: {names}")
        if status == "ready":
            for s in names:
                if not (shots_dir / s).is_file():
                    errs.append(f"{where}: screenshot {s} is missing (status ready)")
    return errs


def check_dir(d: Path) -> list[str]:
    files = sorted(p for p in d.glob("*.md") if p.name != "README.md")
    if not files:
        return [f"{d}: no checklist"]
    errs: list[str] = []
    for f in files:
        errs += check_file(f, d / "screenshots")
    return errs


def main(argv: list[str]) -> int:
    d = Path(argv[1] if len(argv) > 1 else "docs/nas/checklists")
    errs = check_dir(d)
    for e in errs:
        print(e, file=sys.stderr)
    if not errs:
        print(f"checklists: {', '.join(p.name for p in sorted(d.glob('*.md')) if p.name != 'README.md')} ok")
    return 1 if errs else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
