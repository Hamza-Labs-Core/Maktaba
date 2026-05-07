---
name: Plan 03-05 — Backend registry, transcript history & per-library selection
description: Implementation plan for Epic 3 Story 5 (backend registry + transcript-history schema). Owns the migration that adds `transcripts.is_active` and `transcripts.metadata`, replaces the full UNIQUE with a partial unique index, backfills history. Ships the in-process Python registry, fallback chain, the atomic flip-active transaction, and the Go API endpoint that exposes ready backends.
type: plan
---

# Plan 03-05 — Backend Registry, Transcript History & Per-Library Selection

> **Canonical story:** [story-03-05-backend-registry.md](story-03-05-backend-registry.md).
>
> **Depends on:** [Plan 03-01](plan-03-01-backend-protocol.md) (Protocol,
> health, errors), [Plan 03-02](plan-03-02-whisper-mlx-backend.md),
> [Plan 03-03](plan-03-03-faster-whisper-backend.md), and
> [Plan 03-04](plan-03-04-openai-api-backend.md) — all three concrete
> backends are registry inputs.
>
> **Architecture references.** [§3.4 Transcriber](../../architecture.md)
> (the swappable backend list), [§8.1 Core tables](../../architecture.md)
> (the `transcripts` schema this plan rewrites — see REVIEW §1.1.b /
> §1.1.i), [§11.4 pipeline.toml](../../architecture.md)
> (`stt.backend`, `stt.fallback`).
>
> **Resolves REVIEW §1.1.b and §1.1.i.** This story is the **single
> owner** of the schema change that adds `is_active` to `transcripts`
> and replaces the impossible-to-reprocess UNIQUE constraint with a
> partial unique index. No other story modifies `transcripts.is_active`
> or this constraint.
>
> **Out of scope.** Per-segment commit (3.6). Pause / resume (3.7).
> Crash recovery (3.8). Diarization (3.9). The actual subtitle
> regeneration triggered by an active-flip is Epic 4 Story 4.1's retry
> path; this plan calls the enqueue, but the consumer lives in Epic 4.

---

## 1. Architecture diagram

```
                              ┌────────────────────────────────────┐
                              │  pipeline.toml [libraries.X]       │
                              │   stt_profile = "default"          │
                              │  pipeline.toml [stt.default]       │
                              │   backend  = "whisper-mlx"         │
                              │   fallback = ["whisper-cuda",      │
                              │               "whisper-cpu"]       │
                              └────────────────────────────────────┘
                                              │ loaded by pydantic-settings
                                              ▼
                         ┌──────────────────────────────────────────┐
                         │  STTRegistry  (pipeline/stt/registry.py) │
                         │                                          │
                         │  __init__(builders, settings, ledger)   │
                         │   - builders: name → factory closure     │
                         │     so health probes can run without     │
                         │     instantiating a model                │
                         │   - holds a singleton instance per name  │
                         │     once first instantiated (warmup)     │
                         │                                          │
                         │  async list() → list[STTBackend]:        │
                         │    For each registered name, call its    │
                         │    factory's `health_probe()` (≠ a       │
                         │    full instance). Return only ready.    │
                         │                                          │
                         │  async resolve(library) → STTBackend:    │
                         │    chain = [stt.backend, *stt.fallback]  │
                         │    walk chain; first ready wins;         │
                         │    if none, raise NoBackendReady.        │
                         │                                          │
                         │  async claim_or_defer(job, video,        │
                         │                        backend, ledger): │
                         │    runs the budget pre-check (3.4) and   │
                         │    the no-backend-ready re-queue policy  │
                         │                                          │
                         │  async flip_active(video_id, track_id,   │
                         │      new_transcript_id):                 │
                         │    runs the single-tx UPDATE+INSERT      │
                         └──────────────────────────────────────────┘
                                              │
                                              ▼
                         ┌──────────────────────────────────────────┐
                         │  Postgres                                │
                         │   transcripts (is_active partial UNIQUE) │
                         │   transcripts.metadata JSONB             │
                         │   stt_usage (Plan 03-04)                 │
                         └──────────────────────────────────────────┘
                                              │
                                              ▼ NOTIFY transcript_active_changed
                         ┌──────────────────────────────────────────┐
                         │  Subscribers:                            │
                         │   - subtitle_gen (Epic 4 Story 4.1)      │
                         │   - search reindex (Epic 5 Story 5.5)    │
                         │   - WebSocket fan-out via Go API         │
                         └──────────────────────────────────────────┘
```

Six things to notice:

1. **Builders, not instances, in the registry.** Health probes must
   not load a 1.5 GB model. The registry holds a `name → factory`
   table where the factory exposes a cheap `health_probe()` that
   doesn't touch the model file beyond `model_present()` (Plan 03-02
   §3.2). A full `instance()` is only created on first selection,
   memoized for re-use.
