# Implementation Plan — Story 19.4 Pipeline Horizontal Scale-Out

> Companion to [story-19-04-pipeline-scale-out.md](story-19-04-pipeline-scale-out.md).
> N workers across hosts coordinate via Postgres (`SKIP LOCKED`); GPU jobs take
> per-device advisory locks; ChromaDB single-writer rule enforced.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Coordination | Postgres `SELECT ... FOR UPDATE SKIP LOCKED`. |
| GPU lock | `pg_advisory_xact_lock(hashtext($host || ':' || $device))`. |
| ChromaDB writer | First worker grabs `pg_advisory_lock(hashtext('chroma:writer'))` at startup; held until shutdown. See §7 for advisory-lock keying. Plan-introduced lock namespace; cross-link Story 24.4. |
| Heartbeat | `processing_jobs.last_heartbeat_at` updated every 5 s (arch §7.1); reaper requeues jobs with stale heartbeat. |
| Claim columns | Canonical names: `claimed_by`, `claimed_at`, `last_heartbeat_at`, `attempts`, `not_before`, `priority` (lower wins), `last_segment_end_sec` (arch §7.1, §7.5). |
| Out of scope | Embedded vs. server ChromaDB (Epic 24); model selection (Epic 3). |

## 1. Project layout

```
pipeline/maktaba_pipeline/
├── coordinator/
│   ├── claim.py                 # SKIP LOCKED claim
│   ├── heartbeat.py             # update last_heartbeat_at
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

> **Canonical column names.** Architecture §7.1/§7.5 owns the `processing_jobs`
> claim-column shape: `claimed_by`, `claimed_at`, `last_heartbeat_at`,
> `attempts`, `not_before`, `priority`, `last_segment_end_sec`. This plan does
> NOT introduce parallel `worker_id` / `heartbeat_at` / `attempt` columns —
> all earlier drafts are removed. Per-job `(backend, model_hash)` are owned
> by Story 24.x (model versioning) and are not added here; the version
> check in §8 keeps them in `payload` JSON for now.

```sql
-- 00xx_pipeline_scale_out.sql
-- Index supporting the reaper scan (§5) over the canonical heartbeat column.
CREATE INDEX IF NOT EXISTS processing_jobs_state_heartbeat_idx
  ON processing_jobs (state, last_heartbeat_at)
  WHERE state = 'running';

-- Index supporting the SKIP LOCKED claim path (§3) ordering by priority/not_before.
CREATE INDEX IF NOT EXISTS processing_jobs_claim_idx
  ON processing_jobs (state, priority, not_before)
  WHERE state = 'pending';
```

(`claimed_by`, `claimed_at`, `last_heartbeat_at`, `attempts`, `not_before`,
`priority`, `last_segment_end_sec` are declared by the canonical
`processing_jobs` migration in plan-22-04 / arch §7.1; this plan only adds
the indexes it needs.)

## 3. Claim with SKIP LOCKED

```python
# coordinator/claim.py
# Canonical (arch §7.5):
#   - filter:   state='pending' AND not_before <= now()
#   - ordering: ORDER BY priority ASC (LOWER WINS), not_before ASC
#   - update:   set state='running', claimed_by, claimed_at, last_heartbeat_at,
#               attempts := attempts + 1
CLAIM_SQL = """
WITH next AS (
  SELECT id FROM processing_jobs
   WHERE state = 'pending' AND not_before <= now()
   ORDER BY priority ASC, not_before ASC
   FOR UPDATE SKIP LOCKED
   LIMIT 1
)
UPDATE processing_jobs j
   SET state = 'running',
       claimed_by = %(worker_id)s,
       claimed_at = now(),
       last_heartbeat_at = now(),
       attempts = j.attempts + 1
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

Cadence is **5 s** per architecture §7.1 (was 30 s in earlier drafts; corrected).
The reaper timeout is `heartbeat_interval × 3` ≈ 15 s (see §5).

```python
# coordinator/heartbeat.py
class Heartbeat:
    def __init__(self, conn_factory, job_id: str, interval_s: int = 5):
        self._stop = threading.Event()
        self._t = threading.Thread(target=self._loop, daemon=True,
                                   args=(conn_factory, job_id, interval_s))
        self._t.start()

    def _loop(self, conn_factory, job_id, interval_s):
        while not self._stop.wait(interval_s):
            with conn_factory() as c, c.cursor() as cur:
                cur.execute(
                    "UPDATE processing_jobs SET last_heartbeat_at = now() WHERE id = %s",
                    (job_id,))

    def stop(self): self._stop.set(); self._t.join()
```

## 5. Reaper

The reaper preserves `paused` / `pause_requested` state (arch §7.9):

- If a stuck `running` row has `pause_requested = true`, transition to
  `paused` and clear `claimed_by`. Do **not** bump `attempts` (the worker
  was paused, not failed).
- Otherwise transition to `pending`, bump `attempts`, set
  `not_before = now() + backoff(attempts)` and clear claim columns.

```python
# coordinator/reaper.py

# Stuck-running rows that the operator/worker asked to pause: move to paused
# and release the claim, leaving attempts untouched.
REAPER_PAUSE_SQL = """
UPDATE processing_jobs
   SET state = 'paused',
       claimed_by = NULL,
       claimed_at = NULL,
       last_heartbeat_at = NULL
 WHERE state = 'running'
   AND pause_requested = true
   AND last_heartbeat_at < now() - (%(timeout_s)s || ' seconds')::interval
