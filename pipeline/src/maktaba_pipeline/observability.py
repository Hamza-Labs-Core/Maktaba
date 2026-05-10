"""Pipeline observability hooks (Story 6.9).

Two surfaces:

1. ``log_event`` — a structlog-shaped helper that bakes in the field
   contract every state-changing event must carry
   (``job_id, video_id, stage, state, attempts``). State-transition
   helpers in :mod:`maktaba_pipeline.db.jobs_state` and the runner
   call this exactly once per transition.

2. An in-process metric registry that emits the four metric series
   the story specifies in plain Prometheus text format, no
   ``prometheus_client`` runtime dep:

   - ``maktaba_jobs_total{stage,state}`` — gauge maintained by the
     state-transition helpers and the claim loop.
   - ``maktaba_job_attempts_total{stage,outcome}`` — counter
     incremented on every attempt outcome.
   - ``maktaba_job_duration_seconds{stage,outcome}`` — histogram of
     wall-clock claim→terminal-or-paused durations.
   - ``maktaba_job_realtime_factor{stage}`` — summary of the audio-
     seconds-per-wall-second ratio reported by ``tick_progress``.

The ``render_prometheus_text`` function emits the standard
text/plain Prometheus exposition format. A tiny HTTP shim (left to
the API service) can mount the renderer at ``/metrics`` directly.

Cardinality discipline: labels are restricted to the canonical sets
above. ``video_id`` and ``library_id`` belong in logs and the DB,
never in metrics.
"""

from __future__ import annotations

import threading
import time
from collections.abc import Mapping
from dataclasses import dataclass, field
from enum import StrEnum
from typing import Any

import structlog

__all__ = [
    "DEFAULT_HISTOGRAM_BUCKETS",
    "JOB_ATTEMPT_OUTCOMES",
    "JOB_STAGES",
    "JOB_STATES",
    "Counter",
    "Gauge",
    "Histogram",
    "JobAttemptOutcome",
    "MetricRegistry",
    "Summary",
    "default_registry",
    "log_event",
    "render_prometheus_text",
    "reset_default_registry",
]


# ---------------------------------------------------------------------------
# Canonical label sets
# ---------------------------------------------------------------------------

#: The seven pipeline stages (architecture §7.4).
JOB_STAGES: tuple[str, ...] = (
    "scan",
    "probe",
    "extract",
    "transcribe",
    "subtitle_gen",
    "index",
    "thumbnail",
)

#: The eight job states (architecture §7.2).
JOB_STATES: tuple[str, ...] = (
    "pending",
    "claimed",
    "running",
    "resuming",
    "paused",
    "done",
    "failed",
    "cancelled",
)


class JobAttemptOutcome(StrEnum):
    """Outcomes recorded by ``maktaba_job_attempts_total``."""

    CLAIMED = "claimed"
    DONE = "done"
    FAILED = "failed"
    RETRY = "retry"
    PAUSED = "paused"
    CANCELLED = "cancelled"


JOB_ATTEMPT_OUTCOMES: tuple[str, ...] = tuple(JobAttemptOutcome)


#: Histogram bucket boundaries in seconds — covers fast probes
#: (sub-second) through long transcribes (multiple hours).
DEFAULT_HISTOGRAM_BUCKETS: tuple[float, ...] = (
    0.5,
    1,
    5,
    30,
    60,
    300,
    600,
    1_800,
    3_600,
    7_200,
    14_400,
    28_800,
)


# ---------------------------------------------------------------------------
# Metric primitives — pure Python, no runtime dep
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class Counter:
    """Monotonic counter keyed by an arbitrary tuple of label values.

    Threading: increments hold a single lock so one ``inc()`` call
    can race with another safely. Reads in ``samples()`` snapshot the
    dict under the same lock.
    """

    name: str
    help: str
    label_names: tuple[str, ...]
    _values: dict[tuple[str, ...], float] = field(default_factory=dict)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def inc(self, *, amount: float = 1.0, **labels: str) -> None:
        key = self._key(labels)
        with self._lock:
            self._values[key] = self._values.get(key, 0.0) + amount

    def value(self, **labels: str) -> float:
        key = self._key(labels)
        with self._lock:
            return self._values.get(key, 0.0)

    def samples(self) -> list[tuple[tuple[str, ...], float]]:
        with self._lock:
            return [(k, v) for k, v in self._values.items()]

    def _key(self, labels: Mapping[str, str]) -> tuple[str, ...]:
        return tuple(str(labels[name]) for name in self.label_names)


