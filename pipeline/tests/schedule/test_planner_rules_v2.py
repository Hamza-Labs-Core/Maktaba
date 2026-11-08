"""Planner rules 2.0: the dizqueTV export's settings went with the export."""

from __future__ import annotations

from maktaba_pipeline import settings
from maktaba_pipeline.schedule.channel_planner import (
    RETIRED_V1,
    PlannerConfig,
    migrate_v1_to_v2,
)


def test_planner_rules_are_at_major_two() -> None:
    assert settings.schema_version(PlannerConfig) == (2, 0)
    names = {f.name for f in PlannerConfig.__dataclass_fields__.values()}
    assert not names & set(RETIRED_V1)


def test_a_stored_v1_value_moves_without_the_export_settings() -> None:
    stored = {
        "max_channels": 12,
        "dz_weight_topic": 250,
        "dz_watermark_position": "top-left",
        **{k: 1 for k in RETIRED_V1 if k.endswith(("_ms", "_s", "_days"))},
        "dz_art_url": "http://old/art",
        "dz_public_url": "http://old",
        "dz_guide_filler_title": "Filler",
        # skipped by 1.x (an unknown name, a wrong shape): left out, not refused
        "gone": 1,
        "channel_min_days": "many",
    }
    moved = settings.migrate(PlannerConfig, stored, 1)
    assert moved == {
        "max_channels": 12,
        "dz_weight_topic": 250,
        "dz_watermark_position": "top-left",
    }
    # the step itself leaves the value it was given alone
    assert "dz_art_url" in stored
    assert migrate_v1_to_v2({}) == {}
