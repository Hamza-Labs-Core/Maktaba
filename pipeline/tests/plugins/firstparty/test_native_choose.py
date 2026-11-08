"""Where a first-party stage's job runs (plugins/native.py ``choose``): the plugin path
only where the plugin's Postgres role is usable by this machine's login (Epic 54, 54.24 §3).
"""

from __future__ import annotations

import sys
from typing import Any

import pytest

from maktaba_pipeline.plugins import firstparty, native, pipe

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
    # a plugin with no file access (tv.maktaba.classify reads thumbnails since wave 4, so
    # its runner must also hold this machine's current areas)
    name = "plex_livetv"
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


async def test_on_windows_a_first_party_stage_stays_in_the_worker(
    stage: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Story 54.23: a runner inside its Windows AppContainer cannot create the named pipes
    asyncio captures a tool's output with, so until the runners start tools through
    anonymous pipes every first-party stage on Windows runs in the worker, and no runner
    is asked or started for it."""
    import sys

    asked: list[str] = []

    async def serving(s: str) -> tuple[bool, str | None]:
        asked.append(s)
        return True, None

    monkeypatch.setattr(native, "serving", serving)
    monkeypatch.setattr(sys, "platform", "win32")
    got = await native.choose(_DB(True), stage)
    assert (got.impl, got.why) == ("inproc", "windows_tool_pipes")
    assert asked == []


async def test_the_answer_is_kept_for_a_minute(stage: str) -> None:
    db = _DB(True)
    await native.choose(db, stage)
    await native.choose(db, stage)
    assert db.role_asks == 1


async def test_the_worker_reaches_a_runner_through_its_pipe(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """On Windows the worker opens the group's pipe (the supervisor's prefix and the
    group's name; story 54.23); without the prefix it finds none, as a missing socket
    elsewhere."""
    opened: list[str] = []

    async def open_pipe(name: str, **_: Any) -> Any:
        opened.append(name)
        return "r", "w"

    monkeypatch.setattr(sys, "platform", "win32")
    monkeypatch.setattr(pipe, "open_pipe", open_pipe)
    monkeypatch.setenv("MAKTABA_RUNNER_PIPES", "\\\\.\\pipe\\maktaba-runner-ab-")
    got: Any = await native.open_runner("light")
    assert got == ("r", "w")
    assert opened == ["\\\\.\\pipe\\maktaba-runner-ab-light"]
    monkeypatch.delenv("MAKTABA_RUNNER_PIPES")
    with pytest.raises(FileNotFoundError):
        await native.open_runner("light")
