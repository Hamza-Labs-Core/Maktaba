# Implementation Plan — Story 19.4 Pipeline Horizontal Scale-Out

> Companion to [story-19-04-pipeline-scale-out.md](story-19-04-pipeline-scale-out.md).
> N workers across hosts coordinate via Postgres (`SKIP LOCKED`); GPU jobs take
> per-device advisory locks; ChromaDB single-writer rule enforced.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Coordination | Postgres `SELECT ... FOR UPDATE SKIP LOCKED`. |
| GPU lock | `pg_advisory_xact_lock(hashtext($host || ':' || $device))`. |
| ChromaDB writer | First worker grabs `pg_advisory_lock(hashtext('chroma:writer'))` at startup; held forever. Second worker gets refused, logs, falls back to read-only. |
| Heartbeat | `processing_jobs.heartbeat_at` updated every 30 s; reaper requeues jobs with stale heartbeat. |
| Out of scope | Embedded vs. server ChromaDB (Epic 24); model selection (Epic 3). |

## 1. Project layout

```
pipeline/maktaba_pipeline/
├── coordinator/
│   ├── claim.py                 # SKIP LOCKED claim
│   ├── heartbeat.py             # update heartbeat_at
│   ├── reaper.py                # reclaim stale jobs
│   ├── gpu_lock.py
│   ├── chroma_writer_lock.py
│   ├── version_check.py
│   └── tests/
└── ...

shared/db/migrations/
└── 00xx_pipeline_scale_out.sql
```

## 2. Schema additions

```sql
-- 00xx_pipeline_scale_out.sql
ALTER TABLE processing_jobs ADD COLUMN worker_id      TEXT;
ALTER TABLE processing_jobs ADD COLUMN heartbeat_at   TIMESTAMPTZ;
ALTER TABLE processing_jobs ADD COLUMN attempt        INT NOT NULL DEFAULT 0;
ALTER TABLE processing_jobs ADD COLUMN backend        TEXT;
ALTER TABLE processing_jobs ADD COLUMN model_hash     TEXT;
ALTER TABLE processing_jobs ADD COLUMN last_segment_end_sec FLOAT;

CREATE INDEX processing_jobs_state_heartbeat_idx
  ON processing_jobs (state, heartbeat_at)
  WHERE state = 'running';
```

## 3. Claim with SKIP LOCKED

```python
# coordinator/claim.py
CLAIM_SQL = """
WITH next AS (
  SELECT id FROM processing_jobs
   WHERE state = 'pending' AND scheduled_at <= now()
   ORDER BY priority DESC, scheduled_at
   FOR UPDATE SKIP LOCKED
   LIMIT 1
)
UPDATE processing_jobs j
   SET state = 'running',
       started_at = now(),
       heartbeat_at = now(),
       worker_id = %(worker_id)s,
       attempt = j.attempt + 1
  FROM next
 WHERE j.id = next.id
RETURNING j.*;
"""

def claim(conn, worker_id: str) -> Job | None:
    with conn.cursor(row_factory=dict_row) as cur:
        cur.execute(CLAIM_SQL, {"worker_id": worker_id})
        row = cur.fetchone()
    return Job(**row) if row else None
```

## 4. Heartbeat

```python
# coordinator/heartbeat.py
class Heartbeat:
    def __init__(self, conn_factory, job_id: str, interval_s: int = 30):
        self._stop = threading.Event()
        self._t = threading.Thread(target=self._loop, daemon=True,
                                   args=(conn_factory, job_id, interval_s))
        self._t.start()

    def _loop(self, conn_factory, job_id, interval_s):
        while not self._stop.wait(interval_s):
            with conn_factory() as c, c.cursor() as cur:
                cur.execute("UPDATE processing_jobs SET heartbeat_at = now() WHERE id = %s", (job_id,))

    def stop(self): self._stop.set(); self._t.join()
```

## 5. Reaper

```python
# coordinator/reaper.py
REAPER_SQL = """
UPDATE processing_jobs
   SET state = 'pending', worker_id = NULL,
       heartbeat_at = NULL, started_at = NULL
 WHERE state = 'running'
   AND heartbeat_at < now() - (%(timeout_s)s || ' seconds')::interval
RETURNING id;
"""

async def reaper_loop(timeout_s: int = 90):
    while True:
        async with pool.connection() as c, c.cursor() as cur:
            await cur.execute(REAPER_SQL, {"timeout_s": timeout_s})
            ids = [r[0] for r in await cur.fetchall()]
            if ids: log.info("reaped %d stale jobs", len(ids))
        await asyncio.sleep(15)
```

`timeout_s` = `heartbeat_interval × 3` per the story.

## 6. GPU device advisory lock

```python
# coordinator/gpu_lock.py
def gpu_lock_key(host_id: str, device_id: int) -> int:
    s = f"gpu:{host_id}:{device_id}"
    # Postgres pg_advisory_xact_lock takes a 64-bit BIGINT.
    return int.from_bytes(hashlib.blake2b(s.encode(), digest_size=8).digest(), "big", signed=True)

@contextmanager
def gpu_lock(conn, host_id: str, device_id: int):
    key = gpu_lock_key(host_id, device_id)
    with conn.cursor() as cur:
        cur.execute("SELECT pg_advisory_xact_lock(%s)", (key,))
        try: yield
        finally: pass  # released at txn end
```

Usage:

