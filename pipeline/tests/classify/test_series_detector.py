"""Story 26.3 — series detection on YouTube-archive shaped libraries.

The cases mirror real channel layouts: Ramadan programs as yearly seasons
in separate playlists, two different shows sharing one name, promos and
shorts mixed into playlists, and episodes that never made it into a
playlist.
"""

from __future__ import annotations

from datetime import date, timedelta

import pytest

from maktaba_pipeline.classify.series_detector import (
    DetectorConfig,
    PlaylistFacts,
    VideoFacts,
    detect,
    is_short,
    series_key,
    title_season,
)

pytestmark = pytest.mark.unit

STOP = DetectorConfig().generic_words | frozenset({"نبيل", "العوضي"})


def _season(prefix: str, n: int, start: date, minutes: float, title: str) -> list[VideoFacts]:
    return [
        VideoFacts(
            key=f"{prefix}{i}",
            title=title.format(i=i),
            duration_s=minutes * 60 + i,
            air_date=start + timedelta(days=i - 1),
            date_exact=True,
        )
        for i in range(1, n + 1)
    ]


def _pl(pid: str, title: str, vids: list[VideoFacts]) -> PlaylistFacts:
    return PlaylistFacts(pid, title, tuple((v.key, i) for i, v in enumerate(vids, 1)))


def test_series_key_strips_presenter_markers_and_season() -> None:
    assert series_key("قصص الأنبياء رمضان 2024", STOP) == ("قصص الانبياء", None, 2024)
    assert series_key("قصص الأنبياء (الموسم الثاني)", STOP) == ("قصص الانبياء", 2, None)
    assert series_key("برنامج قصة وآية 3", STOP)[:2] == ("قصه وايه", 3)
    assert series_key("مشاهد4 نبيل العوضي", STOP)[:2] == ("مشاهد", 4)


def test_yearly_playlists_become_seasons_of_one_series() -> None:
    s1 = _season("a", 30, date(2024, 3, 11), 21, "قصة رقم {i}")
    s2 = _season("b", 30, date(2025, 3, 1), 26, "قصة أخرى {i}")
    det = detect(
        s1 + s2,
        [_pl("PL1", "قصص الأنبياء رمضان 2024", s1), _pl("PL2", "قصص الأنبياء (الموسم الثاني)", s2)],
        presenter_words=["نبيل العوضي"],
    )
    assert len(det.series) == 1
    ser = det.series[0]
    assert ser.name == "قصص الأنبياء"
    assert [(se.number, len(se.episodes)) for se in ser.seasons] == [(1, 30), (2, 30)]


def test_same_name_different_show_is_split_by_duration_band() -> None:
    clips = _season("c", 12, date(2023, 3, 25), 4, "سلسلة الحبيب حلقة {i}")
    drama = _season("d", 30, date(2025, 3, 1), 35, "الحبيب الحلقة {i}")
    det = detect(
        clips + drama,
        [_pl("PL1", "#سلسلة_الحبيب | في #رمضان", clips), _pl("PL2", "حلقات الحبيب", drama)],
    )
    sizes = sorted(se.episodes.__len__() for ser in det.series for se in ser.seasons)
    assert len(det.series) == 2
    assert sizes == [12, 30]


def test_shorts_and_promos_never_become_episodes() -> None:
    eps = _season("e", 30, date(2025, 3, 1), 35, "الحبيب | الحلقة {i}")
    promos = [
        VideoFacts(f"p{i}", f"الحبيب | برومو {i} | حصريا", 70, date(2025, 3, i), True)
        for i in range(1, 6)
    ]
    vertical = VideoFacts("v1", "الحبيب | مقطع", 300, date(2025, 3, 3), True, is_short=True)
    det = detect(eps + promos + [vertical], [_pl("PL1", "الحبيب", eps + promos)])
    assert set(det.shorts) == {p.key for p in promos} | {"v1"}
    assert sum(len(se.episodes) for se in det.series[0].seasons) == 30
    assert is_short(VideoFacts("x", "انظر #Shorts", 400), DetectorConfig())


def test_unlisted_twin_episode_is_attached_and_ordered() -> None:
    parts = ["الأول", "الثاني", "الثالث", "الرابع", "الخامس", "السادس"]
    vids = [
        VideoFacts(f"m{i}", f"قصة موسى عليه السلام الجزء {w}", 1500 + i, date(2025, 3, i + 1), True)
        for i, w in enumerate(parts)
    ]
    listed = [v for v in vids if v.key != "m4"]  # part 5 never made it into the playlist
    other = _season("o", 5, date(2019, 5, 1), 25, "محاضرة عامة {i}")
    det = detect(vids + other, [_pl("PL1", "قصص الأنبياء", listed)])
    se = det.series[0].seasons[0]
    assert "m4" in se.attached
    assert se.episodes == [v.key for v in vids]  # episode number from the ordinal word
    assert set(det.unassigned) == {v.key for v in other}


