"""Story 3.5 — backend registry, health filter, fallback walk.

The registry has two surfaces:

- :meth:`BackendRegistry.list_ready` — every registered backend whose
  ``health.ready == True`` at the moment of the call. Used by
  ``GET /api/system/health`` and by the orchestrator's preflight.
- :func:`pick_backend` — given a library config (``stt.backend``,
  ``stt.fallback``), walk the chain and return the first ready
  backend. Raises :class:`NoBackendReady` when none are.

The "transcript history" piece (Story 3.5 AC-3 — flipping
``is_active=false`` on the previous transcript and inserting a new
row in one transaction) is :func:`flip_active_transcript` here. The
SQL uses the partial unique index from migration 0012; concurrent
flips are handled by retrying on a unique violation.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any, Protocol
from uuid import UUID

from .protocol import BackendHealth, STTBackend

__all__ = [
    "BackendRegistry",
    "FallbackTrace",
    "NoBackendReady",
    "flip_active_transcript",
    "pick_backend",
]


class NoBackendReady(RuntimeError):
    """Raised by :func:`pick_backend` when the chain has no ready backend."""

    def __init__(self, attempted: list[str]) -> None:
        super().__init__(f"no backend ready (attempted: {attempted})")
        self.attempted = attempted


@dataclass(slots=True, frozen=True)
class FallbackTrace:
    """What :func:`pick_backend` walked through to reach its choice.

    Recorded on ``transcripts.metadata.fallback_from`` so audits can
    explain why an unexpected backend ran.
    """

    chosen: str
    attempted: tuple[str, ...]


class BackendRegistry:
    """Mutable registry, intentionally process-local.

    Production wires this up at boot from the library settings (one
    registry per worker). Tests use it directly.
    """

    __slots__ = ("_backends",)

    def __init__(self) -> None:
        self._backends: dict[str, STTBackend] = {}

    def register(self, backend: STTBackend) -> None:
        if backend.name in self._backends:
            raise ValueError(f"backend already registered: {backend.name}")
        self._backends[backend.name] = backend

    def get(self, name: str) -> STTBackend | None:
        return self._backends.get(name)

    def names(self) -> list[str]:
        return list(self._backends.keys())

    async def list_ready(self) -> list[STTBackend]:
        out: list[STTBackend] = []
        for b in self._backends.values():
            try:
                health = await b.health()
            except Exception:
                continue
            if health.ready:
                out.append(b)
        return out

    async def health_map(self) -> dict[str, BackendHealth]:
        out: dict[str, BackendHealth] = {}
        for name, b in self._backends.items():
            try:
                out[name] = await b.health()
            except Exception:
                out[name] = BackendHealth(
                    ready=False,
                    model_loaded=False,
                    version="unknown",
                    device="unknown",
                    last_check_at=datetime.now(tz=UTC),
                    details={"error": "health() raised"},
                )
        return out


async def pick_backend(
    registry: BackendRegistry,
    *,
    primary: str,
    fallback: list[str] | None = None,
) -> tuple[STTBackend, FallbackTrace]:
    """Walk ``[primary, *fallback]`` and return the first ready backend."""
    chain = [primary, *(fallback or [])]
    attempted: list[str] = []
    for name in chain:
        attempted.append(name)
        backend = registry.get(name)
        if backend is None:
            continue
        try:
            health = await backend.health()
        except Exception:
            continue
        if health.ready:
            return backend, FallbackTrace(chosen=name, attempted=tuple(attempted))
    raise NoBackendReady(attempted)


# --- transcript history -----------------------------------------------


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_FLIP_PREVIOUS_SQL = """
UPDATE transcripts
   SET is_active = false,
       superseded_at = now()
 WHERE video_id = $1
   AND audio_track_id = $2
   AND is_active = true
"""

_INSERT_NEW_SQL = """
INSERT INTO transcripts
       (video_id, audio_track_id, language, detected_language,
        language_confidence, backend, model, backend_version,
        word_level, diarized, is_active, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true, $11)
RETURNING id
"""


async def flip_active_transcript(
    db: _DBConn,
    *,
    video_id: UUID,
    audio_track_id: int,
    language: str,
    detected_language: str | None,
    language_confidence: float | None,
    backend: str,
    model: str,
    backend_version: str | None,
    word_level: bool,
    diarized: bool,
    metadata: dict[str, Any] | None = None,
) -> UUID:
    """Atomically retire the previous active transcript and create a new one.

    Returns the new transcript's UUID. Story 3.5 AC-4 — the partial
    unique index on ``(video_id, audio_track_id) WHERE is_active=true``
    enforces "exactly one active" while history rows stay unconstrained.
    Concurrent flips race on the index; the loser sees a unique
    violation and the orchestrator retries.
    """
    payload = json.dumps(metadata or {})
    async with db.transaction():
        await db.execute(_FLIP_PREVIOUS_SQL, video_id, audio_track_id)
        row = await db.fetchrow(
            _INSERT_NEW_SQL,
            video_id,
            audio_track_id,
            language,
            detected_language,
            language_confidence,
            backend,
            model,
            backend_version,
            word_level,
            diarized,
            payload,
        )
        if row is None:
            raise RuntimeError("flip_active_transcript: INSERT returned no row")
        return UUID(str(row["id"]))
