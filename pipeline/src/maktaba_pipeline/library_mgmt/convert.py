"""Convert a video's file to MP4 (Epic 47, story 47.4): the plan and the ffmpeg work.

The ``convert_mp4`` stage (``engine/stages/convert_mp4.py``) decides with :func:`plan_for`
what a file needs, runs :func:`run_ffmpeg` into a local work folder, writes the text
subtitles beside the MP4 (:func:`extract_srt`), and after the swap moves the thumbnail
cache folder to the new content hash (:func:`rekey_thumbnails`).

**The plan** (spec "A. Convert to MP4"):

- ``skip`` / ``already_mp4``: an ``.mp4``/``.m4v`` with H.264 or HEVC video, AAC, AC-3,
  E-AC-3 or MP3 audio and the moov atom first;
- ``remux`` (``-c copy``): MP4-ready video and audio in another container, or an MP4
  whose moov atom is at the end;
- ``audio`` (``-c:v copy -c:a aac -b:a 192k``): MP4-ready video, other audio (Opus,
  Vorbis, FLAC ...);
- ``reencode`` (lossy, always H.264): any other video, only when the library's
  ``settings.convert.allow_reencode`` is on; otherwise ``skip`` / ``needs_reencode``.
  The VideoToolbox preset ported from FileUtilities (``h264_videotoolbox -b:v 8M``),
  ``libx264 -crf 20 -preset medium`` when VideoToolbox is not there or fails;
- ``skip`` / ``image_subtitles``: PGS or VobSub subtitles cannot go into an MP4;
- ``skip`` / ``no_video``: no video stream (a cover picture is not one).

Text subtitles become ``mov_text`` inside the MP4 and ``<name>.<lang>.srt`` beside it.
Chapters and global metadata are kept (``-map_metadata 0 -map_chapters 0``); HEVC is
tagged ``hvc1`` so Apple's players open it.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
import os
import re
import shutil
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Literal

from ..engine import media
from ..engine.sql import loads, uid
from .verify import Mode, ProbeInfo, StreamInfo, moov_before_mdat

__all__ = [
    "IMAGE_SUBS",
    "Action",
    "ConvertSettings",
    "FfmpegFailed",
    "Plan",
    "Probe",
    "SubStream",
    "WORK_ROOT",
    "convert_settings",
    "extract_srt",
    "ffmpeg_args",
    "final_path_for",
    "h264_encoders",
    "input_damaged",
    "plan_for",
    "probe_json",
    "rekey_thumbnails",
    "run_ffmpeg",
    "sidecar_name",
    "work_dir_for",
]

Action = Literal["skip", "remux", "audio", "reencode"]

MP4_EXTS = (".mp4", ".m4v")
MP4_VIDEO = frozenset({"h264", "hevc"})
#: audio an MP4 keeps as it is
MP4_AUDIO = frozenset({"aac", "ac3", "eac3", "mp3"})
TEXT_SUBS = frozenset({"subrip", "srt", "ass", "ssa", "webvtt", "mov_text", "text"})
IMAGE_SUBS = frozenset({"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub"})

#: the FileUtilities VideoToolbox H.264 preset (AppSettings.cs), then the fallback
VIDEOTOOLBOX = ["-c:v", "h264_videotoolbox", "-b:v", "8M", "-pix_fmt", "yuv420p"]
LIBX264 = ["-c:v", "libx264", "-crf", "20", "-preset", "medium", "-pix_fmt", "yuv420p"]
AAC = ["-c:a", "aac", "-b:a", "192k"]

#: the local work folder (``<root>/<job id>/``); ``MAKTABA_CONVERT_WORK_DIR`` or
#: ``~/.maktaba/work/convert``. ffmpeg's mux step fails writing to the NAS (repair.py)
WORK_ROOT: str = os.environ.get("MAKTABA_CONVERT_WORK_DIR") or str(
    Path.home() / ".maktaba" / "work" / "convert"
)

_DAMAGED = re.compile(
    r"invalid data found|moov atom not found|error while decoding|ebml header parsing failed"
    r"|could not find codec parameters|end of file|truncat",
    re.IGNORECASE,
)


def work_dir_for(job_id: int, root: str | None = None) -> Path:
    return Path(root or WORK_ROOT) / str(job_id)


def input_damaged(stderr: str) -> bool:
    """ffmpeg's output says the input cannot be read (an unfinished download, a corrupt
    file): no retry helps."""
    return bool(_DAMAGED.search(stderr))


# ---------------------------------------------------------------- settings


@dataclass(frozen=True, slots=True)
class ConvertSettings:
    allow_reencode: bool = False
    #: queued by a rule on ``scan`` (not wired yet: the Queues page enqueues)
    auto: bool = False


async def convert_settings(db: Any, library_id: Any) -> ConvertSettings:
    """``libraries.settings.convert`` (both off by default)."""
    raw = await db.fetchval("SELECT settings FROM libraries WHERE id = $1", uid(library_id))
    doc = loads(raw) if raw is not None else None
    conv = doc.get("convert") if isinstance(doc, dict) else None
    if not isinstance(conv, dict):
        return ConvertSettings()
    return ConvertSettings(
        allow_reencode=conv.get("allow_reencode") is True, auto=conv.get("auto") is True
    )


# ---------------------------------------------------------------- probe


@dataclass(frozen=True, slots=True)
class SubStream:
    #: the ordinal among the file's subtitle streams (``-map 0:s:<n>``)
    ordinal: int
    codec: str
    language: str


@dataclass(frozen=True, slots=True)
class Probe:
    """What the plan needs from ffprobe's JSON (``-show_streams -show_format``)."""

    doc: Mapping[str, Any]

    @property
    def streams(self) -> list[Mapping[str, Any]]:
        s = self.doc.get("streams")
        return [x for x in s if isinstance(x, Mapping)] if isinstance(s, list) else []

    def _of(self, kind: str) -> list[Mapping[str, Any]]:
        return [s for s in self.streams if s.get("codec_type") == kind]

    @property
    def video(self) -> list[Mapping[str, Any]]:
        """Video streams that are not cover pictures."""
        return [
            s
            for s in self._of("video")
            if not (s.get("disposition") or {}).get("attached_pic")
            and s.get("codec_name") not in ("mjpeg", "png", "bmp")
        ]

    @property
    def audio_codecs(self) -> list[str]:
        return [str(s.get("codec_name") or "") for s in self._of("audio")]

    @property
    def subs(self) -> list[SubStream]:
        out = []
        for n, s in enumerate(self._of("subtitle")):
            tags = s.get("tags") or {}
            lang = str(tags.get("language") or tags.get("LANGUAGE") or "und").strip() or "und"
            out.append(SubStream(n, str(s.get("codec_name") or ""), re.sub(r"\W", "", lang)))
        return out

    @property
    def format_name(self) -> str:
        fmt = self.doc.get("format") or {}
        return str(fmt.get("format_name") or "")

    @property
    def duration(self) -> float | None:
        fmt = self.doc.get("format") or {}
        try:
            return float(fmt["duration"])
        except (KeyError, TypeError, ValueError):
            return None

    def info(self, path: str) -> ProbeInfo:
        """The same file as :mod:`.verify` sees it."""

        def _i(v: Any) -> int | None:
            try:
                return None if v is None else int(v)
            except (TypeError, ValueError):
                return None

        fmt = self.doc.get("format") or {}
        return ProbeInfo(
            path=path,
            format_name=fmt.get("format_name"),
            duration=self.duration,
            size=_i(fmt.get("size")),
            streams=tuple(
                StreamInfo(
                    index=int(s.get("index", i)),
                    codec_type=str(s.get("codec_type") or ""),
                    codec_name=s.get("codec_name"),
                    width=_i(s.get("width")),
                    height=_i(s.get("height")),
                    nb_frames=_i(s.get("nb_frames")),
                )
                for i, s in enumerate(self.streams)
            ),
        )


