"""Channel planner on the shapes an Islamic YouTube archive has.

Fixtures mirror the real library: a presenter's produced Ramadan programmes
next to his older studio talks, highlight-clip playlists, short daily
programmes, shorts, khutbahs, recitations, a political talk channel that must
stay off the Islamic channels, and seasons the production-style pass has not
seen yet (tier unknown).
"""

from __future__ import annotations

from collections.abc import Sequence

import pytest

from maktaba_pipeline.channels.planner.schedule import parse_hhmm, slot_days
from maktaba_pipeline.schedule.channel_planner import (
    DAY_MINUTES,
    WEEKDAYS,
    ChannelSuggestion,
    EpisodeRef,
    LooseVideo,
    PlannerConfig,
    PlanResult,
    SeasonCandidate,
    Slot,
    dizquetv_time_slots,
    group_series,
    maktaba_channel_definition,
    simulate,
    suggest_channels,
)

pytestmark = pytest.mark.unit

PRESENTER = "الشيخ نبيل العوضي"


def season(
    sid: str,
    name: str,
    *,
    n: int = 30,
    minutes: float = 30.0,
    season_no: int = 1,
    genre: str = "seerah",
    tier: int | None = None,
    first_air: str = "2015-06-01",
    kind: str = "episodic",
    tags: dict[str, float] | None = None,
    presenter: str = PRESENTER,
    folder: str = "Y:/nabil",
) -> SeasonCandidate:
    t = {f"topic:{genre}": 1.0, f"genre:{genre}": 1.0, "format:program": 0.9}
    t.update(tags or {})
    return SeasonCandidate(
        series_id=sid,
        name=name,
        season=season_no,
        kind=kind,
        series_key=name,
        folder=folder,
        presenter=presenter,
        first_air=first_air,
        genre=genre,
        tier=tier,
        style={0: "static", 1: "talk", 2: "produced", 3: "cinematic"}.get(tier)
        if tier is not None
        else None,
        tags=t,
        episodes=tuple(
            EpisodeRef(
                path=f"{folder}/{sid}/{i:02d}.mp4",
                duration_s=minutes * 60,
                title=f"{name} {i}",
                season=season_no,
                episode=i,
                plex_rating_key=str(1000 + i),
            )
            for i in range(1, n + 1)
        ),
    )


def short(
    path: str, genre: str = "seerah", seconds: float = 90.0, vertical: bool = False
) -> LooseVideo:
    return LooseVideo(
        path=path,
        duration_s=seconds,
        title=path,
        folder="Y:/nabil",
        presenter=PRESENTER,
        tags={f"topic:{genre}": 0.9, f"genre:{genre}": 1.0},
        genre=genre,
        is_short=True,
        vertical=vertical,
    )


def library() -> list[SeasonCandidate]:
    return [
        # seerah: an old studio talk, a cinematic two-season show, a produced one
        season("s-old", "السيرة النبوية", first_air="2014-06-28", tier=1, minutes=34),
        season(
            "s-hab1",
            "الحبيب",
            first_air="2025-03-01",
            tier=3,
            minutes=35,
            tags={"occasion:ramadan": 0.95},
        ),  # fmt: skip
        season(
            "s-hab2", "الحبيب", season_no=2, first_air="2026-02-18", tier=3, minutes=43, n=10
        ),  # fmt: skip
        season("s-sir", "سيرة 3", first_air="2019-01-01", tier=2, minutes=25),
        # prophets: one tier unknown (style pass has not run on it)
        season(
            "p-1",
            "قصص الأنبياء",
            genre="prophets_stories",
            first_air="2024-03-11",
            minutes=21,
            tags={"occasion:ramadan": 0.99},
        ),  # fmt: skip
        season(
            "p-2",
            "قصص الأنبياء",
            genre="prophets_stories",
            season_no=2,
            first_air="2024-09-01",
            minutes=28,
        ),  # fmt: skip
        # raqaiq talk lectures
        season(
            "r-1",
            "التوبة",
            genre="raqaiq",
            tier=1,
            minutes=40,
            n=20,
            tags={"format:lecture": 0.9, "format:program": 0.0},
        ),  # fmt: skip
        # a short daily programme (6 min) → filler, and a highlight-clips series
        season("short-1", "برنامج الصفوة", genre="seerah", minutes=6, tier=2),
        season(
            "clips-1", "مقاطع مميزة من السيرة النبوية", kind="clips", genre="seerah", minutes=5, n=6
        ),  # fmt: skip
        # recitation + khutbah
        season(
            "q-1",
            "المصحف المرتل",
            genre="quran_recitation",
            tier=0,
            minutes=50,
            n=30,
            presenter="عبد الباسط",
            folder="Y:/basit",
            tags={"format:recitation": 0.95, "format:program": 0.0},
        ),  # fmt: skip
        season(
            "k-1",
            "خطب الجمعة",
            genre="raqaiq",
            minutes=45,
            n=40,
            tags={"format:khutbah": 0.9, "format:program": 0.0},
        ),  # fmt: skip
        # political talk show: stays off the Islamic channels and the Mix
        season(
            "ca-1",
            "ألش خانة",
            genre="current_affairs",
            minutes=30,
            n=40,
            presenter="ألش خانة",
            folder="Y:/alsh",
        ),  # fmt: skip
    ]


