"""Behaviour tests for :mod:`maktaba_pipeline.identity.hasher` (Story 1.2).

The scanner depends on a content hash that is

  * deterministic (same bytes → same hex);
  * sensitive to size, head bytes, and tail bytes;
  * cheap to compute (≤ 8 MiB I/O regardless of file size);
  * stable across path renames (path is not part of the hash input).

Each acceptance criterion in story-01-02 maps to one or more tests
here. The tests run as the ``unit`` tier — no network, no DB, only the
local filesystem under ``tmp_path``.
"""

from __future__ import annotations

import io
import os
import shutil
from pathlib import Path

import pytest
from blake3 import blake3

from maktaba_pipeline.identity import (
    HEAD_TAIL_BYTES,
    HashResult,
    file_signature,
    hash_file,
    hash_reader,
)
from maktaba_pipeline.identity.hasher import SIZE_SUFFIX_LEN

MiB = 1024 * 1024


def _hex_from_parts(*parts: bytes) -> str:
    """Reference implementation used to pin the canonical formula."""
    h = blake3()
    for p in parts:
        h.update(p)
    return h.hexdigest()


def _size_suffix(size: int) -> bytes:
    return size.to_bytes(SIZE_SUFFIX_LEN, "little", signed=False)


@pytest.mark.unit
def test_hash_is_deterministic(tmp_path: Path) -> None:
    body = b"\xab" * (16 * MiB)
    p = tmp_path / "f.bin"
    p.write_bytes(body)

    a = hash_file(p)
    b = hash_file(p)
    assert a == b
    assert isinstance(a, HashResult)
    assert len(a.content_hash) == 64
    assert a.size_bytes == 16 * MiB


@pytest.mark.unit
def test_hash_is_lowercase_hex(tmp_path: Path) -> None:
    p = tmp_path / "f.bin"
    p.write_bytes(b"hello")
    res = hash_file(p)
    assert all(c in "0123456789abcdef" for c in res.content_hash)
    assert len(res.content_hash) == 64


@pytest.mark.unit
def test_hash_handles_small_file_via_head_tail_formula(tmp_path: Path) -> None:
    """File < HEAD_TAIL: head and tail collapse to the same byte range."""
    body = b"\xcd" * MiB  # 1 MiB << 4 MiB HEAD_TAIL
    p = tmp_path / "small.bin"
    p.write_bytes(body)

    got = hash_file(p).content_hash

    # Reference: head and tail are the same buffer; both writes happen.
    want = _hex_from_parts(body, body, _size_suffix(len(body)))
    assert got == want


@pytest.mark.unit
def test_hash_zero_byte_file(tmp_path: Path) -> None:
    """Empty file: only the size-suffix (eight zero bytes) is hashed."""
    p = tmp_path / "zero.bin"
    p.write_bytes(b"")

    res = hash_file(p)
    assert res.size_bytes == 0
    assert res.content_hash == _hex_from_parts(_size_suffix(0))


@pytest.mark.unit
def test_hash_changes_on_size_change(tmp_path: Path) -> None:
    """Appending a single byte must flip the hash even if head/tail bytes match."""
    base = b"\xee" * (12 * MiB)
    a = tmp_path / "a.bin"
    b = tmp_path / "b.bin"
    a.write_bytes(base)
    b.write_bytes(base + b"\x00")  # one extra byte

    ha = hash_file(a).content_hash
    hb = hash_file(b).content_hash
    assert ha != hb, "size suffix did not affect hash"


@pytest.mark.unit
def test_hash_invariant_under_path_change(tmp_path: Path) -> None:
    body = b"\xa5" * (10 * MiB)
    a = tmp_path / "before.bin"
    a.write_bytes(body)
    h_before = hash_file(a).content_hash

    moved = tmp_path / "after_rename.bin"
    shutil.move(str(a), str(moved))

    h_after = hash_file(moved).content_hash
    assert h_before == h_after


@pytest.mark.unit
def test_hash_changes_when_head_byte_flips(tmp_path: Path) -> None:
    """A byte change in the head region must produce a different hash."""
    body = bytearray(b"\xa5" * (16 * MiB))
    p_a = tmp_path / "a.bin"
    p_a.write_bytes(bytes(body))

    body[100] = 0x42  # offset 100 is well within the 4 MiB head
    p_b = tmp_path / "b.bin"
    p_b.write_bytes(bytes(body))

    assert hash_file(p_a).content_hash != hash_file(p_b).content_hash


