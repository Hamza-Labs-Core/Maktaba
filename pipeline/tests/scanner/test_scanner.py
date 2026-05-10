"""Behaviour tests for :mod:`maktaba_pipeline.scanner.service` (Story 1.1).

The orchestrator is exercised through a :class:`FakeScanStore` in-memory
double that implements the :class:`ScanStore` Protocol. The fake models
just enough of the Postgres semantics to drive the AC checklist:

  - per-library content_hash uniqueness (slot 0003)
  - skip-rehash on (library_id, path) when the (size, mtime) signature
    matches (Story 1.2 reuse optimisation)
  - probe-job enqueue inside the same logical transaction as the video
    insert (slot 0002 + slot 0005)
  - SQLite vs Postgres branch on the videos.new pubsub fan-out

Tests are not marked ``unit`` because the asyncio bootstrap calls
``socket.socketpair`` and Story 20.1's netguard rejects unit-tier
sockets — same rationale documented in
``tests/db/test_jobs_enqueue.py``.
"""

from __future__ import annotations

import json
from collections.abc import Iterable
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.db.pubsub import VIDEOS_NEW, get_bus, reset_bus
from maktaba_pipeline.scanner import (
    ExistingVideo,
    LibraryRecord,
    SaveCandidateParams,
    SaveCandidateResult,
    Scanner,
)
from maktaba_pipeline.scanner.service import _mtime_ns_to_db


@dataclass
class _LoggerSink:
    """Records the structlog-shaped calls the scanner makes."""

    events: list[tuple[str, str, dict[str, Any]]] = field(default_factory=list)

    def info(self, event: str, **kwargs: Any) -> None:
        self.events.append(("info", event, kwargs))

    def warning(self, event: str, **kwargs: Any) -> None:
        self.events.append(("warning", event, kwargs))

    def debug(self, event: str, **kwargs: Any) -> None:
        self.events.append(("debug", event, kwargs))

    def error(self, event: str, **kwargs: Any) -> None:
        self.events.append(("error", event, kwargs))

    def names(self, level: str) -> list[str]:
        return [event for lvl, event, _ in self.events if lvl == level]


@dataclass
class _StoredVideo:
    """One persisted ``videos`` row in the fake store."""

    id: UUID
    library_id: UUID
    content_hash: str
    path: str
    filename: str
    size_bytes: int
    mtime: datetime
    last_seen_at: datetime
    state: str = "discovered"


@dataclass
class FakeScanStore:
    """In-memory ScanStore that mimics Postgres ON CONFLICT semantics.

    Models the slot 0003 ``UNIQUE (library_id, content_hash)`` index so
    the scanner's "skip on conflict" path is actually exercised. The
    store is sync inside; the protocol methods are ``async`` so the
    orchestrator can ``await`` them on either dialect.
    """

    libraries: dict[UUID, LibraryRecord] = field(default_factory=dict)
    videos: dict[UUID, _StoredVideo] = field(default_factory=dict)
    jobs: list[dict[str, Any]] = field(default_factory=list)
    dialect: str = "postgres"
    next_job_id: int = 1

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        return self.libraries.get(library_id)

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        for v in self.videos.values():
            if v.library_id == library_id and v.path == path:
                return ExistingVideo(
                    id=v.id,
                    size_bytes=v.size_bytes,
                    mtime=v.mtime,
                    content_hash=v.content_hash,
                )
        return None

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        # Per-library uniqueness: same (library_id, content_hash) → reuse.
        for v in self.videos.values():
            if v.library_id == params.library_id and v.content_hash == params.content_hash:
                return SaveCandidateResult(video_id=v.id, inserted=False, job_id=None)

        new_id = uuid4()
        self.videos[new_id] = _StoredVideo(
            id=new_id,
            library_id=params.library_id,
            content_hash=params.content_hash,
            path=params.path,
            filename=params.filename,
            size_bytes=params.size_bytes,
            mtime=params.mtime,
            last_seen_at=params.last_seen_at,
        )
        job_id: int | None = None
        if params.enqueue_probe:
            job_id = self.next_job_id
            self.next_job_id += 1
            self.jobs.append(
                {
                    "id": job_id,
                    "video_id": new_id,
                    "stage": "probe",
                    "state": "pending",
                    "priority": 100,
                }
            )
        return SaveCandidateResult(video_id=new_id, inserted=True, job_id=job_id)


def _touch(root: Path, rel: str, *, content: bytes = b"x") -> Path:
    p = root / rel
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(content)
    return p


