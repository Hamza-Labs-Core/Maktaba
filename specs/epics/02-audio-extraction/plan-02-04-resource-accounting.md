# Plan 02-04 — Audio extraction resource accounting

> **Note on scope.** This plan implements the user-facing spec in
> [story-02-04-resource-accounting.md](story-02-04-resource-accounting.md):
> a per-process concurrency cap on the `extract` stage, priority-aware
> claim ordering, optional CPU-pressure throttling, and bookkeeping for
> the temporary WAVs introduced in
> [story-02-03-stream-extraction.md](story-02-03-stream-extraction.md).
>
> The mechanism reuses primitives standardised in
> [Epic 6 Story 6.2](../06-job-queue/story-06-02-claim-loop.md) (claim
> loop), [Story 6.6](../06-job-queue/story-06-06-reaper.md) (reaper),
> and [Story 6.7](../06-job-queue/story-06-07-concurrency-caps.md)
> (per-stage semaphore). This plan does not re-derive those — it spells
> out only what is specific to `extract`:
>
> 1. The default cap of `2` and the disk-bound rationale
>    (architecture §7.4).
> 2. Coupling with the streaming service so the Pipeline knows when the
>    box is already busy transcoding (architecture §10.3 single-host
>    contention).
> 3. Lifecycle hooks that delete temp WAVs the moment a job leaves
>    `running` for any terminal-or-paused state.
>
> **Languages.** The extract stage lives in Python
> (`pipeline/src/maktaba_pipeline/pipeline/stages/extract.py`); the
> streaming pressure counter lives in Go
> (`streaming/internal/transcode/pressure`). Both are described below.

---

## 1. Architecture diagram — claim → run → release

```
                     ┌────────────────────────────────────────┐
                     │  Pipeline worker process (Python)      │
                     │                                        │
                     │  ┌──────────────────────────────────┐  │
                     │  │  runner.claim_loop()             │  │
                     │  │  while not shutdown:             │  │
                     │  │    for stage in supported:       │  │
                     │  │      sem = sems[stage]           │  │
                     │  │      if not sem.acquire(0): skip │  │  ◄─ Story 6.7 generic
                     │  │      job = db.claim(stage, …)    │  │  ◄─ Story 6.2 SQL
                     │  │      if job is None:             │  │
                     │  │        sem.release(); continue   │  │
                     │  │      asyncio.create_task(        │  │
                     │  │        run(job, sem))            │  │
                     │  └────────────────┬─────────────────┘  │
                     │                   ▼                    │
                     │  ┌──────────────────────────────────┐  │
                     │  │  stages.extract.run(job)         │  │
                     │  │  (Story 2.3 ffmpeg invocation)   │  │
                     │  │  + temp-WAV registry             │  │
                     │  │  + cooperative pause check       │  │
                     │  └────────────────┬─────────────────┘  │
                     │                   ▼                    │
                     │  ┌──────────────────────────────────┐  │
                     │  │  finally:                        │  │
                     │  │    cleanup_temp_audio(job)       │  │  ◄─ this plan §7
                     │  │    sem.release()                 │  │
                     │  └──────────────────────────────────┘  │
                     │                                        │
                     │  ┌──────────────────────────────────┐  │
                     │  │  cpu_throttle.maybe_delay(stage) │  │  ◄─ this plan §6
                     │  │    if load_avg_5m > N × cores OR │  │
                     │  │       streaming.transcodes > 0:  │  │
                     │  │      next_claim_not_before =     │  │
                     │  │        now() + 30s               │  │
                     │  └──────────────────┬───────────────┘  │
                     └─────────────────────┼──────────────────┘
                                           │ gRPC (cheap, cached 5 s)
                                           ▼
                       ┌─────────────────────────────────────┐
                       │ Streaming service (Go)              │
                       │  pressure.Counter — atomic int      │
                       │   ▲ inc on FFmpeg start             │
                       │   ▼ dec on FFmpeg exit              │
                       │  GetResourcePressure() → snapshot   │
                       └─────────────────────────────────────┘
```

The Pipeline never reaches into the Streaming process or its files;
the only contact point is one cheap gRPC call, with a 5 s in-process
cache so the throttle check is essentially free per claim attempt.

---

## 2. Resource cap — definition and defaults

Source of truth: `pipeline.toml [workers].concurrency.extract`
(architecture §11.4). Default `2`.

| Knob                              | Default | Range  | Effect |
|-----------------------------------|---------|--------|--------|
| `concurrency.extract`             | 2       | 1–16   | Per-process semaphore size for the `extract` stage. |
| `pipeline.cpu_throttle_enabled`   | false   | bool   | Whether to consult load avg / streaming pressure before claiming. Off by default — Story 2.4 AC3 calls out "optional, off by default in v1". |
| `pipeline.cpu_throttle_load_mult` | 1.5     | 0.5–8  | `N` in `load_avg_5m > N × cores`. 1.5 means "50 % above one CPU per core". |
| `pipeline.cpu_throttle_delay_sec` | 30      | 5–600  | `not_before = now() + delay_sec` when throttled. |
| `pipeline.streaming_pressure_endpoint` | `127.0.0.1:50052` | host:port | Streaming gRPC; used only when throttle is enabled. |
| `pipeline.streaming_pressure_cache_sec` | 5 | 1–60 | TTL of the cached snapshot. |