2. **Fallback walks the chain in declaration order.** `stt.backend`
   first, then `stt.fallback[*]` left-to-right. The first one with
   `health.ready=True` wins; we record `metrics.fallback_from` when we
   skipped one or more.
3. **All-unhealthy is a re-queue, not a fail.** Per story 3.5 edge
   case, the job's `not_before` is bumped 60 s and `attempts` increments
   up to `max_attempts`; only then does it transition to `failed`.
4. **The flip-active SQL is one transaction.** UPDATE the previous
   active row to `is_active=false`, then INSERT the new row with
   `is_active=true`. The partial UNIQUE index ensures correctness even
   under concurrent flips.
5. **`transcripts.metadata` is added in this migration.** Plans 03-02
   and 03-03 write into it; we promised to land the column here. The
   migration is bundled with `is_active` to keep history rewrites in a
   single goose step.
6. **Go API exposes the registry read-only.** `GET /api/system/stt/backends`
   (admin) lists every registered backend with `{name, ready,
   model_loaded, version, device, reason}`. The Pipeline service is
   the source of truth (it owns the protocol); the API queries it via
   gRPC.

---

## 2. New artifacts

| Layer | Path | Status | Purpose |
|---|---|---|---|
| Migration | `shared/db/migrations/0012_transcripts_is_active.sql` | **new** | Adds `is_active` + `metadata`; drops full UNIQUE; adds partial UNIQUE index; backfills history. |
| Migration test | `shared/db/migrations/tests/test_0012_transcripts_is_active.sql` | **new** | pgtap test that asserts the post-migration schema and backfill correctness. |
| Python | `pipeline/src/maktaba_pipeline/stt/registry.py` | **new** | `STTRegistry` and `BackendFactory` types. |
| Python | `pipeline/src/maktaba_pipeline/stt/_factories.py` | **new** | Factories for `whisper-mlx`, `whisper-cuda`, `whisper-cpu`, `openai-api` — wraps the constructors with cheap `health_probe()` closures. |
| Python | `pipeline/src/maktaba_pipeline/stt/_flip.py` | **new** | `flip_active(...)` SQL helper (asyncpg). |
| Python | `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` | **edit** | Wire `STTRegistry.resolve` and the budget pre-check into the claim path. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_registry_filters_unhealthy.py` | **new** | Story 3.5 acceptance — health=False excludes from `list()`. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_fallback_walks_chain.py` | **new** | Primary unhealthy → next ready used; `metrics.fallback_from` recorded. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_reprocess_creates_new_row.py` | **new** | Different model → new row, old flipped. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_reprocess_same_backend_model.py` | **new** | Same `(backend, model)` → succeeds (proves migration fix). |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_partial_unique_blocks_double_active.py` | **new** | Two concurrent flips → exactly one wins, the other retries. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_no_backend_ready_requeue.py` | **new** | All unhealthy → `not_before=+60s`, `attempts++`. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_missing_fallback_backend.py` | **new** | Fallback id not in build → ready=False, logged once. |
| Python | `pipeline/src/maktaba_pipeline/stt/tests/test_subtitle_regen_on_flip.py` | **new** | Flip enqueues `subtitle_gen` for new transcript + invalidates artifacts of old. |
| pgtap | `shared/db/migrations/tests/test_partial_unique_blocks_double_active.sql` | **new** | DB-level test: insert two `is_active=true` rows for same `(video_id, audio_track_id)` → second errors. |
| pgtap | `shared/db/migrations/tests/test_backfill_correctness.sql` | **new** | Pre-migration fixture has 2 rows for one `(video_id, audio_track_id)`; post-migration exactly one has `is_active=true` (the most recent `created_at`). |
| Go | `apps/api/internal/http/system/stt_backends.go` | **new** | `GET /api/system/stt/backends` admin endpoint. |
| Go | `apps/api/internal/http/system/stt_backends_test.go` | **new** | Handler tests with a fake gRPC client. |
| Proto | `shared/proto/pipeline.proto` | **edit** | Add `STTService { rpc ListBackends, rpc ResolveActive }` messages. |
| Go (sqlc) | `shared/db/queries/transcripts.sql` | **edit** | New queries: `FlipActiveTranscript`, `GetActiveTranscript`, `ListTranscriptHistory`. |

---

## 3. The migration — single most important file in this plan

### 3.1 `shared/db/migrations/0012_transcripts_is_active.sql`

```sql
-- +goose Up
-- 0012_transcripts_is_active.sql
--
-- Resolves REVIEW §1.1.b and §1.1.i (single owner: story 3.5).
--
-- Three changes, one transaction:
--   1. ADD COLUMN is_active  BOOLEAN NOT NULL DEFAULT TRUE
--   2. ADD COLUMN metadata   JSONB   NOT NULL DEFAULT '{}'::jsonb
--   3. Drop the full UNIQUE; add a partial UNIQUE index.
-- Plus a single-tx backfill: the most-recent row per
-- (video_id, audio_track_id) keeps is_active=true; older rows are
-- flipped to false.
--
-- Why it must be one transaction:
--   - The partial UNIQUE index is created AFTER the backfill, so the
--     backfill can write multiple is_active=false rows for the same
--     (video_id, audio_track_id) without tripping the constraint.
--   - The COLUMN add and the constraint replacement land together so
--     readers never see a half-migrated state.

