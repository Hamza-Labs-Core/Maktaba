"""Where transcribe runs while the second listen is on (review of story 38.8, item 1): a
machine that cannot run it leaves the stage to those that can unless the rules let it,
its transcripts record that they lack it, and a machine that runs it queues them again,
backing off from those it cannot give it and stopping after a few tries.

The tests on a database run in the integration tier: the engine's drain takes longer than
the unit tier's 0.1 s (SQLite always; Postgres with ``MAKTABA_TEST_PG_URL``)."""

from __future__ import annotations

import json
from datetime import timedelta
from pathlib import Path
from typing import Any

import numpy as np
import pytest

from maktaba_pipeline.cluster.stages import StageSelector, refresh_stages
from maktaba_pipeline.engine.context import StageFailure
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.sql import dumps, now, parse_ts, to_ts
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.stt import cohere, engine, model_share
from maktaba_pipeline.stt.placement import TranscriptionConfig
from tests.stages.test_transcribe import FakeTranscriber, _one_video
from tests.stages.world import core_runtime, drain_core


@pytest.fixture(autouse=True)
def _defaults(monkeypatch: pytest.MonkeyPatch) -> None:
    for var in ("MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE", "MAKTABA_STT_REFINE_MODEL"):
        monkeypatch.delenv(var, raising=False)
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig())
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])


@pytest.fixture
def on_ct2(monkeypatch: pytest.MonkeyPatch) -> None:
    """A Windows or Linux worker that can run Whisper (CTranslate2), not the second listen."""
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    monkeypatch.setattr(tr, "_engine_unavailable", lambda: None)


# --- the stage waits for the machines that run the second listen ------------------------


@pytest.mark.unit
def test_a_machine_without_the_second_listen_leaves_transcribe_to_those_with_it(
    on_ct2: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    why = tr.unavailable()
    assert why is not None and "second listen does not run here" in why
    assert "without_second_listen" in why  # says how to let it
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig(without_second_listen=True))
    assert tr.unavailable() is None  # the rules let it
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig())
    monkeypatch.setenv("MAKTABA_STT_REFINE", "0")
    assert tr.unavailable() is None  # nothing to wait for
    # Whisper's own reasons come first
    monkeypatch.setattr(tr, "_engine_unavailable", lambda: "ctranslate2 is not installed")
    assert tr.unavailable() == "ctranslate2 is not installed"


@pytest.mark.unit
def test_apple_silicon_needs_mlx_audio_for_the_second_listen(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import importlib.util

    monkeypatch.setattr(engine, "current", lambda: engine.MLX)
    monkeypatch.setattr(tr, "_engine_unavailable", lambda: None)
    real = importlib.util.find_spec
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: object() if name == "mlx_audio" else real(name, *a),
    )
    assert tr.second_listen_problem() is None and tr.runs_second_listen()
    assert tr.unavailable() is None
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: None if name == "mlx_audio" else real(name, *a),
    )
    assert tr.second_listen_problem() == "mlx-audio is not installed (the stt extra)"
    assert "mlx-audio" in str(tr.unavailable())


async def _set_rule(db: Database, on: bool) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value) VALUES ('rules.transcription', $1)"
        " ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        dumps({"without_second_listen": on}),
    )


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_rule_is_read_before_each_selection(
    db: Database, on_ct2: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tr, "relisten", _no_relisten)
    selector = StageSelector(graph=GRAPH)
    await refresh_stages(db, GRAPH, ())
    selector.compute()
    assert selector.stages_off["transcribe"] == "unavailable"
    assert "second listen" in selector.detail["transcribe"]

    await _set_rule(db, True)  # Settings: Transcription, "Transcribe without the second listen"
    await refresh_stages(db, GRAPH, selector.served)
    assert selector.compute() and "transcribe" in selector.served

    await _set_rule(db, False)
    await refresh_stages(db, GRAPH, selector.served)
    assert selector.compute() and "transcribe" not in selector.served


async def _no_relisten(db: Any) -> int:
    return 0


