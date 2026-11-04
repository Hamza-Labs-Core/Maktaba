"""Every library's YouTube sources from its linked videos, by hand (slot 0182).

::

    python -m maktaba_pipeline.tools.youtube_derive [--database-url URL] [--library ID ...]
                                                    [--apply] [--json]

The reconciler's pass does this by itself (:mod:`..engine.reconcile`, step 9), and the
``link`` stage for each new link. This prints the decision table: for each library, what
its linked videos say its sources are and why (:mod:`..library_mgmt.youtube_derive`).
Without ``--apply`` it only reads (a dry run); with it, the new sources are made, off,
and the decisions kept. No source is ever turned on, and no existing source changes.
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import sys
from typing import Any

from ..library_mgmt import youtube_derive

__all__ = ["main", "render"]


def render(rows: list[dict[str, Any]], names: dict[str, str], *, applied: bool) -> str:
    out = ["APPLIED" if applied else "DRY RUN (nothing written; pass --apply)"]
    counts: dict[str, int] = {}
    for r in rows:
        counts[r["outcome"]] = counts.get(r["outcome"], 0) + 1
        lib = names.get(r["library_id"], r["library_id"])
        ids = ", ".join(r["external_ids"]) or "-"
        line = f"{lib}: {r['outcome']} {r['kind'] or ''} {ids} ({r['reason']})"
        if r["clash_external_id"]:
            holder = names.get(r["clash_library_id"] or "", r["clash_library_id"])
            line += f" [clash: {r['clash_external_id']} is {holder}'s]"
        if r["created"]:
            line += f" [made off: {', '.join(r['created'])}]"
        out.append(line)
    out.append("outcomes: " + ", ".join(f"{k} {v}" for k, v in sorted(counts.items())))
    return "\n".join(out)


def _parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="python -m maktaba_pipeline.tools.youtube_derive")
    p.add_argument("--database-url", default=os.environ.get("MAKTABA_DATABASE_URL"))
    p.add_argument("--library", action="append", default=None, help="only this library id")
    p.add_argument("--apply", action="store_true", help="make the sources (off) and keep it")
    p.add_argument("--json", action="store_true", help="one JSON object per library")
    return p


async def _amain(args: argparse.Namespace) -> int:
    from ..runtime import Database

    if not args.database_url:
        sys.stderr.write("set --database-url or MAKTABA_DATABASE_URL\n")
        return 2
    db = await Database.connect(args.database_url)
    try:
        decisions = await youtube_derive.derive(db, args.library, apply=args.apply)
        names = {
            str(r["id"]): str(r["name"]) for r in await db.fetch("SELECT id, name FROM libraries")
        }
    finally:
        await db.close()
    rows = [d.row() for d in decisions]
    if args.json:
        sys.stdout.write("".join(json.dumps(r, ensure_ascii=False) + "\n" for r in rows))
    else:
        sys.stdout.write(render(rows, names, applied=args.apply) + "\n")
    return 0


def main(argv: list[str] | None = None) -> int:
    return asyncio.run(_amain(_parser().parse_args(argv)))


if __name__ == "__main__":
    raise SystemExit(main())
