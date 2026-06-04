"""Resumable, verified model downloads.

The downloader fetches a model's files into a destination directory with
production-grade safety:

- **Disk-space precheck** — refuse to start if the volume can't hold the
  model (plus a margin), before a single byte is fetched.
- **Resume** — an interrupted download leaves a ``<file>.part``; the next
  run asks the server to continue from the partial offset (HTTP Range).
  If the server ignores Range, we restart cleanly.
- **SHA256 verification** — when the catalog pins a checksum, the
  completed ``.part`` is verified before it is accepted.
- **Atomic move** — files only appear at their final path via
  :func:`os.replace` *after* download and verification, so a crash never
  leaves a half-written file masquerading as complete.
- **Progress callback** — invoked with ``(downloaded_bytes, total_bytes)``
  across the whole model so the caller can report a single percentage.

The byte transfer is delegated to an injected ``fetcher`` callable. The
default ``http_fetcher`` uses ``huggingface_hub`` (auth + CDN) but is
never exercised in unit tests — tests inject a fake, keeping the suite
offline.
"""

from __future__ import annotations

import hashlib
import os
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol, cast

from .registry import ModelSpec, resolve_url

__all__ = [
    "ChecksumMismatch",
    "DownloadError",
    "Downloader",
    "Fetch",
    "InsufficientDiskSpace",
    "ProgressCallback",
    "http_fetcher",
]

# Called as progress_cb(downloaded_bytes, total_bytes).
ProgressCallback = Callable[[int, int], None]

# How much head-room beyond the model size we insist on, so the volume
# isn't filled to the last byte.
_DISK_MARGIN_BYTES = 256 * 1024 * 1024  # 256 MiB

# Streaming read size for the default HTTP fetcher.
_CHUNK_SIZE = 1024 * 1024  # 1 MiB


class DownloadError(RuntimeError):
    """Base class for download failures."""


class InsufficientDiskSpace(DownloadError):
    """Not enough free space to hold the model."""

    def __init__(self, required: int, available: int) -> None:
        super().__init__(
            f"insufficient disk space: need {required} bytes, have {available} available"
        )
        self.required = required
        self.available = available


class ChecksumMismatch(DownloadError):
    """A downloaded file's SHA256 did not match the pinned checksum."""

    def __init__(self, filename: str, expected: str, got: str) -> None:
        super().__init__(f"checksum mismatch for {filename}: expected {expected}, got {got}")
        self.filename = filename
        self.expected = expected
        self.got = got


@dataclass(slots=True)
class Fetch:
    """One file transfer handed back by a ``fetcher``.

    ``total_size`` is the full file size (even on a partial/resumed
    fetch). ``chunks`` yields bytes starting at ``start_byte``: when the
    server honored a Range request ``start_byte`` equals the resume
    offset; when it didn't (or there was nothing to resume) it is 0 and
    ``chunks`` covers the whole file.
    """

    total_size: int
    chunks: Iterable[bytes]
    start_byte: int = 0


# A fetcher takes a URL and an optional resume offset, returning a Fetch.
Fetcher = Callable[..., Fetch]


class _Usage(Protocol):
    """The slice of ``shutil.disk_usage``'s result we use."""

    free: int


# Returns disk-usage stats for a path (``shutil.disk_usage`` by default).
DiskUsage = Callable[[str], _Usage]


