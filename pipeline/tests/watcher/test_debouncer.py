"""Unit tests for :class:`maktaba_pipeline.watcher.Debouncer`.

The debouncer is the only piece of the watcher pipeline that is fully
deterministic when ``schedule=False`` — these tests therefore avoid all
real timers and drive the settle probe via :meth:`Debouncer.tick_now`.
The size-stability and mtime-quarantine logic is covered with a fake
``stat`` so the test runs in nanoseconds and never touches the disk.
"""

from __future__ import annotations

from dataclasses import dataclass

import pytest

from maktaba_pipeline.watcher import Debouncer, DebouncerConfig, Op, RawEvent, SettledEvent

from .conftest import LoggerSink

LIB = "00000000-0000-0000-0000-000000000001"


@dataclass
class _FakeStat:
    """Stand-in for :class:`os.stat_result` with writable nanosecond mtime."""

    st_size: int
    st_mtime: float
    st_mtime_ns: int


class _FakeFs:
    """Minimal stat backend tests can program imperatively."""

    def __init__(self) -> None:
        self.snapshots: dict[str, _FakeStat] = {}

    def set(self, path: str, *, size: int, mtime: float) -> None:
        self.snapshots[path] = _FakeStat(
            st_size=size,
            st_mtime=mtime,
            st_mtime_ns=int(mtime * 1_000_000_000),
        )

    def remove(self, path: str) -> None:
        self.snapshots.pop(path, None)

    def stat(self, path: str) -> _FakeStat:
        snap = self.snapshots.get(path)
        if snap is None:
            raise FileNotFoundError(path)
        return snap


@pytest.fixture
def fs() -> _FakeFs:
    return _FakeFs()


def _make_debouncer(
    fs: _FakeFs,
    *,
    settle_ticks: int = 2,
    settle_sec: float = 0.0,
    log: LoggerSink | None = None,
) -> tuple[Debouncer, list[SettledEvent]]:
    sink = log or LoggerSink()
    out: list[SettledEvent] = []
    db = Debouncer(
        on_settled=out.append,
        log=sink,
        config=DebouncerConfig(
            debounce_sec=0.001,
            settle_sec=settle_sec,
            settle_ticks=settle_ticks,
        ),
        schedule=False,
        _wall=lambda: 1_000_000.0,
        _stat=lambda p: fs.stat(p),
    )
    return db, out


def test_create_event_settles_after_required_ticks(fs: _FakeFs) -> None:
    """A simple CREATE settles after ``settle_ticks`` consecutive stable probes."""
    db, out = _make_debouncer(fs, settle_ticks=2)
    fs.set("/lib/a.mkv", size=1024, mtime=999_999.0)

    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    assert db.pending_count() == 1

    db.tick_now("/lib/a.mkv")  # tick 1: size becomes "last"
    assert out == []
    db.tick_now("/lib/a.mkv")  # tick 2: stable → settled
    assert len(out) == 1
    assert out[0].op == Op.CREATE
    assert out[0].path == "/lib/a.mkv"
    assert out[0].size_bytes == 1024
    assert out[0].library_id == LIB
    assert db.pending_count() == 0


def test_growing_file_re_arms_until_size_stops_changing(fs: _FakeFs) -> None:
    """The AC-2 partial-write case: re-arm while size grows."""
    db, out = _make_debouncer(fs, settle_ticks=2)
    fs.set("/lib/a.mkv", size=100, mtime=999_999.0)

    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    db.tick_now("/lib/a.mkv")  # size=100, stable=1
    assert out == []

    fs.set("/lib/a.mkv", size=200, mtime=999_999.0)
    db.tick_now("/lib/a.mkv")  # size grew → reset, stable=1
    assert out == []
    assert db.pending_count() == 1

    fs.set("/lib/a.mkv", size=300, mtime=999_999.0)
    db.tick_now("/lib/a.mkv")  # size grew again → still stable=1
    assert out == []

    db.tick_now("/lib/a.mkv")  # size held → stable=2 → settled
    assert len(out) == 1
    assert out[0].size_bytes == 300


def test_burst_of_modify_events_collapses_to_one_settle(fs: _FakeFs) -> None:
    """1000 raw events for one path settle exactly once.

    Mirrors the plan §8.1 ``TestDebouncerCollapsesBurst`` synthetic:
    no real timers fire because we drive settle ourselves.
    """
    db, out = _make_debouncer(fs, settle_ticks=1)
    fs.set("/lib/a.mkv", size=42, mtime=999_999.0)

    for _ in range(1000):
        db.feed(RawEvent(library_id=LIB, op=Op.MODIFY, path="/lib/a.mkv"))
    assert db.pending_count() == 1

    db.tick_now("/lib/a.mkv")
    assert len(out) == 1
    assert out[0].op == Op.MODIFY


