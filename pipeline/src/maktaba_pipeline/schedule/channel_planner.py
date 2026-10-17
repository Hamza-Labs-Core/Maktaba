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
per topic family with enough hours), a Mix channel, per-presenter channels and
the Quran channel. A channel's week is laid out the way a TV channel is:

- **Shows, not seasons.** Seasons fold into :class:`Show` s: a *limited* series
  (seasons with an end), an *ongoing* programme (a weekly talk show, a lesson
  series that ran for years — its year or guest seasons are one run) or a
  *collection* of stand-alone videos.
- **Cadence.** A show airs ``weekly`` or as a ``strip`` (every weekday at the
  same time): from its own exact air dates when they show a rhythm, else from
  its format (lessons and Q&A → strip, talk shows and programmes → weekly) and
  type. Its episode length sets the slot (30 / 60 / 90 / 120 min).
- **Slots own a queue.** Each slot (Wednesday 20:00, or weekdays 17:00) is
  owned by a show and keeps a queue (a :class:`Lane`): an ongoing show plays all
  its years there back to back; when a series ends, the next show in the queue
  takes the slot. Shows never interleave and never air more often than their
  cadence (unless a channel has fewer shows than slots: ``extra_airings``).
- **Variety.** Strong shows take prime time; dayparts prefer their topics
  (``mix_daypart_topics``); adjacent slots differ in genre; every night of the
  week has its own prime-time show; weekends take weekly shows.
