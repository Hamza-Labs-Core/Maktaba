"""The ``repair_broken`` stage: broken downloads fetched from YouTube again, only when the rules
switch it on; no yt-dlp, ffmpeg, NAS or network."""

from __future__ import annotations

import json
import uuid
from collections.abc import Iterator
from dataclasses import dataclass
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.graph import GRAPH, POOLS
from maktaba_pipeline.engine.sql import dumps, now, to_ts
from maktaba_pipeline.engine.stages import repair_broken
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.extensions.harness import error_of, events, run_stage, setup_runtime
from tests.extensions.harness import summary_of as summary

BROKEN = b"broken"


@dataclass
class Proc:
    returncode: int = 0
    stdout: bytes = b""
    stderr: bytes = b""


class Tools:
    """``subprocess.run``: ffprobe fails on a file that holds ``broken``; yt-dlp writes a good
    file unless its id is in ``unavailable``."""

    def __init__(self) -> None:
        self.calls: list[list[str]] = []
        self.unavailable: set[str] = set()

    def downloads(self) -> list[str]:
        return [c[-1].rsplit("=", 1)[1] for c in self.calls if "yt_dlp" in c]

    def __call__(self, args: list[str], **kw: Any) -> Proc:
        self.calls.append(list(args))
        if "yt_dlp" in args:
            yid = args[-1].rsplit("=", 1)[1]
            if yid in self.unavailable:
                return Proc(1, b"", b"ERROR: Video unavailable\n")
            Path(args[args.index("-o") + 1].replace("%(ext)s", "mp4")).write_bytes(b"good video")
            return Proc()
        if Path(args[-1]).read_bytes() == BROKEN:
            return Proc(1, b"", b"moov atom not found")
        doc = {"format": {"duration": "300"}, "streams": [{"width": 1280, "height": 720}]}
        return Proc(0, json.dumps(doc).encode())


@pytest.fixture
def tools() -> Iterator[Tools]:
    t = Tools()
    previous = repair_broken.set_runner(t)
    yield t
    repair_broken.set_runner(previous)


async def set_rules(db: Any, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        "rules.repair",
        dumps(value),
        to_ts(db, now()),
    )


async def add_video(
    db: Any, lib: UUID, folder: Path, name: str, content: bytes, yid: str | None, probe: str
) -> UUID:
    f = folder / name
    f.write_bytes(content)
    v = await make_video(db, lib, filename=name, path=str(f))
    if yid:
        await db.execute(
            "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
            " confidence, is_accepted) VALUES ($1,$2,'youtube',$3,'{}',1.0,$4)",
            str(uuid.uuid4()),
            str(v),
            yid,
            True,
        )
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, state) VALUES ($1,'probe',$2)",
        str(v),
        probe,
    )
    return v


@dataclass
class World:
    lib: UUID
    folder: Path
    repaired: UUID
    readable: UUID
    no_id: UUID
    gone: UUID
    fine: UUID


async def world(db: Any, tmp_path: Path) -> World:
    lib = await make_library(db, "YT")
    return World(
        lib,
        tmp_path,
        await add_video(db, lib, tmp_path, "a.mp4", BROKEN, "yt000000001", "failed"),
        await add_video(db, lib, tmp_path, "b.mp4", b"fine now", "yt000000002", "failed"),
        await add_video(db, lib, tmp_path, "c.mp4", BROKEN, None, "failed"),
        await add_video(db, lib, tmp_path, "d.mp4", BROKEN, "yt000000004", "failed"),
        await add_video(db, lib, tmp_path, "e.mp4", b"fine", "yt000000005", "done"),
    )


async def state_of(db: Any, video: UUID) -> Any:
    return await db.fetchrow(
        "SELECT state, message, backup_path, attempts FROM video_repairs WHERE video_id = $1",
        str(video),
    )


async def repair_job(db: Any, rt: Any, lib: UUID, **kw: Any) -> Any:
    return await run_stage(db, rt, "repair_broken", "library", lib, **kw)


def test_the_stage_has_its_own_pool_and_writes_to_the_share() -> None:
    s = next(s for s in GRAPH if s.name == "repair_broken")
    assert s.pool == "repairs" and s.external_write and s.entry_for == ()  # run by an operator
    assert s.edges == () and set(s.may_request) == {"probe", "content_hash"}
    assert "repairs" in {p.pool for p in POOLS}


