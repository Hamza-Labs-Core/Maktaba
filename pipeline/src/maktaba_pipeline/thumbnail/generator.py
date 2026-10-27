"""FFmpeg thumbnail extraction (Story 7.7).

Produces three artifact families from a source video:

- **poster** — one representative frame at a configurable fraction of
  the duration (default 25 %), the card image the library grid shows.
- **sprite sheet** — a grid of ``columns × rows`` frames sampled evenly
  across the whole video, used by the player's scrubber-preview overlay
  (trickplay, Epic 45 story 45.19), with its index ``sprite.json`` beside
  it: the grid and the seconds between two tiles, which the API serves.
- **chapter thumbnails** — one frame at each chapter's start, so the
  chapter list can render a strip of previews.

The module is pure FFmpeg plumbing. The command lines are built by
:func:`build_poster_args` / :func:`build_sprite_args` so tests can assert
the exact argv, and the subprocess spawn sits behind a ``runner`` DI
seam (mirroring the ``run_probe`` / ``run_extract`` seams in
:mod:`maktaba_pipeline.audio`) so the unit suite never shells out.

Artifacts are written content-addressed under
``~/.maktaba/cache/thumbnails/{content_hash}/`` so a re-thumbnail of an
unchanged file overwrites in place and the terminal-state cleanup pass
can find what to delete (the same convention the audio + subtitle caches
use).
"""

from __future__ import annotations

import asyncio
import json
import os
import shutil
from collections.abc import Awaitable, Callable, Sequence
from contextlib import suppress
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from ..binaries import ffmpeg_binary

__all__ = [
    "DEFAULT_CONFIG",
    "SPRITE_INDEX_VERSION",
    "FrameRunner",
    "ThumbnailConfig",
    "ThumbnailError",
    "ThumbnailSet",
    "build_poster_args",
    "build_sprite_args",
    "generate_chapter_thumbs",
    "generate_poster",
    "generate_sprite",
    "generate_thumbnails",
    "scale_to_width",
    "sprite_index",
    "thumbnail_dir_for",
    "write_sprite_index",
]

# The index's shape; the API reads version 1.
SPRITE_INDEX_VERSION = 1


class ThumbnailError(RuntimeError):
    """Structured thumbnail failure. ``kind`` classifies the cause.

    Mirrors :class:`maktaba_pipeline.audio.extract.ExtractError`: a
    non-zero ffmpeg exit is ``ffmpeg_thumbnail`` (retryable — transient
    I/O / partial write), a missing binary is ``ffmpeg_not_found``.
    """

    def __init__(self, kind: str, *, returncode: int | None = None, stderr_tail: str = "") -> None:
        super().__init__(f"thumbnail failed: {kind} rc={returncode} tail={stderr_tail!r}")
        self.kind = kind
        self.returncode = returncode
        self.stderr_tail = stderr_tail

    def to_envelope(self) -> dict[str, Any]:
        """Render as the JSON shape stored in ``processing_jobs.error``."""
        return {
            "kind": self.kind,
            "returncode": self.returncode,
            "stderr_tail": self.stderr_tail,
        }


@dataclass(slots=True, frozen=True)
class ThumbnailConfig:
    """Knobs for the three artifact families.

    ``poster_timestamp_pct`` is clamped to ``[0, 1)``; a video with an
    unknown duration falls back to a fixed 1 s seek so the poster is
    never the (often black) very first frame.
    """

    poster_timestamp_pct: float = 0.25
    poster_width: int = 640
    # 100 tiles: one every 36 s of an hour-long lesson (story 45.19; the
    # sheets made before it are 5x5 and have no index)
    sprite_columns: int = 10
    sprite_rows: int = 10
    sprite_tile_width: int = 160
    # ``jpg`` keeps the sheet small; the player decodes it once.
    image_format: str = "jpg"
    fallback_seek_sec: float = 1.0


DEFAULT_CONFIG = ThumbnailConfig()

# A ``runner`` takes a fully-formed ffmpeg argv (output path is its last
# element) and is responsible for spawning the process + raising
# ThumbnailError on failure. The default is :func:`_run_ffmpeg`; tests
# inject a fake that writes a stub file instead.
FrameRunner = Callable[[list[str]], Awaitable[None]]

