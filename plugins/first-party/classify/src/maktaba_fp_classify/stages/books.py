"""``books`` (tv.maktaba.classify, Epic 54 wave 4, story 54.31): the book a lesson series'
name names corrects the season's topics.

Runs in the ``net`` runner group: the one classify stage that reaches the internet, the
encyclopedia at ``rules.books.wiki_api`` (``ar.wikipedia.org``, the manifest's ``net``). The
other classify stages stay in ``light``, which has no socket. The cache, the match and the
fingerprint are the pipeline's (``engine/stages/_books.py``; mediaintel's import uses them).
"""

from __future__ import annotations

import asyncio
import urllib.error
from typing import Any
from uuid import UUID

from maktaba_pipeline import offline
from maktaba_pipeline.classify import books as bk
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages._books import (
    apply_book,
    book_ref,
    cached_book,
    input_fingerprint,
    save_book,
)
from maktaba_pipeline.engine.stages._tagging import season_tags_digest
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL", "run"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    season_id: UUID = job.subject.id
    cfg: bk.BooksConfig = await ctx.rules("books")
    got = await book_ref(db, season_id)
    if got is None:
        return StageResult(output_hash=canonical_hash("retired"), summary={"book": None})
    library, ref = got
    if not cfg.enabled or ref is None:  # off, or the name names no book
        return StageResult(
            output_hash=await season_tags_digest(db, season_id),
            summary={"enabled": cfg.enabled, "book": None},
        )
    book = await cached_book(db, ref.name, cfg)
    looked_up = book is None
    if book is None and offline.offline():
        # offline (#640): no lookup; the fingerprint says so, so it runs again once online
        return StageResult(
            output_hash=await season_tags_digest(db, season_id),
            summary={"enabled": cfg.enabled, "book": None, "offline": True},
        )
    if book is None:
        try:
            book = await asyncio.to_thread(bk.lookup, ref.name, cfg)
        except (OSError, urllib.error.URLError, ValueError, KeyError) as e:
            raise StageFailure(
                "encyclopedia_unavailable", f"{ref.name}: the lookup failed ({e})"
            ) from e
        await save_book(db, book)  # a lookup is slow: never repeat it
        await asyncio.sleep(cfg.request_gap_s)
    corrected = await apply_book(db, season_id, library, ref, book, cfg)
    return StageResult(
        output_hash=await season_tags_digest(db, season_id),
        summary={
            "book": book.title or None,
            "looked_up": looked_up,
            "found": book.found,
            "corrected": corrected,
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
