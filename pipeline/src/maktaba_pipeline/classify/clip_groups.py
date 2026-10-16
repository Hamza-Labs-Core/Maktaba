"""Clip groups and promo links: pure logic shared by mediaintel and the Maktaba queues.

Detection places episodes in seasons. What is left over is still worth airing:

- **Clips** (filler): YouTube Shorts, clip playlists ("مقاطع مختارة …") and anything
  shorter than :data:`FILLER_MAX_S`. A channel pads its slots with them.
- **Stand-alone videos** (programme material): longer videos in no show.

:func:`plan_groups` puts each leftover in exactly one group: the most specific real
playlist holding at least :data:`MIN_PLAYLIST` of them, else the "فتاوى ومسائل" group
for questions and fatwas, else its topic tag (``genre:*``, at least :data:`MIN_TOPIC`),
else one "متنوعة" group per kind.

**Promos** (برومو / إعلان / تشويق …) are linked to the show they advertise
(:func:`link_promo`, :func:`promo_season`) rather than grouped by topic; callers add
one "إعلانات" group per library holding the linked promos.

Used by ``scripts/mediaintel/group_clips.py`` (SQLite cache) and by the
``clip_groups`` / ``promo_links`` queue stages (``engine/stages``).
"""

from __future__ import annotations

import hashlib
import re
from collections import Counter, defaultdict
from dataclasses import dataclass, field

from ..text.arabic import fold
from .series_detector import DetectorConfig
from .title_template import season_number
from .topic_tags import TOPIC_NAMES_AR

__all__ = [
    "FILLER_MAX_S",
    "MIN_PLAYLIST",
    "MIN_TOPIC",
    "PROMO_RE",
    "QA_RE",
    "Group",
    "Item",
    "Show",
    "group_id",
    "group_title",
    "is_promo",
    "is_qa",
    "link_promo",
    "name_words",
    "plan_groups",
    "promo_season",
]

#: longer than this and not a clip: a stand-alone programme, not filler
FILLER_MAX_S = 9 * 60
MIN_PLAYLIST = 5
MIN_TOPIC = 10
#: a question or a fatwa: grouped as "فتاوى ومسائل" whatever its topic
QA_RE = re.compile(
    r"(?:^|\s)(?:"
    + "|".join(
        re.escape(fold(w))
        for w in (
            "هل يجوز", "هل يصح", "هل يلزم", "هل يشرع", "هل يجب", "ما حكم", "حكم ", "سؤال",
            "أسئلة", "اسألة", "فتوى", "فتاوى", "فتاوي", "يسأل", "ما الفرق", "ما معنى", "مامعنى",
            "ما الراجح", "ما رأي", "ما موقف", "ما صحة", "استفسار",
        )
    )
    + ")"
)  # fmt: skip
#: words that say a video advertises a show
PROMO_RE = re.compile(r"(برومو|promo|اعلان|تشويق|قريبا|trailer|teaser|(?<!\w)تتر(?!\w))")
_CFG = DetectorConfig()
_CATCH_ALL = re.compile(_CFG.catch_all_re)
_NAME_STOP = _CFG.generic_words | frozenset(fold(w) for w in ("برنامج", "برومو", "اعلان"))


def group_id(folder: str, kind: str, basis: str, name: str) -> str:
    return "clips:" + hashlib.sha1(f"{folder}|{kind}|{basis}|{name}".encode()).hexdigest()[:16]


def group_title(library: str, kind: str, basis: str, name: str) -> str:
    """Plex / dizqueTV title of a group: "<lib> - مقاطع - <name>", "<lib> - مقاطع متنوعة"."""
    word = "مقاطع" if kind == "clips" else "منفردات"
    if basis == "rest":
        return f"{library} - {word} متنوعة"
    return f"{library} - {word} - {name}"


@dataclass
class Item:
    path: str
    title: str
    duration_s: float
    upload: str | None
    playlists: dict[str, int] = field(default_factory=dict)  # playlist id → position
    genre: str | None = None
    clip: bool = False
    qa: bool = False  # a question / fatwa ("هل يجوز …", "ما حكم …", "سؤال عن …")


@dataclass
class Group:
    kind: str
    basis: str
    name: str
    topic: str | None
    items: list[str]


@dataclass(frozen=True)
class Show:
    library: str
    folder: str
    name: str
    series_key: str
    words: tuple[str, ...]
    seasons: tuple[int, ...] = ()


def name_words(name: str) -> tuple[str, ...]:
    return tuple(w for w in fold(name).split() if len(w) > 2 and w not in _NAME_STOP)


