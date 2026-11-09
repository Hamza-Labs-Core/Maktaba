"""The in-process fallbacks, counted where a person can read them (Epic 54, story 54.43):
``plugins/native.py`` adds what the worker counted to ``stage_fallbacks`` at each refresh."""

from __future__ import annotations

from collections.abc import Iterator
from typing import Any

import pytest

from maktaba_pipeline.plugins import native

pytestmark = pytest.mark.asyncio


@pytest.fixture(autouse=True)
def _clean() -> Iterator[None]:
    native._UNWRITTEN.clear()
    native.FALLBACKS.clear()
    yield
    native._UNWRITTEN.clear()
    native.FALLBACKS.clear()


async def _rows(db: Any) -> dict[tuple[str, str, str], int]:
    rows = await db.fetch("SELECT machine, stage, reason, count FROM stage_fallbacks")
    return {(r["machine"], r["stage"], r["reason"]): int(r["count"]) for r in rows}


async def test_counts_are_added_and_cleared(db: Any) -> None:  # T01
    for _ in range(3):
        native.note_fallback("probe", "runner_not_serving_here")
    native.count_fallback("thumb_hash", "fingerprint_inproc")
    assert await native.write_fallbacks(db, None) == 2
    assert native._UNWRITTEN == {}
    assert await _rows(db) == {
        ("home", "probe", "runner_not_serving_here"): 3,
        ("home", "thumb_hash", "fingerprint_inproc"): 1,
    }
    # a later write (after a restart: nothing is held in memory) adds to the row
    native.note_fallback("probe", "runner_not_serving_here")
    native.note_fallback("probe", "runner_not_serving_here")
    await native.write_fallbacks(db, None)
    assert (await _rows(db))[("home", "probe", "runner_not_serving_here")] == 5
    # a joined machine has its own row
    native.note_fallback("probe", "runner_not_serving_here")
    await native.write_fallbacks(db, "node-7")
    assert (await _rows(db))[("node-7", "probe", "runner_not_serving_here")] == 1
    # parity's in-memory table keeps its own reasons only
    assert native.FALLBACKS == {("probe", "runner_not_serving_here"): 6}


class _Failing:
    """A database whose statements fail (it went away)."""

    dialect = "sqlite"

    async def execute(self, sql: str, *args: Any) -> None:
        raise ConnectionError("database away")


async def test_a_failed_write_keeps_the_counts(db: Any) -> None:  # T02
    native.note_fallback("link", "role_unusable")
    assert await native.write_fallbacks(_Failing(), None) == 0  # never raises
    native.note_fallback("link", "role_unusable")
    await native.write_fallbacks(db, None)
    assert await _rows(db) == {("home", "link", "role_unusable"): 2}


async def test_a_fingerprint_from_the_worker_is_counted(monkeypatch: pytest.MonkeyPatch) -> None:
    # T03: the fp runner did not answer, so the in-process fingerprint ran
    async def refused(stage: str, kind: str, subjects: list[Any]) -> list[str | None]:
        raise ConnectionError("fp runner away")

    async def inproc(db: Any, subject: Any) -> str:
        return "h1"

    monkeypatch.setattr(native, "fingerprints", refused)
    fn = native.fingerprint_fn("thumb_hash", "input", inproc)
    assert await fn(None, object()) == "h1"
    assert native._UNWRITTEN[("thumb_hash", "fingerprint_inproc")][0] == 1
    assert native.FALLBACKS == {}
