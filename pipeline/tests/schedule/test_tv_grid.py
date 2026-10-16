"""TV grid: shows own slots, ongoing shows run on through their years, a slot's
queue moves on when a series ends, strips and weekly shows, variety, fillers,
and the Quran channel (recitation non-stop)."""

from __future__ import annotations

from collections import Counter
from collections.abc import Sequence

import pytest

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
    simulate,
    slot_timeline,
    suggest_channels,
)

pytestmark = pytest.mark.unit


def ep(
    path: str, minutes: float, i: int, air: str | None = None, exact: bool = False
) -> EpisodeRef:
    return EpisodeRef(path=path, duration_s=minutes * 60, title=f"ح{i}", episode=i,
                      plex_rating_key=path, air_date=air, date_exact=exact)  # fmt: skip


def show(
    sid: str,
    name: str,
    *,
    n: int = 20,
    minutes: float = 50,
    season_no: int = 1,
    genre: str = "seerah",
    kind: str = "episodic",
    fmt: str = "program",
    folder: str = "Y:/a",
    presenter: str = "أحمد",
    first_air: str = "2010-01-01",
    brand: bool = False,
    dates: Sequence[str] | None = None,
    tags: dict[str, float] | None = None,
) -> SeasonCandidate:
    t = {f"topic:{genre}": 1.0, f"genre:{genre}": 1.0, f"format:{fmt}": 0.9}
    t.update(tags or {})
    return SeasonCandidate(
        series_id=sid, name=name, season=season_no, kind=kind, series_key=name, folder=folder,
        presenter=presenter, first_air=first_air, genre=genre, tags=t, brand=brand,
        episodes=tuple(
            ep(f"{folder}/{sid}/{i}.mp4", minutes, i, dates[i - 1] if dates else None, bool(dates))
            for i in range(1, n + 1)
        ),
    )  # fmt: skip


def clip(i: int, *, genre: str = "seerah", seconds: float = 120, promo_of: str = "",
         fmt: str | None = None, folder: str = "Y:/a") -> LooseVideo:  # fmt: skip
    tags = {f"topic:{genre}": 0.9}
    if fmt:
        tags[f"format:{fmt}"] = 0.9
    return LooseVideo(path=f"{folder}/clip{i}.mp4", duration_s=seconds + i, title=f"مقطع {i}",
                      folder=folder, presenter="أحمد", tags=tags, genre=genre, is_short=True,
                      promo_of=promo_of)  # fmt: skip


def plan(seasons: Sequence[SeasonCandidate], loose: Sequence[LooseVideo] = (),
         **cfg: object) -> PlanResult:  # fmt: skip
    base: dict[str, object] = {"theme_min_hours": 1.0, "presenter_channels": False}
    base.update(cfg)
    return suggest_channels(seasons, loose, PlannerConfig(**base))  # type: ignore[arg-type]


def channel(p: PlanResult, key: str) -> ChannelSuggestion:
    return next(c for c in p.channels if c.key == key)


def slot_at(slots: Sequence[Slot], minute: int, day: str) -> Slot:
    hits = [s for s in slots if s.on(day) and s.start <= minute < s.end]
    assert len(hits) == 1, (minute, day, hits)
    return hits[0]


def talk_years(name: str = "بلا حدود", years: range = range(1999, 2004), n: int = 30,
               **kw: object) -> list[SeasonCandidate]:  # fmt: skip
    return [show(f"bh{y}", name, season_no=y, n=n, kind="ongoing", fmt="interview",
                 genre="islamic_history", first_air=f"{y}-01-01", **kw)  # type: ignore[arg-type]
            for y in years]  # fmt: skip


def many_shows(k: int, *, fmt: str = "program", genre: str = "seerah", n: int = 20,
               minutes: float = 50, prefix: str = "w") -> list[SeasonCandidate]:  # fmt: skip
    return [show(f"{prefix}{i}", f"برنامج {prefix}{i}", fmt=fmt, genre=genre, n=n, minutes=minutes,
                 first_air=f"20{10 + i % 10:02d}-01-01") for i in range(k)]  # fmt: skip


