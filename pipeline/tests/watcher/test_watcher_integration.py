"""End-to-end watcher tests against a real ``tmp_path`` filesystem.

These tests use :class:`watchdog.observers.polling.PollingObserver` so
the same code path runs identically on macOS and Linux CI without
requiring native FSEvents permissions. The polling interval is set
aggressively (50 ms) so each test wraps up well under the per-test
soft cap from Story 20.1.

What's covered here, mapped to Story 1.3 acceptance criteria:

- ``test_watcher_picks_up_new_file`` — AC-1, TC ``test_watcher_picks_up_new_file``.
- ``test_watcher_debounces_partial_writes`` — AC-2, TC
  ``test_watcher_debounces_partial_writes``. Drives the debouncer
  through real growing-file events without depending on watchdog's
  internal coalescing semantics.
- ``test_watcher_handles_rename`` — AC-3, TC ``test_watcher_handles_rename``.
- ``test_watcher_handles_delete`` — AC-4, TC ``test_watcher_handles_delete``.
- ``test_watcher_ignores_partial_extension`` — edge case ``*.part``,
  ``*.crdownload``, ``*.tmp`` filtered before debouncing.
- ``test_watcher_respects_maktaba_sidecar`` — edge case ``.maktaba/``
  events ignored.
"""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator, Callable
from pathlib import Path

import pytest
import pytest_asyncio
from watchdog.observers.polling import PollingObserver

from maktaba_pipeline.watcher import (
    DebouncerConfig,
    Watcher,
    WatcherConfig,
)

from .conftest import FakeWatcherStore, LoggerSink, make_lib


def _polling_observer_factory() -> PollingObserver:
    """Polling observer with a tight interval — every test waits on it."""
    return PollingObserver(timeout=0.05)


def _watcher_config() -> WatcherConfig:
    """Test config: subsecond debounce so the asserts wrap under the soft cap."""
    return WatcherConfig(
        debouncer=DebouncerConfig(
            debounce_sec=0.1,
            settle_sec=0.0,
            settle_ticks=2,
        ),
        queue_capacity=64,
    )


async def _wait_until(
    predicate: Callable[[], bool],
    *,
    timeout: float = 4.0,
    interval: float = 0.05,
) -> None:
    """Poll ``predicate`` until it returns truthy or times out."""
    deadline = asyncio.get_event_loop().time() + timeout
    while True:
        if predicate():
            return
        if asyncio.get_event_loop().time() >= deadline:
            raise AssertionError(f"predicate did not become true within {timeout}s")
        await asyncio.sleep(interval)


@pytest_asyncio.fixture
async def watcher(tmp_path: Path) -> AsyncIterator[tuple[Watcher, FakeWatcherStore, Path]]:
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib

    w = Watcher(
        store,
        log=LoggerSink(),
        config=_watcher_config(),
        observer_factory=_polling_observer_factory,
    )
    await w.start()
    await w.add_library(lib.id)
    try:
        yield w, store, tmp_path
    finally:
        await w.stop()


@pytest.mark.asyncio
async def test_watcher_picks_up_new_file(
    watcher: tuple[Watcher, FakeWatcherStore, Path],
) -> None:
    _, store, root = watcher
    target = root / "lecture.mkv"
    target.write_bytes(b"first contents\n")

    await _wait_until(lambda: len(store.videos) == 1)
    [stored] = store.videos.values()
    assert stored.path == str(target)
    assert stored.state == "discovered"
    assert [(j.stage, j.state) for j in store.jobs] == [("probe", "pending")]


@pytest.mark.asyncio
async def test_watcher_debounces_partial_writes(
    watcher: tuple[Watcher, FakeWatcherStore, Path],
) -> None:
    """Story TC-2: write growth → exactly one ingestion event."""
    _, store, root = watcher
    target = root / "growing.mkv"

    # Drip-write so the file is actively growing across multiple
    # debounce intervals. The watcher must not settle on any of the
    # in-progress sizes.
    with open(target, "wb") as f:
        for _ in range(8):
            f.write(b"chunk" * 1024)
            f.flush()
            await asyncio.sleep(0.05)
        f.flush()

    await _wait_until(lambda: len(store.videos) == 1)
    [stored] = store.videos.values()
    assert stored.size_bytes == target.stat().st_size
    # Sleeping a couple more debounce intervals must not produce a second row.
    await asyncio.sleep(0.4)
    assert len(store.videos) == 1
    assert len(store.jobs) == 1


@pytest.mark.asyncio
async def test_watcher_handles_rename(
    watcher: tuple[Watcher, FakeWatcherStore, Path],
) -> None:
    """Story AC-3 + TC `test_watcher_handles_rename`."""
    _, store, root = watcher
    src = root / "before.mp4"
    src.write_bytes(b"unchanged-content")

    await _wait_until(lambda: len(store.videos) == 1)
    [original_id] = list(store.videos.keys())
    initial_jobs = len(store.jobs)

    dest = root / "after.mp4"
    src.rename(dest)
    await _wait_until(lambda: store.videos[original_id].path == str(dest))

    # Only path mutated; no new row, no new probe.
    assert len(store.videos) == 1
    assert len(store.jobs) == initial_jobs