BEGIN;

ALTER TABLE transcripts
    ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE transcripts
    ADD COLUMN metadata  JSONB   NOT NULL DEFAULT '{}'::jsonb;

-- Backfill: for each (video_id, audio_track_id), keep is_active=TRUE
-- on the most recent created_at; flip the rest to FALSE. We pick
-- created_at desc, ties broken by id (UUID) for determinism.
UPDATE transcripts t
SET    is_active = FALSE
WHERE  EXISTS (
    SELECT 1
    FROM   transcripts t2
    WHERE  t2.video_id       = t.video_id
      AND  t2.audio_track_id = t.audio_track_id
      AND  (t2.created_at, t2.id) > (t.created_at, t.id)
);

-- Drop the impossible-to-reprocess full UNIQUE.
ALTER TABLE transcripts
    DROP CONSTRAINT IF EXISTS transcripts_video_id_audio_track_id_backend_model_key;

-- Replace it with a partial UNIQUE INDEX: at most one active
-- transcript per (video_id, audio_track_id).
CREATE UNIQUE INDEX transcripts_active_unique
    ON transcripts (video_id, audio_track_id)
    WHERE is_active = TRUE;

-- Helper index for "show me the history" queries (admin/UI).
CREATE INDEX transcripts_history
    ON transcripts (video_id, audio_track_id, created_at DESC);

COMMIT;

-- +goose Down
BEGIN;
DROP INDEX IF EXISTS transcripts_history;
DROP INDEX IF EXISTS transcripts_active_unique;

-- Best effort: restore the original UNIQUE. If non-unique data exists
-- because reprocessing has happened post-up, this DOWN will fail and
-- require manual deduplication; that is intentional.
ALTER TABLE transcripts
    ADD CONSTRAINT transcripts_video_id_audio_track_id_backend_model_key
    UNIQUE (video_id, audio_track_id, backend, model);

ALTER TABLE transcripts
    DROP COLUMN IF EXISTS metadata;

ALTER TABLE transcripts
    DROP COLUMN IF EXISTS is_active;

COMMIT;
```

### 3.2 `shared/db/migrations/tests/test_0012_transcripts_is_active.sql` (pgtap)

```sql
BEGIN;
SELECT plan(8);

-- 1. is_active column exists, default TRUE, NOT NULL.
SELECT col_type_is        ('transcripts', 'is_active', 'boolean');
SELECT col_not_null       ('transcripts', 'is_active');
SELECT col_default_is     ('transcripts', 'is_active', 'true');

-- 2. metadata column exists, default '{}', NOT NULL.
SELECT col_type_is        ('transcripts', 'metadata',  'jsonb');
SELECT col_not_null       ('transcripts', 'metadata');

-- 3. Old full-UNIQUE constraint is gone.
SELECT hasnt_index('transcripts', 'transcripts_video_id_audio_track_id_backend_model_key');

-- 4. Partial UNIQUE index is present.
SELECT has_index('transcripts', 'transcripts_active_unique');

-- 5. History helper index is present.
SELECT has_index('transcripts', 'transcripts_history');

SELECT * FROM finish();
ROLLBACK;
```

### 3.3 Backfill correctness (separate pgtap, runs against a fixture DB)

```sql
BEGIN;
SELECT plan(3);

-- Fixture: two transcripts for one (video_id, audio_track_id), older
-- inserted first. After 0010, only the newer should be active.
WITH tracks AS (
    SELECT id AS track_id FROM audio_tracks
    WHERE video_id = '00000000-0000-0000-0000-000000000001'
)
SELECT is(
    (SELECT COUNT(*) FROM transcripts
     WHERE video_id = '00000000-0000-0000-0000-000000000001' AND is_active),
    1::bigint,
    'exactly one active row after backfill'
);

SELECT is(
    (SELECT id FROM transcripts
     WHERE video_id = '00000000-0000-0000-0000-000000000001'
     ORDER BY created_at DESC LIMIT 1),
    (SELECT id FROM transcripts
     WHERE video_id = '00000000-0000-0000-0000-000000000001' AND is_active LIMIT 1),
    'newest row is the active one'
);

