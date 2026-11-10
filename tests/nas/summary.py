#!/usr/bin/env python3
"""The nas-shapes verdict's table (story 59.15): one row per cell job with its result and a
link to its logs (the artifact nas-cell-<cell>-<arch>).

    summary.py JOBS_JSON ARTIFACTS_JSON REPO RUN_ID

JOBS_JSON and ARTIFACTS_JSON are what `gh api --paginate` printed for the run's jobs and
artifacts: one or more JSON objects back to back. Prints Markdown rows.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any


def objects(text: str) -> list[dict[str, Any]]:
    """Every JSON object in text, back to back (gh api --paginate's output)."""
    dec, out, i = json.JSONDecoder(), [], 0
    while True:
        while i < len(text) and text[i].isspace():
            i += 1
        if i >= len(text):
            return out
        obj, i = dec.raw_decode(text, i)
        out.append(obj)


def rows(jobs: list[dict[str, Any]], artifacts: list[dict[str, Any]], repo: str, run: str) -> list[str]:
    arts = {a["name"]: a for a in artifacts}
    out = []
    for j in sorted(jobs, key=lambda j: j["name"]):
        name = j["name"]
        if not name.startswith("cell ") or not name.endswith(")") or " (" not in name:
            continue
        cell, arch = name[len("cell "):-1].rsplit(" (", 1)
        art = arts.get(f"nas-cell-{cell}-{arch}")
        link = f"[logs](https://github.com/{repo}/actions/runs/{run}/artifacts/{art['id']})" if art else "none"
        result = j.get("conclusion") or j.get("status")
        out.append(f"| [{name}]({j.get('html_url', '')}) | {result} | {link} |")
    return out


def main(argv: list[str]) -> int:
    jobs_file, arts_file, repo, run = argv[1:5]
    jobs = [j for d in objects(Path(jobs_file).read_text(encoding="utf-8")) for j in d.get("jobs", [])]
    arts = [a for d in objects(Path(arts_file).read_text(encoding="utf-8")) for a in d.get("artifacts", [])]
    print("\n".join(rows(jobs, arts, repo, run)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
