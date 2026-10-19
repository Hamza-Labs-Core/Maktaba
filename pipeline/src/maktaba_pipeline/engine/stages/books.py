"""``books``: the book behind a lesson series corrects its topics (mediaintel ``books``).

Subject: a season. Runs after ``check_tags``. A series whose name names a book («شرح بلوغ
المرام», «كتاب الصيام من بلوغ المرام»): the book is looked up once in the encyclopedia (cached
per name in ``books``) and its subject corrects the season's tags
(:func:`maktaba_pipeline.classify.books.apply`); the match is kept in ``series_books``. A book
not found is searched again after ``retry_missing_days``.

Settings: ``rules.books`` (off, encyclopedia, matching, scores, ...).
"""

from __future__ import annotations

import asyncio
import json
import time
import urllib.error
from typing import Any
from uuid import UUID

from ...classify import books as bk
from ...classify.topic_tags import branding, chapter_tags
from ...db.jobs import Job
from ...rules import read as read_rules
from ...settings import to_json
from ..context import StageContext, StageFailure
from ..graph import StageResult, Subject
from ..hashing import canonical_hash
from ..sql import uid
from ._common import load_season
from ._leftovers import library_name
from ._tagging import latest_output, read_season_tags, season_tags_digest, write_season_tags
from .registry import StageImpl

__all__ = ["IMPL", "apply_book", "book_ref", "cached_book", "save_book"]


async def cached_book(db: Any, name: str, cfg: bk.BooksConfig) -> bk.Book | None:
    """The looked-up book, unless it is stale (another lookup version) or was not found
    last time and ``retry_missing_days`` have passed."""
    r = await db.fetchrow(
        "SELECT version, title, url, categories, summary, checked_at FROM books WHERE name = $1",
        name,
    )
    if r is None or int(r["version"]) != bk.BOOKS_VERSION:
        return None
    if not r["title"] and time.time() - float(r["checked_at"]) > cfg.retry_missing_days * 86400:
        return None  # not found last time: search again
    cats = tuple(json.loads(r["categories"]))
    return bk.Book(name, str(r["title"]), str(r["url"]), cats, str(r["summary"]))


async def save_book(db: Any, book: bk.Book, *, checked_at: float | None = None) -> None:
    await db.execute(
        "INSERT INTO books (name, version, title, url, categories, summary, checked_at)"
        " VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (name) DO UPDATE SET"
        " version = EXCLUDED.version, title = EXCLUDED.title, url = EXCLUDED.url,"
        " categories = EXCLUDED.categories, summary = EXCLUDED.summary,"
        " checked_at = EXCLUDED.checked_at",
        book.name,
        bk.BOOKS_VERSION,
        book.title,
        book.url,
        json.dumps(book.categories, ensure_ascii=False),
        book.summary,
        time.time() if checked_at is None else checked_at,
    )


async def apply_book(
    db: Any,
    season_id: UUID,
    library: str,
    ref: bk.BookRef,
    book: bk.Book,
    cfg: bk.BooksConfig,
) -> bool:
    """The book's subject onto the season's stored tags; records the match in
    ``series_books``. True: tags changed. A book not found, or with no known subject, does
    nothing."""
    if not book.found:
        return False
    fields = bk.fields(book.categories, cfg)
    if not fields:
        return False
    chapter = chapter_tags(ref.chapter, strip=branding([library])) if ref.chapter else {}
    await db.execute(
        "INSERT INTO series_books (season_id, name, chapter, fields) VALUES ($1,$2,$3,$4)"
        " ON CONFLICT (season_id) DO UPDATE SET name = EXCLUDED.name,"
        " chapter = EXCLUDED.chapter, fields = EXCLUDED.fields",
        uid(season_id),
        ref.name,
        ref.chapter,
        ",".join(fields),
    )
    tags = await read_season_tags(db, season_id)
    new = bk.apply(tags, fields, chapter, f"book «{book.title}»", cfg)
    if new == tags:
        return False
    await write_season_tags(db, season_id, new)
    return True


async def book_ref(db: Any, season_id: UUID) -> tuple[str, bk.BookRef | None] | None:
    """``(library name, the book the series' name names)`` of a live season, or None."""
    s = await load_season(db, season_id)
    if s is None or s.retired:
        return None
    library = (await library_name(db, s.library_id)) if s.library_id else ""
    return library or "", bk.book_of(s.name, strip=branding([library or ""]))


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the book the name names, the books settings and the newest ``tag`` and
    ``check_tags`` outputs of the season."""
    cfg: bk.BooksConfig = (await read_rules(db, "books"))[0]
    got = await book_ref(db, subject.id)
    if got is None:
        return canonical_hash("gone")
    if not cfg.enabled:
        return canonical_hash({"off": True, "v": bk.BOOKS_VERSION})
    library, ref = got
    return canonical_hash(
        {
            "book": None if ref is None else [ref.name, ref.chapter],
            "library": library,
            "rules": to_json(cfg),
            "tag": await latest_output(db, "season", subject.id, "tag"),
            "check_tags": await latest_output(db, "season", subject.id, "check_tags"),
            "v": bk.BOOKS_VERSION,
        }
    )


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
