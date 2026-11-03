"""Whisper and the second listen on one NVIDIA GPU: together, or taking turns.

Whisper (``transcribe``, :mod:`.ct2`) and Cohere (``second_listen``, :mod:`.cohere_cuda`)
are separate stages, but on a Windows or Linux machine the same worker process runs both
on the same GPU. Where the GPU holds both at once they stay loaded side by side
(:data:`TOGETHER`). Where it holds only one at a time (an 8 GB card on Windows: the
driver keeps about 1 GiB of the card, and with Whisper loaded 5.8 GiB is left against
the 6.4 Cohere needs), they take turns (:data:`SEQUENTIAL`): :class:`GpuTurns` lets one
model's jobs run while the other's wait, moves the idle model's weights off the GPU
(CTranslate2's ``unload_model(to_cpu=True)``, torch's ``.to("cpu")``, then a CUDA cache
clear) and the other's back on, and keeps each model on the GPU for a batch of files so
that swaps are rare.

The choice (:func:`decide`) comes from the GPU's measured free memory (NVML, as the
capability report reads it, plus what this process itself already holds there), not from
the operating system: a bigger card, or Linux's smaller driver overhead, keeps both
loaded. The sizes are the measured ones of :mod:`.ct2` (Whisper) and :mod:`.cohere_cuda`.
"""

from __future__ import annotations

import asyncio
import contextlib
import time
from collections.abc import AsyncIterator, Callable
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from typing import Final, Literal

from ..log import get_logger
from .settings import WhisperSettings

__all__ = [
    "COHERE",
    "MAX_TURN_S",
    "SEQUENTIAL",
    "TOGETHER",
    "WHISPER",
    "GpuTurns",
    "Mode",
    "Turn",
    "decide",
    "next_holder",
    "shared",
    "sharing",
    "take_turns",
    "use",
]

_log = get_logger()

Model = Literal["whisper", "cohere"]
WHISPER: Final = "whisper"
COHERE: Final = "cohere"

Mode = Literal["together", "sequential"]
#: both models stay on the GPU
TOGETHER: Final = "together"
#: one model on the GPU at a time
SEQUENTIAL: Final = "sequential"

#: the longest one model keeps the GPU while the other's jobs wait (s): a turn ends
#: sooner once it has run its batch, or once its jobs are done
MAX_TURN_S = 1800.0


def decide(free_gb: float, whisper_gb: float, cohere_gb: float) -> Mode | None:
    """How Whisper and the second listen share a GPU with ``free_gb`` GiB free for this
    process (measured free memory plus what the process holds there already), Whisper
    needing ``whisper_gb`` (its model and batch) and Cohere ``cohere_gb`` (its weights,
    what loading takes, a batch of stretches): both loaded, one at a time, or None when
    the GPU cannot hold even the larger of the two."""
    if free_gb >= whisper_gb + cohere_gb:
        return TOGETHER
    if free_gb >= max(whisper_gb, cohere_gb):
        return SEQUENTIAL
    return None


@dataclass
class Turn:
    """What the scheduler knows at one moment: the model on the GPU, each model's jobs
    running and waiting, and the turn's start and its count of jobs let in."""

    holder: Model | None = None
    running: dict[str, int] = field(default_factory=lambda: {WHISPER: 0, COHERE: 0})
    waiting: dict[str, int] = field(default_factory=lambda: {WHISPER: 0, COHERE: 0})
    started: float = 0.0
    admitted: int = 0


def _other(model: str) -> Model:
    return COHERE if model == WHISPER else WHISPER


def next_holder(t: Turn, now: float, batch: int, max_turn_s: float = MAX_TURN_S) -> Model | None:
    """Which model should hold the GPU now (None: no change). The holder keeps it while it
    has jobs and the other model has none waiting. With the other's jobs waiting, the
    holder's turn ends once it has let in ``batch`` jobs, or has held the GPU for
    ``max_turn_s``, or has nothing left to run; then, once its running jobs finish, the
    GPU goes to the other model. Nothing on the GPU: the first model with a job."""
    if t.holder is None:
        order: tuple[Model, Model] = (WHISPER, COHERE)
        for m in order:
            if t.waiting[m]:
                return m
        return None
    other = _other(t.holder)
    if not t.waiting[other] or t.running[t.holder]:
        return None
    if t.waiting[t.holder] and t.admitted < batch and now - t.started < max_turn_s:
        return None
    return other


def may_enter(t: Turn, model: str, now: float, batch: int, max_turn_s: float = MAX_TURN_S) -> bool:
    """Whether a job of ``model`` may start on the GPU now: its model holds it and its turn
    has not ended (the other model waiting, and the batch or the time used up)."""
    if t.holder != model:
        return False
    if not t.waiting[_other(model)]:
        return True
    return t.admitted < batch and now - t.started < max_turn_s