@pytest.mark.unit
def test_hash_changes_when_tail_byte_flips(tmp_path: Path) -> None:
    """A byte change in the tail region must produce a different hash."""
    body = bytearray(b"\xa5" * (16 * MiB))
    p_a = tmp_path / "a.bin"
    p_a.write_bytes(bytes(body))

    # Last byte: definitely in the tail.
    body[-1] = 0x42
    p_b = tmp_path / "b.bin"
    p_b.write_bytes(bytes(body))

    assert hash_file(p_a).content_hash != hash_file(p_b).content_hash


@pytest.mark.unit
def test_hash_documents_middle_collision_limitation(tmp_path: Path) -> None:
    """Two large files with same head/tail/size collide.

    Documented trade-off (story-01-02 §"Edge cases"): the algorithm
    reads only the first/last 4 MiB plus size, so middle-byte
    differences in files larger than 8 MiB are invisible. This test
    exists so we notice if anyone "fixes" it without updating the spec.
    """
    head = b"\x11" * (4 * MiB)
    tail = b"\x22" * (4 * MiB)
    mid_a = b"\xaa" * (4 * MiB)
    mid_b = b"\xbb" * (4 * MiB)

    a = tmp_path / "a.bin"
    b = tmp_path / "b.bin"
    a.write_bytes(head + mid_a + tail)
    b.write_bytes(head + mid_b + tail)

    assert hash_file(a).content_hash == hash_file(b).content_hash, (
        "head/tail/size collision is a documented limitation; "
        "if this fired the algorithm changed silently"
    )