# ----------------------------------------------------------------- show types and cadence


def test_an_ongoing_show_is_one_show_its_years_in_one_queue_back_to_back() -> None:
    p = plan(talk_years() + many_shows(40, prefix="x"), dawn_quran=False, fill_target=0.0)
    queues = [ln for ln in p.lanes.values() if any(u.series_key == "بلا حدود" for u in ln.units)]
    mix = channel(p, "mix")
    in_mix = [ln for ln in queues if ln.key.startswith("mix.")]
    assert len(in_mix) == 1  # one slot owns the whole show on a channel
    ln = in_mix[0]
    years = [u.season for u in ln.units if u.series_key == "بلا حدود"]
    assert years == [1999, 2000, 2001, 2002, 2003]
    keys = [u.series_key for u in ln.units]
    first = keys.index("بلا حدود")
    assert keys[first : first + 5] == ["بلا حدود"] * 5  # never interleaved with another show
    assert ln.cadence == "weekly"
    # the slot's timeline runs from year to year
    tl = slot_timeline(ln, mix.slots, weeks=200)
    shows = [r["show"] for r in tl]
    assert (
        shows.index("بلا حدود 1999") < shows.index("بلا حدود 2000") < shows.index("بلا حدود 2003")
    )


def test_cadence_from_exact_dates_daily_is_a_strip_weekly_stays_weekly() -> None:
    daily = [f"2020-03-{d:02d}" for d in range(1, 21)]
    weekly = [f"2020-{1 + (d * 7) // 28:02d}-{1 + (d * 7) % 28:02d}" for d in range(20)]
    seasons = [
        show("d", "درس يومي", dates=daily, fmt="interview"),  # dates beat the format
        show("w", "لقاء الأسبوع", dates=weekly, fmt="lecture"),
    ]
    p = plan(seasons, dawn_quran=False)
    by = {ln.shows[0]: ln for ln in p.lanes.values() if ln.shows}
    assert by["درس يومي"].cadence == "strip"
    assert by["لقاء الأسبوع"].cadence == "weekly"
    assert any("exact dates" in r for r in by["درس يومي"].reasons)


def test_cadence_defaults_lessons_strip_talk_shows_weekly() -> None:
    seasons = [
        show("l", "شرح الأربعين", fmt="lecture", n=200),
        *talk_years("شاهد على العصر", range(2001, 2003), brand=True),
        show("p", "برنامج", fmt="program", n=100),
    ]
    p = plan(seasons, dawn_quran=False, fill_target=0.0)  # cadence as found, no promotion
    cad = {ln.shows[0]: ln.cadence for ln in p.lanes.values() if ln.shows}
    assert cad["شرح الأربعين"] == "strip"
    assert cad["شاهد على العصر"] == "weekly"
    assert cad["برنامج"] == "weekly"


def test_slot_length_follows_the_episodes() -> None:
    seasons = [
        show("a", "قصير", minutes=27, fmt="lecture"),
        show("b", "ساعة", minutes=62, fmt="lecture"),  # 2 min over: keeps the 60 min slot
        show("c", "طويل", minutes=75, fmt="lecture"),
    ]
    p = plan(seasons, dawn_quran=False)
    mins = {ln.shows[0]: ln.slot_minutes for ln in p.lanes.values() if ln.shows}
    assert mins == {"قصير": 30, "ساعة": 60, "طويل": 90}


# ----------------------------------------------------------------- the grid


def test_a_strip_show_airs_at_the_same_time_every_weekday_weekends_get_weekly_shows() -> None:
    seasons = many_shows(6, fmt="lecture", prefix="s", n=100) + many_shows(120, prefix="w")
    p = plan(seasons, dawn_quran=False)
    mix = channel(p, "mix")
    strips = [s for s in mix.slots if s.lane and p.lanes[s.lane].pattern == "strip"]
    assert len({s.lane for s in strips}) == 6  # every lesson series has its own strip
    for s in strips:
        lane = p.lanes[s.lane or ""]
        assert lane.cadence == "strip"
        for day in ("sun", "mon", "tue", "wed", "thu"):  # same time every weekday
            assert slot_at(mix.slots, s.start, day).lane == lane.key
        for day in ("fri", "sat"):  # the weekend takes weekly shows at that time
            we = p.lanes[slot_at(mix.slots, s.start, day).lane or ""]
            assert we.pattern == "weekly"


