#!/usr/bin/env python3
"""mdns_copies.py: the two copies of the mDNS announcement stay the same.

api/internal/discovery and cmd/maktaba-server/internal/mdns are packages of two Go modules
that announce the same way (announce*.go, host*.go): a fix made in one copy and not the
other ships one module with the bug. Every file of either folder is in both, the same but
for its `package` line, unless it is one module's own (OWN). Run by the mdns-linux job
(_unit.yml); its tests are tools/ci/tests/test_mdns_copies.py.

    python3 tools/ci/mdns_copies.py [repo root]
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

COPIES = ("api/internal/discovery", "cmd/maktaba-server/internal/mdns")

# each module's own files: what the module does with the announcement
OWN = {
    COPIES[0]: {
        "discovery.go",
        "discovery_test.go",
        "mdns.go",
        "mdns_test.go",
        "sqlstore.go",
        "sqlstore_test.go",
    },
    COPIES[1]: {"machine.go", "mdns.go", "mdns_test.go"},
}

_PACKAGE = re.compile(r"^package \w+$", re.M)


def _body(text: str) -> str:
    """The file without its package clause (the one line the copies differ in)."""
    return _PACKAGE.sub("package _", text, count=1)


def problems(root: Path) -> list[str]:
    """What differs between the copies under root ([] when they are the same)."""
    out: list[str] = []
    a, b = (root / c for c in COPIES)
    files = {
        c: {p.name for p in (root / c).iterdir() if p.is_file()} - OWN[c] for c in COPIES
    }
    for name in sorted(files[COPIES[0]] ^ files[COPIES[1]]):
        has = COPIES[0] if name in files[COPIES[0]] else COPIES[1]
        other = COPIES[1] if has == COPIES[0] else COPIES[0]
        out.append(
            f"{has}/{name} has no copy in {other} "
            "(copy it, or list it in tools/ci/mdns_copies.py OWN)"
        )
    for name in sorted(files[COPIES[0]] & files[COPIES[1]]):
        if _body((a / name).read_text()) != _body((b / name).read_text()):
            out.append(f"{COPIES[0]}/{name} and {COPIES[1]}/{name} differ")
    return out


def main(argv: list[str]) -> int:
    root = Path(argv[1]) if len(argv) > 1 else Path(__file__).resolve().parents[2]
    found = problems(root)
    for p in found:
        print(f"::error::{p}")
    if not found:
        print(f"{COPIES[0]} and {COPIES[1]} are the same")
    return 1 if found else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
