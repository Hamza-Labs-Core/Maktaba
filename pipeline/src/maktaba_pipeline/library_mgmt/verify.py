"""Prove a new media file plays before it replaces anything (Epic 47, story 47.3).

FileUtilities' check (the file exists, is not empty, and ffprobe's duration matches the
input) is the floor; a file whose middle is corrupt passes it. This adds:

- ``ffprobe -show_streams -show_format`` reads the file: a video stream (unless the
  original had none), at least as many audio streams as the original, the same width and
  height, the same video codec for a remux, and a duration within ``max(0.5 s, 0.5 %)``;
- a **full decode**, ``ffmpeg -v error -xerror -i new -map 0:v:0 -map 0:a? -f null -``,
  which must exit 0 with nothing on stderr (every frame read once);
- the **frame count** of the first video stream (``-count_packets``) within 1 of the
  original's for a remux, within 0.5 % for a re-encode;
- for an MP4, the **moov atom before mdat** (``+faststart``), so a browser starts playing
  without reading to the end.

The result (``ok``, the ``reasons`` it failed, and what was ``measured``) is stored in
``file_actions.verify``, so the failures page can say why a file was not swapped. Every
subprocess goes through :func:`maktaba_pipeline.engine.media.run_media` (tests install a
fake with ``set_runner``).
"""

from __future__ import annotations

import json
import os
import shutil
import struct
from collections import Counter
from dataclasses import dataclass, field
from typing import Any, Literal

from ..engine import media

__all__ = [
    "Mode",
    "ProbeInfo",
    "StreamInfo",
    "VerifyResult",
    "count_frames",
    "decode_errors",
    "duration_tolerance",
    "moov_before_mdat",
    "probe",
    "verify_media",
]

#: ``remux``: every stream copied; ``audio``: the video copied, the audio re-encoded;
#: ``reencode``: the video re-encoded (lossy)
Mode = Literal["remux", "audio", "reencode"]

MP4_EXTS = (".mp4", ".m4v", ".mov")


@dataclass(frozen=True, slots=True)
class StreamInfo:
    index: int
    codec_type: str
    codec_name: str | None
    width: int | None = None
    height: int | None = None
    nb_frames: int | None = None


@dataclass(frozen=True, slots=True)
class ProbeInfo:
    path: str
    format_name: str | None
    duration: float | None
    size: int | None
    streams: tuple[StreamInfo, ...]

    @property
    def video(self) -> tuple[StreamInfo, ...]:
        return tuple(s for s in self.streams if s.codec_type == "video" and not _is_cover(s))

    @property
    def audio(self) -> tuple[StreamInfo, ...]:
        return tuple(s for s in self.streams if s.codec_type == "audio")

    def as_json(self) -> dict[str, Any]:
        v = self.video[0] if self.video else None
        return {
            "format": self.format_name,
            "duration": self.duration,
            "size": self.size,
            "video_codec": None if v is None else v.codec_name,
            "width": None if v is None else v.width,
            "height": None if v is None else v.height,
            "audio_streams": len(self.audio),
            "audio_codecs": [a.codec_name for a in self.audio],
        }


def _is_cover(s: StreamInfo) -> bool:
    # an attached picture (a cover image in an MKV or MP4) is a "video" stream to ffprobe
    return s.codec_name in ("mjpeg", "png", "bmp") and (s.nb_frames or 0) <= 1


@dataclass
class VerifyResult:
    ok: bool
    reasons: list[str] = field(default_factory=list)
    measured: dict[str, Any] = field(default_factory=dict)

    def as_json(self) -> dict[str, Any]:
        return {"ok": self.ok, "reasons": list(self.reasons), "measured": dict(self.measured)}


def _int(v: Any) -> int | None:
    try:
        return None if v is None else int(v)
    except (TypeError, ValueError):
        return None


def _float(v: Any) -> float | None:
    try:
        return None if v is None else float(v)
    except (TypeError, ValueError):
        return None