RETURNING id;
"""

# Stuck-running rows with no pause request: requeue to pending, bump attempts,
# schedule via not_before with exponential backoff.
REAPER_REQUEUE_SQL = """
UPDATE processing_jobs
   SET state = 'pending',
       claimed_by = NULL,
       claimed_at = NULL,
       last_heartbeat_at = NULL,
       attempts = attempts + 1,
       not_before = now() + (LEAST(60, GREATEST(1, attempts + 1)) * interval '1 second')
 WHERE state = 'running'
   AND (pause_requested IS NULL OR pause_requested = false)
   AND last_heartbeat_at < now() - (%(timeout_s)s || ' seconds')::interval
RETURNING id;
"""

async def reaper_loop(timeout_s: int = 15):
    """timeout_s = heartbeat_interval × 3 = 5 × 3 = 15 s (arch §7.1)."""
    while True:
        async with pool.connection() as c, c.cursor() as cur:
            await cur.execute(REAPER_PAUSE_SQL, {"timeout_s": timeout_s})
            paused_ids = [r[0] for r in await cur.fetchall()]
            await cur.execute(REAPER_REQUEUE_SQL, {"timeout_s": timeout_s})
            requeued_ids = [r[0] for r in await cur.fetchall()]
            if paused_ids:   log.info("reaper paused %d stuck rows", len(paused_ids))
            if requeued_ids: log.info("reaper requeued %d stuck rows", len(requeued_ids))
        await asyncio.sleep(5)
```

`timeout_s` = `heartbeat_interval × 3` = 15 s (5 s cadence × 3, arch §7.1).

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

> **Plan-introduced advisory-lock namespace.** The literal key
> `pg_advisory_lock(hashtext('chroma:writer'))` is owned by this plan and
> is the canonical key for the ChromaDB single-writer rule (cross-link
> Story 24.4). The GPU lock in §6 hashes `gpu:{host}:{device}` via
> `blake2b → BIGINT` and uses the txn-scoped variant; the chroma-writer
> lock is session-scoped (held for the worker process lifetime) and uses
> a separate, fixed key. Both keys live in the BIGINT advisory-lock space
> and must not collide; record both in the advisory-lock namespace
> registry referenced by arch §10.3.

```python
# coordinator/chroma_writer_lock.py
# Canonical chroma-writer key (plan-introduced; cross-link Story 24.4):
#   pg_advisory_lock(hashtext('chroma:writer'))
# We use blake2b → BIGINT below for stable hashing across PG versions; the
# hashtext('chroma:writer') form is the equivalent operator-friendly spelling.
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

`backend` and `model_hash` are read from the job's `payload` JSON (canonical
`processing_jobs` shape, arch §7.1) — they are NOT separate columns on this
plan. Story 24.x owns the durable model-version columns if/when they are
hoisted out of the payload.

```python
# coordinator/version_check.py
def assert_compatible(job: Job, my: WorkerCapabilities) -> None:
    backend    = job.payload.get("backend")
    model_hash = job.payload.get("model_hash")
    if backend and backend != my.backend:
        raise IncompatibleJob(f"job wants backend={backend}, worker has {my.backend}")
    if model_hash and model_hash != my.model_hash:
        raise IncompatibleJob(f"job wants model_hash={model_hash}, worker has {my.model_hash}")
```

`IncompatibleJob` → caller releases the claim back to `pending` (or `retry`
per Story 6.x state taxonomy) with a payload-recorded reason; a
human-readable retry pile (admin UI shows reason).

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
Start worker A on a job; SIGKILL after first heartbeat. Wait
`heartbeat_interval × 3 + buffer` (15 s + buffer). Worker B claims the same
job; resumes from `last_segment_end_sec`. Assert `attempts` was incremented
by 1 and `not_before` advanced.

### EC1b — Pause-preserving reap
Start worker A on a job; set `pause_requested = true` on the row, then SIGKILL
the worker. Wait the reaper window. Assert: row state is `paused` (not
`pending`), `attempts` is unchanged, `claimed_by` is NULL. The job stays
out of the claim queue until `pause_requested` is cleared (Story 6.x pause
semantics).

### EC2 — NFS hiccup
Mount an NFS export with `intr,timeo=10`; firewall the export mid-job. Job retries 3 times, then requeues; on next claim succeeds.

### EC3 — Version mismatch
Enqueue a job with `payload.backend='whisper-mlx', payload.model_hash='X'`. Run worker reporting `model_hash='Y'`. Assert: claim succeeds → version check fails → job is released with a `reason='incompatible_model_hash:wanted_X_have_Y'` recorded in the payload (per Story 6.x retry semantics).

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
  heartbeat_interval_s: 5             # arch §7.1
  reaper_interval_s: 5
  reap_after_s: 15                    # heartbeat_interval × 3
  # Default concurrency map per arch §11.4. Transcribe is GPU-bound and
  # gated by the per-device advisory lock (§6); the rest are CPU/IO bound.
  concurrency:
    scan: 4
    probe: 4
    extract: 2
    transcribe: 1                     # per GPU
    subtitle_gen: 2
    index: 4
    thumbnail: 2
  gpu_devices: auto                   # detected from `nvidia-smi -L` / MLX
  chroma_writer_required: true        # log+exit if cannot acquire and no peer detected within 10s
```

## 14. Dependencies

- Epic 6 job-queue (claim path).
- Epic 24 data-integrity (single-writer rule, Story 24.4).
- Story 19.5 (Postgres scaling).
- Story 21.7 (job-pipeline visibility) for surfacing reaped/stuck jobs.
