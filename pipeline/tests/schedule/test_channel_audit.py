"""The channel audit's judging, shared by the engine stage and the mediaintel tool."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.schedule.channel_audit import (
    AuditLookup,
    audit,
    html_report,
    summarize,
    text_report,
)
from maktaba_pipeline.schedule.channel_planner import PlannerConfig, ThemeSpec

FIQH = ThemeSpec("fiqh", "Fiqh", "الفقه", 105, ("fiqh",))
QURAN = ThemeSpec("quran", "Quran", "القرآن", 101, ("quran",), kind="quran")
CFG = PlannerConfig(themes=(QURAN, FIQH))
HOUR = 3_600_000
NOW = 1_800_000_000_000
START = "2027-01-15T08:00:00Z"  # NOW is 08:00 that day

LOOKUP = AuditLookup(
    item_of={"1": "v1", "2": "v2", "3": "v3"},
    series_of={"v1": "s1", "v2": "s2"},
    library_of={"s1": "Fiqh lessons", "s2": "Cooking"},
    folder_of={"v3": "Loose"},
    series_tags={"s1": {"genre:fiqh": (1.0, "")}, "s2": {"topic:cooking": (0.9, "name:x")}},
    item_tags={},
)


def channel(
    number: int, key: str, *programs: tuple[str, str], fillers: list[Any] | None = None
) -> dict[str, Any]:
    return {
        "number": number,
        "name": f"Channel {number}",
        "maktabaPlan": {"key": key},
        "startTime": START,
        "programs": [
            {"ratingKey": rk, "title": f"t{rk}", "duration": HOUR, "customShowName": show}
            for rk, show in programs
        ],
        "fillerCollections": fillers or [],
    }


def run(*channels: dict[str, Any], fillers: dict[str, Any] | None = None) -> Any:
    return audit(list(channels), fillers or {}, LOOKUP, CFG, NOW, window_days=1)


def test_what_airs_is_weighed_by_hours_against_the_theme() -> None:
    (ca,) = run(channel(5, "fiqh", ("1", "Fiqh [a]"), ("2", "Cooking [b]"), ("3", "Loose [c]")))
    by = {i.key: i for i in ca.programmes}
    assert by["1"].verdict == "strong" and by["1"].library == "Fiqh lessons"
    assert by["2"].verdict == "none" and "cooking" in by["2"].why
    assert by["3"].verdict == "none" and by["3"].library == "Loose"  # the folder's name
    assert round(sum(i.minutes for i in ca.programmes)) == 24 * 60  # one day, all of it counted
    s = summarize(ca)
    assert s["theme"] == "fiqh" and s["hours"] == 24.0
    assert (
        s["share"]["strong"] == 33.3 and s["share"]["none"] == 66.7 and s["shows"][0]["hours"] > 0
    )


def test_the_dawn_quran_hour_is_by_design_except_on_the_quran_channel() -> None:
    dawn = ("1", "maktaba-plan · Dawn [library.quran]")
    fiqh, quran = run(channel(5, "fiqh", dawn), channel(6, "quran", dawn))
    assert fiqh.programmes[0].verdict == "design"
    assert quran.programmes[0].verdict != "design"  # theme.kind == "quran": judged like any


def test_a_channel_that_is_no_theme_is_not_judged() -> None:
    (ca,) = run(channel(9, "mix", ("2", "Cooking [b]")))
    assert ca.theme is None and ca.programmes[0].verdict == "strong"
    assert summarize(ca)["theme"] is None


def test_filler_weighs_what_dizquetv_picks_and_promos_belong() -> None:
    ch = channel(
        5,
        "fiqh",
        ("1", "Fiqh [a]"),
        fillers=[{"id": "f1", "weight": 300}, {"id": "f2", "weight": 100}],
    )
    fillers = {
        "f1": {"name": "x [fiqh.breaks]", "content": [{"ratingKey": "2", "title": "c"}]},
        "f2": {"name": "x [fiqh.promos]", "content": [{"ratingKey": "2", "title": "p"}]},
    }
    (ca,) = run(ch, fillers=fillers)
    clip, promo = ca.fillers
    assert clip.verdict == "none" and promo.verdict == "strong"
    assert round(clip.minutes) == 75 and round(promo.minutes) == 25  # 300 : 100
    s = summarize(ca)
    assert s["filler_clips"] == 2 and s["filler_unrelated"][0]["title"] == "c"


def test_the_reports_name_the_window_and_every_channel() -> None:
    audits = run(channel(5, "fiqh", ("1", "Fiqh [a]"), ("2", "Cooking [b]")))
    text = text_report(audits, 14)
    page = html_report(audits, 14)
    assert "next 14 days" in text and "next 14 days" in page
    assert "#5 Channel 5" in text and "Channel 5" in page and "Cooking" in page