@pytest.mark.unit
def test_hash_at_head_tail_boundary(tmp_path: Path) -> None:
    """Files at exactly HEAD_TAIL bytes hash via the small-file branch."""
    # Use a smaller head_tail so we can write a fixture cheaply.
    ht = 64 * 1024  # 64 KiB
    body = bytes(range(256)) * (ht // 256)  # exactly ht bytes
    assert len(body) == ht

    p = tmp_path / "boundary.bin"
    p.write_bytes(body)

    got = hash_file(p, head_tail=ht).content_hash
    want = _hex_from_parts(body, body, _size_suffix(ht))
    assert got == want


@pytest.mark.unit
def test_hash_just_above_head_tail_boundary(tmp_path: Path) -> None:
    """At ``HEAD_TAIL + 1`` bytes, head and tail overlap by 1 byte."""
    ht = 64 * 1024
    head = bytes(range(256)) * (ht // 256)
    body = head + b"X"  # ht + 1
    assert len(body) == ht + 1

    p = tmp_path / "above.bin"
    p.write_bytes(body)

    got = hash_file(p, head_tail=ht).content_hash
    # head: bytes [0, ht); tail: bytes [1, ht+1) — overlap by ht-1.
    want = _hex_from_parts(body[:ht], body[1:], _size_suffix(len(body)))
    assert got == want


class _CountingReader:
    """File-like wrapper that records every byte returned by ``read``.

    Used by :func:`test_hash_io_budget_for_large_sparse_file` to assert
    that hashing a 30 GiB sparse file actually only reads ≤ 8 MiB. We
    can't trust the OS-level page cache to tell us this, so we wrap
    the underlying file and accumulate ``read`` lengths.
    """

    def __init__(self, inner: io.BufferedReader):
        self._inner = inner
        self.bytes_read = 0

    def read(self, size: int = -1, /) -> bytes:
        data = self._inner.read(size)
        self.bytes_read += len(data)
        return data

    def seek(self, offset: int, whence: int = 0, /) -> int:
        return self._inner.seek(offset, whence)


@pytest.mark.unit
def test_hash_io_budget_for_large_sparse_file(tmp_path: Path) -> None:
    """A 30 GiB sparse file is hashed by reading at most 2*HEAD_TAIL bytes.

    We allocate the file with ``truncate`` (sparse — no actual disk
    space) and then count the bytes our reader actually returns.
    """
    p = tmp_path / "sparse.bin"
    size = 30 * 1024 * MiB  # 30 GiB
    with open(p, "wb") as f:
        f.truncate(size)

    with open(p, "rb") as f:
        counter = _CountingReader(f)
        digest = hash_reader(counter, size)

    assert len(digest) == 64
    assert counter.bytes_read <= 2 * HEAD_TAIL_BYTES, (
        f"I/O budget violated: read {counter.bytes_read} bytes, max {2 * HEAD_TAIL_BYTES}"
    )
    # And specifically: exactly 2 * HEAD_TAIL_BYTES (head + tail).
    assert counter.bytes_read == 2 * HEAD_TAIL_BYTES


@pytest.mark.unit
def test_hash_returns_size_from_stat(tmp_path: Path) -> None:
    body = b"x" * 12345
    p = tmp_path / "f.bin"
    p.write_bytes(body)
    res = hash_file(p)
    assert res.size_bytes == 12345


@pytest.mark.unit
def test_hash_rejects_non_regular_file(tmp_path: Path) -> None:
    fifo = tmp_path / "fifo"
    try:
        os.mkfifo(fifo)
    except (AttributeError, OSError):
        pytest.skip("FIFOs not supported on this platform")
    try:
        with pytest.raises(ValueError, match="not a regular file"):
            hash_file(fifo)
    finally:
        os.unlink(fifo)


@pytest.mark.unit
def test_hash_reader_rejects_negative_size() -> None:
    with pytest.raises(ValueError, match="negative size"):
        hash_reader(io.BytesIO(b"x"), -1)


@pytest.mark.unit
def test_hash_reader_rejects_non_positive_head_tail() -> None:
    with pytest.raises(ValueError, match="head_tail"):
        hash_reader(io.BytesIO(b"x"), 1, head_tail=0)


@pytest.mark.unit
def test_hash_reader_handles_short_reads() -> None:
    """Network filesystems can return fewer bytes than requested.

    The hasher must keep asking until it has the whole buffer (or the
    stream genuinely ends). A short-reading wrapper exercises the
    inner ``while remaining > 0`` loop.
    """

    class ShortReadStream:
        def __init__(self, payload: bytes):
            self._payload = payload
            self._pos = 0

        def read(self, size: int = -1, /) -> bytes:
            # Always return at most one byte — forces the loop to run.
            if size <= 0:
                return b""
            chunk = self._payload[self._pos : self._pos + 1]
            self._pos += len(chunk)
            return chunk

        def seek(self, offset: int, whence: int = 0, /) -> int:
            if whence == 0:
                self._pos = offset
            elif whence == 1:
                self._pos += offset
            elif whence == 2:
                self._pos = len(self._payload) + offset
            return self._pos

    body = b"hello-world-" * 1000  # 12 KiB
    stream = ShortReadStream(body)

    # Use a tiny head_tail so it's smaller than the body but still
    # exercises the head+tail split.
    digest = hash_reader(stream, len(body), head_tail=512)
    assert len(digest) == 64

    # Cross-check against the canonical formula.
    expected = _hex_from_parts(body[:512], body[len(body) - 512 :], _size_suffix(len(body)))
    assert digest == expected


@pytest.mark.unit
def test_hash_reader_raises_on_unexpected_eof() -> None:
    """If the stream lies about size, the hasher must error, not silently truncate."""
    # Stream has 100 bytes but caller claims 1000 — must raise.
    stream = io.BytesIO(b"x" * 100)
    with pytest.raises(OSError, match="unexpected EOF"):
        hash_reader(stream, 1000, head_tail=200)


@pytest.mark.unit
def test_file_signature_captures_size_and_mtime(tmp_path: Path) -> None:
    p = tmp_path / "f.bin"
    p.write_bytes(b"hello")
    sig = file_signature(p)
    assert sig.size_bytes == 5
    assert sig.mtime_ns == os.stat(p).st_mtime_ns


@pytest.mark.unit
def test_file_signature_equality_for_unchanged_file(tmp_path: Path) -> None:
    p = tmp_path / "f.bin"
    p.write_bytes(b"hello")
    s1 = file_signature(p)
    s2 = file_signature(p)
    assert s1 == s2


@pytest.mark.unit
def test_file_signature_changes_after_write(tmp_path: Path) -> None:
    p = tmp_path / "f.bin"
    p.write_bytes(b"hello")
    before = file_signature(p)

    p.write_bytes(b"hello-x")  # size change

    # Bump the mtime explicitly AFTER the write — write_bytes resets
    # mtime to "now," and on fast/low-resolution filesystems "now"
    # can collide with `before.mtime_ns`. Forcing an explicit +10 s
    # post-write guarantees a distinct mtime regardless of clock
    # resolution.
    new_mtime_ns = before.mtime_ns + 10_000_000_000  # +10 s in ns
    os.utime(p, ns=(new_mtime_ns, new_mtime_ns))

    after = file_signature(p)
    assert before != after
    assert after.size_bytes == 7
    assert after.mtime_ns != before.mtime_ns


@pytest.mark.unit
def test_hash_file_pathlike_accepts_str_and_path(tmp_path: Path) -> None:
    """``hash_file`` accepts both ``str`` and ``pathlib.Path``."""
    p = tmp_path / "f.bin"
    p.write_bytes(b"hello")
    a = hash_file(str(p))
    b = hash_file(p)
    assert a == b
