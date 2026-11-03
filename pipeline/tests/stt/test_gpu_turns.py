"""Whisper and the second listen on one GPU (:mod:`maktaba_pipeline.stt.gpu_turns`): the
choice from the measured free memory, the turn policy, and the scheduler that never lets
the two models' jobs run at once where they take turns."""

from __future__ import annotations

import asyncio
import sys
from typing import Any

import pytest

from maktaba_pipeline.stt import cohere_cuda, ct2, engine, gpu_turns
from maktaba_pipeline.stt.gpu_turns import (
    COHERE,
    SEQUENTIAL,
    TOGETHER,
    WHISPER,
    GpuTurns,
    Turn,
    decide,
    may_enter,
    next_holder,
)
from maktaba_pipeline.stt.settings import WhisperSettings

pytestmark = pytest.mark.unit

#: Whisper turbo in float16 with a batch of 8 files (GiB)
WHISPER_GB = ct2.gpu_need_gb("float16", 8)
#: Cohere alone on Windows (GiB): its weights, what loading takes under WDDM, a batch of
#: stretches and the margin (cohere_cuda.alone_gb there; less elsewhere)
COHERE_GB = 6.06


# --- the choice: from the free memory, not the operating system ------------------------


def test_the_sizes_are_the_measured_ones() -> None:
    assert pytest.approx(3.67, abs=0.01) == WHISPER_GB
    want = COHERE_GB if sys.platform == "win32" else 4.96  # Linux: a smaller load margin
    assert cohere_cuda.alone_gb() == pytest.approx(want, abs=0.01)


@pytest.mark.parametrize(
    ("free_gb", "want"),
    [
        # the RTX 2080 SUPER on Windows: WDDM leaves the process 6.95 of its 8 GiB
        (6.95, SEQUENTIAL),
        # a 10 GiB card: one at a time at batch 8
        (9.5, SEQUENTIAL),
        # an RTX 3080 Ti (12 GiB) on Windows, a desktop on it
        (10.9, TOGETHER),
        # a 24 GiB card
        (22.5, TOGETHER),
        # a 6 GiB card on Windows: not even Cohere alone
        (5.0, None),
        # exactly enough for Cohere alone
        (COHERE_GB, SEQUENTIAL),
        (WHISPER_GB + COHERE_GB, TOGETHER),
    ],
)
def test_decide(free_gb: float, want: str | None) -> None:
    assert decide(free_gb, WHISPER_GB, COHERE_GB) == want


def test_a_smaller_batch_can_keep_both_on_a_card_in_between() -> None:
    small = ct2.gpu_need_gb("int8_float16", 2)
    assert decide(9.5, WHISPER_GB, COHERE_GB) == SEQUENTIAL
    assert decide(9.5, small, COHERE_GB) == TOGETHER


# --- the policy ------------------------------------------------------------------------


def _turn(holder: Any, running: tuple[int, int], waiting: tuple[int, int], **kw: Any) -> Turn:
    return Turn(
        holder=holder,
        running={WHISPER: running[0], COHERE: running[1]},
        waiting={WHISPER: waiting[0], COHERE: waiting[1]},
        **kw,
    )


def test_nothing_on_the_gpu_goes_to_the_first_model_with_a_job() -> None:
    assert next_holder(_turn(None, (0, 0), (0, 0)), 0, 4) is None
    assert next_holder(_turn(None, (0, 0), (0, 1)), 0, 4) == COHERE
    assert next_holder(_turn(None, (0, 0), (1, 1)), 0, 4) == WHISPER


def test_the_holder_keeps_the_gpu_while_the_other_has_nothing_waiting() -> None:
    t = _turn(WHISPER, (2, 0), (3, 0), admitted=99, started=0.0)
    assert next_holder(t, 10_000, 4) is None
    assert may_enter(t, WHISPER, 10_000, 4)  # no turn to give up
    assert not may_enter(t, COHERE, 10_000, 4)


def test_a_turn_ends_after_its_batch_and_its_running_jobs() -> None:
    t = _turn(WHISPER, (1, 0), (2, 1), admitted=4, started=0.0)
    assert not may_enter(t, WHISPER, 1, 4)  # the batch is let in: Whisper's next waits
    assert next_holder(t, 1, 4) is None  # ...until its running job ends
    t.running[WHISPER] = 0
    assert next_holder(t, 1, 4) == COHERE
    t.admitted = 3  # one more of the batch to go: Whisper keeps it
    assert next_holder(t, 1, 4) is None and may_enter(t, WHISPER, 1, 4)


