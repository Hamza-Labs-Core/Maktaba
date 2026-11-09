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


async def test_on_windows_a_first_party_stage_asks_its_runner(
    stage: str, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Story 54.23: a runner on Windows starts its tools through anonymous pipes (winproc),
    so a first-party stage there is chosen as on any other OS: its runner is asked."""
    import sys

    asked: list[str] = []

    async def serving(s: str) -> tuple[bool, str | None]:
        asked.append(s)
        return False, None

    monkeypatch.setattr(native, "serving", serving)
    monkeypatch.setattr(sys, "platform", "win32")
    got = await native.choose(_DB(True), stage)
    assert (got.impl, got.why) == ("inproc", "runner_not_serving_here")
    assert asked == [stage]


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


class _LibDB(_DB):
    def __init__(self, roots: list[str] | Exception) -> None:
        super().__init__(True)
        self.roots = roots

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        assert "libraries" in sql
        if isinstance(self.roots, Exception):
            raise self.roots
        return [{"roots": self.roots}]


@pytest.mark.parametrize(
    ("roots", "why"),
    [
        (["\\\\nas\\media\\shows"], "windows_share_library"),
        (RuntimeError("no libraries table"), "windows_share_library"),
        (["C:\\media"], ""),
    ],
)
async def test_on_windows_unproven_library_stages_run_in_the_worker_while_a_library_is_on_a_share(
    monkeypatch: pytest.MonkeyPatch, roots: list[str] | Exception, why: str
) -> None:
    """Story 54.23: v0.3.105's runners read a look on a share as a missing file, so on
    Windows a library stage runs in the worker while a library is on a share (unknown: the
    worker too) unless the Windows end to end proves it in its runner there
    (SHARE_RUNNER_STAGES: thumb_small, chapter_thumbs, subtitle_gen, which ask their
    runner); with local libraries only, or with MAKTABA_WINDOWS_SHARE_RUNNERS=1 (the end
    to end's own cases), every stage asks its runner. A stage with no library access, and
    any stage off Windows, asks its runner either way."""

    async def serving(_s: str) -> tuple[bool, str | None]:
        return True, None

    async def hello(_g: str) -> dict[str, Any]:
        return {"areas": "a"}

    monkeypatch.setattr(native, "serving", serving)
    monkeypatch.setattr(native, "hello", hello)
    monkeypatch.setattr(native, "_AREAS_WRITTEN", ["a"])
    monkeypatch.setattr(sys, "platform", "win32")
    monkeypatch.delenv(native.SHARE_RUNNERS_ENV, raising=False)
    native._ROLE_OK.clear()
    for s in ("thumb_small", "convert_mp4", "dedup_apply", "subtitle_gen", "transcribe"):
        assert native.touches_library(s), s
    assert not native.touches_library("plex_livetv")

    async def chosen(stage: str) -> tuple[str, str]:
        native._SHARE_LIBRARY.clear()
        got = await native.choose(_LibDB(roots), stage)
        return got.impl, got.why

    guarded = (
        "convert_mp4",
        "dedup_apply",
        "dedup_scan",
        "trash_purge",
        "file_restore",
        "repair_broken",
        "youtube_fetch",
        "transcribe",
        "second_listen",
    )
    for s in guarded:
        assert await chosen(s) == (("inproc", why) if why else ("plugin", "")), s
    for s in ("thumb_small", "chapter_thumbs", "subtitle_gen"):
        assert await chosen(s) == ("plugin", ""), s
    assert await chosen("plex_livetv") == ("plugin", "")
    monkeypatch.setenv(native.SHARE_RUNNERS_ENV, "1")
    assert await chosen("convert_mp4") == ("plugin", "")
    monkeypatch.delenv(native.SHARE_RUNNERS_ENV)
    monkeypatch.setattr(sys, "platform", "linux")
    assert await chosen("convert_mp4") == ("plugin", "")


async def test_a_stage_leaves_the_share_guard_only_with_its_end_to_end_case() -> None:
    """SHARE_RUNNER_STAGES: each stage is a library stage whose plugin never writes the
    libraries in full (its broker would serve that share for reads only), and the Windows
    end to end runs it on the share with the override off, in its runner (a case with
    ``ShareRunners: &off`` that is neither a hand-off nor guarded)."""
    import re
    from pathlib import Path

    e2e = (
        Path(__file__).resolve().parents[4]
        / "cmd/maktaba-server/internal/supervisor/runners_windows_stages_e2e_test.go"
    ).read_text(encoding="utf-8")
    cases = re.findall(
        r"\{Name: \"([^\"]+)\", Stage: \"([^\"]+)\", Library: \"(\w+)\"(.*?)\},\n", e2e, re.S
    )
    proven = {
        stage
        for _name, stage, lib, rest in cases
        if lib == "share"
        and "ShareRunners: &off" in rest
        and "handOff: true" not in rest
        and "guarded: true" not in rest
    }
    for stage in native.SHARE_RUNNER_STAGES:
        assert native.touches_library(stage), stage
        fp = firstparty.by_stage(stage)
        assert fp is not None
        writes = (fp.trusted.get("files") or {}).get("write") or []
        assert "library" not in writes, f"{stage} writes the libraries in full"
        assert stage in proven, f"{stage} has no override-off share case in the end to end"
    # and the end to end keeps one guarded case, so the guard itself stays proven
    assert any("guarded: true" in rest for *_x, rest in cases)


async def test_on_sqlite_a_first_party_stage_runs_in_the_worker_counted() -> None:
    """Story 54.43: SQLite has no plugin API, so the choice names ``sqlite``, which the
    worker counts toward stage_fallbacks; a stage no plugin defines is no fallback."""

    class _SQLite:
        dialect = "sqlite"

    got = await native.choose(_SQLite(), "plex_livetv")
    assert (got.impl, got.why) == ("inproc", "sqlite")
    got = await native.choose(_SQLite(), "component_catchup")
    assert (got.impl, got.why) == ("inproc", "")