def _make_lib(
    root: Path, *, disabled: bool = False, follow_symlinks: bool = False
) -> LibraryRecord:
    return LibraryRecord(
        id=uuid4(),
        name="test-lib",
        roots=(str(root),),
        disabled=disabled,
        follow_symlinks=follow_symlinks,
    )


def _stage_state_pairs(jobs: Iterable[dict[str, Any]]) -> list[tuple[str, str]]:
    return [(j["stage"], j["state"]) for j in jobs]


@pytest.mark.asyncio
async def test_scan_inserts_row_per_video(tmp_path: Path) -> None:
    """Story 1.1 AC1 — N supported files → N inserted rows + N probe jobs."""
    n = 50
    for i in range(n):
        # Distinct content per file so the per-library content_hash
        # uniqueness doesn't fold them into a single row.
        _touch(tmp_path, f"sub{i // 10}/v{i:04d}.mp4", content=f"video-{i}".encode())
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    log = _LoggerSink()
    scanner = Scanner(store, log=log)
    res = await scanner.run(lib.id)

    assert res.files_walked == n
    assert res.files_inserted == n
    assert res.files_unchanged == 0
    assert res.errors == []
    assert len(store.videos) == n
    assert _stage_state_pairs(store.jobs) == [("probe", "pending")] * n


@pytest.mark.asyncio
async def test_scan_ignores_non_video_extensions(tmp_path: Path) -> None:
    """Story 1.1 AC3 — only the supported extensions become rows."""
    _touch(tmp_path, "a.mp4", content=b"video bytes")
    _touch(tmp_path, "b.txt")
    _touch(tmp_path, "c.jpg")
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    res = await Scanner(store, log=_LoggerSink()).run(lib.id)

    assert res.files_inserted == 1
    only = next(iter(store.videos.values()))
    assert only.filename == "a.mp4"
    assert len(store.jobs) == 1


@pytest.mark.asyncio
async def test_scan_zero_byte_file_skipped(tmp_path: Path) -> None:
    """Story 1.1 edge case — zero-byte files produce no row, no error."""
    _touch(tmp_path, "empty.mp4", content=b"")
    _touch(tmp_path, "real.mp4", content=b"data")
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    log = _LoggerSink()
    res = await Scanner(store, log=log).run(lib.id)

    assert res.files_inserted == 1
    assert res.files_ignored == 1
    assert res.errors == []
    assert "scanner.zero_byte_skipped" in log.names("debug")


@pytest.mark.asyncio
async def test_scan_creates_no_jobs_when_library_disabled(tmp_path: Path) -> None:
    """Story 1.1 test case 5 — disabled libraries walk but enqueue zero jobs."""
    for i in range(10):
        _touch(tmp_path, f"v{i}.mp4", content=f"content-{i}".encode())
    store = FakeScanStore()
    lib = _make_lib(tmp_path, disabled=True)
    store.libraries[lib.id] = lib

    res = await Scanner(store, log=_LoggerSink()).run(lib.id)

    assert res.files_inserted == 10
    assert len(store.videos) == 10
    assert store.jobs == []


@pytest.mark.asyncio
async def test_scan_skips_unchanged_files_on_second_run(tmp_path: Path) -> None:
    """FileSignature optimisation — second pass over the same tree rehashes nothing."""
    for i in range(5):
        _touch(tmp_path, f"v{i}.mp4", content=f"content-{i}".encode())
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    scanner = Scanner(store, log=_LoggerSink())
    first = await scanner.run(lib.id)
    assert first.files_inserted == 5

    second = await scanner.run(lib.id)
    assert second.files_walked == 5
    assert second.files_inserted == 0
    assert second.files_unchanged == 5
    assert second.errors == []
    # No new jobs on the rerun.
    assert len(store.jobs) == 5


@pytest.mark.asyncio
async def test_scan_rehashes_when_mtime_changes(tmp_path: Path) -> None:
    """If the file's mtime advances, the scanner must rehash and update the row."""
    _touch(tmp_path, "v.mp4", content=b"original")
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    scanner = Scanner(store, log=_LoggerSink())
    await scanner.run(lib.id)
    assert len(store.videos) == 1

    # Mutate the row's stored mtime to something older than the file —
    # simulating an out-of-band change since the last scan.
    only_id = next(iter(store.videos))
    store.videos[only_id].mtime = store.videos[only_id].mtime - timedelta(days=1)

    res = await scanner.run(lib.id)
    # The hash is the same (file unchanged) so ON CONFLICT swallows the
    # second insert — the `inserted` count stays zero, but the unchanged
    # count is also zero because the signature mismatched.
    assert res.files_unchanged == 0
    assert res.files_inserted == 0
    assert res.files_skipped == 1


