"""A non-stop channel: one presenter's series, an episode every hour, its own filler."""

from __future__ import annotations

import pytest

from maktaba_pipeline.schedule.channel_planner import (
    EpisodeRef,
    LooseVideo,
    PlannerConfig,
    SeasonCandidate,
    ThemeSpec,
    suggest_channels,
)

pytestmark = pytest.mark.unit

AWADI = "الشيخ نبيل العوضي"


def _season(sid: str, name: str, season: int, *, library: str = AWADI, n: int = 30,
            minutes: float = 25, first_air: str = "2024-03-11") -> SeasonCandidate:  # fmt: skip
    folder = f"Y:/{library}"
    return SeasonCandidate(
        series_id=sid, name=name, season=season, kind="episodic", series_key=name,
        folder=folder, presenter=library, library=library, first_air=first_air,
        genre="prophets_stories", tags={"topic:prophets_stories": 1.0},
        episodes=tuple(EpisodeRef(path=f"{folder}/{sid}/{i}.mp4", duration_s=minutes * 60,
                                  title=f"ح{i}", episode=i, season=season)
                       for i in range(1, n + 1)),
    )  # fmt: skip


def _clip(path: str, tags: dict[str, float], *, folder: str = "Y:/c") -> LooseVideo:
    genre = next(iter(tags)).split(":")[1]
    return LooseVideo(path=path, duration_s=180, title=path, folder=folder, presenter="",
                      tags=tags, genre=genre, is_short=True)  # fmt: skip


QIMMA = "Y:/استوديو القمة Alqimmah Studio"
NONSTOP = ThemeSpec(
    "prophets_awadi",
    "Prophets: al-Awadi",
    "قصص الأنبياء · نبيل العوضي",
    128,
    ("prophets_stories",),
    libraries=("نبيل العوضي",),
    series=("قصص الأنبياء",),
    kind="nonstop",
    slot_minutes=60,
    filler_topics=("islamic_history",),
    filler_with=("nasheed", "poetry"),
    filler_libraries=("استوديو القمة",),
)
PROPHETS = ThemeSpec("prophets", "Prophets", "قصص الأنبياء", 104, ("prophets_stories",))


def _plan() -> object:
    seasons = [
        _season("a2", "قصص الأنبياء", 2, first_air="2024-06-06"),
        _season("a1", "قصص الأنبياء", 1),
        _season("o1", "الدنيا قصص وعبر", 1),  # al-Awadi, another series
        _season("x1", "قصص القرآن", 1, library="آخر", n=60),
    ]
    clips = [
        _clip("Y:/c/prophet.mp4", {"topic:prophets_stories": 0.9}),
        _clip("Y:/c/history-nasheed.mp4", {"topic:nasheed": 0.9, "topic:islamic_history": 0.8}),
        _clip("Y:/c/plain-history.mp4", {"topic:islamic_history": 0.9}),
        _clip(f"{QIMMA}/poem.mp4", {"topic:poetry": 0.9}, folder=QIMMA),
        _clip(f"{QIMMA}/surah.mp4", {"topic:quran_recitation": 0.9}, folder=QIMMA),
    ]
    cfg = PlannerConfig(themes=(NONSTOP, PROPHETS), mix_channel=False, presenter_channels=False,
                        show_max_channels=1, channel_min_days=1, channel_day_hours=1,
                        dawn_quran=False)  # type: ignore[arg-type]  # fmt: skip
    return suggest_channels(seasons, clips, cfg)


def test_only_its_series_in_order_an_episode_every_hour() -> None:
    p = _plan()
    ch = next(c for c in p.channels if c.key == "prophets_awadi")  # type: ignore[attr-defined]
    assert ch.kind == "nonstop"
    lane = p.lanes[ch.lanes[0]]  # type: ignore[attr-defined]
    assert [(u.name, u.season) for u in lane.units] == [("قصص الأنبياء", 1), ("قصص الأنبياء", 2)]
    assert len(ch.slots) == 24 and all(s.minutes == 60 and s.lane == lane.key for s in ch.slots)
    assert ch.slots[0].start == 0 and ch.slots[-1].end == 1440


def test_its_series_leaves_the_prophets_channel_the_rest_stays() -> None:
    p = _plan()
    on = {c.key: {u.name for k in c.lanes for u in p.lanes[k].units}  # type: ignore[attr-defined]
          for c in p.channels}  # type: ignore[attr-defined]  # fmt: skip
    assert on["prophets_awadi"] == {"قصص الأنبياء"}
    assert "قصص الأنبياء" not in on["prophets"]
    assert {"الدنيا قصص وعبر", "قصص القرآن"} <= on["prophets"]


def test_filler_its_topic_first_then_history_poems() -> None:
    p = _plan()
    pools = p.filler_pools  # type: ignore[attr-defined]
    topic = {i.path for i in pools["prophets_awadi.topic"].items}
    breaks = {i.path for i in pools["prophets_awadi.breaks"].items}
    assert "Y:/c/prophet.mp4" in topic
    assert {"Y:/c/history-nasheed.mp4", f"{QIMMA}/poem.mp4"} <= breaks
    assert "Y:/c/plain-history.mp4" not in breaks  # history, but not a nasheed or poem
    assert f"{QIMMA}/surah.mp4" not in breaks  # Studio Alqimmah's Quran is not a poem


def test_an_unknown_kind_is_reported() -> None:
    bad = ThemeSpec("x", "X", "س", 130, ("prophets_stories",), kind="marathon")
    p = suggest_channels([_season("a1", "قصص الأنبياء", 1)], (),
                         PlannerConfig(themes=(bad,), mix_channel=False,
                                       presenter_channels=False))  # type: ignore[arg-type]  # fmt: skip
    assert "kind 'marathon'" in dict(p.unplaced)["theme x"]
