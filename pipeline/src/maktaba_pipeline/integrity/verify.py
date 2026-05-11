"""Per-video integrity check (Epic 24 plan-24-07).

Runs across the video catalog (nightly or on-demand) and writes one
``integrity_checks`` row per video with:

* file_present — does the path still resolve?
* size_bytes  — current file size
* content_hash — sha256 of the first 16 MiB (matches the scanner's hash)
* segments_count — number of transcript_segments rows
* transcripts_ok — at least one active transcript
* error — non-empty when any of the above fail

A separate sweeper consumes the ``integrity_checks_problems`` partial
index to surface broken videos in the admin UI.
"""

from __future__ import annotations

import dataclasses
import hashlib
import pathlib

# Match the scanner's identity hash window (16 MiB) so verifier output
# can be compared against the row hash in `videos.content_hash`.
HASH_WINDOW_BYTES = 16 * 1024 * 1024


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
    """sha256 of the first 16 MiB of the file. Stable across renames."""
    h = hashlib.sha256()
    remaining = HASH_WINDOW_BYTES
    with path.open("rb") as f:
        while remaining > 0:
            chunk = f.read(min(remaining, 1 << 20))
            if not chunk:
                break
            h.update(chunk)
            remaining -= len(chunk)
    return h.hexdigest()


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
    try:
        digest = hash_file(p)
    except OSError as e:
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