def plan(
    seasons: Sequence[SeasonCandidate] | None = None,
    loose: Sequence[LooseVideo] = (),
    **cfg: object,
) -> PlanResult:
    config = PlannerConfig(**cfg)  # type: ignore[arg-type]
    return suggest_channels(library() if seasons is None else seasons, loose, config)


def channel(p: PlanResult, key: str) -> ChannelSuggestion:
    return next(c for c in p.channels if c.key == key)


def lane_at(p: PlanResult, ch: ChannelSuggestion, minute: int, day: str = "mon") -> str | None:
    s = slot_at(ch.slots, minute, day)
    return s.lane


def slot_at(slots: Sequence[Slot], minute: int, day: str = "mon") -> Slot:
    hits = [s for s in slots if s.on(day) and s.start <= minute < s.end]
    assert len(hits) == 1, (minute, day, hits)
    return hits[0]


# ----------------------------------------------------------------- channel suggestions


def test_themed_channels_need_enough_hours() -> None:
    p = plan()
    keys = {c.key for c in p.channels}
    assert {"seerah", "prophets", "quran", "mix"} <= keys
    # raqaiq has 13.3 h of lectures + the khutbahs → a channel; history has nothing
    assert "history" not in keys
    p2 = plan(theme_min_hours=60.0)
    assert "seerah" not in {c.key for c in p2.channels}
    assert any(w[0] == "theme seerah" and "< 60 h" in w[1] for w in p2.unplaced)


def test_every_suggestion_has_reasons() -> None:
    p = plan()
    for c in p.channels:
        assert c.reasons, c.key
        for s in c.slots:
            assert s.reasons, (c.key, s)
    assert all(ln.reasons for ln in p.lanes.values())


def test_channel_numbers_are_stable_per_theme() -> None:
    p = plan()
    assert channel(p, "quran").number == 101
    assert channel(p, "seerah").number == 103
    assert channel(p, "mix").number == 100
    assert [c.number for c in p.channels] == sorted(c.number for c in p.channels)


def test_current_affairs_stays_off_mix_and_themes() -> None:
    p = plan()
    for c in p.channels:
        if c.kind == "presenter":
            continue
        for k in c.lanes:
            assert all(u.genre != "current_affairs" for u in p.lanes[k].units), (c.key, k)


def test_presenter_channel_threshold() -> None:
    p = plan(presenter_min_hours=5.0, presenter_min_genres=2)
    pres = [c for c in p.channels if c.kind == "presenter"]
    assert [c.name for c in pres] == [PRESENTER]
    assert pres[0].number == 150
    p2 = plan(presenter_min_hours=5000.0)
    assert not [c for c in p2.channels if c.kind == "presenter"]


# ----------------------------------------------------------------- tiers and order


def test_lanes_never_mix_tiers() -> None:
    p = plan()
    for ln in p.lanes.values():
        if ln.special:
            continue
        assert len({u.tier_group for u in ln.units}) == 1, ln.key


def test_produced_and_cinematic_take_prime_talk_takes_day_and_late() -> None:
    ch = channel(plan(), "seerah")
    lanes = plan().lanes
    prime = lanes[lane_at(plan(), ch, 21 * 60) or ""]
    evening = lanes[lane_at(plan(), ch, 18 * 60) or ""]
    morning = lanes[lane_at(plan(), ch, 9 * 60) or ""]
    late = lanes[lane_at(plan(), ch, 1 * 60) or ""]
    assert prime.tier_group == "cinematic"
    assert evening.tier_group in ("produced", "cinematic")
    assert morning.tier_group == "talk"
    assert late.tier_group == "talk"