class Downloader:
    """Downloads model files with resume, verification, and atomic move.

    ``fetcher`` and ``disk_usage`` are injectable for testing; production
    defaults to :func:`http_fetcher` and :func:`shutil.disk_usage`.
    """

    def __init__(
        self,
        *,
        fetcher: Fetcher | None = None,
        disk_usage: DiskUsage | None = None,
        disk_margin_bytes: int = _DISK_MARGIN_BYTES,
    ) -> None:
        if disk_usage is None:
            import shutil  # noqa: PLC0415 - stdlib, only needed for the default

            disk_usage = cast(DiskUsage, shutil.disk_usage)
        self._fetcher: Fetcher = fetcher if fetcher is not None else http_fetcher
        self._disk_usage: DiskUsage = disk_usage
        self._disk_margin = disk_margin_bytes

    def download(
        self,
        spec: ModelSpec,
        dest_dir: Path,
        *,
        progress_cb: ProgressCallback | None = None,
    ) -> Path:
        """Fetch every file of ``spec`` into ``dest_dir``; return the dir.

        Raises :class:`InsufficientDiskSpace` before fetching anything if
        the volume is too small, and :class:`ChecksumMismatch` if a
        verified file's digest is wrong.
        """
        dest_dir = Path(dest_dir)
        dest_dir.mkdir(parents=True, exist_ok=True)

        total = spec.size_bytes
        self._check_disk_space(dest_dir, total)

        downloaded_before = 0  # bytes from files already completed this call
        for f in spec.files:
            url = resolve_url(spec, f.filename)
            final = dest_dir / f.filename

            def _file_progress(file_done: int, _base: int = downloaded_before) -> None:
                if progress_cb is not None:
                    progress_cb(_base + file_done, total)

            self._download_file(
                url=url,
                final=final,
                sha256=f.sha256,
                progress_cb=_file_progress,
            )
            downloaded_before += f.size_bytes
            # Snap to the declared size so cumulative progress lands
            # exactly on the total even if a file's real size drifts
            # slightly from the catalog estimate.
            if progress_cb is not None:
                progress_cb(downloaded_before, total)

        return dest_dir

    def _check_disk_space(self, dest_dir: Path, required: int) -> None:
        usage = self._disk_usage(str(dest_dir))
        free = int(usage.free)
        if free < required + self._disk_margin:
            raise InsufficientDiskSpace(required + self._disk_margin, free)

    def _download_file(
        self,
        *,
        url: str,
        final: Path,
        sha256: str | None,
        progress_cb: Callable[[int], None] | None,
    ) -> None:
        final.parent.mkdir(parents=True, exist_ok=True)
        part = final.with_name(final.name + ".part")

        existing = part.stat().st_size if part.exists() else 0
        fetch = self._fetcher(url, resume_from=existing)

        # Decide how to open the part file. If the server resumed from the
        # exact partial offset we append; otherwise we (re)write from the
        # server's start_byte, truncating any stale partial.
        if fetch.start_byte and fetch.start_byte == existing:
            mode = "ab"
            written = existing
        else:
            mode = "wb"
            written = fetch.start_byte  # normally 0

        try:
            with open(part, mode) as fh:
                if mode == "wb" and fetch.start_byte:
                    fh.seek(fetch.start_byte)
                for chunk in fetch.chunks:
                    if not chunk:
                        continue
                    fh.write(chunk)
                    written += len(chunk)
                    if progress_cb is not None:
                        progress_cb(written)

            if sha256 is not None:
                actual = _sha256_file(part)
                if actual != sha256:
                    raise ChecksumMismatch(final.name, sha256, actual)

            os.replace(part, final)
        except BaseException:
            # On any failure (bad checksum, interrupt) drop the partial so
            # a retry starts clean rather than resuming a poisoned file.
            part.unlink(missing_ok=True)
            raise


def _sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(_CHUNK_SIZE), b""):
            h.update(chunk)
    return h.hexdigest()


def http_fetcher(url: str, *, resume_from: int = 0) -> Fetch:  # pragma: no cover - network
    """Default fetcher: stream ``url`` over HTTPS, honoring Range resume.

    Resolves the Hugging Face auth token via ``huggingface_hub`` so gated
    repos (e.g. pyannote) work, then streams the bytes with stdlib
    ``urllib``. Never exercised by unit tests (the suite injects a fake),
    hence the no-cover pragma.
    """
    import urllib.request  # noqa: PLC0415

    headers: dict[str, str] = {}
    if resume_from:
        headers["Range"] = f"bytes={resume_from}-"

    from huggingface_hub import get_token  # noqa: PLC0415

    token = get_token()
    if token:
        headers["Authorization"] = f"Bearer {token}"

    req = urllib.request.Request(url, headers=headers)  # noqa: S310 - https model URL
    resp = urllib.request.urlopen(req, timeout=60)  # noqa: S310
    start = resume_from if resp.status == 206 else 0
    total = _total_from_headers(dict(resp.headers), start)

    def _iter() -> Iterable[bytes]:
        while True:
            chunk = resp.read(_CHUNK_SIZE)
            if not chunk:
                break
            yield chunk

    return Fetch(total_size=total, chunks=_iter(), start_byte=start)


def _total_from_headers(headers: dict[str, str], start: int) -> int:  # pragma: no cover
    cr = headers.get("Content-Range") or headers.get("content-range")
    if cr and "/" in cr:
        try:
            return int(cr.rsplit("/", 1)[-1])
        except ValueError:
            pass
    cl = headers.get("Content-Length") or headers.get("content-length")
    if cl:
        try:
            return start + int(cl)
        except ValueError:
            pass
    return 0