async def probe(path: str, *, timeout: float = 120.0) -> ProbeInfo | None:
    """ffprobe's streams and format of ``path``; None when ffprobe cannot read it."""
    res = await media.run_media(
        [
            media.FFPROBE,
            "-v",
            "error",
            "-show_streams",
            "-show_format",
            "-of",
            "json",
            path,
        ],
        timeout=timeout,
    )
    if res.returncode != 0:
        return None
    try:
        doc = json.loads(res.stdout or b"{}")
    except ValueError:
        return None
    fmt = doc.get("format") or {}
    streams = tuple(
        StreamInfo(
            index=int(s.get("index", i)),
            codec_type=str(s.get("codec_type") or ""),
            codec_name=s.get("codec_name"),
            width=_int(s.get("width")),
            height=_int(s.get("height")),
            nb_frames=_int(s.get("nb_frames")),
        )
        for i, s in enumerate(doc.get("streams") or [])
    )
    if not streams and not fmt:
        return None
    return ProbeInfo(
        path=path,
        format_name=fmt.get("format_name"),
        duration=_float(fmt.get("duration")),
        size=_int(fmt.get("size")),
        streams=streams,
    )


async def count_frames(path: str, *, timeout: float = 600.0) -> int | None:
    """Packets of the first video stream (``-count_packets``: a demux, no decode)."""
    res = await media.run_media(
        [
            media.FFPROBE,
            "-v",
            "error",
            "-select_streams",
            "v:0",
            "-count_packets",
            "-show_entries",
            "stream=nb_read_packets",
            "-of",
            "csv=p=0",
            path,
        ],
        timeout=timeout,
    )
    if res.returncode != 0:
        return None
    text = res.stdout.decode("utf-8", errors="replace").strip().splitlines()
    return _int(text[0].strip().rstrip(",")) if text else None


async def decode_errors(
    path: str, *, has_video: bool = True, timeout: float = 3600.0, nice: bool = False
) -> str | None:
    """Decode every frame once; None when ffmpeg exits 0 with an empty stderr, else what
    it said (or its exit code)."""
    maps = ["-map", "0:v:0", "-map", "0:a?"] if has_video else ["-map", "0:a"]
    args = [media.FFMPEG, "-nostdin", "-v", "error", "-xerror", "-i", path, *maps]
    args += ["-f", "null", "-"]
    if nice and shutil.which("nice"):
        args = ["nice", "-n", "10", *args]
    res = await media.run_media(args, timeout=timeout)
    err = res.text_err.strip()
    if res.returncode == 0 and not err:
        return None
    return err[-2000:] if err else f"ffmpeg exited {res.returncode}"


def moov_before_mdat(path: str) -> bool | None:
    """Whether the top-level ``moov`` box comes before ``mdat`` (faststart); None when
    the file is not an ISO-BMFF file or has neither box."""
    order: list[bytes] = []
    try:
        size_total = os.path.getsize(path)
        with open(path, "rb") as f:
            pos = 0
            while pos + 8 <= size_total and len(order) < 2:
                f.seek(pos)
                head = f.read(8)
                if len(head) < 8:
                    break
                size, kind = struct.unpack(">I4s", head)
                if size == 1:
                    ext = f.read(8)
                    if len(ext) < 8:
                        break
                    size = struct.unpack(">Q", ext)[0]
                elif size == 0:
                    size = size_total - pos
                if size < 8:
                    return None
                if kind in (b"moov", b"mdat"):
                    order.append(kind)
                pos += size
    except OSError:
        return None
    if not order:
        return None
    return order[0] == b"moov"


def duration_tolerance(original: float) -> float:
    """``max(0.5 s, 0.5 %)`` of the original's duration."""
    return max(0.5, 0.005 * original)


