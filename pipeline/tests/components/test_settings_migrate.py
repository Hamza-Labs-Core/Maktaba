"""Settings versions and migrations without a database (story 51.13): a stored value moves
through its section's chain of migrations and is checked in full (T01, T02), a step that
raises or a result that does not validate is refused (T03's first half), every section of
the schema has a version (T05), and the catalog names an owner for each section."""

from __future__ import annotations

import dataclasses
from typing import Any, ClassVar

import pytest

from maktaba_pipeline import rules, settings
from maktaba_pipeline.components import settings_migrate

pytestmark = pytest.mark.unit


@dataclasses.dataclass(frozen=True, slots=True)
class _V2:
    """A section at 2.0: 1.0's ``tries`` became ``relisten_tries``."""

    SCHEMA_VERSION: ClassVar[tuple[int, int]] = (2, 0)
    relisten_tries: int = 6
    without_second_listen: bool = False


@dataclasses.dataclass(frozen=True, slots=True)
class _V3:
    """A section at 3.0: 1.0 ``a`` -> 2.0 ``b`` -> 3.0 ``c`` (a text now)."""

    SCHEMA_VERSION: ClassVar[tuple[int, int]] = (3, 1)
    c: str = ""
    added_in_3_1: int = 0


@pytest.fixture(autouse=True)
def _own_registry(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(settings, "_MIGRATIONS", {})


def _v1_to_v2(old: dict[str, Any]) -> dict[str, Any]:
    new = dict(old)
    if "tries" in new:
        new["relisten_tries"] = new.pop("tries")
    return new


# T01
def test_a_1_0_value_is_migrated_and_validates() -> None:
    settings.register_migration(_V2, 1, _v1_to_v2)
    got = settings.migrate(_V2, {"tries": 9, "without_second_listen": True}, 1)
    assert got == {"relisten_tries": 9, "without_second_listen": True}
    assert settings.apply(_V2, got).relisten_tries == 9


# T02
def test_the_chain_runs_each_step_in_order() -> None:
    calls: list[str] = []

    def one(old: dict[str, Any]) -> dict[str, Any]:
        calls.append("1->2")
        return {"b": old["a"] * 2}

    def two(old: dict[str, Any]) -> dict[str, Any]:
        calls.append("2->3")
        return {"c": str(old["b"])}

    settings.register_migration(_V3, 2, two)
    settings.register_migration(_V3, 1, one)
    assert settings.migrate(_V3, {"a": 21}, 1) == {"c": "42"}
    assert calls == ["1->2", "2->3"]
    calls.clear()
    assert settings.migrate(_V3, {"b": 5}, 2) == {"c": "5"}
    assert calls == ["2->3"]


# T03 (the refusal; the database half is in tests/db/test_settings_migrate_db.py)
def test_a_step_that_raises_or_a_bad_result_is_refused() -> None:
    def boom(old: dict[str, Any]) -> dict[str, Any]:
        raise KeyError("tries")

    settings.register_migration(_V2, 1, boom)
    with pytest.raises(settings.MigrationError, match="migrate_v1_to_v2"):
        settings.migrate(_V2, {"tries": 9}, 1)

    settings._MIGRATIONS.clear()  # noqa: SLF001
    settings.register_migration(_V2, 1, lambda old: {"relisten_tries": "many"})
    with pytest.raises(settings.MigrationError, match="does not validate"):
        settings.migrate(_V2, {"tries": 9}, 1)

    with pytest.raises(settings.MigrationError, match="no migrate_v1_to_v2"):
        settings.migrate(_V3, {"a": 1}, 1)


def test_versions_and_registration_are_checked() -> None:
    assert settings.schema_version(rules.SECTIONS["planner"][0]) == settings.DEFAULT_VERSION
    assert settings.schema_version(_V3) == (3, 1)

    @dataclasses.dataclass(frozen=True)
    class Bad:
        SCHEMA_VERSION: ClassVar[tuple[int, int]] = (0, 1)

    with pytest.raises(ValueError, match="SCHEMA_VERSION"):
        settings.schema_version(Bad)
    with pytest.raises(ValueError, match="no step from 2"):
        settings.register_migration(_V2, 2, _v1_to_v2)
    settings.register_migration(_V2, 1, _v1_to_v2)
    with pytest.raises(ValueError, match="is registered"):
        settings.register_migration(_V2, 1, _v1_to_v2)


# T05
def test_every_section_of_the_schema_has_its_version() -> None:
    doc = rules.schema()
    assert {s["key"] for s in doc["sections"]} == set(rules.SECTIONS)
    for s in doc["sections"]:
        assert s["version"] == list(settings.schema_version(rules.SECTIONS[s["key"]][0])), s["key"]


def test_parse_version_reads_what_any_release_left() -> None:
    assert rules.parse_version(None) == rules.StoredVersion(1, 0)
    assert rules.parse_version("not json") == rules.StoredVersion(1, 0)
    assert rules.parse_version('{"major": 0, "minor": -1}') == rules.StoredVersion(1, 0)
    v = rules.parse_version('{"major": 2, "minor": 1, "failed": "x", "previous": {"major": 1}}')
    assert (v.major, v.minor, v.failed, v.previous) == (2, 1, "x", {"major": 1})
    assert v.to_json() == {"major": 2, "minor": 1, "failed": "x", "previous": {"major": 1}}


def test_every_section_has_its_owner_from_the_catalog() -> None:
    owners = settings_migrate.owners()
    assert owners["transcription"] == "transcription"
    assert owners["planner"] == settings_migrate.owner("planner") != "core.pipeline"
    assert settings_migrate.owner("no_such_section") == "core.pipeline"
    got = settings_migrate.blocked({"transcription": "bad", "tv": "worse", "planner": "x"})
    assert got["transcription"] == [("transcription", "bad")]
    assert sorted(got[owners["tv"]]) == [("planner", "x"), ("tv", "worse")]
