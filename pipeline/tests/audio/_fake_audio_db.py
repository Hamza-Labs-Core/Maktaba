"""In-memory fake DB for audio probe / extract tests.

Routes the SQL fragments used by :mod:`maktaba_pipeline.audio.probe` and
:mod:`maktaba_pipeline.stt.segment_commit` to in-memory dicts. The
fake's surface is ``transaction``, ``fetchrow``, ``execute`` —
matching the protocol shape both modules consume.
"""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any
from uuid import UUID, uuid4


@dataclass
class _LibraryRow:
    id: UUID
    name: str
    roots: list[str]
    settings: dict[str, Any] = field(default_factory=dict)


@dataclass
class _VideoRow:
    id: UUID
    library_id: UUID
    state: str = "discovered"
    content_hash: str | None = None
    path: str | None = None
    filename: str | None = None
    size_bytes: int | None = None
    mtime: Any = None
    last_seen_at: Any = None


@dataclass
class _MediaInfoRow:
    video_id: UUID
    container: str | None = None
    video_codec: str | None = None
    width: int | None = None
    height: int | None = None
    fps: float | None = None
    bitrate_kbps: int | None = None
    has_subtitles: bool = False
    raw_ffprobe: str = "{}"


@dataclass
class _AudioTrackRow:
    id: int
    video_id: UUID
    track_index: int
    codec: str | None
    channels: int | None
    sample_rate: int | None
    language: str
    title: str | None
    is_default: bool
    disposition: str
    last_extracted_at: datetime | None = None


@dataclass
class _AudioCacheRow:
    content_hash: str
    video_id: UUID
    audio_track_id: int
    path: str
    bytes: int | None


@dataclass
class _ProcessingJobRow:
    id: int
    video_id: UUID | None
    stage: str
    state: str = "pending"
    priority: int = 100
    library_id: UUID | None = None
    # Default 1 (not 0): a claimed/running row has had >=1 claim, and the
    # backoff helper rejects attempts < 1. This preserves the prior
    # behaviour when `_ProcessingJobRow` had no `attempts` field and the
    # StageDB read fell back to `getattr(job, "attempts", 1)`. The fake
    # claim path still increments on top of this.
    attempts: int = 1
    max_attempts: int = 3
    claimed_by: str | None = None
    claimed_at: datetime | None = None
    not_before: datetime | None = None
    total_duration_seconds: float | None = None
    paused_at: datetime | None = None
    paused_at_sec: float | None = None
    resumed_at: datetime | None = None
    resume_count: int = 0
    metrics: str | None = None
    created_at: datetime | None = None
    last_segment_end_sec: float = 0.0
    processed_seconds: float = 0.0
    segments_completed: int = 0
    realtime_factor: float | None = None
    estimated_remaining_sec: float | None = None
    last_heartbeat_at: datetime | None = None
    progress_updated_at: datetime | None = None
    pause_requested: bool = False
    cancel_requested: bool = False
    paused_reason: str | None = None
    payload: str | None = None
    error: str | None = None
    finished_at: datetime | None = None


@dataclass
class _TranscriptSegmentRow:
    id: int
    transcript_id: UUID
    seq: int
    start_sec: float
    end_sec: float
    text: str
    speaker: str | None
    confidence: float | None


@dataclass
class _TranscriptRow:
    id: UUID
    video_id: UUID
    audio_track_id: int
    language: str
    detected_language: str | None
    language_confidence: float | None
    backend: str
    model: str
    backend_version: str | None
    word_level: bool
    diarized: bool
    is_active: bool
    metadata: str
    superseded_at: datetime | None


class _Row(dict[str, Any]):
    pass


