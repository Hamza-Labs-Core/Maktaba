"""Story 2.3 — stream PCM out of ffmpeg into the transcriber.

Two surfaces:

- :func:`stream_pcm` — async iterator of 16 kHz mono s16le PCM chunks.
  The default path. ffmpeg's stdout is a pipe; we yield ``chunk_size``
  bytes at a time so the consumer sees backpressure.
- :func:`extract_to_file` — write a temp WAV under
  ``~/.maktaba/cache/audio/{hash}.wav`` for backends that declare
  ``requires_file = True``.

Both share :func:`build_ffmpeg_args` so the command line is documented in
one place. Tests assert the exact argv.

Pause handling: :class:`StreamHandle` exposes ``terminate()``; the
worker calls it when the cooperative pause check fires. ffmpeg gets
SIGTERM, then SIGKILL after ``terminate_grace_sec`` (5 s default). The
async iterator surfaces ``StopAsyncIteration`` on a clean termination
and raises :class:`ExtractError` on a non-zero exit (kind=
``ffmpeg_decode``) unless we initiated the stop.
"""

from __future__ import annotations

import asyncio
import json
import os
import shutil
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager, suppress
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any, Protocol
from uuid import UUID

if TYPE_CHECKING:
    from .probe import AudioTrack

__all__ = [
    "DEFAULT_CHUNK_BYTES",
    "ExtractError",
    "SelectedTrack",
    "StreamHandle",
    "build_ffmpeg_args",
    "commit_extract",
    "extract_to_file",
    "load_selected_track",
    "stream_pcm",
]

# 64 KiB matches Story 2.3's default. Keeps memory bounded and gives the
# transcriber regular pause-check opportunities.
DEFAULT_CHUNK_BYTES = 64 * 1024

DEFAULT_TERMINATE_GRACE_SEC = 5.0


class ExtractError(RuntimeError):
    """Structured extract failure. ``kind`` matches Story 2.3 AC-3."""

    def __init__(self, kind: str, *, returncode: int | None = None, stderr_tail: str = "") -> None:
        super().__init__(f"extract failed: {kind} rc={returncode} tail={stderr_tail!r}")
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


def build_ffmpeg_args(
    path: str,
    track_index: int,
    *,
    start_sec: float = 0.0,
    sample_rate: int = 16_000,
    channels: int = 1,
    sample_fmt: str = "s16",
    output: str = "pipe:1",
    binary: str = "ffmpeg",
) -> list[str]:
    """Compose the ffmpeg command line.

    The flags mirror Story 2.3 AC-1:

    - ``-hide_banner -nostdin -threads 1`` keep stdout clean and bound
      the worker's CPU footprint to one ffmpeg core (extract is
      I/O-bound; one core is enough).
    - ``-fflags +genpts`` forces monotonic PTS for concatenated TS
      streams (EC: TS PTS resets).
    - ``-ss`` is placed *before* ``-i`` for fast-but-imprecise input
      seek; for VBR/VFR audio the orchestrator subtracts 0.5 s ahead
      of the requested resume point and discards the lead-in until the
      first sample whose PTS ≥ the resume position. The discard loop
      lives in the worker, not here.
    """
    args: list[str] = [
        binary,
        "-hide_banner",
        "-nostdin",
        "-threads",
        "1",
        "-fflags",
        "+genpts",
    ]
    if start_sec > 0:
        args += ["-ss", f"{max(0.0, start_sec - 0.5):.3f}"]
    args += [
        "-i",
        path,
        "-map",
        f"0:a:{track_index}",
        "-ac",
        str(channels),
        "-ar",
        str(sample_rate),
        "-sample_fmt",
        sample_fmt,
        "-f",
        "s16le" if sample_fmt == "s16" else "wav",
        output,
    ]
    return args