SELECT throws_ok($$
    INSERT INTO transcripts (video_id, audio_track_id, language,
                             backend, model, word_level, diarized, is_active)
    VALUES ('00000000-0000-0000-0000-000000000001',
            (SELECT track_id FROM (SELECT id AS track_id FROM audio_tracks
                                   WHERE video_id = '00000000-0000-0000-0000-000000000001'
                                   LIMIT 1) s),
            'en', 'whisper-cpu', 'small', false, false, true);
$$,
    '23505',
    NULL,
    'partial unique index blocks a second is_active=true row'
);

SELECT * FROM finish();
ROLLBACK;
```

---

## 4. Implementation — registry & flip

### 4.1 `pipeline/src/maktaba_pipeline/stt/registry.py`

```python
"""STTRegistry — the single object that knows which backends exist,
which are healthy right now, and how to walk the fallback chain.

Lifecycle:
- One STTRegistry per Python process. Constructed at startup by
  pipeline/cli.py and handed to the runner.
- Backends are registered as `BackendFactory(name, build, health_probe)`.
  - `build()` returns an STTBackend instance (lazy).
  - `health_probe()` is a cheap async function returning BackendHealth.
- An instance, once built, is cached and reused. `close()` releases all
  cached instances at shutdown.
"""
from __future__ import annotations

import asyncio
import dataclasses
import logging
from collections.abc import Awaitable, Callable, Iterable

from .errors import BackendNotReady
from .protocol import STTBackend
from .types import BackendHealth

log = logging.getLogger(__name__)


@dataclasses.dataclass
class BackendFactory:
    name: str
    build: Callable[[], STTBackend]
    health_probe: Callable[[], Awaitable[BackendHealth]]


class NoBackendReady(BackendNotReady):
    """No backend in the chain reported ready."""


class STTRegistry:
    def __init__(
        self,
        factories: Iterable[BackendFactory],
        *,
        configured_backend: str,
        configured_fallback: list[str],
    ) -> None:
        self._factories: dict[str, BackendFactory] = {f.name: f for f in factories}
        self._instances: dict[str, STTBackend] = {}
        self._configured_backend = configured_backend
        self._configured_fallback = list(configured_fallback)
        self._missing_logged: set[str] = set()
        self._lock = asyncio.Lock()

    # ------------------------------------------------------------------ Listing

    async def list(self) -> list[STTBackend]:
        """Return every registered backend whose `health.ready == True`
        right now. Cheap; calls `health_probe()` for every factory."""
        results: list[STTBackend] = []
        probes = await asyncio.gather(
            *(self._probe(f) for f in self._factories.values()),
            return_exceptions=True,
        )
        for f, h in zip(self._factories.values(), probes, strict=True):
            if isinstance(h, BaseException):
                log.warning("health probe failed for %s: %r", f.name, h)
                continue
            if h.ready:
                results.append(await self._instance(f.name))
        return results

    async def _probe(self, f: BackendFactory) -> BackendHealth:
        return await f.health_probe()

    # ------------------------------------------------------------------ Resolve

    async def resolve(self) -> tuple[STTBackend, list[str]]:
        """Walk [primary, *fallback] in order; first ready wins.

        Returns (chosen_backend, fallback_from). `fallback_from` is the
        list of names that were skipped because they were unhealthy or
        missing — used for `metrics.fallback_from` recording.
        """
        chain = [self._configured_backend, *self._configured_fallback]
        skipped: list[str] = []
        for name in chain:
            f = self._factories.get(name)
            if f is None:
                if name not in self._missing_logged:
                    log.warning("backend %r in fallback chain not in build", name)
                    self._missing_logged.add(name)
                skipped.append(f"{name}:missing")
                continue
            try:
                h = await f.health_probe()
            except Exception as exc:
                log.warning("health probe failed for %s: %r", name, exc)
                skipped.append(f"{name}:probe-error")
                continue
            if not h.ready:
                skipped.append(f"{name}:{h.reason or 'not-ready'}")
                continue
            backend = await self._instance(name)
            return backend, skipped
        raise NoBackendReady(
            f"no backend in chain ready: chain={chain} skipped={skipped}"
        )

    async def _instance(self, name: str) -> STTBackend:
        async with self._lock:
            inst = self._instances.get(name)
            if inst is None:
                inst = self._factories[name].build()
                self._instances[name] = inst
            return inst

    # ------------------------------------------------------------------ Shutdown

    async def close(self) -> None:
        for inst in list(self._instances.values()):
            try:
                await inst.close()
            except Exception:
                log.exception("close failed")
        self._instances.clear()
