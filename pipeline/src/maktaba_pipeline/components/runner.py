"""The pipeline's component runner (Epic 51, story 51.06): stage groups stop without
losing work, and the pipeline's own loops follow their components.

One runner per pipeline process (the main worker, each ``stt-N``, the index worker),
started by ``runtime.py``. It reads desired state with the 51.02 store (``LISTEN
components_changed`` on Postgres, and a revision poll every :data:`~.store.POLL_S`
seconds: when nothing changes that poll is its only query), and on every change:

1. computes each component's gate (:mod:`.stages`);
2. gives the closed stage groups' stages to this worker's ``StageSelector`` at once
   (``component_off:<id>`` / ``component_held:<id>``), so it stops claiming them;
3. drains this worker's jobs of the stages that just stopped (``drain_stages``: ask them
   to pause, wait the catalog's ``drain.grace_s``, force-pause the rest and cancel them;
   no job fails, no attempt is counted);
4. on the home, as the leader (an advisory lock on Postgres), writes those stages'
   ``job_stages`` rows (``component_off`` / ``component_held``) so that no machine claims
   them, puts them back when the component returns, and then queues one
   ``component_catchup`` job for what the stages missed while off;
5. starts and stops the pipeline loops of :data:`PIPELINE_LOOPS` (each with its own stop
   event and the catalog's stop grace);
6. reports its ``component_status`` rows (part ``pipeline`` or ``pipeline/<instance>``).

``MAKTABA_COMPONENTS_RECONCILE=off`` (``[components] reconcile = off``): it computes and
reports (reason ``reconcile_off``) but changes nothing, and every loop runs as before.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
from collections.abc import Awaitable, Callable, Collection, Coroutine, Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from ..engine.graph import CATALOG_ID, StageDef
from ..engine.sql import is_pg, now, parse_ts
from ..engine.sync import COMPONENT_OFF, apply_gates
from ..log import get_logger
from ..log.tasks import watch as watch_task
from . import store
from .catalog import DEFAULT_STOP_GRACE_S, Catalog, default
from .stages import (
    HELD,
    Gate,
    extension_name,
    gates,
    home_reasons,
    machine_reasons,
    stage_components,
)
from .store import HOME, Condition, Desired, Status

__all__ = [
    "LEADER_KEY",
    "PIPELINE_LOOPS",
    "ComponentRunner",
    "gate_open",
    "instance_part",
    "reconcile_enabled",
]

_log = get_logger()

#: every loop the pipeline runs, by its catalog name (``pipeline:<name>``), and the
#: component it belongs to. ``core.pipeline``'s are declared and never stopped; the others
#: run while their component's gate is open. The catalog job compares this table with the
#: catalog (tests/components/test_pipeline_loops.py), the pipeline half of F1.
PIPELINE_LOOPS: dict[str, str] = {
    "worker-hb": "core.pipeline",
    "stage-selection": "core.pipeline",
    "reconciler": "core.pipeline",
    "pruner": "core.pipeline",
    "audio-cache-sweep": "core.pipeline",
    "claim-lease": "core.pipeline",
    "index-close": "core.pipeline",
    "channel-topup": "livetv",
    "queue-health": "queue_health",
    "health": "cluster",
    "cluster-agent": "cluster",
}

#: the home's stage gates are written by one worker at a time
LEADER_KEY = "components_stage_gates"

LoopFn = Callable[[asyncio.Event], Coroutine[Any, Any, None]]

_ACTIVE: ComponentRunner | None = None


def reconcile_enabled() -> bool:
    """False when ``MAKTABA_COMPONENTS_RECONCILE`` says off (the supervisor sets it from
    ``[components] reconcile``): runners report but act on nothing."""
    v = os.environ.get("MAKTABA_COMPONENTS_RECONCILE", "").strip().lower()
    return v not in ("off", "0", "false", "no")


def gate_open(component: str) -> bool:
    """Whether this process's runner has ``component`` on (True with no runner, or with
    the reconciler off): for code that serves a component outside a stage (the
    in-process semantic search)."""
    r = _ACTIVE
    if r is None or not r.reconcile:
        return True
    g = r.gates.get(component)
    return g is None or g.open


@dataclass
class _Loop:
    name: str
    component: str
    fn: LoopFn
    grace: float
    #: an environment variable keeps it off on this machine (``env_override``)
    env_off: bool = False
    task: asyncio.Task[None] | None = None
    stop: asyncio.Event | None = None
    #: "" or the reason it is failed (``stop_timeout``)
    failed: str = ""

    def running(self) -> bool:
        return self.task is not None and not self.task.done()

    def state(self) -> str:
        if self.failed:
            return "failed"
        if self.running():
            return "stopping" if self.stop is not None and self.stop.is_set() else "running"
        return "stopped"


class ComponentRunner:
    """See the module docstring. ``apply`` is the worker's ``WorkerAgent.apply`` (it
    recomputes the selection and writes it to the registry); ``claims`` (set once the claim
    loop exists) cancels a drained stage's jobs; ``paused`` (a node whose home's schema
    does not fit, or that cannot reach the home) skips the passes."""

    def __init__(
        self,
        db: Any,
        *,
        machine: str = HOME,
        part: str = "pipeline",
        role: str = "home",
        graph: Sequence[StageDef] = (),
        selector: Any = None,
        apply: Callable[[], Awaitable[Any]] | None = None,
        worker_id: str = "",
        catalog: Catalog | None = None,
        reconcile: bool | None = None,
        poll_s: float = store.POLL_S,
        version: str = "",
        extension_states: Callable[[], Awaitable[Mapping[str, Any]]] | None = None,
        paused: Callable[[], bool] | None = None,
        drain_poll_s: float = 1.0,
    ) -> None:
        self.db = db
        self.machine = machine or HOME
        self.part = part
        self.role = role
        self.graph = tuple(graph)
        self.selector = selector
        self._apply_selection = apply
        self.worker_id = worker_id
        self.catalog = catalog or default()
        self.reconcile = reconcile_enabled() if reconcile is None else reconcile
        self.poll_s = poll_s
        self.version = version
        self._extension_states = extension_states
        self._paused = paused
        self.drain_poll_s = drain_poll_s
        self.claims: Any = None
        self.gates: dict[str, Gate] = {}
        self.home_gates: dict[str, Gate] = {}
        self.revision = -1
        self.loops: dict[str, _Loop] = {}
        self._drains: dict[str, asyncio.Task[dict[str, int]]] = {}
        self._stoppers: set[asyncio.Task[None]] = set()
        #: component -> when the leader turned its stages off (None: unknown)
        self._off_since: dict[str, datetime | None] = {}
        self._off_loaded = False
        #: the built-in extensions' rows (name -> ExtensionState), read at each pass that
        #: acts (story 51.07: an enabled extension without its settings is Waiting)
        self.ext_states: Mapping[str, Any] = {}
        self._reported: list[Status] | None = None
        self._wake = asyncio.Event()
        self._listener: Any = None
        self._graph_names = {s.name for s in self.graph}

    # -- declaring -------------------------------------------------------------

    def declare(self, name: str, fn: LoopFn, *, env_off: bool = False) -> None:
        """A pipeline loop this process runs (its name in :data:`PIPELINE_LOOPS` and the
        catalog). It starts with the runner's first pass when its component is on."""
        comp = PIPELINE_LOOPS.get(name)
        cat = self.catalog.by_loop("pipeline", name)
        if comp is None or cat is None or cat.id != comp:
            raise ValueError(f"pipeline loop {name!r} is not in the catalog as {comp!r}")
        c = self.catalog.get(comp)
        grace = float(c.stop_grace_s or DEFAULT_STOP_GRACE_S) if c is not None else 5.0
        self.loops[name] = _Loop(name, comp, fn, grace, env_off=env_off)

    # -- reading ---------------------------------------------------------------

    async def _desired(self) -> tuple[dict[str, Desired], int]:
        return await store.desired(self.db, self.machine)

    async def prime(self) -> dict[str, str] | None:
        """Before the worker's first selection and graph sync: read desired state, set
        this machine's stage gates on the selector, and answer the home's stage gates for
        ``sync_graph`` (None on a node, with the reconciler off, or when desired state
        cannot be read: the sync then leaves the component reasons as they are)."""
        global _ACTIVE
        _ACTIVE = self
        try:
            desired, rev = await self._desired()
        except Exception as exc:  # noqa: BLE001 - an older schema: today's behaviour
            _log.warning("components_desired_unreadable", error=f"{type(exc).__name__}: {exc}")
            return None
        self.revision = rev
        self.gates = gates(desired, self.catalog)
        self.home_gates = self.gates if self.machine == HOME else {}
        if not self.reconcile:
            return None
        if self.selector is not None:
            self.selector.components = self.machine_reasons()
        if self.role != "home" or self.machine != HOME:
            return None
        return home_reasons(self.home_gates, self.catalog)

    def machine_reasons(self) -> dict[str, str]:
        return {
            st: why
            for st, why in machine_reasons(self.gates, self.catalog).items()
            if st in self._graph_names
        }

    # -- the loop --------------------------------------------------------------

    async def run(self, stop: asyncio.Event) -> None:
        """Pass at once, then on every notification and whenever the revision moved;
        returns when ``stop`` is set, after stopping every loop it started."""
        global _ACTIVE
        _ACTIVE = self
        try:
            await self._pass(force=True)
            while not stop.is_set():
                await self._listen()
                waiter = asyncio.ensure_future(self._wake.wait())
                stopper = asyncio.ensure_future(stop.wait())
                done, pending = await asyncio.wait(
                    {waiter, stopper}, timeout=self.poll_s, return_when=asyncio.FIRST_COMPLETED
                )
                for t in pending:
                    t.cancel()
                if stop.is_set():
                    break
                woken = self._wake.is_set()
                self._wake.clear()
                await self._pass(force=woken)
        finally:
            await self.close()

    async def _listen(self) -> None:
        """LISTEN on Postgres (a connection of its own, opened again after it closed; a
        reconnect is a reload). SQLite has no NOTIFY: the poll alone."""
        if not is_pg(self.db) or not hasattr(self.db, "acquire_listener"):
            return
        conn = self._listener
        if conn is not None and not conn.is_closed():
            return
        try:
            conn = await self.db.acquire_listener()
            await conn.add_listener(store.CHANNEL, self._on_notify)
        except Exception as exc:  # noqa: BLE001 - the poll still sees every change
            _log.warning("components_listen_failed", error=f"{type(exc).__name__}: {exc}")
            self._listener = None
            return
        reconnect = self._listener is not None
        self._listener = conn
        if reconnect:
            self._wake.set()

    def _on_notify(self, *args: Any) -> None:
        payload = str(args[-1]) if args else ""
        ev = store.parse_notify(payload)
        if ev.reload or ev.revision != self.revision:
            self._wake.set()

    async def _pass(self, *, force: bool) -> None:
        if self._paused is not None and self._paused():
            return
        try:
            if not force:
                rev = await store.revision(self.db)
                if rev == self.revision:
                    return
            desired, rev = await self._desired()
            await self.apply(desired, rev)
        except Exception:  # noqa: BLE001 - the next pass tries again
            self.revision = -1
            _log.exception("components_pass_failed", part=self.part)

    # -- one pass ----------------------------------------------------------------

    async def apply(self, desired: Mapping[str, Desired], rev: int) -> None:
        """Act on ``desired`` (this machine's merged desired state, read at ``rev``)."""
        self.revision = rev
        self.gates = gates(desired, self.catalog)
        if self.machine == HOME:
            self.home_gates = self.gates
        await self._read_extensions()
        if self.reconcile:
            await self._stage_gates()
            if self.role == "home" and self.machine == HOME:
                await self._lead()
        self._loops()
        await self._report()

    async def _read_extensions(self) -> None:
        if self._extension_states is None:
            return
        try:
            self.ext_states = await self._extension_states()
        except Exception as exc:  # noqa: BLE001 - the last read stands
            _log.warning("components_extensions_unreadable", error=f"{type(exc).__name__}: {exc}")

    async def _stage_gates(self) -> None:
        reasons = self.machine_reasons()
        for cid, task in list(self._drains.items()):
            g = self.gates.get(cid)
            if g is not None and g.open and not task.done():
                # on again within the grace: the jobs asked to pause carry on
                task.cancel()
        if self.selector is None:
            return
        before = set(self.selector.served)
        self.selector.components = reasons
        if self._apply_selection is not None:
            await self._apply_selection()
        else:
            self.selector.compute()
        left = before - set(self.selector.served)
        by_comp: dict[str, set[str]] = {}
        for st in left:
            why = reasons.get(st)
            if why is None:
                continue
            by_comp.setdefault(why.partition(":")[2], set()).add(st)
        for cid, stages in by_comp.items():
            self._start_drain(
                cid, stages, held=reasons[next(iter(stages))].startswith("component_held")
            )

    def _start_drain(self, cid: str, stages: set[str], *, held: bool) -> None:
        from ..pipeline.shutdown import drain_stages

        old = self._drains.get(cid)
        if old is not None and not old.done():
            return
        c = self.catalog.get(cid)
        grace = float(c.drain.grace_s) if c is not None else 30.0
        stopper = getattr(self.claims, "stop_stages", None)
        task = asyncio.ensure_future(
            drain_stages(
                self.db,
                worker_id=self.worker_id,
                stages=stages,
                grace_sec=grace,
                reason="component_held" if held else COMPONENT_OFF,
                poll_sec=self.drain_poll_s,
                cancel=stopper,
            )
        )
        self._drains[cid] = watch_task(task)
        task.add_done_callback(lambda _t: self._wake.set())

    def draining(self, cid: str) -> bool:
        t = self._drains.get(cid)
        return t is not None and not t.done()

    async def wait_drains(self) -> None:
        """Wait for the drains in progress (tests)."""
        for t in list(self._drains.values()):
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await t

    # -- the home's leader ---------------------------------------------------------

    async def _lead(self) -> None:
        if is_pg(self.db):
            from ..engine.leader import leader

            async with leader(self.db, LEADER_KEY) as got:
                if got:
                    await self._write_home()
            return
        await self._write_home()

    def _managed(self) -> list[str]:
        return [
            st for c in stage_components(self.catalog) for st in c.stages if st in self._graph_names
        ]

    async def _load_off_since(self) -> None:
        """The times this home's leaders turned components off, from their status rows (a
        restarted leader keeps them)."""
        if self._off_loaded:
            return
        self._off_loaded = True
        for s in await store.statuses(self.db):
            if s.machine != HOME or "off_since" not in s.detail:
                continue
            at = parse_ts(s.detail.get("off_since")) if s.detail.get("off_since") else None
            prev = self._off_since.get(s.id, at)
            self._off_since[s.id] = None if prev is None or at is None else min(prev, at)

    async def _write_home(self) -> None:
        from ..engine.enqueue import enqueue_job

        await self._load_off_since()
        reasons = home_reasons(self.home_gates, self.catalog)
        exts = self.ext_states
        managed = self._managed()
        t = now()
        # the components whose stages make no jobs now (off, not held: a held stage's
        # jobs queue and wait); each one's time off is kept until it is back
        closed: set[str] = set()
        for st, why in reasons.items():
            comp = self.catalog.by_stage(st)
            if comp is not None and why == COMPONENT_OFF and st in self._graph_names:
                closed.add(comp.id)
        for cid in closed:
            self._off_since.setdefault(cid, t)
        back = {cid: at for cid, at in self._off_since.items() if cid not in closed}
        async with self.db.transaction():
            changes = await apply_gates(self.db, self.graph, reasons, exts, stages=managed)
            if changes:
                _log.info(
                    "component_stage_gates_written",
                    stages={st: why for st, _a, _b, _c, why in changes},
                )
            if back:
                entries = [
                    {"component": cid, "off_since": None if at is None else at.isoformat()}
                    for cid, at in sorted(back.items())
                ]
                res = await enqueue_job(
                    self.db,
                    subject_kind="catalog",
                    subject_id=CATALOG_ID,
                    stage="component_catchup",
                    payload={"components": entries},
                    reason={"why": "components on again", "by": "components"},
                )
                _log.info("component_catchup_queued", components=entries, job_id=res.job_id)
        for cid in back:
            self._off_since.pop(cid, None)

    # -- loops ---------------------------------------------------------------

    def _loop_wanted(self, lp: _Loop) -> bool:
        if lp.env_off:
            return False
        if not self.reconcile:
            return True
        g = self.gates.get(lp.component)
        return g is None or g.open

    def start_loops(self) -> list[asyncio.Task[None]]:
        """Start the declared loops whose component is on (from :meth:`prime`'s read);
        the tasks started."""
        self._loops()
        return [lp.task for lp in self.loops.values() if lp.task is not None]

    def _loops(self) -> None:
        for lp in self.loops.values():
            want = self._loop_wanted(lp)
            if not want and lp.task is not None and lp.task.done():
                lp.task, lp.stop, lp.failed = None, None, ""
            # a loop that ended on its own (an error, or a one-shot) is not started again
            # until its component has been off and on: no restart spin
            ended = lp.task is not None and lp.task.done() and not (lp.stop and lp.stop.is_set())
            if want and not lp.running() and not lp.failed and not ended:
                lp.stop = asyncio.Event()
                lp.task = watch_task(asyncio.create_task(lp.fn(lp.stop), name=lp.name))
                lp.task.add_done_callback(lambda _t: self._wake.set())
                _log.info("component_loop_started", loop=lp.name, component=lp.component)
            elif not want and lp.running() and lp.stop is not None and not lp.stop.is_set():
                stopper = asyncio.ensure_future(self._stop_loop(lp))
                self._stoppers.add(stopper)
                stopper.add_done_callback(self._stoppers.discard)
            elif want and lp.failed and not lp.running():
                lp.failed = ""  # the stuck copy ended: it may start again next pass
                self._wake.set()

    async def _stop_loop(self, lp: _Loop) -> None:
        task, stop = lp.task, lp.stop
        if task is None or stop is None:
            return
        stop.set()
        try:
            await asyncio.wait_for(asyncio.shield(task), timeout=lp.grace)
        except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            lp.failed = "stop_timeout"
            _log.warning("component_loop_stop_timeout", loop=lp.name, grace_s=lp.grace)
            task.cancel()
        except Exception:  # noqa: BLE001, S110 - the loop logs its own failure (watch_task)
            pass
        else:
            _log.info("component_loop_stopped", loop=lp.name, component=lp.component)
        self._wake.set()

    # -- status --------------------------------------------------------------

    def _reported_ids(self) -> list[str]:
        requested = getattr(self.selector, "requested", None)
        ids: list[str] = []
        for c in stage_components(self.catalog):
            mine = [st for st in c.stages if st in self._graph_names]
            if not mine:
                continue
            if requested is not None and not any(st in requested for st in mine):
                continue
            ids.append(c.id)
        for lp in self.loops.values():
            owner = self.catalog.get(lp.component)
            if owner is not None and owner.switchable and lp.component not in ids:
                ids.append(lp.component)
        return sorted(ids)

    def status_rows(self) -> list[Status]:
        """This part's status, one row per component it serves or hosts a loop of."""
        out: list[Status] = []
        for cid in self._reported_ids():
            g = self.gates.get(cid) or Gate(cid, "on")
            loops = {lp.name: lp for lp in self.loops.values() if lp.component == cid}
            detail: dict[str, Any] = {}
            if loops:
                detail["loops"] = {n: lp.state() for n, lp in sorted(loops.items())}
            if cid in self._off_since:
                at = self._off_since[cid]
                detail["off_since"] = None if at is None else at.isoformat()
            conds: list[Condition] = []
            ext = self._extension_of(cid)
            if not self.reconcile:
                phase = "running"
                conds.append(Condition("Ready", "unknown", "reconcile_off"))
            elif any(lp.env_off for lp in loops.values()) and all(
                lp.env_off for lp in loops.values()
            ):
                phase = "off"
                conds.append(Condition("Ready", "false", "env_override"))
            elif any(lp.failed for lp in loops.values()):
                phase = "failed"
                conds.append(Condition("Ready", "false", "stop_timeout"))
            elif self.draining(cid) or any(lp.state() == "stopping" for lp in loops.values()):
                phase = "draining"
                conds.append(Condition("Ready", "false"))
            elif g.open and ext is not None and not ext.enabled:
                # the extensions row is off (a release before 51.07 wrote it alone, or the
                # mirror is being repaired): its stages are off as extension_disabled
                phase = "off"
                conds.append(Condition("Ready", "false"))
            elif g.open and ext is not None and not ext.configured:
                phase = "waiting"
                conds.append(Condition("Ready", "false", "not_configured"))
            elif g.open:
                phase = "running"
                conds.append(Condition("RequirementsMet", "true"))
                conds.append(Condition("Ready", "true"))
            elif g.reason == "dependency_off":
                phase = "waiting" if g.state != HELD else "stopped"
                conds.append(
                    Condition("RequirementsMet", "false", "dependency_off", dict(g.params))
                )
            elif g.state == HELD:
                phase = "stopped"
                conds.append(Condition("Ready", "false", "held"))
            elif g.want == "absent":
                phase = "not_installed"
                conds.append(Condition("Installed", "false"))
            else:
                phase = "off"
                conds.append(Condition("Ready", "false"))
            out.append(
                Status(
                    id=cid,
                    phase=phase,
                    conditions=tuple(conds),
                    observed_generation=g.generation,
                    detail=detail,
                )
            )
        return out

    def _extension_of(self, cid: str) -> Any:
        """The ExtensionState of a built-in extension component (None: not one, or no
        row read)."""
        c = self.catalog.get(cid)
        name = extension_name(c) if c is not None else None
        return self.ext_states.get(name) if name is not None else None

    async def _report(self) -> None:
        rows = self.status_rows()
        if rows == self._reported:
            return
        try:
            await store.report_status(
                self.db, self.machine, self.part, rows, version=self.version, catalog=self.catalog
            )
        except Exception as exc:  # noqa: BLE001 - an older schema, a busy database: next pass
            _log.warning("components_report_failed", error=f"{type(exc).__name__}: {exc}")
            return
        self._reported = rows

    # -- the end ---------------------------------------------------------------

    async def close(self) -> None:
        """Stop every loop it started (each within its grace) and every drain."""
        global _ACTIVE
        for lp in self.loops.values():
            if lp.stop is not None:
                lp.stop.set()
        for lp in self.loops.values():
            if lp.task is None:
                continue
            try:
                await asyncio.wait_for(asyncio.shield(lp.task), timeout=lp.grace)
            except BaseException:  # noqa: BLE001 - a stuck loop is cancelled
                lp.task.cancel()
                with contextlib.suppress(BaseException):
                    await lp.task
        for t in list(self._drains.values()) + list(self._stoppers):
            t.cancel()
        if self._listener is not None:
            with contextlib.suppress(Exception):
                await self._listener.close()
            self._listener = None
        if _ACTIVE is self:
            _ACTIVE = None


def instance_part(instance: str) -> str:
    """The status part of a pipeline process: ``pipeline`` or ``pipeline/<instance>``."""
    return "pipeline" if not instance else f"pipeline/{instance}"


def stages_of(components: Collection[str], cat: Catalog | None = None) -> list[str]:
    """Every stage of ``components``."""
    cat = cat or default()
    out: list[str] = []
    for cid in components:
        c = cat.get(cid)
        if c is not None:
            out.extend(c.stages)
    return out
