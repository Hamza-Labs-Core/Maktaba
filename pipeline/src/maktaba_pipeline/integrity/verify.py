"""Per-video integrity check (Epic 24 plan-24-07).

Runs across the video catalog (nightly or on-demand) and writes one
``integrity_checks`` row per video with:

* file_present — does the path still resolve?
* size_bytes  — current file size
* content_hash — the canonical content identity
  (``BLAKE3(head 4 MiB ‖ tail 4 MiB ‖ u64_le(size))``), the *same*
  digest the SCAN stage writes to ``videos.content_hash`` via
  :func:`maktaba_pipeline.identity.hash_file`. Comparing the live
  digest against the stored row hash is what makes genuine corruption
  detectable.
* segments_count — number of transcript_segments rows
* transcripts_ok — at least one active transcript
* error — non-empty when any of the above fail

A separate sweeper consumes the ``integrity_checks_problems`` partial
index to surface broken videos in the admin UI.
"""

from __future__ import annotations

import dataclasses
import pathlib

from ..identity import hash_file as _canonical_hash_file


@dataclasses.dataclass
class IntegrityResult:
    file_present: bool
    size_bytes: int | None
    content_hash: str | None
    segments_count: int | None
    transcripts_ok: bool | None
    error: str | None

    def is_ok(self) -> bool:
        return self.file_present and self.error is None


def hash_file(path: pathlib.Path) -> str:
    """The canonical content identity for ``path``.

    Delegates to :func:`maktaba_pipeline.identity.hash_file` — the
    *single* implementation the SCAN stage uses when it writes
    ``videos.content_hash`` — so a verifier digest is byte-for-byte
    comparable against the stored row hash. (Previously this computed a
    sha256 of the first 16 MiB, which could never equal the BLAKE3
    head‖tail‖size identity, so corruption detection never fired.)
    """
    return _canonical_hash_file(path).content_hash


def verify_video(
    *,
    path: pathlib.Path | str,
    expected_size: int | None = None,
    expected_hash: str | None = None,
    segments_count: int | None = None,
    transcripts_ok: bool | None = None,
) -> IntegrityResult:
    """Run the integrity checks. Does not touch the DB."""
    p = pathlib.Path(path)
    if not p.exists():
        return IntegrityResult(
            file_present=False,
            size_bytes=None,
            content_hash=None,
            segments_count=segments_count,
            transcripts_ok=transcripts_ok,
            error="missing",
        )
    try:
        size = p.stat().st_size
    except OSError as e:
        return IntegrityResult(
            file_present=True,
            size_bytes=None,
            content_hash=None,
            segments_count=segments_count,
            transcripts_ok=transcripts_ok,
            error=f"stat: {e}",
        )
    if expected_size is not None and size != expected_size:
        return IntegrityResult(
            file_present=True,
            size_bytes=size,
            content_hash=None,
            segments_count=segments_count,
            transcripts_ok=transcripts_ok,
            error=f"size mismatch: expected {expected_size}, got {size}",
        )
    # TOCTOU note: the size stat above and the re-stat inside
    # hash_file are deliberately not locked — this is a best-effort
    # nightly sweeper, so a future reader should not add a lock here.
    try:
        digest = hash_file(p)
    except (OSError, ValueError) as e:
        # ValueError: canonical hasher rejects non-regular files
        # (FIFO/socket/block device/symlink-to-non-regular) — mirror
        # scanner/service.py's proven (OSError, ValueError) defence so
        # one bad path is recorded, not crashed on.
        return IntegrityResult(
            file_present=True,
            size_bytes=size,
            content_hash=None,
            segments_count=segments_count,
            transcripts_ok=transcripts_ok,
            error=f"hash: {e}",
        )
    if expected_hash is not None and digest != expected_hash:
        return IntegrityResult(
            file_present=True,
            size_bytes=size,
            content_hash=digest,
            segments_count=segments_count,
            transcripts_ok=transcripts_ok,
            error="hash mismatch",
        )
    return IntegrityResult(
        file_present=True,
        size_bytes=size,
        content_hash=digest,
        segments_count=segments_count,
        transcripts_ok=transcripts_ok,
        error=None,
    )
