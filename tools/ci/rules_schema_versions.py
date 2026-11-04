#!/usr/bin/env python3
"""A rules section that removes a field or changes its kind bumps its major version (Epic 51,
story 51.13).

Compares api/internal/handlers/rules/schema.json with the same file at the last release tag
(``git describe --tags --abbrev=0 --match 'v*'``, or ``--base REF``). For every section in
both whose major version did not rise: each field of the tag's schema must still be there
with the same kind (and, for a list of rows, the same column kinds). A section's version
missing from the tag's schema is 1.0. A major that went down fails too.

    python3 tools/ci/rules_schema_versions.py [--base REF]

Exit 0 when every section passes (or there is no tag to compare with), 1 otherwise, naming
each section and field.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parents[2]
SCHEMA = "api/internal/handlers/rules/schema.json"


def _version(section: dict[str, Any]) -> tuple[int, int]:
    v = section.get("version") or [1, 0]
    return (int(v[0]), int(v[1]))


def _shape(field: dict[str, Any]) -> tuple[str, tuple[str, ...]]:
    return (str(field.get("kind")), tuple(field.get("column_kinds") or ()))


def check(old: dict[str, Any], new: dict[str, Any]) -> list[str]:
    """The problems of ``new`` against ``old`` (two schema.json documents)."""
    problems: list[str] = []
    before = {s["key"]: s for s in old.get("sections", [])}
    for sec in new.get("sections", []):
        key = sec["key"]
        prev = before.get(key)
        if prev is None:
            continue
        (om, on), (nm, nn) = _version(prev), _version(sec)
        if nm < om:
            problems.append(f"rules.{key}: version went down from {om}.{on} to {nm}.{nn}")
            continue
        if nm > om:
            continue
        fields = {f["name"]: f for f in sec.get("fields", [])}
        for f in prev.get("fields", []):
            name = f["name"]
            now = fields.get(name)
            if now is None:
                problems.append(
                    f"rules.{key}.{name}: removed without a major bump (still {nm}.{nn});"
                    f" bump SCHEMA_VERSION to ({nm + 1}, 0) and register migrate_v{nm}_to_v{nm + 1}"
                )
                continue
            was, is_ = _shape(f), _shape(now)
            if was != is_:
                problems.append(
                    f"rules.{key}.{name}: kind changed from {_text(was)} to {_text(is_)} without"
                    f" a major bump (still {nm}.{nn}); bump SCHEMA_VERSION to ({nm + 1}, 0) and"
                    f" register migrate_v{nm}_to_v{nm + 1}"
                )
    return problems


def _text(shape: tuple[str, tuple[str, ...]]) -> str:
    kind, cols = shape
    return f"{kind}[{', '.join(cols)}]" if cols else kind


def last_tag(root: Path) -> str:
    out = subprocess.run(
        ["git", "describe", "--tags", "--abbrev=0", "--match", "v*"],
        cwd=root,
        capture_output=True,
        text=True,
        check=False,
    )
    return out.stdout.strip() if out.returncode == 0 else ""


def schema_at(root: Path, ref: str) -> dict[str, Any] | None:
    out = subprocess.run(
        ["git", "show", f"{ref}:{SCHEMA}"], cwd=root, capture_output=True, check=False
    )
    if out.returncode != 0:
        return None
    doc: dict[str, Any] = json.loads(out.stdout)
    return doc


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--base", help="the ref to compare with (default: the last v* tag)")
    p.add_argument("--root", type=Path, default=ROOT)
    a = p.parse_args(argv)
    base = a.base or last_tag(a.root)
    if not base:
        print("rules schema versions: no release tag to compare with")
        return 0
    old = schema_at(a.root, base)
    if old is None:
        print(f"rules schema versions: {SCHEMA} is not at {base}")
        return 0
    new = json.loads((a.root / SCHEMA).read_text(encoding="utf-8"))
    problems = check(old, new)
    for line in problems:
        print(f"::error file={SCHEMA}::{line}")
    if not problems:
        print(f"rules schema versions: every section fits its version (against {base})")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
