"""Unit tests for :class:`maktaba_pipeline.watcher.WatcherDispatcher`.

These tests bypass watchdog and the debouncer entirely — the dispatcher
is fed synthetic :class:`SettledEvent` instances against a
:class:`FakeWatcherStore` so each branch (CREATE / unchanged-MODIFY /
content-changed-MODIFY / MOVED / DELETED / rediscovery) lands its own
assertion. Real DB-side coverage lives in the integration test module.
"""

from __future__ import annotations

import json
from datetime import UTC, datetime
from pathlib import Path

import pytest

from maktaba_pipeline.db.pubsub import VIDEOS_NEW, get_bus, reset_bus
from maktaba_pipeline.identity import hash_file
from maktaba_pipeline.watcher import Op, SettledEvent, WatcherDispatcher

from .conftest import FakeWatcherStore, LoggerSink, make_lib


def _settled(
    *,
    library_id: str,
    op: Op,
    path: str,
    dest_path: str | None = None,
    size_bytes: int = -1,
    mtime_ns: int = 0,
) -> SettledEvent:
    return SettledEvent(
        library_id=library_id,
        op=op,
        path=path,
        dest_path=dest_path,
        size_bytes=size_bytes,
        mtime_ns=mtime_ns,
    )


def _write(tmp_path: Path, name: str, content: bytes) -> Path:
    p = tmp_path / name
    p.write_bytes(content)
    return p


@pytest.mark.asyncio
async def test_create_inserts_new_video_and_probe_job(tmp_path: Path) -> None:
    p = _write(tmp_path, "movie.mp4", b"first-bytes")
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    out = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )

    assert out.inserted is True
    assert out.video_id is not None
    assert len(store.videos) == 1
    [stored] = store.videos.values()
    assert stored.path == str(p)
    assert stored.state == "discovered"
    assert [(j.stage, j.state) for j in store.jobs] == [("probe", "pending")]


@pytest.mark.asyncio
async def test_create_for_disabled_library_inserts_row_but_no_probe(tmp_path: Path) -> None:
    """Mirrors the scanner's enabled/disabled branching."""
    p = _write(tmp_path, "movie.mp4", b"some-bytes")
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path), disabled=True)
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    out = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )

    assert out.inserted is True
    assert store.jobs == []


@pytest.mark.asyncio
async def test_modify_with_unchanged_content_is_a_noop(tmp_path: Path) -> None:
    """A MODIFY whose hash matches the row leaves state untouched."""
    p = _write(tmp_path, "movie.mp4", b"steady-content")
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    # First dispatch creates the row.
    create = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )
    assert create.inserted is True

    # A second dispatch (e.g. spurious MODIFY from a touch) is a no-op.
    second = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.MODIFY,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )
    assert second.no_op is True
    assert second.video_id == create.video_id
    assert len(store.jobs) == 1  # no second probe


@pytest.mark.asyncio
async def test_moved_within_library_updates_path_no_new_row(tmp_path: Path) -> None:
    """Story AC-3 + TC `test_watcher_handles_rename`."""
    src = _write(tmp_path, "old.mp4", b"unchanged-bytes")
    dest = tmp_path / "renamed.mp4"
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(src),
            size_bytes=src.stat().st_size,
            mtime_ns=src.stat().st_mtime_ns,
        )
    )
    [original_id] = list(store.videos.keys())
    assert len(store.jobs) == 1

    # Simulate the rename happening on disk.
    src.rename(dest)
    out = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.MOVED,
            path=str(src),
            dest_path=str(dest),
        )
    )
    assert out.updated_path is True
    assert out.video_id == original_id
    # No fresh probe job and no fresh row.
    assert len(store.videos) == 1
    assert len(store.jobs) == 1
    assert store.videos[original_id].path == str(dest)


@pytest.mark.asyncio
async def test_moved_with_no_source_row_falls_back_to_create(tmp_path: Path) -> None:
    """Atomic mv from outside the watched root: hash + dedupe path."""
    dest = _write(tmp_path, "dropped.mp4", b"new-bytes")
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    out = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.MOVED,
            path="/somewhere/not/watched",
            dest_path=str(dest),
            size_bytes=dest.stat().st_size,
            mtime_ns=dest.stat().st_mtime_ns,
        )
    )
    assert out.inserted is True
    assert len(store.videos) == 1


@pytest.mark.asyncio
async def test_deleted_soft_deletes_existing_row(tmp_path: Path) -> None:
    """Story AC-4 + TC `test_watcher_handles_delete` — state goes to MISSING."""
    p = _write(tmp_path, "going.mp4", b"will-vanish")
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())
    await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )
    [video_id] = list(store.videos.keys())

    out = await dispatcher.dispatch(_settled(library_id=str(lib.id), op=Op.DELETED, path=str(p)))
    assert out.soft_deleted is True
    assert out.video_id == video_id
    assert store.videos[video_id].state == "missing"
    # Derived data is preserved (the story explicitly forbids destroying it).
    assert store.videos[video_id].derived_kept is True


