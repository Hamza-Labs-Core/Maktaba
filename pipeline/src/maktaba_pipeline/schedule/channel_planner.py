"""Channel planner: which live channels a library supports, and their daypart grids.

Pure (no DB, no I/O). Input is what series detection (Story 26.3), topic
tagging and the production-style pass know about a library:

- :class:`SeasonCandidate` — one detected season (a ``series`` row) with its
  ordered episodes, namespaced tags (``topic:``/``format:``/``audience:``/
  ``occasion:``), primary genre and production tier (or ``None`` when the
  style pass has not seen it);
- :class:`LooseVideo` — a video in no series (an unassigned upload or a
  short), with its own tags.

:func:`suggest_channels` returns a :class:`PlanResult`: themed channels (one
per topic family with enough hours), a Mix channel and per-presenter
channels. Every channel carries a weekly time-slot grid over *lanes* —
ordered runs of seasons of one production tier, played episode by episode,
season by season, never interleaved — plus filler pools that pad each slot to
its boundary. The rules a network programmer would apply:

- **Tiers are grouped, never alternated.** A lane holds one tier group
  (static / talk / produced / cinematic / unknown). Produced and cinematic
  lanes take the evening and prime time; talk lanes take daytime and late
  night. Unknown tier is its own group, ranked by format (a ``program`` ranks
  above a ``lecture``).
- **Order is sacred.** Within a lane, series play in first-air order, seasons
  in season order, episodes in episode order.
- **Quran at dawn**, a **Friday khutbah** slot, and a separate **Ramadan grid**
  that moves Ramadan-tagged seasons to the post-iftar prime slot.
- **Filler** (short-series episodes, highlight-clip series, shorts, short loose
  videos) is pooled per channel and per daypart role, kept on-topic with the
  videos' own tags.

The result maps onto Epic 27 directly: each channel becomes a ``schedule``
mode channel (:func:`maktaba_channel_definition`, the slot-0081 shape the Go
validator accepts and :class:`~maktaba_pipeline.channels.planner.schedule.
SchedulePlanner` reads), and onto dizqueTV's time-slot tool
(:func:`dizquetv_time_slots`). :func:`simulate` lays a sample day out the way
both schedulers do (next episode while it fits, filler to the boundary) so a
reviewer sees how full each slot really is.

Every threshold lives in :class:`PlannerConfig`; every suggestion carries
``reasons``.
"""

from __future__ import annotations

import re
from collections import defaultdict
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from statistics import median
from typing import Any

__all__ = [
    "DAY_MINUTES",
    "DEFAULT_DAYPARTS",
    "DEFAULT_RAMADAN_DAYPARTS",
    "DEFAULT_THEMES",
    "PLANNER_VERSION",
    "TIER_NAMES",
    "WEEKDAYS",
    "Airing",
    "ChannelSuggestion",
    "Daypart",
    "EpisodeRef",
    "FillerItem",
    "FillerPool",
    "Lane",
    "LooseVideo",
    "PlanResult",
    "PlannerConfig",
    "ProgramUnit",
    "SeasonCandidate",
    "SeriesCandidate",
    "Slot",
    "ThemeSpec",
    "dizquetv_time_slots",
    "fmt_hhmm",
    "group_series",
    "maktaba_channel_definition",
    "simulate",
    "suggest_channels",
]

PLANNER_VERSION: int = 1
DAY_MINUTES = 24 * 60
WEEKDAYS: tuple[str, ...] = ("mon", "tue", "wed", "thu", "fri", "sat", "sun")
TIER_NAMES: dict[int, str] = {0: "static", 1: "talk", 2: "produced", 3: "cinematic"}
UNKNOWN = "unknown"


# --------------------------------------------------------------------------- inputs


@dataclass(frozen=True, slots=True)
class EpisodeRef:
    """One playable file in a season, in broadcast order."""

    path: str
    duration_s: float
    title: str = ""
    season: int | None = None
    episode: int | None = None
    plex_rating_key: str | None = None
    plex_library: str | None = None
    air_date: str | None = None


@dataclass(frozen=True, slots=True)
class SeasonCandidate:
    """One detected season (a ``series`` row) and what is known about it.

    ``tags`` maps ``namespace:name`` → score (0..1). ``tier`` is the
    production tier (0 static, 1 talk, 2 produced, 3 cinematic) or ``None``
    when the style pass has not classified it. ``kind`` is ``episodic``,
    ``ongoing`` (a show that keeps going; one season per year),
    ``clips`` (highlight-clip playlists) or ``loose`` (a synthetic run of
    unassigned uploads, see :func:`suggest_channels`).
    """

    series_id: str
    name: str
    episodes: tuple[EpisodeRef, ...]
    season: int = 1
    kind: str = "episodic"
    series_key: str = ""
    folder: str = ""
    presenter: str = ""
    library: str = ""
    year: int | None = None
    first_air: str | None = None
    median_duration_s: float | None = None
    tags: Mapping[str, float] = field(default_factory=dict)
    genre: str = "general"
    style: str | None = None
    tier: int | None = None
    style_confidence: float = 0.0

    @property
    def duration_s(self) -> float:
        return sum(e.duration_s for e in self.episodes)

    @property
    def median_s(self) -> float:
        if self.median_duration_s:
            return self.median_duration_s
        durs = [e.duration_s for e in self.episodes if e.duration_s > 0]
        return float(median(durs)) if durs else 0.0


@dataclass(frozen=True, slots=True)
class LooseVideo:
    """A video in no series: an unassigned upload or a short."""

    path: str
    duration_s: float
    title: str = ""
    folder: str = ""
    presenter: str = ""
    tags: Mapping[str, float] = field(default_factory=dict)
    genre: str = "general"
    air_date: str | None = None
    plex_rating_key: str | None = None
    plex_library: str | None = None
    is_short: bool = False
    vertical: bool = False


@dataclass(frozen=True, slots=True)
class SeriesCandidate:
    """The seasons of one series (same folder + series key), in season order."""

    key: str
    name: str
    presenter: str
    seasons: tuple[SeasonCandidate, ...]

    @property
    def first_air(self) -> str:
        return min((s.first_air or "9999") for s in self.seasons)


def group_series(seasons: Iterable[SeasonCandidate]) -> list[SeriesCandidate]:
    """Group season rows into series (folder + series key + kind), seasons in
    order, series in first-air order."""
    groups: dict[tuple[str, str, str], list[SeasonCandidate]] = defaultdict(list)
    for s in seasons:
        groups[(s.folder, s.series_key or s.name, s.kind)].append(s)
    out = [
        SeriesCandidate(
            key=f"{folder}|{key}|{kind}",
            name=ss[0].name,
            presenter=ss[0].presenter,
            seasons=tuple(sorted(ss, key=lambda x: (x.season, x.first_air or ""))),
        )
        for (folder, key, kind), ss in groups.items()
    ]
    out.sort(key=lambda s: (s.first_air, s.name, s.key))
    return out


# --------------------------------------------------------------------------- config


@dataclass(frozen=True, slots=True)
class ThemeSpec:
    """A themed channel candidate. A season belongs to the theme when its
    genre is one of ``topics``, or any ``topic:`` score in ``topics`` (or
    ``audience:`` score in ``audiences``, ``format:`` score in ``formats``)
    reaches :attr:`PlannerConfig.topic_min_score`."""

    key: str
    name: str
    name_ar: str
    number: int
    topics: tuple[str, ...] = ()
    audiences: tuple[str, ...] = ()
    formats: tuple[str, ...] = ()
    category: str = "general"
    min_hours: float | None = None  # None → PlannerConfig.theme_min_hours


