"""``youtube_tartube`` (catalog subject): every library's YouTube sources from Tartube's
records, off, and what Tartube downloaded seeded done
(:mod:`...library_mgmt.tartube_sources`). An admin runs it from Admin > Libraries
(``POST /api/queues/youtube-tartube``, payload ``{"tartube": {"path": ..., "database":
...}}``, paths in the database's form); its report is the job's summary."""

from __future__ import annotations

import os
import pickle
from typing import Any

from ...db.jobs import Job
from ...library_mgmt import tartube_import, tartube_sources
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    spec = (job.payload or {}).get("tartube")
    if not isinstance(spec, dict) or not isinstance(spec.get("path"), str):
        raise StageFailure("tartube_bad_payload", "tartube needs a path", retryable=False)
    path = ctx.local_path(str(spec["path"]))
    db_path = spec.get("database")
    database = ctx.local_path(db_path) if isinstance(db_path, str) and db_path else None
    if not os.path.isabs(path) or not os.path.isfile(path):
        raise StageFailure(
            "tartube_missing", f"no Tartube export or database at {path}", retryable=False
        )
    try:
        out = await tartube_sources.import_sources(
            db, path, database=database, to_local=ctx.local_path
        )
    except tartube_import.TartubeRefused as exc:
        raise StageFailure("tartube_refused", str(exc), retryable=False) from exc
    except (OSError, ValueError, EOFError, pickle.UnpicklingError) as exc:
        raise StageFailure(
            "tartube_unreadable", f"{path}: {type(exc).__name__}: {exc}", retryable=False
        ) from exc
    c = out["counts"]
    await ctx.log(
        "info",
        f"Tartube: {c['created']} sources added (off), {c['existing']} already there,"
        f" {c['skipped']} skipped, {c['not_in_maktaba']} not in Maktaba, {c['seeded']} seeded",
        c,
    )
    return StageResult(output_hash=canonical_hash(["youtube_tartube", out]), summary=out)


IMPL = StageImpl(run=run)