def test_unknown_tier_is_its_own_group() -> None:
    seasons = [
        season("a", "A", tier=1, first_air="2010-01-01"),
        season("b", "B", tier=None, first_air="2011-01-01"),
        season("c", "C", tier=None, first_air="2012-01-01"),
    ]
    p = plan(seasons, dawn_quran=False)
    groups = {ln.tier_group for ln in p.lanes.values()}
    assert groups == {"talk", "unknown"}
    unknown = [ln for ln in p.lanes.values() if ln.tier_group == "unknown"]
    assert {u.series_id for ln in unknown for u in ln.units} == {"b", "c"}


def test_seasons_and_episodes_stay_in_order() -> None:
    p = plan()
    hab = [
        ln
        for ln in p.lanes.values()
        if not ln.ramadan and any(u.series_id == "s-hab1" for u in ln.units)
    ]
    assert hab
    for ln in hab:
        ids = [u.series_id for u in ln.units]
        assert ids.index("s-hab1") < ids.index("s-hab2")  # whole series kept, S1 before S2
        for u in ln.units:
            eps = [e.episode for e in u.episodes]
            assert eps == sorted(eps)  # type: ignore[type-var]


def test_series_play_in_first_air_order_within_a_lane() -> None:
    seasons = [
        season("late", "Z late", tier=1, first_air="2020-01-01"),
        season("early", "A early", tier=1, first_air="2001-01-01"),
        season("mid", "M mid", tier=1, first_air="2010-01-01"),
    ]
    p = plan(seasons, dawn_quran=False, max_lanes_per_daypart=1)
    # one talk lane over several dayparts is split into parts of whole series;
    # inside each part series stay in first-air order
    for ln in p.lanes.values():
        airs = [u.first_air for u in ln.units]
        assert airs == sorted(airs)


def test_group_series_orders_seasons() -> None:
    lib = library()
    groups = group_series(lib)
    hab = next(g for g in groups if g.name == "الحبيب")
    assert [s.season for s in hab.seasons] == [1, 2]
    firsts = [g.first_air for g in groups]
    assert firsts == sorted(firsts)


# ----------------------------------------------------------------- grid shape


def test_grid_covers_every_minute_once_on_every_day() -> None:
    p = plan()
    for c in p.channels:
        for slots in (c.slots, c.ramadan_slots):
            if not slots:
                continue
            for day in WEEKDAYS:
                covered = sorted((s.start, s.end) for s in slots if s.on(day))
                assert covered[0][0] == 0 and covered[-1][1] == DAY_MINUTES, (c.key, day)
                for (a0, a1), (b0, b1) in zip(covered, covered[1:], strict=False):
                    assert a1 == b0, (c.key, day, (a0, a1), (b0, b1))


def test_quran_at_dawn_from_the_library_recitation_lane() -> None:
    p = plan(loose=[short("Y:/nabil/dua.mp4", "dua_dhikr")])
    ch = channel(p, "seerah")
    dawn = slot_at(ch.slots, 5 * 60)
    assert dawn.kind == "quran"
    assert dawn.lane == "library.quran"
    assert dawn.filler_pool == "library.dawn"
    assert any("Quran recitation lane" in r for r in ch.reasons)
    # the recitation season is not in any regular lane
    for ln in p.lanes.values():
        if ln.special != "quran" and not ln.key.startswith("quran."):
            assert all(u.series_id != "q-1" for u in ln.units)


def test_no_quran_content_falls_back_with_a_reason() -> None:
    seasons = [s for s in library() if s.series_id != "q-1"]
    p = plan(seasons)
    ch = channel(p, "seerah")
    assert slot_at(ch.slots, 5 * 60).kind == "program"
    assert any("no Quran recitation" in r for r in ch.reasons)


def test_quran_channel_plays_recitation_all_day() -> None:
    p = plan(theme_min_hours=5.0)
    q = channel(p, "quran")
    for m in range(0, DAY_MINUTES, 60):
        lane = p.lanes[lane_at(p, q, m) or ""]
        assert all(u.genre == "quran_recitation" for u in lane.units)


def test_friday_khutbah_slot_only_on_friday() -> None:
    p = plan()
    mix = channel(p, "mix")
    fri = slot_at(mix.slots, 12 * 60 + 30, "fri")
    assert fri.kind == "khutbah" and fri.days == ("fri",)
    assert fri.lane == "library.khutbah"
    mon = slot_at(mix.slots, 12 * 60 + 30, "mon")
    assert mon.kind == "program" and mon.lane != "library.khutbah"
    # khutbahs left the regular lanes
    for k in mix.lanes:
        if k != "library.khutbah":
            assert all(u.series_id != "k-1" for u in p.lanes[k].units)


def test_khutbah_only_where_it_belongs() -> None:
    p = plan()
    seerah = channel(p, "seerah")
    assert all(s.kind != "khutbah" for s in seerah.slots)


