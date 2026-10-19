"""The ``metrics`` frame (design-agent.md section 5.2), collected every 2 s.

``{t, ts, system, gpu, process, pipeline, llm, health, files}`` (``health``: what the
worker's health sampler measures — NAS latency, CPU, memory, the registered counters,
the network probes — which no longer goes to the database). Collection runs in a worker
thread (psutil and NVML calls are blocking); only local disks are measured (a network
mount can hang). MLX memory is read only when this process already loaded the
transcriber (:mod:`maktaba_pipeline.stt.batched` in ``sys.modules``): a process that does
not transcribe never imports MLX for its metrics.
"""

from __future__ import annotations

import contextlib
import sys
import threading
import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

import psutil

from . import jobs as job_events

__all__ = ["MetricsCollector", "PipelineView"]

#: file systems never measured (network mounts can hang a stat; pseudo file systems)
_SKIP_FSTYPES = frozenset(
    {
        "nfs",
        "nfs4",
        "smbfs",
        "cifs",
        "smb3",
        "afpfs",
        "webdav",
        "fuse.sshfs",
        "autofs",
        "devfs",
        "tmpfs",
        "squashfs",
        "overlay",
        "nullfs",
    }
)


def _iso_now() -> str:
    return datetime.now(UTC).isoformat(timespec="seconds").replace("+00:00", "Z")


@dataclass
class PipelineView:
    """What the metrics frame reads from the running worker (callables: read live)."""

    role: str = "home"
    stages: Callable[[], tuple[str, ...]] = tuple
    stages_off: Callable[[], Mapping[str, str]] = dict
    extra: dict[str, Any] = field(default_factory=dict)


class _Nvidia:
    """NVML when present (``pynvml`` / ``nvidia-ml-py``); off for good after a failure."""

    def __init__(self) -> None:
        self._nv: Any = None
        self._tried = False

    def read(self) -> list[dict[str, Any]]:
        if not self._tried:
            self._tried = True
            try:
                import pynvml  # type: ignore[import-not-found,import-untyped,unused-ignore]

                pynvml.nvmlInit()
                self._nv = pynvml
            except Exception:  # noqa: BLE001 - no NVIDIA here
                self._nv = None
        nv = self._nv
        if nv is None:
            return []
        out: list[dict[str, Any]] = []
        try:
            for i in range(nv.nvmlDeviceGetCount()):
                h = nv.nvmlDeviceGetHandleByIndex(i)
                name = nv.nvmlDeviceGetName(h)
                util = nv.nvmlDeviceGetUtilizationRates(h)
                mem = nv.nvmlDeviceGetMemoryInfo(h)
                try:
                    temp: int | None = int(nv.nvmlDeviceGetTemperature(h, 0))
                except Exception:  # noqa: BLE001
                    temp = None
                out.append(
                    {
                        "name": name.decode() if isinstance(name, bytes) else str(name),
                        "util_pct": float(util.gpu),
                        "mem_used": int(mem.used),
                        "mem_total": int(mem.total),
                        "temp_c": temp,
                    }
                )
        except Exception:  # noqa: BLE001
            self._nv = None
            return []
        return out


def _transcribers() -> list[Any]:
    mod = sys.modules.get("maktaba_pipeline.stt.batched")
    if mod is None:
        return []
    shared = getattr(mod, "_SHARED", None)
    return list(shared.values()) if isinstance(shared, dict) else []