```

### 4.2 `_factories.py`

```python
"""Builders + cheap health probes for every shipped backend.

The probes run in parallel inside STTRegistry.list(). Each probe is
allowed up to ~250 ms; anything slower belongs in a sidecar reaper, not
the hot claim path.
"""
from __future__ import annotations

import asyncio
import os

from .faster_whisper import FasterWhisperCPU, FasterWhisperCUDA
from .faster_whisper._runtime import cuda_available, faster_whisper_importable
from .openai_api import OpenAIWhisperBackend
from .openai_api._budget import BudgetLedger
from .protocol import STTBackend  # noqa: F401  -- typing
from .registry import BackendFactory
from .types import BackendHealth
from .whisper_mlx import WhisperMLXBackend
from .whisper_mlx._runtime import is_apple_silicon, mlx_whisper_importable, model_present
from .whisper_mlx._models import repo_for as mlx_repo_for


def build_default_factories(settings, ledger: BudgetLedger) -> list[BackendFactory]:
    return [
        _mlx_factory(settings),
        _cuda_factory(settings),
        _cpu_factory(settings),
        _openai_factory(settings, ledger),
    ]


# ---- MLX --------------------------------------------------------------

def _mlx_factory(settings) -> BackendFactory:
    model = settings.stt.default.model

    async def probe() -> BackendHealth:
        ready = (
            is_apple_silicon()
            and mlx_whisper_importable()
            and model_present(mlx_repo_for(model))
        )
        return BackendHealth(
            ready=ready, model_loaded=False, version="mlx",
            device="mlx" if ready else "unknown",
            last_check_at=asyncio.get_event_loop().time(),
            reason=None if ready else "non-arm64-darwin or model uncached",
        )

    return BackendFactory(
        name="whisper-mlx",
        build=lambda: WhisperMLXBackend(model=model),
        health_probe=probe,
    )


# ---- CUDA -------------------------------------------------------------

def _cuda_factory(settings) -> BackendFactory:
    model = settings.stt.default.model

    async def probe() -> BackendHealth:
        ready = faster_whisper_importable() and cuda_available()
        return BackendHealth(
            ready=ready, model_loaded=False, version="faster-whisper",
            device="cuda" if ready else "unknown",
            last_check_at=asyncio.get_event_loop().time(),
            reason=None if ready else "no CUDA device or library missing",
        )

    return BackendFactory(
        name="whisper-cuda",
        build=lambda: FasterWhisperCUDA(model=model),
        health_probe=probe,
    )


# ---- CPU --------------------------------------------------------------

def _cpu_factory(settings) -> BackendFactory:
    model = settings.stt.default.model

    async def probe() -> BackendHealth:
        ready = faster_whisper_importable()
        return BackendHealth(
            ready=ready, model_loaded=False, version="faster-whisper",
            device="cpu" if ready else "unknown",
            last_check_at=asyncio.get_event_loop().time(),
            reason=None if ready else "faster-whisper not importable",
        )

    return BackendFactory(
        name="whisper-cpu",
        build=lambda: FasterWhisperCPU(model=model),
        health_probe=probe,
    )


# ---- OpenAI API -------------------------------------------------------

def _openai_factory(settings, ledger: BudgetLedger) -> BackendFactory:
    api_key_env = settings.stt.backends.openai.api_key_env
    model = settings.stt.backends.openai.model

    async def probe() -> BackendHealth:
        if not os.environ.get(api_key_env):
            return BackendHealth(
                ready=False, model_loaded=False, version=model,
                device="remote",
                last_check_at=asyncio.get_event_loop().time(),
                reason=f"{api_key_env} missing",
            )
        return BackendHealth(
            ready=True, model_loaded=True, version=model,
            device="remote",
            last_check_at=asyncio.get_event_loop().time(),
        )

    return BackendFactory(
        name="openai-api",
        build=lambda: OpenAIWhisperBackend(
            api_key=os.environ.get(api_key_env),
            model=model,
            ledger=ledger,
        ),
        health_probe=probe,
    )
```

### 4.3 `_flip.py`

```python
"""The atomic flip-active operation. Same transaction:
  - UPDATE existing active row to is_active=false
  - INSERT new row with is_active=true
  - rely on partial UNIQUE to enforce correctness under concurrency.
"""
from __future__ import annotations

from uuid import UUID

import asyncpg


