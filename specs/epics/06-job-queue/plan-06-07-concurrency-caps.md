# Implementation Plan — Story 6.7 Concurrency Model & Per-Host Caps

> Companion to [story-06-07-concurrency-caps.md](story-06-07-concurrency-caps.md).
> The story states *what* and *why*; this plan states *how*.
> Defaults follow [architecture.md §7.4](../../architecture.md);
> the canonical stage list is owned by
> [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Python (Pipeline). Concurrency is a worker-process-local concern; the DB doesn't enforce caps (it can't — caps are a property of the host's CPU/GPU, not the queue). |
| Files | `pipeline/src/maktaba_pipeline/pipeline/concurrency.py` (semaphores + GPU device locks), `pipeline/src/maktaba_pipeline/pipeline/devices.py` (device enumeration), `pipeline/tests/pipeline/test_concurrency.py`. |
| Config dependency | `pipeline.toml [workers].concurrency` (architecture §11.4); the parser lives in `pipeline/config.py`. |
| Out of scope | The claim loop's interaction with semaphores (Story 6.2 owns that wiring); per-stage handler implementations (Epics 1-5). |

## 1. Architecture diagram

```
            ┌──────────────────────────────────────────────────┐
            │ pipeline.toml                                    │
            │   [workers]                                      │
            │   concurrency = { scan=4, probe=4, extract=2,    │
            │                   transcribe=1, subtitle_gen=2,  │
            │                   index=4, thumbnail=2 }         │
            └──────────────────────┬───────────────────────────┘
                                   │ load on worker startup
                                   ▼
            ┌──────────────────────────────────────────────────┐
            │ ConcurrencyManager(cfg)                           │
            │                                                  │
            │   per-stage semaphores:                          │
            │     {Stage.SCAN:        Semaphore(4),            │
            │      Stage.PROBE:       Semaphore(4),            │
            │      Stage.EXTRACT:     Semaphore(2),            │
            │      Stage.TRANSCRIBE:  Semaphore(num_devices),  │
            │      Stage.SUBTITLE_GEN:Semaphore(2),            │
            │      Stage.INDEX:       Semaphore(4),            │
            │      Stage.THUMBNAIL:   Semaphore(2)}            │
            │                                                  │
            │   per-device locks (GPU stages):                 │
            │     {"cuda:0": Lock(), "cuda:1": Lock(), ...}    │
            │     (or {"mlx:0": Lock()} on Apple silicon)      │
            │                                                  │
            │   acquire(stage):                                │
            │     await stage_semaphore.acquire()              │
            │     if stage in GPU_STAGES:                       │
            │       device = pick_least_busy_device()          │
            │       await device_locks[device].acquire()       │
            │     return Reservation(stage, device)            │
            │                                                  │
            │   release(reservation):                          │
            │     reverse order                                │
            └──────────────────────────────────────────────────┘
                                   │
                                   ▼
            ┌──────────────────────────────────────────────────┐
            │ ClaimLoop (Story 6.2):                           │
            │   try acquire(stage, timeout=0)                  │
            │   if not acquired: skip this iteration           │
            │   else: claim job, dispatch, release on finish   │
            └──────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/devices.py` | `enumerate_devices() -> list[DeviceID]`, healthcheck, recheck-after-failure. |
| `pipeline/src/maktaba_pipeline/pipeline/concurrency.py` | `ConcurrencyManager`, `Reservation`, `GPU_STAGES`. |
| `pipeline/tests/pipeline/test_concurrency.py` | Cap and GPU-lock tests. |
| `pipeline/tests/pipeline/test_devices.py` | Device enumeration on Apple silicon, NVIDIA, CPU-only. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/config.py` | Adds `WorkersConfig.concurrency: dict[Stage, int]` with defaults; adds `subtitle_gen=2`. |
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | `Worker.__init__` builds a `ConcurrencyManager` and passes its semaphores to `ClaimLoop`. |
| `pipeline/src/maktaba_pipeline/cli.py` | `--stages` flag scoped to a comma-separated list of stage names. |

### 2.3 Defaults — single source of truth

```python
# pipeline/src/maktaba_pipeline/config.py
from .db.jobs import Stage


DEFAULT_CONCURRENCY: dict[Stage, int] = {
    Stage.SCAN:         4,
    Stage.PROBE:        4,
    Stage.EXTRACT:      2,
    Stage.TRANSCRIBE:   1,    # overridden at runtime to num_devices
    Stage.SUBTITLE_GEN: 2,    # added per Story 6.7 AC; aligns with canonical enum
    Stage.INDEX:        4,
    Stage.THUMBNAIL:    2,
}

GPU_STAGES: frozenset[Stage] = frozenset({
    Stage.TRANSCRIBE,
    # Future: diarization, vision-model thumbnail backends.
})
```

The defaults match architecture §7.4 with the explicit `subtitle_gen`
addition. Anyone who removes `subtitle_gen` from this dict breaks the
canonical-enum parity test.

### 2.4 Type definitions

```python
# pipeline/src/maktaba_pipeline/pipeline/concurrency.py
from __future__ import annotations

import asyncio
import logging
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Optional

from ..db.jobs import Stage
from ..config import DEFAULT_CONCURRENCY, GPU_STAGES
from .devices import DeviceID, enumerate_devices


log = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class Reservation:
    stage: Stage
    device: DeviceID | None    # set for GPU stages, None otherwise


class ConcurrencyManager:
    def __init__(
        self,
        *,
        per_stage: Mapping[Stage, int] | None = None,
        devices: list[DeviceID] | None = None,
    ):
        self._cfg = dict(DEFAULT_CONCURRENCY)
        if per_stage:
            self._cfg.update(per_stage)

        self._devices = devices if devices is not None else enumerate_devices()
        # Override transcribe concurrency to match device count when GPUs
        # are present; for CPU-only the configured value (default 1) wins.
        if self._devices and Stage.TRANSCRIBE in self._cfg:
            self._cfg[Stage.TRANSCRIBE] = max(
                self._cfg[Stage.TRANSCRIBE], len(self._devices),
            )

        self._stage_sems: dict[Stage, asyncio.Semaphore] = {
            stage: asyncio.Semaphore(cap)
            for stage, cap in self._cfg.items()
        }
        self._device_locks: dict[DeviceID, asyncio.Lock] = {
            d: asyncio.Lock() for d in self._devices
        }
        self._device_health: dict[DeviceID, float] = {
            d: 0.0 for d in self._devices   # next-recheck-time epoch
        }

    @property
    def stage_semaphores(self) -> Mapping[Stage, asyncio.Semaphore]:
        return self._stage_sems

    def cap(self, stage: Stage) -> int:
        return self._cfg.get(stage, 0)

    def supports(self, stage: Stage) -> bool:
        return self._cfg.get(stage, 0) > 0

    async def acquire(self, stage: Stage, *, timeout: float | None = None) -> Reservation:
        """Acquire a stage slot (and a GPU device if the stage is GPU-bound).

        Returns a Reservation that MUST be released via `release()` in a
        finally block.
        """
        sem = self._stage_sems[stage]
        if timeout is None:
            await sem.acquire()
        else:
            await asyncio.wait_for(sem.acquire(), timeout=timeout)

        device: DeviceID | None = None
        try:
            if stage in GPU_STAGES and self._devices:
                device = await self._pick_device()
            return Reservation(stage=stage, device=device)
        except BaseException:
            # Release the stage semaphore on failure; the device lock
            # is released inside _pick_device's failure path.
            sem.release()
            raise

    async def release(self, r: Reservation) -> None:
        if r.device is not None:
            self._device_locks[r.device].release()
        self._stage_sems[r.stage].release()

    async def _pick_device(self) -> DeviceID:
        """Acquire the lock on the least-busy healthy device.

        Strategy: try-acquire each device in turn; the first that
        succeeds wins. If all are busy, await the first one to release.
        """
        import time
        now = time.time()

        # Filter out devices in cooldown.
        healthy = [d for d in self._devices if self._device_health[d] <= now]
        if not healthy:
            # Fall back to any device — the entire pool is in cooldown.
            healthy = list(self._devices)

        # First, attempt non-blocking acquires.
        for d in healthy:
            lock = self._device_locks[d]
            if not lock.locked():
                await lock.acquire()
                return d

        # All locked — wait on the first one available.
        d = healthy[0]
        await self._device_locks[d].acquire()
        return d

    def mark_device_unhealthy(self, device: DeviceID, recheck_sec: float = 300.0) -> None:
        """A stage caller signals the device is unhealthy (driver crash, OOM)."""
        import time
        self._device_health[device] = time.time() + recheck_sec
        log.warning("device_marked_unhealthy",
                    extra={"device": device, "recheck_sec": recheck_sec})
```

### 2.5 Device enumeration

`pipeline/src/maktaba_pipeline/pipeline/devices.py`:

```python
"""Discovers GPU devices at worker startup. Pure best-effort: no PyTorch
dependency. We probe for CUDA via NVML if the lib is available, MLX via
checking for Apple silicon, otherwise return an empty list (CPU-only)."""
from __future__ import annotations

import logging
import platform
from typing import NewType


DeviceID = NewType("DeviceID", str)

log = logging.getLogger(__name__)


def enumerate_devices() -> list[DeviceID]:
    devices: list[DeviceID] = []

    # CUDA via pynvml if installed.
    try:
        import pynvml  # type: ignore[import-not-found]
        pynvml.nvmlInit()
        try:
            count = pynvml.nvmlDeviceGetCount()
            for i in range(count):
                devices.append(DeviceID(f"cuda:{i}"))
        finally:
            pynvml.nvmlShutdown()
    except Exception:
        log.debug("pynvml_unavailable", exc_info=True)

    # Apple silicon — single MLX device.
    if platform.system() == "Darwin" and platform.machine() == "arm64":
        if not devices:
            devices.append(DeviceID("mlx:0"))
        # If both CUDA and Apple silicon are reported (rare external
        # GPU on a Mac), prefer CUDA — CUDA is the more capable backend.

    if not devices:
        log.info("no_gpu_devices_detected")

    return devices
```

The function is pure — no global state. Tests inject `devices=[...]`
directly into `ConcurrencyManager` to avoid hardware dependence.

## 3. Wiring into the runner

`pipeline/src/maktaba_pipeline/pipeline/runner.py` (modifications):

```python
class Worker:
    def __init__(self, cfg: WorkerConfig, db):
        self.cfg = cfg
        self.db = db
        self.concurrency = ConcurrencyManager(
            per_stage=cfg.workers.concurrency,
        )
        # The claim loop sees only the semaphores; the device lock is a
        # post-claim acquire inside the dispatch.
        self.claim_loop = ClaimLoop(
            db=db, cfg=cfg,
            dispatch=self._dispatch,
            semaphores=self.concurrency.stage_semaphores,
            shutdown_event=self._shutdown_event,
        )

    async def _dispatch(self, job: Job) -> None:
        stage = Stage(job.stage)
        # Stage semaphore is already held by ClaimLoop; we acquire only
        # the device lock here.
        if stage in GPU_STAGES and self.concurrency._devices:
            device = await self.concurrency._pick_device()
        else:
            device = None
        try:
            handler = STAGE_HANDLERS[stage]
            await handler(self.ctx, job, device=device)
        except StageError as e:
            await mark_failed_or_retry(self.db, job_id=job.id, error=e)
        except Exception as e:
            await mark_failed_or_retry(
                self.db, job_id=job.id,
                error=StageError(kind=type(e).__name__, message=str(e),
                                 traceback=traceback.format_exc(),
                                 retryable=True),
            )
        finally:
            if device is not None:
                self.concurrency._device_locks[device].release()
            # The claim loop releases the stage semaphore via its own
            # finally block in _run_job.
```

The split — claim loop owns the stage semaphore, dispatch owns the
device lock — keeps the loop simple and lets per-device serialization
run *after* the row is claimed. This means a transcribe row is held
in `claimed` state briefly while waiting on the GPU lock; the
heartbeat task (Story 6.3) keeps the row alive during that wait.

## 4. CLI flag — `--stages`

```python
# pipeline/src/maktaba_pipeline/cli.py (excerpt)
@app.command("run-worker")
def run_worker(
    stages: str = typer.Option(
        "scan,probe,extract,transcribe,subtitle_gen,index,thumbnail",
        "--stages",
        help="Comma-separated stage names this worker will claim.",
    ),
    config: Path = typer.Option("pipeline.toml", "--config"),
):
    cfg = load_config(config)
    parsed = tuple(Stage(s.strip()) for s in stages.split(",") if s.strip())
    if not parsed:
        raise typer.BadParameter("--stages must list at least one stage")
    worker_cfg = WorkerConfig(
        worker_id=f"{socket.gethostname()}/{os.getpid()}/{uuid4().hex[:8]}",
        supported_stages=parsed,
        ...
    )
    asyncio.run(_run(cfg, worker_cfg))
```

## 5. Test plan

### 5.1 Concurrency-cap tests (`pipeline/tests/pipeline/test_concurrency.py`)

| Test | What it pins |
|---|---|
| `test_default_caps_match_arch_7_4` | `ConcurrencyManager()` (no overrides) reports caps `scan=4, probe=4, extract=2, transcribe=1, subtitle_gen=2, index=4, thumbnail=2`. |
| `test_subtitle_gen_default_concurrency` | `cap(Stage.SUBTITLE_GEN) == 2`. |
| `test_concurrency_cap_respected_synthetic` | Spin 5 fake stage handlers calling `acquire(EXTRACT)` then sleeping 100 ms; assert at most 2 active at any sample (probe via `Semaphore._value`). |
| `test_disjoint_stage_workers_scale` | Two `ConcurrencyManager`s sharing nothing (different processes simulated by separate instances); workers with disjoint stage sets never block each other. |
| `test_acquire_release_ordering` | acquire → finally release → acquire again works; no leaked permits after 1000 cycles. |
| `test_acquire_with_timeout_zero_returns_false_when_full` | Saturate cap; `await wait_for(acquire, timeout=0)` raises `TimeoutError`; the claim loop interprets this as "skip this iteration." |
| `test_release_on_acquire_failure_returns_permit` | Patch `_pick_device` to raise; verify the stage semaphore is released. |

### 5.2 GPU-lock tests

| Test | What it pins |
|---|---|
| `test_gpu_lock_serializes_transcribe` | Construct manager with `devices=["cuda:0"]`; two coroutines call `acquire(TRANSCRIBE)`; second blocks until first releases. Assert at most one device-lock holder at any sample. |
| `test_multi_gpu_runs_in_parallel` | `devices=["cuda:0", "cuda:1"]`; transcribe cap auto-bumps to 2; two transcribe acquires return immediately, on distinct devices. |
| `test_gpu_lock_picks_idle_device` | `devices=["cuda:0", "cuda:1"]`; cuda:0 already locked; new acquire returns cuda:1. |
| `test_mark_device_unhealthy_skips_recheck_window` | Mark cuda:0 unhealthy; next acquire returns cuda:1; cuda:0 not picked until recheck_sec elapses. |
| `test_unhealthy_all_falls_back` | Mark all devices unhealthy; acquire still proceeds (any device — graceful degradation). |
| `test_cpu_only_no_device_lock` | `devices=[]`; `acquire(TRANSCRIBE)` returns `Reservation(device=None)`; transcribe cap = configured (default 1). |

### 5.3 Cross-stage GPU contention

`test_gpu_lock_serializes_across_stages` — when a future diarization
stage joins `GPU_STAGES`, both transcribe and diarize compete for the
same device locks. Test inserts a fake `DIARIZATION` stage into
`GPU_STAGES`, runs one of each concurrently → second blocks. Pins the
"GPU lock is per-device, not per-stage" property for forward
compatibility.

### 5.4 Integration with the claim loop

`test_claim_loop_skips_when_cap_full` — extends Story 6.2's runner
test. Saturate the EXTRACT semaphore by holding 2 reservations; enqueue
3 EXTRACT jobs; verify claim loop does not call `claim_one` for
EXTRACT until a reservation releases.

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Multi-GPU host | `enumerate_devices()` returns one DeviceID per GPU; transcribe cap auto-bumps to that count; per-device lock keeps each transcribe on a distinct device. | `test_multi_gpu_runs_in_parallel` |
| Device driver crash mid-job | The stage handler catches the runtime error, calls `concurrency.mark_device_unhealthy(device, recheck_sec=300)`, and re-raises as `StageError(retryable=True)`. The retry queues at `not_before = now + backoff`; subsequent acquires skip the unhealthy device. | `test_mark_device_unhealthy_skips_recheck_window` |
| Operator overrides only one stage in pipeline.toml | The override merges into `DEFAULT_CONCURRENCY`; unmentioned stages keep their defaults. | `test_partial_override_preserves_defaults` |
| Stage = 0 in pipeline.toml | Cap of 0 means "this worker doesn't run this stage." `supports(stage)` returns False; the claim loop excludes the stage from `supported_stages` even if `--stages` listed it. INFO log on startup. | `test_zero_cap_disables_stage` |
| No GPU detected on a host running transcribe | `devices=[]`; transcribe runs CPU-only at the configured cap (default 1). The CPU backend is always available; the worker logs a one-line WARN at startup if `transcribe` is in `--stages` and no devices were found. | `test_cpu_only_no_device_lock` |
| Two workers on the same host both listing transcribe | Both will start with their own `ConcurrencyManager`; their device locks are process-local → both can grab cuda:0 simultaneously, oversubscribing the GPU. **Mitigation:** documented in `pipeline.toml` comments — operators should run a single worker per host with broad `--stages`. A future improvement (out of scope) is a host-level lock via `flock` or a shared memory segment. | Documented in plan; not enforced. |
| `subtitle_gen` missing from defaults | Property test `test_default_caps_match_arch_7_4` enumerates the canonical stage set and asserts each is in the cap dict — adding a stage to the enum without updating defaults fails the build. | `test_default_caps_match_arch_7_4` |
| Reservation released twice | `Semaphore.release` does not error on over-release in CPython, but the count drifts. We detect via `assert sem._value <= cap` in a debug build; production simply tolerates. | Documented; defensive `try ... finally` patterns avoid it. |

## 7. Performance analysis

`asyncio.Semaphore.acquire` and `Lock.acquire` are O(1) under no
contention. With contention, asyncio.Lock uses a FIFO wait queue
(coroutines wake in FIFO order); the GPU lock fairness is "first to
arrive at the lock wins." For the Maktaba workload (handful of
stages, ≤ N=8 GPU devices), contention is the common case during
backfills and the wait queues never grow long. CPU overhead of the
manager is < 0.01% of a core.

## 8. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `pynvml` | optional, ≥ 11.5 | NVIDIA device enumeration. If absent, the manager falls back to "no devices detected." Listed as an optional extra in `pyproject.toml`. |
| stdlib `asyncio.Semaphore`, `asyncio.Lock` | n/a | All concurrency primitives. |

## 9. Acceptance checklist

**Code**
- [ ] `ConcurrencyManager` exposes `acquire(stage, timeout=None)`, `release(reservation)`, `cap(stage)`, `supports(stage)`, `mark_device_unhealthy(device)`, `stage_semaphores`.
- [ ] `enumerate_devices()` works on Apple silicon (returns `["mlx:0"]`), CUDA (returns `["cuda:0", ..., "cuda:N-1"]`), and CPU-only (returns `[]`).
- [ ] `--stages` flag parses a comma-separated list into a tuple of `Stage`; rejects unknown stage names with a typer-friendly error.
- [ ] Defaults dict includes `subtitle_gen=2`.

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_concurrency_cap_respected` — 5 jobs queued, cap 2 → exactly 2 in `running`.
- [ ] AC: `test_subtitle_gen_default_concurrency` — cap = 2.
- [ ] AC: `test_gpu_lock_serializes_transcribe` — never two on the same device.
- [ ] AC: `test_disjoint_stage_workers_scale` — disjoint workers don't block each other.

**Performance**
- [ ] No contention: per-acquire overhead < 10 µs.
- [ ] Under contention: FIFO fairness; no starvation under bounded load.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.7.
- [ ] `pipeline.toml` example in architecture §11.4 is updated to include `subtitle_gen=2`.
- [ ] Single-worker-per-host recommendation is documented in `pipeline.toml`'s `[workers]` block comment.
