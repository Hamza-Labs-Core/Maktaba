import datetime as dt
import pathlib

import pytest

from maktaba_pipeline.identity import hash_file
from maktaba_pipeline.integrity import (
    AtomicWriteError,
    BackupManifest,
    BackupPlanner,
    IdempotencyKey,
    MemoryIdempotencyStore,
    atomic_write_bytes,
    atomic_write_text,
    verify_video,
)

# ---- atomic ----------------------------------------------------------------


def test_atomic_write_creates_file(tmp_path: pathlib.Path) -> None:
    target = tmp_path / "out.bin"
    atomic_write_bytes(target, b"hello")
    assert target.read_bytes() == b"hello"


def test_atomic_write_overwrites(tmp_path: pathlib.Path) -> None:
    target = tmp_path / "out.txt"
    target.write_text("old")
    atomic_write_text(target, "new")
    assert target.read_text() == "new"


def test_atomic_write_no_temp_left_behind(tmp_path: pathlib.Path) -> None:
    target = tmp_path / "k.txt"
    atomic_write_text(target, "x")
    leftovers = [p for p in tmp_path.iterdir() if p.name != "k.txt"]
    assert leftovers == []


def test_atomic_write_fails_on_bad_path(tmp_path: pathlib.Path) -> None:
    bad = tmp_path / "no" / "such" / "dir" / "out.txt"
    with pytest.raises(AtomicWriteError):
        atomic_write_text(bad, "x")


# ---- idempotency -----------------------------------------------------------


def test_idempotency_lookup_returns_stored() -> None:
    s = MemoryIdempotencyStore()
    k = IdempotencyKey(job_id="j1", op="commit_segments", args_hash="h1")
    s.store(k, {"committed": 42})
    rec = s.lookup(k)
    assert rec is not None
    assert rec.result["committed"] == 42


def test_idempotency_miss_returns_none() -> None:
    s = MemoryIdempotencyStore()
    k = IdempotencyKey("j1", "op", "h")
    assert s.lookup(k) is None


def test_idempotency_ttl_expiry() -> None:
    s = MemoryIdempotencyStore(ttl_sec=1)
    fake_now = dt.datetime(2026, 5, 10, 12, 0, 0, tzinfo=dt.UTC)
    s._now = lambda: fake_now  # noqa: SLF001
    k = IdempotencyKey("j1", "op", "h")
    s.store(k, "v")
    assert s.lookup(k) is not None
    fake_now = fake_now + dt.timedelta(seconds=2)
    s._now = lambda: fake_now  # noqa: SLF001
    assert s.lookup(k) is None


def test_idempotency_purge() -> None:
    s = MemoryIdempotencyStore()
    s.store(IdempotencyKey("j1", "op", "h1"), 1)
    s.store(IdempotencyKey("j2", "op", "h2"), 2)
    purged = s.purge_older_than(dt.datetime.now(dt.UTC) + dt.timedelta(hours=1))
    assert purged == 2
    assert s.size() == 0


# ---- backup ----------------------------------------------------------------


def test_backup_manifest_roundtrip() -> None:
    m = BackupManifest(
        snapshot_id="snap-1",
        created_at=dt.datetime(2026, 5, 10, 12, 0, 0, tzinfo=dt.UTC),
        schema_rev=57,
        video_count=120,
        job_count=5,
        notes="nightly",
    )
    back = BackupManifest.from_json(m.to_json())
    assert back.snapshot_id == "snap-1"
    assert back.schema_rev == 57
    assert back.notes == "nightly"


def test_backup_planner_records_and_lists(tmp_path: pathlib.Path) -> None:
    p = BackupPlanner(tmp_path)
    for i in range(3):
        p.record(
            BackupManifest(
                snapshot_id=f"snap-{i}",
                created_at=dt.datetime(2026, 5, 10, 12, i, 0, tzinfo=dt.UTC),
                schema_rev=57,
                video_count=100 + i,
                job_count=i,
            )
        )
    snaps = p.list_snapshots()
    assert len(snaps) == 3
    latest = p.latest()
    assert latest is not None
    assert latest.snapshot_id == "snap-2"


