"""Title-brand talk shows ("شاهد على العصر | <guest>", "بلا حدود - 2005").

A presenter's channel carries a few long-running talk shows. Their episodes say the
show's name in the title; the channel's playlists hold one guest's run, one year, the
whole show, or a topic cutting across shows. Playlist seeding alone scatters such a
show over topic playlists named after one episode; the brand pass rebuilds it.
"""

from __future__ import annotations

from datetime import date, timedelta

import pytest

from maktaba_pipeline.classify.series_detector import (
    DetectorConfig,
    PlaylistFacts,
    VideoFacts,
    detect,
    find_brands,
)
from maktaba_pipeline.text.arabic import fold

pytestmark = pytest.mark.unit

GUESTS = ("وليد جنبلاط", "بطرس غالي", "جيهان السادات")


def _approx(year: int) -> date:
    return date(year, 10, 15)  # "5 years ago": YouTube's listing only knows the year


def _witness() -> list[VideoFacts]:
    out = []
    for g_i, guest in enumerate(GUESTS):
        for i in range(1, 9):
            out.append(
                VideoFacts(
                    key=f"w{g_i}-{i}",
                    title=f"شاهد على العصر | {guest} مع أحمد منصور: الجزء {i} ({i})",
                    duration_s=2900 + i,
                    air_date=_approx(2020),
                )
            )
    # a guest with no playlist of his own: his run shares his name
    for i in range(1, 6):
        out.append(
            VideoFacts(
                key=f"wb-{i}",
                title=f"شاهد على العصر | الباجه جي يتحدث عن العراق ({i})",
                duration_s=2950 + i,
                air_date=_approx(2021),
            )
        )
    return out


def _limits() -> list[VideoFacts]:
    out = []
    for year in (2005, 2006):
        for i in range(1, 11):
            out.append(
                VideoFacts(
                    key=f"b{year}-{i}",
                    title=f"بلا حدود | حوار رقم {i} عن قضية {year}",
                    duration_s=2800 + i,
                    air_date=_approx(2022),
                )
            )
    return out


def _pl(pid: str, title: str, keys: list[str]) -> PlaylistFacts:
    return PlaylistFacts(pid, title, tuple((k, i) for i, k in enumerate(keys, 1)))


def _channel() -> tuple[list[VideoFacts], list[PlaylistFacts]]:
    w, b = _witness(), _limits()
    others = [
        VideoFacts(f"o{i}", f"محاضرة عامة رقم {i}", 1800 + i, _approx(2019)) for i in range(1, 6)
    ]
    playlists = [
        _pl("UMB", "شاهد على العصر كاملا من البداية", [v.key for v in w]),
        *(
            _pl(f"G{g}", f"شاهد على العصر | {guest}", [f"w{g}-{i}" for i in range(1, 9)])
            for g, guest in enumerate(GUESTS)
        ),
        _pl("BU", "بلا حدود من 2005 حتى 2006", [v.key for v in b]),
        _pl("Y5", "بلا حدود - 2005", [f"b2005-{i}" for i in range(1, 11)]),
        _pl("Y6", "حلقات بلا حدود التى تم بثها فى سنة - 2006", [f"b2006-{i}" for i in range(1, 11)]),
        # a topic list named after one episode, mixing both shows: what scatters them
        _pl(
            "MIX",
            "مجزرة صبرا وشاتيلا",
            [f"w0-{i}" for i in range(1, 9)] + [f"b2005-{i}" for i in range(1, 6)],
        ),
        _pl("TOP", "الشأن الفلسطيني", ["w1-1", "w1-2", "w2-3", "b2005-7", "b2006-2", "b2006-3"]),
    ]
    return w + b + others, playlists


def _cfg() -> DetectorConfig:
    return DetectorConfig(brand_min=20)


def test_find_brands_takes_the_shows_not_the_guests() -> None:
    videos, _ = _channel()
    brands = find_brands(videos, frozenset(fold("أحمد منصور").split()), _cfg())
    names = [name for _, name, _ in brands]
    assert names == ["شاهد على العصر", "بلا حدود"]
    assert len(brands[0][2]) == 29 and len(brands[1][2]) == 20


def test_scattered_brand_is_rebuilt_with_guest_and_year_seasons() -> None:
    videos, playlists = _channel()
    det = detect(videos, playlists, presenter_words=["أحمد منصور"], config=_cfg())
    by_name = {s.name: s for s in det.series}
    assert "مجزرة صبرا وشاتيلا" not in by_name
    witness = by_name["شاهد على العصر"]
    assert witness.brand and witness.kind == "ongoing"
    seasons = {se.name: se for se in witness.seasons}
    assert set(GUESTS) <= set(seasons)
    assert all(len(seasons[g].episodes) == 8 for g in GUESTS)
    # episodes in the umbrella's order, numbered from the titles
    assert seasons["وليد جنبلاط"].episodes == [f"w0-{i}" for i in range(1, 9)]
    # the guest without a playlist: one season, named as the titles write him
    baja = next(se for se in witness.seasons if se.name and "الباجه" in fold(se.name))
    assert sorted(baja.episodes) == [f"wb-{i}" for i in range(1, 6)]

    limits = by_name["بلا حدود"]
    years = {se.name: se for se in limits.seasons}
    assert set(years) == {"2005", "2006"}
    # the playlist's year beats the approximate upload dates
    assert years["2005"].first_air == date(2005, 1, 1)
    assert years["2006"].last_air == date(2006, 12, 31)
    # one place per episode
    keys = [k for s in det.series for se in s.seasons for k in se.episodes]
    assert len(keys) == len(set(keys))


def test_topic_playlist_cutting_across_shows_is_a_topic() -> None:
    videos, playlists = _channel()
    det = detect(videos, playlists, presenter_words=["أحمد منصور"], config=_cfg())
    topics = {t.name: t for t in det.topics}
    assert "الشأن الفلسطيني" in topics
    assert topics["الشأن الفلسطيني"].items[:2] == ["w1-1", "w1-2"]
    # a guest's own playlist and the umbrella are the show, not topics
    assert not {"شاهد على العصر | وليد جنبلاط", "شاهد على العصر كاملا من البداية"} & set(topics)


def test_brand_held_together_by_playlists_is_left_alone() -> None:
    start = date(2023, 1, 1)
    eps = [
        VideoFacts(f"k{i}", f"كنوز السيرة {i} | غزوة رقم {i}", 1500 + i,
                   start + timedelta(days=7 * i), date_exact=True)
        for i in range(1, 26)
    ]  # fmt: skip
    pl = _pl("PL", "سيرة الرسول ﷺ", [v.key for v in eps])
    det = detect(eps, [pl], presenter_words=["عثمان الخميس"], config=_cfg())
    assert len(det.series) == 1
    assert det.series[0].name == "كنوز السيرة"
    assert not det.series[0].brand
    assert len(det.series[0].seasons) == 1