@pytest.mark.asyncio
async def test_deleted_for_unknown_path_is_a_noop(tmp_path: Path) -> None:
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    out = await dispatcher.dispatch(
        _settled(library_id=str(lib.id), op=Op.DELETED, path=str(tmp_path / "ghost.mp4"))
    )
    assert out.no_op is True
    assert out.video_id is None


@pytest.mark.asyncio
async def test_create_after_missing_rediscovers_and_keeps_id(tmp_path: Path) -> None:
    """Edge case: a file reappears after being marked missing.

    The dispatcher must look the existing row up by content_hash, call
    ``rediscover`` (which the canonical store implements via
    advance_after_stage with REDISCOVERED), and update the path.
    """
    content = b"missing-then-back"
    p = _write(tmp_path, "first.mp4", content)
    digest = hash_file(p).content_hash

    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib

    # Pre-populate a MISSING row by the same content hash but a stale path.
    from uuid import uuid4

    from .conftest import StoredVideo

    stale = uuid4()
    store.videos[stale] = StoredVideo(
        id=stale,
        library_id=lib.id,
        content_hash=digest,
        path=str(tmp_path / "old_location.mp4"),
        filename="old_location.mp4",
        size_bytes=len(content),
        mtime=datetime.now(tz=UTC),
        last_seen_at=datetime.now(tz=UTC),
        state="missing",
    )

    dispatcher = WatcherDispatcher(store, log=LoggerSink())
    out = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )

    assert out.rediscovered is True
    assert out.video_id == stale
    # The same id stays; only the path and state changed.
    assert store.videos[stale].state == "discovered"
    assert store.videos[stale].path == str(p)
    # No new row, no new probe job.
    assert len(store.videos) == 1
    assert store.jobs == []


@pytest.mark.asyncio
async def test_unknown_library_dispatch_is_logged_warning(tmp_path: Path) -> None:
    p = _write(tmp_path, "movie.mp4", b"orphan")
    store = FakeWatcherStore()
    log = LoggerSink()
    dispatcher = WatcherDispatcher(store, log=log)

    from uuid import uuid4

    out = await dispatcher.dispatch(
        _settled(
            library_id=str(uuid4()),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )
    assert out.no_op is True
    assert "watcher.dispatch.unknown_library" in log.names("warning")


@pytest.mark.asyncio
async def test_dispatch_publishes_videos_new_on_sqlite(tmp_path: Path) -> None:
    """SQLite has no LISTEN/NOTIFY — the dispatcher publishes manually."""
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(VIDEOS_NEW)

    p = _write(tmp_path, "a.mp4", b"sqlite-bytes")
    store = FakeWatcherStore(dialect="sqlite")
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())

    await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )
    note = json.loads(queue.get_nowait())
    assert note["library_id"] == str(lib.id)
    assert note["filename"] == "a.mp4"
    assert note["state"] == "discovered"
    assert note["path"] == str(p)
    assert len(note["content_hash"]) == 64


@pytest.mark.asyncio
async def test_dispatch_does_not_publish_videos_new_on_postgres(tmp_path: Path) -> None:
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(VIDEOS_NEW)

    p = _write(tmp_path, "a.mp4", b"pg-bytes")
    store = FakeWatcherStore(dialect="postgres")
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    dispatcher = WatcherDispatcher(store, log=LoggerSink())
    await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(p),
            size_bytes=p.stat().st_size,
            mtime_ns=p.stat().st_mtime_ns,
        )
    )
    assert queue.empty()


@pytest.mark.asyncio
async def test_dispatch_handles_disappeared_file_between_settle_and_hash(
    tmp_path: Path,
) -> None:
    """If the file vanished after settle, hashing fails with FileNotFoundError.

    The dispatcher logs and returns a no-op outcome rather than
    propagating — the matching DELETED event will arrive on its own.
    """
    store = FakeWatcherStore()
    lib = make_lib(str(tmp_path))
    store.libraries[lib.id] = lib
    log = LoggerSink()
    dispatcher = WatcherDispatcher(store, log=log)

    out = await dispatcher.dispatch(
        _settled(
            library_id=str(lib.id),
            op=Op.CREATE,
            path=str(tmp_path / "ghost.mp4"),
            size_bytes=10,
            mtime_ns=0,
        )
    )
    assert out.no_op is True
    assert out.video_id is None
    assert "watcher.dispatch.gone_before_hash" in log.names("debug")
