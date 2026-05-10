"""Per-stage concurrency caps + per-device GPU locks (Story 6.7).

Two layered primitives:

- One :class:`asyncio.Semaphore` per stage, sized from
  :data:`DEFAULT_CONCURRENCY` (architecture §7.4). The claim loop
  attempts ``acquire()`` before claiming a job for that stage; a worker
  whose stage cap is full skips the claim rather than oversubscribing.
- One :class:`asyncio.Lock` per detected GPU device. GPU-bound stages
  (``transcribe`` today, future diarization etc.) acquire a device
  lock *after* the stage semaphore so two transcribe jobs on a
  single-GPU host serialize correctly even though the stage cap may
  permit more.

The split — claim loop owns the stage semaphore, dispatch owns the
device lock — keeps the loop simple and lets per-device serialization
run *after* the row is claimed. This means a transcribe row is held
in ``claimed`` state briefly while waiting on the GPU lock; the
heartbeat task (Story 6.3) keeps the row alive during that wait.

Defaults match architecture §7.4 + the explicit ``subtitle_gen=2`` AC
from Story 6.7. Operators override per-stage caps via
``pipeline.toml [workers].concurrency`` (which the worker's config
loader merges into :data:`DEFAULT_CONCURRENCY`).
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import Mapping
from dataclasses import dataclass

from ..db.jobs import Stage
from ..log import get_logger
from .devices import DeviceID, enumerate_devices

__all__ = [
    "DEFAULT_CONCURRENCY",
    "GPU_STAGES",
    "ConcurrencyManager",
    "Reservation",
]


_log = get_logger()


# Single source of truth for per-stage caps. The transcribe entry is
# bumped to ``len(devices)`` at construction when GPUs are present.
DEFAULT_CONCURRENCY: dict[Stage, int] = {
    Stage.SCAN: 4,
    Stage.PROBE: 4,
    Stage.EXTRACT: 2,
    Stage.TRANSCRIBE: 1,
    Stage.SUBTITLE_GEN: 2,
    Stage.INDEX: 4,
    Stage.THUMBNAIL: 2,
}


# Stages that need per-device GPU serialization. Future diarization /
# vision-model thumbnail backends would join this set; the per-device
# lock keeps the cross-stage contention story consistent.
GPU_STAGES: frozenset[Stage] = frozenset({Stage.TRANSCRIBE})


@dataclass(slots=True, frozen=True)
class Reservation:
    """Returned by :meth:`ConcurrencyManager.acquire`. Pass to ``release``."""

    stage: Stage
    device: DeviceID | None


class ConcurrencyManager:
    """Stage semaphores + per-device locks for one worker process.

    Construction is cheap and synchronous; the underlying
    :class:`asyncio.Semaphore` and :class:`asyncio.Lock` instances are
    created lazily so they bind to the current event loop. Tests that
    spin up multiple loops should construct the manager inside each
    test rather than at module scope.
    """

    def __init__(
        self,
        *,
        per_stage: Mapping[Stage, int] | None = None,
        devices: list[DeviceID] | None = None,
    ) -> None:
        cfg: dict[Stage, int] = dict(DEFAULT_CONCURRENCY)
        if per_stage:
            cfg.update(per_stage)

        self._devices: list[DeviceID] = devices if devices is not None else enumerate_devices()

        # Auto-bump transcribe to the number of detected devices when
        # GPUs are present (and the operator hasn't already set a higher
        # value). On CPU-only hosts the configured cap (default 1) wins.
        if self._devices and Stage.TRANSCRIBE in cfg:
            cfg[Stage.TRANSCRIBE] = max(cfg[Stage.TRANSCRIBE], len(self._devices))

        self._cfg = cfg
        self._stage_sems: dict[Stage, asyncio.Semaphore] = {
            stage: asyncio.Semaphore(cap) for stage, cap in cfg.items() if cap > 0
        }
        self._device_locks: dict[DeviceID, asyncio.Lock] = {
            d: asyncio.Lock() for d in self._devices
        }
        # Map DeviceID → epoch seconds when the device is healthy again.
        self._device_health: dict[DeviceID, float] = {d: 0.0 for d in self._devices}

    # ---- introspection ----------------------------------------------------

    @property
    def stage_semaphores(self) -> Mapping[Stage, asyncio.Semaphore]:
        return self._stage_sems

    @property
    def devices(self) -> tuple[DeviceID, ...]:
        return tuple(self._devices)

    def cap(self, stage: Stage) -> int:
        return self._cfg.get(stage, 0)

    def supports(self, stage: Stage) -> bool:
        """True iff this worker is configured to run ``stage`` (cap > 0)."""
        return self._cfg.get(stage, 0) > 0

    # ---- acquire / release ------------------------------------------------

    async def acquire(
        self,
        stage: Stage,
        *,
        timeout: float | None = None,
    ) -> Reservation:
        """Acquire a stage slot (and a GPU device for GPU stages).

        Returns a :class:`Reservation` that MUST be passed to
        :meth:`release` in a finally block. ``timeout`` is forwarded to
        the stage semaphore via :func:`asyncio.wait_for`; the device
        lock acquire after that is unbounded (a device that's busy
        will be picked up by the round-robin in :meth:`_pick_device`
        as soon as it frees).
        """
        sem = self._stage_sems.get(stage)
        if sem is None:
            raise ValueError(f"stage {stage.value!r} not configured (cap=0)")

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
            sem.release()
            raise

    async def release(self, r: Reservation) -> None:
        if r.device is not None:
            lock = self._device_locks.get(r.device)
            if lock is not None and lock.locked():
                lock.release()
        sem = self._stage_sems.get(r.stage)
        if sem is not None:
            sem.release()

    # ---- device selection -------------------------------------------------

    async def _pick_device(self) -> DeviceID:
        """Acquire the lock on the least-busy healthy device.

        Strategy: try-acquire each healthy device in turn; the first
        that succeeds wins. If all are busy, await the first one
        (FIFO fairness via :class:`asyncio.Lock`). When every device
        is in cooldown, fall back to any device — graceful degradation
        beats refusing to do work at all.
        """
        now = time.time()
        healthy = [d for d in self._devices if self._device_health[d] <= now]
        if not healthy:
            healthy = list(self._devices)

        # Non-blocking pass first.
        for d in healthy:
            lock = self._device_locks[d]
            if not lock.locked():
                await lock.acquire()
                return d

        # All locked — wait on the first one.
        d = healthy[0]
        await self._device_locks[d].acquire()
        return d

    def mark_device_unhealthy(
        self,
        device: DeviceID,
        *,
        recheck_sec: float = 300.0,
    ) -> None:
        """Skip ``device`` for ``recheck_sec`` after a driver crash / OOM.

        The stage handler typically calls this after catching a runtime
        error from the GPU backend, then re-raises the failure as a
        retryable :class:`StageError` so the queue layer re-tries the
        job after the configured backoff.
        """
        if device not in self._device_health:
            _log.warning("mark_device_unhealthy_unknown", device=device)
            return
        self._device_health[device] = time.time() + recheck_sec
        _log.warning(
            "device_marked_unhealthy",
            device=device,
            recheck_sec=recheck_sec,
        )