#: The longest one ffmpeg run may take. A sprite of an hour-long file takes seconds
#: (keyframes only); past this the source is not answering.
FFMPEG_TIMEOUT_SEC = 600.0


@dataclass(slots=True, frozen=True)
class ThumbnailSet:
    """What :func:`generate_thumbnails` produced.

    ``chapter_thumbs`` maps a chapter ``seq`` to its image path; empty
    when the video has no chapters.
    """

    poster: Path
    sprite: Path
    chapter_thumbs: dict[int, Path] = field(default_factory=dict)


def thumbnail_dir_for(content_hash: str, *, root: str | os.PathLike[str] | None = None) -> Path:
    """Return ``~/.maktaba/cache/thumbnails/{hash}/`` (or under ``root``)."""
    base = Path(root) if root else Path.home() / ".maktaba" / "cache" / "thumbnails"
    return base / content_hash


def _poster_timestamp(duration_sec: float, cfg: ThumbnailConfig) -> float:
    """Pick the poster seek point.

    ``duration_sec * pct`` when the duration is known and the percentage
    is in range; otherwise the fixed fallback seek so we never grab the
    frame-zero black slate.
    """
    pct = cfg.poster_timestamp_pct
    if duration_sec > 0 and 0.0 <= pct < 1.0:
        return duration_sec * pct
    return cfg.fallback_seek_sec


def scale_to_width(width: int) -> str:
    """The ``scale`` filter that makes a frame ``width`` px wide in its displayed shape.

    The clients show the picture whole in a 16:9 box, with bars where it is
    narrower or wider (a vertical, square or ultra-wide video), so the image
    must have the shape the video plays in. ffmpeg turns a frame by the
    stream's rotation before the filters (a phone's video stored as
    landscape pixels with a 90° rotation comes out portrait; nothing here
    may pass ``-noautorotate``), and ``dar`` (``iw/ih*sar``) puts the
    stretch of non-square pixels into the height: an anamorphic 720x480
    16:9 video is 640x360, not 640x426. The height is kept even
    (libjpeg/yuv420), and ``setsar=1`` marks the result square-pixelled.
    """
    return f"scale={width}:trunc(ow/dar/2)*2,setsar=1"


def build_poster_args(
    src: str,
    timestamp_sec: float,
    output: str,
    *,
    width: int = 640,
    binary: str | None = None,
) -> list[str]:
    """Compose the single-frame ffmpeg command line.

    ``-ss`` precedes ``-i`` for fast (keyframe-accurate) input seek;
    ``-frames:v 1`` grabs exactly one frame. :func:`scale_to_width` keeps
    the displayed aspect ratio, and ``-q:v 2`` is near-visually-lossless
    JPEG. ``-y`` overwrites so a re-run is idempotent.
    """
    return [
        ffmpeg_binary(binary),
        "-hide_banner",
        "-nostdin",
        "-threads",
        "1",
        "-ss",
        f"{max(0.0, timestamp_sec):.3f}",
        "-i",
        src,
        "-frames:v",
        "1",
        "-vf",
        scale_to_width(width),
        "-q:v",
        "2",
        "-y",
        output,
    ]


def build_sprite_args(
    src: str,
    output: str,
    *,
    duration_sec: float,
    columns: int = 5,
    rows: int = 5,
    tile_width: int = 160,
    keyframes_only: bool = True,
    binary: str | None = None,
) -> list[str]:
    """Compose the sprite-sheet ffmpeg command line.

    ``fps=count/duration`` samples one frame every ``duration/count``
    seconds so the ``columns × rows`` grid spans the whole video; the
    ``tile`` filter packs those frames into one image and ``-frames:v 1``
    emits exactly that single packed sheet. A zero/unknown duration
    falls back to 1 fps so the command is still valid (the sheet just
    covers the first ``count`` seconds).

    ``-skip_frame nokey`` (before ``-i``) decodes keyframes only, and the
    ``fps`` filter takes the keyframe nearest each tile's time: a scrub
    preview needs no exact frame. Measured on the NAS, a 10-minute 720p
    video went from 4.4 s to 1.5 s and a 58-minute 480p one from 15.3 s
    to 0.8 s, with complete sheets spanning the whole video.
    """
    count = max(1, columns * rows)
    fps = (count / duration_sec) if duration_sec > 0 else 1.0
    vf = f"fps={fps:.6f},{scale_to_width(tile_width)},tile={columns}x{rows}"
    return [
        ffmpeg_binary(binary),
        "-hide_banner",
        "-nostdin",
        "-threads",
        "1",
        *(["-skip_frame", "nokey"] if keyframes_only else []),
        "-i",
        src,
        "-frames:v",
        "1",
        "-vf",
        vf,
        "-q:v",
        "3",
        "-y",
        output,
    ]