@dataclass(slots=True)
class StreamHandle:
    """Cooperative-cancel handle for an in-flight :func:`stream_pcm`.

    ``terminated`` flips to True after :meth:`terminate` (or
    :meth:`stop`) was called so the iterator can distinguish
    operator-requested shutdown from a genuine ffmpeg crash.
    """

    process: asyncio.subprocess.Process
    terminated: bool = False
    terminate_grace_sec: float = DEFAULT_TERMINATE_GRACE_SEC

    async def terminate(self) -> int | None:
        """Send SIGTERM, wait up to grace, then SIGKILL.

        Returns the final exit code. Idempotent: subsequent calls
        return immediately because the process is already gone.
        """
        if self.process.returncode is not None:
            return self.process.returncode
        self.terminated = True
        try:
            self.process.terminate()
        except ProcessLookupError:
            return self.process.returncode
        try:
            return await asyncio.wait_for(self.process.wait(), timeout=self.terminate_grace_sec)
        except TimeoutError:
            with suppress(ProcessLookupError):
                self.process.kill()
            return await self.process.wait()

    # Sync alias for callers that hold the handle without an event loop
    # context (they ``asyncio.create_task`` the call themselves).
    stop = terminate


@asynccontextmanager
async def _spawn(args: list[str]) -> AsyncIterator[asyncio.subprocess.Process]:
    if shutil.which(args[0]) is None:
        raise ExtractError("ffmpeg_not_found", returncode=None, stderr_tail=args[0])
    proc = await asyncio.create_subprocess_exec(
        *args,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        stdin=asyncio.subprocess.DEVNULL,
    )
    try:
        yield proc
    finally:
        if proc.returncode is None:
            proc.kill()
            await proc.wait()


async def stream_pcm(
    path: str,
    track_index: int,
    *,
    start_sec: float = 0.0,
    chunk_bytes: int = DEFAULT_CHUNK_BYTES,
    binary: str = "ffmpeg",
    handle_sink: list[StreamHandle] | None = None,
) -> AsyncIterator[bytes]:
    """Yield raw PCM chunks from ffmpeg until EOF or terminate.

    Pass a single-slot ``handle_sink`` (e.g. ``[]``) to receive the
    :class:`StreamHandle` so the worker can call ``handle.terminate()``
    on a pause request:

        handles: list[StreamHandle] = []
        async for chunk in stream_pcm(path, idx, handle_sink=handles):
            ...
            if pause_requested():
                await handles[0].terminate()
                break
    """
    args = build_ffmpeg_args(path, track_index, start_sec=start_sec, binary=binary)
    async with _spawn(args) as proc:
        handle = StreamHandle(process=proc)
        if handle_sink is not None:
            handle_sink.clear()
            handle_sink.append(handle)

        assert proc.stdout is not None  # pipe always set above

        async for chunk in _iter_chunks(proc.stdout, chunk_bytes):
            yield chunk

        rc = await proc.wait()
        if rc != 0 and not handle.terminated:
            tail = b""
            if proc.stderr is not None:
                tail = await proc.stderr.read()
            raise ExtractError(
                "ffmpeg_decode",
                returncode=rc,
                stderr_tail=tail.decode("utf-8", "replace")[-2000:],
            )


async def _iter_chunks(reader: asyncio.StreamReader, chunk_bytes: int) -> AsyncIterator[bytes]:
    while True:
        buf = await reader.read(chunk_bytes)
        if not buf:
            return
        yield buf


def cache_path_for(content_hash: str, *, root: str | os.PathLike[str] | None = None) -> Path:
    """Return ``~/.maktaba/cache/audio/{hash}.wav`` (or a custom root).

    The path is content-addressed so concurrent extracts of the same
    video share a result and the cleanup pass on terminal job state
    can find what to delete.
    """
    base = Path(root) if root else Path.home() / ".maktaba" / "cache" / "audio"
    return base / f"{content_hash}.wav"


async def extract_to_file(
    path: str,
    track_index: int,
    *,
    content_hash: str,
    cache_root: str | os.PathLike[str] | None = None,
    start_sec: float = 0.0,
    binary: str = "ffmpeg",
) -> Path:
    """Decode the chosen track to a 16 kHz mono s16le WAV.

    Returns the cache path. Idempotent — if the file already exists
    and is non-empty, ffmpeg is not invoked.
    """
    dest = cache_path_for(content_hash, root=cache_root)
    dest.parent.mkdir(parents=True, exist_ok=True)
    if dest.exists() and dest.stat().st_size > 0:
        return dest

    args = build_ffmpeg_args(
        path,
        track_index,
        start_sec=start_sec,
        sample_fmt="s16",
        output=str(dest),
        binary=binary,
    )
    # Output to a real path means we can drop the s16le format and let
    # ffmpeg infer WAV from the extension. Replace the format flag.
    fi = args.index("-f")
    args[fi : fi + 2] = ["-f", "wav"]

    async with _spawn(args) as proc:
        rc = await proc.wait()
        if rc != 0:
            tail = b""
            if proc.stderr is not None:
                tail = await proc.stderr.read()
            with suppress(FileNotFoundError):
                dest.unlink()
            raise ExtractError(
                "ffmpeg_decode",
                returncode=rc,
                stderr_tail=tail.decode("utf-8", "replace")[-2000:],
            )
    return dest


