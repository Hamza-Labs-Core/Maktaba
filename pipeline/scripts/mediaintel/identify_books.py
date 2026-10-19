"""Step ``books``: the book behind each lesson series corrects its topics.

Runs after ``check-tags``. For every series whose name names a book («شرح بلوغ المرام»,
«كتاب الصيام من بلوغ المرام»): the book is looked up once (cached in ``books`` per name)
and its subject corrects ``series_tags``
(:func:`maktaba_pipeline.classify.books.apply`). A book not found is searched again after
``retry_missing_days``.
"""

from __future__ import annotations

import json
import sqlite3
import time
import urllib.error

from maktaba_pipeline.classify import books as bk
from maktaba_pipeline.classify.topic_tags import branding, chapter_tags

SCHEMA = """
CREATE TABLE IF NOT EXISTS books (
    name TEXT PRIMARY KEY,
    version INTEGER NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    categories TEXT NOT NULL,
    summary TEXT NOT NULL,
    checked_at REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS series_books (
    series_id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    chapter TEXT NOT NULL,
    fields TEXT NOT NULL
);
"""


def _cached(conn: sqlite3.Connection, name: str, cfg: bk.BooksConfig) -> bk.Book | None:
    r = conn.execute(
        "SELECT version, title, url, categories, summary, checked_at FROM books WHERE name=?",
        (name,),
    ).fetchone()
    if r is None or r[0] != bk.BOOKS_VERSION:
        return None
    if not r[1] and time.time() - r[5] > cfg.retry_missing_days * 86400:
        return None  # not found last time: search again
    return bk.Book(name, r[1], r[2], tuple(json.loads(r[3])), r[4])


def run(conn: sqlite3.Connection, cfg: bk.BooksConfig | None = None) -> dict[str, int]:
    if cfg is None:
        from . import settings_store

        cfg = settings_store.get(conn, "books")
    counts = {"series": 0, "books": 0, "looked_up": 0, "found": 0, "corrected": 0, "failed": 0}
    if not cfg.enabled:
        print("  -  off (settings: books.enabled)")
        return counts
    conn.executescript(SCHEMA)
    series = conn.execute("SELECT id, name, library FROM series ORDER BY library, name").fetchall()
    refs: dict[str, tuple[bk.BookRef, str]] = {}
    for sid, name, library in series:
        ref = bk.book_of(name or "", strip=branding([library or ""]))
        if ref is not None:
            refs[sid] = (ref, library or "")
    names = sorted({r.name for r, _ in refs.values()})
    counts["series"], counts["books"] = len(refs), len(names)
    print(f"phase: looking up {len(names)} books named by {len(refs)} series", flush=True)
    found: dict[str, bk.Book] = {}
    in_a_row = 0  # failed lookups in a row: the encyclopedia is down or limiting us
    for n, name in enumerate(names, 1):
        book = _cached(conn, name, cfg)
        if book is None:
            try:
                book = bk.lookup(name, cfg)
            except (OSError, urllib.error.URLError, ValueError, KeyError) as e:
                counts["failed"] += 1
                in_a_row += 1
                if counts["failed"] <= 3:
                    print(f"  !  {name}: the lookup failed ({e})", flush=True)
                if in_a_row >= 5:
                    print("  !  the encyclopedia is not answering: stopping (books found so far "
                          "still correct their series; the rest next run)", flush=True)  # fmt: skip
                    break
                continue
            in_a_row = 0
            counts["looked_up"] += 1
            conn.execute(
                "INSERT OR REPLACE INTO books VALUES (?,?,?,?,?,?,?)",
                (name, bk.BOOKS_VERSION, book.title, book.url,
                 json.dumps(book.categories, ensure_ascii=False), book.summary, time.time()),
            )  # fmt: skip
            conn.commit()  # a lookup is slow: never repeat it after a stop
            time.sleep(cfg.request_gap_s)
        found[name] = book
        if n % 10 == 0 or n == len(names):
            conn.commit()
            print(f"  [{n}/{len(names)}] books: {sum(b.found for b in found.values())} found",
                  flush=True)  # fmt: skip
    print("phase: correcting the series' topics", flush=True)
    for sid, (ref, library) in refs.items():
        book = found.get(ref.name)
        if book is None or not book.found:
            continue
        fields = bk.fields(book.categories, cfg)
        if not fields:
            continue
        counts["found"] += 1
        chapter = chapter_tags(ref.chapter, strip=branding([library])) if ref.chapter else {}
        conn.execute("INSERT OR REPLACE INTO series_books VALUES (?,?,?,?)",
                     (sid, ref.name, ref.chapter, ",".join(fields)))  # fmt: skip
        tags = {
            t: (float(s), e or "")
            for t, s, e in conn.execute(
                "SELECT tag, score, evidence FROM series_tags WHERE series_id=?", (sid,)
            )
        }
        new = bk.apply(tags, fields, chapter, f"book «{book.title}»", cfg)
        if new != tags:
            conn.execute("DELETE FROM series_tags WHERE series_id=?", (sid,))
            conn.executemany(
                "INSERT INTO series_tags (series_id, tag, score, evidence) VALUES (?,?,?,?)",
                [(sid, t, s, e) for t, (s, e) in new.items()],
            )
            counts["corrected"] += 1
    conn.commit()
    print(
        f"  ✓  {counts['series']} series name {counts['books']} books: "
        f"{sum(b.found for b in found.values())} found ({counts['looked_up']} looked up now), "
        f"{counts['corrected']} series corrected, {counts['failed']} lookups failed"
    )
    return counts
