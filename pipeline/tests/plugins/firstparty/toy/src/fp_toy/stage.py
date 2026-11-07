"""``fp_toy``: one row per video of the library, the way a first-party stage is written.

The plugin path can be made to differ on purpose (``PLANT``), for the parity proof's
self-tests (story 54.26 T01-T04): a row, a fingerprint, a slowdown.
"""

from __future__ import annotations

import asyncio
from typing import Any

from maktaba_pipeline.engine.graph import StageResult, Subject
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.sql import uid
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.rules import read as read_rules

PLANT: dict[str, Any] = {"row": False, "fingerprint": False, "slow": 0.0}


def _in_plugin(db: Any) -> bool:
    return type(db).__name__ == "StatementDB"


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    rows = await db.fetch(
        "SELECT id, size_bytes FROM videos WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id",
        uid(subject.id),
    )
    extra = 1 if PLANT["fingerprint"] and _in_plugin(db) else 0
    return canonical_hash([[str(r["id"]), int(r["size_bytes"]) + extra] for r in rows])


async def run(db: Any, job: Any, ctx: Any) -> StageResult:
    library = job.subject.id
    if PLANT["slow"] and _in_plugin(db):
        await asyncio.sleep(PLANT["slow"])
    cfg = (await read_rules(db, "groups"))[0]
    rows = await db.fetch(
        "SELECT id, title, size_bytes FROM videos WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id",
        uid(library),
    )
    await ctx.progress("count", 0, len(rows))
    async with db.transaction():
        await db.execute("DELETE FROM fp_toy_out WHERE library_id = $1", uid(library))
        for r in rows:
            n = int(r["size_bytes"]) + (1 if PLANT["row"] and _in_plugin(db) else 0)
            await db.execute(
                "INSERT INTO fp_toy_out (library_id, video_id, title, n) VALUES ($1, $2, $3, $4)",
                uid(library),
                uid(r["id"]),
                r["title"],
                n,
            )
    await ctx.log("info", f"toy wrote {len(rows)} rows")
    return StageResult(
        output_hash=canonical_hash([[str(r["id"]), int(r["size_bytes"])] for r in rows]),
        summary={"rows": len(rows), "promo_words": len(getattr(cfg, "promo_words", ()) or ())},
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
