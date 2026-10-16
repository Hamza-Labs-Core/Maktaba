"""Story 26.3 — series detection for YouTube-archive libraries.

Groups a channel's videos into **series → seasons → ordered episodes**, and
sets aside what is not an episode (promos, next-episode trailers, highlight
clips). Pure and deterministic: callers load :class:`VideoFacts` /
:class:`PlaylistFacts` from wherever (yt-dlp sidecars, YouTube, Plex, the
Maktaba DB) and persist the :class:`Detection`.

How a channel is organised (observed across YouTube lecture/program
channels, e.g. a Ramadan program per year):

- A *program* airs as a season of ~30 equal-length episodes in a short
  window; later years are new seasons, often new playlists whose titles
  differ only by a season marker (``قصة وآية`` / ``قصة وآية 2`` / ``… 3``,
  ``قصص الأنبياء رمضان 2024`` / ``قصص الأنبياء (الموسم الثاني)``).
- The same *name* can be different shows: a 1-minute shorts series in 2023
  and a 35-minute drama in 2025 both titled ``الحبيب``. Name alone is
  never enough — duration band and air dates must agree.
- Promos and trailers air *during* a season and share its name.
- Some shows never close a season: a weekly program or a lecture series that
  keeps going for years (kind ``ongoing``). They are split into one season per
  year (Plex's convention for daily and weekly shows), episodes in air order, and
  a late upload is simply a new episode, not a new season.
- Many episodes are not in any playlist; they are recognised by the
  season's title structure, duration band, air-date window and thumbnail
  branding.

Algorithm (D1 of plan 26.3 adapted: playlist-seeded, then signal-scored
attachment instead of all-pairs union-find, which is equivalent when
playlists exist and far cheaper):

1. **Seed seasons from real playlists.** Clip playlists (``مقاطع مميزة …``)
   are kept apart. Inside each playlist, teasers and members outside the
   *core duration band* (the densest log-duration cluster) become extras.
2. **Key & season of each seed** from its title: presenter/channel words,
   generic words (برنامج، حلقات، سلسلة …) and season/year markers removed;
   a trailing number or ``الموسم N`` is the season.
3. **Merge seeds into series** when keys match *and* duration bands are
   compatible. A short-form seed whose air dates overlap a long-form season
   of the same key is that season's promos (extras), not a series.
   Overlapping seeds (one playlist inside another) merge into one season.
4. **Attach unlisted videos** to the best-scoring season: title evidence
   (series key words / the season's distinctive segments), duration band,
   air-date window, thumbnail dHash to the season's members. Below
   :attr:`DetectorConfig.attach_threshold` a video stays unassigned.
5. **Order** episodes by playlist position (reversed when positions run
   against air dates), then episode number, then air date, then title.
"""

from __future__ import annotations

import math
import re
from collections import Counter, defaultdict
from collections.abc import Callable, Iterable, Sequence
from dataclasses import dataclass, field
from datetime import date, timedelta

from ..audio.chromaprint import MATCH as AUDIO_MATCH
from ..audio.chromaprint import matches as audio_matches
from ..text.arabic import fold
from ..thumbnail.dhash import hamming
from .title_template import (
    episode_number,
    is_recap,
    is_teaser,
    season_number,
    segments,
    strip_markers,
    year_marker,
)

__all__ = [
    "DETECTOR_VERSION",
    "Detection",
    "DetectorConfig",
    "PlaylistFacts",
    "Season",
    "Series",
    "Topic",
    "VideoFacts",
    "detect",
    "is_short",
]

DETECTOR_VERSION: int = 3


@dataclass(frozen=True, slots=True)
class VideoFacts:
    key: str  # stable id (file path, Maktaba video id …)
    title: str
    duration_s: float | None = None
    air_date: date | None = None
    date_exact: bool = False  # False → approximate ("3 years ago" granularity)
    dhash: int | None = None
    is_short: bool = False  # YouTube Short (Shorts tab, #shorts, vertical) — caller's knowledge
    #: chromaprint of the opening / closing ~90 s (theme song, intro, end nasheed);
    #: either may be empty — uploads often cut the intro
    audio_head: tuple[int, ...] = ()
    audio_tail: tuple[int, ...] = ()
    #: dHashes of frames at fixed points (title card, set, presenter)
    frames: tuple[int, ...] = ()


@dataclass(frozen=True, slots=True)
class PlaylistFacts:
    playlist_id: str
    title: str
    members: tuple[tuple[str, int | None], ...]  # (video key, position) — local videos only


@dataclass(frozen=True, slots=True)
class DetectorConfig:
    #: words that never identify a series (presenter/channel words are added per call)
    generic_words: frozenset[str] = frozenset(
        fold(w)
        for w in (
            "برنامج", "برامج", "حلقات", "الحلقات", "حلقة", "سلسلة", "سلسله", "السلسلة", "رمضان",
            "hd", "الشيخ", "للشيخ", "الدكتور", "د", "مع", "في", "جديد", "كامل", "كاملة",
            "حصريا", "حصرياً", "على", "قناة", "الرسمية", "ramadan", "series", "full",
        )
    )  # fmt: skip
    clip_playlist_re: str = r"^(مقاطع|مقطع|مختارات|مقتطفات|بثوث|بث مباشر|shorts|clips|highlights)"
    #: catch-all playlists ("جميع اللقاءات والبرامج", "منوعات", "all videos") group unrelated
    #: videos; from this size they never define a show, they are only evidence
    catch_all_re: str = r"(^|\s)(جميع|كل ال|كافه|منوعات|متنوعه|المتنوعه|all videos|uploads)"
    catch_all_min: int = 40
    #: a seed's members farther than this factor from its core median are extras
    core_band: float = 2.2
    #: two seeds of one key are the same show only if their core medians are this close
    same_show_ratio: float = 2.5
    #: "short form" for the promo rule
    short_form_s: float = 240.0
    #: unlisted-video attachment
    attach_threshold: float = 0.62
    date_window_days: int = 45
    approx_window_days: int = 400
    #: fingerprints are compared only within this length band (|log ratio|) and date window
    sig_length_band: float = 0.35
    sig_window_days: int = 180
    dhash_close_bits: int = 10
    min_season_size: int = 3
    #: "ongoing": at least this many episodes, and either numbers that climb past any
    #: season's size, or exact air dates spread steadily (this share of months) over
    #: at least this many days
    ongoing_min_episodes: int = 12
    ongoing_min_number: int = 60
    ongoing_min_span_days: int = 730
    ongoing_month_coverage: float = 0.5
    #: anything shorter is a short (clip, promo, YouTube Short) and never an episode
    min_episode_s: float = 180.0
    #: title-brand shows ("شاهد على العصر | …"): a title segment carried by at least this
    #: many videos and this share of the channel names a show
    brand_min: int = 20
    brand_share: float = 0.02
    #: … which is rebuilt from its own playlists only when playlist detection scattered
    #: it: over this many series, or with less than this share in series of its name
    brand_scattered_series: int = 3
    brand_named_share: float = 0.7
    #: a curated playlist cutting across shows ("الشأن الفلسطيني") is a topic collection
    topic_min: int = 5


@dataclass
class Season:
    number: int
    episodes: list[str]  # video keys, in order
    extras: list[str] = field(default_factory=list)  # promos/trailers of this season
    playlists: list[str] = field(default_factory=list)
    first_air: date | None = None
    last_air: date | None = None
    median_duration_s: float | None = None
    attached: list[str] = field(default_factory=list)  # keys added from outside playlists
    duplicates: list[str] = field(default_factory=list)  # second uploads of an episode
    #: episode number of each episode, same order: from the titles when the season is
    #: numbered consistently ("كنوز السيرة 31" → 31, gaps kept), else 1..n
    numbers: list[int] = field(default_factory=list)
    #: a season with a name of its own (a guest of a talk show, "وليد جنبلاط"), shown
    #: instead of "الموسم N"
    name: str | None = None
    #: the year a playlist title gives the season ("بلا حدود - 2005")
    year: int | None = None


@dataclass
class Series:
    name: str  # display name (from playlist titles)
    key: str  # folded identity key
    seasons: list[Season]
    kind: str = "episodic"  # episodic | ongoing | short-form | clips
    #: built from a title brand ("شاهد على العصر | …") rather than playlist seeds
    brand: bool = False

    @property
    def episode_count(self) -> int:
        return sum(len(s.episodes) for s in self.seasons)


@dataclass
class Topic:
    """A curated playlist that cuts across shows ("الشأن الفلسطيني"): its own collection,
    which may overlap seasons."""

    name: str
    playlist_id: str
    items: list[str]  # video keys in playlist order


@dataclass
class Detection:
    series: list[Series]
    unassigned: list[str]
    shorts: list[str] = field(default_factory=list)  # excluded from series entirely
    topics: list[Topic] = field(default_factory=list)
    version: int = DETECTOR_VERSION


# --------------------------------------------------------------------------- helpers


def is_short(v: VideoFacts, cfg: DetectorConfig) -> bool:
    """A YouTube Short, or any clip under ``min_episode_s`` (promos, highlights)."""
    if v.is_short or "#short" in (v.title or "").lower():
        return True
    return v.duration_s is not None and v.duration_s < cfg.min_episode_s


_TRAILING_NUM_RE = re.compile(r"^(.*?\D)\s*(\d{1,2})$")


def _median(xs: Sequence[float]) -> float | None:
    s = sorted(xs)
    if not s:
        return None
    m = len(s) // 2
    return s[m] if len(s) % 2 else (s[m - 1] + s[m]) / 2


def _core_median(durations: Sequence[float]) -> float | None:
    """Median of the densest log-duration cluster (robust to promos mixed in)."""
    ds = sorted(d for d in durations if d and d > 0)
    if not ds:
        return None
    logs = [math.log(d) for d in ds]
    best, best_n = 0, -1
    for i, x in enumerate(logs):  # window of ±0.35 in log space ≈ ×1.4
        n = sum(1 for y in logs if abs(y - x) <= 0.35)
        if n > best_n:
            best, best_n = i, n
    center = logs[best]
    return _median([d for d, y in zip(ds, logs, strict=True) if abs(y - center) <= 0.35])