class MetricsCollector:
    def __init__(
        self,
        view: PipelineView | None = None,
        *,
        llm_stats: Callable[[], Mapping[str, Any]] | None = None,
        files_status: Callable[[], Mapping[str, Any]] | None = None,
        health: Callable[[bool], Mapping[str, Any]] | None = None,
        tracker: job_events.JobTracker | None = None,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self.view = view or PipelineView()
        self.health = health
        self.llm_stats = llm_stats
        self.files_status = files_status
        self.tracker = tracker or job_events.tracker()
        self.clock = clock
        self._proc = psutil.Process()
        self._started = self._proc.create_time()
        self._nvidia = _Nvidia()
        self._lock = threading.Lock()
        self._last_net: tuple[float, int, int] | None = None
        self._last_audio: tuple[float, float] | None = None
        # prime the CPU counters (the first reading of cpu_percent(None) is 0)
        psutil.cpu_percent(None)
        self._proc.cpu_percent(None)

    # -- pieces -------------------------------------------------------------------

    def system(self) -> dict[str, Any]:
        vm = psutil.virtual_memory()
        try:
            load1 = float(psutil.getloadavg()[0])
        except (OSError, AttributeError):
            load1 = None
        disks: list[dict[str, Any]] = []
        seen: set[int] = set()
        try:
            parts = psutil.disk_partitions(all=False)
        except OSError:
            parts = []
        for p in parts:
            if p.fstype.lower() in _SKIP_FSTYPES or "ro" in p.opts.split(","):
                continue
            try:
                u = psutil.disk_usage(p.mountpoint)
            except OSError:
                continue
            if u.total in seen:  # macOS shows one APFS container under several mounts
                continue
            seen.add(u.total)
            disks.append({"path": p.mountpoint, "total": int(u.total), "free": int(u.free)})
        return {
            "cpu_pct": float(psutil.cpu_percent(None)),
            "load1": load1,
            "mem_total": int(vm.total),
            "mem_available": int(vm.available),
            "swap_used": int(psutil.swap_memory().used),
            "disks": disks,
            "net": self._net(),
        }

    def _net(self) -> dict[str, float]:
        try:
            io = psutil.net_io_counters()
        except OSError:
            return {"rx_bps": 0.0, "tx_bps": 0.0}
        now = self.clock()
        last, self._last_net = self._last_net, (now, int(io.bytes_recv), int(io.bytes_sent))
        if last is None or now <= last[0]:
            return {"rx_bps": 0.0, "tx_bps": 0.0}
        dt = now - last[0]
        return {
            "rx_bps": round(max(0, io.bytes_recv - last[1]) / dt, 1),
            "tx_bps": round(max(0, io.bytes_sent - last[2]) / dt, 1),
        }

    def gpu(self) -> dict[str, Any]:
        out: dict[str, Any] = {"nvidia": self._nvidia.read(), "apple": None}
        for t in _transcribers():
            mx = getattr(t, "_mx", None)
            if mx is None:
                continue
            with contextlib.suppress(Exception):
                out["apple"] = {
                    "active_gb": round(mx.get_active_memory() / 1e9, 2),
                    "cache_gb": round(mx.get_cache_memory() / 1e9, 2),
                    "peak_gb": round(mx.get_peak_memory() / 1e9, 2),
                }
            break
        return out

    def process(self) -> dict[str, Any]:
        p = self._proc
        with p.oneshot():
            return {
                "pid": p.pid,
                "rss": int(p.memory_info().rss),
                "cpu_pct": float(p.cpu_percent(None)),
                "threads": int(p.num_threads()),
                "uptime_s": int(time.time() - self._started),
            }

    def stt(self) -> dict[str, Any]:
        ts = _transcribers()
        audio = sum(float(getattr(t, "audio_done", 0.0)) for t in ts)
        files = sum(int(getattr(t, "in_flight", 0)) for t in ts)
        now = self.clock()
        last, self._last_audio = self._last_audio, (now, audio)
        rate = 0.0
        if last is not None and now > last[0]:
            rate = max(0.0, audio - last[1]) / (now - last[0])
        return {"audio_sec_per_s": round(rate, 2), "files_in_flight": files}

    def pipeline(self) -> dict[str, Any]:
        v = self.view
        return {
            "role": v.role,
            "stages": list(v.stages()),
            "stages_off": dict(v.stages_off()),
            "jobs": self.tracker.running(),
            "claims_per_min": self.tracker.claims_per_min(),
            "stt": self.stt(),
            **v.extra,
        }

    # -- the frame ----------------------------------------------------------------------

    def collect(self, final: bool = False) -> dict[str, Any]:
        """One frame; ``final`` (at a graceful stop) takes the health counters' shutdown
        values and is marked ``final: true``."""
        with self._lock:
            frame: dict[str, Any] = {"t": "metrics", "ts": _iso_now()}
            for key, fn in (
                ("system", self.system),
                ("gpu", self.gpu),
                ("process", self.process),
                ("pipeline", self.pipeline),
            ):
                try:
                    frame[key] = fn()
                except Exception as exc:  # noqa: BLE001 - one bad piece keeps the rest
                    frame[key] = {"error": str(exc)[:200]}
            frame["llm"] = dict(self.llm_stats()) if self.llm_stats else None
            if self.health is not None:
                try:
                    frame["health"] = dict(self.health(final))
                except Exception as exc:  # noqa: BLE001
                    frame["health"] = {"error": str(exc)[:200]}
            if self.files_status is not None:
                frame["files"] = dict(self.files_status())
            if final:
                frame["final"] = True
            return frame
