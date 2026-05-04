# Implementation Plan — Story 6.8 Graceful Shutdown Semantics

> Companion to [story-06-08-graceful-shutdown.md](story-06-08-graceful-shutdown.md).
> The story states *what* and *why*; this plan states *how*.
> The shutdown protocol is owned by [architecture.md §7.8](../../architecture.md);
> the per-segment cooperation point is in
> [Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Python (Pipeline). Signal handling lives in the worker process; the API is unaware of shutdown semantics — it only sees the resulting `paused` rows. |
| Files | `pipeline/src/maktaba_pipeline/pipeline/shutdown.py` (signal trap + the orchestration), `pipeline/src/maktaba_pipeline/pipeline/runner.py` (modifications to wire the shutdown event), `pipeline/tests/pipeline/test_shutdown.py` (subprocess-based SIGTERM tests). |
| Schema dependency | `processing_jobs` Story 6.1; uses `claimed_by` to find this worker's jobs. |
| Out of scope | The reaper-driven path for `kill -9` (Story 6.6 owns that); per-stage cooperative pause inside the transcribe loop (Epic 3 Story 3.6 owns the cooperation; this story only triggers it). |

## 1. Architecture diagram

```
                           ┌────────────────────────┐
                           │ POSIX SIGTERM / SIGINT │
                           └───────────┬────────────┘
                                       ▼
            ┌──────────────────────────────────────────────────┐
            │ install_shutdown_handlers(worker)                │
            │   loop.add_signal_handler(SIGTERM, _on_signal)   │
            │   loop.add_signal_handler(SIGINT,  _on_signal)   │
            │                                                  │
            │   _on_signal:                                    │
            │     if shutdown_event.is_set():                  │
            │       _force_exit()                              │
            │     else:                                        │
            │       shutdown_event.set()                       │
            └──────────────────┬───────────────────────────────┘
                               ▼
            ┌──────────────────────────────────────────────────┐
            │ ShutdownOrchestrator.run()                       │
            │                                                  │
            │  Step 1. Stop the claim loop                     │
            │   (it observes shutdown_event between iterations) │
            │                                                  │
            │  Step 2. UPDATE processing_jobs                  │
            │            SET pause_requested=true              │
            │          WHERE claimed_by = $worker_id           │
            │            AND state IN ('claimed','running',     │
            │                          'resuming')             │
            │          (no NOTIFY needed — the in-process       │
            │          stage handler is the sole consumer)      │
            │                                                  │
            │  Step 3. Wait up to shutdown_grace_sec (120 s)   │
            │          for those rows to reach 'paused'.        │
            │   - poll every 1 s (cheap PK scan)                │
            │   - exit early when count == 0                    │
            │                                                  │
            │  Step 4. For any row still in claimed/running/    │
            │          resuming, force-pause it:                │
            │            UPDATE … SET state='paused',           │
            │              paused_at=now(),                     │
            │              paused_at_sec=last_segment_end_sec,  │
            │              paused_reason='shutdown',            │
            │              pause_requested=false,               │
            │              claimed_by=NULL                      │
            │            WHERE claimed_by = $worker_id          │
            │              AND state IN (live)                  │
            │            (this is exactly Story 6.4's force-     │
            │             pause UPDATE, applied per worker)     │
            │                                                  │
            │  Step 5. Cancel in-flight asyncio tasks; close   │
            │          DB pool; exit 0.                        │
            └──────────────────────────────────────────────────┘

                                Second SIGTERM/SIGINT?
                                       │
                                       ▼
            ┌──────────────────────────────────────────────────┐
            │ _force_exit():                                   │
            │   - cancel all running tasks                     │
            │   - log WARN "shutdown_forced_by_second_signal"  │
            │   - os._exit(130)   # 128 + SIGINT               │
            │                                                  │
            │   Reaper (Story 6.6) handles cleanup of any      │
            │   rows still in claimed/running/resuming after   │
            │   force-exit, within stale_claim_sec (90 s).     │
            └──────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/shutdown.py` | `ShutdownOrchestrator`, `install_shutdown_handlers`, the orchestration. |
| `pipeline/tests/pipeline/test_shutdown.py` | Loop-level tests with synthetic stages. |
| `pipeline/tests/integration/test_shutdown_subprocess.py` | Real `SIGTERM` tests via `subprocess.Popen` + `os.kill`. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | `Worker.run()` calls `install_shutdown_handlers(self)` and awaits the orchestrator on shutdown. |
| `pipeline/src/maktaba_pipeline/cli.py` | `run-worker` command sets up the signal handlers via `Worker`. |
| `pipeline/src/maktaba_pipeline/config.py` | Adds `WorkersConfig.shutdown_grace_sec = 120.0`. |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/pipeline/shutdown.py
from __future__ import annotations

import asyncio
import logging
import os
import signal


log = logging.getLogger(__name__)


class ShutdownOrchestrator:
    def __init__(
        self,
        db,
        *,
        worker_id: str,
        grace_sec: float = 120.0,
        poll_sec: float = 1.0,
    ):
        self.db = db
        self.worker_id = worker_id
        self.grace_sec = grace_sec
        self.poll_sec = poll_sec
        self.shutdown_event = asyncio.Event()
        self._signal_count = 0
        self._loop = asyncio.get_event_loop()

    def install(self) -> None:
        for sig in (signal.SIGTERM, signal.SIGINT):
            self._loop.add_signal_handler(sig, self._on_signal, sig)

    def _on_signal(self, sig: signal.Signals) -> None:
        self._signal_count += 1
        log.info("shutdown_signal_received",
                 extra={"signal": sig.name, "count": self._signal_count})
        if self._signal_count >= 2:
            self._force_exit(sig)
            return
        self.shutdown_event.set()

    def _force_exit(self, sig: signal.Signals) -> None:
        log.warning("shutdown_forced_by_second_signal",
                    extra={"signal": sig.name})
        # The reaper (Story 6.6) cleans up any orphaned claims within
        # stale_claim_sec.
        for task in asyncio.all_tasks(self._loop):
            task.cancel()
        # os._exit, not sys.exit — we don't want atexit handlers running
        # the orchestration logic.
        os._exit(130)

    async def run_after_signal(self) -> None:
        """Called by Worker.run() after shutdown_event fires."""
        await self.shutdown_event.wait()
        log.info("shutdown_orchestration_started",
                 extra={"worker_id": self.worker_id,
                        "grace_sec": self.grace_sec})

        # Step 2: ask all our claimed jobs to pause cooperatively.
        result = await self.db.execute(
            "UPDATE processing_jobs "
            "   SET pause_requested = true "
            " WHERE claimed_by = $1 "
            "   AND state IN ('claimed', 'running', 'resuming')",
            self.worker_id,
        )
        # asyncpg's execute returns "UPDATE N"; parse for the count.
        n = _parse_update_count(result)
        log.info("shutdown_pause_requested_for_in_flight",
                 extra={"count": n, "worker_id": self.worker_id})

        # Step 3: poll until count == 0 or grace_sec elapses.
        deadline = self._loop.time() + self.grace_sec
        while True:
            remaining = await self.db.fetchval(
                "SELECT count(*) FROM processing_jobs "
                " WHERE claimed_by = $1 "
                "   AND state IN ('claimed', 'running', 'resuming')",
                self.worker_id,
            )
            if remaining == 0:
                log.info("shutdown_all_paused_cleanly",
                         extra={"worker_id": self.worker_id})
                return
            if self._loop.time() >= deadline:
                break
            await asyncio.sleep(self.poll_sec)

        # Step 4: force-pause the stragglers.
        forced = await self.db.fetch(
            "UPDATE processing_jobs "
            "   SET state            = 'paused', "
            "       paused_at        = now(), "
            "       paused_at_sec    = last_segment_end_sec, "
            "       paused_reason    = 'shutdown', "
            "       pause_requested  = false, "
            "       claimed_by       = NULL "
            " WHERE claimed_by = $1 "
            "   AND state IN ('claimed', 'running', 'resuming') "
            "RETURNING id",
            self.worker_id,
        )
        log.warning("shutdown_force_paused_after_grace",
                    extra={"count": len(forced), "worker_id": self.worker_id,
                           "ids": [r["id"] for r in forced]})


def _parse_update_count(tag: str) -> int:
    # asyncpg returns "UPDATE N"; SQLAlchemy returns a CursorResult with rowcount.
    if isinstance(tag, str) and tag.startswith("UPDATE "):
        return int(tag.split()[1])
    return getattr(tag, "rowcount", 0)
```

### 2.4 Wiring into `Worker.run`

```python
# pipeline/src/maktaba_pipeline/pipeline/runner.py (modifications)
class Worker:
    def __init__(self, cfg, db):
        ...
        self.shutdown = ShutdownOrchestrator(
            db, worker_id=cfg.worker_id,
            grace_sec=cfg.shutdown_grace_sec,
        )
        self.claim_loop = ClaimLoop(
            db=db, cfg=cfg, dispatch=self._dispatch,
            semaphores=self.concurrency.stage_semaphores,
            shutdown_event=self.shutdown.shutdown_event,
        )

    async def run(self) -> None:
        self.shutdown.install()
        loop_task = asyncio.create_task(self.claim_loop.run(), name="claim-loop")
        try:
            await self.shutdown.run_after_signal()
        finally:
            # ClaimLoop's `run` already exits when shutdown_event is set;
            # await its task to drain.
            await loop_task
            await self.db.close()
        log.info("worker_exit", extra={"worker_id": self.cfg.worker_id})
```

The order matters: orchestration UPDATE runs before we wait on
`loop_task` so the in-flight stage handlers see `pause_requested=true`
on their next per-segment check.

## 3. Test plan

### 3.1 In-process tests (`pipeline/tests/pipeline/test_shutdown.py`)

These use a synthetic stage handler that polls `pause_requested` itself
(no real subprocess) and a fake DB driver — fast and deterministic.

| Test | What it pins |
|---|---|
| `test_orchestrator_sets_pause_requested_for_claimed_jobs` | Insert 3 rows with `claimed_by='w1'` in state running; trigger shutdown event; assert orchestrator's UPDATE marked all 3 with `pause_requested=true`. |
| `test_orchestrator_waits_for_paused` | Synthetic stage handler that flips the row to `paused` after 0.2 s; orchestrator returns within 0.5 s, no force-pause UPDATE fires. |
| `test_orchestrator_force_pauses_after_grace` | Synthetic handler that ignores pause; grace_sec=0.5; orchestrator force-pauses after 0.5 s with `paused_reason='shutdown'`. |
| `test_orchestrator_filters_by_worker_id` | Two workers' rows in DB; orchestrator only touches its own worker_id's rows. |
| `test_signal_handler_sets_event` | Send SIGTERM to the test process via `os.kill(os.getpid(), SIGTERM)`; assert `shutdown_event.is_set()` within 0.1 s. |
| `test_second_signal_calls_os_exit` | Patch `os._exit`; send two SIGTERMs in quick succession; assert `os._exit(130)` was called. |
| `test_orchestrator_logs_force_pause_count_and_ids` | Capture logs; force-pause path logs WARN with `count` and `ids` fields. |

### 3.2 Subprocess tests (`pipeline/tests/integration/test_shutdown_subprocess.py`)

These boot a real `python -m maktaba_pipeline run-worker` subprocess
and verify the end-to-end behaviour with real signals.

| Test | What it pins |
|---|---|
| `test_shutdown_pauses_all_claims_real_sigterm` | Start worker with two enqueued synthetic-transcribe jobs; wait for both to reach `running`; `os.kill(pid, SIGTERM)`; assert both rows reach `paused` with `paused_reason in ('user', 'shutdown')` within grace + 5 s tolerance. (`'user'` if the cooperative path won; `'shutdown'` if the orchestrator's force-pause UPDATE wrote the row instead.) |
| `test_shutdown_force_pauses_after_grace_real` | Use a synthetic stage that ignores `pause_requested` (sleeps 60 s); set `shutdown_grace_sec=2`; SIGTERM; after ≥ 2 s the row is `paused` with `paused_reason='shutdown'`; subprocess exits within 5 s. |
| `test_no_orphan_after_kill_minus_nine` | `os.kill(pid, SIGKILL)`; row stays in `running` (or `claimed`); start a reaper instance with `stale_claim_sec=2`; wait 3 s; assert row reaped to `paused` with `paused_reason='crash'`. |
| `test_two_sigterms_force_immediate_exit` | SIGTERM, then SIGTERM 50 ms later; subprocess exits with code 130 within 0.5 s; row may be in `running` (orchestrator didn't get to force-pause); reaper sweeps it later. |
| `test_grace_window_doesnt_extend_indefinitely` | Synthetic stage that ALWAYS sleeps; `shutdown_grace_sec=1`; SIGTERM; subprocess exits within 1 + 2 s budget (grace + cleanup). |

The subprocess tests use a synthetic stage runner whose sole purpose is
to be controllable: it reads a TOML knob `[test_stage].sleep_sec` and
either honours pause flags promptly or ignores them. The fixture
fragment:

```python
@pytest.fixture
def worker_subprocess(tmp_path):
    cfg = tmp_path / "pipeline.toml"
    cfg.write_text("""
        [database]
        url = "sqlite:///%s"
        [workers]
        concurrency = { transcribe = 2 }
        heartbeat_sec = 1
        shutdown_grace_sec = 2
    """ % (tmp_path / "test.db"))

    proc = subprocess.Popen(
        [sys.executable, "-m", "maktaba_pipeline", "run-worker",
         "--config", str(cfg), "--stages", "transcribe"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    try:
        yield proc
    finally:
        if proc.poll() is None:
            proc.kill()
        proc.wait(timeout=5)
```

## 4. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Two `SIGTERM`s in quick succession | Second handler invocation calls `_force_exit(SIGTERM)`; `os._exit(130)` runs immediately; reaper sweeps any orphans. | `test_two_sigterms_force_immediate_exit` |
| Container orchestrator's TERM-then-KILL window shorter than `shutdown_grace_sec` | Document in `docker-compose.yml` and Kubernetes manifests: set `stop_grace_period: shutdown_grace_sec + 30s` (i.e., 150 s default). If the container is killed before the orchestrator finishes, the reaper handles cleanup within `stale_claim_sec`. | `pipeline.toml` comment + deployment docs in `specs/architecture.md §12.3`. |
| `kill -9` (SIGKILL) | The signal handler never runs; the process dies; rows stay in `claimed`/`running`/`resuming` until the reaper sweeps them after `stale_claim_sec` (Story 6.6). | `test_no_orphan_after_kill_minus_nine` |
| Worker crashes mid-orchestration (e.g., DB connection drops during the UPDATE in step 2) | Orchestrator's UPDATE rolls back; some rows may have `pause_requested=true` (committed by the API previously) but state still `running`. The cooperative path still works on those rows; the reaper handles the rest. | Inherent from the SQL transaction model. |
| Worker has zero claimed jobs at SIGTERM | Step 2's UPDATE returns 0; step 3 returns immediately; step 4 finds no rows; orchestrator exits cleanly. | `test_shutdown_with_no_claims_exits_immediately` |
| Stage handler observes pause but its DB write fails | The handler retries via the asyncpg pool's transient-error retry; on second failure it raises, and the runner's exception path (Story 6.5) marks the job failed/retried. The orchestrator's force-pause then no-ops on the row (state is `pending` or `failed`, not in the live set). | Inherited from Story 6.5. |
| SIGTERM during the orchestrator's poll loop | Second SIGTERM force-exits per the standard rule; rows that were about to pause cooperatively now wait for the reaper. | `test_two_sigterms_during_orchestration_force_exits` |
| Misconfigured `shutdown_grace_sec=0` | Step 3 returns immediately on first poll; force-pause runs without giving handlers a chance. Allowed; the operator chose this. | Documented; not tested explicitly. |
| `claimed_by` mismatch (worker_id changed across restart) | A re-launched worker uses a new `worker_id` (includes pid + uuid). Old `claimed_by` rows are not its problem; the reaper cleans those. | `WorkerConfig.worker_id` includes `os.getpid()` and a fresh `uuid4`. |

## 5. Performance analysis

The orchestrator's UPDATE matches by `claimed_by` — uses no index on
that column today, so it does a full scan. Acceptable: a worker holds
at most a handful of jobs (per-stage caps); the table's `claimed_by`
selectivity in the live-states partial range is high. If profiling
shows this dominates shutdown time on huge tables, add
`CREATE INDEX ... ON processing_jobs (claimed_by) WHERE state IN
('claimed','running','resuming')` in a follow-up migration.

Poll cost: one `count(*)` per second; negligible.

## 6. Dependencies

No new deps. Signal handling is stdlib `signal` + `asyncio.Loop.add_signal_handler`.

## 7. Acceptance checklist

**Code**
- [ ] `ShutdownOrchestrator.install()` registers SIGTERM and SIGINT handlers via `loop.add_signal_handler`.
- [ ] First signal sets `shutdown_event`; second signal calls `os._exit(130)`.
- [ ] Orchestrator's step 2 UPDATE is `WHERE claimed_by = $worker_id AND state IN (live)`.
- [ ] Step 4 force-pause UPDATE writes `paused_reason='shutdown'`.

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_shutdown_pauses_all_claims_real_sigterm` — both jobs reach `paused` within grace.
- [ ] AC: `test_shutdown_force_pauses_after_grace_real` — synthetic ignore-pause stage forced after 5 s.
- [ ] AC: `test_no_orphan_after_kill_minus_nine` — reaper sweeps within `stale_claim_sec`.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.8.
- [ ] `pipeline.toml` documents `shutdown_grace_sec` default (120 s) and the operator guidance to set Compose `stop_grace_period >= shutdown_grace_sec + 30s`.
- [ ] `architecture.md §7.8`'s second-SIGTERM behaviour is cross-linked to this plan.