DEFAULT_THEMES: tuple[ThemeSpec, ...] = (
    ThemeSpec(
        "quran",
        "Quran",
        "القرآن الكريم",
        101,
        ("quran_recitation", "tajweed"),
        formats=("recitation",),
        category="quran",
    ),  # fmt: skip
    ThemeSpec("hadith", "Hadith", "الحديث", 102, ("hadith",), category="islamic"),
    ThemeSpec("seerah", "Seerah", "السيرة النبوية", 103, ("seerah",), category="islamic"),
    ThemeSpec(
        "prophets",
        "Prophets' Stories",
        "قصص الأنبياء",
        104,
        ("prophets_stories",),
        category="islamic",
    ),  # fmt: skip
    ThemeSpec("tafsir", "Tafsir", "التفسير", 105, ("tafsir",), category="islamic"),
    ThemeSpec(
        "raqaiq", "Raqaiq", "الرقائق", 106, ("raqaiq", "akhirah", "dua_dhikr"), category="islamic"
    ),  # fmt: skip
    ThemeSpec(
        "aqeedah",
        "Aqeedah & Shubuhat",
        "العقيدة والشبهات",
        107,
        ("aqeedah", "shubuhat"),
        category="islamic",
    ),  # fmt: skip
    ThemeSpec(
        "history",
        "Islamic History",
        "التاريخ الإسلامي",
        108,
        ("islamic_history", "sahaba"),
        category="documentaries",
    ),  # fmt: skip
    ThemeSpec(
        "family", "Family", "الأسرة", 109, ("family_marriage", "women", "youth"), category="islamic"
    ),  # fmt: skip
    ThemeSpec("kids", "Kids", "الأطفال", 110, audiences=("kids",), category="kids"),
    ThemeSpec(
        "fiqh",
        "Fiqh & Fatwa",
        "الفقه والفتاوى",
        111,
        ("fiqh",),
        formats=("qa_fatwa",),
        category="islamic",
    ),  # fmt: skip
    ThemeSpec("stories", "Stories", "القصص", 112, ("stories",), category="islamic"),
    ThemeSpec("akhlaq", "Akhlaq", "الأخلاق", 113, ("akhlaq", "dawah"), category="islamic"),
    ThemeSpec("nasheed", "Nasheed", "الأناشيد", 114, ("nasheed", "poetry"), category="music"),
)


@dataclass(frozen=True, slots=True)
class Daypart:
    """A daily window of the grid. ``end <= start`` wraps past midnight.

    ``role``: ``quran`` (recitation lane when the library has one), ``high``
    (produced/cinematic lanes first), ``low`` (talk/lecture lanes first) or
    ``ramadan`` (Ramadan-tagged lanes first; Ramadan grid only)."""

    key: str
    start: int  # minutes after local midnight
    end: int
    role: str
    label: str = ""

    @property
    def minutes(self) -> int:
        return (self.end - self.start) % DAY_MINUTES or DAY_MINUTES


def _hm(s: str) -> int:
    h, m = s.split(":")
    return int(h) * 60 + int(m)


DEFAULT_DAYPARTS: tuple[Daypart, ...] = (
    Daypart("dawn", _hm("04:00"), _hm("07:00"), "quran", "Dawn: Quran"),
    Daypart("morning", _hm("07:00"), _hm("12:00"), "low", "Morning"),
    Daypart("afternoon", _hm("12:00"), _hm("17:00"), "low", "Afternoon"),
    Daypart("evening", _hm("17:00"), _hm("20:00"), "high", "Evening"),
    Daypart("prime", _hm("20:00"), _hm("23:00"), "high", "Prime time"),
    Daypart("late", _hm("23:00"), _hm("04:00"), "low", "Late night"),
)

DEFAULT_RAMADAN_DAYPARTS: tuple[Daypart, ...] = (
    Daypart("suhoor", _hm("01:00"), _hm("04:00"), "low", "Suhoor"),
    Daypart("dawn", _hm("04:00"), _hm("06:00"), "quran", "Fajr: Quran"),
    Daypart("morning", _hm("06:00"), _hm("13:00"), "low", "Morning"),
    Daypart("afternoon", _hm("13:00"), _hm("17:00"), "low", "Afternoon"),
    Daypart("pre_iftar", _hm("17:00"), _hm("19:00"), "quran", "Before iftar: Quran"),
    Daypart("iftar_prime", _hm("19:00"), _hm("22:00"), "ramadan", "After iftar: Ramadan programs"),
    Daypart("night", _hm("22:00"), _hm("01:00"), "high", "After taraweeh"),
)


@dataclass(frozen=True, slots=True)
class PlannerConfig:
    """Every threshold the planner uses. Durations in minutes unless named ``_s``."""

    themes: tuple[ThemeSpec, ...] = DEFAULT_THEMES
    dayparts: tuple[Daypart, ...] = DEFAULT_DAYPARTS
    ramadan_dayparts: tuple[Daypart, ...] = DEFAULT_RAMADAN_DAYPARTS
    # theme membership
    topic_min_score: float = 0.6
    theme_min_hours: float = 10.0
    # mix channel
    mix_channel: bool = True
    mix_number: int = 100
    mix_name: str = "Mix"
    mix_name_ar: str = "منوعات"
    mix_min_hours: float = 4.0
    mix_exclude_topics: tuple[str, ...] = ("current_affairs",)
    # daypart → genres the Mix channel prefers there (series that match go to that lane)
    mix_daypart_topics: Mapping[str, tuple[str, ...]] = field(
        default_factory=lambda: {
            "morning": ("raqaiq", "tafsir", "dua_dhikr", "akhirah", "akhlaq"),
            "afternoon": (
                "fiqh",
                "hadith",
                "aqeedah",
                "shubuhat",
                "arabic_language",
                "women",
                "family_marriage",
            ),  # fmt: skip
            "evening": ("stories", "prophets_stories", "youth", "akhlaq", "dawah"),
            "prime": ("seerah", "prophets_stories", "islamic_history", "sahaba", "tafsir"),
            "late": ("raqaiq", "akhirah", "aqeedah", "shubuhat", "general"),
        }
    )
    # presenter channels
    presenter_channels: bool = True
    presenter_min_hours: float = 30.0
    presenter_min_genres: int = 3
    presenter_first_number: int = 150
    # what counts as a programme vs filler
    short_series_max_minutes: float = 9.0  # an episodic season with a shorter median is filler
    loose_programs: bool = True
    loose_program_min_minutes: float = 15.0
    loose_min_group_videos: int = 6
    loose_skip_genres: tuple[str, ...] = ("general", "current_affairs")
    # filler
    filler_max_minutes: float = 12.0
    filler_min_s: float = 20.0
    filler_topic_min_score: float = 0.4
    filler_exclude_topics: tuple[str, ...] = ("current_affairs",)
    generic_filler_topics: tuple[str, ...] = (
        "raqaiq", "akhlaq", "dua_dhikr", "akhirah", "stories", "seerah", "prophets_stories",
        "tafsir",
    )  # fmt: skip
    dawn_filler_topics: tuple[str, ...] = ("quran_recitation", "tajweed", "dua_dhikr", "raqaiq")
    include_vertical_shorts: bool = True
    filler_recent_window: int = 8  # don't repeat one of the last N fillers when another fits
    # special lanes
    recitation_min_score: float = 0.5
    khutbah_min_score: float = 0.5
    dawn_quran: bool = True
    friday_khutbah: bool = True
    khutbah_day: str = "fri"
    khutbah_start: int = _hm("12:00")
    khutbah_end: int = _hm("13:30")
    khutbah_only_friday: bool = True  # khutbah seasons leave the regular lanes
    # Ramadan
    ramadan_min_score: float = 0.6
    ramadan_exclusive: bool = False  # True: Ramadan seasons air only on the Ramadan grid
    ramadan_grid: bool = True
    # grid shape
    min_slot_minutes: int = 30
    slot_round_minutes: int = 30
    max_lanes_per_daypart: int = 2
    # unknown tier is ranked by format: these formats rank like "produced-ish"
    prime_unknown_formats: tuple[str, ...] = ("program", "documentary", "drama")
    format_min_score: float = 0.5
    timezone: str | None = None  # IANA name written into mode_config; None = server local
    # dizqueTV schedule knobs
    dz_lateness_ms: int = 15 * 60_000
    dz_max_days: int = 365
    dz_pad_ms: int = 1
    dz_filler_cooldown_ms: int = 30 * 60_000


