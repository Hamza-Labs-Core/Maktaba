"""``review_pages``: one HTML page per library to review its seasons by eye.

Ported from mediaintel's ``report`` step, which runs on its own thread so the
Plex and dizqueTV writes never wait for it. Here that independence is structural:
the stage has its own pool (``reports``, one at a time), nothing depends on it
(no edges out), and it reads only the database and the small thumbnails
``thumb_small`` made. A slow page never holds a ``cpu`` or ``network_*`` slot.

The page is written to the app's media cache next to the thumbnails,
``~/.maktaba/cache/reviews/<library_id>.html``, and links each episode's
``../thumbnails/<content_hash>/small.jpg`` by a relative path, so the folder can be
opened or served as it is. Both caches are per host: the stage and
``thumb_small`` should run on the same worker (the default single-host setup).
"""

from __future__ import annotations

import html
import os
from collections import defaultdict
from pathlib import Path
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ..context import StageContext
from ..graph import REVIEW_VERSION, StageResult, Subject
from ..hashing import canonical_hash
from ..sql import uid
from . import thumb_small
from .registry import StageImpl

__all__ = ["IMPL", "REVIEW_VERSION", "page_path", "render"]


def reviews_dir() -> Path:
    """``reviews/`` beside the thumbnail cache (``~/.maktaba/cache/reviews``)."""
    return thumb_small.small_path("x").parent.parent.parent / "reviews"


def page_path(library_id: UUID | str) -> Path:
    return reviews_dir() / f"{library_id}.html"


async def _seasons(db: Any, library_id: UUID) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT ss.id AS id, s.name AS series, COALESCE(s.name_override, s.name) AS shown,"
            " ss.season AS season, ss.kind AS kind, ss.name AS name, ss.first_air AS first_air,"
            " ss.last_air AS last_air, ss.membership_hash AS membership"
            " FROM series_seasons ss JOIN series s ON s.id = ss.series_id"
            " WHERE ss.library_id = $1 AND ss.retired_at IS NULL"
            " ORDER BY s.name, ss.season",
            uid(library_id),
        )
    )


async def _episodes(db: Any, library_id: UUID) -> dict[str, list[Any]]:
    rows = await db.fetch(
        "SELECT ss.id AS season_id, v.id AS video_id, v.content_hash AS content_hash,"
        " COALESCE(v.title, v.filename) AS title, v.duration_sec AS duration,"
        " COALESCE(e.episode_override, e.episode) AS episode"
        " FROM series_episodes e"
        " JOIN series_seasons ss ON ss.series_id = e.series_id"
        "  AND ss.season = COALESCE(e.season_override, e.season)"
        " JOIN videos v ON v.id = e.video_id"
        " WHERE ss.library_id = $1 AND ss.retired_at IS NULL AND v.deleted_at IS NULL"
        " ORDER BY ss.id, COALESCE(e.episode_override, e.episode), v.filename",
        uid(library_id),
    )
    out: dict[str, list[Any]] = defaultdict(list)
    for r in rows:
        out[str(r["season_id"])].append(r)
    return out


def _mmss(sec: Any) -> str:
    if sec is None:
        return ""
    s = int(float(sec))
    return (
        f"{s // 3600}:{s // 60 % 60:02d}:{s % 60:02d}" if s >= 3600 else f"{s // 60}:{s % 60:02d}"
    )


_CSS = """
:root{--bg:#f7f6f2;--fg:#1d1c19;--muted:#6b675e;--card:#fff;--line:#e3e0d8;--accent:#2f6f5e}
@media (prefers-color-scheme:dark){:root{--bg:#151513;--fg:#ecebe6;--muted:#a19d93;
--card:#1f1e1b;--line:#33312c;--accent:#7cc4ad}}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 system-ui,sans-serif;
padding:16px max(16px,env(safe-area-inset-right)) 32px max(16px,env(safe-area-inset-left))}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 4px}
.meta{color:var(--muted);font-size:13px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(160px,1fr));gap:10px;margin-top:10px}
.ep{background:var(--card);border:1px solid var(--line);border-radius:8px;overflow:hidden}
.ep img,.ep .none{display:block;width:100%;aspect-ratio:16/9;object-fit:cover;
background:var(--line)}
.ep p{margin:6px 8px 8px;font-size:12.5px}.ep b{color:var(--accent);font-weight:600}
"""


