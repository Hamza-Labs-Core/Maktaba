"""Extract embedded subtitle tracks from video containers.

Two surfaces:

- :func:`list_embedded` — run ffprobe and parse out one
  :class:`EmbeddedSubtitle` per ``codec_type=subtitle`` stream.
  Image-based codecs (``hdmv_pgs_subtitle``, ``dvd_subtitle``) are
  flagged with ``image_based=True`` — the caller can skip them or hand
  them to an OCR stage.
- :func:`extract_embedded` — shell out to ffmpeg to dump one stream as
  SRT or WebVTT. Image-based streams raise :class:`ExtractSubtitleError`
  since ffmpeg cannot convert them losslessly to text.

The parse seam is :func:`parse_subtitle_streams`, which takes ffprobe
JSON directly so tests don't need ffprobe installed.
"""

from __future__ import annotations

import asyncio
import json
import shutil
from collections.abc import Iterable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .formats import SubtitleFormat

__all__ = [
    "EmbeddedSubtitle",
    "ExtractSubtitleError",
    "extract_embedded",
    "list_embedded",
    "parse_subtitle_streams",
]


# Image-based subtitle codecs cannot be converted to text by ffmpeg
# without an OCR pass; we surface them so the caller can skip or route
# them to an OCR pipeline. The list mirrors ffmpeg's
# ``libavcodec/codec_desc.c`` discriminator.
_IMAGE_BASED_CODECS: frozenset[str] = frozenset(
    {
        "hdmv_pgs_subtitle",
        "dvd_subtitle",
        "dvb_subtitle",
        "xsub",
    }
)


class ExtractSubtitleError(RuntimeError):
    """ffmpeg failed (non-zero exit, missing binary, or image-based stream)."""

    def __init__(self, kind: str, *, returncode: int | None = None, stderr_tail: str = "") -> None:
        super().__init__(f"subtitle extract failed: {kind} rc={returncode} tail={stderr_tail!r}")
        self.kind = kind
        self.returncode = returncode
        self.stderr_tail = stderr_tail


@dataclass(slots=True, frozen=True)
class EmbeddedSubtitle:
    """One ``codec_type=subtitle`` stream from the source container.

    ``stream_index`` is the **global** ffmpeg stream index — i.e. the
    value you pass to ``-map 0:N``. Subtitle-only ranks (the rank you
    pass to ``-map 0:s:N``) are exposed via :attr:`subtitle_index`.
    """

    stream_index: int
    subtitle_index: int
    codec: str
    language: str
    title: str | None
    is_default: bool
    is_forced: bool
    image_based: bool


def parse_subtitle_streams(payload: dict[str, Any]) -> list[EmbeddedSubtitle]:
    """Decode an ffprobe ``-show_streams`` payload into subtitle rows."""
    streams = payload.get("streams") or []
    out: list[EmbeddedSubtitle] = []
    subtitle_rank = 0
    for s in streams:
        if s.get("codec_type") != "subtitle":
            continue
        tags = s.get("tags") or {}
        disposition = s.get("disposition") or {}
        codec = s.get("codec_name") or ""
        out.append(
            EmbeddedSubtitle(
                stream_index=int(s.get("index", subtitle_rank)),
                subtitle_index=subtitle_rank,
                codec=codec,
                language=tags.get("language") or "und",
                title=tags.get("title"),
                is_default=int(disposition.get("default") or 0) == 1,
                is_forced=int(disposition.get("forced") or 0) == 1,
                image_based=codec in _IMAGE_BASED_CODECS,
            )
        )
        subtitle_rank += 1
    return out


async def _run_ffprobe(path: str, *, binary: str = "ffprobe") -> dict[str, Any]:
    if shutil.which(binary) is None:
        raise ExtractSubtitleError("ffprobe_not_found")
    proc = await asyncio.create_subprocess_exec(
        binary,
        "-hide_banner",
        "-loglevel",
        "error",
        "-of",
        "json",
        "-show_streams",
        path,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await proc.communicate()
    if proc.returncode != 0:
        raise ExtractSubtitleError(
            "ffprobe_exit",
            returncode=proc.returncode,
            stderr_tail=(stderr or b"").decode("utf-8", "replace")[-2000:],
        )
    try:
        result: dict[str, Any] = json.loads(stdout.decode("utf-8", "replace"))
    except json.JSONDecodeError as exc:
        raise ExtractSubtitleError("ffprobe_json", stderr_tail=str(exc)) from exc
    return result


async def list_embedded(path: str, *, binary: str = "ffprobe") -> list[EmbeddedSubtitle]:
    """Probe ``path`` and return every subtitle stream the container holds."""
    payload = await _run_ffprobe(path, binary=binary)
    return parse_subtitle_streams(payload)


def build_extract_args(
    src: str,
    *,
    subtitle_index: int,
    out_path: str,
    fmt: SubtitleFormat,
    binary: str = "ffmpeg",
) -> list[str]:
    """Compose the ffmpeg command for one extract.

    Uses ``-map 0:s:N`` to pick the subtitle-rank index and
    ``-c:s srt`` or ``webvtt`` to coerce text codecs into the target
    format. ffmpeg refuses image-based codecs (PGS/DVD) here; we filter
    those out before calling.
    """
    codec = "webvtt" if fmt is SubtitleFormat.VTT else "srt"
    return [
        binary,
        "-y",
        "-hide_banner",
        "-loglevel",
        "error",
        "-i",
        src,
        "-map",
        f"0:s:{subtitle_index}",
        "-c:s",
        codec,
        "-f",
        codec,
        out_path,
    ]


async def extract_embedded(
    src: str,
    track: EmbeddedSubtitle,
    *,
    out_path: str | Path,
    fmt: SubtitleFormat = SubtitleFormat.VTT,
    binary: str = "ffmpeg",
) -> Path:
    """Dump one embedded subtitle stream as a text-format file.

    Image-based streams raise :class:`ExtractSubtitleError(kind=
    'image_based')` rather than producing an empty file.
    """
    if track.image_based:
        raise ExtractSubtitleError("image_based")
    if shutil.which(binary) is None:
        raise ExtractSubtitleError("ffmpeg_not_found")
    dest = Path(out_path)
    dest.parent.mkdir(parents=True, exist_ok=True)
    args = build_extract_args(
        src,
        subtitle_index=track.subtitle_index,
        out_path=str(dest),
        fmt=fmt,
        binary=binary,
    )
    proc = await asyncio.create_subprocess_exec(
        *args,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    _, stderr = await proc.communicate()
    if proc.returncode != 0:
        dest.unlink(missing_ok=True)
        raise ExtractSubtitleError(
            "ffmpeg_exit",
            returncode=proc.returncode,
            stderr_tail=(stderr or b"").decode("utf-8", "replace")[-2000:],
        )
    return dest


def filter_text_based(tracks: Iterable[EmbeddedSubtitle]) -> list[EmbeddedSubtitle]:
    """Drop image-based subtitle tracks from a list."""
    return [t for t in tracks if not t.image_based]