async def verify_media(
    new: str,
    original: str | ProbeInfo | None,
    *,
    mode: Mode = "remux",
    original_frames: int | None = None,
    require_faststart: bool | None = None,
    full_decode: bool = True,
    count: bool = True,
    nice: bool = False,
    decode_timeout: float | None = None,
) -> VerifyResult:
    """Check ``new`` against ``original`` (a path, an earlier probe, or None for no
    comparison: a download). ``require_faststart`` defaults to whether ``new`` is an
    MP4. ``original_frames`` saves counting the original again."""
    reasons: list[str] = []
    measured: dict[str, Any] = {"mode": mode}
    if not os.path.isfile(new) or os.path.getsize(new) == 0:
        return VerifyResult(False, ["the new file is missing or empty"], measured)
    info = await probe(new)
    if info is None:
        return VerifyResult(False, ["ffprobe cannot read the new file"], measured)
    measured["new"] = info.as_json()
    orig: ProbeInfo | None
    if isinstance(original, str):
        orig = await probe(original)
        if orig is None:
            reasons.append("ffprobe cannot read the original")
    else:
        orig = original
    if orig is not None:
        measured["original"] = orig.as_json()

    # streams
    want_video = orig is None or bool(orig.video)
    if want_video and not info.video:
        reasons.append("no video stream")
    if orig is not None and len(info.audio) < len(orig.audio):
        reasons.append(f"{len(info.audio)} audio streams, the original has {len(orig.audio)}")
    if orig is not None and orig.video and info.video:
        ov, nv = orig.video[0], info.video[0]
        if (ov.width, ov.height) != (nv.width, nv.height):
            reasons.append(f"{nv.width}x{nv.height}, the original is {ov.width}x{ov.height}")
        if mode in ("remux", "audio") and ov.codec_name != nv.codec_name:
            reasons.append(f"video codec {nv.codec_name}, the original's is {ov.codec_name}")
    if mode == "remux" and orig is not None:
        # a remux copies every audio stream: each of the original's codecs is still there
        missing = Counter(str(a.codec_name) for a in orig.audio) - Counter(
            str(a.codec_name) for a in info.audio
        )
        if missing:
            reasons.append(f"audio codecs missing after the remux: {sorted(missing)}")

    # duration
    if orig is not None and orig.duration is not None:
        if info.duration is None:
            reasons.append("the new file has no duration")
        else:
            tol = duration_tolerance(orig.duration)
            measured["duration_delta"] = round(info.duration - orig.duration, 3)
            measured["duration_tolerance"] = round(tol, 3)
            if abs(info.duration - orig.duration) > tol:
                reasons.append(
                    f"duration {info.duration:.2f} s, the original {orig.duration:.2f} s"
                    f" (tolerance {tol:.2f} s)"
                )

    # faststart
    if require_faststart is None:
        require_faststart = new.lower().endswith(MP4_EXTS)
    if require_faststart:
        fs = moov_before_mdat(new)
        measured["faststart"] = fs
        if fs is not True:
            reasons.append("the moov atom is not before mdat (no faststart)")

    # frame count
    if count and info.video:
        frames = await count_frames(new)
        measured["frames"] = frames
        if original_frames is None and isinstance(original, str) and orig and orig.video:
            original_frames = await count_frames(original)
        if original_frames is not None:
            measured["original_frames"] = original_frames
            tol_f = 1 if mode in ("remux", "audio") else max(1, int(original_frames * 0.005))
            measured["frames_tolerance"] = tol_f
            if frames is None:
                reasons.append("the new file's frames cannot be counted")
            elif abs(frames - original_frames) > tol_f:
                reasons.append(f"{frames} frames, the original has {original_frames}")

    # full decode (last: the expensive one)
    if full_decode:
        if decode_timeout is None:
            dur = info.duration or 0.0
            decode_timeout = max(300.0, dur * 2)
        err = await decode_errors(
            new, has_video=bool(info.video), timeout=decode_timeout, nice=nice
        )
        measured["decode_errors"] = err
        if err is not None:
            reasons.append(f"decode errors: {err.splitlines()[0][:200]}")

    return VerifyResult(not reasons, reasons, measured)