def test_weekly_shows_air_once_a_week_and_prime_time_gets_the_strongest() -> None:
    seasons = many_shows(120, prefix="w") + many_shows(20, fmt="lecture", prefix="s", n=200)
    seasons.append(show("doc", "وثائقي كبير", fmt="documentary", n=60, genre="islamic_history",
                        tags={"format:program": 0.9}))  # fmt: skip
    p = plan(seasons, dawn_quran=False)
    mix = channel(p, "mix")
    airings: Counter[str] = Counter()
    for s in mix.slots:
        if s.lane and p.lanes[s.lane].pattern == "weekly":
            airings[s.lane] += len(s.days) if s.days else 7
    assert airings and max(airings.values()) == 1  # enough shows: no extra airings
    assert not any("extra airing" in w for w in mix.warnings)
    prime = {p.lanes[slot_at(mix.slots, 20 * 60, d).lane or ""].shows[0] for d in WEEKDAYS}
    assert len(prime) == 7  # a different show every night at 20:00
    assert "وثائقي كبير" in prime


def test_adjacent_slots_vary_the_genre() -> None:
    seasons = many_shows(30, prefix="a", genre="seerah") + many_shows(30, prefix="b", genre="fiqh")
    p = plan(seasons, dawn_quran=False)
    mix = channel(p, "mix")
    for day in WEEKDAYS:
        prog = [s for s in sorted(mix.slots, key=lambda s: s.start)
                if s.on(day) and s.kind == "program" and s.daypart == "prime"]  # fmt: skip
        genres = [p.lanes[s.lane or ""].units[0].genre for s in prog]
        assert all(a != b for a, b in zip(genres, genres[1:], strict=False)), (day, genres)


def test_when_a_series_ends_the_next_show_takes_its_slot() -> None:
    seasons = many_shows(200, prefix="w", n=8)  # more shows than slots: queues of several
    p = plan(seasons, dawn_quran=False)
    mix = channel(p, "mix")
    ln = next(p.lanes[s.lane] for s in mix.slots if s.lane and len(p.lanes[s.lane].shows) > 1)
    tl = slot_timeline(ln, mix.slots, weeks=30)
    names = [r["show"] for r in tl]
    assert names[0].startswith(ln.shows[0]) and names[1].startswith(ln.shows[1])
    assert tl[1]["from_week"] == tl[0]["to_week"] + 1  # the next week, same slot


def test_a_small_channel_gets_extra_airings_and_says_so() -> None:
    p = plan(many_shows(3, prefix="w"), dawn_quran=False)
    mix = channel(p, "mix")
    assert any("extra airing" in w for w in mix.warnings)
    p2 = plan(many_shows(3, prefix="w"), dawn_quran=False, extra_airings=False)
    mix2 = channel(p2, "mix")
    assert any(s.kind == "flex" for s in mix2.slots)
    assert not any("extra airing" in w for w in mix2.warnings)


def test_the_grid_covers_every_minute_once() -> None:
    p = plan(talk_years() + many_shows(25, prefix="w") + many_shows(5, fmt="lecture", prefix="s"))
    for c in p.channels:
        for day in WEEKDAYS:
            cov = sorted((s.start, s.end) for s in c.slots if s.on(day))
            assert cov[0][0] == 0 and cov[-1][1] == DAY_MINUTES, (c.key, day)
            assert all(a[1] == b[0] for a, b in zip(cov, cov[1:], strict=False)), (c.key, day)


# ----------------------------------------------------------------- fillers


