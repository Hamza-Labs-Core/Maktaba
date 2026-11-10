#!/usr/bin/env python3
"""The nas-shapes matrix for GitHub Actions (story 59.15), from tests/nas/cells.json.

    plan.py [--no-arm64] [--only CELL,...]

Prints one JSON object, the `include` list of the cells job's matrix: one entry per cell and
architecture, {"cell", "arch", "runner"}. A cell whose `needs` are not all in `landed` is left
out (its story has not merged yet). --no-arm64 drops the arm64 entries: arm64 runners queue
for longer, and the arm64 cells are required only when the image, streaming or the pipeline's
speech code changed (the workflow's plan job decides). --only keeps the named cells.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
RUNNERS = {"amd64": "ubuntu-24.04", "arm64": "ubuntu-24.04-arm"}


def plan(matrix: dict, arm64: bool = True, only: set[str] | None = None) -> list[dict[str, str]]:
    landed = set(matrix.get("landed", []))
    out = []
    for cell in matrix["cells"]:
        if only and cell["name"] not in only:
            continue
        if not set(cell.get("needs", [])) <= landed:
            continue
        for arch in cell.get("arches", ["amd64"]):
            if arch == "arm64" and not arm64:
                continue
            out.append({"cell": cell["name"], "arch": arch, "runner": RUNNERS[arch]})
    return out


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--no-arm64", action="store_true")
    ap.add_argument("--only", default="")
    ap.add_argument("--cells", type=Path, default=HERE / "cells.json")
    args = ap.parse_args(argv)
    matrix = json.loads(args.cells.read_text(encoding="utf-8"))
    only = {c.strip() for c in args.only.split(",") if c.strip()} or None
    include = plan(matrix, arm64=not args.no_arm64, only=only)
    print(json.dumps({"include": include}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
