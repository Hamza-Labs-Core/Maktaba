# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""A synthetic library for the stage tests: a YouTube-archive style channel."""

from __future__ import annotations

import random
import struct
import uuid
from dataclasses import dataclass, field
from datetime import date, timedelta
from typing import Any
from uuid import UUID

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.media import ProcResult, set_runner
from maktaba_pipeline.engine.sql import dumps, is_pg, to_date
from maktaba_pipeline.engine.sync import sync_graph
from tests.engine.factory import make_library, make_video


@dataclass
class World:
    library: UUID
    channel_id: str = "UCtest"
    episodes: list[UUID] = field(default_factory=list)
    loose: list[UUID] = field(default_factory=list)
    youtube: dict[UUID, str] = field(default_factory=dict)


async def add_channel(
    db: Any, library: UUID, channel_id: str = "UCtest", title: str = "Sheikh Test"
) -> None:
    await db.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url, title, status)"
        " VALUES ($1,$2,$3,$4,'ok') ON CONFLICT (channel_id) DO NOTHING",
        channel_id,
        str(library),
        f"https://youtube.com/channel/{channel_id}",
        title,
    )


async def add_youtube_video(
    db: Any,
    library: UUID,
    *,
    title: str,
    filename: str,
    upload: date,
    duration: float,
    channel_id: str = "UCtest",
    folder: str = "/lib/chan",
    link: bool = True,
) -> tuple[UUID, str]:
    """A local video plus its YouTube row, linked through media_metadata_enrichment."""
    vid = await make_video(
        db, library, filename=filename, title=None, duration=duration, path=f"{folder}/{filename}"
    )
    yt = "yt" + uuid.uuid4().hex[:9]
    await db.execute(
        "INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality,"
        " duration_s, tab, source) VALUES ($1,$2,$3,$4,'exact',$5,'videos','info_json')",
        yt,
        channel_id,
        title,
        to_date(db, upload),
        duration,
    )
    if link:
        await db.execute(
            "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
            " confidence, is_accepted) VALUES ($1,$2,'youtube',$3,$4,1.0,$5)",
            str(uuid.uuid4()),
            str(vid),
            yt,
            dumps({"link_method": "info_json"}),
            True if is_pg(db) else 1,
        )
    return vid, yt


async def make_show(
    db: Any,
    *,
    episodes: int = 12,
    loose: int = 3,
    name: str = "Stories of the Prophets",
    prefix: str = "show",
) -> World:
    """One real playlist of ``episodes`` equal-length episodes plus ``loose`` unrelated talks."""
    lib = await make_library(db)
    await add_channel(db, lib)
    w = World(library=lib)
    for i in range(episodes):
        vid, yt = await add_youtube_video(
            db,
            lib,
            title=f"{name} - Episode {i + 1}",
            filename=f"{prefix}_{i + 1:02d}.mp4",
            upload=date(2024, 3, 1) + timedelta(days=i),
            duration=1500.0 + i,
        )
        w.episodes.append(vid)
        w.youtube[vid] = yt
    for i in range(loose):
        vid, yt = await add_youtube_video(
            db,
            lib,
            title=f"Random talk {i}",
            filename=f"other_{i + 1:02d}.mp4",
            upload=date(2024, 3, 5) + timedelta(days=i * 20),
            duration=2400.0,
        )
        w.loose.append(vid)
        w.youtube[vid] = yt
    await set_playlist(db, "PL1", name, [(v, w.youtube[v]) for v in w.episodes])
    return w


async def set_playlist(db: Any, pid: str, title: str, members: list[tuple[UUID, str]]) -> None:
    await db.execute(
        "INSERT INTO youtube_playlists (playlist_id, channel_id, title, is_real, source)"
        " VALUES ($1,'UCtest',$2,$3,'info_json')"
        " ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title",
        pid,
        title,
        True if is_pg(db) else 1,
    )
    await db.execute("DELETE FROM youtube_playlist_items WHERE playlist_id = $1", pid)
    for pos, (_, yt) in enumerate(members, 1):
        await db.execute(
            "INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position) VALUES ($1,$2,$3)",
            pid,
            yt,
            pos,
        )


# --------------------------------------------------------------------------- engine


async def core_runtime(db: Any) -> EngineRuntime:
    await sync_graph(db, GRAPH)
    from maktaba_pipeline.engine.graph import bound_graph

    return EngineRuntime(graph={s.name: s for s in bound_graph()})