async def _run_ffmpeg(args: list[str], *, timeout_sec: float = FFMPEG_TIMEOUT_SEC) -> None:
    """Default runner: spawn ffmpeg, raise ThumbnailError on failure."""
    if shutil.which(args[0]) is None:
        raise ThumbnailError("ffmpeg_not_found", stderr_tail=args[0])
    proc = await asyncio.create_subprocess_exec(
        *args,
        stdout=asyncio.subprocess.DEVNULL,
        stderr=asyncio.subprocess.PIPE,
        stdin=asyncio.subprocess.DEVNULL,
    )
    output = args[-1]
    # communicate(), not wait(): wait() with stderr on a pipe deadlocks once ffmpeg
    # fills the pipe (a damaged file logs a line per bad frame), and the job then
    # hangs for ever while its heartbeat keeps it looking alive. The timeout bounds a
    # read from a share that stopped answering the same way.
    try:
        _, tail = await asyncio.wait_for(proc.communicate(), timeout=timeout_sec)
    except (TimeoutError, asyncio.TimeoutError) as exc:  # noqa: UP041
        with suppress(ProcessLookupError):
            proc.kill()
        await proc.wait()
        with suppress(FileNotFoundError, OSError):
            Path(output).unlink()
        raise ThumbnailError(
            "ffmpeg_timeout",
            stderr_tail=f"ffmpeg timed out after {timeout_sec:g}s",
        ) from exc
    rc = proc.returncode
    if rc != 0:
        # A partially-written frame is useless; drop it so a retry starts
        # clean and the cache never serves a truncated image.
        with suppress(FileNotFoundError, OSError):
            Path(output).unlink()
        raise ThumbnailError(
            "ffmpeg_thumbnail",
            returncode=rc,
            stderr_tail=(tail or b"").decode("utf-8", "replace")[-2000:],
        )


async def generate_poster(
    src: str,
    *,
    duration_sec: float,
    content_hash: str,
    cfg: ThumbnailConfig = DEFAULT_CONFIG,
    root: str | os.PathLike[str] | None = None,
    binary: str | None = None,
    runner: FrameRunner | None = None,
) -> Path:
    """Extract the poster frame; return its path."""
    run = runner or _run_ffmpeg
    out_dir = thumbnail_dir_for(content_hash, root=root)
    out_dir.mkdir(parents=True, exist_ok=True)
    out = out_dir / f"poster.{cfg.image_format}"
    ts = _poster_timestamp(duration_sec, cfg)
    # A seek past the video's last frame gives ffmpeg nothing to encode (it then fails on
    # the encoder, rc 234): a file whose video track is shorter than the container's
    # duration (a long audio track) or a very short clip. Earlier points, then the start.
    points = [ts, *(p for p in (1.0, 0.0) if p < ts)]
    for i, at in enumerate(points):
        try:
            await run(build_poster_args(src, at, str(out), width=cfg.poster_width, binary=binary))
            return out
        except ThumbnailError as exc:
            if exc.kind != "ffmpeg_thumbnail" or i == len(points) - 1:
                raise
    return out


async def generate_sprite(
    src: str,
    *,
    duration_sec: float,
    content_hash: str,
    cfg: ThumbnailConfig = DEFAULT_CONFIG,
    root: str | os.PathLike[str] | None = None,
    binary: str | None = None,
    runner: FrameRunner | None = None,
) -> Path:
    """Extract the scrubber sprite sheet; return its path."""
    run = runner or _run_ffmpeg
    out_dir = thumbnail_dir_for(content_hash, root=root)
    out_dir.mkdir(parents=True, exist_ok=True)
    out = out_dir / f"sprite.{cfg.image_format}"
    # keyframes only is fast on a long video; a short one can have fewer keyframes than the
    # grid has tiles (or none after the first), and ffmpeg then encodes nothing: every frame
    for keyframes_only in (True, False):
        try:
            await run(
                build_sprite_args(
                    src,
                    str(out),
                    duration_sec=duration_sec,
                    columns=cfg.sprite_columns,
                    rows=cfg.sprite_rows,
                    tile_width=cfg.sprite_tile_width,
                    keyframes_only=keyframes_only,
                    binary=binary,
                )
            )
            write_sprite_index(out, duration_sec, cfg)
            return out
        except ThumbnailError as exc:
            if exc.kind != "ffmpeg_thumbnail" or not keyframes_only:
                raise
    return out