@dataclass(slots=True)
class Gauge:
    """Set/inc/dec metric. Same threading guarantees as :class:`Counter`."""

    name: str
    help: str
    label_names: tuple[str, ...]
    _values: dict[tuple[str, ...], float] = field(default_factory=dict)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def set(self, value: float, **labels: str) -> None:
        key = self._key(labels)
        with self._lock:
            self._values[key] = float(value)

    def inc(self, *, amount: float = 1.0, **labels: str) -> None:
        key = self._key(labels)
        with self._lock:
            self._values[key] = self._values.get(key, 0.0) + amount

    def dec(self, *, amount: float = 1.0, **labels: str) -> None:
        self.inc(amount=-amount, **labels)

    def value(self, **labels: str) -> float:
        key = self._key(labels)
        with self._lock:
            return self._values.get(key, 0.0)

    def samples(self) -> list[tuple[tuple[str, ...], float]]:
        with self._lock:
            return [(k, v) for k, v in self._values.items()]

    def _key(self, labels: Mapping[str, str]) -> tuple[str, ...]:
        return tuple(str(labels[name]) for name in self.label_names)


@dataclass(slots=True)
class Histogram:
    """Cumulative histogram with the canonical Prometheus bucket model.

    Each ``observe(v, labels=...)`` increments every bucket whose
    upper bound is >= v, plus the ``+Inf`` bucket and the ``_sum``
    counter. The text renderer emits one ``_bucket`` line per
    boundary plus the inclusive ``_sum`` and ``_count`` lines.
    """

    name: str
    help: str
    label_names: tuple[str, ...]
    buckets: tuple[float, ...] = DEFAULT_HISTOGRAM_BUCKETS
    _bucket_counts: dict[tuple[str, ...], list[float]] = field(default_factory=dict)
    _sums: dict[tuple[str, ...], float] = field(default_factory=dict)
    _counts: dict[tuple[str, ...], int] = field(default_factory=dict)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def observe(self, value: float, **labels: str) -> None:
        key = self._key(labels)
        with self._lock:
            counts = self._bucket_counts.setdefault(key, [0.0] * (len(self.buckets) + 1))
            for i, bound in enumerate(self.buckets):
                if value <= bound:
                    counts[i] += 1
            counts[-1] += 1  # +Inf bucket
            self._sums[key] = self._sums.get(key, 0.0) + value
            self._counts[key] = self._counts.get(key, 0) + 1

    def count(self, **labels: str) -> int:
        with self._lock:
            return self._counts.get(self._key(labels), 0)

    def sum(self, **labels: str) -> float:
        with self._lock:
            return self._sums.get(self._key(labels), 0.0)

    def samples(self) -> list[tuple[tuple[str, ...], list[float], float, int]]:
        with self._lock:
            return [
                (k, list(self._bucket_counts[k]), self._sums[k], self._counts[k])
                for k in self._bucket_counts
            ]

    def _key(self, labels: Mapping[str, str]) -> tuple[str, ...]:
        return tuple(str(labels[name]) for name in self.label_names)


@dataclass(slots=True)
class Summary:
    """Sum + count summary metric (no quantile estimation).

    The story spec lists ``maktaba_job_realtime_factor`` as a summary;
    we ship sum and count only because Prometheus's quantile
    estimation is a query-side concern when the scrape carries the
    full distribution-over-time. Computing quantiles client-side
    would be wrong (you can't aggregate per-quantile samples
    correctly).
    """

    name: str
    help: str
    label_names: tuple[str, ...]
    _sums: dict[tuple[str, ...], float] = field(default_factory=dict)
    _counts: dict[tuple[str, ...], int] = field(default_factory=dict)
    _lock: threading.Lock = field(default_factory=threading.Lock)

    def observe(self, value: float, **labels: str) -> None:
        key = self._key(labels)
        with self._lock:
            self._sums[key] = self._sums.get(key, 0.0) + value
            self._counts[key] = self._counts.get(key, 0) + 1

    def count(self, **labels: str) -> int:
        with self._lock:
            return self._counts.get(self._key(labels), 0)

    def sum(self, **labels: str) -> float:
        with self._lock:
            return self._sums.get(self._key(labels), 0.0)

    def samples(self) -> list[tuple[tuple[str, ...], float, int]]:
        with self._lock:
            return [(k, self._sums[k], self._counts[k]) for k in self._sums]

    def _key(self, labels: Mapping[str, str]) -> tuple[str, ...]:
        return tuple(str(labels[name]) for name in self.label_names)


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------


