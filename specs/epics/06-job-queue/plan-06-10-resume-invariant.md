# Implementation Plan — Story 6.10 Single Source of Truth for Resume

> Companion to [story-06-10-resume-invariant.md](story-06-10-resume-invariant.md).
> The story states *what* and *why*; this plan states *how*.
> The invariant is the central correctness property of
> [architecture.md §7.6 / §7.7](../../architecture.md): the canonical
> resume offset is `processing_jobs.last_segment_end_sec`, and nothing
> else.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Migration SQL + a Python lint test + a Python property test. No new runtime code paths — this story formalizes invariants that other stories must respect. |
| Files | The CHECK constraint already lands in Story 6.1's migration `0002_processing_jobs.sql`. This story adds: `pipeline/tests/lint/test_no_sidecar_checkpoint_files.py`, `pipeline/tests/lint/test_no_resume_offset_columns.py`, `pipeline/tests/property/test_resume_invariant.py`. |
| Schema dependency | `processing_jobs.last_segment_end_sec` (Story 6.1), `transcript_segments.end_sec` (Epic 3 Story 3.6). |
| Out of scope | The actual segment-commit transaction (Epic 3 Story 3.6). The crash/resume orchestration (Epic 3 Story 3.8). This story owns the invariant guards (CHECK + lints + property test), not the writers. |

## 1. Architecture diagram

```
                  ┌────────────────────────────────────────────┐
                  │  THE INVARIANT (system-wide, all stages):  │
                  │                                            │
                  │  For every job J in any non-terminal       │
                  │  state (incl. paused), the value of        │
                  │      processing_jobs.last_segment_end_sec  │
                  │  IS the resume offset. No other column,    │
                  │  table, or file may serve as a checkpoint. │
                  └─────────────────┬──────────────────────────┘
                                    │
              enforced by FOUR independent guards:
                                    │
        ┌───────────────────────────┼─────────────────────────────────┐
        │                           │                                 │
        ▼                           ▼                                 ▼
 ┌─────────────────┐    ┌──────────────────────┐    ┌─────────────────────────────┐
 │ DB CHECK        │    │ Schema lint test     │    │ Codebase grep lint test     │
 │  last_segment   │    │  No table has a       │    │  No file in pipeline/        │
 │  _end_sec       │    │  *_resume_offset      │    │  contains 'partial.json',    │
 │  ∈ [0, total]   │    │  column anywhere      │    │  'checkpoint', '_resume',    │
 │  (Story 6.1)    │    │  in shared/db/queries │    │  '.partial', etc.            │
 └─────────────────┘    └──────────────────────┘    └─────────────────────────────┘
                                                                      │
                                                                      ▼
                                                  ┌─────────────────────────────────┐
                                                  │ Property test                   │
                                                  │  After every crash/resume cycle │
                                                  │  in synthetic chaos workload:    │
                                                  │   last(transcript_segments       │
                                                  │     WHERE transcript_id = T)     │
                                                  │     .end_sec                     │
                                                  │     == processing_jobs           │
                                                  │        .last_segment_end_sec     │
                                                  └─────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/tests/lint/test_no_sidecar_checkpoint_files.py` | Greps `pipeline/` for `partial.json`, `checkpoint`, `_resume`. |
| `pipeline/tests/lint/test_no_resume_offset_columns.py` | Greps `shared/db/migrations/*.sql` for column names matching `*_resume_offset`. |
| `pipeline/tests/property/test_resume_invariant.py` | Hypothesis-based property test that runs synthetic crash/resume cycles. |
| `pipeline/tests/property/conftest.py` | Fixture: chaos-runner harness. |

### 2.2 The DB CHECK (already in Story 6.1)

Replicated here for visibility. Story 6.1's `0002_processing_jobs.sql`
includes:

```sql
CONSTRAINT processing_jobs_resume_offset_chk CHECK (
    last_segment_end_sec >= 0
    AND last_segment_end_sec <= COALESCE(total_duration_seconds,
                                         last_segment_end_sec)
)
```

This story adds an integration test that explicitly exercises the
constraint:

```python
@pytest.mark.asyncio
async def test_check_constraint_rejects_negative(db, video):
    with pytest.raises(asyncpg.CheckViolationError):
        await db.execute(
            "INSERT INTO processing_jobs (video_id, stage, last_segment_end_sec) "
            "VALUES ($1, 'transcribe', -1)",
            video.id,
        )


@pytest.mark.asyncio
async def test_check_constraint_rejects_beyond_total_duration(db, video):
    with pytest.raises(asyncpg.CheckViolationError):
        await db.execute(
            "INSERT INTO processing_jobs (video_id, stage, "
            "                              total_duration_seconds, "
            "                              last_segment_end_sec) "
            "VALUES ($1, 'transcribe', 100, 101)",
            video.id,
        )
```