@dataclass
class FakeAudioDB:
    dialect: str = "postgres"
    libraries: dict[UUID, _LibraryRow] = field(default_factory=dict)
    videos: dict[UUID, _VideoRow] = field(default_factory=dict)
    media_info: dict[UUID, _MediaInfoRow] = field(default_factory=dict)
    audio_tracks: dict[int, _AudioTrackRow] = field(default_factory=dict)
    audio_cache: dict[str, _AudioCacheRow] = field(default_factory=dict)
    processing_jobs: dict[int, _ProcessingJobRow] = field(default_factory=dict)
    transcripts: dict[UUID, _TranscriptRow] = field(default_factory=dict)
    transcript_segments: dict[int, _TranscriptSegmentRow] = field(default_factory=dict)
    notifies: list[tuple[str, str]] = field(default_factory=list)
    _audio_next_id: int = 1
    _job_next_id: int = 1
    _seg_next_id: int = 1
    _lock_obj: asyncio.Lock | None = None

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    def add_video(
        self,
        *,
        state: str = "discovered",
        library_id: UUID | None = None,
    ) -> UUID:
        vid = uuid4()
        self.videos[vid] = _VideoRow(id=vid, library_id=library_id or uuid4(), state=state)
        return vid

    def add_library(
        self,
        *,
        roots: list[str],
        name: str = "test-lib",
        settings: dict[str, Any] | None = None,
    ) -> UUID:
        lib_id = uuid4()
        self.libraries[lib_id] = _LibraryRow(
            id=lib_id,
            name=name,
            roots=list(roots),
            settings=dict(settings or {}),
        )
        return lib_id

    @staticmethod
    def _now() -> datetime:
        return datetime.now(UTC)

    def _lock(self) -> asyncio.Lock:
        if self._lock_obj is None:
            self._lock_obj = asyncio.Lock()
        return self._lock_obj

    # Driver-shaped surface ----------------------------------------------

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None:
        s = " ".join(sql.split())
        async with self._lock():
            result = self._dispatch(s, args, many=False)
        if result is None or isinstance(result, _Row):
            return result
        return None

    async def fetch(self, sql: str, *args: Any) -> list[_Row]:
        s = " ".join(sql.split())
        async with self._lock():
            r = self._dispatch(s, args, many=True)
        if isinstance(r, list):
            return r
        return [r] if r is not None else []

    async def execute(self, sql: str, *args: Any) -> None:
        s = " ".join(sql.split())
        async with self._lock():
            self._dispatch(s, args, many=False)

    def _job_row_full(self, job: _ProcessingJobRow) -> dict[str, Any]:
        """Materialise a ``RETURNING *``-shaped row for ``_row_to_job``.

        Mirrors the slot 0002 + slot 0058 column set the claim path
        decodes; unset optional columns default to schema-shaped
        nulls/zeros so the frozen :class:`Job` dataclass constructs."""
        return {
            "id": job.id,
            "video_id": job.video_id,
            "library_id": job.library_id,
            "stage": job.stage,
            "state": job.state,
            "priority": job.priority,
            "attempts": job.attempts,
            "max_attempts": job.max_attempts,
            "claimed_by": job.claimed_by,
            "claimed_at": job.claimed_at,
            "last_heartbeat_at": job.last_heartbeat_at,
            "not_before": job.not_before,
            "error": job.error,
            "total_duration_seconds": job.total_duration_seconds,
            "processed_seconds": job.processed_seconds,
            "segments_completed": job.segments_completed,
            "last_segment_end_sec": job.last_segment_end_sec,
            "estimated_remaining_sec": job.estimated_remaining_sec,
            "realtime_factor": job.realtime_factor,
            "progress_updated_at": job.progress_updated_at,
            "pause_requested": job.pause_requested,
            "cancel_requested": job.cancel_requested,
            "paused_at": job.paused_at,
            "paused_at_sec": job.paused_at_sec,
            "paused_reason": job.paused_reason,
            "resumed_at": job.resumed_at,
            "resume_count": job.resume_count,
            "metrics": job.metrics,
            "payload": job.payload,
            "created_at": job.created_at or self._now(),
            "finished_at": job.finished_at,
        }

    def _dispatch(self, s: str, args: tuple[Any, ...], *, many: bool) -> Any:
        # --- SqlScanStore: library projection ---------------------------
        if s.startswith("SELECT id, name, roots, settings FROM libraries"):
            lib = self.libraries.get(args[0])
            if lib is None:
                return None
            return _Row(
                {
                    "id": lib.id,
                    "name": lib.name,
                    "roots": list(lib.roots),
                    "settings": dict(lib.settings),
                }
            )

        # --- SqlScanStore: skip-rehash path lookup ----------------------
        if s.startswith("SELECT id, size_bytes, mtime, content_hash FROM videos"):
            library_id, path = args
            for vid_row in self.videos.values():
                if vid_row.library_id == library_id and vid_row.path == path:
                    return _Row(
                        {
                            "id": vid_row.id,
                            "size_bytes": vid_row.size_bytes,
                            "mtime": vid_row.mtime,
                            "content_hash": vid_row.content_hash,
                        }
                    )
            return None

        # --- SqlScanStore: SQLite pre-check (insert vs update) ----------
        if s.startswith("SELECT id FROM videos WHERE library_id"):
            library_id, content_hash = args
            for vid_row in self.videos.values():
                if (
                    vid_row.library_id == library_id
                    and vid_row.content_hash == content_hash
                ):
                    return _Row({"id": vid_row.id})
            return None

        # --- SqlScanStore: content-addressed UPSERT --------------------
        if s.startswith("INSERT INTO videos"):
            (
                library_id,
                content_hash,
                path,
                filename,
                size_bytes,
                mtime,
                last_seen_at,
            ) = args
            video_match = next(
                (
                    vr
                    for vr in self.videos.values()
                    if vr.library_id == library_id and vr.content_hash == content_hash
                ),
                None,
            )
            if video_match is not None:
                video_match.path = path
                video_match.filename = filename
                video_match.size_bytes = size_bytes
                video_match.mtime = mtime
                video_match.last_seen_at = last_seen_at
                return _Row({"id": video_match.id, "inserted": False})
            new_video_id = uuid4()
            self.videos[new_video_id] = _VideoRow(
                id=new_video_id,
                library_id=library_id,
                state="discovered",
                content_hash=content_hash,
                path=path,
                filename=filename,
                size_bytes=size_bytes,
                mtime=mtime,
                last_seen_at=last_seen_at,
            )
            return _Row({"id": new_video_id, "inserted": True})

        # --- enqueue_scan: library-scoped INSERT ------------------------
        if s.startswith(
            "INSERT INTO processing_jobs (library_id, video_id, stage,"
        ):
            library_id, priority, payload, max_attempts = args
            for pj in self.processing_jobs.values():
                if (
                    pj.library_id == library_id
                    and pj.stage == "scan"
                    and pj.state
                    in {"pending", "claimed", "running", "resuming", "paused"}
                ):
                    return None  # ON CONFLICT DO NOTHING (one live scan/lib)
            new_id = self._job_next_id
            self._job_next_id += 1
            self.processing_jobs[new_id] = _ProcessingJobRow(
                id=new_id,
                video_id=None,
                library_id=library_id,
                stage="scan",
                priority=int(priority),
                payload=payload,
                attempts=0,  # fresh pending row; claim() bumps to 1
                max_attempts=int(max_attempts),
                created_at=self._now(),
            )
            return _Row({"id": new_id})

        # --- enqueue_scan: fallback live-scan SELECT --------------------
        if s.startswith("SELECT id FROM processing_jobs WHERE library_id"):
            (library_id,) = args
            for pj in self.processing_jobs.values():
                if (
                    pj.library_id == library_id
                    and pj.stage == "scan"
                    and pj.state
                    in {"pending", "claimed", "running", "resuming", "paused"}
                ):
                    return _Row({"id": pj.id})
            return None

        # --- claim_one_pg: atomic single-job claim (RETURNING *) -------
        if s.startswith("UPDATE processing_jobs SET state = 'claimed'"):
            stages = list(args[1])
            claim_candidates = sorted(
                (
                    pj
                    for pj in self.processing_jobs.values()
                    if pj.state in {"pending", "paused"}
                    and not pj.pause_requested
                    and not pj.cancel_requested
                    and pj.stage in stages
                ),
                key=lambda r: (r.priority, r.id),
            )
            if not claim_candidates:
                return None
            claim_target = claim_candidates[0]
            claim_target.state = "claimed"
            claim_target.claimed_by = args[0]
            claim_target.claimed_at = self._now()
            claim_target.last_heartbeat_at = self._now()
            claim_target.attempts += 1
            return _Row(self._job_row_full(claim_target))

        # advance_after_stage SELECT-FOR-UPDATE
        if s.startswith("SELECT state, library_id FROM videos"):
            v = self.videos.get(args[0])
            if v is None:
                return None
            return _Row({"state": v.state, "library_id": v.library_id})

        # commit_probe — current-state probe for idempotency.
        if s.startswith("SELECT state FROM videos"):
            v = self.videos.get(args[0])
            if v is None:
                return None
            return _Row({"state": v.state})

        # advance_after_stage UPDATE
        if s.startswith("UPDATE videos SET state ="):
            new_state, vid = args
            self.videos[vid].state = str(new_state)
            return None

        # commit_probe — UPSERT media_info
        if s.startswith("INSERT INTO media_info"):
            (
                video_id,
                container,
                video_codec,
                width,
                height,
                fps,
                bitrate_kbps,
                has_subtitles,
                raw,
            ) = args
            self.media_info[video_id] = _MediaInfoRow(
                video_id=video_id,
                container=container,
                video_codec=video_codec,
                width=width,
                height=height,
                fps=fps,
                bitrate_kbps=bitrate_kbps,
                has_subtitles=bool(has_subtitles),
                raw_ffprobe=raw,
            )
            return None

        # commit_probe — UPSERT audio_tracks (DO NOTHING)
        if s.startswith("INSERT INTO audio_tracks"):
            (
                video_id,
                track_index,
                codec,
                channels,
                sample_rate,
                language,
                title,
                is_default,
                disposition,
            ) = args
            for row in self.audio_tracks.values():
                if row.video_id == video_id and row.track_index == track_index:
                    return None
            new_id = self._audio_next_id
            self._audio_next_id += 1
            self.audio_tracks[new_id] = _AudioTrackRow(
                id=new_id,
                video_id=video_id,
                track_index=int(track_index),
                codec=codec,
                channels=channels,
                sample_rate=sample_rate,
                language=str(language),
                title=title,
                is_default=bool(is_default),
                disposition=disposition,
            )
            return None

        # enqueue() — done-row check
        if s.startswith("SELECT pj.id AS id, pj.finished_at"):
            return None

        # enqueue() — INSERT pending row
        if s.startswith("INSERT INTO processing_jobs"):
            video_id, stage, priority, payload, max_attempts = args
            for pj in self.processing_jobs.values():
                if (
                    pj.video_id == video_id
                    and pj.stage == stage
                    and pj.state in {"pending", "claimed", "running", "resuming", "paused"}
                ):
                    return None  # ON CONFLICT DO NOTHING
            new_id = self._job_next_id
            self._job_next_id += 1
            self.processing_jobs[new_id] = _ProcessingJobRow(
                id=new_id,
                video_id=video_id,
                stage=str(stage),
                priority=int(priority),
                payload=payload,
            )
            return _Row({"id": new_id})

        # enqueue() — fallback live SELECT
        if s.startswith("SELECT id FROM processing_jobs"):
            video_id, stage = args
            for pj in self.processing_jobs.values():
                if (
                    pj.video_id == video_id
                    and pj.stage == stage
                    and pj.state in {"pending", "claimed", "running", "resuming", "paused"}
                ):
                    return _Row({"id": pj.id})
            return None

        # commit_segment — Postgres function call
        if s.startswith("SELECT commit_segment"):
            return self._exec_commit_segment_pg(args)

        # commit_segment — SQLite path: SELECT prev end + factor
        if s.startswith("SELECT last_segment_end_sec, COALESCE(realtime_factor"):
            job = self.processing_jobs.get(int(args[0]))
            if job is None:
                return None
            return _Row(
                {
                    "last_segment_end_sec": job.last_segment_end_sec,
                    "realtime_factor": job.realtime_factor or 0,
                }
            )

        # commit_segment — SQLite INSERT
        if s.startswith("INSERT INTO transcript_segments"):
            return self._exec_segment_insert_sqlite(args)

        # SQLite last_insert_rowid
        if s.startswith("SELECT last_insert_rowid"):
            return _Row({"id": self._last_seg_id})

        # commit_segment — SQLite UPDATE processing_jobs
        if s.startswith("UPDATE processing_jobs SET last_segment_end_sec"):
            return self._exec_progress_sqlite(args)

        # flip_active_transcript / activate_transcript — UPDATE previous active
        if s.startswith("UPDATE transcripts SET is_active = false"):
            video_id, audio_track_id = args
            for tr in self.transcripts.values():
                if tr.video_id == video_id and tr.audio_track_id == audio_track_id and tr.is_active:
                    tr.is_active = False
                    tr.superseded_at = self._now()
            return None

        # activate_transcript — flip a specific (inactive) row active
        if s.startswith("UPDATE transcripts SET is_active = true"):
            (transcript_id,) = args
            target = self.transcripts.get(transcript_id)
            if target is not None:
                target.is_active = True
                target.superseded_at = None
            return None

        # flip_active_transcript / insert_inactive_transcript — INSERT row.
        # The SQL's ``is_active`` literal (true vs false) decides whether
        # the new row is born active (single-shot flip) or inactive
        # (create-then-activate split — REVIEW §1.1).
        if s.startswith("INSERT INTO transcripts"):
            born_active = "is_active, metadata)" in s and ", false, $11)" not in s
            (
                video_id,
                audio_track_id,
                language,
                detected_language,
                language_confidence,
                backend,
                model,
                backend_version,
                word_level,
                diarized,
                metadata,
            ) = args
            new = uuid4()
            self.transcripts[new] = _TranscriptRow(
                id=new,
                video_id=video_id,
                audio_track_id=int(audio_track_id),
                language=str(language),
                detected_language=detected_language,
                language_confidence=language_confidence,
                backend=str(backend),
                model=str(model),
                backend_version=backend_version,
                word_level=bool(word_level),
                diarized=bool(diarized),
                is_active=born_active,
                metadata=metadata,
                superseded_at=None,
            )
            return _Row({"id": new})

        # commit_extract — read back PROBE-persisted audio_tracks rows.
        if s.startswith("SELECT id, track_index, codec, channels, sample_rate"):
            vid = args[0]
            rows = [
                _Row(
                    {
                        "id": t.id,
                        "track_index": t.track_index,
                        "codec": t.codec,
                        "channels": t.channels,
                        "sample_rate": t.sample_rate,
                        "language": t.language,
                        "title": t.title,
                        "is_default": t.is_default,
                        "disposition": t.disposition,
                    }
                )
                for t in sorted(
                    (r for r in self.audio_tracks.values() if r.video_id == vid),
                    key=lambda r: r.track_index,
                )
            ]
            return rows if many else (rows[0] if rows else None)

        # commit_extract — UPSERT the audio_cache artifact reference.
        if s.startswith("INSERT INTO audio_cache"):
            content_hash, video_id, audio_track_id, path, nbytes = args
            self.audio_cache[str(content_hash)] = _AudioCacheRow(
                content_hash=str(content_hash),
                video_id=video_id,
                audio_track_id=int(audio_track_id),
                path=str(path),
                bytes=nbytes,
            )
            return None

        # transcribe handler — read back the EXTRACT-persisted
        # audio_cache artifact (cached WAV path + audio_track_id) by
        # the content-addressed primary key.
        if s.startswith(
            "SELECT content_hash, video_id, audio_track_id, path, bytes FROM audio_cache"
        ):
            cache_row = self.audio_cache.get(str(args[0]))
            if cache_row is None:
                return None
            return _Row(
                {
                    "content_hash": cache_row.content_hash,
                    "video_id": cache_row.video_id,
                    "audio_track_id": cache_row.audio_track_id,
                    "path": cache_row.path,
                    "bytes": cache_row.bytes,
                }
            )

        # commit_extract — stamp audio_tracks.last_extracted_at.
        if s.startswith("UPDATE audio_tracks SET last_extracted_at"):
            track_id = int(args[0])
            if track_id in self.audio_tracks:
                self.audio_tracks[track_id].last_extracted_at = self._now()
            return None

        # load_resume_point — SELECT last K segments
        if s.startswith("SELECT seq, text FROM transcript_segments"):
            transcript_id, k = args
            segs = sorted(
                (s for s in self.transcript_segments.values() if s.transcript_id == transcript_id),
                key=lambda r: -r.seq,
            )[: int(k)]
            return [_Row({"seq": r.seq, "text": r.text}) for r in segs]

        # --- jobs_state.mark_done — terminal success transition --------
        if s.startswith("UPDATE processing_jobs SET state = 'done'"):
            job = self.processing_jobs.get(int(args[0]))
            if job is None or job.state not in {"claimed", "running", "resuming"}:
                return None
            job.state = "done"
            job.finished_at = self._now()
            job.claimed_by = None
            return _Row({"id": job.id, "state": "done"})

        # --- jobs_state.mark_failed_or_retry — read attempts -----------
        if s.startswith("SELECT attempts, max_attempts FROM processing_jobs"):
            job = self.processing_jobs.get(int(args[0]))
            if job is None:
                return None
            return _Row(
                {"attempts": job.attempts, "max_attempts": job.max_attempts}
            )

        # --- jobs_state.mark_failed_or_retry — write failed/pending ----
        if s.startswith("UPDATE processing_jobs SET state = $2") or s.startswith(
            "UPDATE processing_jobs SET state = ?"
        ):
            if self.dialect == "postgres":
                job_id, new_state, not_before, err_json = (
                    args[0],
                    args[1],
                    args[2],
                    args[3],
                )
            else:
                new_state, not_before, err_json, _finished, job_id = (
                    args[0],
                    args[1],
                    args[2],
                    args[3],
                    args[4],
                )
            job = self.processing_jobs.get(int(job_id))
            if job is None or job.state not in {"claimed", "running", "resuming"}:
                return None
            job.state = str(new_state)
            job.error = err_json
            return _Row(
                {"id": job.id, "state": str(new_state), "not_before": not_before}
            )

        raise AssertionError(f"unexpected SQL in fake audio DB: {s!r}")

    # commit_segment helpers --------------------------------------------

    def _exec_commit_segment_pg(self, args: tuple[Any, ...]) -> _Row | None:
        (
            transcript_id,
            job_id,
            seq,
            start_sec,
            end_sec,
            text,
            speaker,
            confidence,
            audio_sec_in_seg,
            wall_sec_in_seg,
            total_duration_sec,
            ewma_alpha,
        ) = args
        job = self.processing_jobs.get(int(job_id))
        if job is None:
            return _Row({"id": None})
        existing = next(
            (
                s
                for s in self.transcript_segments.values()
                if s.transcript_id == transcript_id and s.seq == int(seq)
            ),
            None,
        )
        if existing is not None:
            return _Row({"id": None})
        seg_id = self._seg_next_id
        self._seg_next_id += 1
        self.transcript_segments[seg_id] = _TranscriptSegmentRow(
            id=seg_id,
            transcript_id=transcript_id,
            seq=int(seq),
            start_sec=float(start_sec),
            end_sec=float(end_sec),
            text=str(text),
            speaker=speaker,
            confidence=confidence,
        )
        prev_end = job.last_segment_end_sec
        prev_factor = job.realtime_factor or 0
        processed_delta = max(0.0, float(end_sec) - max(prev_end, float(start_sec)))
        if wall_sec_in_seg > 0:
            sample = audio_sec_in_seg / wall_sec_in_seg
            factor = prev_factor * (1 - ewma_alpha) + sample * ewma_alpha
        else:
            factor = prev_factor
        eta = None
        if factor > 0 and total_duration_sec > 0:
            eta = max(0.0, (total_duration_sec - min(end_sec, total_duration_sec)) / factor)
        job.last_segment_end_sec = max(job.last_segment_end_sec, float(end_sec))
        job.processed_seconds += processed_delta
        job.segments_completed += 1
        job.realtime_factor = float(factor)
        job.estimated_remaining_sec = float(eta) if eta is not None else None
        job.progress_updated_at = self._now()
        job.last_heartbeat_at = self._now()
        return _Row({"id": seg_id})

    def _exec_segment_insert_sqlite(self, args: tuple[Any, ...]) -> None:
        (transcript_id, seq, start_sec, end_sec, text, speaker, confidence) = args
        tid = UUID(str(transcript_id))
        existing = next(
            (
                s
                for s in self.transcript_segments.values()
                if s.transcript_id == tid and s.seq == int(seq)
            ),
            None,
        )
        if existing is not None:
            self._last_seg_id = 0
            return None
        seg_id = self._seg_next_id
        self._seg_next_id += 1
        self.transcript_segments[seg_id] = _TranscriptSegmentRow(
            id=seg_id,
            transcript_id=tid,
            seq=int(seq),
            start_sec=float(start_sec),
            end_sec=float(end_sec),
            text=str(text),
            speaker=speaker,
            confidence=confidence,
        )
        self._last_seg_id = seg_id
        return None

    def _exec_progress_sqlite(self, args: tuple[Any, ...]) -> None:
        (end_sec_max, processed_delta, factor, eta, job_id) = args
        job = self.processing_jobs.get(int(job_id))
        if job is None:
            return None
        job.last_segment_end_sec = max(job.last_segment_end_sec, float(end_sec_max))
        job.processed_seconds += float(processed_delta)
        job.segments_completed += 1
        job.realtime_factor = float(factor)
        job.estimated_remaining_sec = float(eta) if eta is not None else None
        job.progress_updated_at = self._now()
        job.last_heartbeat_at = self._now()
        return None

    _last_seg_id: int = 0