class MetricRegistry:
    """Holds the canonical Maktaba metric collectors.

    A fresh registry is created via :func:`default_registry` per
    process. Tests can construct their own registries to keep the
    global state pristine; :func:`reset_default_registry` does the
    same for the module-level singleton.
    """

    def __init__(self) -> None:
        self.jobs_total = Gauge(
            name="maktaba_jobs_total",
            help="Number of processing jobs by stage and state.",
            label_names=("stage", "state"),
        )
        self.job_attempts_total = Counter(
            name="maktaba_job_attempts_total",
            help="Number of job attempt outcomes by stage.",
            label_names=("stage", "outcome"),
        )
        self.job_duration_seconds = Histogram(
            name="maktaba_job_duration_seconds",
            help="Wall-clock duration of a job from claim to terminal-or-paused.",
            label_names=("stage", "outcome"),
        )
        self.job_realtime_factor = Summary(
            name="maktaba_job_realtime_factor",
            help="Audio-seconds processed per wall-second.",
            label_names=("stage",),
        )

    def record_attempt(
        self,
        *,
        stage: str,
        outcome: str,
        duration_sec: float | None = None,
    ) -> None:
        """Increment the attempts counter and (if given) the duration histogram.

        ``outcome`` should come from :class:`JobAttemptOutcome`. The
        story's edge case "long-failing job creating noisy retry logs"
        is debounced upstream by :func:`log_event`'s WARN/DEBUG split;
        the metric counter is unconditional so retry storms remain
        visible in Prometheus even when log noise is dampened.
        """
        self.job_attempts_total.inc(stage=stage, outcome=outcome)
        if duration_sec is not None:
            self.job_duration_seconds.observe(duration_sec, stage=stage, outcome=outcome)

    def record_progress(self, *, stage: str, realtime_factor: float) -> None:
        self.job_realtime_factor.observe(realtime_factor, stage=stage)

    def set_jobs_count(self, *, stage: str, state: str, n: int) -> None:
        self.jobs_total.set(float(n), stage=stage, state=state)


_default: MetricRegistry | None = None
_default_lock = threading.Lock()


def default_registry() -> MetricRegistry:
    """Return the process-wide registry, creating it lazily."""
    global _default
    with _default_lock:
        if _default is None:
            _default = MetricRegistry()
        return _default


def reset_default_registry() -> MetricRegistry:
    """Replace the process-wide registry with a fresh one (test seam)."""
    global _default
    with _default_lock:
        _default = MetricRegistry()
        return _default


# ---------------------------------------------------------------------------
# Prometheus text exposition
# ---------------------------------------------------------------------------


def render_prometheus_text(registry: MetricRegistry | None = None) -> str:
    """Render every metric in the registry as Prometheus text format.

    The format is the documented stable text encoding (Prometheus
    docs §"Text-based format"), so a normal ``promhttp`` scraper
    reads it without configuration.
    """
    reg = registry or default_registry()
    parts: list[str] = []

    parts.append(_render_gauge(reg.jobs_total))
    parts.append(_render_counter(reg.job_attempts_total))
    parts.append(_render_histogram(reg.job_duration_seconds))
    parts.append(_render_summary(reg.job_realtime_factor))

    return "\n".join(p for p in parts if p) + "\n"


def _format_labels(label_names: tuple[str, ...], values: tuple[str, ...]) -> str:
    if not label_names:
        return ""
    pairs = ",".join(
        f'{name}="{_escape(val)}"' for name, val in zip(label_names, values, strict=True)
    )
    return "{" + pairs + "}"


