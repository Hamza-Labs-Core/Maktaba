"""A library of real generated clips for the Epic 47 dedup tests (stories 47.6, 47.7).

The clips are made with ``ffmpeg -f lavfi`` into tmp_path; the rows come from the real
scanner; the media facts the keeper rule reads (``media_info``, ``audio_tracks``) come
from a real ``ffprobe``; the frame hashes (``video_signals.frame_hashes``) from real
frames, the way the ``fingerprint`` stage reads them.
"""

from __future__ import annotations

import subprocess
from pathlib import Path
from typing import Any
from uuid import UUID

from maktaba_pipeline.engine.sql import dumps, uid
from maktaba_pipeline.library_mgmt.verify import probe
from maktaba_pipeline.log import get_logger
from maktaba_pipeline.scanner import Scanner, SqlScanStore
from maktaba_pipeline.thumbnail.dhash import dhashes_from_frames, ffmpeg_frame_args, frame_times
from tests.library_mgmt.epic47_world import World
from tests.library_mgmt.media_fixtures import FFMPEG, _ff, mkv_h264_aac

SECONDS = 6.0


def other_video(path: Path, seconds: float = SECONDS) -> Path:
    """A different picture and tone of the same length (not a duplicate)."""
    _ff(
        "-f",
        "lavfi",
        "-i",
        "mandelbrot=size=320x240:rate=25",
        "-f",
        "lavfi",
        "-i",
        "sine=frequency=220:sample_rate=48000",
        "-t",
        str(seconds),
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-pix_fmt",
        "yuv420p",
        "-c:a",
        "aac",
        str(path),
    )
    return path


def lecture(path: Path, seconds: float = SECONDS) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    return mkv_h264_aac(path, seconds)


async def scan(db: Any, w: World) -> Any:
    return await Scanner(SqlScanStore(db), log=get_logger()).run(w.library_id)


async def video_at(db: Any, path: Path) -> str:
    vid = await db.fetchval("SELECT id FROM videos WHERE path = $1", str(path))
    assert vid is not None, f"no video at {path}"
    return str(vid)


def _frames(path: Path, duration: float) -> str:
    assert FFMPEG is not None
    raw = b""
    for t in frame_times(duration):
        res = subprocess.run(
            ffmpeg_frame_args(str(path), t, binary=FFMPEG), capture_output=True, timeout=60
        )
        if res.returncode == 0 and len(res.stdout) >= 72:
            raw += res.stdout[:72]
    return ",".join(f"{h:016x}" for h in dhashes_from_frames(raw))


async def probe_into_db(db: Any, vid: str | UUID, path: Path, *, frames: bool = True) -> None:
    """What probe and fingerprint would have stored for this file."""
    info = await probe(str(path))
    assert info is not None
    v = info.video[0]
    await db.execute(
        "INSERT INTO media_info (video_id, container, video_codec, width, height,"
        " bitrate_kbps, has_subtitles, raw_ffprobe) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)",
        uid(vid),
        info.format_name,
        v.codec_name,
        v.width,
        v.height,
        int((info.size or 0) * 8 / 1000 / max(info.duration or 1, 1)),
        False,
        dumps({"streams": [{"codec_type": s.codec_type} for s in info.streams]}),
    )
    for i, a in enumerate(info.audio):
        await db.execute(
            "INSERT INTO audio_tracks (video_id, track_index, codec) VALUES ($1, $2, $3)",
            uid(vid),
            i,
            a.codec_name,
        )
    await db.execute(
        "UPDATE videos SET duration_sec = $2 WHERE id = $1", uid(vid), float(info.duration or 0)
    )
    if frames:
        await db.execute(
            "INSERT INTO video_signals (video_id, frame_hashes) VALUES ($1, $2)",
            uid(vid),
            _frames(path, float(info.duration or 0)),
        )
