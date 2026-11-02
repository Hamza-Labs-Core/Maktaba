"""Extract only when transcribe asks for it (2026-11), and the WAV's lifetime.

The probe no longer queues extract on the engine: transcribe asks for it
(``may_request``) only when the configured STT backend reads a file (openai-api).
A WAV that was read is released with its ``audio_cache`` row; what is left over is
swept. The extract jobs already queued on the home (pending and paused) finish as
no-ops on a streaming backend.
"""

from __future__ import annotations

import os
import time
from datetime import timedelta
from functools import partial
from pathlib import Path
from typing import Any
from uuid import UUID

import numpy as np
import pytest

from maktaba_pipeline.audio.extract import sweep_audio_cache
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.sql import now, to_ts
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.handlers import extract_handler, probe_handler
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt.batched import Sentence, Transcript
from tests.engine.factory import make_library, make_video
from tests.stages.world import core_runtime, drain_core

LINES = [(0.5, 2.0, "بسم الله الرحمن الرحيم")]


class _Whisper:
    def __init__(self) -> None:
        self.calls = 0

    async def transcribe(self, audio: Any, **_: Any) -> Transcript:
        self.calls += 1
        return Transcript(
            sentences=[Sentence(a, b, t, -0.1, 0.0) for a, b, t in LINES],
            audio_sec=len(audio) / 16000,
            language="ar",
            model="fake-whisper",
            gpu_sec=1.0,
        )


@pytest.fixture
def decoded(monkeypatch: pytest.MonkeyPatch) -> list[str]:
    """What the transcribe stage decoded (the paths), with a fake Whisper."""
    paths: list[str] = []

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        paths.append(path)
        return np.zeros(16000 * 4, np.float32)

    whisper = _Whisper()
    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: whisper)
    monkeypatch.setattr(tr, "use_vad", lambda: False)
    monkeypatch.setattr(tr, "refine_model", lambda: None)
    return paths


async def _video_with_audio(db: Database, tmp_path: Path, name: str = "درس 01.mp4") -> UUID:
    lib = await make_library(db)
    media = tmp_path / name
    media.write_bytes(b"\x00" * 64)
    vid = await make_video(db, lib, filename=name, path=str(media))
    for idx, lang, default in ((0, "eng", True), (1, "ara", False)):
        await db.execute(
            "INSERT INTO audio_tracks (video_id, track_index, codec, channels, sample_rate,"
            " language, is_default, disposition) VALUES ($1, $2, 'aac', 2, 48000, $3, $4, $5)",
            str(vid),
            idx,
            lang,
            default,
            f'{{"default": {int(default)}}}',
        )
    return vid


async def _track_id(db: Database, vid: UUID, index: int) -> int:
    got = await db.fetchval(
        "SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = $2", str(vid), index
    )
    return int(got)


async def _jobs(db: Database, stage: str) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT id, state, outcome, payload FROM processing_jobs WHERE stage = $1 ORDER BY id",
            stage,
        )
    )


async def _run_extract(db: Database, rt: EngineRuntime, handler: Any) -> int:
    """Claim and run every extract job (a legacy stage: drain_core leaves them)."""
    ran = 0
    while True:
        job = await claim_for_pool(
            db, worker_id="test", pool="nas_read", stages=["extract"], scope="global"
        )
        if job is None:
            return ran
        await dispatch_job(db, job, runtime=rt, legacy_handlers={"extract": handler})
        ran += 1


def _never_ffmpeg() -> Any:
    async def run_extract(*_a: object, **_k: object) -> Path:  # pragma: no cover
        raise AssertionError("no WAV is written for a streaming backend")

    return run_extract


# ------------------------------------------------------------------ streaming (live)