def _escape(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"').replace("\n", "\\n")


def _render_counter(metric: Counter) -> str:
    lines = [
        f"# HELP {metric.name} {metric.help}",
        f"# TYPE {metric.name} counter",
    ]
    for key, val in sorted(metric.samples()):
        lines.append(f"{metric.name}{_format_labels(metric.label_names, key)} {val}")
    return "\n".join(lines)


def _render_gauge(metric: Gauge) -> str:
    lines = [
        f"# HELP {metric.name} {metric.help}",
        f"# TYPE {metric.name} gauge",
    ]
    for key, val in sorted(metric.samples()):
        lines.append(f"{metric.name}{_format_labels(metric.label_names, key)} {val}")
    return "\n".join(lines)


def _render_histogram(metric: Histogram) -> str:
    lines = [
        f"# HELP {metric.name} {metric.help}",
        f"# TYPE {metric.name} histogram",
    ]
    for key, counts, total_sum, total_count in sorted(metric.samples()):
        labels_no_le = _format_labels(metric.label_names, key)
        for i, bound in enumerate(metric.buckets):
            le_label = _le_label(metric.label_names, key, str(bound))
            lines.append(f"{metric.name}_bucket{le_label} {counts[i]}")
        lines.append(
            f"{metric.name}_bucket{_le_label(metric.label_names, key, '+Inf')} {counts[-1]}"
        )
        lines.append(f"{metric.name}_sum{labels_no_le} {total_sum}")
        lines.append(f"{metric.name}_count{labels_no_le} {total_count}")
    return "\n".join(lines)


def _render_summary(metric: Summary) -> str:
    lines = [
        f"# HELP {metric.name} {metric.help}",
        f"# TYPE {metric.name} summary",
    ]
    for key, total_sum, total_count in sorted(metric.samples()):
        labels = _format_labels(metric.label_names, key)
        lines.append(f"{metric.name}_sum{labels} {total_sum}")
        lines.append(f"{metric.name}_count{labels} {total_count}")
    return "\n".join(lines)


def _le_label(label_names: tuple[str, ...], values: tuple[str, ...], le: str) -> str:
    pairs = [f'{n}="{_escape(v)}"' for n, v in zip(label_names, values, strict=True)]
    pairs.append(f'le="{le}"')
    return "{" + ",".join(pairs) + "}"


# ---------------------------------------------------------------------------
# Structured logging
# ---------------------------------------------------------------------------


_REQUIRED_LOG_KEYS: frozenset[str] = frozenset({"job_id", "video_id", "stage", "state", "attempts"})


def log_event(event: str, **fields: Any) -> None:
    """Emit one structlog line carrying the required state-transition fields.

    Required keys (``job_id, video_id, stage, state, attempts``) are
    documented in :data:`_REQUIRED_LOG_KEYS`; missing-key validation is
    enforced at runtime so a typo in a caller is caught the first
    time the line is emitted in tests.

    Extra context (durations, error kinds, backoff windows) rides
    through any other keyword. ``level`` (one of ``"debug" | "info" |
    "warning" | "error"``) routes the call to the matching structlog
    method; failure-storm debounce is the caller's responsibility
    (see ``mark_failed_or_retry`` in ``db/jobs_state.py``).
    """
    missing = _REQUIRED_LOG_KEYS - fields.keys()
    if missing:
        raise TypeError(
            f"log_event missing required keys: {sorted(missing)}; "
            "got " + sorted(fields.keys()).__repr__()
        )
    level = fields.pop("level", "info")
    log = structlog.get_logger("maktaba.jobs")
    if level == "debug":
        log.debug(event, **fields)
    elif level == "warning":
        log.warning(event, **fields)
    elif level == "error":
        log.error(event, **fields)
    else:
        log.info(event, **fields)


# ---------------------------------------------------------------------------
# Convenience timer
# ---------------------------------------------------------------------------


@dataclass(slots=True)
class StageTimer:
    """Context-manager helper that records duration on exit.

    Usage::

        with StageTimer(stage="probe") as t:
            do_work()
        t.record(outcome="done")  # logs to the histogram

    Two-step (`with` then `record`) so the caller can still report
    the outcome they observed inside the block without contorting
    the API.
    """

    stage: str
    _start: float = 0.0

    def __enter__(self) -> StageTimer:
        self._start = time.monotonic()
        return self

    def __exit__(self, *exc: object) -> None:
        return None

    def elapsed(self) -> float:
        return time.monotonic() - self._start

    def record(
        self,
        *,
        outcome: str,
        registry: MetricRegistry | None = None,
    ) -> None:
        reg = registry or default_registry()
        reg.record_attempt(
            stage=self.stage,
            outcome=outcome,
            duration_sec=self.elapsed(),
        )
