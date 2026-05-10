"""Story 2.1 — ffprobe binding and probe-stage DB writes.

The probe stage runs ``ffprobe -of json -show_streams -show_format`` and
populates two tables:

- ``media_info`` — one row per video: container, video codec,
  resolution, fps, bitrate, ``has_subtitles``.
- ``audio_tracks`` — one row per audio stream: track index, codec,
  channels, sample rate, language (ISO 639-3, ``und`` when absent),
  title, ``is_default``, raw ``disposition``.

After the writes, :func:`commit_probe` advances the video state via
``advance_after_stage``:

- Audio tracks present → ``DISCOVERED → PROBED`` and an ``extract`` job is enqueued.
- No audio → ``DISCOVERED → PROBED → READY_NO_AUDIO``; no ``extract`` job.

The ffprobe invocation is in :func:`run_ffprobe`. Tests substitute the
JSON via :func:`parse_ffprobe_json` directly so unit tests don't shell
out.
"""

from __future__ import annotations

import asyncio
import json
import shutil
from dataclasses import dataclass, field
from typing import Any, Protocol
from uuid import UUID

from ..db.jobs import DBConn as JobDBConn
from ..db.jobs import Stage as JobStage
from ..db.jobs import enqueue
from ..domain.states import Outcome, State, Trigger
from ..log import get_logger
from ..orchestrator.advance import advance_after_stage

__all__ = [
    "AudioTrack",
    "FFprobeNotFound",
    "MediaInfo",
    "ProbeError",
    "ProbeResult",
    "commit_probe",
    "parse_ffprobe_json",
    "probe",
    "run_ffprobe",
]

_log = get_logger()


# Conservative analyzeduration / probesize bumps for fragmented MPEG-TS.
# Story 2.1 EC: applies unconditionally so duration is reported correctly
# for CDN-style fragmented streams.
_FFPROBE_ANALYZE_DURATION = "100M"
_FFPROBE_PROBESIZE = "50M"


class FFprobeNotFound(RuntimeError):
    """Raised by :func:`run_ffprobe` when the binary isn't on PATH."""


class ProbeError(RuntimeError):
    """ffprobe ran but returned a non-zero exit or unparseable JSON."""


@dataclass(slots=True, frozen=True)
class MediaInfo:
    """One row of ``media_info`` plus the raw ffprobe JSON."""

    container: str | None
    video_codec: str | None
    width: int | None
    height: int | None
    fps: float | None
    bitrate_kbps: int | None
    has_subtitles: bool
    raw_ffprobe: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class AudioTrack:
    """One row of ``audio_tracks``.

    ``index`` is the ffmpeg ``-map 0:a:N`` index — i.e. the **audio**
    stream index, not the global stream index. Track-selection,
    extraction, and the ffmpeg map flag all use the audio-only number.
    """

    index: int
    codec: str | None
    channels: int | None
    sample_rate: int | None
    language: str
    title: str | None
    is_default: bool
    disposition: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class ProbeResult:
    media: MediaInfo
    audio: list[AudioTrack]