**Why 2, not "as many as cores"?** Architecture §7.4 documents
extract as disk-bound and competing with the streaming service's
transcode I/O. On a single 30 TB spinning disk, two parallel
sequential reads already saturate the head; on SSD, two parallel
ffmpegs leave ample I/O for live streaming. Operators with multiple
disks can scale by **running additional Pipeline worker processes**
(architecture §10.3 horizontal scale-out), each with its own cap of 2.

**The cap is per-process, not per-host.** This is intentional and
matches the rest of the queue model: workers are stateless and add
capacity by adding processes, not by raising in-process limits.

---

## 3. Concurrent extraction limits — semaphore mechanics

The cap is implemented exactly as specified in
[Epic 6 Story 6.7](../06-job-queue/story-06-07-concurrency-caps.md):
an `asyncio.Semaphore(extract_cap)` in the worker process, acquired
*before* the SQL claim and released after the job reaches a
terminal-or-paused state.

### 3.1 Acquire-before-claim (not "claim-then-acquire")

The order matters. If the worker claimed first and only then tried to
acquire, a row could be marked `claimed` by us while every slot was
busy — that row would sit `claimed` until we ran it, even though
another worker (or process) could have run it sooner. So:

```
1. sem.acquire(timeout=0)   # non-blocking; skip stage if no slot
2. claim(stage='extract')   # the SQL UPDATE … RETURNING
3. if no row: sem.release(); poll next stage
4. else: schedule run(job, sem); the run task releases on exit
```

This keeps the semaphore as the **single gate** that admits a row into
`claimed`. It also means the stage limit is enforced before any DB
write, which is what makes a 5-job test deterministic: with cap 2 you
will never observe more than 2 rows in `running` for stage `extract`.

### 3.2 Release in `finally`, not in the happy path

The release must run on every exit path, including:

- normal completion (`done`)
- cooperative pause (`paused`, reason=user/shutdown)
- crash inside the stage (caught at the runner; row is left for the
  reaper, slot is released)
- decode error (`failed`)
- cancellation (`cancelled`)

A leaked semaphore permit shrinks the effective cap forever. The
runner wraps each stage execution in `try/finally` and releases
unconditionally; tests (§9 T-leak) inject crashes mid-stage to prove
the slot returns.

### 3.3 Priority is the SQL ORDER BY, not anything we add here

Story 6.2's claim SQL already orders by `priority, id`. So a
priority-50 user-initiated extract jumps ahead of a queue of
priority-100 backfill rows the next time *any* slot frees, with no
extra logic in this stage. The test in §9 T-priority verifies this
end-to-end against the real claim SQL.

### 3.4 Cross-stage interactions

The `transcribe` stage acquires the extract slot *transitively*: when
running on a streaming STT backend (the default — see Story 2.3), the
extract subprocess is the upstream of the transcribe pipe. If two
extracts are running and a third is needed because a transcribe job
demands fresh audio, that transcribe waits at the transcribe semaphore
(cap 1 per GPU); when it eventually claims a slot it spawns its own
extract subprocess **inside its own slot**, *not* a fresh
`extract`-stage row. So extract-stage rows only count "standalone"
extractions (the rare case where extract is decoupled from transcribe,
e.g. extract-then-batch-API). The cap is correct in both regimes.

---

## 4. The job state machine, from `extract`'s point of view

`extract` participates in the same machine as every other stage
(architecture §7.2). The transitions this plan needs to be precise
about are:

| From              | Trigger                                           | To           | Slot held? |
|-------------------|---------------------------------------------------|--------------|------------|
| `pending`         | `claim_loop` succeeds, semaphore held             | `claimed`    | yes        |
| `claimed`         | runner enters `run(job)`                          | `running`    | yes        |
| `running`         | extraction completes successfully                 | `done`       | released   |
| `running`         | `pause_requested` observed at chunk boundary      | `paused`     | released   |
| `running`         | `cancel_requested` observed at chunk boundary     | `cancelled`  | released   |
| `running`         | ffmpeg exits non-zero / decode error              | `failed` or → `pending` (retry) | released |
| `claimed`/`running` | worker process dies                            | (left as-is) | leaked until reaper |
| (any)             | reaper detects stale heartbeat (Story 6.6)        | `paused`     | n/a (other process) |

The slot column is the invariant this plan owns: the in-process
semaphore mirrors **only the rows this process moved into
`claimed`/`running`**. When the reaper flips a remote process's row to
`paused`, that affects no semaphore in this process — there is nothing
to release here. This is why the cap is per-process and not a global
counter in the DB.

---

## 5. Priority overrides FIFO — what we have to verify

Story 2.4 AC2 asks: with extract slots saturated by priority-100 jobs,
a freshly enqueued priority-50 job runs **first** when a slot frees.

