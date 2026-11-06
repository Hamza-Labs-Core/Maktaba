"""Checks the escape probes of the plugins-sandbox CI job (Epic 54, story 54.03 §5).

    python3 check_probes.py --sandboxed probes.txt --control control.txt \
        --canary-before 0 --canary-after-sandboxed 0 --canary-after-control 7 \
        [--expect-missing namespaces]

- sandboxed: every probe DENIED, the host said SANDBOX full (or partial with exactly the
  layers --expect-missing names), and the canary counted nothing (N8);
- control (the same binary with --no-sandbox): every network and file probe ALLOWED and
  the canary counted them, so a probe that "passes" because it is broken is caught.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path


def parse(text: str) -> tuple[str, list[str], list[tuple[str, str, str, str]]]:
    level, missing, probes = "", [], []
    for line in text.splitlines():
        if line.startswith("SANDBOX "):
            parts = line.split()
            level = parts[1]
            m = parts[2].removeprefix("missing=") if len(parts) > 2 else ""
            missing = [x for x in m.split(",") if x]
        elif line.startswith("PROBE "):
            _, name, kind, verdict, *detail = line.split(" ")
            probes.append((name, kind, verdict, " ".join(detail)))
    return level, missing, probes


def check(args: argparse.Namespace) -> list[str]:
    errors: list[str] = []
    level, missing, probes = parse(Path(args.sandboxed).read_text())
    if not probes:
        errors.append("the sandboxed run printed no probes")
    want_missing = sorted(x for x in (args.expect_missing or "").split(",") if x)
    if want_missing:
        if level != "partial" or sorted(missing) != want_missing:
            errors.append(f"sandbox {level} missing {missing}, expected partial missing {want_missing}")
    elif level != "full":
        errors.append(f"sandbox {level} missing {missing}, expected full")
    for name, kind, verdict, detail in probes:
        print(f"sandboxed {name:16} {kind:8} {verdict} {detail}")
        # "local": a listener only the sandboxed process itself can reach (Windows'
        # AppContainer loopback); no traffic leaves, as the canary's count proves
        if verdict != "DENIED" and kind != "local":
            errors.append(f"sandboxed probe {name} was {verdict}: {detail}")
    if args.canary_after_sandboxed != args.canary_before:
        errors.append(
            f"the canary counted {args.canary_after_sandboxed - args.canary_before} "
            "connections from the sandboxed host (N8: must be 0)"
        )
    if args.control:
        _, _, control = parse(Path(args.control).read_text())
        if not control:
            errors.append("the control run printed no probes")
        for name, kind, verdict, detail in control:
            print(f"control   {name:16} {kind:8} {verdict} {detail}")
            if kind in ("network", "file") and verdict != "ALLOWED":
                errors.append(f"control probe {name} was {verdict} without the sandbox: the probe is broken ({detail})")
        if args.canary_after_control <= args.canary_after_sandboxed:
            errors.append("the canary counted nothing from the control run: the canary or the probes are broken")
    return errors


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--sandboxed", required=True)
    ap.add_argument("--control")
    ap.add_argument("--canary-before", type=int, required=True)
    ap.add_argument("--canary-after-sandboxed", type=int, required=True)
    ap.add_argument("--canary-after-control", type=int, default=0)
    ap.add_argument("--expect-missing", default="")
    errors = check(ap.parse_args())
    for e in errors:
        print(f"::error::{e}")
    sys.exit(1 if errors else 0)


if __name__ == "__main__":
    main()
