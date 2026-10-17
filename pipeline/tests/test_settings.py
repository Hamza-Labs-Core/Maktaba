"""Settings: configs as data, round trip, only changes saved, wrong shapes refused."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

import maktaba_pipeline.classify.settings_help  # noqa: F401 — registers the help
import maktaba_pipeline.schedule.settings_help  # noqa: F401
from maktaba_pipeline import settings
from maktaba_pipeline.classify.series_detector import DetectorConfig
from maktaba_pipeline.schedule.channel_planner import (
    EpisodeRef,
    PlannerConfig,
    SeasonCandidate,
    suggest_channels,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize("cls", [PlannerConfig, DetectorConfig])
def test_every_field_round_trips_through_json(cls: type) -> None:
    j = settings.to_json(cls())
    assert json.loads(json.dumps(j, ensure_ascii=False)) == j
    assert settings.to_json(settings.apply(cls, j)) == j
    assert settings.overrides(settings.apply(cls, j)) == {}


@pytest.mark.parametrize("cls", [PlannerConfig, DetectorConfig])
def test_every_field_has_help(cls: type) -> None:
    for f in settings.describe(cls()):
        assert f["group"] != "Other", f["name"]
        assert f["label"], f["name"]


def test_only_changes_are_kept_and_times_read_as_hh_mm() -> None:
    cfg = settings.apply(
        PlannerConfig,
        {"dawn_quran": False, "khutbah_start": "12:30",
         "quran_time_rules": [["05:00", "07:00", "mujawwad"]]},
    )  # fmt: skip
    assert cfg.khutbah_start == 12 * 60 + 30
    assert cfg.quran_time_rules == (("05:00", "07:00", "mujawwad"),)
    assert settings.overrides(cfg) == {
        "dawn_quran": False,
        "khutbah_start": "12:30",
        "quran_time_rules": [["05:00", "07:00", "mujawwad"]],
    }


def test_dayparts_and_themes_are_rows_of_named_values() -> None:
    rows = settings.to_json(PlannerConfig())["dayparts"]
    rows[0]["end"] = "06:30"
    cfg = settings.apply(PlannerConfig, {"dayparts": rows})
    assert cfg.dayparts[0].end == 6 * 60 + 30
    themes = settings.to_json(PlannerConfig())["themes"][:2]
    themes[1]["topics"] = ["hadith", "mustalah"]
    assert settings.apply(PlannerConfig, {"themes": themes}).themes[1].topics == (
        "hadith",
        "mustalah",
    )


@pytest.mark.parametrize(
    ("changes", "field"),
    [
        ({"dawn_quran": "yes"}, "dawn_quran"),
        ({"mix_number": 1.5}, "mix_number"),
        ({"khutbah_start": "noon"}, "khutbah_start"),
        ({"quran_time_rules": [["05:00", "07:00"]]}, "quran_time_rules[0]"),
        ({"dayparts": [{"key": "x", "colour": "red"}]}, "dayparts[0]"),
        ({"no_such": 1}, "no_such"),
        # limits: values that would make the planner fail later are refused on save
        ({"cadence_min_gaps": 0}, "cadence_min_gaps"),
        ({"quran_time_rules": [["", "07:00", "mujawwad"]]}, "quran_time_rules[0]"),
        ({"quran_time_rules": [["05:00", "late", "warsh"]]}, "quran_time_rules[0][1]"),
        ({"quran_rotation": "by_week"}, "quran_rotation"),
        ({"khutbah_day": "friday"}, "khutbah_day"),
    ],
)
def test_a_wrong_shape_is_refused_naming_the_field(changes: dict[str, Any], field: str) -> None:
    with pytest.raises(settings.SettingsError) as e:
        settings.apply(PlannerConfig, changes)
    assert e.value.field == field


def test_save_keeps_the_other_sections(tmp_path: Path) -> None:
    f = tmp_path / "settings.json"
    f.write_text(json.dumps({"detect": {"brand_min": 30}}), encoding="utf-8")
    settings.save(f, "planner", settings.apply(PlannerConfig, {"extra_airings": False}))
    assert settings.load(f) == {"detect": {"brand_min": 30}, "planner": {"extra_airings": False}}


def _season(sid: str, library: str, recitation: bool) -> SeasonCandidate:
    tags = (
        {"topic:quran_recitation": 1.0, "format:recitation": 1.0}
        if recitation
        else {"topic:seerah": 1.0, "genre:seerah": 1.0, "format:lecture": 1.0}
    )
    return SeasonCandidate(
        series_id=sid, name=sid, season=1, kind="episodic", series_key=sid, folder=f"Y:/{library}",
        presenter=library, library=library, first_air="2010-01-01",
        genre="quran_recitation" if recitation else "seerah", tags=tags,
        episodes=tuple(EpisodeRef(path=f"Y:/{library}/{sid}/{i}.mp4", duration_s=1500, title=f"{i}",
                                  episode=i, plex_rating_key=f"{sid}{i}") for i in range(1, 31)),
    )  # fmt: skip


def test_the_quran_fallback_is_a_setting() -> None:
    # a scholar's library: mostly lessons, a few recitations, no reciter's library at all
    seasons = [_season(f"l{i}", "شيخ", False) for i in range(4)] + [_season("q", "شيخ", True)]
    on = suggest_channels(seasons, (), PlannerConfig(theme_min_hours=1.0, channel_min_days=0))
    off = suggest_channels(
        seasons,
        (),
        PlannerConfig(theme_min_hours=1.0, channel_min_days=0, quran_fallback_any_recitation=False),
    )
    assert "library.quran" in on.lanes
    assert "library.quran" not in off.lanes