async def probe_json(path: str, *, timeout: float = 120.0) -> tuple[Probe | None, str]:
    """ffprobe's JSON for ``path``, or None and what ffprobe said."""
    res = await media.run_media(
        [media.FFPROBE, "-v", "error", "-show_streams", "-show_format", "-of", "json", path],
        timeout=timeout,
    )
    if res.returncode != 0:
        return None, res.text_err.strip() or f"ffprobe exited {res.returncode}"
    try:
        doc = json.loads(res.stdout or b"{}")
    except ValueError:
        return None, "ffprobe printed no JSON"
    if not isinstance(doc, dict) or not (doc.get("streams") or doc.get("format")):
        return None, "ffprobe found no streams"
    return Probe(doc), ""


async def stored_probe(db: Any, video_id: Any) -> Probe | None:
    """``media_info.raw_ffprobe`` when the probe stage stored one with streams."""
    raw = await db.fetchval("SELECT raw_ffprobe FROM media_info WHERE video_id = $1", uid(video_id))
    doc = loads(raw) if raw is not None else None
    if isinstance(doc, dict) and isinstance(doc.get("streams"), list) and doc["streams"]:
        return Probe(doc)
    return None


# ---------------------------------------------------------------- plan


@dataclass(frozen=True, slots=True)
class Plan:
    action: Action
    #: why it is skipped: already_mp4, needs_reencode, image_subtitles, no_video
    reason: str | None = None
    #: the text subtitles: mov_text inside, .srt beside
    subs: tuple[SubStream, ...] = ()
    #: the audio is copied (remux) or made AAC
    copy_audio: bool = True
    hevc: bool = False

    @property
    def mode(self) -> Mode:
        if self.action == "audio":
            return "audio"
        if self.action == "reencode":
            return "reencode"
        return "remux"

    def as_json(self) -> dict[str, Any]:
        return {
            "action": self.action,
            "reason": self.reason,
            "subtitles": [{"lang": s.language, "codec": s.codec} for s in self.subs],
        }


