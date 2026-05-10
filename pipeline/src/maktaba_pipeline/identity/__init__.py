"""Content-addressable identity for Maktaba videos (Story 1.2).

A video's canonical identity is a 64-character lowercase hex string
computed from its bytes:

    BLAKE3(first 4 MiB || last 4 MiB || u64_le(size))

The head+tail+size shape is what makes the hash cheap (≤ 8 MiB read off
disk regardless of file size) while still being sensitive to single-byte
flips at the start, end, or to size changes — see :mod:`.hasher` for
the full algorithm and known limitations.

This package exposes:

- :data:`HEAD_TAIL_BYTES` — the per-region byte budget (4 MiB).
- :func:`hash_file` — open a path, return ``(content_hash, size_bytes)``.
- :func:`hash_reader` — same algorithm against an arbitrary file-like
  (used by tests with ``io.BytesIO`` and counting wrappers).
- :class:`HashResult` — the return shape of :func:`hash_file`.
- :class:`FileSignature` — ``(size_bytes, mtime_ns)`` snapshot used by
  the scanner to skip rehashing when a file's metadata is unchanged.
- :func:`file_signature` — read the signature from a path on disk.
"""

from __future__ import annotations

from .hasher import (
    HEAD_TAIL_BYTES,
    FileSignature,
    HashResult,
    file_signature,
    hash_file,
    hash_reader,
)

__all__ = [
    "HEAD_TAIL_BYTES",
    "FileSignature",
    "HashResult",
    "file_signature",
    "hash_file",
    "hash_reader",
]