def sprite_index(duration_sec: float, cfg: ThumbnailConfig = DEFAULT_CONFIG) -> dict[str, Any]:
    """The sheet's index: its grid and the seconds between two tiles.

    Tile ``i`` (row-major) is the frame at ``i * interval_sec``, the time the
    ``fps`` filter of :func:`build_sprite_args` sampled; an unknown duration
    sampled at 1 fps. The tile's height follows the video's shape
    (:func:`scale_to_width`), so the reader takes it from the sheet's size.
    """
    count = max(1, cfg.sprite_columns * cfg.sprite_rows)
    interval = (duration_sec / count) if duration_sec > 0 else 1.0
    return {
        "version": SPRITE_INDEX_VERSION,
        "columns": cfg.sprite_columns,
        "rows": cfg.sprite_rows,
        "count": count,
        "interval_sec": round(interval, 6),
        "tile_width": cfg.sprite_tile_width,
        "duration_sec": round(max(0.0, duration_sec), 3),
    }


def write_sprite_index(sprite: Path, duration_sec: float, cfg: ThumbnailConfig) -> Path:
    """Write ``sprite.json`` beside the sheet (atomically: a reader never sees half)."""
    out = sprite.with_suffix(".json")
    tmp = out.with_name(out.name + ".tmp")
    tmp.write_text(json.dumps(sprite_index(duration_sec, cfg)), encoding="utf-8")
    tmp.replace(out)
    return out


async def generate_chapter_thumbs(
    src: str,
    *,
    chapters: Sequence[tuple[int, float]],
    content_hash: str,
    cfg: ThumbnailConfig = DEFAULT_CONFIG,
    root: str | os.PathLike[str] | None = None,
    binary: str | None = None,
    runner: FrameRunner | None = None,
) -> dict[int, Path]:
    """Extract one frame per chapter start.

    ``chapters`` is ``(seq, start_sec)`` pairs. Returns ``{seq: path}``.
    """
    run = runner or _run_ffmpeg
    out_dir = thumbnail_dir_for(content_hash, root=root)
    out_dir.mkdir(parents=True, exist_ok=True)
    out: dict[int, Path] = {}
    for seq, start_sec in chapters:
        dest = out_dir / f"chapter-{seq}.{cfg.image_format}"
        args = build_poster_args(src, start_sec, str(dest), width=cfg.poster_width, binary=binary)
        await run(args)
        out[seq] = dest
    return out


async def generate_thumbnails(
    src: str,
    *,
    duration_sec: float,
    content_hash: str,
    chapters: Sequence[tuple[int, float]] = (),
    cfg: ThumbnailConfig = DEFAULT_CONFIG,
    root: str | os.PathLike[str] | None = None,
    binary: str | None = None,
    runner: FrameRunner | None = None,
) -> ThumbnailSet:
    """Generate poster + sprite + chapter thumbnails for ``src``."""
    poster = await generate_poster(
        src,
        duration_sec=duration_sec,
        content_hash=content_hash,
        cfg=cfg,
        root=root,
        binary=binary,
        runner=runner,
    )
    sprite = await generate_sprite(
        src,
        duration_sec=duration_sec,
        content_hash=content_hash,
        cfg=cfg,
        root=root,
        binary=binary,
        runner=runner,
    )
    chapter_thumbs = (
        await generate_chapter_thumbs(
            src,
            chapters=chapters,
            content_hash=content_hash,
            cfg=cfg,
            root=root,
            binary=binary,
            runner=runner,
        )
        if chapters
        else {}
    )
    return ThumbnailSet(poster=poster, sprite=sprite, chapter_thumbs=chapter_thumbs)