def test_wrong_year_or_length_is_not_attached() -> None:
    s = _season("a", 10, date(2024, 3, 11), 21, "قصص الأنبياء | قصة {i}")
    old = VideoFacts("old", "قصص الأنبياء | قصة قديمة", 21 * 60, date(2016, 5, 1), True)
    long_ = VideoFacts("long", "قصص الأنبياء | محاضرة كاملة", 95 * 60, date(2024, 3, 15), True)
    det = detect(s + [old, long_], [_pl("PL1", "قصص الأنبياء", s)])
    assert set(det.unassigned) == {"old", "long"}


def test_clip_playlists_are_kept_apart() -> None:
    s = _season("a", 10, date(2014, 7, 1), 27, "يا الله الحلقة {i}")
    clips = _season("c", 8, date(2014, 7, 2), 6, "مقطع من يا الله {i}")
    det = detect(
        s + clips,
        [_pl("PL1", "برنامج يا الله 2014", s), _pl("PL2", "مقاطع مميزة من برنامج يا الله", clips)],
    )
    kinds = {ser.kind: sum(len(se.episodes) for se in ser.seasons) for ser in det.series}
    assert kinds == {"episodic": 10, "clips": 8}


def test_late_numbered_episode_joins_but_rerun_and_earlier_show_do_not() -> None:
    s = _season("a", 25, date(2019, 12, 1), 12, "قصة {i} (الدنيا قصص وعبر) #{i}")
    late = VideoFacts("late", "قصة جديدة (الدنيا قصص وعبر) #26", 740, date(2020, 2, 20), True)
    last = VideoFacts("last", "الدنيا قصص وعبر | الحلقة الأخيرة", 730, date(2020, 3, 10), True)
    rerun = VideoFacts("rerun", "إعادة (الدنيا قصص وعبر) #3", 730, date(2020, 3, 1), True)
    early = VideoFacts("early", "الدنيا قصص وعبر #40", 730, date(2016, 1, 1), True)
    # a year on is the next season, not this one
    after = VideoFacts("after", "الدنيا قصص وعبر | الحلقة 41", 730, date(2021, 1, 25), True)
    det = detect(s + [late, last, rerun, early, after], [_pl("PL1", "الدنيا قصص وعبر", s)])
    se = det.series[0].seasons[0]
    assert se.episodes[-2:] == ["late", "last"]
    assert set(det.unassigned) == {"rerun", "early"}
    assert [(x.number, x.episodes) for x in det.series[0].seasons][1:] == [(2, ["after"])]


def test_recap_compilation_is_an_extra_not_an_episode() -> None:
    eps = _season("e", 30, date(2025, 3, 1), 43, "الحبيب | الحلقة {i}")
    recap = VideoFacts("r", "الحبيب | ومضات الموسم الأول", 45 * 60, date(2025, 4, 2), True)
    numbered = VideoFacts("n31", "الحبيب | الحلقة 31 | ومضات", 43 * 60, date(2025, 3, 31), True)
    det = detect(eps + [recap, numbered], [_pl("PL1", "الحبيب", eps)])
    se = det.series[0].seasons[0]
    assert "r" in se.extras and "r" not in se.episodes
    assert se.episodes[-1] == "n31"  # an episode whose title just mentions "ومضات"


def test_second_upload_of_an_episode_is_a_duplicate_and_dates_order_mixed_playlists() -> None:
    names = ["الجاهلية", "للبيت رب", "ولد الهدى", "يتيم مكة", "الزفاف", "هدم الكعبة", "اقرأ"]
    weekly = [
        VideoFacts(f"w{i}", f"نبيل العوضي | الحبيب الموسم الاول | {n}", 2100 + i,
                   date(2025, 4, 1) + timedelta(days=7 * i), True)
        for i, n in enumerate(names)
    ]  # fmt: skip
    tv = VideoFacts("tv7", "الحبيب | الحلقة 7 | اقرأ", 2110, date(2025, 3, 7), True)
    a = [weekly[i] for i in (0, 2, 4)]
    b = [weekly[i] for i in (1, 3)]
    det = detect(
        weekly + [tv],
        [_pl("PL1", "حلقات الحبيب", a), _pl("PL2", "الحبيب", b)],
        presenter_words=["نبيل العوضي"],
    )
    se = det.series[0].seasons[0]
    assert se.duplicates == ["tv7"]
    assert se.episodes == [v.key for v in weekly]


