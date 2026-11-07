"""tv.maktaba.transcription does exactly what its stages did in-process (Epic 54, stories
54.26, 54.30): two episodes transcribed and listened to a second time on two copies of one
database, Whisper and the second listen in-process on one and through the plugin (the ml
runner, the real plugin API, its statements and its role) on the other. Whisper, the audio
decode and the second listen's model are fakes, the same on both sides (the runner runs in
this test's process); the rows each side stores are compared."""

from __future__ import annotations

import os
import uuid
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import numpy as np
import pytest
from maktaba_plugin_sdk.parity import ParityConfig

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.stt.placement import TranscriptionConfig
from maktaba_pipeline.stt.settings import Sentence, Transcript, Word
from tests.plugins.firstparty.harness import SameIds, parity
from tests.stages.world import models_off

pytestmark = [pytest.mark.integration]

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
HERE = Path(__file__).parent
#: the stages wave 3 moved into tv.maktaba.transcription (story 54.30)
STAGES = ["transcribe", "second_listen"]
KEYS = {
    "transcripts": ("id",),
    "transcript_segments": ("transcript_id", "seq"),
    "audio_tracks": ("video_id", "track_index"),
    "transcribe_checkpoints": ("video_id",),
    "relisten_attempts": ("video_id",),
}
LINES = [(0.5, 2.25, "السلام عليكم ورحمة الله"), (2.5, 5.0, "اليوم نتكلم عن السيرة النبوية")]


def selected() -> list[str]:
    want = [s for s in os.environ.get("MAKTABA_PARITY_STAGES", "").split(",") if s]
    return [s for s in STAGES if not want or s in want]


@pytest.fixture
def template(pg_template: str) -> str:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    if not selected():
        pytest.skip("no stage of tv.maktaba.transcription selected")
    return pg_template


class FakeWhisper:
    async def transcribe(self, audio: Any, **_kw: Any) -> Transcript:
        words = [
            [Word(a + i * 0.4, a + i * 0.4 + 0.3, w) for i, w in enumerate(t.split())]
            for a, _b, t in LINES
        ]
        return Transcript(
            sentences=[
                Sentence(a, b, t, -0.1, 0.0, words=ws)
                for (a, b, t), ws in zip(LINES, words, strict=True)
            ],
            audio_sec=len(audio) / 16000,
            language="ar",
            model="fake-whisper",
            gpu_sec=1.0,
        )


@pytest.fixture
def fakes(monkeypatch: pytest.MonkeyPatch) -> Iterator[SameIds]:
    from maktaba_fp_transcription.stages import second_listen as sl
    from maktaba_fp_transcription.stages import transcribe as tr

    from maktaba_pipeline.stt import model_share

    whisper = FakeWhisper()

    async def decode(path: str, timeout: float = 0, *, stream: int = 0) -> Any:
        return np.zeros(16000 * 6, np.float32)

    def refine(result: Transcript, audio: Any, model: str, *_a: Any, **_k: Any) -> Any:
        for s in result.sentences:
            s.text = "قال " + s.text
        return {"model": model, "source": "hf", "changed": len(result.sentences)}

    ids = SameIds(5430)
    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: whisper)
    monkeypatch.setattr(tr, "use_vad", lambda: False)
    monkeypatch.setattr(tr, "runs_second_listen", lambda: True)
    monkeypatch.setattr(tr, "second_listen_problem", lambda: None)
    monkeypatch.setattr(tr, "_refine", refine)
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig())
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])

    async def no_share(db: Any) -> Any:
        return None

    monkeypatch.setattr(model_share, "resolve_models_dir", no_share)
    monkeypatch.setattr(tr, "uuid4", ids)
    monkeypatch.setattr(sl, "uuid4", ids)
    for var in ("MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE", "MAKTABA_STT_REFINE_MODEL"):
        monkeypatch.delenv(var, raising=False)
    yield ids


def seeder(root: Path) -> Any:
    from tests.engine.factory import make_library, make_video

    show = root / "lib" / "دروس السيرة"
    show.mkdir(parents=True, exist_ok=True)

    async def seed(db: Any) -> None:
        with pytest.MonkeyPatch.context() as mp:
            mp.setattr(uuid, "uuid4", SameIds(5431))
            await models_off(db)
            lib = await make_library(db, "مكتبة التفريغ")
            await db.execute(
                "UPDATE libraries SET roots = $1 WHERE id = $2", [str(root / "lib")], lib
            )
            for i in (1, 2):
                media = show / f"درس {i:02d}.mp4"
                media.write_bytes(b"\x00" * 64)
                v = await make_video(db, lib, filename=media.name, path=str(media), size=64)
                await enqueue_job(db, subject_kind="video", subject_id=v, stage="transcribe")

    return seed


@pytest.mark.asyncio
async def test_transcription_wave_3_is_identical(
    template: str, fakes: SameIds, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.plugins import firstparty

    cfg = ParityConfig.load(HERE.parent / "parity.toml")
    async with parity(
        PG_URL,
        template,
        bundle=firstparty._raw(),
        stages=selected(),
        group="ml",
        seed=seeder(tmp_path),
        files={"library": tmp_path / "lib"},
        monkeypatch=monkeypatch,
    ) as p:
        await p.run(before_side=fakes.reset)
        assert p.a.ran == p.b.ran, "the two sides ran different jobs"
        for s in selected():
            assert s in p.b.ran, f"{s} never ran"
        n = await p.b.db.fetchval(
            "SELECT count(*) FROM plugin_invocations WHERE plugin_id = 'tv.maktaba.transcription'"
        )
        assert n >= len(selected()), f"only {n} jobs went through the plugin"
        tables = [(t, KEYS[t]) for t in cfg.tables]
        ignore = {k: list(v) for k, v in cfg.ignore.items()}
        rows = await p.compare(tables, ignore=ignore, perf=False)
        assert rows == [], "\n".join(rows[:30])
        # N16's bench runs each stage again on both sides in turns, with the ids running on:
        # the rows were compared above; only the timing is judged after it
        problems = await p.compare_n16(
            [], ignore=ignore, stages=selected(), perf=cfg.perf, timing_only=True
        )
        slowest = p.report("tv.maktaba.transcription")
    assert problems == [], "\n".join(problems[:20]) + "\nslowest plugin jobs:\n" + slowest
