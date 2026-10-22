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

    def refine(result: Any, audio: Any, model: str, *_rest: Any) -> dict[str, Any]:
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
        "SELECT attempts, last_job_id, last_at, next_at, ended_at, wait_s, stopped_at, model,"
        " relisten_round FROM relisten_attempts WHERE video_id = $1",
        str(vid),
    )


async def _sweep(db: Database, monkeypatch: pytest.MonkeyPatch) -> int:
    """A sweep now (the ten minutes between two of them gone)."""
    monkeypatch.setattr(tr, "_LAST_RELISTEN", [])
    return await tr.relisten(db)


async def _wait_over(db: Database, vid: Any) -> None:
    """The try is over and so is its wait."""
    past = to_ts(db, now() - timedelta(seconds=1))
    await db.execute(
        "UPDATE relisten_attempts SET ended_at = COALESCE(ended_at, $2), next_at = $2"
        " WHERE video_id = $1",
        str(vid),
        past,
    )


def _cannot_read(monkeypatch: pytest.MonkeyPatch, share: str = "") -> None:
    """The worker that runs the second listen cannot read the video's file (on ``share``,
    a folder its path starts with)."""

    async def decode(path: str, timeout: float = 0) -> np.ndarray:
        raise StageFailure(
            "permission_denied", f"{share}{path}: permission denied", retryable=False
        )

    monkeypatch.setattr(tr, "decode_audio", decode)


async def _notes(db: Database) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT job_id, message FROM job_events"
            " WHERE stage = 'transcribe' AND kind = 'step' AND level = 'warn'"
        )
    )


def _here(monkeypatch: pytest.MonkeyPatch, capability: str = "this machine") -> None:
    """This worker runs the second listen, has its model, and its capability is
    ``capability``."""
    _capable(monkeypatch, True)
    monkeypatch.setattr(tr, "_has_second_listen_model", lambda model: True)
    monkeypatch.setattr(tr, "second_listen_capability", lambda: (capability, {"host": capability}))