```python
with conn.transaction(), gpu_lock(conn, host_id, device_id):
    transcribe_with_gpu(job)
```

Two GPU jobs for the same `(host, device)` serialize because the lock is held for the whole transcribe transaction.

## 7. ChromaDB single-writer guard

```python
# coordinator/chroma_writer_lock.py
WRITER_KEY = int.from_bytes(hashlib.blake2b(b"chroma:writer", digest_size=8).digest(), "big", signed=True)

class ChromaWriter:
    def __init__(self, conn_factory):
        self._conn = conn_factory()
        with self._conn.cursor() as cur:
            cur.execute("SELECT pg_try_advisory_lock(%s)", (WRITER_KEY,))
            self.is_writer = cur.fetchone()[0]
        if not self.is_writer:
            log.warning("chroma writer lock held by peer; running read-only")
```

A peer writer blocks the second worker from `chroma.upsert(...)`. The peer exits the writer role; any indexing job claimed by the non-writer is requeued (`pending`) so the writer picks it up.

```python
# In the index stage:
if not chroma_writer.is_writer:
    requeue(job, reason="chroma_writer_held_by_peer")
    return
```

## 8. Worker version check (EC3)

```python
# coordinator/version_check.py
def assert_compatible(job: Job, my: WorkerCapabilities) -> None:
    if job.backend and job.backend != my.backend:
        raise IncompatibleJob(f"job wants backend={job.backend}, worker has {my.backend}")
    if job.model_hash and job.model_hash != my.model_hash:
        raise IncompatibleJob(f"job wants model_hash={job.model_hash}, worker has {my.model_hash}")
```

`IncompatibleJob` → caller marks the job `state='retry'` with reason; a human-readable retry pile (admin UI shows reason).

## 9. Resume-from-`last_segment_end_sec` (EC1)

```python
def transcribe_resumable(audio_path: Path, job: Job) -> None:
    start = job.last_segment_end_sec or 0.0
    for seg in transcriber.stream(audio_path, start=start):
        write_segment(seg)
        update_job_progress(job.id, last_segment_end_sec=seg.end)
```

A reaped job's next attempt resumes from the last persisted segment.

## 10. NFS read retry (EC2)

```python
# common/io.py
def read_with_retry(path: Path, retries: int = 3, base_delay_s: float = 1.0) -> bytes:
    for i in range(retries):
        try: return path.read_bytes()
        except OSError as e:
            if i == retries - 1: raise
            time.sleep(base_delay_s * (2 ** i))
```

## 11. Test cases

### TC1 — Two-host drain
Compose stack with 2 pipeline workers (separate containers). Enqueue 30 transcribe jobs (60 min audio total). Single-host baseline measured first. Two-host: assert wall-clock ∈ [T/2 - 10 %, T/2 + 10 %].

### TC2 — Exactly-once
4 workers, 1,000 small jobs (1 s each). After drain: `SELECT COUNT(DISTINCT (job_id, output_hash)) FROM job_outputs` == 1,000.

### TC3 — GPU lock contention
Pin two transcribe jobs to same `(host_id, device_id=0)` via test override. Run with 2 workers. Sum of per-job durations ≈ wall-clock (sequential), not half (parallel).

### TC4 — Single-writer guard
Launch worker A then B against same embedded ChromaDB. Assert: B's startup log contains `chroma writer lock held by peer; running read-only`. Enqueue 10 index jobs. Confirm: A picks up all 10; B's `chroma_upsert_total` == 0; A's == 10.

### EC1 — Heartbeat reap
Start worker A on a job; SIGKILL after first heartbeat. Wait `heartbeat_interval × 3 + buffer`. Worker B claims the same job; resumes from `last_segment_end_sec`.

### EC2 — NFS hiccup
Mount an NFS export with `intr,timeo=10`; firewall the export mid-job. Job retries 3 times, then requeues; on next claim succeeds.

### EC3 — Version mismatch
Enqueue a job with `backend='whisper-mlx', model_hash='X'`. Run worker reporting `model_hash='Y'`. Assert: claim succeeds → version check fails → job → `state='retry'` with `reason='incompatible_model_hash:wanted_X_have_Y'`.

## 12. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 worker dies | story | Heartbeat reaper requeues; resume from segment offset. |
| EC2 NFS hiccup | story | Backoff + retry + requeue. |
| EC3 version mismatch | story | Per-job `(backend, model_hash)` validated; retry pile. |
| Same job claimed twice | impl | Impossible: `SKIP LOCKED` + `state='running'` UPDATE — second claim sees `state='running'`. |
| Advisory lock leaked across crash | impl | Session-scoped; closing the conn releases. `pg_try_advisory_xact_lock` is txn-scoped. |

## 13. Configuration

```yaml
pipeline:
  worker_id: ${HOSTNAME}-${POD_NAME}
  heartbeat_interval_s: 30
  reaper_interval_s: 15
  reap_after_s: 90                # heartbeat_interval × 3
  concurrency:
    transcribe: 1                  # per GPU
    index: 4
  gpu_devices: auto                # detected from `nvidia-smi -L` / MLX
  chroma_writer_required: true     # log+exit if cannot acquire and no peer detected within 10s
```

## 14. Dependencies

- Epic 6 job-queue (claim path).
- Epic 24 data-integrity (single-writer rule, Story 24.4).
- Story 19.5 (Postgres scaling).
- Story 21.7 (job-pipeline visibility) for surfacing reaped/stuck jobs.