# --------------------------------------------------------------------------- outputs


@dataclass(frozen=True, slots=True)
class ProgramUnit:
    """A season as the grid schedules it."""

    series_id: str
    name: str
    season: int
    presenter: str
    folder: str
    kind: str
    genre: str
    tier: int | None
    style: str | None
    ramadan: bool
    first_air: str
    series_key: str
    episodes: tuple[EpisodeRef, ...]

    @property
    def tier_group(self) -> str:
        return TIER_NAMES.get(self.tier, UNKNOWN) if self.tier is not None else UNKNOWN

    @property
    def duration_s(self) -> float:
        return sum(e.duration_s for e in self.episodes)

    @property
    def max_episode_s(self) -> float:
        return max((e.duration_s for e in self.episodes), default=0.0)

    @property
    def label(self) -> str:
        if self.season >= 1900:  # an ongoing show: its season is the year
            return f"{self.name} {self.season}"
        return (
            self.name
            if self.season <= 1 and self.kind != "loose"
            else f"{self.name} S{self.season}"
        )


@dataclass(frozen=True, slots=True)
class Lane:
    """An ordered run of seasons of one tier group, played sequentially."""

    key: str
    name: str
    tier_group: str
    units: tuple[ProgramUnit, ...]
    rank: float
    order: str = "sequential"
    ramadan: bool = False
    special: str = ""  # "" | "quran" | "khutbah"
    reasons: tuple[str, ...] = ()

    @property
    def duration_s(self) -> float:
        return sum(u.duration_s for u in self.units)

    @property
    def hours(self) -> float:
        return self.duration_s / 3600

    @property
    def episode_count(self) -> int:
        return sum(len(u.episodes) for u in self.units)

    @property
    def max_episode_s(self) -> float:
        return max((u.max_episode_s for u in self.units), default=0.0)

    def episodes(self) -> list[tuple[ProgramUnit, EpisodeRef]]:
        return [(u, e) for u in self.units for e in u.episodes]


@dataclass(frozen=True, slots=True)
class FillerItem:
    """A short video that pads slots to their boundary."""

    path: str
    duration_s: float
    title: str
    source: str  # short | clips | short_series | loose
    genre: str
    tags: Mapping[str, float]
    folder: str = ""
    presenter: str = ""
    plex_rating_key: str | None = None
    plex_library: str | None = None
    vertical: bool = False
    tier: int | None = None

    def topic_score(self, topics: Iterable[str]) -> float:
        ts = tuple(topics)
        if self.genre in ts:
            return 1.0
        return max((self.tags.get(f"topic:{t}", 0.0) for t in ts), default=0.0)


@dataclass(frozen=True, slots=True)
class FillerPool:
    key: str
    name: str
    items: tuple[FillerItem, ...]
    fallback: str | None = None
    reasons: tuple[str, ...] = ()

    @property
    def duration_s(self) -> float:
        return sum(i.duration_s for i in self.items)


@dataclass(frozen=True, slots=True)
class Slot:
    """One grid cell: ``[start, end)`` minutes on ``days`` (empty = every day)."""

    start: int
    end: int  # exclusive, ≤ 1440
    lane: str | None
    daypart: str
    filler_pool: str | None
    days: tuple[str, ...] = ()
    kind: str = "program"  # program | quran | khutbah | flex
    reasons: tuple[str, ...] = ()

    @property
    def minutes(self) -> int:
        return self.end - self.start

    def on(self, day: str) -> bool:
        return not self.days or day in self.days


@dataclass(frozen=True, slots=True)
class ChannelSuggestion:
    key: str
    number: int
    name: str
    name_ar: str
    kind: str  # theme | mix | presenter
    category: str
    lanes: tuple[str, ...]
    slots: tuple[Slot, ...]
    ramadan_slots: tuple[Slot, ...]
    filler_pools: tuple[str, ...]
    hours: float
    reasons: tuple[str, ...]
    warnings: tuple[str, ...] = ()


@dataclass(frozen=True, slots=True)
class Airing:
    """One block of a simulated day (seconds from the simulated day's midnight)."""

    start_s: float
    end_s: float
    kind: str  # program | filler | flex
    title: str
    lane: str | None = None
    path: str | None = None
    slot: int | None = None


@dataclass(frozen=True, slots=True)
class PlanResult:
    channels: tuple[ChannelSuggestion, ...]
    lanes: Mapping[str, Lane]
    filler_pools: Mapping[str, FillerPool]
    unplaced: tuple[tuple[str, str], ...]  # (label, why)
    config: PlannerConfig
    version: int = PLANNER_VERSION


# --------------------------------------------------------------------------- helpers


def fmt_hhmm(minutes: int) -> str:
    return f"{minutes // 60:02d}:{minutes % 60:02d}"


def _slug(s: str) -> str:
    s = re.sub(r"[^\w]+", "-", s.strip().lower(), flags=re.UNICODE).strip("-")
    return s or "x"


def _score(tags: Mapping[str, float], ns: str, names: Iterable[str]) -> float:
    return max((tags.get(f"{ns}:{n}", 0.0) for n in names), default=0.0)


def _format_score(tags: Mapping[str, float], fmt: str) -> float:
    return tags.get(f"format:{fmt}", 0.0)


def _is_recitation(s: SeasonCandidate, cfg: PlannerConfig) -> bool:
    return s.genre in ("quran_recitation", "tajweed") or (
        _format_score(s.tags, "recitation") >= cfg.recitation_min_score
        and _score(s.tags, "topic", ("quran_recitation", "tajweed")) >= cfg.recitation_min_score
    )


def _is_khutbah(s: SeasonCandidate, cfg: PlannerConfig) -> bool:
    return _format_score(s.tags, "khutbah") >= cfg.khutbah_min_score


def _in_theme(s: SeasonCandidate, t: ThemeSpec, cfg: PlannerConfig) -> bool:
    if s.genre in t.topics:
        return True
    return (
        _score(s.tags, "topic", t.topics) >= cfg.topic_min_score
        or _score(s.tags, "audience", t.audiences) >= cfg.topic_min_score
        or _score(s.tags, "format", t.formats) >= cfg.topic_min_score
    )


def _rank(units: Sequence[ProgramUnit], tags_of: Mapping[str, Mapping[str, float]],
          cfg: PlannerConfig) -> float:  # fmt: skip
    """Prime-time suitability of a tier group: the tier itself, or for the
    unknown group a format-based guess between talk (1) and produced (2)."""
    tiers = [u.tier for u in units if u.tier is not None]
    if tiers:
        return float(max(tiers))
    prog = sum(
        u.duration_s
        for u in units
        if _score(tags_of.get(u.series_id, {}), "format", cfg.prime_unknown_formats)
        >= cfg.format_min_score
    )
    total = sum(u.duration_s for u in units) or 1.0
    return 1.0 + 0.5 * (prog / total) + 0.01  # 1.01 … 1.51: between talk and produced


def _unit(s: SeasonCandidate, cfg: PlannerConfig) -> ProgramUnit:
    return ProgramUnit(
        series_id=s.series_id,
        name=s.name,
        season=s.season,
        presenter=s.presenter,
        folder=s.folder,
        kind=s.kind,
        genre=s.genre,
        tier=s.tier,
        style=s.style,
        ramadan=s.tags.get("occasion:ramadan", 0.0) >= cfg.ramadan_min_score,
        first_air=s.first_air or "9999",
        series_key=s.series_key or s.name,
        episodes=tuple(e for e in s.episodes if e.duration_s > 0),
    )


