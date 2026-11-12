"""The content screen never leaves the home (Epic 57, story 57.17): the stage has no path
to a cloud model at all. Proved twice: by what the two modules import and read, and by
running the stage through the real door with every connection off this machine refused.

Also the pure parts: windows, the strictest window, music."""

from __future__ import annotations

import ast
import json
import socket
from collections.abc import AsyncIterator
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.classify import content_screen as screen
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.stages import content_screen as stage
from maktaba_pipeline.llm import ClusterLLM
from maktaba_pipeline.runtime import Database
from tests.stages.test_content_screen import MODEL, _engine, _row, _transcript, _video, answer
from tests.stages.world import drain_core

MODULES = (Path(screen.__file__), Path(stage.__file__))
# what may be imported: the standard library's plain parts and the pipeline's own engine,
# rules and door. Nothing that opens a connection, no plugin, no compute placement.
ALLOWED_STDLIB = {"__future__", "contextlib", "json", "math", "re", "time", "typing", "dataclasses",
                  "collections.abc"}  # fmt: skip
ALLOWED_OWN = {
    "maktaba_pipeline.classify.content_screen",
    "maktaba_pipeline.db.jobs",
    "maktaba_pipeline.engine.context",
    "maktaba_pipeline.engine.enqueue",
    "maktaba_pipeline.engine.graph",
    "maktaba_pipeline.engine.hashing",
    "maktaba_pipeline.engine.sql",
    "maktaba_pipeline.engine.stages.registry",
    "maktaba_pipeline.llm",
    "maktaba_pipeline.log",
    "maktaba_pipeline.rules",
    "maktaba_pipeline.classify",
    "maktaba_pipeline",
}
DOOR_NAMES = {"cluster_llm", "ClusterUnavailable", "NoModel", "LLMError"}


def _absolute(path: Path, node: ast.ImportFrom) -> str:
    """The module an import names, written from the top."""
    if not node.level:
        return node.module or ""
    parts = list(path.with_suffix("").parts)
    package = parts[parts.index("maktaba_pipeline") : -1]
    base = package[: len(package) - (node.level - 1)]
    return ".".join([*base, *((node.module or "").split(".") if node.module else [])])


@pytest.mark.unit
@pytest.mark.parametrize("path", MODULES, ids=lambda p: p.parent.name + "/" + p.name)
def test_the_stage_imports_nothing_that_could_reach_a_cloud(path: Path) -> None:
    tree = ast.parse(path.read_text("utf-8"))
    seen: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            seen.update(a.name for a in node.names)
        elif isinstance(node, ast.ImportFrom):
            module = _absolute(path, node)
            seen.add(module)
            if module == "maktaba_pipeline.llm":
                # only the home's door and its errors: no client of its own
                assert {a.name for a in node.names} <= DOOR_NAMES
            if module in {"maktaba_pipeline", "maktaba_pipeline.classify"}:
                assert {a.name for a in node.names} <= {"rules", "content_screen"}
    assert seen <= ALLOWED_STDLIB | ALLOWED_OWN, sorted(seen - ALLOWED_STDLIB - ALLOWED_OWN)
    # the address beside the model's name (rules.tagcheck.url) is never read, and no
    # address is written in the file
    for node in ast.walk(tree):
        if isinstance(node, ast.Attribute):
            assert node.attr not in {"url", "base_url", "endpoint", "api_key"}, node.lineno
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            assert "http://" not in node.value and "https://" not in node.value, node.lineno


class LocalWith:
    """This machine's runtimes, standing in: it has the models it is given."""

    def __init__(self, replies: dict[str, str]) -> None:
        self.replies = replies
        self.asked: list[tuple[str, str, dict[str, Any]]] = []
        self.runtimes = self

    async def ensure_fresh(self) -> None:
        return None

    def has(self, model: str) -> bool:
        return model in self.replies

    async def run(
        self, op: str, model: str, body: dict[str, Any], **_kw: Any
    ) -> AsyncIterator[dict[str, Any]]:
        self.asked.append((op, model, body))
        yield {"choices": [{"message": {"content": self.replies[model]}}]}


@pytest.fixture
def no_way_out(monkeypatch: pytest.MonkeyPatch) -> list[Any]:
    """Every connection to anything but this machine is refused, and counted."""
    refused: list[Any] = []
    real = socket.socket.connect

    def connect(self: socket.socket, address: Any) -> Any:
        host = address[0] if isinstance(address, tuple) else None
        if host is not None and host not in {"127.0.0.1", "::1", "localhost"}:
            refused.append(address)
            raise OSError(f"the content screen tried to leave the home: {address}")
        return real(self, address)

    monkeypatch.setattr(socket.socket, "connect", connect)
    return refused


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_stage_asks_the_homes_own_runtime_and_nothing_else(
    db: Database, monkeypatch: pytest.MonkeyPatch, no_way_out: list[Any]
) -> None:
    rt = await _engine(db)
    # the address a home may keep beside its model's name is not this stage's to use
    from tests.stages.world import set_rules

    await set_rules(db, "tagcheck", {"enabled": True, "model": MODEL,
                                     "url": "https://cloud.invalid/v1"})  # fmt: skip
    local = LocalWith({MODEL: answer(13, 0.9, coarse_language=(2, [0]))})
    monkeypatch.setattr(
        screen,
        "cluster_llm",
        lambda: ClusterLLM(local, lambda: None, refresh_local=False),  # type: ignore[arg-type]
    )
    assert stage.ASK is None  # the real asker: the door
    vid, _lib = await _video(db, "A film")
    await _transcript(db, vid, ["Words said in the film."])
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="content_screen")
    assert await drain_core(db, rt, only={"content_screen"}) == ["content_screen"]
    row = await _row(db, vid)
    assert row is not None and (row["tier"], row["model"]) == (13, MODEL)
    assert row["categories"]["coarse_language"] == {"level": 2, "evidence": [0.0]}
    # one question, to this machine's runtime, at temperature 0, with the transcript
    assert len(local.asked) == 1
    op, model, body = local.asked[0]
    assert (op, model, body["temperature"]) == ("chat", MODEL, 0)
    assert "Words said in the film." in json.dumps(body["messages"])
    assert no_way_out == []


