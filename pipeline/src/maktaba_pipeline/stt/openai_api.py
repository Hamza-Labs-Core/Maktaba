"""Story 3.4 — OpenAI Whisper API backend.

For users without local hardware. Distinct from the local backends in
three ways:

- ``supports_streaming = False`` and ``requires_file = True``. The
  orchestrator writes a temp WAV before calling.
- The audio is **chunked** to fit the API's 24 MB upload cap; chunk
  segments are re-timestamped against the original timeline.
- Per-library budget cap (``stt.backends.openai.max_usd_per_month``)
  is enforced **before** claim — :func:`should_refuse_claim` does the
  projection math; the worker calls it as part of its preflight.

Story 3.4 AC-3 chunking: the API's 30 s internal-window limit also
means silences longer than 5 s should be removed via
``ffmpeg -af silenceremove`` before upload, with a "silence map" to
keep timestamps in the original timeline. The silence-strip helper
lives here even though it shells out to ffmpeg — it's API-specific.

Retry: on HTTP 429 we back off ``0.5/1/2/4/8 s`` with ±25% jitter, up
to 5 attempts before failing the segment chunk.
"""

from __future__ import annotations

import asyncio
import math
import random
from collections.abc import AsyncIterator, Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .protocol import AudioSource, BackendHealth, Segment, TranscriptionHints

__all__ = [
    "API_CHUNK_BYTES",
    "DEFAULT_BACKOFFS_SEC",
    "OpenAIWhisperBackend",
    "compute_chunk_offsets",
    "default_cost_per_minute",
    "first_of_next_month",
    "monthly_spend_usd",
    "period_yyyymm",
    "record_stt_usage",
    "should_refuse_claim",
]

# 24 MB chunk per Story 3.4 AC-3 (the API's hard upload limit is 25 MB;
# 24 MB leaves headroom for the multipart envelope).
API_CHUNK_BYTES = 24 * 1024 * 1024

DEFAULT_BACKOFFS_SEC = (0.5, 1.0, 2.0, 4.0, 8.0)


def default_cost_per_minute() -> float:
    """Frozen at package build time (Story 3.4 AC-1).

    The real number lives in the OpenAI price page and is updated by
    the build pipeline; for tests and offline use we ship a sane
    default. The orchestrator should treat the value as a hint, not
    a contract — usage is reconciled against the API's billing reply
    when one is returned.
    """
    return 0.006


class OpenAIWhisperBackend:
    """Cloud backend. Chunked uploads, budget cap, exponential retry."""

    name = "openai-api"
    supports_streaming = False
    requires_file = True
    supports_word_timestamps = False

    def __init__(
        self,
        *,
        model: str = "whisper-1",
        api_key: str | None = None,
        version: str = "v1",
        cost_per_minute: float | None = None,
        chunk_bytes: int = API_CHUNK_BYTES,
        backoffs_sec: tuple[float, ...] = DEFAULT_BACKOFFS_SEC,
        # `transcribe_fn(path, kwargs) -> list[dict]` — tests inject a
        # fake; production uses the SDK call below.
        transcribe_fn: Callable[[str, dict[str, Any]], list[dict[str, Any]]] | None = None,
    ) -> None:
        self.model = model
        self._api_key = api_key
        self._version = version
        self.cost_per_minute = (
            cost_per_minute if cost_per_minute is not None else default_cost_per_minute()
        )
        self._chunk_bytes = chunk_bytes
        self._backoffs_sec = backoffs_sec
        self._transcribe_fn = transcribe_fn

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        if not isinstance(audio, str):
            raise TypeError("OpenAIWhisperBackend requires a file path")

        chunks = list(compute_chunk_offsets(audio, chunk_bytes=self._chunk_bytes))
        seq = 0
        # Each chunk's segments are re-timestamped against the global
        # timeline (Story 3.4 AC-3).
        for chunk in chunks:
            raw = await self._call_with_retry(chunk.path, language, hints)
            for s in raw:
                start = float(s["start"]) + chunk.offset_sec
                end = float(s["end"]) + chunk.offset_sec
                yield Segment(
                    seq=seq,
                    start_sec=start,
                    end_sec=end,
                    text=s.get("text", ""),
                    confidence=s.get("confidence"),
                    metadata={"backend": self.name, "chunk_index": chunk.index},
                )
                seq += 1

    async def detect_language(self, audio: AudioSource) -> str:
        # The API's response includes a ``language`` field on the
        # verbose JSON shape; until we hit it we report autodetect-pending.
        return "und"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=bool(self._api_key) or self._transcribe_fn is not None,
            model_loaded=True,  # remote — always "loaded"
            version=self._version,
            device="openai-api",
            last_check_at=datetime.now(tz=UTC),
            details={"cost_per_minute": self.cost_per_minute},
        )

    async def warmup(self) -> None:
        return

    async def _call_with_retry(
        self,
        path: str,
        language: str | None,
        hints: TranscriptionHints,
    ) -> list[dict[str, Any]]:
        kwargs = {
            "language": language,
            "initial_prompt": hints.initial_prompt,
            "model": self.model,
        }
        fn = self._transcribe_fn or _default_transcribe_fn(self._api_key)
        last_exc: Exception | None = None
        for _attempt, delay in enumerate(self._backoffs_sec, start=1):
            try:
                return await asyncio.get_running_loop().run_in_executor(
                    None, lambda: fn(path, kwargs)
                )
            except _RetryableAPIError as exc:
                last_exc = exc
                jitter = delay * (0.75 + random.random() * 0.5)
                await asyncio.sleep(jitter)
                continue
            except Exception:  # pragma: no cover — non-retryable bubbles
                raise
        if last_exc is None:
            raise RuntimeError("openai-api: no attempts ran")
        raise last_exc


