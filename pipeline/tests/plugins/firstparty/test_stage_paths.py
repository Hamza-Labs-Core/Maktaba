"""A plugin stage opens a video's file through the job's path map.

The plugin runner hands each job its path map on the context (``ctx.local_path``); it
never sets the pipeline's process-wide map (``engine.paths.to_local``). A moved stage that
called the process-wide one opened the stored path unmapped: on a home whose share is
mounted elsewhere (``media.path_map``), every thumbnail failed as a missing file.
"""

from __future__ import annotations

import ast
from pathlib import Path
from types import SimpleNamespace
from typing import Any
from uuid import uuid4

import pytest
from maktaba_fp_core_media.stages import probe
from maktaba_fp_thumbnails.stages import thumbnail
from maktaba_fp_transcription.stages import extract

from maktaba_pipeline.engine import paths
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.paths import PathMap

FIRST_PARTY = Path(__file__).resolve().parents[4] / "plugins" / "first-party"
STORED = "/Volumes/Originals/Backup/a.mp4"
MAPPED = "/Users/x/nas-nfs/Backup/a.mp4"


def test_no_first_party_stage_uses_the_process_wide_path_map() -> None:
    found = []
    for src in sorted(FIRST_PARTY.glob("*/src/**/*.py")):
        for node in ast.walk(ast.parse(src.read_text(encoding="utf-8"))):
            if isinstance(node, ast.ImportFrom) and node.module == "maktaba_pipeline.engine.paths":
                found += [
                    f"{src.relative_to(FIRST_PARTY)}: {a.name}"
                    for a in node.names
                    if a.name in ("to_local", "active_path_map")
                ]
    assert found == [], "use ctx.local_path (the job's path map): " + ", ".join(found)


class _DB:
    async def fetchrow(self, sql: str, *args: Any) -> dict[str, Any]:
        return {"path": STORED, "content_hash": "c" * 64, "duration_sec": 10.0}


def _ctx() -> StageContext:
    job = SimpleNamespace(id=1, video_id=uuid4())
    return StageContext(
        db=_DB(),
        job=job,  # type: ignore[arg-type]
        stage=SimpleNamespace(name="t"),  # type: ignore[arg-type]
        path_map=PathMap(((("/Volumes/Originals/", "/Users/x/nas-nfs/")),)),
    )


@pytest.fixture
def opened(monkeypatch: pytest.MonkeyPatch) -> list[str]:
    # the process-wide map is empty, as in the plugin runner
    monkeypatch.setattr(paths, "_ACTIVE", PathMap())
    seen: list[str] = []

    async def record(path: str, *a: Any, **k: Any) -> Any:
        seen.append(path)
        raise RuntimeError("stop after the open")

    async def no_chapters(*a: Any, **k: Any) -> list[float]:
        return []

    async def a_track(*a: Any, **k: Any) -> Any:
        return SimpleNamespace(db_id=1, track=SimpleNamespace(index=0))

    monkeypatch.setattr(probe, "ffprobe", record)
    monkeypatch.setattr(thumbnail, "generate", record)
    monkeypatch.setattr(thumbnail, "_load_chapter_starts", no_chapters)
    monkeypatch.setattr(extract, "decode", record)
    monkeypatch.setattr(extract, "load_selected_track", a_track)
    monkeypatch.setattr(extract, "requires_file", lambda: True)
    return seen


@pytest.mark.asyncio
@pytest.mark.parametrize("stage", [probe, thumbnail, extract], ids=lambda m: m.__name__)
async def test_a_stage_opens_the_mapped_path(stage: Any, opened: list[str]) -> None:
    ctx = _ctx()
    with pytest.raises((StageFailure, RuntimeError)):
        await stage.run(ctx.db, ctx.job, ctx)
    assert opened == [MAPPED]