async def flip_active(
    pool: asyncpg.Pool,
    *,
    video_id: UUID,
    audio_track_id: int,
    new_transcript: dict,
) -> UUID:
    """Returns the inserted transcript id."""
    async with pool.acquire() as conn:
        async with conn.transaction():
            await conn.execute(
                """
                UPDATE transcripts
                SET    is_active = false
                WHERE  video_id = $1
                  AND  audio_track_id = $2
                  AND  is_active = true
                """,
                video_id, audio_track_id,
            )
            new_id = await conn.fetchval(
                """
                INSERT INTO transcripts (
                    video_id, audio_track_id, language,
                    backend, model, backend_version,
                    word_level, diarized, quality_score,
                    metadata, is_active
                ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, true)
                RETURNING id
                """,
                video_id,
                audio_track_id,
                new_transcript["language"],
                new_transcript["backend"],
                new_transcript["model"],
                new_transcript.get("backend_version"),
                new_transcript.get("word_level", True),
                new_transcript.get("diarized", False),
                new_transcript.get("quality_score"),
                new_transcript.get("metadata", {}),
            )
            await conn.execute(
                "NOTIFY transcript_active_changed, $1::text",
                str(new_id),
            )
            return new_id
```

The `NOTIFY` is heard by Epic 4 Story 4.1's subtitle-gen subscriber and
by the Go API's WebSocket fan-out.

### 4.4 Wiring into the transcribe stage

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py (excerpt)
async def run(self, ctx: StageContext, job: Job) -> StageResult:
    video = await self._load_video(job.video_id)
    settings = await self._library_settings(video.library_id)
    backend, fallback_from = await self._registry.resolve()

    # Budget pre-check (Plan 03-04 §3.7 wiring lives here).
    if backend.cost_per_minute and backend.cost_per_minute > 0:
        cap = settings.stt.backends.get(backend.name, {}).get("max_usd_per_month")
        if cap is not None:
            projected = (video.duration_sec or 0.0) / 60.0 * backend.cost_per_minute
            used = await self._ledger.month_used(backend.name, ctx.now)
            if used + projected > cap:
                return StageResult.defer(
                    not_before=_first_of_next_month(ctx.now),
                    error_kind="budget_cap",
                    detail={"projected": projected, "used": used, "cap": cap},
                )

    await backend.warmup()  # avoid eating the heartbeat budget on cold start
    audio_source = await self._open_audio(video, backend.requires_file)

    new_transcript_meta = {
        "backend_version": (await backend.health()).version,
        "fallback_from": fallback_from,
    }

    # Hand off to story 3.6's per-segment-commit driver. The flip happens
    # at job end, after the final segment commits.
    transcript_id = await self._driver.run(
        backend=backend,
        audio_source=audio_source,
        video=video,
        on_complete=lambda t: self._flip_at_end(video, t, new_transcript_meta),
    )
    return StageResult.done(transcript_id=transcript_id)


async def _flip_at_end(self, video, partial_transcript, meta):
    """Called by the segment-commit driver when the last segment lands."""
    return await flip_active(
        self._pool,
        video_id=video.id,
        audio_track_id=partial_transcript.audio_track_id,
        new_transcript=partial_transcript.to_dict() | {"metadata": meta},
    )
```

---

## 5. Test plan

### 5.1 `test_registry_filters_unhealthy.py`

```python
"""Story 3.5 acceptance: `pipeline.stt.registry.list()` returns every
backend whose health.ready == True at the moment of the call."""
import time

import pytest

from maktaba_pipeline.stt.registry import BackendFactory, STTRegistry
from maktaba_pipeline.stt.types import BackendHealth


def _factory(name, ready):
    async def probe():
        return BackendHealth(
            ready=ready, model_loaded=False, version="0",
            device="cpu", last_check_at=time.time(),
        )
    return BackendFactory(name=name, build=lambda: object(), health_probe=probe)


async def test_filters_unhealthy():
    reg = STTRegistry(
        factories=[_factory("a", True), _factory("b", False), _factory("c", True)],
        configured_backend="a", configured_fallback=[],
    )
    out = await reg.list()
    # Build returns a bare object; we only check that exactly two
    # backends were instantiated.
    assert len(out) == 2
```

### 5.2 `test_fallback_walks_chain.py`

```python
async def test_primary_unhealthy_secondary_used():
    reg = STTRegistry(
        factories=[_factory("a", False), _factory("b", True)],
        configured_backend="a", configured_fallback=["b"],
    )
    backend, skipped = await reg.resolve()
    assert backend is reg._instances["b"]
    assert skipped == ["a:not-ready"]


async def test_all_unhealthy_raises_no_backend_ready():
    from maktaba_pipeline.stt.registry import NoBackendReady
    reg = STTRegistry(
        factories=[_factory("a", False), _factory("b", False)],
        configured_backend="a", configured_fallback=["b"],
    )
    with pytest.raises(NoBackendReady):
        await reg.resolve()
```

### 5.3 `test_reprocess_creates_new_row.py`