def _within(d: float | None, med: float | None, factor: float) -> bool:
    if d is None or med is None or med <= 0:
        return True
    return med / factor <= d <= med * factor


def series_key(title: str, stop: frozenset[str]) -> tuple[str, int | None, int | None]:
    """(key, season, year) from a playlist/series title."""
    season = season_number(title)
    year = year_marker(title)
    t = fold(title)
    t = re.sub(r"(?<!\w)(?:الموسم|موسم|season)\s*\S+", " ", t)
    t = re.sub(r"(?<!\d)(?:19[89]\d|20[0-4]\d|14[0-6]\d)(?!\d)", " ", t)
    t = re.sub(r"([^\W\d])(\d)", r"\1 \2", t)  # مشاهد4 → مشاهد 4
    words = [w for w in t.split() if w not in stop]
    if season is None and len(words) > 1 and words[-1].isdigit() and len(words[-1]) <= 2:
        season = int(words.pop())
    words = [w for w in words if not w.isdigit()]
    return " ".join(words), season, year


def title_season(title: str, key: str) -> int | None:
    """Season a *video* title states for series ``key``: a season marker
    ("الموسم الثاني") or a number glued to the series name ("مشاهد3 / الحلقة 26",
    "مشاهد1 حلقة 20", "قصة وآية 3 | …")."""
    if (n := season_number(title)) is not None:
        return n
    words = key.split()
    if not words:
        return None
    # punctuation becomes a word so "(الدنيا قصص وعبر) #26" can't read as season 26
    t = fold(re.sub(r"[^\w\s]+", " zzsep ", title))
    m = re.search(rf"(?<!\w){re.escape(words[-1])}\s*(\d{{1,2}})(?!\d)", t)
    if not m or int(m.group(1)) == episode_number(title):
        return None
    return int(m.group(1))


def _keys_match(a: str, b: str) -> bool:
    if not a or not b:
        return False
    if a == b:
        return True
    ta, tb = a.split(), b.split()
    short, long_ = (ta, tb) if len(ta) <= len(tb) else (tb, ta)
    return len(short) >= 2 and all(w in long_ for w in short)


def _date_span(
    keys: Iterable[str], facts: dict[str, VideoFacts]
) -> tuple[date | None, date | None]:
    ds = sorted(d for k in keys if (d := facts[k].air_date) is not None)
    return (ds[0], ds[-1]) if ds else (None, None)


def _days_outside(d: date, lo: date, hi: date) -> int:
    if d < lo:
        return (lo - d).days
    if d > hi:
        return (d - hi).days
    return 0


# --------------------------------------------------------------------------- seeds


@dataclass
class _Seed:
    playlists: list[str]
    titles: list[str]
    key: str
    season: int | None
    year: int | None
    members: dict[str, int | None]  # key → position
    extras: set[str]
    core_s: float | None
    first: date | None = None
    last: date | None = None


def _make_seed(
    p: PlaylistFacts, facts: dict[str, VideoFacts], stop: frozenset[str], cfg: DetectorConfig
) -> _Seed | None:
    members = {k: pos for k, pos in p.members if k in facts}
    if not members:
        return None
    key, season, year = series_key(p.title, stop)
    durs = [facts[k].duration_s for k in members if facts[k].duration_s]
    core = _core_median([d for d in durs if d is not None])
    # an extra is a teaser/recap by its title, or an odd length *without* an episode
    # number: a numbered member in its playlist is an episode however long it runs
    # ("الأربعون القلبية 2" at 6 min among 20-min talks, a 3-hour tafsir session)
    extras = {
        k
        for k in members
        if is_teaser(facts[k].title, facts[k].duration_s, typical_s=core)
        or (
            not _within(facts[k].duration_s, core, cfg.core_band)
            and _title_number(facts[k].title) is None
        )
    }
    s = _Seed([p.playlist_id], [p.title], key, season, year, members, extras, core)
    s.first, s.last = _date_span((k for k in members if k not in extras), facts)
    return s


def _merge_into(a: _Seed, b: _Seed) -> None:
    a.playlists += b.playlists
    a.titles += b.titles
    for k, pos in b.members.items():
        a.members.setdefault(k, pos)
    # an item is an extra only if no merged playlist counts it as an episode
    episodes = {k for k in a.members if k not in a.extras} | {
        k for k in b.members if k not in b.extras
    }
    a.extras = (a.extras | b.extras) - episodes
    a.season = a.season if a.season is not None else b.season


# --------------------------------------------------------------------------- attach


@dataclass
class _Profile:
    key_words: list[str]
    distinctive: set[str]  # folded title segments typical of this season, rare elsewhere
    core_s: float | None
    first: date | None
    last: date | None
    hashes: list[int]
    title_sets: list[frozenset[str]]  # content words of each member title
    dates_exact: bool = False  # span built from exact air dates
    numbers: frozenset[int] = frozenset()  # explicit episode numbers already in the season
    audio: list[tuple[tuple[int, ...], tuple[int, ...]]] = field(default_factory=list)
    frame_hashes: list[int] = field(default_factory=list)
    #: the weekday most episodes come out on (0 = Monday), when one clearly dominates
    weekday: int | None = None
    #: the series keeps going: a later air date is a new episode, not another show
    ongoing: bool = False
    #: the show's own name words, and whether (almost) every member title carries them
    name_words: tuple[str, ...] = ()
    branded: bool = False


def _content_words(title: str, stop: frozenset[str]) -> frozenset[str]:
    return frozenset(
        w for w in fold(title).split() if w not in stop and not w.isdigit() and len(w) > 1
    )


