"""Where a first-party stage's job runs (plugins/native.py ``choose``): the plugin path
only where the plugin's Postgres role is usable by this machine's login (Epic 54, 54.24 §3).
"""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.plugins import firstparty, native

pytestmark = pytest.mark.asyncio


class _DB:
    dialect = "postgres"

    def __init__(self, role_ok: bool | Exception) -> None:
        self.role_ok = role_ok
        self.role_asks = 0

    async def fetchval(self, sql: str, *args: Any) -> Any:
        if "job_stages" in sql:
            return "plugin"
        self.role_asks += 1
        if isinstance(self.role_ok, Exception):
            raise self.role_ok
        return self.role_ok


@pytest.fixture
def stage(monkeypatch: pytest.MonkeyPatch) -> str:
    name = "promo_links"  # tv.maktaba.classify's (wave 1)
    assert firstparty.by_stage(name) is not None

    async def serving(_stage: str) -> tuple[bool, str | None]:
        return True, None

    monkeypatch.setattr(native, "serving", serving)
    native._ROLE_OK.clear()
    return name


async def test_a_usable_role_runs_the_plugin(stage: str) -> None:
    assert (await native.choose(_DB(True), stage)).impl == "plugin"


async def test_a_role_this_login_may_not_use_runs_in_process(stage: str) -> None:
    got = await native.choose(_DB(False), stage)
    assert (got.impl, got.why) == ("inproc", "role_unusable")


async def test_an_unreadable_answer_runs_in_process(stage: str) -> None:
    got = await native.choose(_DB(RuntimeError("no")), stage)
    assert (got.impl, got.why) == ("inproc", "role_unusable")


async def test_the_answer_is_kept_for_a_minute(stage: str) -> None:
    db = _DB(True)
    await native.choose(db, stage)
    await native.choose(db, stage)
    assert db.role_asks == 1