def test_a_slot_pads_with_its_own_promos_first_then_its_topic() -> None:
    seasons = [show("k", "كنوز السيرة", minutes=50, n=40)] + many_shows(
        30, prefix="w", genre="fiqh"
    )
    loose = [clip(i, promo_of="Y:/a|كنوز السيرة") for i in range(3)]
    loose += [clip(10 + i, genre="seerah") for i in range(30)]
    p = plan(seasons, loose, dawn_quran=False)
    mix = channel(p, "mix")
    ln = next(p.lanes[s.lane] for s in mix.slots
              if s.lane and p.lanes[s.lane].shows[:1] == ("كنوز السيرة",))  # fmt: skip
    s = next(s for s in mix.slots if s.lane == ln.key)
    assert s.filler_pool == f"{ln.key}.promo"
    promo = p.filler_pools[s.filler_pool]
    assert all(i.promo_of for i in promo.items)
    nxt = p.filler_pools[promo.fallback or ""]
    assert all(i.genre == "seerah" for i in nxt.items)  # then this slot's own topic
    week = simulate(mix, p, weekday="mon", days=7)
    after = [a for a in week if a.kind == "filler" and a.lane == ln.key]
    assert after and "promo" not in after[0].title  # titles are "مقطع N"; check the path
    assert any(a.path and a.path.endswith(("clip0.mp4", "clip1.mp4", "clip2.mp4"))
               for a in after)  # fmt: skip


def test_fillers_between_episodes_of_a_long_block_and_no_recent_repeat() -> None:
    seasons = [show(f"s{i}", f"قصير {i}", minutes=17, n=100, fmt="lecture",
                    genre=("seerah", "fiqh", "tafsir")[i % 3]) for i in range(30)]  # fmt: skip
    loose = [clip(i, seconds=60 + 7 * i) for i in range(40)]
    p = plan(seasons, loose, dawn_quran=False, slot_round_minutes=60, min_slot_minutes=60,
             overrun_tolerance_minutes=0)  # fmt: skip
    mix = channel(p, "mix")
    day = simulate(mix, p, weekday="mon")
    lane = next(a.lane for a in day if a.kind == "program")
    kinds = [a.kind for a in day if a.lane == lane]
    # program, filler…, program: filler sits between the episodes, not only at the end
    first_prog = kinds.index("program")
    second_prog = kinds.index("program", first_prog + 1)
    assert "filler" in kinds[first_prog:second_prog]
    fills = [a.path for a in day if a.kind == "filler"]
    window = PlannerConfig().filler_recent_window
    for i in range(len(fills)):
        assert fills[i] not in fills[max(0, i - window) : i]


# ----------------------------------------------------------------- Quran channel


def reciter(sid: str, name: str, folder: str, n: int = 114, minutes: float = 15) -> SeasonCandidate:
    from dataclasses import replace

    s = show(sid, name, n=n, minutes=minutes, genre="quran_recitation", fmt="recitation",
             folder=folder, presenter="Quranic Recitations",
             tags={"topic:quran_recitation": 1.0, "format:program": 0.0})  # fmt: skip
    return replace(s, library=Path_leaf(folder))  # the reciter is his library


def Path_leaf(folder: str) -> str:  # noqa: N802 — a tiny helper
    return folder.rsplit("/", 1)[-1]


def quran_library() -> list[SeasonCandidate]:
    return [
        reciter("h", "المصحف المرتل", "Y:/الحصري"),
        reciter("m", "المصحف المرتل", "Y:/المنشاوي"),
        reciter("bm", "المصحف المجود", "Y:/عبد الباسط - مجود", minutes=25),
        reciter("bw", "المصحف", "Y:/عبد الباسط - مرتل - ورش"),
        reciter("g", "المصحف", "Y:/سعد الغامدي"),
    ]


def test_the_quran_channel_plays_recitation_non_stop_a_reciter_per_day() -> None:
    p = plan(quran_library() + many_shows(10, prefix="w"))
    q = channel(p, "quran")
    assert q.kind == "quran"
    assert all(s.kind == "quran" and s.lane for s in q.slots)  # no show grid, no gaps
    noon = {d: p.lanes[slot_at(q.slots, 12 * 60, d).lane or ""].name for d in WEEKDAYS}
    assert len(set(noon.values())) >= 3  # reciters rotate by day
    assert all(u.genre == "quran_recitation" for k in q.lanes for u in p.lanes[k].units)


