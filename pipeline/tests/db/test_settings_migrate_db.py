"""The home's settings pass on both dialects (story 51.13): a 1.0 value of a 2.0 section is
moved with its version and audit row in one transaction (T06), a migration that raises
writes nothing to the section and keeps its component Failed with ``settings_invalid``
while the others run (T03), a full save takes it out of Failed, and the stages never read
a failed section as the defaults."""

from __future__ import annotations

import asyncio
import dataclasses
from typing import Any, ClassVar

import pytest

from maktaba_pipeline import rules, settings
from maktaba_pipeline.components import settings_migrate, store
from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.runtime import Database


@dataclasses.dataclass(frozen=True, slots=True)
class _TranscriptionV2:
    """TranscriptionConfig at 2.0: 1.0's ``relisten_tries`` became ``tries``."""

    SCHEMA_VERSION: ClassVar[tuple[int, int]] = (2, 0)
    tries: int = 6
    without_second_listen: bool = False


def _v1_to_v2(old: dict[str, Any]) -> dict[str, Any]:
    new = dict(old)
    new["tries"] = new.pop("relisten_tries")
    return new


@pytest.fixture
def v2(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(settings, "_MIGRATIONS", {})
    title, what = rules.SECTIONS["transcription"][1:]
    monkeypatch.setitem(rules.SECTIONS, "transcription", (_TranscriptionV2, title, what))


async def _put(db: Database, key: str, value: Any) -> None:
    cast = "::jsonb" if is_pg(db) else ""
    await db.execute(
        f"INSERT INTO app_settings (key, value) VALUES ($1, $2{cast})", key, dumps(value)
    )


async def _get(db: Database, key: str) -> Any:
    return loads(await db.fetchval("SELECT value FROM app_settings WHERE key = $1", key))


async def _audits(db: Database) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT target_id, payload FROM audit_log WHERE category = 'components'"
            " AND action = $1",
            settings_migrate.AUDIT_ACTION,
        )
    )


# T06
@pytest.mark.asyncio
@pytest.mark.usefixtures("v2")
async def test_a_1_0_value_is_moved_with_its_version_and_audit_row(db: Database) -> None:
    settings.register_migration(_TranscriptionV2, 1, _v1_to_v2)
    await _put(db, "rules.transcription", {"relisten_tries": 9})

    failed = await settings_migrate.run_pass(db)

    assert failed == {}
    assert await _get(db, "rules.transcription") == {"tries": 9}
    assert await _get(db, "rules_version.transcription") == {
        "major": 2,
        "minor": 0,
        "previous": {"major": 1, "value": {"relisten_tries": 9}},
    }
    audits = await _audits(db)
    assert len(audits) == 1 and audits[0]["target_id"] == "transcription"
    payload = loads(audits[0]["payload"])
    assert payload["before"]["value"] == {"relisten_tries": 9}
    assert payload["after"] == {"version": [2, 0], "value": {"tries": 9}}
    cfg, _, _ = await rules.read(db, "transcription")
    assert cfg.tries == 9

    # a second worker (or a second home) finds nothing to do
    again = await settings_migrate.migrate_all(db, ["transcription"])
    assert [o.action for o in again] == ["none"]
    assert len(await _audits(db)) == 1


