"""Queue PROBE for videos that were never probed.

Streaming plays only a probed video (its ``media_info`` row). The scanner queues
PROBE when it first finds a file, but a library brought in another way (the
media-intel import) has none: every such video answers "streaming unavailable".
This queues one PROBE per video with no ``media_info`` row and no open probe job:

1. watched videos, the most watched first,
2. the rest, the newest file (``mtime``) first.

Probing an already-transcribed video records its media facts only; it does not
queue audio extraction again (``commit_probe``). Lower priority numbers and, within
one, lower job ids are claimed first, so the order here is the order they run.
A dry run (the default) prints the counts.

    python -m maktaba_pipeline.tools.queue_probe --database-url URL [--apply] [--limit N]
"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys
from dataclasses import dataclass

from ..engine.sql import dumps, is_pg
from ..runtime import Database

__all__ = ["TIERS", "ProbeQueueReport", "queue_probe"]

_WATCHES = (
    "((SELECT count(*) FROM playback_state ps WHERE ps.video_id = v.id)"
    " + (SELECT count(*) FROM watch_sessions ws WHERE ws.video_id = v.id))"
)

#: (priority, name, condition on ``v``, order)
TIERS: tuple[tuple[int, str, str, str], ...] = (
    (90, "watched, most first", f"{_WATCHES} > 0", f"{_WATCHES} DESC, v.id"),
    (100, "the rest, newest first", f"{_WATCHES} = 0", "v.mtime DESC, v.id"),
)

_OPEN = "('pending','claimed','running','resuming','paused')"


@dataclass
class ProbeQueueReport:
    tiers: list[tuple[str, int]]
    applied: bool

    @property
    def total(self) -> int:
        return sum(n for _, n in self.tiers)


async def queue_probe(
    db: Database, *, apply: bool = False, limit: int | None = None
) -> ProbeQueueReport:
    report = ProbeQueueReport(tiers=[], applied=apply)
    left = limit
    for priority, name, cond, order in TIERS:
        if left is not None and left <= 0:
            report.tiers.append((name, 0))
            continue
        where = (
            f"v.deleted_at IS NULL AND {cond}"
            " AND NOT EXISTS (SELECT 1 FROM media_info mi WHERE mi.video_id = v.id)"
            " AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.subject_kind = 'video'"
            f"  AND j.subject_id = v.id AND j.stage = 'probe' AND j.state IN {_OPEN})"
        )
        cap = f" LIMIT {int(left)}" if left is not None else ""
        if not apply:
            n = int(
                await db.fetchval(
                    f"SELECT count(*) FROM (SELECT v.id FROM videos v WHERE {where}{cap}) x"
                )
            )
        else:
            why = dumps(
                [{"why": f"probe the library: {name}", "by": "user", "from_stage": "queue"}]
            )
            count = "SELECT count(*) FROM processing_jobs WHERE stage = 'probe'"
            before = int(await db.fetchval(count))
            # rows are inserted in this order, so their ids (the claim tie-break) follow it
            await db.execute(
                "INSERT INTO processing_jobs (subject_kind, subject_id, video_id, library_id,"
                " stage, state, priority, max_attempts, reasons)"
                f" SELECT 'video', v.id, v.id, v.library_id, 'probe', 'pending', {priority},"
                f" 3, $1 FROM videos v WHERE {where} ORDER BY {order}{cap}",
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
    ap = argparse.ArgumentParser(prog="queue_probe", description=__doc__)
    ap.add_argument("--database-url", default=os.environ.get("DATABASE_URL", ""))
    ap.add_argument("--apply", action="store_true", help="queue (default: count only)")
    ap.add_argument("--limit", type=int, default=None, help="queue at most N")
    args = ap.parse_args(argv)
    if not args.database_url:
        ap.error("--database-url (or DATABASE_URL) is required")

    async def run() -> ProbeQueueReport:
        db = await Database.connect(args.database_url)
        try:
            return await queue_probe(db, apply=args.apply, limit=args.limit)
        finally:
            await db.close()

    report = asyncio.run(run())
    verb = "queued" if report.applied else "would queue (dry run; pass --apply)"
    for name, n in report.tiers:
        sys.stdout.write(f"  {name:28} {n:>8}\n")
    sys.stdout.write(f"{verb}: {report.total} probe jobs\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
