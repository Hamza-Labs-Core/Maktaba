"""The job tracker: job events, the running set, claims per minute."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any

import pytest

from maktaba_pipeline.cluster.agent.jobs import JobTracker, failure

pytestmark = pytest.mark.unit


@dataclass
class FakeJob:
    id: int
    stage: str = "transcribe"
    subject_kind: str = "video"
    subject_id: str | None = "v-1"
    attempts: int = 1
    started_at: datetime | None = None


class Clock:
    def __init__(self) -> None:
        self.t = 100.0

    def __call__(self) -> float:
        return self.t


def _tracker() -> tuple[JobTracker, list[dict[str, Any]], Clock]:
    clock = Clock()
    t = JobTracker(clock=clock)
    seen: list[dict[str, Any]] = []
    t.add_sink(seen.append)
    return t, seen, clock


def test_lifecycle_events_have_the_frame_shape() -> None:
    t, seen, clock = _tracker()
    started = datetime(2026, 10, 19, 1, 2, 3, tzinfo=UTC)
    t.claimed(FakeJob(7, started_at=started), title="Episode 1")
    clock.t += 0.5
    t.progress(7, step="decode", done=10, total=40)
    clock.t += 2.0
    assert t.finished(7, "done", output={"audio_sec": 12.5, "segments": 3})
    assert [e["event"] for e in seen] == ["claimed", "progress", "done"]
    claimed, prog, done = seen
    for e in seen:
        assert e["t"] == "job"
        assert e["job_id"] == 7 and e["stage"] == "transcribe"
        assert e["subject_kind"] == "video" and e["subject_id"] == "v-1"
        assert e["title"] == "Episode 1" and e["attempt"] == 1
        assert e["started_at"] == "2026-10-19T01:02:03.000Z"
        assert e["ts"].endswith("Z")
    assert prog["progress"] == {"step": "decode", "done": 10, "total": 40}
    assert claimed["duration_ms"] is None and claimed["outcome"] is None
    assert done["duration_ms"] == 2500
    assert done["output"] == {"audio_sec": 12.5, "segments": 3}


def test_progress_is_throttled_to_one_a_second_per_job() -> None:
    t, seen, clock = _tracker()
    t.claimed(FakeJob(1))
    t.claimed(FakeJob(2))
    for _ in range(5):
        t.progress(1, step="s", done=1, total=2)
        t.progress(2, step="s", done=1, total=2)
        clock.t += 0.3
    progress = [e for e in seen if e["event"] == "progress"]
    # each job: at 0, then once more after 1 s
    assert [e["job_id"] for e in progress] == [1, 2, 1, 2]
    t.progress(1, step="s", done=2, total=2, force=True)
    assert seen[-1]["progress"]["done"] == 2


def test_an_end_is_reported_once_and_only_for_running_jobs() -> None:
    t, seen, _ = _tracker()
    assert not t.finished(5, "done")  # never claimed here
    t.claimed(FakeJob(5))
    out = failure("ffmpeg", "x" * 2000, retryable=True)
    assert t.finished(5, "failed", outcome=out)
    assert not t.finished(5, "failed")  # the state hook and the engine both report it
    assert [e["event"] for e in seen] == ["claimed", "failed"]
    assert seen[-1]["outcome"]["kind"] == "ffmpeg"
    assert seen[-1]["outcome"]["retryable"] is True
    assert len(seen[-1]["outcome"]["error"]) == 500


def test_running_set_and_claims_per_minute() -> None:
    t, _, clock = _tracker()
    t.claimed(FakeJob(1), title="A")
    t.progress(1, step="s", done=1, total=4)
    clock.t += 30
    t.claimed(FakeJob(2, stage="probe"))
    running = {r["job_id"]: r for r in t.running()}
    assert set(running) == {1, 2}
    assert running[1]["progress"] == {"step": "s", "done": 1, "total": 4}
    assert running[1]["subject"] == "video:v-1" and running[1]["title"] == "A"
    assert running[2]["progress"] is None
    assert t.claims_per_min() == 2
    clock.t += 31
    assert t.claims_per_min() == 1
    t.finished(1, "done")
    assert [r["job_id"] for r in t.running()] == [2]


def test_a_failing_sink_never_fails_the_job() -> None:
    t, seen, _ = _tracker()

    def boom(_: dict[str, Any]) -> None:
        raise RuntimeError("sink down")

    t.add_sink(boom)
    t.claimed(FakeJob(1))
    assert t.finished(1, "done")
    assert [e["event"] for e in seen] == ["claimed", "done"]
    assert t.errors == 2
    t.remove_sink(boom)
