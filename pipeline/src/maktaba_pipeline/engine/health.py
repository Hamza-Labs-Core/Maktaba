"""NAS and host health measurement (plan 06-11 section 6.6).

``ctx.nas.timed(path)`` wraps the ffmpeg / ffprobe call of every ``nas_read``
stage: before the subprocess it times ``stat + open + a 64 KiB read`` of the
file, which is the cheapest honest measure of how the share is doing. A
subprocess whose stderr names a ``nas_*`` cause is counted as a timeout.

Where the numbers go (design-agent.md section 7.9): a worker never writes them to the
database. Its agent's metrics frame carries them as the flat ``health`` map
(:func:`health_frame`: the names ``health_samples`` holds, the network probes every ~60 s
through :func:`run_network_probes`), and the home's hub keeps ``health_samples`` as a
cache (it counts ``nas.reads_busy`` itself). A worker without an agent
(``MAKTABA_CLUSTER_AGENT=0``) records no health numbers.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import time
from collections import deque
from collections.abc import AsyncIterator, Awaitable, Callable

from ..log import get_logger
from .causes import classify

__all__ = [
    "HealthCollector",
    "NasProbe",
    "health_frame",
    "measure",
    "measure_now",
    "register_metric",
    "registered_metrics",
    "run_network_probes",
    "system_memory",
]

_log = get_logger()

_WINDOW_SEC = 10.0
_READ_BYTES = 64 * 1024

ProbeFn = Callable[[], Awaitable[float]]

#: metrics a subsystem of this process publishes (the transcriber's GPU memory ...),
#: sampled with the rest every round
_METRICS: dict[str, Callable[[], float]] = {}
_FINAL: dict[str, Callable[[], float]] = {}


def register_metric(
    name: str, fn: Callable[[], float], *, final: Callable[[], float] | None = None
) -> None:
    """Sample ``fn`` every round; ``final``, when given, instead in the last round, taken
    as the worker shuts down."""
    _METRICS[name] = fn
    if final is not None:
        _FINAL[name] = final


def system_memory() -> tuple[float, float] | None:
    """(total bytes, available bytes) of this host, or None where unknown.

    macOS: free + inactive + speculative + purgeable pages (what can be had without
    swapping or compressing); Linux: MemAvailable."""
    try:
        if os.path.exists("/proc/meminfo"):
            with open("/proc/meminfo") as f:
                info = {
                    k: int(v.split()[0]) * 1024
                    for k, v in (ln.split(":", 1) for ln in f)
                    if v.strip()
                }
            return float(info["MemTotal"]), float(info["MemAvailable"])
        import subprocess

        total = int(subprocess.run(["sysctl", "-n", "hw.memsize"], capture_output=True,
                                   text=True, timeout=5).stdout)  # fmt: skip
        out = subprocess.run(["vm_stat"], capture_output=True, text=True, timeout=5).stdout
        page = int(out.split("page size of", 1)[1].split()[0])
        pages = {
            ln.split(":")[0].strip(): int(ln.split(":")[1].strip().rstrip("."))
            for ln in out.splitlines()[1:]
            if ":" in ln and ln.split(":")[1].strip().rstrip(".").isdigit()
        }
        avail = sum(
            pages.get(k, 0)
            for k in ("Pages free", "Pages inactive", "Pages speculative", "Pages purgeable")
        )
        return float(total), float(avail * page)
    except Exception:  # noqa: BLE001 - a missing measure is no sample, never a crash
        return None


class HealthCollector:
    """Rolling window of NAS open latencies and timeout events."""

    def __init__(self, *, clock: Callable[[], float] = time.monotonic) -> None:
        self._clock = clock
        self._opens: deque[tuple[float, float]] = deque()
        self._timeouts: deque[float] = deque()
        self.probes: dict[str, ProbeFn] = {}
        #: network probe results waiting for the next metrics frame (agent mode)
        self._network: dict[str, float] = {}

    def put_network(self, results: dict[str, float]) -> None:
        self._network = dict(results)

    def take_network(self) -> dict[str, float]:
        out, self._network = self._network, {}
        return out

    def record_open(self, ms: float) -> None:
        self._opens.append((self._clock(), ms))

    def record_timeout(self) -> None:
        self._timeouts.append(self._clock())

    def _trim(self) -> None:
        cut = self._clock() - _WINDOW_SEC
        while self._opens and self._opens[0][0] < cut:
            self._opens.popleft()
        while self._timeouts and self._timeouts[0] < cut:
            self._timeouts.popleft()

    def open_percentiles(self) -> tuple[float, float] | None:
        """``(p50, p95)`` of the last window, or None when nothing was measured."""
        self._trim()
        if not self._opens:
            return None
        xs = sorted(ms for _, ms in self._opens)
        return xs[len(xs) // 2], xs[min(len(xs) - 1, int(len(xs) * 0.95))]

    def timeouts(self) -> int:
        self._trim()
        return len(self._timeouts)


class NasProbe:
    """The ``ctx.nas`` handle: measure the share around each read."""

    def __init__(self, collector: HealthCollector | None = None) -> None:
        self.collector = collector or HealthCollector()

    @contextlib.asynccontextmanager
    async def timed(self, path: str) -> AsyncIterator[None]:
        """Time ``stat + open + 64 KiB`` of ``path`` (``nas.open_ms``), then yield."""
        t0 = time.perf_counter()
        try:
            await asyncio.to_thread(_touch, path)
        except OSError as exc:
            if classify("os", str(exc)) in ("nas_timeout", "nas_invalid_argument"):
                self.collector.record_timeout()
        else:
            self.collector.record_open((time.perf_counter() - t0) * 1000.0)
        yield

    def record_stderr(self, stderr: str) -> bool:
        """Count a NAS failure seen in a subprocess's stderr; True when counted."""
        cause = classify("ffmpeg", stderr)
        if cause in ("nas_timeout", "nas_invalid_argument", "nas_partial_read"):
            self.collector.record_timeout()
            return True
        return False