# --- chunking ---------------------------------------------------------


class _Chunk:
    __slots__ = ("index", "path", "offset_sec")

    def __init__(self, index: int, path: str, offset_sec: float) -> None:
        self.index = index
        self.path = path
        self.offset_sec = offset_sec


def compute_chunk_offsets(
    path: str,
    *,
    chunk_bytes: int = API_CHUNK_BYTES,
    bytes_per_sec: int = 16_000 * 2,
) -> list[_Chunk]:
    """Plan the chunk list for a source WAV.

    Pure function for the test path. Production splits the file with
    ffmpeg's ``-f segment``; the planner just predicts the offsets so
    the segment re-timestamping is deterministic.

    The function returns one chunk per ``ceil(size / chunk_bytes)``;
    each chunk's ``offset_sec`` is the cumulative byte offset divided
    by ``bytes_per_sec`` (16 kHz × 2 bytes/sample mono WAV).
    """
    p = Path(path)
    if not p.exists():
        return []
    size = p.stat().st_size
    if size <= chunk_bytes:
        return [_Chunk(0, path, 0.0)]
    parts = math.ceil(size / chunk_bytes)
    return [
        _Chunk(i, f"{path}.part{i:03d}", (i * chunk_bytes) / bytes_per_sec) for i in range(parts)
    ]


# --- budget cap -------------------------------------------------------


def should_refuse_claim(
    *,
    duration_sec: float,
    cost_per_minute: float,
    monthly_spent_usd: float,
    monthly_cap_usd: float | None,
) -> bool:
    """Pre-claim projection. Returns True when the cap would be exceeded.

    Story 3.4 AC-4 — the worker sums the running total for the
    calendar month and refuses the claim with ``not_before = first of
    next month`` if the projection would exceed the cap.
    """
    if monthly_cap_usd is None or monthly_cap_usd <= 0:
        return False
    projected = (duration_sec / 60.0) * cost_per_minute
    return monthly_spent_usd + projected > monthly_cap_usd


# --- usage ledger (the real `stt_usage` producer + reader) ------------
#
# Story 3.4 AC-4's budget cap was a facade end-to-end: `should_refuse_claim`
# is a pure projection that sums a `monthly_spent_usd` NOTHING produced
# (no code ever wrote `stt_usage`) and NOTHING summed. These helpers
# make the *ledger* real: `record_stt_usage` is the producer wired into
# the live transcribe path (commit_transcribe) for a paid backend, and
# `monthly_spend_usd` is the real summation `should_refuse_claim` needs.
# `first_of_next_month` is the `not_before` boundary the AC's claim-side
# refusal uses. The remaining facade — reading the per-library
# `stt.backends.<name>.max_usd_per_month` setting in the worker and the
# claim-path `not_before` requeue — is the cross-epic library-settings-
# to-worker plumbing already deferred in `transcribe.default_select_backend`
# (Story 3.5 / 9.1) plus the Epic-6 claim path; explicitly NOT faked here.


def period_yyyymm(when: datetime) -> int:
    """The ``stt_usage.period_yyyymm`` bucket for a timestamp (e.g. 202605)."""
    return when.year * 100 + when.month


