"""Story 9.6 — manual scan progress reporting + ?rehash mode."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.manual_scan import (
    PROGRESS_TICK_HZ,
    ProgressReporter,
    ScanMode,
    ScanProgress,
    count_files,
    should_rehash,
)


@pytest.mark.unit
def test_count_files_uses_supplied_walker() -> None:
    def walker(root: str) -> list[str]:
        return ["a", "b", "c"] if root == "/x" else []

    assert count_files(["/x", "/empty"], walker) == 3


@pytest.mark.unit
def test_should_rehash_default_only_when_fast_path_missed() -> None:
    assert not should_rehash(ScanMode.DEFAULT, fast_path_match=True)
    assert should_rehash(ScanMode.DEFAULT, fast_path_match=False)


@pytest.mark.unit
def test_should_rehash_force_always_rehashes() -> None:
    assert should_rehash(ScanMode.REHASH, fast_path_match=True)
    assert should_rehash(ScanMode.REHASH, fast_path_match=False)


@pytest.mark.unit
def test_scan_progress_fraction_caps_at_one() -> None:
    p = ScanProgress(job_id=1, files_scanned=1500, files_to_scan=1000, started_at=0.0)
    assert p.fraction == 1.0


@pytest.mark.unit
def test_scan_progress_fraction_zero_when_total_zero() -> None:
    p = ScanProgress(job_id=1, files_scanned=0, files_to_scan=0, started_at=0.0)
    assert p.fraction == 0.0


@pytest.mark.asyncio
async def test_progress_reporter_coalesces_within_tick_window() -> None:
    sink_calls: list[ScanProgress] = []

    async def sink(p: ScanProgress) -> None:
        sink_calls.append(p)

    clock = iter([0.0, 0.1, 0.2, 0.3, 1.5, 1.6])
    reporter = ProgressReporter(
        job_id=42,
        files_to_scan=100,
        sink=sink,
        clock=lambda: next(clock),
    )
    # First three calls are within the 1 s window — only one flush expected.
    await reporter.note_file("a")
    await reporter.note_file("b")
    await reporter.note_file("c")
    assert len(sink_calls) == 1
    # Fourth call crosses the window → second flush.
    await reporter.note_file("d")
    assert len(sink_calls) == 2


@pytest.mark.asyncio
async def test_progress_reporter_finish_always_flushes() -> None:
    sink_calls: list[ScanProgress] = []

    async def sink(p: ScanProgress) -> None:
        sink_calls.append(p)

    reporter = ProgressReporter(job_id=1, files_to_scan=1, sink=sink, clock=lambda: 0.0)
    await reporter.finish()
    assert len(sink_calls) == 1


@pytest.mark.unit
def test_progress_tick_hz_constant() -> None:
    # AC-3: 1 Hz progress reporting cadence.
    assert PROGRESS_TICK_HZ == 1.0