def render(
    library: str, seasons: list[Any], episodes: dict[str, list[Any]], thumbs: dict[str, str]
) -> str:
    """The page. ``thumbs`` maps a content hash to the relative link of its small copy."""
    e = html.escape
    body: list[str] = []
    total = 0
    for s in seasons:
        eps = episodes.get(str(s["id"]), [])
        total += len(eps)
        span = " – ".join(str(d) for d in (s["first_air"], s["last_air"]) if d)
        cards = []
        for ep in eps:
            link = thumbs.get(str(ep["content_hash"]))
            img = (f'<img loading="lazy" src="{e(link)}" alt="">' if link
                   else '<div class="none" aria-hidden="true"></div>')  # fmt: skip
            num = f"<b>{ep['episode']}</b> · " if ep["episode"] is not None else ""
            meta = f'<span class="meta">{_mmss(ep["duration"])}</span>'
            cards.append(
                f'<div class="ep">{img}<p dir="auto">{num}{e(str(ep["title"]))} {meta}</p></div>'
            )
        body.append(
            f'<section><h2 dir="auto">{e(str(s["name"]))}</h2>'
            f'<div class="meta">{e(str(s["kind"]))} · {len(eps)} episodes'
            f"{' · ' + e(span) if span else ''}</div>"
            f'<div class="grid">{"".join(cards)}</div></section>'
        )
    return (
        '<!doctype html><html lang="ar" dir="auto"><head><meta charset="utf-8">'
        '<meta name="viewport" content="width=device-width,initial-scale=1">'
        f"<title>{e(library)} · review</title><style>{_CSS}</style></head><body>"
        f'<h1 dir="auto">{e(library)}</h1>'
        f'<div class="meta">{len(seasons)} seasons · {total} episodes</div>'
        f"{''.join(body)}</body></html>"
    )


async def input_fingerprint(db: Any, subject: Subject) -> str | None:
    """DB only: the library's seasons (membership and names) plus the small thumbnails
    made so far, so a page is rewritten when either moves and skipped otherwise."""
    lib = await db.fetchrow("SELECT name FROM libraries WHERE id = $1", uid(subject.id))
    if lib is None:
        return None
    seasons = [
        (str(s["id"]), str(s["name"]), str(s["membership"])) for s in await _seasons(db, subject.id)
    ]
    thumbs = await db.fetchrow(
        "SELECT count(*) AS n, max(id) AS last FROM processing_jobs"
        " WHERE stage = 'thumb_small' AND state = 'done' AND library_id = $1",
        uid(subject.id),
    )
    return canonical_hash(
        {
            "library": str(lib["name"]),
            "seasons": seasons,
            "thumbs": [
                int(thumbs["n"] or 0),
                None if thumbs["last"] is None else int(thumbs["last"]),
            ],
            "v": REVIEW_VERSION,
        }
    )


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    library_id: UUID = job.subject.id
    lib = await db.fetchrow("SELECT name FROM libraries WHERE id = $1", uid(library_id))
    if lib is None:
        return StageResult(output_hash=canonical_hash({"library": None}), summary={"gone": True})
    seasons = await _seasons(db, library_id)
    episodes = await _episodes(db, library_id)
    out = page_path(library_id)
    thumbs: dict[str, str] = {}
    n = 0
    for eps in episodes.values():
        for ep in eps:
            h = str(ep["content_hash"])
            p = thumb_small.small_path(h)
            if p.is_file():
                thumbs[h] = Path(os.path.relpath(p, out.parent)).as_posix()
            n += 1
    await ctx.progress("render", 1, 2, f"{len(seasons)} seasons, {n} episodes", force=True)
    page = render(str(lib["name"]), seasons, episodes, thumbs)
    out.parent.mkdir(parents=True, exist_ok=True)
    tmp = out.with_suffix(".part.html")
    tmp.write_text(page, encoding="utf-8")
    tmp.replace(out)
    return StageResult(
        output_hash=canonical_hash(page),
        summary={
            "page": str(out),
            "seasons": len(seasons),
            "episodes": n,
            "with_thumbnail": len(thumbs),
        },
    )


IMPL = StageImpl(run=run, input_fingerprint=input_fingerprint)