The behaviour is supplied entirely by Story 6.2's SQL
(`ORDER BY priority, id`), but two failure modes would silently break
it inside the extract path:

1. **A worker that holds the slot beyond `done`** — e.g., logs after
   release. Slow logging can starve the next claim. Fix: release the
   semaphore before any post-job work; logging happens after release.
2. **A worker that polls only one stage per tick** — if `claim_loop`
   sleeps 1 s between stages and there are 6 stages, the priority-50
   waits up to 6 s after slot-free. Mitigation: the post-release
   `claim_loop` immediately retries the same stage before moving on
   to the next stage.

Both are guarded by tests in §9.

---

## 6. CPU throttle (optional, off by default)

The throttle is a single boolean knob that, when on, asks two
questions before each claim:

1. **Local pressure.** `os.getloadavg()[1]` (5-minute load avg) on
   POSIX. On systems where `getloadavg` is not available, the throttle
   silently behaves as if pressure is 0 (we never block on a missing
   signal — that would let a misconfiguration starve the whole queue).
2. **Streaming pressure.** `streaming.GetResourcePressure()` over the
   gRPC link defined in the streaming proto, returning
   `{active_transcodes, cpu_load_5m_observed}`. Cached for 5 s in the
   Pipeline process so a 100-job batch only makes ~one call per 5 s.

If **either** signal exceeds its threshold, the claim is delayed:

```
not_before = max(existing, now() + cpu_throttle_delay_sec)
```

The check happens at the claim_loop level, not per-job; once
throttled, the worker simply skips the extract stage for one cycle
and tries again 30 s later. No DB write is required for this — we
just don't claim anything for `extract`. Other stages
(`probe`, `index`) are unaffected.

The acceptance criterion uses `cpu.load_avg_5m > N × cores` as the
formula. We use `os.cpu_count()` for the cores divisor; on macOS the
value matches `sysctl hw.logicalcpu`, on Linux it matches
`nproc`. The default `N = 1.5` is calibrated so a quiet idle box
(load ~ 0.5) never throttles, but a transcoding session that sustains
~2× core usage does.