def parse_ffprobe_json(payload: dict[str, Any]) -> ProbeResult:
    """Decode ffprobe's ``-of json`` output into a :class:`ProbeResult`.

    The function is the seam between subprocess invocation and DB
    writes; tests inject curated JSON directly.
    """
    fmt = payload.get("format") or {}
    streams = payload.get("streams") or []

    video_stream = next(
        (s for s in streams if s.get("codec_type") == "video"),
        None,
    )

    width = _safe_int(video_stream.get("width")) if video_stream else None
    height = _safe_int(video_stream.get("height")) if video_stream else None
    video_codec = video_stream.get("codec_name") if video_stream else None
    fps = _parse_fps(video_stream) if video_stream else None
    bitrate_kbps = _bitrate_kbps(fmt, streams)
    has_subtitles = any(s.get("codec_type") == "subtitle" for s in streams)

    media = MediaInfo(
        container=fmt.get("format_name"),
        video_codec=video_codec,
        width=width,
        height=height,
        fps=fps,
        bitrate_kbps=bitrate_kbps,
        has_subtitles=has_subtitles,
        raw_ffprobe=payload,
    )

    # Audio-stream index runs over the *audio* streams only — ffmpeg's
    # ``-map 0:a:N`` selects by audio rank, not global rank. Keep the
    # source-file order so re-probes are stable.
    audio: list[AudioTrack] = []
    audio_rank = 0
    for s in streams:
        if s.get("codec_type") != "audio":
            continue
        tags = s.get("tags") or {}
        disposition = s.get("disposition") or {}
        lang = tags.get("language")
        if not lang:
            lang = "und"
        audio.append(
            AudioTrack(
                index=audio_rank,
                codec=s.get("codec_name"),
                channels=_safe_int(s.get("channels")),
                sample_rate=_safe_int(s.get("sample_rate")),
                language=lang,
                title=tags.get("title"),
                is_default=int(disposition.get("default") or 0) == 1,
                disposition=dict(disposition),
            )
        )
        audio_rank += 1

    return ProbeResult(media=media, audio=audio)


async def run_ffprobe(
    path: str,
    *,
    binary: str = "ffprobe",
    timeout_sec: float = 60.0,
) -> dict[str, Any]:
    """Spawn ffprobe and return parsed JSON. Raises :class:`ProbeError`."""
    if shutil.which(binary) is None:
        raise FFprobeNotFound(f"{binary!r} not on PATH")

    args = [
        binary,
        "-hide_banner",
        "-loglevel",
        "error",
        "-analyzeduration",
        _FFPROBE_ANALYZE_DURATION,
        "-probesize",
        _FFPROBE_PROBESIZE,
        "-of",
        "json",
        "-show_format",
        "-show_streams",
        path,
    ]

    proc = await asyncio.create_subprocess_exec(
        *args,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    try:
        stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=timeout_sec)
    except TimeoutError as exc:
        proc.kill()
        await proc.wait()
        raise ProbeError(f"ffprobe timed out after {timeout_sec}s") from exc

    if proc.returncode != 0:
        tail = (stderr or b"").decode("utf-8", "replace")[-2000:]
        raise ProbeError(f"ffprobe exit={proc.returncode}: {tail}")

    try:
        parsed: dict[str, Any] = json.loads(stdout.decode("utf-8", "replace"))
    except json.JSONDecodeError as exc:
        raise ProbeError(f"ffprobe output is not JSON: {exc}") from exc
    return parsed


async def probe(path: str, *, binary: str = "ffprobe") -> ProbeResult:
    """Convenience wrapper: shell out + parse."""
    payload = await run_ffprobe(path, binary=binary)
    return parse_ffprobe_json(payload)


# --- DB writes ----------------------------------------------------------


class _ProbeDB(Protocol):
    """The connection shape :func:`commit_probe` needs.

    The pipeline's connection wrapper (Story 1.5) implements this; tests
    pass a fake. Combines the ``advance_after_stage`` and ``enqueue``
    contracts.
    """

    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_UPSERT_MEDIA_INFO = """
INSERT INTO media_info
       (video_id, container, video_codec, width, height, fps, bitrate_kbps,
        has_subtitles, raw_ffprobe)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (video_id) DO UPDATE SET
    container       = EXCLUDED.container,
    video_codec     = EXCLUDED.video_codec,
    width           = EXCLUDED.width,
    height          = EXCLUDED.height,
    fps             = EXCLUDED.fps,
    bitrate_kbps    = EXCLUDED.bitrate_kbps,
    has_subtitles   = EXCLUDED.has_subtitles,
    raw_ffprobe     = EXCLUDED.raw_ffprobe,
    probed_at       = now()
"""

_UPSERT_AUDIO_TRACK = """
INSERT INTO audio_tracks
       (video_id, track_index, codec, channels, sample_rate, language, title,
        is_default, disposition)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (video_id, track_index) DO NOTHING
"""

_SELECT_STATE_SQL = "SELECT state FROM videos WHERE id = $1"


