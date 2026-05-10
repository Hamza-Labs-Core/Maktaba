"""Embedded subtitle extraction via ffmpeg (Story 4.4).

Text-based embedded subtitle streams (subrip, mov_text, ASS, WebVTT)
extract to a single ``.vtt`` per stream under ``.maktaba/subs/``. The
extractor refuses bitmap-based streams (PGS, DVD/DVB SUB, XSUB) — those
need OCR which lives in a later story.

The extractor is async; it spawns ``ffmpeg`` via
``asyncio.create_subprocess_exec`` so the pipeline event loop stays
responsive. Output is gated by ``max_size_bytes`` to defend against
pathological streams.
"""

from __future__ import annotations

import asyncio
import contextlib
from dataclasses import dataclass
from pathlib import Path

__all__ = [
    "BITMAP_CODECS",
    "EmbeddedExtractionResult",
    "EmbeddedExtractor",
    "SubtitleTooLarge",
    "TEXT_CODECS",
    "UnknownSubtitleStream",
    "UnsupportedSubtitleCodec",
]


class UnsupportedSubtitleCodec(Exception):
    """The stream codec is bitmap or otherwise outside ``TEXT_CODECS``."""


class SubtitleTooLarge(Exception):
    """The extracted file exceeds ``max_size_bytes``; we unlinked it."""


class UnknownSubtitleStream(Exception):
    """Caller asked for a stream index that ffmpeg could not resolve."""


# Bitmap codecs would require OCR to turn into text — out of scope
# for Story 4.4 and explicitly rejected at the gate.
BITMAP_CODECS: frozenset[str] = frozenset(
    {"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub"}
)

# Text codecs ffmpeg can transmux to WebVTT without a render pass.
TEXT_CODECS: frozenset[str] = frozenset(
    {"subrip", "webvtt", "ass", "ssa", "mov_text"}
)


@dataclass(frozen=True, slots=True)
class EmbeddedExtractionResult:
    """Outcome of a single :meth:`EmbeddedExtractor.extract` call.

    ``cached=True`` means we returned an existing file without
    re-running ffmpeg. ``bytes`` is the on-disk size at the time of
    extraction; useful as a sanity check at the call site.
    """

    path: Path
    codec: str
    language: str
    cached: bool
    bytes: int


class EmbeddedExtractor:
    """Run ffmpeg to extract one embedded subtitle stream as WebVTT.

    The instance is cheap (no resources held); production callers
    instantiate one per stage invocation. The class is deliberately
    not a singleton so tests can swap in a fake ``ffmpeg_path``.
    """

    def __init__(
        self,
        *,
        ffmpeg_path: str = "ffmpeg",
        max_size_bytes: int = 50 * 1024 * 1024,
    ) -> None:
        self.ffmpeg_path = ffmpeg_path
        self.max_size_bytes = max_size_bytes

    async def extract(
        self,
        *,
        video_path: Path,
        output_dir: Path,
        content_hash: str,
        stream_index: int,
        codec: str,
        language: str,
    ) -> EmbeddedExtractionResult:
        """Extract one stream to ``<output_dir>/<hash>.<lang>.s<i>.embedded.vtt``.

        ``stream_index`` is the *absolute* stream index from
        ``ffprobe`` (e.g. ``2`` in a video that has v0/a1/s2). We use
        it directly as ``-map 0:<index>`` because the caller already
        knows which stream they want; this avoids the need to
        translate between absolute and subtitle-relative indices
        inside the extractor.
        """
        if codec in BITMAP_CODECS:
            raise UnsupportedSubtitleCodec(
                f"bitmap subtitle codec {codec!r} requires OCR (not implemented)"
            )
        if codec not in TEXT_CODECS:
            raise UnsupportedSubtitleCodec(
                f"unsupported subtitle codec {codec!r}"
            )

        out_path = (
            output_dir
            / f"{content_hash}.{language}.s{stream_index}.embedded.vtt"
        )
        if out_path.exists():
            return EmbeddedExtractionResult(
                path=out_path,
                codec=codec,
                language=language,
                cached=True,
                bytes=out_path.stat().st_size,
            )

        cmd: list[str] = [
            self.ffmpeg_path,
            "-y",
            "-i",
            str(video_path),
            "-map",
            f"0:{stream_index}",
            "-c:s",
            "webvtt",
            "-f",
            "webvtt",
            str(out_path),
        ]

        proc = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        _, stderr = await proc.communicate()
        if proc.returncode != 0:
            # ffmpeg can leave a partial file on error; clean it up
            # so the next attempt doesn't see a "cached" empty file.
            with contextlib.suppress(OSError):
                if out_path.exists():
                    out_path.unlink()
            err_text = stderr.decode("utf-8", errors="replace") if stderr else ""
            raise RuntimeError(
                f"ffmpeg failed (exit {proc.returncode}) extracting stream "
                f"{stream_index} from {video_path}: {err_text}"
            )

        size = out_path.stat().st_size
        if size > self.max_size_bytes:
            with contextlib.suppress(OSError):
                out_path.unlink()
            raise SubtitleTooLarge(
                f"extracted subtitle {size} bytes exceeds "
                f"max {self.max_size_bytes} bytes"
            )

        return EmbeddedExtractionResult(
            path=out_path,
            codec=codec,
            language=language,
            cached=False,
            bytes=size,
        )