**Why off by default.** v1 ships single-host with predictable
hardware; throttling adds operational mystery ("why is my queue
idle?") that is hard to debug. Operators who genuinely contend with
streaming flip it on per
[architecture §11.4 example](../../architecture.md).

---

## 7. Temp file cleanup

Story 2.3 introduces `~/.maktaba/cache/audio/{hash}.wav` for the
fallback path where the STT backend cannot consume a stream. This
plan owns its lifecycle:

### 7.1 Ownership rule

Every temp WAV is created by exactly one job and is owned by that job
until the job reaches a terminal-or-paused state, at which point the
file is deleted unless **the same job intends to resume on the same
file** (paused with `reason='user' | 'shutdown' | 'crash'`). On
resume, the audio is re-extracted from source — the WAV cache is not
a resume point.

So the deletion rule is simpler than it sounds: **delete on
`done`, `failed`, `cancelled`, and on every `paused`**. Re-create on
the next claim if needed.

### 7.2 Tracking

Each running job maintains an entry in an in-process registry:

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/extract.py
_TEMP_AUDIO: dict[int, set[Path]] = {}  # job_id → file paths
```

The registry is `dict[int, set[Path]]`; a single job can produce only
one file in v1, but the set accommodates future multi-track libraries
(architecture §3.3 multi_audio).

### 7.3 Cleanup hook

The runner's `try/finally` (the same one that releases the semaphore)
calls `cleanup_temp_audio(job_id)`:

```python
def cleanup_temp_audio(job_id: int, logger: Logger) -> None:
    paths = _TEMP_AUDIO.pop(job_id, set())
    for p in paths:
        try:
            p.unlink(missing_ok=True)
        except OSError as e:
            logger.warning("temp_audio.cleanup_failed", path=str(p), err=str(e))
```

`missing_ok=True` makes a double-cleanup a no-op (e.g., reaper-driven
cleanup followed by worker-driven cleanup). Unlink failures are
logged but never raised — a stranded WAV is not worth crashing the
worker over; the cache GC task (architecture §12.1
`tasks/cache GC`) sweeps any leftover files older than 24 h.

### 7.4 Cache GC backstop

The nightly `tasks.cache_gc` task already exists per architecture
(`pipeline/src/maktaba_pipeline/tasks/`). This plan adds one rule:

- Files in `~/.maktaba/cache/audio/` whose mtime is older than 24 h
  AND whose `{hash}` is not present in any `running`/`paused` job's
  registry are deleted.

This is a backstop, not the primary mechanism. In normal operation,
in-process cleanup deletes the file within seconds of job
termination; the GC catches files orphaned by a kill -9.

### 7.5 Cross-process orphans

If a worker process dies holding a WAV, the in-process registry is
gone with it. The **reaper** (Story 6.6) flips the row to `paused`,
and on the next claim either:

- a different worker re-creates the WAV from source (because the
  cache lookup is keyed by content hash and the hashing process has
  stable inputs — same source file → same hash → same cache key); or
- the cache GC backstop removes the orphan within 24 h.

Either way, no human intervention is required.

---

## 8. Code

### 8.1 Python — extract stage with semaphore + temp WAV registry

`pipeline/src/maktaba_pipeline/pipeline/stages/extract.py`:

```python
from __future__ import annotations

import asyncio
import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from pathlib import Path

import structlog

from maktaba_pipeline.media import ffmpeg
from maktaba_pipeline.pipeline.runner import StageContext

log = structlog.get_logger(__name__)

# Per-process registry. Keyed by job_id so cleanup is O(1).
_TEMP_AUDIO: dict[int, set[Path]] = {}


@dataclass(frozen=True)
class ExtractResult:
    """Returned by run() so the runner can record metrics."""

    bytes_streamed: int
    used_temp_file: bool
    temp_path: Path | None
    decoded_seconds: float


async def run(ctx: StageContext) -> ExtractResult:
    """Execute one extract job.

    The runner has already acquired the extract semaphore and claimed
    the row; this function only performs the work and registers any
    temp WAV it creates. Cleanup is the runner's responsibility (it
    happens in its `finally`).
    """
    job, video, track = ctx.job, ctx.video, ctx.selected_track

    backend = ctx.stt_backend_for(video)
    if backend.requires_file:
        path = _temp_audio_path(video, track)
        _TEMP_AUDIO.setdefault(job.id, set()).add(path)
        await ffmpeg.extract_to_file(
            source=video.path,
            track_index=track.index,
            dest=path,
            seek_sec=job.last_segment_end_sec,
        )
        decoded = await ffmpeg.audio_seconds(path)
        return ExtractResult(
            bytes_streamed=path.stat().st_size,
            used_temp_file=True,
            temp_path=path,
            decoded_seconds=decoded,
        )

    bytes_streamed = 0
    decoded = 0.0
    async for chunk in ffmpeg.extract_stream(
        source=video.path,
        track_index=track.index,
        seek_sec=job.last_segment_end_sec,
    ):
        bytes_streamed += len(chunk)
        # 16 kHz mono s16: 2 bytes per sample, 16000 samples per second.
        decoded = bytes_streamed / (16000 * 2)
        await ctx.transcribe_consumer.feed(chunk)
        if await ctx.should_pause():
            await ffmpeg.terminate_current(timeout_sec=5.0)
            raise ctx.PauseRequested(at_sec=decoded)

    return ExtractResult(
        bytes_streamed=bytes_streamed,
        used_temp_file=False,
        temp_path=None,
        decoded_seconds=decoded,
    )


def cleanup_temp_audio(job_id: int) -> None:
    """Idempotent. Called from the runner's `finally`."""
    paths = _TEMP_AUDIO.pop(job_id, set())
    for p in paths:
        try:
            p.unlink(missing_ok=True)
        except OSError as e:
            log.warning("temp_audio.cleanup_failed", path=str(p), err=str(e))


def _temp_audio_path(video, track) -> Path:
    root = Path(os.environ.get("MAKTABA_HOME", "/var/maktaba")) / "cache" / "audio"
    root.mkdir(parents=True, exist_ok=True)
    # Keyed by content hash + track index so the same job resuming on
    # the same source picks up the same path; different tracks of the
    # same source coexist.
    return root / f"{video.content_hash}-a{track.index}.wav"
```

`pipeline/src/maktaba_pipeline/pipeline/runner.py` (relevant excerpt
— the rest is shared with Story 6.7):

```python
class Runner:
    def __init__(self, settings: Settings, db: Database) -> None:
        self.settings = settings
        self.db = db
        cap = settings.workers.concurrency
        self._semaphores: dict[str, asyncio.Semaphore] = {
            stage: asyncio.Semaphore(value=cap[stage]) for stage in cap
        }
        self._throttle = CpuThrottle(settings)

    async def claim_loop(self, supported_stages: list[str]) -> None:
        while not self._shutdown.is_set():
            claimed_any = False
            for stage in supported_stages:
                if not await self._throttle.may_claim(stage):
                    continue
                sem = self._semaphores[stage]
                if not sem.locked() and sem._value > 0:  # cheap check
                    await sem.acquire()
                else:
                    continue
                job = await self.db.claim(stage=stage, worker_id=self.id)
                if job is None:
                    sem.release()
                    continue
                claimed_any = True
                asyncio.create_task(self._run_with_release(job, sem))
            if not claimed_any:
                await asyncio.sleep(self.settings.claim_poll_sec)

    async def _run_with_release(self, job, sem: asyncio.Semaphore) -> None:
        try:
            ctx = await self._build_ctx(job)
            await self._run_stage(ctx)  # dispatches to stages.extract.run, etc.
        except PauseRequested as p:
            await self.db.mark_paused(job.id, at_sec=p.at_sec, reason="user")
        except CancelRequested as c:
            await self.db.mark_cancelled(job.id, at_sec=c.at_sec)
        except Exception as e:
            await self._record_failure(job, e)  # sets failed or pending+backoff
        else:
            await self.db.mark_done(job.id)
        finally:
            if job.stage == "extract":
                from maktaba_pipeline.pipeline.stages.extract import cleanup_temp_audio
                cleanup_temp_audio(job.id)
            sem.release()
```

`pipeline/src/maktaba_pipeline/pipeline/cpu_throttle.py`:

```python
from __future__ import annotations

import os
import time
from dataclasses import dataclass

import grpc
import structlog

from maktaba_pipeline.grpc_clients import streaming_pb2, streaming_pb2_grpc

log = structlog.get_logger(__name__)


@dataclass
class _PressureSnapshot:
    active_transcodes: int
    load_avg_5m: float
    fetched_at: float


class CpuThrottle:
    """Decides whether the next claim of a heavy stage should be skipped."""

    HEAVY_STAGES = {"extract", "transcribe"}

    def __init__(self, settings) -> None:
        self.enabled = settings.pipeline.cpu_throttle_enabled
        self.load_mult = settings.pipeline.cpu_throttle_load_mult
        self.delay_sec = settings.pipeline.cpu_throttle_delay_sec
        self.cache_ttl = settings.pipeline.streaming_pressure_cache_sec
        self.endpoint = settings.pipeline.streaming_pressure_endpoint
        self._cores = max(1, os.cpu_count() or 1)
        self._cache: _PressureSnapshot | None = None
        self._next_claim_after: float = 0.0

    async def may_claim(self, stage: str) -> bool:
        if not self.enabled or stage not in self.HEAVY_STAGES:
            return True
        now = time.monotonic()
        if now < self._next_claim_after:
            return False
        snap = await self._snapshot(now)
        threshold = self.load_mult * self._cores
        if snap.load_avg_5m > threshold or snap.active_transcodes > 0:
            self._next_claim_after = now + self.delay_sec
            log.info(
                "cpu_throttle.delay",
                stage=stage,
                load_avg_5m=snap.load_avg_5m,
                threshold=threshold,
                active_transcodes=snap.active_transcodes,
                delay_sec=self.delay_sec,
            )
            return False
        return True

    async def _snapshot(self, now: float) -> _PressureSnapshot:
        if self._cache and now - self._cache.fetched_at < self.cache_ttl:
            return self._cache
        try:
            local_load = os.getloadavg()[1]
        except (OSError, AttributeError):  # pragma: no cover — Windows
            local_load = 0.0
        active = 0
        try:
            async with grpc.aio.insecure_channel(self.endpoint) as ch:
                stub = streaming_pb2_grpc.StreamingServiceStub(ch)
                resp = await stub.GetResourcePressure(
                    streaming_pb2.GetResourcePressureRequest(),
                    timeout=0.5,
                )
            active = resp.active_transcodes
        except grpc.RpcError as e:
            log.warning("streaming_pressure.unreachable", err=str(e))
        snap = _PressureSnapshot(
            active_transcodes=active,
            load_avg_5m=local_load,
            fetched_at=now,
        )
        self._cache = snap
        return snap
```

### 8.2 Go — streaming pressure exporter

`shared/proto/streaming.proto` (additions only):

```proto
service StreamingService {
  // existing OpenSession / CloseSession RPCs remain unchanged
  rpc GetResourcePressure(GetResourcePressureRequest)
    returns (GetResourcePressureResponse);
}

message GetResourcePressureRequest {}

message GetResourcePressureResponse {
  int32  active_transcodes = 1;   // running FFmpeg transcoders right now
  double cpu_load_5m       = 2;   // streaming process's view of /proc/loadavg
  int64  observed_at_unix_ms = 3; // server-side timestamp for staleness checks
}
```

`streaming/internal/transcode/pressure/counter.go`:

```go
package pressure

import (
    "sync/atomic"
    "time"
)

// Counter is process-global. The transcode orchestrator increments
// when it spawns an FFmpeg process and decrements when the process
// exits. The counter is monotonic per-spawn, not per-segment, so the
// reading is "how many ffmpegs are currently transcoding" not "how
// many segments are in flight" — which is what the Pipeline needs for
// throttling.
type Counter struct {
    active atomic.Int32
}

// Default is the singleton injected into the gRPC server and the
// transcode package. We keep it package-level so wiring is trivial.
var Default = &Counter{}

func (c *Counter) Inc() { c.active.Add(1) }
func (c *Counter) Dec() { c.active.Add(-1) }
func (c *Counter) Snapshot() Snapshot {
    return Snapshot{Active: int(c.active.Load()), At: time.Now()}
}

type Snapshot struct {
    Active int
    At     time.Time
}
```

`streaming/internal/transcode/transcoder.go` (one-line additions, in
the section that already spawns FFmpeg):

```go
import "github.com/maktaba/maktaba/streaming/internal/transcode/pressure"

func (t *Transcoder) Start(ctx context.Context, req Request) (*Session, error) {
    cmd := exec.CommandContext(ctx, t.binary, req.Args()...)
    if err := cmd.Start(); err != nil {
        return nil, err
    }
    pressure.Default.Inc()
    go func() {
        _ = cmd.Wait()
        pressure.Default.Dec()
    }()
    // …existing session bookkeeping…
}
```

`streaming/internal/grpcserver/pressure.go`:

```go
package grpcserver

import (
    "context"
    "fmt"
    "os"
    "time"

    "github.com/maktaba/maktaba/streaming/internal/transcode/pressure"
    pb "github.com/maktaba/maktaba/shared/proto/gen/go/streaming"
)

// loadAvg5m is overridable for tests. On non-Linux it returns 0.
var loadAvg5m = func() float64 {
    if avg, err := readLoadAvg(); err == nil {
        return avg
    }
    return 0
}

func readLoadAvg() (float64, error) {
    // /proc/loadavg: "0.10 0.20 0.30 1/123 4567"
    data, err := os.ReadFile("/proc/loadavg")
    if err != nil {
        return 0, err
    }
    var one, five, fifteen float64
    if _, err := fmt.Sscanf(string(data), "%f %f %f", &one, &five, &fifteen); err != nil {
        return 0, err
    }
    return five, nil
}

func (s *Server) GetResourcePressure(
    _ context.Context, _ *pb.GetResourcePressureRequest,
) (*pb.GetResourcePressureResponse, error) {
    snap := pressure.Default.Snapshot()
    return &pb.GetResourcePressureResponse{
        ActiveTranscodes:  int32(snap.Active),
        CpuLoad_5M:        loadAvg5m(),
        ObservedAtUnixMs:  time.Now().UnixMilli(),
    }, nil
}
```

The handler is intentionally tiny: a counter snapshot + a `/proc/loadavg`
read. It runs in well under 1 ms; even with 5 s caching disabled, the
cost on the streaming service is negligible.

---

## 9. Test cases

### 9.1 Story 2.4 acceptance tests (Python)

Located at `pipeline/tests/test_extract_resource_accounting.py`.

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| T1 | `test_concurrency_cap_enforced` | Cap 2; enqueue 5 extract jobs; the stage `run` is replaced with `await asyncio.sleep(0.5)` so timing is deterministic. | At every 50 ms sample for the run, `count(state='running')` ≤ 2. Total wall time ≈ `ceil(5/2) × 0.5 = 1.5 s ± tolerance`. |
| T2 | `test_priority_overrides_fifo` | Cap 2; enqueue 3 jobs at priority 100, then 1 at priority 50; run sleeps 200 ms. | The priority-50 job is among the first slot to free; assert `claimed_at[priority=50] < claimed_at[priority=100][2]`. |
| T3 | `test_cpu_throttle_delays_claim` | Throttle on; mock `os.getloadavg` to return `(0, 4.0, 0)` and `os.cpu_count()=2` (load 4.0 > 1.5×2=3.0). | Next claim attempt returns `False` from `may_claim`; `_next_claim_after` is bumped by `delay_sec`; no `extract` claim happens for the next `delay_sec`. |
| T4 | `test_cpu_throttle_off_by_default` | Default settings; load avg fixture cranked to 99. | `may_claim` returns `True`; jobs are claimed normally. |
| T5 | `test_cpu_throttle_streaming_active` | Throttle on; mock `streaming.GetResourcePressure` to return `active_transcodes=1, load=0`. | Throttled regardless of low load. |
| T6 | `test_cpu_throttle_grpc_unreachable_does_not_block` | Throttle on; streaming endpoint refuses connection. | `may_claim` returns `True` (the missing signal must not starve the queue); a warning is logged. |

### 9.2 Temp file lifecycle (Python)

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| T7 | `test_temp_wav_deleted_on_done` | STT backend with `requires_file=True`; run extract to completion. | After `mark_done`, the WAV path does not exist; `_TEMP_AUDIO[job_id]` raises KeyError. |
| T8 | `test_temp_wav_deleted_on_failure` | Inject `ffmpeg.extract_to_file` raising `RuntimeError("decode")`. | After error handling, WAV deleted; semaphore released; row is `failed` (or `pending` with backoff if attempts remain). |
| T9 | `test_temp_wav_deleted_on_pause` | Cooperative pause mid-extraction. | WAV deleted at pause; row is `paused`; on resume claim, a fresh WAV is created (test asserts the new file's mtime > old). |
| T10 | `test_temp_wav_deleted_on_cancel` | Set `cancel_requested=True` mid-extraction. | WAV deleted; row is `cancelled`. |
| T11 | `test_temp_wav_double_cleanup_is_noop` | Call `cleanup_temp_audio(job_id)` twice. | Second call neither raises nor logs an error. |
| T12 | `test_cache_gc_removes_24h_orphan` | Place `~/.maktaba/cache/audio/orphan.wav` with mtime `now - 25h`; no job registers it. | GC sweep deletes the file. |
| T13 | `test_cache_gc_keeps_in_use_file` | Register the path in `_TEMP_AUDIO` for a `running` job; mtime 25 h. | GC sweep does **not** delete it. |

### 9.3 Semaphore-leak guard (Python)

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| T14 | `test_semaphore_released_on_stage_crash` | Replace `extract.run` with `raise RuntimeError("boom")`. | After run, `sem._value == cap` (no leaked permit). Run a second batch of `cap` jobs — they all execute, proving the slot returned. |
| T15 | `test_semaphore_released_on_pause` | Pause-requested pause path. | Same: `sem._value == cap`. |
| T16 | `test_release_happens_before_post_job_logging` | Patch the runner's post-`finally` log emit with a 1 s sleep; assert `sem._value == cap` is observed within 50 ms of the run task's exit. | Confirms §5 mitigation. |

### 9.4 Reaper interactions (Python)

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| T17 | `test_reaper_does_not_touch_local_semaphore` | Reaper flips a *different* worker's row to `paused`. | This worker's `sem._value` is unchanged (the leaked-other-process permit is not ours to reclaim). |
| T18 | `test_reaper_orphans_temp_wav_until_gc` | Worker dies holding a registered WAV; reaper paused row. | WAV is still on disk (no in-process cleanup ran); content-hash key is reused on next claim; cache GC eventually removes the orphan within 24 h. |

### 9.5 Streaming pressure exporter (Go)

`streaming/internal/transcode/pressure/counter_test.go`:

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| G1 | `TestCounter_IncDec` | 100 goroutines call `Inc()`, then 100 call `Dec()`. | `Snapshot().Active == 0` at the end; race detector clean. |
| G2 | `TestCounter_NeverNegative` | One spurious extra `Dec()`. | `Snapshot().Active == -1` is permitted by the type but is logged as anomaly via the gRPC handler's clamp (handler returns `max(0, snap.Active)`). |

`streaming/internal/grpcserver/pressure_test.go`:

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| G3 | `TestGetResourcePressure_ReportsCounter` | Increment counter to 3; call gRPC. | Response `ActiveTranscodes == 3`. |
| G4 | `TestGetResourcePressure_NoLoadavgIsZero` | Override `loadAvg5m` to return error. | `CpuLoad_5M == 0`; no error returned to the caller. |
| G5 | `TestTranscoderIntegratesWithCounter` | Spawn an FFmpeg with a tiny generated `lavfi anullsrc` source for 200 ms; observe counter. | While running: `Active == 1`. After exit: `Active == 0`. |

### 9.6 End-to-end (real DB, real ffmpeg)

`pipeline/tests/integration/test_extract_e2e.py`:

| ID | Name | Setup | Assertion |
|----|------|-------|-----------|
| E1 | `test_5_jobs_2_slots_real_ffmpeg` | A SQLite DB plus 5 rows pointing at the same 30 s fixture, cap 2. | Wall time ≈ 3 batches × extract time; `running` count never exceeds 2; all rows reach `done`. |
| E2 | `test_pause_resume_temp_wav_lifecycle` | One row, file-based STT mock; pause at ~50 % progress; resume. | First WAV deleted at pause; second WAV created on resume; both have the same hash key but distinct mtimes; final transcript covers the whole file. |

---

## 10. Configuration

### 10.1 `pipeline.toml` — additions

```toml
[workers]
concurrency       = { scan = 4, probe = 4, extract = 2, transcribe = 1, index = 4, thumbnail = 2 }
heartbeat_sec     = 5

[pipeline]
cpu_throttle_enabled        = false
cpu_throttle_load_mult      = 1.5
cpu_throttle_delay_sec      = 30
streaming_pressure_endpoint = "127.0.0.1:50052"
streaming_pressure_cache_sec = 5

[cache]
audio_dir                    = "${MAKTABA_HOME}/cache/audio"
audio_orphan_max_age_hours   = 24
```

### 10.2 Environment overrides

Per architecture §11.1, every key is overridable via
`MAKTABA_PIPELINE_CPU_THROTTLE_ENABLED=true` etc. Tests cover this
once at the settings layer; this plan does not duplicate.

### 10.3 DB-stored override

Architecture §11.1 makes runtime knobs DB-overridable. The
`workers.concurrency.extract` entry is one such knob; the `/api/settings`
endpoint exposes it. A change is picked up at the start of the next
claim cycle (workers reload on every poll — cheap because it's an
in-process settings object). No worker restart is needed.

---

## 11. Observability

### 11.1 Metrics

OpenTelemetry counters/gauges, emitted by the runner and by
`cpu_throttle.py`:

| Metric                                  | Type    | Labels             | Meaning |
|-----------------------------------------|---------|--------------------|---------|
| `pipeline.extract.slots.in_use`         | gauge   | `worker_id`        | Current `cap - sem._value`. |
| `pipeline.extract.slots.cap`            | gauge   | `worker_id`        | Static cap value (reload-aware). |
| `pipeline.extract.claim.skipped_full`   | counter | `worker_id`        | Times the claim was skipped because `sem.acquire(0)` would have blocked. |
| `pipeline.extract.throttle.delays`      | counter | `worker_id, signal`| `signal ∈ {load, streaming}`. |
| `pipeline.extract.temp_wav.created`     | counter | `worker_id`        | Per-job WAV creation. |
| `pipeline.extract.temp_wav.cleanup_failed` | counter | `worker_id`     | Unlink errors. |
| `pipeline.extract.temp_wav.gc_removed`  | counter |                    | Files removed by the cache GC backstop. |
| `streaming.transcode.active`            | gauge   | `host`             | The Go counter, exported via the existing Prom scrape on the streaming process. |

### 11.2 Structured log events

| Event                          | Level | Fields |
|--------------------------------|-------|--------|
| `extract.slot.acquired`        | DEBUG | `job_id`, `slots_in_use`, `slots_cap` |
| `extract.slot.released`        | DEBUG | `job_id`, `state`, `duration_ms` |
| `cpu_throttle.delay`           | INFO  | `stage`, `load_avg_5m`, `threshold`, `active_transcodes`, `delay_sec` |
| `temp_audio.created`           | DEBUG | `job_id`, `path`, `bytes` |
| `temp_audio.cleanup_failed`    | WARN  | `path`, `err` |
| `streaming_pressure.unreachable` | WARN | `endpoint`, `err` (rate-limited 1/min) |

### 11.3 Tracing

Each `extract` run is wrapped in an OTel span `pipeline.extract` with
attributes `job.id`, `video.id`, `track.index`, `slots_in_use_at_start`,
`used_temp_file`. The throttle decision attaches a child span
`pipeline.extract.throttle` only when a delay is applied.

---

## 12. Error handling

| Failure | Detection | Response |
|---------|-----------|----------|
| **Semaphore leaked** (e.g., a `KeyboardInterrupt` slips past `finally`) | Periodic self-check: if `sem._value > cap` → log assertion, clamp. | A startup invariant test catches this in CI; in production we log `extract.slot.invariant_violation` at ERROR and reset the semaphore. |
| **Streaming gRPC unreachable** | `grpc.RpcError` from `GetResourcePressure`. | Treat as no streaming pressure (do not block). Log WARN, rate-limited. |
| **`os.getloadavg` unavailable** (Windows) | `OSError` / `AttributeError`. | Treat as load 0; throttle relies only on streaming counter. |
| **Temp WAV unlink fails** (file in use, permission, NFS stale handle) | `OSError` from `Path.unlink`. | Log WARN; rely on cache GC to mop up within 24 h. |
| **Cache directory missing or unwritable** | `OSError` from `mkdir` / `open`. | Stage fails with a structured error; the row goes `failed` (not retried — operator must fix the disk). |
| **CPU throttle set with implausible knobs** (e.g., `load_mult=0`) | Settings validator (`pydantic-settings`). | Refuse to start; clear error message names the offending key. |
| **Reaper races with in-process cleanup** | Reaper paused our row; we then enter `finally` and try to `mark_paused` again | The runner's `mark_paused` is idempotent on the (`paused`) state and a no-op if the reaper already wrote the same field; tests cover. |

---

## 13. Acceptance checklist

Sourced from
[story-02-04-resource-accounting.md](story-02-04-resource-accounting.md)
plus the operational invariants this plan adds.

**Behavioral**

- [ ] Cap default is `2`; loaded from `pipeline.toml [workers].concurrency.extract`.
- [ ] At most `cap` extract jobs are in `running` per worker process at any sampled instant (T1, E1).
- [ ] A priority-50 job runs ahead of priority-100 jobs as soon as a slot frees (T2).
- [ ] With throttle on, `load_avg_5m > N × cores` postpones the next extract claim by `delay_sec` (T3).
- [ ] With throttle on, `streaming.active_transcodes > 0` also postpones (T5).
- [ ] With throttle off (default), neither signal blocks claims (T4).
- [ ] An unreachable streaming endpoint does not block claims (T6).

**Temp file lifecycle**

- [ ] Temp WAV is deleted on `done` (T7), `failed` (T8), `paused` (T9), `cancelled` (T10).
- [ ] `cleanup_temp_audio` is idempotent (T11).
- [ ] Cache GC removes orphans older than 24 h (T12) and spares in-use files (T13).
- [ ] A killed worker's WAV is recovered by GC within 24 h (T18).

**Semaphore correctness**

- [ ] Slot is released even if the stage crashes (T14) or pauses (T15).
- [ ] Slot is released before any post-job logging or metric emit (T16).
- [ ] Reaper-driven pause on a remote process does not affect local semaphore counts (T17).

**Cross-language wiring**

- [ ] `shared/proto/streaming.proto` adds `GetResourcePressure`; generated code lands in both `gen/go/` and `gen/python/`.
- [ ] Streaming Go counter is incremented and decremented exactly once per FFmpeg lifecycle (G1, G5).
- [ ] gRPC handler returns the live counter and a non-fatal load-avg reading (G3, G4).

**Operational**

- [ ] All 18 Python tests + 5 Go tests pass on Linux and macOS in CI.
- [ ] `streaming.transcode.active` and `pipeline.extract.slots.in_use` gauges show up on the Prometheus scrape.
- [ ] `/api/settings` returns the current `concurrency.extract` value; PATCHing it takes effect on the next claim cycle without a restart.
- [ ] Documentation in [README.md](README.md) for Epic 02 cross-links to this plan.