def test_ramadan_seasons_flagged_and_moved_after_iftar() -> None:
    p = plan()
    ch = channel(p, "seerah")
    assert ch.ramadan_slots
    iftar = slot_at(ch.ramadan_slots, 20 * 60)
    lane = p.lanes[iftar.lane or ""]
    assert lane.ramadan
    assert all(u.ramadan for u in lane.units)
    assert {u.series_id for u in lane.units} == {"s-hab1"}
    assert any("Ramadan" in r for r in ch.reasons)
    # no Ramadan seasons on history-less channels → no Ramadan grid
    raq = channel(p, "raqaiq")
    assert not raq.ramadan_slots


def test_ramadan_exclusive_keeps_them_off_the_regular_grid() -> None:
    p = plan(ramadan_exclusive=True)
    ch = channel(p, "seerah")
    regular = {s.lane for s in ch.slots}
    for k in regular:
        if k and not p.lanes[k].ramadan:
            assert all(u.series_id != "s-hab1" for u in p.lanes[k].units)


def test_slot_longer_than_episode_or_warned() -> None:
    seasons = [season("long", "Long", tier=1, minutes=200, n=5)]
    p = plan(seasons, dawn_quran=False, theme_min_hours=1.0)
    ch = channel(p, "seerah")
    reasons = [r for s in ch.slots for r in s.reasons]
    assert any("overruns" in r for r in reasons)


# ----------------------------------------------------------------- filler


def test_short_series_and_clips_become_filler_not_lanes() -> None:
    p = plan()
    in_lanes = {u.series_id for ln in p.lanes.values() for u in ln.units}
    assert "short-1" not in in_lanes and "clips-1" not in in_lanes
    pool = p.filler_pools["seerah.topic"]
    sources = {i.source for i in pool.items}
    assert {"short_series", "clips"} <= sources


def test_filler_stays_on_topic_with_video_tags() -> None:
    loose = [
        short("Y:/nabil/s1.mp4", "seerah"),
        short("Y:/nabil/p1.mp4", "prophets_stories"),
        short("Y:/nabil/ca.mp4", "current_affairs"),
        short("Y:/nabil/v.mp4", "seerah", vertical=True),
    ]
    p = plan(loose=loose)
    seerah = {i.path for i in p.filler_pools["seerah.topic"].items}
    prophets = {i.path for i in p.filler_pools["prophets.topic"].items}
    assert "Y:/nabil/s1.mp4" in seerah and "Y:/nabil/p1.mp4" not in seerah
    assert "Y:/nabil/p1.mp4" in prophets
    assert all("ca.mp4" not in i.path for pool in p.filler_pools.values() for i in pool.items)
    # horizontal clips come before vertical shorts
    items = list(p.filler_pools["seerah.topic"].items)
    assert items.index(next(i for i in items if i.vertical)) > items.index(
        next(i for i in items if i.path == "Y:/nabil/s1.mp4")
    )
    p2 = plan(loose=loose, include_vertical_shorts=False)
    assert all(not i.vertical for pool in p2.filler_pools.values() for i in pool.items)