@pytest.mark.integration
@pytest.mark.asyncio
async def test_an_unreadable_rule_keeps_the_last_one_read(
    db: Database, on_ct2: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    await _set_rule(db, True)
    await tr.refresh(db, False)
    assert tr._RULES.without_second_listen

    async def broken(db: Any, section: str) -> Any:
        raise ConnectionError("the home is away")

    monkeypatch.setattr("maktaba_pipeline.rules.read", broken)
    await refresh_stages(db, GRAPH, ())  # logged, not raised
    assert tr._RULES.without_second_listen


# --- the fingerprint records a skipped second listen -----------------------------------


@pytest.fixture
def fake(monkeypatch: pytest.MonkeyPatch) -> FakeTranscriber:
    f = FakeTranscriber()

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        return np.zeros(16000 * 6, np.float32)

    async def no_share(db: Any) -> Any:
        return None

    monkeypatch.setattr(tr, "decode_audio", decode)
    monkeypatch.setattr(tr, "get_transcriber", lambda settings: f)
    monkeypatch.setattr(tr, "use_vad", lambda: False)
    monkeypatch.setattr(model_share, "resolve_models_dir", no_share)
    return f


def _capable(monkeypatch: pytest.MonkeyPatch, here: bool) -> None:
    """This worker runs the second listen (``here``: a fake that corrects nothing) or not
    (it keeps Whisper's words and says why)."""
    monkeypatch.setattr(tr, "runs_second_listen", lambda: here)
    monkeypatch.setattr(tr, "second_listen_problem", lambda: None if here else "not here")

    def refine(result: Any, audio: Any, model: str, share: Any = None) -> dict[str, Any]:
        if here:
            return {"model": model, "source": "hf", "changed": 0}
        return {"model": model, "source": "skipped", "skipped": "RuntimeError: not here"}

    monkeypatch.setattr(tr, "_refine", refine)


async def _last_job(db: Database, vid: Any) -> Any:
    return await db.fetchrow(
        "SELECT id, state, input_hash, outcome, paused_reason FROM processing_jobs"
        " WHERE subject_id = $1 AND stage = 'transcribe' ORDER BY id DESC LIMIT 1",
        str(vid),
    )


async def _refine_source(db: Database, vid: Any) -> str:
    row = await db.fetchrow(
        "SELECT metadata FROM transcripts WHERE video_id = $1 AND is_active", str(vid)
    )
    meta = row["metadata"] if isinstance(row["metadata"], dict) else json.loads(row["metadata"])
    return str(meta["refine"]["source"])


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_transcript_without_its_second_listen_says_so_in_its_fingerprint(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vid, _ = await _one_video(db, tmp_path)
    _capable(monkeypatch, False)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 1 and await _refine_source(db, vid) == "skipped"
    fp = await tr.input_fingerprint(db, Subject("video", vid))
    assert fp is not None
    assert (await _last_job(db, vid))["input_hash"] == tr.skipped_fingerprint(fp) != fp

    # the same worker has nothing better to make: the transcript stands, still so recorded
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    job = await _last_job(db, vid)
    assert fake.calls == 1 and job["input_hash"] == tr.skipped_fingerprint(fp)

    # a worker that runs the second listen does not take it as done
    _capable(monkeypatch, True)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2 and await _refine_source(db, vid) == "hf"
    assert (await _last_job(db, vid))["input_hash"] == fp
    # ...and then it is done for everyone
    _capable(monkeypatch, False)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2


# --- a machine that runs the second listen queues the others again --------------------


async def _skipped_transcript(
    db: Database, rt: Any, tmp_path: Path, monkeypatch: pytest.MonkeyPatch, hours_ago: float
) -> Any:
    vid, _ = await _one_video(db, tmp_path)
    _capable(monkeypatch, False)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    await db.execute(
        "UPDATE transcripts SET created_at = $2 WHERE video_id = $1",
        str(vid),
        to_ts(db, now() - timedelta(hours=hours_ago)),
    )
    return vid


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_machine_with_the_second_listen_queues_the_transcripts_without_it(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    assert fake.calls == 1

    # only a worker that serves transcribe and runs the second listen sweeps...
    sweeps: list[bool] = []
    real = tr.relisten

    async def spy(db: Any) -> int:
        sweeps.append(True)
        return 0

    monkeypatch.setattr(tr, "relisten", spy)
    await tr.refresh(db, True)
    _capable(monkeypatch, True)
    await tr.refresh(db, False)
    assert sweeps == []
    await tr.refresh(db, True)
    assert sweeps == [True]
    monkeypatch.setattr(tr, "relisten", real)
    # ...once it has the model on its disk
    monkeypatch.setattr(tr, "_has_second_listen_model", lambda model: False)
    assert await tr.relisten(db) == 0
    monkeypatch.setattr(tr, "_has_second_listen_model", lambda model: True)
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    assert await tr.relisten(db) == 1
    job = await db.fetchrow(
        "SELECT state, priority, payload FROM processing_jobs WHERE subject_id = $1"
        " AND stage = 'transcribe' AND state = 'pending'",
        str(vid),
    )
    payload = job["payload"] if isinstance(job["payload"], dict) else json.loads(job["payload"])
    assert job["priority"] == tr.RELISTEN_PRIORITY and payload == {tr.RELISTEN: True}
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    assert await tr.relisten(db) == 0  # one job at a time per video
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2 and await _refine_source(db, vid) == "hf"
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    assert await tr.relisten(db) == 0  # nothing left without it


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_sweep_waits_between_rounds_and_for_recent_transcripts(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=0.2)
    _capable(monkeypatch, True)
    monkeypatch.setattr(tr, "_has_second_listen_model", lambda model: True)
    assert await tr.relisten(db) == 0  # made 12 minutes ago: not yet
    await db.execute(
        "UPDATE transcripts SET created_at = $1", to_ts(db, now() - timedelta(hours=2))
    )
    assert await tr.relisten(db) == 0  # the last round was just now
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    assert await tr.relisten(db) == 1


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_machine_without_the_second_listen_gives_a_relisten_job_back(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    await enqueue_job(
        db, subject_kind="video", subject_id=vid, stage="transcribe", payload={tr.RELISTEN: True}
    )
    await drain_core(db, rt, only={"transcribe"})  # on the incapable worker
    job = await _last_job(db, vid)
    assert fake.calls == 1 and job["state"] == "paused"
    assert "second listen" in str(job["paused_reason"])
    # a day on, nobody that runs it took it: the transcript stands
    await db.execute(
        "UPDATE processing_jobs SET state = 'pending', not_before = NULL, created_at = $2"
        " WHERE id = $1",
        job["id"],
        to_ts(db, now() - timedelta(seconds=tr.RELISTEN_WAIT_S + 60)),
    )
    await drain_core(db, rt, only={"transcribe"})
    job = await _last_job(db, vid)
    assert fake.calls == 1 and job["state"] == "done"


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_relisten_job_runs_on_a_transcript_recorded_before_the_mark(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A transcript made without its second listen before its job recorded it (its input
    hash the full one) still gets it from a relisten job."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    fp = await tr.input_fingerprint(db, Subject("video", vid))
    await db.execute(
        "UPDATE processing_jobs SET input_hash = $2 WHERE subject_id = $1", str(vid), fp
    )
    _capable(monkeypatch, True)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 1  # an ordinary job: the record says it is done
    await enqueue_job(
        db, subject_kind="video", subject_id=vid, stage="transcribe", payload={tr.RELISTEN: True}
    )
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2 and await _refine_source(db, vid) == "hf"


# --- a transcript the second listen cannot reach: backoff, turns, a stop said once ------


async def _tries(db: Database, vid: Any) -> Any:
    return await db.fetchrow(
        "SELECT attempts, last_job_id, last_at, next_at, stopped_at FROM relisten_attempts"
        " WHERE video_id = $1",
        str(vid),
    )


async def _sweep(db: Database, monkeypatch: pytest.MonkeyPatch) -> int:
    """A sweep now (the ten minutes between two of them gone)."""
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    return await tr.relisten(db)


async def _wait_over(db: Database, vid: Any) -> None:
    await db.execute(
        "UPDATE relisten_attempts SET next_at = $2 WHERE video_id = $1",
        str(vid),
        to_ts(db, now() - timedelta(seconds=1)),
    )


def _cannot_read(monkeypatch: pytest.MonkeyPatch) -> None:
    """The worker that runs the second listen cannot read the video's file."""

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        raise StageFailure("permission_denied", f"{path}: permission denied", retryable=False)

    monkeypatch.setattr(tr, "decode_audio", decode)


async def _notes(db: Database) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT job_id, message FROM job_events"
            " WHERE stage = 'transcribe' AND kind = 'step' AND level = 'warn'"
        )
    )


def _here(monkeypatch: pytest.MonkeyPatch) -> None:
    _capable(monkeypatch, True)
    monkeypatch.setattr(tr, "_has_second_listen_model", lambda model: True)


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_file_that_keeps_failing_backs_off_and_stops(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    _cannot_read(monkeypatch)
    for n in range(1, tr.RELISTEN_TRIES + 1):
        assert await _sweep(db, monkeypatch) == 1
        await drain_core(db, rt, only={"transcribe"})
        job = await _last_job(db, vid)
        assert job["state"] == "failed"
        tries = await _tries(db, vid)
        assert tries["attempts"] == n and tries["last_job_id"] == job["id"]
        # each wait twice the last (within the engine's jitter), at most a day
        step = min(tr.RELISTEN_BACKOFF_S * 2 ** (n - 1), tr.RELISTEN_BACKOFF_MAX_S)
        wait = (parse_ts(tries["next_at"]) - parse_ts(tries["last_at"])).total_seconds()  # type: ignore[operator]
        assert 0.74 * step <= wait <= 1.26 * step
        assert await _sweep(db, monkeypatch) == 0  # it waits
        await _wait_over(db, vid)
    # out of tries: no more jobs, said once, on the last one
    assert await _sweep(db, monkeypatch) == 0
    assert (await _tries(db, vid))["stopped_at"] is not None
    notes = await _notes(db)
    assert len(notes) == 1 and notes[0]["job_id"] == job["id"]
    assert f"{tr.RELISTEN_TRIES} tries" in notes[0]["message"]
    assert "permission denied" in notes[0]["message"]
    assert await _sweep(db, monkeypatch) == 0
    assert len(await _notes(db)) == 1
    assert fake.calls == 1  # Whisper never ran again


@pytest.mark.integration
@pytest.mark.asyncio
async def test_newer_transcripts_get_their_turn_while_an_old_one_backs_off(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vids = {}
    for name, hours in (("old", 5), ("newer", 2), ("newest", 1.5)):
        (tmp_path / name).mkdir()
        vids[name] = await _skipped_transcript(db, rt, tmp_path / name, monkeypatch, hours)
    _here(monkeypatch)
    _cannot_read(monkeypatch)
    monkeypatch.setattr(tr, "RELISTEN_BATCH", 1)

    async def turn() -> str:
        assert await _sweep(db, monkeypatch) == 1
        pending = [
            name for name, vid in vids.items() if (await _last_job(db, vid))["state"] == "pending"
        ]
        await drain_core(db, rt, only={"transcribe"})
        assert len(pending) == 1
        return pending[0]

    assert await turn() == "old"  # the oldest first
    assert await turn() == "newer"  # the old one waits
    await _wait_over(db, vids["old"])
    assert await turn() == "newest"  # untried before tried, however old
    assert await turn() == "old"
    assert await _sweep(db, monkeypatch) == 0  # the others wait


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_second_listen_clears_the_record(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)

    def no_model(result: Any, audio: Any, model: str, *_rest: Any) -> dict[str, Any]:
        return {"model": model, "source": "skipped", "skipped": "OSError: it did not load"}

    # the model does not load: the relisten job makes the transcript again without it
    monkeypatch.setattr(tr, "_refine", no_model)
    assert await _sweep(db, monkeypatch) == 1
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2 and await _refine_source(db, vid) == "skipped"
    assert (await _tries(db, vid))["attempts"] == 1  # the try counts

    # it loads now: the next try gives the transcript its second listen, the record goes
    _here(monkeypatch)
    await db.execute(
        "UPDATE transcripts SET created_at = $2 WHERE video_id = $1 AND is_active",
        str(vid),
        to_ts(db, now() - timedelta(hours=3)),
    )
    await _wait_over(db, vid)
    assert await _sweep(db, monkeypatch) == 1
    assert (await _tries(db, vid))["attempts"] == 2
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 3 and await _refine_source(db, vid) == "hf"
    assert await _tries(db, vid) is None


@pytest.mark.integration
@pytest.mark.asyncio
async def test_an_ordinary_run_starts_the_tries_over(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """An operator's run (or a changed file) makes a new transcript: the tries of the
    old one, even stopped ones, go with it."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    await db.execute(
        "INSERT INTO relisten_attempts (video_id, attempts, last_at, next_at, stopped_at)"
        " VALUES ($1, $2, $3, $3, $3)",
        str(vid),
        tr.RELISTEN_TRIES,
        to_ts(db, now()),
    )
    _here(monkeypatch)
    assert await _sweep(db, monkeypatch) == 0  # stopped

    def no_model(result: Any, audio: Any, model: str, *_rest: Any) -> dict[str, Any]:
        return {"model": model, "source": "skipped", "skipped": "OSError: it did not load"}

    monkeypatch.setattr(tr, "_refine", no_model)
    await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe", force=True)
    await drain_core(db, rt, only={"transcribe"})
    assert fake.calls == 2 and await _refine_source(db, vid) == "skipped"
    assert await _tries(db, vid) is None
    await db.execute(
        "UPDATE transcripts SET created_at = $2 WHERE video_id = $1 AND is_active",
        str(vid),
        to_ts(db, now() - timedelta(hours=3)),
    )
    assert await _sweep(db, monkeypatch) == 1  # tried again, from the first try
    assert (await _tries(db, vid))["attempts"] == 1


@pytest.mark.unit
def test_the_model_counts_as_here_once_loaded_or_on_disk(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(model_share, "hf_snapshot", lambda model: None)
    monkeypatch.setattr(model_share, "local_snapshots", lambda model: [])
    monkeypatch.setattr(cohere, "loaded", lambda model: False)
    assert not tr._has_second_listen_model("CohereLabs/x")
    assert tr._has_second_listen_model(str(tmp_path))  # a folder
    monkeypatch.setattr(model_share, "hf_snapshot", lambda model: tmp_path)
    assert tr._has_second_listen_model("CohereLabs/x")
    monkeypatch.setattr(model_share, "hf_snapshot", lambda model: None)
    monkeypatch.setattr(cohere, "loaded", lambda model: True)
    assert tr._has_second_listen_model("CohereLabs/x")