def _order_units(units: Iterable[ProgramUnit]) -> list[ProgramUnit]:
    """Series in first-air order (a series' first season dates it), seasons in order."""
    units = list(units)
    series_first: dict[tuple[str, str], str] = {}
    for u in units:
        k = (u.folder, u.series_key)
        series_first[k] = min(series_first.get(k, "9999"), u.first_air)
    return sorted(
        units,
        key=lambda u: (series_first[(u.folder, u.series_key)], u.folder, u.series_key, u.season),
    )


def _series_blocks(units: Sequence[ProgramUnit]) -> list[list[ProgramUnit]]:
    """Consecutive seasons of one series, in lane order."""
    blocks: list[list[ProgramUnit]] = []
    for u in _order_units(units):
        if blocks and (blocks[-1][0].folder, blocks[-1][0].series_key) == (u.folder, u.series_key):
            blocks[-1].append(u)
        else:
            blocks.append([u])
    return blocks


def _interleave_by_genre(units: Sequence[ProgramUnit]) -> list[ProgramUnit]:
    """Mix order: whole series blocks, rotating through genres so one topic
    does not own a week; within a genre, first-air order."""
    by_genre: dict[str, list[list[ProgramUnit]]] = defaultdict(list)
    for b in _series_blocks(units):
        by_genre[b[0].genre].append(b)
    queues = sorted(by_genre.values(), key=lambda q: (q[0][0].first_air, q[0][0].genre))
    out: list[ProgramUnit] = []
    i = 0
    while any(queues):
        q = queues[i % len(queues)]
        if q:
            out.extend(q.pop(0))
        i += 1
    return out


# --------------------------------------------------------------------------- filler


def _filler_items(
    seasons: Sequence[SeasonCandidate], loose: Sequence[LooseVideo], cfg: PlannerConfig
) -> tuple[list[FillerItem], list[tuple[str, str]]]:
    items: list[FillerItem] = []
    notes: list[tuple[str, str]] = []
    max_s = cfg.filler_max_minutes * 60
    for s in seasons:
        if s.kind == "clips":
            source = "clips"
        elif s.kind == "episodic" and s.median_s < cfg.short_series_max_minutes * 60:
            source = "short_series"
        else:
            continue
        for e in s.episodes:
            if cfg.filler_min_s <= e.duration_s <= max_s:
                items.append(
                    FillerItem(
                        path=e.path, duration_s=e.duration_s, title=e.title, source=source,
                        genre=s.genre, tags=s.tags, folder=s.folder, presenter=s.presenter,
                        plex_rating_key=e.plex_rating_key, plex_library=e.plex_library,
                        tier=s.tier,
                    )
                )  # fmt: skip
        notes.append((f"{s.name} S{s.season}", f"filler ({source}, {len(s.episodes)} items)"))
    for v in loose:
        if not cfg.filler_min_s <= v.duration_s <= max_s:
            continue
        if v.vertical and not cfg.include_vertical_shorts:
            continue
        items.append(
            FillerItem(
                path=v.path, duration_s=v.duration_s, title=v.title,
                source="short" if v.is_short else "loose", genre=v.genre, tags=v.tags,
                folder=v.folder, presenter=v.presenter, plex_rating_key=v.plex_rating_key,
                plex_library=v.plex_library, vertical=v.vertical,
            )
        )  # fmt: skip
    excluded = set(cfg.filler_exclude_topics)
    keep = [
        i
        for i in items
        if i.genre not in excluded
        and _score(i.tags, "topic", excluded) < cfg.filler_topic_min_score
    ]
    return keep, notes


def _pool(key: str, name: str, items: Iterable[FillerItem], fallback: str | None,
          reason: str) -> FillerPool:  # fmt: skip
    seen: set[str] = set()
    uniq: list[FillerItem] = []
    # horizontal before vertical, then longest first (best-fit padding scans in order)
    for i in sorted(items, key=lambda i: (i.vertical, -i.duration_s, i.path)):
        if i.path not in seen:
            seen.add(i.path)
            uniq.append(i)
    return FillerPool(key, name, tuple(uniq), fallback, (reason,))


def _loose_units(loose: Sequence[LooseVideo], cfg: PlannerConfig) -> list[SeasonCandidate]:
    """Long unassigned uploads, grouped per (folder, genre) into a synthetic
    season played in air-date order (``kind='loose'``, tier unknown)."""
    if not cfg.loose_programs:
        return []
    groups: dict[tuple[str, str], list[LooseVideo]] = defaultdict(list)
    for v in loose:
        if v.is_short or v.duration_s < cfg.loose_program_min_minutes * 60:
            continue
        if v.genre in cfg.loose_skip_genres:
            continue
        groups[(v.folder, v.genre)].append(v)
    out: list[SeasonCandidate] = []
    for (folder, genre), vids in sorted(groups.items()):
        if len(vids) < cfg.loose_min_group_videos:
            continue
        vids.sort(key=lambda v: (v.air_date or "9999", v.title, v.path))
        tags: dict[str, float] = defaultdict(float)
        for v in vids:
            for t, sc in v.tags.items():
                tags[t] += sc / len(vids)
        presenter = vids[0].presenter
        out.append(
            SeasonCandidate(
                series_id=f"loose:{folder}:{genre}",
                name=f"{presenter or folder} · {genre}",
                series_key=f"loose:{genre}",
                kind="loose",
                folder=folder,
                presenter=presenter,
                first_air=vids[0].air_date,
                genre=genre,
                tags=dict(tags),
                episodes=tuple(
                    EpisodeRef(
                        path=v.path, duration_s=v.duration_s, title=v.title, season=1,
                        episode=i, plex_rating_key=v.plex_rating_key,
                        plex_library=v.plex_library, air_date=v.air_date,
                    )
                    for i, v in enumerate(vids, 1)
                ),
            )
        )  # fmt: skip
    return out


# --------------------------------------------------------------------------- lanes


def _tier_lanes(
    prefix: str,
    label: str,
    units: Sequence[ProgramUnit],
    tags_of: Mapping[str, Mapping[str, float]],
    cfg: PlannerConfig,
    *,
    mix: bool = False,
    ramadan: bool = False,
) -> list[Lane]:
    by_tier: dict[str, list[ProgramUnit]] = defaultdict(list)
    for u in units:
        by_tier[u.tier_group].append(u)
    lanes: list[Lane] = []
    for tg, us in by_tier.items():
        ordered = _interleave_by_genre(us) if mix else _order_units(us)
        rank = _rank(ordered, tags_of, cfg)
        n_series = len({(u.folder, u.series_key) for u in ordered})
        lanes.append(
            Lane(
                key=f"{prefix}.{tg}",
                name=f"{label} · {tg}",
                tier_group=tg,
                units=tuple(ordered),
                rank=rank,
                ramadan=ramadan,
                reasons=(
                    f"{len(ordered)} season(s) of {n_series} series, tier group '{tg}'"
                    + (" (no production-style data: grouped as unknown)" if tg == UNKNOWN else "")
                    + (", interleaved by genre (whole series blocks)" if mix else
                       ", series in first-air order, seasons and episodes in order"),
                ),
            )
        )  # fmt: skip
    lanes.sort(key=lambda ln: (-ln.rank, -ln.duration_s, ln.key))
    return lanes