@pytest.mark.asyncio
async def test_a_streaming_backend_never_asks_for_extract(
    db: Database, tmp_path: Path, decoded: list[str]
) -> None:
    rt = await core_runtime(db)
    vid = await _video_with_audio(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    assert await drain_core(db, rt, only={"transcribe"}) == ["transcribe"]

    assert await _jobs(db, "extract") == []
    assert decoded == [str(tmp_path / "درس 01.mp4")]  # off the share
    assert await db.fetchval("SELECT count(*) FROM audio_cache") == 0


# Postgres only: the legacy probe's SQL is (``probed_at = now()``, and the FSM advance's
# ``FOR UPDATE``); tests/audio/test_probe.py covers chain_extract on both dialects' fakes
@pytest.mark.parametrize("db", ["postgres"], indirect=True)
@pytest.mark.asyncio
async def test_the_engine_probe_queues_no_extract(db: Database, tmp_path: Path) -> None:
    """The engine's probe handler (build_real_dispatch(engine=True)) leaves extract out."""
    from maktaba_pipeline.audio.probe import AudioTrack, MediaInfo, ProbeResult
    from maktaba_pipeline.handlers import build_real_dispatch

    rt = await core_runtime(db)
    lib = await make_library(db)
    vid = await make_video(db, lib, path=str(tmp_path / "a.mp4"))
    track = AudioTrack(0, "aac", 2, 48000, "ara", None, True, {"default": 1})

    async def ffprobe(path: str) -> ProbeResult:
        return ProbeResult(
            media=MediaInfo("mp4", "h264", 640, 360, 25.0, 900, False, {}), audio=[track]
        )

    probe = build_real_dispatch(engine=True)["probe"]
    assert probe is not probe_handler
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="probe")
    job = await claim_for_pool(db, worker_id="t", pool="nas_read", stages=["probe"], scope="global")
    assert job is not None
    handlers = {"probe": partial(probe_handler, run_probe=ffprobe, chain_extract=False)}
    await dispatch_job(db, job, runtime=rt, legacy_handlers=handlers)

    assert [j["state"] for j in await _jobs(db, "probe")] == ["done"]
    assert await _jobs(db, "extract") == []


# ------------------------------------------------------------------ a backend that reads a file


@pytest.mark.asyncio
async def test_a_file_backend_asks_for_the_wav_reads_it_and_releases_it(
    db: Database, tmp_path: Path, decoded: list[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tr, "needs_file", lambda: True)
    rt = await core_runtime(db)
    vid = await _video_with_audio(db, tmp_path)
    cache = tmp_path / "cache"
    written: list[tuple[str, int]] = []

    async def run_extract(path: str, track_index: int, *, content_hash: str) -> Path:
        written.append((path, track_index))
        cache.mkdir(exist_ok=True)
        dest = cache / f"{content_hash}.wav"
        dest.write_bytes(b"RIFF....WAVE")
        return dest

    extract = partial(extract_handler, run_extract=run_extract, requires_file=lambda: True)

    # 1. transcribe has no WAV: it waits and asks for extract
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    assert await drain_core(db, rt, only={"transcribe"}) == ["transcribe"]
    first = (await _jobs(db, "transcribe"))[0]
    assert (first["state"], first["outcome"]) == ("done", "waiting_inputs")
    assert decoded == []
    assert [j["state"] for j in await _jobs(db, "extract")] == ["pending"]

    # 2. extract writes the WAV of the Arabic track and queues transcribe again
    assert await _run_extract(db, rt, extract) == 1
    arabic = await _track_id(db, vid, 1)
    assert written == [(str(tmp_path / "درس 01.mp4"), 1)]
    row = await db.fetchrow("SELECT path, audio_track_id FROM audio_cache")
    assert row is not None and int(row["audio_track_id"]) == arabic
    wav = Path(str(row["path"]))
    assert wav.exists()

    # 3. transcribe reads the WAV, records its track, and releases it
    assert await drain_core(db, rt, only={"transcribe"}) == ["transcribe"]
    assert decoded == [wav.as_posix()]
    t = await db.fetchrow("SELECT audio_track_id FROM transcripts WHERE video_id = $1", str(vid))
    assert t is not None and int(t["audio_track_id"]) == arabic
    assert await db.fetchval("SELECT count(*) FROM audio_cache") == 0
    assert not wav.exists()
    assert [j["state"] for j in await _jobs(db, "extract")] == ["done"]  # asked once


@pytest.mark.asyncio
async def test_a_wav_written_on_another_machine_is_decoded_off_the_share(
    db: Database, tmp_path: Path, decoded: list[str], monkeypatch: pytest.MonkeyPatch
) -> None:
    """The row is there but the file is not on this machine: no second extract (that
    could go on forever), the share is decoded instead."""
    monkeypatch.setattr(tr, "needs_file", lambda: True)
    rt = await core_runtime(db)
    vid = await _video_with_audio(db, tmp_path)
    chash = await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(vid))
    await db.execute(
        "INSERT INTO audio_cache (content_hash, video_id, audio_track_id, path)"
        " VALUES ($1, $2, $3, '/elsewhere/x.wav')",
        chash,
        str(vid),
        await _track_id(db, vid, 1),
    )
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})

    assert decoded == [str(tmp_path / "درس 01.mp4")]
    assert await _jobs(db, "extract") == []


# ------------------------------------------------------------------ the live backlog