## 3. Lint test — sidecar checkpoint files

`pipeline/tests/lint/test_no_sidecar_checkpoint_files.py`:

```python
"""Architectural smoke test: no sidecar checkpoint files in pipeline/.

The DB is the only checkpoint. Sidecar JSON or partial files create a
second source of truth for resume offsets and have caused production
incidents in prior systems (architecture §7.9). This test fails the
build if any code path implies a sidecar.
"""
from __future__ import annotations

import pathlib
import re

import pytest


PIPELINE_SRC = pathlib.Path(__file__).resolve().parents[3] / "src"
PIPELINE_TESTS = pathlib.Path(__file__).resolve().parents[3] / "tests"

# Patterns that indicate a sidecar checkpoint. The list is conservative —
# false positives must add a `# resume-invariant-ok: <reason>` marker on
# the offending line.
PATTERNS = [
    re.compile(r"\bpartial\.json\b"),
    re.compile(r"\b\.partial\b"),
    re.compile(r"\bcheckpoint(\.|_)"),
    re.compile(r"\b_resume(?:_offset)?\b"),
    re.compile(r"\.maktaba/transcripts/.*\.partial"),
]

# A few files reference these strings as documentation of the rejected
# alternative; they're allowlisted by an explicit marker on each line.
ALLOWLIST_MARKER = "# resume-invariant-ok"


@pytest.mark.parametrize("root", [PIPELINE_SRC, PIPELINE_TESTS])
def test_no_sidecar_checkpoint_strings(root: pathlib.Path):
    hits: list[str] = []
    for path in root.rglob("*.py"):
        for i, line in enumerate(path.read_text().splitlines(), 1):
            if ALLOWLIST_MARKER in line:
                continue
            for pat in PATTERNS:
                if pat.search(line):
                    hits.append(f"{path.relative_to(root.parent.parent.parent)}:{i}: {line.strip()}")
                    break
    assert not hits, (
        "Sidecar checkpoint patterns found. The DB's "
        "processing_jobs.last_segment_end_sec is the canonical resume "
        "offset (Story 6.10). Mark intentional references with "
        f"`{ALLOWLIST_MARKER}: <reason>`.\n\n" + "\n".join(hits)
    )
```

## 4. Lint test — no `*_resume_offset` columns

`pipeline/tests/lint/test_no_resume_offset_columns.py`:

```python
"""No table other than processing_jobs may have a *_resume_offset column.

Resume positioning is owned by processing_jobs.last_segment_end_sec.
Adding a parallel column anywhere else creates an invariant gap that
silent reads can fall into.
"""
from __future__ import annotations

import pathlib
import re

import pytest


MIGRATIONS = pathlib.Path(__file__).resolve().parents[3].parent.parent / "shared" / "db" / "migrations"

COLUMN_PATTERN = re.compile(
    r"^\s*(?P<col>\w+)\s+(?:REAL|FLOAT|DOUBLE|INT|BIGINT|INTEGER)",
    re.IGNORECASE | re.MULTILINE,
)


def _columns_in_sql(text: str) -> set[str]:
    return {m.group("col").lower() for m in COLUMN_PATTERN.finditer(text)}


def test_no_resume_offset_column_in_any_table():
    forbidden_substrings = ("_resume_offset", "_resume_position", "_resume_at_sec")

    hits: list[str] = []
    for path in MIGRATIONS.rglob("*.sql"):
        text = path.read_text()
        for col in _columns_in_sql(text):
            if any(sub in col for sub in forbidden_substrings):
                hits.append(f"{path.name}: column `{col}` violates Story 6.10 invariant")

    assert not hits, "Resume-offset columns found:\n" + "\n".join(hits)
```

The regex is conservative. Future column-add migrations that have
legitimate reasons to mention resume in a name (e.g., `resumed_at`,
`resume_count` — both already in `processing_jobs`) are NOT matched
because the substring patterns require `_resume_offset` /
`_resume_position` / `_resume_at_sec`.

## 5. Property test — invariant under chaos

`pipeline/tests/property/test_resume_invariant.py`:

```python
"""Run synthetic transcribe workloads through chaos crash/resume cycles
and assert the invariant at every consistent read.

Property: at any point in time, for any job J that has progress,
    last(transcript_segments
         WHERE transcript_id = J.transcript_id ORDER BY end_sec).end_sec
    == processing_jobs.last_segment_end_sec WHERE id = J.id.
"""
from __future__ import annotations

import asyncio
import random
import pytest

from maktaba_pipeline.db.jobs import enqueue, Stage
from .chaos import SyntheticTranscribeStage, ChaosRunner