# --- DB read-back + commit (EXTRACT-stage glue) -------------------------
#
# The EXTRACT stage consumes what PROBE persisted: the ``audio_tracks``
# rows ``commit_probe`` wrote. ``load_selected_track`` reconstructs the
# pure :class:`~maktaba_pipeline.audio.probe.AudioTrack` view those rows
# represent and runs the existing track-selection policy
# (Story 2.2) to pick exactly one. ``commit_extract`` is the EXTRACT
# analogue of ``commit_probe``: it persists the artifact reference,
# stamps the track, advances the FSM, and enqueues TRANSCRIBE — the
# same shape, the same helpers (``advance_after_stage`` / ``enqueue``).


@dataclass(slots=True, frozen=True)
class SelectedTrack:
    """The track EXTRACT chose plus its ``audio_tracks`` primary key.

    ``db_id`` is the BIGSERIAL ``audio_tracks.id`` — the ``audio_cache``
    /``transcripts`` foreign key. ``track`` is the pure
    :class:`AudioTrack` (audio-rank ``index`` for ffmpeg's
    ``-map 0:a:N``) the selection policy reasoned over.
    """

    db_id: int
    track: AudioTrack


class _ExtractDB(Protocol):
    """The connection shape :func:`commit_extract` needs.

    A strict superset of ``commit_probe``'s ``_ProbeDB``; the runtime
    ``Database`` facade satisfies it. Tests pass the canonical fake.
    """

    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_SELECT_AUDIO_TRACKS = """
SELECT id, track_index, codec, channels, sample_rate, language, title,
       is_default, disposition
  FROM audio_tracks
 WHERE video_id = $1
 ORDER BY track_index
"""

_UPSERT_AUDIO_CACHE = """
INSERT INTO audio_cache (content_hash, video_id, audio_track_id, path, bytes)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (content_hash) DO UPDATE SET
    video_id       = EXCLUDED.video_id,
    audio_track_id = EXCLUDED.audio_track_id,
    path           = EXCLUDED.path,
    bytes          = EXCLUDED.bytes
"""

_STAMP_TRACK_EXTRACTED = """
UPDATE audio_tracks SET last_extracted_at = now() WHERE id = $1
"""


async def load_selected_track(
    db: _ExtractDB,
    *,
    video_id: UUID,
    settings: Any | None = None,
) -> SelectedTrack | None:
    """Read the PROBE-persisted ``audio_tracks`` rows and pick one.

    Reconstructs the pure :class:`AudioTrack` list from the DB rows
    ``commit_probe`` wrote and delegates the choice to the existing
    Story 2.2 :func:`~maktaba_pipeline.audio.track_selection.select_tracks`
    policy (preferred language → Arabic → container default → first
    audio stream). Returns ``None`` when no non-commentary track
    remains — the caller treats that as an unrecoverable inconsistency
    (PROBE only enqueues EXTRACT when audio was present).
    """
    # Local imports keep this module importable without the probe /
    # track-selection import chain at module load (mirrors the lazy
    # imports the probe adapter uses to dodge the package cycle).
    from ..log import get_logger  # noqa: PLC0415
    from .probe import AudioTrack as _AudioTrack  # noqa: PLC0415
    from .track_selection import select_tracks  # noqa: PLC0415

    log = get_logger()

    rows = await db.fetch(_SELECT_AUDIO_TRACKS, video_id)
    if not rows:
        return None

    by_index: dict[int, int] = {}
    tracks: list[AudioTrack] = []
    for r in rows:
        disp_raw = r["disposition"]
        if isinstance(disp_raw, str):
            try:
                disposition = json.loads(disp_raw) if disp_raw else {}
            except json.JSONDecodeError:
                disposition = {}
        else:
            disposition = dict(disp_raw or {})
        idx = int(r["track_index"])
        by_index[idx] = int(r["id"])
        tracks.append(
            _AudioTrack(
                index=idx,
                codec=r["codec"],
                channels=r["channels"],
                sample_rate=r["sample_rate"],
                language=str(r["language"]),
                title=r["title"],
                is_default=bool(r["is_default"]),
                disposition=disposition,
            )
        )

    selected = select_tracks(tracks, settings)
    if not selected:
        return None
    if len(selected) > 1:
        # Wave 0 is single-track only. The Story 2.2 policy can return
        # several tracks (multi_audio settings); EXTRACT keeps the first
        # and drops the rest. Surface the dropped tracks so this
        # intentional narrowing is visible in the logs rather than a
        # silent cliff.
        log.warning(
            "extract_multi_track_truncated",
            video_id=str(video_id),
            selected_count=len(selected),
            kept_track_index=selected[0].index,
            dropped_track_indices=[t.index for t in selected[1:]],
        )
    # TODO(multi-audio): single-track only for Wave 0; libraries.settings
    # (preferred_audio_language/multi_audio/include_commentary) not yet
    # plumbed — see Story 2.2/2.3.
    chosen = selected[0]
    return SelectedTrack(db_id=by_index[chosen.index], track=chosen)