@pytest.mark.asyncio
async def test_scan_warns_on_zero_roots(tmp_path: Path) -> None:
    """Story 1.1 edge case — library with zero roots completes immediately with WARN."""
    store = FakeScanStore()
    lib = LibraryRecord(id=uuid4(), name="empty", roots=(), disabled=False)
    store.libraries[lib.id] = lib

    log = _LoggerSink()
    res = await Scanner(store, log=log).run(lib.id)

    assert res.files_walked == 0
    assert res.files_inserted == 0
    assert "scanner.no_roots" in log.names("warning")


@pytest.mark.asyncio
async def test_scan_unknown_library_raises(tmp_path: Path) -> None:
    store = FakeScanStore()
    with pytest.raises(LookupError):
        await Scanner(store, log=_LoggerSink()).run(uuid4())


@pytest.mark.asyncio
async def test_scan_publishes_videos_new_on_sqlite(tmp_path: Path) -> None:
    """SQLite has no LISTEN/NOTIFY — the scanner publishes manually."""
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(VIDEOS_NEW)

    _touch(tmp_path, "a.mp4", content=b"sqlite-bytes")
    store = FakeScanStore(dialect="sqlite")
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    res = await Scanner(store, log=_LoggerSink()).run(lib.id)
    assert res.files_inserted == 1

    note = json.loads(queue.get_nowait())
    assert note["library_id"] == str(lib.id)
    assert note["filename"] == "a.mp4"
    assert note["state"] == "discovered"
    assert note["path"].endswith("a.mp4")
    assert len(note["content_hash"]) == 64


@pytest.mark.asyncio
async def test_scan_does_not_publish_videos_new_on_postgres(tmp_path: Path) -> None:
    """Postgres' slot 0005 trigger fires NOTIFY at the SQL layer.

    The scanner must NOT also publish on the in-process bus, otherwise
    in-process subscribers in integration tests would see double events.
    """
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(VIDEOS_NEW)

    _touch(tmp_path, "a.mp4", content=b"pg-bytes")
    store = FakeScanStore(dialect="postgres")
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    await Scanner(store, log=_LoggerSink()).run(lib.id)
    assert queue.empty()


@pytest.mark.asyncio
async def test_scan_per_file_save_error_aggregated_not_aborted(tmp_path: Path) -> None:
    """A failing save on one file must not stop the rest of the scan."""
    _touch(tmp_path, "good.mp4", content=b"ok")
    _touch(tmp_path, "bad.mp4", content=b"explode")
    boom_paths: set[str] = set()

    class _ExplodingStore(FakeScanStore):
        async def save_candidate(self, params: SaveCandidateParams) -> SaveCandidateResult:
            if params.filename == "bad.mp4":
                boom_paths.add(params.path)
                raise RuntimeError("synthetic save failure")
            return await super().save_candidate(params)

    store = _ExplodingStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    log = _LoggerSink()
    res = await Scanner(store, log=log).run(lib.id)

    assert res.files_walked == 2
    assert res.files_inserted == 1
    assert len(res.errors) == 1
    assert res.errors[0].path in boom_paths
    assert "scanner.save_failed" in log.names("error")


@pytest.mark.asyncio
async def test_scan_records_started_and_finished(tmp_path: Path) -> None:
    _touch(tmp_path, "a.mp4")
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    res = await Scanner(store, log=_LoggerSink()).run(lib.id)

    assert res.started_at.tzinfo is not None
    assert res.finished_at is not None
    assert res.finished_at >= res.started_at


@pytest.mark.asyncio
async def test_scan_normalises_mtime_to_microseconds(tmp_path: Path) -> None:
    """``videos.mtime`` is microsecond-precision — the helper must round."""
    _touch(tmp_path, "a.mp4", content=b"x")
    store = FakeScanStore()
    lib = _make_lib(tmp_path)
    store.libraries[lib.id] = lib

    await Scanner(store, log=_LoggerSink()).run(lib.id)
    only = next(iter(store.videos.values()))
    # The microsecond field should be a non-negative int below 1_000_000.
    assert 0 <= only.mtime.microsecond < 1_000_000
    # Round-tripping our helper on the same ns produces the same value.
    p = next(tmp_path.iterdir())
    assert _mtime_ns_to_db(p.stat().st_mtime_ns) == only.mtime
    assert only.mtime.tzinfo is UTC