@pytest.mark.integration
@pytest.mark.asyncio
async def test_with_no_runtime_and_no_channel_nothing_is_asked_or_written(
    db: Database, monkeypatch: pytest.MonkeyPatch, no_way_out: list[Any]
) -> None:
    rt = await _engine(db)
    local = LocalWith({})  # this machine has no model, and there is no channel to the home
    monkeypatch.setattr(
        screen,
        "cluster_llm",
        lambda: ClusterLLM(local, lambda: None, refresh_local=False),  # type: ignore[arg-type]
    )
    assert not await screen.model_reachable(MODEL)
    vid, lib = await _video(db, "A film")
    await _transcript(db, vid, ["Words said in the film."])
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="content_screen")
    await drain_core(db, rt, only={"content_screen"})
    # no fallback to anything else: no detected tier at all
    assert await _row(db, vid) is None and local.asked == [] and no_way_out == []
    assert await db.fetchval(
        "SELECT state FROM processing_jobs WHERE stage = 'content_screen' AND subject_id = $1",
        str(vid),
    ) == "done"  # fmt: skip
    stage._NO_MODEL_AT.clear()
    assert (await stage.plan_library(db, lib)).summary["detail"] == "not reachable"


# --- the pure parts ---------------------------------------------------------------------------


@pytest.mark.unit
def test_windows_keep_every_line_once_with_its_time() -> None:
    segs = [(i * 4.0, i * 4.0 + 3, f"line {i} " + "x" * 90) for i in range(50)]
    spans = screen.windows(segs, 1000)
    assert len(spans) > 3 and all(len(w.text) <= 1000 for w in spans)
    assert "\n".join(w.text for w in spans).count("line ") == 50
    assert spans[0].start == 0.0 and spans[-1].end == 49 * 4.0 + 3
    assert all(a.end <= b.start for a, b in zip(spans, spans[1:], strict=False))
    assert spans[1].text.startswith(f"[{int(spans[1].start)}] line ")
    # blank lines are not sent; a transcript of nothing is no window
    assert screen.windows([(0, 1, ""), (1, 2, "   ")]) == []
    # one line longer than a window is still one window
    assert len(screen.windows([(0, 1, "y" * 5000)], 1000)) == 1


@pytest.mark.unit
def test_combine_takes_the_strictest_window_passing_the_line() -> None:
    A = screen.Answer
    assert screen.combine([A(0, 0.9), A(18, 0.5), A(13, 0.85)], 0.8).tier == 13
    assert screen.combine([A(0, 0.9), A(18, 0.8), A(13, 0.85)], 0.8).tier == 18
    # two windows of one tier: the surer one speaks
    assert screen.combine([A(13, 0.82), A(13, 0.95)], 0.8).confidence == 0.95
    # none passes: the strictest guess, with the confidence that keeps it from counting
    low = screen.combine([A(9, 0.4), A(16, 0.6), A(None, 0.99)], 0.8)
    assert (low.tier, low.confidence) == (16, 0.6)
    none = screen.combine([A(None, 0.9), A(None, 0.3)], 0.8)
    assert none.tier is None
    assert screen.combine([], 0.8).tier is None
    # categories: the highest level of any window, its times first, three at most
    got = screen.combine(
        [
            A(9, 0.9, {"violence": {"level": 1, "evidence": [5.0]}}),
            A(13, 0.9, {"violence": {"level": 3, "evidence": [70.0, 80.0, 90.0, 95.0]}}),
        ],
        0.8,
    )
    assert got.categories["violence"]["level"] == 3
    assert got.categories["violence"]["evidence"][0] == 70.0
    assert len(got.categories["violence"]["evidence"]) == 3


@pytest.mark.unit
def test_music_is_the_strongest_thing_a_video_is() -> None:
    assert screen.is_music("music_video", {})
    assert screen.is_music(None, {"topic:nasheed": 0.9, "topic:family": 0.3})
    assert screen.is_music("episode", {"topic:quran_recitation": 0.8, "format:lecture": 0.9})
    assert screen.is_music(None, {"format:recitation": 0.7, "topic:tafsir": 0.9})
    assert not screen.is_music(None, {"topic:tafsir": 0.9, "topic:quran_recitation": 0.4})
    assert not screen.is_music("movie", {})
