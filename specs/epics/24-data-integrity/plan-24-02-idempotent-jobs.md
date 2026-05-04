# Implementation Plan — Story 24.2 Idempotent and resumable jobs

> Companion to [story-24-02-idempotent-jobs.md](story-24-02-idempotent-jobs.md).
> Story states *what* and *why*; this plan states *how*.
> Builds on the job state machine in
> [architecture.md §7](../../architecture.md) and the segment commit
> protocol in §7.6. Sidecar regeneration uses the helpers from
> [Story 24.1](plan-24-01-atomic-writes.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Idempotency key | `(content_hash, stage, backend, model, config_hash)` stored on `processing_jobs`. Re-claim with the same key short-circuits the work. |
| Resume protocol | Per-segment commit in own DB tx with `last_segment_end_sec`. STT engines accept a start offset. |
| Sidecar projection | DB is the source of truth; sidecars regenerated from segments rows. A `--rebuild-sidecars` flag exists. |
| Bulk re-process | `maktaba-pipeline reprocess --from-stage <name>` walks the DAG. |
| Out of scope | Atomic writes (Story 24.1); concurrency/locking (Story 24.4); job state machine itself (architecture §7). |

## 1. Architecture diagram

```
   claim ──► load processing_jobs.last_segment_end_sec
              │
              ▼
   STT engine.transcribe(audio, start=last_segment_end_sec)
              │ yields per-segment events
              ▼
   tx{
     INSERT segments(job_id, segment_idx, start, end, text, ...)
     UPDATE processing_jobs SET last_segment_end_sec=end, heartbeat=now()
   }
              │
              ▼ on completion
   regenerate sidecar from segments rows (atomic_write)
   set processing_jobs.state=DONE
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/idempotency.py` | `compute_key(stage, video, backend, model, config) -> str`. |
| `pipeline/src/maktaba_pipeline/pipeline/resume.py` | Glue for the per-segment commit + heartbeat. |
| `pipeline/src/maktaba_pipeline/pipeline/sidecars.py` | `regenerate(video_id, stage)` — DB → atomic_write. |
| `pipeline/src/maktaba_pipeline/cli/reprocess.py` | `reprocess --from-stage`. |
| `shared/db/queries/processing_jobs_resume.sql` | Resume-specific queries. |
| Tests — `tests/integration/resume_*.py`, `tests/integration/idempotent_claim_*.py`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | Loads idempotency key on claim; short-circuits if a DONE job for the same key exists. |
| `pipeline/src/maktaba_pipeline/stt/*.py` | Each backend accepts `start_at_sec` and a `yield_segment` callback. |
| `pipeline/src/maktaba_pipeline/subtitles/*.py` | Read from segments table, not from in-memory STT output. |

### 2.3 Idempotency key

`idempotency.py`:

```python
import hashlib
import json
from typing import Any

def compute_config_hash(stage: str, config: dict[str, Any]) -> str:
    """Stable hash of config that affects output for a given stage."""
    relevant = _relevant_keys(stage, config)
    s = json.dumps(relevant, sort_keys=True, separators=(",", ":"))
    return hashlib.blake2b(s.encode(), digest_size=12).hexdigest()

def compute_key(stage: str, content_hash: str, backend: str,
                model: str, config: dict[str, Any]) -> str:
    parts = (stage, content_hash, backend, model, compute_config_hash(stage, config))
    return ":".join(parts)

def _relevant_keys(stage: str, config: dict[str, Any]) -> dict[str, Any]:
    """Filter to only those that change the output bytes."""
    if stage == "transcribe":
        return {k: config.get(k) for k in ("language", "vad", "beam_size", "temperature")}
    if stage == "embed":
        return {k: config.get(k) for k in ("dim", "normalize", "model_revision")}
    return config
```

The idempotency key is stored on `processing_jobs.idempotency_key` (a
new column in the next migration on top of architecture §8).

### 2.4 Resume protocol

`resume.py`:

```python
from contextlib import asynccontextmanager

@asynccontextmanager
async def per_segment_commit(job_id, db):
    """Yield a callable that commits one segment + heartbeat in own tx.

    Usage:
        async with per_segment_commit(job_id, db) as commit:
            for seg in stt.iter():
                await commit(seg)
    """
    async def commit(seg):
        async with db.transaction():
            await db.execute(
                """INSERT INTO segments(job_id, video_id, segment_idx,
                                        start_sec, end_sec, text)
                   VALUES ($1,$2,$3,$4,$5,$6)
                   ON CONFLICT (job_id, segment_idx) DO NOTHING""",
                job_id, seg.video_id, seg.idx, seg.start, seg.end, seg.text)
            await db.execute(
                """UPDATE processing_jobs
                   SET last_segment_end_sec = GREATEST(last_segment_end_sec, $2),
                       heartbeat_at = now()
                   WHERE id = $1""",
                job_id, seg.end)
    yield commit
```

The `ON CONFLICT DO NOTHING` makes recommit safe at the segment-commit
crash boundary (EC3).

### 2.5 STT backend resume hook

Each backend gains a `start_at_sec` argument; behavior is documented:

```python
class STTBackend(Protocol):
    async def transcribe(self, audio: Path, *, start_at_sec: float = 0.0,
                          ) -> AsyncIterator[Segment]: ...
```

`whisper_mlx` and `faster_whisper` accept the offset by seeking the
audio file and resuming. `openai_api` doesn't natively support resume
inside a single transcription call; the pipeline splits the audio at
the offset, transcribes only the remainder, and adjusts segment
timestamps before emitting.

### 2.6 Runner short-circuit

`runner.py` (relevant logic):

```python
async def run_job(job, db, stage_impls):
    key = idempotency.compute_key(job.stage, job.content_hash,
                                  job.backend, job.model, job.config)
    if existing := await db.fetch_one(
        "SELECT id, state FROM processing_jobs "
        "WHERE idempotency_key = $1 AND state = 'DONE' "
        "AND id != $2 LIMIT 1", key, job.id):
        # Mirror the existing result onto this job so callers see the
        # same outcome shape. Then mark the new one DONE without doing
        # work. (Bulk re-enqueues become cheap.)
        await db.execute(
            "UPDATE processing_jobs SET state='DONE', "
            "last_segment_end_sec=src.last_segment_end_sec "
            "FROM (SELECT * FROM processing_jobs WHERE id=$1) AS src "
            "WHERE processing_jobs.id=$2", existing["id"], job.id)
        return

    impl = stage_impls[job.stage]
    await impl.run(job)
```

### 2.7 Sidecar regeneration

`sidecars.py`:

```python
async def regenerate(video_id: str, stage: str, db) -> None:
    """Rebuild on-disk sidecars for `video_id` from DB rows."""
    if stage == "subtitle_gen":
        rows = await db.fetch_all(
            "SELECT segment_idx, start_sec, end_sec, text FROM segments "
            "WHERE video_id=$1 ORDER BY segment_idx", video_id)
        vtt_path = await _vtt_path(video_id, db)
        atomic_write_bytes(vtt_path, render_vtt(rows))
        srt_path = await _srt_path(video_id, db)
        atomic_write_bytes(srt_path, render_srt(rows))
    elif stage == "thumbnails":
        # ...
    elif stage == "sprites":
        # ...
```

Cue text is sanitized via Story 23.5's helper before write.

### 2.8 Reprocess CLI

`cli/reprocess.py`:

```python
class Stage(str, Enum):
    SCAN = "scan"
    PROBE = "probe"
    EXTRACT = "extract_audio"
    TRANSCRIBE = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"
    INDEX = "index"
    THUMBNAIL = "thumbnail"

DAG_ORDER = [Stage.SCAN, Stage.PROBE, Stage.EXTRACT, Stage.TRANSCRIBE,
             Stage.SUBTITLE_GEN, Stage.INDEX, Stage.THUMBNAIL]

async def main(args):
    start = Stage(args.from_stage)
    downstream = DAG_ORDER[DAG_ORDER.index(start):]
    async with db_conn() as db:
        for stage in downstream:
            for video_id in await videos_for_library(db, args.library):
                await db.execute(
                    "INSERT INTO processing_jobs(video_id, stage, state) "
                    "VALUES ($1, $2, 'QUEUED') ON CONFLICT DO NOTHING",
                    video_id, stage.value)
```

Upstream stages are not re-enqueued; their outputs (e.g., probe rows)
remain.

## 3. Test plan

### 3.1 Resume from segment N (TC1)

| Test | What it pins |
|---|---|
| `TestResumeAt30MinOf60Min` | Run transcribe on a 60-min fixture; SIGKILL at 30 min wall-clock; restart; total wall-clock ~30 min more (not 60 min). The output's segment text similarity ≥ 95 % vs. a clean run reference. |
| `TestResumeWithLastSegmentEndSec` | Mock STT to fail after segment 100; restart; the restart sees `last_segment_end_sec` and passes it as `start_at_sec`. |
| `TestResumeOpenaiBackendSplitsAudio` | The OpenAI backend's resume splits the audio, transcribes the remainder, adjusts segment timestamps. |

### 3.2 Idempotent claim (TC2)

| Test | What it pins |
|---|---|
| `TestSecondClaimNoOps` | Enqueue the same `(content_hash, transcribe, whisper_mlx, large-v3, cfg-hash-X)` twice; only the first runs the work; the second is short-circuited to DONE with the same artifact. |
| `TestKeyChangesOnConfigBump` | A config change to `language` produces a different `idempotency_key`; both jobs run independently. |

### 3.3 Sidecar rebuild (TC3)

| Test | What it pins |
|---|---|
| `TestRebuildVttFromSegments` | Delete `.maktaba/<video>/transcript.vtt`; run `reprocess --from-stage subtitle_gen`; the file is regenerated; bytes match a reference (modulo cue ordering invariants). |
| `TestRebuildSpritesFromSegments` | Same flow for sprite sheets; the regenerated sprite hash matches a reference. |
| `TestRebuildLeavesUnreferencedFilesAlone` | Other files in the `.maktaba/` dir are untouched. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| STT non-determinism (EC1) | Boundaries differ across runs; the resume test asserts text similarity ≥ 95 %, not byte-equality. | `TestResumeTextSimilarity95` |
| Backend changed mid-job (EC2) | The new claim's `idempotency_key` differs (config_hash differs); the runner re-runs from start; segments rows from the previous backend are kept under the previous job_id. | `TestBackendChangeStartsFresh` |
| Crash at segment-commit boundary (EC3) | The `ON CONFLICT (job_id, segment_idx) DO NOTHING` makes the re-attempt a no-op. The next worker's tx commits successfully. | `TestSegmentCommitCrashIdempotent` |
| Resumed STT yields fewer segments | If the engine ends earlier on a resume than expected, the sidecar regen still produces a file from the available rows; the DAG marks the job DONE; downstream stages handle a shorter transcript. | `TestShorterResumeStillCompletes` |
| `last_segment_end_sec` is NULL (first run) | The runner passes `start_at_sec=0`. | `TestFirstRunStartFromZero` |
| Resume but the audio file was deleted | The runner fails fast with `audio_missing`; the job is marked FAILED with a documented error category. | `TestAudioMissingMarksFailed` |
| Two workers race on the same job | Job claim uses `SELECT ... FOR UPDATE SKIP LOCKED` (Story 24.4); only one runs. | n/a (24.4) |
| Bulk reprocess overlaps with running job | Insert with `ON CONFLICT DO NOTHING` skips a duplicate enqueue; the running job continues; downstream re-enqueue waits for it. | `TestBulkReprocessConcurrentSkip` |
| `config_hash` excludes non-output-affecting keys | `_relevant_keys` is the source of truth; a CI fixture enumerates every config key per stage and asserts each is either listed (relevant) or annotated (irrelevant). | `TestConfigHashCoverage` |
| Sidecar regen during active read | Atomic-write helper (24.1) ensures readers see old or new, never partial. | `TestRegenAtomicVsReader` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `asyncpg` | already | DB tx for per-segment commit. |
| `hashlib` | stdlib | Idempotency key. |

## 6. Acceptance checklist

**Idempotency**
- [ ] `compute_key` defined; stored on `processing_jobs.idempotency_key`.
- [ ] Runner short-circuits on a DONE peer for the same key.

**Resume**
- [ ] Per-segment commit in own tx with `last_segment_end_sec`.
- [ ] STT backends accept `start_at_sec`.
- [ ] `(job_id, segment_idx)` unique constraint provides crash idempotency.

**Projection**
- [ ] Sidecar regeneration reads from `segments` rows, not in-memory state.
- [ ] `reprocess --from-stage` walks the DAG forward.
- [ ] Atomic-write helpers (24.1) used for all sidecar writes.

**Tests**
- [ ] All §3 tests pass.
