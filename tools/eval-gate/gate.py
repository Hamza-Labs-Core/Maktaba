#!/usr/bin/env python3
"""Word review's evaluation gate (Epic 52, story 52.2).

    python3 tools/eval-gate/gate.py --report report.json --baseline gold/baseline.json \
        [--base-ref origin/main]

Fails when precision or recall of any language drops by more than ``TOLERANCE`` against
the baseline, naming the metric, unless a commit since ``--base-ref`` carries an
``Eval-Change: <reason>`` trailer (the same idea as ``No-Test-Change``). A PR that improves
the numbers updates the baseline.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

TOLERANCE = 0.02
TRAILER = re.compile(r"^Eval-Change:\s*\S", re.M)


def drops(report: dict[str, Any], baseline: dict[str, Any]) -> list[str]:
    out = []
    for lang, base in sorted(baseline.get("languages", {}).items()):
        got = report.get("languages", {}).get(lang)
        if got is None:
            out.append(f"{lang}: missing from the report")
            continue
        for metric in ("precision", "recall"):
            if float(got[metric]) < float(base[metric]) - TOLERANCE:
                out.append(f"{lang} {metric}: {got[metric]:.3f}, baseline {base[metric]:.3f}")
    return out


def has_trailer(base_ref: str | None, messages: str | None = None) -> bool:
    if messages is None:
        if not base_ref:
            return False
        try:
            messages = subprocess.run(
                ["git", "log", "--format=%B", f"{base_ref}..HEAD"],
                check=True, capture_output=True, text=True,
            ).stdout  # fmt: skip
        except (OSError, subprocess.CalledProcessError):
            return False
    return bool(TRAILER.search(messages))


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--report", type=Path, required=True)
    ap.add_argument("--baseline", type=Path, required=True)
    ap.add_argument("--base-ref", default=None)
    a = ap.parse_args(argv)
    report = json.loads(a.report.read_text(encoding="utf-8"))
    baseline = json.loads(a.baseline.read_text(encoding="utf-8"))
    bad = drops(report, baseline)
    if not bad:
        print("eval-gate: no metric dropped")
        return 0
    if has_trailer(a.base_ref):
        print("eval-gate: dropped, accepted by an Eval-Change trailer: " + "; ".join(bad))
        return 0
    for b in bad:
        print(f"eval-gate: {b}", file=sys.stderr)
    print("eval-gate: add an `Eval-Change: <reason>` trailer to accept it", file=sys.stderr)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