async def drain_core(
    db: Any,
    rt: EngineRuntime,
    *,
    limit: int = 500,
    only: set[str] | None = None,
    max_jobs: int | None = None,
) -> list[str]:
    """Run every claimable job; returns the stages run, in order.

    Debounce and back-off windows are honoured *while other work is claimable* (so triggers
    coalesce exactly as in production) and elapse only when the queue is otherwise idle.
    """
    ran: list[str] = []
    pools = {s.pool for s in rt.graph.values()}
    while len(ran) < limit:
        if max_jobs is not None and len(ran) >= max_jobs:
            return ran
        job = await _claim_any(db, rt, pools, only)
        if job is None:
            waiting = await db.fetchval(
                "SELECT count(*) FROM processing_jobs WHERE state = 'pending'"
                " AND not_before IS NOT NULL"
                + (
                    ""
                    if only is None
                    else " AND stage IN (" + ",".join(f"'{x}'" for x in only) + ")"
                )
            )
            if not waiting:
                return ran
            await db.execute(
                "UPDATE processing_jobs SET not_before = NULL WHERE state = 'pending'"
                + (
                    ""
                    if only is None
                    else " AND stage IN (" + ",".join(f"'{x}'" for x in only) + ")"
                )
            )
            continue
        await dispatch_job(db, job, runtime=rt, legacy_handlers={})
        ran.append(job.stage)
    raise AssertionError(f"did not quiesce after {limit} jobs: {ran[-20:]}")


async def _claim_any(
    db: Any, rt: EngineRuntime, pools: set[str], only: set[str] | None
) -> Job | None:
    for pool in sorted(pools):
        stages = [
            s.name
            for s in rt.graph.values()
            if s.pool == pool and not s.legacy and (only is None or s.name in only)
        ]
        if not stages:
            continue
        job = await claim_for_pool(
            db, worker_id="test", pool=pool, stages=stages, scope="per_worker"
        )
        if job is not None:
            return job
    return None


# --------------------------------------------------------------------------- fake ffmpeg


def _seed(family: str) -> int:
    return sum(ord(c) * (i + 3) for i, c in enumerate(family))


def make_fake_media(counter: dict[str, int] | None = None) -> None:
    """Install a runner whose chromaprint and frames depend only on the file's family
    (the filename up to the first underscore): files of one family 'share an intro'."""

    async def runner(args: list[str], timeout: float) -> ProcResult:
        path = args[args.index("-i") + 1] if "-i" in args else ""
        family = path.replace("\\", "/").rsplit("/", 1)[-1].split("_")[0]
        rng = random.Random(_seed(family))
        if counter is not None:
            counter["calls"] = counter.get("calls", 0) + 1
            counter[family] = counter.get(family, 0) + 1
        if "chromaprint" in args:
            data = struct.pack("<400I", *[rng.getrandbits(32) for _ in range(400)])
            return ProcResult(data, b"", 0)
        if "-filter_complex" in args:  # style sampling: stats on stderr, thumbnails on stdout
            return ProcResult(_thumbs(rng), _style_log(rng).encode(), 0)
        if "rawvideo" in args:
            return ProcResult(bytes(rng.getrandbits(8) for _ in range(72)), b"", 0)
        return ProcResult(b"", b"unsupported in fake", 1)

    set_runner(runner)


def _thumbs(rng: random.Random) -> bytes:
    return bytes(rng.getrandbits(8) for _ in range(64 * 36 * 40))


def _style_log(rng: random.Random) -> str:
    lines = [
        "Input #0, mov,mp4, from 'x.mp4':",
        "  Duration: 00:25:00.00, start: 0.000000, bitrate: 1200 kb/s",
        "  Stream #0:0[0x1](und): Video: h264, yuv420p, 1280x720, 1100 kb/s, 25 fps, 25 tbr",
    ]
    for n in range(40):
        t = n * 0.1
        lines.append(f"[Parsed_metadata_5 @ 0x1] frame:{n}   pts:{n * 4} pts_time:{t:.1f}")
        for k, v in (
            ("scd.score", rng.random() * 3),
            ("signalstats.YDIF", 1.0 + rng.random()),
            ("signalstats.YAVG", 90 + rng.random() * 40),
            ("signalstats.YLOW", 16),
            ("signalstats.YHIGH", 235),
            ("signalstats.SATAVG", 40 + rng.random() * 10),
            ("signalstats.UAVG", 120),
            ("signalstats.VAVG", 130),
        ):
            lines.append(f"[Parsed_metadata_5 @ 0x1] lavfi.{k}={v:.3f}")
    return "\n".join(lines)
