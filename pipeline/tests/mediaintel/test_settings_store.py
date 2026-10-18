"""The cache's settings.json: read by the steps, edited by the settings page."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from maktaba_pipeline import settings  # noqa: E402
from scripts.mediaintel import db, serve, settings_store  # noqa: E402
from scripts.mediaintel.group_clips import GroupConfig  # noqa: E402

pytestmark = pytest.mark.unit


def test_steps_read_the_file_beside_the_database(tmp_path: Path) -> None:
    conn = db.connect(tmp_path / "mi.db")
    (tmp_path / "settings.json").write_text(
        json.dumps({"planner": {"extra_airings": False}, "groups": {"min_topic": 4},
                    "detect": {"brand_min": 30}}),
        encoding="utf-8",
    )  # fmt: skip
    assert settings_store.get(conn, "planner").extra_airings is False
    assert settings_store.get(conn, "groups").min_topic == 4
    assert settings_store.get(conn, "detect").brand_min == 30


def test_an_in_memory_cache_has_the_defaults() -> None:
    import sqlite3

    assert settings_store.get(sqlite3.connect(":memory:"), "groups") == GroupConfig()


def test_promo_words_are_a_setting() -> None:
    cfg = settings.apply(GroupConfig, {"promo_words": ["قريبا", "تتر"]})
    assert cfg.promo_re().search("قريبا على الشاشه")
    assert not cfg.promo_re().search("برومو البرنامج")
    assert not cfg.promo_re().search("تترك")  # "تتر" only as a word of its own
    assert cfg.promo_re().search("تتر البرنامج")


def test_the_page_saves_only_valid_values_and_resets(tmp_path: Path) -> None:
    dbf = str(tmp_path / "mi.db")
    out = serve.save_settings(dbf, {"section": "planner", "set": {"mix_number": 200}})
    assert out["changed"] == ["mix_number"]
    with pytest.raises(settings.SettingsError):
        serve.save_settings(dbf, {"section": "planner", "set": {"mix_number": "two"}})
    assert settings.load(tmp_path / "settings.json") == {"planner": {"mix_number": 200}}
    serve.save_settings(dbf, {"section": "planner", "reset": ["mix_number"]})
    assert settings.load(tmp_path / "settings.json") == {"planner": {}}
    page = serve.settings_payload(dbf)
    assert [s["key"] for s in page["sections"]] == ["planner", "groups", "detect", "tag"]
    assert all(f["label"] for s in page["sections"] for f in s["fields"])


def test_a_hand_edited_bad_value_is_shown_and_can_go_back_to_default(tmp_path: Path) -> None:
    dbf = str(tmp_path / "mi.db")
    (tmp_path / "settings.json").write_text(
        json.dumps({"planner": {"mix_number": "two", "dawn_quran": False, "gone": 1}}),
        encoding="utf-8",
    )
    planner = serve.settings_payload(dbf)["sections"][0]
    by = {f["name"]: f for f in planner["fields"]}
    assert "whole number" in by["mix_number"]["invalid"] and by["mix_number"]["changed"]
    assert by["dawn_quran"]["value"] is False  # the good change still applies
    assert planner["unknown"] == ["gone"]
    serve.save_settings(dbf, {"section": "planner", "reset": ["mix_number", "gone"]})
    assert settings.load(tmp_path / "settings.json") == {"planner": {"dawn_quran": False}}
