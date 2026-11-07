"""The seeded library of the file-writing waves' parity proofs (Epic 54, story 54.29): real
files under a temporary library root, their thumbnails beside them or in ``.thumbs/``,
chapters and transcripts, and an ffmpeg stand-in whose output depends only on what it read,
so the in-process side and the plugin side can each write their files and be compared."""

from __future__ import annotations

import hashlib
import random
import uuid
from collections.abc import Iterator, Sequence
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import ProcResult, reset_runner, set_runner
from tests.stages.world import models_off

#: the stages each video's jobs start at (the drain runs what they lead to)
VIDEO_STAGES = ("thumb_small", "chapter_thumbs", "subtitle_gen")
JPEG = b"\xff\xd8\xff\xe0parity\xff\xd9"


class _Ids:
    """The same ids on both sides: uuid4 answers a fixed sequence while a side is seeded."""

    def __init__(self) -> None:
        self.rng = random.Random(5429)

    def __call__(self) -> uuid.UUID:
        return uuid.UUID(int=self.rng.getrandbits(128), version=4)


def _put(p: Path, data: bytes) -> None:
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(data)


@pytest.fixture
def ffmpeg() -> Iterator[None]:
    """A small JPEG and a gray frame that depend on the file read (never on the side)."""

    async def runner(args: list[str], timeout: float) -> ProcResult:
        src = args[args.index("-i") + 1] if "-i" in args else ""
        tag = hashlib.sha256(src.encode()).digest()
        if "image2pipe" in args:
            return ProcResult(JPEG + tag, b"", 0)
        if "rawvideo" in args:
            return ProcResult((tag * 3)[:72], b"", 0)
        return ProcResult(b"", b"unsupported in fake", 1)

    async def frames(args: list[str]) -> None:
        Path(args[-1]).write_bytes(JPEG + hashlib.sha256(" ".join(args[:-1]).encode()).digest())

    from maktaba_fp_thumbnails.stages import chapter_thumbs

    set_runner(runner)
    chapter_thumbs.set_frame_runner(frames)
    yield
    chapter_thumbs.reset_frame_runner()
    reset_runner()


def areas(root: Path) -> dict[str, Path]:
    """The folders the stages write: the library (subtitles beside the video) and the
    thumbnail cache (``maktaba_pipeline.thumbnail.small.ROOT``)."""
    return {"library": root / "lib", "data.thumbnails": root / "thumbs"}


def seeder(root: Path, monkeypatch: pytest.MonkeyPatch) -> Any:
    """Seeds one side: a library rooted at ``root/lib`` with four episodes of one show."""
    lib_dir = root / "lib"
    show = lib_dir / "دروس الفقه"
    for i in range(1, 5):
        _put(show / f"ep{i}.mp4", f"video {i}".encode() * 64)
    _put(show / "ep1.jpg", JPEG + b"1")  # beside the video
    _put(show / ".thumbs" / "ep2.jpg", JPEG + b"2")  # in the folder's .thumbs/
    _put(show / "ep3.webp", JPEG + b"3")  # ep4 has none: the poster, which is missing

    async def seed(db: Any) -> None:
        from tests.engine.factory import make_library, make_video

        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", _Ids())
            await models_off(db)
            lib = await make_library(db, "مكتبة الملفات")
            await db.execute("UPDATE libraries SET roots = $1 WHERE id = $2", [str(lib_dir)], lib)
            for i in range(1, 5):
                path = show / f"ep{i}.mp4"
                v = await make_video(
                    db, lib, filename=path.name, title=f"الحلقة {i}", path=str(path), size=64 * 7
                )
                if i <= 2:
                    for seq, start in ((0, 0.0), (1, 300.0), (2, 600.0)):
                        await db.execute(
                            "INSERT INTO chapters (video_id, seq, start_sec, title, source)"
                            " VALUES ($1, $2, $3, $4, 'inferred')",
                            str(v), seq, start, f"الباب {seq + 1}",
                        )  # fmt: skip
                if i <= 3:
                    await db.execute(
                        "INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, 0)", str(v)
                    )
                    track = await db.fetchval(
                        "SELECT id FROM audio_tracks WHERE video_id = $1", str(v)
                    )
                    tid = str(uuid.uuid4())
                    await db.execute(
                        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend,"
                        " model, is_active) VALUES ($1, $2, $3, 'ar', 'test', 'test', true)",
                        tid, str(v), track,
                    )  # fmt: skip
                    for seq, text in enumerate(("بسم الله", "الحمد لله رب العالمين", "قال")):
                        await db.execute(
                            "INSERT INTO transcript_segments (transcript_id, seq, start_sec,"
                            " end_sec, text) VALUES ($1, $2, $3, $4, $5)",
                            tid, seq, seq * 4.0, seq * 4.0 + 3.5, text,
                        )  # fmt: skip
                for stage in VIDEO_STAGES:
                    await enqueue_job(db, subject_kind="video", subject_id=v, stage=stage)

    return seed


async def prove(
    *,
    pg_url: str,
    template: str,
    plugin_id: str,
    stages: list[str],
    keys: dict[str, tuple[str, ...]],
    config: Path,
    root: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """One file-writing plugin's parity proof: rows, jobs, log lines, files, then N16."""
    from maktaba_plugin_sdk.parity import ParityConfig, files_tree

    from maktaba_pipeline.plugins import firstparty
    from maktaba_pipeline.thumbnail import small
    from tests.plugins.firstparty.harness import parity

    cfg = ParityConfig.load(config)
    where = areas(root)
    monkeypatch.setattr(small, "ROOT", str(where["data.thumbnails"]))
    for d in where.values():
        d.mkdir(parents=True, exist_ok=True)
    async with parity(
        pg_url,
        template,
        bundle=firstparty._raw(),
        stages=stages,
        seed=seeder(root, monkeypatch),
        # every area starts each side from the same files (a stage the wave did not move
        # writes too); all are compared
        files=where,
        monkeypatch=monkeypatch,
    ) as p:
        await p.run()
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in stages:
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = $1", plugin_id
        )
        assert n >= len(stages), f"only {n} jobs went through the plugin"
        tables = [(t, keys[t]) for t in cfg.tables]
        ignore: dict[str, Sequence[str]] = {k: list(v) for k, v in cfg.ignore.items()}
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:20])
        for area in cfg.files:
            base = files_tree(p.tmp / "files-base" / area)
            made = files_tree(p.tmp / "files-a" / area)
            assert set(made) - set(base), f"{area}: the stages wrote no file to compare"
        problems = await p.compare_n16(tables, ignore=ignore, stages=stages, perf=cfg.perf)
        slowest = p.slowest()
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
