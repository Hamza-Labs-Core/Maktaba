"""transcribe on the CTranslate2 engine (story 38.8): the transcript names the engine, the
second listen keeps Whisper's words and says why, and a PC does not transcribe again
what a Mac did."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt import engine, model_share
from maktaba_pipeline.stt.settings import Transcript
from tests.stages.test_transcribe import FakeTranscriber, _one_video
from tests.stages.world import core_runtime, drain_core

VERSION = "faster-whisper 1.2.1, ctranslate2 4.8.3, cuda float16"


class CT2Fake(FakeTranscriber):
    """What the CTranslate2 transcriber returns: its engine and runtime with the words."""

    async def transcribe(self, audio: np.ndarray, **kw: Any) -> Transcript:
        t = await super().transcribe(audio, **kw)
        t.backend, t.backend_version = "ct2-batched", VERSION
        return t


@pytest.fixture
def fake(monkeypatch: pytest.MonkeyPatch) -> CT2Fake:
    f = CT2Fake()

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        return np.zeros(16000 * 6, np.float32)

    async def no_share(db: Any) -> Any:
        raise AssertionError("no second listen here: the share is not looked for")

    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: f)
    monkeypatch.setattr(tr, "use_vad", lambda: False)
    monkeypatch.setattr(model_share, "resolve_models_dir", no_share)
    for var in ("MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE", "MAKTABA_STT_REFINE_MODEL"):
        monkeypatch.delenv(var, raising=False)
    return f


@pytest.mark.asyncio
async def test_a_pc_s_transcript_names_its_engine_and_keeps_whisper_s_words(
    db: Database, tmp_path: Path, fake: CT2Fake, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    rt = await core_runtime(db)
    vid, _media = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    assert await drain_core(db, rt, only={"transcribe"}) == ["transcribe"]
    t = await db.fetchrow(
        "SELECT backend, backend_version, metadata FROM transcripts"
        " WHERE video_id = $1 AND is_active",
        str(vid),
    )
    assert t is not None and t["backend"] == "ct2-batched" and t["backend_version"] == VERSION
    meta = t["metadata"] if isinstance(t["metadata"], dict) else loads(t["metadata"])
    refine = meta["refine"]
    assert refine["source"] == "skipped" and "Apple Silicon (MLX) only" in refine["skipped"]


@pytest.mark.asyncio
async def test_a_pc_does_not_transcribe_again_what_a_mac_did(
    db: Database, tmp_path: Path, fake: CT2Fake, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setenv("MAKTABA_STT_REFINE", "0")
    rt = await core_runtime(db)
    vid, _media = await _one_video(db, tmp_path)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})  # on the Mac (the tests' engine)
    assert fake.calls == 1
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})  # on a PC: the same input, skipped
    assert fake.calls == 1
    # another Whisper is another input
    monkeypatch.setenv("MAKTABA_STT_MODEL", "Systran/faster-whisper-small")
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2
