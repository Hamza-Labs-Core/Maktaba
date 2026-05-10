"""Throughput probe (Epic 18 plan-18-04).

Counts operations per second over a sliding window. Used by the
orchestrator's metrics exporter and by the perf-regression test harness
to detect throughput regressions vs the YAML target.
"""

from __future__ import annotations

import time
from collections import deque
from dataclasses import dataclass


@dataclass
class Sample:
    at: float
    count: int


class ThroughputProbe:
    """Rolling counter with a fixed ``window_sec``.

    Call :meth:`record` after each completed batch; :meth:`per_second`
    returns the moving average over the window.
    """

    def __init__(self, window_sec: float = 60.0) -> None:
        if window_sec <= 0:
            raise ValueError("window_sec must be > 0")
        self.window_sec = window_sec
        self._samples: deque[Sample] = deque()
        self._now = time.monotonic

    def record(self, count: int = 1) -> None:
        self._gc()
        self._samples.append(Sample(at=self._now(), count=count))

    def total(self) -> int:
        self._gc()
        return sum(s.count for s in self._samples)

    def per_second(self) -> float:
        self._gc()
        if not self._samples:
            return 0.0
        first = self._samples[0].at
        last = self._samples[-1].at
        span = max(last - first, 1e-6)
        return float(self.total()) / span

    def reset(self) -> None:
        self._samples.clear()

    def _gc(self) -> None:
        cutoff = self._now() - self.window_sec
        while self._samples and self._samples[0].at < cutoff:
            self._samples.popleft()