def test_episode_aired_after_the_last_season_opens_the_next_season() -> None:
    s1 = _season("a", 29, date(2025, 3, 1), 35, "الحبيب الموسم الاول | قصة {i}")
    finale = VideoFacts(
        "fin", "الحبيب | بل الرفيق الأعلى | الحلقة الأخيرة", 2580, date(2026, 1, 25), True
    )
    clip = VideoFacts("clip", "تأمل رحمة الحبيب", 2100, date(2026, 2, 1), True)  # no marker, alone
    det = detect(s1 + [finale, clip], [_pl("PL1", "الحبيب", s1)])
    ser = det.series[0]
    assert [(se.number, se.episodes) for se in ser.seasons][1:] == [(2, ["fin"])]
    assert "clip" in det.unassigned


def test_title_season_number_wins_over_late_date_and_vetoes_other_seasons() -> None:
    s3 = _season("a", 20, date(2013, 8, 1), 25, "مشاهد3 / الحلقة {i} / نبيل العوضي")
    s4 = _season("b", 30, date(2014, 7, 1), 25, "مشاهد4 / الحلقة {i}")
    late = [
        VideoFacts(f"l{i}", f"مشاهد3 / الحلقة {20 + i} / نبيل العوضي", 1510, date(2014, 6, i), True)
        for i in range(1, 6)
    ]  # weeks after the season-3 playlist ends, still season 3
    s1 = VideoFacts("s1", "مشاهد1 حلقة 20 لقاء", 1500, date(2013, 8, 15), True)
    det = detect(
        s3 + s4 + late + [s1],
        [_pl("PL3", "مشاهد3", s3), _pl("PL4", "مشاهد4", s4)],
        presenter_words=["نبيل العوضي"],
    )
    seasons = {se.number: se for se in det.series[0].seasons}
    assert sorted(seasons) == [3, 4]
    assert seasons[3].episodes[-5:] == [v.key for v in late]
    assert "s1" not in seasons[3].episodes and "s1" not in seasons[4].episodes
    assert title_season("مشاهد1 حلقة 20 لقاء", "مشاهد") == 1
    assert title_season("قصة وآية 3 | يوسف", "قصه وايه") == 3


def _weekly(prefix: str, n: int, start: date, minutes: float, title: str) -> list[VideoFacts]:
    return [
        VideoFacts(
            key=f"{prefix}{i}",
            title=title.format(i=i),
            duration_s=minutes * 60 + i % 7,
            air_date=start + timedelta(weeks=i - 1),
            date_exact=True,
        )
        for i in range(1, n + 1)
    ]


def test_weekly_show_over_years_is_ongoing_with_a_season_per_year() -> None:
    eps = _weekly("w", 120, date(2015, 1, 2), 12, "على ما تفرج {i}")
    late = VideoFacts("late", "على ما تفرج 140", 12 * 60, date(2018, 3, 2), True)
    det = detect([*eps, late], [_pl("PLw", "على ما تفرج", eps)])
    (ser,) = det.series
    assert ser.kind == "ongoing"
    assert [se.number for se in ser.seasons] == [2015, 2016, 2017, 2018]
    assert ser.seasons[0].episodes[:2] == ["w1", "w2"]  # air order inside a year
    # an episode after the playlist ends is the next episode, not a new season
    assert "late" in ser.seasons[-1].episodes
    assert ser.episode_count == 121


def test_yearly_ramadan_program_keeps_its_seasons() -> None:
    s1 = _season("a", 30, date(2022, 4, 2), 25, "قصة وآية {i}")
    s2 = _season("b", 30, date(2023, 3, 23), 25, "قصة وآية 2 الحلقة {i}")
    s3 = _season("c", 30, date(2024, 3, 11), 25, "قصة وآية 3 الحلقة {i}")
    det = detect(
        [*s1, *s2, *s3],
        [_pl("P1", "قصة وآية", s1), _pl("P2", "قصة وآية 2", s2), _pl("P3", "قصة وآية 3", s3)],
    )
    (ser,) = [s for s in det.series if s.episode_count == 90]
    assert ser.kind == "episodic"  # bursts once a year: seasons, not an ongoing show
    assert [se.number for se in ser.seasons] == [1, 2, 3]


def test_closed_numbered_run_released_weekly_is_not_ongoing() -> None:
    # السيرة النبوية: episodes 1..30, one a week for most of a year, then it ends
    eps = _weekly("s", 30, date(2014, 11, 7), 40, "السيرة النبوية الحلقة {i}")
    det = detect(eps, [_pl("PLs", "السيرة النبوية", eps)])
    (ser,) = det.series
    assert ser.kind == "episodic"
    assert [se.number for se in ser.seasons] == [1]