def test_quran_time_rules_mujawwad_after_fajr_warsh_at_night() -> None:
    p = plan(quran_library())
    q = channel(p, "quran")
    for d in WEEKDAYS:
        assert p.lanes[slot_at(q.slots, 5 * 60, d).lane or ""].cadence == "mujawwad"
        assert p.lanes[slot_at(q.slots, 21 * 60 + 30, d).lane or ""].cadence == "warsh"


def test_quran_reciter_resumes_where_he_stopped() -> None:
    p = plan(quran_library())
    q = channel(p, "quran")
    week = simulate(q, p, weekday="mon", days=14)
    by_lane: dict[str, list[str]] = {}
    for a in week:
        if a.kind == "program" and a.lane and a.path:
            by_lane.setdefault(a.lane, []).append(a.path)
    for lane, paths in by_lane.items():
        expected = [e.path for _, e in p.lanes[lane].episodes()]
        assert paths == [expected[i % len(expected)] for i in range(len(paths))], lane


def test_quran_filler_is_only_short_recitation_or_dua_never_talk() -> None:
    loose = [
        clip(1, genre="quran_recitation"),
        clip(2, genre="dua_dhikr"),
        clip(3, genre="dua_dhikr", fmt="lecture"),  # a talk about dua: not on the Quran channel
        clip(4, genre="seerah"),
    ]
    p = plan(quran_library(), loose)
    q = channel(p, "quran")
    assert q.filler_pools == ("quran.filler",)
    pool = p.filler_pools["quran.filler"]
    assert {i.path for i in pool.items} == {"Y:/a/clip1.mp4", "Y:/a/clip2.mp4"}
    assert pool.fallback is None  # never the general (talk) filler


def test_quran_at_dawn_stays_a_slot_on_other_channels() -> None:
    p = plan(quran_library() + many_shows(30, prefix="w"))
    mix = channel(p, "mix")
    assert slot_at(mix.slots, 5 * 60, "mon").kind == "quran"
    assert slot_at(mix.slots, 20 * 60, "mon").kind == "program"


def test_friday_slots_go_around_the_khutbah_and_no_queue_is_lost() -> None:
    khutbah = show("k", "خطب الجمعة", n=40, minutes=45, fmt="khutbah",
                   tags={"format:khutbah": 0.9, "format:program": 0.0})  # fmt: skip
    p = plan([khutbah, *many_shows(120, prefix="w")], dawn_quran=False)
    mix = channel(p, "mix")
    noon = slot_at(mix.slots, 12 * 60 + 30, "fri")
    assert noon.kind == "khutbah"
    # every queue the grid made is on air somewhere: none hidden under the khutbah
    on_air = {s.lane for s in mix.slots}
    assert set(mix.lanes) <= on_air
    placed = {u.series_key for k in mix.lanes for u in p.lanes[k].units}
    assert all(f"برنامج w{i}" in placed for i in range(120))


def test_the_reciter_is_his_library_and_a_scholars_recitation_stays_a_programme() -> None:
    from dataclasses import replace

    scholar = [replace(x, library="العدوي") for x in many_shows(20, prefix="l", fmt="lecture")]
    stray = replace(reciter("st", "قراءة", "Y:/العدوي", n=10), library="العدوي")
    p = plan([*quran_library(), *scholar, stray])
    q = channel(p, "quran")
    names = {p.lanes[k].name for k in q.lanes}
    assert "الحصري" in names and "المنشاوي" in names
    assert not any("Quranic Recitations" in n for n in names)  # the YouTube channel name
    assert not any(u.series_id == "st" for k in q.lanes for u in p.lanes[k].units)
    # the scholar's recitation-tagged season is one of his programmes
    assert any(u.series_id == "st" for ln in p.lanes.values() if not ln.special for u in ln.units)