class GpuTurns:
    """Turns on one GPU for this process's Whisper and Cohere jobs (asyncio: the worker's
    event loop). ``unload(model)`` moves a model off the GPU and ``load(model)`` back on
    (both may be blocking: they run in a thread); ``batch`` is how many jobs of a model a
    turn lets in while the other's wait (the worker's batch). Every swap is timed
    (:attr:`swap_s`) and logged (``gpu.swap``)."""

    def __init__(
        self,
        unload: Callable[[str], object],
        load: Callable[[str], object],
        *,
        batch: int,
        max_turn_s: float = MAX_TURN_S,
        clock: Callable[[], float] = time.monotonic,
        poll_s: float = 1.0,
    ) -> None:
        self._unload = unload
        self._load = load
        self.batch = max(1, batch)
        self.max_turn_s = max_turn_s
        self._clock = clock
        self._poll_s = poll_s
        self.state = Turn()
        self._cond: asyncio.Condition | None = None
        self._swapping = False
        #: swaps made, and the seconds each took (unload plus load)
        self.swaps = 0
        self.swap_s: list[float] = []

    def _c(self) -> asyncio.Condition:
        if self._cond is None:
            self._cond = asyncio.Condition()
        return self._cond

    @asynccontextmanager
    async def turn(self, model: Model) -> AsyncIterator[None]:
        """Run the block while ``model`` holds the GPU."""
        await self._enter(model)
        try:
            yield
        finally:
            await self._leave(model)

    async def _enter(self, model: Model) -> None:
        t = self.state
        cond = self._c()
        async with cond:
            t.waiting[model] += 1
            try:
                while True:
                    now = self._clock()
                    if not self._swapping:
                        to = next_holder(t, now, self.batch, self.max_turn_s)
                        if to is not None:
                            await self._swap(to)
                            continue
                        if may_enter(t, model, now, self.batch, self.max_turn_s):
                            break
                    # a turn can end with time alone
                    with contextlib.suppress(TimeoutError):
                        await asyncio.wait_for(cond.wait(), timeout=self._poll_s)
            finally:
                t.waiting[model] -= 1
            t.running[model] += 1
            t.admitted += 1

    async def _leave(self, model: Model) -> None:
        cond = self._c()
        async with cond:
            self.state.running[model] -= 1
            cond.notify_all()

    async def _swap(self, to: Model) -> None:
        """Called holding the condition's lock: give the GPU to ``to``. The lock is let go
        while the weights move, and taken again."""
        t = self.state
        cond = self._c()
        old = t.holder
        self._swapping = True
        t0 = self._clock()
        cond.release()
        try:
            if old is not None:
                await _call(self._unload, old)
            await _call(self._load, to)
        finally:
            await cond.acquire()
            self._swapping = False
        t.holder = to
        t.started = self._clock()
        t.admitted = 0
        if old is not None:
            took = t.started - t0
            self.swaps += 1
            self.swap_s.append(round(took, 2))
            _log.info("gpu.swap", frm=old, to=to, seconds=round(took, 2), swaps=self.swaps)
        cond.notify_all()


async def _call(fn: Callable[[str], object], model: str) -> None:
    await asyncio.to_thread(fn, model)


# --- this process's GPU -----------------------------------------------------------------

#: how this process shares its GPU, once decided (:func:`sharing`): model -> mode
_MODE: dict[str, Mode | None] = {}
_SHARED: list[GpuTurns] = []


def sharing(model_id: str | None, settings: WhisperSettings) -> Mode | None:
    """How this process's Whisper and second listen (``model_id``; None: off) share the
    GPU: ``together``, ``sequential``, or None where there is nothing to share (Apple
    Silicon's unified memory, no second listen here, no NVIDIA GPU). Decided once, from
    the measured free memory (:func:`.cohere_cuda.mode`); :func:`take_turns` changes it."""
    from . import engine

    if model_id is None or engine.current() == engine.MLX:
        return None
    if model_id not in _MODE:
        from . import cohere_cuda

        if cohere_cuda.problem(settings.device) is not None:
            return None
        try:
            got = cohere_cuda.mode(model_id, settings)
        except Exception:  # noqa: BLE001 - the driver's answer: the room check decides
            _log.warning("gpu.mode_unknown", exc_info=True)
            got = TOGETHER
        _MODE[model_id] = got
        _log.info("gpu.sharing", model=model_id, mode=got)
    return _MODE[model_id]


def take_turns(model_id: str) -> None:
    """From now on this process's Whisper and second listen take turns on the GPU: the
    second listen found no room beside Whisper after all (another program holds part of
    the GPU)."""
    if _MODE.get(model_id) != SEQUENTIAL:
        _MODE[model_id] = SEQUENTIAL
        _log.warning("gpu.sharing", model=model_id, mode=SEQUENTIAL, why="no room beside Whisper")


def shared(batch: int) -> GpuTurns:
    """The process's turns (one GPU per worker process: ``MAKTABA_STT_DEVICE``)."""
    if not _SHARED:
        _SHARED.append(GpuTurns(_unload, _load, batch=batch))
    return _SHARED[0]


@asynccontextmanager
async def use(model: Model, model_id: str | None, settings: WhisperSettings) -> AsyncIterator[None]:
    """Run the block with ``model`` on the GPU: at once where the two share it or nothing
    is shared, else in ``model``'s turn."""
    if sharing(model_id, settings) != SEQUENTIAL:
        yield
        return
    async with shared(settings.max_batch).turn(model):
        yield


def _unload(model: str) -> None:
    """Move ``model``'s weights off the GPU (in a thread)."""
    if model == WHISPER:
        from .ct2 import shared_transcribers

        for t in shared_transcribers():
            t.release_gpu()
        return
    from .cohere import loaded_recogniser_all

    for rec in loaded_recogniser_all():
        offload = getattr(rec, "offload", None)
        if callable(offload):
            offload()


def _load(model: str) -> None:
    """Put ``model``'s weights back on the GPU (in a thread); one never loaded loads on
    its first use."""
    if model == WHISPER:
        from .ct2 import shared_transcribers

        for t in shared_transcribers():
            t.restore_gpu()
        return
    from .cohere import loaded_recogniser_all

    for rec in loaded_recogniser_all():
        onload = getattr(rec, "onload", None)
        if callable(onload):
            onload()
