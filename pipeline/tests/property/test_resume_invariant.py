"""Story 6.10 — synthetic crash/resume cycles preserve the resume invariant.

Property under test::

    For every job J that has progress, at every consistent read,
        max(transcript_segments WHERE transcript_id = J.transcript_id).end_sec
        == processing_jobs.last_segment_end_sec WHERE id = J.id.

The test runs an in-memory simulation: a fake stage emits segments,
randomly crashes, and is restarted from ``last_segment_end_sec``.
The simulation models the architecture §7.6 transactional contract
explicitly — segment INSERT and ``last_segment_end_sec`` UPDATE
inside the same transaction, so a crash always lands either before
the INSERT (no segment, no offset advance) or after the commit
(segment present, offset matches).

100 chaos cycles per run; deterministic seed so a regression
shows up on every CI execution rather than every Nth.
"""

from __future__ import annotations

import random
from dataclasses import dataclass, field

import pytest

from maktaba_pipeline.observability import default_registry, reset_default_registry


@dataclass(slots=True)
class _Segment:
    start_sec: float
    end_sec: float
    text: str


@dataclass(slots=True)
class _JobRow:
    """In-memory ``processing_jobs`` row for the simulated worker."""

    id: int
    state: str = "running"
    last_segment_end_sec: float = 0.0
    paused_at_sec: float | None = None
    total_duration_seconds: float = 0.0


@dataclass(slots=True)
class _FakeDB:
    """Tiny in-memory DB that enforces the schema CHECK constraint.

    The ``commit_segment`` method is the one transactional surface the
    simulated worker has — it inserts a segment AND advances the
    offset atomically (raising on CHECK violations) so a crash can
    never land between the two.
    """

    job: _JobRow
    segments: list[_Segment] = field(default_factory=list)

    def commit_segment(self, seg: _Segment) -> None:
        # Schema CHECK: 0 <= last_segment_end_sec <= total_duration_seconds.
        if seg.end_sec < 0:
            raise ValueError("segment end < 0")
        if seg.end_sec > self.job.total_duration_seconds:
            raise ValueError(
                f"segment end {seg.end_sec} > total {self.job.total_duration_seconds}"
            )
        self.segments.append(seg)
        self.job.last_segment_end_sec = seg.end_sec

    def pause(self) -> None:
        self.job.state = "paused"
        self.job.paused_at_sec = self.job.last_segment_end_sec

    def resume(self) -> None:
        self.job.state = "running"

    def mark_done(self) -> None:
        self.job.state = "done"


class _SyntheticTranscribeStage:
    """Emits segments at fixed cadence; randomly raises to simulate crashes."""

    def __init__(
        self,
        *,
        total_duration_sec: float,
        segment_dur_sec: float,
        crash_prob: float,
        rng: random.Random,
    ) -> None:
        self.total = total_duration_sec
        self.dur = segment_dur_sec
        self.crash_prob = crash_prob
        self.rng = rng
        self.cursor = 0.0

    def transcribe_one_segment(self) -> _Segment | None:
        if self.rng.random() < self.crash_prob:
            raise RuntimeError("synthetic crash")
        if self.cursor >= self.total:
            return None
        end = min(self.cursor + self.dur, self.total)
        seg = _Segment(start_sec=self.cursor, end_sec=end, text=f"@{self.cursor:.1f}")
        self.cursor = end
        return seg


def _run_until_paused_or_done(db: _FakeDB, stage: _SyntheticTranscribeStage) -> None:
    """One worker shift — runs until crash, pause, or completion."""
    stage.cursor = db.job.last_segment_end_sec
    db.resume()
    while True:
        try:
            seg = stage.transcribe_one_segment()
        except RuntimeError:
            db.pause()
            return
        if seg is None:
            db.mark_done()
            return
        db.commit_segment(seg)


def _assert_invariant(db: _FakeDB) -> None:
    """At any consistent read, the canonical offset matches the last segment."""
    if not db.segments:
        assert db.job.last_segment_end_sec == 0.0
        return
    last = max(db.segments, key=lambda s: s.end_sec)
    assert db.job.last_segment_end_sec == last.end_sec, (
        f"invariant violated: job.last_segment_end_sec="
        f"{db.job.last_segment_end_sec} but max(segments).end_sec="
        f"{last.end_sec}"
    )


@pytest.mark.parametrize("seed", [1, 17, 42])
def test_invariant_after_crash_resume(seed: int) -> None:
    rng = random.Random(seed)
    job = _JobRow(id=1, total_duration_seconds=600.0)
    db = _FakeDB(job=job)
    stage = _SyntheticTranscribeStage(
        total_duration_sec=600.0,
        segment_dur_sec=2.0,
        crash_prob=0.05,
        rng=rng,
    )

    for _ in range(100):
        _run_until_paused_or_done(db, stage)
        _assert_invariant(db)
        if db.job.state == "done":
            break

    # The job either eventually completed, or it's still paused — both
    # are fine. What matters is the invariant held at every restart.
    assert db.job.state in ("done", "paused")
    if db.job.state == "done":
        assert abs(db.job.last_segment_end_sec - 600.0) < 1e-9


def test_invariant_holds_when_no_segments_yet() -> None:
    job = _JobRow(id=2, total_duration_seconds=100.0)
    db = _FakeDB(job=job)
    _assert_invariant(db)


def test_check_constraint_rejects_segment_past_total() -> None:
    """The Python-side simulation models the DB CHECK from slot 0002.

    A backend that emits an end past ``total_duration_seconds`` must
    not corrupt the offset; the constraint rejects the INSERT and
    the offset stays where it was.
    """
    job = _JobRow(id=3, total_duration_seconds=100.0)
    db = _FakeDB(job=job)
    db.commit_segment(_Segment(0.0, 50.0, "first"))

    with pytest.raises(ValueError):
        db.commit_segment(_Segment(50.0, 101.0, "past total"))

    # The offset was not advanced by the rejected INSERT.
    assert db.job.last_segment_end_sec == 50.0
    _assert_invariant(db)


def test_observability_metric_reset_does_not_break_property() -> None:
    """The observability registry should be independent of the property test."""
    reset_default_registry()
    reg = default_registry()
    # Touching the registry must not affect the simulation above.
    reg.record_progress(stage="transcribe", realtime_factor=0.3)
    assert reg.job_realtime_factor.count(stage="transcribe") == 1