def test_a_turn_ends_after_its_time() -> None:
    t = _turn(COHERE, (0, 0), (1, 1), admitted=1, started=0.0)
    assert next_holder(t, 10, 100, max_turn_s=60) is None
    assert next_holder(t, 61, 100, max_turn_s=60) == WHISPER
    assert not may_enter(t, COHERE, 61, 100, max_turn_s=60)


def test_a_holder_with_nothing_left_gives_the_gpu_at_once() -> None:
    t = _turn(COHERE, (0, 0), (1, 0), admitted=0, started=0.0)
    assert next_holder(t, 0, 4) == WHISPER


# --- the scheduler ---------------------------------------------------------------------


class FakeGpu:
    """The GPU's two models: which are on it, every move timed by the fake clock."""

    def __init__(self, clock: list[float], swap_s: float = 3.0) -> None:
        self.clock = clock
        self.swap_s = swap_s
        self.on: set[str] = set()
        self.moves: list[tuple[str, str]] = []

    def unload(self, model: str) -> None:
        self.on.discard(model)
        self.moves.append(("off", model))
        self.clock[0] += self.swap_s / 2

    def load(self, model: str) -> None:
        self.on.add(model)
        self.moves.append(("on", model))
        self.clock[0] += self.swap_s / 2


def _turns(gpu: FakeGpu, batch: int, **kw: Any) -> GpuTurns:
    return GpuTurns(
        gpu.unload, gpu.load, batch=batch, clock=lambda: gpu.clock[0], poll_s=0.005, **kw
    )


async def _jobs(turns: GpuTurns, gpu: FakeGpu, plan: list[str], took: float = 0.0) -> list[str]:
    """Run ``plan``'s jobs at once (one task each); returns the order they ran in, each
    checking that its model alone is on the GPU and no job of the other runs."""
    ran: list[str] = []
    live = {WHISPER: 0, COHERE: 0}

    async def job(model: Any) -> None:
        async with turns.turn(model):
            other = COHERE if model == WHISPER else WHISPER
            assert gpu.on == {model}, (model, gpu.on)
            assert live[other] == 0, "both models' jobs ran at once"
            live[model] += 1
            ran.append(model)
            await asyncio.sleep(0.001)
            gpu.clock[0] += took
            live[model] -= 1

    await asyncio.wait_for(asyncio.gather(*(job(m) for m in plan)), timeout=10)
    return ran


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_the_two_models_never_run_at_once_and_each_runs_its_batch() -> None:
    gpu = FakeGpu([0.0])
    turns = _turns(gpu, batch=2)
    ran = await _jobs(turns, gpu, [WHISPER] * 5 + [COHERE] * 3)
    assert sorted(ran) == sorted([WHISPER] * 5 + [COHERE] * 3)
    # Whisper first (its jobs came first), two at a time while the other's wait
    assert ran == [WHISPER, WHISPER, COHERE, COHERE, WHISPER, WHISPER, COHERE, WHISPER]
    assert turns.swaps == 4 and turns.swap_s == [3.0] * 4
    assert gpu.moves[0] == ("on", WHISPER)  # nothing to move off at first


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_a_bigger_batch_means_fewer_swaps() -> None:
    gpu = FakeGpu([0.0])
    turns = _turns(gpu, batch=8)
    await _jobs(turns, gpu, [WHISPER] * 8 + [COHERE] * 8)
    assert turns.swaps == 1


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_one_model_alone_never_swaps() -> None:
    gpu = FakeGpu([0.0])
    turns = _turns(gpu, batch=1)
    await _jobs(turns, gpu, [COHERE] * 6)
    assert turns.swaps == 0 and gpu.moves == [("on", COHERE)]


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_a_long_turn_ends_with_its_time() -> None:
    """A queue of long files, one at a time (a pool of one): the other model's jobs wait
    for no more files once Whisper has held the GPU ``max_turn_s``."""
    gpu = FakeGpu([0.0])
    turns = _turns(gpu, batch=100, max_turn_s=60)
    ran: list[str] = []

    async def whisper_queue() -> None:
        for _ in range(4):
            async with turns.turn(WHISPER):
                ran.append(WHISPER)
                await asyncio.sleep(0.01)
                gpu.clock[0] += 40  # a 40 s file

    async def cohere_job() -> None:
        await asyncio.sleep(0.002)  # queued while Whisper's first file runs
        async with turns.turn(COHERE):
            ran.append(COHERE)

    await asyncio.wait_for(asyncio.gather(whisper_queue(), cohere_job()), timeout=10)
    assert ran.index(COHERE) == 2  # two files of 40 s used up the minute


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_a_failed_job_gives_its_turn_back() -> None:
    gpu = FakeGpu([0.0])
    turns = _turns(gpu, batch=4)
    with pytest.raises(RuntimeError):
        async with turns.turn(WHISPER):
            raise RuntimeError("Whisper failed")
    assert turns.state.running[WHISPER] == 0
    async with turns.turn(COHERE):
        assert gpu.on == {COHERE}