def _split_lane(lane: Lane, parts: int, daypart_keys: Sequence[str], cfg: PlannerConfig,
                *, mix: bool) -> list[tuple[int, Lane]]:  # fmt: skip
    """Split one lane into ``parts`` sub-lanes of whole series (same tier group,
    order kept) so the dayparts it serves show different series. Mix lanes
    split by the dayparts' preferred genres (part ``i`` ↔ ``daypart_keys[i]``);
    others by balanced hours. Returns ``(part index, lane)``; empty parts are
    left out."""
    blocks = _series_blocks(lane.units) if not mix else _blocks_in_order(lane.units)
    parts = min(parts, len(blocks))
    if parts <= 1:
        return [(0, lane)]
    bins: list[list[list[ProgramUnit]]] = [[] for _ in range(parts)]
    weight = [0.0] * parts
    rest: list[list[ProgramUnit]] = []
    if mix:
        for b in blocks:
            g = b[0].genre
            homes = [
                i
                for i, dk in enumerate(daypart_keys[:parts])
                if g in cfg.mix_daypart_topics.get(dk, ())
            ]
            if homes:
                i = min(homes, key=lambda j: weight[j])
                bins[i].append(b)
                weight[i] += sum(u.duration_s for u in b)
            else:
                rest.append(b)
    else:
        rest = sorted(blocks, key=lambda b: -sum(u.duration_s for u in b))
    for b in rest:
        i = min(range(parts), key=lambda j: weight[j])
        bins[i].append(b)
        weight[i] += sum(u.duration_s for u in b)
    out: list[tuple[int, Lane]] = []
    for i, bn in enumerate(bins):
        if not bn:
            continue
        units = [u for b in bn for u in b]
        units = units if mix else _order_units(units)
        out.append((i,
            replace(
                lane,
                key=f"{lane.key}.{i + 1}",
                name=f"{lane.name} {i + 1}",
                units=tuple(units),
                reasons=lane.reasons + (f"part {i + 1}/{parts} of the lane (whole series kept)",),
            ),
        ))  # fmt: skip
    return out


def _blocks_in_order(units: Sequence[ProgramUnit]) -> list[list[ProgramUnit]]:
    blocks: list[list[ProgramUnit]] = []
    for u in units:
        if blocks and (blocks[-1][0].folder, blocks[-1][0].series_key) == (u.folder, u.series_key):
            blocks[-1].append(u)
        else:
            blocks.append([u])
    return blocks


# --------------------------------------------------------------------------- grid


@dataclass(slots=True)
class _Seg:
    start: int
    end: int
    lane: str | None
    daypart: str
    pool: str | None
    kind: str
    reasons: tuple[str, ...]


def _round_down(m: int, step: int) -> int:
    return m - m % step if step > 0 else m


def _assign_dayparts(
    lanes: list[Lane],
    dayparts: Sequence[Daypart],
    quran: Lane | None,
    ramadan_lanes: Sequence[Lane],
    cfg: PlannerConfig,
    *,
    mix: bool,
) -> tuple[dict[str, list[Lane]], list[Lane], list[str]]:
    """Decide which lane(s) each daypart plays. Returns (daypart → lanes,
    every lane used (sub-lanes included), warnings)."""
    warnings: list[str] = []
    ranked = sorted(lanes, key=lambda ln: (-ln.rank, -ln.duration_s, ln.key))
    shared = len(ranked) <= 1  # one tier group: split it once across every daypart
    high: list[Lane] = []
    low: list[Lane] = []
    if not shared:
        high = [ln for ln in ranked if ln.rank >= 2]
        low = [ln for ln in ranked if ln.rank < 2]
        if not high:
            high, low = ranked[:1], ranked[1:]
        elif not low:
            high, low = ranked[:-1], ranked[-1:]
    role_lanes: dict[str, list[Lane]] = {"high": high, "low": low, "all": list(ranked)}
    roles: dict[str, str] = {}
    for d in dayparts:
        r = d.role
        if r == "quran" and quran is None:
            r = "low"
        if r == "ramadan" and not ramadan_lanes:
            r = "high"
        if shared and r in ("high", "low"):
            r = "all"
        roles[d.key] = r
    out: dict[str, list[Lane]] = {}
    used: list[Lane] = []
    if quran is not None:
        used.append(quran)
    if ramadan_lanes:
        used.extend(ramadan_lanes)
    for role in ("high", "low", "all"):
        dps = [d for d in dayparts if roles[d.key] == role]
        pool = role_lanes[role]
        if not dps or not pool:
            continue
        # one lane per daypart when possible; a lane serving several dayparts is
        # split into sub-lanes of whole series so each daypart shows something else
        per_lane: dict[str, list[Daypart]] = defaultdict(list)
        # best lane → longest daypart; on a tie, high roles prefer the later
        # daypart (prime over evening), low roles the earlier one
        sign = -1 if role == "high" else 1
        for i, d in enumerate(sorted(dps, key=lambda d: (-d.minutes, sign * d.start))):
            per_lane[pool[i % len(pool)].key].append(d)
        extra = pool[len(dps) :]
        by_key = {ln.key: ln for ln in pool}
        for lk, ds in per_lane.items():
            ln = by_key[lk]
            ds_sorted = sorted(ds, key=lambda d: d.start)
            parts = _split_lane(ln, len(ds), [d.key for d in ds_sorted], cfg, mix=mix)
            subs = [sub for _, sub in parts]
            biggest = max(subs, key=lambda s: s.duration_s)
            if mix:
                by_idx = dict(parts)
                for i, d in enumerate(ds_sorted):
                    out.setdefault(d.key, []).append(by_idx.get(i, biggest))
            else:  # longest sub-lane → longest daypart
                order = sorted(subs, key=lambda s: -s.duration_s)
                for i, d in enumerate(sorted(ds, key=lambda d: -d.minutes)):
                    out.setdefault(d.key, []).append(order[i] if i < len(order) else biggest)
            used.extend(subs)
        # more lanes than dayparts: share the longest dayparts
        for ln in extra:
            hosts = sorted(dps, key=lambda d: (len(out.get(d.key, [])), -d.minutes))
            target = hosts[0]
            if len(out.get(target.key, [])) >= cfg.max_lanes_per_daypart:
                warnings.append(f"lane {ln.key} has no daypart left (max_lanes_per_daypart)")
                continue
            out.setdefault(target.key, []).append(ln)
            used.append(ln)
    for d in dayparts:
        if roles[d.key] == "quran" and quran is not None:
            out[d.key] = [quran]
        elif roles[d.key] == "ramadan":
            out[d.key] = list(ramadan_lanes[: cfg.max_lanes_per_daypart])
    uniq: dict[str, Lane] = {}
    for ln in used:
        uniq.setdefault(ln.key, ln)
    return out, list(uniq.values()), warnings


