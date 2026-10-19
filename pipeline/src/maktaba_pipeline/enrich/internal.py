"""The internal suggestion: what Maktaba's own intel says, with no network at all.

Always made, so "Find metadata" never answers with nothing: at worst the title is the
file's name. Each field names the intel it rests on (``file``, ``youtube``, ``series``,
``book``, ``transcript`` ...) and a confidence that says how direct that intel is (a title
someone set in the library is surer than one read off a file name).
"""

from __future__ import annotations

from .intel import VideoIntel
from .proposal import Proposal, Source, clean_text

__all__ = ["INTEL_LABELS", "internal_proposal"]

INTEL_LABELS: dict[str, str] = {
    "file": "File and folder names",
    "library": "Library",
    "title": "Set in the library",
    "youtube": "YouTube listing",
    "series": "Series and sibling episodes",
    "book": "Book the series explains",
    "topics": "Topic tags",
    "speakers": "Speakers and people named",
    "chapters": "Chapters",
    "transcript": "Transcript",
    "collections": "Collections",
}


def _src(p: Proposal, kind: str) -> str:
    return p.add_source(Source(f"internal:{kind}", INTEL_LABELS[kind], kind=kind))


def internal_proposal(intel: VideoIntel, *, query: str = "") -> Proposal:
    p = Proposal("internal", meta={"query": query} if query else {})

    # title: what a person set, else the YouTube listing, else the parser's reading of
    # the file name, else the file name itself
    if intel.display_title or intel.title:
        p.set("title", intel.display_title or intel.title, 0.7, [_src(p, "title")])
    elif intel.yt_title:
        p.set("title", intel.yt_title, 0.65, [_src(p, "youtube")])
    elif intel.parsed_show:
        p.set("title", intel.parsed_show, 0.45, [_src(p, "file")])
    else:
        p.set("title", intel.best_title or str(intel.video_id), 0.3, [_src(p, "file")])

    if intel.series:
        s = _src(p, "series")
        p.set("series", intel.series, 0.8, [s])
        if intel.season is not None:
            p.set("season", intel.season, 0.8, [s])
        if intel.episode is not None:
            p.set("episode", intel.episode, 0.8, [s])
    elif intel.parsed_show and (intel.parsed_episode is not None):
        f = _src(p, "file")
        p.set("series", intel.parsed_show, 0.5, [f])
        if intel.parsed_season is not None:
            p.set("season", intel.parsed_season, 0.5, [f])
        p.set("episode", intel.parsed_episode, 0.5, [f])

    # the book behind a lesson series (books-are-sources)
    if intel.book is not None:
        b = _src(p, "book")
        sure = 0.75 if intel.book.via == "lookup" else 0.5
        p.set("book_title", intel.book.title or intel.book.name, sure, [b])
        if intel.book.subjects:
            p.set("book_subject", ", ".join(intel.book.subjects), sure, [b])
        if intel.book.chapter:
            p.set("book_chapters", [intel.book.chapter], sure, [b])

    # description: the book's summary, else the YouTube description, else the chapters
    # or the opening of the transcript
    desc_parts: list[str] = []
    desc_src: list[str] = []
    if intel.description:
        p.set("description", intel.description, 0.7, [_src(p, "title")])
    else:
        if intel.yt_description:
            desc_parts.append(clean_text(intel.yt_description, limit=900, keep_newlines=True))
            desc_src.append(_src(p, "youtube"))
        if intel.book is not None and intel.book.summary:
            desc_parts.append(clean_text(intel.book.summary, limit=600))
            desc_src.append(_src(p, "book"))
        if not desc_parts and intel.chapters:
            desc_parts.append(" · ".join(intel.chapters[:12]))
            desc_src.append(_src(p, "chapters"))
        if not desc_parts and intel.transcript:
            desc_parts.append(clean_text(intel.transcript, limit=400))
            desc_src.append(_src(p, "transcript"))
        if desc_parts:
            p.set("description", "\n\n".join(x for x in desc_parts if x), 0.45, desc_src)

    presenter = ""
    if intel.speakers:
        presenter, kind = intel.speakers[0], "speakers"
    elif intel.yt_channel:
        presenter, kind = intel.yt_channel, "youtube"
    if presenter:
        p.set("presenter", presenter, 0.55 if kind == "speakers" else 0.4, [_src(p, kind)])
    people = list(dict.fromkeys([*intel.speakers, *intel.people]))
    if people:
        p.set("speakers", people, 0.55, [_src(p, "speakers")])

    if intel.language:
        p.set("language", intel.language.split("-")[0], 0.8, [_src(p, "transcript")])
    if intel.topics:
        p.set("topic", intel.topics[0], 0.6, [_src(p, "topics")])

    year = intel.parsed_year or intel.yt_year
    if year:
        p.set("year", year, 0.6 if intel.parsed_year else 0.5,
              [_src(p, "file" if intel.parsed_year else "youtube")])  # fmt: skip
    return p