async def commit_probe(
    db: _ProbeDB,
    *,
    video_id: UUID,
    result: ProbeResult,
) -> str:
    """Persist a :class:`ProbeResult` and advance the video state.

    Returns the new ``videos.state``. Idempotent: running twice writes the
    same media_info via UPSERT and skips audio_tracks via the unique
    constraint; the FSM advance uses ``advance_after_stage`` whose
    terminal-drop guard handles double calls.

    The ``extract`` job is enqueued only when at least one audio track
    was found. AC: a video with zero audio tracks transitions to
    ``READY_NO_AUDIO`` and skips the extract enqueue.
    """
    raw_json = json.dumps(result.media.raw_ffprobe)

    async with db.transaction():
        await db.execute(
            _UPSERT_MEDIA_INFO,
            video_id,
            result.media.container,
            result.media.video_codec,
            result.media.width,
            result.media.height,
            result.media.fps,
            result.media.bitrate_kbps,
            result.media.has_subtitles,
            raw_json,
        )
        for track in result.audio:
            await db.execute(
                _UPSERT_AUDIO_TRACK,
                video_id,
                track.index,
                track.codec,
                track.channels,
                track.sample_rate,
                track.language,
                track.title,
                track.is_default,
                json.dumps(track.disposition),
            )

    # Idempotency: a re-probe on an already-advanced row is a no-op
    # for the FSM. The UPSERT/DO-NOTHING above already rejected the
    # duplicate writes; we just need to skip the advance + enqueue
    # rather than raise IllegalStateTransition.
    state_row = await db.fetchrow(_SELECT_STATE_SQL, video_id)
    current_state = State(state_row["state"]) if state_row is not None else None

    new_state: State | None
    if not result.audio:
        if current_state is None or current_state == State.DISCOVERED:
            await advance_after_stage(db, video_id, Trigger.PROBE, Outcome.OK, log=_log)
        if current_state in (None, State.DISCOVERED, State.PROBED):
            new_state = await advance_after_stage(
                db, video_id, Trigger.PROBE, Outcome.NO_AUDIO, log=_log
            )
        else:
            new_state = current_state
        _log.info(
            "probe_no_audio",
            video_id=str(video_id),
            new_state=str(new_state),
        )
        return str(new_state)

    if current_state == State.DISCOVERED or current_state is None:
        new_state = await advance_after_stage(db, video_id, Trigger.PROBE, Outcome.OK, log=_log)
    else:
        new_state = current_state
    # The job-queue helpers expect a connection that satisfies their own
    # Protocol; the probe DB shape is a strict superset, so the cast is
    # type-safe at runtime.
    await enqueue(
        _as_job_db(db),
        video_id=video_id,
        stage=JobStage.EXTRACT,
        priority=100,
        payload={"audio_track_count": len(result.audio)},
    )
    _log.info(
        "probe_committed",
        video_id=str(video_id),
        audio_tracks=len(result.audio),
        new_state=str(new_state),
    )
    return str(new_state)


def _as_job_db(db: _ProbeDB) -> JobDBConn:
    # The job-queue helpers expect a connection that satisfies their own
    # Protocol; the probe DB shape is a strict superset.
    return db


# --- helpers ------------------------------------------------------------


def _safe_int(value: Any) -> int | None:
    if value is None:
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _parse_fps(stream: dict[str, Any]) -> float | None:
    rate = stream.get("avg_frame_rate") or stream.get("r_frame_rate")
    if not rate or rate == "0/0":
        return None
    if "/" not in rate:
        try:
            return float(rate)
        except ValueError:
            return None
    num_s, _, den_s = rate.partition("/")
    try:
        num, den = float(num_s), float(den_s)
    except ValueError:
        return None
    return num / den if den else None


def _bitrate_kbps(fmt: dict[str, Any], streams: list[dict[str, Any]]) -> int | None:
    raw = fmt.get("bit_rate")
    if raw is None:
        for s in streams:
            if s.get("codec_type") == "video" and s.get("bit_rate"):
                raw = s.get("bit_rate")
                break
    n = _safe_int(raw)
    if n is None:
        return None
    return max(0, n // 1000)