def _daypart_segments(
    d: Daypart, lanes: Sequence[Lane], pool: str | None, kind: str, cfg: PlannerConfig
) -> list[_Seg]:
    """Cut a daypart into one segment per lane (rounded to the slot step,
    each at least as long as its lane's longest episode when possible)."""
    if not lanes:
        return [_Seg(d.start, d.start + d.minutes, None, d.key, pool, "flex",
                     ("no lane for this daypart: filler only",))]  # fmt: skip
    total = d.minutes
    n = len(lanes)
    cuts = [d.start]
    for i in range(1, n):
        cuts.append(d.start + _round_down(total * i // n, cfg.slot_round_minutes))
    cuts.append(d.start + total)
    segs: list[_Seg] = []
    for i, ln in enumerate(lanes):
        a, b = cuts[i], cuts[i + 1]
        if b - a < cfg.min_slot_minutes and segs:
            segs[-1].end = b
            continue
        why = [f"{d.label or d.key}: {d.role} daypart → lane {ln.key} (rank {ln.rank:.2f})"]
        if ln.max_episode_s > (b - a) * 60:
            why.append(
                f"longest episode {ln.max_episode_s / 60:.0f} min exceeds the "
                f"{b - a} min slot: it overruns into the next slot"
            )
        segs.append(_Seg(a, b, ln.key, d.key, pool, kind, tuple(why)))
    return segs


def _build_grid(
    channel_key: str,
    lanes: list[Lane],
    dayparts: Sequence[Daypart],
    quran: Lane | None,
    khutbah: Lane | None,
    ramadan_lanes: Sequence[Lane],
    pools: Mapping[str, str | None],
    cfg: PlannerConfig,
    *,
    mix: bool,
) -> tuple[list[Slot], list[Lane], list[str]]:
    assign, used, warnings = _assign_dayparts(lanes, dayparts, quran, ramadan_lanes, cfg, mix=mix)
    daily: list[_Seg] = []
    for d in dayparts:
        ls = assign.get(d.key, [])
        is_quran = bool(ls) and ls[0].special == "quran"
        pool = pools.get("dawn") if is_quran else pools.get("topic")
        daily.extend(_daypart_segments(d, ls, pool, "quran" if is_quran else "program", cfg))
    # unwrap past-midnight segments, then cover any hole with a flex segment
    flat: list[_Seg] = []
    for s in daily:
        if s.end > DAY_MINUTES:
            flat.append(_Seg(s.start, DAY_MINUTES, s.lane, s.daypart, s.pool, s.kind, s.reasons))
            flat.append(_Seg(0, s.end - DAY_MINUTES, s.lane, s.daypart, s.pool, s.kind, s.reasons))
        else:
            flat.append(s)
    flat.sort(key=lambda s: s.start)
    filled: list[_Seg] = []
    t = 0
    for s in flat:
        if s.start > t:
            filled.append(_Seg(t, s.start, None, "gap", pools.get("topic"), "flex",
                               ("no daypart covers this time: filler only",)))  # fmt: skip
        if s.end > t:
            filled.append(_Seg(max(s.start, t), s.end, s.lane, s.daypart, s.pool, s.kind,
                               s.reasons))  # fmt: skip
            t = s.end
    if t < DAY_MINUTES:
        filled.append(_Seg(t, DAY_MINUTES, None, "gap", pools.get("topic"), "flex",
                           ("no daypart covers this time: filler only",)))  # fmt: skip
    per_day: dict[str, list[_Seg]] = {d: [_copy(s) for s in filled] for d in WEEKDAYS}
    if khutbah is not None and cfg.friday_khutbah:
        day = cfg.khutbah_day
        per_day[day] = _overlay(
            per_day[day],
            _Seg(cfg.khutbah_start, cfg.khutbah_end, khutbah.key, "khutbah", pools.get("topic"),
                 "khutbah", (f"Friday khutbah {fmt_hhmm(cfg.khutbah_start)}–"
                             f"{fmt_hhmm(cfg.khutbah_end)} ({khutbah.episode_count} khutbahs, "
                             "one per week)",)),
        )  # fmt: skip
        used.append(khutbah)
    slots = _merge_days(per_day)
    uniq: dict[str, Lane] = {}
    for ln in used:
        uniq.setdefault(ln.key, ln)
    referenced = {s.lane for s in slots}
    return slots, [ln for ln in uniq.values() if ln.key in referenced], warnings


def _copy(s: _Seg) -> _Seg:
    return _Seg(s.start, s.end, s.lane, s.daypart, s.pool, s.kind, s.reasons)


def _overlay(segs: list[_Seg], o: _Seg) -> list[_Seg]:
    out: list[_Seg] = []
    for s in segs:
        if s.end <= o.start or s.start >= o.end:
            out.append(s)
            continue
        if s.start < o.start:
            out.append(_Seg(s.start, o.start, s.lane, s.daypart, s.pool, s.kind, s.reasons))
        if s.end > o.end:
            out.append(_Seg(o.end, s.end, s.lane, s.daypart, s.pool, s.kind, s.reasons))
    out.append(o)
    out.sort(key=lambda s: s.start)
    return out


def _merge_days(per_day: Mapping[str, list[_Seg]]) -> list[Slot]:
    """Same segment on several days → one slot with a day list (all days → ``days=()``).
    The result never overlaps on any day (so first-match and last-match slot
    resolution agree)."""
    groups: dict[tuple[Any, ...], list[str]] = {}
    proto: dict[tuple[Any, ...], _Seg] = {}
    for day in WEEKDAYS:
        merged: list[_Seg] = []
        for s in per_day[day]:  # join adjacent identical segments (e.g. a wrapped daypart)
            p = merged[-1] if merged else None
            if p and p.end == s.start and (p.lane, p.daypart, p.pool, p.kind) == (
                s.lane, s.daypart, s.pool, s.kind,
            ):  # fmt: skip
                p.end = s.end
            else:
                merged.append(_copy(s))
        for s in merged:
            k = (s.start, s.end, s.lane, s.daypart, s.pool, s.kind)
            groups.setdefault(k, []).append(day)
            proto.setdefault(k, s)
    slots = [
        Slot(
            start=k[0], end=k[1], lane=k[2], daypart=k[3], filler_pool=k[4], kind=k[5],
            days=() if len(days) == 7 else tuple(days), reasons=proto[k].reasons,
        )
        for k, days in groups.items()
    ]  # fmt: skip
    slots.sort(key=lambda s: (s.start, s.days != (), s.days))
    return slots


# --------------------------------------------------------------------------- planner


def suggest_channels(
    seasons: Sequence[SeasonCandidate],
    loose: Sequence[LooseVideo] = (),
    config: PlannerConfig | None = None,
) -> PlanResult:
    """Suggest live channels for a library. See the module docstring."""
    cfg = config or PlannerConfig()
    all_seasons = list(seasons) + _loose_units(loose, cfg)
    fillers, filler_notes = _filler_items(all_seasons, loose, cfg)
    filler_ids = {s.series_id for s in all_seasons if s.kind == "clips"} | {
        s.series_id
        for s in all_seasons
        if s.kind == "episodic" and s.median_s < cfg.short_series_max_minutes * 60
    }
    tags_of = {s.series_id: s.tags for s in all_seasons}
    by_id = {s.series_id: s for s in all_seasons}
    programs = [s for s in all_seasons if s.series_id not in filler_ids and s.episodes]
    units = {s.series_id: _unit(s, cfg) for s in programs}
    unplaced: list[tuple[str, str]] = list(filler_notes)

    quran_ids = {s.series_id for s in programs if _is_recitation(s, cfg)}
    khutbah_ids = {s.series_id for s in programs if _is_khutbah(s, cfg)} - quran_ids

    lanes: dict[str, Lane] = {}
    pools: dict[str, FillerPool] = {}
    channels: list[ChannelSuggestion] = []

    fmin = cfg.filler_topic_min_score
    general = _pool(
        "library.general",
        "General filler",
        (f for f in fillers if f.topic_score(cfg.generic_filler_topics) >= fmin),
        None,
        f"filler tagged {', '.join(cfg.generic_filler_topics)} (≥ {fmin})",
    )
    pools[general.key] = general
    dawn = _pool(
        "library.dawn", "Dawn filler (Quran, dua)",
        (f for f in fillers if f.topic_score(cfg.dawn_filler_topics) >= cfg.filler_topic_min_score),
        general.key, f"filler tagged {', '.join(cfg.dawn_filler_topics)}",
    )  # fmt: skip
    pools[dawn.key] = dawn

    def lib_lane(key: str, name: str, ids: set[str], special: str) -> Lane | None:
        us = _order_units(units[i] for i in ids)
        if not us:
            return None
        return Lane(
            key=key, name=name, tier_group="mixed", units=tuple(us), rank=0.0, special=special,
            reasons=(f"{len(us)} {special} season(s) from the whole library, in first-air order",),
        )  # fmt: skip

    quran_lane = lib_lane("library.quran", "Quran recitation", quran_ids, "quran")
    khutbah_lane = lib_lane("library.khutbah", "Friday khutbah", khutbah_ids, "khutbah")
    regular_ids = set(units) - quran_ids - (khutbah_ids if cfg.khutbah_only_friday else set())

    def regular(ids: Iterable[str]) -> list[ProgramUnit]:
        us = [units[i] for i in ids if i in regular_ids]
        if cfg.ramadan_exclusive:
            us = [u for u in us if not u.ramadan]
        return us

    def channel_pools(
        key: str, name: str, items: Iterable[FillerItem], why: str
    ) -> dict[str, str | None]:
        p = _pool(f"{key}.topic", f"{name} filler", items, general.key, why)
        pools[p.key] = p
        return {"topic": p.key, "dawn": dawn.key if dawn.items else general.key}

    def make_channel(
        key: str, number: int, name: str, name_ar: str, kind: str, category: str,
        member_ids: set[str], why: list[str], pool_keys: dict[str, str | None], *, mix: bool,
        own_quran: bool = False,
    ) -> None:  # fmt: skip
        reg = regular(member_ids)
        if own_quran:
            reg = [units[i] for i in member_ids]
        tier_lanes = _tier_lanes(key, name, reg, tags_of, cfg, mix=mix)
        q = None if own_quran else (quran_lane if cfg.dawn_quran else None)
        k = khutbah_lane if (khutbah_lane and (mix or member_ids & khutbah_ids)) else None
        ram_units = [units[i] for i in member_ids if i in units and units[i].ramadan]
        ram_lanes = (
            _tier_lanes(f"{key}.ramadan", f"{name} Ramadan", ram_units, tags_of, cfg,
                        mix=mix, ramadan=True)
            if cfg.ramadan_grid and ram_units else []
        )  # fmt: skip
        slots, used, warn = _build_grid(
            key, tier_lanes, cfg.dayparts, q, k, (), pool_keys, cfg, mix=mix
        )
        r_slots: list[Slot] = []
        r_used: list[Lane] = []
        if ram_lanes:
            r_slots, r_used, w2 = _build_grid(
                key, tier_lanes, cfg.ramadan_dayparts, q, k, ram_lanes, pool_keys, cfg, mix=mix
            )
            warn += w2
            why.append(
                f"{len(ram_units)} Ramadan-tagged season(s) (occasion:ramadan ≥ "
                f"{cfg.ramadan_min_score}): Ramadan grid puts them after iftar"
            )
        if q is not None:
            why.append(f"dawn plays the library's Quran recitation lane ({q.hours:.1f} h)")
        elif cfg.dawn_quran and not own_quran:
            why.append("no Quran recitation in the library: dawn falls back to a talk lane")
        if k is not None:
            why.append(f"Friday khutbah slot from {k.episode_count} khutbah(s)")
        for ln in used + r_used:
            lanes.setdefault(ln.key, ln)
        hours = sum(u.duration_s for u in reg) / 3600
        channels.append(
            ChannelSuggestion(
                key=key, number=number, name=name, name_ar=name_ar, kind=kind,
                category=category, lanes=tuple(dict.fromkeys(ln.key for ln in used + r_used)),
                slots=tuple(slots), ramadan_slots=tuple(r_slots),
                filler_pools=tuple(dict.fromkeys(
                    p for p in [*pool_keys.values(), general.key] if p is not None)),
                hours=hours, reasons=tuple(why), warnings=tuple(warn),
            )
        )  # fmt: skip

    placed: set[str] = set()
    # ---- themed channels
    for t in cfg.themes:
        if t.key == "quran":
            ids = set(quran_ids) | {i for i in units if _in_theme(by_id[i], t, cfg)}
        else:
            ids = {i for i in regular_ids if _in_theme(by_id[i], t, cfg)}
            ids |= {i for i in khutbah_ids if _in_theme(by_id[i], t, cfg)}
        hours = sum(units[i].duration_s for i in ids) / 3600
        need = t.min_hours if t.min_hours is not None else cfg.theme_min_hours
        if not ids or hours < need:
            if ids:
                unplaced.append((f"theme {t.key}", f"only {hours:.1f} h (< {need:.0f} h)"))
            continue
        topics = t.topics or ("__none__",)
        pk = channel_pools(
            t.key, t.name,
            (f for f in fillers
             if f.topic_score(topics) >= cfg.filler_topic_min_score
             or _score(f.tags, "audience", t.audiences) >= cfg.filler_topic_min_score),
            f"filler tagged {', '.join(t.topics + t.audiences)} (≥ {cfg.filler_topic_min_score})",
        )  # fmt: skip
        n_series = len({(units[i].folder, units[i].series_key) for i in ids})
        why = [
            f"{len(ids)} season(s) of {n_series} series, {hours:.1f} h match "
            f"{'/'.join(t.topics + t.audiences + t.formats)} "
            f"(genre or score ≥ {cfg.topic_min_score})"
        ]
        make_channel(t.key, t.number, t.name, t.name_ar, "theme", t.category, ids, why, pk,
                     mix=False, own_quran=t.key == "quran")  # fmt: skip
        placed |= ids

    # ---- mix channel
    excluded = set(cfg.mix_exclude_topics)
    mix_ids = {
        i
        for i in units
        if units[i].genre not in excluded
        and _score(tags_of[i], "topic", excluded) < cfg.topic_min_score
    }
    mix_hours = sum(units[i].duration_s for i in mix_ids if i in regular_ids) / 3600
    if cfg.mix_channel and mix_hours >= cfg.mix_min_hours:
        pk = channel_pools("mix", cfg.mix_name, fillers, "all on-topic filler of the library")
        genres = {units[i].genre for i in mix_ids}
        make_channel(
            "mix", cfg.mix_number, cfg.mix_name, cfg.mix_name_ar, "mix", "general", mix_ids,
            [f"{len(mix_ids)} season(s), {mix_hours:.1f} h across {len(genres)} genres; "
             "dayparts prefer genres per mix_daypart_topics; "
             f"excluded: {', '.join(excluded) or '-'}"],
            pk, mix=True,
        )  # fmt: skip
        placed |= mix_ids

    # ---- presenter channels
    if cfg.presenter_channels:
        by_presenter: dict[str, set[str]] = defaultdict(set)
        for i, u in units.items():
            if u.presenter:
                by_presenter[u.presenter].add(i)
        n = 0
        for presenter in sorted(by_presenter):
            ids = by_presenter[presenter]
            hours = sum(units[i].duration_s for i in ids if i in regular_ids) / 3600
            genres = {units[i].genre for i in ids}
            if hours < cfg.presenter_min_hours or len(genres) < cfg.presenter_min_genres:
                unplaced.append(
                    (f"presenter {presenter}",
                     f"{hours:.1f} h, {len(genres)} genres (needs {cfg.presenter_min_hours:.0f} h,"
                     f" {cfg.presenter_min_genres} genres)")
                )  # fmt: skip
                continue
            key = f"presenter.{_slug(presenter)}"
            pk = channel_pools(key, presenter, (f for f in fillers if f.presenter == presenter),
                               f"filler from {presenter}'s own uploads")  # fmt: skip
            make_channel(
                key, cfg.presenter_first_number + n, presenter, presenter, "presenter", "islamic",
                ids, [f"{len(ids)} season(s), {hours:.1f} h by {presenter} across "
                      f"{len(genres)} genres"], pk, mix=True,
            )  # fmt: skip
            placed |= ids
            n += 1

    for i in sorted(set(units) - placed):
        u = units[i]
        unplaced.append((u.label, f"genre {u.genre}: no channel took it"))
    used_pools = {p for c in channels for p in c.filler_pools}
    used_pools |= {fb for p in list(used_pools) if (fb := pools[p].fallback) is not None}
    channels.sort(key=lambda c: c.number)
    return PlanResult(
        channels=tuple(channels),
        lanes=lanes,
        filler_pools={k: v for k, v in pools.items() if k in used_pools},
        unplaced=tuple(unplaced),
        config=cfg,
    )


# --------------------------------------------------------------------------- simulation


def _pick_filler(pool: Sequence[FillerItem], remaining: float, recent: list[str],
                 window: int) -> FillerItem | None:  # fmt: skip
    """Best fit: the longest item that fits, skipping the last ``window`` picks
    when another one fits (pool is sorted longest first)."""
    fallback: FillerItem | None = None
    for it in pool:
        if it.duration_s <= remaining:
            if it.path not in recent[-window:]:
                return it
            if fallback is None:
                fallback = it
    return fallback


def simulate(
    channel: ChannelSuggestion,
    plan: PlanResult,
    *,
    weekday: str = "mon",
    days: int = 1,
    ramadan: bool = False,
) -> list[Airing]:
    """Lay ``days`` days out from ``weekday`` 00:00: in each slot window play
    the lane's next episode while it fits (the first one may overrun, like
    dizqueTV's "slide"), then pad to the boundary with best-fit filler from
    the slot's pool (falling back along ``FillerPool.fallback``); what no
    filler fits is ``flex`` (bumper / slate time). Lane cursors persist across
    slots and days, so a lane continues where it stopped."""
    slots = channel.ramadan_slots if ramadan and channel.ramadan_slots else channel.slots
    cfg = plan.config
    cursors: dict[str, int] = defaultdict(int)
    recent: list[str] = []
    out: list[Airing] = []
    t = 0.0
    d0 = WEEKDAYS.index(weekday)

    def pool_items(key: str | None) -> list[FillerItem]:
        items: list[FillerItem] = []
        seen: set[str] = set()
        while key is not None and key not in seen:
            seen.add(key)
            p = plan.filler_pools.get(key)
            if p is None:
                break
            items.extend(p.items)
            key = p.fallback
        uniq = {i.path: i for i in items}
        return sorted(uniq.values(), key=lambda i: (i.vertical, -i.duration_s, i.path))

    for day_i in range(days):
        day = WEEKDAYS[(d0 + day_i) % 7]
        base = day_i * DAY_MINUTES * 60.0
        for si, s in enumerate(sorted((s for s in slots if s.on(day)), key=lambda s: s.start)):
            ws, we = base + s.start * 60.0, base + s.end * 60.0
            if t >= we:
                continue
            t = max(t, ws)
            lane = plan.lanes.get(s.lane) if s.lane else None
            if lane is not None:
                eps = lane.episodes()
                played = 0
                while eps:
                    u, e = eps[cursors[lane.key] % len(eps)]
                    # doesn't fit what is left: pad and wait for the next window, unless
                    # the episode is longer than the whole slot (then it overruns)
                    if t + e.duration_s > we and (played or e.duration_s <= we - ws):
                        break
                    title = f"{u.label} · {e.title}"[:160]
                    out.append(Airing(t, t + e.duration_s, "program", title, lane.key, e.path, si))
                    t += e.duration_s
                    cursors[lane.key] += 1
                    played += 1
                    if t >= we:
                        break
            items = pool_items(s.filler_pool)
            while we - t > 0.5:
                f = _pick_filler(items, we - t, recent, cfg.filler_recent_window)
                if f is None or we - t < cfg.filler_min_s:
                    out.append(Airing(t, we, "flex", "flex / bumper", s.lane, None, si))
                    t = we
                    break
                out.append(Airing(t, t + f.duration_s, "filler", f.title[:160], s.lane, f.path, si))
                recent.append(f.path)
                t += f.duration_s
    return out


# --------------------------------------------------------------------------- mappings


def _lane_source(lane: Lane) -> dict[str, Any]:
    return {
        "type": "lane",
        "lane": lane.key,
        "order": lane.order,
        "tier_group": lane.tier_group,
        "series_ids": list(dict.fromkeys(u.series_id for u in lane.units)),
    }


def _maktaba_slots(slots: Sequence[Slot], plan: PlanResult) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for s in slots:
        lane = plan.lanes.get(s.lane) if s.lane else None
        d: dict[str, Any] = {
            "start": fmt_hhmm(s.start),
            "end": fmt_hhmm(s.end) if s.end < DAY_MINUTES else "24:00",
            "source": _lane_source(lane) if lane else {"type": "filler"},
            "daypart": s.daypart,
            "kind": s.kind,
            "filler_pool": s.filler_pool,
        }
        if s.days:
            d["days"] = list(s.days)
        out.append(d)
    return out


def maktaba_channel_definition(channel: ChannelSuggestion, plan: PlanResult) -> dict[str, Any]:
    """The ``POST /api/channels`` body (Story 27.1 ``CreateRequest``) for a
    suggestion: ``mode=schedule``, ``mode_config.slots`` in the shape
    ``ValidateModeConfig`` accepts (``start``/``end`` strings, optional
    ``days``) and ``SchedulePlanner`` reads. Slots never overlap on a day, so
    first-match (today's planner) and later-wins (the story's AC5) agree.
    Extra keys (``source``, ``filler_pool``, ``ramadan``, ``lanes``) are what
    the proposed spec changes would read; today they are ignored."""
    cfg = plan.config
    mode_config: dict[str, Any] = {
        "slots": _maktaba_slots(channel.slots, plan),
        "fill": {
            "type": "filler_pool",
            "pool": channel.filler_pools[0] if channel.filler_pools else None,
        },
        "planner_version": plan.version,
    }
    if cfg.timezone:
        mode_config["timezone"] = cfg.timezone
    if channel.ramadan_slots:
        mode_config["ramadan"] = {
            "activate": {"hijri_month": 9},
            "slots": _maktaba_slots(channel.ramadan_slots, plan),
        }
    return {
        "name": channel.name_ar or channel.name,
        "number": channel.number,
        "category": channel.category,
        "mode": "schedule",
        "mode_config": mode_config,
        "transition": "cut",
        "enabled": True,
        "sort_order": channel.number,
    }


# dizqueTV's weekly period starts at the epoch's weekday, a Thursday
_DZ_WEEK_INDEX = {"thu": 0, "fri": 1, "sat": 2, "sun": 3, "mon": 4, "tue": 5, "wed": 6}
_DAY_MS = DAY_MINUTES * 60_000


def dizquetv_time_slots(
    channel: ChannelSuggestion, plan: PlanResult, *, ramadan: bool = False
) -> dict[str, Any]:
    """dizqueTV ``/api/channel-tools/time-slots`` schedule for a suggestion.

    A dizqueTV slot is a start time; it plays its show until the next slot.
    Lanes map to custom shows (``showId`` here is ``lane:<key>``, resolved to
    ``custom.<uuid>`` by the exporter); slots without a lane are ``flex.``.
    Day-specific slots (the Friday khutbah) need the weekly period, whose
    day 0 is Thursday. Adjacent slots of one lane are merged. Times are local
    wall-clock; the exporter sets ``timeZoneOffset`` (minutes, as the web UI
    does with ``Date.getTimezoneOffset``)."""
    cfg = plan.config
    src = channel.ramadan_slots if ramadan and channel.ramadan_slots else channel.slots
    weekly = any(s.days for s in src)
    period = _DAY_MS * (7 if weekly else 1)
    raw: list[tuple[int, str, str]] = []  # (time ms, showId, order)
    days = list(_DZ_WEEK_INDEX) if weekly else ["mon"]
    for day in days:
        off = _DZ_WEEK_INDEX[day] * _DAY_MS if weekly else 0
        for s in src:
            if weekly and not s.on(day):
                continue
            if not weekly and s.days:
                continue
            show = f"lane:{s.lane}" if s.lane else "flex."
            raw.append((off + s.start * 60_000, show, "next" if s.lane else "shuffle"))
    raw.sort()
    merged: list[tuple[int, str, str]] = []
    for item in raw:
        if merged and merged[-1][1] == item[1]:
            continue
        merged.append(item)
    if len(merged) > 1 and merged[0][1] == merged[-1][1]:
        merged = merged[1:]  # the last slot wraps past the period end into the first one
    return {
        "period": period,
        "lateness": cfg.dz_lateness_ms,
        "maxDays": cfg.dz_max_days,
        "flexPreference": "distribute",
        "pad": cfg.dz_pad_ms,
        "slots": [{"time": t, "showId": sh, "order": o} for t, sh, o in merged],
    }