def _profile(
    eps: Sequence[str],
    key: str,
    facts: dict[str, VideoFacts],
    seg_df: Counter[str],
    stop: frozenset[str],
    *,
    ongoing: bool = False,
    name: str = "",
) -> _Profile:
    seg_counts: Counter[str] = Counter()
    for k in eps:
        seg_counts.update(set(segments(facts[k].title)))
    n = max(1, len(eps))
    distinctive = {
        s
        for s, c in seg_counts.items()
        if c / n >= 0.4 and seg_df[s] <= max(3, 2.5 * c) and len(s) > 2
    }
    exact = [k for k in eps if facts[k].air_date and facts[k].date_exact]
    # prefer exact dates for the span when there are enough of them
    dates_exact = len(exact) >= max(3, len(eps) // 3)
    f, l_ = _date_span(exact if dates_exact else eps, facts)
    days = Counter(d.weekday() for k in exact if (d := facts[k].air_date))
    top = days.most_common(1)
    weekday = top[0][0] if top and len(exact) >= 4 and top[0][1] >= 0.7 * len(exact) else None
    return _Profile(
        key_words=key.split(),
        distinctive=distinctive,
        core_s=_core_median([dur for k in eps if (dur := facts[k].duration_s)]),
        first=f,
        last=l_,
        hashes=[h for k in eps if (h := facts[k].dhash) is not None],
        title_sets=[_content_words(facts[k].title, stop) for k in eps],
        dates_exact=dates_exact,
        numbers=frozenset(num for k in eps if (num := episode_number(facts[k].title)) is not None),
        # a handful of members is enough to know the season's music
        audio=[
            (facts[k].audio_head, facts[k].audio_tail)
            for k in eps
            if facts[k].audio_head or facts[k].audio_tail
        ][:6],
        frame_hashes=[h for k in eps for h in facts[k].frames],
        weekday=weekday,
        ongoing=ongoing,
        name_words=(nw := tuple(w for w in fold(name).split() if len(w) > 2)),
        branded=bool(nw)
        and sum(all(w in fold(facts[k].title) for w in nw) for k in eps) >= 0.7 * max(1, len(eps)),
    )


def _attach_score(
    v: VideoFacts,
    p: _Profile,
    cfg: DetectorConfig,
    stop: frozenset[str],
    season_match: bool | None = None,
) -> float:
    """``season_match``: the title states this season (True), another one (False)."""
    if season_match is False:
        return 0.0  # "مشاهد1 حلقة 20" is not an episode of season 3
    t = fold(v.title)
    words = set(t.split())
    # title evidence
    if p.key_words and all(w in words or w in t for w in p.key_words):
        title_s = 1.0
    elif p.branded and not all(w in t for w in p.name_words):
        # every episode says the show's name ("… أندى العالمين موسم 1442") and this title
        # does not: sharing a topic ("محبة النبي ﷺ") does not make it an episode. Only the
        # signature (same intro / end song) below can still place it.
        title_s = 0.0
    elif p.distinctive and set(segments(v.title)) & p.distinctive:
        title_s = 0.8
    else:
        # a near-twin of a member title ("…الجزء السادس" next to "…الجزء الثالث")
        mine = _content_words(v.title, stop)
        best_j = max(
            (len(mine & o) / len(mine | o) for o in p.title_sets if mine and o), default=0.0
        )
        title_s = 0.75 if best_j >= 0.6 else 0.0
    # same intro / end song, or the same title card / set: the season's signature.
    # Evidence only — a missing match proves nothing (intros get cut on upload).
    # comparing fingerprints is the costly part, so it is only done where the cheap facts
    # already point at this season: about the same length, released within half a year of
    # it, and on its usual weekday (±1) when it has one. Evidence only, so being strict
    # here can only miss a match, never make a wrong one.
    sig = _signature_match(v, p) if title_s < 1.0 and _fingerprint_worth_it(v, p, cfg) else 0.0
    if sig:
        title_s = max(title_s, sig)
    # same branded thumbnail: the title just doesn't name the series
    if (
        title_s == 0.0
        and v.dhash is not None
        and p.hashes
        and min(hamming(v.dhash, h) for h in p.hashes) <= cfg.dhash_close_bits // 2 + 1
    ):
        title_s = 0.5
    # duration band
    if v.duration_s is None or p.core_s is None:
        dur_s = 0.5
    else:
        r = abs(math.log(v.duration_s / p.core_s))
        dur_s = 1.0 if r <= 0.35 else 0.6 if r <= 0.7 else 0.0
    # air-date window
    if v.air_date is None or p.first is None or p.last is None:
        date_s = 0.5
    else:
        # the tolerance follows the weaker of the two dates being compared
        window = cfg.date_window_days if v.date_exact and p.dates_exact else cfg.approx_window_days
        out = _days_outside(v.air_date, p.first, p.last)
        if p.ongoing and v.air_date > p.last:
            out = 0  # an ongoing show has no last episode yet
        date_s = 1.0 if out == 0 else 0.7 if out <= window else 0.0
        # a late upload of a numbered episode the season is missing ("#26" months after
        # the playlist, "الحلقة الأخيرة" re-uploaded a year on) still names its season;
        # earlier than the season it is another show, and a taken number is a rerun
        n = episode_number(v.title)
        if (
            date_s == 0.0
            and title_s == 1.0
            and n is not None
            and n not in p.numbers
            and v.air_date > p.last
            and (v.air_date - p.last).days <= max(90, (p.last - p.first).days)
        ):
            date_s = 0.4
    # thumbnail branding
    if v.dhash is None or not p.hashes:
        thumb_s = 0.5
    else:
        best = min(hamming(v.dhash, h) for h in p.hashes)
        thumb_s = (
            1.0
            if best <= cfg.dhash_close_bits
            else 0.6
            if best <= cfg.dhash_close_bits * 2
            else 0.2
        )
    if sig >= 0.8:
        thumb_s = 1.0
    if season_match and title_s > 0.0 and date_s == 0.0:
        date_s = 0.5  # "مشاهد3 / الحلقة 26" months late is still season 3
    if title_s == 0.0 or dur_s == 0.0 or date_s == 0.0:
        return 0.0  # each is a veto: right name, wrong length or wrong year is another show
    return 0.45 * title_s + 0.2 * dur_s + 0.2 * date_s + 0.15 * thumb_s


def _fingerprint_worth_it(v: VideoFacts, p: _Profile, cfg: DetectorConfig) -> bool:
    if not (v.audio_head or v.audio_tail or v.frames):
        return False
    if v.duration_s and p.core_s and abs(math.log(v.duration_s / p.core_s)) > cfg.sig_length_band:
        return False
    if v.air_date and p.first and p.last:
        out = _days_outside(v.air_date, p.first, p.last)
        if p.ongoing and v.air_date > p.last:
            out = 0
        if out > cfg.sig_window_days:
            return False
        if p.weekday is not None and v.date_exact:
            off = (v.air_date.weekday() - p.weekday) % 7
            if min(off, 7 - off) > 1:
                return False
    return True


def _signature_match(v: VideoFacts, p: _Profile) -> float:
    """0.8 when the opening or closing music matches a season member's, 0.6 when two of
    the video's frames match the season's frames, else 0."""
    if (v.audio_head or v.audio_tail) and p.audio:
        for head, tail in p.audio:
            for a, b in ((v.audio_head, head), (v.audio_tail, tail), (v.audio_head, tail)):
                if a and b and audio_matches(a, b, threshold=AUDIO_MATCH):
                    return 0.8
    if v.frames and p.frame_hashes:
        hits = sum(1 for f in v.frames if min(hamming(f, h) for h in p.frame_hashes) <= 6)
        if hits >= 2:
            return 0.6
    return 0.0


def _promo_score(v: VideoFacts, p: _Profile, cfg: DetectorConfig) -> float:
    """A short clip naming a long-form season, aired around it: that season's promo."""
    if v.duration_s is None or p.core_s is None:
        return 0.0
    if v.duration_s >= cfg.short_form_s or p.core_s < cfg.short_form_s:
        return 0.0
    t = fold(v.title)
    if not p.key_words or not all(w in t for w in p.key_words):
        return 0.0
    if v.air_date is None or p.first is None or p.last is None:
        date_s = 0.5
    else:
        window = (
            cfg.date_window_days * 3 if v.date_exact and p.dates_exact else cfg.approx_window_days
        )
        out = _days_outside(v.air_date, p.first, p.last)
        if out > window:
            return 0.0
        date_s = 1.0 if out <= cfg.date_window_days * 3 else 0.7
    if v.dhash is None or not p.hashes:
        thumb_s = 0.5
    else:
        best = min(hamming(v.dhash, h) for h in p.hashes)
        thumb_s = 1.0 if best <= cfg.dhash_close_bits * 2 else 0.4
    return 0.5 + 0.3 * date_s + 0.2 * thumb_s


# --------------------------------------------------------------------------- ordering


def _number_after_key(title: str, key: str) -> int | None:
    """The number right after the show's name: "على ما تفرج ١٣٥ - …" → 135."""
    words = key.split()
    if not words:
        return None
    m = re.search(re.escape(words[-1]) + r"\s+(\d{1,4})\b", fold(title))
    return int(m.group(1)) if m else None


def _is_ongoing(
    keys: Sequence[str], facts: dict[str, VideoFacts], cfg: DetectorConfig, key: str = ""
) -> bool:
    """A show that keeps going (a weekly program, a lecture series) rather than closed
    seasons. A closed numbered run (1..30) or numbering that restarts is seasons, even
    when it was released slowly; otherwise numbers climbing past any season's size, or
    exact air dates spread steadily over years, mean ongoing."""
    if len(keys) < cfg.ongoing_min_episodes:
        return False
    nums = []
    for k in keys:
        n = episode_number(facts[k].title)
        n = n if n is not None and n < 9000 else _number_after_key(facts[k].title, key)
        if n is not None:
            nums.append(n)
    if len(nums) >= 0.6 * len(keys):
        if len(nums) - len(set(nums)) > max(2, len(nums) // 10):
            return False  # numbers restart: seasons
        if max(nums) <= cfg.ongoing_min_number:
            return False  # a closed run
    if len(set(nums)) >= cfg.ongoing_min_episodes and max(nums) > cfg.ongoing_min_number:
        return True
    ds = sorted(d for k in keys if facts[k].date_exact and (d := facts[k].air_date))
    if len(ds) < cfg.ongoing_min_episodes or (ds[-1] - ds[0]).days < cfg.ongoing_min_span_days:
        return False
    months = {(d.year, d.month) for d in ds}
    total = (ds[-1].year - ds[0].year) * 12 + ds[-1].month - ds[0].month + 1
    return len(months) / total >= cfg.ongoing_month_coverage


def _by_year(seasons: Sequence[Season], facts: dict[str, VideoFacts]) -> list[Season]:
    """An ongoing show's episodes regrouped as one season per year (season number = year).
    Undated episodes go with the year most episodes have."""
    eps = [k for se in seasons for k in se.episodes]
    years = Counter(d.year for k in eps if (d := facts[k].air_date))
    if not years:
        return list(seasons)
    common = years.most_common(1)[0][0]

    def year(k: str) -> int:
        d = facts[k].air_date
        return d.year if d else common

    out: dict[int, Season] = {}
    playlists = [pl for se in seasons for pl in se.playlists]

    def season(k: str) -> Season:
        y = year(k)
        return out.setdefault(y, Season(number=y, episodes=[], playlists=list(playlists)))

    attached = {k for se in seasons for k in se.attached}
    for k in eps:
        season(k).episodes.append(k)
        if k in attached:
            season(k).attached.append(k)
    for k in (x for se in seasons for x in se.extras):
        season(k).extras.append(k)
    for k in (x for se in seasons for x in se.duplicates):
        season(k).duplicates.append(k)
    return [out[y] for y in sorted(out) if out[y].episodes]


def _late_seasons(
    videos: Sequence[VideoFacts],
    assigned: set[str],
    profiles: Sequence[tuple[Series, Season, _Profile]],
    facts: dict[str, VideoFacts],
    cfg: DetectorConfig,
) -> list[tuple[Series, list[list[str]]]]:
    """Unassigned videos naming a series, in its length band, aired after its last season.

    They need an episode marker (or to come three or more together), so a stray clip
    that merely mentions the name does not open a season; a video naming two series is
    left alone. Runs closer than ``date_window_days * 3`` form one season.
    """
    latest: dict[int, tuple[Series, _Profile, date]] = {}
    for ser, se, prof in profiles:
        # the season's end *after* attaching (late episodes already joined it)
        _, end = _date_span(se.episodes, facts)
        cur = latest.get(id(ser))
        if ser.kind == "ongoing":
            continue  # a late upload of an ongoing show is just its next episode
        if end and (cur is None or end > cur[2]):
            latest[id(ser)] = (ser, prof, end)
    found: dict[int, list[VideoFacts]] = defaultdict(list)
    for v in videos:
        if v.key in assigned or v.air_date is None or v.duration_s is None:
            continue
        t = fold(v.title)
        hits = [
            (ser, prof)
            for ser, prof, end in latest.values()
            if prof.key_words
            and all(w in t for w in prof.key_words)
            and prof.core_s
            and abs(math.log(v.duration_s / prof.core_s)) <= 0.35
            and v.air_date > end
            # a title naming a season the series has belongs there, not to a new one
            and title_season(v.title, ser.key) not in {se.number for se in ser.seasons}
        ]
        if len(hits) == 1:
            found[id(hits[0][0])].append(v)
    out: list[tuple[Series, list[list[str]]]] = []
    for sid, vs in found.items():
        vs.sort(key=lambda v: v.air_date or date.max)
        runs: list[list[VideoFacts]] = []
        for v in vs:
            prev = runs[-1][-1].air_date if runs else None
            if (
                runs
                and prev
                and v.air_date
                and (v.air_date - prev).days <= cfg.date_window_days * 3
            ):
                runs[-1].append(v)
            else:
                runs.append([v])
        keep = [
            [v.key for v in r if len(r) >= 3 or episode_number(v.title) is not None] for r in runs
        ]
        keep = [r for r in keep if r]
        if keep:
            out.append((latest[sid][0], keep))
    return out


def positions_of(playlists: Sequence[PlaylistFacts]) -> dict[str, int | None]:
    positions: dict[str, int | None] = {}
    for p in playlists:
        for k, pos in p.members:
            positions.setdefault(k, pos)
    return positions


def _story(title: str, key: str, stop: frozenset[str]) -> frozenset[str]:
    """The part of a title naming *this* episode: segments without the series name,
    presenter, season/episode markers ("الحلقة 29 | طلع البدر" → {"طلع البدر"})."""
    key_words = set(key.split())
    parts = set()
    for seg in segments(title):
        words = [w for w in strip_markers(seg).split() if w not in stop and w not in key_words]
        if words and not any(w in key_words for w in fold(seg).split()):
            parts.add(" ".join(words))
    return frozenset(parts)


def _dedupe(
    keys: list[str],
    positions: dict[str, int | None],
    facts: dict[str, VideoFacts],
    key: str,
    stop: frozenset[str],
) -> tuple[list[str], list[str]]:
    """Drop second uploads: same story title and duration band. Keeps the copy in a
    playlist, else the one whose title follows the season's usual template."""
    seg_count: Counter[str] = Counter(s for k in keys for s in set(segments(facts[k].title)))

    def keep_rank(k: str) -> tuple[int, int, str]:
        typical = sum(seg_count[s] for s in set(segments(facts[k].title)))
        return (positions.get(k) is None, -typical, k)

    by_story: dict[frozenset[str], list[str]] = defaultdict(list)
    for k in keys:
        if st := _story(facts[k].title, key, stop):
            by_story[st].append(k)
    dups: set[str] = set()
    for group in by_story.values():
        if len(group) < 2:
            continue
        group.sort(key=keep_rank)
        kept: list[str] = []
        for k in group:
            n, d = episode_number(facts[k].title), facts[k].duration_s
            twin = next(
                (
                    j
                    for j in kept
                    if (m := episode_number(facts[j].title)) is None or n is None or m == n
                    if (dj := facts[j].duration_s) and d and abs(math.log(d / dj)) <= 0.35
                ),
                None,
            )  # "الجزء الخامس" and "الجزء السادس" of one story are two episodes
            if twin is None:
                kept.append(k)
            else:
                dups.add(k)
    return [k for k in keys if k not in dups], sorted(dups)


def _order_by_date(ks: list[str], facts: dict[str, VideoFacts]) -> list[str]:
    """Air-date order; a lone "الحلقة N" (no numbered sibling titles) goes to slot N."""
    out = sorted(ks, key=lambda k: (facts[k].air_date or date.max, fold(facts[k].title)))
    numbered = {k: n for k in ks if (n := episode_number(facts[k].title)) is not None}
    if len(numbered) <= len(ks) // 3:  # numbering is the exception, not the scheme
        for k, n in sorted(numbered.items(), key=lambda x: x[1]):
            out.remove(k)
            out.insert(min(n, len(out) + 1) - 1, k)
    return out


def _order(
    keys: Iterable[str],
    positions: dict[str, int | None],
    facts: dict[str, VideoFacts],
    multi_playlist: bool = False,
) -> list[str]:
    """Episode order: playlist position, with attached episodes slotted in beside their siblings.

    - Playlists that run newest-first (positions against *exact* air dates) are flipped.
    - An episode without a position goes right after the sibling with the next-lower
      number among near-identical titles ("…الجزء الخامس" after "…الجزء الرابع"),
      else between the positioned episodes aired before/after it (exact dates only),
      else at the end.
    - With no positions at all: episode number, then air date, then title.
    """
    ks = list(keys)
    pos: dict[str, float] = {k: float(p) for k in ks if (p := positions.get(k)) is not None}
    dated = [k for k in ks if facts[k].air_date and facts[k].date_exact]
    if len(dated) >= 0.8 * len(ks) and (len(pos) < 0.7 * len(ks) or multi_playlist):
        # partial or mixed playlist numbering: exact air dates are the better order
        return _order_by_date(ks, facts)
    exact = [(pos[k], d) for k in pos if (d := facts[k].air_date) and facts[k].date_exact]
    if len(exact) >= 3:
        pairs = sorted(exact)
        back = sum(1 for x, y in zip(pairs, pairs[1:], strict=False) if y[1] < x[1])
        fwd = sum(1 for x, y in zip(pairs, pairs[1:], strict=False) if y[1] > x[1])
        if back > fwd * 2:
            top = max(pos.values())
            pos = {k: top - p for k, p in pos.items()}

    ep = {k: episode_number(facts[k].title) for k in ks}
    words = {k: frozenset(w for w in fold(facts[k].title).split() if not w.isdigit()) for k in ks}

    def similar(a: str, b: str) -> bool:
        wa, wb = words[a], words[b]
        return bool(wa and wb) and len(wa & wb) / len(wa | wb) >= 0.5

    numbered = {k: n for k, n in ep.items() if n is not None}
    placed = dict(pos)
    for k in ks:
        if k in pos:
            continue
        n = numbered.get(k)
        sibs = [j for j in pos if n is not None and j in numbered and similar(j, k)]
        below = [j for j in sibs if n is not None and numbered[j] < n]
        above = [j for j in sibs if n is not None and numbered[j] > n]
        d = facts[k].air_date
        if below:
            placed[k] = pos[max(below, key=lambda j: (numbered[j], pos[j]))] + 0.5
        elif above:
            placed[k] = pos[min(above, key=lambda j: (numbered[j], pos[j]))] - 0.5
        elif d is not None and facts[k].date_exact:
            earlier = [
                j for j in pos if facts[j].date_exact and (dj := facts[j].air_date) and dj <= d
            ]
            if earlier:
                placed[k] = max(pos[j] for j in earlier) + 0.5
            elif pos:
                placed[k] = min(pos.values()) - 0.5

    def sort_key(k: str) -> tuple[float, float, str, str]:
        v = facts[k]
        return (
            placed.get(k, math.inf),
            float(numbered[k]) if k in numbered else math.inf,
            v.air_date.isoformat() if v.air_date else "9999",
            fold(v.title),
        )

    return sorted(ks, key=sort_key)


# --------------------------------------------------------------------------- detect


_SEP_SPLIT_RE = re.compile(r"\s*(?:[|｜/⧸:：–—]|\s-\s|^-|-$)\s*")
_NAME_DROP = frozenset(fold(w) for w in ("برنامج", "حلقات", "hd", "سلسلة", "الشيخ", "للشيخ", "د"))


_TITLE_NUM_RE = re.compile(r"(?<!\d)(\d{1,3})(?!\d)")


def _title_number(title: str) -> int | None:
    n = episode_number(title)
    if n is not None and n < 9000:
        return n
    m = _TITLE_NUM_RE.search(fold(title))  # fold turns ٣١ into 31
    return int(m.group(1)) if m and int(m.group(1)) > 0 else None


def title_numbers(eps: Sequence[str], facts: dict[str, VideoFacts]) -> list[int]:
    """Episode numbers for an ordered season: the numbers in the titles when most titles
    carry one and they rise strictly in order (a missing one takes the next free number),
    otherwise positions 1..n."""
    raw = [_title_number(facts[k].title) for k in eps]
    have = [n for n in raw if n is not None]
    if (
        len(have) >= 0.7 * len(eps)
        and len(set(have)) == len(have)
        and all(a < b for a, b in zip(have, have[1:], strict=False))
    ):
        out: list[int] = []
        for i, n in enumerate(raw):
            if n is None:
                nxt = next((x for x in raw[i + 1 :] if x is not None), None)
                n = (out[-1] if out else 0) + 1
                if nxt is not None and n >= nxt:
                    break
            out.append(n)
        else:
            return out
    return list(range(1, len(eps) + 1))


def _title_name(
    eps: Sequence[str], facts: dict[str, VideoFacts], drop: frozenset[str]
) -> str | None:
    """The show's name as its episode titles write it: the words before the episode
    number, presenter and generic words left out, shared by most titles."""
    names: Counter[str] = Counter()
    for k in eps:
        words = []
        for w in facts[k].title.replace("|", " ").replace("-", " ").split():
            if _TITLE_NUM_RE.search(fold(w)):
                break
            if fold(w) not in drop and len(fold(w)) > 1:
                words.append(w)
        if words:
            names[" ".join(words)] += 1
    if not names:
        return None
    name, n = names.most_common(1)[0]
    return name if n >= 0.6 * len(eps) and any(len(fold(w)) > 2 for w in name.split()) else None


def _qualifier(
    titles: Sequence[str], name: str, presenter: frozenset[str], generic: frozenset[str]
) -> str | None:
    """The part of a playlist title that is not the show's name ("أنوار الصحيحين | كتاب
    الإيمان | شرح …" → "كتاب الإيمان"), skipping presenter- or generic-only parts."""
    own = fold(name)
    for t in sorted(titles, key=len):
        for part in _SEP_SPLIT_RE.split(t.replace("#", " ")):
            part = part.strip(" |｜-–/⧸.")
            words = [fold(w) for w in part.split()]
            if (
                part
                and fold(part) != own
                and fold(part) not in own
                and any(w not in presenter | generic for w in words)
            ):
                return part
    return None


def _distinct_names(
    series: list[Series],
    playlists: Sequence[PlaylistFacts],
    presenter: frozenset[str],
    cfg: DetectorConfig,
) -> None:
    """Two shows of one channel never share a name: they would land in one Plex
    collection and one dizqueTV show. Each takes the part of its playlist title that
    tells them apart, else its first year, else a number."""
    titles_of = {p.playlist_id: p.title for p in playlists}
    groups: dict[str, list[Series]] = defaultdict(list)
    for ser in series:
        groups[fold(ser.name)].append(ser)
    for group in groups.values():
        if len(group) < 2:
            continue
        for ser in group:
            pts = [titles_of[pid] for se in ser.seasons for pid in se.playlists if pid in titles_of]
            q = _qualifier(pts, ser.name, presenter, cfg.generic_words)
            if q is None and ser.seasons and ser.seasons[0].first_air:
                q = str(ser.seasons[0].first_air.year)
            if q:
                ser.name = f"{ser.name} - {q}"
        seen: Counter[str] = Counter()
        for ser in group:  # still equal (same part, same year): number them
            seen[fold(ser.name)] += 1
            if seen[fold(ser.name)] > 1:
                ser.name = f"{ser.name} ({seen[fold(ser.name)]})"


def _display_name(titles: Sequence[str], presenter: frozenset[str], generic: frozenset[str]) -> str:
    """Human name for a series from its playlist titles (original spelling kept).

    Separator-delimited parts made only of presenter/generic words are dropped
    ("نبيل العوضي - شهر الرحمة والغفران" → "شهر الرحمة والغفران"); presenter
    words, season/year markers and a trailing season number are removed;
    the first remaining part is the name;
    "برنامج" is kept only when the name would otherwise be a single word
    ("برنامج قصة", not "قصة").
    """
    t = min(titles, key=len)
    parts = [x for x in _SEP_SPLIT_RE.split(t.replace("#", " ").replace("_", " ")) if x.strip()]
    keep = [x for x in parts if any(fold(w) not in presenter | generic for w in x.split())] or parts
    name = keep[0]  # the first meaningful part names the show ("سواعد الإخاء - نخبة من المشايخ")
    name = re.sub(r"\s*\(?(?:الموسم|موسم)\s+\S+\)?", "", name)
    name = re.sub(r"\s*(?:رمضان\s*)?(?:19[89]\d|20[0-4]\d|14[0-6]\d)", "", name)
    name = " ".join(w for w in name.split() if fold(w) not in presenter)
    name = re.sub(r"\s*\d{1,2}$", "", name)  # trailing season number ("مشاهد3", "قصة وآية 2")
    words = name.split()
    core = [w for w in words if fold(w) not in _NAME_DROP]
    if len(core) == 1 and any(fold(w) == fold("برنامج") for w in words):
        core = ["برنامج", *core]
    name = " ".join(core or words)
    if name.count("(") != name.count(")"):
        name = name.replace("(", " ").replace(")", " ")
    return " ".join(name.split()).strip(" |｜-–/⧸.") or titles[0]


def detect(
    videos: Sequence[VideoFacts],
    playlists: Sequence[PlaylistFacts],
    *,
    presenter_words: Iterable[str] = (),
    config: DetectorConfig | None = None,
    progress: Callable[[int, int], None] | None = None,
) -> Detection:
    """Group one channel's videos into series/seasons. See module docstring.
    ``progress(done, total)`` is called now and then while loose videos are matched
    against the seasons, the slow part on a large channel."""
    cfg = config or DetectorConfig()
    presenter_words = list(presenter_words)
    # shorts never become episodes: drop them before anything else looks at the channel
    shorts = sorted(v.key for v in videos if is_short(v, cfg))
    short_set = set(shorts)
    videos = [v for v in videos if v.key not in short_set]
    playlists = [
        PlaylistFacts(p.playlist_id, p.title, tuple(m for m in p.members if m[0] not in short_set))
        for p in playlists
    ]
    facts = {v.key: v for v in videos}
    presenter = frozenset(fold(w) for w in presenter_words for w in fold(w).split())
    stop = cfg.generic_words | presenter
    clip_re = re.compile(cfg.clip_playlist_re)

    # 1–2. seeds
    seeds: list[_Seed] = []
    clip_seeds: list[_Seed] = []
    for p in playlists:
        s = _make_seed(p, facts, stop, cfg)
        if s is None or len(set(s.members) - s.extras) < cfg.min_season_size:
            continue  # too small to define a season; its videos can still attach (step 4)
        if len(s.members) >= cfg.catch_all_min and re.search(cfg.catch_all_re, fold(p.title)):
            continue  # a catch-all list of unrelated videos is not a show
        (clip_seeds if clip_re.search(fold(p.title)) else seeds).append(s)

    # merge overlapping seeds (one playlist inside another) → same season
    merged: list[_Seed] = []
    for s in sorted(seeds, key=lambda x: -len(x.members)):
        for m in merged:
            inter = len(set(s.members) & set(m.members))
            small = min(len(s.members), len(m.members))
            # a playlist nested in a bigger one ("موسى ﷺ في القرآن" inside "أنوار الأنبياء") is
            # a topic list of that show, whatever its name, not a show of its own
            nested = inter >= 0.8 * small and small < len(m.members)
            if nested or (inter >= 0.6 * small and _keys_match(s.key, m.key)):
                _merge_into(m, s)
                break
        else:
            merged.append(s)
    seeds = merged

    # 3a. promo seeds: a short-form playlist that airs *during* a long-form playlist of the
    # same name is that season's promos — decided per playlist, so a short-form series of
    # the same name in another year stays its own series.
    def is_long(x: _Seed) -> bool:
        return x.core_s is not None and x.core_s >= cfg.short_form_s

    promo_target: dict[int, _Seed] = {}
    for s in seeds:
        if is_long(s):
            continue
        for t in seeds:
            if is_long(t) and _keys_match(s.key, t.key) and _overlaps(s, t, cfg):
                promo_target[id(s)] = t
                break
    regular = [s for s in seeds if id(s) not in promo_target]

    # 3b. seeds → series: same key and a compatible duration band
    groups: list[list[_Seed]] = []
    for s in sorted(regular, key=lambda x: x.first or date.max):
        for g in groups:
            ref = g[0]
            if not _keys_match(s.key, ref.key):
                continue
            if (
                s.core_s
                and ref.core_s
                and max(s.core_s, ref.core_s) / min(s.core_s, ref.core_s) > cfg.same_show_ratio
            ):
                continue
            g.append(s)
            break
        else:
            groups.append([s])

    seg_df: Counter[str] = Counter()
    for v in videos:
        seg_df.update(set(segments(v.title)))

    # a video that is an episode in several playlists belongs to the most specific one:
    # the playlist whose name its title carries most, then the smaller playlist
    def _fit(sd: _Seed, k: str) -> tuple[float, int]:
        words = [w for w in sd.key.split() if len(w) > 2]
        t = fold(facts[k].title)
        return (sum(w in t for w in words) / len(words) if words else 0.0, -len(sd.members))

    owner: dict[str, _Seed] = {}
    for g in groups:
        for sd in g:
            for k in sd.members:
                if k not in sd.extras and (k not in owner or _fit(sd, k) > _fit(owner[k], k)):
                    owner[k] = sd

    assigned: set[str] = set()
    series: list[Series] = []
    season_of_seed: dict[int, Season] = {}
    for g in groups:
        seasons: list[Season] = []
        used_numbers = {s.season for s in g if s.season is not None}
        next_free = 1
        for s in sorted(
            g, key=lambda x: (x.season if x.season is not None else 999, x.first or date.max)
        ):
            if s.season is None:
                while next_free in used_numbers:
                    next_free += 1
                s.season = next_free
                used_numbers.add(next_free)
            eps = [
                k
                for k in s.members
                if k not in s.extras and owner.get(k) is s and k not in assigned
            ]
            own_extras = sorted(k for k in s.extras if k not in owner)  # an episode elsewhere wins
            if (
                len(eps) < cfg.min_season_size
                and len(g) == 1
                and len(s.members) < cfg.min_season_size
            ):
                continue
            same = next((x for x in seasons if x.number == s.season), None)
            if same:
                same.episodes += [k for k in eps if k not in same.episodes]
                same.extras += [k for k in own_extras if k not in same.extras]
                same.playlists += s.playlists
                season_of_seed[id(s)] = same
                assigned.update(eps)
                continue
            se = Season(
                number=s.season, episodes=eps, extras=own_extras, playlists=list(s.playlists)
            )
            seasons.append(se)
            season_of_seed[id(s)] = se
            assigned.update(eps)
        if not seasons:
            continue
        core = _median([s.core_s for s in g if s.core_s])
        kind = "short-form" if core is not None and core < cfg.short_form_s else "episodic"
        names = [t for s in g for t in s.titles]
        series.append(
            Series(
                name=_display_name(names, presenter, cfg.generic_words),
                key=g[0].key,
                seasons=seasons,
                kind=kind,
            )
        )
        assigned.update(k for se in seasons for k in se.extras)

    # promo playlists → extras of the season they aired with
    for s in seeds:
        target = promo_target.get(id(s))
        season = season_of_seed.get(id(target)) if target is not None else None
        if season is None:
            continue
        new = [k for k in s.members if k not in assigned]
        season.extras += new
        season.playlists.append(s.playlists[0])
        assigned.update(new)

    # clip playlists → their own series of kind "clips" (kept apart from episodes)
    for s in clip_seeds:
        keys = [k for k in s.members if k not in assigned]
        if len(keys) >= cfg.min_season_size:
            series.append(
                Series(
                    name=_display_name(s.titles, presenter, cfg.generic_words),
                    key=s.key,
                    seasons=[Season(number=1, episodes=keys, playlists=list(s.playlists))],
                    kind="clips",
                )
            )
            assigned.update(keys)

    # 3c. ongoing shows: steady releases over a long time, no closed seasons
    for ser in series:
        if ser.kind == "episodic" and _is_ongoing(
            [k for se in ser.seasons for k in se.episodes], facts, cfg, ser.key
        ):
            ser.kind = "ongoing"

    # 4. attach unlisted videos
    profiles: list[tuple[Series, Season, _Profile]] = []
    for ser in series:
        if ser.kind == "clips":
            continue
        for se in ser.seasons:
            ongoing = ser.kind == "ongoing"
            prof = _profile(
                se.episodes, ser.key, facts, seg_df, stop, ongoing=ongoing, name=ser.name
            )
            profiles.append((ser, se, prof))
    for i_v, v in enumerate(videos, 1):
        if progress is not None and (i_v % 250 == 0 or i_v == len(videos)):
            progress(i_v, len(videos))
        if v.key in assigned:
            continue
        cands: list[tuple[float, str, Series, Season, _Profile]] = []
        for ser, se, prof in profiles:
            hint = title_season(v.title, ser.key)
            match = None if hint is None else hint == se.number
            sc = _attach_score(v, prof, cfg, stop, match)
            if sc >= cfg.attach_threshold:
                cands.append((sc, "episode", ser, se, prof))
            pc = _promo_score(v, prof, cfg)
            if pc >= cfg.attach_threshold:
                cands.append((pc, "promo", ser, se, prof))
        if not cands:
            continue
        cands.sort(key=lambda x: -x[0])
        best, role, ser, se, prof = cands[0]
        rival = next((c for c in cands[1:] if c[2] is not ser), None)
        if rival is not None and rival[0] >= best - 0.1:
            continue  # plausible in two different series (e.g. no air date): leave for review
        if (
            role == "promo"
            or is_teaser(v.title, v.duration_s, typical_s=prof.core_s)
            or (is_recap(v.title, ser.key) and episode_number(v.title) is None)
        ):
            se.extras.append(v.key)
        else:
            se.episodes.append(v.key)
            se.attached.append(v.key)
        assigned.add(v.key)

    # 4b. episodes aired after a series' last season ends start its next season
    #     ("الحبيب | بل الرفيق الأعلى | الحلقة الأخيرة" a half-year after season 1)
    for ser, cands_ in _late_seasons(videos, assigned, profiles, facts, cfg):
        last_no = max(se.number for se in ser.seasons)
        for i, keys in enumerate(cands_, 1):
            ser.seasons.append(Season(number=last_no + i, episodes=keys, attached=list(keys)))
            assigned.update(keys)

    # 5. one copy per episode (a TV-run upload and a later re-upload of the same story)
    for ser in series:
        for se in ser.seasons:
            se.episodes, dups = _dedupe(se.episodes, positions_of(playlists), facts, ser.key, stop)
            se.duplicates = dups
            se.attached = [k for k in se.attached if k not in dups]

    # 5b. attaching can reveal an ongoing show; ongoing shows get a season per year
    for ser in series:
        if ser.kind == "episodic" and _is_ongoing(
            [k for se in ser.seasons for k in se.episodes], facts, cfg, ser.key
        ):
            ser.kind = "ongoing"
        if ser.kind == "ongoing":
            ser.seasons = _by_year(ser.seasons, facts)

    # 5b'. title-brand shows that playlist detection scattered are rebuilt from their own
    #      playlists ("شاهد على العصر | <guest>", "بلا حدود - 2005")
    phrases = [fold(w) for w in presenter_words]
    if _apply_brands(series, videos, playlists, facts, presenter, cfg, phrases):
        assigned = {
            k
            for ser in series
            for se in ser.seasons
            for k in (*se.episodes, *se.extras, *se.duplicates)
        }

    # 5c. one place per video: a video in two playlists can be an episode of one season
    #     and fall outside another playlist's length band (an "extra" there). The episode
    #     wins; an extra, a duplicate upload or a clip belongs to one season only.
    as_episode = {k for ser in series for se in ser.seasons for k in se.episodes}
    placed: set[str] = set(as_episode)
    for ser in series:
        for se in ser.seasons:
            se.extras = [k for k in dict.fromkeys(se.extras) if k not in placed]
            placed.update(se.extras)
            se.duplicates = [k for k in dict.fromkeys(se.duplicates) if k not in as_episode]

    # 6. order + stats
    by_id = {p.playlist_id: p for p in playlists}
    for ser in series:
        for se in ser.seasons:
            # the season's own playlists, biggest first: a nested topic list ("موسى ﷺ في
            # القرآن") must not renumber the series playlist's order
            own_pls = sorted(
                (by_id[pid] for pid in dict.fromkeys(se.playlists) if pid in by_id),
                key=lambda p: -len(p.members),
            )
            positions = positions_of([*own_pls, *playlists])
            primary = len(own_pls) <= 1 or len(own_pls[0].members) >= 2 * len(own_pls[1].members)
            if ser.brand:
                pass  # brand seasons arrive ordered (umbrella playlist, then title numbers)
            elif ser.kind == "ongoing":
                se.episodes = sorted(
                    se.episodes, key=lambda k: (facts[k].air_date or date.max, facts[k].title)
                )
            else:
                se.episodes = _order(se.episodes, positions, facts, multi_playlist=not primary)
            se.extras = sorted(
                set(se.extras), key=lambda k: (facts[k].air_date or date.max, facts[k].title)
            )
            se.numbers = title_numbers(se.episodes, facts)
            se.first_air, se.last_air = _date_span(se.episodes, facts)
            if se.year and not any(facts[k].date_exact for k in se.episodes):
                # the playlist says the year; approximate upload dates ("5 years ago") don't
                se.first_air, se.last_air = date(se.year, 1, 1), date(se.year, 12, 31)
            se.median_duration_s = _median([d for k in se.episodes if (d := facts[k].duration_s)])
        ser.seasons.sort(key=lambda x: x.number)
        # a playlist named apart from its episodes ("سيرة الرسول ﷺ" holding "كنوز السيرة N")
        # is named the way the episode titles name the show
        eps_all = [k for se in ser.seasons for k in se.episodes]
        own = [w for w in fold(ser.name).split() if len(w) > 2]
        if ser.kind != "clips" and not ser.brand and eps_all and own:
            carried = sum(all(w in fold(facts[k].title) for w in own) for k in eps_all)
            if carried < 0.3 * len(eps_all):
                named = _title_name(eps_all, facts, presenter | cfg.generic_words)
                # never the name another show of this channel already has: two shows of
                # one name would land in one Plex collection / dizqueTV show
                taken = {fold(o.name) for o in series if o is not ser}
                if named and fold(named) not in taken:
                    ser.name = named

    unassigned = sorted(k for k in facts if k not in assigned)
    series = [s for s in series if s.episode_count > 0]
    _distinct_names(series, playlists, presenter, cfg)
    series.sort(key=lambda s: (s.seasons[0].first_air or date.max, s.name))
    topics = _topics(series, playlists, facts, cfg)
    return Detection(series=series, unassigned=unassigned, shorts=shorts, topics=topics)


def _overlaps(a: _Seed, b: _Seed, cfg: DetectorConfig) -> bool:
    if not (a.first and a.last and b.first and b.last):
        return False
    return a.first <= b.last + timedelta(
        days=cfg.date_window_days
    ) and b.first <= a.last + timedelta(days=cfg.date_window_days)


# --------------------------------------------------------------------------- brand shows

#: separators that set a show's name apart in a title ("شاهد على العصر | …", "… - بلا حدود")
_BRAND_SPLIT_RE = re.compile(r"\s*(?:[|｜¦⧸/\\:：–—]|\s-\s|\s-|-\s|\(|\)|（|）|\[|\]|«|»|\.\.)\s*")
#: playlist words that only say "episodes aired in year …" ("حلقات بلا حدود التى تم بثها فى سنة")
_YEAR_FILLER = frozenset(
    fold(w)
    for w in (
        "حلقات", "حلقه", "حلقة", "التي", "التى", "تم", "بثها", "سنه", "سنة", "عام", "العام", "في",
        "فى", "من", "حتى", "حتي", "وحتى", "الى", "إلى", "مع",
    )
)  # fmt: skip
#: an umbrella playlist holds the whole show ("شاهد على العصر كاملا من البداية فبراير 1999")
_UMBRELLA_RE = re.compile(r"(?<!\w)(كامل[اه]?|من البدايه|جميع|كل حلقات)(?!\w)")
#: brand-free leftovers of a season playlist's title that carry no name
_QUAL_DROP = frozenset(fold(w) for w in ("حلقة", "حلقات", "حلقه", "و", "مع", "-"))
_PUNCT = "|｜:.-()\"'«»،,؟?!#_"
#: a clip list wherever the words sit ("بلا حدود | مقاطع مختارة من أهم الحلقات …")
_CLIPS_ANY_RE = re.compile(r"(?<!\w)(مقاطع|مقتطفات|مختارات|shorts|clips|highlights)(?!\w)")


def _plain(title: str | None) -> str:
    """Hashtags read as words: "#فاضل_العزاوي" → "فاضل العزاوي"."""
    return (title or "").replace("_", " ").replace("#", " ")


def _drop_phrases(words: list[str], phrases: Sequence[str]) -> list[str]:
    """Words without any presenter phrase ("أحمد منصور") and a "مع" just before it; a
    guest who shares a first name with the presenter ("أحمد بن بلة") keeps it."""
    folded = [fold(w) for w in words]
    drop: set[int] = set()
    for ph in phrases:
        pw = ph.split()
        if not pw:
            continue
        for i in range(len(folded) - len(pw) + 1):
            if folded[i : i + len(pw)] == pw:
                drop.update(range(i, i + len(pw)))
                if i > 0 and folded[i - 1] == fold("مع"):
                    drop.add(i - 1)
    return [w for i, w in enumerate(words) if i not in drop]


def _ns(text: str) -> str:
    """Folded, without spaces, digits or punctuation: "بلاحدود" == "بلا حدود"."""
    return re.sub(r"[\W\d_]+", "", fold(text))


def _brand_key(seg: str, presenter: frozenset[str], generic: frozenset[str]) -> str | None:
    """The show name a title segment could be: presenter words, markers and numbers
    out, generic words trimmed off the ends ("بلا حدود مع أحمد منصور" → "بلا حدود")."""
    words = [w for w in strip_markers(seg).split() if w not in presenter and not w.isdigit()]
    words = [w for w in words if re.sub(r"[\W\d_]+", "", w)]
    while words and words[0] in generic:
        words.pop(0)
    while words and words[-1] in generic:
        words.pop()
    if not words or all(w in generic for w in words):
        return None
    key = _ns(" ".join(words))
    return key if len(key) >= 6 else None


def find_brands(
    videos: Sequence[VideoFacts], presenter: frozenset[str], cfg: DetectorConfig
) -> list[tuple[str, str, list[str]]]:
    """Title brands of a channel, biggest first: ``(key, display name, video keys)``.

    A segment many titles share ("شاهد على العصر", carried by 800 of 2,200 videos) names
    a show. Brands are taken greedily, biggest first, each video belonging to the first
    brand its title carries, so a guest's name inside one show ("الشيخ أحمد ياسين | شاهد
    على العصر") does not become a show of its own."""
    seen: dict[str, Counter[str]] = defaultdict(Counter)  # key → original spellings
    per_video: dict[str, set[str]] = {}
    slot: dict[str, list[int]] = defaultdict(list)  # which segment of the title it is
    for v in videos:
        keys = set()
        segs = [x for x in _BRAND_SPLIT_RE.split(v.title or "") if x.strip()]
        for i, seg in enumerate(segs):
            k = _brand_key(seg, presenter, cfg.generic_words)
            if k and k not in keys:
                keys.add(k)
                seen[k][" ".join(seg.split())] += 1
                slot[k].append(i)
        per_video[v.key] = keys
    titles_ns = {v.key: _ns(v.title or "") for v in videos}
    need = max(cfg.brand_min, math.ceil(cfg.brand_share * len(videos)))
    out: list[tuple[str, str, list[str]]] = []
    free = set(titles_ns)
    while True:
        counts: Counter[str] = Counter(k for vk in free for k in per_video[vk])
        # equal counts: the segment titles lead with names the show ("بلا حدود | حوار …")
        ranked = sorted(counts.items(), key=lambda kv: (-kv[1], _median(slot[kv[0]]) or 0.0))
        key = next((k for k, n in ranked if n >= need), None)
        if key is None:
            break
        members = sorted(vk for vk in free if key in titles_ns[vk])
        if len(members) < need:
            break
        spelled = seen[key].most_common(1)[0][0]
        name = " ".join(w for w in spelled.split() if fold(w) not in presenter)
        name = re.sub(r"\s*\(?[\d٠-٩]+\)?\s*", " ", name)
        name = " ".join(name.split()).strip(" |｜-–/⧸.")
        while name.split() and fold(name.split()[-1]) in cfg.generic_words:
            name = " ".join(name.split()[:-1])
        out.append((key, name or spelled, members))
        free -= set(members)
    return out


def _scattered(
    key: str, members: Sequence[str], series: Sequence[Series], cfg: DetectorConfig
) -> bool:
    """Did playlist detection scatter this brand over many series, or file it under
    other names? A show it already holds together ("كنوز السيرة") is left alone."""
    where: Counter[int] = Counter()
    named = 0
    eps = {k: ser for ser in series for se in ser.seasons for k in se.episodes}
    for k in members:
        ser = eps.get(k)
        if ser is None:
            continue
        where[id(ser)] += 1
        if key in _ns(ser.name):
            named += 1
    placed = sum(where.values())
    if placed < cfg.brand_named_share * len(members):
        return True  # most of the show sits in no season at all
    many = sum(1 for n in where.values() if n >= 2) >= cfg.brand_scattered_series
    # one series holds (nearly) all of it, whatever its playlist called it ("سيرة الرسول ﷺ"
    # holding "كنوز السيرة N"): together
    together = max(where.values()) >= cfg.brand_named_share * placed
    return many or not (together or named >= cfg.brand_named_share * placed)


def _qualifier_of(title: str, key: str, phrases: Sequence[str]) -> str:
    """A season playlist's title without the show's name: "شاهد على العصر | وليد جنبلاط"
    → "وليد جنبلاط", "13حلقة وحوار مع د.عبدالله النفيسي -بلا حدود" → "وحوار مع
    د.عبدالله النفيسي"."""
    parts = []
    for seg in _BRAND_SPLIT_RE.split(title):
        if not seg.strip():
            continue
        if key in _ns(seg):
            # the brand inside a longer segment ("حلقات بلا حدود التى …"): keep the rest
            seg = " ".join(w for w in seg.split() if _ns(w) and _ns(w) not in key)
        words = _drop_phrases(seg.split(), phrases)
        while words:
            first = re.sub(r"^[\d٠-٩]+", "", words[0])
            if not first or fold(first) in _QUAL_DROP:
                words.pop(0)
                continue
            words[0] = first
            break
        if words:
            parts.append(" ".join(words))
    return " - ".join(parts).strip(" |｜-–/⧸.")


def _years_in(title: str) -> list[int]:
    return [int(y) for y in re.findall(r"(?<!\d)(19[89]\d|20[0-4]\d)(?!\d)", fold(title))]


def _brand_series(
    key: str,
    name: str,
    titled: Sequence[str],
    claimed_elsewhere: set[str],
    playlists: Sequence[PlaylistFacts],
    facts: dict[str, VideoFacts],
    presenter: frozenset[str],
    cfg: DetectorConfig,
    channel_df: Counter[str],
    phrases: Sequence[str] = (),
) -> Series:
    """One title-brand show: seasons from its playlists (a guest, a year), ordered by
    its umbrella playlist(s), the rest grouped by a shared guest name, else by year."""
    clip_re = re.compile(cfg.clip_playlist_re)
    umbrellas: list[PlaylistFacts] = []
    seasons_pl: list[tuple[PlaylistFacts, str | None, int | None]] = []
    titled_set = set(titled)
    for p in playlists:
        if key not in _ns(p.title) or clip_re.search(fold(p.title)):
            continue
        if _CLIPS_ANY_RE.search(fold(p.title)):
            continue  # a clip list of the show, not a season
        members = [k for k, _ in p.members if k in facts]
        if len(members) < cfg.min_season_size:
            continue
        years = sorted(set(_years_in(p.title)))
        if _UMBRELLA_RE.search(fold(p.title)) or len(years) >= 2:
            umbrellas.append(p)  # the whole show, or a run of years
            continue
        qual = _qualifier_of(p.title, key, phrases)
        qual_words = [
            w
            for w in fold(re.sub(r"[\d٠-٩]+", " ", qual)).split()
            if w not in _YEAR_FILLER and _ns(w)
        ]
        if len(years) == 1 and not qual_words:
            seasons_pl.append((p, None, years[0]))
        elif qual_words:
            seasons_pl.append((p, qual, years[0] if len(years) == 1 else None))
        elif len(set(members) & titled_set) >= 0.4 * len(titled_set):
            umbrellas.append(p)  # just the show's name, holding much of it
    # global order: umbrellas by their first year, then their positions
    umbrellas.sort(key=lambda p: (min(_years_in(p.title), default=9999), -len(p.members)))
    gpos: dict[str, tuple[int, float]] = {}
    for i, p in enumerate(umbrellas):
        for k, pos in p.members:
            if k in facts and k not in gpos:
                gpos[k] = (i, float(pos if pos is not None else 1e6))
    members_all = set(titled_set)
    for p in [*umbrellas, *(x[0] for x in seasons_pl)]:
        members_all |= {k for k, _ in p.members if k in facts and k not in claimed_elsewhere}
    # each video in the smallest season playlist holding it
    owner: dict[str, int] = {}
    for i, (p, _, _) in sorted(enumerate(seasons_pl), key=lambda x: len(x[1][0].members)):
        for k, _ in p.members:
            if k in members_all and k not in owner:
                owner[k] = i
    seasons: list[Season] = []
    for i, (p, qual, year) in enumerate(seasons_pl):
        eps = [k for k, _ in p.members if owner.get(k) == i]
        if len(eps) < cfg.min_season_size:
            for k in eps:
                del owner[k]  # too few for a season of its own: grouped with the rest
            continue
        if eps:
            label = qual if qual is not None else (str(year) if year is not None else None)
            seasons.append(
                Season(number=0, episodes=eps, playlists=[p.playlist_id], name=label, year=year)
            )
    rest = sorted(k for k in members_all if k not in owner)
    for extra in _group_rest(rest, key, facts, presenter, cfg, channel_df, phrases):
        # a guest's other episodes join the season his playlist already named
        same = next(
            (
                se
                for se in seasons
                if extra.name
                and se.name
                and extra.name != "حلقات أخرى"
                and (fold(extra.name) in fold(se.name) or fold(se.name) in fold(extra.name))
            ),
            None,
        )
        if same is not None:
            same.episodes += extra.episodes
        else:
            seasons.append(extra)
    pos_of = {p.playlist_id: p for p in playlists}
    for se in seasons:
        own_pos = {
            k: float(pos)
            for pid in se.playlists
            if pid in pos_of
            for k, pos in pos_of[pid].members
            if pos is not None
        }

        def order(k: str, own_pos: dict[str, float] = own_pos) -> tuple[float, ...]:
            g = gpos.get(k)
            n = _title_number(facts[k].title)
            return (
                g[0] if g else math.inf,
                g[1] if g else math.inf,
                float(n) if n is not None else math.inf,
                own_pos.get(k, math.inf),
                float((facts[k].air_date or date.max).toordinal()),
            )

        teasers = [k for k in se.episodes if is_teaser(facts[k].title, facts[k].duration_s)]
        se.extras = teasers
        se.episodes = sorted((k for k in se.episodes if k not in teasers), key=order)
        se.playlists += [p.playlist_id for p in umbrellas]
    seasons = [se for se in seasons if se.episodes]

    def season_order(se: Season) -> tuple[float, ...]:
        # a year first (a talk show's year seasons in air order), then the umbrella
        # order (a guest's run where the full-show playlist puts it), then exact dates
        firsts = [gpos[k] for k in se.episodes if k in gpos]
        g = min(firsts) if firsts else (math.inf, math.inf)
        ds = [
            d.toordinal() for k in se.episodes if (d := facts[k].air_date) and facts[k].date_exact
        ]
        return (float(se.year or 9999), g[0], g[1], float(min(ds) if ds else math.inf))

    seasons.sort(key=season_order)
    for i, se in enumerate(seasons, 1):
        se.number = i
    n_eps = sum(len(se.episodes) for se in seasons)
    kind = "ongoing" if n_eps >= cfg.ongoing_min_episodes and len(seasons) > 1 else "episodic"
    return Series(name=name, key=fold(name), seasons=seasons, kind=kind, brand=True)


def _title_words(title: str, stop: frozenset[str], brand: str) -> list[str]:
    out = []
    for w in _plain(title).split():
        f = fold(w).strip(_PUNCT)
        digits = any(ch.isdigit() for ch in f)
        if len(f) > 2 and f not in stop and not digits and _ns(f) not in brand:
            out.append(f)
    return out


def _group_rest(
    keys: Sequence[str],
    brand: str,
    facts: dict[str, VideoFacts],
    presenter: frozenset[str],
    cfg: DetectorConfig,
    channel_df: Counter[str],
    phrases: Sequence[str] = (),
) -> list[Season]:
    """Brand episodes no season playlist holds. A guest's run ("عدنان الباجة جي …(6)",
    "… الباجة جي يتحدث …(9)") shares a name found almost nowhere else on the channel;
    else exact air dates give the year; the rest wait together as "حلقات أخرى"."""
    stop = cfg.generic_words | presenter | _YEAR_FILLER
    words_of = {k: _title_words(facts[k].title, stop, brand) for k in keys}
    df: Counter[str] = Counter(w for ws in words_of.values() for w in set(ws))
    min_run = max(4, cfg.min_season_size)

    def name_like(w: str) -> bool:
        # carried by a run of these episodes and (nearly) nowhere else on the channel;
        # "مقاطع مختارة" is a clip list, not a guest
        return (
            min_run <= df[w] <= 60
            and channel_df[w] <= 1.25 * df[w] + 1
            and not _CLIPS_ANY_RE.search(w)
            and fold("مختاره") != w
        )

    # where in the title a word sits: a guest's name comes early ("الباجه جي يتحدث …")
    place: dict[str, list[int]] = defaultdict(list)
    for ws in words_of.values():
        for i, w in enumerate(ws):
            place[w].append(i)
    group_of: dict[str, str] = {}
    for k, ws in words_of.items():
        cands = [w for w in set(ws) if name_like(w)]
        if cands:  # the guest's whole run: the most shared, earliest word
            group_of[k] = max(cands, key=lambda w: (df[w], -_median(place[w]) or 0.0, w))
    sizes = Counter(group_of.values())
    out: list[Season] = []
    by_group: dict[str, list[str]] = defaultdict(list)
    left: list[str] = []
    for k in keys:
        g = group_of.get(k)
        if g and sizes[g] >= min_run:
            by_group[g].append(k)
        else:
            left.append(k)
    for g, ks in by_group.items():
        # the guest's name as titles write it: the word, with its neighbour when most
        # titles carry both ("عدنان الباجة")
        pairs: Counter[str] = Counter()
        singles: Counter[str] = Counter()
        for k in ks:
            ws = [w.strip(_PUNCT) for w in _plain(facts[k].title).split()]
            for i, w in enumerate(ws):
                if fold(w) != g:
                    continue
                singles[w] += 1
                for a, b in ((i - 1, i), (i, i + 1)):
                    if a >= 0 and b < len(ws):
                        other = ws[b] if a == i else ws[a]
                        pair = f"{fold(ws[a])} {fold(ws[b])}"
                        fo = fold(other)
                        if (
                            len(fo) > 2
                            and fo not in cfg.generic_words | _YEAR_FILLER
                            and not any(pair in ph or ph in pair for ph in phrases)
                            # glued words ("منصورمع") and the show's own name stay out
                            and not any(len(pw) > 3 and pw in fo for pw in presenter)
                            and _ns(fo) not in brand
                        ):
                            pairs[f"{ws[a]} {ws[b]}"] += 1
        pair = pairs.most_common(1)
        if pair and pair[0][1] >= 0.6 * len(ks):
            label = pair[0][0]
        else:
            label = singles.most_common(1)[0][0] if singles else g
        out.append(Season(number=0, episodes=ks, name=label))
    by_year: dict[int, list[str]] = defaultdict(list)
    other: list[str] = []
    for k in left:
        d = facts[k].air_date
        if d is not None and facts[k].date_exact:
            by_year[d.year].append(k)
        else:
            other.append(k)
    for y, ks in sorted(by_year.items()):
        if len(ks) >= cfg.min_season_size:
            out.append(Season(number=0, episodes=ks, name=str(y), year=y))
        else:
            other += ks
    if other:
        out.append(Season(number=0, episodes=sorted(other), name="حلقات أخرى"))
    return out


def _under_split(
    cand: Series, members: Sequence[str], series: Sequence[Series], cfg: DetectorConfig
) -> bool:
    """The channel files the show by guest or year (several season playlists) but
    playlist detection folded it into far fewer seasons (one season of 600 episodes)."""
    if len(cand.seasons) < 2:
        return False
    common = set.intersection(*(set(se.playlists) for se in cand.seasons))
    from_playlists = sum(1 for se in cand.seasons if se.playlists and se.playlists[0] not in common)
    if from_playlists < 2:
        return False
    member_set = set(members)
    held = sum(
        1
        for ser in series
        for se in ser.seasons
        if len(member_set.intersection(se.episodes)) >= cfg.min_season_size
    )
    return held * 2 < from_playlists


def _apply_brands(
    series: list[Series],
    videos: Sequence[VideoFacts],
    playlists: Sequence[PlaylistFacts],
    facts: dict[str, VideoFacts],
    presenter: frozenset[str],
    cfg: DetectorConfig,
    phrases: Sequence[str] = (),
) -> bool:
    """Rebuild the brands playlist detection scattered; their videos leave the other
    series, and a series that loses most of its episodes to them is dissolved.
    Returns whether anything changed."""
    brands = find_brands(videos, presenter, cfg)
    if not brands:
        return False
    all_titled = {vk for _, _, m in brands for vk in m}
    stop = cfg.generic_words | presenter | _YEAR_FILLER
    channel_df: Counter[str] = Counter(
        w for v in videos for w in set(_title_words(v.title, stop, ""))
    )
    built: list[Series] = []
    taken: set[str] = set()
    for key, name, members in brands:
        elsewhere = (all_titled - set(members)) | taken
        ser = _brand_series(
            key, name, members, elsewhere, playlists, facts, presenter, cfg, channel_df, phrases
        )
        if not ser.episode_count:
            continue
        if not (_scattered(key, members, series, cfg) or _under_split(ser, members, series, cfg)):
            continue  # playlist detection already holds the show together
        built.append(ser)
        taken |= {k for se in ser.seasons for k in (*se.episodes, *se.extras)}
    if not built:
        return False
    kept: list[Series] = []
    for ser in series:
        before = ser.episode_count
        for se in ser.seasons:
            se.episodes = [k for k in se.episodes if k not in taken]
            se.extras = [k for k in se.extras if k not in taken]
            se.attached = [k for k in se.attached if k not in taken]
            se.duplicates = [k for k in se.duplicates if k not in taken]
        ser.seasons = [se for se in ser.seasons if se.episodes]
        if ser.episode_count and ser.episode_count * 2 > before:
            kept.append(ser)
    series[:] = kept + built
    return True


def _topics(
    series: Sequence[Series],
    playlists: Sequence[PlaylistFacts],
    facts: dict[str, VideoFacts],
    cfg: DetectorConfig,
) -> list[Topic]:
    """Curated playlists that cut across shows: their episodes sit in two or more
    series (or three or more seasons of one), and the playlist is no season's own."""
    season_of: dict[str, tuple[int, int]] = {}
    for ser in series:
        if ser.kind == "clips":
            continue
        for se in ser.seasons:
            for k in se.episodes:
                season_of[k] = (id(ser), se.number)
    own = {se.playlists[0] for ser in series for se in ser.seasons if se.playlists}
    # a brand show's playlists (its umbrella, its guests) are the show itself
    brand_pls = {pid for ser in series if ser.brand for se in ser.seasons for pid in se.playlists}
    clip_re = re.compile(cfg.clip_playlist_re)
    out: list[Topic] = []
    for p in playlists:
        title = fold(p.title)
        if clip_re.search(title) or re.search(cfg.catch_all_re, title):
            continue
        if p.playlist_id in brand_pls:
            continue
        items = [
            k for k, _ in sorted(p.members, key=lambda m: (m[1] is None, m[1] or 0)) if k in facts
        ]
        if len(items) < cfg.topic_min:
            continue
        hits = [season_of[k] for k in items if k in season_of]
        if len(hits) < 0.5 * len(items):
            continue
        biggest = Counter(hits).most_common(1)[0][1]
        if p.playlist_id in own and biggest >= 0.8 * len(hits):
            continue  # the season's own playlist
        if len({h[0] for h in hits}) >= 2 or len(set(hits)) >= 3:
            out.append(Topic(" ".join(p.title.split()), p.playlist_id, items))
    return out


def group_by_season_key(det: Detection) -> dict[str, list[Series]]:
    out: dict[str, list[Series]] = defaultdict(list)
    for s in det.series:
        out[s.key].append(s)
    return out