def final_path_for(path: str) -> str:
    """The MP4's path: the same for an ``.mp4``/``.m4v``, else ``<stem>.mp4``. Works on
    the database's form (``/`` or ``\\``)."""
    stem, ext = os.path.splitext(path)
    if ext.lower() in MP4_EXTS:
        return path
    return stem + ".mp4"


def plan_for(
    probe: Probe,
    path: str,
    *,
    allow_reencode: bool,
    faststart: bool | None,
    force_reencode: bool = False,
) -> Plan:
    """What ``path`` (probed as ``probe``) needs. ``faststart``: whether its moov atom
    comes first (None: not ISO-BMFF). ``force_reencode``: the retry after a failed
    verification, honoured only where re-encoding is allowed."""
    video = probe.video
    if not video:
        return Plan("skip", "no_video")
    subs = probe.subs
    if any(s.codec not in TEXT_SUBS for s in subs):
        return Plan("skip", "image_subtitles", subs=tuple(subs))
    vcodec = str(video[0].get("codec_name") or "")
    video_ok = all(str(v.get("codec_name") or "") in MP4_VIDEO for v in video)
    audio_ok = all(c in MP4_AUDIO for c in probe.audio_codecs)
    text = tuple(s for s in subs if s.codec != "mov_text") if _is_mp4(path, probe) else ()
    if not video_ok or (force_reencode and allow_reencode):
        if not allow_reencode:
            return Plan("skip", "needs_reencode", subs=tuple(subs))
        return Plan("reencode", subs=tuple(subs), copy_audio=audio_ok)
    hevc = vcodec == "hevc"
    if not audio_ok:
        return Plan("audio", subs=tuple(subs), copy_audio=False, hevc=hevc)
    if _is_mp4(path, probe) and faststart is True and not text:
        return Plan("skip", "already_mp4", subs=tuple(subs))
    return Plan("remux", subs=tuple(subs), hevc=hevc)


def _is_mp4(path: str, probe: Probe) -> bool:
    return os.path.splitext(path)[1].lower() in MP4_EXTS and (
        "mp4" in probe.format_name or "mov" in probe.format_name
    )


def faststart_of(local: str) -> bool | None:
    """``moov`` before ``mdat`` (reads the box headers only)."""
    return moov_before_mdat(local)


# ---------------------------------------------------------------- ffmpeg


_encoders: set[str] | None = None


async def h264_encoders() -> list[list[str]]:
    """The H.264 encoders to try, in order: VideoToolbox when this ffmpeg has it, then
    libx264."""
    global _encoders
    if _encoders is None:
        res = await media.run_media([media.FFMPEG, "-hide_banner", "-encoders"], timeout=30)
        text = res.stdout.decode("utf-8", errors="replace")
        _encoders = {
            name for name in ("h264_videotoolbox", "libx264") if re.search(rf"\s{name}\s", text)
        }
    out = []
    if "h264_videotoolbox" in _encoders:
        out.append(VIDEOTOOLBOX)
    out.append(LIBX264)
    return out


def ffmpeg_args(
    src: str, dest: str, plan: Plan, *, video_encoder: Sequence[str] = LIBX264
) -> list[str]:
    """The ffmpeg command line for ``plan`` (progress on stdout)."""
    args = [media.FFMPEG, "-nostdin", "-hide_banner", "-v", "error", "-y", "-i", src]
    args += ["-map", "0:V", "-map", "0:a?"]
    if plan.subs:
        args += ["-map", "0:s?"]
    args += ["-map_metadata", "0", "-map_chapters", "0"]
    if plan.action == "reencode":
        args += list(video_encoder)
    else:
        args += ["-c:v", "copy"]
        if plan.hevc:
            args += ["-tag:v", "hvc1"]
    args += ["-c:a", "copy"] if plan.copy_audio else AAC
    if plan.subs:
        args += ["-c:s", "mov_text"]
    args += ["-movflags", "+faststart", "-progress", "pipe:1", "-nostats", dest]
    return args


class FfmpegFailed(Exception):
    def __init__(self, returncode: int, stderr: str) -> None:
        super().__init__(f"ffmpeg exited {returncode}: {stderr[-500:]}")
        self.returncode = returncode
        self.stderr = stderr


ProgressFn = Callable[[float], Awaitable[None]]


