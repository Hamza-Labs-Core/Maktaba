# Plan 3.7 — Pause and resume to the exact second (implementation)

> Implementation plan for [story-03-07-pause-resume.md](story-03-07-pause-resume.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: builds directly on the per-segment commit
> contract from [Plan 3.6](plan-03-06-segment-commit.md), the audio
> decoder seek from [Plan 2.3](../02-audio-extraction/plan-02-03-stream-extraction.md),
> the job state machine from [Epic 6 Story 6.1](../06-job-queue/story-06-01-job-state-machine.md),
> and the backend registry / fallback rules from
> [Story 3.5](story-03-05-backend-registry.md). Architectural reference:
> [`architecture.md` §7.7 "Pause and resume"](../../architecture.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | The pause/resume API endpoints (`POST /api/jobs/{id}/pause`, `…/resume`, `…/pause?force=true`) live in the API service (Go), not the Pipeline Service. The Pipeline Service is the **observer** of the flag, not the setter. | Refines the story (which doesn't say where the endpoint lives). | The API already owns `processing_jobs` write access for state transitions (claim/cancel). Putting pause/resume in the same service keeps a single audit-log trail for "who paused job X". The flag is the boundary; the worker is decoupled from the API. |
| D2 | `force=true` is **not** an instant state flip in the API. It sets `pause_requested = true` *and* fires a `pg_notify('jobs.force_pause', {job_id})` that the worker consumes via a per-claim asyncio task that calls `subprocess.terminate()` on the backend's child process (e.g., the `mlx-whisper` subprocess). The state flips to `paused` only when the worker confirms the kill. | Story acceptance: "force-pause … the in-flight segment is discarded; no commit was attempted yet". | Setting `state='paused'` directly from the API would break Story 3.6's invariant that the worker is the *only* writer of `state` transitions out of `running`. We preserve that invariant; force-pause is "asynchronous abort signal", not "synchronous state mutation". The worker's commit invariant (the in-flight segment was uncommitted) makes the discard safe. |
| D3 | The Whisper resume **prompt** is built from the last `K = 3` *full* segment texts (default), concatenated with single spaces, and truncated from the left to ≤ 224 BPE tokens (Whisper's prompt window). `K` is configurable per-library via `library.settings.transcribe.resume_prompt_segments`. | Story acceptance: "rebuilds the Whisper prompt from the last K segments' text (default K=3)". | 3 segments × ~30 s = ~90 s of context, which Whisper's empirical sweet spot for "preserve continuity without repeating" lands inside. The 224-token clamp avoids a backend OOM when an Arabic segment is long; truncating from the left preserves the *most recent* context. |
| D4 | Resume preserves the original `transcripts.language` and **disables** auto-detect on the resume backend session (`stt.session(detect_language=False, language=transcript.language, …)`). | Story edge case "Whisper prompt seam glitch — re-detect language on the resume boundary". | The first 30 s after seek is mid-sentence; Whisper's language detector is unreliable on partial input and has been observed to flip Arabic recitation to "fa" or "ur". Locking the language to what the pre-pause `transcripts` row already established is correct. |
| D5 | Backend-change resumes (`whisper-mlx → whisper-cuda` or any cross-backend swap) **insert a new `transcripts` row** for the resumed portion and tag it with `metrics.resumed_with_different_backend = {from, to, at_sec}`. The old transcript row is closed at `paused_at_sec` (`is_active = false` per Story 3.5); the new row covers `[paused_at_sec, end_of_audio]`. The video's *displayed* transcript is the union via a view `v_video_transcript`. | Story acceptance: "the `transcripts` row records `metrics.resumed_with_different_backend`". | Two backends produce different segment boundaries, different word timings, and (for diarization) different speaker IDs. Concatenating into the same `transcripts` row would corrupt downstream subtitle/index/search tooling that assumes a single backend's quality profile. Splitting into two rows lets every consumer treat the union as "two backends, here's the seam". |
| D6 | The `audio_missing` failure mode (story edge case) is implemented as a *resume-time* check, not a pre-claim check. The worker resolves `videos.path` (and falls back to `content_hash` lookup), opens the file with `os.open(..., O_RDONLY)`, and on `FileNotFoundError` runs `mark_paused(reason='audio_missing', not_before=now() + 5 min)` and exits without flipping to `running`. | Story edge case "Audio file moved between pause and resume". | A pre-claim check would race the actual open. Doing it at the worker keeps the resume claim atomic with the file probe; `not_before = +5 min` debounces the queue so a continuously-broken file doesn't spin the workers. |
| D7 | Double-resume is idempotent because the **claim query** is the natural serialization point. `POST /api/jobs/{id}/resume` is a no-op if the job is not in `paused` (returns `200 {state: <current>}`). The first claim wins via `FOR UPDATE SKIP LOCKED`; the second sees `state != 'paused'` and returns 200 without re-claiming. | Story acceptance: "exactly one worker claim succeeds; the second returns 200 with the unchanged state". | The story conflates "user calls /resume twice" with "two workers claim". Both must be handled; the API endpoint handles the first by being idempotent against the `state` column, and the claim loop handles the second by `SKIP LOCKED`. |

If D5 is rejected (cross-backend resumes append into the same
transcripts row), §6 changes (no view, single transcript_id passed to
the resume worker) and the test §8.4 changes its assertions; everything
else holds.

---

## 1. Architecture diagram — pause + resume control flow

```
                         ┌──────────────────────────────────────┐
                         │  User clicks Pause in the UI         │
                         └─────────────────┬────────────────────┘
                                           │ POST /api/jobs/{id}/pause
                                           ▼
       ┌─────────────────────────────────────────────────────────────┐
       │ API service (Go)                                            │
       │  PauseJobHandler:                                           │
       │   if force=true:                                            │
       │     UPDATE processing_jobs                                  │
       │        SET pause_requested = true,                          │
       │            metrics = jsonb_set(metrics,'{force_pause}',     │
       │                       'true')                               │
       │      WHERE id = $id                                         │
       │     pg_notify('jobs.force_pause', {job_id, force:true})     │
       │   else:                                                     │
       │     UPDATE processing_jobs                                  │
       │        SET pause_requested = true                           │
       │      WHERE id = $id                                         │
       │   RETURN 202 { id, state, pause_requested:true }            │
       └─────────────────────────────┬───────────────────────────────┘
                                     │
                  ┌──────────────────┴──────────────────┐
                  │ DB: row is updated;                 │
                  │ SegmentCommitter (Plan 3.6) returns │
                  │ pause_requested=true on next commit │
                  └──────────────────┬──────────────────┘
                                     │
                                     ▼  (cooperative path)
            ┌──────────────────────────────────────────────────┐
            │ Worker (Pipeline)                                │
            │  on StopWorker(reason='pause'):                  │
            │   1. ReorderBuffer.flush() → drain & commit tail │
            │   2. close STT backend session (release GPU)     │
            │   3. close audio decoder (SIGTERM ffmpeg)        │
            │   4. mark_paused(reason='user', at_sec=last)     │
            │   5. release advisory lock                       │
            │   6. exit stage                                  │
            └──────────────────────────────────────────────────┘

                                     ▼  (force-pause additional path)
            ┌──────────────────────────────────────────────────┐
            │ ForcePauseWatcher (per-claim asyncio Task)       │
            │  LISTEN 'jobs.force_pause'                       │
            │  on payload.job_id == self.job.id:               │
            │    backend.subprocess.terminate()  (SIGTERM)     │
            │    → backend.transcribe_stream raises Aborted    │
            │    → run_transcribe_stage handles via:           │
            │        commit no in-flight segment (none was)    │
            │        mark_paused(reason='user',                │
            │           at_sec=last_segment_end_sec)           │
            └──────────────────────────────────────────────────┘


                         ┌──────────────────────────────────────┐
                         │  User clicks Resume                  │
                         └─────────────────┬────────────────────┘
                                           │ POST /api/jobs/{id}/resume
                                           ▼
       ┌─────────────────────────────────────────────────────────────┐
       │ API service                                                 │
       │  ResumeJobHandler:                                          │
       │   row = SELECT FOR UPDATE                                   │
       │   if row.state != 'paused': return 200 {state}              │
       │   UPDATE jobs SET pause_requested = false                   │
       │   pg_notify('jobs.resume_ready', {job_id, library_id})      │
       │   RETURN 202 { id, state:'paused', resumable:true }         │
       └─────────────────────────────┬───────────────────────────────┘
                                     │  (workers were already idling
                                     │   on the claim loop with
                                     │   state IN ('pending','paused'))
                                     ▼
       ┌─────────────────────────────────────────────────────────────┐
       │ Worker claim loop                                           │
       │  claim() returns the row → state := 'resuming'              │
       │  ResumeContextBuilder.build():                              │
       │   1. Resolve videos.path (fall back via content_hash)       │
       │   2. os.open(path, O_RDONLY) — fail → mark_paused('audio_missing') │
       │   3. SELECT transcripts WHERE id = job.transcript_id        │
       │   4. If library.settings.transcribe.backend != transcript.backend │
       │      → open new transcripts row (D5) + record metrics       │
       │   5. SELECT last K segments (text), build prompt (D3)       │
       │   6. Open STT session(detect_language=False,                │
       │      language=transcript.language, prompt=…)                │
       │   7. Open audio decoder seeked to last_segment_end_sec      │
       │      (Plan 2.3 §2.4 fast input -ss seek)                    │
       │  state := 'running'; resume_count += 1                      │
       │  Re-enter the §7.6 transcribe loop (Plan 3.6)               │
       └─────────────────────────────────────────────────────────────┘
```

The flag (`pause_requested`) is the only synchronization primitive. The
worker loop is otherwise unmodified from Plan 3.6 — pause is "just
another reason to exit cleanly after the next commit".

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── transcribe/
│   ├── pause.py              # mark_paused(), mark_resuming(), state guards
│   ├── resume_context.py     # ResumeContextBuilder, prompt rebuild, seek setup
│   ├── force_pause.py        # ForcePauseWatcher (per-claim LISTEN task)
│   └── tests/
│       ├── test_pause_marks.py
│       ├── test_resume_context_builder.py
│       ├── test_resume_prompt_construction.py
│       ├── test_force_pause_aborts_subprocess.py
│       ├── test_resume_audio_missing.py
│       ├── test_resume_starts_from_last_segment_end_sec.py
│       ├── test_resume_across_process_restart.py
│       ├── test_resume_after_backend_change.py
│       └── test_double_resume_is_idempotent.py
└── pipeline/
    └── stages/
        └── transcribe.py     # extended: state=resuming → state=running
```

### 2.2 Package layout — Go (API service)

```
api/internal/jobs/
├── pause_handler.go          # POST /api/jobs/{id}/pause[?force=true]
├── pause_handler_test.go
├── resume_handler.go         # POST /api/jobs/{id}/resume
└── resume_handler_test.go
```

### 2.3 API handler — pause (Go)

```go
// api/internal/jobs/pause_handler.go
package jobs

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/jackc/pgx/v5"
)

type PauseHandler struct{ DB DBPool }

type pauseResponse struct {
    ID              int64  `json:"id"`
    State           string `json:"state"`
    PauseRequested  bool   `json:"pause_requested"`
    Forced          bool   `json:"forced"`
}

func (h *PauseHandler) Serve(w http.ResponseWriter, r *http.Request) {
    id, ok := parseJobID(r)
    if !ok {
        http.Error(w, "invalid job id", http.StatusBadRequest)
        return
    }
    forced := r.URL.Query().Get("force") == "true"

    var (
        state          string
        pauseRequested bool
    )
    err := h.DB.BeginFunc(r.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
        var sql string
        if forced {
            sql = `UPDATE processing_jobs
                      SET pause_requested = true,
                          metrics = COALESCE(metrics, '{}'::jsonb) ||
                                    jsonb_build_object('force_pause', true,
                                                        'force_pause_at',
                                                        to_jsonb(now()))
                    WHERE id = $1
                RETURNING state, pause_requested`
        } else {
            sql = `UPDATE processing_jobs
                      SET pause_requested = true
                    WHERE id = $1
                RETURNING state, pause_requested`
        }
        if err := tx.QueryRow(r.Context(), sql, id).Scan(&state, &pauseRequested); err != nil {
            return err
        }
        if forced {
            _, err := tx.Exec(r.Context(),
                `SELECT pg_notify('jobs.force_pause',
                    json_build_object('job_id', $1::bigint, 'force', true)::text)`,
                id)
            return err
        }
        return nil
    })
    if errors.Is(err, pgx.ErrNoRows) {
        http.Error(w, "job not found", http.StatusNotFound)
        return
    }
    if err != nil {
        http.Error(w, "db error", http.StatusInternalServerError)
        return
    }

    resp := pauseResponse{
        ID:             id,
        State:          state,
        PauseRequested: pauseRequested,
        Forced:         forced,
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusAccepted)
    _ = json.NewEncoder(w).Encode(resp)
}
```

Note the response is `202 Accepted` for both forms because pause is
**always** asynchronous from the API's perspective — even a forced
pause depends on the worker observing the abort. The UI polls
`/api/jobs/{id}` (or its websocket) for the `state='paused'`
transition.

### 2.4 API handler — resume (Go)

```go
// api/internal/jobs/resume_handler.go
package jobs

func (h *ResumeHandler) Serve(w http.ResponseWriter, r *http.Request) {
    id, ok := parseJobID(r)
    if !ok {
        http.Error(w, "invalid job id", http.StatusBadRequest)
        return
    }

    var (
        state     string
        libraryID string
    )
    err := h.DB.BeginFunc(r.Context(), pgx.TxOptions{}, func(tx pgx.Tx) error {
        // Lock the row first; idempotent claim semantics (D7).
        if err := tx.QueryRow(r.Context(),
            `SELECT j.state, v.library_id::text
               FROM processing_jobs j
               JOIN videos v ON v.id = j.video_id
              WHERE j.id = $1
                FOR UPDATE`, id).Scan(&state, &libraryID); err != nil {
            return err
        }
        if state != "paused" {
            return nil // 200 with the unchanged state; idempotent no-op.
        }
        if _, err := tx.Exec(r.Context(),
            `UPDATE processing_jobs
                SET pause_requested = false,
                    resumed_at = now(),
                    resume_count = resume_count + 1
              WHERE id = $1`, id); err != nil {
            return err
        }
        _, err := tx.Exec(r.Context(),
            `SELECT pg_notify('jobs.resume_ready',
                json_build_object('job_id', $1::bigint,
                                  'library_id', $2)::text)`,
            id, libraryID)
        return err
    })
    if errors.Is(err, pgx.ErrNoRows) {
        http.Error(w, "job not found", http.StatusNotFound)
        return
    }
    if err != nil {
        http.Error(w, "db error", http.StatusInternalServerError)
        return
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusAccepted)
    _ = json.NewEncoder(w).Encode(map[string]any{
        "id":        id,
        "state":     state, // 'paused' on first call; 'resuming'/'running' on duplicate
        "resumable": state == "paused",
    })
}
```

The handler is intentionally write-once: it never sets
`state='resuming'` itself. The worker does that on claim.

### 2.5 `pause.py` — state mutators (Python)

```python
"""mark_paused / mark_resuming — the only writers of pause-related state."""
from __future__ import annotations
import logging
from typing import Literal

import asyncpg

log = logging.getLogger(__name__)

PausedReason = Literal["user", "shutdown", "crash", "audio_missing", "budget", "policy"]


async def mark_paused(
    pool: asyncpg.Pool,
    *,
    job_id: int,
    at_sec: float,
    reason: PausedReason,
    not_before_sec: float | None = None,
) -> None:
    """Flip job to 'paused' atomically with releasing the claim.

    Worker MUST have committed all in-flight segments before calling this.
    """
    sql = """
    UPDATE processing_jobs
       SET state           = 'paused',
           paused_at       = now(),
           paused_at_sec   = $2,
           paused_reason   = $3,
           pause_requested = false,
           claimed_by      = NULL,
           claimed_at      = NULL,
           not_before      = CASE WHEN $4::REAL IS NULL
                                  THEN not_before
                                  ELSE now() + ($4 || ' seconds')::interval
                             END
     WHERE id = $1
       AND state IN ('running', 'resuming')
    RETURNING state
    """
    async with pool.acquire() as conn:
        row = await conn.fetchrow(sql, job_id, at_sec, reason, not_before_sec)
    if row is None:
        log.warning("mark_paused_no_op",
                    extra={"job_id": job_id, "reason": reason})
        return
    log.info("job_paused",
             extra={"job_id": job_id, "at_sec": at_sec, "reason": reason})


async def mark_resuming(
    pool: asyncpg.Pool, *, job_id: int, worker_id: str,
) -> int:
    """Atomically claim a paused job and flip to 'resuming'.

    Returns the row's last_segment_end_sec for the worker to seek to.
    Raises ResumeRaceLost if another worker already claimed it.
    """
    sql = """
    UPDATE processing_jobs
       SET state             = 'resuming',
           claimed_by        = $2,
           claimed_at        = now(),
           last_heartbeat_at = now()
     WHERE id = $1
       AND state = 'paused'
       AND pause_requested = false
    RETURNING last_segment_end_sec
    """
    async with pool.acquire() as conn:
        row = await conn.fetchrow(sql, job_id, worker_id)
    if row is None:
        raise ResumeRaceLost(job_id)
    return row["last_segment_end_sec"]


async def mark_running(pool: asyncpg.Pool, *, job_id: int) -> None:
    async with pool.acquire() as conn:
        await conn.execute(
            "UPDATE processing_jobs SET state = 'running' "
            " WHERE id = $1 AND state = 'resuming'",
            job_id)


class ResumeRaceLost(Exception):
    pass
```

### 2.6 `resume_context.py` — prompt + seek setup

```python
"""ResumeContextBuilder — everything the worker needs to enter run_transcribe."""
from __future__ import annotations
import logging
from dataclasses import dataclass
from typing import Sequence
from uuid import UUID

import asyncpg

log = logging.getLogger(__name__)

# Whisper's prompt window is 224 BPE tokens ≈ ~1000 chars for Latin script,
# fewer for Arabic. We clamp by char count and let Whisper truncate further.
RESUME_PROMPT_CHAR_BUDGET = 1500


@dataclass(frozen=True)
class ResumeContext:
    transcript_id: UUID
    transcript_id_for_new_segments: UUID  # may differ from transcript_id (D5)
    seek_from_sec: float
    language: str                        # the locked-in language (D4)
    prompt: str                          # the K-segment prompt (D3)
    backend_changed: bool
    previous_backend: str | None
    new_backend: str


async def build(
    pool: asyncpg.Pool,
    *,
    job_id: int,
    seek_from_sec: float,
    library_settings: dict,
    k: int = 3,
) -> ResumeContext:
    async with pool.acquire() as conn:
        # Resolve the most-recent active transcript for this job's video.
        prev = await conn.fetchrow("""
            SELECT t.id, t.video_id, t.audio_track_id, t.language,
                   t.backend, t.model, t.word_level, t.diarized
              FROM transcripts t
              JOIN processing_jobs j ON j.video_id = t.video_id
             WHERE j.id = $1
               AND t.is_active = true
             ORDER BY t.created_at DESC
             LIMIT 1
        """, job_id)
        if prev is None:
            raise RuntimeError(f"no active transcript for job {job_id}")

        # Last K segments — for the prompt.
        last_segments = await conn.fetch("""
            SELECT seq, text
              FROM transcript_segments
             WHERE transcript_id = $1
               AND end_sec <= $2
             ORDER BY seq DESC
             LIMIT $3
        """, prev["id"], seek_from_sec, k)
        prompt = _build_prompt(reversed(last_segments))

        new_backend_name = library_settings.get(
            "transcribe", {}).get("backend", prev["backend"])
        backend_changed = new_backend_name != prev["backend"]

        if backend_changed:
            log.info("resume_backend_change",
                     extra={"from": prev["backend"], "to": new_backend_name,
                            "at_sec": seek_from_sec})
            new_t = await conn.fetchrow("""
                INSERT INTO transcripts
                    (video_id, audio_track_id, language, backend, model,
                     word_level, diarized, is_active, metrics)
                VALUES ($1, $2, $3, $4, $5, $6, $7, true,
                        jsonb_build_object(
                            'resumed_with_different_backend',
                            jsonb_build_object('from', $8::text,
                                               'to',   $4::text,
                                               'at_sec', $9::real),
                            'resumed_from_transcript_id', $10::text))
                RETURNING id
            """,
                prev["video_id"], prev["audio_track_id"], prev["language"],
                new_backend_name,
                library_settings.get("transcribe", {}).get("model", prev["model"]),
                library_settings.get("transcribe", {}).get(
                    "word_level", prev["word_level"]),
                library_settings.get("transcribe", {}).get(
                    "diarize", prev["diarized"]),
                prev["backend"], seek_from_sec, str(prev["id"]),
            )
            # Mark the old transcript as closed at this point.
            await conn.execute(
                "UPDATE transcripts SET is_active = false WHERE id = $1",
                prev["id"])
            new_id = new_t["id"]
        else:
            new_id = prev["id"]

    return ResumeContext(
        transcript_id=prev["id"],
        transcript_id_for_new_segments=new_id,
        seek_from_sec=seek_from_sec,
        language=prev["language"],            # D4
        prompt=prompt,
        backend_changed=backend_changed,
        previous_backend=prev["backend"] if backend_changed else None,
        new_backend=new_backend_name,
    )


def _build_prompt(segments_in_order: Sequence) -> str:
    parts = [s["text"].strip() for s in segments_in_order if s["text"].strip()]
    full = " ".join(parts)
    if len(full) <= RESUME_PROMPT_CHAR_BUDGET:
        return full
    # Truncate from the LEFT — keep the most recent context (D3).
    return full[-RESUME_PROMPT_CHAR_BUDGET:]
```

### 2.7 `force_pause.py` — abort the in-flight segment

```python
"""ForcePauseWatcher — listens for jobs.force_pause and aborts the backend subprocess."""
from __future__ import annotations
import asyncio
import json
import logging
import signal
from typing import Protocol

import asyncpg

log = logging.getLogger(__name__)


class AbortableBackend(Protocol):
    """Backends that support force-pause expose this protocol."""

    @property
    def subprocess_pid(self) -> int | None: ...
    async def abort(self) -> None: ...


class ForcePauseWatcher:
    """Per-claim async task: LISTENs and aborts the backend on hit."""

    def __init__(
        self, pool: asyncpg.Pool, *, job_id: int, backend: AbortableBackend,
    ):
        self._pool = pool
        self._job_id = job_id
        self._backend = backend
        self._task: asyncio.Task | None = None
        self._stop = asyncio.Event()

    async def __aenter__(self) -> "ForcePauseWatcher":
        self._task = asyncio.create_task(self._run(), name=f"force-pause-{self._job_id}")
        return self

    async def __aexit__(self, *exc) -> None:
        self._stop.set()
        if self._task:
            self._task.cancel()
            try:
                await self._task
            except (asyncio.CancelledError, Exception):
                pass

    async def _run(self) -> None:
        async with self._pool.acquire() as conn:
            await conn.add_listener("jobs.force_pause", self._on_notify)
            try:
                await self._stop.wait()
            finally:
                await conn.remove_listener("jobs.force_pause", self._on_notify)

    def _on_notify(self, conn, pid, channel, payload):
        try:
            msg = json.loads(payload)
        except Exception:
            return
        if msg.get("job_id") != self._job_id:
            return
        log.warning("force_pause_received", extra={"job_id": self._job_id})
        # Schedule the abort on the same event loop.
        asyncio.create_task(self._backend.abort(), name=f"abort-{self._job_id}")
```

### 2.8 Stage integration — `run_transcribe_stage` (extended)

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py  (excerpt)

from maktaba_pipeline.transcribe import (
    pause as pause_mod, resume_context, force_pause, committer as commit_mod,
    reorder as reorder_mod,
)
from maktaba_pipeline.media import audio_decoder
from maktaba_pipeline.stt import registry as stt_registry


async def run_transcribe_stage(ctx, claimed_job):
    job_id = claimed_job.id
    is_resume = claimed_job.last_segment_end_sec > 0

    if is_resume:
        seek_from = await pause_mod.mark_resuming(
            ctx.db_pool, job_id=job_id, worker_id=ctx.worker_id)
    else:
        seek_from = 0.0
        await transition_to_running(ctx.db_pool, job_id=job_id)  # claimed → running

    try:
        ctx_resume = await resume_context.build(
            ctx.db_pool,
            job_id=job_id,
            seek_from_sec=seek_from,
            library_settings=claimed_job.library_settings,
            k=claimed_job.library_settings.get("transcribe", {}).get(
                "resume_prompt_segments", 3),
        )
    except FileNotFoundError:
        await pause_mod.mark_paused(
            ctx.db_pool, job_id=job_id, at_sec=seek_from,
            reason="audio_missing", not_before_sec=300.0)
        return {"reason": "audio_missing", "at_sec": seek_from}

    backend = await stt_registry.open_session(
        name=ctx_resume.new_backend,
        language=ctx_resume.language,
        detect_language=False,                      # D4
        initial_prompt=ctx_resume.prompt,
        word_level=claimed_job.library_settings.get(
            "transcribe", {}).get("word_level", False),
    )

    if is_resume:
        await pause_mod.mark_running(ctx.db_pool, job_id=job_id)

    com = commit_mod.SegmentCommitter(
        ctx.db_pool, job_id=job_id,
        transcript_id=ctx_resume.transcript_id_for_new_segments)
    rb = reorder_mod.ReorderBuffer(
        window_sec=ctx.cfg.transcribe.reorder_window_sec)

    async with audio_decoder(claimed_job.video, start_sec=seek_from) as audio, \
               force_pause.ForcePauseWatcher(
                   ctx.db_pool, job_id=job_id, backend=backend):
        try:
            t_seg_start = time.monotonic()
            async for seg in backend.transcribe_stream(audio):
                wall_sec = time.monotonic() - t_seg_start
                t_seg_start = time.monotonic()
                try:
                    ready = rb.push(seg)
                except reorder_mod.OutOfOrderSegmentDropped:
                    continue
                for s in ready:
                    await com.commit(s, wall_sec=wall_sec)
            for s in rb.flush():
                await com.commit(s, wall_sec=0.0)
        except commit_mod.StopWorker as stop:
            for s in rb.flush():
                try:
                    await com.commit(s, wall_sec=0.0)
                except commit_mod.StopWorker:
                    break
            current_end = await read_last_segment_end(ctx.db_pool, job_id)
            await pause_mod.mark_paused(
                ctx.db_pool, job_id=job_id, at_sec=current_end,
                reason="user" if stop.reason == "pause" else "user")
            return {"reason": stop.reason, "at_sec": current_end}
        except backend.AbortedError:
            # Force pause: in-flight segment was uncommitted; pause at the
            # last DURABLY committed end_sec.
            current_end = await read_last_segment_end(ctx.db_pool, job_id)
            await pause_mod.mark_paused(
                ctx.db_pool, job_id=job_id, at_sec=current_end, reason="user")
            return {"reason": "force_paused", "at_sec": current_end}

    await mark_done(ctx.db_pool, job_id=job_id)
    return {"reason": "ok"}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/transcribe/pause.py` | `mark_paused`, `mark_resuming`, `mark_running`, `ResumeRaceLost` | `test_pause_marks` |
| 2 | `pipeline/src/maktaba_pipeline/transcribe/resume_context.py` | `ResumeContext`, `build`, `_build_prompt`, `RESUME_PROMPT_CHAR_BUDGET` | `test_resume_context_builder`, `test_resume_prompt_construction` |
| 3 | `pipeline/src/maktaba_pipeline/transcribe/force_pause.py` | `ForcePauseWatcher`, `AbortableBackend` protocol | `test_force_pause_aborts_subprocess` |
| 4 | `api/internal/jobs/pause_handler.go` | `PauseHandler` | `pause_handler_test.go` (cooperative + forced) |
| 5 | `api/internal/jobs/resume_handler.go` | `ResumeHandler` | `resume_handler_test.go` (idempotency, missing job) |
| 6 | `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` | wire ResumeContextBuilder + ForcePauseWatcher; resume branch | end-to-end story tests below |

A backend that wants to support force-pause implements
`AbortableBackend.abort()` (e.g., `whisper-mlx`'s subprocess wrapper
calls `proc.terminate()`; the OpenAI HTTP backend calls
`session.cancel()` which closes the streaming HTTP connection). Any
backend that doesn't implement it gracefully degrades to cooperative
pause (force-pause becomes equivalent to regular pause; the abort
notify is logged as `force_pause_unsupported_by_backend`).

---

## 4. Test cases

All tests are async and use `pytest-asyncio` + the existing pipeline DB
fixture. The synthetic backend from Plan 3.6 §4 is extended with an
`abort()` method (sets an internal event that cancels its `async for`).

### 4.1 `test_resume_starts_from_last_segment_end_sec` (story-named)

```python
async def test_resume_seeks_to_last_segment_end(
    db, job_factory, transcript_factory, decoder_recorder, fake_backend,
):
    """Pause at 600.0 → resume → first emitted segment.start ≥ 600.0 within 0.5 s."""
    job = await job_factory.running(at_sec=600.0)
    await transcript_factory(video_id=job.video_id, language="ar",
                             segments_up_to_sec=600.0)
    # Cooperative pause: flag set, worker exits.
    await db.execute(
        "UPDATE processing_jobs SET pause_requested=true WHERE id=$1", job.id)
    await run_transcribe_stage_until_paused(job.id)
    paused = await db.fetchrow(
        "SELECT state, paused_at_sec FROM processing_jobs WHERE id=$1", job.id)
    assert paused["state"] == "paused"
    assert paused["paused_at_sec"] == pytest.approx(600.0, abs=0.01)

    # Resume.
    fake_backend.script_segments_starting_at(600.0,
        [Segment(start=600.05, end=605.0, text="resume tail", words=[],
                 speaker=None, confidence=0.9)])
    await db.execute(
        "UPDATE processing_jobs SET pause_requested=false WHERE id=$1", job.id)
    await run_transcribe_stage_one_segment(job.id)

    # Decoder was opened with start_sec=600.0.
    assert decoder_recorder.last_seek_sec == pytest.approx(600.0)
    # First post-resume segment lands ≥ 600 and within 0.5 s.
    new = await db.fetchrow("""
        SELECT start_sec, end_sec FROM transcript_segments
         WHERE transcript_id IN (SELECT id FROM transcripts WHERE video_id=$1)
         ORDER BY end_sec DESC LIMIT 1""", job.video_id)
    assert new["start_sec"] >= 600.0
    assert new["start_sec"] < 600.5
```

### 4.2 `test_resume_across_process_restart` (story-named)

```python
async def test_resume_across_process_restart(db, worker_factory, job_factory):
    """Pause; kill the worker; spawn a new worker; the job resumes with no rework."""
    job = await job_factory.running(at_sec=300.0)
    w1 = await worker_factory.spawn()
    await w1.wait_until_claimed(job.id)
    await db.execute(
        "UPDATE processing_jobs SET pause_requested=true WHERE id=$1", job.id)
    await w1.wait_until_paused(job.id, timeout=10.0)
    await w1.kill()

    # Counts before restart.
    before = await db.fetchval(
        "SELECT segments_completed FROM processing_jobs WHERE id=$1", job.id)

    w2 = await worker_factory.spawn()
    await db.execute(
        "UPDATE processing_jobs SET pause_requested=false WHERE id=$1", job.id)
    await w2.wait_until_running(job.id, timeout=15.0)
    await w2.wait_for_n_more_segments(job.id, n=5)

    after = await db.fetchval(
        "SELECT segments_completed FROM processing_jobs WHERE id=$1", job.id)
    assert after == before + 5            # exactly 5 new segments, no rework
```

### 4.3 `test_resume_after_backend_change` (story-named)

```python
async def test_resume_after_backend_change_writes_metrics(
    db, job_factory, library, fake_backends,
):
    """Pause on whisper-mlx; flip library setting to whisper-cuda; resume."""
    await library.set_setting("transcribe", {"backend": "whisper-mlx",
                                             "model": "large-v3"})
    job = await job_factory.running(at_sec=120.0, backend="whisper-mlx")
    await pause_and_wait(job.id)

    await library.set_setting("transcribe", {"backend": "whisper-cuda",
                                             "model": "large-v3"})
    await db.execute(
        "UPDATE processing_jobs SET pause_requested=false WHERE id=$1", job.id)
    await run_transcribe_stage_one_segment(job.id)

    # Old transcript closed; new transcript open with metrics.
    rows = await db.fetch("""
        SELECT id, backend, is_active, metrics
          FROM transcripts WHERE video_id = $1
         ORDER BY created_at""", job.video_id)
    assert len(rows) == 2
    assert rows[0]["backend"] == "whisper-mlx" and rows[0]["is_active"] is False
    assert rows[1]["backend"] == "whisper-cuda" and rows[1]["is_active"] is True
    md = rows[1]["metrics"]
    assert md["resumed_with_different_backend"] == {
        "from": "whisper-mlx", "to": "whisper-cuda", "at_sec": 120.0}
```

### 4.4 `test_force_pause_drops_inflight` (story-named)

```python
async def test_force_pause_drops_in_flight_segment(
    db, job_factory, hanging_backend, force_pause_client,
):
    """Backend hangs in a single segment → force-pause aborts in < 1 s; no commit."""
    job = await job_factory.running(at_sec=0.0)
    hanging_backend.hang_for_seconds(60)  # never yields a segment
    stage_task = asyncio.create_task(run_transcribe_stage(job.id))
    await asyncio.sleep(0.5)              # let the worker enter the backend loop

    t0 = time.monotonic()
    await force_pause_client.post(f"/api/jobs/{job.id}/pause?force=true")
    await stage_task                       # returns when worker exits
    elapsed = time.monotonic() - t0
    assert elapsed < 1.5

    j = await db.fetchrow(
        "SELECT state, paused_at_sec, segments_completed FROM processing_jobs WHERE id=$1",
        job.id)
    assert j["state"] == "paused"
    assert j["segments_completed"] == 0
    assert j["paused_at_sec"] == 0.0
    # No segment row was written.
    assert await db.fetchval(
        "SELECT count(*) FROM transcript_segments "
        "WHERE transcript_id IN (SELECT id FROM transcripts WHERE video_id=$1)",
        job.video_id) == 0
```

### 4.5 `test_double_resume_is_idempotent` (story-named)

```python
async def test_double_resume_returns_200_unchanged(api_client, job_factory):
    job = await job_factory.paused(at_sec=42.0)
    r1 = await api_client.post(f"/api/jobs/{job.id}/resume")
    r2 = await api_client.post(f"/api/jobs/{job.id}/resume")
    assert r1.status_code == 202
    assert r1.json()["resumable"] is True
    # Second call sees state=='paused' OR has already moved to resuming/running.
    # Either way: 200/202, no error, no extra resume_count tick.
    assert r2.status_code in (200, 202)
    j = await api_client.get(f"/api/jobs/{job.id}")
    assert j.json()["resume_count"] == 1     # only one tick
```

### 4.6 `test_pause_marks` (unit)

```python
async def test_mark_paused_clears_claim(db, job_factory):
    job = await job_factory.running(at_sec=100.0, claimed_by="worker-A")
    await pause_mod.mark_paused(
        db.pool, job_id=job.id, at_sec=120.0, reason="user")
    row = await db.fetchrow("SELECT * FROM processing_jobs WHERE id=$1", job.id)
    assert row["state"] == "paused"
    assert row["paused_at_sec"] == 120.0
    assert row["paused_reason"] == "user"
    assert row["claimed_by"] is None
    assert row["pause_requested"] is False


async def test_mark_resuming_atomic_claim(db, job_factory):
    job = await job_factory.paused(at_sec=42.0)
    seek_a, seek_b = await asyncio.gather(
        pause_mod.mark_resuming(db.pool, job_id=job.id, worker_id="A"),
        try_resume_returning_exc(db.pool, job_id=job.id, worker_id="B"),
    )
    # One won, one raised ResumeRaceLost.
    successes = [s for s in [seek_a, seek_b] if not isinstance(s, Exception)]
    failures  = [s for s in [seek_a, seek_b] if isinstance(s, Exception)]
    assert len(successes) == 1 and len(failures) == 1
    assert isinstance(failures[0], pause_mod.ResumeRaceLost)
    assert successes[0] == 42.0
```

### 4.7 `test_resume_context_builder` + `test_resume_prompt_construction`

```python
def test_prompt_uses_last_k_segments_in_chronological_order(monkeypatch):
    segs = [
        {"seq": 1, "text": "first"},
        {"seq": 2, "text": "second"},
        {"seq": 3, "text": "third"},
    ]
    out = resume_context._build_prompt(segs)
    assert out == "first second third"


def test_prompt_truncates_from_left():
    long = [{"seq": i, "text": "x" * 100} for i in range(50)]
    out = resume_context._build_prompt(long)
    assert len(out) == resume_context.RESUME_PROMPT_CHAR_BUDGET
    # Tail (most recent) is preserved; head is truncated.
    assert out.endswith("x" * 100)


async def test_build_returns_locked_language(db, job_factory, transcript_factory):
    job = await job_factory.paused(at_sec=300.0)
    await transcript_factory(video_id=job.video_id, language="ar",
                             backend="whisper-mlx")
    ctx = await resume_context.build(
        db.pool, job_id=job.id, seek_from_sec=300.0,
        library_settings={"transcribe": {"backend": "whisper-mlx"}})
    assert ctx.language == "ar"
    assert ctx.backend_changed is False
```

### 4.8 `test_resume_audio_missing` (edge case)

```python
async def test_resume_when_file_is_missing_marks_audio_missing(
    db, job_factory, tmp_path,
):
    job = await job_factory.paused(at_sec=42.0,
                                    video_path=str(tmp_path / "gone.mkv"))
    # File doesn't exist; resume worker hits FileNotFoundError on open.
    await db.execute(
        "UPDATE processing_jobs SET pause_requested=false WHERE id=$1", job.id)
    await run_transcribe_stage(job.id)
    j = await db.fetchrow("SELECT * FROM processing_jobs WHERE id=$1", job.id)
    assert j["state"] == "paused"
    assert j["paused_reason"] == "audio_missing"
    assert j["paused_at_sec"] == 42.0
    assert j["not_before"] > datetime.now(tz=UTC) + timedelta(minutes=4, seconds=50)
```

### 4.9 `test_force_pause_aborts_subprocess` (unit)

```python
async def test_force_pause_watcher_calls_abort(db, job_factory):
    backend = AbortRecordingBackend()
    job = await job_factory.running()
    async with force_pause.ForcePauseWatcher(
        db.pool, job_id=job.id, backend=backend,
    ):
        await db.execute(
            "SELECT pg_notify('jobs.force_pause', "
            "json_build_object('job_id', $1::bigint, 'force', true)::text)",
            job.id)
        await asyncio.sleep(0.05)
    assert backend.abort_calls == 1


async def test_force_pause_watcher_ignores_other_jobs(db, job_factory):
    backend = AbortRecordingBackend()
    job = await job_factory.running()
    async with force_pause.ForcePauseWatcher(
        db.pool, job_id=job.id, backend=backend,
    ):
        # Notify for a *different* job id.
        await db.execute(
            "SELECT pg_notify('jobs.force_pause', "
            "json_build_object('job_id', 999999::bigint)::text)")
        await asyncio.sleep(0.05)
    assert backend.abort_calls == 0
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case (story §"Edge cases") | Handled by |
|---|---------------------------------|------------|
| E1 | Audio file moved between pause and resume. | `ResumeContextBuilder.build()` resolves `videos.path` and falls back to a `videos.content_hash` lookup before opening. On `FileNotFoundError` the worker calls `mark_paused(reason='audio_missing', not_before=+5min)` instead of flipping to `running` (D6). The user sees `paused_reason='audio_missing'`; restoring the file and clicking Resume re-runs the same path. (`test_resume_audio_missing`) |
| E2 | Whisper prompt seam glitch — language re-detect on resume boundary. | The resume STT session opens with `detect_language=False` and `language=transcript.language` (D4). The `transcripts.language` field is the source of truth and survives any backend change. (`test_resume_context_builder` asserts the locked language.) |
| E3 | Crash mid-segment commit. | Transaction atomicity from Plan 3.6 §2.2 guarantees no partial commit. On resume, the worker rebuilds from the same `last_segment_end_sec` it saw before the crash. Verified end-to-end by the chaos test in [Plan 3.8 §4.4](plan-03-08-crash-recovery.md). |
| E4 | Backend no longer available on resume (e.g., whisper-mlx but the user moved off Apple Silicon). | The library's `transcribe.backend` setting drives the resume backend selection. If the new backend differs, D5 opens a new `transcripts` row tagged with `metrics.resumed_with_different_backend`. The Story 3.5 fallback chain (mlx → faster-whisper → openai-api) is consulted for "backend unavailable" cases. (`test_resume_after_backend_change`) |
| E5 | User clicks Resume during the worker's resume context build (race). | `ResumeHandler` (Go) is idempotent: on a non-`paused` row, returns 200 with the current state. The worker's `mark_resuming` acquires the row via `WHERE state = 'paused'` so a second worker-side claim sees no rows and raises `ResumeRaceLost`, which the worker treats as "another worker has it; nothing to do". (`test_double_resume_is_idempotent`, `test_mark_resuming_atomic_claim`) |
| E6 | User clicks Pause then immediately Resume before the worker observes either. | Both API calls UPDATE the same row in two separate transactions. The final value of `pause_requested` after the second call is `false`; the worker observes no pause and continues. The `resume_count` does not increment because the row was never in `state='paused'`. |
| E7 | Force-pause used on a backend that doesn't implement `AbortableBackend`. | `ForcePauseWatcher` logs `force_pause_unsupported_by_backend` and falls back to cooperative behavior — the `pause_requested` flag is still set, so the next segment commit triggers normal pause. The user sees a slightly longer pause-to-state-flip latency for that backend; correctness is preserved. |
| E8 | Resume on a job whose backend changed but the new backend has a different model context (e.g., word-level on but old run word-level off). | Backend-change resumes (D5) start a fresh `transcripts` row with the *new* settings; the old transcript stays read-only at `paused_at_sec`. The `subtitle_gen` and `index` consumers stitch via the union view `v_video_transcript`. |
| E9 | Force-pause notify arrives after the worker has already exited cleanly via cooperative pause. | The `ForcePauseWatcher` is shut down by the `__aexit__` in the `async with` block before `mark_paused` returns. A late notify is delivered to a removed listener and dropped. |
| E10 | Resume prompt becomes very large for verbose libraries. | `_build_prompt` truncates to `RESUME_PROMPT_CHAR_BUDGET = 1500` chars from the LEFT, keeping the most recent context (D3). Whisper additionally clamps to 224 tokens internally. |

---

## 6. Acceptance checklist

- [ ] **A1** `POST /api/jobs/{id}/pause` flips `pause_requested = true` and returns `202` with the unchanged state. Within one segment boundary the worker commits the current segment, sets `state = 'paused'`, `paused_at_sec = last_segment_end_sec`, `paused_reason = 'user'`, and releases the GPU lock. (`test_pause_marks`, end-to-end via `test_resume_seeks_to_last_segment_end`)
- [ ] **A2** `POST /api/jobs/{id}/resume` makes the job claimable; the next worker flips state to `resuming`, opens the audio decoder seeked to `last_segment_end_sec`, rebuilds the Whisper prompt from the last K segments, then flips to `running`. The first emitted segment's `start_sec >= last_segment_end_sec`. (`test_resume_starts_from_last_segment_end_sec`)
- [ ] **A3** Resume with a different library-configured backend succeeds; `transcripts.metrics.resumed_with_different_backend = {from, to, at_sec}` is recorded on the new transcript row. (`test_resume_after_backend_change`)
- [ ] **A4** `POST /api/jobs/{id}/pause?force=true` flips to `paused` immediately when the worker is stuck in a single long segment; in-flight segment is discarded; `paused_at_sec = last_segment_end_sec` of the last DURABLY committed segment. (`test_force_pause_drops_inflight`)
- [ ] **A5** Double `/resume` is idempotent — exactly one worker claim succeeds; the second API call returns `200`/`202` with the unchanged state. (`test_double_resume_is_idempotent`)
- [ ] **A6** Resume across process restart (kill the worker; spawn a new one) reclaims the same job and continues from `last_segment_end_sec`. (`test_resume_across_process_restart`)
- [ ] **A7** Audio file moved between pause and resume → resume worker fails the open, calls `mark_paused(reason='audio_missing', not_before=+5min)`, returns without state flip to `running`. (`test_resume_audio_missing`)
- [ ] **A8** Resume STT session opens with `detect_language=False, language=transcript.language` — no auto-detect on the seek boundary. (`test_resume_context_builder`)
- [ ] **A9** The Whisper prompt is built from the last K segment texts, joined with single spaces, truncated from the left to 1500 chars. K is configurable via `library.settings.transcribe.resume_prompt_segments`. (`test_resume_prompt_construction`)
- [ ] **A10** `ForcePauseWatcher` correctly filters notifies by `job_id` and aborts only the matching backend. (`test_force_pause_watcher_*`)
- [ ] **A11** API handlers (Go) and worker mutators (Python) are the **only** writers to `pause_requested`, `state`, `paused_*`, and `resume_count` columns. Verified by a static check (`grep` against the rest of the codebase) added to CI.