@pytest.mark.asyncio
async def test_invariant_after_crash_resume(db, video):
    """100 chaos cycles; assert invariant after each."""
    res = await enqueue(db, video_id=video.id, stage=Stage.TRANSCRIBE)

    runner = ChaosRunner(
        db=db,
        stage=SyntheticTranscribeStage(
            total_duration_sec=600.0,
            segment_dur_sec=2.0,
            crash_prob=0.05,
        ),
    )

    for cycle in range(100):
        await runner.run_until_paused_or_done(job_id=res.id)
        await assert_invariant(db, res.id)
        if await runner.is_done(res.id):
            break

    assert await runner.is_done(res.id)


async def assert_invariant(db, job_id: int) -> None:
    job = await db.fetchrow(
        "SELECT last_segment_end_sec FROM processing_jobs WHERE id = $1",
        job_id,
    )
    seg = await db.fetchrow(
        "SELECT end_sec FROM transcript_segments "
        " WHERE transcript_id IN ("
        "   SELECT t.id FROM transcripts t "
        "    WHERE t.video_id = (SELECT video_id FROM processing_jobs "
        "                          WHERE id = $1))"
        " ORDER BY end_sec DESC LIMIT 1",
        job_id,
    )

    if seg is None:
        # No segments yet → last_segment_end_sec must be 0.
        assert job["last_segment_end_sec"] == 0.0, (
            f"job {job_id}: no segments but last_segment_end_sec="
            f"{job['last_segment_end_sec']}"
        )
    else:
        # The two values must agree exactly.
        assert job["last_segment_end_sec"] == seg["end_sec"], (
            f"job {job_id}: invariant violated. "
            f"job.last_segment_end_sec={job['last_segment_end_sec']}, "
            f"max(transcript_segments.end_sec)={seg['end_sec']}"
        )
```

The `ChaosRunner` and `SyntheticTranscribeStage` come from a fixture
module:

```python
# pipeline/tests/property/chaos.py (excerpt)
import asyncio
import random


class SyntheticTranscribeStage:
    """A fake transcribe stage that emits segments and randomly crashes."""

    def __init__(self, *, total_duration_sec, segment_dur_sec, crash_prob):
        self.total = total_duration_sec
        self.dur = segment_dur_sec
        self.crash = crash_prob
        self.cursor = 0.0   # current "audio time"

    async def transcribe_one_segment(self) -> tuple[float, float, str] | None:
        """Returns (start, end, text) or None if past end. Raises if crash."""
        if random.random() < self.crash:
            raise RuntimeError("synthetic crash")
        if self.cursor >= self.total:
            return None
        end = min(self.cursor + self.dur, self.total)
        seg = (self.cursor, end, f"segment at {self.cursor:.1f}")
        self.cursor = end
        return seg


class ChaosRunner:
    """Runs the synthetic stage in the canonical per-segment-commit pattern.

    On crash, records the current state and restarts from
    last_segment_end_sec — exactly as a real worker would after the
    reaper paused it (Story 6.6).
    """

    def __init__(self, db, stage):
        self.db = db
        self.stage = stage

    async def run_until_paused_or_done(self, *, job_id: int) -> None:
        # Read the canonical resume offset.
        cur = await self.db.fetchrow(
            "SELECT last_segment_end_sec FROM processing_jobs WHERE id = $1",
            job_id,
        )
        self.stage.cursor = cur["last_segment_end_sec"]

        while True:
            try:
                seg = await self.stage.transcribe_one_segment()
            except RuntimeError:
                # Crash → emulate reaper. Pause at last_segment_end_sec.
                await self.db.execute(
                    "UPDATE processing_jobs "
                    "   SET state='paused', paused_at_sec=last_segment_end_sec "
                    " WHERE id=$1", job_id,
                )
                return

            if seg is None:
                await self.db.execute(
                    "UPDATE processing_jobs SET state='done' WHERE id=$1", job_id,
                )
                return

            start, end, text = seg
            async with self.db.transaction():
                await self.db.execute(
                    "INSERT INTO transcript_segments "
                    "(transcript_id, start_sec, end_sec, text) "
                    "VALUES ((SELECT id FROM transcripts WHERE video_id="
                    " (SELECT video_id FROM processing_jobs WHERE id=$1)), "
                    " $2, $3, $4)",
                    job_id, start, end, text,
                )
                await self.db.execute(
                    "UPDATE processing_jobs SET last_segment_end_sec=$1 "
                    " WHERE id=$2", end, job_id,
                )

    async def is_done(self, job_id: int) -> bool:
        return (await self.db.fetchval(
            "SELECT state FROM processing_jobs WHERE id=$1", job_id,
        )) == "done"