def link_promo(title: str, folder: str, shows: list[Show]) -> tuple[Show, float, str] | None:
    """The show a promo's title names: every name word in the title; the same channel
    first, then the longest name. None when two shows fit equally."""
    t = fold(title)

    def names(s: Show) -> bool:
        if s.words and sum(len(w) for w in s.words) >= 5:
            return all(w in t for w in s.words)
        # a short name ("كن نجما"): the whole name, as written
        whole = " ".join(fold(s.name).split())
        return len(whole) >= 5 and re.search(rf"(?<!\w){re.escape(whole)}", t) is not None

    fits = [s for s in shows if names(s)]
    if not fits:
        return None

    def rank(s: Show) -> tuple[int, int]:
        return (s.folder == folder, len(fold(s.name)))

    fits.sort(key=rank, reverse=True)
    if len(fits) > 1 and rank(fits[0]) == rank(fits[1]) and fits[0].name != fits[1].name:
        return None
    best = fits[0]
    where = "same channel" if best.folder == folder else f"in {best.library}"
    return best, 0.9 if best.folder == folder else 0.7, f"title names «{best.name}» ({where})"


def promo_season(title: str, show: Show) -> int | None:
    """The season a promo names: a number glued to or right after the show's name
    ("برومو قصة وفكرة2" → 2) or "الموسم الثاني"; with none, the show's first season."""
    t = fold(title)
    if show.words:
        m = re.search(re.escape(show.words[-1]) + r"\s*(\d{1,2})(?!\d)", t)
        if m and int(m.group(1)) in show.seasons:
            return int(m.group(1))
    if (n := season_number(title)) is not None and n in show.seasons:
        return n
    return min(show.seasons) if show.seasons else None


def plan_groups(items: list[Item], playlist_titles: dict[str, str]) -> list[Group]:
    """One group per item: smallest real playlist with enough of them, else topic, else
    the kind's "متنوعة" group."""
    out: list[Group] = []
    for kind in ("clips", "standalone"):
        pool = {i.path: i for i in items if i.clip == (kind == "clips")}
        sizes = Counter(pid for i in pool.values() for pid in i.playlists)
        by_pl: dict[str, list[Item]] = defaultdict(list)
        for i in pool.values():
            for pid in i.playlists:
                by_pl[pid].append(i)
        candidates = [
            pid
            for pid, n in sizes.items()
            if n >= MIN_PLAYLIST
            and not (n >= _CFG.catch_all_min and _CATCH_ALL.search(fold(playlist_titles[pid])))
        ]
        names: Counter[str] = Counter()
        for pid in sorted(candidates, key=lambda p: (sizes[p], p)):
            members = [i for i in by_pl[pid] if i.path in pool]
            if len(members) < MIN_PLAYLIST:
                continue
            members.sort(key=lambda i: (i.playlists[pid], i.title))
            name = " ".join(playlist_titles[pid].split())
            names[fold(name)] += 1
            if names[fold(name)] > 1:
                name = f"{name} ({names[fold(name)]})"
            out.append(
                Group(kind, "playlist", name, _main_genre(members), [i.path for i in members])
            )
            for i in members:
                del pool[i.path]
        qa = sorted((i for i in pool.values() if i.qa), key=lambda i: (i.upload or "9999", i.title))
        if qa:  # questions and fatwas together, whatever their topic
            out.append(Group(kind, "topic", "فتاوى ومسائل", "qa_fatwa", [i.path for i in qa]))
            for i in qa:
                del pool[i.path]
        by_genre: dict[str, list[Item]] = defaultdict(list)
        for i in pool.values():
            if i.genre and i.genre != "general":
                by_genre[i.genre].append(i)
        for genre, members in sorted(by_genre.items()):
            if len(members) < MIN_TOPIC:
                continue
            members.sort(key=lambda i: (i.upload or "9999", i.title))
            name = TOPIC_NAMES_AR.get(genre, genre)
            out.append(Group(kind, "topic", name, genre, [i.path for i in members]))
            for i in members:
                del pool[i.path]
        if pool:
            rest = sorted(pool.values(), key=lambda i: (i.upload or "9999", i.title))
            out.append(Group(kind, "rest", "متنوعة", _main_genre(rest), [i.path for i in rest]))
    return out


def _main_genre(items: list[Item]) -> str | None:
    c = Counter(i.genre for i in items if i.genre)
    return c.most_common(1)[0][0] if c else None


def is_promo(title: str) -> bool:
    """Does the title say the video advertises a show?"""
    return bool(PROMO_RE.search(fold(title)))


def is_qa(title: str) -> bool:
    """Is it a question or a fatwa ("هل يجوز …", "ما حكم …")?"""
    return bool(QA_RE.search(fold(title)))