```python
"""Story 3.5: re-running with a different model creates a new row,
old row's is_active flips to false in the same transaction."""
import asyncpg
from uuid import uuid4

from maktaba_pipeline.stt._flip import flip_active


async def test_flip_creates_new_row(pool: asyncpg.Pool, sample_video, sample_track):
    # Insert initial transcript.
    async with pool.acquire() as c:
        old_id = await c.fetchval(
            """INSERT INTO transcripts (video_id, audio_track_id, language,
                                        backend, model, word_level, diarized, is_active)
               VALUES ($1, $2, 'en', 'whisper-cpu', 'small', true, false, true)
               RETURNING id""",
            sample_video.id, sample_track.id,
        )

    new_id = await flip_active(
        pool,
        video_id=sample_video.id,
        audio_track_id=sample_track.id,
        new_transcript={"language": "en", "backend": "whisper-cuda",
                        "model": "large-v3", "metadata": {}},
    )
    assert new_id != old_id

    async with pool.acquire() as c:
        rows = await c.fetch(
            "SELECT id, is_active FROM transcripts WHERE video_id=$1 AND audio_track_id=$2",
            sample_video.id, sample_track.id,
        )
    assert len(rows) == 2
    actives = [r for r in rows if r["is_active"]]
    assert len(actives) == 1
    assert actives[0]["id"] == new_id
```

### 5.4 `test_reprocess_same_backend_model.py`

```python
"""Story 3.5: re-running with the *same* (backend, model) — previously
blocked by the full UNIQUE — must now succeed."""

async def test_same_backend_model_succeeds(pool, sample_video, sample_track):
    async with pool.acquire() as c:
        await c.execute(
            """INSERT INTO transcripts (video_id, audio_track_id, language,
                                        backend, model, word_level, diarized, is_active)
               VALUES ($1, $2, 'en', 'whisper-cuda', 'large-v3', true, false, true)""",
            sample_video.id, sample_track.id,
        )

    new_id = await flip_active(
        pool,
        video_id=sample_video.id,
        audio_track_id=sample_track.id,
        new_transcript={"language": "en", "backend": "whisper-cuda",
                        "model": "large-v3", "metadata": {}},
    )
    assert new_id is not None  # the migration removed the blocker
```

### 5.5 `test_partial_unique_blocks_double_active.py`

```python
"""Story 3.5: two concurrent flips → exactly one wins; the other
raises a unique-violation that the orchestrator retries."""
import asyncio

import asyncpg.exceptions
import pytest


async def test_concurrent_flips_one_winner(pool, sample_video, sample_track):
    new = lambda: {"language": "en", "backend": "whisper-cpu",
                   "model": "small", "metadata": {}}
    async def attempt():
        try:
            return await flip_active(
                pool, video_id=sample_video.id,
                audio_track_id=sample_track.id, new_transcript=new(),
            )
        except asyncpg.exceptions.UniqueViolationError:
            return None

    a, b = await asyncio.gather(attempt(), attempt())
    winners = [x for x in (a, b) if x is not None]
    assert len(winners) == 1
```

### 5.6 `test_no_backend_ready_requeue.py`

```python
"""Story 3.5 edge case: all backends unhealthy at claim time → job
requeued with not_before = now() + 60s up to max_attempts."""
async def test_requeue_when_no_backend(stage, mock_registry_all_unhealthy, fake_now):
    decision = await stage.claim_or_defer(job=..., now=fake_now)
    assert decision.action == "defer"
    assert decision.not_before == fake_now + timedelta(seconds=60)
    assert decision.error_kind == "no_backend_ready"
```

### 5.7 `test_missing_fallback_backend.py`

```python
"""Story 3.5 edge case: a backend listed in fallback that is missing
from the build is treated as ready=False, logged once."""
import logging


async def test_missing_logged_once(caplog):
    reg = STTRegistry(
        factories=[_factory("a", True)],
        configured_backend="a",
        configured_fallback=["does-not-exist", "does-not-exist"],
    )
    with caplog.at_level(logging.WARNING):
        # First resolve: warns
        await reg.resolve()
        # Second resolve: should NOT warn again for the same name
        await reg.resolve()
    relevant = [r for r in caplog.records if "does-not-exist" in r.message]
    assert len(relevant) == 1
```

### 5.8 `test_subtitle_regen_on_flip.py`

```python
"""Story 3.5 edge case: flipping active triggers a new subtitle_gen
enqueue + invalidation pass for the previous transcript's artifacts.
The Pipeline service issues NOTIFY transcript_active_changed; Epic 4
Story 4.1's subscriber consumes it. We verify the NOTIFY is sent."""
import asyncpg


async def test_notify_emitted(pool, sample_video, sample_track):
    listener = await asyncpg.connect()
    listened: list[str] = []
    async def cb(_c, _pid, _ch, payload):
        listened.append(payload)
    await listener.add_listener("transcript_active_changed", cb)
    try:
        new_id = await flip_active(
            pool, video_id=sample_video.id, audio_track_id=sample_track.id,
            new_transcript={"language": "en", "backend": "whisper-cpu",
                            "model": "small", "metadata": {}},
        )
        await asyncio.sleep(0.1)
        assert listened == [str(new_id)]
    finally:
        await listener.close()
```