def test_simulated_day_pads_every_slot_to_its_boundary() -> None:
    loose = [short(f"Y:/nabil/c{i}.mp4", "seerah", seconds=60 + 37 * i) for i in range(40)]
    p = plan(loose=loose)
    ch = channel(p, "seerah")
    day = simulate(ch, p, weekday="fri", days=1)
    assert day[0].start_s == 0
    assert abs(day[-1].end_s - DAY_MINUTES * 60) < 1
    for a, b in zip(day, day[1:], strict=False):
        assert abs(a.end_s - b.start_s) < 1e-6  # contiguous
    slots = sorted((s for s in ch.slots if s.on("fri")), key=lambda s: s.start)
    for a in day:
        if a.kind != "program":
            continue
        s = slot_at(slots, int(a.start_s // 60), "fri")
        # an episode never crosses its slot's end (none is longer than a slot here)
        assert a.end_s <= s.end * 60 + 1e-6, a
    progs = sum(a.end_s - a.start_s for a in day if a.kind == "program")
    assert progs / (DAY_MINUTES * 60) > 0.8


def test_simulation_keeps_episode_order_across_slots() -> None:
    p = plan()
    ch = channel(p, "seerah")
    week = simulate(ch, p, weekday="mon", days=7)
    by_lane: dict[str, list[str]] = {}
    for a in week:
        if a.kind == "program" and a.lane and a.path:
            by_lane.setdefault(a.lane, []).append(a.path)
    for lane, paths in by_lane.items():
        expected = [e.path for _, e in p.lanes[lane].episodes()]
        n = len(expected)
        assert paths == [expected[i % n] for i in range(len(paths))], lane


# ----------------------------------------------------------------- loose programmes


def test_long_loose_videos_group_into_a_programme_lane() -> None:
    loose = [
        LooseVideo(
            path=f"Y:/anti/{i}.mp4", duration_s=40 * 60, title=f"رد {i}", folder="Y:/anti",
            presenter="مكافح الشبهات", genre="shubuhat",
            tags={"topic:shubuhat": 0.9, "genre:shubuhat": 1.0},
            air_date=f"2020-01-{30 - i:02d}",
        )
        for i in range(20)
    ] + [
        LooseVideo(path="Y:/anti/g.mp4", duration_s=3000, folder="Y:/anti", genre="general")
    ]  # fmt: skip
    p = plan(library(), loose)
    aq = channel(p, "aqeedah")
    units = [u for k in aq.lanes for u in p.lanes[k].units if u.kind == "loose"]
    assert len(units) == 1
    dates = [e.air_date for e in units[0].episodes]
    assert dates == sorted(dates)  # type: ignore[type-var]
    assert all("g.mp4" not in e.path for e in units[0].episodes)  # general loose skipped


def test_mix_rotates_genres_by_whole_series() -> None:
    seasons = [
        season("a1", "A", genre="seerah", tier=1, first_air="2001-01-01"),
        season("a2", "A", genre="seerah", tier=1, season_no=2, first_air="2002-01-01"),
        season("b1", "B", genre="seerah", tier=1, first_air="2003-01-01"),
        season("c1", "C", genre="raqaiq", tier=1, first_air="2004-01-01"),
    ]
    p = plan(seasons, dawn_quran=False, theme_min_hours=1000.0, presenter_channels=False,
             max_lanes_per_daypart=1, dayparts=PlannerConfig().dayparts[:1])  # fmt: skip
    mix = channel(p, "mix")
    lane = p.lanes[mix.lanes[0]]
    assert [u.series_id for u in lane.units] == ["a1", "a2", "c1", "b1"]


# ----------------------------------------------------------------- mappings


def test_maktaba_definition_matches_the_slot_0081_schema() -> None:
    p = plan()
    for ch in p.channels:
        body = maktaba_channel_definition(ch, p)
        assert body["mode"] == "schedule"
        assert body["number"] == ch.number and body["name"]
        slots = body["mode_config"]["slots"]
        assert slots  # ValidateModeConfig: non-empty slots array …
        for s in slots:  # … each with start/end time strings
            assert isinstance(s["start"], str) and isinstance(s["end"], str)
            assert s["source"]["type"] in ("lane", "filler")
        # read back with the 27.2 SchedulePlanner's own helpers: one slot per minute per day
        for day in range(7):
            for m in range(0, DAY_MINUTES, 15):
                hits = [
                    s
                    for s in slots
                    if day in slot_days(s)
                    and parse_hhmm(s["start"]) <= m < (parse_hhmm(s["end"]) or DAY_MINUTES)
                ]
                assert len(hits) == 1, (ch.key, day, m)
        if ch.ramadan_slots:
            assert body["mode_config"]["ramadan"]["activate"] == {"hijri_month": 9}


def test_dizquetv_daily_schedule_when_no_day_specific_slot() -> None:
    p = plan()
    ch = channel(p, "seerah")
    dz = dizquetv_time_slots(ch, p)
    assert dz["period"] == 86_400_000
    times = [s["time"] for s in dz["slots"]]
    assert times == sorted(times) and len(set(times)) == len(times)
    shows = [s["showId"] for s in dz["slots"]]
    for a, b in zip(shows, shows[1:], strict=False):
        assert a != b  # adjacent slots of one lane are merged
    assert shows[0] != shows[-1]  # …also across midnight
    assert all(s["order"] == "next" for s in dz["slots"] if s["showId"] != "flex.")
    assert {"lateness", "maxDays", "pad", "flexPreference"} <= set(dz)


def test_dizquetv_weekly_schedule_puts_khutbah_on_friday() -> None:
    p = plan()
    mix = channel(p, "mix")
    dz = dizquetv_time_slots(mix, p)
    day = 86_400_000
    assert dz["period"] == 7 * day
    kh = [s for s in dz["slots"] if s["showId"] == "lane:library.khutbah"]
    # dizqueTV's weekly period starts on Thursday: Friday is day 1
    assert [s["time"] for s in kh] == [1 * day + 12 * 3_600_000]