def test_mtime_quarantine_blocks_settle_until_quiet(fs: _FakeFs) -> None:
    """A file whose mtime is fresher than ``settle_sec`` is not settled."""
    db, out = _make_debouncer(fs, settle_ticks=1, settle_sec=10.0)
    fs.set("/lib/a.mkv", size=1024, mtime=1_000_000.0 - 5.0)  # 5s old

    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    db.tick_now("/lib/a.mkv")  # stable=1 but mtime too fresh
    assert out == []
    assert db.pending_count() == 1

    fs.set("/lib/a.mkv", size=1024, mtime=1_000_000.0 - 20.0)  # 20s old
    db.tick_now("/lib/a.mkv")
    assert len(out) == 1


def test_delete_event_emits_immediately(fs: _FakeFs) -> None:
    """DELETED bypasses the timer entirely — there is nothing to wait for."""
    db, out = _make_debouncer(fs, settle_ticks=2)
    fs.set("/lib/a.mkv", size=1, mtime=999_999.0)
    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    assert db.pending_count() == 1

    db.feed(RawEvent(library_id=LIB, op=Op.DELETED, path="/lib/a.mkv"))
    assert len(out) == 1
    assert out[0].op == Op.DELETED
    # The pending settle was cancelled, so a subsequent tick is a no-op.
    assert db.pending_count() == 0
    db.tick_now("/lib/a.mkv")
    assert len(out) == 1


def test_moved_event_emits_immediately_with_dest(fs: _FakeFs) -> None:
    """MOVED carries dest_path through the debouncer for the dispatcher."""
    db, out = _make_debouncer(fs, settle_ticks=2)
    db.feed(
        RawEvent(
            library_id=LIB,
            op=Op.MOVED,
            path="/lib/a.mkv",
            dest_path="/lib/sub/b.mkv",
        )
    )
    assert len(out) == 1
    assert out[0].op == Op.MOVED
    assert out[0].path == "/lib/a.mkv"
    assert out[0].dest_path == "/lib/sub/b.mkv"


def test_create_then_delete_within_window_drops_settle(fs: _FakeFs) -> None:
    """Plan-§10 edge case: rapid create-then-delete must never settle a CREATE."""
    db, out = _make_debouncer(fs, settle_ticks=2)
    fs.set("/lib/a.mkv", size=1, mtime=999_999.0)
    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    fs.remove("/lib/a.mkv")
    db.feed(RawEvent(library_id=LIB, op=Op.DELETED, path="/lib/a.mkv"))

    # Only the DELETED settled event escapes; the CREATE was cancelled.
    assert [e.op for e in out] == [Op.DELETED]


def test_vanish_during_settle_is_logged_and_drops_entry(fs: _FakeFs) -> None:
    """If the file is gone when the timer probes, the entry is dropped."""
    log = LoggerSink()
    db, out = _make_debouncer(fs, settle_ticks=2, log=log)
    fs.set("/lib/a.mkv", size=10, mtime=999_999.0)

    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    fs.remove("/lib/a.mkv")
    db.tick_now("/lib/a.mkv")

    assert out == []
    assert db.pending_count() == 0
    assert "watcher.debouncer.gone_during_settle" in log.names("debug")


def test_shutdown_drops_pending_and_blocks_further_feeds(fs: _FakeFs) -> None:
    """``shutdown()`` is permanent: post-shutdown ``feed`` is a no-op."""
    db, out = _make_debouncer(fs, settle_ticks=2)
    fs.set("/lib/a.mkv", size=1, mtime=999_999.0)
    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    assert db.pending_count() == 1

    db.shutdown()
    assert db.pending_count() == 0

    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    db.feed(RawEvent(library_id=LIB, op=Op.DELETED, path="/lib/a.mkv"))
    assert out == []


def test_callback_failure_is_logged_not_raised(fs: _FakeFs) -> None:
    """A throwing callback must not tear down the debouncer."""
    log = LoggerSink()
    fs.set("/lib/a.mkv", size=1, mtime=999_999.0)

    def boom(_: SettledEvent) -> None:
        raise RuntimeError("synthetic")

    db = Debouncer(
        on_settled=boom,
        log=log,
        config=DebouncerConfig(debounce_sec=0, settle_sec=0, settle_ticks=1),
        schedule=False,
        _wall=lambda: 1_000_000.0,
        _stat=lambda p: fs.stat(p),
    )
    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    db.tick_now("/lib/a.mkv")  # must not raise
    assert "watcher.debouncer.callback_failed" in log.names("warning")


def test_create_after_modify_promotes_op(fs: _FakeFs) -> None:
    """MODIFY then CREATE on same path → settled op stays CREATE.

    Some filesystems reuse inodes such that a MODIFY can race a CREATE
    for the new file at the same path; we want the dispatcher to treat
    the result as a fresh discovery, not an update.
    """
    db, out = _make_debouncer(fs, settle_ticks=1)
    fs.set("/lib/a.mkv", size=1, mtime=999_999.0)

    db.feed(RawEvent(library_id=LIB, op=Op.MODIFY, path="/lib/a.mkv"))
    db.feed(RawEvent(library_id=LIB, op=Op.CREATE, path="/lib/a.mkv"))
    db.tick_now("/lib/a.mkv")
    assert len(out) == 1
    assert out[0].op == Op.CREATE