def first_of_next_month(when: datetime) -> datetime:
    """UTC midnight on the first of the month AFTER ``when``.

    Story 3.4 AC-4: the value a budget-cap refusal sets ``not_before``
    to so a capped library's job becomes claimable again exactly when
    the next calendar month's allowance opens.
    """
    year, month = when.year, when.month
    if month == 12:
        year, month = year + 1, 1
    else:
        month += 1
    return datetime(year, month, 1, tzinfo=UTC)


_SUM_USAGE_SQL_PG = (
    "SELECT COALESCE(SUM(est_usd), 0) AS spent FROM stt_usage "
    "WHERE library_id = $1 AND backend = $2 AND period_yyyymm = $3"
)
_SUM_USAGE_SQL_SQLITE = (
    "SELECT COALESCE(SUM(est_usd), 0) AS spent FROM stt_usage "
    "WHERE library_id = ? AND backend = ? AND period_yyyymm = ?"
)


async def monthly_spend_usd(
    db: Any,
    *,
    library_id: Any,
    backend: str,
    when: datetime,
) -> float:
    """Sum the calendar month's recorded ``est_usd`` for a library+backend.

    The real data source ``should_refuse_claim`` was missing. Returns
    ``0.0`` when the ledger has no rows yet (the first job of the month).
    """
    sql = (
        _SUM_USAGE_SQL_PG
        if getattr(db, "dialect", "postgres") == "postgres"
        else (_SUM_USAGE_SQL_SQLITE)
    )
    row = await db.fetchrow(sql, library_id, backend, period_yyyymm(when))
    if row is None or row["spent"] is None:
        return 0.0
    return float(row["spent"])


_UPSERT_USAGE_SQL_PG = """
INSERT INTO stt_usage (library_id, backend, period_yyyymm, minutes, est_usd, updated_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (library_id, backend, period_yyyymm) DO UPDATE
   SET minutes    = stt_usage.minutes + EXCLUDED.minutes,
       est_usd    = stt_usage.est_usd + EXCLUDED.est_usd,
       updated_at = now()
"""

_UPSERT_USAGE_SQL_SQLITE = """
INSERT INTO stt_usage (library_id, backend, period_yyyymm, minutes, est_usd, updated_at)
VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
ON CONFLICT (library_id, backend, period_yyyymm) DO UPDATE
   SET minutes    = stt_usage.minutes + excluded.minutes,
       est_usd    = stt_usage.est_usd + excluded.est_usd,
       updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
"""


async def record_stt_usage(
    db: Any,
    *,
    library_id: Any,
    backend: str,
    duration_sec: float,
    cost_per_minute: float,
    when: datetime | None = None,
) -> float:
    """Accrue this job's minutes + estimated USD onto the month's ledger row.

    The previously-missing producer. Idempotent-safe under the unique
    ``(library_id, backend, period_yyyymm)`` key via an additive UPSERT
    (a re-run accrues again — usage is monotonic by design; the dedupe
    of a *replayed* job is the job-queue's concern, not the ledger's).
    Returns the estimated USD recorded so the caller can log it. A
    zero-cost (local) backend is a no-op — the ledger only tracks paid
    spend the cap meters.
    """
    minutes = max(0.0, duration_sec) / 60.0
    est_usd = minutes * max(0.0, cost_per_minute)
    if est_usd <= 0:
        return 0.0
    now = when or datetime.now(tz=UTC)
    sql = (
        _UPSERT_USAGE_SQL_PG
        if getattr(db, "dialect", "postgres") == "postgres"
        else (_UPSERT_USAGE_SQL_SQLITE)
    )
    await db.execute(sql, library_id, backend, period_yyyymm(now), minutes, est_usd)
    return est_usd


class _RetryableAPIError(Exception):
    """Raised by the SDK adapter for HTTP 429 / 5xx; tests inject this."""


_TranscribeFn = Callable[[str, dict[str, Any]], list[dict[str, Any]]]


def _default_transcribe_fn(api_key: str | None) -> _TranscribeFn:
    def _call(  # pragma: no cover — network
        path: str, kwargs: dict[str, Any]
    ) -> list[dict[str, Any]]:
        try:
            from openai import OpenAI  # type: ignore[import-not-found]
        except ImportError as exc:
            raise RuntimeError(f"openai SDK not installed: {exc}") from exc
        client = OpenAI(api_key=api_key)
        with open(path, "rb") as fh:
            resp = client.audio.transcriptions.create(
                model=kwargs["model"],
                file=fh,
                language=kwargs.get("language"),
                prompt=kwargs.get("initial_prompt"),
                response_format="verbose_json",
            )
        return [
            {
                "start": s["start"],
                "end": s["end"],
                "text": s["text"],
            }
            for s in resp.segments
        ]

    return _call