def test_a_channel_short_of_shows_moves_its_biggest_weekly_shows_to_strips() -> None:
    seasons = [*talk_years(n=140), *many_shows(5, prefix="w", n=8)]
    p = plan(seasons, dawn_quran=False)
    mix = channel(p, "mix")
    bh = next(p.lanes[k] for k in mix.lanes if p.lanes[k].shows[:1] == ("بلا حدود",))
    assert bh.pattern == "strip"  # a deep archive re-runs daily on a small channel
    assert any("daily strips" in w for w in mix.warnings)
    # a short series is not looped all week: the deep archive takes the extra airings
    small = [p.lanes[k] for k in mix.lanes if p.lanes[k].shows[:1] == ("برنامج w0",)]
    big_airs = sum(len(s.days) or 7 for s in mix.slots if s.lane == bh.key)
    for ln in small:
        airs = sum(len(s.days) or 7 for s in mix.slots if s.lane == ln.key)
        assert airs < big_airs, (ln.key, airs, big_airs)


def test_more_than_seven_reciters_all_air_each_week() -> None:
    extra = [reciter(f"r{i}", "المصحف", f"Y:/قارئ {i}") for i in range(8)]
    p = plan(quran_library() + extra)
    q = channel(p, "quran")
    on_air = {s.lane for s in q.slots}
    reciters = {k for k in p.lanes if k.startswith("quran.")}
    assert len(reciters) == 13
    assert reciters <= on_air  # every reciter lane airs somewhere in the week
    assert any("reciters a day" in r for r in q.reasons)


def test_a_show_joins_a_channel_whole_never_a_few_of_its_years() -> None:
    from dataclasses import replace

    years = talk_years(n=30)
    # two of five years are current affairs (off the mix): the show is still one show
    years = [replace(y, genre="current_affairs",
                     tags={**y.tags, "topic:current_affairs": 1.0, "genre:current_affairs": 1.0})
             if y.season in (2000, 2001) else y for y in years]  # fmt: skip
    p = plan([*years, *many_shows(60, prefix="w")], dawn_quran=False, fill_target=0.0)
    mix = channel(p, "mix")
    seasons = [u.season for k in mix.lanes for u in p.lanes[k].units if u.series_key == "بلا حدود"]
    assert seasons == [1999, 2000, 2001, 2002, 2003]
    # mostly current affairs: off the mix entirely, not with holes
    years2 = [replace(y, genre="current_affairs",
                      tags={**y.tags, "topic:current_affairs": 1.0, "genre:current_affairs": 1.0})
              if y.season != 1999 else y for y in years]  # fmt: skip
    p2 = plan([*years2, *many_shows(60, prefix="w")], dawn_quran=False, fill_target=0.0)
    mix2 = channel(p2, "mix")
    assert not any(u.series_key == "بلا حدود" for k in mix2.lanes for u in p2.lanes[k].units)


def test_more_mujawwad_reciters_than_days_all_air_in_the_morning_window() -> None:
    extra = [reciter(f"j{i}", "المصحف المجود", f"Y:/قارئ {i} - مجود", minutes=25)
             for i in range(8)]  # fmt: skip
    p = plan(quran_library() + extra)
    q = channel(p, "quran")
    mujawwad = {k for k in p.lanes if k.startswith("quran.") and p.lanes[k].cadence == "mujawwad"}
    assert len(mujawwad) == 9
    assert mujawwad <= {s.lane for s in q.slots}


def test_without_extra_airings_a_slot_is_never_stretched_past_its_show() -> None:
    p = plan(many_shows(3, prefix="w"), dawn_quran=False, extra_airings=False)
    mix = channel(p, "mix")
    for s in mix.slots:
        if s.kind == "program":
            assert s.end - s.start <= p.lanes[s.lane or ""].slot_minutes, s


def test_exclusive_ramadan_shows_still_get_the_ramadan_grid() -> None:
    ram = show("r", "خواطر رمضانية", n=30, tags={"occasion:ramadan": 1.0})
    p = plan([ram, *many_shows(6, prefix="w")], ramadan_exclusive=True, dawn_quran=False)
    mix = channel(p, "mix")
    assert any(p.lanes[s.lane or ""].ramadan for s in mix.ramadan_slots if s.lane)
    assert not any("خواطر رمضانية" in p.lanes[s.lane or ""].shows for s in mix.slots if s.lane)