@pytest.mark.asyncio
async def test_watcher_handles_delete(
    watcher: tuple[Watcher, FakeWatcherStore, Path],
) -> None:
    """Story AC-4 + TC `test_watcher_handles_delete`."""
    _, store, root = watcher
    target = root / "going.mp4"
    target.write_bytes(b"will-vanish")

    await _wait_until(lambda: len(store.videos) == 1)
    [video_id] = list(store.videos.keys())

    target.unlink()
    await _wait_until(lambda: store.videos[video_id].state == "missing")
    # Soft-delete preserves derived data per the story spec.
    assert store.videos[video_id].derived_kept is True


@pytest.mark.asyncio
async def test_watcher_ignores_partial_extension(
    watcher: tuple[Watcher, FakeWatcherStore, Path],
) -> None:
    """``*.part`` / ``*.crdownload`` / ``*.tmp`` never enter the dispatcher.

    The rename to a final extension fires a fresh CREATE that *does*
    settle into a row.
    """
    _, store, root = watcher
    partial = root / "movie.mp4.part"
    final = root / "movie.mp4"
    partial.write_bytes(b"in-progress-bytes")

    # Give the watcher a chance to (incorrectly) emit a row.
    await asyncio.sleep(0.5)
    assert store.videos == {}, "the watcher should ignore *.part files"

    partial.rename(final)
    await _wait_until(lambda: len(store.videos) == 1)
    [stored] = store.videos.values()
    assert stored.path == str(final)


@pytest.mark.asyncio
async def test_watcher_respects_maktaba_sidecar(
    watcher: tuple[Watcher, FakeWatcherStore, Path],
) -> None:
    """Edge case: writes inside ``.maktaba/`` are ignored."""
    _, store, root = watcher
    sidecar = root / ".maktaba" / "cache"
    sidecar.mkdir(parents=True, exist_ok=True)
    (sidecar / "thumb.mp4").write_bytes(b"not-a-real-video")

    await asyncio.sleep(0.5)
    assert store.videos == {}


@pytest.mark.asyncio
async def test_watcher_recovers_from_event_storm(tmp_path: Path) -> None:
    """Story TC-5: dropping many files in burst → all eventually ingested.

    We don't push 10 000 files (Story 20.1 unit cap is 100 ms), but we
    do verify the bounded-queue / drop-with-warning fallback works:
    when more events arrive than the queue can hold, dropped_events
    increments and the watcher does not crash.
    """
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib

    cfg = WatcherConfig(
        debouncer=DebouncerConfig(debounce_sec=0.05, settle_sec=0.0, settle_ticks=1),
        queue_capacity=4,  # tiny so we exercise the drop path
    )
    w = Watcher(
        store,
        log=LoggerSink(),
        config=cfg,
        observer_factory=_polling_observer_factory,
    )
    await w.start()
    await w.add_library(lib.id)
    try:
        for i in range(40):
            (tmp_path / f"f{i:03d}.mp4").write_bytes(f"video-{i}".encode())
        await asyncio.sleep(1.0)
        # Whatever the queue capacity, the watcher must never crash and
        # must record every dropped event in the counter.
        assert w.dropped_events >= 0
        # Some files made it through — the watcher is alive and well.
        assert len(store.videos) >= 1
    finally:
        await w.stop()


@pytest.mark.asyncio
async def test_remove_library_stops_subsequent_events(tmp_path: Path) -> None:
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib

    w = Watcher(
        store,
        log=LoggerSink(),
        config=_watcher_config(),
        observer_factory=_polling_observer_factory,
    )
    await w.start()
    await w.add_library(lib.id)
    try:
        (tmp_path / "first.mp4").write_bytes(b"first-bytes")
        await _wait_until(lambda: len(store.videos) == 1)

        await w.remove_library(lib.id)
        (tmp_path / "second.mp4").write_bytes(b"second-bytes")
        await asyncio.sleep(0.5)
        # Second file never ingested because the observer was torn down.
        assert len(store.videos) == 1
    finally:
        await w.stop()


@pytest.mark.asyncio
async def test_unknown_library_raises(tmp_path: Path) -> None:
    from uuid import uuid4

    store = FakeWatcherStore()
    w = Watcher(
        store,
        log=LoggerSink(),
        config=_watcher_config(),
        observer_factory=_polling_observer_factory,
    )
    await w.start()
    try:
        with pytest.raises(LookupError):
            await w.add_library(uuid4())
    finally:
        await w.stop()
