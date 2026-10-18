"""Queue the library for transcription (plan 03-10).

New videos reach ``transcribe`` by the graph (``content_hash -> transcribe``); this
queues what is already there: every video with no active transcript and no open
``transcribe`` job, in one statement per tier (a library has ~150k videos):

1. on Live TV in the next 48 h (what is about to air),
2. an episode of a detected series,
3. the rest.

Lower priority numbers are claimed first. A dry run (the default) prints the counts.

    python -m maktaba_pipeline.tools.queue_transcription --database-url URL [--apply]
        [--library NAME_OR_ID ...] [--limit N]
"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys
from dataclasses import dataclass
from typing import Any

from ..engine.sql import dumps, is_pg
from ..runtime import Database

__all__ = ["TIERS", "QueueReport", "queue_transcription"]

#: (priority, name, the SQL condition on ``v``)
TIERS: tuple[tuple[int, str, str], ...] = (
    (
        10,
        "on Live TV in the next 48 h",
        "EXISTS (SELECT 1 FROM channel_programs p WHERE p.video_id = v.id"
        " AND p.end_at > {now} AND p.start_at < {soon})",
    ),
    (20, "series episodes", "EXISTS (SELECT 1 FROM series_episodes e WHERE e.video_id = v.id)"),
    (30, "the rest", "1 = 1"),
)

_OPEN = "('pending','claimed','running','resuming','paused')"


@dataclass
class QueueReport:
    tiers: list[tuple[str, int]]
    applied: bool

    @property
    def total(self) -> int:
        return sum(n for _, n in self.tiers)


async def _library_ids(db: Database, names: list[str]) -> list[str]:
    out = []
    for n in names:
        row = await db.fetchrow(
            "SELECT id FROM libraries WHERE CAST(id AS TEXT) = $1 OR name = $1", n
        )
        if row is None:
            raise SystemExit(f"no library {n!r}")
        out.append(str(row["id"]))
    return out


async def queue_transcription(
    db: Database,
    *,
    apply: bool = False,
    libraries: list[str] | None = None,
    limit: int | None = None,
) -> QueueReport:
    from datetime import UTC, datetime, timedelta

    from ..engine.sql import to_ts

    now = datetime.now(UTC)
    lib_ids = await _library_ids(db, libraries or [])
    report = QueueReport(tiers=[], applied=apply)
    left = limit
    for priority, name, cond in TIERS:
        if left is not None and left <= 0:
            report.tiers.append((name, 0))
            continue
        args: list[Any] = []

        def param(value: Any, args: list[Any] = args) -> str:
            args.append(value)
            return f"${len(args)}"

        def bind(c: str, param: Any = param) -> str:
            if "{now}" not in c:
                return c
            return c.format(
                now=param(to_ts(db, now)), soon=param(to_ts(db, now + timedelta(hours=48)))
            )

        # a video belongs to the first tier it matches
        earlier = [bind(c) for p_, _, c in TIERS if p_ < priority]
        cond = " AND ".join([bind(cond), *(f"NOT ({c})" for c in earlier)])
        lib_sql = ""
        if lib_ids:
            marks = ",".join(param(i) for i in lib_ids)
            lib_sql = f" AND CAST(v.library_id AS TEXT) IN ({marks})"
        where = (
            "v.deleted_at IS NULL AND COALESCE(v.duration_sec, 0) > 0"
            f" AND {cond}{lib_sql}"
            " AND NOT EXISTS (SELECT 1 FROM transcripts t WHERE t.video_id = v.id"
            "  AND t.is_active)"
            " AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.subject_kind = 'video'"
            f"  AND j.subject_id = v.id AND j.stage = 'transcribe' AND j.state IN {_OPEN})"
        )
        cap = f" ORDER BY v.id LIMIT {int(left)}" if left is not None else ""
        if not apply:
            n = int(
                await db.fetchval(
                    f"SELECT count(*) FROM (SELECT v.id FROM videos v WHERE {where}{cap}) x",
                    *args,
                )
            )
        else:
            why = dumps(
                [{"why": f"transcribe the library: {name}", "by": "user", "from_stage": "queue"}]
            )
            count = "SELECT count(*) FROM processing_jobs WHERE stage = 'transcribe'"
            before = int(await db.fetchval(count))
            await db.execute(
                "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id,"
                " stage, state, priority, max_attempts, reasons)"
                f" SELECT 'video', v.id, v.id, v.library_id, 'transcribe', 'pending', {priority},"
                f" 3, ${len(args) + 1} FROM videos v WHERE {where}{cap}",
                *args,
                why,
            )
            n = int(await db.fetchval(count)) - before
        report.tiers.append((name, n))
        if left is not None:
            left -= n
    if apply and is_pg(db):
        await db.execute("ANALYZE processing_jobs")
    return report


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="queue_transcription", description=__doc__)
    ap.add_argument("--database-url", default=os.environ.get("DATABASE_URL", ""))
    ap.add_argument("--apply", action="store_true", help="queue (default: count only)")
    ap.add_argument("--library", action="append", default=[], help="name or id (repeatable)")
    ap.add_argument("--limit", type=int, default=None, help="at most this many videos")
    a = ap.parse_args(argv)
    if not a.database_url:
        ap.error("--database-url (or DATABASE_URL) is required")

    async def run() -> QueueReport:
        db = await Database.connect(a.database_url)
        try:
            return await queue_transcription(db, apply=a.apply, libraries=a.library, limit=a.limit)
        finally:
            await db.close()

    r = asyncio.run(run())
    verb = "queued" if r.applied else "would queue (dry run; pass --apply)"
    for name, n in r.tiers:
        sys.stdout.write(f"  {name:32} {n:>8}\n")
    sys.stdout.write(f"{verb}: {r.total} videos\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