def test_backup_planner_skips_corrupt(tmp_path: pathlib.Path) -> None:
    (tmp_path / "bad.json").write_text("not json")
    p = BackupPlanner(tmp_path)
    p.record(BackupManifest("snap-ok", dt.datetime.now(dt.UTC), 1, 0, 0))
    snaps = p.list_snapshots()
    assert len(snaps) == 1


# ---- verify ---------------------------------------------------------------


def test_verify_missing_file(tmp_path: pathlib.Path) -> None:
    res = verify_video(path=tmp_path / "missing.mp4")
    assert not res.file_present
    assert res.error == "missing"
    assert not res.is_ok()


def test_verify_ok_against_canonical_content_hash(tmp_path: pathlib.Path) -> None:
    """An unmodified file verifies OK against its stored ``videos.content_hash``.

    ``videos.content_hash`` is written by the SCAN stage via the
    canonical identity hasher (BLAKE3 head‖tail‖size), so the verifier
    must produce the *same* digest for an untouched file.
    """
    f = tmp_path / "v.mp4"
    f.write_bytes(b"x" * 1024)
    canonical = hash_file(f)
    res = verify_video(
        path=f,
        expected_size=canonical.size_bytes,
        expected_hash=canonical.content_hash,
    )
    assert res.is_ok(), res.error
    assert res.size_bytes == 1024
    assert res.content_hash == canonical.content_hash


def test_verify_detects_altered_file(tmp_path: pathlib.Path) -> None:
    """A file altered in place (same size) is DETECTED as corrupt.

    The head bytes change while size is unchanged, so only a real
    content identity (not a size check) can catch this.
    """
    f = tmp_path / "v.mp4"
    f.write_bytes(b"x" * 1024)
    stored = hash_file(f).content_hash
    f.write_bytes(b"y" * 1024)  # same length, different bytes
    res = verify_video(path=f, expected_size=1024, expected_hash=stored)
    assert res.error == "hash mismatch"
    assert not res.is_ok()


def test_verify_detects_truncated_file(tmp_path: pathlib.Path) -> None:
    """A truncated file is DETECTED as mismatched."""
    f = tmp_path / "v.mp4"
    f.write_bytes(b"x" * 4096)
    stored_hash = hash_file(f).content_hash
    stored_size = 4096
    f.write_bytes(b"x" * 100)  # truncated
    res = verify_video(path=f, expected_size=stored_size, expected_hash=stored_hash)
    # Size check fires first for a length change; either way it must fail.
    assert res.error is not None
    assert not res.is_ok()


def test_verify_small_file_under_head_tail_window(tmp_path: pathlib.Path) -> None:
    """Edge: a file smaller than the head+tail window verifies via the
    canonical formula (head and tail are the same byte range)."""
    f = tmp_path / "tiny.mp4"
    f.write_bytes(b"abc")  # 3 bytes, far below HEAD_TAIL_BYTES
    canonical = hash_file(f)
    res = verify_video(
        path=f,
        expected_size=canonical.size_bytes,
        expected_hash=canonical.content_hash,
    )
    assert res.is_ok(), res.error
    assert res.content_hash == canonical.content_hash


def test_verify_size_mismatch(tmp_path: pathlib.Path) -> None:
    f = tmp_path / "v.mp4"
    f.write_bytes(b"abc")
    res = verify_video(path=f, expected_size=9999)
    assert "size mismatch" in (res.error or "")


def test_verify_hash_mismatch(tmp_path: pathlib.Path) -> None:
    f = tmp_path / "v.mp4"
    f.write_bytes(b"abc")
    res = verify_video(path=f, expected_hash="0" * 64)
    assert res.error == "hash mismatch"