def _touch(path: str) -> None:
    os.stat(path)
    with open(path, "rb") as fh:
        fh.read(_READ_BYTES)


def _cpu_percent() -> float | None:
    try:
        import psutil  # type: ignore[import-not-found,import-untyped,unused-ignore]  # optional dep; stubs may be absent

        return float(psutil.cpu_percent(interval=None))
    except ImportError:
        pass
    getloadavg = getattr(os, "getloadavg", None)
    if getloadavg is None:
        return None  # Windows without psutil: omitted
    return float(getloadavg()[0] / (os.cpu_count() or 1) * 100.0)


def registered_metrics(*, final: bool = False) -> dict[str, float]:
    """Every :func:`register_metric` value now (``final``: the shutdown values)."""
    out: dict[str, float] = {}
    for metric, read in list(_METRICS.items()):
        if final:
            read = _FINAL.get(metric, read)
        try:
            out[metric] = float(read())
        except Exception:  # noqa: BLE001 - one bad probe is skipped
            _log.debug("metric_probe_failed", metric=metric)
    return out


def measure_now(collector: HealthCollector, *, final: bool = False) -> dict[str, float]:
    """The sampler's numbers that need no network: NAS latency and timeouts, CPU, memory,
    every registered metric (``final``: their shutdown readers). No database."""
    metrics: dict[str, float] = {}
    pct = collector.open_percentiles()
    if pct is not None:
        metrics["nas.open_ms_p50"], metrics["nas.open_ms_p95"] = pct
    metrics["nas.timeouts"] = float(collector.timeouts())
    cpu = _cpu_percent()
    if cpu is not None:
        metrics["cpu.pct"] = cpu
    mem = system_memory()
    if mem is not None:
        total, avail = mem
        metrics["mem.free_pct"] = round(100.0 * avail / total, 1) if total else 0.0
        metrics["mem.available_gb"] = round(avail / 1e9, 2)
    metrics.update(registered_metrics(final=final))
    return metrics


async def measure(
    collector: HealthCollector, *, probe_network: bool = False, final: bool = False
) -> dict[str, float]:
    """One round of the sampler's numbers (no database): :func:`measure_now` plus, with
    ``probe_network``, the network probes (an unreachable service is -1)."""
    metrics = measure_now(collector, final=final)
    if probe_network:
        metrics.update(await _probe_network(collector))
    return metrics


def health_frame(collector: HealthCollector, *, final: bool = False) -> dict[str, float]:
    """The metrics frame's ``health``: the flat map of ``health_samples`` names, without
    ``nas.reads_busy`` (the hub counts that itself); the network probes' results only in
    the first frame after they were measured (~every 60 s, :func:`run_network_probes`)."""
    metrics = measure_now(collector, final=final)
    metrics.update(collector.take_network())
    return metrics


async def _probe_network(collector: HealthCollector) -> dict[str, float]:
    out: dict[str, float] = {}
    for metric, fn in collector.probes.items():
        try:
            out[metric] = float(await fn())
        except Exception:  # noqa: BLE001 - an unreachable service is a -1 sample, not a crash
            out[metric] = -1.0
    return out


async def run_network_probes(
    collector: HealthCollector, stop: asyncio.Event, *, interval_sec: float = 60.0
) -> None:
    """The agent's mode (no ``health_samples``): the network probes every
    ``interval_sec`` (the first at once), their results handed to the next metrics frame."""
    while not stop.is_set():
        if collector.probes:
            try:
                collector.put_network(await _probe_network(collector))
            except Exception:  # noqa: BLE001 - health must never take the worker down
                _log.exception("health_probe_failed")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=interval_sec)