def _rules(monkeypatch: pytest.MonkeyPatch, **values: Any) -> None:
    monkeypatch.setattr(tr, "_RULES", TranscriptionConfig(**values))


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_file_that_keeps_failing_backs_off_and_stops(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    _cannot_read(monkeypatch)
    rules = tr._RULES
    for n in range(1, rules.relisten_tries + 1):
        assert await _sweep(db, monkeypatch) == 1
        assert (await _tries(db, vid))["ended_at"] is None  # its job is still to run
        await drain_core(db, rt, only={"transcribe"})
        job = await _last_job(db, vid)
        assert job["state"] == "failed"
        assert await _sweep(db, monkeypatch) == 0  # it waits, from the end of the try
        tries = await _tries(db, vid)
        assert tries["attempts"] == n and tries["last_job_id"] == job["id"]
        assert parse_ts(tries["ended_at"]) == parse_ts(job_end := await _finished(db, job["id"]))
        # each wait twice the last (within the engine's jitter), at most the rule's longest
        step = min(rules.relisten_wait_hours * 2 ** (n - 1), rules.relisten_wait_max_hours) * 3600
        wait = (parse_ts(tries["next_at"]) - parse_ts(job_end)).total_seconds()  # type: ignore[operator]
        assert 0.74 * step <= wait <= 1.26 * step
        await _wait_over(db, vid)
    # out of tries: no more jobs, said once, on the last one
    assert await _sweep(db, monkeypatch) == 0
    assert (await _tries(db, vid))["stopped_at"] is not None
    notes = await _notes(db)
    assert len(notes) == 1 and notes[0]["job_id"] == job["id"]
    assert f"{rules.relisten_tries} tries" in notes[0]["message"]
    assert "(permission_denied): permission denied" in notes[0]["message"]
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
    _here(monkeypatch)
    await _stopped(db, vid)
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


async def _finished(db: Database, job_id: int) -> Any:
    return await db.fetchval("SELECT finished_at FROM processing_jobs WHERE id = $1", job_id)


async def _seen(db: Database, kind: str, key: str) -> None:
    """The home has seen this ``kind`` (model, capability) of second listen before."""
    await db.execute(
        "INSERT INTO relisten_seen (kind, key, detail, first_seen) VALUES ($1, $2, '{}', $3)"
        " ON CONFLICT (kind, key) DO NOTHING",
        kind,
        key,
        to_ts(db, now()),
    )


async def _stopped(
    db: Database, vid: Any, capability: str = "this machine", attempts: int | None = None
) -> None:
    """``vid``'s tries ran out (the default number; or ``attempts`` made, not stopped), and
    this machine's capability and model are ones the home knows."""
    await _seen(db, "capability", capability)
    await _seen(db, "model", str(tr.refine_model()))
    at = to_ts(db, now())
    stopped = attempts is None
    await db.execute(
        "INSERT INTO relisten_attempts (video_id, attempts, last_at, next_at, ended_at,"
        " stopped_at, model) VALUES ($1, $2, $3, $3, $3, $4, $5)"
        " ON CONFLICT (video_id) DO UPDATE SET attempts = EXCLUDED.attempts,"
        " stopped_at = EXCLUDED.stopped_at, ended_at = EXCLUDED.ended_at,"
        " next_at = EXCLUDED.next_at",
        str(vid),
        TranscriptionConfig().relisten_tries if stopped else attempts,
        at,
        at if stopped else None,
        tr.refine_model(),
    )


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_stop_note_keeps_the_reason_under_a_long_path(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Follow-up of #221: the note cut the reason when the path came first (a share on
    Windows, a long temp folder): the reason comes first now, the path last."""
    rt = await core_runtime(db)
    await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    _cannot_read(monkeypatch, share="\\\\nas\\library\\" + "a long folder name\\" * 12)
    _rules(monkeypatch, relisten_tries=1)
    assert await _sweep(db, monkeypatch) == 1
    await drain_core(db, rt, only={"transcribe"})
    assert await _sweep(db, monkeypatch) == 0
    notes = await _notes(db)
    assert len(notes) == 1
    message = notes[0]["message"]
    assert "1 tries" in message and "no more tries" in message
    assert "its last job failed (permission_denied): permission denied (\\\\nas" in message


@pytest.mark.integration
@pytest.mark.asyncio
async def test_the_wait_counts_from_the_end_of_the_try(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Follow-up of #221: a try whose job waited hours in the queue had its wait used up
    by then."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=8)
    _here(monkeypatch)
    _cannot_read(monkeypatch)
    assert await _sweep(db, monkeypatch) == 1
    # queued five hours ago, it ran ten minutes ago
    await db.execute(
        "UPDATE relisten_attempts SET last_at = $2 WHERE video_id = $1",
        str(vid),
        to_ts(db, now() - timedelta(hours=5)),
    )
    await drain_core(db, rt, only={"transcribe"})
    job = await _last_job(db, vid)
    ran = now() - timedelta(minutes=10)
    await db.execute(
        "UPDATE processing_jobs SET finished_at = $2 WHERE id = $1", job["id"], to_ts(db, ran)
    )
    assert await _sweep(db, monkeypatch) == 0  # an hour from its end, not from its queueing
    tries = await _tries(db, vid)
    assert abs((parse_ts(tries["ended_at"]) - ran).total_seconds()) < 1  # type: ignore[operator]
    assert parse_ts(tries["next_at"]) > now() + timedelta(minutes=30)  # type: ignore[operator]
    assert 0.74 * 3600 <= float(tries["wait_s"]) <= 1.26 * 3600
    # the rules set the wait
    await _wait_over(db, vid)
    _rules(monkeypatch, relisten_wait_hours=0.5, relisten_wait_max_hours=0.5)
    assert await _sweep(db, monkeypatch) == 1
    assert 0.74 * 1800 <= float((await _tries(db, vid))["wait_s"]) <= 1.26 * 1800


@pytest.mark.integration
@pytest.mark.asyncio
async def test_stopped_transcripts_get_their_tries_again_when_something_changes(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Follow-up of #221: a stopped video came back only with an ordinary run. Now a
    second-listen model or a capability (a new machine, GPU, driver, packages) the home has
    not seen before, or the operator's next round, give it its tries again: once each."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch, capability="pc-a")
    await _stopped(db, vid, capability="pc-a")
    assert await _sweep(db, monkeypatch) == 0  # nothing changed

    async def queued_again(why: str) -> None:
        tries = await _tries(db, vid)
        assert tries["stopped_at"] is None and tries["attempts"] == 1, why
        await db.execute("DELETE FROM processing_jobs WHERE subject_id = $1", str(vid))
        await _stopped(db, vid, capability="pc-a")

    # another machine, or this one with a new GPU or new packages
    _here(monkeypatch, capability="pc-b")
    assert await _sweep(db, monkeypatch) == 1
    await queued_again("a capability the home had not seen")
    _here(monkeypatch, capability="pc-a")
    assert await _sweep(db, monkeypatch) == 0  # seen before: no ping-pong between machines
    _here(monkeypatch, capability="pc-b")
    assert await _sweep(db, monkeypatch) == 0

    # a model the home has not seen
    monkeypatch.setenv("MAKTABA_STT_REFINE_MODEL", "someone/a-better-model")
    assert await _sweep(db, monkeypatch) == 1
    assert (await _tries(db, vid))["model"] == "someone/a-better-model"
    await queued_again("a new model")
    assert await _sweep(db, monkeypatch) == 0

    # the operator's next round (Settings, Transcription)
    _rules(monkeypatch, relisten_round=1)
    assert await _sweep(db, monkeypatch) == 1
    assert (await _tries(db, vid))["relisten_round"] == 1
    await queued_again("the operator's round")
    assert await _sweep(db, monkeypatch) == 0  # once a round


@pytest.mark.integration
@pytest.mark.asyncio
async def test_machines_with_different_models_never_reset_each_other(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review of #228, N1: a worker reset every video, tries in progress too, to its own
    MAKTABA_STT_REFINE_MODEL, so two machines that name the model differently (a folder,
    the repo id) reset each other every sweep, and the tries never ran out. A model resets
    the stopped tries once, the first time the home sees it, and only those."""
    rt = await core_runtime(db)
    (tmp_path / "stopped").mkdir()
    (tmp_path / "trying").mkdir()
    stopped = await _skipped_transcript(db, rt, tmp_path / "stopped", monkeypatch, 3)
    trying = await _skipped_transcript(db, rt, tmp_path / "trying", monkeypatch, 3)
    _here(monkeypatch, capability="pc-a")
    await _seen(db, "capability", "pc-b")
    names = {"pc-a": "CohereLabs/cohere-transcribe-arabic-07-2026", "pc-b": "D:/models/cohere"}
    for name in names.values():
        await _seen(db, "model", name)
    monkeypatch.setenv("MAKTABA_STT_REFINE_MODEL", names["pc-a"])
    await _stopped(db, stopped, capability="pc-a")
    await _stopped(db, trying, capability="pc-a", attempts=3)
    await db.execute(
        "UPDATE relisten_attempts SET next_at = $2 WHERE video_id = $1",
        str(trying),
        to_ts(db, now() + timedelta(hours=4)),  # it waits
    )
    for _ in range(3):  # the two machines' sweeps, by turns
        for machine, name in names.items():
            _here(monkeypatch, capability=machine)
            monkeypatch.setenv("MAKTABA_STT_REFINE_MODEL", name)
            assert await _sweep(db, monkeypatch) == 0, machine
    assert (await _tries(db, stopped))["stopped_at"] is not None
    assert (await _tries(db, trying))["attempts"] == 3  # its count stands
    # a model no machine has used before: the stopped ones, once
    monkeypatch.setenv("MAKTABA_STT_REFINE_MODEL", "CohereLabs/cohere-transcribe-arabic-11-2026")
    assert await _sweep(db, monkeypatch) == 1
    assert (await _tries(db, stopped))["attempts"] == 1
    assert (await _tries(db, trying))["attempts"] == 3
    _here(monkeypatch, capability="pc-b")
    monkeypatch.setenv("MAKTABA_STT_REFINE_MODEL", names["pc-b"])
    assert await _sweep(db, monkeypatch) == 0


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_reset_between_the_choice_and_the_stop_stands(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review of #228, N3: the stop checked only stopped_at IS NULL, so a reset made while
    a worker was stopping the video (another worker's, the operator's round) was undone."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    await _stopped(db, vid, attempts=TranscriptionConfig().relisten_tries)  # out of tries
    real = tr._last_try

    async def meanwhile(db_: Any, job_id: Any, metadata: Any) -> str:
        # another worker's reset, between this one's choice and its stop
        await db.execute(
            "UPDATE relisten_attempts SET attempts = 0, stopped_at = NULL WHERE video_id = $1",
            str(vid),
        )
        return await real(db_, job_id, metadata)

    monkeypatch.setattr(tr, "_last_try", meanwhile)
    await _sweep(db, monkeypatch)
    tries = await _tries(db, vid)
    assert tries["stopped_at"] is None and await _notes(db) == []


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_worker_from_before_slot_0146_waits_too(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review of #228, N4: a try was recorded with next_at = its queueing, which a worker
    from before this change (it knows no ended_at) took as "due" the moment the job ended.
    next_at is the wait from now meanwhile; the end of the try moves it on."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    _cannot_read(monkeypatch)
    assert await _sweep(db, monkeypatch) == 1
    tries = await _tries(db, vid)
    ahead = (parse_ts(tries["next_at"]) - parse_ts(tries["last_at"])).total_seconds()  # type: ignore[operator]
    assert ahead == pytest.approx(float(tries["wait_s"]), abs=1)
    await drain_core(db, rt, only={"transcribe"})
    # the sweep of #221 (slot 0145): its choice of the videos due
    old = await db.fetch(
        "SELECT t.video_id FROM transcripts t"
        " LEFT JOIN relisten_attempts r ON r.video_id = t.video_id"
        f" WHERE t.is_active AND {tr._skipped_sql(db)}"
        " AND (r.video_id IS NULL OR (r.stopped_at IS NULL AND r.attempts < $1"
        " AND r.next_at <= $2))",
        6,
        to_ts(db, now()),
    )
    assert old == []  # not due for it either
    assert await _sweep(db, monkeypatch) == 0  # nor for this one, its wait from the end


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_try_without_room_on_the_gpu_is_no_try(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """#209's follow-up: a relisten job whose worker had no room for the second listen
    counted as one of its video's tries. The job now goes back (paused, its transcript as
    it was), and a job that ended without running (given back until it was a day old) is
    no try either."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    before = await db.fetchval(
        "SELECT id FROM transcripts WHERE video_id = $1 AND is_active", str(vid)
    )

    def no_room(result: Any, audio: Any, model: str, *_rest: Any) -> dict[str, Any]:
        return {"model": model, "source": "skipped", "skipped": "NoRoom: full", "no_room": True}

    monkeypatch.setattr(tr, "_refine", no_room)
    assert await _sweep(db, monkeypatch) == 1
    await drain_core(db, rt, only={"transcribe"})
    job = await _last_job(db, vid)
    assert job["state"] == "paused" and "second listen" in str(job["paused_reason"])
    assert (
        await db.fetchval("SELECT id FROM transcripts WHERE video_id = $1 AND is_active", str(vid))
        == before
    )
    # nobody with room took it within a day: it ends without running, and is no try
    _capable(monkeypatch, False)
    await db.execute(
        "UPDATE processing_jobs SET state = 'pending', not_before = NULL, created_at = $2"
        " WHERE id = $1",
        job["id"],
        to_ts(db, now() - timedelta(seconds=tr.RELISTEN_WAIT_S + 60)),
    )
    await drain_core(db, rt, only={"transcribe"})
    assert (await _last_job(db, vid))["outcome"] == "skipped_unchanged"
    _here(monkeypatch)
    await _sweep(db, monkeypatch)
    assert (await _tries(db, vid))["attempts"] == 0


class _ReadBeforeAReset:
    """The database as a worker's sweep saw it a moment ago: the tries it reads still say 1
    (another worker has reset them to 0 since)."""

    def __init__(self, db: Database) -> None:
        self._db = db

    def __getattr__(self, name: str) -> Any:
        return getattr(self._db, name)

    async def fetch(self, sql: str, *args: Any) -> list[Any]:
        rows = await self._db.fetch(sql, *args)
        if "FROM relisten_attempts r" not in sql:
            return rows
        return [{**dict(zip(r.keys(), r, strict=True)), "attempts": 1} for r in rows]


@pytest.mark.integration
@pytest.mark.asyncio
async def test_a_try_that_was_no_try_never_takes_the_count_below_zero(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review of #228 (round 2): the uncounting of a try that never ran used the count read
    earlier in the sweep; a reset in between made it -1, against the table's CHECK."""
    rt = await core_runtime(db)
    vid = await _skipped_transcript(db, rt, tmp_path, monkeypatch, hours_ago=3)
    _here(monkeypatch)
    assert await _sweep(db, monkeypatch) == 1  # a try, its job still to run
    job = await _last_job(db, vid)
    # it ended without running, and another worker reset the video's tries meanwhile
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', outcome = 'skipped_unchanged',"
        " finished_at = $2 WHERE id = $1",
        job["id"],
        to_ts(db, now()),
    )
    await db.execute("UPDATE relisten_attempts SET attempts = 0 WHERE video_id = $1", str(vid))
    assert await tr._settle_tries(_ReadBeforeAReset(db), tr._RULES) == 1
    tries = await _tries(db, vid)
    assert tries["attempts"] == 0 and tries["ended_at"] is not None


@pytest.mark.unit
def test_the_capability_names_the_gpu_and_a_steady_machine(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Review of #228, N2: the key had no GPU, while the help promises that a new GPU brings
    the stopped transcripts back; and on a Mac (no MachineGuid) a new host name was a new
    machine."""
    from maktaba_pipeline.stt import ct2
    from maktaba_pipeline.stt.ct2 import GpuInfo

    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    monkeypatch.setattr(tr, "stable_machine_id", lambda: "4C4C4544-0042-3510-8051-B4C04F4E3732")
    gpus = {0: GpuInfo("NVIDIA GeForce RTX 3080 Ti", 12.0, 9.0, (8, 6), "591.44")}
    monkeypatch.setattr(ct2, "gpu_info", lambda index: gpus.get(index))
    key, detail = tr.second_listen_capability()
    assert detail["gpu"] == {
        "name": "NVIDIA GeForce RTX 3080 Ti", "memory_gb": 12, "compute": "8.6", "driver": 591
    }  # fmt: skip
    assert "4C4C4544" not in str(detail)  # the id itself is not stored
    monkeypatch.setattr("socket.gethostname", lambda: "renamed-pc")
    assert tr.second_listen_capability()[0] == key  # a rename is no new machine
    gpus[0] = GpuInfo("NVIDIA GeForce RTX 4090", 24.0, 20.0, (8, 9), "591.44")
    assert tr.second_listen_capability()[0] != key  # a new GPU is
    gpus[0] = GpuInfo("NVIDIA GeForce RTX 3080 Ti", 12.0, 9.0, (8, 6), "600.10")
    assert tr.second_listen_capability()[0] != key  # and so is a new driver
    monkeypatch.setenv("MAKTABA_STT_DEVICE", "cuda:1")
    assert tr.second_listen_capability()[1]["gpu"] is None  # Whisper's GPU: none at 1


@pytest.mark.unit
def test_a_mac_s_id_is_its_platform_uuid(monkeypatch: pytest.MonkeyPatch) -> None:
    import subprocess
    import sys

    calls: list[list[str]] = []

    def ioreg(args: list[str], **kw: Any) -> subprocess.CompletedProcess[str]:
        calls.append(args)
        assert kw.get("shell") is not True
        out = '  | |   "IOPlatformUUID" = "564D8E6A-2E7B-4C1A-9F5E-1B2C3D4E5F60"\n'
        return subprocess.CompletedProcess(args, 0, out, "")

    monkeypatch.delenv("MAKTABA_MACHINE_ID", raising=False)
    monkeypatch.setattr(sys, "platform", "darwin")  # the module the stage reads it from
    monkeypatch.setattr(subprocess, "run", ioreg)
    tr.stable_machine_id.cache_clear()
    try:
        assert tr.stable_machine_id() == "564D8E6A-2E7B-4C1A-9F5E-1B2C3D4E5F60"
        assert calls == [["/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice"]]
    finally:
        tr.stable_machine_id.cache_clear()


@pytest.mark.integration
@pytest.mark.asyncio
async def test_deleted_and_missing_videos_are_not_tried(
    db: Database, tmp_path: Path, fake: FakeTranscriber, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Follow-up of #221: the sweep queued deleted and missing videos too, and kept their
    records."""
    rt = await core_runtime(db)
    vids = {}
    for name in ("deleted", "confirmed missing", "missing", "superseded", "here"):
        (tmp_path / name).mkdir()
        vids[name] = await _skipped_transcript(db, rt, tmp_path / name, monkeypatch, 3)
    _here(monkeypatch)
    for vid in vids.values():
        await db.execute(
            "INSERT INTO relisten_attempts (video_id, attempts, last_at, next_at, ended_at)"
            " VALUES ($1, 1, $2, $2, $2)",
            str(vid),
            to_ts(db, now() - timedelta(hours=2)),
        )
    at = to_ts(db, now())
    await db.execute("UPDATE videos SET deleted_at = $2 WHERE id = $1", str(vids["deleted"]), at)
    await db.execute(
        "UPDATE videos SET missing_confirmed_at = $2 WHERE id = $1",
        str(vids["confirmed missing"]),
        at,
    )
    for state in ("missing", "superseded"):
        await db.execute("UPDATE videos SET state = $2 WHERE id = $1", str(vids[state]), state)
    assert await _sweep(db, monkeypatch) == 1
    for name, vid in vids.items():
        if name == "here":
            assert (await _tries(db, vid))["attempts"] == 2
            assert (await _last_job(db, vid))["state"] == "pending"
        else:
            assert await _tries(db, vid) is None, name  # its record goes
            assert (await _last_job(db, vid))["state"] == "done", name  # not queued


@pytest.mark.unit
def test_the_relisten_rules_have_their_limits() -> None:
    from maktaba_pipeline import settings

    fields = {f["name"]: f for f in settings.describe(TranscriptionConfig())}
    assert fields["relisten_tries"]["default"] == 6 and fields["relisten_tries"]["minimum"] == 1
    assert fields["relisten_wait_hours"]["default"] == 1.0
    assert fields["relisten_wait_max_hours"]["default"] == 24.0
    assert fields["relisten_round"]["minimum"] == 0
    for f in fields.values():
        assert f["group"] == "Transcription" and f["help"], f["name"]


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