# T06: a failure mid-way (here the audit row) writes none of the three
@pytest.mark.asyncio
@pytest.mark.usefixtures("v2")
async def test_a_pass_cut_short_writes_nothing(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    settings.register_migration(_TranscriptionV2, 1, _v1_to_v2)
    await _put(db, "rules.transcription", {"relisten_tries": 9})

    async def cut(*_: object) -> None:
        raise ConnectionResetError("killed")

    monkeypatch.setattr(settings_migrate, "_audit", cut)
    with pytest.raises(ConnectionResetError):
        await settings_migrate.migrate_section(db, "transcription")
    assert await _get(db, "rules.transcription") == {"relisten_tries": 9}
    assert (
        await db.fetchval(
            "SELECT count(*) FROM app_settings WHERE key = 'rules_version.transcription'"
        )
        == 0
    )


# T03
@pytest.mark.asyncio
@pytest.mark.usefixtures("v2")
async def test_a_migration_that_raises_keeps_its_component_failed(db: Database) -> None:
    def boom(old: dict[str, Any]) -> dict[str, Any]:
        raise ValueError("cannot read relisten_tries")

    settings.register_migration(_TranscriptionV2, 1, boom)
    await _put(db, "rules.transcription", {"relisten_tries": 9})
    await _put(db, "rules.planner", {"dawn_quran": False})

    failed = await settings_migrate.run_pass(db)

    assert (
        set(failed) == {"transcription"} and "cannot read relisten_tries" in failed["transcription"]
    )
    assert await _get(db, "rules.transcription") == {"relisten_tries": 9}
    version = await _get(db, "rules_version.transcription")
    assert version["major"] == 1 and "cannot read" in version["failed"]
    assert await _audits(db) == []

    rows = await store.statuses(db, "transcription")
    assert [(r.part, r.phase, r.reason) for r in rows] == [
        (settings_migrate.PART, "failed", "settings_invalid")
    ]
    assert rows[0].conditions[0].params["section"] == "transcription"
    # the other components are not touched, and their sections read as they are
    assert await store.statuses(db, "livetv") == []
    cfg, _, _ = await rules.read(db, "planner")
    assert cfg.dawn_quran is False
    # the stage never runs on the defaults instead
    with pytest.raises(rules.SettingsInvalid, match="cannot read"):
        await rules.read(db, "transcription")


# T03 continued: a full save (the API's replace) clears failed; the watch takes the
# component out of Failed on its next round
@pytest.mark.asyncio
@pytest.mark.usefixtures("v2")
async def test_a_full_save_takes_the_component_out_of_failed(db: Database) -> None:
    settings.register_migration(_TranscriptionV2, 1, lambda old: {"tries": "many"})
    await _put(db, "rules.transcription", {"relisten_tries": 9})
    assert set(await settings_migrate.run_pass(db)) == {"transcription"}

    stop = asyncio.Event()
    task = asyncio.create_task(settings_migrate.watch(db, stop, interval=0.05))
    try:
        await asyncio.sleep(0.1)
        assert [r.phase for r in await store.statuses(db, "transcription")] == ["failed"]
        # what PATCH ... {"replace": true} writes
        async with db.transaction():
            await db.execute("DELETE FROM app_settings WHERE key LIKE 'rules%transcription'")
            await _put(db, "rules.transcription", {"tries": 3})
            await _put(db, "rules_version.transcription", {"major": 2, "minor": 0})
        for _ in range(100):
            if not await store.statuses(db, "transcription"):
                break
            await asyncio.sleep(0.05)
        assert await store.statuses(db, "transcription") == []
    finally:
        stop.set()
        await task
    cfg, _, _ = await rules.read(db, "transcription")
    assert cfg.tries == 3


# the runners of every machine hear of a failed section at once (a reload on
# components_changed), not only on their next desired-state change
@pytest.mark.asyncio
@pytest.mark.usefixtures("v2")
async def test_a_failed_section_tells_the_runners(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("SQLite has no NOTIFY: its runners poll")
    heard: asyncio.Queue[str] = asyncio.Queue()
    conn = await db.acquire_listener()
    try:
        await conn.add_listener(store.CHANNEL, lambda *a: heard.put_nowait(str(a[-1])))
        assert await settings_migrate.run_pass(db) == {}
        await asyncio.sleep(0.2)
        assert heard.empty()  # nothing failed: nothing said

        def boom(old: dict[str, Any]) -> dict[str, Any]:
            raise ValueError("cannot read relisten_tries")

        settings.register_migration(_TranscriptionV2, 1, boom)
        await _put(db, "rules.transcription", {"relisten_tries": 9})
        assert set(await settings_migrate.run_pass(db)) == {"transcription"}
        payload = await asyncio.wait_for(heard.get(), timeout=5)
        assert store.parse_notify(payload).reload
    finally:
        await conn.close()


@pytest.mark.asyncio
async def test_same_major_and_no_value_need_nothing(db: Database) -> None:
    await _put(db, "rules.planner", {"dawn_quran": False})
    outcomes = await settings_migrate.migrate_all(db)
    assert {o.action for o in outcomes} == {"none"}
    assert (
        await db.fetchval(
            "SELECT count(*) FROM app_settings WHERE key LIKE 'rules\\_version.%' ESCAPE '\\'"
        )
        == 0
    )
    assert await settings_migrate.run_pass(db) == {}
    assert await store.statuses(db) == []


@pytest.mark.asyncio
async def test_a_newer_major_is_left_and_read_field_by_field(db: Database) -> None:
    await _put(db, "rules.planner", {"dawn_quran": False, "renamed_later": 1})
    await _put(db, "rules_version.planner", {"major": 3, "minor": 0})
    [o] = await settings_migrate.migrate_all(db, ["planner"])
    assert o.action == "newer"
    cfg, _, bad = await rules.read(db, "planner")
    assert cfg.dawn_quran is False and "renamed_later" in bad