async def run_ffmpeg(
    args: list[str],
    *,
    on_progress: ProgressFn,
    check_cancel: Callable[[], Awaitable[None]],
    cancel_every: float = 1.0,
) -> None:
    """Run ``args``; ``on_progress(seconds done)`` per progress block. ``check_cancel``
    is awaited about every ``cancel_every`` seconds: when it raises, ffmpeg is killed and
    the exception goes on. Raises :class:`FfmpegFailed` on a non-zero exit."""
    proc = await asyncio.create_subprocess_exec(
        *args,
        stdin=asyncio.subprocess.DEVNULL,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    assert proc.stdout is not None and proc.stderr is not None
    err = bytearray()

    async def drain_err() -> None:
        assert proc.stderr is not None
        while chunk := await proc.stderr.read(4096):
            err.extend(chunk)
            del err[:-16384]

    err_task = asyncio.create_task(drain_err())
    loop = asyncio.get_running_loop()
    last_check = loop.time()
    out_s = 0.0
    try:
        while True:
            try:
                line = await asyncio.wait_for(proc.stdout.readline(), cancel_every)
            except TimeoutError:
                line = None
            if line == b"":
                break
            if line:
                key, _, value = line.decode("utf-8", errors="replace").strip().partition("=")
                if key in ("out_time_us", "out_time_ms") and value.strip().isdigit():
                    out_s = int(value) / 1_000_000
                elif key == "progress":
                    await on_progress(out_s)
            if loop.time() - last_check >= cancel_every:
                last_check = loop.time()
                await check_cancel()
        rc = await proc.wait()
    except BaseException:
        with contextlib.suppress(ProcessLookupError):
            proc.kill()
        await proc.wait()
        err_task.cancel()
        raise
    await err_task
    if rc != 0:
        raise FfmpegFailed(rc, err.decode("utf-8", errors="replace"))


async def extract_srt(src: str, sub: SubStream, dest: str) -> bool:
    """Subtitle stream ``sub`` of ``src`` as SubRip at ``dest``."""
    res = await media.run_media(
        [
            media.FFMPEG, "-nostdin", "-hide_banner", "-v", "error", "-y", "-i", src,
            "-map", f"0:s:{sub.ordinal}", "-c:s", "srt", dest,
        ],
        timeout=600,
    )  # fmt: skip
    return res.returncode == 0 and os.path.isfile(dest) and os.path.getsize(dest) > 0


def sidecar_name(final_local: str, sub: SubStream, taken: Callable[[str], bool]) -> str | None:
    """``<stem>.<lang>.srt`` beside the MP4, ``<stem>.<lang>.<n>.srt`` when that is taken
    (a downloaded sidecar is never overwritten); None when both are."""
    stem = os.path.splitext(final_local)[0]
    for cand in (f"{stem}.{sub.language}.srt", f"{stem}.{sub.language}.{sub.ordinal}.srt"):
        if not taken(cand):
            return cand
    return None


# ---------------------------------------------------------------- thumbnails


@dataclass
class Rekey:
    ok: bool
    moved: bool = False
    poster_path: str | None = None
    sprite_path: str | None = None
    error: str | None = None
    notes: list[str] = field(default_factory=list)


def rekey_thumbnails(
    old_hash: str,
    new_hash: str,
    *,
    root: str | None,
    poster_path: str | None,
    sprite_path: str | None,
) -> Rekey:
    """Move ``thumbnails/<old_hash>/`` to ``thumbnails/<new_hash>/`` (the poster, sprite,
    small copy and chapter frames: the picture did not change) and return the poster and
    sprite paths under the new folder. Not ok when the new folder exists already or the
    rename fails: the caller asks for the thumbnails to be made again."""
    from ..thumbnail.generator import thumbnail_dir_for

    src = thumbnail_dir_for(old_hash, root=root)
    dst = thumbnail_dir_for(new_hash, root=root)

    def moved(p: str | None) -> str | None:
        if p is None:
            return None
        s = str(src)
        if p == s or p.startswith(s + os.sep) or p.startswith(s + "/"):
            return str(dst) + p[len(s) :]
        return p

    if old_hash == new_hash:
        return Rekey(True, poster_path=poster_path, sprite_path=sprite_path)
    if not src.is_dir():
        return Rekey(True, poster_path=poster_path, sprite_path=sprite_path)
    if dst.exists():
        return Rekey(False, error=f"{dst} exists already")
    try:
        os.rename(src, dst)
    except OSError as exc:
        return Rekey(False, error=str(exc))
    return Rekey(True, moved=True, poster_path=moved(poster_path), sprite_path=moved(sprite_path))


def free_bytes(path: str) -> int:
    os.makedirs(path, exist_ok=True)
    return shutil.disk_usage(path).free
