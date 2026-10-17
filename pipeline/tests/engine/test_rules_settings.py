"""Rules as settings in Maktaba: stages read ``app_settings`` ``rules.<section>``, a bad
stored value is skipped (its default applies), a changed rule re-runs what it affects,
and the schema the API serves matches the code."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline import rules
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.sql import dumps, now, to_ts
from maktaba_pipeline.engine.stages import clip_groups, detect, plan_channels, promo_links
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library

SCHEMA = Path(__file__).resolve().parents[3] / "api/internal/handlers/rules/schema.json"


async def _set(db: Any, section: str, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        f"rules.{section}",
        dumps(value),
        to_ts(db, now()),
    )


@pytest.mark.asyncio
async def test_no_stored_rules_are_the_defaults(db: Database) -> None:
    cfg, changes, bad = await rules.read(db, "planner")
    assert cfg == rules.SECTIONS["planner"][0]() and changes == {} and bad == {}


@pytest.mark.asyncio
async def test_stored_changes_apply_and_a_bad_one_is_skipped(db: Database) -> None:
    await _set(db, "planner", {"extra_airings": False, "mix_number": "two", "gone": 1})
    cfg, _, bad = await rules.read(db, "planner")
    assert cfg.extra_airings is False
    assert cfg.mix_number == rules.SECTIONS["planner"][0]().mix_number  # default kept
    assert set(bad) == {"mix_number", "gone"}


@pytest.mark.asyncio
async def test_a_changed_rule_runs_its_stages_again(db: Database) -> None:
    lib = await make_library(db, "Lib")
    subject = Subject("library", lib)
    before = {
        m.__name__: await m.input_fingerprint(db, subject)
        for m in (detect, clip_groups, promo_links)
    }
    plan_before = await plan_channels.input_fingerprint(db, Subject("catalog", lib))
    await _set(db, "groups", {"min_topic": 4})
    await _set(db, "detect", {"brand_min": 30})
    await _set(db, "planner", {"extra_airings": False})
    for m in (detect, clip_groups, promo_links):
        assert await m.input_fingerprint(db, subject) != before[m.__name__], m.__name__
    assert await plan_channels.input_fingerprint(db, Subject("catalog", lib)) != plan_before


def test_the_api_schema_is_current() -> None:
    """``python -m maktaba_pipeline.rules --schema api/internal/handlers/rules/schema.json``
    regenerates it after a rule or its help changes."""
    assert json.loads(SCHEMA.read_text(encoding="utf-8")) == json.loads(
        json.dumps(rules.schema(), ensure_ascii=False)
    )


class _Failing:
    def __init__(self, exc: Exception) -> None:
        self.exc = exc

    async def fetchrow(self, *_: Any) -> Any:
        raise self.exc


@pytest.mark.asyncio
async def test_only_a_missing_table_falls_back_to_the_defaults() -> None:
    import sqlite3

    cfg, changes, _ = await rules.read(
        _Failing(sqlite3.OperationalError("no such table: app_settings")), "planner"
    )
    assert (
        changes == {}
        and cfg
        == (await rules.read(_Failing(sqlite3.OperationalError("no such table: x")), "planner"))[0]
    )
    with pytest.raises(ConnectionResetError):
        await rules.read(_Failing(ConnectionResetError("server closed the connection")), "planner")