@pytest.mark.asyncio
async def test_the_queued_extract_backlog_drains_as_no_ops(db: Database, tmp_path: Path) -> None:
    """The home's ~1,300 extract jobs (pending, and paused by a stopped worker) after
    0172 resumes the stage: each finishes without ffmpeg, a file or an audio_cache row;
    a video already transcribed or queued for it gets no second transcribe."""
    rt = await core_runtime(db)
    vids = [await _video_with_audio(db, tmp_path, f"v{i}.mp4") for i in range(6)]
    for i, vid in enumerate(vids):
        res = await enqueue_job(db, subject_kind="video", subject_id=vid, stage="extract")
        if i % 2:
            await db.execute(
                "UPDATE processing_jobs SET state = 'paused', paused_at = $2,"
                " paused_reason = 'worker stopping' WHERE id = $1",
                res.job_id,
                to_ts(db, now()),
            )
    # vids[0] is already queued for transcription (content_hash's edge)
    await enqueue_job(db, subject_kind="video", subject_id=vids[0], stage="transcribe")
    # a stage paused and resumed through the API (0172's trigger wants an owner)
    await db.execute(
        "UPDATE job_stages SET paused = true, paused_at = $1, paused_by = 'test'"
        " WHERE stage = 'extract'",
        to_ts(db, now()),
    )
    assert await _run_extract(db, rt, extract_handler) == 0  # paused: nothing claimed
    await db.execute(
        "UPDATE job_stages SET paused = false, paused_at = NULL, paused_by = NULL"
        " WHERE stage = 'extract'"
    )

    handler = partial(extract_handler, run_extract=_never_ffmpeg(), requires_file=lambda: False)
    t0 = time.monotonic()
    assert await _run_extract(db, rt, handler) == 6
    assert time.monotonic() - t0 < 5

    assert {j["state"] for j in await _jobs(db, "extract")} == {"done"}
    assert await db.fetchval("SELECT count(*) FROM audio_cache") == 0
    transcribes = await db.fetch("SELECT video_id FROM processing_jobs WHERE stage = 'transcribe'")
    assert sorted(str(r["video_id"]) for r in transcribes) == sorted(map(str, vids))


# ------------------------------------------------------------------ the sweep


async def _cached(db: Database, vid: UUID, path: Path, *, age_h: float) -> str:
    chash = str(await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(vid)))
    path.write_bytes(b"RIFF")
    await db.execute(
        "INSERT INTO audio_cache (content_hash, video_id, audio_track_id, path, created_at)"
        " VALUES ($1, $2, $3, $4, $5)",
        chash,
        str(vid),
        await _track_id(db, vid, 0),
        path.as_posix(),
        to_ts(db, now() - timedelta(hours=age_h)),
    )
    return chash


def _age(path: Path, hours: float) -> None:
    t = time.time() - hours * 3600
    os.utime(path, (t, t))


@pytest.mark.asyncio
async def test_the_sweep_removes_what_nothing_will_read(db: Database, tmp_path: Path) -> None:
    await core_runtime(db)
    cache = tmp_path / "cache"
    cache.mkdir()
    old, busy, young, gone, kept = [
        await _video_with_audio(db, tmp_path, f"s{i}.mp4") for i in range(5)
    ]
    # rows: an old one nothing needs, one a transcribe still needs, a young one
    await _cached(db, old, cache / "old.wav", age_h=24)
    await _cached(db, busy, cache / "busy.wav", age_h=24)
    await enqueue_job(db, subject_kind="video", subject_id=busy, stage="transcribe")
    await _cached(db, young, cache / "young.wav", age_h=1)
    # files with no row: a video's (old: swept; new: kept), and one this database
    # does not know (another database's on the same machine: kept)
    for vid, age in ((gone, 24), (kept, 1)):
        h = await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(vid))
        f = cache / f"{h}.wav"
        f.write_bytes(b"RIFF")
        _age(f, age)
    stranger = cache / f"{'f' * 64}.wav"
    stranger.write_bytes(b"RIFF")
    _age(stranger, 24)

    report = await sweep_audio_cache(db, root=cache)

    assert (report.rows, report.files) == (1, 1)
    left = {p.name for p in cache.iterdir()}
    gone_h = await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(gone))
    kept_h = await db.fetchval("SELECT content_hash FROM videos WHERE id = $1", str(kept))
    assert "old.wav" not in left and f"{gone_h}.wav" not in left
    assert {"busy.wav", "young.wav", f"{kept_h}.wav", stranger.name} <= left
    rows = await db.fetch("SELECT video_id FROM audio_cache")
    assert sorted(str(r["video_id"]) for r in rows) == sorted([str(busy), str(young)])