# --- this process's GPU ----------------------------------------------------------------


@pytest.fixture
def fresh(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(gpu_turns, "_MODE", {})
    monkeypatch.setattr(gpu_turns, "_SHARED", [])


def test_apple_silicon_and_a_second_listen_off_share_nothing(
    fresh: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)
    assert gpu_turns.sharing("CohereLabs/x", WhisperSettings()) is None
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    assert gpu_turns.sharing(None, WhisperSettings()) is None


def test_the_choice_is_made_once_and_no_room_turns_it_to_turns(
    fresh: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    asked: list[str] = []
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    monkeypatch.setattr(cohere_cuda, "problem", lambda device=None: None)

    def mode(model: str, settings: WhisperSettings) -> Any:
        asked.append(model)
        return TOGETHER

    monkeypatch.setattr(cohere_cuda, "mode", mode)
    assert gpu_turns.sharing("m", WhisperSettings()) == TOGETHER
    assert gpu_turns.sharing("m", WhisperSettings()) == TOGETHER and asked == ["m"]
    gpu_turns.take_turns("m")  # the second listen found no room beside Whisper
    assert gpu_turns.sharing("m", WhisperSettings()) == SEQUENTIAL


def test_a_driver_that_cannot_say_keeps_both_and_the_room_check_decides(
    fresh: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    monkeypatch.setattr(cohere_cuda, "problem", lambda device=None: None)

    def broken(model: str, settings: WhisperSettings) -> Any:
        raise OSError("NVML: driver not loaded")

    monkeypatch.setattr(cohere_cuda, "mode", broken)
    assert gpu_turns.sharing("m", WhisperSettings()) == TOGETHER


def test_a_machine_without_the_second_listen_shares_nothing(
    fresh: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(engine, "current", lambda: engine.CT2)
    monkeypatch.setattr(cohere_cuda, "problem", lambda device=None: "no NVIDIA GPU")
    assert gpu_turns.sharing("m", WhisperSettings()) is None


@pytest.mark.integration  # an event loop opens a socket pair
@pytest.mark.asyncio
async def test_use_takes_turns_only_where_the_gpu_holds_one_at_a_time(
    fresh: None, monkeypatch: pytest.MonkeyPatch
) -> None:
    moved: list[tuple[str, str]] = []
    monkeypatch.setattr(gpu_turns, "_unload", lambda m: moved.append(("off", m)))
    monkeypatch.setattr(gpu_turns, "_load", lambda m: moved.append(("on", m)))
    mode: dict[str, Any] = {"m": TOGETHER}
    monkeypatch.setattr(gpu_turns, "sharing", lambda model_id, settings: mode["m"])
    async with gpu_turns.use(WHISPER, "m", WhisperSettings()):
        pass
    assert moved == [] and gpu_turns._SHARED == []
    mode["m"] = SEQUENTIAL
    async with gpu_turns.use(WHISPER, "m", WhisperSettings()):
        pass
    async with gpu_turns.use(COHERE, "m", WhisperSettings()):
        pass
    assert moved == [("on", WHISPER), ("off", WHISPER), ("on", COHERE)]


def test_the_swap_moves_whisper_and_cohere_off_and_back(monkeypatch: pytest.MonkeyPatch) -> None:
    from maktaba_pipeline.stt import cohere

    calls: list[str] = []

    class T:
        def release_gpu(self) -> None:
            calls.append("whisper off")

        def restore_gpu(self) -> None:
            calls.append("whisper on")

    class R:
        def offload(self) -> bool:
            calls.append("cohere off")
            return True

        def onload(self) -> bool:
            calls.append("cohere on")
            return True

    monkeypatch.setattr(ct2, "shared_transcribers", lambda: [T()])
    monkeypatch.setattr(cohere, "loaded_recogniser_all", lambda: [R(), object()])
    gpu_turns._unload(WHISPER)
    gpu_turns._load(COHERE)
    gpu_turns._unload(COHERE)
    gpu_turns._load(WHISPER)
    assert calls == ["whisper off", "cohere on", "cohere off", "whisper on"]