async def commit_extract(
    db: _ExtractDB,
    *,
    video_id: UUID,
    audio_track_id: int,
    content_hash: str,
    cache_path: str,
    bytes_written: int | None,
) -> str:
    """Persist the extracted-audio artifact and advance the video state.

    Returns the new ``videos.state``. The EXTRACT analogue of
    :func:`maktaba_pipeline.audio.probe.commit_probe`:

    1. UPSERT the ``audio_cache`` row (content-addressed PK → a re-run
       overwrites in place rather than duplicating),
    2. stamp ``audio_tracks.last_extracted_at``,
    3. advance the FSM ``PROBED -> AUDIO_EXTRACTED`` via
       :func:`advance_after_stage` (its terminal-drop guard makes a
       replay a no-op),
    4. enqueue the follow-on TRANSCRIBE job via :func:`enqueue` — the
       *exact* mechanism ``commit_probe`` uses for the EXTRACT enqueue.

    Idempotent on replay: the artifact UPSERT, the FSM
    ``late_stage_finish`` guard, and the ``enqueue`` unique-live index
    all tolerate a repeat.
    """
    from ..db.jobs import Stage as _JobStage  # noqa: PLC0415
    from ..db.jobs import enqueue  # noqa: PLC0415
    from ..domain.states import Outcome, State, Trigger  # noqa: PLC0415
    from ..log import get_logger  # noqa: PLC0415
    from ..orchestrator.advance import advance_after_stage  # noqa: PLC0415

    log = get_logger()

    async with db.transaction():
        await db.execute(
            _UPSERT_AUDIO_CACHE,
            content_hash,
            video_id,
            audio_track_id,
            cache_path,
            bytes_written,
        )
        await db.execute(_STAMP_TRACK_EXTRACTED, audio_track_id)

    state_row = await db.fetchrow("SELECT state FROM videos WHERE id = $1", video_id)
    if state_row is None:
        raise LookupError(f"video {video_id} not found")
    current_state = State(state_row["state"])

    if current_state == State.PROBED:
        new_state = await advance_after_stage(db, video_id, Trigger.EXTRACT, Outcome.OK, log=log)
    else:
        # Replay / out-of-order: leave the row where it is. The FSM
        # would otherwise raise IllegalStateTransition for
        # AUDIO_EXTRACTED --EXTRACT--> (no such edge).
        new_state = current_state

    await enqueue(
        _as_job_db(db),
        video_id=video_id,
        stage=_JobStage.TRANSCRIBE,
        priority=100,
        payload={"audio_track_id": audio_track_id, "content_hash": content_hash},
    )
    log.info(
        "extract_committed",
        video_id=str(video_id),
        audio_track_id=audio_track_id,
        new_state=str(new_state),
    )
    return str(new_state)


def _as_job_db(db: _ExtractDB) -> Any:
    # The job-queue helpers expect their own Protocol; the extract DB
    # shape is a strict superset, so the cast is type-safe at runtime
    # (mirrors ``probe._as_job_db``).
    return db