@pytest.mark.asyncio
async def test_it_is_off_until_the_rules_enable_it(
    db: Database, tools: Tools, tmp_path: Path
) -> None:
    w = await world(db, tmp_path)
    rt = await setup_runtime(db, [])
    job = await repair_job(db, rt, w.lib)
    assert job["state"] == "done" and summary(job)["enabled"] is False
    assert tools.calls == [] and (tmp_path / "a.mp4").read_bytes() == BROKEN
    assert await db.fetchval("SELECT count(*) FROM video_repairs") == 0


@pytest.mark.asyncio
async def test_each_failed_probe_is_read_again_then_repaired_or_reported(
    db: Database, tools: Tools, tmp_path: Path
) -> None:
    w = await world(db, tmp_path)
    tools.unavailable.add("yt000000004")
    await set_rules(db, {"enabled": True})
    rt = await setup_runtime(db, [])
    job = await repair_job(db, rt, w.lib)
    assert job["state"] == "done" and job["outcome"] == "changed", error_of(job)
    s = summary(job)
    assert (s["broken"], s["repaired"], s["readable"], s["no_id"], s["failed"]) == (4, 1, 1, 1, 1)

    assert tools.downloads() == ["yt000000001", "yt000000004"]  # b reads fine, c has no id
    assert (tmp_path / "a.mp4").read_bytes() == b"good video"
    assert (tmp_path / "a.mp4.broken").read_bytes() == BROKEN  # kept beside the new one
    for name in ("c.mp4", "d.mp4"):
        assert (tmp_path / name).read_bytes() == BROKEN  # never replaced on a guess
    assert (tmp_path / "b.mp4").read_bytes() == b"fine now"

    assert (await state_of(db, w.repaired))["state"] == "repaired"
    assert (await state_of(db, w.repaired))["backup_path"].endswith("a.mp4.broken")
    assert (await state_of(db, w.readable))["state"] == "readable"
    assert "no YouTube id" in (await state_of(db, w.no_id))["message"]
    failed = await state_of(db, w.gone)
    assert failed["state"] == "failed" and "Video unavailable" in failed["message"]
    assert await state_of(db, w.fine) is None  # its probe did not fail

    # the new file is looked at again; so is the one that read fine
    asked = {
        (str(r["video_id"]), r["stage"])
        for r in await db.fetch(
            "SELECT video_id, stage FROM processing_jobs WHERE stage IN ('probe','content_hash')"
            " AND state = 'pending'"
        )
    }
    assert (str(w.repaired), "probe") in asked and (str(w.repaired), "content_hash") in asked
    assert (str(w.readable), "probe") in asked and (str(w.no_id), "probe") not in asked


@pytest.mark.asyncio
async def test_the_broken_copy_can_be_dropped_and_a_run_bounded(
    db: Database, tools: Tools, tmp_path: Path
) -> None:
    w = await world(db, tmp_path)
    await set_rules(db, {"enabled": True, "keep_broken_copy": False, "max_per_run": 1})
    rt = await setup_runtime(db, [])
    job = await repair_job(db, rt, w.lib)
    assert summary(job)["broken"] == 1 and (tmp_path / "a.mp4").read_bytes() == b"good video"
    assert not (tmp_path / "a.mp4.broken").exists()
    assert (await state_of(db, w.repaired))["backup_path"] is None


@pytest.mark.asyncio
async def test_review_mode_downloads_nothing_until_an_operator_applies(
    db: Database, tools: Tools, tmp_path: Path
) -> None:
    w = await world(db, tmp_path)
    await set_rules(db, {"enabled": True})
    rt = await setup_runtime(db, [])
    await db.execute("UPDATE job_stages SET write_mode = 'review' WHERE stage = 'repair_broken'")
    job = await repair_job(db, rt, w.lib)
    assert job["outcome"] == "awaiting_review" and tools.calls == []
    (parked,) = await events(db, job["id"], "review")
    assert len(parked["changes"]["changed"]) == 4 and (tmp_path / "a.mp4").read_bytes() == BROKEN


@pytest.mark.asyncio
async def test_a_repaired_file_is_not_downloaded_again(
    db: Database, tools: Tools, tmp_path: Path
) -> None:
    w = await world(db, tmp_path)
    await set_rules(db, {"enabled": True, "max_per_run": 1})
    rt = await setup_runtime(db, [])
    await repair_job(db, rt, w.lib)
    again = await repair_job(db, rt, w.lib)
    # the repaired file's probe is asked again, not failed any more: nothing left to fetch
    assert again["state"] == "done" and tools.downloads() == ["yt000000001"]