```

The runner enforces the architecture §7.6 transaction semantic
explicitly: segment INSERT and `last_segment_end_sec` UPDATE inside
one transaction. The chaos crash always lands either before the INSERT
(no segment, no offset advance) or after the commit (segment present,
offset matches). The invariant therefore holds after every crash.

## 6. Test plan summary

| Test | Where | What it pins |
|---|---|---|
| `test_check_constraint_rejects_negative` | `tests/db/test_resume_invariant_checks.py` | DB rejects negative offsets. |
| `test_check_constraint_rejects_beyond_total_duration` | same | DB rejects offsets > total. |
| `test_no_sidecar_checkpoint_strings` | `tests/lint/test_no_sidecar_checkpoint_files.py` | No `partial.json`, `checkpoint`, `_resume` patterns in pipeline/. |
| `test_no_resume_offset_column_in_any_table` | `tests/lint/test_no_resume_offset_columns.py` | No `*_resume_offset` columns in any migration. |
| `test_invariant_after_crash_resume` | `tests/property/test_resume_invariant.py` | After 100 chaos cycles, the invariant holds at every consistent read. |
| `test_invariant_after_force_pause` | same | Force-pause path also preserves the invariant (covered by Epic 3 Story 3.7's tests; cross-referenced here). |

## 7. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Backend emits a segment whose `end_sec` exceeds `total_duration_seconds` | The per-segment commit clamps to `total_duration_seconds` (Epic 3 Story 3.6's edge cases). The CHECK constraint enforces the clamp at the DB layer; an over-shoot fails the INSERT. | Architecture §7.6 + this CHECK. |
| Migration adds a new column related to resume | The grep lint test `test_no_resume_offset_column_in_any_table` fails CI; the author either renames the column to one that doesn't trigger or, if there's a legitimate need (extremely rare), updates this lint to add an explicit allowlist with a referenced ADR. | `test_no_resume_offset_column_in_any_table` |
| Documentation mentions "resume offset" | The lint matches column names, not free-text mentions. Documentation can discuss the concept freely. | Pattern only matches `_resume_offset` style identifiers. |
| Test fixtures want to write a `partial.json` for unrelated reasons | Add `# resume-invariant-ok: test fixture, not a checkpoint` on the line; the lint allowlist marker exempts it. Real production code should never need this exception. | `ALLOWLIST_MARKER` |
| Race between crash, reaper pause, and a stale UPDATE | The reaper sets `paused_at_sec = last_segment_end_sec` atomically; a stale UPDATE from a dying worker that lost the SELECT-then-UPDATE race is rejected because of the per-segment `state IN (live)` predicate (Story 6.3). | Story 6.6 + 6.3 cooperation; covered by `test_invariant_after_crash_resume`. |
| Subtitle generator (a downstream stage) wants to remember "where it left off" mid-render | Out of scope: subtitle_gen is short-lived and re-runs from scratch on resume (architecture §7.9). It doesn't need a checkpoint. If the design ever changes, the new offset belongs as a column on `processing_jobs`, not in a sidecar. | Documented in Story 6.10's spec. |

## 8. Performance analysis

The CHECK constraint is evaluated on every INSERT/UPDATE; cost is two
comparisons + one COALESCE — O(1), sub-µs.

The lint tests run in CI; full grep over `pipeline/` (~10K lines) takes
~50 ms. Property test runs in CI's slow-tests bucket; 100 chaos cycles
on synthetic 600 s audio with 2 s segments completes in ~10 s.

## 9. Dependencies

No new runtime deps. The property test uses `pytest`, `asyncio`,
`random` — all stdlib.

## 10. Acceptance checklist

**Schema**
- [ ] `processing_jobs.last_segment_end_sec` CHECK constraint (`>= 0` AND `<= COALESCE(total_duration_seconds, last_segment_end_sec)`) is enforced (Story 6.1's migration).
- [ ] No other table has a `*_resume_offset` column.

**Lints**
- [ ] `test_no_sidecar_checkpoint_strings` passes (no sidecar references in `pipeline/src/`).
- [ ] `test_no_resume_offset_column_in_any_table` passes.

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_invariant_after_crash_resume` — chaos-kill loop preserves the invariant for every cycle.
- [ ] AC: `test_no_sidecar_checkpoint_files` — grep returns zero non-allowlisted hits.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.10.
- [ ] The `processing_jobs` migration includes a SQL comment block on `last_segment_end_sec` declaring it the canonical resume offset and linking back to this plan.
- [ ] `pipeline/src/maktaba_pipeline/db/jobs.py` (the `Job` dataclass) carries a docstring on `last_segment_end_sec` explicitly stating: "Single source of truth for resume position. See specs/epics/06-job-queue/plan-06-10-resume-invariant.md."
