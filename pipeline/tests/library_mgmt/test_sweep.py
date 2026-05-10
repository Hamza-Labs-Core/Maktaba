"""Story 9.3 — periodic-sweep single-flight + diff against catalog."""

from __future__ import annotations

import asyncio
from collections.abc import Iterable

import pytest

from maktaba_pipeline.library_mgmt.sweep import (
    SweepReport,
    SweepRunner,
    SweepStore,
    _CatalogRow,  # noqa: PLC2701 — internal projection used by tests
)


class _FakeStore(SweepStore):
    def __init__(self, catalog: list[_CatalogRow]) -> None:
        self._catalog = catalog
        self.scan_jobs: list[tuple[str, str]] = []
        self.path_updates: list[tuple[str, str]] = []
        self.missing_marks: list[str] = []
        self.reports: list[SweepReport] = []

    async def list_catalog(self, library_id: str) -> Iterable[_CatalogRow]:
        return list(self._catalog)

    async def insert_scan_job(self, library_id: str, path: str) -> None:
        self.scan_jobs.append((library_id, path))

    async def update_path(self, video_id: str, new_path: str) -> None:
        self.path_updates.append((video_id, new_path))

    async def mark_missing(self, video_id: str) -> None:
        self.missing_marks.append(video_id)

    async def write_sweep_report(self, report: SweepReport) -> None:
        self.reports.append(report)


def _make_walker(*entries: tuple[str, int, int]):
    def walker(root: str) -> list[tuple[str, int, int]]:
        return [e for e in entries if e[0].startswith(root)]
    return walker


@pytest.mark.asyncio
async def test_sweep_enqueues_new_files() -> None:
    store = _FakeStore(catalog=[])
    walker = _make_walker(("/lib/a.mp4", 100, 1000), ("/lib/b.mp4", 200, 2000))
    runner = SweepRunner("lib-1", ["/lib"], store, walker)
    report = await runner.try_sweep()
    assert report is not None
    assert report.scanned == 2
    assert report.new_videos == 2
    assert {p for _, p in store.scan_jobs} == {"/lib/a.mp4", "/lib/b.mp4"}


@pytest.mark.asyncio
async def test_sweep_skips_unchanged_via_size_mtime_fast_path() -> None:
    catalog = [
        _CatalogRow(
            video_id="v1", path="/lib/a.mp4", content_hash="h", size_bytes=100, mtime_ns=1000
        ),
    ]
    store = _FakeStore(catalog=catalog)
    walker = _make_walker(("/lib/a.mp4", 100, 1000))
    runner = SweepRunner("lib-1", ["/lib"], store, walker)
    report = await runner.try_sweep()
    assert report is not None
    assert report.scanned == 1
    assert report.new_videos == 0
    assert store.scan_jobs == []


@pytest.mark.asyncio
async def test_sweep_marks_missing_when_file_gone() -> None:
    catalog = [
        _CatalogRow(
            video_id="v1", path="/lib/a.mp4", content_hash="h", size_bytes=100, mtime_ns=1000
        ),
    ]
    store = _FakeStore(catalog=catalog)
    walker = _make_walker()  # no files on disk
    runner = SweepRunner("lib-1", ["/lib"], store, walker)
    report = await runner.try_sweep()
    assert report is not None
    assert report.removed_videos == 1
    assert store.missing_marks == ["v1"]


@pytest.mark.asyncio
async def test_sweep_is_single_flight() -> None:
    """AC-2: a tick that fires while a sweep is running is dropped."""
    store = _FakeStore(catalog=[])

    # Walker that yields slowly so the second try_sweep() lands while
    # the first is still inside the lock.
    started = asyncio.Event()
    finish = asyncio.Event()

    def slow_walker(root: str) -> list[tuple[str, int, int]]:
        # The runner wraps the walker call in a coroutine — we use the
        # event to keep the lock taken.
        started.set()
        return [("/lib/a.mp4", 100, 1000)]

    runner = SweepRunner("lib-1", ["/lib"], store, slow_walker)

    # Hold the report writer to keep the lock during overlap test.
    real_write = store.write_sweep_report

    async def hold_then_write(report: SweepReport) -> None:
        await finish.wait()
        await real_write(report)

    store.write_sweep_report = hold_then_write  # type: ignore[method-assign]

    task = asyncio.create_task(runner.try_sweep())
    await started.wait()
    second = await runner.try_sweep()
    assert second is None  # AC-2: dropped
    finish.set()
    await task
    assert len(store.reports) == 1


@pytest.mark.asyncio
async def test_sweep_walker_exception_recorded_in_report() -> None:
    store = _FakeStore(catalog=[])

    def angry_walker(root: str) -> list[tuple[str, int, int]]:
        raise OSError("nfs is down")

    runner = SweepRunner("lib-1", ["/lib"], store, angry_walker)
    report = await runner.try_sweep()
    assert report is not None
    assert any("nfs is down" in e["error"] for e in report.errors)