---

## 6. Edge cases (story 3.5) — explicit handling

| Story §Edge case | Handling here |
|---|---|
| **All backends unhealthy at claim time.** | Stage-level `claim_or_defer` returns `defer(not_before=now+60s, error_kind="no_backend_ready")` until `attempts >= max_attempts`, then the job runner transitions to `failed`. Verified by `test_no_backend_ready_requeue.py`. |
| **A backend listed in fallback is missing from the build.** | `STTRegistry.resolve()` treats it as `ready=False`, logs one warning per name (gated by `_missing_logged: set[str]`). Verified by `test_missing_fallback_backend.py`. |
| **Reader sees momentary "no active transcript".** | `flip_active()` runs UPDATE + INSERT in one `BEGIN/COMMIT` (asyncpg `conn.transaction()`), so any concurrent reader sees either the old or the new row, never neither and never both. The partial UNIQUE index enforces "at most one" and rejects two-actives concurrently. Verified by `test_partial_unique_blocks_double_active.py` and the pgtap test. |
| **Subtitle generation depends on the active transcript.** | The flip emits `NOTIFY transcript_active_changed, <new_id>`; Epic 4 Story 4.1's subscriber reacts. This plan does not implement the subscriber but verifies the emission. Listed in §5.8. |

---

## 7. Acceptance checklist

| # | Item | Verified by |
|---|---|---|
| 1 | `pipeline.stt.registry.STTRegistry` exists; `list()` returns only `health.ready=True` backends. | `test_registry_filters_unhealthy.py`. |
| 2 | `STTRegistry.resolve()` walks `[stt.backend, *stt.fallback]` in order; first ready wins. | `test_fallback_walks_chain.py`. |
| 3 | If none ready: `NoBackendReady` raised; orchestrator defers job with `not_before = now() + 60s` and `error_kind = "no_backend_ready"`. | `test_no_backend_ready_requeue.py`. |
| 4 | `transcripts` row persists `(backend, model, backend_version)`. | sqlc-generated insert query covers all three. |
| 5 | Re-running with a different `(backend, model)` creates a new row; the old row flips to `is_active=false` atomically. | `test_reprocess_creates_new_row.py`. |
| 6 | Re-running with the **same** `(backend, model)` succeeds. | `test_reprocess_same_backend_model.py`. |
| 7 | Migration `0012_transcripts_is_active.sql` exists; adds `is_active BOOLEAN NOT NULL DEFAULT TRUE`; adds `metadata JSONB NOT NULL DEFAULT '{}'`. | pgtap `test_0012_transcripts_is_active.sql`. |
| 8 | Migration drops the full UNIQUE `(video_id, audio_track_id, backend, model)`. | pgtap test (5th assertion). |
| 9 | Migration adds partial unique index `transcripts_active_unique` on `(video_id, audio_track_id) WHERE is_active = true`. | pgtap test (7th assertion) + `test_partial_unique_blocks_double_active`. |
| 10 | Migration backfills `is_active = true` for the latest `created_at` per `(video_id, audio_track_id)` and `false` for the rest, in one transaction. | pgtap `test_backfill_correctness.sql`. |
| 11 | The flip path uses one transaction (`conn.transaction()`): UPDATE + INSERT. | Source diff in `_flip.py`; correctness verified by `test_partial_unique_blocks_double_active.py`. |
| 12 | A backend listed in fallback but missing from the build is treated as ready=False and logged exactly once at startup. | `test_missing_fallback_backend.py`. |
| 13 | Flipping active emits `NOTIFY transcript_active_changed, <new_id>`. | `test_subtitle_regen_on_flip.py`. |
| 14 | Go API endpoint `GET /api/system/stt/backends` returns `{name, ready, model_loaded, version, device, reason}` per backend; admin-scoped. | `stt_backends_test.go`. |
| 15 | Per-library budget cap pre-check is wired into the transcribe stage's claim path (using Plan 03-04 §3.7 logic). | `test_claim_refused_when_over_cap.py` (integration). |
| 16 | `metrics.fallback_from` is recorded into `transcripts.metadata.fallback_from` when a non-primary backend was used. | `test_fallback_metric_persisted.py`. |
| 17 | The plan does not introduce duplicate ownership of `is_active` — no other migration in `shared/db/migrations/` touches it. | `git grep -n is_active shared/db/migrations/` returns this file only. |
