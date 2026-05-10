"""BLAKE3 head+tail+size content hasher.

The canonical formula, applied uniformly at every file size, is:

    content_hash = BLAKE3( first_HT_bytes || last_HT_bytes || size_le_u64 )

where ``HT = min(HEAD_TAIL_BYTES, size)``. For files smaller than
:data:`HEAD_TAIL_BYTES` the head and tail are the same byte range; we
still write the buffer to the hasher twice so the formula stays uniform
across the size = 2*HEAD_TAIL_BYTES boundary.

Known limitations (documented and accepted in the story spec):

- Two large files with identical head + tail + size but different
  *middle* bytes collide. This is the price of bounded I/O. The scanner
  treats this as content-equivalent on purpose.
- Sparse holes are read as zero bytes. The size suffix prevents two
  different-size sparse files from colliding.

The zero-byte path is special: nothing to read, only the size suffix
(eight zero bytes) is fed to the hasher.
"""

from __future__ import annotations

import os
import stat
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol

from blake3 import blake3


class _Seekable(Protocol):
    """Minimal binary-stream shape the hasher needs.

    Narrower than ``typing.IO[bytes]`` so test wrappers don't have to
    implement the full ``BinaryIO`` API just to record bytes. Anything
    that exposes ``read(n) -> bytes`` and ``seek(offset)`` qualifies —
    open files, ``io.BytesIO``, and the counting/short-reading wrappers
    in the test suite all match.
    """

    def read(self, size: int = ..., /) -> bytes: ...

    def seek(self, offset: int, whence: int = 0, /) -> int: ...


__all__ = [
    "HEAD_TAIL_BYTES",
    "SIZE_SUFFIX_LEN",
    "FileSignature",
    "HashResult",
    "file_signature",
    "hash_file",
    "hash_reader",
]


HEAD_TAIL_BYTES: int = 4 * 1024 * 1024
"""Per-region byte budget. Total disk read for a large file: 2 × this."""

SIZE_SUFFIX_LEN: int = 8
"""Width of the little-endian uint64 size suffix appended to the hash."""


@dataclass(slots=True, frozen=True)
class HashResult:
    """Pair returned by :func:`hash_file`.

    ``content_hash`` is the 64-char lowercase hex digest;
    ``size_bytes`` is the value the hash already incorporated, captured
    so callers can persist it without a second ``stat()`` call.
    """

    content_hash: str
    size_bytes: int


@dataclass(slots=True, frozen=True)
class FileSignature:
    """Stable subset of stat metadata used to skip rehashing.

    The scanner stores the signature alongside each ``videos`` row.
    When a periodic full sweep revisits a path, it compares the live
    ``file_signature(path)`` to the stored one; on equality the row is
    reused as-is (no open, no read, no rehash). The intent is the
    "Reuse on (path, size, mtime) unchanged" optimisation called out in
    Story 1.2.

    ``mtime_ns`` is nanosecond resolution because that's what
    ``os.stat_result.st_mtime_ns`` exposes; truncating to seconds would
    lose detail on filesystems that record sub-second mtimes.
    """

    size_bytes: int
    mtime_ns: int


def file_signature(path: str | os.PathLike[str]) -> FileSignature:
    """Read ``(size_bytes, mtime_ns)`` from ``path``."""
    st = os.stat(path)
    return FileSignature(size_bytes=st.st_size, mtime_ns=st.st_mtime_ns)


def hash_file(
    path: str | os.PathLike[str],
    *,
    head_tail: int = HEAD_TAIL_BYTES,
) -> HashResult:
    """Open ``path`` read-only, hash it, return the digest and size.

    ``head_tail`` is exposed so the ``< 8 MiB`` boundary can be
    exercised in tests without writing 8 MiB fixtures; production
    callers leave it at the default.

    Raises :class:`ValueError` if the path is not a regular file
    (FIFO, socket, block device, …) — the scanner walks symlinks via
    its own discipline, so by the time identity sees a path it is
    expected to refer to a regular file.
    """
    p = Path(path)
    st = os.stat(p)
    if not stat.S_ISREG(st.st_mode):
        raise ValueError(f"identity: not a regular file: {p}")
    size = st.st_size
    with open(p, "rb") as f:
        digest = hash_reader(f, size, head_tail=head_tail)
    return HashResult(content_hash=digest, size_bytes=size)


def hash_reader(
    reader: _Seekable,
    size: int,
    *,
    head_tail: int = HEAD_TAIL_BYTES,
) -> str:
    """Hash a binary file-like, given its total ``size``.

    The reader must support ``.seek(offset)`` and ``.read(n)``;
    ``io.BytesIO``, ``open(..., 'rb')`` handles, and the test
    counting wrappers all qualify.

    The function makes at most two seeks and two reads of ``head_tail``
    bytes each (or one read of ``size`` bytes when ``size <
    head_tail``), then one short write of the size suffix to the
    hasher. No buffering beyond the two region buffers — peak memory
    is ``2 × head_tail`` bytes.
    """
    if size < 0:
        raise ValueError(f"identity: negative size: {size}")
    if head_tail <= 0:
        raise ValueError(f"identity: head_tail must be positive, got {head_tail}")

    h = blake3()

    if size > 0:
        ht = min(head_tail, size)

        reader.seek(0)
        head = _read_exactly(reader, ht)
        h.update(head)

        tail_offset = size - ht
        if tail_offset == 0:
            # size <= head_tail: head and tail are the same byte range.
            # Honour the formula by feeding the buffer to the hasher a
            # second time. This keeps the algorithm uniform across the
            # size = head_tail boundary; without the second write a
            # 4 MiB file and a 4 MiB+1 file would not produce the same
            # shape of input to BLAKE3.
            h.update(head)
        else:
            reader.seek(tail_offset)
            tail = _read_exactly(reader, ht)
            h.update(tail)

    # Size suffix is fed to the hasher even for empty files — that's
    # what makes the empty-file hash deterministic and distinct from
    # any other size's hash.
    h.update(size.to_bytes(SIZE_SUFFIX_LEN, "little", signed=False))

    return h.hexdigest()


def _read_exactly(reader: _Seekable, n: int) -> bytes:
    """Read exactly ``n`` bytes or raise ``OSError``.

    ``IO.read(n)`` is allowed to return fewer than ``n`` bytes on
    network filesystems and pipes; this helper loops until either the
    request is satisfied or the stream is exhausted (which we treat as
    an error — the scanner expects a stable byte count from
    ``os.stat``).
    """
    if n == 0:
        return b""
    chunks: list[bytes] = []
    remaining = n
    while remaining > 0:
        chunk = reader.read(remaining)
        if not chunk:
            raise OSError(f"identity: unexpected EOF after {n - remaining} of {n} bytes")
        chunks.append(chunk)
        remaining -= len(chunk)
    if len(chunks) == 1:
        return chunks[0]
    return b"".join(chunks)