- **Fillers are first-class.** Every slot pads to its boundary — and between
  the episodes of a long block — from its pool chain: its own shows' promos,
  then clips on its topics (the group step's clip collections and shorts), the
  channel's, the library's; never a recent repeat. What nothing fits is dead
  air (``flex``), which the plan reports per slot.
- **Quran at dawn**, a **Friday khutbah** slot, and a separate **Ramadan grid**
  whose after-iftar slots air the Ramadan shows every day.
- **The Quran channel** is not a show grid: recitation non-stop, a reciter per
  day (or rotating by hours), styles at set times (mujawwad after Fajr, warsh at
  night); a reciter resumes where he stopped and starts the next khatma after
  the last surah; its filler is only short recitations and dua.
- Production style is at most a tie-breaker in a show's strength.

The result maps onto Epic 27 directly: each channel becomes a ``schedule``
mode channel (:func:`maktaba_channel_definition`, the slot-0081 shape the Go
validator accepts and :class:`~maktaba_pipeline.channels.planner.schedule.
SchedulePlanner` reads), and onto dizqueTV's time-slot tool
(:func:`dizquetv_time_slots`; each queue becomes one dizqueTV custom show
played in order, so a slot runs on through the years). :func:`simulate` lays
days out the way both schedulers do (next episodes while they fit, filler
between and to the boundary) and :func:`slot_timeline` shows what a slot airs
over the coming months.

Every threshold lives in :class:`PlannerConfig`; every suggestion carries
``reasons``.
"""

from __future__ import annotations

import contextlib
import math
import re
from collections import Counter, defaultdict
from collections.abc import Callable, Iterable, Mapping, Sequence
from dataclasses import dataclass, field, replace
from datetime import date
from functools import partial
from pathlib import Path
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
    "Show",
    "recitation_style",
    "simulate",
    "slot_timeline",
    "suggest_channels",
]

PLANNER_VERSION: int = 3  # 2: TV grid (show slots with queues), Quran channel; 3: the
#: lineup (at most max_channels, each with a month of content), presenter slots, blocks
DAY_MINUTES = 24 * 60
WEEKDAYS: tuple[str, ...] = ("mon", "tue", "wed", "thu", "fri", "sat", "sun")
TIER_NAMES: dict[int, str] = {0: "static", 1: "talk", 2: "produced", 3: "cinematic"}
UNKNOWN = "unknown"
#: Arabic words for lane names ("منوعات · حوارات · المساء"); unknown tier adds no word
TIER_NAMES_AR: dict[str, str] = {
    "static": "دروس", "talk": "حوارات", "produced": "برامج", "cinematic": "وثائقيات",
    "mixed": "متنوعة", UNKNOWN: "",
}  # fmt: skip
DAYPART_NAMES_AR: dict[str, str] = {
    "dawn": "الفجر", "morning": "الصباح", "afternoon": "الظهيرة", "evening": "المساء",
    "prime": "السهرة", "late": "آخر الليل", "suhoor": "السحور", "pre_iftar": "قبل الإفطار",
    "iftar_prime": "بعد الإفطار", "night": "بعد التراويح",
}  # fmt: skip


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
    #: the air date is the real day (YouTube's exact upload date), not "N years ago"
    date_exact: bool = False
    #: its own tags, from its title (topic:palestine in a talk show's season)
    tags: Mapping[str, float] = field(default_factory=dict)


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
    #: a talk show found by its title brand ("شاهد على العصر | …"): one ongoing programme
    brand: bool = False
    #: the season's own name (a guest, a year, a topic), when detection gave one
    season_name: str | None = None

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
    #: the clip / stand-alone group it is in ("<lib> - مقاطع - الفقه"), "" when none
    group: str = ""
    #: a promo: "<folder>|<series key>" of the show it advertises, "" otherwise
    promo_of: str = ""


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
    ``audience:`` score in ``audiences``, ``format:`` in ``formats``,
    ``occasion:`` in ``occasions``) reaches :attr:`PlannerConfig.topic_min_score`,
    and none of the ``exclude`` tags (full names: ``audience:adults``) does.
    The order of :attr:`PlannerConfig.themes` is their priority in the lineup."""

    key: str
    name: str
    name_ar: str
    number: int
    topics: tuple[str, ...] = ()
    audiences: tuple[str, ...] = ()
    formats: tuple[str, ...] = ()
    category: str = "general"
    #: set: this theme's own minimum, instead of the month rule (a Quran channel repeats
    #: by nature: reciters rotate, a khatma starts again); None: the month rule
    min_hours: float | None = None
    occasions: tuple[str, ...] = ()
    exclude: tuple[str, ...] = ()


#: In priority order: when more themes have enough content than the lineup has room
#: for (:attr:`PlannerConfig.max_channels`), the earlier ones get a channel.
DEFAULT_THEMES: tuple[ThemeSpec, ...] = (
    ThemeSpec("quran", "Quran", "القرآن الكريم", 101, ("quran_recitation",),
              formats=("recitation",), category="quran", min_hours=100.0),
    ThemeSpec("kids", "Kids & Family", "الأطفال والأسرة", 110, ("seerah", "prophets_stories"),
              audiences=("kids", "kids_friendly", "beginners"), category="kids",
              exclude=("audience:adults",)),
    ThemeSpec("ramadan", "Ramadan", "رمضان", 115, ("fasting", "qiyam"),
              occasions=("ramadan",), category="islamic"),
    ThemeSpec("seerah", "Seerah", "السيرة النبوية", 103, ("seerah",), category="islamic"),
    ThemeSpec("tafsir", "Tafsir", "التفسير", 105, ("tafsir",), category="islamic"),
    ThemeSpec("hadith", "Hadith", "الحديث", 102, ("hadith",), category="islamic"),
    ThemeSpec("fiqh", "Fiqh & Fatwa", "الفقه والفتاوى", 111, ("fiqh",),
              formats=("qa_fatwa",), category="islamic"),
    ThemeSpec("aqeedah", "Aqeedah & Shubuhat", "العقيدة والشبهات", 107,
              ("aqeedah", "shubuhat"), category="islamic"),
    ThemeSpec("history", "Islamic History", "التاريخ الإسلامي", 108,
              ("islamic_history", "sahaba"), category="documentaries"),
    ThemeSpec("palestine", "Palestine", "فلسطين", 125, ("palestine",), category="news"),
    ThemeSpec("news", "News & Current Affairs", "الأخبار والقضايا المعاصرة", 124,
              ("current_affairs",), category="news"),
    ThemeSpec("talk", "Talk Shows", "البرامج الحوارية", 126,
              formats=("program", "interview", "podcast"), category="general"),
    ThemeSpec("prophets", "Prophets' Stories", "قصص الأنبياء", 104, ("prophets_stories",),
              category="islamic"),
    ThemeSpec("raqaiq", "Raqaiq", "الرقائق", 106, ("raqaiq", "akhirah", "dua_dhikr"),
              category="islamic"),
    ThemeSpec("akhlaq", "Akhlaq", "الأخلاق", 113, ("akhlaq", "dawah"), category="islamic"),
    ThemeSpec("family", "Family", "الأسرة", 109, ("family_marriage", "women", "youth"),
              category="islamic"),
    ThemeSpec("salah", "Fiqh of Salah", "فقه الصلاة", 121, ("salah",), category="islamic"),
    ThemeSpec("muamalat", "Fiqh of Money & Trade", "فقه المعاملات", 123, ("muamalat",),
              category="islamic"),
    ThemeSpec("family_fiqh", "Fiqh of the Family", "فقه الأسرة", 122, ("family_fiqh",),
              category="islamic"),
    ThemeSpec("fasting", "Fasting", "الصيام", 116, ("fasting",), category="islamic"),
    ThemeSpec("qiyam", "Taraweeh & Qiyam", "التراويح والقيام", 117, ("qiyam",),
              category="quran"),
    ThemeSpec("quran_learning", "Quran Learning", "تعليم القرآن", 118, ("tajweed",),
              category="quran"),
    ThemeSpec("quran_competition", "Quran Competitions", "مسابقات القرآن", 119,
              ("quran_competition",), category="quran"),
    ThemeSpec("zakat", "Zakat & Charity", "الزكاة والصدقة", 120, ("zakat",), category="islamic"),
    ThemeSpec("hajj", "Hajj & Umrah", "الحج والعمرة", 127, occasions=("hajj",),
              category="islamic"),
    ThemeSpec("stories", "Stories", "القصص", 112, ("stories",), category="islamic"),
    ThemeSpec("nasheed", "Nasheed", "الأناشيد", 114, ("nasheed", "poetry"), category="music"),
)  # fmt: skip


@dataclass(frozen=True, slots=True)
class Daypart:
    """A daily window of the grid. ``end <= start`` wraps past midnight.

    ``role``: ``quran`` (recitation lane when the library has one), ``high``
    (the strongest shows first: prime time), ``low`` (the daypart's own topics
    first), ``block`` (a fixed block like the kids' hour: the shows matching
    ``topics`` first, in their order (``audience:kids``, a genre), never one with an
    ``exclude`` tag; filled before the other dayparts) or ``ramadan`` (Ramadan-tagged
    shows first; Ramadan grid only).

    ``pattern`` is how its slots repeat through the week, like a TV schedule:
    ``weekly`` (each day its own show: Wednesday 20:00 is one show, Thursday
    20:00 another), ``strip`` (one show at the same time every weekday; the
    weekend days get weekly shows), ``strip7`` (the same show every day, e.g.
    the Ramadan series after iftar) or ``quran`` (recitation)."""

    key: str
    start: int  # minutes after local midnight
    end: int
    role: str
    label: str = ""
    pattern: str = "weekly"
    topics: tuple[str, ...] = ()
    exclude: tuple[str, ...] = ()

    @property
    def minutes(self) -> int:
        return (self.end - self.start) % DAY_MINUTES or DAY_MINUTES


def _hm(s: str) -> int:
    h, m = s.split(":")
    return int(h) * 60 + int(m)


#: Like a real channel, easy to remember: Quran at 5, the kids' hour at 4, and strips
#: (the same slot every weekday) from the morning to prime time.
DEFAULT_DAYPARTS: tuple[Daypart, ...] = (
    Daypart("dawn", _hm("05:00"), _hm("07:00"), "quran", "Dawn: Quran", "quran"),
    Daypart("morning", _hm("07:00"), _hm("12:00"), "low", "Morning", "strip"),
    Daypart("afternoon", _hm("12:00"), _hm("16:00"), "low", "Afternoon", "strip"),
    Daypart("kids", _hm("16:00"), _hm("17:00"), "block", "Kids' hour", "strip",
            ("audience:kids", "audience:kids_friendly", "seerah", "prophets_stories",
             "audience:beginners"), ("audience:adults",)),
    Daypart("evening", _hm("17:00"), _hm("20:00"), "high", "Evening", "strip"),
    Daypart("prime", _hm("20:00"), _hm("23:00"), "high", "Prime time", "strip"),
    Daypart("late", _hm("23:00"), _hm("05:00"), "low", "Late night", "weekly"),
)  # fmt: skip

DEFAULT_RAMADAN_DAYPARTS: tuple[Daypart, ...] = (
    Daypart("suhoor", _hm("01:00"), _hm("04:00"), "low", "Suhoor", "weekly"),
    Daypart("dawn", _hm("04:00"), _hm("06:00"), "quran", "Fajr: Quran", "quran"),
    Daypart("morning", _hm("06:00"), _hm("13:00"), "low", "Morning", "strip"),
    Daypart("afternoon", _hm("13:00"), _hm("17:00"), "low", "Afternoon", "strip"),
    Daypart("pre_iftar", _hm("17:00"), _hm("19:00"), "quran", "Before iftar: Quran", "quran"),
    Daypart(
        "iftar_prime",
        _hm("19:00"),
        _hm("22:00"),
        "ramadan",
        "After iftar: Ramadan programs",
        "strip7",
    ),
    Daypart("night", _hm("22:00"), _hm("01:00"), "high", "After taraweeh", "weekly"),
)


@dataclass(frozen=True, slots=True)
class PlannerConfig:
    """Every threshold the planner uses. Durations in minutes unless named ``_s``."""

    themes: tuple[ThemeSpec, ...] = DEFAULT_THEMES
    dayparts: tuple[Daypart, ...] = DEFAULT_DAYPARTS
    ramadan_dayparts: tuple[Daypart, ...] = DEFAULT_RAMADAN_DAYPARTS
    # the lineup
    #: at most this many channels in all: candidates in channel_kind_order, themes in
    #: their order, presenters largest first
    max_channels: int = 20
    channel_kind_order: tuple[str, ...] = ("quran", "theme", "mix", "presenter")
    #: a channel needs this many days of content without a repeat …
    channel_min_days: float = 30.0
    #: … at this many programme hours a day (the rest of the day is filler)
    channel_day_hours: float = 20.0
    #: tags given to every video of a library (by its exact name): audience:kids,
    #: audience:kids_friendly, audience:adults (kept off the kids' channel and hour) …
    library_tags: Mapping[str, tuple[str, ...]] = field(default_factory=dict)
    #: a slot belongs to a show (the shows left over join the shortest queues) or to a
    #: presenter (8 pm is one sheikh's: his next show follows in the same slot)
    slot_owner: str = "presenter"
    # theme membership
    topic_min_score: float = 0.6
    theme_min_hours: float = 10.0
    #: a theme also takes single episodes about it from every other show (a talk show's
    #: Palestine episodes), as one collection per presenter in air order
    theme_episodes: bool = True
    theme_episode_min_score: float = 0.6
    theme_episode_min_minutes: float = 10.0
    #: a presenter's collection needs at least this many such episodes
    theme_episode_min_count: int = 2
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
    #: presenter channels only for the largest libraries: at most this many
    presenter_max_channels: int = 3
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
    #: a season of these genres is recitation (the Quran lanes); a tajweed lesson is not
    recitation_genres: tuple[str, ...] = ("quran_recitation",)
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
    max_slot_minutes: int = 120
    #: an episode this much longer than its slot still gets that slot (it overruns a little)
    overrun_tolerance_minutes: int = 5
    #: the weekend: strip shows air on the other days, weekly shows take these
    weekend_days: tuple[str, ...] = ("fri", "sat")
    # cadence: how often a show airs (weekly, or a strip: every weekday)
    cadence_min_gaps: int = 5  # exact air dates needed to read a show's own rhythm
    strip_max_gap_days: float = 2.5  # median gap ≤ this → it aired daily → a strip
    #: format tags whose shows air weekly (talk shows, programmes, interviews)
    weekly_formats: tuple[str, ...] = ("program", "interview", "podcast", "documentary", "live")
    #: format tags whose shows air as daily strips (lesson series, Q&A, drama serials)
    strip_formats: tuple[str, ...] = ("lecture", "qa_fatwa", "drama")
    #: a limited series with at most this many episodes airs as a strip by default
    strip_max_limited_episodes: int = 60
    #: a channel with fewer shows than its grid has slots gives shows an extra airing
    #: (False: the rest of the week is filler)
    extra_airings: bool = True
    #: a channel whose shows at their own cadence fill less than this share of its week
    #: moves its biggest weekly shows to daily strips first (an archive channel re-runs
    #: its long-running shows daily), largest first, while they have enough episodes
    #: a show joins a channel (whole) when this share of its hours matches the channel
    show_member_min_share: float = 0.5
    fill_target: float = 0.8
    promote_min_episodes: int = 26
    #: an extra airing never brings a queue under this many weeks of content (it would loop)
    extra_min_weeks_left: float = 8.0
    # filler
    filler_between_episodes: bool = True  # pad between the episodes of a long block too
    slot_filler_topics: int = 3  # a slot's own filler matches its shows' first N topics
    dead_air_warn_minutes: float = 3.0
    # Quran channel: recitation non-stop, a reciter per day, styles at set times
    quran_rotation: str = "by_day"  # by_day | by_time (time rules only, rotating)
    #: (start, end, style): e.g. mujawwad after Fajr, warsh at night
    quran_time_rules: tuple[tuple[str, str, str], ...] = (
        ("04:00", "07:00", "mujawwad"), ("21:00", "22:00", "warsh"),
    )  # fmt: skip
    #: a library is a reciter's when this share of its hours is recitation; only those
    #: feed the Quran channel and Quran at dawn (a scholar's few recitation-tagged
    #: seasons stay his programmes)
    quran_library_min_share: float = 0.6
    #: no library reaches quran_library_min_share: its recitations still make the
    #: Quran lanes (False: no Quran lane then)
    quran_fallback_any_recitation: bool = True
    #: recitation styles: the words that name each in a reciter's library / season names
    #: (the first style that matches wins; quran_default_style otherwise)
    quran_styles: Mapping[str, tuple[str, ...]] = field(
        default_factory=lambda: {
            "warsh": ("ورش", "warsh"),
            "mujawwad": ("مجود", "مجوّد", "mojawwad", "mujawwad"),
            "murattal": ("مرتل", "murattal", "حفص"),
        }
    )
    quran_default_style: str = "murattal"
    #: more reciters than days split each day, into at most this many parts
    quran_max_reciters_per_day: int = 4
    #: quran_rotation by_time: the reciter changes every this many hours
    quran_rotation_hours: int = 4
    quran_filler_topics: tuple[str, ...] = ("quran_recitation", "tajweed", "dua_dhikr")
    #: talk formats never used as filler on the Quran channel
    quran_filler_exclude_formats: tuple[str, ...] = (
        "lecture", "qa_fatwa", "program", "interview", "podcast", "khutbah",
    )  # fmt: skip
    # unknown tier is ranked by format: these formats rank like "produced-ish"
    prime_unknown_formats: tuple[str, ...] = ("program", "documentary", "drama")
    # prime-time rank of a show (higher airs at prime time): a base of 1, plus …
    rank_formats: tuple[str, ...] = ("program", "documentary", "drama", "interview")
    rank_format_bonus: float = 1.0  # … when its format is one of rank_formats
    rank_prime_topic_bonus: float = 0.4  # … when its genre is a prime-time topic
    rank_size_bonus: float = 0.3  # … up to this much for its size
    rank_size_full_hours: float = 40.0  # … reached at this many hours
    rank_ongoing_bonus: float = 0.2  # … for an ongoing programme
    rank_collection_bonus: float = -0.8  # … (negative) for a collection of lectures
    rank_style_bonus: float = 0.1  # … for a produced or cinematic production style
    format_min_score: float = 0.5
    timezone: str | None = None  # IANA name written into mode_config; None = server local
    # dizqueTV schedule knobs
    dz_lateness_ms: int = 15 * 60_000
    dz_max_days: int = 365
    dz_pad_ms: int = 1
    dz_filler_cooldown_ms: int = 30 * 60_000
    #: a dizqueTV filler list holds at most this many clips (the first of the pool, which
    #: is in priority order): a pool can hold 75,000 clips, more than dizqueTV accepts in
    #: one request (50 MB) and far more than a filler list needs
    dz_filler_max_items: int = 2000


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
    brand: bool = False
    season_name: str | None = None
    library: str = ""

    @property
    def tier_group(self) -> str:
        return TIER_NAMES.get(self.tier, UNKNOWN) if self.tier is not None else UNKNOWN

    @property
    def duration_s(self) -> float:
        return _unit_stats(self)[1]

    @property
    def max_episode_s(self) -> float:
        return _unit_stats(self)[2]

    @property
    def label(self) -> str:
        if self.season_name and self.season_name != self.name:
            return f"{self.name} · {self.season_name}"
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
    #: the slot this lane is the queue of: weekly | strip | strip7 | quran | special
    pattern: str = ""
    cadence: str = ""  # of its shows: weekly | strip
    slot_minutes: int = 0
    #: its shows in queue order (one after the other, never interleaved)
    shows: tuple[str, ...] = ()

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
    source: str  # short | clips | short_series | loose | clip_group | promo
    genre: str
    tags: Mapping[str, float]
    folder: str = ""
    presenter: str = ""
    plex_rating_key: str | None = None
    plex_library: str | None = None
    vertical: bool = False
    tier: int | None = None
    group: str = ""
    promo_of: str = ""  # "<folder>|<series key>" of the show a promo advertises

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
    kind: str  # theme | mix | presenter | quran
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
    return s.genre in cfg.recitation_genres or (
        _format_score(s.tags, "recitation") >= cfg.recitation_min_score
        and _score(s.tags, "topic", ("quran_recitation", "tajweed")) >= cfg.recitation_min_score
    )


def _is_khutbah(s: SeasonCandidate, cfg: PlannerConfig) -> bool:
    return _format_score(s.tags, "khutbah") >= cfg.khutbah_min_score


def _excluded(tags: Mapping[str, float], exclude: Iterable[str], cfg: PlannerConfig) -> bool:
    return any(tags.get(t, 0.0) >= cfg.topic_min_score for t in exclude)


def _tag_rank(genre: str, tags: Mapping[str, float], names: Sequence[str],
              cfg: PlannerConfig) -> int:  # fmt: skip
    """The first of ``names`` it matches (a full tag such as ``audience:kids`` by its
    score, a topic by the genre or its ``topic:`` score); ``len(names)`` when none."""
    for i, n in enumerate(names):
        if ":" in n:
            if tags.get(n, 0.0) >= cfg.topic_min_score:
                return i
        elif genre == n or tags.get(f"topic:{n}", 0.0) >= cfg.topic_min_score:
            return i
    return len(names)


def _in_theme(s: SeasonCandidate, t: ThemeSpec, cfg: PlannerConfig) -> bool:
    if _excluded(s.tags, t.exclude, cfg):
        return False
    if s.genre in t.topics:
        return True
    return (
        _score(s.tags, "topic", t.topics) >= cfg.topic_min_score
        or _score(s.tags, "audience", t.audiences) >= cfg.topic_min_score
        or _score(s.tags, "format", t.formats) >= cfg.topic_min_score
        or _score(s.tags, "occasion", t.occasions) >= cfg.topic_min_score
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
        brand=s.brand,
        season_name=s.season_name,
        library=s.library,
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
        source = (
            "promo" if v.promo_of else "clip_group" if v.group
            else "short" if v.is_short else "loose"
        )  # fmt: skip
        items.append(
            FillerItem(
                path=v.path, duration_s=v.duration_s, title=v.title,
                source=source, genre=v.genre, tags=v.tags,
                folder=v.folder, presenter=v.presenter, plex_rating_key=v.plex_rating_key,
                plex_library=v.plex_library, vertical=v.vertical, group=v.group,
                promo_of=v.promo_of,
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
        if v.is_short or v.promo_of or v.duration_s < cfg.loose_program_min_minutes * 60:
            continue
        if v.genre in cfg.loose_skip_genres and not v.group:
            continue
        groups[(v.folder, v.group or v.genre)].append(v)
    out: list[SeasonCandidate] = []
    for (folder, group_or_genre), vids in sorted(groups.items()):
        if len(vids) < cfg.loose_min_group_videos:
            continue
        group = vids[0].group
        genre = Counter(v.genre for v in vids).most_common(1)[0][0] if group else group_or_genre
        vids.sort(key=lambda v: (v.air_date or "9999", v.title, v.path))
        tags: dict[str, float] = defaultdict(float)
        for v in vids:
            for t, sc in v.tags.items():
                tags[t] += sc / len(vids)
        presenter = vids[0].presenter
        out.append(
            SeasonCandidate(
                series_id=f"loose:{folder}:{group_or_genre}",
                name=group or f"{presenter or folder} · {genre}",
                series_key=f"loose:{group_or_genre}",
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


# --------------------------------------------------------------------------- shows

DAY_NAMES_AR: dict[str, str] = {
    "mon": "الاثنين", "tue": "الثلاثاء", "wed": "الأربعاء", "thu": "الخميس",
    "fri": "الجمعة", "sat": "السبت", "sun": "الأحد",
}  # fmt: skip
SHOW_TYPES_AR: dict[str, str] = {
    "limited": "مسلسل/برنامج محدود", "ongoing": "برنامج مستمر", "collection": "مختارات",
}  # fmt: skip
STRIP_FAMILY = ("strip", "strip7")


@dataclass(frozen=True, slots=True)
class Show:
    """One show as a channel schedules it: every season of one series, in order.

    ``type``: ``limited`` (a series with an end), ``ongoing`` (a programme that
    ran for years — a weekly talk show, a lesson series; its year or guest
    seasons are one run) or ``collection`` (stand-alone videos grouped by topic).
    ``cadence``: ``weekly`` or ``strip`` (every weekday at the same time).
    ``slot_minutes``: the slot its episodes need (30 / 60 / 90 / 120)."""

    key: str
    name: str
    presenter: str
    folder: str
    genre: str
    units: tuple[ProgramUnit, ...]
    type: str
    cadence: str
    slot_minutes: int
    rank: float
    ramadan: bool
    reasons: tuple[str, ...] = ()
    tags: Mapping[str, float] = field(default_factory=dict)

    @property
    def duration_s(self) -> float:
        return sum(u.duration_s for u in self.units)

    @property
    def episode_count(self) -> int:
        return sum(len(u.episodes) for u in self.units)

    @property
    def first_air(self) -> str:
        return min((u.first_air for u in self.units), default="9999")

    @property
    def max_episode_s(self) -> float:
        return max((u.max_episode_s for u in self.units), default=0.0)


def _median_gap_days(units: Sequence[ProgramUnit], cfg: PlannerConfig) -> float | None:
    """Median days between a show's exact air dates ("N years ago" dates don't count)."""
    days: set[int] = set()
    for u in units:
        for e in u.episodes:
            if e.date_exact and e.air_date:
                with contextlib.suppress(ValueError):
                    days.add(date.fromisoformat(e.air_date[:10]).toordinal())
    ds = sorted(days)
    gaps = [b - a for a, b in zip(ds, ds[1:], strict=False)]
    if len(gaps) < cfg.cadence_min_gaps:
        return None
    return float(median(gaps))


def _show_type(units: Sequence[ProgramUnit]) -> str:
    if any(u.kind == "loose" for u in units):
        return "collection"
    if any(u.kind == "ongoing" or u.brand for u in units):
        return "ongoing"
    if sum(1 for u in units if u.season >= 1900) >= 2:  # a season per year
        return "ongoing"
    return "limited"


def _cadence(
    units: Sequence[ProgramUnit], tags: Mapping[str, float], type_: str, cfg: PlannerConfig
) -> tuple[str, str]:
    """(``weekly`` | ``strip``, why). The show's own rhythm when its exact air dates
    show one; otherwise its format and type (documented defaults)."""
    gap = _median_gap_days(units, cfg)
    if gap is not None:
        if gap <= cfg.strip_max_gap_days:
            return "strip", f"aired every {gap:.1f} days (exact dates): a daily strip"
        return "weekly", f"aired every {gap:.0f} days (exact dates): weekly"
    fw = max((_format_score(tags, f) for f in cfg.weekly_formats), default=0.0)
    fs = max((_format_score(tags, f) for f in cfg.strip_formats), default=0.0)
    n = sum(len(u.episodes) for u in units)
    if type_ == "collection":
        return "weekly", "stand-alone videos grouped by topic: weekly"
    if type_ == "ongoing" and fw >= fs:
        return "weekly", "an ongoing talk show / programme: weekly (no exact dates)"
    if fs >= cfg.format_min_score and fs >= fw:
        return "strip", "a lesson / Q&A series: a daily strip (no exact dates)"
    if fw >= cfg.format_min_score:
        return "weekly", "a programme / interview: weekly (no exact dates)"
    if type_ == "limited" and n <= cfg.strip_max_limited_episodes:
        return "strip", f"a limited series of {n} episodes: a daily strip"
    return "weekly", f"{n} episodes, no format or date evidence: weekly"


def _slot_minutes(median_s: float, cfg: PlannerConfig) -> int:
    """The slot an episode of this length needs, rounded up to the slot step; an
    episode up to ``overrun_tolerance_minutes`` longer keeps the shorter slot."""
    step = cfg.slot_round_minutes or 30
    m = max(1.0, median_s / 60 - cfg.overrun_tolerance_minutes)
    return int(min(cfg.max_slot_minutes, max(cfg.min_slot_minutes, step * math.ceil(m / step))))


def _show_rank(units: Sequence[ProgramUnit], tags: Mapping[str, float], genre: str, type_: str,
               cfg: PlannerConfig) -> float:  # fmt: skip
    """Prime-time strength: programmes, documentaries and interviews over lessons,
    prime-time topics, size, an ongoing run. Production style is only a tie-breaker."""
    hours = sum(u.duration_s for u in units) / 3600
    r = 1.0
    if max((_format_score(tags, f) for f in cfg.rank_formats), default=0.0) >= cfg.format_min_score:
        r += cfg.rank_format_bonus
    if genre in cfg.mix_daypart_topics.get("prime", ()):
        r += cfg.rank_prime_topic_bonus
    r += cfg.rank_size_bonus * min(1.0, hours / max(cfg.rank_size_full_hours, 1e-9))
    if type_ == "ongoing":
        r += cfg.rank_ongoing_bonus
    if type_ == "collection":
        r += cfg.rank_collection_bonus
    tiers = [u.tier for u in units if u.tier is not None]
    if tiers and max(tiers) >= 2:
        r += cfg.rank_style_bonus
    return round(r, 3)


def _shows(units: Iterable[ProgramUnit], tags_of: Mapping[str, Mapping[str, float]],
           cfg: PlannerConfig) -> list[Show]:  # fmt: skip
    """Seasons → shows (folder + series key), seasons in order, strongest first."""
    groups: dict[tuple[str, str], list[ProgramUnit]] = defaultdict(list)
    for u in units:
        groups[(u.folder, u.series_key)].append(u)
    out: list[Show] = []
    for (folder, key), us in groups.items():
        us = _order_units(us)
        tags: dict[str, float] = {}
        for u in us:
            for t, sc in tags_of.get(u.series_id, {}).items():
                tags[t] = max(tags.get(t, 0.0), sc)
        weight: Counter[str] = Counter()
        for u in us:
            weight[u.genre] += int(u.duration_s)
        genre = weight.most_common(1)[0][0] if weight else "general"
        type_ = _show_type(us)
        cadence, why = _cadence(us, tags, type_, cfg)
        durs = [e.duration_s for u in us for e in u.episodes]
        slot = _slot_minutes(float(median(durs)) if durs else 0.0, cfg)
        out.append(
            Show(
                key=f"{folder}|{key}", name=us[0].name, presenter=us[0].presenter, folder=folder,
                genre=genre, units=tuple(us), type=type_, cadence=cadence, slot_minutes=slot,
                rank=_show_rank(us, tags, genre, type_, cfg), ramadan=any(u.ramadan for u in us),
                tags=tags,
                reasons=(f"{type_} show, {len(us)} season(s), "
                         f"{sum(len(u.episodes) for u in us)} episodes; {why}; {slot} min slot",),
            )
        )  # fmt: skip
    out.sort(key=lambda s: (-s.rank, s.first_air, s.name, s.key))
    return out


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


@dataclass(slots=True)
class _Queue:
    """A slot's queue: its shows play one after the other, never interleaved."""

    key: str
    pattern: str  # weekly | strip | strip7
    slot_minutes: int
    start: int
    day: str  # the day of a weekly slot, "" for strips
    shows: list[Show]
    airings: int = 0  # occurrences per week
    extra: int = 0  # occurrences beyond the shows' cadence (a channel short of shows)
    block: Daypart | None = None  # a fixed block's queue: only its kind of show joins
    # (show count, total minutes): shows are only ever appended, so the count says
    # whether the cached total is current. The grid asks for it a few hundred
    # thousand times per plan.
    _mins: tuple[int, float] = field(default=(-1, 0.0), repr=False, compare=False)

    @property
    def family(self) -> str:
        return "strip" if self.pattern in STRIP_FAMILY else "weekly"

    def minutes(self) -> float:
        if self._mins[0] != len(self.shows):
            self._mins = (len(self.shows), sum(_show_minutes(s) for s in self.shows))
        return self._mins[1]

    def weeks_left(self) -> float:
        return self.minutes() / max(1, self.airings * self.slot_minutes)


_UNIT_STATS: dict[int, tuple[ProgramUnit, float, float]] = {}


def _unit_stats(u: ProgramUnit) -> tuple[ProgramUnit, float, float]:
    """(unit, total seconds, longest episode seconds), summed once per unit: the grid
    asks for them tens of thousands of times per plan."""
    hit = _UNIT_STATS.get(id(u))
    if hit is None or hit[0] is not u:
        if len(_UNIT_STATS) > 100_000:
            _UNIT_STATS.clear()
        secs = [e.duration_s for e in u.episodes]
        hit = _UNIT_STATS[id(u)] = (u, sum(secs), max(secs, default=0.0))
    return hit


_SHOW_MINUTES: dict[int, tuple[Show, float]] = {}


def _show_minutes(s: Show) -> float:
    """A show's length in minutes, summed once (the grid asks for it thousands of times)."""
    hit = _SHOW_MINUTES.get(id(s))
    if hit is None or hit[0] is not s:
        if len(_SHOW_MINUTES) > 100_000:
            _SHOW_MINUTES.clear()
        hit = _SHOW_MINUTES[id(s)] = (s, s.duration_s / 60)
    return hit[1]


def _fhm(m: int) -> str:
    return fmt_hhmm(m % DAY_MINUTES)


def _queue_name(q: _Queue, label: str) -> str:
    day = q.day
    if day and q.start >= DAY_MINUTES:  # after midnight: the next day's early hours
        day = WEEKDAYS[(WEEKDAYS.index(day) + 1) % 7]
    when = (
        DAY_NAMES_AR.get(day, day) if q.pattern == "weekly"
        else "يومياً" if q.pattern == "strip7" else "أيام الأسبوع"
    )  # fmt: skip
    head = q.shows[0].name if q.shows else "—"
    return " · ".join(x for x in (label, f"{when} {_fhm(q.start)}", head) if x)


def _show_grid(
    ck: str,
    label: str,
    shows: Sequence[Show],
    dayparts: Sequence[Daypart],
    quran: Lane | None,
    khutbah: Lane | None,
    pools: Mapping[str, str | None],
    cfg: PlannerConfig,
    *,
    ramadan: bool = False,
) -> tuple[list[Slot], list[Lane], list[str]]:
    """A TV week: every slot is owned by a show and keeps a queue.

    Dayparts are filled strongest first (Ramadan after iftar, then prime time and
    the evening, then the rest). A ``strip`` daypart gives each block one strip
    show at the same time every weekday (its weekend days get weekly shows); a
    ``weekly`` daypart gives every day its own shows. A show heads one slot only
    and never airs more often than its cadence — a strip show may fill a weekly
    slot (slower), a weekly show never a strip. Adjacent blocks avoid the same
    genre. When the channel has more slots than shows, existing queues get extra
    airings (``extra_airings``) or the time is filler. The shows left over join
    the queues of their cadence, balanced so every queue runs about as long: when
    a limited series ends, the next show takes its slot; an ongoing show plays all
    its years in its slot before the queue moves on."""
    strip_days = tuple(d for d in WEEKDAYS if d not in cfg.weekend_days)
    weekend = tuple(d for d in WEEKDAYS if d in cfg.weekend_days)
    warnings: list[str] = []
    # too few shows for the week at their own cadence: the biggest weekly shows become
    # daily strips (an archive channel re-runs its long-running shows every weekday)
    week_min = sum(
        d.minutes * 7 for d in dayparts if not (d.pattern == "quran" and quran is not None)
    )
    demand = sum(s.slot_minutes * (len(strip_days) if s.cadence == "strip" else 1) for s in shows)
    if week_min and demand < cfg.fill_target * week_min:
        promoted: list[Show] = []
        out_shows = list(shows)
        for s in sorted(
            (
                s
                for s in shows
                if s.cadence == "weekly" and s.episode_count >= cfg.promote_min_episodes
            ),
            key=lambda s: -s.episode_count,
        ):
            if demand >= cfg.fill_target * week_min:
                break
            i = out_shows.index(s)
            out_shows[i] = replace(
                s,
                cadence="strip",
                reasons=(*s.reasons, "moved to a daily strip: the channel is short of shows"),
            )
            promoted.append(s)
            demand += s.slot_minutes * (len(strip_days) - 1)
        if promoted:
            warnings.append(
                f"{len(promoted)} weekly show(s) air as daily strips (too few shows for a full "
                f"week at their own cadence): {', '.join(s.name for s in promoted[:5])}"
            )
        shows = out_shows
    used: set[str] = set()
    heads: set[str] = set()  # presenters who own a slot
    queues: dict[str, _Queue] = {}
    per_day: dict[str, list[_Seg]] = {d: [] for d in WEEKDAYS}
    strip_c = [s for s in shows if s.cadence == "strip"]
    weekly_c = [s for s in shows if s.cadence == "weekly"]
    prefix = f"{ck}.ramadan" if ramadan else ck

    def pick(cands: Sequence[Show], d: Daypart, prev: str | None, room: int,
             *, ramadan_first: bool = False) -> Show | None:  # fmt: skip
        topics = cfg.mix_daypart_topics.get(d.key, ())
        block = d.role == "block" and bool(d.topics)
        best: tuple[Any, ...] | None = None
        out: Show | None = None
        for i, s in enumerate(cands):
            if s.key in used or s.slot_minutes > room:
                continue
            if d.exclude and _excluded(s.tags, d.exclude, cfg):
                continue
            miss = 0 if s.genre in topics else 1
            k = (
                (0 if s.ramadan else 1) if ramadan_first else 0,
                # a block (the kids' hour) takes its kind of show first, in its order
                _tag_rank(s.genre, s.tags, d.topics, cfg) if block else 0,
                1 if s.genre == prev else 0,  # variety first: never the same genre twice in a row
                # a presenter owns one slot: his other shows follow in it
                1 if cfg.slot_owner == "presenter" and s.presenter in heads else 0,
                -s.rank if d.role != "low" else miss,
                miss if d.role != "low" else -s.rank,
                i,
            )
            if best is None or k < best:
                best, out = k, s
        return out

    def extra_pick(family: str, room: int, prev_q: str | None, day_qs: set[str],
                   prev: str | None) -> _Queue | None:  # fmt: skip
        if not cfg.extra_airings:
            return None

        def weeks_after(q: _Queue) -> float:  # weeks of content once it airs once more
            return q.minutes() / max(1, (q.airings + 1) * q.slot_minutes)

        fits = [q for q in queues.values() if q.slot_minutes <= room and q.key != prev_q
                and q.key not in day_qs]  # fmt: skip
        # prefer queues that keep 8+ weeks of content; if none does, the deepest one
        # (a channel of repeats beats a channel of clips)
        cands = [q for q in fits if weeks_after(q) >= cfg.extra_min_weeks_left] or fits
        if not cands:
            return None
        # the queue with the most weeks of content left takes it: a deep archive absorbs
        # the repeats, a 7-episode series does not loop all week
        return min(
            cands,
            key=lambda q: (
                -q.weeks_left() * (0.5 if q.shows[0].genre == prev else 1.0),
                q.family != family,
                q.key,
            ),
        )

    def put(days: Sequence[str], a: int, b: int, q: _Queue, d: Daypart, why: str) -> None:
        for day in days:
            per_day[day].append(_Seg(a, b, q.key, d.key, None, "program", (why,)))
        q.airings += len(days)

    def fill(d: Daypart, days: Sequence[str], pattern: str) -> None:
        """Blocks from the daypart's start to its end on ``days`` (one block = one
        queue: the same show on every day of a strip, each day its own when weekly)."""
        groups: list[tuple[str, ...]] = [tuple(days)] if pattern in STRIP_FAMILY else [
            (day,) for day in days]  # fmt: skip
        end = d.start + d.minutes
        for grp in groups:
            t = d.start
            prev: str | None = None
            prev_q: str | None = None
            day_qs: set[str] = set()
            last: list[_Seg] = []
            # the Friday khutbah owns its window: that day's slots go around it
            kb = (
                (cfg.khutbah_start, cfg.khutbah_end)
                if khutbah is not None and cfg.friday_khutbah and grp == (cfg.khutbah_day,)
                else None
            )
            while t < end:
                if kb and kb[0] <= t % DAY_MINUTES < kb[1]:
                    t += kb[1] - t % DAY_MINUTES
                    last, prev_q = [], None
                    continue
                stop = end
                if kb and t % DAY_MINUTES < kb[0] < t % DAY_MINUTES + (end - t):
                    stop = t + kb[0] - t % DAY_MINUTES
                room = stop - t
                if pattern in STRIP_FAMILY:
                    s = pick(strip_c if pattern == "strip" else shows, d, prev, room,
                             ramadan_first=pattern == "strip7")  # fmt: skip
                else:  # a strip show may air weekly; a weekly show never daily
                    s = pick(weekly_c, d, prev, room) or pick(strip_c, d, prev, room)
                q: _Queue | None = None
                why = ""
                if s is not None:
                    used.add(s.key)
                    heads.add(s.presenter)
                    where = grp[0] if pattern == "weekly" else pattern
                    key = f"{prefix}.{where}.{_fhm(t).replace(':', '')}"
                    q = _Queue(
                        key,
                        pattern,
                        s.slot_minutes,
                        t,
                        grp[0] if pattern == "weekly" else "",
                        [s],
                        block=d if d.role == "block" and d.topics else None,
                    )
                    queues[key] = q
                    why = (
                        f"{d.label or d.key}: {s.name} ({s.cadence}, {s.type}, rank {s.rank:.2f})"
                        + (" — Ramadan show" if pattern == "strip7" and s.ramadan else "")
                    )
                elif pattern == "weekly" or not (strip_c or shows):
                    q = extra_pick(pattern if pattern == "weekly" else "strip", room, prev_q,
                                   day_qs, prev)  # fmt: skip
                    if q is not None:
                        q.extra += len(grp)
                        why = (f"{d.label or d.key}: extra airing of {q.shows[0].name} — the "
                               "channel has fewer shows than slots")  # fmt: skip
                elif pattern == "strip":  # no strip show left: the weekdays take weekly shows,
                    # after the weekly dayparts had their pick (prime time first)
                    later.append((_Daypart_from(d, t), grp))
                    return
                if q is None:
                    # extra airings allowed: the previous block runs on to the daypart's
                    # end; otherwise the time is filler of its own, so a show never airs
                    # more often than its cadence
                    if last and cfg.extra_airings:
                        for sg in last:
                            sg.end = stop
                    else:
                        for day in grp:
                            per_day[day].append(
                                _Seg(
                                    t,
                                    stop,
                                    None,
                                    d.key,
                                    pools.get("topic"),
                                    "flex",
                                    ("no show left for this time: filler only",),
                                )
                            )
                    if stop >= end:
                        break
                    t, last = stop, []
                    continue
                b = min(stop, t + q.slot_minutes)
                if q.shows[0].max_episode_s > (b - t + cfg.overrun_tolerance_minutes) * 60:
                    why += (
                        f"; longest episode {q.shows[0].max_episode_s / 60:.0f} min "
                        f"overruns the {b - t} min slot"
                    )
                put(grp, t, b, q, d, why)
                last = [sg for day in grp for sg in per_day[day][-1:]]
                prev, prev_q = q.shows[0].genre, q.key
                day_qs.add(q.key)
                t = b

    later: list[tuple[Daypart, tuple[str, ...]]] = []  # weekly fills after the strips
    order = sorted(dayparts, key=lambda d: (
        0 if d.role == "ramadan" else 1 if d.role == "block" else 2 if d.role == "high" else 3,
        -d.start if d.role == "high" else d.start,
    ))  # fmt: skip
    weekly_tasks: list[tuple[Daypart, tuple[str, ...]]] = []
    # strips first (they need a show for every weekday), then the weekly slots: prime
    # time, the other weekly dayparts, the strip dayparts' weekends
    for d in order:
        if d.pattern == "quran" and quran is not None:
            for day in WEEKDAYS:
                per_day[day].append(_Seg(d.start, d.start + d.minutes, quran.key, d.key,
                                         pools.get("dawn"), "quran",
                                         (f"{d.label or d.key}: Quran recitation",)))  # fmt: skip
            continue
        pattern = "strip" if d.pattern == "quran" else d.pattern
        if pattern == "strip7" and not ramadan:
            pattern = "strip"
        if pattern == "strip":
            fill(d, strip_days, "strip")
            if weekend:
                later.append((d, weekend))
        elif pattern == "strip7":
            fill(d, WEEKDAYS, "strip7")
        else:
            weekly_tasks.append((d, WEEKDAYS))
    for d, days in weekly_tasks + later:
        fill(d, days, "weekly")

    def joins(q: _Queue, s: Show) -> bool:
        """A block's queue (the kids' hour) takes only its kind of show."""
        b = q.block
        return b is None or (
            _tag_rank(s.genre, s.tags, b.topics, cfg) < len(b.topics)
            and not _excluded(s.tags, b.exclude, cfg)
        )

    # the shows left over join the queues of their cadence: their presenter's slot
    # first (slot_owner presenter), else balanced by how long each queue runs
    for s in [s for s in shows if s.key not in used]:
        fam = "strip" if s.cadence == "strip" else "weekly"
        open_ = [q for q in queues.values() if joins(q, s)]
        cands = [q for q in open_ if q.family == fam] or open_
        if not cands:
            warnings.append(f"{s.name}: no slot on this channel")
            continue
        fit = [q for q in cands
               if s.slot_minutes <= q.slot_minutes + cfg.overrun_tolerance_minutes]  # fmt: skip
        if not fit:
            # in a shorter slot its episodes would run into the next show's
            warnings.append(f"{s.name}: needs {s.slot_minutes} min, no slot that long on "
                            "this channel")  # fmt: skip
            continue
        if cfg.slot_owner == "presenter":
            # the next show in a slot is related: the same sheikh's, else the same topic
            fit = (
                [q for q in fit if q.shows[0].presenter == s.presenter]
                or [q for q in fit if any(x.genre == s.genre for x in q.shows)]
                or fit
            )
        q = min(fit, key=lambda q: (q.weeks_left(), q.shows[-1].genre == s.genre, q.key))
        q.shows.append(s)
        used.add(s.key)
    for q in queues.values():
        if q.extra:
            warnings.append(f"{_queue_name(q, label)}: {q.extra} extra airing(s) a week "
                            "(the channel has fewer shows than slots)")  # fmt: skip

    lanes: dict[str, Lane] = {}
    for q in queues.values():
        head = q.shows[0]
        lanes[q.key] = Lane(
            key=q.key, name=_queue_name(q, label), tier_group="mixed",
            units=tuple(u for s in q.shows for u in s.units), rank=head.rank, ramadan=ramadan,
            pattern=q.pattern, cadence=q.family, slot_minutes=q.slot_minutes,
            shows=tuple(s.name for s in q.shows),
            reasons=(
                f"{q.airings} airing(s) a week, {q.slot_minutes} min",
                *head.reasons,
                *([f"then, in this slot: {', '.join(s.name for s in q.shows[1:6])}"
                   + (f" and {len(q.shows) - 6} more" if len(q.shows) > 6 else "")]
                  if len(q.shows) > 1 else []),
            ),
        )  # fmt: skip

    # unwrap past midnight (the rest belongs to the next day), then cover holes with flex
    flat: dict[str, list[_Seg]] = {d: [] for d in WEEKDAYS}
    for i, day in enumerate(WEEKDAYS):
        nxt = WEEKDAYS[(i + 1) % 7]
        for sg in per_day[day]:
            if sg.start >= DAY_MINUTES:  # wholly after midnight: the next day's early hours
                flat[nxt].append(_Seg(sg.start - DAY_MINUTES, sg.end - DAY_MINUTES, sg.lane,
                                      sg.daypart, sg.pool, sg.kind, sg.reasons))  # fmt: skip
            elif sg.end > DAY_MINUTES:
                flat[day].append(_Seg(sg.start, DAY_MINUTES, sg.lane, sg.daypart, sg.pool,
                                      sg.kind, sg.reasons))  # fmt: skip
                flat[nxt].append(_Seg(0, sg.end - DAY_MINUTES, sg.lane, sg.daypart, sg.pool,
                                      sg.kind, sg.reasons))  # fmt: skip
            else:
                flat[day].append(sg)
    out_days: dict[str, list[_Seg]] = {}
    for day in WEEKDAYS:
        segs = sorted(flat[day], key=lambda s: s.start)
        filled: list[_Seg] = []
        t = 0
        for sg in segs:
            if sg.start > t:
                filled.append(_Seg(t, sg.start, None, "gap", pools.get("topic"), "flex",
                                   ("no daypart covers this time: filler only",)))  # fmt: skip
            if sg.end > t:
                filled.append(_Seg(max(sg.start, t), sg.end, sg.lane, sg.daypart, sg.pool, sg.kind,
                                   sg.reasons))  # fmt: skip
                t = sg.end
        if t < DAY_MINUTES:
            filled.append(_Seg(t, DAY_MINUTES, None, "gap", pools.get("topic"), "flex",
                               ("no daypart covers this time: filler only",)))  # fmt: skip
        out_days[day] = filled
    used_lanes = list(lanes.values())
    if khutbah is not None and cfg.friday_khutbah:
        day = cfg.khutbah_day
        out_days[day] = _overlay(
            out_days[day],
            _Seg(cfg.khutbah_start, cfg.khutbah_end, khutbah.key, "khutbah", pools.get("topic"),
                 "khutbah", (f"Friday khutbah {fmt_hhmm(cfg.khutbah_start)}–"
                             f"{fmt_hhmm(cfg.khutbah_end)} ({khutbah.episode_count} khutbahs, "
                             "one per week)",)),
        )  # fmt: skip
        used_lanes.append(khutbah)
    if quran is not None:
        used_lanes.append(quran)
    slots = _merge_days(out_days)
    referenced = {s.lane for s in slots}
    return slots, [ln for ln in used_lanes if ln.key in referenced], warnings


def _Daypart_from(d: Daypart, start: int) -> Daypart:  # noqa: N802 — a Daypart "constructor"
    """The rest of a daypart from ``start`` (minutes may run past midnight)."""
    end = d.start + d.minutes
    return replace(d, start=start, end=end % DAY_MINUTES if end > DAY_MINUTES else end)


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


# --------------------------------------------------------------------------- Quran channel

QURAN_STYLES_AR: dict[str, str] = {"warsh": "ورش", "mujawwad": "مجود", "murattal": "مرتل"}


def recitation_style(u: ProgramUnit, cfg: PlannerConfig | None = None) -> str:
    cfg = cfg or PlannerConfig()
    text = f"{u.library} {u.name} {u.folder} {u.presenter}".lower()
    for style, words in cfg.quran_styles.items():
        if any(w.lower() in text for w in words):
            return style
    return cfg.quran_default_style


def _reciter_lanes(units: Sequence[ProgramUnit], cfg: PlannerConfig) -> list[Lane]:
    """One lane per reciter and style (a reciter's library: الحصري, عبد الباسط - مجود …),
    his surahs in their order."""
    groups: dict[tuple[str, str], list[ProgramUnit]] = defaultdict(list)
    for u in units:
        reciter = u.library or u.presenter or Path(u.folder.replace("\\", "/")).name
        groups[(reciter, recitation_style(u, cfg))].append(u)
    lanes: list[Lane] = []
    for (reciter, style), us in groups.items():
        us = _order_units(us)
        has_word = any(w.lower() in reciter.lower() for w in cfg.quran_styles.get(style, ()))
        name = (
            reciter
            if has_word or style == cfg.quran_default_style
            else f"{reciter} · {QURAN_STYLES_AR.get(style, style)}"
        )
        lanes.append(Lane(
            key=f"quran.{_slug(reciter)}.{style}", name=name, tier_group="mixed", units=tuple(us),
            rank=0.0, special="quran", pattern="quran", cadence=style, shows=(name,),
            reasons=(f"{reciter}, {QURAN_STYLES_AR.get(style, style)}: "
                     f"{sum(len(u.episodes) for u in us)} "
                     "recitations in order; each airing resumes where he stopped, and after "
                     "the last surah the next khatma starts",),
        ))  # fmt: skip
    # Hafs murattal first (the everyday recitation), then the longest
    lanes.sort(key=lambda ln: (ln.cadence != "murattal", -ln.duration_s, ln.key))
    return lanes


def _quran_grid(lanes: Sequence[Lane], pool: str | None,
                cfg: PlannerConfig) -> tuple[list[Slot], list[str]]:  # fmt: skip
    """Recitation non-stop. ``by_day``: a reciter per day (Saturday one reciter,
    Sunday the next …); ``by_time``: reciters rotate every few hours. On top,
    ``quran_time_rules`` give set hours to a style (mujawwad after Fajr, warsh at
    night), rotating among the reciters of that style day by day. Continuity: a
    reciter's lane resumes where it stopped the last time he aired, so each runs
    his khatma through the week and starts the next one after the last surah."""
    why: list[str] = []
    if not lanes:
        return [], why
    order = ("sat", "sun", "mon", "tue", "wed", "thu", "fri")  # the week as it is lived
    # reciters of a style the time rules give set hours to keep those hours; the others
    # share the days — more than 7 of them split each day (up to 4 parts) so all air
    ruled = {style for _, _, style in cfg.quran_time_rules}
    day_lanes = [ln for ln in lanes if ln.cadence not in ruled] or list(lanes)
    parts = min(max(1, cfg.quran_max_reciters_per_day), max(1, math.ceil(len(day_lanes) / 7)))
    step_p = DAY_MINUTES // parts

    def day_reciters(i: int) -> list[Lane]:
        return [day_lanes[(i * parts + k) % len(day_lanes)] for k in range(parts)]

    per_day: dict[str, list[_Seg]] = {}
    for i, day in enumerate(order):
        if cfg.quran_rotation == "by_time":
            step = max(1, cfg.quran_rotation_hours) * 60
            segs = [_Seg(a, a + step, lanes[(i * 6 + a // step) % len(lanes)].key, "quran", pool,
                         "quran", (f"reciters rotate every {cfg.quran_rotation_hours} hours",))
                    for a in range(0, DAY_MINUTES, step)]  # fmt: skip
        else:
            segs = [
                _Seg(k * step_p, DAY_MINUTES if k == parts - 1 else (k + 1) * step_p, ln.key,
                     "quran", pool, "quran", (f"{DAY_NAMES_AR[day]}: {ln.name}",))
                for k, ln in enumerate(day_reciters(i))
            ]  # fmt: skip
        for a, b, style in cfg.quran_time_rules:
            cands = [ln for ln in lanes if ln.cadence == style]
            if not cands:
                continue
            # more reciters of the style than days: the window is split so every one airs
            n = math.ceil(len(cands) / 7)
            lo, hi = _hm(a), _hm(b) or DAY_MINUTES
            cut = [lo + (hi - lo) * k // n for k in range(n + 1)]
            for k in range(n):
                ln = cands[(i * n + k) % len(cands)]
                segs = _overlay(
                    segs,
                    _Seg(
                        cut[k],
                        cut[k + 1],
                        ln.key,
                        "quran",
                        pool,
                        "quran",
                        (f"{a}–{b}: {QURAN_STYLES_AR.get(style, style)} — {ln.name}",),
                    ),
                )
        per_day[day] = segs
    if cfg.quran_rotation == "by_time":
        why.append(f"reciters rotate every {cfg.quran_rotation_hours} hours")
    else:
        why.append(
            ("a reciter per day: " if parts == 1 else f"{parts} reciters a day: ")
            + ", ".join(
                f"{DAY_NAMES_AR[d]} {' / '.join(ln.name for ln in day_reciters(i))}"
                for i, d in enumerate(order)
            )
        )
    for a, b, style in cfg.quran_time_rules:
        if any(ln.cadence == style for ln in lanes):
            why.append(f"{a}–{b} every day: {QURAN_STYLES_AR.get(style, style)} recitation")
    why.append("continuity: a reciter resumes where he stopped; after the last surah the next "
               "khatma starts")  # fmt: skip
    return _merge_days(per_day), why


# --------------------------------------------------------------------------- planner


def suggest_channels(
    seasons: Sequence[SeasonCandidate],
    loose: Sequence[LooseVideo] = (),
    config: PlannerConfig | None = None,
) -> PlanResult:
    """Suggest live channels for a library. See the module docstring."""
    cfg = config or PlannerConfig()
    if cfg.library_tags:  # tags the user gives whole libraries (audience:kids_friendly …)

        def with_lib(lib: str | None, tags: Mapping[str, float]) -> Mapping[str, float]:
            add = cfg.library_tags.get(lib or "", ())
            return {**tags, **dict.fromkeys(add, 1.0)} if add else tags

        seasons = [replace(x, tags=with_lib(x.library, x.tags)) for x in seasons]
        loose = [replace(v, tags=with_lib(v.plex_library, v.tags)) for v in loose]
    all_seasons = list(seasons) + _loose_units(loose, cfg)
    fillers, filler_notes = _filler_items(all_seasons, loose, cfg)
    # promos air only next to the show they advertise, never as anyone's general filler
    promos = [f for f in fillers if f.promo_of]
    fillers = [f for f in fillers if not f.promo_of]
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

    # Quran comes from the reciters' libraries (most of their hours are recitation)
    lib_hours: dict[str, float] = defaultdict(float)
    lib_rec: dict[str, float] = defaultdict(float)
    for s in programs:
        lib_hours[s.library] += s.duration_s
        if _is_recitation(s, cfg):
            lib_rec[s.library] += s.duration_s
    reciter_libs = {
        lib for lib, h in lib_rec.items() if h >= cfg.quran_library_min_share * lib_hours[lib]
    }
    quran_ids = {
        s.series_id
        for s in programs
        # no reciter library at all: the library's own recitations still make the dawn
        # lane (a scholar's channel with a few recitations keeps them on air)
        if _is_recitation(s, cfg)
        and (s.library in reciter_libs or (not reciter_libs and cfg.quran_fallback_any_recitation))
    }
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

    seasons_of_show: dict[tuple[str, str], list[str]] = defaultdict(list)
    for i, u in units.items():
        seasons_of_show[(u.folder, u.series_key)].append(i)
    show_hours = {k: sum(units[i].duration_s for i in v) for k, v in seasons_of_show.items()}

    def whole_shows(ids: set[str]) -> set[str]:
        """A show joins a channel whole (all its years), when at least
        ``show_member_min_share`` of its hours belong there — never a few seasons."""
        mine: dict[tuple[str, str], float] = defaultdict(float)
        for i in ids:
            if (u := units.get(i)) is not None:
                mine[(u.folder, u.series_key)] += u.duration_s
        out: set[str] = set()
        for show, h in mine.items():
            total = show_hours[show]
            if total and h >= cfg.show_member_min_share * total:
                out.update(seasons_of_show[show])
        return out

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

    def slot_pool(ln: Lane, ck: str, label: str, topic_key: str | None,
                  exclude: tuple[str, ...] = ()) -> str | None:  # fmt: skip
        """A slot's filler: its own shows' promos first ("coming up"), then clips on its
        topics, then the channel's on-topic filler, then the library's general filler."""
        genres = list(dict.fromkeys(u.genre for u in ln.units))[: max(1, cfg.slot_filler_topics)]
        gk = f"{ck}.topic.{'+'.join(_slug(g) for g in genres)}" if genres else None
        if gk is not None and gk not in pools:
            items = [f for f in fillers if f.topic_score(genres) >= fmin
                     and not _excluded(f.tags, exclude, cfg)]  # fmt: skip
            if items:
                pools[gk] = _pool(
                    gk,
                    f"{label} · {' / '.join(genres)}",
                    items,
                    topic_key,
                    f"filler on this slot's topics ({', '.join(genres)})",
                )
        nxt = gk if gk in pools else topic_key
        shows = {f"{u.folder}|{u.series_key}" for u in ln.units}
        mine = [f for f in promos if f.promo_of in shows]
        if not mine:
            return nxt
        pk_ = f"{ln.key}.promo"
        if pk_ not in pools:
            pools[pk_] = _pool(
                pk_,
                f"{ln.name} · إعلانات",
                mine,
                nxt,
                f"{len(mine)} promo(s) of this slot's shows, then its topics",
            )
        return pk_

    def make_channel(
        key: str, number: int, name: str, name_ar: str, kind: str, category: str,
        member_ids: set[str], why: list[str], pool_keys: dict[str, str | None], *, mix: bool,
        exclude: tuple[str, ...] = (),
    ) -> None:  # fmt: skip
        member_ids = whole_shows(member_ids)
        reg = regular(member_ids)
        label = name_ar or name
        shows = _shows(reg, tags_of, cfg)
        q = quran_lane if cfg.dawn_quran else None
        k = khutbah_lane if (khutbah_lane and (mix or member_ids & khutbah_ids)) else None
        slots, used, warn = _show_grid(key, label, shows, cfg.dayparts, q, k, pool_keys, cfg)
        r_slots: list[Slot] = []
        r_used: list[Lane] = []
        # the Ramadan grid sees every show, also those ramadan_exclusive keeps off the
        # normal grid
        r_shows = (
            _shows([units[i] for i in member_ids if i in regular_ids], tags_of, cfg)
            if cfg.ramadan_exclusive
            else shows
        )
        ram = [s for s in r_shows if s.ramadan]
        if cfg.ramadan_grid and ram:
            r_slots, r_used, w2 = _show_grid(key, label, r_shows, cfg.ramadan_dayparts, q, k,
                                             pool_keys, cfg, ramadan=True)  # fmt: skip
            warn += w2
            why.append(
                f"{len(ram)} Ramadan-tagged show(s) (occasion:ramadan ≥ {cfg.ramadan_min_score}): "
                "the Ramadan grid airs them every day after iftar"
            )
        n_strip = sum(1 for s in shows if s.cadence == "strip")
        why.append(
            f"{len(shows)} shows ({sum(1 for s in shows if s.type == 'ongoing')} ongoing, "
            f"{sum(1 for s in shows if s.type == 'limited')} limited, "
            f"{sum(1 for s in shows if s.type == 'collection')} collections): {n_strip} air as "
            f"daily strips, {len(shows) - n_strip} weekly; each slot keeps a queue"
        )
        if q is not None:
            why.append(f"dawn plays the library's Quran recitation lane ({q.hours:.1f} h)")
        elif cfg.dawn_quran:
            why.append("no Quran recitation in the library: dawn falls back to shows")
        if k is not None:
            why.append(f"Friday khutbah slot from {k.episode_count} khutbah(s)")
        for ln in used + r_used:
            lanes.setdefault(ln.key, ln)
        slot_pools: dict[str, str | None] = {}
        for ln in used + r_used:
            if not ln.special:
                slot_pools[ln.key] = slot_pool(ln, key, label, pool_keys.get("topic"), exclude)
        slots = [
            replace(sl, filler_pool=slot_pools[sl.lane])
            if sl.kind == "program" and sl.lane in slot_pools
            else sl
            for sl in slots
        ]
        r_slots = [
            replace(sl, filler_pool=slot_pools[sl.lane])
            if sl.kind == "program" and sl.lane in slot_pools
            else sl
            for sl in r_slots
        ]
        # one list of every promo of the channel's shows (dizqueTV fills per channel)
        shows_keys = {s.key for s in shows}
        ch_promos = [f for f in promos if f.promo_of in shows_keys]
        promo_key: str | None = None
        if ch_promos:
            promo_key = f"{key}.promos"
            pools[promo_key] = _pool(
                promo_key,
                f"{label} · إعلانات البرامج",
                ch_promos,
                pool_keys.get("topic"),
                "promos of this channel's shows",
            )
            why.append(f"{len(ch_promos)} promo(s) of its shows pad their own slots first")
        hours = sum(s.duration_s for s in shows) / 3600
        channels.append(
            ChannelSuggestion(
                key=key, number=number, name=name, name_ar=name_ar, kind=kind,
                category=category, lanes=tuple(dict.fromkeys(ln.key for ln in used + r_used)),
                slots=tuple(slots), ramadan_slots=tuple(r_slots),
                filler_pools=tuple(dict.fromkeys(
                    p for p in [pool_keys.get("topic"), promo_key, pool_keys.get("dawn"),
                                *slot_pools.values(), general.key] if p is not None)),
                hours=hours, reasons=tuple(why), warnings=tuple(warn),
            )
        )  # fmt: skip

    def make_quran_channel(t: ThemeSpec) -> set[str]:
        """The Quran channel: recitation non-stop, no show grid."""
        rec = [units[i] for i in quran_ids]
        hours = sum(u.duration_s for u in rec) / 3600
        reciters = _reciter_lanes(rec, cfg)
        talk = cfg.quran_filler_exclude_formats
        items = [
            f for f in fillers
            if f.topic_score(cfg.quran_filler_topics) >= fmin
            and max((_format_score(f.tags, x) for x in talk), default=0.0) < cfg.format_min_score
        ]  # fmt: skip
        pk_: str | None = None
        if items:
            pk_ = "quran.filler"
            pools[pk_] = _pool(pk_, "تلاوات قصيرة وأدعية", items, None,
                               "short recitations and dua only (never talk)")  # fmt: skip
        slots, why = _quran_grid(reciters, pk_, cfg)
        for ln in reciters:
            lanes.setdefault(ln.key, ln)
        referenced = {s.lane for s in slots}
        channels.append(
            ChannelSuggestion(
                key=t.key, number=t.number, name=t.name, name_ar=t.name_ar, kind="quran",
                category=t.category,
                lanes=tuple(ln.key for ln in reciters if ln.key in referenced),
                slots=tuple(slots), ramadan_slots=(),
                filler_pools=(pk_,) if pk_ else (), hours=hours,
                reasons=(f"{len(reciters)} reciter lane(s), {hours:.1f} h of recitation: Quran "
                         "non-stop, no show grid", *why,
                         "filler: short recitations and dua only" if pk_ else
                         "no short recitation / dua clips: gaps stay silent (flex)"),
            )
        )  # fmt: skip
        return set(quran_ids)

    # ---- single episodes about a theme, from the shows it does not take whole
    synthetic: set[str] = set()

    def theme_episode_units(t: ThemeSpec, have: set[str]) -> set[str]:
        """One collection per presenter of the episodes whose own title is about the
        theme (its topics, audiences, occasions), from every show not in ``have``."""
        names = [*t.topics, *(f"audience:{a}" for a in t.audiences),
                 *(f"occasion:{o}" for o in t.occasions)]  # fmt: skip
        if not names or not cfg.theme_episodes:
            return set()
        keys = [n if ":" in n else f"topic:{n}" for n in names]
        mins = cfg.theme_episode_min_minutes * 60
        by_src: dict[str, list[tuple[ProgramUnit, EpisodeRef]]] = defaultdict(list)
        for i, u in units.items():
            if i in have or i in synthetic or _excluded(tags_of.get(i, {}), t.exclude, cfg):
                continue
            for e in u.episodes:
                if e.duration_s < mins or not e.tags or _excluded(e.tags, t.exclude, cfg):
                    continue
                if max(e.tags.get(k, 0.0) for k in keys) >= cfg.theme_episode_min_score:
                    by_src[u.presenter or u.library or u.folder].append((u, e))
        out: set[str] = set()
        label = t.name_ar or t.name
        for src, pairs in sorted(by_src.items()):
            if len(pairs) < cfg.theme_episode_min_count:
                continue
            pairs.sort(key=lambda p: (p[1].air_date or p[0].first_air, p[0].first_air,
                                      p[0].season, p[1].episode or 0))  # fmt: skip
            first = pairs[0][0]
            sid = f"{t.key}~{_slug(src)}"
            nu = ProgramUnit(
                series_id=sid, name=f"{src} · {label}", season=1, presenter=first.presenter,
                folder=first.folder, kind="loose", genre=t.topics[0] if t.topics else first.genre,
                tier=None, style=None, ramadan=any(u.ramadan for u, _ in pairs),
                first_air=min(u.first_air for u, _ in pairs), series_key=sid,
                episodes=tuple(e for _, e in pairs), library=first.library,
            )  # fmt: skip
            units[sid] = nu
            synthetic.add(sid)
            regular_ids.add(sid)
            tags_of[sid] = {**dict.fromkeys(keys, 1.0), f"genre:{nu.genre}": 1.0}
            seasons_of_show[(nu.folder, nu.series_key)] = [sid]
            show_hours[(nu.folder, nu.series_key)] = nu.duration_s
            out.add(sid)
        return out

    # ---- the lineup: every candidate with enough content, at most max_channels of them
    month_h = cfg.channel_min_days * cfg.channel_day_hours
    kind_rank = {k: i for i, k in enumerate(cfg.channel_kind_order)}
    cands: list[tuple[tuple[int, float], str, Callable[[], set[str]]]] = []

    def short(label: str, hours: float, need: float) -> None:
        unplaced.append((label, f"only {hours:.1f} h, needs {need:.0f} h "
                                f"({cfg.channel_min_days:g} days at {cfg.channel_day_hours:g} h a "
                                "day without a repeat, or the kind's own minimum)"))  # fmt: skip

    def offer(kind: str, order: float, label: str, build: Callable[[], set[str]]) -> None:
        if kind not in kind_rank:
            unplaced.append((label, f"{kind} is not in channel_kind_order"))
            return
        cands.append(((kind_rank[kind], order), label, build))

    def build_theme(t: ThemeSpec, ids: set[str], hours: float) -> set[str]:
        topics = t.topics or ("__none__",)
        pk = channel_pools(
            t.key, t.name,
            (f for f in fillers
             if not _excluded(f.tags, t.exclude, cfg)
             and (f.topic_score(topics) >= fmin
                  or _score(f.tags, "audience", t.audiences) >= fmin
                  or _score(f.tags, "occasion", t.occasions) >= fmin
                  or _score(f.tags, "format", t.formats) >= fmin)),
            f"filler tagged {', '.join(t.topics + t.audiences + t.occasions + t.formats)} "
            f"(≥ {fmin})" + (f", never {', '.join(t.exclude)}" if t.exclude else ""),
        )  # fmt: skip
        n_series = len({(units[i].folder, units[i].series_key) for i in ids})
        n_libs = len({units[i].library for i in ids})
        why = [
            f"{len(ids)} season(s) of {n_series} series from {n_libs} libraries, {hours:.1f} h "
            f"(≈ {hours / max(1.0, cfg.channel_day_hours):.0f} days without a repeat) match "
            f"{'/'.join(t.topics + t.audiences + t.formats + t.occasions)} "
            f"(genre or score ≥ {cfg.topic_min_score})"
            + (f", never {', '.join(t.exclude)}" if t.exclude else "")
        ]
        make_channel(t.key, t.number, t.name, t.name_ar, "theme", t.category, ids, why, pk,
                     mix=False, exclude=t.exclude)  # fmt: skip
        return whole_shows(ids)

    for n_t, t in enumerate(cfg.themes):
        need = t.min_hours if t.min_hours is not None else max(cfg.theme_min_hours, month_h)
        if t.key == "quran":
            q_hours = sum(units[i].duration_s for i in quran_ids) / 3600
            if not quran_ids:
                continue
            if q_hours < need:
                short(f"theme {t.key}", q_hours, need)
                continue
            offer("quran", n_t, f"theme {t.key}", partial(make_quran_channel, t))
            continue
        ids = {i for i in regular_ids if i in by_id and _in_theme(by_id[i], t, cfg)}
        ids |= {i for i in khutbah_ids if _in_theme(by_id[i], t, cfg)}
        # a show joins whole or not at all, so the hours counted are the hours it airs
        ids = {i for i in whole_shows(ids) if i in regular_ids or i in khutbah_ids}
        ids |= theme_episode_units(t, ids)
        hours = sum(units[i].duration_s for i in ids) / 3600
        if not ids:
            continue
        if hours < need:
            short(f"theme {t.key}", hours, need)
            continue
        offer("theme", n_t, f"theme {t.key}", partial(build_theme, t, ids, hours))

    # ---- mix channel
    excluded = set(cfg.mix_exclude_topics)
    mix_ids = {
        i
        for i in units
        if i not in synthetic  # a theme's episode collection: the mix has the shows
        and units[i].genre not in excluded
        and _score(tags_of[i], "topic", excluded) < cfg.topic_min_score
    }
    mix_ids = whole_shows(mix_ids)
    mix_hours = sum(units[i].duration_s for i in mix_ids if i in regular_ids) / 3600

    def build_mix() -> set[str]:
        pk = channel_pools("mix", cfg.mix_name, fillers, "all on-topic filler of the library")
        genres = {units[i].genre for i in mix_ids}
        make_channel(
            "mix", cfg.mix_number, cfg.mix_name, cfg.mix_name_ar, "mix", "general", mix_ids,
            [f"{len(mix_ids)} season(s), {mix_hours:.1f} h across {len(genres)} genres; "
             "dayparts prefer genres per mix_daypart_topics, adjacent slots differ; "
             f"excluded: {', '.join(excluded) or '-'}"],
            pk, mix=True,
        )  # fmt: skip
        return mix_ids

    if cfg.mix_channel and mix_ids:
        need = max(cfg.mix_min_hours, month_h)
        if mix_hours < need:
            short("mix", mix_hours, need)
        else:
            offer("mix", 0, "mix", build_mix)

    # ---- presenter channels: only the largest libraries
    def build_presenter(presenter: str, ids: set[str], hours: float, genres: set[str],
                        number: int) -> set[str]:  # fmt: skip
        key = f"presenter.{_slug(presenter)}"
        pk = channel_pools(key, presenter, (f for f in fillers if f.presenter == presenter),
                           f"filler from {presenter}'s own uploads")  # fmt: skip
        make_channel(
            key, number, presenter, presenter, "presenter", "islamic", ids,
            [f"{len(ids)} season(s), {hours:.1f} h by {presenter} across {len(genres)} genres "
             "(one of the largest libraries)"], pk, mix=True,
        )  # fmt: skip
        return ids

    if cfg.presenter_channels:
        by_presenter: dict[str, set[str]] = defaultdict(set)
        for i, u in units.items():
            if u.presenter and i not in synthetic:
                by_presenter[u.presenter].add(i)
        big: list[tuple[float, str, set[str], set[str]]] = []
        need = max(cfg.presenter_min_hours, month_h)
        for presenter in sorted(by_presenter):
            ids = whole_shows(by_presenter[presenter])
            hours = sum(units[i].duration_s for i in ids if i in regular_ids) / 3600
            genres = {units[i].genre for i in ids}
            if hours < need or len(genres) < cfg.presenter_min_genres:
                unplaced.append(
                    (f"presenter {presenter}",
                     f"{hours:.1f} h, {len(genres)} genres (needs {need:.0f} h, "
                     f"{cfg.presenter_min_genres} genres): airs on the theme channels")
                )  # fmt: skip
                continue
            big.append((hours, presenter, ids, genres))
        big.sort(key=lambda x: (-x[0], x[1]))
        for j, (hours, presenter, ids, genres) in enumerate(big):
            if j >= cfg.presenter_max_channels:
                unplaced.append(
                    (
                        f"presenter {presenter}",
                        f"{hours:.0f} h, past presenter_max_channels "
                        f"({cfg.presenter_max_channels}): airs on the theme channels",
                    )
                )
                continue
            offer("presenter", j, f"presenter {presenter}",
                  partial(build_presenter, presenter, ids, hours, genres,
                          cfg.presenter_first_number + j))  # fmt: skip

    placed: set[str] = set()
    cands.sort(key=lambda c: c[0])
    for n_c, (_, label, build) in enumerate(cands):
        if n_c >= cfg.max_channels:
            unplaced.append((label, f"past max_channels ({cfg.max_channels}): the channels "
                                    "before it in the lineup order took the places"))  # fmt: skip
            continue
        placed |= build()

    for i in sorted(set(units) - placed - synthetic):
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


def _pick_filler(tiers: Sequence[Sequence[FillerItem]], remaining: float, recent: list[str],
                 window: int) -> FillerItem | None:  # fmt: skip
    """Best fit, tier by tier (the slot's own promos, then its topics, then the
    channel's, then general): the longest item that fits and is not one of the last
    ``window`` picks; a recent one only when nothing else in any tier fits."""
    fallback: FillerItem | None = None
    for pool in tiers:
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
    the lane's next episodes while they fit (the first one may overrun, like
    dizqueTV's "slide"), with filler between them (``filler_between_episodes``,
    dizqueTV's ``flexPreference: distribute``) and to the boundary — best fit
    from the slot's pool chain: its own shows' promos, then its topics, the
    channel's, the library's (``FillerPool.fallback``). What no filler fits is
    ``flex`` (dead air: a bumper / slate). Lane cursors persist across slots and
    days, so a lane continues where it stopped."""
    slots = channel.ramadan_slots if ramadan and channel.ramadan_slots else channel.slots
    cfg = plan.config
    cursors: dict[str, int] = defaultdict(int)
    recent: list[str] = []
    out: list[Airing] = []
    t = 0.0
    d0 = WEEKDAYS.index(weekday)
    tier_cache: dict[str | None, list[list[FillerItem]]] = {}

    def pool_tiers(key: str | None) -> list[list[FillerItem]]:
        if key in tier_cache:
            return tier_cache[key]
        tiers: list[list[FillerItem]] = []
        seen: set[str] = set()
        paths: set[str] = set()
        k = key
        while k is not None and k not in seen:
            seen.add(k)
            p = plan.filler_pools.get(k)
            if p is None:
                break
            tier = [i for i in p.items if i.path not in paths]
            paths.update(i.path for i in tier)
            tiers.append(sorted(tier, key=lambda i: (i.vertical, -i.duration_s, i.path)))
            k = p.fallback
        tier_cache[key] = tiers
        return tiers

    def pad(until: float, s: Slot, si: int, *, flex: bool) -> None:
        nonlocal t
        tiers = pool_tiers(s.filler_pool)
        while until - t > 0.5:
            f = _pick_filler(tiers, until - t, recent, cfg.filler_recent_window)
            if f is None or until - t < cfg.filler_min_s:
                if flex:
                    out.append(Airing(t, until, "flex", "flex / bumper", s.lane, None, si))
                    t = until
                return
            out.append(Airing(t, t + f.duration_s, "filler", f.title[:160], s.lane, f.path, si))
            recent.append(f.path)
            t += f.duration_s

    for day_i in range(days):
        day = WEEKDAYS[(d0 + day_i) % 7]
        base = day_i * DAY_MINUTES * 60.0
        for si, s in enumerate(sorted((s for s in slots if s.on(day)), key=lambda s: s.start)):
            ws, we = base + s.start * 60.0, base + s.end * 60.0
            if t >= we:
                continue
            t = max(t, ws)
            lane = plan.lanes.get(s.lane) if s.lane else None
            picks: list[tuple[ProgramUnit, EpisodeRef]] = []
            if lane is not None:
                eps = lane.episodes()
                tt = t
                c = cursors[lane.key]
                tol = cfg.overrun_tolerance_minutes * 60.0
                while eps:
                    u, e = eps[c % len(eps)]
                    # doesn't fit what is left (an episode may end up to the tolerance past
                    # the slot): pad and wait for the next window, unless the episode is
                    # longer than the whole slot (then it overruns)
                    if tt + e.duration_s > we + tol and (picks or e.duration_s <= we - ws + tol):
                        break
                    picks.append((u, e))
                    tt += e.duration_s
                    c += 1
                    if tt >= we:
                        break
                spare = max(0.0, we - tt)
                between = cfg.filler_between_episodes and len(picks) > 1
                for n, (u, e) in enumerate(picks):
                    title = f"{u.label} · {e.title}"[:160]
                    out.append(Airing(t, t + e.duration_s, "program", title, lane.key, e.path, si))
                    t += e.duration_s
                    cursors[lane.key] += 1
                    if between and n < len(picks) - 1:
                        # an even share of the spare time after each episode
                        pad(t + spare / len(picks), s, si, flex=False)
            pad(we, s, si, flex=True)
    return out


def slot_timeline(
    lane: Lane, slots: Sequence[Slot], *, weeks: int = 52, tolerance_minutes: int = 5
) -> list[dict[str, Any]]:
    """What a slot's queue airs, week by week, for ``weeks`` weeks: consecutive
    weeks of one season are one row (``{"from_week", "to_week", "show"}``), so a
    reviewer sees an ongoing show run through its years and the next show take
    over. Counts only the episodes that fit each airing (the first may overrun);
    filler is not simulated. When the queue runs out it starts again (a marker row)."""
    occ = sorted(
        (WEEKDAYS.index(day), s.start, s.minutes)
        for s in slots
        if s.lane == lane.key
        for day in (s.days or WEEKDAYS)
    )
    eps = lane.episodes()
    if not occ or not eps:
        return []
    rows: list[dict[str, Any]] = []
    i = 0

    def note(week: int, label: str) -> None:
        if rows and rows[-1]["show"] == label and rows[-1]["to_week"] >= week - 1:
            rows[-1]["to_week"] = week
        else:
            rows.append({"from_week": week, "to_week": week, "show": label})

    for w in range(1, weeks + 1):
        for _, _, mins in occ:
            room = (mins + tolerance_minutes) * 60.0  # as simulate: a little overrun is fine
            played = 0
            while True:
                u, e = eps[i % len(eps)]
                if e.duration_s > room and played:
                    break
                note(w, u.label)
                room -= e.duration_s
                played += 1
                i += 1
                if i % len(eps) == 0:
                    note(w, "↻ the queue starts again")
                if room <= 0:
                    break
    return rows


# --------------------------------------------------------------------------- mappings


def _lane_source(lane: Lane) -> dict[str, Any]:
    return {
        "type": "lane",
        "lane": lane.key,
        "order": lane.order,
        "pattern": lane.pattern,
        "cadence": lane.cadence,
        "slot_minutes": lane.slot_minutes,
        "shows": list(lane.shows),
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
